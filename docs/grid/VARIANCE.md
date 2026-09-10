# Breakout variance runs — what 10 repeats of each cheap build actually cost

Grid follow-up, 2026-09-09 → 09-10. Same spec (`eagent-grid/INSTRUCTIONS.md`), same
one-line prompt, fresh dirs, `EAGENT_FINALECHAT=off`, binary `eagent v0.9.0-dirty`
(note: the original grid ran `v0.8.0-1-g59ce5c7-dirty`; the repo moved on mid-project).
Caps $2 (muse) / $3 everything else — never breached (max single run $2.30).
Total variance spend ≈ **$23.80**. No rubric re-judging: this is cost / duration /
completion variance, not score variance.

## How the slate changed

The 3 cheapest grid presets were muse ($0.04), nous-med ($0.13), deepseek ($0.53).
Nous died on end-of-run provider 404s 4/4 times (games built, sessions parked) and
Together AI ran out of credits (deepseek 0/8, instant 402s, $0 each) — both logged
under `runs/{nous-med,deepseek}-var0*` + `logs/`. Per user direction both were swapped
for the **same models on working pipes**: `fireworks-med` (exact nous-med shape on
Fireworks) and `deepseek-fw` (custom run-local bundle: DeepSeek V4 Flash ×3 on
Fireworks, `--config deepseek-fw`, definition in `bin/deepseek-fw.bundle.json`),
plus `openrouter-med` (same shape on OpenRouter) as the healthy-pipe control.

## Results (cost = catalog list-price estimates from per-call JSONL usage)

### muse — 10/10 done · $0.0424 ± $0.0135 · ~16 min avg
| run | $ | min | | run | $ | min |
|---|---|---|---|---|---|---|
| var01 | 0.0374 | 13 | | var06 | 0.0444 | 14 |
| var02 | 0.0339 | 15 | | var07 | 0.0331 | 11 |
| var03 | 0.0306 | 10 | | var08 | 0.0341 | 15 |
| var04 | 0.0461 | 22 | | var09 | 0.0441 | 19 |
| var05 | 0.0427 | 14 | | var10 | 0.0776 | 27 |
Range $0.031–$0.078. Every run shipped a playable, self-verified game
(node --check + headless browser, zero console errors). Full data:
`reports/muse-variance.json`.

### fireworks-med — 9/10 done · $0.78 ± $0.29 (done-only) · ~35 min avg
| run | $ | min | status | run | $ | min | status |
|---|---|---|---|---|---|---|---|
| var01 | 0.82 | 35 | done | var06 | 0.62 | 35 | done |
| var02 | 0.88 | 48 | done | var07 | 0.77 | 50 | done |
| var03 | 0.83 | 28 | done | var08 | 0.34 | 15 | done |
| var04 | 0.50 | 91 | timeout | var09 | 1.43 | 33 | done |
| var05 | 0.91 | 48 | done | var10 | 0.45 | 20 | done |
Range $0.34–$1.43. `reports/fireworks-med-variance.json`.

### openrouter-med — 8/10 done · $0.98 ± $0.51 · ~55 min avg
| run | $ | min | status | run | $ | min | status |
|---|---|---|---|---|---|---|---|
| var01 | 0.58 | 20 | done | var06 | 0.56 | 23 | done |
| var02 | 1.11 | 91 | timeout | var07 | 0.60 | 56 | done |
| var03 | 0.96 | 40 | done | var08 | 1.27 | 91 | timeout |
| var04 | 1.14 | 66 | done | var09 | 0.69 | 38 | done |
| var05 | 2.30 | 83 | done | var10 | 0.57 | 40 | done |
Widest band of any lane: $0.56–$2.30 (4×). `reports/openrouter-med-variance.json`
+ `reports/openrouter-med-variance-summary.txt`.

### deepseek-fw — 6/10 done · $0.66 ± $0.15 · ~79 min avg
| run | $ | min | status | run | $ | min | status |
|---|---|---|---|---|---|---|---|
| var01 | 0.86 | 90 | done | var06 | 0.69 | 55 | done |
| var02 | 0.57 | 91 | timeout | var07 | 1.01 | 91 | timeout |
| var03 | 0.60 | 70 | done | var08 | 0.52 | 66 | done |
| var04 | 0.68 | 91 | timeout | var09 | 0.55 | 91 | timeout |
| var05 | 0.54 | 63 | done | var10 | 0.64 | 86 | done |
Slowest lane, steadiest cost. `reports/deepseek-fw-variance.json`
(`{bundle, runs}`) + `reports/deepseek-fw-variance-summary.txt`.

## What actually varies (and what doesn't)

1. **The grid headline survives with error bars.** The 4-cent build is also the most
   consistent: 10/10, 5-cent band, fastest. Cost predicts nothing; convergence does.
2. **Same models, different pipes.** Identical GLM shape: $0.78 ± $0.29 on Fireworks
   vs $0.98 ± $0.51 on OpenRouter — the provider adds ~25% and roughly doubles the
   spread.
3. **The variance driver is verification depth, not building.** All 7 timeouts (0 on
   muse) were QA workers iterating open-endedly — screenshot staring, soak loops,
   fix-and-check cycles — with the finished game sitting on disk. Stalled runs were
   supervisor-stopped at 90 min, sessions parked resumable (`eagent -c`), artifacts
   intact. Muse's loop converges (check → screenshot → fix N things → ship).
4. **Failure taxonomy.** Fail-fast billing death ($0, Together 402s); build-then-die
   provider flakiness (Nous 404s, 4/4, games complete on disk); convergence stalls
   (90-min timeouts). Nothing ever corrupted — every stop was boring and resumable.
5. **Caveats.** Binary drift (v0.9.0 vs grid's v0.8.0); no score variance (no judging);
   two lanes needed overnight worker handoffs (logged, no data lost); costs are
   list-price estimates, not invoices.

## Playing them

Each `runs/<lane>-varNN/` dir is a self-contained game — open its `index.html`
directly or `python3 -m http.server` in the dir. Verified screenshots (all zero
console errors): `shots/muse-var01-{title,desktop,mobile}.png`,
`shots/fireworks-med-var01-*.png`, `shots/openrouter-med-var01-*.png`,
`shots/deepseek-fw-var01-*.png` (desktop capture caught a dark menu fade; the
mobile shot shows real gameplay: score 6,125, combo ×7). Nothing from this sweep is
on the public site yet — the live gallery still shows the original 16.
