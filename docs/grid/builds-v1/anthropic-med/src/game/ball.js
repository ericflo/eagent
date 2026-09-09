// src/game/ball.js — ball entity + swept collision physics.
//
// The collision math here is written as small PURE functions (no `this`,
// no globals, no side effects) so it can be unit-tested in isolation
// (see tests/collision-test.mjs) without booting the whole game/canvas.
//
// Collision strategy: for every fixed physics substep we do a continuous
// (swept) circle-vs-AABB test against the paddle and every alive brick,
// plus an exact swept test against the three static walls. We take the
// EARLIEST collision across the whole substep displacement, resolve it,
// then keep resolving any remaining time in the same substep (up to a
// small iteration cap) — so a ball can never tunnel through geometry no
// matter how fast it is moving, regardless of substep size.

import { BALL_R, BALL_SPEED_BASE, BALL_SPEED_MAX } from '../core/constants.js';
import { clamp, hypot } from '../core/math.js';

export const MIN_VERTICAL_ANGLE = 18 * Math.PI / 180; // clamp near-horizontal trajectories

export function makeBall(x, y, vx, vy, opts = {}) {
  return {
    x, y, vx, vy,
    r: opts.r ?? BALL_R,
    speed: hypot(vx, vy),
    flags: { fire: false, super: false, heavy: false, ghost: false, sticky: false, ...(opts.flags || {}) },
    trailT: 0,
    stuckToPaddle: opts.stuckToPaddle || false,
    stuckOffset: 0,
    id: opts.id ?? (Math.random() * 1e9 | 0),
  };
}

/** Clamp a ball's velocity direction so it's never near-horizontal, keep speed. */
export function clampBallAngle(vx, vy, minAngle = MIN_VERTICAL_ANGLE) {
  const speed = hypot(vx, vy) || 1;
  let ang = Math.atan2(vy, vx); // angle from +x axis
  // distance from horizontal (0 or PI)
  const fromZero = Math.abs(ang);
  const fromPi = Math.abs(Math.PI - Math.abs(ang));
  const nearestHorizDist = Math.min(fromZero, fromPi);
  if (nearestHorizDist < minAngle) {
    const sign = ang >= 0 ? 1 : -1;
    if (fromZero <= fromPi) {
      ang = sign * minAngle;
    } else {
      ang = ang >= 0 ? Math.PI - minAngle : -Math.PI + minAngle;
    }
  }
  return { vx: Math.cos(ang) * speed, vy: Math.sin(ang) * speed };
}

export function clampSpeed(vx, vy, lo = BALL_SPEED_BASE * 0.55, hi = BALL_SPEED_MAX) {
  const s = hypot(vx, vy) || 1;
  const target = clamp(s, lo, hi);
  const k = target / s;
  return { vx: vx * k, vy: vy * k, speed: target };
}

// ---------------------------------------------------------------------
// Swept collision primitives (pure, exported for tests)
// ---------------------------------------------------------------------

/**
 * Sweep a moving circle (p0 -> p1, radius r) against an axis-aligned box.
 * Returns {t, nx, ny} where t in [0,1] is the fraction of the displacement
 * at which contact occurs, and (nx,ny) is the outward surface normal — or
 * null if there is no contact within this displacement.
 *
 * Implemented via the classic slab method against the box inflated by r
 * (a conservative Minkowski-sum approximation: corners become square
 * instead of rounded, which can only trigger collisions slightly EARLY
 * near the diagonal corner region, never late — so it can never miss a
 * collision / allow tunneling).
 */
export function sweepCircleVsRect(x0, y0, x1, y1, r, rect) {
  const ex0 = rect.x - r, ey0 = rect.y - r, ex1 = rect.x + rect.w + r, ey1 = rect.y + rect.h + r;
  const dx = x1 - x0, dy = y1 - y0;

  // already overlapping at the start -> immediate contact
  if (x0 >= ex0 && x0 <= ex1 && y0 >= ey0 && y0 <= ey1) {
    // push out along the shallowest penetration axis
    const cx = clamp(x0, rect.x, rect.x + rect.w);
    const cy = clamp(y0, rect.y, rect.y + rect.h);
    let ndx = x0 - cx, ndy = y0 - cy;
    const nd = hypot(ndx, ndy);
    if (nd > 1e-6) { ndx /= nd; ndy /= nd; } else { ndx = 0; ndy = -1; }
    return { t: 0, nx: ndx, ny: ndy };
  }

  let tmin = 0, tmax = 1, axis = 0;
  if (dx === 0) {
    if (x0 < ex0 || x0 > ex1) return null;
  } else {
    let t1 = (ex0 - x0) / dx, t2 = (ex1 - x0) / dx;
    let sign = 1;
    if (t1 > t2) { const tmp = t1; t1 = t2; t2 = tmp; sign = -1; }
    // The face entered first is the near face relative to travel direction;
    // its OUTWARD normal points opposite the (post-swap) `sign` — i.e. -sign,
    // not +sign. (Bug fix: previously used `sign` directly, which produced
    // an inverted normal on every axis-aligned rect hit. `reflectVel` is
    // sign-invariant so bounces still looked correct, but the tiny
    // post-contact "nudge off the surface" pushed the ball the WRONG way —
    // deeper into the rect instead of out of it — which combined with the
    // ignore-list bug below to allow rare tunneling/embedding.)
    if (t1 > tmin) { tmin = t1; axis = -sign; }
    if (t2 < tmax) tmax = t2;
    if (tmin > tmax) return null;
  }
  let axisIsX = axis !== 0;
  if (dy === 0) {
    if (y0 < ey0 || y0 > ey1) return null;
  } else {
    let t1 = (ey0 - y0) / dy, t2 = (ey1 - y0) / dy;
    let sign = 1;
    if (t1 > t2) { const tmp = t1; t1 = t2; t2 = tmp; sign = -1; }
    if (t1 > tmin) { tmin = t1; axis = -sign; axisIsX = false; }
    if (t2 < tmax) tmax = t2;
    if (tmin > tmax) return null;
  }
  if (tmin < 0 || tmin > 1) return null;
  const nx = axisIsX ? axis : 0;
  const ny = axisIsX ? 0 : axis;
  if (nx === 0 && ny === 0) return null;
  return { t: tmin, nx, ny };
}

/** Sweep against the three static walls (left x=0, right x=W, ceiling y=0). Bottom is the death line, not a wall. */
export function sweepWalls(x0, y0, x1, y1, r, W) {
  let best = null;
  const consider = (t, nx, ny) => { if (t >= 0 && t <= 1 && (!best || t < best.t)) best = { t, nx, ny }; };
  const dx = x1 - x0, dy = y1 - y0;
  if (dx < 0 && x0 - r >= 0) { const t = (r - x0) / dx; consider(t, 1, 0); }
  if (dx > 0 && x0 + r <= W) { const t = (W - r - x0) / dx; consider(t, -1, 0); }
  if (dy < 0 && y0 - r >= 0) { const t = (r - y0) / dy; consider(t, 0, 1); }
  return best;
}

/**
 * Advance a ball through one fixed physics substep with full swept
 * collision resolution against walls, the paddle, and bricks.
 *
 * world = {
 *   W, H,                          world bounds
 *   paddleRect: {x,y,w,h}|null,    axis-aligned paddle box (or null)
 *   onPaddleHit(ball, nx, ny),     called on paddle contact; may mutate ball velocity
 *   bricks: [{x,y,w,h,alive}],     candidate bricks (dead ones are skipped)
 *   onBrickHit(brick, ball, nx, ny) -> 'break'|'bounce'|'pass'
 * }
 * Returns array of event descriptors {type:'wall'|'paddle'|'brick', brick?, verdict?}
 */
export function stepBallPhysics(ball, dt, world) {
  const events = [];
  let remaining = dt;
  let iter = 0;
  const ignoreThisStep = new Set();
  while (remaining > 1e-9 && iter < 12) {
    iter++;
    const x0 = ball.x, y0 = ball.y;
    const x1 = x0 + ball.vx * remaining, y1 = y0 + ball.vy * remaining;

    let best = null; // {t, nx, ny, kind, brick}
    const consider = (hit, kind, brick) => {
      if (!hit) return;
      if (!best || hit.t < best.t) best = { ...hit, kind, brick };
    };

    consider(sweepWalls(x0, y0, x1, y1, ball.r, world.W), 'wall');
    if (world.paddleRect) consider(sweepCircleVsRect(x0, y0, x1, y1, ball.r, world.paddleRect), 'paddle');
    if (world.bricks) {
      for (const b of world.bricks) {
        if (!b.alive || ignoreThisStep.has(b)) continue;
        consider(sweepCircleVsRect(x0, y0, x1, y1, ball.r, b), 'brick', b);
      }
    }

    if (!best) { ball.x = x1; ball.y = y1; break; }

    // move to point of contact
    const t = clamp(best.t, 0, 1);
    ball.x = x0 + (x1 - x0) * t;
    ball.y = y0 + (y1 - y0) * t;

    if (best.kind === 'wall') {
      const r = reflectVel(ball.vx, ball.vy, best.nx, best.ny);
      ball.vx = r.vx; ball.vy = r.vy;
      events.push({ type: 'wall' });
    } else if (best.kind === 'paddle') {
      world.onPaddleHit && world.onPaddleHit(ball, best.nx, best.ny);
      events.push({ type: 'paddle' });
    } else if (best.kind === 'brick') {
      const verdict = world.onBrickHit ? world.onBrickHit(best.brick, ball, best.nx, best.ny) : 'bounce';
      if (verdict === 'pass') {
        ignoreThisStep.add(best.brick);
        events.push({ type: 'brick', brick: best.brick, verdict });
        // do not consume time, do not bounce; continue integrating through
        remaining = remaining * (1 - t);
        continue;
      }
      if (verdict === 'break' && (ball.flags.fire || ball.flags.super || ball.flags.ghost)) {
        // piercing balls plow through what they break
        ignoreThisStep.add(best.brick);
        events.push({ type: 'brick', brick: best.brick, verdict });
        remaining = remaining * (1 - t);
        continue;
      }
      const r = reflectVel(ball.vx, ball.vy, best.nx, best.ny);
      ball.vx = r.vx; ball.vy = r.vy;
      // NOTE: do NOT add this brick to ignoreThisStep here. It must remain
      // a live collision candidate for the rest of the substep — a bounce
      // can send the ball straight into a wall (or another brick) a
      // fraction of an iteration later, which can bounce it right back
      // toward THIS brick within the same substep. Permanently ignoring it
      // after the first bounce (the previous behaviour) let the ball sail
      // through it undetected on the very next iteration -> tunneling /
      // embedding. (root cause of the intermittent collision-test failure)
      events.push({ type: 'brick', brick: best.brick, verdict });
    }

    // nudge off the surface a hair to avoid re-triggering next iteration due to fp error
    ball.x += best.nx * 1e-2;
    ball.y += best.ny * 1e-2;

    remaining = remaining * (1 - t);
  }
  ball.speed = hypot(ball.vx, ball.vy);
  resolvePenetration(ball, world);
  return events;
}

/**
 * Safety-net pass: if, after all substep iterations, the ball's center still
 * ends up overlapping the paddle or a (non-passable) brick — e.g. it started
 * the frame already inside one, or the iteration cap (12) was hit before the
 * substep's time was fully consumed — push it out along the shallowest
 * penetration axis instead of leaving it embedded. This never changes
 * velocity, only position, and only acts when there IS overlap.
 */
function resolvePenetration(ball, world) {
  const rects = [];
  if (world.paddleRect) rects.push(world.paddleRect);
  if (world.bricks) {
    for (const b of world.bricks) if (b.alive) rects.push(b);
  }
  for (const rect of rects) {
    const cx = clamp(ball.x, rect.x, rect.x + rect.w);
    const cy = clamp(ball.y, rect.y, rect.y + rect.h);
    const dx0 = ball.x - cx, dy0 = ball.y - cy;
    const d = hypot(dx0, dy0);
    if (d < ball.r) {
      if (d > 1e-6) {
        // ball center is OUTSIDE the rect but within r of the nearest edge
        // point (cx,cy) — push straight out along that vector.
        const dx = dx0 / d, dy = dy0 / d;
        ball.x = cx + dx * ball.r;
        ball.y = cy + dy * ball.r;
      } else {
        // ball center is exactly ON the boundary or fully INSIDE the rect —
        // (cx,cy) degenerates to the ball's own position in this case, so we
        // can't use it as an edge reference. Instead push straight out to
        // the nearest actual rect edge along the shallowest axis.
        const distLeft = ball.x - rect.x, distRight = (rect.x + rect.w) - ball.x;
        const distTop = ball.y - rect.y, distBottom = (rect.y + rect.h) - ball.y;
        const minAxis = Math.min(distLeft, distRight, distTop, distBottom);
        if (minAxis === distLeft) ball.x = rect.x - ball.r;
        else if (minAxis === distRight) ball.x = rect.x + rect.w + ball.r;
        else if (minAxis === distTop) ball.y = rect.y - ball.r;
        else ball.y = rect.y + rect.h + ball.r;
      }
    }
  }
}

export function reflectVel(vx, vy, nx, ny) {
  const d = vx * nx + vy * ny;
  return { vx: vx - 2 * d * nx, vy: vy - 2 * d * ny };
}

function hsl(h, s, l, a = 1) { return `hsla(${h % 360},${s}%,${l}%,${a})`; }

/**
 * Draw a ball as a layered additive glow orb: soft outer halo, bright core,
 * and a velocity speed-streak, with dramatically distinct looks per flag
 * (fire plume, prismatic super sparkle, heavy shockwave rings, translucent
 * ghost). At high attic intensity the core hue cycles (rainbow).
 * @param {object} opts {atticIntensity=0..1, mult=1, rainbow=bool}
 */
export function drawBall(g, ball, t, opts = {}) {
  const flags = ball.flags || {};
  const atticIntensity = opts.atticIntensity || 0;
  const mult = opts.mult || 1;
  const rainbow = opts.rainbow || mult >= 20 || atticIntensity > 0.85;
  const speed = ball.speed || hypot(ball.vx, ball.vy);
  const speedK = clamp(speed / BALL_SPEED_MAX, 0, 1);

  let baseColor = '#eafcff';
  let hue = 190;
  if (flags.super) { baseColor = '#ffe15e'; hue = 48; }
  else if (flags.fire) { baseColor = '#ff7a3d'; hue = 22; }
  else if (flags.heavy) { baseColor = '#c39bff'; hue = 265; }
  else if (flags.ghost) { baseColor = 'rgba(180,220,255,0.7)'; hue = 200; }

  const color = rainbow ? hsl((t * 220 + ball.x * 0.3) % 360, 95, 62) : baseColor;

  g.save();

  // --- speed streak (behind the ball, additive) ---
  if (speed > 60) {
    const ang = Math.atan2(ball.vy, ball.vx);
    const len = ball.r * (2 + speedK * 5 + atticIntensity * 4);
    g.globalCompositeOperation = 'lighter';
    const sx = ball.x - Math.cos(ang) * len, sy = ball.y - Math.sin(ang) * len;
    const streak = g.createLinearGradient(ball.x, ball.y, sx, sy);
    streak.addColorStop(0, colorWithAlpha(color, 0.55));
    streak.addColorStop(1, colorWithAlpha(color, 0));
    g.strokeStyle = streak;
    g.lineWidth = ball.r * (0.9 + speedK * 0.6);
    g.lineCap = 'round';
    g.beginPath();
    g.moveTo(ball.x, ball.y);
    g.lineTo(sx, sy);
    g.stroke();
  }

  // --- heavy: pulsing shockwave rings ---
  if (flags.heavy) {
    g.globalCompositeOperation = 'lighter';
    for (let i = 0; i < 2; i++) {
      const ph = ((t * 1.4 + i * 0.5) % 1);
      const rr = ball.r * (1.4 + ph * 2.8);
      g.strokeStyle = colorWithAlpha('#c39bff', (1 - ph) * 0.45);
      g.lineWidth = 3 * (1 - ph) + 0.5;
      g.beginPath();
      g.arc(ball.x, ball.y, rr, 0, Math.PI * 2);
      g.stroke();
    }
  }

  // --- fire: small flame plume trailing the ball ---
  if (flags.fire) {
    g.globalCompositeOperation = 'lighter';
    const ang = Math.atan2(-ball.vy, -ball.vx) || Math.PI / 2;
    for (let i = 0; i < 3; i++) {
      const k = i / 3;
      const wobble = Math.sin(t * 18 + i * 2) * 4;
      const fx = ball.x + Math.cos(ang) * (ball.r + k * 22) + Math.cos(ang + Math.PI / 2) * wobble;
      const fy = ball.y + Math.sin(ang) * (ball.r + k * 22) + Math.sin(ang + Math.PI / 2) * wobble;
      const fr = ball.r * (0.9 - k * 0.55);
      const fg = g.createRadialGradient(fx, fy, 0, fx, fy, fr);
      fg.addColorStop(0, `rgba(255,${200 - k * 90 | 0},${60 - k * 40 | 0},${0.55 - k * 0.15})`);
      fg.addColorStop(1, 'rgba(255,80,0,0)');
      g.fillStyle = fg;
      g.beginPath(); g.arc(fx, fy, Math.max(1, fr), 0, Math.PI * 2); g.fill();
    }
  }

  // --- outer halo ---
  g.globalCompositeOperation = 'lighter';
  const haloR = ball.r * (2.4 + speedK * 1.2 + atticIntensity * 1.6);
  const grd = g.createRadialGradient(ball.x, ball.y, 0, ball.x, ball.y, haloR);
  grd.addColorStop(0, colorWithAlpha(color, 0.9));
  grd.addColorStop(0.5, colorWithAlpha(color, 0.28));
  grd.addColorStop(1, 'rgba(0,0,0,0)');
  g.fillStyle = grd;
  g.beginPath();
  g.arc(ball.x, ball.y, haloR, 0, Math.PI * 2);
  g.fill();

  // --- core ---
  const alpha = flags.ghost ? 0.55 : 1;
  g.globalCompositeOperation = flags.ghost ? 'lighter' : 'source-over';
  g.fillStyle = colorWithAlpha(color, alpha);
  g.beginPath();
  g.arc(ball.x, ball.y, ball.r, 0, Math.PI * 2);
  g.fill();
  // bright hot center
  g.globalCompositeOperation = 'lighter';
  g.fillStyle = 'rgba(255,255,255,0.75)';
  g.beginPath();
  g.arc(ball.x, ball.y, ball.r * 0.42, 0, Math.PI * 2);
  g.fill();

  // --- super: prismatic sparkle glints orbiting the ball ---
  if (flags.super) {
    g.globalCompositeOperation = 'lighter';
    for (let i = 0; i < 4; i++) {
      const a = t * 4 + i * (Math.PI / 2);
      const sr = ball.r * 1.9;
      const sx = ball.x + Math.cos(a) * sr, sy = ball.y + Math.sin(a) * sr;
      g.fillStyle = hsl((i * 90 + t * 200) % 360, 100, 70, 0.9);
      g.beginPath();
      g.arc(sx, sy, ball.r * 0.22, 0, Math.PI * 2);
      g.fill();
    }
  }

  // --- ghost: dashed translucent ring outline ---
  if (flags.ghost) {
    g.globalCompositeOperation = 'source-over';
    g.setLineDash([4, 4]);
    g.strokeStyle = 'rgba(200,230,255,0.55)';
    g.lineWidth = 1.4;
    g.beginPath();
    g.arc(ball.x, ball.y, ball.r + 2, 0, Math.PI * 2);
    g.stroke();
    g.setLineDash([]);
  }

  g.restore();
}

function colorWithAlpha(color, a) {
  if (typeof color !== 'string') return `rgba(255,255,255,${a})`;
  if (color[0] === '#') {
    let h = color.slice(1);
    if (h.length === 3) h = h.split('').map((c) => c + c).join('');
    const n = parseInt(h, 16);
    const r = (n >> 16) & 255, gg = (n >> 8) & 255, b = n & 255;
    return `rgba(${r},${gg},${b},${a})`;
  }
  if (color.startsWith('hsla')) {
    return color.replace(/,[^,]+\)$/, `,${a})`);
  }
  const m = color.match(/rgba?\(([^)]+)\)/);
  if (m) {
    const parts = m[1].split(',').map((s) => parseFloat(s));
    return `rgba(${parts[0]},${parts[1]},${parts[2]},${a})`;
  }
  return color;
}
