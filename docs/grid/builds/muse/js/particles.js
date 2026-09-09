/* BreakoutParticles — pooled particle system: shards, sparks, rings, confetti, texts.
   Global: window.BreakoutParticles */
(function () {
  'use strict';
  var MAX = 900;
  var parts = [];
  var texts = [];

  function spawn(o) {
    if (parts.length >= MAX) parts.shift();
    parts.push(Object.assign({
      x: 0, y: 0, vx: 0, vy: 0, life: 0.6, age: 0,
      size: 3, color: '#fff', grav: 0, drag: 0,
      kind: 'dot', // dot | shard | spark | ring | confetti | glow
      rot: Math.random() * 6.28, vr: (Math.random() - 0.5) * 8
    }, o));
  }
  function burst(x, y, color, n, speed, life, kind) {
    for (var i = 0; i < n; i++) {
      var a = Math.random() * 6.283, s = speed * (0.3 + Math.random() * 0.9);
      spawn({ x: x, y: y, vx: Math.cos(a) * s, vy: Math.sin(a) * s,
        color: color, life: life * (0.6 + Math.random() * 0.7),
        size: 2 + Math.random() * 4, kind: kind || 'dot', grav: 300 });
    }
  }
  function shards(x, y, w, h, color) {
    for (var i = 0; i < 10; i++) {
      spawn({ x: x + (Math.random() - 0.5) * w, y: y + (Math.random() - 0.5) * h,
        vx: (Math.random() - 0.5) * 380, vy: -Math.random() * 320 - 40,
        color: color, life: 0.7 + Math.random() * 0.5, size: 3 + Math.random() * 5,
        kind: 'shard', grav: 700 });
    }
  }
  function sparks(x, y, color, n) {
    n = n || 8;
    for (var i = 0; i < n; i++) {
      var a = Math.random() * 6.283, s = 120 + Math.random() * 320;
      spawn({ x: x, y: y, vx: Math.cos(a) * s, vy: Math.sin(a) * s,
        color: color, life: 0.25 + Math.random() * 0.3, size: 1.5 + Math.random() * 2,
        kind: 'spark', drag: 3 });
    }
  }
  function ring(x, y, color, maxR, life) {
    spawn({ x: x, y: y, vx: 0, vy: 0, color: color, life: life || 0.45,
      size: 4, kind: 'ring', maxR: maxR || 70 });
  }
  function confetti(x, y, n) {
    var cols = ['#ff2d95', '#ffe14d', '#7cff6b', '#00e5ff', '#b16bff', '#ff9a3d'];
    for (var i = 0; i < (n || 60); i++) {
      spawn({ x: x + (Math.random() - 0.5) * 120, y: y + (Math.random() - 0.5) * 20,
        vx: (Math.random() - 0.5) * 420, vy: -Math.random() * 420 - 60,
        color: cols[i % cols.length], life: 1.2 + Math.random() * 1.2,
        size: 3 + Math.random() * 4, kind: 'confetti', grav: 420, drag: 0.6 });
    }
  }
  function floatText(x, y, str, color, size) {
    if (texts.length > 40) texts.shift();
    texts.push({ x: x, y: y, str: str, color: color || '#fff', size: size || 15, age: 0, life: 1.1 });
  }
  function update(dt) {
    for (var i = parts.length - 1; i >= 0; i--) {
      var p = parts[i];
      p.age += dt;
      if (p.age >= p.life) { parts.splice(i, 1); continue; }
      if (p.kind !== 'ring') {
        if (p.drag) { var k = Math.exp(-p.drag * dt); p.vx *= k; p.vy *= k; }
        p.vy += (p.grav || 0) * dt;
        p.x += p.vx * dt; p.y += p.vy * dt;
        p.rot += p.vr * dt;
      }
    }
    for (var j = texts.length - 1; j >= 0; j--) {
      var t = texts[j];
      t.age += dt; t.y -= 46 * dt;
      if (t.age >= t.life) texts.splice(j, 1);
    }
  }
  function draw(ctx) {
    var i, p, a;
    for (i = 0; i < parts.length; i++) {
      p = parts[i]; a = 1 - p.age / p.life;
      if (p.kind === 'ring') {
        var r = (p.maxR || 70) * (p.age / p.life);
        ctx.globalAlpha = a;
        ctx.strokeStyle = p.color; ctx.lineWidth = 3 * a + 1;
        ctx.beginPath(); ctx.arc(p.x, p.y, r, 0, 6.283); ctx.stroke();
      } else if (p.kind === 'shard' || p.kind === 'confetti') {
        ctx.save(); ctx.globalAlpha = Math.min(1, a * 1.5);
        ctx.translate(p.x, p.y); ctx.rotate(p.rot);
        ctx.fillStyle = p.color;
        ctx.fillRect(-p.size / 2, -p.size / 4, p.size, p.size / 2);
        ctx.restore();
      } else if (p.kind === 'spark') {
        ctx.globalAlpha = a;
        ctx.strokeStyle = p.color; ctx.lineWidth = p.size;
        ctx.beginPath(); ctx.moveTo(p.x, p.y);
        ctx.lineTo(p.x - p.vx * 0.03, p.y - p.vy * 0.03); ctx.stroke();
      } else { // dot / glow
        ctx.globalAlpha = a;
        ctx.fillStyle = p.color;
        ctx.beginPath(); ctx.arc(p.x, p.y, p.size * (p.kind === 'glow' ? (1 + a) : a), 0, 6.283);
        ctx.fill();
      }
    }
    ctx.globalAlpha = 1;
    ctx.textAlign = 'center';
    for (i = 0; i < texts.length; i++) {
      var t = texts[i], ta = 1 - t.age / t.life;
      ctx.globalAlpha = Math.min(1, ta * 2);
      ctx.font = '800 ' + t.size + 'px system-ui, sans-serif';
      ctx.fillStyle = t.color;
      ctx.shadowColor = t.color; ctx.shadowBlur = 10;
      ctx.fillText(t.str, t.x, t.y);
      ctx.shadowBlur = 0;
    }
    ctx.globalAlpha = 1;
  }
  function clear() { parts.length = 0; texts.length = 0; }

  window.BreakoutParticles = {
    spawn: spawn, burst: burst, shards: shards, sparks: sparks,
    ring: ring, confetti: confetti, floatText: floatText,
    update: update, draw: draw, clear: clear
  };
})();
