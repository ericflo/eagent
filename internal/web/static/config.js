/* eagent configuration editor. Loaded after app.js; uses its helpers via window.__eagent. */
(() => {
'use strict';
const X = window.__eagent;
const {h, api, toast, showModal, closeModal, k, money, $} = X;

// The model routes the page edits: eagent's own, and the dedicated
// summarizer's when it has one.
const ROUTES = {
  model: {key: 'model', ptr: '/model', name: 'eagent', role: 'The model eagent runs on. It reads what the agent does, talks to you, and types into the agent\'s terminal. It is called often, so a fast model whose prompt cache holds pays off.'},
  summarizer: {key: 'summarizer', ptr: '/summarizer/model', name: 'Summarizer', role: 'Keeps the thread\'s title and summary in a separate small call.'},
};
const routeOf = (cfg, key) => key === 'model' ? cfg.model : cfg.summarizer && cfg.summarizer.model;
const ROUTE_RE = /^\/(model|summarizer\/model)\/(fallback\/)?(\w+)/;
const ROUTE_NAME = {'model': 'eagent\'s model', 'summarizer/model': 'summarizer model'};
const EFFORT_HELP = {
  default: 'No reasoning_effort field is sent; the model or server default applies.',
  none: 'Turns reasoning off (or sends no thinking block where supported).',
  minimal: 'Barely any thinking before answering.',
  low: 'A little thinking. The usual choice for eagent: it is called often.',
  medium: 'Balanced.',
  high: 'Thinks at length. Slower and pricier.',
  xhigh: 'Very long thinking. Only worth it for hard problems.',
  max: 'The model thinks as long as it wants.',
};
const PROTO_LABELS = {'openai-chat': 'Chat Completions', 'openai-responses': 'Responses', 'anthropic': 'Anthropic Messages'};
const ENGINE_LABELS = {lmstudio: 'LM Studio', loom: 'Loom', llamacpp: 'llama.cpp', vllm: 'vLLM', sglang: 'SGLang', ollama: 'Ollama'};
const LABELS = {
  '/tick_seconds': 'seconds between progress looks', '/quiet_seconds': 'longest silence while the agent works', '/max_calls_per_turn': 'calls per turn',
  '/rollover_tokens': 'context rollover', '/report_max_chars': 'report kept', '/persona': 'eagent\'s voice',
  '/summarizer/mode': 'title and summary', '/relay/default': 'default agent', '/finalechat/enabled': 'phone mirror',
  '/finalechat/question_timeout_seconds': 'phone question timeout', '/finalechat/mirror_input': 'mirror your messages to the phone',
  '/finalechat/agent': 'phone agent name', '/finalechat/token_env': 'phone token variable', '/finalechat/base_url': 'phone service address',
  '/finalechat/artifacts': 'session archives',
};
const FIELD_LABEL = {model: 'model', protocol: 'protocol', base_url: 'provider', api_key_env: 'key variable', reasoning_effort: 'effort', max_tokens: 'max output tokens', context_tokens: 'context window', replay_reasoning: 'replay reasoning', fallback: 'fallback'};

let E = null;

// ---- state -------------------------------------------------------------------------------
const clone = v => JSON.parse(JSON.stringify(v));
function strip(cfg) { const c = clone(cfg); delete c.name; delete c.description; delete c.instructions; delete c.preset; delete c.default_config; delete c.servers; delete c.add_server_url; delete c.save_preset; if (!c.finalechat) c.finalechat = {}; if (!c.summarizer) c.summarizer = {mode: 'inline'}; return c; }
// at reads a nested value by its pointer path ("model/fallback").
function at(obj, path) { for (const seg of path.split('/')) obj = obj == null ? undefined : obj[seg]; return obj; }
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
  const m = ptr.match(ROUTE_RE);
  if (m) return `${ROUTE_NAME[m[1]]} ${m[2] ? 'fallback ' : ''}${FIELD_LABEL[m[3]] || m[3].replace(/_/g, ' ')}`;
  return ptr.slice(1).replace(/[/_]/g, ' ');
}
function groupChanges(ptrs) {
  // Collapse one route's many fields into one item so the summary reads naturally.
  const seen = new Set(), out = [];
  for (const p of ptrs) {
    const m = p.match(ROUTE_RE);
    const key = m ? `/${m[1]}${m[2] ? '/fallback' : ''}` : p;
    if (seen.has(key)) continue;
    seen.add(key);
    if (m) {
      const who = `${ROUTE_NAME[m[1]]}${m[2] ? ' fallback' : ''}`;
      const path = m[1] + (m[2] ? '/fallback' : '');
      const before = at(E.loaded, path), after = at(E.cfg, path);
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
  if (!s) { // a parent may carry it (e.g. the whole route came from one layer)
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
function keyReady(env) { return !env || !!E.c.keys[env]; }
function priceLine(m, prov) {
  if (prov && prov.custom) {
    const parts = [ENGINE_LABELS[prov.engine] ? `${ENGINE_LABELS[prov.engine]} · local server` : 'custom server'];
    if (m && m.context) parts.push(`${ctx(m.context)} context`);
    if (m && m.vision) parts.push('sees images');
    if (m && m.note) parts.push(m.note);
    return parts.join(' · ');
  }
  if (!m) return 'not in any catalog: sessions will show as unpriced';
  const parts = [];
  if (m.price) parts.push(`$${trimNum(m.price.in)} in · $${trimNum(m.price.out)} out per M tokens${m.price.nominal ? ' (list price)' : ''}${m.live ? ' (live)' : ''}`);
  else parts.push(m.live ? 'listed live by the provider, price unknown: sessions will show as unpriced' : 'price unknown');
  if (m.context) parts.push(`${ctx(m.context)} context`);
  if (m.vision) parts.push('sees images');
  return parts.join(' · ');
}
const ctx = n => !n ? '' : n >= 1e6 ? `${(n / 1e6).toFixed(n % 1e6 ? 1 : 0)}M` : `${Math.round(n / 1000)}k`;
const trimNum = n => n >= 10 ? n.toFixed(0) : n >= 1 ? n.toFixed(2).replace(/\.?0+$/, '') : n.toFixed(3).replace(/\.?0+$/, '');
function checkFor(a, effort) { return E.cat.checks[`${trimSlash(a.base_url)}|${a.model}|${effort}`]; }
function setModel(a, m) {
  const p = E.cat.providers.find(x => x.id === m.provider);
  a.base_url = p.base_url; a.protocol = protocolOf(m); a.api_key_env = p.key_env || ''; a.model = m.id;
  // The window is the new model's; 0 is unknown, never the old model's.
  a.context_tokens = m.context || 0;
  // The server's rule for a pick (config.PickRoute): no effort sends none
  // on any model and stays; a level the model lacks moves to the nearest
  // it offers, the lower on a tie (config.NearestEffort).
  if (a.reasoning_effort && m.efforts && m.efforts.length && !m.efforts.includes(a.reasoning_effort)) {
    const order = E.cat.effort_order || [], at = e => order.indexOf(e), want = at(a.reasoning_effort);
    a.reasoning_effort = want < 0 ? (m.efforts.includes('medium') ? 'medium' : m.efforts[Math.floor(m.efforts.length / 2)])
      : m.efforts.reduce((best, e) => { const d = Math.abs(at(e) - want), bd = Math.abs(at(best) - want); return d < bd || (d === bd && at(e) < at(best)) ? e : best; });
  }
  if (!replays(a)) delete a.replay_reasoning;
}
// replays reports whether a route's protocol can send the model its earlier
// reasoning back: Chat Completions and Responses can (replay_reasoning).
function replays(a) { return a.protocol === 'openai-chat' || a.protocol === 'openai-responses'; }
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
  const fileBase = res.file.exists ? (res.file.preset || '') : (res.preset || '');
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
    body.append(h('div',{class:'settings-intro'},
      h('div',{class:'settings-intro-row'},
        h('div',null,
          h('h2',null,tab==='Models'?'Choose the model eagent runs on':tab==='Behavior'?'Make it work your way':tab==='Prompts'?'Give eagent direction':'Fine-tune your setup'),
          h('p',null,tab==='Models'?'Pick a model and reasoning effort, and how the thread is summarized.':tab==='Behavior'?'Choose the default agent and adjust progress updates, limits, and the phone mirror.':tab==='Prompts'?'Customize the instructions eagent works from.':'Presets, saved configurations, and detailed overrides.')),
        tab==='Models' && canAction('bundle.save') ? h('button',{class:'small',onclick:saveBundleDialog,title:'Save this setup as a reusable named preset'},'+ Save as preset…') : null)));
    if(tab==='Models') { const sc = serversCard(); if (sc) body.append(sc); body.append(h('div',{class:'routes'},routeCard(ROUTES.model),summarizerCard())); }
    if(tab==='Behavior') body.append(agentCard(),behaviorCard(),phoneCard());
    if(tab==='Prompts') body.append(promptsCard());
    if(tab==='Advanced') body.append(quickStart(),advancedCard());
  } else {
  body.append(quickStart());
  const sc = serversCard();
  if (sc) body.append(sc);
  body.append(h('div', {class: 'routes'}, routeCard(ROUTES.model), summarizerCard()));
  body.append(h('div', {class: 'grid2'}, agentCard(), behaviorCard()));
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
  return h('div', {class: 'cfg-head'}, h('div', null, h('h2', null, 'Configuration'), h('p', {class: 'sub'}, 'The model eagent runs on, how hard it thinks, which agent a session starts, and how the session behaves. Changes apply to sessions started or resumed after you save.')),
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
  return h('div', {class: 'card quick'}, h('h3', null, 'Start from', h('span', {class: 'grow'}), h('span', {class: 'sub'}, 'Loads a setup into the form; nothing is saved until you press Save.'),
      canAction('bundle.save') ? h('button', {class: 'small', onclick: saveBundleDialog, title: 'Save the current form setup as a reusable named preset'}, '+ Save as preset…') : null),
    h('div', {class: 'chips'}, ...chips, h('button', {class: 'chip-btn', title: 'The built-in defaults, before any preset', onclick: () => loadInto('', null)}, 'defaults')),
    bchips.length ? h('div', {class: 'chips bundles'}, h('span', {class: 'sub'}, 'saved presets'), ...bchips) : null,
    X.remote ? bundleActions() : (E.c.bundles.length ? localBundleActions() : null));
}
function localBundleActions() {
  const current = E.c.resolution.file.default_config || '';
  const selected = h('select', {'aria-label': 'Default saved preset'}, h('option', {value: '', selected: !current}, 'project defaults'), ...E.c.bundles.map(b => h('option', {value: b.name, selected: b.name === current}, b.name)));
  const removal = h('select', {'aria-label': 'Saved preset to delete'}, ...E.c.bundles.map(b => h('option', {value: b.name}, b.name)));
  return h('details', null, h('summary', null, 'Manage saved presets'),
    h('p', {class: 'sub'}, 'Pick a saved preset as the project default for new sessions, or delete ones you no longer need.'),
    h('div', {class: 'row'}, selected, h('button', {onclick: async () => {
      try {
        await api('/api/config', {method: 'PUT', body: JSON.stringify({config: E.loaded, base_preset: E.loadedBase, mode: E.mode, if_match: E.etag, default_config: selected.value, loaded: E.loaded})});
        toast(selected.value ? `default preset set to ${selected.value}` : 'default preset cleared');
        await load(); rerender();
      } catch (e) { toast(e.message, 'bad'); }
    }}, 'Set default')),
    h('div', {class: 'row'}, removal, h('button', {onclick: async () => {
      const target = removal.value;
      if (!target || !confirm(`Delete saved preset "${target}"?`)) return;
      try {
        await api(`/api/config/bundles/${encodeURIComponent(target)}`, {method: 'DELETE'});
        toast(`preset ${target} deleted`);
        await load(); rerender();
      } catch (e) { toast(e.message, 'bad'); }
    }}, 'Delete preset')));
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
  for (const key of Object.keys(ROUTES)) { const a = routeOf(E.cfg, key); if (a?.fallback && !Object.keys(a.fallback).length) delete a.fallback; }
  if (X.canEdit && !X.canEdit('/preset')) E.base = E.loadedBase;
}

// ---- local & custom servers ----------------------------------------------------------------
function customServers() { return (E.cat.providers || []).filter(p => p.custom); }
function modelsForProvider(id) { return (E.cat.models || []).filter(m => m.provider === id); }
function canManageServers() { return !X.remote || (!X.readOnly?.() && (!X.canEdit || X.canEdit('/servers') || X.canEdit('/add_server_url'))); }
function serversCard() {
  const servers = customServers();
  const canManage = canManageServers();
  const canRefresh = !X.remote || (!X.readOnly?.() && canAction('settings.refresh'));
  const head = h('h3', null, 'Local & custom servers', h('span', {class: 'grow'}),
    servers.length && canRefresh ? h('button', {class: 'ghost small', onclick: refreshAllServers}, X.remote ? 'Review refresh' : 'Refresh all') : null,
    canManage ? h('button', {class: 'small primary', onclick: () => serverDialog(null)}, '+ Add server…') : null);
  const intro = h('p', {class: 'sub'}, X.remote
    ? 'Register any local or custom server URL (llama.cpp, vLLM, SGLang, LM Studio, Loom, Ollama, etc.). Saving asks your local eagent connector to query /models and probe Chat Completions, Responses, and Anthropic Messages automatically.'
    : 'Register any OpenAI Chat Completions, OpenAI Responses, or Anthropic Messages server (llama.cpp, vLLM, SGLang, LM Studio, Loom, Ollama, etc.). eagent inspects /models and probes supported API kinds automatically.');
  const rows = servers.map(s => {
    const ms = modelsForProvider(s.id);
    const err = E.cat.live_errors && E.cat.live_errors[s.id];
    const ready = keyReady(s.key_env);
    const protos = (s.protocols && s.protocols.length) ? s.protocols : [s.protocol || 'openai-chat'];
    const useAll = ms.length && !lockedField('/model/model')
      ? h('button', {class: 'small', title: `Use ${ms[0].id} on ${s.label} as eagent's model`, onclick: () => {
          setModel(E.cfg.model, ms[0]);
          E.cfg.model.protocol = s.protocol || protocolOf(ms[0]);
          if (!replays(E.cfg.model)) delete E.cfg.model.replay_reasoning;
          if (ms[0].context && E.cfg.rollover_tokens > Math.floor(ms[0].context * 0.85)) {
            E.cfg.rollover_tokens = Math.max(20000, Math.floor(ms[0].context * 0.75));
          }
          rerender();
          toast(`loaded ${ms[0].id} as eagent's model (press Save to apply)`);
        }}, 'Use for eagent')
      : null;
    const protoEl = canManage
      ? h('select', {class: 'mini', title: 'Default wire API protocol for this server', onchange: async ev => {
          const next = ev.target.value;
          try {
            const r = await api(`/api/servers/${encodeURIComponent(s.id)}`, {method: 'PUT', body: JSON.stringify({url: s.base_url, protocol: next, key_env: s.key_env || '', label: s.label, note: s.note || '', skip_probe: true})});
            if (r && r.staged) { toast('Ready for review in FinaleChat. Tap Save to update the server protocol.'); return; }
            await reloadCatalogKeepDraft();
            toast(`${s.label} default API set to ${PROTO_LABELS[next] || next}`);
          } catch (e) { toast(e.message, 'bad'); draw(); }
        }}, ...['openai-chat', 'openai-responses', 'anthropic'].map(p => h('option', {value: p, selected: (s.protocol || 'openai-chat') === p}, `${PROTO_LABELS[p]}${protos.includes(p) ? ' ✓' : ''}`)))
      : h('span', {class: 'badge mono'}, PROTO_LABELS[s.protocol] || s.protocol || 'openai-chat');
    const modelChips = ms.length
      ? h('div', {class: 'srv-models'}, ...ms.slice(0, 8).map(m => h('span', {class: 'badge mono', title: `${m.id}${m.context ? ' · ' + ctx(m.context) + ' ctx' : ''}${m.vision ? ' · vision' : ''}`}, m.id, m.context ? ` · ${ctx(m.context)}` : '', m.vision ? ' · vision' : '')), ms.length > 8 ? h('span', {class: 'sub'}, `+${ms.length - 8} more`) : null)
      : h('div', {class: 'sub'}, err ? h('span', {class: 'warn-text'}, err) : 'No models discovered yet.');
    return h('div', {class: 'srv-row'},
      h('div', {class: 'srv-main'},
        h('div', {class: 'srv-title'},
          h('span', {class: 'kdot' + (ready && !err ? ' on' : '')}),
          h('b', null, s.label),
          h('code', {class: 'inline'}, s.id),
          s.engine ? h('span', {class: 'livetag'}, ENGINE_LABELS[s.engine] || s.engine) : h('span', {class: 'livetag'}, 'local'),
          h('span', {class: 'sub'}, s.base_url),
          s.key_env ? h('span', {class: 'sub' + (ready ? '' : ' warn-text')}, ` · $${s.key_env}${ready ? ' (set)' : ' (not set)'}`) : h('span', {class: 'sub'}, ' · no key')),
        modelChips),
      h('div', {class: 'srv-actions'},
        protoEl,
        useAll,
        canRefresh ? h('button', {class: 'ghost small', onclick: () => probeExistingServer(s)}, 'Probe') : null,
        canManage ? h('button', {class: 'ghost small', onclick: () => serverDialog(s)}, 'Edit') : null,
        canManage ? h('button', {class: 'ghost small', onclick: () => removeServer(s)}, 'Remove') : null));
  });
  return h('div', {class: 'card servers-card'}, head, intro,
    rows.length ? h('div', {class: 'srv-list'}, ...rows) : h('div', {class: 'sub'}, canManage ? 'No local or custom servers registered yet. Click “+ Add server…” to enter a URL and probe it.' : 'No local or custom servers registered in this project yet.'));
}
async function reloadCatalogKeepDraft() {
  const [c, cat] = await Promise.all([api('/api/config'), api('/api/catalog')]);
  E.c = c; E.cat = cat;
  draw();
}
async function probeExistingServer(s) {
  try {
    if (!X.remote) toast(`probing ${s.label}…`);
    const r = await api(`/api/servers/${encodeURIComponent(s.id)}/probe`, {method: 'POST', body: '{}'});
    if (r && r.staged) { toast('Ready for review in FinaleChat. Tap Save to re-probe local servers.'); return; }
    await reloadCatalogKeepDraft();
    toast(`${r.server.label}: ${(r.models || []).length} model${(r.models || []).length === 1 ? '' : 's'} · ${(r.server.protocols || [r.server.protocol]).join(', ')}`);
  } catch (e) { toast(e.message, 'bad'); }
}
async function refreshAllServers() {
  try {
    if (!X.remote) toast('refreshing local servers…');
    const r = await api('/api/servers/refresh', {method: 'POST', body: '{}'});
    if (r && r.staged) { toast('Ready for review in FinaleChat. Tap Save to re-probe local servers.'); return; }
    await reloadCatalogKeepDraft();
    const okCount = Object.keys(r.servers || {}).length, errCount = Object.keys(r.errors || {}).length;
    toast(`refreshed ${okCount} server${okCount === 1 ? '' : 's'}${errCount ? ` (${errCount} unreachable)` : ''}`, errCount && !okCount ? 'bad' : '');
  } catch (e) { toast(e.message, 'bad'); }
}
async function removeServer(s) {
  if (!X.remote && !confirm(`Remove registered server "${s.label}" (${s.base_url})?`)) return;
  try {
    const r = await api(`/api/servers/${encodeURIComponent(s.id)}`, {method: 'DELETE'});
    if (r && r.staged) { toast(`Staged removal of ${s.label}. Tap Save in FinaleChat to confirm.`); return; }
    await reloadCatalogKeepDraft();
    toast(`removed ${s.label}`);
  } catch (e) { toast(e.message, 'bad'); }
}
function serverDialog(existing) {
  const isEdit = !!existing;
  const urlIn = h('input', {class: 'search', style: 'flex:1;min-width:260px', value: existing ? existing.base_url : '', placeholder: 'http://localhost:8080 or http://gpu-box:8000/v1'});
  const keyIn = h('input', {class: 'search', style: 'min-width:180px', value: existing ? (existing.key_env || '') : '', placeholder: 'optional, e.g. VLLM_API_KEY'});
  const protoSel = h('select', null,
    h('option', {value: '', selected: !existing}, 'Auto-detect via probe'),
    ...['openai-chat', 'openai-responses', 'anthropic'].map(p => h('option', {value: p, selected: existing && existing.protocol === p}, `${PROTO_LABELS[p]} (${p})`)));
  const labelIn = h('input', {class: 'search', style: 'flex:1;min-width:180px', value: existing ? existing.label : '', placeholder: 'auto-filled on probe'});
  const idIn = h('input', {class: 'search', style: 'min-width:160px', value: existing ? existing.id : '', disabled: isEdit, placeholder: 'auto-generated slug'});
  const skipChk = h('input', {type: 'checkbox', checked: false});
  const preview = h('div', {class: 'probe-preview'});
  const status = h('span', {class: 'sub'});
  let lastProbe = null;

  function renderPreview(res) {
    lastProbe = res;
    const s = res.server;
    const protos = s.protocols || [];
    const ms = res.models || [];
    if (!labelIn.value.trim()) labelIn.value = s.label || '';
    if (!isEdit && !idIn.value.trim()) idIn.value = s.id || '';
    if (!protoSel.value && s.protocol) protoSel.value = s.protocol;
    const protoBtns = h('div', {class: 'seg'}, ...['openai-chat', 'openai-responses', 'anthropic'].map(p => {
      const supported = protos.includes(p);
      return h('button', {type: 'button', class: (protoSel.value === p ? 'on' : '') + (supported ? ' tool_call' : ''), onclick: () => { protoSel.value = p; renderPreview(res); }},
        h('span', {class: 'e'}, PROTO_LABELS[p]),
        h('span', {class: 'ann'}, supported ? '✓ probed' : 'not detected'));
    }));
    preview.replaceChildren(
      h('div', {class: 'banner'},
        h('div', {class: 'row', style: 'margin:0 0 6px'},
          h('b', null, s.label),
          s.engine ? h('span', {class: 'livetag'}, ENGINE_LABELS[s.engine] || s.engine) : null,
          h('code', {class: 'inline'}, s.base_url)),
        h('div', {class: 'sub', style: 'margin-bottom:6px'}, 'Supported API kinds (click to choose default):'),
        protoBtns,
        h('div', {class: 'sub', style: 'margin-top:8px'}, ms.length
          ? `${ms.length} model${ms.length === 1 ? '' : 's'} discovered:`
          : 'No models returned by /models yet.'),
        ms.length ? h('div', {class: 'srv-models', style: 'margin-top:4px'}, ...ms.slice(0, 12).map(m =>
          h('span', {class: 'badge mono'}, m.id, m.context ? ` · ${ctx(m.context)}` : '', m.vision ? ' · vision' : '', m.efforts && m.efforts.length ? ` · ${m.efforts.length} efforts` : ''))) : null,
        ...(res.warnings || []).map(w => h('div', {class: 'problem warning'}, w))));
  }

  const probeBtn = !X.remote ? h('button', {type: 'button', onclick: async () => {
    const rawURL = urlIn.value.trim();
    if (!rawURL) { status.textContent = 'Enter a server URL first.'; return; }
    probeBtn.disabled = true; status.textContent = 'Probing /models and API endpoints…';
    try {
      const r = await api('/api/servers/probe', {method: 'POST', body: JSON.stringify({url: rawURL, protocol: protoSel.value, key_env: keyIn.value.trim(), id: isEdit ? existing.id : idIn.value.trim(), label: labelIn.value.trim()})});
      status.textContent = 'Probe succeeded.';
      renderPreview(r);
    } catch (e) { status.textContent = e.message; preview.replaceChildren(h('div', {class: 'problem error'}, e.message)); }
    finally { probeBtn.disabled = false; }
  }}, 'Probe server') : null;

  const saveBtn = h('button', {type: 'button', class: 'primary', onclick: async () => {
    const rawURL = urlIn.value.trim();
    if (!rawURL) { status.textContent = 'Enter a server URL first.'; return; }
    saveBtn.disabled = true; if (probeBtn) probeBtn.disabled = true;
    status.textContent = X.remote ? 'Staging for review…' : (skipChk.checked ? 'Saving…' : 'Probing and saving…');
    try {
      const body = {url: rawURL, protocol: protoSel.value, key_env: keyIn.value.trim(), id: isEdit ? existing.id : idIn.value.trim(), label: labelIn.value.trim(), skip_probe: skipChk.checked};
      const r = isEdit
        ? await api(`/api/servers/${encodeURIComponent(existing.id)}`, {method: 'PUT', body: JSON.stringify(body)})
        : await api('/api/servers', {method: 'POST', body: JSON.stringify(body)});
      closeModal();
      if (r && r.staged) {
        toast('Ready for review in FinaleChat. Tap Save to probe and register this server.');
        return;
      }
      await reloadCatalogKeepDraft();
      const count = (r.models || []).length;
      toast(`${isEdit ? 'Updated' : 'Registered'} ${r.server.label}${count ? ` · ${count} model${count === 1 ? '' : 's'}` : ''}`);
    } catch (e) { status.textContent = e.message; saveBtn.disabled = false; if (probeBtn) probeBtn.disabled = false; }
  }}, X.remote ? (isEdit ? 'Review server change' : 'Review & probe in FinaleChat') : (isEdit ? 'Save changes' : 'Add server'));

  urlIn.addEventListener('keydown', ev => { if (ev.key === 'Enter') { ev.preventDefault(); (probeBtn || saveBtn).click(); } });
  showModal(h('div', null,
    h('h2', null, isEdit ? `Edit server · ${existing.id}` : 'Add local or custom model server'),
    h('p', {class: 'sub'}, X.remote
      ? 'Enter any server URL reachable from your eagent machine. When you tap Save in FinaleChat, your local connector queries /models and probes Chat Completions, Responses, and Anthropic Messages automatically.'
      : 'Enter any server URL (with or without /v1). eagent checks /models and probes Chat Completions (/chat/completions), OpenAI Responses (/responses), and Anthropic Messages (/messages).'),
    field('Server URL', [urlIn, probeBtn], [h('span', {class: 'src'}, 'Examples: http://localhost:8080 (llama.cpp), http://localhost:1234 (LM Studio), http://gpu-box:8000 (vLLM / SGLang)')]),
    preview,
    h('div', {class: 'fields'},
      field('Default API protocol', [protoSel], [h('span', {class: 'src'}, 'A model route can still pick another protocol')]),
      field('API key env variable (optional)', [keyIn], [h('span', {class: 'src'}, 'Leave blank for local servers without authentication')]),
      field('Display name (optional)', [labelIn], []),
      field('Server ID (optional)', [idIn], [h('span', {class: 'src'}, 'Used in CLI and FinaleChat provider lists')])),
    h('label', {class: 'check', style: 'margin-top:6px'}, skipChk, 'Register without probing (if the server is currently offline)'),
    h('div', {class: 'foot'}, status, h('span', {style: 'flex:1'}), h('button', {type: 'button', onclick: closeModal}, 'Cancel'), saveBtn)));
  urlIn.focus();
}

// ---- route cards ---------------------------------------------------------------------------
// routeCard edits one model route. top holds extra controls shown under the
// heading (the summarizer's mode).
function routeCard(slot, top) {
  const a = routeOf(E.cfg, slot.key);
  const key = slot.key;
  const ptr = f => `${slot.ptr}/${f}`;
  const m = lookup(a);
  const prov = providerOf(a.base_url);
  const locked = f => lockedField(ptr(f));
  const card = h('div', {class: 'card route ' + key});
  card.append(h('div', {class: 'rhead'}, h('h3', null, h('span', {class: 'swatch'}), slot.name), h('p', {class: 'sub role'}, slot.role)));
  if (top) card.append(...[].concat(top));

  // Model
  const pickBtn = h('button', {class: 'pick', disabled: locked('model') || locked('base_url'), onclick: () => openPicker(card, a, key, false)},
    h('span', {class: 'pk'}, h('span', {class: 'kdot' + (keyReady(a.api_key_env) ? ' on' : '')}), h('b', null, m ? m.label : a.model), h('span', {class: 'sub'}, prov ? prov.label : a.base_url.replace(/^https?:\/\//, ''))), h('span', {class: 'caret'}, '▾'));
  const same = key === 'summarizer' && !locked('model') ? h('button', {class: 'link', title: 'Remove this route; the summarizer then calls eagent\'s model', onclick: () => { delete E.cfg.summarizer.model; rerender(); }}, 'use eagent\'s model instead') : null;
  card.append(field('Model', [pickBtn], [h('span', {class: 'notes-row'}, srcNode(ptr('model')), same), ...problemNodes(ptr('model'))]));
  card.append(h('div', {class: 'sub price'}, priceLine(m, prov),
    a.api_key_env && !keyPresent(a.api_key_env) ? h('span', {class: 'warn-text'}, ` · $${a.api_key_env} is not set in this shell`)
      : !a.api_key_env && (!prov || prov.custom) ? h('span', null, ' · no key required') : null));

  // Inline API endpoint selector for custom/local or multi-protocol servers
  if (prov && (prov.custom || (prov.protocols && prov.protocols.length > 1))) {
    const supported = (m && m.protocols && m.protocols.length) ? m.protocols : (prov.protocols || []);
    const pseg = h('div', {class: 'seg' + (supported.length ? ' annotated' : '')});
    for (const p of ['openai-chat', 'openai-responses', 'anthropic']) {
      const ok = supported.includes(p);
      pseg.append(h('button', {class: (a.protocol === p ? 'on' : '') + (ok ? ' tool_call' : ''), disabled: locked('protocol'), title: E.cat.transports[p] || p, onclick: () => {
        a.protocol = p;
        if (!replays(a)) delete a.replay_reasoning;
        rerender();
      }}, h('span', {class: 'e'}, PROTO_LABELS[p]), supported.length ? h('span', {class: 'ann'}, ok ? '✓' : '—') : null));
    }
    card.append(field('API endpoint', [pseg], [srcNode(ptr('protocol')), ...problemNodes(ptr('protocol'))]));
  }

  // Effort
  const {list, checked} = effortsFor(a);
  // On Chat Completions no effort sends nothing (the provider's default),
  // which is not 'none': 'default' is shown, and lit, whenever it is the
  // route's effort, even on a model whose efforts are checked.
  const showDefault = a.protocol === 'openai-chat' && (!checked || !a.reasoning_effort);
  const displayList = showDefault ? ['default', ...list] : list;
  const cur = a.reasoning_effort || (a.protocol === 'openai-chat' ? (showDefault ? 'default' : '') : 'none');
  const anyCheck = list.some(e => checkFor(a, e)) || (showDefault && checkFor(a, ''));
  const seg = h('div', {class: 'seg' + (checked ? '' : ' unchecked') + (anyCheck ? ' annotated' : '')});
  for (const e of displayList) {
    const chk = checkFor(a, e === 'default' ? '' : e);
    const on = cur === e || (!showDefault && e === 'none' && cur === '');
    seg.append(h('button', {class: (on ? 'on' : '') + (chk ? ' ' + chk.status : ''), disabled: locked('reasoning_effort'), title: (EFFORT_HELP[e] || '') + (chk ? `\nLast test: ${checkText(chk, true)}` : ''), onclick: () => {
      if (e === 'default') a.reasoning_effort = '';
      else if (e === 'none' && a.protocol === 'openai-chat' && !(checked && list.includes('none')) && !showDefault) a.reasoning_effort = '';
      else a.reasoning_effort = e;
      rerender();
    }},
      h('span', {class: 'e'}, e), anyCheck ? h('span', {class: 'ann'}, chk ? checkText(chk) : '') : null));
  }
  const testing = E.tests[key] && E.tests[key].busy;
  const tools = h('div', {class: 'testrow'},
    h('button', {class: 'small', disabled: testing || !canAction('route.test'), onclick: () => runTest(key, null)}, testing ? 'testing…' : X.remote ? 'Review route test' : 'Test this route'),
    X.remote ? null : h('button', {class: 'small', disabled: testing, title: 'Send one tool call at each effort and note the latency and reasoning tokens. Each call uses provider billing.', onclick: () => runAll(key)}, 'Try every effort'),
    h('span', {class: 'sub grow'}, checked ? '' : 'Efforts are not verified for this model; the annotations come from your tests.'));
  card.append(field('Reasoning effort', [seg, tools], [srcNode(ptr('reasoning_effort')), h('span', {class: 'src'}, 'sends ', h('code', {class: 'inline'}, sentFor(a))), ...problemNodes(ptr('reasoning_effort'))]));
  const t = E.tests[key];
  if (t && t.result) card.append(resultLine(t.result));

  // Fallback
  if(!X.threadControls) card.append(fallbackLine(card, a, slot));

  // Advanced
  const open = !!E.open[key];
  const det = h('details', {open}, h('summary', {onclick: () => { E.open[key] = !open; }}, 'Advanced'));
  const num = (f, lbl, help, attrs) => field(lbl, [h('input', Object.assign({type: 'number', value: a[f] || '', disabled: locked(f), onchange: ev => { const v = parseInt(ev.target.value, 10); if (isNaN(v)) delete a[f]; else a[f] = v; rerender(); }}, attrs || {}))], [srcNode(ptr(f)), help ? h('span', {class: 'src'}, help) : null, ...problemNodes(ptr(f))]);
  if(X.threadControls) det.append(fallbackLine(card, a, slot));
  det.append(h('div', {class: 'adv'},
    num('max_tokens', 'Max output tokens', 'per call; reasoning counts toward it on most providers'),
    num('context_tokens', 'Context window', 'the model\'s usable window; the conversation restarts from a dossier at half of it unless a rollover number is set'),
    replays(a) ? field('Replay reasoning', [h('label', {class: 'check'}, h('input', {type: 'checkbox', checked: a.replay_reasoning !== false, disabled: locked('replay_reasoning'), onchange: ev => { if (ev.target.checked) delete a.replay_reasoning; else a.replay_reasoning = false; rerender(); }}), a.protocol === 'openai-responses' ? 'keep the model\'s reasoning items across turns (recommended; keeps the prompt cache warm)' : 'send the model its earlier reasoning back (reasoning_content); turn off for a server that rejects it')], [srcNode(ptr('replay_reasoning'))]) : null,
    field('Protocol', [h('select', {disabled: locked('protocol'), onchange: ev => { a.protocol = ev.target.value; if (!replays(a)) delete a.replay_reasoning; rerender(); }}, ...['openai-chat', 'openai-responses', 'anthropic'].map(p => h('option', {value: p, selected: a.protocol === p}, `${p} · ${E.cat.transports[p] || ''}`)))], [srcNode(ptr('protocol')), ...problemNodes(ptr('protocol'))]),
    field('Provider address', [h('select', {disabled: locked('base_url'), onchange: ev => { a.base_url = ev.target.value; const p = providerOf(a.base_url); if (p) { a.api_key_env = p.key_env || ''; if (!lookup(a)) a.protocol = p.protocol; } rerender(); }}, ...providerOptions(a.base_url))], [srcNode(ptr('base_url')), h('span', {class: 'src'}, 'Register custom or local servers above; catalog and registered servers appear here.'), ...problemNodes(ptr('base_url'))]),
    field('Key variable', [h('select', {disabled: locked('api_key_env'), onchange: ev => { a.api_key_env = ev.target.value; rerender(); }}, ...keyOptions(a.api_key_env))], [srcNode(ptr('api_key_env')), h('span', {class: 'src'}, 'Only the variable name is stored; leave empty for keyless local servers.'), ...problemNodes(ptr('api_key_env'))]),
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
  return [
    h('option', {value: '', selected: !current}, '(none — no key required)'),
    ...names.map(n => h('option', {value: n, selected: n === current}, `${n}${E.c.keys[n] ? ' · set' : ' · not set'}`)),
  ];
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
// A route test sends the route as the form has it ("model") and which of
// the saved routes it is ("target": model or summarizer) for the artifact
// transport, which can only test saved routes.
async function runTest(key, effort, route) {
  const a = routeOf(E.cfg, key);
  const testKey = route === 'fallback' ? key + ':fb' : key;
  E.tests[testKey] = {busy: true, result: null}; rerender();
  try {
    const body = {model: a, target: key, route: route || 'primary'};
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
  const a = routeOf(E.cfg, key);
  const {list, checked} = effortsFor(a);
  const showDefault = a.protocol === 'openai-chat' && !checked;
  const efforts = showDefault ? ['default', ...list] : list;
  for (const e of efforts) {
    E.tests[key] = {busy: true, result: E.tests[key] && E.tests[key].result}; rerender();
    const ro = e === 'default' ? '' : (e === 'none' && a.protocol === 'openai-chat' && !(checked && list.includes('none')) && !showDefault ? '' : e);
    try {
      const r = await api('/api/config/test', {method: 'POST', body: JSON.stringify({model: a, target: key, route: 'primary', effort: ro})});
      E.cat.checks[`${trimSlash(r.base_url)}|${r.model}|${r.effort}`] = {status: r.status, ms: r.ms, output: r.usage.output, reasoning: r.usage.reasoning, error: r.error, at: r.at};
      E.tests[key] = {busy: true, result: r};
    } catch (err) { E.tests[key] = {busy: true, result: {route: 'primary', effort: e, status: 'failed', error: err.message, usage: {}}}; }
    rerender();
  }
  E.tests[key].busy = false; rerender();
}

function fallbackLine(card, a, slot) {
  const key = slot.key, base = slot.ptr;
  const fb = a.fallback;
  const locked = lockedField(`${base}/fallback/model`) || lockedField(`${base}/fallback/base_url`);
  if (!fb) {
    const alt = suggestFallback(a);
    return h('div', {class: 'fb'}, h('span', {class: 'sub'}, 'Fallback: none. If this route fails, the call fails.'),
      alt ? h('button', {class: 'ghost small', disabled: locked, onclick: () => { a.fallback = alt; rerender(); }}, `add ${providerLabel(alt.base_url)} fallback`) : null,
      h('button', {class: 'ghost small', disabled: locked, onclick: () => openPicker(card, a, key, true)}, 'choose…'));
  }
  const fm = lookup(fb);
  const inert = trimSlash(fb.base_url) === trimSlash(a.base_url) && fb.model === a.model;
  const sameProv = !inert && trimSlash(fb.base_url) === trimSlash(a.base_url);
  const {list, checked} = effortsFor(fb);
  const showDefault = fb.protocol === 'openai-chat' && (!checked || !fb.reasoning_effort);
  const displayList = showDefault ? ['default', ...list] : list;
  const cur = fb.reasoning_effort || (fb.protocol === 'openai-chat' ? (showDefault ? 'default' : '') : 'none');
  const sel = h('select', {class: 'mini', disabled: lockedField(`${base}/fallback/reasoning_effort`), onchange: ev => {
    const v = ev.target.value;
    if (v === 'default') fb.reasoning_effort = '';
    else if (v === 'none' && fb.protocol === 'openai-chat' && !(checked && list.includes('none')) && !showDefault) fb.reasoning_effort = '';
    else fb.reasoning_effort = v;
    rerender();
  }}, ...displayList.map(e => h('option', {value: e, selected: cur === e || (!showDefault && e === 'none' && cur === '')}, e)));
  const t = E.tests[key + ':fb'];
  return h('div', {class: 'fb has'}, h('div', {class: 'row'}, h('span', {class: 'sub'}, 'Fallback'),
      h('button', {class: 'pick mini', disabled: locked, onclick: () => openPicker(card, a, key, true)}, h('span', {class: 'kdot' + (keyReady(fb.api_key_env) ? ' on' : '')}), h('b', null, fm ? fm.label : fb.model), h('span', {class: 'sub'}, providerLabel(fb.base_url)), h('span', {class: 'caret'}, '▾')),
      h('span', {class: 'sub'}, 'effort'), sel,
      h('button', {class: 'ghost small', disabled: t && t.busy || !canAction('route.test'), onclick: () => runTest(key, null, 'fallback').then(() => {})}, X.remote ? 'review test' : 'test'),
      h('button', {class: 'ghost small', disabled: locked, onclick: () => { delete a.fallback; rerender(); }}, 'remove')),
    t && t.result ? resultLine(t.result) : null,
    inert ? h('div', {class: 'problem error'}, 'This fallback is the same route as the primary, so it cannot help. Pick another provider or model.') : null,
    sameProv ? h('div', {class: 'problem warning'}, 'Same provider as the primary: this covers a model outage, not a provider outage.') : null,
    ...problemNodes(`${base}/fallback/model`), ...problemNodes(`${base}/fallback/base_url`), ...problemNodes(`${base}/fallback/reasoning_effort`), ...problemNodes(`${base}/fallback/fallback`));
}
function providerLabel(u) { const p = providerOf(u); return p ? p.label : String(u || '').replace(/^https?:\/\//, ''); }
function suggestFallback(a) {
  // The same family at another provider whose key is present, else nothing.
  const m = lookup(a);
  if (!m) return null;
  const alts = E.cat.models.filter(x => x.family === m.family && x.provider !== m.provider);
  const withKey = alts.filter(x => { const p = E.cat.providers.find(pp => pp.id === x.provider); return p && keyReady(p.key_env); });
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
    let rows = E.cat.models.map(m => ({m, p: provOf(m.provider)})).filter(({m, p}) => p && (!q || `${m.label} ${m.id} ${m.family} ${p.label} ${p.id} ${p.engine || ''} ${p.custom ? 'local custom' : ''} ${(m.note || '')}${m.live ? ' live' : ''}`.toLowerCase().includes(q)));
    // Registered local/custom servers first, then keyed cloud providers, then by family & price.
    rows.sort((x, y) => (!!y.p.custom - !!x.p.custom) || (keyReady(y.p.key_env) - keyReady(x.p.key_env)) || (x.p.custom ? x.p.label.localeCompare(y.p.label) || x.m.id.localeCompare(y.m.id) : x.m.family.localeCompare(y.m.family) || ((x.m.price ? x.m.price.out : 1e9) - (y.m.price ? y.m.price.out : 1e9))));
    list.replaceChildren();
    if (E.cat.live_fetched_at) {
      const errs = Object.entries(E.cat.live_errors || {}).map(([k, v]) => `${k}: ${v}`).join('\n');
      list.append(h('div', {class: 'liveinfo', title: errs || undefined},
        `live provider lists · fetched ${E.cat.live_fetched_at}${E.cat.live_refreshed ? ' (refreshed just now)' : ''}${E.cat.live_stale ? ' (refresh failed, showing cache)' : ''}`));
    }
    let lastGroup = null, lastKeyed = null;
    for (const {m, p} of rows.slice(0, E.cat.live_fetched_at ? 400 : 120)) {
      const keyed = keyReady(p.key_env);
      if (lastKeyed !== null && keyed !== lastKeyed) list.append(h('div', {class: 'pgroup dim'}, 'providers without a key in this shell'));
      lastKeyed = keyed;
      const grp = p.custom ? `${p.label} · local` : m.family;
      if (grp !== lastGroup) { list.append(h('div', {class: 'pgroup'}, grp)); lastGroup = grp; }
      const sel = trimSlash(p.base_url) === trimSlash(target.base_url) && m.id === target.model;
      const ctxStr = m.context ? ctx(m.context) : '';
      const meta = [ctxStr, m.vision ? 'vision' : ''].filter(Boolean).join(' · ');
      list.append(h('button', {class: 'prow' + (sel ? ' on' : '') + (keyed ? '' : ' nokey'), onclick: () => choose(m)},
        h('span', {class: 'kdot' + (keyed ? ' on' : '')}), h('span', {class: 'pl'}, h('span', {class: 'l1'}, h('b', null, m.label), p.custom ? h('span', {class: 'livetag local'}, ENGINE_LABELS[p.engine] || 'local') : (m.live ? h('span', {class: 'livetag'}, 'live') : null), h('span', {class: 'sub'}, ` · ${p.label}${m.protocol && m.protocol !== p.protocol ? ' · ' + m.protocol : ''}`)), m.note ? h('span', {class: 'sub pnote'}, m.note) : null),
        h('span', {class: 'pr'}, m.price ? `$${trimNum(m.price.in)} / $${trimNum(m.price.out)}` : (p.custom ? 'local' : '—'), meta ? h('span', {class: 'sub'}, ` · ${meta}`) : null)));
    }
    if (!rows.length) list.append(h('div', {class: 'sub', style: 'padding:10px'}, 'No model matches. Add a local server above to probe its models automatically.'));
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

// ---- summarizer, agent, behavior, phone, prompts, advanced --------------------------------------
// ownSummarizerRoute is a copy of eagent's route for the summarizer to
// start from, as the preset and the file give it: a value an EAGENT_*
// variable puts in force is the shell's, and the summarizer's route is
// written to the file, so such a value is never copied. Where the layers
// have none, the field is left for the user to pick.
async function ownSummarizerRoute() {
  const c = clone(E.cfg.model); delete c.fallback;
  const pinned = Object.keys(c).filter(k => envVar('/model/' + k));
  if (!pinned.length) return c;
  // A window is its model's: one the environment's model brought goes with it.
  if (pinned.includes('model') && !pinned.includes('context_tokens')) pinned.push('context_tokens');
  let layered = {};
  try { layered = clone(E.base ? (await api(`/api/config/presets/${encodeURIComponent(E.base)}?kind=preset`)).config.model : E.c.defaults.model); } catch { /* no base to read: the pinned fields are left empty */ }
  if (E.base === E.loadedBase) {
    let file = {};
    try { file = JSON.parse(E.c.resolution.file.raw || '{}').model || {}; } catch { /* an unreadable file sets nothing */ }
    if (file.model !== undefined && file.context_tokens === undefined) delete layered.context_tokens;
    Object.assign(layered, file);
  }
  delete layered.fallback;
  for (const k of pinned) { if (layered[k] === undefined || layered[k] === null) delete c[k]; else c[k] = clone(layered[k]); }
  return c;
}

// summarizerCard chooses how the thread's title and summary are kept and,
// when a dedicated call keeps them, the route it calls.
function summarizerCard() {
  const sm = E.cfg.summarizer || (E.cfg.summarizer = {mode: 'inline'});
  const locked = lockedField('/summarizer/mode');
  const mode = sm.mode === 'dedicated' ? 'dedicated' : 'inline';
  const seg = h('div', {class: 'seg'}, ...[['inline', 'Inline', 'eagent keeps the title and summary itself, on the prompt it already has.'], ['dedicated', 'Dedicated', 'A separate small call keeps them, on its own model or on eagent\'s.']].map(([v, t, tip]) =>
    h('button', {class: mode === v ? 'on' : '', disabled: locked, title: tip, onclick: () => { sm.mode = v; rerender(); }}, t)));
  const modeField = field('Title and summary', [seg], [srcNode('/summarizer/mode'), h('span', {class: 'src'}, mode === 'inline' ? 'eagent keeps them itself; no extra model calls.' : 'A separate call keeps them.'), ...problemNodes('/summarizer/mode')]);
  if (mode === 'dedicated' && sm.model && sm.model.model) return routeCard(ROUTES.summarizer, modeField);
  const own = mode === 'dedicated' && !lockedField('/summarizer/model/model')
    ? h('button', {class: 'small', onclick: async () => { try { sm.model = await ownSummarizerRoute(); rerender(); } catch (e) { toast(e.message, 'bad'); } }}, 'Give it its own model')
    : null;
  return h('div', {class: 'card route summarizer'}, h('div', {class: 'rhead'}, h('h3', null, h('span', {class: 'swatch'}), ROUTES.summarizer.name), h('p', {class: 'sub role'}, 'How the thread\'s title and summary are kept.')),
    modeField, mode === 'dedicated' ? h('div', {class: 'row'}, h('span', {class: 'sub'}, 'It calls eagent\'s model.'), own) : null, ...problemNodes('/summarizer/model'));
}
function numField(f, lbl, help, attrs) {
  const ptr = '/' + f;
  const locked = lockedField(ptr);
  return field(lbl, [h('input', Object.assign({type: 'number', value: E.cfg[f] ?? '', disabled: locked, onchange: ev => { const v = parseInt(ev.target.value, 10); if (!isNaN(v)) E.cfg[f] = v; rerender(); }}, attrs || {}))], [srcNode(ptr), help ? h('span', {class: 'src'}, help) : null, ...problemNodes(ptr)]);
}
// rolloverField sets the prompt size at which the conversation restarts
// from a dossier. Left blank it follows the model: half its context window.
function rolloverField() {
  const ptr = '/rollover_tokens';
  const m = E.cfg.model || {};
  const auto = autoRollover(m.context_tokens, m.max_tokens);
  const input = h('input', {type: 'number', min: 20000, step: 10000, value: E.cfg.rollover_tokens || '', placeholder: `${auto.toLocaleString('en-US')} (half the model's window)`,
    'aria-label': 'Context rollover', disabled: lockedField(ptr),
    onchange: ev => { const v = parseInt(ev.target.value, 10); E.cfg.rollover_tokens = isNaN(v) ? 0 : v; rerender(); }});
  return field('Context rollover', [input], [srcNode(ptr), h('span', {class: 'src'}, 'at this prompt size eagent writes a dossier and continues from it in a fresh conversation; blank follows the model'), ...problemNodes(ptr)]);
}
// autoRollover is config.AutoRolloverWithin(window, max_tokens, 0)
// (internal/config/config.go), what Config.Rollover gives: half the window
// in whole thousands, less where the reply ceiling leaves no room, at
// least 20,000, and held inside what the route can take as input.
function autoRollover(win, maxTok) {
  const w = win > 0 ? win : 200000;
  const n = Math.max(Math.floor(Math.min(w / 2, w - (maxTok || 0) - 33000) / 1000) * 1000, 20000);
  const room = w - (maxTok > 0 ? maxTok : 16384);
  if (room <= 0) return n;
  let limit = Math.trunc(room * 3 / 4);
  if (Math.trunc(limit / 100) * 100 > 0) limit = Math.trunc(limit / 100) * 100;
  return Math.min(Math.max(n, Math.trunc(room / 4)), limit);
}
// agentCard picks the coding agent a session starts when it is not told
// which.
function agentCard() {
  const agents = E.c.agents || [];
  const cur = (E.cfg.relay && E.cfg.relay.default) || '';
  const sel = h('select', {'aria-label': 'Default agent', disabled: lockedField('/relay/default'), onchange: ev => {
    const v = ev.target.value;
    if (v) { E.cfg.relay = E.cfg.relay || {}; E.cfg.relay.default = v; }
    else if (E.cfg.relay) { delete E.cfg.relay.default; if (!Object.keys(E.cfg.relay).length) delete E.cfg.relay; }
    rerender();
  }}, h('option', {value: '', selected: !cur}, 'the first one installed'),
    ...agents.map(a => h('option', {value: a.name, selected: a.name === cur}, a.label && a.label !== a.name ? `${a.label} (${a.name})` : a.name)),
    cur && !agents.some(a => a.name === cur) ? h('option', {value: cur, selected: true}, `${cur} (not a known profile)`) : null);
  return h('div', {class: 'card'}, h('h3', null, 'Agent'),
    h('div', {class: 'fields'},
      field('Default agent', [sel], [srcNode('/relay/default'), h('span', {class: 'src'}, 'The coding agent a new session starts when it is not told which. A session can still pick another one, with its own model and effort.'), ...problemNodes('/relay/default')])));
}
function behaviorCard() {
  const locked = lockedField('/persona');
  return h('div', {class: 'card'}, h('h3', null, 'Progress and voice'),
    h('div', {class: 'fields'},
      numField('tick_seconds', 'Look at progress every', 'seconds between progress looks while the agent works; a look with nothing new costs no model call', {min: 5}),
      numField('quiet_seconds', 'Longest silence', 'seconds you go without a progress line while the agent works; 0 turns the limit off', {min: 0}),
      numField('max_calls_per_turn', 'Calls per turn', 'model calls eagent may make each time it wakes: a look at the screen, a key press, another look', {min: 1}),
      rolloverField(),
      numField('report_max_chars', 'Report kept', 'characters of the agent\'s turn report eagent itself reads; you always get the whole report', {min: 500, step: 500}),
      field('Voice', [h('textarea', {rows: 4, disabled: locked, placeholder: 'How eagent should sound. Leave empty for the built-in voice (warm, plain, no catchphrases).', onchange: ev => { E.cfg.persona = ev.target.value; if (!E.cfg.persona) delete E.cfg.persona; rerender(); }}, E.cfg.persona || '')], [srcNode('/persona'), h('span', {class: 'src'}, 'Replaces the persona prompt for new sessions. The full prompts are under Prompts.'), ...problemNodes('/persona')])));
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
  return h('div', {class: 'card'}, h('h3', null, 'Phone mirror'), h('p', {class: 'sub'}, 'Mirrors the conversation to Finalechat on your phone: everything eagent tells you, every question, and the pictures the agent takes.'),
    field('Mirror', [seg], [srcNode('/finalechat/enabled'), ...problemNodes('/finalechat/enabled')]), state, det);
}
function promptsCard() {
  const HELP = {'EAGENT.md': 'what eagent does: passing your words on, watching the agent, answering its terminal, telling you how it goes', 'PERSONA.md': 'eagent\'s voice', 'USER_MESSAGE.md': 'how a message you write is put to eagent', 'USER_ANSWER.md': 'how your answer to a question is put to eagent', 'DOSSIER.md': 'the briefing written before a fresh conversation', 'THREAD-SUMMARY.md': 'how a session\'s title and summary in the thread list are written'};
  return h('div', {class: 'card'}, h('h3', null, 'Prompts'), h('p', {class: 'sub'}, 'The instructions eagent works from. Click one to read or edit it; edits are saved as project overrides.'),
    h('div', {class: 'plist2'}, ...E.c.prompts.map(p => p.retired ? h('button', {class: 'prompt-row', disabled: X.readOnly?.() || !canAction('prompt.reset'), onclick: () => removeRetiredPrompt(p.name)},
      h('code', {class: 'inline'}, p.name), h('span', {class: 'badge blocked'}, 'no longer read'), h('span', {class: 'sub'}, 'An override from the eagent that ran three models. Nothing reads it now. Click to remove it.')) : h('button', {class: 'prompt-row', onclick: () => X.promptEditor(p.name)},
      h('code', {class: 'inline'}, p.name), h('span', {class: 'badge' + (p.source === 'built-in' ? '' : ' completed')}, p.source === 'built-in' ? 'built-in' : 'override'), h('span', {class: 'sub'}, HELP[p.name] || '')))));
}
async function removeRetiredPrompt(name) {
  // In FinaleChat the removal is staged for review there; a sandboxed frame
  // has no confirm().
  if (!X.remote && !confirm(`Remove the override of ${name}? eagent has one prompt now (EAGENT.md, with PERSONA.md), and nothing reads this file. Removing it cannot be undone from here.`)) return;
  try {
    const r = await api(`/api/prompts/${encodeURIComponent(name)}`, {method: 'DELETE'});
    if (r && r.staged) { toast('Ready for review in FinaleChat.'); return; }
    toast(`${name} removed`);
    await reloadCatalogKeepDraft();
  } catch (e) { toast(e.message, 'bad'); }
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
  // loaded: what the form was loaded with, so a value it merely shows (a
  // window an address variable's server gave) is not taken as entered.
  const body = {config: E.cfg, base_preset: E.base, mode: E.mode, if_match: E.etag, loaded: E.loaded};
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
  const desc = h('input', {class: 'search', placeholder: 'what it is for (optional)', style: 'flex:1;min-width:200px'});
  const status = h('span', {class: 'sub'});
  const submit = async () => {
    const n = name.value.trim();
    if (!n) { status.textContent = 'Enter a preset name first.'; name.focus(); return; }
    try {
      const cfg = clone(E.cfg);
      const r = await api('/api/config/bundles', {method: 'POST', body: JSON.stringify({name: n, description: desc.value.trim(), config: cfg})});
      if (r.staged) {
        if (X.threadControls) {
          closeModal();
          toast(`Ready to save preset “${n}”. Tap Save named configuration below.`);
          return;
        }
        status.textContent = 'Ready for review in FinaleChat. The bundle has not changed yet.';
        return;
      }
      closeModal();
      toast(`preset ${r.name} saved`);
      await load(); rerender();
    } catch (e) { status.textContent = e.message; }
  };
  const go = h('button', {class: 'primary', onclick: submit}, X.remote ? 'Review bundle change' : 'Save preset');
  for (const input of [name, desc]) input.addEventListener('keydown', ev => { if (ev.key === 'Enter') { ev.preventDefault(); void submit(); } });
  showModal(h('div', null, h('h2', null, 'Save as preset'),
    h('p', {class: 'sub'}, 'Saves a named copy of this setup in ', h('code', {class: 'inline'}, shortPath(E.c.files.bundles)), '. Pick it from New Session, FinaleChat, or the CLI with ', h('code', {class: 'inline'}, '--preset NAME'), ' / ', h('code', {class: 'inline'}, '--config NAME'), '.'),
    h('div', {class: 'row'}, name, desc),
    h('div', {class: 'foot'}, status, h('span', {style: 'flex:1'}), h('button', {onclick: closeModal}, 'Cancel'), go)));
  name.focus();
}

window.addEventListener('beforeunload', ev => { if (E && changes().length) { ev.preventDefault(); ev.returnValue = ''; } });
window.EagentConfig = {render, stageDraft: () => E && (E.rawDraft !== undefined && E.rawDraft !== E.c.resolution.file.raw ? api('/api/config/raw',{method:'PUT',body:JSON.stringify({raw:E.rawDraft,if_match:E.etag})}) : api('/api/config', {method:'PUT',body:JSON.stringify({config:E.cfg,base_preset:E.base,mode:E.mode,if_match:E.etag})})), dirty: () => !!(E && (changes().length || E.rawDraft !== undefined && E.rawDraft !== E.c.resolution.file.raw)), reset: () => { E = null; },
  draftToken: () => E ? X.canonical({config: E.cfg, base_preset: E.base, mode: E.mode, if_match: E.etag, raw: E.rawDraft}) : '',
  routeResult: (key, r) => { if (!E) return; E.tests[key + (r.route === 'fallback' ? ':fb' : '')] = {busy: false, result: r}; if (r.base_url && r.model) E.cat.checks[`${trimSlash(r.base_url)}|${r.model}|${r.effort}`] = {status: r.status, ms: r.ms, output: r.usage?.output, reasoning: r.usage?.reasoning, error: r.error, at: r.at}; rerender(); }
};
})();
