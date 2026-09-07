package harness

import (
	"fmt"
	"strings"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
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
	if reason != wakeFinal && r.narrWorthy <= r.narrLastSeen {
		return // nothing new since it last looked
	}
	r.startNarratorTurn(reason)
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
		// Only if the orchestrator's world moved since the narrator looked.
		if r.narrWorthy <= r.narrLastSeen {
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
	var steer string
	mustSpeak := false
	r.sync(func() {
		msgs = r.st.NarratorView(nil)
		seenSeq = r.st.LastSeq()
		mustSpeak = r.narrSaidSeq <= r.st.LastUserSeq
		steer = steerNarrator(r.st, time.Now(), reason, r.opts.Interactive, r.narrLastSaid, mustSpeak)
	})
	if len(msgs) == 0 {
		return "nothing to see"
	}
	msgs = append(msgs, llm.Message{Role: "user", Text: steer})
	req := llm.Request{
		System: narratorSystem(r.cfg.Persona), Messages: msgs, Tools: r.narrTools,
		ToolChoice: "required", CacheKey: r.sess.ID + "-narrator",
	}
	for attempt := 0; attempt < 2; attempt++ {
		resp, err := r.narrClient.Complete(ctx, req, r.observer(event.ActorNarrator, ""))
		if err != nil {
			if ctx.Err() != nil && reason != wakeFinal {
				return "interrupted"
			}
			r.ui.Log("narrator: model call failed: %v", shortErr(err))
			r.sync(func() {
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
				r.deliverMessage(resp.Text)
				return "final"
			}
			if attempt == 0 {
				req.Messages = append(req.Messages, llm.Message{Role: "assistant", Text: resp.Text}, llm.Message{Role: "user", Text: "[harness] Respond with exactly one tool call: send_message, ask_user, or hold."})
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
				r.deliverMessage(text)
				r.recordToolResult(event.ActorNarrator, "", tc, "delivered", false)
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
				})
				r.ui.Ask(id, text, opts)
				if r.opts.Interactive {
					r.recordToolResult(event.ActorNarrator, "", tc, "asked; the answer will appear as a user message", false)
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
				req.Messages = append(req.Messages, llm.Message{Role: "assistant", Text: resp.Text, ToolCalls: resp.ToolCalls}, llm.Message{Role: "tool", Results: []llm.ToolResult{{CallID: resp.ToolCalls[0].ID, Name: resp.ToolCalls[0].Name, Output: "holding is not allowed on the final wake"}}}, llm.Message{Role: "user", Text: "[harness] The session is ending. Send the final report now with send_message."})
				continue
			}
			r.deliverFallbackFinal()
		}
		return "done"
	}
	return "done"
}

// deliverMessage records and shows a narrator message.
func (r *Runtime) deliverMessage(text string) {
	text = strings.TrimSpace(text)
	r.sync(func() {
		ev := r.append(event.New(event.NarratorMessage, event.ActorNarrator, event.NarratorMessageData{Text: text}))
		r.narrLastSaid = text
		r.narrSaidSeq = ev.Seq
	})
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
	r.deliverMessage(b.String())
}
