// entities/paddle.js
import { clamp, damp } from '../util.js';

export const PADDLE_W = 104;
export const PADDLE_H = 16;

export class Paddle {
  constructor(field) {
    this.field = field;
    this.baseW = PADDLE_W;
    this.w = PADDLE_W;
    this.h = PADDLE_H;
    this.x = field.left + (field.right - field.left) / 2 - this.w / 2;
    this.y = field.bottom - 120;
    this.vx = 0;
    this.vy = 0;
    // desired position (pointer / drag mode)
    this.targetX = this.x;
    this.targetY = this.y;
    this.useTarget = false;
    // velocity-driven (keys / thumbstick)
    this.driveX = 0;
    this.driveY = 0;
    this.speed = 1180;     // px/s at full deflection (crosses the field in ~0.45s)
    this.accel = 5600;
    this.friction = 0.0008; // damp factor per second
    this.smashVel = 0;      // decaying memory of recent upward speed (smash forgiveness)
    this.wideTimer = 0;
    this.laserTimer = 0;
    this.magnetTimer = 0;
    this.flash = 0;
    this.squash = 0;
    // vertical band: lowest 22% of the playfield
    const h = field.bottom - field.top;
    this.bandTop = field.bottom - h * 0.22;
    this.bandBottom = field.bottom - 34;
  }

  get cx() { return this.x + this.w / 2; }
  get cy() { return this.y + this.h / 2; }
  get box() { return { x: this.x, y: this.y, w: this.w, h: this.h }; }

  setWide(on) {
    const c = this.cx;
    this.w = on ? this.baseW * 1.6 : this.baseW;
    this.x = c - this.w / 2;
    this.clampPos();
  }

  clampPos() {
    this.x = clamp(this.x, this.field.left + 4, this.field.right - this.w - 4);
    this.y = clamp(this.y, this.bandTop, this.bandBottom);
  }

  /**
   * @param {number} dt
   * @param {object} cmd  { mode:'target'|'drive', x, y, dx, dy }
   */
  update(dt, cmd) {
    const px = this.x, py = this.y;

    if (cmd && cmd.mode === 'target') {
      const tx = clamp(cmd.x - this.w / 2, this.field.left + 4, this.field.right - this.w - 4);
      const ty = clamp(cmd.y - this.h / 2, this.bandTop, this.bandBottom);
      // Snappy but not instant, so smash velocity reads nicely.
      this.x = damp(this.x, tx, 0.000004, dt);
      this.y = damp(this.y, ty, 0.00002, dt);
    } else {
      const dx = cmd ? cmd.dx || 0 : 0;
      const dy = cmd ? cmd.dy || 0 : 0;
      const targetVX = dx * this.speed;
      const targetVY = dy * this.speed * 0.86;
      // Reversing (or starting) is much snappier than coasting to a stop, so a quick
      // tap of "up" produces a real smash instead of a lazy drift.
      const smoothX = dx === 0 ? 0.000001 : (targetVX * this.driveX <= 0 ? 0.000002 : 0.0004);
      const smoothY = dy === 0 ? 0.000001 : (targetVY * this.driveY <= 0 ? 0.0000005 : 0.0002);
      this.driveX = damp(this.driveX, targetVX, smoothX, dt);
      this.driveY = damp(this.driveY, targetVY, smoothY, dt);
      this.x += this.driveX * dt;
      this.y += this.driveY * dt;
    }

    this.clampPos();
    this.vx = dt > 0 ? (this.x - px) / dt : 0;
    this.vy = dt > 0 ? (this.y - py) / dt : 0;
    // Remember how hard we were rising for a moment, so a smash that lands a frame
    // after the paddle stops still counts. Decays to ~2% in a quarter second.
    this.smashVel = Math.max(this.smashVel * Math.pow(0.02, dt * 4), Math.max(0, -this.vy));

    if (this.flash > 0) this.flash -= dt * 4;
    if (this.squash > 0) this.squash -= dt * 6;
  }
}
