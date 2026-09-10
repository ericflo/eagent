/* ============================================================
   TOPSIDE — fx.js
   Particles, floating text, shockwaves, rings, confetti.
   All rendering here expects the 2D context passed in.
   ============================================================ */
'use strict';

const FX = (() => {
  const parts = [];
  const texts = [];
  const shocks = [];
  let cap = 1400; // particle cap

  function addPart(x, y, vx, vy, life, size, color, o = {}) {
    if (parts.length >= cap) {
      /* full: evict the oldest particle so the NEW effect always spawns
         (newest-always-spawned policy). O(1) shift amortizes to O(n). */
      parts.shift();
    }
    parts.push({
      x, y, vx, vy, life, max: life, size, color,
      grav: o.grav || 0, drag: o.drag || 1,
      glow: o.glow !== false, shape: o.shape || 'dot',
      rot: RNG.next() * TAU, vr: RNG.range(-8, 8),
      shock: o.shock || false
    });
  }

  /* burst of particles */
  function burst(x, y, o = {}) {
    const colors = o.colors || ['#ffffff'];
    const count = o.count || 12;
    const sp = o.speed || [40, 260];
    const sz = o.size || [2, 5];
    const life = o.life || [0.35, 0.85];
    const grav = o.grav || 0;
    const glow = o.glow !== false;
    const shape = o.shape || 'dot';
    const ang = o.ang;               // directional bias (radians)
    const spread = o.spread || TAU;
    for (let i = 0; i < count; i++) {
      const a = ang !== undefined ? ang + RNG.range(-spread / 2, spread / 2) : RNG.next() * TAU;
      const s = RNG.range(sp[0], sp[1]);
      addPart(x, y, Math.cos(a) * s, Math.sin(a) * s,
        RNG.range(life[0], life[1]), RNG.range(sz[0], sz[1]),
        colors[i % colors.length], { grav, glow, shape });
    }
  }

  /* brick hit: chip shards flying off the surface */
  function brickHit(x, y, colors, dir) {
    const a = Math.atan2(dir.y, dir.x);
    burst(x, y, {
      colors, count: 7, speed: [80, 280], size: [1.5, 4],
      life: [0.24, 0.48], grav: 320, glow: true, shape: 'dot',
      ang: a, spread: 2.4
    });
    /* few square chips */
    burst(x, y, {
      colors, count: 4, speed: [60, 220], size: [3, 6],
      life: [0.32, 0.72], grav: 500, glow: false, shape: 'chip',
      ang: a, spread: 3.0
    });
  }

  /* brick destroyed: big confetti pop */
  function brickPop(x, y, colors) {
    burst(x, y, { colors, count: 24, speed: [90, 330], size: [2, 5], life: [0.32, 0.8], grav: 380, glow: true });
    burst(x, y, { colors, count: 8, speed: [60, 200], size: [4, 8], life: [0.4, 0.88], grav: 520, glow: false, shape: 'chip' });
  }

  /* screen-center confetti wave (level up / big moments) */
  function confetti() {
    const colors = ['#ff5c8a', '#ffd84d', '#4ade80', '#22d3ee', '#b05cff', '#ffffff'];
    const cy = CFG.H * 0.35;
    for (let i = 0; i < 56; i++) {
      addPart(
        RNG.range(60, CFG.W - 60), cy - RNG.range(0, 120),
        RNG.range(-40, 40), RNG.range(-160, -40),
        RNG.range(0.8, 1.8), RNG.range(3, 7),
        colors[i % colors.length],
        { grav: 260, glow: false, shape: 'chip', drag: 0.995 }
      );
    }
  }

  /* shockwave ring */
  function shock(x, y, o = {}) {
    shocks.push({
      x, y,
      r: o.r0 || 6,
      r1: o.r1 || 130,
      life: o.life || 0.5,
      max: o.life || 0.5,
      color: o.color || '#ffffff',
      lw: o.lw || 5,
      glow: o.glow !== false
    });
  }

  /* floating text */
  function text(x, y, str, o = {}) {
    texts.push({
      x, y, str,
      life: o.life || 1.1, max: o.life || 1.1,
      size: o.size || 26,
      color: o.color || '#ffffff',
      vy: o.vy || -70,
      vx: o.vx || 0,
      font: o.font || '800',
      glow: o.glow !== false
    });
  }

  /* speed-line streaks in a direction (bomb / cannon / flux) */
  function streakLine(x1, y1, x2, y2, color = '#fff', life = 0.25) {
    /* simple: four dots along the line */
    for (let i = 0; i < 5; i++) {
      const t = i / 4;
      addPart(lerp(x1, x2, t), lerp(y1, y2, t), 0, 0, life, 2.5 - t * 1.8, color, { glow: true });
    }
  }

  /* snow flurry for freeze / slow-mo ambiance */
  function snowBurst(n = 24) {
    for (let i = 0; i < n; i++) {
      addPart(
        RNG.next() * CFG.W, RNG.next() * CFG.H,
        RNG.range(-10, 10), RNG.range(20, 80),
        RNG.range(0.6, 1.6), RNG.range(1.5, 4),
        '#bfeaff', { grav: -8, glow: true, drag: 1 }
      );
    }
  }

  /* subtle golden shimmer raining around the riding ball (crown moment) */
  function ridingShimmer(x, y) {
    for (let i = 0; i < 2; i++) {
      const dx = RNG.range(-46, 46);
      addPart(
        x + dx, y + RNG.range(-30, 26),
        RNG.range(-12, 12), RNG.range(60, 150),
        RNG.range(0.35, 0.7), RNG.range(1, 2.4),
        RNG.chance(0.5) ? '#ffe14d' : '#ffd84d',
        { grav: 40, glow: true, drag: 0.985 }
      );
    }
  }

  function hitStop(ms) { S.hitStop = Math.max(S.hitStop || 0, ms); }

  function update(dt, ctxUnused) {
    for (let i = parts.length - 1; i >= 0; i--) {
      const p = parts[i];
      p.life -= dt;
      if (p.life <= 0) { parts.splice(i, 1); continue; }
      p.vy += p.grav * dt;
      const d = Math.pow(p.drag, dt * 60);
      p.vx *= d; p.vy *= d;
      p.x += p.vx * dt; p.y += p.vy * dt;
      p.rot += p.vr * dt;
    }
    for (let i = texts.length - 1; i >= 0; i--) {
      const t = texts[i];
      t.life -= dt;
      if (t.life <= 0) { texts.splice(i, 1); continue; }
      t.x += t.vx * dt; t.y += t.vy * dt;
    }
    for (let i = shocks.length - 1; i >= 0; i--) {
      const s = shocks[i];
      s.life -= dt;
      if (s.life <= 0) { shocks.splice(i, 1); continue; }
    }
  }

  function render(ctx) {
    /* shockwaves (under particles) */
    for (const s of shocks) {
      const t = 1 - s.life / s.max;
      const r = lerp(s.r, s.r1, easeOutCubic(t));
      const a = (1 - t) * 0.9;
      ctx.globalAlpha = a;
      ctx.strokeStyle = s.color;
      ctx.lineWidth = s.lw;
      if (s.glow) { ctx.shadowColor = s.color; ctx.shadowBlur = 18; }
      ctx.beginPath();
      ctx.arc(s.x, s.y, r, 0, TAU);
      ctx.stroke();
      ctx.shadowBlur = 0;
      ctx.globalAlpha = 1;
    }

    /* particles */
    for (const p of parts) {
      const t = p.life / p.max;
      if (p.shape === 'chip') {
        ctx.globalAlpha = Math.min(1, t * 1.4);
        ctx.fillStyle = p.color;
        ctx.save();
        ctx.translate(p.x, p.y);
        ctx.rotate(Math.sin(p.rot) * 0.5);
        ctx.fillRect(-p.size / 2, -p.size / 2, p.size, p.size * 0.7);
        ctx.restore();
        ctx.globalAlpha = 1;
      } else {
        const a = Math.min(1, t * 1.5);
        if (p.glow) {
          ctx.globalAlpha = a * 0.35;
          ctx.fillStyle = p.color;
          ctx.beginPath();
          ctx.arc(p.x, p.y, p.size * 2.4, 0, TAU);
          ctx.fill();
        }
        ctx.globalAlpha = Math.min(1, a);
        ctx.fillStyle = p.color;
        ctx.beginPath();
        ctx.arc(p.x, p.y, p.size, 0, TAU);
        ctx.fill();
        ctx.globalAlpha = 1;
      }
    }

    /* floating text */
    for (const t of texts) {
      const k = t.life / t.max;
      const a = k < 0.3 ? k / 0.3 : 1;
      const pop = t.max - t.life < 0.12 ? 1 + (0.12 - (t.max - t.life)) * 4 : 1;
      ctx.globalAlpha = a;
      ctx.font = `${t.font} ${t.size * pop}px 'Avenir Next', system-ui, sans-serif`;
      ctx.textAlign = 'center';
      ctx.textBaseline = 'middle';
      if (t.glow) { ctx.shadowColor = t.color; ctx.shadowBlur = 16; }
      ctx.fillStyle = t.color;
      ctx.fillText(t.str, t.x, t.y);
      ctx.shadowBlur = 0;
      ctx.globalAlpha = 1;
    }
  }

  function easeOutCubic(t) { return 1 - Math.pow(1 - t, 3); }

  function clear() {
    parts.length = 0; texts.length = 0; shocks.length = 0;
  }

  return { burst, brickHit, brickPop, confetti, shock, text, streakLine, snowBurst, ridingShimmer, hitStop, update, render, clear, count: () => parts.length, cap: () => cap };
})();
