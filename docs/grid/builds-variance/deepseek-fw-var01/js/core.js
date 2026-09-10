/* ============================================================
   TOPSIDE — core.js
   Constants, helpers, entity classes, and the global game state.
   No external dependencies.
   ============================================================ */
'use strict';

/* ---------- colors / palette ---------- */
const PAL = {
  bg0:   '#0b0620',
  bg1:   '#120a2e',
  nebula:['#2a1460','#1b0f45','#0e0930','#171046'],
  paddle:'#9ff6ff',
  trail: ['#fffbe0','#ffe14d','#ff9d5c','#ff5c8a','#b05cff'],
  brick: {
    p:   {n:'pink',    fill:'#ff5c8a', glow:'#ff8fb2', edge:'#ffd9e6'},
    o:   {n:'orange',  fill:'#ff9d5c', glow:'#ffb97f', edge:'#ffe6cc'},
    y:   {n:'gold',    fill:'#ffd84d', glow:'#ffe58a', edge:'#fff2cc'},
    g:   {n:'green',   fill:'#4ade80', glow:'#8af0b0', edge:'#d9ffe9'},
    c:   {n:'cyan',    fill:'#22d3ee', glow:'#7fecff', edge:'#d6fbff'},
    b:   {n:'blue',    fill:'#5b8cff', glow:'#8fb2ff', edge:'#dbe6ff'},
    v:   {n:'violet',  fill:'#b05cff', glow:'#cc8fff', edge:'#f0e0ff'},
    w:   {n:'white',   fill:'#ffffff', glow:'#ffffff', edge:'#ffffff'}
  },
  danger:'#ff3b6b',
  uiText:'#ffffff'
};

/* ---------- tuning constants (tweak here) ---------- */
const CFG = {
  W: 720, H: 1280,              // logical pixels (portrait)
  paddleW: 176, paddleH: 24, paddleY: 1156,
  paddleMinY: 640, paddleMaxY: 1196,
  paddleAccel: 0.40, paddleDrag: 0.90,
  paddleVmax: 56,
  slideFactor: 0.18,            // vertical movement imparted to ball on paddle hit
  angleFactor: 0.055,           // hit position -> horizontal angle
  ballR: 13,
  ballLaunch: 720,
  ballMin: 330, ballMax: 1180,
  flameCount: 2,                // trail points per frame ball 'flames'
  brickW: 86, brickH: 34, brickGap: 6,
  brickTop: 250, brickLeft: 40,
  rows: 8, cols: 7,
  pointsBase: 25,
  chargeRate: 0.28,             // power meter per successful hit
  maxCharge: 100,
  fireCost: 55,
  comboLinger: 2.6,             // s before combo decays
  comboDecay: 0.25,             // combo multiplier lost per decay step
  shake: { dur: 1.4, max: 9 },
  metro: { pts: 90, dur: 2.5, max: 430 },
  flux: { power: 5.5, dur: 9, max: 700 },
  pearl: { power: 5.0, dur: 8, max: 650 },
  cannon: { dur: 0.5, wait: 1.35, v: 1500, r: 7 },
  musicNoteMin: 30, musicNoteMax: 220
};
CFG.boardW = CFG.cols * (CFG.brickW + CFG.brickGap) - CFG.brickGap;
CFG.boardLeft = (CFG.W - CFG.boardW) / 2;
CFG.leftWall = 26;
CFG.rightWall = CFG.W - 26;

/* ---------- rng / helpers ---------- */
const RNG = (() => {
  let s = (Date.now() ^ 0x9e3779b9) >>> 0;
  return {
    seed(x) { s = (x >>> 0) || 1; },
    next() { s = (s * 1664525 + 1013904223) >>> 0; return s / 4294967296; },
    range(a, b) { return a + this.next() * (b - a); },
    int(a, b) { return Math.floor(this.range(a, b + 1)); },
    pick(arr) { return arr[Math.floor(this.next() * arr.length)]; },
    chance(p) { return this.next() < p; }
  };
})();

const clamp = (v, a, b) => v < a ? a : (v > b ? b : v);
const lerp = (a, b, t) => a + (b - a) * t;
const dist2 = (ax, ay, bx, by) => { const dx = ax - bx, dy = ay - by; return dx * dx + dy * dy; };
const TAU = Math.PI * 2;

/* ---------- color utils ---------- */
function hexToRgb(hex) {
  const h = hex.replace('#', '');
  return {
    r: parseInt(h.substring(0, 2), 16),
    g: parseInt(h.substring(2, 4), 16),
    b: parseInt(h.substring(4, 6), 16)
  };
}
function rgbStr(r, g, b, a = 1) {
  return `rgba(${r | 0},${g | 0},${b | 0},${a})`;
}
function lerpColor(a, b, t) {       // a,b: {r,g,b}
  return { r: lerp(a.r, b.r, t), g: lerp(a.g, b.g, t), b: lerp(a.b, b.b, t) };
}
function withAlpha(hex, a) {        // hex -> rgba string
  const c = hexToRgb(hex);
  return rgbStr(c.r, c.g, c.b, a);
}
/* cache of precomputed rgba strings for hot colors */
const CACHE = {};
function cc(hex, a) {               // cached rgba
  const key = hex + '|' + a;
  let v = CACHE[key];
  if (!v) { v = withAlpha(hex, a); CACHE[key] = v; }
  return v;
}
function cssGlow(hex, a, blur) { return `drop-shadow(0 0 ${blur}px ${withAlpha(hex, a)})`; }

/* ---------- deterministic level string -> bricks ---------- */
function parseLevelRows(rows, defs) {
  const bricks = [];
  for (let r = 0; r < rows.length; r++) {
    const line = rows[r];
    for (let c = 0; c < line.length; c++) {
      const ch = line[c];
      if (ch === '.' || ch === ' ') continue;
      if (!defs[ch]) continue;
      const bc = JSON.parse(JSON.stringify(defs[ch]));
      bc.x = CFG.brickLeft + c * (CFG.brickW + CFG.brickGap);
      bc.y = CFG.brickTop + r * (CFG.brickH + CFG.brickGap);
      bc.col = c; bc.row = r;
      bc.hp = Math.max(1, bc.hp | 0);
      bricks.push(bc);
    }
    const wrap = { cells: line.length };
    bricks._rows = rows.length;
    bricks._cols = Math.max(...rows.map(r => r.length));
  }
  return bricks;
}

/* ---------- entity classes ---------- */
class Ball {
  constructor(x, y, vx, vy, opts = {}) {
    this.x = x; this.y = y;
    this.vx = vx; this.vy = vy;
    this.r = opts.r || CFG.ballR;
    this.type = opts.type || 'normal';     // normal | flux | pearl | cannon
    this.hue = opts.hue || 48;
    this.trail = [];
    this.lastBrick = -1;
    this.flames = 0;                       // consecutive fast-wall-hit flame fuel
    this.deflected = false;
  }
  get speed() { return Math.hypot(this.vx, this.vy); }
  setSpeed(s) {
    const cur = this.speed || 1;
    const k = s / cur;
    this.vx *= k; this.vy *= k;
  }
  /* reflect velocity component off a normal (nx, ny) with restitution e */
  reflect(nx, ny, e = 1) {
    const dot = this.vx * nx + this.vy * ny;
    if (dot < 0) {
      this.vx -= (1 + e) * dot * nx;
      this.vy -= (1 + e) * dot * ny;
    }
  }
  step(dt) {
    const sp = this.speed;
    let fuel = 0;
    for (let i = 0; i < CFG.flameCount; i++) {
      this.trail.push({ x: this.x - this.vx * dt * i, y: this.y - this.vy * dt * i, life: 0.5 });
    }
    if (this.trail.length > 46) this.trail.splice(0, this.trail.length - 46);
    this.x += this.vx * dt;
    this.y += this.vy * dt;
    return fuel;
  }
}

class Powerup {
  constructor(x, y, type) {
    this.x = x; this.y = y;
    this.type = type;
    this.vy = 230;
    this.life = 8.5;
    this.t = RNG.next() * TAU;
    this.caught = false;
  }
  step(dt) {
    this.t += dt * 4;
    this.x += Math.sin(this.t * 1.3) * 26 * dt;
    this.y += this.vy * dt;
    this.life -= dt;
  }
  get color() {
    const map = {
      multi: '#ff5c8a', big: '#22d3ee', slow: '#4ade80',
      grab: '#ffd84d', flux: '#ff9d5c', pearl: '#5b8cff',
      bomb: '#ff3b6b', life: '#ffffff', mega: '#b05cff'
    };
    return map[this.type] || '#fff';
  }
  get icon() {
    const map = {
      multi: '●●●', big: '◄►', slow: '❄', grab: '◎',
      flux: '≈', pearl: '◈', bomb: '✸', life: '♥', mega: '★'
    };
    return map[this.type] || '?';
  }
  get label() {
    const map = {
      multi: 'MULTIBALL', big: 'WIDE PADDLE', slow: 'SLOW-MO',
      grab: 'GRABBER', flux: 'FLUX BALL', pearl: 'PEARL BALL',
      bomb: 'BOMB', life: 'EXTRA LIFE', mega: 'TEMPEST'
    };
    return map[this.type] || 'POWER';
  }
}

class Particle {
  constructor(x, y, vx, vy, life, size, color, opts = {}) {
    this.x = x; this.y = y;
    this.vx = vx; this.vy = vy;
    this.life = life; this.maxLife = life;
    this.size = size;
    this.color = color;
    this.grav = opts.grav || 0;
    this.drag = opts.drag || 1;
    this.glow = opts.glow !== undefined ? opts.glow : true;
    this.shock = opts.shock || false;
    this.shape = opts.shape || 'dot';
  }
  step(dt) {
    this.life -= dt;
    if (this.life <= 0) return false;
    this.vy += this.grav * dt;
    const d = Math.pow(this.drag, dt * 60);
    this.vx *= d; this.vy *= d;
    this.x += this.vx * dt;
    this.y += this.vy * dt;
    return true;
  }
}

/* ---------- the one big mutable state object ---------- */
const S = {
  state: 'boot',            // boot | menu | playing | pause | over | win
  modeCastle: false,        // true = hit-any-brick-victory mode
  muted: false,

  score: 0, hi: 0,
  lives: 3, bombs: 0,

  mr: 1, combo: 0, comboT: 0, basePts: 0,
  charge: 0, fireReady: false,
  comboPops: [],

  balls: [],
  bricks: [],
  brickMap: new Map(),
  powerups: [],
  paddle: { x: 0, w: CFG.paddleW, y: CFG.paddleY, vx: 0, vy: 0, target: null, grab: false, magnetT: 0, flash: 0 },
  cannon: null,             // {wait, dir, dy}
  mode: { flux: 0, pearl: 0, slow: 0, freeze: 0 },

  shake: 0, shakeDx: 0, shakeDy: 0,
  flash: 0, flashColor: [255,255,255],
  time: 0, dt: 0,
  level: 1, bricksLeft: 0,
  totals: { bricks: 0, balls: 0 },

  countdown: 0,            // >0 = serving between serves
  serveT: 0,
  overT: 0,
  winT: 0,
  nextBall: null,          // queue of waiting ball clones

  stars: [], shooting: [], neb: [],
  offscreen: [],           // reusable particle pool
  freezeFlash: 0
};

S.neb = Array.from({ length: 5 }, () => ({
  x: RNG.next() * CFG.W, y: RNG.next() * CFG.H,
  r: RNG.range(180, 420), hue: RNG.int(245, 285), a: RNG.range(0.05, 0.14), sp: RNG.range(4, 12)
}));
for (let i = 0; i < 60; i++) {
  S.stars.push({ x: RNG.next() * CFG.W, y: RNG.next() * CFG.H * 0.7, z: RNG.range(0.2, 1), tw: RNG.next() * TAU });
}
for (let i = 0; i < 12; i++) {
  S.shooting.push({ x: RNG.next() * CFG.W, y: RNG.next() * CFG.H, vx: RNG.range(60, 160), vy: RNG.range(30, 90), life: RNG.range(0.4, 1.4), tw: RNG.next() * TAU });
}
