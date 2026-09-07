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
function svg(tag, attrs, ...children) {
  const el = document.createElementNS('http://www.w3.org/2000/svg', tag);
  if (attrs) for (const [k, v] of Object.entries(attrs)) { if (k.startsWith('on')) el.addEventListener(k.slice(2), v); else el.setAttribute(k, v); }
  for (const c of children.flat()) if (c !== null && c !== undefined && c !== false) el.append(c.nodeType ? c : document.createTextNode(String(c)));
  return el;
}
const esc = s => String(s ?? '').replace(/[&<>"]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]));
const fmtTime = ts => { const d = new Date(ts); return isNaN(d) ? '' : d.toLocaleTimeString([], {hour:'2-digit', minute:'2-digit', second:'2-digit', hour12: false}); };
const rel = (ts, start) => { const ms = new Date(ts) - start; if (isNaN(ms)) return ''; const s = Math.max(0, ms) / 1000; const m = Math.floor(s / 60); return m >= 60 ? `+${Math.floor(m/60)}:${String(m%60).padStart(2,'0')}:${String(Math.floor(s%60)).padStart(2,'0')}` : `+${m}:${(s%60).toFixed(1).padStart(4,'0')}`; };
const fmtDate = ts => { const d = new Date(ts); return isNaN(d) ? '' : d.toLocaleString([], {month:'short', day:'numeric', hour:'2-digit', minute:'2-digit'}); };
const k = n => n >= 1e6 ? (n/1e6).toFixed(1)+'M' : n >= 1000 ? (n/1000).toFixed(1)+'k' : String(n ?? 0);
const dur = ms => ms >= 3600000 ? `${Math.floor(ms/3600000)}h${Math.round((ms%3600000)/60000)}m` : ms >= 60000 ? `${Math.floor(ms/60000)}m${Math.round((ms%60000)/1000)}s` : ms >= 1000 ? (ms/1000).toFixed(1)+'s' : `${ms}ms`;
const money = usd => usd >= 1 ? '$' + usd.toFixed(2) : usd >= 0.01 ? '$' + usd.toFixed(2) : usd > 0 ? '<$0.01' : '$0';
const clip = (s, n) => { s = String(s ?? '').replace(/\s+/g, ' ').trim(); return s.length > n ? s.slice(0, n-1) + '…' : s; };
async function api(path, opts) {
  const r = await fetch(path, Object.assign({headers: {'Content-Type': 'application/json'}}, opts));
  const body = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(body.error || r.statusText);
  return body;
}
function toast(text, kind) {
  const t = h('div', {class: 'toast ' + (kind || '')}, text);
  document.body.append(t);
  setTimeout(() => t.classList.add('show'), 10);
  setTimeout(() => { t.classList.remove('show'); setTimeout(() => t.remove(), 300); }, 3200);
}

// Minimal markdown: fences, inline code, bold, lists, paragraphs.
function md(text) {
  const out = []; const lines = String(text ?? '').split('\n'); let i = 0;
  while (i < lines.length) {
    const l = lines[i];
    if (l.startsWith('```')) { const buf = []; i++; while (i < lines.length && !lines[i].startsWith('```')) buf.push(lines[i++]); i++; out.push(`<pre class="code"><button class="copy" data-copy>copy</button>${esc(buf.join('\n'))}</pre>`); continue; }
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
document.addEventListener('click', e => {
  const b = e.target.closest('[data-copy]'); if (!b) return;
  const pre = b.parentElement; const text = pre.innerText.replace(/^copy\n?/, '');
  navigator.clipboard?.writeText(text).then(() => { b.textContent = 'copied'; setTimeout(() => b.textContent = 'copy', 1200); });
});

// ---- state ------------------------------------------------------------------
const S = { sessions: [], sid: null, tab: 'chat', detail: null, events: [], lastSeq: 0, es: null, showActivity: localStorage.getItem('eagent.activity') === '1', filters: new Set(), search: '', taskSel: null };

function route() {
  const hash = location.hash.replace(/^#\/?/, '');
  const parts = hash.split('/').filter(Boolean);
  if (parts[0] === 'config') { S.sid = null; S.tab = 'config'; }
  else if (parts[0] === 's' && parts[1]) { if (S.sid !== parts[1]) S.taskSel = null; S.sid = parts[1]; S.tab = parts[2] || 'chat'; }
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
  let lastDay = '';
  for (const s of S.sessions) {
    const day = new Date(s.started).toLocaleDateString([], {month: 'short', day: 'numeric'});
    if (day !== lastDay) { ul.append(h('li', {class: 'day'}, day)); lastDay = day; }
    const meta = [fmtTime(s.started).slice(0, 5), s.status];
    if (s.duration_s > 30) meta.push(dur(s.duration_s * 1000));
    if (s.tokens) meta.push(k(s.tokens) + ' tok');
    if (s.priced && s.cost_usd > 0) meta.push(money(s.cost_usd));
    ul.append(h('li', {class: s.id === S.sid ? 'active' : '', onclick: () => location.hash = `#/s/${s.id}/chat`},
      h('span', {class: `st ${s.status}` + (s.alive ? ' pulse' : ''), title: s.status}),
      h('div', null,
        h('div', {class: 'title'}, s.first_message ? clip(s.first_message, 70) : '(no prompt)'),
        h('div', {class: 'meta'}, meta.join(' · '), s.config ? h('span', {class: 'cfg'}, ' ' + s.config) : null))));
  }
}

// ---- session view ---------------------------------------------------------
async function openSession(id) {
  closeStream();
  S.events = []; S.detail = null; S.lastSeq = 0;
  try {
    S.detail = await api(`/api/sessions/${id}`);
    S.events = await api(`/api/sessions/${id}/events`);
    S.lastSeq = S.events.length ? S.events[S.events.length-1].seq : 0;
  } catch (e) { $('#main').replaceChildren(h('div', {class: 'empty'}, h('h2', null, 'Could not load session'), e.message)); return; }
  renderSessionShell();
  renderTab();
  renderLive();
  openStream(id, S.lastSeq);
}
function closeStream() { if (S.es) { S.es.close(); S.es = null; } }
function openStream(id, after) {
  const es = new EventSource(`/api/sessions/${id}/stream?after=${after}`);
  S.es = es;
  es.addEventListener('append', e => {
    const ev = JSON.parse(e.data);
    if (S.sid !== id || ev.seq <= S.lastSeq) return; // reconnects replay; ignore what we have
    S.lastSeq = ev.seq;
    S.events.push(ev);
    onEvent(ev);
  });
  es.addEventListener('status', e => {
    if (S.sid !== id) return;
    const before = S.detail;
    S.detail = JSON.parse(e.data);
    renderLive();
    if (S.tab === 'tasks') renderTab();
    updateComposer();
    if (S.detail.question && !(before && before.question)) notify('eagent has a question for you');
    const s = S.sessions.find(x => x.id === id);
    if (s && (s.status !== S.detail.status || s.alive !== S.detail.alive)) { s.status = S.detail.status; s.alive = S.detail.alive; renderSessions(); }
  });
  es.onerror = () => { /* the browser reconnects */ };
}
function onEvent(ev) {
  if (S.tab === 'chat') { const node = chatNode(ev); if (node) { const sc = $('.chat .scroll'); const col = $('.chat .col'); const atBottom = sc.scrollHeight - sc.scrollTop - sc.clientHeight < 80; const w = $('#working'); if (w) col.insertBefore(node, w); else col.append(node); if (atBottom) sc.scrollTop = sc.scrollHeight; } }
  else if (S.tab === 'timeline') { const tb = $('.tl tbody'); if (tb && passesFilter(ev)) tb.append(tlRow(ev)); }
  if (ev.type === 'narrator.message' && document.hidden) notify('eagent: ' + clip(ev.data.text, 80));
}
function notify(text) {
  if (!('Notification' in window) || Notification.permission !== 'granted') return;
  try { new Notification(text, {tag: 'eagent'}); } catch (e) {}
}

function renderSessionShell() {
  const d = S.detail;
  const tabs = ['chat', 'tasks', 'timeline'].map((t, i) => h('a', {href: `#/s/${S.sid}/${t}`, class: S.tab === t ? 'active' : '', title: `shortcut: ${i+1}`}, t[0].toUpperCase() + t.slice(1), t === 'tasks' && d.tasks.length ? ` (${d.tasks.length})` : ''));
  const stopBtn = h('button', {onclick: async () => { if (confirm('Stop this session? It can be resumed later.')) await api(`/api/sessions/${S.sid}/stop`, {method: 'POST', body: '{}'}); }}, 'Stop');
  const resumeBtn = h('button', {onclick: async () => { try { await api(`/api/sessions/${S.sid}/resume`, {method: 'POST', body: '{}'}); toast('resuming'); } catch (e) { toast(e.message, 'bad'); } }}, 'Resume');
  const cost = d.priced && d.cost_usd > 0 ? h('span', {class: 'badge', title: 'estimated at list prices'}, money(d.cost_usd)) : null;
  $('#main').replaceChildren(
    h('div', {class: 'tabs'}, ...tabs, h('span', {class: 'spacer'}), h('div', {class: 'tools'},
      d.duration_s > 0 ? h('span', {class: 'badge', title: 'duration'}, dur(d.duration_s * 1000)) : null,
      cost,
      h('span', {class: 'badge'}, `${d.subsessions} file${d.subsessions === 1 ? '' : 's'}`),
      h('span', {class: 'badge mono', title: 'session id'}, S.sid),
      d.alive ? stopBtn : resumeBtn)),
    h('div', {class: 'pane', id: 'pane'}));
}

function renderTab() {
  const pane = $('#pane'); if (!pane) return;
  if (S.tab === 'chat') renderChat(pane);
  else if (S.tab === 'tasks') renderTasks(pane);
  else if (S.tab === 'timeline') renderTimeline(pane);
}

// ---- live strip + title -------------------------------------------------------
function renderLive() {
  const el = $('#live'); const d = S.detail;
  const w = $('#working');
  let busy = false;
  if (w && d) { const live = d.live || {}; busy = d.alive && (live.OrchestratorBusy || live.Rollover || d.tasks.some(t => t.status === 'running' || t.status === 'queued')); w.classList.toggle('hidden', !busy); w.querySelector('.wtext').textContent = live.Rollover ? 'summarising the session for a fresh context…' : live.OrchestratorBusy ? 'thinking' + (live.OrchestratorFor ? ' · ' + live.OrchestratorFor : '') + '…' : 'tasks running…'; }
  if (!d) { el.innerHTML = ''; document.title = 'eagent'; return; }
  const parts = [];
  const live = d.live || {};
  if (d.alive) {
    if (live.Rollover) parts.push('writing notes for a fresh context');
    else if (live.OrchestratorBusy) parts.push('working' + (live.OrchestratorFor ? ' · ' + live.OrchestratorFor : ''));
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
  document.title = (d.question ? '? ' : (d.alive && (busy || live.OrchestratorBusy)) ? '● ' : '') + 'eagent';
}

// ---- chat ---------------------------------------------------------------------
function renderChat(pane) {
  pane.classList.add('chat'); pane.style.padding = '0';
  const col = h('div', {class: 'col'});
  for (const ev of S.events) { const n = chatNode(ev); if (n) col.append(n); }
  col.append(h('div', {class: 'working hidden', id: 'working'}, h('span', {class: 'dots'}, h('i'), h('i'), h('i')), h('span', {class: 'wtext'})));
  const scroll = h('div', {class: 'scroll'}, col);
  const composer = h('div', {class: 'composer'}, h('div', {class: 'col'},
    h('div', {class: 'pending', id: 'pending'}),
    h('form', {onsubmit: e => { e.preventDefault(); send(); }},
      h('textarea', {id: 'input', placeholder: 'Message the agent… (Enter to send, Shift+Enter for a new line)', rows: 1,
        onkeydown: e => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send(); } },
        oninput: e => { e.target.style.height = 'auto'; e.target.style.height = Math.min(200, e.target.scrollHeight) + 'px'; }}),
      h('button', {class: 'primary', type: 'submit'}, 'Send')),
    h('div', {class: 'hint'},
      h('span', {id: 'hint'}),
      h('label', {class: 'toggle'}, h('input', {type: 'checkbox', checked: S.showActivity, onchange: e => { S.showActivity = e.target.checked; localStorage.setItem('eagent.activity', S.showActivity ? '1' : '0'); renderTab(); }}), 'show activity'))));
  pane.replaceChildren(scroll, composer);
  scroll.scrollTop = scroll.scrollHeight;
  updateComposer();
  renderLive();
  setTimeout(() => $('#input')?.focus(), 0);
}
function updateComposer() {
  const d = S.detail; const pend = $('#pending'); const hint = $('#hint'); if (!pend || !d) return;
  pend.innerHTML = '';
  if (d.question) pend.append(h('span', null, 'Question pending:'), ...(d.question.Options || []).map(o => h('button', {onclick: () => answer(o)}, o)));
  hint.textContent = d.alive ? (d.question ? 'Pick an option above or type your answer.' : 'Delivered to the running session; the agent reads it at its next step.') : 'The session is not running; sending a message resumes it here.';
}
async function send() {
  const ta = $('#input'); const text = ta.value.trim(); if (!text) return;
  ta.value = ''; ta.style.height = 'auto';
  try {
    if (S.detail && S.detail.question) await api(`/api/sessions/${S.sid}/answer`, {method: 'POST', body: JSON.stringify({text, question_id: S.detail.question.ID})});
    else await api(`/api/sessions/${S.sid}/message`, {method: 'POST', body: JSON.stringify({text})});
  } catch (e) { toast(e.message, 'bad'); }
}
async function answer(text) { try { await api(`/api/sessions/${S.sid}/answer`, {method: 'POST', body: JSON.stringify({text, question_id: S.detail.question.ID})}); } catch (e) { toast(e.message, 'bad'); } }

function chatNode(ev) {
  const d = ev.data || {};
  switch (ev.type) {
    case 'user.message': return h('div', {class: 'msg user'}, h('div', {class: 'head'}, 'you · ' + fmtTime(ev.ts)), h('div', {class: 'bubble', html: md(d.text)}));
    case 'user.answer': return h('div', {class: 'msg user'}, h('div', {class: 'head'}, 'your answer · ' + fmtTime(ev.ts)), h('div', {class: 'bubble', html: md(d.text)}));
    case 'narrator.message': return h('div', {class: 'msg'}, h('div', {class: 'avatar'}, 'e'), h('div', {class: 'mbody'}, h('div', {class: 'head'}, h('b', null, 'eagent'), h('span', null, fmtTime(ev.ts))), h('div', {class: 'text', html: md(d.text)})));
    case 'narrator.question': {
      const answered = S.events.find(e => e.type === 'user.answer' && e.data && e.data.question_id === d.id);
      return h('div', {class: 'msg'}, h('div', {class: 'avatar'}, '?'), h('div', {class: 'mbody'}, h('div', {class: 'head'}, h('b', null, 'eagent'), h('span', null, 'needs a decision · ' + fmtTime(ev.ts))),
        h('div', {class: 'qcard'}, h('div', {class: 'text', html: md(d.text)}),
          answered ? h('div', {class: 'answered'}, 'you answered: ' + answered.data.text) : h('div', {class: 'options'}, ...(d.options || []).map(o => h('button', {onclick: () => answer(o)}, o))))));
    }
    case 'session.start': { const m = d.models || {}; return h('div', {class: 'notice'}, `session started ${fmtDate(ev.ts)} · ${['orchestrator', 'task', 'narrator'].map(a => (m[a] || '?').split('/').pop()).join(' / ')}` + (d.config ? ` · ${d.config}` : '')); }
    case 'session.resume': return h('div', {class: 'notice'}, 'session resumed ' + fmtDate(ev.ts) + (d.closed && d.closed.length ? ' · ' + d.closed.join('; ') : ''));
    case 'session.end': return h('div', {class: 'notice'}, `session ended (${d.reason}) ${fmtDate(ev.ts)}`);
    case 'subsession.start': return d.reason === 'rollover' ? h('div', {class: 'notice'}, `fresh context (subsession ${d.index + 1}); notes carried over`) : null;
    case 'error': return h('div', {class: 'act bad'}, h('span', {class: 't'}, fmtTime(ev.ts)), h('span', {class: 'k'}, 'error'), h('span', null, clip(d.where + ': ' + d.text, 200)));
  }
  if (!S.showActivity) return null;
  switch (ev.type) {
    case 'note': return act(ev, 'note', 'note', d.text);
    case 'task.create': return act(ev, '', d.kind === 'dossier' ? 'notes for a fresh context' : 'delegated ' + d.id, d.title);
    case 'task.end': return act(ev, d.status === 'completed' ? 'ok' : 'bad', `${d.id} ${d.status}`, clip(d.summary, 240));
    case 'yield': return act(ev, d.done ? 'ok' : '', d.done ? 'done' : 'waiting', d.reason);
    case 'schedule.create': return act(ev, '', `schedule ${d.id}`, `${d.kind} ${d.spec}: ${d.note}`);
    case 'schedule.fire': return act(ev, '', `schedule ${d.id} fired`, d.note);
    case 'route': return act(ev, '', 'route', `${d.actor} → ${d.model}`);
    case 'dossier': return act(ev, '', 'fresh context', 'notes delivered; the orchestrator continues');
  }
  return null;
}
function act(ev, cls, kind, text) { return h('div', {class: 'act ' + cls}, h('span', {class: 't'}, fmtTime(ev.ts)), h('span', {class: 'k'}, kind), h('span', null, clip(text, 300))); }

// ---- tasks ----------------------------------------------------------------------
function renderTasks(pane) {
  pane.classList.remove('chat'); pane.style.padding = '';
  const d = S.detail;
  const sel = S.taskSel;
  const usage = h('div', {class: 'stats'}, ...d.usage.filter(u => u.calls).map(u => h('div', {class: 'stat'},
    h('span', {class: 'v'}, u.priced ? money(u.cost_usd) : k(u.input) + ' in'),
    h('span', {class: 'l'}, `${u.actor} · ${u.model ? u.model.split('/').pop() : ''}`),
    h('span', {class: 'l'}, `${u.calls} calls · ${k(u.input)} in · ${k(u.output)} out · cache ${Math.round(u.cache_ratio * 100)}%`),
    u.p50_ms ? h('span', {class: 'l'}, `latency p50 ${dur(u.p50_ms)} · p95 ${dur(u.p95_ms)}`) : null)),
    d.priced && d.cost_usd > 0 ? h('div', {class: 'stat total'}, h('span', {class: 'v'}, money(d.cost_usd)), h('span', {class: 'l'}, 'estimated total at list prices'), h('span', {class: 'l'}, `${k(d.tokens)} input tokens · ${dur(d.duration_s * 1000)}`)) : null);
  const table = h('table', {class: 'tasks'}, h('thead', null, h('tr', null, ...['id', 'title', 'status', 'calls', 'tokens (cache)', 'duration'].map(x => h('th', null, x)))),
    h('tbody', null, ...d.tasks.map(t => h('tr', {class: 'row' + (sel === t.id ? ' open' : ''), onclick: () => { S.taskSel = t.id; renderTab(); setTimeout(() => $('#taskdetail')?.scrollIntoView({behavior: 'smooth', block: 'start'}), 0); }},
      h('td', {class: 'num'}, t.id), h('td', {class: 'wrap'}, t.title || '(untitled)', t.kind === 'dossier' ? h('span', {class: 'badge', style: 'margin-left:6px'}, 'notes') : null),
      h('td', null, h('span', {class: 'badge ' + t.status}, t.status)), h('td', {class: 'num'}, t.turns),
      h('td', {class: 'num'}, `${k(t.usage.input)} · ${k(t.usage.output)} (${Math.round(t.usage.cache_ratio * 100)}%)`),
      h('td', {class: 'num'}, t.ended && t.created ? dur(new Date(t.ended) - new Date(t.created)) : t.created ? dur(Date.now() - new Date(t.created)) + '…' : '')))));
  const parts = [usage, chartsCard(d)];
  parts.push(h('div', {class: 'card'}, h('h3', null, 'Tasks'), d.tasks.length ? table : h('div', {class: 'sub'}, 'Nothing delegated yet.')));
  if (d.procs.length) { const procs = S.allProcs ? d.procs : d.procs.slice(-25); parts.push(h('div', {class: 'card'}, h('h3', null, 'Processes', h('span', {class: 'sub'}, `${d.procs.length}`), d.procs.length > 25 ? h('button', {class: 'ghost small', style: 'margin-left:auto', onclick: () => { S.allProcs = !S.allProcs; renderTab(); }}, S.allProcs ? 'show recent' : 'show all') : null), h('table', null, h('tbody', null, ...procs.map(p => h('tr', null, h('td', {class: 'num'}, p.handle), h('td', null, h('span', {class: 'badge ' + p.status}, p.status + (p.status !== 'running' ? ` ${p.exit_code}` : ''))), h('td', {class: 'nowrap'}, p.task || p.actor), h('td', {class: 'wrap'}, h('code', {class: 'inline'}, clip(p.command, 120))))))))); }
  if (d.schedules.length) parts.push(h('div', {class: 'card'}, h('h3', null, 'Schedules'), ...d.schedules.map(s => h('div', null, h('code', {class: 'inline'}, `${s.id} ${s.kind} ${s.spec}`), ' ', s.note, h('span', {class: 'sub'}, ` · next ${fmtTime(s.next)} · fired ${s.fires}×`)))));
  if (sel) { const t = d.tasks.find(x => x.id === sel); if (t) parts.push(taskDetail(t)); }
  pane.replaceChildren(...parts);
}

// chartsCard: a timeline of who was doing what, and the orchestrator's
// context size per call with fresh-context resets.
function chartsCard(d) {
  const evs = S.events;
  const start = evs.length ? new Date(evs[0].ts).getTime() : Date.now();
  const end = Math.max(start + 1000, d.alive ? Date.now() : new Date(evs[evs.length-1].ts).getTime());
  const span = end - start;
  const W = 900, LW = 92, RW = W - LW - 12;
  const x = t => LW + (Math.min(Math.max(t, start), end) - start) / span * RW;
  const rows = [];
  // orchestrator turns
  const turns = []; let open = null;
  for (const e of evs) { if (e.actor === 'orchestrator' && e.type === 'turn.start') open = new Date(e.ts).getTime(); if (e.actor === 'orchestrator' && e.type === 'turn.end' && open) { turns.push([open, new Date(e.ts).getTime()]); open = null; } }
  if (open) turns.push([open, end]);
  rows.push({label: 'orchestrator', bars: turns.map(([a, b]) => ({a, b, cls: 'orch'}))});
  for (const t of d.tasks) { const a = new Date(t.created).getTime(); const b = t.ended ? new Date(t.ended).getTime() : end; rows.push({label: `${t.id} ${clip(t.title, 14)}`, bars: [{a, b, cls: t.status, title: `${t.id} ${t.title} · ${t.status} · ${dur(b - a)}`}]}); }
  const RH = 18, top = 22, height = top + rows.length * RH + 8;
  const g = svg('svg', {viewBox: `0 0 ${W} ${height}`, class: 'gantt', preserveAspectRatio: 'none'});
  // time axis
  const ticks = 6;
  for (let i = 0; i <= ticks; i++) { const t = start + span * i / ticks; g.append(svg('line', {x1: x(t), x2: x(t), y1: top - 4, y2: height, class: 'grid'}), svg('text', {x: x(t), y: 12, class: 'tick', 'text-anchor': i === 0 ? 'start' : i === ticks ? 'end' : 'middle'}, dur(t - start))); }
  // rollover markers
  for (const e of evs) if (e.type === 'subsession.start' && e.data.reason === 'rollover') { const xx = x(new Date(e.ts).getTime()); g.append(svg('line', {x1: xx, x2: xx, y1: top - 4, y2: height, class: 'rollover'}), svg('title', null, 'fresh context')); }
  rows.forEach((r, i) => {
    const y = top + i * RH;
    g.append(svg('text', {x: LW - 6, y: y + 13, class: 'label', 'text-anchor': 'end'}, r.label));
    for (const b of r.bars) { const rect = svg('rect', {x: x(b.a), y: y + 3, width: Math.max(2, x(b.b) - x(b.a)), height: RH - 6, rx: 3, class: 'bar ' + b.cls}); if (b.title) rect.append(svg('title', null, b.title)); g.append(rect); }
  });
  // context sparkline
  const calls = evs.filter(e => e.type === 'assistant' && e.actor === 'orchestrator' && !e.task && e.data.usage);
  let spark = null;
  if (calls.length > 1) {
    const SH = 70, ST = 16, maxIn = Math.max(...calls.map(c => c.data.usage.input), 1);
    const sy = v => ST + (SH - 6) * (1 - v / maxIn);
    spark = svg('svg', {viewBox: `0 0 ${W} ${SH + 18}`, class: 'spark', preserveAspectRatio: 'none'});
    for (const e of evs) if (e.type === 'subsession.start' && e.data.reason === 'rollover') { const xx = x(new Date(e.ts).getTime()); spark.append(svg('line', {x1: xx, x2: xx, y1: ST - 4, y2: SH + 4, class: 'rollover'})); }
    let path = '', prevSub = null;
    for (const c of calls) { const t = new Date(c.ts).getTime(); const px = x(t), py = sy(c.data.usage.input); const sub = c.file; path += (path && sub === prevSub ? ' L' : ' M') + px.toFixed(1) + ' ' + py.toFixed(1); prevSub = sub; }
    spark.append(svg('path', {d: path.trim(), class: 'line'}));
    for (const c of calls) { const dot = svg('circle', {cx: x(new Date(c.ts).getTime()), cy: sy(c.data.usage.input), r: 2.2, class: 'dot' + (c.data.usage.cached / Math.max(c.data.usage.input, 1) > 0.5 ? ' cached' : '')}); dot.append(svg('title', null, `${k(c.data.usage.input)} in (${Math.round(100 * c.data.usage.cached / Math.max(c.data.usage.input, 1))}% cached) · ${dur(c.data.elapsed_ms || 0)} · ${fmtTime(c.ts)}`)); spark.append(dot); }
    spark.append(svg('text', {x: LW - 6, y: ST + 4, class: 'label', 'text-anchor': 'end'}, k(maxIn)), svg('text', {x: LW - 6, y: SH, class: 'label', 'text-anchor': 'end'}, '0'), svg('text', {x: LW - 6, y: ST + (SH - 6) / 2 + 4, class: 'label dim', 'text-anchor': 'end'}, 'context'));
  }
  return h('div', {class: 'card'}, h('h3', null, 'Timeline ', h('span', {class: 'sub'}, `${dur(span)} · dotted lines mark a fresh context`)), g,
    spark ? h('div', null, h('div', {class: 'sub', style: 'margin:8px 0 2px'}, 'Orchestrator prompt size per call (green dots were mostly served from cache)'), spark) : null);
}

function taskDetail(t) {
  const evs = S.events.filter(e => e.task === t.id);
  const created = S.events.find(e => e.type === 'task.create' && e.data && e.data.id === t.id);
  return h('div', {class: 'card', id: 'taskdetail'},
    h('h3', null, h('span', {class: 'grow'}, `${t.id} · ${t.title}`), h('span', {class: 'badge ' + t.status}, t.status), h('button', {class: 'ghost small', onclick: () => { S.taskSel = null; renderTab(); }}, 'close')),
    created && h('details', null, h('summary', null, 'task description'), h('pre', {class: 'code'}, created.data.description)),
    t.summary && h('div', null, h('div', {class: 'sub'}, 'report'), h('div', {html: md(t.summary)})),
    h('div', {class: 'sub', style: 'margin-top:10px'}, `${t.turns} model calls · ${k(t.usage.input)} in · ${k(t.usage.output)} out`),
    ...turns(evs));
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
        ...(d.tool_calls || []).map(tc => renderCall(tc, results.get(tc.id)))));
    } else if (e.type === 'harness.message') out.push(h('div', {class: 'turn'}, h('div', {class: 'hd'}, h('span', {class: 'actor'}, 'harness'), fmtTime(e.ts)), h('div', {class: 'text'}, d.text)));
  }
  return out;
}
// renderCall shows a tool call the way a person would read it.
function renderCall(tc, r) {
  const a = (tc.args && typeof tc.args === 'object') ? tc.args : {};
  const res = r ? h('div', {class: 'res' + (r.is_error ? ' err' : '')}, r.output && r.output.length > 600 ? h('details', null, h('summary', null, (r.is_error ? 'error' : 'result') + ` (${r.output.length} chars)`), h('pre', {class: 'code'}, r.output)) : h('pre', {class: 'code'}, r.output || '(empty)')) : h('div', {class: 'res sub'}, 'no result recorded');
  const head = (label, extra) => h('div', {class: 'callhd'}, h('span', {class: 'name'}, tc.name), extra ? h('span', {class: 'sub'}, ' ', extra) : null, label ? h('span', {class: 'sub'}, ' · ', label) : null);
  switch (tc.name) {
    case 'bash': return h('div', {class: 'call'}, head('', a.timeout_seconds === 0 ? 'service' : ''), h('pre', {class: 'code'}, '$ ' + (a.command || '')), res);
    case 'bash_poll': case 'bash_kill': case 'bash_extend': case 'bash_write': return h('div', {class: 'call'}, head('', a.handle + (a.input ? ' ← ' + JSON.stringify(a.input) : '')), res);
    case 'write_file': { const c = String(a.content || ''); const lines = c.split('\n').length; return h('div', {class: 'call'}, head(`${lines} lines`, a.path), h('details', {open: lines <= 40}, h('summary', null, 'content'), h('pre', {class: 'code'}, c)), res); }
    case 'edit_file': return h('div', {class: 'call'}, head('', a.path), h('div', {class: 'diff'}, h('pre', {class: 'code old'}, a.old_text || ''), h('pre', {class: 'code new'}, a.new_text || '')), res);
    case 'read_file': return h('div', {class: 'call'}, head(a.offset ? `from line ${a.offset}${a.limit ? ', ' + a.limit + ' lines' : ''}` : '', a.path), res);
    case 'list_dir': case 'session_list': case 'session_read': case 'session_search': return h('div', {class: 'call'}, head('', a.path || a.file || a.query || ''), res);
    case 'delegate': return h('div', {class: 'call'}, head('', a.title), h('details', null, h('summary', null, 'task description'), h('pre', {class: 'code'}, a.description || '')), res);
    case 'note': case 'yield': case 'complete_task': return h('div', {class: 'call'}, head(tc.name === 'yield' ? (a.done ? 'done' : 'waiting') : (a.status || ''), ''), h('div', {class: 'text'}, a.text || a.reason || a.summary || ''), res);
    case 'wait': return h('div', {class: 'call'}, head('', [].concat(a.tasks || [], a.processes || []).join(', ') || 'any task'), res);
    case 'schedule': case 'cancel_schedule': case 'cancel_task': return h('div', {class: 'call'}, head('', a.spec || a.schedule || a.task || ''), a.note ? h('div', {class: 'text'}, a.note) : null, res);
    default: return h('div', {class: 'call'}, head('', ''), h('details', null, h('summary', null, 'arguments'), h('pre', {class: 'code'}, JSON.stringify(tc.args, null, 2))), res);
  }
}

// ---- timeline -----------------------------------------------------------------------
const ACTORS = ['user', 'orchestrator', 'task', 'narrator', 'harness'];
function passesFilter(ev) {
  if (S.filters.size && !S.filters.has(ev.actor)) return false;
  if (S.search) { const q = S.search.toLowerCase(); if (!(ev.type + ' ' + summary(ev) + ' ' + (ev.task || '')).toLowerCase().includes(q)) return false; }
  return true;
}
function renderTimeline(pane) {
  pane.classList.remove('chat'); pane.style.padding = '';
  const chips = ACTORS.map(a => h('span', {class: 'chip' + (S.filters.has(a) ? ' on' : ''), onclick: () => { S.filters.has(a) ? S.filters.delete(a) : S.filters.add(a); renderTab(); }}, a));
  const search = h('input', {class: 'search', placeholder: 'search events…', value: S.search, oninput: e => { S.search = e.target.value; const tb = $('.tl tbody'); tb.replaceChildren(...S.events.filter(passesFilter).map(tlRow)); $('#tlcount').textContent = `${tb.children.length} of ${S.events.length} events`; }});
  const tbody = h('tbody', null, ...S.events.filter(passesFilter).map(tlRow));
  pane.replaceChildren(h('div', {class: 'filters'}, h('span', {class: 'sub'}, 'actor:'), ...chips, search, h('span', {class: 'sub', id: 'tlcount', style: 'margin-left:auto'}, `${tbody.children.length} of ${S.events.length} events`)),
    h('table', {class: 'tl'}, h('thead', null, h('tr', null, h('th', null, '#'), h('th', null, 'time'), h('th', null, 'actor'), h('th', null, 'type'), h('th', null, 'what happened'))), tbody));
}
function tlRow(ev) {
  const start = S.events.length ? new Date(S.events[0].ts) : new Date(ev.ts);
  const tr = h('tr', {class: 'row', onclick: () => {
      const open = tr.classList.toggle('open');
      if (open) tr.after(h('tr', {class: 'detail'}, h('td', {colspan: 5}, h('div', {class: 'where'}, `${ev.file}:${ev.line} · ${fmtDate(ev.ts)}`), h('pre', {class: 'code'}, JSON.stringify(ev.data, null, 2)))));
      else if (tr.nextSibling && tr.nextSibling.classList.contains('detail')) tr.nextSibling.remove();
    }},
    h('td', {class: 'num'}, ev.seq), h('td', {class: 'num', title: fmtTime(ev.ts)}, rel(ev.ts, start)),
    h('td', {class: 'actor actor-' + ev.actor}, ev.actor, ev.task ? h('span', {class: 'tid'}, ev.task) : null),
    h('td', {class: 'type'}, ev.type), h('td', {class: 'sum', title: summary(ev)}, summary(ev)));
  return tr;
}
function argPreview(args) {
  if (!args || typeof args !== 'object') return String(args ?? '');
  for (const key of ['command', 'path', 'title', 'text', 'reason', 'handle', 'query', 'spec']) if (typeof args[key] === 'string') return args[key];
  return JSON.stringify(args);
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
      h('span', {class: 'k'}, 'task concurrency'), h('span', null, eff.task_concurrency), h('span', {class: 'k'}, 'fresh context at'), h('span', null, `${k(eff.rollover_tokens)} tokens`), h('span', {class: 'k'}, 'max task calls'), h('span', null, eff.max_task_turns)),
    h('div', {style: 'margin-top:10px'}, ...Object.entries(c.keys).sort().map(([env, on]) => h('span', {class: 'key'}, h('span', {class: 'd' + (on ? ' on' : '')}), env))));
  const bundleRows = (list, kind) => h('table', null, h('tbody', null, ...list.map(b => h('tr', null, h('td', null, h('code', {class: 'inline'}, b.name), b.active ? h('span', {class: 'badge completed', style: 'margin-left:6px'}, 'active') : null), h('td', {class: 'wrap'}, b.models), h('td', {class: 'sub wrap'}, b.invalid ? 'invalid: ' + b.invalid : b.description),
    h('td', null, h('button', {onclick: () => newSessionDialog(kind === 'preset' ? {preset: b.name} : {config: b.name})}, 'New session'))))));
  const presets = h('div', {class: 'card'}, h('h3', null, 'Built-in presets'), bundleRows(c.presets, 'preset'), h('div', {class: 'sub', style: 'margin-top:8px'}, 'Use one with ', h('code', {class: 'inline'}, 'eagent --preset NAME …'), ' or ', h('code', {class: 'inline'}, 'eagent doctor --live --preset NAME'), ' to prove it works first.'));
  const bundles = h('div', {class: 'card'}, h('h3', null, 'Project bundles'), c.bundles.length ? bundleRows(c.bundles, 'bundle') : h('div', {class: 'sub'}, 'None yet.'),
    h('div', {class: 'sub', style: 'margin-top:8px'}, 'Bundles live in ', h('code', {class: 'inline'}, c.files.bundles), ' and are meant to be committed, so teammates can pick each other\'s setups with ', h('code', {class: 'inline'}, '--config NAME'), ' or ', h('code', {class: 'inline'}, 'EAGENT_CONFIG=NAME'), '.'));
  const nameIn = h('input', {class: 'search', placeholder: 'name (e.g. eric-fast)'});
  const descIn = h('input', {class: 'search', placeholder: 'description', style: 'flex:1;min-width:200px'});
  const fromSel = h('select', null, h('option', {value: ''}, 'copy: current configuration'), ...c.presets.map(p => h('option', {value: p.name}, `copy preset · ${p.name}`)), ...c.bundles.map(b => h('option', {value: b.name}, `copy bundle · ${b.name}`)));
  const jsonTa = h('textarea', {class: 'code', style: 'width:100%;min-height:160px;margin-top:8px', placeholder: 'Optional: full configuration JSON to save instead of a copy.'});
  const saveBtn = h('button', {class: 'primary', onclick: async () => {
    try {
      const body = {name: nameIn.value.trim(), description: descIn.value.trim(), from: fromSel.value};
      if (jsonTa.value.trim()) body.config = JSON.parse(jsonTa.value);
      const r = await api('/api/config/bundles', {method: 'POST', body: JSON.stringify(body)});
      toast(`saved ${r.name}`); renderConfig();
    } catch (e) { toast(e.message, 'bad'); }
  }}, 'Save bundle');
  const saveCard = h('div', {class: 'card'}, h('h3', null, 'Save a bundle'),
    h('div', {style: 'display:flex;gap:8px;flex-wrap:wrap;align-items:center'}, nameIn, descIn, fromSel, saveBtn),
    h('details', null, h('summary', null, 'advanced: paste a full configuration'), jsonTa, h('div', {class: 'sub'}, 'Start from the effective configuration below; keys are never stored, only the environment variable names.')));
  const promptsCard = h('div', {class: 'card'}, h('h3', null, 'Prompts'), h('table', null, h('tbody', null, ...c.prompts.map(p => h('tr', {class: 'row', onclick: () => promptEditor(p.name)},
    h('td', null, h('code', {class: 'inline'}, p.name)), h('td', {class: 'sub'}, p.source === 'built-in' ? 'built-in' : 'project override'), h('td', {class: 'sub'}, {'ORCHESTRATOR.md': 'how the orchestrator plans, delegates, and finishes', 'TASK-WORKER.md': 'how a worker does one task and reports', 'NARRATOR.md': 'when the narrator speaks and what it never writes', 'PERSONA.md': 'the narrator\'s voice', 'COMPACTION-DOSSIER.md': 'the notes written before a fresh context'}[p.name] || ''))))),
    h('div', {class: 'sub', style: 'margin-top:8px'}, 'Click a prompt to read or edit it. Edits are saved as project overrides in ', h('code', {class: 'inline'}, c.files.prompts), ' and apply to new sessions.'));
  const raw = h('div', {class: 'card'}, h('h3', null, 'Effective configuration'), h('pre', {class: 'code'}, JSON.stringify(eff, null, 2)), h('div', {class: 'sub'}, 'Project file: ', h('code', {class: 'inline'}, c.files.config), ' · ', h('code', {class: 'inline'}, 'eagent config'), ' prints this.'));
  $('#main').replaceChildren(h('div', {class: 'pane'}, models, presets, bundles, saveCard, promptsCard, raw));
}
async function promptEditor(name) {
  let r; try { r = await api(`/api/prompts/${name}`); } catch (e) { toast(e.message, 'bad'); return; }
  const ta = h('textarea', {class: 'code', style: 'width:100%;min-height:50vh'}, r.text);
  const status = h('span', {class: 'sub'});
  const save = h('button', {class: 'primary', onclick: async () => { try { const x = await api(`/api/prompts/${name}`, {method: 'PUT', body: JSON.stringify({text: ta.value})}); status.textContent = 'saved as project override'; toast(`${name} saved`); r.source = x.source; } catch (e) { status.textContent = e.message; } }}, 'Save as override');
  const reset = h('button', {onclick: async () => { if (!confirm('Remove the project override and go back to the built-in prompt?')) return; try { await api(`/api/prompts/${name}`, {method: 'DELETE'}); const x = await api(`/api/prompts/${name}`); ta.value = x.text; status.textContent = 'built-in restored'; renderConfig(); } catch (e) { status.textContent = e.message; } }}, 'Remove override');
  showModal(h('div', null, h('h2', null, name, ' ', h('span', {class: 'badge'}, r.source === 'built-in' ? 'built-in' : 'project override')), ta,
    h('div', {class: 'sub', style: 'margin-top:6px'}, 'Go template; variables depend on the prompt ({{.Project}}, {{.Instructions}}, {{.Interactive}}, {{.Persona}}, …). Changes apply to sessions started after saving.'),
    h('div', {class: 'foot'}, status, h('span', {style: 'flex:1'}), reset, h('button', {onclick: closeModal}, 'Close'), save)));
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
    catch (e) { toast(e.message, 'bad'); start.disabled = false; }
  }}, 'Start');
  ta.addEventListener('keydown', e => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) start.click(); });
  showModal(h('div', null, h('h2', null, 'New session'), ta, h('div', {class: 'row'}, h('span', {class: 'sub'}, 'configuration'), sel, h('span', {class: 'sub', style: 'margin-left:auto'}, '⌘/Ctrl+Enter to start')), h('div', {class: 'foot'}, h('button', {onclick: closeModal}, 'Cancel'), start)));
  ta.focus();
}
function showModal(content) { const m = $('#modal'); m.replaceChildren(h('div', {class: 'box'}, content)); m.classList.remove('hidden'); m.onclick = e => { if (e.target === m) closeModal(); }; }
function closeModal() { $('#modal').classList.add('hidden'); }

// ---- keyboard --------------------------------------------------------------------------------
document.addEventListener('keydown', e => {
  const typing = ['INPUT', 'TEXTAREA', 'SELECT'].includes(document.activeElement?.tagName);
  if (e.key === 'Escape') { closeModal(); return; }
  if (typing || e.metaKey || e.ctrlKey || e.altKey) return;
  if (e.key === 'n') { e.preventDefault(); newSessionDialog(); }
  else if (e.key === '/' && S.sid) { e.preventDefault(); location.hash = `#/s/${S.sid}/chat`; setTimeout(() => $('#input')?.focus(), 50); }
  else if (['1', '2', '3'].includes(e.key) && S.sid) { location.hash = `#/s/${S.sid}/${['chat', 'tasks', 'timeline'][+e.key - 1]}`; }
  else if (e.key === 'g') { location.hash = '#/config'; }
});

// ---- render root -------------------------------------------------------------------------
function renderHome() {
  closeStream(); S.detail = null; renderLive();
  $('#main').replaceChildren(h('div', {class: 'empty'}, h('h2', null, 'eagent'),
    h('p', null, 'Pick a session, or start a new one.'),
    h('p', null, h('button', {class: 'primary', onclick: () => newSessionDialog()}, 'New session')),
    h('div', {class: 'keys'}, h('span', null, h('kbd', null, 'n'), 'new session'), h('span', null, h('kbd', null, '1'), h('kbd', null, '2'), h('kbd', null, '3'), 'tabs'), h('span', null, h('kbd', null, '/'), 'message'), h('span', null, h('kbd', null, 'g'), 'config'))));
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
api('/api/health').then(hh => { $('#project').textContent = hh.project; $('#project').title = hh.project; }).catch(() => {});
if ('Notification' in window && Notification.permission === 'default') { document.addEventListener('click', () => Notification.requestPermission(), {once: true}); }
loadSessions().then(route);
setInterval(loadSessions, 15000);
})();
