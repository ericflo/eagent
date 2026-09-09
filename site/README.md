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

The visual identity uses warm paper and charcoal, with orange for the
orchestrator, sage for the worker, and pale yellow for the narrator. The
hero's orbital drawing, dossier, event strip, and phone are native SVG/CSS.
The actor diagram changes active nodes, connection direction, and task states
with the walkthrough tabs. The recovery diagram shows how `seen_seq` preserves
causal order when task reports arrive during a model call. The page respects reduced motion, pauses offscreen animation, and supports
keyboard navigation, native disclosure panels, and a focus-managed image
dialog.

Keep the illustrations labeled as illustrations. The actual application
screenshots and game output come from the repository's published docs.

| Content | Source of truth |
| --- | --- |
| Installation, CLI, feature overview | [README](../README.md), [CLI](../cmd/eagent/main.go) |
| Example models, presets, reasoning effort, fallbacks | [Configuration](../internal/config/config.go) |
| Three roles and context model | [Design specification](../docs/DESIGN-SPEC.md), [actor prompts](../internal/prompts/) |
| Replay and committed events | [Event schema](../docs/EVENTS.md), [state views](../internal/state/views.go) |
| Dossiers and rollover | [Rollover implementation](../internal/harness/rollover.go) |
| Original Neon Brickles run: ~\$1.20, 29 minutes, 74 + 9 checks | [README run report](../README.md#what-it-did-on-a-real-task) |
| GLM and Anthropic-high examples | [Breakout grid](../docs/breakout-grid.md), [recorded data](../docs/grid/results.json) |
| Phone, archives, and settings | [Integration documentation](../docs/FINALECHAT-INTEGRATIONS.md) |

The build runs [`scripts/site-data`](../scripts/site-data/main.go), which reads
`config.Defaults`, `config.Presets`, `config.PresetNames`, and the model/provider
catalog in [`internal/config`](../internal/config/). It generates the dropdown,
example actor cards, provider list, setup commands, and embedded JSON for interactive selection.
It never loads local configuration or API keys. Do not maintain a separate
preset catalog in HTML or JavaScript. `examples.json` chooses existing preset
names for the site: OpenRouter first, followed by OpenCode, OpenAI, and Anthropic.
Their actual routes are always exported from Go. Configuration changes trigger the Pages
workflow, and browser tests compare every displayed route with a fresh export.

Prices and scores on the page describe
documented September 2026 runs, rather than current estimates or universal
performance claims. Keep the run date and methodology link alongside them.

Space Grotesk, DM Sans, and IBM Plex Mono are self-hosted under the SIL Open
Font License. Their license files are included in `assets/fonts/`.
