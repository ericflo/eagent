// Canvas rendering: background, ozone band, bricks, paddle, balls, capsules, HUD, screens.
import { W, H, OZONE_H, FIELD_TOP, FIELD_LEFT, ZONE_TOP, COLORS, BRICK_STYLE, PT_STYLE, CHAOS_CAP } from './constants.js';

const BG_GRAD = null; // cached lazily

export class Render {
  constructor(ctx, game) {
    this.ctx = ctx;
    this.g = game;
    this.bgGrad = null;
    this.hue = 0;
  }

  draw(dt) {
    const ctx = this.ctx, g = this.g;
    const chaos01 = Math.min(1, Math.log2(g.chaosMult) / Math.log2(CHAOS_CAP));
    this.hue += dt * (10 + 60 * chaos01);
    const hueShift = g.chaosOn ? this.hue : 0;

    // background
    if (!this.bgGrad) {
      this.bgGrad = ctx.createLinearGradient(0, 0, 0, H);
      this.bgGrad.addColorStop(0, '#0b1030');
      this.bgGrad.addColorStop(0.5, '#070812');
      this.bgGrad.addColorStop(1, '#04050c');
    }
    ctx.fillStyle = this.bgGrad;
    ctx.fillRect(0, 0, W, H);
    if (g.chaosOn || g.chaosMult > 1) {
      ctx.globalAlpha = 0.06 + 0.10 * chaos01;
      ctx.fillStyle = `hsl(${(hueShift % 360) | 0} 90% 55%)`;
      ctx.fillRect(0, 0, W, H);
      ctx.globalAlpha = 1;
    }
    this.drawStars(chaos01, dt);

    // paddle move zone
    ctx.strokeStyle = 'rgba(120,150,200,0.10)';
    ctx.setLineDash([6, 10]);
    ctx.beginPath(); ctx.moveTo(0, ZONE_TOP); ctx.lineTo(W, ZONE_TOP); ctx.stroke();
    ctx.setLineDash([]);

    // ozone band
    const ozPulse = g.chaosOn ? 0.5 + 0.5 * Math.sin(g.time * (4 + chaos01 * 10)) : 0.25;
    const ozGrad = ctx.createLinearGradient(0, 0, 0, OZONE_H + 30);
    ozGrad.addColorStop(0, `rgba(143,107,255,${0.25 + 0.3 * ozPulse})`);
    ozGrad.addColorStop(1, 'rgba(143,107,255,0)');
    ctx.fillStyle = ozGrad;
    ctx.fillRect(0, 0, W, OZONE_H + 30);
    ctx.strokeStyle = `rgba(201,182,255,${0.35 + 0.5 * ozPulse})`;
    ctx.shadowColor = COLORS.ozoneGlow; ctx.shadowBlur = 18;
    ctx.beginPath(); ctx.moveTo(0, OZONE_H); ctx.lineTo(W, OZONE_H); ctx.stroke();
    ctx.shadowBlur = 0;
    ctx.fillStyle = 'rgba(201,182,255,.55)';
    ctx.font = '10px ui-monospace, monospace';
    ctx.textAlign = 'left';
    ctx.fillText('OZONE', 12, OZONE_H - 8);

    this.drawBricks();
    this.drawShield();
    this.drawCapsules();
    this.drawPaddle();
    this.drawBalls();
    g.fx.draw(ctx);
    this.drawHUD();
    if (g.state === 'title') this.drawTitle();
    if (g.state === 'gameover') this.drawGameOver();
    if (g.state === 'levelintro') this.drawLevelIntro();
    if (g.state === 'paused') this.drawPaused();
  }

  drawStars(chaos01, dt) {
    const ctx = this.ctx, g = this.g;
    if (!g.stars) return;
    ctx.fillStyle = '#8fa8d8';
    for (const s of g.stars) {
      ctx.globalAlpha = 0.25 + 0.5 * Math.abs(Math.sin(g.time * s.tw + s.ph));
      ctx.fillRect(s.x, s.y, 2, 2);
    }
    ctx.globalAlpha = 1;
  }

  drawBricks() {
    const ctx = this.ctx, g = this.g;
    for (const b of g.bricks) {
      if (!b.alive) continue;
      const st = BRICK_STYLE[b.type];
      let alpha = 1;
      if (b.type === 'G') alpha = b.solid ? 0.95 : 0.22;
      ctx.globalAlpha = alpha;
      const x = b.x, y = b.y, w = b.w, h = b.h;
      // body
      const grad = ctx.createLinearGradient(x, y, x, y + h);
      grad.addColorStop(0, st.color); grad.addColorStop(1, st.color2);
      ctx.fillStyle = grad;
      ctx.beginPath(); ctx.roundRect(x, y, w, h, 5); ctx.fill();
      ctx.shadowColor = st.color; ctx.shadowBlur = b.hitFx > 0 ? 20 : 6;
      ctx.strokeStyle = st.color; ctx.lineWidth = 1.5;
      ctx.stroke(); ctx.shadowBlur = 0;
      // type markings
      ctx.font = 'bold 11px ui-monospace, monospace';
      ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
      if (b.type === 'A') { // speed lines
        ctx.strokeStyle = 'rgba(255,255,255,.35)';
        ctx.beginPath();
        for (let i = -1; i <= 1; i++) { ctx.moveTo(x + 8, y + h / 2 + i * 6); ctx.lineTo(x + w - 8, y + h / 2 + i * 6); }
        ctx.stroke();
        ctx.fillStyle = '#cfd8ff'; ctx.fillText('≡', x + w / 2, y + h / 2);
      } else if (b.type === 'P') { // facet triangle pointing down (needs steep hit)
        ctx.fillStyle = 'rgba(255,255,255,.8)';
        ctx.beginPath();
        ctx.moveTo(x + w / 2, y + h - 7); ctx.lineTo(x + w / 2 - 7, y + 7); ctx.lineTo(x + w / 2 + 7, y + 7);
        ctx.closePath(); ctx.fill();
      } else if (b.type === 'G') {
        ctx.fillStyle = b.solid ? '#0a2033' : 'rgba(255,255,255,.5)';
        ctx.fillText(b.solid ? '◆' : '◇', x + w / 2, y + h / 2);
        // phase timer ring
        const frac = b.phase / 1.6;
        ctx.strokeStyle = 'rgba(154,215,255,.8)';
        ctx.beginPath(); ctx.arc(x + w / 2, y + h / 2, h / 2 - 3, -Math.PI / 2, -Math.PI / 2 + frac * Math.PI * 2); ctx.stroke();
      } else if (b.type === 'V') {
        ctx.fillStyle = '#ffd6ff'; ctx.fillText('⚡', x + w / 2, y + h / 2);
      } else if (b.type === 'X') {
        ctx.fillStyle = '#ffd6ec'; ctx.fillText('◉', x + w / 2, y + h / 2);
      } else if (b.type === 'L') {
        ctx.fillStyle = 'rgba(255,255,255,.5)';
        ctx.beginPath(); ctx.ellipse(x + w / 2, y + h / 2, w / 3, h / 3.2, 0, 0, 7); ctx.fill();
      } else if (b.type === 'T') {
        ctx.fillStyle = 'rgba(255,255,255,.12)';
        ctx.fillRect(x + 3, y + 3, w - 6, h - 6);
        ctx.strokeStyle = 'rgba(0,0,0,.4)';
        ctx.strokeRect(x + 6.5, y + 6.5, w - 13, h - 13);
      }
      if (b.hitFx > 0) {
        ctx.globalAlpha = b.hitFx * 2;
        ctx.fillStyle = '#fff';
        ctx.beginPath(); ctx.roundRect(x, y, w, h, 5); ctx.fill();
      }
      ctx.globalAlpha = 1;
    }
    ctx.textBaseline = 'alphabetic';
  }

  drawShield() {
    const g = this.g, ctx = this.ctx;
    if (!g.shield) return;
    const y = H - 6;
    ctx.strokeStyle = '#c77dff';
    ctx.shadowColor = '#c77dff'; ctx.shadowBlur = 14;
    ctx.lineWidth = 3;
    ctx.beginPath(); ctx.moveTo(0, y); ctx.lineTo(W, y); ctx.stroke();
    ctx.shadowBlur = 0; ctx.lineWidth = 1;
  }

  drawPaddle() {
    const ctx = this.ctx, g = this.g, p = g.paddle;
    ctx.shadowColor = COLORS.paddle; ctx.shadowBlur = 16;
    const grad = ctx.createLinearGradient(0, p.y, 0, p.y + p.h);
    grad.addColorStop(0, '#9ffcff'); grad.addColorStop(1, '#00b4d8');
    ctx.fillStyle = p.magnet ? PT_STYLE.G.color : grad;
    ctx.beginPath(); ctx.roundRect(p.x, p.y, p.w, p.h, 8); ctx.fill();
    ctx.shadowBlur = 0;
    // center notch & smash indicator
    ctx.fillStyle = 'rgba(5,10,20,.7)';
    ctx.fillRect(p.cx - 2, p.y + 3, 4, p.h - 6);
    if (Math.abs(p.vy) > 40) {
      const up = p.vy < 0;
      ctx.fillStyle = up ? '#ff2d78' : '#5b6a8c';
      ctx.font = 'bold 12px ui-monospace, monospace';
      ctx.textAlign = 'center';
      ctx.fillText(up ? '▲' : '▼', p.cx, p.y - 4);
    }
    if (p.magnet) {
      ctx.strokeStyle = 'rgba(255,209,102,.7)';
      ctx.beginPath(); ctx.arc(p.cx, p.y, 20 + Math.sin(g.time * 6) * 3, 0, 7); ctx.stroke();
    }
  }

  drawBalls() {
    const ctx = this.ctx, g = this.g;
    for (const ball of g.balls) {
      // trail
      for (let i = 0; i < ball.trail.length; i++) {
        const t = ball.trail[i];
        const a = (i / ball.trail.length) * (g.chaosOn ? 0.7 : 0.4);
        ctx.globalAlpha = a;
        ctx.fillStyle = ball.fire > 0 ? '#ff7b2d' : (g.chaosOn ? `hsl(${(g.fx.hueRef || 300) | 0} 100% 70%)` : COLORS.ball);
        ctx.beginPath(); ctx.arc(t.x, t.y, ball.r * (0.3 + 0.7 * i / ball.trail.length), 0, 7); ctx.fill();
      }
      ctx.globalAlpha = 1;
      const c = ball.fire > 0 ? '#ff7b2d' : ball.phase > 0 ? '#b8fffa' : COLORS.ball;
      ctx.shadowColor = c; ctx.shadowBlur = ball.fire > 0 ? 25 : 14;
      ctx.fillStyle = c;
      ctx.beginPath(); ctx.arc(ball.x, ball.y, ball.r, 0, 7); ctx.fill();
      ctx.shadowBlur = 0;
      if (ball.stuck) {
        ctx.strokeStyle = '#ffd166'; ctx.globalAlpha = 0.6;
        ctx.beginPath(); ctx.arc(ball.x, ball.y, ball.r + 5, 0, 7); ctx.stroke();
        ctx.globalAlpha = 1;
      }
    }
  }

  drawCapsules() {
    const ctx = this.ctx, g = this.g;
    for (const c of g.capsules) {
      const st = PT_STYLE[c.kind];
      ctx.save();
      ctx.translate(c.x, c.y);
      ctx.rotate(Math.sin(c.t * 4) * 0.15);
      ctx.shadowColor = st.color; ctx.shadowBlur = 12;
      ctx.fillStyle = 'rgba(10,14,26,.9)';
      ctx.beginPath(); ctx.roundRect(-c.w / 2, -c.h / 2, c.w, c.h, 12); ctx.fill();
      ctx.strokeStyle = st.color; ctx.lineWidth = 2; ctx.stroke();
      ctx.shadowBlur = 0;
      ctx.fillStyle = st.color;
      ctx.font = 'bold 14px ui-monospace, monospace';
      ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
      ctx.fillText(st.glyph, 0, 1);
      ctx.restore();
    }
    ctx.textBaseline = 'alphabetic';
  }

  drawHUD() {
    const ctx = this.ctx, g = this.g;
    ctx.textAlign = 'left';
    ctx.font = 'bold 20px ui-monospace, monospace';
    ctx.fillStyle = COLORS.hud;
    ctx.fillText(String(g.score).padStart(7, '0'), 12, H - 12);
    ctx.font = '12px ui-monospace, monospace';
    ctx.fillStyle = 'rgba(159,180,216,.6)';
    ctx.fillText('BEST ' + g.best, 12, H - 30);
    // level + lives right side
    ctx.textAlign = 'right';
    ctx.font = 'bold 14px ui-monospace, monospace';
    ctx.fillStyle = COLORS.hud;
    ctx.fillText('LVL ' + (g.levelIndex + 1) + ' · ' + g.levelName, W - 12, H - 30);
    let lx = W - 16;
    for (let i = 0; i < g.maxLives; i++) {
      ctx.fillStyle = i < g.lives ? COLORS.accent : 'rgba(255,45,120,.15)';
      ctx.shadowColor = COLORS.accent; ctx.shadowBlur = i < g.lives ? 8 : 0;
      ctx.beginPath(); ctx.arc(lx, H - 14, 6, 0, 7); ctx.fill();
      ctx.shadowBlur = 0;
      lx -= 18;
    }
    // power-up timers
    let ty = OZONE_H + 18;
    ctx.textAlign = 'left';
    for (const [k, pw] of Object.entries(g.powerTimers)) {
      if (pw <= 0) continue;
      const st = PT_STYLE[k];
      ctx.fillStyle = st.color;
      ctx.font = 'bold 12px ui-monospace, monospace';
      ctx.fillText(st.name + ' ' + pw.toFixed(1), 12, ty);
      ty += 16;
    }
    // chaos ticker
    this.drawChaos();
  }

  drawChaos() {
    const ctx = this.ctx, g = this.g;
    const m = g.chaosMult;
    const cx = W / 2;
    if (m > 1) {
      const big = g.chaosOn;
      const pulse = 1 + (big ? Math.sin(g.time * 10) * 0.04 : 0);
      ctx.save();
      ctx.translate(cx, FIELD_TOP - 12);
      ctx.scale(pulse, pulse);
      ctx.font = `bold ${big ? 34 : 22}px ui-monospace, monospace`;
      ctx.textAlign = 'center';
      ctx.fillStyle = `hsl(${(g.fx.hueRef = (g.fx.hueRef || 0) + 0) } 100% 65%)`;
      ctx.fillStyle = big ? '#ffe14d' : 'rgba(255,225,77,.6)';
      ctx.shadowColor = '#ff9d00'; ctx.shadowBlur = big ? 22 : 8;
      ctx.fillText('CHAOS ×' + m, 0, 0);
      ctx.restore();
      ctx.shadowBlur = 0;
    }
    // decay bar
    if (g.chaosDecay > 0 && !g.chaosOn) {
      const w = 220, frac = g.chaosDecay / g.chaosDecayMax;
      ctx.fillStyle = 'rgba(255,255,255,.12)';
      ctx.fillRect(cx - w / 2, FIELD_TOP - 8, w, 5);
      ctx.fillStyle = '#ffe14d';
      ctx.fillRect(cx - w / 2, FIELD_TOP - 8, w * frac, 5);
      ctx.strokeStyle = 'rgba(255,225,77,.5)';
      ctx.strokeRect(cx - w / 2, FIELD_TOP - 8, w, 5);
    }
    // combo
    if (g.combo >= 3) {
      ctx.font = 'bold 16px ui-monospace, monospace';
      ctx.textAlign = 'right';
      ctx.fillStyle = '#2de2a6';
      ctx.shadowColor = '#2de2a6'; ctx.shadowBlur = 10;
      ctx.fillText('COMBO ×' + g.combo, W - 12, 34);
      ctx.shadowBlur = 0;
    }
  }

  centerText(txt, y, size, color, glow) {
    const ctx = this.ctx;
    ctx.font = `bold ${size}px ui-monospace, monospace`;
    ctx.textAlign = 'center';
    ctx.fillStyle = color;
    if (glow) { ctx.shadowColor = glow; ctx.shadowBlur = 24; }
    ctx.fillText(txt, W / 2, y);
    ctx.shadowBlur = 0;
  }

  drawTitle() {
    const ctx = this.ctx, g = this.g;
    ctx.fillStyle = 'rgba(4,5,12,.72)';
    ctx.fillRect(0, 0, W, H);
    const t = g.time;
    ctx.save();
    ctx.translate(W / 2, 300);
    for (let i = 0; i < 12; i++) {
      ctx.globalAlpha = 0.12;
      ctx.fillStyle = i % 2 ? '#00f6ff' : '#ff2d78';
      const s = 1 + i * 0.06 + Math.sin(t * 2 + i) * 0.01;
      ctx.font = `bold ${72 * s}px ui-monospace, monospace`;
      ctx.textAlign = 'center';
      ctx.fillText('BREAKTHROUGH', 0, 0);
    }
    ctx.restore();
    ctx.globalAlpha = 1;
    this.centerText('BREAKTHROUGH', 300, 72, '#eafcff', '#00f6ff');
    this.centerText('BREAK THE CEILING. RIDE THE OZONE.', 350, 17, '#8f6bff');
    this.centerText('BEST ' + g.best, 430, 18, 'rgba(159,180,216,.8)');
    const blink = Math.sin(t * 4) > -0.2;
    if (blink) this.centerText('TAP · CLICK · SPACE  TO START', 560, 22, '#2de2a6', '#2de2a6');
    this.centerText('DRAG to move (anywhere) · paddle SMASHES upward · get the ball ABOVE the bricks', 660, 13, 'rgba(159,180,216,.75)');
    this.centerText('↑↓ move vertically · build CHAOS on top · don\'t drop below for long', 684, 13, 'rgba(159,180,216,.55)');
  }

  drawGameOver() {
    const ctx = this.ctx;
    ctx.fillStyle = 'rgba(4,5,12,.8)';
    ctx.fillRect(0, 0, W, H);
    this.centerText('GAME OVER', 380, 60, '#ff2d78', '#ff2d78');
    this.centerText('SCORE ' + g_score(this), 460, 30, '#eafcff');
    this.centerText('BEST ' + this.g.best + (this.g.score >= this.g.best && this.g.score > 0 ? '  ★ NEW!' : ''), 500, 20, 'rgba(159,180,216,.8)');
    this.centerText('REACHED LEVEL ' + (this.g.levelIndex + 1), 540, 16, 'rgba(159,180,216,.6)');
    if (Math.sin(this.g.time * 4) > -0.2) this.centerText('TAP · CLICK · SPACE TO RETRY', 640, 20, '#2de2a6', '#2de2a6');
  }

  drawLevelIntro() {
    const ctx = this.ctx;
    ctx.fillStyle = 'rgba(4,5,12,.55)';
    ctx.fillRect(0, 0, W, H);
    const frac = this.g.stateTimer / 2;
    const slide = (1 - Math.min(1, frac * 3)) * 80;
    this.centerText('LEVEL ' + (this.g.levelIndex + 1), 440 + slide, 54, '#00f6ff', '#00f6ff');
    this.centerText(this.g.levelName, 500 + slide, 28, '#eafcff');
    this.centerText('GET THE BALL ABOVE THE BRICKS', 700, 14, 'rgba(143,107,255,.8)');
  }

  drawPaused() {
    const ctx = this.ctx;
    ctx.fillStyle = 'rgba(4,5,12,.7)';
    ctx.fillRect(0, 0, W, H);
    this.centerText('PAUSED', 420, 50, '#eafcff', '#00f6ff');
    this.centerText('P / ESC — RESUME', 500, 16, 'rgba(159,180,216,.8)');
    this.centerText('M — ' + (this.g.audio.muted ? 'UNMUTE' : 'MUTE') + '   ·   R — RESTART LEVEL', 530, 16, 'rgba(159,180,216,.8)');
  }
}

function g_score(r) { return String(r.g.score); }
