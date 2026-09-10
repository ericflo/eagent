// SKYBREAK — small shared helpers

const TAU = Math.PI * 2;

const clamp = (v, lo, hi) => (v < lo ? lo : v > hi ? hi : v);
const lerp = (a, b, t) => a + (b - a) * t;
const rand = (lo, hi) => lo + Math.random() * (hi - lo);
const randInt = (lo, hi) => Math.floor(rand(lo, hi + 1));
const pick = (arr) => arr[(Math.random() * arr.length) | 0];

function weightedPick(table) {
  let total = 0;
  for (const k in table) total += table[k];
  let r = Math.random() * total;
  for (const k in table) {
    r -= table[k];
    if (r <= 0) return k;
  }
  return Object.keys(table)[0];
}

// Shortest signed difference between two angles
function angleDiff(a, b) {
  let d = (a - b) % TAU;
  if (d > Math.PI) d -= TAU;
  if (d < -Math.PI) d += TAU;
  return d;
}

// Compute the reflected velocity after bouncing a circle moving along (vx,vy)
// off a surface with inward normal (nx, ny). Mutates and returns [vx, vy].
function reflect(vx, vy, nx, ny) {
  const dot = vx * nx + vy * ny;
  if (dot >= 0) return [vx, vy]; // already moving away
  return [vx - 2 * dot * nx, vy - 2 * dot * ny];
}

// Format a score with thousands separators
function fmtScore(n) {
  return Math.round(n).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',');
}

// Persisted progress (unlocked levels, best scores, settings)
const Store = {
  key: 'skybreak.v1',
  load() {
    try { return JSON.parse(localStorage.getItem(this.key)) || {}; }
    catch (e) { return {}; }
  },
  save(data) {
    try { localStorage.setItem(this.key, JSON.stringify(data)); } catch (e) { /* private mode */ }
  },
};
