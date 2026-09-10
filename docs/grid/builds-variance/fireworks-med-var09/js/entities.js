/* entities.js — paddle, ball (with special types), powerup capsule factories.
 * Plain data + update functions; rendering lives in main.js/ui.js. */
(function (root) {
  'use strict';
  var BO = root.BO = root.BO || {};
  var P = BO;

  /* ---------- Paddle ---------- */
  BO.makePaddle = function (field) {
    return {
      x: field.w / 2, y: field.h - 70,
      w: 110, h: 14, baseW: 110,
      vx: 0, vy: 0,
      // vertical band: bottom 25% of field
      bandTop: field.h * 0.75, bandBottom: field.h - 20,
      squash: 0, // 0..1 squash-and-stretch timer
      targetX: field.w / 2, targetY: field.h - 70,
      kbSpeed: 620,
      wideT: 0, magnetT: 0,
      sticky: null // caught ball ref (magnet powerup)
    };
  };

  BO.updatePaddle = function (p, input, dt, field) {
    var px = p.x, py = p.y;
    // Keyboard
    var kx = 0, ky = 0;
    if (input.keys.left) kx -= 1;
    if (input.keys.right) kx += 1;
    if (input.keys.up) ky -= 1;
    if (input.keys.down) ky += 1;
    if (kx || ky) {
      p.targetX += kx * p.kbSpeed * dt;
      p.targetY += ky * p.kbSpeed * dt * 0.7;
    }
    // Mouse/touch set target directly (input.js, CSS px relative to canvas);
    // convert through the view transform (set by main.js) into field coords.
    if (input.pointer.active) {
      var v = input.view;
      if (v) {
        p.targetX = (input.pointer.x - v.ox) / v.scale;
        p.targetY = (input.pointer.y - v.oy) / v.scale;
      } else {
        p.targetX = input.pointer.x;
        p.targetY = input.pointer.y;
      }
    }
    p.targetX = P.clamp(p.targetX, p.w / 2 + field.x, field.x + field.w - p.w / 2);
    p.targetY = P.clamp(p.targetY, p.bandTop, p.bandBottom);

    var nx = P.lerp(p.x, p.targetX, Math.min(1, dt * 22));
    var ny = P.lerp(p.y, p.targetY, Math.min(1, dt * 22));
    p.vx = (nx - px) / dt;
    p.vy = (ny - py) / dt;
    p.x = nx; p.y = ny;
    if (p.squash > 0) p.squash = Math.max(0, p.squash - dt * 4);
    if (p.wideT > 0) {
      p.wideT -= dt;
      p.w = p.baseW * 1.55;
    } else p.w = p.baseW;
    if (p.magnetT > 0) p.magnetT -= dt;
  };

  /* ---------- Balls ---------- */
  var BALL_TYPES = {
    normal: { color: '#ffffff', glow: 'rgba(255,255,255,0.8)', r: 7, speedK: 1, trail: 1 },
    fire:   { color: '#ff6a3d', glow: 'rgba(255,106,61,0.9)', r: 7, speedK: 1.12, trail: 1.6, pierce: true },
    heavy:  { color: '#9ab0c8', glow: 'rgba(154,176,200,0.9)', r: 10, speedK: 0.92, trail: 0.7, heavy: true },
    ghost:  { color: '#7dfcff', glow: 'rgba(125,252,255,0.85)', r: 7, speedK: 1, trail: 1.1, ghost: true },
    rubber: { color: '#ff4dd2', glow: 'rgba(255,77,210,0.9)', r: 6, speedK: 1.28, trail: 2.0, bouncy: true }
  };
  BO.BALL_TYPES = BALL_TYPES;

  BO.makeBall = function (x, y, type, baseSpeed) {
    var t = BALL_TYPES[type || 'normal'];
    return {
      x: x, y: y, vx: 0, vy: 0, r: t.r,
      type: type || 'normal',
      baseSpeed: baseSpeed * t.speedK,
      stuck: true, // stuck to paddle until launch
      ghostT: 0, ghostPhase: 0, ghostReady: true,
      trail: [], alive: true,
      px: x, py: y
    };
  };

  BO.setBallType = function (ball, type) {
    var t = BALL_TYPES[type];
    ball.type = type;
    ball.r = t.r;
    ball.baseSpeed = ball.baseSpeed / BALL_TYPES[ball._prevType || 'normal'].speedK * t.speedK;
    ball._prevType = type;
  };

  /* ---------- Powerup capsules ---------- */
  var POWERS = {
    fire:   { name: 'FIREBALL',  color: '#ff6a3d', kind: 'ball', duration: 9 },
    heavy:  { name: 'IRON BALL', color: '#9ab0c8', kind: 'ball', duration: 8 },
    multi:  { name: 'MULTIBALL', color: '#ffd24d', kind: 'instant' },
    ghost:  { name: 'GHOST',     color: '#7dfcff', kind: 'ball', duration: 8 },
    rubber: { name: 'RUBBER',    color: '#ff4dd2', kind: 'ball', duration: 8 },
    wide:   { name: 'WIDE',      color: '#4dffa6', kind: 'timed', duration: 12 },
    slow:   { name: 'SLOW-MO',   color: '#8f9bff', kind: 'timed', duration: 4 },
    magnet: { name: 'MAGNET',    color: '#c46bff', kind: 'timed', duration: 10 }
  };
  BO.POWERS = POWERS;

  BO.makeDrop = function (x, y, id) {
    return { x: x, y: y, vy: 130, id: id, t: 0, alive: true, w: 34, h: 16 };
  };
})(typeof window !== 'undefined' ? window : globalThis);
