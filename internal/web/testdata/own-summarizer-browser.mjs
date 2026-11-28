// Gives a dedicated summarizer its own model on the configuration page
// while the environment names eagent's model, and saves; run by
// r3browser_test.go, which checks the file.
// node own-summarizer-browser.mjs <base>
import assert from 'node:assert/strict';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

const [base] = process.argv.slice(2);
const moduleName = process.env.EAGENT_PLAYWRIGHT_MODULE;
const { chromium } = await import(path.isAbsolute(moduleName) ? pathToFileURL(moduleName).href : moduleName);
const browser = await chromium.launch({ headless: true });
try {
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
  const errors = [];
  page.on('pageerror', e => errors.push(e.message));
  let sent = null;
  await page.route('**/api/config', route => { if (route.request().method() === 'PUT') sent = route.request().postDataJSON(); return route.continue(); });
  await page.goto(`${base}/#/config`);
  await page.getByRole('button', { name: 'Give it its own model', exact: true }).click();
  await page.locator('.route.summarizer .rhead').waitFor();
  await page.getByRole('button', { name: /^Save to project/ }).click();
  for (let i = 0; i < 50 && !sent; i++) await page.waitForTimeout(100);
  await page.getByText('Saved.', { exact: false }).first().waitFor();
  const route = sent?.config?.summarizer?.model;
  assert.ok(route, JSON.stringify(sent));
  assert.doesNotMatch(JSON.stringify(route), /anthropic|claude/i, JSON.stringify(route));
  assert.deepEqual(errors, [], 'the page threw');
  console.log('ok');
} finally {
  await browser.close();
}
