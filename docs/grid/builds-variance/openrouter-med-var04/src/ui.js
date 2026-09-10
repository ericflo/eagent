// ui.js — HUD (score, lives, multiplier, frenzy meter), effect countdown icons,
// banners and menus. DOM overlays over the canvas, plus canvas title art.
import { WORLD_W, WORLD_H } from './engine.js';

const HUD_ID = 'bt-hud';

// Simple word-wrap used for banner subtitles so text never runs past the edges.
function wrapText(ctx, text, maxWidth) {
  const words = String(text).split(/\s+/);
  const lines = [];
  let cur = '';
  for (const w of words) {
    const test = cur ? cur + ' ' + w : w;
    if (ctx.measureText(test).width > maxWidth && cur) {
      lines.push(cur);
      cur = w;
    } else {
      cur = test;
    }
  }
  if (cur) lines.push(cur);
  return lines.length ? lines : [''];
}

export function buildHUD() {
  let hud = document.getElementById(HUD_ID);
  if (!hud) {
    hud = document.createElement('div');
    hud.id = HUD_ID;
    hud.style.cssText = 'position:fixed;top:0;left:50%;transform:translateX(-50%);width:min(540px,96vw);padding:10px 14px;box-sizing:border-box;display:flex;justify-content:space-between;align-items:center;gap:10px;font-family:system-ui,sans-serif;color:#e2e8f0;font-size:15px;z-index:20;pointer-events:none;text-shadow:0 1px 3px rgba(0,0,0,.7)';
    hud.innerHTML = `
      <div id="bt-lives" style="font-size:18px;letter-spacing:2px">♥♥♥</div>
      <div id="bt-score" style="font-weight:800;font-size:20px">0</div>
      <div id="bt-combo" style="opacity:0;transition:opacity .3s;font-weight:700;color:#fbbf24"></div>
      <div style="display:flex;align-items:center;gap:10px">
        <div id="bt-frenzy" style="width:90px;height:10px;border:1px solid rgba(255,215,0,.7);border-radius:6px;overflow:hidden">
          <div id="bt-frenzyFill" style="height:100%;width:0;background:linear-gradient(90deg,#fbbf24,#f97316);transition:width .3s"></div>
        </div>
        <div id="bt-mult" style="font-weight:800;color:#fbbf24;min-width:44px">x1</div>
        <div id="bt-fx" style="display:flex;gap:4px"></div>
      </div>`;
    document.body.appendChild(hud);
  }
  return hud;
}

export class UI {
  constructor() {
    this.hud = buildHUD();
    this.muteButton();
  }

  muteButton() {
    const b = document.createElement('button');
    b.className = 'ui-button';
    b.textContent = localStorage.getItem('breakthrough.muted') === '1' ? '🔇' : '🔊';
    b.setAttribute('aria-label', 'Mute');
    b.style.cssText = 'position:fixed;top:10px;right:10px;z-index:30;background:rgba(2,6,23,.6);color:#e2e8f0;border:1px solid rgba(148,163,184,.4);border-radius:10px;padding:6px 10px;font-size:16px;cursor:pointer';
    b.onclick = (e) => {
      e.stopPropagation();
      b.textContent = window.__game.audio.toggleMute() ? '🔇' : '🔊';
    };
    document.body.appendChild(b);
    this.muteBtn = b;
  }

  update(game) {
    // hide HUD entirely on the menu screen
    this.hud.style.display = game.state === 'menu' ? 'none' : 'flex';
    this.hud.querySelector('#bt-score').textContent = game.score.toLocaleString('en-US');
    this.hud.querySelector('#bt-lives').textContent = '♥'.repeat(game.lives) + '♡'.repeat(Math.max(0, 3 - game.lives));
    const mult = this.hud.querySelector('#bt-mult');
    mult.textContent = 'x' + (game.multiplier || 1);
    const fill = this.hud.querySelector('#bt-frenzyFill');
    fill.style.width = Math.round((game.frenzyIntensity || 0) * 100) + '%';
    const combo = this.hud.querySelector('#bt-combo');
    if (game.combo >= 2) { combo.style.opacity = 1; combo.textContent = 'Combo ' + game.combo; }
    else combo.style.opacity = 0;
    // effect countdown icons
    const fx = this.hud.querySelector('#bt-fx');
    fx.innerHTML = (game.hudEffects || []).map(e =>
      `<span title="${e.name}" style="background:rgba(2,6,23,.7);border:1px solid rgba(148,163,184,.4);border-radius:8px;padding:2px 5px;font-size:12px">${e.glyph} ${Math.ceil(e.remaining)}s</span>`).join('');
  }

  banner(title, sub, tSec, dur = 6) {
    // in-canvas animated banner: fade in fast, hold, fade out near the end
    const ctx = window.__game.ctx;
    ctx.save();
    const fadeT = 0.35, outT = 0.6;
    let a = 1;
    if (tSec < fadeT) a = tSec / fadeT;
    else if (tSec > dur - outT) a = Math.max(0, (dur - tSec) / outT);
    ctx.globalAlpha = a;
    ctx.fillStyle = 'rgba(2,6,23,0.78)';
    ctx.fillRect(0, WORLD_H * 0.38, WORLD_W, 200);
    ctx.textAlign = 'center';
    ctx.fillStyle = '#fbbf24';
    ctx.font = 'bold 42px system-ui, sans-serif';
    ctx.fillText(title, WORLD_W / 2, WORLD_H * 0.38 + 80);
    if (sub) {
      ctx.fillStyle = '#e2e8f0';
      ctx.font = '20px system-ui, sans-serif';
      const lines = wrapText(ctx, sub, WORLD_W - 60);
      lines.forEach((ln, i) => ctx.fillText(ln, WORLD_W / 2, WORLD_H * 0.38 + 122 + i * 26));
    }
    ctx.globalAlpha = 1;
    ctx.restore();
  }

  drawPause(ctx) {
    ctx.save();
    ctx.fillStyle = 'rgba(2,6,23,0.66)';
    ctx.fillRect(0, 0, WORLD_W, WORLD_H);
    ctx.textAlign = 'center';
    ctx.fillStyle = '#fbbf24';
    ctx.font = 'bold 56px system-ui, sans-serif';
    ctx.fillText('PAUSED', WORLD_W / 2, WORLD_H / 2 - 10);
    ctx.fillStyle = '#e2e8f0';
    ctx.font = '22px system-ui, sans-serif';
    ctx.fillText('press P or tap to resume', WORLD_W / 2, WORLD_H / 2 + 40);
    ctx.restore();
  }

  drawMenu(ctx, highScore) {
    ctx.save();
    const g = ctx.createLinearGradient(0, 0, 0, WORLD_H);
    g.addColorStop(0, '#0b1120'); g.addColorStop(1, '#1e293b');
    ctx.fillStyle = g; ctx.fillRect(0, 0, WORLD_W, WORLD_H);
    // decorative bricks
    const cols = ['#38bdf8', '#5eead4', '#fbbf24', '#fb7185', '#fcd34d'];
    for (let i = 0; i < 4; i++) {
      ctx.fillStyle = cols[i];
      ctx.globalAlpha = 0.85;
      ctx.beginPath();
      ctx.roundRect(70 + i * 110, 240, 90, 40, 6);
      ctx.fill();
    }
    ctx.globalAlpha = 1;
    // glowing ball + trail
    for (let i = 0; i < 6; i++) {
      ctx.globalAlpha = 0.08 * (6 - i);
      ctx.fillStyle = '#67e8f9';
      ctx.beginPath(); ctx.arc(330 - i * 18, 380 + i * 14, 10 - i, 0, Math.PI * 2); ctx.fill();
    }
    ctx.globalAlpha = 1;
    ctx.shadowColor = '#38bdf8'; ctx.shadowBlur = 25;
    ctx.fillStyle = '#e0f2fe';
    ctx.beginPath(); ctx.arc(318, 360, 11, 0, Math.PI * 2); ctx.fill();
    ctx.shadowBlur = 0;
    // paddle
    const pg = ctx.createLinearGradient(170, 0, 370, 0);
    pg.addColorStop(0, '#0ea5e9'); pg.addColorStop(.5, '#67e8f9'); pg.addColorStop(1, '#0ea5e9');
    ctx.shadowColor = 'rgba(14,165,233,.6)'; ctx.shadowBlur = 18;
    ctx.fillStyle = pg;
    ctx.beginPath(); ctx.roundRect(160, 760, 220, 16, 8); ctx.fill();
    ctx.shadowBlur = 0;
    // frenzy dashed line motif
    ctx.strokeStyle = 'rgba(251,191,36,.8)'; ctx.setLineDash([10, 8]); ctx.lineWidth = 3;
    ctx.beginPath(); ctx.moveTo(0, 210); ctx.lineTo(WORLD_W, 210); ctx.stroke();
    ctx.setLineDash([]);
    // titles
    ctx.textAlign = 'center';
    ctx.fillStyle = '#fbbf24';
    ctx.font = 'bold 58px system-ui, sans-serif';
    ctx.fillText('BREAKTHROUGH', WORLD_W / 2, 470);
    ctx.fillStyle = '#e2e8f0';
    ctx.font = '22px system-ui, sans-serif';
    ctx.fillText('Tap to start', WORLD_W / 2, 540);
    ctx.fillStyle = '#94a3b8';
    ctx.font = '17px system-ui, sans-serif';
    ctx.fillText('High score: ' + highScore.toLocaleString('en-US'), WORLD_W / 2, 590);
    ctx.restore();
  }

  drawEndScreen(ctx, won, score, highScore, isNew, stats = {}) {
    ctx.save();
    ctx.fillStyle = 'rgba(2,6,23,0.85)';
    ctx.fillRect(0, 0, WORLD_W, WORLD_H);
    ctx.textAlign = 'center';
    ctx.fillStyle = won ? '#fbbf24' : '#f87171';
    ctx.font = 'bold 52px system-ui, sans-serif';
    ctx.fillText(won ? 'YOU WIN!' : 'GAME OVER', WORLD_W / 2, 380);
    ctx.fillStyle = '#e2e8f0';
    ctx.font = '24px system-ui, sans-serif';
    ctx.fillText('Score: ' + score.toLocaleString('en-US'), WORLD_W / 2, 445);
    // run stats — level reached and bricks broken
    if (stats.level || stats.bricks) {
      ctx.fillStyle = '#94a3b8';
      ctx.font = '19px system-ui, sans-serif';
      const lvl = stats.level ? `Level reached: ${stats.level}` : '';
      const brk = stats.bricks ? `Bricks broken: ${stats.bricks}` : '';
      const line = [lvl, brk].filter(Boolean).join('   ·   ');
      ctx.fillText(line, WORLD_W / 2, 482);
    }
    if (isNew) {
      ctx.fillStyle = '#fbbf24';
      ctx.font = 'bold 22px system-ui, sans-serif';
      ctx.fillText('New high score!', WORLD_W / 2, 525);
    } else {
      ctx.fillStyle = '#94a3b8';
      ctx.font = '18px system-ui, sans-serif';
      ctx.fillText('High score: ' + highScore.toLocaleString('en-US'), WORLD_W / 2, 525);
    }
    ctx.fillStyle = '#67e8f9';
    ctx.font = '20px system-ui, sans-serif';
    ctx.fillText('Tap to play again', WORLD_W / 2, 600);
    ctx.restore();
  }
}
