package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/procs"
	"github.com/ericflo/eagent/internal/sched"
	"github.com/ericflo/eagent/internal/tools"
)

// caller identifies who is executing a tool.
type caller struct {
	actor string
	task  string
	ctx   context.Context
}

// execTool runs one tool call for an actor and returns the result text. It
// runs on the actor's goroutine; state changes go through r.sync.
func (r *Runtime) execTool(c caller, tc event.ToolCall) (out string, isErr bool) {
	args, err := llm.ArgsObject(tc.Args)
	if err != nil {
		return "Tool call rejected: " + err.Error(), true
	}
	defer func() {
		if max := r.cfg.ToolOutputMaxChars; max > 0 && len(out) > max {
			out = r.spillAndTruncate(c, tc, out, max)
		}
	}()
	str := func(k string) string {
		switch v := args[k].(type) {
		case string:
			return v
		case nil:
			return ""
		case float64, int64, int, bool:
			return fmt.Sprint(v) // models sometimes pass numbers for string fields
		}
		return ""
	}
	num := func(k string, def int) int {
		switch v := args[k].(type) {
		case float64:
			return int(v)
		case int:
			return v
		case string:
			var n int
			if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
				return n
			}
		}
		return def
	}
	boolean := func(k string) bool {
		v, _ := args[k].(bool)
		return v
	}
	strs := func(k string) []string {
		var out []string
		v := args[k]
		if s, ok := v.(string); ok {
			// Some models stringify arrays: "[\"t1\"]" or "t1, t2".
			var arr []any
			if err := json.Unmarshal([]byte(s), &arr); err == nil {
				v = arr
			} else {
				for _, part := range strings.Split(s, ",") {
					if part = strings.TrimSpace(part); part != "" {
						out = append(out, part)
					}
				}
				return out
			}
		}
		if arr, ok := v.([]any); ok {
			for _, x := range arr {
				if s, ok := x.(string); ok {
					out = append(out, s)
				}
			}
		}
		return out
	}

	switch tc.Name {
	// ---- shell ----
	case "bash":
		cmd := str("command")
		if strings.TrimSpace(cmd) == "" {
			return "command is required", true
		}
		wait := clampInt(num("wait_seconds", r.cfg.BashWaitSeconds), 0, 300)
		timeout := num("timeout_seconds", r.cfg.BashTimeoutSeconds)
		if timeout < 0 {
			timeout = 0
		}
		return r.startBash(c, cmd, wait, timeout)
	case "bash_poll":
		return r.pollBash(c, str("handle"), clampInt(num("wait_seconds", 0), 0, 300))
	case "bash_write":
		p, ok := r.ownedProc(c, str("handle"))
		if !ok {
			return "no such process " + str("handle"), true
		}
		input := str("input")
		if input != "" && !boolean("raw") && !strings.HasSuffix(input, "\n") {
			input += "\n"
		}
		if err := p.Write(input, boolean("close")); err != nil {
			return err.Error(), true
		}
		// Give the program a moment to react, then report.
		return r.pollBash(c, str("handle"), 2)
	case "bash_kill":
		p, ok := r.ownedProc(c, str("handle"))
		if !ok {
			return "no such process " + str("handle"), true
		}
		p.Kill()
		p.Wait(c.ctx, 3*time.Second)
		return fmt.Sprintf("%s killed", p.Handle), false
	case "bash_extend":
		p, ok := r.ownedProc(c, str("handle"))
		if !ok {
			return "no such process " + str("handle"), true
		}
		p.Extend(time.Duration(num("seconds", 600)) * time.Second)
		if d := p.Deadline(); d.IsZero() {
			return p.Handle + " now has no deadline", false
		} else {
			return fmt.Sprintf("%s deadline extended to %s", p.Handle, d.Local().Format("15:04:05")), false
		}
	case "bash_list":
		var mine []*procs.Proc
		r.sync(func() {
			for _, p := range r.procs.All() {
				if o := r.procOwner[p.Handle]; o.actor == c.actor && o.task == c.task {
					mine = append(mine, p)
				}
			}
		})
		if len(mine) == 0 {
			return "no processes started", false
		}
		var b strings.Builder
		for _, p := range mine {
			b.WriteString(p.Describe() + "\n")
		}
		return b.String(), false

	// ---- files ----
	case "read_file":
		out, err := r.files.ReadFile(str("path"), num("offset", 0), num("limit", 0), r.cfg.ToolOutputMaxChars)
		if err != nil {
			return err.Error(), true
		}
		return out, false
	case "write_file":
		content, hasContent := args["content"].(string)
		if !hasContent {
			return "content is required (a string)", true
		}
		out, err := r.files.WriteFile(str("path"), content)
		if err != nil {
			return err.Error(), true
		}
		return out, false
	case "edit_file":
		out, err := r.files.EditFile(str("path"), str("old_text"), str("new_text"), boolean("replace_all"))
		if err != nil {
			return err.Error(), true
		}
		return out, false
	case "list_dir":
		out, err := r.files.ListDir(str("path"))
		if err != nil {
			return err.Error(), true
		}
		return out, false

	// ---- session archive ----
	case "session_list":
		out, err := r.archive.List()
		if err != nil {
			return err.Error(), true
		}
		return out, false
	case "session_read":
		out, err := r.archive.Read(str("file"), num("from", 1), num("to", 0), boolean("raw"), r.cfg.ToolOutputMaxChars)
		if err != nil {
			return err.Error(), true
		}
		return out, false
	case "session_search":
		out, err := r.archive.Search(str("query"), str("file"), num("max_results", 40))
		if err != nil {
			return err.Error(), true
		}
		return out, false

	// ---- orchestrator ----
	case "delegate":
		if c.actor != event.ActorOrchestrator {
			return "only the orchestrator can delegate", true
		}
		title, desc := strings.TrimSpace(str("title")), strings.TrimSpace(str("description"))
		if desc == "" {
			return "description is required", true
		}
		if title == "" {
			title = firstLine(desc, 60)
		}
		var id string
		r.sync(func() {
			id = r.st.NextTaskID()
			r.append(event.New(event.TaskCreate, event.ActorOrchestrator, event.TaskCreateData{ID: id, Title: title, Description: desc, Kind: "work"}))
		})
		return fmt.Sprintf("Task %s started: %s. Its report will arrive as a message; use wait to block on it.", id, title), false
	case "wait":
		return r.waitTool(c, strs("tasks"), strs("processes"), clampInt(num("timeout_seconds", 600), 1, 3600))
	case "cancel_task":
		id := str("task")
		var msg string
		var isErr bool
		r.sync(func() {
			t := r.st.Tasks[id]
			if t == nil {
				msg, isErr = "no such task "+id, true
				return
			}
			if !t.Running() {
				msg = fmt.Sprintf("task %s already %s", id, t.Status)
				return
			}
			if cancel := r.running[id]; cancel != nil {
				cancel()
			} else {
				r.append(event.New(event.TaskEnd, event.ActorHarness, event.TaskEndData{ID: id, Status: "cancelled", Summary: "cancelled by the orchestrator before it started"}).WithTask(id))
			}
			msg = fmt.Sprintf("task %s cancelled", id)
		})
		return msg, isErr
	case "note":
		text := strings.TrimSpace(str("text"))
		if text == "" {
			return "text is required", true
		}
		r.sync(func() {
			r.append(event.New(event.Note, event.ActorOrchestrator, event.NoteData{Text: text}))
			r.wakeNarrator(wakeNote)
		})
		return "noted; the narrator will decide what to relay", false
	case "schedule":
		spec := strings.TrimSpace(str("spec"))
		s, err := sched.Parse(spec)
		if err != nil {
			return err.Error(), true
		}
		now := time.Now()
		next := s.Next(now)
		if next.IsZero() {
			return "that schedule would never fire", true
		}
		var id string
		r.sync(func() {
			id = r.st.NextScheduleID()
			r.append(event.New(event.ScheduleCreate, event.ActorOrchestrator, event.ScheduleCreateData{ID: id, Kind: s.Kind, Spec: spec, Note: str("note"), Next: next}))
			r.armSchedule(id, next)
		})
		return fmt.Sprintf("schedule %s created (%s); first wake at %s", id, s.Kind, next.Local().Format("15:04:05")), false
	case "cancel_schedule":
		id := str("schedule")
		var msg string
		var isErr bool
		r.sync(func() {
			sc := r.st.Schedules[id]
			if sc == nil || sc.Cancelled {
				msg, isErr = "no active schedule "+id, true
				return
			}
			if t := r.timers[id]; t != nil {
				t.Stop()
				delete(r.timers, id)
			}
			r.append(event.New(event.ScheduleCancel, event.ActorOrchestrator, event.ScheduleCancelData{ID: id}))
			msg = "schedule " + id + " cancelled"
		})
		return msg, isErr
	case "yield":
		// Recorded by the orchestrator loop, which ends the turn.
		return "ok", false
	case "complete_task":
		return "ok", false
	}
	return fmt.Sprintf("unknown tool %q", tc.Name), true
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func firstLine(s string, max int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > max {
		s = s[:max-3] + "..."
	}
	return s
}

// ownedProc looks up a process the caller started.
func (r *Runtime) ownedProc(c caller, handle string) (*procs.Proc, bool) {
	p, ok := r.procs.Get(handle)
	if !ok {
		return nil, false
	}
	var owned bool
	r.sync(func() {
		o := r.procOwner[handle]
		owned = o.actor == c.actor && o.task == c.task
	})
	if !owned {
		return nil, false
	}
	return p, true
}

// startBash launches a command and waits briefly for it. The start event,
// owner, and attended flag are recorded before the process is launched so a
// command that exits instantly cannot race its own bookkeeping.
func (r *Runtime) startBash(c caller, command string, waitSeconds, timeoutSeconds int) (string, bool) {
	var handle string
	cwd := r.projectPath()
	r.sync(func() {
		handle = r.st.NextProcHandle()
		r.procOwner[handle] = owner{c.actor, c.task}
		r.procAttended[handle] = waitSeconds > 0
		r.append(event.New(event.ProcStart, c.actor, event.ProcStartData{Handle: handle, Command: command, Cwd: cwd, TimeoutS: timeoutSeconds}).WithTask(c.task))
	})
	spec := procs.Spec{Handle: handle, Command: command, Cwd: cwd, Owner: c.actor + "/" + c.task}
	if timeoutSeconds > 0 {
		spec.Timeout = time.Duration(timeoutSeconds) * time.Second
	}
	p, err := r.procs.Start(spec)
	if err != nil {
		r.sync(func() {
			r.append(event.New(event.ProcExit, c.actor, event.ProcExitData{Handle: handle, ExitCode: -1, Reason: "failed", Tail: err.Error()}).WithTask(c.task))
		})
		return "could not start command: " + err.Error(), true
	}
	if waitSeconds > 0 {
		p.Wait(c.ctx, time.Duration(waitSeconds)*time.Second)
	}
	return r.renderProc(c, p, waitSeconds > 0), false
}

// pollBash reads new output, optionally waiting for exit first.
func (r *Runtime) pollBash(c caller, handle string, waitSeconds int) (string, bool) {
	p, ok := r.ownedProc(c, handle)
	if !ok {
		return "no such process " + handle + " (see bash_list)", true
	}
	if waitSeconds > 0 {
		r.attend(p.Handle, c.ctx, time.Duration(waitSeconds)*time.Second, p)
	}
	return r.renderProc(c, p, waitSeconds > 0), false
}

// attend blocks on a process while marking it attended, so an exit during
// the wait is delivered in the tool result rather than as a notification.
func (r *Runtime) attend(handle string, ctx context.Context, d time.Duration, p *procs.Proc) {
	r.sync(func() { r.procAttended[handle] = true })
	p.Wait(ctx, d)
}

// renderProc formats a process's new output and status for the model, and
// records whether the final output has been delivered.
func (r *Runtime) renderProc(c caller, p *procs.Proc, waited bool) string {
	var cursor int
	r.sync(func() { cursor = r.procCursor[p.Handle] })
	out, next := p.Output(cursor)
	status := p.Status()
	finished := status != procs.Running
	r.sync(func() {
		r.procCursor[p.Handle] = next
		r.procAttended[p.Handle] = false
		if finished {
			r.procSeenAt[p.Handle] = true
		}
	})
	var b strings.Builder
	switch status {
	case procs.Running:
		fmt.Fprintf(&b, "[%s still running after %s; bash_poll to read more, bash_poll with wait_seconds to block, bash_kill to stop]", p.Handle, p.Duration().Round(time.Second))
		if d := p.Deadline(); !d.IsZero() {
			fmt.Fprintf(&b, " (times out in %s)", time.Until(d).Round(time.Second))
		}
	case procs.Exited:
		fmt.Fprintf(&b, "[%s exited with code %d after %s]", p.Handle, p.ExitCode(), p.Duration().Round(time.Millisecond))
	case procs.Timeout:
		fmt.Fprintf(&b, "[%s killed: timed out after %s. Use timeout_seconds to allow more time, or 0 for servers]", p.Handle, p.Duration().Round(time.Second))
	default:
		fmt.Fprintf(&b, "[%s %s after %s]", p.Handle, status, p.Duration().Round(time.Second))
	}
	if strings.TrimSpace(out) != "" {
		b.WriteString("\n")
		b.WriteString(out)
	} else if finished {
		b.WriteString("\n(no output)")
	} else if !waited {
		b.WriteString("\n(no output yet)")
	} else {
		b.WriteString("\n(no new output)")
	}
	return b.String()
}

// waitTool blocks the orchestrator until a task or process finishes.
func (r *Runtime) waitTool(c caller, taskIDs, handles []string, timeoutSeconds int) (string, bool) {
	w := &waiter{tasks: map[string]bool{}, procs: map[string]bool{}, ch: make(chan string, 1)}
	for _, id := range taskIDs {
		w.tasks[id] = true
	}
	for _, h := range handles {
		w.procs[h] = true
	}
	w.any = len(taskIDs) == 0 && len(handles) == 0
	var already string
	r.sync(func() {
		// Anything already finished?
		for id := range w.tasks {
			if t := r.st.Tasks[id]; t == nil {
				already = fmt.Sprintf("no such task %s", id)
			} else if !t.Running() {
				already = fmt.Sprintf("task %s already %s (see its report above)", id, t.Status)
			}
		}
		for h := range w.procs {
			if p := r.st.Procs[h]; p == nil {
				already = fmt.Sprintf("no such process %s", h)
			} else if p.Status != "running" {
				already = fmt.Sprintf("process %s already finished (%s, exit %d); use bash_poll for its output", h, p.Status, p.ExitCode)
			}
		}
		if w.any && len(r.st.RunningTasks()) == 0 {
			already = "no tasks are running; nothing to wait for"
		}
		if already == "" {
			r.waiters = append(r.waiters, w)
		}
	})
	if already != "" {
		return already, false
	}
	timer := time.NewTimer(time.Duration(timeoutSeconds) * time.Second)
	defer timer.Stop()
	select {
	case what := <-w.ch:
		return fmt.Sprintf("%s finished; its result is in the next message.", what), false
	case <-timer.C:
		r.sync(func() {
			for i, x := range r.waiters {
				if x == w {
					r.waiters = append(r.waiters[:i], r.waiters[i+1:]...)
					break
				}
			}
		})
		return fmt.Sprintf("nothing finished within %ds; still running. You can wait again, check on them, or do other work.", timeoutSeconds), false
	case <-c.ctx.Done():
		r.sync(func() {
			for i, x := range r.waiters {
				if x == w {
					r.waiters = append(r.waiters[:i], r.waiters[i+1:]...)
					break
				}
			}
		})
		return "wait cancelled: the session is stopping", true
	}
}

// toolListFor returns the tool list an actor is allowed to use.
func (r *Runtime) toolListFor(actor string) []llm.Tool {
	switch actor {
	case event.ActorOrchestrator:
		return r.orchTools
	case event.ActorTask:
		return r.taskTools
	default:
		return r.narrTools
	}
}

// recordAssistant appends the model response as an event and returns it.
func (r *Runtime) recordAssistant(actor, task string, resp *llm.Response, seenSeq int64) event.Event {
	data := event.AssistantData{
		Provider: resp.Protocol, Model: resp.Model, Text: resp.Text, Reasoning: summarizeReasoning(resp.Reasoning),
		ToolCalls: resp.ToolCalls, Native: resp.Native, Usage: resp.Usage,
		ElapsedMS: resp.Elapsed.Milliseconds(), SeenSeq: seenSeq, Stop: resp.Stop,
	}
	if resp.Protocol == llm.ProtocolChat {
		data.Native = nil // chat replays from text + tool calls
	}
	var ev event.Event
	r.sync(func() { ev = r.append(event.New(event.Assistant, actor, data).WithTask(task)) })
	return ev
}

// summarizeReasoning keeps reasoning text for humans without bloating the log.
func summarizeReasoning(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 4000 {
		return s[:4000] + "..."
	}
	return s
}

// recordToolResult appends a tool result.
func (r *Runtime) recordToolResult(actor, task string, tc event.ToolCall, out string, isErr bool) {
	r.sync(func() {
		r.append(event.New(event.ToolResult, actor, event.ToolResultData{CallID: tc.ID, Name: tc.Name, Output: out, IsError: isErr}).WithTask(task))
	})
}

// unknownToolResult rejects tools an actor does not have.
func (r *Runtime) allowed(actor string, name string) bool {
	return tools.Has(r.toolListFor(actor), name)
}

// jsonPreview renders arguments compactly for traces.
func jsonPreview(raw json.RawMessage, max int) string {
	s := string(raw)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		s = s[:max-3] + "..."
	}
	return s
}

// spillAndTruncate saves an oversized tool result in full under the session
// directory and returns the truncated text with a notice that tells the
// model how to read the rest, so nothing is ever out of reach.
func (r *Runtime) spillAndTruncate(c caller, tc event.ToolCall, out string, max int) string {
	dir := filepath.Join(r.sess.Path, "outputs")
	base := tc.ID
	if c.task != "" {
		base = c.task + "-" + base
	}
	path, err := tools.Spill(dir, base, out)
	if err != nil {
		return tools.Truncate(out, max)
	}
	if rel, rerr := filepath.Rel(r.projectPath(), path); rerr == nil && !strings.HasPrefix(rel, "..") {
		path = rel
	}
	lines := strings.Count(out, "\n") + 1
	notice := fmt.Sprintf("Full output (%d lines) saved to %s; page through it with read_file(path, offset, limit) or search it with bash grep", lines, path)
	return tools.TruncateWithNotice(out, max, notice)
}
