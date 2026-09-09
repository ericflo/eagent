// physics.js — pure collision & bounce math. No DOM, no canvas.
// Coordinates: x grows right, y grows DOWN (canvas convention). "Up" = negative y.

import { clamp, normalize, sign } from './util.js';

export const FIELD = { w: 720, h: 1280 };

/** Ball speed limits in logical units / second. */
export const SPEED = { min: 520, base: 620, max: 1500 };

/** After any bounce |dir.y| must be >= this so the ball can never trap horizontally. */
export const MIN_DIR_Y = 0.18;

/** Paddle bounce tuning. */
export const PADDLE_BOUNCE = {
  maxAngle: (65 * Math.PI) / 180, // ball leaving from the paddle edge flies 65° from vertical
  vxInfluence: 0.22, // fraction of paddle horizontal velocity mixed into ball direction
  upK: 0.6, // ball.speed += upK * upward paddle speed   (the "power hit")
  downK: 0.3, // ball.speed -= downK * downward paddle speed (the "soft catch")
  powerHitVy: 140, // paddle must be rising faster than this for the FX/sound to fire
};

/**
 * Swept circle vs AABB (box = {x,y,w,h}). The circle at (cx,cy) with radius r moves
 * by (dx,dy) during this step. Returns the earliest contact { t in [0,1], nx, ny }
 * or null. Implemented as a ray against the box expanded by r (slab method); corners
 * are treated square, a tiny over-approximation that is invisible at these sizes and
 * guarantees fast balls never tunnel because the whole path is tested.
 * If the circle already overlaps the box the result is t=0 with the normal of least
 * penetration and `overlap: true` so callers can push the ball out.
 */
export function sweepCircleAABB(cx, cy, r, dx, dy, box) {
  const minX = box.x - r;
  const maxX = box.x + box.w + r;
  const minY = box.y - r;
  const maxY = box.y + box.h + r;

  if (cx > minX && cx < maxX && cy > minY && cy < maxY) {
    const pl = cx - minX;
    const pr = maxX - cx;
    const pt = cy - minY;
    const pb = maxY - cy;
    const m = Math.min(pl, pr, pt, pb);
    if (m === pt) return { t: 0, nx: 0, ny: -1, overlap: true };
    if (m === pb) return { t: 0, nx: 0, ny: 1, overlap: true };
    if (m === pl) return { t: 0, nx: -1, ny: 0, overlap: true };
    return { t: 0, nx: 1, ny: 0, overlap: true };
  }

  let tmin = 0;
  let tmax = 1;
  let nx = 0;
  let ny = 0;

  if (Math.abs(dx) < 1e-12) {
    if (cx <= minX || cx >= maxX) return null;
  } else {
    let t1 = (minX - cx) / dx;
    let t2 = (maxX - cx) / dx;
    let n = -1;
    if (t1 > t2) {
      [t1, t2] = [t2, t1];
      n = 1;
    }
    if (t1 >= tmin) {
      tmin = t1;
      nx = n;
      ny = 0;
    }
    tmax = Math.min(tmax, t2);
    if (tmin > tmax) return null;
  }

  if (Math.abs(dy) < 1e-12) {
    if (cy <= minY || cy >= maxY) return null;
  } else {
    let t1 = (minY - cy) / dy;
    let t2 = (maxY - cy) / dy;
    let n = -1;
    if (t1 > t2) {
      [t1, t2] = [t2, t1];
      n = 1;
    }
    if (t1 >= tmin) {
      tmin = t1;
      nx = 0;
      ny = n;
    }
    tmax = Math.min(tmax, t2);
    if (tmin > tmax) return null;
  }

  if (tmin > 1 || (nx === 0 && ny === 0)) return null;
  if (nx * dx + ny * dy > 0) return null; // moving away from the face we'd "enter"
  return { t: tmin, nx, ny };
}

/** Earliest hit among many boxes. Returns { t, nx, ny, box, index } or null. */
export function earliestHit(cx, cy, r, dx, dy, boxes, filter) {
  let best = null;
  for (let i = 0; i < boxes.length; i++) {
    const box = boxes[i];
    if (filter && !filter(box)) continue;
    // Cheap broad-phase reject using the swept bounding box.
    const pad = r + 1;
    const lo = Math.min(cx, cx + dx) - pad;
    const hi = Math.max(cx, cx + dx) + pad;
    if (box.x > hi || box.x + box.w < lo) continue;
    const loY = Math.min(cy, cy + dy) - pad;
    const hiY = Math.max(cy, cy + dy) + pad;
    if (box.y > hiY || box.y + box.h < loY) continue;
    const h = sweepCircleAABB(cx, cy, r, dx, dy, box);
    if (h && (!best || h.t < best.t)) best = { ...h, box, index: i };
  }
  return best;
}

/** Reflect direction (dx,dy) about unit normal (nx,ny). */
export function reflect(dx, dy, nx, ny) {
  const d = dx * nx + dy * ny;
  return { x: dx - 2 * d * nx, y: dy - 2 * d * ny };
}

/**
 * Guard against horizontal trapping: a ball skimming almost horizontally could
 * bounce between walls forever. Force |dir.y| >= MIN_DIR_Y, preserving the sign
 * (a perfectly horizontal ball is nudged upward) and re-normalise.
 */
export function enforceMinVertical(dir, minY = MIN_DIR_Y) {
  const n = normalize(dir.x, dir.y);
  if (Math.abs(n.y) >= minY) return n;
  const sy = n.y === 0 ? -1 : sign(n.y);
  const sx = n.x === 0 ? 1 : sign(n.x);
  return { x: sx * Math.sqrt(1 - minY * minY), y: sy * minY };
}

/**
 * Paddle bounce (the heart of the 2D-paddle mechanic).
 *  - Horizontal component comes from where the ball hit along the paddle
 *    (offset -1..1 → angle up to maxAngle from vertical), plus a slice of the
 *    paddle's own horizontal velocity for "english".
 *  - Paddle rising at impact ADDS speed (power hit), paddle descending softens.
 * ball = {x, speed}; paddle = {x (center), w, vx, vy}.
 * Returns { dir:{x,y}, speed, powerHit, strength } without mutating inputs.
 */
export function paddleBounce(ball, paddle, tuning = PADDLE_BOUNCE) {
  const half = Math.max(1, paddle.w / 2);
  const offset = clamp((ball.x - paddle.x) / half, -1, 1);
  const angle = offset * tuning.maxAngle;
  const english = ((paddle.vx || 0) * tuning.vxInfluence) / Math.max(1, ball.speed);
  let dir = normalize(Math.sin(angle) + english, -Math.cos(angle));
  if (dir.y > -MIN_DIR_Y) dir = { x: sign(dir.x) || 1, y: -MIN_DIR_Y };
  dir = enforceMinVertical(dir);

  const up = Math.max(0, -(paddle.vy || 0)); // paddle moving up → negative vy
  const down = Math.max(0, paddle.vy || 0);
  let speed = ball.speed + tuning.upK * up - tuning.downK * down;
  speed = clamp(speed, SPEED.min, SPEED.max);
  const powerHit = up >= tuning.powerHitVy;
  const strength = clamp((speed - SPEED.min) / (SPEED.max - SPEED.min), 0, 1);
  return { dir, speed, powerHit, strength };
}

/** Axis-aligned overlap test for boxes {x,y,w,h}. */
export const aabbOverlap = (a, b) =>
  a.x < b.x + b.w && a.x + a.w > b.x && a.y < b.y + b.h && a.y + a.h > b.y;

/** Circle vs box overlap test. */
export function circleBoxOverlap(cx, cy, r, box) {
  const nx = clamp(cx, box.x, box.x + box.w);
  const ny = clamp(cy, box.y, box.y + box.h);
  const dx = cx - nx;
  const dy = cy - ny;
  return dx * dx + dy * dy < r * r;
}
