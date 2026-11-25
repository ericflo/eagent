# Meta-harness plan

eagent becomes one loop that sits between the user and a coding agent running
in a terminal. The three-actor design (orchestrator, task worker, narrator) and
the native mode where eagent did the work itself are removed. This document is
the audit that led there and the plan for getting there.

Branch: `meta-harness` in both `eagent` and `finalechat`. Nothing lands on
`main` until the checklist at the end passes.

## 1. What the audit found

### 1.1 How eagent is used

Measured over the last 60 sessions in the main project (54 relay, 6 native):

| Actor | Calls | Input tokens | Cached | Output tokens |
|---|---|---|---|---|
| Orchestrator | 7,795 | 890M | 95.6% | 0.94M |
| Narrator | 7,929 | 732M | 94.6% | 0.97M |
| Summarizer | 789 | 1.6M | 1.8% | 0.79M (0.71M of it reasoning) |
| Task worker | 140 | small | | |

- The task worker ran 10 tasks. Seven were dossiers. None was a delegation the
  user asked for.
- In relay sessions the orchestrator's tool calls were: `yield` 4,254, `note`
  1,100, `relay_status` 982, `relay_send` 643, `relay_screen` 454, `bash` 180
  (nearly all `tmux capture-pane` or reading eagent's own files),
  `relay_forward` 131, `relay_control` 68, `retitle_thread` 59,
  `relay_autopilot` 11, `schedule` 2, `session_*` 0.
- Orchestrator prompt size per call: p50 98k tokens, p90 246k, max 301k.

Three conclusions:

1. Two transcripts of the same session cost about twice the tokens. The
   orchestrator and the narrator each carry the whole history and each is
   called for most events.
2. Prompt size per call is the main cost, and it is set by the rollover
   threshold (300k in most presets).
3. The shell, file, schedule, delegate and session tools do no real work in
   relay sessions. The orchestrator mostly yields.

### 1.2 What changes the prompt prefix today

| Source | Effect |
|---|---|
| Tool list chosen per call (`OrchestratorToolsFor(relayMode, hasRollover)`) | The first `relay_start` and the first rollover change the tools mid-session, which invalidates the whole cache |
| System prompt rebuilt per call, `RELAY.md` vs `ORCHESTRATOR.md` | Switches when the first relay starts |
| Steer text with wall-clock time | Recorded, so stable, but one more message per call |
| Timestamps rendered with `time.Local()` | A resume in another time zone rewrites history |
| Narrator trim | Moves the start of the narrator view (rare by design) |
| Sticky provider fallbacks, image rejection | Flip once and change every later body |
| Project overrides of `USER_MESSAGE.md` | Re-render all past user messages |

The narrator's progress check is the one part already built for the cache: a
check that ends in silence records nothing, so the next check reuses the same
prefix. The new loop generalises it.

### 1.3 The summarizer

`summary.go` runs title and summary updates on the narrator's route through
`summaryClient`, which silently forces reasoning effort to `none` or `minimal`
for some models. It is not in the config, cannot be seen in settings, and its
calls are almost never cached (1.8%).

### 1.4 Relay adapters

Agent names are hard-coded in about 45 places across `relay`, `harness`,
`state`, `config`, `integration` and `settings`. The `agent` interface in
`relay/agents.go` covers launching and parsing; defaults, model lists,
efforts, interrupt keys, upgrade commands and launch hooks are switches
elsewhere.

### 1.5 FinaleChat

The protocol is generic. A fourth profile appears with no FinaleChat change.
The eagent-era leftovers are:

- The new-session draft always offers "eagent" (does the work itself) beside
  the agents eagent can start.
- Messenger does not know `/model`, `/screen` or `/notify-done`, so they can
  be taken as the answer to an open question.
- Docs, comments and test fixtures describe three roles.
- `docs/openapi.json` lacks the `when` field on action inputs.

## 2. Target design

### 2.1 One loop

```
user (FinaleChat, terminal, web UI)
        │  messages, answers, slash commands
        ▼
   ┌─────────┐   look: one model call over one transcript
   │  eagent │   act: speak, ask, forward, press keys, or hold
   └─────────┘
        │  typed text, keys            ▲  transcript lines, screen
        ▼                              │
   coding agent in tmux/screen (Claude Code, Codex, Antigravity, pi)
```

One actor, named `eagent` in the log. Every session relays to a coding agent;
there is no native mode. A session with no profile chosen starts the default
profile (`relay.default`, else the first installed agent).

The loop does four things: passes the user's words on faithfully, tells the
user what the agent is doing, handles what the terminal asks (dialogs, menus),
and answers questions about the session itself. It never does the agent's
work.

### 2.2 Wakes

The loop is idle until something wakes it:

| Wake | When |
|---|---|
| user | A message or answer meant for eagent. A message for the agent is forwarded by the harness on arrival and needs no model call |
| turn | The agent finished or was interrupted |
| attention | The agent is blocked on something, exited, hit a usage limit, or a send was not confirmed |
| progress | A tick while the agent works, when there is something new since the user last heard (existing `moved` / `fresh` / gap rules) |
| quiet | The quiet limit passed while work goes on |
| autopilot | Idle check or deadline under a standing order |
| thread | The title or summary is due (inline summarizer) |
| final | The session is ending |

Wakes that arrive during a turn are merged and served by the next turn.

### 2.3 A turn

A turn is one or more looks, capped by `max_calls_per_turn`.

1. Build the request: static system prompt, fixed tools, the recorded
   transcript, and a tail that is not recorded (section 2.5).
2. Call the model.
3. If the reply is `hold`, or has no tool call, the look leaves no trace.
   Only its token count is kept (`checks` event, batched).
4. Otherwise record the tail as a `steer` event, then the assistant message
   and the tool results, so the transcript reads as what the model saw and
   did.
5. If any call was a look (`relay_screen`, `session_read`, `session_search`,
   `relay_control` returning a screen) or returned an error, go to 1.
   Otherwise the turn ends.

### 2.4 Tools

Twelve tools, same list and order for the whole session:

| Tool | Use |
|---|---|
| `send_message` | Tell the user something (text, important, attachments) |
| `ask_user` | Ask a question, optionally with choices |
| `hold` | Nothing to do or say now |
| `relay_start` | Start or switch the agent |
| `relay_forward` | Pass a user message on verbatim, by reference |
| `relay_send` | Send words of eagent's own (mirrored to the user) |
| `relay_screen` | Read the agent's terminal |
| `relay_control` | Interrupt, restart, stop, press keys |
| `relay_autopilot` | Set or clear a standing order |
| `update_thread` | Set the thread's title and summary |
| `session_search`, `session_read` | Look something up in earlier parts of the log |

Removed: the shell and file tools, `view_image`, `delegate`, `wait`,
`cancel_task`, `complete_task`, `note`, `yield`, `schedule`,
`cancel_schedule`, `retitle_thread`, `relay_status`, `session_list`.

### 2.5 Prompt layout

```
system      EAGENT.md, rendered once per subsession: role, relay contract,
            dialog handling, voice (PERSONA.md), fixed facts. No clock, no
            state.
tools       the twelve, fixed
user        dossier (only after a rollover)
...         transcript, append-only:
              user       observations: user messages, what the agent said
                         and did, harness notices; times come from the event
                         and the session's recorded time zone
              assistant  tool calls
              tool       results
user        tail, rebuilt per look, recorded only if the model acts: the
            time, each relay's state, steps since the user last heard, how
            long the user has heard nothing, standing notices (thread update
            due, autopilot order, context nearly full)
```

Rules that keep the prefix byte-stable:

- The system prompt and tool list are built once per subsession and reused.
  Config or prompt edits take effect at the next subsession.
- Everything that varies goes in the tail, after everything that does not.
- A recorded event always renders to the same text: event time, fixed zone,
  no dependence on current state.
- A silent look adds nothing, so a run of "nothing to report" polls shares
  one prefix. On Anthropic the cache breakpoint sits before the tail, so a
  silent look writes no cache entry for text that will never be read again.
- Agent turn reports are clipped in the transcript (`report_max_chars`); the
  user still gets the full report as its own message.
- One mechanism bounds the prompt: rollover at `rollover_tokens` (default
  64k, down from 150k–300k). The narrator trim is removed.

### 2.6 Compaction and the dossier

When a turn ends with the prompt at or over `rollover_tokens`:

1. One look on the cached prefix with a tail asking for the dossier
   (`DOSSIER.md`). The reply text is the dossier body: what the user wants
   and has asked for, where the work stands, what is open, what the user was
   last told.
2. The harness adds what it knows for certain: each relay's facts (profile,
   model, directory, terminal, how to resume), every user message verbatim
   with `file:line`, and a map of agent turn ends with `file:line`.
3. A new subsession starts. Its transcript is the dossier, then every event
   after the one the dossier call saw, so nothing that arrived during the
   call is lost.

If the model returns nothing usable, one retry, then a dossier built by the
harness alone. `session_read` and `session_search` reach the older files.

Sessions written by the old design resume into a new subsession with a
harness-built dossier. Their relays reattach as before.

### 2.7 Thread title and summary

```json
"summarizer": {"mode": "inline"}
"summarizer": {"mode": "dedicated", "model": {"protocol": "...", "model": "...", "reasoning_effort": "low"}}
```

- **inline** (default): the loop itself calls `update_thread`. At the points
  the current schedule uses (first message, after user messages, agent turn
  ends, long gaps) the tail carries one standard line saying the title or
  summary may be stale and showing the current ones. The model decides. No
  forced tool choice. If no look is due anyway, the notice triggers one.
- **dedicated**: a separate small call on its own route, as today, with the
  model and effort exactly as configured.

| | inline | dedicated |
|---|---|---|
| Input cost | Rides the loop's cached prefix | About 2k uncached tokens per update |
| What it knows | The whole session | A digest the harness builds |
| Transcript growth | About 150 tokens per update | None |
| Timing | With the loop's turn | Independent, a few seconds after a trigger |
| Config | None | One more model to choose |

The hidden effort override is deleted. `eagent summarize` (re-titling stored
sessions) uses the dedicated route if set, else the main model.

### 2.8 Config

```json
{
  "preset": "muse",
  "model": {"protocol": "openai-chat", "base_url": "...", "model": "...", "reasoning_effort": "low"},
  "summarizer": {"mode": "inline"},
  "tick_seconds": 30,
  "quiet_seconds": 180,
  "rollover_tokens": 64000,
  "max_calls_per_turn": 8,
  "report_max_chars": 6000,
  "relay": {"default": "claude", "profiles": {}}
}
```

Migration, applied when a file is read and written back on the next save:

| Old | New |
|---|---|
| `orchestrator` | `model`, with reasoning effort from `narrator` (low by default) |
| `task`, `task_concurrency`, `max_task_turns`, `bash_*`, `tool_output_max_chars` | dropped |
| `narrator` | dropped (its effort carries over, see above) |
| `narrator_tick_seconds`, `narrator_quiet_seconds`, `relay.quiet_seconds` | `tick_seconds`, `quiet_seconds` |
| `max_orchestrator_calls_per_turn` | `max_calls_per_turn` |
| `EAGENT_ORCHESTRATOR_*` | `EAGENT_MODEL`, `EAGENT_PROTOCOL`, ... (old names still read) |
| Preset names | Unchanged; each now names one model at low effort |

### 2.9 Events and old logs

- New sessions write actor `eagent`. `narrator.message`, `narrator.question`,
  `narrator.checks` become `message`, `question`, `checks`; old names are
  mapped when a log is read, so old sessions still list, show and export.
- Removed from new logs: `task.*`, `note`, `yield`, `schedule.*`, `proc.*`,
  `cwd.change`, `narrator.trim`. Readers ignore them in old logs except the
  timeline, which shows every event.
- `session.start` gains `tz`. `relay.start` gains the agent's display name.
- Idle is no longer a `yield`: a session is idle when no turn is open and no
  agent is working.

## 3. Relay adapters and pi

One interface owns everything agent-specific: names, binary, launch and
resume command, session id and transcript location, parser, after-turn
delivery, interrupt keys, dialogs, defaults, model and effort lists,
validation, upgrade command, launch hooks. A registry lists them. A new agent
is one file plus one built-in profile line.

pi (`@earendil-works/pi-coding-agent` 1.6.0):

| Aspect | pi |
|---|---|
| Launch and resume | `pi --session-id <uuid> [--model provider/id] [--thinking level] --approve`; same command resumes |
| Transcript | `~/.pi/agent/sessions/--<cwd>--/*_<id>.jsonl`, created at the first message |
| Turn end | Ambiguous in the transcript (retries, compaction, queued follow-ups), so an extension passed with `-e` writes `agent_start` / `agent_settled` to a status file the seat polls |
| After-turn send | The same extension delivers from a spool when the agent settles |
| Interrupt | One Escape. Never Ctrl+C: twice exits pi |
| Permissions | None to grant; only a project-trust dialog, avoided with `--approve` |
| Models | `pi --offline --list-models`, ids as `provider/model`; defaults from `~/.pi/agent/settings.json` |
| Efforts | off, minimal, low, medium, high, xhigh, max |
| Upgrade | `pi update --all` |

`pi-durable` is out of scope; the pi adapter is written to be embedded.

## 4. FinaleChat

No protocol change. eagent publishes less and FinaleChat renders what it
gets.

- Draft: the "eagent" choice is shown only if the agent still offers it
  (`relay` enum contains `""`). Otherwise profiles only, default first.
- Start retries once on a version conflict.
- The host's name comes from the thread or connector, not the literal
  "eagent". The route pip and the `eagent.route` / `eagent.delivery` keys stay.
- Messenger passes unknown slash commands to the thread.
- Docs, comments, fixtures and `openapi.json` updated.

Compatibility: an old eagent with the new web app still shows the "eagent"
choice. The new eagent with an old web app shows the chip, sends no `relay`,
and eagent starts its default profile.

## 5. Files

**Deleted (eagent):** `harness/orchestrator.go`, `narrator.go`,
`narratorcheck.go`, `taskworker.go`, `exec.go` (shell half), `inbox.go` (if
unused), `tools/files.go`, shell tool defs, `internal/procs`, `internal/sched`,
prompts `ORCHESTRATOR.md`, `RELAY.md`, `TASK-WORKER.md`, `NARRATOR.md`,
`COMPACTION-DOSSIER.md`; the Tasks tab, task drill-down and per-actor pickers
in the web UI and archive viewer; `StartActors`, route picks.

**New (eagent):** `harness/loop.go` (wakes, turns), `harness/tail.go`,
`harness/dossier.go`, `harness/thread.go`, `prompts/EAGENT.md`,
`prompts/DOSSIER.md`, `relay/adapter.go`, `relay/pi.go`, `config/migrate.go`,
`event/legacy.go`.

**Reworked (eagent):** `harness/runtime.go`, `relay.go`, `rollover.go`,
`summary.go`, `prompts.go`; `state/state.go`, `views.go`; `tools/defs.go`;
`config/config.go`, `editor.go`; `settings/*`; `runtimecontrol/model.go`;
`integration/session_start.go`; `projection`; `web` (Go and static);
`archive` viewer and replay WASM; `cmd/eagent`; docs and README.

## 6. Commits

eagent:
1. Relay adapter registry and pi.
2. One loop: events, state, views, prompts, tools, config, cache layout, and
   everything that depended on three actors.
3. In-loop dossier, in-loop thread updates, summarizer config.
4. Docs.

finalechat:
1. Draft, Messenger, host names, docs, fixtures.

## 7. Verification

- [ ] `go test -race ./...`, `go vet`, `gofmt` in eagent with provider keys
      unset; `make check` in finalechat.
- [ ] New tests: loop turn rules, silent looks leave no trace, prefix
      stability across polls and turns (byte comparison of consecutive
      request bodies), fixed tool list, config migration and presets, each
      adapter, registry, dossier (in-loop, fallback, events arriving during
      the call), inline and dedicated summarizer, old-log resume.
- [ ] Old logs: list, show, export and resume real sessions from the old
      design.
- [ ] Live, against a local FinaleChat: start a session with each of the
      four agents; forward, interrupt, after-turn send; a dialog answered
      with keys; `/screen`; `/model`; `/switch`; title and summary appear;
      a forced rollover; cached-token share from provider usage.
- [ ] Browser tests in both repos.

## 8. Deployment

1. Merge `meta-harness` to `main` in finalechat, push; Woodpecker builds, Flux
   promotes. Check the version at `/api/v1/auth/status`.
2. Merge in eagent, push. `make build`, then move the binary into
   `~/.local/bin/eagent` in one step.
3. Restart `eagent serve` by hand (it hosts live sessions; they resume into
   the new loop with their agents still running).
4. Config: nothing to do for a project that only names a preset. Projects
   with `orchestrator` / `task` / `narrator` blocks are migrated on read as in
   2.8; `eagent doctor` prints what was mapped.

Rollback: reinstall the previous binary. New-format sessions are not readable
by the old binary; sessions it started are unaffected.
