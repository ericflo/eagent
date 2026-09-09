// fx.js — particles, shards, rings, rays, screen shake/zoom, flashes, floating text.
//
// The public API is a contract used by game.js and render.js and is backward compatible:
//   burst, brickBurst, sparks, ring, shockwave, text, shake, flash, setIntensity,
//   update, draw, drawOverlay, clear
// New (render-side / additive) helpers: flashRect, ray, zoomPunch, ember, jet, smoke,
//   setLite, toggleLite, glowSprite, shakeRot, zoom.

import { clamp, TAU, rand, hsl, lerp, storage } from './util.js';

const hasDOM = typeof document !== 'undefined' && !!document.createElement;

function offscreen(w, h) {
  if (typeof OffscreenCanvas !== 'undefined') {
    try { return new OffscreenCanvas(w, h); } catch (e) { /* fall through */ }
  }
  if (!hasDOM) return null;
  const c = document.createElement('canvas');
  c.width = w; c.height = h;
  return c;
}

export const qhue = (h) => Math.round((((h % 360) + 360) % 360) / 12) * 12;

const glowCache = new Map();
/**
 * Pre-rendered radial glow sprite. shadowBlur is far too slow for hot loops, so every
 * glowing thing in the game is one of these drawn with `lighter`.
 */
export function glowSprite(hue, sat = 100, lum = 62, size = 64, core = 0.2) {
  const h = qhue(hue);
  const key = `${h}|${sat}|${lum}|${size}|${core}`;
  let c = glowCache.get(key);
  if (c !== undefined) return c;
  const cv = offscreen(size, size);
  if (!cv) { glowCache.set(key, null); return null; }
  const g = cv.getContext('2d');
  const r = size / 2;
  const grd = g.createRadialGradient(r, r, 0, r, r, r);
  grd.addColorStop(0, 'rgba(255,255,255,1)');
  grd.addColorStop(core, hsl(h, sat, Math.min(97, lum + 28), 0.92));
  grd.addColorStop(0.45, hsl(h, sat, lum, 0.4));
  grd.addColorStop(0.75, hsl(h, sat, lum, 0.11));
  grd.addColorStop(1, hsl(h, sat, lum, 0));
  g.fillStyle = grd;
  g.fillRect(0, 0, size, size);
  glowCache.set(key, cv);
  return cv;
}

/** Soft round puff used for smoke / dissolve (no white core). */
const puffCache = new Map();
export function puffSprite(hue, sat = 30, lum = 60) {
  const h = qhue(hue);
  const key = `${h}|${sat}|${lum}`;
  let c = puffCache.get(key);
  if (c !== undefined) return c;
  const size = 64;
  const cv = offscreen(size, size);
  if (!cv) { puffCache.set(key, null); return null; }
  const g = cv.getContext('2d');
  const r = size / 2;
  const grd = g.createRadialGradient(r, r, 0, r, r, r);
  grd.addColorStop(0, hsl(h, sat, lum, 0.55));
  grd.addColorStop(0.55, hsl(h, sat, lum, 0.22));
  grd.addColorStop(1, hsl(h, sat, lum, 0));
  g.fillStyle = grd;
  g.fillRect(0, 0, size, size);
  puffCache.set(key, cv);
  return cv;
}

// ------------------------------------------------------------------ particles

class Particle {
  constructor() { this.alive = false; }
  init(x, y, vx, vy, opts) {
    this.alive = true;
    this.x = x; this.y = y; this.vx = vx; this.vy = vy;
    this.life = opts.life ?? 0.6;
    this.maxLife = this.life;
    this.size = opts.size ?? 3;
    this.w = opts.w ?? 0;
    this.h = opts.h ?? 0;
    this.hue = opts.hue ?? 200;
    this.sat = opts.sat ?? 90;
    this.lum = opts.lum ?? 60;
    this.grav = opts.grav ?? 480;
    this.drag = opts.drag ?? 0.9;
    this.shape = opts.shape ?? 'rect';
    this.spin = opts.spin ?? rand(-8, 8);
    this.rot = opts.rot ?? rand(0, TAU);
    this.additive = opts.additive ?? true;
    this.grow = opts.grow ?? 0;          // px/s size growth (smoke)
    this.hueShift = opts.hueShift ?? 0;  // deg/s
    this.alpha = opts.alpha ?? 1;
    this.twinkle = opts.twinkle ?? 0;
    this.fade = opts.fade ?? 1;          // exponent on life fade
  }
  update(dt) {
    this.life -= dt;
    if (this.life <= 0) { this.alive = false; return; }
    this.vy += this.grav * dt;
    const d = Math.pow(this.drag, dt * 60);
    this.vx *= d; this.vy *= d;
    this.x += this.vx * dt;
    this.y += this.vy * dt;
    this.rot += this.spin * dt;
    if (this.grow) this.size += this.grow * dt;
    if (this.hueShift) this.hue += this.hueShift * dt;
  }
}

class Ring {
  constructor(x, y, opts) {
    this.x = x; this.y = y;
    this.r = opts.r0 ?? 4;
    this.r0 = this.r;
    this.r1 = opts.r1 ?? 60;
    this.life = opts.life ?? 0.4;
    this.maxLife = this.life;
    this.hue = opts.hue ?? 200;
    this.sat = opts.sat ?? 95;
    this.lum = opts.lum ?? 68;
    this.width = opts.width ?? 3;
    this.squash = opts.squash ?? 1;   // ellipse y scale
    this.rot = opts.rot ?? 0;
    this.glow = opts.glow ?? true;
    this.alive = true;
  }
  update(dt) {
    this.life -= dt;
    if (this.life <= 0) this.alive = false;
    const k = 1 - clamp(this.life / this.maxLife, 0, 1);
    // ease-out expansion, frame-rate independent
    this.r = this.r0 + (this.r1 - this.r0) * (1 - Math.pow(1 - k, 2.4));
  }
}

class Ray {
  constructor(x, y, opts) {
    this.x = x; this.y = y;
    this.a = opts.a ?? 0;
    this.len = opts.len ?? 60;
    this.width = opts.width ?? 3;
    this.hue = opts.hue ?? 200;
    this.life = opts.life ?? 0.3;
    this.maxLife = this.life;
    this.spin = opts.spin ?? 0;
    this.alive = true;
  }
  update(dt) {
    this.life -= dt;
    if (this.life <= 0) this.alive = false;
    this.a += this.spin * dt;
  }
}

class FlashRect {
  constructor(x, y, w, h, hue, life) {
    this.x = x; this.y = y; this.w = w; this.h = h;
    this.hue = hue;
    this.life = life;
    this.maxLife = life;
    this.alive = true;
  }
  update(dt) {
    this.life -= dt;
    if (this.life <= 0) this.alive = false;
  }
}

class FloatText {
  constructor(x, y, text, opts) {
    this.x = x; this.y = y; this.text = text;
    this.life = opts.life ?? 0.9;
    this.maxLife = this.life;
    this.hue = opts.hue ?? 50;
    this.size = opts.size ?? 18;
    this.vy = opts.vy ?? -60;
    this.value = opts.value ?? 0;     // numeric value for merging
    this.pop = 0;                     // 0..1 punch envelope
    this.jitter = opts.jitter ?? 0;
    this.weight = opts.weight ?? 800;
    this.alive = true;
  }
  update(dt) {
    this.life -= dt;
    if (this.life <= 0) this.alive = false;
    this.y += this.vy * dt;
    this.vy *= Math.pow(0.94, dt * 60);
    this.pop = Math.max(0, this.pop - dt * 5);
  }
}

const NUM_RE = /^\+([\d,]+)$/;

export class FX {
  constructor(max = 1000) {
    this.pool = Array.from({ length: max }, () => new Particle());
    this.cursor = 0;
    this.rings = [];
    this.rays = [];
    this.texts = [];
    this.rects = [];
    this.shakeMag = 0;
    this.shakeTime = 0;
    this.shakeDur = 0.001;
    this.shakeX = 0;
    this.shakeY = 0;
    this.shakeRot = 0;
    this.zoom = 1;
    this.zoomAmt = 0;
    this.zoomTime = 0;
    this.zoomDur = 0.001;
    this.flashes = [];       // {hue, a, life, maxLife}
    this.intensity = 0;
    this.enabled = true;
    this.lite = !!storage.get('overtop.fxLite', false);
    this.time = 0;
    this.beat = 0;
    this.chroma = 0;         // extra chromatic-jitter amount used by render
  }

  setIntensity(v) { this.intensity = clamp(v, 0, 1); }

  setLite(v) {
    this.lite = !!v;
    storage.set('overtop.fxLite', this.lite);
  }
  toggleLite() { this.setLite(!this.lite); return this.lite; }

  /** Count scaling: intensity adds particles, lite mode takes them away. */
  _n(count, boost = 0.9) {
    const n = count * (1 + this.intensity * boost);
    return Math.max(1, Math.round(this.lite ? n * 0.42 : n));
  }

  clear() {
    for (const p of this.pool) p.alive = false;
    this.rings.length = 0;
    this.rays.length = 0;
    this.texts.length = 0;
    this.rects.length = 0;
    this.flashes.length = 0;
    this.shakeMag = 0;
    this.shakeTime = 0;
    this.shakeX = this.shakeY = this.shakeRot = 0;
    this.zoom = 1;
    this.zoomAmt = 0;
    this.zoomTime = 0;
  }

  _get() {
    for (let i = 0; i < this.pool.length; i++) {
      const p = this.pool[this.cursor];
      this.cursor = (this.cursor + 1) % this.pool.length;
      if (!p.alive) return p;
    }
    return this.pool[0];
  }

  /** Generic particle burst. */
  burst(x, y, count, opts = {}) {
    if (!this.enabled) return;
    const n = this._n(count);
    const spread = opts.spread ?? TAU;
    const dir = opts.dir ?? 0;
    const speed = opts.speed ?? 180;
    for (let i = 0; i < n; i++) {
      const a = dir + rand(-spread / 2, spread / 2);
      const s = speed * rand(0.35, 1.25);
      const p = this._get();
      p.init(x + rand(-2, 2), y + rand(-2, 2), Math.cos(a) * s, Math.sin(a) * s, opts);
    }
  }

  /** Rectangular debris shards that inherit a brick's colour. */
  shards(brick, hue, count, opts = {}) {
    if (!this.enabled) return;
    const n = this._n(count, 0.5);
    const sat = opts.sat ?? 80;
    const spd = opts.speed ?? 190;
    for (let i = 0; i < n; i++) {
      const px = brick.x + rand(0, brick.w);
      const py = brick.y + rand(0, brick.h);
      const dx = (px - (brick.x + brick.w / 2)) / (brick.w / 2);
      const p = this._get();
      p.init(px, py, dx * spd * rand(0.4, 1.1) + rand(-40, 40), rand(-spd * 1.15, -spd * 0.15), {
        life: rand(0.45, 0.95),
        w: rand(3, Math.min(11, brick.w * 0.34)),
        h: rand(2.5, 7),
        hue: hue + rand(-12, 12),
        sat,
        lum: opts.lum ?? rand(48, 74),
        grav: opts.grav ?? 900,
        drag: 0.985,
        shape: 'shard',
        spin: rand(-14, 14),
        additive: false,
        alpha: opts.alpha ?? 1,
      });
    }
  }

  /** Brick shattering — a distinct recipe per brick kind. */
  brickBurst(brick, hue, big = false) {
    if (!this.enabled) return;
    const kind = brick.kind || 'normal';
    const cx = brick.x + brick.w / 2;
    const cy = brick.y + brick.h / 2;
    const I = this.intensity;

    switch (kind) {
      case 'angle':
      case 'angleH': {
        // Prism: glassy high-lum shards plus refracted light rays.
        this.shards(brick, hue, big ? 11 : 8, { sat: 60, lum: 84, speed: 210, grav: 820 });
        this.burst(cx, cy, 5, {
          hue: hue + 40, sat: 70, lum: 90, speed: 220, life: 0.4, size: 2.4,
          shape: 'circle', grav: 120, drag: 0.9,
        });
        const base = kind === 'angle' ? Math.PI / 2 : 0;
        for (let i = 0; i < 3; i++) {
          this.ray(cx, cy, {
            a: base + (i - 1) * 0.5, len: 54 + I * 40 + (big ? 26 : 0),
            width: 3.2, hue: hue + i * 22, life: 0.26,
          });
          this.ray(cx, cy, {
            a: base + Math.PI + (i - 1) * 0.5, len: 54 + I * 40,
            width: 3.2, hue: hue + i * 22, life: 0.26,
          });
        }
        this.flashRect(brick, hue, 90);
        this.ring(cx, cy, { r0: 4, r1: big ? 74 : 44, life: 0.3, hue, width: 2.4, lum: 82 });
        break;
      }
      case 'speed': {
        // Armor: metal fragments + a spray of hot white sparks.
        this.shards(brick, 24, big ? 9 : 7, { sat: 16, lum: 62, speed: 200, grav: 1050 });
        for (let i = 0; i < this._n(10, 0.4); i++) {
          const a = rand(-Math.PI, 0) + rand(-0.4, 0.4);
          const s = rand(240, 520);
          const p = this._get();
          p.init(cx + rand(-brick.w / 3, brick.w / 3), cy, Math.cos(a) * s, Math.sin(a) * s, {
            life: rand(0.2, 0.5), size: rand(1.4, 2.6), hue: rand(28, 46), sat: 100, lum: 84,
            grav: 700, drag: 0.93, shape: 'spark',
          });
        }
        this.flashRect(brick, 40, 100);
        this.ring(cx, cy, { r0: 5, r1: big ? 66 : 40, life: 0.26, hue: 30, width: 3, lum: 74 });
        break;
      }
      case 'slow': {
        // Glass: a shower of tiny fragments and a shimmer.
        this.shards(brick, hue, big ? 16 : 13, { sat: 45, lum: 88, speed: 230, grav: 940 });
        for (let i = 0; i < this._n(12, 0.5); i++) {
          const a = rand(0, TAU);
          const s = rand(60, 330);
          const p = this._get();
          p.init(cx + rand(-brick.w / 2, brick.w / 2), cy + rand(-brick.h / 2, brick.h / 2),
            Math.cos(a) * s, Math.sin(a) * s, {
              life: rand(0.3, 0.8), size: rand(1.2, 2.6), hue: hue + rand(-20, 30),
              sat: 60, lum: 92, grav: 520, drag: 0.94, shape: 'circle', twinkle: rand(14, 34),
            });
        }
        this.flashRect(brick, hue + 20, 92);
        this.ring(cx, cy, { r0: 3, r1: big ? 78 : 50, life: 0.34, hue: hue + 15, width: 1.6, lum: 90 });
        break;
      }
      case 'ghost': {
        // Phase brick: dissolves into smoke.
        for (let i = 0; i < this._n(9, 0.5); i++) {
          const p = this._get();
          p.init(brick.x + rand(0, brick.w), brick.y + rand(0, brick.h), rand(-60, 60), rand(-90, -10), {
            life: rand(0.6, 1.15), size: rand(7, 15), hue: hue + rand(-16, 16), sat: 55, lum: 66,
            grav: -40, drag: 0.94, shape: 'smoke', grow: rand(18, 44), additive: false,
          });
        }
        this.burst(cx, cy, 5, {
          hue, speed: 130, life: 0.5, size: 2.2, shape: 'circle', grav: -30, drag: 0.92, lum: 80,
        });
        this.ring(cx, cy, { r0: 6, r1: 46, life: 0.4, hue, width: 1.4, lum: 78 });
        break;
      }
      case 'bomb': {
        this.shards(brick, 18, 12, { sat: 70, lum: 56, speed: 300, grav: 1000 });
        this.burst(cx, cy, 16, {
          hue: rand(16, 42), speed: 420, life: 0.5, size: 4.2, shape: 'circle',
          grav: 220, drag: 0.9, lum: 70, hueShift: -40,
        });
        for (let i = 0; i < this._n(6, 0.4); i++) {
          const p = this._get();
          p.init(cx + rand(-10, 10), cy + rand(-8, 8), rand(-70, 70), rand(-120, -20), {
            life: rand(0.5, 1.0), size: rand(9, 18), hue: 24, sat: 25, lum: 30,
            grav: -60, drag: 0.93, shape: 'smoke', grow: rand(26, 54), additive: false,
          });
        }
        this.flashRect(brick, 40, 100, 0.11);
        this.zoomPunch(0.035, 0.3);
        break;
      }
      case 'boost': {
        // Launch pad: a jet of flame straight up.
        this.jet(cx, brick.y + brick.h * 0.6, 16, { hue: 95, hueShift: -70, speed: 460 });
        this.shards(brick, hue, 6, { sat: 70, lum: 66, speed: 150, grav: 900 });
        this.ray(cx, cy, { a: -Math.PI / 2, len: 130 + I * 70, width: 9, hue: 95, life: 0.3 });
        this.flashRect(brick, 95, 92);
        this.ring(cx, cy, { r0: 5, r1: 70, life: 0.3, hue: 95, width: 3, squash: 0.45 });
        break;
      }
      case 'topOnly': {
        // Cap: gold shards plus downward arrow glyph particles dissolving.
        this.shards(brick, hue, big ? 11 : 8, { sat: 88, lum: 68, speed: 200 });
        for (let i = 0; i < this._n(5, 0.4); i++) {
          const p = this._get();
          p.init(brick.x + rand(4, brick.w - 4), cy, rand(-40, 40), rand(60, 190), {
            life: rand(0.4, 0.7), size: rand(6, 10), hue: 50, sat: 100, lum: 78,
            grav: 240, drag: 0.96, shape: 'arrow', spin: 0, rot: 0,
          });
        }
        this.flashRect(brick, 50, 96);
        this.ring(cx, cy, { r0: 4, r1: big ? 72 : 46, life: 0.3, hue: 50, width: 3 });
        break;
      }
      case 'powerup': {
        // Capsule brick: confetti.
        for (let i = 0; i < this._n(16, 0.6); i++) {
          const a = rand(-Math.PI * 0.95, -Math.PI * 0.05);
          const s = rand(140, 380);
          const p = this._get();
          p.init(cx + rand(-brick.w / 2, brick.w / 2), cy, Math.cos(a) * s, Math.sin(a) * s, {
            life: rand(0.7, 1.3), w: rand(4, 8), h: rand(2.5, 4.5), hue: rand(0, 360),
            sat: 95, lum: 66, grav: 780, drag: 0.985, shape: 'confetti', spin: rand(-18, 18),
            additive: false,
          });
        }
        this.flashRect(brick, 130, 92);
        this.ring(cx, cy, { r0: 4, r1: 60, life: 0.32, hue: 130, width: 3 });
        break;
      }
      default: {
        this.shards(brick, hue, big ? 12 : 8, { speed: big ? 230 : 190 });
        this.burst(cx, cy, big ? 7 : 4, {
          hue: hue + 12, speed: 250, life: 0.36, size: 2.6, shape: 'circle',
          grav: 300, drag: 0.9, lum: 82,
        });
        this.flashRect(brick, hue, 92);
        this.ring(cx, cy, {
          r0: 3, r1: big ? 74 : 38, life: big ? 0.36 : 0.24, hue, width: big ? 3.6 : 2,
        });
        break;
      }
    }
    if (big && kind !== 'ghost') {
      this.ring(cx, cy, {
        r0: 2, r1: 100 + I * 60, life: 0.42, hue: hue + 20, width: 1.4, lum: 86,
      });
    }
  }

  sparks(x, y, count, hue, speed = 240) {
    this.burst(x, y, count, {
      hue, speed, life: 0.3, size: 2.2, grav: 200, drag: 0.9, shape: 'spark', lum: 74,
    });
  }

  /** Ember trail motes (used by the ball at high intensity). */
  ember(x, y, hue, count = 1, opts = {}) {
    if (!this.enabled) return;
    for (let i = 0; i < count; i++) {
      const p = this._get();
      p.init(x + rand(-3, 3), y + rand(-3, 3), rand(-40, 40) + (opts.vx ?? 0) * 0.1,
        rand(-50, 10) + (opts.vy ?? 0) * 0.1, {
          life: rand(0.35, 0.8), size: rand(1.6, 3.4), hue: hue + rand(-14, 22), sat: 100,
          lum: 76, grav: opts.grav ?? -30, drag: 0.93, shape: 'circle', hueShift: opts.hueShift ?? 0,
        });
    }
  }

  /** Upward flame jet. */
  jet(x, y, count, opts = {}) {
    if (!this.enabled) return;
    const n = this._n(count, 0.5);
    const speed = opts.speed ?? 400;
    for (let i = 0; i < n; i++) {
      const a = -Math.PI / 2 + rand(-0.34, 0.34);
      const s = speed * rand(0.35, 1.15);
      const p = this._get();
      p.init(x + rand(-6, 6), y, Math.cos(a) * s, Math.sin(a) * s, {
        life: rand(0.3, 0.7), size: rand(3, 7), hue: opts.hue ?? 40, sat: 100, lum: 72,
        grav: -120, drag: 0.92, shape: 'circle', hueShift: opts.hueShift ?? -60,
      });
    }
  }

  smoke(x, y, count, hue = 220, opts = {}) {
    if (!this.enabled) return;
    const n = this._n(count, 0.3);
    for (let i = 0; i < n; i++) {
      const p = this._get();
      p.init(x + rand(-6, 6), y + rand(-6, 6), rand(-50, 50), rand(-70, -10), {
        life: rand(0.5, 1.1), size: rand(6, 14), hue, sat: opts.sat ?? 40, lum: opts.lum ?? 60,
        grav: -30, drag: 0.94, shape: 'smoke', grow: rand(20, 46), additive: false,
      });
    }
  }

  ring(x, y, opts = {}) {
    if (!this.enabled) return;
    const cap = this.lite ? 30 : 72;
    if (this.rings.length > cap) this.rings.shift();
    this.rings.push(new Ring(x, y, opts));
  }

  ray(x, y, opts = {}) {
    if (!this.enabled || this.lite) return;
    if (this.rays.length > 40) this.rays.shift();
    this.rays.push(new Ray(x, y, opts));
  }

  /** One-to-two frame bright rectangle where a brick used to be. */
  flashRect(brick, hue = 200, lum = 92, life = 0.075) {
    if (!this.enabled) return;
    if (this.rects.length > 40) this.rects.shift();
    const r = new FlashRect(brick.x - 2, brick.y - 2, brick.w + 4, brick.h + 4, hue, life);
    r.lum = lum;
    this.rects.push(r);
  }

  shockwave(x, y, r = 120, hue = 20) {
    this.ring(x, y, { r0: 8, r1: r, life: 0.42, hue, width: 6 });
    this.ring(x, y, { r0: 4, r1: r * 0.7, life: 0.3, hue: hue + 25, width: 3 });
    this.ring(x, y, { r0: 2, r1: r * 1.3, life: 0.55, hue: hue + 10, width: 1.4, lum: 88 });
    this.burst(x, y, 22, {
      hue, speed: 340, life: 0.5, size: 3.4, grav: 260, drag: 0.9, shape: 'circle', lum: 72,
    });
    for (let i = 0; i < 6; i++) {
      this.ray(x, y, { a: rand(0, TAU), len: r * rand(0.5, 1.1), width: 4, hue: hue + 15, life: 0.3 });
    }
    this.zoomPunch(0.03, 0.28);
  }

  text(x, y, str, opts = {}) {
    if (!this.enabled) return;
    // Merge nearby score pops so a hot streak reads as one big number.
    const m = NUM_RE.exec(str);
    const value = m ? Number(m[1].replace(/,/g, '')) : 0;
    if (value > 0 && opts.merge !== false) {
      for (const t of this.texts) {
        if (t.value > 0 && Math.abs(t.x - x) < 44 && Math.abs(t.y - y) < 34 && t.life > 0.15) {
          t.value += value;
          t.text = `+${t.value.toLocaleString('en-US')}`;
          t.size = Math.min(46, t.size + 3.5);
          t.life = Math.max(t.life, t.maxLife * 0.85);
          t.pop = 1;
          t.hue = opts.hue ?? t.hue;
          t.x = lerp(t.x, x, 0.35);
          t.y = lerp(t.y, y, 0.35);
          return;
        }
      }
    }
    const cap = this.lite ? 22 : 44;
    if (this.texts.length > cap) this.texts.shift();
    const f = new FloatText(x, y, str, { ...opts, value });
    f.pop = 1;
    this.texts.push(f);
  }

  /** Trauma-style shake: magnitude decays quadratically and adds a little roll. */
  shake(mag, dur = 0.25) {
    if (mag > this.shakeMag || this.shakeTime <= 0) {
      this.shakeMag = Math.min(26, Math.max(this.shakeMag, mag));
      this.shakeDur = Math.max(dur, 0.05);
      this.shakeTime = this.shakeDur;
    }
  }

  /** Brief scale pulse of the whole scene (render applies fx.zoom around center). */
  zoomPunch(amount = 0.03, dur = 0.25) {
    if (!this.enabled || this.lite) return;
    if (amount <= this.zoomAmt && this.zoomTime > 0) return;
    this.zoomAmt = Math.min(0.08, amount);
    this.zoomDur = Math.max(0.08, dur);
    this.zoomTime = this.zoomDur;
  }

  flash(hue = 200, a = 0.35, life = 0.25) {
    if (!this.enabled) return;
    if (this.flashes.length > 8) this.flashes.shift();
    this.flashes.push({ hue, a, life, maxLife: life });
    // A big flash is always a big moment: give it a camera punch too.
    if (a >= 0.28) this.zoomPunch(0.022 + a * 0.05, 0.26 + a * 0.2);
  }

  update(dt) {
    this.time += dt;
    // 120 BPM pulse, shared by render for grid/HUD beats.
    this.beat = 0.5 - 0.5 * Math.cos(this.time * Math.PI * 4);
    this.chroma = clamp((this.intensity - 0.6) / 0.4, 0, 1);

    for (const p of this.pool) if (p.alive) p.update(dt);
    for (let i = this.rings.length - 1; i >= 0; i--) {
      this.rings[i].update(dt);
      if (!this.rings[i].alive) this.rings.splice(i, 1);
    }
    for (let i = this.rays.length - 1; i >= 0; i--) {
      this.rays[i].update(dt);
      if (!this.rays[i].alive) this.rays.splice(i, 1);
    }
    for (let i = this.rects.length - 1; i >= 0; i--) {
      this.rects[i].update(dt);
      if (!this.rects[i].alive) this.rects.splice(i, 1);
    }
    for (let i = this.texts.length - 1; i >= 0; i--) {
      this.texts[i].update(dt);
      if (!this.texts[i].alive) this.texts.splice(i, 1);
    }
    for (let i = this.flashes.length - 1; i >= 0; i--) {
      this.flashes[i].life -= dt;
      if (this.flashes[i].life <= 0) this.flashes.splice(i, 1);
    }

    if (this.shakeTime > 0) {
      this.shakeTime -= dt;
      const k = Math.max(0, this.shakeTime / this.shakeDur);
      const trauma = k * k;                       // trauma^2 falloff
      const m = Math.min(24, this.shakeMag) * trauma;
      this.shakeX = rand(-m, m);
      this.shakeY = rand(-m, m);
      this.shakeRot = clamp(rand(-m, m) * 0.0016, -0.02, 0.02);
      if (this.shakeTime <= 0) {
        this.shakeMag = 0;
        this.shakeX = this.shakeY = this.shakeRot = 0;
      }
    } else {
      this.shakeX = this.shakeY = this.shakeRot = 0;
    }

    if (this.zoomTime > 0) {
      this.zoomTime -= dt;
      const k = clamp(this.zoomTime / this.zoomDur, 0, 1);
      // fast punch out, springy settle back — always >= 1 so no black edges appear
      const e = k * k;
      this.zoom = 1 + this.zoomAmt * e * (0.7 + 0.3 * Math.cos((1 - k) * 16));
      if (this.zoomTime <= 0) { this.zoom = 1; this.zoomAmt = 0; }
    } else if (this.zoom !== 1) {
      this.zoom = 1;
    }
  }

  // ---------------------------------------------------------------- drawing

  draw(ctx) {
    // ---- pass 1: solid debris (source-over so it reads as matter, not light)
    ctx.save();
    for (const p of this.pool) {
      if (!p.alive || p.additive) continue;
      const t = clamp(p.life / p.maxLife, 0, 1);
      if (p.shape === 'smoke') {
        const spr = puffSprite(p.hue, p.sat, p.lum);
        const s = p.size * (1.2 - t * 0.3) * 2;
        ctx.globalAlpha = t * t * 0.75;
        if (spr) ctx.drawImage(spr, p.x - s / 2, p.y - s / 2, s, s);
        else {
          ctx.fillStyle = hsl(p.hue, p.sat, p.lum, 0.4);
          ctx.beginPath();
          ctx.arc(p.x, p.y, s / 2, 0, TAU);
          ctx.fill();
        }
        continue;
      }
      ctx.globalAlpha = t < 0.25 ? t / 0.25 : 1;
      ctx.fillStyle = hsl(p.hue, p.sat, p.lum);
      ctx.save();
      ctx.translate(p.x, p.y);
      ctx.rotate(p.rot);
      const w = p.w || p.size;
      const h = p.shape === 'confetti' ? (p.h || p.size) * Math.cos(p.rot * 1.7) : (p.h || p.size);
      ctx.fillRect(-w / 2, -h / 2, w, h);
      // a lit top edge sells the shard as a 3d chip
      ctx.fillStyle = hsl(p.hue, p.sat, Math.min(96, p.lum + 26), 0.75);
      ctx.fillRect(-w / 2, -h / 2, w, Math.max(0.8, Math.abs(h) * 0.28));
      ctx.restore();
    }
    ctx.restore();

    // ---- pass 2: light (additive)
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    ctx.lineCap = 'round';
    for (const r of this.rects) {
      const t = clamp(r.life / r.maxLife, 0, 1);
      ctx.globalAlpha = t * 0.95;
      ctx.fillStyle = hsl(r.hue, 100, r.lum ?? 92, 1);
      ctx.fillRect(r.x, r.y, r.w, r.h);
    }
    for (const p of this.pool) {
      if (!p.alive || !p.additive) continue;
      const t = clamp(p.life / p.maxLife, 0, 1);
      const fade = p.fade === 1 ? t : Math.pow(t, p.fade);
      if (p.shape === 'circle') {
        const tw = p.twinkle ? 0.55 + 0.45 * Math.sin(this.time * p.twinkle + p.rot) : 1;
        const s = p.size * (0.5 + t * 0.8) * 3.4;
        const spr = glowSprite(p.hue, p.sat, p.lum);
        ctx.globalAlpha = fade * 0.95 * tw;
        if (spr) ctx.drawImage(spr, p.x - s / 2, p.y - s / 2, s, s);
        else {
          ctx.fillStyle = hsl(p.hue, p.sat, p.lum);
          ctx.beginPath();
          ctx.arc(p.x, p.y, p.size, 0, TAU);
          ctx.fill();
        }
      } else if (p.shape === 'spark') {
        const sp = Math.hypot(p.vx, p.vy);
        const l = clamp(sp * 0.022, 2.5, 16);
        const ux = sp > 1e-4 ? p.vx / sp : 0;
        const uy = sp > 1e-4 ? p.vy / sp : -1;
        ctx.globalAlpha = fade;
        ctx.strokeStyle = hsl(p.hue, p.sat, p.lum);
        ctx.lineWidth = Math.max(0.9, p.size * 0.8 * t);
        ctx.beginPath();
        ctx.moveTo(p.x - ux * l, p.y - uy * l);
        ctx.lineTo(p.x + ux * l * 0.3, p.y + uy * l * 0.3);
        ctx.stroke();
      } else if (p.shape === 'arrow') {
        ctx.globalAlpha = fade * 0.9;
        ctx.strokeStyle = hsl(p.hue, p.sat, p.lum);
        ctx.lineWidth = 2;
        const s = p.size * (0.5 + t * 0.5);
        ctx.beginPath();
        ctx.moveTo(p.x - s * 0.5, p.y - s * 0.3);
        ctx.lineTo(p.x, p.y + s * 0.45);
        ctx.lineTo(p.x + s * 0.5, p.y - s * 0.3);
        ctx.stroke();
      } else {
        ctx.globalAlpha = fade;
        ctx.fillStyle = hsl(p.hue, p.sat, p.lum);
        const s = p.size * (0.4 + t * 0.6);
        ctx.save();
        ctx.translate(p.x, p.y);
        ctx.rotate(p.rot);
        ctx.fillRect(-s / 2, -s / 2, s, s);
        ctx.restore();
      }
    }
    for (const r of this.rays) {
      const t = clamp(r.life / r.maxLife, 0, 1);
      const l = r.len * (1.15 - t * 0.35);
      ctx.globalAlpha = t * t * 0.8;
      ctx.strokeStyle = hsl(r.hue, 100, 80);
      ctx.lineWidth = Math.max(0.6, r.width * t);
      ctx.beginPath();
      ctx.moveTo(r.x, r.y);
      ctx.lineTo(r.x + Math.cos(r.a) * l, r.y + Math.sin(r.a) * l);
      ctx.stroke();
    }
    for (const r of this.rings) {
      const t = clamp(r.life / r.maxLife, 0, 1);
      ctx.globalAlpha = t * t * 0.85;
      ctx.strokeStyle = hsl(r.hue, r.sat, r.lum);
      ctx.lineWidth = r.width * t + 0.4;
      ctx.beginPath();
      if (r.squash !== 1) ctx.ellipse(r.x, r.y, r.r, r.r * r.squash, r.rot, 0, TAU);
      else ctx.arc(r.x, r.y, r.r, 0, TAU);
      ctx.stroke();
    }
    ctx.restore();

    // ---- pass 3: floating score text
    if (this.texts.length) {
      ctx.save();
      ctx.textAlign = 'center';
      ctx.textBaseline = 'alphabetic';
      ctx.lineJoin = 'round';
      for (const f of this.texts) {
        const t = clamp(f.life / f.maxLife, 0, 1);
        const pop = f.pop * f.pop;
        const scale = 1 + pop * 0.75;
        const a = t > 0.6 ? 1 : t / 0.6;
        ctx.save();
        ctx.globalAlpha = a;
        ctx.translate(f.x, f.y);
        if (f.jitter > 0) ctx.rotate((Math.random() - 0.5) * f.jitter);
        ctx.scale(scale, scale);
        ctx.font = `${f.weight} ${f.size}px ${TEXT_FONT}`;
        ctx.lineWidth = Math.max(3, f.size * 0.22);
        ctx.strokeStyle = 'rgba(2,4,12,0.85)';
        ctx.strokeText(f.text, 0, 0);
        const grd = ctx.createLinearGradient(0, -f.size * 0.8, 0, f.size * 0.3);
        grd.addColorStop(0, '#ffffff');
        grd.addColorStop(0.55, hsl(f.hue, 100, 78));
        grd.addColorStop(1, hsl(f.hue, 95, 58));
        ctx.fillStyle = grd;
        ctx.fillText(f.text, 0, 0);
        if (pop > 0.02) {
          ctx.globalCompositeOperation = 'lighter';
          ctx.globalAlpha = pop * 0.55;
          ctx.fillStyle = hsl(f.hue, 100, 72);
          ctx.fillText(f.text, 0, 0);
        }
        ctx.restore();
      }
      ctx.restore();
    }
  }

  /** Full-screen colour flashes; draw last, in screen space. */
  drawOverlay(ctx, w, h) {
    for (const f of this.flashes) {
      const t = clamp(f.life / f.maxLife, 0, 1);
      ctx.save();
      ctx.globalCompositeOperation = 'lighter';
      ctx.fillStyle = hsl(f.hue, 90, 60, f.a * t * t);
      ctx.fillRect(0, 0, w, h);
      ctx.restore();
    }
  }
}

const TEXT_FONT = '"Trebuchet MS", "Avenir Next", Inter, system-ui, sans-serif';

export const fx = new FX();
export default fx;
