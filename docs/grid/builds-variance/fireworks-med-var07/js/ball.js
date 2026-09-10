'use strict';
/* ============================================================
   Attic Breakout — ball.js
   Ball with glowing speed-scaled trail, timed power states
   (fire / heavy / ghost), multiball splitting.
   ============================================================ */

class Ball {
  constructor(x, y, vx, vy) {
    this.x = x; this.y = y;
    this.vx = vx; this.vy = vy;
    this.r = CONFIG.BALL_R;
    this.stuck = false;      // sticky paddle hold
    this.launched = false;
    // Timed states, seconds remaining (0 = inactive).
    this.fire = 0; this.heavy = 0; this.ghost = 0;
    this.trail = [];         // recent positions [x,y,speedT]
    this.maxTrail = 16;
  }

  get speed() { return Math.hypot(this.vx, this.vy); }

  setSpeed(s) {
    const cur = this.speed || 1;
    this.vx = this.vx / cur * s;
    this.vy = this.vy / cur * s;
  }

  get isAboveBricks() { return this.y - this.r < CONFIG.FIELD_TOP && this.launched; }

  update(dt) {
    if (this.fire > 0) this.fire = Math.max(0, this.fire - dt);
    if (this.heavy > 0) this.heavy = Math.max(0, this.heavy - dt);
    if (this.ghost > 0) this.ghost = Math.max(0, this.ghost - dt);
    this.r = this.heavy > 0 ? CONFIG.BALL_R_HEAVY : CONFIG.BALL_R;
  }

  pushTrail() {
    this.trail.push(this.x, this.y, this.speed / CONFIG.BALL_SPEED_MAX);
    if (this.trail.length > this.maxTrail * 3) this.trail.splice(0, 3);
  }

  draw(ctx, time) {
    // Trail: length/alpha scale with speed.
    const n = this.trail.length / 3;
    if (n > 1) {
      for (let i = 0; i < n - 1; i++) {
        const t = i / n;
        const x = this.trail[i * 3], y = this.trail[i * 3 + 1], sp = this.trail[i * 3 + 2];
        const a = t * (0.12 + sp * 0.5);
        const rad = this.r * (0.35 + t * 0.6);
        ctx.fillStyle = this._trailColor(a);
        ctx.beginPath();
        ctx.arc(x, y, rad, 0, TAU);
        ctx.fill();
      }
    }
    // Glow
    ctx.save();
    const [gr, gg, gb] = this._coreColor();
    ctx.shadowColor = `rgb(${gr},${gg},${gb})`;
    ctx.shadowBlur = this.fire > 0 ? 26 : 14;
    // Body
    const grad = ctx.createRadialGradient(this.x - this.r * 0.3, this.y - this.r * 0.3, 1, this.x, this.y, this.r);
    grad.addColorStop(0, '#ffffff');
    grad.addColorStop(0.45, `rgb(${gr},${gg},${gb})`);
    grad.addColorStop(1, `rgba(${gr * 0.5 | 0},${gg * 0.5 | 0},${gb * 0.5 | 0},1)`);
    ctx.fillStyle = grad;
    ctx.beginPath();
    ctx.arc(this.x, this.y, this.r, 0, TAU);
    ctx.fill();
    // Fire flames
    if (this.fire > 0) {
      ctx.shadowBlur = 0;
      for (let i = 0; i < 3; i++) {
        const a = time * 14 + i * 2.1;
        ctx.fillStyle = `rgba(255,${120 + ((i * 40) | 0)},40,0.7)`;
        ctx.beginPath();
        ctx.arc(this.x + Math.cos(a) * this.r * 1.15, this.y + Math.sin(a) * this.r * 1.15, this.r * 0.5, 0, TAU);
        ctx.fill();
      }
    }
    // Ghost shimmer ring
    if (this.ghost > 0) {
      ctx.strokeStyle = `rgba(170,230,255,${0.5 + Math.sin(time * 10) * 0.3})`;
      ctx.lineWidth = 2;
      ctx.beginPath();
      ctx.arc(this.x, this.y, this.r + 4, 0, TAU);
      ctx.stroke();
    }
    ctx.restore();
  }

  _coreColor() {
    if (this.fire > 0) return [255, 130, 60];
    if (this.heavy > 0) return [200, 205, 225];
    if (this.ghost > 0) return [170, 230, 255];
    return [255, 235, 180];
  }
  _trailColor(a) {
    const [r, g, b] = this._coreColor();
    return `rgba(${r},${g},${b},${a.toFixed(3)})`;
  }
}

window.Ball = Ball;
