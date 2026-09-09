// utils.js — math helpers, seeded RNG (mulberry32), and small pooling helper.

export function clamp(v, lo, hi) { return v < lo ? lo : (v > hi ? hi : v); }
export function lerp(a, b, t) { return a + (b - a) * t; }
export function rand(lo, hi) { return lo + Math.random() * (hi - lo); }
export function randInt(lo, hi) { return Math.floor(rand(lo, hi + 1)); }

export const TAU = Math.PI * 2;
export const DEG = Math.PI / 180;

export function angleOf(x, y) { return Math.atan2(y, x); }
export function setAngle(vx, vy, a, speed) { return { x: Math.cos(a) * speed, y: Math.sin(a) * speed }; }
export function norm(vx, vy) {
  const m = Math.hypot(vx, vy);
  return m > 1e-6 ? { x: vx / m, y: vy / m } : { x: 0, y: -1 };
}
export function len(vx, vy) { return Math.hypot(vx, vy); }

// Smallest signed angle difference between two angles (radians, -PI..PI).
export function angleDiff(a, b) {
  let d = (a - b) % TAU;
  if (d > Math.PI) d -= TAU;
  if (d < -Math.PI) d += TAU;
  return d;
}

// mulberry32 — small, fast, seedable PRNG. Returns function() -> [0,1).
export function mulberry32(seed) {
  let a = seed >>> 0;
  return function () {
    a |= 0; a = (a + 0x6D2B79F5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export function rngPick(rng, arr) { return arr[Math.floor(rng() * arr.length)]; }

export function rngShuffle(rng, arr) {
  for (let i = arr.length - 1; i > 0; i--) {
    const j = Math.floor(rng() * (i + 1));
    const t = arr[i]; arr[i] = arr[j]; arr[j] = t;
  }
  return arr;
}

// Free-list pool: pool.get() returns a fresh or recycled object, pool.free(o)
// returns it. Cap is enforced by callers (they skip when pool.free is empty
// and active count is at the cap).
export function makePool(factory) {
  const free = [];
  return {
    get() { return free.length ? free.pop() : factory(); },
    free(o) { free.push(o); },
    get size() { return free.length; }
  };
}

// Rounded-rect path helper (canvas ctx).
export function roundRectPath(ctx, x, y, w, h, r) {
  r = Math.min(r, w / 2, h / 2);
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.lineTo(x + w - r, y);
  ctx.arcTo(x + w, y, x + w, y + r, r);
  ctx.lineTo(x + w, y + h - r);
  ctx.arcTo(x + w, y + h, x + w - r, y + h, r);
  ctx.lineTo(x + r, y + h);
  ctx.arcTo(x, y + h, x, y + h - r, r);
  ctx.lineTo(x, y + r);
  ctx.arcTo(x, y, x + r, y, r);
  ctx.closePath();
}
