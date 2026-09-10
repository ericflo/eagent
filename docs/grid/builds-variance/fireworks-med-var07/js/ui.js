'use strict';
/* ============================================================
   Attic Breakout — ui.js
   Canvas-drawn HUD, menus (title / pause / game over / win /
   level intro / settings) and touch hit-zones. All text is
   canvas-drawn with a system font stack.
   ============================================================ */

const UI = {
  FONT: `'Trebuchet MS','Segoe UI',system-ui,-apple-system,sans-serif`,
  buttons: [], // rebuilt per frame: {x,y,w,h,action,label}
};

class UIRenderer {
  constructor(game) { this.game = game; this.btns = []; }

  reset() { this.btns = []; UI.buttons = this.btns; }

  button(x, y, w, h, label, action, { primary = false, small = false } = {}) {
    this.btns.push({ x, y, w, h, action, label });
    const ctx = this.game.ctx;
    const hovered = this.game.pointerOver && this.game.pointerOver.x > x && this.game.pointerOver.x < x + w
      && this.game.pointerOver.y > y && this.game.pointerOver.y < y + h;
    ctx.save();
    ctx.shadowColor = primary ? 'rgba(110,200,255,0.9)' : 'rgba(0,0,0,0.6)';
    ctx.shadowBlur = hovered ? 22 : 10;
    ctx.fillStyle = primary ? 'rgba(70,160,235,0.95)' : 'rgba(255,255,255,0.1)';
    if (ctx.roundRect) {
      ctx.beginPath(); ctx.roundRect(x, y, w, h, 12); ctx.fill();
    } else ctx.fillRect(x, y, w, h);
    ctx.shadowBlur = 0;
    ctx.strokeStyle = hovered ? 'rgba(255,255,255,0.9)' : 'rgba(255,255,255,0.35)';
    ctx.lineWidth = 2;
    if (ctx.roundRect) {
      ctx.beginPath(); ctx.roundRect(x, y, w, h, 12); ctx.stroke();
    } else ctx.strokeRect(x, y, w, h);
    ctx.fillStyle = '#fff';
    ctx.font = `bold ${small ? 16 : 20}px ${UI.FONT}`;
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    ctx.fillText(label, x + w / 2, y + h / 2 + 1);
    ctx.restore();
  }

  dim(alpha = 0.6) {
    const ctx = this.game.ctx;
    ctx.fillStyle = `rgba(5,6,14,${alpha})`;
    ctx.fillRect(0, 0, CONFIG.W, CONFIG.H);
  }

  title(text, y, size = 56, color = '#eaf6ff') {
    const ctx = this.game.ctx;
    ctx.save();
    ctx.font = `900 ${size}px ${UI.FONT}`;
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    ctx.lineWidth = 8; ctx.strokeStyle = 'rgba(0,0,0,0.65)';
    ctx.strokeText(text, CONFIG.W / 2, y);
    const g = ctx.createLinearGradient(0, y - size / 2, 0, y + size / 2);
    g.addColorStop(0, '#ffffff');
    g.addColorStop(1, color);
    ctx.fillStyle = g;
    ctx.fillText(text, CONFIG.W / 2, y);
    ctx.restore();
  }

  text(str, y, { size = 18, color = '#cfe3f5', align = 'center', weight = 'normal', alpha = 1 } = {}) {
    const ctx = this.game.ctx;
    ctx.save();
    ctx.globalAlpha = alpha;
    ctx.font = `${weight} ${size}px ${UI.FONT}`;
    ctx.textAlign = align; ctx.textBaseline = 'middle';
    ctx.fillStyle = color;
    ctx.fillText(str, align === 'left' ? 40 : align === 'right' ? CONFIG.W - 40 : CONFIG.W / 2, y);
    ctx.restore();
  }

  // ---- HUD ---------------------------------------------------------------

  drawHUD() {
    const g = this.game, ctx = g.ctx;
    this.reset();

    // Top bar
    this.text(`SCORE ${fmtScore(g.score)}`, 26, { size: 22, align: 'left', weight: 'bold', color: '#fff' });
    this.text(`BEST ${fmtScore(g.best)}`, 52, { size: 14, align: 'left', color: '#8fb4d9' });
    this.text(`LEVEL ${g.level + 1} — ${g.levelDef.name}`, 26, { align: 'center', size: 15, color: '#9db8d4' });
    // Lives as dots
    ctx.save();
    for (let i = 0; i < CONFIG.LIVES; i++) {
      ctx.beginPath();
      ctx.arc(CONFIG.W - 36 - i * 24, 26, 7, 0, TAU);
      ctx.fillStyle = i < g.lives ? '#ff5c6e' : 'rgba(255,255,255,0.15)';
      ctx.fill();
    }
    ctx.restore();

    // Active power-up timers (left side under score)
    let ty = 78;
    const timers = g.activePowerLabels();
    for (const t of timers) {
      ctx.save();
      ctx.font = `bold 13px ${UI.FONT}`;
      ctx.textAlign = 'left';
      ctx.fillStyle = t.color;
      ctx.globalAlpha = 0.9;
      ctx.fillText(t.label, 40, ty);
      ctx.fillStyle = 'rgba(255,255,255,0.2)';
      ctx.fillRect(40, ty + 5, 90, 4);
      ctx.fillStyle = t.color;
      ctx.fillRect(40, ty + 5, 90 * t.frac, 4);
      ctx.restore();
      ty += 24;
    }

    // Combo meter (right side, animated fill) — below the pause button
    const comboFrac = clamp(g.combo / 10, 0, 1);
    ctx.save();
    ctx.translate(CONFIG.W - 44, 116);
    ctx.fillStyle = 'rgba(255,255,255,0.1)';
    this._roundRect(ctx, -14, -60, 16, 120, 8); ctx.fill();
    const grd = ctx.createLinearGradient(0, 60, 0, -60);
    grd.addColorStop(0, '#4fd1ff'); grd.addColorStop(1, '#ffe14f');
    ctx.fillStyle = grd;
    const fh = 114 * comboFrac;
    this._roundRect(ctx, -12, 58 - fh, 12, Math.max(0, fh), 6); ctx.fill();
    ctx.fillStyle = '#fff';
    ctx.font = `bold 13px ${UI.FONT}`;
    ctx.textAlign = 'center';
    ctx.fillText(`x${g.combo}`, -6, 76);
    ctx.restore();

    // Fever multiplier display (center-top, big while active)
    if (g.multiplier > 1.05) {
      const pulse = 1 + Math.sin(g.time * 8) * 0.05 * clamp(g.multiplier / 6, 0, 1);
      ctx.save();
      ctx.translate(CONFIG.W / 2, 62);
      ctx.scale(pulse, pulse);
      ctx.font = `900 26px ${UI.FONT}`;
      ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
      ctx.lineWidth = 5; ctx.strokeStyle = 'rgba(0,0,0,0.6)';
      ctx.strokeText(`×${g.multiplier.toFixed(1)}`, 0, 0);
      ctx.fillStyle = g.feverTier >= 2 ? '#ff9ae0' : '#ffe14f';
      ctx.fillText(`×${g.multiplier.toFixed(1)}`, 0, 0);
      ctx.restore();
    }

    // Pause button (top corner, thumb reachable)
    const pb = { x: CONFIG.W - 54, y: 8, w: 44, h: 36 };
    this.btns.push({ ...pb, action: 'pause', label: '❚❚' });
    ctx.save();
    ctx.fillStyle = 'rgba(255,255,255,0.1)';
    ctx.strokeStyle = 'rgba(255,255,255,0.4)';
    ctx.lineWidth = 1.5;
    if (ctx.roundRect) { ctx.beginPath(); ctx.roundRect(pb.x, pb.y, pb.w, pb.h, 8); ctx.fill(); ctx.stroke(); }
    else ctx.fillRect(pb.x, pb.y, pb.w, pb.h);
    ctx.fillStyle = '#fff';
    ctx.font = 'bold 14px sans-serif';
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    ctx.fillText('❚❚', pb.x + pb.w / 2, pb.y + pb.h / 2);
    ctx.restore();
    g.pauseBtnRect = pb;
  }

  _roundRect(ctx, x, y, w, h, r) {
    if (ctx.roundRect) { ctx.beginPath(); ctx.roundRect(x, y, w, h, r); return; }
    ctx.beginPath(); ctx.rect(x, y, w, h);
  }

  // ---- screens ------------------------------------------------------------

  drawTitle() {
    const g = this.game, ctx = g.ctx;
    this.reset();
    this.dim(0.35);
    this.title('ATTIC', 300, 92);
    this.title('BREAKOUT', 400, 72, '#7fd4ff');
    this.text('Break through to the space above the bricks.', 480, { size: 19, color: '#bcd7ef' });
    this.text('Live in the attic. Ramp the multiplier. Get loud.', 512, { size: 17, color: '#7f9cbd' });
    const t = performance.now() / 1000;
    const a = 0.75 + Math.sin(t * 2.4) * 0.25;
    this.text('CLICK / TAP TO START', 600, { size: 24, weight: 'bold', color: '#fff', alpha: a });
    this.text('Mouse • WASD/Arrows • Touch thumbstick', 650, { size: 15, color: '#6f89a8' });
    this.text('M mute   P pause', 676, { size: 14, color: '#52708f' });

    const y = CONFIG.H - 190;
    this.button(CONFIG.W / 2 - 130, y, 260, 56, 'PLAY', 'start', { primary: true });
    this.button(CONFIG.W / 2 - 130, y + 72, 260, 44, 'SETTINGS', 'settings');
  }

  drawSettings() {
    const g = this.game, ctx = g.ctx;
    this.reset();
    this.dim(0.7);
    this.title('SETTINGS', 200, 44);
    const schemeNames = { auto: 'AUTO', mouse: 'MOUSE', keyboard: 'KEYBOARD', touch: 'TOUCH' };
    this.button(CONFIG.W / 2 - 170, 270, 340, 54, `CONTROLS: ${schemeNames[g.input.scheme]}`, 'cycleScheme');
    this.button(CONFIG.W / 2 - 170, 340, 340, 54, `TOUCH MODE: ${g.input.followFinger ? 'FOLLOW FINGER' : 'THUMBSTICK'}`, 'toggleFollow');
    this.button(CONFIG.W / 2 - 170, 410, 340, 54, `SOUND: ${g.audio.muted ? 'OFF' : 'ON'}`, 'toggleMute');
    this.button(CONFIG.W / 2 - 130, 520, 260, 54, 'BACK', 'back', { primary: true });
  }

  drawLevelIntro() {
    const g = this.game;
    this.reset();
    const t = g.stateT;
    const a = clamp(Math.min(t * 3, (g.introDur - t) * 2), 0, 1);
    this.text(`LEVEL ${g.level + 1}`, CONFIG.H / 2 - 40, { size: 40, weight: 'bold', color: '#fff', alpha: a });
    this.text(g.levelDef.name, CONFIG.H / 2 + 16, { size: 28, color: '#7fd4ff', alpha: a });
    this.text(g.levelDef.hint, CONFIG.H / 2 + 66, { size: 17, color: '#bcd7ef', alpha: a });
  }

  drawPause() {
    const g = this.game;
    this.reset();
    this.dim(0.66);
    this.title('PAUSED', 320, 52);
    this.text(`SCORE ${fmtScore(g.score)}   BEST ${fmtScore(g.best)}`, 380, { size: 18, color: '#9db8d4' });
    this.button(CONFIG.W / 2 - 130, 440, 260, 54, 'RESUME', 'resume', { primary: true });
    this.button(CONFIG.W / 2 - 130, 508, 260, 50, 'RESTART LEVEL', 'restart');
    this.button(CONFIG.W / 2 - 130, 572, 260, 50, `SOUND: ${g.audio.muted ? 'OFF' : 'ON'}`, 'toggleMute');
    this.button(CONFIG.W / 2 - 130, 636, 260, 50, `TOUCH: ${g.input.followFinger ? 'FOLLOW' : 'THUMBSTICK'}`, 'toggleFollow');
    this.button(CONFIG.W / 2 - 130, 700, 260, 50, 'QUIT TO TITLE', 'quit');
  }

  drawGameOver() {
    const g = this.game;
    this.reset();
    this.dim(0.72);
    this.title('GAME OVER', 300, 64, '#ff7d8a');
    this.text(`SCORE  ${fmtScore(g.score)}`, 390, { size: 30, weight: 'bold', color: '#fff' });
    this.text(`BEST  ${fmtScore(g.best)}`, 436, { size: 20, color: '#9db8d4' });
    this.text(g.statsLine(), 480, { size: 16, color: '#7f9cbd' });
    const nb = g.score >= g.best && g.score > 0;
    if (nb) this.text('★ NEW BEST ★', 516, { size: 20, weight: 'bold', color: '#ffe14f' });
    this.button(CONFIG.W / 2 - 130, 570, 260, 56, 'PLAY AGAIN', 'start', { primary: true });
    this.button(CONFIG.W / 2 - 130, 642, 260, 50, 'QUIT TO TITLE', 'quit');
  }

  drawWin() {
    const g = this.game;
    this.reset();
    this.dim(0.7);
    this.title('YOU MADE IT', 280, 58, '#ffe14f');
    this.title('OUT OF THE ATTIC', 348, 34, '#7fd4ff');
    this.text(`FINAL SCORE  ${fmtScore(g.score)}`, 430, { size: 28, weight: 'bold', color: '#fff' });
    this.text(g.statsLine(), 478, { size: 17, color: '#bcd7ef' });
    this.text(`BEST  ${fmtScore(g.best)}`, 510, { size: 18, color: '#9db8d4' });
    this.button(CONFIG.W / 2 - 130, 570, 260, 56, 'PLAY AGAIN', 'start', { primary: true });
    this.button(CONFIG.W / 2 - 130, 642, 260, 50, 'QUIT TO TITLE', 'quit');
  }

  drawLevelClear() {
    const g = this.game;
    this.reset();
    const t = g.stateT;
    const a = clamp(Math.min(t * 3, (g.introDur - t) * 2), 0, 1);
    this.text('LEVEL CLEAR!', CONFIG.H / 2 - 60, { size: 46, weight: 'bold', color: '#ffe14f', alpha: a });
    this.text(`Clear bonus +${fmtScore(g.clearBonus)}`, CONFIG.H / 2 + 6, { size: 22, color: '#fff', alpha: a });
    this.text(`Time bonus +${fmtScore(g.timeBonus)}`, CONFIG.H / 2 + 44, { size: 22, color: '#fff', alpha: a });
    this.text('Next level incoming…', CONFIG.H / 2 + 100, { size: 17, color: '#7f9cbd', alpha: a });
  }
}

window.UI = UI;
window.UIRenderer = UIRenderer;
