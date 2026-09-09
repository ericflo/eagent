// src/game/paddle.js — the paddle entity: movement, smoothing, and the
// "uppercut / soft catch" ball response.
import { PADDLE_W, PADDLE_H, PADDLE_Y_MIN, PADDLE_Y_MAX, W } from '../core/constants.js';
import { clamp, damp, hypot } from '../core/math.js';
import { clampBallAngle, clampSpeed, MIN_VERTICAL_ANGLE } from './ball.js';

export class Paddle {
  constructor() {
    this.x = W / 2;
    this.y = PADDLE_Y_MAX;
    this.w = PADDLE_W;
    this.baseW = PADDLE_W;
    this.h = PADDLE_H;
    this.vx = 0;
    this.vy = 0;
    this.targetX = this.x;
    this.targetY = this.y;
    this.flags = { sticky: false, magnet: false, lasers: false };
    this.uppercutFlashT = 0;
    this.tilt = 0;
  }

  get rect() { return { x: this.x - this.w / 2, y: this.y - this.h / 2, w: this.w, h: this.h }; }

  setTarget(x, y) {
    this.targetX = clamp(x, this.w / 2, W - this.w / 2);
    this.targetY = clamp(y, PADDLE_Y_MIN, PADDLE_Y_MAX);
  }

  update(dt) {
    const px = this.x, py = this.y;
    this.x = damp(this.x, this.targetX, 22, dt);
    this.y = damp(this.y, this.targetY, 22, dt);
    this.x = clamp(this.x, this.w / 2, W - this.w / 2);
    this.y = clamp(this.y, PADDLE_Y_MIN, PADDLE_Y_MAX);
    const invDt = dt > 1e-6 ? 1 / dt : 0;
    this.vx = (this.x - px) * invDt;
    this.vy = (this.y - py) * invDt;
    if (this.uppercutFlashT > 0) this.uppercutFlashT = Math.max(0, this.uppercutFlashT - dt * 2.2);
    this.tilt = damp(this.tilt, clamp(this.vx * 0.0006, -0.25, 0.25), 8, dt);
  }

  /**
   * Resolve a ball bouncing off the paddle. Mutates ball velocity.
   * Returns {uppercut:boolean, softCatch:boolean, speedBefore, speedAfter}
   * so the caller can trigger FX/audio/score feedback.
   */
  resolveHit(ball) {
    const speedBefore = hypot(ball.vx, ball.vy);
    const rel = clamp((ball.x - this.x) / (this.w / 2), -1, 1);
    // base angle: -90deg (straight up) fanned out by rel, up to ~65deg from vertical
    const maxFan = 65 * Math.PI / 180;
    let ang = -Math.PI / 2 + rel * maxFan;

    let speed = speedBefore;
    const uppercut = this.vy < -40;
    const softCatch = this.vy > 40;
    if (uppercut) {
      speed += Math.min(0.55 * Math.abs(this.vy), 900);
      this.uppercutFlashT = 1;
    } else if (softCatch) {
      speed *= 0.72;
    }
    // paddle vx imparts spin/english: shifts the angle further in the direction of paddle motion
    ang += clamp(this.vx * 0.00035, -0.5, 0.5);

    let vx = Math.cos(ang) * speed;
    let vy = Math.sin(ang) * speed;
    const clamped = clampBallAngle(vx, vy, MIN_VERTICAL_ANGLE);
    const finalSpeed = clampSpeed(clamped.vx, clamped.vy);
    ball.vx = finalSpeed.vx;
    ball.vy = finalSpeed.vy;
    ball.speed = finalSpeed.speed;
    ball.y = this.y - this.h / 2 - ball.r - 0.5;

    return { uppercut, softCatch, speedBefore, speedAfter: ball.speed };
  }

  draw(g, t, opts = {}) {
    const mult = opts.mult || 1;
    const chargeK = clamp((mult - 1) / 30, 0, 1); // "charge-up" as multiplier climbs
    g.save();
    g.translate(this.x, this.y);
    g.rotate(this.tilt);
    const glow = 0.35 + this.uppercutFlashT * 0.65 + chargeK * 0.35;
    g.shadowColor = 'transparent';
    const hw = this.w / 2, hh = this.h / 2;

    // charge-up outer glow ring that grows with multiplier
    if (chargeK > 0.02) {
      g.globalCompositeOperation = 'lighter';
      const pulse = 0.6 + 0.4 * Math.sin(t * (4 + chargeK * 8));
      const cg = g.createRadialGradient(0, 0, hh, 0, 0, hw + 20 + chargeK * 30);
      cg.addColorStop(0, `rgba(255,209,94,${0.05 + chargeK * 0.22 * pulse})`);
      cg.addColorStop(1, 'rgba(255,94,200,0)');
      g.fillStyle = cg;
      g.beginPath();
      g.ellipse(0, 0, hw + 20 + chargeK * 30, hh + 16 + chargeK * 20, 0, 0, Math.PI * 2);
      g.fill();
      g.globalCompositeOperation = 'source-over';
    }

    const grad = g.createLinearGradient(-hw, 0, hw, 0);
    if (chargeK > 0.6) {
      grad.addColorStop(0, '#ff5ec8'); grad.addColorStop(0.5, '#ffd15e'); grad.addColorStop(1, '#8ffcff');
    } else {
      grad.addColorStop(0, '#3ad7ff'); grad.addColorStop(0.5, '#8ffcff'); grad.addColorStop(1, '#ff5ec8');
    }
    g.fillStyle = grad;
    roundRect(g, -hw, -hh, this.w, this.h, 10);
    g.fill();
    g.globalCompositeOperation = 'lighter';
    g.globalAlpha = glow * 0.6;
    g.fillStyle = '#ffffff';
    roundRect(g, -hw, -hh, this.w, this.h * 0.5, 8);
    g.fill();

    // uppercut impact flare + upward speed lines, unmistakable feedback
    if (this.uppercutFlashT > 0.02) {
      const k = this.uppercutFlashT;
      g.globalAlpha = k;
      const fg = g.createRadialGradient(0, -hh, 0, 0, -hh, 90);
      fg.addColorStop(0, 'rgba(255,240,180,0.9)');
      fg.addColorStop(1, 'rgba(255,240,180,0)');
      g.fillStyle = fg;
      g.beginPath(); g.arc(0, -hh, 90 * (1.4 - k), 0, Math.PI * 2); g.fill();
      g.strokeStyle = `rgba(255,225,120,${k})`;
      g.lineWidth = 2.5;
      for (let i = -2; i <= 2; i++) {
        const lx = i * 22;
        g.beginPath();
        g.moveTo(lx, -hh - 4);
        g.lineTo(lx * 1.3, -hh - 40 - (1 - k) * 60);
        g.stroke();
      }
    }
    g.globalAlpha = 1;
    g.globalCompositeOperation = 'source-over';
    if (this.flags.sticky) {
      g.strokeStyle = 'rgba(255,255,150,0.8)';
      g.lineWidth = 2;
      roundRect(g, -hw, -hh, this.w, this.h, 10);
      g.stroke();
    }
    if (this.flags.magnet) {
      g.strokeStyle = 'rgba(180,120,255,0.7)';
      g.lineWidth = 2;
      g.beginPath();
      g.arc(0, 0, hw + 10, Math.PI * 0.15, Math.PI * 0.85);
      g.stroke();
    }
    g.restore();
  }
}

function roundRect(g, x, y, w, h, r) {
  g.beginPath();
  g.moveTo(x + r, y);
  g.arcTo(x + w, y, x + w, y + h, r);
  g.arcTo(x + w, y + h, x, y + h, r);
  g.arcTo(x, y + h, x, y, r);
  g.arcTo(x, y, x + w, y, r);
  g.closePath();
}
