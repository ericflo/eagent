// ---------------------------------------------------------------------------
// render.js — entity drawing (ball, paddle, bricks, powerups, stick)
// ---------------------------------------------------------------------------

import { W, H, WALL, CEIL, COLORS, heatColor, STEEP, SPEED_GATE } from './config.js';
import { TAU, clamp } from './utils.js';
import { PU } from './powerups.js';
import { PHASE_PERIOD } from './bricks.js';

const TYPE_STYLE = {
  '1': { base: '#3e6cf0', hi: '#7aa8ff', lo: '#22348f' },
  '2': { base: '#18b5a8', hi: '#6ff0e2', lo: '#0c5a56' },
  A: { base: '#e0902c', hi: '#ffcd7a', lo: '#7c4708' },
  S: { base: '#33c94e', hi: '#8bff9e', lo: '#0f6a22' },
  P: { base: '#9a5cf0', hi: '#cfa8ff', lo: '#451d78' },
  X: { base: '#e0485a', hi: '#ff9dab', lo: '#6e1420' },
  U: { base: '#f0c33e', hi: '#ffe98a', lo: '#8a6608' },
  W: { base: '#4fb8e8', hi: '#a8e6ff', lo: '#1b5e80' },
};

// Standard bricks step through a cool hue ramp per row (indigo → blue → teal),
// so fields read as layered bands instead of a flat single color.
const ROW_HUES = [224, 208, 192, 238, 176, 252];
export function rowStyle(row) {
  const h = ROW_HUES[((row % ROW_HUES.length) + ROW_HUES.length) % ROW_HUES.length];
  const s = 76 + ((row * 37) % 12);
  return {
    base: `hsl(${h} ${s}% 56%)`,
    hi: `hsl(${h} ${s + 8}% 74%)`,
    lo: `hsl(${h} ${s + 4}% 26%)`,
    hue: h,
  };
}
/** resolved visual style for a brick (row ramp for standard type) */
export function brickStyle(br) {
  if (br.type === '1') return rowStyle(br.row);
  return TYPE_STYLE[br.type] || TYPE_STYLE['1'];
}

// --- ball -------------------------------------------------------------------

export function drawBall(ctx, b, heat, t) {
  const r = b.r;
  ctx.save();
  // trail
  const n = b.trail.length;
  for (let i = 0; i < n; i++) {
    const p = b.trail[i];
    const f = (i + 1) / n;
    ctx.globalAlpha = f * f * 0.5;
    ctx.fillStyle = heat > 0.02 ? heatColor(heat, 90, 64) : '#7dd6ff';
    ctx.beginPath();
    ctx.arc(p.x, p.y, r * f * 0.85, 0, TAU);
    ctx.fill();
  }
  ctx.globalAlpha = 1;

  const col = heat > 0.02 ? heatColor(Math.min(1, heat), 92, 66) : '#eaf4ff';
  if (b.power === 'fire') {
    ctx.shadowColor = '#ff6a2a'; ctx.shadowBlur = 26;
    ctx.fillStyle = '#ffb35c';
  } else if (b.power === 'pierce') {
    ctx.shadowColor = '#aaff44'; ctx.shadowBlur = 24;
    ctx.fillStyle = '#d6ff8a';
  } else {
    ctx.shadowColor = col; ctx.shadowBlur = 14 + heat * 22;
    ctx.fillStyle = col;
  }
  ctx.beginPath();
  ctx.arc(b.x, b.y, r, 0, TAU);
  ctx.fill();
  ctx.shadowBlur = 0;
  // highlight
  ctx.fillStyle = 'rgba(255,255,255,0.85)';
  ctx.beginPath();
  ctx.arc(b.x - r * 0.3, b.y - r * 0.32, r * 0.34, 0, TAU);
  ctx.fill();
  if (b.power === 'giant') {
    ctx.strokeStyle = 'rgba(192,132,252,0.8)';
    ctx.lineWidth = 2;
    ctx.beginPath(); ctx.arc(b.x, b.y, r + 4 + Math.sin(t * 9) * 1.5, 0, TAU); ctx.stroke();
  }
  ctx.restore();
}

// --- paddle -----------------------------------------------------------------

export function drawPaddle(ctx, p, heat, t) {
  const w = p.w * p.sx, h = p.h * p.sy;
  const x = p.x - w / 2, y = p.y - h / 2;
  ctx.save();
  ctx.translate(p.x, p.y);
  ctx.rotate(p.tilt);
  ctx.translate(-p.x, -p.y);

  const grad = ctx.createLinearGradient(x, y, x, y + h);
  const c1 = heat > 0.05 ? heatColor(heat * 0.8, 85, 62) : '#8fdcff';
  grad.addColorStop(0, '#e8f7ff');
  grad.addColorStop(0.45, c1);
  grad.addColorStop(1, '#2b5f9e');

  ctx.shadowColor = p.magnet ? '#f472b6' : 'rgba(125,214,255,0.8)';
  ctx.shadowBlur = p.magnet ? 22 : 14;
  ctx.fillStyle = p.magnet ? '#f472b6' : grad;
  roundRect(ctx, x, y, w, h, h / 2);
  ctx.fill();
  ctx.shadowBlur = 0;

  if (p.magnet) {
    ctx.strokeStyle = 'rgba(255,182,222,0.9)';
    ctx.lineWidth = 2;
    ctx.setLineDash([4, 5]);
    ctx.beginPath();
    ctx.arc(p.x, p.y, w / 2 + 7 + Math.sin(t * 7) * 2, Math.PI, TAU);
    ctx.stroke();
    ctx.setLineDash([]);
  }
  // glossy top
  ctx.fillStyle = 'rgba(255,255,255,0.35)';
  roundRect(ctx, x + 6, y + 2, w - 12, h * 0.32, h * 0.16);
  ctx.fill();
  ctx.restore();
}

// --- powerup capsule ----------------------------------------------------------

export function drawPowerup(ctx, pu, t) {
  const info = PU[pu.id] || { label: '?', color: '#fff' };
  ctx.save();
  ctx.translate(pu.x, pu.y);
  const pulse = 1 + Math.sin(pu.t * 8) * 0.06;
  ctx.scale(pulse, 1 / pulse);
  ctx.shadowColor = info.color; ctx.shadowBlur = 16;
  ctx.fillStyle = 'rgba(10,14,26,0.92)';
  ctx.strokeStyle = info.color;
  ctx.lineWidth = 2.5;
  roundRect(ctx, -24, -15, 48, 30, 15);
  ctx.fill(); ctx.stroke();
  ctx.shadowBlur = 0;
  ctx.fillStyle = info.color;
  ctx.font = '800 13px ui-rounded, system-ui, sans-serif';
  ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
  ctx.fillText(info.label, 0, 1);
  ctx.restore();
}

// --- bricks -------------------------------------------------------------------

export function drawBrick(ctx, br, time, heat) {
  if (!br.alive && br.breaking <= 0) return;
  const style = brickStyle(br);
  const { x, y, w, h } = br;

  ctx.save();

  if (!br.alive && br.frags) {
    // fragment shatter
    const p = br.breaking;
    ctx.globalAlpha = 1 - p;
    for (const f of br.frags) {
      ctx.save();
      ctx.translate(f.x, f.y);
      ctx.rotate(f.rot);
      ctx.fillStyle = style.base;
      ctx.globalAlpha = (1 - p) * 0.95;
      ctx.fillRect(-f.w / 2, -f.h / 2, f.w, f.h);
      ctx.fillStyle = style.hi;
      ctx.fillRect(-f.w / 2, -f.h / 2, f.w, 2);
      ctx.restore();
    }
    ctx.restore();
    return;
  }

  const jx = br.shakeT > 0 ? Math.sin(br.shakeT * 70) * 3 * br.shakeT : 0;

  if (br.type === 'W') {
    // spinner: rotating disc with blade gaps
    const cx = br.cx + jx, cy = br.cy, R = Math.min(w, h) / 2 + 3;
    ctx.translate(cx, cy);
    ctx.rotate(br.spin);
    ctx.shadowColor = style.hi; ctx.shadowBlur = 10;
    ctx.fillStyle = style.base;
    ctx.beginPath();
    for (let i = 0; i < 3; i++) {
      const a0 = (i / 3) * TAU + 0.22, a1 = ((i + 1) / 3) * TAU - 0.22;
      ctx.arc(0, 0, R, a0, a1);
      ctx.lineTo(0, 0);
    }
    ctx.closePath();
    ctx.fill();
    ctx.shadowBlur = 0;
    ctx.fillStyle = style.hi;
    ctx.beginPath(); ctx.arc(0, 0, R * 0.32, 0, TAU); ctx.fill();
    ctx.restore();
    return;
  }

  if (br.type === 'P') {
    const ph = (time + br.phaseOff) % PHASE_PERIOD;
    const solid = ph < 1.15;
    const edge = solid ? Math.min((1.15 - ph) / 0.14, (ph) / 0.14, 1) : 1 - Math.min((PHASE_PERIOD - ph) / 0.1, 1);
    ctx.globalAlpha = solid ? 0.55 + 0.45 * clamp(edge, 0, 1) : 0.16 + 0.1 * Math.sin(time * 30);
    ctx.setLineDash(solid ? [] : [5, 4]);
    ctx.strokeStyle = style.hi;
    ctx.lineWidth = 1.5;
  }

  // body
  const grad = ctx.createLinearGradient(x, y, x, y + h);
  grad.addColorStop(0, style.hi);
  grad.addColorStop(0.5, style.base);
  grad.addColorStop(1, style.lo);
  ctx.shadowColor = style.base;
  ctx.shadowBlur = br.flash > 0 ? 22 : (br.type === 'U' ? 10 + Math.sin(time * 4 + br.pulse) * 6 : 6);
  ctx.fillStyle = grad;
  roundRect(ctx, x + jx, y, w, h, 5);
  ctx.fill();
  ctx.shadowBlur = 0;

  if (br.type === 'P') { ctx.stroke(); ctx.setLineDash([]); ctx.globalAlpha = 1; }

  // gloss
  ctx.fillStyle = 'rgba(255,255,255,0.22)';
  roundRect(ctx, x + jx + 3, y + 2.5, w - 6, h * 0.3, 3);
  ctx.fill();

  // type glyphs
  ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
  const gx = br.cx + jx, gy = br.cy;
  if (br.type === 'A' || br.type === 'B') {
    // steep-angle arrows (chevrons pointing down)
    const ok = br.flash > 0;
    ctx.strokeStyle = ok ? '#ffffff' : 'rgba(255,240,210,0.95)';
    ctx.lineWidth = 2.4;
    ctx.lineCap = 'round';
    for (const off of [-6, 6]) {
      ctx.beginPath();
      ctx.moveTo(gx - 7, gy + off - 4);
      ctx.lineTo(gx, gy + off + 3);
      ctx.lineTo(gx + 7, gy + off - 4);
      ctx.stroke();
    }
  } else if (br.type === 'S') {
    // lightning bolt; glow when ball is fast enough handled via ignite
    ctx.fillStyle = br.ignite > 0.5 ? '#f6ffb0' : 'rgba(255,255,255,0.85)';
    ctx.shadowColor = '#eaff7a';
    ctx.shadowBlur = br.ignite > 0.5 ? 14 : 0;
    ctx.beginPath();
    ctx.moveTo(gx + 3, gy - 8);
    ctx.lineTo(gx - 5, gy + 1);
    ctx.lineTo(gx - 1, gy + 1);
    ctx.lineTo(gx - 3, gy + 8);
    ctx.lineTo(gx + 5, gy - 2);
    ctx.lineTo(gx + 1, gy - 2);
    ctx.closePath();
    ctx.fill();
    ctx.shadowBlur = 0;
  } else if (br.type === 'X') {
    // armored: rivets + shield frame
    ctx.strokeStyle = 'rgba(255,220,225,0.9)';
    ctx.lineWidth = 2;
    roundRect(ctx, x + jx + 3, y + 3, w - 6, h - 6, 4);
    ctx.stroke();
    ctx.fillStyle = '#ffd9de';
    for (const dx of [-10, 10]) { ctx.beginPath(); ctx.arc(gx + dx, gy, 2.2, 0, TAU); ctx.fill(); }
    ctx.font = '900 10px system-ui'; ctx.fillStyle = '#ffd9de';
    ctx.fillText('★', gx, gy + 1);
  } else if (br.type === 'U') {
    // unstable: pulsing core dot
    const pu2 = 0.5 + 0.5 * Math.sin(time * 5 + br.pulse);
    ctx.fillStyle = `rgba(255,255,255,${0.5 + pu2 * 0.5})`;
    ctx.beginPath(); ctx.arc(gx, gy, 3 + pu2 * 2, 0, TAU); ctx.fill();
  } else if (br.type === '2') {
    ctx.fillStyle = 'rgba(255,255,255,0.9)';
    ctx.font = '900 12px system-ui';
    ctx.fillText('◆', gx, gy + 1);
  }

  // flash overlay
  if (br.flash > 0) {
    ctx.globalAlpha = br.flash * 0.9;
    ctx.fillStyle = '#ffffff';
    roundRect(ctx, x + jx, y, w, h, 5);
    ctx.fill();
    ctx.globalAlpha = 1;
  }
  // deny feedback (dull cross flash)
  if (br.deny > 0) {
    ctx.globalAlpha = Math.min(1, br.deny * 3) * 0.8;
    ctx.strokeStyle = '#ff5d6e';
    ctx.lineWidth = 2.5;
    const s = 8;
    ctx.beginPath();
    ctx.moveTo(gx - s, gy - s); ctx.lineTo(gx + s, gy + s);
    ctx.moveTo(gx + s, gy - s); ctx.lineTo(gx - s, gy + s);
    ctx.stroke();
    ctx.globalAlpha = 1;
  }
  ctx.restore();
}

export function roundRect(ctx, x, y, w, h, r) {
  r = Math.min(r, Math.abs(w) / 2, Math.abs(h) / 2);
  if (r <= 0) r = 0.1;
  w = Math.abs(w); h = Math.abs(h);
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + w, y, x + w, y + h, r);
  ctx.arcTo(x + w, y + h, x, y + h, r);
  ctx.arcTo(x, y + h, x, y, r);
  ctx.arcTo(x, y, x + w, y, r);
  ctx.closePath();
}

// walls + playfield frame.
// fullBleed (narrow viewports where the field spans the window): the side
// walls render as a translucent veil over the shared backdrop instead of
// opaque slabs — no dark gutters at the screen edges, one continuous field.
export function drawFrame(ctx, heat, t, fullBleed = false) {
  ctx.save();
  const g = ctx.createLinearGradient(0, 0, W, 0);
  const side = heat > 0.05 ? heatColor(heat * 0.7, 70, 30) : '#233156';
  g.addColorStop(0, side);
  g.addColorStop(0.5, heat > 0.05 ? heatColor(heat * 0.5, 60, 38) : '#2d4372');
  g.addColorStop(1, side);
  if (fullBleed) ctx.globalAlpha = 0.10;
  ctx.fillStyle = g;
  ctx.fillRect(0, 0, WALL, H);
  ctx.fillRect(W - WALL, 0, WALL, H);
  // ceiling band (the "rim"). Full-bleed viewports: fade the glow in from
  // ABOVE the field top (no hard edge where the letterbox boundary sits) and
  // span the full width so the rim never reads as an inset bar or a seam.
  const cg = ctx.createLinearGradient(0, fullBleed ? -CEIL * 0.9 : 0, 0, CEIL + 8);
  cg.addColorStop(0, fullBleed ? 'rgba(143,180,255,0)' : (heat > 0.05 ? '#ffd9a0' : '#8fb4ff'));
  if (!fullBleed) cg.addColorStop(0.12, heat > 0.05 ? '#ffd9a0' : '#8fb4ff');
  cg.addColorStop(1, 'rgba(0,0,0,0)');
  ctx.fillStyle = cg;
  if (fullBleed) ctx.globalAlpha = 0.5;
  if (fullBleed) ctx.fillRect(0, -CEIL * 0.9, W, CEIL * 0.9 + CEIL + 8);
  else ctx.fillRect(WALL, 0, W - 2 * WALL, CEIL + 8);
  if (heat > 0.05) {
    ctx.globalAlpha = 0.4 + 0.3 * Math.sin(t * 10);
    ctx.fillStyle = '#ffcf8a';
    ctx.fillRect(WALL, 0, W - 2 * WALL, 3);
    ctx.globalAlpha = 1;
  }
  ctx.restore();
}
