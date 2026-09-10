// Paddle model + reflection math. Pure, no DOM.
// The paddle lives in a band near the bottom and can move vertically.
// Hitting while moving up adds speed/lift; while moving down it deadens
// and steepens the ball. Hit position steers the reflection angle.

export const PADDLE = {
  w: 104,
  h: 14,
  bandTop: 0.62,   // paddle y ranges from 62%..92% of play height
  bandBottom: 0.92,
  accel: 4200,     // keyboard accel px/s^2
  drag: 9,         // velocity damping
  maxV: 950,
};

export function createPaddle(field) {
  return {
    x: field.width / 2,
    y: field.height * 0.86,
    vx: 0,
    vy: 0,
    w: PADDLE.w,
    targetX: field.width / 2,
    targetY: field.height * 0.86,
    wideT: 0,   // wide-paddle power-up timer
    magnetT: 0, // magnet/catch timer
  };
}

// Integrate paddle. keys: {left,right,up,down} booleans.
export function updatePaddle(p, input, dt, field) {
  const ax = (input.right ? 1 : 0) - (input.left ? 1 : 0);
  const ay = (input.down ? 1 : 0) - (input.up ? 1 : 0);

  p.vx += ax * PADDLE.accel * dt;
  p.vy += ay * PADDLE.accel * dt;

  // pointer/touch targets ease toward the pointer (mouse) with velocity
  // proportional to distance so we can measure paddle vx honestly.
  if (input.pointerActive) {
    const k = Math.min(1, dt * 22);
    const nx = p.x + (input.pointerX - p.x) * k;
    p.vx = (nx - p.x) / dt;
    p.x = nx;
    const ny = p.y + (input.pointerY - p.y) * k;
    p.vy = (ny - p.y) / dt;
    p.y = ny;
  }

  // Damping
  const f = Math.max(0, 1 - PADDLE.drag * dt);
  if (!ax) p.vx *= f;
  if (!ay) p.vy *= f;

  p.vx = clamp(p.vx, -PADDLE.maxV, PADDLE.maxV);
  p.vy = clamp(p.vy, -PADDLE.maxV, PADDLE.maxV);

  p.x += p.vx * dt;
  p.y += p.vy * dt;

  // Constrain to field & band
  const half = p.w / 2;
  if (p.x < half) { p.x = half; p.vx = Math.max(0, p.vx); }
  if (p.x > field.width - half) { p.x = field.width - half; p.vx = Math.min(0, p.vx); }

  const yMin = field.height * PADDLE.bandTop;
  const yMax = field.height * PADDLE.bandBottom;
  if (p.y < yMin) { p.y = yMin; p.vy = Math.max(0, p.vy); }
  if (p.y > yMax) { p.y = yMax; p.vy = Math.min(0, p.vy); }

  // Wide-paddle timer
  if (p.wideT > 0) {
    p.wideT -= dt;
    p.w = PADDLE.w * (1 + 0.55 * Math.min(1, p.wideT / 1));
  } else {
    p.w += (PADDLE.w - p.w) * Math.min(1, dt * 6);
  }
  if (p.magnetT > 0) p.magnetT -= dt;
}

export function clamp(v, lo, hi) { return v < lo ? lo : v > hi ? hi : v; }

// Reflect a ball off the paddle.
// rel = (ball.x - paddle.x) / (paddle.w/2), clamped [-1, 1].
// Returns new velocity. Pure.
export function paddleReflect(ball, paddle, opts = {}) {
  const rel = clamp((ball.x - paddle.x) / (paddle.w / 2), -1, 1);
  const speed = Math.max(320, Math.hypot(ball.vx, ball.vy));

  // Base outgoing angle: edge hits send the ball shallow, center sends it up.
  const maxAngle = 62 * Math.PI / 180;   // from vertical
  const theta = rel * maxAngle;

  let vx = Math.sin(theta) * speed;
  let vy = -Math.cos(theta) * speed;

  // Vertical paddle motion: moving up at impact imparts extra speed and lift;
  // moving down deadens and steepens the ball.
  const lift = -paddle.vy; // positive when paddle moving up
  if (lift > 0) {
    const boost = Math.min(1, lift / 700);
    vx *= 1 + 0.35 * boost;
    vy *= 1 + 0.85 * boost; // extra upward kick
  } else if (lift < 0) {
    const deaden = Math.min(1, -lift / 700);
    vx *= 1 - 0.25 * deaden;
    vy *= 1 - 0.45 * deaden; // kills upward speed → ball hangs/steepens
    vy -= 60 * deaden;       // small nudge to avoid flat loops
  }

  const out = { vx, vy, rel };
  return out;
}
