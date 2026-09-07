/* eagent web UI — vanilla JS, no build step. */
(() => {
'use strict';

// ---- tiny helpers ---------------------------------------------------------
const $ = (sel, el = document) => el.querySelector(sel);
function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  if (attrs) for (const [k, v] of Object.entries(attrs)) {
    if (k === 'class') el.className = v;
    else if (k === 'html') el.innerHTML = v;
    else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else if (v !== null && v !== undefined && v !== false) el.setAttribute(k, v === true ? '' : v);
  }
  for (const c of children.flat()) if (c !== null && c !== undefined && c !== false) el.append(c.nodeType ? c : document.createTextNode(String(c)));
  return el;
}
const esc = s => String(s ?? '').replace(/[&<>"]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]));
const fmtTime = ts => { const d = new Date(ts); return isNaN(d) ? '' : d.toLocaleTimeString([], {hour:'2-digit', minute:'2-digit', second:'2-digit'}); };
const fmtDate = ts => { const d = new Date(ts); return isNaN(d) ? '' : d.toLocaleString([], {month:'short', day:'numeric', hour:'2-digit', minute:'2-digit'}); };
const k = n => n >= 1e6 ? (n/1e6).toFixed(1)+'M' : n >= 1000 ? (n/1000).toFixed(1)+'k' : String(n ?? 0);
const dur = ms => ms >= 60000 ? `${Math.floor(ms/60000)}m${Math.round((ms%60000)/1000)}s` : ms >= 1000 ? (ms/1000).toFixed(1)+'s' : `${ms}ms`;
const clip = (s, n) => { s = String(s ?? '').replace(/\s+/g, ' ').trim(); return s.length > n ? s.slice(0, n-1) + '…' : s; };
async function api(path, opts) {
  const r = await fetch(path, Object.assign({headers: {'Content-Type': 'application/json'}}, opts));
  const body = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(body.error || r.statusText);
  return body;
}

// Minimal markdown: fences, inline code, bold, lists, paragraphs.
function md(text) {
  const out = []; const lines = String(text ?? '').split('\n'); let i = 0;
  while (i < lines.length) {
    const l = lines[i];
    if (l.startsWith('```')) { const buf = []; i++; while (i < lines.length && !lines[i].startsWith('```')) buf.push(lines[i++]); i++; out.push(`<pre>${esc(buf.join('\n'))}</pre>`); continue; }
    if (/^\s*[-*] /.test(l)) { const items = []; while (i < lines.length && /^\s*[-*] /.test(lines[i])) items.push(`<li>${inline(lines[i++].replace(/^\s*[-*] /, ''))}</li>`); out.push(`<ul>${items.join('')}</ul>`); continue; }
    if (/^\s*\d+[.)] /.test(l)) { const items = []; while (i < lines.length && /^\s*\d+[.)] /.test(lines[i])) items.push(`<li>${inline(lines[i++].replace(/^\s*\d+[.)] /, ''))}</li>`); out.push(`<ol>${items.join('')}</ol>`); continue; }
    if (/^#{1,3} /.test(l)) { out.push(`<p><strong>${inline(l.replace(/^#+ /, ''))}</strong></p>`); i++; continue; }
    if (!l.trim()) { i++; continue; }
    const para = []; while (i < lines.length && lines[i].trim() && !lines[i].startsWith('```') && !/^\s*[-*] /.test(lines[i]) && !/^\s*\d+[.)] /.test(lines[i])) para.push(lines[i++]);
    out.push(`<p>${inline(para.join('<br>'))}</p>`);
  }
  return out.join('');
  function inline(s) {
    s = esc(s).replace(/&lt;br&gt;/g, '<br>');
    return s.replace(/`([^`]+)`/g, '<code class="inline">$1</code>').replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
  }
}

// ---- state ------------------------------------------------------------------
const S = { sessions: [], sid: null, tab: 'chat', detail: null, events: [], es: null, showActivity: localStorage.getItem('eagent.activity') === '1', filters: new Set() };

function route() {
  const hash = location.hash.replace(/^#\/?/, '');
  const parts = hash.split('/').filter(Boolean);
  if (parts[0] === 'config') { S.sid = null; S.tab = 'config'; }
  else if (parts[0] === 's' && parts[1]) { S.sid = parts[1]; S.tab = parts[2] || 'chat'; }
  else { S.sid = null; S.tab = 'home'; }
  render();
}
window.addEventListener('hashchange', route);

// ---- sessions list -----------------------------------------------------------
async function loadSessions() {
  try { S.sessions = await api('/api/sessions'); } catch (e) { S.sessions = []; }
  renderSessions();
}
function renderSessions() {
  const ul = $('#sessions'); ul.innerHTML = '';
  if (!S.sessions.length) { ul.append(h('li', {class: 'empty', style: 'display:block'}, 'No sessions yet.')); return; }
  for (const s of S.sessions) {
    ul.append(h('li', {class: s.id === S.sid ? 'active' : '', onclick: () => location.hash = `#/s/${s.id}/chat`},
      h('span', {class: `st ${s.status}`, title: s.status}),
      h('div', null,
        h('div', {class: 'title'}, s.first_message ? clip(s.first_message, 60) : '(no prompt)'),
        h('div', {class: 'meta'}, h('span', null, fmtDate(s.started)), h('span', null, s.status), s.config && h('span', {class: 'cfg'}, s.config)))));
  }
}

// ---- session view ---------------------------------------------------------
async function openSession(id) {
  closeStream();
  S.events = []; S.detail = null;
  try {
    S.detail = await api(`/api/sessions/${id}`);
    S.events = await api(`/api/sessions/${id}/events`);
  } catch (e) { $('#main').replaceChildren(h('div', {class: 'empty'}, h('h2', null, 'Could not load session'), e.message)); return; }
  renderSessionShell();
  renderTab();
  renderLive();
  openStream(id, S.events.length ? S.events[S.events.length-1].seq : 0);
}
function closeStream() { if (S.es) { S.es.close(); S.es = null; } }
function openStream(id, after) {
  const es = new EventSource(`/api/sessions/${id}/stream?after=${after}`);
  S.es = es;
  es.addEventListener('append', e => {
    const ev = JSON.parse(e.data);
    if (S.sid !== id) return;
    S.events.push(ev);
    onEvent(ev);
  });
  es.addEventListener('status', e => {
    if (S.sid !== id) return;
    S.detail = JSON.parse(e.data);
    renderLive();
    if (S.tab === 'tasks') renderTab();
    updateComposer();
    // keep the sidebar status fresh
    const s = S.sessions.find(x => x.id === id); if (s && s.status !== S.detail.status) { s.status = S.detail.status; renderSessions(); }
  });
  es.onerror = () => { /* the browser reconnects */ };
}
function onEvent(ev) {
  if (S.tab === 'chat') { const node = chatNode(ev); if (node) { const sc = $('.chat .scroll'); const atBottom = sc.scrollHeight - sc.scrollTop - sc.clientHeight < 80; sc.append(node); if (atBottom) sc.scrollTop = sc.scrollHeight; } }
  else if (S.tab === 'timeline') { const tb = $('.tl tbody'); if (tb && passesFilter(ev)) tb.append(tlRow(ev)); }
}

function renderSessionShell() {
  const d = S.detail;
  const tabs = ['chat', 'tasks', 'timeline'].map(t => h('a', {href: `#/s/${S.sid}/${t}`, class: S.tab === t ? 'active' : ''}, t[0].toUpperCase() + t.slice(1), t === 'tasks' && d.tasks.length ? ` (${d.tasks.length})` : ''));
  const stopBtn = h('button', {onclick: async () => { if (confirm('Stop this session? It can be resumed later.')) await api(`/api/sessions/${S.sid}/stop`, {method: 'POST', body: '{}'}); }}, 'Stop');
  const resumeBtn = h('button', {onclick: async () => { try { await api(`/api/sessions/${S.sid}/resume`, {method: 'POST', body: '{}'}); } catch (e) { alert(e.message); } }}, 'Resume');
  $('#main').replaceChildren(
    h('div', {class: 'tabs'}, ...tabs, h('span', {class: 'spacer'}), h('div', {class: 'tools'},
      h('span', {class: 'badge'}, `${d.subsessions} file${d.subsessions === 1 ? '' : 's'}`),
      h('span', {class: 'badge', title: 'session id'}, S.sid),
      d.alive ? stopBtn : resumeBtn)),
    h('div', {class: 'pane', id: 'pane'}));
}

function renderTab() {
  const pane = $('#pane'); if (!pane) return;
  if (S.tab === 'chat') renderChat(pane);
  else if (S.tab === 'tasks') renderTasks(pane);
  else if (S.tab === 'timeline') renderTimeline(pane);
}

// ---- live strip ---------------------------------------------------------------
function renderLive() {
  const el = $('#live'); const d = S.detail;
  if (!d) { el.innerHTML = ''; return; }
  const parts = [];
  const live = d.live || {};
  if (d.alive) {
    if (live.Rollover) parts.push('writing dossier for a new subsession');
    else if (live.OrchestratorBusy) parts.push('orchestrator working' + (live.OrchestratorFor ? ' ' + live.OrchestratorFor : ''));
    else if (d.question) parts.push('waiting for your answer');
    else if (d.idle) parts.push(d.done ? 'done, waiting for you' : 'waiting for you');
    else parts.push('running');
    const running = d.tasks.filter(t => t.status === 'running' || t.status === 'queued').length;
    if (running) parts.push(`${running} task${running === 1 ? '' : 's'}`);
    const procs = d.procs.filter(p => p.status === 'running').length;
    if (procs) parts.push(`${procs} proc${procs === 1 ? '' : 's'}`);
  } else parts.push(`session ${d.status}`);
  if (d.context_tokens) parts.push(`ctx ${k(d.context_tokens)}`);
  const orch = d.usage.find(u => u.actor === 'orchestrator');
  if (orch && orch.input) parts.push(`cache ${Math.round(orch.cache_ratio * 100)}%`);
  el.replaceChildren(h('span', {class: 'dot ' + (d.alive ? 'on' : '')}), h('span', null, parts.join(' · ')));
}

// ---- chat ---------------------------------------------------------------------
function renderChat(pane) {
  pane.classList.add('chat'); pane.style.padding = '0';
  const scroll = h('div', {class: 'scroll'});
  for (const ev of S.events) { const n = chatNode(ev); if (n) scroll.append(n); }
  const composer = h('div', {class: 'composer'},
    h('div', {class: 'pending', id: 'pending'}),
    h('form', {onsubmit: e => { e.preventDefault(); send(); }},
      h('textarea', {id: 'input', placeholder: 'Message the agent… (Enter to send, Shift+Enter for a new line)', rows: 1,
        onkeydown: e => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send(); } },
        oninput: e => { e.target.style.height = 'auto'; e.target.style.height = Math.min(200, e.target.scrollHeight) + 'px'; }}),
      h('button', {class: 'primary', type: 'submit'}, 'Send')),
    h('div', {class: 'hint'},
      h('span', {id: 'hint'}),
      h('label', {class: 'toggle'}, h('input', {type: 'checkbox', checked: S.showActivity, onchange: e => { S.showActivity = e.target.checked; localStorage.setItem('eagent.activity', S.showActivity ? '1' : '0'); renderTab(); }}), 'show activity')));
  pane.replaceChildren(scroll, composer);
  scroll.scrollTop = scroll.scrollHeight;
  updateComposer();
}
function updateComposer() {
  const d = S.detail; const pend = $('#pending'); const hint = $('#hint'); if (!pend || !d) return;
  pend.innerHTML = '';
  if (d.question) {
    pend.append(h('span', null, 'Question pending:'), ...(d.question.Options || []).map(o => h('button', {onclick: () => answer(o)}, o)));
  }
  hint.textContent = d.alive ? (d.question ? 'Pick an option above or type your answer.' : 'Delivered to the running session.') : 'The session is not running; sending a message resumes it here.';
}
async function send() {
  const ta = $('#input'); const text = ta.value.trim(); if (!text) return;
  ta.value = ''; ta.style.height = 'auto';
  try {
    if (S.detail && S.detail.question) await api(`/api/sessions/${S.sid}/answer`, {method: 'POST', body: JSON.stringify({text, question_id: S.detail.question.ID})});
    else await api(`/api/sessions/${S.sid}/message`, {method: 'POST', body: JSON.stringify({text})});
  } catch (e) { alert(e.message); }
}
async function answer(text) { try { await api(`/api/sessions/${S.sid}/answer`, {method: 'POST', body: JSON.stringify({text, question_id: S.detail.question.ID})}); } catch (e) { alert(e.message); } }

function chatNode(ev) {
  const d = ev.data || {};
  switch (ev.type) {
    case 'user.message': return h('div', {class: 'msg user'}, h('div', {class: 'who'}, 'you · ' + fmtTime(ev.ts)), h('div', {html: md(d.text)}));
    case 'user.answer': return h('div', {class: 'msg user'}, h('div', {class: 'who'}, `you · answer to ${d.question_id} · ` + fmtTime(ev.ts)), h('div', {html: md(d.text)}));
    case 'narrator.message': return h('div', {class: 'msg'}, h('div', {class: 'who'}, 'eagent · ' + fmtTime(ev.ts)), h('div', {html: md(d.text)}));
    case 'narrator.question': {
      const answered = S.events.find(e => e.type === 'user.answer' && e.data && e.data.question_id === d.id);
      return h('div', {class: 'msg question'}, h('div', {class: 'who'}, 'eagent asks · ' + fmtTime(ev.ts)), h('div', {html: md(d.text)}),
        answered ? h('div', {class: 'answered'}, 'answered: ' + answered.data.text) : h('div', {class: 'options'}, ...(d.options || []).map(o => h('button', {onclick: () => answer(o)}, o))));
    }
    case 'session.start': return h('div', {class: 'notice'}, `session started ${fmtDate(ev.ts)} · ${Object.values(d.models || {}).map(m => m.split('/').pop()).join(' / ')}`);
    case 'session.resume': return h('div', {class: 'notice'}, 'session resumed ' + fmtDate(ev.ts) + (d.closed && d.closed.length ? ' · ' + d.closed.join('; ') : ''));
    case 'session.end': return h('div', {class: 'notice'}, `session ended (${d.reason}) ${fmtDate(ev.ts)}`);
    case 'subsession.start': return d.reason === 'rollover' ? h('div', {class: 'notice'}, `context rolled over into subsession ${d.index + 1}`) : null;
    case 'dossier': return h('div', {class: 'notice'}, 'dossier delivered; the orchestrator continues with a fresh context');
    case 'error': return h('div', {class: 'act bad'}, h('span', {class: 't'}, fmtTime(ev.ts)), h('span', {class: 'k'}, 'error'), h('span', null, clip(d.where + ': ' + d.text, 200)));
  }
  if (!S.showActivity) return null;
  switch (ev.type) {
    case 'note': return act(ev, 'note', 'note', d.text);
    case 'task.create': return act(ev, '', d.kind === 'dossier' ? 'dossier task' : 'delegated ' + d.id, d.title);
    case 'task.end': return act(ev, d.status === 'completed' ? 'ok' : 'bad', `${d.id} ${d.status}`, clip(d.summary, 240));
    case 'yield': return act(ev, d.done ? 'ok' : '', d.done ? 'done' : 'waiting', d.reason);
    case 'schedule.create': return act(ev, '', `schedule ${d.id}`, `${d.kind} ${d.spec}: ${d.note}`);
    case 'schedule.fire': return act(ev, '', `schedule ${d.id} fired`, d.note);
    case 'route': return act(ev, '', 'route', `${d.actor} → ${d.model}`);
  }
  return null;
}
function act(ev, cls, kind, text) { return h('div', {class: 'act ' + cls}, h('span', {class: 't'}, fmtTime(ev.ts)), h('span', {class: 'k'}, kind), h('span', null, clip(text, 300))); }

// ---- tasks ----------------------------------------------------------------------
function renderTasks(pane) {
  pane.classList.remove('chat'); pane.style.padding = '';
  const d = S.detail;
  const sel = S.taskSel;
  const table = h('table', null, h('thead', null, h('tr', null, ...['id', 'title', 'status', 'calls', 'tokens (cache)', 'duration'].map(x => h('th', null, x)))),
    h('tbody', null, ...d.tasks.map(t => h('tr', {class: 'row' + (sel === t.id ? ' open' : ''), onclick: () => { S.taskSel = t.id; renderTab(); }},
      h('td', {class: 'num'}, t.id), h('td', null, t.title || '(untitled)', t.kind === 'dossier' ? h('span', {class: 'badge', style: 'margin-left:6px'}, 'dossier') : null),
      h('td', null, h('span', {class: 'badge ' + t.status}, t.status)), h('td', {class: 'num'}, t.turns),
      h('td', {class: 'num'}, `${k(t.usage.input)} in · ${k(t.usage.output)} out (${Math.round(t.usage.cache_ratio * 100)}%)`),
      h('td', {class: 'num'}, t.ended && t.created ? dur(new Date(t.ended) - new Date(t.created)) : t.created ? dur(Date.now() - new Date(t.created)) + '…' : '')))));
  const stats = h('div', null, ...d.usage.map(u => h('div', {class: 'stat'}, h('span', {class: 'v'}, `${k(u.input)} in`), h('span', {class: 'l'}, `${u.actor} · ${u.calls} calls · ${k(u.output)} out · cache ${Math.round(u.cache_ratio * 100)}%`))));
  const parts = [stats, h('div', {class: 'card'}, h('h3', null, 'Tasks'), d.tasks.length ? table : h('div', {class: 'sub'}, 'Nothing delegated yet.'))];
  if (d.procs.length) parts.push(h('div', {class: 'card'}, h('h3', null, 'Processes'), h('table', null, h('tbody', null, ...d.procs.map(p => h('tr', null, h('td', {class: 'num'}, p.handle), h('td', null, h('span', {class: 'badge ' + p.status}, p.status + (p.status !== 'running' ? ` ${p.exit_code}` : ''))), h('td', null, p.task || p.actor), h('td', null, h('code', {class: 'inline'}, clip(p.command, 120)))))))));
  if (d.schedules.length) parts.push(h('div', {class: 'card'}, h('h3', null, 'Schedules'), ...d.schedules.map(s => h('div', null, h('code', {class: 'inline'}, `${s.id} ${s.kind} ${s.spec}`), ' ', s.note, h('span', {class: 'sub'}, ` · next ${fmtTime(s.next)} · fired ${s.fires}×`)))));
  if (sel) { const t = d.tasks.find(x => x.id === sel); if (t) parts.push(taskDetail(t)); }
  pane.replaceChildren(...parts);
}
function taskDetail(t) {
  const evs = S.events.filter(e => e.task === t.id);
  const created = S.events.find(e => e.type === 'task.create' && e.data && e.data.id === t.id);
  const card = h('div', {class: 'card'},
    h('h3', null, `${t.id} · ${t.title}`, ' ', h('span', {class: 'badge ' + t.status}, t.status)),
    created && h('details', null, h('summary', null, 'task description'), h('pre', null, created.data.description)),
    t.summary && h('div', null, h('div', {class: 'sub'}, 'report'), h('div', {html: md(t.summary)})),
    h('div', {class: 'sub', style: 'margin-top:10px'}, `${t.turns} model calls`),
    ...turns(evs));
  return card;
}
// Render assistant events with their tool calls and results.
function turns(evs) {
  const results = new Map();
  for (const e of evs) if (e.type === 'tool.result') results.set(e.data.call_id, e.data);
  const out = [];
  for (const e of evs) {
    const d = e.data || {};
    if (e.type === 'assistant') {
      out.push(h('div', {class: 'turn'},
        h('div', {class: 'hd'}, h('span', {class: 'actor'}, e.actor), h('span', null, fmtTime(e.ts)), h('span', null, dur(d.elapsed_ms || 0)), h('span', {class: 'num'}, `${k(d.usage?.input)} in · ${k(d.usage?.output)} out`), d.stop === 'length' && h('span', {class: 'badge failed'}, 'cut off')),
        d.reasoning && h('details', null, h('summary', null, 'reasoning'), h('div', {class: 'reasoning'}, d.reasoning)),
        d.text && h('div', {class: 'text'}, d.text),
        ...(d.tool_calls || []).map(tc => { const r = results.get(tc.id); return h('div', {class: 'call'},
          h('span', {class: 'name'}, tc.name), ' ', h('span', {class: 'sub'}, clip(argPreview(tc.args), 160)),
          h('details', null, h('summary', null, 'arguments'), h('pre', null, JSON.stringify(tc.args, null, 2))),
          r ? h('div', {class: 'res' + (r.is_error ? ' err' : '')}, h('details', {open: r.output && r.output.length < 600}, h('summary', null, r.is_error ? 'error' : `result (${r.output ? r.output.length : 0} chars)`), h('pre', null, r.output))) : h('div', {class: 'res sub'}, 'no result recorded')); })));
    } else if (e.type === 'harness.message') out.push(h('div', {class: 'turn'}, h('div', {class: 'hd'}, h('span', {class: 'actor'}, 'harness'), fmtTime(e.ts)), h('div', {class: 'text'}, d.text)));
  }
  return out;
}
function argPreview(args) {
  if (!args || typeof args !== 'object') return String(args ?? '');
  for (const key of ['command', 'path', 'title', 'text', 'reason', 'handle', 'query', 'spec']) if (typeof args[key] === 'string') return args[key];
  return JSON.stringify(args);
}

// ---- timeline -----------------------------------------------------------------------
const ACTORS = ['user', 'orchestrator', 'task', 'narrator', 'harness'];
function passesFilter(ev) { return S.filters.size === 0 || S.filters.has(ev.actor); }
function renderTimeline(pane) {
  pane.classList.remove('chat'); pane.style.padding = '';
  const chips = ACTORS.map(a => h('span', {class: 'chip' + (S.filters.has(a) ? ' on' : ''), onclick: () => { S.filters.has(a) ? S.filters.delete(a) : S.filters.add(a); renderTab(); }}, a));
  const tbody = h('tbody', null, ...S.events.filter(passesFilter).map(tlRow));
  pane.replaceChildren(h('div', {class: 'filters'}, h('span', {class: 'sub'}, 'actor:'), ...chips, h('span', {class: 'sub', style: 'margin-left:auto'}, `${S.events.length} events`)),
    h('table', {class: 'tl'}, h('thead', null, h('tr', null, h('th', null, '#'), h('th', null, 'time'), h('th', null, 'actor'), h('th', null, 'type'), h('th', null, 'summary'))), tbody));
}
function tlRow(ev) {
  const tr = h('tr', {class: 'row', onclick: () => { tr.classList.toggle('open'); const s = tr.querySelector('.sum'); s.textContent = tr.classList.contains('open') ? JSON.stringify(ev.data, null, 2) : summary(ev); }},
    h('td', {class: 'num'}, ev.seq), h('td', {class: 'num'}, fmtTime(ev.ts)), h('td', null, ev.task ? `${ev.actor} ${ev.task}` : ev.actor), h('td', {class: 'type'}, ev.type), h('td', {class: 'sum'}, summary(ev)));
  tr.title = `${ev.file}:${ev.line}`;
  return tr;
}
function summary(ev) {
  const d = ev.data || {};
  switch (ev.type) {
    case 'assistant': return [d.text, ...(d.tool_calls || []).map(t => `${t.name}(${clip(argPreview(t.args), 80)})`)].filter(Boolean).join(' | ');
    case 'tool.result': return `${d.name}${d.is_error ? ' ERROR' : ''} → ${clip(d.output, 200)}`;
    case 'user.message': case 'narrator.message': case 'note': case 'harness.message': case 'dossier': return clip(d.text, 240);
    case 'task.create': return `${d.id} ${d.title}`;
    case 'task.end': return `${d.id} ${d.status}: ${clip(d.summary, 200)}`;
    case 'proc.start': return `${d.handle} $ ${clip(d.command, 200)}`;
    case 'proc.exit': return `${d.handle} ${d.reason} exit=${d.exit_code}`;
    case 'yield': return (d.done ? 'DONE: ' : 'waiting: ') + d.reason;
    default: return clip(JSON.stringify(d), 200);
  }
}

// ---- config ---------------------------------------------------------------------------
async function renderConfig() {
  closeStream(); S.detail = null; renderLive();
  let c;
  try { c = await api('/api/config'); } catch (e) { $('#main').replaceChildren(h('div', {class: 'empty'}, h('h2', null, 'Configuration unavailable'), e.message)); return; }
  const eff = c.effective;
  const models = h('div', {class: 'card'}, h('h3', null, 'Active configuration', eff.name ? h('span', {class: 'badge', style: 'margin-left:8px'}, `bundle ${eff.name}`) : eff.preset ? h('span', {class: 'badge', style: 'margin-left:8px'}, `preset ${eff.preset}`) : null),
    h('div', {class: 'kv'}, ...['orchestrator', 'task', 'narrator'].flatMap(a => { const x = eff[a]; return [h('span', {class: 'k'}, a), h('span', null, h('code', {class: 'inline'}, x.model), ` · ${x.protocol} · ${x.base_url.replace(/^https?:\/\//, '')}`, x.reasoning_effort ? ` · effort ${x.reasoning_effort}` : '', x.fallback ? ` · fallback ${x.fallback.model}` : '')]; }),
      h('span', {class: 'k'}, 'task concurrency'), h('span', null, eff.task_concurrency), h('span', {class: 'k'}, 'rollover at'), h('span', null, `${k(eff.rollover_tokens)} tokens`), h('span', {class: 'k'}, 'max task calls'), h('span', null, eff.max_task_turns)),
    h('div', {style: 'margin-top:10px'}, ...Object.entries(c.keys).sort().map(([env, on]) => h('span', {class: 'key'}, h('span', {class: 'd' + (on ? ' on' : '')}), env))));
  const bundleRows = (list, kind) => h('table', null, h('tbody', null, ...list.map(b => h('tr', null, h('td', null, h('code', {class: 'inline'}, b.name), b.active ? h('span', {class: 'badge completed', style: 'margin-left:6px'}, 'active') : null), h('td', null, b.models), h('td', {class: 'sub'}, b.invalid ? 'invalid: ' + b.invalid : b.description),
    h('td', null, h('button', {onclick: () => newSessionDialog(kind === 'preset' ? {preset: b.name} : {config: b.name})}, 'New session'))))));
  const presets = h('div', {class: 'card'}, h('h3', null, 'Built-in presets'), bundleRows(c.presets, 'preset'), h('div', {class: 'sub', style: 'margin-top:8px'}, 'Use one with ', h('code', {class: 'inline'}, 'eagent --preset NAME …')));
  const bundles = h('div', {class: 'card'}, h('h3', null, 'Project bundles'), c.bundles.length ? bundleRows(c.bundles, 'bundle') : h('div', {class: 'sub'}, 'None yet.'),
    h('div', {class: 'sub', style: 'margin-top:8px'}, 'Bundles live in ', h('code', {class: 'inline'}, c.files.bundles), ' and are meant to be committed. Save the current configuration as one with ', h('code', {class: 'inline'}, 'eagent config save NAME "description"'), '; select with ', h('code', {class: 'inline'}, '--config NAME'), ' or ', h('code', {class: 'inline'}, 'EAGENT_CONFIG=NAME'), '.'));
  const promptsCard = h('div', {class: 'card'}, h('h3', null, 'Prompts'), h('table', null, h('tbody', null, ...c.prompts.map(p => h('tr', {class: 'row', onclick: async () => { const r = await api(`/api/prompts/${p.name}`); showModal(h('div', null, h('h2', null, p.name, ' ', h('span', {class: 'badge'}, r.source)), h('pre', null, r.text), h('div', {class: 'foot'}, h('button', {onclick: closeModal}, 'Close')))); }},
    h('td', null, h('code', {class: 'inline'}, p.name)), h('td', {class: 'sub'}, p.source))))),
    h('div', {class: 'sub', style: 'margin-top:8px'}, 'Override any prompt by putting a file with the same name in ', h('code', {class: 'inline'}, c.files.prompts), ' (', h('code', {class: 'inline'}, 'eagent prompts export'), ' copies the defaults there).'));
  const raw = h('div', {class: 'card'}, h('h3', null, 'Effective configuration'), h('pre', null, JSON.stringify(eff, null, 2)), h('div', {class: 'sub'}, 'Project file: ', h('code', {class: 'inline'}, c.files.config), ' · ', h('code', {class: 'inline'}, 'eagent config'), ' prints this.'));
  $('#main').replaceChildren(h('div', {class: 'pane'}, models, presets, bundles, promptsCard, raw));
}

// ---- new session -------------------------------------------------------------------------
async function newSessionDialog(pre = {}) {
  let c = null; try { c = await api('/api/config'); } catch (e) {}
  const options = [];
  if (c) { for (const p of c.presets) options.push(h('option', {value: 'preset:' + p.name, selected: pre.preset === p.name}, `preset · ${p.name} — ${p.models}`)); for (const b of c.bundles) options.push(h('option', {value: 'config:' + b.name, selected: pre.config === b.name}, `bundle · ${b.name} — ${b.models}`)); }
  const ta = h('textarea', {placeholder: 'What should the agent do? Be as specific as you like; it will ask if it needs a decision.'});
  const sel = h('select', null, h('option', {value: ''}, 'default configuration'), ...options);
  const start = h('button', {class: 'primary', onclick: async () => {
    const prompt = ta.value.trim(); if (!prompt) return;
    start.disabled = true;
    const [kind, name] = sel.value.split(':');
    try { const r = await api('/api/sessions', {method: 'POST', body: JSON.stringify({prompt, preset: kind === 'preset' ? name : '', config: kind === 'config' ? name : ''})}); closeModal(); await loadSessions(); location.hash = `#/s/${r.id}/chat`; }
    catch (e) { alert(e.message); start.disabled = false; }
  }}, 'Start');
  showModal(h('div', null, h('h2', null, 'New session'), ta, h('div', {class: 'row'}, h('span', {class: 'sub'}, 'configuration'), sel), h('div', {class: 'foot'}, h('button', {onclick: closeModal}, 'Cancel'), start)));
  ta.focus();
}
function showModal(content) { const m = $('#modal'); m.replaceChildren(h('div', {class: 'box'}, content)); m.classList.remove('hidden'); m.onclick = e => { if (e.target === m) closeModal(); }; }
function closeModal() { $('#modal').classList.add('hidden'); }

// ---- render root -------------------------------------------------------------------------
function renderHome() {
  closeStream(); S.detail = null; renderLive();
  $('#main').replaceChildren(h('div', {class: 'empty'}, h('h2', null, 'eagent'),
    h('p', null, 'Pick a session on the left, or start a new one.'),
    h('p', null, h('button', {class: 'primary', onclick: () => newSessionDialog()}, 'New session'))));
}
function render() {
  renderSessions();
  if (S.tab === 'config') return renderConfig();
  if (!S.sid) return renderHome();
  if (S.detail && S.detail.id === S.sid) { renderSessionShell(); renderTab(); renderLive(); return; }
  openSession(S.sid);
}

// ---- boot --------------------------------------------------------------------------------
$('#btn-new').onclick = () => newSessionDialog();
$('#btn-refresh').onclick = loadSessions;
api('/api/health').then(hh => { $('#project').textContent = hh.project; }).catch(() => {});
loadSessions().then(route);
setInterval(loadSessions, 15000);
})();
