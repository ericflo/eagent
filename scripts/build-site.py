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

# Recreate the artifact so removed source assets cannot linger in a deployment.
if destination.exists():
    shutil.rmtree(destination)
destination.mkdir(parents=True, exist_ok=True)
(destination / "index.html").write_text(page)
for name in ("404.html", "style.css", "script.js", "robots.txt", "sitemap.xml", ".nojekyll"):
    shutil.copy2(source / name, destination / name)
shutil.copytree(source / "assets", destination / "assets", dirs_exist_ok=True)
subprocess.run([sys.executable, str(root / "scripts" / "check-site.py")], check=True)
size = sum(path.stat().st_size for path in destination.rglob("*") if path.is_file())
print(f"Staged GitHub Pages site in {destination} ({size / 1024:.0f} KiB).")
