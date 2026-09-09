// src/game/ui.js — Attic Breaker menu/overlay screens (title, howto, settings,
// paused, gameOver). Self-contained ES module, owned entirely by this file.
//
// Everything is drawn procedurally in WORLD space (0..1000 wide, 0..H tall,
// H is a dynamic live binding from ../core/constants.js). No DOM, no images,
// no emoji — glyphs/diagrams are drawn with canvas paths, matching the rest
// of the game's dark-neon identity (deep indigo/near-black + cyan/magenta/
// lime neon accents).
//
// This module is defensive by design: it may be `import()`-ed dynamically by
// the core game loop with optional chaining, and it must never throw even if
// bricks.js/powerups.js are missing or partially implemented.

import { W, H } from '../core/constants.js';
import { clamp, lerp, TAU } from '../core/math.js';

let Bricks = null, Powerups = null;
try { Bricks = await import('./bricks.js'); } catch (e) { /* degrade to text */ }
try { Powerups = await import('./powerups.js'); } catch (e) { /* degrade to text */ }

// ---------------------------------------------------------------------------
// Typography scale (WORLD units). The renderer scales world->screen by
// roughly (viewportWidthPx / 1000) on phone aspect ratios (width-bound), so
// e.g. at a 390px-wide phone, 1 world unit ≈ 0.39 CSS px. To keep body copy
// comfortably readable (>= ~12 CSS px at 390 wide, ideally 13-15px) every
// font size in this file is drawn from this table rather than ad-hoc literal
// pixel values. See tests/ui-shots.mjs for the measured CSS-px table.
//   world -> css@390  css@320  css@430
//   32     12.5       10.2     13.8
//   36     14.0       11.5     15.5
//   38     14.8       12.2     16.3
//   40     15.6       12.8     17.2
//   48     18.7       15.4     20.6
// ---------------------------------------------------------------------------
const FS = {
  h1: 48,     // top-level screen titles
  h2: 40,     // card / section headers
  h3: 36,     // sub-headers, row labels, button labels
  body: 38,   // paragraph / description copy (legend text, hints)
  caption: 32, // secondary meta / footnotes / percentages
};

// Minimum touch-target height, in world units, for anything tappable.
const MIN_TAP = 112;

const PALETTE = {
  bg: '#050409',
  panel: '#06060f',
  cyan: '#5ee6ff',
  magenta: '#ff5ec8',
  lime: '#b0ff5e',
  gold: '#ffd166',
  ink: '#eaf6ff',
};

// ---------------------------------------------------------------------------
// small local helpers
// ---------------------------------------------------------------------------
function worldH(state) {
  const h = state && Number.isFinite(state.worldH) ? state.worldH : H;
  return h || 1500;
}

function motion(state) {
  const reduced = !!(state && state.settings && state.settings.reducedMotion);
  return reduced ? { amp: 0.22, speed: 0.28, on: false } : { amp: 1, speed: 1, on: true };
}

function rr(g, x, y, w, h, r) {
  if (typeof r === 'number') r = { tl: r, tr: r, br: r, bl: r };
  const { tl, tr, br, bl } = r;
  g.beginPath();
  g.moveTo(x + tl, y);
  g.lineTo(x + w - tr, y);
  g.arcTo(x + w, y, x + w, y + tr, tr);
  g.lineTo(x + w, y + h - br);
  g.arcTo(x + w, y + h, x + w - br, y + h, br);
  g.lineTo(x + bl, y + h);
  g.arcTo(x, y + h, x, y + h - bl, bl);
  g.lineTo(x + tl, y);
  g.arcTo(x, y, x + tl, y, tl);
  g.closePath();
}

function hexA(hex, a) {
  if (!hex || hex[0] !== '#') return `rgba(255,255,255,${a})`;
  let h = hex.slice(1);
  if (h.length === 3) h = h.split('').map((c) => c + c).join('');
  const r = parseInt(h.slice(0, 2), 16) || 0;
  const gg = parseInt(h.slice(2, 4), 16) || 0;
  const b = parseInt(h.slice(4, 6), 16) || 0;
  return `rgba(${r},${gg},${b},${a})`;
}

function fmtInt(n) { return Math.round(n || 0).toLocaleString(); }
function fmtTime(sec) {
  sec = Math.max(0, sec || 0);
  const m = Math.floor(sec / 60), s = Math.floor(sec % 60);
  return `${m}:${String(s).padStart(2, '0')}`;
}

// ---------------------------------------------------------------------------
// Internal UI state: registered hit-regions per frame, hover/press ids,
// animation clock, howto page index, settings drag state.
// ---------------------------------------------------------------------------
const S = {
  cfg: null,
  t: 0,
  page: 0,             // howto page index
  pageCount: 3,
  hoverId: null,
  pressId: null,
  lastScreen: null,
  regions: [],          // [{id, x, y, w, h, shape:'rect'|'circle', screen}] rebuilt every draw()
  sliderDrag: null,      // 'sfx' | 'music' | null (for continuous drag - caller only gives us discrete hit() calls, but we support it)
};

function resetRegions() { S.regions.length = 0; }
function region(id, x, y, w, h) { S.regions.push({ id, x, y, w, h }); }

function findRegion(x, y) {
  // iterate in reverse so later-drawn (topmost) regions win
  for (let i = S.regions.length - 1; i >= 0; i--) {
    const r = S.regions[i];
    if (x >= r.x && x <= r.x + r.w && y >= r.y && y <= r.y + r.h) return r;
  }
  return null;
}

function playClick() {
  try { S.cfg?.audio?.sfx?.('uiClick'); } catch (e) { /* ignore */ }
}
function playMove() {
  try { S.cfg?.audio?.sfx?.('menuMove'); } catch (e) { /* ignore */ }
}

// ---------------------------------------------------------------------------
// Button widget — draws a glowing rounded-rect button, registers a hit
// region, and returns whether it's currently hovered/pressed (for callers
// that want extra flourish). Buttons are drawn bottom-anchored press feel.
// ---------------------------------------------------------------------------
function button(g, id, x, y, w, h, label, opts = {}) {
  const {
    color = PALETTE.cyan,
    sub = null,
    big = false,
    filled = false,
  } = opts;
  region(id, x, y, w, h);
  const hover = S.hoverId === id;
  const pressed = S.pressId === id;
  const press = pressed ? 0.95 : hover ? 1.025 : 1;
  const cx = x + w / 2, cy = y + h / 2;
  g.save();
  g.translate(cx, cy);
  g.scale(press, press);
  g.translate(-cx, -cy);

  // glow halo
  g.save();
  g.globalCompositeOperation = 'lighter';
  const glowA = hover ? 0.42 : 0.24;
  const grad = g.createRadialGradient(cx, cy, 1, cx, cy, w * 0.7);
  grad.addColorStop(0, hexA(color, glowA));
  grad.addColorStop(1, hexA(color, 0));
  g.fillStyle = grad;
  g.fillRect(x - w * 0.4, y - h * 0.6, w * 1.8, h * 2.2);
  g.restore();

  rr(g, x, y, w, h, Math.min(22, h * 0.24));
  const bg = g.createLinearGradient(x, y, x, y + h);
  if (filled) {
    bg.addColorStop(0, hexA(color, 0.95));
    bg.addColorStop(1, hexA(color, 0.62));
  } else {
    bg.addColorStop(0, hexA(color, hover ? 0.30 : 0.17));
    bg.addColorStop(1, hexA(color, hover ? 0.15 : 0.07));
  }
  g.fillStyle = bg;
  g.fill();
  g.lineWidth = big ? 3 : 2.25;
  g.strokeStyle = hexA(color, hover ? 1 : 0.85);
  g.stroke();
  // top glass highlight
  g.save();
  rr(g, x + 3, y + 3, w - 6, h * 0.42, Math.min(16, h * 0.2));
  const gloss = g.createLinearGradient(x, y, x, y + h * 0.42);
  gloss.addColorStop(0, hexA('#ffffff', filled ? 0.28 : 0.10));
  gloss.addColorStop(1, hexA('#ffffff', 0));
  g.fillStyle = gloss;
  g.fill();
  g.restore();

  g.textAlign = 'center';
  g.textBaseline = 'middle';
  g.fillStyle = filled ? '#0a0912' : '#f5fbff';
  const labelSize = Math.round(Math.max(big ? FS.h2 : FS.h3, h * (big ? 0.32 : 0.30)));
  g.font = `${big ? 800 : 700} ${labelSize}px system-ui, -apple-system, sans-serif`;
  g.fillText(label, cx, cy + (sub ? -h * 0.15 : 0));
  if (sub) {
    const subSize = Math.round(Math.max(FS.caption, h * 0.15));
    g.font = `600 ${subSize}px system-ui, sans-serif`;
    g.fillStyle = filled ? 'rgba(10,9,18,0.72)' : 'rgba(245,251,255,0.68)';
    g.fillText(sub, cx, cy + h * 0.26);
  }
  g.restore();
}

// toggle switch row: label left, switch right. Returns nothing; registers hit region.
function toggleRow(g, id, x, y, w, h, label, on) {
  region(id, x, y, w, h);
  const hover = S.hoverId === id;
  g.save();
  rr(g, x, y, w, h, 16);
  g.fillStyle = hover ? 'rgba(255,255,255,0.07)' : 'rgba(255,255,255,0.035)';
  g.fill();
  g.lineWidth = 1.5;
  g.strokeStyle = hover ? 'rgba(255,255,255,0.18)' : 'rgba(255,255,255,0.08)';
  g.stroke();
  g.textAlign = 'left';
  g.textBaseline = 'middle';
  g.font = `700 ${FS.h3}px system-ui, sans-serif`;
  g.fillStyle = PALETTE.ink;
  g.fillText(label, x + 26, y + h / 2);

  // switch graphic, right-aligned
  const swW = 108, swH = 52;
  const sx = x + w - swW - 22, sy = y + h / 2 - swH / 2;
  rr(g, sx, sy, swW, swH, swH / 2);
  const onColor = PALETTE.cyan;
  g.fillStyle = on ? hexA(onColor, 0.85) : 'rgba(255,255,255,0.12)';
  g.fill();
  g.lineWidth = 2;
  g.strokeStyle = on ? onColor : 'rgba(255,255,255,0.35)';
  g.stroke();
  const knobR = swH / 2 - 6;
  const knobX = on ? sx + swW - swH / 2 : sx + swH / 2;
  g.beginPath();
  g.arc(knobX, sy + swH / 2, knobR, 0, TAU);
  g.fillStyle = on ? '#08202a' : '#cfd6de';
  g.fill();
  g.restore();
}

// slider row: label + track + filled portion + knob. Returns hit-testable id
// prefix; hit() computes value from x position within the track.
function sliderRow(g, id, x, y, w, h, label, value) {
  region(id, x, y, w, h);
  const hover = S.hoverId === id;
  g.save();
  rr(g, x, y, w, h, 16);
  g.fillStyle = hover ? 'rgba(255,255,255,0.07)' : 'rgba(255,255,255,0.035)';
  g.fill();
  g.lineWidth = 1.5;
  g.strokeStyle = hover ? 'rgba(255,255,255,0.18)' : 'rgba(255,255,255,0.08)';
  g.stroke();
  g.textAlign = 'left';
  g.textBaseline = 'middle';
  g.font = `700 ${FS.h3}px system-ui, sans-serif`;
  g.fillStyle = PALETTE.ink;
  g.fillText(label, x + 26, y + h * 0.32);
  g.font = `700 ${FS.caption}px system-ui, sans-serif`;
  g.fillStyle = 'rgba(255,255,255,0.65)';
  g.textAlign = 'right';
  g.fillText(`${Math.round(clamp(value, 0, 1) * 100)}%`, x + w - 26, y + h * 0.32);

  const trackX = x + 26, trackY = y + h * 0.62, trackW = w - 52, trackH = 16;
  rr(g, trackX, trackY, trackW, trackH, trackH / 2);
  g.fillStyle = 'rgba(255,255,255,0.12)';
  g.fill();
  const fillW = trackW * clamp(value, 0, 1);
  if (fillW > 1) {
    rr(g, trackX, trackY, fillW, trackH, trackH / 2);
    const grad = g.createLinearGradient(trackX, 0, trackX + trackW, 0);
    grad.addColorStop(0, PALETTE.cyan);
    grad.addColorStop(1, PALETTE.magenta);
    g.fillStyle = grad;
    g.fill();
  }
  const knobX = trackX + fillW;
  g.beginPath();
  g.arc(knobX, trackY + trackH / 2, 18, 0, TAU);
  g.fillStyle = '#fff';
  g.fill();
  g.strokeStyle = PALETTE.cyan;
  g.lineWidth = 2.5;
  g.stroke();
  g.restore();
  // stash track geometry on the region for accurate hit()->value mapping
  S.regions[S.regions.length - 1].track = { x: trackX, w: trackW };
}

// ---------------------------------------------------------------------------
// panel background helper — translucent rounded card so attract-mode visuals
// behind (title screen) remain partly visible.
// ---------------------------------------------------------------------------
function panel(g, x, y, w, h, opts = {}) {
  const { alpha = 0.6, stroke = 'rgba(120,220,255,0.28)' } = opts;
  g.save();
  rr(g, x, y, w, h, 26);
  const grad = g.createLinearGradient(x, y, x, y + h);
  grad.addColorStop(0, `rgba(10,10,22,${Math.min(1, alpha + 0.08)})`);
  grad.addColorStop(1, `rgba(4,4,10,${alpha})`);
  g.fillStyle = grad;
  g.fill();
  g.lineWidth = 1.5;
  g.strokeStyle = stroke;
  g.stroke();
  // faint inner top highlight for a glassy shipped-product feel
  g.save();
  rr(g, x + 2, y + 2, w - 4, Math.min(60, h * 0.2), 22);
  const gloss = g.createLinearGradient(x, y, x, y + h * 0.2);
  gloss.addColorStop(0, 'rgba(255,255,255,0.06)');
  gloss.addColorStop(1, 'rgba(255,255,255,0)');
  g.fillStyle = gloss;
  g.fill();
  g.restore();
  g.restore();
}

/** Panel sized to content, then vertically centered — avoids both dead
 * zones at tall world heights and overflow at short ones. */
function fitPanel(h, { header, footer, contentMin, contentMax, contentFrac = 0.4, minPh, maxPh }) {
  const naturalContent = clamp(h * contentFrac, contentMin, contentMax);
  const desired = header + naturalContent + footer;
  const ph = clamp(desired, minPh ?? h * 0.5, maxPh ?? h * 0.94);
  const contentH = ph - header - footer;
  const py = clamp((h - ph) / 2, h * 0.015, Math.max(h * 0.015, h - ph - h * 0.015));
  return { ph, py, contentH };
}

// ===========================================================================
// TITLE SCREEN
// ===========================================================================
function drawTitle(g, screen, state, t) {
  const h = worldH(state);
  const cx = W / 2;
  const rm = motion(state);
  // The core game draws a self-playing "attract mode" demo behind this whole
  // screen. Default to true (per contract) whenever the flag is unset so we
  // never clutter the mid-screen void when the caller forgets to pass it.
  const attractBehind = state?.attractBehind !== false;

  drawTitleBackdrop(g, cx, h, t, attractBehind, rm);

  // ---- logo, upper area, translucent so attract-mode plays behind it ----
  const title = 'ATTIC BREAKER';
  const logoY = h * 0.135;
  g.save();
  g.textAlign = 'center';
  g.textBaseline = 'alphabetic';
  const baseSize = Math.min(84, W / (title.length * 0.6));
  g.font = `900 ${baseSize}px system-ui, -apple-system, sans-serif`;
  let totalW = 0;
  const widths = [];
  for (const ch of title) { const w = g.measureText(ch).width; widths.push(w); totalW += w; }
  let cxAcc = cx - totalW / 2;
  const sweep = (Math.sin(t * 0.6 * rm.speed) * 0.5 + 0.5);
  const grad = g.createLinearGradient(cx - 260, 0, cx + 260, 0);
  grad.addColorStop(0, PALETTE.cyan);
  grad.addColorStop(clamp(sweep, 0.05, 0.95), PALETTE.magenta);
  grad.addColorStop(1, PALETTE.lime);
  g.save();
  g.globalCompositeOperation = 'lighter';
  g.fillStyle = hexA(PALETTE.cyan, 0.32);
  g.font = `900 ${baseSize}px system-ui, sans-serif`;
  g.fillText(title, cx, logoY + 6);
  g.restore();
  for (let i = 0; i < title.length; i++) {
    const ch = title[i];
    const bob = Math.sin(t * 2.2 * rm.speed + i * 0.4) * 3.5 * rm.amp;
    const x = cxAcc + widths[i] / 2;
    g.save();
    g.translate(x, logoY + bob);
    g.fillStyle = grad;
    g.fillText(ch, 0, 0);
    g.restore();
    cxAcc += widths[i];
  }
  g.restore();

  // ---- tagline: sells the core fantasy in one line ----
  g.save();
  g.textAlign = 'center';
  g.font = `700 ${FS.body}px system-ui, sans-serif`;
  g.fillStyle = 'rgba(255,255,255,0.9)';
  g.fillText('Smash a path UP. Ride the ball above the bricks.', cx, logoY + 52);
  g.font = `600 ${FS.caption}px system-ui, sans-serif`;
  g.fillStyle = hexA(PALETTE.lime, 0.9);
  g.fillText('That\u2019s where the score multiplier explodes. \u2191', cx, logoY + 86);
  g.restore();

  // ---- high score chip ----
  g.save();
  g.textAlign = 'center';
  const chipW = Math.min(380, W * 0.72);
  panel(g, cx - chipW / 2, logoY + 106, chipW, 56, { alpha: 0.42 });
  g.font = `800 ${FS.caption}px system-ui, sans-serif`;
  g.fillStyle = PALETTE.gold;
  g.fillText(`HIGH SCORE  ${fmtInt(state?.highScore)}`, cx, logoY + 140);
  g.restore();

  // ---- buttons, lower half ----
  const playW = Math.min(440, W * 0.66), playH = 128;
  const playY = h * 0.615;
  const pulse = 1 + Math.sin(t * 3 * rm.speed) * 0.02 * rm.amp;
  g.save();
  g.translate(cx, playY + playH / 2);
  g.scale(pulse, pulse);
  g.translate(-cx, -(playY + playH / 2));
  button(g, 'play', cx - playW / 2, playY, playW, playH, 'PLAY', { color: PALETTE.cyan, filled: true, big: true, sub: 'tap anywhere to start' });
  g.restore();

  const smallW = Math.min(230, W * 0.44), smallH = MIN_TAP, gap = 18;
  const rowY = playY + playH + 26;
  button(g, 'howto', cx - smallW - gap / 2, rowY, smallW, smallH, 'HOW TO PLAY', { color: PALETTE.magenta });
  button(g, 'settings', cx + gap / 2, rowY, smallW, smallH, 'SETTINGS', { color: PALETTE.lime });

  g.save();
  g.textAlign = 'center';
  g.font = `600 ${FS.caption}px system-ui, sans-serif`;
  g.fillStyle = 'rgba(255,255,255,0.5)';
  g.fillText('mouse \u00b7 touch \u00b7 WASD/arrows \u00b7 gamepad  \u2014  v1.0', cx, h - 30);
  g.restore();
}

/**
 * Backdrop behind the title UI. When `attractBehind` is true the real game
 * renders a self-playing attract-mode demo underneath this whole draw call,
 * so we draw NO filler content in the mid-screen void — only a soft vignette
 * and legibility gradients so the text/buttons read clearly over the moving
 * gameplay. When there is no attract-mode layer (fallback), we draw a single
 * tasteful, slow-moving glow flourish near the logo instead of scattered
 * icons.
 */
function drawTitleBackdrop(g, cx, h, t, attractBehind, rm) {
  g.save();
  // legibility gradients: dark bands behind logo/tagline area and behind the
  // button stack, regardless of what's underneath.
  const topBand = g.createLinearGradient(0, 0, 0, h * 0.34);
  topBand.addColorStop(0, 'rgba(3,3,9,0.82)');
  topBand.addColorStop(1, 'rgba(3,3,9,0)');
  g.fillStyle = topBand;
  g.fillRect(0, 0, W, h * 0.34);

  const botBand = g.createLinearGradient(0, h * 0.54, 0, h);
  botBand.addColorStop(0, 'rgba(3,3,9,0)');
  botBand.addColorStop(1, 'rgba(3,3,9,0.88)');
  g.fillStyle = botBand;
  g.fillRect(0, h * 0.54, W, h * 0.46);

  // full-frame vignette so edges recede whether or not gameplay plays behind
  const vg = g.createRadialGradient(cx, h * 0.42, h * 0.1, cx, h * 0.42, h * 0.68);
  vg.addColorStop(0, 'rgba(3,3,9,0)');
  vg.addColorStop(1, 'rgba(2,2,7,0.5)');
  g.fillStyle = vg;
  g.fillRect(0, 0, W, h);
  g.restore();

  if (attractBehind) return; // gameplay demo shows through the clear middle

  // ---- fallback flourish: soft breathing halo + a handful of slow bokeh
  // orbs drifting near the logo. Tasteful, not "random clutter". ----
  g.save();
  g.globalCompositeOperation = 'lighter';
  const bob = Math.sin(t * 0.5 * rm.speed) * 10 * rm.amp;
  const glow = g.createRadialGradient(cx, h * 0.16 + bob, 10, cx, h * 0.16 + bob, 360);
  glow.addColorStop(0, hexA(PALETTE.cyan, 0.18));
  glow.addColorStop(1, hexA(PALETTE.cyan, 0));
  g.fillStyle = glow;
  g.fillRect(cx - 380, h * 0.16 - 380 + bob, 760, 760);
  const orbColors = [PALETTE.cyan, PALETTE.magenta, PALETTE.lime];
  for (let i = 0; i < 5; i++) {
    const a = t * 0.06 * rm.speed + i * 1.4;
    const r = 200 + i * 42;
    const x = cx + Math.cos(a) * r * 0.9;
    const y = h * 0.47 + Math.sin(a * 1.3) * h * 0.15;
    const rad = 48 + i * 7;
    const og = g.createRadialGradient(x, y, 1, x, y, rad);
    og.addColorStop(0, hexA(orbColors[i % orbColors.length], 0.12));
    og.addColorStop(1, hexA(orbColors[i % orbColors.length], 0));
    g.fillStyle = og;
    g.beginPath(); g.arc(x, y, rad, 0, TAU); g.fill();
  }
  g.restore();
}

// ===========================================================================
// HOWTO SCREEN
// ===========================================================================
function howtoPanelRect(h) {
  // Content-fit + centered: stops both dead zones at tall H and overflow at
  // short H. Natural content budget is capped so the panel never balloons
  // past what the content actually needs on very tall phones.
  const header = 104, footer = 168;
  const { ph, py, contentH } = fitPanel(h, {
    header, footer, contentFrac: 0.62, contentMin: 620, contentMax: 1150,
    minPh: h * 0.62, maxPh: h * 0.92,
  });
  const pw = Math.min(940, W - 32);
  const px = W / 2 - pw / 2;
  return { px, py, pw, ph, contentY: py + header, contentH, header, footer };
}

function drawHowto(g, screen, state, t) {
  const h = worldH(state);
  const { px, py, pw, ph, contentY, contentH } = howtoPanelRect(h);
  panel(g, px, py, pw, ph, { alpha: 0.76 });

  g.save();
  g.textAlign = 'center';
  g.font = `800 ${FS.h1}px system-ui, sans-serif`;
  g.fillStyle = '#8ffcff';
  g.fillText('HOW TO PLAY', W / 2, py + 58);
  g.restore();

  const page = clamp(S.page | 0, 0, S.pageCount - 1);
  if (page === 0) drawHowtoControls(g, px + 22, contentY, pw - 44, contentH, state);
  else if (page === 1) drawHowtoBricks(g, px + 22, contentY, pw - 44, contentH);
  else drawHowtoPowerups(g, px + 22, contentY, pw - 44, contentH);

  // prev/next/back buttons, anchored to the panel bottom
  const btnH = MIN_TAP - 12;
  const btnY = py + ph - btnH - 26;

  // page dots, sitting in the gap above the button row
  const dotsY = btnY - 30;
  const dotR = 9, dotGap = 30;
  const dotsW = (S.pageCount - 1) * dotGap;
  for (let i = 0; i < S.pageCount; i++) {
    const dx = W / 2 - dotsW / 2 + i * dotGap;
    g.beginPath();
    g.arc(dx, dotsY, i === page ? dotR + 1.5 : dotR, 0, TAU);
    g.fillStyle = i === page ? PALETTE.cyan : 'rgba(255,255,255,0.25)';
    g.fill();
  }
  if (page > 0) button(g, 'page:prev', px + 22, btnY, 150, btnH, '\u2039 PREV', { color: PALETTE.magenta });
  if (page < S.pageCount - 1) button(g, 'page:next', px + pw - 172, btnY, 150, btnH, 'NEXT \u203a', { color: PALETTE.lime });
  button(g, 'back', W / 2 - 100, btnY, 200, btnH, 'BACK', { color: PALETTE.cyan });
}

function diagramCard(g, x, y, w, h, title, accent) {
  panel(g, x, y, w, h, { alpha: 0.38, stroke: accent ? hexA(accent, 0.35) : 'rgba(255,255,255,0.14)' });
  g.save();
  g.textAlign = 'center';
  g.font = `700 ${FS.caption}px system-ui, sans-serif`;
  g.fillStyle = accent || PALETTE.ink;
  g.fillText(title, x + w / 2, y + 30);
  g.restore();
}

// ---------------------------------------------------------------------------
// PAGE 1: controls + the core fantasy ("GET ABOVE THE BRICKS") + the
// paddle's vertical-move uppercut/soften mechanic.
// ---------------------------------------------------------------------------
function drawHowtoControls(g, x, y, w, h, state) {
  const gap = 16;
  // three stacked sections that always exactly fill the available content
  // height (no leftover dead space, no overflow): input methods, the core
  // fantasy diagram (hero of this page), then paddle vertical movement.
  const inputH = h * 0.30;
  const fantasyH = h * 0.40;
  const paddleH = h - inputH - fantasyH - gap * 2;

  drawInputRow(g, x, y, w, inputH);
  drawCoreFantasyDiagram(g, x, y + inputH + gap, w, fantasyH, state);
  drawUppercutRow(g, x, y + inputH + fantasyH + gap * 2, w, paddleH);
}

function drawInputRow(g, x, y, w, h) {
  const gap = 14;
  const colW = (w - gap * 2) / 3;
  diagramCard(g, x, y, colW, h, 'MOUSE');
  g.save();
  g.translate(x + colW / 2, y + h / 2 + 6);
  g.strokeStyle = PALETTE.cyan; g.lineWidth = 3;
  rr(g, -48, 26, 96, 18, 9); g.stroke();
  g.beginPath(); g.moveTo(-6, -38); g.lineTo(-6, -8); g.lineTo(2, -14); g.lineTo(10, 0); g.lineTo(15, -3); g.lineTo(6, -18); g.lineTo(17, -18); g.closePath();
  g.fillStyle = PALETTE.ink; g.fill();
  g.font = `500 32px system-ui, sans-serif`; g.textAlign = 'center'; g.fillStyle = 'rgba(255,255,255,0.78)';
  fitText(g, 'move + click to launch', 0, 58, colW - 20, 32, '500');
  g.restore();

  diagramCard(g, x + colW + gap, y, colW, h, 'TOUCH');
  g.save();
  g.translate(x + colW + gap + colW / 2, y + h / 2 + 2);
  g.beginPath(); g.arc(-48, 6, 28, 0, TAU); g.strokeStyle = 'rgba(255,255,255,0.42)'; g.lineWidth = 2.5; g.stroke();
  g.beginPath(); g.arc(-40, -6, 13, 0, TAU); g.fillStyle = PALETTE.cyan; g.fill();
  g.beginPath(); g.arc(48, 0, 18, 0, TAU); g.strokeStyle = PALETTE.magenta; g.lineWidth = 2.5; g.stroke();
  g.beginPath(); g.arc(48, 0, 6, 0, TAU); g.fillStyle = PALETTE.magenta; g.fill();
  g.fillStyle = 'rgba(255,255,255,0.78)'; g.textAlign = 'center';
  fitText(g, 'stick to move', -48, 46, colW * 0.46, 30, '500');
  fitText(g, 'tap to launch', 48, 46, colW * 0.46, 30, '500');
  g.restore();

  diagramCard(g, x + (colW + gap) * 2, y, colW, h, 'KEYBOARD');
  g.save();
  g.translate(x + (colW + gap) * 2 + colW / 2, y + h / 2 - 14);
  g.fillStyle = PALETTE.ink; g.textAlign = 'center';
  const keys = ['\u25c4 \u25ba  /  A D', '\u25b2 \u25bc  /  W S', 'SPACE  launch', 'P pause \u00b7 M mute'];
  const lh = Math.max(28, h * 0.16);
  keys.forEach((k, i) => fitText(g, k, 0, i * lh + 8, colW - 20, 30, '600'));
  g.restore();
}

/** The hero diagram of page 1: explains the whole point of the game. */
function drawCoreFantasyDiagram(g, x, y, w, h, state) {
  const rm = motion(state);
  panel(g, x, y, w, h, { alpha: 0.3, stroke: hexA(PALETTE.lime, 0.4) });
  g.save();
  g.textAlign = 'center';
  g.font = `900 ${FS.h2}px system-ui, sans-serif`;
  g.fillStyle = PALETTE.lime;
  g.fillText('GET ABOVE THE BRICKS', x + w / 2, y + 40);
  g.font = `600 32px system-ui, sans-serif`;
  g.fillStyle = 'rgba(255,255,255,0.78)';
  g.fillText('That\u2019s "the Attic" \u2014 land the ball on top and the multiplier keeps climbing on its own.', x + w / 2, y + 40 + FS.h2 * 0.62);
  g.restore();

  const diaTop = y + 88;
  const diaH = Math.max(70, h - 100);
  const diaCx = x + w / 2;
  const brickRowY = diaTop + diaH * 0.72;
  const brickW = Math.min(64, w / 9), brickH = brickW * 0.42, bgap = 6;
  const cols = Math.min(8, Math.floor((w * 0.8) / (brickW + bgap)));
  const rowsW = cols * (brickW + bgap) - bgap;
  const startX = diaCx - rowsW / 2;
  const colors = [PALETTE.cyan, PALETTE.magenta, PALETTE.lime, PALETTE.gold];
  for (let r = 0; r < 2; r++) {
    for (let c = 0; c < cols; c++) {
      const bx = startX + c * (brickW + bgap);
      const by = brickRowY + r * (brickH + bgap);
      rr(g, bx, by, brickW, brickH, 4);
      g.fillStyle = hexA(colors[(c + r) % colors.length], 0.85);
      g.fill();
    }
  }
  // ball bouncing along the top of the brick field, with a dotted arc trail
  const ballY0 = brickRowY - brickH * 0.9;
  const bounceAmp = diaH * 0.22;
  const phase = (state?.__unused || 0);
  const ballX = diaCx + Math.sin((S.t || 0) * 1.4 * rm.speed) * rowsW * 0.32;
  const ballY = ballY0 - Math.abs(Math.sin((S.t || 0) * 2.8 * rm.speed)) * bounceAmp;
  g.save();
  g.strokeStyle = hexA('#ffffff', 0.35);
  g.setLineDash([4, 7]);
  g.lineWidth = 2;
  g.beginPath();
  for (let i = 0; i <= 24; i++) {
    const px = startX - brickW + (rowsW + brickW * 2) * (i / 24);
    const py = ballY0 - Math.abs(Math.sin(i * 0.6)) * bounceAmp;
    if (i === 0) g.moveTo(px, py); else g.lineTo(px, py);
  }
  g.stroke();
  g.setLineDash([]);
  g.restore();
  g.save();
  g.globalCompositeOperation = 'lighter';
  const bg = g.createRadialGradient(ballX, ballY, 1, ballX, ballY, 30);
  bg.addColorStop(0, hexA('#ffffff', 0.9));
  bg.addColorStop(1, hexA(PALETTE.cyan, 0));
  g.fillStyle = bg;
  g.beginPath(); g.arc(ballX, ballY, 28, 0, TAU); g.fill();
  g.restore();
  g.beginPath();
  g.arc(ballX, ballY, 13, 0, TAU);
  g.fillStyle = '#fff';
  g.fill();

  // big upward arrows either side, reinforcing "up"
  for (const dx of [-1, 1]) {
    g.save();
    g.translate(diaCx + dx * (rowsW / 2 + brickW * 1.1), brickRowY - 8);
    g.strokeStyle = hexA(PALETTE.lime, 0.85);
    g.lineWidth = 5;
    g.lineCap = 'round';
    const ah = diaH * 0.55;
    g.beginPath(); g.moveTo(0, 0); g.lineTo(0, -ah); g.stroke();
    g.beginPath(); g.moveTo(-14, -ah + 20); g.lineTo(0, -ah); g.lineTo(14, -ah + 20); g.stroke();
    g.restore();
  }
}

function drawUppercutRow(g, x, y, w, h) {
  const gap = 16;
  const colW = (w - gap) / 2;
  diagramCard(g, x, y, colW, h, 'MOVE UP ON IMPACT \u2192 FASTER BALL', PALETTE.lime);
  g.save();
  g.translate(x + colW / 2, y + h * 0.62);
  g.strokeStyle = PALETTE.lime; g.lineWidth = 4; g.lineCap = 'round';
  const amp = Math.max(24, h * 0.32);
  g.beginPath(); g.moveTo(0, amp * 0.5); g.lineTo(0, -amp * 0.4); g.moveTo(-12, -amp * 0.2); g.lineTo(0, -amp * 0.4); g.lineTo(12, -amp * 0.2); g.stroke();
  rr(g, -50, amp * 0.5, 100, 16, 8); g.fillStyle = hexA(PALETTE.lime, 0.28); g.fill(); g.strokeStyle = PALETTE.lime; g.lineWidth = 2; g.stroke();
  g.beginPath(); g.arc(0, -amp * 0.65, 12, 0, TAU); g.fillStyle = '#fff'; g.fill();
  g.restore();

  diagramCard(g, x + colW + gap, y, colW, h, 'MOVE DOWN ON IMPACT \u2192 SOFTER CATCH', PALETTE.gold);
  g.save();
  g.translate(x + colW + gap + colW / 2, y + h * 0.62);
  g.strokeStyle = PALETTE.gold; g.lineWidth = 4; g.lineCap = 'round';
  const amp2 = Math.max(24, h * 0.32);
  g.beginPath(); g.moveTo(0, -amp2 * 0.4); g.lineTo(0, amp2 * 0.5); g.moveTo(-12, amp2 * 0.3); g.lineTo(0, amp2 * 0.5); g.lineTo(12, amp2 * 0.3); g.stroke();
  rr(g, -50, amp2 * 0.5, 100, 16, 8); g.fillStyle = hexA(PALETTE.gold, 0.28); g.fill(); g.strokeStyle = PALETTE.gold; g.lineWidth = 2; g.stroke();
  g.beginPath(); g.arc(0, -amp2 * 0.65, 12, 0, TAU); g.fillStyle = '#fff'; g.fill();
  g.restore();
}

// ---------------------------------------------------------------------------
// PAGE 2: brick legend
// ---------------------------------------------------------------------------
function drawHowtoBricks(g, x, y, w, h) {
  g.save();
  g.textAlign = 'left';
  g.font = `700 ${FS.h2}px system-ui, sans-serif`;
  g.fillStyle = PALETTE.ink;
  g.fillText('BRICK LEGEND \u2014 how to beat each one', x, y + 22);
  g.restore();

  const types = (Bricks && Bricks.BRICK_TYPES) ? Object.keys(Bricks.BRICK_TYPES) : [
    'normal', 'angle', 'speed', 'attic', 'shield', 'mirror', 'mover', 'explosive', 'magnet', 'steel',
  ];
  const cols = 2;
  const rows = Math.ceil(types.length / cols);
  const gap = 12;
  const titleH = 48;
  const available = h - titleH;
  const rowH = (available - (rows - 1) * gap) / rows;
  const colW = w / cols;
  const iconSize = Math.min(rowH * 0.68, 60);
  types.forEach((type, i) => {
    const col = i % cols, row = (i / cols) | 0;
    const rx = x + col * colW;
    const ry = y + titleH + row * (rowH + gap);
    const info = (Bricks && Bricks.BRICK_TYPES && Bricks.BRICK_TYPES[type]) || { name: type, desc: '', color: '#8ffcff' };
    g.save();
    rr(g, rx, ry, colW - 14, rowH, 12);
    g.fillStyle = 'rgba(255,255,255,0.035)';
    g.fill();
    g.lineWidth = 1;
    g.strokeStyle = 'rgba(255,255,255,0.08)';
    g.stroke();
    const iconY = ry + rowH / 2 - iconSize / 2;
    if (Bricks?.drawLegend) {
      try { Bricks.drawLegend(g, rx + 12, iconY, iconSize, type); }
      catch (e) { drawFallbackIcon(g, rx + 12, iconY, iconSize, info.color); }
    } else {
      drawFallbackIcon(g, rx + 12, iconY, iconSize, info.color);
    }
    const textX = rx + iconSize + 28;
    const textW = colW - iconSize - 48;
    g.textAlign = 'left';
    g.font = `700 34px system-ui, sans-serif`;
    g.fillStyle = info.color || PALETTE.ink;
    g.fillText((info.name || type).toUpperCase(), textX, ry + rowH * 0.32);
    g.font = `500 32px system-ui, sans-serif`;
    g.fillStyle = 'rgba(255,255,255,0.85)';
    wrapText(g, info.desc || '', textX, ry + rowH * 0.32 + 34, textW, 34);
    g.restore();
  });
}

function drawFallbackIcon(g, x, y, size, color) {
  g.save();
  rr(g, x, y, size, size, 8);
  g.fillStyle = hexA(color || PALETTE.cyan, 0.25);
  g.fill();
  g.strokeStyle = color || PALETTE.cyan;
  g.lineWidth = 2;
  g.stroke();
  g.restore();
}

function wrapText(g, text, x, y, maxW, lh) {
  if (!text) return;
  const words = text.split(' ');
  let line = '', ly = y, lines = 0;
  for (const word of words) {
    const test = line ? line + ' ' + word : word;
    if (g.measureText(test).width > maxW && line) {
      g.fillText(line, x, ly);
      line = word; ly += lh; lines++;
      if (lines >= 2) { return; }
    } else line = test;
  }
  if (line) g.fillText(line, x, ly);
}

/** Draw text centered at (cx,cy), shrinking the font (down to a floor) so it
 * never overflows a tight cell — used for short labels under icons. */
function fitText(g, text, cx, cy, maxW, baseSize, weight = '700') {
  let size = baseSize;
  g.font = `${weight} ${size}px system-ui, sans-serif`;
  while (size > 26 && g.measureText(text).width > maxW) {
    size -= 2;
    g.font = `${weight} ${size}px system-ui, sans-serif`;
  }
  g.fillText(text, cx, cy);
  return size;
}

// ---------------------------------------------------------------------------
// PAGE 3: powerup legend
// ---------------------------------------------------------------------------
function drawHowtoPowerups(g, x, y, w, h) {
  g.save();
  g.textAlign = 'left';
  g.font = `700 ${FS.h2}px system-ui, sans-serif`;
  g.fillStyle = PALETTE.ink;
  g.fillText('POWERUP LEGEND', x, y + 22);
  g.restore();

  const all = (Powerups && Powerups.POWERUPS) ? Powerups.POWERUPS : {};
  const kinds = Object.keys(all);
  const good = kinds.filter((k) => all[k].good !== false);
  const bad = kinds.filter((k) => all[k].good === false);

  const cols = Math.min(5, Math.max(1, good.length || 1));
  const badCols = Math.min(5, Math.max(1, bad.length || 1));
  const goodRows = Math.max(1, Math.ceil(good.length / cols));
  const badRows = Math.max(1, Math.ceil(bad.length / badCols));
  const titleH = 48, labelH = 40, gap = 18;
  const available = h - titleH - labelH - gap;
  const rowH = clamp(available / (goodRows + badRows), 96, 168);
  const goodH = goodRows * rowH;
  const badH = badRows * rowH;

  drawIconGrid(g, x, y + titleH, w, goodH, good, all, false, cols, rowH);
  g.save();
  g.textAlign = 'left';
  g.font = `800 ${FS.caption}px system-ui, sans-serif`;
  g.fillStyle = '#ff3b5c';
  g.fillText('BAD \u2014 clearly hostile, avoid these!', x, y + titleH + goodH + labelH * 0.6);
  g.restore();
  drawIconGrid(g, x, y + titleH + goodH + labelH, w, badH, bad, all, true, badCols, rowH);
}

function drawIconGrid(g, x, y, w, h, kinds, all, bad, colsHint, rowHHint) {
  if (kinds.length === 0) return;
  const cols = colsHint || (kinds.length > 6 ? 5 : Math.min(kinds.length, 5));
  const rows = Math.ceil(kinds.length / cols);
  const cellW = w / cols;
  const cellH = rowHHint || h / Math.max(1, rows);
  const iconSize = Math.min(cellH * 0.5, cellW * 0.42, 58);
  kinds.forEach((kind, i) => {
    const col = i % cols, row = (i / cols) | 0;
    const cx = x + col * cellW + cellW / 2;
    const cy = y + row * cellH + cellH * 0.36;
    const info = all[kind] || { name: kind, color: '#ccc' };
    g.save();
    if (Powerups?.drawPowerupIcon) {
      try { Powerups.drawPowerupIcon(g, cx, cy, iconSize, kind); }
      catch (e) { drawFallbackIcon(g, cx - iconSize / 2, cy - iconSize / 2, iconSize, info.color); }
    } else {
      drawFallbackIcon(g, cx - iconSize / 2, cy - iconSize / 2, iconSize, info.color);
    }
    if (bad) {
      g.strokeStyle = '#ff3b5c';
      g.lineWidth = 2;
      g.beginPath(); g.arc(cx, cy, iconSize / 2 + 6, 0, TAU); g.stroke();
    }
    g.textAlign = 'center';
    g.fillStyle = bad ? '#ff8098' : PALETTE.ink;
    fitText(g, info.name || kind, cx, cy + iconSize / 2 + 22, cellW - 10, 30, '700');
    g.restore();
  });
}

// ===========================================================================
// SETTINGS SCREEN
// ===========================================================================
function drawSettings(g, screen, state, t) {
  const h = worldH(state);
  const pw = Math.min(780, W - 32);
  const rowH = MIN_TAP, rowGap = 18;
  const numRows = 6; // 4 toggles + 2 sliders
  const header = 104;
  const footer = MIN_TAP + 40;
  const naturalContent = numRows * rowH + (numRows - 1) * rowGap;
  const { ph, py } = fitPanel(h, {
    header, footer, contentFrac: 1, contentMin: naturalContent, contentMax: naturalContent,
    minPh: header + naturalContent + footer, maxPh: h * 0.94,
  });
  const px = W / 2 - pw / 2;
  panel(g, px, py, pw, ph, { alpha: 0.8 });

  g.save();
  g.textAlign = 'center';
  g.font = `800 ${FS.h1}px system-ui, sans-serif`;
  g.fillStyle = PALETTE.lime;
  g.fillText('SETTINGS', W / 2, py + 58);
  g.restore();

  const settings = state?.settings || {};
  let ry = py + header;
  const rx = px + 24, rw = pw - 48;

  toggleRow(g, 'toggle:muted', rx, ry, rw, rowH, 'Mute Audio', !!settings.muted); ry += rowH + rowGap;
  toggleRow(g, 'toggle:shake', rx, ry, rw, rowH, 'Screen Shake', settings.shake !== false); ry += rowH + rowGap;
  toggleRow(g, 'toggle:reducedMotion', rx, ry, rw, rowH, 'Reduced Motion', !!settings.reducedMotion); ry += rowH + rowGap;
  toggleRow(g, 'toggle:colorblind', rx, ry, rw, rowH, 'Colorblind-Safe Palette', !!settings.colorblind); ry += rowH + rowGap;

  sliderRow(g, 'vol:sfx', rx, ry, rw, rowH, 'SFX Volume', settings.sfxVolume ?? 1); ry += rowH + rowGap;
  sliderRow(g, 'vol:music', rx, ry, rw, rowH, 'Music Volume', settings.musicVolume ?? 1); ry += rowH + rowGap;

  const btnW = 220, btnH = MIN_TAP - 12;
  button(g, 'back', W / 2 - btnW / 2, py + ph - btnH - 22, btnW, btnH, 'BACK', { color: PALETTE.cyan });
}

// ===========================================================================
// PAUSED SCREEN
// ===========================================================================
function drawPaused(g, screen, state, t) {
  const h = worldH(state);
  const rm = motion(state);
  g.save();
  g.fillStyle = 'rgba(4,4,10,0.62)';
  g.fillRect(0, 0, W, h);
  g.restore();

  const btnH = MIN_TAP, gap = 16;
  const numBtns = 4;
  const header = 128;
  const footer = 56;
  const naturalContent = numBtns * btnH + (numBtns - 1) * gap;
  const pw = Math.min(560, W - 56);
  const { ph, py } = fitPanel(h, {
    header, footer, contentFrac: 1, contentMin: naturalContent, contentMax: naturalContent,
    minPh: header + naturalContent + footer, maxPh: h * 0.9,
  });
  const px = W / 2 - pw / 2;
  panel(g, px, py, pw, ph, { alpha: 0.8 });

  g.save();
  g.textAlign = 'center';
  const pulse = 1 + Math.sin(t * 2.4 * rm.speed) * 0.015 * rm.amp;
  g.translate(W / 2, py + 76);
  g.scale(pulse, pulse);
  g.font = `900 ${FS.h1}px system-ui, sans-serif`;
  g.fillStyle = '#fff';
  g.fillText('PAUSED', 0, 0);
  g.restore();

  const btnW = pw - 64;
  let by = py + header;
  button(g, 'resume', px + 32, by, btnW, btnH, 'RESUME', { color: PALETTE.cyan, filled: true }); by += btnH + gap;
  button(g, 'restart', px + 32, by, btnW, btnH, 'RESTART', { color: PALETTE.magenta }); by += btnH + gap;
  button(g, 'settings', px + 32, by, btnW, btnH, 'SETTINGS', { color: PALETTE.lime }); by += btnH + gap;
  button(g, 'howto', px + 32, by, btnW, btnH, 'HOW TO PLAY', { color: PALETTE.gold }); by += btnH + gap;

  g.save();
  g.textAlign = 'center';
  g.font = `600 32px system-ui, sans-serif`;
  g.fillStyle = 'rgba(255,255,255,0.55)';
  g.fillText('Space/P resume \u00b7 R restart \u00b7 M mute', W / 2, py + ph - 20);
  g.restore();
}

// ===========================================================================
// GAME OVER SCREEN
// ===========================================================================
function drawGameOver(g, screen, state, t) {
  const h = worldH(state);
  const rm = motion(state);
  g.save();
  g.fillStyle = 'rgba(4,4,10,0.72)';
  g.fillRect(0, 0, W, h);
  g.restore();

  const stats = state?.stats || {};
  const numRows = 6;
  const rh = 46;
  const btnH = MIN_TAP;
  const naturalContent = 66 + 62 + 46 + 26 + numRows * rh + 26 + btnH;
  const pw = Math.min(720, W - 32);
  const { ph, py } = fitPanel(h, {
    header: 0, footer: 0, contentFrac: 1, contentMin: naturalContent, contentMax: naturalContent,
    minPh: naturalContent + 40, maxPh: h * 0.94,
  });
  const px = W / 2 - pw / 2;
  panel(g, px, py, pw, ph, { alpha: 0.84 });

  g.save();
  g.textAlign = 'center';
  g.font = `900 ${FS.h1}px system-ui, sans-serif`;
  g.fillStyle = PALETTE.magenta;
  g.fillText('GAME OVER', W / 2, py + 62);
  g.restore();

  // odometer-feel score
  const scoreStr = fmtInt(state?.score);
  g.save();
  g.textAlign = 'center';
  const bump = 1 + Math.max(0, Math.sin(t * 6 * rm.speed)) * 0.02 * rm.amp;
  g.translate(W / 2, py + 126);
  g.scale(bump, bump);
  g.font = '800 58px "Courier New", monospace';
  g.fillStyle = '#fff';
  g.fillText(scoreStr, 0, 0);
  g.restore();

  let yy = py + 160;
  if (state?.isNewHigh) {
    g.save();
    g.textAlign = 'center';
    const flick = rm.on ? (0.7 + Math.sin(t * 10) * 0.3) : 0.95;
    g.font = `800 ${FS.h3}px system-ui, sans-serif`;
    g.fillStyle = `rgba(255,209,94,${flick})`;
    g.fillText('\u2605 NEW BEST! \u2605', W / 2, yy + 20);
    g.restore();
    yy += 46;
    try { S.cfg?.fx?.confetti?.(W / 2, py + 60, 1); } catch (e) { /* ignore */ }
  } else {
    g.save();
    g.textAlign = 'center';
    g.font = `600 32px system-ui, sans-serif`;
    g.fillStyle = 'rgba(255,255,255,0.65)';
    g.fillText(`High Score: ${fmtInt(state?.highScore)}`, W / 2, yy + 16);
    g.restore();
    yy += 46;
  }

  // stats table
  const rows = [
    ['Bricks Broken', fmtInt(stats.bricksBroken)],
    ['Best Multiplier', `x${(stats.maxMult ?? 1).toFixed(1)}`],
    ['Best Combo', fmtInt(stats.maxCombo)],
    ['Time in the Attic', fmtTime(stats.atticTime)],
    ['Levels Cleared', fmtInt(stats.levelsCleared)],
    ['Powerups Collected', fmtInt(stats.powerupsCollected)],
  ];
  const tableY = yy + 30;
  const tableW = pw - 80;
  const tableX = px + 40;
  g.save();
  g.font = `500 32px system-ui, sans-serif`;
  rows.forEach((row, i) => {
    const ry = tableY + i * rh;
    if (i % 2 === 0) { g.fillStyle = 'rgba(255,255,255,0.045)'; g.fillRect(tableX, ry, tableW, rh); }
    g.textAlign = 'left';
    g.fillStyle = 'rgba(255,255,255,0.8)';
    g.fillText(row[0], tableX + 18, ry + rh / 2 + 7);
    g.textAlign = 'right';
    g.fillStyle = '#8ffcff';
    g.font = `700 32px system-ui, sans-serif`;
    g.fillText(row[1], tableX + tableW - 18, ry + rh / 2 + 7);
    g.font = `500 32px system-ui, sans-serif`;
  });
  g.restore();

  const btnY = tableY + rows.length * rh + 26;
  const btnW = (pw - 80 - 20) / 2;
  button(g, 'restart', px + 40, btnY, btnW, btnH, 'RESTART', { color: PALETTE.cyan, filled: true });
  button(g, 'title', px + 40 + btnW + 20, btnY, btnW, btnH, 'TITLE', { color: PALETTE.magenta });
}


// ===========================================================================
// PUBLIC API
// ===========================================================================
export const UI = {
  /**
   * One-time init. Called once by the caller before the first draw/update.
   * @param {{audio?: object, fx?: object}} [cfg]
   */
  init(cfg) {
    S.cfg = cfg || {};
    S.t = 0;
    S.page = 0;
  },

  /**
   * Advance internal animation clock. Purely cosmetic; never mutates game state.
   * @param {number} dt seconds since last frame
   * @param {number} t total elapsed seconds (authoritative clock, preferred over internal accumulation)
   */
  update(dt, t) {
    S.t = Number.isFinite(t) ? t : S.t + (dt || 0);
  },

  /**
   * Draw a screen in WORLD space.
   * @param {CanvasRenderingContext2D} g world-space 2D context
   * @param {string} screen one of 'title'|'howto'|'settings'|'paused'|'gameOver'
   * @param {object} state see module header doc for shape
   * @param {number} [t] optional explicit time override
   */
  draw(g, screen, state, t) {
    if (screen !== S.lastScreen) { S.lastScreen = screen; }
    const time = Number.isFinite(t) ? t : S.t;
    resetRegions();
    try {
      switch (screen) {
        case 'title': drawTitle(g, screen, state, time); break;
        case 'howto': drawHowto(g, screen, state, time); break;
        case 'settings': drawSettings(g, screen, state, time); break;
        case 'paused': drawPaused(g, screen, state, time); break;
        case 'gameOver': drawGameOver(g, screen, state, time); break;
        default: break;
      }
    } catch (e) {
      console.warn('[ui] draw failed for screen', screen, e);
    }
  },

  /**
   * Hit-test a pointer press in WORLD coords against the regions registered
   * by the most recent draw() call for this screen.
   * @param {number} x @param {number} y
   * @param {string} screen
   * @param {object} state
   * @returns {string|null} action id, or null if nothing was hit
   */
  hit(x, y, screen, state) {
    const r = findRegion(x, y);
    if (!r) {
      S.pressId = null;
      return null;
    }
    S.pressId = r.id;
    playClick();
    let action = r.id;
    if (r.id === 'vol:sfx' || r.id === 'vol:music') {
      const kind = r.id === 'vol:sfx' ? 'sfx' : 'music';
      const tr = r.track || { x: r.x, w: r.w };
      const v = clamp((x - tr.x) / (tr.w || 1), 0, 1);
      action = `vol:${kind}:${v.toFixed(3)}`;
    }
    if (r.id === 'page:next') { S.page = clamp(S.page + 1, 0, S.pageCount - 1); }
    if (r.id === 'page:prev') { S.page = clamp(S.page - 1, 0, S.pageCount - 1); }
    if (r.id === 'howto') { S.page = 0; }
    return action;
  },

  /**
   * Update hover state for mouse movement (no action fired). Plays a subtle
   * menuMove sfx on hover-target change.
   * @param {number} x @param {number} y
   * @param {string} screen
   * @param {object} state
   */
  hover(x, y, screen, state) {
    const r = findRegion(x, y);
    const id = r ? r.id : null;
    if (id !== S.hoverId) {
      S.hoverId = id;
      if (id) playMove();
    }
    S.pressId = null;
    return id;
  },

  /** Screens this module can draw, in the order they typically flow. */
  screens: ['title', 'howto', 'settings', 'paused', 'gameOver'],

  /**
   * TEST/DEBUG ONLY — not part of the required contract. Directly sets the
   * howto pagination index so test harnesses can render a specific page
   * without simulating a click. Safe no-op misuse (clamped internally).
   * @param {number} n
   */
  __debugSetPage(n) { S.page = clamp(n | 0, 0, S.pageCount - 1); },
};

export default UI;

