// src/fx/fx.js
// Attic Breaker — FX module: pooled particles, screen shake, flashes,
// chromatic aberration, and all juice. Vanilla ES module, Canvas 2D only,
// zero per-frame allocation in update(), zero external deps.
//
// Coordinate spaces:
//   drawBelow(ctx) / drawAbove(ctx) are called with a WORLD-space ctx
//   (logical 1000x1500 units, already transformed by the renderer).
//   drawPost(ctx, w, h) is called with a SCREEN-space ctx (raw canvas pixels).

'use strict';

// ---------------------------------------------------------------------------
// Tunables
// ---------------------------------------------------------------------------
const MAX_PARTICLES = 2600;  // raised from 2200 (round-2 escalation), still << hard ceiling ~3000
const MAX_TRAILS = 400;      // ribbon trail segments (separate small pool) — legacy `trail()` dots
const MAX_POPUPS = 64;
const MAX_BOLTS = 24;        // lightning bolts, each with fixed-size segment buffer
const BOLT_SEGMENTS = 14;
const MAX_RIBBONS = 12;      // per-entity continuous ribbons (FX.ribbon)
const RIBBON_PTS = 16;       // fixed point buffer per ribbon
const RIBBON_IDLE_EXPIRE = 0.5; // seconds unfed before a ribbon id is dropped
const MAX_BLOOMS = 24;       // FX.bloomPulse pool

// Particle kind ids
const K_DOT = 0;      // glow spark, gravity+drag
const K_SHARD = 1;    // spinning square brick shard
const K_STREAK = 2;   // velocity-stretched line
const K_SMOKE = 3;    // soft expanding puff
const K_RING = 4;     // shockwave ring
const K_CONFETTI = 5; // fluttering square
const K_FLAME = 6;    // rising flame/plume particle (FX.flame)
const K_EMBER = 7;    // long-lived lingering ember (high-tier burst embellishment)

function isNum(n) { return typeof n === 'number' && Number.isFinite(n); }
function safe(n, fallback) { return isNum(n) ? n : fallback; }
function clamp01(n) { return n < 0 ? 0 : n > 1 ? 1 : n; }

// ---------------------------------------------------------------------------
// Structure-of-arrays particle pool (no per-particle objects)
// ---------------------------------------------------------------------------
const N = MAX_PARTICLES;
const px = new Float32Array(N);
const py = new Float32Array(N);
const pvx = new Float32Array(N);
const pvy = new Float32Array(N);
const page = new Float32Array(N);      // seconds alive
const plife = new Float32Array(N);     // total life seconds
const psize = new Float32Array(N);
const pgrav = new Float32Array(N);
const pdrag = new Float32Array(N);
const prot = new Float32Array(N);
const pvrot = new Float32Array(N);
const pkind = new Uint8Array(N);
const pglow = new Uint8Array(N);       // 0/1 additive
const pr = new Uint8Array(N);
const pg = new Uint8Array(N);
const pb = new Uint8Array(N);
const palive = new Uint8Array(N);
const pextra = new Float32Array(N);    // kind-specific extra (ring: r0/r1 packed via size fields below)
const pextra2 = new Float32Array(N);

let liveCount = 0;
let cursor = 0; // ring-buffer allocation pointer for O(1) recycle-oldest

function allocParticle() {
  // Find a free slot; if pool full, recycle the oldest (ring cursor).
  let idx = -1;
  if (liveCount < N) {
    // linear scan from cursor for a free slot (bounded, pool mostly dense in practice)
    for (let i = 0; i < N; i++) {
      const c = (cursor + i) % N;
      if (!palive[c]) { idx = c; cursor = (c + 1) % N; break; }
    }
  }
  if (idx === -1) {
    // pool full: recycle oldest/dimmest -> the one at `cursor`
    idx = cursor;
    cursor = (cursor + 1) % N;
    if (!palive[idx]) liveCount++; // shouldn't happen, but guard count consistency
  } else {
    liveCount++;
  }
  palive[idx] = 1;
  return idx;
}

function killParticle(i) {
  if (palive[i]) {
    palive[i] = 0;
    liveCount--;
  }
}

// ---------------------------------------------------------------------------
// Ribbon trail pool (each entry a short-lived point with radius/color)
// ---------------------------------------------------------------------------
const tx = new Float32Array(MAX_TRAILS);
const ty = new Float32Array(MAX_TRAILS);
const tr = new Float32Array(MAX_TRAILS);
const tage = new Float32Array(MAX_TRAILS);
const tlife = new Float32Array(MAX_TRAILS);
const tcr = new Uint8Array(MAX_TRAILS);
const tcg = new Uint8Array(MAX_TRAILS);
const tcb = new Uint8Array(MAX_TRAILS);
const talive = new Uint8Array(MAX_TRAILS);
let trailCursor = 0;
let trailCount = 0;

function allocTrail() {
  let idx = -1;
  if (trailCount < MAX_TRAILS) {
    for (let i = 0; i < MAX_TRAILS; i++) {
      const c = (trailCursor + i) % MAX_TRAILS;
      if (!talive[c]) { idx = c; trailCursor = (c + 1) % MAX_TRAILS; break; }
    }
  }
  if (idx === -1) { idx = trailCursor; trailCursor = (trailCursor + 1) % MAX_TRAILS; }
  else trailCount++;
  talive[idx] = 1;
  return idx;
}

// ---------------------------------------------------------------------------
// Popup text pool
// ---------------------------------------------------------------------------
const popX = new Float32Array(MAX_POPUPS);
const popY = new Float32Array(MAX_POPUPS);
const popVy = new Float32Array(MAX_POPUPS);
const popAge = new Float32Array(MAX_POPUPS);
const popLife = new Float32Array(MAX_POPUPS);
const popSize = new Float32Array(MAX_POPUPS);
const popShake = new Float32Array(MAX_POPUPS);
const popAlive = new Uint8Array(MAX_POPUPS);
const popText = new Array(MAX_POPUPS).fill('');
const popColor = new Array(MAX_POPUPS).fill('#fff');
let popCursor = 0;

function allocPopup() {
  let idx = -1;
  for (let i = 0; i < MAX_POPUPS; i++) {
    const c = (popCursor + i) % MAX_POPUPS;
    if (!popAlive[c]) { idx = c; popCursor = (c + 1) % MAX_POPUPS; break; }
  }
  if (idx === -1) { idx = popCursor; popCursor = (popCursor + 1) % MAX_POPUPS; }
  popAlive[idx] = 1;
  return idx;
}

// ---------------------------------------------------------------------------
// Lightning bolt pool. Each bolt has a fixed-size preallocated point buffer.
// ---------------------------------------------------------------------------
const boltAlive = new Uint8Array(MAX_BOLTS);
const boltAge = new Float32Array(MAX_BOLTS);
const boltLife = new Float32Array(MAX_BOLTS);
const boltColor = new Array(MAX_BOLTS).fill('#8cf');
const boltWidth = new Float32Array(MAX_BOLTS);
// flattened xy buffer: [bolt][segment] x2
const boltPts = new Float32Array(MAX_BOLTS * (BOLT_SEGMENTS + 1) * 2);
let boltCursor = 0;

function allocBolt() {
  let idx = -1;
  for (let i = 0; i < MAX_BOLTS; i++) {
    const c = (boltCursor + i) % MAX_BOLTS;
    if (!boltAlive[c]) { idx = c; boltCursor = (c + 1) % MAX_BOLTS; break; }
  }
  if (idx === -1) { idx = boltCursor; boltCursor = (boltCursor + 1) % MAX_BOLTS; }
  boltAlive[idx] = 1;
  return idx;
}

// ---------------------------------------------------------------------------
// Ribbon pool (FX.ribbon) — continuous per-entity trails keyed by a stable id.
// Fixed-length ring buffer of points per ribbon slot; ids not fed for
// RIBBON_IDLE_EXPIRE seconds are auto-freed. Capped at MAX_RIBBONS.
// ---------------------------------------------------------------------------
const ribbonId = new Array(MAX_RIBBONS).fill(null);
const ribbonActive = new Uint8Array(MAX_RIBBONS);
const ribbonHead = new Int32Array(MAX_RIBBONS);   // next write index (ring)
const ribbonLen = new Int32Array(MAX_RIBBONS);    // valid points so far (<=RIBBON_PTS)
const ribbonIdle = new Float32Array(MAX_RIBBONS); // seconds since last fed
const ribbonPX = new Float32Array(MAX_RIBBONS * RIBBON_PTS);
const ribbonPY = new Float32Array(MAX_RIBBONS * RIBBON_PTS);
const ribbonCR = new Uint8Array(MAX_RIBBONS);
const ribbonCG = new Uint8Array(MAX_RIBBONS);
const ribbonCB = new Uint8Array(MAX_RIBBONS);
const ribbonWidth = new Float32Array(MAX_RIBBONS);
const ribbonGlow = new Float32Array(MAX_RIBBONS);
const ribbonRainbow = new Uint8Array(MAX_RIBBONS);
const ribbonHue = new Float32Array(MAX_RIBBONS);
let ribbonCount = 0;

function findRibbonSlot(id) {
  let free = -1;
  let oldest = -1, oldestAge = -1;
  for (let i = 0; i < MAX_RIBBONS; i++) {
    if (ribbonActive[i] && ribbonId[i] === id) return i;
    if (!ribbonActive[i] && free === -1) free = i;
    if (ribbonActive[i] && ribbonIdle[i] > oldestAge) { oldestAge = ribbonIdle[i]; oldest = i; }
  }
  if (free !== -1) return free;
  return oldest !== -1 ? oldest : 0;
}

// ---------------------------------------------------------------------------
// Bloom pool (FX.bloomPulse) — big soft expanding additive blooms, cheap.
// ---------------------------------------------------------------------------
const bloomActive = new Uint8Array(MAX_BLOOMS);
const bloomX = new Float32Array(MAX_BLOOMS);
const bloomY = new Float32Array(MAX_BLOOMS);
const bloomAge = new Float32Array(MAX_BLOOMS);
const bloomLife = new Float32Array(MAX_BLOOMS);
const bloomR = new Float32Array(MAX_BLOOMS);
const bloomStrength = new Float32Array(MAX_BLOOMS);
const bloomCR = new Uint8Array(MAX_BLOOMS);
const bloomCG = new Uint8Array(MAX_BLOOMS);
const bloomCB = new Uint8Array(MAX_BLOOMS);
let bloomCursor = 0;

function allocBloom() {
  let idx = -1;
  for (let i = 0; i < MAX_BLOOMS; i++) {
    const c = (bloomCursor + i) % MAX_BLOOMS;
    if (!bloomActive[c]) { idx = c; bloomCursor = (c + 1) % MAX_BLOOMS; break; }
  }
  if (idx === -1) { idx = bloomCursor; bloomCursor = (bloomCursor + 1) % MAX_BLOOMS; }
  bloomActive[idx] = 1;
  return idx;
}

// ---------------------------------------------------------------------------
// Palettes — remap the *default* colors emitters fall back to when the
// caller doesn't pass an explicit opts.color. Explicit colors always win.
// ---------------------------------------------------------------------------
const PALETTES = {
  neon: {
    spark: '#8cf', burst: '#8cf', shock: '#ffffff', bloom: '#8f5bff',
    ring: '#bfffe4', flameA: '#ff8a3d', flameB: '#ffd23f',
    aurora: ['#8f5bff', '#3fd0ff', '#ff5fd1'],
  },
  colorblind: { // avoid red/green pairing, favor blue/yellow/white
    spark: '#ffffff', burst: '#f2c14e', shock: '#ffffff', bloom: '#3050ff',
    ring: '#ffffff', flameA: '#f2c14e', flameB: '#ffffff',
    aurora: ['#3050ff', '#f2c14e', '#ffffff'],
  },
  mono: {
    spark: '#ffffff', burst: '#e8e8e8', shock: '#ffffff', bloom: '#cfcfcf',
    ring: '#ffffff', flameA: '#dddddd', flameB: '#ffffff',
    aurora: ['#8a8a8a', '#c9c9c9', '#ffffff'],
  },
};
let currentPaletteName = 'neon';
function pal(key) {
  const p = PALETTES[currentPaletteName] || PALETTES.neon;
  return p[key] !== undefined ? p[key] : PALETTES.neon[key];
}

// ---------------------------------------------------------------------------
// Global juice state
// ---------------------------------------------------------------------------
let intensity = 0.5;
let reducedMotion = false;
let currentTier = 0;          // 0..5 escalation tier (FX.tier)
let manualQuality = 1;        // FX.setQuality(0..1)
let autoQuality = 1;          // adaptive governor output, multiplies with manualQuality
let dtEMA = 1 / 60;
let goodFrameStreak = 0;
let badFrameStreak = 0;
let auroraPhase = 0;
let auroraIntensity = 0.18;   // idle baseline so the backdrop is never a flat wash

/** Effective render quality 0..1 = manual * auto governor. */
function effQuality() { return clamp01(manualQuality * autoQuality); }
/** Escalation multiplier derived from `currentTier` (0..5). */
function tierMul() { return 1 + currentTier * 0.22; }
function tierGlow() { return 1 + currentTier * 0.35; }

let shakeAmt = 0, shakeDur = 0, shakeAge = 0;
const _shakeOffset = { x: 0, y: 0 };
let shakeSeed = Math.random() * 1000;

let flashColor = '#ffffff';
let flashAlpha = 0;
let flashDur = 0;
let flashAge = 0;

let chromaAmt = 0, chromaDur = 0, chromaAge = 0;

let lastBurstStrength = 0; // decays -> feeds timeScaleHint()
let timeHint = 0;

let clockT = 0; // running clock for animations (grain/scanline drift, noise)

// ---------------------------------------------------------------------------
// Color helpers (parse a hex/rgb string once per emitter call, not per frame)
// ---------------------------------------------------------------------------
function parseColor(c) {
  if (typeof c !== 'string') return [255, 255, 255];
  if (c[0] === '#') {
    let h = c.slice(1);
    if (h.length === 3) h = h.split('').map((ch) => ch + ch).join('');
    const num = parseInt(h, 16);
    if (Number.isFinite(num)) {
      return [(num >> 16) & 255, (num >> 8) & 255, num & 255];
    }
    return [255, 255, 255];
  }
  const m = c.match(/rgba?\(([^)]+)\)/);
  if (m) {
    const parts = m[1].split(',').map((s) => parseFloat(s));
    return [safe(parts[0], 255), safe(parts[1], 255), safe(parts[2], 255)];
  }
  return [255, 255, 255];
}

// ---------------------------------------------------------------------------
// Emitters
// ---------------------------------------------------------------------------

/**
 * Spawn a radial burst of particles (mixed dots + shards for brick-break feel).
 * @param {number} x world x
 * @param {number} y world y
 * @param {object} [opts]
 * @param {number} [opts.count=18] particle count (scaled by intensity)
 * @param {string} [opts.color='#8cf'] hex or rgb color
 * @param {number} [opts.speed=260] base outward speed (units/s)
 * @param {number} [opts.spread=Math.PI*2] angular spread in radians
 * @param {number} [opts.dir=0] center direction of spread in radians
 * @param {number} [opts.life=0.6] seconds
 * @param {number} [opts.size=6] base particle size
 * @param {number} [opts.gravity=600] downward accel units/s^2
 * @param {boolean} [opts.glow=true] additive glow rendering
 * @param {boolean} [opts.shards=true] mix in spinning square shards
 */
function burst(x, y, opts) {
  x = safe(x, 0); y = safe(y, 0);
  opts = opts || {};
  const [r, g, b] = parseColor(opts.color || pal('burst'));
  const baseCount = safe(opts.count, 18);
  const q = effQuality();
  // At high tier/intensity the burst becomes a genuine spectacle: more
  // particles, bigger shard mix, tumbling, and — at tier>=2 — secondary
  // sparks + lingering embers. Quality governor scales it back down under load.
  const count = Math.max(1, Math.round(baseCount * (0.4 + 0.9 * intensity) * tierMul() * (0.5 + 0.5 * q)));
  const speed = safe(opts.speed, 260) * (1 + 0.18 * currentTier);
  const spread = safe(opts.spread, Math.PI * 2);
  const dir = safe(opts.dir, 0);
  const life = safe(opts.life, 0.6) * (1 + 0.12 * currentTier);
  const size = safe(opts.size, 6);
  const gravity = safe(opts.gravity, 600);
  const glow = opts.glow !== false;
  const shards = opts.shards !== false;
  const shardChance = shards ? (0.35 + 0.12 * currentTier) : 0;
  const streakChance = 0.08 + 0.08 * currentTier;

  for (let i = 0; i < count; i++) {
    const idx = allocParticle();
    const a = dir + (Math.random() - 0.5) * spread;
    const sp = speed * (0.35 + Math.random() * 0.9);
    px[idx] = x; py[idx] = y;
    pvx[idx] = Math.cos(a) * sp;
    pvy[idx] = Math.sin(a) * sp;
    page[idx] = 0;
    plife[idx] = life * (0.7 + Math.random() * 0.6);
    const roll = Math.random();
    const useShard = roll < shardChance;
    const useStreak = !useShard && roll < shardChance + streakChance;
    pkind[idx] = useShard ? K_SHARD : (useStreak ? K_STREAK : K_DOT);
    psize[idx] = size * (0.6 + Math.random() * 0.8) * (useShard ? (1 + 0.15 * currentTier) : 1);
    pgrav[idx] = gravity;
    pdrag[idx] = useShard ? 0.985 : 0.94;
    prot[idx] = Math.random() * Math.PI * 2;
    // tumbling: higher tiers spin harder and less uniformly (velocity-stretch reads better)
    pvrot[idx] = (Math.random() - 0.5) * (14 + currentTier * 6);
    pglow[idx] = glow ? 1 : 0;
    pr[idx] = r; pg[idx] = g; pb[idx] = b;
  }

  // Secondary sparks + lingering embers at escalated tiers — cheap (bounded
  // count), reuses the same pool, no extra allocation shapes.
  if (currentTier >= 1 && q > 0.35) {
    spark(x, y, opts.color || pal('spark'), Math.round(3 + 3 * currentTier * q));
  }
  if (currentTier >= 2 && q > 0.45) {
    const emberCount = Math.round(3 * currentTier * q);
    for (let i = 0; i < emberCount; i++) {
      const idx = allocParticle();
      const a = Math.random() * Math.PI * 2;
      const sp = 20 + Math.random() * 60;
      px[idx] = x; py[idx] = y;
      pvx[idx] = Math.cos(a) * sp;
      pvy[idx] = Math.sin(a) * sp * 0.6 - 30;
      page[idx] = 0;
      plife[idx] = 0.9 + Math.random() * 0.9;
      pkind[idx] = K_EMBER;
      psize[idx] = 1.5 + Math.random() * 2;
      pgrav[idx] = 40;
      pdrag[idx] = 0.985;
      prot[idx] = 0; pvrot[idx] = 0;
      pglow[idx] = 1;
      pr[idx] = r; pg[idx] = g; pb[idx] = b;
    }
  }
  lastBurstStrength = Math.min(1, lastBurstStrength + count / 40);
}

/**
 * Small directional shower of glowing dot sparks.
 * @param {number} x world x
 * @param {number} y world y
 * @param {string} color hex/rgb color
 * @param {number} n number of sparks
 */
function spark(x, y, color, n) {
  x = safe(x, 0); y = safe(y, 0);
  const q = effQuality();
  const count = Math.max(1, Math.round(safe(n, 8) * (0.5 + 0.8 * intensity) * (0.6 + 0.4 * q)));
  const [r, g, b] = parseColor(color || pal('spark'));
  for (let i = 0; i < count; i++) {
    const idx = allocParticle();
    const a = Math.random() * Math.PI * 2;
    const sp = 120 + Math.random() * 260;
    px[idx] = x; py[idx] = y;
    pvx[idx] = Math.cos(a) * sp;
    pvy[idx] = Math.sin(a) * sp;
    page[idx] = 0;
    plife[idx] = 0.25 + Math.random() * 0.25;
    pkind[idx] = K_STREAK;
    psize[idx] = 3 + Math.random() * 3;
    pgrav[idx] = 300;
    pdrag[idx] = 0.9;
    prot[idx] = a;
    pvrot[idx] = 0;
    pglow[idx] = 1;
    pr[idx] = r; pg[idx] = g; pb[idx] = b;
  }
}

/**
 * Expanding ring shockwave.
 * @param {number} x world x
 * @param {number} y world y
 * @param {object} [opts]
 * @param {string} [opts.color='#fff']
 * @param {number} [opts.r0=10] starting radius
 * @param {number} [opts.r1=140] ending radius
 * @param {number} [opts.life=0.45] seconds
 * @param {number} [opts.width=6] stroke width at start
 */
function shockwave(x, y, opts) {
  x = safe(x, 0); y = safe(y, 0);
  opts = opts || {};
  const [r, g, b] = parseColor(opts.color || pal('shock'));
  const idx = allocParticle();
  px[idx] = x; py[idx] = y;
  pvx[idx] = 0; pvy[idx] = 0;
  page[idx] = 0;
  plife[idx] = safe(opts.life, 0.45);
  pkind[idx] = K_RING;
  psize[idx] = safe(opts.r0, 10);        // start radius stashed in size
  pextra[idx] = safe(opts.r1, 140);       // end radius
  pextra2[idx] = safe(opts.width, 6);     // stroke width
  pgrav[idx] = 0;
  pdrag[idx] = 1;
  pglow[idx] = 1;
  pr[idx] = r; pg[idx] = g; pb[idx] = b;
}

/**
 * Deposit one ribbon-trail segment (e.g. behind a moving ball). Fades over time.
 * @param {number} x world x
 * @param {number} y world y
 * @param {number} r segment radius
 * @param {string} color hex/rgb color
 * @param {object} [opts]
 * @param {number} [opts.life=0.35] seconds to live
 */
function trail(x, y, r, color, opts) {
  x = safe(x, 0); y = safe(y, 0);
  opts = opts || {};
  const idx = allocTrail();
  const [cr, cg, cb] = parseColor(color || '#8cf');
  tx[idx] = x; ty[idx] = y;
  tr[idx] = Math.max(0.5, safe(r, 6)) * (0.6 + 0.7 * intensity);
  tage[idx] = 0;
  tlife[idx] = safe(opts.life, 0.35);
  tcr[idx] = cr; tcg[idx] = cg; tcb[idx] = cb;
}

/**
 * Floating score/label popup text with pop-in scale and optional shake.
 * @param {number} x world x
 * @param {number} y world y
 * @param {string} text label
 * @param {object} [opts]
 * @param {string} [opts.color='#fff']
 * @param {number} [opts.size=32] font size in world units
 * @param {number} [opts.vy=-90] rise speed
 * @param {number} [opts.life=0.9] seconds
 * @param {number} [opts.shake=0] extra jitter amount
 */
function popup(x, y, text, opts) {
  x = safe(x, 0); y = safe(y, 0);
  opts = opts || {};
  const idx = allocPopup();
  popX[idx] = x; popY[idx] = y;
  popVy[idx] = safe(opts.vy, -90);
  popAge[idx] = 0;
  popLife[idx] = safe(opts.life, 0.9);
  popSize[idx] = safe(opts.size, 32) * (0.85 + 0.4 * intensity);
  popShake[idx] = safe(opts.shake, 0);
  popText[idx] = (text === undefined || text === null) ? '' : String(text);
  popColor[idx] = opts.color || '#ffffff';
}

/**
 * Jagged lightning bolt (with light branching) from (x1,y1) to (x2,y2).
 * @param {number} x1
 * @param {number} y1
 * @param {number} x2
 * @param {number} y2
 * @param {object} [opts]
 * @param {string} [opts.color='#9cf']
 * @param {number} [opts.life=0.25]
 * @param {number} [opts.width=4]
 */
function lightning(x1, y1, x2, y2, opts) {
  x1 = safe(x1, 0); y1 = safe(y1, 0); x2 = safe(x2, x1); y2 = safe(y2, y1);
  opts = opts || {};
  const idx = allocBolt();
  boltAge[idx] = 0;
  boltLife[idx] = safe(opts.life, 0.25);
  boltColor[idx] = opts.color || '#9cf';
  boltWidth[idx] = safe(opts.width, 4);
  const base = idx * (BOLT_SEGMENTS + 1) * 2;
  const dx = x2 - x1, dy = y2 - y1;
  const len = Math.max(1, Math.hypot(dx, dy));
  const nx = -dy / len, ny = dx / len;
  for (let i = 0; i <= BOLT_SEGMENTS; i++) {
    const t = i / BOLT_SEGMENTS;
    let jitter = (i === 0 || i === BOLT_SEGMENTS) ? 0 : (Math.random() - 0.5) * 40;
    const cx = x1 + dx * t + nx * jitter;
    const cy = y1 + dy * t + ny * jitter;
    boltPts[base + i * 2] = cx;
    boltPts[base + i * 2 + 1] = cy;
  }
  // spawn a couple sparks at the strike point for extra readability
  spark(x2, y2, opts.color || '#9cf', 4);
}

/**
 * Confetti burst: fluttering square particles with slow fall.
 * @param {number} x world x
 * @param {number} y world y
 * @param {number} n particle count
 */
function confetti(x, y, n) {
  x = safe(x, 0); y = safe(y, 0);
  const count = Math.max(1, Math.round(safe(n, 20) * (0.5 + 0.8 * intensity)));
  const palette = [[255, 90, 160], [120, 220, 255], [255, 220, 90], [140, 255, 140], [200, 140, 255]];
  for (let i = 0; i < count; i++) {
    const idx = allocParticle();
    const [r, g, b] = palette[(Math.random() * palette.length) | 0];
    const a = -Math.PI / 2 + (Math.random() - 0.5) * 1.6;
    const sp = 140 + Math.random() * 220;
    px[idx] = x; py[idx] = y;
    pvx[idx] = Math.cos(a) * sp;
    pvy[idx] = Math.sin(a) * sp;
    page[idx] = 0;
    plife[idx] = 1.2 + Math.random() * 0.8;
    pkind[idx] = K_CONFETTI;
    psize[idx] = 5 + Math.random() * 4;
    pgrav[idx] = 220;
    pdrag[idx] = 0.99;
    prot[idx] = Math.random() * Math.PI * 2;
    pvrot[idx] = (Math.random() - 0.5) * 8;
    pglow[idx] = 0;
    pr[idx] = r; pg[idx] = g; pb[idx] = b;
  }
}

/**
 * Trigger screen shake with decaying noise offset.
 * @param {number} amount world-unit magnitude
 * @param {number} dur seconds
 */
function shake(amount, dur) {
  amount = safe(amount, 0); dur = safe(dur, 0.2);
  if (reducedMotion) amount *= 0.15;
  // combine with existing shake rather than always overwrite, so overlapping
  // hits feel stronger without needing more allocation.
  if (amount > shakeAmt) { shakeAmt = amount; shakeDur = dur; shakeAge = 0; }
  else { shakeDur = Math.max(shakeDur - shakeAge, dur); shakeAge = 0; }
}

/**
 * Full-screen flash.
 * @param {string} color css color
 * @param {number} alpha 0..1 peak alpha
 * @param {number} dur seconds
 */
function flash(color, alpha, dur) {
  flashColor = color || '#ffffff';
  flashAlpha = Math.max(flashAlpha, reducedMotion ? safe(alpha, 0.5) * 0.4 : safe(alpha, 0.5));
  flashDur = safe(dur, 0.25);
  flashAge = 0;
}

/**
 * Cheap fake chromatic aberration pulse applied in drawPost.
 * @param {number} amount pixel-ish offset amount (screen space)
 * @param {number} dur seconds
 */
function chroma(amount, dur) {
  chromaAmt = Math.max(chromaAmt, reducedMotion ? safe(amount, 4) * 0.25 : safe(amount, 4));
  chromaDur = safe(dur, 0.2);
  chromaAge = 0;
}

// ---------------------------------------------------------------------------
// Round-2 emitters: ribbon, aurora backdrop, bloomPulse, beatRing, flame,
// palette/tier/quality control.
// ---------------------------------------------------------------------------

/**
 * Feed one point into a continuous per-entity ribbon trail. Call every frame
 * for each entity you want a trail behind (e.g. `FX.ribbon('ball'+i, x, y, ...)`).
 * Maintains a fixed-length point ring buffer per id; ids not fed for ~0.5s
 * auto-expire. Capped at MAX_RIBBONS (12) — feeding a 13th id evicts the
 * stalest one.
 * @param {string|number} id stable per-entity key
 * @param {number} x world x
 * @param {number} y world y
 * @param {object} [opts]
 * @param {string} [opts.color='#8cf']
 * @param {number} [opts.width=8] ribbon width at the head
 * @param {number} [opts.life=0.5] idle-expiry window in seconds (not per-point life)
 * @param {boolean} [opts.rainbow=false] cycle hue along the ribbon over time
 * @param {number} [opts.hue] fixed hue (0..360) override when rainbow is on
 * @param {number} [opts.glow=1] additive glow strength multiplier
 */
function ribbon(id, x, y, opts) {
  if (id === undefined || id === null) return;
  x = safe(x, 0); y = safe(y, 0);
  opts = opts || {};
  const idx = findRibbonSlot(id);
  const isNew = !(ribbonActive[idx] && ribbonId[idx] === id);
  if (isNew) {
    ribbonId[idx] = id;
    ribbonActive[idx] = 1;
    ribbonHead[idx] = 0;
    ribbonLen[idx] = 0;
  }
  ribbonIdle[idx] = 0;
  const [r, g, b] = parseColor(opts.color || pal('spark'));
  ribbonCR[idx] = r; ribbonCG[idx] = g; ribbonCB[idx] = b;
  ribbonWidth[idx] = safe(opts.width, 8);
  ribbonGlow[idx] = safe(opts.glow, 1);
  ribbonRainbow[idx] = opts.rainbow ? 1 : 0;
  ribbonHue[idx] = safe(opts.hue, (clockT * 120) % 360);
  const base = idx * RIBBON_PTS;
  const h = ribbonHead[idx];
  ribbonPX[base + h] = x;
  ribbonPY[base + h] = y;
  ribbonHead[idx] = (h + 1) % RIBBON_PTS;
  ribbonLen[idx] = Math.min(RIBBON_PTS, ribbonLen[idx] + 1);
}

/**
 * Advance the animated aurora backdrop energy field. Purely cosmetic — the
 * field also idles/animates on its own via `update(dt)` even if this is
 * never called, so a caller that never calls `aurora()` still sees motion.
 * @param {number} dt seconds
 * @param {number} [intensityArg] 0..~1.5 how energetic the field should look
 */
function aurora(dt, intensityArg) {
  dt = safe(dt, 0);
  if (isNum(intensityArg)) auroraIntensity = Math.max(0, Math.min(1.5, intensityArg));
  auroraPhase += dt * (0.12 + auroraIntensity * 0.5);
}

/**
 * Expanding soft additive bloom — big, cheap, gorgeous. Good for attic
 * escalation pulses / multiplier milestones.
 * @param {number} x world x
 * @param {number} y world y
 * @param {object} [opts]
 * @param {string} [opts.color] defaults to the palette's bloom color
 * @param {number} [opts.r=260] max radius
 * @param {number} [opts.life=0.8] seconds
 * @param {number} [opts.strength=1] peak alpha multiplier
 */
function bloomPulse(x, y, opts) {
  x = safe(x, 0); y = safe(y, 0);
  opts = opts || {};
  const idx = allocBloom();
  const [r, g, b] = parseColor(opts.color || pal('bloom'));
  bloomX[idx] = x; bloomY[idx] = y;
  bloomAge[idx] = 0;
  bloomLife[idx] = safe(opts.life, 0.8);
  bloomR[idx] = safe(opts.r, 260) * (0.85 + 0.3 * currentTier * 0.1);
  bloomStrength[idx] = safe(opts.strength, 1) * (reducedMotion ? 0.4 : 1);
  bloomCR[idx] = r; bloomCG[idx] = g; bloomCB[idx] = b;
}

/**
 * Crisp thin ring for beat-synced pulses (thinner/brighter than `shockwave`).
 * @param {number} x world x
 * @param {number} y world y
 * @param {object} [opts] {color, r0=4, r1=90, life=0.35, width=2}
 */
function beatRing(x, y, opts) {
  opts = opts || {};
  shockwave(x, y, {
    color: opts.color || pal('ring'),
    r0: safe(opts.r0, 4),
    r1: safe(opts.r1, 90),
    life: safe(opts.life, 0.35),
    width: safe(opts.width, 2),
  });
}

/**
 * Rising flame/plume particles (fireball trails, explosive fuses, etc).
 * @param {number} x world x
 * @param {number} y world y
 * @param {object} [opts]
 * @param {string} [opts.color] base color (overrides the two-tone palette flame gradient)
 * @param {number} [opts.dir=-Math.PI/2] plume direction (up by default)
 * @param {number} [opts.count=12] particle count
 * @param {number} [opts.size=7] base particle size
 * @param {number} [opts.life=0.5] seconds
 */
function flame(x, y, opts) {
  x = safe(x, 0); y = safe(y, 0);
  opts = opts || {};
  const q = effQuality();
  const count = Math.max(1, Math.round(safe(opts.count, 12) * (0.5 + 0.7 * intensity) * tierMul() * (0.5 + 0.5 * q)));
  const dir = safe(opts.dir, -Math.PI / 2);
  const size = safe(opts.size, 7);
  const life = safe(opts.life, 0.5);
  const useCustomColor = !!opts.color;
  const [cr, cg, cb] = parseColor(opts.color || pal('flameA'));
  const [wr, wg, wb] = parseColor(pal('flameB'));
  for (let i = 0; i < count; i++) {
    const idx = allocParticle();
    const a = dir + (Math.random() - 0.5) * 0.9;
    const sp = 90 + Math.random() * 180;
    px[idx] = x + (Math.random() - 0.5) * 6;
    py[idx] = y;
    pvx[idx] = Math.cos(a) * sp * 0.4;
    pvy[idx] = Math.sin(a) * sp;
    page[idx] = 0;
    plife[idx] = life * (0.6 + Math.random() * 0.7);
    pkind[idx] = K_FLAME;
    psize[idx] = size * (0.5 + Math.random() * 0.9);
    pgrav[idx] = -80; // rises
    pdrag[idx] = 0.94;
    prot[idx] = 0; pvrot[idx] = 0;
    pglow[idx] = 1;
    // blend base->tip color per-particle (baked at spawn since we don't
    // animate hue per frame): hotter core particles skew toward flameB.
    const mix = useCustomColor ? 0 : Math.random() * Math.random(); // bias toward the base/orange end
    pr[idx] = (cr + (wr - cr) * mix) | 0;
    pg[idx] = (cg + (wg - cg) * mix) | 0;
    pb[idx] = (cb + (wb - cb) * mix) | 0;
  }
}

/**
 * Switch the default-color palette used whenever an emitter is called
 * without an explicit opts.color. Does not touch already-live particles.
 * @param {'neon'|'colorblind'|'mono'} name
 */
function setPalette(name) {
  if (PALETTES[name]) currentPaletteName = name;
}

/**
 * Set the global escalation tier (0..5). Raises glow/count/afterimage
 * across every emitter and the post-process afterimage bloom. Intended to
 * be driven by the attic multiplier / score escalation.
 * @param {number} n
 */
function tier(n) {
  if (!isNum(n)) return;
  currentTier = Math.max(0, Math.min(5, Math.round(n)));
}

/**
 * Manual render-quality dial 0..1 (multiplies with the automatic governor,
 * see `update(dt)`). Lower values reduce spawn multipliers and skip grain/
 * scanlines/afterimage in drawPost.
 * @param {number} q
 */
function setQuality(q) {
  if (!isNum(q)) return;
  manualQuality = clamp01(q);
}

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------
/**
 * Advance all FX simulation by dt seconds. No allocation happens here.
 * @param {number} dt seconds since last frame
 */
function update(dt) {
  if (!isNum(dt) || dt <= 0) dt = 0;
  dt = Math.min(dt, 0.05); // guard against huge tab-switch jumps
  clockT += dt;

  // ---- adaptive quality governor -----------------------------------
  // EMA of frame dt; sustained > ~22ms ramps autoQuality down, sustained
  // comfortably-under-16.7ms ramps it back up. Never allocates.
  if (dt > 0) {
    dtEMA = dtEMA * 0.9 + dt * 0.1;
    if (dtEMA > 0.022) {
      badFrameStreak++; goodFrameStreak = 0;
      if (badFrameStreak > 8) autoQuality = Math.max(0.25, autoQuality - 0.04);
    } else if (dtEMA < 0.015) {
      goodFrameStreak++; badFrameStreak = 0;
      if (goodFrameStreak > 20) autoQuality = Math.min(1, autoQuality + 0.02);
    } else {
      badFrameStreak = 0; goodFrameStreak = 0;
    }
  }

  // ---- aurora idle drift (also nudged explicitly via FX.aurora(dt,i)) ----
  auroraPhase += dt * (0.05 + intensity * 0.25 + currentTier * 0.03);

  // ---- ribbons: expire ids that haven't been fed recently ----------
  for (let i = 0; i < MAX_RIBBONS; i++) {
    if (!ribbonActive[i]) continue;
    ribbonIdle[i] += dt;
    if (ribbonIdle[i] > RIBBON_IDLE_EXPIRE) {
      ribbonActive[i] = 0;
      ribbonId[i] = null;
      ribbonLen[i] = 0;
    }
  }

  // ---- blooms ---------------------------------------------------------
  for (let i = 0; i < MAX_BLOOMS; i++) {
    if (!bloomActive[i]) continue;
    bloomAge[i] += dt;
    if (bloomAge[i] >= bloomLife[i]) bloomActive[i] = 0;
  }

  for (let i = 0; i < N; i++) {
    if (!palive[i]) continue;
    page[i] += dt;
    if (page[i] >= plife[i]) { killParticle(i); continue; }
    if (pkind[i] === K_RING) continue; // rings don't move, just grow (handled in draw)
    pvx[i] *= pdrag[i];
    pvy[i] *= pdrag[i];
    pvy[i] += pgrav[i] * dt;
    px[i] += pvx[i] * dt;
    py[i] += pvy[i] * dt;
    prot[i] += pvrot[i] * dt;
  }

  for (let i = 0; i < MAX_TRAILS; i++) {
    if (!talive[i]) continue;
    tage[i] += dt;
    if (tage[i] >= tlife[i]) { talive[i] = 0; trailCount = Math.max(0, trailCount - 1); }
  }

  for (let i = 0; i < MAX_POPUPS; i++) {
    if (!popAlive[i]) continue;
    popAge[i] += dt;
    if (popAge[i] >= popLife[i]) { popAlive[i] = 0; continue; }
    popY[i] += popVy[i] * dt;
    popVy[i] *= 0.98;
  }

  for (let i = 0; i < MAX_BOLTS; i++) {
    if (!boltAlive[i]) continue;
    boltAge[i] += dt;
    if (boltAge[i] >= boltLife[i]) boltAlive[i] = 0;
  }

  // shake decay
  if (shakeDur > 0) {
    shakeAge += dt;
    const t = shakeAge / shakeDur;
    if (t >= 1) {
      shakeAmt = 0; shakeDur = 0; shakeAge = 0;
      _shakeOffset.x = 0; _shakeOffset.y = 0;
    } else {
      const decay = 1 - t;
      const mag = shakeAmt * decay * decay;
      const n1 = Math.sin(clockT * 47.0 + shakeSeed) * Math.cos(clockT * 13.0);
      const n2 = Math.cos(clockT * 59.0 + shakeSeed * 1.7) * Math.sin(clockT * 17.0);
      _shakeOffset.x = n1 * mag;
      _shakeOffset.y = n2 * mag;
    }
  } else {
    _shakeOffset.x = 0; _shakeOffset.y = 0;
  }

  // flash decay
  if (flashDur > 0) {
    flashAge += dt;
    if (flashAge >= flashDur) { flashAlpha = 0; flashDur = 0; flashAge = 0; }
  }

  // chroma decay
  if (chromaDur > 0) {
    chromaAge += dt;
    if (chromaAge >= chromaDur) { chromaAmt = 0; chromaDur = 0; chromaAge = 0; }
  }

  // time-scale hint decays toward 0
  timeHint = Math.max(timeHint - dt * 1.5, lastBurstStrength);
  lastBurstStrength = Math.max(0, lastBurstStrength - dt * 2.5);
}

// ---------------------------------------------------------------------------
// Drawing helpers
// ---------------------------------------------------------------------------
function colStr(r, g, b, a) {
  return `rgba(${r|0},${g|0},${b|0},${a})`;
}

/**
 * Animated background energy field ("aurora"). Cheap: a handful of
 * linear-gradient band fills, no per-pixel work. Drawn automatically from
 * `drawBelow()` — call `drawBackdrop(ctx)` directly if you want it on its
 * own layer (e.g. behind bricks but in front of a static gradient sky).
 * @param {CanvasRenderingContext2D} ctx world-space ctx
 * @param {number} [w=1000] logical world width
 * @param {number} [h=1500] logical world height
 */
function drawBackdrop(ctx, w, h) {
  if (!ctx) return;
  w = safe(w, 1000); h = safe(h, 1500);
  const q = effQuality();
  const bands = pal('aurora');
  const bandCount = q < 0.4 ? 2 : 3;
  ctx.save();
  ctx.globalCompositeOperation = 'lighter';
  for (let i = 0; i < bandCount; i++) {
    const [r, g, b] = parseColor(bands[i % bands.length]);
    const yBase = h * (0.12 + i * 0.3);
    const amp = (30 + 26 * Math.sin(auroraPhase * 0.4 + i)) * (reducedMotion ? 0.3 : 1);
    const a = (0.045 + 0.05 * auroraIntensity + 0.018 * currentTier) * q;
    const grad = ctx.createLinearGradient(0, yBase - 140, 0, yBase + 140);
    grad.addColorStop(0, 'rgba(0,0,0,0)');
    grad.addColorStop(0.5, colStr(r, g, b, Math.max(0, a)));
    grad.addColorStop(1, 'rgba(0,0,0,0)');
    ctx.save();
    ctx.translate(0, Math.sin(auroraPhase * 0.55 + i * 1.7) * amp);
    ctx.fillStyle = grad;
    ctx.fillRect(0, -140, w, h + 280);
    ctx.restore();
  }
  ctx.restore();
}

/**
 * Draw layers that should sit under game entities (aurora backdrop, ribbon
 * trails, shockwaves, ground glows). Expects a WORLD-space ctx.
 * @param {CanvasRenderingContext2D} ctx
 */
function drawBelow(ctx) {
  if (!ctx) return;
  ctx.save();

  drawBackdrop(ctx, 1000, 1500);

  // bloom pulses (big soft additive circles), drawn before ribbons/rings
  // so they read as background energy rather than foreground sparks.
  ctx.globalCompositeOperation = 'lighter';
  for (let i = 0; i < MAX_BLOOMS; i++) {
    if (!bloomActive[i]) continue;
    const t = bloomAge[i] / bloomLife[i];
    const a = (1 - t) * (1 - t) * 0.5 * bloomStrength[i];
    if (a <= 0.003) continue;
    const rad = Math.max(1, bloomR[i] * (0.25 + 0.85 * t));
    const grad = ctx.createRadialGradient(bloomX[i], bloomY[i], 0, bloomX[i], bloomY[i], rad);
    grad.addColorStop(0, colStr(bloomCR[i], bloomCG[i], bloomCB[i], a));
    grad.addColorStop(1, 'rgba(0,0,0,0)');
    ctx.fillStyle = grad;
    ctx.beginPath();
    ctx.arc(bloomX[i], bloomY[i], rad, 0, Math.PI * 2);
    ctx.fill();
  }

  // continuous per-entity ribbons — smooth tapered polyline through the
  // fixed point buffer, additive, optional rainbow hue cycling.
  for (let i = 0; i < MAX_RIBBONS; i++) {
    if (!ribbonActive[i] || ribbonLen[i] < 2) continue;
    const base = i * RIBBON_PTS;
    const n = ribbonLen[i];
    const startIdx = (ribbonHead[i] - n + RIBBON_PTS) % RIBBON_PTS;
    const glow = ribbonGlow[i];
    for (let s = 1; s < n; s++) {
      const t = s / (n - 1); // 0 at tail, 1 at head (newest)
      const a = t * t * 0.6 * glow;
      if (a <= 0.004) continue;
      const i0 = (startIdx + s - 1) % RIBBON_PTS;
      const i1 = (startIdx + s) % RIBBON_PTS;
      let r = ribbonCR[i], g = ribbonCG[i], b = ribbonCB[i];
      if (ribbonRainbow[i]) {
        const hue = (ribbonHue[i] + t * 90 + clockT * 90) % 360;
        [r, g, b] = hslToRgb(hue, 0.85, 0.6);
      }
      ctx.strokeStyle = colStr(r, g, b, a);
      ctx.lineWidth = Math.max(0.5, ribbonWidth[i] * (0.25 + 0.75 * t));
      ctx.lineCap = 'round';
      ctx.beginPath();
      ctx.moveTo(ribbonPX[base + i0], ribbonPY[base + i0]);
      ctx.lineTo(ribbonPX[base + i1], ribbonPY[base + i1]);
      ctx.stroke();
    }
  }

  // ribbon trail (fading circles), additive
  for (let i = 0; i < MAX_TRAILS; i++) {
    if (!talive[i]) continue;
    const t = tage[i] / tlife[i];
    const a = (1 - t) * 0.55;
    if (a <= 0.002) continue;
    ctx.fillStyle = colStr(tcr[i], tcg[i], tcb[i], a);
    ctx.beginPath();
    ctx.arc(tx[i], ty[i], tr[i] * (1 - t * 0.4), 0, Math.PI * 2);
    ctx.fill();
  }

  // shockwave rings, additive
  for (let i = 0; i < N; i++) {
    if (!palive[i] || pkind[i] !== K_RING) continue;
    const t = page[i] / plife[i];
    const rad = psize[i] + (pextra[i] - psize[i]) * t;
    const a = (1 - t) * 0.8;
    if (a <= 0.002) continue;
    ctx.strokeStyle = colStr(pr[i], pg[i], pb[i], a);
    ctx.lineWidth = Math.max(0.5, pextra2[i] * (1 - t));
    ctx.beginPath();
    ctx.arc(px[i], py[i], Math.max(0.1, rad), 0, Math.PI * 2);
    ctx.stroke();
  }

  ctx.restore();
}

function hslToRgb(h, s, l) {
  h = ((h % 360) + 360) % 360;
  const c = (1 - Math.abs(2 * l - 1)) * s;
  const x = c * (1 - Math.abs((h / 60) % 2 - 1));
  const m = l - c / 2;
  let r = 0, g = 0, b = 0;
  if (h < 60) { r = c; g = x; } else if (h < 120) { r = x; g = c; }
  else if (h < 180) { g = c; b = x; } else if (h < 240) { g = x; b = c; }
  else if (h < 300) { r = x; b = c; } else { r = c; b = x; }
  return [Math.round((r + m) * 255), Math.round((g + m) * 255), Math.round((b + m) * 255)];
}


/**
 * Draw layers that sit above game entities (sparks, shards, smoke,
 * confetti, lightning, popups). Expects a WORLD-space ctx.
 * @param {CanvasRenderingContext2D} ctx
 */
function drawAbove(ctx) {
  if (!ctx) return;
  ctx.save();

  // additive kinds first: dots + streaks + flame + embers
  ctx.globalCompositeOperation = 'lighter';
  for (let i = 0; i < N; i++) {
    if (!palive[i]) continue;
    const k = pkind[i];
    if (k !== K_DOT && k !== K_STREAK && k !== K_FLAME && k !== K_EMBER) continue;
    const t = page[i] / plife[i];
    const a = (1 - t);
    if (a <= 0.003) continue;
    if (k === K_DOT) {
      const s = psize[i] * (1 - t * 0.5);
      ctx.fillStyle = colStr(pr[i], pg[i], pb[i], a);
      ctx.beginPath();
      ctx.arc(px[i], py[i], Math.max(0.1, s), 0, Math.PI * 2);
      ctx.fill();
    } else if (k === K_FLAME) {
      // rising plume: elongated vertically, flickers, shrinks near tip
      const flicker = 0.7 + 0.3 * Math.sin(page[i] * 40 + px[i]);
      const s = psize[i] * (1 - t * 0.6) * flicker;
      ctx.save();
      ctx.translate(px[i], py[i]);
      ctx.scale(1, 1.3);
      ctx.fillStyle = colStr(pr[i], pg[i], pb[i], a * 0.55);
      ctx.beginPath();
      ctx.arc(0, 0, Math.max(0.1, s), 0, Math.PI * 2);
      ctx.fill();
      ctx.restore();
    } else if (k === K_EMBER) {
      // lingering ember: tiny, slow twinkle
      const tw = 0.5 + 0.5 * Math.sin(page[i] * 9 + prot[i] + px[i] * 0.01);
      ctx.fillStyle = colStr(pr[i], pg[i], pb[i], a * tw * 0.8);
      ctx.beginPath();
      ctx.arc(px[i], py[i], Math.max(0.1, psize[i] * (0.6 + 0.4 * tw)), 0, Math.PI * 2);
      ctx.fill();
    } else {
      // streak: stretch along velocity
      const speed = Math.hypot(pvx[i], pvy[i]);
      const len = Math.min(60, speed * 0.05) + psize[i];
      const ang = Math.atan2(pvy[i], pvx[i]);
      ctx.strokeStyle = colStr(pr[i], pg[i], pb[i], a);
      ctx.lineWidth = Math.max(0.5, psize[i] * 0.5);
      ctx.beginPath();
      ctx.moveTo(px[i], py[i]);
      ctx.lineTo(px[i] - Math.cos(ang) * len, py[i] - Math.sin(ang) * len);
      ctx.stroke();
    }
  }

  // lightning bolts, additive
  for (let i = 0; i < MAX_BOLTS; i++) {
    if (!boltAlive[i]) continue;
    const t = boltAge[i] / boltLife[i];
    const a = (1 - t);
    if (a <= 0.01) continue;
    const [r, g, b] = parseColor(boltColor[i]);
    const base = i * (BOLT_SEGMENTS + 1) * 2;
    ctx.strokeStyle = colStr(r, g, b, a);
    ctx.lineWidth = Math.max(0.5, boltWidth[i] * a + 0.5);
    ctx.beginPath();
    ctx.moveTo(boltPts[base], boltPts[base + 1]);
    for (let s = 1; s <= BOLT_SEGMENTS; s++) {
      ctx.lineTo(boltPts[base + s * 2], boltPts[base + s * 2 + 1]);
    }
    ctx.stroke();
    // bright core
    ctx.strokeStyle = colStr(255, 255, 255, a * 0.6);
    ctx.lineWidth = Math.max(0.5, boltWidth[i] * 0.35);
    ctx.stroke();
  }

  // non-additive shapes: shards, confetti
  ctx.globalCompositeOperation = 'source-over';
  for (let i = 0; i < N; i++) {
    if (!palive[i]) continue;
    const k = pkind[i];
    if (k !== K_SHARD && k !== K_CONFETTI && k !== K_SMOKE) continue;
    const t = page[i] / plife[i];
    const a = (1 - t);
    if (a <= 0.003) continue;
    ctx.save();
    ctx.translate(px[i], py[i]);
    ctx.rotate(prot[i]);
    if (k === K_SMOKE) {
      ctx.globalCompositeOperation = 'lighter';
      ctx.fillStyle = colStr(pr[i], pg[i], pb[i], a * 0.25);
      ctx.beginPath();
      ctx.arc(0, 0, psize[i] * (1 + t * 1.5), 0, Math.PI * 2);
      ctx.fill();
    } else {
      const flutter = k === K_CONFETTI ? Math.sin(page[i] * 10 + prot[i]) : 1;
      const s = psize[i];
      ctx.fillStyle = colStr(pr[i], pg[i], pb[i], a);
      ctx.fillRect(-s * 0.5 * Math.abs(flutter) - 0.5, -s * 0.5, s * Math.max(0.15, Math.abs(flutter)) + 1, s);
    }
    ctx.restore();
  }

  // popups (floating text)
  ctx.globalCompositeOperation = 'source-over';
  ctx.textAlign = 'center';
  ctx.textBaseline = 'middle';
  for (let i = 0; i < MAX_POPUPS; i++) {
    if (!popAlive[i]) continue;
    const t = popAge[i] / popLife[i];
    const popIn = Math.min(1, popAge[i] / 0.12);
    const scale = t < 1 ? (0.7 + 0.3 * (1 - Math.pow(1 - popIn, 3))) * (1 + (1 - popIn) * 0.4) : 1;
    const a = t < 0.75 ? 1 : 1 - (t - 0.75) / 0.25;
    if (a <= 0.01) continue;
    let jx = 0, jy = 0;
    if (popShake[i] > 0) {
      jx = (Math.random() - 0.5) * popShake[i] * (1 - t);
      jy = (Math.random() - 0.5) * popShake[i] * (1 - t);
    }
    ctx.save();
    ctx.translate(popX[i] + jx, popY[i] + jy);
    ctx.scale(scale, scale);
    ctx.font = `bold ${popSize[i]}px system-ui, sans-serif`;
    ctx.fillStyle = colStr(0, 0, 0, a * 0.5);
    ctx.fillText(popText[i], 2, 3);
    const [r, g, b] = parseColor(popColor[i]);
    ctx.fillStyle = colStr(r, g, b, a);
    ctx.fillText(popText[i], 0, 0);
    ctx.restore();
  }

  ctx.restore();
}

/**
 * Draw full-screen post effects: flash, vignette, scanlines/grain and the
 * fake chromatic-aberration split. Expects a SCREEN-space ctx (raw pixels).
 * @param {CanvasRenderingContext2D} ctx
 * @param {number} w canvas width in pixels
 * @param {number} h canvas height in pixels
 */
function drawPost(ctx, w, h) {
  if (!ctx) return;
  w = safe(w, 0); h = safe(h, 0);
  if (w <= 0 || h <= 0) return;
  ctx.save();
  const q = effQuality();

  // Cheap fake chromatic aberration: draw the flash/vignette content is not
  // available here (we don't have the frame as a texture without another
  // canvas), so instead we do a lightweight RGB-split overlay using thin
  // translucent bands near the edges that read as "aberration" without
  // re-drawing the scene. This avoids getImageData / filter costs entirely.
  if (chromaAmt > 0.01) {
    const amt = chromaAmt * (0.4 + 0.6 * intensity);
    ctx.globalCompositeOperation = 'lighter';
    ctx.fillStyle = 'rgba(255,0,60,0.05)';
    ctx.fillRect(-amt, 0, w, h);
    ctx.fillStyle = 'rgba(0,255,255,0.05)';
    ctx.fillRect(amt, 0, w, h);
    ctx.globalCompositeOperation = 'source-over';
  }

  // vignette (cheap radial gradient, cached-ish since w/h rarely change)
  const vg = ctx.createRadialGradient(w / 2, h / 2, Math.min(w, h) * 0.35, w / 2, h / 2, Math.max(w, h) * 0.75);
  vg.addColorStop(0, 'rgba(0,0,0,0)');
  vg.addColorStop(1, 'rgba(0,0,0,0.45)');
  ctx.fillStyle = vg;
  ctx.fillRect(0, 0, w, h);

  // Escalation "afterimage" bloom — a couple of cheap additive radial
  // gradients whose alpha rides the tier + recent-burst-strength. Skipped
  // entirely under quality pressure (governor) since it's the least
  // gameplay-critical layer.
  if (currentTier >= 2 && q > 0.4 && !reducedMotion) {
    const glowA = (0.035 * currentTier) * q * (0.4 + 0.6 * lastBurstStrength);
    if (glowA > 0.004) {
      ctx.save();
      ctx.globalCompositeOperation = 'lighter';
      const [r, g, b] = parseColor(pal('bloom'));
      const g2 = ctx.createRadialGradient(w / 2, h * 0.32, 0, w / 2, h * 0.32, Math.max(w, h) * 0.7);
      g2.addColorStop(0, colStr(r, g, b, glowA));
      g2.addColorStop(1, 'rgba(0,0,0,0)');
      ctx.fillStyle = g2;
      ctx.fillRect(0, 0, w, h);
      ctx.restore();
    }
  }

  // subtle scanlines (skipped when quality is low — cheapest thing to cut)
  if (q > 0.5) {
    ctx.globalAlpha = 0.05;
    ctx.fillStyle = '#000';
    const step = 3;
    for (let y = 0; y < h; y += step * 2) {
      ctx.fillRect(0, y, w, step);
    }
    ctx.globalAlpha = 1;
  }

  // grain (a handful of random dots, cheap, not per-pixel; skipped at low quality)
  if (q > 0.55) {
    ctx.globalAlpha = 0.04;
    ctx.fillStyle = '#fff';
    for (let i = 0; i < 40; i++) {
      const gx = (Math.sin(i * 12.9898 + clockT * 60) * 43758.5453) % 1;
      const gy = (Math.sin(i * 78.233 + clockT * 37) * 12345.678) % 1;
      const rx = ((gx < 0 ? -gx : gx)) * w;
      const ry = ((gy < 0 ? -gy : gy)) * h;
      ctx.fillRect(rx, ry, 1.5, 1.5);
    }
    ctx.globalAlpha = 1;
  }

  // full-screen flash
  if (flashAlpha > 0.001 && flashDur > 0) {
    const t = flashAge / flashDur;
    const a = flashAlpha * (1 - t);
    ctx.fillStyle = flashColor;
    ctx.globalAlpha = Math.max(0, a);
    ctx.fillRect(0, 0, w, h);
    ctx.globalAlpha = 1;
  }

  ctx.restore();
}

// ---------------------------------------------------------------------------
// Misc control API
// ---------------------------------------------------------------------------

/** Clear all particles, trails, popups, bolts, ribbons, blooms and screen effects. */
function reset() {
  for (let i = 0; i < N; i++) palive[i] = 0;
  liveCount = 0; cursor = 0;
  for (let i = 0; i < MAX_TRAILS; i++) talive[i] = 0;
  trailCount = 0; trailCursor = 0;
  for (let i = 0; i < MAX_POPUPS; i++) popAlive[i] = 0;
  popCursor = 0;
  for (let i = 0; i < MAX_BOLTS; i++) boltAlive[i] = 0;
  boltCursor = 0;
  for (let i = 0; i < MAX_RIBBONS; i++) { ribbonActive[i] = 0; ribbonId[i] = null; ribbonLen[i] = 0; }
  for (let i = 0; i < MAX_BLOOMS; i++) bloomActive[i] = 0;
  bloomCursor = 0;
  shakeAmt = 0; shakeDur = 0; shakeAge = 0;
  _shakeOffset.x = 0; _shakeOffset.y = 0;
  flashAlpha = 0; flashDur = 0; flashAge = 0;
  chromaAmt = 0; chromaDur = 0; chromaAge = 0;
  lastBurstStrength = 0; timeHint = 0;
}

/**
 * Set global juice intensity 0..1 (typically driven by score/multiplier).
 * Scales particle counts, sizes, popup scale and trail length in emitters.
 * @param {number} v
 */
function setIntensity(v) {
  if (!isNum(v)) return;
  intensity = Math.max(0, Math.min(1, v));
}

/**
 * Cut shake/flash/chroma intensity for accessibility (reduced motion).
 * @param {boolean} v
 */
function setReducedMotion(v) {
  reducedMotion = !!v;
}

/** Suggested 0..1 slow-mo hint based on recent burst intensity. FX never mutates time itself. */
function timeScaleHint() {
  return Math.max(0, Math.min(1, timeHint));
}

export const FX = {
  reset,
  update,
  drawBelow,
  drawAbove,
  drawPost,
  drawBackdrop,
  burst,
  spark,
  shockwave,
  trail,
  popup,
  lightning,
  confetti,
  shake,
  flash,
  chroma,
  ribbon,
  aurora,
  bloomPulse,
  beatRing,
  flame,
  setPalette,
  tier,
  setQuality,
  timeScaleHint,
  setIntensity,
  setReducedMotion,
  get shakeOffset() { return _shakeOffset; },
  get count() { return liveCount + trailCount; },
  get quality() { return effQuality(); },
};

export default FX;
