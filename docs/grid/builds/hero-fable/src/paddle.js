// paddle.js — the 2D paddle model. Pure data + update; input decides the command.

import { clamp } from './util.js';
import { FIELD } from './physics.js';
import { POWERS } from './balls.js';

export const PADDLE = {
  w: 150,
  h: 22,
  yMin: 1040, // vertical band the paddle may move in
  yMax: 1180,
  restY: 1150,
  wideScale: 1.5,
  keySpeedX: 1000, // keyboard/stick full-deflection speeds (u/s)
  keySpeedY: 700,
  laserInterval: 0.5,
};

export function createPaddle() {
  return {
    x: FIELD.w / 2,
    y: PADDLE.restY,
    w: PADDLE.w,
    h: PADDLE.h,
    baseW: PADDLE.w,
    vx: 0,
    vy: 0,
    // paddle-side power timers (seconds remaining)
    wideTime: 0,
    magnetTime: 0,
    laserTime: 0,
    laserCooldown: 0,
    // render-only feel
    recoil: 0,
    squash: 0,
    glow: 0,
  };
}

/** Axis-aligned box of the paddle (x,y are the paddle centre). */
export const paddleBox = (p) => ({ x: p.x - p.w / 2, y: p.y - p.h / 2, w: p.w, h: p.h });

/**
 * Move the paddle according to an input command and track its velocity.
 *   cmd = { mode:'absolute', x, y }         mouse / drag-touch
 *       | { mode:'velocity', vx, vy }       keyboard / thumbstick (units per second)
 *       | null                              no input this frame
 * Velocity is low-pass filtered so one jittery mouse sample can't produce a
 * ridiculous power hit, but a real flick upward still registers.
 */
export function updatePaddle(p, cmd, dt) {
  if (dt <= 0) return;
  const prevX = p.x;
  const prevY = p.y;
  if (cmd) {
    if (cmd.mode === 'absolute') {
      p.x = cmd.x;
      p.y = cmd.y;
    } else if (cmd.mode === 'velocity') {
      p.x += cmd.vx * dt;
      p.y += cmd.vy * dt;
    }
  }
  p.x = clamp(p.x, p.w / 2, FIELD.w - p.w / 2);
  p.y = clamp(p.y, PADDLE.yMin, PADDLE.yMax);

  const ivx = (p.x - prevX) / dt;
  const ivy = (p.y - prevY) / dt;
  const k = 1 - Math.exp(-dt * 28);
  p.vx += (ivx - p.vx) * k;
  p.vy += (ivy - p.vy) * k;

  // timers & animated width
  p.wideTime = Math.max(0, p.wideTime - dt);
  p.magnetTime = Math.max(0, p.magnetTime - dt);
  p.laserTime = Math.max(0, p.laserTime - dt);
  p.laserCooldown = Math.max(0, p.laserCooldown - dt);
  const targetW = p.wideTime > 0 ? p.baseW * PADDLE.wideScale : p.baseW;
  p.w += (targetW - p.w) * (1 - Math.exp(-dt * 12));

  p.recoil *= Math.exp(-dt * 9);
  p.squash *= Math.exp(-dt * 8);
  p.glow *= Math.exp(-dt * 4);
}

/** Apply a paddle-targeted power. Returns true if it applied. */
export function applyPaddlePower(p, kind) {
  const def = POWERS[kind];
  if (!def || def.target !== 'paddle') return false;
  if (kind === 'wide') p.wideTime = def.duration;
  if (kind === 'magnet') p.magnetTime = def.duration;
  if (kind === 'laser') p.laserTime = def.duration;
  p.glow = 1;
  return true;
}

/** Which paddle powers are active (for HUD). */
export function activePaddlePowers(p) {
  const out = [];
  if (p.wideTime > 0) out.push({ kind: 'wide', time: p.wideTime });
  if (p.magnetTime > 0) out.push({ kind: 'magnet', time: p.magnetTime });
  if (p.laserTime > 0) out.push({ kind: 'laser', time: p.laserTime });
  return out;
}
