// Paddle, Ball, Capsule entities.
import { PADDLE, BALL, CAPSULE, FIELD, POWER_DEFS, POWER_POOL } from './constants.js';

export class Paddle {
  constructor() {
    this.w = PADDLE.w; this.h = PADDLE.h;
    this.x = FIELD.w / 2; this.y = FIELD.h - 90;
    this.vx = 0; this.vy = 0; // smoothed, logical units/sec
    this.sticky = false; this.stuckBall = null;
    this.squash = 0;
    this.wideTimer = 0; this.shrinkTimer = 0;
  }
  get half() { return this.w / 2; }
  get bandTop() { return FIELD.h * PADDLE.bandTop; }
  get bandBottom() { return FIELD.h - PADDLE.h - 14; }
  targetWidth() {
    let w = PADDLE.w;
    if (this.wideTimer > 0) w *= 1.55;
    if (this.shrinkTimer > 0) w *= 0.62;
    return w;
  }
  update(dt, input, pointerField) {
    // timers
    if (this.wideTimer > 0) this.wideTimer -= dt;
    if (this.shrinkTimer > 0) this.shrinkTimer -= dt;
    this.w += (this.targetWidth() - this.w) * Math.min(1, dt * 10);

    let tx = this.x, ty = this.y, hasTarget = false;
    const s = input.sample(dt);
    if (s.usePointer && pointerField) {
      tx = pointerField.x; ty = pointerField.y; hasTarget = true;
    }
    if (s.dx || s.dy) {
      const v = PADDLE.baseSpeed;
      tx = this.x + s.dx * v * dt; ty = this.y + s.dy * v * dt; hasTarget = true;
      this._kbVx = s.dx * v; this._kbVy = s.dy * v;
    } else { this._kbVx = 0; this._kbVy = 0; }

    if (hasTarget) {
      const nx = tx, ny = ty;
      const oldX = this.x, oldY = this.y;
      this.x = nx; this.y = ny;
      // measure velocity then clamp to band
      const clamped = this._clamp();
      const dtf = Math.max(dt, 1e-4);
      this.vx = (this.x - oldX) / dtf;
      this.vy = (this.y - oldY) / dtf;
    } else {
      this.vx *= Math.pow(0.001, dt); this.vy *= Math.pow(0.001, dt);
    }
    if (this.squash > 0) this.squash = Math.max(0, this.squash - dt * 5);
  }
  _clamp() {
    const xMin = this.half + 6, xMax = FIELD.w - this.half - 6;
    let hit = false;
    if (this.x < xMin) { this.x = xMin; hit = true; }
    if (this.x > xMax) { this.x = xMax; hit = true; }
    if (this.y < this.bandTop) { this.y = this.bandTop; }
    if (this.y > this.bandBottom) { this.y = this.bandBottom; }
    return hit;
  }
}

let _ballId = 0;
export class Ball {
  constructor(x, y, vx, vy) {
    this.id = ++_ballId;
    this.x = x; this.y = y; this.vx = vx; this.vy = vy;
    this.r = BALL.r;
    this.stuck = false; this.stuckOffset = 0;
    this.squash = 0; this.squashAng = 0;
    this.trail = [];
    this.lastBrickCombo = 0;
  }
  speed() { return Math.hypot(this.vx, this.vy); }
  setSpeed(s) {
    const cur = this.speed() || 1;
    this.vx = this.vx / cur * s; this.vy = this.vy / cur * s;
  }
  clampSpeed() {
    const s = this.speed();
    if (s < BALL.minSpeed) this.setSpeed(BALL.minSpeed);
    else if (s > BALL.maxSpeed) this.setSpeed(BALL.maxSpeed);
  }
}

export function rollPower() {
  let total = 0;
  for (const [, w] of POWER_POOL) total += w;
  let r = Math.random() * total;
  for (const [k, w] of POWER_POOL) { r -= w; if (r <= 0) return k; }
  return 'WIDE';
}

export class Capsule {
  constructor(x, y, kind) {
    this.kind = kind;
    this.def = POWER_DEFS[kind];
    this.x = x; this.y = y;
    this.vy = CAPSULE.fallSpeed;
    this.w = CAPSULE.w; this.h = CAPSULE.h;
    this.t = 0; this.dead = false;
  }
  update(dt) { this.t += dt; this.y += this.vy * dt; if (this.y > FIELD.h + 40) this.dead = true; }
}
