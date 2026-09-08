package harness

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/state"
)

// wakeNarrator requests a narrator turn. Loop goroutine only. If a turn is
// in flight the request is kept and honoured when it ends.
func (r *Runtime) wakeNarrator(reason string) {
	if r.ending && reason != wakeFinal {
		return
	}
	if r.narrBusy {
		if r.narrPending == "" || reason == wakeFinal || reason == wakeDone {
			r.narrPending = reason
		}
		return
	}
	r.narrPending = reason
}

// maybeWakeNarrator starts a narrator turn if one is pending and something
// new is visible. Loop goroutine only.
func (r *Runtime) maybeWakeNarrator() {
	if r.narrBusy || r.narrPending == "" {
		return
	}
	reason := r.narrPending
	r.narrPending = ""
	if reason != wakeFinal && reason != wakePeriodic && r.narrWorthy <= r.narrLastSeen {
		return // nothing new since it last looked (the tick decides for itself)
	}
	if (reason == wakeDone || reason == wakeYield) && r.narratorCoveredYield() {
		return // it saw the yield in the call that made it, and spoke; the yield event's own seq is not news
	}
	r.startNarratorTurn(reason)
}

// narratorCoveredYield reports that the narrator has both seen the latest
// yield (its cursor is at or past the seq where the yield became visible)
// and spoken since, so waking it for the same yield would only produce a
// duplicate report.
func (r *Runtime) narratorCoveredYield() bool {
	y := r.st.LastYield
	return y != nil && r.yieldSeenSeq > 0 && r.narrLastSeen >= r.yieldSeenSeq && r.narrSaidSeq > r.yieldSeenSeq
}

// narratorTick wakes the narrator periodically while work is happening.
func (r *Runtime) narratorTick() {
	r.post(func() {
		defer func() {
			if !r.ending {
				r.narrTicker = time.AfterFunc(time.Duration(r.cfg.NarratorTickSeconds)*time.Second, r.narratorTick)
			}
		}()
		if r.narrBusy || r.ending {
			return
		}
		busy := r.orchBusy || len(r.st.RunningTasks()) > 0
		if !busy {
			return
		}
		// Wake when the orchestrator's world moved since the narrator looked,
		// or when the user has waited past the quiet limit even though nothing
		// new landed (a long command, a long think): that is when they most
		// want to hear which step is taking its time.
		limit := time.Duration(r.cfg.NarratorQuietSeconds) * time.Second
		if r.narrWorthy <= r.narrLastSeen && !(limit > 0 && r.quietFor() >= limit) {
			return
		}
		if r.narrPending == "" {
			r.narrPending = wakePeriodic
		}
	})
}

func (r *Runtime) startNarratorTurn(reason string) {
	r.narrBusy = true
	r.append(event.New(event.TurnStart, event.ActorNarrator, event.TurnData{Reason: reason}))
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		endReason := r.narratorTurn(reason)
		r.post(func() {
			r.narrBusy = false
			r.append(event.New(event.TurnEnd, event.ActorNarrator, event.TurnData{Reason: endReason}))
			if reason == wakeFinal {
				r.narrFinal = true
			}
		})
	}()
}

// narratorTurn makes one narrator decision. Runs on its own goroutine.
func (r *Runtime) narratorTurn(reason string) string {
	ctx := r.ctx
	if reason == wakeFinal {
		// Give the final report a short grace period even during shutdown.
		var cancel func()
		ctx, cancel = contextWithTimeout(60 * time.Second)
		defer cancel()
	}
	var msgs []llm.Message
	var seenSeq int64
	var steer, phoneLine string
	mustSpeak := false
	r.sync(func() {
		mustSpeak = r.narrSaidSeq <= r.st.LastUserSeq
		if r.phone != nil {
			phoneLine = PhoneStatus(r.phone.isRemote())
		}
		steer = steerNarrator(r.st, time.Now(), reason, r.opts.Interactive, r.phone != nil, r.narrLastSaid, mustSpeak, r.quietFor(), time.Duration(r.cfg.NarratorQuietSeconds)*time.Second, r.inflightLines(time.Now()))
		r.append(event.New(event.Steer, event.ActorNarrator, event.SteerData{Text: steer}))
		msgs = r.st.NarratorView(nil)
		seenSeq = r.st.LastSeq()
	})
	if len(msgs) == 0 {
		return "nothing to see"
	}
	req := llm.Request{
		System: r.narratorSystem(phoneLine), Messages: msgs, Tools: r.narrTools,
		ToolChoice: "required", CacheKey: r.sess.ID + "-narrator",
	}
	for attempt := 0; attempt < 2; attempt++ {
		resp, err := r.completeActor(ctx, event.ActorNarrator, "", req)
		if err != nil {
			if ctx.Err() != nil && reason != wakeFinal {
				return "interrupted"
			}
			r.ui.Log("narrator: model call failed: %v", shortErr(err))
			r.sync(func() {
				// The narrator looked at everything up to seenSeq even though
				// the call failed; leaving the cursor behind would make the
				// loop call it again every tick instead of showing the prompt.
				r.narrLastSeen = seenSeq
				r.append(event.New(event.Error, event.ActorHarness, event.ErrorData{Where: "narrator", Text: err.Error()}))
			})
			if reason == wakeFinal && mustSpeak {
				// The user must still get something.
				r.deliverFallbackFinal()
			}
			return "error"
		}
		r.ui.Stream(event.ActorNarrator, "", "", "")
		r.recordAssistant(event.ActorNarrator, "", resp, seenSeq)
		r.sync(func() { r.narrLastSeen = seenSeq })

		if len(resp.ToolCalls) == 0 {
			// Plain text: treat as a held thought unless this is the final
			// report, in which case the text is the report.
			if reason == wakeFinal && mustSpeak && strings.TrimSpace(resp.Text) != "" {
				r.deliverMessage(resp.Text, true)
				return "final"
			}
			if attempt == 0 {
				req.Messages = append(req.Messages, llm.Message{Role: "assistant", Text: resp.Text, Native: resp.Native, NativeProtocol: resp.Protocol}, llm.Message{Role: "user", Text: "[harness] Respond with exactly one tool call: send_message, ask_user, or hold."})
				continue
			}
			return "held"
		}
		spoke := false
		for _, tc := range resp.ToolCalls {
			args, err := llm.ArgsObject(tc.Args)
			if err != nil {
				r.recordToolResult(event.ActorNarrator, "", tc, err.Error(), true)
				continue
			}
			switch tc.Name {
			case "send_message":
				text, _ := args["text"].(string)
				if strings.TrimSpace(text) == "" {
					r.recordToolResult(event.ActorNarrator, "", tc, "text is required", true)
					continue
				}
				if spoke {
					r.recordToolResult(event.ActorNarrator, "", tc, "one message per wake; combine them next time", true)
					continue
				}
				atts, err := r.narratorAttachments(args["attachments"])
				if err != nil {
					r.recordToolResult(event.ActorNarrator, "", tc, "not sent: "+err.Error()+". Fix the attachment or send the message without it.", true)
					continue
				}
				important, _ := args["important"].(bool)
				r.deliverMessage(text, important || reason == wakeFinal, atts...)
				if len(atts) > 0 {
					r.recordToolResult(event.ActorNarrator, "", tc, fmt.Sprintf("delivered with %d attachment(s)", len(atts)), false)
				} else {
					r.recordToolResult(event.ActorNarrator, "", tc, "delivered", false)
				}
				spoke = true
			case "ask_user":
				text, _ := args["text"].(string)
				if strings.TrimSpace(text) == "" {
					r.recordToolResult(event.ActorNarrator, "", tc, "text is required", true)
					continue
				}
				var opts []string
				if arr, ok := args["options"].([]any); ok {
					for _, o := range arr {
						if s, ok := o.(string); ok && strings.TrimSpace(s) != "" {
							opts = append(opts, s)
						}
					}
				}
				var id string
				r.sync(func() {
					id = r.st.NextQuestionID()
					r.append(event.New(event.NarratorQuestion, event.ActorNarrator, event.NarratorQuestionData{ID: id, Text: text, Options: opts}))
					r.narrLastSaid = text
					r.narrSaidAt = time.Now()
				})
				r.ui.Ask(id, text, opts)
				var onPhone bool
				r.sync(func() { onPhone = r.phone != nil })
				if r.opts.Interactive {
					r.recordToolResult(event.ActorNarrator, "", tc, "asked; the answer will appear as a user message", false)
				} else if onPhone {
					r.recordToolResult(event.ActorNarrator, "", tc, "asked on the user's phone; the session waits for the answer, which will appear as a user message", false)
				} else {
					r.recordToolResult(event.ActorNarrator, "", tc, "shown to the user; this session is non-interactive so it ends now and they can answer with `eagent resume`", false)
				}
				spoke = true
			case "hold":
				r.recordToolResult(event.ActorNarrator, "", tc, "holding", false)
			default:
				r.recordToolResult(event.ActorNarrator, "", tc, fmt.Sprintf("unknown tool %q", tc.Name), true)
			}
		}
		if reason == wakeFinal && !spoke && mustSpeak {
			if attempt == 0 {
				req.Messages = append(req.Messages, llm.Message{Role: "assistant", Text: resp.Text, ToolCalls: resp.ToolCalls, Native: resp.Native, NativeProtocol: resp.Protocol}, llm.Message{Role: "tool", Results: []llm.ToolResult{{CallID: resp.ToolCalls[0].ID, Name: resp.ToolCalls[0].Name, Output: "holding is not allowed on the final wake"}}}, llm.Message{Role: "user", Text: "[harness] The session is ending. Send the final report now with send_message."})
				continue
			}
			r.deliverFallbackFinal()
		}
		return "done"
	}
	return "done"
}

// leakedToken matches provider control markup that occasionally escapes into
// model text, e.g. DeepSeek's <｜DSML｜…> tool markup or <|im_end|>.
var leakedToken = regexp.MustCompile(`</?[｜|][^<>]{0,80}>`)

// cleanNarration removes leaked control tokens and trailing junk.
func cleanNarration(text string) string {
	text = leakedToken.ReplaceAllString(text, "")
	return strings.TrimSpace(text)
}

// deliverMessage records and shows a narrator message.
func (r *Runtime) deliverMessage(text string, important bool, atts ...event.Attachment) {
	text = cleanNarration(text)
	r.sync(func() {
		if r.st.LastYield != nil && r.st.LastYield.Done {
			important = true // the final report always earns the notification
		}
		ev := r.append(event.New(event.NarratorMessage, event.ActorNarrator, event.NarratorMessageData{Text: text, Important: important, Attachments: atts}))
		r.narrLastSaid = text
		r.narrSaidSeq = ev.Seq
		r.narrSaidAt = time.Now()
	})
	if s := attachmentSummary(atts); s != "" {
		text += "\n" + s
	}
	r.ui.Narrate(text)
}

// deliverFallbackFinal makes sure the user gets a status even if the
// narrator model is unavailable.
func (r *Runtime) deliverFallbackFinal() {
	var b strings.Builder
	r.sync(func() {
		b.WriteString("Session ending. ")
		if y := r.st.LastYield; y != nil {
			if y.Done {
				b.WriteString("The orchestrator reported the work done: ")
			} else {
				b.WriteString("The orchestrator stopped and is waiting: ")
			}
			b.WriteString(y.Reason)
		}
		if n := len(r.st.Notes); n > 0 {
			b.WriteString("\n\nLast note from the orchestrator: " + r.st.Notes[n-1].Text)
		}
	})
	r.deliverMessage(b.String(), true)
}

// quietFor is how long the user has gone without a narrator message, or
// since the session started when there has been none. Loop goroutine only.
func (r *Runtime) quietFor() time.Duration {
	if !r.narrSaidAt.IsZero() {
		return time.Since(r.narrSaidAt)
	}
	if !r.st.Started.IsZero() {
		return time.Since(r.st.Started)
	}
	return 0
}

// inflightLines names what is running right now and for how long, so the
// narrator can tell the user which command or step is taking its time.
// Loop goroutine only.
func (r *Runtime) inflightLines(now time.Time) []string {
	var out []string
	for _, p := range r.st.RunningProcs() {
		owner := "the orchestrator"
		if p.Task != "" {
			owner = "task " + p.Task
		}
		out = append(out, fmt.Sprintf("`%s` (%s, %s) has been running for %s", shortCommand(p.Command), p.Handle, owner, since(p.Started, now)))
	}
	for _, t := range r.st.RunningTasks() {
		if t.Status == "running" {
			out = append(out, taskEvidence(r.st, t, now))
		}
	}
	if r.orchBusy && !r.orchCallAt.IsZero() && now.Sub(r.orchCallAt) > 45*time.Second {
		out = append(out, fmt.Sprintf("the orchestrator's current model call has been going for %s (a long think or a long reply)", since(r.orchCallAt, now)))
	}
	return out
}

// taskEvidence states, from the log alone, what a running task has actually
// done: how many model calls came back, whether one is in flight and for how
// long, how many commands ran, which files it wrote, and its latest visible
// step. The narrator learns nothing else about the task, on purpose: a step
// that is not here has not happened, however plausible it sounds.
func taskEvidence(st *state.State, t *state.Task, now time.Time) string {
	// A tool call is work only once its result is back and is not an error:
	// the model asking to write a file is not the file being written.
	results := map[string]event.ToolResultData{}
	for _, ev := range st.Events {
		if ev.Task != t.ID || ev.Actor != event.ActorTask || ev.Type != event.ToolResult {
			continue
		}
		var d event.ToolResultData
		if ev.Decode(&d) == nil {
			results[d.CallID] = d
		}
	}
	outcome := func(have, done bool) string {
		switch {
		case !have:
			return " (no result yet)"
		case !done:
			return " (failed)"
		}
		return ""
	}
	calls, cmds, failed, pending := 0, 0, 0, 0
	var files []string
	seen := map[string]bool{}
	inCall := false
	var callStart time.Time
	var step string
	var stepAt time.Time
	for _, ev := range st.Events {
		if ev.Task != t.ID || ev.Actor != event.ActorTask {
			continue
		}
		switch ev.Type {
		case event.TurnStart:
			inCall, callStart = true, ev.Time
		case event.TurnEnd:
			inCall = false
		case event.Assistant:
			calls++
			var d event.AssistantData
			_ = ev.Decode(&d)
			var parts []string
			for _, tc := range d.ToolCalls {
				res, have := results[tc.ID]
				done := have && !res.IsError
				switch {
				case !have:
					pending++
				case res.IsError:
					failed++
				}
				args, err := llm.ArgsObject(tc.Args)
				if err != nil {
					parts = append(parts, tc.Name+outcome(have, done))
					continue
				}
				switch tc.Name {
				case "bash":
					if done {
						cmds++
					}
					cmd, _ := args["command"].(string)
					parts = append(parts, "`"+shortCommand(cmd)+"`"+outcome(have, done))
				case "write_file", "edit_file":
					p, _ := args["path"].(string)
					if done && p != "" && !seen[p] {
						seen[p] = true
						files = append(files, p)
					}
					parts = append(parts, tc.Name+" "+p+outcome(have, done))
				case "read_file", "view_image", "list_dir":
					p, _ := args["path"].(string)
					parts = append(parts, tc.Name+" "+p+outcome(have, done))
				default:
					parts = append(parts, tc.Name+outcome(have, done))
				}
			}
			if len(parts) == 0 && strings.TrimSpace(d.Text) != "" {
				parts = append(parts, fmt.Sprintf("said %q", clipTail(d.Text, 100)))
			}
			if len(parts) > 0 {
				step, stepAt = strings.Join(parts, ", "), ev.Time
			}
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "task %s (%q) has been working for %s", t.ID, t.Title, since(t.Created, now))
	if calls == 0 {
		b.WriteString("; no model call has come back yet")
	} else {
		fmt.Fprintf(&b, "; %d model call(s) have come back", calls)
	}
	if inCall {
		fmt.Fprintf(&b, "; its current model call has been running for %s and has produced nothing visible yet", since(callStart, now))
	}
	switch {
	case cmds == 0 && len(files) == 0:
		b.WriteString("; it has run no commands and written no files so far")
	default:
		fmt.Fprintf(&b, "; %d command(s) run, %d file(s) written", cmds, len(files))
		if len(files) > 0 {
			shown := files
			if len(shown) > 3 {
				shown = shown[len(shown)-3:]
			}
			fmt.Fprintf(&b, " (%s)", strings.Join(shown, ", "))
		}
	}
	if failed > 0 {
		fmt.Fprintf(&b, "; %d tool call(s) came back as errors and changed nothing", failed)
	}
	if pending > 0 {
		fmt.Fprintf(&b, "; %d tool call(s) have not returned yet", pending)
	}
	if step != "" {
		fmt.Fprintf(&b, "; latest visible step, %s ago: %s", since(stepAt, now), step)
	}
	return b.String()
}

// shortCommand trims a shell command to its first line, at most 90 characters.
func shortCommand(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if i := strings.IndexByte(cmd, '\n'); i >= 0 {
		cmd = cmd[:i] + " …"
	}
	if len(cmd) > 90 {
		cmd = cmd[:89] + "…"
	}
	return cmd
}

// lastStep describes a task worker's most recent action (its last tool call,
// or its last words), so the narrator can say why a task is taking a while.
// Loop goroutine only.
func (r *Runtime) lastStep(taskID string) (string, time.Time) {
	for i := len(r.st.Events) - 1; i >= 0; i-- {
		ev := r.st.Events[i]
		if ev.Type != event.Assistant || ev.Actor != event.ActorTask || ev.Task != taskID {
			continue
		}
		var d event.AssistantData
		_ = ev.Decode(&d)
		var parts []string
		for _, tc := range d.ToolCalls {
			args, err := llm.ArgsObject(tc.Args)
			if err != nil {
				parts = append(parts, tc.Name)
				continue
			}
			switch tc.Name {
			case "bash":
				cmd, _ := args["command"].(string)
				parts = append(parts, "`"+shortCommand(cmd)+"`")
			case "write_file", "edit_file", "read_file", "view_image":
				p, _ := args["path"].(string)
				parts = append(parts, tc.Name+" "+p)
			case "bash_poll", "bash_write", "bash_kill":
				h, _ := args["handle"].(string)
				parts = append(parts, tc.Name+" "+h)
			default:
				parts = append(parts, tc.Name)
			}
		}
		if len(parts) == 0 && strings.TrimSpace(d.Text) != "" {
			parts = append(parts, fmt.Sprintf("said %q", clipTail(d.Text, 100)))
		}
		if len(parts) == 0 {
			continue
		}
		return strings.Join(parts, ", "), ev.Time
	}
	return "", time.Time{}
}
