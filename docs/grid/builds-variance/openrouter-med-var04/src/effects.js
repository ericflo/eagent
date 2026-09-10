// effects.js — particles, shards, shockwaves, popups, screen shake, hit-stop,
// flashes, slow-mo and the frenzy visual layer. All rendering is procedural.
import { WORLD_W, WORLD_H, lerp, easeOutCubic } from './engine.js';

export class Effects {
  constructor() {
    this.particles = [];   // {x,y,vx,vy,life,maxLife,size,color,gravity,spin,angle,type}
    this.shards = [];      // rotating brick fragments
    this.rings = [];       // shockwaves
    this.popups = [];      // score/combo text
    this.shake = 0;        // magnitude
    this.shakeT = 0;
    this.hitStop = 0;      // seconds of physics freeze
    this.flash = 0;        // 0..1 full-screen chromatic flash
    this.flashColor = '255,255,255';
    this.desat = 0;        // death desaturate flash
    this.slowMo = 0;       // seconds of 0.25x time
    this.frenzyGlow = 0;   // 0..1 visual frenzy warmth
  }

  update(dt) {
    if (this.hitStop > 0) {
      this.hitStop -= dt;
      return; // freeze everything but the timers
    }
    this.shakeT += dt;
    this.shake = Math.max(0, this.shake - dt * 30);
    this.flash = Math.max(0, this.flash - dt * 3.2);
    this.desat = Math.max(0, this.desat - dt * 1.4);
    if (this.slowMo > 0) this.slowMo -= dt;

    for (const p of this.particles) {
      p.life -= dt;
      p.x += p.vx * dt; p.y += p.vy * dt;
      p.vy += (p.gravity || 0) * dt;
      p.vx *= (1 - dt * 0.6); p.vy *= (1 - dt * 0.6);
      if (p.spin) p.angle += p.spin * dt;
    }
    this.particles = this.particles.filter(p => p.life > 0);

    for (const s of this.shards) {
      s.life -= dt;
      s.x += s.vx * dt; s.y += s.vy * dt;
      s.vy += 900 * dt;
      s.angle += s.spin * dt;
    }
    this.shards = this.shards.filter(s => s.life > 0);

    for (const r of this.rings) r.life -= dt;
    this.rings = this.rings.filter(r => r.life > 0);

    for (const p of this.popups) {
      p.life -= dt;
      p.y -= 46 * dt;
    }
    this.popups = this.popups.filter(p => p.life > 0);
  }

  burst(x, y, color, n = 12, speed = 260, opts = {}) {
    for (let i = 0; i < n; i++) {
      const a = Math.random() * Math.PI * 2;
      const v = speed * (0.4 + Math.random() * 0.8);
      this.particles.push({
        x, y, vx: Math.cos(a) * v, vy: Math.sin(a) * v,
        life: 0.35 + Math.random() * 0.35, maxLife: 0.7,
        size: 2 + Math.random() * 3, color,
        gravity: opts.gravity ?? 500,
        angle: Math.random() * Math.PI, spin: 0,
        type: 'spark',
      });
    }
  }

  shatter(x, y, w, h, color) {
    const cols = 4, rowsN = 2;
    for (let i = 0; i < cols; i++) {
      for (let j = 0; j < rowsN; j++) {
        this.shards.push({
          x: x + (i + 0.5) * w / cols, y: y + (j + 0.5) * h / rowsN,
          w: w / cols - 1, h: h / rowsN - 1, color,
          vx: (i - (cols - 1) / 2) * 90 + (Math.random() - 0.5) * 60,
          vy: -140 - Math.random() * 160,
          spin: (Math.random() - 0.5) * 14, angle: 0,
          life: 0.8 + Math.random() * 0.5,
        });
      }
    }
  }

  ring(x, y, color, maxR = 120, life = 0.4) {
    this.rings.push({ x, y, r: 6, maxR, life, maxLife: life, color });
  }

  popup(x, y, text, color = '#fff', size = 18, life = 0.65) {
    // only one combo popup at a time — a new "Combo N" replaces the previous
    if (/^Combo \d+$/.test(text)) {
      this.popups = this.popups.filter(p => !/^Combo \d+$/.test(p.text));
    }
    // cap concurrent popups: drop the oldest when over the limit
    if (this.popups.length >= 8) this.popups.shift();
    this.popups.push({ x, y, text, color, size, life, maxLife: life });
  }

  // fire trail embers: small orange/yellow sparks fading along the fire trail
  ember(x, y) {
    this.particles.push({
      x: x + (Math.random() - 0.5) * 6, y: y + (Math.random() - 0.5) * 6,
      vx: (Math.random() - 0.5) * 60, vy: -40 - Math.random() * 90,
      life: 0.3 + Math.random() * 0.4, maxLife: 0.7,
      size: 1.2 + Math.random() * 2.2,
      color: Math.random() < 0.4 ? '#fde047' : Math.random() < 0.7 ? '#fb923c' : '#ef4444',
      gravity: -60, angle: 0, spin: 0, type: 'spark',
    });
  }

  addShake(mag) { this.shake = Math.min(22, this.shake + mag); }
  addHitStop(sec) { this.hitStop = Math.max(this.hitStop, sec); }
  doFlash(color = '255,255,255', strength = 0.6) { this.flash = strength; this.flashColor = color; }
  doSlowMo(sec = 0.8) { this.slowMo = Math.max(this.slowMo, sec); }

  // Rain-upward particles while frenzy is active (called each frame with intensity)
  frenzyRain(intensity, dt) {
    if (Math.random() < intensity * dt * 40) {
      this.particles.push({
        x: Math.random() * WORLD_W, y: WORLD_H * (0.55 + Math.random() * 0.45),
        vx: (Math.random() - 0.5) * 30, vy: -120 - Math.random() * 180,
        life: 0.8 + Math.random() * 0.6, maxLife: 1.4,
        size: 1.5 + Math.random() * 2.5,
        color: Math.random() < 0.5 ? '#ffb347' : '#ffd700',
        gravity: -60, angle: 0, spin: 0, type: 'spark',
      });
    }
  }

  draw(ctx, alpha) {
    ctx.save();
    // screen shake (in world units)
    if (this.shake > 0.3) {
      const t = this.shakeT * 50;
      ctx.translate(Math.sin(t * 1.1) * this.shake * 0.5, Math.cos(t * 1.7) * this.shake * 0.5);
    }

    // shockwave rings
    for (const r of this.rings) {
      const p = 1 - r.life / r.maxLife;
      const rad = lerp(6, r.maxR, easeOutCubic(p));
      ctx.globalAlpha = (1 - p) * 0.8;
      ctx.strokeStyle = r.color;
      ctx.lineWidth = 3 * (1 - p) + 1;
      ctx.beginPath(); ctx.arc(r.x, r.y, rad, 0, Math.PI * 2); ctx.stroke();
    }
    ctx.globalAlpha = 1;

    // shards
    for (const s of this.shards) {
      ctx.save();
      ctx.translate(s.x, s.y);
      ctx.rotate(s.angle);
      ctx.globalAlpha = Math.min(1, s.life * 2);
      ctx.fillStyle = s.color;
      ctx.fillRect(-s.w / 2, -s.h / 2, s.w, s.h);
      ctx.restore();
    }
    ctx.globalAlpha = 1;

    // particles
    for (const p of this.particles) {
      const a = Math.max(0, p.life / p.maxLife);
      ctx.globalAlpha = a;
      ctx.fillStyle = p.color;
      ctx.beginPath();
      ctx.arc(p.x, p.y, p.size * (0.5 + a * 0.5), 0, Math.PI * 2);
      ctx.fill();
    }
    ctx.globalAlpha = 1;

    // popups
    for (const p of this.popups) {
      const a = Math.min(1, p.life / (p.maxLife * 0.5));
      ctx.globalAlpha = a;
      ctx.font = `bold ${p.size}px system-ui, sans-serif`;
      ctx.textAlign = 'center';
      ctx.lineWidth = 3; ctx.strokeStyle = 'rgba(0,0,0,0.6)';
      ctx.strokeText(p.text, p.x, p.y);
      ctx.fillStyle = p.color;
      ctx.fillText(p.text, p.x, p.y);
    }
    ctx.globalAlpha = 1;

    // frenzy warmth overlay
    if (this.frenzyGlow > 0.01) {
      const g = ctx.createLinearGradient(0, 0, 0, WORLD_H);
      g.addColorStop(0, `rgba(255,150,30,${0.10 * this.frenzyGlow})`);
      g.addColorStop(1, `rgba(255,60,0,${0.03 * this.frenzyGlow})`);
      ctx.fillStyle = g;
      ctx.fillRect(0, 0, WORLD_W, WORLD_H);
    }

    // vignette
    const vg = ctx.createRadialGradient(WORLD_W / 2, WORLD_H / 2, WORLD_H * 0.35,
      WORLD_W / 2, WORLD_H / 2, WORLD_H * 0.72);
    vg.addColorStop(0, 'rgba(0,0,0,0)');
    vg.addColorStop(1, `rgba(0,0,0,${0.35 + this.frenzyGlow * 0.1})`);
    ctx.fillStyle = vg;
    ctx.fillRect(0, 0, WORLD_W, WORLD_H);

    ctx.restore();

    // full-screen flashes (not shaken)
    if (this.flash > 0.01) {
      ctx.globalAlpha = Math.min(0.55, this.flash * 0.55);
      ctx.fillStyle = `rgb(${this.flashColor})`;
      ctx.fillRect(0, 0, WORLD_W, WORLD_H);
      // chromatic edge
      ctx.globalAlpha = Math.min(0.3, this.flash * 0.3);
      ctx.strokeStyle = 'rgba(80,180,255,0.9)';
      ctx.lineWidth = 14;
      ctx.strokeRect(0, 0, WORLD_W, WORLD_H);
      ctx.globalAlpha = 1;
    }
    if (this.desat > 0.01) {
      ctx.globalAlpha = Math.min(0.6, this.desat);
      ctx.fillStyle = '#666';
      ctx.fillRect(0, 0, WORLD_W, WORLD_H);
      ctx.globalAlpha = 1;
    }
  }
}
