// Renderer: canvas 2D with DPR handling, additive bloom-ish trails,
// background nebula, particles, popups, HUD. Reads game state; owns no logic.

import { POWER_META } from './core/powerups.js';
import { KIND, ANGLE_TOLERANCE, SPEED_THRESHOLD, phaseSolid, PHASE_PERIOD, MOVE_RANGE } from './core/bricks.js';

const BG_HUES = [215, 275, 330];

export class Renderer {
  constructor(canvas) {
    this.canvas = canvas;
    this.ctx = canvas.getContext('2d');
    this.dpr = 1;
    this.scaleX = 1; this.scaleY = 1;
    this.popups = [];
    this.shakeX = 0; this.shakeY = 0;
    this.resize();
  }

  resize() {
    const dpr = Math.min(window.devicePixelRatio || 1, 2.5);
    const w = window.innerWidth, h = window.innerHeight;
    this.canvas.width = Math.round(w * dpr);
    this.canvas.height = Math.round(h * dpr);
    this.canvas.style.width = w + 'px';
    this.canvas.style.height = h + 'px';
    this.dpr = dpr;
    this.viewW = w; this.viewH = h;
  }

  // Logical field is 480x800; letterbox-fit with cover semantics.
  computeView(field) {
    const s = Math.max(this.viewW / field.width, this.viewH / field.height);
    // Use 'contain' so the whole play area is visible:
    const sc = Math.min(this.viewW / field.width, this.viewH / field.height);
    this.scale = sc;
    this.offX = (this.viewW - field.width * sc) / 2;
    this.offY = (this.viewH - field.height * sc) / 2;
  }

  toField(px, py, field) {
    this.computeView(field);
    return { x: (px - this.offX) / this.scale, y: (py - this.offY) / this.scale };
  }

  addPopup(x, y, text, hue, big = false) {
    this.popups.push({ x, y, text, hue, life: 0.9, max: 0.9, big });
    if (this.popups.length > 40) this.popups.shift();
  }

  render(g, parts, dt) {
    const ctx = this.ctx;
    this.computeView(g.field);

    // Screen shake (scaled by multiplier heat)
    const sh = g.shake * (0.5 + Math.min(1, (g.mult.value - 1) / 12));
    this.shakeX = (Math.random() * 2 - 1) * 14 * sh;
    this.shakeY = (Math.random() * 2 - 1) * 14 * sh;

    // ---- background (with additive feedback trail for bloom feel)
    ctx.setTransform(this.dpr, 0, 0, this.dpr, 0, 0);
    ctx.globalCompositeOperation = 'source-over';
    const heat = g.mult.chaosHeat;
    const bgHue = BG_HUES[0] + (BG_HUES[1] - BG_HUES[0]) * heat + Math.sin(g.time * 0.3) * 18 * heat;
    ctx.fillStyle = `hsl(${bgHue}, 45%, ${4 + 5 * heat}%)`;
    ctx.fillRect(0, 0, this.viewW, this.viewH);

    ctx.globalCompositeOperation = 'lighter';
    ctx.fillStyle = `hsla(${bgHue + 30}, 80%, 60%, ${0.045 + 0.1 * heat})`;
    ctx.fillRect(0, 0, this.viewW, this.viewH);

    // pulsing radial glow (faster + brighter with heat)
    const pulse = 0.5 + 0.5 * Math.sin(g.time * (2 + 6 * heat));
    const cx = this.viewW / 2, cy = this.viewH * 0.35;
    const rad = Math.max(this.viewW, this.viewH) * (0.35 + 0.08 * pulse);
    const grad = ctx.createRadialGradient(cx, cy, 0, cx, cy, rad);
    grad.addColorStop(0, `hsla(${bgHue + 40}, 90%, 65%, ${0.05 + 0.16 * heat * pulse})`);
    grad.addColorStop(1, 'transparent');
    ctx.fillStyle = grad;
    ctx.fillRect(0, 0, this.viewW, this.viewH);

    // ---- world transform
    ctx.setTransform(this.dpr, 0, 0, this.dpr, 0, 0);
    ctx.translate(this.offX + this.shakeX, this.offY + this.shakeY);
    ctx.scale(this.scale, this.scale);

    this.drawFieldBounds(g, ctx, heat);
    this.drawBricks(g, ctx, parts);
    this.drawCapsules(g, ctx);
    this.drawPaddle(g, ctx);
    this.drawBalls(g, ctx, parts);
    this.drawParticles(ctx, parts);
    this.drawPopups(ctx, dt, g);
    this.drawThumbstick(g, ctx);
    this.drawHudText(g, ctx, heat);

    // white flash on power-up catch
    if (g.flash > 0.01) {
      ctx.setTransform(this.dpr, 0, 0, this.dpr, 0, 0);
      ctx.globalCompositeOperation = 'lighter';
      ctx.fillStyle = `rgba(255,255,255,${g.flash * 0.35})`;
      ctx.fillRect(0, 0, this.viewW, this.viewH);
    }
    ctx.globalCompositeOperation = 'source-over';
  }

  drawFieldBounds(g, ctx, heat) {
    ctx.globalCompositeOperation = 'lighter';
    ctx.strokeStyle = `hsla(${190 + 80 * heat}, 100%, 65%, ${0.25 + 0.3 * heat})`;
    ctx.lineWidth = 2;
    ctx.strokeRect(2, 2, g.field.width - 4, g.field.height - 4);
    ctx.globalCompositeOperation = 'source-over';
  }

  drawBricks(g, ctx, parts) {
    for (const b of g.bricks) {
      if (!b.alive) continue;
      const phased = b.kind === KIND.PHASE && !phaseSolid(b.phaseT);
      if (phased) {
        // faint ghost while intangible
        ctx.fillStyle = `hsla(${b.hue}, 90%, 60%, 0.12)`;
        ctx.fillRect(b.x, b.y, b.w, b.h);
        continue;
      }
      const heat = g.mult.chaosHeat;
      let fill = `hsl(${b.hue}, 75%, ${38 + 8 * heat}%)`;
      let stroke = `hsl(${b.hue}, 95%, 70%)`;

      if (b.kind === KIND.SPEED) {
        const ballFast = g.balls.some(bl => Math.hypot(bl.vx, bl.vy) > SPEED_THRESHOLD);
        const l = ballFast ? 68 : 52;
        fill = `hsl(${b.hue}, 85%, ${l}%)`;
        // shimmer stripes
        ctx.save();
        ctx.beginPath(); ctx.rect(b.x, b.y, b.w, b.h); ctx.clip();
        ctx.strokeStyle = `hsla(${b.hue}, 100%, 85%, ${0.25 + 0.35 * Math.sin(g.time * 6 + b.id)})`;
        ctx.lineWidth = 2;
        for (let sx = b.x - b.h; sx < b.x + b.w; sx += 9) {
          ctx.beginPath();
          ctx.moveTo(sx, b.y + b.h); ctx.lineTo(sx + b.h, b.y);
          ctx.stroke();
        }
        ctx.restore();
      } else if (b.kind === KIND.ANGLE) {
        // Draw the required-angle arc indicator
        ctx.save();
        const ccx = b.x + b.w / 2, ccy = b.y + b.h / 2;
        const R = Math.min(b.w, b.h) * 0.42;
        ctx.strokeStyle = `hsla(${b.hue}, 100%, 75%, 0.9)`;
        ctx.lineWidth = 3;
        const a = b.targetAngle;
        ctx.beginPath();
        ctx.arc(ccx, ccy, R, a - ANGLE_TOLERANCE, a + ANGLE_TOLERANCE);
        ctx.stroke();
        // faint full circle
        ctx.strokeStyle = `hsla(${b.hue}, 100%, 70%, 0.2)`;
        ctx.lineWidth = 1;
        ctx.beginPath(); ctx.arc(ccx, ccy, R, 0, Math.PI * 2); ctx.stroke();
        ctx.restore();
      } else if (b.kind === KIND.PHASE) {
        const solidFrac = phaseSolid(b.phaseT) ? 1 : 0;
        const cyclePos = (b.phaseT % PHASE_PERIOD) / PHASE_PERIOD;
        const flicker = 0.5 + 0.5 * Math.sin(b.phaseT * 3);
        ctx.globalAlpha = solidFrac ? 1 : 0.35;
        fill = `hsla(${b.hue}, 80%, ${40 + 12 * flicker}%, 0.95)`;
        ctx.globalAlpha = 1;
      } else if (b.kind === KIND.MOVING) {
        // motion trail hint
        ctx.fillStyle = `hsla(${b.hue}, 80%, 55%, 0.18)`;
        ctx.fillRect(b.x - MOVE_RANGE * 0.3, b.y, b.w + MOVE_RANGE * 0.6, b.h);
      } else if (b.kind === KIND.EXPLODING) {
        const pulse = 0.5 + 0.5 * Math.sin(g.time * 5 + b.id);
        fill = `hsl(${15 + 10 * pulse}, 95%, ${42 + 14 * pulse}%)`;
        stroke = `hsl(${25 + 10 * pulse}, 100%, 72%)`;
      }

      ctx.shadowColor = stroke;
      ctx.shadowBlur = 8 + 10 * g.mult.chaosHeat;
      ctx.fillStyle = fill;
      ctx.fillRect(b.x, b.y, b.w, b.h);
      ctx.shadowBlur = 0;
      ctx.strokeStyle = stroke;
      ctx.lineWidth = 1.5;
      ctx.strokeRect(b.x + 0.75, b.y + 0.75, b.w - 1.5, b.h - 1.5);
      // top sheen
      ctx.fillStyle = 'rgba(255,255,255,0.16)';
      ctx.fillRect(b.x + 2, b.y + 2, b.w - 4, 3);
    }
  }

  drawCapsules(g, ctx) {
    for (const c of g.capsules) {
      const meta = POWER_META[c.kind];
      const wobble = Math.sin(c.t * 5) * 2;
      ctx.save();
      ctx.translate(c.x, c.y + wobble);
      ctx.shadowColor = `hsl(${meta.hue}, 100%, 65%)`;
      ctx.shadowBlur = 12;
      ctx.fillStyle = `hsla(${meta.hue}, 85%, 55%, 0.9)`;
      roundRect(ctx, -c.w / 2, -c.h / 2, c.w, c.h, 8);
      ctx.fill();
      ctx.shadowBlur = 0;
      ctx.fillStyle = '#06121f';
      ctx.font = 'bold 9px system-ui, sans-serif';
      ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
      ctx.fillText(meta.label, 0, 0.5);
      ctx.restore();
    }
  }

  drawPaddle(g, ctx) {
    const p = g.paddle;
    const heat = g.mult.chaosHeat;
    ctx.save();
    ctx.shadowColor = `hsl(${190 + 80 * heat}, 100%, 65%)`;
    ctx.shadowBlur = 14 + 14 * heat;
    const grad = ctx.createLinearGradient(p.x - p.w / 2, 0, p.x + p.w / 2, 0);
    grad.addColorStop(0, `hsl(${190}, 100%, 60%)`);
    grad.addColorStop(0.5, `hsl(${230 + 60 * heat}, 100%, 68%)`);
    grad.addColorStop(1, `hsl(${300}, 100%, 62%)`);
    ctx.fillStyle = grad;
    roundRect(ctx, p.x - p.w / 2, p.y - PADDLE_H / 2, p.w, PADDLE_H, 7);
    ctx.fill();
    ctx.restore();
    if (p.magnetT > 0) {
      ctx.strokeStyle = `hsla(265, 100%, 75%, ${0.4 + 0.3 * Math.sin(g.time * 8)})`;
      ctx.lineWidth = 2;
      ctx.beginPath();
      ctx.moveTo(p.x - p.w / 2, p.y - PADDLE_H / 2 - 6);
      ctx.lineTo(p.x + p.w / 2, p.y - PADDLE_H / 2 - 6);
      ctx.stroke();
    }
  }

  drawBalls(g, ctx, parts) {
    const heat = g.mult.chaosHeat;
    for (const b of g.balls) {
      const speed = Math.hypot(b.vx, b.vy);
      const hue = g.fireT > 0 ? 22 : g.heavyT > 0 ? 32 : b.hue + 60 * heat;
      const R = b.r * (g.heavyT > 0 ? 1.4 : 1);
      ctx.save();
      ctx.shadowColor = `hsl(${hue}, 100%, 65%)`;
      ctx.shadowBlur = 12 + 16 * heat;
      const grad = ctx.createRadialGradient(b.x, b.y, 0, b.x, b.y, R);
      grad.addColorStop(0, '#fff');
      grad.addColorStop(0.5, `hsl(${hue}, 100%, 75%)`);
      grad.addColorStop(1, `hsla(${hue}, 100%, 55%, 0)`);
      ctx.fillStyle = grad;
      ctx.beginPath();
      ctx.arc(b.x, b.y, R, 0, Math.PI * 2);
      ctx.fill();
      ctx.restore();
    }
  }

  drawParticles(ctx, parts) {
    ctx.globalCompositeOperation = 'lighter';
    for (const p of parts.list) {
      const a = Math.max(0, p.life / p.max);
      if (p.type === 'ring') {
        ctx.strokeStyle = `hsla(${p.hue}, 100%, 70%, ${a * 0.8})`;
        ctx.lineWidth = p.width * a;
        ctx.beginPath();
        ctx.arc(p.x, p.y, p.size, 0, Math.PI * 2);
        ctx.stroke();
      } else if (p.type === 'glow') {
        const g2 = ctx.createRadialGradient(p.x, p.y, 0, p.x, p.y, p.size);
        g2.addColorStop(0, `hsla(${p.hue}, 100%, 70%, ${a * 0.5})`);
        g2.addColorStop(1, 'transparent');
        ctx.fillStyle = g2;
        ctx.beginPath(); ctx.arc(p.x, p.y, p.size, 0, Math.PI * 2); ctx.fill();
      } else if (p.type === 'chunk') {
        ctx.fillStyle = `hsla(${p.hue}, 80%, 60%, ${a})`;
        ctx.fillRect(p.x - p.size / 2, p.y - p.size / 2, p.size, p.size);
      } else {
        ctx.fillStyle = `hsla(${p.hue}, 100%, ${60 + 25 * a}%, ${a})`;
        ctx.beginPath();
        ctx.arc(p.x, p.y, p.size * a + 0.4, 0, Math.PI * 2);
        ctx.fill();
      }
    }
    ctx.globalCompositeOperation = 'source-over';
  }

  drawPopups(ctx, dt, g) {
    ctx.textAlign = 'center';
    for (let i = this.popups.length - 1; i >= 0; i--) {
      const p = this.popups[i];
      p.life -= dt;
      if (p.life <= 0) { this.popups.splice(i, 1); continue; }
      const a = p.life / p.max;
      p.y -= dt * 46;
      ctx.fillStyle = `hsla(${p.hue}, 100%, 75%, ${a})`;
      ctx.font = `bold ${p.big ? 22 : 14}px system-ui, sans-serif`;
      ctx.shadowColor = `hsla(${p.hue}, 100%, 60%, 0.8)`;
      ctx.shadowBlur = 8;
      ctx.fillText(p.text, p.x, p.y);
      ctx.shadowBlur = 0;
    }
  }

  // On-screen thumbstick visual (shown while a touch drag is active).
  drawThumbstick(g, ctx) {
    const ts = g.thumbstick;
    if (!ts || !ts.active) return;
    ctx.save();
    ctx.globalAlpha = 0.4;
    ctx.strokeStyle = '#8ef4ff';
    ctx.lineWidth = 2;
    ctx.beginPath(); ctx.arc(ts.baseX, ts.baseY, 34, 0, Math.PI * 2); ctx.stroke();
    ctx.globalAlpha = 0.6;
    ctx.fillStyle = '#8ef4ff';
    const kx = ts.baseX + ts.dx, ky = ts.baseY + ts.dy;
    ctx.beginPath(); ctx.arc(kx, ky, 16, 0, Math.PI * 2); ctx.fill();
    ctx.restore();
  }

  drawHudText(g, ctx, heat) {
    ctx.textAlign = 'left';
    ctx.textBaseline = 'top';
    ctx.font = 'bold 15px system-ui, sans-serif';
    ctx.fillStyle = 'rgba(230,245,255,0.85)';
    ctx.fillText(`SCORE ${g.score}`, 14, 12);
    ctx.fillText(`♥ ${g.lives}`, 14, 34);
    ctx.fillText(`LVL ${g.levelIndex + 1}`, 14, 56);

    // multiplier — prominent, grows with heat
    const mv = g.mult.value;
    const big = mv >= 2;
    const size = 16 + Math.min(14, mv * 0.8) + heat * 8;
    ctx.textAlign = 'center';
    ctx.font = `800 ${size}px system-ui, sans-serif`;
    if (big) {
      ctx.shadowColor = `hsla(${20 + 40 * heat}, 100%, 60%, 0.9)`;
      ctx.shadowBlur = 10 + 18 * heat;
    }
    ctx.fillStyle = big ? `hsl(${30 + 30 * heat}, 100%, ${65 + 10 * heat}%)` : 'rgba(200,220,240,0.5)';
    ctx.fillText(`×${mv.toFixed(1)}`, g.field.width / 2, 10 + (big ? Math.sin(g.time * 10) * heat * 2 : 0));
    ctx.shadowBlur = 0;

    if (g.mult.combo >= 3) {
      ctx.font = 'bold 13px system-ui, sans-serif';
      ctx.fillStyle = `hsla(${50 + g.mult.combo * 6}, 100%, 70%, 0.95)`;
      ctx.fillText(`COMBO ×${g.mult.combo}`, g.field.width / 2, 40 + size);
    }
    if (g.chaos) {
      ctx.font = '800 15px system-ui, sans-serif';
      ctx.fillStyle = `hsla(${160 + Math.sin(g.time * 8) * 60}, 100%, 70%, 0.95)`;
      ctx.shadowColor = 'hsla(180,100%,60%,0.9)';
      ctx.shadowBlur = 12;
      ctx.fillText('⚡ ABOVE THE BRICKS ⚡', g.field.width / 2, 60 + size);
      ctx.shadowBlur = 0;
    }
  }
}

const PADDLE_H = 14;

function roundRect(ctx, x, y, w, h, r) {
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + w, y, x + w, y + h, r);
  ctx.arcTo(x + w, y + h, x, y + h, r);
  ctx.arcTo(x, y + h, x, y, r);
  ctx.arcTo(x, y, x + w, y, r);
  ctx.closePath();
}
