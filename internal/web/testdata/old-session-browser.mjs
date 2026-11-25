// Drives the web UI over old sessions; run by browser_test.go.
// node old-session-browser.mjs <base> check|look "<id> <id>"
import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

const [base, mode, ids] = process.argv.slice(2);
const moduleName = process.env.EAGENT_PLAYWRIGHT_MODULE;
const { chromium } = await import(path.isAbsolute(moduleName) ? pathToFileURL(moduleName).href : moduleName);
const shots = process.env.EAGENT_BROWSER_SHOTS || '';
if (shots) await mkdir(shots, { recursive: true });
const sessions = (ids || '').split(/\s+/).filter(Boolean);

const browser = await chromium.launch({ headless: true });
try {
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
  const errors = [];
  page.on('pageerror', e => errors.push(e.message));
  const shot = async name => { if (shots) await page.screenshot({ path: path.join(shots, name + '.png'), fullPage: false }); };
  const open = async (id, tab) => {
    await page.goto(`${base}/#/s/${id}/${tab}`);
    await page.locator('#pane').waitFor();
    await page.waitForTimeout(400);
  };
  if (mode === 'check') {
    const [done, upgraded] = sessions;
    // An old bookmark to the removed Tasks tab opens the chat.
    await open(done, 'tasks');
    await page.locator('#pane .chat, #pane.chat').first().waitFor();
    assert.ok(await page.locator('#pane .msg').count() > 0, 'the tasks bookmark opened a blank pane');
    // Legacy rows show by default, in words.
    const acts = await page.locator('#pane .act').allInnerTexts();
    const text = acts.join('\n');
    for (const want of ['narrator trimmed', 'told the user the tokenizer review began', 'delegated t1', 'Review the tokenizer', 't1 completed', 'found one off-by-one bug in peek', 'note', 'starting with the tokenizer', 'p1 started', 'go test ./parser', 'p1 exited', 'done', 'reviewed the tokenizer and fixed peek']) {
      assert.ok(text.includes(want), `activity is missing ${JSON.stringify(want)}:\n${text}`);
    }
    assert.ok(await page.locator('label.toggle input').isChecked(), 'the activity toggle is off for an old session');
    // How it stopped, in the live line.
    assert.match(await page.locator('#live').innerText(), /done, waiting for you/);
    await shot('fixture-old-chat');
    // Turning it off sticks for old sessions only.
    await page.locator('label.toggle input').uncheck();
    assert.equal(await page.locator('#pane .act').count(), 0, 'the toggle did not hide the rows');
    await page.locator('label.toggle input').check();
    // The timeline reads the same events in words.
    await open(done, 'timeline');
    const rows = (await page.locator('.tl td.sum').allInnerTexts()).join('\n');
    for (const want of ['t1 Review the tokenizer', 't1 completed: found one', 'p1 $ go test ./parser', 'p1 exited exit=0', 'DONE: reviewed the tokenizer', 'starting with the tokenizer', 't1 started']) {
      assert.ok(rows.includes(want), `timeline is missing ${JSON.stringify(want)}:\n${rows}`);
    }
    await shot('fixture-old-timeline');
    await open(done, 'usage');
    await page.locator('#pane .card').first().waitFor();
    await shot('fixture-old-usage');
    // The takeover boundary shows in the chat, like a rollover.
    await open(upgraded, 'chat');
    const notices = (await page.locator('#pane .notice').allInnerTexts()).join('\n');
    assert.match(notices, /the current eagent took this older session over here \(subsession 2\)/);
    // An agent's state other than a stop or an exit is activity.
    assert.match((await page.locator('#pane .act').allInnerTexts()).join('\n'), /working/);
    await shot('fixture-upgraded-chat');
    // An unknown tab falls back to the chat as well.
    await open(upgraded, 'nonsense');
    assert.ok(await page.locator('#pane .msg').count() > 0, 'an unknown tab opened a blank pane');
  } else if (mode === 'config') {
    // "New session with this" on the configuration page starts on the
    // project's own configuration.
    let started = null;
    await page.route('**/api/sessions', route => { if (route.request().method() !== 'POST') return route.continue(); started = route.request().postDataJSON(); return route.fulfill({ status: 500, contentType: 'application/json', body: '{"error":"not in this test"}' }); });
    await page.goto(`${base}/#/config`);
    await page.getByRole('button', { name: 'New session with this', exact: true }).click();
    await page.getByText('It runs on the project\'s configuration as saved.', { exact: true }).waitFor();
    await page.locator('#modal textarea').fill('hello');
    await page.getByRole('button', { name: 'Start', exact: true }).click();
    for (let i = 0; i < 50 && !started; i++) await page.waitForTimeout(100);
    assert.equal(started?.use_project, true, JSON.stringify(started));
  } else {
    for (const id of sessions) {
      for (const tab of ['chat', 'timeline', 'usage']) {
        await open(id, tab);
        if (tab === 'chat') { const sc = page.locator('#pane .scroll'); await sc.evaluate(el => { el.scrollTop = 0; }); }
        await shot(`${id}-${tab}`);
        if (tab === 'chat') {
          await page.locator('#pane .scroll').evaluate(el => { el.scrollTop = el.scrollHeight; });
          await shot(`${id}-${tab}-end`);
          console.log(id, 'activity rows', await page.locator('#pane .act').count(), 'messages', await page.locator('#pane .msg').count(), 'notices', await page.locator('#pane .notice').count());
          console.log(id, 'live:', await page.locator('#live').innerText());
        }
      }
    }
  }
  assert.deepEqual(errors, [], 'the page threw');
  console.log('ok');
} finally {
  await browser.close();
}
