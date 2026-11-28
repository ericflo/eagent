#!/usr/bin/env python3
"""Check the distributable site's assets, anchors, and metadata without dependencies."""
from html.parser import HTMLParser
from pathlib import Path
import json
import re
import sys
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parent.parent
SITE = ROOT / "dist" / "site"
if not (SITE / "index.html").is_file():
    sys.exit("Build the website first: python3 scripts/build-site.py")
ERRORS = []


class Page(HTMLParser):
    def __init__(self, path, base=None):
        super().__init__(convert_charrefs=True)
        self.path = path
        # Directory that relative references resolve against (subpath pages
        # like variance/index.html link with ../).
        self.base = base or path.parent
        self.ids = set()
        self.references = []
        self.metadata = {}
        self.headings = 0
        self.feed(path.read_text())

    def handle_starttag(self, tag, attributes):
        attrs = dict(attributes)
        if "id" in attrs:
            if attrs["id"] in self.ids:
                ERRORS.append(f"{self.path.name}: duplicate id {attrs['id']}")
            self.ids.add(attrs["id"])
        if tag == "h1":
            self.headings += 1
        if tag == "meta":
            self.metadata[attrs.get("name", attrs.get("property", ""))] = attrs.get("content")
        if tag == "img" and "alt" not in attrs:
            ERRORS.append(f"{self.path.name}: an image needs alternative text")
        for name in ("href", "src"):
            if attrs.get(name):
                self.references.append(attrs[name])
        for name in ("aria-controls", "aria-labelledby"):
            for ident in attrs.get(name, "").split():
                self.references.append("#" + ident)


pages = {
    "index.html": Page(SITE / "index.html"),
    "404.html": Page(SITE / "404.html"),
    "variance/index.html": Page(SITE / "variance" / "index.html"),
}
for name, page in pages.items():
    if page.headings != 1:
        ERRORS.append(f"{name}: expected one h1, found {page.headings}")
    for reference in page.references:
        url = urlsplit(reference)
        if url.scheme or url.netloc:
            if url.scheme not in ("https", "http"):
                ERRORS.append(f"{name}: unexpected link scheme: {reference}")
            continue
        path = unquote(url.path)
        if path.startswith("/eagent/"):
            path = path.removeprefix("/eagent/")
            target = SITE / (path or name)
        elif path.startswith("/"):
            ERRORS.append(f"{name}: absolute local path: {reference}")
            continue
        else:
            target = page.base / path if path else page.path
        if url.path == "/eagent/":
            target = SITE / "index.html"
        if target.is_dir():
            target = target / "index.html"
        if not target.resolve().is_relative_to(SITE.resolve()) or not target.is_file():
            ERRORS.append(f"{name}: missing local target: {reference}")
        if url.fragment and not path and url.fragment not in page.ids:
            ERRORS.append(f"{name}: missing anchor: {reference}")

for stylesheet in [SITE / "style.css"]:
    for reference in re.findall(r'url\(["\x27]?([^)"\x27]+)', stylesheet.read_text()):
        if not (SITE / reference).is_file():
            ERRORS.append(f"{stylesheet.name}: missing asset: {reference}")
for reference in re.findall(r'["\x27](assets/[^"\x27]+)["\x27]', (SITE / "script.js").read_text()):
    if not (SITE / reference).is_file():
        ERRORS.append(f"script.js: missing asset: {reference}")

index = pages["index.html"]
for key in ("description", "viewport", "og:title", "og:description", "og:image", "twitter:card"):
    if not index.metadata.get(key):
        ERRORS.append(f"index.html: missing {key}")
for name in ("social-card.png", "favicon.svg"):
    if not (SITE / "assets" / name).is_file():
        ERRORS.append(f"Missing {name}")
for name in ("robots.txt", "sitemap.xml", ".nojekyll"):
    if not (SITE / name).is_file():
        ERRORS.append(f"Missing {name}")

page_text = (SITE / "index.html").read_text()

# The main page tells the current story: eagent runs an agent and stands
# between it and you. The three-actor design survives only in the FAQ entry
# that says it is gone and in the footer's "Earlier design" links.
for node in ("user", "eagent", "agent"):
    if f'data-node="{node}"' not in page_text:
        ERRORS.append(f"index.html: walkthrough is missing the {node} node")
for edge in ("words", "relay", "tell"):
    if f'data-edge="{edge}"' not in page_text:
        ERRORS.append(f"index.html: walkthrough is missing the {edge} edge")
for step in ("send", "watch", "progress", "dialog", "end", "ask"):
    if f'id="step-{step}"' not in page_text:
        ERRORS.append(f"index.html: walkthrough is missing the {step} step")
current = re.sub(r'<section class="faq.*?</section>', "", page_text, flags=re.DOTALL)
current = re.sub(r"<footer.*?</footer>", "", current, flags=re.DOTALL)
for retired in (
    "orchestrator", "Orchestrator", "narrator", "Narrator", "task worker",
    "Task worker", "data-node=\"engine\"", "data-node=\"touch\"", "game-card",
    "data-play=", "web-tasks.png", "web-chat.png", "neon-brickles", "93%",
    "Breakout", "#in-practice",
):
    if retired in current:
        ERRORS.append(f"index.html: retired three-actor content outside the FAQ and footer: {retired}")
if "variance/" in current:
    ERRORS.append("index.html: the variance page may be linked only from the footer")
footer = re.search(r"<footer.*?</footer>", page_text, re.DOTALL)
if not footer or not re.search(r"EARLIER DESIGN</span.*?href=\"variance/\"", footer.group(0), re.DOTALL):
    ERRORS.append("index.html: the variance page belongs under the footer's EARLIER DESIGN heading")
if "Does eagent still do the work itself?" not in page_text:
    ERRORS.append("index.html: missing the FAQ entry on what eagent no longer does")

# The Breakout grid games stay staged so their old URLs keep working.
grid = json.loads((ROOT / "docs" / "grid" / "results.json").read_text())
order = grid.get("ranking_by_rubric_total") or sorted(grid["presets"])
if len(order) != 16:
    ERRORS.append(f"grid: expected 16 grid presets, found {len(order)}")
for preset in order:
    game_dir = SITE / "games" / preset
    if not (game_dir / "index.html").is_file():
        ERRORS.append(f"Missing staged game entry: games/{preset}/index.html")
    for shot in ("desktop", "mobile"):
        if not (SITE / "assets" / "games" / f"{preset}-{shot}.png").is_file():
            ERRORS.append(f"Missing grid thumbnail: assets/games/{preset}-{shot}.png")
    if (game_dir / "index.html").is_file():
        staged = " ".join(
            path.read_text(errors="replace")
            for path in game_dir.rglob("*")
            if path.is_file()
            and path.suffix in (".html", ".css", ".js", ".mjs", ".svg", ".json", ".webmanifest", ".xml")
        )
        for pattern in (
            'href="/',
            "href='/",
            'src="/',
            "src='/",
            "url(/",
            "url('/",
            'url("/',
            "@import '/",
            '@import "/',
            "from '/",
            'from "/',
            "import('/",
            'import("/',
        ):
            if pattern in staged:
                ERRORS.append(f"games/{preset}/: absolute asset ref {pattern} survived staging")
                break
# Breakout variance annex: staged games, thumbnails, and variance.json parity.
variance = json.loads((ROOT / "docs" / "grid" / "variance.json").read_text())
variance_order = [run for lane in ("muse", "fireworks-med", "openrouter-med", "deepseek-fw") for run in variance["lanes"][lane]["runs"]]
if len(variance_order) != 33:
    ERRORS.append(f"variance: expected 33 staged runs, found {len(variance_order)}")
if set(variance_order) != set(variance["runs"]):
    ERRORS.append("variance: lane listings differ from variance.json runs")
if len(variance["excluded"]) != 7:
    ERRORS.append(f"variance: expected 7 excluded timeout runs, found {len(variance['excluded'])}")
variance_text = (SITE / "variance" / "index.html").read_text()
variance_data_text = None
for match in re.finditer(
    r'<script type="application/json" id="game-data">(.*?)</script>', variance_text, re.DOTALL
):
    variance_data_text = match.group(1)
if variance_data_text is None:
    ERRORS.append("variance: missing game-data gallery payload")
    variance_data = {}
else:
    variance_data = json.loads(variance_data_text.replace("\\u003c", "<"))
if set(variance_data) != set(variance["runs"]):
    ERRORS.append("variance: game-data runs differ from variance.json")
for run in variance_order:
    info = variance["runs"][run]
    entry = variance_data.get(run, {})
    for key, expected in (
        ("title", info["title"]),
        ("href", f"../games/{run}/"),
        ("desktop", f"../assets/games/{run}-desktop.png"),
        ("mobile", f"../assets/games/{run}-mobile.png"),
    ):
        if entry.get(key) != expected:
            ERRORS.append(f"variance: game-data {run}.{key} != variance.json")
    if entry.get("cost") != f'${info["cost_usd"]:.2f}':
        ERRORS.append(f"variance: game-data {run}.cost != variance.json")
    if f'id="game-card-{run}"' not in variance_text:
        ERRORS.append(f"variance: missing gallery card for {run}")
    if f'data-play="{run}"' not in variance_text:
        ERRORS.append(f"variance: missing Play button for {run}")
    game_dir = SITE / "games" / run
    if not (game_dir / "index.html").is_file():
        ERRORS.append(f"Missing staged variance game entry: games/{run}/index.html")
    for shot in ("desktop", "mobile"):
        if not (SITE / "assets" / "games" / f"{run}-{shot}.png").is_file():
            ERRORS.append(f"Missing variance thumbnail: assets/games/{run}-{shot}.png")
    if (game_dir / "index.html").is_file():
        staged = " ".join(
            path.read_text(errors="replace")
            for path in game_dir.rglob("*")
            if path.is_file()
            and path.suffix in (".html", ".css", ".js", ".mjs", ".svg", ".json", ".webmanifest", ".xml")
        )
        for pattern in (
            'href="/',
            "href='/",
            'src="/',
            "src='/",
            "url(/",
            "url('/",
            'url("/',
            "@import '/",
            '@import "/',
            "from '/",
            'from "/',
            "import('/",
            'import("/',
        ):
            if pattern in staged:
                ERRORS.append(f"games/{run}/: absolute asset ref {pattern} survived staging")
                break
if variance_text.count('<article class="game-card') != len(variance_order):
    ERRORS.append("variance: gallery card count != variance.json run count")
for lane in ("muse", "fireworks-med", "openrouter-med", "deepseek-fw"):
    if f"<code>{lane}</code>" not in variance_text:
        ERRORS.append(f"variance: missing per-lane table row for {lane}")
for finding in (
    "survives with error bars",
    "different pipes",
    "verification depth",
):
    if finding not in variance_text:
        ERRORS.append(f"variance: missing key finding ({finding})")
if 'class="history-banner' not in variance_text or "earlier design" not in variance_text:
    ERRORS.append("variance: missing the banner saying the page documents eagent's earlier design")
if "#in-practice" in variance_text:
    ERRORS.append("variance: links to the main page's removed game gallery")
if "VARIANCE.md" not in variance_text:
    ERRORS.append("variance: missing link to the variance writeup")
variance_page = pages["variance/index.html"]
for key in ("description", "viewport", "og:title", "og:description", "og:image", "twitter:card"):
    if not variance_page.metadata.get(key):
        ERRORS.append(f"variance: missing {key}")
if "https://ericflo.github.io/eagent/variance/" not in (SITE / "sitemap.xml").read_text():
    ERRORS.append("sitemap.xml: missing variance URL")
if ERRORS:
    print("\n".join(ERRORS), file=sys.stderr)
    sys.exit(1)
print(f"Site checks passed: {len(index.ids)} unique IDs, local assets, links, and metadata.")
