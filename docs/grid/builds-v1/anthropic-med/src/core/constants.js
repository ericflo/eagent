// src/core/constants.js
// Attic Breaker — authoritative world-layout constants. See ARCHITECTURE.md.
// Owned by core worker. Pure data, zero imports.

export const W = 1000;

// World HEIGHT is dynamic: on tall/narrow phone viewports we stretch the
// logical world taller (instead of scaling-to-fit and letterboxing) so the
// whole screen is used with no black bars. `H` (and everything derived from
// it) is a mutable ES-module *live binding* — every importer (`import { H }
// from './constants.js'`) always sees the latest value automatically,
// because ES module bindings are live references, not snapshots. The only
// rule for consumers: never copy it into a top-level `const` that outlives
// a single function call (e.g. `const localH = H` cached at import time)
// — always read `H`/`DEATH_Y`/`PADDLE_Y_*` fresh where they're used. This
// file has been audited (and so has every module that imports these) to
// confirm nothing does that.
export let H = 1500;

export const ATTIC_TOP = 40;      // ceiling gap: ball can live between 40 and BRICK_TOP
export const BRICK_TOP = 260;     // top row of bricks starts here (anchored, does not move)
export let DEATH_Y = 1500;        // bottom = death line — derived from H

export let PADDLE_Y_MAX = 1400;   // lowest paddle center Y   — derived from H
export let PADDLE_Y_MIN = 1120;   // highest paddle center Y  — derived from H
export const PADDLE_W = 190;
export const PADDLE_H = 22;

export const BALL_R = 13;
export const BALL_SPEED_BASE = 720;  // units/sec
export const BALL_SPEED_MAX = 2100;

export const LIVES_START = 3;
export const EXTRA_LIFE_SCORE = 50000;

export const FIXED_DT = 1 / 120;
export const MAX_SUBSTEPS = 4;
export const MAX_DT = 0.05; // clamp huge dt spikes (tab-blur etc.)

export const NUM_LEVELS_HAND = 12;

// World-height bounds. The renderer picks an H inside this range so a
// width-fit scale exactly fills the viewport height on phone aspect ratios,
// with no letterboxing. Bricks stay anchored to BRICK_TOP; only the "floor"
// (paddle box + death line) area grows/shrinks.
export const H_MIN = 1400;
export const H_MAX = 2100;

// Margins (in world units, at the default H=1500) that PADDLE_Y_MAX/MIN keep
// relative to the bottom, so paddle travel room scales proportionally with H.
const PADDLE_Y_MAX_MARGIN = 1500 - 1400; // 100
const PADDLE_Y_MIN_MARGIN = 1500 - 1120; // 380

/**
 * Set the live world height, clamped to [H_MIN, H_MAX], and recompute every
 * height-derived constant (DEATH_Y, PADDLE_Y_MAX, PADDLE_Y_MIN). Call this
 * from the renderer on resize, BEFORE computing the world<->screen scale.
 * Safe to call every resize (idempotent for the same h).
 * @param {number} h desired world height in world units
 * @returns {number} the clamped height actually applied
 */
export function setWorldHeight(h) {
  if (!Number.isFinite(h)) return H;
  h = Math.max(H_MIN, Math.min(H_MAX, h));
  H = h;
  DEATH_Y = h;
  PADDLE_Y_MAX = h - PADDLE_Y_MAX_MARGIN;
  PADDLE_Y_MIN = h - PADDLE_Y_MIN_MARGIN;
  return H;
}
