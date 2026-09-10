'use strict';
/* ============================================================
   Attic Breakout — particles.js
   Pooled particle system (hard cap, zero allocation during play)
   plus floating score / text popups.
   ============================================================ */

const Particle = {
  DEAD: 0, SHARD: 1, SPARK: 2, SMOKE: 3, RAIN: 4, RING: 5, FLAME: 6,
};

class ParticlePool {
  constructor(max = 600) {
    this.max = max;
    // Structure-of-arrays style pool.
    this.x = new Float32Array(max);
    this.y = new Float32Array(max);
    this.vx = new Float32Array(max);
    this.vy = new Float32Array(max);
    this.life = new Float32Array(max);
    this.maxLife = new Float32Array(max);
    this.size = new Float32Array(max);
    this.rot = new Float32Array(max);
    this.vrot = new Float32Array(max);
    this.kind = new Uint8Array(max);
    this.cr = new Float32Array(max); // color rgb 0..255
    this.cg = new Float32Array(max);
    this.cb = new Float32Array(max);
    this.grav = new Float32Array(max);
    this.alpha = new Float32Array(max);
    this.cursor = 0;
    this.alive = 0;
  }

  _next() {
    // Ring-buffer allocation: oldest particles get recycled first.
    for (let i = 0; i < this.max; i++) {
      const idx = this.cursor;
      this.cursor = (this.cursor + 1) % this.max;
      if (this.life[idx] <= 0) return idx;
    }
    return this.cursor; // pool full: stomp the next slot
  }

  spawn(kind, x, y, vx, vy, life, size, r, g, b, { grav = 0, vrot = 0, rot = 0 } = {}) {
    const i = this._next();
    this.kind[i] = kind;
    this.x[i] = x; this.y[i] = y;
    this.vx[i] = vx; this.vy[i] = vy;
    this.life[i] = life; this.maxLife[i] = life;
    this.size[i] = size;
    this.cr[i] = r; this.cg[i] = g; this.cb[i] = b;
    this.grav[i] = grav;
    this.rot[i] = rot; this.vrot[i] = vrot;
    return i;
  }

  update(dt) {
    let alive = 0;
    for (let i = 0; i < this.max; i++) {
      if (this.life[i] <= 0) continue;
      this.life[i] -= dt;
      this.x[i] += this.vx[i] * dt;
      this.y[i] += this.vy[i] * dt;
      this.vy[i] += this.grav[i] * dt;
      this.rot[i] += this.vrot[i] * dt;
      // Gentle drag so shards settle instead of drifting forever.
      const drag = 1 - 1.6 * dt;
      this.vx[i] *= drag < 0 ? 0 : drag;
      if (this.life[i] <= 0) { this.alive--; continue; }
      alive++;
    }
    this.alive = alive;
  }

  /** Draw everything; ctx is already camera/shake-transformed. */
  draw(ctx) {
    for (let i = 0; i < this.max; i++) {
      const l = this.life[i];
      if (l <= 0) continue;
      const t = l / this.maxLife[i]; // 1 → 0
      const a = this.kind[i] === Particle.SMOKE ? (1 - t) * 0.35 : t;
      const col = `rgba(${this.cr[i] | 0},${this.cg[i] | 0},${this.cb[i] | 0},${a.toFixed(3)})`;
      const s = this.size[i] * (this.kind[i] === Particle.FLAME ? t : 1);
      switch (this.kind[i]) {
        case Particle.SHARD:
          ctx.save();
          ctx.translate(this.x[i], this.y[i]);
          ctx.rotate(this.rot[i]);
          ctx.fillStyle = col;
          ctx.fillRect(-s, -s * 0.6, s * 2, s * 1.2);
          ctx.restore();
          break;
        case Particle.RING: {
          ctx.strokeStyle = col;
          ctx.lineWidth = Math.max(1, 3.5 * t);
          ctx.beginPath();
          ctx.arc(this.x[i], this.y[i], s * (1.15 - t * 0.85), 0, TAU);
          ctx.stroke();
          break;
        }
        case Particle.SMOKE:
          ctx.fillStyle = col;
          ctx.beginPath();
          ctx.arc(this.x[i], this.y[i], s * (1.6 - t * 0.6), 0, TAU);
          ctx.fill();
          break;
        case Particle.RAIN: // streak
          ctx.strokeStyle = col;
          ctx.lineWidth = 2;
          ctx.beginPath();
          ctx.moveTo(this.x[i], this.y[i]);
          ctx.lineTo(this.x[i] - this.vx[i] * 0.04, this.y[i] - this.vy[i] * 0.04);
          ctx.stroke();
          break;
        default: // SPARK / FLAME — glowing dot
          ctx.fillStyle = col;
          ctx.beginPath();
          ctx.arc(this.x[i], this.y[i], Math.max(0.5, s * t), 0, TAU);
          ctx.fill();
      }
    }
  }

  clear() { this.life.fill(0); this.alive = 0; this.cursor = 0; }
}

/** Helper emitters used all over the game. */
class Emitter {
  constructor(pool) { this.pool = pool; }

  brickShards(x, y, w, h, r, g, b, n = 14) {
    for (let i = 0; i < n; i++) {
      const a = rand(TAU), sp = rand(60, 420);
      this.pool.spawn(Particle.SHARD, x + rand(-w / 2, w / 2), y + rand(-h / 2, h / 2),
        Math.cos(a) * sp, Math.sin(a) * sp - 120, rand(0.5, 1.1), rand(2.5, 6),
        r, g, b, { grav: 900, vrot: rand(-9, 9) });
    }
    for (let i = 0; i < 6; i++) {
      this.pool.spawn(Particle.SMOKE, x + rand(-10, 10), y + rand(-6, 6),
        rand(-40, 40), rand(-70, -20), rand(0.4, 0.8), rand(4, 9), 120, 120, 130);
    }
  }

  sparks(x, y, n = 8, r = 255, g = 230, b = 150, spread = 380) {
    for (let i = 0; i < n; i++) {
      const a = rand(TAU), sp = rand(80, spread);
      this.pool.spawn(Particle.SPARK, x, y, Math.cos(a) * sp, Math.sin(a) * sp,
        rand(0.2, 0.5), rand(1.6, 3.4), r, g, b, { grav: 500 });
    }
  }

  ring(x, y, size, r = 255, g = 255, b = 255, life = 0.35) {
    this.pool.spawn(Particle.RING, x, y, 0, 0, life, size, r, g, b);
  }

  flame(x, y, vx, vy) {
    this.pool.spawn(Particle.FLAME, x + rand(-4, 4), y + rand(-4, 4),
      vx + rand(-40, 40), vy + rand(-40, 40), rand(0.2, 0.45), rand(3, 6),
      rand(200, 255), rand(90, 170), rand(10, 60));
  }

  /** FEVER ceiling light-ray rain. */
  rain(dt, intensity) {
    if (!chance(dt * 26 * intensity)) return;
    const x = rand(30, CONFIG.W - 30);
    this.pool.spawn(Particle.RAIN, x, CONFIG.CEIL_Y + 4, rand(-25, 25), rand(180, 380),
      rand(0.7, 1.3), 2, 255, rand(190, 240), rand(120, 200));
  }

  clear() { this.pool.clear(); }
}

/** Floating text popups (score numbers, "TOO SLOW!", power-up names). */
class Popups {
  constructor(max = 40) { this.items = []; this.max = max; }
  add(x, y, text, { color = '#fff', size = 20, life = 0.9, vy = -70, weight = 'bold' } = {}) {
    if (this.items.length >= this.max) this.items.shift();
    this.items.push({ x, y, text, color, size, life, maxLife: life, vy, weight });
  }
  update(dt) {
    for (let i = this.items.length - 1; i >= 0; i--) {
      const p = this.items[i];
      p.life -= dt;
      p.y += p.vy * dt;
      p.vy *= 1 - 1.4 * dt;
      if (p.life <= 0) this.items.splice(i, 1);
    }
  }
  draw(ctx) {
    for (const p of this.items) {
      const t = p.life / p.maxLife;
      const a = t < 0.75 ? 1 : t / 0.75;
      const pop = t > 0.8 ? 1 + (1 - (t - 0.8) / 0.2) * 0.25 : 1;
      ctx.save();
      ctx.globalAlpha = a;
      ctx.font = `${p.weight} ${Math.round(p.size * pop)}px ${UI.FONT}`;
      ctx.textAlign = 'center';
      ctx.textBaseline = 'middle';
      ctx.lineWidth = 4;
      ctx.strokeStyle = 'rgba(0,0,0,0.65)';
      ctx.strokeText(p.text, p.x, p.y);
      ctx.fillStyle = p.color;
      ctx.fillText(p.text, p.x, p.y);
      ctx.restore();
    }
  }
  clear() { this.items.length = 0; }
}

window.Particle = Particle;
window.ParticlePool = ParticlePool;
window.Emitter = Emitter;
window.Popups = Popups;
