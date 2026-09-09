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
    check(await page.title() === "eagent — Big work. One binary.", "page title");
    check(await page.locator("h1").count() === 1, "one main heading");

    // All assets stay relative to the GitHub Pages project path.
    check(await page.locator('link[rel="canonical"]').getAttribute("href") === "https://ericflo.github.io/eagent/", "canonical URL");
    await page.locator("#step-build").click();
    check((await page.locator("#step-content").innerText()).includes("Each worker starts with a fresh context"), "walkthrough changes");
    await page.locator("#step-build").press("End");
    await selected(page, "step-report");
    check((await page.locator("#step-content").innerText()).includes("final report"), "report panel");
    await page.locator("#step-report").press("Home");
    await selected(page, "step-plan");
    await page.locator("#step-plan").press("ArrowDown");
    await selected(page, "step-build");
    await page.locator("#step-verify").click();
    check((await page.locator("#step-content").innerText()).includes("verifies the result against your request"), "verification panel");

    for (const [stage, active] of Object.entries({plan: ["user", "orchestrator"], build: ["engine", "touch"], verify: ["orchestrator"], report: ["user", "narrator"]})) {
      await page.locator("#step-" + stage).click();
      check(await page.locator(".actor-flow").getAttribute("data-stage") === stage, "diagram follows stage " + stage);
      assert.deepEqual(await page.locator('.flow-node[data-active="true"]').evaluateAll(nodes => nodes.map(node => node.dataset.node)), active); assertions++;
      check((await page.locator(".flow-canvas").getAttribute("aria-label")).length > 100, "accessible diagram description");
    }
    check(await page.locator('[data-edge="report"]').getAttribute("data-active") === "true", "final report returns to user");
    await page.locator("#step-plan").click();
    check(await page.locator('[data-edge="report"]').getAttribute("data-active") === "false", "returning to plan clears later-stage paths");
    check(await page.locator('[data-edge="engine"]').getAttribute("marker-end") === "url(#flow-arrow)", "delegation arrows point toward workers");
    await page.locator("#step-verify").click();
    check(await page.locator('[data-edge="engine"]').getAttribute("marker-start") === "url(#flow-arrow)", "verification arrows return reports to the orchestrator");

    const names = await page.locator("#preset-select option").evaluateAll(options => options.map(option => option.value));
    assert.deepEqual(names, [...examples, ...canonical.presets.map(preset => preset.name).filter(name => !examples.includes(name))]); assertions++;
    assert.deepEqual((await page.locator(".provider-list span").allTextContents()).sort(), [...canonical.providers].sort()); assertions++;
    const embedded = await page.locator("#preset-data").textContent();
    assert.deepEqual(JSON.parse(embedded), canonical); assertions++;
    check(await page.locator("#preset-select").inputValue() === example.name, "featured example selected");
    for (const id of ["key-command", "start-command"]) {
      check((await page.locator("#" + id).innerText()).includes("--preset " + example.name), id + " uses the featured provider");
    }
    for (const route of example.routes) {
      if (route.keyEnv) check((await page.locator("#key-command").innerText()).includes(route.keyEnv), "example API key matches its canonical route");
    }
    for (const preset of canonical.presets) {
      await page.locator("#preset-select").selectOption(preset.name);
      for (const route of preset.routes) {
        check(await page.locator("#model-" + route.actor).innerText() === route.label, preset.name + ": " + route.actor + " model");
        check(await page.locator("#provider-" + route.actor).innerText() === route.provider, preset.name + ": " + route.actor + " provider");
        const effort = await page.locator("#effort-" + route.actor).innerText();
        check(route.effort === "none" ? effort === "no reasoning" : route.effort ? effort === route.effort + " effort" : effort === "provider default", preset.name + ": " + route.actor + " effort");
        const fallback = page.locator("#fallback-" + route.actor);
        check(await fallback.isVisible() === Boolean(route.fallback), preset.name + ": " + route.actor + " fallback visibility");
        for (let next = route.fallback; next; next = next.fallback) {
          const text = await fallback.innerText();
          check(text.includes(next.label) && text.includes(next.provider), preset.name + ": " + route.actor + " fallback route");
        }
      }
      check(await page.locator("#preset-note").innerText() === preset.description, "canonical description: " + preset.name);
      const command = await page.locator("#preset-command").innerText();
      check(preset.name ? command.includes("--preset " + preset.name + " ") : !command.includes("--preset"), "matching command: " + preset.name);
    }
    await page.locator("#preset-select").selectOption(example.name);

    await page.locator("#view-chat").click();
    await selected(page, "view-chat");
    check((await page.locator("#workspace-image").getAttribute("src")).endsWith("web-chat.png"), "chat screenshot");
    await page.locator("#expand-screenshot").click();
    check(await page.locator("#image-dialog").evaluate(dialog => dialog.open), "image dialog opens");
    await page.keyboard.press("Escape");
    check(!(await page.locator("#image-dialog").evaluate(dialog => dialog.open)), "Escape closes dialog");
    check(await page.locator("#expand-screenshot").evaluate(button => button === document.activeElement), "dialog returns focus");
    await page.locator("#view-tasks").click();

    await page.locator("#game-thumb-muse").scrollIntoViewIfNeeded();
    check(await page.locator(".game-card").count() === 14, "fourteen playable cards");
    check(await page.locator("#game-table tbody tr").count() === 14, "fourteen comparison rows");
    check(await page.locator("#game-thumb-muse").getAttribute("src") === "assets/games/muse-desktop.png", "card thumbnail");
    await page.locator('[data-play="muse"]').click();
    check(await page.locator("#game-dialog").evaluate(dialog => dialog.open), "game dialog opens");
    check(((await page.locator("#game-dialog-frame").getAttribute("src")) ?? "").endsWith("games/muse/"), "lazy game iframe");
    check(((await page.locator("#game-dialog-open").getAttribute("href")) ?? "").endsWith("games/muse/"), "full-page link");
    await page.locator("#game-shot-mobile").click();
    check(((await page.locator("#game-dialog-shot").getAttribute("src")) ?? "").endsWith("muse-mobile.png"), "mobile shot toggle");
    check(await page.locator("#game-shot-mobile").getAttribute("aria-pressed") === "true", "shot toggle pressed");
    await page.keyboard.press("Escape");
    check(!(await page.locator("#game-dialog").evaluate(dialog => dialog.open)), "Escape closes game dialog");

    await page.locator("#install-source").click();
    const sourceCommand = await page.locator("#install-command").innerText();
    check(sourceCommand.split("\n").length === 3 && sourceCommand.includes("go build"), "source install keeps real newlines");
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
    check(await plain.locator("#game-table tbody tr").count() === 14, "no-JavaScript comparison table");
    check(await plain.locator(".game-card").count() === 14, "no-JavaScript game cards");
    for (const route of example.routes) {
      check(await plain.locator("#model-" + route.actor).innerText() === route.label, "no-JavaScript canonical route: " + route.actor);
    }
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
    await local.locator("#step-report").click();
    check((await local.locator("#step-content").innerText()).includes("final report"), "file URL works");
    await local.close();

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
