// Entity classes: Paddle, Ball, Brick, PowerUp.

import { BALL_R, BALL_BASE_SPEED, BALL_MAX_SPEED, PADDLE_W, PADDLE_H, POWERUP_R } from './config.js';

let nextId = 1;

export class Paddle {
  constructor() {
    this.w = PADDLE_W; this.h = PADDLE_H;
    this.x = 50; this.y = 0; // y set by game (bottom)
    this.vx = 0; this.vy = 0;
    this.sticky = false; this.wideT = 0;
    this.smashCharge = 0; // flash timer for smash feedback
  }
}

export class Ball {
  constructor(x, y, vx, vy) {
    this.id = nextId++;
    this.x = x; this.y = y;
    this.vx = vx; this.vy = vy;
    this.r = BALL_R;
    this.speed = BALL_BASE_SPEED;
    this.type = 'normal'; // normal | fire | pierce | sticky | ghost | magnet
    this.trail = [];
    this.stuckTo = null;  // paddle ref when sticky-caught
    this.stuckOffset = 0;
    this.prevX = x; this.prevY = y;
  }
  setSpeed(s) {
    this.speed = Math.min(BALL_MAX_SPEED, Math.max(BALL_BASE_SPEED * 0.6, s));
    const len = Math.hypot(this.vx, this.vy) || 1;
    this.vx = this.vx / len * this.speed;
    this.vy = this.vy / len * this.speed;
  }
  get color() {
    return {
      normal: '#cfeaff', fire: '#ff9b3d', pierce: '#b4ff6c',
      sticky: '#ff6cc4', ghost: '#9affee', magnet: '#7c8cff'
    }[this.type] || '#cfeaff';
  }
}

// ---- Brick types. Difficulty via ANGLE / SPEED / POSITION rules, never hit counts. ----
// kind: std | shield(top|bottom|left|right) | phase | deflect | mover | bomb | armor | regen
export class Brick {
  constructor(o) {
    Object.assign(this, {
      kind: 'std', x: 0, y: 0, w: 8, h: 4.2, hp: 1,
      color: '#4ea6ff', glow: 0, alive: true,
      moveT: 0, moveAmp: 0, moveAxis: 'x', moveSpeed: 0, baseX: 0, baseY: 0,
      regenT: 0, shieldSide: 'top', phaseSpeed: 95, flash: 0, wobble: Math.random() * 6.28,
    }, o);
    this.baseX = this.x; this.baseY = this.y;
  }
  get cx() { return this.x + this.w / 2; }
  get cy() { return this.y + this.h / 2; }

  hitSide(bx, by, prevX, prevY, r) {
    // Determine which side of the brick the ball struck using previous position.
    const nearLeft = prevX <= this.x - r;
    const nearRight = prevX >= this.x + this.w + r;
    const nearTop = prevY <= this.y - r;
    const nearBot = prevY >= this.y + this.h + r;
    if (nearTop && !nearBot) return 'top';
    if (nearBot && !nearTop) return 'bottom';
    if (nearLeft && !nearRight) return 'left';
    if (nearRight && !nearLeft) return 'right';
    // fallback: closest edge
    const dl = Math.abs(bx - this.x), dr = Math.abs(bx - this.x - this.w);
    const dt = Math.abs(by - this.y), db = Math.abs(by - this.y - this.h);
    return Math.min(dl, dr, dt, db) === dt ? 'top' : Math.min(dl, dr, dt, db) === db ? 'bottom' : Math.min(dl, dr, dt, db) === dl ? 'left' : 'right';
  }

  // Can this ball break this brick right now? Returns {break:bool, blocked:bool, reason}
  canBreak(ball, hitSide) {
    switch (this.kind) {
      case 'std': case 'bomb': case 'mover': case 'deflect': case 'prism':
        return { break: true };
      case 'shield':
        // breakable ONLY from the open side (shield on the opposite). e.g. shieldSide 'top'
        // means a shield sits on 'bottom' -> break from top.
        return { break: hitSide === this.shieldSide, blocked: hitSide !== this.shieldSide, reason: 'shield' };
      case 'phase': {
        const fast = ball.speed >= this.phaseSpeed;
        return { break: fast, blocked: !fast, reason: 'phase' };
      }
      case 'armor':
        return { break: ball.type === 'fire' || ball.type === 'pierce', blocked: ball.type !== 'fire' && ball.type !== 'pierce', reason: 'armor' };
      case 'regen':
        return { break: true };
    }
    return { break: true };
  }
}

export class PowerUp {
  constructor(x, y, kind) {
    this.x = x; this.y = y; this.kind = kind;
    this.r = POWERUP_R; this.vy = 0; this.wobble = Math.random() * 6.28;
  }
  get label() { return PU_META[this.kind]?.label || '?'; }
  get color() { return PU_META[this.kind]?.color || '#fff'; }
  get glyph() { return PU_META[this.kind]?.glyph || '?'; }
}

export const PU_META = {
  multi:   { label: 'MULTI',  color: '#6cf',   glyph: '×3' },
  fire:    { label: 'FIRE',   color: '#ff9b3d', glyph: '🔥' },
  pierce:  { label: 'PIERCE', color: '#b4ff6c', glyph: '➜' },
  sticky:  { label: 'STICKY', color: '#ff6cc4', glyph: '≡' },
  wide:    { label: 'WIDE',   color: '#8fd4ff', glyph: '↔' },
  slow:    { label: 'SLOW',   color: '#c8b4ff', glyph: '◔' },
  life:    { label: '+LIFE',  color: '#ff6c8a', glyph: '♥' },
  speed:   { label: 'SPEED',  color: '#ffd76c', glyph: '»' },
  magnet:  { label: 'MAGNET', color: '#7c8cff', glyph: 'U' },
  ghost:   { label: 'GHOST',  color: '#9affee', glyph: '◌' }, // invention: ball phases THROUGH bricks from any side, immune to deflectors/movers, brief
  bombP:   { label: 'BOMBER', color: '#ffae42', glyph: '✸' }, // invention: next bricks hit explode in a plus-pattern
};