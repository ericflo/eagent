/* BREAKTHROUGH — js/particles.js
   Object-pooled particle system + floating text popups + shockwaves.
   Handles: shatter debris, sparks, trails glow, ripples, confetti (frenzy).
*/
window.Particles = (function () {
  const U = window.U;
  const MAX = 1400;
  const pool = [];
  const live = [];
  const texts = [];
  const ripples = [];

  function spawn(opts) {
    let p = pool.pop();
    if (!p) { if (live.length >= MAX) return null; p = {}; }
    p.x = opts.x || 0; p.y = opts.y || 0;
    p.vx = opts.vx || 0; p.vy = opts.vy || 0;
    p.size = opts.size || 3;
    p.life = opts.life || 0.6; p.maxLife = p.life;
    p.drag = opts.drag === undefined ? 0.9 : opts.drag;
    p.grav = opts.grav === undefined ? 260 : opts.grav;
    p.hue = opts.hue === undefined ? 200 : opts.hue;
    p.sat = opts.sat === undefined ? 90 : opts.sat;
    p.lit = opts.lit === undefined ? 60 : opts.lit;
    p.hueEnd = opts.hueEnd === undefined ? p.hue : opts.hueEnd;
    p.shape = opts.shape || 'rect';       // rect | spark | glow | tri
    p.spin = opts.spin || 0; p.rot = opts.rot || 0;
    p.glow = opts.glow === undefined ? 0 : opts.glow;
    live.push(p);
    return p;
  }

  // helpers that emit in bulk -------------------------------------------
  function burst(x, y, n, opts = {}) {
    for (let i = 0; i < n; i++) {
      const a = opts.dir !== undefined ? opts.dir + U.rand(-0.9, 0.9) : U.rand(U.TAU);
      const sp = U.rand(opts.spMin || 60, opts.spMax || 320);
      spawn({
        x, y, vx: Math.cos(a) * sp, vy: Math.sin(a) * sp,
        size: U.rand(2, opts.size || 6), life: U.rand(0.3, opts.life || 0.9),
        hue: opts.hue !== undefined ? U.rand(opts.hue - 14, opts.hue + 14) : U.rand(360),
        shape: opts.shape || 'rect', glow: opts.glow || 0, drag: opts.drag,
        grav: opts.grav, hueEnd: opts.hueEnd, lit: opts.lit, sat: opts.sat
      });
    }
  }
  function shatter(b, hue) { // brick-sized debris explosion
    const cx = b.x + b.w / 2, cy = b.y + b.h / 2;
    for (let i = 0; i < 26; i++) {
      spawn({
        x: cx + U.rand(-b.w / 2, b.w / 2), y: cy + U.rand(-b.h / 2, b.h / 2),
        vx: U.rand(-260, 260), vy: U.rand(-320, 60),
        size: U.rand(3, 8), life: U.rand(0.5, 1.1),
        hue: U.rand(hue - 12, hue + 12), shape: 'rect', spin: U.rand(-8, 8),
        glow: 0.2
      });
    }
    burst(cx, cy, 16, { hue: hue + 20, shape: 'spark', glow: 0.6, spMax: 420, grav: 120, life: 0.5 });
  }
  function impact(x, y, hue, dir) { // paddle / surface impact sparks
    burst(x, y, 10, { hue, shape: 'spark', glow: 0.7, dir: dir === undefined ? U.rand(U.TAU) : dir, spMax: 260, life: 0.4, grav: 200 });
  }
  function confetti(x, y, n) {
    for (let i = 0; i < n; i++) {
      spawn({
        x, y, vx: U.rand(-180, 180), vy: U.rand(-420, -80),
        size: U.rand(3, 7), life: U.rand(0.8, 1.6),
        hue: U.rand(360), sat: 95, lit: 62, shape: 'rect', spin: U.rand(-10, 10), grav: 320, drag: 0.995
      });
    }
  }

  // floating score / combo text -----------------------------------------
  function text(x, y, str, opts = {}) {
    texts.push({
      x, y, str, life: opts.life || 1.0, maxLife: opts.life || 1.0,
      hue: opts.hue || 190, size: opts.size || 22, weight: opts.weight || 900,
      vy: opts.vy === undefined ? -70 : opts.vy, vx: opts.vx || 0,
      scalePop: opts.pop === undefined ? 1.6 : opts.pop
    });
    if (texts.length > 40) texts.shift();
  }
  function ripple(x, y, opts = {}) {
    ripples.push({ x, y, r: opts.r || 6, maxR: opts.maxR || 70, life: opts.life || 0.4, maxLife: opts.life || 0.4, hue: opts.hue || 200, w: opts.w || 3 });
    if (ripples.length > 30) ripples.shift();
  }

  function update(dt) {
    for (let i = live.length - 1; i >= 0; i--) {
      const p = live[i];
      p.life -= dt;
      if (p.life <= 0) { live.splice(i, 1); if (pool.length < MAX) pool.push(p); continue; }
      const d = Math.pow(p.drag, dt * 60);
      p.vx *= d; p.vy *= d;
      p.vy += p.grav * dt;
      p.x += p.vx * dt; p.y += p.vy * dt;
      p.rot += p.spin * dt;
    }
    for (let i = texts.length - 1; i >= 0; i--) {
      const t = texts[i];
      t.life -= dt; t.y += t.vy * dt; t.x += t.vx * dt;
      t.vy *= Math.pow(0.92, dt * 60);
      if (t.life <= 0) texts.splice(i, 1);
    }
    for (let i = ripples.length - 1; i >= 0; i--) {
      const r = ripples[i];
      r.life -= dt;
      if (r.life <= 0) ripples.splice(i, 1);
    }
  }

  function draw(ctx) {
    // ripples under particles
    for (const r of ripples) {
      const t = 1 - r.life / r.maxLife;
      const rr = r.r + (r.maxR - r.r) * U.easeOutCubic(t);
      ctx.strokeStyle = U.hsl(r.hue, 90, 70, (1 - t) * 0.8);
      ctx.lineWidth = r.w * (1 - t) + 0.5;
      ctx.beginPath(); ctx.arc(r.x, r.y, rr, 0, U.TAU); ctx.stroke();
    }
    ctx.globalCompositeOperation = 'lighter';
    for (const p of live) {
      const t = p.life / p.maxLife;
      const hue = U.lerp(p.hue, p.hueEnd, 1 - t);
      const a = Math.min(1, t * 1.4);
      const col = U.hsl(hue, p.sat, p.lit, a);
      if (p.shape === 'spark') {
        const len = Math.min(14, Math.hypot(p.vx, p.vy) * 0.035);
        const ang = Math.atan2(p.vy, p.vx);
        ctx.strokeStyle = col; ctx.lineWidth = p.size * t;
        ctx.beginPath();
        ctx.moveTo(p.x, p.y);
        ctx.lineTo(p.x - Math.cos(ang) * len, p.y - Math.sin(ang) * len);
        ctx.stroke();
      } else if (p.shape === 'glow') {
        const g = ctx.createRadialGradient(p.x, p.y, 0, p.x, p.y, p.size * 3);
        g.addColorStop(0, U.hsl(hue, 100, 75, a * 0.8));
        g.addColorStop(1, U.hsl(hue, 100, 60, 0));
        ctx.fillStyle = g;
        ctx.beginPath(); ctx.arc(p.x, p.y, p.size * 3, 0, U.TAU); ctx.fill();
      } else {
        ctx.save();
        ctx.translate(p.x, p.y); ctx.rotate(p.rot);
        ctx.fillStyle = col;
        if (p.shape === 'tri') {
          ctx.beginPath();
          ctx.moveTo(-p.size, p.size); ctx.lineTo(0, -p.size); ctx.lineTo(p.size, p.size);
          ctx.closePath(); ctx.fill();
        } else {
          ctx.fillRect(-p.size / 2, -p.size / 2, p.size, p.size);
        }
        ctx.restore();
      }
    }
    ctx.globalCompositeOperation = 'source-over';
  }

  function drawTexts(ctx) {
    for (const t of texts) {
      const k = 1 - t.life / t.maxLife;                 // 0..1 age
      const pop = 1 + (t.scalePop - 1) * U.easeOutBack(Math.min(1, k * 4)) * (1 - k);
      const a = k < 0.7 ? 1 : 1 - (k - 0.7) / 0.3;
      ctx.save();
      ctx.translate(t.x, t.y); ctx.scale(pop, pop);
      ctx.font = `${t.weight} ${t.size}px ui-rounded, system-ui, sans-serif`;
      ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
      ctx.shadowColor = U.hsl(t.hue, 100, 60, a); ctx.shadowBlur = 14;
      ctx.fillStyle = U.hsl(t.hue, 100, 82, a);
      ctx.fillText(t.str, 0, 0);
      ctx.restore();
    }
  }

  function clear() { live.length = 0; texts.length = 0; ripples.length = 0; }
  function count() { return live.length; }

  return { spawn, burst, shatter, impact, confetti, text, ripple, update, draw, drawTexts, clear, count };
})();