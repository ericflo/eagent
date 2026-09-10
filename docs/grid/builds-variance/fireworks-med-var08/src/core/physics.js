// Fixed-timestep physics + swept collision. Pure math, no DOM.
// Balls are circles; walls/bricks/paddle are AABBs. Fast balls are handled
// by sweeping the circle against each box along the step's motion, plus
// collision substeps — no tunneling even at very high speed.

export function makeBall(x, y, vx, vy, opts = {}) {
  return {
    x, y, vx, vy,
    r: opts.r ?? 7,
    stuck: opts.stuck ?? false,     // resting on the paddle awaiting launch
    stuckOffset: opts.stuckOffset ?? 0,
    fire: opts.fire ?? false,       // fireball: pierces standard bricks
    heavy: opts.heavy ?? false,     // heavy: slower but counts as fast
    hue: opts.hue ?? 190,
    pierceLeft: 0,                  // temporary pierce counter
  };
}

export function effectiveSpeed(ball) {
  const sp = Math.hypot(ball.vx, ball.vy);
  return ball.heavy ? Math.max(sp, 520) : sp;
}

// --- Circle vs AABB sweep -------------------------------------------------
// Returns {hit, nx, ny, t} with surface normal (pointing out of the box)
// and time-of-impact in [0,1], or null if no hit this step.
export function sweepCircleBox(x, y, r, vx, vy, bw, bh, t0 = 0) {
  // Box: [0,bw] x [0,bh] centered coords — caller pre-translates.
  // Expand the box by r and sweep the point.
  const hw = bw / 2 + r;
  const hh = bh / 2 + r;
  const cx = 0, cy = 0; // box center at origin after translation

  const px = x - cx, py = y - cy; // relative to box center
  let tEnter = 0, tExit = 1;
  let nx = 0, ny = 0;

  // X slab
  if (Math.abs(vx) < 1e-9) {
    if (Math.abs(px) >= hw) return null;
  } else {
    const t1 = (-hw - px) / vx;
    const t2 = (hw - px) / vx;
    let tNear = Math.min(t1, t2), tFar = Math.max(t1, t2);
    if (tNear > tEnter) { tEnter = tNear; nx = vx > 0 ? -1 : 1; ny = 0; }
    tExit = Math.min(tExit, tFar);
  }
  // Y slab
  if (Math.abs(vy) < 1e-9) {
    if (Math.abs(py) >= hh) return null;
  } else {
    const t1 = (-hh - py) / vy;
    const t2 = (hh - py) / vy;
    let tNear = Math.min(t1, t2), tFar = Math.max(t1, t2);
    if (tNear > tEnter) { tEnter = tNear; nx = 0; ny = vy > 0 ? -1 : 1; }
    tExit = Math.min(tExit, tFar);
  }

  if (tEnter > tExit || tExit < 0 || tEnter > 1) return null;
  if (tEnter < 0) {
    // Started inside the expanded box: use smallest penetration axis.
    const dx = hw - Math.abs(px), dy = hh - Math.abs(py);
    if (dx < dy) { nx = px >= 0 ? 1 : -1; ny = 0; }
    else { nx = 0; ny = py >= 0 ? 1 : -1; }
    return { hit: true, nx, ny, t: Math.max(0, t0) };
  }
  return { hit: true, nx, ny, t: tEnter };
}

// Generic: circle vs AABB given box center + size. Translates into
// sweepCircleBox's frame. Optional callback filter.
export function circleVsRect(ball, rect, dx, dy) {
  // rect: {x, y, w, h} where x,y is CENTER.
  const relX = ball.x - rect.x;
  const relY = ball.y - rect.y;
  const res = sweepCircleBox(relX, relY, ball.r, dx, dy, rect.w, rect.h);
  if (!res) return null;
  return { ...res, x: ball.x + dx * res.t, y: ball.y + dy * res.t };
}

export function reflect(bvx, bvy, nx, ny, restitution = 1) {
  const d = bvx * nx + bvy * ny;
  let rx = bvx - 2 * d * nx;
  let ry = bvy - 2 * d * ny;
  return { vx: rx * restitution, vy: ry * restitution };
}
