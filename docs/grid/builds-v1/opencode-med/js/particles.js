// ---------------------------------------------------------------------------
// particles.js — pooled particles, score popups, floating texts
// ---------------------------------------------------------------------------

import { rand, TAU, clamp } from './utils.js';

const MAX = 900;

export class Particles {
  constructor() {
    this.pool = [];
    this.n = 0;
    this.items = new Array(MAX);
    for (let i = 0; i < MAX; i++) this.items[i] = { alive: false };
    this.texts = [];   // {x,y,vy,t,life,txt,size,color}
    this.rings = [];   // impact ripples {x,y,t,life,r0,r1,color,w}
  }

  spawn(x, y, vx, vy, life, size, color, opts = {}) {
    const it = this.items[this.n];
    this.n = (this.n + 1) % MAX;
    it.alive = true;
    it.x = x; it.y = y; it.vx = vx; it.vy = vy;
    it.life = life; it.t = 0;
    it.size = size; it.color = color;
    it.drag = opts.drag ?? 1.6;
    it.grav = opts.grav ?? 320;
    it.glow = opts.glow ?? 0;
    it.shape = opts.shape || 'rect';
    it.spin = opts.spin ?? rand(-8, 8);
    it.rot = opts.rot ?? rand(TAU);
  }

  burst(x, y, color, count, opts = {}) {
    const spd = opts.speed ?? 260;
    for (let i = 0; i < count; i++) {
      const a = rand(TAU);
      const s = spd * rand(0.25, 1);
      this.spawn(x, y, Math.cos(a) * s, Math.sin(a) * s - (opts.up ?? 40),
        rand(0.35, 0.85) * (opts.life ?? 1), rand(2.5, 6), color, opts);
    }
  }

  debris(x, y, w, h, color, count = 12) {
    for (let i = 0; i < count; i++) {
      this.spawn(x + rand(0, w), y + rand(0, h),
        rand(-240, 240), rand(-320, 40), rand(0.5, 1.0), rand(3, 8),
        color, { shape: 'rect', grav: 620, drag: 0.6 });
    }
  }

  ring(x, y, r0, r1, life, color, width = 3) {
    this.rings.push({ x, y, r0, r1, t: 0, life, color, width });
  }

  text(x, y, txt, color, size = 26, life = 0.9) {
    this.texts.push({ x, y, vy: -70, t: 0, life, txt, size, color });
  }

  update(dt) {
    for (let i = 0; i < MAX; i++) {
      const it = this.items[i];
      if (!it.alive) continue;
      it.t += dt;
      if (it.t >= it.life) { it.alive = false; continue; }
      const d = Math.exp(-it.drag * dt);
      it.vx *= d; it.vy *= d;
      it.vy += it.grav * dt;
      it.x += it.vx * dt; it.y += it.vy * dt;
      it.rot += it.spin * dt;
    }
    for (let i = this.rings.length - 1; i >= 0; i--) {
      const r = this.rings[i];
      r.t += dt;
      if (r.t >= r.life) this.rings.splice(i, 1);
    }
    for (let i = this.texts.length - 1; i >= 0; i--) {
      const t = this.texts[i];
      t.t += dt;
      t.y += t.vy * dt;
      t.vy *= Math.exp(-2 * dt);
      if (t.t >= t.life) this.texts.splice(i, 1);
    }
  }

  draw(ctx, alphaMul = 1) {
    // rings
    for (const r of this.rings) {
      const p = r.t / r.life;
      const rad = r.r0 + (r.r1 - r.r0) * (1 - Math.pow(1 - p, 2.2));
      ctx.globalAlpha = (1 - p) * alphaMul;
      ctx.strokeStyle = r.color;
      ctx.lineWidth = r.width * (1 - p * 0.6);
      ctx.beginPath();
      ctx.arc(r.x, r.y, Math.max(1, rad), 0, TAU);
      ctx.stroke();
    }
    // particles
    for (let i = 0; i < MAX; i++) {
      const it = this.items[i];
      if (!it.alive) continue;
      const p = it.t / it.life;
      const a = (1 - p) * alphaMul;
      ctx.globalAlpha = a;
      ctx.fillStyle = it.color;
      if (it.glow) {
        ctx.shadowColor = it.color;
        ctx.shadowBlur = it.glow;
      }
      const s = it.size * (it.shape === 'rect' ? 1 : (1 - p * 0.5));
      if (it.shape === 'rect') {
        ctx.save();
        ctx.translate(it.x, it.y);
        ctx.rotate(it.rot);
        ctx.fillRect(-s / 2, -s / 2, s, s * 0.75);
        ctx.restore();
      } else {
        ctx.beginPath();
        ctx.arc(it.x, it.y, Math.max(0.5, s), 0, TAU);
        ctx.fill();
      }
      if (it.glow) ctx.shadowBlur = 0;
    }
    // floating text
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    for (const t of this.texts) {
      const p = t.t / t.life;
      ctx.globalAlpha = (1 - p * p) * alphaMul;
      const grow = 1 + 0.25 * Math.sin(Math.min(1, p * 3) * Math.PI);
      ctx.font = `800 ${t.size * grow}px ui-rounded, system-ui, sans-serif`;
      ctx.fillStyle = t.color;
      ctx.strokeStyle = 'rgba(5,8,18,0.75)';
      ctx.lineWidth = 4;
      ctx.strokeText(t.txt, t.x, t.y);
      ctx.fillText(t.txt, t.x, t.y);
    }
    ctx.globalAlpha = 1;
    ctx.shadowBlur = 0;
  }

  clear() {
    for (let i = 0; i < MAX; i++) this.items[i].alive = false;
    this.rings.length = 0;
    this.texts.length = 0;
    this.n = 0;
  }
}
