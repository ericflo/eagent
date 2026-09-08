# Session archives and remote settings

These commands require a FinaleChat server with the artifact and settings-control APIs. Existing conversation mirroring remains independent of both opt-ins.

## Portable archives

```sh
eagent artifact export SESSION_ID --output ./new-archive
eagent artifact verify ./new-archive
eagent artifact restore ./new-archive --output ./new-recovery
eagent artifact publish SESSION_ID
eagent artifact enable
eagent artifact disable
```

Export and verify run locally. Restore verifies every manifest file and copies native source files into a new directory with recovery metadata. It does not start an agent, execute the website, install settings, or replay tool side effects. Existing destinations are never overwritten.

The archive includes the native complete JSONL record prefix, session attachments and outputs, captured settings/provenance, the local settings audit, and a self-contained HTML/JavaScript/Go WebAssembly explorer. The portable and local viewers use the same Go state projection. Download a revision's ZIP, extract it, open `index.html`, and choose the extracted directory to read it offline. The SDK verifies accessed file chunks; `artifact verify` checks the complete archive.

Publish performs one upload without enabling future publication. Enable saves `finalechat.artifacts: true` in the project configuration. Sessions running in this project and previously published sessions are checked approximately every 30 seconds while eagent, its web server, or the connector companion runs. Publication takes a complete snapshot before uploading, persists its intent, and reuses unchanged content-addressed chunks. Each committed manifest is an immutable revision; updating a website does not delete its prior source records.

Archiving uploads native tool arguments/results and session files as well as chat. Source bytes are preserved for recovery, so secrets present in a transcript are preserved too. Configuration snapshots exclude unknown raw keys and credential values; connector credentials and process-control files are not archived. Account tokens are resolved using the existing FinaleChat CLI/environment mechanism.

## Settings control

```sh
eagent connector pair
eagent connector run
```

Pair requests access to this specific project. The printed URL opens FinaleChat's trusted approval screen. The connector credential is stored privately in `.agents/eagent/finalechat-connector.json`. The browser can revoke it. Keep the connector command, an eagent session, or the web server running to receive commands while the model is idle; no inbound port or localhost proxy is needed.

A settings artifact is a captured view by default. Edit the current binding to stage typed changes and review them in FinaleChat's own Save controls. The iframe receives a bounded MessageChannel, never an account token or generic HTTP capability. The local connector validates the approved resource, operation, field classes, schema and current version before writing. Its shared project lock and conditional file writer also serve the local config web API. Stale settings produce a conflict instead of overwriting a newer local edit.

Command intents, acknowledgements and a settings audit survive process restarts. Redelivery reconciles an existing intent; an uncertain delivery with no local journal is reported as unknown and is not executed again. Saved settings and runtime adoption are separate: configuration, prompt and bundle changes are currently reported for new or resumed sessions, with no claim that an existing model turn adopted them.

The current implementation exposes typed project settings plus prompt/bundle operations. Work remaining in this implementation includes the richer local-editor presentation, conditional undo and explicit route-test actions. These are not yet exposed as callable actions in the published descriptor.

## Disable all outbound integration traffic

`EAGENT_FINALECHAT=off` is the process-wide kill switch. `0`, `false`, and `no` also disable it, case-insensitively. It overrides artifact opt-in, forced publish, pairing, and stored connector credentials, including the publisher's final shutdown sweep. Local export, verification and recovery remain available.

Setting only `finalechat.enabled: false` disables ordinary chat mirroring. It does not disable separately opted-in archives or paired settings access. `artifact disable` stops future automatic archives while leaving existing revisions and settings pairing intact. Revoke the connector in FinaleChat to stop its settings access.

## Development checkpoint

Run `EAGENT_FINALECHAT=off go test ./...` and `EAGENT_FINALECHAT=off go vet ./...`. The new integration tests use only loopback HTTP fixtures. Rebuild the embedded reducer after changing event/state/projection code with `python3 scripts/build-replay.py`.

Portable manifest/control definitions and the browser SDK come from the sibling FinaleChat repository. Run its `scripts/sync-eagent-protocol.py /path/to/eagent_final --check` to check synchronization; omit `--check` to update generated copies. No production service needs to be restarted to validate these features.
