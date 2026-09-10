/* =====================================================================
   NEON BREAKER — a dependency-free canvas breakout
   Sections:
     1. Config & constants
     2. Utility / math helpers
     3. SFX (WebAudio synth, no assets)
     4. FX (particles, shake, flash, trails) — baseline juice
     5. Input (keyboard, mouse, touch, virtual thumbstick)
     6. Bricks (per-type registry) & Levels
     7. Entities: Ball, Paddle, PowerUp, Laser, Particles (pooled)
     8. Game state machine & screens
     9. Physics (fixed timestep + substepping)
     10. Rendering
     11. Main loop & boot
   ===================================================================== */
'use strict';

/* =====================================================================
   1. CONFIG & CONSTANTS
   ===================================================================== */
const DEBUG_DEFAULT = false;
const DEBUG = { enabled: DEBUG_DEFAULT };

let W = 960;               // logical world width (adapts to viewport aspect)
let H = 720;               // logical world height
const FIXED_DT = 1 / 240;  // physics step (s) — fine enough to prevent tunneling
const MAX_FRAME_DT = 1 / 20; // cap real dt per frame
const MAX_SUBSTEPS = 40;

const BALL_MIN_SPEED = 260;
const BALL_MAX_SPEED = 760;
const BALL_BASE_SPEED = 340;

const PADDLE_W = 130;
const PADDLE_H = 18;
let PADDLE_Y_BASE = H - 70;         // bottom of the vertical band (let: re-derived on resize)
let PADDLE_BAND_TOP = H - 190;      // top of the vertical band
const PADDLE_SPEED = 620;
const PADDLE_STICK_VEL = 0.14;        // thumbstick direct velocity factor

const TOP_HUD = 46;          // HUD height
const FIELD_TOP = TOP_HUD + 56;      // top of brick field
const FIELD_SIDE = 12;

const ARMOR_SPEED_THRESHOLD = 430;   // ball must exceed this to break armored
const ANGLE_WINDOW_DEG = 25;         // angle-brick impact window (+/-)
const PHASE_CYCLE = 2.4;             // s per phase cycle
const GENERATOR_PERIOD = 9;          // s between spawns
const GENERATOR_MAX = 4;             // max bricks spawned per generator
const EXPLOSION_RADIUS = 74;

const POWERUP_TYPES = {
  multi:  { color: '#ffd166', letter: 'M', name: 'MULTIBALL' },
  fire:   { color: '#ff5c49', letter: 'F', name: 'FIREBALL' },
  heavy:  { color: '#b388ff', letter: 'H', name: 'HEAVY' },
  sticky: { color: '#4dd0e1', letter: 'S', name: 'STICKY' },
  wide:   { color: '#69f0ae', letter: 'W', name: 'WIDE' },
  slow:   { color: '#40c4ff', letter: 'T', name: 'SLOW-MO' },
  life:   { color: '#ff6ec7', letter: '+', name: 'EXTRA LIFE' },
  laser:  { color: '#ffee58', letter: 'L', name: 'LASER' },
};
const TIMED_POWER = { fire: 12, heavy: 10, sticky: 14, wide: 12, slow: 8, laser: 10 };
const POWERUP_DROP_CHANCE = 0.22;

const HIGHSCORE_KEY = 'neon-breaker-highscore';

/* ---- ESCALATION TIERS (heat-driven) ---- */
const TIERS = [
  { name: 'COOL',      color: '#4dd0e1' },
  { name: 'WARM',      color: '#69f0ae' },
  { name: 'HOT',       color: '#ffd166' },
  { name: 'BLAZING',   color: '#ff9e3d' },
  { name: 'SUPERNOVA', color: '#ff5c49' },
];
const LOOP_SPEED_BONUS = 0.07;   // +7% ball base speed per loop past level 6
const LOOP_DROP_BONUS  = 0.10;   // +10% power-up drop odds per loop
const LOOP_CAP = 4;              // max effective loops for scaling

/* =====================================================================
   1b. HEAT / TIER SYSTEM — global escalation read by every FX system
   ===================================================================== */
const TierHeat = (() => {
  const SMOOTH = 1.6;           // heat rise/fall smoothing (per second)
  let heat = 0;                 // 0..1 smoothed intensity
  let tierIdx = 0;
  let momentum = 0;             // slow session momentum: sustained play lowers the floor

  function targetHeat(g) {
    // combo chain is the dominant driver; multiplier & onTop add on top
    const comboH = clamp(g.combo / 15, 0, 0.5);              // live combo chain
    const multH = clamp((g.multiplier - 1) / 9, 0, 0.3);     // earned multiplier
    const onTopH = g.onTop ? 0.22 : 0;                       // farming the sky
    const powerH = Math.min(0.2, Object.keys(g.activeTimers || {}).length * 0.07);
    return clamp(Math.max(comboH, multH) + onTopH + powerH, 0, 1);
  }

  return {
    get heat() { return heat; },
    get tier() { return tierIdx; },
    get tierInfo() { return TIERS[tierIdx]; },
    // call on every brick destroyed: builds the session momentum floor
    bump() { momentum = Math.min(0.3, momentum + 0.012); },
    update(dt, g) {
      const target = (g.state === STATE.PLAYING || g.state === STATE.LEVEL_CLEAR) ? targetHeat(g) : 0;
      // momentum decays very slowly, so sustained success keeps the field alive
      momentum = Math.max(0, momentum - dt * 0.018);
      const targetWithFloor = clamp(Math.max(target, momentum), 0, 1);
      // heat falls slower than it rises, so tiers feel earned but not whiplashy
      const rate = targetWithFloor > heat ? SMOOTH * 2.0 : SMOOTH * 0.9;
      heat += clamp(targetWithFloor - heat, -rate * dt, rate * dt);
      const t = clamp(Math.floor(heat * TIERS.length), 0, TIERS.length - 1);
      if (t !== tierIdx) {
        const rising = t > tierIdx;
        tierIdx = t;
        if (rising) { SFX.tierUp(t); FX.tierUp(t); }
      }
    },
    reset() { heat = 0; tierIdx = 0; momentum = 0; },
  };
})();
window.TierHeat = TierHeat;

/* =====================================================================
   2. UTILITY
   ===================================================================== */
const clamp = (v, a, b) => v < a ? a : v > b ? b : v;
const lerp = (a, b, t) => a + (b - a) * t;
const rand = (a, b) => a + Math.random() * (b - a);
const randInt = (a, b) => Math.floor(rand(a, b + 1));
const TAU = Math.PI * 2;

function hexToRgb(hex) {
  const n = parseInt(hex.slice(1), 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}
function rgba(hex, a) {
  const [r, g, b] = hexToRgb(hex);
  return `rgba(${r},${g},${b},${a})`;
}

/* =====================================================================
   3. SFX — WebAudio synth baseline. Global hook: SFX.*
   ===================================================================== */
const SFX = (() => {
  let ctx = null;
  let master = null;
  let muted = false;
  let comboPitchStep = 0;           // semitone-ish step, resets on paddle touch
  // layered pad/drone for high heat (item 2)
  let padNodes = null;              // { osc1, osc2, lfo, lfoGain, filter, gain }
  let padTarget = 0;

  function ensure() {
    if (!ctx) {
      try {
        ctx = new (window.AudioContext || window.webkitAudioContext)();
        master = ctx.createGain();
        master.gain.value = 0.22;
        master.connect(ctx.destination);
      } catch (e) { /* audio unavailable */ }
    }
    if (ctx && ctx.state === 'suspended') ctx.resume();
    return ctx;
  }

  function blip(freq, dur, type = 'square', vol = 1, slideTo = 0, bright = 0) {
    if (muted) return;
    const c = ensure();
    if (!c) return;
    const o = c.createOscillator();
    const g = c.createGain();
    // brightness: a gentle lowpass that opens with heat (0..1)
    if (bright > 0.01) {
      const f = c.createBiquadFilter();
      f.type = 'lowpass';
      f.frequency.setValueAtTime(600 + bright * 5200, c.currentTime);
      f.Q.value = 0.6;
      o.connect(f); f.connect(g);
    } else {
      o.connect(g);
    }
    o.type = type;
    o.frequency.setValueAtTime(freq, c.currentTime);
    if (slideTo) o.frequency.exponentialRampToValueAtTime(Math.max(30, slideTo), c.currentTime + dur);
    g.gain.setValueAtTime(vol, c.currentTime);
    g.gain.exponentialRampToValueAtTime(0.001, c.currentTime + dur);
    g.connect(master);
    o.start(); o.stop(c.currentTime + dur + 0.02);
  }

  function noiseBurst(dur, vol = 0.6, bright = 0) {
    if (muted) return;
    const c = ensure();
    if (!c) return;
    const len = Math.floor(c.sampleRate * dur);
    const buf = c.createBuffer(1, len, c.sampleRate);
    const data = buf.getChannelData(0);
    for (let i = 0; i < len; i++) data[i] = (Math.random() * 2 - 1) * (1 - i / len);
    const src = c.createBufferSource();
    const g = c.createGain();
    if (bright > 0.01) {
      const f = c.createBiquadFilter();
      f.type = 'highpass';
      f.frequency.setValueAtTime(200 + bright * 2400, c.currentTime);
      src.connect(f); f.connect(g);
    } else {
      src.connect(g);
    }
    g.gain.value = vol;
    g.connect(master);
    src.buffer = buf;
    src.start();
  }

  const OCTAVE = [1, 9 / 8, 5 / 4, 4 / 3, 3 / 2, 5 / 3, 15 / 8, 2]; // major-ish ladder
  function comboPitch(baseFreq) {
    const step = ((comboPitchStep % OCTAVE.length) + OCTAVE.length) % OCTAVE.length;
    const oct = Math.floor(comboPitchStep / OCTAVE.length);
    const mult = OCTAVE[step] * Math.pow(2, oct);
    return baseFreq * mult;
  }

  // ---- heat drone: two detuned saws -> lowpass -> slow LFO on filter ----
  function ensurePad() {
    const c = ensure();
    if (!c || padNodes) return padNodes;
    try {
      const gain = c.createGain();
      gain.gain.value = 0;
      const filter = c.createBiquadFilter();
      filter.type = 'lowpass';
      filter.frequency.value = 500;
      filter.Q.value = 1.2;
      const o1 = c.createOscillator(); o1.type = 'sawtooth'; o1.frequency.value = 55;  // A1
      const o2 = c.createOscillator(); o2.type = 'sawtooth'; o2.frequency.value = 82.5; // E2 detuned fifth
      const o3 = c.createOscillator(); o3.type = 'sine'; o3.frequency.value = 110;
      const lfo = c.createOscillator(); lfo.frequency.value = 0.13;
      const lfoGain = c.createGain(); lfoGain.gain.value = 240;
      lfo.connect(lfoGain); lfoGain.connect(filter.frequency);
      o1.connect(filter); o2.connect(filter); o3.connect(filter);
      filter.connect(gain); gain.connect(master);
      o1.start(); o2.start(); o3.start(); lfo.start();
      padNodes = { o1, o2, o3, lfo, filter, gain };
    } catch (e) { padNodes = null; }
    return padNodes;
  }

  function setPad(level) { // level 0..1 from heat
    padTarget = level;
    const c = ensure();
    if (!c) return;
    const pad = padTarget > 0.01 ? ensurePad() : padNodes;
    if (!pad) return;
    const now = c.currentTime;
    pad.gain.gain.cancelScheduledValues(now);
    pad.gain.gain.setTargetAtTime(padTarget * 0.05, now, 0.6); // tasteful: ≤0.05 gain
    pad.filter.frequency.setTargetAtTime(300 + padTarget * 1400, now, 0.8);
  }

  return {
    ensure, // lazy resume on first gesture
    get muted() { return muted; },
    toggleMute() {
      muted = !muted;
      if (muted && padNodes) padNodes.gain.gain.setTargetAtTime(0, ctx ? ctx.currentTime : 0, 0.1);
      return muted;
    },
    // heat 0..1 drives the drone; called every frame (no-op until audio is unlocked by a gesture)
    heat(h) { if (!muted && ctx) setPad(h > 0.5 ? (h - 0.5) / 0.5 : 0); },
    resetComboPitch() { comboPitchStep = 0; },
    brick(type = 'standard') {
      const bright = clamp(TierHeat.heat, 0, 1);
      switch (type) {
        case 'armored': blip(160, 0.12, 'sawtooth', 0.8, 90, bright); noiseBurst(0.08, 0.25, bright); break; // clank
        case 'angle': blip(comboPitch(500), 0.07, 'triangle', 0.6, 0, bright); break;
        case 'phase': blip(comboPitch(700), 0.06, 'sine', 0.5, comboPitch(1200), bright); break;
        case 'explosive': blip(90, 0.35, 'sawtooth', 1, 40, bright); noiseBurst(0.3, 0.6, bright); break;
        case 'indestructible': blip(120, 0.06, 'square', 0.4); break;
        default: blip(comboPitch(320), 0.07, 'square', 0.7, comboPitch(480), bright);
      }
      comboPitchStep++; // classic rising-pitch ladder; reset on paddle touch
    },
    paddle(energy = 0) { blip(180 + energy * 260, 0.08, 'triangle', 0.6 + energy * 0.5); comboPitchStep = 0; },
    bounce() { blip(240, 0.05, 'sine', 0.4); },
    powerup() { blip(520, 0.1, 'square', 0.5); setTimeout(() => blip(780, 0.14, 'square', 0.5), 90); },
    laser() { blip(900, 0.09, 'sawtooth', 0.5, 300); },
    ballLost() { blip(300, 0.4, 'sawtooth', 0.7, 60); comboPitchStep = 0; },
    levelClear() {
      [440, 554, 659, 880].forEach((f, i) => setTimeout(() => blip(f, 0.16, 'square', 0.5), i * 110));
    },
    // distinctive rising arpeggio for the breakthrough moment
    breakthrough() {
      [330, 415, 494, 660, 830, 988, 1320].forEach((f, i) => setTimeout(() => blip(f, 0.22, 'sine', 0.55), i * 62));
      setTimeout(() => blip(165, 0.7, 'sawtooth', 0.35, 660), 0);
      noiseBurst(0.4, 0.3);
    },
    comboTick(n) { blip(600 + Math.min(n, 30) * 40, 0.05, 'square', 0.35); },
    explosion() { blip(70, 0.4, 'sawtooth', 0.9, 35); noiseBurst(0.35, 0.7); },
    tierUp(tierIdx) {
      // little escalation sting per tier
      const base = 380 + tierIdx * 90;
      [0, 4, 7].forEach((semi, i) => setTimeout(() => blip(base * Math.pow(2, semi / 12), 0.14, 'sine', 0.32), i * 55));
    },
  };
})();
window.SFX = SFX;

/* =====================================================================
   4. FX — particles / shake / flash baseline. Global hook: FX.*
   ===================================================================== */
const FX = (() => {
  // Particle budget scales with device so mid phones stay at 60fps (item 7)
  function computeBudget() {
    const area = (window.innerWidth || 960) * (window.innerHeight || 720);
    const dpr = Math.min(window.devicePixelRatio || 1, 2);
    if (dpr >= 2 && area > 900000) return 420;      // big hi-dpi phone
    if (area > 700000) return 900;                  // desktop
    return 560;                                     // small screens
  }
  let MAX_PARTICLES = computeBudget();

  const pool = [];
  const active = [];
  let shakeMag = 0, shakeDecay = 26;
  let flashColor = '#ffffff';
  let flashAlpha = 0;
  // shockwave rings: pooled
  const rings = [];
  const RING_MAX = 24;
  for (let i = 0; i < RING_MAX; i++) rings.push({ on: false, x: 0, y: 0, r: 0, vr: 0, life: 0, maxLife: 1, color: '#fff', width: 2 });
  // hit-stop freeze (item 5)
  let hitStop = 0;
  // slow-mo tint state
  let slowTint = 0;
  // hue shift for breakthrough
  let hueShift = 0;
  // breakthrough banner (letter stagger)
  const banner = { on: false, t: 0, text: 'BREAKTHROUGH!', color: '#ffd166' };
  // vignette strength (tier-scaled)
  let vignette = 0;
  // directional kick on paddle hits
  let kick = { x: 0, y: 0, vx: 0, vy: 0 };

  function spawn(x, y, vx, vy, life, color, size) {
    let p;
    if (pool.length) p = pool.pop();
    else if (active.length < MAX_PARTICLES) p = {};
    else return; // pool exhausted, drop it
    p.x = x; p.y = y; p.vx = vx; p.vy = vy;
    p.life = life; p.maxLife = life; p.color = color; p.size = size;
    p.grav = 260; p.drag = 0.99;
    active.push(p);
    return p;
  }
  const spawnRet = spawn;

  function ring(x, y, color, opts) {
    const o = opts || {};
    for (let i = 0; i < RING_MAX; i++) {
      const r = rings[i];
      if (!r.on) { r.on = true; r.x = x; r.y = y; r.r = o.r0 || 4; r.vr = o.vr || 260; r.life = o.life || 0.45; r.maxLife = r.life; r.color = color; r.width = o.width || 2; return; }
    }
  }

  // ember spark (rises from bottom of screen at BLAZING+)
  function ember(tierColor) {
    const p = spawnRet(rand(0, W), H + 6, rand(-18, 18), rand(-160, -60), rand(1.2, 2.4), tierColor, rand(2, 4));
    if (p) p.grav = -30; // embers keep rising
  }

  return {
    update(dt) {
      const heat = TierHeat.heat;
      for (let i = active.length - 1; i >= 0; i--) {
        const p = active[i];
        p.life -= dt;
        if (p.life <= 0) { active.splice(i, 1); if (pool.length < MAX_PARTICLES) pool.push(p); continue; }
        p.x += p.vx * dt; p.y += p.vy * dt;
        p.vy += p.grav * dt;
        p.vx *= p.drag;
      }
      for (let i = 0; i < RING_MAX; i++) {
        const r = rings[i];
        if (!r.on) continue;
        r.life -= dt; r.r += r.vr * dt;
        if (r.life <= 0) r.on = false;
      }
      shakeMag = Math.max(0, shakeMag - dt * shakeDecay);
      kick.x += kick.vx * dt; kick.y += kick.vy * dt;
      kick.vx *= Math.pow(0.001, dt); kick.vy *= Math.pow(0.001, dt); // spring back
      flashAlpha = Math.max(0, flashAlpha - dt * 2.6);
      if (hitStop > 0) hitStop -= dt;
      slowTint = clamp(slowTint + ((Game.activeTimers && Game.activeTimers.slow) ? dt * 3 : -dt * 3), 0, 1);
      if (hueShift > 0) hueShift = Math.max(0, hueShift - dt * 0.55);
      if (banner.on) { banner.t += dt; if (banner.t > 2.2) banner.on = false; }
      vignette = lerp(vignette, 0.10 + heat * 0.30, dt * 3);
      // ambient embers at BLAZING+
      if (TierHeat.tier >= 3 && Game.state === 'playing' && active.length < MAX_PARTICLES - 40) {
        if (Math.random() < dt * (14 + 10 * (TierHeat.tier - 3))) ember(TIERS[TierHeat.tier].color);
      }
    },
    draw(ctx) {
      for (let i = 0; i < active.length; i++) {
        const p = active[i];
        const t = p.life / p.maxLife;
        ctx.globalAlpha = t;
        ctx.fillStyle = p.color;
        ctx.fillRect(p.x - p.size / 2, p.y - p.size / 2, p.size, p.size);
      }
      ctx.globalAlpha = 1;
      // rings (layered strokes instead of shadowBlur — cheap glow, item 7)
      for (let i = 0; i < RING_MAX; i++) {
        const r = rings[i];
        if (!r.on) continue;
        const t = r.life / r.maxLife;
        ctx.globalAlpha = t * 0.9;
        ctx.strokeStyle = r.color;
        ctx.lineWidth = r.width;
        ctx.beginPath(); ctx.arc(r.x, r.y, r.r, 0, TAU); ctx.stroke();
        ctx.globalAlpha = t * 0.35;
        ctx.lineWidth = r.width * 2.6;
        ctx.beginPath(); ctx.arc(r.x, r.y, r.r, 0, TAU); ctx.stroke();
      }
      ctx.globalAlpha = 1;
    },
    burst(x, y, color, n = 14, speed = 240) {
      n = Math.min(n, MAX_PARTICLES - active.length);
      for (let i = 0; i < n; i++) {
        const a = rand(0, TAU), s = rand(speed * 0.3, speed);
        spawn(x, y, Math.cos(a) * s, Math.sin(a) * s, rand(0.3, 0.8), color, rand(2, 5));
      }
    },
    ring(x, y, color, opts) {
      const o = opts || {};
      ring(x, y, color, o.r0 || 4, o.vr || 260, o.life || 0.45, o.width || 2.5);
    },
    // shard burst for brick breaks — count & speed scale with tier
    brickBreak(brick, x, y, color) {
      const tier = TierHeat.tier;
      const n = Math.min(14 + tier * 6, MAX_PARTICLES - active.length);
      this.burst(x, y, color || '#ffffff', n, 280 + tier * 70);
      if (tier >= 2) {
        ring(x, y, color || '#ffffff', { r0: 4, vr: 300 + tier * 90, life: 0.35, width: 2 });
        FX.flash(color || '#ffffff', 0.03 + tier * 0.015); // chromatic-ish split flash
      }
      if (tier >= 3) FX.shake(1.5 + tier * 0.7); else FX.shake(2);
      // chromatic split: two offset ghost bursts
      if (tier >= 3) {
        this.burst(x - 4, y, '#ff3355', Math.min(5, MAX_PARTICLES - active.length), 200);
        this.burst(x + 4, y, '#40c4ff', Math.min(5, MAX_PARTICLES - active.length), 200);
      }
    },
    brickBounce(x, y, color) { this.burst(x, y, color || '#ffffff', 5, 120); },
    // metal sparks for armored clank
    armoredClank(x, y) {
      this.burst(x, y, '#cfd8ff', 10, 360);
      this.burst(x, y, '#7e9bff', 6, 220);
      ring(x, y, '#7e9bff', { r0: 3, vr: 220, life: 0.25, width: 2 });
      FX.hitStop(0.055); // 2-4 frame freeze feel at 60fps
      FX.shake(3);
    },
    paddleHit(x, y, energy) {
      this.burst(x, y, energy > 0.5 ? '#ffd166' : '#4dd0e1', 4 + Math.floor(energy * 8), 140 + energy * 200);
      // directional kick proportional to impact energy (item 1)
      const e = clamp(energy, 0, 1.2);
      kick.vx -= (x - W / 2) * e * 0.35;
      kick.vy = Math.max(kick.vy, -e * 90);
    },
    powerupCatch(type, x, y) {
      const info = POWERUP_TYPES[type] || { color: '#fff' };
      const px = x == null ? W / 2 : x, py = y == null ? H - 100 : y;
      this.burst(px, py, info.color, 22, 320);
      ring(px, py, info.color, { r0: 8, vr: 420, life: 0.5, width: 3 });
      FX.flash(info.color, 0.12);
    },
    explosion(x, y) {
      this.burst(x, y, '#ff9e3d', 40, 460);
      this.burst(x, y, '#ffee58', 20, 300);
      ring(x, y, '#ff9e3d', { r0: 10, vr: 700, life: 0.55, width: 5 });
      FX.shake(9);
      FX.flash('#ff9e3d', 0.2);
    },
    ballLost(x, y) {
      // implosion at the loss point + red flash
      const px = x == null ? W / 2 : x, py = y == null ? H - 40 : y;
      for (let i = 0; i < 20; i++) {
        const a = rand(0, TAU), s = rand(120, 300);
        spawn(px + Math.cos(a) * 20, py + Math.sin(a) * 20, -Math.cos(a) * s, -Math.sin(a) * s, 0.5, '#ff3355', rand(2, 4));
      }
      ring(px, py, '#ff3355', { r0: 60, vr: -90, life: 0.5, width: 3 }); // collapsing ring
      this.burst(px, py, '#ff3355', 18, 320);
      FX.flash('#ff3355', 0.22);
      FX.shake(5);
    },
    // level-clear sweep wave + fireworks (item 5)
    levelClear() {
      ring(W / 2, H / 2, '#ffd166', { r0: 10, vr: Math.max(W, H) * 1.4, life: 0.9, width: 7 });
      const colors = ['#ffd166', '#4dd0e1', '#ff6ec7'];
      for (let i = 0; i < 7; i++) setTimeout(() => { if (active.length < MAX_PARTICLES - 30) this.burst(rand(100, W - 100), rand(120, 420), colors[i % 3], 24, 380); }, i * 130);
    },
    breakthrough() {
      FX.flash('#ffd166', 0.3); FX.shake(6);
      ring(W / 2, FIELD_TOP - 8, '#ffd166', { r0: 10, vr: 900, life: 0.8, width: 6 });
      ring(W / 2, FIELD_TOP - 8, '#ffffff', { r0: 10, vr: 600, life: 0.6, width: 3 });
      for (let i = 0; i < 40; i++) this.burst(rand(0, W), FIELD_TOP - 8, '#ffd166', 2, 300);
      hueShift = 1;              // whole-field hue shift ~2s (decays 0.55/s)
      banner.on = true; banner.t = 0;
      banner.text = 'BREAKTHROUGH!'; banner.color = '#ffd166';
    },
    combo(n) { if (n % 5 === 0) FX.flash('#ffd166', 0.08); },
    tierUp(tierIdx) {
      const info = TIERS[tierIdx];
      ring(W / 2, H / 2, info.color, { r0: 20, vr: 800, life: 0.7, width: 4 });
      FX.flash(info.color, 0.1);
      FX.shake(2 + tierIdx);
    },
    hitStop(sec) { hitStop = Math.max(hitStop, sec); },
    get hitStopTime() { return hitStop; },
    showBanner(text, color) { banner.on = true; banner.t = 0; banner.text = text; banner.color = color || '#ffd166'; },
    get bannerState() { return banner; },
    get slowTintAmount() { return slowTint; },
    get hueShiftAmount() { return hueShift; },
    get vignetteAmount() { return vignette; },
    get kickOffset() { return kick; },
    shake(mag) { shakeMag = Math.min(18, shakeMag + mag); },
    setShakeDecay(v) { shakeDecay = v; },
    flash(color, alpha) { flashColor = color || '#ffffff'; flashAlpha = Math.max(flashAlpha, alpha == null ? 0.3 : alpha); },
    get shakeOffset() {
      if (shakeMag <= 0) return { x: 0, y: 0 };
      return { x: rand(-shakeMag, shakeMag), y: rand(-shakeMag, shakeMag) };
    },
    get flashState() { return { color: flashColor, alpha: flashAlpha }; },
    reset() {
      for (const p of active) pool.push(p);
      active.length = 0;
      for (let i = 0; i < RING_MAX; i++) rings[i].on = false;
      shakeMag = 0; flashAlpha = 0; hitStop = 0; hueShift = 0; slowTint = 0;
      banner.on = false; banner.t = 0;
      kick.x = 0; kick.y = 0; kick.vx = 0; kick.vy = 0;
    },
    resize() { MAX_PARTICLES = computeBudget(); },
    get budget() { return MAX_PARTICLES; },
  };
})();
window.FX = FX;

/* =====================================================================
   5. INPUT — keyboard, mouse, touch + virtual thumbstick
   ===================================================================== */
const Input = {
  keys: {},               // held keys
  mouseX: W / 2, mouseY: H / 2,
  usingMouse: false,
  // thumbstick state (touch lower-left quadrant)
  stick: { active: false, id: -1, baseX: 0, baseY: 0, knobX: 0, knobY: 0, dx: 0, dy: 0 },
  directDrag: { active: false, id: -1, x: 0 },
  stickRadius: 64,
  actionQueued: false,    // tap right / click / space => launch or fire

  init(canvas) {
    window.addEventListener('keydown', (e) => {
      this.keys[e.code] = true;
      SFX.ensure();
      if (e.code === 'Space') { this.actionQueued = true; e.preventDefault(); }
      if (e.code === 'KeyP') Game.togglePause();
      if (e.code === 'Enter') { if (Game.state === 'title' || Game.state === 'gameover') this.actionQueued = true; }
      if (e.code === 'KeyM') Game.toggleMute();
      if (e.code === 'F3') { DEBUG.enabled = !DEBUG.enabled; e.preventDefault(); }
    });
    window.addEventListener('keyup', (e) => { this.keys[e.code] = false; });
    window.addEventListener('blur', () => { this.keys = {}; if (Game.state === 'playing') Game.autoPause(); });
    document.addEventListener('visibilitychange', () => { if (document.hidden && Game.state === 'playing') Game.autoPause(); });

    canvas.addEventListener('mousemove', (e) => {
      this.usingMouse = true;
      const p = toWorld(e.clientX, e.clientY);
      this.mouseX = p.x; this.mouseY = p.y;
    });
    canvas.addEventListener('mousedown', (e) => {
      SFX.ensure();
      this.usingMouse = true;
      const p = toWorld(e.clientX, e.clientY);
      this.mouseX = p.x; this.mouseY = p.y;
      this.actionQueued = true;
      e.preventDefault();
    });

    // ---- touch ----
    canvas.addEventListener('touchstart', (e) => {
      SFX.ensure();
      e.preventDefault();
      for (const t of e.changedTouches) {
        const p = toWorld(t.clientX, t.clientY);
        const inStickZone = p.x < W * 0.42 && p.y > H * 0.55;
        if (!this.stick.active && inStickZone) {
          this.stick.active = true; this.stick.id = t.identifier;
          Game.thumbstickUsed = true; // hide idle hint after first use (item 6b)
          this.stick.baseX = p.x; this.stick.baseY = p.y;
          this.stick.knobX = p.x; this.stick.knobY = p.y;
          this.stick.dx = 0; this.stick.dy = 0;
        } else if (!this.directDrag.active) {
          this.directDrag.active = true; this.directDrag.id = t.identifier;
          this.directDrag.x = p.x;
          // a quick tap outside stick zone is an action
          this.actionQueued = true;
        } else {
          this.actionQueued = true; // third+ finger = action
        }
      }
    }, { passive: false });
    canvas.addEventListener('touchmove', (e) => {
      e.preventDefault();
      for (const t of e.changedTouches) {
        const p = toWorld(t.clientX, t.clientY);
        if (this.stick.active && t.identifier === this.stick.id) {
          let dx = p.x - this.stick.baseX, dy = p.y - this.stick.baseY;
          const len = Math.hypot(dx, dy);
          if (len > this.stickRadius) { dx *= this.stickRadius / len; dy *= this.stickRadius / len; }
          this.stick.knobX = this.stick.baseX + dx;
          this.stick.knobY = this.stick.baseY + dy;
          this.stick.dx = dx / this.stickRadius; this.stick.dy = dy / this.stickRadius;
          this.usingMouse = false;
        } else if (this.directDrag.active && t.identifier === this.directDrag.id) {
          this.directDrag.x = p.x;
          this.mouseX = p.x; this.mouseY = p.y;
        }
      }
    }, { passive: false });
    const touchEnd = (e) => {
      e.preventDefault();
      for (const t of e.changedTouches) {
        if (this.stick.active && t.identifier === this.stick.id) {
          this.stick.active = false; this.stick.id = -1;
          this.stick.dx = 0; this.stick.dy = 0; // spring back
        } else if (this.directDrag.active && t.identifier === this.directDrag.id) {
          this.directDrag.active = false; this.directDrag.id = -1;
        }
      }
    };
    canvas.addEventListener('touchend', touchEnd, { passive: false });
    canvas.addEventListener('touchcancel', touchEnd, { passive: false });
  },

  // Called by game each frame: returns desired paddle velocity {x, y}
  paddleIntent(paddle) {
    let vx = 0, vy = 0;
    const k = this.keys;
    if (k['ArrowLeft'] || k['KeyA']) vx -= 1;
    if (k['ArrowRight'] || k['KeyD']) vx += 1;
    if (k['ArrowUp'] || k['KeyW']) vy -= 1;
    if (k['ArrowDown'] || k['KeyS']) vy += 1;

    if (this.stick.active) {
      vx = this.stick.dx; vy = this.stick.dy;
      const mag = Math.hypot(vx, vy);
      if (mag > 1) { vx /= mag; vy /= mag; }
    } else if (this.usingMouse && !vx && !vy) {
      // pointer targeting: move toward pointer
      const dx = this.mouseX - paddle.x, dy = clamp(this.mouseY, PADDLE_BAND_TOP, PADDLE_Y_BASE) - paddle.y;
      if (Math.abs(dx) > 4) vx = clamp(dx / 40, -1, 1);
      if (Math.abs(dy) > 4) vy = clamp(dy / 30, -1, 1);
    }
    return { vx, vy };
  },
};

/* =====================================================================
   6. BRICKS & LEVELS
   Char legend for LEVELS grids:
     .  empty
     s  standard brick   (1 hit, white-cyan)
     a  ARMORED brick    (needs ball speed > threshold, steel blue)
     g  ANGLE brick      (needs near-normal impact, amber dial)
     p  PHASE brick      (blinks solid/intangible on a cycle, violet)
     x  EXPLOSIVE brick  (destroys neighbors in radius, orange)
     m  MOVING brick     (slides horizontally, green)
     i  INDESTRUCTIBLE   (never breaks — architecture / roofs, gray)
     G  GENERATOR popper (spawns standard bricks nearby, magenta)
     c  CARRIER brick    (standard + guaranteed power-up drop, gold)
   ===================================================================== */
const BRICK_TYPES = {
  standard:       { color: '#4dd0e1', score: 50 },
  armored:        { color: '#7e9bff', score: 120 },
  angle:          { color: '#ffb74d', score: 120 },
  phase:          { color: '#b388ff', score: 100 },
  explosive:      { color: '#ff6d3a', score: 150 },
  moving:         { color: '#69f0ae', score: 100 },
  indestructible: { color: '#8a8f98', score: 0 },
  generator:      { color: '#ff6ec7', score: 200 },
  carrier:        { color: '#ffd166', score: 80 },
};

class Brick {
  constructor(type, col, row, cols) {
    this.type = type;
    this.col = col; this.row = row;
    this.cols = cols;   // total columns in level grid (for responsive width)
    this.w = 74; this.h = 26;
    // scale brick width down on narrow viewports so the grid + side margins
    // always fits inside the playfield (keeps FIELD_SIDE margin at all widths)
    const gridW = W - FIELD_SIDE * 2 - (this.cols - 1) * 3.2;
    this.w = Math.max(20, Math.min(74, gridW / this.cols));
    this.x = FIELD_SIDE + col * (this.w + 3.2) + (74 - this.w) / 2;
    this.y = FIELD_TOP + row * (this.h + 5) + 2.5;
    this.alive = true;
    this.solid = true;            // phase bricks toggle this
    this.phaseT = rand(0, PHASE_CYCLE);
    this.moveDir = rand() < 0.5 ? -1 : 1;
    this.moveRange = 130;
    this.moveOrigin = this.x;
    this.spawnedCount = 0;        // generator budget
    this.genT = rand(2, GENERATOR_PERIOD);
    this.knock = 0;               // knockback visual on bounce
    this.spawnT = -1;             // spawn-in animation: -1 = done, >=0 = animating
    this.spawnDelay = 0;          // staggered per brick
  }
  get cx() { return this.x + this.w / 2; }
  get cy() { return this.y + this.h / 2; }

  update(dt, game) {
    if (this.spawnT >= 0) {
      this.spawnDelay -= dt;
      if (this.spawnDelay <= 0) this.spawnT += dt;
      if (this.spawnT > 0.45) this.spawnT = -1; // done
    }
    if (this.type === 'phase') {
      this.phaseT += dt;
      const c = this.phaseT % PHASE_CYCLE;
      this.solid = c < PHASE_CYCLE * 0.6;
    } else if (this.type === 'moving') {
      this.x += this.moveDir * 60 * dt;
      if (this.x > this.moveOrigin + this.moveRange) { this.x = this.moveOrigin + this.moveRange; this.moveDir = -1; }
      if (this.x < this.moveOrigin - this.moveRange) { this.x = this.moveOrigin - this.moveRange; this.moveDir = 1; }
    } else if (this.type === 'generator') {
      this.genT -= dt;
      if (this.genT <= 0 && this.spawnedCount < GENERATOR_MAX) {
        this.genT = GENERATOR_PERIOD;
        game.tryGeneratorSpawn(this);
      }
    }
    if (this.knock > 0) this.knock = Math.max(0, this.knock - dt * 8);
  }

  // Called when the ball hits this brick. Returns true if destroyed.
  // `info` = { speed, impactDot } where impactDot = |cos| of angle between ball
  // velocity and the face normal (1 = dead-on, 0 = glancing).
  hit(ball, info, game) {
    switch (this.type) {
      case 'indestructible':
        SFX.brick('indestructible');
        FX.brickBounce(this.cx, this.cy, BRICK_TYPES.indestructible.color);
        this.knock = 1;
        return false;
      case 'armored':
        if (info.speed >= ARMOR_SPEED_THRESHOLD) { game.destroyBrick(this, ball); return true; }
        SFX.brick('armored'); // clank
        FX.armoredClank(this.cx, this.cy); // metal sparks + hit-stop (items 1, 5)
        this.knock = 1;
        return false;
      case 'angle':
        if (info.impactDot >= Math.cos(ANGLE_WINDOW_DEG * Math.PI / 180)) { game.destroyBrick(this, ball); return true; }
        SFX.brick('angle');
        FX.brickBounce(this.cx, this.cy, BRICK_TYPES.angle.color);
        this.knock = 1;
        return false;
      case 'phase':
        if (!this.solid) return false; // intangible, handled by collision skip
        game.destroyBrick(this, ball); return true;
      default:
        game.destroyBrick(this, ball);
        return true;
    }
  }
}

/* Hand-designed levels. Each: { name, subtitle, rows: [...] } */
const LEVELS = [
  { name: 'FIRST CONTACT', subtitle: 'Break the line', rows: [
    '.ssssssssss.',
    '.ssssssssss.',
    '.ssxssssxss.',
    '.ssssssssss.',
  ]},
  { name: 'THE ROOF', subtitle: 'Find the gap — then farm the sky', rows: [
    'iiiiiiii.iii',
    'iipppppppppi',
    'iissmmmmssii',
    '..ssssssss..',
    '....ssss....',
  ]},
  { name: 'ANGLES & ARMOR', subtitle: 'Hit straight, hit fast', rows: [
    'agagagagagag',
    'sasasasasasa',
    '.g.g.g.g.g.g',
    '..x......x..',
  ]},
  { name: 'PATROL', subtitle: 'The field moves', rows: [
    '.m...m...m..',
    'sassssssssas',
    '..m...m...m.',
    'ppp....pppp.',
    '..ss.ss.ss..',
  ]},
  { name: 'POPULATORS', subtitle: 'They keep coming', rows: [
    '.G..s..s..G.',
    '.ss.s..s.ss.',
    '.Gg..gg..gG.',
    '..ss.ss.ss..',
  ]},
  { name: 'THE GAUNTLET', subtitle: 'Everything at once', rows: [
    'iiiiiiiii.ii',
    'aapappapappi',
    'mggxxggxxggm',
    '.s.s.ss.s.s.',
    'G..m....m..G',
  ]},
];

/* =====================================================================
   7. ENTITIES
   ===================================================================== */
class Ball {
  constructor() {
    this.active = false;
    this.x = 0; this.y = 0;
    this.vx = 0; this.vy = 0;
    this.r = 8;
    this.stuck = false;       // sticky paddle
    this.stickOffset = 0;
    this.trail = [];          // ring buffer of recent positions
    this.fire = false; this.heavy = false;
    this.prevX = 0; this.prevY = 0;
    this.impact = 0;               // squash amount from recent impact
    this.splitT = 0;               // multiball cell-division peel animation
  }
  get speed() { return Math.hypot(this.vx, this.vy); }
  launch(power = 0) {
    if (!this.stuck) return;
    this.stuck = false;
    const a = rand(-0.35, 0.35) - Math.PI / 2;
    // loop difficulty scaling: +7% base speed per loop past level 6, capped (item 6a)
    const loopMult = 1 + Math.min(Game.loopLevel || 0, LOOP_CAP) * LOOP_SPEED_BONUS;
    const s = clamp((BALL_BASE_SPEED + power * 220) * loopMult, BALL_MIN_SPEED, BALL_MAX_SPEED);
    this.vx = Math.cos(a) * s; this.vy = Math.sin(a) * s;
  }
  clampSpeed() {
    const s = this.speed;
    if (s === 0) return;
    const clamped = clamp(s, BALL_MIN_SPEED, BALL_MAX_SPEED);
    const f = clamped / s;
    this.vx *= f; this.vy *= f;
  }
}

class Paddle {
  constructor() {
    this.baseW = PADDLE_W;
    this.w = PADDLE_W; this.h = PADDLE_H;
    this.x = W / 2; this.y = PADDLE_Y_BASE;
    this.vx = 0; this.vy = 0; // actual velocity (world units/s)
    this.sticky = false;
    this.squash = 0;          // squash-and-stretch amount (item 5)
    this.squashVX = 1;        // direction: 1 = horizontal squash
  }
  update(dt, intent) {
    const prevX = this.x, prevY = this.y;
    const px = intent.vx * PADDLE_SPEED;
    const py = intent.vy * PADDLE_SPEED * 0.7;
    this.x = clamp(this.x + px * dt, this.w / 2 + FIELD_SIDE, W - this.w / 2 - FIELD_SIDE);
    this.y = clamp(this.y + py * dt, PADDLE_BAND_TOP, PADDLE_Y_BASE);
    this.vx = (this.x - prevX) / dt;
    this.vy = (this.y - prevY) / dt;
  }
}

class PowerUp {
  constructor(x, y, type) {
    this.x = x; this.y = y; this.type = type;
    this.vy = 110; this.w = 40; this.h = 22; this.alive = true;
    this.t = rand(0, TAU);
  }
  update(dt) { this.y += this.vy * dt; this.t += dt * 4; }
}

class Laser {
  constructor(x, y, dir) { this.x = x; this.y = y; this.vy = dir; this.alive = true; }
  update(dt) { this.y += this.vy * 900 * dt; if (this.y < TOP_HUD - 10 || this.y > H + 10) this.alive = false; }
}

/* =====================================================================
   8. GAME — state machine, level loading, scoring, physics helpers
   ===================================================================== */
const STATE = { TITLE: 'title', LEVEL_INTRO: 'levelintro', READY: 'ready', PLAYING: 'playing', PAUSED: 'paused', LEVEL_CLEAR: 'levelclear', GAME_OVER: 'gameover' };

const Game = {
  state: STATE.TITLE,
  levelIndex: 0,
  loopLevel: 0,           // how many times past the last level
  score: 0, lives: 3, combo: 0, comboBest: 0,
  multiplier: 1,
  onTop: false,           // ball above brick field bonus state
  brokeThrough: false,    // per-level first breakthrough
  highScore: 0,
  bricks: [],
  balls: [],              // active balls (pooled)
  ballPool: [],
  powerups: [],
  lasers: [],
  lasersCd: 0,
  paddle: new Paddle(),
  level: null,
  time: 0,
  introT: 0, clearT: 0, titleT: 0, pausedByUser: false,
  activeTimers: {},       // timed power-ups: type -> seconds remaining
  shakeT: 0,
  flash: { color: '#fff', a: 0 },
  muted: false,
  destroyedCount: 0,
  comboTicks: 0,          // sparkle counter for onTop farming (item 3)
  slowmoT: 0,             // slow-mo time-dip state for breakthrough
  slowmoF: 1,             // current time factor (1 = normal)
  newBest: false,         // game-over "NEW BEST" flag
  thumbstickUsed: false,  // hide idle hint after first use (item 6b)
  hudChipAnim: null,      // power-up catch chip animation state

  // convenience accessors for the escalation system (item 1)
  get heat() { return TierHeat.heat; },
  get tier() { return TierHeat.tier; },

  init() {
    try { this.highScore = parseInt(localStorage.getItem(HIGHSCORE_KEY) || '0', 10) || 0; } catch (e) { this.highScore = 0; }
    this.spawnBall();
  },

  /* ---- ball pool ---- */
  spawnBall() {
    let b = this.ballPool.pop();
    if (!b) b = new Ball();
    b.active = true; b.stuck = true; b.stickOffset = 0;
    b.fire = false; b.heavy = false; b.trail.length = 0;
    b.impact = 0; b.splitT = 0;
    b.vx = 0; b.vy = 0;
    b.x = this.paddle.x; b.y = this.paddle.y - this.paddle.h / 2 - b.r - 1;
    this.balls.push(b);
    return b;
  },
  recycleBall(b) {
    b.active = false;
    const i = this.balls.indexOf(b);
    if (i >= 0) this.balls.splice(i, 1);
    this.ballPool.push(b);
  },

  /* ---- state transitions ---- */
  startGame() {
    this.levelIndex = 0; this.loopLevel = 0;
    this.score = 0; this.lives = 3; this.combo = 0; this.multiplier = 1;
    this.destroyedCount = 0; this.comboBest = 0; this.newBest = false;
    TierHeat.reset();
    this.loadLevel(0);
  },
  loadLevel(idx) {
    this.levelIndex = idx;
    this.loopLevel = Math.floor(idx / LEVELS.length);
    this.level = LEVELS[idx % LEVELS.length];
    this.bricks = [];
    const rows = this.level.rows;
    for (let r = 0; r < rows.length; r++) {
      const line = rows[r];
      for (let c = 0; c < line.length; c++) {
        const ch = line[c];
        if (ch === '.' || ch === ' ') continue;
        const type = ({ s: 'standard', a: 'armored', g: 'angle', p: 'phase', x: 'explosive', m: 'moving', i: 'indestructible', G: 'generator', c: 'carrier' })[ch] || 'standard';
        const brick = new Brick(type, c, r, line.length);
        // staggered spawn-in, row by row (item 5)
        brick.spawnT = 0;
        brick.spawnDelay = r * 0.09 + c * 0.012;
        this.bricks.push(brick);
      }
    }
    this.brokeThrough = false;
    this.onTop = false;
    this.combo = 0; this.multiplier = 1;
    this.powerups.length = 0; this.lasers.length = 0;
    this.activeTimers = {};
    this.paddle.w = this.paddle.baseW; this.paddle.sticky = false;
    FX.reset();
    // recycle all balls into one fresh stuck ball
    while (this.balls.length) this.recycleBall(this.balls[0]);
    this.spawnBall();
    this.state = STATE.LEVEL_INTRO;
    this.introT = 0;
  },
  beginReady() {
    this.state = STATE.READY;
  },
  levelCleared() {
    this.state = STATE.LEVEL_CLEAR;
    this.clearT = 0;
    SFX.levelClear(); FX.levelClear();
    this.score += 500 * (this.loopLevel + 1); // loop bonus
    if (this.score > this.highScore) { this.highScore = this.score; try { localStorage.setItem(HIGHSCORE_KEY, String(this.highScore)); } catch (e) {} }
  },
  gameOver() {
    this.state = STATE.GAME_OVER;
    this.newBest = this.score > this.highScore;
    if (this.newBest) { this.highScore = this.score; try { localStorage.setItem(HIGHSCORE_KEY, String(this.highScore)); } catch (e) {} }
  },
  togglePause() {
    if (this.state === STATE.PLAYING) { this.state = STATE.PAUSED; this.pausedByUser = true; }
    else if (this.state === STATE.PAUSED) { this.state = STATE.PLAYING; this.pausedByUser = false; }
  },
  autoPause() {
    if (this.state === STATE.PLAYING) { this.state = STATE.PAUSED; this.pausedByUser = false; }
  },
  toggleMute() { this.muted = SFX.toggleMute(); },

  /* ---- brick destruction / explosion / generator ---- */
  destroyBrick(brick, ball) {
    if (!brick.alive) return;
    brick.alive = false;
    const info = BRICK_TYPES[brick.type];
    this.combo++;
    this.comboBest = Math.max(this.comboBest, this.combo);
    TierHeat.bump(); // session momentum toward higher tiers (item 1)
    // combo multiplier ramps much faster while ball is above the field
    const ramp = this.onTop ? 3 : 1;
    this.multiplier = 1 + Math.floor(this.combo / 6) * ramp;
    const base = info.score || 0;
    this.score += Math.round(base * this.multiplier);
    this.destroyedCount++;
    SFX.brick(brick.type);
    FX.brickBreak(brick, brick.cx, brick.cy, info.color);
    FX.combo(this.combo);
    if (this.combo > 4 && this.combo % 4 === 0) SFX.comboTick(this.combo);
    // heavy-ball breaks: brief hit-stop for weight (item 5)
    if (ball && ball.heavy) FX.hitStop(0.05);

    if (brick.type === 'explosive') {
      SFX.explosion();
      FX.explosion(brick.cx, brick.cy);
      for (const other of this.bricks) {
        if (other.alive && other !== brick && other.type !== 'indestructible') {
          const d = Math.hypot(other.cx - brick.cx, other.cy - brick.cy);
          if (d < EXPLOSION_RADIUS) {
            if (other.type === 'explosive') { this.destroyBrick(other, ball); } // chain!
            else this.destroyBrickQuiet(other);
          }
        }
      }
    }
    // power-up drops — odds scale slightly with loop count (item 6a), capped
    const dropChance = POWERUP_DROP_CHANCE * (1 + Math.min(this.loopLevel, LOOP_CAP) * LOOP_DROP_BONUS);
    const drop = brick.type === 'carrier' || Math.random() < dropChance;
    if (drop) this.spawnPowerUp(brick.cx, brick.cy);
  },
  destroyBrickQuiet(brick) {
    if (!brick.alive) return;
    brick.alive = false;
    this.combo++;
    this.multiplier = 1 + Math.floor(this.combo / 6) * (this.onTop ? 3 : 1);
    this.score += Math.round((BRICK_TYPES[brick.type].score || 0) * this.multiplier);
    this.destroyedCount++;
    SFX.brick(brick.type);
    FX.brickBreak(brick, brick.cx, brick.cy, BRICK_TYPES[brick.type].color);
    // power-up drops — keep loop scaling consistent here too
    const dropChance = POWERUP_DROP_CHANCE * (1 + Math.min(this.loopLevel, LOOP_CAP) * LOOP_DROP_BONUS);
    if (brick.type === 'carrier' || Math.random() < dropChance) this.spawnPowerUp(brick.cx, brick.cy);
  },
  tryGeneratorSpawn(genBrick) {
    // find a nearby empty cell (row above/below, col +/-)
    const dw = 77.2, dh = 31;
    const options = [[0, -1], [0, 1], [-1, 0], [1, 0]];
    for (const [dc, dr] of options.sort(() => Math.random() - 0.5)) {
      const col = genBrick.col + dc, row = genBrick.row + dr;
      const occ = this.bricks.some(b => b.alive && b.col === col && b.row === row);
      if (!occ && row >= 0) {
        const nb = new Brick('standard', col, row, genBrick.cols);
        nb.spawnedFrom = genBrick;
        this.bricks.push(nb);
        genBrick.spawnedCount++;
        FX.burst(nb.cx, nb.cy, '#ff6ec7', 10, 180);
        SFX.bounce();
        return;
      }
    }
  },

  /* ---- power-ups ---- */
  spawnPowerUp(x, y) {
    const keys = Object.keys(POWERUP_TYPES);
    // extra life is rare
    const pool = keys.filter(k => k !== 'life').concat(keys.filter(k => k !== 'life'), ['life']);
    const type = pool[randInt(0, pool.length - 1)];
    this.powerups.push(new PowerUp(x, y, type));
  },
  catchPowerUp(pu) {
    const type = pu.type;
    SFX.powerup();
    FX.powerupCatch(type, pu.x, pu.y);
    this.hudChipAnim = { type, t: 0 }; // HUD chip pop animation (item 4)
    switch (type) {
      case 'multi': {
        const src = this.balls[0];
        if (src) {
          for (let i = 0; i < 2; i++) {
            const nb = this.spawnBall();
            nb.stuck = false;
            nb.x = src.x; nb.y = src.y;
            const a = Math.atan2(src.vy || -1, src.vx || 0) + (i === 0 ? 0.5 : -0.5);
            const s = clamp(src.speed || BALL_BASE_SPEED, BALL_MIN_SPEED, BALL_MAX_SPEED);
            nb.vx = Math.cos(a) * s; nb.vy = Math.sin(a) * s;
            // cell-division look: new balls peel apart (item 4)
            nb.splitT = 0.35;
            nb.splitDX = (i === 0 ? 1 : -1);
          }
          src.splitT = 0.35; src.splitDX = 0;
          FX.ring(src.x, src.y, '#ffd166', { r0: 4, vr: 340, life: 0.4, width: 3 }); // division ripple
        }
        break;
      }
      case 'life': this.lives++; break;
      case 'wide': this.paddle.w = this.paddle.baseW * 1.6; break;
      case 'sticky': this.paddle.sticky = true; break;
      default: break; // timed types handled below
    }
    if (TIMED_POWER[type]) this.activeTimers[type] = TIMED_POWER[type];
    // apply fire/heavy/slow to all current balls
    this.syncBallMods();
  },
  syncBallMods() {
    for (const b of this.balls) {
      b.fire = !!this.activeTimers.fire;
      b.heavy = !!this.activeTimers.heavy;
    }
  },

  /* ---- ball lost / lives ---- */
  ballLost(ball) {
    FX.ballLost(ball.x, Math.min(ball.y, H - 10)); SFX.ballLost();
    this.recycleBall(ball);
    this.combo = 0; this.multiplier = 1; this.onTop = false;
    SFX.resetComboPitch();
    if (this.balls.length === 0) {
      this.lives--;
      if (this.lives <= 0) { this.gameOver(); return; }
      this.paddle.w = this.paddle.baseW; this.paddle.sticky = false;
      this.activeTimers = {};
      this.state = STATE.READY;
      this.spawnBall();
    }
  },

  /* ---- called when ball first rises above top brick row ---- */
  checkBreakthrough(ball) {
    if (this.brokeThrough || !this.bricks.length) return;
    let topY = Infinity;
    for (const b of this.bricks) if (b.alive) topY = Math.min(topY, b.y);
    if (ball.y + ball.r < topY) {
      this.brokeThrough = true;
      this.onTop = true;
      SFX.breakthrough();
      FX.breakthrough();
      this.score += 500;
      this.slowmoT = 0.6;    // time dip: 0.35x for ~0.6s, smooth ramp back (item 3)
      this.slowmoF = 0.35;
    }
  },
};

/* =====================================================================
   9. PHYSICS — fixed timestep, substepping, no tunneling
   ===================================================================== */
const Physics = {
  lastSubsteps: 0,

  step(dt) {
    const g = Game;
    // paddle
    const intent = Input.paddleIntent(g.paddle);
    g.paddle.update(dt, intent);
    // paddle squash decay (item 5)
    if (g.paddle.squash > 0) g.paddle.squash = Math.max(0, g.paddle.squash - dt * 6);
    // ball split/impact decay
    for (const b of g.balls) {
      if (b.impact > 0) b.impact = Math.max(0, b.impact - dt * 8);
      if (b.splitT > 0) b.splitT = Math.max(0, b.splitT - dt);
    }

    // balls
    for (let i = g.balls.length - 1; i >= 0; i--) {
      const ball = g.balls[i];
      if (!ball.active) continue;
      if (ball.stuck) {
        ball.x = g.paddle.x + ball.stickOffset;
        ball.y = g.paddle.y - g.paddle.h / 2 - ball.r - 1;
        continue;
      }
      // slow-mo power-up
      const slowF = g.activeTimers.slow ? 0.62 : 1;
      // substep so movement per step < ball radius
      const spd = ball.speed * slowF;
      const steps = clamp(Math.ceil((spd * dt) / (ball.r * 0.8)), 1, MAX_SUBSTEPS);
      const sdt = dt / steps;
      this.lastSubsteps = steps;
      for (let s = 0; s < steps; s++) {
        ball.x += ball.vx * sdt * slowF;
        ball.y += ball.vy * sdt * slowF;
        this.collideWalls(ball, g);
        this.collidePaddle(ball, g);
        this.collideBricks(ball, g);
        if (!ball.active) break; // lost
      }
      ball.clampSpeed();
      // trail: length/width scale with tier and power state (item 1)
      ball.trail.push({ x: ball.x, y: ball.y });
      const tier = TierHeat.tier;
      const trailMax = 10 + tier * 3 + ((ball.fire || ball.heavy) ? 6 : 0);
      while (ball.trail.length > trailMax) ball.trail.shift();
    }

    // power-ups
    for (let i = g.powerups.length - 1; i >= 0; i--) {
      const pu = g.powerups[i];
      pu.update(dt);
      // catch by paddle
      if (pu.y + pu.h / 2 > g.paddle.y - g.paddle.h / 2 &&
          pu.y - pu.h / 2 < g.paddle.y + g.paddle.h / 2 &&
          Math.abs(pu.x - g.paddle.x) < g.paddle.w / 2 + pu.w / 2) {
        g.catchPowerUp(pu);
        g.powerups.splice(i, 1);
        continue;
      }
      if (pu.y > H + 30) g.powerups.splice(i, 1);
    }

    // lasers
    for (let i = g.lasers.length - 1; i >= 0; i--) {
      const l = g.lasers[i];
      l.update(dt);
      let hit = false;
      for (const b of g.bricks) {
        if (b.alive && b.solid && l.x > b.x && l.x < b.x + b.w && l.y > b.y && l.y < b.y + b.h) {
          if (b.type === 'indestructible') { SFX.brick('indestructible'); }
          else if (b.type === 'armored' || b.type === 'angle') {
            // lasers treat armor/angle as breakable (they're energy shots)
            g.destroyBrickQuiet(b);
          } else g.destroyBrickQuiet(b);
          hit = true;
          break;
        }
      }
      if (hit || !l.alive) g.lasers.splice(i, 1);
    }

    // brick updates (moving / phase / generator)
    for (const b of g.bricks) if (b.alive) b.update(dt, g);

    // timed power-up timers
    for (const k of Object.keys(g.activeTimers)) {
      g.activeTimers[k] -= dt;
      if (g.activeTimers[k] <= 0) {
        delete g.activeTimers[k];
        g.syncBallMods();
        if (k === 'wide') g.paddle.w = g.paddle.baseW;
        if (k === 'sticky') g.paddle.sticky = false;
      }
    }
    // laser fire cooldown + auto fire while laser active
    g.lasersCd -= dt;
    if (g.activeTimers.laser && g.lasersCd <= 0) {
      g.lasersCd = 0.32;
      g.lasers.push(new Laser(g.paddle.x - g.paddle.w / 3, g.paddle.y - g.paddle.h, -1));
      g.lasers.push(new Laser(g.paddle.x + g.paddle.w / 3, g.paddle.y - g.paddle.h, -1));
      SFX.laser();
    }

    // level clear?
    const breakables = g.bricks.some(b => b.alive && b.type !== 'indestructible' && b.type !== 'generator');
    if (!breakables && g.state === STATE.PLAYING) g.levelCleared();
  },

  collideWalls(ball, g) {
    const floorY = H + 60; // generous: ball is lost below screen
    if (ball.x - ball.r < FIELD_SIDE) { ball.x = FIELD_SIDE + ball.r; ball.vx = Math.abs(ball.vx); SFX.bounce(); FX.brickBounce(ball.x, ball.y, '#4dd0e1'); }
    if (ball.x + ball.r > W - FIELD_SIDE) { ball.x = W - FIELD_SIDE - ball.r; ball.vx = -Math.abs(ball.vx); SFX.bounce(); FX.brickBounce(ball.x, ball.y, '#4dd0e1'); }
    // top wall (or roof)
    if (ball.y - ball.r < TOP_HUD + 4) { ball.y = TOP_HUD + 4 + ball.r; ball.vy = Math.abs(ball.vy); SFX.bounce(); }
    if (ball.y - ball.r > floorY) g.ballLost(ball);
    // track onTop state
    if (g.bricks.length) {
      let topY = Infinity;
      for (const b of g.bricks) if (b.alive) topY = Math.min(topY, b.y);
      if (ball.vy < 0 && ball.y + ball.r < topY) { g.onTop = true; g.checkBreakthrough(ball); }
      else if (ball.vy > 0 && ball.y - ball.r > topY + 40) g.onTop = false;
    }
  },

  collidePaddle(ball, g) {
    const p = g.paddle;
    if (ball.vy <= 0) return;
    if (ball.y + ball.r < p.y - p.h / 2 || ball.y - ball.r > p.y + p.h / 2) return;
    if (ball.x < p.x - p.w / 2 - ball.r || ball.x > p.x + p.w / 2 + ball.r) return;

    // hit! compute energy from paddle vertical motion
    const energy = clamp(-p.vy / 300, -0.5, 1);   // moving up = positive energy
    const hitX = clamp((ball.x - p.x) / (p.w / 2), -1, 1);
    const speed = ball.speed;
    let outAngle = -Math.PI / 2 + hitX * 1.05;    // base fan
    outAngle += clamp(p.vx / 900, -0.45, 0.45);   // paddle x velocity steers
    let newSpeed = speed * (1 + energy * 0.35);   // up = energy, down = dampen
    if (energy < 0) newSpeed = speed * (1 + energy * 0.2);
    newSpeed = clamp(newSpeed, BALL_MIN_SPEED * 0.9, BALL_MAX_SPEED);
    ball.vx = Math.cos(outAngle) * newSpeed;
    ball.vy = Math.sin(outAngle) * newSpeed;
    ball.y = p.y - p.h / 2 - ball.r - 0.5;

    if (p.sticky) { ball.stuck = true; ball.stickOffset = ball.x - p.x; }

    SFX.paddle(Math.max(0, energy));
    FX.paddleHit(ball.x, p.y - p.h / 2, Math.max(0, energy));
    // paddle squash-and-stretch + ball impact deform (item 5)
    p.squash = clamp(0.3 + Math.abs(energy) * 0.5, 0, 0.8);
    p.squashVX = 1; // compressed vertically (stretched horizontally)
    ball.impact = 1;
    // touching paddle resets the combo chain (and the pitch ladder)
    g.combo = 0; g.multiplier = 1;
    g.onTop = false;
    SFX.resetComboPitch();
  },

  collideBricks(ball, g) {
    for (const b of g.bricks) {
      if (!b.alive) continue;
      if (!b.solid && b.type === 'phase') continue; // intangible while phased out
      if (ball.x + ball.r < b.x || ball.x - ball.r > b.x + b.w ||
          ball.y + ball.r < b.y || ball.y - ball.r > b.y + b.h) continue;

      // resolve collision: find smallest penetration axis
      const overlapL = ball.x + ball.r - b.x;
      const overlapR = b.x + b.w - (ball.x - ball.r);
      const overlapT = ball.y + ball.r - b.y;
      const overlapB = b.y + b.h - (ball.y - ball.r);
      const minX = Math.min(overlapL, overlapR), minY = Math.min(overlapT, overlapB);
      let nx = 0, ny = 0;
      if (minX < minY) {
        nx = overlapL < overlapR ? -1 : 1;
        ball.x += nx * minX;
      } else {
        ny = overlapT < overlapB ? -1 : 1;
        ball.y += ny * minY;
      }
      // impact dot = |cos angle| between velocity and face normal
      const sp = Math.hypot(ball.vx, ball.vy) || 1;
      const impactDot = Math.abs((ball.vx * nx + ball.vy * ny) / sp);
      const info = { speed: sp, impactDot };

      // fireball passes through standard/phase/moving bricks (burns them), heavy smashes armor
      let destroyed = false;
      if (ball.fire && ['standard', 'phase', 'moving', 'angle', 'carrier'].includes(b.type)) {
        g.destroyBrick(b, ball);
        destroyed = true;
      } else if (ball.heavy && b.type === 'armored') {
        g.destroyBrick(b, ball);
        destroyed = true;
      } else {
        destroyed = b.hit(ball, info, g);
      }

      if (!destroyed) {
        // bounce off the face we resolved (reflect on that axis only)
        if (nx) ball.vx = -ball.vx;
        if (ny) ball.vy = -ball.vy;
        SFX.bounce();
        FX.brickBounce(ball.x, ball.y, BRICK_TYPES[b.type].color);
      }
      break; // one brick per substep
    }
  },
};

/* =====================================================================
   10. RENDERING
   ===================================================================== */
let canvas, ctx;
let viewScale = 1, viewOffX = 0, viewOffY = 0;
let postOverlay = null, postOverlayW = 0, postOverlayH = 0;

// map client px -> world px
function toWorld(clientX, clientY) {
  // clientX/Y are CSS pixels; viewScale is in device pixels (includes dpr).
  // Map through the canvas' CSS bounding rect so touch input is correct
  // at any devicePixelRatio (full-bleed canvas). The mapping is uniform
  // (same scale on both axes) and accounts for the letterbox offsets used
  // when the world aspect doesn't match the viewport exactly.
  const r = canvas.getBoundingClientRect();
  const cssScale = r.width > 0 ? r.width / W : viewScale / (canvas.width / W);
  const offX = r.width > 0 ? viewOffX * (r.width / canvas.width) : 0;
  const offY = r.height > 0 ? viewOffY * (r.height / canvas.height) : 0;
  return { x: (clientX - r.left - offX) / cssScale, y: (clientY - r.top - offY) / cssScale };
}

function setupCanvas() {
  const dpr = Math.min(window.devicePixelRatio || 1, 2);
  const vw = window.innerWidth, vh = window.innerHeight;
  canvas.width = Math.floor(vw * dpr);
  canvas.height = Math.floor(vh * dpr);
  canvas.style.width = vw + 'px';
  canvas.style.height = vh + 'px';
  // full-bleed: keep world width fixed, extend world height on tall screens
  // (portrait phones) so the play area fills the viewport with no letterbox.
  W = 960;
  H = Math.round(W * vh / vw);
  H = clamp(H, 600, 1600);
  // On wider-than-16:9 viewports the clamped-min height would letterbox with a
  // negative offset (cropping the HUD off the top). Never let the vertical
  // offset go negative: shrink H to the true viewport aspect instead.
  viewScale = canvas.width / W;
  if (H * viewScale > canvas.height) H = Math.round(canvas.height / viewScale);
  viewOffX = 0;
  viewOffY = (canvas.height - H * viewScale) / 2; // tiny centering if clamped
  // keep derived paddle geometry consistent
  PADDLE_Y_BASE = H - 70;
  PADDLE_BAND_TOP = H - 190;
  Game.paddle.y = clamp(Game.paddle.y, PADDLE_BAND_TOP, PADDLE_Y_BASE);
  Game.paddle.x = clamp(Game.paddle.x, Game.paddle.w / 2 + FIELD_SIDE, W - Game.paddle.w / 2 - FIELD_SIDE);
}

/* --- star field (parallax) --- */
const stars = [];
for (let i = 0; i < 90; i++) stars.push({ x: Math.random() * W, y: Math.random() * H, z: rand(0.2, 1), tw: rand(0, TAU) });
// extra stars that only appear at higher tiers (density per tier, item 1)
const hotStars = [];
for (let i = 0; i < 130; i++) hotStars.push({ x: Math.random() * W, y: Math.random() * H, z: rand(0.3, 1), tw: rand(0, TAU) });
// drifting nebula glow blobs (pre-rendered radial gradients, item 7: no per-frame shadowBlur)
const nebulas = [];
for (let i = 0; i < 6; i++) nebulas.push({
  x: Math.random() * W, y: Math.random() * H,
  r: rand(120, 260), drift: rand(4, 14), phase: rand(0, TAU),
});
let nebulaSprite = null, nebulaSpriteSize = 0;
function getNebulaSprite(size) {
  if (nebulaSprite && nebulaSpriteSize === size) return nebulaSprite;
  const c = document.createElement('canvas');
  c.width = c.height = size;
  const g2 = c.getContext('2d');
  const grad = g2.createRadialGradient(size / 2, size / 2, 0, size / 2, size / 2, size / 2);
  grad.addColorStop(0, 'rgba(255,255,255,0.9)');
  grad.addColorStop(0.4, 'rgba(255,255,255,0.35)');
  grad.addColorStop(1, 'rgba(255,255,255,0)');
  g2.fillStyle = grad;
  g2.fillRect(0, 0, size, size);
  nebulaSprite = c; nebulaSpriteSize = size;
  return c;
}
// pre-tinted white sprite × tier color, cached per tier (item 7)
const nebulaTinted = new Array(TIERS.length).fill(null);
function getTintedNebula(tier) {
  if (nebulaTinted[tier]) return nebulaTinted[tier];
  const size = 256;
  const base = getNebulaSprite(size);
  const c = document.createElement('canvas');
  c.width = c.height = size;
  const g2 = c.getContext('2d');
  g2.drawImage(base, 0, 0);
  g2.globalCompositeOperation = 'source-in'; // keep sprite alpha, fill with color
  g2.fillStyle = NEBULA_HUES[tier];
  g2.fillRect(0, 0, size, size);
  nebulaTinted[tier] = c;
  return c;
}
const NEBULA_HUES = ['#1a3a5c', '#1f4a3a', '#4a3a1a', '#5c2a1a', '#5c1a2a']; // per-tier hue family

function drawBackground(t) {
  const tier = TierHeat.tier;
  // nebula blobs: alpha/saturation intensify with tier (item 1)
  if (tier > 0) {
    const sprite = getTintedNebula(tier);
    ctx.globalCompositeOperation = 'lighter';
    for (let i = 0; i < nebulas.length; i++) {
      const n = nebulas[i];
      const y = (n.y + t * n.drift) % (H + n.r * 2) - n.r;
      const wobble = 1 + Math.sin(t * 0.4 + n.phase) * 0.12;
      ctx.globalAlpha = 0.10 + tier * 0.09;
      ctx.drawImage(sprite, n.x + Math.sin(t * 0.2 + n.phase) * 30 - n.r * wobble, y - n.r * wobble, n.r * 2 * wobble, n.r * 2 * wobble);
    }
    ctx.globalCompositeOperation = 'source-over';
    ctx.globalAlpha = 1;
  }
  // subtle grid (world space)
  ctx.strokeStyle = 'rgba(70,90,160,0.07)';
  ctx.lineWidth = 1;
  ctx.beginPath();
  for (let x = 0; x <= W; x += 60) { ctx.moveTo(x, 0); ctx.lineTo(x, H); }
  for (let y = 0; y <= H; y += 60) { ctx.moveTo(0, y); ctx.lineTo(W, y); }
  ctx.stroke();
  // parallax stars: speed & density scale with tier (item 1)
  const heat = TierHeat.heat;
  const speed = 6 + tier * tier * 7;
  const showHot = Math.floor(heat * hotStars.length);
  for (let i = 0; i < stars.length; i++) {
    const s = stars[i];
    const y = (s.y + t * speed * s.z) % H;
    const tw = 0.4 + 0.6 * Math.sin(t * 2 + s.tw);
    ctx.fillStyle = `rgba(180,200,255,${0.35 * s.z * tw})`;
    ctx.fillRect(s.x, y, s.z * 2, s.z * 2);
  }
  for (let i = 0; i < showHot; i++) {
    const s = hotStars[i];
    const y = (s.y + t * (speed + 14) * s.z) % H;
    const tw = 0.4 + 0.6 * Math.sin(t * 3 + s.tw);
    ctx.fillStyle = `rgba(255,${200 - tier * 20},180,${0.4 * s.z * tw})`;
    ctx.fillRect(s.x, y, s.z * 2.4, s.z * 2.4);
  }
  // scanlines get stronger with tier (item 1) — drawn in render() vignette pass
}

function drawBrick(b, t) {
  const info = BRICK_TYPES[b.type];
  const kx = b.knock > 0 ? rand(-2, 2) * b.knock : 0;
  let x = b.x + kx, y = b.y;
  const w = b.w, h = b.h;
  let alpha = 1;
  if (b.type === 'phase') {
    // pulse glow in sync with phase timer: bright near the solid->intangible switch (item 1)
    const c = b.phaseT % PHASE_CYCLE;
    const imminent = c > PHASE_CYCLE * 0.6 - 0.35 && b.solid;
    alpha = b.solid ? (0.75 + 0.25 * Math.sin(t * 12)) : 0.14;
    if (imminent) alpha = Math.min(1, alpha + 0.25 * (1 - Math.abs((PHASE_CYCLE * 0.6 - c) / 0.35 - 0.5) * 2));
  }
  // spawn-in animation: staggered drop + scale-in (item 5)
  let scale = 1, dropY = 0;
  if (b.spawnT >= 0) {
    const st = clamp(b.spawnT / 0.45, 0, 1);
    const ease = 1 - Math.pow(1 - st, 3); // easeOutCubic
    scale = 0.3 + ease * 0.7;
    dropY = (1 - ease) * -26;
    alpha = ease;
  }
  ctx.globalAlpha = alpha;
  x += (w - w * scale) / 2; y += (h - h * scale) / 2 + dropY;
  // glow border — layered strokes instead of shadowBlur in the hot path (item 7)
  ctx.strokeStyle = info.color;
  ctx.lineWidth = 2;
  ctx.fillStyle = rgba(info.color, 0.16);
  ctx.beginPath(); ctx.rect(x, y, w * scale, h * scale); ctx.fill();
  // soft outer glow: two cheap translucent strokes
  ctx.globalAlpha = alpha * 0.35;
  ctx.lineWidth = 5;
  ctx.stroke();
  ctx.globalAlpha = alpha * 0.15;
  ctx.lineWidth = 9;
  ctx.stroke();
  ctx.globalAlpha = alpha;
  ctx.lineWidth = 2;
  ctx.stroke();

  // per-type motifs
  if (b.type === 'armored') {
    // speed gauge: 3 bars; lit if a ball at threshold+ would break it
    for (let i = 0; i < 3; i++) {
      ctx.fillStyle = i < 2 ? info.color : rgba(info.color, 0.35);
      ctx.fillRect(x + 5 + i * 8, y + b.h - 8, 5, 4);
    }
  } else if (b.type === 'angle') {
    // dial with tick marks around center dot + slowly rotating marker (item 1)
    const cx = b.cx, cy = b.cy;
    ctx.strokeStyle = info.color; ctx.lineWidth = 1.5;
    ctx.beginPath(); ctx.arc(cx, cy, 7, 0, TAU); ctx.stroke();
    for (let i = 0; i < 5; i++) {
      const a = -Math.PI / 2 + (i - 2) * (ANGLE_WINDOW_DEG * Math.PI / 180) * 0.6;
      ctx.beginPath();
      ctx.moveTo(cx + Math.cos(a) * 5, cy + Math.sin(a) * 5);
      ctx.lineTo(cx + Math.cos(a) * 9, cy + Math.sin(a) * 9);
      ctx.stroke();
    }
    // rotating outer marker
    const ma = t * 1.6 + b.col;
    ctx.beginPath();
    ctx.arc(cx + Math.cos(ma) * 11, cy + Math.sin(ma) * 11, 2, 0, TAU);
    ctx.fillStyle = info.color; ctx.fill();
  } else if (b.type === 'generator') {
    ctx.fillStyle = info.color;
    ctx.font = 'bold 14px monospace'; ctx.textAlign = 'center';
    ctx.fillText('G', b.cx, b.cy + 5);
    // pulse when about to spawn (item 1)
    if (b.genT < 1.2) {
      const p = 1 - b.genT / 1.2;
      ctx.strokeStyle = info.color;
      ctx.globalAlpha = alpha * p * (0.5 + 0.5 * Math.sin(t * 20));
      ctx.lineWidth = 3;
      ctx.beginPath(); ctx.rect(x - 4, y - 4, w * scale + 8, h * scale + 8); ctx.stroke();
      ctx.globalAlpha = alpha;
    }
  } else if (b.type === 'indestructible') {
    ctx.strokeStyle = rgba(info.color, 0.5); ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.moveTo(x + 4, y + 4); ctx.lineTo(x + b.w - 4, y + b.h - 4);
    ctx.moveTo(x + b.w - 4, y + 4); ctx.lineTo(x + 4, y + b.h - 4);
    ctx.stroke();
  } else if (b.type === 'explosive') {
    ctx.fillStyle = info.color;
    ctx.beginPath(); ctx.arc(b.cx, b.cy, 5 + Math.sin(t * 6 + b.x) * 1.5, 0, TAU); ctx.fill();
  }
  ctx.globalAlpha = 1;
}

function drawPaddle(t) {
  const p = Game.paddle;
  const tier = TierHeat.tier;
  const tierCol = TIERS[tier].color;
  const col = Game.activeTimers.laser ? '#ffee58' : p.sticky ? '#4dd0e1' : tierCol;
  // squash-and-stretch: squash compresses vertically, stretches horizontally (item 5)
  const sq = p.squash;
  const w = p.w * (1 + sq * 0.22);
  const h = p.h * (1 - sq * 0.35);
  const x = p.x - w / 2, y = p.y - h / 2;
  // energy rim: layered strokes, animated speed & color shift per tier (item 1)
  ctx.strokeStyle = col;
  ctx.lineWidth = 2;
  ctx.fillStyle = col;
  ctx.beginPath();
  ctx.roundRect(x, y, w, h, h / 2);
  ctx.fill();
  ctx.globalAlpha = 0.4;
  ctx.lineWidth = 4;
  ctx.stroke();
  ctx.globalAlpha = 0.15;
  ctx.lineWidth = 8;
  ctx.stroke();
  ctx.globalAlpha = 1;
  // energy core line — brighter & faster shimmer at higher tiers
  const eg = clamp(-p.vy / 300, 0, 1);
  const shimmer = 0.4 + eg * 0.6 + (tier > 0 ? Math.sin(t * (6 + tier * 5)) * 0.2 : 0);
  ctx.fillStyle = `rgba(255,209,102,${clamp(shimmer, 0, 1)})`;
  ctx.fillRect(x + 8, y + h / 2 - 1.5, w - 16, 3);
  if (Game.activeTimers.laser) {
    // twin cannons (item 1)
    ctx.fillStyle = '#ffee58';
    ctx.fillRect(x + w / 3 - 3, y - 9, 6, 9);
    ctx.fillRect(x + 2 * w / 3 - 5, y - 9, 6, 9);
    ctx.globalAlpha = 0.5 + 0.5 * Math.sin(t * 14);
    ctx.fillRect(x + w / 3 - 1.5, y - 13, 3, 4);
    ctx.fillRect(x + 2 * w / 3 - 3, y - 13, 3, 4);
    ctx.globalAlpha = 1;
  }
  if (p.sticky) {
    // catch-field arc above the paddle (item 1)
    ctx.strokeStyle = '#4dd0e1';
    ctx.globalAlpha = 0.4 + 0.2 * Math.sin(t * 6);
    ctx.lineWidth = 2.5;
    ctx.beginPath();
    ctx.arc(p.x, p.y, w * 0.62, Math.PI * 1.15, Math.PI * 1.85);
    ctx.stroke();
    ctx.globalAlpha = 1;
  }
}

function drawBall(b) {
  const tier = TierHeat.tier;
  const fast = b.speed / BALL_MAX_SPEED;
  const powered = b.fire || b.heavy;
  const trailN = b.trail.length;
  // trail: longer & wider per tier / power state (item 1)
  for (let i = 0; i < trailN; i++) {
    const tp = b.trail[i];
    const f = i / trailN;
    ctx.globalAlpha = f * (powered ? 0.7 : 0.35) * (0.4 + fast);
    ctx.fillStyle = b.fire ? '#ff5c49' : b.heavy ? '#b388ff' : '#7de8ff';
    ctx.beginPath(); ctx.arc(tp.x, tp.y, b.r * f * (0.9 + tier * 0.14), 0, TAU); ctx.fill();
  }
  ctx.globalAlpha = 1;
  // glow halo: radius/pulse scales with tier (item 1)
  const haloR = b.r * (2.2 + tier * 0.7) + Math.sin(Game.time * (6 + tier * 3)) * (1.5 + tier);
  ctx.globalAlpha = powered ? 0.3 : 0.14 + tier * 0.05;
  ctx.fillStyle = b.fire ? '#ff5c49' : b.heavy ? '#b388ff' : '#7de8ff';
  ctx.beginPath(); ctx.arc(b.x, b.y, haloR, 0, TAU); ctx.fill();
  ctx.globalAlpha = 1;
  // core (impact squash: stretched along velocity)
  const imp = b.impact;
  ctx.fillStyle = b.fire ? '#ff5c49' : b.heavy ? '#b388ff' : '#ffffff';
  ctx.beginPath();
  if (imp > 0.01) {
    const rx = b.r * (1 + imp * 0.5), ry = b.r * (1 - imp * 0.4);
    ctx.ellipse(b.x, b.y, Math.max(rx, ry), Math.min(rx, ry), Math.atan2(b.vy, b.vx), 0, TAU);
  } else {
    ctx.arc(b.x, b.y, b.r, 0, TAU);
  }
  ctx.fill();
  // fireball: ember sparks off the trail (item 1)
  if (b.fire && Game.state === 'playing' && Math.random() < 0.35 && FX.budget - 60 > 0) {
    const a = rand(0, TAU), s = rand(30, 90);
    FX.burst(b.x, b.y, '#ff9e3d', 1, s);
  }
  // multiball cell-division peel (item 4)
  if (b.splitT > 0) {
    const st = b.splitT / 0.35;
    const off = (1 - st) * 22 * (b.splitDX || 1);
    ctx.globalAlpha = st * 0.6;
    ctx.strokeStyle = '#ffd166';
    ctx.lineWidth = 2;
    ctx.beginPath(); ctx.arc(b.x - off, b.y, b.r + (1 - st) * 14, 0, TAU); ctx.stroke();
    ctx.globalAlpha = 1;
  }
}

function drawPowerups(t) {
  ctx.font = 'bold 13px monospace'; ctx.textAlign = 'center';
  for (const pu of Game.powerups) {
    const info = POWERUP_TYPES[pu.type];
    const bob = Math.sin(pu.t) * 2;
    ctx.shadowColor = info.color; ctx.shadowBlur = 14;
    ctx.fillStyle = rgba(info.color, 0.25);
    ctx.strokeStyle = info.color; ctx.lineWidth = 2;
    ctx.beginPath(); ctx.roundRect(pu.x - pu.w / 2, pu.y - pu.h / 2 + bob, pu.w, pu.h, 8); ctx.fill(); ctx.stroke();
    ctx.shadowBlur = 0;
    ctx.fillStyle = info.color;
    ctx.fillText(info.letter, pu.x, pu.y + 5 + bob);
  }
}

function drawStick() {
  const s = Input.stick;
  if (s.active) {
    ctx.strokeStyle = 'rgba(120,160,255,0.4)'; ctx.lineWidth = 2;
    ctx.beginPath(); ctx.arc(s.baseX, s.baseY, Input.stickRadius, 0, TAU); ctx.stroke();
    ctx.fillStyle = 'rgba(120,160,255,0.55)';
    ctx.beginPath(); ctx.arc(s.knobX, s.knobY, 26, 0, TAU); ctx.fill();
  } else if (!Game.thumbstickUsed && isTouchDevice()) {
    // faint idle thumbstick hint (item 6b): dashed circle + DRAG microcopy
    const hx = 96, hy = H - 110;
    ctx.save();
    ctx.globalAlpha = 0.22 + 0.08 * Math.sin(Game.time * 2);
    ctx.strokeStyle = '#78a0ff'; ctx.lineWidth = 2;
    ctx.setLineDash([7, 7]);
    ctx.beginPath(); ctx.arc(hx, hy, Input.stickRadius, 0, TAU); ctx.stroke();
    ctx.setLineDash([]);
    ctx.fillStyle = '#78a0ff';
    ctx.beginPath(); ctx.arc(hx, hy, 22, 0, TAU); ctx.fill();
    ctx.fillStyle = 'rgba(232,244,255,0.6)';
    ctx.font = '11px monospace'; ctx.textAlign = 'center';
    ctx.fillText('DRAG', hx, hy + 4);
    ctx.restore();
  }
}

let touchDeviceCache = null;
function isTouchDevice() {
  if (touchDeviceCache === null) {
    touchDeviceCache = ('ontouchstart' in window) || (navigator.maxTouchPoints > 0);
  }
  return touchDeviceCache;
}

/* HUD buttons (drawn, with hit zones) */
const pauseBtn = { x: W - 52, y: TOP_HUD / 2 + 4, w: 44, h: 36 };
const muteBtn = { x: W - 104, y: TOP_HUD / 2 + 4, w: 44, h: 36 };

function drawHUD(t) {
  const g = Game;
  ctx.fillStyle = 'rgba(10,14,30,0.85)';
  ctx.fillRect(0, 0, W, TOP_HUD);
  ctx.font = 'bold 15px monospace'; ctx.textAlign = 'left';
  ctx.fillStyle = '#e8f4ff';
  ctx.fillText('SCORE ' + g.score, 14, 29);
  ctx.fillStyle = '#ffd166';
  ctx.fillText('HI ' + g.highScore, 170, 29);
  ctx.textAlign = 'center';
  ctx.fillStyle = '#4dd0e1';
  ctx.fillText('LVL ' + (g.levelIndex + 1), W / 2 - 80, 29);
  // lives
  ctx.textAlign = 'left';
  ctx.fillStyle = '#ff6ec7';
  let lx = W / 2 - 30;
  for (let i = 0; i < Math.min(g.lives, 6); i++) { ctx.beginPath(); ctx.arc(lx + i * 18, 24, 5, 0, TAU); ctx.fill(); lx += 0; }
  // active timed power-ups with radial timers (item 4)
  let px = W / 2 + 60;
  for (const [type, timeLeft] of Object.entries(g.activeTimers)) {
    const info = POWERUP_TYPES[type];
    const frac = clamp(timeLeft / TIMED_POWER[type], 0, 1);
    // blink when running out
    const urgent = frac < 0.25 && Math.sin(t * 16) > 0;
    ctx.globalAlpha = urgent ? 0.4 : 1;
    ctx.strokeStyle = info.color; ctx.lineWidth = 3;
    ctx.beginPath();
    ctx.arc(px, 24, 9, -Math.PI / 2, -Math.PI / 2 + TAU * frac);
    ctx.stroke();
    ctx.fillStyle = info.color; ctx.font = 'bold 10px monospace'; ctx.textAlign = 'center';
    ctx.fillText(info.letter, px, 28);
    ctx.globalAlpha = 1;
    px += 26;
  }
  // power-up catch chip pop animation (item 4)
  if (g.hudChipAnim) {
    const a = g.hudChipAnim;
    const info = POWERUP_TYPES[a.type];
    if (info) {
      const p = a.t / 0.6;
      const rise = -26 * Math.sin(p * Math.PI);
      ctx.globalAlpha = 1 - Math.max(0, (p - 0.6) / 0.4);
      ctx.font = `bold ${13 + Math.sin(p * Math.PI) * 8}px monospace`;
      ctx.textAlign = 'center';
      ctx.fillStyle = info.color;
      ctx.fillText(info.name, W / 2 + 30, 26 + rise);
      ctx.globalAlpha = 1;
    }
  }
  // pause + mute buttons
  drawBtn(pauseBtn, g.state === STATE.PAUSED ? '▶' : '❚❚');
  drawBtn(muteBtn, g.muted ? '🔇' : '🔊');
  // heat meter (escalation visibility, item 1): thin bar under the HUD.
  // Crisp on any scale: snap to device pixels and skip the faint empty
  // track at very low zoom (it read as a stray dotted line under the HUD).
  const meterW = 120, meterH = 5;
  const mx = W / 2 - meterW / 2, my = TOP_HUD + 8;
  const heat = TierHeat.heat;
  const sc = canvas.width / W;
  const heatFrac = heat > 0.02 ? heat : 0;
  if (heatFrac > 0) {
    ctx.fillStyle = 'rgba(120,160,255,0.4)';
    ctx.fillRect(Math.round(mx * sc) / sc, Math.round(my * sc) / sc, Math.round(meterW * heatFrac * sc) / sc, Math.max(1, Math.round(meterH * sc)) / sc);
    const grad2 = ctx.createLinearGradient(mx, 0, mx + meterW, 0);
    grad2.addColorStop(0, TIERS[0].color);
    grad2.addColorStop(1, TIERS[TIERS.length - 1].color);
    ctx.fillStyle = grad2;
    ctx.fillRect(Math.round(mx * sc) / sc, Math.round(my * sc) / sc, Math.round(meterW * heatFrac * sc) / sc, Math.max(1, Math.round(meterH * sc)) / sc);
    // tier tick marks
    ctx.fillStyle = 'rgba(255,255,255,0.45)';
    for (let i = 1; i < TIERS.length; i++) {
      const tx = Math.round((mx + meterW * (i / TIERS.length)) * sc) / sc;
      ctx.fillRect(tx, Math.round(my * sc) / sc, Math.max(0.5, 1 / sc), Math.max(1, Math.round(meterH * sc)) / sc);
    }
  }
}

function drawGameOverStats(t) {
  const g = Game;
  const rows = [
    ['SCORE', String(g.score)],
    ['BEST COMBO', 'x' + (1 + Math.floor(g.comboBest / 6) * (g.onTop ? 3 : 1)) + '  (' + g.comboBest + ' chain)'],
    ['BRICKS DESTROYED', String(g.destroyedCount)],
    ['LEVEL REACHED', String(g.levelIndex + 1)],
    ['HIGH SCORE', String(g.highScore)],
  ];
  ctx.textAlign = 'center';
  ctx.font = 'bold 46px monospace';
  ctx.shadowColor = '#ff3355'; ctx.shadowBlur = 26;
  ctx.fillStyle = '#ff3355';
  ctx.fillText('GAME OVER', W / 2, H / 2 - 130);
  ctx.shadowBlur = 0;
  if (g.newBest) {
    ctx.font = 'bold 20px monospace';
    ctx.fillStyle = '#ffd166';
    ctx.globalAlpha = 0.7 + 0.3 * Math.sin(t * 6);
    ctx.fillText('★ NEW BEST! ★', W / 2, H / 2 - 94);
    ctx.globalAlpha = 1;
  }
  ctx.font = '16px monospace';
  rows.forEach((r, i) => {
    const y = H / 2 - 56 + i * 30;
    ctx.textAlign = 'right';
    ctx.fillStyle = 'rgba(150,170,220,0.85)';
    ctx.fillText(r[0], W / 2 - 14, y);
    ctx.textAlign = 'left';
    ctx.fillStyle = '#e8f4ff';
    ctx.font = 'bold 16px monospace';
    ctx.fillText(r[1], W / 2 + 14, y);
    ctx.font = '16px monospace';
  });
  ctx.textAlign = 'center';
  ctx.fillStyle = 'rgba(232,244,255,0.75)'; ctx.font = '18px monospace';
  ctx.globalAlpha = 0.6 + 0.4 * Math.sin(t * 4);
  ctx.fillText((isTouchDevice() ? 'TAP' : 'ENTER') + ' to play again', W / 2, H / 2 + 116);
  ctx.globalAlpha = 1;
}

function drawBtn(b, label) {
  ctx.fillStyle = 'rgba(120,160,255,0.15)';
  ctx.strokeStyle = 'rgba(120,160,255,0.5)'; ctx.lineWidth = 2;
  ctx.beginPath(); ctx.roundRect(b.x, b.y, b.w, b.h, 8); ctx.fill(); ctx.stroke();
  ctx.fillStyle = '#e8f4ff'; ctx.font = '15px monospace'; ctx.textAlign = 'center';
  ctx.fillText(label, b.x + b.w / 2, b.y + b.h / 2 + 5);
}

function drawBigText(txt, sub, color, t, pulse) {
  ctx.textAlign = 'center';
  const s = pulse ? 1 + Math.sin(t * 6) * 0.04 : 1;
  ctx.save();
  ctx.translate(W / 2, H / 2 - 40); ctx.scale(s, s);
  ctx.shadowColor = color; ctx.shadowBlur = 30;
  ctx.fillStyle = color; ctx.font = 'bold 54px monospace';
  ctx.fillText(txt, 0, 0);
  ctx.restore();
  ctx.shadowBlur = 0;
  if (sub) {
    ctx.fillStyle = 'rgba(232,244,255,0.85)'; ctx.font = '20px monospace';
    ctx.fillText(sub, W / 2, H / 2 + 20);
  }
}

function drawCombo(t) {
  const g = Game;
  const tier = TierHeat.tier;
  const tierInfo = TIERS[tier];
  if (g.combo >= 6) {
    const big = g.combo >= 15;
    // scale/pulse with tier (item 1)
    const s = (big ? 1 + Math.sin(t * 10) * 0.06 : 1) * (1 + tier * 0.08);
    ctx.save();
    ctx.translate(W / 2, H / 2); ctx.scale(s, s);
    ctx.globalAlpha = big ? 0.9 : 0.55;
    ctx.shadowColor = tierInfo.color; ctx.shadowBlur = 24 + tier * 6;
    ctx.fillStyle = tierInfo.color; ctx.font = `bold ${(big ? 72 : 44) + tier * 4}px monospace`;
    ctx.textAlign = 'center';
    ctx.fillText('x' + g.multiplier, 0, 0);
    ctx.restore();
    ctx.shadowBlur = 0; ctx.globalAlpha = 1;
    if (g.onTop) {
      ctx.fillStyle = '#ff9e3d'; ctx.font = 'bold 20px monospace'; ctx.textAlign = 'center';
      ctx.globalAlpha = 0.7 + 0.3 * Math.sin(t * 8);
      ctx.fillText('★ ON FIRE — ABOVE THE FIELD ★', W / 2, H / 2 + 64);
      ctx.globalAlpha = 1;
      // combo ticks sparkle (item 3)
      if (g.state === 'playing' && Math.random() < 0.3) FX.burst(rand(80, W - 80), rand(FIELD_TOP - 60, FIELD_TOP - 10), '#ffd166', 1, 60);
    }
  }
  // persistent tier label with animated gradient text at HOT+ (item 1)
  if (tier >= 2 && (g.state === 'playing' || g.state === 'levelclear')) {
    const label = tierInfo.name;
    ctx.save();
    ctx.font = 'bold 17px monospace';
    ctx.textAlign = 'center';
    const grad = ctx.createLinearGradient(W / 2 - 70, 0, W / 2 + 70, 0);
    const shift = (t * 0.25) % 1;
    grad.addColorStop(Math.max(0, shift - 0.3), tierInfo.color);
    grad.addColorStop(clamp(shift, 0.01, 0.99), '#ffffff');
    grad.addColorStop(Math.min(1, shift + 0.3), tierInfo.color);
    ctx.fillStyle = grad;
    ctx.globalAlpha = 0.85 + 0.15 * Math.sin(t * 5);
    ctx.fillText('⟪ ' + label + ' ⟫', W / 2, FIELD_TOP + 34);
    ctx.restore();
    ctx.globalAlpha = 1;
  }
}

function render(t) {
  const g = Game;
  ctx.save();
  // full clear in device space
  ctx.setTransform(1, 0, 0, 1, 0, 0);
  ctx.fillStyle = '#05060f';
  ctx.fillRect(0, 0, canvas.width, canvas.height);
  // world transform with shake + directional kick (item 1)
  const so = FX.shakeOffset;
  const ko = FX.kickOffset;
  ctx.setTransform(viewScale, 0, 0, viewScale,
    viewOffX + (so.x + clamp(ko.x, -14, 14)) * viewScale,
    viewOffY + (so.y + clamp(ko.y, -14, 14)) * viewScale);
  drawBackground(t);

  // playfield (clipped below HUD)
  ctx.save();
  ctx.beginPath(); ctx.rect(0, TOP_HUD, W, H - TOP_HUD); ctx.clip();
  if (g.state === STATE.TITLE) {
    drawTitle(t);
  } else {
    for (const b of g.bricks) if (b.alive) drawBrick(b, t);
    drawPowerups(t);
    for (const l of g.lasers) {
      ctx.fillStyle = '#ffee58'; ctx.shadowColor = '#ffee58'; ctx.shadowBlur = 10;
      ctx.fillRect(l.x - 1.5, l.y - 12, 3, 14); ctx.shadowBlur = 0;
    }
    drawPaddle(t);
    for (const b of g.balls) if (b.active) drawBall(b);
    drawCombo(t);

    if (g.state === STATE.LEVEL_INTRO) {
      const a = clamp(g.introT / 0.4, 0, 1) * clamp((2.2 - g.introT) / 0.6, 0, 1);
      ctx.globalAlpha = clamp(a, 0, 1);
      drawBigText('LEVEL ' + (g.levelIndex + 1), g.level ? g.level.name : '', '#4dd0e1', t, false);
      if (g.level) { ctx.fillStyle = 'rgba(232,244,255,0.7)'; ctx.font = '16px monospace'; ctx.textAlign = 'center'; ctx.fillText(g.level.subtitle, W / 2, H / 2 + 52); }
      ctx.globalAlpha = 1;
    } else if (g.state === STATE.READY) {
      drawBigText('READY', isTouchDevice() ? 'Tap right side to launch' : 'Click / Space to launch', '#69f0ae', t, true);
    } else if (g.state === STATE.PAUSED) {
      ctx.fillStyle = 'rgba(0,0,0,0.55)'; ctx.fillRect(0, TOP_HUD, W, H);
      drawBigText('PAUSED', 'P or button to resume', '#7e9bff', t, false);
    } else if (g.state === STATE.LEVEL_CLEAR) {
      drawBigText('LEVEL CLEAR', 'Score ' + g.score, '#ffd166', t, true);
    } else if (g.state === STATE.GAME_OVER) {
      ctx.fillStyle = 'rgba(0,0,0,0.6)'; ctx.fillRect(0, TOP_HUD, W, H);
      drawGameOverStats(t);
    }
  }
  // onTop farming state: subtle top-of-field glow band (item 3)
  if (g.onTop && g.state === STATE.PLAYING) {
    const band = ctx.createLinearGradient(0, TOP_HUD, 0, FIELD_TOP + 40);
    const a = 0.10 + 0.05 * Math.sin(t * 4);
    band.addColorStop(0, `rgba(255,209,102,${a})`);
    band.addColorStop(1, 'rgba(255,209,102,0)');
    ctx.fillStyle = band;
    ctx.fillRect(0, TOP_HUD, W, FIELD_TOP + 40 - TOP_HUD);
  }
  // breakthrough banner with letter stagger (item 3)
  const bn = FX.bannerState;
  if (bn.on) {
    const bt = bn.t;
    const inA = clamp(bt / 0.3, 0, 1);
    const outA = clamp((2.2 - bt) / 0.5, 0, 1);
    ctx.globalAlpha = Math.min(inA, outA);
    ctx.font = 'bold 56px monospace';
    ctx.textAlign = 'center';
    const chars = bn.text.split('');
    const totalW = ctx.measureText(bn.text).width;
    let cx0 = W / 2 - totalW / 2;
    chars.forEach((ch, i) => {
      const stagger = clamp((bt - i * 0.045) / 0.25, 0, 1);
      const pop = Math.sin(stagger * Math.PI);
      const chw = ctx.measureText(ch).width;
      ctx.save();
      ctx.translate(cx0 + chw / 2, H / 2 - 176 - pop * 14);
      ctx.scale(1 + pop * 0.3, 1 + pop * 0.3);
      ctx.fillStyle = bn.color;
      ctx.shadowColor = bn.color; ctx.shadowBlur = 18 * pop;
      ctx.fillText(ch, 0, 0);
      ctx.restore();
      cx0 += chw;
      ctx.shadowBlur = 0;
    });
    ctx.globalAlpha = 1;
  }
  FX.draw(ctx);
  ctx.restore(); // end playfield clip

  // HUD & overlays in unclipped world space
  if (g.state !== STATE.TITLE) drawHUD(t);
  // flash overlay
  const fl = FX.flashState;
  if (fl.alpha > 0) { ctx.fillStyle = rgba(fl.color, fl.alpha); ctx.fillRect(0, 0, W, H); }

  // ---- post-processing (tier + slow-mo) ----
  // hue shift of the whole field for ~2s after breakthrough (item 3)
  const hue = FX.hueShiftAmount;
  if (hue > 0.01) {
    ctx.save();
    ctx.globalCompositeOperation = 'hue';
    ctx.globalAlpha = clamp(hue, 0, 1) * 0.5;
    ctx.fillStyle = '#ffb3d9';
    ctx.fillRect(0, 0, W, H);
    ctx.restore();
  }
  // slow-mo power-up: tint + slight desaturation feel + vignette (item 4)
  const st = FX.slowTintAmount;
  if (st > 0.01) {
    ctx.save();
    ctx.globalAlpha = st * 0.18;
    ctx.fillStyle = '#40c4ff';
    ctx.fillRect(0, 0, W, H);
    ctx.restore();
  }
  // vignette + scanlines, stronger with tier (item 1) — baked to one offscreen
  // overlay at boot/resize, drawn with a single drawImage (item 7)
  const vig = clamp(FX.vignetteAmount, 0, 0.55);
  if (!postOverlay || postOverlayW !== W || postOverlayH !== H) {
    postOverlay = document.createElement('canvas');
    postOverlay.width = W; postOverlay.height = H;
    const pg = postOverlay.getContext('2d');
    const vg = pg.createRadialGradient(W / 2, H / 2, Math.min(W, H) * 0.36, W / 2, H / 2, Math.max(W, H) * 0.72);
    vg.addColorStop(0, 'rgba(0,0,0,0)');
    vg.addColorStop(1, 'rgba(0,0,0,1)');
    pg.fillStyle = vg;
    pg.fillRect(0, 0, W, H);
    pg.fillStyle = 'rgba(0,0,0,0.35)';
    for (let y = TOP_HUD; y < H; y += 4) pg.fillRect(0, y, W, 1);
    postOverlayW = W; postOverlayH = H;
  }
  ctx.globalAlpha = vig;
  ctx.drawImage(postOverlay, 0, 0);
  ctx.globalAlpha = 1;

  if (DEBUG.enabled) drawDebug();

  ctx.restore();
  // thumbstick is drawn in screen space
  ctx.setTransform(viewScale, 0, 0, viewScale, viewOffX, viewOffY);
  drawStick();
}

function drawTitle(t) {
  const bob = Math.sin(t * 2) * 8;
  ctx.textAlign = 'center';
  ctx.save();
  ctx.translate(W / 2, H / 2 - 120 + bob);
  ctx.shadowColor = '#4dd0e1'; ctx.shadowBlur = 40;
  ctx.fillStyle = '#4dd0e1'; ctx.font = 'bold 72px monospace';
  ctx.fillText('NEON BREAKER', 0, 0);
  ctx.restore(); ctx.shadowBlur = 0;
  // demo brick strip
  const cols = Object.values(BRICK_TYPES);
  cols.forEach((info, i) => {
    const x = W / 2 - (cols.length * 80) / 2 + i * 80;
    const y = H / 2 - 30;
    ctx.strokeStyle = info.color; ctx.shadowColor = info.color; ctx.shadowBlur = 10;
    ctx.fillStyle = rgba(info.color, 0.2);
    ctx.beginPath(); ctx.rect(x, y, 74, 22); ctx.fill(); ctx.stroke();
  });
  ctx.shadowBlur = 0;
  ctx.fillStyle = 'rgba(232,244,255,0.9)'; ctx.font = '20px monospace';
  ctx.fillText('Break through the field. Farm the sky.', W / 2, H / 2 + 40);
  ctx.fillStyle = '#ffd166'; ctx.font = 'bold 24px monospace';
  ctx.globalAlpha = 0.6 + 0.4 * Math.sin(t * 4);
  ctx.fillText(isTouchDevice() ? 'TAP TO START' : 'CLICK / ENTER TO START', W / 2, H / 2 + 110);
  ctx.globalAlpha = 1;
  ctx.fillStyle = 'rgba(150,170,220,0.6)'; ctx.font = '14px monospace';
  ctx.fillText('Arrows/WASD move  •  Space launch/fire  •  P pause  •  M mute  •  F3 debug', W / 2, H / 2 + 150);
  ctx.fillText('Mobile: lower-left thumbstick  •  tap right side to launch', W / 2, H / 2 + 176);
}

function drawDebug() {
  ctx.strokeStyle = '#00ff00'; ctx.lineWidth = 1;
  for (const b of Game.bricks) if (b.alive) ctx.strokeRect(b.x, b.y, b.w, b.h);
  for (const ball of Game.balls) if (ball.active) {
    ctx.beginPath(); ctx.arc(ball.x, ball.y, ball.r, 0, TAU); ctx.stroke();
  }
  ctx.fillStyle = '#00ff00'; ctx.font = '12px monospace'; ctx.textAlign = 'left';
  ctx.fillText('substeps: ' + Physics.lastSubsteps + '  balls: ' + Game.balls.length, 14, TOP_HUD + 20);
}

/* =====================================================================
   11. MAIN LOOP & BOOT
   ===================================================================== */
let lastTime = 0;
let accum = 0;

function frame(ts) {
  requestAnimationFrame(frame);
  const now = ts / 1000;
  let dt = Math.min(now - lastTime, MAX_FRAME_DT);
  lastTime = now;
  if (!(dt > 0)) dt = 0;

  const g = Game;

  // hit-stop: freeze the world briefly but keep rendering (item 5)
  if (FX.hitStopTime > 0) { FX.update(dt); render(g.time); return; }

  // breakthrough slow-mo: dip time to 0.35x with smooth ramp back (item 3)
  if (g.slowmoT > 0) {
    g.slowmoT -= dt;
    // hold at 0.35 for most of the dip, then ramp back up
    const f = g.slowmoT > 0.18 ? 0.35 : lerp(1, 0.35, g.slowmoT / 0.18);
    g.slowmoF = f;
  } else g.slowmoF = 1;
  const sdt = dt * g.slowmoF;

  g.time += dt;

  // global action input (click/space/tap)
  if (Input.actionQueued) {
    Input.actionQueued = false;
    SFX.ensure();
    if (g.state === STATE.TITLE) g.startGame();
    else if (g.state === STATE.READY) {
      for (const b of g.balls) if (b.stuck) b.launch(0.4);
      g.state = STATE.PLAYING;
    }
    else if (g.state === STATE.PLAYING) {
      // release stuck balls / fire laser handled by laser autofire
      let released = false;
      for (const b of g.balls) if (b.stuck) { b.launch(0.4); released = true; }
      if (!released && g.activeTimers.laser) { /* autofire already */ }
    }
    else if (g.state === STATE.GAME_OVER) g.startGame();
    else if (g.state === STATE.PAUSED && g.pausedByUser) g.togglePause();
  }

  // level intro timer
  if (g.state === STATE.LEVEL_INTRO) {
    g.introT += dt;
    if (g.introT > 2.2) g.beginReady();
  }
  if (g.state === STATE.LEVEL_CLEAR) {
    g.clearT += dt;
    if (g.clearT > 2.6) g.loadLevel(g.levelIndex + 1);
  }

  // fixed-timestep physics only while playing (intro/ready keep paddle live)
  const running = g.state === STATE.PLAYING;
  const paddleLive = running || g.state === STATE.READY || g.state === STATE.LEVEL_INTRO;
  if (paddleLive) {
    if (running) {
      accum += sdt;
      let steps = 0;
      while (accum >= FIXED_DT && steps < MAX_SUBSTEPS) { Physics.step(FIXED_DT); accum -= FIXED_DT; steps++; }
      if (steps >= MAX_SUBSTEPS) accum = 0; // bail out if we fell behind
    } else {
      // still allow paddle movement on ready/intro
      const intent = Input.paddleIntent(g.paddle);
      g.paddle.update(dt, intent);
      for (const b of g.balls) if (b.stuck) { b.x = g.paddle.x; b.y = g.paddle.y - g.paddle.h / 2 - b.r - 1; }
      // run spawn-in animation timers during intro so bricks are placed when play starts
      for (const b of g.bricks) if (b.alive && b.spawnT >= 0) b.update(dt, g);
    }
  }

  // escalation heat + tier stings, audio drone follows heat (items 1, 2)
  TierHeat.update(dt, g);
  SFX.heat(TierHeat.heat);

  // HUD chip animation decay
  if (g.hudChipAnim) { g.hudChipAnim.t += dt; if (g.hudChipAnim.t > 0.6) g.hudChipAnim = null; }

  FX.update(dt);
  render(g.time);
}

/* ---- boot ---- */
function boot() {
  canvas = document.getElementById('game');
  ctx = canvas.getContext('2d');
  setupCanvas();
  window.addEventListener('resize', () => { setupCanvas(); FX.resize(); });
  window.addEventListener('orientationchange', () => setTimeout(setupCanvas, 100));

  Input.init(canvas);
  // pause/mute button hit zones (pointer events in world coords)
  canvas.addEventListener('pointerdown', (e) => {
    const p = toWorld(e.clientX, e.clientY);
    if (hitBtn(pauseBtn, p)) { Game.togglePause(); Input.actionQueued = false; e.stopImmediatePropagation(); }
    else if (hitBtn(muteBtn, p)) { Game.toggleMute(); Input.actionQueued = false; e.stopImmediatePropagation(); }
  });

  Game.init();
  requestAnimationFrame((t) => { lastTime = t / 1000; requestAnimationFrame(frame); });
}

function hitBtn(b, p) {
  return p.x >= b.x && p.x <= b.x + b.w && p.y >= b.y && p.y <= b.y + b.h;
}

if (typeof window !== 'undefined') {
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', boot);
  else boot();
}
