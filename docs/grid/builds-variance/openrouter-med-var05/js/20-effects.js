/* SKYBREAK — 20-effects.js → window.FX
   Singleton particle / effect / background system.
   All coordinates are virtual units (900×1340); caller transforms ctx.
   References only window.U (defensively). Never touches DOM/network. */
(function () {
  'use strict';
  var FX = {};

  // ---- tiny U fallbacks (00-util normally loaded first) -------------------
  function U_() { return (typeof window !== 'undefined' && window.U) || null; }
  function rand(a, b) { if (a === undefined) a = 0; if (b === undefined) { b = a; a = 0; } return a + Math.random() * (b - a); }
  function clamp(v, a, b) { return v < a ? a : v > b ? b : v; }
  function lerp(a, b, t) { return a + (b - a) * t; }
  function TAU() { var u = U_(); return u && u.TAU ? u.TAU : Math.PI * 2; }
  function hsv(h, s, v) {
    var u = U_();
    if (u && u.hsv) return u.hsv(h, s, v);
    h = ((h % 1) + 1) % 1; s = clamp(s, 0, 1); v = clamp(v, 0, 1);
    var i = Math.floor(h * 6), f = h * 6 - i, p = v * (1 - s), q = v * (1 - f * s), t = v * (1 - (1 - f) * s);
    var r, g, b;
    switch (i % 6) {
      case 0: r = v; g = t; b = p; break; case 1: r = q; g = v; b = p; break;
      case 2: r = p; g = v; b = t; break; case 3: r = p; g = q; b = v; break;
      case 4: r = t; g = p; b = v; break; default: r = v; g = p; b = q;
    }
    return 'rgb(' + ((r * 255) | 0) + ',' + ((g * 255) | 0) + ',' + ((b * 255) | 0) + ')';
  }

  // ---- pools --------------------------------------------------------------
  var MAX_P = 900;
  var parts = new Array(MAX_P); // ring buffer of particle objects
  var pHead = 0, pCount = 0;    // ring buffer: oldest at pHead
  var KIND_CIRCLE = 0, KIND_SPARK = 1, KIND_RING = 2, KIND_BEAM = 3, KIND_LIGHTNING = 4;

  for (var i = 0; i < MAX_P; i++) {
    parts[i] = {
      kind: 0, alive: false, x: 0, y: 0, vx: 0, vy: 0, size: 1, size1: 1,
      life: 0, maxLife: 1, grav: 0, glow: false, hue: 0, useHue: false,
      color: '#4fd8ff', r: 0, r1: 0, width: 2, x2: 0, y2: 0, rot: 0, seeds: null, seedN: 0
    };
  }
  var lightSeeds = []; // shared scratch: polylines for lightning
  for (i = 0; i < 12; i++) lightSeeds.push({ a: 0, j: 0, l: 0 });

  var MAX_T = 24;
  var texts = new Array(MAX_T);
  for (i = 0; i < MAX_T; i++) texts[i] = { alive: false, x: 0, y: 0, str: '', color: '#fff', size: 30, life: 0, maxLife: 1, rise: 60, shadow: true };
  var tHead = 0;

  // ---- state --------------------------------------------------------------
  FX.shakeAmount = 0;
  FX.shakeOffset = { x: 0, y: 0 };
  FX.shaking = false;
  FX.flashAlpha = 0;
  FX.flashColor = '255,255,255';
  FX.vignetteStrength = 0;
  FX.vignetteTarget = 0;
  FX.ambientHue = 0;
  FX._hueSmooth = 0;
  FX.energy = 0;
  FX._energySmooth = 0;

  var STARS = 90;
  var stars = [];
  var starInit = false;
  var NEB = 4;
  var nebs = [];

  var shadowBudget = 24;

  // ---- init ---------------------------------------------------------------
  FX.init = function (ctx) {
    if (!starInit) {
      for (var s = 0; s < STARS; s++) {
        var layer = s % 2;
        stars.push({
          x: rand(0, 900), y: rand(0, 1340), r: layer ? rand(0.7, 1.4) : rand(1.0, 2.2),
          sp: layer ? rand(8, 16) : rand(18, 34), tw: rand(0, TAU()),
          tws: rand(1.5, 4), hue: rand(0.5, 0.68), par: layer ? 0.45 : 1
        });
      }
      for (s = 0; s < NEB; s++) {
        nebs.push({
          x: rand(100, 800), y: rand(0, 1340), r: rand(240, 420),
          sp: rand(4, 9), hue: rand(0.5, 0.75), a: rand(0.05, 0.1), sway: rand(0, TAU())
        });
      }
      starInit = true;
    }
    shadowBudget = 24;
  };

  // ---- shake --------------------------------------------------------------
  FX.shake = function (amount) {
    FX.shakeAmount = Math.min(FX.shakeAmount + amount, 60);
  };

  // ---- flash / vignette ---------------------------------------------------
  FX.flash = function (alpha) {
    if (typeof alpha === 'number') { FX.flashAlpha = alpha; FX.flashColor = '255,255,255'; }
    else if (alpha) { FX.flashAlpha = alpha.a !== undefined ? alpha.a : 0.3; FX.flashColor = alpha.rgb || '255,255,255'; }
  };
  FX.vignettePulse = function (strength) {
    FX.vignetteTarget = Math.min(FX.vignetteTarget + strength, 1.5);
  };

  // ---- particle slot acquisition (ring buffer, no allocation) -------------
  function grab() {
    if (pCount < MAX_P) { pCount++; }
    else { pHead = (pHead + 1) % MAX_P; } // overwrite oldest
    var idx = (pHead + pCount - 1) % MAX_P;
    return parts[idx];
  }

  // ---- emitters -----------------------------------------------------------
  FX.burst = function (x, y, o) {
    o = o || {};
    var count = o.count || 18;
    var color = o.color || '#4fd8ff';
    var spd = o.spd !== undefined ? o.spd : 320;
    var size = o.size !== undefined ? o.size : 3;
    var life = o.life !== undefined ? o.life : 0.6;
    var grav = o.grav !== undefined ? o.grav : 0;
    var glow = o.glow !== undefined ? o.glow : false;
    var spread = o.spread !== undefined ? o.spread : TAU();
    var baseA = o.angle !== undefined ? o.angle : 0;
    for (var n = 0; n < count; n++) {
      var p = grab();
      var a = baseA + (spread >= TAU() - 0.01 ? rand(0, TAU()) : rand(-spread / 2, spread / 2));
      var s = spd * (0.35 + Math.random() * 0.65);
      p.kind = KIND_CIRCLE; p.alive = true;
      p.x = x; p.y = y;
      p.vx = Math.cos(a) * s; p.vy = Math.sin(a) * s;
      p.size = size * (0.5 + Math.random() * 0.9); p.size1 = 0.3;
      p.maxLife = life * (0.6 + Math.random() * 0.6); p.life = p.maxLife;
      p.grav = grav; p.glow = glow; p.color = color; p.useHue = false;
      p.hue = 0;
    }
  };

  FX.spark = function (x, y, color) {
    var p = grab();
    var a = rand(0, TAU());
    p.kind = KIND_SPARK; p.alive = true;
    p.x = x; p.y = y;
    var s = rand(260, 520);
    p.vx = Math.cos(a) * s; p.vy = Math.sin(a) * s;
    p.maxLife = rand(0.15, 0.3); p.life = p.maxLife;
    p.grav = 300; p.glow = true; p.color = color || '#fff'; p.useHue = false;
    p.size = 1; p.size1 = 1;
  };

  // comet trail: emit a fading, shrinking, hue-shifting circle — reuse pool.
  FX.trail = function (x, y, r, color, hue) {
    var p = grab();
    p.kind = KIND_CIRCLE; p.alive = true;
    p.x = x; p.y = y; p.vx = 0; p.vy = 0;
    p.size = r * 0.9; p.size1 = 0.05;
    p.maxLife = 0.28; p.life = 0.28;
    p.grav = 0; p.glow = false;
    p.useHue = true; p.hue = (hue !== undefined && hue !== null) ? hue : 0.55;
    p.color = color || '#4fd8ff';
  };

  FX.text = function (x, y, str, o) {
    o = o || {};
    var t = texts[tHead];
    tHead = (tHead + 1) % MAX_T; // replaces oldest when full
    t.alive = true; t.x = x; t.y = y; t.str = '' + str;
    t.color = o.color || '#4fd8ff';
    t.size = o.size !== undefined ? o.size : 30;
    t.maxLife = o.life !== undefined ? o.life : 1.0; t.life = t.maxLife;
    t.rise = o.rise !== undefined ? o.rise : 60;
    t.shadow = o.shadow !== undefined ? o.shadow : true;
  };

  FX.ring = function (x, y, o) {
    o = o || {};
    var p = grab();
    p.kind = KIND_RING; p.alive = true;
    p.x = x; p.y = y; p.vx = 0; p.vy = 0;
    p.r = o.r0 !== undefined ? o.r0 : 10;
    p.r1 = o.r1 !== undefined ? o.r1 : 90;
    p.maxLife = o.life !== undefined ? o.life : 0.5; p.life = p.maxLife;
    p.width = o.width !== undefined ? o.width : 3;
    p.glow = true; p.color = o.color || '#4fd8ff'; p.useHue = false;
  };

  FX.beam = function (x1, y1, x2, y2, o) {
    o = o || {};
    var p = grab();
    p.kind = KIND_BEAM; p.alive = true;
    p.x = x1; p.y = y1; p.x2 = x2; p.y2 = y2;
    p.maxLife = o.life !== undefined ? o.life : 0.18; p.life = p.maxLife;
    p.width = o.width !== undefined ? o.width : 4;
    p.glow = true; p.color = o.color || '#ff4f6e'; p.useHue = false;
    p.r = 0; p.r1 = 0;
  };

  FX.lightning = function (x, y, r, color) {
    var p = grab();
    p.kind = KIND_LIGHTNING; p.alive = true;
    p.x = x; p.y = y; p.r = r; p.r1 = 0; p.vx = 0; p.vy = 0;
    p.maxLife = 0.22; p.life = p.maxLife;
    p.glow = true; p.color = color || '#fff'; p.useHue = false;
    p.width = 2; p.size = 1; p.size1 = 1;
    // build polyline spokes (small one-off allocation at trigger time — OK)
    var spokes = [];
    var n = 5 + ((Math.random() * 3) | 0);
    for (var k = 0; k < n; k++) {
      var a = (k / n) * TAU() + rand(-0.25, 0.25);
      var segs = 3 + ((Math.random() * 3) | 0);
      var pts = [0, 0];
      var px = 0, py = 0;
      for (var s2 = 1; s2 <= segs; s2++) {
        var rr = r * (s2 / segs);
        var ja = a + rand(-0.35, 0.35) * (s2 / segs);
        px = Math.cos(ja) * rr; py = Math.sin(ja) * rr;
        pts.push(px, py);
      }
      spokes.push(pts);
    }
    p.seeds = spokes;
  };

  // ---- update -------------------------------------------------------------
  FX.update = function (dt) {
    if (dt === undefined) dt = 1 / 60;
    dt = clamp(dt, 0, 0.1);
    // shake decay
    FX.shakeAmount *= Math.exp(-dt * 6.5);
    if (FX.shakeAmount < 0.05) FX.shakeAmount = 0;
    FX.shaking = FX.shakeAmount > 0.05;
    if (FX.shaking) {
      var a = rand(0, TAU()), m = FX.shakeAmount * rand(0.5, 1);
      FX.shakeOffset.x = Math.cos(a) * m;
      FX.shakeOffset.y = Math.sin(a) * m;
    } else { FX.shakeOffset.x = 0; FX.shakeOffset.y = 0; }

    // flash decay (~0.6s)
    FX.flashAlpha *= Math.exp(-dt / 0.19); // ~0.6s to near-zero
    if (FX.flashAlpha < 0.002) FX.flashAlpha = 0;

    // vignette smoothing
    FX.vignetteTarget *= Math.exp(-dt * 1.6);
    FX.vignetteStrength = lerp(FX.vignetteStrength, FX.vignetteTarget, 1 - Math.exp(-dt * 8));
    if (FX.vignetteStrength < 0.002) FX.vignetteStrength = 0;

    // energy / hue smoothing
    FX._energySmooth = lerp(FX._energySmooth, clamp(FX.energy, 0, 1), 1 - Math.exp(-dt * 4));
    FX._hueSmooth = lerp(FX._hueSmooth, clamp(FX.ambientHue, 0, 1), 1 - Math.exp(-dt * 3));

    var en = FX._energySmooth;

    // background drift
    for (var s = 0; s < STARS; s++) {
      var st = stars[s];
      st.y += st.sp * (0.6 + en * 2.2) * dt;
      if (st.y > 1348) { st.y -= 1364; st.x = rand(0, 900); }
      st.tw += st.tws * (1 + en * 1.6) * dt;
    }
    for (s = 0; s < NEB; s++) {
      var nb = nebs[s];
      nb.y += nb.sp * (0.6 + en * 2) * dt;
      nb.sway += dt * 0.3;
      if (nb.y - nb.r > 1340) { nb.y = -nb.r - rand(0, 200); nb.x = rand(100, 800); }
    }

    // particles
    for (var k = 0; k < pCount; k++) {
      var idx = (pHead + k) % MAX_P;
      var p = parts[idx];
      if (!p.alive) continue;
      p.life -= dt;
      if (p.life <= 0) { p.alive = false; continue; }
      if (p.grav) p.vy += p.grav * dt;
      p.x += p.vx * dt;
      p.y += p.vy * dt;
    }
    // texts
    for (k = 0; k < MAX_T; k++) {
      var t = texts[k];
      if (!t.alive) continue;
      t.life -= dt;
      if (t.life <= 0) { t.alive = false; continue; }
      t.y -= t.rise * dt;
    }
  };

  // ---- draw ---------------------------------------------------------------
  // Game calls drawBg (background) and drawFx (additive overlay) separately so
  // entities render between them; FX.draw does both for convenience.
  FX.drawBg = function (ctx, t) {
    if (t === undefined) t = 0;
    var en = FX._energySmooth, hshift = FX._hueSmooth;
    var s;

    // ---------- background (source-over) ----------
    // nebula blobs
    for (s = 0; s < NEB; s++) {
      var nb = nebs[s];
      var nx = nb.x + Math.sin(nb.sway) * 30;
      var na = nb.a * (0.7 + en * 1.6);
      var g = ctx.createRadialGradient(nx, nb.y, 0, nx, nb.y, nb.r);
      g.addColorStop(0, 'hsla(' + (((nb.hue + hshift) % 1) * 360) + ',70%,55%,' + na + ')');
      g.addColorStop(1, 'hsla(' + (((nb.hue + hshift) % 1) * 360) + ',70%,55%,0)');
      ctx.globalAlpha = 1;
      ctx.fillStyle = g;
      ctx.fillRect(nx - nb.r, nb.y - nb.r, nb.r * 2, nb.r * 2);
    }
    // stars (2 parallax layers)
    for (s = 0; s < STARS; s++) {
      var st = stars[s];
      var tw = 0.5 + 0.5 * Math.sin(st.tw);
      var al = (0.25 + 0.55 * tw) * (0.55 + en * 0.45) * st.par;
      ctx.globalAlpha = al;
      ctx.fillStyle = hsv(st.hue + hshift, 0.35, 0.95);
      var r = st.r * (1 + tw * 0.35) * (1 + en * 0.2);
      ctx.fillRect(st.x - r / 2, st.y - r / 2, r, r);
    }
    ctx.globalAlpha = 1;
  };

  FX.drawFx = function (ctx, t) {
    if (t === undefined) t = 0;
    var hshift = FX._hueSmooth;
    var k, idx, p, lt;
    ctx.globalCompositeOperation = 'lighter';

    // sparks + circles pass (batch: one globalAlpha model per particle)
    for (k = 0; k < pCount; k++) {
      var idx = (pHead + k) % MAX_P;
      var p = parts[idx];
      if (!p.alive) continue;
      var lt = p.life / p.maxLife; // 1 → 0
      if (p.kind === KIND_CIRCLE || p.kind === KIND_SPARK) {
        if (p.glow && shadowBudget > 0) { ctx.shadowBlur = 12; ctx.shadowColor = p.useHue ? hsv(p.hue + hshift, 0.8, 1) : p.color; shadowBudget--; }
        else ctx.shadowBlur = 0;
        var sz = p.size * (p.size1 + (1 - p.size1) * lt);
        var col = p.useHue ? hsv(p.hue + hshift, 0.85, 1) : p.color;
        if (p.kind === KIND_SPARK) {
          // short streak along velocity
          var vl = Math.sqrt(p.vx * p.vx + p.vy * p.vy) || 1;
          var nx2 = p.vx / vl, ny2 = p.vy / vl, L = 6 + 10 * lt;
          ctx.globalAlpha = lt * 0.9;
          ctx.strokeStyle = col;
          ctx.lineWidth = 1.6;
          ctx.beginPath();
          ctx.moveTo(p.x - nx2 * L, p.y - ny2 * L);
          ctx.lineTo(p.x + nx2 * 2, p.y + ny2 * 2);
          ctx.stroke();
        } else {
          ctx.globalAlpha = lt * 0.85;
          ctx.fillStyle = col;
          ctx.beginPath();
          ctx.arc(p.x, p.y, sz, 0, TAU());
          ctx.fill();
        }
        ctx.shadowBlur = 0;
      }
    }

    // rings
    for (k = 0; k < pCount; k++) {
      idx = (pHead + k) % MAX_P;
      p = parts[idx];
      if (!p.alive || p.kind !== KIND_RING) continue;
      lt = p.life / p.maxLife;
      var rr = lerp(p.r1, p.r, lt); // expand r0 → r1
      if (p.glow && shadowBudget > 0) { ctx.shadowBlur = 14; ctx.shadowColor = p.color; shadowBudget--; } else ctx.shadowBlur = 0;
      ctx.globalAlpha = lt * 0.8;
      ctx.strokeStyle = p.color;
      ctx.lineWidth = p.width * (0.4 + 0.6 * lt);
      ctx.beginPath();
      ctx.arc(p.x, p.y, rr, 0, TAU());
      ctx.stroke();
      ctx.shadowBlur = 0;
    }

    // beams: core + halo (two strokes)
    for (k = 0; k < pCount; k++) {
      idx = (pHead + k) % MAX_P;
      p = parts[idx];
      if (!p.alive || p.kind !== KIND_BEAM) continue;
      lt = p.life / p.maxLife;
      ctx.shadowBlur = 0;
      // halo
      ctx.globalAlpha = lt * 0.5;
      ctx.strokeStyle = p.color;
      ctx.lineWidth = p.width * 3.2;
      ctx.beginPath(); ctx.moveTo(p.x, p.y); ctx.lineTo(p.x2, p.y2); ctx.stroke();
      // core
      ctx.globalAlpha = lt;
      ctx.strokeStyle = '#ffffff';
      ctx.lineWidth = p.width * (0.4 + 0.6 * lt);
      ctx.beginPath(); ctx.moveTo(p.x, p.y); ctx.lineTo(p.x2, p.y2); ctx.stroke();
    }

    // lightning
    for (k = 0; k < pCount; k++) {
      idx = (pHead + k) % MAX_P;
      p = parts[idx];
      if (!p.alive || p.kind !== KIND_LIGHTNING) continue;
      lt = p.life / p.maxLife;
      if (shadowBudget > 0) { ctx.shadowBlur = 12; ctx.shadowColor = p.color; shadowBudget--; } else ctx.shadowBlur = 0;
      ctx.globalAlpha = lt;
      ctx.strokeStyle = p.color;
      ctx.lineWidth = p.width;
      var sp2 = p.seeds;
      for (var m = 0; m < sp2.length; m++) {
        var pts = sp2[m];
        ctx.beginPath();
        ctx.moveTo(p.x + pts[0], p.y + pts[1]);
        for (var q = 2; q < pts.length; q += 2) ctx.lineTo(p.x + pts[q], p.y + pts[q + 1]);
        ctx.stroke();
      }
      ctx.shadowBlur = 0;
    }

    // texts (last, additive, with outline)
    for (k = 0; k < MAX_T; k++) {
      var tx = texts[k];
      if (!tx.alive) continue;
      lt = tx.life / tx.maxLife;
      var aIn = lt > 0.75 ? (1 - lt) * 4 : lt; // quick fade-in, slow fade-out
      ctx.globalAlpha = clamp(aIn, 0, 1);
      var fs = tx.size * (0.9 + 0.1 * lt);
      ctx.font = '800 ' + fs.toFixed(1) + 'px "Segoe UI", system-ui, sans-serif';
      ctx.textAlign = 'center';
      ctx.textBaseline = 'middle';
      if (tx.shadow && shadowBudget > 0) { ctx.shadowBlur = 10; ctx.shadowColor = tx.color; shadowBudget--; } else ctx.shadowBlur = 0;
      ctx.lineWidth = Math.max(2, fs * 0.14);
      ctx.strokeStyle = 'rgba(4,5,12,0.9)';
      ctx.strokeText(tx.str, tx.x, tx.y);
      ctx.shadowBlur = 0;
      ctx.fillStyle = tx.color;
      ctx.fillText(tx.str, tx.x, tx.y);
    }

    // ---------- flash overlay ----------
    if (FX.flashAlpha > 0.002) {
      ctx.globalCompositeOperation = 'source-over';
      ctx.globalAlpha = clamp(FX.flashAlpha, 0, 1);
      ctx.fillStyle = 'rgba(' + FX.flashColor + ',1)';
      ctx.fillRect(-80, -80, 1060, 1500);
      ctx.globalAlpha = 1;
    }

    // vignette / border glow drawn additively at edges
    if (FX.vignetteStrength > 0.002) {
      var vg = ctx.createRadialGradient(450, 670, 420, 450, 670, 820);
      var vs = FX.vignetteStrength;
      vg.addColorStop(0, 'rgba(80,120,255,0)');
      vg.addColorStop(1, 'rgba(80,160,255,' + (vs * 0.35).toFixed(3) + ')');
      ctx.fillStyle = vg;
      ctx.fillRect(-80, -80, 1060, 1500);
    }

    ctx.globalAlpha = 1;
    ctx.shadowBlur = 0;
    ctx.globalCompositeOperation = 'source-over';
    shadowBudget = 24; // reset per-frame cap
  };

  // combined convenience: background + overlay in one call
  FX.draw = function (ctx, t) {
    FX.drawBg(ctx, t);
    FX.drawFx(ctx, t);
  };

  window.FX = FX;
})();
