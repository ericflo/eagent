# How eagent works

eagent runs a coding agent (Codex, Claude Code, Antigravity, pi, or a profile of your own) in a terminal and stands between that agent and the user. This document describes the design: what is plain code, what the one model is asked to do, and how the two are arranged so that a session can run for days without becoming slow or expensive.

```
 user ── terminal / web page / phone ──┐
                                       ▼
                               ┌───────────────┐   types into,   ┌──────────────────┐
                               │    harness    │ ──────────────▶ │ agent in a tmux  │
                               │  (plain Go)   │ ◀────────────── │ terminal         │
                               └───────┬───────┘  its transcript └──────────────────┘
                                 looks │ ▲ tool calls
                                       ▼ │
                                 one model loop
                                   ("eagent")
```

## The parts

- **The harness** (`internal/harness`) is deterministic code. It forwards the user's messages, tails the agent's transcript, writes the log, runs timers, mirrors the session to the phone, and decides when the model should look.
- **The model loop** is one model with one system prompt and one fixed list of tools. It is called when something happened that needs judgement: what to tell the user, how to clear a stuck terminal, whether to send the agent on. Its name in the log is `eagent`.
- **The agent** is another program in a terminal that eagent owns (`eagent-relay-<session>-<name>` on a private tmux socket; screen when tmux is not installed). It does all the work in the project. It keeps running when eagent stops, and a resumed eagent reattaches to it.

eagent has no shell and no file tools of its own. The only things it can do to the project are the things the agent does.

## The record

Every session is a directory of JSONL files under `.agents/eagent/sessions/` in the project. Each line is one event, appended with `fsync` and never edited. One reducer (`internal/state`) folds the events into the session's state; the running process and `eagent replay` use the same code, so replaying the log rebuilds the session exactly. [EVENTS.md](EVENTS.md) lists the event types.

A session's files are its **subsessions**: a new file starts each time the conversation is restarted from a dossier (below). Nothing is ever removed from earlier files.

## What happens to a message from the user

Routing is code, and it happens before any model call (`routeUserMessage` in `relay.go`):

| The message | What becomes of it |
|---|---|
| Ordinary text, one agent running | Typed into the agent's terminal as written, at once. It is queued behind the agent's current step, unless it opens with "stop", "wait", "don't" and the like, which interrupts first. |
| Starts with `@e`, or the client marked it for eagent | Kept for the model. |
| Asks about the session ("how's it going?", "is it stuck?") | Kept for the model, which answers from the record and the screen without disturbing the agent. |
| No agent is running, or more than one is | Kept for the model, which starts one or picks one and passes the message on with `relay_forward`. |
| A slash command (`/screen`, `/model`, `/restart`, `/switch`, `/autopilot`, `/interrupt`, `/notify-done`, `/stop-relay`, `/schedules`, `/status`, `/dismiss`) | Carried out by the harness. No model call. |

So the agent hears the user even while eagent's own model is slow or unreachable. Each message gets a delivery receipt when the agent's transcript shows it arrived.

A message kept for the model is always answered. If the model holds, it is told once that holding is not an answer; if it still says nothing it is woken again for that message alone (after 15 seconds, a minute, five minutes) until it has answered or acted. A newer message does not cancel the debt: the oldest unanswered one stays owed. An answer to eagent's own question is treated the same way, since nobody but the model has seen it.

## When the model is called

The model is woken by events, never on a bare timer:

- **The user wrote** something that was kept for it, answered a question, or (after a passed message) is owed one short line saying it reached the agent.
- **The agent needs attention**: a dialog it is stuck on, an exit, a message that could not be delivered, a usage limit.
- **The agent finished a turn.** Its report is in the record; the model gives the user the short version, asks what the agent asks, or sends it on.
- **A notice**: a timer the model set came due, or the harness has something to say (a resume, an autopilot deadline).
- **A progress look**, while the agent is in the middle of a turn. Every `tick_seconds` (30) the harness checks whether the agent has done anything new since the last look. If not, no call is made. If so, the model sees the new steps and decides whether to tell the user. A look comes no sooner than 45 seconds after eagent last spoke, and after `quiet_seconds` (180) of silence the model is told to speak.
- **The closing look.** A session with no terminal ends with a final report marked important. It is owed whenever the user wrote after eagent last spoke, it has a minute of its own even when the session is being shut down, and plain text in reply is taken as the report. If the model does not give it, the harness writes one from the agent's last report and says how to pick the session back up. Such a session lives as long as its agent does. It ends `done` (exit 0) when the agent was stopped with nothing open, and `awaiting-input` (exit 2) when a question is open, the model could not be reached, or no agent could be started.

Wakes that arrive together are answered together: the harness waits 400 ms for stragglers, and the call lists every reason.

## One call

Every call sends the same four things:

1. **The system prompt** (`EAGENT.md` with `PERSONA.md`), rendered once per run.
2. **The tool list**, fixed (below).
3. **The conversation**, rendered from the log by `state.View`: the dossier if there is one, then what the user wrote, what the agent reported, what eagent said and did.
4. **The tail**: a last message written for this call only. It says what time it is, why the model was woken, what each agent is doing right now, and any standing order.

The model answers with tool calls. `hold` means nothing needs saying. A tool that only looks (`relay_screen`, `session_search`, `session_read`, `view_image`) gives the model another call in the same turn to use what it saw, and so does an action on the agent (`relay_start`, `relay_control`, `relay_autopilot`), so the model can say how it went. If something arrives while a turn is running, the turn's next call gets a new tail that names it; nothing is marked as seen before the model was shown it. A turn is at most `max_calls_per_turn` (30) calls. If that limit stops a turn while the agent is still stuck on its screen, the model gets one more go, and after that the user is told how to reach the terminal.

A reply is sent back once, off the record, when it cannot stand: text with no tool call (nobody would see it), a reply cut off by the output limit, a message that names eagent's internals to the user, silence on a message addressed to eagent or on an answer to its question, silence on the closing look when the user is owed a last word.

**What is recorded.** When the model acts, the turn goes into the log: the tail it was given (a `steer` event), its reply, the results. When it holds, nothing is recorded but a running count of the tokens spent (`narrator.checks`). A quiet look leaves the conversation exactly as it was.

## Why looks are cheap

Providers cache a prompt by its prefix. eagent is built so that each call's prompt is the previous call's prompt plus something at the end:

- The system prompt and the tool list never change during a run.
- The conversation is rendered from the log, which only grows. Nothing in it is rewritten or summarised in place.
- Everything that changes from call to call is in the tail, which is last. A tail is kept in the log only when the model acted on it, so a kept tail becomes part of the stable prefix and a discarded one costs nothing later.
- **Every call is the same request apart from its messages**: one model, one reasoning effort, one output ceiling, one tool list, one cache key (the session id). Engines render the effort into the prompt or key the cache on it, so a different effort for a different kind of call would throw the cache away. Tests hold this (`TestRequestPrefixIsByteStableAcrossLooksAndTurns`).
- On Anthropic the cache breakpoints sit on the last two stable blocks, so replacing the tail does not move them.

A progress look where nothing is worth saying therefore costs a cached read of the conversation and a few output tokens.

## The tools

| Tool | For |
|---|---|
| `send_message` | Tell the user something; `important` buzzes the phone; files can be attached. |
| `ask_user` | Ask the user, optionally with choices. A new question replaces an open one. |
| `hold` | Nothing to say or do. |
| `relay_start` | Start an agent (profile, folder, model, effort). |
| `relay_forward` | Pass the user's kept message on, as written. |
| `relay_send` | Send the agent words of eagent's own: a nudge, an answer, missing context. |
| `relay_screen` | Read the agent's terminal. |
| `relay_control` | Keys for a dialog, interrupt, restart, upgrade, change model, switch agent, stop. |
| `relay_autopilot` | Set or end a standing order. |
| `schedule`, `cancel_schedule` | A timer that wakes eagent later with a note to itself. A session with a timer set stays alive. |
| `view_image` | Look at a picture the agent made or the user sent. |
| `update_thread` | The thread's title and summary. |
| `session_search`, `session_read` | Find and read lines anywhere in the session's record. |

## The dossier

The conversation grows with everything that happens. When its prompt reaches `rollover_tokens` (64,000), or the provider refuses it as too long, it is restarted: the current file is closed, a new one is opened, and the new conversation begins with a **dossier**. The restart happens at the first quiet moment after the limit is passed, while the prompt is still in the provider's cache, so the user's next message does not wait for it.

A dossier is two things. It says where the session stands (what the user wants, where the work is, what was decided, what is open, what the user last heard). And it is a **map**: for every topic that matters, the places in the record, as `file:line`, where the source can be read. The record itself is never shortened, and the model keeps `session_search` and `session_read`, so when the user comes back to something old the model goes to the lines the map names and reads them. The map covers the whole session, across every file, and each file opens with the dossier that was written when it began, so earlier maps stay reachable too.

How it is written (`dossier.go`):

- The model that has been following the session writes it, at the end of the conversation it already has, so the request reuses the cached prompt.
- It is given a table of every file and an index of the whole record by place: everything before its last dossier in outline (the user's words, the agent's turn ends, where each earlier dossier sits) and the stretch since line by line.
- It studies the record before writing. During the pass only `session_search` and `session_read` run, up to 40 calls, and how much it may read back follows the room in the model's window (between 60,000 and 300,000 characters; up to 600,000 when it works from the files alone). The lookups are part of the writing and are not recorded.
- It is told to rebuild from the record every time. Earlier dossiers are sources to read and check against the lines they cite, not text to carry forward.
- The harness checks the result: it must be a real text, cite at least three places, and every citation must point at a line that exists. A dossier that fails is sent back once with what was wrong. Places that still do not exist are named in the dossier so nothing sends the reader to a missing line. A text that comes back a second time without a map is kept as a summary and labelled as one, in its first lines and in the notice above it; it is never passed off as a map.
- After the model's text the harness adds what it knows exactly, each with its place: the table of files; every agent with its terminal, model and effort; every message the user sent (the first three and the latest 25 word for word, each one between in a line, and in a session with more than 150 between, the oldest by place); the latest turns the agent finished; an open question.
- One failed model call during the pass is made again. If the model still cannot write one, the harness writes the dossier from its records. It has no judgement in it, but every place is still there.
- The pass is bounded: 40 calls and 15 minutes, after which the model is told to write with what it has. While it runs eagent says nothing else; the phone's status line says the session is being reread, and messages still go straight to the agent.
- If the provider refused the conversation as too long, the writer works from the files alone, starting from the dossier the current file opened with.
- What arrives while the dossier is being written still goes to the agent at once and is in the new conversation. So is whatever set the restart off: a message or a turn end that no look had taken in yet is shown whole after the dossier, pictures included.

If the prompt is over the limit on the first call after a restart (the dossier and the project's standing instructions alone are too much), restarting again cannot help. The user is told once per restart, with what to change.

**Sessions from the earlier design.** A log written when eagent ran three models stays as it is and stays readable in every viewer. When such a session is resumed, the first thing the loop does is write a dossier from its files (the old dossiers among them) and continue in a new file. The old model replies are never rendered into the new conversation.

## Title and summary

The thread's title and summary are kept by `summary.go`. By default the loop does it: when they are out of date, the tail says so and the model calls `update_thread` alongside whatever else it does. `update_thread` with neither field means both still fit. A model that says nothing about them is asked again after two minutes, then four, and after that its silence is taken as "they still fit" until there is news. With `"summarizer": {"mode": "dedicated"}` a separate small model writes them from the conversation, and the loop is not asked. A title the user chose is pinned and kept in their words. A session that is about to end waits at most 45 seconds for its last summary.

## When the model fails

A failed call is retried at once, then after a minute, 5, 15 and 30 minutes, then hourly. After the second failure the harness tells the user in its own words that it cannot reach its model and that their messages still go straight to the agent. What asked for the look is still owed one when the model comes back, and a message from the user retries at once. A route with a `fallback` switches to it when the primary is unroutable (bad key, no access, no credits).

## Agents

Everything that differs between agents is behind one interface (`relay.Agent` in `internal/relay/agents.go`): how to start and resume it, where its transcript is and how to parse it, which startup dialogs to answer, which models and efforts it takes. Adding an agent is one file that implements the interface and registers itself, plus a line in the built-in profiles. `claude.go`, `codex.go`, `antigravity.go` and `pi.go` are the four that ship.

What the agent says and does is read from its own transcript, not scraped from the screen. The screen is read only for dialogs and when the model asks to see it.

## Configuration

`.agents/eagent/config.json`, layered over a preset and under `EAGENT_*` environment variables:

| Key | Default | |
|---|---|---|
| `model` | GLM-5.3 on Together | The one model: `protocol`, `base_url`, `model`, `api_key_env`, `reasoning_effort`, `max_tokens`, `context_tokens`, `fallback`. |
| `summarizer` | `{"mode": "inline"}` | Or `dedicated`, with its own `model`. |
| `rollover_tokens` | 64000 | Prompt size at which the conversation restarts from a dossier. |
| `tick_seconds` | 30 | How often to check for something new while the agent works. |
| `quiet_seconds` | 180 | Longest silence while the agent works. 0 turns the limit off. |
| `max_calls_per_turn` | 30 | Calls on one wake. |
| `report_max_chars` | 20000 | How much of an agent's turn report eagent's own prompt carries. The user always gets the whole report, and the rest is in the record. |
| `relay` | | `default` profile and custom `profiles`. |
| `finalechat` | on when a token is found | The phone mirror and session archives. |
| `persona` | | Replaces the voice in `PERSONA.md`. |

Presets name one model each (`eagent config list`). They run it at the effort their orchestrator ran at before: `xhigh`, or the highest the model offers below `max` (`high` for GLM, Kimi, Mistral and DeepSeek). `model.reasoning_effort` sets another level.

Prompts are Markdown files embedded in the binary: `EAGENT.md`, `PERSONA.md`, `DOSSIER.md`, `THREAD-SUMMARY.md`, `USER_MESSAGE.md`, `USER_ANSWER.md`. A file of the same name in `.agents/eagent/prompts/` overrides one.

## Coming from the three-model eagent

eagent used to run an orchestrator, task workers and a narrator. The one loop does the narrator's job and the part of the orchestrator's job that concerned an agent in a terminal.

**Config files still load.** `orchestrator` becomes `model`. The orchestrator's route is carried over as written, so its reasoning effort is kept and now applies to every call. The narrator's effort is never carried over; a file that named no orchestrator effort gets the preset's. `narrator_tick_seconds` and `narrator_quiet_seconds` become `tick_seconds` and `quiet_seconds`, and `max_orchestrator_calls_per_turn` becomes `max_calls_per_turn` (values above 200 are read as 200). `task`, `narrator` and the settings of task workers and shell tools are dropped. A file that does not set `rollover_tokens` gets the new default. Every key that was carried over, changed or dropped has a line in `eagent doctor`, and a run says in one line that there are some.

**Old sessions still work.** They list, show, export and replay as before, and resume as described under the dossier.

**Removed:**

| Gone | Instead |
|---|---|
| eagent doing the work itself: `bash`, file tools, task workers (`delegate`, `wait`, `/tasks`, `/procs`, `/kill`), `task_concurrency`, `max_task_turns`, `bash_*` settings | The agent does the work. A session that names no agent starts the default one. |
| A separate narrator model, which could sit on another provider | One model with a fallback route. The harness itself tells the user when the model is unreachable, and messages still reach the agent. |
| A model per actor, an effort per actor | One model at one effort. |
| `note`, `yield` | `send_message`, `ask_user`, `hold`. |
| `relay_status`, `session_list`, `retitle_thread` | The tail of every call carries each agent's status; every dossier carries the table of files; `update_thread` sets titles. |
| Prompt files ORCHESTRATOR, NARRATOR, RELAY, TASK-WORKER, FACTS, COMPACTION-DOSSIER | EAGENT, PERSONA, DOSSIER. A project's override of a retired name is ignored, and eagent logs that it found one. |
| A log entry for every look that ended in silence | A token count (`narrator.checks`). |
| The dossier writer checking the filesystem | It reads the whole record. |

**Changed behaviour:**

| | Before | Now | Why |
|---|---|---|---|
| Calls on one wake | A reminder at 30, no stop | Stops at `max_calls_per_turn` (30) | One loop with no reviewer above it needs a stop. The stopped turn says so to the user when the agent is still stuck. |
| When the conversation restarts | Just before the orchestrator's next call | At the first quiet moment past the limit | The prompt is still cached then, and the next message does not wait. |
| While the dossier is written | The narrator kept talking | eagent says nothing else for the pass (at most 15 minutes) | One model. Messages still reach the agent and its reports still show. |
| When the model is unreachable | The narrator, on its own route, could still talk | The harness tells the user itself; a `fallback` route covers an outage | One model. |
| "The work is done" buzzing the phone | Automatic once the orchestrator said done | The model marks the message important; the tail asks it to | `yield` is gone, so the harness no longer knows "done". `/notify-done` still forces it. |

**Changed numbers:**

| | Before | Now | Why |
|---|---|---|---|
| `rollover_tokens` default | 150,000 | 64,000 | Every look re-reads the conversation from cache, so its length is the price of each look. A dossier loses nothing, since the record is kept whole and mapped. |
| Reasoning effort | Orchestrator `xhigh` (or `high`), narrator `low` | One for every call: the orchestrator's | A call's effort is part of what the cache is keyed on, so the frequent looks the narrator made at `low` now run at the one effort too. |
| Output ceiling | Per actor | 32,768 for every call | Same reason. |
