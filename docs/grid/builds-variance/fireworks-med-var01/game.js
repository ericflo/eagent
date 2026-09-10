/* =========================================================================
   Crownline — a modern Breakout about breaking through to the top.
   Pure JS, no dependencies. Works from file:// (plain script tags).

   Architecture (single file, namespaced sections):
     Math/UI helpers -> Audio engine (synthesized SFX) -> Input
     -> Particles & FX -> Entities (Ball, Brick, Paddle, PowerUp)
     -> Level generation -> Game state machine + fixed-timestep loop
     -> Rendering -> Debug hooks (window.__game)
   ========================================================================= */
'use strict'; window.__gameMarker = (window.__gameMarker || 0) + 1; // PROCESSED

/* ============================== CONSTANTS =============================== */
const W = 960, H = 720;                 // logical playfield size (~4:3)
const DT = 1 / 120;                     // fixed physics timestep (120 Hz)
const PADDLE_W = 130, PADDLE_H = 18;
const PADDLE_BAND_TOP = H * 0.72;       // paddle Y band: bottom ~28%
const PADDLE_Y = H - 60;
const BALL_R = 8;
const BASE_BALL_SPEED = 380;
const MAX_BALL_SPEED = 900;
const BRICK_W = 74, BRICK_H = 26;
const BRICK_TOP = 96;                   // top of the brick field
const FIELD = { top: BRICK_TOP, bottom: BRICK_TOP + 10 * (BRICK_H + 6), left: 8, right: W - 8 };
const CROWN_SCORE = 500;
const LIVES_MAX = 3;
const MULTIBALL_MAX = 12;

const POWERUP_TYPES = ['multi', 'wide', 'slow', 'magnet', 'fire', 'life'];

/* ============================ MATH HELPERS ============================== */
const clamp = (v, a, b) => v < a ? a : v > b ? b : v;
const lerp = (a, b, t) => a + (b - a) * t;
const rand = (a, b) => a + Math.random() * (b - a);
const randi = (a, b) => Math.floor(rand(a, b + 1));
const dist2 = (x1, y1, x2, y2) => { const dx = x2 - x1, dy = y2 - y1; return dx * dx + dy * dy; };
const TAU = Math.PI * 2;

// frame-rate independent easing: approaches target by (1 - k^(dt*60))
const approach = (cur, target, k, dt) => target + (cur - target) * Math.pow(k, dt * 60);

/* ================================ AUDIO =================================
   All SFX synthesized. Kept short + quiet. Pitch/volume/density escalate
   with the multiplier + on-top "heat" so the game audibly powers up.
   AudioContext is created lazily on first user gesture (autoplay policy).
========================================================================= */
const AudioSys = {
  ctx: null, master: null, muted: true, // muted until first interaction
  _droneOsc: null, _droneGain: null, _droneFilter: null, _droneLayer: null,

  ensure() {
    if (this.ctx) return;
    const AC = window.AudioContext || window.webkitAudioContext;
    if (!AC) { this.ctx = { currentTime: 0, state: 'running', sampleRate: 44100, destination: null,
      createGain: () => ({ gain: { value: 0, setValueAtTime: () => {}, linearRampToValueAtTime: () => {}, exponentialRampToValueAtTime: () => {}, setTargetAtTime: () => {} }, connect: () => {} }),
      createOscillator: () => ({ type: '', frequency: { value: 0, setValueAtTime: () => {}, exponentialRampToValueAtTime: () => {}, setTargetAtTime: () => {} }, connect: () => {}, start: () => {}, stop: () => {} }),
      createBiquadFilter: () => ({ type: '', frequency: { value: 0, setTargetAtTime: () => {} }, connect: () => {} }),
      createBuffer: () => ({ getChannelData: () => new Float32Array(1) }),
      createBufferSource: () => ({ buffer: null, connect: () => {}, start: () => {} }),
    }; this.master = this.ctx.createGain(); return; }
    this.ctx = new AC();
    this.master = this.ctx.createGain();
    this.master.gain.value = this.muted ? 0 : 0.5;
    this.master.connect(this.ctx.destination);
    this._startDrone();
  },
  resume() { if (this.ctx && this.ctx.state === 'suspended') this.ctx.resume(); },
  setMuted(m) {
    this.muted = m;
    if (this.master) this.master.gain.setTargetAtTime(m ? 0 : 0.5, this.ctx.currentTime, 0.05);
  },
  toggleMute() { this.setMuted(!this.muted); return this.muted; },

  // Low ambient drone whose brightness/tempo follow the heat level 0..3
  _startDrone() {
    const c = this.ctx;
    this._droneGain = c.createGain(); this._droneGain.gain.value = 0;
    this._droneFilter = c.createBiquadFilter(); this._droneFilter.type = 'lowpass';
    this._droneFilter.frequency.value = 200;
    this._droneGain.connect(this._droneFilter); this._droneFilter.connect(this.master);
    this._droneOsc = c.createOscillator(); this._droneOsc.type = 'sawtooth';
    this._droneOsc.frequency.value = 55;
    this._droneOsc.connect(this._droneGain); this._droneOsc.start();
    this._droneLayer = c.createOscillator(); this._droneLayer.type = 'triangle';
    this._droneLayer.frequency.value = 82.5;
    const lg = c.createGain(); lg.gain.value = 0;
    this._droneLayer.connect(lg); lg.connect(this._droneGain); this._droneLayer.start();
    this._layerGain = lg;
  },
  // heat: 0..3+ — drone opens up and a fifth layer fades in on top
  setHeat(heat, on) {
    if (!this.ctx || this.muted) return;
    const t = this.ctx.currentTime;
    this._droneGain.gain.setTargetAtTime(on ? 0.05 + heat * 0.02 : 0.015, t, 0.4);
    this._droneFilter.frequency.setTargetAtTime(180 + heat * heat * 220, t, 0.4);
    this._droneOsc.frequency.setTargetAtTime(55 * Math.pow(1.06, heat), t, 0.4);
    this._layerGain.gain.setTargetAtTime(on ? 0.05 + heat * 0.03 : 0, t, 0.6);
    this._droneLayer.frequency.setTargetAtTime(82.5 * Math.pow(1.06, heat), t, 0.4);
  },

  _env(vol, attack, decay) {
    const g = this.ctx.createGain();
    const t = this.ctx.currentTime;
    g.gain.setValueAtTime(0, t);
    g.gain.linearRampToValueAtTime(vol, t + attack);
    g.gain.exponentialRampToValueAtTime(0.0001, t + attack + decay);
    return g;
  },
  _beep(type, f0, f1, vol, decay, delay = 0) {
    if (!this.ctx || this.muted) return;
    const t = this.ctx.currentTime + delay;
    const o = this.ctx.createOscillator(), g = this._env(vol, 0.004, decay);
    o.type = type;
    o.frequency.setValueAtTime(f0, t);
    if (f1 !== f0) o.frequency.exponentialRampToValueAtTime(Math.max(1, f1), t + decay);
    o.connect(g); g.connect(this.master);
    o.start(t); o.stop(t + decay + 0.05);
  },
  _noise(vol, decay, filterFreq, delay = 0) {
    if (!this.ctx || this.muted) return;
    const t = this.ctx.currentTime + delay;
    const len = Math.ceil(this.ctx.sampleRate * decay);
    const buf = this.ctx.createBuffer(1, len, this.ctx.sampleRate);
    const d = buf.getChannelData(0);
    for (let i = 0; i < len; i++) d[i] = (Math.random() * 2 - 1) * (1 - i / len);
    const src = this.ctx.createBufferSource(); src.buffer = buf;
    const f = this.ctx.createBiquadFilter(); f.type = 'bandpass'; f.frequency.value = filterFreq;
    const g = this._env(vol, 0.002, decay);
    src.connect(f); f.connect(g); g.connect(this.master);
    src.start(t);
  },

  // --- SFX catalogue: every effect takes intensity 0..1 (from heat/multiplier)
  sfx(name, it = 0) {
    if (!this.ctx || this.muted) return;
    const v = 0.6 + it * 0.7; // intensity scales volume
    switch (name) {
      case 'paddle': this._beep('sine', 160 + it * 90, 110, 0.10 * v, 0.09); break;
      case 'smash': this._beep('sawtooth', 220, 60, 0.14, 0.22); this._noise(0.12, 0.18, 2400); break;
      case 'brick': this._beep('square', 340 + it * 420, 240 + it * 300, 0.07 * v, 0.07); this._noise(0.05 * v, 0.05, 3200); break;
      case 'armorspark': this._beep('square', 900, 700, 0.04, 0.04); this._noise(0.05, 0.03, 6000); break;
      case 'deflect': this._beep('sine', 420, 380, 0.05, 0.04); break;
      case 'crown': {
        const base = 523 * Math.pow(1.06, Math.min(it, 2) * 4);
        [0, 0.06, 0.12].forEach((d, i) => this._beep('triangle', base * (1 + i * 0.25), base * (1 + i * 0.25), 0.12, 0.3, d));
        this._noise(0.08, 0.4, 1800);
        break;
      }
      case 'breakthrough': [0, 0.08, 0.16, 0.28].forEach((d, i) => this._beep('triangle', 392 * Math.pow(2, i / 4), 392 * Math.pow(2, i / 4), 0.11, 0.35, d)); break;
      case 'ontopTick': this._beep('sine', 700 + it * 500, 700 + it * 500, 0.05, 0.1); break;
      case 'powerup': this._beep('sine', 440, 880, 0.1, 0.18); this._beep('sine', 660, 1320, 0.07, 0.2, 0.05); break;
      case 'life': [0, 0.1, 0.2].forEach((d, i) => this._beep('sine', 523 + i * 262, 523 + i * 262, 0.1, 0.25, d)); break;
      case 'lose': [0, 0.12, 0.26].forEach((d, i) => this._beep('sawtooth', 300 - i * 80, 160 - i * 40, 0.1, 0.3, d)); break;
      case 'gameover': [0, 0.18, 0.4, 0.62].forEach((d, i) => this._beep('sawtooth', 260 - i * 45, 120, 0.12, 0.5, d)); break;
      case 'levelclear': [0, 0.09, 0.18, 0.3].forEach((d, i) => this._beep('square', 523 * Math.pow(2, i / 6), 523 * Math.pow(2, i / 6), 0.09, 0.3, d)); break;
      case 'launch': this._beep('sine', 240, 480, 0.08, 0.1); break;
      case 'ui': this._beep('sine', 600, 600, 0.05, 0.05); break;
      case 'wall': this._beep('sine', 200 + it * 120, 180, 0.04, 0.04); break;
      case 'magnetCatch': this._beep('sine', 300, 500, 0.08, 0.12); break;
    }
  },
};

/* ================================ INPUT =================================
   Mouse: absolute paddle positioning. Touch: drag offset-relative (finger
   never needs to be on the paddle). Keys: velocity nudge.
========================================================================= */
const Input = {
  mouseX: W / 2, mouseY: PADDLE_Y,
  keys: {}, _dx: 0, _dy: 0,           // _dx/_dy: drag delta accumulated this frame
  launchQueued: false, anyKey: false,
  init(canvas) {
    const rel = (e) => {
      const r = canvas.getBoundingClientRect();
      const cx = (e.touches ? e.touches[0].clientX : e.clientX) - r.left;
      const cy = (e.touches ? e.touches[0].clientY : e.clientY) - r.top;
      return { x: cx * (W / r.width), y: cy * (H / r.height) };
    };
    canvas.addEventListener('mousemove', (e) => { const p = rel(e); this.mouseX = p.x; this.mouseY = p.y; this._usingTouch = false; });
    canvas.addEventListener('mousedown', (e) => { this.anyKey = true; this.launchQueued = true; });
    // Touch: track drag offset relative to touch start
    let touchStart = null;
    canvas.addEventListener('touchstart', (e) => {
      e.preventDefault(); this.anyKey = true; this._usingTouch = true;
      const p = rel(e); touchStart = p; this._startPaddle = { x: Game.paddle.x, y: Game.paddle.y };
      this.launchQueued = true;
    }, { passive: false });
    canvas.addEventListener('touchmove', (e) => {
      e.preventDefault();
      if (!touchStart) return;
      const p = rel(e);
      this._dx += p.x - touchStart.x; this._dy += p.y - touchStart.y;
      touchStart = p;
    }, { passive: false });
    canvas.addEventListener('touchend', (e) => { e.preventDefault(); touchStart = null; }, { passive: false });
    window.addEventListener('keydown', (e) => {
      this.anyKey = true;
      if (['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', ' '].includes(e.key)) e.preventDefault();
      if (!this.keys[e.key]) {
        if (e.key === ' ' || e.key === 'Enter') this.launchQueued = true;
        if (e.key === 'p' || e.key === 'P' || e.key === 'Escape') Game.togglePause();
        if (e.key === 'm' || e.key === 'M') Game.toggleMute();
      }
      this.keys[e.key] = true;
    });
    window.addEventListener('keyup', (e) => { this.keys[e.key] = false; });
  },
  // Called once per physics frame; returns {vx, vy} desired paddle velocity + consumes taps
  consume() {
    let vx = 0, vy = 0;
    if (this.keys['ArrowLeft'] || this.keys['a'] || this.keys['A']) vx -= 1;
    if (this.keys['ArrowRight'] || this.keys['d'] || this.keys['D']) vx += 1;
    if (this.keys['ArrowUp'] || this.keys['w'] || this.keys['W']) vy -= 1;
    if (this.keys['ArrowDown'] || this.keys['s'] || this.keys['S']) vy += 1;
    const drag = { dx: this._dx, dy: this._dy };
    this._dx = 0; this._dy = 0;
    const launch = this.launchQueued; this.launchQueued = false;
    const key = this.anyKey; this.anyKey = false;
    return { vx, vy, drag, launch, key, mouseX: this.mouseX, mouseY: this.mouseY };
  },
};

/* ============================ PARTICLES & FX ============================ */
const FX = {
  parts: [], pops: [], ring: [],
  shakeT: 0, shakeMag: 0, shakeX: 0, shakeY: 0,
  flash: 0, timeWarp: 0, hue: 225, // background hue drifts with heat

  shake(mag) { this.shakeMag = Math.max(this.shakeMag, mag); this.shakeT = Math.max(this.shakeT, 0.3); },

  burst(x, y, color, n, spd, life = 0.6, grav = 900, size = 3) {
    for (let i = 0; i < n; i++) {
      const a = rand(0, TAU), s = rand(spd * 0.3, spd);
      this.parts.push({ x, y, vx: Math.cos(a) * s, vy: Math.sin(a) * s - spd * 0.3, life, t: 0, color, size: size * rand(0.6, 1.5), grav });
    }
    if (this.parts.length > 900) this.parts.splice(0, this.parts.length - 900);
  },
  sparks(x, y, dirX, dirY, color, n = 8, spd = 420) {
    for (let i = 0; i < n; i++) {
      const a = Math.atan2(dirY, dirX) + rand(-0.9, 0.9), s = rand(spd * 0.4, spd);
      this.parts.push({ x, y, vx: Math.cos(a) * s, vy: Math.sin(a) * s, life: rand(0.2, 0.45), t: 0, color, size: rand(1.5, 3), grav: 500 });
    }
  },
  popup(x, y, text, color, size = 18, tag = null) {
    // tagged popups replace the previous one with the same tag (e.g. power-up
    // callouts above the paddle — two in quick succession must not overdraw)
    if (tag) {
      for (let i = this.pops.length - 1; i >= 0; i--) if (this.pops[i].tag === tag) this.pops.splice(i, 1);
    }
    // clamp x so centered text can't clip the left/right canvas edge
    // (rough width estimate: ~0.62em per character)
    const halfW = text.length * size * 0.31;
    x = clamp(x, halfW + 6, W - halfW - 6);
    // nudge vertically away from nearby live popups so they don't stack
    for (const p of this.pops) {
      if (p.t < p.life && Math.abs(p.x - x) < 60 && Math.abs(p.y - y) < 18) y -= 16;
    }
    this.pops.push({ x, y, text, color, size, t: 0, life: 0.9, tag });
    // cap concurrent popups: past ~8, fade the oldest out quickly
    if (this.pops.length > 8) {
      for (let i = 0; i < this.pops.length - 8; i++) this.pops[i].life = Math.min(this.pops[i].life, this.pops[i].t + 0.25);
    }
    if (this.pops.length > 40) this.pops.shift();
  },
  ringPulse(x, y, color, maxR = 60) { this.ring.push({ x, y, t: 0, life: 0.4, color, maxR }); },

  update(dt) {
    this.shakeT = Math.max(0, this.shakeT - dt);
    if (this.shakeT <= 0) this.shakeMag = 0;
    const sm = this.shakeMag * (this.shakeT / 0.3);
    this.shakeX = rand(-1, 1) * sm; this.shakeY = rand(-1, 1) * sm;
    this.flash = Math.max(0, this.flash - dt * 3);
    this.timeWarp = Math.max(0, this.timeWarp - dt * 1.5);
    for (let i = this.parts.length - 1; i >= 0; i--) {
      const p = this.parts[i]; p.t += dt;
      if (p.t >= p.life) { this.parts.splice(i, 1); continue; }
      p.vy += p.grav * dt; p.x += p.vx * dt; p.y += p.vy * dt;
    }
    for (let i = this.pops.length - 1; i >= 0; i--) {
      const p = this.pops[i]; p.t += dt; p.y -= 46 * dt;
      if (p.t >= p.life) this.pops.splice(i, 1);
    }
    for (let i = this.ring.length - 1; i >= 0; i--) {
      const r = this.ring[i]; r.t += dt;
      if (r.t >= r.life) this.ring.splice(i, 1);
    }
  },
  clear() { this.parts.length = 0; this.pops.length = 0; this.ring.length = 0; this.shakeMag = 0; this.shakeT = 0; },
};

/* =============================== ENTITIES =============================== */
class Ball {
  constructor(x, y, vx, vy) { this.x = x; this.y = y; this.vx = vx; this.vy = vy; this.stuck = false; this.stuckOffset = 0; this.fire = 0; this.trail = []; this.speedScale = 1; }
  get speed() { return Math.hypot(this.vx, this.vy); }
  setSpeed(s) { const c = this.speed || 1; this.vx = this.vx / c * s; this.vy = this.vy / c * s; }
  // Prevent too-horizontal travel: re-steepen the angle if needed
  enforceAngle() {
    const s = this.speed; if (s === 0) return;
    let a = Math.atan2(this.vy, this.vx);
    const minSteep = 0.32; // ~18° off horizontal
    // If moving mostly horizontally, push toward vertical in current direction
    const sinA = Math.sin(a);
    if (Math.abs(sinA) < minSteep) {
      const dirX = Math.cos(a) >= 0 ? 1 : -1;
      const dirY = this.vy >= 0 ? 1 : -1;
      const newA = Math.atan2(dirY * minSteep, dirX * Math.sqrt(1 - minSteep * minSteep));
      this.vx = Math.cos(newA) * s; this.vy = Math.sin(newA) * s;
    }
  }
}

class Paddle {
  constructor() { this.x = W / 2; this.y = PADDLE_Y; this.w = PADDLE_W; this.baseW = PADDLE_W; this.vx = 0; this.vy = 0; this.squash = 0; this.magnet = 0; }
  update(dt, input) {
    const prevX = this.x, prevY = this.y;
    // Keyboard: velocity in px/s
    const KS = 640;
    if (input.vx || input.vy) { this.x += input.vx * KS * dt; this.y += input.vy * KS * dt; }
    // Mouse: ease toward cursor (gives the paddle a velocity we can read for english)
    if (!Input._usingTouch && !input.vx && !input.vy) {
      const tx = clamp(input.mouseX, this.w / 2, W - this.w / 2);
      const ty = clamp(input.mouseY, PADDLE_BAND_TOP, H - PADDLE_H);
      this.x = approach(this.x, tx, 0.15, dt); this.y = approach(this.y, ty, 0.15, dt);
    }
    // Touch drag: offset-relative movement
    if (input.drag && (input.drag.dx || input.drag.dy)) {
      const s = 1.35; // slight amplification so short thumb strokes cover the field
      this.x += input.drag.dx * s; this.y += input.drag.dy * s;
    }
    this.x = clamp(this.x, this.w / 2, W - this.w / 2);
    this.y = clamp(this.y, PADDLE_BAND_TOP, H - PADDLE_H);
    this.vx = (this.x - prevX) / dt; this.vy = (this.y - prevY) / dt;
    this.squash = approach(this.squash, 0, 0.75, dt);
    this.w = approach(this.w, this.baseW, 0.7, dt);
  }
}

/* Brick types (mechanically distinct, no plain multi-hit HP):
   std      — 1 hit, standard.
   armored  — breaks only if |ball speed| >= ARMOR_SPEED on impact; else sparks.
   angle    — breaks only when impact angle (vs vertical) is inside its window;
              wrong-angle hits deflect off with a clang. Window shown as stripes.
   shifter  — teleports to a free slot on a slow cycle (or when hit).
   crown    — top-row gold; big points; breaking one triggers BREAKTHROUGH.
=========================================================================== */
const ARMOR_SPEED = 520;          // ball speed needed to crack armored bricks
const ANGLE_WINDOW = 0.62;        // radians from vertical that breaks angle bricks
const SHIFTER_CYCLE = 7;          // seconds between shifter teleports

class Brick {
  constructor(col, row, type, opts = {}) {
    this.col = col; this.row = row; this.type = type;
    this.w = type === 'crown' ? BRICK_W : BRICK_W; this.h = BRICK_H;
    this.w = (opts.w || 1) * BRICK_W - 6; this.h = BRICK_H - 6;
    this.colSpan = opts.w || 1;
    this.dead = false;
    this.flash = 0;                    // hit flash
    this.born = 0;                     // spawn animation
    this.regrow = 0;
    this.shiftT = rand(0, SHIFTER_CYCLE);
    this.angleSign = opts.angleSign || (Math.random() < 0.5 ? 1 : -1); // +1 steep | -1 shallow preferred side
  }
  get x() { return FIELD.left + this.col * (BRICK_W + 6) + 3; }
  get y() { return FIELD.top + this.row * (BRICK_H + 6) + 3; }
  get cx() { return this.x + this.w / 2; }
  get cy() { return this.y + this.h / 2; }
  get basePoints() {
    return this.type === 'crown' ? CROWN_SCORE : this.type === 'armored' ? 90 : this.type === 'angle' ? 120 : this.type === 'shifter' ? 150 : 20;
  }
}

class PowerUp {
  constructor(x, y, type) {
    this.x = x; this.y = y; this.type = type; this.t = 0; this.vy = 120; this.dead = false;
    const STYLE = {
      multi: { color: '#ff5db1', icon: '⦿' }, wide: { color: '#3dd6ff', icon: '↔' },
      slow: { color: '#9d6bff', icon: '⏱' }, magnet: { color: '#ffd23d', icon: 'U' },
      fire: { color: '#ff7a2f', icon: '✦' }, life: { color: '#57ff8a', icon: '+' },
    };
    this.style = STYLE[type];
  }
}

const POWERUP_LABEL = { multi: 'MULTIBALL', wide: 'WIDE', slow: 'SLOW-MO', magnet: 'MAGNET', fire: 'FIREBALL', life: '+1 LIFE' };

/* =============================== LEVELS =================================
   5+ distinct layouts; every level always has a gold crown row on top so
   the breakthrough path exists. Level 6+ cycles layouts with higher speed.
========================================================================= */
const COLS = 12, ROWS = 9;
// Each pattern fills col,row grid; row 0 is reserved for crowns (auto-added).
function generateLevel(n) {
  const idx = (n - 1) % 5;                 // cycle after level 5
  const tier = Math.floor((n - 1) / 5);    // difficulty tier for speed/mix
  const grid = [];                         // grid[row][col] = type|null
  for (let r = 0; r < ROWS; r++) { grid[r] = []; for (let c = 0; c < COLS; c++) grid[r][c] = null; }
  const set = (r, c, t) => { if (r >= 0 && r < ROWS && c >= 0 && c < COLS) grid[r][c] = t; };

  if (idx === 0) { // --- Level 1: simple rows with gaps
    for (let r = 1; r <= 5; r++) for (let c = 0; c < COLS; c++)
      if ((c + r) % 4 !== 3) set(r, c, r === 5 && c % 4 === 1 ? 'angle' : 'std');
    set(3, 2, 'armored'); set(3, 9, 'armored'); set(4, 5, 'shifter');
  } else if (idx === 1) { // --- Level 2: checkerboard with armored wall
    for (let r = 1; r <= 7; r++) for (let c = 0; c < COLS; c++)
      if ((r + c) % 2 === 0) set(r, c, 'std');
    for (let c = 3; c <= 8; c++) set(4, c, 'armored');
  } else if (idx === 2) { // --- Level 3: diamond
    const mid = (COLS - 1) / 2;
    for (let r = 1; r <= 8; r++) for (let c = 0; c < COLS; c++) {
      const d = Math.abs(c - mid) + (r - 4.5);
      if (d < 4.2) set(r, c, d > 3.1 ? 'armored' : (r + c) % 5 === 0 ? 'shifter' : 'std');
    }
  } else if (idx === 3) { // --- Level 4: arch / twin towers
    for (let r = 1; r <= 8; r++) {
      for (let c = 0; c < COLS; c++) {
        const tower = (c >= 2 && c <= 4) || (c >= 7 && c <= 9);
        if (tower) set(r, c, r === 5 ? 'angle' : 'std');
      }
    }
    for (let c = 5; c <= 6; c++) { set(8, c, 'armored'); set(7, c, 'shifter'); }
  } else { // --- Level 5: random-with-gaps, guaranteed pass-through columns
    for (let c = 0; c < COLS; c++) if (c % 4 === 2) continue; // gap columns
    for (let r = 1; r <= 8; r++) for (let c = 0; c < COLS; c++) {
      if (c % 4 === 2) continue;
      if (Math.random() < 0.18) continue; // random gaps
      const roll = Math.random();
      set(r, c, roll < 0.10 ? 'shifter' : roll < 0.24 ? 'armored' : roll < 0.38 ? 'angle' : 'std');
    }
  }

  // Crown row on top of every level: the literal gateway to ON TOP.
  const bricks = [];
  for (let c = 0; c < COLS; c++) bricks.push(new Brick(c, 0, 'crown'));
  for (let r = 1; r < ROWS; r++) for (let c = 0; c < COLS; c++)
    if (grid[r][c]) bricks.push(new Brick(c, r, grid[r][c]));
  return bricks;
}

/* ============================ GAME STATE MACHINE ========================
   States: 'menu' -> 'serve' -> 'play' -> ('levelclear'|'gameover')
   BREAKTHROUGH (onTop) sub-state while any ball is above FIELD.top:
     meter 0..1 fills in ~2s per multiplier step; multiplier = 1 + floor.
     Crown brick break grants/extends the state.
========================================================================= */
const HEAT_MAX = 3;

const Game = {
  state: 'menu',        // menu | serve | play | paused | levelclear | gameover
  prevState: null,
  score: 0, lives: LIVES_MAX, level: 1,
  highScore: +(localStorage.getItem('crownline.high') || 0),
  chain: 0,             // bricks broken since last paddle touch
  balls: [], bricks: [], powerups: [],
  paddle: new Paddle(),
  effects: {},          // timed power-up effects: {wide, slow, fire, magnet}
  levelIntroT: 0, clearT: 0, overT: 0, pausedFrom: null,
  banner: { t: 0, text: '', sub: '', life: 0 },

  // breakthrough state
  onTop: false, onTopT: 0, onTopMult: 1, meter: 0, graceT: 0,
  stars: [],

  init() {
    for (let i = 0; i < 90; i++) this.stars.push({ x: rand(0, W), y: rand(0, H), z: rand(0.2, 1), tw: rand(0, TAU) });
    Input.init(document.getElementById('game'));
  },

  start() {
    this.score = 0; this.lives = LIVES_MAX; this.level = 1; this.chain = 0;
    this.onTop = false; this.onTopT = 0; this.onTopMult = 1; this.meter = 0;
    this.effects = {};
    this.banner.life = 0; // drop any banner from a previous run
    this.loadLevel(1);
    this.setState('serve'); this.resetBall();
    FX.clear();
    document.getElementById('game').focus?.();
  },
  // Banner lifecycle: one banner at a time; a state transition drops any
  // banner owned by the state we're leaving so nothing stale crosses over.
  // Transient banners (ON TOP / BALL LOST) expire on their own timer; the
  // level-clear banner is owned by the 'levelclear' state and is cleared
  // explicitly in loadLevel() / start().
  setState(s) {
    if (s !== this.state && this.state === 'levelclear') this.banner.life = 0; // leaving levelclear: drop the CLEAR banner
    if (s === 'gameover') this.banner.life = 0; // never render a banner over the game-over screen
    this.state = s;
  },
  togglePause() {
    if (this.state === 'play' || this.state === 'serve') { this.pausedFrom = this.state; this.setState('paused'); AudioSys.sfx('ui'); }
    else if (this.state === 'paused') { this.setState(this.pausedFrom || 'play'); AudioSys.sfx('ui'); }
  },
  toggleMute() { const m = AudioSys.toggleMute(); return m; },

  loadLevel(n) {
    this.bricks = generateLevel(n);
    this.powerups.length = 0;
    // level intro is now a real banner: single owner, fades on its own
    // ~2.2s timer, and it replaces (never stacks with) any other banner
    this.levelIntroT = 0;
    this.showBanner('LEVEL ' + n, 'break the gold crowns to break through', 2.2);
    FX.clear();
    this.onTop = false; this.onTopT = 0; this.onTopMult = 1; this.meter = 0; this.graceT = 0;
  },

  resetBall() {
    this.balls = [new Ball(this.paddle.x, this.paddle.y - BALL_R - 10, 0, 0)];
    this.balls[0].stuck = true;
    this.chain = 0;
    this.effects.fire = 0;
  },
  launch() {
    if (this.state !== 'serve') return;
    const b = this.balls[0];
    if (!b.stuck) return;
    const a = rand(-0.6, 0.6) - Math.PI / 2;
    const sp = BASE_BALL_SPEED * (1 + (this.level - 1) * 0.06);
    b.vx = Math.cos(a) * sp; b.vy = Math.sin(a) * sp; b.stuck = false;
    AudioSys.sfx('launch');
    this.setState('play');
  },

  // ---------- power-up drops & effects ----------
  maybeDrop(x, y) {
    if (Math.random() < 0.14) {
      const pool = this.lives < LIVES_MAX ? POWERUP_TYPES : POWERUP_TYPES.slice(0, 5);
      const type = pool[randi(0, pool.length - 1)];
      this.powerups.push(new PowerUp(x, y, type === 'life' && Math.random() < 0.75 ? 'multi' : type));
    }
  },
  applyPowerUp(type) {
    const e = this.effects;
    AudioSys.sfx(type === 'life' ? 'life' : 'powerup');
    FX.popup(this.paddle.x, this.paddle.y - 40, POWERUP_LABEL[type], '#ffffff', 20, 'callout-' + type);
    if (type === 'multi') {
      const cur = this.balls.filter(b => !b.stuck);
      const src = cur.length ? cur : this.balls;
      const added = [];
      for (const b of src) {
        if (this.balls.length + added.length >= MULTIBALL_MAX) break;
        for (let k = -1; k <= 1 && this.balls.length + added.length < MULTIBALL_MAX; k += 2) {
          const a = Math.atan2(b.vy, b.vx) + k * 0.5;
          const nb = new Ball(b.x, b.y, Math.cos(a) * b.speed, Math.sin(a) * b.speed);
          nb.fire = b.fire; added.push(nb);
        }
      }
      this.balls.push(...added);
    } else if (type === 'life') { this.lives = Math.min(LIVES_MAX, this.lives + 1); }
    else if (type === 'wide') { e.wide = 12; this.paddle.baseW = PADDLE_W * 1.6; }
    else if (type === 'slow') { e.slow = 6; }
    else if (type === 'magnet') { e.magnet = 10; this.paddle.magnet = 1; }
    else if (type === 'fire') { e.fire = 7; for (const b of this.balls) b.fire = 1; }
  },

  // ---------- breakthrough state machine ----------
  updateOnTop(dt) {
    const above = this.balls.some(b => !b.stuck && b.y - BALL_R < FIELD.top);
    if (above) {
      this.graceT = 0;
      if (!this.onTop) {
        this.onTop = true; this.onTopT = 0;
        this.showBanner('ON TOP!', 'ricochet for glory', 1.6);
        AudioSys.sfx('breakthrough');
        FX.flash = 0.5; FX.shake(7);
      }
      this.onTopT += dt;
      // meter fills 2s per multiplier step, ramping 1x -> 2x -> 3x ...
      this.meter = (this.onTopT % 2) / 2;
      this.onTopMult = 1 + Math.floor(this.onTopT / 2);
    } else if (this.onTop) {
      // decay: ball back below the field — hold ~1s, then reset the state
      this.graceT += dt;
      if (this.graceT > 1) {
        this.onTop = false; this.onTopMult = 1; this.meter = 0; this.onTopT = 0; this.graceT = 0;
      }
    } else { this.graceT = 0; }
    AudioSys.setHeat(this.heat, this.onTop);
  },
  get heat() { return clamp((this.onTopMult - 1) * 0.8 + Math.min(this.chain, 6) * 0.25, 0, HEAT_MAX); },

  showBanner(text, sub, life) { this.banner = { t: 0, text, sub, life }; },

  // ---------- ball vs bricks ----------
  ballBrickCollide(b, br) {
    // circle-vs-AABB, returns push-out normal or null
    const nx = clamp(b.x, br.x, br.x + br.w), ny = clamp(b.y, br.y, br.y + br.h);
    const dx = b.x - nx, dy = b.y - ny;
    if (dx * dx + dy * dy > BALL_R * BALL_R) return null;
    if (dx === 0 && dy === 0) return { x: 0, y: b.vy > 0 ? -1 : 1 };
    const d = Math.hypot(dx, dy);
    return { x: dx / d, y: dy / d };
  },

  hitBrick(b, br, hitNormal) {
    const impact = br.angleSign > 0 ? Math.abs(Math.sin(Math.atan2(b.vy, Math.abs(b.vx)))) : Math.abs(Math.cos(Math.atan2(b.vy, Math.abs(b.vx))));
    const fast = b.speed >= ARMOR_SPEED;
    const inWindow = br.type === 'angle'
      ? (br.angleSign > 0 ? impact < ANGLE_WINDOW : impact > 1 - ANGLE_WINDOW) : false;
    let breaks = true, why = null;
    if (br.type === 'armored' && !fast) { breaks = false; why = 'armor'; }
    if (br.type === 'angle' && !inWindow && !fast) { breaks = false; why = 'angle'; }

    if (!breaks) {
      // deflect + feedback; fireball pierces through the "why"
      if (b.fire > 0) { breaks = true; }
      else {
        if (why === 'armor') { FX.sparks(br.cx, br.cy, -b.vx, -b.vy, '#cfd8ff', 12, 380); AudioSys.sfx('armorspark'); }
        else { FX.sparks(br.cx, br.cy, -b.vx, -b.vy, '#ffd23d', 8, 300); AudioSys.sfx('deflect'); }
        br.flash = 1;
        return false;
      }
    }

    // break it
    br.dead = true;
    br.flash = 1;
    this.chain++;
    const mult = this.chainMult();
    const pts = Math.round(br.basePoints * mult * (this.onTop ? this.onTopMult : 1));
    this.score += pts;
    FX.popup(br.cx, br.cy, '+' + pts, br.type === 'crown' ? '#ffd23d' : '#fff', br.type === 'crown' ? 26 : 16);
    if (this.chain >= 2) FX.popup(br.cx, br.cy - 24, 'x' + this.chain + ' CHAIN!', '#ff5db1', 15, 'chain');
    const it = clamp(this.heat / HEAT_MAX, 0, 1);
    FX.burst(br.cx, br.cy, this.brickColor(br), br.type === 'crown' ? 34 : 16, br.type === 'crown' ? 480 : 300, 0.7, 900);
    FX.ringPulse(br.cx, br.cy, br.type === 'crown' ? '#ffd23d' : '#ffffff', br.type === 'crown' ? 90 : 46);
    FX.shake(br.type === 'crown' ? 6 : 2 + it * 3);
    AudioSys.sfx(br.type === 'crown' ? 'crown' : 'brick', it);

    if (br.type === 'crown') this.triggerBreakthrough();
    this.maybeDrop(br.cx, br.cy);
    if (b.fire <= 0) this.bounceOffBrick(b, br, hitNormal);
    // last-brick slow-mo handled in checkLevelClear
    return true;
  },

  chainMult() { return 1 + Math.floor(this.chain / 2); },

  bounceOffBrick(b, br, n) {
    n = n || { x: 0, y: -1 };
    const dot = b.vx * n.x + b.vy * n.y;
    if (dot < 0) { b.vx -= 2 * dot * n.x; b.vy -= 2 * dot * n.y; }
    else { b.vx = -b.vx; b.vy = -b.vy; }
    b.x = clamp(b.x, br.x - BALL_R, br.x + br.w + BALL_R);
    b.y = clamp(b.y, br.y - BALL_R, br.y + br.h + BALL_R);
  },

  triggerBreakthrough() {
    this.onTop = true; this.onTopT = Math.max(this.onTopT, 0.01);
    this.graceT = 0;
    this.showBanner('BREAKTHROUGH!', 'the top is open', 2.2);
    FX.flash = 0.8; FX.shake(10);
    AudioSys.sfx('breakthrough');
  },

  // ---------- ball physics ----------
  updateBall(dt, b) {
    if (b.stuck) { b.x = this.paddle.x; b.y = this.paddle.y - BALL_R - 10; return; }
    if (!isFinite(b.vx) || !isFinite(b.vy) || !isFinite(b.x) || !isFinite(b.y)) {
      // numerical safety net: a NaN can never recover; re-serve instead of wedging
      b.vx = 0; b.vy = 0; b.x = this.paddle.x; b.y = this.paddle.y - BALL_R - 10; b.stuck = true; return;
    }
    const slow = this.effects.slow > 0 ? 0.55 : 1;
    b.x += b.vx * dt * slow; b.y += b.vy * dt * slow;

    // trail
    b.trail.push({ x: b.x, y: b.y });
    if (b.trail.length > 14 + (this.onTop ? 10 : 0)) b.trail.shift();

    // walls
    if (b.x < BALL_R) { b.x = BALL_R; b.vx = Math.abs(b.vx); this.wallHit(b); }
    if (b.x > W - BALL_R) { b.x = W - BALL_R; b.vx = -Math.abs(b.vx); this.wallHit(b); }
    if (b.y < BALL_R) { b.y = BALL_R; b.vy = Math.abs(b.vy); this.wallHit(b); }

    // paddle collision (only when moving down)
    const p = this.paddle;
    if (b.vy > 0 && b.y + BALL_R >= p.y - PADDLE_H / 2 && b.y - BALL_R < p.y + PADDLE_H &&
        Math.abs(b.x - p.x) <= p.w / 2 + BALL_R) {
      this.paddleHit(b);
    }

    // fell out the bottom
    if (b.y > H + 40) { b.dead = true; }
  },

  wallHit(b) { AudioSys.sfx('wall', clamp(this.heat / HEAT_MAX, 0, 1)); FX.sparks(b.x, b.y, b.vx, b.vy, '#8fa3c8', 4, 200); },

  paddleHit(b) {
    const p = this.paddle;
    // english guard: NaN would poison the whole sim from here
    if (!isFinite(p.vx) || !isFinite(p.vy)) { p.vx = 0; p.vy = 0; }
    b.y = p.y - PADDLE_H / 2 - BALL_R - 0.5;
    // angle from hit offset across the paddle
    const off = clamp((b.x - p.x) / (p.w / 2), -1, 1);
    let a = -Math.PI / 2 + off * 1.05; // up to ~60° off vertical
    // english: paddle horizontal velocity bends the exit angle
    a += clamp(p.vx * 0.00045, -0.35, 0.35);
    // speed: base + rally growth, boosted if paddle moving up (smash)
    let sp = Math.min(MAX_BALL_SPEED, Math.max(b.speed, BASE_BALL_SPEED * (1 + (this.level - 1) * 0.06)) + 14 + Math.max(0, p.vy) * 0.35);
    if (!isFinite(sp) || sp <= 0) sp = BASE_BALL_SPEED;
    b.vx = Math.cos(a) * sp; b.vy = Math.sin(a) * sp;
    b.enforceAngle();
    b.trail.length = 0;
    this.chain = 0; // chain resets on paddle touch

    const smash = p.vy > 120; // moving up at contact
    p.squash = 1;
    FX.ringPulse(b.x, b.y + 8, '#8fd0ff', smash ? 70 : 40);
    if (smash) {
      AudioSys.sfx('smash'); FX.shake(6);
      FX.sparks(b.x, b.y, 0, -1, '#8fd0ff', 16, 500);
      FX.popup(p.x, p.y - 34, 'SMASH!', '#8fd0ff', 18);
    } else AudioSys.sfx('paddle', clamp(this.heat / HEAT_MAX, 0, 1));

    // magnet: catch the ball for a re-aimed release
    if (this.effects.magnet > 0 && !b.stuck) {
      b.stuck = true; b.magnetHeld = true;
      AudioSys.sfx('magnetCatch');
      this.setState('serve');
    }
  },

  // ---------- main update ----------
  update(dt) {
    const input = Input.consume();

    // banner + level-intro timers tick in EVERY state — the early returns
    // below (levelclear, gameover, paused) must not freeze a banner
    // mid-display or block its fade-out (P1-1/P1-2)
    this.banner.t += dt;
    if (this.levelIntroT > 0) this.levelIntroT -= dt;

    if (input.key) AudioSys.ensure();

    if (this.state === 'menu') { if (input.launch || input.key) { AudioSys.ensure(); AudioSys.resume(); this.start(); } return; }
    if (this.state === 'paused') return;
    if (this.state === 'gameover') {
      this.overT += dt;
      if (this.overT > 1 && (input.launch || input.key)) this.start();
      return;
    }
    if (this.state === 'levelclear') {
      this.clearT += dt;
      this.paddle.update(dt, input); // keep the paddle alive & tilting during the clear cinematic
      if (this.clearT > 1.6) { this.level++; this.loadLevel(this.level); this.resetBall(); this.setState('serve'); }
      return;
    }
    if (this.state === 'serve' && (input.launch)) { this.launch(); }

    // paddle always active in serve/play
    this.paddle.update(dt, input);

    // release magnet-held balls on launch input
    if (this.state === 'serve' && input.launch) {
      for (const b of this.balls) if (b.stuck && b.magnetHeld) {
        b.stuck = false; b.magnetHeld = false;
        const a = rand(-0.5, 0.5) - Math.PI / 2, sp = Math.max(b.speed, BASE_BALL_SPEED);
        b.vx = Math.cos(a) * sp; b.vy = Math.sin(a) * sp;
      }
    }

    const scale = this.effects.slow > 0 ? 0.55 : 1;
    // sub-step balls for fast movement (avoid tunneling at high speed)
    for (const b of this.balls) {
      const steps = Math.max(1, Math.ceil(b.speed * dt * scale / (BALL_R * 0.8)));
      const sdt = dt / steps;
      for (let s = 0; s < steps; s++) {
        if (b.dead) break;
        this.updateBall(sdt, b);
        if (b.stuck) break;
        // brick collisions
        for (const br of this.bricks) {
          if (br.dead) continue;
          const n = this.ballBrickCollide(b, br);
          if (n) { this.hitBrick(b, br, n); if (b.fire <= 0) break; }
        }
      }
    }
    this.balls = this.balls.filter(b => !b.dead);

    // power-up tokens fall & are caught
    for (const pu of this.powerups) {
      pu.t += dt; pu.y += pu.vy * dt; pu.vy = Math.min(pu.vy + 60 * dt, 260);
      if (pu.y > H + 30) pu.dead = true;
      const p = this.paddle;
      if (!pu.dead && pu.y > p.y - PADDLE_H && pu.y < p.y + PADDLE_H + 14 &&
          Math.abs(pu.x - p.x) < p.w / 2 + 16) { pu.dead = true; this.applyPowerUp(pu.type); FX.ringPulse(p.x, p.y, pu.style.color, 60); }
    }
    this.powerups = this.powerups.filter(pu => !pu.dead);

    // timed effects
    for (const k of ['wide', 'slow', 'magnet', 'fire']) {
      if (this.effects[k] > 0) {
        this.effects[k] -= dt;
        if (this.effects[k] <= 0) {
          this.effects[k] = 0;
          if (k === 'wide') { this.paddle.baseW = PADDLE_W; }
          if (k === 'magnet') { this.paddle.magnet = 0; }
          if (k === 'fire') { for (const b of this.balls) b.fire = 0; }
        }
      }
    }

    // breakthrough
    this.updateOnTop(dt);
    // tick sound as multiplier climbs
    if (this.onTop && Math.floor(this.onTopT / 2) !== Math.floor((this.onTopT - dt) / 2)) AudioSys.sfx('ontopTick', clamp(this.onTopMult / 5, 0, 1));

    // lives / level clear
    if (this.balls.length === 0 && this.state === 'play') this.loseLife();
    this.checkLevelClear();

    // ambient hue drift with heat
    FX.hue = approach(FX.hue, 225 - this.heat * 38, 0.2, dt);
  },

  loseLife() {
    this.lives--;
    this.chain = 0;
    this.onTop = false; this.onTopMult = 1; this.meter = 0; this.onTopT = 0;
    FX.timeWarp = 1; FX.shake(5);
    for (const k of ['wide', 'magnet', 'fire']) if (this.effects[k]) { this.effects[k] = 0; }
    this.paddle.baseW = PADDLE_W; this.paddle.magnet = 0;
    if (this.lives <= 0) {
      AudioSys.sfx('gameover');
      this.overT = 0;
      this.setState('gameover');
      this.highScore = Math.max(this.highScore, this.score);
      localStorage.setItem('crownline.high', String(this.highScore));
    } else {
      AudioSys.sfx('lose');
      this.showBanner('BALL LOST', this.lives + ' left', 1.4);
      this.resetBall();
      this.setState('serve');
    }
  },

  checkLevelClear() {
    if (this.state !== 'play' && this.state !== 'serve') return;
    const remaining = this.bricks.filter(b => !b.dead).length;
    if (remaining === 0) {
      FX.timeWarp = 1.2; FX.shake(12); FX.flash = 0.6;
      this.score += 1000 * this.level;
      this.showBanner('LEVEL ' + this.level + ' CLEAR!', '+ ' + (1000 * this.level) + ' bonus', 2.5);
      AudioSys.sfx('levelclear');
      this.clearT = 0;
      this.setState('levelclear');
    }
  },

  /* ============================== RENDER ============================== */
  render() {
    const ctx = ctx2d;
    ctx.save();
    ctx.clearRect(0, 0, W, H);
    // shake
    ctx.translate(FX.shakeX, FX.shakeY);

    // background: hue drifts with heat
    const h = FX.hue;
    const grad = ctx.createLinearGradient(0, 0, 0, H);
    grad.addColorStop(0, `hsl(${h}, 45%, ${6 + (this.onTop ? 4 : 0)}%)`);
    grad.addColorStop(1, `hsl(${h + 30}, 40%, 3%)`);
    ctx.fillStyle = grad; ctx.fillRect(-20, -20, W + 40, H + 40);

    // starfield: density/speed react to on-top
    const spd = this.onTop ? 3 + this.onTopMult : 0.4;
    for (const s of this.stars) {
      s.y += s.z * spd * 0.6; s.tw += 0.05;
      if (s.y > H) { s.y = -2; s.x = rand(0, W); }
      const a = (0.25 + 0.35 * Math.sin(s.tw)) * (this.onTop ? 0.9 : 0.5) * s.z;
      ctx.fillStyle = `hsla(${h + 200}, 60%, 80%, ${a})`;
      ctx.fillRect(s.x, s.y, s.z * 2.2, s.z * 2.2);
    }

    // brick field top line — the frontier
    ctx.strokeStyle = `hsla(${h + 180}, 80%, 70%, ${this.onTop ? 0.9 : 0.3})`;
    ctx.lineWidth = this.onTop ? 3 : 1.5;
    ctx.beginPath(); ctx.moveTo(0, FIELD.top - 6); ctx.lineTo(W, FIELD.top - 6); ctx.stroke();

    // bricks
    for (const br of this.bricks) if (!br.dead) this.drawBrick(ctx, br);

    // power-ups
    for (const pu of this.powerups) this.drawPowerUp(ctx, pu);

    // balls + trails
    for (const b of this.balls) {
      const tr = b.trail;
      for (let i = 0; i < tr.length; i++) {
        const a = (i / tr.length) * (b.fire > 0 ? 0.7 : 0.45);
        ctx.fillStyle = b.fire > 0 ? `rgba(255,120,40,${a})` : `rgba(255,255,255,${a * 0.7})`;
        const r = BALL_R * (i / tr.length);
        ctx.beginPath(); ctx.arc(tr[i].x, tr[i].y, Math.max(0.5, r), 0, TAU); ctx.fill();
      }
      if (b.fire > 0) { ctx.fillStyle = 'rgba(255,122,47,0.35)'; ctx.beginPath(); ctx.arc(b.x, b.y, BALL_R + 8 + Math.sin(performance.now() / 60) * 3, 0, TAU); ctx.fill(); }
      ctx.fillStyle = b.fire > 0 ? '#ffb36b' : '#ffffff';
      ctx.beginPath(); ctx.arc(b.x, b.y, BALL_R, 0, TAU); ctx.fill();
    }

    this.drawPaddle(ctx);

    // particles
    for (const p of FX.parts) {
      const a = 1 - p.t / p.life;
      ctx.globalAlpha = a; ctx.fillStyle = p.color;
      ctx.fillRect(p.x - p.size / 2, p.y - p.size / 2, p.size, p.size);
    }
    ctx.globalAlpha = 1;
    for (const r of FX.ring) {
      const t = r.t / r.life;
      ctx.strokeStyle = r.color; ctx.globalAlpha = 1 - t; ctx.lineWidth = 3 * (1 - t);
      ctx.beginPath(); ctx.arc(r.x, r.y, r.maxR * t, 0, TAU); ctx.stroke();
    }
    ctx.globalAlpha = 1;

    // heat vignette when on top
    if (this.onTop) {
      const v = ctx.createRadialGradient(W / 2, H / 2, H * 0.3, W / 2, H / 2, H * 0.75);
      v.addColorStop(0, 'rgba(255,180,60,0)');
      v.addColorStop(1, `rgba(255,150,40,${0.18 + 0.1 * Math.sin(performance.now() / 300)})`);
      ctx.fillStyle = v; ctx.fillRect(0, 0, W, H);
    }
    // on-top border glow
    if (this.onTop) {
      ctx.strokeStyle = `hsla(45, 100%, 60%, ${0.5 + 0.4 * Math.sin(performance.now() / 200)})`;
      ctx.lineWidth = 10; ctx.strokeRect(5, 5, W - 10, H - 10);
    }

    // HUD
    this.drawHUD(ctx);

    // floating score popups (above HUD so readable)
    ctx.textAlign = 'center';
    for (const p of FX.pops) {
      const t = p.t / p.life;
      ctx.globalAlpha = 1 - t * t;
      ctx.font = `800 ${p.size}px system-ui, sans-serif`;
      ctx.fillStyle = p.color;
      ctx.fillText(p.text, p.x, p.y);
    }
    ctx.globalAlpha = 1;

    // banner — drawn in serve/play/levelclear only, never over the
    // game-over screen or menu (its life is forced to 0 on gameover)
    if (this.banner.t < this.banner.life &&
        this.state !== 'gameover' && this.state !== 'menu') this.drawBanner(ctx);
    // slow-mo tint
    if (FX.timeWarp > 0) { ctx.fillStyle = `rgba(120,90,255,${FX.timeWarp * 0.16})`; ctx.fillRect(0, 0, W, H); }
    // white flash
    if (FX.flash > 0) { ctx.fillStyle = `rgba(255,255,255,${FX.flash * 0.5})`; ctx.fillRect(0, 0, W, H); }

    // overlays
    if (this.state === 'menu') this.drawMenu(ctx);
    if (this.state === 'paused') this.drawPause(ctx);
    if (this.state === 'gameover') this.drawGameOver(ctx);

    ctx.restore();
  },

  brickColor(br) {
    return { std: '#4da3ff', armored: '#aab6cc', angle: '#ffd23d', shifter: '#c46bff', crown: '#ffcc33' }[br.type];
  },
  drawBrick(ctx, br) {
    const col = this.brickColor(br);
    ctx.save();
    if (br.flash > 0) { ctx.fillStyle = `rgba(255,255,255,${br.flash})`; ctx.fillRect(br.x - 2, br.y - 2, br.w + 4, br.h + 4); }
    ctx.fillStyle = col;
    ctx.fillRect(br.x, br.y, br.w, br.h);
    // top highlight + border
    ctx.fillStyle = 'rgba(255,255,255,0.25)'; ctx.fillRect(br.x, br.y, br.w, 4);
    ctx.strokeStyle = 'rgba(0,0,0,0.35)'; ctx.strokeRect(br.x + 0.5, br.y + 0.5, br.w - 1, br.h - 1);

    if (br.type === 'crown') {
      // little crown zigzag in gold brick
      ctx.fillStyle = 'rgba(80,50,0,0.75)';
      ctx.beginPath();
      const x = br.x + br.w / 2, y = br.y + br.h / 2, s = 8;
      ctx.moveTo(x - s, y + s * 0.5); ctx.lineTo(x - s, y - s * 0.4); ctx.lineTo(x - s * 0.4, y + s * 0.1);
      ctx.lineTo(x, y - s * 0.7); ctx.lineTo(x + s * 0.4, y + s * 0.1); ctx.lineTo(x + s, y - s * 0.4);
      ctx.lineTo(x + s, y + s * 0.5); ctx.closePath(); ctx.fill();
      ctx.strokeStyle = `rgba(255,240,150,${0.5 + 0.4 * Math.sin(performance.now() / 250 + br.col)})`;
      ctx.lineWidth = 2; ctx.strokeRect(br.x + 1, br.y + 1, br.w - 2, br.h - 2);
    } else if (br.type === 'armored') {
      // steel plate: rivets + border
      ctx.strokeStyle = 'rgba(20,25,40,0.8)'; ctx.lineWidth = 2; ctx.strokeRect(br.x + 2, br.y + 2, br.w - 4, br.h - 4);
      ctx.fillStyle = 'rgba(255,255,255,0.5)';
      [[5, 5], [br.w - 5, 5], [5, br.h - 5], [br.w - 5, br.h - 5]].forEach(([dx, dy]) => { ctx.beginPath(); ctx.arc(br.x + dx, br.y + dy, 1.8, 0, TAU); ctx.fill(); });
    } else if (br.type === 'angle') {
      // stripes indicate the accepted impact window:
      // "\\" stripes = steep hits break it | "/" stripes = shallow hits break it
      ctx.save();
      ctx.beginPath(); ctx.rect(br.x, br.y, br.w, br.h); ctx.clip();
      ctx.strokeStyle = 'rgba(0,0,0,0.5)'; ctx.lineWidth = 3;
      for (let sx = (br.angleSign > 0 ? 4 : 10); sx < br.w; sx += 12) {
        ctx.beginPath();
        if (br.angleSign > 0) { ctx.moveTo(br.x + sx, br.y + br.h); ctx.lineTo(br.x + sx + 6, br.y); }
        else { ctx.moveTo(br.x + sx, br.y); ctx.lineTo(br.x + sx + 6, br.y + br.h); }
        ctx.stroke();
      }
      ctx.restore();
    } else if (br.type === 'shifter') {
      // shimmering dashed outline signals teleporting
      ctx.setLineDash([5, 4]);
      ctx.strokeStyle = `rgba(255,255,255,${0.5 + 0.4 * Math.sin(performance.now() / 180 + br.col)})`;
      ctx.lineWidth = 2; ctx.strokeRect(br.x + 2, br.y + 2, br.w - 4, br.h - 4);
      ctx.setLineDash([]);
    }
    ctx.restore();
  },
  drawPowerUp(ctx, pu) {
    ctx.save();
    ctx.translate(pu.x, pu.y);
    ctx.rotate(Math.sin(pu.t * 3) * 0.3);
    const pulse = 1 + Math.sin(pu.t * 6) * 0.12;
    ctx.scale(pulse, pulse);
    ctx.fillStyle = 'rgba(0,0,0,0.6)';
    ctx.beginPath(); ctx.arc(0, 0, 15, 0, TAU); ctx.fill();
    ctx.fillStyle = pu.style.color; ctx.strokeStyle = pu.style.color;
    ctx.lineWidth = 2.5; ctx.beginPath(); ctx.arc(0, 0, 15, 0, TAU); ctx.stroke();
    ctx.fillStyle = pu.style.color;
    ctx.font = '700 15px system-ui, sans-serif'; ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    ctx.fillText(pu.style.icon, 0, 1);
    ctx.restore();
  },
  drawPaddle(ctx) {
    const p = this.paddle;
    // motion-blur trail: ghost of recent position opposite velocity
    if (Math.abs(p.vx) > 250) {
      ctx.globalAlpha = 0.25;
      ctx.fillStyle = '#8fd0ff';
      ctx.fillRect(p.x - Math.sign(p.vx) * 18 - p.w / 2, p.y - PADDLE_H / 2, p.w, PADDLE_H);
      ctx.globalAlpha = 1;
    }
    ctx.save();
    ctx.translate(p.x, p.y);
    // tilt with horizontal velocity, squash on impact
    ctx.rotate(clamp(p.vx * 0.00012, -0.09, 0.09));
    const sq = p.squash;
    ctx.scale(1 + sq * 0.12, 1 - sq * 0.3);
    const g = ctx.createLinearGradient(0, -PADDLE_H / 2, 0, PADDLE_H / 2);
    g.addColorStop(0, '#cfe6ff'); g.addColorStop(0.5, '#5fa8ff'); g.addColorStop(1, '#2b5cb0');
    ctx.fillStyle = g;
    if (p.magnet) { ctx.strokeStyle = '#ffd23d'; ctx.lineWidth = 3; }
    ctx.fillRect(-p.w / 2, -PADDLE_H / 2, p.w, PADDLE_H);
    if (p.magnet) { ctx.strokeRect(-p.w / 2, -PADDLE_H / 2, p.w, PADDLE_H); }
    // core stripe
    ctx.fillStyle = 'rgba(255,255,255,0.6)';
    ctx.fillRect(-p.w / 2 + 4, -PADDLE_H / 2 + 3, p.w - 8, 3);
    ctx.restore();
  },
  drawHUD(ctx) {
    ctx.font = '700 16px system-ui, sans-serif';
    ctx.textAlign = 'left'; ctx.textBaseline = 'top';
    ctx.fillStyle = '#dfe8ff';
    ctx.fillText('SCORE ' + this.score, 16, 12);
    ctx.textAlign = 'center';
    ctx.fillStyle = '#8fa3c8';
    ctx.fillText('LVL ' + this.level, W / 2, 12);
    // lives as little balls, tweened scale on change handled implicitly
    ctx.textAlign = 'right';
    ctx.fillStyle = '#dfe8ff';
    let lx = W - 16;
    for (let i = 0; i < this.lives; i++) { ctx.beginPath(); ctx.arc(lx, 22, 7, 0, TAU); ctx.fillStyle = '#ffffff'; ctx.fill(); lx -= 20; }
    // high score small
    ctx.font = '600 12px system-ui, sans-serif';
    ctx.fillStyle = '#5a6a8a';
    ctx.fillText('HI ' + this.highScore, W - 16, 36);

    // chain indicator
    if (this.chain >= 2) {
      ctx.font = '800 20px system-ui, sans-serif';
      ctx.textAlign = 'left';
      ctx.fillStyle = '#ff5db1';
      ctx.fillText('x' + this.chainMult() + ' CHAIN', 16, 36);
    }

    // active timed effects bars
    let ey = 64;
    ctx.textAlign = 'left';
    for (const [k, label, color] of [['wide', 'WIDE', '#3dd6ff'], ['slow', 'SLOW-MO', '#9d6bff'], ['magnet', 'MAGNET', '#ffd23d'], ['fire', 'FIREBALL', '#ff7a2f']]) {
      const max = { wide: 12, slow: 6, magnet: 10, fire: 7 }[k];
      if (this.effects[k] > 0) {
        ctx.fillStyle = 'rgba(0,0,0,0.4)'; ctx.fillRect(16, ey, 130, 14);
        ctx.fillStyle = color; ctx.fillRect(16, ey, 130 * clamp(this.effects[k] / max, 0, 1), 14);
        ctx.fillStyle = '#04101f'; ctx.font = '700 10px system-ui, sans-serif';
        ctx.fillText(label, 22, ey + 3);
        ey += 18;
      }
    }

    // on-top meter: multiplier + countdown ring at right (placed below the
    // brick field so the ring never overlaps the top crown row)
    if (this.onTop) {
      const cx = W - 60, cy = FIELD.bottom + 44, R = 34;
      ctx.strokeStyle = 'rgba(255,255,255,0.25)'; ctx.lineWidth = 6;
      ctx.beginPath(); ctx.arc(cx, cy, R, 0, TAU); ctx.stroke();
      ctx.strokeStyle = '#ffd23d';
      ctx.beginPath(); ctx.arc(cx, cy, R, -Math.PI / 2, -Math.PI / 2 + TAU * this.meter); ctx.stroke();
      ctx.fillStyle = '#ffd23d'; ctx.font = '800 26px system-ui, sans-serif'; ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
      ctx.fillText(this.onTopMult + 'x', cx, cy);
      ctx.font = '700 11px system-ui, sans-serif'; ctx.fillStyle = '#fff';
      ctx.fillText('ON TOP', cx, cy + R + 14);
    }
  },
  drawBanner(ctx) {
    const b = this.banner;
    const tIn = clamp(b.t / 0.25, 0, 1), tOut = clamp((b.life - b.t) / 0.3, 0, 1);
    const a = Math.min(tIn, tOut);
    const scale = 0.7 + 0.3 * (1 - Math.pow(1 - tIn, 3)); // ease-out back-ish
    ctx.save();
    ctx.globalAlpha = a;
    ctx.translate(W / 2, H * 0.4);
    ctx.scale(scale, scale);
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    ctx.font = '900 54px system-ui, sans-serif';
    ctx.lineWidth = 8; ctx.strokeStyle = 'rgba(0,0,0,0.7)';
    ctx.strokeText(b.text, 0, 0);
    const g = ctx.createLinearGradient(0, -30, 0, 30);
    g.addColorStop(0, '#fff2c4'); g.addColorStop(1, '#ffb32f');
    ctx.fillStyle = g; ctx.fillText(b.text, 0, 0);
    if (b.sub) { ctx.font = '600 18px system-ui, sans-serif'; ctx.fillStyle = '#dfe8ff'; ctx.fillText(b.sub, 0, 42); }
    ctx.restore();
  },
  drawLevelIntro(ctx) {
    // kept for reference: the level intro now lives in the banner system
    // (showBanner in loadLevel) so it has a single owner + own expiry.
  },
  drawMenu(ctx) {
    ctx.fillStyle = 'rgba(4,5,13,0.72)'; ctx.fillRect(0, 0, W, H);
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    const t = performance.now() / 1000;
    ctx.save();
    ctx.translate(W / 2, H * 0.36 + Math.sin(t * 1.5) * 5);
    ctx.font = '900 76px system-ui, sans-serif';
    const g = ctx.createLinearGradient(-180, 0, 180, 0);
    g.addColorStop(0, '#ffd23d'); g.addColorStop(0.5, '#fff2c4'); g.addColorStop(1, '#ffb32f');
    ctx.fillStyle = g;
    ctx.fillText('CROWNLINE', 0, 0);
    ctx.restore();
    ctx.font = '600 18px system-ui, sans-serif';
    ctx.fillStyle = '#8fa3c8';
    ctx.fillText('break through the bricks. own the top.', W / 2, H * 0.36 + 60);
    ctx.font = '500 15px system-ui, sans-serif';
    ctx.fillStyle = '#5a6a8a';
    ctx.fillText('mouse / drag to move — space / tap to launch — P pause — M mute', W / 2, H * 0.58);
    ctx.font = '800 22px system-ui, sans-serif';
    ctx.fillStyle = '#ffffff';
    ctx.globalAlpha = 0.55 + 0.45 * Math.sin(t * 3);
    ctx.fillText('CLICK OR TAP TO START', W / 2, H * 0.68);
    ctx.globalAlpha = 1;
    if (this.highScore > 0) { ctx.font = '700 16px system-ui, sans-serif'; ctx.fillStyle = '#ffd23d'; ctx.fillText('HIGH SCORE ' + this.highScore, W / 2, H * 0.76); }
  },
  drawPause(ctx) {
    ctx.fillStyle = 'rgba(4,5,13,0.65)'; ctx.fillRect(0, 0, W, H);
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    ctx.font = '900 44px system-ui, sans-serif'; ctx.fillStyle = '#dfe8ff';
    ctx.fillText('PAUSED', W / 2, H * 0.42);
    ctx.font = '600 15px system-ui, sans-serif'; ctx.fillStyle = '#8fa3c8';
    ctx.fillText('P / ESC to resume — M mute', W / 2, H * 0.42 + 44);
  },
  drawGameOver(ctx) {
    const a = clamp(this.overT / 0.6, 0, 1);
    ctx.fillStyle = `rgba(4,5,13,${0.78 * a})`; ctx.fillRect(0, 0, W, H);
    if (a < 1) return;
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    ctx.font = '900 60px system-ui, sans-serif';
    ctx.fillStyle = '#ff5db1';
    ctx.fillText('GAME OVER', W / 2, H * 0.34);
    ctx.font = '700 24px system-ui, sans-serif';
    ctx.fillStyle = '#dfe8ff';
    ctx.fillText('SCORE ' + this.score, W / 2, H * 0.46);
    const isHi = this.score >= this.highScore && this.score > 0;
    ctx.font = '700 17px system-ui, sans-serif';
    ctx.fillStyle = isHi ? '#ffd23d' : '#8fa3c8';
    ctx.fillText(isHi ? '★ NEW HIGH SCORE! ★' : 'HIGH SCORE ' + this.highScore, W / 2, H * 0.53);
    if (this.overT > 1) {
      ctx.font = '800 20px system-ui, sans-serif';
      ctx.fillStyle = '#ffffff';
      ctx.globalAlpha = 0.55 + 0.45 * Math.sin(performance.now() / 250);
      ctx.fillText('CLICK OR TAP TO PLAY AGAIN', W / 2, H * 0.66);
      ctx.globalAlpha = 1;
    }
  },
};

/* ============================ BOOT & LOOP =============================== */
const canvas = document.getElementById('game');
const ctx2d = canvas.getContext('2d');
Game.init();

// fixed-timestep accumulator loop at 120 Hz physics
let last = performance.now(), acc = 0;
function frame(now) {
  let el = (now - last) / 1000; last = now;
  if (el > 0.25) el = 0.25; // tab-switch clamp
  acc += el;
  while (acc >= DT) { Game.update(DT); acc -= DT; }
  Game.render();
  requestAnimationFrame(frame);
}
requestAnimationFrame(frame);

// canvas scaling: letterbox fit, DPR-aware, crisp
function fit() {
  const dpr = Math.min(window.devicePixelRatio || 1, 2.5);
  const vw = window.innerWidth, vh = window.innerHeight;
  const s = Math.min(vw / W, vh / H);
  canvas.style.width = Math.floor(W * s) + 'px';
  canvas.style.height = Math.floor(H * s) + 'px';
  canvas.width = Math.floor(W * s * dpr);
  canvas.height = Math.floor(H * s * dpr);
  ctx2d.setTransform(canvas.width / W, 0, 0, canvas.height / H, 0, 0);
  ctx2d.imageSmoothingEnabled = true;
}
window.addEventListener('resize', fit);
fit();

// first gesture anywhere unlocks audio (autoplay policy); starts muted anyway
const unlock = () => { AudioSys.ensure(); AudioSys.resume(); };
window.addEventListener('pointerdown', unlock, { once: true });
window.addEventListener('keydown', unlock, { once: true });

/* ============================ DEBUG HOOKS ===============================
   window.__game for automated testing / verification.
========================================================================= */
window.__game = {
  start: () => { AudioSys.ensure(); Game.start(); },
  launch: () => Game.launch(),
  step(seconds) { // advance the sim deterministically without rendering
    let t = seconds;
    while (t > 0) { const d = Math.min(DT, t); Game.update(d); t -= d; }
  },
  state: () => ({
    state: Game.state, score: Game.score, lives: Game.lives, level: Game.level,
    chain: Game.chain, onTop: Game.onTop, onTopMult: Game.onTopMult,
    balls: Game.balls.map(b => ({ x: +b.x.toFixed(1), y: +b.y.toFixed(1), vx: +b.vx.toFixed(1), vy: +b.vy.toFixed(1), stuck: b.stuck, speed: +b.speed.toFixed(1) })),
    bricksLeft: Game.bricks.filter(b => !b.dead).length,
    crownsLeft: Game.bricks.filter(b => !b.dead && b.type === 'crown').length,
    effects: { ...Game.effects }, powerups: Game.powerups.map(p => p.type),
    highScore: Game.highScore, banner: Game.banner.text,
  }),
  setBallSpeed: (s) => { for (const b of Game.balls) b.setSpeed(s); },
  givePowerUp: (t) => { if (POWERUP_TYPES.includes(t)) Game.applyPowerUp(t); else throw new Error('unknown powerup ' + t); },
  fastForward: (s) => { for (let i = 0; i < s / DT; i++) Game.update(DT); },
  // testing utilities
  breakBrick: (i) => { const alive = Game.bricks.filter(b => !b.dead); if (alive[i]) { alive[i].dead = true; } },
  updateOnTop: (dt) => Game.updateOnTop(dt),
  checkLevelClear: () => Game.checkLevelClear(),
  loseLife: () => Game.loseLife(),
  killBalls: () => { Game.balls.length = 0; Game.loseLife(); },
  clearCrowns: () => { for (const b of Game.bricks) if (b.type === 'crown') b.dead = true; },
  clearAll: () => { for (const b of Game.bricks) b.dead = true; },
  movePaddle: (x, y) => { Game.paddle.x = clamp(x, 0, W); if (y != null) Game.paddle.y = clamp(y, PADDLE_BAND_TOP, H); },
  setOnTop: () => { for (const b of Game.balls) { b.y = FIELD.top - 60; } Game.updateOnTop(DT); },
  dropBall: () => { for (const b of Game.balls) b.y = H + 60; Game.balls = Game.balls.filter(b => !b.dead); Game.loseLife(); },
  crownPoints: CROWN_SCORE, ARMOR_SPEED, ANGLE_WINDOW,
};
