// util.js — tiny helpers shared across modules. No DOM access except storage helpers.

export const TAU = Math.PI * 2;

export const clamp = (v, lo, hi) => (v < lo ? lo : v > hi ? hi : v);
export const lerp = (a, b, t) => a + (b - a) * t;
export const invLerp = (a, b, v) => (b === a ? 0 : (v - a) / (b - a));
export const sign = (v) => (v < 0 ? -1 : v > 0 ? 1 : 0);
export const len = (x, y) => Math.hypot(x, y);
export const dist = (ax, ay, bx, by) => Math.hypot(bx - ax, by - ay);
export const smoothstep = (t) => {
  t = clamp(t, 0, 1);
  return t * t * (3 - 2 * t);
};

/** Frame-rate independent exponential approach. */
export function damp(current, target, smoothing, dt) {
  return lerp(current, target, 1 - Math.pow(smoothing, dt));
}

/** Move `current` toward `target` by at most `maxDelta`. */
export function approach(current, target, maxDelta) {
  const d = target - current;
  if (Math.abs(d) <= maxDelta) return target;
  return current + Math.sign(d) * maxDelta;
}

export const rand = (a = 1, b) => (b === undefined ? Math.random() * a : a + Math.random() * (b - a));
export const randInt = (a, b) => Math.floor(rand(a, b + 1));
export const pick = (arr) => arr[(Math.random() * arr.length) | 0];
export const chance = (p) => Math.random() < p;

/** Deterministic PRNG (mulberry32) — used for procedural levels. */
export function makeRng(seed) {
  let a = seed >>> 0;
  return function rng() {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export function formatScore(n) {
  return String(Math.floor(n)).replace(/\B(?=(\d{3})+(?!\d))/g, ',');
}

/** Rotate a 2d vector. */
export function rotate(x, y, a) {
  const c = Math.cos(a), s = Math.sin(a);
  return [x * c - y * s, x * s + y * c];
}

/** Normalize a vector to a given length. Returns [x, y]. */
export function setLength(x, y, l) {
  const m = Math.hypot(x, y);
  if (m < 1e-9) return [0, -l];
  return [(x / m) * l, (y / m) * l];
}

export const storage = {
  get(key, fallback) {
    try {
      const v = globalThis.localStorage?.getItem(key);
      return v === null || v === undefined ? fallback : JSON.parse(v);
    } catch (e) {
      return fallback;
    }
  },
  set(key, value) {
    try {
      globalThis.localStorage?.setItem(key, JSON.stringify(value));
    } catch (e) {
      /* ignore */
    }
  },
};

/** Simple hsl string builder. */
export const hsl = (h, s, l, a = 1) =>
  a >= 1 ? `hsl(${h} ${s}% ${l}%)` : `hsl(${h} ${s}% ${l}% / ${a})`;
