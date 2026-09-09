// src/core/math.js — tiny math helpers shared across core modules.
export const TAU = Math.PI * 2;

export function clamp(v, lo, hi) { return v < lo ? lo : v > hi ? hi : v; }
export function lerp(a, b, t) { return a + (b - a) * t; }
export function rand() { return Math.random(); }
export function randRange(lo, hi) { return lo + Math.random() * (hi - lo); }
export function pick(arr) { return arr[(Math.random() * arr.length) | 0]; }
export function hypot(x, y) { return Math.sqrt(x * x + y * y); }
export function angle(x, y) { return Math.atan2(y, x); }
export function dist(x0, y0, x1, y1) { return hypot(x1 - x0, y1 - y0); }
export function angleDelta(a, b) {
  let d = a - b;
  while (d > Math.PI) d -= TAU;
  while (d < -Math.PI) d += TAU;
  return d;
}
// reflect vector (vx,vy) about a surface normal (nx,ny), normal must be unit length.
export function refl(vx, vy, nx, ny) {
  const d = vx * nx + vy * ny;
  return { x: vx - 2 * d * nx, y: vy - 2 * d * ny };
}
export function normalize(x, y) {
  const m = hypot(x, y) || 1;
  return { x: x / m, y: y / m };
}
// exponential smoothing towards a value; k in units of "how much per second"
export function damp(cur, target, k, dt) {
  return lerp(cur, target, 1 - Math.exp(-k * dt));
}
