// render.js — all canvas drawing: background, bricks, balls, paddle, HUD, tiers.
//
// The 720×1280 logical field is scaled into the viewport at device pixel ratio; the
// background (gradient, starburst, stars, grid, vignette) covers the WHOLE canvas so
// the letterbox reads as part of the scene, while the field itself keeps a crisp
// border. Hot paths avoid per-frame gradients/shadows: bricks, text glows and radial
// glows are pre-rendered once into offscreen sprites at device resolution.

import { TAU, clamp, lerp, hsl, mixHex, rgba, formatScore, createRng, easeOutBack, easeOutCubic } from './util.js';
import { FIELD } from './physics.js';
import { BRICK_TYPES, ghostCycle } from './bricks.js';
import { POWERS } from './balls.js';
import { PADDLE, activePaddlePowers } from './paddle.js';
import { TIER_NAMES } from './game.js';
import { LEVELS, loopSpeedScale } from './levels.js';
import { drawParticles, drawRings, drawFlashes, drawPopups, drawTrails, RAINBOW } from './fx.js';
import { STICK } from './input.js';

/** One palette per multiplier tier (Warm → TRANSCENDENT). Backgrounds get richer as you climb. */
// `accent` is used for text/paddle; `glow` tints vignette, frame, grid and attic band
// (TRANSCENDENT's white accent would wash the whole screen grey, so its glow is magenta).
export const PALETTES = [
  { bg1: '#0a0f1f', bg2: '#131a33', accent: '#3fb5ff', glow: '#3fb5ff', hue: 205 }, // Warm
  { bg1: '#140f24', bg2: '#2a1a44', accent: '#ff8a4d', glow: '#ff8a4d', hue: 22 }, // Hot
  { bg1: '#1c0d22', bg2: '#47163f', accent: '#ff4d7d', glow: '#ff4d7d', hue: 345 }, // Blazing
  { bg1: '#0c1a2c', bg2: '#1a3a6b', accent: '#c084fc', glow: '#c084fc', hue: 272 }, // Plasma
  { bg1: '#0d1b14', bg2: '#1b4d36', accent: '#5dff9f', glow: '#5dff9f', hue: 145 }, // Nova
  { bg1: '#1f1406', bg2: '#523712', accent: '#ffe14d', glow: '#ffe14d', hue: 48 }, // Supernova
  { bg1: '#1a0820', bg2: '#421556', accent: '#ffffff', glow: '#ff5cf0', hue: 300 }, // TRANSCENDENT
];

const FONT = 'system-ui, -apple-system, "Segoe UI", Roboto, sans-serif';
const font = (weight, px) => `${weight} ${px}px ${FONT}`;
const TIER_LABELS = TIER_NAMES.map((n) => n.toUpperCase());
const SHAKE_MARGIN = 48; // logical units of slack so a shaken background never shows the clear colour
const NO_DASH = [];
const BAND_DASH = [6, 10];
const ATTIC_DASH = [10, 8];
// Star twinkle alphas, quantised so we never build colour strings per star per frame.
const STAR_ALPHAS = Array.from({ length: 8 }, (_, i) => `rgba(255,255,255,${(0.12 + (0.7 * i) / 7).toFixed(3)})`);

export function createRenderer(canvas) {
  const ctx = canvas.getContext('2d', { alpha: false });
  const rng = createRng(99);
  // Stars live in normalised viewport space so they always cover the whole canvas.
  const stars = Array.from({ length: 170 }, () => ({ u: rng(), v: rng(), z: 0.3 + rng() * 0.7, tw: rng() * TAU }));
  const r = {
    canvas, ctx, cssW: 0, cssH: 0, dpr: 1, scale: 1, ox: 0, oy: 0, k: 1,
    view: { x: 0, y: 0, w: FIELD.w, h: FIELD.h }, // visible logical rect (includes letterbox)
    safe: { top: 0, right: 0, bottom: 0, left: 0 }, // CSS px insets (iOS notch / home bar)
    hudTop: 0, hudBottom: 0, // logical offsets keeping the HUD out of unsafe areas
    wide: false, // wide viewport → HUD lives in the letterbox margins
    hs: 1, // wide-mode text scale (the field is small on landscape phones)
    stars, rayAngle: 0, tierSmooth: 0, multPulse: 0, time: 0,
    pal: null, palKey: -1,
    sprites: new Map(), // brick sprites: type|colour|w|h → canvas
    glows: new Map(), // glowing text sprites (LRU)
    blobs: new Map(), // radial glow sprites by colour
    grads: { bg: null, bgKey: '', vig: null, vigKey: '', vigHole: 0, attic: null, atticKey: '' },
    strings: { score: -1, scoreStr: '0', best: -1, bestStr: '', level: null, levelStr: '', loop: -1, loopStr: 'LIVES' },
  };

  function resize(cssW, cssH, dpr) {
    r.cssW = cssW;
    r.cssH = cssH;
    r.dpr = dpr;
    canvas.width = Math.max(1, Math.round(cssW * dpr));
    canvas.height = Math.max(1, Math.round(cssH * dpr));
    canvas.style.width = `${cssW}px`;
    canvas.style.height = `${cssH}px`;
    r.scale = Math.min(cssW / FIELD.w, cssH / FIELD.h);
    r.ox = (cssW - FIELD.w * r.scale) / 2;
    r.oy = (cssH - FIELD.h * r.scale) / 2;
    r.k = r.dpr * r.scale; // device pixels per logical unit
    r.view.x = -r.ox / r.scale;
    r.view.y = -r.oy / r.scale;
    r.view.w = cssW / r.scale;
    r.view.h = cssH / r.scale;
    readSafeArea(r);
    r.hudTop = Math.max(0, (r.safe.top - r.oy) / r.scale);
    r.hudBottom = Math.max(0, (r.safe.bottom - (cssH - r.oy - FIELD.h * r.scale)) / r.scale);
    r.wide = cssW / cssH >= 1.05; // HUD moves into the side margins (keep in sync with the CSS aspect query)
    r.hs = r.wide ? clamp(-r.view.x / 420, 1.15, 2.4) : 1; // wide-mode HUD/banner scale-up
    // Sprites are baked at device resolution → rebuild them after any scale change.
    r.sprites.clear();
    r.glows.clear();
    r.blobs.clear();
    r.grads.bgKey = r.grads.vigKey = r.grads.atticKey = '';
    if (typeof document !== 'undefined') {
      document.documentElement.style.setProperty('--hud-shift', `${(r.hudTop * r.scale).toFixed(1)}px`);
    }
  }

  const toLogical = (clientX, clientY) => ({ x: (clientX - r.ox) / r.scale, y: (clientY - r.oy) / r.scale });

  function draw(game, fx, input, dt, beatPhase) {
    r.time += dt;
    r.tierSmooth += (game.tier - r.tierSmooth) * (1 - Math.exp(-dt * 4));
    if (Math.abs(r.tierSmooth - game.tier) < 0.004) r.tierSmooth = game.tier;
    r.multPulse *= Math.exp(-dt * 6);
    const motion = fx.motion === undefined ? 1 : fx.motion; // prefers-reduced-motion → 0.35
    r.rayAngle += dt * (0.08 + game.overtop * 0.5 + game.tier * 0.05) * (0.4 + 0.6 * motion);
    const pal = palette(r);
    const k = r.k;

    // Camera: the whole canvas shakes so the letterbox is part of the scene, and
    // while a ball is on top the BACKGROUND pushes in ~3 %. The field itself never
    // zooms: it always fills at least one axis of the viewport, so any field zoom
    // would crop a wall and cut the ball in half every time it bounced off one.
    ctx.setTransform(k, 0, 0, k, (r.ox + fx.shake.x * r.scale) * r.dpr, (r.oy + fx.shake.y * r.scale) * r.dpr);
    const zoom = motion < 1 ? 1 : 1 + game.overtop * 0.03; // no camera zoom under reduced motion
    ctx.save();
    zoomField(ctx, zoom);
    drawBackground(ctx, r, pal, game, beatPhase, dt);
    ctx.restore();
    drawFrame(ctx, r, pal, game);

    // Field layer: clipped to the play area.
    ctx.save();
    ctx.beginPath();
    ctx.rect(0, 0, FIELD.w, FIELD.h);
    ctx.clip();
    drawAttic(ctx, r, game, pal);
    drawBandGuides(ctx, pal);
    drawBricks(ctx, r, game, dt);
    drawCapsules(ctx, game, r.time);
    drawLasers(ctx, game);
    drawTrails(ctx, fx, game.balls, game.tier, r.time);
    drawPaddle(ctx, r, game, pal, r.time);
    drawBalls(ctx, r, game, r.time);
    drawParticles(ctx, fx);
    drawRings(ctx, fx);
    drawFlashes(ctx, fx);
    drawPopups(ctx, fx);
    ctx.restore();

    drawVignette(ctx, r, game, pal);
    if (fx.screenFlash.alpha > 0) {
      const v = r.view;
      ctx.globalAlpha = fx.screenFlash.alpha;
      ctx.fillStyle = fx.screenFlash.color;
      ctx.fillRect(v.x - SHAKE_MARGIN, v.y - SHAKE_MARGIN, v.w + SHAKE_MARGIN * 2, v.h + SHAKE_MARGIN * 2);
      ctx.globalAlpha = 1;
    }
    drawHud(ctx, r, game, pal);
    drawBanner(ctx, r, fx);
    if (game.state === 'levelclear') drawLevelClear(ctx, r, game, pal);
    if (game.state === 'playing' && game.balls.some((b) => b.stuck && b.serve)) drawServeHint(ctx, r, game, input, fx, r.time);

    if (input && input.state.stick.active) drawStick(ctx, r, input.state.stick, pal);
  }

  return { resize, toLogical, draw, state: r, pulseMultiplier: () => (r.multPulse = 1) };
}

/** Scale the current transform about the field centre (the on-top camera push on the background). */
function zoomField(ctx, zoom) {
  if (zoom === 1) return;
  ctx.translate(FIELD.w / 2, FIELD.h / 2);
  ctx.scale(zoom, zoom);
  ctx.translate(-FIELD.w / 2, -FIELD.h / 2);
}

// ---- palette & caches --------------------------------------------------------

function readSafeArea(r) {
  try {
    const cs = getComputedStyle(document.documentElement);
    for (const side of ['top', 'right', 'bottom', 'left']) {
      r.safe[side] = parseFloat(cs.getPropertyValue(`--safe-${side}`)) || 0;
    }
  } catch {
    /* no DOM (tests) */
  }
}

/** Blend adjacent tier palettes; cached while the smoothed tier is unchanged. */
function palette(r) {
  const key = Math.round(r.tierSmooth * 32);
  if (r.pal && r.palKey === key) return r.pal;
  const t = key / 32;
  const i = clamp(Math.floor(t), 0, PALETTES.length - 1);
  const j = clamp(i + 1, 0, PALETTES.length - 1);
  const f = clamp(t - i, 0, 1);
  const a = PALETTES[i];
  const b = PALETTES[j];
  const accentRgb = mixHex(a.accent, b.accent, f);
  const glowRgb = mixHex(a.glow, b.glow, f);
  r.palKey = key;
  r.pal = {
    key,
    bg1: rgba(mixHex(a.bg1, b.bg1, f)),
    bg2: rgba(mixHex(a.bg2, b.bg2, f)),
    accent: rgba(accentRgb),
    accentRgb,
    glow: rgba(glowRgb),
    glowRgb,
    hue: lerp(a.hue, b.hue, f),
    tier: t,
  };
  return r.pal;
}

/** Rectangle outline drawn as four axis-aligned fills (far cheaper than stroking a huge path). */
function outlineRect(ctx, x, y, w, h, lw) {
  ctx.fillRect(x - lw, y - lw, w + lw * 2, lw); // top
  ctx.fillRect(x - lw, y + h, w + lw * 2, lw); // bottom
  ctx.fillRect(x - lw, y, lw, h); // left
  ctx.fillRect(x + w, y, lw, h); // right
}

function makeCanvas(wPx, hPx) {
  const c = document.createElement('canvas');
  c.width = Math.max(1, Math.ceil(wPx));
  c.height = Math.max(1, Math.ceil(hPx));
  return c;
}

/** Soft radial glow sprite (opaque centre → transparent), drawn scaled to any radius. */
function blob(r, color) {
  let s = r.blobs.get(color);
  if (s) return s;
  const size = 96;
  const c = makeCanvas(size, size);
  const cx = c.getContext('2d');
  const g = cx.createRadialGradient(size / 2, size / 2, size * 0.1, size / 2, size / 2, size / 2);
  g.addColorStop(0, color);
  g.addColorStop(1, 'rgba(0,0,0,0)');
  cx.fillStyle = g;
  cx.fillRect(0, 0, size, size);
  s = c;
  if (r.blobs.size > 24) r.blobs.delete(r.blobs.keys().next().value);
  r.blobs.set(color, s);
  return s;
}

let scratchCtx = null;
function measure(fontStr, text) {
  if (!scratchCtx) scratchCtx = makeCanvas(1, 1).getContext('2d');
  scratchCtx.font = fontStr;
  return scratchCtx.measureText(text).width;
}

/**
 * Glowing text baked once at device resolution (shadowBlur is expensive per frame).
 * Returns { canvas, w, h } in logical units; draw centred with drawImage.
 */
function glowText(r, text, weight, px, color, glowColor, blur) {
  const key = `${text}|${weight}|${px}|${color}|${glowColor}|${blur}`;
  let s = r.glows.get(key);
  if (s) {
    r.glows.delete(key); // LRU touch
    r.glows.set(key, s);
    return s;
  }
  const f = font(weight, px);
  const w = Math.ceil(measure(f, text) + blur * 2 + px * 0.6);
  const h = Math.ceil(px * 1.35 + blur * 2);
  const c = makeCanvas(w * r.k, h * r.k);
  const cx = c.getContext('2d');
  cx.scale(c.width / w, c.height / h);
  cx.font = f;
  cx.textAlign = 'center';
  cx.textBaseline = 'middle';
  if (blur > 0) {
    cx.shadowColor = glowColor;
    cx.shadowBlur = blur * r.k; // shadowBlur is in device pixels
    cx.fillStyle = glowColor;
    cx.fillText(text, w / 2, h / 2);
    cx.fillText(text, w / 2, h / 2);
    cx.shadowBlur = 0;
  }
  cx.fillStyle = color;
  cx.fillText(text, w / 2, h / 2);
  s = { canvas: c, w, h };
  if (r.glows.size > 32) r.glows.delete(r.glows.keys().next().value);
  r.glows.set(key, s);
  return s;
}

function drawGlowText(ctx, s, x, y, scale = 1, alpha = 1) {
  if (alpha !== 1) ctx.globalAlpha = alpha;
  const w = s.w * scale;
  const h = s.h * scale;
  ctx.drawImage(s.canvas, x - w / 2, y - h / 2, w, h);
  if (alpha !== 1) ctx.globalAlpha = 1;
}

// ---- background (whole canvas) ----------------------------------------------

function drawBackground(ctx, r, pal, game, beatPhase, dt) {
  const v = r.view;
  const bx = v.x - SHAKE_MARGIN;
  const by = v.y - SHAKE_MARGIN;
  const bw = v.w + SHAKE_MARGIN * 2;
  const bh = v.h + SHAKE_MARGIN * 2;
  const tier = pal.tier;

  // vertical wash in the tier colours (gradient object cached per palette/view)
  const gKey = `${pal.key}|${by}|${bh}`;
  if (r.grads.bgKey !== gKey) {
    const g = ctx.createLinearGradient(0, by, 0, by + bh);
    g.addColorStop(0, pal.bg2);
    g.addColorStop(1, pal.bg1);
    r.grads.bg = g;
    r.grads.bgKey = gKey;
  }
  ctx.fillStyle = r.grads.bg;
  ctx.fillRect(bx, by, bw, bh);

  // starburst rays from tier 3: more rays, brighter, faster as the tier climbs; rainbow at 5+
  if (tier >= 2.5) {
    const strength = clamp((tier - 2.5) / 1.5, 0, 1);
    const n = 12 + Math.round(clamp(tier, 3, 6)) * 2;
    const R = Math.hypot(bw, bh);
    const beat = 0.5 + 0.5 * Math.sin(beatPhase * TAU);
    const alpha = (0.05 + 0.035 * beat + 0.012 * clamp(tier - 3, 0, 3)) * strength;
    const rainbow = tier >= 5;
    ctx.save();
    ctx.translate(FIELD.w / 2, FIELD.h * 0.42);
    ctx.rotate(r.rayAngle);
    if (!rainbow) {
      ctx.fillStyle = hsl(pal.hue, 90, 62, alpha);
      ctx.beginPath();
    }
    for (let i = 0; i < n; i++) {
      const a = (i / n) * TAU;
      if (rainbow) {
        ctx.fillStyle = hsl(r.time * 60 + i * (360 / n), 95, 62, alpha * 1.15);
        ctx.beginPath();
      }
      ctx.moveTo(0, 0);
      ctx.arc(0, 0, R, a, a + TAU / n / 2);
      ctx.closePath();
      if (rainbow) ctx.fill();
    }
    if (!rainbow) ctx.fill();
    ctx.restore();
  }

  // starfield: slow parallax drift (faster while on top / at high tiers), twinkle
  const drift = (dt * 12 * (1 + game.overtop * 2 + tier * 0.25)) / FIELD.h;
  const stars = r.stars;
  for (let i = 0; i < stars.length; i++) {
    const s = stars[i];
    s.v += s.z * drift;
    if (s.v > 1) s.v -= 1;
    const tw = 0.5 + 0.5 * Math.sin(r.time * (1.5 + s.z) + s.tw);
    const lvl = Math.min(7, (tw * s.z * 8) | 0);
    ctx.fillStyle = STAR_ALPHAS[lvl];
    const size = 1.5 + s.z * 1.5;
    ctx.fillRect(v.x + s.u * v.w, v.y + s.v * v.h, size, size);
  }

  // faint grid across the whole view, pulsing to the beat (brighter at higher tiers)
  const beat = Math.pow(1 - beatPhase, 3);
  ctx.fillStyle = rgba(pal.glowRgb, 0.04 + 0.02 * tier + 0.09 * beat);
  const x0 = Math.floor(bx / 80) * 80;
  const y0 = Math.floor(by / 80) * 80;
  for (let x = x0; x <= bx + bw; x += 80) ctx.fillRect(x - 0.5, by, 1, bh);
  for (let y = y0; y <= by + bh; y += 80) ctx.fillRect(bx, y - 0.5, bw, 1);
}

/** Dashed guides marking the paddle's vertical band (field layer, so they zoom with the paddle). */
function drawBandGuides(ctx, pal) {
  ctx.strokeStyle = rgba(pal.glowRgb, 0.14);
  ctx.lineWidth = 2;
  ctx.setLineDash(BAND_DASH);
  ctx.beginPath();
  ctx.moveTo(0, PADDLE.yMin - PADDLE.h);
  ctx.lineTo(FIELD.w, PADDLE.yMin - PADDLE.h);
  ctx.moveTo(0, PADDLE.yMax + PADDLE.h);
  ctx.lineTo(FIELD.w, PADDLE.yMax + PADDLE.h);
  ctx.stroke();
  ctx.setLineDash(NO_DASH);
}

/** Dim the letterbox slightly and outline the field so the play boundary stays obvious. */
function drawFrame(ctx, r, pal, game) {
  const v = r.view;
  const m = SHAKE_MARGIN;
  ctx.fillStyle = 'rgba(2,3,8,0.4)';
  if (v.x < -0.5) ctx.fillRect(v.x - m, v.y - m, -v.x + m, v.h + 2 * m);
  if (v.x + v.w > FIELD.w + 0.5) ctx.fillRect(FIELD.w, v.y - m, v.x + v.w - FIELD.w + m, v.h + 2 * m);
  if (v.y < -0.5) ctx.fillRect(0, v.y - m, FIELD.w, -v.y + m);
  if (v.y + v.h > FIELD.h + 0.5) ctx.fillRect(0, FIELD.h, FIELD.w, v.y + v.h - FIELD.h + m);

  // soft outer glow that grows with tier / on-top, then a crisp edge line
  const tier = game.tier;
  const lw = 12 + tier * 4;
  ctx.fillStyle = rgba(pal.glowRgb, 0.08 + tier * 0.035 + game.overtop * 0.1);
  outlineRect(ctx, 0, 0, FIELD.w, FIELD.h, lw);
  ctx.fillStyle = rgba(pal.glowRgb, 0.35 + tier * 0.05);
  outlineRect(ctx, 0, 0, FIELD.w, FIELD.h, 2);
}

// ---- attic indicator ------------------------------------------------------

/** Top edge of the highest remaining breakable brick (the attic floor). */
function atticTop(game) {
  let top = Infinity;
  for (const b of game.bricks) {
    if (b.alive && b.y < top && BRICK_TYPES[b.type].breakable) top = b.y;
  }
  return top;
}

/**
 * Faint glowing band filling the empty zone above the highest brick row — the place
 * to aim for. It brightens (and the label lights up) while a ball is up there.
 */
function drawAttic(ctx, r, game, pal) {
  const top = atticTop(game);
  if (!Number.isFinite(top) || top < 60) return;
  const on = game.overtop;
  const line = Math.round(top) - 5;
  const q = Math.round(on * 16) / 16;
  const key = `${pal.key}|${line}|${q}`;
  if (r.grads.atticKey !== key) {
    const g = ctx.createLinearGradient(0, 0, 0, line);
    g.addColorStop(0, rgba(pal.glowRgb, 0.02 + 0.03 * q));
    g.addColorStop(1, rgba(pal.glowRgb, 0.09 + 0.2 * q));
    r.grads.attic = g;
    r.grads.atticKey = key;
  }
  ctx.fillStyle = r.grads.attic;
  ctx.fillRect(0, 0, FIELD.w, line);
  ctx.strokeStyle = rgba(pal.glowRgb, 0.22 + 0.5 * on);
  ctx.lineWidth = 1.5;
  ctx.setLineDash(ATTIC_DASH);
  ctx.beginPath();
  ctx.moveTo(0, line);
  ctx.lineTo(FIELD.w, line);
  ctx.stroke();
  ctx.setLineDash(NO_DASH);
  ctx.font = font(800, 12);
  ctx.textAlign = 'left';
  ctx.textBaseline = 'alphabetic';
  ctx.fillStyle = rgba(pal.accentRgb, 0.35 + 0.6 * on);
  ctx.fillText(on > 0.5 ? 'THE ATTIC · ×2 POINTS' : 'THE ATTIC ↑', 14, line - 8);
}

// ---- bricks (sprite cached) ---------------------------------------------------

const PAD = 4; // sprite padding (logical units) for the drop shadow / glow

function roundRect(ctx, x, y, w, h, rad) {
  const rr = Math.min(rad, w / 2, h / 2);
  ctx.beginPath();
  ctx.moveTo(x + rr, y);
  ctx.arcTo(x + w, y, x + w, y + h, rr);
  ctx.arcTo(x + w, y + h, x, y + h, rr);
  ctx.arcTo(x, y + h, x, y, rr);
  ctx.arcTo(x, y, x + w, y, rr);
  ctx.closePath();
}

/** Sprite baked at `px` device pixels per logical unit (the field scale, quantised). */
function brickSprite(r, b, px) {
  const key = `${b.type}|${b.color}|${b.w}|${b.h}|${px}`;
  let s = r.sprites.get(key);
  if (s) return s;
  const w = b.w + PAD * 2;
  const h = b.h + PAD * 2;
  const c = makeCanvas(w * px, h * px);
  const cx = c.getContext('2d');
  cx.scale(c.width / w, c.height / h);
  cx.translate(PAD, PAD);
  paintBrick(cx, b.type, b.color, b.w, b.h);
  if (r.sprites.size > 160) r.sprites.clear(); // scale variants accumulate; cheap to rebuild
  r.sprites.set(key, c);
  return c;
}

/** Static brick artwork (body, bevel, type glyph). Animated bits live in drawBrickAnim. */
function paintBrick(ctx, type, base, w, h) {
  // drop shadow
  ctx.fillStyle = 'rgba(0,0,0,0.35)';
  roundRect(ctx, 0, 2.5, w, h, 6);
  ctx.fill();
  // body + vertical shade
  roundRect(ctx, 0, 0, w, h, 6);
  ctx.fillStyle = base;
  ctx.fill();
  const g = ctx.createLinearGradient(0, 0, 0, h);
  g.addColorStop(0, 'rgba(255,255,255,0.10)');
  g.addColorStop(1, 'rgba(0,0,0,0.38)');
  ctx.fillStyle = g;
  ctx.fill();
  // top bevel
  ctx.fillStyle = 'rgba(255,255,255,0.3)';
  roundRect(ctx, 3, 2, w - 6, 5, 3);
  ctx.fill();

  ctx.save();
  ctx.translate(w / 2, h / 2);
  switch (type) {
    case 'angle': {
      // Prism: diamond
      ctx.fillStyle = 'rgba(255,255,255,0.75)';
      ctx.beginPath();
      ctx.moveTo(0, -9);
      ctx.lineTo(10, 0);
      ctx.lineTo(0, 9);
      ctx.lineTo(-10, 0);
      ctx.closePath();
      ctx.fill();
      break;
    }
    case 'speed': {
      // Armor: speed gauge glyph + side plates
      ctx.strokeStyle = 'rgba(20,24,34,0.85)';
      ctx.lineWidth = 3;
      ctx.beginPath();
      ctx.arc(0, 5, 9, Math.PI, TAU);
      ctx.stroke();
      ctx.beginPath();
      ctx.moveTo(0, 5);
      ctx.lineTo(6, -3);
      ctx.stroke();
      ctx.fillStyle = 'rgba(20,24,34,0.85)';
      ctx.fillRect(-w / 2 + 6, -h / 2 + 9, 6, h - 16);
      ctx.fillRect(w / 2 - 12, -h / 2 + 9, 6, h - 16);
      break;
    }
    case 'top': {
      // Lid: hatched top strip + lock glyph
      ctx.strokeStyle = 'rgba(255,255,255,0.7)';
      ctx.lineWidth = 2;
      ctx.beginPath();
      for (let i = -w / 2; i < w / 2; i += 8) {
        ctx.moveTo(i, -h / 2 + 1);
        ctx.lineTo(i + 6, -h / 2 + 8);
      }
      ctx.stroke();
      ctx.fillStyle = 'rgba(30,10,50,0.85)';
      ctx.fillRect(-6, 0, 12, 9);
      ctx.strokeStyle = 'rgba(30,10,50,0.85)';
      ctx.beginPath();
      ctx.arc(0, 0, 4, Math.PI, TAU);
      ctx.stroke();
      break;
    }
    case 'ghost': {
      ctx.strokeStyle = 'rgba(255,255,255,0.8)';
      ctx.setLineDash([5, 4]);
      ctx.lineWidth = 2;
      roundRect(ctx, -w / 2 + 4, -h / 2 + 4, w - 8, h - 8, 4);
      ctx.stroke();
      ctx.setLineDash(NO_DASH);
      break;
    }
    case 'boost': {
      // Charger: lightning bolt (outline pulses in drawBrickAnim)
      ctx.fillStyle = 'rgba(80,50,0,0.9)';
      ctx.beginPath();
      ctx.moveTo(2, -11);
      ctx.lineTo(-6, 1);
      ctx.lineTo(0, 1);
      ctx.lineTo(-2, 11);
      ctx.lineTo(6, -1);
      ctx.lineTo(0, -1);
      ctx.closePath();
      ctx.fill();
      break;
    }
    case 'bomb': {
      ctx.fillStyle = '#1d0d12';
      ctx.beginPath();
      ctx.arc(0, 2, 9, 0, TAU);
      ctx.fill();
      ctx.strokeStyle = '#1d0d12';
      ctx.lineWidth = 2;
      ctx.beginPath();
      ctx.moveTo(4, -5);
      ctx.quadraticCurveTo(10, -12, 14, -8);
      ctx.stroke();
      break;
    }
    case 'gem': {
      ctx.fillStyle = 'rgba(0,60,30,0.85)';
      ctx.font = font(700, 18);
      ctx.textAlign = 'center';
      ctx.textBaseline = 'middle';
      ctx.fillText('?', 0, 1);
      ctx.strokeStyle = 'rgba(0,60,30,0.6)';
      ctx.lineWidth = 2;
      roundRect(ctx, -w / 2 + 3, -h / 2 + 3, w - 6, h - 6, 4);
      ctx.stroke();
      break;
    }
    case 'steel': {
      ctx.fillStyle = 'rgba(255,255,255,0.35)';
      for (const px of [-w / 2 + 8, w / 2 - 8]) {
        for (const py of [-h / 2 + 7, h / 2 - 7]) {
          ctx.beginPath();
          ctx.arc(px, py, 2.2, 0, TAU);
          ctx.fill();
        }
      }
      ctx.fillStyle = 'rgba(0,0,0,0.25)';
      ctx.fillRect(-w / 2 + 14, -2, w - 28, 4);
      break;
    }
    default:
      break;
  }
  ctx.restore();
}

/** Per-frame animated details: prism shimmer, charger pulse, bomb fuse spark. */
function drawBrickAnim(ctx, b, time) {
  const { x, y, w, h } = b;
  switch (b.type) {
    case 'angle': {
      const sx = (time * 90 + b.col * 17) % (w + 20) - 10;
      const x0 = Math.max(0, sx);
      const x1 = Math.min(w, sx + 6);
      if (x1 > x0) {
        ctx.fillStyle = 'rgba(255,255,255,0.35)';
        ctx.fillRect(x + x0, y + 3, x1 - x0, h - 6);
      }
      break;
    }
    case 'boost': {
      ctx.strokeStyle = `rgba(255,255,255,${(0.3 + 0.3 * Math.sin(time * 6)).toFixed(3)})`;
      ctx.lineWidth = 2;
      roundRect(ctx, x + 1, y + 1, w - 2, h - 2, 6);
      ctx.stroke();
      break;
    }
    case 'bomb': {
      const s = Math.sin(time * 14 + b.col);
      ctx.fillStyle = `rgba(255,${(180 + 70 * s) | 0},80,1)`;
      ctx.beginPath();
      ctx.arc(x + w / 2 + 14, y + h / 2 - 8, 2.5 + s, 0, TAU);
      ctx.fill();
      break;
    }
    default:
      break;
  }
}

/**
 * Bricks are blitted 1:1 on integer device pixels — the rasteriser's fast copy path,
 * no resampling. The bake scale follows the current transform (quantised to 1 %), so
 * any camera scale swaps sprite sets instead of bilinear-scaling 100+ images per frame.
 */
function drawBricks(ctx, r, game, dt) {
  const clock = game.clock;
  const time = game.time;
  const m = ctx.getTransform ? ctx.getTransform() : null;
  const a = m ? m.a : r.k; // device px per logical unit, zoom included
  const px = Math.round((a / r.k) * 100) / 100 * r.k; // quantised bake scale
  const snap = !!m;
  if (snap) ctx.imageSmoothingEnabled = false;
  for (const b of game.bricks) {
    if (!b.alive) continue;
    if (b.flash > 0) b.flash = Math.max(0, b.flash - dt * 5);
    let alpha = 1;
    if (b.type === 'ghost') {
      // solid first half of the cycle; fade across the transitions
      const c = ghostCycle(b, clock);
      const solid = c < 0.5;
      const edge = Math.min(Math.abs(c - 0.5), Math.abs(c - (c < 0.5 ? 0 : 1))) / 0.06;
      const soft = clamp(edge, 0, 1);
      alpha = solid ? lerp(0.5, 0.95, soft) : lerp(0.5, 0.16, soft);
    }
    if (alpha !== 1) ctx.globalAlpha = alpha;
    const sp = brickSprite(r, b, px);
    if (snap) {
      const dx = (Math.round(m.e + (b.x - PAD) * a) - m.e) / a;
      const dy = (Math.round(m.f + (b.y - PAD) * a) - m.f) / a;
      ctx.drawImage(sp, dx, dy, sp.width / a, sp.height / a);
    } else {
      ctx.drawImage(sp, b.x - PAD, b.y - PAD, b.w + PAD * 2, b.h + PAD * 2);
    }
    drawBrickAnim(ctx, b, time);
    if (alpha !== 1) ctx.globalAlpha = 1;
    if (b.flash > 0) {
      ctx.globalAlpha = b.flash * 0.8;
      ctx.fillStyle = '#ffffff';
      roundRect(ctx, b.x, b.y, b.w, b.h, 6);
      ctx.fill();
      ctx.globalAlpha = 1;
    }
  }
  if (snap) ctx.imageSmoothingEnabled = true;
}

// ---- capsules / lasers / paddle / balls ---------------------------------------

function drawCapsules(ctx, game, time) {
  for (const c of game.capsules) {
    const def = POWERS[c.kind];
    const bob = Math.sin(time * 6 + c.spin) * 2;
    ctx.save();
    ctx.translate(c.x, c.y + bob);
    ctx.fillStyle = 'rgba(0,0,0,0.35)';
    roundRect(ctx, -c.w / 2 + 2, -c.h / 2 + 3, c.w, c.h, 13);
    ctx.fill();
    ctx.fillStyle = def.color;
    roundRect(ctx, -c.w / 2, -c.h / 2, c.w, c.h, 13);
    ctx.fill();
    ctx.fillStyle = 'rgba(255,255,255,0.35)';
    roundRect(ctx, -c.w / 2 + 4, -c.h / 2 + 3, c.w - 8, 7, 4);
    ctx.fill();
    ctx.fillStyle = '#10141f';
    ctx.font = font(800, 18);
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    ctx.fillText(def.letter, 0, 1);
    ctx.restore();
  }
}

function drawLasers(ctx, game) {
  for (const l of game.lasers) {
    ctx.fillStyle = 'rgba(255,120,120,0.45)';
    ctx.fillRect(l.x - 5, l.y - l.h, 10, l.h);
    ctx.fillStyle = '#fff0f0';
    ctx.fillRect(l.x - 1.5, l.y - l.h, 3, l.h);
  }
}

function drawPaddle(ctx, r, game, pal, time) {
  const p = game.paddle;
  const w = p.w * (1 + p.squash * 0.12); // squash: wider & flatter on impact
  const h = p.h * (1 - p.squash * 0.25);
  const y = p.y + p.recoil * 7; // recoil: dips on impact
  ctx.save();
  ctx.translate(p.x, y);
  // glow: tier-tinted halo (pre-rendered blob) + soft outline
  const glowA = 0.25 + game.tier * 0.06 + p.glow * 0.4;
  const glowColor = p.magnetTime > 0 ? 'rgba(192,132,252,1)' : pal.accent;
  ctx.globalAlpha = Math.min(1, glowA);
  ctx.drawImage(blob(r, glowColor), -w / 2 - 30, -h * 2.4, w + 60, h * 4.8);
  ctx.globalAlpha = 1;
  ctx.fillStyle = p.magnetTime > 0 ? `rgba(192,132,252,${glowA})` : rgba(pal.accentRgb, glowA);
  roundRect(ctx, -w / 2 - 8, -h / 2 - 8, w + 16, h + 16, 16);
  ctx.fill();
  // body
  const g = ctx.createLinearGradient(0, -h / 2, 0, h / 2);
  g.addColorStop(0, '#ffffff');
  g.addColorStop(0.5, '#dbe6ff');
  g.addColorStop(1, '#8aa0c8');
  ctx.fillStyle = g;
  roundRect(ctx, -w / 2, -h / 2, w, h, 11);
  ctx.fill();
  // accent stripe
  ctx.fillStyle = pal.accent;
  roundRect(ctx, -w / 2 + 10, -3, w - 20, 6, 3);
  ctx.fill();
  if (p.laserTime > 0) {
    ctx.fillStyle = '#ff5d5d';
    for (const sx of [-1, 1]) {
      roundRect(ctx, sx * w * 0.36 - 4, -h / 2 - 8, 8, 10, 2);
      ctx.fill();
    }
  }
  if (p.magnetTime > 0) {
    ctx.strokeStyle = `rgba(192,132,252,${(0.6 + 0.3 * Math.sin(time * 8)).toFixed(3)})`;
    ctx.lineWidth = 2;
    ctx.beginPath();
    ctx.arc(0, -h / 2, w * 0.3, Math.PI, TAU);
    ctx.stroke();
  }
  ctx.restore();
}

const GLOW_TOP = 'rgba(255,242,168,0.6)';
const GLOW_NORMAL = 'rgba(160,210,255,0.5)';
const GLOW_FIRE = 'rgba(255,150,60,0.6)';

function drawBalls(ctx, r, game, time) {
  for (const b of game.balls) {
    const heavy = b.power === 'heavy' || b.forceHeavy;
    const color = b.power === 'fire' ? '#ff9a3c' : heavy ? '#cfd6e6' : b.power === 'slow' ? '#9fe6ff' : '#ffffff';
    ctx.save();
    ctx.translate(b.x, b.y);
    const sq = b.squash;
    if (sq > 0.01) ctx.scale(1 + sq * 0.35, 1 - sq * 0.3);
    // glow grows with tier and while on top (pre-rendered radial sprite)
    const glowR = b.r * (2.4 + game.tier * 0.3 + (b.onTop ? 1 : 0));
    ctx.drawImage(blob(r, b.onTop ? GLOW_TOP : b.power === 'fire' ? GLOW_FIRE : GLOW_NORMAL), -glowR, -glowR, glowR * 2, glowR * 2);
    // halo after 4 s on top
    if (b.halo) {
      ctx.strokeStyle = RAINBOW[((time * 36) | 0) % RAINBOW.length];
      ctx.lineWidth = 3;
      ctx.beginPath();
      ctx.ellipse(0, 0, b.r * 2.4, b.r * 1.1, time * 3, 0, TAU);
      ctx.stroke();
    }
    ctx.fillStyle = color;
    ctx.beginPath();
    ctx.arc(0, 0, b.r, 0, TAU);
    ctx.fill();
    ctx.fillStyle = 'rgba(255,255,255,0.8)';
    ctx.beginPath();
    ctx.arc(-b.r * 0.3, -b.r * 0.3, b.r * 0.35, 0, TAU);
    ctx.fill();
    if (heavy) {
      ctx.strokeStyle = 'rgba(40,50,70,0.7)';
      ctx.lineWidth = 2;
      ctx.beginPath();
      ctx.arc(0, 0, b.r * 0.6, 0, TAU);
      ctx.stroke();
    }
    ctx.restore();
  }
}

// ---- vignette / edge glow (whole canvas) ---------------------------------------

function drawVignette(ctx, r, game, pal) {
  const v = r.view;
  const slow = game.slowmo > 0 || game.balls.some((b) => b.power === 'slow');
  const strength = Math.round((0.18 + game.tier * 0.045 + game.overtop * 0.1 + (slow ? 0.28 : 0)) * 50) / 50;
  const key = `${pal.key}|${strength}|${slow ? 1 : 0}|${v.w}|${v.h}`;
  if (r.grads.vigKey !== key) {
    const cx = FIELD.w / 2;
    const cy = FIELD.h / 2;
    const outer = Math.hypot(v.w, v.h) * 0.55;
    const g = ctx.createRadialGradient(cx, cy, outer * 0.45, cx, cy, outer);
    g.addColorStop(0, 'rgba(0,0,0,0)');
    g.addColorStop(1, slow ? `rgba(40,120,255,${strength})` : rgba(pal.glowRgb, strength));
    r.grads.vig = g;
    r.grads.vigKey = key;
    r.grads.vigHole = (outer * 0.45) / Math.SQRT2 - 1; // half-side of the transparent square
  }
  ctx.fillStyle = r.grads.vig;
  // The gradient is fully transparent inside its inner radius, so fill only the frame
  // around the largest square that fits in it (≈30% less full-canvas gradient work).
  const hole = r.grads.vigHole;
  const x0 = v.x - SHAKE_MARGIN;
  const y0 = v.y - SHAKE_MARGIN;
  const x1 = v.x + v.w + SHAKE_MARGIN;
  const y1 = v.y + v.h + SHAKE_MARGIN;
  const hx0 = Math.max(x0, FIELD.w / 2 - hole);
  const hx1 = Math.min(x1, FIELD.w / 2 + hole);
  const hy0 = Math.max(y0, FIELD.h / 2 - hole);
  const hy1 = Math.min(y1, FIELD.h / 2 + hole);
  ctx.fillRect(x0, y0, x1 - x0, hy0 - y0); // top band
  ctx.fillRect(x0, hy1, x1 - x0, y1 - hy1); // bottom band
  ctx.fillRect(x0, hy0, hx0 - x0, hy1 - hy0); // left strip
  ctx.fillRect(hx1, hy0, x1 - hx1, hy1 - hy0); // right strip
  // chromatic edge glow inside the field from tier 1: three hue-shifted strokes
  if (game.tier >= 1) {
    const a = 0.1 + game.tier * 0.04;
    const lw = 3 + game.tier;
    ctx.fillStyle = hsl(pal.hue - 40, 95, 60, a);
    outlineRect(ctx, lw, lw, FIELD.w - lw * 2, FIELD.h - lw * 2, lw);
    ctx.fillStyle = hsl(pal.hue + 40, 95, 60, a);
    outlineRect(ctx, lw * 2, lw * 2, FIELD.w - lw * 4, FIELD.h - lw * 4, lw);
    ctx.fillStyle = rgba(pal.glowRgb, a * 0.8);
    outlineRect(ctx, lw * 1.5, lw * 1.5, FIELD.w - lw * 3, FIELD.h - lw * 3, lw);
  }
}

// ---- HUD --------------------------------------------------------------------

/** Cached HUD strings (formatting scores every frame allocates + runs a regex). */
function hudStrings(r, game) {
  const s = r.strings;
  const score = game.scoring.score;
  if (s.score !== score) {
    s.score = score;
    s.scoreStr = formatScore(score);
  }
  const best = Math.max(game.highScore, score);
  if (s.best !== best) {
    s.best = best;
    s.bestStr = `BEST ${formatScore(best)}`;
  }
  const levelKey = game.level ? `${game.levelIndex}|${game.loop}|${game.level.name}` : null;
  if (s.level !== levelKey) {
    s.level = levelKey;
    s.levelStr = game.level ? `LEVEL ${game.levelIndex + 1 + game.loop * LEVELS.length} · ${game.level.name.toUpperCase()}` : '';
  }
  if (s.loop !== game.loop) {
    s.loop = game.loop;
    s.loopStr = game.loop > 0 ? `LOOP ${game.loop + 1} · +${Math.round((loopSpeedScale(game.loop) - 1) * 100)}% SPEED` : 'LIVES';
  }
  return s;
}

/**
 * HUD. Portrait / square viewports draw it inside the field (classic). When the
 * viewport is wide enough (aspect ≥ 1.05: landscape phones, desktop) the letterbox
 * margins are huge and the field is small, so the HUD moves into the margins and
 * scales up — readable, and the play area stays clean.
 */
function drawHud(ctx, r, game, pal) {
  const s = hudStrings(r, game);
  if (r.wide) drawHudWide(ctx, r, game, pal, s);
  else drawHudField(ctx, r, game, pal, s);
}

function drawHudField(ctx, r, game, pal, s) {
  const top = r.hudTop;
  ctx.save();
  ctx.translate(22, top + 14);
  drawScoreBlock(ctx, s, 'left');
  ctx.restore();
  ctx.save();
  ctx.translate(FIELD.w - 22, top + 14);
  drawLevelBlock(ctx, game, s, 'right');
  ctx.restore();
  drawMultiplier(ctx, r, game, pal, FIELD.w / 2, top + 46, 1);

  const bottom = FIELD.h - r.hudBottom;
  ctx.save();
  ctx.translate(22, bottom - 58);
  drawChips(ctx, game, 'row');
  ctx.restore();
  if (game.anyOnTop) drawOnTop(ctx, game, FIELD.w - 22, bottom - 45, 'right');
}

function drawHudWide(ctx, r, game, pal, s) {
  const v = r.view;
  const hs = r.hs;
  const lx = v.x / 2;
  const rx = FIELD.w - v.x / 2;
  const top = v.y + r.safe.top / r.scale + 36 * hs;
  // left column: score
  ctx.save();
  ctx.translate(lx, top);
  ctx.scale(hs, hs);
  drawScoreBlock(ctx, s, 'center');
  ctx.translate(-60, 110);
  drawChips(ctx, game, 'column');
  ctx.restore();
  // right column: multiplier, level, lives
  drawMultiplier(ctx, r, game, pal, rx, top + 40 * hs, hs);
  ctx.save();
  ctx.translate(rx, top + 110 * hs);
  ctx.scale(hs, hs);
  drawLevelBlock(ctx, game, s, 'center');
  ctx.restore();
  if (game.anyOnTop) {
    ctx.save();
    ctx.translate(rx, Math.min(v.y + v.h, FIELD.h) - 40 * hs);
    ctx.scale(hs, hs);
    drawOnTop(ctx, game, 0, 0, 'center');
    ctx.restore();
  }
}

/** SCORE / number / BEST, drawn at the origin. */
function drawScoreBlock(ctx, s, align) {
  ctx.textBaseline = 'top';
  ctx.textAlign = align;
  ctx.font = font(600, 15);
  ctx.fillStyle = 'rgba(255,255,255,0.55)';
  ctx.fillText('SCORE', 0, 0);
  ctx.font = font(800, 34);
  ctx.fillStyle = '#ffffff';
  ctx.fillText(s.scoreStr, 0, 16);
  ctx.font = font(600, 13);
  ctx.fillStyle = 'rgba(255,255,255,0.45)';
  ctx.fillText(s.bestStr, 0, 56);
}

/** Level name, life dots and LIVES / loop label, drawn at the origin. */
function drawLevelBlock(ctx, game, s, align) {
  ctx.textBaseline = 'top';
  ctx.textAlign = align;
  ctx.font = font(600, 15);
  ctx.fillStyle = 'rgba(255,255,255,0.55)';
  ctx.fillText(s.levelStr, 0, 0);
  const n = Math.min(game.lives, 8);
  const start = align === 'right' ? -8 : align === 'center' ? ((n - 1) * 22) / 2 : (n - 1) * 22 + 8;
  ctx.fillStyle = '#ffffff';
  for (let i = 0; i < n; i++) {
    ctx.beginPath();
    ctx.arc(start - i * 22, 30, 7, 0, TAU);
    ctx.fill();
  }
  ctx.fillStyle = 'rgba(255,255,255,0.45)';
  ctx.font = font(600, 13);
  ctx.fillText(s.loopStr, 0, 48);
}

/** Multiplier: pre-rendered glow text that pulses on each increment, plus the tier name. */
function drawMultiplier(ctx, r, game, pal, x, y, hs) {
  const tier = game.tier;
  const sc = game.scoring;
  // top tier: white digits on the magenta glow (white-on-white would vanish; a cycling
  // glow colour would re-bake the sprite every few frames)
  const glowColor = PALETTES[tier].glow;
  const textColor = tier >= 6 ? '#ffffff' : PALETTES[tier].accent;
  const glow = glowText(r, `×${sc.multiplier}`, 900, 44 + tier * 2, textColor, glowColor, 10 + tier * 3);
  const pulse = (1 + r.multPulse * 0.35) * hs;
  if (r.multPulse > 0.02) {
    ctx.globalAlpha = r.multPulse * 0.7;
    const br = (70 + tier * 10) * hs;
    ctx.drawImage(blob(r, pal.glow), x - br, y - br, br * 2, br * 2);
    ctx.globalAlpha = 1;
  }
  drawGlowText(ctx, glow, x, y, pulse);
  ctx.save();
  ctx.translate(x, y + 30 * hs);
  ctx.scale(hs, hs);
  ctx.textAlign = 'center';
  ctx.textBaseline = 'top';
  ctx.font = font(700, 13);
  ctx.fillStyle = rgba(pal.accentRgb, 0.9);
  ctx.fillText(TIER_LABELS[tier], 0, 0);
  ctx.restore();
}

/** Active power-up chips with remaining-time bars; laid out as a row or a column from the origin. */
function drawChips(ctx, game, layout) {
  let cx = 0;
  let cy = 0;
  const seen = [];
  ctx.textAlign = 'left';
  ctx.textBaseline = 'middle';
  const chip = (kind, time) => {
    if (seen.includes(kind)) return;
    seen.push(kind);
    const def = POWERS[kind];
    const frac = clamp(time / (def.duration || 1), 0, 1);
    ctx.fillStyle = 'rgba(0,0,0,0.35)';
    roundRect(ctx, cx, cy, 120, 26, 13);
    ctx.fill();
    ctx.fillStyle = def.color;
    roundRect(ctx, cx, cy, Math.max(26, 120 * frac), 26, 13);
    ctx.fill();
    ctx.fillStyle = '#10141f';
    ctx.font = font(800, 13);
    ctx.fillText(`${def.letter} ${def.name}`, cx + 10, cy + 13);
    if (layout === 'row') cx += 128;
    else cy += 32;
  };
  for (const b of game.balls) if (b.power) chip(b.power, b.powerTime);
  for (const pp of activePaddlePowers(game.paddle)) chip(pp.kind, pp.time);
}

function drawOnTop(ctx, game, x, y, align) {
  ctx.textAlign = align;
  ctx.textBaseline = 'middle';
  ctx.fillStyle = '#fff2a8';
  ctx.font = font(800, 16);
  ctx.fillText(`ON TOP ${game.topTime.toFixed(1)}s`, x, y);
}

function drawBanner(ctx, r, fx) {
  const b = fx.banner;
  if (!b) return;
  const t = 1 - b.life / b.maxLife;
  const inA = easeOutBack(clamp(t * 4, 0, 1));
  const out = clamp((t - 0.75) * 4, 0, 1);
  ctx.save();
  ctx.globalAlpha = 1 - out;
  ctx.translate(FIELD.w / 2, FIELD.h * 0.36);
  ctx.scale(inA * r.hs, inA * r.hs); // in wide mode the banner sweeps across the margins too
  // sweep bar
  const bw = FIELD.w * 1.2;
  ctx.fillStyle = 'rgba(0,0,0,0.45)';
  ctx.fillRect(-bw / 2, -46, bw, 92);
  const sx = -bw / 2 + easeOutCubic(clamp(t * 1.4, 0, 1)) * bw;
  const sweep = ctx.createLinearGradient(sx - 140, 0, sx + 40, 0);
  sweep.addColorStop(0, 'rgba(255,255,255,0)');
  sweep.addColorStop(0.8, 'rgba(255,255,255,0.28)');
  sweep.addColorStop(1, 'rgba(255,255,255,0)');
  ctx.fillStyle = sweep;
  ctx.fillRect(sx - 140, -46, 180, 92);
  ctx.fillStyle = b.color;
  ctx.fillRect(-bw / 2, -46, bw, 3);
  ctx.fillRect(-bw / 2, 43, bw, 3);
  const title = glowText(r, b.text, 900, 44, '#ffffff', b.color, 24);
  drawGlowText(ctx, title, 0, b.sub ? -12 : 0);
  if (b.sub) {
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    ctx.font = font(600, 18);
    ctx.fillStyle = 'rgba(255,255,255,0.85)';
    ctx.fillText(b.sub, 0, 24);
  }
  ctx.restore();
}

function drawLevelClear(ctx, r, game, pal) {
  const time = r.time;
  const v = r.view;
  ctx.save();
  ctx.fillStyle = 'rgba(0,0,0,0.35)';
  ctx.fillRect(v.x - SHAKE_MARGIN, v.y - SHAKE_MARGIN, v.w + SHAKE_MARGIN * 2, v.h + SHAKE_MARGIN * 2);
  ctx.translate(FIELD.w / 2, FIELD.h * 0.42);
  ctx.scale(r.hs, r.hs);
  const title = glowText(r, 'LEVEL CLEAR', 900, 56, '#ffffff', pal.accent, 30);
  drawGlowText(ctx, title, 0, Math.sin(time * 4) * 4);
  ctx.textAlign = 'center';
  ctx.textBaseline = 'middle';
  ctx.font = font(700, 26);
  ctx.fillStyle = pal.accent;
  ctx.fillText(`BONUS +${formatScore(game.levelBonus)}`, 0, 56);
  ctx.font = font(600, 18);
  ctx.fillStyle = 'rgba(255,255,255,0.7)';
  ctx.fillText(`best multiplier ×${game.stats.bestMultiplier} · ${game.stats.topBreaks} bricks from the attic`, 0, 96);
  ctx.restore();
}

function drawServeHint(ctx, r, game, input, fx, time) {
  const mode = input ? input.state.mode : 'mouse';
  const text = mode === 'touch' ? 'TAP TO LAUNCH' : mode === 'keys' ? 'SPACE TO LAUNCH' : 'CLICK TO LAUNCH';
  const hs = Math.min(r.hs, 1.8);
  ctx.save();
  ctx.translate(FIELD.w / 2, PADDLE.yMin - 90);
  ctx.scale(hs, hs);
  ctx.globalAlpha = 0.6 + 0.4 * Math.sin(time * 4);
  ctx.textAlign = 'center';
  ctx.textBaseline = 'middle';
  ctx.font = font(800, 22);
  ctx.fillStyle = '#ffffff';
  ctx.fillText(text, 0, 0);
  // the level banner already shows the hint while it is up — don't print it twice
  if (game.level && game.level.hint && !fx.banner) {
    ctx.globalAlpha = 0.75;
    ctx.font = font(600, 16);
    ctx.fillStyle = 'rgba(255,255,255,0.85)';
    ctx.fillText(game.level.hint, 0, 30);
  }
  ctx.restore();
}

/** Virtual thumbstick, drawn in CSS-pixel screen space where the thumb landed (letterbox included). */
function drawStick(ctx, r, stick, pal) {
  ctx.setTransform(r.dpr, 0, 0, r.dpr, 0, 0);
  const R = STICK.radius;
  let dx = stick.x - stick.ox;
  let dy = stick.y - stick.oy;
  const d = Math.hypot(dx, dy);
  if (d > R) {
    dx = (dx / d) * R;
    dy = (dy / d) * R;
  }
  ctx.globalAlpha = 0.4;
  ctx.strokeStyle = '#ffffff';
  ctx.lineWidth = 2;
  ctx.beginPath();
  ctx.arc(stick.ox, stick.oy, R, 0, TAU);
  ctx.stroke();
  ctx.fillStyle = 'rgba(255,255,255,0.08)';
  ctx.fill();
  // dead-zone: per-axis (narrow sideways, taller up/down), drawn as the actual no-motion box
  ctx.globalAlpha = 0.25;
  roundRect(ctx, stick.ox - STICK.deadZone, stick.oy - STICK.deadZoneY, STICK.deadZone * 2, STICK.deadZoneY * 2, 5);
  ctx.stroke();
  ctx.globalAlpha = 0.75;
  ctx.fillStyle = pal.accent;
  ctx.beginPath();
  ctx.arc(stick.ox + dx, stick.oy + dy, 26, 0, TAU);
  ctx.fill();
  ctx.globalAlpha = 1;
}
