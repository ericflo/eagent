'use strict';
/* ============================================================
   Attic Breakout — utils.js
   Math helpers, storage guards, collision primitives and the
   shared CONFIG layout constants (internal 720x1000 portrait).
   ============================================================ */

const TAU = Math.PI * 2;

const clamp = (v, a, b) => (v < a ? a : v > b ? b : v);
const lerp = (a, b, t) => a + (b - a) * t;
const rand = (a = 1, b) => (b === undefined ? Math.random() * a : a + Math.random() * (b - a));
const randInt = (a, b) => Math.floor(rand(a, b + 1));
const pick = (arr) => arr[(Math.random() * arr.length) | 0];
const chance = (p) => Math.random() < p;

/** localStorage wrappers that never throw (private mode / quota). */
function storageGet(key, fallback) {
  try {
    const v = localStorage.getItem(key);
    return v === null ? fallback : JSON.parse(v);
  } catch (e) {
    return fallback;
  }
}
function storageSet(key, val) {
  try { localStorage.setItem(key, JSON.stringify(val)); } catch (e) { /* ignore */ }
}

function fmtScore(n) {
  try { return Math.round(n).toLocaleString('en-US'); } catch (e) { return String(Math.round(n)); }
}

/** Circle vs axis-aligned rect. Returns {nx,ny,pen} pushing the circle
 *  out of the rect along the collision normal, or null if no overlap. */
function circleRect(cx, cy, r, rx, ry, rw, rh) {
  const nx = clamp(cx, rx, rx + rw);
  const ny = clamp(cy, ry, ry + rh);
  const dx = cx - nx, dy = cy - ny;
  const d2 = dx * dx + dy * dy;
  if (d2 > r * r) return null;
  if (d2 > 1e-9) {
    const d = Math.sqrt(d2);
    return { nx: dx / d, ny: dy / d, pen: r - d };
  }
  // Circle center is inside the rect: escape along the shallowest axis.
  const left = cx - rx, right = rx + rw - cx, top = cy - ry, bottom = ry + rh - cy;
  const m = Math.min(left, right, top, bottom);
  if (m === left) return { nx: -1, ny: 0, pen: r + left };
  if (m === right) return { nx: 1, ny: 0, pen: r + right };
  if (m === top) return { nx: 0, ny: -1, pen: r + top };
  return { nx: 0, ny: 1, pen: r + bottom };
}

/**
 * Shared layout + tuning constants. The playfield is a fixed 720x1000
 * portrait stage, uniformly scaled and letterboxed to the real window.
 */
const CONFIG = {
  W: 720,
  H: 1000,

  CEIL_Y: 64,            // ceiling line; balls bounce below this, HUD above
  FIELD_TOP: 170,        // top of the brick field — above this is "the attic"
  ROWS: 9,
  COLS: 12,
  CELL_W: 60,
  CELL_H: 32,
  BRICK_H: 26,
  BRICK_PAD: 3,          // inset so bricks read as individual tiles
  get FIELD_BOTTOM() { return this.FIELD_TOP + this.ROWS * this.CELL_H; }, // 458

  PADDLE_ZONE_TOP: 780,  // paddle center may roam in this band AND full width
  PADDLE_ZONE_BOTTOM: 950,
  PADDLE_W: 140,
  PADDLE_H: 28,
  PADDLE_W_WIDE: 204,
  PADDLE_W_SHRINK: 92,

  BALL_R: 9,
  BALL_R_HEAVY: 13,
  BALL_SPEED_BASE: 500,  // launch speed, +20 per level
  BALL_SPEED_MIN: 430,   // never lets the ball stall
  BALL_SPEED_MAX: 980,

  ARMOR_SPEED: 640,      // armored bricks only break above this impact speed
  ANGLE_BREAK_COS: Math.cos(25 * Math.PI / 180), // straight-on tolerance for angle-lock

  PHYS_STEP: 1 / 120,    // fixed physics substep
  MAX_BALLS: 6,
  DROP_CHANCE: 0.12,

  FEVER_RAMP: 0.45,      // fever multiplier gained per second spent in the attic
  FEVER_TIERS: [0.6, 2.6, 5.2], // attic-seconds needed for FEVER 1 / 2 / 3
  FEVER_DECAY: 0.55,     // fever seconds lost per second while down in the field

  LIVES: 3,
  LEVELS: 6,
};
