# The eagent website

The GitHub Pages site lives in this directory. It is plain HTML, CSS, and
JavaScript with self-hosted fonts and images. The build renders `index.template.html` with the canonical Go configuration
into `dist/site/index.html`. The output works even from a file URL; there is
no production framework, analytics, or external resource request. JavaScript enhances the examples; the feature story and
installation instructions are already in the HTML.

## Preview

From the repository root:

```sh
python3 scripts/build-site.py
python3 -m http.server 4173 --bind 127.0.0.1 --directory dist/site
```

Open http://127.0.0.1:4173. This server is separate from `eagent serve`,
which serves the agent's own interface.

## Publish with GitHub Pages

The workflow is [`pages.yml`](../.github/workflows/pages.yml). In the
repository's **Settings → Pages → Build and deployment**, select
**GitHub Actions** as the source. Then push the website changes to `main`
or run **Website** manually from the Actions tab.

The workflow checks formatting, assets, links, and metadata; runs the
Chromium interaction and accessibility suite; stages the public files; and
deploys the resulting artifact. Pull requests run the checks without
deploying. Only `dist/site` is uploaded, so development dependencies,
test scripts, and this guide are excluded.

The intended URL is **https://ericflo.github.io/eagent/**. The main page
uses relative resource paths and is tested at the `/eagent/` prefix.
Canonical, Open Graph, sitemap, robots, and 404 URLs are explicit; update
them together if the repository name or domain changes.

The deployment follows GitHub's
[custom workflow documentation](https://docs.github.com/en/pages/getting-started-with-github-pages/using-custom-workflows-with-github-pages).

## Work on the site

Building requires Go (the version in `go.mod`) and Python 3. Node is used
only for development checks. The agent’s runtime is unchanged.

```sh
npm --prefix site ci
./site/node_modules/.bin/playwright install --with-deps chromium
npm --prefix site run check
npm --prefix site test
npm --prefix site run build
```

- `npm --prefix site run format` formats the HTML, CSS, and JavaScript.
- `python3 scripts/check-site.py` checks local references and metadata
  without Node.
- `npm --prefix site run build` renders the template and stages only public files in `dist/site`.
- `go test ./scripts/site-data` checks that new presets and changed routes flow through the exporter.
- `npm --prefix site run social-card` renders the 1200 × 630 sharing image
  from the actual site typography, mark, and SVG illustration.

To check all three browser engines:

```sh
./site/node_modules/.bin/playwright install --with-deps chromium firefox webkit
SITE_BROWSERS=chromium,firefox,webkit npm --prefix site test
```

Set `SITE_SCREENSHOT_DIR=/tmp/eagent-site-screenshots` to save desktop and
mobile screenshots during tests. Tests serve a temporary loopback site at
the GitHub Pages project path, never start an agent, and reject external
resource requests.

## Content and design

The page describes eagent as it is now: it runs a coding agent (Codex,
Claude Code, Antigravity, pi, or a custom profile) in a terminal and stands
between it and you. Every claim on it should trace to the README,
`docs/DESIGN.md`, the prompts in `internal/prompts/`, the CLI usage text, or
the code.

The visual identity uses warm paper and charcoal, with orange for eagent,
sage for the agent, and pale yellow for labels on connections. The hero's
orbital drawing, the session-directory and prompt diagrams, the dossier, and
the phone are native SVG/CSS. The turn walkthrough has six steps (your words,
watching, a progress line, a stuck dialog, the turn's end, a question for
you); each tab changes the active parties, the connections between them,
their direction, and their labels. The page respects reduced motion, pauses
offscreen animation, and supports keyboard navigation and native disclosure
panels.

Keep illustrations labeled as illustrations. The phone conversation and the
file names in the log diagram are illustrative; commands shown on the page
come from the README and `eagent help`. The old web UI screenshots
(`assets/web-*.png`) and Neon Brickles images show the earlier three-actor
design and are no longer used.

| Content | Source of truth |
| --- | --- |
| Installation, CLI, in-session commands | [README](../README.md), [CLI usage](../cmd/eagent/main.go) |
| The design: routing, looks, caching, dossier, retries | [How eagent works](../docs/DESIGN.md) |
| What eagent says and when | [EAGENT.md](../internal/prompts/EAGENT.md), [PERSONA.md](../internal/prompts/PERSONA.md) |
| Presets, models, reasoning effort, fallbacks | [Configuration](../internal/config/) |
| Event log and replay | [Event schema](../docs/EVENTS.md), [store](../internal/store/store.go) |
| Phone, archives, and settings | [Integration documentation](../docs/FINALECHAT-INTEGRATIONS.md) |
| Breakout variance page (earlier design) | [Variance data](../docs/grid/variance.json), [staged builds](../docs/grid/builds-variance/) |

The build runs [`scripts/site-data`](../scripts/site-data/main.go), which reads
`config.Defaults`, `config.Presets`, `config.PresetNames`, and the model/provider
catalog in [`internal/config`](../internal/config/). Each preset names one
model for eagent's loop (the `eagent` route; a dedicated summarizer route is
exported too but not shown). The build generates the dropdown, the example
model, the built-in default, the provider list, setup commands, and embedded
JSON for interactive selection. It never loads local configuration or API
keys. Do not maintain a separate preset catalog in HTML or JavaScript.
`examples.json` chooses existing preset names for the site. Commands omit
`--preset` when the example is the built-in default. Browser tests compare
every displayed route with a fresh export.

## Earlier design

The Breakout grid (sixteen presets building one game) and its variance annex
were run when eagent did the work itself with an orchestrator, task workers
and a narrator. The main page no longer shows them; it links to them only
under "Earlier design" in the footer. The variance page opens with a banner
saying which design it documents. The sixteen grid games are still staged
under `games/<preset>/` so existing links keep working.

Space Grotesk, DM Sans, and IBM Plex Mono are self-hosted under the SIL Open
Font License. Their license files are included in `assets/fonts/`.
