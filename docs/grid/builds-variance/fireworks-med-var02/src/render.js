// All canvas rendering: field, bricks, ball, paddle, capsules, particles, FX.
import { FIELD, PADDLE, BALL, COLORS, POWER_DEFS } from './constants.js';

const BRICK_GLYPH = { ANGLE: '◢', VELOCITY: '⚡', PHASE: '◐', EXPLOSIVE: '✸', MOVER: '⇄', REGEN: '♻', UNBREAK: '✕', ARMOR: '' };

export class Renderer {
  constructor(canvas) {
    this.canvas = canvas;
    this.ctx = canvas.getContext('2d');
    this.scale = { sx: 1, sy: 1, ox: 0, oy: 0, dpr: 1 };
    this.shake = 0; this.shakeX = 0; this.shakeY = 0;
    this.flashWhite = 0;
    this.tint = { a: 0, color: '251,113,133' };
    this.time = 0;
    this.stick = null; // thumbstick visual state {cx,cy,bx,by,active} in css px
  }
  resize() {
    const dpr = Math.min(window.devicePixelRatio || 1, 2.5);
    const w = window.innerWidth, h = window.innerHeight;
    this.canvas.width = Math.round(w * dpr);
    this.canvas.height = Math.round(h * dpr);
    // fit FIELD into viewport preserving aspect (letterbox if needed)
    const scaleFit = Math.min(w / FIELD.w, h / FIELD.h);
    const sx = scaleFit, sy = scaleFit;
    const ox = (w - FIELD.w * sx) / 2, oy = (h - FIELD.h * sy) / 2;
    this.scale = { sx, sy, ox, oy, dpr, cssW: w, cssH: h };
  }
  fieldToCss(x, y) {
    const { sx, sy, ox, oy } = this.scale;
    return { x: x * sx + ox, y: y * sy + oy };
  }
  cssToField(x, y) {
    const { sx, sy, ox, oy } = this.scale;
    return { x: (x - ox) / sx, y: (y - oy) / sy };
  }
  addShake(v) { this.shake = Math.min(26, this.shake + v); }
  addFlash(v) { this.flashWhite = Math.min(1, this.flashWhite + v); }
  addTint(v) { this.tint.a = Math.min(1, this.tint.a + v); }

  beginFrame(dt, mult) {
    this.time += dt;
    this.shake = Math.max(0, this.shake - dt * 30);
    const s = this.shake;
    const reduced = localStorage.getItem('bt_reducedshake') === '1';
    const k = reduced ? 0.25 : 1;
    this.shakeX = (Math.random() * 2 - 1) * s * k;
    this.shakeY = (Math.random() * 2 - 1) * s * k;
    this.flashWhite = Math.max(0, this.flashWhite - dt * 2.6);
    this.tint.a = Math.max(0, this.tint.a - dt * 0.9);
    const { ctx, scale } = this;
    // full-canvas clear in device px
    ctx.setTransform(1, 0, 0, 1, 0, 0);
    ctx.fillStyle = COLORS.bg;
    ctx.fillRect(0, 0, this.canvas.width, this.canvas.height);
    // field transform: logical FIELD units -> device px (fit + center + shake)
    this._fieldTransform(scale.dpr * scale.sx, 0, 0, scale.dpr * scale.sy, scale.dpr * scale.ox + this.shakeX, scale.dpr * scale.oy + this.shakeY);
    ctx.save();
    // subtle bg pulse tied to multiplier
    const pulse = mult > 1 ? (Math.sin(this.time * (4 + mult)) * 0.5 + 0.5) * mult * 0.03 : 0;
    if (mult > 1) {
      const g = ctx.createRadialGradient(FIELD.w / 2, FIELD.h / 2, 100, FIELD.w / 2, FIELD.h / 2, FIELD.w);
      g.addColorStop(0, `rgba(251,113,133,${0.05 + pulse})`);
      g.addColorStop(1, 'rgba(5,6,15,0)');
      ctx.fillStyle = g; ctx.fillRect(0, 0, FIELD.w, FIELD.h);
    }
  }
  _fieldTransform(...args) {
    // allow tests/hooks to read the exact transform
    this._ft = args;
    this.ctx.setTransform(...args);
  }
  fieldTransform() { return this._ft || [1, 0, 0, 1, 0, 0]; }
  endFrame() {
    const { ctx } = this;
    ctx.restore();
    // full-canvas overlays in device px
    ctx.setTransform(1, 0, 0, 1, 0, 0);
    if (this.tint.a > 0.01) {
      ctx.fillStyle = `rgba(${this.tint.color},${this.tint.a * 0.18})`;
      ctx.fillRect(0, 0, this.canvas.width, this.canvas.height);
    }
    if (this.flashWhite > 0.01) {
      ctx.fillStyle = `rgba(255,255,255,${this.flashWhite * 0.85})`;
      ctx.fillRect(0, 0, this.canvas.width, this.canvas.height);
    }
  }
  drawField(walls, topY) {
    const { ctx } = this;
    // side walls + top
    ctx.fillStyle = 'rgba(140,160,220,0.22)';
    ctx.fillRect(0, 0, walls, FIELD.h);
    ctx.fillRect(FIELD.w - walls, 0, walls, FIELD.h);
    ctx.fillRect(0, 0, FIELD.w, topY);
    // frenzy "ceiling" glow line
    const grd = ctx.createLinearGradient(0, topY, 0, topY + 90);
    grd.addColorStop(0, 'rgba(94,234,212,0.20)');
    grd.addColorStop(1, 'rgba(94,234,212,0)');
    ctx.fillStyle = grd; ctx.fillRect(0, topY, FIELD.w, 90);
  }
  drawBricks(field, mult) {
    const { ctx } = this;
    for (const b of field.bricks) {
      if (!b.alive) {
        if (b.type === 'REGEN' && b.regenWaiting) this._drawRegenGhost(b);
        continue;
      }
      if (b.type === 'PHASE' && !b.phaseSolid) { this._drawPhaseGhost(b); continue; }
      const c = COLORS.brick[b.type] || COLORS.brick.STD;
      let x = b.x, y = b.y;
      if (b.shake > 0) { x += (Math.random() - .5) * b.shake * 3; y += (Math.random() - .5) * b.shake * 3; b.shake = Math.max(0, b.shake - 0.03); }
      ctx.save();
      // glow scales with frenzy for hazard bricks
      if (mult > 2) { ctx.shadowColor = c; ctx.shadowBlur = Math.min(18, mult * 2); }
      ctx.fillStyle = c;
      this._roundRect(x, y, b.w, b.h, 5); ctx.fill();
      ctx.shadowBlur = 0;
      // face detail
      ctx.fillStyle = 'rgba(255,255,255,0.22)';
      this._roundRect(x + 3, y + 3, b.w - 6, b.h * 0.32, 3); ctx.fill();
      ctx.fillStyle = 'rgba(0,0,0,0.22)';
      this._roundRect(x + 3, y + b.h - b.h * 0.28, b.w - 6, b.h * 0.2, 3); ctx.fill();
      // glyph
      const g = BRICK_GLYPH[b.type];
      if (g && b.type !== 'ARMOR') {
        ctx.fillStyle = 'rgba(5,6,15,0.75)';
        ctx.font = 'bold 14px system-ui, sans-serif';
        ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
        ctx.fillText(g, x + b.w / 2, y + b.h / 2 + 1);
      }
      // armor cracks
      if (b.type === 'ARMOR') {
        ctx.strokeStyle = 'rgba(10,12,20,0.8)'; ctx.lineWidth = 2;
        const dmg = 3 - b.hp;
        for (let i = 0; i < dmg; i++) {
          ctx.beginPath();
          const sx = x + 12 + i * 22;
          ctx.moveTo(sx, y + 4); ctx.lineTo(sx + 8, y + b.h / 2); ctx.lineTo(sx + 2, y + b.h - 4);
          ctx.stroke();
        }
      }
      // angle-lock sheen hint (diagonal arrows)
      if (b.type === 'ANGLE') {
        ctx.strokeStyle = 'rgba(5,6,15,0.5)'; ctx.lineWidth = 2.5;
        ctx.beginPath();
        ctx.moveTo(x + 8, y + b.h - 6); ctx.lineTo(x + b.w - 14, y + 7);
        ctx.stroke();
        ctx.beginPath();
        const ax = x + b.w - 16, ay = y + 9;
        ctx.moveTo(ax, ay); ctx.lineTo(ax - 7, ay + 1); ctx.moveTo(ax, ay); ctx.lineTo(ax + 1, ay + 7);
        ctx.stroke();
      }
      // velocity cracks/hazard ticks
      if (b.type === 'VELOCITY') {
        ctx.strokeStyle = 'rgba(5,6,15,0.6)'; ctx.lineWidth = 1.6;
        for (let i = 0; i < 3; i++) {
          const sx = x + 14 + i * 22;
          ctx.beginPath(); ctx.moveTo(sx, y + b.h - 5); ctx.lineTo(sx + 6, y + 5); ctx.stroke();
        }
      }
      // explosive core
      if (b.type === 'EXPLOSIVE') {
        ctx.fillStyle = 'rgba(5,6,15,0.7)';
        ctx.beginPath(); ctx.arc(x + b.w / 2, y + b.h / 2, 8 + Math.sin(this.time * 7) * 1.5, 0, 7); ctx.fill();
        ctx.fillStyle = '#ffd9d0';
        ctx.beginPath(); ctx.arc(x + b.w / 2, y + b.h / 2, 3.4, 0, 7); ctx.fill();
      }
      // flash overlay
      if (b.flash > 0) {
        ctx.fillStyle = `rgba(255,255,255,${b.flash * 0.9})`;
        this._roundRect(x, y, b.w, b.h, 5); ctx.fill();
      }
      ctx.restore();
    }
  }
  _roundRect(x, y, w, h, r) {
    const ctx = this.ctx;
    ctx.beginPath();
    ctx.moveTo(x + r, y);
    ctx.arcTo(x + w, y, x + w, y + h, r);
    ctx.arcTo(x + w, y + h, x, y + h, r);
    ctx.arcTo(x, y + h, x, y, r);
    ctx.arcTo(x, y, x + w, y, r);
    ctx.closePath();
  }
  _drawPhaseGhost(b) {
    const ctx = this.ctx;
    const t = (Math.sin(this.time * 10) + 1) / 2;
    ctx.strokeStyle = `rgba(192,132,252,${0.25 + t * 0.3})`;
    ctx.lineWidth = 2;
    ctx.setLineDash([6, 5]);
    this._roundRect(b.x + 1, b.y + 1, b.w - 2, b.h - 2, 5); ctx.stroke();
    ctx.setLineDash([]);
  }
  _drawRegenGhost(b) {
    const ctx = this.ctx;
    const t = 1 - Math.min(1, b.regenTimer / 6);
    ctx.strokeStyle = `rgba(52,211,153,${0.15 + t * 0.5})`;
    ctx.lineWidth = 2;
    this._roundRect(b.x + 1, b.y + 1, b.w - 2, b.h - 2, 5); ctx.stroke();
    ctx.fillStyle = `rgba(52,211,153,${t * 0.25})`;
    this._roundRect(b.x + 1, b.y + 1, (b.w - 2) * t, b.h - 2, 5); ctx.fill();
  }
  drawBall(ball, power, mult) {
    const ctx = this.ctx;
    const def = power ? POWER_DEFS[power] : null;
    // trail
    ctx.save();
    for (let i = 0; i < ball.trail.length; i++) {
      const t = ball.trail[i];
      const a = (i / ball.trail.length) * 0.5;
      ctx.fillStyle = def ? def.color : (mult > 1 ? '#fbbf24' : '#e8f0ff');
      ctx.globalAlpha = a * (mult > 1 ? 1.4 : 1);
      ctx.beginPath(); ctx.arc(t.x, t.y, ball.r * (0.3 + 0.6 * i / ball.trail.length), 0, 7); ctx.fill();
    }
    ctx.globalAlpha = 1;
    // aura for ball powers
    if (def) {
      ctx.shadowColor = def.color; ctx.shadowBlur = 22;
      ctx.fillStyle = def.color + '55';
      ctx.beginPath(); ctx.arc(ball.x, ball.y, ball.r * 2, 0, 7); ctx.fill();
    }
    const sq = ball.squash;
    ctx.translate(ball.x, ball.y); ctx.rotate(ball.squashAng);
    ctx.scale(1 + sq, 1 - sq);
    ctx.rotate(-ball.squashAng); ctx.translate(-ball.x, -ball.y);
    const base = def ? def.color : (mult > 1 ? '#ffe9a8' : '#ffffff');
    ctx.fillStyle = base;
    ctx.shadowColor = base; ctx.shadowBlur = def ? 18 : 10;
    ctx.beginPath(); ctx.arc(ball.x, ball.y, ball.r, 0, 7); ctx.fill();
    ctx.shadowBlur = 0;
    if (power === 'HEAVY') {
      ctx.strokeStyle = '#5b5b5b'; ctx.lineWidth = 3;
      ctx.beginPath(); ctx.arc(ball.x, ball.y, ball.r * 1.5, this.time * 6, this.time * 6 + 4); ctx.stroke();
    }
    if (power === 'GHOST') {
      ctx.strokeStyle = '#a5b4fc88'; ctx.lineWidth = 2;
      ctx.beginPath(); ctx.arc(ball.x, ball.y, ball.r * 1.8, 0, 7); ctx.stroke();
    }
    ctx.restore();
  }
  drawBalls(balls, power, mult) {
    for (const b of balls) this.drawBall(b, power, mult);
  }
  drawPaddle(p, sticky) {
    const ctx = this.ctx;
    const sq = p.squash;
    ctx.save();
    ctx.translate(p.x, p.y);
    ctx.scale(1 + sq * 0.3, 1 - sq * 0.35);
    // sticky indicator
    if (sticky) {
      ctx.strokeStyle = '#a3e635'; ctx.lineWidth = 3;
      ctx.shadowColor = '#a3e635'; ctx.shadowBlur = 10;
      this._roundRect(-p.w / 2 - 4, -p.h / 2 - 4, p.w + 8, p.h + 8, 10); ctx.stroke();
      ctx.shadowBlur = 0;
    }
    const g = ctx.createLinearGradient(0, -p.h / 2, 0, p.h / 2);
    g.addColorStop(0, '#9be8ff'); g.addColorStop(0.5, '#38bdf8'); g.addColorStop(1, '#0369a1');
    ctx.fillStyle = g;
    ctx.shadowColor = '#38bdf8'; ctx.shadowBlur = 12;
    this._roundRect(-p.w / 2, -p.h / 2, p.w, p.h, p.h / 2); ctx.fill();
    ctx.shadowBlur = 0;
    ctx.fillStyle = 'rgba(255,255,255,0.5)';
    this._roundRect(-p.w / 2 + 5, -p.h / 2 + 3, p.w - 10, 4, 2); ctx.fill();
    // center notch (aim hint)
    ctx.fillStyle = 'rgba(255,255,255,0.85)';
    ctx.fillRect(-2, -p.h / 2, 4, p.h);
    ctx.restore();
  }
  drawCapsules(capsules) {
    const ctx = this.ctx;
    for (const c of capsules) {
      const def = c.def;
      ctx.save();
      ctx.translate(c.x, c.y);
      const wob = Math.sin(c.t * 6) * 0.12;
      ctx.rotate(wob);
      ctx.shadowColor = def.color; ctx.shadowBlur = 14;
      ctx.fillStyle = def.color;
      this._roundRect(-c.w / 2, -c.h / 2, c.w, c.h, 7); ctx.fill();
      ctx.shadowBlur = 0;
      ctx.fillStyle = '#05060f';
      ctx.font = 'bold 10px system-ui, sans-serif';
      ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
      ctx.fillText(def.label.slice(0, 7), 0, 1);
      // sparkle
      if (Math.random() < 0.3) {
        ctx.fillStyle = '#ffffffaa';
        ctx.fillRect((Math.random() - .5) * c.w, (Math.random() - .5) * c.h, 2, 2);
      }
      ctx.restore();
    }
  }
  drawStickVisual() {
    if (!this.stick || !this.stick.on || !this.stick.active) return;
    const ctx = this.ctx;
    // stick coords are in CSS px; overlay in device px
    const dpr = this.scale.dpr;
    const bx = this.stick.bx, by = this.stick.by;
    ctx.save();
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.strokeStyle = 'rgba(94,234,212,0.5)'; ctx.lineWidth = 2;
    ctx.beginPath(); ctx.arc(bx, by, 55, 0, 7); ctx.stroke();
    const dx = (this.stick.cx - bx), dy = (this.stick.cy - by);
    const d = Math.hypot(dx, dy) || 1;
    const cl = Math.min(d, 55) / d;
    ctx.fillStyle = 'rgba(94,234,212,0.7)';
    ctx.beginPath(); ctx.arc(bx + dx * cl, by + dy * cl, 22, 0, 7); ctx.fill();
    ctx.restore();
  }
}
