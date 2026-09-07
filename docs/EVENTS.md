# The event log

Every session is a directory of JSONL files under `.agents/eagent/sessions/`.
Each line is one event:

```json
{"seq":14,"ts":"2026-09-07T01:54:03.120Z","type":"assistant","actor":"orchestrator","task":"","data":{...}}
```

| Field | Meaning |
|---|---|
| `seq` | Monotonic within the session, across subsession files. Never reused. |
| `ts` | UTC time the event was appended. |
| `type` | One of the types below. Dotted, stable; new types may be added, existing ones are never renamed. |
| `actor` | `user`, `harness`, `orchestrator`, `task`, or `narrator`. |
| `task` | Task id for task-worker events (`t3`), otherwise absent. |
| `data` | Type-specific payload. |

Lines are appended with `fsync`. Committed lines are never edited. A crash
mid-write leaves a final line without a newline; readers skip it, and the
next writer moves those bytes to `<file>.torn` before appending.

## Types

### Session lifecycle

| Type | Actor | Payload | Notes |
|---|---|---|---|
| `session.start` | harness | `session`, `cwd`, `version`, `interactive`, `models{actor: model}`, `endpoints{actor: base_url}`, `config` | First event of a session. `models` and `endpoints` are the routes actually in use (a fallback may have taken over at startup); `config` names the bundle or preset. |
| `session.resume` | harness | `interactive`, `closed[]` | Appended by `eagent -c`; `closed` lists what the replay had to mark interrupted. |
| `session.end` | harness | `reason` | `done`, `awaiting-input`, `quit`, `interrupted`, `error`. A session can be resumed after any of these. Before it is written, every still-running process gets a `proc.exit` and every queued or running task a `task.end` of status `interrupted`, so the log is consistent at every end. |
| `subsession.start` | harness | `file`, `index`, `reason` (`new` or `rollover`) | First event in every file. |
| `subsession.end` | harness | `reason`, `next_file`, `input_tokens` | Last event in a file that rolled over. |
| `dossier` | harness | `task_id`, `text` | The briefing that opens a rolled-over subsession; rendered as the orchestrator's first message. |

### Conversation

| Type | Actor | Payload | Notes |
|---|---|---|---|
| `user.message` | user | `text` | Wakes the orchestrator. |
| `user.answer` | user | `question_id`, `text` | Answers a `narrator.question`; wakes the orchestrator. |
| `turn.start` / `turn.end` | orchestrator, narrator | `reason` | Brackets one actor turn (one or more model calls). Informational. |
| `assistant` | any model actor | `provider`, `model`, `text`, `reasoning`, `tool_calls[{id,name,args}]`, `native`, `usage{input,output,cached,reasoning}`, `elapsed_ms`, `seen_seq`, `stop` | One model response. `native` is the provider's own representation (Responses output items, Anthropic content blocks) replayed verbatim on later calls. `seen_seq` is the last event the prompt contained; anything after it is delivered on the next call. |
| `tool.result` | same actor | `call_id`, `name`, `output`, `is_error` | Always rendered directly after the call that produced it. |
| `harness.message` | orchestrator or task | `text` | Steering the harness chose to persist (resume notice, nudge after a tool-less reply, recovery after a provider error). |
| `yield` | orchestrator | `done`, `reason`, `forced` | The orchestrator has nothing to do until something happens; `done=true` ends a batch session. `forced` yields are issued by the harness and always count. |
| `note` | orchestrator | `text` | A hint for the narrator; wakes it. |
| `narrator.message` | narrator | `text` | User-visible output. |
| `narrator.question` | narrator | `id`, `text`, `options[]` | Blocks on the user in interactive sessions; ends a batch session with exit code 2. |

### Work

| Type | Actor | Payload | Notes |
|---|---|---|---|
| `task.create` | orchestrator or harness | `id`, `title`, `description`, `kind` (`work` or `dossier`) | Queued immediately. |
| `task.start` | harness | `id` | A worker picked it up. |
| `task.end` | harness | `id`, `status`, `summary`, `turns`, `usage` | `completed`, `failed`, `cancelled`, `interrupted`. Rendered into the orchestrator's history as a message. |
| `proc.start` | orchestrator or task | `handle`, `command`, `cwd`, `timeout_s` | A shell command began. `timeout_s` of 0 means a service. |
| `proc.exit` | same | `handle`, `exit_code`, `reason`, `duration_ms`, `tail`, `notify` | `reason`: `exited`, `killed`, `timeout`, `lost`, `failed`. `notify` is true when the owner had moved on and is told on its next call. |
| `schedule.create` | orchestrator | `id`, `kind` (`loop`, `cron`, `once`), `spec`, `note`, `next` | Keeps the session alive while active. |
| `schedule.fire` | harness | `id`, `note`, `next` | Wakes the orchestrator. `next` empty means the schedule is finished. |
| `schedule.cancel` | orchestrator | `id`, `reason` | |

### Diagnostics

| Type | Actor | Payload | Notes |
|---|---|---|---|
| `route` | harness | `actor`, `provider`, `base_url`, `model`, `reason` | A fallback route took over; the reducer updates the actor's model and endpoint. |
| `error` | harness | `where`, `text` | A model call failed after retries; the narrator sees it. |

## Who sees what

The same log feeds three different prompts:

- **Orchestrator:** `user.message`, `user.answer`, its own `assistant` and `tool.result` events, `task.end` of work tasks, `schedule.fire`, `proc.exit` with `notify`, `harness.message` addressed to it, and the `dossier` — all within the current subsession.
- **Task worker:** the task's `description`, its own `assistant` and `tool.result` events, `proc.exit` for its processes, and `harness.message` addressed to the task.
- **Narrator:** its own turns plus a one-line rendering of everything the orchestrator did (text, tool calls, clipped tool results, notes, task lifecycle, schedules, yields, errors) — batched between its turns so each batch is frozen once the narrator has seen it.

`eagent show <id> --raw` prints every event with its `file:line`. `eagent show <id> --actor task --task t3` prints one worker's private view.
