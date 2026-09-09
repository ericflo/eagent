// ---------------------------------------------------------------------------
// starfield.js — parallax dot field, reacts to heat
// ---------------------------------------------------------------------------

import { W, H, heatColor } from './config.js';
import { mulberry, TAU } from './utils.js';

export class Starfield {
  constructor() {
    const rnd = mulberry(1337);
    this.stars = [];
    // x range is set in draw() via gutter extent; store normalized 0..1
    for (let i = 0; i < 110; i++) {
      const depth = rnd();
      this.stars.push({
        u: rnd(),                       // 0..1 across painted width (field + gutters)
        y: rnd() * H,
        z: depth,                       // parallax depth 0..1
        r: 0.6 + depth * 1.9,
        tw: rnd() * TAU,                // twinkle phase
        ts: 0.4 + rnd() * 1.8,
      });
    }
    this.t = 0;
  }

  /** painted x range in world coords (field plus surrounding gutters) */
  range(gutter) {
    const g = Math.max(0, gutter || 0);
    return { x0: -g, x1: W + g };
  }

  update(dt, scroll, gy = 0) {
    this.t += dt;
    const y0 = -gy, y1 = H + gy;
    for (const s of this.stars) {
      s.y += (6 + s.z * 26 + scroll * (20 + s.z * 90)) * dt;
      if (s.y > y1) { s.y = y0; s.u = Math.random(); }
      if (s.y < y0 - 4) s.y = y0;
    }
  }

  draw(ctx, heat, gutterW = 0, gy = 0) {
    const { x0, x1 } = this.range(gutterW);
    const span = x1 - x0;
    ctx.save();
    for (const s of this.stars) {
      const tw = 0.45 + 0.55 * Math.sin(this.t * s.ts + s.tw) ** 2;
      // stars in the outer gutters are dimmed slightly (depth cue)
      const px = x0 + s.u * span;
      const inField = px >= 0 && px <= W;
      ctx.globalAlpha = (0.18 + s.z * 0.5) * tw * (inField ? 1 : 0.7);
      ctx.fillStyle = heat > 0.05 ? heatColor(heat * s.z, 80, 70) : '#8fb4ff';
      ctx.beginPath();
      ctx.arc(px, s.y, s.r, 0, TAU);
      ctx.fill();
    }
    ctx.restore();
  }
}
