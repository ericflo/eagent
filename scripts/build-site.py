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


def eagent_route(preset):
    """The route of eagent's own model loop. A preset may also carry a
    dedicated summarizer route; the site shows only the loop's model."""
    for route in preset["routes"]:
        if route["actor"] == "eagent":
            return route
    sys.exit(f"Preset has no eagent route: {preset['name']!r}")


def route_html(route):
    fallbacks = []
    fallback = route.get("fallback")
    while fallback:
        fallbacks.append(f'{fallback["label"]} · {fallback["provider"]} · {effort_label(fallback["effort"])}')
        fallback = fallback.get("fallback")
    fallback_text = "Fallback: " + " → ".join(fallbacks) if fallbacks else ""
    hidden = "" if fallbacks else " hidden"
    return f'''<div class="model-route">
      <span class="route-number ink-orange">01</span>
      <div class="route-info">
        <span>EAGENT&#8217;S MODEL</span>
        <strong id="model-eagent">{escape(route["label"])}</strong>
        <p class="route-provider" id="provider-eagent">{escape(route["provider"])}</p>
      </div>
      <span class="route-tag" id="effort-eagent">{escape(effort_label(route["effort"]))}</span>
      <p class="route-fallback" id="fallback-eagent"{hidden}>{escape(fallback_text)}</p>
    </div>'''


def preset_flag(preset):
    """A preset that is the built-in default needs no flag."""
    return "" if not preset["name"] or preset["name"] == catalog["default"] else " --preset " + preset["name"]


options = []
for preset in ordered_presets:
    selected = preset["name"] == example["name"]
    label = preset["name"] or "Built-in configuration"
    attribute = " selected" if selected else ""
    options.append(f'<option value="{escape(preset["name"])}"{attribute}>{escape(label)}</option>')

# The prompt is the README's own example.
EXAMPLE_PROMPT = "add a Makefile with build and test targets"
example_route = eagent_route(example)
command = "eagent" + preset_flag(example) + f' "{EXAMPLE_PROMPT}"'
example_providers = list(dict.fromkeys(route["provider"] for route in example["routes"]))
example_keys = dict.fromkeys(route["keyEnv"] for route in example["routes"] if route.get("keyEnv"))
key_commands = [f'export {key}="your-key"' for key in example_keys] + ["eagent doctor" + preset_flag(example) + " --live"]
default_route = catalog["defaults"][0]
featured_providers = list(dict.fromkeys(route["provider"] for name in example_names for route in by_name[name]["routes"]))
ordered_providers = featured_providers + [label for label in catalog["providers"] if label not in featured_providers]
replacements = {
    "<!-- site:preset-options -->": "\n".join(options),
    "<!-- site:providers -->": "".join(f"<span>{escape(label)}</span>" for label in ordered_providers),
    "<!-- site:example-route -->": route_html(example_route),
    "{{EXAMPLE_EAGENT}}": escape(example_route["label"]),
    "{{DEFAULT_MODEL}}": escape(default_route["label"] + " on " + default_route["provider"]),
    "{{EXAMPLE_DESCRIPTION}}": escape(example["description"]),
    "{{EXAMPLE_COMMAND}}": escape(command),
    "{{EXAMPLE_PROVIDERS}}": escape(
        ("Example provider: " if len(example_providers) == 1 else "Example providers: ") + ", ".join(example_providers) + "."
    ),
    "{{EXAMPLE_KEY_COMMANDS}}": escape("\n".join(key_commands)),
    "{{EXAMPLE_START_COMMAND}}": escape("cd your-project\neagent" + preset_flag(example) + f' "{EXAMPLE_PROMPT}"'),
    # Keep JSON inert even if a future catalog label contains HTML.
    "<!-- site:preset-data -->": '<script type="application/json" id="preset-data">' + json.dumps(catalog, separators=(",", ":"), ensure_ascii=True).replace("<", "\\u003c") + "</script>",
}
page = (source / "index.template.html").read_text()
for marker, value in replacements.items():
    if page.count(marker) != 1:
        sys.exit(f"Expected exactly one template marker: {marker}")
    page = page.replace(marker, value)
if re.search(r"<!-- site:|\{\{[A-Z_]+\}\}", page):
    sys.exit("Unresolved website template marker")

# The Breakout grid and its variance annex document eagent's earlier design,
# in which eagent did the work itself. The main page no longer shows them; the
# games stay staged so their existing URLs keep working, and the variance page
# carries a banner that says which design it documents.
grid = json.loads((root / "docs" / "grid" / "results.json").read_text())
grid_presets = grid["presets"]
grid_order = grid.get("ranking_by_rubric_total") or sorted(grid_presets)
if set(grid_order) != set(grid_presets):
    sys.exit("Grid ranking does not match results.json presets")

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


# Breakout variance annex. Separate page under variance/; the index gallery
# above is untouched. docs/grid/variance.json is the source of truth for every
# displayed number; prose findings below summarize reports/VARIANCE.md in
# eagent-grid (vendored at docs/grid/VARIANCE.md).
VARIANCE_LANE_ORDER = ["muse", "fireworks-med", "openrouter-med", "deepseek-fw"]


def variance_run_number(run):
    return int(run.rsplit("-var", 1)[1])


def build_variance_page():
    grid_root = root / "docs" / "grid"
    variance = json.loads((grid_root / "variance.json").read_text())
    runs = variance["runs"]
    lanes = variance["lanes"]
    excluded = variance["excluded"]
    for lane in VARIANCE_LANE_ORDER:
        if lane not in lanes:
            sys.exit(f"Variance lane missing from variance.json: {lane}")
    if set(runs) != {run for lane in VARIANCE_LANE_ORDER for run in lanes[lane]["runs"]}:
        sys.exit("Variance runs do not match lane listings in variance.json")

    summary_rows = []
    for lane in VARIANCE_LANE_ORDER:
        info = lanes[lane]
        summary_rows.append(
            "<tr>"
            f"<td><code>{escape(lane)}</code></td>"
            f"<td>{escape(info['models'])}</td>"
            f"<td>{info['done']}/{info['total']}</td>"
            f"<td>${info['cost_mean']:.4f} ± ${info['cost_sd']:.4f}</td>"
            f"<td>${info['cost_min']:.2f}–${info['cost_max']:.2f}</td>"
            f"<td>{info['duration_mean_min']:.0f}m avg</td>"
            "</tr>"
        )
    summary = (
        '<h2>Per-lane cost, time, and completion</h2>'
        '<div class="game-table-wrap" tabindex="0" role="region" aria-label="Variance per-lane comparison table, scrollable">'
        '<table class="game-table" id="variance-table">'
        "<caption>Ten repeats per lane — catalog list-price cost and wall-clock time for finished (status=done) runs; means and spreads are done-only.</caption>"
        "<thead><tr><th scope=\"col\">Lane</th><th scope=\"col\">Preset shape</th>"
        "<th scope=\"col\">Finished</th><th scope=\"col\">Cost mean ± sd</th>"
        "<th scope=\"col\">Cost range</th><th scope=\"col\">Build time</th></tr></thead>"
        "<tbody>" + "".join(summary_rows) + "</tbody></table></div>"
    )

    excluded_rows = []
    for run in sorted(excluded):
        info = excluded[run]
        excluded_rows.append(
            "<tr>"
            f"<td><code>{escape(run)}</code></td>"
            f"<td><code>{escape(info['lane'])}</code></td>"
            f"<td>{escape(info['status'])}</td>"
            f"<td>${info['cost_usd']:.2f}</td>"
            f"<td>{info['duration_min']:.0f}m</td>"
            "</tr>"
        )
    excluded_html = (
        "<h2>Excluded: the 7 timeout runs</h2>"
        "<p>"
        "Seven runs never declared themselves finished and are not staged "
        "below — all seven were supervisor-stopped at the 90-minute run cap "
        "while QA workers iterated open-endedly (screenshot staring, soak "
        "loops, fix-and-check cycles), with the finished game sitting on "
        "disk and the session parked resumable. Nothing corrupted; every "
        "stop was boring and resumable. Muse had zero timeouts."
        "</p>"
        '<div class="game-table-wrap" tabindex="0" role="region" aria-label="Excluded timeout runs, scrollable">'
        '<table class="game-table" id="variance-excluded">'
        "<caption>Timeout runs skipped by the gallery, with spend and wall-clock time at the stop.</caption>"
        "<thead><tr><th scope=\"col\">Run</th><th scope=\"col\">Lane</th>"
        "<th scope=\"col\">Status</th><th scope=\"col\">Cost</th><th scope=\"col\">Time</th></tr></thead>"
        "<tbody>" + "".join(excluded_rows) + "</tbody></table></div>"
    )

    row_cards = []
    data = {}
    for lane in VARIANCE_LANE_ORDER:
        info = lanes[lane]
        ordered = sorted(info["runs"], key=variance_run_number)
        row_cards.append(
            f"<h2>{escape(lane)} — {info['done']}/{info['total']} finished</h2>"
            f"<p>{escape(info['models'])}</p>"
        )
        cards = []
        for run in ordered:
            game = runs[run]
            number = variance_run_number(run)
            title = game["title"]
            cost = f"${game['cost_usd']:.2f}"
            minutes = int(round(game["duration_min"]))
            badges = f'<span class="game-badge">{cost}</span><span class="game-badge">{minutes} min</span>'
            cards.append(
                f'<article class="game-card" id="game-card-{escape(run)}">'
                f'<img id="game-thumb-{escape(run)}" src="../assets/games/{escape(run)}-desktop.png" '
                'width="1280" height="800" loading="lazy" '
                f'alt="Screenshot of {escape(title)} played on a desktop browser" />'
                '<div class="game-card-body">'
                f"<h3>{escape(title)}</h3>"
                f"<p class=\"game-preset\"><code>{escape(run)}</code> · {escape(game['models'])}</p>"
                f"<p>Repeat {number} of 10 · finished in {minutes} min.</p>"
                f'<p class="game-badges">{badges}</p>'
                '<div class="game-card-actions">'
                f'<button type="button" class="game-play" data-play="{escape(run)}">Play</button>'
                f'<a class="game-open" href="../games/{escape(run)}/">Open full page</a>'
                "</div></div></article>"
            )
            data[run] = {
                "title": title,
                "models": game["models"],
                "cost": cost,
                "duration": f"{minutes} min",
                "status": "Done",
                "href": f"../games/{run}/",
                "desktop": f"../assets/games/{run}-desktop.png",
                "mobile": f"../assets/games/{run}-mobile.png",
            }
        row_cards.append('<div class="game-grid">' + "".join(cards) + "</div>")
    gallery = "".join(row_cards) + '<script type="application/json" id="game-data">' + json.dumps(data, separators=(",", ":"), ensure_ascii=True).replace("<", "\\u003c") + "</script>"
    return summary, excluded_html, gallery


def stage_variance():
    grid_root = root / "docs" / "grid"
    builds = grid_root / "builds-variance"
    variance = json.loads((grid_root / "variance.json").read_text())
    runs = variance["runs"]
    known = set(runs)
    actual = {path.name for path in builds.iterdir() if path.is_dir()}
    if actual != known:
        sys.exit(f"Variance builds out of sync with variance.json: extra={sorted(actual - known)} missing={sorted(known - actual)}")
    shots = destination / "assets" / "games"
    shots.mkdir(parents=True, exist_ok=True)
    for run in sorted(runs):
        manifest = set(runs[run]["manifest"])
        source_dir = builds / run
        target = destination / "games" / run
        shutil.copytree(source_dir, target, dirs_exist_ok=True)
        staged = {str(path.relative_to(target)) for path in target.rglob("*") if path.is_file()}
        if staged != manifest:
            sys.exit(f"Variance run {run} staged files differ from manifest: extra={sorted(staged - manifest)} missing={sorted(manifest - staged)}")
        for path in sorted(target.rglob("*")):
            if not path.is_file() or path.suffix not in STAGED_TEXT_SUFFIXES:
                continue
            try:
                text = path.read_text()
            except UnicodeDecodeError:
                continue
            updated = text
            for pattern, replacement in SUBPATH_REWRITES:
                updated, _ = pattern.subn(replacement, updated)
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
            sys.exit(f"Staged variance game {run} keeps absolute asset refs: {leftovers}")
        if not (target / "index.html").is_file():
            sys.exit(f"Staged variance game {run} has no index.html")
        for shot in ("desktop", "mobile"):
            origin = grid_root / f"{run}-{shot}.png"
            if not origin.is_file():
                sys.exit(f"Variance run {run} is missing its {shot} screenshot")
            shutil.copy2(origin, shots / f"{run}-{shot}.png")
    summary, excluded_html, gallery = build_variance_page()
    template = (source / "variance.template.html").read_text()
    replacements = {
        "<!-- site:variance-summary -->": summary,
        "<!-- site:variance-excluded -->": excluded_html,
        "<!-- site:variance-gallery -->": gallery,
    }
    for marker, value in replacements.items():
        if template.count(marker) != 1:
            sys.exit(f"Expected exactly one variance template marker: {marker}")
        template = template.replace(marker, value)
    if re.search(r"<!-- site:", template):
        sys.exit("Unresolved variance template marker")
    variance_dir = destination / "variance"
    variance_dir.mkdir(parents=True, exist_ok=True)
    (variance_dir / "index.html").write_text(template)
    print(f"Staged {len(runs)} variance games.")


# Recreate the artifact so removed source assets cannot linger in a deployment.
if destination.exists():
    shutil.rmtree(destination)
destination.mkdir(parents=True, exist_ok=True)
(destination / "index.html").write_text(page)
for name in ("404.html", "style.css", "script.js", "robots.txt", "sitemap.xml", ".nojekyll"):
    shutil.copy2(source / name, destination / name)
shutil.copytree(source / "assets", destination / "assets", dirs_exist_ok=True)
stage_games()
stage_variance()
subprocess.run([sys.executable, str(root / "scripts" / "check-site.py")], check=True)
size = sum(path.stat().st_size for path in destination.rglob("*") if path.is_file())
print(f"Staged GitHub Pages site in {destination} ({size / 1024:.0f} KiB).")
