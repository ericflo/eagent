// paddle.js — paddle with X/Y motion inside its band, smoothing, squash/stretch,
// vertical-velocity smash transfer, horizontal english, charged visual.
import { WORLD_W, BAND_TOP, BAND_BOTTOM, clamp, lerp } from './engine.js';

export class Paddle {
  constructor() {
    this.w = 110;
    this.h = 16;
    this.x = WORLD_W / 2 - this.w / 2;
    this.y = BAND_BOTTOM - 40;
    this.vx = 0;
    this.vy = 0;
    this.prevX = this.x;
    this.prevY = this.y;
    this.smoothing = 0.35;      // per physics step toward target
    this.charged = 0;           // seconds remaining of post-smash charge visual
    this.pickupFlash = 0;       // seconds remaining of pickup flash
    this.pickupColor = '#fff';
  }

  // target: {x, y} from Input.paddleTarget. Apply smoothing per physics step.
  step(target) {
    this.prevX = this.x; this.prevY = this.y;
    const nx = lerp(this.x, target.x, this.smoothing);
    const ny = lerp(this.y, target.y, this.smoothing);
    const clampedX = clamp(nx, 0, WORLD_W - this.w);
    const clampedY = clamp(ny, BAND_TOP, BAND_BOTTOM - this.h);
    this.vx = (clampedX - this.x) / (1 / 120);
    this.vy = (clampedY - this.y) / (1 / 120);
    this.x = clampedX; this.y = clampedY;
    if (this.charged > 0) this.charged -= 1 / 120;
    if (this.pickupFlash > 0) this.pickupFlash -= 1 / 120;
  }

  rect(alpha) {
    return {
      x: lerp(this.prevX, this.x, alpha),
      y: lerp(this.prevY, this.y, alpha),
      w: this.w, h: this.h,
    };
  }

  // current instantaneous velocity estimate for smash/english decisions
  velocity() { return { vx: this.vx, vy: this.vy }; }

  isSmash() { return this.vy < -60; } // moving up faster than 60 px/s at contact

  render(ctx, alpha) {
    const r = this.rect(alpha);
    const stretch = clamp(-this.vy / 600, 0, 1); // upward motion stretches taller
    const squashW = this.w * (1 + stretch * 0.06);
    const squashH = this.h * (1 - stretch * 0.18) * (1 + stretch * 0.18);
    const cx = r.x + r.w / 2, cy = r.y + r.h / 2;
    const charged = this.charged > 0;
    const flashing = this.pickupFlash > 0;

    let body1 = '#67e8f9', body2 = '#0ea5e9', glow = 'rgba(14,165,233,0.55)';
    if (charged) { body1 = '#fde68a'; body2 = '#f59e0b'; glow = 'rgba(245,158,11,0.8)'; }
    if (flashing) {
      body1 = this.pickupColor; body2 = this.pickupColor; glow = this.pickupColor;
    }

    ctx.save();
    // glow
    ctx.shadowColor = glow;
    ctx.shadowBlur = charged ? 26 : 14;
    const g = ctx.createLinearGradient(cx - squashW / 2, 0, cx + squashW / 2, 0);
    g.addColorStop(0, body2); g.addColorStop(0.5, body1); g.addColorStop(1, body2);
    ctx.fillStyle = g;
    const rr = squashH / 2;
    ctx.beginPath();
    ctx.roundRect(cx - squashW / 2, cy - squashH / 2, squashW, squashH, rr);
    ctx.fill();
    ctx.shadowBlur = 0;
    // top highlight
    ctx.fillStyle = 'rgba(255,255,255,0.55)';
    ctx.beginPath();
    ctx.roundRect(cx - squashW / 2 + 6, cy - squashH / 2 + 2, squashW - 12, 3, 2);
    ctx.fill();
    ctx.restore();
  }
}
