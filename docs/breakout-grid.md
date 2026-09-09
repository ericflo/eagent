# The Breakout grid v2: fourteen presets, one brief

On 2026-09-08/09 fourteen eagent presets each built the same game from the same one-paragraph spec, in parallel, unattended, on binary v0.8.0-1-g59ce5c7 (with all four cache/fallback fixes from the first grid). A measuring run then compiled all fourteen: it parsed their event logs, served each build, played it with scripted Chromium, took screenshots, and scored it against the same nine-item rubric as v1. This page is the record: what ran, what it cost, what each build shipped, and what the exercise changed in the harness. It replaces the six-preset grid of 2026-09-07, which is archived as [`docs/grid/results-2026-09-07.json`](grid/results-2026-09-07.json).

## What was run

The spec is [`docs/grid/SPEC.md`](grid/SPEC.md). In one paragraph: a modern take on Breakout built around the moment the ball gets above the bricks and keeps going on its own, with things escalating once it is up there. Game juice that scales with score and multiplier. Power-up balls, and bricks that are difficult in ways other than hit counts, such as a required angle or a required speed. A paddle that also moves up and down, where moving up on contact accelerates the ball. Mouse play, with mobile thumb or thumbstick control as a first-class citizen. Polish and audio.

Every build got the same prompt:

> Read INSTRUCTIONS.md and relentlessly build it until you're confident that it's fully and completely implemented to the highest possible standard you can manage.

The fourteen presets, with the model each actor ran (orchestrator / task worker / narrator):

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

Each run started in its own directory holding nothing but `INSTRUCTIONS.md`, with the phone mirror off (`EAGENT_FINALECHAT=off`). Nine runs ended by declaring the work done (`muse`, `openrouter-high`, `openrouter-med`, `openai-low`, `openai-med`, `openai-high`, `glm`, `deepseek`, `qwen`). Two were stopped by budget SIGTERM after passing verification (`anthropic-high` $24.31, `anthropic-med` $23.17). One was halted by its provider (`nous-med` $0.13 — Nous 404s, ended awaiting-input). Two were killed by caps mid-verification (`opencode-med` $1.11, `astra` $7.40 on the global $90 brake). No run rolled its context over, and no narrator asked a question.

The measuring run parsed each run's JSONL event logs for duration, tokens, cache hits, tasks, narrator messages and errors; computed cost at September 2026 list prices; served each build statically (Vite builds from `dist/`) and played it with scripted Chromium (Playwright) at a 1280×800 desktop viewport and a 390×844 touch phone viewport with a multi-step start; captured console and page errors; and scored the nine spec items from code reading plus that play. Run spend totalled $91.54. Its raw per-build reports live with the run directories outside this repository; [`docs/grid/results.json`](grid/results.json) is the compiled data.

## Results

All numbers are from `results.json`. Status is `done` (declared done itself), `stopped-budget` (SIGTERM after passing verification), `stopped-capped` (killed by a cost cap), or `halted-provider` (provider errors ended it).

| Preset | Status | Duration | Cost | Tokens in / out | Cache | Tasks done | Narr msgs | Errors | Files / LOC | Tests | Play errors |
|---|---|---|---|---|---|---|---|---|---|---|---|
| `opencode-med` | stopped-capped | 100 min | $1.11 | 17.9M / 232k | 95% | 5/8 | 38 | 13 | 15 / 3,054 | none | 0 / 0 |
| `astra` | stopped-capped | 110 min | $7.40 | 48.3M / 972k | 87% | 5/8 | 47 | 36 | 245 / 976* | yes (vitest) | 0 / 0 |
| `openai-med` | done | 27 min | $12.12 | 17.4M / 209k | 95% | 14/14 | 41 | 3 | 30 / 2,229* | yes (node --test) | 0 / 0 |
| `openai-high` | done | 42 min | $13.69 | 24.5M / 182k | 97% | 5/8 | 50 | 26 | 77 / 3,044* | yes (tsx --test) | 0 / 0 |
| `anthropic-med` | stopped-budget | 60 min | $23.17 | 57.6M / 608k | 96% | 7/9 | 33 | 7 | 21 / 7,806 | per-file node runners | 0 / 0 |
| `anthropic-high` | stopped-budget | 60 min | $24.31 | 18.8M / 387k | 94% | 2/4 | 21 | 5 | 42 / 6,010 | per-file node runners | 0 / 2 |
| `muse` | done | 11 min | $0.04 | 2.4M / 73k | 92% | 2/2 | 7 | 2 | 10 / 1,907 | none | 0 / 0 |
| `glm` | done | 88 min | $1.22 | 9.3M / 258k | 79% | 2/3 | 36 | 11 | 3 / 2,035 | none | 0 / 0 |
| `openrouter-high` | done | 23 min | $1.44 | 2.5M / 124k | 88% | 2/2 | 16 | 2 | 3 / 1,577 | none | 0 / 0 |
| `deepseek` | done | 101 min | $0.53 | 8.4M / 404k | 82% | 1/3 | 54 | 13 | 23 / 1,581 | yes (qa/smoke) | 0 / 0 |
| `openai-low` | done | 37 min | $1.13 | 18.2M / 153k | 97% | 5/5 | 27 | 4 | 12 / 616 | yes (physics.test.js) | 0 / 0 |
| `openrouter-med` | done | 23 min | $0.66 | 3.4M / 65k | 73% | 2/2 | 14 | 4 | 17 / 1,459 | none | 0 / 0 |
| `qwen` | done | 113 min | $4.61 | 9.6M / 192k | 57% | 1/3 | 46 | 7 | 15 / 3,556 | none | 0 / 0 |
| `nous-med` | halted-provider | 34 min | $0.13 | 2.7M / 37k | 90% | 1/1 | 10 | 2 | 16 / 1,375 | none | 0 / 0 |

\* `astra`, `openai-high` and `openai-med` are Vite builds: the judged entry is `dist/index.html`, so file counts include `dist/` output and LOC counts understate hand-written source.

Model calls, as orchestrator / task worker / narrator: `opencode-med` 434 (48/333/53), `astra` 753 (64/624/65), `openai-med` 448 (78/317/53), `openai-high` 487 (65/371/51), `anthropic-med` 620 (35/542/43), `anthropic-high` 231 (12/190/29), `muse` 77 (20/45/12), `glm` 286 (55/178/53), `openrouter-high` 110 (33/56/21), `deepseek` 231 (92/77/62), `openai-low` 301 (31/240/30), `openrouter-med` 133 (27/83/23), `qwen` 226 (78/76/72), `nous-med` 112 (15/70/27).

Leaderboard (rubric, then tests, then cost — see Verdict for the rule):

| # | Preset | Rubric | Tests | Cost |
|---|---|---|---|---|
| 1 | `opencode-med` | 27 | none | $1.11 |
| 2 | `astra` | 26 | vitest | $7.40 |
| 3 | `openai-med` | 26 | node --test | $12.12 |
| 4 | `openai-high` | 26 | tsx --test | $13.69 |
| 5 | `anthropic-med` | 26 | per-file runners | $23.17 |
| 6 | `anthropic-high` | 26 | per-file runners | $24.31 |
| 7 | `muse` | 26 | none | $0.04 |
| 8 | `glm` | 25 | none | $1.22 |
| 9 | `openrouter-high` | 25 | none | $1.44 |
| 10 | `deepseek` | 24 | qa / smoke | $0.53 |
| 11 | `openai-low` | 24 | physics.test.js | $1.13 |
| 12 | `openrouter-med` | 24 | none | $0.66 |
| 13 | `qwen` | 24 | none | $4.61 |
| 14 | `nous-med` | 22 | none | $0.13 |

Notes on the table:

- Overall prompt-cache hit rate was 91.2%. The cache fix from v1 is verified: `openai-high` went from 6% cached ($31.59 on 6.9M input tokens) to 96.6% cached ($13.69 on 24.5M input tokens) — roughly 3× the tokens for less than half the money.
- Thirteen of fourteen builds played clean: zero console errors everywhere, zero page errors except `anthropic-high` (2 × `nebula is not defined`, desktop and mobile — a genuine bug, see Verdict).
- The OpenAI-direct key ran out of credits mid-grid (502s onward); affected runs survived on their OpenRouter fallbacks by preset design. The `astra` log also shows Together 402 credit-limit failures on three late tasks.
- Caps overshot between ticks: `openai-med` finished at $12.12 against an $8.40 cap, `deepseek` at $0.53 vs $0.50, `qwen` at $4.59 vs a raised $3.50 cap. Enforcement only checks between model calls, so a run in flight sails past.
- `qwen` was cap-killed once at $0.88, resumed with a raised cap, then briefly ran 4 read-only calls against the wrong preset (no files written — killed before it could do anything), and resumed correctly on `qwen` to finish at $4.61.
- External dependencies are rare: only `deepseek` and `openai-med` load Google Fonts; everything else is dependency-free at runtime.

## Rubric

Each spec item scored 0 to 3: 3 means present, working and genuinely well done; 2 present and working; 1 token effort; 0 absent. Fourteen columns will not fit a readable table, so presets are rows.

| Preset | Breakthrough | Juice | Power-balls | Special bricks | 2-D paddle | Mouse | Mobile | Polish | Audio | **Total** |
|---|---|---|---|---|---|---|---|---|---|---|
| `opencode-med` | 3 | 3 | 3 | 3 | 3 | 3 | 3 | 3 | 3 | **27** |
| `astra` | 3 | 2 | 3 | 3 | 3 | 3 | 3 | 3 | 3 | **26** |
| `openai-med` | 3 | 2 | 3 | 3 | 3 | 3 | 3 | 3 | 3 | **26** |
| `openai-high` | 3 | 3 | 3 | 3 | 3 | 3 | 3 | 3 | 2 | **26** |
| `anthropic-med` | 3 | 3 | 3 | 3 | 3 | 3 | 3 | 2 | 3 | **26** |
| `anthropic-high` | 3 | 3 | 3 | 3 | 3 | 3 | 3 | 2 | 3 | **26** |
| `muse` | 3 | 2 | 3 | 3 | 3 | 3 | 3 | 3 | 3 | **26** |
| `glm` | 3 | 3 | 2 | 3 | 3 | 3 | 3 | 2 | 3 | **25** |
| `openrouter-high` | 3 | 3 | 3 | 3 | 3 | 3 | 2 | 3 | 2 | **25** |
| `deepseek` | 3 | 2 | 2 | 3 | 3 | 3 | 3 | 3 | 2 | **24** |
| `openai-low` | 3 | 2 | 3 | 3 | 3 | 3 | 3 | 2 | 2 | **24** |
| `openrouter-med` | 2 | 2 | 3 | 3 | 3 | 3 | 2 | 3 | 3 | **24** |
| `qwen` | 3 | 3 | 2 | 3 | 3 | 3 | 1 | 3 | 3 | **24** |
| `nous-med` | 3 | 2 | 2 | 3 | 3 | 2 | 2 | 2 | 3 | **22** |

The evidence behind each score, per build, quoted from the measuring run's reports:

### opencode-med — BREAKTOP (27/27)

- **Breakthrough:** `js/game.js:863-884` overdrive entry on ball-above-rim while rising (banner + flash + shake + music lift), `:885-900` rim exit pays out; charge past 55% drops a reward capsule.
- **Juice:** pooled particles + score popups (`js/particles.js:2`), slow-mo timeScale (`:344-358`), lift-scaled shake, overdrive heat vignette; desktop shot shows +136/+124 popups and COMBO x3 live.
- **Power-up balls:** `js/powerups.js:11-18` FIRE/GIANT/SLOW/MAGNET/STICKY/WIDE/BURST with a weighted drop table; a falling WIDE capsule photographed mid-game.
- **Special bricks:** `js/bricks.js:54-78` steep-only / speed-gate / speed+steep gates with STEEP ONLY / NEED SPEED fail reasons, plus armored, chain-explosive and spinner codes on the title screen.
- **2-D paddle:** vertical band clamp, vy lerp, squash-and-stretch (`:463-472`); rising lift adds up to +150 ball speed (`:675-680`).
- **Mouse:** pointer maps to paddle target in the lower zone (`js/input.js:51-57`); richest title screen in the grid (run status, controls, 6 brick codes, overdrive rules).
- **Mobile:** drag + sliding-stick touch origin (`:12, :68-73`); mobile shot is a clean stacked layout with DRAG badge and tap-to-launch. PLAY-OK both viewports, 0 errors.
- **Polish:** side panels, charge/lives/combo/multiplier HUD, pause/sound, localStorage best; live desktop shot (score 483, best combo x3).
- **Audio:** procedural 8th-note music stepper with layers 0–3 plus overdrive heat (`js/audio.js:16-17, :181-214`); combo and lift drive the layers. The only 3-for-audio without qualification issues — and the only 27 overall, with no test suite.

### astra — OVERDRIVE (26/27, cap-killed mid-verification)

- **Breakthrough:** genuine above-the-roof breach detection (`src/engine/Game.ts:576-590`), breakthrough heat that decays slowly, tiered overdrive meter until "the arena burns".
- **Juice (2):** particles + floating text, screen flash, ball trails, camera shake — present and working, modest next to peers.
- **Power-up balls:** plasma/split/chain/wide/slow/life (`src/engine/types.ts:5`); split is multiball, chain is lightning.
- **Special bricks:** standard/angular/speed/steel with rejection-flash feedback and a gate legend.
- **2-D paddle:** clamped vertical zone with rising-feed / dipping-soften (`Game.ts:330-348`, lift boost in `physics.ts:109-114`).
- **Mouse / mobile:** single pointer surface for mouse + touch; left-half thumbstick (r62), right-half drag; title offers CADET/PILOT/ACE with a live demo behind the modal. PLAY-OK both viewports, 0 errors — mobile shot caught a real `BALL LOST - 1 left` toast.
- **Polish:** 3 difficulties, how-to, field guide, sound/music/thumbstick/calm toggles, prefs + best persisted. Vite build (`dist/index.html`), vitest suite (`npm test`).
- **Audio:** separate sfx + music gains, minor-pentatonic sequencer driven by overdrive energy. Killed by the global $90 brake at $7.40 mid-verification — the game was already verified playable.

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

### anthropic-med — Attic Breaker (26/27, budget-SIGTERM)

- **Breakthrough:** get-a-ball-above-the-field fantasy (`src/game/attic.js:2`), attic-break scoring with a time-dilation pulse (`:152-167`).
- **Juice:** pooled particles + ribbon trails, screen shake, beat-synced attic pulse ring.
- **Power-up balls:** 11 kinds — multiball, fire/super/heavy/ghost balls, wide, sticky, lasers, magnet, slowmo, attictime.
- **Special bricks:** angle + speed defs with per-brick required direction and a 1050 speed threshold.
- **2-D paddle:** targetY clamp with vy tracking; rising uppercuts add up to +900 speed, downward dips soften.
- **Mouse / mobile:** pointer handlers on desktop; STICK_MAX_PX=70 thumbstick with touch routing and width-fit portrait on mobile. PLAY-OK both, 0 errors.
- **Polish (2):** letterbox/portrait renderer, tap-to-start gate, functional but spare — plain title, monochrome field; the docked point.
- **Audio:** synthesized SFX + adaptive music with per-name rate limiting for 40-brick bursts and music that ducks under sfx. 7,806 LOC, the largest build; SIGTERM'd at $23.17 after passing verification.

### anthropic-high — OVERTOP (26/27, budget-SIGTERM — canvas is black)

- **Breakthrough:** overtop hysteresis — climb above brick tops in one continuous run (`src/game.js:17-19`), multiplier `1 + overtopTime/2 + streak/3`.
- **Juice:** particles, shards, rings, shake/zoom, flashes, floating text; sky tint warms with intensity.
- **Power-up balls:** multiball/fireball/ghostball/magnet/wide/slowmo/laser + heavy with timed ball mods.
- **Special bricks:** steep-only and horizontal angle gates plus a speed gate, with slow/ghost/topOnly condition kinds.
- **2-D paddle:** lowest-22% vertical band; smashVel remembers upward speed so late smashes count.
- **Mouse / mobile:** pointer capture with mouse target mode; thumbstick-or-drag touch routing with `viewport-fit=cover`. Designed well — never seen running.
- **Polish (2):** persistence, settings, mute; play-unverified because the canvas never renders.
- **Audio:** adaptive procedural music with a lookahead scheduler and intensity-gated layers. The honest caveat, stated plainly: `src/render.js:214` calls `nebula = buildNebula()` with no such binding, so the first frame throws and every screenshot is pure black with 2 page errors. This is best-code-that-doesn't-run, not a fraudulent 26 — every point is earned in the code, and none of it is playable. SIGTERM'd at $24.31 after passing verification (verification evidently did not include looking at the screen).

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
- **Juice (2):** particle system, ball trails, score floaters, fire glow; screen-shake not evidenced.
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

1. **`opencode-med` — BREAKTOP (27/27, $1.11, stopped-capped).** The only perfect rubric in the grid, and the only build whose screenshots show a live combo (×3) with score popups and a falling capsule mid-game. The asterisk: no test suite (its README cites headless-Chromium Playwright verification only), and it was cap-killed mid-verification — finished on quality, stopped on budget. Ranked first because 27 is 27.
2. **`astra` — OVERDRIVE (26/27, $7.40, stopped-capped).** Best of the 26s by the rule: a real vitest suite plus the most complete play evidence among the capped runs (title with 3 difficulties and live demo, mid-run desktop, BALL LOST toast on mobile proving progression). Killed by the global $90 brake after the game was already verified.
3. **`openai-med` — OVERDRIVE (26/27, $12.12, done).** The art-directed pick: mission brief, field-intelligence codex, flight manual, the most cohesive layout on both viewports, and 14/14 tasks complete. Costs more than its cap said it would ($8.40 → $12.12 between ticks), which is a harness fact, not a build demerit.
4. **`openai-high` — OVERDRIVE, Evolved (26/27, $13.69, done).** The strongest live-play evidence in the grid (multiball banner with pickup toasts on mobile, 6/64 cleared on desktop) with engine/physics/audio unit tests. Docked one rubric point for SFX-only audio and priced highest among the done runs — though less than half of what the same preset paid in v1.
5. **`anthropic-med` — Attic Breaker (26/27, $23.17, stopped-budget).** Largest build (7,806 LOC), chained-break multiplier under time-dilation, 11 power-up kinds, PLAY-OK both viewports. Ranks below the builds above it on cost at equal rubric-with-tests; its polish point was docked for a spare monochrome presentation.
6. **`anthropic-high` — OVERTOP (26/27, $24.31, stopped-budget).** The honest disappointment: by the code it is superb — overtop hysteresis, smashVel memory, adaptive procedural music — but the canvas is black in play (`nebula is not defined`, `src/render.js:214`) and every screenshot is a black rectangle. A fraudulent 26? No — an honest one: best code that doesn't run. Every point is earned in the source; none of it is playable. It ranks here by the rule and belongs lower by any rule that weights play; read it as a warning about verification that never looks at the screen.
7. **`muse` — OVERDRIVE BREAKOUT (26/27, $0.04, done in 11 minutes).** The value result of the grid, by distance. Full-bleed portrait, power-hit smashes, adaptive tempo, clean play on both viewports — for four cents in eleven minutes. It ranks last among the 26s only because the stated rule prefers a test suite to a price tag; if your rule is code-per-dollar that plays, it wins the whole grid.
8. **`glm` — NEON BREAKER (25/27, $1.22, done).** A single self-contained `index.html` with a floating thumbstick, chained bombs and a breakthrough arpeggio. Thin power-ball bench and no fixed timestep keep it at 25.
9. **`openrouter-high` — OVERDRIVE (25/27, $1.44, done).** Six levels, swept comet collision, endless mode, no dependencies — the best single-file engineering per dollar. No thumbstick and SFX-simple audio hold it at 25.
10. **`deepseek` — ROOFTOP (24/27, $0.53, done).** Real play progression photographed on both viewports (score 100 desktop, BALL LOST at 310 mobile) with a left-side thumbstick — fifty-three cents that plays. Lasers-as-projectiles and unscaled audio keep it at 24.
11. **`openai-low` — Orbit Breaker (24/27, $1.13, done).** The small-model surprise: a rendered thumbstick, UPDRAFT paddle boosts, reject-feedback gates and a real physics test at 616 LOC. The blind sweep kept dying — read as a difficulty signal, not breakage.
12. **`openrouter-med` — OVERDRIVE/frenzy (24/27, $0.66, done).** Solid systems (frenzy meter, hitStop, 120Hz loop) under a bare boot-overlay presentation with no thumbstick.
13. **`qwen` — Overdrive Breakout (24/27, $4.61, done).** The most-engineered build (3,556 LOC, 5-band overdrive meter, key/lock bricks, attract demo) sunk by one viewport-scaling bug on mobile and a power-ball bench that is really paddle effects. Plus the saga below.
14. **`nous-med` — BREAKTHROUGH (22/27, $0.13, halted-provider).** Last on the rubric, first on grit-per-cent: its orchestrator route 404'd twice, the session parked as awaiting-input, and the game on disk still played clean on both viewports. Mouse-as-second-citizen and effect-not-ball powers are the real docks.

## What the grid taught the harness

v1's fixes survived contact with a bigger grid; v2's failures propose the next ones. All four v1 fixes shipped in this binary, and all four held:

- **Cache fix verified.** Recorded steers kept every prompt a strict extension of the last across all fourteen runs; overall cache hit was 91.2%, and `openai-high` went from 6% cached ($31.59 on 6.9M input tokens in v1) to 96.6% cached ($13.69 on 24.5M input tokens now) — ~3× the tokens for less than half the money. Read any remaining cost as the price of the model, not the harness.
- **Fallbacks survived a real outage.** The OpenAI-direct key ran out of credits mid-grid (502s onward) and affected runs continued on their OpenRouter fallbacks by preset design — the exact failure mode `95b364a` was built for. The `astra` log shows the same story on Together (402 credit-limit failures absorbed on late tasks).
- **Image-rejection and read-curb fixes held.** No run lost a task to an image-input 404 this time, and re-read curbs kept the long_session token profiles sane.

New lessons, all about money and stopping:

- **Caps need a floor — propose $2.** The `qwen` cap killed a healthy run at $0.88; the resume cost operator attention plus three restarts to land at $4.61. Caps under ~$2 don't buy control, they buy churn. Set $2 as the minimum cap and let cheap runs simply be cheap.
- **Enforcement happens between ticks, so caps overshoot.** `openai-med` ($12.12 vs $8.40), `deepseek` ($0.53 vs $0.50) and `qwen` ($4.59 vs raised $3.50) all sailed past their caps while a model call was in flight. A cap is closer to a suggestion with a SIGTERM attached; size caps assuming ~50% overshoot on agentic presets.
- **SIGTERM works as a budget tool when the game is already verified.** Both Anthropic runs were SIGTERM'd after passing verification and both left complete, playable, high-scoring games. Stopping spend is not the same as stopping work — verify first, kill freely.
- **The global brake works.** `astra` died on the $90 global brake at $7.40 mid-verification with a verified-playable game on disk. Total run spend $91.54; measuring overhead ~$0.50; grand total $92.04 against the $100 budget.
- **Provider halts park, they don't burn.** `nous-med`'s Nous 404s ended the session as awaiting-input at $0.13 with a complete game on disk — the cheapest lesson in the grid: a halted run is resumable, not lost.
- **Resumes need preset hygiene.** The `qwen` saga's ugly middle — 4 read-only calls against the wrong preset, killed before writing anything — argues for pinning the preset visibly in the resume path so a misdirected resume is impossible, not just killable.

One open question, carried over from v1 and now louder: nothing signals diminishing returns. `qwen` (113 min) and `astra` (110 min) both ran an order of magnitude longer than `muse` (11 min) for rubric scores within two points of it. When to stop is still unanswered.

## Methods and caveats

- Play was scripted Chromium (Playwright), not a human: a multi-step start per build (title → launch → sweeps), mouse sweeps on desktop, scripted touch drags on the phone, at 1280×800 and 390×844 (touch).
- Entries include build output: `openai-high`, `openai-med` and `astra` were judged and played from `dist/index.html` (Vite builds); the rest from `index.html` (or their game root).
- Sound was never heard; the play harness was headless. Audio scores come from the code.
- Rubric scores came from code reading plus that scripted play, not from a blind review.
- Costs are September 2026 list-price estimates and are heavily influenced by each provider's cache economics, so cross-provider comparisons are not apples to apples.
- `anthropic-high`'s 26 is code-only: the canvas is black in every screenshot (stale `nebula=buildNebula()` in `src/render.js:214`). All its scores credit structure that reads as complete and is play-unverified.
- `openai-low`'s blind sweep died during play on both viewports; read as a difficulty/autopilot signal (600-speed gates at 616 LOC), not breakage — menu, HUD and game-over flows all render.
- `qwen`'s mobile score reflects a confirmed viewport-scaling bug (squeezed middle band, black bars), not the desktop game.
- All interventions are listed here, none hidden: `qwen` cap-killed at $0.88 then resumed on a raised $3.50 cap (with 4 read-only wrong-preset calls killed before any write, then a correct resume); `anthropic-high`/`anthropic-med` budget-SIGTERM'd after passing verification; `opencode-med` cap-killed at $1.11; `astra` killed by the global $90 brake at $7.40; `openai-med`/`deepseek`/`qwen` overshot caps between ticks; the OpenAI-direct key ran dry mid-grid with OpenRouter fallbacks absorbing it; `nous-med` halted on Nous 404s as awaiting-input.
- Frame rate was not measured; several builds ship timestep claims (120–240Hz fixed/substepped) that were not independently verified.
- Every number on this page is in [`docs/grid/results.json`](grid/results.json); the v1 dataset is archived at [`docs/grid/results-2026-09-07.json`](grid/results-2026-09-07.json); the spec is [`docs/grid/SPEC.md`](grid/SPEC.md).

## How to reproduce

Put the spec in a fresh directory as `INSTRUCTIONS.md` and run the same prompt once per preset:

```
EAGENT_FINALECHAT=off eagent --preset NAME -p "Read INSTRUCTIONS.md and relentlessly build it until you're confident that it's fully and completely implemented to the highest possible standard you can manage."
```

`EAGENT_FINALECHAT=off` keeps the phone mirror out of the way for a grid of unattended runs. Fourteen directories, fourteen presets (`glm`, `openrouter-high`, `openrouter-med`, `anthropic-high`, `anthropic-med`, `openai-high`, `openai-med`, `openai-low`, `astra`, `deepseek`, `qwen`, `muse`, `opencode-med`, `nous-med`), run at the same time on `v0.8.0-1-g59ce5c7`, reproduces the set. The measuring run compiled `reports/*.metrics.json`, `*.judge.json` and `*.play.json` per preset into `docs/grid/results.json`, with per-preset spend and the $100 budget accounting in `reports/grid-costs.json`.


