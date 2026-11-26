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
| A slash command (`/screen`, `/model`, `/restart`, `/switch`, `/autopilot`, `/interrupt`, `/notify-done`, `/stop-relay`, `/schedules`, `/status`, `/dismiss`) | Carried out by the harness. No model call. From the phone or the web, while an agent runs, `/status` and `/schedules` (and `/help`) go to the agent instead, since eagent's own output for them shows only in its terminal. While an agent runs, `/dismiss` with no question of eagent's open goes to the agent too. With none running, a line from the phone or the web that starts with a slash and is not one of these (a path, an agent's own command) is kept for the model. |

So the agent hears the user even while eagent's own model is slow or unreachable. Each message gets a delivery receipt when the agent's transcript shows it arrived.

A message kept for the model is always answered. If the model holds, it is told once that holding is not an answer; if it still says nothing it is woken again for that message alone (after 15 seconds, a minute, five minutes) until it has answered or acted. A newer message does not cancel the debt: the oldest unanswered one stays owed. An answer to eagent's own question has also reached nobody but the model, so a hold on it is sent back once with that said. When the turn has no call left for that, the answer is brought up once more a little later. A hold after that stands: an answer can need nothing done.

## When the model is called

The model is woken by events, never on a bare timer:

- **The user wrote** something that was kept for it, answered a question, or (after a passed message) is owed one short line saying it reached the agent.
- **The agent needs attention**: a dialog it is stuck on, an exit, a message that could not be delivered, a usage limit.
- **The agent finished a turn.** Its report is in the record; the model gives the user the short version, asks what the agent asks, or sends it on.
- **A notice**: a timer the model set came due, or the harness has something to say (a resume, an autopilot deadline).
- **A progress look**, while the agent is in the middle of a turn. Every `tick_seconds` (30) the harness checks whether the agent has done anything new since the last look. If not, no call is made. If so, the model sees the new steps and decides whether to tell the user. A look comes no sooner than 45 seconds after eagent last spoke, and after `quiet_seconds` (180) of silence the model is told to speak.
- **The closing look.** A session with no terminal ends with a final report marked important. It is owed whenever the user wrote after eagent last spoke, it has a minute of its own even when the session is being shut down, and plain text in reply is taken as the report. If the model does not give it, the harness writes one from the agent's last report and says how to pick the session back up. Such a session lives as long as its agent does. It ends `done` (exit 0) when the agent was stopped with nothing open, and `awaiting-input` (exit 2) when a question is open, the model could not be reached, or no agent could be started. With no phone either (`eagent -p`), nobody can write to it while it runs: eagent is told to stop the agent once the asked work is done and reported, and a question ends the session at once with the agent left running, for `eagent resume --answer` to pick up.

Wakes that arrive together are answered together: the harness waits 400 ms for stragglers, and the call lists every reason.

## One call

Every call sends the same four things:

1. **The system prompt** (`EAGENT.md` with `PERSONA.md`), rendered once per run.
2. **The tool list**, fixed (below).
3. **The conversation**, rendered from the log by `state.View`: the dossier if there is one, then what the user wrote, what the agent reported, what eagent said and did.
4. **The tail**: a last message written for this call only. It says what time it is, why the model was woken, what each agent is doing right now, and any standing order.

The model answers with tool calls. `hold` means nothing needs saying. A tool that only looks (`relay_screen`, `session_search`, `session_read`, `view_image`) gives the model another call in the same turn to use what it saw, and so does an action on the agent (`relay_start`, `relay_control`, `relay_autopilot`), so the model can say how it went. If something arrives while a turn is running, the turn's next call gets a new tail that names it; nothing is marked as seen before the model was shown it. Each reply is recorded with the newest event its call was shown, and the renderer puts anything newer after that reply, so every later prompt has each line where the model first saw it. A turn is at most `max_calls_per_turn` (30) calls. If that limit stops a turn while the agent is still stuck on its screen, the model gets one more go, and after that the user is told how to reach the terminal.

A reply is sent back once, off the record, when it cannot stand: text with no tool call (nobody would see it), a reply cut off by the output limit, a message that names eagent's internals to the user, silence on a message addressed to eagent or on an answer to its question, silence on the closing look when the user is owed a last word. A reply that did something else instead (it set the title and nothing more) is told the same, once. The second try is given the conversation the first one had, plus the reply and the reason. Nothing is sent back on the last call a turn may make (`max_calls_per_turn`): no call is left to answer it, so the reply stands as it came and the look is settled. A message addressed to eagent that is still unanswered then is asked for again later, after 15 seconds, a minute and five minutes, and then eagent's log says it went unanswered. The debt is on the record (`answer.owed`), so a stop and resume keeps it and the reminders already made count.

**What is recorded.** When the model acts, the turn goes into the log: the tail it was given (a `steer` event), its reply, the results. When it holds, nothing is recorded but a running count of the tokens spent (`narrator.checks`). A quiet look leaves the conversation exactly as it was.

## Why looks are cheap

Providers cache a prompt by its prefix. eagent is built so that each call's prompt is the previous call's prompt plus something at the end:

- The system prompt and the tool list never change during a run.
- The conversation is rendered from the log, which only grows. Nothing in it is rewritten or summarised in place.
- Everything that changes from call to call is in the tail, which is last. A tail is kept in the log only when the model acted on it, so a kept tail becomes part of the stable prefix and a discarded one costs nothing later.
- **Every call is the same request apart from its messages**: one model, one reasoning effort, one output ceiling, one tool list, one cache key (the session id). Engines render the effort into the prompt or key the cache on it, so a different effort for a different kind of call would throw the cache away. Tests hold this (`TestRequestPrefixIsByteStableAcrossLooksAndTurns`).
- On Anthropic the cache breakpoints sit on the last two stable blocks, so replacing the tail does not move them. Anthropic finds an earlier cache entry only about twenty blocks behind a breakpoint, so when more than that arrived since the last call a third breakpoint sits where that call left its entry. The system prompt's breakpoint covers the tools, which keeps a request within the four it may carry.

A progress look where nothing is worth saying therefore costs a cached read of the conversation, plus what the model writes before it holds. Output is never cached, and how much a model thinks before a `hold` follows the session's effort and the model: little on some, a good deal on others. `model.reasoning_effort` is the setting for it. A look is made only when the agent is working and there is news since eagent last looked, at the earliest 45 seconds after it last spoke; a tick with nothing new makes no call.

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
| `view_image` | Look at a picture the agent made or the user sent. The file must be where the session works, or one an agent or the user named. |
| `update_thread` | The thread's title and summary. |
| `session_search`, `session_read` | Find and read lines anywhere in the session's record. One result is at most 24,000 characters; a line longer than that is read in pieces, each result saying which `offset` reads on. A search matches the text of an event's fields as a person reads it, ignoring case and treating line breaks as spaces; it does not look inside the provider's replay payload. A hit whose match the result line does not show says which `offset` (and `raw=true`, when only the JSON form has it) reads the match. |

## The dossier

The conversation grows with everything that happens. When its prompt fills half the context window of the model in use (100,000 tokens on a 200k model, 500,000 on a 1M model), or the provider refuses it as too long, it is restarted: the current file is closed, a new one is opened, and the new conversation begins with a **dossier**. The restart happens at the first quiet moment after the limit is passed, while the prompt is still in the provider's cache, so the user's next message does not wait for it.

A dossier is two things. It says where the session stands (what the user wants, where the work is, what was decided, what is open, what the user last heard). And it is a **map**: for every topic that matters, the places in the record, as `file:line`, where the source can be read. The record itself is never shortened, and the model keeps `session_search` and `session_read`, so when the user comes back to something old the model goes to the lines the map names and reads them. The map covers the whole session, across every file, and each file opens with the dossier that was written when it began, so earlier maps stay reachable too.

How it is written (`dossier.go`):

- The model that has been following the session writes it, at the end of the conversation it already has, so the request reuses the cached prompt.
- It is given a table of every file and an index of the whole record by place: everything before its last dossier in outline (the user's words, the agent's turn ends, where each earlier dossier sits) and the stretch since line by line.
- It studies the record before writing. During the pass only `session_search` and `session_read` run, up to 40 calls, and how much it may read back follows the room in the model's window (up to 300,000 characters; up to 600,000 when it works from the files alone). A conversation that leaves no room for even one lookup (24,000 characters) is not written in: the writer works from the files instead. The lookups are part of the writing and are not recorded.
- It is told to rebuild from the record every time. Earlier dossiers are sources to read and check against the lines they cite, not text to carry forward.
- The harness checks the result: it must be a real text, cite at least three distinct places that exist (a place is a file and the line reading starts at, so one cited again counts once), and every citation must point at a line that exists. A dossier that fails is sent back once with what was wrong. Places that still do not exist are named in the dossier so nothing sends the reader to a missing line. A text that comes back a second time without a map is kept as a summary and labelled as one, in its first lines and in the notice above it; it is never passed off as a map.
- After the model's text the harness adds what it knows exactly, each with its place: the table of files; every agent with its terminal, model and effort; every message the user sent (the first three and the latest 25 word for word, each one between in a line, and in a session with more than 150 between, the oldest by place); the latest turns the agent finished; an open question.
- One failed model call during the pass is made again. A call the provider refuses as too long is not repeated as it was: the lookups that did not fit are taken out and the model writes without them, and if the conversation itself does not fit, the model writes from the files. If the model still cannot write one, the harness writes the dossier from its records. It has no judgement in it, but every place is still there.
- The pass is bounded: 40 calls and 15 minutes, after which the model is told to write with what it has. The writing then has 8 minutes more, two calls' worth, so one deadline ends the whole pass within 23 minutes: every call, and the wait before a failed one is made again, ends by it. A call begun in the reading time still runs its full four minutes (it may be the model writing its briefing unasked), and a writing call asked for after it has four of its own. A pass that reaches the deadline with a draft in hand keeps the draft, labelled as a summary if it is no map; one with none gets the harness's. While it runs eagent says nothing else; the phone's status line says the session is being reread, and messages still go straight to the agent.
- If the provider refused the conversation as too long, the writer works from the files alone, starting from the dossier the current file opened with.
- What arrives while the dossier is being written still goes to the agent at once and is in the new conversation. So is whatever set the restart off: a message or a turn end that no look had taken in yet is shown whole after the dossier, pictures included. eagent's own calls from the replaced conversation are not shown again; the dossier was written with them in view.

If the prompt is over the limit on the first call after a restart (the dossier and the project's standing instructions alone are too much), restarting again cannot help. The user is told once per restart, with what to change.

**Small windows.** Everything above is sized against what a request on the route in use can hold: its window less the reply ceiling, less the system prompt and the tools. On every ordinary cloud model that changes nothing. On a small local route (under 64k tokens of room past the system prompt and tools; a 16k window, say) the sizes are held against what the provider counts, which can be well above eagent's own estimate: the ratio is learned from the sizes the provider reports for eagent's calls, or from a request it refused as too long when it reports none (until a call has taught it, as on the first call of a run, it is taken as 1). The restart comes at three quarters of the room past the system prompt and tools; of what the conversation may fill before it, the dossier takes at most a third and what is carried over whole at most a sixth. When the facts would not fit, the older messages and turn ends give way first, to their `file:line` places. Then the dossier leaves the record before the briefing its conversation opened with to that briefing: the earlier files as one range and a pointer to it by `file:line`, and only what came since by place. Each briefing maps everything before it, by its own lists or by the one it points at, back to the start, so the narrowest dossier does not grow with the record. The index a pass from the files is given loses its oldest lines the same way (session_search finds them), so the pass can still be written. A route whose window cannot hold the system prompt and tools with room for a briefing and a message is said to be so in the log, once, and is not restarted again.

**A restart that was stopped.** If eagent stops in the middle of a restart, the next run finishes it before anything else. An empty next file is let go; a closed file with no next one has that file begun; a new file with no dossier yet is not the conversation: the conversation stays the one it was replacing, with everything since, until the dossier is written. A session of the earlier design whose takeover stopped before its dossier is taken over again.

**Sessions from the earlier design.** A log written when eagent ran three models stays as it is and stays readable in every viewer. When such a session is resumed, the first thing the loop does is write a dossier from its files (the old dossiers among them) and continue in a new file. The old model replies are never rendered into the new conversation.

## Title and summary

The thread's title and summary are kept by `summary.go`. By default the loop does it: when they are out of date, the tail says so and the model calls `update_thread` alongside whatever else it does. `update_thread` with neither field means both still fit. A model that says nothing about them is asked again after two minutes, then four, and after that its silence is taken as "they still fit" until there is news. With `"summarizer": {"mode": "dedicated"}` a separate small model writes them from the conversation, and the loop is not asked. A title the user chose is pinned and kept in their words. A session that is about to end waits at most 45 seconds for its last summary.

## When the model fails

A failed call is retried at once, then after a minute, 5, 15 and 30 minutes, then hourly. After the second failure the harness tells the user in its own words that it cannot reach its model and that their messages still go straight to the agent. What asked for the look is still owed one when the model comes back, and a message from the user retries at once. A route with a `fallback` switches to it when the primary is unroutable (bad key, no access, no credits).

## Agents

Everything that differs between agents is behind one interface (`relay.Agent` in `internal/relay/agents.go`): how to start and resume it, where its transcript is and how to parse it, which startup dialogs to answer, which models and efforts it takes. Adding an agent is one file that implements the interface and registers itself, plus a line in the built-in profiles. `claude.go`, `codex.go`, `antigravity.go` and `pi.go` are the four that ship.

What the agent says and does is read from its own transcript, not scraped from the screen. The screen is read only for dialogs and when the model asks to see it. pi says itself when it waits on a dialog (its extension writes it to a status file); the relay is `blocked` until the dialog ends.

Starting an agent, starting it again and handing over to another one can take a minute (a `git pull`, the terminal, the agent's own startup), so none of it runs on the loop: the loop claims the name, the launch runs beside it, and the result is recorded when it is in. A message sent to an agent that is starting again waits and is typed into the new one. An agent that was stopped to start again and could not start is recorded as `exited` with the reason, which wakes eagent. News from a terminal that has been replaced is dropped.

## Configuration

`.agents/eagent/config.json`, layered over a preset and under `EAGENT_*` environment variables:

| Key | Default | |
|---|---|---|
| `model` | GLM-5.3 on Together | The one model: `protocol`, `base_url`, `model`, `api_key_env`, `reasoning_effort`, `max_tokens`, `context_tokens`, `fallback`. `context_tokens` follows the model: a file, bundle or environment variable that names another model without a window of its own gets that model's, from the catalog or the provider's cached list. |
| `summarizer` | `{"mode": "inline"}` | Or `dedicated`, with its own `model`. |
| `rollover_tokens` | 0 | Prompt size at which the conversation restarts from a dossier. 0 follows the model in use: half its context window, in whole thousands (less on a small window, see the dossier), and it moves if the model changes mid-session. A number fixes it. |
| `tick_seconds` | 30 | How often to check for something new while the agent works. |
| `quiet_seconds` | 180 | Longest silence while the agent works. 0 turns the limit off. |
| `max_calls_per_turn` | 30 | Calls on one wake. |
| `report_max_chars` | 20000 | How much of an agent's turn report eagent's own prompt carries. The user always gets the whole report (on the phone, one longer than a FinaleChat message takes arrives in numbered parts), and the rest is in the record (up to 1 MB a report). |
| `relay` | | `default` profile and custom `profiles`. |
| `finalechat` | on when a token is found | The phone mirror and session archives. |
| `persona` | | Replaces the voice in `PERSONA.md`. |

Presets name one model each (`eagent config list`). They run it at the effort their orchestrator ran at before: `xhigh`, or the highest the model offers below `max` (`high` for GLM, Kimi, Mistral and DeepSeek). `model.reasoning_effort` sets another level. `astra` and `openai-med` ran the same orchestrator as `openai-high` and differed only in the task worker's model, so both names now load `openai-high`; `eagent doctor` says so.

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
| While the dossier is written | The narrator kept talking | eagent says nothing else for the pass (at most 23 minutes) | One model. Messages still reach the agent and its reports still show. |
| When the model is unreachable | The narrator, on its own route, could still talk | The harness tells the user itself; a `fallback` route covers an outage | One model. |
| "The work is done" buzzing the phone | Automatic once the orchestrator said done | The model marks the message important; the tail asks it to | `yield` is gone, so the harness no longer knows "done". `/notify-done` still forces it. |

**Changed numbers:**

| | Before | Now | Why |
|---|---|---|---|
| `rollover_tokens` default | 150,000 (300,000 in eleven presets) | Half the model's context window: 100,000 on a 200k model, 500,000 on a 1M model | The size follows what the model can hold. The other half is room for the reply and for the dossier pass, which is now written inside the conversation it replaces and reads the record on top of it. |
| Reasoning effort | Orchestrator `xhigh` (or `high`), narrator `low` | One for every call: the orchestrator's | A call's effort is part of what the cache is keyed on, so the frequent looks the narrator made at `low` now run at the one effort too. |
| Output ceiling | Per actor | 32,768 for every call | Same reason. |
