/* eagent web UI — vanilla JS, no build step. */
(() => {
'use strict';
const ARCHIVE = window.EagentArchive || null;
const now = () => ARCHIVE ? ARCHIVE.now() : Date.now();

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
const dur = ms => (ms = Math.round(ms)) >= 3600000 ? `${Math.floor(ms/3600000)}h${Math.round((ms%3600000)/60000)}m` : ms >= 60000 ? `${Math.floor(ms/60000)}m${Math.round((ms%60000)/1000)}s` : ms >= 1000 ? (ms/1000).toFixed(1)+'s' : `${ms}ms`;
const money = usd => usd >= 1 ? '$' + usd.toFixed(2) : usd >= 0.01 ? '$' + usd.toFixed(2) : usd > 0 ? '<$0.01' : '$0';
const when = ts => { const d = ts ? new Date(ts) : null; return d && !isNaN(d) && d.getFullYear() > 1 ? d : null; };
const clip = (s, n) => { s = String(s ?? '').replace(/\s+/g, ' ').trim(); return s.length > n ? s.slice(0, n-1) + '…' : s; };
const TOKEN = document.querySelector('meta[name="eagent-token"]')?.content || '';
// Client capability handshake (v1): describe what context this browser can
// supply. Gathered once, sent as `client` with every message and answer;
// no new endpoints. Everything is guarded so a missing API never breaks send.
let clientCapsCache = null;
function clientCaps() {
  if (clientCapsCache) return clientCapsCache;
  const caps = {source: 'web', supplies: ['tz', 'locale', 'screen']};
  try {
    const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
    if (tz) caps.timezone = tz;
    if (navigator.language) caps.locale = navigator.language;
    if (window.screen && screen.width > 0 && screen.height > 0) caps.screen = screen.width + 'x' + screen.height;
    caps.device = (navigator.userAgentData && navigator.userAgentData.mobile) ? 'phone' : 'desktop';
  } catch (e) { /* keep the base capsule */ }
  clientCapsCache = caps;
  return caps;
}
async function api(path, opts) {
  if (ARCHIVE) throw new Error('This saved session is read only.');
  const headers = {'Content-Type': 'application/json'};
  if (opts && opts.method && opts.method !== 'GET') headers['X-Eagent-Token'] = TOKEN;
  const r = await fetch(path, Object.assign({headers}, opts));
  const body = await r.json().catch(() => ({}));
  if (!r.ok) { const err = new Error(body.error || r.statusText); err.status = r.status; err.body = body; throw err; }
  return body;
}
const toastHost = () => $('#toasts') || document.body;
function toast(text, kind) {
  const t = h('div', {class: 'toast ' + (kind || '')}, text);
  toastHost().append(t);
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
function stored(key) { try { return localStorage.getItem(key); } catch (e) { return null; } }
const S = { gen: 0, open: new Set(), closed: new Set(), sessions: [], sid: null, tab: 'chat', detail: null, events: [], lastSeq: 0, es: null, showActivity: !ARCHIVE && stored('eagent.activity') === '1', showLegacyActivity: ARCHIVE || stored('eagent.activity.legacy') !== '0', filters: new Set(), search: '' };

let lastHash = location.hash;
function route() {
  if (ARCHIVE) return;
  if (window.EagentConfig && lastHash.startsWith('#/config') && !location.hash.startsWith('#/config') && window.EagentConfig.dirty()) {
    if (!confirm('You have unsaved configuration changes. Leave and lose them?')) { history.replaceState(null, '', lastHash); return; }
    window.EagentConfig.reset();
  }
  lastHash = location.hash;
  const hash = location.hash.replace(/^#\/?/, '');
  const parts = hash.split('/').filter(Boolean);
  if (parts[0] === 'config') { S.sid = null; S.tab = 'config'; }
  else if (parts[0] === 's' && parts[1]) { if (S.sid !== parts[1]) { S.open.clear(); S.closed.clear(); } S.sid = parts[1]; S.tab = TABS.includes(parts[2]) ? parts[2] : 'chat'; }
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
        h('div', {class: 'title', title: s.description || ''}, s.title ? clip(s.title, 70) : (s.first_message ? clip(s.first_message, 70) : '(no prompt)')),
        h('div', {class: 'meta'}, meta.join(' · '), s.config ? h('span', {class: 'cfg'}, ' ' + s.config) : null))));
  }
}

// ---- session view ---------------------------------------------------------
async function openSession(id) {
  closeStream();
  S.events = []; S.detail = null; S.lastSeq = 0;
  const gen = ++S.gen; // a later click wins: an older load must not publish its state
  let detail, events;
  try {
    detail = await api(`/api/sessions/${id}`);
    events = await api(`/api/sessions/${id}/events`);
  } catch (e) { if (S.gen !== gen) return; $('#main').replaceChildren(h('div', {class: 'empty'}, h('h2', null, 'Could not load session'), e.message)); return; }
  if (S.gen !== gen || S.sid !== id) return;
  S.detail = detail; S.events = events;
  S.lastSeq = events.length ? events[events.length-1].seq : 0;
  renderSessionShell();
  renderTab();
  renderLive();
  openStream(id, S.lastSeq);
}
function closeStream() { if (S.es) { S.es.close(); S.es = null; } }
function openStream(id, after) {
  closeStream(); // never two streams: the old one would keep a server goroutine busy for the life of the tab
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
    renderTools();
    // Rebuild the Usage tab only when something it shows changed; open
    // panels survive either way.
    if (S.tab === 'usage' && usageDigest(S.detail) !== usageDigest(before)) renderTab();
    renderAgents();
    updateComposer();
    if (S.detail.question && !(before && before.question)) notify('eagent has a question for you');
    const s = S.sessions.find(x => x.id === id);
    if (s && (s.status !== S.detail.status || s.alive !== S.detail.alive || s.title !== S.detail.title || s.description !== S.detail.description)) { s.status = S.detail.status; s.alive = S.detail.alive; s.title = S.detail.title; s.description = S.detail.description; renderSessions(); }
  });
  es.onerror = () => { /* the browser reconnects */ };
}
function onEvent(ev) {
  if (S.tab === 'chat' && (ev.type === 'user.answer' || ev.type === 'user.dismiss')) closeQuestionCard(ev.data && ev.data.question_id);
  if (S.tab === 'chat') { const node = chatNode(ev); if (node) { const sc = $('.chat .scroll'); const col = $('.chat .col'); const atBottom = sc.scrollHeight - sc.scrollTop - sc.clientHeight < 80; const w = $('#working'); if (w) col.insertBefore(node, w); else col.append(node); if (atBottom) sc.scrollTop = sc.scrollHeight; } }
  else if (S.tab === 'timeline') { const tb = $('.tl tbody'); if (tb) { if (passesFilter(ev)) tb.append(tlRow(ev)); tlUpdateCount(); } }
  if (ev.type === 'narrator.message' && document.hidden) notify('eagent: ' + clip(ev.data.text, 80));
}
// closeQuestionCard redraws a question's card once it is answered or
// dismissed, so its options can no longer be picked.
function closeQuestionCard(qid) {
  if (!qid) return;
  const q = S.events.find(e => e.type === 'narrator.question' && e.data && e.data.id === qid);
  const old = q && [...document.querySelectorAll('#pane [data-qid]')].find(n => n.dataset.qid === qid);
  const node = old && chatNode(q);
  if (node) old.replaceWith(node);
}
function notify(text) {
  if (!('Notification' in window) || Notification.permission !== 'granted') return;
  try { new Notification(text, {tag: 'eagent'}); } catch (e) {}
}

// eagentModel is the model eagent ran on: the one loop's, or in an old
// session the orchestrator's.
const eagentModel = d => (d.models || {}).eagent || (d.models || {}).orchestrator || '';
const shortModel = m => String(m || '').split('/').pop();
const TABS = ['chat', 'usage', 'timeline'];
function renderSessionShell() {
  const d = S.detail;
  const tabs = TABS.map((t, i) => h('a', {href: `#/s/${S.sid}/${t}`, class: S.tab === t ? 'active' : '', title: `shortcut: ${i+1}`}, t[0].toUpperCase() + t.slice(1)));
  const titleText = d.title ? d.title : (d.first_message ? clip(d.first_message, 120) : 'New session');
  $('#main').replaceChildren(
    h('div', {class: 'session-head'},
      h('div', {class: 'session-title', id: 'session-title'}, titleText),
      h('div', {class: 'session-desc', id: 'session-desc', style: d.description ? '' : 'display:none'}, d.description || ''),
      h('div', {class: 'agents', id: 'agents'})),
    h('div', {class: 'tabs'}, ...tabs, h('span', {class: 'spacer'}), sessionTools(d)),
    h('div', {class: 'pane', id: 'pane'}));
  renderAgents();
}
// sessionTools is the toolbar beside the tabs: what the session runs on,
// what it has cost, and Stop or Resume. The button is made once and only
// relabelled (renderTools), so a click never lands on a button that is
// being swapped out; what it does is decided when it is clicked.
function sessionTools(d) {
  const act = ARCHIVE ? null : h('button', {id: 'session-act', onclick: async () => {
    if (S.detail && S.detail.alive) { if (confirm('Stop this session and its agent? It can be resumed later.')) await api(`/api/sessions/${S.sid}/stop`, {method: 'POST', body: '{}'}); return; }
    try { await api(`/api/sessions/${S.sid}/resume`, {method: 'POST', body: '{}'}); toast('resuming'); } catch (e) { toast(e.message, 'bad'); }
  }}, d.alive ? 'Stop' : 'Resume');
  return h('div', {class: 'tools', id: 'session-tools'}, h('span', {id: 'session-badges', style: 'display:contents'}, ...toolBadges(d)), act);
}
// toolBadges are the toolbar's badges for a session's detail.
function toolBadges(d) {
  const cost = d.priced && d.cost_usd > 0 ? h('span', {class: 'badge', title: 'estimated at list prices'}, money(d.cost_usd)) : null;
  const model = eagentModel(d);
  return [
    d.legacy ? h('span', {class: 'badge', title: 'Written before eagent was one loop: its tasks, processes and schedules are shown as history only.'}, 'older session') : null,
    model ? h('span', {class: 'badge model', title: `eagent runs on ${model}`}, shortModel(model)) : null,
    d.duration_s > 0 ? h('span', {class: 'badge', title: 'duration'}, dur(d.duration_s * 1000)) : null,
    cost,
    d.phone ? h('span', {class: 'badge phone', title: `Finalechat thread ${d.phone.external_id}` + (d.phone.remote_mode ? ' · remote mode' : '')}, 'phone') : null,
    h('span', {class: 'badge'}, `${d.subsessions} file${d.subsessions === 1 ? '' : 's'}`),
    h('span', {class: 'badge mono', title: 'session id'}, S.sid),
  ].filter(Boolean);
}
// renderTools brings the toolbar up to date with the latest status in
// place: the badges only when what they show changed, the button only by
// its label.
function renderTools() {
  const badges = $('#session-badges'); if (!badges || !S.detail) return;
  const next = toolBadges(S.detail);
  const key = nodes => nodes.map(n => n.className + '\u0000' + (n.title || '') + '\u0000' + n.textContent).join('\u0001');
  if (key(next) !== key([...badges.children])) badges.replaceChildren(...next);
  const act = $('#session-act');
  const label = S.detail.alive ? 'Stop' : 'Resume';
  if (act && act.textContent !== label) act.textContent = label;
}

// renderAgents shows the session's agents under its title: who, what it
// runs, what it is doing, and a look at its terminal while it runs.
function renderAgents() {
  const el = $('#agents'); const d = S.detail; if (!el || !d) return;
  const agents = d.agents || [];
  el.replaceChildren(...agents.map(a => {
    const running = d.alive && !ARCHIVE && a.state !== 'exited' && a.state !== 'stopped';
    const what = [a.model ? shortModel(a.model) : '', a.effort].filter(Boolean).join(' ');
    return h('span', {class: 'agent ' + a.state, title: `${a.who}${what ? ' · ' + what : ''} · ${a.state}${a.detail ? ': ' + a.detail : ''} · ${a.dir}` + (a.autopilot ? ' · autopilot on' : '')},
      h('span', {class: 'st ' + a.state}), h('b', null, a.who), what ? h('span', {class: 'sub'}, ' ' + what) : null, h('span', {class: 'sub'}, ' · ' + a.state + (a.autopilot ? ' · autopilot' : '')),
      running ? h('button', {class: 'ghost small', title: 'What its terminal shows right now', onclick: () => showScreen(a.name)}, 'Screen') : null);
  }));
}
async function showScreen(name) {
  const pre = h('pre', {class: 'code screen'}, 'reading…');
  const title = h('h2', null, 'Screen');
  const load = async () => {
    try { const r = await api(`/api/sessions/${S.sid}/agents/${encodeURIComponent(name)}/screen`); title.textContent = `${r.who} · ${r.state}`; pre.textContent = r.screen || '(blank)'; }
    catch (e) { pre.textContent = e.message; }
  };
  showModal(h('div', null, title, pre, h('div', {class: 'foot'}, h('span', {class: 'sub'}, 'Read only; nothing is typed into it.'), h('span', {style: 'flex:1'}), h('button', {onclick: load}, 'Refresh'), h('button', {onclick: closeModal}, 'Close'))));
  load();
}

function renderTab() {
  const pane = $('#pane'); if (!pane) return;
  if (S.tab === 'chat') renderChat(pane);
  else if (S.tab === 'usage') renderUsage(pane);
  else if (S.tab === 'timeline') renderTimeline(pane);
  if (ARCHIVE) ARCHIVE.changed?.();
}

// ---- live strip + title -------------------------------------------------------
function renderLive() {
  if (ARCHIVE) return;
  const el = $('#live'); const d = S.detail;
  const w = $('#working');
  const live = (d && d.live) || {};
  const busy = !!(d && d.alive && (live.Busy || live.Compacting));
  if (w) { w.classList.toggle('hidden', !busy); w.querySelector('.wtext').textContent = live.Compacting ? 'writing a briefing for a fresh conversation…' : 'eagent is thinking' + (live.BusyFor ? ' · ' + live.BusyFor : '') + '…'; }
  if (!d) { el.innerHTML = ''; document.title = 'eagent'; return; }
  const parts = [];
  if (d.alive) {
    if (live.Compacting) parts.push('eagent writing a briefing for a fresh conversation');
    else if (live.Busy) parts.push('eagent thinking' + (live.BusyFor ? ' · ' + live.BusyFor : ''));
    else if (d.question) parts.push('waiting for your answer');
    else if (d.idle) parts.push(d.done ? 'done, waiting for you' : 'waiting for you');
    for (const a of live.Agents || []) parts.push(`${a.Who || a.Name} ${a.State}`);
    if (!parts.length) parts.push('running');
  } else parts.push(`session ${d.status}`);
  if (d.context_tokens) parts.push(`ctx ${k(d.context_tokens)}`);
  const main = d.usage.find(u => u.actor === 'eagent') || d.usage.find(u => u.actor === 'orchestrator');
  if (main && main.input) parts.push(`cache ${Math.round(main.cache_ratio * 100)}%`);
  el.replaceChildren(h('span', {class: 'dot ' + (d.alive ? 'on' : '')}), h('span', null, parts.join(' · ')));
  // The event-sourced title (thread.title) names the whole session so far;
  // fall back to the opening prompt, as the list does.
  const name = d.title ? d.title : (d.first_message ? clip(d.first_message, 60) : '');
  const st = $('#session-title');
  if (st) st.textContent = name || 'New session';
  const sd = $('#session-desc');
  if (sd) { sd.textContent = d.description || ''; sd.style.display = d.description ? '' : 'none'; }
  if (d.description) el.title = d.description; else el.removeAttribute('title');
  document.title = (d.question ? '? ' : busy ? '● ' : '') + (name ? name + ' · eagent' : 'eagent');
}

// ---- chat ---------------------------------------------------------------------
function renderChat(pane) {
  pane.classList.add('chat'); pane.style.padding = '0';
  const col = h('div', {class: 'col'});
  for (const ev of S.events) { const n = chatNode(ev); if (n) col.append(n); }
  col.append(h('div', {class: 'working hidden', id: 'working'}, h('span', {class: 'dots'}, h('i'), h('i'), h('i')), h('span', {class: 'wtext'})));
  const scroll = h('div', {class: 'scroll'}, col);
  if (ARCHIVE) { pane.replaceChildren(scroll); scroll.scrollTop = scroll.scrollHeight; return; }
  const composer = h('div', {class: 'composer'}, h('div', {class: 'col'},
    h('div', {class: 'pending', id: 'pending'}),
    h('form', {onsubmit: e => { e.preventDefault(); send(); }},
      h('textarea', {id: 'input', placeholder: 'Message the agent… (Enter to send, Shift+Enter for a new line)', rows: 1,
        onkeydown: e => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send(); } },
        oninput: e => { e.target.style.height = 'auto'; e.target.style.height = Math.min(200, e.target.scrollHeight) + 'px'; }}),
      h('button', {class: 'primary', type: 'submit'}, 'Send')),
    h('div', {class: 'hint'},
      h('span', {id: 'hint'}),
      h('label', {class: 'toggle'}, h('input', {type: 'checkbox', checked: activityShown(), onchange: e => setActivity(e.target.checked)}), 'show activity'))));
  pane.replaceChildren(scroll, composer);
  scroll.scrollTop = scroll.scrollHeight;
  updateComposer();
  renderLive();
  // No autofocus: the single-key shortcuts must keep working on the default tab; "/" focuses the composer.
}
// An older session's tasks, notes and processes are its history: its
// activity shows unless the user turned it off for older sessions.
function activityShown() { return S.detail && S.detail.legacy ? S.showLegacyActivity : S.showActivity; }
function setActivity(on) {
  const legacy = !!(S.detail && S.detail.legacy);
  if (legacy) S.showLegacyActivity = on; else S.showActivity = on;
  try { localStorage.setItem(legacy ? 'eagent.activity.legacy' : 'eagent.activity', on ? '1' : '0'); } catch (e) {}
  renderTab();
}
function updateComposer() {
  const d = S.detail; const pend = $('#pending'); const hint = $('#hint'); if (!pend || !d) return;
  pend.innerHTML = '';
  if (d.question) pend.append(h('span', null, 'Question pending:'), ...(d.question.Options || []).map(o => h('button', {disabled: !!ARCHIVE, onclick: () => answer(o)}, o)), ...(d.alive && !ARCHIVE ? [h('button', {class: 'ghost', title: 'Close the question without answering. Nothing is sent to the agent.', onclick: dismissQuestion}, 'Dismiss')] : []));
  hint.textContent = d.alive ? (d.question ? 'Pick an option above or type your answer. Dismiss closes the question and sends nothing.' : 'Goes to the agent word for word. Start with @e to talk to eagent instead; /help lists the commands.') : 'The session is not running; sending a message resumes it here.';
}
async function send() {
  const ta = $('#input'); const text = ta.value.trim(); if (!text) return;
  ta.value = ''; ta.style.height = 'auto';
  try {
    if (S.detail && S.detail.question) await api(`/api/sessions/${S.sid}/answer`, {method: 'POST', body: JSON.stringify({text, question_id: S.detail.question.ID, client: clientCaps()})});
    else await api(`/api/sessions/${S.sid}/message`, {method: 'POST', body: JSON.stringify({text, client: clientCaps()})});
  } catch (e) { toast(e.message, 'bad'); }
}
async function dismissQuestion() { try { await api(`/api/sessions/${S.sid}/dismiss`, {method: 'POST', body: JSON.stringify({question_id: S.detail.question.ID})}); } catch (e) { toast(e.message, 'bad'); } }
async function answer(text) { try { await api(`/api/sessions/${S.sid}/answer`, {method: 'POST', body: JSON.stringify({text, question_id: S.detail.question.ID, client: clientCaps()})}); } catch (e) { toast(e.message, 'bad'); } }

// What a thread.title event changed, in words: the session's own summary
// (by "summary"), a name set at the user's request, or the thread as the
// phone app has it. '' when a summary call found nothing to change.
function titleNotice(d) {
  const title = d.title ? `titled “${d.title}”` : '';
  const text = d.description ? clip(d.description, 200) : '';
  if (!title && !text) return '';
  const who = d.by === 'summary' ? '' : d.by === 'finalechat' ? ' (from the phone app)' : ' (set by hand)';
  if (title && text) return `${title}${who} · ${text}`;
  return title ? title + who : `summary${who}: ${text}`;
}

function chatNode(ev) {
  const d = ev.data || {};
  switch (ev.type) {
    case 'user.message': return h('div', {class: 'msg user'}, h('div', {class: 'head'}, 'you' + srcLabel(d.source) + ' · ' + fmtTime(ev.ts)), h('div', {class: 'bubble'}, d.text ? h('div', {html: md(d.text)}) : null, attachmentsNode(d.attachments)));
    case 'user.answer': return h('div', {class: 'msg user'}, h('div', {class: 'head'}, 'your answer' + srcLabel(d.source) + ' · ' + fmtTime(ev.ts)), h('div', {class: 'bubble'}, h('div', {html: md(d.text)}), attachmentsNode(d.attachments)));
    case 'narrator.message': return h('div', {class: 'msg'}, h('div', {class: 'avatar'}, 'e'), h('div', {class: 'mbody'}, h('div', {class: 'head'}, h('b', null, 'eagent'), h('span', null, fmtTime(ev.ts)), d.important ? h('span', {class: 'badge buzz', title: 'sent as important: the phone buzzed'}, 'buzzed') : null), h('div', {class: 'text', html: md(d.text)}), attachmentsNode(d.attachments)));
    case 'phone.thread': return h('div', {class: 'notice'}, 'mirrored to your phone' + (d.remote_mode ? ' (remote mode)' : ''));
    case 'thread.title': return titleNotice(d) ? h('div', {class: 'notice'}, titleNotice(d)) : null;
    case 'cwd.change': return !ev.task ? h('div', {class: 'notice'}, 'working directory is now ' + d.path) : null;
    case 'user.dismiss': return h('div', {class: 'notice'}, 'you dismissed the question' + srcLabel(d.source) + ' · nothing was sent to the agent');
    case 'narrator.question': {
      const answered = S.events.find(e => e.type === 'user.answer' && e.data && e.data.question_id === d.id);
      const dismissed = S.events.find(e => e.type === 'user.dismiss' && e.data && e.data.question_id === d.id);
      return h('div', {class: 'msg', 'data-qid': d.id}, h('div', {class: 'avatar'}, '?'), h('div', {class: 'mbody'}, h('div', {class: 'head'}, h('b', null, 'eagent'), h('span', null, 'needs a decision · ' + fmtTime(ev.ts))),
        h('div', {class: 'qcard'}, h('div', {class: 'text', html: md(d.text)}),
          answered ? h('div', {class: 'answered'}, 'you answered: ' + answered.data.text) : dismissed ? h('div', {class: 'answered'}, 'dismissed without an answer') : h('div', {class: 'options'}, ...(d.options || []).map(o => h('button', {disabled: !!ARCHIVE, onclick: () => answer(o)}, o))))));
    }
    case 'session.start': { const m = d.models || {}; const models = m.eagent ? 'eagent on ' + shortModel(m.eagent) : m.orchestrator ? ['orchestrator', 'task', 'narrator'].map(a => shortModel(m[a]) || '?').join(' / ') : ''; return h('div', {class: 'notice'}, `session started ${fmtDate(ev.ts)}` + (models ? ` · ${models}` : '') + (d.config ? ` · ${d.config}` : '')); }
    case 'session.resume': return h('div', {class: 'notice'}, 'session resumed ' + fmtDate(ev.ts) + (d.closed && d.closed.length ? ' · ' + d.closed.join('; ') : ''));
    case 'session.end': return h('div', {class: 'notice'}, `session ended (${d.reason}) ${fmtDate(ev.ts)}`);
    case 'subsession.start': return d.reason === 'rollover' ? h('div', {class: 'notice'}, `fresh conversation (subsession ${d.index + 1}); a briefing carried over`)
      : d.reason === 'upgrade' ? h('div', {class: 'notice'}, `the current eagent took this older session over here (subsession ${d.index + 1}); it continues from a briefing written from the record above`) : null;
    case 'relay.start': return h('div', {class: 'notice'}, `${agentWho(d.name, d)} ${d.resumed ? 'resumed' : 'started'} in ${d.dir}` + (d.model || d.effort ? ` · ${[shortModel(d.model), d.effort].filter(Boolean).join(' ')}` : ''));
    case 'relay.end': return h('div', {class: 'notice'}, `${agentWho(d.name)} stopped`);
    case 'relay.turn': {
      const text = String(d.text || '').trim();
      const meta = [d.duration_ms ? dur(d.duration_ms) : '', d.tools ? `${d.tools} tool call${d.tools === 1 ? '' : 's'}` : '', d.interrupted ? 'interrupted' : ''].filter(Boolean).join(' · ');
      return h('div', {class: 'msg agent'}, h('div', {class: 'avatar'}, agentWho(d.name).slice(0, 1)), h('div', {class: 'mbody'}, h('div', {class: 'head'}, h('b', null, agentWho(d.name)), h('span', null, 'finished a turn · ' + fmtTime(ev.ts)), meta ? h('span', {class: 'sub'}, meta) : null),
        text ? h('div', {class: 'text', html: md(text)}) : h('div', {class: 'sub'}, 'it ended the turn without a message')));
    }
    case 'relay.delivered': {
      // A quiet receipt for a message of the user's that went to the agent.
      const sent = S.events.find(e => e.type === 'relay.send' && e.data && e.data.id === d.id && e.data.name === d.name);
      if (!sent || sent.data.from !== 'user') return null;
      return h('div', {class: 'receipt' + (d.ok ? '' : ' bad')}, d.ok ? `delivered to ${agentWho(d.name)}` : `did not reach ${agentWho(d.name)}` + (d.detail ? ': ' + clip(d.detail, 160) : ''));
    }
    case 'relay.state':
      // A stop on a dialog and an exit are notices; the rest is activity.
      if (d.state === 'blocked') return h('div', {class: 'notice'}, `${agentWho(d.name)} is stopped on a dialog` + (d.detail ? ': ' + clip(d.detail, 160) : ''));
      if (d.state === 'exited') return h('div', {class: 'notice'}, `${agentWho(d.name)} exited` + (d.detail ? ': ' + clip(d.detail, 160) : ''));
      break;
    case 'relay.limit': return h('div', {class: 'notice'}, `${agentWho(d.name)} usage` + (d.exhausted ? ' ran out' : d.used_percent ? ` at ${Math.round(d.used_percent)}%` : '') + (d.window ? ` (${d.window})` : '') + (d.resets_at ? `, resets ${fmtDate(d.resets_at)}` : '') + (d.text ? ': ' + clip(d.text, 160) : ''));
    case 'relay.autopilot': return h('div', {class: 'notice'}, d.off ? `autopilot off for ${agentWho(d.name)}` : `autopilot on for ${agentWho(d.name)}` + (d.until ? ` until ${fmtDate(d.until)}` : '') + (d.instruction ? ': ' + clip(d.instruction, 160) : ''));
    case 'error': return h('div', {class: 'act bad'}, h('span', {class: 't'}, fmtTime(ev.ts)), h('span', {class: 'k'}, 'error'), h('span', null, clip(d.where + ': ' + d.text, 200)));
  }
  if (!activityShown()) return null;
  switch (ev.type) {
    case 'route': return act(ev, '', 'route', `${d.actor} → ${d.model}`);
    case 'dossier': return act(ev, '', 'fresh conversation', 'briefing written; eagent continues from it');
    case 'relay.send': return act(ev, '', `→ ${agentWho(d.name)}`, `${d.from === 'user' ? 'your message' : 'from eagent'} (${d.mode}): ${d.text}`);
    case 'schedule.create': return act(ev, '', `schedule ${d.id}`, `${d.kind} ${d.spec}: ${d.note}`);
    case 'schedule.fire': return act(ev, '', `schedule ${d.id} fired`, d.note);
    case 'schedule.cancel': return act(ev, '', `schedule ${d.id} cancelled`, d.reason || '');
    case 'tool.result': return d.images && d.images.length ? h('div', null, act(ev, d.is_error ? 'bad' : '', d.name, d.output), attachmentsNode(d.images)) : null;
    case 'relay.progress': return act(ev, '', agentWho(d.name), d.text);
    case 'relay.state': return act(ev, '', agentWho(d.name), d.state + (d.detail ? ': ' + d.detail : ''));
    case 'harness.message': return act(ev, '', 'notice', d.text);
  }
  switch (ev.type) {
    case 'note': return act(ev, 'note', d.answer ? 'answer' : 'note', d.text);
    case 'task.create': return act(ev, '', d.kind === 'dossier' ? 'notes for a fresh context' : 'delegated ' + d.id, d.title + (d.description ? ': ' + d.description : ''));
    case 'task.start': return act(ev, '', `${ev.task || d.id} started`, '');
    case 'task.end': return act(ev, d.status === 'completed' ? 'ok' : 'bad', `${d.id} ${d.status}`, d.summary);
    case 'yield': return act(ev, d.done ? 'ok' : '', d.done ? 'done' : 'waiting', d.reason);
    case 'proc.start': return act(ev, '', `${d.handle} started` + (ev.task ? ' · ' + ev.task : ''), '$ ' + d.command);
    case 'proc.exit': return act(ev, d.exit_code === 0 ? '' : 'bad', `${d.handle} ${d.reason}`, `exit ${d.exit_code}` + (d.duration_ms ? ' after ' + dur(d.duration_ms) : '') + (d.tail ? ' · ' + d.tail : ''));
    case 'proc.pid': return null;
    case 'narrator.trim': return act(ev, '', 'narrator trimmed', `its view starts at #${d.from_seq}` + (d.recap ? ': ' + clip(d.recap, 200) : ''));
  }
  if (LEGACY_TYPES.has(ev.type)) return act(ev, '', ev.type + (ev.task ? ' ' + ev.task : ''), legacyText(d));
  return null;
}
// Event types only sessions from before eagent was one loop hold. They are
// shown as plain history.
const LEGACY_TYPES = new Set(['task.create', 'task.start', 'task.end', 'note', 'yield', 'proc.start', 'proc.pid', 'proc.exit', 'narrator.trim']);
const legacyText = d => d.text || d.title || d.summary || d.reason || d.command || d.note || d.id || '';
// agentWho is how the session's agent called name reads for people.
function agentWho(name, start) {
  if (start && start.agent_label) return start.agent_label;
  const a = (S.detail && S.detail.agents || []).find(x => x.name === name);
  return a ? a.who : (name || 'the agent');
}
function attachmentsNode(atts) {
  if (!atts || !atts.length) return null;
  if (ARCHIVE) return ARCHIVE.attachments(atts);
  const base = (a) => a.path.split('/').pop();
  const url = (a) => `/api/sessions/${S.sid}/attachments/${encodeURIComponent(base(a))}`;
  return h('div', {class: 'atts'}, ...atts.map(a => a.kind === 'image' || /^image\//.test(a.content_type)
    ? h('a', {class: 'att-img', href: url(a), target: '_blank', title: `${a.name} · ${a.content_type} · ${fmtBytes(a.size)}`}, h('img', {src: url(a), alt: a.name, loading: 'lazy'}))
    : h('a', {class: 'att-file', href: url(a), target: '_blank'}, '📎 ', a.name, h('span', {class: 'sub'}, ` ${a.content_type} · ${fmtBytes(a.size)}`))));
}
const fmtBytes = n => n >= 1048576 ? (n/1048576).toFixed(1) + ' MB' : n >= 1024 ? Math.round(n/1024) + ' KB' : (n||0) + ' B';
function srcLabel(src) { return src === 'finalechat' ? ' · from your phone' : src === 'web' ? ' · web' : ''; }
function act(ev, cls, kind, text) { return h('div', {class: 'act ' + cls}, h('span', {class: 't'}, fmtTime(ev.ts)), h('span', {class: 'k'}, kind), h('span', null, clip(text, 300))); }

// ---- usage ----------------------------------------------------------------------
function usageDigest(d) {
  if (!d) return '';
  return JSON.stringify([d.last_seq, d.idle, !!d.question, schedulesOf(S.events).map(s => [s.id, s.fires]), d.usage.map(u => [u.actor, u.calls]), (d.agents || []).map(a => [a.name, a.state, a.turns])]);
}
// det is a <details> that remembers being opened or closed by the user, so a
// re-render (every status frame while work runs) does not fold it back.
function det(key, defaultOpen, ...children) {
  const open = S.open.has(key) ? true : S.closed.has(key) ? false : !!defaultOpen;
  return h('details', {open, ontoggle: e => { if (e.target.open) { S.open.add(key); S.closed.delete(key); } else { S.closed.add(key); S.open.delete(key); } if (ARCHIVE) ARCHIVE.changed?.(); }}, ...children);
}
function renderUsage(pane) {
  pane.classList.remove('chat'); pane.style.padding = '';
  const y = pane.scrollTop;
  const d = S.detail;
  const usage = h('div', {class: 'stats'}, ...d.usage.filter(u => u.calls).map(u => h('div', {class: 'stat'},
    h('span', {class: 'v'}, u.priced ? money(u.cost_usd) : k(u.input) + ' in'),
    h('span', {class: 'l'}, `${u.actor} · ${shortModel(u.model)}`),
    h('span', {class: 'l'}, `${u.calls} calls · ${k(u.input)} in · ${k(u.output)} out · cache ${Math.round(u.cache_ratio * 100)}%`),
    u.p50_ms ? h('span', {class: 'l'}, `latency p50 ${dur(u.p50_ms)} · p95 ${dur(u.p95_ms)}`) : null)),
    d.priced && d.cost_usd > 0 ? h('div', {class: 'stat total'}, h('span', {class: 'v'}, money(d.cost_usd)), h('span', {class: 'l'}, 'estimated total at list prices'), h('span', {class: 'l'}, `${k(d.tokens)} input tokens · ${dur(d.duration_s * 1000)}`)) : null);
  const agents = d.agents || [];
  const table = h('table', null, h('thead', null, h('tr', null, ...['agent', 'model', 'state', 'turns', 'folder'].map(x => h('th', null, x)))),
    h('tbody', null, ...agents.map(a => h('tr', null, h('td', null, h('b', null, a.who), a.label && a.label !== a.who ? h('span', {class: 'sub'}, ' ' + a.label) : null),
      h('td', null, [shortModel(a.model), a.effort].filter(Boolean).join(' ') || h('span', {class: 'sub'}, 'its own default')),
      h('td', null, h('span', {class: 'badge ' + a.state}, a.state), a.autopilot ? h('span', {class: 'badge', style: 'margin-left:6px'}, 'autopilot') : null),
      h('td', {class: 'num'}, a.turns), h('td', {class: 'wrap'}, h('code', {class: 'inline'}, a.dir))))));
  const parts = [usage, chartsCard(d, pane.clientWidth), h('div', {class: 'card'}, h('h3', null, 'Agents'), agents.length ? table : h('div', {class: 'sub'}, d.legacy ? 'This session is from before eagent ran an agent in a terminal.' : 'No agent has been started.'))];
  const scheds = schedulesOf(S.events);
  if (scheds.length) parts.push(h('div', {class: 'card'}, h('h3', null, 'Schedules'), ...scheds.map(s => h('div', null, h('code', {class: 'inline'}, `${s.id} ${s.kind} ${s.spec}`), ' ', s.note, h('span', {class: 'sub'}, (s.next ? ` · next ${fmtTime(s.next)}` : '') + ` · fired ${s.fires}×`)))));
  pane.replaceChildren(...parts);
  pane.scrollTop = y;
}

// schedulesOf folds the log's scheduled wake-ups into the ones that will
// fire again.
function schedulesOf(evs) {
  const by = new Map();
  for (const e of evs) {
    const d = e.data || {};
    if (e.type === 'schedule.create') by.set(d.id, {id: d.id, kind: d.kind, spec: d.spec, note: d.note, next: d.next, fires: 0});
    else if (e.type === 'schedule.fire' && by.has(d.id)) { const s = by.get(d.id); s.fires++; s.next = d.next; if (s.kind === 'once' || !when(d.next)) by.delete(d.id); }
    else if (e.type === 'schedule.cancel') by.delete(d.id);
  }
  return [...by.values()];
}

// isBoundary says an event starts a fresh conversation: a rollover, or the
// current eagent taking an older session over.
const isBoundary = e => e.type === 'subsession.start' && e.data && (e.data.reason === 'rollover' || e.data.reason === 'upgrade');

// isMain says an event is eagent's own (or, in an old session, the
// orchestrator's outside any task).
const isMain = e => e.actor === 'eagent' || (e.actor === 'orchestrator' && !e.task);

// chartsCard: a timeline of when eagent was thinking and when each agent
// was working, and eagent's prompt size per call with fresh-conversation
// resets.
function chartsCard(d, paneW) {
  const evs = S.events;
  const start = evs.length ? new Date(evs[0].ts).getTime() : Date.now();
  const last = evs.length ? new Date(evs[evs.length-1].ts).getTime() : start;
  const end = Math.max(start + 1000, d.alive ? Date.now() : last);
  const span = end - start;
  // Drawn at the pane's width so text stays 10px on a phone instead of scaling down with the viewBox.
  const W = Math.max(340, Math.round((paneW || 900) - 34)), narrow = W < 560;
  const LW = narrow ? 76 : 124, RW = W - LW - 12;
  const x = t => LW + (Math.min(Math.max(t, start), end) - start) / span * RW;
  const rows = [];
  const turns = []; let open = null;
  for (const e of evs) { if (isMain(e) && e.type === 'turn.start') open = new Date(e.ts).getTime(); if (isMain(e) && e.type === 'turn.end' && open) { turns.push([open, new Date(e.ts).getTime()]); open = null; } }
  if (open) turns.push([open, end]);
  rows.push({label: 'eagent', bars: turns.map(([a, b]) => ({a, b, cls: 'orch'}))});
  // Each agent's working spans, from its state changes.
  const spans = new Map(); const since = new Map();
  for (const e of evs) {
    if (e.type !== 'relay.state' || !e.data) continue;
    const n = e.data.name, t = new Date(e.ts).getTime();
    if (!spans.has(n)) spans.set(n, []);
    if (e.data.state === 'working') { if (!since.has(n)) since.set(n, t); }
    else if (since.has(n)) { spans.get(n).push([since.get(n), t]); since.delete(n); }
  }
  for (const [n, t] of since) spans.get(n).push([t, end]);
  for (const [n, bars] of spans) rows.push({label: clip(agentWho(n), narrow ? 10 : 18), bars: bars.map(([a, b]) => ({a, b, cls: 'running', title: `${agentWho(n)} working · ${dur(b - a)}`}))});
  const RH = 18, top = 22, height = top + rows.length * RH + 8;
  const g = svg('svg', {viewBox: `0 0 ${W} ${height}`, class: 'gantt', preserveAspectRatio: 'none'});
  // time axis
  const ticks = narrow ? 3 : 6;
  for (let i = 0; i <= ticks; i++) { const t = start + span * i / ticks; g.append(svg('line', {x1: x(t), x2: x(t), y1: top - 4, y2: height, class: 'grid'}), svg('text', {x: x(t), y: 12, class: 'tick', 'text-anchor': i === 0 ? 'start' : i === ticks ? 'end' : 'middle'}, dur(t - start))); }
  // rollover markers
  for (const e of evs) if (isBoundary(e)) { const xx = x(new Date(e.ts).getTime()); g.append(svg('line', {x1: xx, x2: xx, y1: top - 4, y2: height, class: 'rollover'}), svg('title', null, e.data.reason === 'upgrade' ? 'taken over by the current eagent' : 'fresh conversation')); }
  rows.forEach((r, i) => {
    const y = top + i * RH;
    g.append(svg('text', {x: LW - 6, y: y + 13, class: 'label', 'text-anchor': 'end'}, r.label));
    for (const b of r.bars) { const rect = svg('rect', {x: x(b.a), y: y + 3, width: Math.max(2, x(b.b) - x(b.a)), height: RH - 6, rx: 3, class: 'bar ' + b.cls}); if (b.title) rect.append(svg('title', null, b.title)); g.append(rect); }
  });
  // context sparkline
  const calls = evs.filter(e => e.type === 'assistant' && isMain(e) && e.data.usage);
  let spark = null;
  if (calls.length > 1) {
    const SH = 70, ST = 16, maxIn = Math.max(...calls.map(c => c.data.usage.input), 1);
    const sy = v => ST + (SH - 6) * (1 - v / maxIn);
    spark = svg('svg', {viewBox: `0 0 ${W} ${SH + 18}`, class: 'spark', preserveAspectRatio: 'none'});
    for (const e of evs) if (isBoundary(e)) { const xx = x(new Date(e.ts).getTime()); spark.append(svg('line', {x1: xx, x2: xx, y1: ST - 4, y2: SH + 4, class: 'rollover'})); }
    let path = '', prevSub = null;
    for (const c of calls) { const t = new Date(c.ts).getTime(); const px = x(t), py = sy(c.data.usage.input); const sub = c.file; path += (path && sub === prevSub ? ' L' : ' M') + px.toFixed(1) + ' ' + py.toFixed(1); prevSub = sub; }
    spark.append(svg('path', {d: path.trim(), class: 'line'}));
    for (const c of calls) { const dot = svg('circle', {cx: x(new Date(c.ts).getTime()), cy: sy(c.data.usage.input), r: 2.2, class: 'dot' + (c.data.usage.cached / Math.max(c.data.usage.input, 1) > 0.5 ? ' cached' : '')}); dot.append(svg('title', null, `${k(c.data.usage.input)} in (${Math.round(100 * c.data.usage.cached / Math.max(c.data.usage.input, 1))}% cached) · ${dur(c.data.elapsed_ms || 0)} · ${fmtTime(c.ts)}`)); spark.append(dot); }
    spark.append(svg('text', {x: LW - 6, y: ST + 4, class: 'label', 'text-anchor': 'end'}, k(maxIn)), svg('text', {x: LW - 6, y: SH, class: 'label', 'text-anchor': 'end'}, '0'), svg('text', {x: LW - 6, y: ST + (SH - 6) / 2 + 4, class: 'label dim', 'text-anchor': 'end'}, 'context'));
  }
  return h('div', {class: 'card'}, h('h3', null, 'Timeline ', h('span', {class: 'sub'}, `${dur(span)} · dotted lines mark a fresh conversation`)), g,
    spark ? h('div', null, h('div', {class: 'sub', style: 'margin:8px 0 2px'}, 'eagent prompt size per call (green dots were mostly served from cache)'), spark) : null);
}

// ---- timeline -----------------------------------------------------------------------
// The actors a log names, in a steady order: the one loop's first, then
// those of older sessions.
const ACTOR_ORDER = ['user', 'eagent', 'summarizer', 'harness', 'orchestrator', 'task', 'narrator'];
const actorsOf = evs => { const seen = new Set(evs.map(e => e.actor).filter(Boolean)); return [...ACTOR_ORDER.filter(a => seen.has(a)), ...[...seen].filter(a => !ACTOR_ORDER.includes(a)).sort()]; };
function passesFilter(ev) {
  if (S.filters.size && !S.filters.has(ev.actor)) return false;
  if (S.search) { const q = S.search.toLowerCase(); if (!(ev.type + ' ' + summary(ev) + ' ' + (ev.task || '')).toLowerCase().includes(q)) return false; }
  return true;
}
function tlUpdateCount() {
  const tb = $('.tl tbody'), c = $('#tlcount');
  if (tb && c) c.textContent = `${tb.querySelectorAll('tr.row').length} of ${S.events.length} events`;
}
function renderTimeline(pane) {
  pane.classList.remove('chat'); pane.style.padding = '';
  const chips = actorsOf(S.events).map(a => h('span', {class: 'chip' + (S.filters.has(a) ? ' on' : ''), onclick: () => { S.filters.has(a) ? S.filters.delete(a) : S.filters.add(a); renderTab(); }}, a));
  const search = h('input', {class: 'search', placeholder: 'search events…', value: S.search, oninput: e => { S.search = e.target.value; const tb = $('.tl tbody'); tb.replaceChildren(...S.events.filter(passesFilter).map(tlRow)); tlUpdateCount(); }});
  const tbody = h('tbody', null, ...S.events.filter(passesFilter).map(tlRow));
  pane.replaceChildren(h('div', {class: 'filters'}, h('span', {class: 'sub'}, 'actor:'), ...chips, search, h('span', {class: 'sub', id: 'tlcount', style: 'margin-left:auto'}, `${tbody.children.length} of ${S.events.length} events`)),
    h('table', {class: 'tl'}, h('thead', null, h('tr', null, h('th', null, '#'), h('th', null, 'time'), h('th', null, 'actor'), h('th', null, 'type'), h('th', null, 'what happened'))), tbody));
}
function tlRow(ev) {
  const start = (ARCHIVE && when(S.detail?.started)) || (S.events.length ? new Date(S.events[0].ts) : new Date(ev.ts));
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
  for (const key of ['text', 'query', 'reason', 'action', 'instruction', 'profile', 'title', 'file', 'command', 'path']) if (typeof args[key] === 'string') return args[key];
  return JSON.stringify(args);
}
function summary(ev) {
  const d = ev.data || {};
  switch (ev.type) {
    case 'assistant': return [d.text, ...(d.tool_calls || []).map(t => `${t.name}(${clip(argPreview(t.args), 80)})`)].filter(Boolean).join(' | ');
    case 'tool.result': return `${d.name}${d.is_error ? ' ERROR' : ''} → ${clip(d.output, 200)}`;
    case 'user.message': case 'narrator.message': case 'narrator.question': case 'harness.message': case 'dossier': case 'steer': return clip(d.text, 240);
    case 'cwd.change': return 'working directory → ' + d.path;
    case 'turn.start': case 'turn.end': return d.reason || '';
    case 'thread.title': return titleNotice(d) || 'summary checked: nothing to change';
    case 'relay.start': return `${d.name} (${d.agent}) in ${d.dir}` + (d.model ? ` · ${d.model}` : '') + (d.effort ? ` ${d.effort}` : '');
    case 'relay.send': return `→ ${d.name} ${d.mode} from ${d.from}: ${clip(d.text, 200)}`;
    case 'relay.state': return `${d.name} ${d.state}` + (d.detail ? ': ' + clip(d.detail, 200) : '');
    case 'relay.progress': return `${d.name}: ${clip(d.text, 200)}`;
    case 'relay.turn': return `${d.name} finished a turn` + (d.tools ? ` (${d.tools} tools)` : '') + ': ' + clip(d.text, 200);
    case 'task.create': return `${d.id} ${d.title}`;
    case 'task.end': return `${d.id} ${d.status}: ${clip(d.summary, 200)}`;
    case 'proc.start': return `${d.handle} $ ${clip(d.command, 200)}`;
    case 'proc.exit': return `${d.handle} ${d.reason} exit=${d.exit_code}`;
    case 'yield': return (d.done ? 'DONE: ' : 'waiting: ') + (d.reason || '');
    case 'note': return clip(d.text, 240);
    case 'task.start': return `${d.id || ev.task} started`;
    case 'proc.pid': return `${d.handle} pid ${d.pid}`;
    case 'narrator.trim': return `narrator conversation trimmed from #${d.from_seq}` + (d.recap ? ': ' + clip(d.recap, 200) : '');
    case 'subsession.start': return `${d.file} (${d.reason})`;
    default: return clip(JSON.stringify(d), 200);
  }
}

// ---- config ---------------------------------------------------------------------------
async function renderConfig() {
  // The editor lives in config.js and shares this file's helpers through window.__eagent.
  if (!window.EagentConfig) { await new Promise(r => window.addEventListener('load', r, {once: true})); }
  return window.EagentConfig.render();
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
// A new session is a first message, the folder its agent starts in, the
// agent profile (the project's default preselected) and, optionally, that
// agent's model and effort. eagent's own model comes from the configuration.
// opts.project starts it on the project's own configuration, not the
// server's --preset or --config.
async function newSessionDialog(opts = {}) {
  const ta = h('textarea', {placeholder: 'What should the agent do? This is sent to it word for word.'});
  const dir = h('input', {class: 'search', placeholder: 'folder (default: the project root)', autocomplete: 'off', style: 'flex:1; min-width:0'});
  const agentSel = h('select', null, h('option', {value: ''}, 'loading agents…'));
  const modelList = h('datalist', {id: 'agent-models'});
  const model = h('input', {class: 'search', list: 'agent-models', autocomplete: 'off', style: 'flex:1; min-width:0'});
  const effortSel = h('select');
  let agents = [];
  try { agents = await api('/api/agents'); } catch (e) { toast(e.message, 'bad'); }
  const picked = () => agents.find(a => a.name === agentSel.value);
  const fill = () => {
    const a = picked();
    model.value = '';
    model.placeholder = a ? `model (default: ${a.model || 'its own'})` : 'model';
    modelList.replaceChildren(...(a ? a.models : []).map(m => h('option', {value: m.id}, m.label && m.label !== m.id ? m.label : null)));
    effortSel.replaceChildren(h('option', {value: ''}, `effort: ${a && a.effort ? a.effort + ' (default)' : 'default'}`), ...(a ? a.efforts : []).map(e => h('option', {value: e}, e)));
  };
  agentSel.replaceChildren(...agents.map(a => h('option', {value: a.name, selected: !!a.default}, a.who + (a.label && a.label !== a.who ? ' · ' + a.label : '') + (a.default ? ' (default)' : ''))));
  if (!agents.length) agentSel.append(h('option', {value: ''}, 'the default agent'));
  agentSel.addEventListener('change', fill);
  fill();
  const start = h('button', {class: 'primary', onclick: async () => {
    const prompt = ta.value.trim(); if (!prompt) return;
    start.disabled = true;
    const body = {prompt, dir: dir.value.trim(), relay: agentSel.value, relay_model: model.value.trim(), relay_effort: effortSel.value, client: clientCaps()};
    if (opts.project) body.use_project = true;
    try { const r = await api('/api/sessions', {method: 'POST', body: JSON.stringify(body)}); closeModal(); await loadSessions(); location.hash = `#/s/${r.id}/chat`; }
    catch (e) { toast(e.message, 'bad'); start.disabled = false; }
  }}, 'Start');
  ta.addEventListener('keydown', e => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) start.click(); });
  showModal(h('div', null, h('h2', null, 'New session'), opts.project ? h('p', {class: 'sub'}, 'It runs on the project\'s configuration as saved.') : null, ta,
    h('div', {class: 'row'}, h('span', {class: 'sub'}, 'folder'), dir),
    h('div', {class: 'row'}, h('span', {class: 'sub'}, 'agent'), agentSel),
    h('div', {class: 'row'}, h('span', {class: 'sub'}, 'its model'), model, modelList, effortSel),
    h('div', {class: 'foot'}, h('span', {class: 'sub'}, '⌘/Ctrl+Enter to start'), h('span', {style: 'flex:1'}), h('button', {onclick: closeModal}, 'Cancel'), start)));
  ta.focus();
}
function showModal(content) { const m = $('#modal'); m.replaceChildren(h('div', {class: 'box'}, content)); m.classList.remove('hidden'); m.onclick = e => { if (e.target === m) closeModal(); }; }
function closeModal() { $('#modal').classList.add('hidden'); }

// ---- keyboard --------------------------------------------------------------------------------
document.addEventListener('keydown', e => {
  if (ARCHIVE) return;
  const typing = ['INPUT', 'TEXTAREA', 'SELECT'].includes(document.activeElement?.tagName);
  if (e.key === 'Escape') { closeModal(); return; }
  if (typing || e.metaKey || e.ctrlKey || e.altKey) return;
  if (e.key === 'n') { e.preventDefault(); newSessionDialog(); }
  else if (e.key === '/' && S.sid) { e.preventDefault(); location.hash = `#/s/${S.sid}/chat`; setTimeout(() => $('#input')?.focus(), 50); }
  else if (['1', '2', '3'].includes(e.key) && S.sid) { location.hash = `#/s/${S.sid}/${TABS[+e.key - 1]}`; }
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

window.__eagent = {$, h, api, toast, toastHost, showModal, closeModal, newSessionDialog, promptEditor, k, money, dur, clip, S, renderLive, closeStream, loadSessions};

let resizeTimer = null;
window.addEventListener('resize', () => { clearTimeout(resizeTimer); resizeTimer = setTimeout(() => { if (S.sid && S.tab === 'usage') renderTab(); }, 150); });

// The portable archive uses these exact session renderers and stylesheet.
// It supplies replayed data directly; no local server or browser storage is used.
if (ARCHIVE) {
  window.EagentSessionView = {
    state: () => ({open: [...S.open].slice(-200), closed: [...S.closed].slice(-200)}),
    restore(saved) {
      for (const key of ['open', 'closed']) S[key] = new Set(Array.isArray(saved?.[key]) ? saved[key].filter(v => typeof v === 'string' && v.length < 200).slice(-200) : []);
    },
    render(pane, detail, events, tab) {
      S.sid = detail.id; S.detail = {...detail, alive: false}; S.events = events;
      S.tab = tab; S.search = ''; S.filters.clear();
      if (tab === 'usage') renderUsage(pane);
      else if (tab === 'timeline') renderTimeline(pane);
      else renderChat(pane);
    },
  };
  return;
}

// ---- boot --------------------------------------------------------------------------------
$('#btn-new').onclick = () => newSessionDialog();
$('#btn-refresh').onclick = loadSessions;
api('/api/health').then(hh => { $('#project').textContent = hh.project; $('#project').title = hh.project; }).catch(() => {});
if ('Notification' in window && Notification.permission === 'default') { document.addEventListener('click', () => Notification.requestPermission(), {once: true}); }
loadSessions().then(route);
setInterval(loadSessions, 15000);
})();
