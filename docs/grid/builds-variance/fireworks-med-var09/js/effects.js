/* effects.js — pooled particles, ripples, popups, screen shake, starfield.
 * Object pooling; zero allocation per frame in steady state. */
(function (root) {
  'use strict';
  var BO = root.BO = root.BO || {};

  function Pool(n, make) {
    this.items = new Array(n);
    for (var i = 0; i < n; i++) this.items[i] = make();
    this.n = n;
  }
  Pool.prototype.get = function () {
    for (var i = 0; i < this.n; i++) {
      if (!this.items[i].alive) return this.items[i];
    }
    return null; // pool exhausted; skip (no allocation)
  };
  Pool.prototype.each = function (fn, dt) {
    for (var i = 0; i < this.n; i++) {
      var it = this.items[i];
      if (it.alive) fn(it, dt);
    }
  };

  function Particle() {
    return { alive: false, x: 0, y: 0, vx: 0, vy: 0, life: 0, maxLife: 1,
             size: 3, color: '#fff', drag: 0.98, grav: 0, glow: false };
  }
  function Ripple() {
    return { alive: false, x: 0, y: 0, life: 0, maxLife: 0.5, maxR: 40, color: '#fff' };
  }
  function Popup() {
    return { alive: false, x: 0, y: 0, vy: -60, life: 0, maxLife: 0.9,
             text: '', color: '#fff', size: 14 };
  }
  function TrailDot() {
    return { alive: false, x: 0, y: 0, life: 0, maxLife: 1, size: 4, color: '#fff' };
  }

  function Effects() {
    this.particles = new Pool(420, Particle);
    this.trailDots = new Pool(320, TrailDot);
    this.ripples = new Pool(48, Ripple);
    this.popups = new Pool(40, Popup);
    this.shake = 0; this.shakeX = 0; this.shakeY = 0;
    this.flash = 0;
    this.hitstop = 0;
    this.stars = [];
    for (var i = 0; i < 90; i++) {
      this.stars.push({ x: Math.random(), y: Math.random(),
        s: 0.4 + Math.random() * 1.6, sp: 0.02 + Math.random() * 0.06 });
    }
    this.time = 0;
  }

  Effects.prototype.burst = function (x, y, color, count, speed) {
    speed = speed || 220;
    for (var i = 0; i < count; i++) {
      var p = this.particles.get();
      if (!p) return;
      var a = Math.random() * Math.PI * 2;
      var v = speed * (0.35 + Math.random() * 0.85);
      p.alive = true; p.x = x; p.y = y;
      p.vx = Math.cos(a) * v; p.vy = Math.sin(a) * v;
      p.maxLife = p.life = 0.35 + Math.random() * 0.5;
      p.size = 2 + Math.random() * 3.5;
      p.color = color; p.grav = 260; p.drag = 0.985; p.glow = true;
    }
  };

  Effects.prototype.trailPuff = function (x, y, color, size) {
    // smooth comet trail: dedicated additive dot buffer, no velocity jitter,
    // long enough life to form a streak (drawn in main.js with 'lighter')
    var p = this.trailDots.get();
    if (!p) return;
    p.alive = true; p.x = x; p.y = y;
    p.maxLife = p.life = 0.34;
    p.size = size || 3;
    p.color = color;
  };

  Effects.prototype.ripple = function (x, y, color, maxR) {
    var r = this.ripples.get();
    if (!r) return;
    r.alive = true; r.x = x; r.y = y;
    r.maxLife = r.life = 0.45; r.color = color; r.maxR = maxR || 42;
  };

  Effects.prototype.popup = function (x, y, text, color, size) {
    var p = this.popups.get();
    if (!p) return;
    p.alive = true; p.x = x; p.y = y; p.vy = -70;
    p.maxLife = p.life = 0.9; p.text = text; p.color = color; p.size = size || 14;
  };

  Effects.prototype.addShake = function (amt) {
    this.shake = Math.min(26, this.shake + amt);
  };

  Effects.prototype.addHitstop = function (t) {
    this.hitstop = Math.max(this.hitstop, t);
  };

  Effects.prototype.update = function (dt) {
    this.time += dt;
    var self = this;
    this.particles.each(function (p, dt) {
      p.life -= dt;
      if (p.life <= 0) { p.alive = false; return; }
      p.vy += p.grav * dt;
      var d = Math.pow(p.drag, dt * 60);
      p.vx *= d; p.vy *= d;
      p.x += p.vx * dt; p.y += p.vy * dt;
    }, dt);
    this.ripples.each(function (r, dt) {
      r.life -= dt;
      if (r.life <= 0) r.alive = false;
    }, dt);
    this.trailDots.each(function (d, dt) {
      d.life -= dt;
      if (d.life <= 0) d.alive = false;
    }, dt);
    this.popups.each(function (p, dt) {
      p.life -= dt;
      if (p.life <= 0) { p.alive = false; return; }
      p.y += p.vy * dt;
      p.vy *= 0.96;
    }, dt);
    if (this.shake > 0) {
      this.shake = Math.max(0, this.shake - dt * 40);
      var s = this.shake;
      this.shakeX = (Math.random() * 2 - 1) * s;
      this.shakeY = (Math.random() * 2 - 1) * s;
    } else { this.shakeX = this.shakeY = 0; }
    if (this.flash > 0) this.flash = Math.max(0, this.flash - dt * 3);
    for (var i = 0; i < this.stars.length; i++) {
      var st = this.stars[i];
      st.y += st.sp * dt;
      if (st.y > 1) st.y -= 1;
    }
  };

  BO.Effects = Effects;
})(typeof window !== 'undefined' ? window : globalThis);
