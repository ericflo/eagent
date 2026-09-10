// particles.js — pooled particles, floating score text, shockwave rings
'use strict';

const Particles = (() => {
  const pool = new Util.Pool(() => ({
    x: 0, y: 0, vx: 0, vy: 0, life: 0, maxLife: 1, size: 3, color: '#fff',
    grav: 0, spin: 0, rot: 0, kind: 0, // kind: 0 shard, 1 spark, 2 ember, 3 confetti
  }), CONFIG.PARTICLE_CAP);

  const texts = [];
  const rings = [];

  function spawn(x, y, vx, vy, life, size, color, opts = {}) {
    const p = pool.get();
    if (!p) { pool.items[0].active = false; return spawn(x, y, vx, vy, life, size, color, opts); }
    Object.assign(p, { x, y, vx, vy, life, maxLife: life, size, color, rot: 0,
      grav: opts.grav || 0, spin: opts.spin || 0, kind: opts.kind || 0 });
    return p;
  }

  function shards(x, y, color, n = 12, power = 1) {
    for (let i = 0; i < n; i++) {
      const a = Math.random() * Math.PI * 2, s = (80 + Math.random() * 260) * power;
      spawn(x, y, Math.cos(a) * s, Math.sin(a) * s, 0.5 + Math.random() * 0.4,
        3 + Math.random() * 5, color, { grav: 700, spin: (Math.random() - 0.5) * 14, kind: 0 });
    }
  }
  function sparks(x, y, n = 8) {
    for (let i = 0; i < n; i++) {
      const a = Math.random() * Math.PI * 2, s = 200 + Math.random() * 350;
      spawn(x, y, Math.cos(a) * s, Math.sin(a) * s, 0.2 + Math.random() * 0.2, 2, '#fff8b0', { kind: 1 });
    }
  }
  function embers(x, y, n = 2) {
    for (let i = 0; i < n; i++)
      spawn(x + (Math.random() - 0.5) * 10, y, (Math.random() - 0.5) * 40, -60 - Math.random() * 90,
        0.4 + Math.random() * 0.3, 2 + Math.random() * 2, '#fb923c', { grav: -60, kind: 2 });
  }
  function confetti(x, y, n = 24) {
    const cols = ['#f472b6', '#facc15', '#5eead4', '#38bdf8', '#c084fc'];
    for (let i = 0; i < n; i++) {
      const a = Math.random() * Math.PI * 2, s = 120 + Math.random() * 320;
      spawn(x, y, Math.cos(a) * s, Math.sin(a) * s - 150, 0.9 + Math.random() * 0.5,
        3 + Math.random() * 4, cols[(Math.random() * cols.length) | 0],
        { grav: 500, spin: (Math.random() - 0.5) * 20, kind: 3 });
    }
  }
  function rain(strength) {
    if (Math.random() > strength) return;
    const x = Math.random() * CONFIG.LOGICAL_W;
    spawn(x, -10, (Math.random() - 0.5) * 30, 220 + Math.random() * 260,
      2.5, 2.5, 'rgba(255,255,255,0.6)', { grav: 30, kind: 1 });
  }
  function streaks(x, y, n = 6) { // punch whoosh streaks
    for (let i = 0; i < n; i++)
      spawn(x + (Math.random() - 0.5) * 60, y, (Math.random() - 0.5) * 60, -300 - Math.random() * 300,
        0.25, 2.5, 'rgba(255,255,255,0.8)', { kind: 1 });
  }

  function text(x, y, str, opts = {}) {
    texts.push({ x, y, str, life: opts.life || 0.9, maxLife: opts.life || 0.9,
      color: opts.color || '#fff', size: opts.size || 26, vy: opts.vy || -70 });
    if (texts.length > 40) texts.shift();
  }
  function ring(x, y, opts = {}) {
    rings.push({ x, y, r: opts.r0 || 10, vr: opts.vr || 500, life: opts.life || 0.4,
      maxLife: opts.life || 0.4, color: opts.color || '#fff', width: opts.width || 4 });
    if (rings.length > 20) rings.shift();
  }

  function update(dt) {
    pool.each(p => {
      p.life -= dt; p.x += p.vx * dt; p.y += p.vy * dt;
      p.vy += p.grav * dt; p.rot += p.spin * dt;
      if (p.life <= 0) p.active = false;
    });
    for (let i = texts.length - 1; i >= 0; i--) {
      const t = texts[i]; t.life -= dt; t.y += t.vy * dt; t.vy *= (1 - 2 * dt);
      if (t.life <= 0) texts.splice(i, 1);
    }
    for (let i = rings.length - 1; i >= 0; i--) {
      const r = rings[i]; r.life -= dt; r.r += r.vr * dt; r.vr *= (1 - 3 * dt);
      if (r.life <= 0) rings.splice(i, 1);
    }
  }

  function draw(ctx) {
    pool.each(p => {
      const a = Util.clamp(p.life / p.maxLife, 0, 1);
      ctx.globalAlpha = a;
      ctx.fillStyle = p.color;
      if (p.kind === 0 || p.kind === 3) { // shard / confetti: rotated rect
        ctx.save(); ctx.translate(p.x, p.y); ctx.rotate(p.rot);
        ctx.fillRect(-p.size / 2, -p.size / 2, p.size, p.size * (p.kind === 3 ? 0.6 : 1.4));
        ctx.restore();
      } else if (p.kind === 2) { // ember glow
        ctx.beginPath(); ctx.arc(p.x, p.y, p.size, 0, 7); ctx.fill();
      } else { // spark: streak line
        ctx.strokeStyle = p.color; ctx.lineWidth = p.size;
        ctx.beginPath(); ctx.moveTo(p.x, p.y);
        ctx.lineTo(p.x - p.vx * 0.02, p.y - p.vy * 0.02); ctx.stroke();
      }
    });
    for (const r of rings) {
      const a = Util.clamp(r.life / r.maxLife, 0, 1);
      ctx.globalAlpha = a * 0.8; ctx.strokeStyle = r.color; ctx.lineWidth = r.width * a;
      ctx.beginPath(); ctx.arc(r.x, r.y, r.r, 0, 7); ctx.stroke();
    }
    for (const t of texts) {
      const a = Util.clamp(t.life / t.maxLife, 0, 1);
      ctx.globalAlpha = a;
      ctx.font = `800 ${t.size}px system-ui, sans-serif`;
      ctx.textAlign = 'center';
      ctx.lineWidth = 4; ctx.strokeStyle = 'rgba(0,0,0,0.7)';
      ctx.strokeText(t.str, t.x, t.y); ctx.fillStyle = t.color; ctx.fillText(t.str, t.x, t.y);
    }
    ctx.globalAlpha = 1;
  }

  function clear() { pool.clear(); texts.length = 0; rings.length = 0; }

  return { shards, sparks, embers, confetti, rain, streaks, text, ring, update, draw, clear,
    pool, texts, rings };
})();