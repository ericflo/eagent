/* OVERDRIVE — js/fx.js
 * window.FX: pooled particle system (cap ~900, auto-throttle), brick shatter
 * shards, sparks, fire/ice/electric trails, golden sparkles, laser flashes,
 * explosion ring + shake, shockwaves, popup texts, screen shake, flashes,
 * vignette, animated neon background (gradient + perspective grid + parallax
 * stars + hue shift). Classic script.
 */
(function () {
  'use strict';

  var TAU = Math.PI * 2;
  function rnd(a, b) { return a + Math.random() * (b - a); }

  window.FX = {
    particles: [],
    popups: [],
    shocks: [],
    rings: [],
    beams: [],
    shakeT: 0, shakeMag: 0, shakeRot: 0,
    flash: 0, flashColor: '255,255,255',
    vignette: 0,
    hue: 262,
    stars: null,
    _cap: 0,           // dynamic particle cap (auto-throttled)
    _frameMsCost: 0,
    ctx: null,
    W: 720, H: 1100,

    init: function (ctx, w, h) {
      this.ctx = ctx; this.W = w; this.H = h;
      this._cap = window.CFG.PARTICLE_CAP;
      this._makeStars();
    },

    resize: function (w, h) { this.W = w; this.H = h; this._makeStars(); },

    _makeStars: function () {
      var n = 90;
      this.stars = [];
      for (var i = 0; i < n; i++) {
        this.stars.push({
          x: Math.random() * this.W,
          y: Math.random() * this.H,
          z: rnd(0.25, 1),
          tw: rnd(0.5, 2.2)
        });
      }
    },

    // ---- pool helpers ----------------------------------------------------------
    _new: function () {
      if (this.particles.length >= this._cap) return null;
      var p = { on: true, x: 0, y: 0, vx: 0, vy: 0, life: 1, max: 1, size: 2, color: '#fff',
                grav: 0, drag: 0, glow: false, kind: 'spark', rot: 0, vr: 0, shape: 'r' };
      this.particles.push(p);
      return p;
    },

    /**
     * Spawn a particle. opts: x,y,vx,vy,life,size,color,grav,drag,glow,kind,shape.
     */
    spawn: function (o) {
      var p = this._new();
      if (!p) return null;
      p.on = true;
      p.x = o.x; p.y = o.y;
      p.vx = o.vx || 0; p.vy = o.vy || 0;
      p.max = p.life = o.life || 0.6;
      p.size = o.size || 2;
      p.color = o.color || '#fff';
      p.grav = o.grav || 0;
      p.drag = o.drag === undefined ? 0.5 : o.drag;
      p.glow = !!o.glow;
      p.kind = o.kind || 'spark';
      p.shape = o.shape || 'r';
      p.rot = o.rot || 0;
      p.vr = o.vr || 0;
      return p;
    },

    burst: function (x, y, n, opts) {
      opts = opts || {};
      for (var i = 0; i < n; i++) {
        var a = rnd(0, TAU);
        var sp = rnd(opts.speedMin || 30, opts.speedMax || 260);
        this.spawn({
          x: x, y: y,
          vx: Math.cos(a) * sp, vy: Math.sin(a) * sp,
          life: rnd(opts.lifeMin || 0.3, opts.lifeMax || 0.9),
          size: rnd(opts.sizeMin || 2, opts.sizeMax || 5),
          color: opts.colors ? opts.colors[Math.floor(Math.random() * opts.colors.length)] : (opts.color || '#fff'),
          grav: opts.grav || 0, drag: opts.drag === undefined ? 0.6 : opts.drag,
          glow: opts.glow, shape: opts.shape || 'r', kind: 'spark'
        });
      }
    },

    // ---- compound effects ---------------------------------------------------------
    brickShatter: function (x, y, color, w, h) {
      var n = Math.min(18, Math.max(6, Math.round((w * h) / 140)));
      var cols = this._shades(color);
      for (var i = 0; i < n; i++) {
        this.spawn({
          x: x + rnd(-w / 2, w / 2), y: y + rnd(-h / 2, h / 2),
          vx: rnd(-220, 220), vy: rnd(-320, 80),
          life: rnd(0.4, 1.0),
          size: rnd(2, 6),
          color: cols[i % cols.length],
          grav: 700, drag: 0.4, shape: 'r', rot: rnd(0, TAU), vr: rnd(-10, 10)
        });
      }
    },

    sparks: function (x, y, color, n) {
      this.burst(x, y, n || 8, { colors: this._shades(color), speedMin: 60, speedMax: 320, lifeMin: 0.2, lifeMax: 0.5, glow: true, sizeMin: 1.5, sizeMax: 3.5 });
    },

    trail: function (x, y, kind, vx, vy) {
      var p = this.spawn({
        x: x + rnd(-3, 3), y: y + rnd(-3, 3),
        vx: -vx * 0.15 + rnd(-25, 25), vy: -vy * 0.15 + rnd(-25, 25),
        life: 0.35, size: rnd(2, 4.5), drag: 0.2,
        shape: 'r', kind: 'trail'
      });
      if (!p) return;
      if (kind === 'fire') { p.color = Math.random() < 0.5 ? '#ff6a00' : '#ffd23f'; p.glow = true; }
      else if (kind === 'ice') { p.color = '#9fe8ff'; p.glow = true; }
      else if (kind === 'electric') { p.color = Math.random() < 0.5 ? '#b7aaff' : '#7df9ff'; p.glow = true; }
      else if (kind === 'ember') { p.color = Math.random() < 0.5 ? '#ff9500' : '#ff3d3d'; p.glow = true; }
    },

    goldenSparkles: function (x, y) {
      // ambient sparkle idle on golden bricks
      this.spawn({
        x: x + rnd(-14, 14), y: y + rnd(-10, 10),
        vx: rnd(-10, 10), vy: rnd(-40, -10),
        life: rnd(0.5, 1.1), size: rnd(1.2, 2.6),
        color: Math.random() < 0.5 ? '#ffe066' : '#ffcc00', glow: true,
        drag: 0.9, grav: 120
      });
    },

    coinSparkles: function (x, y) {
      this.burst(x, y, 10, { colors: ['#ffe066', '#ffcc00', '#fff2b0'], speedMin: 40, speedMax: 220, lifeMin: 0.3, lifeMax: 0.8, glow: true });
    },

    explosion: function (x, y) {
      this.burst(x, y, 38, { colors: ['#ff6a00', '#ffd23f', '#ff3d3d', '#fff'], speedMin: 120, speedMax: 620, lifeMin: 0.35, lifeMax: 0.9, glow: true, sizeMin: 2.5, sizeMax: 6 });
      this.ring(x, y, '#ff9030', 260, 0.5);
      this.shake(12, 0.35);
      this.flashScreen('255,180,80', 0.35);
    },

    freezeBurst: function (x, y) {
      this.burst(x, y, 22, { colors: ['#9fe8ff', '#dffaff', '#5fb8ff'], speedMin: 60, speedMax: 340, lifeMin: 0.3, lifeMax: 0.8, glow: true });
      this.ring(x, y, '#bff4ff', 200, 0.45);
    },

    laserFlash: function (x, y) {
      this.burst(x, y, 10, { colors: ['#ff5f7a', '#ffd23f', '#fff'], speedMin: 80, speedMax: 300, lifeMin: 0.15, lifeMax: 0.4, glow: true });
      this.spawn({ x: x, y: y, vx: 0, vy: 0, life: 0.12, size: 16, color: '#ffd23f', glow: true, drag: 1 });
    },

    shockwave: function (x, y, r0, color) {
      this.shocks.push({ x: x, y: y, r: r0 || 10, maxR: 220, color: color || '#b7aaff', t: 0, dur: 0.45 });
    },

    ring: function (x, y, color, maxR, dur) {
      this.rings.push({ x: x, y: y, r: 8, maxR: maxR, color: color, t: 0, dur: dur || 0.4 });
    },

    beam: function (x1, y1, x2, y2, color) {
      this.beams.push({ pts: [x1, y1, x2, y2], color: color || '#ffd23f', t: 0, dur: 0.18, seed: Math.random() * 100 });
    },

    popup: function (x, y, text, color, size) {
      this.popups.push({
        x: x, y: y, text: text, color: color || '#fff',
        size: size || 20, t: 0, dur: 1.1, vy: -60
      });
    },

    shake: function (mag, dur) {
      if (mag > this.shakeMag) { this.shakeMag = mag; }
      this.shakeT = Math.max(this.shakeT, dur || 0.25);
      this.shakeRot = Math.min(0.05, mag * 0.004);
    },

    flashScreen: function (rgba, alpha) {
      this.flash = Math.max(this.flash, alpha || 0.3);
      if (rgba) this.flashColor = rgba;
    },

    setPower: function (powerTier, overdrive) {
      // hue shift with tier & overdrive
      var target = overdrive ? 330 : 262 + (powerTier - 1) * 22;
      this.hue += (target - this.hue) * 0.05;
      this.vignette = 0.16 + powerTier * 0.05 + (overdrive ? 0.12 : 0);
    },

    // ---- per-frame update ----------------------------------------------------------
    update: function (dt) {
      var t0 = performance.now();
      var parts = this.particles;
      for (var i = parts.length - 1; i >= 0; i--) {
        var p = parts[i];
        p.life -= dt;
        if (p.life <= 0) { parts.splice(i, 1); continue; }
        p.vy += (p.grav || 0) * dt;
        var d = Math.max(0, 1 - (p.drag || 0) * dt);
        p.vx *= d; p.vy *= d;
        p.x += p.vx * dt; p.y += p.vy * dt;
        p.rot += (p.vr || 0) * dt;
      }
      // popups
      for (i = this.popups.length - 1; i >= 0; i--) {
        var pp = this.popups[i];
        pp.t += dt; pp.y += pp.vy * dt; pp.vy *= 0.9;
        if (pp.t >= pp.dur) this.popups.splice(i, 1);
      }
      // shockwaves / rings
      for (i = this.shocks.length - 1; i >= 0; i--) {
        var s = this.shocks[i];
        s.t += dt; s.r += (s.maxR - s.r) * dt * 3.2;
        if (s.t >= s.dur) this.shocks.splice(i, 1);
      }
      for (i = this.rings.length - 1; i >= 0; i--) {
        var rg = this.rings[i];
        rg.t += dt;
        rg.r += (rg.maxR - rg.r) * dt * 4;
        if (rg.t >= rg.dur) this.rings.splice(i, 1);
      }
      for (i = this.beams.length - 1; i >= 0; i--) {
        var b = this.beams[i];
        b.t += dt;
        if (b.t >= b.dur) this.beams.splice(i, 1);
      }
      // shake decay
      if (this.shakeT > 0) {
        this.shakeT -= dt;
        if (this.shakeT < 0) { this.shakeT = 0; this.shakeMag = 0; }
        else this.shakeMag *= Math.pow(0.0025, dt); // fast decay
      }
      this.flash *= Math.pow(0.04, dt);
      if (this.flash < 0.01) this.flash = 0;

      // auto-throttle particle budget if update+draw gets expensive
      var cost = performance.now() - t0;
      this._frameMsCost = this._frameMsCost * 0.9 + cost * 0.1;
      if (this._frameMsCost > window.CFG.PARTICLE_BUDGET_MS && this._cap > 200) {
        this._cap = Math.max(200, this._cap - 60);
      } else if (this._frameMsCost < 6 && this._cap < window.CFG.PARTICLE_CAP) {
        this._cap = Math.min(window.CFG.PARTICLE_CAP, this._cap + 30);
      }
    },

    // ---- drawing ---------------------------------------------------------------
    drawScene: function (ctx, game) {
      // freeze state for shake offsets
      var shx = 0, shy = 0, shr = 0;
      if (this.shakeT > 0) {
        shx = rnd(-this.shakeMag, this.shakeMag);
        shy = rnd(-this.shakeMag, this.shakeMag) * 0.6;
        shr = rnd(-this.shakeRot, this.shakeRot);
      }
      ctx.save();
      ctx.translate(this.W / 2 + shx, this.H / 2 + shy);
      ctx.rotate(shr);
      ctx.translate(-this.W / 2, -this.H / 2);
      game.drawWorld(ctx);
      ctx.restore();

      // post FX: beams, particles, rings, shocks, popups
      this._drawBeams(ctx);
      this._drawParticles(ctx);
      this._drawRings(ctx);
      this._drawShocks(ctx);
      this._drawPopups(ctx);
      // full-screen flash
      if (this.flash > 0.01) {
        ctx.fillStyle = 'rgba(' + this.flashColor + ',' + this.flash.toFixed(3) + ')';
        ctx.fillRect(0, 0, this.W, this.H);
      }
      // vignette
      if (this.vignette > 0.01) {
        var v = this.vignette;
        var g = ctx.createRadialGradient(this.W / 2, this.H / 2, Math.min(this.W, this.H) * 0.36, this.W / 2, this.H / 2, Math.max(this.W, this.H) * 0.72);
        g.addColorStop(0, 'rgba(0,0,0,0)');
        g.addColorStop(1, 'rgba(0,0,0,' + v.toFixed(2) + ')');
        ctx.fillStyle = g;
        ctx.fillRect(0, 0, this.W, this.H);
      }
    },

    drawBackground: function (ctx, game) {
      // gradient sky
      var h = this.hue;
      var g = ctx.createLinearGradient(0, 0, 0, this.H);
      g.addColorStop(0, 'hsl(' + h + ',70%,8%)');
      g.addColorStop(0.55, 'hsl(' + ((h + 18) % 360) + ',65%,14%)');
      g.addColorStop(1, 'hsl(' + ((h + 40) % 360) + ',70%,22%)');
      ctx.fillStyle = g;
      ctx.fillRect(0, 0, this.W, this.H);

      // parallax stars (slow drift down-right)
      var t = game ? game.time : 0;
      ctx.fillStyle = '#fff';
      for (var i = 0; i < this.stars.length; i++) {
        var s = this.stars[i];
        var sx = (s.x + t * 4 * s.z) % this.W;
        var sy = (s.y + t * 8 * s.z + s.tw * Math.sin(t * 2 + i)) % this.H;
        ctx.globalAlpha = 0.25 + 0.55 * (0.5 + 0.5 * Math.sin(t * s.tw + i));
        var sz = s.z * 2.2;
        ctx.fillRect(sx, sy, sz, sz);
      }
      ctx.globalAlpha = 1;

      // perspective grid
      this._drawGrid(ctx, t, h);
    },

    _drawGrid: function (ctx, t, h) {
      var horizon = this.H * 0.35;
      ctx.strokeStyle = 'hsla(' + h + ',100%,70%,0.13)';
      ctx.lineWidth = 1;
      // verticals converging to vanishing point
      var vp = { x: this.W / 2 + Math.sin(t * 0.3) * 40, y: horizon - 120 };
      ctx.beginPath();
      for (var i = -6; i <= 6; i++) {
        var x = this.W / 2 + i * 90 + Math.sin(t * 0.5 + i) * 14;
        ctx.moveTo(x, this.H + 40);
        ctx.lineTo(vp.x + (x - vp.x) * 0.06, vp.y);
      }
      ctx.stroke();
      // horizontals (scrolling toward player)
      var speed = 120 + t * 10;
      ctx.beginPath();
      var off = (t * speed) % 70;
      for (var y = this.H + 30 - off; y > horizon; y -= 70) {
        var k = (y - horizon) / (this.H - horizon); // 0 at horizon → 1 at bottom
        var w = k * this.W * 0.6;
        ctx.moveTo(this.W / 2 - w, y);
        ctx.lineTo(this.W / 2 + w, y);
      }
      ctx.stroke();
      // sun disc near horizon
      var sg = ctx.createLinearGradient(vp.x - 60, vp.y - 60, vp.x + 60, vp.y + 60);
      sg.addColorStop(0, 'rgba(255,64,140,0.5)');
      sg.addColorStop(1, 'rgba(255,140,60,0.22)');
      ctx.fillStyle = sg;
      ctx.beginPath();
      ctx.arc(vp.x, vp.y, 64, 0, TAU);
      ctx.fill();
    },

    _drawParticles: function (ctx) {
      var parts = this.particles;
      // two passes: glow first then core (cheap fake additive)
      for (var pass = 0; pass < 2; pass++) {
        for (var i = 0; i < parts.length; i++) {
          var p = parts[i];
          var a = Math.min(1, p.life / (p.max * 0.4));
          if (pass === 0) {
            if (!p.glow) continue;
            ctx.globalAlpha = a * 0.35;
            ctx.fillStyle = p.color;
            ctx.beginPath();
            ctx.arc(p.x, p.y, p.size * 2.4, 0, TAU);
            ctx.fill();
          } else {
            ctx.globalAlpha = Math.min(1, a * 1.2);
            ctx.fillStyle = p.color;
            if (p.shape === 'r') {
              ctx.save();
              ctx.translate(p.x, p.y);
              ctx.rotate(p.rot);
              ctx.fillRect(-p.size / 2, -p.size / 2, p.size, p.size * 0.7);
              ctx.restore();
            } else {
              ctx.beginPath();
              ctx.arc(p.x, p.y, p.size / 2, 0, TAU);
              ctx.fill();
            }
          }
        }
      }
      ctx.globalAlpha = 1;
    },

    _drawRings: function (ctx) {
      for (var i = 0; i < this.rings.length; i++) {
        var r = this.rings[i];
        var k = r.t / r.dur;
        ctx.globalAlpha = (1 - k) * 0.7;
        ctx.strokeStyle = r.color;
        ctx.lineWidth = 3 * (1 - k) + 1;
        ctx.beginPath();
        ctx.arc(r.x, r.y, r.r, 0, TAU);
        ctx.stroke();
      }
      ctx.globalAlpha = 1;
    },

    _drawShocks: function (ctx) {
      for (var i = 0; i < this.shocks.length; i++) {
        var s = this.shocks[i];
        var k = s.t / s.dur;
        ctx.globalAlpha = (1 - k) * 0.6;
        ctx.strokeStyle = s.color;
        ctx.lineWidth = 2.5;
        ctx.beginPath();
        ctx.arc(s.x, s.y, s.r, 0, TAU);
        ctx.stroke();
        ctx.globalAlpha = (1 - k) * 0.25;
        ctx.fillStyle = s.color;
        ctx.beginPath();
        ctx.arc(s.x, s.y, s.r * 0.85, 0, TAU);
        ctx.fill();
      }
      ctx.globalAlpha = 1;
    },

    _drawBeams: function (ctx) {
      for (var i = this.beams.length - 1; i >= 0; i--) {
        var b = this.beams[i];
        var k = b.t / b.dur;
        ctx.globalAlpha = 1 - k;
        ctx.strokeStyle = b.color;
        ctx.lineWidth = 3;
        // lightning-ish zigzag
        ctx.beginPath();
        var x1 = b.pts[0], y1 = b.pts[1], x2 = b.pts[2], y2 = b.pts[3];
        ctx.moveTo(x1, y1);
        var seg = 4;
        for (var s = 1; s <= seg; s++) {
          var t = s / seg;
          var zx = Math.sin(t * 40 + b.seed) * 4 * (1 - k);
          ctx.lineTo(x1 + (x2 - x1) * t + zx, y1 + (y2 - y1) * t);
        }
        ctx.stroke();
        ctx.globalAlpha = (1 - k) * 0.4;
        ctx.lineWidth = 8;
        ctx.stroke();
      }
      ctx.globalAlpha = 1;
    },

    _drawPopups: function (ctx) {
      ctx.textAlign = 'center';
      for (var i = 0; i < this.popups.length; i++) {
        var p = this.popups[i];
        var k = p.t / p.dur;
        var a = k < 0.15 ? k / 0.15 : (1 - k > 0 ? 1 - k : 0);
        ctx.globalAlpha = Math.min(1, a * 1.4);
        var scale = 1 + (k < 0.2 ? (0.2 - k) * 3 : 0);
        ctx.font = 'bold ' + Math.round(p.size * scale) + 'px ' + window.CFG.FONT;
        ctx.strokeStyle = 'rgba(0,0,0,0.7)';
        ctx.lineWidth = 4;
        ctx.strokeText(p.text, p.x, p.y);
        ctx.fillStyle = p.color;
        ctx.fillText(p.text, p.x, p.y);
      }
      ctx.globalAlpha = 1;
      ctx.textAlign = 'left';
    },

    _shades: function (hex) {
      // return a few lighter/darker variants of a hex color
      var c = parseInt(hex.replace('#', ''), 16);
      var r = (c >> 16) & 255, g = (c >> 8) & 255, b = c & 255;
      return [
        hex,
        'rgb(' + Math.min(255, r + 60) + ',' + Math.min(255, g + 60) + ',' + Math.min(255, b + 60) + ')',
        'rgb(' + Math.max(0, r - 50) + ',' + Math.max(0, g - 50) + ',' + Math.max(0, b - 50) + ')',
        'rgb(255,255,255)'
      ];
    },

    clear: function () {
      this.particles.length = 0;
      this.popups.length = 0;
      this.shocks.length = 0;
      this.rings.length = 0;
      this.beams.length = 0;
      this.shakeT = 0; this.shakeMag = 0; this.flash = 0;
    }
  };
})();
