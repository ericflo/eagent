// paddle.js — paddle with vertical band movement, punch mechanic, squash/stretch
'use strict';

const Paddle = (() => {
  const p = {
    x: CONFIG.LOGICAL_W / 2, y: CONFIG.LOGICAL_H - 150,
    w: CONFIG.PADDLE_W, h: CONFIG.PADDLE_H,
    vx: 0, vy: 0, prevY: 0,
    wideTimer: 0, magnetTimer: 0,
    squash: 0, glow: 0, // timers
  };

  function reset() {
    p.x = CONFIG.LOGICAL_W / 2; p.y = CONFIG.LOGICAL_H - 150;
    p.w = CONFIG.PADDLE_W; p.vx = 0; p.vy = 0;
    p.wideTimer = 0; p.magnetTimer = 0; p.squash = 0; p.glow = 0;
  }

  function update(dt, input) {
    p.prevY = p.y;
    // priority: keyboard/stick velocity when pressed > absolute pointer position
    const kvel = input.stickVX, kvelY = input.stickVY;
    const keysActive = Math.abs(kvel) > 1 || Math.abs(kvelY) > 1 || input.keyLeft || input.keyRight || input.keyUp || input.keyDown;
    if (input.pointerActive && !input.stick.drag && !keysActive) {
      // absolute target from pointer, but move toward it with a velocity (so vy for punch still works)
      const tx = Util.clamp(input.px, p.w / 2, CONFIG.LOGICAL_W - p.w / 2);
      const ty = Util.clamp(input.py, CONFIG.PADDLE_BAND_TOP, CONFIG.PADDLE_BAND_BOTTOM);
      const nx = tx - p.x, ny = ty - p.y;
      // critically-damped-ish follow: snap for x, fast follow for y
      p.vx = Util.clamp(nx / Math.max(dt, 1 / 240), -CONFIG.STICK_MAX_SPEED, CONFIG.STICK_MAX_SPEED);
      p.vy = Util.clamp(ny / Math.max(dt, 1 / 240), -CONFIG.STICK_MAX_SPEED, CONFIG.STICK_MAX_SPEED);
      const maxStepX = p.vx * dt, maxStepY = p.vy * dt;
      // avoid jitter: move at most to target
      p.x += Math.abs(maxStepX) > Math.abs(nx) ? nx : maxStepX;
      p.y += Math.abs(maxStepY) > Math.abs(ny) ? ny : maxStepY;
    } else {
      p.x += kvel * dt; p.y += kvelY * dt;
      p.vx = kvel; p.vy = kvelY;
    }
    p.x = Util.clamp(p.x, p.w / 2, CONFIG.LOGICAL_W - p.w / 2);
    p.y = Util.clamp(p.y, CONFIG.PADDLE_BAND_TOP, CONFIG.PADDLE_BAND_BOTTOM);
    // recompute actual velocity after clamps (for punch detection)
    p.vy = (p.y - p.prevY) / Math.max(dt, 1e-6);

    if (p.wideTimer > 0) p.wideTimer -= dt;
    if (p.magnetTimer > 0) p.magnetTimer -= dt;
    const targetW = CONFIG.PADDLE_W * (p.wideTimer > 0 ? CONFIG.PADDLE_WIDE_MULT : 1);
    p.w = Util.lerp(p.w, targetW, Math.min(1, 12 * dt));
    if (p.squash > 0) p.squash -= dt * 5;
    if (p.glow > 0) p.glow -= dt * 3;
  }

  // called when ball hits paddle; returns punch info
  function onBallHit() {
    p.squash = 1; p.glow = 1;
    const punching = p.vy < CONFIG.PUNCH_VY_THRESHOLD;
    return punching;
  }

  function draw(ctx) {
    const sq = Math.max(0, p.squash);
    const w = p.w * (1 + sq * 0.15), h = p.h * (1 - sq * 0.35);
    ctx.save();
    ctx.translate(p.x, p.y);
    // glow when powered
    const glow = Math.max(p.glow, p.wideTimer > 0 ? 0.6 : 0, p.magnetTimer > 0 ? 0.6 : 0);
    if (glow > 0) {
      ctx.shadowColor = p.magnetTimer > 0 ? CONFIG.COLORS.ghost : '#7dd3fc';
      ctx.shadowBlur = 18 * glow;
    }
    // body
    const grad = ctx.createLinearGradient(-w / 2, 0, w / 2, 0);
    grad.addColorStop(0, '#94a3b8'); grad.addColorStop(0.5, CONFIG.COLORS.paddle); grad.addColorStop(1, '#94a3b8');
    ctx.fillStyle = grad;
    roundRect(ctx, -w / 2, -h / 2, w, h, h / 2); ctx.fill();
    // punch accent stripe
    ctx.fillStyle = p.vy < -50 ? '#fb923c' : 'rgba(255,255,255,0.25)';
    roundRect(ctx, -w / 2 + 4, -h / 2 + 3, w - 8, 4, 2); ctx.fill();
    ctx.restore();
  }

  function roundRect(ctx, x, y, w, h, r) {
    r = Math.min(r, w / 2, h / 2);
    ctx.beginPath();
    ctx.moveTo(x + r, y);
    ctx.arcTo(x + w, y, x + w, y + h, r);
    ctx.arcTo(x + w, y + h, x, y + h, r);
    ctx.arcTo(x, y + h, x, y, r);
    ctx.arcTo(x, y, x + w, y, r);
    ctx.closePath();
  }

  return Object.assign(p, { reset, update, draw, onBallHit, roundRect });
})();