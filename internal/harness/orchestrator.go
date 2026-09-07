package harness

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
)

// startOrchestratorTurn launches a turn goroutine. Loop goroutine only.
func (r *Runtime) startOrchestratorTurn() {
	r.orchBusy = true
	r.orchCallAt = time.Now()
	reason := r.wakeReason()
	r.append(event.New(event.TurnStart, event.ActorOrchestrator, event.TurnData{Reason: reason}))
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		endReason := r.orchestratorTurn(reason)
		r.post(func() {
			r.orchBusy = false
			r.orchCallAt = time.Time{}
			r.append(event.New(event.TurnEnd, event.ActorOrchestrator, event.TurnData{Reason: endReason}))
			if endReason == "yield" || endReason == "done" || endReason == "context" {
				r.wakeNarrator(narratorReasonForYield(r.st))
			}
		})
	}()
}

// wakeReason describes why the orchestrator is running, for the log and the
// steering text.
func (r *Runtime) wakeReason() string {
	if r.st.LastUserSeq > r.lastOrchSeen && r.st.LastUserSeq > 0 {
		return "user message"
	}
	if r.lastWake > 0 && r.lastWake > r.lastOrchSeen {
		for i := len(r.st.Events) - 1; i >= 0; i-- {
			ev := r.st.Events[i]
			if ev.Seq != r.lastWake {
				continue
			}
			switch ev.Type {
			case event.TaskEnd:
				return "task finished"
			case event.ScheduleFire:
				return "schedule fired"
			case event.ProcExit:
				return "process finished"
			case event.Dossier:
				return "new subsession with dossier"
			case event.HarnessMessage:
				return "harness message"
			}
			break
		}
	}
	return "continue"
}

// orchestratorTurn runs model calls until the orchestrator yields, the
// context fills, or the session stops. Runs on its own goroutine.
func (r *Runtime) orchestratorTurn(reason string) string {
	ctx := r.ctx
	c := caller{actor: event.ActorOrchestrator, ctx: ctx}
	nudges := 0
	calls := 0
	failures := 0
	for {
		if ctx.Err() != nil {
			return "interrupted"
		}
		calls++
		var msgs []llm.Message
		var seenSeq int64
		var steer string
		r.sync(func() {
			msgs = r.st.OrchestratorView()
			seenSeq = r.st.LastSeq()
			steer = steerOrchestrator(r.st, time.Now(), r.st.ContextTokens(event.ActorOrchestrator), r.cfg.RolloverTokens, reason, calls)
			r.orchCallAt = time.Now()
		})
		if len(msgs) == 0 {
			return "nothing to do"
		}
		reason = ""
		msgs = append(msgs, llm.Message{Role: "user", Text: steer})
		req := llm.Request{
			System:   r.orchestratorSystem(),
			Messages: msgs, Tools: r.orchTools, CacheKey: r.sess.ID + "-orchestrator",
		}
		resp, err := r.completeOrchestrator(ctx, req)
		if err != nil {
			if ctx.Err() != nil {
				return "interrupted"
			}
			r.sync(func() {
				r.append(event.New(event.Error, event.ActorHarness, event.ErrorData{Where: "orchestrator", Text: err.Error()}))
			})
			var ae *llm.APIError
			if errors.As(err, &ae) && ae.ContextOverflow() {
				// Force a rollover: mark the context as full.
				r.sync(func() {
					u := r.st.LastUsage[event.ActorOrchestrator]
					u.Input = r.cfg.RolloverTokens
					r.st.LastUsage[event.ActorOrchestrator] = u
				})
				return "context"
			}
			r.ui.Log("orchestrator: model call failed: %v", shortErr(err))
			failures++
			if failures == 1 {
				// One recovery attempt: the failure is on the record, the
				// history is re-rendered (malformed calls are sanitised), and
				// the model gets to continue.
				r.recordHarnessMessage(event.ActorOrchestrator, "", "Your previous response could not be processed by the model provider ("+shortErr(err)+"). Continue from where you were; if you were mid-way through a tool call, issue it again.")
				continue
			}
			// Pause the session rather than spin: tell the narrator, yield.
			r.sync(func() {
				r.append(event.New(event.Yield, event.ActorOrchestrator, event.YieldData{Done: false, Forced: true, Reason: "the model call failed repeatedly: " + shortErr(err) + ". Resume the session to retry."}))
				r.wakeNarrator(wakeError)
			})
			return "error"
		}
		failures = 0
		r.ui.Stream(event.ActorOrchestrator, "", "", "")
		ev := r.recordAssistant(event.ActorOrchestrator, "", resp, seenSeq)
		_ = ev

		if len(resp.ToolCalls) == 0 {
			if resp.Truncated() {
				r.recordHarnessMessage(event.ActorOrchestrator, "", "Your previous response was cut off by the output limit before any tool call was made. Keep responses short and act through tool calls.")
				continue
			}
			nudges++
			if nudges > 2 {
				r.sync(func() {
					r.append(event.New(event.Yield, event.ActorOrchestrator, event.YieldData{Done: false, Forced: true, Reason: "orchestrator stopped calling tools: " + firstLine(strings.TrimSpace(resp.Text), 200)}))
				})
				return "yield"
			}
			r.recordHarnessMessage(event.ActorOrchestrator, "", "Your reply contained no tool call, so nothing happened and the user saw nothing. If the work is finished and verified, call yield with done=true (after a final note). If you are waiting, call yield with done=false. Otherwise keep working with tools.")
			continue
		}
		nudges = 0

		yielded := ""
		for _, tc := range resp.ToolCalls {
			if ctx.Err() != nil {
				r.recordToolResult(event.ActorOrchestrator, "", tc, "interrupted", true)
				continue
			}
			if !r.allowed(event.ActorOrchestrator, tc.Name) {
				r.recordToolResult(event.ActorOrchestrator, "", tc, fmt.Sprintf("unknown tool %q", tc.Name), true)
				continue
			}
			if tc.Name == "yield" {
				args, err := llm.ArgsObject(tc.Args)
				if err != nil {
					r.recordToolResult(event.ActorOrchestrator, "", tc, err.Error(), true)
					continue
				}
				done, _ := args["done"].(bool)
				reason, _ := args["reason"].(string)
				var blocked string
				r.sync(func() {
					if done && len(r.st.RunningTasks()) > 0 {
						blocked = "cannot declare done while tasks are still running; wait for them (or cancel them) first"
						return
					}
					r.append(event.New(event.Yield, event.ActorOrchestrator, event.YieldData{Done: done, Reason: reason}))
				})
				if blocked != "" {
					r.recordToolResult(event.ActorOrchestrator, "", tc, blocked, true)
					continue
				}
				r.recordToolResult(event.ActorOrchestrator, "", tc, "ok", false)
				if done {
					yielded = "done"
				} else {
					yielded = "yield"
				}
				continue
			}
			out, isErr := r.execTool(c, tc)
			r.recordToolResult(event.ActorOrchestrator, "", tc, out, isErr)
		}
		if yielded != "" {
			return yielded
		}
		// Rollover check between calls.
		var full bool
		r.sync(func() { full = r.needsRollover() })
		if full {
			return "context"
		}
	}
}

// completeOrchestrator calls the orchestrator through its routes.
func (r *Runtime) completeOrchestrator(ctx context.Context, req llm.Request) (*llm.Response, error) {
	return r.completeActor(ctx, event.ActorOrchestrator, "", req)
}

// observer forwards streaming deltas to the UI.
func (r *Runtime) observer(actor, task string) *llm.Observer {
	return &llm.Observer{
		Text:      func(d string) { r.ui.Stream(actor, task, "text", d) },
		Reasoning: func(d string) { r.ui.Stream(actor, task, "reasoning", d) },
		ToolCall:  func(n string) { r.ui.Stream(actor, task, "tool", n) },
		Reset:     func() { r.ui.Stream(actor, task, "", "") },
	}
}

// recordHarnessMessage injects steering into an actor's persisted history.
func (r *Runtime) recordHarnessMessage(actor, task, text string) {
	r.sync(func() {
		r.append(event.New(event.HarnessMessage, actor, event.HarnessMessageData{Text: text}).WithTask(task))
	})
}
