// Follows a hosted session in the browser while its record grows; run by
// r5web_test.go. It prints "ready" once the page shows the session, and
// waits for a line on stdin, sent after the test has written the next
// events, before it checks that the page followed them.
// node live-session-browser.mjs <base> <id>
import assert from 'node:assert/strict';
import path from 'node:path';
import readline from 'node:readline';
import { pathToFileURL } from 'node:url';

const [base, id] = process.argv.slice(2);
const moduleName = process.env.EAGENT_PLAYWRIGHT_MODULE;
const { chromium } = await import(path.isAbsolute(moduleName) ? pathToFileURL(moduleName).href : moduleName);
const lines = readline.createInterface({ input: process.stdin });
const next = () => new Promise(resolve => lines.once('line', resolve));

const browser = await chromium.launch({ headless: true });
try {
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
  const errors = [];
  page.on('pageerror', e => errors.push(e.message));
  await page.goto(`${base}/#/s/${id}/chat`);
  await page.locator('#pane .qcard').nth(1).waitFor();
  const cards = page.locator('#pane .qcard');
  assert.equal(await cards.nth(0).locator('.options button').count(), 2, 'the first question has no options to pick');
  assert.equal(await cards.nth(1).locator('.options button').count(), 2, 'the second question has no options to pick');
  const tools = page.locator('.tabs .tools');
  await tools.getByRole('button', { name: 'Stop', exact: true }).waitFor();
  assert.match(await tools.locator('.badge.model').innerText(), /^first$/);
  console.log('ready');
  await next();
  // One question answered, the other dismissed: neither card offers its
  // options any more.
  await cards.nth(0).locator('.answered').getByText('you answered: yes').waitFor({ timeout: 5000 });
  await cards.nth(1).locator('.answered').getByText('dismissed without an answer').waitFor({ timeout: 5000 });
  assert.equal(await page.locator('#pane .qcard .options button').count(), 0, 'a closed question still offers its options');
  // The session stopped and its model changed: the toolbar says so.
  await tools.getByRole('button', { name: 'Resume', exact: true }).waitFor({ timeout: 5000 });
  assert.equal(await tools.getByRole('button', { name: 'Stop', exact: true }).count(), 0, 'Stop is still offered for a stopped session');
  await tools.locator('.badge.model').getByText('second', { exact: true }).waitFor({ timeout: 5000 });
  assert.deepEqual(errors, [], 'the page threw');
  console.log('ok');
} finally {
  lines.close();
  await browser.close();
}
