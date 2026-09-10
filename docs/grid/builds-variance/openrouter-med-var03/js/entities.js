/* ============================================================
   Overdrive Breakout — entities.js
   Brick + Ball classes. Pure state + draw; physics handled in
   game.js. Logical field is 1080 x 1920 (portrait).
   ============================================================ */
'use strict';

const FIELD_W = 1080;
const FIELD_H = 1920;

// Brick geometry
const COLS = 30;
const BRICK_W = FIELD_W / COLS;          // 36
const BRICK_H = 64;

// Brick kinds
const B_EMPTY = 0, B_STD = 1, B_ANGLE_STEEP = 2, B_ANGLE_SHALLOW = 3,
      B_SPEED = 4, B_PHASE = 5, B_REFLECT = 6, B_PAYDIRT = 7, B_STEEL = 8;

class Brick {
  constructor(kind, col, row) {
    this.kind = kind;
    this.col = col; this.row = row;
    this.x = col * BRICK_W;
    this.y = FIELD_H * 0.16 + row * BRICK_H;
    this.w = BRICK_W; this.h = BRICK_H;
    this.alive = true;
    this.phaseT = Math.random() * 6.28;       // phase pulse cycle
    this.solidNow = true;                      // for PHASE bricks
    this.wobble = 0;                           // thud wobble anim
    this.crackSeed = 0;                        // reflect ripple timer
    this.flashT = 0;                           // wrong-angle spark timer
    this.glow = 0;
    // per-kind tuning
    switch (kind) {
      case B_ANGLE_STEEP:  this.req = 'steep';  break;
      case B_ANGLE_SHALLOW: this.req = 'shallow'; break;
      case B_SPEED:        this.reqSpeed = 7.2; break;
      case B_PHASE:        this.solidNow = false; this.period = 2.4; break;
      case B_PAYDIRT:      this.reqSpeed = 4.6; break;
    }
  }

  // is this brick solid for a ball of the given type/state?
  isSolid(ballType) {
    switch (this.kind) {
      case B_PHASE: return this.solidNow;
      default: return true;
    }
  }

  // does the ball (type, speed, approach angle) destroy it?
  // angleDeg: |angle of velocity from vertical|, 0 = straight down/up
  canBreak(ballSpeed, angleDeg, ballType) {
    switch (this.kind) {
      case B_STD:
      case B_PAYDIRT:
        return true;
      case B_ANGLE_STEEP:  return ballType === 'heavy' || angleDeg < 30;
      case B_ANGLE_SHALLOW: return ballType === 'heavy' || angleDeg > 55;
      case B_SPEED:        return ballType === 'heavy' || ballSpeed >= this.reqSpeed;
      case B_REFLECT:      return ballType === 'fire' || ballType === 'plasma' || ballType === 'heavy';
      case B_PHASE:        return this.solidNow; // timing
      case B_STEEL:        return false;
    }
    return true;
  }

  score() {
    switch (this.kind) {
      case B_STD: return 50;
      case B_ANGLE_STEEP: case B_ANGLE_SHALLOW: return 150;
      case B_SPEED: return 150;
      case B_PHASE: return 120;
      case B_REFLECT: return 300;
      case B_PAYDIRT: return 400;
      default: return 50;
    }
  }

  color() {
    switch (this.kind) {
      case B_STD: return '#37b6ff';
      case B_ANGLE_STEEP: case B_ANGLE_SHALLOW: return '#ffb020';
      case B_SPEED: return '#ff4d6d';
      case B_PHASE: return this.solidNow ? '#b06cff' : '#4a3a6e';
      case B_REFLECT: return '#8fa3b8';
      case B_PAYDIRT: return '#ffd23f';
      case B_STEEL: return '#5a6572';
    }
    return '#888';
  }

  update(dt) {
    if (this.kind === B_PHASE) {
      this.phaseT += dt;
      const cyc = (this.phaseT % this.period) / this.period;
      const was = this.solidNow;
      this.solidNow = cyc < 0.62;                 // 62% solid, 38% ghost
      if (this.solidNow !== was) this.flashT = 0.12;
    }
    if (this.wobble > 0) this.wobble -= dt;
    if (this.crackSeed > 0) this.crackSeed -= dt;
    if (this.flashT > 0) this.flashT -= dt;
    if (this.glow > 0) this.glow -= dt * 2;
  }

  draw(ctx, t) {
    if (!this.alive) return;
    let cx = this.x + this.w / 2, cy = this.y + this.h / 2;
    let w = this.w - 3, h = this.h - 6;
    ctx.save();
    // wobble (speed-brick slow thud) — offset horizontally with sine
    if (this.wobble > 0) {
      const wo = Math.sin(this.wobble * 40) * this.wobble * 14;
      ctx.translate(wo, 0);
    }
    const col = this.color();
    // body
    ctx.fillStyle = col;
    if (this.kind === B_PHASE) ctx.globalAlpha = this.solidNow ? 0.95 : 0.28;
    ctx.shadowColor = col; ctx.shadowBlur = this.glow > 0 ? 18 : 0;
    if (this.kind === B_STEEL) {
      ctx.fillStyle = '#39424e';
      ctx.fillRect(cx - w / 2, cy - h / 2, w, h);
      ctx.strokeStyle = '#7c8a99'; ctx.lineWidth = 2;
      ctx.strokeRect(cx - w / 2, cy - h / 2, w, h);
      // rivets
      ctx.fillStyle = '#8b98a6';
      ctx.fillRect(cx - w / 2 + 4, cy - h / 2 + 4, 4, 4);
      ctx.fillRect(cx + w / 2 - 8, cy + h / 2 - 8, 4, 4);
    } else {
      // rounded rect body
      const r = 6;
      ctx.beginPath();
      ctx.moveTo(cx - w / 2 + r, cy - h / 2);
      ctx.arcTo(cx + w / 2, cy - h / 2, cx + w / 2, cy + h / 2, r);
      ctx.arcTo(cx + w / 2, cy + h / 2, cx - w / 2, cy + h / 2, r);
      ctx.arcTo(cx - w / 2, cy + h / 2, cx - w / 2, cy - h / 2, r);
      ctx.arcTo(cx - w / 2, cy - h / 2, cx + w / 2, cy - h / 2, r);
      ctx.closePath();
      ctx.fill();
      ctx.strokeStyle = 'rgba(255,255,255,0.5)'; ctx.lineWidth = 2;
      ctx.stroke();
    }
    ctx.shadowBlur = 0;
    ctx.globalAlpha = 1;

    // ---- glyph markings per kind (distinct silhouettes) ----
    ctx.strokeStyle = 'rgba(0,0,0,0.55)'; ctx.lineWidth = 2.5;
    if (this.kind === B_ANGLE_STEEP) {
      // down chevron (steep approach)
      ctx.beginPath();
      ctx.moveTo(cx - 8, cy - 10); ctx.lineTo(cx, cy + 6); ctx.lineTo(cx + 8, cy - 10);
      ctx.stroke();
    } else if (this.kind === B_ANGLE_SHALLOW) {
      // horizontal double arrows (shallow approach)
      ctx.beginPath();
      ctx.moveTo(cx - 10, cy); ctx.lineTo(cx + 10, cy);
      ctx.moveTo(cx + 4, cy - 6); ctx.lineTo(cx + 10, cy); ctx.lineTo(cx + 4, cy + 6);
      ctx.moveTo(cx - 4, cy - 6); ctx.lineTo(cx - 10, cy); ctx.lineTo(cx - 4, cy + 6);
      ctx.stroke();
    } else if (this.kind === B_SPEED) {
      // speed lines
      ctx.beginPath();
      ctx.moveTo(cx - 12, cy - 6); ctx.lineTo(cx + 12, cy - 6);
      ctx.moveTo(cx - 12, cy + 1); ctx.lineTo(cx + 12, cy + 1);
      ctx.moveTo(cx - 6, cy + 8); ctx.lineTo(cx + 12, cy + 8);
      ctx.stroke();
    } else if (this.kind === B_REFLECT) {
      // armor plate diagonal + shine
      ctx.strokeStyle = 'rgba(255,255,255,0.35)'; ctx.lineWidth = 3;
      ctx.beginPath();
      ctx.moveTo(cx - w / 2 + 5, cy + h / 2 - 5); ctx.lineTo(cx + w / 2 - 5, cy - h / 2 + 5);
      ctx.stroke();
      // crack ripple when hit by normal ball
      if (this.crackSeed > 0) {
        ctx.strokeStyle = 'rgba(255,255,255,0.7)'; ctx.lineWidth = 1.5;
        ctx.beginPath();
        ctx.moveTo(cx - 8, cy - 12); ctx.lineTo(cx - 2, cy); ctx.lineTo(cx - 9, cy + 10);
        ctx.moveTo(cx - 2, cy); ctx.lineTo(cx + 10, cy + 3);
        ctx.stroke();
      }
    } else if (this.kind === B_PAYDIRT) {
      // starburst
      ctx.fillStyle = 'rgba(0,0,0,0.5)';
      ctx.font = '700 26px system-ui, sans-serif';
      ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
      ctx.fillText('★', cx, cy + 1);
    } else if (this.kind === B_PHASE) {
      ctx.strokeStyle = this.solidNow ? 'rgba(0,0,0,0.5)' : 'rgba(180,140,255,0.8)';
      ctx.lineWidth = 2;
      ctx.beginPath(); ctx.arc(cx, cy, 9, 0, 6.283); ctx.stroke();
      ctx.beginPath(); ctx.arc(cx, cy, 3, 0, 6.283); ctx.fillStyle = ctx.strokeStyle; ctx.fill();
    } else if (this.kind === B_STD) {
      // subtle inner line
      ctx.strokeStyle = 'rgba(0,0,0,0.25)'; ctx.lineWidth = 1.5;
      ctx.beginPath(); ctx.moveTo(cx - w / 2 + 6, cy - 8); ctx.lineTo(cx + w / 2 - 6, cy - 8); ctx.stroke();
    }
    // wrong-angle spark flash overlay
    if (this.flashT > 0 && (this.kind === B_ANGLE_STEEP || this.kind === B_ANGLE_SHALLOW)) {
      ctx.globalAlpha = this.flashT / 0.12;
      ctx.fillStyle = '#fff';
      ctx.fillRect(cx - w / 2, cy - h / 2, w, h);
      ctx.globalAlpha = 1;
    }
    ctx.restore();
  }
}

// ---------------------------------------------------------------------------
class Ball {
  constructor(x, y, vx, vy, type) {
    this.x = x; this.y = y;
    this.vx = vx; this.vy = vy;
    this.r = 16;
    this.type = type || 'std';       // std | fire | plasma | heavy
    this.timer = 0;                  // power-up time remaining
    this.stuck = false;              // magnet hold
    this.stuckDx = 0; this.stuckDy = 0;
    this.straightT = 0;              // time spent near-axis (anti-stuck)
    this.baseSpeed = 9.0;            // px per substep at 120Hz
    this.smashBoost = 0;             // extra speed from paddle smash (decays)
    this.trail = [];
  }

  speed() { return Math.hypot(this.vx, this.vy); }
  setSpeed(s) {
    const c = this.speed() || 1;
    this.vx = this.vx / c * s; this.vy = this.vy / c * s;
  }

  updateTimer(dt) {
    if (this.type !== 'std' && this.timer > 0) {
      this.timer -= dt;
      if (this.timer <= 0) { this.type = 'std'; this.timer = 0; }
    }
  }

  color() {
    switch (this.type) {
      case 'fire': return '#ff7a1a';
      case 'plasma': return '#7df9ff';
      case 'heavy': return '#ff4444';
      default: return '#ffffff';
    }
  }

  radius() { return this.type === 'heavy' ? this.r + 5 : this.r; }

  draw(ctx) {
    const r = this.radius();
    const c = this.color();
    ctx.globalCompositeOperation = 'lighter';
    // outer glow
    const g = ctx.createRadialGradient(this.x, this.y, r * 0.2, this.x, this.y, r * 2.6);
    g.addColorStop(0, c);
    g.addColorStop(1, 'rgba(0,0,0,0)');
    ctx.globalAlpha = 0.5;
    ctx.fillStyle = g;
    ctx.beginPath(); ctx.arc(this.x, this.y, r * 2.6, 0, 6.283); ctx.fill();
    ctx.globalAlpha = 1;
    // core
    ctx.fillStyle = c;
    ctx.beginPath(); ctx.arc(this.x, this.y, r, 0, 6.283); ctx.fill();
    ctx.fillStyle = '#fff';
    ctx.beginPath(); ctx.arc(this.x - r * 0.3, this.y - r * 0.3, r * 0.45, 0, 6.283); ctx.fill();
    ctx.globalCompositeOperation = 'source-over';
  }
}

window.Brick = Brick;
window.Ball = Ball;
window.COLS = COLS; window.BRICK_W = BRICK_W; window.BRICK_H = BRICK_H;
window.FIELD_W = FIELD_W; window.FIELD_H = FIELD_H;
window.B_EMPTY = B_EMPTY; window.B_STD = B_STD; window.B_ANGLE_STEEP = B_ANGLE_STEEP;
window.B_ANGLE_SHALLOW = B_ANGLE_SHALLOW; window.B_SPEED = B_SPEED; window.B_PHASE = B_PHASE;
window.B_REFLECT = B_REFLECT; window.B_PAYDIRT = B_PAYDIRT; window.B_STEEL = B_STEEL;