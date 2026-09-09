// src/game/hud.js — score/multiplier/lives/level/powerup-chip/attic-meter HUD.
import { W, H } from '../core/constants.js';
import { clamp, lerp } from '../core/math.js';

let Powerups = null;
try { Powerups = await import('./powerups.js'); } catch { /* optional */ }

let dispScore = 0;
let scorePop = 0;
let lastScore = 0;

export function resetHud() { dispScore = 0; scorePop = 0; lastScore = 0; }

const SAFE = 24; // extra inset so HUD clears device safe-areas (rendered content also padded by CSS env())

function tierColor(mult) {
  if (mult >= 40) return '#ffffff';
  if (mult >= 20) return '#b0ff5e';
  if (mult >= 10) return '#ff5ec8';
  if (mult >= 5) return '#ffd15e';
  if (mult >= 2) return '#5ee6ff';
  return '#eafcff';
}

export function drawHUD(g, game, t) {
  const beat = game.audio?.music?.beatPhase?.() ?? 0;
  const beatPulse = Math.max(0, Math.sin(beat * Math.PI * 2));

  if (Math.round(game.score) !== lastScore) { scorePop = 1; lastScore = Math.round(game.score); }
  if (scorePop > 0) scorePop = Math.max(0, scorePop - 0.06);

  // Safe-area aware insets (world units) — guarantees nothing is ever
  // clipped by a notch/home-indicator/rounded corner OR by the letterboxed
  // canvas edge on any viewport from 320x568 up to 1600x900.
  const safeTop = (game.renderer?.safeTop || 0);
  const safeRight = (game.renderer?.safeRight || 0);
  const topPad = SAFE + safeTop;
  const rightPad = SAFE + safeRight;

  g.save();
  g.textBaseline = 'top';

  // ---- dark scrim behind the whole top HUD band, so text stays readable
  // at every escalation tier no matter how bright the background gets ----
  const scrimH = topPad + 150;
  const scrim = g.createLinearGradient(0, 0, 0, scrimH);
  scrim.addColorStop(0, 'rgba(2,2,10,0.62)');
  scrim.addColorStop(1, 'rgba(2,2,10,0)');
  g.fillStyle = scrim;
  g.fillRect(0, 0, W, scrimH);

  // ---- rolling odometer score (top-left), weighty pop-scale on gain ----
  dispScore = lerp(dispScore, game.score, 0.15);
  const shown = Math.round(dispScore).toLocaleString();
  const scoreScale = 1 + scorePop * 0.22;
  g.save();
  g.translate(SAFE, topPad);
  g.scale(scoreScale, scoreScale);
  g.font = '900 46px system-ui, -apple-system, Segoe UI, Roboto, sans-serif';
  g.shadowColor = 'rgba(94,230,255,0.65)';
  g.shadowBlur = 14;
  g.fillStyle = 'rgba(0,0,0,0.4)';
  g.fillText(shown, 2, 3);
  const grad = g.createLinearGradient(0, 0, 220, 0);
  grad.addColorStop(0, '#8ffcff');
  grad.addColorStop(1, '#ff9ee8');
  g.fillStyle = grad;
  g.fillText(shown, 0, 0);
  g.shadowBlur = 0;
  g.restore();

  // high score, small, under it
  g.font = '600 15px system-ui, sans-serif';
  g.fillStyle = 'rgba(255,255,255,0.55)';
  g.fillText(`BEST ${Math.round(game.highScore).toLocaleString()}`, SAFE, topPad + 52);

  // ---- multiplier: bold glowing badge, pulses per beat, colored by tier ----
  const mColor = tierColor(game.mult);
  const cx = W - rightPad - 64, cy = topPad + 52, rr = 40;

  const mPulse = 1 + 0.08 * beatPulse + Math.min(0.25, (game.mult - 1) * 0.01);
  g.save();
  g.translate(cx, cy);
  g.scale(mPulse, mPulse);
  g.globalCompositeOperation = 'lighter';
  const glowR = rr + 14;
  const og = g.createRadialGradient(0, 0, rr * 0.4, 0, 0, glowR);
  og.addColorStop(0, colorAlpha(mColor, 0.5));
  og.addColorStop(1, colorAlpha(mColor, 0));
  g.fillStyle = og;
  g.beginPath(); g.arc(0, 0, glowR, 0, Math.PI * 2); g.fill();
  g.globalCompositeOperation = 'source-over';
  g.lineWidth = 7;
  g.strokeStyle = 'rgba(255,255,255,0.15)';
  g.beginPath(); g.arc(0, 0, rr, 0, Math.PI * 2); g.stroke();
  const frac = clamp(((game.mult - 1) % 10) / 10, 0, 1);
  g.strokeStyle = mColor;
  g.beginPath();
  g.arc(0, 0, rr, -Math.PI / 2, -Math.PI / 2 + frac * Math.PI * 2);
  g.stroke();
  g.font = '800 28px system-ui, sans-serif';
  g.textAlign = 'center';
  g.fillStyle = '#fff';
  g.fillText(`x${game.mult.toFixed(1)}`, 0, -15);
  g.textAlign = 'left';
  g.restore();

  // ---- lives, glowing dots ----
  g.save();
  g.translate(SAFE, topPad + 80);
  for (let i = 0; i < Math.max(0, game.lives); i++) {
    g.globalCompositeOperation = 'lighter';
    g.fillStyle = 'rgba(255,94,200,0.4)';
    g.beginPath(); g.arc(i * 34 + 12, 12, 15, 0, Math.PI * 2); g.fill();
    g.globalCompositeOperation = 'source-over';
    g.fillStyle = '#ff5ec8';
    g.beginPath();
    g.arc(i * 34 + 12, 12, 9, 0, Math.PI * 2);
    g.fill();
  }
  // shield charges, right next to lives
  if (game.shieldCharges > 0) {
    const sx0 = Math.max(0, game.lives) * 34 + 20;
    for (let i = 0; i < game.shieldCharges; i++) {
      g.strokeStyle = '#9dff5c';
      g.lineWidth = 2.5;
      g.beginPath();
      g.arc(sx0 + i * 30 + 10, 12, 11, 0, Math.PI * 2);
      g.stroke();
    }
  }
  g.restore();

  // level name
  g.font = '700 20px system-ui, sans-serif';
  g.fillStyle = 'rgba(255,255,255,0.8)';
  g.fillText(game.levelName || '', SAFE, topPad + 118);

  // combo
  if (game.combo > 1) {
    g.save();
    g.font = '800 26px system-ui, sans-serif';
    g.fillStyle = '#ffd15e';
    g.textAlign = 'center';
    g.shadowColor = 'rgba(255,209,94,0.7)';
    g.shadowBlur = 10;
    g.fillText(`COMBO x${game.combo}`, W / 2, topPad + 4);
    g.textAlign = 'left';
    g.restore();
  }

  // ---- attic energy column: only appears when relevant (meter>0 or active) ----
  const atticA = game.attic;
  if (atticA && (atticA.meter > 0.01 || atticA.active)) {
    const colW = 20, colH = clamp(H * 0.32, 260, 460), colX = W - rightPad - colW - 4, colY = topPad + 130;
    g.save();
    g.globalAlpha = atticA.active ? 1 : clamp(atticA.meter * 3, 0, 1);
    // column housing
    roundRect(g, colX, colY, colW, colH, colW / 2);
    g.fillStyle = 'rgba(10,8,24,0.55)';
    g.fill();
    g.strokeStyle = 'rgba(143,252,255,0.35)';
    g.lineWidth = 2;
    g.stroke();
    // rising energy fill
    const fillH = colH * atticA.meter;
    g.save();
    roundRect(g, colX, colY, colW, colH, colW / 2);
    g.clip();
    g.globalCompositeOperation = 'lighter';
    const fg = g.createLinearGradient(0, colY + colH, 0, colY + colH - fillH);
    fg.addColorStop(0, '#5ee6ff');
    fg.addColorStop(0.6, '#ff5ec8');
    fg.addColorStop(1, '#ffe15e');
    g.fillStyle = fg;
    g.fillRect(colX, colY + colH - fillH, colW, fillH);
    g.restore();
    g.restore();
    // "ATTIC!" label only while actually active or the banner is fading
    if (atticA.active || atticA.bannerT > 0) {
      g.save();
      g.globalAlpha = atticA.active ? 1 : atticA.bannerT;
      g.font = '900 26px system-ui, sans-serif';
      g.fillStyle = mColor;
      g.textAlign = 'right';
      g.shadowColor = mColor;
      g.shadowBlur = 12;
      g.fillText('ATTIC', colX - 10, colY - 6);
      g.textAlign = 'left';
      g.restore();
    }
  }

  // ---- active powerup chips (bottom-left, countdown rings) ----
  const list = Powerups?.activeList ? Powerups.activeList(game) : [];
  const safeBottom = (game.renderer?.safeBottom || 0);
  const safeLeft = (game.renderer?.safeLeft || 0);
  let chipX = SAFE + safeLeft;
  const chipY = H - 90 - SAFE - safeBottom;
  for (const e of list) {
    g.save();
    g.translate(chipX + 26, chipY + 26);
    g.fillStyle = 'rgba(20,20,40,0.65)';
    g.beginPath(); g.arc(0, 0, 26, 0, Math.PI * 2); g.fill();
    g.strokeStyle = 'rgba(255,255,255,0.15)';
    g.lineWidth = 4;
    g.beginPath(); g.arc(0, 0, 22, 0, Math.PI * 2); g.stroke();
    g.strokeStyle = e.color || '#5ee6ff';
    g.beginPath(); g.arc(0, 0, 22, -Math.PI / 2, -Math.PI / 2 + e.frac * Math.PI * 2); g.stroke();
    g.fillStyle = '#fff';
    g.font = '700 11px system-ui, sans-serif';
    g.textAlign = 'center';
    g.fillText((e.name || e.kind).slice(0, 6).toUpperCase(), 0, -3);
    g.textAlign = 'left';
    g.restore();
    chipX += 60;
  }

  // FPS
  if (game.showFps) {
    g.font = '600 16px monospace';
    g.fillStyle = 'rgba(255,255,255,0.5)';
    g.fillText(`${game.fps | 0} fps`, W - 90, H - 30);
  }

  g.restore();
}

function colorAlpha(hex, a) {
  const h = hex.replace('#', '');
  const n = parseInt(h.length === 3 ? h.split('').map((c) => c + c).join('') : h, 16);
  const r = (n >> 16) & 255, gg = (n >> 8) & 255, b = n & 255;
  return `rgba(${r},${gg},${b},${a})`;
}

function roundRect(g, x, y, w, h, r) {
  g.beginPath();
  g.moveTo(x + r, y);
  g.arcTo(x + w, y, x + w, y + h, r);
  g.arcTo(x + w, y + h, x, y + h, r);
  g.arcTo(x, y + h, x, y, r);
  g.arcTo(x, y, x + w, y, r);
  g.closePath();
}
