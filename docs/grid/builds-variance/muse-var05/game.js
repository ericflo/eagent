/* ============================================================================
   NEON BREACH — a modern Breakout about getting ABOVE the bricks.
   ----------------------------------------------------------------------------
   Single-file game (classic script, no modules/build) so it runs from both
   file:// and any static http server. Canvas logical resolution is 480x720
   and is scaled with devicePixelRatio (see Section: Canvas & Resize).

   TABLE OF CONTENTS
     1. Helpers & math            5. Levels & brick roster
     2. Persistent storage        6. Game state
     3. Audio (WebAudio synth)    7. Canvas / resize / HUD / overlays
     4. Tuning config             8. Input (mouse/keys/touch/stick)
                                  9. Entities: paddle/balls/bricks/powerups
                                  10. Collision & gate conditions
                                  11. Update (fixed timestep)
                                  12. Render & juice   13. Flow & boot
   ========================================================================== */
'use strict';

/* ============================ 1. HELPERS & MATH ========================= */
const TAU = Math.PI * 2;
const clamp = (v, a, b) => v < a ? a : (v > b ? b : v);
const lerp = (a, b, t) => a + (b - a) * t;
const rand = (a, b) => a + Math.random() * (b - a);
const randi = (a, b) => Math.floor(rand(a, b + 1));
const pick = arr => arr[Math.floor(Math.random() * arr.length)];
const hyp = (x, y) => Math.sqrt(x * x + y * y);
function fmt(n) { return n.toLocaleString('en-US'); }
// Smooth-step helper for juice curves (0..1 -> 0..1).
function sstep(t) { t = clamp(t, 0, 1); return t * t * (3 - 2 * t); }

/* ========================= 2. PERSISTENT STORAGE ======================== */
// High score survives reloads; settings survive too. All guarded for privacy
// modes where localStorage throws.
const store = {
  get(k, fb) {
    try { const v = localStorage.getItem(k); return v === null ? fb : v; }
    catch (e) { return fb; }
  },
  set(k, v) { try { localStorage.setItem(k, v); } catch (e) { /* ignore */ } }
};
let highScore = parseInt(store.get('neonBreachHigh', '0'), 10) || 0;

/* ============================ 3. TUNING CONFIG ========================== */
// Central tuning knobs. Paddle upward-smash values are documented in the
// ball-paddle collision section (10B).
const CFG = {
  W: 480, H: 720,                 // logical playfield
  paddleW: 92, paddleH: 14,       // base paddle size (expand: x1.45)
  paddleMinY: 0.78,               // paddle Y zone: bottom 22% of screen
  paddleSpeedX: 560, paddleSpeedY: 420,
  ballR: 7,
  ballBaseSpeed: 430,             // launch speed
  ballMinSpeed: 310, ballMaxSpeed: 980,
  ballRamp: 5,                    // +px/s per second of flight
  lightningMin: 780, lightningMax: 1080,
  speedGate: 560,                 // SPEED bricks break only above this
  angleGateDeg: 50,               // ANGLE bricks need impact > 50° from horizontal
  powerDrop: 0.16,                // drop chance per normal brick
  powerFall: 150,                 // powerup fall speed px/s
  fixedDt: 1 / 120,               // fixed timestep (substeps stop tunneling)
  odStepTime: 2.2,                // seconds in top-zone per multiplier doubling
  odTickScore: 12,                // score/sec * mult while in overdrive
  maxMult: 64,
};
const MULT_STEPS = [1, 2, 4, 8, 16, 32, 64];

/* ===================== 4. AUDIO (WEBAUDIO SYNTH, NO ASSETS) ============= */
// Everything is synthesized. AudioContext is created lazily on the first user
// gesture (browser autoplay policy). Master gain -> destination; music has its
// own gain so intensity layers can swell with the multiplier.
const AU = {
  ctx: null, master: null, musicG: null, odOsc: null, odGain: null,
  muted: store.get('neonBreachSound', 'on') !== 'off',
  volume: (parseInt(store.get('neonBreachVol', '70'), 10) || 70) / 100,
  musicT: 0, beatT: 0, beatN: 0,

  init() {
    // Must be called from a user gesture. Safe to call repeatedly.
    if (this.ctx) { if (this.ctx.state === 'suspended') this.ctx.resume(); return; }
    try {
      const AC = window.AudioContext || window.webkitAudioContext;
      if (!AC) return;
      this.ctx = new AC();
      this.master = this.ctx.createGain();
      this.master.gain.value = this.muted ? 0 : this.volume;
      this.master.connect(this.ctx.destination);
      this.musicG = this.ctx.createGain();
      this.musicG.gain.value = 0.30;
      this.musicG.connect(this.master);
    } catch (e) { this.ctx = null; }
  },
  setMuted(m) {
    this.muted = m;
    store.set('neonBreachSound', m ? 'off' : 'on');
    if (this.master && this.ctx) {
      this.master.gain.setTargetAtTime(m ? 0 : this.volume, this.ctx.currentTime, 0.03);
    }
  },
  setVolume(v) {
    this.volume = v;
    store.set('neonBreachVol', String(Math.round(v * 100)));
    if (this.master && this.ctx && !this.muted) {
      this.master.gain.setTargetAtTime(v, this.ctx.currentTime, 0.03);
    }
  },
  // Generic enveloped oscillator blip. The whole SFX kit builds on this.
  tone(freq, dur, type, vol, slideTo, when) {
    if (!this.ctx || this.muted) return;
    try {
      const t0 = this.ctx.currentTime + (when || 0);
      const o = this.ctx.createOscillator();
      const g = this.ctx.createGain();
      o.type = type || 'square';
      o.frequency.setValueAtTime(freq, t0);
      if (slideTo) o.frequency.exponentialRampToValueAtTime(Math.max(20, slideTo), t0 + dur);
      g.gain.setValueAtTime(vol || 0.2, t0);
      g.gain.exponentialRampToValueAtTime(0.0001, t0 + dur);
      o.connect(g); g.connect(this.master);
      o.start(t0); o.stop(t0 + dur + 0.02);
    } catch (e) { /* ignore */ }
  },
  // Short filtered noise burst (explosions, clanks).
  noise(dur, vol, freq, when) {
    if (!this.ctx || this.muted) return;
    try {
      const t0 = this.ctx.currentTime + (when || 0);
      const len = Math.max(1, Math.floor(this.ctx.sampleRate * dur));
      const buf = this.ctx.createBuffer(1, len, this.ctx.sampleRate);
      const d = buf.getChannelData(0);
      for (let i = 0; i < len; i++) d[i] = (Math.random() * 2 - 1) * (1 - i / len);
      const src = this.ctx.createBufferSource(); src.buffer = buf;
      const f = this.ctx.createBiquadFilter(); f.type = 'lowpass'; f.frequency.value = freq || 1200;
      const g = this.ctx.createGain(); g.gain.value = vol || 0.3;
      src.connect(f); f.connect(g); g.connect(this.master);
      src.start(t0);
    } catch (e) { /* ignore */ }
  },
  paddleBlip(edge) { this.tone(320 + edge * 380, 0.07, 'square', 0.16, 180 + edge * 200); },
  wallBlip() { this.tone(240, 0.05, 'square', 0.10, 180); },
  brick(combo) {
    // Pitch climbs with combo — the core "doing great!" feedback.
    const f = 380 * Math.pow(1.059, Math.min(combo, 24));
    this.tone(f, 0.11, 'square', 0.20, f * 1.6);
    this.tone(f * 2, 0.07, 'sine', 0.10);
  },
  clank() { this.noise(0.09, 0.25, 2600); this.tone(140, 0.12, 'sawtooth', 0.18, 90); },
  chime() { this.tone(660, 0.12, 'sine', 0.20, 990); this.tone(990, 0.16, 'sine', 0.16, 1320, 0.08); },
  launch() { this.tone(220, 0.16, 'sawtooth', 0.18, 660); },
  laser() { this.tone(1200, 0.09, 'sawtooth', 0.12, 300); },
  explode() { this.noise(0.35, 0.4, 900); this.tone(90, 0.3, 'sawtooth', 0.25, 40); },
  loseLife() { this.tone(500, 0.5, 'sawtooth', 0.22, 110); },
  bank() { this.tone(520, 0.09, 'sine', 0.18, 780); this.tone(780, 0.12, 'sine', 0.16, 1170, 0.07); },
  fanfare() {
    const seq = [523, 659, 784, 1046, 784, 1046, 1318];
    seq.forEach((f, i) => this.tone(f, 0.18, 'square', 0.18, null, i * 0.13));
  },
  odStart() { this.tone(150, 0.5, 'sawtooth', 0.20, 900); this.noise(0.4, 0.2, 3000); },
  // Overdrive riser loop: a slowly climbing detuned pair while active.
  odLoop(on) {
    if (!this.ctx) return;
    try {
      if (on && !this.odOsc) {
        this.odOsc = this.ctx.createOscillator();
        this.odGain = this.ctx.createGain();
        this.odOsc.type = 'sawtooth';
        this.odOsc.frequency.value = 110;
        this.odGain.gain.value = 0.0;
        this.odOsc.connect(this.odGain); this.odGain.connect(this.master);
        this.odOsc.start();
        this.odGain.gain.setTargetAtTime(0.06, this.ctx.currentTime, 0.4);
      } else if (on && this.odOsc) {
        const f = Math.min(880, this.odOsc.frequency.value + 30);
        this.odOsc.frequency.setTargetAtTime(f, this.ctx.currentTime, 0.5);
      } else if (!on && this.odOsc) {
        const o = this.odOsc, g = this.odGain;
        g.gain.setTargetAtTime(0.0, this.ctx.currentTime, 0.1);
        setTimeout(() => { try { o.stop(); } catch (e) {} }, 400);
        this.odOsc = null; this.odGain = null;
      }
    } catch (e) { /* ignore */ }
  },
  // Adaptive music: tiny 8-step sequencer. Tempo + brightness + extra layers
  // scale with the multiplier step index (0..6). Called from update().
  music(dt, multIdx, inOD) {
    if (!this.ctx || this.muted) return;
    this.musicT += dt;
    const bpm = 96 + multIdx * 14 + (inOD ? 20 : 0);
    const stepDur = 60 / bpm / 2;
    this.beatT += dt;
    if (this.beatT < stepDur) return;
    this.beatT -= stepDur;
    const bass = [55, 55, 82.4, 55, 65.4, 55, 98, 82.4];
    const n = this.beatN % 8; this.beatN++;
    try {
      const t0 = this.ctx.currentTime;
      const o = this.ctx.createOscillator(), g = this.ctx.createGain();
      o.type = 'sawtooth'; o.frequency.value = bass[n] * 2;
      g.gain.setValueAtTime(0.10 + multIdx * 0.012, t0);
      g.gain.exponentialRampToValueAtTime(0.0001, t0 + stepDur * 0.95);
      const f = this.ctx.createBiquadFilter(); f.type = 'lowpass';
      f.frequency.value = 400 + multIdx * 350;
      o.connect(f); f.connect(g); g.connect(this.musicG);
      o.start(t0); o.stop(t0 + stepDur);
      // Hat layer kicks in at x4+, sparkle arp at x16+ in overdrive.
      if (multIdx >= 2 && (n % 2 === 0)) this.tone(6000, 0.03, 'square', 0.03);
      if (inOD && multIdx >= 4) {
        const arp = [440, 554, 659, 880];
        this.tone(arp[n % 4] * 2, 0.08, 'sine', 0.05);
      }
    } catch (e) { /* ignore */ }
  }
};
/* ================== 5. LEVELS & BRICK ROSTER (NO multi-HP!) ==============
   Difficulty NEVER comes from hit-points: every brick breaks in ONE clean
   hit once you satisfy its condition. Legend glyphs are drawn on-canvas
   (see renderBricks) and in the HTML field guide.
     S standard  : 1 hit, anything.            (cyan)
     A angle     : breaks only if impact angle is STEEP (>50° from
                   horizontal); shallow hits bounce off with a CLINK. (orange)
     V speed     : breaks only if ball speed > threshold; slow balls bounce.
                   Glows harder as your fastest ball gets close. (magenta)
     G gate      : one-way — breaks only on TOP/BOTTOM hits (chevrons);
                   side hits bounce. (green)
     M mover     : 1 hit, but slides horizontally. (violet)
     X explosive : 1 hit, then chain-detonates neighbours. (red)
     B bonus     : 1 hit + GUARANTEED powerup drop. (gold)
   Maps are 9 columns wide. '.' = empty. Levels are designed with funnels /
   gaps so you can punch through to the top — the fun part. */
const BRICKS = {
  S: { name: 'Standard',  color: '#22e6ff', glow: '#22e6ff', desc: 'Breaks in one hit.' },
  A: { name: 'Angle',     color: '#ff9a3d', glow: '#ff9a3d', desc: 'Only breaks on STEEP hits (>50°). Shallow hits bounce off!' },
  V: { name: 'Volt',      color: '#ff3df0', glow: '#ff3df0', desc: 'Only breaks when the ball is FAST. Smash upward with the paddle!' },
  G: { name: 'Gate',      color: '#3dff8a', glow: '#3dff8a', desc: 'One-way: breaks from TOP or BOTTOM only. Side hits bounce.' },
  M: { name: 'Shifter',   color: '#9a6bff', glow: '#9a6bff', desc: 'Slides sideways. Still one hit!' },
  X: { name: 'Volatile',  color: '#ff4d6d', glow: '#ff4d6d', desc: 'Explodes and chains into neighbours!' },
  B: { name: 'Bonus',     color: '#ffd23d', glow: '#ffd23d', desc: 'Guaranteed powerup drop!' },
};

const LEVELS = [
  {
    name: 'First Breach', sub: 'Level 1 of 5',
    desc: 'Punch through the middle and park the ball ABOVE the bricks. That is where the points live.',
    new: 'NEW: Standard ★ Bonus bricks — Bonus always drops a powerup!',
    hint: '⬆ Break the middle column, then get the ball ABOVE the bricks for OVERDRIVE!',
    map: [
      '....B....',
      '....S....',
      '...SSS...',
      '...SSS...',
      '..SSSSS..',
      '..SSSSS..',
      '.SSSSSSS.',
    ]
  },
  {
    name: 'Steep Approach', sub: 'Level 2 of 5',
    desc: 'Orange ANGLE bricks shrug off flat hits — come down on them steeply. Green GATES only open from top or bottom.',
    new: 'NEW: ◣ Angle bricks (steep hits only) + ≋ Gate bricks (top/bottom only)',
    hint: 'Hit ◣ bricks nearly straight-on · Hit ≋ bricks from above or below!',
    map: [
      'SSAAAAASS',
      'SSAAAAASS',
      'GGSSSSGGG',
      'SSSBSSBSS',
      'SSSSSSSSS',
    ]
  },
  {
    name: 'Velocity', sub: 'Level 3 of 5',
    desc: 'Pink VOLT bricks only break for FAST balls. Rising-slam the paddle upward into the ball to inject speed. Watch the sliding Shifters.',
    new: 'NEW: ⚡ Volt bricks (fast balls only) + ⇔ Shifter bricks (they slide!)',
    hint: 'Move the paddle UP into the ball to make it FASTER — pink bricks need speed!',
    map: [
      'VVVV.VVVV',
      'MMMMMMMMM',
      'SSSGSSGSS',
      'SSSBSSBSS',
      'SSSSSSSSS',
    ]
  },
  {
    name: 'Chain Reaction', sub: 'Level 4 of 5',
    desc: 'Volatiles chain-detonate. Crack one and let the explosion do your job for you.',
    new: 'NEW: ✸ Volatile bricks — hit one, clear the neighbourhood!',
    hint: 'Aim for a ✸ Volatile and enjoy the fireworks. Explosions ignore gates!',
    map: [
      'SSXSXSXSS',
      'SXSXSXSXS',
      'MMMGGGMMM',
      'GGAAAAAGG',
      'SSBSSSBSS',
      'SSSSSSSSS',
    ]
  },
  {
    name: 'Overdrive Gauntlet', sub: 'Level 5 of 5',
    desc: 'Everything at once. Thread the gaps, live above the bricks, and ride the ×64.',
    new: 'FINAL: every brick type. Survive. Overdrive. Win.',
    hint: 'Stay ABOVE the field as long as you can — the multiplier doubles every few seconds!',
    map: [
      'VXAGBGAVX',
      'MMSSBSSMM',
      'GAAVVVAAG',
      'SSXBXBXSS',
      'SSSBSSBSS',
      '..SSSSS..',
      'SSSSSSSSS',
    ]
  },
];

/* ======================= 6. GAME STATE ================================== */
const G = {
  state: 'menu',        // menu|intro|playing|paused|clear|over|win
  score: 0, levelStartScore: 0,
  mult: 1, multIdx: 0, combo: 0,
  lives: 3, levelIdx: 0,
  time: 0,              // total running play time (drives movers, anim)
  shake: 0,             // trauma 0..1 (screen shake)
  freezeT: 0,           // hit-stop timer (micro freeze on big events)
  slowT: 0,             // life-loss slow-mo timer
  // Overdrive: active while ANY live ball is above the lowest alive brick row.
  od: { active: false, time: 0, stepAcc: 0, tickAcc: 0, banked: 0 },
  balls: [], bricks: [], powerups: [], lasers: [], parts: [], floaters: [], booms: [],
  // Timed paddle/global effects (seconds remaining). Ball-type timers live
  // on each ball (fireT/ghostT/lightT) so multiballs can differ.
  eff: { expandT: 0, slowT: 0, laserT: 0, laserCd: 0 },
  quality: 'high', shakeOn: true,
  hintT: 0,
};

const P = {
  // Paddle. x,y = center. tx,ty = pointer target. vx,vy = smoothed velocity
  // (vy<0 means moving UP — used for the upward-smash acceleration bonus).
  x: CFG.W / 2, y: CFG.H - 90, tx: CFG.W / 2, ty: CFG.H - 90,
  vx: 0, vy: 0, w: CFG.paddleW, h: CFG.paddleH, squash: 0, glow: 0,
};
function paddleMinY() { return CFG.H * CFG.paddleMinY; }
function paddleMaxY() { return CFG.H - 34; }
function paddleW() { return CFG.paddleW * (G.eff.expandT > 0 ? 1.45 : 1); }

/* ================= 7. CANVAS / RESIZE / HUD / OVERLAYS ================== */
const cv = document.getElementById('game');
const ctx = cv.getContext('2d');
let DPR = 1, viewScale = 1;

function resize() {
  // Canvas fills its wrapper; the logical 480x720 field is letterboxed with
  // 'cover'-like math done manually in render (uniform scale + center).
  const wrap = document.getElementById('canvas-wrap');
  const r = wrap.getBoundingClientRect();
  DPR = Math.min(window.devicePixelRatio || 1, 2);
  cv.width = Math.max(2, Math.round(r.width * DPR));
  cv.height = Math.max(2, Math.round(r.height * DPR));
  viewScale = Math.min(cv.width / CFG.W, cv.height / CFG.H);
}
window.addEventListener('resize', resize);
window.addEventListener('orientationchange', () => setTimeout(resize, 120));

const $ = id => document.getElementById(id);
const el = {
  score: $('hud-score'), high: $('hud-high'), mult: $('hud-mult'),
  lives: $('hud-lives'), level: $('hud-level'),
  odFill: $('od-fill'), odLabel: $('od-label'), fx: $('fx-bar'),
  banner: $('banner'), bannerMain: $('banner-main'), bannerSub: $('banner-sub'), hint: $('hint'),
  multBlock: document.querySelector('.mult-block'),
  overlays: {
    menu: $('ov-menu'), legend: $('ov-legend'), intro: $('ov-intro'),
    pause: $('ov-pause'), clear: $('ov-clear'), over: $('ov-over'), win: $('ov-win'),
  },
};
function showOverlay(name) {
  for (const k in el.overlays) el.overlays[k].classList.toggle('hidden', k !== name);
  if (!name) for (const k in el.overlays) el.overlays[k].classList.add('hidden');
  if (name === 'menu' && typeof refreshLevelButtons === 'function') refreshLevelButtons();
}
function refreshHigh() {
  el.high.textContent = fmt(highScore);
  $('menu-high').textContent = fmt(highScore);
}
function addScore(n) {
  G.score += n;
  if (G.score > highScore) {
    highScore = G.score;
    store.set('neonBreachHigh', String(highScore));
  }
}
// Multiplier ladder: combo>=threshold steps up; overdrive doubles over time.
function setMultIdx(i, reason) {
  i = clamp(i, 0, MULT_STEPS.length - 1);
  if (i !== G.multIdx) {
    G.multIdx = i; G.mult = MULT_STEPS[i];
    el.multBlock.classList.toggle('hot', i >= 3);
    if (i >= 3) pulseBanner('MULTIPLIER UP', '×' + G.mult + (reason ? ' ' + reason : ''));
  }
}
function bumpCombo() {
  G.combo++;
  // Every 5 consecutive breaks without draining steps the multiplier up.
  if (G.combo % 5 === 0 && G.multIdx < MULT_STEPS.length - 1) {
    setMultIdx(G.multIdx + 1, 'COMBO');
    addFloat(CFG.W / 2, CFG.H * 0.5, 'COMBO ×' + G.mult, '#ffd23d', 22);
  }
}
function resetCombo(full) {
  G.combo = 0;
  if (full) setMultIdx(0);
}
function updateHUD() {
  el.score.textContent = fmt(G.score);
  el.high.textContent = fmt(highScore);
  el.mult.textContent = '×' + G.mult;
  el.level.textContent = (G.levelIdx + 1) + '/' + LEVELS.length;
  el.lives.textContent = '●'.repeat(Math.max(0, G.lives)) + '○'.repeat(Math.max(0, 3 - G.lives)) +
    (G.lives > 3 ? ' +' + (G.lives - 3) : '');
  // Overdrive meter: progress toward next doubling.
  if (G.od.active) {
    const p = G.od.stepAcc / CFG.odStepTime;
    el.odFill.style.width = (clamp(p, 0, 1) * 100).toFixed(1) + '%';
    el.odLabel.textContent = 'OVERDRIVE ×' + G.mult + ' — banked ' + fmt(G.od.banked);
  } else {
    el.odFill.style.width = '0%';
    el.odLabel.textContent = G.bricks.length
      ? 'Get ABOVE the bricks for OVERDRIVE ×2 → ×64!'
      : 'Field clear!';
  }
  // Timed-effect icons with remaining seconds.
  const icons = [];
  if (G.eff.expandT > 0) icons.push(['WIDE', G.eff.expandT, false]);
  if (G.eff.slowT > 0) icons.push(['SLOW-MO', G.eff.slowT, false]);
  if (G.eff.laserT > 0) icons.push(['LASER', G.eff.laserT, false]);
  const fb = G.balls.find(b => b.fireT > 0);
  const gb = G.balls.find(b => b.ghostT > 0);
  const lb = G.balls.find(b => b.lightT > 0);
  if (fb) icons.push(['FIRE', fb.fireT, false]);
  if (gb) icons.push(['GHOST', gb.ghostT, false]);
  if (lb) icons.push(['BOLT', lb.lightT, false]);
  el.fx.innerHTML = icons.map(([n, t, w]) =>
    '<span class="fx' + (w ? ' warn' : '') + '">' + n + ' <span class="t">' +
    Math.ceil(t) + 's</span></span>').join('');
}
// Legend (field guide) rendered from the BRICKS roster — single source of truth.
function brickSwatchHTML(k) {
  const b = BRICKS[k];
  return '<div class="leg-row"><span class="sw" style="color:' + b.color +
    ';background:' + b.color + '"></span><span><b>' + b.name +
    '</b> — ' + b.desc + '</span></div>';
}
function buildLegends() {
  $('legend-mini').innerHTML = ['S', 'A', 'V'].map(brickSwatchHTML).join('') +
    '<div class="leg-row"><span>…plus <b>Gate / Shifter / Volatile / Bonus</b> in later levels!</span></div>';
  $('legend-full').innerHTML = Object.keys(BRICKS).map(brickSwatchHTML).join('');
}
// Big center banner (overdrive entry, multiplier steps) with re-trigger.
// Two-arg form: pulseBanner(main, sub). Single-arg form pulseBanner(sub)
// is kept for backwards compatibility (main text left unchanged).
let bannerTO = null;
function pulseBanner(main, sub) {
  if (sub === undefined) { sub = main; main = null; }
  if (main) el.bannerMain.textContent = main;
  el.bannerSub.textContent = sub || '';
  el.banner.classList.remove('hidden', 'show');
  void el.banner.offsetWidth; // restart CSS animation
  el.banner.classList.add('show');
  clearTimeout(bannerTO);
  bannerTO = setTimeout(() => el.banner.classList.add('hidden'), 1600);
}
function showHint(text, secs) {
  el.hint.textContent = text;
  el.hint.classList.remove('hidden');
  G.hintT = secs || 6;
}
/* ============ 8. INPUT: MOUSE / KEYS / TOUCH / THUMBSTICK =============== */
const keys = {};
// Floating thumbstick state: touchstart inside #stick sets the base where the
// thumb landed; drag offset (clamped to radius) drives paddle VELOCITY, so the
// game is fully playable one-thumb. Canvas touches use RELATIVE drag instead
// (grab anywhere, paddle follows your finger delta — no occlusion problem).
const stick = { id: null, bx: 0, by: 0, dx: 0, dy: 0, active: false };
const drag = { id: null, lx: 0, ly: 0 };
const STICK_R = 46;

function canvasPos(clientX, clientY) {
  const r = cv.getBoundingClientRect();
  return {
    x: (clientX - r.left) / r.width * CFG.W,
    y: (clientY - r.top) / r.height * CFG.H,
  };
}
function isTouchDevice() {
  return ('ontouchstart' in window) || (navigator.maxTouchPoints > 0);
}

// --- Keyboard ---
window.addEventListener('keydown', e => {
  if (e.repeat) { keys[e.code] = true; return; }
  keys[e.code] = true;
  AU.init();
  if (e.code === 'Space') { e.preventDefault(); onAction(); }
  else if (e.code === 'KeyP' || e.code === 'Escape') togglePause();
  else if (e.code === 'KeyM') toggleMute();
});
window.addEventListener('keyup', e => { keys[e.code] = false; });

// --- Mouse: paddle follows pointer X/Y inside the play zone; click = action.
cv.addEventListener('mousemove', e => {
  const p = canvasPos(e.clientX, e.clientY);
  P.tx = clamp(p.x, 20, CFG.W - 20);
  P.ty = clamp(p.y, paddleMinY(), paddleMaxY());
});
cv.addEventListener('mousedown', e => { AU.init(); onAction(); });
function onAction() {
  // Context button: menu->start, intro->begin, playing->launch/fire, etc.
  AU.init();
  if (G.state === 'menu') startGame();
  else if (G.state === 'intro') beginLevel();
  else if (G.state === 'playing') launchOrFire();
  else if (G.state === 'paused') resumeGame();
  else if (G.state === 'clear') nextLevel();
  else if (G.state === 'over') retryLevel();
  else if (G.state === 'win') restartAll();
}

// --- Touch: canvas drag (relative) + floating stick zone + buttons. ---
cv.addEventListener('touchstart', e => {
  e.preventDefault(); AU.init();
  document.body.classList.add('touch');
  for (const t of e.changedTouches) {
    if (drag.id === null) {
      const p = canvasPos(t.clientX, t.clientY);
      drag.id = t.identifier; drag.lx = p.x; drag.ly = p.y;
    }
  }
}, { passive: false });
cv.addEventListener('touchmove', e => {
  e.preventDefault();
  for (const t of e.changedTouches) {
    if (t.identifier === drag.id) {
      const p = canvasPos(t.clientX, t.clientY);
      // Relative drag: paddle target shifts by finger delta (scaled up a bit
      // so small thumbs can cross the whole field comfortably).
      P.tx = clamp(P.tx + (p.x - drag.lx) * 1.35, 20, CFG.W - 20);
      P.ty = clamp(P.ty + (p.y - drag.ly) * 1.35, paddleMinY(), paddleMaxY());
      drag.lx = p.x; drag.ly = p.y;
    }
  }
}, { passive: false });
function dragEnd(e) {
  for (const t of e.changedTouches) if (t.identifier === drag.id) drag.id = null;
}
cv.addEventListener('touchend', dragEnd);
cv.addEventListener('touchcancel', dragEnd);
// Tap (short touch without much move) on canvas = launch.
let tapT = 0, tapMoved = false;
cv.addEventListener('touchstart', () => { tapT = performance.now(); tapMoved = false; }, { passive: true });
cv.addEventListener('touchmove', () => { tapMoved = true; }, { passive: true });
cv.addEventListener('touchend', e => {
  if (performance.now() - tapT < 250 && !tapMoved) onAction();
}, { passive: true });

const stickZone = $('stick'), stickBase = $('stick-base'), stickKnob = $('stick-knob');
stickZone.addEventListener('touchstart', e => {
  e.preventDefault(); e.stopPropagation(); AU.init();
  document.body.classList.add('touch');
  const t = e.changedTouches[0];
  const wrap = $('canvas-wrap').getBoundingClientRect();
  stick.id = t.identifier; stick.active = true;
  stick.bx = t.clientX - wrap.left; stick.by = t.clientY - wrap.top;
  stick.dx = 0; stick.dy = 0;
  stickBase.style.display = 'block';
  stickBase.style.left = stick.bx + 'px';
  stickBase.style.top = stick.by + 'px';
  stickKnob.style.transform = 'translate(-50%,-50%)';
}, { passive: false });
stickZone.addEventListener('touchmove', e => {
  e.preventDefault(); e.stopPropagation();
  for (const t of e.changedTouches) {
    if (t.identifier !== stick.id) continue;
    const wrap = $('canvas-wrap').getBoundingClientRect();
    let dx = (t.clientX - wrap.left) - stick.bx;
    let dy = (t.clientY - wrap.top) - stick.by;
    const m = hyp(dx, dy);
    if (m > STICK_R) { dx = dx / m * STICK_R; dy = dy / m * STICK_R; }
    stick.dx = dx / STICK_R; stick.dy = dy / STICK_R;
    stickKnob.style.transform =
      'translate(calc(-50% + ' + dx + 'px), calc(-50% + ' + dy + 'px))';
  }
}, { passive: false });
function stickEnd(e) {
  for (const t of e.changedTouches) {
    if (t.identifier === stick.id) {
      stick.id = null; stick.active = false; stick.dx = stick.dy = 0;
      stickBase.style.display = 'none';
    }
  }
}
stickZone.addEventListener('touchend', stickEnd);
stickZone.addEventListener('touchcancel', stickEnd);

$('btn-launch').addEventListener('click', e => { e.stopPropagation(); onAction(); });
$('btn-pause-touch').addEventListener('click', e => { e.stopPropagation(); AU.init(); togglePause(); });
// Any first gesture unlocks audio.
window.addEventListener('pointerdown', () => AU.init(), { passive: true });

function toggleMute() {
  AU.init();
  AU.setMuted(!AU.muted);
  syncMuteUI();
}
function syncMuteUI() {
  $('chk-sound').checked = !AU.muted;
  $('btn-mute-pause').textContent = AU.muted ? '🔇 Sound: Off' : '🔊 Sound: On';
}
/* ======== 9. ENTITIES: PADDLE / BALLS / BRICKS / POWERUPS / JUICE ======= */
function resetPaddle() {
  P.x = P.tx = CFG.W / 2; P.y = P.ty = CFG.H - 90;
  P.vx = P.vy = 0; P.squash = 0; P.glow = 0;
}

function buildLevel(idx) {
  G.bricks.length = 0; G.powerups.length = 0; G.lasers.length = 0;
  G.booms.length = 0;
  const L = LEVELS[idx];
  const cols = 9, margin = 16, gap = 6;
  const bw = (CFG.W - margin * 2 - gap * (cols - 1)) / cols, bh = 22;
  const y0 = 92, rowH = 28;
  L.map.forEach((row, r) => {
    for (let c = 0; c < Math.min(cols, row.length); c++) {
      const ch = row[c];
      if (ch === '.' || !BRICKS[ch]) continue;
      const w = bw, h = bh;
      const x = margin + c * (bw + gap), y = y0 + r * rowH;
      G.bricks.push({
        type: ch, x, y, w, h,
        cx: x + w / 2,
        phase: Math.random() * TAU,          // mover / shimmer phase
        range: 30 + Math.random() * 26,      // mover travel
        ghostCd: 0, boomT: -1,               // fuse for chain explosions
        born: G.time,
      });
    }
  });
}

function makeBall(x, y, opts) {
  opts = opts || {};
  const a = opts.angle !== undefined ? opts.angle : rand(-0.5, 0.5);
  const sp = opts.speed || CFG.ballBaseSpeed;
  return {
    x, y, r: CFG.ballR,
    vx: Math.sin(a) * sp, vy: opts.up === false ? Math.abs(Math.cos(a)) * sp : -Math.abs(Math.cos(a)) * sp,
    stuck: !!opts.stuck, fireT: 0, ghostT: 0, lightT: 0,
    trail: [], spin: rand(0, TAU),
  };
}
function spawnStuckBall() {
  const b = makeBall(P.x, P.y - 20, { stuck: true, angle: 0 });
  b.vx = 0; b.vy = 0;
  G.balls.push(b);
}
function launchOrFire() {
  // Laser paddle fires bolts with the launch action too (space/tap/LAUNCH).
  if (G.eff.laserT > 0 && G.eff.laserCd <= 0) fireLaser();
  const stuck = G.balls.find(b => b.stuck);
  if (stuck) {
    stuck.stuck = false;
    const a = rand(-0.35, 0.35);
    stuck.vx = Math.sin(a) * CFG.ballBaseSpeed;
    stuck.vy = -Math.abs(Math.cos(a)) * CFG.ballBaseSpeed;
    AU.launch();
    burst(stuck.x, stuck.y, '#22e6ff', 10, 160);
  }
}
function ballSpeed(b) { return hyp(b.vx, b.vy); }
function setBallSpeed(b, sp) {
  const s = ballSpeed(b) || 1;
  b.vx = b.vx / s * sp; b.vy = b.vy / s * sp;
}

// --- Powerups: falling capsules caught with the paddle. Roster (8): ---
const POWERS = {
  expand: { name: 'WIDE',   color: '#22e6ff', dur: 20, w: 3, desc: 'Wide paddle 20s' },
  multi:  { name: '×3',     color: '#9a6bff', dur: 0,  w: 3, desc: 'Multiball: splits into 3!' },
  fire:   { name: 'FIRE',   color: '#ff9a3d', dur: 10, w: 2, desc: 'Fire balls pierce bricks!' },
  ghost:  { name: 'GHOST',  color: '#c77dff', dur: 8,  w: 2, desc: 'Ghost balls phase through!' },
  slow:   { name: 'SLOW',   color: '#3dff8a', dur: 8,  w: 2, desc: 'Slow-mo everything!' },
  laser:  { name: 'LASER',  color: '#ff3df0', dur: 12, w: 2, desc: 'Launch fires laser bolts!' },
  life:   { name: '+1',     color: '#ffffff', dur: 0,  w: 1, desc: 'Extra life!' },
  bolt:   { name: 'BOLT',   color: '#fff23d', dur: 10, w: 1, desc: 'LIGHTNING ball: super fast, double score!' },
};
function rollPower() {
  const bag = [];
  for (const k in POWERS) for (let i = 0; i < POWERS[k].w; i++) bag.push(k);
  return pick(bag);
}
function dropPower(x, y, force) {
  const type = force || (Math.random() < CFG.powerDrop ? rollPower() : null);
  if (!type) return;
  G.powerups.push({
    type, x: clamp(x, 20, CFG.W - 20), y,
    vy: CFG.powerFall, w: 46, h: 18, phase: rand(0, TAU),
  });
}
function applyPower(type) {
  AU.chime();
  const pdef = POWERS[type];
  addFloat(P.x, P.y - 26, pdef.name + '!', pdef.color, 18);
  burst(P.x, P.y - 10, pdef.color, 16, 200);
  switch (type) {
    case 'expand': G.eff.expandT = pdef.dur; break;
    case 'slow': G.eff.slowT = pdef.dur; break;
    case 'laser': G.eff.laserT = pdef.dur; break;
    case 'fire': G.balls.forEach(b => { b.fireT = pdef.dur; b.ghostT = 0; }); break;
    case 'ghost': G.balls.forEach(b => { b.ghostT = pdef.dur; b.fireT = 0; }); break;
    case 'bolt':
      G.balls.forEach(b => {
        b.lightT = pdef.dur;
        if (ballSpeed(b) < CFG.lightningMin) setBallSpeed(b, CFG.lightningMin);
      });
      addShake(0.25);
      break;
    case 'life':
      G.lives = Math.min(6, G.lives + 1);
      addScore(250 * G.mult);
      break;
    case 'multi': {
      // Multiball: every live ball splits into 3 (original + 2 angled clones).
      const src = G.balls.filter(b => !b.stuck);
      const seed = src.length ? src : G.balls.slice(0, 1);
      seed.forEach(b => {
        for (const da of [-0.5, 0.5]) {
          const nb = makeBall(b.x, b.y, { angle: 0 });
          const s = Math.max(ballSpeed(b), CFG.ballBaseSpeed);
          const base = Math.atan2(b.vx || rand(-1, 1), -(b.vy || -1));
          nb.vx = Math.sin(base + da) * s; nb.vy = -Math.cos(base + da) * s;
          nb.fireT = b.fireT; nb.ghostT = b.ghostT; nb.lightT = b.lightT;
          G.balls.push(nb);
          burst(b.x, b.y, '#9a6bff', 8, 180);
        }
      });
      break;
    }
  }
}
function fireLaser() {
  G.eff.laserCd = 0.22;
  [-1, 1].forEach(s => {
    G.lasers.push({
      x: P.x + s * (paddleW() / 2 - 6), y: P.y - 12,
      vy: -760, w: 5, h: 14,
    });
  });
  AU.laser();
}

// --- Juice primitives ---
function addShake(n) { if (G.shakeOn) G.shake = clamp(G.shake + n, 0, 1); }
function hitStop(t) { G.freezeT = Math.max(G.freezeT, t); }
function burst(x, y, color, n, spd) {
  if (G.quality === 'low') n = Math.ceil(n / 3);
  else if (G.quality === 'medium') n = Math.ceil(n / 1.6);
  for (let i = 0; i < n; i++) {
    if (G.parts.length > 500) return;
    const a = rand(0, TAU), s = rand(spd * 0.25, spd);
    G.parts.push({
      x, y, vx: Math.cos(a) * s, vy: Math.sin(a) * s,
      life: rand(0.3, 0.8), age: 0, size: rand(2, 4.5), color,
    });
  }
}
function addFloat(x, y, text, color, size) {
  if (G.floaters.length > 40) G.floaters.shift();
  G.floaters.push({ x: clamp(x, 60, CFG.W - 60), y, text, color, size: size || 15, age: 0, life: 1.1 });
}
function addBoom(x, y, radius, delay) {
  G.booms.push({ x, y, radius, t: delay || 0, age: 0 });
}
/* ============ 10. COLLISION & GATE CONDITIONS ============================
   circleVsBrick returns the contact normal (from brick toward ball center)
   plus penetration depth. Gate logic then decides BREAK vs BOUNCE:
     A angle: impactAngle = atan2(|vy|,|vx|) must exceed 50°. Shallow → clink.
     V speed: |v| must exceed CFG.speedGate. Slow → clank + hint glow.
     G gate : |normal.y| > 0.5 required (hit from top/bottom). Side → clank.
   Fire balls melt through anything; ghost balls phase (no bounce) and damage
   anything they overlap (per-brick immunity 0.25s); explosions ignore gates.
   NOTHING here uses multi-HP: a satisfied condition always one-shots. */
function circleBrickNormal(bx, by, r, br) {
  const nx = clamp(bx, br.x, br.x + br.w);
  const ny = clamp(by, br.y, br.y + br.h);
  let dx = bx - nx, dy = by - ny;
  const d2 = dx * dx + dy * dy;
  if (d2 > r * r) return null;
  if (d2 > 0.0001) {
    const d = Math.sqrt(d2);
    return { nx: dx / d, ny: dy / d, depth: r - d };
  }
  // Center inside the brick: push out along the least-penetration axis.
  const L = bx - br.x, R = br.x + br.w - bx, T = by - br.y, B = br.y + br.h - by;
  const m = Math.min(L, R, T, B);
  if (m === T) return { nx: 0, ny: -1, depth: r + T };
  if (m === B) return { nx: 0, ny: 1, depth: r + B };
  if (m === L) return { nx: -1, ny: 0, depth: r + L };
  return { nx: 1, ny: 0, depth: r + R };
}
function reflectVel(b, nx, ny) {
  const d = b.vx * nx + b.vy * ny;
  b.vx -= 2 * d * nx; b.vy -= 2 * d * ny;
  ensureMinVy(b);
}
// Anti-stall: kill near-horizontal loops. If |vy| < 12% of speed, redirect
// to ±12% (sign preserved; vy==0 picks up when the ball is low, down when
// high) and renormalize vx so total speed is unchanged (no balance change).
function ensureMinVy(b) {
  const sp = ballSpeed(b);
  if (sp <= 1) return;
  const min = sp * 0.12;
  if (Math.abs(b.vy) >= min) return;
  const s = b.vy !== 0 ? Math.sign(b.vy) : (b.y > CFG.H / 2 ? -1 : 1);
  b.vy = s * min;
  const vxMag = Math.sqrt(Math.max(0, sp * sp - b.vy * b.vy));
  b.vx = (b.vx !== 0 ? Math.sign(b.vx) : (Math.random() < 0.5 ? -1 : 1)) * vxMag;
}
// Returns 'break' | 'bounce' | 'phase' (ghost passes, damages separately).
function gateCheck(br, b, fromExplosion) {
  if (fromExplosion) return 'break';
  if (b.fireT > 0) return 'break';       // fire melts through everything
  if (b.ghostT > 0) return 'phase';      // ghost phases, damage via overlap
  const sp = ballSpeed(b);
  if (br.type === 'A') {
    const deg = Math.atan2(Math.abs(b.vy), Math.abs(b.vx)) * 180 / Math.PI;
    return deg >= CFG.angleGateDeg ? 'break' : 'bounce';
  }
  if (br.type === 'V') return sp >= CFG.speedGate ? 'break' : 'bounce';
  if (br.type === 'G') {
    const n = circleBrickNormal(b.x, b.y, b.r, br);
    if (n && Math.abs(n.ny) > 0.5) return 'break';
    return 'bounce';
  }
  return 'break'; // S, M, X, B always break on contact
}
function rejectFeedback(br, b) {
  // Distinct CLINK/CLANK + sparks when a gate condition fails.
  AU.clank();
  burst(b.x, b.y, '#9fb3d9', 6, 140);
  const cx = br.x + br.w / 2, cy = br.y + br.h / 2;
  if (br.type === 'A') addFloat(cx, cy - 8, 'TOO FLAT!', '#ff9a3d', 13);
  else if (br.type === 'V') addFloat(cx, cy - 8, 'TOO SLOW!', '#ff3df0', 13);
  else if (br.type === 'G') addFloat(cx, cy - 8, 'SIDE BLOCKED!', '#3dff8a', 13);
  addShake(0.08);
}
function breakBrick(br, b, fromExplosion) {
  const idx = G.bricks.indexOf(br);
  if (idx === -1) return;
  G.bricks.splice(idx, 1);
  const cx = br.x + br.w / 2, cy = br.y + br.h / 2;
  const info = BRICKS[br.type];
  const odBonus = G.od.active ? 2 : 1;
  const lightMul = (b && b.lightT > 0) ? 2 : 1;
  const pts = Math.round(50 * G.mult * odBonus * lightMul);
  addScore(pts);
  bumpCombo();
  addFloat(cx, cy, '+' + fmt(pts), info.color, 14 + Math.min(10, G.multIdx * 1.5));
  burst(cx, cy, info.color, br.type === 'X' ? 26 : 14, 260);
  AU.brick(G.combo);
  addShake(br.type === 'X' ? 0.35 : 0.10);
  if (br.type === 'X') {
    // Volatile: detonate — chain hits neighbours after a short fuse so the
    // chain reads as an explosion cascade, not an instant clear.
    AU.explode();
    hitStop(0.06);
    addShake(0.3);
    addBoom(cx, cy, 86, 0.02);
    G.bricks.slice().forEach(nb => {
      if (nb.boomT >= 0) return;
      const nx = nb.x + nb.w / 2, ny = nb.y + nb.h / 2;
      if (hyp(nx - cx, ny - cy) < 86) nb.boomT = 0.12 + Math.random() * 0.12;
    });
  }
  // Drops: Bonus bricks ALWAYS drop; others roll the dice.
  if (br.type === 'B') dropPower(cx, cy, rollPower());
  else dropPower(cx, cy, null);
  if (G.multIdx >= 4) hitStop(0.03);
  checkClear();
}
function checkClear() {
  if (G.bricks.length === 0 && (G.state === 'playing')) levelClear();
}
// Ghost overlap damage: called per substep for ghost balls (no bounce).
function ghostDamage(b) {
  for (let i = G.bricks.length - 1; i >= 0; i--) {
    const br = G.bricks[i];
    if (br.ghostCd > 0) continue;
    if (circleBrickNormal(b.x, b.y, b.r + 2, br)) {
      br.ghostCd = 0.25;
      breakBrick(br, b, false);
    }
  }
}

/* ---- 10B. BALL-PADDLE: angle control + English + UPWARD SMASH ----------
   - Hit position steers rebound angle (up to 65° off vertical).
   - Paddle X velocity adds "English" ( Sidespin ): vx += paddle.vx * 0.28.
   - UPWARD paddle motion (vy < 0) at impact injects speed AND steepens the
     launch: newSpeed += |vy|*0.55 (capped +240), and the rebound angle is
     pulled toward vertical by up to 40%. Downward motion softens slightly.
   Tuned so a hard rising slam is the reliable way to crack VOLT bricks and
   punch the ball above the field. */
function paddleBounce(b) {
  const w = paddleW();
  const rel = clamp((b.x - P.x) / (w / 2), -1, 1);
  let speed = ballSpeed(b);
  speed = clamp(speed + CFG.ballRamp * 0.5, CFG.ballMinSpeed, CFG.ballMaxSpeed);
  const smash = Math.max(0, -P.vy);          // upward paddle speed px/s
  if (smash > 60) speed = Math.min(CFG.ballMaxSpeed, speed + Math.min(240, smash * 0.55));
  else if (P.vy > 200) speed = Math.max(CFG.ballMinSpeed, speed - 60);
  let ang = rel * (65 * Math.PI / 180);      // off-vertical
  if (smash > 60) ang *= lerp(1, 0.6, clamp(smash / 700, 0, 1)); // steepen
  speed = clamp(speed, CFG.ballMinSpeed, b.lightT > 0 ? CFG.lightningMax : CFG.ballMaxSpeed);
  b.vx = Math.sin(ang) * speed + P.vx * 0.28;
  b.vy = -Math.abs(Math.cos(ang) * speed);
  // Re-clamp after English so min/max guarantees hold.
  const s2 = ballSpeed(b);
  const cap = b.lightT > 0 ? CFG.lightningMax : CFG.ballMaxSpeed;
  if (s2 > cap) setBallSpeed(b, cap);
  else if (s2 < CFG.ballMinSpeed) setBallSpeed(b, CFG.ballMinSpeed);
  b.y = P.y - P.h / 2 - b.r - 1;
  P.squash = 1; P.glow = 1;
  AU.paddleBlip(Math.abs(rel));
  burst(b.x, P.y - 8, '#22e6ff', smash > 200 ? 16 : 7, smash > 200 ? 300 : 170);
  if (smash > 200) {
    addFloat(b.x, P.y - 30, 'SMASH!', '#ffd23d', 16);
    addShake(0.12);
  }
  bumpComboSafe();
}
function bumpComboSafe() { if (G.combo > 0) bumpCombo(); }
/* ================== 11. UPDATE (FIXED TIMESTEP) ========================= */
function update(dt) {
  G.time += dt;
  // Hit-stop: micro freeze on big events (render keeps going).
  if (G.freezeT > 0) { G.freezeT -= dt; return; }
  // Life-loss slow-mo.
  let ts = 1;
  if (G.slowT > 0) { G.slowT -= dt; ts = 0.3; }
  else if (G.eff.slowT > 0) ts = 0.45;
  dt *= ts;

  updatePaddle(dt);
  updateBalls(dt);
  updateBricks(dt);
  updatePowerups(dt);
  updateLasers(dt);
  updateFx(dt);
  updateOverdrive(dt);
  // Timed effects countdown.
  for (const k of ['expandT', 'slowT', 'laserT']) G.eff[k] = Math.max(0, G.eff[k] - dt);
  G.eff.laserCd = Math.max(0, G.eff.laserCd - dt);
  G.balls.forEach(b => {
    b.fireT = Math.max(0, b.fireT - dt);
    b.ghostT = Math.max(0, b.ghostT - dt);
    b.lightT = Math.max(0, b.lightT - dt);
  });
  G.shake = Math.max(0, G.shake - dt * 1.6);
  if (G.hintT > 0) {
    G.hintT -= dt;
    if (G.hintT <= 0) el.hint.classList.add('hidden');
  }
  // Adaptive music intensity follows the multiplier ladder.
  AU.music(dt, G.multIdx, G.od.active);
}

function updatePaddle(dt) {
  const px = P.x, py = P.y;
  // Keyboard velocity.
  let kvx = 0, kvy = 0;
  if (keys.ArrowLeft || keys.KeyA) kvx -= 1;
  if (keys.ArrowRight || keys.KeyD) kvx += 1;
  if (keys.ArrowUp || keys.KeyW) kvy -= 1;
  if (keys.ArrowDown || keys.KeyS) kvy += 1;
  // Thumbstick velocity (floating stick, bottom-left zone).
  let svx = 0, svy = 0;
  if (stick.active) {
    const m = hyp(stick.dx, stick.dy);
    if (m > 0.18) { svx = stick.dx; svy = stick.dy; }
  }
  const hasKey = kvx !== 0 || kvy !== 0;
  const hasStick = svx !== 0 || svy !== 0;
  if (hasKey || hasStick) {
    // Direct velocity control; also drags the pointer target along so the
    // control modes never fight each other.
    const ix = (kvx || 0) + (svx || 0), iy = (kvy || 0) + (svy || 0);
    const im = hyp(ix, iy) || 1;
    P.tx = clamp(P.tx + ix / im * CFG.paddleSpeedX * dt * Math.min(1.2, im),
      20, CFG.W - 20);
    P.ty = clamp(P.ty + iy / im * CFG.paddleSpeedY * dt * Math.min(1.2, im),
      paddleMinY(), paddleMaxY());
  }
  // Critically-damped-ish follow of the target (mouse / drag / keys / stick).
  const fx = 1 - Math.exp(-dt * 18), fy = 1 - Math.exp(-dt * 14);
  P.x = lerp(P.x, P.tx, fx);
  P.y = lerp(P.y, P.ty, fy);
  P.x = clamp(P.x, paddleW() / 2 + 4, CFG.W - paddleW() / 2 - 4);
  P.y = clamp(P.y, paddleMinY(), paddleMaxY());
  // Smoothed velocity (drives English + upward-smash bonus).
  P.vx = lerp(P.vx, (P.x - px) / Math.max(dt, 1e-4), 0.35);
  P.vy = lerp(P.vy, (P.y - py) / Math.max(dt, 1e-4), 0.35);
  P.squash = Math.max(0, P.squash - dt * 5);
  P.glow = Math.max(0, P.glow - dt * 2.5);
  // Stuck balls ride the paddle.
  G.balls.forEach(b => {
    if (b.stuck) { b.x = P.x; b.y = P.y - P.h / 2 - b.r - 2; }
  });
}

function lowestBrickTop() {
  // Top edge of the lowest ALIVE brick row — the overdrive threshold.
  let m = -Infinity;
  for (const br of G.bricks) if (br.y > m) m = br.y;
  return m;
}

function updateBalls(dt) {
  const minY = paddleMinY();
  for (let i = G.balls.length - 1; i >= 0; i--) {
    const b = G.balls[i];
    if (b.stuck) continue;
    // Gradual ramp toward max (lightning ramps toward its own max).
    const cap = b.lightT > 0 ? CFG.lightningMax : CFG.ballMaxSpeed;
    const floor = b.lightT > 0 ? CFG.lightningMin * 0.9 : CFG.ballMinSpeed;
    let sp = ballSpeed(b) + CFG.ballRamp * dt;
    // --- Anti-tunneling: substep so no step exceeds ~60% of ball radius. ---
    const dist = sp * dt;
    const steps = clamp(Math.ceil(dist / (b.r * 0.6)), 1, 10);
    const sdt = dt / steps;
    for (let s = 0; s < steps; s++) {
      b.x += b.vx * sdt; b.y += b.vy * sdt;
      // Walls: left / right / ceiling bounce.
      if (b.x < b.r + 4) { b.x = b.r + 4; b.vx = Math.abs(b.vx); ensureMinVy(b); AU.wallBlip(); }
      else if (b.x > CFG.W - b.r - 4) { b.x = CFG.W - b.r - 4; b.vx = -Math.abs(b.vx); ensureMinVy(b); AU.wallBlip(); }
      if (b.y < b.r + 56) { b.y = b.r + 56; b.vy = Math.abs(b.vy); ensureMinVy(b); AU.wallBlip(); }
      // Paddle collision (only while descending).
      const w = paddleW();
      if (b.vy > 0 &&
          b.y + b.r >= P.y - P.h / 2 - 2 && b.y - b.r <= P.y + P.h / 2 + 2 &&
          Math.abs(b.x - P.x) <= w / 2 + b.r) {
        paddleBounce(b);
      }
      // Ghost: phase + damage along path, no bounce.
      if (b.ghostT > 0) { ghostDamage(b); continue; }
      // Bricks: resolve at most ONE bounce per substep (stable + fast).
      for (let j = G.bricks.length - 1; j >= 0; j--) {
        const br = G.bricks[j];
        const n = circleBrickNormal(b.x, b.y, b.r, br);
        if (!n) continue;
        const verdict = gateCheck(br, b, false);
        if (verdict === 'break') {
          breakBrick(br, b, false);
          // Fire pierces (no bounce); normal balls bounce off the break.
          if (b.fireT <= 0) {
            reflectVel(b, n.nx, n.ny);
            ensureMinVy(b);
            b.x += n.nx * (n.depth + 0.5); b.y += n.ny * (n.depth + 0.5);
          }
          break;
        } else if (verdict === 'phase') {
          ghostDamage(b);
          break;
        } else {
          reflectVel(b, n.nx, n.ny);
          ensureMinVy(b);
          b.x += n.nx * (n.depth + 0.5); b.y += n.ny * (n.depth + 0.5);
          rejectFeedback(br, b);
          break;
        }
      }
    }
    // Enforce speed band after collisions.
    sp = ballSpeed(b);
    if (sp > cap) setBallSpeed(b, cap);
    else if (sp < floor && sp > 0.01) setBallSpeed(b, floor);
    // Trail (for juice render).
    b.trail.push({ x: b.x, y: b.y, age: 0 });
    if (b.trail.length > 22) b.trail.shift();
    b.trail.forEach(t => t.age += dt);
    b.spin += dt * 6;
    // Drain below the screen.
    if (b.y - b.r > CFG.H + 10) {
      G.balls.splice(i, 1);
      burst(clamp(b.x, 0, CFG.W), CFG.H - 8, '#ff4d6d', 14, 220);
      if (G.balls.length === 0) {
        // Lost the last ball: full combo/multiplier reset + life loss.
        resetCombo(true);
        loseLife();
      } else {
        // Lost one of several: still punishing — reset combo/multiplier.
        resetCombo(true);
        addFloat(clamp(b.x, 60, CFG.W - 60), CFG.H - 120, 'BALL LOST', '#ff4d6d', 16);
        AU.loseLife();
      }
    }
  }
  void minY;
}

function updateBricks(dt) {
  for (const br of G.bricks) {
    br.ghostCd = Math.max(0, br.ghostCd - dt);
    if (br.type === 'M') {
      // Shifter: sinusoidal slide around its home column.
      br.x = br.cx + Math.sin(G.time * 1.6 + br.phase) * br.range;
      br.x = clamp(br.x, 8, CFG.W - 8 - br.w);
    }
    if (br.boomT >= 0) {
      // Explosion fuse: chained Volatile detonation (ignores gate rules).
      br.boomT -= dt;
      if (br.boomT < 0) {
        const cx = br.x + br.w / 2, cy = br.y + br.h / 2;
        breakBrick(br, null, true);
        addBoom(cx, cy, 70, 0);
        AU.explode();
      }
    }
  }
  // Shockwave list (visual + kept here for fixed-step consistency).
  for (let i = G.booms.length - 1; i >= 0; i--) {
    const m = G.booms[i];
    if (m.t > 0) { m.t -= dt; continue; }
    m.age += dt * 2.4;
    if (m.age >= 1) G.booms.splice(i, 1);
  }
}

function updatePowerups(dt) {
  for (let i = G.powerups.length - 1; i >= 0; i--) {
    const p = G.powerups[i];
    p.y += p.vy * dt;
    p.phase += dt * 5;
    const w = paddleW();
    // Caught by the paddle?
    if (p.y + p.h / 2 >= P.y - P.h / 2 && p.y - p.h / 2 <= P.y + P.h / 2 &&
        Math.abs(p.x - P.x) <= w / 2 + p.w / 2) {
      applyPower(p.type);
      G.powerups.splice(i, 1);
      continue;
    }
    if (p.y - p.h > CFG.H + 8) G.powerups.splice(i, 1); // missed
  }
}

function updateLasers(dt) {
  for (let i = G.lasers.length - 1; i >= 0; i--) {
    const L = G.lasers[i];
    L.y += L.vy * dt;
    let dead = L.y + L.h < 52;
    if (!dead) {
      // Laser bolts strike from BELOW, fast and steep: they satisfy Angle
      // (>50°) and Volt (fast) conditions, and Gate from-bottom hits.
      for (let j = G.bricks.length - 1; j >= 0; j--) {
        const br = G.bricks[j];
        if (L.x > br.x - 3 && L.x < br.x + br.w + 3 &&
            L.y < br.y + br.h && L.y + L.h > br.y) {
          const fake = { x: L.x, y: L.y + L.h + 4, r: 3, vx: 0, vy: -760, fireT: 0, ghostT: 0, lightT: 0 };
          const v = gateCheck(br, fake, false);
          if (v === 'break' || v === 'phase') {
            breakBrick(br, null, false);
            burst(L.x, br.y + br.h, '#ff3df0', 8, 200);
          } else {
            rejectFeedback(br, fake);
          }
          dead = true;
          break;
        }
      }
    }
    if (dead) G.lasers.splice(i, 1);
  }
}

function updateFx(dt) {
  for (let i = G.parts.length - 1; i >= 0; i--) {
    const p = G.parts[i];
    p.age += dt;
    if (p.age >= p.life) { G.parts.splice(i, 1); continue; }
    p.x += p.vx * dt; p.y += p.vy * dt;
    p.vx *= (1 - dt * 2); p.vy *= (1 - dt * 2);
  }
  for (let i = G.floaters.length - 1; i >= 0; i--) {
    const f = G.floaters[i];
    f.age += dt; f.y -= dt * 46;
    if (f.age >= f.life) G.floaters.splice(i, 1);
  }
}

/* ---- OVERDRIVE: any ball above the lowest alive brick row = top zone. ---
   While active: multiplier doubles every odStepTime (cap ×64), score ticks
   continuously, juice ramps (pulse/glow/particles/riser). Leaving the zone
   banks a bonus. Draining a ball resets combo + multiplier (see updateBalls). */
function updateOverdrive(dt) {
  if (!G.bricks.length || !G.balls.some(b => !b.stuck)) {
    setOD(false);
    return;
  }
  const thresh = lowestBrickTop();
  const anyAbove = G.balls.some(b => !b.stuck && (b.y - b.r) < thresh);
  setOD(anyAbove);
  if (!G.od.active) return;
  G.od.time += dt;
  G.od.stepAcc += dt;
  if (G.od.stepAcc >= CFG.odStepTime) {
    G.od.stepAcc -= CFG.odStepTime;
    if (G.multIdx < MULT_STEPS.length - 1) {
      setMultIdx(G.multIdx + 1, 'OVERDRIVE');
      AU.odStart();
      pulseBanner('OVERDRIVE ×' + G.mult, '×' + G.mult + ' OVERDRIVE');
    }
  }
  // Continuous score tick while riding the top.
  G.od.tickAcc += dt;
  if (G.od.tickAcc >= 0.25) {
    const pts = Math.round(CFG.odTickScore * G.mult * G.od.tickAcc * 4) / 1;
    addScore(pts);
    G.od.banked += pts;
    G.od.tickAcc = 0;
  }
  // Ambient overdrive sparks (rate scales with multiplier + quality).
  if (Math.random() < (0.1 + G.multIdx * 0.12) * (G.quality === 'low' ? 0.3 : 1)) {
    burst(rand(40, CFG.W - 40), rand(70, thresh), pick(['#22e6ff', '#ff3df0', '#ffd23d']), 2, 120);
  }
}
function setOD(on) {
  if (on === G.od.active) return;
  if (on) {
    G.od.active = true; G.od.time = 0; G.od.stepAcc = 0; G.od.tickAcc = 0;
    setMultIdx(Math.max(G.multIdx, 1), 'OVERDRIVE'); // jump straight to ×2
    pulseBanner('OVERDRIVE!', '×' + G.mult + ' OVERDRIVE!');
    showHint('OVERDRIVE! Stay above the bricks — multiplier doubles!', 3);
    AU.odStart(); AU.odLoop(true);
    hitStop(0.05);
    addShake(0.2);
  } else {
    if (G.od.active && G.od.time > 1 && G.state === 'playing') {
      // Leaving the zone banks a bonus proportional to ride time × mult.
      const bonus = Math.round(G.od.time * G.mult * 5);
      if (bonus > 0) {
        addScore(bonus);
        addFloat(CFG.W / 2, CFG.H * 0.42, 'BANKED +' + fmt(bonus), '#ffd23d', 20);
        AU.bank();
      }
    }
    G.od.active = false; G.od.time = 0; G.od.stepAcc = 0;
    AU.odLoop(false);
  }
}
/* ================== 12. RENDER & JUICE ================================== */
function render() {
  // Letterbox the 480x720 field into the canvas + apply screen shake.
  ctx.setTransform(DPR, 0, 0, DPR, 0, 0);
  ctx.fillStyle = '#050816';
  ctx.fillRect(0, 0, cv.width / DPR, cv.height / DPR);
  const cw = cv.width / DPR, ch = cv.height / DPR;
  const s = Math.min(cw / CFG.W, ch / CFG.H);
  const ox = (cw - CFG.W * s) / 2, oy = (ch - CFG.H * s) / 2;
  ctx.translate(ox, oy);
  ctx.scale(s, s);
  // Screen shake (trauma², scaled by event size).
  if (G.shake > 0) {
    const m = G.shake * G.shake * 14;
    ctx.translate(rand(-m, m), rand(-m, m));
  }
  ctx.save();
  ctx.beginPath(); ctx.rect(0, 0, CFG.W, CFG.H); ctx.clip();

  drawBackground();
  drawOverdriveLine();
  drawBricks();
  drawPowerups();
  drawLasers();
  drawPaddle();
  drawBalls();
  drawBooms();
  drawParticles();
  drawFloaters();
  ctx.restore();
}

function drawBackground() {
  // Reactive gradient + perspective grid. Hue energy follows multiplier;
  // overdrive adds a fast pulse.
  const od = G.od.active;
  const e = G.multIdx / 6;
  const pulse = od ? (0.5 + 0.5 * Math.sin(G.time * 9)) : (0.5 + 0.5 * Math.sin(G.time * 1.4));
  const g = ctx.createLinearGradient(0, 0, 0, CFG.H);
  g.addColorStop(0, '#0a1030');
  g.addColorStop(0.55, '#070b1e');
  g.addColorStop(1, od ? '#1c0a24' : '#050816');
  ctx.fillStyle = g;
  ctx.fillRect(-20, -20, CFG.W + 40, CFG.H + 40);
  // Overdrive aura.
  if (od || e > 0) {
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    const rg = ctx.createRadialGradient(CFG.W / 2, CFG.H * 0.3, 20,
      CFG.W / 2, CFG.H * 0.3, 420);
    const a = 0.10 + e * 0.16 + (od ? pulse * 0.14 : 0);
    rg.addColorStop(0, 'rgba(255,61,240,' + a.toFixed(3) + ')');
    rg.addColorStop(0.6, 'rgba(34,230,255,' + (a * 0.5).toFixed(3) + ')');
    rg.addColorStop(1, 'rgba(0,0,0,0)');
    ctx.fillStyle = rg;
    ctx.fillRect(0, 0, CFG.W, CFG.H);
    ctx.restore();
  }
  // Grid.
  ctx.save();
  ctx.strokeStyle = od ? 'rgba(255,61,240,' + (0.16 + pulse * 0.14).toFixed(3) + ')'
    : 'rgba(34,230,255,' + (0.10 + e * 0.08).toFixed(3) + ')';
  ctx.lineWidth = 1;
  const step = 40, off = (G.time * (od ? 60 : 18)) % step;
  ctx.beginPath();
  for (let x = -step; x <= CFG.W + step; x += step) { ctx.moveTo(x, 50); ctx.lineTo(x, CFG.H); }
  for (let y = 50 - step + off; y <= CFG.H; y += step) { ctx.moveTo(0, y); ctx.lineTo(CFG.W, y); }
  ctx.stroke();
  ctx.restore();
  // Ceiling.
  ctx.fillStyle = '#22e6ff';
  ctx.fillRect(0, 52, CFG.W, 3);
}

function drawOverdriveLine() {
  if (!G.bricks.length) return;
  const t = lowestBrickTop();
  ctx.save();
  ctx.setLineDash([8, 8]);
  ctx.lineDashOffset = -G.time * 40;
  ctx.strokeStyle = G.od.active ? 'rgba(255,210,61,.8)' : 'rgba(255,210,61,.28)';
  ctx.lineWidth = G.od.active ? 2.5 : 1.5;
  ctx.beginPath(); ctx.moveTo(6, t); ctx.lineTo(CFG.W - 6, t); ctx.stroke();
  ctx.setLineDash([]);
  ctx.fillStyle = G.od.active ? '#ffd23d' : 'rgba(255,210,61,.5)';
  ctx.font = '700 10px system-ui';
  ctx.fillText(G.od.active ? '★ OVERDRIVE ZONE ★' : '▲ overdrive zone ▲', 12, t - 5);
  ctx.restore();
}

function brickGlowColor(br) {
  // Volt bricks glow harder as your fastest ball nears the speed gate.
  if (br.type !== 'V') return BRICKS[br.type].glow;
  let fastest = 0;
  G.balls.forEach(b => { fastest = Math.max(fastest, ballSpeed(b)); });
  const p = clamp(fastest / CFG.speedGate, 0, 1.2);
  return p >= 1 ? '#ffffff' : BRICKS.V.glow;
}

function drawBricks() {
  const hiQ = G.quality === 'high';
  for (const br of G.bricks) {
    const info = BRICKS[br.type];
    const cx = br.x + br.w / 2, cy = br.y + br.h / 2;
    let pop = 1;
    const age = G.time - br.born;
    if (age < 0.3) pop = 0.5 + 0.5 * sstep(age / 0.3); // spawn pop
    if (br.boomT >= 0) pop = 1 + 0.12 * Math.sin(G.time * 60); // about to blow: jitter
    const w = br.w * pop, h = br.h * pop;
    const x = cx - w / 2, y = cy - h / 2;
    ctx.save();
    if (hiQ) { ctx.shadowColor = brickGlowColor(br); ctx.shadowBlur = 12 + G.multIdx * 2; }
    const grad = ctx.createLinearGradient(x, y, x, y + h);
    grad.addColorStop(0, info.color);
    grad.addColorStop(1, '#0a0f24');
    ctx.fillStyle = grad;
    roundRect(x, y, w, h, 5); ctx.fill();
    ctx.shadowBlur = 0;
    ctx.strokeStyle = info.color; ctx.lineWidth = 1.5;
    roundRect(x, y, w, h, 5); ctx.stroke();
    // Glassy highlight.
    ctx.fillStyle = 'rgba(255,255,255,.25)';
    roundRect(x + 3, y + 2, w - 6, 3, 2); ctx.fill();
    drawBrickIcon(br, cx, cy);
    // Volt readiness shimmer: white-hot edge when breakable.
    if (br.type === 'V') {
      let fastest = 0;
      G.balls.forEach(b => { fastest = Math.max(fastest, ballSpeed(b)); });
      if (fastest >= CFG.speedGate) {
        ctx.strokeStyle = 'rgba(255,255,255,.9)'; ctx.lineWidth = 2;
        roundRect(x - 1, y - 1, w + 2, h + 2, 6); ctx.stroke();
      }
    }
    ctx.restore();
  }
}
// Per-type glyphs so every brick reads at a glance (plus the HTML legend).
function drawBrickIcon(br, cx, cy) {
  ctx.save();
  ctx.strokeStyle = 'rgba(0,0,10,.85)'; ctx.fillStyle = 'rgba(0,0,10,.85)';
  ctx.lineWidth = 2.4; ctx.lineCap = 'round'; ctx.lineJoin = 'round';
  if (br.type === 'A') {
    // Diagonal arrow = "come in steep".
    ctx.beginPath();
    ctx.moveTo(cx + 7, cy - 6); ctx.lineTo(cx - 5, cy + 6);
    ctx.moveTo(cx - 5, cy + 6); ctx.lineTo(cx, cy + 6); ctx.lineTo(cx - 5, cy + 1);
    ctx.stroke();
  } else if (br.type === 'V') {
    // Speed lines.
    ctx.beginPath();
    for (let r = -1; r <= 1; r++) {
      ctx.moveTo(cx - 10, cy + r * 5); ctx.lineTo(cx + 2, cy + r * 5);
    }
    ctx.stroke();
    ctx.beginPath();
    ctx.moveTo(cx + 5, cy - 6); ctx.lineTo(cx + 11, cy); ctx.lineTo(cx + 5, cy + 6);
    ctx.stroke();
  } else if (br.type === 'G') {
    // Chevrons up + down = "vertical only".
    ctx.beginPath();
    ctx.moveTo(cx - 7, cy - 1); ctx.lineTo(cx, cy - 6); ctx.lineTo(cx + 7, cy - 1);
    ctx.moveTo(cx - 7, cy + 1); ctx.lineTo(cx, cy + 6); ctx.lineTo(cx + 7, cy + 1);
    ctx.stroke();
  } else if (br.type === 'M') {
    // Double arrow = "it slides".
    const o = Math.sin(G.time * 4 + br.phase) * 2;
    ctx.beginPath();
    ctx.moveTo(cx - 9 + o, cy); ctx.lineTo(cx + 9 + o, cy);
    ctx.moveTo(cx - 6 + o, cy - 4); ctx.lineTo(cx - 9 + o, cy); ctx.lineTo(cx - 6 + o, cy + 4);
    ctx.moveTo(cx + 6 + o, cy - 4); ctx.lineTo(cx + 9 + o, cy); ctx.lineTo(cx + 6 + o, cy + 4);
    ctx.stroke();
  } else if (br.type === 'X') {
    // Burst/star = explosive.
    ctx.beginPath();
    for (let i = 0; i < 8; i++) {
      const a = i / 8 * TAU + G.time * 0.8;
      ctx.moveTo(cx + Math.cos(a) * 3, cy + Math.sin(a) * 3);
      ctx.lineTo(cx + Math.cos(a) * 8, cy + Math.sin(a) * 8);
    }
    ctx.stroke();
  } else if (br.type === 'B') {
    // Star = bonus drop.
    ctx.beginPath();
    for (let i = 0; i < 10; i++) {
      const a = -Math.PI / 2 + i / 10 * TAU;
      const r = i % 2 === 0 ? 8 : 3.6;
      const px = cx + Math.cos(a) * r, py = cy + Math.sin(a) * r;
      if (i === 0) ctx.moveTo(px, py); else ctx.lineTo(px, py);
    }
    ctx.closePath(); ctx.fill();
  }
  ctx.restore();
}

function drawPaddle() {
  const w = paddleW(), h = P.h;
  const sq = P.squash; // squash & stretch on hits
  const pw = w * (1 + sq * 0.18), ph = h * (1 - sq * 0.35);
  const x = P.x - pw / 2, y = P.y - ph / 2;
  ctx.save();
  const hiQ = G.quality === 'high';
  if (hiQ) { ctx.shadowColor = '#22e6ff'; ctx.shadowBlur = 16 + P.glow * 22; }
  const g = ctx.createLinearGradient(x, y, x, y + ph);
  g.addColorStop(0, '#d8fbff'); g.addColorStop(0.4, '#22e6ff'); g.addColorStop(1, '#0a6d8f');
  ctx.fillStyle = g;
  roundRect(x, y, pw, ph, 7); ctx.fill();
  ctx.shadowBlur = 0;
  // Upward-motion streaks: the faster you rise, the hotter the paddle reads.
  const smash = Math.max(0, -P.vy);
  if (smash > 120) {
    ctx.globalAlpha = clamp(smash / 900, 0, 0.8);
    ctx.fillStyle = '#ffd23d';
    roundRect(x + 4, y - 6 - smash * 0.012, pw - 8, 5, 2); ctx.fill();
    ctx.globalAlpha = 1;
  }
  // Laser cannons while the LASER effect runs.
  if (G.eff.laserT > 0) {
    ctx.fillStyle = '#ff3df0';
    ctx.fillRect(x - 3, y - 10, 6, 12);
    ctx.fillRect(x + pw - 3, y - 10, 6, 12);
  }
  ctx.restore();
}

function ballColor(b) {
  if (b.lightT > 0) return '#fff23d';
  if (b.fireT > 0) return '#ff9a3d';
  if (b.ghostT > 0) return '#c77dff';
  return '#eafcff';
}
function drawBalls() {
  const hiQ = G.quality === 'high';
  for (const b of G.balls) {
    // Trail (additive, length fixed; lightning gets a longer electric tail).
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    const n = b.trail.length;
    for (let i = 0; i < n; i++) {
      const t = b.trail[i];
      const k = i / Math.max(1, n - 1);
      ctx.globalAlpha = 0.05 + k * 0.30;
      ctx.fillStyle = ballColor(b);
      const r = b.r * (0.3 + k * 0.8);
      ctx.beginPath(); ctx.arc(t.x, t.y, r, 0, TAU); ctx.fill();
    }
    ctx.restore();
    // Core + glow.
    ctx.save();
    if (hiQ) { ctx.shadowColor = ballColor(b); ctx.shadowBlur = 18; }
    if (b.ghostT > 0) ctx.globalAlpha = 0.65 + 0.3 * Math.sin(G.time * 14);
    const g = ctx.createRadialGradient(b.x - 2, b.y - 2, 1, b.x, b.y, b.r + 2);
    g.addColorStop(0, '#ffffff'); g.addColorStop(0.55, ballColor(b)); g.addColorStop(1, 'rgba(0,0,20,0)');
    ctx.fillStyle = g;
    ctx.beginPath(); ctx.arc(b.x, b.y, b.r + 1.5, 0, TAU); ctx.fill();
    ctx.shadowBlur = 0;
    // Fire flicker / lightning arcs.
    if (b.fireT > 0) {
      ctx.fillStyle = 'rgba(255,120,20,.5)';
      ctx.beginPath(); ctx.arc(b.x + rand(-3, 3), b.y + rand(-3, 3), b.r * 1.5, 0, TAU); ctx.fill();
    }
    if (b.lightT > 0) {
      ctx.strokeStyle = '#fff23d'; ctx.lineWidth = 1.5;
      ctx.beginPath();
      ctx.moveTo(b.x - 8, b.y + rand(-6, 6));
      ctx.lineTo(b.x - 2, b.y + rand(-6, 6));
      ctx.lineTo(b.x + 4, b.y + rand(-6, 6));
      ctx.lineTo(b.x + 9, b.y + rand(-6, 6));
      ctx.stroke();
    }
    ctx.restore();
  }
}

function drawPowerups() {
  for (const p of G.powerups) {
    const info = POWERS[p.type];
    const bob = Math.sin(p.phase) * 2;
    ctx.save();
    if (G.quality === 'high') { ctx.shadowColor = info.color; ctx.shadowBlur = 12; }
    ctx.fillStyle = '#0a1028';
    roundRect(p.x - p.w / 2, p.y - p.h / 2 + bob, p.w, p.h, 9); ctx.fill();
    ctx.strokeStyle = info.color; ctx.lineWidth = 2;
    roundRect(p.x - p.w / 2, p.y - p.h / 2 + bob, p.w, p.h, 9); ctx.stroke();
    ctx.shadowBlur = 0;
    ctx.fillStyle = info.color;
    ctx.font = '800 11px system-ui'; ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    ctx.fillText(info.name, p.x, p.y + bob + 0.5);
    ctx.restore();
  }
  ctx.textAlign = 'left'; ctx.textBaseline = 'alphabetic';
}

function drawLasers() {
  ctx.save();
  if (G.quality === 'high') { ctx.shadowColor = '#ff3df0'; ctx.shadowBlur = 10; }
  ctx.fillStyle = '#ff9df7';
  for (const L of G.lasers) {
    roundRect(L.x - L.w / 2, L.y, L.w, L.h, 2); ctx.fill();
  }
  ctx.restore();
}

function drawBooms() {
  ctx.save();
  ctx.globalCompositeOperation = 'lighter';
  for (const m of G.booms) {
    if (m.t > 0) continue;
    const k = 1 - m.age;
    ctx.globalAlpha = Math.max(0, k) * 0.9;
    ctx.strokeStyle = '#ff9a3d'; ctx.lineWidth = 4 * k + 1;
    ctx.beginPath(); ctx.arc(m.x, m.y, m.radius * (0.3 + 0.7 * m.age) + 6, 0, TAU); ctx.stroke();
    ctx.strokeStyle = '#ffd23d'; ctx.lineWidth = 2;
    ctx.beginPath(); ctx.arc(m.x, m.y, m.radius * m.age * 0.6, 0, TAU); ctx.stroke();
  }
  ctx.restore();
}

function drawParticles() {
  ctx.save();
  ctx.globalCompositeOperation = 'lighter';
  for (const p of G.parts) {
    const k = 1 - p.age / p.life;
    ctx.globalAlpha = Math.max(0, k);
    ctx.fillStyle = p.color;
    ctx.fillRect(p.x - p.size / 2, p.y - p.size / 2, p.size, p.size);
  }
  ctx.restore();
}

function drawFloaters() {
  ctx.save();
  ctx.textAlign = 'center'; ctx.font = '800 15px system-ui';
  for (const f of G.floaters) {
    const k = 1 - f.age / f.life;
    ctx.globalAlpha = Math.max(0, Math.min(1, k * 1.6));
    ctx.font = '800 ' + f.size + 'px system-ui';
    if (G.quality === 'high') { ctx.shadowColor = f.color; ctx.shadowBlur = 10; }
    ctx.fillStyle = f.color;
    ctx.fillText(f.text, f.x, f.y);
  }
  ctx.restore();
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
/* ================== 13. FLOW, LOOP, SETTINGS, BOOT ====================== */
/* ---- LEVEL SELECT: menu-only row of 5 buttons, gated by localStorage ----
   'neonBreachUnlock' stores the max unlocked level INDEX (default 0).
   Clearing level i unlocks i+1; clearing the last level unlocks everything. */
let maxUnlock = clamp(parseInt(store.get('neonBreachUnlock', '0'), 10) || 0, 0, LEVELS.length - 1);
function unlockNext(clearedIdx) {
  if (clearedIdx >= LEVELS.length - 1) maxUnlock = LEVELS.length - 1;
  else maxUnlock = clamp(Math.max(maxUnlock, clearedIdx + 1), 0, LEVELS.length - 1);
  store.set('neonBreachUnlock', String(maxUnlock));
  refreshLevelButtons();
}
function refreshLevelButtons() {
  const btns = document.querySelectorAll('.lvl-btn');
  btns.forEach(btn => {
    const idx = parseInt(btn.dataset.lvl, 10);
    const locked = idx > maxUnlock;
    btn.disabled = locked;
    btn.classList.toggle('locked', locked);
    btn.setAttribute('aria-disabled', locked ? 'true' : 'false');
    btn.title = locked ? 'Locked — clear level ' + maxUnlock + ' to unlock' : 'Start at level ' + (idx + 1);
    btn.textContent = locked ? (idx + 1) + ' 🔒' : String(idx + 1);
  });
}
function startGameAt(idx) {
  G.score = 0;
  G.levelIdx = clamp(idx === undefined ? 0 : idx, 0, maxUnlock);
  G.lives = 3;
  resetCombo(true);
  showIntro();
}
function startGame(idx) {
  startGameAt(idx === undefined ? 0 : idx);
}
function restartAll() { startGame(); }
function showIntro() {
  const L = LEVELS[G.levelIdx];
  G.state = 'intro';
  G.levelStartScore = G.score;
  resetPaddle();
  G.balls.length = 0; G.powerups.length = 0; G.lasers.length = 0;
  G.parts.length = 0; G.floaters.length = 0;
  G.eff.expandT = G.eff.slowT = G.eff.laserT = 0;
  setOD(false);
  resetCombo(true);
  buildLevel(G.levelIdx);
  spawnStuckBall();
  $('intro-kicker').textContent = L.sub;
  $('intro-title').textContent = L.name;
  $('intro-desc').textContent = L.desc;
  $('intro-new').textContent = L.new;
  showOverlay('intro');
  showHint(L.hint, 8);
  updateHUD();
}
function beginLevel() {
  if (G.state !== 'intro') return;
  G.state = 'playing';
  showOverlay(null);
  updateHUD();
  // Friendly auto-launch so one tap both starts AND serves (mobile-first).
  setTimeout(() => {
    if (G.state === 'playing') {
      const s = G.balls.find(b => b.stuck);
      if (s) launchOrFire();
    }
  }, 500);
}
function levelClear() {
  if (G.state !== 'playing') return;
  // Bank whatever overdrive ride was running, then a clear bonus.
  setOD(false);
  unlockNext(G.levelIdx);
  pulseBanner('BREACH CLEAR!', '+' + fmt(500 * (G.levelIdx + 1)) + ' BONUS');
  const bonus = 500 * (G.levelIdx + 1);
  addScore(bonus);
  AU.fanfare();
  burst(CFG.W / 2, CFG.H / 2, '#ffd23d', 60, 380);
  hitStop(0.12);
  if (G.levelIdx >= LEVELS.length - 1) {
    G.state = 'win';
    $('win-stats').innerHTML = 'Final score <b>' + fmt(G.score) + '</b><br>Best <b>' +
      fmt(highScore) + '</b>' + (G.score >= highScore && G.score > 0 ? ' — NEW BEST! ★' : '');
    showOverlay('win');
  } else {
    G.state = 'clear';
    $('clear-title').textContent = LEVELS[G.levelIdx].name + ' — Breached!';
    $('clear-stats').innerHTML = 'Level bonus <b>+' + fmt(bonus) + '</b><br>Score <b>' +
      fmt(G.score) + '</b> · Lives <b>' + G.lives + '</b>';
    showOverlay('clear');
  }
  updateHUD();
}
function nextLevel() {
  if (G.state !== 'clear') return;
  G.levelIdx++;
  G.lives = Math.min(6, G.lives + 0); // no free life; Bonus bricks cover that
  showIntro();
}
function loseLife() {
  G.lives--;
  AU.loseLife();
  updateHUD();
  if (G.lives <= 0) {
    G.state = 'over';
    setOD(false);
    AU.odLoop(false);
    $('over-stats').innerHTML = 'Score <b>' + fmt(G.score) + '</b><br>Best <b>' +
      fmt(highScore) + '</b><br>Reached <b>' + LEVELS[G.levelIdx].name + '</b>';
    showOverlay('over');
    return;
  }
  // Slow-mo beat so the loss reads, then re-serve a stuck ball.
  G.slowT = 1.1;
  hitStop(0.09);
  addShake(0.45);
  pulseBanner('BALL LOST', G.lives + (G.lives === 1 ? ' LIFE' : ' LIVES') + ' LEFT');
  addFloat(CFG.W / 2, CFG.H - 200, 'BALL LOST — ' + G.lives + ' LEFT', '#ff4d6d', 18);
  spawnStuckBall();
  setTimeout(() => {
    if (G.state === 'playing') {
      const s = G.balls.find(b => b.stuck);
      if (s && G.balls.length === 1) launchOrFire();
    }
  }, 1300);
}
function retryLevel() {
  // Retry the current level; score rolls back to level entry (friendly).
  G.score = G.levelStartScore;
  G.lives = 3;
  resetCombo(true);
  showIntro();
}
function pauseGame() {
  if (G.state !== 'playing') return;
  G.state = 'paused';
  setOD(false);
  AU.odLoop(false);
  showOverlay('pause');
}
function resumeGame() {
  if (G.state !== 'paused') return;
  G.state = 'playing';
  showOverlay(null);
}
function togglePause() {
  if (G.state === 'playing') pauseGame();
  else if (G.state === 'paused') resumeGame();
}
// Pause when the tab hides or the window blurs (never drain in background).
document.addEventListener('visibilitychange', () => { if (document.hidden) pauseGame(); });
window.addEventListener('blur', () => pauseGame());

// --- Main loop: fixed-timestep update + rAF render. ---
let lastT = 0, acc = 0;
function frame(t) {
  requestAnimationFrame(frame);
  if (!lastT) lastT = t;
  let dt = (t - lastT) / 1000;
  lastT = t;
  if (dt > 0.25) dt = 0.25; // tab-switch spike guard
  if (G.state === 'playing') {
    acc += dt;
    let n = 0;
    while (acc >= CFG.fixedDt && n < 12) {
      update(CFG.fixedDt);
      acc -= CFG.fixedDt; n++;
    }
    if (n >= 12) acc = 0;
  } else {
    // Menus still animate juice (particles/floaters) for a lively backdrop.
    G.time += dt * 0.5;
    updateFx(Math.min(dt, 0.05));
  }
  render();
  updateHUD();
}

// --- Settings + buttons wiring ---
function applyQuality(q) {
  if (q === 'auto') {
    q = (isTouchDevice() && Math.min(screen.width, screen.height) < 500) ? 'medium' : 'high';
  }
  G.quality = q;
}
function boot() {
  buildLegends();
  refreshHigh();
  if (isTouchDevice()) document.body.classList.add('touch');
  // Restore settings.
  const q = store.get('neonBreachQuality', 'auto');
  $('sel-quality').value = ['auto', 'high', 'medium', 'low'].includes(q) ? q : 'auto';
  applyQuality($('sel-quality').value);
  G.shakeOn = store.get('neonBreachShake', 'on') !== 'off';
  $('chk-shake').checked = G.shakeOn;
  $('chk-sound').checked = !AU.muted;
  $('rng-vol').value = String(Math.round(AU.volume * 100));
  syncMuteUI();

  $('sel-quality').addEventListener('change', e => {
    store.set('neonBreachQuality', e.target.value);
    applyQuality(e.target.value);
  });
  $('chk-shake').addEventListener('change', e => {
    G.shakeOn = e.target.checked;
    store.set('neonBreachShake', G.shakeOn ? 'on' : 'off');
  });
  $('chk-sound').addEventListener('change', e => {
    AU.init();
    AU.setMuted(!e.target.checked);
    syncMuteUI();
  });
  $('rng-vol').addEventListener('input', e => {
    AU.init();
    AU.setVolume(e.target.value / 100);
  });
  $('btn-start').addEventListener('click', () => { AU.init(); startGame(0); });
  document.querySelectorAll('.lvl-btn').forEach(btn => {
    btn.addEventListener('click', () => {
      if (btn.disabled) return;
      AU.init();
      startGameAt(parseInt(btn.dataset.lvl, 10));
    });
  });
  refreshLevelButtons();
  $('btn-how').addEventListener('click', () => {
    AU.init();
    $('howto').classList.toggle('hidden');
    $('btn-how').textContent = $('howto').classList.contains('hidden') ? 'How to Play' : 'Hide Help';
  });
  $('btn-legend-close').addEventListener('click', () => showOverlay('menu'));
  $('btn-resume').addEventListener('click', resumeGame);
  $('btn-pause-restart').addEventListener('click', () => { retryLevel(); });
  $('btn-pause-menu').addEventListener('click', () => { G.state = 'menu'; showOverlay('menu'); refreshHigh(); });
  $('btn-mute-pause').addEventListener('click', toggleMute);
  $('btn-next').addEventListener('click', () => { AU.init(); nextLevel(); });
  $('btn-retry').addEventListener('click', () => { AU.init(); retryLevel(); });
  $('btn-over-menu').addEventListener('click', () => { G.state = 'menu'; showOverlay('menu'); refreshHigh(); });
  $('btn-win-again').addEventListener('click', () => { AU.init(); restartAll(); });
  $('btn-win-menu').addEventListener('click', () => { G.state = 'menu'; showOverlay('menu'); refreshHigh(); });
  // Clicking the dimmed intro backdrop also begins (big tap target).
  $('ov-intro').addEventListener('click', () => { AU.init(); beginLevel(); });

  resetPaddle();
  buildLevel(0);
  G.state = 'menu';
  showOverlay('menu');
  resize();
  setTimeout(resize, 60);
  requestAnimationFrame(frame);
}
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', boot);
} else {
  boot();
}
