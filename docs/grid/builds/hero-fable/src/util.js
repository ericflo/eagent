// util.js — small pure helpers shared by every module. No DOM here.

export const TAU = Math.PI * 2;

export const clamp = (v, lo, hi) => (v < lo ? lo : v > hi ? hi : v);
export const lerp = (a, b, t) => a + (b - a) * t;
export const inverseLerp = (a, b, v) => (a === b ? 0 : clamp((v - a) / (b - a), 0, 1));
export const sign = (v) => (v < 0 ? -1 : v > 0 ? 1 : 0);
export const len = (x, y) => Math.hypot(x, y);

/** Normalise a 2D vector; returns (0,-1) for a zero vector so balls always move. */
export function normalize(x, y) {
  const l = Math.hypot(x, y);
  if (l < 1e-9) return { x: 0, y: -1 };
  return { x: x / l, y: y / l };
}

/** Move `cur` toward `target` by at most `maxDelta`. */
export const approach = (cur, target, maxDelta) =>
  cur < target ? Math.min(cur + maxDelta, target) : Math.max(cur - maxDelta, target);

// ---- easing ---------------------------------------------------------------
export const easeOutCubic = (t) => 1 - Math.pow(1 - t, 3);
export const easeInCubic = (t) => t * t * t;
export const easeOutQuad = (t) => 1 - (1 - t) * (1 - t);
export const easeInOutQuad = (t) => (t < 0.5 ? 2 * t * t : 1 - Math.pow(-2 * t + 2, 2) / 2);
export const easeOutBack = (t) => {
  const c1 = 1.70158;
  const c3 = c1 + 1;
  return 1 + c3 * Math.pow(t - 1, 3) + c1 * Math.pow(t - 1, 2);
};
export const easeOutElastic = (t) => {
  if (t <= 0) return 0;
  if (t >= 1) return 1;
  const c4 = TAU / 3;
  return Math.pow(2, -10 * t) * Math.sin((t * 10 - 0.75) * c4) + 1;
};

// ---- random ---------------------------------------------------------------
/**
 * Seedable RNG (mulberry32). Returns a function producing floats in [0,1)
 * plus helpers. Deterministic seeds make tests reproducible.
 */
export function createRng(seed = (Date.now() ^ 0x9e3779b9) >>> 0) {
  let s = seed >>> 0;
  const next = () => {
    s = (s + 0x6d2b79f5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
  next.range = (lo, hi) => lo + (hi - lo) * next();
  next.int = (lo, hi) => Math.floor(next.range(lo, hi + 1));
  next.pick = (arr) => arr[Math.floor(next() * arr.length)];
  next.chance = (p) => next() < p;
  next.sign = () => (next() < 0.5 ? -1 : 1);
  return next;
}

// ---- colour ---------------------------------------------------------------
export const hsl = (h, s, l, a = 1) => `hsla(${((h % 360) + 360) % 360},${s}%,${l}%,${a})`;

/** Parse "#rrggbb" into [r,g,b]. */
export function hexToRgb(hex) {
  const n = parseInt(hex.replace('#', ''), 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}
export const rgba = (rgb, a = 1) => `rgba(${rgb[0]},${rgb[1]},${rgb[2]},${a})`;

/** Blend two hex colours (t=0 → a, t=1 → b) returning an rgb triple. */
export function mixHex(a, b, t) {
  const ca = hexToRgb(a);
  const cb = hexToRgb(b);
  return [0, 1, 2].map((i) => Math.round(lerp(ca[i], cb[i], t)));
}

/** Format an integer with thin group separators for the HUD. */
export function formatScore(n) {
  return Math.floor(n).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',');
}

let nextId = 1;
export const uid = () => nextId++;
