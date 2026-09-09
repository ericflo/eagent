#!/usr/bin/env node
// Render the sharing image from the site's own type, mark, and orbital drawing.
import path from "node:path";
import { serveSite, source, siteRequire } from "./site-server.mjs";

const { chromium } = siteRequire("playwright");
const { server, base } = await serveSite();
let browser;
try {
  browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1200, height: 630 }, deviceScaleFactor: 1, reducedMotion: "reduce" });
  await page.goto(base, { waitUntil: "networkidle" });
  await page.evaluate(() => {
    document.querySelectorAll("main > :not(.hero), .site-footer, .skip-link, .toast, .image-dialog, .menu-toggle, .navigation, .header-github, .hero-actions, .hero-install, .hero-footnote").forEach(element => element.remove());
    const footer = document.createElement("div");
    footer.className = "social-footer";
    footer.innerHTML = "<span>THREE ACTORS. ONE SHARED PURPOSE.</span><span>github.com/ericflo/eagent</span>";
    document.body.append(footer);
  });
  await page.addStyleTag({ content: `
    html,body{width:1200px;height:630px;overflow:hidden}
    .site-header{position:static;border:0;background:none}
    .header-inner{width:1100px;height:90px}
    .wordmark{font-size:30px}
    .hero{width:1100px;min-height:0;height:450px;padding:0;grid-template-columns:1.15fr 1fr;gap:22px}
    .hero h1{font-size:96px}
    .hero .eyebrow{font-size:9px;margin-bottom:27px}
    .hero-description{font-size:17px;max-width:430px;margin-top:24px}
    .hero-art{margin:0;width:455px;justify-self:end}
    .art-coordinate{font-size:8px}
    .coordinate-bottom{font-size:7px}
    .social-footer{display:flex;justify-content:space-between;align-items:center;width:1100px;height:70px;margin:10px auto 0;border-top:1px solid var(--line);font:10px var(--mono);letter-spacing:.07em;color:var(--muted)}
    .social-footer>span:last-child{color:var(--orange);letter-spacing:0}
  ` });
  await page.evaluate(() => document.fonts.ready);
  await page.screenshot({ path: path.join(source, "assets", "social-card.png") });
  console.log("Rendered site/assets/social-card.png (1200 × 630).");
} finally {
  await browser?.close();
  await new Promise(resolve => server.close(resolve));
}
