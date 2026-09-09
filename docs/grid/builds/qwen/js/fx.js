// fx.js — all juice: pooled particle system (shards, additive sparks, smoke,
// confetti), floating text, screen shake (exponential decay), hit-stop,
// pre-rendered glow sprites + ball trail helper, parallax starfield +
// nebula background whose hue shifts with overdrive, and a vignette.
// Everything is scaled by juiceLevel = meter/100.

import { FIELD, FX as FXCFG } from './config.js';
import { clamp, rand, TAU, lerp } from './utils.js';

// ------------------------------------------------------- Pre-rendered sprites

// Soft radial glow sprite (white core → transparent), tinted via composite.
function makeGlowSprite(size = 128, inner = 'rgba(255,255,255,1)') {
  const cv = document.createElement('canvas');
  cv.width = cv.height = size;
  const ctx = cv.getContext('2d');
  const g = ctx.createRadialGradient(size / 2, size / 2, 0, size / 2, size / 2, size / 2);
  g.addColorStop(0, 'rgba(255,255,255,0.9)');
  g.addColorStop(0.25, 'rgba(140,240,255,0.55)');
  g.addColorStop(0.6, 'rgba(80,180,255,0.18)');
  g.addColorStop(1, 'rgba(60,140,255,0)');
  ctx.fillStyle = g;
  ctx.fillRect(0, 0, size, size);
  return cv;
}

// Star layer: N random stars on an offscreen canvas.
function makeStarLayer(count, size, brightness) {
  const cv = document.createElement('canvas');
  cv.width = FIELD.W; cv.height = FIELD.H;
  const ctx = cv.getContext('2d');
  for (let i = 0; i < count; i++) {
    const x = Math.random() * FIELD.W, y = Math.random() * FIELD.H;
    const r = Math.random() * 1.6 + 0.4;
    ctx.globalAlpha = Math.random() * 0.7 * brightness + 0.15;
    ctx.fillStyle = Math.random() < 0.15 ? '#ffd9a0' : (Math.random() < 0.3 ? '#a0d4ff' : '#ffffff');
    ctx.beginPath();
    ctx.arc(x, y, r, 0, TAU);
    ctx.fill();
  }
  return cv;
}

export class FX {
  constructor() {
    this.particles = [];        // active list
    this.pool = [];             // free objects
    this.texts = [];            // floating text
    this.textPool = [];
    this.shake = 0;
    this.shakeX = 0;
    this.shakeY = 0;
    this.hitStop = 0;           // seconds of physics freeze remaining
    this.hitStopMax = 0;
    this.flash = 0;             // full-screen flash 0..1 (life lost, max bank)
    this.flashColor = '255,255,255';

    // Pre-rendered assets
    this.ballGlow = makeGlowSprite(128);
    this.starLayers = [
      makeStarLayer(70, 0, 0.6),   // far, dim
      makeStarLayer(45, 0, 0.8),   // mid
      makeStarLayer(25, 0, 1.0)    // near, bright
    ];
    this.starSpeeds = [6, 14, 28]; // parallax drift px/s
    this.vignette = this._makeVignette();
  }

  _makeVignette() {
    const cv = document.createElement('canvas');
    cv.width = FIELD.W; cv.height = FIELD.H;
    const ctx = cv.getContext('2d');
    const g = ctx.createRadialGradient(
      FIELD.W / 2, FIELD.H / 2, FIELD.H * 0.35,
      FIELD.W / 2, FIELD.H / 2, FIELD.H * 0.85);
    g.addColorStop(0, 'rgba(0,0,0,0)');
    g.addColorStop(1, 'rgba(0,0,10,0.55)');
    ctx.fillStyle = g;
    ctx.fillRect(0, 0, FIELD.W, FIELD.H);
    return cv;
  }

  // ------------------------------------------------------------- Hit-stop

  addHitStop(seconds) {
    this.hitStop = Math.max(this.hitStop, seconds);
  }

  consumeHitStop(dt) {
    if (this.hitStop > 0) {
      this.hitStop -= dt;
      return this.hitStop > 0; // still frozen this frame
    }
    return false;
  }

  // ------------------------------------------------------------- Shake

  addShake(px) {
    this.shake = clamp(Math.max(this.shake, px), FXCFG.SHAKE_MIN, FXCFG.SHAKE_MAX);
  }

  _updateShake(dt) {
    if (this.shake > 0.05) {
      const decay = Math.exp(-6 * dt);
      this.shake *= decay;
      this.shakeX = (Math.random() * 2 - 1) * this.shake;
      this.shakeY = (Math.random() * 2 - 1) * this.shake;
    } else {
      this.shake = 0; this.shakeX = 0; this.shakeY = 0;
    }
  }

  // ------------------------------------------------------------- Particles
  // Pooled rotating quad shards, additive sparks, smoke, confetti.
  // kind: 'shard' | 'spark' | 'smoke' | 'confetti' | 'ring'

  _getParticle() {
    return this.pool.length ? this.pool.pop() :
      { active: false, kind: '', x: 0, y: 0, vx: 0, vy: 0, rot: 0, vrot: 0,
        w: 0, h: 0, life: 0, maxLife: 0, color: '#fff', size: 0, grow: 0, drag: 0 };
  }

  spawn(p) {
    if (this.particles.length >= FXCFG.PARTICLE_CAP) {
      // Recycle the oldest particle instead of dropping the new one
      const old = this.particles.shift();
      this.pool.push(old);
    }
    const o = this._getParticle();
    Object.assign(o, {
      active: true, kind: 'shard', rot: rand(0, TAU), vrot: rand(-8, 8),
      w: 6, h: 6, maxLife: 0.5, life: 0.5, color: '#fff', size: 2, grow: 0, drag: 0.5,
      gravity: 0
    }, p);
    this.particles.push(o);
  }

  // Rotating quad shards in a brick's color — the standard break burst.
  shards(x, y, color, count = 8, juice = 0) {
    const n = Math.round(count * (1 + juice * 0.8));
    for (let i = 0; i < n; i++) {
      const a = rand(0, TAU);
      const sp = rand(120, 420) * (1 + juice * 0.4);
      this.spawn({
        kind: 'shard', x, y,
        vx: Math.cos(a) * sp, vy: Math.sin(a) * sp - 60,
        w: rand(4, 11), h: rand(4, 9),
        life: rand(0.35, 0.7), maxLife: 0.7,
        color, gravity: 500, drag: 1.2
      });
    }
    // Additive core sparks
    this.sparks(x, y, color, Math.round(6 + juice * 6));
  }

  sparks(x, y, color, count = 8) {
    for (let i = 0; i < count; i++) {
      const a = rand(0, TAU);
      const sp = rand(200, 700);
      this.spawn({
        kind: 'spark', x, y,
        vx: Math.cos(a) * sp, vy: Math.sin(a) * sp,
        life: rand(0.15, 0.4), maxLife: 0.4,
        color, size: rand(1.5, 3.5), drag: 2.0, gravity: 0
      });
    }
  }

  smoke(x, y, count = 4, color = 'rgba(160,170,190,') {
    for (let i = 0; i < count; i++) {
      const a = rand(0, TAU);
      const sp = rand(20, 90);
      this.spawn({
        kind: 'smoke', x, y,
        vx: Math.cos(a) * sp, vy: Math.sin(a) * sp - 30,
        life: rand(0.5, 1.1), maxLife: 1.1,
        color, size: rand(8, 18), grow: rand(10, 30), drag: 1.0, gravity: -40
      });
    }
  }

  confetti(x, y, count = 30) {
    const colors = ['#ff4d5e', '#ffd93d', '#4dffc3', '#4d8dff', '#c44dff', '#4dff6a'];
    for (let i = 0; i < count; i++) {
      const a = rand(-Math.PI * 0.85, -Math.PI * 0.15);
      const sp = rand(250, 750);
      this.spawn({
        kind: 'confetti', x, y,
        vx: Math.cos(a) * sp, vy: Math.sin(a) * sp,
        w: rand(5, 9), h: rand(8, 14),
        life: rand(0.8, 1.6), maxLife: 1.6,
        color: colors[i % colors.length],
        gravity: 700, drag: 0.6,
        vrot: rand(-14, 14)
      });
    }
  }

  // Expanding ring (used at band 80+ per bounce)
  ring(x, y, color, size = 60) {
    this.spawn({ kind: 'ring', x, y, life: 0.35, maxLife: 0.35, color, size, grow: size * 3 });
  }

  update(dt) {
    this._updateShake(dt);
    this.flash = Math.max(0, this.flash - dt * 2.5);
    for (let i = this.particles.length - 1; i >= 0; i--) {
      const p = this.particles[i];
      p.life -= dt;
      if (p.life <= 0) {
        p.active = false;
        this.pool.push(p);
        this.particles.splice(i, 1);
        continue;
      }
      const drag = Math.exp(-(p.drag || 0) * dt);
      p.vx *= drag;
      p.vy = p.vy * drag + (p.gravity || 0) * dt;
      p.x += p.vx * dt;
      p.y += p.vy * dt;
      p.rot += (p.vrot || 0) * dt;
      if (p.kind === 'smoke' || p.kind === 'ring') p.size += (p.grow || 0) * dt;
    }
    // Floating texts
    for (let i = this.texts.length - 1; i >= 0; i--) {
      const t = this.texts[i];
      t.life -= dt;
      t.y -= t.vy * dt;
      if (t.life <= 0) {
        this.texts.splice(i, 1);
        this.textPool.push(t);
      }
    }
  }

  drawParticles(ctx, additive) {
    // Shards, smoke, confetti: normal composite first
    for (const p of this.particles) {
      const t = p.life / p.maxLife;
      if (p.kind === 'shard') {
        ctx.save();
        ctx.globalAlpha = Math.min(1, t * 1.5);
        ctx.translate(p.x, p.y);
        ctx.rotate(p.rot);
        ctx.fillStyle = p.color;
        ctx.fillRect(-p.w / 2, -p.h / 2, p.w, p.h);
        ctx.restore();
      } else if (p.kind === 'smoke') {
        ctx.globalAlpha = t * 0.25;
        ctx.fillStyle = p.color + '1)';
        ctx.beginPath();
        ctx.arc(p.x, p.y, p.size, 0, TAU);
        ctx.fill();
        ctx.globalAlpha = 1;
      } else if (p.kind === 'confetti') {
        ctx.save();
        ctx.globalAlpha = Math.min(1, t * 1.4);
        ctx.translate(p.x, p.y);
        ctx.rotate(p.rot);
        ctx.fillStyle = p.color;
        ctx.fillRect(-p.w / 2, -p.h / 2, p.w, p.h);
        ctx.restore();
      }
    }
    if (additive) {
      ctx.save();
      ctx.globalCompositeOperation = 'lighter';
      for (const p of this.particles) {
        const t = p.life / p.maxLife;
        if (p.kind === 'spark') {
          ctx.globalAlpha = t;
          ctx.fillStyle = p.color;
          ctx.beginPath();
          ctx.arc(p.x, p.y, p.size * t + 0.5, 0, TAU);
          ctx.fill();
        } else if (p.kind === 'ring') {
          ctx.globalAlpha = t;
          ctx.strokeStyle = p.color;
          ctx.lineWidth = 3 * t + 1;
          ctx.beginPath();
          ctx.arc(p.x, p.y, p.size + p.grow * (1 - t), 0, TAU);
          ctx.stroke();
        }
      }
      ctx.restore();
      ctx.globalAlpha = 1;
    }
  }

  // ------------------------------------------------------------- Text

  text(x, y, str, opts = {}) {
    if (this.texts.length > 24) this.textPool.splice(0, this.texts.length - 24);
    const t = this.textPool.pop() || {};
    Object.assign(t, {
      x, y, str,
      color: opts.color || '#ffffff',
      size: opts.size || 22,
      life: opts.life || 0.8,
      maxLife: opts.life || 0.8,
      vy: opts.vy != null ? opts.vy : 70,
      bold: !!opts.bold
    });
    this.texts.push(t);
  }

  drawTexts(ctx) {
    if (!this.texts.length) return;
    ctx.save();
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    for (const t of this.texts) {
      const a = Math.min(1, (t.life / t.maxLife) * 2);
      const pop = 1 + 0.3 * Math.max(0, t.life / t.maxLife - 0.7);
      ctx.globalAlpha = a;
      ctx.font = `${t.bold ? '900' : '700'} ${Math.round(t.size * pop)}px system-ui, sans-serif`;
      ctx.shadowColor = t.color;
      ctx.shadowBlur = 10;
      ctx.fillStyle = t.color;
      ctx.fillText(t.str, t.x, t.y);
    }
    ctx.restore();
  }

  // ------------------------------------------------------------- Background

  // Deep space with 3-layer parallax stars + nebula that hue-shifts with
  // overdrive (base hue 230 → 300 magenta at max).
  drawBackground(ctx, juice, time) {
    // Base
    ctx.fillStyle = '#0a0e1a';
    ctx.fillRect(0, 0, FIELD.W, FIELD.H);

    // Nebula (two drifting radial blobs), hue shifts 225°→305° with juice
    const hue = 225 + juice * 80;
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    const n1x = FIELD.W * (0.3 + 0.05 * Math.sin(time * 0.07));
    const n1y = FIELD.H * (0.35 + 0.04 * Math.cos(time * 0.05));
    let g = ctx.createRadialGradient(n1x, n1y, 0, n1x, n1y, 700);
    g.addColorStop(0, `hsla(${hue}, 80%, ${18 + juice * 14}%, ${0.28 + juice * 0.2})`);
    g.addColorStop(1, 'hsla(225,80%,10%,0)');
    ctx.fillStyle = g;
    ctx.fillRect(0, 0, FIELD.W, FIELD.H);
    const n2x = FIELD.W * (0.75 + 0.04 * Math.cos(time * 0.06));
    const n2y = FIELD.H * (0.55 + 0.05 * Math.sin(time * 0.08));
    g = ctx.createRadialGradient(n2x, n2y, 0, n2x, n2y, 600);
    g.addColorStop(0, `hsla(${(hue + 40) % 360}, 85%, ${20 + juice * 12}%, ${0.22 + juice * 0.18})`);
    g.addColorStop(1, 'hsla(265,85%,10%,0)');
    ctx.fillStyle = g;
    ctx.fillRect(0, 0, FIELD.W, FIELD.H);
    ctx.restore();

    // Star layers with slow vertical parallax drift
    const off = (i) => (time * this.starSpeeds[i] * (1 + juice * 2)) % FIELD.H;
    for (let i = 0; i < 3; i++) {
      const layer = this.starLayers[i];
      const o = off(i);
      ctx.globalAlpha = 1;
      ctx.drawImage(layer, 0, o - FIELD.H);
      ctx.drawImage(layer, 0, o);
    }
    ctx.globalAlpha = 1;

    // Warp streaks at band 40+ (juice >= 0.4)
    if (juice >= 0.4) {
      ctx.save();
      ctx.globalCompositeOperation = 'lighter';
      const k = (juice - 0.4) / 0.6;
      ctx.strokeStyle = `rgba(160,220,255,${0.10 * k})`;
      ctx.lineWidth = 2;
      for (let i = 0; i < 12; i++) {
        const sx = (i * 127.3) % FIELD.W;
        const sy = ((time * (300 + i * 60) * (1 + k)) % FIELD.H);
        ctx.beginPath();
        ctx.moveTo(sx, sy);
        ctx.lineTo(sx, sy + 40 + 120 * k);
        ctx.stroke();
      }
      ctx.restore();
    }

    // Overdrive zone glow (band 20+)
    if (juice > 0.15) {
      const k = (juice - 0.15) / 0.85;
      const pulse = 0.7 + 0.3 * Math.sin(time * (3 + k * 5));
      ctx.save();
      ctx.globalCompositeOperation = 'lighter';
      const zg = ctx.createLinearGradient(0, 0, 0, 260 + 80 * k);
      zg.addColorStop(0, `hsla(${hue + 30}, 95%, 60%, ${0.10 + 0.22 * k * pulse})`);
      zg.addColorStop(1, 'hsla(265,95%,60%,0)');
      ctx.fillStyle = zg;
      ctx.fillRect(0, 0, FIELD.W, 340);
      // Zone boundary line
      ctx.strokeStyle = `hsla(${hue + 30}, 95%, 70%, ${0.25 + 0.4 * k * pulse})`;
      ctx.lineWidth = 2 + 2 * k;
      ctx.setLineDash([14, 10]);
      ctx.lineDashOffset = -time * 60;
      ctx.beginPath();
      ctx.moveTo(0, 260);
      ctx.lineTo(FIELD.W, 260);
      ctx.stroke();
      ctx.restore();
    }
  }

  drawVignette(ctx) {
    ctx.drawImage(this.vignette, 0, 0);
  }

  drawFlash(ctx) {
    if (this.flash > 0) {
      ctx.fillStyle = `rgba(${this.flashColor},${this.flash * 0.6})`;
      ctx.fillRect(0, 0, FIELD.W, FIELD.H);
    }
  }

  flashScreen(color, amount) {
    this.flashColor = color;
    this.flash = Math.max(this.flash, amount);
  }

  clear() {
    for (const p of this.particles) { p.active = false; this.pool.push(p); }
    this.particles.length = 0;
    this.texts.length = 0;
    this.shake = 0; this.flash = 0;
    this.hitStop = 0;
  }
}