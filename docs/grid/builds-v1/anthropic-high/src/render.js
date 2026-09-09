// render.js — all drawing. Logical space is 540x960; main.js sets up the transform.
//
// Performance rules of thumb used throughout:
//   * no shadowBlur in per-frame hot loops (pre-rendered glow sprites instead)
//   * gradients are cached by key, never rebuilt every frame in the field/HUD
//   * the nebula backdrop is one pre-rendered offscreen canvas + cheap additive tints
//   * particle counts / extra layers collapse when fx.lite is on

import { clamp, lerp, TAU, hsl, formatScore, rand, smoothstep } from './util.js';
import { W, H, FIELD, POWERUPS, MULT_CAP } from './game.js';
import fx, { glowSprite, qhue } from './fx.js';

const MONO = 'ui-monospace, "SF Mono", SFMono-Regular, Menlo, Consolas, monospace';
const DISPLAY = '"Trebuchet MS", "Avenir Next", Inter, system-ui, sans-serif';

// ---------------------------------------------------------------- tiny helpers

function rr(ctx, x, y, w, h, r) {
  const rad = Math.min(r, w / 2, h / 2);
  ctx.beginPath();
  if (ctx.roundRect) ctx.roundRect(x, y, w, h, rad);
  else {
    ctx.moveTo(x + rad, y);
    ctx.arcTo(x + w, y, x + w, y + h, rad);
    ctx.arcTo(x + w, y + h, x, y + h, rad);
    ctx.arcTo(x, y + h, x, y, rad);
    ctx.arcTo(x, y, x + w, y, rad);
    ctx.closePath();
  }
}

function offscreen(w, h) {
  if (typeof OffscreenCanvas !== 'undefined') {
    try { return new OffscreenCanvas(w, h); } catch (e) { /* fall through */ }
  }
  if (typeof document === 'undefined') return null;
  const c = document.createElement('canvas');
  c.width = w; c.height = h;
  return c;
}

// Gradients are expensive to rebuild; cache them by key for the life of the context.
let gradCtx = null;
const gradCache = new Map();
function cachedGrad(ctx, key, make) {
  if (gradCtx !== ctx) { gradCtx = ctx; gradCache.clear(); }
  let g = gradCache.get(key);
  if (g === undefined) { g = make(ctx); gradCache.set(key, g); }
  return g;
}

// ---------------------------------------------------------------- frame timing

let lastTime = -1;
let dt = 1 / 60;
const perfBuf = new Float32Array(600);
let perfN = 0;
let perfI = 0;
globalThis.__renderPerf = {
  stats() {
    const n = Math.min(perfN, perfBuf.length);
    if (!n) return null;
    let sum = 0, worst = 0;
    const arr = [];
    for (let i = 0; i < n; i++) {
      const v = perfBuf[i];
      arr.push(v);
      sum += v;
      if (v > worst) worst = v;
    }
    arr.sort((a, b) => a - b);
    return {
      frames: n,
      avg: sum / n,
      p50: arr[(n * 0.5) | 0],
      p95: arr[(n * 0.95) | 0],
      worst,
    };
  },
  reset() { perfN = 0; perfI = 0; },
};

// ---------------------------------------------------------------- pointer state
// Render-only: hover / pressed feedback for the HUD buttons, plus the FX-quality row
// in the settings overlay (which main.js does not know about yet).

const pointer = { x: -999, y: -999, down: false, hot: null, seen: false };
let lastGame = null;

function updatePointer(e) {
  const f = globalThis.__toLogical;
  if (!f) return;
  const p = f(e.clientX, e.clientY);
  if (!p) return;
  pointer.x = p.x;
  pointer.y = p.y;
  pointer.seen = true;
}

if (typeof window !== 'undefined' && window.addEventListener) {
  window.addEventListener('pointermove', updatePointer, { passive: true, capture: true });
  window.addEventListener('pointerdown', (e) => {
    updatePointer(e);
    pointer.down = true;
    // FX quality row (settings overlay only).
    const g = lastGame;
    if (g && g.showSettings) {
      const b = getButtons(g).find((x) => x.id === 'toggleFx');
      if (b && pointer.x >= b.x && pointer.x <= b.x + b.w && pointer.y >= b.y && pointer.y <= b.y + b.h) {
        fx.toggleLite();
      }
    }
  }, { passive: true, capture: true });
  window.addEventListener('pointerup', () => { pointer.down = false; }, { passive: true, capture: true });
  window.addEventListener('pointercancel', () => { pointer.down = false; }, { passive: true, capture: true });
}

function hitButton(b) {
  return pointer.seen && pointer.x >= b.x - 3 && pointer.x <= b.x + b.w + 3
    && pointer.y >= b.y - 3 && pointer.y <= b.y + b.h + 3;
}

// ---------------------------------------------------------------- button layout

const BTN = 40;
const BTN_GAP = 46;

export function getButtons(game) {
  const list = [
    { id: 'pause', x: W - 12 - BTN, y: 8, w: BTN, h: BTN },
    { id: 'mute', x: W - 12 - BTN - BTN_GAP, y: 8, w: BTN, h: BTN },
    { id: 'settings', x: W - 12 - BTN - BTN_GAP * 2, y: 8, w: BTN, h: BTN },
  ];
  if (game.showSettings) {
    list.push({ id: 'toggleDrag', x: W / 2 - 155, y: 356, w: 310, h: 58 });
    list.push({ id: 'toggleMute', x: W / 2 - 155, y: 424, w: 310, h: 58 });
    list.push({ id: 'toggleFx', x: W / 2 - 155, y: 492, w: 310, h: 58 });
    list.push({ id: 'closeSettings', x: W / 2 - 155, y: 568, w: 310, h: 58 });
  }
  return list;
}

// ---------------------------------------------------------------- backdrop

// The whole backdrop (base gradient + nebula clouds + intensity tint) is baked into one
// half-resolution offscreen canvas per intensity bucket. One drawImage per frame beats
// four full-screen gradient fills by a mile, especially on software rasterisers.
const BG_BUCKETS = 8;
const bgCache = new Array(BG_BUCKETS).fill(null);

function bgLayer(bucket) {
  if (bgCache[bucket]) return bgCache[bucket];
  const NW = 300, NH = 520;
  const cv = offscreen(NW, NH);
  if (!cv) return null;
  const I = bucket / (BG_BUCKETS - 1);
  const c = cv.getContext('2d');
  const h0 = 228 + I * 96;                 // indigo -> violet/magenta
  const h1 = 244 + I * 74;
  const base = c.createLinearGradient(0, 0, 0, NH);
  base.addColorStop(0, hsl(h0, 52 + I * 30, 8 + I * 9));
  base.addColorStop(0.42, hsl(h0 + 8, 55 + I * 28, 10 + I * 11));
  base.addColorStop(0.74, hsl(h1, 55 + I * 25, 8 + I * 8));
  base.addColorStop(1, hsl(h1 + 10, 50 + I * 20, 4 + I * 5));
  c.fillStyle = base;
  c.fillRect(0, 0, NW, NH);

  // Nebula clouds; they warm up and brighten with intensity.
  c.globalCompositeOperation = 'lighter';
  const clouds = [
    [70, 90, 155, 205, 0.20], [235, 150, 180, 315, 0.16], [140, 250, 215, 265, 0.15],
    [40, 400, 170, 190, 0.13], [255, 430, 195, 285, 0.14], [150, 40, 135, 230, 0.13],
    [190, 330, 125, 175, 0.11], [60, 200, 105, 322, 0.11],
  ];
  for (const [x, y, r, hue, a] of clouds) {
    const hh = hue + I * 46;
    const aa = a * (1 + I * 1.7);
    const g = c.createRadialGradient(x, y, 0, x, y, r);
    g.addColorStop(0, hsl(hh, 88, 50 + I * 12, aa));
    g.addColorStop(0.55, hsl(hh, 88, 40 + I * 10, aa * 0.42));
    g.addColorStop(1, hsl(hh, 88, 30, 0));
    c.fillStyle = g;
    c.fillRect(x - r, y - r, r * 2, r * 2);
  }
  c.globalCompositeOperation = 'source-over';
  bgCache[bucket] = cv;
  return cv;
}

// Star field, drawn procedurally over the baked layer (cheap: tiny fillRects).
const STARS = Array.from({ length: 84 }, (_, i) => {
  const a = Math.sin(i * 12.9898) * 43758.5453;
  const b = Math.sin(i * 78.233) * 12345.6789;
  const c = Math.sin(i * 3.14159) * 9876.54321;
  return {
    x: (a - Math.floor(a)),
    y: (b - Math.floor(b)),
    s: (c - Math.floor(c)) < 0.85 ? 1 : 1.8,
    tw: 1.4 + (a - Math.floor(a)) * 5,
    a: 0.22 + (c - Math.floor(c)) * 0.55,
  };
});

// Drifting light streaks that only show up when you are properly powered up.
const STREAKS = Array.from({ length: 26 }, (_, i) => ({
  x: ((i * 97) % 100) / 100,
  y: ((i * 53) % 100) / 100,
  len: 40 + ((i * 37) % 90),
  spd: 260 + ((i * 71) % 340),
  w: i % 3 === 0 ? 2 : 1,
}));

function drawBackground(ctx, game, t, I) {
  if (!nebula) nebula = buildNebula();
  const beat = fx.beat;

  if (nebula) {
    const dx = Math.sin(t * 0.045) * 26;
    const dy = Math.cos(t * 0.031) * 34;
    ctx.drawImage(nebula, -30 + dx, -40 + dy, W + 60, H + 80);
  } else {
    ctx.fillStyle = '#0a0f28';
    ctx.fillRect(0, 0, W, H);
  }

  // Intensity tint: the whole sky warms up and brightens as you power up.
  if (I > 0.01) {
    const hueA = qhue(lerp(212, 322, I));
    const hueB = qhue(lerp(190, 32, I));
    const g = cachedGrad(ctx, `tint|${hueA}|${hueB}`, (c) => {
      const gr = c.createLinearGradient(0, 0, 0, H);
      gr.addColorStop(0, hsl(hueA, 95, 46, 0.55));
      gr.addColorStop(0.5, hsl(hueB, 95, 40, 0.30));
      gr.addColorStop(1, hsl(hueA, 90, 30, 0.10));
      return gr;
    });
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    ctx.globalAlpha = (0.10 + I * 0.34) * (0.86 + beat * 0.14);
    ctx.fillStyle = g;
    ctx.fillRect(0, 0, W, H);
    ctx.restore();
  }

  // Pulsing grid, locked to a 120 BPM beat.
  ctx.save();
  ctx.globalAlpha = 0.045 + I * 0.075 + beat * (0.02 + I * 0.06);
  ctx.strokeStyle = hsl(qhue(lerp(196, 320, I)), 90, 62);
  ctx.lineWidth = 1;
  const step = 45;
  const off = (t * 12) % step;
  ctx.beginPath();
  for (let x = 0; x <= W; x += step) {
    ctx.moveTo(x + 0.5, FIELD.top);
    ctx.lineTo(x + 0.5, H);
  }
  for (let y = FIELD.top + off; y <= H; y += step) {
    ctx.moveTo(0, y);
    ctx.lineTo(W, y);
  }
  ctx.stroke();
  // brighter horizon lines sweeping down on the beat
  if (I > 0.12) {
    ctx.globalAlpha = (0.05 + I * 0.16) * (0.35 + beat * 0.65);
    ctx.lineWidth = 1.6;
    ctx.beginPath();
    const yb = FIELD.top + ((t * 90) % (H - FIELD.top));
    ctx.moveTo(0, yb);
    ctx.lineTo(W, yb);
    ctx.stroke();
  }
  ctx.restore();

  // Speed lines at high intensity.
  if (I > 0.55 && !fx.lite) {
    const k = (I - 0.55) / 0.45;
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    ctx.strokeStyle = hsl(qhue(lerp(190, 40, I)), 100, 76);
    for (const s of STREAKS) {
      const y = FIELD.top + ((s.y * (H - FIELD.top) + t * s.spd) % (H - FIELD.top));
      ctx.globalAlpha = 0.05 + k * 0.22;
      ctx.lineWidth = s.w;
      ctx.beginPath();
      ctx.moveTo(s.x * W, y);
      ctx.lineTo(s.x * W, y + s.len * (0.4 + k));
      ctx.stroke();
    }
    ctx.restore();
  }

  // Side gutters (the lanes that get you up top) and the paddle band.
  const gut = cachedGrad(ctx, 'gutter', (c) => {
    const gr = c.createLinearGradient(0, 0, 26, 0);
    gr.addColorStop(0, hsl(196, 80, 62, 0.13));
    gr.addColorStop(1, hsl(196, 80, 62, 0));
    return gr;
  });
  ctx.save();
  ctx.globalAlpha = 0.5 + I * 0.5;
  ctx.fillStyle = gut;
  ctx.fillRect(0, FIELD.top, 26, H - FIELD.top);
  ctx.translate(W, 0);
  ctx.scale(-1, 1);
  ctx.fillRect(0, FIELD.top, 26, H - FIELD.top);
  ctx.restore();

  const band = game.paddle.bandTop;
  const bg = cachedGrad(ctx, `band|${Math.round(band)}`, (c) => {
    const gr = c.createLinearGradient(0, band, 0, H);
    gr.addColorStop(0, hsl(282, 70, 60, 0.0));
    gr.addColorStop(0.55, hsl(282, 70, 55, 0.07));
    gr.addColorStop(1, hsl(300, 70, 50, 0.13));
    return gr;
  });
  ctx.fillStyle = bg;
  ctx.fillRect(0, band, W, H - band);
  ctx.save();
  ctx.globalAlpha = 0.10 + I * 0.16 + beat * 0.05;
  ctx.strokeStyle = hsl(292, 90, 70);
  ctx.setLineDash([3, 9]);
  ctx.lineWidth = 1;
  ctx.beginPath();
  ctx.moveTo(0, band + 0.5);
  ctx.lineTo(W, band + 0.5);
  ctx.stroke();
  ctx.restore();
}

// ---------------------------------------------------------------- overtop zone

// 0..1 how "charged" the sky above the bricks is. Eases so the zone fades out
// (desaturates) for a moment after an overtop run ends.
let zoneEnergy = 0;
const MOTES = Array.from({ length: 30 }, (_, i) => ({
  x: ((i * 61) % 100) / 100,
  o: ((i * 43) % 100) / 100,
  spd: 26 + ((i * 29) % 46),
  s: 1 + ((i * 17) % 3) * 0.6,
}));

function drawOvertopZone(ctx, game, t, I) {
  const top = game.brickTop;
  if (!Number.isFinite(top) || top <= FIELD.top) return;
  const bottom = top - 14;
  const zh = bottom - FIELD.top;
  if (zh < 8) return;

  const target = game.overtop ? 1 : 0;
  zoneEnergy += (target - zoneEnergy) * clamp(dt * (target > zoneEnergy ? 9 : 2.4), 0, 1);
  const E = zoneEnergy;
  const hue = qhue(lerp(196, 172, E * I));

  // Base wash.
  const wash = cachedGrad(ctx, `zone|${Math.round(bottom)}|${hue}`, (c) => {
    const gr = c.createLinearGradient(0, FIELD.top, 0, bottom);
    gr.addColorStop(0, hsl(hue, 95, 62, 0.34));
    gr.addColorStop(0.45, hsl(hue, 95, 58, 0.14));
    gr.addColorStop(1, hsl(hue, 95, 55, 0.02));
    return gr;
  });
  ctx.save();
  ctx.globalAlpha = 0.24 + E * (0.55 + 0.14 * Math.sin(t * 6));
  ctx.fillStyle = wash;
  ctx.fillRect(0, FIELD.top, W, zh);
  ctx.restore();

  // Aurora ribbons — the "power zone" tell.
  if (!fx.lite && (E > 0.02 || I > 0.25)) {
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    ctx.lineCap = 'round';
    const bands = E > 0.5 ? 4 : 3;
    for (let b = 0; b < bands; b++) {
      const ph = t * (0.5 + b * 0.23) + b * 2.1;
      const amp = (6 + b * 5) * (0.5 + E * 0.9);
      const yBase = FIELD.top + zh * (0.24 + b * 0.19);
      const bh = qhue(hue + b * 26 - 14 + I * 40);
      ctx.strokeStyle = hsl(bh, 100, 66);
      ctx.globalAlpha = (0.05 + E * 0.2) * (0.7 + 0.3 * Math.sin(ph * 1.7));
      ctx.lineWidth = 14 + b * 5 + E * 12;
      ctx.beginPath();
      for (let i = 0; i <= 12; i++) {
        const x = (i / 12) * W;
        const y = yBase + Math.sin(i * 0.55 + ph) * amp + Math.cos(i * 0.31 - ph * 0.7) * amp * 0.4;
        if (i === 0) ctx.moveTo(x, y);
        else ctx.lineTo(x, y);
      }
      ctx.stroke();
    }
    ctx.restore();
  }

  // Rising motes.
  if (E > 0.05) {
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    ctx.fillStyle = hsl(hue, 100, 82);
    for (const m of MOTES) {
      const p = (m.o + t * m.spd / zh) % 1;
      const y = bottom - p * zh;
      const a = (1 - p) * (p < 0.12 ? p / 0.12 : 1) * E * 0.85;
      if (a <= 0.01) continue;
      ctx.globalAlpha = a;
      const s = m.s * (1 + E * 0.6);
      ctx.fillRect(m.x * W + Math.sin(t * 1.7 + m.o * 9) * 6, y, s, s * 2.2);
    }
    ctx.restore();
  }

  // Boundary: glowing dashed line that ripples while you are up there.
  ctx.save();
  ctx.globalCompositeOperation = 'lighter';
  ctx.setLineDash([12, 10]);
  ctx.lineDashOffset = -t * (30 + E * 90);
  ctx.strokeStyle = hsl(hue, 100, 70);
  ctx.lineWidth = 1.5 + E * 2.2;
  ctx.globalAlpha = 0.22 + E * 0.7;
  ctx.beginPath();
  for (let i = 0; i <= 18; i++) {
    const x = (i / 18) * W;
    const y = bottom + Math.sin(i * 0.7 - t * 5) * (0.6 + E * 3.4);
    if (i === 0) ctx.moveTo(x, y);
    else ctx.lineTo(x, y);
  }
  ctx.stroke();
  ctx.setLineDash([]);
  if (E > 0.1) {
    // soft bloom under the line
    const gl = cachedGrad(ctx, `zoneline|${Math.round(bottom)}|${hue}`, (c) => {
      const gr = c.createLinearGradient(0, bottom - 26, 0, bottom + 4);
      gr.addColorStop(0, hsl(hue, 100, 62, 0));
      gr.addColorStop(1, hsl(hue, 100, 66, 0.5));
      return gr;
    });
    ctx.globalAlpha = E * 0.55;
    ctx.fillStyle = gl;
    ctx.fillRect(0, bottom - 26, W, 30);
  }
  ctx.restore();

  // Label, only while the zone is live.
  if (E > 0.25) {
    ctx.save();
    ctx.globalAlpha = (E - 0.25) / 0.75 * 0.5;
    ctx.textAlign = 'center';
    ctx.fillStyle = hsl(hue, 100, 84);
    ctx.font = `800 10px ${MONO}`;
    ctx.fillText('P O W E R   Z O N E', W / 2, FIELD.top + 16);
    ctx.restore();
  }
}

// ---------------------------------------------------------------- bricks

// Brick faces are drawn in local space (0,0 .. w,h) so their gradients can be cached.
function brickFace(ctx, b, alpha, lum = 52) {
  const key = `bf|${qhue(b.hue)}|${lum}|${Math.round(b.h)}`;
  const grd = cachedGrad(ctx, key, (c) => {
    const gr = c.createLinearGradient(0, 0, 0, b.h);
    gr.addColorStop(0, hsl(qhue(b.hue), 78, lum + 16));
    gr.addColorStop(0.52, hsl(qhue(b.hue), 82, lum + 2));
    gr.addColorStop(1, hsl(qhue(b.hue), 84, lum - 14));
    return gr;
  });
  ctx.globalAlpha *= alpha;
  ctx.fillStyle = grd;
  rr(ctx, 0, 0, b.w, b.h, 4);
  ctx.fill();
  ctx.strokeStyle = hsl(b.hue, 95, 80, 0.7);
  ctx.lineWidth = 1;
  ctx.stroke();
  // top gloss + bottom shade
  ctx.fillStyle = hsl(b.hue, 95, 92, 0.22);
  rr(ctx, 2, 1.5, b.w - 4, b.h * 0.3, 3);
  ctx.fill();
  ctx.fillStyle = 'rgba(0,0,0,0.18)';
  ctx.fillRect(2, b.h - 2.5, b.w - 4, 2);
  ctx.globalAlpha /= alpha;
}

function chevron(ctx, cx, cy, size, dir, color, lw = 2) {
  ctx.strokeStyle = color;
  ctx.lineWidth = lw;
  ctx.lineCap = 'round';
  ctx.beginPath();
  if (dir === 'v') {
    ctx.moveTo(cx - size, cy - size * 0.55);
    ctx.lineTo(cx, cy + size * 0.55);
    ctx.lineTo(cx + size, cy - size * 0.55);
  } else {
    ctx.moveTo(cx - size * 0.55, cy - size);
    ctx.lineTo(cx + size * 0.55, cy);
    ctx.lineTo(cx - size * 0.55, cy + size);
  }
  ctx.stroke();
}

function drawBrick(ctx, b, t, I) {
  const sp = b.spawnT;
  const alpha = b.kind === 'ghost' ? 0.16 + b.solidity * 0.84 : 1;
  const jitter = b.shake > 0 ? b.shake * 2 : 0;
  ctx.save();
  ctx.translate(
    b.cx + (jitter ? (Math.random() - 0.5) * jitter : 0),
    b.cy + (jitter ? (Math.random() - 0.5) * jitter : 0),
  );
  const s = sp < 1 ? 0.4 + sp * 0.6 : 1;
  ctx.scale(s, s);
  ctx.globalAlpha = sp;
  ctx.translate(-b.w / 2, -b.h / 2);
  const cx = b.w / 2, cy = b.h / 2;

  switch (b.kind) {
    case 'steel': {
      ctx.fillStyle = hsl(215, 8, 32);
      rr(ctx, 0, 0, b.w, b.h, 3);
      ctx.fill();
      ctx.strokeStyle = hsl(215, 10, 60, 0.85);
      ctx.lineWidth = 1.5;
      ctx.stroke();
      ctx.strokeStyle = hsl(215, 8, 18, 0.8);
      ctx.lineWidth = 1;
      ctx.beginPath();
      for (let x = 6; x < b.w; x += 8) {
        ctx.moveTo(x, b.h);
        ctx.lineTo(x + 6, 0);
      }
      ctx.stroke();
      ctx.fillStyle = hsl(215, 12, 68, 0.8);
      for (const dx of [6, b.w - 6]) {
        ctx.beginPath();
        ctx.arc(dx, cy, 2, 0, TAU);
        ctx.fill();
      }
      break;
    }
    case 'slow': {
      // Glass: translucent pane with hairline cracks.
      ctx.fillStyle = hsl(b.hue, 70, 62, 0.22);
      rr(ctx, 0, 0, b.w, b.h, 4);
      ctx.fill();
      ctx.strokeStyle = hsl(b.hue, 95, 82, 0.9);
      ctx.lineWidth = 1.3;
      ctx.stroke();
      ctx.fillStyle = hsl(b.hue, 100, 95, 0.16 + 0.06 * Math.sin(t * 2 + b.wobble));
      rr(ctx, 2, 1.5, b.w - 4, b.h * 0.34, 3);
      ctx.fill();
      ctx.strokeStyle = hsl(b.hue, 95, 94, 0.45);
      ctx.lineWidth = 0.8;
      ctx.beginPath();
      ctx.moveTo(4, b.h - 4);
      ctx.lineTo(cx, cy - 2);
      ctx.lineTo(b.w - 6, 4);
      ctx.moveTo(cx, cy - 2);
      ctx.lineTo(cx + 6, b.h - 3);
      ctx.stroke();
      break;
    }
    case 'speed': {
      brickFace(ctx, b, alpha, 42);
      ctx.strokeStyle = hsl(b.hue, 100, 86, 0.95);
      ctx.lineWidth = 1.5;
      ctx.beginPath();
      ctx.moveTo(cx - 7, cy);
      ctx.lineTo(cx, cy - 5.5);
      ctx.lineTo(cx + 7, cy);
      ctx.lineTo(cx, cy + 5.5);
      ctx.closePath();
      ctx.stroke();
      ctx.strokeStyle = hsl(b.hue, 40, 92, 0.5);
      ctx.lineWidth = 1;
      ctx.beginPath();
      ctx.moveTo(3, 3);
      ctx.lineTo(b.w - 3, 3);
      ctx.stroke();
      break;
    }
    case 'angle': {
      brickFace(ctx, b, alpha);
      chevron(ctx, cx, cy - 3, 6, 'v', hsl(b.hue, 100, 92, 0.95));
      chevron(ctx, cx, cy + 4, 6, 'v', hsl(b.hue, 100, 92, 0.5));
      break;
    }
    case 'angleH': {
      brickFace(ctx, b, alpha);
      chevron(ctx, cx - 4, cy, 5.5, 'h', hsl(b.hue, 100, 92, 0.95));
      chevron(ctx, cx + 5, cy, 5.5, 'h', hsl(b.hue, 100, 92, 0.5));
      break;
    }
    case 'topOnly': {
      brickFace(ctx, b, alpha, 48);
      ctx.strokeStyle = hsl(48, 100, 94, 0.95);
      ctx.lineWidth = 2;
      ctx.lineCap = 'round';
      ctx.beginPath();
      ctx.moveTo(cx, cy - 6);
      ctx.lineTo(cx, cy + 4);
      ctx.moveTo(cx - 4, cy);
      ctx.lineTo(cx, cy + 5);
      ctx.lineTo(cx + 4, cy);
      ctx.stroke();
      ctx.fillStyle = hsl(48, 100, 94, 0.35 + 0.25 * Math.sin(t * 3 + b.wobble));
      ctx.fillRect(2, 1, b.w - 4, 2.2);
      break;
    }
    case 'bomb': {
      brickFace(ctx, b, alpha, 44);
      const pulse = 0.55 + 0.45 * Math.sin(t * 6 + b.wobble);
      const spr = glowSprite(24, 100, 62);
      if (spr) {
        ctx.save();
        ctx.globalCompositeOperation = 'lighter';
        ctx.globalAlpha = 0.35 + pulse * 0.5;
        const gs = 26 + pulse * 8;
        ctx.drawImage(spr, cx - gs / 2, cy - gs / 2, gs, gs);
        ctx.restore();
      }
      ctx.fillStyle = hsl(28, 100, 66, 0.5 + pulse * 0.5);
      ctx.beginPath();
      ctx.arc(cx, cy, 5.2, 0, TAU);
      ctx.fill();
      ctx.strokeStyle = hsl(50, 100, 88, pulse);
      ctx.lineWidth = 1.4;
      ctx.beginPath();
      ctx.arc(cx, cy, 8, 0, TAU);
      ctx.stroke();
      break;
    }
    case 'powerup': {
      brickFace(ctx, b, alpha, 44);
      const pulse = 0.55 + 0.45 * Math.sin(t * 4 + b.wobble);
      ctx.fillStyle = hsl(96, 100, 78, 0.55 + pulse * 0.45);
      rr(ctx, cx - 9, cy - 4, 18, 8, 4);
      ctx.fill();
      ctx.strokeStyle = hsl(126, 100, 94, 0.9);
      ctx.lineWidth = 1;
      ctx.stroke();
      break;
    }
    case 'boost': {
      brickFace(ctx, b, alpha, 42);
      const pulse = 0.5 + 0.5 * Math.sin(t * 7 + b.wobble);
      ctx.strokeStyle = hsl(95, 100, 84, 0.55 + pulse * 0.45);
      ctx.lineWidth = 2;
      ctx.lineCap = 'round';
      for (const dy of [3, -2]) {
        ctx.beginPath();
        ctx.moveTo(cx - 6, cy + dy + 2);
        ctx.lineTo(cx, cy + dy - 3);
        ctx.lineTo(cx + 6, cy + dy + 2);
        ctx.stroke();
      }
      break;
    }
    case 'ghost': {
      ctx.globalAlpha = sp * alpha;
      brickFace(ctx, b, 1, 50);
      ctx.setLineDash([4, 3]);
      ctx.strokeStyle = hsl(b.hue, 100, 90, 0.85);
      ctx.lineWidth = 1;
      rr(ctx, 1, 1, b.w - 2, b.h - 2, 3);
      ctx.stroke();
      ctx.setLineDash([]);
      break;
    }
    default:
      brickFace(ctx, b, alpha);
      break;
  }

  // A struck-but-unbroken brick gets a one-frame brighten plus a coloured rim.
  if (b.hitFlash > 0) {
    ctx.fillStyle = `rgba(255,255,255,${clamp(b.hitFlash, 0, 1) * 0.6})`;
    rr(ctx, 0, 0, b.w, b.h, 4);
    ctx.fill();
  }
  if (b.denyFlash > 0) {
    const d = clamp(b.denyFlash, 0, 1);
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    if (d > 0.82) {
      ctx.fillStyle = `rgba(255,255,255,${(d - 0.82) / 0.18 * 0.7})`;
      rr(ctx, 0, 0, b.w, b.h, 4);
      ctx.fill();
    }
    ctx.strokeStyle = hsl(b.hue, 100, 78, d * 0.95);
    ctx.lineWidth = 2 + d * 2.4;
    rr(ctx, -1, -1, b.w + 2, b.h + 2, 5);
    ctx.stroke();
    ctx.restore();
  }
  ctx.restore();
}

// ---------------------------------------------------------------- entities

const ballSprites = new Map();
function ballSprite(hue) {
  const h = qhue(hue);
  let c = ballSprites.get(h);
  if (c !== undefined) return c;
  const size = 64;
  const cv = offscreen(size, size);
  if (!cv) { ballSprites.set(h, null); return null; }
  const g = cv.getContext('2d');
  const r = size / 2;
  const grd = g.createRadialGradient(r - r * 0.3, r - r * 0.34, 1, r, r, r * 0.98);
  grd.addColorStop(0, '#ffffff');
  grd.addColorStop(0.42, hsl(h, 100, 82));
  grd.addColorStop(0.86, hsl(h, 96, 56));
  grd.addColorStop(1, hsl(h, 96, 48, 0.0));
  g.fillStyle = grd;
  g.beginPath();
  g.arc(r, r, r * 0.98, 0, TAU);
  g.fill();
  ballSprites.set(h, cv);
  return cv;
}

let emberAcc = 0;
let jetAcc = 0;

function drawBall(ctx, b, game, t, I) {
  const fire = b.fire > 0;
  const ghost = b.ghost > 0;
  const hue = fire ? 22 : ghost ? 265 : b.overtop ? 190 : 48;
  const speed = b.speed;
  const tier = clamp((speed - 300) / 420, 0, 1);      // 0 slow .. 1 blazing
  const heat = clamp(tier * 0.6 + I * 0.5, 0, 1);
  const ang = Math.atan2(b.vy, b.vx);

  // ---- trail: per-segment taper so length/width/brightness track speed + intensity
  const pts = b.trail;
  if (pts && pts.length > 1) {
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    ctx.lineCap = 'round';
    ctx.lineJoin = 'round';
    const wMax = b.r * (0.85 + heat * 0.9) * (fire ? 1.2 : 1);
    const keep = Math.max(4, Math.round(pts.length * (0.45 + heat * 0.55)));
    for (let i = 1; i < Math.min(pts.length, keep); i++) {
      const p0 = pts[i - 1];
      const p1 = pts[i];
      const k = 1 - i / keep;
      const a = clamp(p1.a, 0, 1) * k;
      if (a <= 0.02) continue;
      ctx.globalAlpha = a * (0.28 + heat * 0.42);
      ctx.strokeStyle = hsl(hue + (fire ? i * 2.5 : ghost ? -i : i * 0.8), 100, 62 + k * 18);
      ctx.lineWidth = Math.max(0.6, wMax * k * 2);
      ctx.beginPath();
      ctx.moveTo(p0.x, p0.y);
      ctx.lineTo(p1.x, p1.y);
      ctx.stroke();
    }
    // bloom blobs along the trail
    const spr = glowSprite(hue, 100, 64);
    if (spr && !fx.lite) {
      for (let i = 0; i < Math.min(pts.length, keep); i += 3) {
        const p = pts[i];
        const k = 1 - i / keep;
        const s = b.r * (2.6 + heat * 2.2) * k;
        if (s < 1) continue;
        ctx.globalAlpha = clamp(p.a, 0, 1) * k * (0.16 + heat * 0.26);
        ctx.drawImage(spr, p.x - s / 2, p.y - s / 2, s, s);
      }
    }
    ctx.restore();
  }

  // ---- glow halo
  const spr = glowSprite(hue, 100, 64);
  if (spr) {
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    const s = b.r * (5 + heat * 5 + (b.overtop ? 1.6 : 0));
    ctx.globalAlpha = 0.4 + heat * 0.4;
    ctx.drawImage(spr, b.x - s / 2, b.y - s / 2, s, s);
    ctx.restore();
  }

  // ---- comet head: stretch the ball along its velocity in the blazing tier
  const stretch = clamp((speed - 560) / 500, 0, 0.55);
  const core = ballSprite(hue);
  ctx.save();
  ctx.globalAlpha = ghost ? 0.72 : 1;
  ctx.translate(b.x, b.y);
  if (stretch > 0.01) {
    ctx.rotate(ang);
    ctx.scale(1 + stretch, 1 - stretch * 0.42);
    ctx.rotate(-ang);
  }
  const d = b.r * 2.04;
  if (core) ctx.drawImage(core, -d / 2, -d / 2, d, d);
  else {
    ctx.fillStyle = hsl(hue, 100, 74);
    ctx.beginPath();
    ctx.arc(0, 0, b.r, 0, TAU);
    ctx.fill();
  }
  ctx.restore();

  // ---- overtop halo ring
  if (b.overtop) {
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    ctx.globalAlpha = 0.45 + 0.3 * Math.sin(t * 12);
    ctx.strokeStyle = hsl(190, 100, 82);
    ctx.lineWidth = 2;
    ctx.beginPath();
    ctx.arc(b.x, b.y, b.r + 5 + Math.sin(t * 9) * 1.4, 0, TAU);
    ctx.stroke();
    ctx.restore();
  }
}

function emitBallFx(game, I) {
  // Embers stream off the balls once you are properly powered up.
  const rate = (I > 0.35 ? (I - 0.35) * 34 : 0) + (game.powers.fire > 0 ? 16 : 0);
  if (rate <= 0 || fx.lite) { emberAcc = 0; return; }
  emberAcc += dt * rate;
  while (emberAcc >= 1) {
    emberAcc -= 1;
    const list = game.balls;
    if (!list.length) { emberAcc = 0; return; }
    const b = list[(Math.random() * list.length) | 0];
    if (!b || b.stuck) continue;
    const hue = b.fire > 0 ? 24 : b.overtop ? 186 : 42;
    fx.ember(b.x, b.y, hue, 1, { vx: -b.vx, vy: -b.vy, hueShift: -30 });
  }
}

function drawPaddle(ctx, game, t, I) {
  const p = game.paddle;
  const squash = p.squash > 0 ? p.squash : 0;
  const h = p.h * (1 - squash * 0.25);
  const y = p.y + (p.h - h);
  const hue = game.powers.laser > 0 ? 0 : game.powers.magnet > 0 ? 330 : qhue(lerp(195, 285, I));
  const up = p.vy < 0;
  const rising = Math.abs(p.vy) > 60;

  ctx.save();
  // Thruster: rising into the ball is the smash verb, so make it look like thrust.
  if (rising) {
    const k = clamp(Math.abs(p.vy) / 700, 0, 1);
    const g = cachedGrad(ctx, `thrust|${up ? 1 : 0}`, (c) => {
      const gr = c.createLinearGradient(0, 0, 0, up ? 54 : -54);
      gr.addColorStop(0, hsl(up ? 28 : 200, 100, 68, 0.75));
      gr.addColorStop(0.4, hsl(up ? 40 : 200, 100, 62, 0.28));
      gr.addColorStop(1, hsl(up ? 50 : 200, 100, 60, 0));
      return gr;
    });
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    ctx.globalAlpha = k * 0.9;
    ctx.translate(0, y + (up ? h : 0));
    ctx.fillStyle = g;
    ctx.fillRect(p.x + 2, up ? 0 : -54, p.w - 4, 54);
    ctx.restore();
    if (up && k > 0.35 && !fx.lite) {
      jetAcc += dt * k * 46;
      while (jetAcc >= 1) {
        jetAcc -= 1;
        fx.ember(p.x + rand(6, p.w - 6), y + h + 2, 34, 1, { grav: 220, hueShift: -40 });
      }
    }
  }

  // Energy glow under the paddle, scaling with intensity.
  const spr = glowSprite(hue, 100, 60);
  if (spr) {
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    ctx.globalAlpha = 0.35 + I * 0.45 + (p.flash > 0 ? p.flash * 0.3 : 0);
    const gw = p.w * (1.25 + I * 0.3);
    const gh = 46 + I * 34;
    ctx.drawImage(spr, p.cx - gw / 2, y + h / 2 - gh / 2, gw, gh);
    ctx.restore();
  }

  const grd = cachedGrad(ctx, `pad|${hue}|${Math.round(h * 4)}`, (c) => {
    const gr = c.createLinearGradient(0, 0, 0, h);
    gr.addColorStop(0, hsl(hue, 95, 86));
    gr.addColorStop(0.42, hsl(hue, 90, 62));
    gr.addColorStop(0.75, hsl(hue, 88, 46));
    gr.addColorStop(1, hsl(hue, 85, 34));
    return gr;
  });
  ctx.save();
  ctx.translate(0, y);
  ctx.fillStyle = grd;
  rr(ctx, p.x, 0, p.w, h, h / 2);
  ctx.fill();
  ctx.strokeStyle = hsl(hue, 100, 88, 0.5 + I * 0.4);
  ctx.lineWidth = 1;
  ctx.stroke();
  ctx.fillStyle = `rgba(255,255,255,${0.3 + (p.flash > 0 ? p.flash * 0.55 : 0)})`;
  rr(ctx, p.x + 4, 2, p.w - 8, 3.2, 2);
  ctx.fill();
  // energy core line that brightens with intensity
  ctx.save();
  ctx.globalCompositeOperation = 'lighter';
  ctx.globalAlpha = 0.25 + I * 0.6;
  ctx.fillStyle = hsl(hue + 30, 100, 78);
  rr(ctx, p.x + 8, h * 0.5 - 1, p.w - 16, 2, 1);
  ctx.fill();
  ctx.restore();
  ctx.restore();

  if (game.powers.laser > 0) {
    ctx.fillStyle = hsl(0, 100, 72, 0.6 + 0.4 * Math.sin(t * 14));
    ctx.fillRect(p.x + 6, y - 5, 4, 5);
    ctx.fillRect(p.x + p.w - 10, y - 5, 4, 5);
  }
  if (game.powers.magnet > 0) {
    ctx.strokeStyle = hsl(330, 100, 78, 0.35 + 0.25 * Math.sin(t * 8));
    ctx.lineWidth = 2;
    ctx.beginPath();
    ctx.arc(p.cx, y, p.w * 0.55, Math.PI * 1.15, Math.PI * 1.85);
    ctx.stroke();
  }
  ctx.restore();
}

function drawCapsule(ctx, c, t) {
  const def = POWERUPS[c.type];
  ctx.save();
  ctx.translate(c.x + c.w / 2, c.y + c.h / 2);
  const spr = glowSprite(def.hue, 100, 60);
  if (spr) {
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    ctx.globalAlpha = 0.55 + 0.2 * Math.sin(c.t * 7);
    ctx.drawImage(spr, -34, -22, 68, 44);
    ctx.restore();
  }
  ctx.rotate(Math.sin(c.t * 4) * 0.16);
  const g = cachedGrad(ctx, `cap|${qhue(def.hue)}`, (cc) => {
    const gr = cc.createLinearGradient(0, -9, 0, 9);
    gr.addColorStop(0, hsl(def.hue, 95, 78));
    gr.addColorStop(0.5, hsl(def.hue, 92, 58));
    gr.addColorStop(1, hsl(def.hue, 90, 42));
    return gr;
  });
  ctx.fillStyle = g;
  rr(ctx, -c.w / 2, -c.h / 2, c.w, c.h, c.h / 2);
  ctx.fill();
  ctx.strokeStyle = 'rgba(255,255,255,0.55)';
  ctx.lineWidth = 1;
  ctx.stroke();
  ctx.fillStyle = 'rgba(255,255,255,0.95)';
  ctx.font = `700 12px ${MONO}`;
  ctx.textAlign = 'center';
  ctx.textBaseline = 'middle';
  ctx.fillText(def.glyph, 0, 0.5);
  ctx.restore();
}

function drawBolts(ctx, game) {
  if (!game.bolts.length) return;
  ctx.save();
  ctx.globalCompositeOperation = 'lighter';
  const g = cachedGrad(ctx, 'bolt', (c) => {
    const gr = c.createLinearGradient(0, 0, 0, 16);
    gr.addColorStop(0, hsl(0, 100, 80, 0.08));
    gr.addColorStop(1, hsl(0, 100, 72, 1));
    return gr;
  });
  for (const b of game.bolts) {
    ctx.save();
    ctx.translate(b.x, b.y);
    ctx.fillStyle = g;
    ctx.fillRect(0, 0, b.w, b.h);
    ctx.restore();
  }
  ctx.restore();
}

// ---------------------------------------------------------------- buttons

export function drawButtons(ctx, game) {
  ctx.save();
  ctx.textAlign = 'center';
  ctx.textBaseline = 'middle';
  for (const b of getButtons(game)) {
    if (b.y > 100) continue;
    const hot = hitButton(b);
    const pressed = hot && pointer.down;
    ctx.globalAlpha = 1;
    ctx.fillStyle = pressed ? 'rgba(120,220,255,0.30)' : hot ? 'rgba(255,255,255,0.16)' : 'rgba(255,255,255,0.07)';
    rr(ctx, b.x, b.y, b.w, b.h, 10);
    ctx.fill();
    ctx.strokeStyle = pressed ? 'rgba(150,235,255,0.9)' : hot ? 'rgba(255,255,255,0.42)' : 'rgba(255,255,255,0.18)';
    ctx.lineWidth = hot ? 1.6 : 1;
    ctx.stroke();
    ctx.fillStyle = hot ? '#ffffff' : 'rgba(255,255,255,0.82)';
    ctx.font = `700 15px ${MONO}`;
    const label = b.id === 'pause' ? (game.state === 'paused' ? '\u25B6' : 'II')
      : b.id === 'mute' ? (game.muted ? '\u2715' : '\u266A') : '\u2699';
    ctx.fillText(label, b.x + b.w / 2, b.y + b.h / 2 + 1);
  }
  ctx.restore();
}

// ---------------------------------------------------------------- HUD

const MULT_TIERS = [
  { hue: 190, sat: 95 },   // x1
  { hue: 52, sat: 100 },   // x2-3
  { hue: 26, sat: 100 },   // x4-6
  { hue: 328, sat: 100 },  // x7-10
];
function multTier(m) {
  if (m >= 7) return 3;
  if (m >= 4) return 2;
  if (m >= 2) return 1;
  return 0;
}

function drawMultiplier(ctx, game, t, I) {
  const mi = Math.round(game.multiplier);
  const tier = multTier(mi);
  const { hue, sat } = MULT_TIERS[tier];
  const pulse = clamp(game.multPulse, 0, 1);
  const beat = fx.beat;
  const scale = 1 + pulse * pulse * 0.7 + (tier >= 2 ? beat * 0.05 * (tier - 1) : 0);
  const label = `x${game.displayMult < 1.02 ? 1 : (Math.round(game.displayMult * 10) / 10).toFixed(mi >= 10 ? 0 : 1)}`;

  ctx.save();
  ctx.translate(W / 2, 74);
  ctx.scale(scale, scale);
  ctx.textAlign = 'center';
  ctx.textBaseline = 'alphabetic';

  // glow behind the number from x4
  if (tier >= 2) {
    const spr = glowSprite(hue, sat, 60);
    if (spr) {
      ctx.save();
      ctx.globalCompositeOperation = 'lighter';
      ctx.globalAlpha = 0.35 + 0.3 * beat + pulse * 0.4;
      const s = 96 + tier * 26;
      ctx.drawImage(spr, -s / 2, -s / 2 - 8, s, s * 0.62);
      ctx.restore();
    }
  }
  // flame licks at x7+
  if (tier >= 3 && !fx.lite) {
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    const spr = glowSprite(30, 100, 62);
    if (spr) {
      for (let i = 0; i < 6; i++) {
        const ph = t * 4 + i * 1.7;
        const fx0 = -34 + i * 13.6 + Math.sin(ph) * 3;
        const h0 = 12 + (Math.sin(ph * 1.7) * 0.5 + 0.5) * 16;
        ctx.globalAlpha = 0.25 + 0.25 * Math.sin(ph * 2.3);
        ctx.drawImage(spr, fx0 - 7, -18 - h0, 14, h0 + 10);
      }
    }
    ctx.restore();
  }

  const size = 30 + tier * 3.2;
  ctx.font = `900 ${size}px ${DISPLAY}`;
  ctx.lineJoin = 'round';
  ctx.lineWidth = 5;
  ctx.strokeStyle = 'rgba(3,5,14,0.8)';
  ctx.strokeText(label, 0, 0);

  // chromatic jitter at x7+
  if (tier >= 3) {
    const j = 1.2 + fx.chroma * 1.8;
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    ctx.globalAlpha = 0.55;
    ctx.fillStyle = hsl(hue - 40, 100, 60);
    ctx.fillText(label, -j - Math.sin(t * 31) * 0.8, Math.cos(t * 27) * 0.6);
    ctx.fillStyle = hsl(hue + 55, 100, 60);
    ctx.fillText(label, j + Math.sin(t * 29) * 0.8, -Math.cos(t * 23) * 0.6);
    ctx.restore();
  }

  const grd = cachedGrad(ctx, `mult|${hue}|${Math.round(size)}`, (c) => {
    const gr = c.createLinearGradient(0, -size * 0.85, 0, size * 0.25);
    gr.addColorStop(0, '#ffffff');
    gr.addColorStop(0.45, hsl(hue, sat, 82));
    gr.addColorStop(1, hsl(hue, sat, 56));
    return gr;
  });
  ctx.fillStyle = grd;
  ctx.fillText(label, 0, 0);

  ctx.font = `700 8px ${MONO}`;
  ctx.fillStyle = hsl(hue, 70, 78, 0.75);
  ctx.fillText(tier >= 3 ? 'MULTIPLIER · MAXING' : 'MULTIPLIER', 0, 12);
  ctx.restore();
}

function shimmerBar(ctx, x, y, w, h, frac, hue, t) {
  ctx.save();
  ctx.fillStyle = 'rgba(255,255,255,0.10)';
  rr(ctx, x, y, w, h, h / 2);
  ctx.fill();
  const fw = Math.max(2, w * clamp(frac, 0, 1));
  const g = cachedGrad(ctx, `bar|${qhue(hue)}`, (c) => {
    const gr = c.createLinearGradient(0, 0, 1, 0);
    gr.addColorStop(0, hsl(hue, 100, 60));
    gr.addColorStop(1, hsl(hue, 100, 78));
    return gr;
  });
  ctx.save();
  ctx.translate(x, y);
  ctx.scale(fw, 1);
  ctx.fillStyle = g;
  rr(ctx, 0, 0, 1, h, 0);
  ctx.fill();
  ctx.restore();
  // shimmer sweep
  ctx.save();
  ctx.beginPath();
  rr(ctx, x, y, fw, h, h / 2);
  ctx.clip();
  ctx.globalCompositeOperation = 'lighter';
  const sx = x + ((t * 90) % (w + 40)) - 20;
  const sg = cachedGrad(ctx, 'shimmer', (c) => {
    const gr = c.createLinearGradient(0, 0, 26, 0);
    gr.addColorStop(0, 'rgba(255,255,255,0)');
    gr.addColorStop(0.5, 'rgba(255,255,255,0.65)');
    gr.addColorStop(1, 'rgba(255,255,255,0)');
    return gr;
  });
  ctx.save();
  ctx.translate(sx, y);
  ctx.fillStyle = sg;
  ctx.fillRect(0, 0, 26, h);
  ctx.restore();
  ctx.restore();
  ctx.restore();
}

function drawHud(ctx, game, t, I) {
  const beat = fx.beat;
  ctx.save();
  const g = cachedGrad(ctx, 'hud', (c) => {
    const gr = c.createLinearGradient(0, 0, 0, FIELD.top);
    gr.addColorStop(0, 'rgba(6,8,22,0.96)');
    gr.addColorStop(0.7, 'rgba(9,11,30,0.92)');
    gr.addColorStop(1, 'rgba(12,14,38,0.86)');
    return gr;
  });
  ctx.fillStyle = g;
  ctx.fillRect(0, 0, W, FIELD.top);
  if (I > 0.02) {
    const th = qhue(lerp(200, 330, I));
    const tg = cachedGrad(ctx, `hudtint|${th}`, (c) => {
      const gr = c.createLinearGradient(0, 0, 0, FIELD.top);
      gr.addColorStop(0, hsl(th, 100, 50, 0.0));
      gr.addColorStop(1, hsl(th, 100, 52, 0.4));
      return gr;
    });
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    ctx.globalAlpha = I * 0.55;
    ctx.fillStyle = tg;
    ctx.fillRect(0, 0, W, FIELD.top);
    ctx.restore();
  }
  // neon rail under the HUD
  ctx.save();
  ctx.globalCompositeOperation = 'lighter';
  ctx.globalAlpha = 0.35 + I * 0.5 + beat * 0.15;
  ctx.fillStyle = hsl(qhue(lerp(190, 330, I)), 100, 66);
  ctx.fillRect(0, FIELD.top - 2, W, 1.6);
  ctx.globalAlpha *= 0.35;
  ctx.fillRect(0, FIELD.top - 6, W, 4);
  ctx.restore();

  ctx.textBaseline = 'alphabetic';
  ctx.textAlign = 'left';
  ctx.fillStyle = hsl(196, 40, 76, 0.62);
  ctx.font = `700 9px ${MONO}`;
  ctx.fillText('SCORE', 14, 19);
  ctx.fillStyle = '#fff';
  ctx.font = `800 24px ${DISPLAY}`;
  ctx.fillText(formatScore(game.score), 14, 44);
  ctx.fillStyle = hsl(48, 90, 74, 0.85);
  ctx.font = `700 10px ${MONO}`;
  ctx.fillText(`HI ${formatScore(game.highScore)}`, 14, 60);

  // lives
  ctx.fillStyle = hsl(196, 40, 76, 0.6);
  ctx.font = `700 9px ${MONO}`;
  ctx.fillText('BALLS', 14, 78);
  const pipSpr = glowSprite(48, 100, 62);
  for (let i = 0; i < Math.min(game.lives, 5); i++) {
    const px = 62 + i * 15;
    if (pipSpr) {
      ctx.save();
      ctx.globalCompositeOperation = 'lighter';
      ctx.globalAlpha = 0.5;
      ctx.drawImage(pipSpr, px - 9, 65 - 9, 18, 18);
      ctx.restore();
    }
    ctx.fillStyle = hsl(48, 100, 70);
    ctx.beginPath();
    ctx.arc(px, 74, 4.4, 0, TAU);
    ctx.fill();
  }
  if (game.lives > 5) {
    ctx.fillStyle = hsl(48, 100, 70, 0.95);
    ctx.font = `700 10px ${MONO}`;
    ctx.fillText(`+${game.lives - 5}`, 62 + 5 * 15, 78);
  }

  // level
  ctx.textAlign = 'center';
  ctx.fillStyle = hsl(196, 70, 84, 0.92);
  ctx.font = `800 13px ${MONO}`;
  ctx.fillText(`LEVEL ${game.level}`, W / 2, 22);
  ctx.fillStyle = hsl(196, 45, 74, 0.5);
  ctx.font = `600 9px ${MONO}`;
  ctx.fillText((game.levelName || '').toUpperCase(), W / 2, 34);

  drawMultiplier(ctx, game, t, I);

  // right column: overtop state
  ctx.textAlign = 'right';
  const barX = W - 14 - 104;
  if (game.overtop) {
    ctx.fillStyle = hsl(186, 100, 78, 0.6 + 0.4 * Math.sin(t * 9));
    ctx.font = `800 12px ${MONO}`;
    ctx.fillText('OVERTOP', W - 14, 46);
    ctx.fillStyle = '#fff';
    ctx.font = `800 15px ${DISPLAY}`;
    ctx.fillText(`${game.overtopTime.toFixed(1)}s`, W - 14, 62);
    shimmerBar(ctx, barX, 66, 104, 5, (game.overtopTime % 2) / 2, 186, t);
    if (game.ceilingCombo > 1) {
      ctx.fillStyle = hsl(48, 100, 74, 0.95);
      ctx.font = `800 10px ${MONO}`;
      ctx.fillText(`CEILING x${game.ceilingCombo}`, W - 14, 84);
    } else {
      ctx.fillStyle = hsl(186, 60, 70, 0.55);
      ctx.font = `600 8px ${MONO}`;
      ctx.fillText('+1 MULT PER 2s', W - 14, 84);
    }
  } else {
    ctx.fillStyle = hsl(196, 45, 66, 0.5);
    ctx.font = `800 11px ${MONO}`;
    ctx.fillText(game.overtopStreak > 0 ? `STREAK ${game.overtopStreak}` : 'GET OVERTOP', W - 14, 60);
    if (game.overtopStreak > 0) {
      shimmerBar(ctx, barX, 66, 104, 5, (game.overtopStreak % 3) / 3, 196, t);
    }
    if (game.bestOvertop > 0.1) {
      ctx.fillStyle = hsl(196, 40, 66, 0.4);
      ctx.font = `600 8px ${MONO}`;
      ctx.fillText(`BEST ${game.bestOvertop.toFixed(1)}s`, W - 14, 84);
    }
  }

  drawButtons(ctx, game);

  // active power-up pips
  const active = [];
  if (game.powers.fire > 0) active.push(['fireball', game.powers.fire]);
  if (game.powers.heavy > 0) active.push(['heavy', game.powers.heavy]);
  if (game.powers.ghost > 0) active.push(['ghostball', game.powers.ghost]);
  if (game.powers.magnet > 0) active.push(['magnet', game.powers.magnet]);
  if (game.powers.wide > 0) active.push(['wide', game.powers.wide]);
  if (game.powers.laser > 0) active.push(['laser', game.powers.laser]);
  if (game.slowmoTimer > 0) active.push(['slowmo', game.slowmoTimer]);
  active.forEach(([key, time], i) => {
    const def = POWERUPS[key];
    const x = 14 + i * 32;
    const yy = FIELD.top + 8;
    const low = time < 3;
    const a = low ? 0.45 + 0.55 * Math.abs(Math.sin(t * 8)) : 1;
    ctx.save();
    ctx.globalAlpha = a;
    ctx.fillStyle = hsl(def.hue, 90, 55, 0.28);
    rr(ctx, x, yy, 28, 22, 6);
    ctx.fill();
    ctx.strokeStyle = hsl(def.hue, 100, 72, 0.6);
    ctx.lineWidth = 1;
    ctx.stroke();
    ctx.fillStyle = hsl(def.hue, 100, 84, 0.98);
    ctx.textAlign = 'center';
    ctx.font = `700 12px ${MONO}`;
    ctx.fillText(def.glyph, x + 14, yy + 15);
    ctx.fillStyle = hsl(def.hue, 95, 74, 0.9);
    ctx.fillRect(x + 2, yy + 19, 24 * clamp(time / 12, 0, 1), 2);
    ctx.restore();
  });

  ctx.restore();
}

// ---------------------------------------------------------------- overlays

function panel(ctx, x, y, w, h, hue = 200, a = 0.9) {
  ctx.save();
  const g = cachedGrad(ctx, `panel|${qhue(hue)}|${Math.round(y)}|${Math.round(h)}`, (c) => {
    const gr = c.createLinearGradient(0, y, 0, y + h);
    gr.addColorStop(0, `hsl(${qhue(hue)} 55% 12% / ${a})`);
    gr.addColorStop(1, `hsl(${qhue(hue) + 12} 60% 6% / ${a})`);
    return gr;
  });
  ctx.fillStyle = g;
  rr(ctx, x, y, w, h, 18);
  ctx.fill();
  ctx.strokeStyle = hsl(hue, 95, 66, 0.42);
  ctx.lineWidth = 1.5;
  ctx.stroke();
  ctx.save();
  ctx.globalCompositeOperation = 'lighter';
  ctx.globalAlpha = 0.5;
  ctx.strokeStyle = hsl(hue, 100, 72, 0.25);
  ctx.lineWidth = 1;
  rr(ctx, x + 3, y + 3, w - 6, h - 6, 15);
  ctx.stroke();
  ctx.restore();
  ctx.restore();
}

function centerText(ctx, text, y, size, color, weight = 800, font = MONO) {
  ctx.textAlign = 'center';
  ctx.font = `${weight} ${size}px ${font}`;
  ctx.fillStyle = color;
  ctx.fillText(text, W / 2, y);
}

/** Big title-style text with a pre-blurred glow behind it (overlay screens only). */
function glowTitle(ctx, text, y, size, hue, t, spread = 26) {
  ctx.save();
  ctx.textAlign = 'center';
  ctx.textBaseline = 'alphabetic';
  ctx.font = `900 ${size}px ${DISPLAY}`;
  if ('letterSpacing' in ctx) ctx.letterSpacing = `${Math.round(size * 0.06)}px`;
  ctx.save();
  ctx.globalCompositeOperation = 'lighter';
  ctx.shadowColor = hsl(hue, 100, 60, 0.95);
  ctx.shadowBlur = spread * (0.8 + 0.2 * Math.sin(t * 3));
  ctx.fillStyle = hsl(hue, 100, 60, 0.5);
  ctx.fillText(text, W / 2, y);
  ctx.shadowBlur = spread * 2;
  ctx.fillStyle = hsl(hue, 100, 55, 0.35);
  ctx.fillText(text, W / 2, y);
  ctx.restore();
  ctx.shadowBlur = 0;
  const grd = ctx.createLinearGradient(0, y - size * 0.85, 0, y + size * 0.2);
  grd.addColorStop(0, '#ffffff');
  grd.addColorStop(0.5, hsl(hue, 100, 88));
  grd.addColorStop(0.51, hsl(hue, 95, 72));
  grd.addColorStop(1, hsl(hue, 95, 52));
  ctx.lineJoin = 'round';
  ctx.lineWidth = size * 0.1;
  ctx.strokeStyle = 'rgba(3,4,14,0.55)';
  ctx.strokeText(text, W / 2, y);
  ctx.fillStyle = grd;
  ctx.fillText(text, W / 2, y);
  if ('letterSpacing' in ctx) ctx.letterSpacing = '0px';
  ctx.restore();
}

function statRows(ctx, rows, x, y, w, lineH = 26) {
  ctx.save();
  ctx.textBaseline = 'alphabetic';
  rows.forEach(([label, value, hue], i) => {
    const yy = y + i * lineH;
    ctx.textAlign = 'left';
    ctx.font = `700 11px ${MONO}`;
    ctx.fillStyle = 'rgba(230,238,255,0.5)';
    ctx.fillText(label, x, yy);
    ctx.textAlign = 'right';
    ctx.font = `800 16px ${DISPLAY}`;
    ctx.fillStyle = hue === undefined ? '#fff' : hsl(hue, 100, 76);
    ctx.fillText(String(value), x + w, yy);
    ctx.strokeStyle = 'rgba(255,255,255,0.07)';
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.moveTo(x, yy + 7.5);
    ctx.lineTo(x + w, yy + 7.5);
    ctx.stroke();
  });
  ctx.restore();
}

// ---- title screen -------------------------------------------------------

const demo = { x: 180, y: 150, vx: 168, vy: -132, trail: [], r: 8, fire: 0, ghost: 0, overtop: true, speed: 210 };

function updateDemoBall(game) {
  const top = FIELD.top + 12;
  const bottom = (Number.isFinite(game.brickTop) ? game.brickTop : FIELD.top + 140) - 12;
  demo.x += demo.vx * dt;
  demo.y += demo.vy * dt;
  if (demo.x < 20) { demo.x = 20; demo.vx = Math.abs(demo.vx); }
  if (demo.x > W - 20) { demo.x = W - 20; demo.vx = -Math.abs(demo.vx); }
  if (demo.y < top) { demo.y = top; demo.vy = Math.abs(demo.vy); fx.sparks(demo.x, top, 4, 190, 120); }
  if (demo.y > bottom) {
    demo.y = bottom;
    demo.vy = -Math.abs(demo.vy);
    fx.sparks(demo.x, bottom, 5, 190, 140);
    fx.ring(demo.x, bottom, { r0: 2, r1: 26, life: 0.35, hue: 190, width: 2 });
  }
  demo.speed = Math.hypot(demo.vx, demo.vy);
  demo.trail.unshift({ x: demo.x, y: demo.y, a: 1 });
  if (demo.trail.length > 18) demo.trail.length = 18;
  for (const p of demo.trail) p.a -= dt * 2.6;
  while (demo.trail.length && demo.trail[demo.trail.length - 1].a <= 0) demo.trail.pop();
}

function drawTitle(ctx, game, t) {
  ctx.save();
  const scrim = cachedGrad(ctx, 'titlescrim', (c) => {
    const gr = c.createLinearGradient(0, 0, 0, H);
    gr.addColorStop(0, 'rgba(4,6,18,0.72)');
    gr.addColorStop(0.32, 'rgba(4,6,18,0.88)');
    gr.addColorStop(1, 'rgba(2,3,10,0.94)');
    return gr;
  });
  ctx.fillStyle = scrim;
  ctx.fillRect(0, 0, W, H);

  // demo ball bouncing along the roof of the brick field
  drawBall(ctx, demo, game, t, 0.5);

  const bob = Math.sin(t * 1.6) * 5;
  ctx.save();
  ctx.translate(0, bob);
  glowTitle(ctx, 'OVERTOP', 300, 66, 190, t, 30);
  ctx.restore();

  // neon rule
  ctx.save();
  ctx.globalCompositeOperation = 'lighter';
  const rule = cachedGrad(ctx, 'titlerule', (c) => {
    const gr = c.createLinearGradient(70, 0, W - 70, 0);
    gr.addColorStop(0, hsl(190, 100, 60, 0));
    gr.addColorStop(0.5, hsl(190, 100, 72, 0.9));
    gr.addColorStop(1, hsl(320, 100, 60, 0));
    return gr;
  });
  ctx.fillStyle = rule;
  ctx.fillRect(70, 318 + bob, W - 140, 2);
  ctx.restore();

  centerText(ctx, 'GET THE BALL ABOVE THE BRICKS', 344, 13, hsl(190, 90, 78, 0.95), 800);
  centerText(ctx, 'AND LET IT EAT', 364, 13, hsl(320, 90, 76, 0.7), 800);

  const pulse = 0.55 + 0.45 * Math.sin(t * 4);
  ctx.save();
  ctx.globalAlpha = 0.35 + pulse * 0.4;
  panel(ctx, W / 2 - 150, 396, 300, 44, 190, 0.5);
  ctx.restore();
  centerText(ctx, 'TAP  ·  CLICK  ·  SPACE', 424, 16, hsl(50, 100, 74, 0.7 + pulse * 0.3), 900, DISPLAY);

  panel(ctx, 52, 462, W - 104, 214, 220, 0.72);
  ctx.textAlign = 'left';
  ctx.font = `800 11px ${MONO}`;
  ctx.fillStyle = hsl(190, 80, 82, 0.95);
  ctx.fillText('HOW TO PLAY', 76, 490);
  ctx.font = `600 12px ${MONO}`;
  const lines = [
    ['MOUSE', 'move in X and Y · click to launch'],
    ['KEYS', 'WASD / arrows · SPACE · P pause'],
    ['TOUCH', 'thumb anywhere low · 2nd tap fires'],
  ];
  lines.forEach(([k, v], i) => {
    const y = 514 + i * 21;
    ctx.fillStyle = hsl(190, 90, 74, 0.95);
    ctx.font = `800 11px ${MONO}`;
    ctx.fillText(k, 76, y);
    ctx.fillStyle = 'rgba(232,236,255,0.78)';
    ctx.font = `600 11px ${MONO}`;
    ctx.fillText(v, 138, y);
  });
  ctx.fillStyle = hsl(48, 100, 78, 0.92);
  ctx.font = `800 12px ${MONO}`;
  ctx.fillText('THE PADDLE MOVES UP AND DOWN.', 76, 600);
  ctx.fillStyle = 'rgba(232,236,255,0.66)';
  ctx.font = `600 11px ${MONO}`;
  ctx.fillText('Rise into the ball to SMASH it faster and', 76, 620);
  ctx.fillText('steeper. Punch a hole, ride the gutters, get', 76, 638);
  ctx.fillText('OVERTOP — up there the multiplier climbs.', 76, 656);

  ctx.textAlign = 'center';
  ctx.font = `800 13px ${DISPLAY}`;
  ctx.fillStyle = hsl(48, 100, 76, 0.95);
  ctx.fillText(`HIGH SCORE  ${formatScore(game.highScore)}`, W / 2, 712);
  ctx.font = `600 10px ${MONO}`;
  ctx.fillStyle = 'rgba(255,255,255,0.34)';
  ctx.fillText('3 LIVES · 12 HAND-BUILT LEVELS · THEN IT KEEPS GOING', W / 2, 736);
  ctx.restore();
}

function drawPause(ctx, game, t) {
  ctx.save();
  ctx.fillStyle = 'rgba(4,6,16,0.74)';
  ctx.fillRect(0, 0, W, H);
  glowTitle(ctx, 'PAUSED', 430, 46, 200, t, 22);
  centerText(ctx, 'P · ESC · TAP TO RESUME', 468, 12, 'rgba(255,255,255,0.62)', 700);
  ctx.restore();
}

function maxMult(game) {
  const m = game.maxMultiplier ?? game.bestMultiplier ?? game.multiplier ?? 1;
  return Math.round(m);
}

function drawLevelClear(ctx, game, t) {
  const k = clamp((2.4 - (game.levelClearTimer ?? 0)) / 0.35, 0, 1);
  const e = smoothstep(k);
  ctx.save();
  ctx.globalAlpha = e;
  ctx.fillStyle = 'rgba(3,8,14,0.62)';
  ctx.fillRect(0, 0, W, H);
  ctx.translate(0, (1 - e) * 26);
  panel(ctx, 74, 286, W - 148, 232, 150, 0.88);
  glowTitle(ctx, 'LEVEL CLEAR', 330, 32, 145, t, 20);
  statRows(ctx, [
    ['SCORE', formatScore(game.score), 48],
    ['BEST OVERTOP RUN', `${(game.bestOvertop || 0).toFixed(1)}s`, 186],
    ['MAX MULTIPLIER', `x${maxMult(game)}`, 328],
    ['BRICKS BROKEN', formatScore(game.stats?.bricksBroken ?? 0), 190],
  ], 104, 372, W - 208, 32);
  centerText(ctx, `NEXT: LEVEL ${game.level + 1}`, 500, 12, hsl(145, 90, 76, 0.8), 800);
  ctx.restore();
}

function drawGameOver(ctx, game, t) {
  ctx.save();
  const scrim = cachedGrad(ctx, 'goscrim', (c) => {
    const gr = c.createLinearGradient(0, 0, 0, H);
    gr.addColorStop(0, 'rgba(14,2,10,0.82)');
    gr.addColorStop(1, 'rgba(3,2,8,0.94)');
    return gr;
  });
  ctx.fillStyle = scrim;
  ctx.fillRect(0, 0, W, H);
  glowTitle(ctx, 'GAME OVER', 286, 46, 352, t, 26);

  panel(ctx, 66, 320, W - 132, 262, 340, 0.86);
  statRows(ctx, [
    ['FINAL SCORE', formatScore(game.score), 48],
    ['HIGH SCORE', formatScore(game.highScore), 190],
    ['LEVEL REACHED', game.level, 200],
    ['BRICKS BROKEN', formatScore(game.stats?.bricksBroken ?? 0), 320],
    ['BROKEN OVERTOP', formatScore(game.stats?.overtopBricks ?? 0), 186],
    ['BEST OVERTOP RUN', `${(game.bestOvertop || 0).toFixed(1)}s`, 186],
  ], 96, 360, W - 192, 36);

  if (game.newHighScore) {
    const p = 0.5 + 0.5 * Math.sin(t * 6);
    ctx.save();
    ctx.globalAlpha = 0.7 + p * 0.3;
    panel(ctx, W / 2 - 110, 604, 220, 40, 48, 0.7);
    centerText(ctx, 'NEW HIGH SCORE!', 630, 15, hsl(48, 100, 78), 900, DISPLAY);
    ctx.restore();
  }
  const pulse = 0.5 + 0.5 * Math.sin(t * 4);
  centerText(ctx, 'TAP · CLICK · SPACE TO PLAY AGAIN', 700, 14, hsl(190, 100, 76, 0.5 + pulse * 0.5), 800);
  ctx.restore();
}

function drawSettings(ctx, game, t) {
  ctx.save();
  ctx.fillStyle = 'rgba(4,6,16,0.86)';
  ctx.fillRect(0, 0, W, H);
  glowTitle(ctx, 'SETTINGS', 300, 30, 200, t, 18);
  const rows = [
    ['toggleDrag', `TOUCH MODE: ${game.dragMode ? 'DRAG' : 'THUMBSTICK'}`],
    ['toggleMute', `SOUND: ${game.muted ? 'OFF' : 'ON'}`],
    ['toggleFx', `FX: ${fx.lite ? 'LITE' : 'FULL'}`],
    ['closeSettings', 'CLOSE'],
  ];
  const btns = getButtons(game);
  for (const [id, label] of rows) {
    const b = btns.find((x) => x.id === id);
    if (!b) continue;
    const hot = hitButton(b);
    const pressed = hot && pointer.down;
    const hue = id === 'closeSettings' ? 200 : 190;
    panel(ctx, b.x, b.y, b.w, b.h, hue, pressed ? 0.95 : 0.72);
    if (hot) {
      ctx.save();
      ctx.globalCompositeOperation = 'lighter';
      ctx.globalAlpha = pressed ? 0.3 : 0.16;
      ctx.fillStyle = hsl(hue, 100, 62);
      rr(ctx, b.x, b.y, b.w, b.h, 18);
      ctx.fill();
      ctx.restore();
    }
    ctx.textAlign = 'center';
    ctx.font = `800 15px ${MONO}`;
    ctx.fillStyle = hot ? '#fff' : 'rgba(255,255,255,0.9)';
    ctx.fillText(label, b.x + b.w / 2, b.y + b.h / 2 + 5);
  }
  centerText(ctx, 'LITE FX = fewer particles, no aurora (slow phones)', 660, 10, 'rgba(255,255,255,0.4)', 600);
  ctx.restore();
}

function drawBanner(ctx, game) {
  const b = game.banner;
  if (!b) return;
  const k = clamp(b.t / b.life, 0, 1);
  const a = k < 0.12 ? k / 0.12 : k > 0.82 ? (1 - k) / 0.18 : 1;
  const slide = (1 - Math.min(1, k / 0.15)) * 40;
  ctx.save();
  ctx.globalAlpha = clamp(a, 0, 1);
  ctx.translate(0, slide);
  glowTitle(ctx, b.title, 566, 40, b.hue, game.realTime || 0, 22);
  if (b.sub) centerText(ctx, b.sub, 598, 15, hsl(b.hue, 90, 78, 0.92), 800);
  ctx.restore();
}

function drawToast(ctx, game, t) {
  const to = game.toast;
  if (!to) return;
  const k = clamp(to.t / to.life, 0, 1);
  const a = k > 0.7 ? (1 - k) / 0.3 : k < 0.08 ? k / 0.08 : 1;
  const pop = k < 0.16 ? 1 + (1 - k / 0.16) * 0.4 : 1;
  ctx.save();
  ctx.globalAlpha = clamp(a, 0, 1);
  const y = 682 - k * 26;
  ctx.translate(W / 2, y);
  ctx.scale(pop, pop);
  ctx.translate(-W / 2, -y);
  glowTitle(ctx, to.text, y, 27, to.hue, t, 20);
  ctx.restore();
}

function drawStick(ctx, input) {
  const s = input.stick;
  if (!s.active) return;
  ctx.save();
  ctx.globalAlpha = 0.5;
  ctx.strokeStyle = hsl(190, 90, 72);
  ctx.lineWidth = 2.5;
  ctx.beginPath();
  ctx.arc(s.ox, s.oy, 74, 0, TAU);
  ctx.stroke();
  ctx.globalAlpha = 0.14;
  ctx.fillStyle = hsl(190, 90, 60);
  ctx.fill();
  const kx = s.ox + s.dx * 74;
  const ky = s.oy + s.dy * 74;
  const spr = glowSprite(190, 100, 62);
  if (spr) {
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    ctx.globalAlpha = 0.5;
    ctx.drawImage(spr, kx - 40, ky - 40, 80, 80);
    ctx.restore();
  }
  ctx.globalAlpha = 0.9;
  ctx.fillStyle = hsl(190, 100, 74);
  ctx.beginPath();
  ctx.arc(kx, ky, 26, 0, TAU);
  ctx.fill();
  ctx.restore();
}

// ---------------------------------------------------------------- post pass

function drawVignette(ctx, game, t, I) {
  const vig = cachedGrad(ctx, 'vignette', (c) => {
    const gr = c.createRadialGradient(W / 2, H * 0.44, H * 0.24, W / 2, H * 0.44, H * 0.78);
    gr.addColorStop(0, 'rgba(0,0,0,0)');
    gr.addColorStop(0.65, 'rgba(0,0,0,0.24)');
    gr.addColorStop(1, 'rgba(2,2,8,0.62)');
    return gr;
  });
  ctx.save();
  ctx.globalAlpha = 0.75 + fx.beat * 0.12 * I;
  ctx.fillStyle = vig;
  ctx.fillRect(0, 0, W, H);
  ctx.restore();

  // Coloured screen-edge glow: the whole screen reacts when you are up top.
  const e = clamp(zoneEnergy * (0.45 + I * 0.75), 0, 1);
  if (e > 0.02) {
    const hue = qhue(lerp(190, 320, I));
    const eg = cachedGrad(ctx, `edge|${hue}`, (c) => {
      const gr = c.createRadialGradient(W / 2, H * 0.45, H * 0.3, W / 2, H * 0.45, H * 0.8);
      gr.addColorStop(0, hsl(hue, 100, 60, 0));
      gr.addColorStop(0.72, hsl(hue, 100, 58, 0.16));
      gr.addColorStop(1, hsl(hue, 100, 62, 0.5));
      return gr;
    });
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    ctx.globalAlpha = e * (0.55 + 0.45 * Math.sin(t * 5));
    ctx.fillStyle = eg;
    ctx.fillRect(0, 0, W, H);
    ctx.restore();
  }
}

// ---------------------------------------------------------------- main entry

export function render(ctx, game, input, time) {
  const t0 = performance.now();
  lastGame = game;
  dt = lastTime < 0 ? 1 / 60 : clamp(time - lastTime, 0, 0.1);
  lastTime = time;

  const I = clamp(game.intensity || 0, 0, 1);
  const title = game.state === 'title';
  const bgI = title ? 0.26 + 0.06 * Math.sin(time * 0.7) : I;

  // ---- camera: shake (trauma) + roll + zoom punch, clamped to stay readable
  const z = Math.max(1, fx.zoom);
  ctx.save();
  ctx.translate(W / 2 + clamp(fx.shakeX, -22, 22), H / 2 + clamp(fx.shakeY, -22, 22));
  if (fx.shakeRot) ctx.rotate(clamp(fx.shakeRot, -0.02, 0.02));
  if (z !== 1) ctx.scale(z, z);
  ctx.translate(-W / 2, -H / 2);

  drawBackground(ctx, game, time, bgI);
  drawOvertopZone(ctx, game, time, bgI);

  for (const b of game.bricks) {
    if (b.dead) continue;
    drawBrick(ctx, b, time, I);
  }
  for (const c of game.capsules) drawCapsule(ctx, c, time);
  drawBolts(ctx, game);
  if (!title) drawPaddle(ctx, game, time, I);
  if (title) updateDemoBall(game);
  for (const b of game.balls) drawBall(ctx, b, game, time, I);
  if (game.state === 'playing' || game.state === 'serve') emitBallFx(game, I);

  fx.draw(ctx);
  ctx.restore();

  drawVignette(ctx, game, time, I);

  if (!title) drawHud(ctx, game, time, I);
  drawBanner(ctx, game);
  drawToast(ctx, game, time);
  if (input) drawStick(ctx, input);

  if (title) drawTitle(ctx, game, time);
  else if (game.state === 'gameover') drawGameOver(ctx, game, time);
  else if (game.state === 'levelclear') drawLevelClear(ctx, game, time);
  else if (game.state === 'paused' && !game.showSettings) drawPause(ctx, game, time);
  if (game.showSettings) drawSettings(ctx, game, time);
  if (title) drawButtons(ctx, game);

  fx.drawOverlay(ctx, W, H);

  perfBuf[perfI] = performance.now() - t0;
  perfI = (perfI + 1) % perfBuf.length;
  perfN++;
}
