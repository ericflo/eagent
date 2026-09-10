// Particle system with object pooling.
export class Particles {
  constructor(max = 2400) {
    this.max = max;
    this.pool = [];
    this.active = [];
    for (let i = 0; i < max; i++) this.pool.push({ x:0,y:0,vx:0,vy:0,life:0,ttl:1,size:2,color:'#fff',grav:0,drag:1,shape:0,glow:1 });
  }
  spawn(o) {
    const p = this.pool.length ? this.pool.pop() : this.active[0]; // recycle oldest if full
    if (!this.pool.length) this.active.shift();
    p.x = o.x; p.y = o.y;
    const a = o.angle !== undefined ? o.angle : Math.random() * Math.PI * 2;
    const sp = o.speed !== undefined ? o.speed : 120;
    p.vx = Math.cos(a) * sp; p.vy = Math.sin(a) * sp;
    if (o.spread) { p.vx += (Math.random() - .5) * o.spread; p.vy += (Math.random() - .5) * o.spread; }
    p.life = 0; p.ttl = o.ttl || 0.6;
    p.size = o.size || 3; p.color = o.color || '#fff';
    p.grav = o.grav !== undefined ? o.grav : 260;
    p.drag = o.drag !== undefined ? o.drag : 0.995;
    p.shape = o.shape || 0; // 0 = square debris, 1 = spark line, 2 = glow dot
    p.glow = o.glow !== undefined ? o.glow : 1;
    this.active.push(p);
  }
  burst(x, y, color, n, opts = {}) {
    for (let i = 0; i < n; i++) {
      this.spawn({
        x, y, color,
        speed: (opts.speed || 180) * (0.4 + Math.random() * 0.9),
        ttl: (opts.ttl || 0.55) * (0.6 + Math.random() * 0.8),
        size: (opts.size || 4) * (0.6 + Math.random()),
        shape: opts.shape || 0, grav: opts.grav, drag: opts.drag,
      });
    }
  }
  update(dt) {
    const act = this.active;
    for (let i = act.length - 1; i >= 0; i--) {
      const p = act[i];
      p.life += dt;
      if (p.life >= p.ttl) { act.splice(i, 1); this.pool.push(p); continue; }
      p.vy += p.grav * dt;
      const d = Math.pow(p.drag, dt * 60);
      p.vx *= d; p.vy *= d;
      p.x += p.vx * dt; p.y += p.vy * dt;
    }
  }
  draw(ctx) {
    for (const p of this.active) {
      const t = 1 - p.life / p.ttl;
      ctx.globalAlpha = Math.min(1, t * 1.6);
      ctx.fillStyle = p.color;
      if (p.shape === 1) {
        ctx.strokeStyle = p.color; ctx.lineWidth = Math.max(1, p.size * t * .7);
        ctx.beginPath(); ctx.moveTo(p.x, p.y);
        ctx.lineTo(p.x - p.vx * 0.03, p.y - p.vy * 0.03); ctx.stroke();
      } else if (p.shape === 2) {
        ctx.shadowColor = p.color; ctx.shadowBlur = p.glow ? 10 : 0;
        ctx.beginPath(); ctx.arc(p.x, p.y, p.size * t, 0, 7); ctx.fill();
        ctx.shadowBlur = 0;
      } else {
        ctx.fillRect(p.x - p.size / 2, p.y - p.size / 2, p.size * t, p.size * t);
      }
    }
    ctx.globalAlpha = 1;
  }
  clear() { while (this.active.length) this.pool.push(this.active.pop()); }
}
