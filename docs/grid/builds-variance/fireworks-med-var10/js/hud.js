// SKYBREAK — in-canvas HUD: score, mult, lives, active powerups, hype meter,
// level name, ON TOP callout. Drawn last, unaffected by screen shake.

const HUD_FONT = '800 %spx "Avenir Next", "Segoe UI", system-ui, sans-serif';

function hudFont(px) { return HUD_FONT.replace('%s', px); }

class HUD {
  constructor() { this.pulse = 0; }

  // pulse: 0..1 burst set when multiplier tiers up
  draw(ctx, game) {
    const { score, mult, lives, powerupTimers, levelIndex } = game;
    const onTop = game.ontop;

    // Top bar backdrop.
    const grad = ctx.createLinearGradient(0, 0, 0, 92);
    grad.addColorStop(0, 'rgba(7,11,22,0.95)');
    grad.addColorStop(1, 'rgba(7,11,22,0)');
    ctx.fillStyle = grad;
    ctx.fillRect(0, 0, CONFIG.W, 92);

    ctx.textBaseline = 'middle';

    // Score (left)
    ctx.textAlign = 'left';
    ctx.fillStyle = '#ffffff';
    ctx.shadowColor = '#35e0ff'; ctx.shadowBlur = 10;
    ctx.font = hudFont(38);
    ctx.fillText(fmtScore(score), 24, 34);
    ctx.shadowBlur = 0;
    ctx.fillStyle = 'rgba(143,160,200,0.9)';
    ctx.font = hudFont(14);
    ctx.fillText('SCORE', 25, 66);

    // Level name (center)
    ctx.textAlign = 'center';
    ctx.fillStyle = 'rgba(143,160,200,0.95)';
    ctx.font = hudFont(17);
    ctx.fillText(`${levelIndex + 1}. ${LEVELS[levelIndex].name}`, CONFIG.W / 2, 30);

    // Lives (top-right)
    ctx.textAlign = 'right';
    for (let i = 0; i < Math.min(lives, 6); i++) {
      const x = CONFIG.W - 26 - i * 26;
      ctx.save();
      ctx.shadowColor = '#ff4d6d'; ctx.shadowBlur = 8;
      ctx.fillStyle = '#ff4d6d';
      ctx.beginPath();
      ctx.arc(x, 30, 7, 0, TAU);
      ctx.fill();
      ctx.restore();
    }
    if (lives > 6) {
      ctx.fillStyle = '#ff4d6d';
      ctx.font = hudFont(15);
      ctx.fillText(`x${lives}`, CONFIG.W - 190, 30);
    }

    // Multiplier (under lives) — ramps while on top.
    const showMult = mult >= CONFIG.HYPE_MIN_VIS_MULT;
    if (showMult) {
      const heat = clamp((mult - 2) / (CONFIG.HYPE_MAX_MULT - 2), 0, 1);
      const size = 26 + heat * 14 + this.pulse * 10;
      const col = heat > 0.66 ? '#ff4d6d' : heat > 0.33 ? '#ffd23f' : '#35e0ff';
      ctx.save();
      ctx.textAlign = 'right';
      ctx.shadowColor = col; ctx.shadowBlur = 18;
      ctx.fillStyle = col;
      ctx.font = hudFont(size);
      ctx.fillText(`x${mult.toFixed(1)}`, CONFIG.W - 24, 70);
      ctx.restore();
    }

    // Active powerup timers (left side, under score).
    let py = 110;
    const now = performance.now() / 1000;
    for (const [kind, t] of powerupTimers) {
      const def = POWERUPS[kind];
      ctx.save();
      ctx.textAlign = 'left';
      ctx.shadowColor = def.color; ctx.shadowBlur = 8;
      ctx.fillStyle = def.color;
      ctx.font = hudFont(15);
      const frac = clamp(t / def.duration, 0, 1);
      ctx.fillText(`${def.label}`, 24, py);
      ctx.globalAlpha = 0.35;
      ctx.fillRect(86, py - 2, 90, 4);
      ctx.globalAlpha = 1;
      ctx.fillRect(86, py - 2, 90 * frac, 4);
      ctx.restore();
      py += 24;
    }

    // ON TOP callout — big, breathing, center-top.
    if (onTop) {
      const t = now * 4;
      const scale = 1 + Math.sin(t) * 0.05;
      ctx.save();
      ctx.translate(CONFIG.W / 2, CONFIG.ONTOP_Y + 30);
      ctx.scale(scale, scale);
      ctx.textAlign = 'center';
      ctx.font = hudFont(46);
      ctx.shadowColor = '#ff4d6d'; ctx.shadowBlur = 26;
      ctx.fillStyle = `rgba(255,210,63,${0.85 + Math.sin(t * 2) * 0.15})`;
      ctx.fillText('ON TOP!', 0, 0);
      ctx.restore();
    }

    // Ball-in-waiting hint.
    if (game.waitingLaunch) {
      ctx.save();
      ctx.textAlign = 'center';
      ctx.globalAlpha = 0.6 + Math.sin(now * 5) * 0.3;
      ctx.fillStyle = '#ffffff';
      ctx.font = hudFont(18);
      ctx.fillText('TAP / SPACE TO LAUNCH', CONFIG.W / 2, CONFIG.H - 70);
      ctx.restore();
    }
  }
}
