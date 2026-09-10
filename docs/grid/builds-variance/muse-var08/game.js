/* ============================================================================
   OVER THE TOP — a modern Breakout
   Vanilla JS + Canvas + WebAudio. No dependencies. No build step.
   Logical resolution: W x H, scaled by devicePixelRatio + CSS.
   Sections:
     1. Config & utils          5. Bricks            9.  Collisions
     2. Audio engine            6. Powerups          10. Over-the-top
     3. Input (mouse/kb/touch)  7. Levels            11. Render & background
     4. Paddle & balls          8. Game state/order  12. HUD/menus/loop/boot
   ============================================================================ */
'use strict';

/* ============================ 1. CONFIG & UTILS ============================ */
const W = 480, H = 760;
const WALL = 10;                 // side wall thickness (visual)
const TOP_Y = 64;                // HUD clearance: playfield top
const DEATH_Y = H + 30;
const PADDLE_ZONE_TOP = H - 190; // paddle may roam vertically in this band
const PADDLE_ZONE_BOT = H - 46;
const BALL_R = 7;
const BASE_BALL_SPEED = 430;
const MAX_BALL_SPEED = 980;
const SPEED_BRICK_THRESHOLD = 520;   // speed bricks need ball faster than this
const HEAVY_SPEED_THRESHOLD = 380;   // heavy balls smash speed bricks easier
const STEEP_MIN = 0.52;              // |vy|/speed needed for angle bricks (~31deg)
const COMBO_PER_MULT = 6;
const MULT_CAP_GROUND = 8, MULT_CAP_OTT = 64;

const clamp = (v, a, b) => v < a ? a : v > b ? b : v;
const lerp = (a, b, t) => a + (b - a) * t;
const rand = (a, b) => a + Math.random() * (b - a);
const randi = (a, b) => Math.floor(rand(a, b + 1));
const choice = arr => arr[(Math.random() * arr.length) | 0];
const TAU = Math.PI * 2;
function dist2(ax, ay, bx, by) { const dx = ax - bx, dy = ay - by; return dx * dx + dy * dy; }

/* Screen shake trauma + hit-stop freeze live here so all systems can use them */
const Juice = {
  trauma: 0, freeze: 0, slowmo: 0, slowmoScale: 1,
  shake(mag) { this.trauma = clamp(this.trauma + mag, 0, 1); },
  hitstop(t) { this.freeze = Math.max(this.freeze, t); },
  getShake() { return this.trauma * this.trauma; },
  update(dt) {
    this.trauma = Math.max(0, this.trauma - dt * 1.6);
    if (this.freeze > 0) this.freeze -= dt;
    if (this.slowmo > 0) { this.slowmo -= dt; if (this.slowmo <= 0) this.slowmoScale = 1; }
  }
};
function slowMo(dur, scale) { Juice.slowmo = dur; Juice.slowmoScale = scale; }

/* Floating score texts */
const Floaters = [];
function addFloater(x, y, text, color, size) {
  Floaters.push({ x, y, text, color: color || '#fff', size: size || 15, life: 1.1, max: 1.1, vy: -66 });
  if (Floaters.length > 60) Floaters.shift();
}
function updateFloaters(dt) {
  for (let i = Floaters.length - 1; i >= 0; i--) {
    const f = Floaters[i]; f.life -= dt; f.y += f.vy * dt; f.vy *= (1 - dt * 1.2);
    if (f.life <= 0) Floaters.splice(i, 1);
  }
}

/* Announcements (combo callouts) */
let announceTimer = 0;
function announce(text, dur) {
  const el = document.getElementById('announce');
  el.textContent = text; el.classList.remove('hidden');
  el.style.transform = 'scale(1.25)'; el.style.opacity = '1';
  requestAnimationFrame(() => { el.style.transform = 'scale(1)'; });
  announceTimer = dur || 1.4;
}
function updateAnnounce(dt) {
  if (announceTimer > 0) {
    announceTimer -= dt;
    if (announceTimer <= 0) document.getElementById('announce').classList.add('hidden');
  }
}
function hint(text, dur) {
  const el = document.getElementById('hint-bar');
  el.textContent = text; el.classList.remove('hidden');
  clearTimeout(hint._t);
  hint._t = setTimeout(() => el.classList.add('hidden'), (dur || 1.2) * 1000);
}

/* Particle system: shards, sparks, shockwaves, embers, rain */
const Particles = [];
function spawnParticle(p) {
  if (Particles.length > 900) Particles.splice(0, Particles.length - 900);
  Particles.push(Object.assign({ life: 0.6, max: 0.6, vx: 0, vy: 0, size: 3, color: '#fff', grav: 0, drag: 0, shape: 'rect', rot: rand(0, TAU), vr: rand(-8, 8), glow: false }, p));
}
function brickShatter(x, y, color, big) {
  const n = big ? 22 : 12;
  for (let i = 0; i < n; i++) {
    const a = rand(0, TAU), sp = rand(60, big ? 420 : 300);
    spawnParticle({ x, y, vx: Math.cos(a) * sp, vy: Math.sin(a) * sp - 60, size: rand(2, big ? 7 : 5), color: Math.random() < 0.3 ? '#ffffff' : color, grav: 900, drag: 1.2, life: rand(0.4, 0.9), max: 0.9, shape: 'rect', glow: true });
  }
  for (let i = 0; i < (big ? 10 : 5); i++) {
    const a = rand(0, TAU), sp = rand(100, 380);
    spawnParticle({ x, y, vx: Math.cos(a) * sp, vy: Math.sin(a) * sp, size: rand(1, 2.5), color: '#fff8d0', grav: 0, drag: 2.5, life: rand(0.2, 0.5), max: 0.5, shape: 'spark', glow: true });
  }
  shockwave(x, y, color, big ? 70 : 40);
}
function shockwave(x, y, color, r) {
  Particles.push({ shape: 'ring', x, y, vx: 0, vy: 0, size: 6, color, life: 0.45, max: 0.45, grav: 0, drag: 0, rot: 0, vr: 0, glow: true, grow: (r || 40) });
}
function trailPuff(x, y, color, size) {
  spawnParticle({ x: x + rand(-3, 3), y: y + rand(-3, 3), vx: rand(-30, 30), vy: rand(-30, 30), size: size || rand(2, 4), color, life: rand(0.25, 0.5), max: 0.5, shape: 'circle', glow: true });
}
function burnEmber(x, y) {
  spawnParticle({ x: x + rand(-5, 5), y: y + rand(-2, 6), vx: rand(-40, 40), vy: rand(-140, -40), size: rand(1.5, 3.5), color: choice(['#ff7b2e', '#ffb02e', '#ff3d2e', '#ffe08a']), life: rand(0.3, 0.7), max: 0.7, shape: 'circle', glow: true });
}
function voltSpark(x, y) {
  spawnParticle({ x, y, vx: rand(-220, 220), vy: rand(-220, 220), size: rand(1, 2.5), color: choice(['#fff36b', '#c8ff4d', '#ffffff']), life: rand(0.15, 0.4), max: 0.4, shape: 'spark', glow: true });
}
function ghostWisp(x, y) {
  spawnParticle({ x: x + rand(-6, 6), y: y + rand(-6, 6), vx: rand(-25, 25), vy: rand(-70, -20), size: rand(2, 5), color: '#c09aff', life: rand(0.4, 0.8), max: 0.8, shape: 'circle', glow: true });
}
function ottRain() {
  spawnParticle({ x: rand(WALL + 6, W - WALL - 6), y: -12, vx: rand(-40, 40), vy: rand(120, 320), size: rand(2, 5), color: choice(['#ffd23d', '#ff3df0', '#38e1ff', '#7dff5e', '#ffffff']), life: rand(0.8, 1.6), max: 1.6, shape: Math.random() < 0.4 ? 'note' : 'rect', grav: 200, glow: true });
}
function updateParticles(dt) {
  for (let i = Particles.length - 1; i >= 0; i--) {
    const p = Particles[i]; p.life -= dt;
    if (p.life <= 0) { Particles.splice(i, 1); continue; }
    if (p.shape === 'ring') { p.size += (p.grow || 40) * dt * 4; continue; }
    p.vy += (p.grav || 0) * dt;
    if (p.drag) { const d = 1 - Math.min(0.9, p.drag * dt); p.vx *= d; p.vy *= d; }
    p.x += p.vx * dt; p.y += p.vy * dt; p.rot += (p.vr || 0) * dt;
  }
}

/* Starfield background (reacts to heat) */
const Stars = [];
function initStars() {
  Stars.length = 0;
  for (let i = 0; i < 110; i++) Stars.push({ x: rand(0, W), y: rand(0, H), z: rand(0.25, 1), tw: rand(0, TAU) });
}
function updateStars(dt, heat, ott) {
  const fall = (8 + heat * 60 + (ott ? 120 : 0));
  for (const s of Stars) {
    s.y += fall * s.z * dt; s.tw += dt * 3;
    if (s.y > H) { s.y = -4; s.x = rand(0, W); }
  }
}
/* ============================ 2. AUDIO ENGINE ================================
   All synthesized with WebAudio. No assets. SFX + adaptive procedural music. */
const AudioSys = {
  ctx: null, master: null, musicGain: null, sfxGain: null,
  muted: false, started: false,
  musicTimer: 0, beat: 0, intensity: 0, ottMode: false,
  bassNotes: [55, 55, 65.41, 49], // A1 A1 C2 G1 groove
  init() {
    if (this.ctx) return;
    const AC = window.AudioContext || window.webkitAudioContext;
    if (!AC) return;
    this.ctx = new AC();
    this.master = this.ctx.createGain(); this.master.gain.value = 0.9; this.master.connect(this.ctx.destination);
    this.sfxGain = this.ctx.createGain(); this.sfxGain.gain.value = 0.8; this.sfxGain.connect(this.master);
    this.musicGain = this.ctx.createGain(); this.musicGain.gain.value = 0.30; this.musicGain.connect(this.master);
    this.started = true;
  },
  resume() { if (this.ctx && this.ctx.state === 'suspended') this.ctx.resume(); },
  toggleMute() {
    this.muted = !this.muted;
    if (this.master) this.master.gain.value = this.muted ? 0 : 0.9;
    document.getElementById('btn-mute').textContent = this.muted ? '🔇' : '🔊';
    return this.muted;
  },
  now() { return this.ctx ? this.ctx.currentTime : 0; },
  env(gainNode, t, peak, decay) {
    gainNode.gain.setValueAtTime(0.0001, t);
    gainNode.gain.exponentialRampToValueAtTime(Math.max(0.001, peak), t + 0.008);
    gainNode.gain.exponentialRampToValueAtTime(0.0001, t + decay);
  },
  tone(freq, dur, type, vol, slideTo) {
    if (!this.ctx || this.muted) return;
    const t = this.now(), o = this.ctx.createOscillator(), g = this.ctx.createGain();
    o.type = type || 'square'; o.frequency.setValueAtTime(freq, t);
    if (slideTo) o.frequency.exponentialRampToValueAtTime(Math.max(20, slideTo), t + dur);
    this.env(g, t, vol || 0.25, dur);
    o.connect(g); g.connect(this.sfxGain); o.start(t); o.stop(t + dur + 0.05);
  },
  noise(dur, vol, filterFreq, filterType) {
    if (!this.ctx || this.muted) return;
    const t = this.now(), len = Math.max(1, (dur * this.ctx.sampleRate) | 0);
    const buf = this.ctx.createBuffer(1, len, this.ctx.sampleRate);
    const d = buf.getChannelData(0);
    for (let i = 0; i < len; i++) d[i] = Math.random() * 2 - 1;
    const src = this.ctx.createBufferSource(); src.buffer = buf;
    const f = this.ctx.createBiquadFilter(); f.type = filterType || 'lowpass'; f.frequency.value = filterFreq || 1200;
    const g = this.ctx.createGain(); this.env(g, t, vol || 0.3, dur);
    src.connect(f); f.connect(g); g.connect(this.sfxGain); src.start(t); src.stop(t + dur + 0.05);
  },
  paddleBlip(offset, paddleVy) {
    const base = 340 + Math.abs(offset) * 260 + clamp(-paddleVy, 0, 600) * 0.25;
    this.tone(base, 0.09, 'square', 0.20, base * 1.4);
  },
  wallBlip() { this.tone(220, 0.06, 'square', 0.12, 300); },
  brickHit() { this.tone(520, 0.07, 'square', 0.18, 760); },
  brickShatter(big) { this.noise(big ? 0.35 : 0.22, big ? 0.4 : 0.3, big ? 3200 : 2400, 'highpass'); this.tone(big ? 180 : 260, 0.2, 'sawtooth', 0.16, 60); },
  brickResist() { this.tone(140, 0.15, 'sawtooth', 0.2, 90); },
  explosion() {
    this.noise(0.7, 0.55, 900, 'lowpass'); this.noise(0.3, 0.35, 5000, 'highpass');
    this.tone(90, 0.6, 'sine', 0.5, 32);
  },
  powerup() {
    if (!this.ctx || this.muted) return;
    const t = this.now();
    [523, 659, 784, 1046].forEach((f, i) => {
      const o = this.ctx.createOscillator(), g = this.ctx.createGain();
      o.type = 'square'; o.frequency.value = f;
      const st = t + i * 0.07; this.env(g, st, 0.16, 0.18);
      o.connect(g); g.connect(this.sfxGain); o.start(st); o.stop(st + 0.25);
    });
  },
  powerDown() { this.tone(400, 0.3, 'sawtooth', 0.2, 120); },
  launch() { this.tone(300, 0.18, 'square', 0.22, 700); },
  lifeLost() {
    this.tone(330, 0.5, 'sawtooth', 0.3, 80);
    this.noise(0.4, 0.25, 600, 'lowpass');
  },
  levelClear() {
    if (!this.ctx || this.muted) return;
    const t = this.now();
    [523, 659, 784, 1046, 1318].forEach((f, i) => {
      const o = this.ctx.createOscillator(), g = this.ctx.createGain();
      o.type = 'triangle'; o.frequency.value = f;
      const st = t + i * 0.1; this.env(g, st, 0.25, 0.35);
      o.connect(g); g.connect(this.musicGain); o.start(st); o.stop(st + 0.4);
    });
  },
  bankFanfare() {
    if (!this.ctx || this.muted) return;
    const t = this.now();
    [784, 988, 1175, 1568, 2093].forEach((f, i) => {
      const o = this.ctx.createOscillator(), g = this.ctx.createGain();
      o.type = 'square'; o.frequency.value = f;
      const st = t + i * 0.08; this.env(g, st, 0.18, 0.3);
      o.connect(g); g.connect(this.sfxGain); o.start(st); o.stop(st + 0.35);
    });
    this.noise(0.5, 0.2, 6000, 'highpass');
  },
  gameOver() {
    if (!this.ctx || this.muted) return;
    const t = this.now();
    [392, 330, 262, 196].forEach((f, i) => {
      const o = this.ctx.createOscillator(), g = this.ctx.createGain();
      o.type = 'sawtooth'; o.frequency.value = f;
      const st = t + i * 0.22; this.env(g, st, 0.25, 0.4);
      o.connect(g); g.connect(this.sfxGain); o.start(st); o.stop(st + 0.45);
    });
  },
  ottTick(mult) {
    const f = 300 + Math.log2(Math.max(1, mult)) * 160;
    this.tone(f, 0.12, 'square', 0.16, f * 1.5);
  },
  riser() { this.noise(0.9, 0.18, 4000, 'highpass'); this.tone(120, 0.9, 'sawtooth', 0.1, 900); },
  /* --- adaptive music: called every frame; schedules notes on a step grid --- */
  musicUpdate(dt) {
    if (!this.ctx || this.muted || !Game.running || Game.paused) return;
    this.musicTimer -= dt;
    if (this.musicTimer > 0) return;
    const heat = clamp((Game.multiplier - 1) / 15, 0, 1);
    const ott = Game.ott.active;
    const stepDur = ott ? 0.135 : lerp(0.22, 0.15, heat);
    this.musicTimer = stepDur;
    this.beat = (this.beat + 1) % 16;
    const t = this.now();
    const play = (freq, dur, type, vol, dest) => {
      const o = this.ctx.createOscillator(), g = this.ctx.createGain();
      o.type = type; o.frequency.value = freq;
      this.env(g, t, vol, dur);
      o.connect(g); g.connect(dest || this.musicGain); o.start(t); o.stop(t + dur + 0.05);
    };
    // bass groove every 4 steps
    if (this.beat % 4 === 0) {
      const n = this.bassNotes[(this.beat / 4) | 0];
      play(n, 0.3, 'triangle', 0.5);
      play(n * 2, 0.2, 'sine', 0.25);
    }
    // hats
    if (this.beat % 2 === 1 && (heat > 0.15 || ott)) {
      const len = (0.05 * this.ctx.sampleRate) | 0;
      const buf = this.ctx.createBuffer(1, len, this.ctx.sampleRate);
      const d = buf.getChannelData(0);
      for (let i = 0; i < len; i++) d[i] = (Math.random() * 2 - 1) * (1 - i / len);
      const src = this.ctx.createBufferSource(); src.buffer = buf;
      const g = this.ctx.createGain(); g.gain.value = ott ? 0.35 : 0.2;
      const f = this.ctx.createBiquadFilter(); f.type = 'highpass'; f.frequency.value = 7000;
      src.connect(f); f.connect(g); g.connect(this.musicGain); src.start(t);
    }
    // arp layer unlocks with multiplier
    const layers = ott ? 3 : heat > 0.55 ? 2 : heat > 0.2 ? 1 : 0;
    if (layers >= 1 && this.beat % 2 === 0) {
      const scale = [220, 261.6, 329.6, 440, 523.25, 659.25];
      play(scale[(this.beat + ((Game.level - 1) * 2)) % scale.length], 0.18, 'square', 0.10);
    }
    if (layers >= 2 && this.beat % 4 === 2) {
      play(440 * Math.pow(2, (this.beat % 8) / 12), 0.22, 'sawtooth', 0.07);
    }
    if (layers >= 3) {
      const scale = [880, 1046, 1175, 1318, 1568];
      play(scale[(Math.random() * scale.length) | 0], 0.12, 'square', 0.08);
    }
    // OTT pulse bass
    if (ott && this.beat % 2 === 0) play(110, 0.12, 'sawtooth', 0.2);
  }
};

/* ==================== 3. INPUT: MOUSE / KEYS / THUMBSTICK =================== */
const Input = {
  pointerActive: false, pointerX: W / 2, pointerY: PADDLE_ZONE_BOT - 60,
  keys: {}, usingTouch: false,
  stick: { active: false, id: -1, ox: 0, oy: 0, dx: 0, dy: 0 },
  keyLeft: false, keyRight: false, keyUp: false, keyDown: false,
  init(canvas) {
    window.addEventListener('keydown', e => {
      if (['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', ' '].includes(e.key)) e.preventDefault();
      this.keys[e.key.toLowerCase()] = true;
      this.keyLeft = !!(this.keys['arrowleft'] || this.keys['a']);
      this.keyRight = !!(this.keys['arrowright'] || this.keys['d']);
      this.keyUp = !!(this.keys['arrowup'] || this.keys['w']);
      this.keyDown = !!(this.keys['arrowdown'] || this.keys['s']);
      if (e.key === ' ' ) Game.tryLaunch();
      if (e.key === 'p' || e.key === 'P' || e.key === 'Escape') Game.togglePause();
      if (e.key === 'm' || e.key === 'M') AudioSys.toggleMute();
      if (e.key === 'Enter' && !Game.running) Game.startFromTitle();
    });
    window.addEventListener('keyup', e => {
      this.keys[e.key.toLowerCase()] = false;
      this.keyLeft = !!(this.keys['arrowleft'] || this.keys['a']);
      this.keyRight = !!(this.keys['arrowright'] || this.keys['d']);
      this.keyUp = !!(this.keys['arrowup'] || this.keys['w']);
      this.keyDown = !!(this.keys['arrowdown'] || this.keys['s']);
    });
    canvas.addEventListener('pointermove', e => {
      if (e.pointerType === 'touch') return;
      const r = canvas.getBoundingClientRect();
      this.pointerX = (e.clientX - r.left) / r.width * W;
      this.pointerY = (e.clientY - r.top) / r.height * H;
      this.pointerActive = true; this.usingTouch = false;
    });
    canvas.addEventListener('pointerdown', e => {
      AudioSys.init(); AudioSys.resume();
      if (e.pointerType === 'touch') { this.stickStart(e); return; }
      const r = canvas.getBoundingClientRect();
      this.pointerX = (e.clientX - r.left) / r.width * W;
      this.pointerY = (e.clientY - r.top) / r.height * H;
      this.pointerActive = true;
      Game.tryLaunch();
    });
    canvas.addEventListener('touchstart', e => { e.preventDefault(); AudioSys.init(); AudioSys.resume(); this.usingTouch = true; }, { passive: false });
    canvas.addEventListener('touchmove', e => { e.preventDefault(); }, { passive: false });
    canvas.addEventListener('touchend', e => { e.preventDefault(); this.stickEnd(); }, { passive: false });
    // global touch handlers for floating stick (drag anywhere)
    const stage = document.getElementById('stage');
    stage.addEventListener('touchstart', e => {
      AudioSys.init(); AudioSys.resume(); this.usingTouch = true;
      const t = e.changedTouches[0];
      if (this.stick.id === -1) this.stickStartTouch(t.clientX, t.clientY);
    }, { passive: true });
    stage.addEventListener('touchmove', e => {
      for (const t of e.changedTouches) {
        if (t.identifier === this.stick.id) this.stickMoveTouch(t.clientX, t.clientY);
      }
    }, { passive: true });
    const endTouch = e => {
      for (const t of e.changedTouches) if (t.identifier === this.stick.id) this.stickEnd();
    };
    stage.addEventListener('touchend', endTouch); stage.addEventListener('touchcancel', endTouch);
  },
  stickStart(e) {
    const stage = document.getElementById('stage');
    const r = stage.getBoundingClientRect();
    this.stickStartTouch(e.clientX, e.clientY, r);
  },
  stickStartTouch(cx, cy, r) {
    const stage = document.getElementById('stage');
    r = r || stage.getBoundingClientRect();
    this.stick.active = true; this.stick.id = 0;
    // store origin in CSS px relative to stage
    this.stick.ox = cx - r.left; this.stick.oy = cy - r.top;
    this.stick.dx = 0; this.stick.dy = 0;
    this.stick.baseX = Paddle.x; this.stick.baseY = Paddle.y;
    this.showStick(this.stick.ox, this.stick.oy, 0, 0);
    Game.tryLaunch();
  },
  stickMoveTouch(cx, cy) {
    const stage = document.getElementById('stage');
    const r = stage.getBoundingClientRect();
    const px = cx - r.left, py = cy - r.top;
    const scaleX = W / r.width, scaleY = H / r.height;
    // stick vector in game units, clamped to radius
    let dx = (px - this.stick.ox) * scaleX, dy = (py - this.stick.oy) * scaleY;
    const max = 90, len = Math.hypot(dx, dy);
    if (len > max) { dx = dx / len * max; dy = dy / len * max; }
    this.stick.dx = dx; this.stick.dy = dy;
    // relative paddle control: base + stick * sensitivity
    Paddle.targetX = clamp(this.stick.baseX + dx * 2.2, WALL + Paddle.w / 2, W - WALL - Paddle.w / 2);
    Paddle.targetY = clamp(this.stick.baseY + dy * 2.2, PADDLE_ZONE_TOP, PADDLE_ZONE_BOT);
    this.showStick(this.stick.ox, this.stick.oy, (px - this.stick.ox), (py - this.stick.oy));
  },
  stickEnd() {
    this.stick.active = false; this.stick.id = -1;
    this.stick.baseX = Paddle.x; this.stick.baseY = Paddle.y;
    document.getElementById('stick').classList.add('hidden');
  },
  showStick(ox, oy, kx, ky) {
    const el = document.getElementById('stick');
    el.classList.remove('hidden');
    const base = document.getElementById('stick-base');
    base.style.left = ox + 'px'; base.style.top = oy + 'px';
    const knob = document.getElementById('stick-knob');
    const max = 36, len = Math.hypot(kx, ky);
    let nx = kx, ny = ky;
    if (len > max) { nx = kx / len * max; ny = ky / len * max; }
    knob.style.transform = `translate(calc(-50% + ${nx}px), calc(-50% + ${ny}px))`;
  }
};
/* ==================== 4. PADDLE & BALLS ==================== */
const Paddle = {
  x: W / 2, y: PADDLE_ZONE_BOT - 60, targetX: W / 2, targetY: PADDLE_ZONE_BOT - 60,
  w: 110, h: 15, baseW: 110, vx: 0, vy: 0, px: W / 2, py: 0,
  squash: 0, trail: [], glow: 0,
  reset(wide) {
    this.w = wide ? this.baseW * 1.5 : this.baseW;
    this.x = this.targetX = W / 2; this.y = this.targetY = PADDLE_ZONE_BOT - 60;
    this.vx = 0; this.vy = 0; this.trail.length = 0;
  },
  update(dt) {
    this.px = this.x; this.py = this.y;
    const KB = 560; // keyboard speed px/s
    if (Input.keyLeft) this.targetX -= KB * dt;
    if (Input.keyRight) this.targetX += KB * dt;
    if (Input.keyUp) this.targetY -= KB * 0.62 * dt;
    if (Input.keyDown) this.targetY += KB * 0.62 * dt;
    // mouse steering (only when no keys/stick driving)
    if (Input.pointerActive && !Input.usingTouch && !Input.keyLeft && !Input.keyRight && !Input.keyUp && !Input.keyDown) {
      this.targetX = clamp(Input.pointerX, WALL + this.w / 2, W - WALL - this.w / 2);
      this.targetY = clamp(Input.pointerY, PADDLE_ZONE_TOP, PADDLE_ZONE_BOT);
    }
    this.targetX = clamp(this.targetX, WALL + this.w / 2, W - WALL - this.w / 2);
    this.targetY = clamp(this.targetY, PADDLE_ZONE_TOP, PADDLE_ZONE_BOT);
    // smoothing follow
    const s = 1 - Math.pow(0.0001, dt); // frame-rate independent smoothing
    this.x = lerp(this.x, this.targetX, s);
    this.y = lerp(this.y, this.targetY, s * 0.9);
    this.vx = (this.x - this.px) / Math.max(dt, 1e-4);
    this.vy = (this.y - this.py) / Math.max(dt, 1e-4);
    this.squash = Math.max(0, this.squash - dt * 5);
    this.glow = Math.max(0, this.glow - dt * 2);
    this.trail.push({ x: this.x, y: this.y, w: this.w, life: 0.3 });
    if (this.trail.length > 14) this.trail.shift();
    for (const t of this.trail) t.life -= dt;
  },
  onHit() { this.squash = 1; this.glow = 1; }
};

const BALL_TYPES = {
  standard: { color: '#ffffff', glow: '#9adcff', r: 1.0, speedMul: 1 },
  fire:     { color: '#ff8b2e', glow: '#ff3d2e', r: 1.05, speedMul: 1.02 },
  volt:     { color: '#fff36b', glow: '#c8ff4d', r: 0.92, speedMul: 1.28 },
  ghost:    { color: '#c09aff', glow: '#7b4dff', r: 1.0, speedMul: 1.05 },
  heavy:    { color: '#ff6b6b', glow: '#ff2222', r: 1.45, speedMul: 0.92 }
};
let ballId = 1;
function makeBall(x, y, angle, type, speed) {
  const t = BALL_TYPES[type || 'standard'];
  const sp = (speed || BASE_BALL_SPEED) * t.speedMul;
  return {
    id: ballId++, x, y, type: type || 'standard',
    vx: Math.cos(angle) * sp, vy: Math.sin(angle) * sp,
    r: BALL_R * t.r, stuck: false, trail: [],
    streak: 0, // volt split counter
    spin: rand(0, TAU)
  };
}
function ballSpeed(b) { return Math.hypot(b.vx, b.vy); }
function setBallSpeed(b, sp) {
  sp = clamp(sp, 200, MAX_BALL_SPEED);
  const s = ballSpeed(b) || 1;
  b.vx = b.vx / s * sp; b.vy = b.vy / s * sp;
}
function convertBalls(type) {
  for (const b of Game.balls) {
    const sp = ballSpeed(b);
    b.type = type;
    const t = BALL_TYPES[type];
    b.r = BALL_R * t.r;
    setBallSpeed(b, sp * t.speedMul);
  }
}

/* ==================== 5. BRICKS ====================
   Types: normal, angle, speed, ghost(phase), slider, bomb. No plain multi-HP. */
const BRICK_COLORS = {
  normal: ['#3d6bff', '#38e1ff'],
  angle: ['#ff9f2e', '#ff5e3a'],
  speed: ['#ff3d6e', '#ff7b9c'],
  ghost: ['#9d6bff', '#5e8bff'],
  slider: ['#2ed9a3', '#b8ff5e'],
  bomb: ['#ff5252', '#ff9f2e']
};
function makeBrick(col, row, type, bw, bh, ox, oy) {
  return {
    col, row, type,
    x: ox + col * bw, y: oy + row * bh, w: bw - 5, h: bh - 5,
    alive: true, flash: 0, phase: rand(0, TAU),
    slideDir: Math.random() < 0.5 ? -1 : 1, slideSpeed: rand(50, 110),
    homeX: ox + col * bw, hitHintCd: 0
  };
}
function brickCenter(b) { return { x: b.x + b.w / 2, y: b.y + b.h / 2 }; }
function brickColor(b) {
  const c = BRICK_COLORS[b.type] || BRICK_COLORS.normal;
  const shade = 0.85 + 0.15 * Math.sin(b.phase + Game.time * 2);
  return c[Math.sin(b.phase) > 0 ? 0 : 1] || c[0];
}
/* Can this ball damage this brick at all? */
function brickDamageable(brick, ball) {
  if (brick.type === 'ghost') return ball.type === 'ghost' || ball.type === 'fire';
  return true;
}
/* Should this ball physically collide with this brick? (phase bricks are intangible to normal balls) */
function brickSolid(brick, ball) {
  if (brick.type === 'ghost' && !(ball.type === 'ghost' || ball.type === 'fire')) return false;
  return true;
}
function brickSpeedOK(brick, ball) {
  if (brick.type !== 'speed') return true;
  const th = ball.type === 'heavy' ? HEAVY_SPEED_THRESHOLD : SPEED_BRICK_THRESHOLD;
  return ballSpeed(ball) >= th;
}
function brickAngleOK(brick, ball, nx, ny) {
  if (brick.type !== 'angle') return true;
  // steepness of incoming velocity relative to surface normal: need strong normal component
  const sp = ballSpeed(ball) || 1;
  const vn = Math.abs((ball.vx * nx + ball.vy * ny)) / sp;
  void vn;
  // simpler & readable: require steep impact = mostly vertical motion
  return Math.abs(ball.vy) / sp >= STEEP_MIN;
}

/* ==================== 6. POWERUPS (falling capsules) ==================== */
const POWER_TYPES = {
  multi: { label: 'MULTI', color: '#38e1ff', dur: 0, desc: '+2 balls!' },
  fire:  { label: 'FIRE', color: '#ff7b2e', dur: 14, desc: 'Fire balls pierce bricks!' },
  ghost: { label: 'GHOST', color: '#c09aff', dur: 14, desc: 'Ghost balls phase!' },
  volt:  { label: 'VOLT', color: '#fff36b', dur: 14, desc: 'Volt balls: FAST & splitting!' },
  heavy: { label: 'HEAVY', color: '#ff5252', dur: 14, desc: 'Heavy smash balls!' },
  wide:  { label: 'WIDE', color: '#7dff5e', dur: 18, desc: 'Wide paddle!' },
  slow:  { label: 'SLOW', color: '#8ad8ff', dur: 10, desc: 'Slow-mo!' },
  life:  { label: '+1❤', color: '#ff5e7a', dur: 0, desc: 'Extra life!' }
};
function maybeDropPowerup(x, y) {
  const chance = Game.ott.active ? 0.14 : 0.20;
  if (Math.random() > chance) return;
  const pool = ['multi', 'fire', 'ghost', 'volt', 'heavy', 'wide', 'slow', 'multi', 'fire', 'wide', 'life'];
  const weights = { multi: 3, fire: 2, ghost: 2, volt: 2, heavy: 2, wide: 2, slow: 1.5, life: 0.7 };
  let total = 0; for (const k of pool) total += weights[k];
  let r = Math.random() * total, pick = 'multi';
  for (const k of pool) { r -= weights[k]; if (r <= 0) { pick = k; break; } }
  Game.powerups.push({ x, y, vy: 130, type: pick, phase: rand(0, TAU), w: 52, h: 20 });
}
function applyPowerup(type) {
  const P = POWER_TYPES[type];
  AudioSys.powerup();
  announce(P.label + ' — ' + P.desc, 1.3);
  switch (type) {
    case 'multi': {
      const src = Game.balls.slice(0, 3);
      if (Game.balls.length === 0) break;
      for (const b of src) {
        if (Game.balls.length >= 8) break;
        const a = Math.atan2(b.vy, b.vx) + rand(-0.6, 0.6);
        Game.balls.push(makeBall(b.x, b.y, a, b.type, ballSpeed(b)));
      }
      for (let i = 0; i < 16; i++) trailPuff(Paddle.x + rand(-40, 40), Paddle.y, '#38e1ff', 4);
      break;
    }
    case 'fire': convertBalls('fire'); Game.effects.fire = P.dur; break;
    case 'ghost': convertBalls('ghost'); Game.effects.ghost = P.dur; break;
    case 'volt':
      convertBalls('volt');
      for (const b of Game.balls) setBallSpeed(b, Math.max(ballSpeed(b), 560));
      Game.effects.volt = P.dur; break;
    case 'heavy': convertBalls('heavy'); Game.effects.heavy = P.dur; break;
    case 'wide': Paddle.w = Paddle.baseW * 1.55; Game.effects.wide = P.dur; break;
    case 'slow': Game.effects.slow = P.dur; slowMo(P.dur, 0.55); break;
    case 'life':
      Game.lives = Math.min(6, Game.lives + 1);
      addFloater(Paddle.x, Paddle.y - 30, '+1 LIFE!', '#ff5e7a', 20);
      break;
  }
}
/* ==================== 7. LEVELS (5 hand-designed) ====================
   Legend: . empty | N normal | A angle | S speed | G ghost/phase
           M slider/mover | B bomb
   Every level keeps a guaranteed vertical corridor to the top. */
const LEVELS = [
  {
    name: 'First Contact', sub: 'Learn the ropes. Punch a hole and ride it over the top!',
    map: [
      '........',
      '.NNNNNN.',
      '.NNNNNN.',
      '..NNNN..',
      '..NBBN..',
      '..NNNN..',
      '.NNNNNN.',
    ]
  },
  {
    name: 'Chevron Gate', sub: 'Orange ANGLE bricks ▲ only break on STEEP hits. Come down on them!',
    map: [
      '...GG...',
      '.NNNNNN.',
      '.AAAAAA.',
      '.NNNNNN.',
      '..NNNN..',
      '.NBNBBN.',
      '.NNNNNN.',
    ]
  },
  {
    name: 'Speed Trap', sub: 'Red SPEED bricks ≫ need a FAST ball. Flick UP on the paddle for boost!',
    map: [
      '...GG...',
      '.SSSSSS.',
      '.NNNNNN.',
      'MN.NN.MM',
      '.NNNNNN.',
      '.NBBBN..',
      '..NAAN..',
    ]
  },
  {
    name: 'Phantom Lane', sub: 'Purple PHASE bricks ◌ ignore normal balls. Grab GHOST or FIRE!',
    map: [
      '..G..G..',
      '.GGGGGG.',
      '.NANNAN.',
      'MNNNNNNM',
      '.SSSSSS.',
      '.NBNBN..',
      '.NNNNNN.',
    ]
  },
  {
    name: 'Overlord', sub: 'Everything at once. Break the core, live on top, bank it all. Good luck!',
    map: [
      '..GGG...',
      '.MAAAM..',
      '.SSSSS..',
      'MNGGGNMM',
      '.NBBBN..',
      '.SSSSS..',
      'NNNNNNNN',
    ]
  }
];
const BRICK_TOP = 108, BRICK_W = 53, BRICK_H = 26;
function buildBricks(levelIdx) {
  const map = LEVELS[levelIdx].map;
  const bricks = [];
  const cols = 8;
  const gridW = cols * BRICK_W;
  const ox = (W - gridW) / 2 + 2.5, oy = BRICK_TOP;
  const key = { N: 'normal', A: 'angle', S: 'speed', G: 'ghost', M: 'slider', B: 'bomb' };
  for (let r = 0; r < map.length; r++) {
    for (let c = 0; c < map[r].length; c++) {
      const ch = map[r][c];
      if (ch === '.' || ch === ' ') continue;
      bricks.push(makeBrick(c, r, key[ch] || 'normal', BRICK_W, BRICK_H, ox, oy));
    }
  }
  return bricks;
}

/* ==================== 8. GAME STATE ==================== */
const Game = {
  state: 'title', // title | intro | playing | paused | over | win
  running: false, paused: false,
  score: 0, hi: 0, lives: 3, level: 1,
  multiplier: 1, combo: 0, bestCombo: 0,
  balls: [], bricks: [], powerups: [],
  effects: { fire: 0, ghost: 0, volt: 0, heavy: 0, wide: 0, slow: 0 },
  ott: { active: false, time: 0, tick: 0, bonus: 0, bannerShown: false },
  time: 0, launchReady: true, shakeX: 0, shakeY: 0,
  bricksBroken: 0, totalBricks: 1,
  loadHi() {
    try { this.hi = parseInt(localStorage.getItem('ott_hiscore') || '0', 10) || 0; } catch (e) { this.hi = 0; }
  },
  saveHi() {
    if (this.score > this.hi) {
      this.hi = this.score;
      try { localStorage.setItem('ott_hiscore', String(this.hi)); } catch (e) {}
    }
  },
  /* ---- flow ---- */
  startFromTitle() {
    document.getElementById('overlay-title').classList.add('hidden');
    this.startRun(0);
  },
  startRun(levelIdx) {
    this.score = 0; this.lives = 3; this.multiplier = 1; this.combo = 0; this.bestCombo = 0;
    this.bricksBroken = 0;
    this.clearEffects();
    this.loadLevel(levelIdx, true);
  },
  loadLevel(idx, showIntro) {
    this.level = idx + 1;
    this.bricks = buildBricks(idx);
    this.totalBricks = this.bricks.length;
    this.balls = []; this.powerups = [];
    this.ott.active = false; this.ott.time = 0; this.ott.tick = 0; this.ott.bonus = 0;
    Paddle.reset(this.effects.wide > 0);
    this.clearEffects();
    this.stickBallToPaddle();
    this.running = true; this.paused = false;
    updateLevelButtons();
    if (showIntro) showIntroOverlay();
    else { this.state = 'playing'; syncOverlays(); }
    updateHUDStatic();
  },
  clearEffects() { this.effects = { fire: 0, ghost: 0, volt: 0, heavy: 0, wide: 0, slow: 0 }; },
  stickBallToPaddle() {
    this.balls = [makeBall(Paddle.x, Paddle.y - 20, -Math.PI / 2, 'standard', BASE_BALL_SPEED)];
    this.balls[0].stuck = true;
    this.launchReady = true;
  },
  tryLaunch() {
    AudioSys.init(); AudioSys.resume();
    if (this.state !== 'playing' || this.paused) return;
    const stuck = this.balls.find(b => b.stuck);
    if (!stuck) return;
    stuck.stuck = false;
    const up = clamp(Paddle.vy, -500, 0);
    const sp = BASE_BALL_SPEED + (-up) * 0.35;
    const a = -Math.PI / 2 + rand(-0.25, 0.25) + clamp(Paddle.vx / 2400, -0.3, 0.3);
    stuck.vx = Math.cos(a) * sp; stuck.vy = Math.sin(a) * sp;
    stuck.x = Paddle.x; stuck.y = Paddle.y - 18;
    this.launchReady = false;
    AudioSys.launch();
    shockwave(stuck.x, stuck.y, '#38e1ff', 30);
  },
  togglePause() {
    if (this.state !== 'playing') return;
    this.paused = !this.paused;
    syncOverlays();
  },
  loseLife() {
    this.lives--;
    this.combo = 0;
    this.multiplier = Math.max(1, Math.floor(this.multiplier / 2));
    this.ott.active = false; this.ott.bonus = 0; this.ott.time = 0;
    document.getElementById('ott-banner').classList.add('hidden');
    AudioSys.lifeLost();
    slowMo(1.2, 0.35);
    Juice.shake(0.7);
    updateHUDStatic();
    if (this.lives <= 0) { this.gameOver(); return; }
    this.clearEffects();
    Paddle.reset(false);
    this.stickBallToPaddle();
    announce('BALL LOST', 1.2);
  },
  gameOver() {
    this.state = 'over'; this.running = false;
    this.saveHi();
    AudioSys.gameOver();
    document.getElementById('over-title').textContent = '💀 GAME OVER';
    document.getElementById('over-stats').innerHTML =
      `Score <b>${this.score}</b> • Best <b>${this.hi}</b><br>Level <b>${this.level}</b> • Best combo <b>x${this.bestCombo}</b>`;
    syncOverlays();
  },
  levelClear() {
    const bonus = 500 * this.level * this.multiplier;
    this.score += bonus;
    addFloater(W / 2, H / 2, `LEVEL CLEAR +${bonus}`, '#7dff5e', 24);
    AudioSys.levelClear();
    Juice.shake(0.35);
    this.saveHi();
    // fanfare particles
    for (let i = 0; i < 60; i++) spawnParticle({ x: rand(40, W - 40), y: rand(150, 400), vx: rand(-200, 200), vy: rand(-260, 40), size: rand(2, 5), color: choice(['#ffd23d', '#7dff5e', '#38e1ff', '#ff3df0']), grav: 500, life: rand(0.6, 1.4), max: 1.4, shape: 'rect', glow: true });
    if (this.level >= LEVELS.length) {
      this.state = 'win'; this.running = false;
      document.getElementById('win-stats').innerHTML =
        `Final score <b>${this.score}</b> • Best <b>${this.hi}</b><br>Best combo <b>x${this.bestCombo}</b> • You are OVER THE TOP! ⚡`;
      setTimeout(syncOverlays, 1400);
    } else {
      this.state = 'intro';
      const next = this.level; // 1-based current; next index = level (0-based of next)
      setTimeout(() => this.loadLevel(next, true), 1500);
    }
    updateHUDStatic();
  },
  addScore(base, x, y) {
    const pts = base * this.multiplier;
    this.score += pts;
    if (x !== undefined) addFloater(x, y, '+' + pts, this.multiplier >= 8 ? '#ffd23d' : '#eaf2ff', this.multiplier >= 8 ? 18 : 13);
    if (this.score > this.hi) { this.hi = this.score; }
  },
  registerBreak() {
    this.combo++; this.bricksBroken++;
    this.bestCombo = Math.max(this.bestCombo, this.combo);
    if (this.combo > 0 && this.combo % COMBO_PER_MULT === 0 && !this.ott.active) {
      this.multiplier = Math.min(MULT_CAP_GROUND, this.multiplier + 1);
      announce('MULTIPLIER x' + this.multiplier, 1.1);
      AudioSys.powerup();
    }
    if (this.combo === 12) announce('COMBO x12 — COOKING!', 1.2);
    if (this.combo === 24) announce('COMBO x24 — RAMPAGE!', 1.3);
    if (this.combo === 40) announce('COMBO x40 — UNSTOPPABLE!', 1.4);
  }
};
/* ==================== 9. UPDATE & COLLISIONS ==================== */
function circleVsAABB(b, r) {
  // r: {x,y,w,h}. Returns {nx,ny,depth} or null. Proper side detection.
  const cx = clamp(b.x, r.x, r.x + r.w), cy = clamp(b.y, r.y, r.y + r.h);
  let dx = b.x - cx, dy = b.y - cy;
  const d2 = dx * dx + dy * dy;
  if (d2 > b.r * b.r) return null;
  if (d2 > 1e-9) {
    const d = Math.sqrt(d2);
    return { nx: dx / d, ny: dy / d, depth: b.r - d };
  }
  // center inside box: push out along min-penetration axis
  const left = b.x - r.x, right = r.x + r.w - b.x, top = b.y - r.y, bot = r.y + r.h - b.y;
  const m = Math.min(left, right, top, bot);
  if (m === left) return { nx: -1, ny: 0, depth: left + b.r };
  if (m === right) return { nx: 1, ny: 0, depth: right + b.r };
  if (m === top) return { nx: 0, ny: -1, depth: top + b.r };
  return { nx: 0, ny: 1, depth: bot + b.r };
}
function reflectVel(b, nx, ny) {
  const dot = b.vx * nx + b.vy * ny;
  if (dot < 0) { b.vx -= 2 * dot * nx; b.vy -= 2 * dot * ny; }
}
function destroyBrick(brick, ball, award) {
  if (!brick.alive) return;
  brick.alive = false;
  const c = brickCenter(brick);
  const col = brickColor(brick);
  const big = brick.type === 'bomb' || Game.multiplier >= 8;
  brickShatter(c.x, c.y, col, big);
  if (award !== false) {
    Game.registerBreak();
    const base = { normal: 50, angle: 80, speed: 80, ghost: 90, slider: 100, bomb: 60 }[brick.type] || 50;
    Game.addScore(base, c.x, c.y);
    Juice.shake(clamp(0.12 + Game.multiplier * 0.02, 0, 0.5));
    AudioSys.brickShatter(big);
    if (Game.multiplier >= 8) Juice.hitstop(0.03);
    maybeDropPowerup(c.x, c.y);
  }
  if (brick.type === 'bomb') explodeBomb(brick, ball);
  if (ball && ball.type === 'volt') {
    ball.streak++;
    if (ball.streak >= 4 && Game.balls.length < 8) {
      ball.streak = 0;
      const a = Math.atan2(ball.vy, ball.vx) + (Math.random() < 0.5 ? 0.5 : -0.5);
      Game.balls.push(makeBall(ball.x, ball.y, a, 'volt', ballSpeed(ball)));
      announce('VOLT SPLIT!', 0.9);
      AudioSys.powerup();
      shockwave(ball.x, ball.y, '#fff36b', 50);
    }
  }
}
function explodeBomb(brick, igniter) {
  const c = brickCenter(brick);
  AudioSys.explosion();
  Juice.shake(0.65); Juice.hitstop(0.06);
  shockwave(c.x, c.y, '#ff9f2e', 130);
  for (let i = 0; i < 30; i++) {
    const a = rand(0, TAU), sp = rand(80, 520);
    spawnParticle({ x: c.x, y: c.y, vx: Math.cos(a) * sp, vy: Math.sin(a) * sp, size: rand(2, 6), color: choice(['#ff5252', '#ff9f2e', '#ffd23d', '#ffffff']), grav: 600, drag: 1, life: rand(0.4, 1), max: 1, shape: 'circle', glow: true });
  }
  addFloater(c.x, c.y - 14, 'BOOM!', '#ff9f2e', 22);
  const R = 105;
  for (const b of Game.bricks) {
    if (!b.alive || b === brick) continue;
    const bc = brickCenter(b);
    if (dist2(c.x, c.y, bc.x, bc.y) < R * R) destroyBrick(b, igniter, true);
  }
}
/* Handle one ball vs one brick. Returns true if ball velocity was changed (bounced/pierced). */
function collideBallBrick(ball, brick) {
  if (!brick.alive) return false;
  if (!brickSolid(brick, ball)) return false; // phase bricks intangible to normal balls
  const hit = circleVsAABB(ball, brick);
  if (!hit) return false;
  const steepOK = brickAngleOK(brick, ball, hit.nx, hit.ny);
  const fastOK = brickSpeedOK(brick, ball);
  const canDamage = brickDamageable(brick, ball);
  // FIRE pierces normal-ish bricks: destroy & continue without bounce
  if (ball.type === 'fire' && brick.type !== 'bomb' && canDamage && steepOK && fastOK) {
    destroyBrick(brick, ball, true);
    for (let i = 0; i < 4; i++) burnEmber(ball.x, ball.y);
    return false;
  }
  // GHOST ball vs ghost brick: damage + pass a touch (slight deflect)
  if (ball.type === 'ghost' && brick.type === 'ghost') {
    destroyBrick(brick, ball, true);
    for (let i = 0; i < 4; i++) ghostWisp(ball.x, ball.y);
    return false;
  }
  if (!canDamage) return false;
  if (!steepOK) {
    // shallow hit on angle brick: bounce, no damage, hint
    reflectVel(ball, hit.nx, hit.ny);
    ball.x += hit.nx * (hit.depth + 0.5); ball.y += hit.ny * (hit.depth + 0.5);
    brick.flash = 1;
    if (brick.hitHintCd <= 0) { hint('▲ TOO SHALLOW — hit it STEEP!'); brick.hitHintCd = 1.2; }
    AudioSys.brickResist();
    for (let i = 0; i < 3; i++) trailPuff(ball.x, ball.y, '#ff9f2e', 3);
    return true;
  }
  if (!fastOK) {
    reflectVel(ball, hit.nx, hit.ny);
    ball.x += hit.nx * (hit.depth + 0.5); ball.y += hit.ny * (hit.depth + 0.5);
    brick.flash = 1;
    if (brick.hitHintCd <= 0) { hint('≫ TOO SLOW — flick UP on the paddle for speed!'); brick.hitHintCd = 1.2; }
    AudioSys.brickResist();
    return true;
  }
  // normal destructive bounce
  reflectVel(ball, hit.nx, hit.ny);
  ball.x += hit.nx * (hit.depth + 0.5); ball.y += hit.ny * (hit.depth + 0.5);
  // heavy balls plow: keep most of velocity, tiny deflect
  if (ball.type === 'heavy') { Juice.shake(0.3); }
  destroyBrick(brick, ball, true);
  return true;
}

function paddleBounce(ball) {
  const p = Paddle;
  // generous top catch: expand the box a few px upward so fast balls don't slip through
  const box = { x: p.x - p.w / 2, y: p.y - p.h / 2 - 7, w: p.w, h: p.h + 7 };
  const hit = circleVsAABB(ball, box);
  if (!hit) return false;
  if (ball.vy < 0 && ball.y < p.y - p.h) return false; // moving away above
  const offset = clamp((ball.x - p.x) / (p.w / 2), -1, 1);
  // energy from paddle Y motion: moving UP (vy<0) adds speed
  const upBoost = clamp(-p.vy, 0, 700);
  let sp = ballSpeed(ball) + 14 + upBoost * 0.28;
  if (ball.type === 'volt') sp = Math.max(sp, 560);
  sp = clamp(sp, 260, MAX_BALL_SPEED);
  const maxAngle = 1.05; // ~60deg
  const ang = -Math.PI / 2 + offset * maxAngle + clamp(p.vx / 2600, -0.25, 0.25);
  ball.vx = Math.cos(ang) * sp; ball.vy = Math.sin(ang) * sp;
  // force upward
  if (ball.vy > -120) ball.vy = -120;
  ball.y = p.y - p.h / 2 - ball.r - 0.5;
  p.onHit();
  Game.combo = 0; // paddle touch resets ground combo chain? No—keep mult, reset combo counter
  AudioSys.paddleBlip(offset, p.vy);
  shockwave(ball.x, ball.y, '#38e1ff', upBoost > 250 ? 55 : 26);
  if (upBoost > 250) {
    addFloater(ball.x, ball.y - 18, 'POWER HIT!', '#ffd23d', 16);
    Juice.shake(0.22);
    for (let i = 0; i < 8; i++) trailPuff(ball.x, ball.y, '#ffd23d', 3);
  } else {
    for (let i = 0; i < 4; i++) trailPuff(ball.x, ball.y, '#38e1ff', 3);
  }
  return true;
}

function updateBalls(dt) {
  const slowScale = Game.effects.slow > 0 ? 0.62 : 1;
  for (let i = Game.balls.length - 1; i >= 0; i--) {
    const b = Game.balls[i];
    if (b.stuck) { b.x = Paddle.x; b.y = Paddle.y - 20; continue; }
    // sub-stepping to prevent tunneling: max move = r*0.7 per step
    const sp = ballSpeed(b) * slowScale;
    const steps = clamp(Math.ceil(sp * dt / (b.r * 0.7)), 1, 10);
    const sdt = dt * slowScale / steps;
    for (let s = 0; s < steps; s++) {
      b.x += b.vx * sdt * (Game.effects.slow > 0 ? 1 : 1);
      b.y += b.vy * sdt;
      // walls
      if (b.x - b.r < WALL) { b.x = WALL + b.r; b.vx = Math.abs(b.vx); AudioSys.wallBlip(); }
      if (b.x + b.r > W - WALL) { b.x = W - WALL - b.r; b.vx = -Math.abs(b.vx); AudioSys.wallBlip(); }
      if (b.y - b.r < TOP_Y) { b.y = TOP_Y + b.r; b.vy = Math.abs(b.vy); AudioSys.wallBlip(); }
      // paddle
      paddleBounce(b);
      // bricks
      for (const br of Game.bricks) {
        if (!br.alive) continue;
        collideBallBrick(b, br);
      }
    }
    // trails
    b.trail.push({ x: b.x, y: b.y, life: 0.35 });
    if (b.trail.length > 22) b.trail.shift();
    for (const t of b.trail) t.life -= dt;
    if (b.type === 'fire' && Math.random() < 0.8) burnEmber(b.x, b.y);
    if (b.type === 'volt') { voltSpark(b.x, b.y); if (Math.random() < 0.5) voltSpark(b.x + rand(-6, 6), b.y + rand(-6, 6)); }
    if (b.type === 'ghost' && Math.random() < 0.5) ghostWisp(b.x, b.y);
    if (b.type === 'heavy' && Math.random() < 0.4) trailPuff(b.x, b.y, '#ff5252', 4);
    // ball loss
    if (b.y > DEATH_Y) {
      Game.balls.splice(i, 1);
      shockwave(clamp(b.x, 20, W - 20), H - 20, '#ff5252', 50);
    }
  }
  if (Game.balls.length === 0 && Game.state === 'playing') Game.loseLife();
}

function updateBricks(dt) {
  for (const b of Game.bricks) {
    if (!b.alive) continue;
    b.flash = Math.max(0, b.flash - dt * 3);
    b.hitHintCd = Math.max(0, (b.hitHintCd || 0) - dt);
    if (b.type === 'slider') {
      b.x += b.slideDir * b.slideSpeed * dt;
      const minX = 12, maxX = W - 12 - b.w;
      if (b.x < minX) { b.x = minX; b.slideDir = 1; }
      if (b.x > maxX) { b.x = maxX; b.slideDir = -1; }
    }
  }
}
function updatePowerups(dt) {
  for (let i = Game.powerups.length - 1; i >= 0; i--) {
    const p = Game.powerups[i];
    p.y += p.vy * dt; p.phase += dt * 5;
    if (p.y > H + 20) { Game.powerups.splice(i, 1); continue; }
    // catch with paddle (generous box)
    if (p.y + p.h / 2 > Paddle.y - Paddle.h / 2 - 6 && p.y - p.h / 2 < Paddle.y + Paddle.h / 2 + 10 &&
        Math.abs(p.x - Paddle.x) < Paddle.w / 2 + p.w / 2) {
      Game.powerups.splice(i, 1);
      applyPowerup(p.type);
      Juice.shake(0.15);
      updateHUDStatic();
    }
  }
}
function updateEffects(dt) {
  const E = Game.effects;
  if (E.fire > 0) { E.fire -= dt; if (E.fire <= 0 && ballTypeActive('fire')) convertBalls('standard'); }
  if (E.ghost > 0) { E.ghost -= dt; if (E.ghost <= 0 && ballTypeActive('ghost')) convertBalls('standard'); }
  if (E.volt > 0) { E.volt -= dt; if (E.volt <= 0 && ballTypeActive('volt')) convertBalls('standard'); }
  if (E.heavy > 0) { E.heavy -= dt; if (E.heavy <= 0 && ballTypeActive('heavy')) convertBalls('standard'); }
  if (E.wide > 0) { E.wide -= dt; if (E.wide <= 0) Paddle.w = Paddle.baseW; }
  if (E.slow > 0) { E.slow -= dt; }
  if (E.fire <= 0 && E.ghost <= 0 && E.volt <= 0 && E.heavy <= 0) {
    // ensure no stranded special balls after stacking converts
  }
}
function ballTypeActive(t) { return Game.balls.some(b => b.type === t); }

/* ==================== 10. OVER-THE-TOP MODE ==================== */
function brickFieldTop() {
  let top = Infinity;
  for (const b of Game.bricks) if (b.alive) top = Math.min(top, b.y);
  return top === Infinity ? TOP_Y : top;
}
function updateOTT(dt) {
  const alive = Game.bricks.some(b => b.alive);
  const top = brickFieldTop();
  const anyAbove = alive && Game.balls.some(b => !b.stuck && b.y < top - 4);
  if (anyAbove && !Game.ott.active) {
    // ENTER
    Game.ott.active = true; Game.ott.time = 0; Game.ott.tick = 0; Game.ott.bonus = 0;
    document.getElementById('ott-banner').classList.remove('hidden');
    AudioSys.riser();
    announce('⚡ OVER THE TOP! ⚡', 1.5);
    Juice.shake(0.4);
    shockwave(W / 2, top, '#ffd23d', 160);
  } else if (!anyAbove && Game.ott.active) {
    // EXIT: bank bonus with fanfare
    const bonus = Game.ott.bonus;
    if (bonus > 0) {
      Game.score += bonus;
      addFloater(W / 2, H * 0.4, `BANKED +${bonus}!`, '#ffd23d', 26);
      AudioSys.bankFanfare();
      Juice.shake(0.3);
      for (let i = 0; i < 30; i++) ottRain();
    } else announce('BACK DOWN', 0.8);
    Game.ott.active = false; Game.ott.time = 0;
    document.getElementById('ott-banner').classList.add('hidden');
    updateHUDStatic();
  }
  if (Game.ott.active) {
    Game.ott.time += dt;
    Game.ott.tick += dt;
    // multiplier ticks up exponentially-ish: double every ~2.2s up to cap
    if (Game.ott.time >= 2.2) {
      Game.ott.time = 0;
      if (Game.multiplier < MULT_CAP_OTT) {
        Game.multiplier = Math.min(MULT_CAP_OTT, Game.multiplier * 2);
        announce('OVER THE TOP x' + Game.multiplier, 1.0);
      }
      AudioSys.ottTick(Game.multiplier);
    }
    // bonus accrues: 25 * mult per second, ticked in chunks
    if (Game.ott.tick >= 0.25) {
      Game.ott.tick = 0;
      const gain = Math.round(25 * Game.multiplier * 0.25);
      Game.ott.bonus += gain;
      Game.score += gain;
      if (ScorePop._a++ % 4 === 0) addFloater(rand(W * 0.3, W * 0.7), rand(120, 260), '+' + gain, '#ffd23d', 13);
      document.getElementById('ott-mult').textContent = 'x' + Game.multiplier + '  +(' + Game.ott.bonus + ')';
    }
    // rain particles + riser shimmer
    for (let i = 0; i < 3; i++) ottRain();
    AudioSys.intensity = 1;
  } else {
    AudioSys.intensity = clamp((Game.multiplier - 1) / 15, 0, 1);
  }
}
const ScorePop = { _a: 0 };
/* ==================== 11. RENDER ==================== */
const canvas = document.getElementById('game');
const ctx = canvas.getContext('2d');
let DPR = 1, viewScale = 1;
function resize() {
  const stage = document.getElementById('stage');
  const r = stage.getBoundingClientRect();
  DPR = Math.min(2.5, window.devicePixelRatio || 1);
  canvas.width = Math.max(1, Math.round(r.width * DPR));
  canvas.height = Math.max(1, Math.round(r.height * DPR));
  viewScale = canvas.width / W; // we fit width; height letterboxed via cover mapping
  // Use full-stage mapping: scale so W->width and H->height independently (stage aspect ~ W:H)
  ctx.setTransform(canvas.width / W, 0, 0, canvas.height / H, 0, 0);
}
window.addEventListener('resize', resize);
window.addEventListener('orientationchange', () => setTimeout(resize, 200));

function heatLevel() { return clamp((Game.multiplier - 1) / 20, 0, 1); }

function drawBackground(dt) {
  const heat = heatLevel(), ott = Game.ott.active;
  // gradient shifts with heat + OTT
  const g = ctx.createLinearGradient(0, 0, 0, H);
  if (ott) {
    g.addColorStop(0, '#2b0f3f'); g.addColorStop(0.5, '#141b45'); g.addColorStop(1, '#3f0f2b');
  } else {
    const r = Math.round(lerp(7, 40, heat)), b = Math.round(lerp(29, 60, heat));
    g.addColorStop(0, `rgb(${r},${11 + heat * 10},${b + 20})`);
    g.addColorStop(0.6, '#0b1026');
    g.addColorStop(1, `rgb(${10 + heat * 40},${8},${26 + heat * 20})`);
  }
  ctx.fillStyle = g; ctx.fillRect(0, 0, W, H);
  // stars
  for (const s of Stars) {
    const a = 0.3 + 0.7 * Math.abs(Math.sin(s.tw)) + heat * 0.3;
    ctx.globalAlpha = clamp(a, 0, 1);
    ctx.fillStyle = ott && Math.random() < 0.02 ? '#ffd23d' : '#cfe8ff';
    const sz = s.z * (ott ? 3 : 2);
    ctx.fillRect(s.x, s.y, sz, sz * (ott ? 3 : 1.5));
  }
  ctx.globalAlpha = 1;
  // nebula blobs
  ctx.save(); ctx.globalAlpha = 0.10 + heat * 0.12;
  const t = Game.time;
  blob(W * 0.2 + Math.sin(t * 0.4) * 30, H * 0.25, 130, ott ? '#ff3df0' : '#2233aa');
  blob(W * 0.85 + Math.cos(t * 0.3) * 30, H * 0.55, 150, ott ? '#ffd23d' : '#0e4a5a');
  blob(W * 0.5, H * 0.9, 160, '#3a1a66');
  ctx.restore();
  // OTT sky glow line at brick top
  if (ott) {
    const top = brickFieldTop();
    ctx.save();
    ctx.strokeStyle = '#ffd23d'; ctx.lineWidth = 2; ctx.globalAlpha = 0.7 + 0.3 * Math.sin(t * 10);
    ctx.shadowColor = '#ffd23d'; ctx.shadowBlur = 18;
    ctx.setLineDash([12, 8]);
    ctx.beginPath(); ctx.moveTo(WALL, top - 6); ctx.lineTo(W - WALL, top - 6); ctx.stroke();
    ctx.restore();
  }
  // walls
  ctx.fillStyle = 'rgba(56,225,255,.25)';
  ctx.fillRect(0, TOP_Y - 8, WALL, H - TOP_Y);
  ctx.fillRect(W - WALL, TOP_Y - 8, WALL, H - TOP_Y);
  ctx.fillStyle = 'rgba(56,225,255,.5)';
  ctx.fillRect(WALL - 2, TOP_Y - 8, 2, H - TOP_Y);
  ctx.fillRect(W - WALL, TOP_Y - 8, 2, H - TOP_Y);
  // ceiling
  ctx.fillStyle = 'rgba(56,225,255,.4)'; ctx.fillRect(0, TOP_Y - 8, W, 4);
}
function blob(x, y, r, color) {
  const g = ctx.createRadialGradient(x, y, 0, x, y, r);
  g.addColorStop(0, color); g.addColorStop(1, 'transparent');
  ctx.fillStyle = g; ctx.beginPath(); ctx.arc(x, y, r, 0, TAU); ctx.fill();
}

function drawPaddle() {
  const p = Paddle, heat = heatLevel();
  // motion trail
  for (const t of p.trail) {
    if (t.life <= 0) continue;
    ctx.globalAlpha = t.life * 0.8;
    ctx.fillStyle = '#38e1ff';
    roundRect(p.x - t.w / 2, t.y - p.h / 2, t.w, p.h, 7); ctx.fill();
  }
  ctx.globalAlpha = 1;
  // squash & stretch
  const sq = p.squash;
  const ww = p.w * (1 + sq * 0.18), hh = p.h * (1 - sq * 0.35);
  const speedGlow = clamp(Math.abs(p.vy) / 700, 0, 1);
  ctx.save();
  ctx.shadowColor = Game.ott.active ? '#ffd23d' : '#38e1ff';
  ctx.shadowBlur = 14 + heat * 22 + speedGlow * 16 + p.glow * 12;
  const g = ctx.createLinearGradient(0, p.y - hh / 2, 0, p.y + hh / 2);
  if (Game.effects.wide > 0) { g.addColorStop(0, '#d6ffd0'); g.addColorStop(0.5, '#7dff5e'); g.addColorStop(1, '#2e9e5e'); }
  else if (Game.ott.active) { g.addColorStop(0, '#fff6c8'); g.addColorStop(0.5, '#ffd23d'); g.addColorStop(1, '#ff9f2e'); }
  else { g.addColorStop(0, '#d8f6ff'); g.addColorStop(0.45, '#38e1ff'); g.addColorStop(1, '#0e6f9e'); }
  ctx.fillStyle = g;
  roundRect(p.x - ww / 2, p.y - hh / 2, ww, hh, 7); ctx.fill();
  // core stripe
  ctx.shadowBlur = 0;
  ctx.fillStyle = 'rgba(255,255,255,.75)';
  roundRect(p.x - ww / 2 + 5, p.y - 2, ww - 10, 4, 2); ctx.fill();
  // up-thrust flames when moving up fast
  if (p.vy < -180) {
    ctx.fillStyle = 'rgba(255,210,61,.8)';
    for (let i = 0; i < 5; i++) {
      const fx = p.x + rand(-ww / 2, ww / 2), fy = p.y + hh / 2;
      ctx.beginPath(); ctx.moveTo(fx - 3, fy); ctx.lineTo(fx + 3, fy); ctx.lineTo(fx, fy + rand(6, 18)); ctx.fill();
    }
  }
  // paddle zone hint
  ctx.restore();
}
function roundRect(x, y, w, h, r) {
  r = Math.min(r, w / 2, h / 2);
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + w, y, x + w, y + h, r); ctx.arcTo(x + w, y + h, x, y + h, r);
  ctx.arcTo(x, y + h, x, y, r); ctx.arcTo(x, y, x + w, y, r);
  ctx.closePath();
}

function drawBall(b) {
  const t = BALL_TYPES[b.type], heat = heatLevel();
  // trail
  for (let i = 0; i < b.trail.length; i++) {
    const tr = b.trail[i];
    if (tr.life <= 0) continue;
    ctx.globalAlpha = (tr.life / 0.35) * 0.5;
    ctx.fillStyle = t.glow;
    ctx.beginPath(); ctx.arc(tr.x, tr.y, b.r * (0.4 + 0.6 * i / b.trail.length), 0, TAU); ctx.fill();
  }
  ctx.globalAlpha = 1;
  if (b.type === 'volt' && b.trail.length > 1) {
    // lightning polyline through recent trail
    ctx.save();
    ctx.strokeStyle = '#fff36b'; ctx.lineWidth = 2; ctx.shadowColor = '#c8ff4d'; ctx.shadowBlur = 12;
    ctx.beginPath();
    const pts = b.trail.slice(-7);
    pts.forEach((p, i) => {
      const jx = (i % 2 ? 3 : -3);
      if (i === 0) ctx.moveTo(p.x, p.y); else ctx.lineTo(p.x + jx, p.y);
    });
    ctx.lineTo(b.x, b.y); ctx.stroke();
    ctx.restore();
  }
  ctx.save();
  if (b.type === 'ghost') ctx.globalAlpha = 0.65;
  ctx.shadowColor = t.glow; ctx.shadowBlur = 12 + heat * 20;
  const g = ctx.createRadialGradient(b.x - b.r * 0.3, b.y - b.r * 0.3, 1, b.x, b.y, b.r * 1.4);
  g.addColorStop(0, '#ffffff'); g.addColorStop(0.4, t.color); g.addColorStop(1, t.glow);
  ctx.fillStyle = g;
  ctx.beginPath(); ctx.arc(b.x, b.y, b.r, 0, TAU); ctx.fill();
  ctx.shadowBlur = 0;
  if (b.type === 'fire') {
    ctx.fillStyle = 'rgba(255,60,30,.9)';
    ctx.beginPath(); ctx.arc(b.x, b.y, b.r * 0.45, 0, TAU); ctx.fill();
  }
  if (b.type === 'heavy') {
    ctx.strokeStyle = 'rgba(255,255,255,.85)'; ctx.lineWidth = 1.5;
    ctx.beginPath(); ctx.arc(b.x, b.y, b.r * 0.62, 0, TAU); ctx.stroke();
  }
  ctx.restore();
  if (b.stuck) {
    ctx.save();
    ctx.globalAlpha = 0.6 + 0.4 * Math.sin(Game.time * 6);
    ctx.fillStyle = '#fff'; ctx.font = '700 13px system-ui'; ctx.textAlign = 'center';
    ctx.fillText('SPACE / TAP TO LAUNCH', b.x, b.y - 22);
    ctx.restore();
  }
}

function drawBrick(b) {
  const c = brickCenter(b);
  const pulse = 0.5 + 0.5 * Math.sin(Game.time * 3 + b.phase);
  ctx.save();
  if (b.flash > 0) {
    ctx.shadowColor = '#ffffff'; ctx.shadowBlur = 24;
    ctx.fillStyle = '#ffffff';
    roundRect(b.x - 1, b.y - 1, b.w + 2, b.h + 2, 6); ctx.fill();
    ctx.restore();
    return;
  }
  const cols = BRICK_COLORS[b.type];
  ctx.shadowColor = cols[0]; ctx.shadowBlur = 6 + heatLevel() * 10 + (b.type === 'bomb' ? 8 + pulse * 8 : 0);
  const g = ctx.createLinearGradient(0, b.y, 0, b.y + b.h);
  g.addColorStop(0, cols[1]); g.addColorStop(0.5, cols[0]); g.addColorStop(1, cols[0]);
  ctx.fillStyle = g;
  if (b.type === 'ghost') ctx.globalAlpha = 0.55 + pulse * 0.2;
  roundRect(b.x, b.y, b.w, b.h, 6); ctx.fill();
  ctx.shadowBlur = 0;
  ctx.globalAlpha = 1;
  // top highlight
  ctx.fillStyle = 'rgba(255,255,255,.35)';
  roundRect(b.x + 3, b.y + 2, b.w - 6, 4, 2); ctx.fill();
  // icons
  ctx.fillStyle = 'rgba(255,255,255,.95)'; ctx.strokeStyle = 'rgba(255,255,255,.9)';
  ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
  const cx = c.x, cy = c.y + 1;
  if (b.type === 'angle') {
    // chevron pointing up = hit from steep angle
    ctx.font = '900 13px system-ui';
    ctx.fillText(pulse > 0.5 ? '▲▲' : '▲', cx, cy);
  } else if (b.type === 'speed') {
    ctx.font = '900 12px system-ui';
    ctx.fillText('≫', cx, cy);
    // speed lines
    ctx.lineWidth = 1.5; ctx.globalAlpha = 0.5 + pulse * 0.5;
    ctx.beginPath();
    ctx.moveTo(b.x - 6, b.y + 4); ctx.lineTo(b.x - 1, b.y + 4);
    ctx.moveTo(b.x - 6, b.y + b.h / 2); ctx.lineTo(b.x - 1, b.y + b.h / 2);
    ctx.moveTo(b.x - 6, b.y + b.h - 4); ctx.lineTo(b.x - 1, b.y + b.h - 4);
    ctx.moveTo(b.x + b.w + 1, b.y + 4); ctx.lineTo(b.x + b.w + 6, b.y + 4);
    ctx.moveTo(b.x + b.w + 1, b.y + b.h / 2); ctx.lineTo(b.x + b.w + 6, b.y + b.h / 2);
    ctx.moveTo(b.x + b.w + 1, b.y + b.h - 4); ctx.lineTo(b.x + b.w + 6, b.y + b.h - 4);
    ctx.stroke(); ctx.globalAlpha = 1;
  } else if (b.type === 'ghost') {
    ctx.font = '900 13px system-ui';
    ctx.globalAlpha = 0.9;
    ctx.fillText('◌', cx, cy);
    ctx.globalAlpha = 1;
    ctx.setLineDash([4, 3]);
    ctx.lineWidth = 1.5;
    roundRect(b.x + 1, b.y + 1, b.w - 2, b.h - 2, 5); ctx.stroke();
    ctx.setLineDash([]);
  } else if (b.type === 'slider') {
    ctx.font = '900 13px system-ui';
    ctx.fillText('⇔', cx, cy);
  } else if (b.type === 'bomb') {
    ctx.font = '900 14px system-ui';
    ctx.fillText(pulse > 0.3 ? '✸' : '●', cx, cy);
  }
  ctx.restore();
}

function drawPowerups(dt) {
  for (const p of Game.powerups) {
    const P = POWER_TYPES[p.type];
    const bob = Math.sin(p.phase) * 2;
    ctx.save();
    ctx.translate(p.x, p.y + bob);
    ctx.shadowColor = P.color; ctx.shadowBlur = 12;
    ctx.fillStyle = 'rgba(5,10,28,.9)';
    roundRect(-p.w / 2, -p.h / 2, p.w, p.h, 10); ctx.fill();
    ctx.strokeStyle = P.color; ctx.lineWidth = 2;
    roundRect(-p.w / 2, -p.h / 2, p.w, p.h, 10); ctx.stroke();
    ctx.shadowBlur = 0;
    ctx.fillStyle = P.color; ctx.font = '900 11px system-ui'; ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    ctx.fillText(P.label, 0, 1);
    ctx.restore();
  }
}
function drawParticles() {
  for (const p of Particles) {
    const a = clamp(p.life / p.max, 0, 1);
    ctx.save();
    ctx.globalAlpha = a;
    if (p.glow) { ctx.shadowColor = p.color; ctx.shadowBlur = 10; }
    ctx.fillStyle = p.color; ctx.strokeStyle = p.color;
    if (p.shape === 'rect') {
      ctx.translate(p.x, p.y); ctx.rotate(p.rot || 0);
      ctx.fillRect(-p.size / 2, -p.size / 2, p.size, p.size * 0.7);
    } else if (p.shape === 'circle') {
      ctx.beginPath(); ctx.arc(p.x, p.y, p.size * a + 0.5, 0, TAU); ctx.fill();
    } else if (p.shape === 'spark') {
      ctx.lineWidth = 1.6;
      ctx.beginPath(); ctx.moveTo(p.x, p.y); ctx.lineTo(p.x - p.vx * 0.03, p.y - p.vy * 0.03); ctx.stroke();
    } else if (p.shape === 'ring') {
      ctx.globalAlpha = a * 0.9; ctx.lineWidth = 3 * a + 1;
      ctx.beginPath(); ctx.arc(p.x, p.y, p.size, 0, TAU); ctx.stroke();
    } else if (p.shape === 'note') {
      ctx.font = `${Math.round(p.size * 4)}px system-ui`; ctx.textAlign = 'center';
      ctx.fillText('♪', p.x, p.y);
    }
    ctx.restore();
  }
  ctx.globalAlpha = 1;
}
function drawFloaters() {
  ctx.save(); ctx.textAlign = 'center';
  for (const f of Floaters) {
    const a = clamp(f.life / f.max, 0, 1);
    ctx.globalAlpha = a;
    ctx.font = `900 ${f.size}px system-ui`;
    ctx.shadowColor = f.color; ctx.shadowBlur = 10;
    ctx.fillStyle = '#fff';
    ctx.fillText(f.text, f.x, f.y);
  }
  ctx.restore(); ctx.globalAlpha = 1;
}
function drawPaddleZone() {
  ctx.save();
  ctx.strokeStyle = 'rgba(56,225,255,.14)'; ctx.lineWidth = 1.5;
  ctx.setLineDash([6, 6]);
  ctx.strokeRect(WALL + 4, PADDLE_ZONE_TOP - 12, W - 2 * (WALL + 4), (PADDLE_ZONE_BOT - PADDLE_ZONE_TOP) + 24);
  ctx.setLineDash([]);
  ctx.restore();
}

function render(dt) {
  ctx.save();
  // screen shake
  const sh = Juice.getShake();
  const heat = heatLevel();
  const mag = sh * (6 + heat * 14 + (Game.ott.active ? 6 : 0));
  Game.shakeX = rand(-mag, mag); Game.shakeY = rand(-mag, mag);
  ctx.translate(Game.shakeX, Game.shakeY);
  drawBackground(dt);
  drawPaddleZone();
  for (const b of Game.bricks) if (b.alive) drawBrick(b);
  drawPowerups(dt);
  drawPaddle();
  for (const b of Game.balls) drawBall(b);
  drawParticles();
  drawFloaters();
  ctx.restore();
}
/* ==================== 12. HUD / OVERLAYS / LOOP / BOOT ==================== */
function updateHUDStatic() {
  document.getElementById('hud-score').textContent = Game.score;
  document.getElementById('hud-hi').textContent = 'BEST ' + Game.hi;
  document.getElementById('hud-mult').textContent = 'x' + Game.multiplier;
  document.getElementById('hud-level').textContent = 'LEVEL ' + Game.level + ' • ' + LEVELS[Game.level - 1].name.toUpperCase();
  document.getElementById('hud-lives').textContent = '❤'.repeat(Math.max(0, Game.lives)) || '—';
  const done = Game.totalBricks ? (Game.bricksBroken / Game.totalBricks) : 0;
  document.getElementById('hud-progress-fill').style.width = (done * 100).toFixed(1) + '%';
  const heat = clamp((Game.multiplier - 1) / 20, 0, 1);
  document.getElementById('heatfill').style.width = (heat * 100).toFixed(1) + '%';
  renderFxChips();
}
function renderFxChips() {
  const E = Game.effects, el = document.getElementById('hud-effects');
  const chips = [];
  if (E.fire > 0) chips.push(['🔥 FIRE', E.fire, '#ff7b2e']);
  if (E.ghost > 0) chips.push(['👻 GHOST', E.ghost, '#c09aff']);
  if (E.volt > 0) chips.push(['⚡ VOLT', E.volt, '#fff36b']);
  if (E.heavy > 0) chips.push(['💥 HEAVY', E.heavy, '#ff5252']);
  if (E.wide > 0) chips.push(['↔ WIDE', E.wide, '#7dff5e']);
  if (E.slow > 0) chips.push(['🐌 SLOW', E.slow, '#8ad8ff']);
  el.innerHTML = chips.map(c => `<span class="fx-chip" style="color:${c[2]}">${c[0]} ${Math.ceil(c[1])}s</span>`).join('');
}
let hudAcc = 0;
function updateHUDLive(dt) {
  hudAcc += dt;
  if (hudAcc < 0.12) return;
  hudAcc = 0;
  document.getElementById('hud-score').textContent = Game.score;
  document.getElementById('hud-mult').textContent = 'x' + Game.multiplier;
  const heat = clamp((Game.multiplier - 1) / 20, 0, 1);
  document.getElementById('heatfill').style.width = (heat * 100).toFixed(1) + '%';
  const done = Game.totalBricks ? (Game.bricksBroken / Game.totalBricks) : 0;
  document.getElementById('hud-progress-fill').style.width = (done * 100).toFixed(1) + '%';
  renderFxChips();
}

function syncOverlays() {
  const S = Game.state;
  if (S === 'title') {
    const tb = document.getElementById('title-best');
    if (tb) tb.textContent = '★ BEST ' + Game.hi + ' ★';
  }
  document.getElementById('overlay-title').classList.toggle('hidden', S !== 'title');
  document.getElementById('overlay-intro').classList.toggle('hidden', S !== 'intro');
  document.getElementById('overlay-pause').classList.toggle('hidden', !(S === 'playing' && Game.paused));
  document.getElementById('overlay-over').classList.toggle('hidden', S !== 'over');
  document.getElementById('overlay-win').classList.toggle('hidden', S !== 'win');
  document.getElementById('hud').classList.toggle('hidden', S === 'title');
}
function showIntroOverlay() {
  Game.state = 'intro';
  const L = LEVELS[Game.level - 1];
  document.getElementById('intro-kicker').textContent = `LEVEL ${Game.level} OF ${LEVELS.length}`;
  document.getElementById('intro-name').textContent = L.name;
  document.getElementById('intro-desc').textContent = L.sub;
  syncOverlays();
}
function updateLevelButtons() {
  const grid = document.getElementById('level-grid');
  grid.innerHTML = '';
  LEVELS.forEach((L, i) => {
    const b = document.createElement('button');
    b.className = 'lvl-btn'; b.textContent = (i + 1) + '';
    b.title = L.name;
    b.onclick = () => { AudioSys.init(); AudioSys.resume(); hideTitlePlay(i); };
    grid.appendChild(b);
  });
}
function hideTitlePlay(idx) {
  document.getElementById('overlay-title').classList.add('hidden');
  Game.startRun(idx || 0);
}

/* ---- main loop: fixed-dt-independent, clamped, with hit-stop & slow-mo ---- */
let lastT = 0, fpsAcc = 0;
function frame(t) {
  requestAnimationFrame(frame);
  if (!lastT) lastT = t;
  let dt = (t - lastT) / 1000;
  lastT = t;
  if (dt > 0.05) dt = 0.05; // clamp huge gaps
  // hit-stop freeze
  if (Juice.freeze > 0) { Juice.freeze -= dt; render(dt); return; }
  // slow-mo scaling (ball loss / slow powerup)
  let scale = Juice.slowmo > 0 ? Juice.slowmoScale : 1;
  if (Game.effects.slow > 0) scale *= 0.62;
  const gdt = dt * scale;
  Game.time += gdt;
  Juice.update(dt);
  updateStars(gdt, heatLevel(), Game.ott.active);
  updateParticles(gdt);
  updateFloaters(gdt);
  updateAnnounce(dt);
  AudioSys.musicUpdate(dt);
  if (Game.state === 'playing' && !Game.paused) {
    Paddle.update(gdt);
    updateBalls(gdt);
    updateBricks(gdt);
    updatePowerups(gdt);
    updateEffects(gdt);
    updateOTT(gdt);
    updateHUDLive(gdt);
    // level clear check
    if (Game.state === 'playing' && !Game.bricks.some(b => b.alive)) {
      // small delay handled by levelClear's own flow; guard re-entry
      Game.bricks.push({ alive: false, dummy: true, x: -999, y: -999, w: 0, h: 0, type: 'normal', flash: 0, phase: 0, hitHintCd: 0 });
      Game.levelClear();
    }
  } else if (Game.state === 'title') {
    // idle attract: paddle sways
    Paddle.targetX = W / 2 + Math.sin(Game.time * 0.8) * 120;
    Paddle.targetY = PADDLE_ZONE_BOT - 60;
    Paddle.update(gdt);
  }
  render(gdt);
}

/* ---- wire buttons ---- */
function wireUI() {
  document.getElementById('btn-start').onclick = () => { AudioSys.init(); AudioSys.resume(); hideTitlePlay(0); };
  document.getElementById('btn-levels').onclick = () => {
    document.getElementById('level-grid').classList.toggle('hidden');
  };
  document.getElementById('btn-how').onclick = () => {
    hint(''); // noop
    announce('BREAK THROUGH → LIVE ON TOP ⚡', 1.6);
  };
  document.getElementById('btn-go').onclick = () => {
    AudioSys.init(); AudioSys.resume();
    Game.state = 'playing'; Game.paused = false; syncOverlays();
  };
  document.getElementById('btn-resume').onclick = () => Game.togglePause();
  document.getElementById('btn-pause').onclick = () => Game.togglePause();
  document.getElementById('btn-mute').onclick = () => { AudioSys.init(); AudioSys.toggleMute(); };
  document.getElementById('btn-restart').onclick = () => { Game.paused = false; Game.loadLevel(Game.level - 1, false); };
  document.getElementById('btn-quit').onclick = () => { Game.state = 'title'; Game.running = false; Game.paused = false; syncOverlays(); };
  document.getElementById('btn-retry').onclick = () => { hideTitlePlay(0); };
  document.getElementById('btn-again').onclick = () => { hideTitlePlay(0); };
  document.getElementById('btn-over-title').onclick = () => { Game.state = 'title'; syncOverlays(); };
  document.getElementById('btn-win-title').onclick = () => { Game.state = 'title'; syncOverlays(); };
  // prevent context menu / double-tap zoom on stage
  document.getElementById('stage').addEventListener('contextmenu', e => e.preventDefault());
  document.addEventListener('gesturestart', e => e.preventDefault());
}

/* ---- boot ---- */
let booted = false;
function boot() {
  if (booted) return; booted = true;
  Game.loadHi();
  initStars();
  Input.init(canvas);
  wireUI();
  resize();
  setTimeout(resize, 100);
  updateLevelButtons();
  Game.state = 'title';
  Game.bricks = buildBricks(0); // attract backdrop behind title
  Game.balls = [];
  syncOverlays();
  document.getElementById('hud-hi');
  requestAnimationFrame(frame);
}
document.addEventListener('DOMContentLoaded', boot);
// Fallback if script loads after DOMContentLoaded
if (document.readyState !== 'loading') boot();






