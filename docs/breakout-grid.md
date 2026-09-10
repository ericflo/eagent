# The Breakout grid v3: sixteen presets, one brief — plus heroes

On 2026-09-08/09 fourteen eagent presets each built the same game from the same one-paragraph spec, in parallel, unattended, on binary v0.8.0-1-g59ce5c7 (with all four cache/fallback fixes from the first grid). A measuring run then compiled all fourteen: it parsed their event logs, served each build, played it with scripted Chromium, took screenshots, and scored it against the same nine-item rubric as v1. Then phase 3, on 2026-09-09: four interrupted runs were resumed (the two budget-SIGTERM'd Anthropic runs and the two cap-killed runs), and two new **hero** runs were launched on the hero-tier models at xhigh effort. All six were re-shot, re-played and re-judged; the other ten scores are carried over untouched. This page is the record: what ran, what it cost, what each build shipped, and what the exercise changed in the harness. It replaces the fourteen-preset v2 grid; the six-preset grid of 2026-09-07 is archived as [`docs/grid/results-2026-09-07.json`](grid/results-2026-09-07.json), and the v2 dataset survives in git history.

## Heroes

Phase 3 launched two hero runs — xhigh effort on the hero-tier models, with the GPT-5.6 Luna narrator on med. They took the top of the table as read: a second perfect 27, and a 26 with the deepest site-shell polish in the set.

### hero-fable — OVERTOP (27/27, $78.11, stopped-capped)

- **Config:** orchestrator Claude Fable 5.1 (xhigh) / task worker Claude Fable 5.1 (xhigh) / narrator GPT-5.6 Luna (med). Root `index.html` entry.
- **Cost:** $78.11 against a $75 cap — overshot by $3.11 between ticks, then cap-killed; 127 minutes, 4/5 tasks, 50 narrator messages.
- **Game:** OVERTOP again (no relation): punch a hole in the bricks and get the ball into the attic above them, where it bounces on its own and the multiplier climbs. 8 capsule powers (fire/heavy/split-multiball/magnet/wide/slow/laser/life), prism/armor/lid/phase bricks, and an adaptive **7-tier** soundtrack that crossfades with play intensity.
- **Verification:** `npm test` — 82 tests, 0 fail (incl. a 20s sim run, life/multiplier/game-over flow, split/life/wide powerups). PLAY-OK desktop + mobile, 0 console/page errors.
- **Billing note:** the OpenAI-direct key was dry (502 no-credits), so the Luna narrator served — and billed — via the OpenRouter fallback by preset design (see routes in `results.json`).

### hero-astra — ASTRA, Breakout. Evolved. (26/27, $30.39, done)

- **Config:** orchestrator GPT-6 Astra (xhigh) / task worker GPT-6 Astra (xhigh) / narrator GPT-5.6 Luna (med). Judged and played from `dist/index.html` (Vite build).
- **Cost:** $30.39 in 33 minutes, 5/5 tasks, 31 narrator messages, zero errors in the log.
- **Game:** a two-axis paddle (move + lift to boost) across 5 sectors with tactical briefings; breach the array to enter overdrive, where plasma piercing becomes multiball and then electric chain reactions. Full site shell — header, HUD, mission rail, field guide, footer — played to a real game over on desktop (score 00200).
- **Verification:** `npm test` — 29 tests, 0 fail (incl. a fixed-step autopilot that wins all five arrays). PLAY-OK both viewports, 0 errors. Audio docked to 2: SFX-only synth, no music engine.
- **Billing note:** same story as its sibling — OpenAI-direct dry, all three actors served via the OpenRouter fallback and priced at OpenRouter catalog rates.

## Redemption

### anthropic-high — OVERTOP (26→27: the nebula fix)

V2's honest disappointment is v3's redemption arc. The resume fixed the black-canvas bug: `src/render.js` now binds `const nebula = bgLayer(bucket)` (:241) inside the pre-rendered backdrop path instead of the bare `nebula = buildNebula()` call that threw a ReferenceError on the first frame. Shots went from 2–4 KB pure black to 231–343 KB live frames; canvas renders on both viewports with zero console/page errors. Game code grew 6,010→6,975 LOC (+965), and the node-only harnesses now pass (`tools/physics-test.mjs` PHYSICS TEST PASSED; `tools/level-sim.mjs` LEVEL AUDIT PASSED with 1 minor warning). Scoring 26→27, polish 2→3 — the title/HUD/BALL LOST flow is now play-verified desktop + mobile. Cost $40.95 against a $40 cap (overshoot +0.95 between ticks, then stopped-capped). The full per-version spend split is in `results.json` (`spend_split`: v1 $24.31 + resume $16.64).

The other three resumes, briefly: `anthropic-med` 26→26 (+2,417 LOC to 10,223, all four per-file runners pass, polish stays 2 — still spare next to peers; $37.03 vs $36 cap). `astra` 26→26 (TypeScript grown to 5,633 LOC, `dist/` rebuilt, all 124 vitest tests pass including an input-only autoplayer that wins all ten sectors; finished $18.34 vs an $18 cap — done between ticks). `opencode-med` 27→27 (3,054→3,373 LOC, no structural change, still the only perfect score and still without a test suite; resumed to a clean `done` at $1.81 under its $5 cap).

## What was run

The spec is [`docs/grid/SPEC.md`](grid/SPEC.md). In one paragraph: a modern take on Breakout built around the moment the ball gets above the bricks and keeps going on its own, with things escalating once it is up there. Game juice that scales with score and multiplier. Power-up balls, and bricks that are difficult in ways other than hit counts, such as a required angle or a required speed. A paddle that also moves up and down, where moving up on contact accelerates the ball. Mouse play, with mobile thumb or thumbstick control as a first-class citizen. Polish and audio.

Every build got the same prompt:

> Read INSTRUCTIONS.md and relentlessly build it until you're confident that it's fully and completely implemented to the highest possible standard you can manage.

The sixteen presets, with the model each actor ran (orchestrator / task worker / narrator):

| Preset | Orchestrator | Task worker | Narrator |
|---|---|---|---|
| `glm` | GLM-5.3 | GLM-5.3-Flash | DeepSeek V4 Flash |
| `openrouter-high` | Kimi K3 | GLM-5.3 | GLM-5.3-Flash |
| `openrouter-med` | GLM-5.3 | GLM-5.3-Flash | DeepSeek V4 Flash |
| `anthropic-high` | Claude Fable 5.1 | Claude Opus 5 | Claude Sonnet 5 |
| `anthropic-med` | Claude Opus 5 | Claude Sonnet 5 | Claude Haiku 4.5 |
| `openai-high` | GPT-6 Astra (high effort) | GPT-5.6 Sol | GPT-5.6 Luna |
| `openai-med` | GPT-6 Astra | GPT-5.6 Terra | GPT-5.6 Luna |
| `openai-low` | GPT-5.6 Sol | GPT-5.6 Luna | GPT-5.6 Luna |
| `astra` | GPT-6 Astra | GLM-5.3-Flash | DeepSeek V4 Flash |
| `deepseek` | DeepSeek V4 Flash | DeepSeek V4 Flash | DeepSeek V4 Flash |
| `qwen` | Qwen 3.8 27B | Qwen 3.8 27B | Qwen 3.8 27B |
| `muse` | Muse Spark 1.3 | Muse Spark 1.3 | Muse Spark 1.3 |
| `opencode-med` | GLM-5.3 | GLM-5.3-Flash | DeepSeek V4 Flash |
| `nous-med` | GLM-5.3 | GLM-5.3-Flash | DeepSeek V4 Flash |
| `hero-astra` | GPT-6 Astra (xhigh) | GPT-6 Astra (xhigh) | GPT-5.6 Luna (med) |
| `hero-fable` | Claude Fable 5.1 (xhigh) | Claude Fable 5.1 (xhigh) | GPT-5.6 Luna (med) |

Each run started in its own directory holding nothing but `INSTRUCTIONS.md`, with the phone mirror off (`EAGENT_FINALECHAT=off`). Phase 1 endings: nine runs declared done, two budget-SIGTERM'd after passing verification (`anthropic-high` $24.31, `anthropic-med` $23.17), one provider-halted (`nous-med` $0.13 — Nous 404s, awaiting-input), two cap-killed mid-verification (`opencode-med` $1.11, `astra` $7.40 on the global $90 brake). Phase 3 (2026-09-09): all four interrupted runs resumed — `anthropic-high` and `anthropic-med` ran to their new caps ($40/$36), `astra` finished between ticks ($18.34 vs $18), `opencode-med` finished clean ($1.81 vs $5) — and the two heroes ran fresh (`hero-astra` done $30.39, `hero-fable` capped $78.11 vs $75). `nous-med` was left untouched and is byte-identical to v2. No run rolled its context over, and no narrator asked a question.

The measuring run parsed each run's JSONL event logs for duration, tokens, cache hits, tasks, narrator messages and errors; computed cost at September 2026 list prices; served each build statically (Vite builds from `dist/`) and played it with scripted Chromium (Playwright) at a 1280×800 desktop viewport and a 390×844 touch phone viewport with a multi-step start; captured console and page errors; and scored the nine spec items from code reading plus that play. Phase 3 re-shot, re-played and re-judged exactly the six touched builds; the other ten results are carried over from v2. Run spend totalled $242.19 ($91.54 phase 1 + $150.65 resumes and heroes). Raw per-build reports live with the run directories outside this repository; [`docs/grid/results.json`](grid/results.json) is the compiled data.

## Results

All numbers are from `results.json`. Status is `done` (declared done itself), `stopped-budget` (SIGTERM after passing verification), `stopped-capped` (killed by a cost cap), or `halted-provider` (provider errors ended it).

| Preset | Status | Duration | Cost | Tokens in / out | Cache | Tasks done | Narr msgs | Errors | Files / LOC | Tests | Play errors |
|---|---|---|---|---|---|---|---|---|---|---|---|
| `hero-fable` | stopped-capped | 127 min | $78.11 | 38.9M / 525k | 96% | 4/5 | 50 | 2 | 28 / 4,819 | yes (node --test, 82 pass) | 0 / 0 |
| `hero-astra` | done | 33 min | $30.39 | 16.2M / 180k | 95% | 5/5 | 31 | 0 | 178 / 1,829* | yes (node --test, 29 pass) | 0 / 0 |
| `opencode-med` | done | 237 min | $1.81 | 21.3M / 259k | 95% | 8/11 | 46 | 13 | 15 / 3,373 | none | 0 / 0 |
| `anthropic-high` | stopped-capped | 277 min | $40.95 | 36.7M / 511k | 95% | 2/6 | 39 | 9 | 66 / 6,975 | per-file node runners pass | 0 / 0 |
| `astra` | done | 248 min | $18.34 | 56.5M / 1020k | 88% | 5/11 | 50 | 50 | 329 / 5,633* | yes (vitest, 124 pass) | 0 / 0 |
| `openai-med` | done | 27 min | $12.12 | 17.4M / 209k | 95% | 14/14 | 41 | 3 | 30 / 2,229* | yes (node --test) | 0 / 0 |
| `openai-high` | done | 42 min | $13.69 | 24.5M / 182k | 97% | 5/8 | 50 | 26 | 77 / 3,044* | yes (tsx --test) | 0 / 0 |
| `anthropic-med` | stopped-capped | 267 min | $37.03 | 93.3M / 860k | 96% | 9/13 | 53 | 12 | 22 / 10,223 | per-file node runners pass | 0 / 0 |
| `muse` | done | 11 min | $0.04 | 2.4M / 73k | 92% | 2/2 | 7 | 2 | 10 / 1,907 | none | 0 / 0 |
| `glm` | done | 88 min | $1.22 | 9.3M / 258k | 79% | 2/3 | 36 | 11 | 3 / 2,035 | none | 0 / 0 |
| `openrouter-high` | done | 23 min | $1.44 | 2.5M / 124k | 88% | 2/2 | 16 | 2 | 3 / 1,577 | none | 0 / 0 |
| `deepseek` | done | 101 min | $0.53 | 8.4M / 404k | 82% | 1/3 | 54 | 13 | 23 / 1,581 | yes (qa/smoke) | 0 / 0 |
| `openai-low` | done | 37 min | $1.13 | 18.2M / 153k | 97% | 5/5 | 27 | 4 | 12 / 616 | yes (physics.test.js) | 0 / 0 |
| `openrouter-med` | done | 23 min | $0.66 | 3.4M / 65k | 73% | 2/2 | 14 | 4 | 17 / 1,459 | none | 0 / 0 |
| `qwen` | done | 113 min | $4.61 | 9.6M / 192k | 57% | 1/3 | 46 | 7 | 15 / 3,556 | none | 0 / 0 |
| `nous-med` | halted-provider | 34 min | $0.13 | 2.7M / 37k | 90% | 1/1 | 10 | 2 | 16 / 1,375 | none | 0 / 0 |

\* `astra`, `openai-high`, `openai-med` and `hero-astra` are Vite builds: the judged entry is `dist/index.html`, so file counts include `dist/` output and LOC counts understate hand-written source.

Model calls, as orchestrator / task worker / narrator: `hero-fable` 373 (24/297/52), `hero-astra` 290 (51/204/35), `opencode-med` 506 (89/343/74), `anthropic-high` 417 (36/319/62), `astra` 841 (116/657/68), `openai-med` 448 (78/317/53), `openai-high` 487 (65/371/51), `anthropic-med` 954 (59/825/70), `muse` 77 (20/45/12), `glm` 286 (55/178/53), `openrouter-high` 110 (33/56/21), `deepseek` 231 (92/77/62), `openai-low` 301 (31/240/30), `openrouter-med` 133 (27/83/23), `qwen` 226 (78/76/72), `nous-med` 112 (15/70/27).

Leaderboard (rubric, then tests, then cost — see Verdict for the rule):

| # | Preset | Rubric | Tests | Cost |
|---|---|---|---|---|
| 1 | `hero-fable` | 27 | node --test (82 pass) | $78.11 |
| 2 | `opencode-med` | 27 | none | $1.81 |
| 3 | `anthropic-high` | 27 | per-file runners pass | $40.95 |
| 4 | `astra` | 26 | vitest (124 pass) | $18.34 |
| 5 | `openai-med` | 26 | node --test | $12.12 |
| 6 | `openai-high` | 26 | tsx --test | $13.69 |
| 7 | `anthropic-med` | 26 | per-file runners pass | $37.03 |
| 8 | `hero-astra` | 26 | node --test (29 pass) | $30.39 |
| 9 | `muse` | 26 | none | $0.04 |
| 10 | `glm` | 25 | none | $1.22 |
| 11 | `openrouter-high` | 25 | none | $1.44 |
| 12 | `deepseek` | 24 | qa / smoke | $0.53 |
| 13 | `openai-low` | 24 | physics.test.js | $1.13 |
| 14 | `openrouter-med` | 24 | none | $0.66 |
| 15 | `qwen` | 24 | none | $4.61 |
| 16 | `nous-med` | 22 | none | $0.13 |

Notes on the table:

- Overall prompt-cache hit rate was 92.5% across all sixteen runs. The cache fix from v1 keeps verifying: `openai-high` went from 6% cached ($31.59 on 6.9M input tokens) to 96.6% cached ($13.69 on 24.5M input tokens) — roughly 3× the tokens for less than half the money.
- All sixteen builds played clean: zero console errors and zero page errors everywhere. The v2 `nebula is not defined` black canvases are gone — the single most visible redemption in the grid.
- The OpenAI-direct key ran out of credits mid-grid (502s onward) and stayed dry through phase 3; affected runs survived on their OpenRouter fallbacks by preset design, and hero costs are OpenRouter-billed (see routes in `results.json`). The `astra` log also shows Together 402 credit-limit failures on late tasks.
- Caps overshot between ticks, every time: `openai-med` finished at $12.12 against an $8.40 cap (+3.72), `deepseek` at $0.53 vs $0.50 (+0.03), `qwen` at $4.59 vs a raised $3.50 cap (+1.09), `astra` at $18.34 vs $18.00 (+0.34, done between ticks), `anthropic-med` at $37.03 vs $36.00 (+1.03), `anthropic-high` at $40.95 vs $40.00 (+0.95), `hero-fable` at $78.11 vs $75.00 (+3.11). See BUDGET CAVEATS for the full accounting.
- `qwen` was cap-killed once at $0.88, resumed with a raised cap, then briefly ran 4 read-only calls against the wrong preset (no files written — killed before it could do anything), and resumed correctly on `qwen` to finish at $4.61. See RESUMABILITY.
- External dependencies are rare: only `deepseek` and `openai-med` load Google Fonts; everything else is dependency-free at runtime.

## Rubric

Each spec item scored 0 to 3: 3 means present, working and genuinely well done; 2 present and working; 1 token effort; 0 absent. Sixteen columns will not fit a readable table, so presets are rows.

| Preset | Breakthrough | Juice | Power-balls | Special bricks | 2-D paddle | Mouse | Mobile | Polish | Audio | **Total** |
|---|---|---|---|---|---|---|---|---|---|---|
| `hero-fable` | 3 | 3 | 3 | 3 | 3 | 3 | 3 | 3 | 3 | **27** |
| `opencode-med` | 3 | 3 | 3 | 3 | 3 | 3 | 3 | 3 | 3 | **27** |
| `anthropic-high` | 3 | 3 | 3 | 3 | 3 | 3 | 3 | 3 | 3 | **27** |
| `astra` | 3 | 2 | 3 | 3 | 3 | 3 | 3 | 3 | 3 | **26** |
| `openai-med` | 3 | 2 | 3 | 3 | 3 | 3 | 3 | 3 | 3 | **26** |
| `openai-high` | 3 | 3 | 3 | 3 | 3 | 3 | 3 | 3 | 2 | **26** |
| `anthropic-med` | 3 | 3 | 3 | 3 | 3 | 3 | 3 | 2 | 3 | **26** |
| `hero-astra` | 3 | 3 | 3 | 3 | 3 | 3 | 3 | 3 | 2 | **26** |
| `muse` | 3 | 2 | 3 | 3 | 3 | 3 | 3 | 3 | 3 | **26** |
| `glm` | 3 | 3 | 2 | 3 | 3 | 3 | 3 | 2 | 3 | **25** |
| `openrouter-high` | 3 | 3 | 3 | 3 | 3 | 3 | 2 | 3 | 2 | **25** |
| `deepseek` | 3 | 2 | 2 | 3 | 3 | 3 | 3 | 3 | 2 | **24** |
| `openai-low` | 3 | 2 | 3 | 3 | 3 | 3 | 3 | 2 | 2 | **24** |
| `openrouter-med` | 2 | 2 | 3 | 3 | 3 | 3 | 2 | 3 | 3 | **24** |
| `qwen` | 3 | 3 | 2 | 3 | 3 | 3 | 1 | 3 | 3 | **24** |
| `nous-med` | 3 | 2 | 2 | 3 | 3 | 2 | 2 | 2 | 3 | **22** |

The evidence behind each score, per build, quoted from the measuring run's reports. Heroes first, then the redemption, then the rest in rank order.

### hero-fable — OVERTOP (27/27)

- **Breakthrough:** on-top means ball centre above the highest breakable brick but below the top wall — the attic (`src/game.js:129-130`); overtopEnter/Exit events (`:719-722`), from-above breaks score ×2 (`:27`), 8s on top grows a heavy ball (`:705-708`).
- **Juice:** particles, shards, rings, popups, shake, flashes, banners, ball trails (`src/fx.js:1-2`), pooled (`:18-27`), honours `prefers-reduced-motion`; desktop shot live at score 100.
- **Power-up balls:** fire/heavy/split-multiball/magnet/wide/slow/laser/life with a weighted drop table (`src/balls.js:17-25`), capsule drops (`src/game.js:582`), wide/magnet/laser timers; 8 genuine ball powers.
- **Special bricks:** steel unbreakable, phase bricks blinking 2s solid / 2s intangible (`:56-57`), ghost solidity rules (`:108-115`); title teaches prisms-steep, armor-speed, lids-from-above, phase-blink.
- **2-D paddle:** vertical band (`src/paddle.js:10`), per-frame vy tracking (`:61-71`); rising paddle ADDS speed / power hit, descending softens (`src/physics.js:142-156`).
- **Mouse:** mouse first in the input header, default `mouse` mode, absolute positioning; title documents mouse move, click = launch/fire.
- **Mobile:** thumbstick (r64, dead-zone, tap-to-fire) plus drag schemes; mobile PLAY-OK stacked portrait, CLICK TO LAUNCH, score 250.
- **Polish:** title panel (goal/paddle/tricky-bricks howto, controls, high score), pause, persisted highScore + settings, full level flow (title/playing/paused/levelclear/gameover), phase-brick coach marks; 0 errors.
- **Audio:** lazy AudioContext with 7 background layer levels crossfading across tiers 0–6 plus a per-frame beat scheduler (`src/audio.js:9-17, :40`). The full music engine the sibling hero lacks — and the point that separates them.

### hero-astra — ASTRA (26/27, audio docked: SFX only)

- **Breakthrough:** `_enterOverdrive` on breach above the array — overdrive online, plasma pierce, speed floor 780 (`src/game.js:535-547`); 5 hits → multiball, 11 hits → chain arcs (`:548-557`); mission rail says "Break the ceiling".
- **Juice:** pooled particles/rings/floaters/bolts/drops (`:260`), shake with reduced-motion clamp, launch bursts, ball trails, BREAKTHROUGH/CHAIN REACTION banners.
- **Power-up balls:** kill-milestone drops — wide/plasma/split/boost + rare life (`:519-521`), timed collects (`:570-593`), overdrive staging plasma → multiball → arcs.
- **Special bricks:** armor needs speed (740), prism needs a 20°-off-vertical matching slash (`:4, :67-74`); 5 arrays with tactical briefings (Prism passage, Terminal velocity).
- **2-D paddle:** targetY clamped to the field band, vy tracked per frame (`:342, :366-370`); rising paddle adds energy, no horizontal traps; help reads "Move up to boost".
- **Mouse / mobile:** pointer default with canvas capture; Thumb mode default on coarse pointers with a Pointer/Thumb toggle; mobile PLAY-OK stacked with Thumb selected.
- **Polish:** full site shell (header/HUD/mission rail/field guide/footer), help dialog, pause/restart, best+mute+mode persistence, a real game-over flow, reduced-motion toggle; 0 errors.
- **Audio (2):** lazy WebAudio synth with oscillator+gain voices and power/overdrive/clear chords (`src/game.js:126-149`) — SFX only, no music sequencer; the docked point.

### opencode-med — BREAKTOP (27/27, resumed to done)

- **Breakthrough:** `js/game.js:863-884` overdrive entry on ball-above-rim while rising (banner + flash + shake + music lift), `:885-900` rim exit pays out; charge past 55% drops a reward capsule.
- **Juice:** pooled particles + score popups (`js/particles.js:2`), slow-mo timeScale (`:344-358`), lift-scaled shake, overdrive heat vignette; desktop live at score 112.
- **Power-up balls:** `js/powerups.js:11-18` FIRE/GIANT/SLOW/MAGNET/STICKY/WIDE/BURST with a weighted drop table; a falling WIDE capsule photographed mid-game.
- **Special bricks:** `js/bricks.js:54-78` steep-only / speed-gate / speed+steep gates with STEEP ONLY / NEED SPEED fail reasons, plus armored, chain-explosive and spinner codes on the title screen.
- **2-D paddle:** vertical band clamp, vy lerp, squash-and-stretch (`:463-472`); rising lift adds up to +150 ball speed (`:675-680`).
- **Mouse:** pointer maps to paddle target in the lower zone (`js/input.js:51-57`); richest title screen in the grid (run status, controls, 6 brick codes, overdrive rules).
- **Mobile:** drag + sliding-stick touch origin (`:12, :68-73`); mobile shot is a clean stacked layout with DRAG badge and tap-to-launch. PLAY-OK both viewports, 0 errors.
- **Polish:** side panels, charge/lives/combo/multiplier HUD, pause/sound, localStorage best; title + desktop + mobile all live-rendered.
- **Audio:** procedural 8th-note music stepper with layers 0–3 plus overdrive heat (`js/audio.js:16-17, :181-214`); combo and lift drive the layers. Still the only 27 without a test suite (README cites headless-Chromium Playwright verification only) — and still perfect.

### anthropic-high — OVERTOP (26→27, the nebula fix)

- **Breakthrough:** overtop hysteresis — climb above brick tops in one continuous run (`src/game.js:17-19`), multiplier `1 + overtopTime/2 + streak/3`.
- **Juice:** particles, shards, rings, shake/zoom, flashes, floating text; sky tint warms with intensity (`src/render.js:224+`).
- **Power-up balls:** multiball/fireball/ghostball/magnet/wide/slowmo/laser + heavy with timed ball mods.
- **Special bricks:** steep-only and horizontal angle gates plus a speed gate (`src/entities/bricks.js:25, :40, :56`), with slow/ghost/topOnly condition kinds; `tools/level-sim.mjs` audits all 30 sectors.
- **2-D paddle:** lowest-22% vertical band; smashVel remembers upward speed so late smashes count (`src/entities/paddle.js:87`).
- **Mouse / mobile:** pointer capture with mouse target mode; thumbstick-or-drag touch routing with `viewport-fit=cover`. Mobile PLAY-OK portrait (score 300, ball on paddle).
- **Polish (2→3):** full title (OVERTOP logo, TAP-CLICK-SPACE, how-to, paddle-moves-up coach, high score, 12 hand-built levels), HUD, BALL LOST flow, settings/mute/pause — now play-verified on both viewports. The resume fixed `src/render.js:241` (`const nebula = bgLayer(bucket)`), so the canvas renders and the point is earned, not credited.
- **Audio:** adaptive procedural music + synthesized SFX with a lookahead scheduler and intensity-gated layers (`src/audio.js:1-17`, `src/audio/music.js`).

### astra — OVERDRIVE (26/27, resumed to done)

- **Breakthrough:** genuine above-the-roof breach detection (`src/engine/Game.ts:576-590`), breakthrough heat that decays slowly, tiered overdrive meter until "the arena burns".
- **Juice (2):** particles + floating text, screen flash, ball trails, camera shake — present and working, modest next to peers.
- **Power-up balls:** plasma/split/chain/wide/slow/life (`src/engine/types.ts:5`); split is multiball, chain is lightning.
- **Special bricks:** standard/angular/speed/steel with rejection-flash feedback and a gate legend (`levels.ts:2`); field guide on screen.
- **2-D paddle:** clamped vertical zone with rising-feed / dipping-soften (`Game.ts:330-348`, lift boost in `physics.ts:109-114`).
- **Mouse / mobile:** single pointer surface for mouse + touch; left-half thumbstick (r62), right-half drag; CADET/PILOT/ACE picker now advertising 10 SECTORS / 6 POWER-UPS / +8 OVERDRIVE. PLAY-OK both — mobile caught a live ball in flight with break particles and "70 BRICKS TO BREAKTHROUGH".
- **Polish:** title modal with 3 difficulties, how-to, field guide, toggles, live demo behind modal; prefs + best persisted; sector intro cards. Vite build (`dist/index.html`).
- **Audio:** separate sfx + music gains, minor-pentatonic sequencer driven by overdrive energy (`src/ui/audio.ts:129-140`). The vitest suite passes 124/124 across 6 files — including an input-only autoplayer that wins all ten sectors.

### openai-med — OVERDRIVE (26/27, $12.12 vs $8.40 cap)

- **Breakthrough:** per-ball overdrive breach check (`src/game.js:574`), upperBreach event with multiplier + ball count (`:645-661`), OVERDRIVE CASCADE banner.
- **Juice (2):** particle/trail pools, overdrive trails, impact sparks and shake — tasteful neon rather than maximal.
- **Power-up balls:** fire/split/magnet/wide drops with timed clears and a current-power HUD slot.
- **Special bricks:** steep angle gate (<0.82 verticality) and 465+ speed gate (`:551-553`), documented in a field-intelligence codex.
- **2-D paddle:** vertical lane clamp with vy tracking; rising boost `clamp(-vy*0.22, …, 135)` added to ball speed.
- **Mouse / mobile:** direct mouse steer; thumbstick element with pointer capture plus LAUNCH button; desktop shot shows the aim phase, mobile a live ball. Vite build, `npm test` (game, audio, responsive, terminal).
- **Polish:** mission brief, flight manual, best/multiplier/lives/charge HUD, reduced-motion; cohesive art direction on both viewports.
- **Audio:** adaptive music + sfx with intensity state driving register, fullness and timbre. Overshot its $8.40 cap between ticks and finished at $12.12 with 14/14 tasks complete.

### openai-high — OVERDRIVE, Evolved (26/27)

- **Breakthrough:** overdrive charges above the field and decays below (`src/game/engine.ts:446-450`); MAXIMUM OVERDRIVE event takes the multiplier to 12 and opens charged speed-gate access.
- **Juice:** particles, shake/flash, topside trails, impact shake with toasts; mobile shot shows a MULTIBALL banner with pickup toasts — the strongest live-play evidence in the grid.
- **Power-up balls:** fire/split/wide/magnet (8–12s durations); energy bricks cycle Fireball/Multiball/Wide/Magnetic.
- **Special bricks:** angle cone (side-hit or >0.72 verticality) and 555+ speed gate with NEED CHARGE / CHANGE ANGLE toasts.
- **2-D paddle:** 2D follow with vy tracking; upward velocity boosts launches.
- **Mouse / mobile:** pointer-to-target mapping; VECTOR CONTROL joystick with PUSH-UP-BOOST hint on mobile. Desktop: 6/64 cleared, score 001116. Vite build, `npm test` (engine, physics, audio).
- **Polish:** sector HUD, intro/restart, field-notes codex, reduced-motion, personal best.
- **Audio (2):** 8 named SFX voices with intensity-scaled pitch — SFX only, no music engine; the one non-3. $13.69 (was $31.59 at 6% cache in v1) on 24.5M input tokens at 96.6% cached.

### anthropic-med — Attic Breaker (26/27, resumed; polish still 2)

- **Breakthrough:** get-a-ball-above-the-field fantasy (`src/game/attic.js:2`), attic-break scoring with a time-dilation pulse (`:152-167`).
- **Juice:** pooled particles + ribbon trails, screen shake, beat-synced attic pulse ring.
- **Power-up balls:** 11 kinds — multiball, fire/super/heavy/ghost balls, wide, sticky, lasers, magnet, slowmo, attictime; all drawn and described in the passing powerups runner.
- **Special bricks:** angle + speed defs with per-brick required direction, 1050 speed threshold, 35° angle cone and speed-gate break rules (`src/game/bricks.js:78-81, :133-134, :239-250`).
- **2-D paddle:** targetY clamp with vy tracking; rising uppercuts add up to +900 speed, downward dips soften (`src/game/paddle.js:27-38, :56-59`).
- **Mouse / mobile:** pointer handlers on desktop (score 645, x1.0, WIDE badge live); STICK_MAX_PX=70 thumbstick with touch routing and width-fit portrait on mobile. PLAY-OK both, 0 errors.
- **Polish (2):** letterbox/portrait renderer, tap-to-start gate, functional but spare — plain gradient title, monochrome field; the resume grew code 7,806→10,223 LOC without changing the presentation, so the dock stands.
- **Audio:** synthesized SFX + adaptive music with per-name rate limiting for 40-brick bursts and music that ducks under sfx. 10,223 LOC, the largest build; stopped-capped at $37.03.

### muse — OVERDRIVE BREAKOUT (26/27, $0.04 in 11 minutes)

- **Breakthrough:** topside detect with 1.2s grace, banner + jingle (`js/game.js:602-623`); multiplier grows ×1.015/frame to ×12 while topside; audio intensity forced to 1.
- **Juice (2):** power-hit sparks + floatText, velocity-brick bursts, paddle/ball trails; screen-shake not evidenced.
- **Power-up balls:** multiball ×3, fire plow-through, ghost phase, heavy 1.6× radius, all timed with banner + HUD.
- **Special bricks:** steep/flat/velocity gates with hints, blink timing, drifters; velocity needs 560+ with a flick hint.
- **2-D paddle:** 2D zone below 0.72H across mouse/keys/touch; upward vy<−140 is a power hit (1.18× speed, steeper angle), downward softens.
- **Mouse / mobile:** mouse steers both axes; touch drag with Y offset so the finger never covers the paddle, plus a thumbstick visual. Full-bleed portrait, BREAK THROUGH! marker, 0 errors.
- **Polish:** 120Hz fixed timestep, pause overlay, localStorage best, stale-launch discard, visibility auto-pause.
- **Audio:** intensity drives tempo (132+40), arp octave and density; overdrive enter/exit jingles, brick pitch by combo. The value result of the grid: 26/27, plays clean on both viewports, $0.04 in 11 minutes — no test suite, which is what the ranking rule docks it for.

### glm — NEON BREAKER (25/27)

- **Breakthrough:** detect/toggle with growing btBonus, zone glow above the bricks, ambient ripples while hot — all in one self-contained `index.html`.
- **Juice:** brick-shard particles, shake + flash on brick/bomb, background pulse reacting to multiplier/breakthrough.
- **Power-up balls (2):** multi (splits into 3) and fire (plow-through, 12s) from a POWER_BAG; a thin bench next to peers.
- **Special bricks:** speed-locked (560), directional shield, mover, regenerator, chained bombs, steel, armored, with a level legend.
- **2-D paddle:** 2D band (VIEW.h−170…−44), WASD + arrows; upward-vy boost with BOOST! text, downward damp.
- **Mouse / mobile:** critically-damped pointer chase; floating thumbstick on hold/second finger; full-bleed portrait, 0 errors.
- **Polish (2):** pause, hiscore persistence; rAF with clamped delta rather than a fixed timestep.
- **Audio:** WebAudio synth with combo-pitched bricks and a breakthrough arpeggio. 25/27 for $1.22 in 88 minutes.

### openrouter-high — OVERDRIVE (25/27)

- **Breakthrough:** ball-above-top-row check with banner, double confetti rain, jet particles and shake; ×2 score multiplier in overdrive; six levels with a level select.
- **Juice:** burst/confetti/jet/rise particles; burst size and shake scale with overdrive and combo; comet trail sparkle.
- **Power-up balls:** comet (fast, swept collision so it never tunnels), heavy (smashes through), SPLIT/GROW/life pickups; bonus balls cost no life.
- **Special bricks:** four-direction angle bricks (break only from the arrow direction), SPEED bricks needing boost, bombs, SPLIT LANE level.
- **2-D paddle:** 0.70VH vertical range, WASD/arrows X+Y, vx/vy tracked; upward boost-hit (+250 speed) smashes SPEED bricks.
- **Mouse / mobile (2):** absolute mouse mapping; relative-drag touch with preventDefault — solid, no thumbstick widget; desktop renders letterboxed by design.
- **Polish:** 120Hz fixed timestep, pause, P/Esc, localStorage best, endless mode, no TODOs.
- **Audio (2):** procedural tone/noise/drone with combo-escalating brick pitch and an overdrive jingle; no adaptive layers. Single-file, no dependencies, $1.44 in 23 minutes.

### deepseek — ROOFTOP (24/27, $0.53 vs $0.50 cap)

- **Breakthrough:** rooftop detect with banner + fanfare; multiplier steps to ×10 every 2s, bonus ball every 3.5s (cap 6), 40/s speed ramp.
- **Juice (2):** particle system, ball trails, score popups, fire glow; screen-shake not evidenced.
- **Power-up balls (2):** fire-bullet balls and multiball (+ rooftop bonus balls); lasers are projectiles, not balls.
- **Special bricks:** steep gate (1.15), 700+ speed gate, fire-only steel, angle/speed gates, nested-chain explosives, golden, core.
- **2-D paddle:** tracked/clamped vy with 0.55 vertical transfer; "move up when hitting the ball to boost it".
- **Mouse / mobile:** pointer steering; left-side thumbstick + right-side direct touch; mobile shot shows full-bleed BALL LOST state — real play progressed (desktop score 100, mobile 310).
- **Polish:** 240Hz fixed substeps, pause + auto-pause on blur, persisted rooftop best; qa + smoke probes (`node test/qa.js`).
- **Audio (2):** tone engine with bandpass filter and per-event jingles; intensity scaling not evidenced. Overshot its $0.50 cap between ticks to $0.53.

### openai-low — Orbit Breaker (24/27, $1.13)

- **Breakthrough:** triggerOrbit with BREAKTHROUGH! banner, cascade online, shake + flash; wall-contact + rising-above-brickTop gating per ball.
- **Juice (2):** bursts + rings + floating score per break; per-type brick glyphs; mid-tier volume.
- **Power-up balls:** split/plasma/magnet/overdrive drops with 12s telegraphs, coexisting timed powers in the HUD.
- **Special bricks:** angle (|vy|>|vx|×0.72), 600+ speed, phased windows, with NEED STEEP ANGLE / NEED SPEED reject feedback and wave-scaled angle rows.
- **2-D paddle:** 2D critically-damped follow with tracked vy; UPDRAFT — rising hits gain speed, bonus points and a toast.
- **Mouse / mobile:** pointer drives both axes; rendered thumbstick + knob with pointer capture and a touch pause button.
- **Polish (2):** full menu with brick/orb codex, best run, waves, toasts, portrait tuning — docked because the blind sweep kept dying (read as difficulty/autopilot signal at 616 LOC with 600-speed gates, not breakage; both viewports STARTED clean with 0 errors and menu/HUD/game-over flows all render).
- **Audio (2):** SFX-only tone() voices with combo pitch and orbit/cascade stingers; no music engine. Has a real physics test (`node physics.test.js`: sweeps, solvability, gating, stacking, precedence).

### openrouter-med — OVERDRIVE/frenzy (24/27)

- **Breakthrough (2):** 1.4s-above-bricks frenzy threshold with OVERDRIVE popup, frenzy audio and HUD meter; the threshold mechanic reads slightly arbitrary next to peers.
- **Juice (2):** SMASH popup + shake + ripple, shatter particles, frenzy-scaled glow and trails, hitStop, slowmo.
- **Power-up balls:** fire/split/giant/laser/sticky kinds with random + guaranteed drops and multiball().
- **Special bricks:** 25° angle/graze gate, 640 shock-velocity gate, movers, chain booms, steel; an OVERDRIVE ARENA level built for breakthrough.
- **2-D paddle:** lower-22% 2D zone, arrows + WASD + gamepad; upward smashes earn SMASH! + shake + sound.
- **Mouse / mobile (2):** unified pointer events; viewport-fit with preventDefault — full-bleed with brick-face art, but no thumbstick and a bare black boot overlay title.
- **Polish:** 120Hz fixed timestep, hitStop, pause, persisted best, no TODOs.
- **Audio:** tone/noise/drone oscillators with a beat scheduler; drone gain/filter and beat period follow frenzy, brick pitch by row + combo, frenzy riser.

### qwen — Overdrive Breakout (24/27, the resume saga)

- **Breakthrough:** Overdrive meter with 5 bands and multipliers (`js/overdrive.js`), TOP-260 zone, MAX bonus text/audio, zone glow.
- **Juice:** 430-line fx module — hue-shifting nebula background, sparks, popups, flashScreen; thicker trails in overdrive.
- **Power-up balls (2):** MULTI pickup only (+2 balls, cap 6); WIDE/SLOW/LASER/MAGNET/BOOST are paddle effects, not balls — the docked point.
- **Special bricks:** the widest menagerie in the grid — glass, angle, speed, ramp, magnet, mirror, mover, bomb, mini, key/lock (±35° tolerance, movers, gate clinks).
- **2-D paddle:** accel keys both axes; slam (upward vy → −260, downward weakens); "Slam UP to launch!".
- **Mouse / mobile (1):** smooth pointer-follow lerp; thumb model keeps the finger off the paddle — but the mobile shot confirms a viewport-scaling bug: the game squeezed into a middle band with big black bars and awkward buttons.
- **Polish:** 240Hz substeps, persisted best, pause incl. gamepad, attract demo; no TODOs; dinged for the scaling bug.
- **Audio:** band-scaled loudness/brightness with stereo pan, overdrive ticks, ambient hum, per-brick voices. Most-engineered build (3,556 LOC, 113 min); cap-killed at $0.88, resumed on a raised $3.50 cap, overshot to $4.61 between ticks.

### nous-med — BREAKTHROUGH (22/27, provider-halted)

- **Breakthrough:** chaos mode — onTop detect with doubling multiplier to CHAOS_CAP, intensity follows; breakthrough channel + titan pocket in the levels; BREAKTHROUGH banner.
- **Juice (2):** hitStop slowmo, score popups, vortexBoom with shockwave push; mobile shot shows a fire-ring ball in a full magenta chaos wash; screen-shake not evidenced.
- **Power-up balls (2):** multiball split, 8s fireball plow-through, 6s phase; paddle timers are effects, not balls.
- **Special bricks:** speed-gated armored, angle-gated prism, phase-through ghost, volt chain, vortex boom, gel catch, fire-immune titan.
- **2-D paddle:** both-axes pointer with 260 upward-smash velocity; "move vertically, paddle SMASHES upward".
- **Mouse (2):** mousemove absolute + relative-drag model; title says DRAG to move — mouse works but is not the primary citizen.
- **Mobile (2):** relative touch drag anywhere, viewport-fit; playable with mild letterbox; no thumbstick.
- **Polish (2):** pause + auto-pause, persisted best/mute/reduced-motion, substeps for fast balls but no full fixed timestep.
- **Audio:** chaos beat that layers up with intensity, paddle pitch by vy, per-brick/volt/fanfare voices, persisted mute. Complete despite its halted session: the provider returned 404s ("Couldn't find that, sorry") on the orchestrator route, the session parked as awaiting-input at $0.13, and the game on disk played clean on both viewports with 0 errors.

## Verdict

The ranking rule is stated up front so it can be argued with: rubric total first, then whether the build ships a test suite, then cost ascending. Ties on the rubric are where judgement lives, and the notes below say where the rule produces an order worth disagreeing with.

1. **`hero-fable` — OVERTOP (27/27, $78.11, stopped-capped).** The second perfect rubric in the grid, and the better-documented one: 82 passing tests, 8 genuine ball powers, prism/armor/lid/phase bricks, a 7-tier adaptive soundtrack, PLAY-OK both viewports with 0 errors. The asterisk is the price tag — the most expensive run in the grid by 2×, capped at $75 and killed $3.11 over. Ranked first because 27 with a suite beats 27 without one.
2. **`opencode-med` — BREAKTOP (27/27, $1.81, done).** The first perfect rubric, now resumed to a clean finish. Still the value champion of the top end: richest chrome in group B, live-combo screenshots, procedural music — and still no test suite (its README cites headless-Chromium Playwright verification only). If your rule is 27-is-27 regardless of tests, it never left first place; the stated rule prefers a suite, so it sits second.
3. **`anthropic-high` — OVERTOP (27/27, $40.95, stopped-capped).** The redemption: the `nebula` ReferenceError is fixed (`src/render.js:241`), the canvas renders, physics + level audits pass, and the polish point is now earned rather than credited. Still the priciest 27 per point that isn't a hero — $40.95 cumulative across the stop and the resume.
4. **`astra` — OVERDRIVE (26/27, $18.34, done).** Best of the 26s by the rule: a real vitest suite (124 pass, including an autoplayer that wins all ten sectors) plus the deepest play evidence among the resumed runs. Finished between ticks $0.34 over its $18 resume cap — the good kind of overshoot.
5. **`openai-med` — OVERDRIVE (26/27, $12.12, done).** The art-directed pick: mission brief, field-intelligence codex, flight manual, the most cohesive layout on both viewports, and 14/14 tasks complete. Costs more than its cap said it would ($8.40 → $12.12 between ticks), which is a harness fact, not a build demerit.
6. **`openai-high` — OVERDRIVE, Evolved (26/27, $13.69, done).** The strongest live-play evidence in the grid (multiball banner with pickup toasts on mobile, 6/64 cleared on desktop) with engine/physics/audio unit tests. Docked one rubric point for SFX-only audio and priced highest among the done non-hero runs — though less than half of what the same preset paid in v1.
7. **`anthropic-med` — Attic Breaker (26/27, $37.03, stopped-capped).** Largest build (10,223 LOC), chained-break multiplier under time-dilation, 11 power-up kinds, PLAY-OK both viewports with all runners passing. Ranks below the builds above it on cost at equal rubric-with-tests; its polish point stays docked for a spare monochrome presentation even after +2,417 LOC.
8. **`hero-astra` — ASTRA (26/27, $30.39, done in 33 minutes).** The fast hero: 5/5 tasks, 29 passing tests, a full site shell, real game-over progression photographed — done in half an hour with a clean log. Sits mid-26s only because the stated rule weighs its suite below cheaper suites; SFX-only audio is the on-rubric dock, and $30.39 is the off-rubric one.
9. **`muse` — OVERDRIVE BREAKOUT (26/27, $0.04, done in 11 minutes).** The value result of the grid, by distance. Full-bleed portrait, power-hit smashes, adaptive tempo, clean play on both viewports — for four cents in eleven minutes. It ranks last among the 26s only because the stated rule prefers a test suite to a price tag; if your rule is code-per-dollar that plays, it wins the whole grid.
10. **`glm` — NEON BREAKER (25/27, $1.22, done).** A single self-contained `index.html` with a floating thumbstick, chained bombs and a breakthrough arpeggio. Thin power-ball bench and no fixed timestep keep it at 25.
11. **`openrouter-high` — OVERDRIVE (25/27, $1.44, done).** Six levels, swept comet collision, endless mode, no dependencies — the best single-file engineering per dollar. No thumbstick and SFX-simple audio hold it at 25.
12. **`deepseek` — ROOFTOP (24/27, $0.53, done).** Real play progression photographed on both viewports (score 100 desktop, BALL LOST at 310 mobile) with a left-side thumbstick — fifty-three cents that plays. Lasers-as-projectiles and unscaled audio keep it at 24.
13. **`openai-low` — Orbit Breaker (24/27, $1.13, done).** The small-model surprise: a rendered thumbstick, UPDRAFT paddle boosts, reject-feedback gates and a real physics test at 616 LOC. The blind sweep kept dying — read as a difficulty signal, not breakage.
14. **`openrouter-med` — OVERDRIVE/frenzy (24/27, $0.66, done).** Solid systems (frenzy meter, hitStop, 120Hz loop) under a bare boot-overlay presentation with no thumbstick.
15. **`qwen` — Overdrive Breakout (24/27, $4.61, done).** The most-engineered build (3,556 LOC, 5-band overdrive meter, key/lock bricks, attract demo) sunk by one viewport-scaling bug on mobile and a power-ball bench that is really paddle effects. Plus the saga below.
16. **`nous-med` — BREAKTHROUGH (22/27, $0.13, halted-provider).** Last on the rubric, first on grit-per-cent: its orchestrator route 404'd twice, the session parked as awaiting-input, and the game on disk still played clean on both viewports. Mouse-as-second-citizen and effect-not-ball powers are the real docks.

## What the grid taught the harness

v1's fixes survived contact with a bigger grid; v2's failures propose the next ones. All four v1 fixes shipped in this binary, and all four held:

- **Cache fix verified, again.** Recorded steers kept every prompt a strict extension of the last across all sixteen runs; overall cache hit was 92.5%, and `openai-high` went from 6% cached ($31.59 on 6.9M input tokens in v1) to 96.6% cached ($13.69 on 24.5M input tokens now) — ~3× the tokens for less than half the money. Read any remaining cost as the price of the model, not the harness.
- **Fallbacks survived a real outage, twice.** The OpenAI-direct key ran out of credits mid-grid (502s onward) and stayed dry through phase 3; affected runs — including both heroes — continued on their OpenRouter fallbacks by preset design, billed at OpenRouter catalog rates. The `astra` log shows the same story on Together (402 credit-limit failures absorbed on late tasks).
- **Image-rejection and read-curb fixes held.** No run lost a task to an image-input 404 this time, and re-read curbs kept the long_session token profiles sane.

Phase-3 lessons, all about resumes and money:

- **Resumes work because the event log is the state.** Four interrupted runs resumed days later (across a budget SIGTERM, two cap kills and a global-brake kill) and every one of them produced a better game than it stopped with. See RESUMABILITY.
- **The redemption was real.** A one-line fix (`src/render.js:241`) turned v2's exhibit-A failure into a verified 27. Verification that looks at the screen catches in one re-shoot what code reading never will.
- **Heroes cost hero money.** The two hero runs together ($108.50) outspent the entire fourteen-preset phase 1 ($91.54). `hero-fable` at $78.11 is a second 27; `opencode-med` at $1.81 is the first. Price per rubric point differs by ~40×. When to buy xhigh is still an open question — see the carried-over question below.
- **Caps need a floor — propose $2.** The `qwen` cap killed a healthy run at $0.88; the resume cost operator attention plus three restarts to land at $4.61. Caps under ~$2 don't buy control, they buy churn. Set $2 as the minimum cap and let cheap runs simply be cheap.
- **Enforcement happens between ticks, so caps overshoot.** Seven runs sailed past their caps while a model call was in flight (see BUDGET CAVEATS). A cap is closer to a suggestion with a SIGTERM attached; size caps assuming ~50% overshoot on agentic presets.
- **SIGTERM works as a budget tool when the game is already verified.** Both Anthropic runs were SIGTERM'd after passing verification and both left complete, playable, high-scoring games — and both resumed cleanly. Stopping spend is not the same as stopping work — verify first, kill freely.
- **The global brake works.** `astra` died on the $90 global brake at $7.40 mid-verification with a verified-playable game on disk. Total run spend $91.54; measuring overhead ~$0.50; grand total $92.04 against the $100 budget.
- **Provider halts park, they don't burn.** `nous-med`'s Nous 404s ended the session as awaiting-input at $0.13 with a complete game on disk — the cheapest lesson in the grid: a halted run is resumable, not lost.
- **Resumes need preset hygiene.** The `qwen` saga's ugly middle — 4 read-only calls against the wrong preset, killed before writing anything — argues for pinning the preset visibly in the resume path so a misdirected resume is impossible, not just killable.

One open question, carried over from v1 and now louder: nothing signals diminishing returns. `qwen` (113 min) and `astra` (248 min cumulative) both ran an order of magnitude longer than `muse` (11 min) for rubric scores within two points of it. When to stop is still unanswered.

## RESUMABILITY

Phase 3 touched six runs — four resumes and two fresh hero launches — and every stop was SIGTERM-safe: the event log is the state, so killing a run never loses work, it only pauses billing.

- `anthropic-high` / `anthropic-med`: budget-SIGTERM'd in phase 1 after passing verification; resumed 2026-09-09 under new caps ($40/$36), ran to cap-kill at $40.95/$37.03. Both improved in the resume (nebula fix + passing harnesses; +2,417 LOC + passing runners).
- `astra`: killed by the global $90 brake at $7.40 mid-verification; resumed under an $18 per-run cap, finished between ticks at $18.34 (`done` — the overage landed inside the final model call).
- `opencode-med`: cap-killed at $1.11 mid-verification; resumed under a $5 cap, finished clean at $1.81 (`done`, 8/11 tasks).
- `hero-astra` / `hero-fable`: fresh launches on hero presets, not resumes — included here because they exercised the same stop path (`hero-fable` cap-killed at $78.11 vs $75) with the same clean outcome.
- The v2 `qwen` saga stands as the incident report: cap-killed at $0.88, resumed on a raised $3.50 cap, but the first resume attempt ran 4 read-only calls against the wrong preset — killed before writing anything, then resumed correctly to finish at $4.61. No recurrence in phase 3; the preset-hygiene proposal (pin the preset in the resume path) stands.

No run rolled its context over in either phase. All 6 PLAY-OK checks in phase 3 passed with zero errors.

## BUDGET CAVEATS

Stated plainly against the original $100 budget: phase 1 spent $91.54 of runs + ~$0.50 overhead = $92.04 grand total (under budget). Phase 3 added $150.65 of runs ($16.64 + $13.85 + $10.95 + $0.71 in resume deltas, plus $30.39 + $78.11 for the heroes) plus its share of orchestration/judging overhead — the new grand total is in `reports/grid-costs.json`, roughly 2.4× the original envelope. The envelope was sized for fourteen presets, not sixteen plus resumes; the brake was raised accordingly ($90 → $280 global, 2026-09-09) and per-run caps were set or raised for every touched run.

Every cap overshoot, none hidden — enforcement checks between model calls, so a run in flight sails past:

| Preset | Cap | Final | Over |
|---|---|---|---|
| `openai-med` | $8.40 | $12.12 | +$3.72 |
| `deepseek` | $0.50 | $0.53 | +$0.03 |
| `qwen` | $3.50 (raised after $0.88 kill) | $4.61 | +$1.09 |
| `astra` (resume) | $18.00 | $18.34 | +$0.34, done between ticks |
| `anthropic-med` (resume) | $36.00 | $37.03 | +$1.03 |
| `anthropic-high` (resume) | $40.00 | $40.95 | +$0.95 |
| `hero-fable` | $75.00 | $78.11 | +$3.11 |

Raised caps: `qwen` (killed at $0.88, resumed on a raised $3.50 cap), `opencode-med` (killed at $1.11, resumed under $5.00, finished $1.81), the four resume caps above ($40/$36/$18/$5, all new for phase 3), and the two hero caps ($75 each). Global brake $90 → $280 with the phase-3 setup on 2026-09-09.

Per-version spend for the four resumes lives in `results.json` (`spend_split` per preset: v1_cost + resume_cost). OpenAI-direct ran dry mid-grid and stayed dry: every OpenRouter-fallback call is flagged in `results.json` (`routes` on `astra`, `hero-astra`, `hero-fable`) and priced at OpenRouter catalog rates, not Anthropic/OpenAI-direct list. `nous-med`'s 404s are unchanged from v2 — halted-provider at $0.13, game intact.

## Methods and caveats

- Play was scripted Chromium (Playwright), not a human: a multi-step start per build (title → launch → sweeps), mouse sweeps on desktop, scripted touch drags on the phone, at 1280×800 and 390×844 (touch).
- Phase 3 re-shot, re-played and re-judged exactly six builds (the four resumes + two heroes); the other ten scores, screenshots and reports are carried over from v2 byte-identical.
- Entries include build output: `openai-high`, `openai-med`, `astra` and `hero-astra` were judged and played from `dist/index.html` (Vite builds); the rest from `index.html` (or their game root).
- Sound was never heard; the play harness was headless. Audio scores come from the code.
- Rubric scores came from code reading plus that scripted play, not from a blind review.
- Costs are September 2026 list-price estimates and are heavily influenced by each provider's cache economics, so cross-provider comparisons are not apples to apples. Fallback calls (OpenAI-direct dry → OpenRouter) are priced at OpenRouter catalog rates.
- `anthropic-high`'s v2 caveat is resolved: the `nebula` ReferenceError is fixed at `src/render.js:241` and the canvas renders on both viewports. Its v2 code-only 26 is now a play-verified 27.
- `openai-low`'s blind sweep died during play on both viewports; read as a difficulty/autopilot signal (600-speed gates at 616 LOC), not breakage — menu, HUD and game-over flows all render.
- `qwen`'s mobile score reflects a confirmed viewport-scaling bug (squeezed middle band, black bars), not the desktop game.
- All interventions are listed here, none hidden: phase 1 — `qwen` cap-killed at $0.88 then resumed on a raised $3.50 cap (with 4 read-only wrong-preset calls killed before any write, then a correct resume); `anthropic-high`/`anthropic-med` budget-SIGTERM'd after passing verification; `opencode-med` cap-killed at $1.11; `astra` killed by the global $90 brake at $7.40; `openai-med`/`deepseek`/`qwen` overshot caps between ticks; the OpenAI-direct key ran dry mid-grid with OpenRouter fallbacks absorbing it; `nous-med` halted on Nous 404s as awaiting-input. Phase 3 — four resumes under new caps ($40/$36/$18/$5) plus two hero launches ($75 caps); overshoots on seven runs (see BUDGET CAVEATS); brake raised $90 → $280.
- Frame rate was not measured; several builds ship timestep claims (120–240Hz fixed/substepped) that were not independently verified.
- Every number on this page is in [`docs/grid/results.json`](grid/results.json); the v1 dataset is archived at [`docs/grid/results-2026-09-07.json`](grid/results-2026-09-07.json); the v2 dataset survives in git history; the spec is [`docs/grid/SPEC.md`](grid/SPEC.md).

## How to reproduce

Put the spec in a fresh directory as `INSTRUCTIONS.md` and run the same prompt once per preset:

```
EAGENT_FINALECHAT=off eagent --preset NAME -p "Read INSTRUCTIONS.md and relentlessly build it until you're confident that it's fully and completely implemented to the highest possible standard you can manage."
```

`EAGENT_FINALECHAT=off` keeps the phone mirror out of the way for a grid of unattended runs. Sixteen directories, sixteen presets (`glm`, `openrouter-high`, `openrouter-med`, `anthropic-high`, `anthropic-med`, `openai-high`, `openai-med`, `openai-low`, `astra`, `deepseek`, `qwen`, `muse`, `opencode-med`, `nous-med`, `hero-astra`, `hero-fable`), run at the same time on `v0.8.0-1-g59ce5c7`, reproduces the set (heroes need their xhigh hero-model routes). The measuring run compiled `reports/*.metrics.json`, `*.judge.json` and `*.play.json` per preset into `docs/grid/results.json`, with per-preset spend, phase splits and the budget accounting in `reports/grid-costs.json`.

## Variance annex

Follow-up repeats of the cheapest builds are written up in [`docs/grid/VARIANCE.md`](grid/VARIANCE.md) and staged as the live [variance annex](https://ericflo.github.io/eagent/variance/): muse 10/10 at $0.04±0.01, fireworks-med 9/10 at $0.78±0.29, openrouter-med 8/10 at $0.98±0.51, deepseek-fw 6/10 at $0.66±0.15.
The headline driver is verification depth, not building — every timeout was a QA worker iterating open-endedly with the finished game already on disk.



