# The event log

Every session is a directory of JSONL files under `.agents/eagent/sessions/`.
Each line is one event:

```json
{"seq":14,"ts":"2026-09-07T01:54:03.120Z","type":"assistant","actor":"eagent","data":{...}}
```

| Field | Meaning |
|---|---|
| `seq` | Monotonic within the session, across subsession files. Never reused. |
| `ts` | UTC time the event was appended. |
| `type` | One of the types below. Dotted, stable; new types may be added, existing ones are never renamed. |
| `actor` | `user`, `harness`, `eagent`, or `summarizer`. Older logs also name `orchestrator`, `task` and `narrator` (see *Events in older logs*). |
| `task` | Set only in older logs, on task-worker events (`t3`). |
| `data` | Type-specific payload. |

Lines are appended with `fsync`. Committed lines are never edited. A crash
mid-write leaves a final line without a newline; readers skip it, and the
next writer moves those bytes to `<file>.torn` before appending.

eagent is the one model-driven actor. Some type names come from when a
separate narrator did the talking: `narrator.message`, `narrator.question`
and `narrator.checks` keep those names, and their actor is `eagent`.

What wakes eagent's model is decided by `wakeFor` (`internal/harness/loop.go`).
A wake waits 400 ms for events that arrive together, then makes one call that
answers all of them. The notes below say which events wake it.

## Types

### Session lifecycle

| Type | Actor | Payload | Notes |
|---|---|---|---|
| `session.start` | harness | `session`, `cwd`, `version`, `interactive`, `models{actor: model}`, `endpoints{actor: base_url}`, `efforts{actor: effort}`, `config`, `tz` | First event of a session. The maps name the routes actually in use (a fallback may have taken over at startup): `eagent`, plus `summarizer` when the summarizer is dedicated. `config` names the bundle or preset. `tz` is the IANA zone clock times are rendered in for the model, fixed for the session's life. |
| `session.resume` | harness | `interactive`, `closed[]` | Appended when a session is resumed. `closed` lists the agents that were gone when it reattached. Wakes eagent when `closed` is not empty. |
| `session.end` | harness | `reason` | `done`, `awaiting-input`, `quit`, `interrupted`, `error`. A session can be resumed after any of these. Silent looks not yet counted are written as a `narrator.checks` first. The agents keep running in their terminals for a resume to pick up, except after Stop session, which writes a `relay.end` for each first. |
| `subsession.start` | harness | `file`, `index`, `reason` (`new`, `rollover`, or `upgrade`) | First event in every file. `upgrade` opens the first subsession an older log gets when it is resumed. |
| `subsession.end` | harness | `reason` (`context full`, or `resumed by an eagent with one loop`), `next_file`, `input_tokens` | Last event in a file that rolled over. `input_tokens` is the prompt size that triggered it. |
| `dossier` | harness | `text`, `covers_seq`, `covers_none`, `by` (`eagent` or `harness`), `no_map`, `usage`, `elapsed_ms`, `calls`, `consulted[]`, `task_id` (older logs) | The briefing that opens a rolled-over subsession: where the session stands and a map (`file:line`) of where each topic can be read, followed by facts the harness adds from the log. `covers_seq` is the last event it took in. `covers_none` says it took in none (the session's first request was refused as too long, so no call had seen anything); a log with neither field is an older one that does not say. `by: harness` means the model could not write it and the harness built it from the log. `no_map` marks a body by the model that was kept although it cites too few places to be a map (it was sent back once and came back the same); the body says so in its first lines. `consulted` lists the lookups the writer made (`read FILE:40-90`, `search deploy`); the lookups themselves are not recorded. Its usage counts toward eagent's. |
| `cwd.change` | harness | `path`, `previous` | The folder the session works in, written at start when it is not the project root (`--cwd`, or `cwd` on a phone `session.start`). Agents start there unless told otherwise. Mirrored to the phone as the thread's folder and branch chips. |

### Conversation

| Type | Actor | Payload | Notes |
|---|---|---|---|
| `user.message` | user | `text`, `source` (`web`, `finalechat`, or absent for the terminal), `client` (optional capsule of what the sending client declared it can supply: `source`, `timezone`, `locale`, `device`, `app`, `screen`, `supplies`), `attachments` (files saved under `<session>/attachments/`: `path`, `name`, `content_type`, `size`, `kind`, `width`, `height`, `id`), `route` (`agent` or `eagent`, when the client let the user say whom it is for), `delivery` (`queue`, `interrupt`, `after_turn`), `held`, `phone_id` (the FinaleChat message id) | Wakes eagent. Unless `held` is set, the harness types it into the agent's terminal as it arrives, word for word, before any model call (a `relay.send` with `from: user`, `auto: true`). `held` says why it was kept for eagent instead: it was addressed to eagent, no agent or more than one is running, the agent is not running, or it asks about the session. eagent then answers it or passes it on with `relay_forward`. |
| `user.answer` | user | `question_id`, `text`, `source`, `client`, `attachments` | What the user wrote while a `narrator.question` was open; an option's number is recorded as the option's text. Wakes eagent. |
| `user.dismiss` | user | `question_id`, `source` | The user closed a `narrator.question` without answering (Dismiss on the phone card, the button in the web UI, or `/dismiss`). The question is over and nothing was decided. It wakes nobody; eagent reads that it was dismissed with whatever wakes it next. `ask_user` is refused until the user writes or an agent finishes a turn, blocks or exits. A card dismissed while the session was not running is recorded when the session resumes, before the resuming message, so that message is not taken for the answer. |
| `turn.start` / `turn.end` | eagent | `reason` | Bracket a turn that went on the record. `turn.start` is written when a call first acts, with the wake's reason; `turn.end` gives `done`, `call limit`, `error` or `interrupted`. A look that ends in silence writes neither. |
| `steer` | eagent | `text` | The tail of the prompt for a look eagent acted on: why it was woken, what each agent is doing right now, the steps an agent took since the user last heard, how long that has been, what is expected of the reply. Written right before the `assistant` event it led to. A look that ends in silence leaves no steer. |
| `assistant` | eagent | `provider`, `model`, `asked`, `host`, `text`, `reasoning`, `tool_calls[{id,name,args}]`, `native`, `usage{input,output,cached,reasoning}`, `elapsed_ms`, `seen_seq`, `stop` | One model response that acted (called a tool other than `hold`). `native` is the provider's own representation (Responses output items, Anthropic content blocks), replayed verbatim on later calls to the same `host` (on Responses, only to the same `model`, a dated variant included; on Anthropic, a turn another vendor's model wrote is rebuilt for Claude). `model` is the model the response named and `asked` the one the request asked for (absent in older records, which are held to `model`): `asked` says which route served the call and which model the native items can go back to. `reasoning` is a summary for humans, clipped to 4000 characters. `seen_seq` is the last event the prompt contained; anything after it is delivered on the next call. |
| `tool.result` | eagent | `call_id`, `name`, `output`, `is_error`, `images` | The outcome of one tool call. `images` are the pictures `view_image` brought back, copied under `<session>/attachments/`; they are shown to the model right after the result. A call left without a result by a restart gets one on resume (`interrupted: eagent restarted before this finished`). |
| `narrator.checks` | eagent | `count`, `usage`, `elapsed_ms`, `prompt_tokens`, `first_prompt`, `seen_seq` | Totals for looks that ended in silence since the last such record, so their tokens count in the session's totals. `prompt_tokens` is the size of the last of those prompts and `first_prompt` of the first (a resumed session reads how long its conversation was at its first call from the record, here or from the first `assistant`); `seen_seq` the newest event it had in view, so a resumed session does not look again at what was already weighed. Written after 20 such looks, before a look that acts, before a dossier, and at session end. |
| `answer.owed` | eagent | `seq`, `answer`, `tries`, `started` | What eagent still owes the user, as it stands after this change: `seq` is the oldest message kept for eagent that it looked at and left with no answer and nothing done (the reminders name it and every one kept since), or with `answer` the user's answer to its question that it held on; `seq` 0 says nothing is owed any more (answered, acted on, or given up after the last reminder). `tries` counts the reminder calls arranged, and `started` says reminder number `tries` has gone out. Written when a reminder is arranged, just before it goes out, and when the debt ends; never in a session whose model answers. A resumed session reads the newest one: a waiting reminder is due when it would have been, one that had gone out counts as made, so stopping and resuming does not start the reminders over. It renders nothing in eagent's conversation; an older reader ignores it. |
| `narrator.message` | eagent | `text`, `important` (buzz the phone), `attachments` (copied under `<session>/attachments/`), `auto` | Something eagent told the user. `auto` marks words the harness said without a model call: the outcome of a command the user typed, that the model cannot be reached, a closing report it had to write itself. |
| `narrator.question` | eagent | `id`, `text`, `options[]` | A question for the user, from `ask_user`. A new question replaces one still open. A batch session that ends with one open ends `awaiting-input` with exit code 2. |
| `harness.message` | eagent | `text` | A notice from the harness in eagent's conversation: no agent could be started, a turn stopped at the call limit with an agent still stuck on its screen, an autopilot idle check, an autopilot that reached its `until`. Wakes eagent. |
| `thread.title` | eagent, summarizer, or harness | `title`, `description`, `by`, `pin`, `seen_seq`, `usage`, `elapsed_ms` | The session's title and summary, always of the whole session so far. Each replaces the previous; an empty field leaves that side unchanged. `by: summary` is the running summary: eagent's `update_thread` (actor `eagent`) or the dedicated summarizer's call (actor `summarizer`, with its `usage`). `by` absent with `pin` true is a name the user asked for. `by: finalechat` (actor `harness`) is what the app showed when the mirror attached. A pinned title is kept by later summaries. Mirrored to FinaleChat when the phone is on. |

### Timers

| Type | Actor | Payload | Notes |
|---|---|---|---|
| `schedule.create` | eagent | `id`, `kind` (`loop`, `cron`, `once`), `spec`, `note`, `next` | A timer set with the `schedule` tool. A batch session stays up while one is active. |
| `schedule.fire` | harness | `id`, `note`, `next` | Wakes eagent with the note it left. `next` empty means the timer is finished. A timer that came due while eagent was not running fires once, two seconds after resume. |
| `schedule.cancel` | eagent | `id`, `reason` | A timer cancelled with `cancel_schedule`, which sets no `reason`. |

### Relays

A relay is a coding agent (Claude Code, Codex, Antigravity, pi) that the session runs in a tmux (or screen) terminal eagent owns and passes the user's words to. The agent's own transcript (a Codex rollout, a Claude Code session file) is the source of these events; the terminal is typed into and read for dialogs. Every relay event is written by the harness.

| Type | Actor | Payload | Notes |
|---|---|---|---|
| `relay.start` | harness | `name`, `profile`, `agent` (`claude`, `codex`, `antigravity`, `pi`), `agent_label`, `label`, `dir`, `host` (`tmux` or `screen`), `target` (the terminal session, `eagent-relay-…`), `command`, `inner_id`, `transcript`, `resumed`, `model`, `effort` | Launched, or relaunched continuing the agent's own session. A start under the name of a relay that was never stopped (a restart, a model change), or one with `resumed`, continues that relay's history (turns, last report, sends, autopilot); any other start under a stopped relay's name is a new conversation and keeps only its usage limit. `model` and `effort` are the session's picks over the profile's (empty keeps the profile's, else the agent's own configuration). A resumed eagent reattaches to `target` instead of starting again. |
| `relay.inner` | harness | `name`, `inner_id`, `transcript` | The agent's own session id and transcript path, once found. Recorded again when the agent moves to another session of its own (pi's `/new`, `/resume`, `/fork`, or an extension's `ctx.newSession`). What that session already held when pi opened it is history and is not reported; everything it writes after is, including a turn that finishes before eagent notices the move. |
| `relay.send` | harness | `name`, `id` (`r1`…), `from` (`user` or `eagent`), `seq` (the forwarded user message), `mode` (`queue`, `interrupt`, `after_turn`), `text`, `auto` | Text typed into the agent. `auto` marks a send made with no model call: a user message forwarded as it arrived, or one a typed command made. `from: eagent` sends are mirrored to the phone as typed. |
| `relay.moved` | harness | the fields of `relay.send` (`name` is the relay it goes to now, `id` unchanged), `prev` (the relay it was first sent to) | A send that waited for a switch, or was waiting to be typed when the switch stopped the old agent, goes to the agent that took over. From then on it is that relay's send: its arrival is confirmed under the new name, and the old relay stops waiting for it. Older sessions have none. |
| `relay.delivered` | harness | `name`, `id`, `ok`, `detail` | The transcript shows the text arrived, or (`ok` false) it did not. A failure wakes eagent. One transcript entry confirms each send once. A resumed eagent takes the sends still unconfirmed (from their `relay.send` or `relay.moved`) to the reattached agent, which confirms them from its transcript or, after the usual wait, reports them not confirmed without typing them again; for an agent that is gone, they are reported not confirmed at the resume. |
| `relay.state` | harness | `name`, `state` (`starting`, `working`, `idle`, `blocked`, `exited`), `detail` | `blocked`: a dialog on the screen (or one pi reports itself), not ready 25 s after starting, or no transcript activity for 10 minutes while working. `exited` also records an agent that was stopped to start again and could not start. `blocked` and `exited` wake eagent. |
| `relay.progress` | harness | `name`, `text` | Something the agent said mid-turn. Shown in eagent's conversation; a progress look weighs it. |
| `relay.turn` | harness | `name`, `text` (the agent's final message), `duration_ms`, `tools`, `images`, `interrupted`, `idle` | Wakes eagent with the agent's report. `idle` marks a turn that answered a message eagent sent without running any tools, and adds a reminder that an agent waiting on its own work should not be sent another message. |
| `relay.limit` | harness | `name`, `used_percent`, `window`, `resets_at`, `text`, `exhausted` | The agent's usage budget crossed 50, 80, 95 or 100%, or a limit message appeared. Wakes eagent. |
| `relay.autopilot` | harness | `name`, `instruction`, `until` (RFC3339, empty for "until the user says stop"), `decide`, `off` | A standing order to keep the agent going, set with `relay_autopilot` or `/autopilot`. The steer of every wake that asks eagent to act restates it. While the agent sits idle after a turn with nothing sent to it since, the harness wakes eagent every 10 minutes to look (a `harness.message`). When `until` passes the harness writes `off` and tells eagent to report. |
| `relay.end` | harness | `name`, `reason` | Stopped on purpose, or handed over to another profile. A batch session stays up while any relay is active. |

### Runtime settings

These are written by `internal/harness/settings.go` with the type strings below; they have no payload struct in `internal/event`.

| Type | Actor | Payload | Notes |
|---|---|---|---|
| `settings.runtime_started` | harness | `format`, `generation`, `values`, `persistence` | The session's live settings resource started for this run: its schema, generation and values (`tick_seconds`, `quiet_seconds`, `routes`, `config`, `relays`). |
| `settings.changed` | harness | `format`, `command_id`, `user_id`, `proposal_sha256`, `generation`, `resource_key`, `previous`, `values`, `result` | A settings change from the app, recorded before it is applied and acknowledged. A changed route also writes a `route` event. |
| `session.stop_requested` | harness | `command_id`, `user_id`, `proposal_sha256`, `generation`, `resource_key` | Stop session from the app. The relays are stopped (`relay.end`) and the session ends `quit`. |

### Diagnostics and the phone

| Type | Actor | Payload | Notes |
|---|---|---|---|
| `route` | harness | `actor` (`eagent` or `summarizer`), `provider`, `base_url`, `model`, `reason` | A fallback route took over, or a settings change picked another route. The reducer updates the actor's model and endpoint; the phone's model chip follows eagent's. |
| `phone.question` | harness | `question_id`, `card_id`, `thread_id`, `key` | A question was posted to the phone as a card, so a later run can withdraw the card when the question is answered elsewhere. `thread_id` and `key` (the number of the card's client key) let a later ask of the question start where its cards in that thread left off. |
| `phone.answer_taken` | harness | `card_id` | The answer written on that phone card reached the session (as the answer, or as a message). The message poll and a re-ask of the question leave the card's answer alone from then on, in this run and later ones. |
| `phone.mirror` | harness | `seq`, `message_id` | A user message written at the terminal or in the web UI (`seq`) was copied to the phone thread as `message_id`, so a later run keeps the receipt under it. |
| `images.refused` | harness | `paths` | A provider refused these pictures during a call (undecodable, or over a limit of its own that the file does not show), and the call went through without them. `paths` are the copies kept in the session. From here on the conversation is rendered with a note in place of each, whichever process or route renders it, so the picture is not uploaded again and the request stays the same after a resume. |
| `phone.thread` | harness | `thread_id`, `external_id`, `base_url`, `remote_mode` | The session is mirrored to the user's phone through FinaleChat; `external_id` is `eagent:<session>`. Recorded again, with the new `remote_mode`, when the user flips remote mode mid-session. The phone's live status line is derived from the state and never written to the log. |
| `error` | harness | `where` (`eagent`, `relay`), `text` | A model call failed after its retries, or an agent could not be started. Shown in eagent's conversation; it does not wake eagent. |

## eagent's conversation

eagent's model reads one conversation, rendered from the log by `state.View`
(`internal/state/views.go`) for every call. The rendering depends only on the
log, so each call's conversation extends the one before it and provider
prompt caches keep hitting.

- **Which events.** In a session's first subsession, its own file. After a rollover, the `dossier` as the first message (a `[Briefing…]` header and its text), then every event after its `covers_seq`, including events written to the previous file while the dossier was being composed. With `covers_none` that is the whole record. A dossier with neither field (an older log) is followed by its own file only.
- **eagent's own turns.** Each `assistant` event is an assistant message with its tool calls, followed by their `tool.result`s; a call with no result reads as interrupted. A result belongs to the reply it follows in the log, and two calls of one reply with the same id take its results in order: an id alone does not say which call, because some providers use the same ids in every response. Pictures from `view_image` follow as a user message. Events that arrived while a call was in flight (after its `seen_seq`) are placed after that call's results, which is when the model first saw them. A `steer` renders as the user message it was, right before the reply it led to.
- **What the user wrote.** Each `user.message` renders through the `USER_MESSAGE.md` template as an IRC-style line, `[2006-01-02 15:04:05] <nick> text` (nick from `source`: the terminal is `user`, `web` is `user@web`, `finalechat` is `user@phone`), with a note on any attachments, the client capsule as a trailing `[client line]` when the event has one, and `[Not passed to the agent: <held>.]` when it was held. Each `user.answer` renders through `USER_ANSWER.md` the same way, with the answered question's id, and the question's text when it was asked before the current briefing. Pictures the user sent in common formats go with the message. Timestamps come from the event's own time in the session's `tz`. Both templates can be overridden in `.agents/eagent/prompts/` like every other prompt.
- **Notices.** One user message each for: a `user.dismiss`; a `harness.message`; a `schedule.fire` (with its note); an `auto` `narrator.message`; a `session.resume`; an `error`; a `route` for eagent; a `phone.thread` whose `remote_mode` changed; and the relay events (`relay.start`, `relay.end`, `relay.progress`, `relay.turn` with its report clipped to `report_max_chars`, `relay.limit`, `relay.autopilot`, `relay.state` when `blocked` or `exited`, `relay.delivered` when not `ok`, `relay.send` when `auto`, and `relay.moved`). Other events add nothing.
- **The tail.** After the conversation each call gets the tail (`internal/harness/tail.go`) as one more user message. It is recorded as a `steer` only when the model acts on that look; when it holds, nothing is recorded but a count in `narrator.checks`, and the next call starts from the same bytes.

`eagent show <id>` prints a readable transcript; `--raw` prints every event with its `file:line`, and `--actor eagent` (or `user`, `harness`) one actor's events. `eagent replay <id>` reports the state rebuilt from the log and how many messages eagent's conversation has.

## Events in older logs

Logs written when eagent ran three models still list, show, export, replay and resume. Nothing writes these any more (`internal/event/legacy.go`).

| Actor or type | What it was | How it is treated now |
|---|---|---|
| `orchestrator` | The model that decided and could do the work itself. | Its `assistant`, `tool.result`, `steer` and `harness.message` events are not rendered in eagent's conversation. Its model shows in `eagent show` and in the usage totals; its last prompt size stands in for the context size. |
| `task` | Task workers, with `task` set on their events. | Their events are not rendered in eagent's conversation. `eagent show --actor task --task t3` still prints one worker's events. Their `cwd.change` events are ignored. |
| `narrator` | The model that spoke to the user. | `eagent show` prints its `narrator.message` and `narrator.question` events as eagent's; the dossier index lists them as what the user was told and asked. Summary calls made on its route were written as `thread.title` by the harness, and their usage counts as the narrator's. |
| `task.create`, `task.start`, `task.end` | A task's lifecycle. | `eagent show` prints `task.create` (as `delegate`) and `task.end`; the dossier index lists `task.end`. |
| `note` | A hint from the orchestrator to the narrator. | Printed by `eagent show`; listed in the dossier index. |
| `yield` | The orchestrator had nothing to do (`done` ended a batch session). | Printed by `eagent show` as `DONE` or `waiting`; listed in the dossier index and in the fallback briefing. |
| `proc.start`, `proc.pid`, `proc.exit` | Shell commands the orchestrator and workers ran. | History only: shown by `eagent show --raw`. No process is tracked or stopped on resume. |
| `narrator.trim` | Moved the start of the narrator's view to keep its prompt short. | History only. |
| `cwd.change` from an orchestrator | Every `cd` an actor's shell made. | Only the latest path counts, as the session's folder. |
| `dossier` with `task_id` | A briefing written by a task worker. | Read like any dossier; `by` is absent. |

On resume, a log from the three-actor design with no `eagent` calls is not owed any looks. Before anything else the loop writes a dossier from the record's files, with a map over every old file and the old dossiers included, and opens a new subsession (`reason: upgrade`) with it, so eagent's conversation never has to render the old events. If the model cannot write that dossier, the harness assembles one from what the user was last told, how the old session left things, each agent's last report and the old session's last briefing. `session_search` and `session_read` reach the whole record, old files included. `eagent replay` notes that such a log's tasks and processes are history only.
