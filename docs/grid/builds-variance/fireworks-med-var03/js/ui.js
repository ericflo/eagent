'use strict';
// ---------------------------------------------------------------------------
// ui.js — screen-space HUD, title / pause / gameover / levelclear overlays,
// thumbstick rendering, screen border glow. All coordinates are in logical
// screen space (W×H) scaled by the view scale.
// ---------------------------------------------------------------------------

const UI = (() => {
  let popT = 0;         // multiplier chip pop timer
  let lastMulti = 1;
  const tapTargets = [];  // rebuilt each frame for hit-testing
  let time = 0;

  function font(size, weight) {
    return `${weight || 800} ${Math.round(size)}px "SF Mono", ui-monospace, Menlo, Consolas, monospace`;
  }

  function fontSans(size, weight) {
    return `${weight || 800} ${Math.round(size)}px system-ui, -apple-system, 'Segoe UI', sans-serif`;
  }

  // register a tappable rect (logical screen coords); Game checks clicks here
  function tap(id, x, y, w, h) {
    tapTargets.push({ id, x, y, w, h });
  }

  function hitTest(px, py) {
    for (const t of tapTargets) {
      if (px >= t.x && px <= t.x + t.w && py >= t.y && py <= t.y + t.h) return t.id;
    }
    return null;
  }

  function multiColor(m) {
    if (m >= 12) return '#ff4dd8';
    if (m >= 8) return '#ffb347';
    if (m >= 4) return '#ffe066';
    return '#ffffff';
  }

  // ------------------------------------------------------------------ HUD --
  function drawHUD(ctx, game) {
    time += 1 / 60;
    if (game.multiplier !== lastMulti) { popT = 1; lastMulti = game.multiplier; }
    if (popT > 0) popT = Math.max(0, popT - 1 / 12);

    tapTargets.length = 0;

    // score
    ctx.textAlign = 'left'; ctx.textBaseline = 'top';
    ctx.font = font(26);
    ctx.fillStyle = 'rgba(255,255,255,0.95)';
    ctx.shadowBlur = 8; ctx.shadowColor = 'rgba(120,240,255,0.6)';
    ctx.fillText(fmtScore(game.score), 16, 12);
    ctx.shadowBlur = 0;

    // multiplier chip
    if (game.multiplier > 1) {
      const pop = 1 + easeOutBack(1 - popT) * 0.35;
      const label = '×' + game.multiplier;
      ctx.save();
      ctx.translate(18, 48);
      ctx.scale(pop, pop);
      ctx.font = font(15);
      const tw = ctx.measureText(label).width;
      ctx.fillStyle = 'rgba(10,14,30,0.75)';
      roundRect(ctx, -4, -3, tw + 12, 22, 10);
      ctx.fill();
      ctx.strokeStyle = multiColor(game.multiplier);
      ctx.lineWidth = 1.5;
      roundRect(ctx, -4, -3, tw + 12, 22, 10);
      ctx.stroke();
      ctx.fillStyle = multiColor(game.multiplier);
      ctx.textAlign = 'left'; ctx.textBaseline = 'middle';
      ctx.fillText(label, 2, 8);
      ctx.restore();
    }

    // lives (ball icons)
    for (let i = 0; i < game.lives; i++) {
      ctx.beginPath();
      ctx.arc(W - 24 - i * 18, 24, 6, 0, TAU);
      ctx.fillStyle = '#8ef7ff';
      ctx.shadowBlur = 8; ctx.shadowColor = '#4ef0ff';
      ctx.fill();
      ctx.shadowBlur = 0;
    }

    // level name
    ctx.font = fontSans(11, 600);
    ctx.fillStyle = 'rgba(255,255,255,0.55)';
    ctx.textAlign = 'center';
    ctx.fillText(game.levelName.toUpperCase(), W / 2, 12);

    // pause + mute buttons (44px tap targets)
    drawIconButton(ctx, W - 30, 52, 30, 'pause');
    tap('pause', W - 60, 36, 44, 44);
    drawIconButton(ctx, 30, 52, 30, AudioSys.enabled ? 'sound' : 'mute');
    tap('mute', 8, 36, 44, 44);

    // overdrive / high-multi border glow
    if (game.overdrive || game.multiplier >= 8) {
      const t = performance.now() / 1000;
      const pulse = 0.35 + 0.3 * Math.sin(t * 6);
      const g = ctx.createLinearGradient(0, 0, 0, H);
      const hue = game.overdrive ? 300 : 45;
      g.addColorStop(0, `hsla(${hue},100%,65%,${pulse})`);
      g.addColorStop(0.15, 'hsla(0,0%,0%,0)');
      g.addColorStop(0.85, 'hsla(0,0%,0%,0)');
      g.addColorStop(1, `hsla(${hue},100%,65%,${pulse})`);
      ctx.fillStyle = g;
      ctx.fillRect(0, 0, W, 8);
      ctx.fillRect(0, H - 8, W, 8);
      ctx.fillRect(0, 0, 8, H);
      ctx.fillRect(W - 8, 0, 8, H);
    }
  }

  function drawIconButton(ctx, cx, cy, r, kind) {
    ctx.save();
    ctx.globalAlpha = 0.7;
    ctx.fillStyle = 'rgba(255,255,255,0.12)';
    ctx.beginPath(); ctx.arc(cx, cy, r / 2 + 6, 0, TAU); ctx.fill();
    ctx.strokeStyle = '#fff'; ctx.fillStyle = '#fff';
    ctx.lineWidth = 2;
    if (kind === 'pause') {
      ctx.fillRect(cx - 4, cy - 6, 3, 12);
      ctx.fillRect(cx + 1, cy - 6, 3, 12);
    } else {
      // speaker
      ctx.beginPath();
      ctx.moveTo(cx - 7, cy - 3); ctx.lineTo(cx - 3, cy - 3); ctx.lineTo(cx + 1, cy - 7);
      ctx.lineTo(cx + 1, cy + 7); ctx.lineTo(cx - 3, cy + 3); ctx.lineTo(cx - 7, cy + 3);
      ctx.closePath(); ctx.fill();
      if (kind === 'sound') {
        ctx.beginPath(); ctx.arc(cx + 2, cy, 6, -0.9, 0.9); ctx.stroke();
      } else {
        ctx.beginPath();
        ctx.moveTo(cx + 3, cy - 4); ctx.lineTo(cx + 10, cy + 4);
        ctx.moveTo(cx + 10, cy - 4); ctx.lineTo(cx + 3, cy + 4);
        ctx.stroke();
      }
    }
    ctx.restore();
  }

  // -------------------------------------------------------------- screens --
  function dim(ctx, a) {
    ctx.fillStyle = `rgba(4,6,16,${a})`;
    ctx.fillRect(0, 0, W, H);
  }

  function drawTitle(ctx, game) {
    dim(ctx, 0.35);
    const t = performance.now() / 1000;
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    // logo
    ctx.save();
    ctx.translate(W / 2, H * 0.34 + Math.sin(t * 1.2) * 5);
    const g = ctx.createLinearGradient(0, -40, 0, 40);
    g.addColorStop(0, '#8ef7ff');
    g.addColorStop(0.5, '#4ef0ff');
    g.addColorStop(1, '#c74dff');
    ctx.font = font(52);
    ctx.shadowBlur = 24; ctx.shadowColor = 'rgba(120,240,255,0.8)';
    ctx.fillStyle = g;
    ctx.fillText('BREAKPOINT', 0, 0);
    ctx.restore();
    ctx.font = fontSans(15);
    ctx.fillStyle = 'rgba(200,240,255,0.8)';
    ctx.fillText('get on top of the bricks', W / 2, H * 0.34 + 44);
    // high score
    ctx.font = font(13);
    ctx.fillStyle = 'rgba(255,255,255,0.5)';
    ctx.fillText('HI ' + fmtScore(game.hiScore), W / 2, H * 0.34 + 74);
    // CTA
    const a = 0.55 + 0.45 * Math.sin(t * 4);
    ctx.font = fontSans(17);
    ctx.fillStyle = `rgba(255,255,255,${a})`;
    ctx.fillText('TAP / CLICK TO START', W / 2, H * 0.62);
    ctx.font = fontSans(12);
    ctx.fillStyle = 'rgba(255,255,255,0.4)';
    ctx.fillText('break through the bricks to the top', W / 2, H * 0.62 + 26);
  }

  function drawLevelIntro(ctx, game) {
    const a = game.introT > 0.9 ? (1.2 - game.introT) / 0.3 : Math.min(1, game.introT / 0.2);
    ctx.globalAlpha = clamp(a, 0, 1);
    dim(ctx, 0.45);
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    ctx.font = font(15);
    ctx.fillStyle = '#8ef7ff';
    ctx.fillText('LEVEL ' + game.level, W / 2, H * 0.42);
    ctx.font = fontSans(26);
    ctx.fillStyle = '#fff';
    ctx.fillText(game.levelName, W / 2, H * 0.42 + 34);
    ctx.globalAlpha = 1;
  }

  function drawHints(ctx, game) {
    if (game.level !== 1 || game.hintDone) return;
    ctx.font = fontSans(12);
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    ctx.fillStyle = 'rgba(255,255,255,0.75)';
    ctx.fillText('DRAG or ARROWS/WASD to move', W / 2, H - 180);
    ctx.fillText('TAP to launch', W / 2, H - 160);
    ctx.fillText('touch lower-left for thumbstick', W / 2, H - 140);
    ctx.fillStyle = 'rgba(255,224,102,0.9)';
    ctx.fillText('move UP as you hit to LIFT', W / 2, H - 120);
  }

  function drawPause(ctx) {
    dim(ctx, 0.6);
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    ctx.font = font(34);
    ctx.fillStyle = '#fff';
    ctx.fillText('PAUSED', W / 2, H * 0.4);
    ctx.font = fontSans(14);
    ctx.fillStyle = 'rgba(255,255,255,0.7)';
    ctx.fillText('TAP TO RESUME', W / 2, H * 0.55);
  }

  function drawGameOver(ctx, game) {
    dim(ctx, 0.7);
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    ctx.font = font(38);
    ctx.fillStyle = '#ff5470';
    ctx.fillText('GAME OVER', W / 2, H * 0.28);
    ctx.font = font(24);
    ctx.fillStyle = '#fff';
    ctx.fillText(fmtScore(game.score), W / 2, H * 0.38);
    if (game.newBest) {
      const t = performance.now() / 1000;
      ctx.font = fontSans(14);
      ctx.fillStyle = `rgba(255,224,102,${0.6 + 0.4 * Math.sin(t * 6)})`;
      ctx.fillText('NEW BEST!', W / 2, H * 0.38 + 30);
    }
    ctx.font = fontSans(13);
    ctx.fillStyle = 'rgba(255,255,255,0.75)';
    const s = game.stats;
    ctx.fillText(`bricks ${s.bricks} · max ×${s.maxMulti} · best on-top ${s.bestOverdrive.toFixed(1)}s`, W / 2, H * 0.5);
    const a = 0.5 + 0.5 * Math.sin(performance.now() / 1000 * 4);
    ctx.font = fontSans(16);
    ctx.fillStyle = `rgba(255,255,255,${a})`;
    ctx.fillText('TAP TO RESTART', W / 2, H * 0.64);
  }

  function drawLevelClear(ctx, game) {
    dim(ctx, 0.6);
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    ctx.font = font(30);
    ctx.fillStyle = '#78ffb4';
    ctx.fillText('LEVEL CLEAR', W / 2, H * 0.3);
    ctx.font = font(22);
    ctx.fillStyle = '#fff';
    ctx.fillText('+' + fmtScore(game.clearBonus), W / 2, H * 0.3 + 44);
    ctx.font = fontSans(13);
    ctx.fillStyle = 'rgba(255,255,255,0.7)';
    const s = game.stats;
    ctx.fillText(`bricks ${s.bricks} · max ×${s.maxMulti}`, W / 2, H * 0.3 + 84);
  }

  // thumbstick — screen space (canvas px converted by caller)
  function drawStick(ctx, st, view) {
    if (!st.stickActive || !st.stickOrigin || !st.stickPos) return;
    const sx = (st.stickOrigin.x - view.offX * view.dpr) / (view.s * view.dpr);
    const sy = (st.stickOrigin.y - view.offY * view.dpr) / (view.s * view.dpr);
    const px = (st.stickPos.x - view.offX * view.dpr) / (view.s * view.dpr);
    const py = (st.stickPos.y - view.offY * view.dpr) / (view.s * view.dpr);
    const R = 70 * view.s;
    ctx.save();
    ctx.globalAlpha = 0.45;
    ctx.strokeStyle = '#8ef7ff';
    ctx.lineWidth = 2;
    ctx.beginPath(); ctx.arc(sx, sy, R, 0, TAU); ctx.stroke();
    ctx.globalAlpha = 0.75;
    ctx.fillStyle = '#4ef0ff';
    ctx.beginPath(); ctx.arc(px, py, R * 0.35, 0, TAU); ctx.fill();
    ctx.restore();
  }

  return {
    drawHUD, drawTitle, drawLevelIntro, drawHints, drawPause, drawGameOver,
    drawLevelClear, drawStick, hitTest,
  };
})();
