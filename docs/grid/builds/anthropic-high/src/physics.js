// physics.js — pure math + swept collision. NO DOM references (Node-importable).
//
// Core primitive: swept circle vs AABB (Minkowski-expanded box + corner ray/circle test).
// Everything else (walls, bricks, paddle) is resolved through `moveBall`, which
// repeatedly advances the ball to the earliest contact along its path so that a
// ball travelling thousands of px/s can never tunnel through anything.

import { clamp, sign } from './util.js';

export const EPS = 1e-6;
/** Distance we keep the ball off surfaces after a resolve, to avoid re-contact jitter. */
export const SKIN = 0.01;

/**
 * Closest point on an AABB to a point.
 */
export function closestPointOnBox(px, py, b) {
  return [clamp(px, b.x, b.x + b.w), clamp(py, b.y, b.y + b.h)];
}

/**
 * Is a circle overlapping an AABB?
 */
export function circleOverlapsBox(cx, cy, r, b) {
  const [qx, qy] = closestPointOnBox(cx, cy, b);
  const dx = cx - qx, dy = cy - qy;
  return dx * dx + dy * dy < r * r - EPS;
}

/**
 * Depenetrate a circle that already overlaps a box.
 * Returns {nx, ny, depth} — push direction and how far, or null when not overlapping.
 */
export function depenetrate(cx, cy, r, b) {
  const [qx, qy] = closestPointOnBox(cx, cy, b);
  let dx = cx - qx, dy = cy - qy;
  const d2 = dx * dx + dy * dy;
  if (d2 > r * r) return null;
  if (d2 > EPS) {
    const d = Math.sqrt(d2);
    return { nx: dx / d, ny: dy / d, depth: r - d };
  }
  // Center is inside the box: push out along the shallowest axis.
  const left = cx - b.x;
  const right = b.x + b.w - cx;
  const top = cy - b.y;
  const bottom = b.y + b.h - cy;
  const m = Math.min(left, right, top, bottom);
  if (m === left) return { nx: -1, ny: 0, depth: left + r };
  if (m === right) return { nx: 1, ny: 0, depth: right + r };
  if (m === top) return { nx: 0, ny: -1, depth: top + r };
  return { nx: 0, ny: 1, depth: bottom + r };
}

/**
 * Push a ball out of a collider that MOVED into it (the paddle). Swept tests assume
 * static geometry, so a fast-rising paddle can end a frame overlapping the ball;
 * call this right after moving both. Returns the contact normal/depth or null.
 */
export function resolveMovingOverlap(ball, box) {
  const pen = depenetrate(ball.x, ball.y, ball.r, box);
  if (!pen) return null;
  ball.x += pen.nx * (pen.depth + SKIN);
  ball.y += pen.ny * (pen.depth + SKIN);
  return pen;
}

/**
 * Swept circle (cx,cy,r) moving by (dx,dy) against AABB b.
 * Returns { t, nx, ny, px, py } with t in [0,1], or null if no hit in this step.
 * If the circle already overlaps at t=0 the result has t=0 and the depenetration normal.
 */
export function sweepCircleBox(cx, cy, r, dx, dy, b) {
  // Already touching?
  const pen = depenetrate(cx, cy, r, b);
  if (pen) {
    // Only report as a collision if we're moving into it (or deeply stuck).
    const into = dx * pen.nx + dy * pen.ny;
    if (into < 0 || pen.depth > r * 0.5) {
      return { t: 0, nx: pen.nx, ny: pen.ny, px: cx, py: cy, depth: pen.depth, overlap: true };
    }
    return null;
  }
  if (dx === 0 && dy === 0) return null;

  // Expanded (Minkowski) box.
  const x0 = b.x - r, y0 = b.y - r, x1 = b.x + b.w + r, y1 = b.y + b.h + r;

  let tmin = 0, tmax = 1;
  // X slab
  if (Math.abs(dx) < EPS) {
    if (cx < x0 || cx > x1) return null;
  } else {
    let t1 = (x0 - cx) / dx, t2 = (x1 - cx) / dx;
    if (t1 > t2) { const tt = t1; t1 = t2; t2 = tt; }
    tmin = Math.max(tmin, t1);
    tmax = Math.min(tmax, t2);
    if (tmin > tmax) return null;
  }
  // Y slab
  if (Math.abs(dy) < EPS) {
    if (cy < y0 || cy > y1) return null;
  } else {
    let t1 = (y0 - cy) / dy, t2 = (y1 - cy) / dy;
    if (t1 > t2) { const tt = t1; t1 = t2; t2 = tt; }
    tmin = Math.max(tmin, t1);
    tmax = Math.min(tmax, t2);
    if (tmin > tmax) return null;
  }
  if (tmin > 1 || tmin < 0) return null;

  const hx = cx + dx * tmin;
  const hy = cy + dy * tmin;

  const inXRange = hx >= b.x && hx <= b.x + b.w;
  const inYRange = hy >= b.y && hy <= b.y + b.h;

  if (inXRange && inYRange) {
    // Shouldn't happen (would mean starting inside), but be safe.
    const p = depenetrate(hx, hy, r, b);
    return p ? { t: tmin, nx: p.nx, ny: p.ny, px: hx, py: hy } : null;
  }
  if (inXRange) {
    const ny = hy < b.y ? -1 : 1;
    return { t: tmin, nx: 0, ny, px: hx, py: hy };
  }
  if (inYRange) {
    const nx = hx < b.x ? -1 : 1;
    return { t: tmin, nx, ny: 0, px: hx, py: hy };
  }
  // Corner region — solve circle-of-radius-r around the nearest corner.
  const cornerX = hx < b.x ? b.x : b.x + b.w;
  const cornerY = hy < b.y ? b.y : b.y + b.h;
  const ct = rayCircle(cx, cy, dx, dy, cornerX, cornerY, r);
  if (ct === null || ct > 1 || ct < 0) return null;
  const px = cx + dx * ct, py = cy + dy * ct;
  const nx = (px - cornerX) / r, ny = (py - cornerY) / r;
  return { t: ct, nx, ny, px, py };
}

/**
 * Earliest intersection of ray (ox,oy)+(t)(dx,dy) with circle (sx,sy,r). t in [0,1] preferred.
 */
export function rayCircle(ox, oy, dx, dy, sx, sy, r) {
  const mx = ox - sx, my = oy - sy;
  const a = dx * dx + dy * dy;
  if (a < EPS) return null;
  const b = 2 * (mx * dx + my * dy);
  const c = mx * mx + my * my - r * r;
  const disc = b * b - 4 * a * c;
  if (disc < 0) return null;
  const sq = Math.sqrt(disc);
  const t0 = (-b - sq) / (2 * a);
  const t1 = (-b + sq) / (2 * a);
  if (t0 >= -EPS) return t0;
  if (t1 >= -EPS) return t1;
  return null;
}

/** Reflect velocity (vx,vy) around unit normal (nx,ny) with restitution. */
export function reflect(vx, vy, nx, ny, restitution = 1) {
  const d = vx * nx + vy * ny;
  return [vx - (1 + restitution) * d * nx, vy - (1 + restitution) * d * ny];
}

/** Speed tiers used by brick conditions and juice systems. */
export const SPEED_TIERS = ['slow', 'normal', 'fast', 'blazing'];
export const TIER_THRESHOLDS = [300, 470, 640]; // < 300 slow, < 470 normal, < 640 fast, else blazing

export function speedTierIndex(speed) {
  if (speed < TIER_THRESHOLDS[0]) return 0;
  if (speed < TIER_THRESHOLDS[1]) return 1;
  if (speed < TIER_THRESHOLDS[2]) return 2;
  return 3;
}
export function speedTier(speed) {
  return SPEED_TIERS[speedTierIndex(speed)];
}

/**
 * Keep the ball from degenerating into a purely horizontal or purely vertical path.
 * `minRatio` is the minimum |vy|/speed (and |vx|/speed) allowed.
 */
export function avoidDegenerate(ball, minRatio = 0.14) {
  const s = Math.hypot(ball.vx, ball.vy);
  if (s < EPS) return;
  if (Math.abs(ball.vy) / s < minRatio) {
    const ny = (sign(ball.vy) || -1) * minRatio * s;
    const nx = sign(ball.vx) * Math.sqrt(Math.max(0, s * s - ny * ny));
    ball.vx = nx || s * 0.98;
    ball.vy = ny;
  }
  if (Math.abs(ball.vx) / s < minRatio * 0.5) {
    const nx = (sign(ball.vx) || 1) * minRatio * 0.5 * s;
    const ny = sign(ball.vy) * Math.sqrt(Math.max(0, s * s - nx * nx));
    ball.vx = nx;
    ball.vy = ny || -s * 0.98;
  }
}

/**
 * Move a ball for `dt` seconds through the world, resolving collisions in time order.
 *
 * world = {
 *   bounds: {left, top, right, bottom},
 *   bricks: iterable of {x,y,w,h, ...} (only "collidable right now" ones),
 *   paddle: {x,y,w,h} | null,
 *   onWall(ball, side, info)            -> void            ('left'|'right'|'top')
 *   onBrick(ball, brick, info)          -> 'destroy'|'reflect'|'pass'
 *   onPaddle(ball, paddle, info)        -> 'reflect'|'stick'|'pass'  (may set ball velocity itself)
 *   onBottom(ball)                      -> void (ball fell out; caller should mark dead)
 * }
 *
 * The ball object needs: x, y, vx, vy, r. Optional: dead (bool).
 */
export function moveBall(ball, dt, world) {
  const bounds = world.bounds;
  let remaining = dt;
  let guard = 0;
  let ignored = null; // things the ball is currently phasing through

  while (remaining > EPS && guard++ < 24) {
    if (ball.dead) return;
    const dx = ball.vx * remaining;
    const dy = ball.vy * remaining;

    let best = null; // {t, nx, ny, kind, ref}

    // ---- Walls (treated as planes; simple analytic time-of-impact) ----
    const consider = (t, nx, ny, kind, ref, hit) => {
      if (t < -EPS || t > 1) return;
      if (!best || t < best.t - 1e-9) {
        best = { t: Math.max(0, t), nx, ny, kind, ref, depth: hit ? hit.depth || 0 : 0 };
      }
    };
    if (dx < 0) {
      const t = (bounds.left + ball.r - ball.x) / dx;
      if (ball.x + dx < bounds.left + ball.r) consider(t, 1, 0, 'wall', 'left');
    } else if (dx > 0) {
      const t = (bounds.right - ball.r - ball.x) / dx;
      if (ball.x + dx > bounds.right - ball.r) consider(t, -1, 0, 'wall', 'right');
    }
    if (dy < 0) {
      const t = (bounds.top + ball.r - ball.y) / dy;
      if (ball.y + dy < bounds.top + ball.r) consider(t, 0, 1, 'wall', 'top');
    }

    // ---- Bricks ----
    if (world.bricks) {
      for (const brick of world.bricks) {
        if (brick.dead) continue;
        if (ignored && ignored.has(brick)) continue;
        const hit = sweepCircleBox(ball.x, ball.y, ball.r, dx, dy, brick);
        if (hit) consider(hit.t, hit.nx, hit.ny, 'brick', brick, hit);
      }
    }

    // ---- Paddle ----
    if (world.paddle && !(ignored && ignored.has(world.paddle))) {
      const hit = sweepCircleBox(ball.x, ball.y, ball.r, dx, dy, world.paddle);
      if (hit) consider(hit.t, hit.nx, hit.ny, 'paddle', world.paddle, hit);
    }

    if (!best) {
      ball.x += dx;
      ball.y += dy;
      break;
    }

    // Advance to the contact point.
    const travel = Math.max(0, best.t);
    ball.x += dx * travel;
    ball.y += dy * travel;
    const usedTime = remaining * travel;
    remaining -= usedTime;

    const info = { nx: best.nx, ny: best.ny, t: best.t, speed: Math.hypot(ball.vx, ball.vy) };
    let phased = false;

    if (best.kind === 'wall') {
      const [nvx, nvy] = reflect(ball.vx, ball.vy, best.nx, best.ny);
      ball.vx = nvx;
      ball.vy = nvy;
      world.onWall?.(ball, best.ref, info);
    } else if (best.kind === 'brick') {
      const result = world.onBrick ? world.onBrick(ball, best.ref, info) : 'reflect';
      if (result === 'destroy' || result === 'destroyBounce') best.ref.dead = true;
      if (result === 'reflect' || result === 'destroyBounce') {
        const [nvx, nvy] = reflect(ball.vx, ball.vy, best.nx, best.ny);
        ball.vx = nvx;
        ball.vy = nvy;
      }
      if (result === 'pass') {
        phased = true;
        (ignored || (ignored = new Set())).add(best.ref);
      }
    } else if (best.kind === 'paddle') {
      const result = world.onPaddle ? world.onPaddle(ball, world.paddle, info) : 'reflect';
      if (result === 'reflect') {
        const [nvx, nvy] = reflect(ball.vx, ball.vy, best.nx, best.ny);
        ball.vx = nvx;
        ball.vy = nvy;
      }
      if (result === 'pass') {
        phased = true;
        (ignored || (ignored = new Set())).add(world.paddle);
      }
      if (result === 'stick') return;
    }

    // Keep a hair of separation from whatever we just bounced off; if we started the
    // step already overlapping, push all the way out so we can't get stuck inside.
    if (!phased) {
      const push = (best.depth || 0) + SKIN;
      ball.x += best.nx * push;
      ball.y += best.ny * push;
    }
  }

  // Hard safety clamp: never let the ball live outside the side/top walls.
  if (ball.x < bounds.left + ball.r) {
    ball.x = bounds.left + ball.r;
    if (ball.vx < 0) ball.vx = -ball.vx;
  }
  if (ball.x > bounds.right - ball.r) {
    ball.x = bounds.right - ball.r;
    if (ball.vx > 0) ball.vx = -ball.vx;
  }
  if (ball.y < bounds.top + ball.r) {
    ball.y = bounds.top + ball.r;
    if (ball.vy < 0) ball.vy = -ball.vy;
  }
  if (ball.y - ball.r > bounds.bottom) {
    world.onBottom?.(ball);
  }
}

/**
 * Paddle bounce model shared by game + tests.
 * Returns [vx, vy] for the new ball velocity.
 *
 * - hit position along the paddle sets the base angle (±maxAngle from vertical)
 * - paddle horizontal velocity adds "english"
 * - paddle vertical velocity adds/removes speed (smash / soft)
 */
export function paddleBounce(ball, paddle, opts = {}) {
  const maxAngle = opts.maxAngle ?? 1.13; // ~65deg from vertical
  const english = opts.english ?? 0.14;
  const smash = opts.smash ?? 0.4;
  const minSpeed = opts.minSpeed ?? 240;
  const maxSpeed = opts.maxSpeed ?? 900;

  const half = paddle.w / 2;
  const cx = paddle.x + half;
  let rel = clamp((ball.x - cx) / half, -1, 1);
  // Slight bias so the very center isn't a perfectly vertical shot.
  let angle = rel * maxAngle;
  angle += clamp((paddle.vx || 0) * english * 0.0016, -0.34, 0.34);
  angle = clamp(angle, -1.32, 1.32);

  let speed = Math.hypot(ball.vx, ball.vy);
  // Effective vertical paddle speed; callers may pass a "recent peak" for forgiveness.
  const pvy = opts.pvy !== undefined ? opts.pvy : (paddle.vy || 0);
  // Moving up (negative vy) => smash; a decent rise also gets a flat kick so the
  // "fast" speed tier (armour bricks) is reliably reachable.
  speed += -pvy * smash;
  if (pvy < -150) speed += 70;
  speed = clamp(speed, minSpeed, maxSpeed);

  const vx = Math.sin(angle) * speed;
  const vy = -Math.cos(angle) * speed;
  return [vx, vy];
}
