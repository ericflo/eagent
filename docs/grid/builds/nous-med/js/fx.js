// Particle system (pooled, capped), floating score popups, screen shake.
import { W, H } from './constants.js';

const MAX_PARTICLES = 600;

export class FX {
  constructor() {
    this.parts = [];
    this.popups = [];
    this.shake = 0;
    this.shakeX = 0; this.shakeY = 0;
    this.flash = 0; this.flashColor = '#fff';
    this.hitStop = 0;
    this.slowmo = 1;
    this.reduced = matchMedia('(prefers-reduced-motion: reduce)').matches
      || localStorage.getItem('bt_reduced') === '1';
  }

  spawn(x, y, { n = 8, color = '#fff', speed = 200, life = 0.6, size = 3, grav = 400, spread = Math.PI * 2, dir = 0, glow = false } = {}) {
    if (this.reduced) { n = Math.ceil(n / 3); speed *= 0.6; }
    for (let i = 0; i < n; i++) {
      if (this.parts.length >= MAX_PARTICLES) this.parts.shift();
      const a = dir + (Math.random() - 0.5) * spread;
      const s = speed * (0.4 + Math.random() * 0.8);
      this.parts.push({
        x, y, vx: Math.cos(a) * s, vy: Math.sin(a) * s,
        life: life * (0.6 + Math.random() * 0.7), t: 0,
        size: size * (0.6 + Math.random() * 0.8), color, grav, glow,
      });
    }
  }

  popup(x, y, text, color = '#fff', big = false) {
    if (this.popups.length > 40) this.popups.shift();
    this.popups.push({ x, y, text, color, t: 0, life: big ? 1.2 : 0.8, big });
  }

  doShake(amount) {
    if (this.reduced) amount *= 0.3;
    this.shake = Math.min(26, this.shake + amount);
  }

  doFlash(color, a = 0.5) {
    if (this.reduced) a *= 0.4;
    this.flash = Math.max(this.flash, a);
    this.flashColor = color;
  }

  doHitStop(sec) { if (!this.reduced) this.hitStop = Math.max(this.hitStop, sec); }

  update(dt) {
    if (this.hitStop > 0) { this.hitStop -= dt; return; }
    this.shake *= Math.pow(0.001, dt);
    if (this.shake < 0.3) this.shake = 0;
    this.shakeX = (Math.random() - 0.5) * 2 * this.shake;
    this.shakeY = (Math.random() - 0.5) * 2 * this.shake;
    this.flash *= Math.pow(0.01, dt);
    for (let i = this.parts.length - 1; i >= 0; i--) {
      const p = this.parts[i];
      p.t += dt;
      if (p.t >= p.life) { this.parts.splice(i, 1); continue; }
      p.vy += p.grav * dt;
      p.x += p.vx * dt; p.y += p.vy * dt;
    }
    for (let i = this.popups.length - 1; i >= 0; i--) {
      const p = this.popups[i];
      p.t += dt; p.y -= 40 * dt;
      if (p.t >= p.life) this.popups.splice(i, 1);
    }
  }

  draw(ctx) {
    for (const p of this.parts) {
      const a = 1 - p.t / p.life;
      ctx.globalAlpha = a;
      if (p.glow) { ctx.shadowColor = p.color; ctx.shadowBlur = 8; }
      ctx.fillStyle = p.color;
      ctx.fillRect(p.x - p.size / 2, p.y - p.size / 2, p.size, p.size);
      ctx.shadowBlur = 0;
    }
    ctx.globalAlpha = 1;
    for (const p of this.popups) {
      const a = 1 - p.t / p.life;
      ctx.globalAlpha = a;
      ctx.fillStyle = p.color;
      ctx.font = (p.big ? 'bold 30px' : 'bold 16px') + ' ui-monospace, monospace';
      ctx.textAlign = 'center';
      ctx.shadowColor = p.color; ctx.shadowBlur = 12;
      ctx.fillText(p.text, p.x, p.y);
      ctx.shadowBlur = 0;
    }
    ctx.globalAlpha = 1;
    if (this.flash > 0.02) {
      ctx.globalAlpha = this.flash;
      ctx.fillStyle = this.flashColor;
      ctx.fillRect(0, 0, W, H);
      ctx.globalAlpha = 1;
    }
  }
}
