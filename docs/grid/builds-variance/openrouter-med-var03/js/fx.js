/* ============================================================
   Overdrive Breakout — fx.js
   Juice systems: particle pool, floating score popups, screen
   shake, flash overlay, shockwave rings, ball trail.
   All pooled / capped to avoid per-frame allocation.
   ============================================================ */
'use strict';

class FX {
  constructor() {
    this.maxParticles = 900;
    this.particles = [];        // preallocated pool
    this.pHead = 0;
    for (let i = 0; i < this.maxParticles; i++) {
      this.particles.push({ alive: false });
    }
    this.popups = [];           // score text popups (small, capped 60)
    this.rings = [];            // shockwave rings
    this.shake = 0;             // current shake magnitude (px, logical)
    this.shakeX = 0; this.shakeY = 0;
    this.flash = 0;             // 0..1 white/color flash
    this.flashHue = 0;
    this.hitPause = 0;          // frames of hit-pause remaining
    this.trail = [];            // ball trail points (additive)
  }

  spawnParticle(x, y, vx, vy, life, size, color, kind) {
    // kind: 0 spark, 1 shard (rect), 2 ember (fades up)
    let p = null;
    for (let i = 0; i < this.maxParticles; i++) {
      const c = this.particles[(this.pHead + i) % this.maxParticles];
      if (!c.alive) { p = c; this.pHead = (this.pHead + i + 1) % this.maxParticles; break; }
    }
    if (!p) return; // pool full: silently drop
    p.alive = true; p.x = x; p.y = y; p.vx = vx; p.vy = vy;
    p.life = life; p.maxLife = life; p.size = size; p.color = color;
    p.kind = kind || 0; p.rot = Math.random() * 6.28; p.vr = (Math.random() - 0.5) * 0.3;
    p.grav = kind === 2 ? -0.02 : 0.09;
  }

  burst(x, y, n, color, power) {
    n = Math.min(n, 40);
    for (let i = 0; i < n; i++) {
      const a = Math.random() * Math.PI * 2;
      const s = (0.5 + Math.random()) * power;
      this.spawnParticle(x, y, Math.cos(a) * s, Math.sin(a) * s,
        0.4 + Math.random() * 0.5, 2 + Math.random() * 4, color, Math.random() < 0.5 ? 0 : 1);
    }
  }

  shards(x, y, n, color, power) {
    n = Math.min(n, 24);
    for (let i = 0; i < n; i++) {
      const a = Math.random() * Math.PI * 2;
      const s = (0.5 + Math.random()) * power;
      this.spawnParticle(x, y, Math.cos(a) * s, Math.sin(a) * s - 0.5,
        0.6 + Math.random() * 0.7, 4 + Math.random() * 7, color, 1);
    }
  }

  popup(x, y, text, color, size) {
    // stack near-concurrent popups so they don't overlap
    let yy = y;
    for (const p of this.popups) {
      if (p.maxLife === 0.9 && Math.hypot(p.x - x, p.y - yy) < 60) yy -= 34;
    }
    if (this.popups.length > 60) this.popups.shift();
    this.popups.push({ x, y: yy, text, color, size: size || 26, life: 0.9, maxLife: 0.9, vy: -60 });
  }

  ring(x, y, maxR, color, width) {
    this.rings.push({ x, y, r: 4, maxR, color, width: width || 5, life: 0.5, maxLife: 0.5 });
  }

  addShake(amount) { this.shake = Math.min(34, this.shake + amount); }

  doFlash(hue) { this.flash = 1; this.flashHue = hue || 0; }

  addHitPause(frames) { this.hitPause = Math.max(this.hitPause, frames); }

  update(dt) {
    if (this.hitPause > 0) { this.hitPause -= dt * 60; } // consumes substeps
    // particles
    for (let i = 0; i < this.maxParticles; i++) {
      const p = this.particles[i];
      if (!p.alive) continue;
      p.life -= dt;
      if (p.life <= 0) { p.alive = false; continue; }
      p.vy += p.grav;
      p.x += p.vx; p.y += p.vy; p.rot += p.vr;
    }
    // popups
    for (let i = this.popups.length - 1; i >= 0; i--) {
      const u = this.popups[i];
      u.life -= dt; u.y += u.vy * dt; u.vy *= 0.94;
      if (u.life <= 0) this.popups.splice(i, 1);
    }
    // rings
    for (let i = this.rings.length - 1; i >= 0; i--) {
      const r = this.rings[i];
      r.life -= dt;
      r.r += (r.maxR - r.r) * Math.min(1, dt * 9);
      if (r.life <= 0) this.rings.splice(i, 1);
    }
    // shake decay
    this.shake *= Math.pow(0.001, dt); // fast exponential decay
    if (this.shake < 0.2) this.shake = 0;
    const s = this.shake;
    this.shakeX = (Math.random() - 0.5) * s;
    this.shakeY = (Math.random() - 0.5) * s;
    this.flash *= Math.pow(0.01, dt);
  }

  drawParticles(ctx) {
    ctx.globalCompositeOperation = 'lighter';
    for (let i = 0; i < this.maxParticles; i++) {
      const p = this.particles[i];
      if (!p.alive) continue;
      const a = Math.max(0, p.life / p.maxLife);
      ctx.globalAlpha = a;
      ctx.fillStyle = p.color;
      if (p.kind === 1) { // shard: rotated rect
        ctx.save();
        ctx.translate(p.x, p.y); ctx.rotate(p.rot);
        ctx.fillRect(-p.size / 2, -p.size / 2, p.size, p.size * 0.6);
        ctx.restore();
      } else {
        ctx.beginPath();
        ctx.arc(p.x, p.y, p.size * a + 0.5, 0, 6.283);
        ctx.fill();
      }
    }
    ctx.globalAlpha = 1;
    ctx.globalCompositeOperation = 'source-over';
  }

  drawPopups(ctx) {
    ctx.textAlign = 'center';
    for (const u of this.popups) {
      const a = Math.min(1, u.life / u.maxLife * 1.6);
      ctx.globalAlpha = a;
      ctx.font = '700 ' + u.size + 'px system-ui, sans-serif';
      ctx.fillStyle = '#0008';
      ctx.fillText(u.text, u.x + 2, u.y + 2);
      ctx.fillStyle = u.color;
      ctx.fillText(u.text, u.x, u.y);
    }
    ctx.globalAlpha = 1;
  }

  drawRings(ctx) {
    ctx.globalCompositeOperation = 'lighter';
    for (const r of this.rings) {
      ctx.globalAlpha = Math.max(0, r.life / r.maxLife);
      ctx.strokeStyle = r.color;
      ctx.lineWidth = r.width;
      ctx.beginPath();
      ctx.arc(r.x, r.y, r.r, 0, 6.283);
      ctx.stroke();
    }
    ctx.globalAlpha = 1;
    ctx.globalCompositeOperation = 'source-over';
  }

  drawFlash(ctx, W, H) {
    if (this.flash > 0.01) {
      ctx.globalCompositeOperation = 'lighter';
      ctx.globalAlpha = this.flash * 0.55;
      ctx.fillStyle = 'hsl(' + this.flashHue + ',90%,60%)';
      ctx.fillRect(0, 0, W, H);
      ctx.globalAlpha = 1;
      ctx.globalCompositeOperation = 'source-over';
    }
  }
}

window.FX = FX;