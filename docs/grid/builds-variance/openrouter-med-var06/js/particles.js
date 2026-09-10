// Particle system: pooled simple particles + floating text + glow blobs.

export class Particles {
  constructor() { this.list = []; this.max = 900; }

  spawn(opts) {
    if (this.list.length >= this.max) this.list.shift();
    this.list.push({
      x: 0, y: 0, vx: 0, vy: 0, life: 1, decay: 1.5,
      size: 2, color: '#fff', grav: 0, glow: 0, shrink: true, ...opts
    });
  }

  burst(x, y, n, opts = {}) {
    for (let i = 0; i < n; i++) {
      const a = Math.random() * Math.PI * 2;
      const sp = (opts.speed || 30) * (0.3 + Math.random());
      this.spawn({
        x, y,
        vx: Math.cos(a) * sp + (opts.vx || 0),
        vy: Math.sin(a) * sp + (opts.vy || 0),
        life: 1, decay: opts.decay || (1.2 + Math.random() * 1.2),
        size: opts.size || (1 + Math.random() * 2.4),
        color: Array.isArray(opts.color) ? opts.color[(Math.random() * opts.color.length) | 0] : (opts.color || '#fff'),
        grav: opts.grav ?? 40,
        glow: opts.glow ?? 0.5,
      });
    }
  }

  text(x, y, str, color = '#ffd76c', size = 5, vy = -22) {
    this.list.push({ x, y, vx: 0, vy, life: 1, decay: 0.9, size, color, text: str, grav: 0, glow: 0.8, shrink: false });
  }

  update(dt) {
    for (let i = this.list.length - 1; i >= 0; i--) {
      const p = this.list[i];
      p.x += p.vx * dt; p.y += p.vy * dt;
      p.vy += p.grav * dt;
      p.vx *= (1 - 0.6 * dt); p.vy *= (1 - 0.15 * dt);
      p.life -= p.decay * dt;
      if (p.life <= 0) this.list.splice(i, 1);
    }
  }

  draw(ctx, scale) {
    for (const p of this.list) {
      const a = Math.max(0, Math.min(1, p.life));
      ctx.globalAlpha = a;
      if (p.text) {
        ctx.fillStyle = p.color;
        ctx.font = `900 ${p.size * scale}px system-ui, sans-serif`;
        ctx.textAlign = 'center';
        ctx.shadowColor = p.color; ctx.shadowBlur = 8 * p.glow;
        ctx.fillText(p.text, p.x * scale, p.y * scale);
        ctx.shadowBlur = 0;
      } else {
        const s = p.size * (p.shrink ? a : 1) * scale;
        ctx.fillStyle = p.color;
        if (p.glow) { ctx.shadowColor = p.color; ctx.shadowBlur = 6 * p.glow; }
        ctx.fillRect(p.x * scale - s / 2, p.y * scale - s / 2, s, s);
        ctx.shadowBlur = 0;
      }
    }
    ctx.globalAlpha = 1;
  }

  clear() { this.list.length = 0; }
}