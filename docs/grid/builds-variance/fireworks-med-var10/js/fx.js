// SKYBREAK — particles, floating text, shake, slow-mo, trails.
// Pools are capped; objects are recycled to avoid per-frame allocation.

class Particle {
  constructor() { this.alive = false; }
  init(x, y, vx, vy, life, size, color, drag = 0.9, glow = true, gravity = 0) {
    this.alive = true;
    this.x = x; this.y = y; this.vx = vx; this.vy = vy;
    this.life = life; this.maxLife = life;
    this.size = size; this.color = color;
    this.drag = drag; this.glow = glow; this.gravity = gravity;
    return this;
  }
  update(dt) {
    this.life -= dt;
    if (this.life <= 0) { this.alive = false; return; }
    const d = Math.pow(this.drag, dt * 60);
    this.vx *= d; this.vy *= d;
    this.vy += this.gravity * dt;
    this.x += this.vx * dt;
    this.y += this.vy * dt;
  }
}

class Floater {
  constructor() { this.alive = false; }
  init(x, y, text, color, size = 22, vy = -70) {
    this.alive = true;
    this.x = x; this.y = y; this.text = text; this.color = color;
    this.size = size; this.vy = vy; this.life = 0.9; this.maxLife = 0.9;
    return this;
  }
  update(dt) {
    this.life -= dt;
    if (this.life <= 0) { this.alive = false; return; }
    this.y += this.vy * dt;
    this.vy *= Math.pow(0.94, dt * 60);
  }
}

class FX {
  constructor() {
    this.particles = Array.from({ length: CONFIG.MAX_PARTICLES }, () => new Particle());
    this.floaters = Array.from({ length: CONFIG.MAX_FLOATERS }, () => new Floater());
    this.pHead = 0; // round-robin allocation cursor
    this.shake = 0;
    this.shakeX = 0; this.shakeY = 0;
    this.flash = 0;        // 0..1 white/color flash
    this.flashColor = '#ffffff';
    this.slowMo = 0;       // seconds of slow-motion remaining
    this.chroma = 0;       // 0..1 red/blue offset strength
  }

  // --- allocation ---------------------------------------------------------

  spawn(x, y, vx, vy, life, size, color, drag, glow, gravity) {
    // Overwrite oldest slot if pool is exhausted (round-robin keeps this O(1)).
    const p = this.particles[this.pHead];
    this.pHead = (this.pHead + 1) % this.particles.length;
    return p.init(x, y, vx, vy, life, size, color, drag, glow, gravity);
  }

  float(x, y, text, color, size, vy) {
    const f = this.floaters.find((f) => !f.alive) || this.floaters[0];
    f.init(x, y, text, color, size, vy);
  }

  // --- events -------------------------------------------------------------

  brickBreak(x, y, color, intensity = 1) {
    const n = Math.min(16 + (intensity * 10) | 0, 42);
    for (let i = 0; i < n; i++) {
      const a = rand(0, TAU), sp = rand(60, 420) * Math.sqrt(intensity);
      this.spawn(x, y, Math.cos(a) * sp, Math.sin(a) * sp,
        rand(0.3, 0.8), rand(2, 6), color, 0.9, true, 300);
    }
  }

  sparks(x, y, color, n = 8, speed = 260) {
    for (let i = 0; i < n; i++) {
      const a = rand(0, TAU), sp = rand(0.3, 1) * speed;
      this.spawn(x, y, Math.cos(a) * sp, Math.sin(a) * sp,
        rand(0.15, 0.4), rand(1.5, 3.5), color, 0.86, true, 0);
    }
  }

  explosion(x, y) {
    for (let i = 0; i < 50; i++) {
      const a = rand(0, TAU), sp = rand(80, 620);
      this.spawn(x, y, Math.cos(a) * sp, Math.sin(a) * sp,
        rand(0.4, 1.0), rand(2, 7), pick(['#ff6b35', '#ffd23f', '#ffffff']), 0.88, true, 120);
    }
    this.addShake(16);
    this.flashNow('#ffb35c', 0.35);
  }

  ringBurst(x, y, color) {
    for (let i = 0; i < 24; i++) {
      const a = (i / 24) * TAU;
      this.spawn(x, y, Math.cos(a) * 300, Math.sin(a) * 300, 0.5, 3, color, 0.9, true, 0);
    }
  }

  addShake(amount) { this.shake = Math.min(this.shake + amount, 34); }

  flashNow(color, amount) {
    this.flash = Math.max(this.flash, amount);
    this.flashColor = color;
  }

  doSlowMo(seconds) { this.slowMo = Math.max(this.slowMo, seconds); }

  chromatic(amount) { this.chroma = Math.max(this.chroma, amount); }

  // --- per-frame ----------------------------------------------------------

  update(dt) {
    for (const p of this.particles) if (p.alive) p.update(dt);
    for (const f of this.floaters) if (f.alive) f.update(dt);

    this.shake *= Math.pow(1 / (1 + CONFIG.SHAKE_DECAY), dt);
    if (this.shake < 0.15) this.shake = 0;
    const s = this.shake;
    this.shakeX = rand(-s, s);
    this.shakeY = rand(-s, s);

    this.flash = Math.max(0, this.flash - dt * 3);
    this.chroma = Math.max(0, this.chroma - dt * 2.2);
    if (this.slowMo > 0) this.slowMo -= dt;
    this.beatFlash = audio.beatFlash;
    if (this.beatFlash > 0) audio.beatFlash = Math.max(0, audio.beatFlash - dt * 4);
  }

  get timeScale() {
    return this.slowMo > 0 ? 0.35 : 1;
  }

  draw(ctx) {
    // Particles: additive for glow.
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    for (const p of this.particles) {
      if (!p.alive) continue;
      const t = p.life / p.maxLife;
      ctx.globalAlpha = clamp(t, 0, 1);
      ctx.fillStyle = p.color;
      if (p.glow) {
        ctx.shadowColor = p.color;
        ctx.shadowBlur = 8;
      }
      ctx.beginPath();
      ctx.arc(p.x, p.y, p.size * (0.4 + 0.6 * t), 0, TAU);
      ctx.fill();
    }
    ctx.restore();

    // Floating score text.
    ctx.save();
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    for (const f of this.floaters) {
      if (!f.alive) continue;
      const t = f.life / f.maxLife;
      ctx.globalAlpha = clamp(t * 1.4, 0, 1);
      ctx.font = `800 ${f.size}px "Avenir Next", "Segoe UI", system-ui, sans-serif`;
      ctx.fillStyle = f.color;
      ctx.shadowColor = f.color;
      ctx.shadowBlur = 10;
      ctx.fillText(f.text, f.x, f.y);
    }
    ctx.restore();
  }

  reset() {
    for (const p of this.particles) p.alive = false;
    for (const f of this.floaters) f.alive = false;
    this.shake = 0; this.flash = 0; this.chroma = 0; this.slowMo = 0;
  }
}
