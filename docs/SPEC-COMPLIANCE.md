# Spec compliance

A section-by-section reading of the design specification (`CLEANED_SPEC.md`
in the parent project) against what eagent does. "Met" means implemented and
covered by a test or a real run; "Deviates" means a deliberate difference with
the reason.

## 1. Goals

| Requirement | Status | Where |
|---|---|---|
| Single self-contained binary | Met | `go build -o eagent ./cmd/eagent`; stdlib only, no cgo |
| Event sourced; replay reconstructs full state | Met | `internal/store` (JSONL), `internal/state` (one reducer for live and replay); `eagent replay`; `TestInterruptAndResume` |
| Three independent actors with their own jobs, contexts, and tiers | Met | `internal/harness/{orchestrator,taskworker,narrator}.go`; three model routes in `internal/config` |
| Prefix caching first-class | Met | Fixed system prompts per subsession, deterministic history, steering only in the final message, native reasoning replay, `prompt_cache_key`, Anthropic `cache_control`; measured 93% cached on a 55-call run |
| No lossy compaction; dossier subsessions | Met | `internal/harness/rollover.go`; `TestRolloverProducesDossierAndNewSubsession` |
| Asynchronous tool execution with handles | Met | `internal/procs`; every command has a handle from the start |
| Local, checked-in storage under the project | Met | `.agents/eagent/sessions/<session>/<epoch-ms>.jsonl` |

## 2. Sessions and event sourcing

| Requirement | Status | Notes |
|---|---|---|
| One append-only JSONL log per subsession | Met | `store.Session.Append` fsyncs each line; committed lines are never rewritten |
| Crash recovery by replaying every line | Met | A torn final line (crash mid-write) is skipped on read, preserved as `.torn`, and truncated when the session is reopened |
| One log, many readers; per-actor filtering at replay | Met | `state.OrchestratorView`, `TaskView`, `NarratorView` are filters over the same events |
| `.agents/eagent/sessions/<session>/<epoch>.jsonl` layout | Met | Session and subsession ids are zero-padded epoch milliseconds so lexicographic order is chronological |
| Pooling session directories from several machines | Accepted limitation, as in the spec | Nothing merges them; the dossier task is what makes sense of a combined history |

## 3. The three actors

**Orchestrator.** Continuous history within a subsession; delegates with
`delegate`; `note` is the hint tool that wakes the narrator; `schedule`
creates loops (`5m`), cron (`*/15 * * * *`), and one-shots (`in 10m`); the
session does not end while a schedule is active or a task is running; it
uses the largest model of the three.

**Task worker.** A fresh context per task consisting of the task description
and the tools; an agentic loop until `complete_task`; the report is appended
to the log and rendered into the orchestrator's history as a message and into
the narrator's observation stream; it cannot talk to the user (it has no
`send_message` or `ask_user`); mid-tier model.

**Narrator.** Sees the same history as the orchestrator rendered as a compact
observation stream, with its own system prompt and its own final steering
message; wakes on notes, task completions, yields, errors, and periodically
while work is in progress; decides between `send_message`, `ask_user`, and
`hold`; cheapest model. `ask_user` blocks on the answer in interactive
sessions; in batch sessions it is shown and the session exits with code 2 so
the user can answer with `eagent -c --answer`.

**Model assignment (3.4).** *Deviates, configurable.* The spec names GPT-6
Astra for the orchestrator. The default is `zai-org/GLM-5.3` on Together
because the project owner's OpenAI credits ran out during evaluation and asked
for GLM as the default; `--preset astra` restores the spec's routing (OpenAI
direct, OpenRouter `openai/gpt-6-astra` as fallback). Task worker and narrator
are exactly as specified: `zai-org/GLM-5.3-Flash` and
`deepseek-ai/DeepSeek-V4-Flash-0731` on Together.

## 4. Prefix caching

| Rule | Status |
|---|---|
| Never change the beginning of the prompt between turns | Met: system prompt and tool list are computed once per subsession from inputs that do not change (project path, instructions file); history is never edited |
| Steer from the end | Met: `steerOrchestrator`/`steerTask`/`steerNarrator` are appended as the final user message and never persisted |
| Use provider cache controls | Met: `prompt_cache_key` (Responses), `cache_control` breakpoints on system, tools, and the last stable block (Anthropic), stable prefixes for Together's automatic cache |

## 5. Context limits: dossiers instead of compaction

| Requirement | Status |
|---|---|
| New subsession file when the orchestrator hits its limit | Met: threshold `rollover_tokens` (default 150k) read from provider usage after every call; also forced by a provider context-overflow error |
| Orchestrator pauses | Met: no orchestrator turn starts while `rolling` |
| Task worker gets the directory of all subsessions | Met: the dossier task lists the files and has `session_list`, `session_read`, `session_search` |
| Dossier is a brief plus a map into the logs | Met: required `file:line` citations; rejected and retried once without them; harness-built fallback if the retry fails |
| Compaction happens at the start of a subsession, over everything | Met: the dossier task reads all files, every time |
| Two levels of fidelity | Met: the orchestrator has the same session tools and is told to use them |
| Narrator and task worker exhaustion out of scope | Accepted; the narrator restarts with each subsession, workers are bounded by `max_task_turns` |

## 6. Asynchronous bash

| Requirement | Status |
|---|---|
| Single bash tool as the way to act | *Deviates, documented.* `bash` plus `read_file`, `write_file`, `edit_file`, `list_dir`. Models writing 300-line files through heredocs was the largest single source of broken output in the earlier attempts; the file tools are cheaper, safer, and confined to the project for writes |
| Every command runs asynchronously under a handle | Met: `procs.Manager.Start` returns a handle immediately; `bash` then *waits up to* `wait_seconds` and returns either the finished output or the handle. The command is asynchronous either way; the inline wait is a harness convenience that saves a round-trip for short commands |
| Harness decides cancel / send input / extend | Met: `bash_kill`, `bash_write` (stdin, optional EOF), `bash_extend`; default deadline 600s, `timeout_seconds=0` for services |
| No sandboxing | As specified: commands run locally as the invoking user |

## 7. Providers and credentials

| Requirement | Status |
|---|---|
| OpenAI Responses API | Met: `internal/llm/responses.go`, streaming, encrypted reasoning replay, `store:false` |
| Anthropic Messages API | Met: `internal/llm/anthropic.go`, streaming, thinking blocks, cache breakpoints |
| Together AI for worker and narrator | Met via OpenAI-style Chat Completions (`internal/llm/chat.go`), Together's supported protocol |
| `OPENAI_API_KEY`, `TOGETHER_API_KEY`, `OPENROUTER_API_KEY` | Met: read from the environment only; `eagent doctor` reports which are set and can make a live call per route |
| Astra via OpenAI, OpenRouter fallback | Met under `--preset astra`; the fallback fires on unroutable errors such as missing access or exhausted credits, and is recorded in the log |

## 8. Out of scope

Sandboxing, remote execution, and worker/narrator context exhaustion are not
attempted, as the specification says.

## Beyond the spec

- `eagent -c` / `eagent resume` reopen a session after a crash or Ctrl-C and close whatever was left open.
- `eagent show`, `eagent replay`, `eagent sessions`, `eagent doctor --live`, `eagent config list/save/show`, `eagent prompts`.
- `eagent serve`: a web UI over the same logs, with live chat and question answering through a per-session inbox, a session browser, task and tool-call drill-down, a timeline, and configuration (presets, bundles, prompts, key presence, bundle saving).
- Named configuration bundles in `.agents/eagent/configs/`, built-in presets for GLM, Astra, OpenAI (high/med/low), OpenRouter (high/med/low), Anthropic (high: Fable 5.1 / Opus 5 / Sonnet 5; med: Opus 5 / Sonnet 5 / Haiku 4.5), all-DeepSeek, and all-Qwen routings.
- Prompts as embedded Markdown templates with per-project overrides.
- Retry with backoff for transient provider failures; streams that stall or close early are retried, never accepted as fragments; malformed tool calls are sanitised on replay so one bad turn cannot poison a session; oversized tool output is spilled to files the model can page through.
- Interactive sessions accept input while the orchestrator is working; `/status`, `/tasks`, `/procs`, `/kill`, `/schedules`.
- A harness integration test suite drives the whole runtime against a scripted provider, and two chaos suites inject random provider faults and kill/resume loops while checking log invariants.
