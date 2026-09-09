// Lightweight particle system with additive glow rendering.

export class Particles {
  constructor() {
    this.list = [];
  }

  clear() { this.list.length = 0; }

  burst(x, y, { count = 12, speed = 220, size = 4, life = 0.6, colors = ['#ffd166', '#ff9f43', '#ffffff'], spread = Math.PI * 2, angle = 0, gravity = 300, drag = 0.9, glow = true } = {}) {
    for (let i = 0; i < count; i++) {
      const a = angle + (Math.random() - 0.5) * spread;
      const sp = speed * (0.3 + Math.random() * 0.9);
      this.list.push({
        x, y,
        vx: Math.cos(a) * sp,
        vy: Math.sin(a) * sp,
        size: size * (0.5 + Math.random()),
        life: life * (0.6 + Math.random() * 0.6),
        maxLife: life,
        color: colors[(Math.random() * colors.length) | 0],
        gravity, drag, glow,
      });
    }
  }

  ring(x, y, { count = 24, speed = 300, size = 3, life = 0.5, colors = ['#7df9ff', '#ffffff'] } = {}) {
    for (let i = 0; i < count; i++) {
      const a = (i / count) * Math.PI * 2;
      this.list.push({
        x, y,
        vx: Math.cos(a) * speed * (0.6 + Math.random() * 0.6),
        vy: Math.sin(a) * speed * (0.6 + Math.random() * 0.6),
        size: size * (0.5 + Math.random()),
        life: life * (0.7 + Math.random() * 0.5),
        maxLife: life,
        color: colors[(Math.random() * colors.length) | 0],
        gravity: 0, drag: 0.96, glow: true,
      });
    }
  }

  update(dt) {
    const l = this.list;
    for (let i = l.length - 1; i >= 0; i--) {
      const p = l[i];
      p.life -= dt;
      if (p.life <= 0) { l.splice(i, 1); continue; }
      p.vy += p.gravity * dt;
      p.vx *= Math.pow(p.drag, dt * 60);
      p.vy *= Math.pow(p.drag, dt * 60);
      p.x += p.vx * dt;
      p.y += p.vy * dt;
    }
  }

  draw(ctx) {
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    for (const p of this.list) {
      const t = p.life / p.maxLife;
      ctx.globalAlpha = Math.max(t, 0) * 0.9;
      ctx.fillStyle = p.color;
      if (p.glow) {
        ctx.shadowColor = p.color;
        ctx.shadowBlur = 8;
      }
      ctx.beginPath();
      ctx.arc(p.x, p.y, Math.max(p.size * t, 0.4), 0, Math.PI * 2);
      ctx.fill();
    }
    ctx.restore();
  }
}
