// Canvas setup, DPR handling, resize, starfield background, screenshake.
export class CanvasMgr {
  constructor(canvas) {
    this.cv = canvas;
    this.ctx = canvas.getContext('2d');
    this.w = 0; this.h = 0;       // css pixels
    this.dpr = 1;
    this.shake = 0;               // screenshake magnitude
    this.shakeEnabled = true;
    this.flash = 0;               // white flash 0..1
    this.flashColor = '255,255,255';
    this.chroma = 0;              // chromatic offset strength
    this.stars = [];
    this.starSpeed = 1;
    this._t = 0;
    this.resize();
  }

  resize() {
    const dpr = Math.min(window.devicePixelRatio || 1, 2.5);
    this.w = window.innerWidth;
    this.h = window.innerHeight;
    this.cv.width = Math.round(this.w * dpr);
    this.cv.height = Math.round(this.h * dpr);
    this.cv.style.width = this.w + 'px';
    this.cv.style.height = this.h + 'px';
    this.dpr = dpr;
    this.ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    // starfield
    const n = Math.min(170, (this.w * this.h) / 9000 | 0);
    this.stars = Array.from({ length: n }, () => ({
      x: Math.random() * this.w, y: Math.random() * this.h,
      z: 0.3 + Math.random() * 0.7, tw: Math.random() * 6.28
    }));
  }

  addShake(m) { if (this.shakeEnabled) this.shake = Math.min(22, this.shake + m); }
  addFlash(a, color = '255,255,255') { this.flash = Math.max(this.flash, a); this.flashColor = color; }
  addChroma(a) { this.chroma = Math.min(1, this.chroma + a); }

  update(dt) {
    this._t += dt;
    this.shake *= Math.pow(0.0018, dt);
    this.flash = Math.max(0, this.flash - dt * 3.2);
    this.chroma = Math.max(0, this.chroma - dt * 2);
  }

  // returns [sx, sy] screen offset for shake
  offset() {
    if (this.shake < 0.2) return [0, 0];
    return [(Math.random() - 0.5) * this.shake, (Math.random() - 0.5) * this.shake];
  }

  drawBackground(pulse, hueShift, multGlow) {
    const ctx = this.ctx;
    const t = this._t;
    // keep the palette in the blue→violet→magenta band (never drifts to muddy red/green)
    const hue = 236 + 14 * Math.sin(t * 0.21) + hueShift * 84;
    // animated gradient
    const g = ctx.createLinearGradient(0, 0, this.w * 0.3, this.h);
    g.addColorStop(0, `hsl(${238 + hueShift * 60}, 62%, ${7 + multGlow * 9}%)`);
    g.addColorStop(0.5, `hsl(${252 + hueShift * 60}, 58%, ${5 + multGlow * 7}%)`);
    g.addColorStop(1, `hsl(${226 + hueShift * 60}, 64%, ${8 + multGlow * 10}%)`);
    ctx.fillStyle = g;
    ctx.fillRect(0, 0, this.w, this.h);

    // beat pulse vignette
    if (pulse > 0.02) {
      ctx.save();
      ctx.globalAlpha = pulse * 0.22;
      const rg = ctx.createRadialGradient(this.w / 2, this.h * 0.35, 0, this.w / 2, this.h * 0.35, this.w * 0.7);
      rg.addColorStop(0, `hsl(${hue}, 90%, 60%)`);
      rg.addColorStop(1, 'transparent');
      ctx.fillStyle = rg;
      ctx.fillRect(0, 0, this.w, this.h);
      ctx.restore();
    }

    // starfield with parallax speed
    const sp = this.starSpeed;
    ctx.save();
    for (const s of this.stars) {
      s.y += (14 + s.z * 46) * sp * 0.016;
      s.tw += 0.05;
      if (s.y > this.h + 4) { s.y = -4; s.x = Math.random() * this.w; }
      ctx.globalAlpha = 0.25 + s.z * 0.45 + Math.sin(s.tw) * 0.15;
      ctx.fillStyle = s.z > 0.8 ? `hsl(${hue}, 80%, 80%)` : '#aabbff';
      ctx.fillRect(s.x, s.y, 1.6 + s.z, 1.6 + s.z);
    }
    ctx.restore();
  }

  applyOverlays() {
    const ctx = this.ctx;
    if (this.flash > 0.01) {
      ctx.fillStyle = `rgba(${this.flashColor},${this.flash * 0.55})`;
      ctx.fillRect(0, 0, this.w, this.h);
    }
    if (this.chroma > 0.02) {
      // cheap chromatic aberration: tinted edge bands
      const a = this.chroma * 0.35;
      ctx.save();
      ctx.globalCompositeOperation = 'screen';
      ctx.fillStyle = `rgba(255,0,60,${a})`;
      ctx.fillRect(0, 0, 8, this.h);
      ctx.fillStyle = `rgba(0,120,255,${a})`;
      ctx.fillRect(this.w - 8, 0, 8, this.h);
      ctx.restore();
    }
  }
}

export function roundedRect(ctx, x, y, w, h, r) {
  r = Math.min(r, w / 2, h / 2);
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + w, y, x + w, y + h, r);
  ctx.arcTo(x + w, y + h, x, y + h, r);
  ctx.arcTo(x, y + h, x, y, r);
  ctx.arcTo(x, y, x + w, y, r);
  ctx.closePath();
}

export function hsl(h, s, l, a = 1) { return `hsla(${h},${s}%,${l}%,${a})`; }
