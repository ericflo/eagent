// Particle system + floating score popups. Capped pools, additive glow feel.
export class Particles {
  constructor(max = 600) {
    this.max = max;
    this.pool = [];
    this.popups = [];
  }

  get count() { return this.pool.length; }

  spawn(x, y, vx, vy, life, size, color, opts = {}) {
    if (this.pool.length >= this.max) this.pool.shift();
    this.pool.push({ x, y, vx, vy, life, t: 0, size, color,
      grav: opts.grav ?? 0, drag: opts.drag ?? 0, shape: opts.shape ?? 'square', spin: opts.spin ?? 0, rot: Math.random() * 6.28 });
  }

  burst(x, y, n, color, speed = 160, opts = {}) {
    for (let i = 0; i < n; i++) {
      const a = Math.random() * Math.PI * 2;
      const s = speed * (0.3 + Math.random() * 0.9);
      this.spawn(x, y, Math.cos(a) * s, Math.sin(a) * s,
        0.35 + Math.random() * 0.45, 2 + Math.random() * 3.5, color, opts);
    }
  }

  shatter(x, y, w, h, color, dir = 1) {
    const n = Math.min(16, Math.max(7, (w * h) / 220 | 0));
    for (let i = 0; i < n; i++) {
      this.spawn(x + (Math.random() - 0.5) * w, y + (Math.random() - 0.5) * h,
        (Math.random() - 0.5) * 220, (Math.random() * 140 + 30) * dir,
        0.4 + Math.random() * 0.5, 2 + Math.random() * 4.5, color, { grav: 460, spin: 8 });
    }
  }

  popup(x, y, text, color, size = 15) {
    this.popups.push({ x, y, text, color, size, t: 0, life: 0.9 });
    if (this.popups.length > 24) this.popups.shift();
  }

  update(dt) {
    for (let i = this.pool.length - 1; i >= 0; i--) {
      const p = this.pool[i];
      p.t += dt;
      if (p.t >= p.life) { this.pool.splice(i, 1); continue; }
      p.vy += p.grav * dt;
      if (p.drag) { const d = Math.max(0, 1 - p.drag * dt); p.vx *= d; p.vy *= d; }
      p.x += p.vx * dt; p.y += p.vy * dt; p.rot += p.spin * dt;
    }
    for (let i = this.popups.length - 1; i >= 0; i--) {
      const p = this.popups[i];
      p.t += dt; p.y -= 42 * dt;
      if (p.t >= p.life) this.popups.splice(i, 1);
    }
  }

  draw(ctx) {
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    for (const p of this.pool) {
      const a = 1 - p.t / p.life;
      ctx.globalAlpha = a;
      ctx.fillStyle = p.color;
      if (p.shape === 'circle') {
        ctx.beginPath();
        ctx.arc(p.x, p.y, p.size * (0.5 + a * 0.5), 0, 6.283);
        ctx.fill();
      } else {
        ctx.save();
        ctx.translate(p.x, p.y); ctx.rotate(p.rot);
        ctx.fillRect(-p.size / 2, -p.size / 2, p.size, p.size * a);
        ctx.restore();
      }
    }
    ctx.restore();
    for (const p of this.popups) {
      const a = 1 - p.t / p.life;
      ctx.save();
      ctx.globalAlpha = Math.min(1, a * 2);
      ctx.fillStyle = p.color;
      ctx.font = `700 ${p.size}px "Segoe UI", system-ui, sans-serif`;
      ctx.textAlign = 'center';
      ctx.shadowColor = p.color; ctx.shadowBlur = 8;
      ctx.fillText(p.text, p.x, p.y);
      ctx.restore();
    }
  }

  clear() { this.pool.length = 0; this.popups.length = 0; }
}
