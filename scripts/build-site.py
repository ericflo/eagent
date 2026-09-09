#!/usr/bin/env python3
"""Render canonical configuration into a self-contained GitHub Pages site."""
from html import escape
import json
from pathlib import Path
import re
import shutil
import subprocess
import sys

root = Path(__file__).resolve().parent.parent
source = root / "site"
destination = root / "dist" / "site"
catalog = json.loads(subprocess.check_output(
    ["go", "run", "./scripts/site-data"], cwd=root, text=True,
))
# These are editorial choices of existing presets, never copies of their routes.
example_names = json.loads((source / "examples.json").read_text())["presets"]
by_name = {preset["name"]: preset for preset in catalog["presets"]}
for name in example_names:
    if name not in by_name:
        sys.exit(f"Website example no longer exists in canonical configuration: {name}")
example = by_name[example_names[0]]
ordered_presets = [by_name[name] for name in example_names] + [preset for preset in catalog["presets"] if preset["name"] not in example_names]


def effort_label(effort):
    return "no reasoning" if effort == "none" else effort + " effort" if effort else "provider default"


def route_html(route, number, label, color):
    actor = route["actor"]
    fallbacks = []
    fallback = route.get("fallback")
    while fallback:
        fallbacks.append(f'{fallback["label"]} · {fallback["provider"]} · {effort_label(fallback["effort"])}')
        fallback = fallback.get("fallback")
    fallback_text = "Fallback: " + " → ".join(fallbacks) if fallbacks else ""
    hidden = "" if fallbacks else " hidden"
    return f'''<div class="model-route">
      <span class="route-number ink-{color}">{number:02d}</span>
      <div class="route-info">
        <span>{label}</span>
        <strong id="model-{actor}">{escape(route["label"])}</strong>
        <p class="route-provider" id="provider-{actor}">{escape(route["provider"])}</p>
      </div>
      <span class="route-tag" id="effort-{actor}">{escape(effort_label(route["effort"]))}</span>
      <p class="route-fallback" id="fallback-{actor}"{hidden}>{escape(fallback_text)}</p>
    </div>'''


options = []
for preset in ordered_presets:
    selected = preset["name"] == example["name"]
    label = preset["name"] or "Built-in configuration"
    attribute = " selected" if selected else ""
    options.append(f'<option value="{escape(preset["name"])}"{attribute}>{escape(label)}</option>')

command = "eagent" + (" --preset " + example["name"] if example["name"] else "") + ' "Build something good"'
example_providers = list(dict.fromkeys(route["provider"] for route in example["routes"]))
example_keys = dict.fromkeys(route["keyEnv"] for route in example["routes"] if route.get("keyEnv"))
key_commands = [f'export {key}="your-key"' for key in example_keys] + [f'eagent doctor --preset {example["name"]} --live']

# Breakout-grid gallery data. docs/grid/results.json is the source of truth
# for every displayed number; one-liners and short test labels below are
# editorial summaries of its evidence/play_notes fields.
grid = json.loads((root / "docs" / "grid" / "results.json").read_text())
grid_presets = grid["presets"]
grid_order = grid.get("ranking_by_rubric_total") or sorted(grid_presets)
if set(grid_order) != set(grid_presets):
    sys.exit("Grid ranking does not match results.json presets")
grid_one_liners = {
    "opencode-med": "Break through, get on top, go overdrive \u2014 rim payouts and reward capsules.",
    "astra": "Breach the field; balls escaping above charge a burning overdrive meter.",
    "openai-med": "Crack a route topside; breach lanes and cascades multiply everything.",
    "openai-high": "Breach-protocol campaign: punch above, score topside, hit Maximum Overdrive.",
    "anthropic-med": "Break through to the attic, where chained breaks escalate under time-dilation.",
    "anthropic-high": "A neon Breakout about getting above the bricks. Ships broken \u2014 canvas stays black.",
    "muse": "Go exponential above the bricks; gated bricks and fire, ghost, heavy, multiball powers.",
    "glm": "The real game starts above the bricks \u2014 \u00d72.5 multiplier, lights and shake.",
    "openrouter-high": "Punch above the field across 6 levels; OVERDRIVE sends combos skyward.",
    "deepseek": "Rooftop mode ramps the multiplier and rains bonus balls.",
    "openai-low": "Tunnel the wall, trigger the orbit cascade, chain breaks to FRENZY.",
    "openrouter-med": "Graze- and shock-gated bricks with fire, split, giant, laser and sticky balls.",
    "qwen": "Five overdrive bands, key/lock and mover bricks, slam paddle, attract demo.",
    "nous-med": "Chaos-doubling multiplier topside; volt chains and vortex booms.",
}
grid_status_labels = {
    "done": "Done",
    "stopped-budget": "Stopped \u00b7 budget",
    "stopped-capped": "Stopped \u00b7 cap",
    "halted-provider": "Halted \u00b7 provider",
}
# Honest defect labels, summarized from results.json play_notes/page_errors.
grid_flags = {
    "anthropic-high": "Broken canvas \u2014 crashes on load (nebula is not defined)",
    "qwen": "Mobile viewport-scaling bug \u2014 tiny centered playfield",
}


def grid_tests_label(preset):
    tests = grid_presets[preset]["tests"]
    if not tests["exists"]:
        return "None"
    command = tests["command"]
    if "vitest" in command:
        return "vitest"
    if "node --test" in command:
        return "node --test"
    if "tsx --test" in command:
        return "tsx --test"
    if "physics.test" in command:
        return "physics.test.js"
    if "qa.js" in command:
        return "browser smoke (local server)"
    return "node runners"


def build_game_gallery():
    rows = []
    cards = []
    data = {}
    for preset in grid_order:
        info = grid_presets[preset]
        if preset not in grid_one_liners:
            sys.exit(f"No gallery one-liner for grid preset: {preset}")
        title = info["title"]
        one_liner = grid_one_liners[preset]
        models = f'{info["orchestrator"]} / {info["worker"]} / {info["narrator"]}'
        cost = f'${info["cost_usd"]:.2f}'
        minutes = int(round(info["duration_s"] / 60))
        status = grid_status_labels.get(info["status"], info["status"])
        flag = grid_flags.get(preset) or ("Broken canvas" if info["page_errors"] else "")
        tests_label = grid_tests_label(preset)
        status_cell = escape(status + (" \u2014 " + flag if flag else ""))
        rows.append(
            "<tr>"
            f"<td><code>{escape(preset)}</code></td>"
            f"<td>{escape(title)}</td>"
            f"<td>{escape(models)}</td>"
            f"<td>{cost}</td>"
            f"<td>{minutes}m</td>"
            f"<td>{info['loc']:,}</td>"
            f"<td>{info['rubric_total']}/27</td>"
            f"<td>{escape(tests_label)}</td>"
            f"<td>{status_cell}</td>"
            "</tr>"
        )
        badges = (
            f'<span class="game-badge">{cost}</span>'
            f'<span class="game-badge">{info["rubric_total"]}/27 rubric</span>'
        )
        if flag:
            badges += f'<span class="game-badge game-badge-warn">{escape(flag.split(" \u2014 ")[0])}</span>'
        cards.append(
            f'<article class="game-card" id="game-card-{escape(preset)}">'
            f'<img id="game-thumb-{escape(preset)}" src="assets/games/{escape(preset)}-desktop.png" '
            'width="1280" height="800" loading="lazy" '
            f'alt="Screenshot of {escape(title)} played on a desktop browser" />'
            '<div class="game-card-body">'
            f"<h3>{escape(title)}</h3>"
            f'<p class="game-preset"><code>{escape(preset)}</code> \u00b7 {escape(models)}</p>'
            f"<p>{escape(one_liner)}</p>"
            + (f'<p class="game-flag">{escape(flag)}</p>' if flag else "")
            + f'<p class="game-badges">{badges}</p>'
            '<div class="game-card-actions">'
            f'<button type="button" class="game-play" data-play="{escape(preset)}">Play</button>'
            f'<a class="game-open" href="games/{escape(preset)}/">Open full page</a>'
            "</div></div></article>"
        )
        data[preset] = {
            "title": title,
            "oneLiner": one_liner,
            "models": models,
            "cost": cost,
            "rubric": f'{info["rubric_total"]}/27',
            "status": status,
            "flag": flag,
            "href": f"games/{preset}/",
            "desktop": f"assets/games/{preset}-desktop.png",
            "mobile": f"assets/games/{preset}-mobile.png",
        }
    table = (
        '<div class="game-table-wrap" tabindex="0" role="region" aria-label="Breakout grid comparison table, scrollable"><table class="game-table" id="game-table">'
        "<caption>Fourteen presets, one Breakout spec \u2014 cost, time, size, rubric score, tests, and run status from results.json.</caption>"
        "<thead><tr><th scope=\"col\">Preset</th><th scope=\"col\">Game</th><th scope=\"col\">Models</th>"
        "<th scope=\"col\">Cost</th><th scope=\"col\">Time</th><th scope=\"col\">LOC</th>"
        "<th scope=\"col\">Rubric</th><th scope=\"col\">Tests</th><th scope=\"col\">Status</th></tr></thead>"
        "<tbody>" + "".join(rows) + "</tbody></table></div>"
    )
    gallery = (
        table
        + '<div class="game-grid" id="game-grid">'
        + "".join(cards)
        + "</div>"
        # Keep JSON inert even if a future title contains HTML.
        + '<script type="application/json" id="game-data">'
        + json.dumps(data, separators=(",", ":"), ensure_ascii=True).replace("<", "\\u003c")
        + "</script>"
    )
    return gallery
featured_providers = list(dict.fromkeys(route["provider"] for name in example_names for route in by_name[name]["routes"]))
ordered_providers = featured_providers + [label for label in catalog["providers"] if label not in featured_providers]
replacements = {
    "<!-- site:preset-options -->": "\n".join(options),
    "<!-- site:providers -->": "".join(f"<span>{escape(label)}</span>" for label in ordered_providers),
    "<!-- site:example-routes -->": "\n".join(
        route_html(route, i, label, color)
        for i, (route, label, color) in enumerate(zip(example["routes"], ["ORCHESTRATOR", "TASK WORKER", "NARRATOR"], ["orange", "sage", "yellow"]), 1)
    ),
    "{{EXAMPLE_DESCRIPTION}}": escape(example["description"]),
    "{{EXAMPLE_COMMAND}}": escape(command),
    "{{EXAMPLE_PROVIDERS}}": escape(
        ("Example provider: " if len(example_providers) == 1 else "Example providers: ") + ", ".join(example_providers) + "."
    ),
    "{{EXAMPLE_KEY_COMMANDS}}": escape("\n".join(key_commands)),
    "{{EXAMPLE_START_COMMAND}}": escape('cd your-project\neagent --preset ' + example["name"] + ' "Read this repo and fix the failing tests"'),
    "{{EXAMPLE_SERVE_COMMAND}}": escape("eagent serve --preset " + example["name"]),
    # Keep JSON inert even if a future catalog label contains HTML.
    "<!-- site:preset-data -->": '<script type="application/json" id="preset-data">' + json.dumps(catalog, separators=(",", ":"), ensure_ascii=True).replace("<", "\\u003c") + "</script>",
    "<!-- site:game-gallery -->": build_game_gallery(),
}
for route in example["routes"]:
    replacements["{{EXAMPLE_" + route["actor"].upper() + "}}"] = escape(route["label"])
page = (source / "index.template.html").read_text()
for marker, value in replacements.items():
    if page.count(marker) != 1:
        sys.exit(f"Expected exactly one template marker: {marker}")
    page = page.replace(marker, value)
if re.search(r"<!-- site:|\{\{[A-Z_]+\}\}", page):
    sys.exit("Unresolved website template marker")

# Leading-slash asset references break under the /eagent/ project path and in
# games/<preset>/ subpaths. Rewrite them to relative paths in staged copies.
SUBPATH_REWRITES = [
    (re.compile(r'''(href|src)=(["'])/(?!/)'''), r"\1=\2./"),
    (re.compile(r"""url\(\s*(["']?)/(?!/)"""), r"url(\1./"),
    (re.compile(r"""@import\s+(["'])/(?!/)"""), r"@import \1./"),
    (re.compile(r"""from\s+(["'])/(?!/)"""), r"from \1./"),
    (re.compile(r"""import\(\s*(["'])/(?!/)"""), r"import(\1./"),
]
LEFTOVER_ABSOLUTE = re.compile(
    r"""(?:(?:href|src)=["']/(?!/)|url\(\s*["']?/(?!/)|@import\s+["']/(?!/)|from\s+["']/(?!/)|import\(\s*["']/(?!/))"""
)
STAGED_TEXT_SUFFIXES = {".html", ".css", ".js", ".mjs", ".svg", ".json", ".webmanifest", ".xml", ".txt"}


def stage_games():
    grid_root = root / "docs" / "grid"
    builds = grid_root / "builds"
    known = set(grid_presets)
    actual = {path.name for path in builds.iterdir() if path.is_dir()}
    if actual != known:
        sys.exit(f"Grid builds out of sync with results.json: extra={sorted(actual - known)} missing={sorted(known - actual)}")
    shots = destination / "assets" / "games"
    shots.mkdir(parents=True, exist_ok=True)
    for preset in grid_order:
        entry = grid_presets[preset]["entry"]
        source_dir = builds / preset
        # Vite builds serve from dist/: strip the prefix so the game loads
        # from the clean games/<preset>/ URL.
        if (source_dir / "dist" / "index.html").is_file():
            source_dir = source_dir / "dist"
        if not (source_dir / entry.split("/")[-1]).is_file() and not (builds / preset / entry).is_file():
            sys.exit(f"Grid preset {preset} is missing its entry: {entry}")
        target = destination / "games" / preset
        shutil.copytree(source_dir, target, dirs_exist_ok=True)
        rewritten = 0
        for path in sorted(target.rglob("*")):
            if not path.is_file() or path.suffix not in STAGED_TEXT_SUFFIXES:
                continue
            try:
                text = path.read_text()
            except UnicodeDecodeError:
                continue
            updated = text
            for pattern, replacement in SUBPATH_REWRITES:
                updated, count = pattern.subn(replacement, updated)
                rewritten += count
            if updated != text:
                path.write_text(updated)
        leftovers = []
        for path in sorted(target.rglob("*")):
            if not path.is_file() or path.suffix not in STAGED_TEXT_SUFFIXES:
                continue
            try:
                text = path.read_text()
            except UnicodeDecodeError:
                continue
            if LEFTOVER_ABSOLUTE.search(text):
                leftovers.append(str(path.relative_to(target)))
        if leftovers:
            sys.exit(f"Staged game {preset} keeps absolute asset refs: {leftovers}")
        if not (target / "index.html").is_file():
            sys.exit(f"Staged game {preset} has no index.html")
        for shot in ("desktop", "mobile"):
            origin = grid_root / f"{preset}-{shot}.png"
            if not origin.is_file():
                sys.exit(f"Grid preset {preset} is missing its {shot} screenshot")
            shutil.copy2(origin, shots / f"{preset}-{shot}.png")
    print(f"Staged {len(grid_order)} grid games.")


# Recreate the artifact so removed source assets cannot linger in a deployment.
if destination.exists():
    shutil.rmtree(destination)
destination.mkdir(parents=True, exist_ok=True)
(destination / "index.html").write_text(page)
for name in ("404.html", "style.css", "script.js", "robots.txt", "sitemap.xml", ".nojekyll"):
    shutil.copy2(source / name, destination / name)
shutil.copytree(source / "assets", destination / "assets", dirs_exist_ok=True)
stage_games()
subprocess.run([sys.executable, str(root / "scripts" / "check-site.py")], check=True)
size = sum(path.stat().st_size for path in destination.rglob("*") if path.is_file())
print(f"Staged GitHub Pages site in {destination} ({size / 1024:.0f} KiB).")
