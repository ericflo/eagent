# Relay sessions as a first-class feature: plan

Status: proposal, 2026-10-27. Based on every eagent session under `~/Development/**/.agents` (the grid and duel runs were left out: those are benchmarks with a fixed prompt), the Codex rollouts in `~/.codex/sessions` and Claude Code transcripts in `~/.claude/projects` for the relayed work, and the Finalechat repo at `3e2854c`.

## Status (2026-10-28)

Shipped on main and verified:
- **eagent** (`db7b264`, `683b983`, `613d79c`, `93deb5b`): `internal/relay`, a tmux/screen terminal host with Codex and Claude Code transcript parsers, dialog handling and confirmed delivery. Also relay mode as state (`relay.*` events, `RELAY.md`, the dossier header, strict mode), and forwarding on arrival with urgent interrupt. Plus autopilot, restart/upgrade/switch, the relay commands, `eagent relay` / `eagent relays`, the phone thread meta, and running relays in the connector snapshot.
  - The unit and harness tests pass under `-race`.
  - `TestLiveRelay` (`EAGENT_LIVE_RELAY=1`) passes against the real Codex 0.164 and Claude Code 2.1.312.
  - CI is green.
- **Finalechat** (`5049bcc`, live as `main-20261028075729-5049bcc3b830`, checked through `/version`, the epsilon Flux commit `6b3c7dad`, and `rollout status deploy/web`): the New-session agent picker, `GET /version`, "Sent to Codex" bubbles, and Messenger `/new codex …`.

Deliberately not built: idle auto-park. With no polling, an idle relay costs nothing but its terminal process. Also skipped are phone question countdowns (autopilot covers the "waited all day" case) and a pinned ask-ledger card (`/relay` lists what was sent and whether it arrived).

## 1. What the sessions show

Since Sep 21, **17 of the 39 sessions in `~/Development/.agents/eagent/sessions` are relays**, and they hold **682 of your 889 messages (77%)**. Every multi-day session is one of them. Most start with a version of the same paragraph, retyped each time:

> First, cd to /home/ericflo/Development/eagent, then start a new screen session with claude --dangerously-skip-permissions and model set to claude-opus-5-5 @ xhigh. I will communicate with you and you should relay what I say to Claude, never try to do things yourself, you are to act as a relay … Is this clear and understood? Today's session will be about …

The Codex version swaps in `codex --yolo` with `gpt-6-astra @ xhigh`. Some versions add `git pull origin main` first. Four of the 17 add "Close all my existing screen sessions". The rest deliberately don't, because other relays are running.

Parallel relays are normal:
- "There are other agents doing similar right now on your machine, so be careful not to interrupt or clobber their work" (`1790301204795:3`)
- "Are both screens active?" (`1790042922203:37602`)
- One session was told to "close all of my screen sessions, except for 1 new 1", and an older relay was still running afterwards (`1790778586992:50565`).

The newest relay session, `1793168417454`, is the one that asked for this plan.

### How a relay runs today

eagent has no idea a relay exists. The orchestrator works it out again every session using `bash`:

1. It runs `codex --help`, reads `~/.codex/config.toml`, and tries different flags. The epsilon session took about 30 calls and two failed launches before Codex was up. Codex then sat on an update prompt until it got an Esc.
2. It starts the agent with `screen -dm`, types into it with `screen -X stuff '…\r'`, and reads it with `screen -X hardcopy`. That reads the visible screen only, as text.
3. It builds its own polling loop out of `schedule` ("Poll Codex screen session via hardcopy every 90s") and wakes, re-reads, compares and notes, for days.

Across the 17 relay sessions:

| | |
|---|---|
| Orchestrator calls | ~43,000 (bash 19,948 · yield 17,110 · note 6,070) |
| `screen hardcopy` reads | ~18,000 |
| Input tokens | **11.7 B** (97% cached), about $62 at Muse Spark Contributor prices. That is cheap only because of Muse; at mainstream prices the same pattern would cost one to two orders of magnitude more. |
| Worst session (`1792357674674`, behaviour compiler) | 6,415 schedule wakes, 4.0 B input tokens, **22 context rollovers** (each one a dossier rebuild), 3,940 narrator messages in 7 days |
| Epsilon session (`1792971117131`) | 363 orchestrator wakes, while Codex finished 48 turns across all rollouts Oct 25–27 |

### Where it breaks (your words, with `session:seq`)

**Setup is a ritual.** The same 80–100-word incantation opens every relay. Two whole sessions exist only to run `claude update` / `codex update`.

**Reading the screen is lossy.**
- 10 orchestrator notes say Claude's output "scrolled off", for example "relaying verbatim (steps 1-2 scrolled off…)" (`1790301204795:562`). Your reaction: "This whole 'scrolled off' thing is hilarious to me." (`:1206`)
- Claude Code's ghost-text suggestions were taken for questions 42 times. You had to explain: "Those are suggested responses by Claude Code - ignore them" (`1790301204795:1260`), "That is claude auto-suggesting a response" (`1791062358940:31855`).
- In one session you answered **"Send" 29 times** to approve a suggestion (`1790958787858`).
- "Are we sure we're not reading something wrong in the screen session?" (`1792971117131:7462`)

**Typing into the screen is lossy.**
- 31 notes are about text left typed but not submitted, for example "Checklist command was sitting typed-but-unsubmitted for ~12 min" (`1790042922203:18741`).
- The epsilon brief reached Codex as **"oday's mission is…"**: `stuff` dropped the first character (rollout `01a17569…`).

**The relay paraphrases, and paraphrase loses constraints.**
- "Lol you literally just told it 'make it impressive' like that?" (`1790133895932:472`)
- "I want you to be as emphatic with claude as I'm being" → the relay apologised to Codex → "I didn't say to say sorry!" (`1790676344895:1429-1487`)
- The www-vs-next mix-up happened twice: "You spent like 100 dollars on the old damn website!?" (`1790160728016:3350`), "Codex shipped to the old site??????" (`1790676344895:1734`).
- You kept asking for the exact text: "send me the prompt that you sent to codex" (`1792971117131:184`), "tell me what the /goal prompt was, verbatim" (`1790042922203:34982`).

**The relay drifts into doing the work itself.**
- "You are doing WAY too much yourself" (`1790301204795:116`)
- "Have you been doing this work, or asking Claude to do it in screen?" (`1790160728016:10372`)
- "I want you to put codex on the task" (`1790883509974:482`)
- "Remember you're a relay" (`1792971117131:6013`)

The drift clusters after context rollovers and deep into long contexts:
- epsilon: "Remember you're a relay" came 956 events after its dossier.
- copy-pass: "I wanted you to do all the work via claude in screens. I'm very disappointed" came about 3,300 events after its dossier, alongside 41 self-work calls (`1790160728016`).
- Soundboxing: "You have Claude doing all this, right?" came 71 events after a dossier (`1790042922203:11902`).

The dossiers show why:
- The copy-pass dossier never mentions "relay" (0 hits in 14k characters), so the role was simply lost.
- The epsilon dossier mentions it 13 times, but only inside a quoted user message. The fresh subsession's system prompt (`ORCHESTRATOR.md`: delegate, "do small things yourself", verify) pulls the other way.

**The relay contract lives in the conversation, where it decays, instead of in state, where it would persist.**

**There is no way to say how or when a message should be delivered.**
- "queue my note as a steer, don't stop it, but also don't wait" (`1792357674674:989`)
- "Interrupt codex at the next milestone to tell it this" (`:48662`)
- "Is my message from yesterday about Go still queued?" (`:20837`)
- "You cant send it an esc key or something?" (`:138454`)

**Asks get lost.**
- "ok list everything I've told you" (`1790676344895:254`)
- "What about my last message about language choice?" (`1792357674674:3936`)
- "you never got back to me on option A" (`1790301204795:1282`)

**Updates come at the wrong cadence and in the wrong form.**
- Too often: "You really don't need to update me this often" (`1790958787858:60071`).
- Not often enough or not clear enough: "How far along are we now, in your estimation?" ×6, and "I'm really struggling to follow this project any more" (`1792357674674:138116`).
- Images: "Attach them here!!! Don't just give me paths. I'm on my damn phone" (`1790160728016:9363`), "You stopped sending pictures" (`1790042922203:3333`).

**There is no autopilot.**
- "keep it going until I say stop" / "you have until 10am" (`1792894656679:3857`)
- "nudge it if it stops or stalls" (`1790042922203:24909`)
- "Omg you've been waiting all day for me to say something!?" (`1792357674674:116097`)
- "u wasted hours on a question meant to save time" (`1791062358940:39307`)

**Budgets and lifecycle are handled by hand.**
- "We have used 85% of our weekly token budget" (`1790778586992:44163`)
- "13 terminals wow" (`1792357674674:138817`)
- "does the new codex have context?" (`:138883`)
- "switch over to Claude and have that continue where codex left off" (`1790042922203:10432`)
- "I thought we were done here. I thought it was stopped." (`1790778586992:50565`): that session had kept polling for 10 days.

### One root cause

The harness treats Codex and Claude as text in a terminal. A general-purpose model has to rediscover them, scrape them and type into them, so every piece of the workflow is improvised by the model. It is paid for in tokens, and it fails the way models fail: by paraphrasing, drifting, misreading and forgetting.

Meanwhile both CLIs already offer structured interfaces that eagent doesn't use:

| Need | Codex CLI 0.164 | Claude Code 2.1.312 |
|---|---|---|
| Observe | Rollout JSONL in `~/.codex/sessions/…`: `task_started`, `task_complete.last_agent_message` (the turn's own report), `item_completed` (user message delivered), `compacted`, `token_count.rate_limits.primary.used_percent` (the weekly budget, 10080-minute window) | `~/.claude/projects/<cwd-slug>/<uuid>.jsonl`: user/assistant messages, `queue-operation` (message queued while working), `system:turn_duration` (turn end), `compact_boundary`, `cost-state` |
| Know the transcript before launch | Match `session_meta.cwd` and start time, or `session_index.jsonl` | `--session-id <uuid>`: eagent chooses the id, so it knows the path in advance |
| Inject | `codex queue --thread <id> --message …` (native; *verify it reaches a TUI-hosted thread*); bracketed paste into the TUI as a fallback | A Stop hook passed per launch with `--settings` that returns `{"decision":"block","reason":…}` (the same trick Finalechat's own hook uses); paste into the TUI; or headless `-p --input-format stream-json` |
| Resume | `codex resume <id>`, `codex fork` | `claude --resume <uuid>` |
| Inventory | Shared app-server daemon, `codex agents` | `--bg`, `claude agents --json`, `attach`, `logs`, `stop` |

## 2. Design: the relay becomes a seat the harness owns

```
phone ⇄ Finalechat ⇄ eagent harness ──relay adapter──▶ codex / claude (TUI in tmux, or screen)
                          ▲                                   │
                          └── relay.* events ◀── transcript tail (structured)
               (orchestrator wakes on transitions; narrator relays turn reports; phone status)
```

Principles:

1. **The harness owns the mechanics; the model owns judgment.** Launching, reading, typing, confirming delivery and detecting state are Go code. The model decides what to say and when to step in.
2. **Verbatim by default, with a receipt every time.** Your words reach the agent by reference, not by paraphrase. Everything eagent sends shows up on the phone exactly as sent.
3. **Event-driven, not polled.** Wakes come from turn ends, questions, stalls and exits, not timers. Target: about one supervisor wake per inner turn, instead of 7.5 in the epsilon session.
4. **One profile replaces the incantation.**
5. **Relay mode limits the supervisor.** Doing the work itself becomes structurally hard, not a prompt plea.

## 3. eagent changes

### Phase 1: the relay seat (most of the win)

1. **Profiles** in config (`internal/config/config.go`, next to `Finalechat`), with two built in that encode your incantation:
   ```json
   "relay": {
     "profiles": {
       "codex":  {"agent": "codex",  "model": "gpt-6-astra",     "effort": "xhigh", "args": ["--yolo"]},
       "claude": {"agent": "claude", "model": "claude-opus-5-5", "effort": "xhigh", "args": ["--dangerously-skip-permissions"]}
     },
     "on_start": {"git_pull": true, "update": "auto"},
     "strict": true
   }
   ```
   `update: auto` runs the CLI's updater before launch, so the TUI never starts on an update prompt. It skips the update, rather than restarting anything, if another relay is using the same CLI.

   **Relays never touch each other by default.**
   - Each relay belongs to the eagent session that started it: its own tmux session, named after that session id, on eagent's private socket.
   - A relay only reattaches to, types into or stops its own agent.
   - eagent never kills a tmux or screen session it didn't create.
   - Stopping other relays is an explicit, per-start choice: `--stop-others`, or a toggle on the phone that defaults to off. It always lists exactly what it will stop: "stops Codex in SoundboxingWork (session 1792357674674), started 2 days ago".
   - "Close all my sessions" means this session's relays unless you name others.

   The long first message disappears. The profile carries the setup and the relay contract, so the first message is only the brief ("merge all pending PRs, then a deep dependency pass"). It can even be empty, in which case the agent is started and waits for you. On the phone it is one tap on a saved template (F1).
2. **Start paths.** Add `eagent relay codex -C ~/Development/epsilon "Today: merge every pending PR…"` and `eagent --relay claude`. `integration.SessionRequest` (`internal/integration/session_start.go:33`) gains `Relay string`, and the connector advertises the profiles in `snapshot.details` next to `directories` (`projectDirectories`, `session_start.go:173`). The phone and Messenger can then start a relay directly.
3. **`internal/relay`.** An `Adapter` interface (`Launch`, `Send(text, attachments, mode)`, `Interrupt`, `Events`, `State`, `Restart(resume)`, `Stop`) with `codex` and `claude` implementations on one terminal host:
   - The terminal host sits behind its own small interface, **tmux preferred** and screen as the fallback. You're installing tmux on this machine now; screen 5.0.2 stays as the fallback for machines without tmux.
   - tmux runs on a private socket (`tmux -L eagent`), so eagent's relays never collide with your own sessions or other agents' sessions. Each session is named `eagent-<session>-<profile>`.
   - tmux gives exact text injection through `load-buffer` + `paste-buffer -p` (bracketed paste: multi-line, no escaping, no lost first character), then `send-keys Enter`. It also gives `send-keys Escape` to interrupt, `capture-pane -p -S -` for full scrollback when a modal has to be read, `#{pane_dead}` and `#{pane_pid}` for liveness, and `pipe-pane` for a full output log.
   - The screen fallback uses `readbuf <file>` + `paste .`, a large scrollback, and `logfile`.
   - Either way, `eagent relay attach` opens the right session at the desk.
   - Delivery is **confirmed against the transcript**: Codex `item_completed` UserMessage, or Claude `user`/`queue-operation`. If the text doesn't appear within a few seconds, the adapter retries the Enter. That fixes "typed but unsubmitted".
   - Relay processes are services. `eagent -c` reattaches to them instead of killing them, using the pid recorded in `relay.start`.
4. **Observers.** Tail the rollout or transcript and emit normalized events. These are new types in `internal/event/event.go` and `docs/EVENTS.md`, folded by `internal/state`:
   - `relay.start`: profile, cwd, command, screen name, inner session id, transcript path
   - `relay.state`: working, idle, question, blocked, exited
   - `relay.turn`: the agent's final message, duration, files touched, commits
   - `relay.delivered`
   - `relay.compact`
   - `relay.limit`: budget percent, or limit reached
   - `relay.image`: a new image the agent wrote or looked at
   - `relay.end`
5. **Wakes.** The orchestrator wakes on `relay.turn`, a question, blocked, exited or a limit, and on your messages. It never polls. The relay prompt says so, so it stops building `schedule` loops.
6. **The relay-mode orchestrator.**
   - A new embedded prompt, `RELAY.md` (`internal/prompts`), replaces "plan, delegate, verify" with "route, deliver, watch, unblock, report".
   - New tools:
     - `relay_forward(seq, mode)`: sends one of your messages **by reference**, verbatim, with its attachments.
     - `relay_send(text, mode, attachments)`: for eagent's own additions.
     - `relay_status()`: structured state plus recent inner events, never a screen dump.
     - `relay_screen()`: the pane text, as a fallback for modals.
     - `relay_control(action)`: restart, upgrade, stop, switch.
   - With `strict` on, `write_file`, `edit_file` and `delegate` return an error unless your current message was addressed to eagent. `bash`, `read_file` and `view_image` stay for meta work such as disk cleanup, telemetry or finding a thread.
7. **Relay mode is state, not memory.** This fixes "it forgets to relay after a dossier or a long session".
   - `relay.start` is folded by the reducer, and every subsession picks its system prompt and tool list from that state. After a rollover the new subsession still starts as `RELAY.md` plus the relay tools, never as `ORCHESTRATOR.md`.
   - The dossier opens with a **harness-written relay header** generated from state, not from the worker's summary: profile, screen name, inner session id, transcript path, held messages, open asks, autopilot. The worker's prose follows it. `COMPACTION-DOSSIER.md` gets a matching required section.
   - Every relay-mode steer carries one recorded line ("Relay: Codex in `eagent-…-codex`. Forward the user's words; don't do the work."), so the contract sits at the end of the prompt however long the context gets. Because it is recorded as a `steer` event, prompt caching is unaffected.
   - `strict` refusals of `write_file`, `edit_file` and `delegate` don't depend on the model remembering anything.
   - A guard: if the orchestrator's first call after a work-directed message is neither `relay_forward` nor an answer note, the harness steers once ("forward this message or say why not").
8. **Phone status from real state** (`internal/harness/phonestatus.go`): "Codex · running `cargo test` (3m)", "Claude · waiting for you", "Codex · compacting".

### Phase 2: fidelity and control

1. **Receipts.** Every forward or send is mirrored to the thread as a collapsed "→ Codex" bubble showing the exact text. The harness adds a one-line header (`[Eric · phone · 14:02]`) and the absolute paths of attachments. A brief eagent wrote itself is posted the same way, which answers "send me the prompt you sent".
2. **Addressing.** Messages go to the agent by default. A leading `@e`, `eagent:` or "(not for relay)", or Finalechat's composer toggle (`meta["eagent.route"]`), goes to eagent instead. The harness detects the prefix deterministically. The model routes anything ambiguous, and verbatim forwarding is its one-call default.
3. **Delivery modes.**
   - `queue` (default): both TUIs queue a message typed mid-turn.
   - `interrupt`: Esc, wait for idle, then send.
   - `after_turn`: held by the harness until `relay.turn`. Claude uses a per-launch Stop hook (`--settings`) that asks `eagent relay pull` for held messages; Codex uses `codex queue`.

   "Is my message still queued?" is then a state lookup.
4. **Ask ledger.** Every forwarded directive becomes a `relay.ask`: text, time, delivered, acknowledged, done or dropped. The supervisor updates it at turn ends. `/asks` and a pinned card answer "list everything I told you".
5. **Images by default.** New images from the agent (Claude `Read`/`Write` of an image, Codex `view_image`/`generated_images`, screenshot paths in its output) are attached to the narrator's next message, deduplicated by hash. Per-session policy: all, when asked, or never.
6. **Narration for relays.** The agent's turn report is already written for a human, so relay it close to verbatim: keep paths and numbers, and define invented project jargon. Between turns, one progress line at your chosen cadence. The relay default for `narrator_quiet_seconds` becomes 900 instead of 180. It is already tunable live (`internal/runtimecontrol/model.go:61`), so "updates every 5 min" and "stop updating" become one setting, not a prompt.
7. **Modals and stalls.** Read the pane only when the transcript has been silent for N minutes while working, or right after a launch or restart. Recognise the update prompt, trust prompt, autocompact choice, usage limit and transport retry, and handle the known ones. Anything unknown becomes a phone question with the pane text. Ghost suggestions are never forwarded as questions; they can appear as a one-tap quick reply (the 29 "Send"s).
8. **Budget.** Report the Codex weekly `used_percent` at 50/80/95%. When Claude hits its usage limit, pause and ask "wait for reset or use credits?".

### Phase 3: autonomy and lifecycle

1. **Autopilot.** `/autopilot until 10am "<standing order>"` stores an instruction, a deadline and a decision policy (decide / ask), plus a question grace period (default 15m). It then:
   - continues the agent when a turn ends without a question;
   - answers the agent's questions itself, by your stated preferences, once the grace period has passed, and tells you what it chose;
   - nudges or restarts on a stall;
   - wraps up at the deadline.
2. **Question deadlines.** In relay and autopilot mode, phone questions carry a `default` and an `auto_after`. Nothing blocks a working agent for hours on a trivial choice.
3. **Restart, upgrade and resume as one action.** Update the CLI, relaunch, and resume the same inner session id.
4. **Switch agent or start fresh with context.** Build a handoff brief with the dossier machinery (`internal/harness/rollover.go`), pointed at the *inner* transcript, start the new agent with it, and post the brief to the phone.
5. **Multiple named relays per session.** `relay_status` lists them all ("Are both screens active?").
6. **Inventory and cleanup.**
   - A machine-wide registry (`~/.local/state/eagent/relays.json`) backs `eagent relays`, which lists every relay with its owning session, folder, agent and state. Stopping goes by name (`eagent relays stop <name>`); "stop others" is an explicit option, never a default.
   - `/wrap` asks the agent to commit and push, waits for the turn to end, stops the relay, cancels schedules and ends the session.
   - Auto-park: after 12h with the agent idle and no word from you, close the TUI and mark the thread parked. A reply revives it, as finished sessions already do.

## 4. Finalechat changes

The full map is in the agent report; the relevant facts are:
- `session.start` takes only `{prompt, cwd}` (`web/src/components/NewSession.tsx:71-72`, `internal/api/messenger_commands.go:614-618`).
- The draft screen is hard-wired to eagent (`web/src/screens/Draft.tsx:73,100`).
- `meta.via` already renders "via X" under a bubble.
- The status line is ordered by `seq`, so several writers can share it.
- Connectors store only a heartbeat (`internal/db/migrations/0008_control.sql`).
- Codex has no feed besides voluntary MCP calls.

**F1, Start.**
- The draft gains an agent picker (eagent / Codex / Claude Code) with model and effort, a "pull main" toggle, an off-by-default "stop my other relays" toggle that lists exactly which relays it would stop, and **saved templates** such as "Codex relay · SoundboxingWork". The incantation becomes one tap.
- `session.start` gains `relay` (the profile) and `options`, taken from what the connector advertises.
- Draft copy stops being eagent-only.
- Messenger: `/new codex epsilon <brief>`.

**F2, Thread.**
- Identity: "Codex via eagent", with an avatar per agent kind.
- Two status lines on the shared seq: eagent's, and the agent's.
- Collapsed "→ Codex" bubbles for everything sent.
- Header actions wired to connector commands and runtime settings: Status, Interrupt, Updates (off / turn-end / 15m / 1h), Autopilot until…, Restart / Upgrade, Switch agent, Wrap up.

**F3, Composer.**
- A **To: Codex | eagent** toggle.
- A send menu: Queue / Interrupt / After this turn.
- Delivery ticks: queued → delivered → acknowledged.
- Quick-reply chips for agent suggestions.
- Questions show their default and a countdown.

**F4, Machine and ledger.**
- A "Running agents" view per connector, with a stop button per relay, so you can see the parallel relays and stop one on purpose instead of "closing all".
- A pinned ask-ledger card.
- When eagent launches Claude, pass `FINALECHAT_MIRROR=off` so that if Finalechat's own Claude Code hooks are ever installed they don't open a second `claude-code:<sid>` thread. They aren't installed today.

## 5. Shipping it

### Finalechat to production (through epsilon)

How it ships today (checked):

1. **Push to finalechat `main`.** In-cluster Woodpecker runs `.woodpecker.yaml`: gofmt/vet/test, web typecheck and build, CLI selftest. Then BuildKit builds the `Dockerfile`, whose test stage runs the PostgreSQL integration suite and gates the image. It pushes `finalechat:main-<UTC timestamp>-<sha12>` to the shared registry, `registry.clouderic.svc.cluster.local:5000`. That registry was deliberately kept when Clouderic was retired on 2026-10-26 (`epsilon/recovery/clouderic-retirement/2026-10-26/README.md:50-75`). GitHub Actions also runs the Go suite against PostgreSQL.
2. **Flux in epsilon promotes it.**
   - `ImageRepository finalechat` polls every minute.
   - `ImagePolicy finalechat` picks the newest `main-*` tag.
   - `ImageUpdateAutomation epsilon-finalechat` commits `[CI SKIP] Deploy verified image build` to epsilon `main`, rewriting the tag and digest in `apps/finalechat/kustomization.yaml` (`platform/ci-image-automation/image-policies.yaml:55-79`, `:493-`).
3. **The rollout.** `apps/finalechat/web.yaml` defines Deployment `finalechat/web`: 2 replicas, RollingUpdate, `/readyz` and `/healthz` probes. Migrations apply on start.

It serves finalechat.com, www, api, and finalechat.epsilon.maxint.co. The live tag is `main-20261004145833-3e2854c06155`, which is finalechat HEAD `3e2854c`, so production is current.

**Rules for the relay work.**
- **Ship in small slices**, each one a push to `main`: F1, then each F2/F3 control, then F4. Every deploy stays small and easy to revert.
- **Additive in both directions.** During a rolling update an old pod runs against the new schema, and your eagent may be older or newer than the server.
  - New API fields are optional.
  - Relay identity, routes, delivery state, sent-bubble markers and the ask ledger go in thread and message `meta` (jsonb, no migration).
  - Migrations only where the server must query or enforce something, such as question `default`/`auto_after` expiry. They add nullable columns or tables; nothing is renamed or dropped in the same deploy.
  - `session.start` parameters are already connector-defined (actions carry their own parameter schema), so the agent picker is mostly a web change that renders what eagent advertises.
- **Feature gating.** New server behaviour is advertised through the existing deployment feature flags (`internal/api/server.go:53-65`). Relay controls render only for connectors that declare them, so an old eagent sees nothing new. This mirrors eagent's existing rule that it does without features an older Finalechat lacks.
- **Docs in the same commit.** `AGENTS.md`, `docs/API.md`, `docs/openapi.json`, `cli/finalechat` and `skill/finalechat/SKILL.md` are embedded in the binary and served, so they ship with the handlers they describe.
- **Add a version endpoint in the first slice.** Today `/healthz` returns a bare `ok` and nothing on the server reports what's deployed; only the web bundle knows its tag. A `GET /version` (leaving the probes untouched) that returns the image tag lets the relay, and eagent's feature detection, check that live matches what was pushed.

**Verifying each deploy.** An agent can do every step, working from `~/Development/epsilon`, where cluster access lives. Never add kubectl, credentials or manifests to the finalechat repo.
1. The Woodpecker pipeline for the commit is green and the tag `main-…-<sha12>` exists.
2. An epsilon `[CI SKIP] Deploy verified image build` commit names that tag, usually within a few minutes.
3. `kubectl -n finalechat rollout status deploy/web` succeeds, and both pods are Ready on the new digest.
4. `GET /version` returns the new tag.
5. A smoke test on a throwaway `ext:relay-smoke-<ts>` thread: post a message and a question, round-trip the new relay `meta` fields, then delete the thread.
6. On the phone: the draft screen shows the agent picker, and an existing eagent still starts and revives sessions.

**Rollback.**
- Normal path: revert the finalechat commit and push. That is forward-only and goes through the same pipeline.
- Emergency path: suspend `ImageUpdateAutomation epsilon-finalechat`, pin the previous tag and digest in `apps/finalechat/kustomization.yaml`, commit to epsilon, and resume once main is fixed.
- Registry retention keeps the newest 15 `main-*` builds (epsilon `docs/OPERATIONS.md:756-760`). Migrations are expand-only, so a code rollback never needs a down-migration.
- Don't deploy during epsilon maintenance windows, such as a database cutover.

### eagent

eagent is a locally built binary; the sessions record versions like `v0.10.0-6-g3dcc93d-dirty`.
1. Rebuild with `go build -o eagent ./cmd/eagent`. `make release` plus a `v*` tag publishes the GitHub release binaries.
2. Restart long-running `eagent serve` and `eagent connector run` processes. Sessions resume from their logs.
3. Until Phase 1's reattach lands, restarting eagent in the middle of a relay leaves the agent's tmux or screen session running but orphaned. Roll eagent between relays, or ship reattach first.

**Order:** each Finalechat slice deploys and is verified before the eagent change that uses it. eagent detects the feature through `GET /version` or the feature flags and keeps working against the previous server.

## 6. Order, spikes and how to measure

**Spikes first** (about half a day each):
1. Does `codex queue` reach a thread hosted in a live TUI?
2. Does a per-launch Claude Stop hook via `--settings` deliver held text?
3. Does tmux `paste-buffer -p` (and screen `readbuf`/`paste` as the fallback) pass multi-line and Unicode text exactly into both TUIs, with each TUI's queue-while-working behaviour intact?
4. Do the rollout and transcript tailers work against the real files listed above?

**Then:** Phase 1 (eagent only, including reattach) → F1 deployed with `GET /version` → Phase 2 → F2 + F3 deployed as separate slices → Phase 3 → F4 deployed. Each Finalechat slice is verified live (section 5) before the eagent code that uses it ships.

**Measure it like the breakout grid**, from the logs:
- supervisor wakes per inner turn (≤ 1.5)
- hardcopies in steady state (≈ 0)
- first-message length (a profile plus a brief)
- time to brief delivered (< 30s)
- unsubmitted or garbled sends (0)
- "you're a relay" corrections (0)
- narrator messages per day (matches the setting)

## 7. Decisions for you

1. **Routing default.** *Decided (2026-10-28): "make sure it gets the message sooner rather than later".* The harness forwards a message for the agent verbatim the moment it arrives, before any model call; urgent ones ("stop", "wait", "don't…") interrupt first. Messages addressed to eagent (`@e`, "not for relay"), short questions about the relay, relay management, answers, and anything while several relays run stay with the orchestrator, which can still `relay_forward` them.
2. **Claude host.** I recommend the TUI in tmux: it keeps slash commands and `/goal`-style features, and you can peek at the desk. The alternative is headless `stream-json`, which is cleaner but invisible. You said "I'm not in the screen with Claude", which suggests you rarely attach.
3. **Default update cadence for relays.** I'd pick turn ends plus a line every 15 minutes.
