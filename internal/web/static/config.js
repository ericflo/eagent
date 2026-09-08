/* eagent configuration editor. Loaded after app.js; uses its helpers via window.__eagent. */
(() => {
'use strict';
const X = window.__eagent;
const {h, api, toast, showModal, closeModal, k, money, $} = X;

const ACTORS = [
  {key: 'orchestrator', name: 'Orchestrator', role: 'Plans the work, delegates tasks, and decides when it is done. It reads every result, so a stronger model pays off here.'},
  {key: 'task', name: 'Task worker', role: 'Does one task at a time: reads, edits, runs commands. Most tokens are spent here, so price matters most.'},
  {key: 'narrator', name: 'Narrator', role: 'Talks to you: acknowledges requests, reports progress, asks questions. It speaks often, so keep it fast.'},
];
const EFFORT_HELP = {
  none: 'No reasoning parameter is sent. The model answers directly; fastest and cheapest.',
  minimal: 'Barely any thinking before answering.',
  low: 'A little thinking. Good for narration and routine tasks.',
  medium: 'Balanced. The usual choice for task workers.',
  high: 'Thinks at length. Slower and pricier; good for planning.',
  xhigh: 'Very long thinking. Only worth it for hard problems.',
  max: 'The model thinks as long as it wants.',
};
const LABELS = {
  '/task_concurrency': 'task concurrency', '/max_task_turns': 'max task calls', '/max_orchestrator_calls_per_turn': 'max orchestrator calls per turn',
  '/narrator_tick_seconds': 'narrator tick', '/narrator_quiet_seconds': 'narrator quiet limit', '/rollover_tokens': 'fresh-context threshold',
  '/bash_wait_seconds': 'command wait', '/bash_timeout_seconds': 'command timeout', '/tool_output_max_chars': 'tool output limit',
  '/persona': 'narrator voice', '/allow_outside_project': 'allow edits outside the project', '/finalechat/enabled': 'phone mirror',
  '/finalechat/question_timeout_seconds': 'phone question timeout', '/finalechat/mirror_input': 'mirror your messages to the phone',
  '/finalechat/agent': 'phone agent name', '/finalechat/token_env': 'phone token variable', '/finalechat/base_url': 'phone service address',
};
const ACTOR_LABEL = {orchestrator: 'orchestrator', task: 'task worker', narrator: 'narrator'};
const FIELD_LABEL = {model: 'model', protocol: 'protocol', base_url: 'provider', api_key_env: 'key variable', reasoning_effort: 'effort', max_tokens: 'max output tokens', context_tokens: 'context window', replay_reasoning: 'replay reasoning', fallback: 'fallback'};

let E = null;

// ---- state -------------------------------------------------------------------------------
const clone = v => JSON.parse(JSON.stringify(v));
function strip(cfg) { const c = clone(cfg); delete c.name; delete c.description; delete c.instructions; delete c.preset; delete c.default_config; if (!c.finalechat) c.finalechat = {}; return c; }
function flatten(v, prefix = '', out = {}) {
  if (v && typeof v === 'object' && !Array.isArray(v)) { for (const key of Object.keys(v)) flatten(v[key], prefix + '/' + key, out); }
  else out[prefix] = JSON.stringify(v);
  return out;
}
function changes() {
  const a = flatten(E.loaded), b = flatten(E.cfg), ptrs = new Set([...Object.keys(a), ...Object.keys(b)]);
  const out = [];
  for (const p of ptrs) if (a[p] !== b[p]) out.push(p);
  if (E.base !== E.loadedBase) out.push('/preset');
  return out.sort();
}
function label(ptr) {
  if (ptr === '/preset') return 'base preset';
  if (LABELS[ptr]) return LABELS[ptr];
  const m = ptr.match(/^\/(orchestrator|task|narrator)\/(fallback\/)?(\w+)/);
  if (m) return `${ACTOR_LABEL[m[1]]} ${m[2] ? 'fallback ' : ''}${FIELD_LABEL[m[3]] || m[3].replace(/_/g, ' ')}`;
  return ptr.slice(1).replace(/[/_]/g, ' ');
}
function groupChanges(ptrs) {
  // Collapse one route's many fields into one item so the summary reads naturally.
  const seen = new Set(), out = [];
  for (const p of ptrs) {
    const m = p.match(/^\/(orchestrator|task|narrator)\/(fallback)?/);
    const key = m ? `/${m[1]}${m[2] ? '/fallback' : ''}` : p;
    if (seen.has(key)) continue;
    seen.add(key);
    if (m) {
      const who = `${ACTOR_LABEL[m[1]]}${m[2] ? ' fallback' : ''}`;
      const before = m[2] ? E.loaded[m[1]].fallback : E.loaded[m[1]], after = m[2] ? E.cfg[m[1]].fallback : E.cfg[m[1]];
      if (!before && after) { out.push(`${who} added`); continue; }
      if (before && !after) { out.push(`${who} removed`); continue; }
      let fields = [...new Set(ptrs.filter(q => q.startsWith(key + '/') && !(q.slice(key.length + 1).includes('/'))).map(q => q.split('/').pop()))];
      if (fields.includes('model')) fields = fields.filter(f => !['protocol', 'base_url', 'api_key_env', 'context_tokens'].includes(f)); // implied by the model
      out.push(`${who} ${fields.map(f => FIELD_LABEL[f] || f.replace(/_/g, ' ')).join(', ')}`);
    }
    else out.push(label(p));
  }
  return out;
}
function source(ptr) {
  const s = E.sources[ptr];
  if (!s) { // a parent may carry it (e.g. the whole actor came from one layer)
    const parts = ptr.split('/'); while (parts.length > 2) { parts.pop(); const ps = E.sources[parts.join('/')]; if (ps) return ps; }
    return '';
  }
  return s;
}
function sourceText(s) {
  if (!s) return '';
  if (s === 'default') return 'built-in default';
  if (s === 'file') return 'project file';
  if (s.startsWith('preset:')) return 'preset ' + s.slice(7);
  if (s.startsWith('bundle:')) return 'bundle ' + s.slice(7);
  if (s.startsWith('env:')) return '$' + s.slice(4) + ' in this shell';
  return s;
}
function envVar(ptr) { const s = source(ptr); return s.startsWith('env:') ? s.slice(4) : ''; }
function lockedField(ptr) { return X.canEdit ? !X.canEdit(ptr) : !!envVar(ptr); }
function canAction(operation) { return X.canAct ? X.canAct(operation) : true; }
function edited(ptr) { const a = flatten(E.loaded), b = flatten(E.cfg); return a[ptr] !== b[ptr]; }
function srcNode(ptr) {
  const env = envVar(ptr);
  if (env) return h('span', {class: 'src env', title: 'Set by an environment variable in the shell that started eagent. The file cannot override it.'}, `set by $${env}`);
  if (X.field && lockedField(ptr)) return h('span', {class: 'src'}, X.field(ptr)?.locked_reason || 'Read only in this snapshot');
  if (edited(ptr)) return h('span', {class: 'src edited'}, 'edited');
  const s = sourceText(source(ptr));
  return s ? h('span', {class: 'src', title: 'Where the current value comes from'}, s) : null;
}
function problemsFor(ptr) { return (E.problems || []).filter(p => p.path === ptr); }
function problemNodes(ptr) { return problemsFor(ptr).map(p => h('div', {class: 'problem ' + p.severity}, p.message)); }

// ---- catalog helpers -----------------------------------------------------------------------
const trimSlash = u => String(u || '').replace(/\/+$/, '');
function providerOf(baseURL) { return E.cat.providers.find(p => trimSlash(p.base_url) === trimSlash(baseURL)); }
function lookup(a) { const p = providerOf(a.base_url); return p ? E.cat.models.find(m => m.provider === p.id && m.id === a.model) : null; }
function protocolOf(m) { const p = E.cat.providers.find(x => x.id === m.provider); return m.protocol || (p && p.protocol) || 'openai-chat'; }
function effortsFor(a) { const m = lookup(a); if (m && m.efforts && m.efforts.length) return {list: m.efforts, checked: true}; return {list: E.cat.effort_order, checked: false}; }
function keyPresent(env) { return !!E.c.keys[env]; }
function priceLine(m) {
  if (!m) return 'not in the catalog: sessions will show as unpriced';
  const parts = [];
  if (m.price) parts.push(`$${trimNum(m.price.in)} in · $${trimNum(m.price.out)} out per M tokens${m.price.nominal ? ' (list price)' : ''}`);
  else parts.push('price unknown');
  parts.push(`${ctx(m.context)} context`);
  if (m.vision) parts.push('sees images');
  return parts.join(' · ');
}
const ctx = n => n >= 1e6 ? `${(n / 1e6).toFixed(n % 1e6 ? 1 : 0)}M` : `${Math.round(n / 1000)}k`;
const trimNum = n => n >= 10 ? n.toFixed(0) : n >= 1 ? n.toFixed(2).replace(/\.?0+$/, '') : n.toFixed(3).replace(/\.?0+$/, '');
function checkFor(a, effort) { return E.cat.checks[`${trimSlash(a.base_url)}|${a.model}|${effort}`]; }
function setModel(a, m) {
  const p = E.cat.providers.find(x => x.id === m.provider);
  a.base_url = p.base_url; a.protocol = protocolOf(m); a.api_key_env = p.key_env; a.model = m.id;
  if (m.context) a.context_tokens = m.context;
  if (m.efforts && m.efforts.length && !m.efforts.includes(a.reasoning_effort || 'none')) {
    a.reasoning_effort = m.efforts.includes('medium') ? 'medium' : m.efforts[Math.floor(m.efforts.length / 2)];
  }
  if (a.protocol !== 'openai-responses') delete a.replay_reasoning;
}
function sentFor(a) {
  const e = a.reasoning_effort || '';
  if (a.protocol === 'openai-responses') return !e || e === 'none' ? 'no reasoning parameter is sent' : `"reasoning": {"effort": "${e}"}`;
  if (a.protocol === 'anthropic') return !e || e === 'none' ? 'no thinking block is requested' : `"thinking": {"type": "adaptive"}, "output_config": {"effort": "${e}"}`;
  return !e ? 'no reasoning_effort is sent; the provider default applies' : `"reasoning_effort": "${e}"`;
}

// ---- page -------------------------------------------------------------------------------------
async function render() {
  X.closeStream(); X.S.detail = null; X.renderLive();
  const main = $('#main');
  if (!E) {
    main.replaceChildren(h('div', {class: 'empty'}, h('h2', null, 'Configuration'), h('p', {class: 'sub'}, 'Loading…')));
    try { await load(); } catch (e) { main.replaceChildren(h('div', {class: 'empty'}, h('h2', null, 'Configuration unavailable'), e.message)); return; }
  }
  draw();
}
async function load() {
  const [c, cat] = await Promise.all([api('/api/config'), api('/api/catalog')]);
  const res = c.resolution;
  // The base is what the file itself builds on. A preset the server was
  // started with (or EAGENT_PRESET) shapes the values shown, but saving must
  // not silently rewrite the file's own preset to it.
  const fileBase = res.file.exists && res.file.preset ? res.file.preset : (res.preset || '');
  E = Object.assign(E || {}, {c, cat, sources: res.sources || {}, cfg: strip(res.effective), loaded: strip(res.effective), base: fileBase, loadedBase: fileBase, etag: res.file.etag || '', rawDraft: undefined, mode: E && E.mode || 'overlay', problems: [], tests: E && E.tests || {}, open: E && E.open || {}});
}
function draw() {
  const main = $('#main');
  const old = main.querySelector('.pane');
  const y = old ? old.scrollTop : 0;
  const pane = h('div', {class: 'pane cfgpage'});
  const body = h('div', {class: 'cfg-body'});
  if (!X.threadControls) body.append(header());
  const rz = E.c.resolution;
  const fileBroken = !!rz.file.parse_error || (rz.load_error && rz.file.path && rz.load_error.startsWith(rz.file.path));
  if (fileBroken) body.append(repairBanner());
  else if (rz.load_error) body.append(h('div', {class: 'banner warn'}, h('b', null, 'This configuration cannot start a session as it stands.'), h('div', {class: 'sub'}, rz.load_error)));
  if (X.threadControls) {
    const tab = E.tab || 'Models';
    body.append(h('nav', {class:'settings-tabs', 'aria-label':'Settings categories'}, ...['Models','Behavior','Prompts','Advanced'].map(name => h('button', {'aria-pressed':String(tab===name), onclick:()=>{E.tab=name;draw();}}, name))));
    body.append(h('div',{class:'settings-intro'},h('h2',null,tab==='Models'?'Choose how your agent thinks':tab==='Behavior'?'Make it work your way':tab==='Prompts'?'Give your agent direction':'Fine-tune your setup'),h('p',null,tab==='Models'?'Pick a model and reasoning effort for each role.':tab==='Behavior'?'Adjust session limits, narration, and phone updates.':tab==='Prompts'?'Customize the instructions behind each role.':'Presets, saved configurations, and detailed overrides.')));
    if(tab==='Models') body.append(h('div',{class:'routes'},...ACTORS.map(routeCard)));
    if(tab==='Behavior') body.append(sessionCard(),narratorCard(),phoneCard());
    if(tab==='Prompts') body.append(promptsCard());
    if(tab==='Advanced') body.append(quickStart(),advancedCard());
  } else {
  body.append(quickStart());
  body.append(h('div', {class: 'routes'}, ...ACTORS.map(routeCard)));
  body.append(h('div', {class: 'grid2'}, sessionCard(), narratorCard()));
  body.append(h('div', {class: 'grid2'}, phoneCard(), promptsCard()));
  body.append(advancedCard());
  }
  // The bar is a direct child of the scroller with no padding between them,
  // so sticky positioning can bring it right to the bottom edge.
  pane.append(body); if(!X.threadControls) pane.append(saveBar());
  main.replaceChildren(pane);
  pane.scrollTop = y;
}
function rerender() { if (X.remote) keepPinned(); X.formChanged?.(); draw(); }

function header() {
  const res = E.c.resolution, f = res.file;
  const bits = [];
  if (f.exists) bits.push(h('span', null, 'Saved in ', h('code', {class: 'inline'}, shortPath(f.path))));
  else bits.push(h('span', null, 'Nothing saved yet; the project uses ', E.base ? `preset ${E.base}` : 'the built-in defaults'));
  if (f.exists && E.loadedBase) bits.push(h('span', null, 'builds on preset ', h('code', {class: 'inline'}, E.loadedBase)));
  if (res.active.kind === 'bundle') bits.push(h('span', {class: 'warn-text'}, `the server was started with --config ${res.active.name}; this page edits the project file, which that bundle overrides`));
  if (res.preset && res.preset !== E.loadedBase) bits.push(h('span', {class: 'warn-text'}, `the values shown come from preset ${res.preset}, which the server was started with; the file builds on ${E.loadedBase || 'the defaults'}`));
  const envs = Object.entries(E.sources).filter(([, s]) => s.startsWith('env:'));
  if (envs.length) bits.push(h('span', {class: 'warn-text', title: envs.map(([p, s]) => `${label(p)} ← $${s.slice(4)}`).join('\n')}, `${envs.length} value${envs.length > 1 ? 's' : ''} pinned by environment variables`));
  if (f.unknown_keys && f.unknown_keys.length) bits.push(h('span', {title: f.unknown_keys.join(', ')}, `${f.unknown_keys.length} hand-written key${f.unknown_keys.length > 1 ? 's' : ''} kept as is`));
  return h('div', {class: 'cfg-head'}, h('div', null, h('h2', null, 'Configuration'), h('p', {class: 'sub'}, 'Which models do the work, how hard they think, and how the session behaves. Changes apply to sessions started or resumed after you save.')),
    h('div', {class: 'where'}, ...bits.flatMap((b, i) => i ? [h('span', {class: 'dot-sep'}, '·'), b] : [b])));
}
const shortPath = p => { const i = p.indexOf('.agents/'); return i >= 0 ? p.slice(i) : p; };

function repairBanner() {
  const res = E.c.resolution;
  const ta = h('textarea', {class: 'code', style: 'width:100%;min-height:180px;margin-top:8px'}, res.file.raw || '');
  const status = h('span', {class: 'sub'});
  return h('div', {class: 'banner bad'}, h('b', null, 'The project file could not be loaded, so sessions are using the last layer that did.'),
    h('div', {class: 'sub'}, res.load_error), ta, h('div', {class: 'row'}, status, h('span', {style: 'flex:1'}),
      h('button', {onclick: async () => { try { await api('/api/config/raw', {method: 'PUT', body: JSON.stringify({raw: '{\n  "preset": "glm"\n}\n', if_match: E.etag})}); toast('file reset'); E = null; render(); } catch (e) { status.textContent = e.message; } }}, 'Start over with defaults'),
      h('button', {class: 'primary', onclick: async () => { try { await api('/api/config/raw', {method: 'PUT', body: JSON.stringify({raw: ta.value, if_match: E.etag})}); toast('file repaired'); E = null; render(); } catch (e) { status.textContent = e.message; } }}, 'Save repaired file')));
}

function quickStart() {
  const chips = E.c.presets.map(p => chip(p, 'preset'));
  const bchips = E.c.bundles.map(b => chip(b, 'bundle'));
  return h('div', {class: 'card quick'}, h('h3', null, 'Start from', h('span', {class: 'grow'}), h('span', {class: 'sub'}, 'Loads a setup into the form; nothing is saved until you press Save.')),
    h('div', {class: 'chips'}, ...chips, h('button', {class: 'chip-btn', title: 'The built-in defaults, before any preset', onclick: () => loadInto('', null)}, 'defaults')),
    bchips.length ? h('div', {class: 'chips bundles'}, h('span', {class: 'sub'}, 'bundles'), ...bchips) : null,
    X.remote ? bundleActions() : null);
}
function bundleActions() {
  const current = E.c.resolution.file.default_config || '';
  const selected = h('select', {'aria-label': 'Default named configuration', disabled: lockedField('/default_config')}, h('option', {value: '', selected: !current}, 'project defaults'), ...E.c.bundles.map(b => h('option', {value: b.name, selected: b.name === current}, b.name)));
  const removal = h('select', {'aria-label': 'Named configuration to delete'}, ...E.c.bundles.map(b => h('option', {value: b.name}, b.name)));
  const review = async (path, body) => { try { await api(path, {method: 'POST', body: JSON.stringify(body)}); toast('Ready for review in FinaleChat. No configuration has changed yet.'); } catch (error) { toast(error.message, 'bad'); } };
  return h('details', null, h('summary', null, 'Named configuration actions'),
    h('p', {class: 'sub'}, 'Selecting a default can change model routes and permissions for future sessions. Deleting a configuration never restarts a running session.'),
    h('div', {class: 'row'}, selected, h('button', {disabled: lockedField('/default_config'), onclick: () => review('/api/config/default-bundle', {name: selected.value})}, 'Review default configuration')),
    h('div', {class: 'row'}, removal, h('button', {disabled: !canAction('bundle.delete') || !E.c.bundles.length, onclick: () => review('/api/config/delete-bundle', {name: removal.value})}, 'Review configuration deletion')));
}
function chip(b, kind) {
  const active = kind === 'preset' ? E.base === b.name && !changes().filter(p => p !== '/preset').length : false;
  return h('button', {class: 'chip-btn' + (active ? ' on' : '') + (b.invalid ? ' bad' : ''), title: b.invalid ? 'invalid: ' + b.invalid : `${b.description || ''}\n${b.models || ''}`.trim(), onclick: () => loadInto(b.name, kind)}, b.name);
}
async function loadInto(name, kind) {
  try {
    if (!name) { E.cfg = strip(E.c.defaults); E.base = ''; }
    else { const r = await api(`/api/config/presets/${encodeURIComponent(name)}?kind=${kind || ''}`); E.cfg = strip(r.config); E.base = kind === 'preset' ? name : (r.config.preset || ''); }
    keepPinned();
    E.problems = []; rerender();
    toast(name ? `loaded ${name} into the form` : 'defaults loaded into the form');
  } catch (e) { toast(e.message, 'bad'); }
}

// keepPinned copies every environment-pinned value from the loaded
// configuration into the working copy: the file cannot change those, so a
// loaded preset must not appear to.
function keepPinned() {
  const candidates = new Set([...Object.keys(E.sources), ...Object.keys(flatten(E.loaded)), ...Object.keys(flatten(E.cfg))]);
  for (const ptr of candidates) {
    const s = source(ptr);
    if (!s.startsWith('env:') && !(X.canEdit && !X.canEdit(ptr))) continue;
    const segs = ptr.split('/').slice(1);
    let from = E.loaded, to = E.cfg;
    for (let i = 0; i < segs.length - 1; i++) { from = from?.[segs[i]]; if (to[segs[i]] == null || typeof to[segs[i]] !== 'object') to[segs[i]] = {}; to = to[segs[i]]; }
    const last = segs[segs.length - 1];
    if (from && from[last] !== undefined) to[last] = clone(from[last]); else if (to) delete to[last];
  }
  // Restoring absent locked fallback fields must not create an empty route.
  for (const actor of ACTORS) if (E.cfg[actor.key]?.fallback && !Object.keys(E.cfg[actor.key].fallback).length) delete E.cfg[actor.key].fallback;
  if (X.canEdit && !X.canEdit('/preset')) E.base = E.loadedBase;
}

// ---- route cards ---------------------------------------------------------------------------
function routeCard(actor) {
  const a = E.cfg[actor.key];
  const ptr = f => `/${actor.key}/${f}`;
  const m = lookup(a);
  const prov = providerOf(a.base_url);
  const locked = f => lockedField(ptr(f));
  const card = h('div', {class: 'card route ' + actor.key});
  card.append(h('div', {class: 'rhead'}, h('h3', null, h('span', {class: 'swatch'}), actor.name), h('p', {class: 'sub role'}, X.threadControls ? ({orchestrator:'Plans the work and coordinates the team.',task:'Handles individual tasks and tools.',narrator:'Keeps you informed as work progresses.'}[actor.key]) : actor.role)));

  // Model
  const pickBtn = h('button', {class: 'pick', disabled: locked('model') || locked('base_url'), onclick: () => openPicker(card, a, actor.key, false)},
    h('span', {class: 'pk'}, h('span', {class: 'kdot' + (keyPresent(a.api_key_env) ? ' on' : '')}), h('b', null, m ? m.label : a.model), h('span', {class: 'sub'}, prov ? prov.label : a.base_url.replace(/^https?:\/\//, ''))), h('span', {class: 'caret'}, '▾'));
  const same = actor.key !== 'orchestrator' && !locked('model') ? h('button', {class: 'link', title: 'Copy the orchestrator\'s provider, model, and effort', onclick: () => { const o = E.cfg.orchestrator; Object.assign(a, {base_url: o.base_url, protocol: o.protocol, api_key_env: o.api_key_env, model: o.model, reasoning_effort: o.reasoning_effort, context_tokens: o.context_tokens}); rerender(); }}, 'same as orchestrator') : null;
  card.append(field('Model', [pickBtn], [h('span', {class: 'notes-row'}, srcNode(ptr('model')), same), ...problemNodes(ptr('model'))]));
  card.append(h('div', {class: 'sub price'}, priceLine(m), !keyPresent(a.api_key_env) ? h('span', {class: 'warn-text'}, ` · $${a.api_key_env} is not set in this shell`) : null));

  // Effort
  const {list, checked} = effortsFor(a);
  const cur = a.reasoning_effort || (a.protocol === 'openai-chat' ? '' : 'none');
  const anyCheck = list.some(e => checkFor(a, e));
  const seg = h('div', {class: 'seg' + (checked ? '' : ' unchecked') + (anyCheck ? ' annotated' : '')});
  for (const e of list) {
    const chk = checkFor(a, e);
    const on = cur === e || (e === 'none' && cur === '');
    seg.append(h('button', {class: (on ? 'on' : '') + (chk ? ' ' + chk.status : ''), disabled: locked('reasoning_effort'), title: EFFORT_HELP[e] + (chk ? `\nLast test: ${checkText(chk, true)}` : ''), onclick: () => { a.reasoning_effort = e === 'none' && a.protocol === 'openai-chat' ? '' : e; rerender(); }},
      h('span', {class: 'e'}, e), anyCheck ? h('span', {class: 'ann'}, chk ? checkText(chk) : '') : null));
  }
  const testing = E.tests[actor.key] && E.tests[actor.key].busy;
  const tools = h('div', {class: 'testrow'},
    h('button', {class: 'small', disabled: testing || !canAction('route.test'), onclick: () => runTest(actor.key, null)}, testing ? 'testing…' : X.remote ? 'Review route test' : 'Test this route'),
    X.remote ? null : h('button', {class: 'small', disabled: testing, title: 'Send one tool call at each effort and note the latency and reasoning tokens. Each call uses provider billing.', onclick: () => runAll(actor.key)}, 'Try every effort'),
    h('span', {class: 'sub grow'}, checked ? '' : 'Efforts are not verified for this model; the annotations come from your tests.'));
  card.append(field('Reasoning effort', [seg, tools], [srcNode(ptr('reasoning_effort')), h('span', {class: 'src'}, 'sends ', h('code', {class: 'inline'}, sentFor(a))), ...problemNodes(ptr('reasoning_effort'))]));
  const t = E.tests[actor.key];
  if (t && t.result) card.append(resultLine(t.result));

  // Fallback
  if(!X.threadControls) card.append(fallbackLine(card, a, actor.key));

  // Advanced
  const open = !!E.open[actor.key];
  const det = h('details', {open}, h('summary', {onclick: () => { E.open[actor.key] = !open; }}, 'Advanced'));
  const num = (f, lbl, help, attrs) => field(lbl, [h('input', Object.assign({type: 'number', value: a[f] || '', disabled: locked(f), onchange: ev => { const v = parseInt(ev.target.value, 10); if (isNaN(v)) delete a[f]; else a[f] = v; rerender(); }}, attrs || {}))], [srcNode(ptr(f)), help ? h('span', {class: 'src'}, help) : null, ...problemNodes(ptr(f))]);
  if(X.threadControls) det.append(fallbackLine(card, a, actor.key));
  det.append(h('div', {class: 'adv'},
    num('max_tokens', 'Max output tokens', 'per call; reasoning counts toward it on most providers'),
    num('context_tokens', 'Context window', 'what the harness assumes when deciding to start a fresh context'),
    a.protocol === 'openai-responses' ? field('Replay reasoning', [h('label', {class: 'check'}, h('input', {type: 'checkbox', checked: a.replay_reasoning !== false, disabled: locked('replay_reasoning'), onchange: ev => { if (ev.target.checked) delete a.replay_reasoning; else a.replay_reasoning = false; rerender(); }}), 'keep the model\'s reasoning items across turns (recommended; keeps the prompt cache warm)')], [srcNode(ptr('replay_reasoning'))]) : null,
    field('Protocol', [h('select', {disabled: locked('protocol'), onchange: ev => { a.protocol = ev.target.value; if (a.protocol !== 'openai-responses') delete a.replay_reasoning; rerender(); }}, ...['openai-chat', 'openai-responses', 'anthropic'].map(p => h('option', {value: p, selected: a.protocol === p}, `${p} · ${E.cat.transports[p] || ''}`)))], [srcNode(ptr('protocol')), ...problemNodes(ptr('protocol'))]),
    field('Provider address', [h('select', {disabled: locked('base_url'), onchange: ev => { a.base_url = ev.target.value; const p = providerOf(a.base_url); if (p) { a.api_key_env = p.key_env; if (!lookup(a)) a.protocol = p.protocol; } rerender(); }}, ...providerOptions(a.base_url))], [srcNode(ptr('base_url')), h('span', {class: 'src'}, 'Custom addresses are added by editing the file; the browser can only choose catalog providers.'), ...problemNodes(ptr('base_url'))]),
    field('Key variable', [h('select', {disabled: locked('api_key_env'), onchange: ev => { a.api_key_env = ev.target.value; rerender(); }}, ...keyOptions(a.api_key_env))], [srcNode(ptr('api_key_env')), h('span', {class: 'src'}, 'Only the variable name is stored; keys never leave your shell.'), ...problemNodes(ptr('api_key_env'))]),
  ));
  card.append(det);
  return card;
}
function providerOptions(current) {
  const opts = E.cat.providers.map(p => h('option', {value: p.base_url, selected: trimSlash(p.base_url) === trimSlash(current)}, `${p.label} · ${p.base_url.replace(/^https?:\/\//, '')}`));
  if (!providerOf(current)) opts.unshift(h('option', {value: current, selected: true}, `${current} (custom)`));
  return opts;
}
function keyOptions(current) {
  const names = Object.keys(E.c.keys).sort();
  if (current && !names.includes(current)) names.unshift(current);
  return names.map(n => h('option', {value: n, selected: n === current}, `${n}${E.c.keys[n] ? ' · set' : ' · not set'}`));
}
function field(lbl, controls, notes) {
  return h('div', {class: 'field'}, h('label', null, lbl), h('div', {class: 'ctl'}, ...controls.filter(Boolean)), h('div', {class: 'notes'}, ...notes.filter(Boolean)));
}
function checkText(c, long) {
  if (!c) return '';
  if (c.status === 'tool_call') return long ? `worked in ${(c.ms / 1000).toFixed(1)}s, ${c.reasoning ? k(c.reasoning) + ' reasoning tokens' : 'no reasoning tokens reported'} (${c.at.slice(0, 10)})` : `${(c.ms / 1000).toFixed(1)}s${c.reasoning ? ' · ' + k(c.reasoning) + 'rt' : ''}`;
  if (c.status === 'no_tool_call') return long ? `answered without calling the tool (${c.at.slice(0, 10)})` : 'no tool';
  if (c.status === 'timeout') return long ? 'timed out after two minutes' : 'timeout';
  if (c.status === 'no_key') return long ? 'no key in this shell' : 'no key';
  return long ? `failed: ${c.error || ''}` : 'failed';
}
function resultLine(r) {
  if (r.staged) return h('p', {class: 'sub'}, 'Route test staged for review. Submit it in FinaleChat to make the provider call.');
  const ok = r.status === 'tool_call';
  const parts = [];
  if (X.remote && r.model) parts.push(`${r.model} · ${r.route || 'primary'} · ${r.effort || 'provider default'}`);
  if (ok) parts.push(`tool call in ${(r.ms / 1000).toFixed(1)}s`);
  else if (r.status === 'no_tool_call') parts.push(`answered with text instead of a tool call in ${(r.ms / 1000).toFixed(1)}s`);
  else if (r.status === 'timeout') parts.push('no answer within two minutes');
  else if (r.status === 'no_key') parts.push(r.error);
  else parts.push(`failed${r.http_status ? ' (' + r.http_status + ')' : ''}: ${r.error}`);
  if (r.usage && r.usage.input) parts.push(`${k(r.usage.input)} in / ${k(r.usage.output)} out${r.usage.reasoning ? ' / ' + k(r.usage.reasoning) + ' reasoning' : ''}`);
  if (r.priced) parts.push(`~${money(r.est_cost_usd)}`);
  return h('div', {class: 'testresult ' + (ok ? 'ok' : r.status === 'no_tool_call' ? 'warn' : 'bad')}, h('span', {class: 'mark'}, ok ? '✓' : r.status === 'no_tool_call' ? '△' : '✕'), h('span', null, `${r.route} · effort ${r.effort || 'default'} · ${parts.join(' · ')}`), r.text ? h('span', {class: 'sub'}, ` “${r.text}”`) : null);
}
async function runTest(key, effort, route) {
  const a = E.cfg[key];
  const testKey = route === 'fallback' ? key + ':fb' : key;
  E.tests[testKey] = {busy: true, result: null}; rerender();
  try {
    const body = {actor: a, actor_key: key, route: route || 'primary'};
    if (effort !== null && effort !== undefined) body.effort = effort;
    const r = await api('/api/config/test', {method: 'POST', body: JSON.stringify(body)});
    if (r.staged) { E.tests[testKey] = {busy: false, result: r}; return r; }
    E.cat.checks[`${trimSlash(r.base_url)}|${r.model}|${r.effort}`] = {status: r.status, ms: r.ms, output: r.usage.output, reasoning: r.usage.reasoning, error: r.error, at: r.at};
    E.tests[testKey] = {busy: false, result: r};
    return r;
  } catch (e) { E.tests[testKey] = {busy: false, result: {route: route || 'primary', effort: effort ?? a.reasoning_effort, status: 'failed', error: e.message, usage: {}}}; }
  finally { rerender(); }
}
async function runAll(key) {
  const a = E.cfg[key];
  const {list} = effortsFor(a);
  for (const e of list) {
    E.tests[key] = {busy: true, result: E.tests[key] && E.tests[key].result}; rerender();
    const ro = e === 'none' && a.protocol === 'openai-chat' ? '' : e;
    try {
      const r = await api('/api/config/test', {method: 'POST', body: JSON.stringify({actor: a, route: 'primary', effort: ro})});
      E.cat.checks[`${trimSlash(r.base_url)}|${r.model}|${r.effort}`] = {status: r.status, ms: r.ms, output: r.usage.output, reasoning: r.usage.reasoning, error: r.error, at: r.at};
      E.tests[key] = {busy: true, result: r};
    } catch (err) { E.tests[key] = {busy: true, result: {route: 'primary', effort: e, status: 'failed', error: err.message, usage: {}}}; }
    rerender();
  }
  E.tests[key].busy = false; rerender();
}

function fallbackLine(card, a, key) {
  const fb = a.fallback;
  const locked = lockedField(`/${key}/fallback/model`) || lockedField(`/${key}/fallback/base_url`);
  if (!fb) {
    const alt = suggestFallback(a);
    return h('div', {class: 'fb'}, h('span', {class: 'sub'}, 'Fallback: none. If this route fails, the call fails.'),
      alt ? h('button', {class: 'ghost small', disabled: locked, onclick: () => { a.fallback = alt; rerender(); }}, `add ${providerLabel(alt.base_url)} fallback`) : null,
      h('button', {class: 'ghost small', disabled: locked, onclick: () => openPicker(card, a, key, true)}, 'choose…'));
  }
  const fm = lookup(fb);
  const inert = trimSlash(fb.base_url) === trimSlash(a.base_url) && fb.model === a.model;
  const sameProv = !inert && trimSlash(fb.base_url) === trimSlash(a.base_url);
  const {list} = effortsFor(fb);
  const sel = h('select', {class: 'mini', disabled: lockedField(`/${key}/fallback/reasoning_effort`), onchange: ev => { fb.reasoning_effort = ev.target.value === 'none' && fb.protocol === 'openai-chat' ? '' : ev.target.value; rerender(); }}, ...list.map(e => h('option', {value: e, selected: (fb.reasoning_effort || (fb.protocol === 'openai-chat' ? '' : 'none')) === e || (e === 'none' && !fb.reasoning_effort)}, e)));
  const t = E.tests[key + ':fb'];
  return h('div', {class: 'fb has'}, h('div', {class: 'row'}, h('span', {class: 'sub'}, 'Fallback'),
      h('button', {class: 'pick mini', disabled: locked, onclick: () => openPicker(card, a, key, true)}, h('span', {class: 'kdot' + (keyPresent(fb.api_key_env) ? ' on' : '')}), h('b', null, fm ? fm.label : fb.model), h('span', {class: 'sub'}, providerLabel(fb.base_url)), h('span', {class: 'caret'}, '▾')),
      h('span', {class: 'sub'}, 'effort'), sel,
      h('button', {class: 'ghost small', disabled: t && t.busy || !canAction('route.test'), onclick: () => runTest(key, null, 'fallback').then(() => {})}, X.remote ? 'review test' : 'test'),
      h('button', {class: 'ghost small', disabled: locked, onclick: () => { delete a.fallback; rerender(); }}, 'remove')),
    t && t.result ? resultLine(t.result) : null,
    inert ? h('div', {class: 'problem error'}, 'This fallback is the same route as the primary, so it cannot help. Pick another provider or model.') : null,
    sameProv ? h('div', {class: 'problem warning'}, 'Same provider as the primary: this covers a model outage, not a provider outage.') : null,
    ...problemNodes(`/${key}/fallback/model`), ...problemNodes(`/${key}/fallback/base_url`), ...problemNodes(`/${key}/fallback/reasoning_effort`), ...problemNodes(`/${key}/fallback/fallback`));
}
function providerLabel(u) { const p = providerOf(u); return p ? p.label : String(u || '').replace(/^https?:\/\//, ''); }
function suggestFallback(a) {
  // The same family at another provider whose key is present, else nothing.
  const m = lookup(a);
  if (!m) return null;
  const alts = E.cat.models.filter(x => x.family === m.family && x.provider !== m.provider);
  const withKey = alts.filter(x => { const p = E.cat.providers.find(pp => pp.id === x.provider); return p && keyPresent(p.key_env); });
  const pick = withKey[0] || null;
  if (!pick) return null;
  const fb = {protocol: '', base_url: '', model: '', api_key_env: '', reasoning_effort: a.reasoning_effort, max_tokens: a.max_tokens};
  setModel(fb, pick);
  return fb;
}

// ---- model picker ------------------------------------------------------------------------------
function openPicker(card, a, key, forFallback) {
  document.querySelectorAll('.picker').forEach(p => p.remove());
  const target = forFallback ? (a.fallback || {protocol: '', base_url: '', model: '', api_key_env: '', reasoning_effort: a.reasoning_effort, max_tokens: a.max_tokens}) : a;
  const input = h('input', {class: 'search', placeholder: 'Search models or providers…', autocomplete: 'off'});
  const list = h('div', {class: 'plist'});
  const pop = h('div', {class: 'picker'}, h('div', {class: 'phead'}, input, h('button', {class: 'ghost small', onclick: close}, 'esc')), list);
  function close() { pop.remove(); document.removeEventListener('mousedown', outside, true); }
  function outside(ev) { if (!pop.contains(ev.target)) close(); }
  function choose(m) { setModel(target, m); if (forFallback) a.fallback = target; close(); rerender(); }
  function fill() {
    const q = input.value.trim().toLowerCase();
    const provOf = id => E.cat.providers.find(p => p.id === id);
    let rows = E.cat.models.map(m => ({m, p: provOf(m.provider)})).filter(({m, p}) => p && (!q || `${m.label} ${m.id} ${m.family} ${p.label} ${p.id} ${(m.note || '')}`.toLowerCase().includes(q)));
    // Keyed providers first, then by family, then by price.
    rows.sort((x, y) => (keyPresent(y.p.key_env) - keyPresent(x.p.key_env)) || x.m.family.localeCompare(y.m.family) || ((x.m.price ? x.m.price.out : 1e9) - (y.m.price ? y.m.price.out : 1e9)));
    list.replaceChildren();
    let lastFam = null, lastKeyed = null;
    for (const {m, p} of rows.slice(0, 80)) {
      const keyed = keyPresent(p.key_env);
      if (lastKeyed !== null && keyed !== lastKeyed) list.append(h('div', {class: 'pgroup dim'}, 'providers without a key in this shell'));
      lastKeyed = keyed;
      if (m.family !== lastFam) { list.append(h('div', {class: 'pgroup'}, m.family)); lastFam = m.family; }
      const sel = trimSlash(p.base_url) === trimSlash(target.base_url) && m.id === target.model;
      list.append(h('button', {class: 'prow' + (sel ? ' on' : '') + (keyed ? '' : ' nokey'), onclick: () => choose(m)},
        h('span', {class: 'kdot' + (keyed ? ' on' : '')}), h('span', {class: 'pl'}, h('span', {class: 'l1'}, h('b', null, m.label), h('span', {class: 'sub'}, ` · ${p.label}${m.protocol && m.protocol !== p.protocol ? ' · ' + m.protocol : ''}`)), m.note ? h('span', {class: 'sub pnote'}, m.note) : null),
        h('span', {class: 'pr'}, m.price ? `$${trimNum(m.price.in)} / $${trimNum(m.price.out)}` : '—', h('span', {class: 'sub'}, ` · ${ctx(m.context)}${m.vision ? ' · vision' : ''}`))));
    }
    if (!rows.length) list.append(h('div', {class: 'sub', style: 'padding:10px'}, 'No catalog model matches. Custom models are added by editing the project file.'));
  }
  input.addEventListener('input', fill);
  input.addEventListener('keydown', ev => { if (ev.key === 'Escape') { ev.stopPropagation(); close(); } if (ev.key === 'Enter') { const first = list.querySelector('.prow'); if (first) first.click(); } });
  fill();
  // Placed in the scrolling pane so it can be wider than its card, and
  // pulled left when the card sits near the right edge.
  const pane = card.closest('.pane');
  const width = Math.min(560, pane.clientWidth - 28);
  let left = card.offsetLeft + 14;
  if (left + width > pane.clientWidth - 14) left = Math.max(14, pane.clientWidth - 14 - width);
  const anchor = card.querySelector('.pick');
  const top = card.offsetTop + (anchor ? anchor.offsetTop + anchor.offsetHeight + 6 : 110);
  pop.style.left = left + 'px'; pop.style.top = top + 'px'; pop.style.width = width + 'px';
  pane.append(pop);
  setTimeout(() => { document.addEventListener('mousedown', outside, true); input.focus(); }, 0);
}

// ---- session, narrator, phone, prompts, advanced -------------------------------------------------
function numField(f, lbl, help, attrs) {
  const ptr = '/' + f;
  const locked = lockedField(ptr);
  return field(lbl, [h('input', Object.assign({type: 'number', value: E.cfg[f] ?? '', disabled: locked, onchange: ev => { const v = parseInt(ev.target.value, 10); if (!isNaN(v)) E.cfg[f] = v; rerender(); }}, attrs || {}))], [srcNode(ptr), help ? h('span', {class: 'src'}, help) : null, ...problemNodes(ptr)]);
}
function sessionCard() {
  return h('div', {class: 'card'}, h('h3', null, 'Session'),
    h('div', {class: 'fields'},
      numField('task_concurrency', 'Task workers at once', 'how many delegated tasks run in parallel', {min: 1, max: 32}),
      numField('max_task_turns', 'Max calls per task', 'a worker that reaches this is stopped and reports what it has', {min: 1}),
      numField('max_orchestrator_calls_per_turn', 'Nudge the planner after', 'consecutive calls in one turn before the harness suggests delegating, waiting, or yielding; a nudge, not a cap', {min: 1}),
      numField('rollover_tokens', 'Fresh-context threshold', 'the orchestrator writes a dossier and starts a fresh context past this many tokens', {min: 20000, step: 10000}),
      numField('bash_wait_seconds', 'Command wait', 'seconds a command may run before it becomes a background handle', {min: 0, max: 300}),
      numField('bash_timeout_seconds', 'Command timeout', 'seconds before a background command is killed', {min: 1}),
      numField('tool_output_max_chars', 'Tool output limit', 'characters of tool output kept per call', {min: 1000, step: 1000}),
      field('Edits outside the project', [h('label', {class: 'check'}, h('input', {type: 'checkbox', disabled: lockedField('/allow_outside_project'), checked: !!E.cfg.allow_outside_project, onchange: ev => { E.cfg.allow_outside_project = ev.target.checked; rerender(); }}), 'allow the agent to read and write files outside the project directory')], [srcNode('/allow_outside_project'), E.cfg.allow_outside_project ? h('span', {class: 'problem warning'}, 'The agent can then change anything your user can. Turn it off when you do not need it.') : null])));
}
function narratorCard() {
  const locked = lockedField('/persona');
  return h('div', {class: 'card'}, h('h3', null, 'Narrator cadence and voice'),
    h('div', {class: 'fields'},
      numField('narrator_tick_seconds', 'Check in every', 'seconds between narrator wake-ups while work runs; it also wakes when you write', {min: 5}),
      numField('narrator_quiet_seconds', 'Must speak after', 'seconds of silence after which the narrator has to say something, even a short hold', {min: 0}),
      field('Voice', [h('textarea', {rows: 4, disabled: locked, placeholder: 'How the narrator should sound. Leave empty for the built-in voice (warm, plain, no catchphrases).', onchange: ev => { E.cfg.persona = ev.target.value; if (!E.cfg.persona) delete E.cfg.persona; rerender(); }}, E.cfg.persona || '')], [srcNode('/persona'), h('span', {class: 'src'}, 'Replaces the persona prompt for new sessions. The full prompts are under Prompts.'), ...problemNodes('/persona')])));
}
function phoneCard() {
  const fc = E.cfg.finalechat || (E.cfg.finalechat = {});
  const ph = E.c.phone;
  const locked = lockedField('/finalechat/enabled');
  const mode = fc.enabled === undefined || fc.enabled === null ? 'auto' : fc.enabled ? 'always' : 'off';
  const seg = h('div', {class: 'seg tri'}, ...[['auto', 'Auto', 'On whenever a Finalechat token is found; silently off otherwise.'], ['always', 'Always', 'Sessions refuse to start without a token, so you never miss a question.'], ['off', 'Off', 'Never mirror, even with a token present.']].map(([v, t, tip]) =>
    h('button', {class: mode === v ? 'on' : '', disabled: locked, title: tip, onclick: () => { if (v === 'auto') delete fc.enabled; else fc.enabled = v === 'always'; rerender(); }}, t)));
  const state = h('div', {class: 'sub'}, h('span', {class: 'badge ' + (ph.state === 'on' ? 'completed' : ph.state === 'disabled' ? 'failed' : '')}, ph.state), ' ', ph.detail);
  const open = !!E.open.phone;
  const det = h('details', {open}, h('summary', {onclick: () => { E.open.phone = !open; }}, 'Advanced'),
    h('div', {class: 'adv'},
      field('Question timeout', [h('input', {type: 'number', disabled: lockedField('/finalechat/question_timeout_seconds'), min: 30, value: fc.question_timeout_seconds || '', placeholder: '3600', onchange: ev => { const v = parseInt(ev.target.value, 10); if (isNaN(v) || v <= 0) delete fc.question_timeout_seconds; else fc.question_timeout_seconds = v; rerender(); }})], [srcNode('/finalechat/question_timeout_seconds'), h('span', {class: 'src'}, 'seconds a phone question stays open; a batch session waits this long for an answer'), ...problemNodes('/finalechat/question_timeout_seconds')]),
      field('Mirror what you type', [h('label', {class: 'check'}, h('input', {type: 'checkbox', disabled: lockedField('/finalechat/mirror_input'), checked: fc.mirror_input !== false, onchange: ev => { if (ev.target.checked) delete fc.mirror_input; else fc.mirror_input = false; rerender(); }}), 'copy your terminal and web messages into the phone thread')], [srcNode('/finalechat/mirror_input')]),
      field('Agent name', [h('input', {class: 'search', disabled: lockedField('/finalechat/agent'), value: fc.agent || '', placeholder: 'eagent', onchange: ev => { fc.agent = ev.target.value.trim(); if (!fc.agent) delete fc.agent; rerender(); }})], [srcNode('/finalechat/agent'), h('span', {class: 'src'}, 'shown next to the thread in the app')]),
      field('Token variable', [h('input', {class: 'search', disabled: lockedField('/finalechat/token_env'), value: fc.token_env || '', placeholder: 'FINALECHAT_TOKEN', onchange: ev => { fc.token_env = ev.target.value.trim(); if (!fc.token_env) delete fc.token_env; rerender(); }})], [srcNode('/finalechat/token_env'), h('span', {class: 'src'}, 'falls back to ~/.config/finalechat/config.json'), ...problemNodes('/finalechat/token_env')]),
      field('Service address', [h('input', {class: 'search', disabled: lockedField('/finalechat/base_url'), value: fc.base_url || '', placeholder: 'https://www.finalechat.com', onchange: ev => { fc.base_url = ev.target.value.trim(); if (!fc.base_url) delete fc.base_url; rerender(); }})], [srcNode('/finalechat/base_url'), h('span', {class: 'src'}, 'New custom service addresses must first be established locally.'), ...problemNodes('/finalechat/base_url')]),
      field('Session archives', [h('label', {class: 'check'}, h('input', {type: 'checkbox', disabled: lockedField('/finalechat/artifacts'), checked: !!fc.artifacts, onchange: ev => { fc.artifacts = ev.target.checked; rerender(); }}), 'publish permanent session websites, including native logs and attachments')], [srcNode('/finalechat/artifacts'), h('span', {class: 'src'}, 'Separate opt-in from chat mirroring. The EAGENT_FINALECHAT=off kill switch disables all outbound integration traffic.')])));
  return h('div', {class: 'card'}, h('h3', null, 'Phone mirror'), h('p', {class: 'sub'}, 'Mirrors the conversation to Finalechat on your phone: every narrator message, every question, and the pictures the agent takes.'),
    field('Mirror', [seg], [srcNode('/finalechat/enabled'), ...problemNodes('/finalechat/enabled')]), state, det);
}
function promptsCard() {
  const HELP = {'ORCHESTRATOR.md': 'how the orchestrator plans, delegates, and finishes', 'TASK-WORKER.md': 'how a worker does one task and reports', 'NARRATOR.md': 'when the narrator speaks and what it never writes', 'PERSONA.md': 'the narrator\'s voice', 'COMPACTION-DOSSIER.md': 'the notes written before a fresh context'};
  return h('div', {class: 'card'}, h('h3', null, 'Prompts'), h('p', {class: 'sub'}, 'The instructions each actor works from. Click one to read or edit it; edits are saved as project overrides.'),
    h('div', {class: 'plist2'}, ...E.c.prompts.map(p => h('button', {class: 'prompt-row', onclick: () => X.promptEditor(p.name)},
      h('code', {class: 'inline'}, p.name), h('span', {class: 'badge' + (p.source === 'built-in' ? '' : ' completed')}, p.source === 'built-in' ? 'built-in' : 'override'), h('span', {class: 'sub'}, HELP[p.name] || '')))));
}
function advancedCard() {
  const res = E.c.resolution;
  const open = !!E.open.adv;
  const det = h('details', {class: 'card adv-card', open}, h('summary', {onclick: () => { E.open.adv = !open; }}, 'Files and raw JSON'));
  const rawTa = h('textarea', {class: 'code', 'aria-label': X.remote ? 'Known saved overrides' : 'Project file JSON', disabled: X.readOnly?.(), style: 'width:100%;min-height:200px', spellcheck: 'false', oninput: ev => { E.rawDraft = ev.target.value; }}, E.rawDraft ?? res.file.raw ?? '');
  const rawStatus = h('span', {class: 'sub'});
  det.append(h('div', {class: 'adv2'},
    h('div', null, h('h4', null, 'Project file'), h('p', {class: 'sub'}, h('code', {class: 'inline'}, res.file.path), res.file.exists ? '' : ' (not written yet)'),
      h('p', {class: 'sub'}, X.remote ? 'Known saved overrides only. Editing this JSON proposes typed changes; unknown local keys and credentials are excluded and preserved by the connector.' : 'What Save writes. Hand edits are fine; keys the editor does not know are kept.'), rawTa,
      h('div', {class: 'row'}, rawStatus, h('span', {style: 'flex:1'}), h('button', {class: 'small', disabled: X.readOnly?.(), onclick: async () => { try { const r = await api('/api/config/raw', {method: 'PUT', body: JSON.stringify({raw: rawTa.value, if_match: E.etag})}); if (r.staged) { rawStatus.textContent = 'Ready for review in FinaleChat. Nothing has been saved yet.'; return; } toast('file written'); E = null; render(); } catch (e) { rawStatus.textContent = e.message; } }}, X.remote ? 'Review override changes' : 'Write file as typed'))),
    h('div', null, h('h4', null, 'Effective configuration'), h('p', {class: 'sub'}, 'What a new session gets right now, after presets, the project file, and environment variables. ', h('code', {class: 'inline'}, 'eagent config'), ' prints the same.'),
      h('pre', {class: 'code'}, JSON.stringify(res.effective, null, 2)),
      h('p', {class: 'sub'}, 'Bundles live in ', h('code', {class: 'inline'}, E.c.files.bundles), ' and are meant to be committed; start one with ', h('code', {class: 'inline'}, 'eagent --config NAME'), '.'))));
  return det;
}

// ---- save bar ------------------------------------------------------------------------------------
function saveBar() {
  const ch = changes();
  const dirty = ch.length > 0;
  const running = E.c.running.length;
  const bar = h('div', {class: 'savebar' + (dirty ? ' dirty' : '')});
  const groups = groupChanges(ch);
  const summary = dirty ? h('div', {class: 'savesum'}, h('b', null, `${groups.length} change${groups.length > 1 ? 's' : ''}`), h('span', {class: 'sub'}, ' · ' + groups.join(' · ')))
    : h('div', {class: 'savesum'}, h('b', null, E.c.resolution.file.exists ? 'Saved' : 'Not saved yet'), h('span', {class: 'sub'}, running ? ` · ${running} running session${running > 1 ? 's' : ''} keep${running > 1 ? '' : 's'} the configuration they started with` : ' · for new and resumed sessions'));
  const pin = h('label', {class: 'check sub', title: 'Write every value instead of only the differences from the base preset, so the file stands alone even if the preset changes in a future release.'}, h('input', {type: 'checkbox', disabled: X.readOnly?.(), checked: E.mode === 'pin', onchange: ev => { E.mode = ev.target.checked ? 'pin' : 'overlay'; rerender(); }}), 'write every value');
  const baseSel = h('select', {class: 'mini', disabled: lockedField('/preset'), title: 'The preset the saved file builds on; only differences from it are written.', onchange: ev => { E.base = ev.target.value; rerender(); }}, h('option', {value: '', selected: !E.base}, 'no base preset'), ...E.c.presets.map(p => h('option', {value: p.name, selected: E.base === p.name}, `base: ${p.name}`)));
  const actions = h('div', {class: 'acts'},
    dirty ? h('button', {onclick: () => { E.cfg = clone(E.loaded); E.base = E.loadedBase; E.problems = []; rerender(); }}, 'Discard') : null,
    h('button', {disabled: !canAction('bundle.save'), onclick: saveBundleDialog, title: 'Save this setup as a named configuration so it can be picked per session or shared'}, 'Save as bundle…'),
    dirty || X.remote ? null : h('button', {onclick: () => X.newSessionDialog({project: true})}, 'New session with this'),
    h('button', {class: 'primary', disabled: X.readOnly?.() || !dirty && E.mode !== 'pin' && E.c.resolution.file.exists, onclick: () => save()}, X.remote ? 'Review project changes' : E.c.resolution.file.exists ? 'Save to project' : 'Save to project file'));
  bar.append(summary, h('div', {class: 'opts'}, baseSel, pin), actions);
  return bar;
}
async function save() {
  // Always conditional: the etag the page loaded (or "" for no file yet).
  // A conflict comes back as 409 and is resolved in conflictDialog.
  const body = {config: E.cfg, base_preset: E.base, mode: E.mode, if_match: E.etag};
  const changed = changes();
  try {
    const r = await api('/api/config', {method: 'PUT', body: JSON.stringify(body)});
    if (r.staged) { toast('Ready for review in FinaleChat. Nothing has been saved yet.'); return; }
    const prev = E.c.resolution.file.raw, prevExists = E.c.resolution.file.exists;
    E.problems = [];
    const skipped = (r.skipped_by_env || []).filter(x => changed.includes(x.pointer));
    await load(); rerender();
    undoToast(prev, prevExists, r.saved.etag);
    for (const x of skipped) toast(`${label(x.pointer)} was not written: $${x.env} pins it in this shell`, 'warn');
  } catch (e) {
    if (e.status === 409 && !X.remote) { conflictDialog(e.body); return; }
    if (e.status === 422 && e.body && e.body.problems) { E.problems = e.body.problems; rerender(); toast('fix the highlighted fields', 'bad'); const first = $('#main .problem.error'); if (first) first.scrollIntoView({block: 'center'}); return; }
    toast(e.message, 'bad');
  }
}
function undoToast(prevRaw, prevExists, newETag) {
  document.querySelectorAll('.toast.undo').forEach(o => o.remove()); // only the latest save can be undone
  const t = h('div', {class: 'toast show undo'}, 'Saved. ', h('button', {class: 'small', onclick: async () => {
    try { await api('/api/config/raw', {method: 'PUT', body: JSON.stringify({raw: prevExists ? prevRaw : '{\n}\n', if_match: newETag})}); t.remove(); toast('undone'); E = null; render(); } catch (e) { toast(e.message, 'bad'); }
  }}, 'Undo'));
  X.toastHost().append(t);
  setTimeout(() => { t.classList.remove('show'); setTimeout(() => t.remove(), 300); }, 8000);
}
function conflictDialog(body) {
  showModal(h('div', null, h('h2', null, 'The file changed on disk'), h('p', {class: 'sub'}, 'Someone (or another eagent) wrote the project file since this page loaded. Reload to see it, or overwrite it with what you have here.'),
    h('pre', {class: 'code', style: 'max-height:40vh;overflow:auto'}, body && body.current || ''),
    h('div', {class: 'foot'}, h('button', {onclick: () => { closeModal(); E = null; render(); }}, 'Reload'), h('button', {class: 'primary', onclick: () => { closeModal(); E.etag = body.etag || ''; save(); }}, 'Overwrite'))));
}
function saveBundleDialog() {
  const name = h('input', {class: 'search', placeholder: 'name, e.g. eric-fast'});
  const desc = h('input', {class: 'search', placeholder: 'what it is for', style: 'flex:1;min-width:200px'});
  const status = h('span', {class: 'sub'});
  const go = h('button', {class: 'primary', onclick: async () => {
    try { const cfg = clone(E.cfg); cfg.preset = E.base || undefined; const r = await api('/api/config/bundles', {method: 'POST', body: JSON.stringify({name: name.value.trim(), description: desc.value.trim(), config: cfg})}); if (r.staged) { status.textContent = 'Ready for review in FinaleChat. The bundle has not changed yet.'; return; } closeModal(); toast(`bundle ${r.name} saved`); await load(); rerender(); }
    catch (e) { status.textContent = e.message; }
  }}, X.remote ? 'Review bundle change' : 'Save bundle');
  showModal(h('div', null, h('h2', null, 'Save as bundle'), h('p', {class: 'sub'}, 'A bundle is a named copy of this setup in ', h('code', {class: 'inline'}, shortPath(E.c.files.bundles)), '. Commit it so teammates can start sessions with it; pick it per session with ', h('code', {class: 'inline'}, '--config NAME'), '.'),
    h('div', {class: 'row'}, name, desc), h('div', {class: 'foot'}, status, h('span', {style: 'flex:1'}), h('button', {onclick: closeModal}, 'Cancel'), go)));
  name.focus();
}

window.addEventListener('beforeunload', ev => { if (E && changes().length) { ev.preventDefault(); ev.returnValue = ''; } });
window.EagentConfig = {render, stageDraft: () => E && (E.rawDraft !== undefined && E.rawDraft !== E.c.resolution.file.raw ? api('/api/config/raw',{method:'PUT',body:JSON.stringify({raw:E.rawDraft,if_match:E.etag})}) : api('/api/config', {method:'PUT',body:JSON.stringify({config:E.cfg,base_preset:E.base,mode:E.mode,if_match:E.etag})})), dirty: () => !!(E && (changes().length || E.rawDraft !== undefined && E.rawDraft !== E.c.resolution.file.raw)), reset: () => { E = null; },
  draftToken: () => E ? X.canonical({config: E.cfg, base_preset: E.base, mode: E.mode, if_match: E.etag, raw: E.rawDraft}) : '',
  routeResult: (key, r) => { if (!E) return; E.tests[key + (r.route === 'fallback' ? ':fb' : '')] = {busy: false, result: r}; if (r.base_url && r.model) E.cat.checks[`${trimSlash(r.base_url)}|${r.model}|${r.effort}`] = {status: r.status, ms: r.ms, output: r.usage?.output, reasoning: r.usage?.reasoning, error: r.error, at: r.at}; rerender(); }
};
})();
