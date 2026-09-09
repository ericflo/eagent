// fx.js — game juice: particles, shards, rings, popups, shake, flashes, banners,
// ball trails. Pure data + update, plus draw(ctx) helpers used by render.js.
// Everything scales with the multiplier tier so a hot run *looks* hot.

import { TAU, clamp, lerp, easeOutCubic, easeOutBack, createRng, hsl } from './util.js';
import { FIELD } from './physics.js';

const MAX_PARTICLES = 640; // hard cap; when full the oldest slots are recycled round-robin
const MAX_BURST = 60; // no single emitter call spawns more than this

/** 72-step hue wheel (5° apart) so rainbow effects never build colour strings per frame. */
export const RAINBOW = Array.from({ length: 72 }, (_, i) => hsl(i * 5, 100, 65));
const CONFETTI_COLORS = Array.from({ length: 24 }, (_, i) => hsl(i * 15, 95, 62));

export function createFx() {
  const fx = {
    rng: createRng(1337),
    particles: [],
    recycle: 0, // next slot to overwrite once the particle cap is hit
    rings: [],
    popups: [],
    flashes: [], // brick outline flashes
    screenFlash: { color: '#ffffff', alpha: 0 },
    shake: { power: 0, x: 0, y: 0, t: 0 },
    banner: null,
    trails: new Map(), // ball.id → [{x,y,r}]
    motion: 1, // 1 normally, 0.35 under prefers-reduced-motion (scales shake/flash/zoom)
    time: 0,
  };
  watchReducedMotion(fx);
  return fx;
}

/** Respect the OS "reduce motion" setting: softer shake, dimmer flashes, no zoom. */
function watchReducedMotion(fx) {
  try {
    if (typeof matchMedia !== 'function') return;
    const mq = matchMedia('(prefers-reduced-motion: reduce)');
    const apply = () => (fx.motion = mq.matches ? 0.35 : 1);
    apply();
    if (mq.addEventListener) mq.addEventListener('change', apply);
  } catch {
    /* non-browser environment */
  }
}

// ---- emitters -------------------------------------------------------------

function push(fx, p) {
  const ps = fx.particles;
  if (ps.length >= MAX_PARTICLES) {
    ps[fx.recycle] = p;
    fx.recycle = (fx.recycle + 1) % MAX_PARTICLES;
    return;
  }
  ps.push(p);
}

export function sparks(fx, x, y, color, n, speed = 320, life = 0.5, size = 3) {
  const r = fx.rng;
  n = Math.min(n, MAX_BURST);
  for (let i = 0; i < n; i++) {
    const a = r() * TAU;
    const s = speed * (0.35 + r() * 0.85);
    push(fx, {
      kind: 'spark', x, y, vx: Math.cos(a) * s, vy: Math.sin(a) * s - 60, life: life * (0.5 + r() * 0.7),
      maxLife: life, size: size * (0.6 + r() * 0.8), color, gravity: 500, drag: 2.2,
    });
  }
}

/** Brick shards: tumbling rectangles with gravity. */
export function shards(fx, brick, color, n = 8, boost = 1) {
  const r = fx.rng;
  n = Math.min(n, 24);
  for (let i = 0; i < n; i++) {
    const px = brick.x + r() * brick.w;
    const py = brick.y + r() * brick.h;
    const cx = brick.x + brick.w / 2;
    const cy = brick.y + brick.h / 2;
    const ang = Math.atan2(py - cy, px - cx) + r.range(-0.6, 0.6);
    const s = (140 + r() * 260) * boost;
    push(fx, {
      kind: 'shard', x: px, y: py, vx: Math.cos(ang) * s, vy: Math.sin(ang) * s - 120 * boost,
      w: 6 + r() * 12, h: 4 + r() * 8, rot: r() * TAU, vr: r.range(-12, 12),
      life: 0.9 + r() * 0.5, maxLife: 1.4, color, gravity: 1100, drag: 0.6,
    });
  }
}

export function confetti(fx, x, y, n = 30, spread = 520) {
  const r = fx.rng;
  n = Math.min(n, MAX_BURST);
  for (let i = 0; i < n; i++) {
    const a = r() * TAU;
    const s = spread * (0.3 + r() * 0.8);
    push(fx, {
      kind: 'confetti', x, y, vx: Math.cos(a) * s, vy: Math.sin(a) * s - 200, w: 5 + r() * 6, h: 3 + r() * 4,
      rot: r() * TAU, vr: r.range(-14, 14), life: 1.4 + r() * 0.8, maxLife: 2.2, color: r.pick(CONFETTI_COLORS),
      gravity: 420, drag: 1.6, flutter: r() * TAU,
    });
  }
}

export function ember(fx, x, y, color = '#ff7a1a') {
  const r = fx.rng;
  push(fx, {
    kind: 'spark', x: x + r.range(-4, 4), y: y + r.range(-4, 4), vx: r.range(-40, 40), vy: r.range(-90, -20),
    life: 0.35 + r() * 0.3, maxLife: 0.6, size: 2 + r() * 3, color, gravity: -200, drag: 1.5,
  });
}

export function ring(fx, x, y, color, maxR = 120, life = 0.45, width = 6) {
  fx.rings.push({ x, y, r: 6, maxR, life, maxLife: life, color, width });
}

/**
 * Popups rise ~80 units over their life. Anything spawned in the attic (ON TOP!,
 * HALO, CASCADE!…) is pushed down to this line first so it never floats over the
 * score / multiplier HUD drawn across the top of the field.
 */
export const POPUP_MIN_Y = 190;

export function popup(fx, x, y, text, color = '#fff', scale = 1) {
  fx.popups.push({ x, y: Math.max(y, POPUP_MIN_Y), text, color, scale, life: 0.9, maxLife: 0.9, vy: -90 });
}

export function outlineFlash(fx, brick) {
  fx.flashes.push({ x: brick.x, y: brick.y, w: brick.w, h: brick.h, life: 0.14, maxLife: 0.14 });
}

export function screenFlash(fx, color, alpha) {
  fx.screenFlash.color = color;
  fx.screenFlash.alpha = Math.max(fx.screenFlash.alpha, alpha * (0.4 + 0.6 * fx.motion));
}

export function shake(fx, power) {
  fx.shake.power = Math.min(40, fx.shake.power + power * fx.motion);
}

export function banner(fx, text, sub = '', color = '#fff', life = 1.6) {
  fx.banner = { text, sub, color, life, maxLife: life };
}

// ---- composite effects (called from main.js on game events) ----------------

export function brickBreakFx(fx, brick, { tier = 0, fromTop = false, points = 0 } = {}) {
  const k = 1 + tier * 0.35;
  shards(fx, brick, brick.color, Math.round(6 + tier * 1.5), 1 + tier * 0.12);
  sparks(fx, brick.x + brick.w / 2, brick.y + brick.h / 2, brick.color, Math.round(8 * k), 300 + tier * 40, 0.45, 3);
  if (tier >= 2) sparks(fx, brick.x + brick.w / 2, brick.y + brick.h / 2, '#ffffff', 4 + tier, 200, 0.3, 2);
  outlineFlash(fx, brick);
  if (fromTop) ring(fx, brick.x + brick.w / 2, brick.y + brick.h / 2, brick.color, 90 + tier * 12, 0.4, 4);
  if (points) popup(fx, brick.x + brick.w / 2, brick.y, `+${points}`, fromTop ? '#fff7b0' : '#ffffff', fromTop ? 1.25 : 1);
}

export function denyFx(fx, x, y, kind) {
  const colors = { angle: '#bfefff', speed: '#e6e9f0', top: '#d9b3ff', steel: '#c8ccd6', laser: '#ffb0b0' };
  sparks(fx, x, y, colors[kind] || '#fff', kind === 'steel' ? 3 : 6, 180, 0.3, 2);
}

export function bombFx(fx, x, y, tier, count) {
  ring(fx, x, y, '#ffb347', 260 + count * 12, 0.6, 10);
  ring(fx, x, y, '#ffffff', 140, 0.3, 4);
  sparks(fx, x, y, '#ff5d5d', 26 + tier * 4, 620, 0.7, 4);
  sparks(fx, x, y, '#ffe14d', 18 + tier * 3, 420, 0.6, 3);
  shake(fx, 10 + tier * 1.5 + count);
  screenFlash(fx, '#ffd9a0', 0.28);
}

export function cascadeFx(fx, x, y, tier) {
  ring(fx, x, y, '#ffffff', 220, 0.5, 8);
  ring(fx, x, y, hsl(40 + tier * 30, 100, 65), 320, 0.7, 5);
  sparks(fx, x, y, '#ffffff', 20 + tier * 3, 500, 0.6, 3);
  shake(fx, 8 + tier);
  screenFlash(fx, '#ffffff', 0.18);
}

export function paddleHitFx(fx, x, y, powerHit, strength, tier) {
  sparks(fx, x, y, powerHit ? '#fff2a8' : '#9fd4ff', powerHit ? 14 + tier * 2 : 5, powerHit ? 420 : 180, 0.4, 2.5);
  if (powerHit) {
    ring(fx, x, y, '#fff2a8', 70 + strength * 60, 0.35, 5);
    shake(fx, 3 + strength * 5);
  }
}

export function powerupFx(fx, x, y, color, name) {
  ring(fx, x, y, color, 130, 0.5, 6);
  confetti(fx, x, y, 22, 420);
  popup(fx, x, y - 30, name, color, 1.3);
}

export function ballLostFx(fx, x) {
  sparks(fx, x, FIELD.h - 10, '#ff5d5d', 24, 400, 0.6, 3);
  screenFlash(fx, '#ff3355', 0.32);
  shake(fx, 12);
}

export function levelClearFx(fx) {
  for (let i = 0; i < 5; i++) confetti(fx, 120 + i * 120, 300 + (i % 2) * 120, 26, 560);
  screenFlash(fx, '#ffffff', 0.4);
  shake(fx, 6);
}

// ---- trails ---------------------------------------------------------------

export function pushTrail(fx, ball, tier, speedNorm) {
  let t = fx.trails.get(ball.id);
  if (!t) {
    t = [];
    fx.trails.set(ball.id, t);
  }
  t.push({ x: ball.x, y: ball.y, r: ball.r });
  const maxLen = Math.round(6 + speedNorm * 14 + tier * 3);
  while (t.length > maxLen) t.shift();
}

export function pruneTrails(fx, balls) {
  const ids = new Set(balls.map((b) => b.id));
  for (const id of fx.trails.keys()) if (!ids.has(id)) fx.trails.delete(id);
}

// ---- update ---------------------------------------------------------------

export function updateFx(fx, dt) {
  fx.time += dt;
  const ps = fx.particles;
  for (let i = ps.length - 1; i >= 0; i--) {
    const p = ps[i];
    p.life -= dt;
    if (p.life <= 0) {
      ps[i] = ps[ps.length - 1];
      ps.pop();
      continue;
    }
    p.vy += (p.gravity || 0) * dt;
    const drag = Math.exp(-(p.drag || 0) * dt);
    p.vx *= drag;
    p.vy *= drag;
    if (p.flutter !== undefined) p.vx += Math.sin(fx.time * 9 + p.flutter) * 60 * dt;
    p.x += p.vx * dt;
    p.y += p.vy * dt;
    if (p.rot !== undefined) p.rot += p.vr * dt;
  }
  for (let i = fx.rings.length - 1; i >= 0; i--) {
    const r = fx.rings[i];
    r.life -= dt;
    if (r.life <= 0) fx.rings.splice(i, 1);
  }
  for (let i = fx.popups.length - 1; i >= 0; i--) {
    const p = fx.popups[i];
    p.life -= dt;
    p.y += p.vy * dt;
    if (p.life <= 0) fx.popups.splice(i, 1);
  }
  for (let i = fx.flashes.length - 1; i >= 0; i--) {
    fx.flashes[i].life -= dt;
    if (fx.flashes[i].life <= 0) fx.flashes.splice(i, 1);
  }
  fx.screenFlash.alpha = Math.max(0, fx.screenFlash.alpha - dt * 2.4);
  // shake: decaying random offset
  const s = fx.shake;
  s.power *= Math.exp(-dt * 7);
  if (s.power < 0.15) s.power = 0;
  s.t += dt * 60;
  s.x = (fx.rng() * 2 - 1) * s.power;
  s.y = (fx.rng() * 2 - 1) * s.power;
  if (fx.banner) {
    fx.banner.life -= dt;
    if (fx.banner.life <= 0) fx.banner = null;
  }
}

// ---- drawing (field space) -------------------------------------------------

// Particles are batched by colour and quantised alpha so ~800 particles cost a few
// dozen fill calls instead of ~800 (each fillStyle/globalAlpha change breaks batching
// in GPU-backed canvases). Batch arrays persist between frames — no per-frame allocs.
const ALPHA_LEVELS = 6;
const batches = new Map(); // colour → ALPHA_LEVELS arrays of particles

function batchFor(color) {
  let b = batches.get(color);
  if (!b) {
    b = [];
    for (let i = 0; i < ALPHA_LEVELS; i++) b.push([]);
    batches.set(color, b);
  }
  return b;
}

function addParticlePath(ctx, p) {
  if (p.kind === 'spark') {
    const rad = p.size * (0.4 + (0.6 * p.life) / p.maxLife);
    ctx.moveTo(p.x + rad, p.y);
    ctx.arc(p.x, p.y, rad, 0, TAU);
    return;
  }
  // rotated rectangle as an explicit quad (same winding as arc so overlaps stay filled)
  const c = Math.cos(p.rot);
  const s = Math.sin(p.rot);
  const hw = p.w / 2;
  const hh = p.h / 2;
  const ax = hw * c;
  const ay = hw * s;
  const bx = hh * s;
  const by = hh * c;
  ctx.moveTo(p.x - ax + bx, p.y - ay - by);
  ctx.lineTo(p.x + ax + bx, p.y + ay - by);
  ctx.lineTo(p.x + ax - bx, p.y + ay + by);
  ctx.lineTo(p.x - ax - bx, p.y - ay + by);
  ctx.closePath();
}

export function drawParticles(ctx, fx) {
  const ps = fx.particles;
  if (ps.length === 0) return;
  for (let i = 0; i < ps.length; i++) {
    const p = ps[i];
    const a = p.life / p.maxLife;
    const lvl = a >= 1 ? ALPHA_LEVELS - 1 : a <= 0 ? 0 : (a * ALPHA_LEVELS) | 0;
    batchFor(p.color)[lvl].push(p);
  }
  for (const [color, levels] of batches) {
    let used = false;
    for (let l = 0; l < ALPHA_LEVELS; l++) {
      const arr = levels[l];
      if (arr.length === 0) continue;
      used = true;
      ctx.globalAlpha = (l + 0.5) / ALPHA_LEVELS;
      ctx.fillStyle = color;
      ctx.beginPath();
      for (let i = 0; i < arr.length; i++) addParticlePath(ctx, arr[i]);
      ctx.fill();
      arr.length = 0;
    }
    if (!used) batches.delete(color); // forget colours no longer in play
  }
  ctx.globalAlpha = 1;
}

export function drawRings(ctx, fx) {
  for (const r of fx.rings) {
    const t = 1 - r.life / r.maxLife;
    const rad = lerp(r.r, r.maxR, easeOutCubic(t));
    ctx.globalAlpha = (1 - t) * 0.9;
    ctx.lineWidth = r.width * (1 - t * 0.7) + 0.5;
    ctx.strokeStyle = r.color;
    ctx.beginPath();
    ctx.arc(r.x, r.y, rad, 0, TAU);
    ctx.stroke();
  }
  ctx.globalAlpha = 1;
}

export function drawFlashes(ctx, fx) {
  for (const f of fx.flashes) {
    ctx.globalAlpha = f.life / f.maxLife;
    ctx.strokeStyle = '#ffffff';
    ctx.lineWidth = 3;
    ctx.strokeRect(f.x - 2, f.y - 2, f.w + 4, f.h + 4);
  }
  ctx.globalAlpha = 1;
}

export function drawPopups(ctx, fx) {
  ctx.textAlign = 'center';
  ctx.textBaseline = 'middle';
  for (const p of fx.popups) {
    const t = 1 - p.life / p.maxLife;
    const s = p.scale * (0.6 + 0.4 * easeOutBack(clamp(t * 3, 0, 1)));
    ctx.globalAlpha = clamp(1 - Math.max(0, t - 0.5) * 2, 0, 1);
    ctx.font = `700 ${Math.round(26 * s)}px "Segoe UI", system-ui, sans-serif`;
    ctx.lineWidth = 4;
    ctx.strokeStyle = 'rgba(0,0,0,0.55)';
    ctx.strokeText(p.text, p.x, p.y);
    ctx.fillStyle = p.color;
    ctx.fillText(p.text, p.x, p.y);
  }
  ctx.globalAlpha = 1;
}

/** Ball trails: glow grows with speed & tier; tier ≥ 5 cycles the rainbow. */
export function drawTrails(ctx, fx, balls, tier, time) {
  const rainbow = tier >= 5;
  const hueBase = (time * 48) | 0;
  const alphaK = 0.35 + tier * 0.06;
  const sizeK = 0.9 + tier * 0.05;
  for (const b of balls) {
    const t = fx.trails.get(b.id);
    if (!t || t.length < 2 || b.stuck) continue;
    const n = t.length;
    const solid = b.power === 'fire' ? '#ff7a1a' : b.onTop ? '#fff2a8' : '#8fd6ff';
    if (!rainbow) ctx.fillStyle = solid;
    for (let i = 0; i < n; i++) {
      const p = t[i];
      const f = (i + 1) / n;
      if (rainbow) ctx.fillStyle = RAINBOW[(hueBase + i * 3) % RAINBOW.length];
      ctx.globalAlpha = f * f * alphaK;
      ctx.beginPath();
      ctx.arc(p.x, p.y, p.r * f * sizeK, 0, TAU);
      ctx.fill();
    }
  }
  ctx.globalAlpha = 1;
}
