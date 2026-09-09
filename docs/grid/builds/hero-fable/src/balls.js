// balls.js — pure ball model & power-up rules. No DOM, no canvas.

import { uid, normalize, clamp } from './util.js';
import { SPEED } from './physics.js';

export const BALL_RADIUS = 10;
export const HEAVY_RADIUS = 16;

/**
 * Power-up catalogue. `target` says where the power lives:
 *  'ball'    — timed state on the ball (fire/heavy/slow)
 *  'paddle'  — timed state on the paddle (magnet/wide/laser)
 *  'instant' — applied once (split/life)
 * `weight` drives the random drop table (life is rare).
 */
export const POWERS = {
  fire: { letter: 'F', name: 'FIREBALL', color: '#ff7a1a', target: 'ball', duration: 12, weight: 10 },
  heavy: { letter: 'H', name: 'WRECKING BALL', color: '#c0c8d8', target: 'ball', duration: 12, weight: 9 },
  split: { letter: 'S', name: 'MULTIBALL', color: '#5dff9f', target: 'instant', duration: 0, weight: 12 },
  magnet: { letter: 'M', name: 'MAGNET', color: '#c084fc', target: 'paddle', duration: 12, weight: 8 },
  wide: { letter: 'W', name: 'WIDE', color: '#3fb5ff', target: 'paddle', duration: 15, weight: 11 },
  slow: { letter: 'T', name: 'SLOW-MO', color: '#7dd3fc', target: 'ball', duration: 8, weight: 8 },
  laser: { letter: 'L', name: 'LASER', color: '#ff5d5d', target: 'paddle', duration: 10, weight: 9 },
  life: { letter: '+', name: 'EXTRA LIFE', color: '#ff5cc8', target: 'instant', duration: 0, weight: 2 },
};

export const SLOW_FACTOR = 0.7;
export const SPLIT_ANGLE = (25 * Math.PI) / 180;
export const MAGNET_AUTO_LAUNCH = 2; // seconds a caught ball waits before launching itself

export function createBall(x, y, dir = { x: 0, y: -1 }, speed = SPEED.base) {
  const d = normalize(dir.x, dir.y);
  return {
    id: uid(),
    x,
    y,
    dx: d.x,
    dy: d.y,
    speed,
    r: BALL_RADIUS,
    power: null, // 'fire' | 'heavy' | 'slow' | null
    powerTime: 0,
    forceHeavy: false, // granted after 8 s on top
    onTop: false,
    topTime: 0,
    offTop: 0, // s spent below the brick line since last in the attic (on-top hysteresis)
    topBreaks: 0, // consecutive breaks from above (cascade every 5)
    idle: 0, // s since the last paddle hit / brick break (stuck-ball watchdog)
    watchdog: false, // true while the watchdog is nudging this ball's bounces
    stuck: false, // resting on / magnetised to the paddle
    stuckOffset: 0,
    stuckTime: 0,
    lastPaddleHit: 0,
    squash: 0, // render-only
  };
}

/** Pick a random power kind using the weight table. rng() → [0,1). */
export function rollPower(rng) {
  const kinds = Object.keys(POWERS);
  const total = kinds.reduce((s, k) => s + POWERS[k].weight, 0);
  let r = rng() * total;
  for (const k of kinds) {
    r -= POWERS[k].weight;
    if (r < 0) return k;
  }
  return kinds[kinds.length - 1];
}

/** Apply a ball-targeted power. Replaces any current ball power. */
export function applyBallPower(ball, kind) {
  const def = POWERS[kind];
  if (!def || def.target !== 'ball') return false;
  const wasHeavy = isHeavy(ball);
  ball.power = kind;
  ball.powerTime = def.duration;
  ball.r = effectiveRadius(ball);
  if (kind === 'heavy' && !wasHeavy) ball.speed = Math.max(SPEED.min, ball.speed * 0.9); // slightly slower
  return true;
}

/** Tick the ball's power timer. Returns the kind that just expired, or null. */
export function updateBallPower(ball, dt) {
  if (!ball.power) return null;
  ball.powerTime -= dt;
  if (ball.powerTime > 0) return null;
  const ended = ball.power;
  ball.power = null;
  ball.powerTime = 0;
  ball.r = effectiveRadius(ball);
  return ended;
}

export const isHeavy = (ball) => ball.power === 'heavy' || ball.forceHeavy;
export const isFire = (ball) => ball.power === 'fire';

/** Fire and heavy balls ignore the angle/speed/top rules. */
export const ignoresRules = (ball) => isFire(ball) || isHeavy(ball);

/** Fireballs go straight through bricks they break instead of bouncing. */
export const pierces = (ball) => isFire(ball);

export const effectiveRadius = (ball) => (isHeavy(ball) ? HEAVY_RADIUS : BALL_RADIUS);

/** Movement speed after the slow-mo power. */
export const effectiveSpeed = (ball) => (ball.power === 'slow' ? ball.speed * SLOW_FACTOR : ball.speed);

/** Rotate a unit direction by `a` radians. */
export function rotateDir(dx, dy, a) {
  const c = Math.cos(a);
  const s = Math.sin(a);
  return { x: dx * c - dy * s, y: dx * s + dy * c };
}

/** Multiball: two extra balls at ±25° from the source, inheriting speed & power. */
export function splitBall(ball) {
  return [SPLIT_ANGLE, -SPLIT_ANGLE].map((a) => {
    const d = rotateDir(ball.dx, ball.dy, a);
    const b = createBall(ball.x, ball.y, d, ball.speed);
    b.power = ball.power;
    b.powerTime = ball.powerTime;
    b.r = ball.r;
    return b;
  });
}

/** Clamp a speed into the legal range. */
export const clampSpeed = (s) => clamp(s, SPEED.min, SPEED.max);
