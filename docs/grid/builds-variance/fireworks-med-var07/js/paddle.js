'use strict';
/* ============================================================
   Attic Breakout — paddle.js
   2D paddle within its zone; velocity tracked for "english" and
   the upward SMASH mechanic. Squash & stretch, idle glow that
   scales with the score multiplier.
   ============================================================ */

class Paddle {
  constructor() {
    this.x = CONFIG.W / 2;
    this.y = CONFIG.PADDLE_ZONE_BOTTOM - 40;
    this.w = CONFIG.PADDLE_W;
    this.h = CONFIG.PADDLE_H;
    this.vx = 0; this.vy = 0;   // smoothed velocity (px/s)
    this._px = this.x; this._py = this.y;
    this.squash = 0;            // squash-and-stretch timer
    this.smashFx = 0;           // recent smash glow
    this.sticky = 0;            // seconds of sticky remaining
    this.wide = 0; this.shrink = 0;
    this.heldBall = null;       // ball held by sticky
    this.holdOffset = 0;
    this.glow = 0;              // extra flash on catch
  }

  get top() { return this.y - this.h / 2; }
  get halfW() { return this.w / 2; }

  applyTimers(dt) {
    if (this.wide > 0) this.wide = Math.max(0, this.wide - dt);
    if (this.shrink > 0) this.shrink = Math.max(0, this.shrink - dt);
    if (this.sticky > 0) this.sticky = Math.max(0, this.sticky - dt);
    const targetW = this.shrink > 0 ? CONFIG.PADDLE_W_SHRINK
      : this.wide > 0 ? CONFIG.PADDLE_W_WIDE : CONFIG.PADDLE_W;
    this.w = lerp(this.w, targetW, 1 - Math.exp(-10 * dt));
    if (this.squash > 0) this.squash = Math.max(0, this.squash - dt * 5);
    if (this.smashFx > 0) this.smashFx = Math.max(0, this.smashFx - dt * 3);
    if (this.glow > 0) this.glow = Math.max(0, this.glow - dt * 4);
  }

  /** Integrate toward a desired position (input sets this each frame). */
  seekTo(tx, ty, dt, maxYVel = 900) {
    const maxSpeed = 2600;
    let dx = tx - this.x;
    let dy = clamp(ty, CONFIG.PADDLE_ZONE_TOP, CONFIG.PADDLE_ZONE_BOTTOM) - this.y;
    const dist = Math.hypot(dx, dy);
    let stepX = 0, stepY = 0;
    if (dist > 0.5) {
      const sp = Math.min(maxSpeed, dist * 18);
      stepX = dx / dist * sp * dt;
      stepY = dy / dist * sp * dt;
      if (Math.abs(stepX) > Math.abs(dx)) stepX = dx;
      if (Math.abs(stepY) > Math.abs(dy)) stepY = dy;
    }
    this.x = clamp(this.x + stepX, this.halfW, CONFIG.W - this.halfW);
    this.y = clamp(this.y + stepY, CONFIG.PADDLE_ZONE_TOP, CONFIG.PADDLE_ZONE_BOTTOM);
    this._trackVel(dt, maxYVel);
  }

  _trackVel(dt, maxYVel) {
    if (dt <= 0) return;
    const ivx = (this.x - this._px) / dt;
    const ivy = clamp((this.y - this._py) / dt, -maxYVel, maxYVel);
    const k = 1 - Math.exp(-14 * dt);
    this.vx = lerp(this.vx, ivx, k);
    this.vy = lerp(this.vy, ivy, k);
    this._px = this.x; this._py = this.y;
  }

  /** Reflect the ball off the paddle. dirx/diry = ball velocity direction.
   *  Returns {smash:boolean} for FX. */
  bounce(ball, speed) {
    // Classic angle-by-hit-offset reflection off the TOP face.
    const rel = clamp((ball.x - this.x) / (this.halfW + ball.r), -1, 1);
    const maxAngle = 62 * Math.PI / 180; // from vertical
    let ang = -Math.PI / 2 + rel * maxAngle;
    // Horizontal english from paddle velocity.
    const english = clamp(this.vx * 0.28, -320, 320);
    let nvx = Math.cos(ang) * speed + english;
    let nvy = Math.sin(ang) * speed;
    // Slight upward bias if paddle dropping, avoid horizontal stall.
    if (nvy > -80) nvy = -80;
    // Smash: paddle rising into the ball.
    let smash = false;
    if (this.vy < -140) {
      smash = true;
      const boost = clamp(-this.vy * 0.55, 160, 340);
      nvy -= boost;
      nvx *= 1.05;
    }
    const sp2 = Math.hypot(nvx, nvy);
    const capped = clamp(sp2, CONFIG.BALL_SPEED_MIN, CONFIG.BALL_SPEED_MAX);
    ball.vx = nvx / sp2 * capped;
    ball.vy = nvy / sp2 * capped;
    this.squash = 1;
    if (smash) this.smashFx = 1;
    return { smash };
  }

  draw(ctx, time, multiplier) {
    const sq = this.squash;
    const sy = 1 - sq * 0.3, sx = 1 + sq * 0.22;
    const w = this.w * sx, h = this.h * sy;
    const glowT = clamp(0.25 + multiplier * 0.14, 0.25, 1);
    ctx.save();
    ctx.translate(this.x, this.y);

    // Idle glow (intensifies with multiplier) + catch flash + smash flash
    const g = glowT * 0.5 + this.glow * 0.8 + this.smashFx * 1.2;
    ctx.shadowColor = `rgba(120,200,255,${clamp(g, 0, 1)})`;
    ctx.shadowBlur = 12 + g * 26;

    // Body
    const grad = ctx.createLinearGradient(0, -h / 2, 0, h / 2);
    grad.addColorStop(0, '#dff3ff');
    grad.addColorStop(0.5, '#6cc8ff');
    grad.addColorStop(1, '#2a6db5');
    ctx.fillStyle = grad;
    if (ctx.roundRect) {
      ctx.beginPath(); ctx.roundRect(-w / 2, -h / 2, w, h, 10); ctx.fill();
    } else { ctx.fillRect(-w / 2, -h / 2, w, h); }
    ctx.shadowBlur = 0;

    // Sticky sheen
    if (this.sticky > 0) {
      ctx.fillStyle = `rgba(255,180,220,${0.25 + Math.sin(time * 6) * 0.12})`;
      if (ctx.roundRect) {
        ctx.beginPath(); ctx.roundRect(-w / 2, -h / 2, w, h * 0.5, 10); ctx.fill();
      } else { ctx.fillRect(-w / 2, -h / 2, w, h * 0.5); }
    }
    // Smash charge line
    if (this.smashFx > 0) {
      ctx.strokeStyle = `rgba(255,255,255,${this.smashFx})`;
      ctx.lineWidth = 2;
      ctx.beginPath(); ctx.moveTo(-w / 2, -h / 2 - 2); ctx.lineTo(w / 2, -h / 2 - 2); ctx.stroke();
    }
    // Center notch (aim marker)
    ctx.fillStyle = 'rgba(255,255,255,0.55)';
    ctx.fillRect(-1.5, -h / 2 + 4, 3, 5);
    ctx.restore();
  }
}

window.Paddle = Paddle;
