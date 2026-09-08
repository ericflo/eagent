# Contributing

Thanks for looking. eagent is one binary with no runtime dependencies, and
the aim is to keep it that way while it gets better at real work.

## Before you start

Open an issue for anything beyond a small fix. The most useful issues come
with a session: run the thing that went wrong, then attach the relevant
`.agents/eagent/sessions/<id>/*.jsonl` (remove anything private) or the
output of `eagent show <id>`. `docs/LESSONS.md` records what has gone wrong
before and what the harness does about it; a change that reverses one of
those decisions needs a reason.

## Developing

```
make check          # gofmt, go vet, go test -race
make build          # rebuilds the embedded replay assets, then the binary
go run ./cmd/eagent doctor --live
```

Layout: `cmd/eagent` (the CLI), `internal/harness` (the runtime and the three
actors), `internal/state` (the one reducer live and replay share),
`internal/llm` (the three provider protocols), `internal/tools` and
`internal/procs` (tools and the asynchronous shell), `internal/web` (the UI),
`internal/prompts` (the actor prompts as Markdown). `docs/EVENTS.md` is the
log schema; new event types are added, existing ones are never renamed.

The tests run the whole harness against a scripted fake provider and set
`EAGENT_FINALECHAT=off`, so nothing reaches a real account or a real model.
Changing `cmd/eagent-replay-wasm` or the reducer means running
`python3 scripts/build-replay.py` so the committed viewer assets match.

## Pull requests

- One change per PR, with a test where the behaviour can be tested (most
  can: the harness tests and the chaos suites are the model to copy).
- `make check` must pass; CI runs it on Go 1.24 and the current release.
- Keep the README truthful: if a flag, a preset or a default changes, the
  README and `eagent --help` change in the same commit.

By contributing you agree that your contribution is licensed under the MIT
License in `LICENSE`.
