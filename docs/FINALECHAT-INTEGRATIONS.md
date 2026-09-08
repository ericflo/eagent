# Session archives and remote settings

These commands require a FinaleChat server with the artifact and settings-control APIs. Existing conversation mirroring remains independent of both opt-ins.

## Portable archives

```sh
eagent artifact export SESSION_ID --output ./new-archive
eagent artifact verify ./new-archive
eagent artifact restore ./new-archive --output ./new-recovery
eagent artifact publish SESSION_ID
eagent artifact publish SESSION_ID --recreate
eagent artifact enable
eagent artifact disable
```

Export and verify run locally. Restore verifies every manifest file and copies native source files into a new directory with recovery metadata. It does not start an agent, execute the website, install settings, or replay tool side effects. Existing destinations are never overwritten.

The archive includes the native complete JSONL record prefix, session attachments and outputs, captured settings/provenance, the local settings audit, and a self-contained HTML/JavaScript/Go WebAssembly explorer. The portable and local viewers use the same Go state projection. Download a revision's ZIP, extract it, open `index.html`, and choose the extracted directory to read it offline. The SDK verifies accessed file chunks; `artifact verify` checks the complete archive.

The manifest records the committed event frontier and attachment completeness. `context/attachments.json` identifies included, unavailable and size-mismatched native references; an incomplete checkpoint is labeled explicitly. The settings audit is captured through its last complete record with bounded, confined reads. `context/pricing.json` freezes the exporter’s catalog rates, including unknown routes, and the replay uses those rates even with an updated renderer. These are estimates at capture time; historical invoices and unrecorded past prompt/configuration values cannot be inferred from current defaults.

FinaleChat pins an immutable revision by default. **Follow latest** accepts compatible snapshots and keeps the viewer's section, search, actor/task filters, event position and scroll. Changed renderer bytes require an explicit selection. A different saved viewer can also be paired with the same historical dataset; source/context bytes and the original download are preserved, and the composed website has no settings authority. Presentation state stays in the open container through the SDK's bounded `viewState` methods.

The browser loads at most 50,000 events, 32 MiB of JSONL or 128 source files and labels a limited prefix. Go WebAssembly replays that same prefix without fetching it twice; elapsed time stops at the selected event. Complete source files and the captured full summary remain available in the archive independently of the display bound.

Mirrored messages carry a dataset/session/sequence anchor. FinaleChat's **Inspect this moment** opens that event in a fixed revision, while **Find chat message** asks the trusted container for a link back to the same thread. Earlier messages with only `meta.seq` remain supported. Events outside the loaded prefix, not yet captured, or never mirrored to chat are reported as unavailable; a navigation hint cannot change settings or execute a workflow.

Publish performs one upload without enabling future publication. Enable saves `finalechat.artifacts: true` in the project configuration. Sessions running in this project and previously published sessions are checked approximately every 30 seconds while eagent, its web server, or the connector companion runs. Publication takes a complete snapshot before uploading, persists its intent, and reuses unchanged content-addressed chunks. Each committed manifest is an immutable revision; updating a website does not delete its prior source records.

Lost acknowledgements reuse the recorded capture, parent and client key. If another publisher advances the artifact, a new capture must preserve every native source file from both the pending capture and the published revision as an exact byte prefix. Truncated or divergent history leaves the remote revision unchanged and retains the pending capture for recovery. The private publication `status.json` distinguishes pending uploads, history conflicts and remote deletion.

Deleting an artifact pauses even an unchanged publisher. Ordinary `publish`, including its manual opt-in bypass, cannot recreate it. `publish SESSION_ID --recreate` verifies that the old record is absent, retains its identity and pending evidence in a retired journal, and creates a distinct record. It cannot bypass a conflict while the old artifact still exists. Journals bind to their configured FinaleChat service; API and connector credentials never follow redirects to another endpoint.

Archiving uploads native tool arguments/results and session files as well as chat. Source bytes are preserved for recovery, so secrets present in a transcript are preserved too. Configuration snapshots exclude unknown raw keys and credential values; connector credentials and process-control files are not archived. Account tokens are resolved using the existing FinaleChat CLI/environment mechanism.

## Settings control

```sh
eagent connector pair
eagent connector run
```

Pair requests access to this specific project. The printed URL opens FinaleChat's trusted approval screen. The connector credential is stored privately in `.agents/eagent/finalechat-connector.json`. The browser can revoke it. Keep the connector command, an eagent session, or the web server running to receive commands while the model is idle; no inbound port or localhost proxy is needed.

A settings artifact is a captured view by default. Edit the current binding to stage typed changes and review them in FinaleChat's own Save controls. The iframe receives a bounded MessageChannel, never an account token or generic HTTP capability. The local connector validates the approved resource, operation, field classes, schema and current version before writing. Its shared project lock and conditional file writer also serve the local config web API. Stale settings produce a conflict instead of overwriting a newer local edit.

Command intents, acknowledgements and a settings audit survive process restarts. Redelivery reconciles an existing intent; an uncertain delivery with no local journal is reported as unknown and is not executed again. Saved settings and runtime adoption are separate: configuration, prompt and bundle changes are currently reported for new or resumed sessions, with no claim that an existing model turn adopted them.

The current implementation exposes typed project settings, prompt/bundle operations, and tests of saved primary or fallback model routes. A route test requires the approved cost class and makes one provider attempt, capped at 4096 output tokens and two minutes. It shares the local editor's probe implementation and reports usage, known pricing, and provider errors. Retrying an acknowledgement returns the original result; an interrupted test with an uncertain outcome is never repeated automatically. Capability changes invalidate the reviewed settings version and publish an updated form.

Successful settings, prompt and bundle commands can return a conditional undo review. FinaleChat's **Review undo** control stages that reversal, and its own action button submits it. The reviewed digest identifies the original local reversal; current grants, field locks, saved values, templates and route restrictions are checked again. Configuration undo restores only the original fields and preserves unrelated edits. A changed affected field or prompt/bundle rejects the reversal. An active default bundle cannot be deleted through undo. Reversals have their own durable intent, result and audit, and can themselves be undone.

Undo metadata stays in the private command journal; results expose only the affected configuration fields or a resource name/content hash. Reviews outside the protocol's size limits are omitted. Older command journals without reversal metadata remain readable but cannot offer undo. Restoring an inherited config value removes its override; an originally absent project config may remain as an empty JSON object.

The settings website embeds the same `config.js` and stylesheet as the local configuration page. Its transport stages typed proposals for model/fallback/effort choices, numeric settings, narration, mirror/archive preferences, prompt overrides, and named configurations. Route tests require a saved route and a separate trusted action. The raw JSON view contains only known overrides; it cannot replace unknown local keys or write arbitrary files. Default-configuration selection and deletion also have explicit review actions.

Picker catalogs and named configuration content are captured with the archive. A bundle whose current hash no longer matches the captured content cannot be loaded into the form; reopen the latest archive. Granted field locks and environment overrides are visible in the shared editor. New edits clear the pending host proposal, and delayed acknowledgements preserve newer form or raw-JSON drafts. Opening `settings/index.html` from an extracted archive provides the same read-only editor without a running server.

## Live session controls

Run `eagent connector pair-session SESSION_ID` in the project, then approve the separate session grant in FinaleChat. This requests access only to that session; project-default pairing does not acquire it. Keep an updated eagent session running. Its chat thread offers **Live session settings** once the connector publishes its resource. The same form is available under FinaleChat's integration settings. Pairing can precede the session's next run; a runtime resource appears only after the updated harness has started.

These controls affect this process only:

| Setting | When it takes effect |
| --- | --- |
| Task workers at once | Admission of the next queued task; reducing the limit preserves current workers. |
| Narrator check interval | Reschedules its next check immediately; a current model turn continues. |
| Narrator quiet limit | The next check and next narrator turn; zero disables quiet-limit wakes. |

The trusted Save sends an expiring, versioned command for the displayed runtime generation. The connector hands it to the harness's bounded local queue; the normal scheduler records `settings.changed` and applies it before acknowledging success. No connector writes directly to an active event log. Expired or uncertain requests are never blindly repeated, and an old generation cannot target a replacement process. Session controls have no offline drafts or send-later option. Resuming loads project defaults and starts a fresh generation.

Each run records `settings.runtime_started`. Archives retain these native events and capture `context/runtime-settings.json` when local status exists, explicitly distinguishing the captured observation from the native source prefix. Project defaults remain in their separate settings artifact. Credentials, control queues and runtime acknowledgements stay in private local state and are excluded from archives. Session pairing files live under `.agents/eagent/finalechat-state/runtime-connectors/`; the kill switch also gates these workers.

## Disable all outbound integration traffic

`EAGENT_FINALECHAT=off` is the process-wide kill switch. `0`, `false`, and `no` also disable it, case-insensitively. It overrides artifact opt-in, forced publish, pairing, and stored connector credentials, including the publisher's final shutdown sweep. Local export, verification and recovery remain available.

Setting only `finalechat.enabled: false` disables ordinary chat mirroring. It does not disable separately opted-in archives or paired settings access. `artifact disable` stops future automatic archives while leaving existing revisions and settings pairing intact. Revoke the connector in FinaleChat to stop its settings access.

## Development checkpoint

Run `EAGENT_FINALECHAT=off go test ./...` and `EAGENT_FINALECHAT=off go vet ./...`. The new integration tests use only loopback HTTP fixtures. `make build`, `make test`, `make install`, and `make release` rebuild the matching embedded reducer first. `python3 scripts/build-replay.py --check` verifies it against the current source and Go toolchain. Viewer content hashes also participate in publication, so updated renderer assets are published even for development builds.

`make test-browser` generates a synthetic archive and opens its actual settings website in Chromium, both in an opaque iframe and directly from disk. It tests typed changes, prompts/bundles, saved-route actions, grants, stale proposals and delayed acknowledgements. It uses a temporary loopback fixture with no account, provider calls, or native user settings. Set `FINALECHAT_PLAYWRIGHT_MODULE` to an installed Playwright module, for example `/path/to/tools/node_modules/playwright/index.mjs`.

Portable manifest/control definitions and the browser SDK come from the sibling FinaleChat repository. Run its `scripts/sync-eagent-protocol.py /path/to/eagent_final --check` to check synchronization; omit `--check` to update generated copies. No production service needs to be restarted to validate these features.
