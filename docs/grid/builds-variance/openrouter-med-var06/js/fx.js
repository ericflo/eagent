// FX: screen shake, flashes, background, blast rings, whoosh.

export class FX {
  constructor() {
    this.shakeT = 0; this.shakeMag = 0;
    this.flashT = 0; this.flashColor = '#fff'; this.flashMax = 0;
    this.blasts = []; // {x,y,t}
    this.pulse = 0;
  }

  shake(n) { this.shakeMag = Math.min(10, this.shakeMag + n); this.shakeT = 1; }
  flash(c, amt = 0.3) { this.flashColor = c; this.flashT = 1; this.flashMax = amt; }
  blast(x, y) { this.blasts.push({ x, y, t: 0 }); }
  whoosh(x, y) { this.blasts.push({ x, y, t: 0, soft: true }); }

  update(dt) {
    if (this.shakeT > 0) {
      this.shakeT -= dt * 5;
      this.shakeMag *= Math.max(0, 1 - dt * 9);
      if (this.shakeT <= 0) this.shakeMag = 0;
    }
    if (this.flashT > 0) this.flashT -= dt * 3;
    for (let i = this.blasts.length - 1; i >= 0; i--) {
      this.blasts[i].t += dt;
      if (this.blasts[i].t > 0.45) this.blasts.splice(i, 1);
    }
    this.pulse = Math.max(0, this.pulse - dt);
  }

  shakeOffset() {
    if (this.shakeMag <= 0.01) return { x: 0, y: 0 };
    return {
      x: (Math.random() - 0.5) * this.shakeMag,
      y: (Math.random() - 0.5) * this.shakeMag,
    };
  }

  // Background: dark gradient + subtle grid + rush pulse tint.
  drawBG(ctx, w, h, time, rush) {
    // base
    const g = ctx.createLinearGradient(0, 0, 0, h);
    const hue = 230 - rush * 40;
    g.addColorStop(0, `hsl(${hue}, 45%, ${7 + rush * 6}%)`);
    g.addColorStop(1, `hsl(${hue + 15}, 40%, ${4 + rush * 3}%)`);
    ctx.fillStyle = g;
    ctx.fillRect(0, 0, w, h);

    // rush radial glow from top
    if (rush > 0.05) {
      const rg = ctx.createRadialGradient(w / 2, 0, 0, w / 2, 0, h * (0.4 + rush * 0.3));
      const a = rush * (0.25 + 0.12 * Math.sin(time * (6 + rush * 10)));
      rg.addColorStop(0, `rgba(255,215,108,${a})`);
      rg.addColorStop(0.5, `rgba(200,108,255,${a * 0.5})`);
      rg.addColorStop(1, 'rgba(0,0,0,0)');
      ctx.fillStyle = rg;
      ctx.fillRect(0, 0, w, h);
    }

    // faint grid
    ctx.strokeStyle = `rgba(120,150,220,${0.05 + rush * 0.04})`;
    ctx.lineWidth = 1;
    const step = 60;
    ctx.beginPath();
    for (let x = 0; x < w; x += step) { ctx.moveTo(x, 0); ctx.lineTo(x, h); }
    for (let y = 0; y < h; y += step) { ctx.moveTo(0, y); ctx.lineTo(w, y); }
    ctx.stroke();
  }

  drawOverlays(ctx, W, H, rush, time) {
    // blast rings
    for (const b of this.blasts) {
      const p = b.t / 0.45;
      const r = b.soft ? 4 + p * 14 : 2 + p * 22;
      ctx.save();
      ctx.globalAlpha = (1 - p) * (b.soft ? 0.5 : 0.8);
      ctx.strokeStyle = b.soft ? 'rgba(160,220,255,1)' : '#ffae42';
      ctx.lineWidth = b.soft ? 1 : 2;
      ctx.shadowColor = ctx.strokeStyle; ctx.shadowBlur = 10;
      ctx.beginPath(); ctx.arc(b.x, b.y, r, 0, Math.PI * 2); ctx.stroke();
      ctx.restore();
    }
    // rush tint at screen edges
    if (rush > 0.4) {
      const a = (rush - 0.4) * 0.5 * (0.7 + 0.3 * Math.sin(time * 8));
      const g = ctx.createLinearGradient(0, 0, 0, H);
      g.addColorStop(0, `rgba(255,180,60,${a * 0.35})`);
      g.addColorStop(0.6, 'rgba(0,0,0,0)');
      g.addColorStop(1, `rgba(200,80,255,${a * 0.25})`);
      ctx.fillStyle = g;
      ctx.fillRect(0, 0, W, H);
    }
    // flash
    if (this.flashT > 0) {
      ctx.globalAlpha = this.flashT * this.flashMax;
      ctx.fillStyle = this.flashColor;
      ctx.fillRect(0, 0, W, H);
      ctx.globalAlpha = 1;
    }
  }
}