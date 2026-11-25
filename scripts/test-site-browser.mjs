#!/usr/bin/env node
// Real browsers, the published /eagent/ path, and no model or account traffic.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdir, readFile } from "node:fs/promises";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { serveSite, root, source, site, siteRequire } from "./site-server.mjs";

const playwright = siteRequire("playwright");
const axe = siteRequire("axe-core");
const { server, base } = await serveSite();
const origin = new URL(base).origin;
const canonical = JSON.parse(execFileSync("go", ["run", "./scripts/site-data"], { cwd: root, encoding: "utf8" }));
const examples = JSON.parse(await readFile(path.join(source, "examples.json"), "utf8")).presets;
const example = canonical.presets.find(preset => preset.name === examples[0]);
const engines = (process.env.SITE_BROWSERS || "chromium").split(",");
let browser;
let assertions = 0;
function check(value, message) { assert.ok(value, message); assertions++; }
async function selected(page, id) {
  check(await page.locator("#" + id).getAttribute("aria-selected") === "true", id + " is selected");
}
async function accessibility(page) {
  await page.addScriptTag({ content: axe.source });
  const result = await page.evaluate(() => window.axe.run(document, {
    runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21aa", "best-practice"] }
  }));
  assert.deepEqual(result.violations.map(item => ({ id: item.id, nodes: item.nodes.map(node => node.target) })), []);
  assertions++;
}
try {
  for (const engine of engines) {
    check(Boolean(playwright[engine]), "known browser: " + engine);
    browser = await playwright[engine].launch({ headless: true });
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
    const errors = [], external = [], failed = [];
    context.on("page", page => {
      page.on("pageerror", error => errors.push(error.message));
      page.on("console", message => { if (message.type() === "error") errors.push(message.text()); });
      page.on("response", response => { if (response.status() >= 400) failed.push(response.url()); });
    });
    await context.route("**/*", route => {
      if (new URL(route.request().url()).origin !== origin) {
        external.push(route.request().url());
        return route.abort();
      }
      return route.continue();
    });
    const page = await context.newPage();
    await page.goto(base, { waitUntil: "networkidle" });
    await page.evaluate(() => document.fonts.ready);
    await page.addStyleTag({ content: "html { scroll-behavior: auto !important; }" });
    check(await page.title() === "eagent — A go-between for your coding agent", "page title");
    check(await page.locator("h1").count() === 1, "one main heading");

    // All assets stay relative to the GitHub Pages project path.
    check(await page.locator('link[rel="canonical"]').getAttribute("href") === "https://ericflo.github.io/eagent/", "canonical URL");

    // The turn walkthrough: six steps, three parties, three connections.
    const stepText = () => page.locator("#step-content").innerText();
    check((await stepText()).includes("before any model call"), "first step says forwarding comes before any model call");
    await page.locator("#step-watch").click();
    check((await stepText()).includes("transcript"), "walkthrough changes");
    await page.locator("#step-watch").press("End");
    await selected(page, "step-ask");
    check((await stepText()).includes("Your answer goes back to the agent"), "question panel");
    await page.locator("#step-ask").press("Home");
    await selected(page, "step-send");
    await page.locator("#step-send").press("ArrowDown");
    await selected(page, "step-watch");
    await page.locator("#step-dialog").click();
    check((await stepText()).includes("read off the screen"), "dialog panel");

    const stages = {
      send: { active: ["user", "agent"], edges: ["words"] },
      watch: { active: ["eagent", "agent"], edges: ["relay"] },
      progress: { active: ["user", "eagent"], edges: ["relay", "tell"] },
      dialog: { active: ["eagent", "agent"], edges: ["relay"] },
      end: { active: ["user", "eagent", "agent"], edges: ["relay", "tell"] },
      ask: { active: ["user", "eagent"], edges: ["relay", "tell"] },
    };
    for (const [stage, expected] of Object.entries(stages)) {
      await page.locator("#step-" + stage).click();
      check(await page.locator(".actor-flow").getAttribute("data-stage") === stage, "diagram follows step " + stage);
      assert.deepEqual(await page.locator('.flow-node[data-active="true"]').evaluateAll(nodes => nodes.map(node => node.dataset.node)), expected.active); assertions++;
      assert.deepEqual((await page.locator('.flow-edges [data-edge][data-active="true"]').evaluateAll(edges => edges.map(edge => edge.dataset.edge))).sort(), [...expected.edges].sort()); assertions++;
      check((await page.locator(".flow-canvas").getAttribute("aria-label")).length > 100, "accessible diagram description: " + stage);
      for (const edge of expected.edges) {
        check(await page.locator(`.flow-label[data-label="${edge}"]`).isVisible(), stage + ": the " + edge + " connection is labelled");
      }
    }
    // The relay arrow points at eagent when it reads, at the agent when it sends.
    const relay = page.locator('[data-edge="relay"]');
    await page.locator("#step-watch").click();
    check(await relay.getAttribute("marker-start") === "url(#flow-arrow)" && await relay.getAttribute("marker-end") === "none", "watching: transcript flows to eagent");
    await page.locator("#step-dialog").click();
    check(await relay.getAttribute("marker-start") === "url(#flow-arrow)" && await relay.getAttribute("marker-end") === "url(#flow-arrow)", "dialog: screen in, keys out");
    await page.locator("#step-ask").click();
    check(await relay.getAttribute("marker-start") === "none" && await relay.getAttribute("marker-end") === "url(#flow-arrow)", "question: your answer flows to the agent");
    check(await page.locator('[data-edge="tell"]').getAttribute("marker-end") === "url(#flow-arrow)", "eagent's messages point at you");
    await page.locator("#step-send").click();
    check(await page.locator('[data-edge="tell"]').getAttribute("data-active") === "false", "returning to the first step clears later paths");
    check(await page.locator('.flow-label[data-label="tell"]').isHidden(), "returning to the first step hides later labels");

    // The page no longer presents the three-actor design or its evidence.
    const mainText = await page.locator("main").innerText();
    for (const retired of ["Orchestrator delegates", "Task worker", "Breakout", "93%", "Sixteen presets"]) {
      check(!mainText.includes(retired), "main page does not present the earlier design: " + retired);
    }
    check(await page.locator('main a[href="variance/"]').count() === 0, "the variance page is not cited as evidence in the main page");
    check(await page.locator('footer a[href="variance/"]').count() === 1, "the variance page is linked from the footer");
    check(await page.locator('img[src*="web-tasks"], img[src*="web-chat"]').count() === 0, "no screenshots of the old web UI");

    // Presets: one model per preset, all from a fresh export of the Go config.
    const names = await page.locator("#preset-select option").evaluateAll(options => options.map(option => option.value));
    assert.deepEqual(names, [...examples, ...canonical.presets.map(preset => preset.name).filter(name => !examples.includes(name))]); assertions++;
    assert.deepEqual((await page.locator(".provider-list span").allTextContents()).sort(), [...canonical.providers].sort()); assertions++;
    const embedded = await page.locator("#preset-data").textContent();
    assert.deepEqual(JSON.parse(embedded), canonical); assertions++;
    check(await page.locator("#preset-select").inputValue() === example.name, "featured example selected");
    check(await page.locator(".model-route").count() === 1, "one model per preset");
    const presetFlag = preset => (!preset.name || preset.name === canonical.default ? "" : "--preset " + preset.name);
    for (const id of ["key-command", "start-command"]) {
      const text = await page.locator("#" + id).innerText();
      check(presetFlag(example) ? text.includes(presetFlag(example)) : !text.includes("--preset"), id + " uses the featured preset");
    }
    const exampleRoute = example.routes.find(route => route.actor === "eagent");
    if (exampleRoute.keyEnv) check((await page.locator("#key-command").innerText()).includes(exampleRoute.keyEnv), "example API key matches its canonical route");
    const defaults = canonical.defaults[0];
    check((await page.locator("#models").innerText()).includes(defaults.label + " on " + defaults.provider), "built-in default model named from the catalog");
    for (const preset of canonical.presets) {
      await page.locator("#preset-select").selectOption(preset.name);
      const route = preset.routes.find(item => item.actor === "eagent");
      check(Boolean(route), preset.name + ": has a model for eagent");
      check(await page.locator("#model-eagent").innerText() === route.label, preset.name + ": model");
      check(await page.locator("#provider-eagent").innerText() === route.provider, preset.name + ": provider");
      const effort = await page.locator("#effort-eagent").innerText();
      check(route.effort === "none" ? effort === "no reasoning" : route.effort ? effort === route.effort + " effort" : effort === "provider default", preset.name + ": effort");
      const fallback = page.locator("#fallback-eagent");
      check(await fallback.isVisible() === Boolean(route.fallback), preset.name + ": fallback visibility");
      for (let next = route.fallback; next; next = next.fallback) {
        const text = await fallback.innerText();
        check(text.includes(next.label) && text.includes(next.provider), preset.name + ": fallback route");
      }
      check(await page.locator("#preset-note").innerText() === preset.description, "canonical description: " + preset.name);
      const command = await page.locator("#preset-command").innerText();
      check(presetFlag(preset) ? command.includes(presetFlag(preset) + " ") : !command.includes("--preset"), "matching command: " + preset.name);
    }
    await page.locator("#preset-select").selectOption(example.name);

    await page.locator("#install-source").click();
    const sourceCommand = await page.locator("#install-command").innerText();
    check(sourceCommand.split("\n").length === 3 && sourceCommand.includes("make build"), "source install keeps real newlines");
    if (engine === "chromium") {
      await context.grantPermissions(["clipboard-read", "clipboard-write"]);
      await page.locator('[data-copy="install-command"]').click();
      check(await page.evaluate(() => navigator.clipboard.readText()) === sourceCommand, "clipboard gets the full command");
      await page.locator('[data-copy="hero-command"]').click();
      check(await page.evaluate(() => navigator.clipboard.readText()) === "go install github.com/ericflo/eagent/cmd/eagent@latest", "hero clipboard");
      // The fallback preserves keyboard focus when the modern API is unavailable.
      await page.evaluate(() => Object.defineProperty(navigator, "clipboard", { configurable: true, value: undefined }));
      await page.locator('[data-copy="preset-command"]').click();
      check((await page.locator("#toast").innerText()).startsWith("Copied."), "clipboard fallback");
    }
    await page.locator("#install-go").click();
    await page.locator(".faq-list summary").first().click();
    check(await page.locator(".faq-list details").first().getAttribute("open") !== null, "FAQ expands");

    for (const width of [320, 360, 390, 430, 650, 651, 768, 1024, 1440, 1920]) {
      await page.setViewportSize({ width, height: 900 });
      check(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "no horizontal overflow at " + width);
      check(await page.locator(".flow-node").evaluateAll(nodes => nodes.every(node => node.scrollWidth <= node.clientWidth)), "diagram labels fit at " + width);
      check(await page.locator(".flow-node").evaluateAll(nodes => nodes.every((node, i) => nodes.every((other, j) => {
        if (i >= j) return true;
        const a = node.getBoundingClientRect(), b = other.getBoundingClientRect();
        return a.right <= b.left || b.right <= a.left || a.bottom <= b.top || b.bottom <= a.top;
      }))), "diagram nodes do not overlap at " + width);
    }
    await page.setViewportSize({ width: 390, height: 844 });
    await page.evaluate(() => scrollTo(0, 0));
    await page.locator(".menu-toggle").click();
    check(await page.locator(".menu-toggle").getAttribute("aria-expanded") === "true", "mobile menu opens");
    await page.keyboard.press("Escape");
    check(await page.locator(".menu-toggle").getAttribute("aria-expanded") === "false", "Escape closes menu");
    await page.locator(".menu-toggle").click();
    await page.locator("#navigation a").first().click();
    check(await page.locator(".menu-toggle").getAttribute("aria-expanded") === "false", "navigation closes menu");

    if (engine === "chromium") {
      await accessibility(page);
      await page.setViewportSize({ width: 1440, height: 1000 });
      await accessibility(page);
    }

    if (process.env.SITE_SCREENSHOT_DIR) {
      const directory = path.resolve(process.env.SITE_SCREENSHOT_DIR);
      await mkdir(directory, { recursive: true });
      for (const [name, width, height] of [["desktop", 1440, 1000], ["mobile", 390, 844]]) {
        await page.setViewportSize({ width, height });
        await page.evaluate(() => scrollTo(0, 0));
        await page.screenshot({ path: path.join(directory, engine + "-" + name + ".png") });
      }
    }

    // Readable with scripts disabled; all essential content is in the HTML.
    const noJS = await browser.newContext({ javaScriptEnabled: false, viewport: { width: 390, height: 844 } });
    const plain = await noJS.newPage();
    await plain.goto(base);
    check(await plain.locator("#hero-title").isVisible(), "no-JavaScript headline");
    check(await plain.locator("#navigation").isVisible(), "no-JavaScript mobile navigation");
    check(await plain.locator("#install-command").innerText() === "go install github.com/ericflo/eagent/cmd/eagent@latest", "no-JavaScript installation");
    check(await plain.locator(".menu-toggle").isHidden(), "no dead menu toggle without JavaScript");
    check(await plain.locator("#model-eagent").innerText() === exampleRoute.label, "no-JavaScript canonical model");
    check((await plain.locator("#step-content").innerText()).includes("as written"), "no-JavaScript first walkthrough step");
    check(await plain.locator(".faq-list details").count() >= 5, "no-JavaScript FAQ");
    await noJS.close();

    await page.emulateMedia({ reducedMotion: "reduce" });
    check(await page.locator(".orbit-trail").first().evaluate(element => parseFloat(getComputedStyle(element).animationDuration) < 0.01), "reduced motion respected");
    assert.deepEqual(errors, [], "no browser exceptions");
    assert.deepEqual(external, [], "no external resource requests");
    assert.deepEqual(failed, [], "no failed resource loads");
    assertions += 3;
    await context.close();

    // Opening the index from disk works too: there are no fetches or module imports.
    const local = await browser.newPage();
    await local.goto(pathToFileURL(path.join(site, "index.html")).href);
    await local.locator("#step-end").click();
    check((await local.locator("#step-content").innerText()).includes("full report"), "file URL works");
    await local.close();

    // The variance page says up front that it documents the earlier design.
    const annex = await browser.newPage({ viewport: { width: 390, height: 844 } });
    const annexErrors = [];
    annex.on("pageerror", error => annexErrors.push(error.message));
    await annex.goto(base + "variance/", { waitUntil: "domcontentloaded" });
    const banner = annex.locator(".history-banner");
    check(await banner.isVisible(), "variance page shows its earlier-design banner");
    check((await banner.innerText()).includes("earlier design"), "banner names the earlier design");
    const bannerBox = await banner.boundingBox();
    const headingBox = await annex.locator("h1").boundingBox();
    check(bannerBox.y < headingBox.y, "banner comes before the page heading");
    check(await annex.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "variance page has no horizontal overflow on a phone");
    assert.deepEqual(annexErrors, [], "no exceptions on the variance page"); assertions++;
    await annex.close();

    const missing = await browser.newPage();
    const response = await missing.goto(base + "a-page-that-does-not-exist");
    check(response.status() === 404, "custom 404 response");
    check((await missing.locator("h1").innerText()).includes("off the map"), "branded 404 page");
    await missing.locator("a").click();
    await missing.locator("#hero-title").waitFor({ state: "visible" });
    check(await missing.locator("#hero-title").isVisible(), "404 returns home under the project prefix");
    await missing.close();

    console.log(engine + ": interactions, responsive layout, keyboard navigation, reduced motion, and static loading passed.");
    await browser.close();
    browser = null;
  }
  console.log("Passed " + assertions + " browser checks.");
} finally {
  await browser?.close();
  await new Promise(resolve => server.close(resolve));
}
