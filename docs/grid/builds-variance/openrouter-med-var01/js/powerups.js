// powerups.js — falling capsules + effect application
'use strict';

const Powerups = (() => {
  const TYPES = ['MULTI', 'WIDE', 'FIRE', 'HEAVY', 'SPLIT', 'MAGNET', 'SHIELD', 'SLOW'];
  const WEIGHTS = { MULTI: 22, WIDE: 18, FIRE: 14, HEAVY: 10, SPLIT: 12, MAGNET: 10, SHIELD: 8, SLOW: 8 };
  const list = [];

  const ICONS = { MULTI: 'M', WIDE: 'W', FIRE: 'F', HEAVY: 'H', SPLIT: 'S', MAGNET: 'U', SHIELD: 'O', SLOW: 'L' };
  const COLORS = { MULTI: '#5eead4', WIDE: '#38bdf8', FIRE: '#fb923c', HEAVY: '#a3a3a3',
    SPLIT: '#f472b6', MAGNET: '#67e8f9', SHIELD: '#a78bfa', SLOW: '#facc15' };

  function pickType(rng) {
    let total = 0; for (const t of TYPES) total += WEIGHTS[t];
    let r = rng() * total;
    for (const t of TYPES) { r -= WEIGHTS[t]; if (r <= 0) return t; }
    return 'MULTI';
  }

  function spawn(x, y, rng) {
    list.push({ x, y, type: pickType(rng || Math.random), vy: CONFIG.CAPSULE_FALL_SPEED, t: 0 });
  }

  function update(dt, game) {
    for (let i = list.length - 1; i >= 0; i--) {
      const c = list[i];
      c.t += dt; c.y += c.vy * dt;
      // catch vs paddle
      if (c.y > Paddle.y - Paddle.h && c.y < Paddle.y + Paddle.h + 14 &&
          Math.abs(c.x - Paddle.x) < Paddle.w / 2 + 16) {
        apply(c.type, game);
        AudioSys.sfx.capsule();
        Particles.confetti(c.x, c.y, 18);
        Particles.ring(c.x, c.y, { color: COLORS[c.type], vr: 400 });
        Particles.text(c.x, c.y - 20, c.type + '!', { color: COLORS[c.type], size: 24 });
        list.splice(i, 1); continue;
      }
      if (c.y > CONFIG.LOGICAL_H + 30) list.splice(i, 1);
    }
  }

  function apply(type, game) {
    switch (type) {
      case 'MULTI': {
        const src = Balls.list.filter(b => !b.stuck);
        const base = src[0] || Balls.list[0];
        if (base) {
          for (let i = 0; i < 2; i++) {
            if (Balls.list.length >= CONFIG.SPLIT_CAP) break;
            const a = Math.atan2(base.vy, base.vx) + (i === 0 ? 0.5 : -0.5);
            Balls.make(base.x, base.y, a, Balls.speedOf(base));
          }
        }
        break;
      }
      case 'WIDE': Paddle.wideTimer = CONFIG.PADDLE_WIDE_TIME; break;
      case 'FIRE':
        for (const b of Balls.list) { b.type = 'FIRE'; b.fireTimer = CONFIG.FIRE_DURATION; }
        break;
      case 'HEAVY':
        for (const b of Balls.list) { b.type = 'HEAVY'; b.pierceCount = CONFIG.HEAVY_PIERCE_COUNT; }
        break;
      case 'SPLIT':
        for (const b of Balls.list) if (b.type === 'NORMAL') b.type = 'SPLITTER';
        game.splitterTimer = CONFIG.SPLIT_TIME;
        break;
      case 'MAGNET': Paddle.magnetTimer = CONFIG.MAGNET_DURATION; break;
      case 'SHIELD': game.shield = true; break;
      case 'SLOW': game.slowTimer = CONFIG.SLOW_TIME; break;
    }
  }

  function draw(ctx) {
    for (const c of list) {
      const col = COLORS[c.type];
      const bob = Math.sin(c.t * 6) * 2;
      ctx.save();
      ctx.translate(c.x, c.y + bob);
      ctx.shadowColor = col; ctx.shadowBlur = 14;
      // capsule body
      const g = ctx.createLinearGradient(0, -12, 0, 12);
      g.addColorStop(0, '#ffffff'); g.addColorStop(0.5, col); g.addColorStop(1, '#00000055');
      ctx.fillStyle = g;
      ctx.beginPath();
      ctx.roundRect ? ctx.roundRect(-20, -12, 40, 24, 12) : ctx.rect(-20, -12, 40, 24);
      ctx.fill();
      ctx.shadowBlur = 0;
      ctx.fillStyle = '#0b0b14'; ctx.font = '800 15px system-ui';
      ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
      ctx.fillText(ICONS[c.type], 0, 1);
      ctx.restore();
    }
  }

  function reset() { list.length = 0; }

  return { list, spawn, update, draw, reset, apply, TYPES, COLORS };
})();