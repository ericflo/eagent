# eagent

Runs a coding agent in a terminal and stands between it and you.

You write, and your words go to the agent as written. eagent watches the agent work, tells you what it is doing and what it finished, clears the dialogs it gets stuck on, and asks you only what is yours to decide. You can do this from a terminal, a local web page, or your phone.

```
eagent relay codex --cwd ~/Development/epsilon "Merge every pending PR, then a deep dependency pass"
eagent relay claude --cwd ~/Development/snowdown     # starts it and waits for your first message
eagent -c                                            # pick the latest session back up
```

## What you get

- **Any agent.** Codex, Claude Code, Antigravity and pi are built in; add your own profile in the project config. The agent runs in a tmux terminal eagent owns (screen when tmux is missing) and keeps running when eagent restarts.
- **Your words, unchanged.** A message reaches the agent the moment it arrives, before any model call. "Stop" and "wait" interrupt it first. Start a line with `@e` to talk to eagent itself; questions about the session ("how's it going?") stay with eagent too.
- **Narration.** While the agent works, eagent says what it is doing every minute or two, from the agent's own transcript. When a turn ends you get the short version and the agent's report.
- **Stuck terminals handled.** Trust prompts, update dialogs and menus are read off the screen and answered. A choice that is yours comes to you as a question.
- **Autopilot.** "Keep it going until 10am" makes eagent send the agent on at every stop, in the spirit of your standing order, and decide its questions the way you would.
- **Timers.** "At 9:00 tell it to deploy", "check back in half an hour".
- **Your phone.** With [FinaleChat](https://www.finalechat.com), every session is a thread: messages, questions with buttons, the agent's screen, settings, and starting new sessions.
- **Nothing lost.** Every event is a line in a JSONL log in your project. `eagent -c` replays it and reattaches to the running agent. When the conversation grows long, eagent restarts it from a dossier: a briefing with a map of `file:line` places in the log, which it can go back and read.

## Install

```
go install github.com/ericflo/eagent/cmd/eagent@latest
```

or clone and `make build`. You need tmux (or screen) and at least one agent CLI installed.

eagent's own model defaults to GLM-5.3 on Together:

```
export TOGETHER_API_KEY=...
eagent doctor --live
```

`eagent --preset NAME` picks another model (`eagent config list` shows them: OpenAI, Anthropic, OpenRouter, DeepInfra, Fireworks, and more).

## Use

```
eagent "add a Makefile with build and test targets"   # interactive, with the default agent
eagent -p "fix the failing test in pkg/store"         # no terminal: exits when the work is done
eagent --relay claude --relay-model sonnet "…"        # choose the agent and its model
eagent relays                                         # every agent running on this machine
eagent relays attach codex                            # watch or type into one yourself
eagent sessions                                       # what has run in this project
eagent show <id>                                      # a readable transcript
eagent                                                # web UI and phone host for this project
```

In a session:

| Type | What happens |
|---|---|
| anything | goes to the agent, word for word |
| `@e …` | goes to eagent instead |
| `/screen` | the agent's terminal |
| `/interrupt`, `/restart`, `/stop-relay` | what they say |
| `/model sonnet high` | change the agent's model and effort; it restarts on the same conversation |
| `/switch claude` | hand the work to another agent, with a brief |
| `/autopilot until 10:00 keep going until the tests pass` | standing order; `/autopilot off` ends it |
| `/notify-done` | buzz the phone when this turn ends |
| `/schedules`, `/status`, `/dismiss`, `/quit` | timers, state, close a question unanswered, stop |

Batch mode (`-p`) exits `0` when the work is done, `2` when it stopped on a question for you (`eagent -c --answer "…"`), `130` on Ctrl-C.

## Web UI and phone

`eagent` (or `eagent serve`) serves a page on `127.0.0.1:7331`: chat, the agent's screen, token usage and cost, the raw timeline, and a settings editor. It also hosts the project for FinaleChat, so sessions can be started, watched and answered from your phone. Set `FINALECHAT_TOKEN` (or run `finalechat login`) to turn the phone on; `EAGENT_FINALECHAT=off` turns it off.

Details: [docs/FINALECHAT-INTEGRATIONS.md](docs/FINALECHAT-INTEGRATIONS.md).

## Configuration

`.agents/eagent/config.json` in the project, or the settings page:

```json
{
  "preset": "anthropic-med",
  "model": { "reasoning_effort": "high" },
  "relay": { "default": "claude" },
  "rollover_tokens": 64000,
  "quiet_seconds": 180
}
```

- One model, with an optional fallback route. It runs at one reasoning effort for every call, so the provider's prompt cache keeps hitting.
- `summarizer` keeps the thread's title and summary: in the same loop by default, or on a small model of its own (`"mode": "dedicated"`).
- `.agents/eagent/INSTRUCTIONS.md` holds standing instructions for the project (`AGENTS.md` is read when there is none).
- `eagent prompts export` copies the prompts into the project, where you can edit them.
- A config file written for the earlier three-model eagent still loads; `eagent doctor` says what was carried over.

## How it works

One model loop sits between you and the agent. The harness around it is plain code: it forwards messages, tails the agent's transcript, keeps the log, and decides when the model needs to look. Each look is the same request as the last one plus what happened since, which is what makes the looks cheap.

[docs/DESIGN.md](docs/DESIGN.md) has the whole design. [docs/EVENTS.md](docs/EVENTS.md) is the log's schema.

## Limits

- No sandbox. The agent runs on your machine as you, with whatever permissions its profile gives it.
- eagent has no shell or file tools of its own. Work in the project is the agent's.
- One machine. A session's log and its terminal live where it started.

## Development

```
make check      # gofmt, go vet, go test -race
make release    # dist/eagent-{linux,darwin}-{amd64,arm64}
```

Bug reports and pull requests are welcome: see `CONTRIBUTING.md`, and `SECURITY.md` for reporting a vulnerability privately. Earlier design documents are kept in `docs/` and marked historical.
