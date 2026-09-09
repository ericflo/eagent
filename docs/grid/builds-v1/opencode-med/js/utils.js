// ---------------------------------------------------------------------------
// utils.js — small helpers
// ---------------------------------------------------------------------------

export const TAU = Math.PI * 2;

export const clamp = (v, a, b) => (v < a ? a : v > b ? b : v);
export const lerp = (a, b, t) => a + (b - a) * t;

export const rand = (a = 1, b) => (b === undefined ? Math.random() * a : a + Math.random() * (b - a));
export const randi = (a, b) => Math.floor(rand(a, b + 1));
export const pick = (arr) => arr[(Math.random() * arr.length) | 0];

export const hypot = (x, y) => Math.sqrt(x * x + y * y);

export const easeOutCubic = (t) => 1 - Math.pow(1 - t, 3);
export const easeOutBack = (t) => 1 + 2.7 * Math.pow(t - 1, 3) + 1.7 * Math.pow(t - 1, 2);

export function fmt(n) {
  return n.toLocaleString('en-US');
}

// Deterministic pseudo-random for star field etc.
export function mulberry(seed) {
  let a = seed >>> 0;
  return function () {
    a |= 0; a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// Circle vs axis-aligned rect: returns null or {nx, ny, depth, px, py}
export function circleRect(cx, cy, r, rx, ry, rw, rh) {
  const px = clamp(cx, rx, rx + rw);
  const py = clamp(cy, ry, ry + rh);
  const dx = cx - px;
  const dy = cy - py;
  const d2 = dx * dx + dy * dy;
  if (d2 > r * r) return null;
  let nx = 0, ny = 0, depth = r;
  if (d2 > 1e-9) {
    const d = Math.sqrt(d2);
    nx = dx / d; ny = dy / d; depth = r - d;
  } else {
    // center inside rect: push out along smallest axis
    const l = cx - rx, rt = rx + rw - cx, t = cy - ry, b = ry + rh - cy;
    const m = Math.min(l, rt, t, b);
    if (m === l) { nx = -1; depth = r + l; }
    else if (m === rt) { nx = 1; depth = r + rt; }
    else if (m === t) { ny = -1; depth = r + t; }
    else { ny = 1; depth = r + b; }
  }
  return { nx, ny, depth, px, py };
}
