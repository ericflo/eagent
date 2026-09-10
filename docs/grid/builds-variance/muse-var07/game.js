/* ROOFTOP RAMPAGE — neon Breakout. Vanilla JS, no deps. Classic script (file:// safe). */
'use strict';
/* ================= 1. UTILS ================= */
const TAU = Math.PI * 2;
const clamp = (v, a, b) => v < a ? a : v > b ? b : v;
const lerp = (a, b, t) => a + (b - a) * t;
const rand = (a, b) => a + Math.random() * (b - a);
const randi = (a, b) => Math.floor(rand(a, b + 1));
const choice = arr => arr[Math.floor(Math.random() * arr.length)];
function $(id) { return document.getElementById(id); }

/* ================= 2. AUDIO (all synthesized WebAudio) ================= */
const AudioSys = {
  ctx: null, master: null, musicGain: null, muted: false, started: false,
  bassTimer: 0, bassStep: 0,
  init() {
    if (this.ctx) return;
    try {
      const AC = window.AudioContext || window.webkitAudioContext;
      this.ctx = new AC();
      this.master = this.ctx.createGain(); this.master.gain.value = 0.5;
      this.master.connect(this.ctx.destination);
      this.musicGain = this.ctx.createGain(); this.musicGain.gain.value = 0.16;
      this.musicGain.connect(this.master);
      this.started = true;
    } catch (e) { /* no audio */ }
  },
  resume() { this.init(); if (this.ctx && this.ctx.state === 'suspended') this.ctx.resume(); },
  toggleMute() {
    this.muted = !this.muted;
    if (this.master) this.master.gain.value = this.muted ? 0 : 0.5;
    return this.muted;
  },
  // generic synth blip
  tone(freq, dur, type, vol, slideTo, delay) {
    if (!this.ctx || this.muted) return;
    type = type || 'square'; vol = vol == null ? 0.25 : vol; delay = delay || 0;
    const t0 = this.ctx.currentTime + delay;
    const o = this.ctx.createOscillator(), g = this.ctx.createGain();
    o.type = type; o.frequency.setValueAtTime(freq, t0);
    if (slideTo) o.frequency.exponentialRampToValueAtTime(Math.max(20, slideTo), t0 + dur);
    g.gain.setValueAtTime(vol, t0);
    g.gain.exponentialRampToValueAtTime(0.0001, t0 + dur);
    o.connect(g); g.connect(this.master);
    o.start(t0); o.stop(t0 + dur + 0.02);
  },
  noise(dur, vol, filterFreq, delay) {
    if (!this.ctx || this.muted) return;
    delay = delay || 0;
    const t0 = this.ctx.currentTime + delay;
    const len = Math.max(1, Math.floor(this.ctx.sampleRate * dur));
    const buf = this.ctx.createBuffer(1, len, this.ctx.sampleRate);
    const d = buf.getChannelData(0);
    for (let i = 0; i < len; i++) d[i] = (Math.random() * 2 - 1) * (1 - i / len);
    const src = this.ctx.createBufferSource(); src.buffer = buf;
    const f = this.ctx.createBiquadFilter(); f.type = 'lowpass'; f.frequency.value = filterFreq || 2000;
    const g = this.ctx.createGain(); g.gain.setValueAtTime(vol || 0.3, t0);
    g.gain.exponentialRampToValueAtTime(0.0001, t0 + dur);
    src.connect(f); f.connect(g); g.connect(this.master);
    src.start(t0);
  },
  paddle(smash, speedRatio) {
    const base = 300 + speedRatio * 300 + (smash ? 250 : 0);
    this.tone(base, 0.09, 'square', 0.22, base * 1.6);
    if (smash) { this.noise(0.18, 0.3, 3000); this.tone(base * 2, 0.16, 'sawtooth', 0.15, base * 3); }
  },
  wall() { this.tone(220, 0.06, 'square', 0.12, 330); },
  brick(type, mult) {
    const m = 1 + Math.log2(Math.max(1, mult)) * 0.12;
    if (type === 'V') { this.tone(900 * m, 0.12, 'sawtooth', 0.25, 200); this.noise(0.1, 0.25, 5000); }
    else if (type === 'A') { this.tone(520 * m, 0.1, 'triangle', 0.3, 1040 * m); }
    else if (type === 'S') { this.tone(700 * m, 0.14, 'sine', 0.3, 350); }
    else if (type === 'P') { this.tone(440 * m, 0.2, 'sine', 0.3, 880 * m); this.tone(660 * m, 0.2, 'sine', 0.2, 1320 * m, 0.03); }
    else if (type === 'M') { this.tone(600 * m, 0.09, 'square', 0.22, 900 * m); }
    else { this.tone(500 * m, 0.08, 'square', 0.22, 750 * m); }
  },
  fail() { this.tone(160, 0.3, 'sawtooth', 0.3, 60); },
  denied() { this.tone(140, 0.12, 'square', 0.2, 110); },
  power() { const n = [523, 659, 784, 1046]; for (let i = 0; i < n.length; i++) this.tone(n[i], 0.12, 'square', 0.18, null, i * 0.05); },
  launch() { this.tone(250, 0.18, 'sawtooth', 0.25, 700); },
  roofEnter() { const n = [392, 523, 659, 784, 1046]; for (let i = 0; i < n.length; i++) this.tone(n[i], 0.14, 'sawtooth', 0.2, null, i * 0.045); this.noise(0.3, 0.2, 6000); },
  roofExit() { this.tone(600, 0.25, 'sawtooth', 0.2, 200); },
  roofTick(mult) { const f = 500 * (1 + Math.log2(Math.max(1, mult)) * 0.15); this.tone(f, 0.05, 'sine', 0.08, f * 1.3); },
  levelClear() { const n = [523, 659, 784, 1046, 784, 1046, 1318]; for (let i = 0; i < n.length; i++) this.tone(n[i], 0.16, 'triangle', 0.25, null, i * 0.09); },
  lifeLost() { const n = [400, 300, 200]; for (let i = 0; i < n.length; i++) this.tone(n[i], 0.18, 'sawtooth', 0.22, n[i] * 0.8, i * 0.1); },
  win() { const n = [523, 659, 784, 1046, 1318, 1568, 2093]; for (let i = 0; i < n.length; i++) this.tone(n[i], 0.25, 'triangle', 0.25, null, i * 0.12); },
  gameOver() { const n = [400, 350, 300, 200, 120]; for (let i = 0; i < n.length; i++) this.tone(n[i], 0.3, 'sawtooth', 0.25, n[i] * 0.7, i * 0.16); },
  smash() { this.noise(0.25, 0.35, 4000); this.tone(150, 0.2, 'sine', 0.4, 50); },
  // adaptive bass pulse; rate/pitch rise with multiplier & rooftop
  bass(dt, mult, rooftop) {
    if (!this.ctx || this.muted) return;
    this.bassTimer -= dt;
    if (this.bassTimer <= 0) {
      const heat = clamp(Math.log2(Math.max(1, mult)) / 6, 0, 1);
      this.bassTimer = rooftop ? lerp(0.42, 0.16, heat) : 0.55;
      const scale = [55, 55, 65.4, 49, 58.3, 55, 73.4, 65.4];
      const f = scale[this.bassStep % scale.length] * (rooftop ? 2 : 1) * (1 + heat * 0.5);
      this.bassStep++;
      const t0 = this.ctx.currentTime;
      const o = this.ctx.createOscillator(), g = this.ctx.createGain();
      o.type = 'sawtooth'; o.frequency.value = f;
      g.gain.setValueAtTime(rooftop ? 0.5 : 0.28, t0);
      g.gain.exponentialRampToValueAtTime(0.0001, t0 + 0.3);
      o.connect(g); g.connect(this.musicGain);
      o.start(t0); o.stop(t0 + 0.32);
      // sparkle arp on rooftop
      if (rooftop && Math.random() < 0.5 + heat * 0.4) {
        const arp = [880, 1108, 1318, 1568][randi(0, 3)] * (1 + heat);
        this.tone(arp, 0.1, 'sine', 0.06);
      }
    }
  }
};

/* ================= 3. LEVELS (7 hand-designed barrier layouts) ================= */
// chars: . empty | G glass | V volt | A apex | S skim | P spectral | M shifter
const LEVELS = [
  { name: 'FIRST CONTACT', desc: 'One gap. Punch through, then live on the roof.',
    rows: ['GGGGGGGGGG', 'GGGGGGGGGG', 'GGGG..GGGG', 'GGGG..GGGG'] },
  { name: 'DOUBLE DOORS', desc: 'Two gaps. VOLT bricks need SPEED — flick up for smash.',
    rows: ['GGGVVVVGGG', 'GGGGGGGGGG', 'GGG....GGG', 'GGGGGGGGGG'] },
  { name: 'THE CROWN', desc: 'APEX bricks need STEEP head-on hits. Come down on them.',
    rows: ['GGAAAAAAGG', 'GGAAAAAAGG', 'GGA....AGG', 'GGGGGGGGGG'] },
  { name: 'GRAZE GARDEN', desc: 'SKIM bricks only break on grazing hits. Slice them sideways.',
    rows: ['GGSSSSSSGG', 'GGSSSSSSGG', 'GG...SS.GG', 'GGGGGGGGGG'] },
  { name: 'HAUNTED ROOF', desc: 'SPECTRAL bricks need EMBER or SPECTRE balls. Grab a capsule!',
    rows: ['GGPPPPPPGG', 'GVVPPPPVVG', 'GGG....GGG', 'GGAMSSMAGG'] },
  { name: 'SPEEDWAY', desc: 'SHIFTERs slide. VOLTs guard the gaps — stay FAST.',
    rows: ['MMVVVVVVMM', 'GGMMMMMMGG', 'GG...VV.GG', 'AAASSSSAAA'] },
  { name: 'THE FORTRESS', desc: 'Everything at once. Three gaps. Good luck, legend.',
    rows: ['MPVASVSAPM', 'VVAASSSVVV', 'GG...GG...', 'GGM....MGG', 'GGGGGGGGGG'] },
];

/* ================= 4. GAME STATE ================= */
const canvas = $('game');
const ctx = canvas.getContext('2d');
let DPR = 1, W = 800, H = 560;
// safe storage (file:// + privacy modes can throw on localStorage access)
const store = {
  get(k) { try { return window.localStorage.getItem(k); } catch (e) { return null; } },
  set(k, v) { try { window.localStorage.setItem(k, v); } catch (e) {} },
};

const G = {
  state: 'title', // title|intro|play|pause|over|win
  level: 0, score: 0, lives: 3,
  hi: parseInt(store.get('rooftopRampageHi') || '0', 10) || 0,
  mult: 1, rooftop: false, roofTime: 0, roofGrace: 0, roofTickT: 0,
  combo: 0, comboT: 0, bestCombo: 0,
  shake: 0, hitstop: 0, slowmo: 0, slowmoDur: 0,
  time: 0, flashA: 0, pulse: 0,
  balls: [], bricks: [], parts: [], texts: [], waves: [], caps: [], stars: [],
  roofY: 150, gridL: 0, gridT: 0, cellW: 0, cellH: 0,
  clearedLevels: JSON.parse(store.get('rooftopRampageDone') || '[]'),
  introT: 0, overT: 0, announced: {},
};
const FX = { ember: 0, spectre: 0, wide: 0, shield: 0 }; // active effect timers
const VOLT_SPEED = 560;      // volt breaks only above this
const APEX_COS = Math.cos(40 * Math.PI / 180); // within 40deg of normal
const BALL_MAX = 12, BALL_CAP = 1100;

/* ================= 5. INPUT (mouse + keyboard + touch, unified pointer) ================= */
const Input = {
  x: 400, y: 500, active: false, hasPointer: false,
  keys: {}, lastDX: 0,
  dragId: null, dragDX: 0, dragDY: 0, downX: 0, downY: 0,
};
function canvasPos(e) {
  const r = canvas.getBoundingClientRect();
  return { x: (e.clientX - r.left), y: (e.clientY - r.top) };
}
canvas.addEventListener('pointerdown', e => {
  AudioSys.resume();
  try { canvas.setPointerCapture && canvas.setPointerCapture(e.pointerId); } catch (err) {}
  const p = canvasPos(e);
  Input.hasPointer = true; Input.active = true;
  Input.downX = p.x; Input.downY = p.y;
  // paddle target = pointer, but offset finger up so paddle isn't covered on touch
  const off = (e.pointerType === 'touch') ? -70 : 0;
  Input.x = p.x; Input.y = p.y + off;
  if (G.state === 'play') tryLaunch();
  e.preventDefault();
}, { passive: false });
canvas.addEventListener('pointermove', e => {
  if (!Input.active && e.pointerType === 'mouse') {
    const p = canvasPos(e); Input.x = p.x; Input.y = p.y; Input.hasPointer = true; return;
  }
  if (!Input.active) return;
  const p = canvasPos(e);
  const off = (e.pointerType === 'touch') ? -70 : 0;
  // 1:1 drag with slight sensitivity boost for touch
  const boost = (e.pointerType === 'touch') ? 1.25 : 1.0;
  const tx = p.x, ty = p.y + off;
  Input.x = Input.x + (tx - Input.x) * boost;
  Input.y = Input.y + (ty - Input.y) * boost;
  const r = canvas.getBoundingClientRect();
  Input.x = clamp(Input.x, 0, r.width); Input.y = clamp(Input.y, 0, r.height);
  e.preventDefault();
}, { passive: false });
const endPointer = () => { Input.active = false; };
canvas.addEventListener('pointerup', endPointer);
canvas.addEventListener('pointercancel', endPointer);
window.addEventListener('keydown', e => {
  if (['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', ' '].includes(e.key)) e.preventDefault();
  AudioSys.resume();
  Input.keys[e.key.toLowerCase()] = true;
  if (e.key === ' ') {
    if (G.state === 'title') startGame(0);
    else if (G.state === 'play') tryLaunch();
    else if (G.state === 'pause') togglePause();
    else if (G.state === 'over' || G.state === 'win') toTitle();
  }
  if (e.key.toLowerCase() === 'p' || e.key === 'Escape') {
    if (G.state === 'play' || G.state === 'pause') togglePause();
  }
  if (e.key.toLowerCase() === 'm') doMute();
});
window.addEventListener('keyup', e => { Input.keys[e.key.toLowerCase()] = false; });
document.addEventListener('visibilitychange', () => {
  if (document.hidden && G.state === 'play') togglePause(true);
});
// block scroll/zoom gestures on stage
document.addEventListener('touchmove', e => { if (e.target === canvas) e.preventDefault(); }, { passive: false });
document.addEventListener('gesturestart', e => e.preventDefault());

/* ================= 6. ENTITIES ================= */
const paddle = {
  x: 400, y: 500, w: 110, h: 16, vx: 0, vy: 0, px: 400, py: 500,
  tilt: 0, stretch: 0, flash: 0, minY: 0, maxY: 0,
};
function baseBallSpeed() { return clamp(Math.min(W, 800) * 0.62, 420, 520); }
function makeBall(x, y, angle, speed, kind) {
  const s = speed || baseBallSpeed();
  const a = angle == null ? rand(-Math.PI * 0.35, Math.PI * 0.35) - Math.PI / 2 : angle;
  return { x, y, vx: Math.cos(a) * s, vy: Math.sin(a) * s,
    r: 8, stuck: false, kind: kind || 'CHROME', trail: [], splitDone: false, id: Math.random() };
}
function ballSpeed(b) { return Math.hypot(b.vx, b.vy); }
function ballKind(b) {
  if (FX.spectre > 0) return 'SPECTRE';
  if (FX.ember > 0) return 'EMBER';
  return (b && b.kind) || 'CHROME';
}
const BRICK_STYLE = {
  G: { c1: '#2ee6ff', c2: '#0e6f8f', glow: '#22e6ff', name: 'GLASS', pts: 50 },
  V: { c1: '#ffe45e', c2: '#8f6a00', glow: '#ffd23d', name: 'VOLT', pts: 120 },
  A: { c1: '#ff6b7a', c2: '#8f1030', glow: '#ff5d6c', name: 'APEX', pts: 120 },
  S: { c1: '#b6ff6b', c2: '#3f8f00', glow: '#a3ff5e', name: 'SKIM', pts: 120 },
  P: { c1: '#c48bff', c2: '#4a1a8f', glow: '#b06bff', name: 'SPECTRAL', pts: 150 },
  M: { c1: '#ff8bf3', c2: '#6a1a7a', glow: '#ff3df0', name: 'SHIFTER', pts: 100 },
};
function buildLevel(idx) {
  G.bricks = []; G.caps = []; G.parts = []; G.texts = []; G.waves = [];
  G.balls = []; FX.ember = FX.spectre = FX.wide = FX.shield = 0; G.slowmo = 0;
  G.mult = 1; G.rooftop = false; G.roofTime = 0; G.roofGrace = 0; G.combo = 0; G.announced = {};
  const def = LEVELS[idx];
  const cols = 10, marginX = Math.max(14, W * 0.03);
  const top = 66, availW = W - marginX * 2;
  G.cellW = availW / cols; G.cellH = clamp(G.cellW * 0.42, 20, 30);
  G.gridL = marginX; G.gridT = top;
  def.rows.forEach((row, r) => {
    for (let c = 0; c < cols; c++) {
      const ch = row[c] || '.';
      if (ch === '.' || ch === ' ') continue;
      const type = 'GVASPM'.includes(ch) ? ch : 'G';
      G.bricks.push({ c, r, type, w: G.cellW - 5, h: G.cellH - 5,
        x: marginX + c * G.cellW + 2.5, y: top + r * G.cellH + 2.5,
        phase: rand(0, TAU), dir: c % 2 === 0 ? 1 : -1, flash: 0,
        born: G.time + (r * cols + c) * 0.012 });
    }
  });
  let minTop = Infinity;
  G.bricks.forEach(b => { minTop = Math.min(minTop, b.y); });
  G.roofY = (minTop === Infinity ? top : minTop) - 14;
  paddle.x = W / 2; paddle.px = paddle.x;
  paddle.maxY = H - 26; paddle.minY = H - 170;
  paddle.y = H - 70; paddle.py = paddle.y;
  paddle.w = clamp(W * 0.15, 86, 130);
  const b = makeBall(paddle.x, paddle.y - 20, -Math.PI / 2, 0);
  b.stuck = true; b.vx = 0; b.vy = 0;
  G.balls = [b];
  updateChips();
}

/* ================= 7. JUICE helpers ================= */
function spawnParts(x, y, color, n, spd, life, size) {
  const heat = 1 + Math.log2(Math.max(1, G.mult)) * 0.25 + (G.rooftop ? 1 : 0);
  n = Math.round(n * (G.rooftop ? heat : 1));
  for (let i = 0; i < n; i++) {
    if (G.parts.length > 700) return;
    const a = rand(0, TAU), s = rand(spd * 0.3, spd);
    G.parts.push({ x, y, vx: Math.cos(a) * s, vy: Math.sin(a) * s - 60,
      life: rand(life * 0.5, life), age: 0, size: rand(size * 0.5, size * 1.4), color });
  }
}
function shockwave(x, y, color, maxR) {
  if (G.waves.length > 24) G.waves.shift();
  G.waves.push({ x, y, r: 6, maxR: maxR || 90, age: 0, life: 0.45, color });
}
function floatText(x, y, str, color, size) {
  if (G.texts.length > 40) G.texts.shift();
  G.texts.push({ x, y, str, color: color || '#fff', age: 0, life: 1.1, size: size || 16 });
}
function toast(str, color) {
  const el = document.createElement('div');
  el.className = 'toastmsg'; el.textContent = str; el.style.color = color || '#fff';
  $('toast').appendChild(el);
  setTimeout(() => el.remove(), 1450);
}
function comboPop(str) {
  const el = $('combo');
  el.textContent = str;
  el.classList.remove('pop'); void el.offsetWidth; el.classList.add('pop');
}
function ballSpeedAvg() {
  if (!G.balls.length) return 0;
  return G.balls.reduce((s, b) => s + ballSpeed(b), 0) / G.balls.length;
}
function addScore(base, x, y, label) {
  const pts = Math.round(base * G.mult * (1 + ballSpeedAvg() / 2500));
  G.score += pts;
  if (x != null) floatText(x, y, '+' + pts, G.rooftop ? '#ffd23d' : '#aef', 15 + Math.min(12, Math.log2(G.mult + 1) * 4));
  if (label) toast(label, '#ffd23d');
  return pts;
}
function bumpCombo(x, y) {
  G.combo++; G.comboT = 2.2; G.bestCombo = Math.max(G.bestCombo, G.combo);
  const names = { 2: 'DOUBLE!', 3: 'TRIPLE!', 4: 'RAMPAGE!', 6: 'FRENZY!', 8: 'UNSTOPPABLE!', 12: 'GODLIKE!', 16: 'ROOFTOP LEGEND!' };
  if (names[G.combo]) { comboPop(names[G.combo]); addScore(25 * G.combo, x, y - 24); AudioSys.power(); }
  else if (G.combo > 2 && G.combo % 4 === 0) { comboPop(G.combo + ' STREAK!'); }
}
function shakeIt(amt) { G.shake = Math.min(22, G.shake + amt); }
function hitstop(t) { G.hitstop = Math.max(G.hitstop, t); }

/* ================= 8. POWER-UP CAPSULES ================= */
const CAPS = [
  { k: 'MULTI', icon: '◉', label: '+2 BALLS', c: '#3dff8b', w: 0.24 },
  { k: 'EMBER', icon: '🔥', label: 'EMBER!', c: '#ff8b3d', w: 0.18 },
  { k: 'GHOST', icon: '👻', label: 'SPECTRE!', c: '#b06bff', w: 0.18 },
  { k: 'WIDE', icon: '⬌', label: 'WIDE!', c: '#3ae0ff', w: 0.15 },
  { k: 'SHIELD', icon: '🛡', label: 'SHIELD!', c: '#ffd23d', w: 0.12 },
  { k: 'SLOW', icon: '🐌', label: 'SLOW-MO!', c: '#8be9ff', w: 0.10 },
  { k: 'LIFE', icon: '❤', label: '+1 LIFE!', c: '#ff5d8e', w: 0.03 },
];
function maybeDrop(x, y) {
  const chance = G.rooftop ? 0.22 : 0.13;
  if (Math.random() > chance) return;
  let tot = 0; CAPS.forEach(c => tot += c.w);
  let roll = Math.random() * tot, pick = CAPS[0];
  for (const c of CAPS) { roll -= c.w; if (roll <= 0) { pick = c; break; } }
  if (G.caps.length > 6) return;
  G.caps.push({ x, y, vy: 90, vx: rand(-20, 20), kind: pick.k, icon: pick.icon, label: pick.label, c: pick.c, age: 0 });
}
function applyCap(cap) {
  AudioSys.power();
  shockwave(paddle.x, paddle.y, cap.c, 70);
  spawnParts(paddle.x, paddle.y, cap.c, 22, 260, 0.6, 4);
  floatText(paddle.x, paddle.y - 30, cap.label, cap.c, 20);
  if (cap.kind === 'MULTI') {
    const flying = G.balls.filter(b => !b.stuck);
    const pool = flying.length ? flying.slice(0, 4) : G.balls.slice(0, 1);
    let added = 0;
    pool.forEach(b => {
      for (let i = 0; i < 2 && G.balls.length < BALL_MAX && added < 4; i++, added++) {
        const sp = Math.max(ballSpeed(b) || baseBallSpeed(), baseBallSpeed());
        const nb = makeBall(b.x, b.y, Math.atan2(b.vy || -1, b.vx || 0) + rand(-0.7, 0.7), sp, 'CHROME');
        nb.splitDone = true; G.balls.push(nb);
      }
    });
    if (!added && G.balls.length && G.balls.length + 2 <= BALL_MAX) {
      const b0 = G.balls[0];
      if (b0.stuck) { b0.stuck = false; b0.vx = 0; b0.vy = -baseBallSpeed(); }
      for (let i = -1; i <= 1; i += 2) {
        const nb = makeBall(b0.x, b0.y, -Math.PI / 2 + i * 0.45, baseBallSpeed(), 'CHROME');
        nb.splitDone = true; G.balls.push(nb);
      }
    }
    toast('MULTIBALL!', '#3dff8b');
  }
  else if (cap.kind === 'EMBER') { FX.ember = 10; toast('EMBER — PIERCE!', '#ff8b3d'); }
  else if (cap.kind === 'GHOST') { FX.spectre = 10; toast('SPECTRE — PHASE!', '#b06bff'); }
  else if (cap.kind === 'WIDE') { FX.wide = 12; }
  else if (cap.kind === 'SHIELD') { FX.shield = 12; }
  else if (cap.kind === 'SLOW') { G.slowmo = 6; G.slowmoDur = 6; }
  else if (cap.kind === 'LIFE') { G.lives++; toast('+1 LIFE!', '#ff5d8e'); }
  updateChips();
}
function updateChips() {
  const el = $('chips'); el.innerHTML = '';
  const add = (name, t, c) => {
    const d = document.createElement('div');
    d.className = 'chip'; d.style.color = c; d.style.borderColor = c;
    d.textContent = name + ' ' + Math.ceil(t) + 's';
    el.appendChild(d);
  };
  if (FX.ember > 0) add('🔥 EMBER', FX.ember, '#ff8b3d');
  if (FX.spectre > 0) add('👻 SPECTRE', FX.spectre, '#b06bff');
  if (FX.wide > 0) add('⬌ WIDE', FX.wide, '#3ae0ff');
  if (FX.shield > 0) add('🛡 SHIELD', FX.shield, '#ffd23d');
  if (G.slowmo > 0) add('🐌 SLOW-MO', G.slowmo, '#8be9ff');
}

/* ================= 9. BRICK HIT RULES (no HP bricks — condition bricks) ================= */
// Returns 'break' | 'bounce'. Sets hint text on bounce.
function brickHitTest(br, b, nx, ny) {
  const kind = ballKind(b), sp = ballSpeed(b);
  if (br.type === 'G' || br.type === 'M') return { r: 'break' };
  if (br.type === 'V') {
    if (sp > VOLT_SPEED) return { r: 'break' };
    return { r: 'bounce', hint: 'TOO SLOW!', c: '#ffd23d' };
  }
  if (br.type === 'A') {
    // need steep: |v . n| / |v| > cos(40°)
    const dot = Math.abs(b.vx * nx + b.vy * ny) / Math.max(1, sp);
    if (dot > APEX_COS) return { r: 'break' };
    return { r: 'bounce', hint: 'NEED STEEP!', c: '#ff6b7a' };
  }
  if (br.type === 'S') {
    // need shallow graze: |v . n| / |v| < cos(65°) (~0.42)
    const dot = Math.abs(b.vx * nx + b.vy * ny) / Math.max(1, sp);
    if (dot < 0.45) return { r: 'break' };
    return { r: 'bounce', hint: 'GRAZE IT!', c: '#b6ff6b' };
  }
  if (br.type === 'P') {
    if (kind === 'EMBER' || kind === 'SPECTRE') return { r: 'break' };
    return { r: 'bounce', hint: 'NEED GHOST/FIRE!', c: '#c48bff' };
  }
  return { r: 'break' };
}
function breakBrick(br, b) {
  br.dead = true;
  const st = BRICK_STYLE[br.type], kind = ballKind(b);
  const cx = br.x + br.w / 2, cy = br.y + br.h / 2;
  AudioSys.brick(br.type, G.mult);
  addScore(st.pts, cx, cy);
  bumpCombo(cx, cy);
  spawnParts(cx, cy, st.glow, 16, 300, 0.7, 5);
  spawnParts(cx, cy, '#ffffff', 6, 200, 0.4, 3);
  shockwave(cx, cy, st.glow, 60);
  shakeIt(br.type === 'G' ? 2 : 4);
  if (kind === 'EMBER') {
    spawnParts(cx, cy, '#ff8b3d', 18, 380, 0.6, 6);
    shockwave(cx, cy, '#ff8b3d', 90); shakeIt(5); hitstop(0.03);
  }
  if (G.rooftop) { AudioSys.roofTick(G.mult); }
  if (G.mult >= 8) hitstop(0.035);
  else if (G.mult >= 4) hitstop(0.02);
  maybeDrop(cx, cy);
  // SPLITTER: first brick impact splits ball into 3 (once per ball)
  if (!b.splitDone && G.balls.length + 2 <= BALL_MAX) {
    b.splitDone = true;
    const base = Math.atan2(b.vy, b.vx);
    for (let i = -1; i <= 1; i += 2) {
      const nb = makeBall(b.x, b.y, base + i * 0.5, clamp(ballSpeed(b), baseBallSpeed() * 0.9, BALL_CAP), 'CHROME');
      nb.splitDone = true; G.balls.push(nb);
    }
    toast('SPLITTER!', '#3ae0ff');
    spawnParts(b.x, b.y, '#3ae0ff', 14, 300, 0.5, 4);
  }
  // level cleared?
  if (!G.bricks.some(x => !x.dead)) levelCleared();
}

/* ================= 10. UPDATE ================= */
function tryLaunch() {
  const stuck = G.balls.find(b => b.stuck);
  if (stuck) {
    stuck.stuck = false;
    const s = baseBallSpeed();
    stuck.vx = rand(-140, 140); stuck.vy = -s;
    AudioSys.launch();
    shockwave(stuck.x, stuck.y, '#22e6ff', 50);
  }
}
function loseLife() {
  G.lives--;
  AudioSys.lifeLost();
  shakeIt(10);
  spawnParts(paddle.x, paddle.y, '#ff5d6c', 30, 350, 0.8, 5);
  G.mult = 1; G.rooftop = false; G.roofTime = 0; G.roofGrace = 0; G.combo = 0;
  FX.ember = FX.spectre = 0; G.slowmo = 0;
  if (G.lives <= 0) { gameOver(); return; }
  const b = makeBall(paddle.x, paddle.y - 20, -Math.PI / 2, 0);
  b.stuck = true; b.vx = 0; b.vy = 0;
  G.balls = [b];
  updateChips();
}
function levelCleared() {
  AudioSys.levelClear();
  addScore(500 * (G.level + 1), W / 2, H / 2, 'LEVEL CLEAR!');
  G.clearedLevels[G.level] = true;
  store.set('rooftopRampageDone', JSON.stringify(G.clearedLevels));
  shockwave(W / 2, H / 2, '#ffd23d', 220);
  hitstop(0.12);
  setTimeout(() => {
    if (G.state !== 'play') return;
    if (G.level >= LEVELS.length - 1) { victory(); }
    else { G.level++; showIntro(); }
  }, 1400);
}
function gameOver() {
  G.state = 'over';
  AudioSys.gameOver();
  saveHi();
  $('overScore').textContent = 'SCORE ' + G.score;
  $('overBest').textContent = 'BEST ' + G.hi + ' · reached level ' + (G.level + 1);
  show(null);
  $('overlayOver').classList.add('show');
}
function victory() {
  G.state = 'win';
  AudioSys.win();
  saveHi();
  $('winScore').textContent = 'SCORE ' + G.score;
  show(null);
  $('overlayWin').classList.add('show');
}
function saveHi() {
  if (G.score > G.hi) { G.hi = G.score; store.set('rooftopRampageHi', String(G.hi)); }
}
function updatePaddle(dt) {
  paddle.px = paddle.x; paddle.py = paddle.y;
  const targetW = (FX.wide > 0 ? clamp(W * 0.22, 130, 200) : clamp(W * 0.15, 86, 130));
  paddle.w = lerp(paddle.w, targetW, 1 - Math.pow(0.001, dt));
  const kb = 560;
  let kx = 0, ky = 0;
  if (Input.keys['arrowleft'] || Input.keys['a']) kx -= 1;
  if (Input.keys['arrowright'] || Input.keys['d']) kx += 1;
  if (Input.keys['arrowup'] || Input.keys['w']) ky -= 1;
  if (Input.keys['arrowdown'] || Input.keys['s']) ky += 1;
  if (kx || ky) { Input.x += kx * kb * dt; Input.y += ky * kb * dt; Input.hasPointer = true; }
  if (Input.hasPointer) {
    const f = 1 - Math.pow(0.0000001, dt); // snappy follow
    paddle.x = lerp(paddle.x, clamp(Input.x, paddle.w / 2 + 4, W - paddle.w / 2 - 4), f);
    paddle.y = lerp(paddle.y, clamp(Input.y, paddle.minY, paddle.maxY), f);
  }
  paddle.vx = (paddle.x - paddle.px) / Math.max(dt, 1e-4);
  paddle.vy = (paddle.y - paddle.py) / Math.max(dt, 1e-4);
  paddle.tilt = lerp(paddle.tilt, clamp(paddle.vx / 2400, -0.35, 0.35), 0.25);
  paddle.stretch = lerp(paddle.stretch, 0, dt * 6);
  paddle.flash = Math.max(0, paddle.flash - dt * 4);
}
function updateBalls(dt) {
  const slowF = G.slowmo > 0 ? 0.45 : 1;
  for (const b of G.balls) {
    if (b.stuck) { b.x = paddle.x; b.y = paddle.y - 20; b.trail.length = 0; continue; }
    const sdt = dt * slowF;
    b.x += b.vx * sdt; b.y += b.vy * sdt;
    // trail ribbon
    b.trail.push({ x: b.x, y: b.y });
    if (b.trail.length > 22) b.trail.shift();
    // walls
    if (b.x < b.r) { b.x = b.r; b.vx = Math.abs(b.vx); AudioSys.wall(); spawnParts(b.x, b.y, '#8ea0c7', 4, 150, 0.3, 3); }
    if (b.x > W - b.r) { b.x = W - b.r; b.vx = -Math.abs(b.vx); AudioSys.wall(); spawnParts(b.x, b.y, '#8ea0c7', 4, 150, 0.3, 3); }
    if (b.y < b.r) { b.y = b.r; b.vy = Math.abs(b.vy); AudioSys.wall(); }
    // paddle collision (circle vs rounded rect approx as rect)
    const pw = paddle.w / 2, ph = paddle.h / 2;
    const nx = clamp(b.x, paddle.x - pw, paddle.x + pw);
    const ny = clamp(b.y, paddle.y - ph, paddle.y + ph);
    const dx = b.x - nx, dy = b.y - ny;
    if (dx * dx + dy * dy < b.r * b.r && b.vy > -50) {
      // place above paddle, reflect up
      b.y = paddle.y - ph - b.r - 0.5;
      const rel = clamp((b.x - paddle.x) / pw, -1, 1);
      let ang = -Math.PI / 2 + rel * 0.9;
      let sp = clamp(ballSpeed(b) + (paddle.vy < -120 ? (-paddle.vy) * 0.45 : 0), baseBallSpeed() * 0.85, BALL_CAP);
      // smash: upward paddle velocity adds juice
      const smash = paddle.vy < -180;
      if (smash) {
        sp = Math.min(BALL_CAP, sp + 120);
        paddle.stretch = 1; paddle.flash = 1;
        AudioSys.smash(); AudioSys.paddle(true, sp / BALL_CAP);
        shockwave(b.x, b.y, '#ffd23d', 110); shakeIt(6); hitstop(0.03);
        floatText(b.x, b.y - 26, 'SMASH!', '#ffd23d', 20);
        spawnParts(b.x, b.y, '#ffd23d', 20, 400, 0.5, 5);
      } else {
        AudioSys.paddle(false, sp / BALL_CAP);
        spawnParts(b.x, b.y + 8, '#22e6ff', 6, 200, 0.35, 3);
      }
      // add paddle horizontal english
      b.vx = Math.cos(ang) * sp + paddle.vx * 0.18;
      b.vy = Math.sin(ang) * sp;
      const ns = ballSpeed(b);
      if (ns > BALL_CAP) { b.vx *= BALL_CAP / ns; b.vy *= BALL_CAP / ns; }
      // keep it moving upward
      if (b.vy > -120) b.vy = -220;
      G.comboT = Math.max(G.comboT, 0.8);
    }
    // floor: shield or death
    if (b.y > H - 14 && FX.shield > 0 && b.vy > 0) {
      b.y = H - 14 - b.r; b.vy = -Math.abs(b.vy);
      AudioSys.paddle(false, 0.5);
      spawnParts(b.x, H - 14, '#ffd23d', 10, 250, 0.4, 4);
      floatText(b.x, H - 60, 'SAVED!', '#ffd23d', 16);
    } else if (b.y > H + 8) { b.gone = true; continue; }
    // brick collisions (one per frame max, swept-ish: check overlap)
    const kind = ballKind(b);
    for (const br of G.bricks) {
      if (br.dead) continue;
      const bx = br.x + (br.type === 'M' ? shifterOffset(br) : 0);
      if (b.x + b.r < bx || b.x - b.r > bx + br.w || b.y + b.r < br.y || b.y - b.r > br.y + br.h) continue;
      // SPECTRE ghost: phase through dealing damage along path (break on contact, no bounce)
      if (kind === 'SPECTRE') { breakBrick(br, b); continue; }
      // contact normal: min penetration axis
      const cx = clamp(b.x, bx, bx + br.w), cy = clamp(b.y, br.y, br.y + br.h);
      let nnx = b.x - cx, nny = b.y - cy;
      if (nnx === 0 && nny === 0) { nny = b.vy > 0 ? -1 : 1; nnx = 0; }
      const nl = Math.hypot(nnx, nny) || 1; nnx /= nl; nny /= nl;
      const test = brickHitTest(br, b, nnx, nny);
      if (test.r === 'break') {
        // EMBER pierces: no bounce, explosion
        if (kind !== 'EMBER') {
          if (Math.abs(nnx) > Math.abs(nny)) b.vx = (nnx > 0 ? Math.abs(b.vx) : -Math.abs(b.vx));
          else b.vy = (nny > 0 ? Math.abs(b.vy) : -Math.abs(b.vy));
          // nudge out
          b.x += nnx * 2; b.y += nny * 2;
        } else {
          spawnParts(b.x, b.y, '#ff8b3d', 10, 320, 0.4, 4);
        }
        breakBrick(br, b);
        break;
      } else {
        // bounce + hint feedback
        if (Math.abs(nnx) > Math.abs(nny)) b.vx = (nnx > 0 ? Math.abs(b.vx) : -Math.abs(b.vx));
        else b.vy = (nny > 0 ? Math.abs(b.vy) : -Math.abs(b.vy));
        b.x += nnx * 3; b.y += nny * 3;
        br.flash = 1;
        AudioSys.denied();
        floatText(cx, cy - 8, test.hint, test.c, 14);
        spawnParts(cx, cy, test.c, 6, 180, 0.35, 3);
        break;
      }
    }
    // anti-horizontal-loop guard: never let a fast ball go (near-)horizontal
    if (!b.stuck && !b.gone) {
      const gsp = Math.hypot(b.vx, b.vy);
      if (gsp > 300 && Math.abs(b.vy) < 90) {
        b.vy += (b.vy >= 0 ? 140 : -140);
        const ns = Math.hypot(b.vx, b.vy) || 1;
        b.vx *= gsp / ns; b.vy *= gsp / ns;
      }
      // speed enforcement: keep ball speed within [base*0.75, BALL_CAP]
      const s3 = Math.hypot(b.vx, b.vy);
      const lo = baseBallSpeed() * 0.75;
      if (s3 > 1 && s3 < lo) { b.vx *= lo / s3; b.vy *= lo / s3; }
      else if (s3 > BALL_CAP) { b.vx *= BALL_CAP / s3; b.vy *= BALL_CAP / s3; }
    }
  }
  G.balls = G.balls.filter(b => !b.gone);
  if (!G.balls.length && G.state === 'play') loseLife();
}
function shifterOffset(br) {
  const range = Math.max(0, G.cellW * 1.2);
  const t = G.time * 0.9 + br.phase;
  return Math.sin(t) * range * 0.5 * (G.cellW > 0 ? 1 : 0);
}
function updateRooftop(dt) {
  const anyRoof = G.balls.some(b => !b.stuck && b.y < G.roofY);
  if (anyRoof) {
    if (!G.rooftop) {
      G.rooftop = true; G.roofTime = 0;
      AudioSys.roofEnter();
      toast('★ ROOFTOP ★', '#ffd23d');
      shockwave(W / 2, G.roofY, '#ff3df0', 200);
      shakeIt(5);
    }
    G.roofGrace = 0.9;
  } else if (G.rooftop) {
    // grace: keep rooftop briefly after last ball leaves roof
    G.roofGrace -= dt;
    if (G.roofGrace <= 0) {
      G.rooftop = false; G.mult = 1;
      AudioSys.roofExit();
    }
  }
  if (G.rooftop) {
    G.roofTime += dt;
    // double every 4s: x2,x4,x8... cap x99
    const target = Math.min(99, Math.pow(2, Math.floor(G.roofTime / 4) + 1));
    if (target > G.mult) {
      G.mult = target;
      toast('ROOFTOP x' + G.mult, '#ff3df0');
      comboPop('x' + G.mult + '!!');
      AudioSys.power();
      shockwave(W / 2, G.roofY, '#ffd23d', 160);
      shakeIt(4);
      if (G.mult >= 8) hitstop(0.04);
    }
    // passive score tick, faster with mult
    G.roofTickT -= dt;
    if (G.roofTickT <= 0) {
      G.roofTickT = 0.5;
      G.score += 5 * G.mult;
      AudioSys.roofTick(G.mult);
      spawnParts(rand(0, W), G.roofY - rand(0, 40), '#ff3df0', 3, 120, 0.6, 3);
    }
  } else {
    G.mult = Math.max(1, G.mult - dt * 0); // stays 1 off-roof
  }
  G.pulse = lerp(G.pulse, G.rooftop ? 1 : 0, dt * 3);
}
function updateCaps(dt) {
  for (const c of G.caps) {
    c.age += dt; c.y += c.vy * dt; c.x += c.vx * dt + Math.sin(c.age * 3) * 20 * dt;
    // magnetism toward paddle when close
    const dx = paddle.x - c.x, dy = paddle.y - c.y;
    const d = Math.hypot(dx, dy);
    if (d < 160 && d > 1) { c.x += dx / d * 160 * dt; c.y += dy / d * 60 * dt; }
    // catch
    if (c.y > paddle.y - 18 && c.y < paddle.y + 18 && Math.abs(c.x - paddle.x) < paddle.w / 2 + 14) {
      c.got = true; applyCap(c);
    }
    if (c.y > H + 20) c.gone = true;
  }
  G.caps = G.caps.filter(c => !c.gone && !c.got);
}
function updateFx(dt) {
  for (const p of G.parts) { p.age += dt; p.x += p.vx * dt; p.y += p.vy * dt; p.vy += 500 * dt; }
  G.parts = G.parts.filter(p => p.age < p.life);
  for (const t of G.texts) { t.age += dt; t.y -= 46 * dt; }
  G.texts = G.texts.filter(t => t.age < t.life);
  for (const w of G.waves) { w.age += dt; w.r = lerp(w.maxR, 6, Math.pow(1 - w.age / w.life, 2)); }
  G.waves = G.waves.filter(w => w.age < w.life);
  for (const br of G.bricks) br.flash = Math.max(0, br.flash - dt * 3);
  G.shake = Math.max(0, G.shake - dt * 40);
  G.comboT -= dt; if (G.comboT <= 0) G.combo = 0;
  G.flashA = Math.max(0, G.flashA - dt * 2);
  // effect timers
  let chipDirty = false;
  for (const k of ['ember', 'spectre', 'wide', 'shield']) {
    if (FX[k] > 0) { FX[k] -= dt; if (FX[k] <= 0) { FX[k] = 0; chipDirty = true; } }
  }
  if (G.slowmo > 0) { G.slowmo -= dt; if (G.slowmo <= 0) { G.slowmo = 0; chipDirty = true; } }
  if (chipDirty || ((FX.ember + FX.spectre + FX.wide + FX.shield + G.slowmo) > 0 && Math.floor(G.time * 4) !== Math.floor((G.time - dt) * 4))) updateChips();
  // ambient stars
  if (G.stars.length < 70 && Math.random() < 0.3) G.stars.push({ x: rand(0, W), y: rand(0, H), s: rand(0.5, 2), tw: rand(0, TAU) });
  if (G.stars.length > 120) G.stars.splice(0, G.stars.length - 120);
}

/* ================= 11. RENDER ================= */
function render() {
  ctx.save();
  // screen shake
  if (G.shake > 0.2) ctx.translate(rand(-G.shake, G.shake) * 0.5, rand(-G.shake, G.shake) * 0.5);
  // background: gradient intensifies with rooftop/pulse
  const p = G.pulse;
  const g = ctx.createLinearGradient(0, 0, 0, H);
  g.addColorStop(0, mixc('#050816', '#2a0a2e', p * 0.8));
  g.addColorStop(0.5, mixc('#070b1e', '#3a0f3a', p * 0.7));
  g.addColorStop(1, mixc('#0a1030', '#40124a', p * 0.6));
  ctx.fillStyle = g; ctx.fillRect(-30, -30, W + 60, H + 60);
  // grid
  ctx.strokeStyle = 'rgba(60,90,180,' + (0.14 + p * 0.25) + ')';
  ctx.lineWidth = 1;
  const gs = 44;
  ctx.beginPath();
  const tOff = (G.time * (20 + p * 60)) % gs;
  for (let x = 0; x <= W; x += gs) { ctx.moveTo(x, 0); ctx.lineTo(x, H); }
  for (let y = -gs + tOff; y <= H; y += gs) { ctx.moveTo(0, y); ctx.lineTo(W, y); }
  ctx.stroke();
  // stars
  for (const s of G.stars) {
    const a = 0.3 + 0.7 * Math.abs(Math.sin(G.time * 2 + s.tw));
    ctx.fillStyle = 'rgba(180,220,255,' + (a * 0.6).toFixed(2) + ')';
    ctx.fillRect(s.x, s.y, s.s, s.s);
  }
  // rooftop zone tint
  if (G.roofY > 0) {
    const rg = ctx.createLinearGradient(0, 0, 0, G.roofY);
    rg.addColorStop(0, 'rgba(255,61,240,' + (0.10 + p * 0.22) + ')');
    rg.addColorStop(1, 'rgba(255,61,240,0.02)');
    ctx.fillStyle = rg; ctx.fillRect(0, 0, W, G.roofY);
  }
  // rooftop line
  if (G.bricks.length || G.rooftop) drawRoofLine();
  drawBricks();
  drawCaps();
  drawPaddleTrack();
  drawPaddle();
  drawBalls();
  drawParts();
  drawWaves();
  drawTexts();
  drawShield();
  // vignette intensity with multiplier
  const heat = clamp(Math.log2(Math.max(1, G.mult)) / 6.5, 0, 1);
  const vg = ctx.createRadialGradient(W / 2, H / 2, Math.min(W, H) * 0.35, W / 2, H / 2, Math.max(W, H) * 0.75);
  vg.addColorStop(0, 'rgba(0,0,0,0)');
  vg.addColorStop(1, 'rgba(' + (120 + heat * 120) + ',0,80,' + (0.28 + heat * 0.3 + p * 0.1) + ')');
  ctx.fillStyle = vg; ctx.fillRect(-30, -30, W + 60, H + 60);
  // slow-mo tint
  if (G.slowmo > 0) { ctx.fillStyle = 'rgba(120,200,255,0.08)'; ctx.fillRect(-30, -30, W + 60, H + 60); }
  // screen pulse on rooftop beat
  if (G.rooftop) {
    const beat = Math.pow(Math.sin(G.time * 6), 2) * 0.05;
    ctx.fillStyle = 'rgba(255,61,240,' + beat.toFixed(3) + ')';
    ctx.fillRect(-30, -30, W + 60, H + 60);
  }
  ctx.restore();
}
function mixc(a, b, t) {
  const pa = [1, 3, 5].map(i => parseInt(a.substr(i, 2), 16));
  const pb = [1, 3, 5].map(i => parseInt(b.substr(i, 2), 16));
  return 'rgb(' + pa.map((v, i) => Math.round(lerp(v, pb[i], clamp(t, 0, 1)))).join(',') + ')';
}
function drawRoofLine() {
  const p = G.pulse, y = G.roofY;
  ctx.save();
  ctx.shadowBlur = 16 + p * 20; ctx.shadowColor = '#ff3df0';
  ctx.strokeStyle = G.rooftop ? '#ffd23d' : '#ff3df0';
  ctx.lineWidth = G.rooftop ? 3.5 : 2;
  ctx.setLineDash([14, 8]);
  ctx.lineDashOffset = -G.time * 60;
  ctx.beginPath(); ctx.moveTo(0, y); ctx.lineTo(W, y); ctx.stroke();
  ctx.setLineDash([]);
  ctx.shadowBlur = 12; ctx.shadowColor = ctx.strokeStyle;
  ctx.fillStyle = ctx.strokeStyle;
  ctx.font = '800 ' + (G.rooftop ? 17 : 13) + 'px sans-serif';
  ctx.textAlign = 'center';
  const label = G.rooftop ? '★ ROOFTOP x' + G.mult + ' ★' : '— ROOFTOP — get above the bricks —';
  ctx.fillText(label, W / 2, y - 6);
  ctx.restore();
}
function drawBricks() {
  const fastEnough = ballSpeedAvg() > VOLT_SPEED || G.balls.some(b => !b.stuck && ballSpeed(b) > VOLT_SPEED);
  for (const br of G.bricks) {
    if (br.dead) continue;
    const pop = clamp((G.time - br.born) * 3, 0, 1);
    const sc = pop < 1 ? 0.3 + 0.7 * (1 - Math.pow(1 - pop, 3)) : 1;
    const ox = br.type === 'M' ? shifterOffset(br) : 0;
    const bx = br.x + ox, by = br.y;
    const cx = bx + br.w / 2, cy = by + br.h / 2;
    const st = BRICK_STYLE[br.type];
    ctx.save();
    ctx.translate(cx, cy); ctx.scale(sc, sc); ctx.translate(-cx, -cy);
    const wob = br.type === 'M' ? Math.sin(G.time * 3 + br.phase) * 1.5 : 0;
    // body
    const bg = ctx.createLinearGradient(bx, by, bx, by + br.h);
    bg.addColorStop(0, st.c1); bg.addColorStop(1, st.c2);
    ctx.fillStyle = bg;
    ctx.shadowBlur = 12 + (br.type === 'V' && fastEnough ? 18 : 0) + br.flash * 24;
    ctx.shadowColor = st.glow;
    roundRect(bx, by + wob * 0.3, br.w, br.h, 6); ctx.fill();
    ctx.shadowBlur = 0;
    ctx.strokeStyle = br.flash > 0 ? '#ffffff' : st.glow + 'aa';
    ctx.lineWidth = br.flash > 0 ? 2.5 : 1.5;
    roundRect(bx, by + wob * 0.3, br.w, br.h, 6); ctx.stroke();
    // glass shine
    ctx.fillStyle = 'rgba(255,255,255,0.25)';
    roundRect(bx + 3, by + 2 + wob * 0.3, br.w - 6, br.h * 0.32, 4); ctx.fill();
    drawBrickIcon(br.type, cx, cy + 1 + wob * 0.3, br.h, fastEnough);
    ctx.restore();
  }
}
function drawBrickIcon(type, cx, cy, h, fastEnough) {
  ctx.save();
  ctx.fillStyle = 'rgba(10,10,25,0.85)';
  ctx.strokeStyle = 'rgba(10,10,25,0.85)';
  const s = Math.min(16, h * 0.55);
  ctx.lineWidth = 2.2; ctx.lineCap = 'round'; ctx.lineJoin = 'round';
  ctx.beginPath();
  if (type === 'V') { // lightning bolt
    ctx.shadowBlur = fastEnough ? 14 : 0; ctx.shadowColor = '#fff';
    ctx.fillStyle = fastEnough ? '#fff' : 'rgba(20,20,10,0.9)';
    const w = s * 0.55;
    ctx.moveTo(cx + w * 0.3, cy - s * 0.6); ctx.lineTo(cx - w * 0.5, cy + s * 0.1);
    ctx.lineTo(cx, cy + s * 0.1); ctx.lineTo(cx - w * 0.3, cy + s * 0.6);
    ctx.lineTo(cx + w * 0.5, cy - s * 0.1); ctx.lineTo(cx, cy - s * 0.1);
    ctx.closePath(); ctx.fill();
  } else if (type === 'A') { // chevron up
    ctx.moveTo(cx - s * 0.55, cy + s * 0.25); ctx.lineTo(cx, cy - s * 0.4); ctx.lineTo(cx + s * 0.55, cy + s * 0.25);
    ctx.moveTo(cx - s * 0.55, cy + s * 0.55); ctx.lineTo(cx, cy - s * 0.1); ctx.lineTo(cx + s * 0.55, cy + s * 0.55);
    ctx.stroke();
  } else if (type === 'S') { // sideways chevrons
    ctx.moveTo(cx - s * 0.3, cy - s * 0.45); ctx.lineTo(cx + s * 0.15, cy); ctx.lineTo(cx - s * 0.3, cy + s * 0.45);
    ctx.moveTo(cx + s * 0.05, cy - s * 0.45); ctx.lineTo(cx + s * 0.5, cy); ctx.lineTo(cx + s * 0.05, cy + s * 0.45);
    ctx.stroke();
  } else if (type === 'P') { // ghost
    ctx.fillStyle = 'rgba(15,5,30,0.9)';
    ctx.arc(cx, cy - s * 0.05, s * 0.42, Math.PI, 0);
    ctx.rect(cx - s * 0.42, cy - s * 0.05, s * 0.84, s * 0.42);
    ctx.fill();
    ctx.fillStyle = '#fff';
    ctx.beginPath(); ctx.arc(cx - s * 0.15, cy - s * 0.1, s * 0.09, 0, TAU); ctx.arc(cx + s * 0.15, cy - s * 0.1, s * 0.09, 0, TAU); ctx.fill();
  } else if (type === 'M') { // double arrow
    ctx.moveTo(cx - s * 0.6, cy); ctx.lineTo(cx - s * 0.2, cy - s * 0.3); ctx.moveTo(cx - s * 0.6, cy); ctx.lineTo(cx - s * 0.2, cy + s * 0.3);
    ctx.moveTo(cx + s * 0.6, cy); ctx.lineTo(cx + s * 0.2, cy - s * 0.3); ctx.moveTo(cx + s * 0.6, cy); ctx.lineTo(cx + s * 0.2, cy + s * 0.3);
    ctx.moveTo(cx - s * 0.6, cy); ctx.lineTo(cx + s * 0.6, cy);
    ctx.stroke();
  }
  ctx.restore();
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
/* ---- paddle / balls / pickups ---- */
function drawPaddleTrack() {
  ctx.save();
  ctx.strokeStyle = 'rgba(90,130,220,0.25)';
  ctx.lineWidth = 1.5; ctx.setLineDash([4, 6]);
  const x = paddle.x;
  ctx.beginPath(); ctx.moveTo(x, paddle.minY - 14); ctx.lineTo(x, paddle.maxY + 6); ctx.stroke();
  ctx.setLineDash([]);
  ctx.strokeStyle = 'rgba(90,130,220,0.35)';
  roundRect(x - paddle.w / 2 - 10, paddle.minY - 14, paddle.w + 20, (paddle.maxY - paddle.minY) + 28, 12);
  ctx.stroke();
  ctx.restore();
}
function drawPaddle() {
  ctx.save();
  ctx.translate(paddle.x, paddle.y);
  ctx.rotate(paddle.tilt);
  const squash = 1 + paddle.stretch * 0.35;
  ctx.scale(1 / Math.sqrt(squash) * (1 + paddle.flash * 0.1), Math.sqrt(squash));
  const w = paddle.w, h = paddle.h;
  ctx.shadowBlur = 22 + paddle.flash * 30 + G.pulse * 14;
  ctx.shadowColor = paddle.flash > 0 ? '#ffd23d' : '#22e6ff';
  const g = ctx.createLinearGradient(0, -h / 2, 0, h / 2);
  if (paddle.flash > 0) { g.addColorStop(0, '#fff'); g.addColorStop(1, '#ffd23d'); }
  else { g.addColorStop(0, '#bdf6ff'); g.addColorStop(0.45, '#22e6ff'); g.addColorStop(1, '#0a5f8f'); }
  ctx.fillStyle = g;
  roundRect(-w / 2, -h / 2, w, h, h / 2); ctx.fill();
  ctx.shadowBlur = 0;
  ctx.fillStyle = 'rgba(255,255,255,0.85)';
  roundRect(-w / 2 + 8, -h / 2 + 2.5, w - 16, 3.5, 2); ctx.fill();
  // core light
  ctx.fillStyle = '#fff';
  ctx.beginPath(); ctx.arc(0, 0, 3 + paddle.flash * 3, 0, TAU); ctx.fill();
  // up-flick indicator: brighter when moving up
  if (paddle.vy < -120) {
    ctx.fillStyle = 'rgba(255,210,61,' + clamp(-paddle.vy / 1500, 0, 0.9).toFixed(2) + ')';
    ctx.beginPath();
    ctx.moveTo(-14, -h / 2 - 4); ctx.lineTo(0, -h / 2 - 14); ctx.lineTo(14, -h / 2 - 4);
    ctx.closePath(); ctx.fill();
  }
  ctx.restore();
}
function drawBalls() {
  for (const b of G.balls) {
    const kind = ballKind(b);
    const sp = ballSpeed(b);
    // speed lines
    if (sp > 620 && !b.stuck) {
      ctx.save();
      ctx.strokeStyle = kind === 'EMBER' ? 'rgba(255,140,60,0.5)' : kind === 'SPECTRE' ? 'rgba(176,107,255,0.5)' : 'rgba(140,230,255,0.45)';
      ctx.lineWidth = 2;
      const l = clamp((sp - 620) / 480, 0, 1) * 46;
      const nx = b.vx / sp, ny = b.vy / sp;
      for (let i = -1; i <= 1; i++) {
        ctx.beginPath();
        ctx.moveTo(b.x - nx * (b.r + 2) - ny * i * 6, b.y - ny * (b.r + 2) + nx * i * 6);
        ctx.lineTo(b.x - nx * (b.r + 2 + l) - ny * i * 6, b.y - ny * (b.r + 2 + l) + nx * i * 6);
        ctx.stroke();
      }
      ctx.restore();
    }
    // trail ribbon
    if (b.trail.length > 1) {
      ctx.save();
      ctx.lineCap = 'round';
      for (let i = 1; i < b.trail.length; i++) {
        const t = i / b.trail.length;
        ctx.strokeStyle = kind === 'EMBER' ? 'rgba(255,120,40,' + (t * 0.55).toFixed(2) + ')'
          : kind === 'SPECTRE' ? 'rgba(176,107,255,' + (t * 0.55).toFixed(2) + ')'
          : 'rgba(60,220,255,' + (t * 0.5).toFixed(2) + ')';
        ctx.lineWidth = b.r * 1.5 * t;
        ctx.beginPath();
        ctx.moveTo(b.trail[i - 1].x, b.trail[i - 1].y);
        ctx.lineTo(b.trail[i].x, b.trail[i].y);
        ctx.stroke();
      }
      ctx.restore();
    }
    // glow ball
    ctx.save();
    const col = kind === 'EMBER' ? '#ff7a2e' : kind === 'SPECTRE' ? '#b06bff' : '#dffaff';
    const core = kind === 'EMBER' ? '#ffd23d' : kind === 'SPECTRE' ? '#e7d0ff' : '#ffffff';
    ctx.shadowBlur = 20; ctx.shadowColor = col;
    const bg = ctx.createRadialGradient(b.x - 2, b.y - 2, 1, b.x, b.y, b.r + 2);
    if (kind === 'EMBER') { bg.addColorStop(0, '#fff'); bg.addColorStop(0.4, core); bg.addColorStop(1, '#ff4400'); }
    else if (kind === 'SPECTRE') { bg.addColorStop(0, '#fff'); bg.addColorStop(0.45, core); bg.addColorStop(1, '#5a1ab0'); }
    else { bg.addColorStop(0, '#fff'); bg.addColorStop(0.55, '#c9f6ff'); bg.addColorStop(1, '#0aa8dd'); }
    ctx.globalAlpha = kind === 'SPECTRE' ? 0.85 : 1;
    ctx.fillStyle = bg;
    ctx.beginPath(); ctx.arc(b.x, b.y, b.r, 0, TAU); ctx.fill();
    ctx.shadowBlur = 0;
    if (kind === 'EMBER') { // flame flicker
      ctx.fillStyle = 'rgba(255,200,80,' + (0.5 + Math.random() * 0.4).toFixed(2) + ')';
      ctx.beginPath(); ctx.arc(b.x + rand(-3, 3), b.y - b.r - rand(1, 5), rand(2, 4.5), 0, TAU); ctx.fill();
    }
    if (kind === 'SPECTRE') {
      ctx.fillStyle = '#2a0a4a';
      ctx.beginPath(); ctx.arc(b.x - 2.5, b.y - 1, 1.6, 0, TAU); ctx.arc(b.x + 2.5, b.y - 1, 1.6, 0, TAU); ctx.fill();
    }
    ctx.restore();
  }
}
function drawCaps() {
  for (const c of G.caps) {
    ctx.save();
    ctx.translate(c.x, c.y + Math.sin(c.age * 5) * 2);
    ctx.shadowBlur = 14; ctx.shadowColor = c.c;
    ctx.fillStyle = '#0d1530';
    ctx.strokeStyle = c.c; ctx.lineWidth = 2;
    roundRect(-22, -12, 44, 24, 12); ctx.fill(); ctx.stroke();
    ctx.shadowBlur = 0;
    ctx.fillStyle = c.c; ctx.font = '800 13px sans-serif'; ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    ctx.fillText(c.icon, 0, 1);
    ctx.restore();
  }
}
function drawParts() {
  ctx.save();
  for (const p of G.parts) {
    const t = 1 - p.age / p.life;
    ctx.globalAlpha = clamp(t, 0, 1);
    ctx.fillStyle = p.color;
    ctx.fillRect(p.x - p.size / 2, p.y - p.size / 2, p.size, p.size);
  }
  ctx.restore();
}
function drawWaves() {
  ctx.save();
  for (const w of G.waves) {
    const t = w.age / w.life;
    ctx.globalAlpha = 1 - t;
    ctx.strokeStyle = w.color; ctx.lineWidth = 3 * (1 - t) + 1;
    ctx.shadowBlur = 12; ctx.shadowColor = w.color;
    ctx.beginPath(); ctx.arc(w.x, w.y, w.r, 0, TAU); ctx.stroke();
  }
  ctx.restore();
}
function drawTexts() {
  ctx.save();
  ctx.textAlign = 'center';
  for (const t of G.texts) {
    const a = t.age < 0.15 ? t.age / 0.15 : 1 - Math.max(0, (t.age - 0.5) / (t.life - 0.5));
    ctx.globalAlpha = clamp(a, 0, 1);
    ctx.font = '800 ' + t.size + 'px sans-serif';
    ctx.shadowBlur = 10; ctx.shadowColor = t.color;
    ctx.fillStyle = t.color;
    ctx.fillText(t.str, t.x, t.y);
  }
  ctx.restore();
}
function drawShield() {
  if (FX.shield <= 0) return;
  ctx.save();
  ctx.strokeStyle = 'rgba(255,210,61,' + (0.5 + Math.sin(G.time * 6) * 0.2).toFixed(2) + ')';
  ctx.lineWidth = 3;
  ctx.shadowBlur = 14; ctx.shadowColor = '#ffd23d';
  ctx.setLineDash([18, 10]); ctx.lineDashOffset = -G.time * 80;
  ctx.beginPath(); ctx.moveTo(4, H - 12); ctx.lineTo(W - 4, H - 12); ctx.stroke();
  ctx.restore();
}

/* ================= 12. HUD / OVERLAYS / FLOW ================= */
function show(id) {
  document.querySelectorAll('.overlay').forEach(o => o.classList.remove('show'));
  if (id) $(id).classList.add('show');
}
function refreshHud() {
  $('hudScore').textContent = G.score;
  $('hudHi').textContent = G.hi;
  $('hudLevel').textContent = (G.level + 1) + '/7';
  $('hudLives').textContent = '●'.repeat(Math.max(0, Math.min(9, G.lives))) || '—';
  $('hudBalls').textContent = G.balls.length;
  const badge = $('rooftopBadge');
  if (G.rooftop) { badge.classList.remove('hidden'); $('rooftopX').textContent = 'x' + G.mult; }
  else badge.classList.add('hidden');
}
function buildLvlBtns() {
  const el = $('lvlBtns'); el.innerHTML = '';
  LEVELS.forEach((L, i) => {
    const b = document.createElement('button');
    b.textContent = i + 1;
    if (G.clearedLevels[i]) b.classList.add('done');
    if (i === G.level) b.classList.add('cur');
    b.onclick = () => { AudioSys.resume(); startGame(i); };
    el.appendChild(b);
  });
}
function startGame(lvl) {
  AudioSys.resume();
  G.level = lvl || 0; G.score = 0; G.lives = 3; G.bestCombo = 0;
  saveHi0();
  showIntro();
}
function saveHi0() { if (G.score > G.hi) { G.hi = G.score; } refreshHud(); }
function showIntro() {
  G.state = 'intro';
  buildLevel(G.level);
  refreshHud(); buildLvlBtns();
  $('lvlKicker').textContent = 'LEVEL ' + (G.level + 1) + ' / 7';
  $('lvlName').textContent = LEVELS[G.level].name;
  $('lvlDesc').textContent = LEVELS[G.level].desc;
  show('overlayLevel');
  G.introT = 1.6;
}
function toTitle() {
  G.state = 'title';
  saveHi();
  buildLvlBtns(); refreshHud();
  show('overlayTitle');
}
function togglePause(force) {
  if (G.state === 'play') { G.state = 'pause'; show('overlayPause'); }
  else if (G.state === 'pause' && !force) { G.state = 'play'; show(null); }
}
function doMute() {
  AudioSys.resume();
  const m = AudioSys.toggleMute();
  $('btnMute').style.opacity = m ? 0.4 : 1;
  $('btnMute').innerHTML = m ? '✕' : '&#9836;';
}
function bindUI() {
  $('btnStart').onclick = () => startGame(G.level);
  $('btnHow').onclick = () => { $('overlayHelp').classList.add('show'); };
  $('btnHelp').onclick = () => { AudioSys.resume(); $('overlayHelp').classList.add('show'); };
  $('btnCloseHelp').onclick = () => $('overlayHelp').classList.remove('show');
  $('btnMute').onclick = doMute;
  $('btnPause').onclick = () => { if (G.state === 'play' || G.state === 'pause') togglePause(); };
  $('btnRestart').onclick = () => startGame(G.level);
  $('btnRestart2').onclick = () => startGame(G.level);
  $('btnResume').onclick = () => togglePause();
  $('btnRetry').onclick = () => startGame(0);
  $('btnAgain').onclick = () => startGame(0);
  $('btnMenu').onclick = toTitle;
  $('btnMenu2').onclick = toTitle;
  $('btnLaunch').onclick = () => { AudioSys.resume(); if (G.state === 'play') tryLaunch(); else if (G.state === 'title') startGame(G.level); };
  $('btnSlowHud').onclick = () => { if (G.state === 'play' || G.state === 'pause') togglePause(); };
  $('overlayHelp').addEventListener('click', e => { if (e.target === $('overlayHelp')) e.target.classList.remove('show'); });
}

/* ================= 13. RESIZE / LOOP / BOOT ================= */
function resize() {
  const r = canvas.getBoundingClientRect();
  DPR = Math.min(2, window.devicePixelRatio || 1);
  const oldW = W, oldH = H;
  W = Math.max(320, Math.round(r.width));
  H = Math.max(380, Math.round(r.height));
  canvas.width = Math.round(W * DPR);
  canvas.height = Math.round(H * DPR);
  ctx.setTransform(DPR, 0, 0, DPR, 0, 0);
  paddle.minY = H - 170; paddle.maxY = H - 26;
  paddle.x = clamp(paddle.x, 0, W); paddle.y = clamp(paddle.y || H - 70, paddle.minY, paddle.maxY);
  Input.x = clamp(Input.x || W / 2, 0, W); Input.y = clamp(Input.y || H - 70, 0, H);
  // Rescale geometry in place: never rebuild level state (keeps balls/FX/score/dead).
  if (!G.bricks.length || !G.cellW) return;
  // Skip trivial changes to avoid jitter.
  if (Math.abs(W - oldW) < 1 && Math.abs(H - oldH) < 1) return;
  const cols = 10, marginX = Math.max(14, W * 0.03);
  const top = 66, availW = W - marginX * 2;
  G.cellW = availW / cols; G.cellH = clamp(G.cellW * 0.42, 20, 30);
  G.gridL = marginX; G.gridT = top;
  for (const br of G.bricks) {
    br.w = G.cellW - 5; br.h = G.cellH - 5;
    br.x = marginX + br.c * G.cellW + 2.5; br.y = top + br.r * G.cellH + 2.5;
  }
  let minTop = Infinity;
  for (const br of G.bricks) { if (!br.dead) minTop = Math.min(minTop, br.y); }
  G.roofY = (minTop === Infinity ? top : minTop) - 14;
  for (const b of G.balls) {
    b.x = clamp(b.x, b.r, W - b.r);
    b.y = clamp(b.y, b.r, H + 8);
    if (!Array.isArray(b.trail)) b.trail = [];
  }
  for (const c of G.caps) { c.x = clamp(c.x, 0, W); c.y = clamp(c.y, -20, H + 20); }
}
let lastT = 0;
function frame(t) {
  requestAnimationFrame(frame);
  const rawDt = Math.min(0.05, (t - lastT) / 1000 || 0.016);
  lastT = t;
  // hit-stop freeze
  if (G.hitstop > 0) { G.hitstop -= rawDt; render(); refreshHud(); return; }
  const dt = rawDt; // framerate-independent (clamped)
  G.time += dt;
  if (G.state === 'intro') {
    G.introT -= dt;
    updatePaddle(dt); updateFx(dt);
    G.balls.forEach(b => { if (b.stuck) { b.x = paddle.x; b.y = paddle.y - 20; } });
    if (G.introT <= 0) { G.state = 'play'; show(null); }
  } else if (G.state === 'play') {
    updatePaddle(dt);
    updateBalls(dt);
    updateRooftop(dt);
    updateCaps(dt);
    updateFx(dt);
    if (G.state === 'play') AudioSys.bass(dt, G.mult, G.rooftop);
    if (G.score > G.hi) { G.hi = G.score; }
  } else if (G.state === 'title' || G.state === 'over' || G.state === 'win' || G.state === 'pause') {
    updateFx(dt);
    if (G.stars.length < 5) { for (let i = 0; i < 40; i++) G.stars.push({ x: rand(0, W), y: rand(0, H), s: rand(0.5, 2), tw: rand(0, TAU) }); }
  }
  render();
  refreshHud();
}
function boot() {
  bindUI();
  buildLvlBtns();
  // initial board behind title
  resize();
  buildLevel(0);
  Input.x = W / 2; Input.y = H - 70;
  paddle.x = W / 2; paddle.y = H - 70;
  window.addEventListener('resize', resize);
  window.addEventListener('orientationchange', () => setTimeout(resize, 200));
  refreshHud();
  show('overlayTitle');
  requestAnimationFrame(frame);
}
boot();





