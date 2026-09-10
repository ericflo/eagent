// Breakthrough — fx.js: particles, floaters, shockwaves, shake, background
window.BT = window.BT || {};
BT.fx = (function () {
  var U = BT.util;
  var particles = [], floaters = [], shocks = [];
  var MAX_PARTS = 900;
  var shake = { x: 0, y: 0, mag: 0, decay: 6 };
  var flashes = []; // {color, alpha, decay}
  var hitStop = 0;
  var stars = [], nebulas = [];
  var bgHue = 0, pulse = 0;

  function initBackground(W, H) {
    stars = [];
    for (var i = 0; i < 130; i++) {
      stars.push({ x: Math.random() * W, y: Math.random() * H, z: U.rand(0.2, 1),
                   s: U.rand(0.6, 2.2), tw: Math.random() * 6.28 });
    }
    nebulas = [];
    for (var j = 0; j < 5; j++) {
      nebulas.push({ x: Math.random() * W, y: Math.random() * H * 0.7, r: U.rand(120, 320),
                     hue: U.rand(200, 320), drift: U.rand(2, 8) });
    }
  }

  function addShake(m) { shake.mag = Math.min(shake.mag + m, 26); }

  function addFlash(color, alpha, decay) {
    flashes.push({ color: color, alpha: alpha, decay: decay || 3 });
  }

  function addHitStop(t) { hitStop = Math.max(hitStop, t); }

  // Particle types: shard (spinning rect), spark (dot), ember, ring handled by shocks
  function spawnShards(x, y, color, n, speed) {
    for (var i = 0; i < n; i++) {
      if (particles.length >= MAX_PARTS) return;
      var a = Math.random() * 6.283, sp = U.rand(0.3, 1) * (speed || 260);
      particles.push({ type: 'shard', x: x, y: y, vx: Math.cos(a) * sp, vy: Math.sin(a) * sp - 60,
                       rot: Math.random() * 6.28, vr: U.rand(-8, 8), w: U.rand(3, 9), h: U.rand(2, 5),
                       color: color, life: U.rand(0.4, 0.9), t: 0, grav: 700 });
    }
  }

  function spawnSparks(x, y, color, n, speed) {
    for (var i = 0; i < n; i++) {
      if (particles.length >= MAX_PARTS) return;
      var a = Math.random() * 6.283, sp = U.rand(0.4, 1) * (speed || 320);
      particles.push({ type: 'spark', x: x, y: y, vx: Math.cos(a) * sp, vy: Math.sin(a) * sp,
                       size: U.rand(1.5, 3.5), color: color, life: U.rand(0.2, 0.5), t: 0, grav: 200 });
    }
  }

  function spawnEmbers(x, y, color, n) {
    for (var i = 0; i < n; i++) {
      if (particles.length >= MAX_PARTS) return;
      particles.push({ type: 'spark', x: x, y: y, vx: U.rand(-60, 60), vy: U.rand(-120, -30),
                       size: U.rand(2, 4), color: color, life: U.rand(0.3, 0.7), t: 0, grav: -80 });
    }
  }

  function spawnRing(x, y, color, maxR, width) {
    shocks.push({ x: x, y: y, r: 0, maxR: maxR || 90, w: width || 5, color: color, t: 0, life: 0.4 });
  }

  function addFloater(x, y, text, color, size, life) {
    floaters.push({ x: x, y: y, text: text, color: color || '#fff', size: size || 18,
                    life: life || 1.0, t: 0, vy: -70 });
  }

  function bannerText(text, color) {
    floaters.push({ banner: true, text: text, color: color || '#fff', t: 0, life: 1.8, x: 0.5, y: 0.38 });
  }

  function update(dt, W, H, speedMul) {
    var sm = speedMul || 1;
    shake.mag *= Math.exp(-shake.decay * dt);
    shake.x = (Math.random() * 2 - 1) * shake.mag;
    shake.y = (Math.random() * 2 - 1) * shake.mag;
    if (shake.mag < 0.1) shake.mag = 0;

    for (var i = particles.length - 1; i >= 0; i--) {
      var p = particles[i];
      p.t += dt * sm;
      if (p.t >= p.life) { particles.splice(i, 1); continue; }
      p.vy += p.grav * dt * sm;
      p.x += p.vx * dt * sm;
      p.y += p.vy * dt * sm;
      if (p.rot !== undefined) p.rot += p.vr * dt * sm;
    }
    for (var f = floaters.length - 1; f >= 0; f--) {
      var fl = floaters[f];
      fl.t += dt;
      if (fl.t >= fl.life) floaters.splice(f, 1);
      else if (!fl.banner) fl.y += fl.vy * dt;
    }
    for (var s = shocks.length - 1; s >= 0; s--) {
      shocks[s].t += dt;
      if (shocks[s].t >= shocks[s].life) shocks.splice(s, 1);
    }
    for (var fl2 = flashes.length - 1; fl2 >= 0; fl2--) {
      flashes[fl2].alpha -= flashes[fl2].decay * dt;
      if (flashes[fl2].alpha <= 0) flashes.splice(fl2, 1);
    }
    if (hitStop > 0) hitStop -= dt;

    // background
    pulse += dt * (1.2 + pulse * 0);
    for (var st = 0; st < stars.length; st++) {
      var star = stars[st];
      star.y += star.z * (30 + 200 * speedMul) * dt;
      star.tw += dt * 3;
      if (star.y > H + 4) { star.y = -4; star.x = Math.random() * W; }
    }
    for (var n = 0; n < nebulas.length; n++) {
      nebulas[n].x += Math.sin(pulse * 0.4 + n) * nebulas[n].drift * dt;
    }
  }

  function drawBackground(ctx, W, H, rushFrac, tier) {
    // gradient
    var g = ctx.createLinearGradient(0, 0, 0, H);
    var hueShift = rushFrac * 40;
    g.addColorStop(0, 'hsl(' + (240 + hueShift) + ',60%,' + (7 + rushFrac * 6) + '%)');
    g.addColorStop(0.6, 'hsl(' + (255 + hueShift) + ',55%,' + (5 + rushFrac * 3) + '%)');
    g.addColorStop(1, 'hsl(' + (275 + hueShift) + ',50%,3%)');
    ctx.fillStyle = g;
    ctx.fillRect(0, 0, W, H);

    // nebulas
    var nb = ctx.globalCompositeOperation;
    ctx.globalCompositeOperation = 'lighter';
    for (var i = 0; i < nebulas.length; i++) {
      var n = nebulas[i];
      var pr = 0.5 + 0.5 * Math.sin(pulse * (1.5 + rushFrac * 3) + i);
      var alpha = 0.05 + rushFrac * 0.10 + pr * 0.03 * (1 + rushFrac);
      var rg = ctx.createRadialGradient(n.x, n.y, 0, n.x, n.y, n.r);
      rg.addColorStop(0, 'hsla(' + (n.hue + rushFrac * 60) + ',80%,60%,' + alpha + ')');
      rg.addColorStop(1, 'hsla(' + (n.hue + rushFrac * 60) + ',80%,60%,0)');
      ctx.fillStyle = rg;
      ctx.fillRect(n.x - n.r, n.y - n.r, n.r * 2, n.r * 2);
    }
    // stars
    for (var s = 0; s < stars.length; s++) {
      var st = stars[s];
      var twk = 0.5 + 0.5 * Math.sin(st.tw);
      ctx.fillStyle = 'rgba(200,220,255,' + (0.25 + 0.55 * twk * st.z) + ')';
      ctx.fillRect(st.x, st.y, st.s, st.s * (1 + rushFrac));
    }
    ctx.globalCompositeOperation = nb;
    // vignette
    var vg = ctx.createRadialGradient(W / 2, H / 2, Math.min(W, H) * 0.4, W / 2, H / 2, Math.max(W, H) * 0.75);
    vg.addColorStop(0, 'rgba(0,0,0,0)');
    vg.addColorStop(1, 'rgba(0,0,0,' + (0.35 + rushFrac * 0.1) + ')');
    ctx.fillStyle = vg;
    ctx.fillRect(0, 0, W, H);
  }

  function drawParticles(ctx) {
    ctx.globalCompositeOperation = 'lighter';
    for (var i = 0; i < particles.length; i++) {
      var p = particles[i];
      var a = 1 - p.t / p.life;
      ctx.globalAlpha = a;
      if (p.type === 'shard') {
        ctx.save();
        ctx.translate(p.x, p.y);
        ctx.rotate(p.rot);
        ctx.fillStyle = p.color;
        ctx.fillRect(-p.w / 2, -p.h / 2, p.w, p.h);
        ctx.restore();
      } else {
        ctx.fillStyle = p.color;
        var sz = p.size * (0.5 + a * 0.5) * 2;
        ctx.beginPath();
        ctx.arc(p.x, p.y, sz, 0, 6.283);
        ctx.fill();
      }
    }
    ctx.globalAlpha = 1;
    // shockwaves
    for (var s = 0; s < shocks.length; s++) {
      var sw = shocks[s];
      var t2 = sw.t / sw.life;
      var r = sw.maxR * U.easeOutCubic(t2);
      ctx.globalAlpha = (1 - t2) * 0.8;
      ctx.strokeStyle = sw.color;
      ctx.lineWidth = sw.w * (1 - t2) + 1;
      ctx.beginPath();
      ctx.arc(sw.x, sw.y, r, 0, 6.283);
      ctx.stroke();
    }
    ctx.globalAlpha = 1;
    ctx.globalCompositeOperation = 'source-over';
  }

  function drawFloaters(ctx, W, H) {
    for (var i = 0; i < floaters.length; i++) {
      var f = floaters[i];
      var t = f.t / f.life;
      var a = f.t < 0.1 ? f.t / 0.1 : (1 - U.easeInQuad(Math.max(0, (t - 0.6) / 0.4)));
      ctx.globalAlpha = Math.max(0, a);
      if (f.banner) {
        var scale = 0.6 + U.easeOutBack(Math.min(1, f.t / 0.35)) * 0.4;
        ctx.save();
        ctx.translate(W / 2, H * f.y);
        ctx.scale(scale, scale);
        ctx.font = '900 ' + Math.round(H * 0.055) + 'px system-ui, sans-serif';
        ctx.textAlign = 'center';
        ctx.fillStyle = f.color;
        ctx.shadowColor = f.color;
        ctx.shadowBlur = 24;
        // letter spacing hack: draw with spacing
        ctx.fillText(f.text, 0, 0);
        ctx.restore();
        ctx.shadowBlur = 0;
      } else {
        ctx.font = '700 ' + f.size + 'px system-ui, sans-serif';
        ctx.textAlign = 'center';
        ctx.fillStyle = f.color;
        ctx.fillText(f.text, f.x, f.y);
      }
      ctx.globalAlpha = 1;
    }
  }

  function drawFlashes(ctx, W, H) {
    for (var i = 0; i < flashes.length; i++) {
      ctx.globalAlpha = Math.min(0.75, flashes[i].alpha);
      ctx.fillStyle = flashes[i].color;
      ctx.fillRect(0, 0, W, H);
      ctx.globalAlpha = 1;
    }
  }

  function clear() { particles.length = 0; floaters.length = 0; shocks.length = 0; }

  return {
    initBackground: initBackground, addShake: addShake, addFlash: addFlash,
    addHitStop: addHitStop, spawnShards: spawnShards, spawnSparks: spawnSparks,
    spawnRing: spawnRing, spawnEmbers: spawnEmbers, addFloater: addFloater,
    bannerText: bannerText, update: update, drawBackground: drawBackground,
    drawParticles: drawParticles, drawFloaters: drawFloaters, drawFlashes: drawFlashes,
    clear: clear, get hitStop() { return hitStop; },
    tickHitStop: function (dt) { hitStop = Math.max(0, hitStop - dt); },
    get shakeX() { return shake.x; }, get shakeY() { return shake.y; },
    get particleCount() { return particles.length; },
    setBgPulse: function (p) { pulse = p; }
  };
})();