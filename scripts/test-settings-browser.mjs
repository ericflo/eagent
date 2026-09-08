#!/usr/bin/env node
// Real exported editor + SDK in an opaque iframe. The parent is a deterministic
// bridge fixture; FinaleChat separately tests its trusted controls and API.
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { readFile, mkdtemp, rm } from 'node:fs/promises';
import { execFileSync } from 'node:child_process';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const moduleName = process.env.FINALECHAT_PLAYWRIGHT_MODULE || 'playwright';
const { chromium } = await import(path.isAbsolute(moduleName) ? pathToFileURL(moduleName).href : moduleName);
const temporary = await mkdtemp(path.join(tmpdir(), 'eagent-settings-browser-'));
let server, browser;
try {
  execFileSync('go', ['run', './scripts/browser-fixture', temporary], { cwd: root, stdio: 'pipe' });
  const archive = path.join(temporary, 'archive');
  const manifest = JSON.parse(await readFile(path.join(archive, 'manifest.json'), 'utf8'));
  const resource = JSON.parse(await readFile(path.join(archive, 'settings/state.json'), 'utf8'));
  resource.id = 'fixture-resource'; resource.generation = '';
  const files = Object.fromEntries(await Promise.all(manifest.files.filter(f => ['settings/state.json', 'settings/editor.json'].includes(f.path)).map(async f => [f.path, [...await readFile(path.join(archive, f.path))]])));
  const website = await readFile(path.join(archive, 'settings/index.html'));
  const viewer = await readFile(path.join(archive, 'index.html'));
  const parent = `<!doctype html><iframe title="Settings" sandbox="allow-scripts" style="width:100%;height:96vh" src="/settings"></iframe><script>
    window.fixture=${JSON.stringify({resource, manifest, files}).replaceAll('<', '\\u003c')};
    window.calls=[]; window.proposals=[]; window.proposal=null; window.editable=true; window.inspection=null;
    window.anchor=null; window.revealed=null;
    document.querySelector('iframe').onload=()=>{
      const channel=new MessageChannel(); window.bridge=channel.port1; const nonce='fixture-nonce';
      bridge.onmessage=async event=>{
        const m=event.data; calls.push(m.method); let result, error;
        if(m.method==='artifact.manifest') result=fixture.manifest;
        else if(m.method==='artifact.read') { const bytes=fixture.files[m.params.path] || new Uint8Array(await (await fetch('/file?path='+encodeURIComponent(m.params.path))).arrayBuffer()); result=new Uint8Array(bytes.slice(m.params.offset,m.params.offset+m.params.length)).buffer; }
        else if(m.method==='artifact.view-state.read') result=window.inspection;
        else if(m.method==='artifact.view-state.write') { window.inspection=m.params; result={saved:true}; }
        else if(m.method==='artifact.anchor') result=window.anchor;
        else if(m.method==='thread.reveal') { window.revealed=m.params; result={available:true}; }
        else if(m.method==='settings.read') result={resource:fixture.resource,editable};
        else if(m.method==='settings.propose') { if(!editable) error='Read only'; else {proposal=m.params; if(proposal) proposals.push(proposal); result={staged:!!proposal};} }
        else error='Unsupported method: '+m.method;
        bridge.postMessage({nonce,id:m.id,result,error});
      };
      document.querySelector('iframe').contentWindow.postMessage({type:'finalechat.artifact.connect',version:1,nonce},'*',[channel.port2]);
    };
    window.complete=(p, result={})=>{bridge.postMessage({nonce:'fixture-nonce',event:'settings.result',detail:{proposal:p,status:'succeeded',result}})};
  </script>`;
  server = createServer(async (req, res) => {
    const url = new URL(req.url, 'http://fixture');
    if (url.pathname === '/file' && manifest.files.some(f => f.path === url.searchParams.get('path'))) {
      res.setHeader('Content-Type', 'application/octet-stream'); res.end(await readFile(path.join(archive, url.searchParams.get('path')))); return;
    }
    if (!['/', '/settings', '/viewer'].includes(url.pathname)) { res.writeHead(404); res.end(); return; }
    res.setHeader('Content-Type', 'text/html; charset=utf-8');
    if (url.pathname !== '/') res.setHeader('Content-Security-Policy', "default-src 'none'; script-src 'unsafe-inline' 'wasm-unsafe-eval'; style-src 'unsafe-inline'; connect-src 'none'; form-action 'none'; base-uri 'none'");
    res.end(url.pathname === '/settings' ? website : url.pathname === '/viewer' ? viewer : parent);
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const base = `http://127.0.0.1:${server.address().port}`;
  browser = await chromium.launch({ headless: true });
  const page = await browser.newPage();
  const errors = [], outbound = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.route('**/*', route => { if (new URL(route.request().url()).origin !== base) { outbound.push(route.request().url()); return route.abort(); } return route.continue(); });
  await page.goto(base);
  const frame = page.frameLocator('iframe');
  const concurrency = frame.locator('.field').filter({ has: frame.getByText('Task workers at once', { exact: true }) }).locator('input');
  await frame.getByRole('heading', { name: 'Configuration', exact: true }).waitFor();
  await frame.getByRole('button', { name: 'Review project changes', exact: true }).waitFor();
  assert.equal(await concurrency.inputValue(), '5');
  assert.equal(await page.evaluate(() => window.proposals.length), 0, 'opening settings staged a change');
  await concurrency.fill('6'); await concurrency.press('Tab');
  await frame.getByRole('button', { name: 'Review project changes', exact: true }).click();
  await page.waitForFunction(() => window.proposal !== null);
  let proposal = await page.evaluate(() => window.proposal);
  assert.deepEqual(proposal.edits, [{ op: 'set', key: '/task_concurrency', value: 6 }]);
  assert.equal(proposal.expected_version, resource.snapshot.version);
  assert.equal(await concurrency.inputValue(), '6', 'staging reset the form');
  assert.match(await frame.locator('#artifact-status').innerText(), /No settings have changed/);
  await concurrency.fill('7'); await concurrency.press('Tab');
  await page.waitForFunction(() => window.proposal === null);
  await frame.getByRole('button', { name: 'Review project changes', exact: true }).click();
  await page.waitForFunction(() => window.proposal?.edits?.[0]?.value === 7);
  await page.evaluate(p => { fixture.resource.snapshot.version = 'v2'; fixture.resource.snapshot.effective['/task_concurrency'] = 6; fixture.resource.snapshot.saved['/task_concurrency'] = 6; complete(p); }, proposal);
  await frame.locator('#artifact-status').filter({ hasText: 'newer local edits remain' }).waitFor();
  assert.equal(await concurrency.inputValue(), '7', 'late result discarded newer edits');
  await concurrency.fill('8'); await concurrency.press('Tab');
  await page.waitForFunction(() => window.proposal === null);
  await frame.getByRole('button', { name: 'Reload current defaults', exact: true }).click();
  await frame.getByRole('button', { name: 'Discard edits and reload', exact: true }).click();
  await page.waitForFunction(() => document.querySelector('iframe') !== null);
  await concurrency.filter({ visible: true }).waitFor();
  await frame.locator('#artifact-status').filter({ hasText: 'Editing current project defaults' }).waitFor();
  assert.equal(await concurrency.inputValue(), '6');

  await frame.getByRole('button', { name: 'fixture', exact: true }).click();
  assert.equal(await concurrency.inputValue(), '7', 'captured bundle could not be loaded');
  await frame.getByRole('button', { name: 'Discard', exact: true }).click();
  await frame.getByText('Named configuration actions', { exact: true }).click();
  await frame.getByLabel('Default named configuration').selectOption('fixture');
  await frame.getByRole('button', { name: 'Review default configuration', exact: true }).click();
  await page.waitForFunction(() => window.proposal?.edits?.[0]?.key === '/default_config');
  assert.equal((await page.evaluate(() => window.proposal)).edits[0].value, 'fixture');
  await frame.getByRole('button', { name: 'Review configuration deletion', exact: true }).click();
  await page.waitForFunction(() => window.proposal?.operation === 'bundle.delete');
  assert.equal((await page.evaluate(() => window.proposal)).parameters.name, 'fixture');

  // A saved-route test is a typed action, and editing an effort clears it.
  await frame.getByRole('button', { name: 'Review route test', exact: true }).first().click();
  await page.waitForFunction(() => window.proposal?.operation === 'route.test');
  proposal = await page.evaluate(() => window.proposal);
  assert.equal(proposal.parameters.actor, 'orchestrator');
  assert.equal(proposal.parameters.route, 'primary');
  await page.evaluate(p => complete(p, { route_test: { route: 'primary', model: 'fixture-model', effort: 'high', base_url: 'https://fixture.invalid', status: 'tool_call', ms: 250, usage: { input: 1, output: 2 }, at: '2026-09-08T12:00:00Z' } }), proposal);
  await frame.locator('.route.orchestrator').getByText(/fixture-model/).waitFor();

  // Prompt and bundle actions use the same editor but remain proposals.
  await frame.getByRole('button').filter({ hasText: /^PERSONA.md/ }).click();
  await frame.locator('#modal textarea').fill('Fixture persona {{.Name}}');
  await frame.getByRole('button', { name: 'Review prompt change', exact: true }).click();
  await page.waitForFunction(() => window.proposal?.operation === 'prompt.set');
  assert.equal((await page.evaluate(() => window.proposal)).parameters.text, 'Fixture persona {{.Name}}');
  await frame.getByRole('button', { name: 'Close', exact: true }).click();
  await frame.getByRole('button', { name: 'Save as bundle…', exact: true }).click();
  await frame.getByPlaceholder('name, e.g. eric-fast').fill('browser-copy');
  await frame.getByRole('button', { name: 'Review bundle change', exact: true }).click();
  await page.waitForFunction(() => window.proposal?.operation === 'bundle.save');
  assert.equal((await page.evaluate(() => window.proposal)).parameters.name, 'browser-copy');
  await frame.getByRole('button', { name: 'Cancel', exact: true }).click();

  // Raw overrides are not a file-write escape hatch; keep new raw drafts too.
  await frame.getByText('Files and raw JSON', { exact: true }).click();
  const raw = frame.getByRole('textbox', { name: 'Known saved overrides' });
  assert.doesNotMatch(await raw.inputValue(), /unknown_secret|fixture-must-not-be-published/);
  const saved = JSON.parse(await raw.inputValue()); saved.task_concurrency = 8;
  await raw.fill(JSON.stringify(saved));
  await frame.getByRole('button', { name: 'Review override changes', exact: true }).click();
  await page.waitForFunction(() => window.proposal?.operation === 'settings.apply');
  proposal = await page.evaluate(() => window.proposal);
  saved.task_concurrency = 9; await raw.fill(JSON.stringify(saved));
  await page.evaluate(p => { fixture.resource.snapshot.version = 'v3'; complete(p); }, proposal);
  await frame.locator('#artifact-status').filter({ hasText: 'newer local edits remain' }).waitFor();
  assert.equal(JSON.parse(await raw.inputValue()).task_concurrency, 9);
  await raw.fill('{"unknown_secret":"bad"}');
  await frame.getByRole('button', { name: 'Review override changes', exact: true }).click();
  await frame.getByText('Unsupported setting: /unknown_secret', { exact: true }).waitFor();
  assert.equal(await page.evaluate(() => window.proposal), null);

  // Revoked/read-only settings display the same complete form with no writes.
  await page.evaluate(() => { window.editable = false; });
  await frame.getByRole('button', { name: 'Reload current defaults', exact: true }).click();
  await frame.getByRole('button', { name: 'Discard edits and reload', exact: true }).click();
  await frame.locator('#artifact-status').filter({ hasText: 'read only' }).waitFor();
  assert.equal(await concurrency.isDisabled(), true);
  assert.equal(await frame.getByRole('button', { name: 'Review project changes', exact: true }).isDisabled(), true);
  assert.equal(await frame.getByRole('button', { name: 'Review route test', exact: true }).first().isDisabled(), true);
  assert.deepEqual(errors, []);
  assert.deepEqual(outbound, []);
  assert.ok((await page.evaluate(() => window.calls)).every(method => ['settings.read', 'settings.propose', 'artifact.manifest', 'artifact.read'].includes(method)));
  console.log('PASS exported shared editor, typed proposals, raw/prompt/bundle controls, read-only grants, route results, late acknowledgements, no external requests');

  await page.evaluate(() => { document.querySelector('iframe').src = '/viewer'; });
  await frame.locator('#until:not([disabled])').waitFor();
  await frame.getByRole('button', { name: 'Timeline', exact: true }).click();
  await frame.getByRole('searchbox', { name: 'Search session' }).fill('Synthetic');
  await page.waitForFunction(() => window.inspection?.search === 'Synthetic' && window.inspection?.view === 'events');
  const savedInspection = await page.evaluate(() => window.inspection);
  await page.evaluate(() => { document.querySelector('iframe').src = '/viewer?refresh=1'; });
  await frame.locator('#until:not([disabled])').waitFor();
  await frame.getByRole('button', { name: 'Timeline', exact: true, pressed: true }).waitFor();
  assert.equal(await frame.getByRole('searchbox', { name: 'Search session' }).inputValue(), savedInspection.search);
  await frame.getByText('2 matching events', { exact: true }).waitFor();
  assert.deepEqual(errors, []);
  assert.deepEqual(outbound, []);
  console.log('PASS actual connected Go WebAssembly viewer retains section and search across iframe replacement');
  await page.evaluate(() => { window.anchor={dataset_format:fixture.manifest.dataset.format,session_id:fixture.manifest.dataset.session_id,seq:2}; document.querySelector('iframe').src='/viewer?anchor=2'; });
  await frame.getByText('Selected event #2.', { exact: true }).waitFor();
  await frame.getByText('1 matching events', { exact: true }).waitFor();
  await page.setViewportSize({width:390,height:844});
  assert.equal(await frame.locator('.tl tr.detail td').isVisible(), true, 'mobile table hid the expanded event');
  assert.equal(await frame.locator('.tl tr.row td:nth-child(2)').innerText(), '+0:10.0', 'focused event lost its session-relative time');
  await page.setViewportSize({width:1280,height:900});
  await frame.getByRole('button', { name: 'Find chat message', exact: true }).click();
  await page.waitForFunction(() => window.revealed?.seq === 2);
  await frame.getByRole('button', { name: 'Show all events', exact: true }).click();
  assert.equal(await frame.locator('#focus-notice').isHidden(), true);
  assert.equal(await frame.locator('#until').inputValue(), '2', 'clearing the event focus jumped forward in time');
  await frame.getByRole('button', { name: 'Latest', exact: true }).click();
  console.log('PASS actual eagent viewer focuses a native sequence and requests its matching chat message');
  // The archive uses the local admin's actual task chart, table and drilldown.
  await frame.getByRole('button', { name: 'Tasks', exact: true }).click();
  await frame.locator('table.tasks tr.row').first().click();
  await frame.locator('#taskdetail').getByText('Archived task result', { exact: true }).waitFor();
  await frame.locator('#taskdetail').getByText('Archived tool output', { exact: true }).waitFor();
  assert.equal(await frame.locator('svg.gantt').count(), 1);
  await page.waitForFunction(() => window.inspection?.admin?.task === 't1');
  await page.evaluate(() => { window.anchor=null; document.querySelector('iframe').src='/viewer?task-refresh=1'; });
  await frame.locator('#taskdetail').getByText('Archived task result', { exact: true }).waitFor();
  await frame.getByRole('heading', { name: /Processes/ }).waitFor();
  assert.equal(await frame.getByRole('button', { name: 'Resume', exact: true }).count(), 0);
  await frame.locator('#until').evaluate(el => { el.value = '3'; el.dispatchEvent(new Event('change', { bubbles:true })); });
  await frame.locator('table.tasks').getByText('queued', { exact: true }).waitFor();
  assert.equal(await frame.getByText('Archived task result', { exact: true }).count(), 0);
  assert.equal(await frame.getByText('Archived tool output', { exact: true }).count(), 0);
  await frame.locator('#until').evaluate(el => { el.value = '2'; el.dispatchEvent(new Event('change', { bubbles:true })); });
  await frame.getByText('Nothing delegated yet.', { exact: true }).waitFor();
  assert.equal(await frame.getByRole('combobox', { name: 'Task', exact: true }).locator('option').count(), 1, 'task filter exposed future tasks');
  const atMessage = await frame.locator('body').evaluate(() => JSON.parse(window.eagentReplayConversation('narrator', '', 2)).result);
  const atLatest = await frame.locator('body').evaluate(() => JSON.parse(window.eagentReplayConversation('narrator', '', 0)).result);
  assert.ok(!JSON.stringify(atMessage).includes('Synthetic response'));
  assert.ok(JSON.stringify(atLatest).includes('Synthetic response'));
  await frame.getByRole('button', { name: 'Chat', exact: true }).click();
  await frame.locator('#pane .msg.user').getByText('Synthetic browser fixture', { exact: true }).waitFor();
  assert.equal(await frame.getByRole('textbox', { name: /Message the agent/ }).count(), 0);
  assert.deepEqual(errors, []);
  assert.deepEqual(outbound, []);
  console.log('PASS shared admin charts, task drilldown and tool output; earlier states exclude future tasks, reports and model conversation');


  // The exact same exported page opens from disk with verified captured data.
  await page.unroute('**/*');
  await page.goto(pathToFileURL(path.join(archive, 'settings/index.html')).href);
  const chooser = page.waitForEvent('filechooser');
  await page.getByRole('button', { name: 'Open extracted archive', exact: true }).click();
  await (await chooser).setFiles(archive);
  await page.locator('#artifact-status').filter({ hasText: 'Saved snapshot, read only' }).waitFor();
  await page.getByRole('heading', { name: 'Configuration', exact: true }).waitFor();
  assert.equal(await page.getByRole('button', { name: 'Review project changes', exact: true }).isDisabled(), true);
  assert.deepEqual(errors, []);
  console.log('PASS extracted settings website opens directly from disk with verified data and read-only controls');

  await page.goto(pathToFileURL(path.join(archive, 'index.html')).href);
  const viewerChooser = page.waitForEvent('filechooser');
  await page.getByRole('button', { name: 'Open extracted archive', exact: true }).click();
  await (await viewerChooser).setFiles(archive);
  await page.waitForFunction(() => !document.querySelector('#until').disabled);
  const expected = JSON.parse(await readFile(path.join(archive, 'derived/summary.json'), 'utf8'));
  const replayed = await page.evaluate(() => JSON.parse(window.eagentReplayView(0)).result);
  assert.equal(replayed.price_basis, 'catalog_at_capture');
  assert.ok(expected.cost_usd > 0, 'pricing fixture did not exercise a known model');
  assert.equal(replayed.cost_usd, expected.cost_usd, 'new viewer repriced saved usage with its own catalog');
  assert.equal(replayed.events, expected.events);
  assert.equal(replayed.duration_s, 30);
  const earlier = await page.evaluate(() => JSON.parse(window.eagentReplayView(2)).result);
  assert.equal(earlier.duration_s, 10, 'historical replay included elapsed time after the selected event');
  assert.deepEqual(errors, []);
  console.log('PASS actual offline Go WebAssembly replay preserves captured pricing across a changed viewer catalog');
} finally {
  await browser?.close();
  if (server) await new Promise(resolve => server.close(resolve));
  await rm(temporary, { recursive: true, force: true });
}
