# Session archives and remote settings

These commands require a FinaleChat server with the artifact and settings-control APIs. Existing conversation mirroring remains independent of archive publication and settings control.

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

The archive includes the native complete JSONL record prefix, session attachments and outputs, captured settings/provenance, the local settings audit, and a self-contained HTML/JavaScript/Go WebAssembly explorer. The portable and local viewers use the same Go state projection, session renderers and stylesheet. Its views are Chat, Usage and Timeline, with an overview, tool calls, eagent's conversation and the original files under **More**, and an actor filter. The archive adds a state-at-event slider; the state shown is rebuilt up to that event. Saved pages do not expose live session controls. Download a revision's ZIP, extract it, open `index.html`, and choose the extracted directory to read it offline. The SDK verifies accessed file chunks; `artifact verify` checks the complete archive.

The manifest records the committed event frontier and attachment completeness. `context/attachments.json` identifies included, unavailable and size-mismatched native references; an incomplete checkpoint is labeled explicitly. The settings audit is captured through its last complete record with bounded, confined reads. `context/pricing.json` freezes the exporter’s catalog rates, including unknown routes, and the replay uses those rates even with an updated renderer. These are estimates at capture time; historical invoices and unrecorded past prompt/configuration values cannot be inferred from current defaults.

FinaleChat pins an immutable revision by default. **Follow latest** accepts compatible snapshots and keeps the viewer's section, search, actor filter, event position and scroll. Changed renderer bytes require an explicit selection. A different saved viewer can also be paired with the same historical dataset; source/context bytes and the original download are preserved, and the composed website has no settings authority. Presentation state stays in the open container through the SDK's bounded `viewState` methods.

The browser loads at most 50,000 events, 32 MiB of JSONL or 128 source files and labels a limited prefix. Go WebAssembly replays that same prefix without fetching it twice; elapsed time stops at the selected event. Complete source files and the captured full summary remain available in the archive independently of the display bound.

Mirrored messages carry a dataset/session/sequence anchor. FinaleChat's **Inspect this moment** clock icon opens that event in a fixed revision, while **Find chat message** asks the trusted container for a link back to the same thread. Earlier messages with only `meta.seq` remain supported. Events outside the loaded prefix, not yet captured, or never mirrored to chat are reported as unavailable; a navigation hint cannot change settings or execute a workflow.

Publish performs one upload without changing future publication. Archive publication is on by default: `disable` saves `finalechat.artifacts: false` in the project configuration to opt out, and `enable` removes the opt-out. Sessions running in this project and previously published sessions are checked approximately every 30 seconds while eagent, its web server, or the connector companion runs. Publication takes a complete snapshot before uploading, persists its intent, and reuses unchanged content-addressed chunks. Each committed manifest is an immutable revision; updating a website does not delete its prior source records.

Lost acknowledgements reuse the recorded capture, parent and client key. If another publisher advances the artifact, a new capture must preserve every native source file from both the pending capture and the published revision as an exact byte prefix. Truncated or divergent history leaves the remote revision unchanged and retains the pending capture for recovery. The private publication `status.json` distinguishes pending uploads, history conflicts and remote deletion.

Deleting an artifact pauses even an unchanged publisher. Ordinary `publish`, including its manual forced-publish path, cannot recreate it. `publish SESSION_ID --recreate` verifies that the old record is absent, retains its identity and pending evidence in a retired journal, and creates a distinct record. It cannot bypass a conflict while the old artifact still exists. Journals bind to their configured FinaleChat service; API and connector credentials never follow redirects to another endpoint.

Archiving uploads native tool arguments/results and session files as well as chat. Source bytes are preserved for recovery, so secrets present in a transcript are preserved too. Configuration snapshots exclude unknown raw keys and credential values; connector credentials and process-control files are not archived. Account tokens are resolved using the existing FinaleChat CLI/environment mechanism.

## Settings control

```sh
eagent connector pair
eagent connector run
```

Your FinaleChat conversations gain settings automatically while eagent or its server is running. **Settings in the thread header** opens on **This session**: the running session's own settings (see *Live session controls*). **Project defaults** is the other tab: the project configuration new and resumed sessions start from (its snapshot details include `effective_routes`, what the project file puts in force). FinaleChat renders both from the published descriptors; eagent's own editor page for the project defaults is offered as an option. The thread's `model` chip is eagent's model actually serving, refreshed when a fallback or a settings change switches it. There is no separate approval screen. `connector pair` can also connect explicitly. The connector credential is stored privately in `.agents/eagent/finalechat-connector.json`. The browser can revoke it. Keep the connector command, an eagent session, or the web server running to receive commands while the model is idle; no inbound port or localhost proxy is needed.

eagent's own editor for the project defaults is published once per project as the settings resource's website (self-contained HTML and bounded settings context), rebuilt only when the settings change, independently of transcript backup publication; FinaleChat opens it only when asked. The native JSONL is not uploaded by a settings connection. Archive browsing still opens captured settings read-only by default. The iframe receives a bounded MessageChannel, never an account token or generic HTTP capability. The local connector validates the approved resource, operation, field classes, schema and current version before writing. Its shared project lock and conditional file writer also serve the local config web API. Stale settings produce a conflict instead of overwriting a newer local edit.

Command intents, acknowledgements and a settings audit survive process restarts. Redelivery reconciles an existing intent; an uncertain delivery with no local journal is reported as unknown and is not executed again. Saved settings and runtime adoption are separate: prompt and bundle changes apply to new or resumed sessions, while the session-scoped resource also carries the session's model routes (`/routes/eagent/provider|model|effort`, and `/routes/summarizer/…` when the summarizer is dedicated; class `route`) that apply live between model calls. The harness swaps that model's client and records a `route` event, and the thread's model chip refreshes. The model must be listed by the static catalog or the live model cache, and its key variable must be set. Other configuration changes are reported for new or resumed sessions, with no claim that an existing model turn adopted them.

The current implementation exposes typed project settings, prompt/bundle operations, and tests of saved primary or fallback model routes. A route test requires the approved cost class and makes one provider attempt, capped at 4096 output tokens and two minutes. It shares the local editor's probe implementation and reports usage, known pricing, and provider errors. Retrying an acknowledgement returns the original result; an interrupted test with an uncertain outcome is never repeated automatically. Capability changes invalidate the reviewed settings version and publish an updated form.

Successful settings, prompt and bundle commands can return a conditional undo review. FinaleChat's **Review undo** control stages that reversal, and its own action button submits it. The reviewed digest identifies the original local reversal; current grants, field locks, saved values, templates and route restrictions are checked again. Configuration undo restores only the original fields and preserves unrelated edits. A changed affected field or prompt/bundle rejects the reversal. An active default bundle cannot be deleted through undo. Reversals have their own durable intent, result and audit, and can themselves be undone.

Undo metadata stays in the private command journal; results expose only the affected configuration fields or a resource name/content hash. Reviews outside the protocol's size limits are omitted. Older command journals without reversal metadata remain readable but cannot offer undo. Restoring an inherited config value removes its override; an originally absent project config may remain as an empty JSON object.

The settings website embeds the same `config.js` and stylesheet as the local configuration page. Its transport stages typed proposals for model/fallback/effort choices, numeric settings, mirror/archive preferences, prompt overrides, and named configurations. Route tests require a saved route and a separate trusted action. The raw JSON view contains only known overrides; it cannot replace unknown local keys or write arbitrary files. Default-configuration selection and deletion also have explicit review actions.

Picker catalogs and named configuration content are captured with the archive. A bundle whose current hash no longer matches the captured content cannot be loaded into the form; reopen the latest archive. Granted field locks and environment overrides are visible in the shared editor. New edits clear the pending host proposal, and delayed acknowledgements preserve newer form or raw-JSON drafts. Opening `settings/index.html` from an extracted archive provides the same read-only editor without a running server.

## Starting and reviving sessions from your phone

**Reviving a finished session.** `eagent serve` and `eagent connector run` watch the account's event stream for replies to threads of sessions that are not running. A reply from the app to such a thread (text, files, or an answer to the question the session stopped on) resumes that session in the host process with the reply as its next turn; the mirror does not echo it back. A running session, wherever it runs, keeps its own poller and is left alone. On connect, and every few minutes, the watcher also sweeps threads whose latest message is a user message newer than the session's last log write, so a reply sent while no eagent was running is picked up when one starts. The watcher is elected per project, like the connector, and `EAGENT_FINALECHAT=off` disables it.

**Starting a new session.** Finalechat's **New session** is a draft that exists only in the app until the first message: its folder chip chooses where the session starts, from the directories the connector publishes in its snapshot (`details.directories`: the project root, directories recent sessions moved to, the project's subdirectories and its siblings) or any absolute path, and its Settings button edits the project's own defaults. The first message becomes a `session.start` command with `prompt` and, when it differs from the project root, `cwd`; eagent checks that the directory exists and can be entered, starts the session with it as the first working-directory move, and reports `cwd` in the result. `eagent --cwd DIR` does the same from a terminal.

**Choosing the agent and its model before it starts.** `session.start` takes `prompt` (the first message; it may be empty when `relay` names an agent), `cwd`, and `relay`: a relay profile (`antigravity`, `claude`, `codex`, `pi`, or one the project defines), left out for the project's default agent (an empty string is not one of the choices and is refused). It declares the agent's model and effort as `inputs`, which the app shows under **Options**: for each profile, `relay_<profile>_model` and `relay_<profile>_effort`, with `when: {"relay": ["<profile>"]}` so they show only for that choice. Their defaults are the profile's, else what the agent's own configuration says. Their suggestions are the list `<agent>_models` (for Codex its own model cache, for Claude Code its aliases plus the known Anthropic models), and any model name can still be typed in. A running session's settings carry the same pair per relay (`/relays/<name>/model`, `/relays/<name>/effort`, unsettable back to the default). Changing one starts the agent again on its own conversation once the turn it is on ends. eagent's own model is not a `session.start` choice: a session starts on the project's configuration, and its routes can be changed once it runs (see *Live session controls*). There, the model fields offer the models eagent knows of (the built-in catalog and the providers' live `/models` lists as last cached), grouped by the provider picked beside them; picking one picks its provider too.

**Presets in thread settings.** A running session's settings start with **Configuration** (`/config`: the preset or `bundle:NAME` its routes were last set from as a whole). Saving one sets eagent's provider, model and effort (and, when the session has a dedicated summarizer and the configuration has one too, the summarizer's; a configuration with an inline summarizer leaves the session's summarizer as it is) to what it runs in this project; route edits in the same proposal apply on top. Each choice's `implies` names those values by field, so the app shows them before Save and reads the configuration as **Custom** once a route differs from it. The project's **Preset** (`/preset`) works the same way over the project's own model settings: each preset implies its own routes (model, effort, base URL, protocol, key variable, token limits), and choosing one in the app unsets the project's overrides of them.

**Layout.** eagent declares how its settings read: fields carry a `section` (Models, Behavior, FinaleChat, and Other for anything unplaced), a `group` (in the project defaults: Configuration, eagent, Summarizer, one card per fallback route, Local servers, Agents, Voice, Progress, Work, Phone; in a running session: eagent, Summarizer, one card per agent, Progress, Configuration) and short labels. A route's plumbing (base URL, key variable, protocol, token limits, reasoning replay), every fallback route, the Work limits and the less used Phone settings are `advanced`, shown on request. FinaleChat draws the form from that alone, and every setting that can be unset offers its default as a choice.

Every process that holds the project's settings connector advertises a `session.start` action, so Finalechat's inbox offers **New session** for the project whenever an eagent is running in it. The first message is the only required parameter. The connector executes each request at most once: a redelivered command returns the original result, and a claim whose local journal is missing is reported as unknown rather than started again. A settings save that landed after the phone loaded its page does not block the start; a replaced runtime generation does.

Where the session runs depends on which process holds the connector:

| Host | What happens |
| --- | --- |
| `eagent serve`, or a session started with `--serve` | The session is hosted in that process, interactive, with the server's default preset or bundle; the web UI shows it and it keeps taking messages from the thread. |
| `eagent connector run` | The same in-process hosting, without the web UI. |
| A plain terminal session | A separate `eagent -p` process is started in its own session group with the project's configuration; it runs until the work is done (or until a question waits too long), survives the terminal session ending, and can be resumed with `eagent -c` or from the web UI. Its output goes to `.agents/eagent/finalechat-state/spawned/`. |

The result reports `session_id`, the thread reference, the host and whether the session is interactive. Existing connectors gain the capability on their next reconnect without a second pairing: eagent asks Finalechat to extend its own grants, which requires the account token and the local connector secret. `EAGENT_FINALECHAT=off` disables this along with the rest of the connector.

## Thread titles and summaries

Each session's phone thread has a `title` and a `description` (max 2000 chars; `summary` is a write alias and the thread carries both with identical values). They are what the thread list shows, so eagent keeps them current itself:

- How they are kept is the `summarizer.mode` setting (`internal/harness/summary.go`). With `inline`, the default, eagent's own model keeps them: when they may be out of date, the tail of its next call says so and shows them as they stand, and the model changes them with the `update_thread` tool in the same reply as whatever else it does, or leaves them. With nothing else going on, that is a call of its own. With `dedicated`, a separate model call writes them, on the route under `summarizer.model` (the main model's route when that is left out) with the prompt `THREAD-SUMMARY.md` (overridable like the other prompts). It reads what the user wrote, what they have been told lately, the relayed agent's latest report and what is happening now, and returns a title and a short summary of the whole session and where it stands. Its calls are counted under `summarizer`.
- Both follow one schedule: about two seconds after a session's first message and twenty seconds after a later one; twenty seconds after the agent finishes a turn (one that ran something), a question is asked or dismissed, or an agent is stopped, and at least a minute after the last update; after eagent tells the user something, at least five minutes after the last; and while one long turn goes on, at least fifteen minutes after the last. In inline mode a call made for another reason is also asked about them when there is news and the last time was over a minute ago. A batch session holds its end for one last update when the thread list has not got its news. Each update is a `thread.title` event with `by: "summary"`; a dedicated call's carries its token usage.
- The title names the session's subject and is meant to stay put. A new title replaces a missing or generic one (the project directory's name is never a title), may be revised once as the work comes into focus, and after that changes only when the user has written since it was set.
- A title someone chose is kept. `update_thread` with `pin` is for a name the user asked for, and pins it. A title or summary the user types in the FinaleChat app is pinned by the service (`title_pinned`, `description_pinned`): token updates to that field are ignored until the user clears it, and the session takes the user's words as its own (`thread.title` with `by: "finalechat"`).
- The thread is created under the project's name with no description. The opening prompt is never sent as the description.

`EAGENT_SUMMARIES=off` (also `0`, `false`, `no`) turns the schedule off: eagent is not asked about the title and summary and no dedicated call is made, so they change only when eagent calls `update_thread` itself, as it does for a name the user asks for.

`eagent summarize <session>... | --all [--dry-run] [--json] [-v]` writes a fresh title and summary for stored sessions from their logs to their threads. It reads logs and never writes them, so it is safe on a session another process is running. `--dry-run` prints without sending, `-v` also prints what the model read, and `--json` includes the old title and summary on each line.

From anywhere with a token, the same update is a PATCH:

```sh
curl -XPATCH $FINALECHAT_URL/api/v1/threads/ext:eagent:SESSION_ID \
  -H "Authorization: Bearer $FINALECHAT_TOKEN" \
  -d '{"title":"Checkout race fix","description":"Cart totals raced with coupon updates. Fix is in and covered; waiting on review."}'
```

From Go: `client.Patch(ctx, finalechat.Ref("eagent:"+id), finalechat.PatchRequest{Title: "...", Description: "...", Summary: "..."})`, or `client.UpdateThreadDescription(ctx, ref, title, description)`, which sends both names and returns the thread as the service now shows it.

## Client capability handshake (v1)

A reply from the app can declare what context its client can supply — timezone, locale, device — so eagent sees it. eagent reads the reply message's `meta` without changing behavior when the keys are absent, preferring the map form:

```json
{"eagent.client": {"timezone": "America/Los_Angeles", "locale": "en-US", "device": "phone", "app": "finalechat/1.2", "supplies": ["tz", "locale"]}}
```

The flat keys `eagent.tz`, `eagent.locale`, and `eagent.device` are accepted as a fallback. The capsule is stored on the `user.message` / `user.answer` event (source `finalechat`) and renders in eagent's conversation as a trailing `[client line]`, e.g. `[finalechat · tz America/Los_Angeles · en-US · phone]`.

## Live session controls

Every running session — including one started from the phone's **New session** — gets a session-scoped settings resource automatically. The project's one elected connector process finds sessions with a live runtime heartbeat (it looks at the newest 64 sessions, and at most 40 running ones get a resource; one past that still runs but is not listed as running), keeps one grant per running session on its own connector (the account token plus the local secret prove the owner's installation; grants of stopped sessions are dropped, so they never pile up), publishes each session's resource under its single heartbeat lease, and routes each claimed command to the project or to that session's harness. A session that stops is published once as unavailable and then leaves the thread.

These controls affect this process only:

| Setting | When it takes effect |
| --- | --- |
| Seconds between progress looks (`/tick_seconds`, 5 to 86400) | Reschedules the next look immediately; a model call in flight continues. A look with nothing new costs no model call. |
| Longest silence while the agent works (`/quiet_seconds`, 0 to 86400) | The next progress look; zero turns the limit off. |
| eagent's provider, model and reasoning effort (`/routes/eagent/…`), and the dedicated summarizer's (`/routes/summarizer/…`) when the session has one | Between model calls; a call in flight finishes on the route it started with. The model must be in the catalog or the cached live list and its key set where the session runs, or the change is rejected. Another model drops the old model's fallback chain; an effort-only change keeps it. A model whose pick did not change keeps its route, including a fallback it switched to. |
| Configuration (`/config`) | Immediately: sets those routes to what the chosen preset or `bundle:NAME` runs in this project. |
| Save as new preset (`/save_preset`) | Saves this session's models and settings as a preset in the project. |
| Each agent's model and reasoning effort (`/relays/<name>/model`, `/relays/<name>/effort`) | When the agent's current turn ends, or at once when it is not working: it starts again on its own conversation. Unsetting returns to the profile's or the agent's own default. |
| Stop session (action `session.stop`) | Records `session.stop_requested`, stops the agents and ends the session. A reply in the thread resumes it while `eagent serve` or `eagent connector run` is up. |

The trusted Save sends an expiring, versioned command for the displayed runtime generation. The connector hands it to the harness's bounded local queue; the normal scheduler records `settings.changed` and applies it before acknowledging success. No connector writes directly to an active event log. Expired or uncertain requests are never blindly repeated, and an old generation cannot target a replacement process. Session controls have no offline drafts or send-later option. Resuming loads project defaults and starts a fresh generation.

Each run records `settings.runtime_started`. Archives retain these native events and capture `context/runtime-settings.json` when local status exists, explicitly distinguishing the captured observation from the native source prefix. Project defaults remain in their separate settings artifact. Credentials, control queues and runtime acknowledgements stay in private local state and are excluded from archives. The kill switch also gates session resources.

## When the network or a provider fails

A long-running `eagent serve` should never need a restart to get unstuck:

- **No network.** Model calls that cannot reach their provider (DNS, no route, refused or dropped connections) wait and retry until it is back; the turn is not failed. A connection that went silent is dropped after two minutes, and idle HTTP/2 connections are pinged so a dead one is noticed in under a minute.
- **Provider errors.** When a model call still fails after its own retries, eagent tries again at once, then after 1, 5, 15 and 30 minutes, then every hour for as long as it takes; a message from you makes it try at once. Your messages still reach the agent meanwhile, and at the second failure in a row eagent tells you it cannot reach its model. A prompt the provider refuses as too long first restarts the conversation from a dossier. A call that runs out its whole 15-minute limit is tried once more, not five times.
- **FinaleChat unreachable.** The phone mirror keeps trying its first contact, posts and questions (each idempotent) until FinaleChat answers; only a rejected token turns it off. A reply whose session cannot be resumed is retried every minute for a day, and the thread is told why once. A proxy's 404 during a deploy no longer marks an archive deleted.
- **Relays.** A restarted or upgraded Codex relay finds its transcript again (by id, whatever day it began); tmux, screen, git and agent CLI calls are bounded; one missed liveness check no longer marks a relay exited.
- **Start folder.** A session started from the app in a folder that does not exist or cannot be entered is refused at start, with the reason.

## Disable all outbound integration traffic

`EAGENT_FINALECHAT=off` is the process-wide kill switch. `0`, `false`, and `no` also disable it, case-insensitively. It overrides artifact publication, forced publish, pairing, and stored connector credentials, including the publisher's final shutdown sweep. Local export, verification and recovery remain available.

Setting only `finalechat.enabled: false` disables ordinary chat mirroring. It does not disable archive publication (on by default) or paired settings access. `artifact disable` stops future automatic archives while leaving existing revisions and settings pairing intact. Revoke the connector in FinaleChat to stop its settings access.

## Development checkpoint

Local export preserves native session files even when project defaults are malformed or unavailable. Such archives omit the settings editor and include `context/settings-unavailable.json`; they do not present partially resolved defaults as known values. Configuration, bundle, prompt and instruction reads are limited to regular files of at most 1 MiB. Exporting is independent of credentials. Network publication still requires a valid configuration so the publisher can resolve its publication preference, token reference and destination without guessing; repair that configuration before retrying publication.

Run `EAGENT_FINALECHAT=off go test ./...` and `EAGENT_FINALECHAT=off go vet ./...`. The new integration tests use only loopback HTTP fixtures. `make build`, `make test`, `make install`, and `make release` rebuild the matching embedded reducer first. `python3 scripts/build-replay.py --check` verifies it against the current source and Go toolchain. Viewer content hashes also participate in publication, so updated renderer assets are published even for development builds.

`make test-browser` generates a synthetic archive and opens its actual settings website in Chromium, both in an opaque iframe and directly from disk. It tests typed changes, prompts/bundles, saved-route actions, grants, stale proposals and delayed acknowledgements. It uses a temporary loopback fixture with no account, provider calls, or native user settings. Set `FINALECHAT_PLAYWRIGHT_MODULE` to an installed Playwright module, for example `/path/to/tools/node_modules/playwright/index.mjs`.

Portable manifest/control definitions and the browser SDK come from the sibling FinaleChat repository. Run its `scripts/sync-eagent-protocol.py /path/to/eagent --check` to check synchronization; omit `--check` to update generated copies. No production service needs to be restarted to validate these features.

Connector workers retain their random process identity across transient network failures. On graceful shutdown they release only their own heartbeat lease, allowing an immediate restart without removing settings grants. After an unclean exit, another worker waits for the existing 90-second lease to expire; it never steals an active connection.
