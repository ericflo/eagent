// balls.js — ball types: NORMAL, FIRE (pierce standard), HEAVY (plow 3), SPLITTER, GHOST (ignores wedge)
'use strict';

const Balls = (() => {
  const list = []; // plain objects (few balls; no pooling needed)
  let nextId = 1;

  function make(x, y, angle, speed, type = 'NORMAL') {
    const b = {
      id: nextId++, x, y, r: CONFIG.BALL_R, type,
      vx: Math.cos(angle) * speed, vy: Math.sin(angle) * speed,
      stuck: false,            // magnet: stuck to paddle
      stuckOff: 0,
      fireTimer: type === 'FIRE' ? CONFIG.FIRE_DURATION : 0,
      heavyLeft: type === 'HEAVY' ? CONFIG.HEAVY_PIERCE_COUNT : 0,
      heavyPermanent: false,   // HEAVY power-up makes next bricks plowed via flag set by game
      pierceCount: type === 'HEAVY' ? CONFIG.HEAVY_PIERCE_COUNT : 0,
      trail: [],
      age: 0,
    };
    list.push(b);
    return b;
  }

  function reset() { list.length = 0; }

  function launchAll() {
    for (const b of list) if (b.stuck) b.stuck = false;
  }

  function speedOf(b) { return Math.hypot(b.vx, b.vy); }
  function setSpeed(b, s) {
    const cur = speedOf(b) || 1;
    b.vx = b.vx / cur * s; b.vy = b.vy / cur * s;
  }

  function update(dt) {
    for (const b of list) {
      b.age += dt;
      if (b.fireTimer > 0) { b.fireTimer -= dt; if (b.fireTimer <= 0) b.type = 'NORMAL'; }
      if (!b.stuck) {
        // clamp speed
        let s = speedOf(b);
        if (s > CONFIG.BALL_MAX_SPEED) setSpeed(b, CONFIG.BALL_MAX_SPEED);
        else if (s < CONFIG.BALL_MIN_SPEED && s > 0) setSpeed(b, CONFIG.BALL_MIN_SPEED);
        b.x += b.vx * dt; b.y += b.vy * dt;
        // walls
        if (b.x < b.r) { b.x = b.r; b.vx = Math.abs(b.vx); }
        if (b.x > CONFIG.LOGICAL_W - b.r) { b.x = CONFIG.LOGICAL_W - b.r; b.vx = -Math.abs(b.vx); }
        if (b.y < b.r) { b.y = b.r; b.vy = Math.abs(b.vy); }
        // floor handled by game (life loss / shield)
      }
      // trail
      b.trail.push(b.x, b.y);
      const maxLen = CONFIG.TRAIL_MAX * 2;
      while (b.trail.length > maxLen) b.trail.splice(0, 2);
    }
  }

  // remove dead balls (below floor) — game decides lives
  function cullBelow() {
    for (let i = list.length - 1; i >= 0; i--)
      if (list[i].y > CONFIG.LOGICAL_H + 40) list.splice(i, 1);
  }

  function colorOf(type) {
    return { NORMAL: CONFIG.COLORS.ball, FIRE: CONFIG.COLORS.fire, HEAVY: CONFIG.COLORS.heavy,
      SPLITTER: CONFIG.COLORS.splitter, GHOST: CONFIG.COLORS.ghost }[type] || '#fff';
  }

  function draw(ctx, heat) {
    for (const b of list) {
      const col = colorOf(b.type);
      // trail
      const n = b.trail.length / 2;
      const trailN = Math.min(n, (b.type === 'FIRE' ? CONFIG.TRAIL_MAX : CONFIG.TRAIL_MAX * (0.5 + heat * 0.7)) | 0);
      for (let i = 0; i < trailN; i++) {
        const tx = b.trail[(n - trailN + i) * 2], ty = b.trail[(n - trailN + i) * 2 + 1];
        const a = (i / trailN) * 0.5;
        ctx.globalAlpha = a; ctx.fillStyle = col;
        const r = b.r * (i / trailN) * 0.9;
        ctx.beginPath(); ctx.arc(tx, ty, Math.max(0.5, r), 0, 7); ctx.fill();
      }
      ctx.globalAlpha = 1;
      // body with glow
      ctx.shadowColor = col; ctx.shadowBlur = b.type === 'NORMAL' ? 8 : 16;
      if (b.type === 'GHOST') ctx.globalAlpha = 0.6;
      ctx.fillStyle = col;
      ctx.beginPath(); ctx.arc(b.x, b.y, b.r, 0, 7); ctx.fill();
      if (b.type === 'FIRE') { // ember core
        ctx.fillStyle = '#fff3c4';
        ctx.beginPath(); ctx.arc(b.x, b.y, b.r * 0.5, 0, 7); ctx.fill();
      }
      if (b.type === 'HEAVY') { // ring
        ctx.strokeStyle = '#fff'; ctx.lineWidth = 2;
        ctx.beginPath(); ctx.arc(b.x, b.y, b.r + 3, 0, 7); ctx.stroke();
      }
      ctx.globalAlpha = 1; ctx.shadowBlur = 0;
    }
  }

  return { list, make, reset, update, draw, launchAll, speedOf, setSpeed, colorOf, cullBelow };
})();