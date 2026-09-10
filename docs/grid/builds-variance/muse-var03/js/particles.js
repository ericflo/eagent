/* particles.js — pooled particles, trails, floating texts, screen shake, flashes. */
'use strict';
const FX = (() => {
  const parts = [];
  const texts = [];
  const flashes = [];
  let shake = 0, shakeX = 0, shakeY = 0;
  let pulse = 0; // background pulse 0..1
  const MAX = 700;

  function spawn(o) {
    if (parts.length >= MAX) parts.shift();
    parts.push(Object.assign({ x: 0, y: 0, vx: 0, vy: 0, life: 0.6, age: 0, size: 4, color: '#fff', grav: 0, drag: 0, glow: true, shape: 'rect' }, o));
  }
  function burst(x, y, color, n = 18, speed = 320) {
    for (let i = 0; i < n; i++) {
      const a = Math.random() * Math.PI * 2, s = speed * (0.25 + Math.random() * 0.95);
      spawn({ x, y, vx: Math.cos(a) * s, vy: Math.sin(a) * s, life: 0.4 + Math.random() * 0.6, size: 2 + Math.random() * 5, color, grav: 500, drag: 1.2, shape: Math.random() < 0.5 ? 'rect' : 'circle' });
    }
  }
  function sparks(x, y, color, n = 8) {
    for (let i = 0; i < n; i++) {
      const a = Math.random() * Math.PI * 2, s = 120 + Math.random() * 380;
      spawn({ x, y, vx: Math.cos(a) * s, vy: Math.sin(a) * s - 120, life: 0.3 + Math.random() * 0.4, size: 1.5 + Math.random() * 3, color, grav: 200, drag: 0.6 });
    }
  }
  function trail(x, y, color, size = 5, life = 0.35) {
    spawn({ x: x + (Math.random() - 0.5) * 4, y: y + (Math.random() - 0.5) * 4, vx: (Math.random() - 0.5) * 40, vy: (Math.random() - 0.5) * 40, life, size: size * (0.5 + Math.random() * 0.6), color, drag: 2, shape: 'circle' });
  }
  function ring(x, y, color, maxR = 60) {
    flashes.push({ x, y, color, age: 0, life: 0.35, maxR });
  }
  function floatText(x, y, str, color = '#fff', size = 18) {
    if (texts.length > 40) texts.shift();
    texts.push({ x, y, str, color, size, age: 0, life: 1.1 });
  }
  function addShake(v) { shake = Math.min(26, shake + v); }
  function addPulse(v) { pulse = Math.min(1.2, pulse + v); }

  function update(dt) {
    shake = Math.max(0, shake - dt * 60);
    shakeX = (Math.random() - 0.5) * shake; shakeY = (Math.random() - 0.5) * shake;
    pulse = Math.max(0, pulse - dt * 1.6);
    for (let i = parts.length - 1; i >= 0; i--) {
      const p = parts[i]; p.age += dt;
      if (p.age >= p.life) { parts.splice(i, 1); continue; }
      p.vy += (p.grav || 0) * dt;
      const dr = 1 - Math.min(0.95, (p.drag || 0) * dt);
      p.vx *= dr; p.vy *= dr; p.x += p.vx * dt; p.y += p.vy * dt;
    }
    for (let i = texts.length - 1; i >= 0; i--) {
      const t = texts[i]; t.age += dt; t.y -= 46 * dt;
      if (t.age >= t.life) texts.splice(i, 1);
    }
    for (let i = flashes.length - 1; i >= 0; i--) {
      flashes[i].age += dt;
      if (flashes[i].age >= flashes[i].life) flashes.splice(i, 1);
    }
  }

  function drawParts(ctx) {
    ctx.save();
    for (const p of parts) {
      const k = 1 - p.age / p.life;
      ctx.globalAlpha = Math.max(0, k);
      if (p.glow) { ctx.shadowBlur = 10; ctx.shadowColor = p.color; }
      else ctx.shadowBlur = 0;
      ctx.fillStyle = p.color;
      if (p.shape === 'circle') { ctx.beginPath(); ctx.arc(p.x, p.y, p.size * k + 0.5, 0, 7); ctx.fill(); }
      else {
        ctx.save(); ctx.translate(p.x, p.y); ctx.rotate(p.age * 6);
        const s = p.size * k + 0.5; ctx.fillRect(-s / 2, -s / 2, s, s * 0.7); ctx.restore();
      }
    }
    ctx.restore();
    ctx.save();
    for (const f of flashes) {
      const k = f.age / f.life;
      ctx.globalAlpha = 1 - k; ctx.strokeStyle = f.color; ctx.lineWidth = 3 * (1 - k) + 1;
      ctx.shadowBlur = 16; ctx.shadowColor = f.color;
      ctx.beginPath(); ctx.arc(f.x, f.y, f.maxR * k + 4, 0, 7); ctx.stroke();
    }
    ctx.restore();
  }
  function drawTexts(ctx) {
    ctx.save(); ctx.textAlign = 'center'; ctx.font = '800 18px system-ui,sans-serif';
    for (const t of texts) {
      const k = 1 - t.age / t.life;
      ctx.globalAlpha = Math.min(1, k * 2);
      ctx.font = `900 ${t.size}px system-ui,sans-serif`;
      ctx.shadowBlur = 12; ctx.shadowColor = t.color;
      ctx.fillStyle = t.color; ctx.strokeStyle = 'rgba(0,0,0,.8)'; ctx.lineWidth = 3;
      ctx.strokeText(t.str, t.x, t.y); ctx.fillText(t.str, t.x, t.y);
    }
    ctx.restore();
  }

  return { spawn, burst, sparks, trail, ring, floatText, addShake, addPulse, update, drawParts, drawTexts,
    get shakeX() { return shakeX; }, get shakeY() { return shakeY; }, get pulse() { return pulse; }, parts };
})();
