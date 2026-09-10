// js/renderer.js — canvas renderer for Rooftop Breakout.
//
// Draws the whole game world in *world units* (480x880). At the start of every
// frame the context is scaled by (canvas.width/worldW, canvas.height/worldH)
// so draw calls below use world coordinates directly.
//
// Import safety: nothing touches the DOM at import time. The constructor only
// calls canvas.getContext('2d') (guarded) and accepts any object that merely
// has getContext() — or none at all. All offscreen caches (cityscape, brick
// tiles, ball sprite, drop pills) are built lazily on first use and only when
// a real document is available; without one the renderer falls back to
// drawing shapes inline on the passed context, so even a recording-stub
// context can exercise render().

import { LEGEND } from './levels.js';
import { POWERS } from './engine.js';

const TAU = Math.PI * 2;
const DEFAULT_W = 480;
const DEFAULT_H = 880;
const FONT = 'system-ui, -apple-system, "Segoe UI", Roboto, sans-serif';
const MULT_COLORS = ['#ffffff', '#7ef0a6', '#66f2ff', '#7aa5ff', '#c77dff', '#ff6ff2'];
const HUD2_COLORS = { laser: '#40c4ff', slow: '#b388ff', fire: '#ff6e40', wide: '#69f0ae', shield: '#80d8ff' };

// ---------------------------------------------------------------------------
// Pure helpers (no DOM anywhere down this path).
// ---------------------------------------------------------------------------

function hexToRgb(hex) {
  const s = String(hex || '#888888').replace('#', '');
  const full = s.length === 3 ? s.split('').map((c) => c + c).join('') : s;
  const n = parseInt(full.length >= 6 ? full.slice(0, 6) : '888888', 16);
  return { r: (n >> 16) & 255, g: (n >> 8) & 255, b: n & 255 };
}

// Lighten (f > 1) or darken (f < 1) a hex colour -> 'rgb()' string.
function shade(hex, f) {
  const m = hexToRgb(hex);
  const c = (v) => {
    const t = f > 1 ? v + (255 - v) * (f - 1) : v * f;
    return Math.max(0, Math.min(255, Math.round(t)));
  };
  return 'rgb(' + c(m.r) + ',' + c(m.g) + ',' + c(m.b) + ')';
}

function rgba(hex, a) {
  const m = hexToRgb(hex);
  return 'rgba(' + m.r + ',' + m.g + ',' + m.b + ',' + a + ')';
}

function clamp(v, a, b) { return v < a ? a : v > b ? b : v; }
function clamp01(v) { return v < 0 ? 0 : v > 1 ? 1 : v; }

// Deterministic PRNG — keeps cityscape and twinkle arrays stable across frames.
function mulberry32(seed) {
  let a = seed >>> 0;
  return function () {
    a |= 0; a = (a + 0x6D2B79F5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// Create an offscreen canvas, or null when there is no DOM (node tests).
// A `doc` may be injected (e.g. renderer opts, or a fake in tests) so the
// pre-render caching path is exercisable outside a browser.
function makeCanvas(w, h, doc) {
  const d = doc || (typeof document !== 'undefined' ? document : null);
  if (!d || typeof d.createElement !== 'function') return null;
  try {
    const c = d.createElement('canvas');
    c.width = Math.max(1, Math.floor(w));
    c.height = Math.max(1, Math.floor(h));
    return c;
  } catch (e) {
    return null;
  }
}

// Rounded-rect path via arcTo — works on any 2D-like context.
function roundRectPath(ctx, x, y, w, h, r) {
  const rr = Math.max(0, Math.min(r, w / 2, h / 2));
  ctx.beginPath();
  ctx.moveTo(x + rr, y);
  ctx.arcTo(x + w, y, x + w, y + h, rr);
  ctx.arcTo(x + w, y + h, x, y + h, rr);
  ctx.arcTo(x, y + h, x, y, rr);
  ctx.arcTo(x, y, x + w, y, rr);
  ctx.closePath();
}

// Gradient helpers return the gradient object or null when the context does
// not implement them (recording stubs), so callers fall back to a flat colour.
function linGrad(ctx, x0, y0, x1, y1, stops) {
  try {
    const g = ctx.createLinearGradient(x0, y0, x1, y1);
    for (let i = 0; i < stops.length; i++) g.addColorStop(stops[i][0], stops[i][1]);
    return g;
  } catch (e) {
    return null;
  }
}

function radGrad(ctx, x0, y0, r0, x1, y1, r1, stops) {
  try {
    const g = ctx.createRadialGradient(x0, y0, r0, x1, y1, r1);
    for (let i = 0; i < stops.length; i++) g.addColorStop(stops[i][0], stops[i][1]);
    return g;
  } catch (e) {
    return null;
  }
}

// Paint one skyline layer into an offscreen canvas (pixel space). The tile is
// exactly one 480-world-unit period wide (supersampled: S = px_width/480).
// Buildings are rectangles with tiny lit windows, generated deterministically
// from `seed`. Generation starts at x=0 and stops once x >= 480, so the last
// building is naturally clipped at the right edge: a flat building wall looks
// identical to a normal building boundary, which keeps the wrap between two
// tiled drawImage calls effectively seamless.
function paintCity(c, worldH, seed, base, winColors, winGlow, winPx) {
  const g = c.getContext('2d');
  if (!g) return;
  const S = c.width / 480;                 // supersample factor (world -> px)
  const W = 480;
  const rng = mulberry32(seed);
  const groundH = 5;
  // ground band across the full tile so tiles tile without gaps
  g.fillStyle = base;
  g.fillRect(0, c.height - groundH * S, c.width, groundH * S);
  let x = 0;
  while (x < W) {
    const bw = 16 + rng() * 34;            // building width in world units
    const bh = (0.14 + rng() * 0.82) * worldH;
    const topY = worldH - bh;
    g.fillStyle = base;
    g.fillRect(x * S, c.height - bh * S, bw * S, bh * S + groundH * S);
    // occasional roof antenna
    if (rng() > 0.55) {
      const ax = x + bw * (0.2 + rng() * 0.6);
      const ah = 3 + rng() * 9;
      g.fillRect(ax * S, c.height - bh * S - ah * S, 1 * S, ah * S);
    }
    // tiny lit windows
    const cols = Math.max(1, Math.floor(bw / 4.4));
    const rows = Math.max(1, Math.floor(bh / 10));
    for (let ci = 0; ci < cols; ci++) {
      for (let ri = 0; ri < rows; ri++) {
        if (rng() >= winGlow) continue;
        const wx = x + 1.6 + ci * ((bw - 3.2) / cols);
        const wy = topY + 2.4 + ri * ((bh - 4.8) / rows);
        g.fillStyle = winColors[(rng() * winColors.length) | 0];
        g.fillRect(wx * S, wy * S, winPx * S, winPx * S);
      }
    }
    x += bw + 1.5 + rng() * 3.5;
  }
}

// ---------------------------------------------------------------------------
// Brick tile pre-rendering (pixel space, supersampled 2x for crispness).
// ---------------------------------------------------------------------------

// Paint one brick tile onto its offscreen canvas, in pixel space. `W`/`H` are
// the pixel size of the tile; `RR` is the corner radius in pixels.
function paintBrick(g, type, W, H) {
  const color = (LEGEND[type] && LEGEND[type].color) || '#4fc3f7';
  const R = Math.min(W, H) * 0.22;
  const rr = () => roundRectPath(g, 0.5, 0.5, W - 1, H - 1, R);
  g.clearRect(0, 0, W, H);

  // base fill: vertical gradient, metallic gold for 'g'
  let fill;
  if (type === 'g') {
    fill = linGrad(g, 0, 0, 0, H, [
      [0, '#fff3b0'], [0.18, '#ffd700'], [0.55, '#e6b800'], [1, '#8a6d00'],
    ]);
  } else {
    fill = linGrad(g, 0, 0, 0, H, [
      [0, shade(color, 1.45)], [0.25, shade(color, 1.12)], [0.7, color], [1, shade(color, 0.5)],
    ]);
  }
  g.fillStyle = fill || color;
  rr();
  g.fill();

  // clipped details: glossy sheen, side shading, top bevel, bottom edge
  g.save();
  rr();
  g.clip();
  const sheen = linGrad(g, 0, 0, W, H, [
    [0, 'rgba(255,255,255,0.20)'], [0.35, 'rgba(255,255,255,0.06)'], [0.52, 'rgba(255,255,255,0)'],
  ]);
  if (sheen) { g.fillStyle = sheen; g.fillRect(0, 0, W, H); }
  const side = linGrad(g, 0, 0, W, 0, [
    [0, 'rgba(0,0,0,0.26)'], [0.14, 'rgba(0,0,0,0)'], [0.86, 'rgba(0,0,0,0)'], [1, 'rgba(0,0,0,0.30)'],
  ]);
  if (side) { g.fillStyle = side; g.fillRect(0, 0, W, H); }
  g.fillStyle = 'rgba(255,255,255,0.50)';
  g.fillRect(R * 0.5, 0.5, W - 1 - R, Math.max(1.6, H * 0.07));   // top bevel
  g.fillStyle = 'rgba(0,0,0,0.30)';
  g.fillRect(R * 0.5, H - Math.max(1.8, H * 0.09), W - 1 - R, Math.max(1.8, H * 0.09)); // bottom edge
  g.restore();

  // subtle type icons so players can read the gated bricks
  if (type === 'a' || type === 's' || type === 't') {
    g.save();
    g.lineCap = 'round';
    g.lineJoin = 'round';
    g.strokeStyle = 'rgba(255,255,255,0.75)';
    g.lineWidth = Math.max(1.4, W * 0.055);
    const cx = W / 2, cy = H / 2, s = Math.min(W, H);
    if (type === 'a') {
      // corner arrow pointing up-right: enter steeply from above
      const a = s * 0.30;
      g.beginPath();
      g.moveTo(cx - a, cy - a * 0.4);
      g.lineTo(cx + a, cy - a * 0.4);
      g.lineTo(cx + a, cy + a * 0.7);
      g.stroke();
      g.beginPath();
      g.moveTo(cx - a * 0.35, cy + a);
      g.lineTo(cx + a, cy - a * 0.35);
      g.lineTo(cx + a, cy + a * 0.7);
      g.stroke();
    } else if (type === 's') {
      // lightning-ish slash
      g.beginPath();
      g.moveTo(cx + s * 0.22, cy - s * 0.30);
      g.lineTo(cx - s * 0.08, cy + s * 0.04);
      g.lineTo(cx + s * 0.10, cy + s * 0.04);
      g.lineTo(cx - s * 0.22, cy + s * 0.30);
      g.stroke();
    } else if (type === 't') {
      // up arrow (hit from above)
      const a = s * 0.26;
      g.beginPath();
      g.moveTo(cx, cy - a);
      g.lineTo(cx + a, cy);
      g.lineTo(cx + a * 0.42, cy);
      g.lineTo(cx + a * 0.42, cy + a);
      g.lineTo(cx - a * 0.42, cy + a);
      g.lineTo(cx - a * 0.42, cy);
      g.lineTo(cx - a, cy);
      g.closePath();
      g.stroke();
    }
    g.restore();
  }

  // gold sparkles are drawn live (they twinkle); reborn cracks are drawn live
  // (they change with hits), so neither is baked into the cached tile.
}

// Get (lazily, cached) the offscreen tile for a brick type at a given pixel
// size. Returns null when no DOM is available; callers then draw inline.
function brickTileFor(renderer, type, pw, ph) {
  const cache = renderer._brickCache;
  const size = pw + 'x' + ph;
  let byType = cache[type];
  if (!byType) { byType = cache[type] = {}; }
  if (byType[size]) return byType[size];
  const c = makeCanvas(pw, ph, renderer._doc);
  if (!c) return null;
  paintBrick(c.getContext('2d'), type, pw, ph);
  byType[size] = c;
  return c;
}

// Draw a single brick in world units, handling per-type live decoration.
function drawBrick(renderer, ctx, bk, time) {
  const type = bk.type;
  const color = (LEGEND[type] && LEGEND[type].color) || '#4fc3f7';
  const b = renderer._brickBleed;      // world padding around the tile
  const x = bk.x - b, y = bk.y - b, w = bk.w + b * 2, h = bk.h + b * 2;
  const pw = Math.max(1, Math.round(renderer._pxScale * w));
  const ph = Math.max(1, Math.round(renderer._pxScale * h));
  const tile = brickTileFor(renderer, type, pw, ph);

  if (tile) {
    ctx.drawImage(tile, x, y, w, h);
  } else {
    // node/stub fallback: draw a simple coloured rounded rect inline
    ctx.save();
    roundRectPath(ctx, x, y, w, h, Math.min(w, h) * 0.2);
    ctx.fillStyle = type === 'g' ? '#ffd700' : color;
    ctx.fill();
    ctx.restore();
  }

  // --- live per-type decoration ---------------------------------------------
  if (type === 'g') {
    // twinkling sparkles — 3 deterministic positions per brick
    const seeds = [11, 47, 83];
    for (let i = 0; i < 3; i++) {
      const tw = 0.5 + 0.5 * Math.sin(time * 2.2 + bk.row * 3.1 + bk.col * 7.7 + i * 2.1);
      const sx = x + w * (0.22 + 0.28 * ((seeds[i] % 10) / 10));
      const sy = y + h * (0.2 + 0.5 * ((seeds[i] * 7 % 10) / 10));
      drawSparkle(ctx, sx, sy, (3 + 2.4 * tw) * clamp01(tw + 0.35), Math.min(1, tw * 1.2), '#fff8e1');
    }
  } else if (type === 'r') {
    // cracks for landed hits
    if (bk.hits >= 1) {
      ctx.save();
      ctx.strokeStyle = 'rgba(10,20,35,0.85)';
      ctx.lineWidth = Math.max(1, h * 0.07);
      ctx.lineCap = 'round';
      const cx = x + w / 2, cy = y + h / 2;
      ctx.beginPath();
      if (bk.hits >= 2) {
        ctx.moveTo(cx - w * 0.26, cy - h * 0.2); ctx.lineTo(cx - w * 0.02, cy + h * 0.05); ctx.lineTo(cx - w * 0.20, cy + h * 0.28);
        ctx.moveTo(cx + w * 0.26, cy - h * 0.22); ctx.lineTo(cx + w * 0.02, cy + h * 0.02); ctx.lineTo(cx + w * 0.18, cy + h * 0.30);
        ctx.moveTo(cx - w * 0.06, cy + h * 0.02); ctx.lineTo(cx + w * 0.16, cy - h * 0.24);
      } else {
        ctx.moveTo(cx - w * 0.22, cy - h * 0.16); ctx.lineTo(cx + w * 0.05, cy + h * 0.08); ctx.lineTo(cx - w * 0.04, cy + h * 0.30);
        ctx.moveTo(cx + w * 0.24, cy - h * 0.14); ctx.lineTo(cx + w * 0.06, cy + h * 0.10);
      }
      ctx.stroke();
      ctx.restore();
    }
    // pulsing regrow rings
    if (bk.regrow > 0 && bk.regrow < 0.5) {
      const t = 1 - bk.regrow / 0.5;   // 0 -> 1 as the brick comes back
      ctx.save();
      ctx.globalCompositeOperation = 'lighter';
      const pulse = (time * 7 + bk.row * 1.3 + bk.col * 0.7) % 1;
      const ar = 0.45 + 0.55 * t;
      ctx.strokeStyle = rgba(color, 0.7 * ar);
      ctx.lineWidth = 1.6 + 1.6 * pulse;
      ctx.beginPath();
      ctx.arc(x + w / 2, y + h / 2, (Math.min(w, h) / 2) * (0.65 + 0.5 * pulse), 0, TAU);
      ctx.stroke();
      ctx.globalCompositeOperation = 'source-over';
      ctx.restore();
    }
  } else if (type === 'b') {
    // bomb: little fuse spark dot on top
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    const flick = 0.6 + 0.4 * Math.sin(time * 13 + bk.col * 5);
    ctx.fillStyle = rgba('#ffd166', 0.8 * flick);
    ctx.beginPath();
    ctx.arc(x + w / 2, y + 2.2, 1.6, 0, TAU);
    ctx.fill();
    ctx.restore();
  }

  // additive glow overlay when the brick was just rejected / hit
  if (bk.flash > 0) {
    const a = clamp01(bk.flash / 0.18);
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    ctx.fillStyle = rgba(color, 0.55 * a);
    roundRectPath(ctx, x, y, w, h, Math.min(w, h) * 0.2);
    ctx.fill();
    // white-hot core
    ctx.fillStyle = 'rgba(255,255,255,' + (0.5 * a) + ')';
    roundRectPath(ctx, x + w * 0.18, y + h * 0.18, w * 0.64, h * 0.64, Math.min(w, h) * 0.14);
    ctx.fill();
    ctx.restore();
  }
}


// Guarded text helper: only draws when the context implements fillText.
function txt(ctx, str, x, y) {
  if (typeof ctx.fillText !== 'function') return;
  ctx.fillText(String(str), x, y);
}

// Four-point sparkle star (additive caller sets composite).
function drawSparkle(ctx, x, y, size, alpha, color) {
  if (size <= 0.2 || alpha <= 0.01) return;
  ctx.save();
  ctx.globalCompositeOperation = 'lighter';
  ctx.globalAlpha = clamp01(alpha);
  ctx.fillStyle = color || '#ffffff';
  ctx.beginPath();
  for (let i = 0; i < 8; i++) {
    const ang = (i * Math.PI) / 4;
    const r = i % 2 === 0 ? size : size * 0.22;
    const px = x + Math.cos(ang) * r;
    const py = y + Math.sin(ang) * r;
    if (i === 0) ctx.moveTo(px, py); else ctx.lineTo(px, py);
  }
  ctx.closePath();
  ctx.fill();
  ctx.restore();
}

// ---------------------------------------------------------------------------
// Entities: paddle, balls, lasers, drops.
// ---------------------------------------------------------------------------

// Rounded capsule paddle with a neon edge that lights up while powers are on;
// shield bubble and laser barrels are layered on top when active.
function drawPaddle(ctx, p, time) {
  if (!p) return;
  const x = p.x, y = p.y, w = p.w || 84, h = p.h || 14;
  const r = Math.min(h / 2, 7);
  const glow = (p.laser > 0 || p.wideTimer > 0 || (p.shield > 0 ? p.shield : 0) > 0) ? 1 : 0;

  // soft ambient glow underneath (additive, no shadowBlur)
  ctx.save();
  ctx.globalCompositeOperation = 'lighter';
  ctx.globalAlpha = 0.16 + 0.2 * glow;
  const g2 = radGrad(ctx, x, y, 2, x, y, w * 0.7, [
    [0, 'rgba(120,220,255,0.8)'], [1, 'rgba(120,220,255,0)'],
  ]);
  if (g2) { ctx.fillStyle = g2; ctx.beginPath(); ctx.arc(x, y, w * 0.7, 0, TAU); ctx.fill(); }
  ctx.restore();

  // body
  ctx.save();
  roundRectPath(ctx, x - w / 2, y - h / 2, w, h, r);
  const body = linGrad(ctx, x, y - h / 2, x, y + h / 2, [
    [0, '#2b3350'], [0.5, '#171c33'], [1, '#0c0f1f'],
  ]);
  ctx.fillStyle = body || '#171c33';
  ctx.fill();
  // neon edging (top + bottom) that brightens with glow
  ctx.strokeStyle = glow > 0
    ? 'rgba(120,220,255,' + (0.85 + 0.15 * Math.sin(time * 8)) + ')'
    : 'rgba(120,220,255,0.35)';
  ctx.lineWidth = 1.6;
  ctx.stroke();
  // glossy centre line
  const sheen = linGrad(ctx, x, y - h / 2, x, y - h / 2 + h * 0.16, [
    [0, 'rgba(255,255,255,0.25)'], [1, 'rgba(255,255,255,0)'],
  ]);
  if (sheen) { ctx.fillStyle = sheen; roundRectPath(ctx, x - w * 0.4, y - h / 2 + 1, w * 0.8, h * 0.16, 2); ctx.fill(); }
  // catch-point pips
  ctx.fillStyle = 'rgba(255,255,255,0.12)';
  for (const fx of [-0.5, 0, 0.5]) ctx.fillRect(x + fx * w * 0.25 - 1, y - 3.2, 2, 2);
  ctx.restore();

  // laser barrels at both ends
  if (p.laser > 0) {
    ctx.save();
    ctx.fillStyle = '#9adcff';
    for (const dir of [-1, 1]) {
      const bx = x + dir * w * 0.30 - 3;
      roundRectPath(ctx, bx, y - h / 2 - 5, 6, 8, 2);
      ctx.fill();
      ctx.fillStyle = 'rgba(64,196,255,0.85)';
      ctx.fillRect(bx + 1.5, y - h / 2 - 8, 3, 4);
      ctx.fillStyle = '#9adcff';
    }
    ctx.restore();
  }

  // shield bubble arc
  if (p.shield > 0) {
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    for (let i = 0; i < p.shield; i++) {
      const a = (time * 2.4 + i * 2.1) % TAU;
      const bcx = x + Math.cos(a) * w * 0.28;
      const bcy = y + Math.sin(a) * w * 0.28;
      ctx.globalAlpha = 0.35 + 0.2 * Math.sin(time * 5 + i);
      ctx.strokeStyle = '#80d8ff';
      ctx.lineWidth = 1.6;
      ctx.beginPath();
      ctx.arc(bcx, bcy, 8, 0, TAU);
      ctx.stroke();
    }
    ctx.globalAlpha = 0.10;
    const sh = radGrad(ctx, x, y, 2, x, y, w * 0.62, [
      [0, 'rgba(128,216,255,0.5)'], [1, 'rgba(128,216,255,0)'],
    ]);
    if (sh) { ctx.fillStyle = sh; ctx.beginPath(); ctx.arc(x, y, w * 0.62, 0, TAU); ctx.fill(); }
    ctx.restore();
  }

  // widescreen pulse when the wide timer is about to expire
  if (p.wideTimer > 0 && p.wideTimer < 1) {
    ctx.save();
    ctx.globalAlpha = 0.25 * (1 - p.wideTimer);
    ctx.strokeStyle = '#69f0ae';
    ctx.lineWidth = 2;
    roundRectPath(ctx, x - w / 2 - 3, y - h / 2 - 3, w + 6, h + 6, r + 3);
    ctx.stroke();
    ctx.restore();
  }
}

// Radial-gradient sphere with a velocity-based motion stretch; fireballs get
// an additive orange/yellow flame trail.
function drawBall(ctx, b, time) {
  if (!b) return;
  const r = b.r || 7;
  const speed = Math.hypot(b.vx || 0, b.vy || 0);
  const stretch = clamp(1 + speed / 2500, 1, 1.25);
  const ang = Math.atan2(b.vy || -1, b.vx || 0) + Math.PI / 2;

  // flame trail (additive) — longest when moving fast
  if (b.fire) {
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    const n = 4 + ((speed / 220) | 0);
    for (let i = 1; i <= n; i++) {
      const t = i / n;
      // trail dots laid out along the -velocity direction
      const bx = b.x - (b.vx * 0.014) * t * 3;
      const by = b.y - (b.vy * 0.014) * t * 3;
      ctx.globalAlpha = 0.5 * (1 - t);
      ctx.fillStyle = b.fire === 'gold' ? '#ffd54f' : (t < 0.5 ? '#ff6e40' : '#ffb300');
      ctx.beginPath();
      ctx.arc(bx, by, r * (1 - 0.55 * t), 0, TAU);
      ctx.fill();
    }
    ctx.restore();
  }

  // body (radial gradient)
  ctx.save();
  ctx.translate(b.x, b.y);
  ctx.rotate(ang);
  ctx.scale(stretch, 1 / stretch);
  const grad = radGrad(ctx, -r * 0.35, -r * 0.35, r * 0.1, 0, 0, r, [
    [0, b.fire ? '#ffe0b2' : '#ffffff'], [0.45, b.fire ? '#ff9d47' : '#cfe8ff'], [1, b.fire ? '#ff5722' : '#4a7fbf'],
  ]);
  ctx.fillStyle = grad || '#ffffff';
  ctx.beginPath();
  ctx.arc(0, 0, r, 0, TAU);
  ctx.fill();
  // subtle rim light
  ctx.strokeStyle = b.fire ? 'rgba(255,180,80,0.9)' : 'rgba(180,220,255,0.6)';
  ctx.lineWidth = 1;
  ctx.stroke();
  ctx.restore();

  // additive core glint
  ctx.save();
  ctx.globalCompositeOperation = 'lighter';
  ctx.globalAlpha = 0.25 + 0.15 * Math.sin(time * 9);
  const gl = radGrad(ctx, b.x, b.y, 0.5, b.x, b.y, r * 1.6, [
    [0, 'rgba(255,255,255,0.9)'], [1, 'rgba(255,255,255,0)'],
  ]);
  if (gl) { ctx.fillStyle = gl; ctx.beginPath(); ctx.arc(b.x, b.y, r * 1.6, 0, TAU); ctx.fill(); }
  ctx.restore();
}

// Vertical laser bolts with an additive glow core.
function drawLaser(ctx, l) {
  if (!l) return;
  const x = l.x, y = l.y, w = l.w || 3.5, h = l.h || 16;
  ctx.save();
  ctx.globalCompositeOperation = 'lighter';
  // halo
  const halo = radGrad(ctx, x, y + h / 2, 1, x, y + h / 2, w * 3.4, [
    [0, 'rgba(64,196,255,0.55)'], [1, 'rgba(64,196,255,0)'],
  ]);
  if (halo) { ctx.fillStyle = halo; ctx.fillRect(x - w * 3.4, y, w * 6.8, h); }
  // beam
  ctx.fillStyle = 'rgba(220,246,255,0.95)';
  ctx.fillRect(x - w / 2, y, w, h);
  ctx.fillStyle = 'rgba(64,196,255,0.9)';
  ctx.fillRect(x - w * 0.14, y, w * 0.28, h);
  ctx.restore();
}

// Falling power-up capsule: rounded pill with a dark border, icon + colour
// from POWERS, a gentle sine bob and a pulsing additive ring.
function drawDrop(ctx, d, time) {
  if (!d) return;
  const def = POWERS[d.type] || { icon: '?', color: '#ffffff' };
  const hw = 13, hh = 6.5;
  const bob = Math.sin(time * 6 + d.x * 0.05) * 1.6;
  const cy = d.y + bob;

  // pulsing ring
  const pr = ((time * 1.6 + d.x * 0.11) % 1);
  ctx.save();
  ctx.globalCompositeOperation = 'lighter';
  ctx.globalAlpha = 0.5 * (1 - pr);
  ctx.strokeStyle = def.color;
  ctx.lineWidth = 1.4;
  ctx.beginPath();
  ctx.arc(d.x, cy, 12 + pr * 14, 0, TAU);
  ctx.stroke();
  ctx.restore();

  // pill body
  ctx.save();
  roundRectPath(ctx, d.x - hw, cy - hh, hw * 2, hh * 2, hh);
  const body = linGrad(ctx, d.x - hw, cy - hh, d.x + hw, cy + hh, [
    [0, shade(def.color, 1.55)], [0.5, def.color], [1, shade(def.color, 0.42)],
  ]);
  ctx.fillStyle = body || def.color;
  ctx.fill();
  ctx.strokeStyle = 'rgba(8,10,22,0.9)';
  ctx.lineWidth = 1.5;
  ctx.stroke();
  // glossy top
  ctx.globalAlpha = 0.55;
  ctx.fillStyle = 'rgba(255,255,255,0.5)';
  roundRectPath(ctx, d.x - hw + 2, cy - hh + 1.2, hw * 2 - 4, 2.4, 1.2);
  ctx.fill();
  ctx.globalAlpha = 1;
  // icon char
  ctx.fillStyle = 'rgba(255,255,255,0.96)';
  ctx.font = 'bold 9px ' + FONT;
  ctx.textAlign = 'center';
  ctx.textBaseline = 'middle';
  txt(ctx, def.icon, d.x, cy + 0.5);
  ctx.restore();
}

// ---------------------------------------------------------------------------
// Background: sky gradient, parallax cityscape, twinkling dots, light beams,
// horizon glow line. The two city layers are pre-rendered once into offscreen
// canvases (wider than the world so they can scroll without seams) and drawn
// with ~2 drawImage calls per frame.
// ---------------------------------------------------------------------------

function ensureBackground(renderer) {
  if (renderer._bg) return renderer._bg;
  const bg = {};
  // far skyline (small, dark blue, few windows)
  const far = makeCanvas(480 * 2, 880 * 2, renderer._doc);
  if (far) {
    paintCity(far, 880, 1337, '#131a38', ['#5f7fb8', '#7d9bd0', '#4a6ba0'], 0.30, 1.1);
    // atmospheric fade at the bottom so it melts into the sky
    const g = far.getContext('2d');
    if (g) {
      const fade = linGrad(g, 0, 880 * 0.55, 0, 880, [
        [0, 'rgba(13,15,46,0)'], [1, 'rgba(13,15,46,0.55)'],
      ]);
      if (fade) { g.fillStyle = fade; g.fillRect(0, 0, 576, 880); }
    }
    bg.far = far;
  }
  // near skyline (bigger, darker, more windows), anchored lower
  const near = makeCanvas(480 * 2, 880 * 2, renderer._doc);
  if (near) {
    paintCity(near, 880, 8001, '#0a0e22', ['#7dd3a8', '#9fd8b8', '#5fbf96', '#e8c07a'], 0.5, 1.3);
    const g = near.getContext('2d');
    if (g) {
      const fade = linGrad(g, 0, 880 * 0.5, 0, 880, [
        [0, 'rgba(13,15,46,0)'], [1, 'rgba(13,15,46,0.6)'],
      ]);
      if (fade) { g.fillStyle = fade; g.fillRect(0, 0, 576, 880); }
    }
    bg.near = near;
  }
  // deterministic twinkle dots + falling streaks (fixed-order array)
  const dots = [];
  const rng = mulberry32(20240);
  for (let i = 0; i < 40; i++) {
    dots.push({
      x: rng() * 480,
      y: rng() * 880,
      r: 0.4 + rng() * 1.2,
      ph: rng() * TAU,
      sp: 0.8 + rng() * 2.4,
      streak: rng() < 0.22,
      fall: 18 + rng() * 46,
    });
  }
  bg.dots = dots;
  renderer._bg = bg;
  return bg;
}

function drawBackground(renderer, ctx, layers, time) {
  const W = renderer.worldW, H = renderer.worldH;
  const bg = ensureBackground(renderer);
  const xt = (layers.bg && typeof layers.bg.xt === 'number') ? layers.bg.xt : 0;

  // sky gradient (cheap: one cached gradient object, recreated per resize)
  let sky = renderer._skyGrad;
  if (!sky || renderer._skyResized) {
    sky = linGrad(ctx, 0, 0, 0, H, [
      [0, '#0b1026'], [0.5, '#1b1247'], [1, '#0d0f2e'],
    ]);
    renderer._skyGrad = sky || '#0b1026';
    renderer._skyResized = false;
  }
  ctx.fillStyle = sky;
  ctx.fillRect(0, 0, W, H);

  // --- cityscape (parallax scroll) ------------------------------------------
  // Each offscreen tile is exactly one 480-world-unit period; draw two copies
  // side by side so the panorama scrolls without gaps.
  if (bg.far) {
    const cw = W;                                  // world width of one tile
    const off = -((xt * 0.18) % cw);               // negative offset scrolls left
    ctx.drawImage(bg.far, off, 0, cw, H);
    ctx.drawImage(bg.far, off + cw, 0, cw, H);
  }
  if (bg.near) {
    const cw = W;
    const off = -((xt * 0.42) % cw);
    ctx.drawImage(bg.near, off, 0, cw, H);
    ctx.drawImage(bg.near, off + cw, 0, cw, H);
  }

  // --- vertical light beams sweeping (very low alpha) ------------------------
  ctx.save();
  ctx.globalCompositeOperation = 'lighter';
  for (let i = 0; i < 3; i++) {
    const bx = ((xt * (0.6 + i * 0.2) + i * 160) % (W + 200)) - 100;
    const bw = 26 + i * 10;
    const grad = linGrad(ctx, bx, 0, bx + bw, 0, [
      [0, 'rgba(120,160,255,0)'], [0.5, 'rgba(120,160,255,' + (0.05 + i * 0.01) + ')'], [1, 'rgba(120,160,255,0)'],
    ]);
    if (grad) { ctx.fillStyle = grad; ctx.fillRect(bx, 0, bw, H); }
  }
  ctx.restore();

  // --- twinkling bokeh dots + falling data streaks ---------------------------
  const dots = bg.dots;
  ctx.save();
  ctx.globalCompositeOperation = 'lighter';
  for (let i = 0; i < dots.length; i++) {
    const d = dots[i];
    const tw = 0.5 + 0.5 * Math.sin(time * d.sp + d.ph);
    if (d.streak) {
      const sy = ((d.y + time * d.fall) % (H + 60)) - 30;
      ctx.globalAlpha = 0.12 + 0.12 * tw;
      ctx.strokeStyle = 'rgba(150,190,255,0.9)';
      ctx.lineWidth = 1;
      ctx.beginPath();
      ctx.moveTo(d.x, sy);
      ctx.lineTo(d.x - 2.5, sy + 14);
      ctx.stroke();
    } else {
      ctx.globalAlpha = 0.10 + 0.20 * tw;
      ctx.fillStyle = 'rgba(190,210,255,0.9)';
      ctx.beginPath();
      ctx.arc(d.x, d.y, d.r * (0.7 + 0.5 * tw), 0, TAU);
      ctx.fill();
    }
  }
  ctx.restore();

  // --- horizon glow line near the bottom -------------------------------------
  ctx.save();
  ctx.globalCompositeOperation = 'lighter';
  const hg = linGrad(ctx, 0, H - 120, 0, H, [
    [0, 'rgba(120,90,255,0)'], [0.5, 'rgba(120,90,255,0.10)'], [1, 'rgba(120,90,255,0.22)'],
  ]);
  if (hg) { ctx.fillStyle = hg; ctx.fillRect(0, H - 120, W, 120); }
  ctx.globalAlpha = 0.5;
  ctx.fillStyle = 'rgba(150,130,255,0.5)';
  ctx.fillRect(0, H - 1.5, W, 1.5);
  ctx.restore();

  // --- top warm glow when the rooftop is active -------------------------------
  if (layers.roof && layers.roof.active) {
    const pulse = 0.5 + 0.5 * Math.sin(time * 3.2);
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    const tg = radGrad(ctx, W / 2, roofTopYOf(renderer, layers), 10, W / 2, roofTopYOf(renderer, layers), 320, [
      [0, 'rgba(255,190,90,' + (0.16 + 0.08 * pulse) + ')'], [1, 'rgba(255,190,90,0)'],
    ]);
    if (tg) { ctx.fillStyle = tg; ctx.fillRect(0, 0, W, roofTopYOf(renderer, layers) + 300); }
    ctx.restore();
  }
}

function roofTopYOf(renderer, layers) {
  const r = layers.roof;
  if (r && typeof r.topY === 'number') return r.topY;
  return 132;
}

// ---------------------------------------------------------------------------
// Roof line: glowing dashed line at the top of the bricks when rooftop play is
// active, with animated dashes flowing sideways.
// ---------------------------------------------------------------------------

function drawRoof(ctx, roof, time, W) {
  if (!roof || !roof.active) return;
  const topY = typeof roof.topY === 'number' ? roof.topY : 132;
  ctx.save();
  // soft band
  ctx.globalCompositeOperation = 'lighter';
  const band = linGrad(ctx, 0, topY - 14, 0, topY + 14, [
    [0, 'rgba(255,200,110,0)'], [0.5, 'rgba(255,200,110,0.16)'], [1, 'rgba(255,200,110,0)'],
  ]);
  if (band) { ctx.fillStyle = band; ctx.fillRect(0, topY - 14, W, 28); }
  // animated dashes
  const dashOff = (time * 90) % 30;
  ctx.strokeStyle = 'rgba(255,215,130,0.85)';
  ctx.lineWidth = 2;
  if (typeof ctx.setLineDash === 'function') ctx.setLineDash([16, 14]);
  ctx.lineDashOffset = -dashOff;
  ctx.beginPath();
  ctx.moveTo(0, topY);
  ctx.lineTo(W, topY);
  ctx.stroke();
  if (typeof ctx.setLineDash === 'function') ctx.setLineDash([]);
  // bright core under the dashes
  ctx.globalAlpha = 0.5 + 0.25 * Math.sin(time * 5);
  ctx.strokeStyle = 'rgba(255,245,220,0.9)';
  ctx.lineWidth = 1;
  ctx.beginPath();
  ctx.moveTo(0, topY + 1.2);
  ctx.lineTo(W, topY + 1.2);
  ctx.stroke();
  ctx.restore();
}

// ---------------------------------------------------------------------------
// Corner HUD bits (minimal, semi-transparent) + power indicator bar.
// ---------------------------------------------------------------------------

function drawCores(ctx, cores) {
  if (!cores) return;
  if (cores.mult > 1) {
    const color = MULT_COLORS[Math.min(MULT_COLORS.length - 1, (cores.mult || 1) - 1)];
    ctx.save();
    ctx.globalAlpha = 0.55;
    ctx.fillStyle = color;
    ctx.font = 'bold 21px ' + FONT;
    ctx.textAlign = 'left';
    ctx.textBaseline = 'top';
    txt(ctx, '×' + cores.mult, 14, 10);
    if (cores.combo > 0) {
      ctx.font = 'bold 11px ' + FONT;
      ctx.fillStyle = 'rgba(255,255,255,0.75)';
      txt(ctx, cores.combo + ' combo', 14, 36);
    }
    ctx.restore();
  } else if (cores.combo > 0) {
    ctx.save();
    ctx.globalAlpha = 0.4;
    ctx.fillStyle = '#ffffff';
    ctx.font = 'bold 11px ' + FONT;
    ctx.textAlign = 'left';
    ctx.textBaseline = 'top';
    txt(ctx, cores.combo + ' combo', 14, 12);
    ctx.restore();
  }
  if (cores.rooftopStreak > 0) {
    ctx.save();
    ctx.globalAlpha = 0.7;
    ctx.fillStyle = '#ffd166';
    ctx.font = 'bold 12px ' + FONT;
    ctx.textAlign = 'right';
    ctx.textBaseline = 'top';
    txt(ctx, 'roof ' + cores.rooftopStreak + 's', 466, 10);
    ctx.restore();
  }
  if (cores.bestStreak > 1) {
    ctx.save();
    ctx.globalAlpha = 0.45;
    ctx.fillStyle = 'rgba(255,255,255,0.8)';
    ctx.font = '10px ' + FONT;
    ctx.textAlign = 'right';
    ctx.textBaseline = 'top';
    txt(ctx, 'best ' + cores.bestStreak + 's', 466, 28);
    ctx.restore();
  }
}

// Active power indicators: small glowing chips under the paddle + shield pips.
function drawHud2(ctx, hud2) {
  if (!hud2) return;
  const chips = [];
  if (hud2.laser > 0) chips.push(['laser', hud2.laser, 10]);
  if (hud2.slow > 0) chips.push(['slow', hud2.slow, 8]);
  if (hud2.fire > 0) chips.push(['fire', hud2.fire, 8]);
  if (hud2.wide > 0) chips.push(['wide', hud2.wide, 14]);
  if (chips.length === 0) return;
  ctx.save();
  ctx.textAlign = 'center';
  ctx.textBaseline = 'middle';
  let x = 240 - (chips.length * 30) / 2;
  const y = 862;
  for (const [name, left] of chips) {
    const color = HUD2_COLORS[name] || '#ffffff';
    const frac = clamp01(left / (name === 'laser' ? 10 : name === 'slow' ? 8 : name === 'fire' ? 8 : 14));
    ctx.globalAlpha = 0.9;
    roundRectPath(ctx, x - 13, y - 6.5, 26, 13, 4);
    ctx.fillStyle = 'rgba(8,10,24,0.65)';
    ctx.fill();
    ctx.strokeStyle = rgba(color, 0.9);
    ctx.lineWidth = 1.2;
    ctx.stroke();
    // depletion bar
    ctx.globalAlpha = 0.85;
    ctx.fillStyle = rgba(color, 0.85);
    const bw = 20 * frac;
    roundRectPath(ctx, x - 10, y + 2.5, bw, 2, 1);
    ctx.fill();
    ctx.globalAlpha = 1;
    ctx.fillStyle = '#ffffff';
    ctx.font = 'bold 9px ' + FONT;
    txt(ctx, name.toUpperCase(), x, y - 1);
    x += 30;
  }
  ctx.restore();
}

// ---------------------------------------------------------------------------
// Vignette + flash overlays (last pass, drawn in screen space via world rect).
// ---------------------------------------------------------------------------

function drawVignette(ctx, W, H, intensity) {
  const s = clamp01(intensity || 0);
  if (s <= 0.01) return;
  ctx.save();
  const g = radGrad(ctx, W / 2, H / 2, Math.min(W, H) * 0.36, W / 2, H / 2, Math.max(W, H) * 0.78, [
    [0, 'rgba(6,8,22,0)'], [0.62, 'rgba(6,8,22,0)'], [1, 'rgba(4,5,16,' + (0.5 * s + 0.12) + ')'],
  ]);
  if (g) { ctx.fillStyle = g; ctx.fillRect(0, 0, W, H); }
  ctx.restore();
}

function drawFlashingOverlays(ctx, W, H, state) {
  if (!state) return;
  if (state.flashWhite > 0) {
    ctx.save();
    ctx.fillStyle = 'rgba(255,255,255,' + clamp01(state.flashWhite) + ')';
    ctx.fillRect(0, 0, W, H);
    ctx.restore();
  }
  if (state.flashColor && state.flashColor.a > 0) {
    ctx.save();
    ctx.fillStyle = state.flashColor.color || 'rgba(255,255,255,0.5)';
    ctx.globalAlpha = clamp01(state.flashColor.a);
    ctx.fillRect(0, 0, W, H);
    ctx.restore();
  }
}

// ---------------------------------------------------------------------------
// Renderer — public API: constructor(canvas, opts), resize(), render(state,
// layers), plus getters ctx / worldW / worldH.
// ---------------------------------------------------------------------------

export class Renderer {
  constructor(canvas, opts = {}) {
    this.canvas = canvas || null;
    this.opts = opts;
    this._doc = (opts && opts.document) || null;
    this.worldW = (opts && opts.worldW) || DEFAULT_W;
    this.worldH = (opts && opts.worldH) || DEFAULT_H;
    // lazy caches (all built on first use, only when a DOM exists)
    this._bg = null;
    this._brickCache = Object.create(null);
    this._pxScale = 2;                 // supersample for offscreen tiles
    this._brickBleed = 2;              // world padding so glows don't clip
    this._skyGrad = null;
    this._skyResized = false;
    this._t = 0;
    // grab the 2d context defensively: accept any object with getContext()
    this._ctx = null;
    if (canvas && typeof canvas.getContext === 'function') {
      try {
        this._ctx = canvas.getContext('2d');
      } catch (e) {
        this._ctx = null;
      }
    }
  }

  get ctx() { return this._ctx; }
  get worldW() { return this._worldW; }
  set worldW(v) { this._worldW = v; }
  get worldH() { return this._worldH; }
  set worldH(v) { this._worldH = v; }

  // Resize the backing store to the container size * devicePixelRatio and
  // return {scale} (multiply world units by scale to get css px).
  resize() {
    const canvas = this.canvas;
    const px = (typeof devicePixelRatio === 'number' && devicePixelRatio > 0) ? devicePixelRatio : 1;
    // The canvas keeps the WORLD aspect ratio; the container scales it visually.
    // Whatever the parent's size, we fit the 480x880 world inside it, maintaining
    // 480:880 so coordinates are never distorted. (Fixes stretched playfield.)
    const worldRatio = this.worldW / this.worldH; // 480/880
    let cw = 0, ch = 0;
    if (canvas && canvas.parentNode && canvas.parentNode.clientWidth) {
      const pW = canvas.parentNode.clientWidth;
      const pH = canvas.parentNode.clientHeight;
      // fit world into parent, preserving aspect
      if (pH > 0 && pW / pH > worldRatio) {
        ch = pH; cw = pH * worldRatio;
      } else if (pW > 0) {
        cw = pW; ch = pW / worldRatio;
      }
    } else if (canvas && canvas.clientWidth) {
      cw = canvas.clientWidth;
      ch = canvas.clientHeight;
    } else if (canvas && canvas.width && canvas.height) {
      cw = canvas.width / px;
      ch = canvas.height / px;
    } else {
      cw = this.worldW;
      ch = this.worldH;
    }
    // clamp to world ratio as a sanity floor (never below the world itself)
    if (cw <= 0 || ch <= 0) { cw = this.worldW; ch = this.worldH; }
    const bw = Math.max(1, Math.round(cw * px));
    const bh = Math.max(1, Math.round(ch * px));
    if (canvas) {
      if (canvas.width !== bw) canvas.width = bw;
      if (canvas.height !== bh) canvas.height = bh;
      if (canvas.style) {
        canvas.style.width = cw + 'px';
        canvas.style.height = ch + 'px';
      }
    }
    this._bw = bw; this._bh = bh;
    this._pxScale = Math.max(1.5, Math.min(3, (bw / this.worldW) * 1.6));
    this._skyResized = true;
    return { scale: bw / this.worldW };
  }

  // Main entry: draw the whole frame in world units.
  render(state = {}, layers = {}) {
    const ctx = this._ctx;
    if (!ctx) return;
    if (!state) state = {};
    if (!layers) layers = {};
    const W = this.worldW, H = this.worldH;
    if (!(W > 0) || !(H > 0)) return;
    if (typeof ctx.save !== 'function') return;
    this._t = (state && typeof state.time === 'number') ? state.time : (this._t || 0);

    ctx.save();
    // world -> canvas scale; canvas pixels already include devicePixelRatio
    if (typeof ctx.scale === 'function' && this._bw > 0) {
      ctx.scale(this._bw / W, this._bw / W);
    }
    ctx.globalCompositeOperation = 'source-over';

    // draw order per spec
    drawBackground(this, ctx, layers, this._t);
    if (layers.bricks) this._drawBricks(ctx, layers.bricks);
    if (layers.drops) this._drawDrops(ctx, layers.drops);
    if (layers.balls) this._drawBalls(ctx, layers.balls);
    if (layers.lasers) this._drawLasers(ctx, layers.lasers);
    if (layers.paddle) drawPaddle(ctx, layers.paddle, this._t);
    if (layers.particles && typeof layers.particles.render === 'function') {
      try { layers.particles.render(ctx); } catch (e) { /* never let FX crash the frame */ }
    }
    if (layers.roof) drawRoof(ctx, layers.roof, this._t, W);
    if (layers.cores) drawCores(ctx, layers.cores);
    if (layers.hud2) drawHud2(ctx, layers.hud2);
    drawVignette(ctx, W, H, layers.vignette && layers.vignette.intensity);
    drawFlashingOverlays(ctx, W, H, state);
    ctx.restore();
  }

  // ---- per-layer renderers (world units) -----------------------------------

  _drawBricks(ctx, grid) {
    const t = this._t;
    for (let r = 0; r < grid.length; r++) {
      const row = grid[r];
      if (!row) continue;
      for (let c = 0; c < row.length; c++) {
        const bk = row[c];
        if (!bk || !bk.alive) continue;
        // skip bricks fully outside the viewport (shouldn't happen, but cheap)
        drawBrick(this, ctx, bk, t);
      }
    }
  }

  _drawDrops(ctx, drops) {
    const t = this._t;
    for (let i = 0; i < drops.length; i++) drawDrop(ctx, drops[i], t);
  }

  _drawBalls(ctx, balls) {
    const t = this._t;
    for (let i = 0; i < balls.length; i++) drawBall(ctx, balls[i], t);
  }

  _drawLasers(ctx, lasers) {
    for (let i = 0; i < lasers.length; i++) {
      const l = lasers[i];
      if (l.y < -40 || l.y > this.worldH + 40) continue; // offscreen skip
      drawLaser(ctx, l);
    }
  }
}
