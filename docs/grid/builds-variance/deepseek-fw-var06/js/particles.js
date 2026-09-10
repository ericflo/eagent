// js/particles.js — particle FX system for Rooftop Breakout.
// Pure ES module: no DOM / window / document access, zero side effects at import time.
//
// All particles are plain pooled objects in a single array (this.particles),
// tagged with a `kind` string. The array is capped; when the cap is exceeded the
// oldest particles are evicted from the front of the array.
//
// update(dt) is pure math (dt in seconds, clamped internally to <= 0.05).
// render(ctx) draws onto any 2D canvas-like context and never touches the DOM.

const MAX_PARTICLES = 1200; // hard cap; oldest particles are evicted
const MAX_DT = 0.05;        // internal per-step clamp
const TAU = Math.PI * 2;

// Default color palettes (used when the caller passes no or an empty colors array).
const DEFAULTS = {
  burst: ['#ffffff', '#ffd166', '#ff9f43', '#ff6b6b', '#feca57', '#ff5e7e'],
  shards: ['#ef476f', '#f78c6b', '#ffd166', '#06d6a0', '#118ab2', '#e9c46a'],
  confetti: ['#ff595e', '#ffca3a', '#8ac926', '#1982c4', '#6a4c93'],
  plume: ['#ff6d00', '#ff9f43', '#ff3d00', '#feca57', '#ffd166', '#ffffff'],
  smoke: ['#b5b5b5', '#9a9a9a', '#c8c8c8', '#8a8a8a'],
};

// Kinds that should be drawn with the 'lighter' (additive) composite operation.
const ADDITIVE = new Set([
  'burst', 'spark', 'ring', 'star', 'flame', 'trail', 'sparkle', 'plume',
]);

function clamp01(v) { return v < 0 ? 0 : v > 1 ? 1 : v; }

function num(v, d) {
  return typeof v === 'number' && Number.isFinite(v) ? v : d;
}

function pick(colors) {
  if (!Array.isArray(colors) || colors.length === 0) return '#ffffff';
  return colors[(Math.random() * colors.length) | 0];
}

// Mix two #rrggbb hex colors -> 'rgb(r,g,b)' string (used for two-tone confetti).
function mixHex(a, b, t) {
  if (typeof a !== 'string' || a[0] !== '#' || a.length < 7) return b;
  if (typeof b !== 'string' || b[0] !== '#' || b.length < 7) return a;
  const ra = parseInt(a.slice(1, 3), 16), ga = parseInt(a.slice(3, 5), 16), ba = parseInt(a.slice(5, 7), 16);
  const rb = parseInt(b.slice(1, 3), 16), gb = parseInt(b.slice(3, 5), 16), bb = parseInt(b.slice(5, 7), 16);
  return 'rgb(' +
    Math.round(ra + (rb - ra) * t) + ',' +
    Math.round(ga + (gb - ga) * t) + ',' +
    Math.round(ba + (bb - ba) * t) + ')';
}

export class ParticleSystem {
  constructor() {
    this.particles = [];
    this.time = 0;               // accumulating sim time, drives sparkle pulse
    this.cap = MAX_PARTICLES;
  }

  clear() {
    this.particles.length = 0;
  }

  spawn(kind, x, y, opts) {
    if (!Number.isFinite(x) || !Number.isFinite(y)) return;
    opts = opts || {};
    switch (kind) {
      case 'burst': this._spawnBurst(x, y, opts); break;
      case 'shards': this._spawnShards(x, y, opts); break;
      case 'confetti': this._spawnConfetti(x, y, opts); break;
      case 'spark': this._spawnSpark(x, y, opts); break;
      case 'ring': this._spawnRing(x, y, opts); break;
      case 'star': this._spawnStar(x, y, opts); break;
      case 'flame': this._spawnFlame(x, y, opts); break;
      case 'trail': this._spawnTrail(x, y, opts); break;
      case 'text': this._spawnText(x, y, opts); break;
      case 'sparkle': this._spawnSparkle(x, y, opts); break;
      case 'smoke': this._spawnSmoke(x, y, opts); break;
      case 'plume': this._spawnPlume(x, y, opts); break;
      default: break; // unknown kinds are ignored
    }
    this._trim();
  }

  // Keep the array at/below the cap, evicting the oldest (front-most) particles.
  _trim() {
    const over = this.particles.length - this.cap;
    if (over > 0) this.particles.splice(0, over);
  }

  // ------------------------------------------------------------------ update

  update(dt) {
    if (typeof dt !== 'number' || !Number.isFinite(dt) || dt <= 0) return;
    if (dt > MAX_DT) dt = MAX_DT;
    this.time += dt;
    const parts = this.particles;
    for (let i = parts.length - 1; i >= 0; i--) {
      const p = parts[i];
      switch (p.kind) {
        case 'burst': {
          p.vy += p.grav * dt;
          const d = Math.exp(-4.2 * dt);          // sparks slow down
          p.vx *= d; p.vy *= d;
          p.x += p.vx * dt; p.y += p.vy * dt;
          p.life -= dt;
          break;
        }
        case 'shards': {
          p.vy += p.grav * dt;
          p.vx *= Math.exp(-1.6 * dt);            // light air drag
          p.x += p.vx * dt; p.y += p.vy * dt;
          p.rot += p.vrot * dt;
          p.life -= dt;
          break;
        }
        case 'confetti': {
          p.age += dt;
          p.vy += p.grav * dt;
          p.x += (p.vx + Math.sin(p.age * 4 + p.ph) * p.sway) * dt;
          p.y += p.vy * dt;
          p.rot += p.vrot * dt;
          p.life -= dt;
          break;
        }
        case 'spark': {
          p.vy += p.grav * dt;
          p.x += p.vx * dt; p.y += p.vy * dt;
          p.life -= dt;
          break;
        }
        case 'ring':
          p.life -= dt;
          break;
        case 'star':
          p.rot += p.vrot * dt;
          p.life -= dt;
          break;
        case 'flame': {
          p.x += p.vx * dt; p.y += p.vy * dt;
          p.vx *= Math.exp(-2 * dt);
          p.life -= dt;
          break;
        }
        case 'trail':
          p.life -= dt;
          break;
        case 'text':
          p.y += p.vy * dt;
          p.life -= dt;
          break;
        case 'sparkle':
          p.life -= dt;
          break;
        case 'smoke': {
          p.vy += p.grav * dt;
          p.x += p.vx * dt; p.y += p.vy * dt;
          p.vx *= Math.exp(-1.2 * dt);
          p.life -= dt;
          break;
        }
        case 'plume': {
          p.vy += p.grav * dt;
          p.x += p.vx * dt; p.y += p.vy * dt;
          p.life -= dt;
          break;
        }
        default:
          p.life -= dt;
          break;
      }
      // Remove dead or corrupted particles (swap-with-last is allocation free).
      if (p.life <= 0 || !Number.isFinite(p.x) || !Number.isFinite(p.y)) {
        parts[i] = parts[parts.length - 1];
        parts.length--;
      }
    }
  }

  // ------------------------------------------------------------------ render

  render(ctx) {
    if (!ctx || typeof ctx.save !== 'function') return;
    const parts = this.particles;
    const time = this.time;

    ctx.save();
    // Pass 1: additive particles.
    ctx.globalCompositeOperation = 'lighter';
    for (let i = 0; i < parts.length; i++) {
      const p = parts[i];
      if (p.blend === 'lighter' || (p.blend == null && ADDITIVE.has(p.kind))) {
        this._draw(ctx, p, time);
      }
    }
    // Pass 2: normal-blend particles.
    ctx.globalCompositeOperation = 'source-over';
    for (let i = 0; i < parts.length; i++) {
      const p = parts[i];
      if (!(p.blend === 'lighter' || (p.blend == null && ADDITIVE.has(p.kind)))) {
        this._draw(ctx, p, time);
      }
    }
    ctx.restore();
  }

  _draw(ctx, p, time) {
    const t = clamp01(p.maxLife > 0 ? p.life / p.maxLife : 0); // 1 -> 0
    switch (p.kind) {
      case 'burst': {
        // Fast line streak along the velocity; the line shrinks as the spark dies.
        ctx.globalAlpha = t * 0.95;
        ctx.strokeStyle = p.color;
        ctx.lineCap = 'round';
        ctx.lineWidth = Math.max(0.75, p.size * 0.8 * t);
        ctx.beginPath();
        ctx.moveTo(p.x, p.y);
        ctx.lineTo(p.x - p.vx * 0.05, p.y - p.vy * 0.05);
        ctx.stroke();
        break;
      }
      case 'shards': {
        // Spinning brick rectangle, normal blend.
        ctx.save();
        ctx.globalAlpha = Math.min(1, t * 1.6);
        ctx.translate(p.x, p.y);
        ctx.rotate(p.rot);
        ctx.fillStyle = p.color;
        const s2 = p.size / 2;
        ctx.fillRect(-s2, -s2, p.size, p.size);
        ctx.restore();
        break;
      }
      case 'confetti': {
        // Two-tone rectangle that rotates and fades out at the end.
        ctx.save();
        ctx.globalAlpha = Math.min(1, t / 0.35);
        ctx.translate(p.x, p.y);
        ctx.rotate(p.rot);
        ctx.fillStyle = p.color;
        const s = p.size, s2 = s / 2;
        ctx.fillRect(-s2, -s2, s, s);
        ctx.fillStyle = p.color2;
        const inset = s * 0.24;
        ctx.fillRect(-s2 + inset, -s2 + inset, s - inset * 2, s - inset * 2);
        ctx.restore();
        break;
      }
      case 'spark': {
        // Tiny bright dot that shrinks to nothing.
        ctx.globalAlpha = t;
        ctx.fillStyle = p.color;
        ctx.beginPath();
        ctx.arc(p.x, p.y, Math.max(0.4, p.size * t), 0, TAU);
        ctx.fill();
        break;
      }
      case 'ring': {
        // Expanding circle stroke (ease-out growth), fades as radius grows.
        const r = p.maxR * (1 - t * t * t);
        ctx.globalAlpha = t;
        ctx.strokeStyle = p.color;
        ctx.lineWidth = Math.max(0.5, p.width * (0.35 + 0.65 * t));
        ctx.beginPath();
        ctx.arc(p.x, p.y, r, 0, TAU);
        ctx.stroke();
        break;
      }
      case 'star': {
        // 4-point sparkle star: scales in fast, spins, shrinks at the end.
        const grow = clamp01((1 - t) / 0.25);
        const shrink = t < 0.3 ? t / 0.3 : 1;
        const s = Math.max(0.01, grow * shrink);
        const r = p.size * s;
        const ir = r * 0.35;
        ctx.save();
        ctx.translate(p.x, p.y);
        ctx.rotate(p.rot);
        ctx.globalAlpha = t;
        ctx.fillStyle = p.color;
        ctx.beginPath();
        for (let i = 0; i < 8; i++) {
          const outer = (i & 1) === 0;
          const idx = i >> 1;
          const ang = outer ? idx * (Math.PI / 2) : idx * (Math.PI / 2) + Math.PI / 4;
          const rad = outer ? r : ir;
          if (i === 0) ctx.moveTo(Math.cos(ang) * rad, Math.sin(ang) * rad);
          else ctx.lineTo(Math.cos(ang) * rad, Math.sin(ang) * rad);
        }
        ctx.closePath();
        ctx.fill();
        ctx.restore();
        break;
      }
      case 'flame': {
        // Rising puff that grows and flickers (sinusoidal + random jitter).
        const age = p.maxLife - p.life;
        const s = p.grow ? p.size * (1.15 - 0.55 * t) : p.size * (0.35 + 0.65 * t);
        const flick = 0.72 + 0.28 * Math.sin(p.ph + age * 46);
        ctx.globalAlpha = clamp01(t * t * flick);
        ctx.fillStyle = p.color;
        const jx = (Math.random() - 0.5) * s * 0.5;
        const jy = (Math.random() - 0.5) * s * 0.5;
        ctx.beginPath();
        ctx.arc(p.x + jx, p.y + jy, s, 0, TAU);
        ctx.fill();
        break;
      }
      case 'trail': {
        // Soft ball trail dot: quadratic shrink + fade.
        ctx.globalAlpha = t;
        ctx.fillStyle = p.color;
        ctx.beginPath();
        ctx.arc(p.x, p.y, Math.max(0.3, p.size * t * t), 0, TAU);
        ctx.fill();
        break;
      }
      case 'text': {
        // Floating score text: quick easeOutBack scale-in, fades at the end.
        const u = clamp01((1 - t) / 0.3);
        const c1 = 1.70158, c3 = c1 + 1;
        // EaseOutBack: u 0->1 scales 0.55 -> 1.0 with a small overshoot.
        const s = 0.55 + 0.45 * (1 + c3 * Math.pow(u - 1, 3) + c1 * Math.pow(u - 1, 2));
        ctx.save();
        ctx.globalAlpha = clamp01(t / 0.35);
        ctx.font = p.font.replace('{size}', String(Math.max(1, Math.round(p.size * s))));
        ctx.textAlign = 'center';
        ctx.textBaseline = 'middle';
        ctx.fillStyle = p.color;
        ctx.fillText(p.text, p.x, p.y);
        ctx.restore();
        break;
      }
      case 'sparkle': {
        // Ambient plus-sign twinkle, pulsing on sin(time*6 + phase).
        const pulse = Math.sin(time * 6 + p.ph);
        const s = Math.max(0.2, p.size * (0.55 + 0.45 * pulse));
        const a = (0.2 + 0.8 * Math.abs(pulse)) * clamp01(t / 0.5);
        ctx.save();
        ctx.globalAlpha = clamp01(a);
        ctx.fillStyle = p.color;
        const w = s * 2, h = Math.max(1, s * 0.34);
        ctx.fillRect(p.x - w / 2, p.y - h / 2, w, h);
        ctx.fillRect(p.x - h / 2, p.y - w / 2, h, w);
        ctx.restore();
        break;
      }
      case 'smoke': {
        // Grey puff, normal blend, low alpha, fades in and out, grows.
        const fadeIn = clamp01((1 - t) / 0.15);
        const fadeOut = clamp01(t / 0.35);
        const s = p.grow ? p.size * (0.55 + 0.9 * (1 - t)) : p.size;
        ctx.globalAlpha = 0.24 * fadeIn * fadeOut;
        ctx.fillStyle = p.color;
        ctx.beginPath();
        ctx.arc(p.x, p.y, s, 0, TAU);
        ctx.fill();
        break;
      }
      case 'plume': {
        // Explosion fireball circle that shrinks and fades quickly.
        ctx.globalAlpha = t * t;
        ctx.fillStyle = p.color;
        ctx.beginPath();
        ctx.arc(p.x, p.y, Math.max(0.4, p.size * (0.3 + 0.7 * t)), 0, TAU);
        ctx.fill();
        break;
      }
      default:
        break;
    }
  }

  // ----------------------------------------------------------------- spawners

  _spawnBurst(x, y, o) {
    const count = num(o.count, 14);
    const life = num(o.life, 0.55);
    if (!(life > 0)) return;
    const colors = (Array.isArray(o.colors) && o.colors.length) ? o.colors : DEFAULTS.burst;
    const speed = num(o.speed, 320);
    const spread = num(o.spread, TAU);
    const size = num(o.size, 3);
    const grav = num(o.gravity, 500);
    const base = Math.random() * TAU;
    for (let i = 0; i < count; i++) {
      const ang = base + Math.random() * spread;
      const sp = speed * (0.45 + Math.random() * 0.75);
      const vx = Math.cos(ang) * sp, vy = Math.sin(ang) * sp;
      if (!Number.isFinite(vx) || !Number.isFinite(vy)) continue;
      this.particles.push({
        kind: 'burst', x, y, vx, vy,
        life, maxLife: life,
        size: size * (0.7 + Math.random() * 0.8),
        color: pick(colors), grav,
        blend: o.add === false ? 'source-over' : null,
      });
    }
  }

  _spawnShards(x, y, o) {
    const count = num(o.count, 8);
    const life = num(o.life, 0.9);
    if (!(life > 0)) return;
    const colors = (Array.isArray(o.colors) && o.colors.length) ? o.colors : DEFAULTS.shards;
    const speed = num(o.speed, 200);
    const size = num(o.size, 4);
    const grav = num(o.gravity, 1000);
    const rot = num(o.rot, 8);
    for (let i = 0; i < count; i++) {
      const ang = Math.random() * TAU;
      const sp = speed * (0.5 + Math.random() * 0.9);
      const vx = Math.cos(ang) * sp, vy = Math.sin(ang) * sp - speed * 0.15;
      if (!Number.isFinite(vx) || !Number.isFinite(vy)) continue;
      this.particles.push({
        kind: 'shards', x, y, vx, vy,
        rot: Math.random() * TAU,
        vrot: (Math.random() * 2 - 1) * rot * (0.6 + Math.random() * 0.8),
        life, maxLife: life,
        size: size * (0.7 + Math.random() * 0.9),
        color: pick(colors), grav,
      });
    }
  }

  _spawnConfetti(x, y, o) {
    const count = num(o.count, 50);
    const life = num(o.life, 2.8);
    if (!(life > 0)) return;
    const x0 = num(o.x, 240); // center of the 480-wide world
    const y0 = num(o.y, -10);
    const grav = num(o.gravity, 120);
    const sway = num(o.sway, 40);
    const size = num(o.size, 6);
    const colors = (Array.isArray(o.colors) && o.colors.length) ? o.colors : DEFAULTS.confetti;
    for (let i = 0; i < count; i++) {
      const c = pick(colors);
      this.particles.push({
        kind: 'confetti',
        x: x0 + (Math.random() * 2 - 1) * 30,
        y: y0,
        vx: (Math.random() * 2 - 1) * 70,
        vy: -(20 + Math.random() * 50),
        grav, sway, age: 0,
        ph: Math.random() * TAU,
        rot: Math.random() * TAU,
        vrot: (Math.random() * 2 - 1) * 6,
        life, maxLife: life,
        size: size * (0.7 + Math.random() * 0.8),
        color: c, color2: mixHex(c, '#ffffff', 0.45),
      });
    }
  }

  _spawnSpark(x, y, o) {
    const life = num(o.life, 0.35);
    if (!(life > 0)) return;
    const color = o.color || '#ffffff';
    const size = num(o.size, 2.5);
    const speed = num(o.speed, 40);
    const grav = num(o.gravity, 0);
    const ang = Math.random() * TAU;
    const sp = speed * (0.3 + Math.random() * 0.7);
    this.particles.push({
      kind: 'spark', x, y,
      vx: Math.cos(ang) * sp, vy: Math.sin(ang) * sp,
      life, maxLife: life, size, color, grav,
    });
  }

  _spawnRing(x, y, o) {
    const life = num(o.life, 0.4);
    if (!(life > 0)) return;
    this.particles.push({
      kind: 'ring', x, y,
      life, maxLife: life,
      maxR: num(o.size, 40),
      width: num(o.width, 3),
      color: o.color || '#7ed4ff',
    });
  }

  _spawnStar(x, y, o) {
    const life = num(o.life, 0.7);
    if (!(life > 0)) return;
    const spin = num(o.spin, 4);
    this.particles.push({
      kind: 'star', x, y,
      rot: Math.random() * TAU,
      vrot: (Math.random() < 0.5 ? -1 : 1) * spin,
      life, maxLife: life,
      size: num(o.size, 8),
      color: o.color || '#ffd166',
    });
  }

  _spawnFlame(x, y, o) {
    const life = num(o.life, 0.5);
    if (!(life > 0)) return;
    this.particles.push({
      kind: 'flame',
      x: x + (Math.random() * 2 - 1) * 2, y,
      vx: (Math.random() * 2 - 1) * 24,
      vy: num(o.vy, -140),
      ph: Math.random() * TAU,
      life, maxLife: life,
      size: num(o.size, 7),
      color: o.color || '#ff6d00',
      grow: o.grow !== false,
    });
  }

  _spawnTrail(x, y, o) {
    const life = num(o.life, 0.35);
    if (!(life > 0)) return;
    this.particles.push({
      kind: 'trail', x, y,
      life, maxLife: life,
      size: num(o.size, 8),
      color: o.color || '#ffd166',
    });
  }

  _spawnText(x, y, o) {
    const life = num(o.life, 1.1);
    if (!(life > 0)) return;
    this.particles.push({
      kind: 'text', x, y,
      vy: num(o.vy, -80),
      life, maxLife: life,
      size: num(o.size, 16),
      color: o.color || '#ffffff',
      text: o.text === undefined ? '+100' : String(o.text),
      font: (typeof o.font === 'string' && o.font !== '')
        ? o.font
        : 'bold {size}px system-ui, sans-serif',
    });
  }

  _spawnSparkle(x, y, o) {
    const life = num(o.life, 3);
    if (!(life > 0)) return;
    this.particles.push({
      kind: 'sparkle',
      x: x + (Math.random() * 2 - 1) * 6,
      y: y + (Math.random() * 2 - 1) * 6,
      life, maxLife: life,
      size: num(o.size, 4),
      color: o.color || '#ffe9a8',
      ph: Number.isFinite(o.phase) ? o.phase : Math.random() * TAU,
    });
  }

  _spawnSmoke(x, y, o) {
    const count = num(o.count, 10);
    const life = num(o.life, 1.2);
    if (!(life > 0)) return;
    const speed = num(o.speed, 60);
    const size = num(o.size, 10);
    const grav = num(o.gravity, -40);
    const grow = o.grow !== false;
    const colors = (Array.isArray(o.colors) && o.colors.length) ? o.colors : DEFAULTS.smoke;
    for (let i = 0; i < count; i++) {
      this.particles.push({
        kind: 'smoke',
        x: x + (Math.random() * 2 - 1) * size * 0.6,
        y: y + (Math.random() * 2 - 1) * size * 0.4,
        vx: (Math.random() * 2 - 1) * speed * 0.7,
        vy: -(0.25 + Math.random() * 0.6) * speed,
        grav,
        life, maxLife: life,
        size: size * (0.7 + Math.random() * 0.8),
        color: pick(colors), grow,
      });
    }
  }

  _spawnPlume(x, y, o) {
    const count = num(o.count, 26);
    const life = num(o.life, 0.7);
    if (!(life > 0)) return;
    const speed = num(o.speed, 520);
    const size = num(o.size, 5);
    const grav = num(o.gravity, 300);
    const colors = (Array.isArray(o.colors) && o.colors.length) ? o.colors : DEFAULTS.plume;
    for (let i = 0; i < count; i++) {
      const ang = Math.random() * TAU;
      const sp = speed * (0.4 + Math.random() * 0.9);
      const vx = Math.cos(ang) * sp, vy = Math.sin(ang) * sp - speed * 0.15;
      if (!Number.isFinite(vx) || !Number.isFinite(vy)) continue;
      this.particles.push({
        kind: 'plume', x, y, vx, vy, grav,
        life, maxLife: life,
        size: size * (0.7 + Math.random() * 0.9),
        color: pick(colors),
      });
    }
  }
}
