/* entities.js — Paddle, Ball, Brick, Powerup, Laser logic. All vanilla. */
'use strict';

// ---------- Paddle ----------
class Paddle {
  constructor() {
    this.w = 120; this.h = 16; this.x = 0; this.y = 0;
    this.vx = 0; this.vy = 0; this.px = 0; this.py = 0;
    this.wideT = 0; this.laserT = 0; this.stickyT = 0; this.stuckBall = null;
    this.baseW = 120; this.cool = 0; this.trail = [];
  }
  get cx() { return this.x + this.w / 2; }
  update(dt) {
    this.vx = dt > 0 ? (this.x - this.px) / dt : 0;
    this.vy = dt > 0 ? (this.y - this.py) / dt : 0;
    this.px = this.x; this.py = this.y;
    if (this.wideT > 0) { this.wideT -= dt; if (this.wideT <= 0) this.w = this.baseW; }
    if (this.laserT > 0) this.laserT -= dt;
    if (this.stickyT > 0) this.stickyT -= dt; else if (!this.stickyT) this.stuckBall = null;
    if (this.cool > 0) this.cool -= dt;
    this.trail.push({ x: this.cx, y: this.y, age: 0 });
    if (this.trail.length > 14) this.trail.shift();
    for (const t of this.trail) t.age += dt;
  }
}

// ---------- Ball ----------
let BALL_ID = 1;
class Ball {
  constructor(x, y, angle, speed) {
    this.id = BALL_ID++;
    this.x = x; this.y = y; this.r = 8;
    this.dx = Math.cos(angle); this.dy = Math.sin(angle);
    this.speed = speed; this.stuck = true; this.gone = false;
    this.fireT = 0; this.lightT = 0; this.bigT = 0; this.slowT = 0;
    this.spin = Math.random() * 6;
  }
  get vx() { return this.dx * this.speed; }
  get vy() { return this.dy * this.speed; }
  get steepness() { return Math.abs(this.dy); } // 1 = vertical
  get angleDeg() { return Math.atan2(this.dy, this.dx) * 180 / Math.PI; }
  effR() { return this.r * (this.bigT > 0 ? 1.8 : 1); }
  update(dt) {
    this.x += this.dx * this.speed * dt;
    this.y += this.dy * this.speed * dt;
    this.spin += dt * 6;
    if (this.fireT > 0) this.fireT -= dt;
    if (this.lightT > 0) this.lightT -= dt;
    if (this.bigT > 0) this.bigT -= dt;
  }
}

// ---------- Brick ----------
const BRICK_COLORS = ['#00e5ff', '#7dff9a', '#ffe45d', '#ff9d5d', '#ff5d7a', '#b14dff'];
class Brick {
  constructor(col, row, type, x, y, w, h, color) {
    this.col = col; this.row = row; this.type = type; // n G V M H S
    this.x = x; this.y = y; this.w = w; this.h = h;
    this.color = color; this.dead = false; this.flash = 0;
    this.phase = Math.random() * 6; // ghost timer offset
    this.solid = true;
    this.ox = x; this.dir = Math.random() < 0.5 ? 1 : -1; // mover
    this.range = 60 + Math.random() * 60;
    this.vuln = 'below'; // shielded side
    this.hitAnim = 0;
  }
  get cx() { return this.x + this.w / 2; }
  get cy() { return this.y + this.h / 2; }
  update(dt, t, fieldW) {
    this.phase += dt; if (this.flash > 0) this.flash -= dt; if (this.hitAnim > 0) this.hitAnim -= dt;
    if (this.type === 'H') this.solid = (this.phase % 5) < 3; // 3s solid / 2s ghost
    if (this.type === 'M') {
      this.x = this.ox + Math.sin(this.phase * 1.4) * this.range * this.dir;
      this.x = Math.max(4, Math.min(fieldW - this.w - 4, this.x));
    }
  }
  // Returns {destroy:boolean, deny:string|null}
  tryHit(ball) {
    if (this.type === 'H' && !this.solid) return { destroy: false, deny: 'ghost' };
    if (this.type === 'G') {
      // need steep: |dy| > 0.55 (within ~57° of vertical)
      if (Math.abs(ball.dy) < 0.55) return { destroy: false, deny: 'angle' };
    }
    if (this.type === 'V') {
      const need = 620;
      if (ball.speed < need && ball.lightT <= 0) return { destroy: false, deny: 'speed' };
    }
    if (this.type === 'S') {
      // vulnerable from below only: ball must be moving UP (dy<0) i.e. hit bottom face
      if (!(ball.dy < -0.15)) return { destroy: false, deny: 'shield' };
    }
    return { destroy: true, deny: null };
  }
}

// ---------- Powerup ----------
const POWERS = {
  M: { name: 'MULTIBALL', color: '#00e5ff' },
  F: { name: 'FIREBALL', color: '#ff7a00' },
  L: { name: 'LIGHTNING', color: '#ffe45d' },
  B: { name: 'BIG BALL', color: '#7dff9a' },
  S: { name: 'STICKY', color: '#b14dff' },
  Z: { name: 'LASER', color: '#ff5d7a' },
  T: { name: 'SLOW-MO', color: '#7dffff' },
  W: { name: 'WIDE', color: '#5dffc7' },
  E: { name: '+1 LIFE', color: '#ff9dcf' },
};
class Powerup {
  constructor(x, y, kind) {
    this.x = x; this.y = y; this.kind = kind;
    this.vy = 130; this.vx = Math.sin(x) * 30;
    this.w = 46; this.h = 24; this.gone = false; this.bob = Math.random() * 6;
  }
  update(dt) { this.bob += dt; this.y += this.vy * dt; this.x += this.vx * dt + Math.sin(this.bob * 3) * 20 * dt; }
}

// ---------- Laser bolt ----------
class Bolt {
  constructor(x, y) { this.x = x; this.y = y; this.w = 5; this.h = 16; this.vy = -900; this.gone = false; }
  update(dt) { this.y += this.vy * dt; if (this.y < -30) this.gone = true; }
}
