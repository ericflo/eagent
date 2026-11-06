/* Artifact transport for the shared eagent editor. No localhost HTTP access. */
(() => {
  'use strict';
  const $ = (selector, root = document) => root.querySelector(selector);
  const clone = value => JSON.parse(JSON.stringify(value));
  const canonical = value => JSON.stringify(value, (_key, item) => item && typeof item === 'object' && !Array.isArray(item) ? Object.fromEntries(Object.keys(item).sort().map(key => [key, item[key]])) : item);
  let resource, data, editable = false, active = false, stopped = false, inputEpoch = 0, proposalSeq = 0, activeDraft = '', activeKey = '';
  const staged = new Map();
  window.addEventListener('pagehide', () => { stopped = true; });
  function h(tag, attrs, ...children) {
    const el = document.createElement(tag);
    for (const [key, value] of Object.entries(attrs || {})) {
      if (key === 'class') el.className = value;
      else if (key.startsWith('on')) el.addEventListener(key.slice(2), value);
      else if (value !== null && value !== undefined && value !== false) el.setAttribute(key, value === true ? '' : value);
    }
    for (const child of children.flat()) if (child !== null && child !== undefined && child !== false) el.append(child.nodeType ? child : document.createTextNode(String(child)));
    return el;
  }
  function message(text) { $('#artifact-status').textContent = text; }
  function toast(text, kind) { const item = h('div', { class: 'toast show ' + (kind || '') }, text); $('#toasts').append(item); setTimeout(() => item.remove(), 6000); }
  function showModal(content) { $('#modal').replaceChildren(h('div', { class: 'box' }, content)); $('#modal').classList.remove('hidden'); }
  function closeModal() { $('#modal').classList.add('hidden'); }
  function field(key) { return resource?.descriptor.fields.find(item => item.key === key); }
  function canEdit(key) { return editable && !!field(key)?.writable; }
  function canAct(operation) { return editable && !!resource?.descriptor.actions?.some(action => action.operation === operation); }
  function assign(root, key, value) {
    const parts = key.split('/').slice(1);
    if (parts.some(part => ['__proto__', 'prototype', 'constructor'].includes(part))) throw new Error('Unsupported configuration key.');
    let at = root;
    for (const part of parts.slice(0, -1)) at = at[part] ||= {};
    at[parts.at(-1)] = clone(value);
  }
  function expand(flat) { const value = {}; for (const [key, item] of Object.entries(flat || {})) assign(value, key, item); return value; }
  function flatten(value, prefix = '', out = {}) {
    if (value && typeof value === 'object' && !Array.isArray(value)) for (const [key, item] of Object.entries(value)) flatten(item, prefix + '/' + key, out);
    else out[prefix] = value;
    return out;
  }
  function configView() {
    const snapshot = resource.snapshot, details = snapshot.details || {}, effective = expand(snapshot.effective), saved = expand(snapshot.saved);
    const models = cfg => ['orchestrator', 'task', 'narrator'].map(actor => cfg[actor]?.model || '').join(' · ');
    const presets = Object.entries(data.presets).map(([name, config]) => ({ name, description: config.description, models: models(config) }));
    const bundles = (details.bundles || []).map(name => {
      const entry = data.bundles[name], valid = entry?.config && entry.sha256 === details.context_digests?.['bundle:' + name];
      return { name, description: valid ? entry.config.description : '', models: valid ? models(entry.config) : '', invalid: valid ? '' : entry?.invalid || 'This bundle changed since capture. Reopen the latest artifact to load it.' };
    });
    const project = details.project || snapshot.context || '';
    return { project, defaults: data.defaults, presets, bundles, prompts: Object.entries(details.prompts || {}).map(([name, value]) => ({ name, source: value.source })), keys: details.keys_present || {}, running: [], server: {},
      files: { config: project + '/.agents/eagent/config.json', bundles: project + '/.agents/eagent/configs', prompts: project + '/.agents/eagent/prompts' },
      phone: { state: effective.finalechat?.enabled === false ? 'disabled' : 'configured', detail: 'Mirror defaults are shown here. Live chat delivery and credentials remain with the integration.' },
      resolution: { effective, sources: details.sources || {}, active: details.active || { kind: '', name: '' }, preset: effective.preset || '', file: { exists: !!details.file_etag, etag: snapshot.version, raw: JSON.stringify(saved, null, 2), preset: saved.preset || '', default_config: saved.default_config || '', path: project + '/.agents/eagent/config.json', unknown_keys: [] } } };
  }
  const draftToken = () => canonical([window.EagentConfig?.draftToken(), inputEpoch]);
  async function clear() { proposalSeq++; active = false; activeKey = ''; if (editable) await finale.settings.clear().catch(() => {}); }
  let stageTimer;
  function formChanged() {
    if (window.__eagent.threadControls) {
      void clear(); clearTimeout(stageTimer);
      stageTimer=setTimeout(()=>{ if(window.EagentConfig.dirty()) void window.EagentConfig.stageDraft().catch(error=>{message(error.message);toast(error.message,'bad');}); },180);
    } else if (active && activeDraft !== draftToken()) { void clear(); message('The form changed. Review its new changes before submitting in FinaleChat.'); }
  }
  async function stage(proposal, meta = {}) {
    clearTimeout(stageTimer);
    if (!editable) throw new Error('This snapshot is read only. Open current settings in FinaleChat to edit.');
    if(proposal.operation === 'settings.apply' && !proposal.edits?.length) { await clear(); return {staged:true}; }
    const p = { ...proposal, schema_version: resource.descriptor.schema_version, expected_version: meta.version || resource.snapshot.version, generation: resource.generation || '' };
    const key = canonical(p), seq = ++proposalSeq;
    meta.draftToken = draftToken();
    staged.set(key, meta);
    while (staged.size > 32) staged.delete(staged.keys().next().value);
    try { await finale.settings.propose(p); if (seq === proposalSeq) { active = true; activeKey = key; activeDraft = meta.draftToken; } }
    catch (error) { staged.delete(key); throw error; }
    message(window.__eagent.threadControls ? 'Unsaved changes' : 'Ready for review in FinaleChat. No settings have changed yet.');
    return { staged: true };
  }
  function editsFor(values, current, skipLocked) {
    const proposed = flatten(values), edits = [];
    for (const key of Object.keys(proposed)) if (!field(key)) throw new Error('Unsupported setting: ' + key);
    for (const key of new Set([...Object.keys(proposed), ...Object.keys(current)])) {
      const desired = proposed[key], present = Object.hasOwn(proposed, key) && desired !== null;
      if (canonical(current[key]) === canonical(desired) && Object.hasOwn(current, key) === present) continue;
      if (!canEdit(key)) { if (skipLocked) continue; throw new Error(field(key)?.locked_reason || 'This setting is read only: ' + key); }
      edits.push(present ? { op: 'set', key, value: desired } : { op: 'unset', key });
    }
    if (!edits.length) throw new Error('There are no writable changes to review.');
    return edits;
  }
  async function api(path, options = {}) {
    const url = new URL(path, 'https://artifact.invalid'), method = options.method || 'GET';
    try {
      const body = options.body ? JSON.parse(options.body) : {};
      if (method === 'GET' && url.pathname === '/api/config') return configView();
      if (method === 'GET' && url.pathname === '/api/catalog') return { ...data.catalog, keys: resource.snapshot.details?.keys_present || {}, checks: {} };
      if (method === 'GET' && url.pathname.startsWith('/api/config/presets/')) {
        const name = decodeURIComponent(url.pathname.slice('/api/config/presets/'.length));
        if (url.searchParams.get('kind') !== 'bundle' && Object.hasOwn(data.presets, name)) return { name, config: clone(data.presets[name]) };
        const bundle = configView().bundles.find(item => item.name === name);
        if (!bundle || bundle.invalid) throw new Error(bundle?.invalid || 'No captured preset or bundle with that name.');
        return { name, config: clone(data.bundles[name].config) };
      }
      if (method === 'GET' && url.pathname.startsWith('/api/prompts/')) {
        const name = decodeURIComponent(url.pathname.slice('/api/prompts/'.length)), prompt = resource.snapshot.details?.prompts?.[name];
        if (!prompt || typeof prompt.text !== 'string') throw new Error('This prompt is not included in the bounded snapshot. Use the local editor to inspect it.');
        return { name, ...prompt };
      }
      if (method === 'PUT' && url.pathname === '/api/config') {
        const cfg = clone(body.config), base = body.base_preset ? data.presets[body.base_preset] : data.defaults;
        if (!base) throw new Error('The base preset is unavailable in this archive.');
        const baseValues = flatten(base), loaded = resource.snapshot.effective, desired = clone(resource.snapshot.saved), form = flatten(cfg);
        const omitted = new Set(['/name', '/description', '/instructions', '/default_config', '/preset']);
        for (const key of new Set([...Object.keys(form), ...Object.keys(loaded)])) {
          if (omitted.has(key)) continue;
          if (!field(key)) throw new Error('Unsupported setting: ' + key);
          if (!canEdit(key)) continue;
          if (body.mode !== 'pin' && canonical(form[key]) === canonical(loaded[key]) && body.base_preset === (resource.snapshot.saved['/preset'] || '')) continue;
          if (form[key] === undefined || form[key] === null || body.mode !== 'pin' && canonical(form[key]) === canonical(baseValues[key])) delete desired[key];
          else desired[key] = form[key];
        }
        if (body.base_preset) desired['/preset'] = body.base_preset; else delete desired['/preset'];
        return await stage({ operation: 'settings.apply', edits: editsFor(expand(desired), resource.snapshot.saved, true) }, { version: body.if_match });
      }
      if (method === 'PUT' && url.pathname === '/api/config/raw') {
        const values = JSON.parse(body.raw);
        if (!values || typeof values !== 'object' || Array.isArray(values)) throw new Error('Known overrides must be a JSON object.');
        return await stage({ operation: 'settings.apply', edits: editsFor(values, resource.snapshot.saved, false) }, { version: body.if_match });
      }
      if (method === 'POST' && url.pathname === '/api/config/test') {
        const actor = body.actor_key, route = body.route || 'primary';
        if (!['orchestrator', 'task', 'narrator'].includes(actor) || !['primary', 'fallback'].includes(route)) throw new Error('Select a saved actor route.');
        const saved = expand(resource.snapshot.effective)[actor];
        const selected = route === 'fallback' ? body.actor?.fallback : body.actor, current = route === 'fallback' ? saved?.fallback : saved;
        const routeIdentity = value => Object.fromEntries(Object.entries(value || {}).filter(([key]) => !['reasoning_effort', 'fallback'].includes(key)));
        if (!selected || !current || canonical(routeIdentity(selected)) !== canonical(routeIdentity(current))) throw new Error('Save this model route before testing it remotely. Each test uses the reviewed saved route.');
        return await stage({ operation: 'route.test', parameters: { actor, route, effort: body.effort ?? selected.reasoning_effort ?? '' } }, { routeKey: actor, route });
      }
      if (method === 'POST' && url.pathname === '/api/config/bundles') return await stage({ operation: 'bundle.save', parameters: { name: body.name, description: body.description || '', config_json: JSON.stringify(body.config) } });
      if (method === 'POST' && url.pathname === '/api/config/delete-bundle') return await stage({ operation: 'bundle.delete', parameters: { name: body.name } });
      if (method === 'POST' && url.pathname === '/api/config/default-bundle') {
        if (!canEdit('/default_config')) throw new Error('The default configuration is read only.');
        if (body.name && !configView().bundles.some(bundle => bundle.name === body.name)) throw new Error('Select a registered named configuration.');
        return await stage({ operation: 'settings.apply', edits: [body.name ? { op: 'set', key: '/default_config', value: body.name } : { op: 'unset', key: '/default_config' }] });
      }
      if (url.pathname.startsWith('/api/prompts/') && ['PUT', 'DELETE'].includes(method)) {
        const name = decodeURIComponent(url.pathname.slice('/api/prompts/'.length));
        return await stage({ operation: method === 'PUT' ? 'prompt.set' : 'prompt.reset', parameters: { name, ...(method === 'PUT' ? { text: body.text } : {}) } });
      }
      throw new Error('This operation is not available through the artifact settings transport.');
    } catch (error) { if (method !== 'GET') await clear(); throw error; }
  }
  async function promptEditor(name) {
    try {
      const prompt = await api('/api/prompts/' + encodeURIComponent(name));
      const text = h('textarea', { class: 'code', style: 'width:100%;min-height:45vh', disabled: !canAct('prompt.set') }, prompt.text);
      const status = h('p', { class: 'sub' }, window.__eagent.threadControls ? 'Edit these instructions, then tap Save below.' : 'The connector validates the Go template before saving.');
      const action = async method => { try { await api('/api/prompts/' + encodeURIComponent(name), { method, body: method === 'PUT' ? JSON.stringify({ text: text.value }) : undefined }); status.textContent = window.__eagent.threadControls ? 'Ready to save. Tap Save below.' : 'Ready for review in FinaleChat. The local prompt has not changed yet.'; } catch (error) { status.textContent = error.message; } };
      if(window.__eagent.threadControls) text.addEventListener('input',()=>{ void clear(); clearTimeout(stageTimer); stageTimer=setTimeout(()=>void action('PUT'),180); });
      showModal(h('div', null, h('h2', null, name), text, status, h('div', { class: 'foot' }, h('button', { disabled: !canAct('prompt.reset'), onclick: () => action('DELETE') }, 'Review reset to built-in'), h('button', { onclick: closeModal }, 'Close'), window.__eagent.threadControls ? null : h('button', { class: 'primary', disabled: !canAct('prompt.set'), onclick: () => action('PUT') }, 'Review prompt change'))));
    } catch (error) { toast(error.message, 'bad'); }
  }
  async function refresh() { const current = await finale.settings.read(); resource = current.resource; editable = !!current.editable; window.__eagent.threadControls = current.presentation === "thread"; document.body.classList.toggle("thread-controls",window.__eagent.threadControls); }
  async function load() {
    try {
      data = JSON.parse(await finale.text('settings/editor.json'));
      try { await refresh(); } catch { resource = JSON.parse(await finale.text('settings/state.json')); editable = false; }
      message(editable ? 'Editing current project defaults. Picker catalogs are captured with this archive; runtime adoption is unconfirmed.' : 'Saved snapshot, read only. These are defaults at capture time; runtime adoption is unconfirmed.');
      window.EagentConfig.reset(); await window.EagentConfig.render();
    } catch (error) { message(error.message); }
  }
  finale.settings.onResult(async command => {
    const meta = staged.get(canonical(command.proposal));
    if (!meta) return;
    const key = canonical(command.proposal);
    staged.delete(key);
    if (activeKey === key) { active = false; activeKey = ''; }
    if (meta.routeKey) {
      window.EagentConfig.routeResult(meta.routeKey, command.result?.route_test || { route: meta.route, status: 'failed', error: command.result?.message || 'Command outcome: ' + command.status, usage: {} });
      message('Route test outcome: ' + command.status + '. Usage and provider results are shown below.'); return;
    }
    if (command.status !== 'succeeded') { message(command.result?.message || 'Command outcome: ' + command.status); return; }
    message('Saved by the connector. Waiting for its current settings snapshot…');
    for (let attempt = 0; attempt < 20 && !stopped; attempt++) {
      try {
        const current = await finale.settings.read();
        if (current.resource.snapshot.version !== command.proposal.expected_version) {
          if (meta.draftToken === draftToken()) {
            await clear();
            resource = current.resource; editable = !!current.editable; window.EagentConfig.reset(); await window.EagentConfig.render(); message('Saved. Showing the connector’s current defaults.');
          } else message('The reviewed changes were saved. Your newer local edits remain; reload current defaults before reviewing them again.');
          return;
        }
      } catch { /* The saved result remains durable while publication catches up. */ }
      await new Promise(resolve => setTimeout(resolve, 1000));
    }
    if (!stopped) message('Saved. The updated snapshot is not available yet; reload current defaults when the connector publishes it.');
  });
  window.__eagent = { $, h, api, toast, toastHost: () => $('#toasts'), showModal, closeModal, promptEditor, canonical, remote: true, canEdit, canAct, formChanged,
    readOnly: () => !editable, field, S: {}, closeStream() {}, renderLive() {},
    k: n => n >= 1000000 ? (n / 1000000).toFixed(1) + 'M' : n >= 1000 ? (n / 1000).toFixed(1) + 'k' : String(n ?? 0),
    money: value => value > 0 && value < 0.01 ? '<$0.01' : '$' + Number(value || 0).toFixed(2) };
  document.addEventListener('input', event => { inputEpoch++; if(window.__eagent.threadControls && event.target.closest('#modal')) return; formChanged(); });
  $('#artifact-local').onclick = () => finale.openLocalFiles().catch(error => message(error.message));
  $('#artifact-reload').onclick = () => {
    const reload = async () => { closeModal(); await clear(); await load(); };
    if (window.EagentConfig.dirty()) showModal(h('div', null, h('h2', null, 'Reload current defaults?'), h('p', null, 'This discards the changes currently in this form.'), h('div', { class: 'foot' }, h('button', { onclick: closeModal }, 'Keep editing'), h('button', { class: 'primary', onclick: reload }, 'Discard edits and reload'))));
    else void reload();
  };
  finale.on('local', load);
  finale.ready.then(connected => { if (connected) { $('#artifact-local').hidden = true; void load(); } });
})();
