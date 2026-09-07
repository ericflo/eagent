package harness

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/state"
)

// startQueuedTasks launches workers up to the concurrency limit. Loop only.
func (r *Runtime) startQueuedTasks() {
	if r.ending {
		return
	}
	running := len(r.running)
	for _, id := range r.st.TaskOrder {
		t := r.st.Tasks[id]
		if t.Status != "queued" {
			continue
		}
		// Dossier tasks always run; work tasks respect the limit.
		if t.Kind != "dossier" && running >= r.cfg.TaskConcurrency {
			break
		}
		running++
		ctx, cancel := context.WithCancel(r.ctx)
		r.running[id] = cancel
		r.append(event.New(event.TaskStart, event.ActorHarness, event.TaskCreateData{ID: id}).WithTask(id))
		r.wg.Add(1)
		go func(t state.Task) {
			defer r.wg.Done()
			status, summary := r.runTask(ctx, t)
			r.post(func() {
				delete(r.running, t.ID)
				if cur := r.st.Tasks[t.ID]; cur != nil && cur.Running() {
					r.append(event.New(event.TaskEnd, event.ActorHarness, event.TaskEndData{ID: t.ID, Status: status, Summary: summary, Turns: cur.Turns, Usage: cur.Usage}).WithTask(t.ID))
				}
				r.killTaskProcs(t.ID)
				r.signalWaiters(t.ID, "")
				if t.Kind == "dossier" {
					r.finishRollover(t.ID, status, summary)
				} else {
					r.wakeNarrator(wakeTask)
				}
			})
		}(*t)
	}
}

// killTaskProcs stops a finished task's leftover commands, except explicit
// services (no timeout), which are handed over to the session.
func (r *Runtime) killTaskProcs(taskID string) {
	for _, p := range r.procs.Running() {
		o := r.procOwner[p.Handle]
		if o.task != taskID {
			continue
		}
		if sp := r.st.Procs[p.Handle]; sp != nil && sp.TimeoutS == 0 {
			continue
		}
		p.Kill()
	}
}

// runTask is the task worker's agentic loop. Runs on its own goroutine.
func (r *Runtime) runTask(ctx context.Context, t state.Task) (status, summary string) {
	c := caller{actor: event.ActorTask, task: t.ID, ctx: ctx}
	maxTurns := r.cfg.MaxTaskTurns
	if t.Kind == "dossier" {
		maxTurns = max(maxTurns, 40)
	}
	textOnly := 0
	lastText := ""
	for turn := 1; ; turn++ {
		if ctx.Err() != nil {
			if r.ctx.Err() != nil {
				return "interrupted", "the session stopped while this task was running"
			}
			return "cancelled", "cancelled by the orchestrator"
		}
		if turn > maxTurns {
			return "failed", fmt.Sprintf("stopped after %d model calls without calling complete_task. Last message from the worker: %s", maxTurns, lastText)
		}
		var msgs []llm.Message
		var seenSeq int64
		var cur *state.Task
		r.sync(func() {
			msgs = r.st.TaskView(t.ID)
			seenSeq = r.st.LastSeq()
			cur = r.st.Tasks[t.ID]
		})
		if cur == nil {
			return "failed", "task vanished from state"
		}
		msgs = append(msgs, llm.Message{Role: "user", Text: steerTask(cur, turn, maxTurns, time.Now())})
		req := llm.Request{
			System:   taskSystem(r.projectPath(), r.cfg.Instructions),
			Messages: msgs, Tools: r.taskTools, CacheKey: r.sess.ID + "-task-" + t.ID,
		}
		resp, err := r.taskClient.Complete(ctx, req, r.observer(event.ActorTask, t.ID))
		if err != nil {
			if ctx.Err() != nil {
				continue
			}
			r.sync(func() {
				r.append(event.New(event.Error, event.ActorHarness, event.ErrorData{Where: "task " + t.ID, Text: err.Error()}).WithTask(t.ID))
			})
			return "failed", "model call failed: " + shortErr(err)
		}
		r.ui.Stream(event.ActorTask, t.ID, "", "")
		r.recordAssistant(event.ActorTask, t.ID, resp, seenSeq)
		if strings.TrimSpace(resp.Text) != "" {
			lastText = strings.TrimSpace(resp.Text)
		}

		if len(resp.ToolCalls) == 0 {
			if resp.Truncated() {
				r.recordHarnessMessage(event.ActorTask, t.ID, "Your response was cut off by the output limit before any tool call. Write files in smaller pieces (write_file with at most ~300 lines, then edit_file to append) and keep deliberation short.")
				continue
			}
			textOnly++
			if textOnly >= 2 {
				if lastText == "" {
					return "failed", "the worker stopped without producing output or calling complete_task"
				}
				return "completed", "(the worker did not call complete_task; its last message follows)\n" + lastText
			}
			r.recordHarnessMessage(event.ActorTask, t.ID, "Your reply contained no tool call. Continue the work with tools, or call complete_task with your report if you are done.")
			continue
		}
		textOnly = 0
		for _, tc := range resp.ToolCalls {
			if ctx.Err() != nil {
				r.recordToolResult(event.ActorTask, t.ID, tc, "interrupted", true)
				continue
			}
			if !r.allowed(event.ActorTask, tc.Name) {
				r.recordToolResult(event.ActorTask, t.ID, tc, fmt.Sprintf("unknown tool %q", tc.Name), true)
				continue
			}
			if r.opts.Verbose {
				r.ui.Log("task %s -> %s %s", t.ID, tc.Name, jsonPreview(tc.Args, 160))
			}
			if tc.Name == "complete_task" {
				args, err := llm.ArgsObject(tc.Args)
				if err != nil {
					r.recordToolResult(event.ActorTask, t.ID, tc, err.Error(), true)
					continue
				}
				st, _ := args["status"].(string)
				sum, _ := args["summary"].(string)
				sum = strings.TrimSpace(sum)
				if sum == "" {
					r.recordToolResult(event.ActorTask, t.ID, tc, "summary is required: say what you did, how you verified it, and what is left", true)
					continue
				}
				if st != "failed" {
					st = "completed"
				}
				r.recordToolResult(event.ActorTask, t.ID, tc, "reported", false)
				return st, sum
			}
			out, isErr := r.execTool(c, tc)
			r.recordToolResult(event.ActorTask, t.ID, tc, out, isErr)
		}
		if resp.Truncated() {
			r.recordHarnessMessage(event.ActorTask, t.ID, "Note: your previous response hit the output limit, so its last tool call may have been incomplete. Check the result above and redo any cut-off write in smaller pieces.")
		}
	}
}
