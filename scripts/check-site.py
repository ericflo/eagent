#!/usr/bin/env python3
"""Check the distributable site's assets, anchors, and metadata without dependencies."""
from html.parser import HTMLParser
from pathlib import Path
import re
import sys
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parent.parent
SITE = ROOT / "dist" / "site"
if not (SITE / "index.html").is_file():
    sys.exit("Build the website first: python3 scripts/build-site.py")
ERRORS = []


class Page(HTMLParser):
    def __init__(self, path):
        super().__init__(convert_charrefs=True)
        self.path = path
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


pages = {name: Page(SITE / name) for name in ("index.html", "404.html")}
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
        if url.path == "/eagent/":
            target = SITE / "index.html"
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
if ERRORS:
    print("\n".join(ERRORS), file=sys.stderr)
    sys.exit(1)
print(f"Site checks passed: {len(index.ids)} unique IDs, local assets, links, and metadata.")
