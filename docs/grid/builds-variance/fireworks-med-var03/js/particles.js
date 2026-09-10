'use strict';
// ---------------------------------------------------------------------------
// particles.js — pooled particle system: sparks, shards, shockwave rings,
// comet trails, ambient starfield / grid backdrop that reacts to hype.
// ---------------------------------------------------------------------------

const Particles = (() => {
  const MAX = 2600;
  const pool = [];
  const active = [];
  for (let i = 0; i < MAX; i++) pool.push({});

  function get() {
    const p = pool.pop();
    if (!p) return null;
    active.push(p);
    return p;
  }

  function spark(x, y, opts = {}) {
    const p = get(); if (!p) return;
    p.kind = 'spark';
    p.x = x; p.y = y;
    const a = opts.angle !== undefined ? opts.angle : Math.random() * TAU;
    const sp = (opts.speed || 160) * lerp(0.4, 1, Math.random());
    p.vx = Math.cos(a) * sp + (opts.vx || 0);
    p.vy = Math.sin(a) * sp + (opts.vy || 0);
    p.life = p.maxLife = opts.life || lerp(0.25, 0.6, Math.random());
    p.size = opts.size || lerp(1.5, 3.5, Math.random());
    p.color = opts.color || '#ffd166';
    p.drag = opts.drag !== undefined ? opts.drag : 0.9;
    p.grav = opts.grav !== undefined ? opts.grav : 300;
    p.glow = opts.glow !== false;
    p.spin = (Math.random() - 0.5) * 10;
    p.rot = Math.random() * TAU;
    p.shape = 'circle';
    p.trail = !!opts.trail;
  }

  function shard(x, y, color, opts = {}) {
    const p = get(); if (!p) return;
    p.kind = 'shard';
    p.x = x; p.y = y;
    const a = Math.random() * TAU;
    const sp = (opts.speed || 120) * lerp(0.3, 1, Math.random());
    p.vx = Math.cos(a) * sp;
    p.vy = Math.sin(a) * sp - 60;
    p.life = p.maxLife = opts.life || lerp(0.4, 0.9, Math.random());
    p.size = opts.size || lerp(3, 7, Math.random());
    p.color = color;
    p.drag = 0.96;
    p.grav = 500;
    p.glow = false;
    p.spin = (Math.random() - 0.5) * 14;
    p.rot = Math.random() * TAU;
    p.shape = 'rect';
    p.trail = false;
  }

  function ring(x, y, opts = {}) {
    const p = get(); if (!p) return;
    p.kind = 'ring';
    p.x = x; p.y = y;
    p.vx = 0; p.vy = 0;
    p.life = p.maxLife = opts.life || 0.45;
    p.size = opts.size || 8;          // start radius
    p.grow = opts.grow || 300;        // px/s
    p.color = opts.color || '#fff';
    p.width = opts.width || 3;
    p.glow = true;
    p.grav = 0; p.drag = 1;
  }

  function textPuff() { /* reserved */ }

  function update(dt) {
    for (let i = active.length - 1; i >= 0; i--) {
      const p = active[i];
      p.life -= dt;
      if (p.life <= 0) {
        active.splice(i, 1);
        pool.push(p);
        continue;
      }
      if (p.kind === 'ring') {
        p.size += p.grow * dt;
        continue;
      }
      p.vy += (p.grav || 0) * dt;
      p.x += p.vx * dt;
      p.y += p.vy * dt;
      const d = Math.pow(p.drag || 1, dt * 60);
      p.vx *= d; p.vy *= d;
      if (p.spin) p.rot += p.spin * dt;
    }
  }

  function draw(ctx) {
    // rings first (under)
    for (const p of active) {
      if (p.kind !== 'ring') continue;
      const a = p.life / p.maxLife;
      ctx.save();
      ctx.globalAlpha = a * 0.8;
      ctx.strokeStyle = p.color;
      ctx.lineWidth = p.width * a;
      if (p.glow) { ctx.shadowBlur = 12; ctx.shadowColor = p.color; }
      ctx.beginPath();
      ctx.arc(p.x, p.y, p.size, 0, TAU);
      ctx.stroke();
      ctx.restore();
    }
    // sparks & shards
    for (const p of active) {
      if (p.kind === 'ring') continue;
      const a = clamp(p.life / p.maxLife * 1.6, 0, 1);
      ctx.save();
      ctx.globalAlpha = a;
      if (p.glow) { ctx.shadowBlur = 10; ctx.shadowColor = p.color; }
      ctx.fillStyle = p.color;
      if (p.shape === 'rect') {
        ctx.translate(p.x, p.y);
        ctx.rotate(p.rot);
        ctx.fillRect(-p.size / 2, -p.size / 2, p.size, p.size * 0.7);
      } else {
        ctx.beginPath();
        if (p.trail && a > 0.5) {
          // stretched motion streak
          const ang = Math.atan2(p.vy, p.vx);
          ctx.ellipse(p.x, p.y, p.size * 2.2, p.size * 0.7, ang, 0, TAU);
        } else {
          ctx.arc(p.x, p.y, p.size * a, 0, TAU);
        }
        ctx.fill();
      }
      ctx.restore();
    }
  }

  function burst(x, y, n, color, opts = {}) {
    for (let i = 0; i < n; i++) spark(x, y, { color, ...opts });
  }

  function brickBurst(x, y, w, h, color, hype) {
    const n = 6 + Math.round(hype * 10);
    for (let i = 0; i < n; i++) shard(x, y, color);
    ring(x, y, { color, size: Math.min(w, h) * 0.4, grow: 260 + hype * 300, life: 0.35 });
    for (let i = 0; i < 3 + hype * 6; i++) spark(x, y, { color: '#fff', speed: 260 + hype * 300, life: 0.3 });
  }

  function count() { return active.length; }
  function clear() {
    while (active.length) pool.push(active.pop());
  }

  return { spark, shard, ring, burst, brickBurst, update, draw, count, clear };
})();

// ---------------------------------------------------------------------------
// Backdrop — layered parallax grid + stars that react to hype level.
// ---------------------------------------------------------------------------
const Backdrop = (() => {
  let W = 0, H = 0, scale = 1;
  const stars = [];
  let pulse = 0; // 0..1 ambient pulse synced to hype

  function init(w, h, s) {
    W = w; H = h; scale = s;
    stars.length = 0;
    const n = 90;
    const rng = mulberry32(1234);
    for (let i = 0; i < n; i++) {
      stars.push({
        x: rng() * W, y: rng() * H,
        z: lerp(0.2, 1, rng()),       // depth
        tw: rng() * TAU,
        hue: 200 + rng() * 120,
      });
    }
  }

  function resize(w, h) { W = w; H = h; }

  function update(dt, hype) {
    pulse = lerp(pulse, hype, dt * 3);
    for (const s of stars) {
      s.tw += dt * (0.5 + s.z * 2);
      s.y += dt * s.z * (6 + pulse * 60);
      if (s.y > H + 4) { s.y = -4; s.x = Math.random() * W; }
    }
  }

  function draw(ctx) {
    // deep space gradient
    const g = ctx.createLinearGradient(0, 0, 0, H);
    g.addColorStop(0, '#070912');
    g.addColorStop(0.55, '#0a0d1f');
    g.addColorStop(1, '#05060f');
    ctx.fillStyle = g;
    ctx.fillRect(0, 0, W, H);

    // hype aurora blobs (drift)
    const t = performance.now() / 1000;
    const blobs = 3;
    for (let i = 0; i < blobs; i++) {
      const bx = W * (0.3 + 0.4 * Math.sin(t * 0.11 + i * 2.1));
      const by = H * (0.3 + 0.35 * Math.cos(t * 0.09 + i * 1.7));
      const r = Math.min(W, H) * (0.35 + 0.1 * pulse);
      const hue = 190 + i * 70 + pulse * 60;
      const rg = ctx.createRadialGradient(bx, by, 0, bx, by, r);
      rg.addColorStop(0, `hsla(${hue},80%,55%,${0.05 + pulse * 0.09})`);
      rg.addColorStop(1, 'hsla(0,0%,0%,0)');
      ctx.fillStyle = rg;
      ctx.fillRect(0, 0, W, H);
    }

    // perspective grid horizon at bottom, brighter with hype
    const horizon = H * 0.72;
    ctx.save();
    ctx.globalAlpha = 0.08 + pulse * 0.14;
    ctx.strokeStyle = `hsl(${200 + pulse * 80}, 90%, 60%)`;
    ctx.lineWidth = 1;
    const scroll = (t * (30 + pulse * 90)) % 40;
    for (let y = horizon; y < H + 40; y += 40) {
      const yy = y + scroll;
      if (yy > H + 40) continue;
      ctx.beginPath();
      ctx.moveTo(0, yy); ctx.lineTo(W, yy);
      ctx.stroke();
    }
    for (let i = -6; i <= 6; i++) {
      const x = W / 2 + i * (W / 7);
      ctx.beginPath();
      ctx.moveTo(W / 2 + i * 14, horizon);
      ctx.lineTo(x, H + 40);
      ctx.stroke();
    }
    ctx.restore();

    // stars
    for (const s of stars) {
      const a = (0.25 + 0.75 * Math.abs(Math.sin(s.tw))) * s.z * (0.6 + pulse * 0.8);
      ctx.fillStyle = `hsla(${s.hue},70%,80%,${clamp(a, 0, 1)})`;
      const sz = s.z * 1.8 * scale + pulse * s.z;
      ctx.fillRect(s.x, s.y, sz, sz);
    }
  }

  return { init, resize, update, draw, get pulse() { return pulse; } };
})();
