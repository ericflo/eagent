/* ============================================================================
 * NEON OVERDRIVE — a modern Breakout
 * vanilla JS + Canvas 2D + WebAudio. No dependencies. Runs offline.
 * ----------------------------------------------------------------------------
 * SECTIONS:
 *  1. Utils & config          2. Audio (SFX + procedural music)
 *  3. Input (mouse/keys/touch-stick/gamepad)  4. Levels (hand-designed)
 *  5. Entities (paddle/balls/bricks/powerups) 6. FX (particles/rings/text)
 *  7. Game state & flow       8. Update (physics/collisions/overdrive)
 *  9. Render                 10. HUD/DOM & boot & main loop
 * ========================================================================== */
'use strict';

/* ============================ 1. UTILS & CONFIG ========================== */
const TAU = Math.PI * 2;
const clamp = (v, a, b) => v < a ? a : v > b ? b : v;
const lerp = (a, b, t) => a + (b - a) * t;
const rand = (a, b) => a + Math.random() * (b - a);
const randi = (a, b) => Math.floor(rand(a, b + 1));
const chance = p => Math.random() < p;
const pick = arr => arr[(Math.random() * arr.length) | 0];
const hyp = Math.hypot;

function fmtTime(sec) {
  sec = Math.max(0, Math.floor(sec));
  return Math.floor(sec / 60) + ':' + String(sec % 60).padStart(2, '0');
}
function fmtScore(n) { return n.toLocaleString('en-US'); }

const CONFIG = {
  step: 1 / 120,            // fixed timestep (s)
  baseBallSpeed: 560,       // px/s logical
  maxBallSpeed: 1350,
  minBallSpeed: 320,
  paddleW: 120, paddleH: 18,
  paddleZoneTop: 0.72,      // paddle may roam bottom 28% vertically
  powerupChance: 0.18,
  smashVy: -260,            // paddle upward velocity that counts as SMASH
  smashBoost: 130,          // bonus ball speed from smash
  odRampTime: 2.2,          // seconds in top-zone per multiplier doubling
  odMax: 32,
  lives: 3,
};

const canvas = document.getElementById('game');
const ctx = canvas.getContext('2d');
let W = 0, H = 0, DPR = 1;  // CSS-pixel playfield size

function resize() {
  DPR = Math.min(window.devicePixelRatio || 1, 2);
  W = window.innerWidth; H = window.innerHeight;
  canvas.width = Math.round(W * DPR);
  canvas.height = Math.round(H * DPR);
  canvas.style.width = W + 'px'; canvas.style.height = H + 'px';
  ctx.setTransform(DPR, 0, 0, DPR, 0, 0);
  layoutField();
  // keep the paddle inside its roam zone after rotation/resize:
  // paddle bounds go stale otherwise (minY/maxY were computed for old H)
  paddle.maxY = H - 56;
  paddle.minY = Math.max(8, Math.min(H * CONFIG.paddleZoneTop, paddle.maxY - 40));
  paddle.x = clamp(paddle.x, paddle.effW / 2, W - paddle.effW / 2);
  paddle.y = clamp(paddle.y, paddle.minY, paddle.maxY);
  paddle.targetX = clamp(paddle.targetX, paddle.effW / 2, W - paddle.effW / 2);
  paddle.targetY = clamp(paddle.targetY, paddle.minY, paddle.maxY);
}
window.addEventListener('resize', resize);
window.addEventListener('orientationchange', () => setTimeout(resize, 120));

/* Brick-field layout (recomputed on resize). Bricks live in top region. */
const field = {
  cols: 10, rows: 0, bw: 0, bh: 26, ox: 0, oy: 96,
  gap: 6, bottom: 0,
};
function layoutField() {
  const maxW = Math.min(W - 20, 860);
  field.cols = W < 480 ? 8 : 10;
  field.gap = W < 480 ? 5 : 6;
  field.bh = clamp(W * 0.032, 20, 28);
  field.bw = (maxW - field.gap * (field.cols - 1)) / field.cols;
  field.ox = (W - maxW) / 2;
  field.oy = clamp(H * 0.115, 84, 130);
  // reposition live bricks
  for (const b of bricks) {
    b.w = field.bw; b.h = field.bh;
    b.x = field.ox + b.col * (field.bw + field.gap);
    b.y = field.oy + b.row * (field.bh + field.gap);
  }
  field.bottom = field.oy + field.rows * (field.bh + field.gap);
}

/* ============================ 2. AUDIO =================================== */
/* All synthesized with WebAudio. Unlocked on first user gesture. */
const AudioSys = {
  ctx: null, master: null, musicGain: null, sfxGain: null,
  muted: false, musicOn: false, odLevel: 0,
  _beatTimer: 0, _step: 0, _nextNoteT: 0,

  init() {
    if (this.ctx) return;
    try {
      const AC = window.AudioContext || window.webkitAudioContext;
      this.ctx = new AC();
      this.master = this.ctx.createGain(); this.master.gain.value = 0.9;
      this.master.connect(this.ctx.destination);
      this.sfxGain = this.ctx.createGain(); this.sfxGain.gain.value = 0.9;
      this.sfxGain.connect(this.master);
      this.musicGain = this.ctx.createGain(); this.musicGain.gain.value = 0.30;
      this.musicGain.connect(this.master);
      this._nextNoteT = this.ctx.currentTime + 0.1;
    } catch (e) { /* no audio */ }
  },
  resume() {
    this.init();
    if (this.ctx && this.ctx.state === 'suspended') {
      try { const p = this.ctx.resume(); if (p && p.catch) p.catch(() => {}); } catch (e) { /* blocked */ }
    }
  },
  setMuted(m) {
    this.muted = m;
    if (this.master) this.master.gain.value = m ? 0 : 0.9;
    document.getElementById('btn-mute').textContent = m ? '🔇' : '🔊';
  },
  toggleMute() { this.resume(); this.setMuted(!this.muted); },

  // generic tone blip
  tone(freq, dur, type = 'sine', vol = 0.3, slideTo = null, delay = 0) {
    if (!this.ctx || this.muted) return;
    try {
    const t0 = this.ctx.currentTime + delay;
    const o = this.ctx.createOscillator(), g = this.ctx.createGain();
    o.type = type; o.frequency.setValueAtTime(freq, t0);
    if (slideTo) o.frequency.exponentialRampToValueAtTime(Math.max(1, slideTo), t0 + dur);
    g.gain.setValueAtTime(0.0001, t0);
    g.gain.exponentialRampToValueAtTime(vol, t0 + 0.008);
    g.gain.exponentialRampToValueAtTime(0.0001, t0 + dur);
    o.connect(g); g.connect(this.sfxGain);
    o.start(t0); o.stop(t0 + dur + 0.05);
    } catch (e) { /* audio blocked — stay silent */ }
  },
  noise(dur, vol = 0.25, filterFreq = 2000, delay = 0) {
    if (!this.ctx || this.muted) return;
    try {
    const t0 = this.ctx.currentTime + delay;
    const len = Math.max(1, (dur * this.ctx.sampleRate) | 0);
    const buf = this.ctx.createBuffer(1, len, this.ctx.sampleRate);
    const d = buf.getChannelData(0);
    for (let i = 0; i < len; i++) d[i] = (Math.random() * 2 - 1) * (1 - i / len);
    const src = this.ctx.createBufferSource(); src.buffer = buf;
    const f = this.ctx.createBiquadFilter(); f.type = 'lowpass'; f.frequency.value = filterFreq;
    const g = this.ctx.createGain(); g.gain.value = vol;
    src.connect(f); f.connect(g); g.connect(this.sfxGain);
    src.start(t0);
    } catch (e) { /* audio blocked — stay silent */ }
  },

  /* ---- named SFX ---- */
  paddle() { this.tone(220, 0.09, 'square', 0.16, 330); },
  launch() { this.tone(300, 0.16, 'sawtooth', 0.18, 700); },
  brick(combo) {
    const base = 380 * Math.pow(2, Math.min(combo, 16) / 12);
    this.tone(base, 0.12, 'square', 0.20, base * 1.5);
    this.tone(base * 2, 0.08, 'sine', 0.10);
  },
  deflect() { this.tone(140, 0.14, 'square', 0.20, 90); this.noise(0.08, 0.12, 900); },
  phaseMiss() { this.tone(520, 0.18, 'sine', 0.12, 180); },
  powerup() { [523, 659, 784, 1046].forEach((f, i) => this.tone(f, 0.12, 'square', 0.14, null, i * 0.055)); },
  smash() { this.noise(0.25, 0.35, 3500); this.tone(120, 0.28, 'sawtooth', 0.30, 480); },
  laser() { this.tone(1400, 0.14, 'sawtooth', 0.16, 220); },
  explode() { this.noise(0.5, 0.4, 1400); this.tone(90, 0.45, 'sine', 0.35, 35); },
  loseLife() { [400, 300, 220, 150].forEach((f, i) => this.tone(f, 0.18, 'sawtooth', 0.18, f * 0.8, i * 0.11)); },
  shieldSave() { this.tone(700, 0.2, 'sine', 0.25, 1400); this.tone(1050, 0.25, 'sine', 0.2, 2100, 0.08); },
  catchBall() { this.tone(500, 0.08, 'sine', 0.15, 750); },
  odRiser() {
    if (!this.ctx || this.muted) return;
    this.tone(110, 1.1, 'sawtooth', 0.30, 880);
    this.tone(220, 1.1, 'square', 0.12, 1760);
    this.noise(1.0, 0.12, 6000);
  },
  odDrop() { this.tone(600, 0.5, 'sawtooth', 0.2, 120); },
  fanfare() {
    [523, 659, 784, 1046, 784, 1046, 1318].forEach((f, i) =>
      this.tone(f, i === 6 ? 0.5 : 0.14, 'square', 0.16, null, i * 0.11));
  },
  gameOver() { [330, 262, 196, 131].forEach((f, i) => this.tone(f, 0.3, 'triangle', 0.22, null, i * 0.2)); },
  win() {
    [523, 659, 784, 1046, 1318, 1568, 2093].forEach((f, i) =>
      this.tone(f, 0.25, 'triangle', 0.2, null, i * 0.12));
  },

  /* ---- procedural music: bass + pad loop, intensifies in overdrive ---- */
  startMusic() { this.musicOn = true; },
  stopMusic() { this.musicOn = false; },
  updateMusic(dt) {
    if (!this.ctx || !this.musicOn || this.muted) return;
    this._beatTimer -= dt;
    const bpm = this.odLevel > 0 ? 148 : 104;
    const spb = 60 / bpm / 2; // 8th notes
    if (this._beatTimer > 0) return;
    this._beatTimer = spb;
    const roots = [55, 55, 65.4, 49]; // A A C G
    const bar = Math.floor(this._step / 8) % 4;
    const root = roots[bar];
    const s = this._step % 8;
    if (s % 2 === 0) this._mnote(root * 2, 0.22, 'sawtooth', 0.10);       // bass pulse
    if (this.odLevel > 0) {
      if (s % 2 === 1) this._mnote(root * 4, 0.1, 'square', 0.05);        // od sparkle
      if (s === 4) this._mnote(root * 6, 0.18, 'square', 0.05);
    } else if (s === 0) {
      this._mnote(root * 4, 0.9, 'triangle', 0.045);                     // pad
      this._mnote(root * 4 * 1.5, 0.9, 'triangle', 0.035);
    }
    this._step++;
  },
  _mnote(freq, dur, type, vol) {
    try {
    const t0 = this.ctx.currentTime;
    const o = this.ctx.createOscillator(), g = this.ctx.createGain();
    o.type = type; o.frequency.value = freq;
    g.gain.setValueAtTime(0.0001, t0);
    g.gain.exponentialRampToValueAtTime(vol, t0 + 0.02);
    g.gain.exponentialRampToValueAtTime(0.0001, t0 + dur);
    o.connect(g); g.connect(this.musicGain);
    o.start(t0); o.stop(t0 + dur + 0.05);
    } catch (e) { /* audio blocked — stay silent */ }
  },
};
/* ============================ 3. INPUT ===================================== */
/* Mouse follows, keys/arrows, touch direct-drag + floating thumbstick,
   gamepad polling. All input writes to `input` intent; paddle consumes it. */
const input = {
  mouseActive: false, mouseX: 0, mouseY: 0,
  keys: Object.create(null),
  touchMode: false,
  joyBaseX: 0, joyBaseY: 0, joyDX: 0, joyDY: 0, joyId: null, joyOn: false,
  dragId: null, dragOX: 0, dragOY: 0, // direct-drag touch
  gamepad: false,
  shootPressed: false, // edge-trigger consumed by update
};

const joyBaseEl = document.getElementById('joy-base');
const joyKnobEl = document.getElementById('joy-knob');
const touchHintEl = document.getElementById('touch-hint');

function showJoystick(bx, by, dx, dy) {
  joyBaseEl.classList.remove('hidden');
  joyBaseEl.style.left = bx + 'px'; joyBaseEl.style.top = by + 'px';
  joyKnobEl.style.transform = `translate(calc(-50% + ${dx}px), calc(-50% + ${dy}px))`;
}
function hideJoystick() { joyBaseEl.classList.add('hidden'); }

// --- mouse: paddle follows cursor while playing ---
canvas.addEventListener('mousemove', e => {
  input.mouseActive = true; input.mouseX = e.clientX; input.mouseY = e.clientY;
});
canvas.addEventListener('mousedown', e => {
  AudioSys.resume();
  input.mouseActive = true; input.mouseX = e.clientX; input.mouseY = e.clientY;
  input.shootPressed = true;
});

// --- keyboard ---
window.addEventListener('keydown', e => {
  if (['ArrowLeft','ArrowRight','ArrowUp','ArrowDown',' '].includes(e.key)) e.preventDefault();
  AudioSys.resume();
  if (e.repeat) return;
  input.keys[e.key.toLowerCase()] = true;
  // keys own the paddle until the mouse moves again — otherwise mouse-follow
  // snaps the paddle back the moment keys are released
  if (['arrowleft','arrowright','arrowup','arrowdown','a','d','w','s'].includes(e.key.toLowerCase())) {
    input.mouseActive = false; paddle.hasTarget = false;
  }
  if (e.key === ' ') input.shootPressed = true;
  if (e.key === 'p' || e.key === 'P' || e.key === 'Escape') togglePause();
  if (e.key === 'm' || e.key === 'M') AudioSys.toggleMute();
});
window.addEventListener('keyup', e => { input.keys[e.key.toLowerCase()] = false; });

// --- touch: first finger drags paddle directly; a held finger w/o much
// --- movement becomes a floating thumbstick driving paddle velocity.
canvas.addEventListener('touchstart', e => {
  e.preventDefault(); AudioSys.resume();
  input.touchMode = true;
  touchHintEl.classList.remove('hidden');
  clearTimeout(touchHintEl._t);
  touchHintEl._t = setTimeout(() => touchHintEl.classList.add('hidden'), 2600);
  for (const t of e.changedTouches) {
    if (input.joyId === null && game.state === 'playing') {
      // candidate thumbstick finger: base spawns where the thumb lands
      input.joyId = t.identifier;
      input.joyBaseX = t.clientX; input.joyBaseY = t.clientY;
      input.joyDX = 0; input.joyDY = 0; input.joyOn = true;
      showJoystick(t.clientX, t.clientY, 0, 0);
    } else if (input.dragId === null) {
      input.dragId = t.identifier;
      input.dragOX = t.clientX - paddle.x; input.dragOY = t.clientY - paddle.y;
    }
  }
  // any tap counts as shoot/launch intent
  input.shootPressed = true;
}, { passive: false });

canvas.addEventListener('touchmove', e => {
  e.preventDefault();
  for (const t of e.changedTouches) {
    if (t.identifier === input.joyId) {
      let dx = t.clientX - input.joyBaseX, dy = t.clientY - input.joyBaseY;
      const m = hyp(dx, dy), max = 60;
      if (m > max) { dx = dx / m * max; dy = dy / m * max; }
      input.joyDX = dx / max; input.joyDY = dy / max; // -1..1
      showJoystick(input.joyBaseX, input.joyBaseY, dx, dy);
      // direct-follow wins when finger travels far from base: glide the base
      if (m > 90) { input.joyBaseX = t.clientX - dx; input.joyBaseY = t.clientY - dy; }
    } else if (t.identifier === input.dragId) {
      paddle.targetX = clamp(t.clientX - input.dragOX, paddle.w / 2, W - paddle.w / 2);
      paddle.targetY = clamp(t.clientY - input.dragOY, paddle.minY, paddle.maxY);
      paddle.hasTarget = true;
    }
  }
}, { passive: false });

function touchEnd(e) {
  e.preventDefault();
  for (const t of e.changedTouches) {
    if (t.identifier === input.joyId) { input.joyId = null; input.joyOn = false; hideJoystick(); }
    if (t.identifier === input.dragId) input.dragId = null;
  }
  if (input.joyId === null && input.dragId === null) paddle.hasTarget = false;
}
canvas.addEventListener('touchend', touchEnd, { passive: false });
canvas.addEventListener('touchcancel', touchEnd, { passive: false });

// prevent scroll / pinch-zoom gestures globally
document.addEventListener('gesturestart', e => e.preventDefault());
document.addEventListener('dblclick', e => e.preventDefault());
document.addEventListener('contextmenu', e => { if (e.target === canvas) e.preventDefault(); });

// --- gamepad polling (left stick / dpad move, A launch) ---
let padPrevA = false;
function pollGamepad() {
  input.gamepad = false;
  try {
    const pads = navigator.getGamepads ? navigator.getGamepads() : [];
    for (const p of pads) {
      if (!p || !p.connected) continue;
      const ax = p.axes[0] || 0, ay = p.axes[1] || 0;
      const b = i => p.buttons[i] && p.buttons[i].pressed;
      if (Math.abs(ax) > 0.15 || Math.abs(ay) > 0.15 || b(14) || b(15) || b(12) || b(13)) {
        input.gamepad = true;
        paddle.padX = (Math.abs(ax) > 0.15 ? ax : 0) + (b(15) ? 1 : 0) - (b(14) ? 1 : 0);
        paddle.padY = (Math.abs(ay) > 0.15 ? ay : 0) + (b(13) ? 1 : 0) - (b(12) ? 1 : 0);
      }
      const a = b(0);
      if (a && !padPrevA) input.shootPressed = true;
      padPrevA = a;
    }
  } catch (e) { /* ignore */ }
}
/* ============================ 4. LEVELS ==================================== */
/* Legend: S=standard  A=angle-gate  V=speed-gate  L=slider  T=timed-phase
           O=bomb  X=mirror(unbreakable)  .=empty
   ALL bricks break in ONE good hit — difficulty comes from angle / speed /
   movement / timing, never HP. Levels funnel toward breakthrough-to-top. */
const BRICK_INFO = {
  S: { name: 'Standard',  score: 50,  color: '#00f6ff',
       desc: 'Breaks on any hit.' },
  A: { name: 'Vector ▸angle-gate◂', score: 100, color: '#b266ff',
       desc: 'Breaks only on STEEP hits (impact angle > ~35° from surface). Shallow = DEFLECTED.' },
  V: { name: 'Velocity ▸speed-gate◂', score: 100, color: '#ff9f1c',
       desc: 'Breaks only if ball is FAST (or SMASH / Heavy). Slow = dull bounce.' },
  L: { name: 'Slider', score: 100, color: '#a6ff00',
       desc: 'Slides side-to-side. Time your shot!' },
  T: { name: 'Phase ▸timed◂', score: 150, color: '#ff2fd6',
       desc: 'Cycles SOLID ⇄ GHOST. Hit it while solid — or be a Ghost yourself.' },
  O: { name: 'Bomb', score: 75, color: '#ff3b5c',
       desc: 'Explodes: destroys neighbors, chains to other bombs!' },
  X: { name: 'Mirror ⬢unbreakable⬢', score: 0, color: '#8fa3c7',
       desc: 'Never breaks. Perfect redirect — bank shots off it.' },
};

const LEVELS = [
  { title: 'FIRST CONTACT', tip: 'Punch a hole, then get the ball ABOVE the bricks for OVERDRIVE!',
    map: [
      '..........',
      '..SSSSSS..',
      '..SSSSSS..',
      '..SSSSSS..',
      '..........',
    ]},
  { title: 'VECTOR GATE', tip: 'Purple VECTOR bricks deflect shallow hits — attack them STEEPLY from below!',
    map: [
      '..........',
      '..AAAAAA..',
      '..SSSSSS..',
      '..SSSSSS..',
      '..SSSSSS..',
      '..........',
    ]},
  { title: 'NEED FOR SPEED', tip: 'Orange VELOCITY bricks need SPEED — smash upward into the ball or grab Heavy!',
    map: [
      '..........',
      '.SVVSSVVS.',
      '.SSSSSSSS.',
      '.SSOSSSSS.',
      '..SSSSSS..',
      '..........',
    ]},
  { title: 'MOVING TARGETS', tip: 'Green SLIDERS dodge you. Lead your shots — bombs help!',
    map: [
      '..........',
      '.LLLLLLLL.',
      '.SASSAASS.',
      '.SSVVVSSS.',
      '.SSOSSSOS.',
      '..SSSSSS..',
    ]},
  { title: 'PHASE SHIFT', tip: 'Pink PHASE bricks blink in and out. Time it — or go GHOST through everything!',
    map: [
      '..........',
      '.TTTTTTTT.',
      '.SASSSAS..',
      '.SSLVVLSS.',
      '.SSOSSVSS.',
      '.XSSSSSSX.',
    ]},
  { title: 'MIRROR MAZE', tip: 'Mirrors never break — bank the ball off them into the vault above!',
    map: [
      '..........',
      '.XTTTTTTX.',
      '.XAVVVAX..',
      '.XLS SOSX.',
      '.XSOSSSSX.',
      '.XSSSSSSX.',
    ]},
  { title: 'OVERDRIVE CORE', tip: 'Everything at once. Break through to the top and RIDE the ×32!',
    map: [
      '.OTTTTTTO.',
      '.LAAVVAAL.',
      '.SVLVVLVS.',
      '.SAVOOAVS.',
      '.SSLVVLS..',
      '.XSSSSSSX.',
      '..SSSSSS..',
    ]},
];
// fix level 6 row with accidental space
LEVELS[5].map[3] = '.XLSOSOSX.';

const POWER_INFO = [
  { id: 'expand',   name: 'Expand',    color: '#00f6ff', icon: '▬', desc: 'Wide paddle (14s).' },
  { id: 'multiball',name: 'Multiball ×3',color: '#a6ff00', icon: '⁂', desc: 'Each ball splits into 3.' },
  { id: 'fire',     name: 'Fireball',  color: '#ff6a00', icon: '🔥', desc: 'Burns THROUGH bricks (10s).' },
  { id: 'ghost',    name: 'Ghost Phase',color: '#c77dff', icon: '👻', desc: 'Passes through bricks (10s).' },
  { id: 'heavy',    name: 'Heavy',     color: '#ffd400', icon: '●', desc: 'Huge wrecking ball, smashes speed bricks (12s).' },
  { id: 'laser',    name: 'Laser',     color: '#ff3b5c', icon: '≋', desc: 'Tap/Space to fire, 8 shots.' },
  { id: 'magnet',   name: 'Magnet Catch',color: '#ff2fd6', icon: '🧲', desc: 'Catches ball — tap/Space to release, auto after 3s.' },
  { id: 'slow',     name: 'Slow-Mo',   color: '#7df9ff', icon: '◔', desc: 'Slows everything (8s).' },
  { id: 'shield',   name: 'Shield',    color: '#3dff8f', icon: '⛨', desc: 'One-miss safety net.' },
  { id: 'life',     name: 'Extra Life',color: '#ffffff', icon: '+1', desc: '+1 life, max 5.' },
];

/* ================= 5. ENTITIES (paddle/balls/bricks/powerups) ============= */
const paddle = {
  x: 0, y: 0, w: CONFIG.paddleW, h: CONFIG.paddleH,
  vx: 0, vy: 0, px: 0, py: 0, // previous pos for velocity
  minY: 0, maxY: 0,
  targetX: 0, targetY: 0, hasTarget: false,
  padX: 0, padY: 0,
  expandT: 0, laserShots: 0, magnetT: 0, slowT: 0,
  caught: [], // balls held by magnet {ball, fuse}
  squash: 0, // squash/stretch timer
  glow: 0,
  reset() {
    this.w = Math.min(CONFIG.paddleW, W * 0.28);
    this.h = CONFIG.paddleH;
    this.x = W / 2; this.maxY = H - 56; this.minY = H * CONFIG.paddleZoneTop;
    this.y = H - 110;
    this.vx = this.vy = 0; this.px = this.x; this.py = this.y;
    this.hasTarget = false; this.caught.length = 0;
    this.expandT = 0; this.laserShots = 0; this.magnetT = 0; this.slowT = 0;
    this.squash = 0; this.glow = 0;
    this.targetX = this.x; this.targetY = this.y;
  },
  get effW() { return this.expandT > 0 ? this.w * 1.6 : this.w; },
};

let balls = [];      // {x,y,vx,vy,r,mode,modeT,trail[],stuck,smashCD,ghostHit}
let bricks = [];     // {col,row,x,y,w,h,type,alive,phase,sliderDir,sliderSpd,flash,spawnT}
let powerups = [];   // {x,y,vy,type,info,rot}
let lasers = [];     // {x,y,vy}
let particles = [];  // {x,y,vx,vy,life,maxLife,size,color,grav,drag,shape}
let floaters = [];   // {x,y,vy,life,text,color,size}
let rings = [];      // {x,y,r,vr,life,maxLife,color,width}
let confetti = [];   // {x,y,vx,vy,rot,vr,life,color,w,h}

function spawnBall(x, y, angle = -Math.PI / 2 + rand(-0.4, 0.4), speed = CONFIG.baseBallSpeed, mode = 'normal', modeT = 0) {
  const heavy = mode === 'heavy';
  balls.push({
    x, y, vx: Math.cos(angle) * speed, vy: Math.sin(angle) * speed,
    r: heavy ? 13 : 8, speed0: speed,
    mode, modeT, trail: [], stuck: false, smashCD: 0, ghostCD: 0,
  });
}

function brickAt(col, row, type) {
  return {
    col, row, type, alive: true,
    x: field.ox + col * (field.bw + field.gap),
    y: field.oy + row * (field.bh + field.gap),
    w: field.bw, h: field.bh,
    phase: Math.random() * TAU,           // timed-phase cycle offset
    sliderDir: chance(0.5) ? 1 : -1,
    sliderSpd: rand(60, 130),
    sliderPhase: 0, sliderW: 1.5,       // set per-row in loadLevel (synced sway)
    flash: 0, spawnT: 0,
  };
}
/* ============================ 6. FX ======================================== */
let shake = 0, shakeX = 0, shakeY = 0;   // screen shake magnitude
let hitStop = 0;                          // micro-freeze timer
let timeScale = 1;
let flashVignette = 0;                    // white flash on big events
const stars = [];                         // parallax starfield
for (let i = 0; i < 130; i++) {
  stars.push({ x: Math.random(), y: Math.random(), z: rand(0.2, 1), tw: rand(0, TAU) });
}

function addShake(m) { shake = Math.min(26, shake + m); }
function addHitStop(t) { hitStop = Math.max(hitStop, t); }
function addRing(x, y, color, vr = 420, width = 4) {
  rings.push({ x, y, r: 6, vr, life: 0.5, maxLife: 0.5, color, width });
  if (rings.length > 40) rings.shift();
}
function burst(x, y, color, n = 14, spd = 320, life = 0.7, size = 4) {
  for (let i = 0; i < n; i++) {
    const a = rand(0, TAU), s = rand(spd * 0.25, spd);
    particles.push({
      x, y, vx: Math.cos(a) * s, vy: Math.sin(a) * s,
      life: rand(life * 0.5, life), maxLife: life,
      size: rand(size * 0.5, size * 1.3), color,
      grav: 500, drag: 0.98, shape: chance(0.3) ? 'rect' : 'circ',
    });
  }
  if (particles.length > 900) particles.splice(0, particles.length - 900);
}
function sparks(x, y, color, n = 6) { burst(x, y, color, n, 200, 0.4, 3); }
function addFloater(x, y, text, color = '#fff', size = 16) {
  floaters.push({ x, y, vy: -90, life: 1.0, text, color, size });
  if (floaters.length > 40) floaters.shift();
}
function shatterBrick(b) {
  const info = BRICK_INFO[b.type];
  burst(b.x + b.w / 2, b.y + b.h / 2, info.color, 18, 380, 0.8, 5);
  burst(b.x + b.w / 2, b.y + b.h / 2, '#ffffff', 6, 260, 0.4, 3);
  addRing(b.x + b.w / 2, b.y + b.h / 2, info.color, 380, 3);
}
function toast(text, cls = '') {
  const wrap = document.getElementById('toast-wrap');
  const el = document.createElement('div');
  el.className = 'toast ' + cls; el.textContent = text;
  wrap.appendChild(el);
  setTimeout(() => el.remove(), 1700);
  while (wrap.children.length > 3) wrap.firstChild.remove();
}
function spawnConfetti() {
  for (let i = 0; i < 160; i++) {
    confetti.push({
      x: rand(0, W), y: rand(-H * 0.4, -10), vx: rand(-80, 80), vy: rand(120, 420),
      rot: rand(0, TAU), vr: rand(-8, 8), life: rand(1.6, 3.2),
      color: pick(['#00f6ff', '#ff2fd6', '#a6ff00', '#ffd400', '#ff3b5c', '#ffffff']),
      w: rand(5, 10), h: rand(3, 6),
    });
  }
}

/* ====================== 7. GAME STATE & FLOW ============================= */
// localStorage may throw on access (private mode / blocked storage) —
// a throw here would kill the whole script at load, so read defensively.
function readHigh() {
  try {
    const v = parseInt(localStorage.getItem('neon-overdrive-high') || '0', 10);
    return v || 0;
  } catch (e) { return 0; }
}
const game = {
  state: 'menu', // menu | intro | playing | paused | clear | over | win
  level: 1, score: 0, lives: CONFIG.lives,
  high: readHigh(),
  mult: 1, combo: 0, bestCombo: 0, maxMult: 1,
  overdrive: false, odTime: 0, odStep: 0, odCool: 0,
  shield: false,
  levelScore: 0, levelTime: 0, serveBall: true,
  elapsed: 0,
};

function saveHigh() {
  if (game.score > game.high) {
    game.high = game.score;
    try { localStorage.setItem('neon-overdrive-high', String(game.high)); } catch (e) {}
    return true;
  }
  return false;
}

function loadLevel(idx) {
  const lv = LEVELS[idx - 1];
  bricks = []; powerups = []; lasers = [];
  paddle.caught.length = 0;
  field.rows = lv.map.length;
  const cols = field.cols;
  for (let r = 0; r < lv.map.length; r++) {
    const rowStr = lv.map[r];
    for (let c = 0; c < cols; c++) {
      const ch = rowStr[c] || '.';
      if (ch === '.' || ch === ' ') continue;
      const type = (ch === 'S' ? 'S' : ch);
      if (!BRICK_INFO[type]) continue;
      const b = brickAt(c, r, type);
      b.spawnT = r * 0.05 + c * 0.012; // cascade-in animation
      bricks.push(b);
    }
  }
  // sync sliders per row: shared phase+speed so same-row sliders sway in
  // formation. Each slider's amplitude is limited by the nearest static
  // (non-slider) brick in its row, so a swaying slider never covers a
  // neighbor: full ±0.45 cell where the row is all sliders (L4), down to a
  // ±half-gap shimmer when boxed in by static bricks (L7 mixed rows).
  const rowPhase = {}, rowW = {};
  const cellW = field.bw + field.gap, halfGap = field.gap / 2;
  const staticColsByRow = {};
  for (const b of bricks) {
    if (b.type === 'L') continue;
    (staticColsByRow[b.row] = staticColsByRow[b.row] || []).push(b.col);
  }
  for (const b of bricks) {
    if (b.type !== 'L') continue;
    if (!(b.row in rowPhase)) { rowPhase[b.row] = rand(0, TAU); rowW[b.row] = rand(1.2, 2.0); }
    b.sliderPhase = rowPhase[b.row]; b.sliderW = rowW[b.row];
    let free = 10; // cells of slider/empty space around b.col before a static brick
    for (const sc of (staticColsByRow[b.row] || [])) {
      const d = Math.abs(sc - b.col) - 1; // 0 = directly adjacent
      if (d < free) free = d;
    }
    b.sliderAmp = Math.min(0.45 * cellW, Math.max(halfGap, free * cellW + halfGap));
  }
  field.bottom = field.oy + field.rows * (field.bh + field.gap);
  balls = [];
  game.serveBall = true;
  game.odTime = 0; game.odStep = 0; game.overdrive = false;
  game.combo = 0; game.mult = 1;
  game.levelTime = 0; game.levelScore = 0;
  paddle.reset();
  spawnBall(paddle.x, paddle.y - 20, -Math.PI / 2, CONFIG.baseBallSpeed);
  balls[0].stuck = true; // starts held until launch
}

function breakableLeft() { return bricks.filter(b => b.alive && b.type !== 'X').length; }

function startGame() {
  AudioSys.resume(); AudioSys.startMusic();
  game.level = 1; game.score = 0; game.lives = CONFIG.lives;
  game.maxMult = 1; game.bestCombo = 0;
  showLevelCard(1, () => { game.state = 'playing'; syncUI(); });
}
function nextLevel() {
  game.level++;
  if (game.level > LEVELS.length) { winGame(); return; }
  showLevelCard(game.level, () => { game.state = 'playing'; syncUI(); });
}
function levelClear() {
  if (game.state !== 'playing') return; // guard double-trigger
  game.state = 'clear';
  AudioSys.fanfare();
  spawnConfetti();
  addShake(8);
  flashVignette = 0.7;
  saveHigh();
  game.maxMult = Math.max(game.maxMult, game.mult);
  document.getElementById('clear-lvl').textContent = game.level;
  document.getElementById('clear-score').textContent = fmtScore(game.levelScore);
  document.getElementById('clear-mult').textContent = '×' + game.maxMult;
  document.getElementById('clear-time').textContent = fmtTime(game.levelTime);
  show('clear-ov'); syncUI();
}
function loseLife() {
  AudioSys.loseLife();
  game.combo = 0; game.mult = 1;
  game.lives--;
  addShake(10);
  burst(paddle.x, paddle.y, '#ff3b5c', 30, 420, 0.8, 5);
  saveHigh(); syncUI();
  if (game.lives <= 0) { gameOver(); return; }
  // re-serve
  balls = [];
  game.serveBall = true;
  paddle.magnetT = 0; paddle.caught.length = 0;
  paddle.expandT = 0; paddle.laserShots = 0;
  spawnBall(paddle.x, paddle.y - 20, -Math.PI / 2 + rand(-0.3, 0.3), CONFIG.baseBallSpeed);
  balls[0].stuck = true;
}
function gameOver() {
  if (game.state !== 'playing') return; // guard double-trigger
  game.state = 'over';
  AudioSys.gameOver(); AudioSys.stopMusic();
  const nb = saveHigh();
  document.getElementById('over-score').textContent = fmtScore(game.score);
  document.getElementById('over-high').textContent = fmtScore(game.high);
  document.getElementById('over-level').textContent = game.level;
  document.getElementById('over-newbest').textContent = nb ? '★ YES ★' : '—';
  show('over-ov'); syncUI();
}
function winGame() {
  if (game.state === 'win' || game.state === 'over') return; // guard double-trigger
  game.state = 'win';
  AudioSys.win(); AudioSys.stopMusic();
  saveHigh();
  spawnConfetti(); spawnConfetti();
  document.getElementById('win-score').textContent = fmtScore(game.score);
  document.getElementById('win-high').textContent = fmtScore(game.high);
  show('win-ov'); syncUI();
}
function togglePause() {
  if (game.state === 'playing') {
    game.state = 'paused'; show('pause-ov');
  } else if (game.state === 'paused') {
    game.state = 'playing'; hide('pause-ov'); syncUI();
  }
}
// auto-pause when the tab is hidden or the window loses focus
// (mobile app-switch, calls, notifications) so the game never runs unseen
document.addEventListener('visibilitychange', () => {
  if (document.hidden && game.state === 'playing') togglePause();
});
window.addEventListener('blur', () => {
  if (game.state === 'playing') togglePause();
});
/* ====================== 8. UPDATE ========================================== */
function update(dt) {
  // dt: fixed step, already scaled by slow-mo where needed
  game.elapsed += dt;
  AudioSys.odLevel = game.overdrive ? game.odStep + 1 : 0;
  AudioSys.updateMusic(dt);

  updatePaddle(dt);
  updateBalls(dt);
  updateBricks(dt);
  updatePowerups(dt);
  updateLasers(dt);
  updateOverdrive(dt);
  updateFx(dt);

  if (input.shootPressed) { handleShoot(); input.shootPressed = false; }
  pollGamepad();

  game.levelTime += dt;
  if (breakableLeft() <= 0 && game.state === 'playing') levelClear();
}

/* ---- paddle: keyboard velocity + mouse follow + touch stick/drag + pad --- */
function updatePaddle(dt) {
  paddle.px = paddle.x; paddle.py = paddle.y;
  const SPD = Math.max(620, W * 1.1);
  let kvx = 0, kvy = 0;
  const k = input.keys;
  if (k['arrowleft'] || k['a']) kvx -= 1;
  if (k['arrowright'] || k['d']) kvx += 1;
  if (k['arrowup'] || k['w']) kvy -= 1;
  if (k['arrowdown'] || k['s']) kvy += 1;

  let vx = kvx * SPD, vy = kvy * SPD;

  if (input.joyOn) { // floating thumbstick drives velocity
    vx += input.joyDX * SPD; vy += input.joyDY * SPD * 0.8;
  }
  if (paddle.padX || paddle.padY) { vx += paddle.padX * SPD; vy += paddle.padY * SPD * 0.8; paddle.padX = paddle.padY = 0; }

  if (paddle.hasTarget && !input.joyOn && kvx === 0 && kvy === 0 && !paddle.padX) {
    // mouse / direct-drag follow: critically-damped approach
    const s = 1 - Math.pow(0.0001, dt);
    paddle.x = lerp(paddle.x, paddle.targetX, s);
    paddle.y = lerp(paddle.y, paddle.targetY, s);
  } else if (input.mouseActive && !input.touchMode && kvx === 0 && kvy === 0 && !input.joyOn && game.state === 'playing') {
    const s = 1 - Math.pow(0.00001, dt);
    paddle.x = lerp(paddle.x, clamp(input.mouseX, paddle.effW / 2, W - paddle.effW / 2), s);
    paddle.y = lerp(paddle.y, clamp(input.mouseY, paddle.minY, paddle.maxY), s * 0.8);
  } else {
    paddle.x += vx * dt; paddle.y += vy * dt;
  }

  paddle.x = clamp(paddle.x, paddle.effW / 2, W - paddle.effW / 2);
  paddle.y = clamp(paddle.y, paddle.minY, paddle.maxY);

  paddle.vx = (paddle.x - paddle.px) / dt;
  paddle.vy = (paddle.y - paddle.py) / dt;

  // timers
  if (paddle.expandT > 0) paddle.expandT -= dt;
  if (paddle.magnetT > 0) paddle.magnetT -= dt;
  if (paddle.slowT > 0) paddle.slowT -= dt;
  if (paddle.squash > 0) paddle.squash -= dt;
  if (paddle.glow > 0) paddle.glow -= dt;

  // magnet-held balls ride the paddle; auto-release after fuse
  for (let i = paddle.caught.length - 1; i >= 0; i--) {
    const c = paddle.caught[i];
    c.ball.x = paddle.x + c.ox; c.ball.y = paddle.y - paddle.h / 2 - c.ball.r - 2;
    c.ball.vx = 0; c.ball.vy = 0;
    c.fuse -= dt;
    if (c.fuse <= 0) { releaseCatch(i, true); }
  }
}

function launchStuck() {
  let launched = false;
  for (const b of balls) {
    if (b.stuck && !paddle.caught.some(c => c.ball === b)) {
      b.stuck = false;
      const a = -Math.PI / 2 + rand(-0.35, 0.35);
      const sp = CONFIG.baseBallSpeed;
      b.vx = Math.cos(a) * sp; b.vy = Math.sin(a) * sp;
      launched = true;
    }
  }
  const hadCatch = paddle.caught.length > 0;
  for (let i = paddle.caught.length - 1; i >= 0; i--) releaseCatch(i, true);
  if (launched || hadCatch) { AudioSys.launch(); game.serveBall = false; }
}
function releaseCatch(i, auto) {
  const c = paddle.caught[i];
  paddle.caught.splice(i, 1);
  const b = c.ball;
  b.stuck = false;
  const a = -Math.PI / 2 + rand(-0.3, 0.3);
  const sp = Math.max(CONFIG.baseBallSpeed, hyp(b.vx, b.vy) || CONFIG.baseBallSpeed);
  b.vx = Math.cos(a) * sp; b.vy = Math.sin(a) * sp * (auto ? 1 : 1);
  if (!auto) { AudioSys.launch(); addRing(b.x, b.y, '#ff2fd6'); }
}

function handleShoot() {
  if (game.state !== 'playing') return;
  // laser fire takes priority
  if (paddle.laserShots > 0 && balls.some(b => !b.stuck)) {
    paddle.laserShots -= 2;
    if (paddle.laserShots < 0) paddle.laserShots = 0;
    lasers.push({ x: paddle.x - paddle.effW / 4, y: paddle.y - 16, vy: -1100 });
    lasers.push({ x: paddle.x + paddle.effW / 4, y: paddle.y - 16, vy: -1100 });
    AudioSys.laser();
    addShake(2);
    paddle.squash = 0.12;
    return;
  }
  // otherwise: launch / release
  if (game.serveBall || balls.some(b => b.stuck) || paddle.caught.length) launchStuck();
}

/* ---- balls ---- */
function ballSpeed(b) { return hyp(b.vx, b.vy); }
function setBallSpeed(b, s) {
  const c = ballSpeed(b) || 1;
  b.vx = b.vx / c * s; b.vy = b.vy / c * s;
}

function updateBalls(dt) {
  for (let i = balls.length - 1; i >= 0; i--) {
    const b = balls[i];
    // magnet-held balls are positioned by updatePaddle; skip here
    if (b.stuck) {
      if (!paddle.caught.some(c => c.ball === b)) {
        b.x = paddle.x; b.y = paddle.y - paddle.h / 2 - b.r - 2;
      }
      continue;
    }
    if (b.smashCD > 0) b.smashCD -= dt;
    if (b.ghostCD > 0) b.ghostCD -= dt;
    if (b.modeT > 0) {
      b.modeT -= dt;
      if (b.modeT <= 0) {
        b.mode = 'normal';
        if (b.r > 9) b.r = 8;
        addRing(b.x, b.y, '#ffffff');
      }
    }
    // trail
    b.trail.push({ x: b.x, y: b.y });
    const maxTrail = b.mode === 'normal' ? 12 : 22;
    if (b.trail.length > maxTrail) b.trail.shift();

    // substeps for fast balls so they can't tunnel bricks
    const sp = ballSpeed(b);
    const steps = Math.min(4, Math.max(1, Math.ceil(sp * dt / (b.r + 6))));
    const sdt = dt / steps;
    for (let s = 0; s < steps; s++) {
      b.x += b.vx * sdt; b.y += b.vy * sdt;
      collideWalls(b);
      collidePaddle(b);
      collideBricks(b);
      if (b.dead) break;
    }
    if (b.dead) { balls.splice(i, 1); continue; }

    // gentle speed regulation toward a target that grows with overdrive
    const target = clamp(CONFIG.baseBallSpeed + game.odStep * 40, CONFIG.baseBallSpeed, CONFIG.maxBallSpeed);
    const cur = ballSpeed(b);
    if (cur > CONFIG.maxBallSpeed) setBallSpeed(b, lerp(cur, CONFIG.maxBallSpeed, 0.06));
    else if (cur < CONFIG.minBallSpeed && !b.stuck) setBallSpeed(b, lerp(cur, target, 0.02));

    // fell below screen
    if (b.y - b.r > H + 10) {
      balls.splice(i, 1);
      burst(b.x, H - 20, '#ff3b5c', 16, 300, 0.6, 4);
    }
  }
  if (balls.length === 0 && game.state === 'playing') {
    // last brick + last ball on the same frame is a WIN, not a life loss
    if (breakableLeft() <= 0) return;
    if (paddle.caught.length === 0) {
      if (game.shield) { // safety net saves it
        game.shield = false;
        AudioSys.shieldSave();
        toast('SHIELD SAVE!', 'gold');
        addRing(W / 2, H - 30, '#3dff8f', 700, 6);
        spawnBall(paddle.x, paddle.y - 24, -Math.PI / 2, CONFIG.baseBallSpeed);
        balls[0].stuck = true; game.serveBall = true;
      } else {
        loseLife();
      }
    }
  }
}

function collideWalls(b) {
  if (b.x - b.r < 0) { b.x = b.r; b.vx = Math.abs(b.vx); sparks(b.x, b.y, '#00f6ff'); AudioSys.paddle(); }
  if (b.x + b.r > W) { b.x = W - b.r; b.vx = -Math.abs(b.vx); sparks(b.x, b.y, '#00f6ff'); AudioSys.paddle(); }
  if (b.y - b.r < 0) { b.y = b.r; b.vy = Math.abs(b.vy); sparks(b.x, b.y, '#00f6ff'); AudioSys.paddle(); }
}

/* Paddle bounce: angle from hit position + paddle velocity; upward paddle
   velocity injects speed (SMASH). */
function collidePaddle(b) {
  const pw = paddle.effW / 2, ph = paddle.h / 2;
  if (b.vy <= 0 && b.y < paddle.y - ph) return; // only when coming down onto it... or side hits
  const dx = b.x - paddle.x, dy = b.y - paddle.y;
  if (Math.abs(dx) > pw + b.r || Math.abs(dy) > ph + b.r) return;
  // must be moving toward paddle
  if (b.vy < 0 && dy < -ph) return;

  const rel = clamp(dx / (pw + b.r * 0.5), -1, 1);
  const maxA = Math.PI * 0.42; // max deflection from vertical
  const ang = -Math.PI / 2 + rel * maxA;
  let sp = ballSpeed(b);
  sp = clamp(sp + Math.abs(paddle.vx) * 0.08, CONFIG.minBallSpeed, CONFIG.maxBallSpeed);
  // paddle horizontal velocity imparts english
  let nx = Math.cos(ang) * sp + paddle.vx * 0.25;
  let ny = Math.sin(ang) * sp;
  // SMASH: paddle moving up at impact adds speed + juice
  const smashing = paddle.vy < CONFIG.smashVy && b.smashCD <= 0;
  if (smashing) {
    const boost = clamp(-paddle.vy * 0.35, 60, 260) + CONFIG.smashBoost * 0.4;
    sp = clamp(sp + boost, sp, CONFIG.maxBallSpeed);
    const m = hyp(nx, ny) || 1;
    nx = nx / m * sp; ny = Math.min(ny / m * sp, -sp * 0.55);
    b.smashCD = 0.4;
    onSmash(b);
  } else {
    const m = hyp(nx, ny) || 1;
    nx = nx / m * sp; ny = ny / m * sp;
    if (ny > -sp * 0.25) ny = -sp * 0.25; // never flat
  }
  b.vx = nx; b.vy = ny;
  b.y = paddle.y - ph - b.r - 1;

  // magnet catch
  if (paddle.magnetT > 0 && paddle.caught.length < 3 && !smashing) {
    b.stuck = true;
    paddle.caught.push({ ball: b, ox: clamp(dx, -pw, pw), fuse: 3 });
    AudioSys.catchBall();
    addRing(b.x, b.y, '#ff2fd6');
    // remove from free balls list bookkeeping: keep in balls[] but stuck
    return;
  }

  paddle.squash = 0.14; paddle.glow = 0.25;
  // NOTE: combo intentionally NOT reset here — it only resets on ball loss
  // (spec: "combo increments per brick without losing ball").
  AudioSys.paddle();
  sparks(b.x, b.y + b.r, '#00f6ff', 5);
}

function onSmash(b) {
  AudioSys.smash();
  addShake(7); addHitStop(0.05); flashVignette = Math.max(flashVignette, 0.25);
  addRing(b.x, b.y, '#ffd400', 600, 5);
  burst(b.x, b.y, '#ffd400', 16, 420, 0.6, 5);
  addFloater(b.x, b.y - 26, 'SMASH!', '#ffd400', 24);
  toast('SMASH!', 'gold');
}
/* ---- brick collisions incl. angle/speed gates, sliders, phase, bombs ---- */
function isPhaseSolid(b, t) {
  if (b.type !== 'T') return true;
  const cyc = (t * 0.9 + b.phase) % TAU;
  return cyc < Math.PI * 1.1; // solid ~55% of cycle
}

function collideBricks(b) {
  for (const br of bricks) {
    if (!br.alive) continue;
    if (br.type === 'T' && !isPhaseSolid(br, game.elapsed)) {
      // ghost-phase: pass through (unless ball itself is ghost/fire which eats it anyway)
      if (b.mode !== 'ghost' && b.mode !== 'fire') continue;
    }
    // circle vs AABB
    const nx = clamp(b.x, br.x, br.x + br.w);
    const ny = clamp(b.y, br.y, br.y + br.h);
    let dx = b.x - nx, dy = b.y - ny;
    if (dx * dx + dy * dy > b.r * b.r) continue;

    const cx = br.x + br.w / 2, cy = br.y + br.h / 2;
    // contact normal: use min-penetration axis for stability
    const ox = (br.w / 2 + b.r) - Math.abs(b.x - cx);
    const oy = (br.h / 2 + b.r) - Math.abs(b.y - cy);
    let nX = 0, nY = 0;
    if (ox < oy) { nX = (b.x < cx ? -1 : 1); b.x += nX * ox; }
    else { nY = (b.y < cy ? -1 : 1); b.y += nY * oy; }
    const vInto = b.vx * nX + b.vy * nY;

    // pass-through modes: destroy & continue without bouncing
    if (b.mode === 'fire') { destroyBrick(br, b, true); addHitStop(0.02); continue; }
    if (b.mode === 'ghost' && b.ghostCD <= 0) {
      destroyBrick(br, b, true); b.ghostCD = 0.03; continue;
    }

    // VECTOR angle-gate: needs steep impact vs the surface normal
    if (br.type === 'A') {
      const sp = ballSpeed(b);
      const steep = sp > 1 ? Math.abs(vInto) / sp : 0; // cos(incidence)
      if (steep < 0.57) { // shallower than ~55° from surface → DEFLECTED
        reflect(b, nX, nY);
        br.flash = 0.35;
        AudioSys.deflect();
        addFloater(cx, cy - 8, 'DEFLECTED!', '#b266ff', 15);
        sparks(b.x, b.y, '#b266ff', 6);
        addHintOnce('angle');
        continue;
      }
    }
    // VELOCITY speed-gate: needs ball speed above threshold (Heavy always smashes)
    if (br.type === 'V') {
      const sp = ballSpeed(b);
      const need = CONFIG.baseBallSpeed * 1.02;
      const ok = sp > need || b.mode === 'heavy' || paddle.vy < CONFIG.smashVy;
      if (!ok) {
        reflect(b, nX, nY);
        setBallSpeed(b, Math.max(sp * 0.94, CONFIG.minBallSpeed));
        br.flash = 0.35;
        AudioSys.deflect();
        addFloater(cx, cy - 8, 'TOO SLOW!', '#ff9f1c', 15);
        sparks(b.x, b.y, '#ff9f1c', 6);
        addHintOnce('speed');
        continue;
      }
    }
    // MIRROR: unbreakable redirect with satisfying ping
    if (br.type === 'X') {
      reflect(b, nX, nY);
      setBallSpeed(b, Math.min(ballSpeed(b) * 1.04, CONFIG.maxBallSpeed));
      br.flash = 0.4;
      AudioSys.tone(880, 0.1, 'sine', 0.18, 1320);
      addRing(cx, cy, '#8fa3c7', 300, 3);
      sparks(b.x, b.y, '#8fa3c7', 5);
      continue;
    }

    // normal break: reflect + destroy
    reflect(b, nX, nY);
    destroyBrick(br, b, false);
    addHitStop(br.type === 'O' ? 0.06 : 0.018);
  }
}

function reflect(b, nX, nY) {
  if (nX !== 0) b.vx = (nX > 0 ? Math.abs(b.vx) : -Math.abs(b.vx));
  if (nY !== 0) b.vy = (nY > 0 ? Math.abs(b.vy) : -Math.abs(b.vy));
  // de-flat: avoid horizontal lock
  const sp = ballSpeed(b);
  if (sp > 1 && Math.abs(b.vy) < sp * 0.18) {
    b.vy += (b.vy >= 0 ? 1 : -1) * sp * 0.22;
    const m = hyp(b.vx, b.vy); b.vx = b.vx / m * sp; b.vy = b.vy / m * sp;
  }
}

const hintState = {};
function addHintOnce(kind) {
  const now = game.elapsed;
  if (hintState[kind] && now - hintState[kind] < 6) return;
  hintState[kind] = now;
  if (kind === 'angle') toast('HIT VECTORS STEEPLY!', 'pink');
  if (kind === 'speed') toast('MORE SPEED NEEDED!', 'gold');
}

function destroyBrick(br, ball, through) {
  if (!br.alive) return;
  br.alive = false;
  const info = BRICK_INFO[br.type];
  const cx = br.x + br.w / 2, cy = br.y + br.h / 2;
  shatterBrick(br);
  addShake(br.type === 'O' ? 9 : through ? 3 : 4);
  game.combo++;
  game.bestCombo = Math.max(game.bestCombo, game.combo);
  const pts = info.score * game.mult;
  game.score += pts; game.levelScore += pts;
  addFloater(cx, cy, '+' + pts, info.color, game.mult >= 8 ? 22 : 15);
  AudioSys.brick(game.combo);
  if (game.combo > 0 && game.combo % 10 === 0) {
    toast(game.combo + ' COMBO!', 'gold');
    AudioSys.powerup();
  }
  // Heavy ball smashing feels massive
  if (ball && ball.mode === 'heavy') { addShake(5); }
  // Bomb chains
  if (br.type === 'O') explodeBomb(br, ball);
  // powerup drop
  if (br.type !== 'X' && chance(CONFIG.powerupChance) && powerups.length < 6) {
    dropPowerup(cx, cy);
  }
}

function explodeBomb(br, ball) {
  const cx = br.x + br.w / 2, cy = br.y + br.h / 2;
  AudioSys.explode();
  addRing(cx, cy, '#ff3b5c', 700, 7);
  addRing(cx, cy, '#ffd400', 480, 4);
  burst(cx, cy, '#ff3b5c', 30, 520, 0.9, 6);
  burst(cx, cy, '#ffd400', 18, 420, 0.7, 5);
  flashVignette = Math.max(flashVignette, 0.3);
  const R = Math.max(field.bw, field.bh) * 2.2;
  for (const o of bricks) {
    if (!o.alive || o === br) continue;
    const ocx = o.x + o.w / 2, ocy = o.y + o.h / 2;
    if (hyp(ocx - cx, ocy - cy) < R) {
      if (o.type === 'X') { o.flash = 0.4; continue; }
      if (o.type === 'O' && chance(0.75)) { // chain with slight delay feel via flash
        const chained = o;
        setTimeout(() => { if (chained.alive && game.state === 'playing') destroyBrick(chained, ball, true); }, 120);
      } else {
        destroyBrick(o, ball, true);
      }
    }
  }
}

/* ---- bricks ambient update: sliders move, flashes decay ---- */
function updateBricks(dt) {
  const t = game.elapsed;
  for (const b of bricks) {
    if (!b.alive) continue;
    if (b.flash > 0) b.flash -= dt;
    if (b.spawnT > 0) b.spawnT -= dt;
    if (b.type === 'L') {
      // formation sway: whole row shares phase+speed (see loadLevel), and
      // per-brick amplitude stops at static neighbors — no overlap, ever.
      // Peak speed ≈ amp*W ≈ 60–100 px/s on open rows.
      const home = field.ox + b.col * (field.bw + field.gap);
      const range = b.sliderAmp || 0;
      b.x = clamp(home + Math.sin(game.elapsed * b.sliderW + b.sliderPhase) * range,
        4, W - b.w - 4);
    }
  }
}

/* ---- powerups ---- */
function dropPowerup(x, y) {
  const info = pick(POWER_INFO);
  powerups.push({ x, y, vy: rand(130, 190), type: info.id, info, rot: 0 });
}
function updatePowerups(dt) {
  for (let i = powerups.length - 1; i >= 0; i--) {
    const p = powerups[i];
    p.y += p.vy * dt; p.rot += dt * 3;
    // caught by paddle?
    const pw = paddle.effW / 2;
    if (p.y + 12 > paddle.y - paddle.h / 2 && p.y - 12 < paddle.y + paddle.h / 2 &&
        Math.abs(p.x - paddle.x) < pw + 14) {
      applyPowerup(p.type);
      AudioSys.powerup();
      addRing(p.x, p.y, p.info.color, 320, 4);
      burst(p.x, p.y, p.info.color, 12, 260, 0.5, 4);
      addFloater(p.x, p.y - 14, p.info.name.toUpperCase() + '!', p.info.color, 17);
      powerups.splice(i, 1);
      continue;
    }
    if (p.y > H + 20) powerups.splice(i, 1);
  }
}
function applyPowerup(type) {
  const free = balls.filter(b => !b.stuck);
  const src = free.length ? free : balls;
  if (type === 'expand') paddle.expandT = 14;
  else if (type === 'multiball') {
    const out = [];
    for (const b of balls) {
      if (out.length + balls.length > 12) break;
      for (const da of [-0.5, 0.5]) {
        const sp = clamp(ballSpeed(b) || CONFIG.baseBallSpeed, CONFIG.minBallSpeed, CONFIG.maxBallSpeed);
        const a = Math.atan2(b.vy, b.vx) + da;
        out.push({ x: b.x, y: b.y, vx: Math.cos(a) * sp, vy: Math.sin(a) * sp,
          r: 8, speed0: sp, mode: b.mode, modeT: b.modeT, trail: [], stuck: false, smashCD: 0, ghostCD: 0 });
      }
    }
    balls = balls.concat(out);
    toast('MULTIBALL!', 'pink');
    addShake(5);
  }
  else if (type === 'fire') { for (const b of src) { b.mode = 'fire'; b.modeT = 10; } }
  else if (type === 'ghost') { for (const b of src) { b.mode = 'ghost'; b.modeT = 10; } }
  else if (type === 'heavy') { for (const b of src) { b.mode = 'heavy'; b.modeT = 12; b.r = 13; } }
  else if (type === 'laser') { paddle.laserShots = 8; toast('LASER ARMED — TAP!', 'pink'); }
  else if (type === 'magnet') { paddle.magnetT = 12; }
  else if (type === 'slow') { paddle.slowT = 8; } // frame loop already scales dt while slowT>0
  else if (type === 'shield') { game.shield = true; }
  else if (type === 'life') { game.lives = Math.min(5, game.lives + 1); toast('+1 LIFE!', 'gold'); }
  paddle.glow = 0.4;
  syncUI();
}

/* ---- lasers (from Laser powerup) ---- */
function updateLasers(dt) {
  for (let i = lasers.length - 1; i >= 0; i--) {
    const l = lasers[i];
    l.y += l.vy * dt;
    let hit = false;
    for (const br of bricks) {
      if (!br.alive || br.type === 'X') continue;
      if (br.type === 'T' && !isPhaseSolid(br, game.elapsed)) continue;
      if (l.x > br.x - 2 && l.x < br.x + br.w + 2 && l.y > br.y && l.y < br.y + br.h) {
        destroyBrick(br, null, true);
        hit = true; break;
      }
    }
    if (hit || l.y < -20) lasers.splice(i, 1);
  }
}

/* ---- OVERDRIVE: ball above the brick field → exponential multiplier ---- */
function updateOverdrive(dt) {
  const anyAbove = balls.some(b => !b.stuck && b.y < field.bottom && b.y > -20 && bricks.some(br => br.alive));
  if (anyAbove && balls.length) {
    if (!game.overdrive) {
      game.overdrive = true; game.odTime = 0; game.odStep = 0;
      game.mult = Math.max(game.mult, 2);
      AudioSys.odRiser();
      toast('★ OVERDRIVE ★', 'gold');
      addRing(W / 2, field.bottom, '#ffd400', 800, 6);
      addShake(6);
    } else {
      game.odTime += dt;
      const want = Math.min(5, Math.floor(game.odTime / CONFIG.odRampTime));
      if (want > game.odStep) {
        game.odStep = want;
        game.mult = Math.pow(2, game.odStep + 1); // x2,x4,x8,x16,x32
        game.maxMult = Math.max(game.maxMult, game.mult);
        AudioSys.odRiser();
        toast('×' + game.mult + ' MULTIPLIER!', 'gold');
        addRing(W / 2, field.bottom, '#ffd400', 900, 6);
        addShake(5);
        flashVignette = Math.max(flashVignette, 0.2);
      }
    }
  } else if (game.overdrive) {
    // drain once all balls return below (grace via odCool)
    game.odCool += dt;
    if (game.odCool > 1.2) {
      game.overdrive = false; game.odCool = 0;
      game.odStep = 0; game.mult = 1;
      AudioSys.odDrop();
    }
  } else {
    game.odCool = 0;
  }
  if (!game.overdrive && game.mult > 1 && balls.every(b => b.stuck)) game.mult = 1;
}

/* ---- fx update ---- */
function updateFx(dt) {
  if (shake > 0) shake = Math.max(0, shake - dt * 40);
  shakeX = rand(-shake, shake); shakeY = rand(-shake, shake);
  if (flashVignette > 0) flashVignette -= dt * 2;
  for (let i = particles.length - 1; i >= 0; i--) {
    const p = particles[i];
    p.life -= dt;
    if (p.life <= 0) { particles.splice(i, 1); continue; }
    p.vy += p.grav * dt;
    p.vx *= p.drag; p.vy *= p.drag;
    p.x += p.vx * dt; p.y += p.vy * dt;
  }
  for (let i = floaters.length - 1; i >= 0; i--) {
    const f = floaters[i];
    f.life -= dt * 0.9; f.y += f.vy * dt;
    if (f.life <= 0) floaters.splice(i, 1);
  }
  for (let i = rings.length - 1; i >= 0; i--) {
    const r = rings[i];
    r.life -= dt; r.r += r.vr * dt;
    if (r.life <= 0) rings.splice(i, 1);
  }
  for (let i = confetti.length - 1; i >= 0; i--) {
    const c = confetti[i];
    c.life -= dt; c.x += c.vx * dt; c.y += c.vy * dt; c.rot += c.vr * dt;
    if (c.life <= 0 || c.y > H + 20) confetti.splice(i, 1);
  }
}
/* ====================== 9. RENDER ========================================== */
function render(alpha) {
  ctx.save();
  ctx.clearRect(0, 0, W, H);
  ctx.translate(shakeX, shakeY);
  drawBackground();
  drawOverdriveZone();
  drawBricks();
  drawPowerups();
  drawLasers();
  drawPaddle();
  drawBalls();
  drawFx();
  ctx.restore();
  if (flashVignette > 0) {
    ctx.fillStyle = `rgba(255,255,255,${(flashVignette * 0.35).toFixed(3)})`;
    ctx.fillRect(0, 0, W, H);
  }
  if (game.overdrive) {
    const p = 0.10 + 0.06 * Math.sin(game.elapsed * 10);
    const g = ctx.createRadialGradient(W/2, H/2, H*0.2, W/2, H/2, H*0.75);
    g.addColorStop(0, 'rgba(255,212,0,0)');
    g.addColorStop(1, `rgba(255,47,214,${p.toFixed(3)})`);
    ctx.fillStyle = g; ctx.fillRect(0, 0, W, H);
  }
}

function drawBackground() {
  // animated reactive gradient
  const t = game.elapsed;
  const od = game.overdrive ? 1 : 0;
  const hue = (260 + Math.sin(t * 0.3) * 20 + od * 40 + Math.min(game.odStep,5) * 8) % 360;
  const g = ctx.createLinearGradient(0, 0, 0, H);
  g.addColorStop(0, `hsl(${hue},70%,${7 + od * 5}%)`);
  g.addColorStop(0.55, '#070312');
  g.addColorStop(1, '#03010a');
  ctx.fillStyle = g; ctx.fillRect(-30, -30, W + 60, H + 60);

  // grid floor
  ctx.strokeStyle = `rgba(0,246,255,${0.07 + od * 0.10})`;
  ctx.lineWidth = 1;
  const gs = 44;
  const off = (t * 30) % gs;
  ctx.beginPath();
  for (let y = H * 0.5 + off; y < H + 30; y += gs) { ctx.moveTo(-30, y); ctx.lineTo(W + 30, y); }
  for (let x = -30; x < W + 30; x += gs) { ctx.moveTo(x, H * 0.4); ctx.lineTo(x - W * 0.1, H + 30); }
  ctx.stroke();

  // starfield
  for (const s of stars) {
    const tw = 0.4 + 0.6 * Math.abs(Math.sin(t * 2 + s.tw));
    ctx.fillStyle = `rgba(255,255,255,${(s.z * tw * 0.8).toFixed(3)})`;
    const sz = s.z * (od ? 3 : 2);
    ctx.fillRect(((s.x + t * 0.004 * s.z) % 1) * W, (s.y * H), sz, sz);
  }

  // paddle roam-zone hint
  ctx.fillStyle = 'rgba(0,246,255,0.035)';
  ctx.fillRect(0, H * CONFIG.paddleZoneTop, W, H * (1 - CONFIG.paddleZoneTop));
  ctx.strokeStyle = 'rgba(0,246,255,0.15)';
  ctx.setLineDash([6, 8]);
  ctx.beginPath(); ctx.moveTo(0, H * CONFIG.paddleZoneTop); ctx.lineTo(W, H * CONFIG.paddleZoneTop); ctx.stroke();
  ctx.setLineDash([]);
}

function drawOverdriveZone() {
  // top-zone line: getting above the bricks = the goal
  const y = field.bottom + 6;
  const od = game.overdrive;
  ctx.save();
  ctx.strokeStyle = od ? '#ffd400' : 'rgba(255,212,0,0.35)';
  ctx.lineWidth = od ? 3 : 1.5;
  ctx.shadowColor = '#ffd400'; ctx.shadowBlur = od ? 18 : 6;
  ctx.setLineDash([14, 10]);
  ctx.lineDashOffset = -game.elapsed * (od ? 120 : 40);
  ctx.beginPath(); ctx.moveTo(10, y); ctx.lineTo(W - 10, y); ctx.stroke();
  ctx.setLineDash([]);
  ctx.shadowBlur = 0;
  ctx.font = '700 11px ' + getFont();
  ctx.textAlign = 'center';
  ctx.fillStyle = od ? '#ffd400' : 'rgba(255,212,0,0.55)';
  ctx.fillText(od ? '★ OVERDRIVE ×' + game.mult + ' ★' : 'GET ABOVE THE BRICKS → OVERDRIVE', W / 2, y + 16);
  ctx.restore();
}

function getFont() { return 'ui-sans-serif,system-ui,"Segoe UI",Roboto,Arial,sans-serif'; }

function brickColor(br) {
  if (br.type === 'T' && !isPhaseSolid(br, game.elapsed)) return null; // ghost = translucent
  return BRICK_INFO[br.type].color;
}

function drawBricks() {
  ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
  for (const br of bricks) {
    if (!br.alive) continue;
    let scale = 1;
    if (br.spawnT > 0) scale = clamp(1 - br.spawnT * 3, 0.01, 1);
    const cx = br.x + br.w / 2, cy = br.y + br.h / 2;
    const ghost = br.type === 'T' && !isPhaseSolid(br, game.elapsed);
    ctx.save();
    ctx.translate(cx, cy); ctx.scale(scale, scale); ctx.translate(-cx, -cy);
    const col = BRICK_INFO[br.type].color;
    const flash = br.flash > 0 ? br.flash * 3 : 0;

    // body
    ctx.globalAlpha = ghost ? 0.22 : 1;
    ctx.shadowColor = col; ctx.shadowBlur = (game.overdrive ? 16 : 10) + flash * 20;
    const grad = ctx.createLinearGradient(br.x, br.y, br.x, br.y + br.h);
    grad.addColorStop(0, flash > 0.2 ? '#ffffff' : col);
    grad.addColorStop(1, shade(col, -55));
    ctx.fillStyle = grad;
    roundRect(br.x, br.y, br.w, br.h, 6); ctx.fill();
    ctx.shadowBlur = 0;
    // glass highlight
    ctx.fillStyle = 'rgba(255,255,255,0.28)';
    roundRect(br.x + 3, br.y + 2, br.w - 6, Math.max(3, br.h * 0.28), 4); ctx.fill();
    // border
    ctx.strokeStyle = ghost ? 'rgba(255,255,255,0.5)' : 'rgba(255,255,255,0.55)';
    ctx.setLineDash(ghost ? [4, 4] : []);
    ctx.lineWidth = 1.2;
    roundRect(br.x + 0.5, br.y + 0.5, br.w - 1, br.h - 1, 6); ctx.stroke();
    ctx.setLineDash([]);

    // icon per type (no HP pips — difficulty is readable, not grindy)
    ctx.fillStyle = 'rgba(0,0,0,0.55)';
    ctx.font = `900 ${Math.max(11, br.h * 0.52)}px ` + getFont();
    const icon = { S: '', A: '⟁', V: '≫', L: '⇔', T: ghost ? '◌' : '◉', O: '✸', X: '⬢' }[br.type];
    if (icon) ctx.fillText(icon, cx, cy + 1);
    if (br.type === 'A') { // steep-arrow hint
      ctx.strokeStyle = 'rgba(0,0,0,0.55)'; ctx.lineWidth = 2;
      ctx.beginPath(); ctx.moveTo(cx - 12, cy + 7); ctx.lineTo(cx, cy - 7); ctx.lineTo(cx + 12, cy + 7); ctx.stroke();
    }
    ctx.restore();
  }
  ctx.textBaseline = 'alphabetic';
}

function shade(hex, amt) {
  const n = parseInt(hex.slice(1), 16);
  const c = v => clamp(Math.round(v + amt), 0, 255);
  return `rgb(${c((n >> 16) & 255)},${c((n >> 8) & 255)},${c(n & 255)})`;
}
function roundRect(x, y, w, h, r) {
  r = Math.min(r, w / 2, h / 2);
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + w, y, x + w, y + h, r);
  ctx.arcTo(x + w, y + h, x, y + h, r);
  ctx.arcTo(x, y + h, x, y, r);
  ctx.arcTo(x, y, x + w, y, r);
  ctx.closePath();
}

function drawPaddle() {
  const pw = paddle.effW, ph = paddle.h;
  let sx = 1, sy = 1;
  if (paddle.squash > 0) { const k = paddle.squash / 0.14; sx = 1 + 0.25 * k; sy = 1 - 0.35 * k; }
  ctx.save();
  ctx.translate(paddle.x, paddle.y); ctx.scale(sx, sy);
  const od = game.overdrive;
  ctx.shadowColor = paddle.magnetT > 0 ? '#ff2fd6' : od ? '#ffd400' : '#00f6ff';
  ctx.shadowBlur = 18 + (paddle.glow > 0 ? 20 : 0) + (od ? 10 : 0);
  const g = ctx.createLinearGradient(0, -ph / 2, 0, ph / 2);
  g.addColorStop(0, '#ffffff');
  g.addColorStop(0.35, paddle.magnetT > 0 ? '#ff2fd6' : '#00f6ff');
  g.addColorStop(1, '#062a44');
  ctx.fillStyle = g;
  roundRect(-pw / 2, -ph / 2, pw, ph, ph / 2); ctx.fill();
  ctx.shadowBlur = 0;
  // smash-charge indicator: glows while moving up fast
  if (paddle.vy < -120) {
    ctx.fillStyle = `rgba(255,212,0,${clamp(-paddle.vy / 900, 0.2, 0.8).toFixed(2)})`;
    roundRect(-pw / 2 + 3, -ph / 2 - 5, pw - 6, 4, 2); ctx.fill();
    ctx.fillStyle = '#ffd400'; ctx.font = '900 11px ' + getFont(); ctx.textAlign = 'center';
    ctx.fillText('SMASH ↑', 0, -ph / 2 - 10);
  }
  if (paddle.laserShots > 0) { // laser tips
    ctx.fillStyle = '#ff3b5c'; ctx.shadowColor = '#ff3b5c'; ctx.shadowBlur = 10;
    ctx.fillRect(-pw / 2 - 2, -ph / 2 - 8, 5, 8);
    ctx.fillRect(pw / 2 - 3, -ph / 2 - 8, 5, 8);
    ctx.shadowBlur = 0;
  }
  ctx.restore();
  // shield net
  if (game.shield) {
    ctx.save();
    ctx.strokeStyle = '#3dff8f'; ctx.shadowColor = '#3dff8f'; ctx.shadowBlur = 12;
    ctx.lineWidth = 2.5; ctx.setLineDash([10, 8]); ctx.lineDashOffset = -game.elapsed * 60;
    ctx.beginPath(); ctx.moveTo(6, H - 18); ctx.lineTo(W - 6, H - 18); ctx.stroke();
    ctx.restore();
  }
}

function ballStyle(mode) {
  return { normal: '#ffffff', fire: '#ff6a00', ghost: '#c77dff', heavy: '#ffd400' }[mode] || '#fff';
}
function drawBalls() {
  for (const b of balls) {
    const col = ballStyle(b.mode);
    // trail (fades, widens for power balls)
    for (let i = 0; i < b.trail.length; i++) {
      const p = b.trail[i], k = i / b.trail.length;
      ctx.globalAlpha = k * (b.mode === 'normal' ? 0.25 : 0.55);
      ctx.fillStyle = col;
      ctx.beginPath(); ctx.arc(p.x, p.y, b.r * (0.3 + 0.7 * k), 0, TAU); ctx.fill();
    }
    ctx.globalAlpha = 1;
    ctx.save();
    ctx.shadowColor = col; ctx.shadowBlur = b.mode === 'normal' ? 14 : 26;
    if (b.mode === 'ghost') ctx.globalAlpha = 0.75;
    const g = ctx.createRadialGradient(b.x - b.r * 0.3, b.y - b.r * 0.3, 1, b.x, b.y, b.r);
    g.addColorStop(0, '#ffffff'); g.addColorStop(0.45, col); g.addColorStop(1, shade(col === '#ffffff' ? '#9adcff' : col, -60));
    ctx.fillStyle = g;
    ctx.beginPath(); ctx.arc(b.x, b.y, b.r, 0, TAU); ctx.fill();
    ctx.shadowBlur = 0;
    if (b.mode === 'fire') { // flickering flame rim
      ctx.strokeStyle = `rgba(255,${randi(120, 220)},0,0.9)`; ctx.lineWidth = 2.5;
      ctx.beginPath(); ctx.arc(b.x, b.y, b.r + rand(0, 3), 0, TAU); ctx.stroke();
      burstLite(b.x - b.vx * 0.02, b.y - b.vy * 0.02);
    }
    if (b.mode === 'heavy') {
      ctx.strokeStyle = 'rgba(0,0,0,0.5)'; ctx.lineWidth = 2;
      ctx.beginPath(); ctx.arc(b.x, b.y, b.r - 3, 0, TAU); ctx.stroke();
    }
    ctx.restore();
  }
  ctx.globalAlpha = 1;
}
function burstLite(x, y) {
  if (chance(0.6) && particles.length < 850) {
    particles.push({ x: x + rand(-3, 3), y: y + rand(-3, 3), vx: rand(-60, 60), vy: rand(-60, 60),
      life: 0.3, maxLife: 0.3, size: rand(2, 5), color: pick(['#ff6a00', '#ffd400', '#ff3b5c']),
      grav: -200, drag: 0.96, shape: 'circ' });
  }
}

function drawPowerups() {
  ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
  for (const p of powerups) {
    ctx.save();
    ctx.translate(p.x, p.y);
    ctx.shadowColor = p.info.color; ctx.shadowBlur = 14;
    // capsule
    ctx.fillStyle = 'rgba(0,0,0,0.75)';
    roundRect(-26, -13, 52, 26, 13); ctx.fill();
    ctx.strokeStyle = p.info.color; ctx.lineWidth = 2;
    roundRect(-26, -13, 52, 26, 13); ctx.stroke();
    ctx.shadowBlur = 0;
    ctx.fillStyle = p.info.color;
    ctx.font = '900 13px ' + getFont();
    ctx.fillText(p.info.icon + ' ' + shortPow(p.type), 0, 1);
    ctx.restore();
  }
  ctx.textBaseline = 'alphabetic';
}
function shortPow(t) {
  return { expand: 'WIDE', multiball: '×3', fire: 'FIRE', ghost: 'GHOST', heavy: 'HEAVY',
    laser: 'LASER', magnet: 'CATCH', slow: 'SLOW', shield: 'SHIELD', life: '+1' }[t] || t;
}

function drawLasers() {
  ctx.save();
  ctx.shadowColor = '#ff3b5c'; ctx.shadowBlur = 12;
  ctx.fillStyle = '#ff8095';
  for (const l of lasers) { ctx.fillRect(l.x - 2.5, l.y - 14, 5, 14); }
  ctx.restore();
}

function drawFx() {
  for (const p of particles) {
    ctx.globalAlpha = clamp(p.life / p.maxLife, 0, 1);
    ctx.fillStyle = p.color;
    if (p.shape === 'rect') {
      ctx.save(); ctx.translate(p.x, p.y); ctx.rotate(p.life * 6);
      ctx.fillRect(-p.size / 2, -p.size / 2, p.size, p.size * 0.7);
      ctx.restore();
    } else { ctx.beginPath(); ctx.arc(p.x, p.y, p.size * (p.life / p.maxLife) + 0.5, 0, TAU); ctx.fill(); }
  }
  ctx.globalAlpha = 1;
  for (const r of rings) {
    ctx.globalAlpha = clamp(r.life / r.maxLife, 0, 1);
    ctx.strokeStyle = r.color; ctx.lineWidth = r.width;
    ctx.beginPath(); ctx.arc(r.x, r.y, r.r, 0, TAU); ctx.stroke();
  }
  ctx.globalAlpha = 1;
  ctx.textAlign = 'center';
  for (const f of floaters) {
    ctx.globalAlpha = clamp(f.life, 0, 1);
    ctx.font = `900 ${f.size}px ` + getFont();
    ctx.lineWidth = 3; ctx.strokeStyle = 'rgba(0,0,0,0.8)';
    ctx.strokeText(f.text, f.x, f.y);
    ctx.fillStyle = f.color; ctx.fillText(f.text, f.x, f.y);
  }
  ctx.globalAlpha = 1;
  for (const c of confetti) {
    ctx.save(); ctx.translate(c.x, c.y); ctx.rotate(c.rot);
    ctx.globalAlpha = clamp(c.life, 0, 1);
    ctx.fillStyle = c.color; ctx.fillRect(-c.w / 2, -c.h / 2, c.w, c.h);
    ctx.restore();
  }
  ctx.globalAlpha = 1;
}
/* ================= 10. HUD / DOM / LOOP / BOOT ============================= */
const $ = id => document.getElementById(id);
function show(id) { $(id).classList.remove('hidden'); }
function hide(id) { $(id).classList.add('hidden'); }
function hideAllOverlays() { ['menu', 'howto', 'pause-ov', 'clear-ov', 'over-ov', 'win-ov'].forEach(hide); $('level-card').classList.add('hidden'); }

function syncUI() {
  const showHud = game.state === 'playing' || game.state === 'paused' || game.state === 'intro';
  $('hud').classList.toggle('hidden', !showHud);
  $('corner-btns').classList.toggle('hidden', game.state === 'menu');
  $('hud-score').textContent = fmtScore(game.score);
  $('hud-high').textContent = fmtScore(game.high);
  $('hud-level').textContent = game.level + '/' + LEVELS.length;
  $('hud-lives').textContent = '●'.repeat(Math.max(0, game.lives)) + '○'.repeat(Math.max(0, 5 - game.lives));
  const mp = $('mult-pill');
  $('hud-mult').textContent = '×' + game.mult;
  $('hud-combo').textContent = game.combo >= 2 ? game.combo + ' COMBO' : '';
  mp.classList.toggle('hot', game.mult >= 4);
  // overdrive bar: fill = progress to next doubling, full while max
  const frac = game.overdrive ? clamp(game.odTime % CONFIG.odRampTime / CONFIG.odRampTime, 0, 1) : 0;
  $('od-fill').style.width = (frac * 100).toFixed(1) + '%';
  $('od-label').textContent = game.overdrive ? '★ OVERDRIVE ×' + game.mult + ' ★' : 'OVERDRIVE';
  $('od-label').classList.toggle('off', !game.overdrive);
  $('menu-high').textContent = fmtScore(game.high);
  renderPowerTimers();
}

function renderPowerTimers() {
  const el = $('power-timers');
  const items = [];
  if (paddle.expandT > 0) items.push(['Expand', '#00f6ff', paddle.expandT / 14]);
  const fb = balls.find(b => b.mode === 'fire' && b.modeT > 0);
  if (fb) items.push(['Fire', '#ff6a00', fb.modeT / 10]);
  const gh = balls.find(b => b.mode === 'ghost' && b.modeT > 0);
  if (gh) items.push(['Ghost', '#c77dff', gh.modeT / 10]);
  const hv = balls.find(b => b.mode === 'heavy' && b.modeT > 0);
  if (hv) items.push(['Heavy', '#ffd400', hv.modeT / 12]);
  if (paddle.laserShots > 0) items.push(['Laser ' + paddle.laserShots, '#ff3b5c', paddle.laserShots / 8]);
  if (paddle.magnetT > 0) items.push(['Catch', '#ff2fd6', paddle.magnetT / 12]);
  if (paddle.slowT > 0) items.push(['Slow', '#7df9ff', paddle.slowT / 8]);
  if (game.shield) items.push(['Shield', '#3dff8f', 1]);
  el.innerHTML = items.map(([n, c, f]) =>
    `<div class="ptimer" style="color:${c};border-color:${c}55"><span class="dot" style="background:${c}"></span>${n}<span class="bar"><i style="width:${Math.round(clamp(f, 0, 1) * 100)}%;background:${c}"></i></span></div>`
  ).join('');
}

function buildLegends() {
  $('legend').innerHTML = Object.entries(BRICK_INFO).map(([k, v]) =>
    `<li><span class="sw" style="color:${v.color};background:${v.color}44"></span><span><b>${v.name}</b> — ${v.desc}</span></li>`
  ).join('');
  $('pow-legend').innerHTML = POWER_INFO.map(p =>
    `<li><span class="sw" style="color:${p.color};background:${p.color}33;text-align:center;font-weight:900">${p.icon}</span><span><b>${p.name}</b> — ${p.desc}</span></li>`
  ).join('');
}

function showLevelCard(n, done) {
  const lv = LEVELS[n - 1];
  game.state = 'intro';
  hideAllOverlays();
  loadLevel(n);
  $('lc-sub').textContent = 'LEVEL ' + n + ' / ' + LEVELS.length;
  $('lc-title').textContent = lv.title;
  $('lc-tip').textContent = '💡 ' + lv.tip;
  $('level-card').classList.remove('hidden');
  syncUI();
  setTimeout(() => { $('level-card').classList.add('hidden'); if (game.state === 'intro') done(); }, 2400);
}

// --- buttons ---
$('btn-start').onclick = () => { AudioSys.resume(); hideAllOverlays(); startGame(); };
$('btn-how').onclick = () => { AudioSys.resume(); show('howto'); };
$('btn-how-back').onclick = () => { hide('howto'); };
$('btn-resume').onclick = () => togglePause();
$('btn-quit').onclick = () => { hide('pause-ov'); game.state = 'menu'; AudioSys.stopMusic(); hideAllOverlays(); show('menu'); syncUI(); };
$('btn-next').onclick = () => { hide('clear-ov'); nextLevel(); };
$('btn-retry').onclick = () => { hide('over-ov'); game.score = 0; game.lives = CONFIG.lives; game.level = 1; game.maxMult = 1; AudioSys.startMusic(); showLevelCard(1, () => { game.state = 'playing'; syncUI(); }); };
$('btn-again').onclick = () => { hide('win-ov'); game.score = 0; game.lives = CONFIG.lives; game.level = 1; game.maxMult = 1; AudioSys.startMusic(); showLevelCard(1, () => { game.state = 'playing'; syncUI(); }); };
$('btn-menu').onclick = () => { hide('over-ov'); game.state = 'menu'; hideAllOverlays(); show('menu'); syncUI(); };
$('btn-menu2').onclick = () => { hide('win-ov'); game.state = 'menu'; hideAllOverlays(); show('menu'); syncUI(); };
$('btn-pause').onclick = () => { AudioSys.resume(); togglePause(); };
$('btn-mute').onclick = () => AudioSys.toggleMute();
// mouse-follow target from cursor even without click (desktop feel)
window.addEventListener('mousemove', e => {
  if (game.state === 'playing' && !input.touchMode) {
    paddle.targetX = clamp(e.clientX, paddle.effW / 2, W - paddle.effW / 2);
    paddle.targetY = clamp(e.clientY, paddle.minY, paddle.maxY);
    paddle.hasTarget = true;
  }
});

/* --- fixed-timestep main loop with hit-stop + slow-mo --- */
let lastT = performance.now(), acc = 0;
function frame(now) {
  requestAnimationFrame(frame);
  if (typeof now !== 'number' || !(now >= 0)) now = performance.now(); // first-frame guard
  let dt = Math.min(0.1, (now - lastT) / 1000);
  if (!(dt >= 0)) dt = 0;
  lastT = now;
  if (game.state === 'playing') {
    if (hitStop > 0) { hitStop -= dt; }
    else {
      const scaled = dt * (paddle.slowT > 0 ? 0.55 : 1) * timeScale;
      acc += scaled;
      let n = 0;
      while (acc >= CONFIG.step && n < 12) { update(CONFIG.step); acc -= CONFIG.step; n++; }
      if (n === 12) acc = 0;
    }
    if (Math.floor(now / 150) !== Math.floor((now - dt * 1000) / 150)) syncUI();
  } else if (game.state === 'intro' || game.state === 'menu') {
    game.elapsed += dt; // keep bg alive
    updateFx(dt);
  } else if (game.state === 'clear' || game.state === 'over' || game.state === 'win') {
    game.elapsed += dt * 0.4;
    updateFx(dt);
  }
  render(dt);
}

/* --- boot --- */
buildLegends();
resize();
paddle.reset();
loadLevel(1);
game.state = 'menu';
hideAllOverlays(); show('menu');
syncUI();
requestAnimationFrame(frame);
