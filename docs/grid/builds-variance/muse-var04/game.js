/* ============================================================================
   OVER THE TOP — a neon Breakout. Plain canvas, no dependencies.
   Logical playfield: 800 x 1080. Canvas scales with DPR + CSS.
   - 2D paddle (X + Y band), upward SMASH adds ball energy
   - Conditional bricks (no HP): steep / shallow / fast / slow / mover / bomb
   - 9 power-ups, multiball, OTT "above the bricks" fantasy, full juice + synth
   ============================================================================ */
(function () {
'use strict';

/* ---------------- utils ---------------- */
const clamp = (v, a, b) => v < a ? a : (v > b ? b : v);
const lerp = (a, b, t) => a + (b - a) * t;
const rand = (a, b) => a + Math.random() * (b - a);
const TAU = Math.PI * 2;
function store(k, v) { try { localStorage.setItem(k, String(v)); } catch (e) {} }
function load(k, d) { try { const v = localStorage.getItem(k); return v == null ? d : v; } catch (e) { return d; } }

/* ---------------- constants ---------------- */
const W = 800, H = 1080;            // logical playfield
const TOP_SAFE = 104;               // bricks start below this
const PADDLE_Y_MIN = H * 0.70;      // paddle band top (~bottom 30%)
const PADDLE_Y_MAX = H - 78;        // paddle band bottom
const BASE_BALL_SPEED = 620;
const MIN_BALL_SPEED = 400;
const MAX_BALL_SPEED = 1250;
const V_FAST_NEED = 600;            // velocity brick: break if speed above this
const F_SLOW_NEED = 700;            // feather brick: break if speed below this
const STEEP_MAX_AX = 0.574;         // |vx|/speed below this = steep (35 deg)
const SHALLOW_MIN_AX = 0.42;        // |vx|/speed above this = shallow/grazing
const OTT_TICK = 0.5;               // OTT multiplier bonus per second
const OTT_CAP = 5.0;                // OTT bonus cap
const START_LIVES = 3;

/* ---------------- canvas ---------------- */
const canvas = document.getElementById('game');
const ctx = canvas.getContext('2d');
let viewScale = 1, dpr = 1, cssW = 0, cssH = 0;
function resize() {
  const r = canvas.parentElement.getBoundingClientRect();
  cssW = Math.max(280, r.width); cssH = Math.max(400, r.height);
  dpr = Math.min(2.5, window.devicePixelRatio || 1);
  canvas.width = Math.round(cssW * dpr); canvas.height = Math.round(cssH * dpr);
  viewScale = Math.min(cssW / W, cssH / H);
}
window.addEventListener('resize', resize);

/* ---------------- DOM refs ---------------- */
const $ = id => document.getElementById(id);
const hudEl = $('hud'), scoreEl = $('hud-score'), hiEl = $('hud-hi'), multEl = $('hud-mult'),
  multFill = $('mult-fill'), livesEl = $('hud-lives'), levelEl = $('hud-level'),
  fxTimers = $('fx-timers'), ottBanner = $('ott-banner'), comboPop = $('combo-pop'),
  floatLayer = $('float-layer'), toastEl = $('toast'), joyBase = $('joy-base'), joyKnob = $('joy-knob');
const overlays = { menu: $('menu'), howto: $('howto'), pause: $('pause-menu'), over: $('gameover'), win: $('victory') };
function showOverlay(el) { for (const k in overlays) overlays[k].classList.add('hidden'); if (el) el.classList.remove('hidden'); }
function toast(msg, ms) {
  toastEl.textContent = msg; toastEl.classList.remove('hidden');
  clearTimeout(toastEl._t); toastEl._t = setTimeout(() => toastEl.classList.add('hidden'), ms || 1400);
}
// DOM floating text (crisp over canvas)
function domFloat(x, y, text, color) {
  const r = canvas.getBoundingClientRect();
  const sx = r.width / W, sy = r.height / H;
  const s = Math.min(sx, sy);
  const offX = (r.width - W * s) / 2, offY = (r.height - H * s) / 2;
  const d = document.createElement('div');
  d.className = 'float-txt'; d.textContent = text;
  d.style.left = (offX + x * s) + 'px'; d.style.top = (offY + y * s) + 'px';
  d.style.color = color || '#fff';
  floatLayer.appendChild(d);
  setTimeout(() => d.remove(), 1050);
  while (floatLayer.children.length > 24) floatLayer.firstChild.remove();
}

/* ---------------- game state ---------------- */
let state = 'menu';           // menu|howto|intro|serving|playing|paused|clear|over|win
let pausedFrom = 'playing';
let score = 0, hiScore = parseInt(load('ott_hiscore', '0'), 10) || 0;
let lives = START_LIVES, levelIndex = 0, combo = 0, comboTimer = 0;
let baseMult = 1;             // from combo: 1 + combo*0.1 capped
let ottActive = false, ottBonus = 0, ottBank = 0;
let shake = 0, hitStop = 0, slowMo = 0, timeScale = 1, elapsed = 0;
let bricks = [], balls = [], powerups = [], particles = [], rings = [], floaters = [], lasers = [];
let bricksLeft = 0, bricksBrokenThisLevel = 0, maxCombo = 0, ottPeak = 0;
let countdownT = 0, clearT = 0, introT = 0, announcedCombo = 0;
let bgHue = 185, bgPulse = 0;
let stars = [];
for (let i = 0; i < 130; i++) stars.push({ x: Math.random() * W, y: Math.random() * H, z: rand(0.2, 1), tw: rand(0, TAU) });

/* power-up effect timers: key -> time left */
let fx = {};   // {fire:10, phase:8, big:8, slow:8, wide:12, shield:15, laser:10}
const FX_DUR = { fire: 10, phase: 8, big: 8, slow: 8, wide: 12, shield: 15, laser: 10 };
const FX_ICON = { fire: '🔥', phase: '👻', big: '●', slow: '◷', wide: '▬', shield: '⛨', laser: '⌁' };
const FX_NAME = { fire: 'FIRE', phase: 'GHOST', big: 'BIG', slow: 'SLOW', wide: 'WIDE', shield: 'SHIELD', laser: 'LASER' };

/* ---------------- paddle ---------------- */
const paddle = {
  x: W / 2, y: H - 140, w: 120, h: 20, vx: 0, vy: 0,
  px: W / 2, py: H - 140, tilt: 0, squash: 0, trail: [], targetX: W / 2, targetY: H - 140,
  hasMouse: false, cool: 0,
};
function paddleW() { return paddle.w * (fx.wide ? 1.6 : 1); }

/* served ball sticks to paddle */
let serveBall = null;

/* ---------------- input: keyboard + mouse + touch thumbstick ---------------- */
const keys = {};
let joyActive = false, joyId = null, joyOX = 0, joyOY = 0, joyDX = 0, joyDY = 0;
let dragPaddle = false, dragId = null, dragOffX = 0, dragOffY = 0;
let lastPointerX = 0, lastPointerY = 0, pointerOnScreen = false;

function toLogical(e) {
  const r = canvas.getBoundingClientRect();
  const s = Math.min(r.width / W, r.height / H);
  const ox = (r.width - W * s) / 2, oy = (r.height - H * s) / 2;
  return { x: (e.clientX - r.left - ox) / s, y: (e.clientY - r.top - oy) / s };
}
function showJoy(cx, cy) {
  const r = canvas.getBoundingClientRect();
  const s = Math.min(r.width / W, r.height / H);
  const ox = (r.width - W * s) / 2, oy = (r.height - H * s) / 2;
  joyBase.style.left = (ox + cx * s) + 'px'; joyBase.style.top = (oy + cy * s) + 'px';
  joyBase.classList.remove('hidden');
}
function moveJoy(dx, dy) {
  const m = 44;
  joyKnob.style.transform = `translate(calc(-50% + ${clamp(dx / 800 * m * 3, -m, m)}px), calc(-50% + ${clamp(dy / 800 * m * 3, -m, m)}px))`;
}
function hideJoy() { joyBase.classList.add('hidden'); }

window.addEventListener('keydown', e => {
  if (['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', ' '].includes(e.key)) e.preventDefault();
  keys[e.key.toLowerCase()] = true;
  AudioSys.init();
  if (e.key === ' ' || e.key === 'Enter') primaryAction();
  if (e.key === 'p' || e.key === 'P' || e.key === 'Escape') togglePause();
  if (e.key === 'm' || e.key === 'M') toggleMute();
});
window.addEventListener('keyup', e => { keys[e.key.toLowerCase()] = false; });

canvas.addEventListener('mousemove', e => {
  const p = toLogical(e);
  lastPointerX = clamp(p.x, 0, W); lastPointerY = clamp(p.y, 0, H);
  pointerOnScreen = true; paddle.hasMouse = true;
  paddle.targetX = lastPointerX; paddle.targetY = clamp(lastPointerY, PADDLE_Y_MIN, PADDLE_Y_MAX);
});
canvas.addEventListener('mouseleave', () => { pointerOnScreen = false; paddle.hasMouse = false; });
canvas.addEventListener('mousedown', e => { AudioSys.init(); primaryAction(); });

// Unified pointer (touch) controls:
// - touch starting NEAR paddle (within 130px) => direct 1:1 drag
// - touch starting elsewhere => dynamic-origin thumbstick
canvas.addEventListener('pointerdown', e => {
  AudioSys.init();
  if (e.pointerType === 'mouse') return;
  e.preventDefault();
  const p = toLogical(e);
  try { canvas.setPointerCapture(e.pointerId); } catch (err) {}
  const nearPaddle = Math.hypot(p.x - paddle.x, p.y - paddle.y) < 150;
  if (nearPaddle && !dragPaddle) {
    dragPaddle = true; dragId = e.pointerId;
    dragOffX = paddle.x - p.x; dragOffY = paddle.y - p.y;
  } else if (!joyActive) {
    joyActive = true; joyId = e.pointerId;
    joyOX = p.x; joyOY = p.y; joyDX = 0; joyDY = 0;
    showJoy(p.x, p.y); moveJoy(0, 0);
  }
}, { passive: false });
canvas.addEventListener('pointermove', e => {
  if (e.pointerType === 'mouse') return;
  e.preventDefault();
  const p = toLogical(e);
  if (dragPaddle && e.pointerId === dragId) {
    paddle.targetX = clamp(p.x + dragOffX, 0, W);
    paddle.targetY = clamp(p.y + dragOffY, PADDLE_Y_MIN, PADDLE_Y_MAX);
    paddle.hasMouse = true;
  } else if (joyActive && e.pointerId === joyId) {
    joyDX = p.x - joyOX; joyDY = p.y - joyOY;
    moveJoy(joyDX, joyDY);
  }
}, { passive: false });
function pointerEnd(e) {
  if (e.pointerType === 'mouse') return;
  if (e.pointerId === dragId) { dragPaddle = false; dragId = null; }
  if (e.pointerId === joyId) { joyActive = false; joyId = null; joyDX = joyDY = 0; hideJoy(); }
}
canvas.addEventListener('pointerup', e => {
  if (e.pointerType !== 'mouse') { pointerEnd(e); primaryAction(); }
});
canvas.addEventListener('pointercancel', e => { if (e.pointerType !== 'mouse') pointerEnd(e); });
// stop scroll/zoom gestures
document.addEventListener('touchmove', e => e.preventDefault(), { passive: false });
document.addEventListener('gesturestart', e => e.preventDefault());
document.addEventListener('dblclick', e => e.preventDefault());

function primaryAction() {
  if (state === 'menu' || state === 'over' || state === 'win') return; // buttons handle it
  if (state === 'howto') return;
  if (state === 'paused') { resumeGame(); return; }
  if (fx.laser && state === 'playing') fireLasers();
  if (state === 'serving') launchBall();
}
function toggleMute() {
  AudioSys.init();
  AudioSys.setMuted(!AudioSys.muted);
  $('btn-mute').textContent = AudioSys.muted ? '🔇' : '🔊';
}
$('btn-mute').addEventListener('click', e => { e.stopPropagation(); AudioSys.init(); AudioSys.uiClick(); toggleMute(); });
$('btn-pause').addEventListener('click', e => { e.stopPropagation(); AudioSys.uiClick(); togglePause(); });
$('btn-start').addEventListener('click', () => { AudioSys.init(); AudioSys.uiClick(); startGame(); });
$('btn-how').addEventListener('click', () => { AudioSys.init(); AudioSys.uiClick(); state = 'howto'; showOverlay(overlays.howto); });
$('btn-how-back').addEventListener('click', () => { AudioSys.uiClick(); state = 'menu'; showOverlay(overlays.menu); });
$('btn-resume').addEventListener('click', () => resumeGame());
$('btn-restart2').addEventListener('click', () => startGame());
$('btn-quit').addEventListener('click', () => { state = 'menu'; showOverlay(overlays.menu); hudEl.classList.add('hidden'); });
$('btn-retry').addEventListener('click', () => startGame());
$('btn-go-menu').addEventListener('click', () => { state = 'menu'; showOverlay(overlays.menu); });
$('btn-win-retry').addEventListener('click', () => startGame());
$('btn-win-menu').addEventListener('click', () => { state = 'menu'; showOverlay(overlays.menu); });

// How-to legends (built from levels.js config)
(function buildLegends() {
  const bl = $('brick-legend');
  const bcols = { N: '#00c8ff', A: '#7b5cff', K: '#2dffc4', V: '#ff6a00', F: '#e8ecff', H: '#ffe14d', X: '#ff2fd6' };
  const bicons = { N: '■', A: '▲', K: '≋', V: '🔥', F: '❄', H: '⇔', X: '✸' };
  for (const k of Object.keys(window.BRICK_STYLE)) {
    const s = window.BRICK_STYLE[k];
    const row = document.createElement('div'); row.className = 'leg-row';
    row.innerHTML = `<span class="sw" style="background:${bcols[k]}">${bicons[k]}</span><span><b>${s.name}</b> — ${s.hint}</span>`;
    bl.appendChild(row);
  }
  const pl = $('pu-legend');
  for (const k of Object.keys(window.POWERUPS)) {
    const p = window.POWERUPS[k];
    const row = document.createElement('div'); row.className = 'leg-row';
    row.innerHTML = `<span class="sw" style="background:${p.color}">${p.icon}</span><span><b>${p.name}</b> — ${p.desc}</span>`;
    pl.appendChild(row);
  }
})();

/* ---------------- levels / bricks ---------------- */
function buildLevel(idx) {
  const L = window.LEVELS[idx];
  bricks = []; bricksLeft = 0;
  const cols = 13, rows = L.map.length;
  const marginX = 36, topY = TOP_SAFE + 20;
  const gap = 6;
  const bw = (W - marginX * 2 - gap * (cols - 1)) / cols;
  const bh = 34;
  for (let r = 0; r < rows; r++) {
    const line = L.map[r];
    for (let c = 0; c < cols; c++) {
      const ch = line[c] || '.';
      if (ch === '.') continue;
      const x = marginX + c * (bw + gap), y = topY + r * (bh + gap);
      bricks.push({ x, y, w: bw, h: bh, type: ch, alive: true,
        phase: Math.random() * TAU, wob: 0, flash: 0, baseX: x, seed: rand(0, 100) });
      bricksLeft++;
    }
  }
  bricksBrokenThisLevel = 0; combo = 0; baseMult = 1;
  ottActive = false; ottBonus = 0;
}
function lowestBrickTop() {
  let m = -Infinity;
  for (const b of bricks) if (b.alive) m = Math.max(m, b.y);
  return m;
}

/* ---------------- balls ---------------- */
function spawnBall(x, y, angle, speed) {
  speed = speed || BASE_BALL_SPEED;
  if (fx.slow) speed *= 0.7;
  balls.push({ x, y, vx: Math.sin(angle) * speed, vy: -Math.cos(angle) * speed,
    r: fx.big ? 15 : 9, trail: [], stuck: false, fire: 0, id: Math.random() });
}
function ballSpeed(b) { return Math.hypot(b.vx, b.vy); }
function setBallSpeed(b, s) {
  s = clamp(s, MIN_BALL_SPEED, MAX_BALL_SPEED);
  const cur = ballSpeed(b) || 1;
  b.vx *= s / cur; b.vy *= s / cur;
}
function resetServe() {
  balls = []; powerups = []; lasers = [];
  serveBall = { x: paddle.x, y: paddle.y - 24, vx: 0, vy: 0, r: fx.big ? 15 : 9,
    trail: [], stuck: true, fire: 0, id: Math.random() };
  balls.push(serveBall);
  countdownT = 0;
  state = 'serving';
}
function launchBall() {
  if (state !== 'serving') return;
  AudioSys.launch();
  for (const b of balls) if (b.stuck) {
    b.stuck = false;
    const a = rand(-0.45, 0.45);
    const sp = BASE_BALL_SPEED * (fx.slow ? 0.7 : 1);
    b.vx = Math.sin(a) * sp; b.vy = -Math.cos(a) * sp;
  }
  serveBall = null;
  state = 'playing';
  toast('SMASH UP into the ball!', 1200);
}

/* ---------------- power-ups ---------------- */
function maybeDrop(x, y, guaranteed) {
  const roll = Math.random();
  if (!guaranteed && roll > 0.22) return;
  const pool = guaranteed
    ? ['multi', 'fire', 'phase', 'big', 'slow', 'wide', 'shield', 'laser', 'multi', 'fire', 'wide']
    : ['multi', 'fire', 'phase', 'big', 'slow', 'wide', 'shield', 'laser', 'laser'];
  let type = pool[(Math.random() * pool.length) | 0];
  if (Math.random() < 0.03) type = 'life';
  const p = window.POWERUPS[type];
  powerups.push({ x, y, vy: 200, type, w: 56, h: 24, color: p.color, icon: p.icon, spin: 0 });
}
function applyPowerup(type) {
  AudioSys.powerup();
  const p = window.POWERUPS[type];
  burst(paddle.x, paddle.y, p.color, 26, 320);
  addRing(paddle.x, paddle.y, p.color, 70);
  if (type === 'life') {
    lives++;
    domFloat(paddle.x, paddle.y - 50, '+1 LIFE ♥', '#ff4d6d');
    updateHUD(); return;
  }
  if (type === 'multi') {
    const src = balls.filter(b => !b.stuck);
    if (!src.length) spawnBall(paddle.x, paddle.y - 30, rand(-0.5, 0.5));
    const news = [];
    for (const b of src) {
      const sp = ballSpeed(b);
      const base = Math.atan2(b.vx, -b.vy);
      for (const off of [-0.5, 0.5]) {
        const nb = { x: b.x, y: b.y, vx: 0, vy: 0, r: b.r, trail: [], stuck: false, fire: b.fire, id: Math.random() };
        nb.vx = Math.sin(base + off) * sp; nb.vy = -Math.cos(base + off) * sp;
        news.push(nb);
      }
    }
    balls = balls.concat(news);
    domFloat(paddle.x, paddle.y - 50, 'MULTIBALL ✚', '#00f0ff');
    return;
  }
  fx[type] = FX_DUR[type] || 10;
  if (type === 'slow') for (const b of balls) setBallSpeed(b, ballSpeed(b) * 0.65);
  if (type === 'big') for (const b of balls) b.r = 15;
  domFloat(paddle.x, paddle.y - 50, p.name + ' ' + p.icon, p.color);
  updateFXHUD();
}
function fireLasers() {
  if (paddle.cool > 0 || state !== 'playing') return;
  paddle.cool = 0.22;
  AudioSys.laser();
  const w = paddleW();
  lasers.push({ x: paddle.x - w * 0.3, y: paddle.y - 20, vy: -1100, dead: false });
  lasers.push({ x: paddle.x + w * 0.3, y: paddle.y - 20, vy: -1100, dead: false });
  paddle.squash = -0.25;
}
function laserHitsBrick(b) {
  if (b.type === 'N' || b.type === 'H' || b.type === 'X') return true;
  if (b.type === 'A') return true;
  if (b.type === 'K') return false;
  if (b.type === 'V') return true;
  if (b.type === 'F') return false;
  return true;
}

/* ---------------- particles ---------------- */
function burst(x, y, color, n, spd) {
  for (let i = 0; i < n; i++) {
    if (particles.length > 700) break;
    const a = Math.random() * TAU, s = rand(spd * 0.3, spd);
    particles.push({ x, y, vx: Math.cos(a) * s, vy: Math.sin(a) * s - 60,
      life: rand(0.35, 0.9), t: 0, color, size: rand(2, 5.5) });
  }
}
function addRing(x, y, color, maxR) { rings.push({ x, y, r: 6, maxR: maxR || 60, t: 0, life: 0.45, color }); }
function shardBurst(b) {
  const st = window.BRICK_STYLE[b.type];
  for (let i = 0; i < 16; i++) {
    if (particles.length > 700) break;
    particles.push({ x: b.x + Math.random() * b.w, y: b.y + Math.random() * b.h,
      vx: rand(-260, 260), vy: rand(-320, 60), life: rand(0.4, 1), t: 0,
      color: i % 3 === 0 ? '#ffffff' : st.color, size: rand(2.5, 6),
      shard: true, rot: rand(0, TAU), vr: rand(-9, 9) });
  }
}
function confetti() {
  const cols = ['#00f0ff', '#ff2fd6', '#ffd23f', '#39ff88', '#ffffff'];
  for (let i = 0; i < 160; i++) {
    particles.push({ x: rand(0, W), y: rand(-40, H * 0.3), vx: rand(-120, 120), vy: rand(120, 420),
      life: rand(1, 2.2), t: 0, color: cols[(Math.random() * cols.length) | 0],
      size: rand(3, 6), shard: true, rot: rand(0, TAU), vr: rand(-9, 9) });
  }
}
function addShake(n) { shake = Math.min(22, shake + n); }
function addHitStop(t) { hitStop = Math.max(hitStop, t); }
function scorePopup(x, y, pts, color) { floaters.push({ x, y, text: '+' + pts, t: 0, life: 0.9, color: color || '#fff' }); }
function hintPopup(x, y, text, color) { floaters.push({ x, y, text, t: 0, life: 1.1, color: color || '#ffb03a', big: true }); }

/* ---------------- brick hit rules ---------------- */
function effMult() { return baseMult + (ottActive ? ottBonus : 0); }
function checkBrickRule(b, vx, vy) {
  const sp = Math.hypot(vx, vy) || 1;
  const ax = Math.abs(vx) / sp; // 0 = vertical, 1 = horizontal
  if (b.type === 'N' || b.type === 'H') return { ok: true };
  if (b.type === 'X') return { ok: true };
  if (b.type === 'A') {
    if (ax <= STEEP_MAX_AX) return { ok: true };
    return { ok: false, hint: 'TOO SHALLOW! ▲ steep only' };
  }
  if (b.type === 'K') {
    if (ax >= SHALLOW_MIN_AX) return { ok: true };
    return { ok: false, hint: 'TOO STEEP! ≋ graze it' };
  }
  if (b.type === 'V') {
    if (sp >= V_FAST_NEED) return { ok: true };
    return { ok: false, hint: 'NEED SPEED! 🔥' };
  }
  if (b.type === 'F') {
    if (sp <= F_SLOW_NEED) return { ok: true };
    return { ok: false, hint: 'TOO FAST! ❄ slow down' };
  }
  return { ok: true };
}
function destroyBrick(b, byBall, silent) {
  if (!b.alive) return;
  b.alive = false; bricksLeft--; bricksBrokenThisLevel++;
  combo++; comboTimer = 3.2; maxCombo = Math.max(maxCombo, combo);
  baseMult = Math.min(9.5, 1 + combo * 0.12);
  const m = effMult();
  const pts = Math.round(50 * m);
  score += pts;
  scorePopup(b.x + b.w / 2, b.y, pts, ottActive ? '#ffd23f' : '#ffffff');
  shardBurst(b);
  burst(b.x + b.w / 2, b.y + b.h / 2, window.BRICK_STYLE[b.type].glow, 8, 260);
  addRing(b.x + b.w / 2, b.y + b.h / 2, window.BRICK_STYLE[b.type].glow, 46);
  if (!silent) AudioSys.brick(combo, m);
  if (combo >= 4 && combo !== announcedCombo) {
    announcedCombo = combo;
    comboPop.textContent = 'x' + combo + ' COMBO!';
    comboPop.classList.remove('hidden', 'show'); void comboPop.offsetWidth;
    comboPop.classList.add('show');
    clearTimeout(comboPop._t);
    comboPop._t = setTimeout(() => comboPop.classList.add('hidden'), 750);
  }
  multEl.classList.remove('pulse'); void multEl.offsetWidth; multEl.classList.add('pulse');
  if (b.type === 'X') explodeAt(b.x + b.w / 2, b.y + b.h / 2, b);
  else maybeDrop(b.x + b.w / 2, b.y + b.h / 2, false);
  bgPulse = Math.min(1.5, bgPulse + 0.25);
  addShake(b.type === 'X' ? 10 : 3);
  if (b.type === 'X') addHitStop(0.09);
  updateHUD();
}
function explodeAt(x, y, src) {
  AudioSys.explosion();
  addShake(12); addHitStop(0.09);
  burst(x, y, '#ff2fd6', 40, 520);
  burst(x, y, '#ffd23f', 24, 420);
  addRing(x, y, '#ff2fd6', 150); addRing(x, y, '#ffd23f', 100);
  domFloat(x, y - 30, 'BOOM! ✸', '#ff2fd6');
  const R = 130;
  const victims = [];
  for (const b of bricks) {
    if (!b.alive || b === src) continue;
    const cx = clamp(x, b.x, b.x + b.w), cy = clamp(y, b.y, b.y + b.h);
    if (Math.hypot(x - cx, y - cy) < R) victims.push(b);
  }
  // destroy neighbors (chain: volatiles recurse)
  for (const v of victims) {
    if (!v.alive) continue;
    v.alive = false; bricksLeft--; bricksBrokenThisLevel++;
    combo++; comboTimer = 3.2;
    baseMult = Math.min(9.5, 1 + combo * 0.12);
    const pts = Math.round(50 * effMult());
    score += pts;
    scorePopup(v.x + v.w / 2, v.y, pts, '#ff7ae8');
    shardBurst(v);
    maybeDrop(v.x + v.w / 2, v.y + v.h / 2, v.type === 'X');
    if (v.type === 'X') explodeAt(v.x + v.w / 2, v.y + v.h / 2, v); // chain reaction
  }
  maybeDrop(x, y, true); // volatile always gifts something
  updateHUD();
}
function rejectBrick(b, hint, ball) {
  b.flash = 1; b.wob = 1;
  AudioSys.clank();
  hintPopup(b.x + b.w / 2, b.y - 6, hint, '#ffb03a');
  burst(ball.x, ball.y, '#8899aa', 5, 160);
  updateHUD();
}

/* ---------------- update ---------------- */
function updatePaddle(dt) {
  paddle.px = paddle.x; paddle.py = paddle.y;
  const SPD = 950;
  let ix = 0, iy = 0;
  if (keys['arrowleft'] || keys['a']) ix -= 1;
  if (keys['arrowright'] || keys['d']) ix += 1;
  if (keys['arrowup'] || keys['w']) iy -= 1;
  if (keys['arrowdown'] || keys['s']) iy += 1;
  if (joyActive) { ix += clamp(joyDX / 60, -1, 1); iy += clamp(joyDY / 60, -1, 1); }
  const keyDriven = ix !== 0 || iy !== 0;
  if (keyDriven || joyActive) {
    paddle.hasMouse = false;
    paddle.targetX = clamp(paddle.x + ix * SPD * dt, 0, W);
    paddle.targetY = clamp(paddle.y + iy * SPD * dt, PADDLE_Y_MIN, PADDLE_Y_MAX);
  } else if (paddle.hasMouse && pointerOnScreen && !dragPaddle && !joyActive) {
    // mouse: target already set by mousemove
  }
  // critically-damped follow toward target (fast + smooth)
  const k = 1 - Math.exp(-dt * 18);
  const nx = lerp(paddle.x, clamp(paddle.targetX, 0, W), k);
  const ny = lerp(paddle.y, clamp(paddle.targetY, PADDLE_Y_MIN, PADDLE_Y_MAX), k);
  paddle.vx = dt > 0 ? (nx - paddle.x) / dt : 0;
  paddle.vy = dt > 0 ? (ny - paddle.y) / dt : 0;
  paddle.x = nx; paddle.y = ny;
  // clamp inside walls honoring width
  const hw = paddleW() / 2;
  paddle.x = clamp(paddle.x, hw + 4, W - hw - 4);
  paddle.targetX = clamp(paddle.targetX, hw + 4, W - hw - 4);
  paddle.tilt = lerp(paddle.tilt, clamp(paddle.vx / 1400, -0.4, 0.4), 1 - Math.exp(-dt * 10));
  paddle.squash = lerp(paddle.squash, 0, 1 - Math.exp(-dt * 8));
  paddle.cool = Math.max(0, paddle.cool - dt);
  // engine trail when fast
  const spd = Math.hypot(paddle.vx, paddle.vy);
  if (spd > 700 && particles.length < 650) {
    particles.push({ x: paddle.x + rand(-hw * 0.7, hw * 0.7), y: paddle.y + 12,
      vx: -paddle.vx * 0.08 + rand(-40, 40), vy: rand(60, 200),
      life: rand(0.2, 0.45), t: 0, color: '#00f0ff', size: rand(1.5, 3.5) });
  }
  if (serveBall) { serveBall.x = paddle.x; serveBall.y = paddle.y - 24; }
}

function collideCircleBrick(b, bx, by, br) {
  const cx = clamp(bx, b.x, b.x + b.w), cy = clamp(by, b.y, b.y + b.h);
  const dx = bx - cx, dy = by - cy;
  return (dx * dx + dy * dy) < br * br ? { nx: cx, ny: cy, dx, dy } : null;
}
function resolveBallBrick(ball, b) {
  // returns 'broken' | 'rejected' | null
  const hit = collideCircleBrick(b, ball.x, ball.y, ball.r);
  if (!hit) return null;
  let nx, ny;
  if (hit.dx === 0 && hit.dy === 0) {
    // center inside brick: push out along smallest penetration
    const l = ball.x - b.x, r = b.x + b.w - ball.x, t = ball.y - b.y, bo = b.y + b.h - ball.y;
    const m = Math.min(l, r, t, bo);
    if (m === l) { nx = -1; ny = 0; } else if (m === r) { nx = 1; ny = 0; }
    else if (m === t) { nx = 0; ny = -1; } else { nx = 0; ny = 1; }
  } else {
    const d = Math.hypot(hit.dx, hit.dy) || 1;
    nx = hit.dx / d; ny = hit.dy / d;
  }
  // fireball: pierce without bouncing
  if (fx.fire) {
    destroyBrick(b, true);
    return 'broken';
  }
  // phase/ghost: pass through, damage without bouncing
  if (fx.phase) {
    const rule = checkBrickRule(b, ball.vx, ball.vy);
    if (rule.ok) { destroyBrick(b, true); burst(ball.x, ball.y, '#b388ff', 8, 200); }
    return 'broken'; // no bounce either way (ghost glide)
  }
  const rule = checkBrickRule(b, ball.vx, ball.vy);
  if (!rule.ok) {
    // bounce off + hint, small push-out
    const dot = ball.vx * nx + ball.vy * ny;
    if (dot < 0) { ball.vx -= 2 * dot * nx; ball.vy -= 2 * dot * ny; }
    ball.x += nx * 2; ball.y += ny * 2;
    rejectBrick(b, rule.hint, ball);
    return 'rejected';
  }
  // break it, then bounce off the surface
  destroyBrick(b, true);
  const dot = ball.vx * nx + ball.vy * ny;
  if (dot < 0) { ball.vx -= 2 * dot * nx; ball.vy -= 2 * dot * ny; }
  ball.x += nx * 2; ball.y += ny * 2;
  // keep some pace after breaks
  const sp = ballSpeed(ball);
  if (sp < MIN_BALL_SPEED) setBallSpeed(ball, MIN_BALL_SPEED + 60);
  return 'broken';
}

function updateBalls(dt) {
  const hadShield = !!fx.shield;
  for (const ball of balls) {
    if (ball.stuck) continue;
    if (fx.big && ball.r < 15) ball.r = 15;
    if (!fx.big && ball.r > 10) ball.r = 9;
    // gentle cruise: bleed off smash heat toward 640 so Feather bricks
    // stay breakable late in a rally (no friction deadlock), while fresh
    // launches (620) and cruise (640) still satisfy every rule window.
    const spCruise = ballSpeed(ball);
    if (spCruise > 640 && !fx.slow) setBallSpeed(ball, lerp(spCruise, 640, 1 - Math.exp(-dt * 0.6)));
    // trail
    ball.trail.push({ x: ball.x, y: ball.y });
    if (ball.trail.length > 22) ball.trail.shift();
    // fixed-substep to avoid tunneling: max step = r/2
    const sp = ballSpeed(ball);
    const steps = clamp(Math.ceil(sp * dt / (ball.r * 0.5)), 1, 12);
    const sdt = dt / steps;
    for (let s = 0; s < steps; s++) {
      ball.x += ball.vx * sdt; ball.y += ball.vy * sdt;
      // walls
      if (ball.x < ball.r + 4) { ball.x = ball.r + 4; ball.vx = Math.abs(ball.vx); AudioSys.wall(); burst(ball.x, ball.y, '#00f0ff', 4, 140); }
      if (ball.x > W - ball.r - 4) { ball.x = W - ball.r - 4; ball.vx = -Math.abs(ball.vx); AudioSys.wall(); burst(ball.x, ball.y, '#00f0ff', 4, 140); }
      if (ball.y < ball.r + TOP_SAFE * 0.4) { ball.y = ball.r + TOP_SAFE * 0.4; ball.vy = Math.abs(ball.vy); AudioSys.wall(); burst(ball.x, ball.y, '#00f0ff', 4, 140); }
      // paddle collision (only when falling onto paddle zone)
      const pw = paddleW(), ph = paddle.h;
      if (ball.vy > 0 &&
          ball.y + ball.r >= paddle.y - ph / 2 && ball.y - ball.r <= paddle.y + ph / 2 &&
          Math.abs(ball.x - paddle.x) <= pw / 2 + ball.r) {
        const hitPos = clamp((ball.x - paddle.x) / (pw / 2), -1, 1);
        // base rebound angle: -60..60 deg from vertical
        let ang = hitPos * 1.05; // radians
        let speed = ballSpeed(ball);
        // SMASH: upward paddle velocity adds energy
        const upV = -paddle.vy; // positive when moving up
        let smashed = false;
        if (upV > 140) {
          const e = clamp(upV / 1100, 0, 1);
          speed = clamp(speed * (1 + 0.38 * e) + 90 * e, speed, MAX_BALL_SPEED);
          ang += rand(-0.06, 0.06);
          smashed = true;
          paddle.squash = 0.55;
          addShake(4 + 6 * e); bgPulse = Math.min(1.5, bgPulse + 0.3);
          domFloat(ball.x, ball.y - 26, 'SMASH! ⚡', '#ff2fd6');
          score += Math.round(25 * effMult());
          AudioSys.smash();
          burst(ball.x, ball.y, '#ff2fd6', 18, 380);
          addRing(ball.x, ball.y, '#ff2fd6', 60);
        } else {
          paddle.squash = Math.max(paddle.squash, 0.3);
        }
        // gentle speed floor growth so rallies accelerate slightly
        speed = clamp(speed + 4, MIN_BALL_SPEED, MAX_BALL_SPEED);
        ball.vx = Math.sin(ang) * speed;
        ball.vy = -Math.cos(ang) * speed;
        // nudge: avoid near-horizontal lock
        if (Math.abs(ball.vy) < speed * 0.22) {
          ball.vy = (ball.vy < 0 ? -1 : 1) * speed * 0.25;
          const sx = Math.sqrt(Math.max(0, speed * speed - ball.vy * ball.vy));
          ball.vx = (ball.vx < 0 ? -1 : 1) * sx;
        }
        ball.y = paddle.y - ph / 2 - ball.r - 1;
        AudioSys.paddle(hitPos);
        combo = Math.max(combo, 1);
        if (!smashed) burst(ball.x, ball.y, '#7df9ff', 6, 180);
        updateHUD();
      }
      // brick collisions (one per substep max, then continue)
      for (const br of bricks) {
        if (!br.alive) continue;
        const res = resolveBallBrick(ball, br);
        if (res === 'broken') { addHitStop(0.02); break; }
        if (res === 'rejected') break;
      }
      // OTT sparkle emission while above line
      if (ottActive && Math.random() < 0.3 && particles.length < 650) {
        particles.push({ x: ball.x, y: ball.y, vx: rand(-160, 160), vy: rand(-220, 40),
          life: rand(0.3, 0.7), t: 0, color: '#ffd23f', size: rand(2, 4) });
      }
    }
    // anti-stall: nudge horizontal loops
    const spd2 = ballSpeed(ball);
    if (Math.abs(ball.vy) < spd2 * 0.16 && !ball.stuck) {
      ball.vy += (ball.vy >= 0 ? 1 : -1) * spd2 * 0.35 * dt * 4;
      setBallSpeed(ball, clamp(spd2, MIN_BALL_SPEED, MAX_BALL_SPEED));
    }
  }
  // remove balls below screen (shield net saves one)
  const toRemove = [];
  for (const ball of balls) {
    if (ball.stuck) continue;
    if (ball.y - ball.r > H + 20) toRemove.push(ball);
  }
  for (const r of toRemove) {
    const i = balls.indexOf(r);
    if (fx.shield && balls.length >= 1) {
      // bounce it back as the one save, then consume shield
      balls.splice(i, 1);
      spawnBall(clamp(r.x, 60, W - 60), H - 220, rand(-0.4, 0.4), Math.max(ballSpeed(r), BASE_BALL_SPEED));
      const nb = balls[balls.length - 1]; nb.vy = -Math.abs(nb.vy);
      delete fx.shield;
      domFloat(paddle.x, H - 260, 'SHIELDED! ⛨', '#ffd23f');
      AudioSys.banked(); addRing(paddle.x, H - 60, '#ffd23f', 120);
      updateFXHUD();
      break; // only one save per frame burst
    } else {
      balls.splice(i, 1);
    }
  }
  if (!balls.length && (state === 'playing' || state === 'serving')) loseLife();
  void hadShield;
}

function loseLife() {
  lives--;
  combo = 0; baseMult = 1; announcedCombo = 0;
  ottActive = false; ottBonus = 0;
  AudioSys.riserStop();
  ottBanner.classList.add('hidden');
  updateHUD();
  if (lives < 0) lives = 0;
  if (lives <= 0) {
    gameOver();
    return;
  }
  AudioSys.loseLife();
  addShake(10);
  toast('BALL LOST — ' + lives + (lives === 1 ? ' LIFE' : ' LIVES') + ' LEFT', 1500);
  // reset effects on life loss (keep it fair, clear chaos)
  fx = {};
  updateFXHUD();
  resetServe();
}

/* ---------------- over-the-top fantasy ---------------- */
function updateOTT(dt) {
  const line = lowestBrickTop();
  let anyAbove = false;
  if (isFinite(line) && bricksLeft > 0) {
    for (const b of balls) {
      if (!b.stuck && b.y < line - 6) { anyAbove = true; break; }
    }
  }
  if (anyAbove && (state === 'playing')) {
    if (!ottActive) {
      ottActive = true; ottBonus = 0;
      ottBanner.classList.remove('hidden');
      AudioSys.riserStart();
      domFloat(W / 2, H * 0.42, '★ OVER THE TOP ★', '#ffd23f');
      addShake(5);
    }
    ottBonus = Math.min(OTT_CAP, ottBonus + OTT_TICK * dt);
    ottPeak = Math.max(ottPeak, ottBonus);
    ottBank += Math.round(120 * dt * (1 + ottBonus)); // trickle score while sky-high
    AudioSys.riserUpdate(ottBonus / OTT_CAP);
    bgPulse = Math.min(1.6, bgPulse + dt * 1.4);
    if (Math.random() < dt * 6) {
      const x = rand(40, W - 40);
      particles.push({ x, y: -10, vx: rand(-30, 30), vy: rand(200, 420), life: rand(0.6, 1.2),
        t: 0, color: '#ffd23f', size: rand(2, 4), beam: true });
    }
  } else if (ottActive) {
    // ball came back down (or no balls left) -> bank it
    ottActive = false;
    AudioSys.riserStop();
    ottBanner.classList.add('hidden');
    const bonus = ottBank + Math.round(ottBonus * 250);
    if (bonus > 0) {
      score += bonus;
      domFloat(W / 2, H * 0.45, 'BANKED! +' + bonus, '#ffd23f');
      AudioSys.banked();
      confettiLite();
      comboPop.textContent = 'BANKED! +' + bonus;
      comboPop.classList.remove('hidden', 'show'); void comboPop.offsetWidth;
      comboPop.classList.add('show');
      clearTimeout(comboPop._t);
      comboPop._t = setTimeout(() => comboPop.classList.add('hidden'), 900);
    }
    ottBank = 0; ottBonus = 0;
    updateHUD();
  }
  if (!ottActive && ottBank !== 0 && state !== 'playing') ottBank = 0;
}
function confettiLite() {
  const cols = ['#ffd23f', '#ff2fd6', '#00f0ff', '#ffffff'];
  for (let i = 0; i < 46; i++) {
    particles.push({ x: W / 2 + rand(-160, 160), y: H * 0.4, vx: rand(-320, 320), vy: rand(-420, -40),
      life: rand(0.6, 1.3), t: 0, color: cols[(Math.random() * cols.length) | 0], size: rand(2.5, 5) });
  }
}

function updateDrops(dt) {
  // powerup capsules
  for (const p of powerups) {
    p.y += p.vy * dt; p.spin += dt * 4;
    const pw = paddleW();
    if (p.y + p.h / 2 >= paddle.y - paddle.h / 2 - 6 && p.y - p.h / 2 <= paddle.y + paddle.h / 2 + 10 &&
        Math.abs(p.x - paddle.x) <= pw / 2 + p.w / 2) {
      p.dead = true;
      applyPowerup(p.type);
    }
    if (p.y > H + 30) p.dead = true;
  }
  powerups = powerups.filter(p => !p.dead);
  // lasers
  for (const l of lasers) {
    l.y += l.vy * dt;
    if (l.y < 40) { l.dead = true; continue; }
    for (const b of bricks) {
      if (!b.alive) continue;
      if (l.x >= b.x - 3 && l.x <= b.x + b.w + 3 && l.y >= b.y && l.y <= b.y + b.h) {
        l.dead = true;
        if (laserHitsBrick(b)) {
          destroyBrick(b, false);
          burst(l.x, l.y, '#ff2fd6', 10, 260);
          addHitStop(0.02);
        } else {
          b.flash = 1; b.wob = 1;
          hintPopup(b.x + b.w / 2, b.y - 6, b.type === 'K' ? 'GRAZE ONLY ≋' : 'WRONG BOLT ❄/≋', '#ffb03a');
          AudioSys.clank();
          burst(l.x, l.y, '#8899aa', 6, 180);
        }
        break;
      }
    }
  }
  lasers = lasers.filter(l => !l.dead);
  // shifter bricks slide
  for (const b of bricks) {
    if (!b.alive || b.type !== 'H') continue;
    b.x = clamp(b.baseX + Math.sin(elapsed * 1.6 + b.seed) * 46, 8, W - b.w - 8);
    b.flash = Math.max(0, b.flash - dt * 3);
    b.wob = Math.max(0, b.wob - dt * 3);
  }
  for (const b of bricks) {
    if (!b.alive || b.type === 'H') {
      b.flash = Math.max(0, (b.flash || 0) - dt * 3);
      b.wob = Math.max(0, (b.wob || 0) - dt * 3);
    }
  }
  // fx timers
  for (const k of Object.keys(fx)) {
    if (typeof fx[k] === 'number') {
      fx[k] -= dt;
      if (fx[k] <= 0) {
        if (k === 'slow') for (const ball of balls) setBallSpeed(ball, ballSpeed(ball) / 0.65);
        delete fx[k];
        updateFXHUD();
      }
    }
  }
  // combo decay
  if (comboTimer > 0) {
    comboTimer -= dt;
    if (comboTimer <= 0) { combo = 0; baseMult = 1; announcedCombo = 0; updateHUD(); }
  }
  // fx particles/rings/floaters
  for (const p of particles) {
    p.t += dt;
    p.x += p.vx * dt; p.y += p.vy * dt;
    p.vy += (p.beam ? -60 : 480) * dt;
    if (p.rot != null) p.rot += p.vr * dt;
  }
  particles = particles.filter(p => p.t < p.life);
  for (const r of rings) { r.t += dt; r.r = lerp(r.maxR, 6, Math.max(0, 1 - r.t / r.life)); }
  rings = rings.filter(r => r.t < r.life);
  for (const f of floaters) { f.t += dt; f.y -= 46 * dt; }
  floaters = floaters.filter(f => f.t < f.life);
  shake = Math.max(0, shake - dt * 46);
  bgPulse = Math.max(0, bgPulse - dt * 1.2);
  if (slowMo > 0) slowMo -= dt;
}

/* ---------------- flow / states ---------------- */
function startGame() {
  score = 0; lives = START_LIVES; levelIndex = 0;
  combo = 0; baseMult = 1; maxCombo = 0; ottPeak = 0;
  fx = {}; particles = []; rings = []; floaters = []; powerups = []; lasers = [];
  ottActive = false; ottBonus = 0; ottBank = 0;
  AudioSys.riserStop();
  paddle.x = W / 2; paddle.y = H - 140;
  paddle.targetX = W / 2; paddle.targetY = H - 140;
  showOverlay(null);
  hudEl.classList.remove('hidden');
  startLevel(0);
  updateHUD(); updateFXHUD();
}
function startLevel(idx) {
  levelIndex = idx;
  buildLevel(idx);
  particles = particles.filter(p => p.t < 0.2);
  powerups = []; lasers = [];
  fx = {};
  updateFXHUD();
  const L = window.LEVELS[idx];
  $('intro-kicker').textContent = 'LEVEL ' + (idx + 1) + ' / ' + window.LEVELS.length;
  $('intro-name').textContent = L.name;
  $('intro-sub').textContent = L.sub;
  const card = $('intro-card');
  card.classList.remove('hidden');
  clearTimeout(card._t);
  card._t = setTimeout(() => card.classList.add('hidden'), 2400);
  levelEl.textContent = 'LEVEL ' + (idx + 1);
  resetServe();
  introT = 0;
  updateHUD();
}
function togglePause() {
  if (state === 'playing' || state === 'serving') {
    pausedFrom = state; state = 'paused';
    showOverlay(overlays.pause);
    AudioSys.riserStop();
  } else if (state === 'paused') resumeGame();
}
function resumeGame() {
  if (state !== 'paused') return;
  AudioSys.uiClick();
  showOverlay(null);
  state = pausedFrom === 'serving' ? 'serving' : 'playing';
  if (ottActive) AudioSys.riserStart();
}
function levelCleared() {
  state = 'clear'; clearT = 0;
  slowMo = 1.6;
  AudioSys.levelClear();
  AudioSys.riserStop();
  ottBanner.classList.add('hidden');
  if (ottActive) { score += ottBank + Math.round(ottBonus * 250); ottBank = 0; }
  ottActive = false; ottBonus = 0;
  score += lives * 100 + Math.round(500 * effMult());
  confetti();
  addRing(W / 2, H / 2, '#ffd23f', 320);
  domFloat(W / 2, H * 0.45, 'SECTOR CLEAR!', '#39ff88');
  updateHUD();
}
function gameOver() {
  state = 'over';
  AudioSys.riserStop();
  AudioSys.loseLife();
  addShake(14);
  const isBest = score > hiScore;
  if (isBest) { hiScore = score; store('ott_hiscore', hiScore); }
  $('go-score').textContent = score;
  $('go-hi').textContent = hiScore;
  $('go-newbest').classList.toggle('hidden', !isBest);
  $('go-stats').textContent = 'reached level ' + (levelIndex + 1) + ' · best combo x' + maxCombo;
  showOverlay(overlays.over);
  hudEl.classList.add('hidden');
  updateHUD();
}
function victory() {
  state = 'win';
  AudioSys.riserStop();
  AudioSys.victory();
  confetti(); setTimeout(confetti, 600);
  const isBest = score > hiScore;
  if (isBest) { hiScore = score; store('ott_hiscore', hiScore); }
  $('win-score').textContent = score;
  $('win-hi').textContent = hiScore;
  $('win-newbest').classList.toggle('hidden', !isBest);
  $('win-stats').textContent = 'best combo x' + maxCombo + ' · peak OTT +' + ottPeak.toFixed(1) + 'x';
  showOverlay(overlays.win);
  hudEl.classList.add('hidden');
}

/* ---------------- HUD ---------------- */
function updateHUD() {
  scoreEl.textContent = score;
  hiEl.textContent = Math.max(hiScore, score);
  const m = effMult();
  multEl.textContent = 'x' + m.toFixed(1);
  multEl.style.color = m >= 6 ? '#ff2fd6' : (m >= 3 ? '#ffd23f' : '#ffd23f');
  multEl.style.fontSize = (24 + Math.min(14, m * 1.6)) + 'px';
  multFill.style.width = clamp(((m - 1) / 9) * 100, 0, 100) + '%';
  livesEl.innerHTML = '';
  for (let i = 0; i < Math.max(0, lives); i++) {
    const s = document.createElement('span'); s.textContent = '●'; livesEl.appendChild(s);
  }
  $('menu-hi').textContent = hiScore;
}
function updateFXHUD() {
  fxTimers.innerHTML = '';
  for (const k of Object.keys(fx)) {
    if (typeof fx[k] !== 'number') continue;
    const chip = document.createElement('div'); chip.className = 'fx-chip';
    const pct = clamp((fx[k] / (FX_DUR[k] || 10)) * 100, 0, 100);
    chip.innerHTML = `<span>${FX_ICON[k] || '★'} ${FX_NAME[k] || k}</span><span class="bar"><i style="width:${pct}%"></i></span>`;
    fxTimers.appendChild(chip);
  }
}
setInterval(() => { if (state === 'playing' || state === 'serving') updateFXHUD(); }, 250);

/* ---------------- render ---------------- */
function render() {
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  // letterbox: fit logical into css box
  const s = Math.min(cssW / W, cssH / H);
  const ox = (cssW - W * s) / 2, oy = (cssH - H * s) / 2;
  ctx.fillStyle = '#030309';
  ctx.fillRect(0, 0, cssW, cssH);
  ctx.translate(ox, oy); ctx.scale(s, s);
  if (shake > 0.2) ctx.translate(rand(-shake, shake) * 0.5, rand(-shake, shake) * 0.5);

  // background: deep gradient + hue shift when OTT
  bgHue = lerp(bgHue, ottActive ? 295 : 222, 0.04);
  const g = ctx.createLinearGradient(0, 0, 0, H);
  g.addColorStop(0, `hsl(${bgHue},60%,${ottActive ? 12 : 8}%)`);
  g.addColorStop(0.6, '#0a0d26');
  g.addColorStop(1, '#070714');
  ctx.fillStyle = g;
  ctx.fillRect(-30, -30, W + 60, H + 60);
  // starfield (twinkle, drifts faster with multiplier)
  const m = effMult();
  for (const st of stars) {
    st.tw += 0.05 + m * 0.008;
    const a = 0.25 + 0.55 * Math.abs(Math.sin(st.tw)) + Math.min(0.3, bgPulse * 0.3);
    ctx.fillStyle = `rgba(180,220,255,${a * st.z})`;
    const sz = st.z * 2.2 + (ottActive ? 0.8 : 0);
    ctx.fillRect(st.x, (st.y + elapsed * (8 + m * 3) * st.z) % H, sz, sz);
  }
  // perspective grid floor
  ctx.strokeStyle = ottActive ? 'rgba(255,47,214,0.28)' : 'rgba(0,240,255,0.13)';
  ctx.lineWidth = 1;
  const horizon = H * 0.62;
  for (let i = 0; i <= 12; i++) {
    const x = (i / 12) * W;
    ctx.beginPath(); ctx.moveTo(W / 2 + (x - W / 2) * 0.3, horizon); ctx.lineTo(x, H); ctx.stroke();
  }
  for (let i = 0; i < 8; i++) {
    const y = horizon + Math.pow(i / 8, 1.8) * (H - horizon) + (elapsed * 60 % 30);
    ctx.globalAlpha = 0.5;
    ctx.beginPath(); ctx.moveTo(0, y); ctx.lineTo(W, y); ctx.stroke();
    ctx.globalAlpha = 1;
  }
  // OTT sky-zone line + beams
  if (bricksLeft > 0) {
    const line = lowestBrickTop();
    if (isFinite(line)) {
      ctx.save();
      ctx.setLineDash([10, 8]);
      ctx.strokeStyle = ottActive ? 'rgba(255,210,63,0.8)' : 'rgba(255,210,63,0.28)';
      ctx.lineWidth = ottActive ? 2.5 : 1.5;
      ctx.beginPath(); ctx.moveTo(10, line - 4); ctx.lineTo(W - 10, line - 4); ctx.stroke();
      ctx.setLineDash([]);
      ctx.fillStyle = ottActive ? 'rgba(255,210,63,0.9)' : 'rgba(255,210,63,0.4)';
      ctx.font = '700 13px sans-serif';
      ctx.fillText(ottActive ? '★ SKY ZONE ★' : '▲ SKY ZONE', 16, line - 12);
      if (ottActive) {
        ctx.globalAlpha = 0.16 + 0.1 * Math.sin(elapsed * 8);
        ctx.fillStyle = '#ffd23f';
        ctx.fillRect(0, 0, W, line);
        ctx.globalAlpha = 1;
      }
      ctx.restore();
    }
  }
  // side walls glow
  ctx.fillStyle = 'rgba(0,240,255,0.5)';
  ctx.fillRect(0, 0, 4, H); ctx.fillRect(W - 4, 0, 4, H);
  ctx.fillStyle = 'rgba(0,240,255,0.15)';
  ctx.fillRect(0, 0, 10, H); ctx.fillRect(W - 10, 0, 10, H);

  drawBricks();
  drawPowerups();
  drawLasers();
  drawPaddle();
  drawBalls();
  drawShield();
  drawParticles();
  drawFloaters();

  // serving hint
  if (state === 'serving') {
    ctx.save();
    ctx.globalAlpha = 0.75 + 0.25 * Math.sin(elapsed * 5);
    ctx.fillStyle = '#fff'; ctx.font = '800 22px sans-serif'; ctx.textAlign = 'center';
    ctx.shadowColor = '#00f0ff'; ctx.shadowBlur = 16;
    ctx.fillText('CLICK / TAP / SPACE TO LAUNCH', W / 2, H - 300);
    ctx.restore();
  }
  // screen pulse vignette when OTT
  if (ottActive || bgPulse > 0.05) {
    ctx.save();
    ctx.globalAlpha = clamp(bgPulse * 0.25 + (ottActive ? 0.12 : 0), 0, 0.4);
    const vg = ctx.createRadialGradient(W / 2, H / 2, H * 0.3, W / 2, H / 2, H * 0.75);
    vg.addColorStop(0, 'rgba(0,0,0,0)');
    vg.addColorStop(1, ottActive ? 'rgba(255,47,214,0.55)' : 'rgba(0,240,255,0.4)');
    ctx.fillStyle = vg;
    ctx.fillRect(0, 0, W, H);
    ctx.restore();
  }
}

const BCOL = { N: '#00c8ff', A: '#7b5cff', K: '#2dffc4', V: '#ff6a00', F: '#dfe6ff', H: '#ffe14d', X: '#ff2fd6' };
const BGLOW = { N: '#00f0ff', A: '#b388ff', K: '#2dffc4', V: '#ffb03a', F: '#ffffff', H: '#ffd23f', X: '#ff7ae8' };
const BICON = { N: '', A: '▲', K: '≋', V: '◆', F: '❄', H: '⇔', X: '✸' };
function drawBricks() {
  for (const b of bricks) {
    if (!b.alive) continue;
    const wob = b.wob > 0 ? Math.sin(elapsed * 40) * 3 * b.wob : 0;
    const bobY = b.type === 'H' ? Math.sin(elapsed * 3 + b.seed) * 2 : 0;
    const x = b.x + wob, y = b.y + bobY;
    ctx.save();
    ctx.shadowColor = BGLOW[b.type]; ctx.shadowBlur = 14 + (b.flash > 0 ? 22 : 0) + (ottActive ? 8 : 0);
    const grad = ctx.createLinearGradient(x, y, x, y + b.h);
    grad.addColorStop(0, b.flash > 0 ? '#ffffff' : BCOL[b.type]);
    grad.addColorStop(1, shade(BCOL[b.type], -45));
    ctx.fillStyle = grad;
    roundRect(x, y, b.w, b.h, 7); ctx.fill();
    ctx.shadowBlur = 0;
    // glass highlight
    ctx.fillStyle = 'rgba(255,255,255,0.28)';
    roundRect(x + 3, y + 3, b.w - 6, b.h * 0.34, 5); ctx.fill();
    // icon
    if (BICON[b.type]) {
      ctx.fillStyle = b.type === 'F' ? '#5a6aa8' : 'rgba(0,0,0,0.55)';
      ctx.font = `900 ${b.type === 'V' ? 15 : 17}px sans-serif`;
      ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
      if (b.type === 'V') {
        // flickering flame triangles
        const f = 0.6 + 0.4 * Math.sin(elapsed * 12 + b.seed);
        ctx.fillStyle = `rgba(120,20,0,${0.5 + 0.3 * f})`;
        ctx.font = '900 16px sans-serif';
      }
      ctx.fillText(BICON[b.type], x + b.w / 2, y + b.h / 2 + 1);
      ctx.textBaseline = 'alphabetic';
    }
    // feather brick: pale + drifting motes
    if (b.type === 'F') {
      ctx.fillStyle = 'rgba(255,255,255,0.5)';
      for (let i = 0; i < 3; i++) {
        const px = x + ((b.seed * 13 + i * 23 + elapsed * 22) % b.w);
        ctx.fillRect(px, y + 6 + i * 8, 5, 1.6);
      }
    }
    // shifter arrows shimmer
    if (b.type === 'H') {
      ctx.fillStyle = 'rgba(0,0,0,0.25)';
      ctx.font = '900 11px sans-serif'; ctx.textAlign = 'center';
      ctx.fillText('‹ ›', x + b.w / 2, y + b.h - 6);
    }
    ctx.restore();
  }
}
function shade(hex, amt) {
  const n = parseInt(hex.slice(1), 16);
  let r = (n >> 16) + amt, g = ((n >> 8) & 255) + amt, b = (n & 255) + amt;
  r = clamp(r, 0, 255); g = clamp(g, 0, 255); b = clamp(b, 0, 255);
  return `rgb(${r},${g},${b})`;
}
function roundRect(x, y, w, h, r) {
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + w, y, x + w, y + h, r);
  ctx.arcTo(x + w, y + h, x, y + h, r);
  ctx.arcTo(x, y + h, x, y, r);
  ctx.arcTo(x, y, x + w, y, r);
  ctx.closePath();
}







setInterval(() => { if (state === 'playing' || state === 'serving') updateFXHUD(); }, 250);

function drawPaddle() {
  const w = paddleW(), h = paddle.h;
  ctx.save();
  ctx.translate(paddle.x, paddle.y);
  ctx.rotate(paddle.tilt);
  const sq = clamp(paddle.squash, -0.4, 0.7);
  ctx.scale(1 + sq * 0.35, 1 - sq * 0.35);
  ctx.shadowColor = fx.laser ? '#ff2fd6' : '#00f0ff';
  ctx.shadowBlur = 24;
  const g = ctx.createLinearGradient(0, -h / 2, 0, h / 2);
  g.addColorStop(0, '#eaffff');
  g.addColorStop(0.45, fx.laser ? '#ff7ae8' : '#00f0ff');
  g.addColorStop(1, fx.laser ? '#a3127f' : '#0055aa');
  ctx.fillStyle = g;
  roundRect(-w / 2, -h / 2, w, h, 10); ctx.fill();
  ctx.shadowBlur = 0;
  ctx.fillStyle = 'rgba(255,255,255,0.85)';
  roundRect(-w / 2 + 8, -3, w - 16, 6, 3); ctx.fill();
  if (fx.laser) {
    ctx.fillStyle = '#ff2fd6';
    ctx.fillRect(-w / 2 - 4, -6, 6, 12); ctx.fillRect(w / 2 - 2, -6, 6, 12);
  }
  if (paddle.vy < -260) {
    ctx.globalAlpha = clamp(-paddle.vy / 1200, 0, 0.8);
    ctx.fillStyle = '#ff2fd6';
    roundRect(-w / 2, -h / 2 - 8, w, 6, 3); ctx.fill();
    ctx.globalAlpha = 1;
  }
  ctx.restore();
  ctx.save();
  ctx.strokeStyle = 'rgba(0,240,255,0.10)';
  ctx.setLineDash([6, 10]);
  ctx.strokeRect(14, PADDLE_Y_MIN - 24, W - 28, (PADDLE_Y_MAX - PADDLE_Y_MIN) + 48);
  ctx.setLineDash([]);
  ctx.restore();
}
function drawBalls() {
  for (const b of balls) {
    for (let i = 0; i < b.trail.length; i++) {
      const t = b.trail[i];
      const f = i / b.trail.length;
      ctx.globalAlpha = f * 0.5;
      ctx.fillStyle = fx.fire ? 'rgb(255,106,0)' : (fx.phase ? 'rgb(179,136,255)' : 'rgb(0,240,255)');
      const s = b.r * 2 * f * 0.9;
      ctx.beginPath(); ctx.arc(t.x, t.y, Math.max(0.5, s / 2), 0, TAU); ctx.fill();
    }
    ctx.globalAlpha = 1;
    ctx.save();
    const fire = fx.fire;
    ctx.shadowColor = fire ? '#ff6a00' : (fx.phase ? '#b388ff' : '#7df9ff');
    ctx.shadowBlur = fire ? 30 : 20;
    const g = ctx.createRadialGradient(b.x - b.r * 0.35, b.y - b.r * 0.35, 1, b.x, b.y, b.r);
    if (fire) { g.addColorStop(0, '#fff'); g.addColorStop(0.4, '#ffd23f'); g.addColorStop(1, '#ff3d00'); }
    else if (fx.phase) { g.addColorStop(0, '#fff'); g.addColorStop(0.5, '#d0b3ff'); g.addColorStop(1, '#6a3df0'); }
    else { g.addColorStop(0, '#fff'); g.addColorStop(0.5, '#aef7ff'); g.addColorStop(1, '#0090ff'); }
    if (fx.phase) ctx.globalAlpha = 0.75;
    ctx.fillStyle = g;
    ctx.beginPath(); ctx.arc(b.x, b.y, b.r, 0, TAU); ctx.fill();
    ctx.globalAlpha = 1;
    ctx.shadowBlur = 0;
    ctx.fillStyle = 'rgba(255,255,255,0.9)';
    ctx.beginPath(); ctx.arc(b.x - b.r * 0.3, b.y - b.r * 0.35, b.r * 0.28, 0, TAU); ctx.fill();
    if (fire && Math.random() < 0.8 && particles.length < 650) {
      particles.push({ x: b.x + rand(-4, 4), y: b.y + rand(-2, 6), vx: rand(-90, 90),
        vy: rand(40, 220), life: rand(0.25, 0.55), t: 0,
        color: ['#ffd23f', '#ff6a00', '#ff3d00'][(Math.random() * 3) | 0], size: rand(2, 5) });
    }
    ctx.restore();
  }
}
function drawPowerups() {
  for (const p of powerups) {
    ctx.save();
    ctx.translate(p.x, p.y);
    ctx.shadowColor = p.color; ctx.shadowBlur = 16;
    ctx.fillStyle = 'rgba(5,8,20,0.92)';
    roundRect(-p.w / 2, -p.h / 2, p.w, p.h, 12); ctx.fill();
    ctx.strokeStyle = p.color; ctx.lineWidth = 2;
    roundRect(-p.w / 2, -p.h / 2, p.w, p.h, 12); ctx.stroke();
    ctx.shadowBlur = 0;
    ctx.fillStyle = p.color;
    ctx.font = '900 13px sans-serif'; ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    ctx.fillText(p.icon + ' ' + (window.POWERUPS[p.type] ? window.POWERUPS[p.type].name : ''), 0, 1);
    ctx.textBaseline = 'alphabetic';
    ctx.restore();
  }
}
function drawLasers() {
  for (const l of lasers) {
    ctx.save();
    ctx.shadowColor = '#ff2fd6'; ctx.shadowBlur = 14;
    ctx.fillStyle = '#ffd7f4';
    roundRect(l.x - 3, l.y - 16, 6, 26, 3); ctx.fill();
    ctx.fillStyle = '#ff2fd6';
    roundRect(l.x - 1.5, l.y - 16, 3, 26, 1.5); ctx.fill();
    ctx.restore();
  }
}
function drawShield() {
  if (!fx.shield) return;
  ctx.save();
  ctx.globalAlpha = 0.7 + 0.2 * Math.sin(elapsed * 6);
  ctx.strokeStyle = '#ffd23f'; ctx.lineWidth = 3;
  ctx.shadowColor = '#ffd23f'; ctx.shadowBlur = 18;
  ctx.beginPath(); ctx.moveTo(14, H - 34); ctx.lineTo(W - 14, H - 34); ctx.stroke();
  ctx.globalAlpha = 0.25;
  for (let x = 24; x < W - 20; x += 24) {
    ctx.beginPath(); ctx.moveTo(x, H - 34); ctx.lineTo(x + 8, H - 22); ctx.stroke();
  }
  ctx.restore();
}
function drawParticles() {
  for (const p of particles) {
    const f = 1 - p.t / p.life;
    ctx.globalAlpha = clamp(f, 0, 1);
    ctx.fillStyle = p.color;
    if (p.shard) {
      ctx.save(); ctx.translate(p.x, p.y); ctx.rotate(p.rot || 0);
      ctx.fillRect(-p.size / 2, -p.size / 2, p.size, p.size * 0.7);
      ctx.restore();
    } else {
      ctx.beginPath(); ctx.arc(p.x, p.y, Math.max(0.4, p.size * f), 0, TAU); ctx.fill();
    }
  }
  ctx.globalAlpha = 1;
  for (const r of rings) {
    const f = 1 - r.t / r.life;
    ctx.globalAlpha = f * 0.9;
    ctx.strokeStyle = r.color; ctx.lineWidth = 3 * f + 1;
    ctx.beginPath(); ctx.arc(r.x, r.y, r.r, 0, TAU); ctx.stroke();
  }
  ctx.globalAlpha = 1;
}
function drawFloaters() {
  ctx.save();
  ctx.textAlign = 'center';
  for (const f of floaters) {
    const a = 1 - f.t / f.life;
    ctx.globalAlpha = clamp(a, 0, 1);
    ctx.font = f.big ? '900 19px sans-serif' : '800 16px sans-serif';
    ctx.shadowColor = f.color; ctx.shadowBlur = 12;
    ctx.fillStyle = f.color;
    ctx.fillText(f.text, f.x, f.y);
  }
  ctx.restore();
  ctx.globalAlpha = 1;
}

/* ---------------- main loop ---------------- */
let lastT = performance.now();
function frame(now) {
  requestAnimationFrame(frame);
  let rawDt = Math.min(0.05, (now - lastT) / 1000);
  lastT = now;
  // hit-stop micro freeze
  if (hitStop > 0) { hitStop -= rawDt; render(); return; }
  // level-clear slow-mo sweep
  timeScale = slowMo > 0 ? 0.35 : 1;
  const dt = rawDt * timeScale;
  elapsed += dt;

  if (state === 'playing' || state === 'serving') {
    updatePaddle(dt);
    // stuck serve ball rides paddle
    for (const b of balls) if (b.stuck) { b.x = paddle.x; b.y = paddle.y - 24; b.trail.length = 0; }
    if (state === 'playing') {
      updateBalls(dt);
      updateOTT(dt);
      updateDrops(dt);
      // sweep: level clear when all breakables gone
      if (bricksLeft <= 0 && state === 'playing') levelCleared();
    } else {
      updateDrops(dt * 0.2);
      updateOTT(dt);
    }
    // refresh OTT banner sub text
    if (ottActive) $('ott-sub').textContent = 'x' + effMult().toFixed(1) + ' multiplier — bank it!';
    if ((state === 'playing' || state === 'serving') && ((elapsed * 4) | 0) !== updateHUD._f) {
      updateHUD._f = (elapsed * 4) | 0;
      score += 0; // no-op keep HUD fresh
      updateHUD();
    }
  } else if (state === 'clear') {
    clearT += rawDt;
    updatePaddle(rawDt);
    updateDrops(rawDt);
    for (const p of particles) { p.t += rawDt; p.x += p.vx * rawDt; p.y += p.vy * rawDt; p.vy += 480 * rawDt; }
    particles = particles.filter(p => p.t < p.life);
    for (const r of rings) { r.t += rawDt; r.r = lerp(r.maxR, 6, Math.max(0, 1 - r.t / r.life)); }
    rings = rings.filter(r => r.t < r.life);
    shake = Math.max(0, shake - rawDt * 46);
    bgPulse = Math.max(0, bgPulse - rawDt);
    if (clearT > 2.4) {
      if (levelIndex + 1 >= window.LEVELS.length) { score += 1000; victory(); }
      else startLevel(levelIndex + 1);
    }
  } else {
    // menu/idle: gentle ambient drift
    updateDrops(rawDt * 0.3);
    for (const p of particles) { p.t += rawDt; p.x += p.vx * rawDt; p.y += p.vy * rawDt; }
    particles = particles.filter(p => p.t < p.life);
  }
  render();
}

/* ---------------- init ---------------- */
function init() {
  resize();
  setTimeout(resize, 60);
  $('menu-hi').textContent = hiScore;
  updateHUD();
  // idle demo bricks behind menu
  buildLevel(0);
  state = 'menu';
  showOverlay(overlays.menu);
  hudEl.classList.add('hidden');
  // deep link: #play starts the game immediately (useful for kiosks/tests)
  if (location.hash === '#play') startGame();
  requestAnimationFrame(frame);
}
if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
else init();

})();
