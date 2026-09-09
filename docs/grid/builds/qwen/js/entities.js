// entities.js — Ball, Paddle, Brick (all types + break rules), PowerUp, Laser,
// and the collision helpers (circle-rect with side detection, ramp deflection,
// paddle bounce + slam, ball-ball elastic).

import {
  FIELD, PADDLE, BALL, BRICK, BRICK_RULES, BRICK_STYLE, POINTS,
  POWERUPS, LASER, PAIR_TINTS
} from './config.js';
import { clamp, lerp, TAU, DEG, roundRectPath } from './utils.js';

// ---------------------------------------------------------------- Paddle

export class Paddle {
  constructor() {
    this.x = FIELD.W / 2;
    this.y = (PADDLE.MIN_Y + PADDLE.MAX_Y) / 2;
    this.w = PADDLE.W;
    this.h = PADDLE.H;
    this.vx = 0;
    this.vy = 0;
    this.squash = 0;         // visual squash (set on bounce)
    this.flash = 0;          // power-up catch flash
    this.prevX = this.x;
    this.prevY = this.y;
  }

  get isWide() { return this.w > PADDLE.W + 4; }

  update(dt, game) {
    const inp = game.input.state;
    const targetW = (game.wideUntil || 0) > game.time ? PADDLE.WIDE_W : PADDLE.W;
    this.w = lerp(this.w, targetW, 1 - Math.exp(-10 * dt));

    this.prevX = this.x;
    this.prevY = this.y;
    this.vx = 0;
    this.vy = 0;

    if (inp.pointerActive && inp.paddleTarget) {
      // Smooth pointer follow (field coords supplied by game)
      const f = inp.paddleTarget;
      const k = 1 - Math.exp(-PADDLE.POINTER_LERP * dt);
      const nx = lerp(this.x, f.x, k);
      const ny = lerp(this.y, f.y, k);
      this.vx = (nx - this.x) / dt;
      this.vy = (ny - this.y) / dt;
      this.x = nx;
      this.y = ny;
    } else {
      // Keyboard / gamepad velocity model
      let ax = 0, ay = 0;
      if (inp.keys.left) ax -= 1;
      if (inp.keys.right) ax += 1;
      if (inp.keys.up) ay -= 1;
      if (inp.keys.down) ay += 1;
      if (inp.gamepadVel) {
        ax = inp.gamepadVel.x;
        ay = inp.gamepadVel.y;
      }
      if (ax !== 0) {
        this.vx += ax * PADDLE.KEY_ACCEL * dt;
        this.vx = clamp(this.vx, -PADDLE.KEY_SPEED_X, PADDLE.KEY_SPEED_X);
      } else {
        this.vx *= Math.exp(-PADDLE.KEY_FRICTION * dt);
        if (Math.abs(this.vx) < 2) this.vx = 0;
      }
      if (ay !== 0) {
        this.vy += ay * PADDLE.KEY_ACCEL * dt;
        this.vy = clamp(this.vy, -PADDLE.KEY_SPEED_Y, PADDLE.KEY_SPEED_Y);
      } else {
        this.vy *= Math.exp(-PADDLE.KEY_FRICTION * dt);
        if (Math.abs(this.vy) < 2) this.vy = 0;
      }
      this.x += this.vx * dt;
      this.y += this.vy * dt;
    }

    this.x = clamp(this.x, this.w / 2, FIELD.W - this.w / 2);
    this.y = clamp(this.y, PADDLE.MIN_Y, PADDLE.MAX_Y);

    this.squash = Math.max(0, this.squash - dt * 6);
    this.flash = Math.max(0, this.flash - dt * 3);
  }

  get rect() {
    return { x: this.x - this.w / 2, y: this.y - this.h / 2, w: this.w, h: this.h };
  }

  draw(ctx, juice) {
    const r = this.rect;
    ctx.save();
    ctx.translate(this.x, this.y);
    // Tilt with upward motion
    const tilt = clamp(this.vy / PADDLE.KEY_SPEED_Y, -1, 1) * -0.08;
    ctx.rotate(tilt);
    const sq = 1 - this.squash * 0.25;
    ctx.scale(1 + this.squash * 0.25, sq);

    const glow = 0.5 + juice * 0.5 + this.flash * 0.8;
    const upGlow = clamp(-this.vy / PADDLE.KEY_SPEED_Y, 0, 1); // rising glow
    ctx.shadowColor = this.flash > 0 ? '#ffffff' : '#57f5ff';
    ctx.shadowBlur = 18 * glow + upGlow * 20;
    ctx.fillStyle = `rgba(120,245,255,${0.35 + 0.4 * glow})`;
    roundRectPath(ctx, -this.w / 2, -this.h / 2, this.w, this.h, PADDLE.R);
    ctx.fill();
    ctx.shadowBlur = 0;
    const g = ctx.createLinearGradient(0, -this.h / 2, 0, this.h / 2);
    g.addColorStop(0, '#bffcff');
    g.addColorStop(0.5, '#3fd9f0');
    g.addColorStop(1, '#1a6f9e');
    ctx.fillStyle = g;
    roundRectPath(ctx, -this.w / 2, -this.h / 2, this.w, this.h, PADDLE.R);
    ctx.fill();
    // Core stripe
    ctx.fillStyle = 'rgba(255,255,255,0.85)';
    roundRectPath(ctx, -this.w / 2 + 10, -3, this.w - 20, 6, 3);
    ctx.fill();
    ctx.restore();
  }
}

// ------------------------------------------------------------------ Ball

export class Ball {
  constructor(x, y, speed, angle = -Math.PI / 2) {
    this.x = x; this.y = y;
    this.vx = Math.cos(angle) * speed;
    this.vy = Math.sin(angle) * speed;
    this.r = BALL.R;
    this.trail = [];           // ring buffer of {x,y}
    this.trailIdx = 0;
    this.held = false;         // stuck to paddle
    this.paddle = null;
    this.heldOffset = 0;
    this.slowUntil = 0;
    this.slowFactor = 1;
  }

  get speed() { return Math.hypot(this.vx, this.vy); }

  setSpeed(s) {
    const m = this.speed || 1;
    const k = s / m;
    this.vx *= k; this.vy *= k;
  }

  // Enforce min |vy| fraction and speed clamp after any interaction.
  enforceConstraints() {
    let s = this.speed;
    s = clamp(s, BALL.MIN_SPEED, BALL.MAX_SPEED);
    this.setSpeed(s);
    const minVy = BALL.MIN_VY_FRACTION * this.speed;
    if (Math.abs(this.vy) < minVy) {
      this.vy = (this.vy < 0 ? -1 : 1) * minVy;
      const vx2 = Math.sqrt(Math.max(0, this.speed * this.speed - this.vy * this.vy));
      this.vx = (this.vx < 0 ? -1 : 1) * vx2 || (Math.random() < 0.5 ? -1 : 1) * vx2;
    }
  }

  // Integrate one physics substep. Returns list of wall hits for FX:
  // [{side:'top'|'left'|'right'|'slam?'}]. Paddle/brick collisions handled by game.
  step(dt, game) {
    if (this.held) return { hits: [] };
    // Magnet bricks + MAGNET power-up steering
    let ax = 0, ay = 0;
    if (game.magnetBricks) {
      for (const m of game.magnetBricks) {
        const dx = m.x - this.x, dy = m.y - this.y;
        const d = Math.hypot(dx, dy);
        if (d < BRICK_RULES.MAGNET_RADIUS && d > 1) {
          const pull = BRICK_RULES.MAGNET_STEER / d;
          ax += dx * pull; ay += dy * pull;
        }
      }
    }
    if (game.magnetPowerUntil > game.time && game.paddle) {
      const dx = game.paddle.x - this.x, dy = game.paddle.y - this.y;
      const d = Math.hypot(dx, dy);
      if (d < POWERUPS.TYPES.MAGNET.radius && d > 1) {
        const pull = 900 / d;
        ax += dx * pull; ay += dy * pull;
      }
    }
    if (ax !== 0 || ay !== 0) {
      this.vx += ax * dt;
      this.vy += ay * dt;
      // preserve speed magnitude (steering only)
      const s = this.speed, target = clamp(s, BALL.MIN_SPEED, BALL.MAX_SPEED);
      this.setSpeed(target);
    }

    const sp = this.speed;
    this.x += this.vx * dt;
    this.y += this.vy * dt;

    const hits = [];
    // Walls
    if (this.x < this.r) { this.x = this.r; this.vx = Math.abs(this.vx); hits.push({ side: 'left', x: 0, y: this.y }); }
    if (this.x > FIELD.W - this.r) { this.x = FIELD.W - this.r; this.vx = -Math.abs(this.vx); hits.push({ side: 'right', x: FIELD.W, y: this.y }); }
    if (this.y < this.r) { this.y = this.r; this.vy = Math.abs(this.vy); hits.push({ side: 'top', x: this.x, y: 0 }); }
    return { hits, speed: sp };
  }

  pushTrail() {
    if (this.trail.length < BALL.TRAIL_LEN) {
      this.trail.push({ x: this.x, y: this.y });
    } else {
      this.trail.shift();
      this.trail.push({ x: this.x, y: this.y });
    }
  }

  draw(ctx, sprite, juice, time) {
    // Trail (thicker in overdrive)
    const n = this.trail.length;
    for (let i = 0; i < n; i++) {
      const p = this.trail[i];
      const t = (i + 1) / n;
      const r = this.r * t * (1 + juice * 0.8);
      ctx.globalAlpha = t * 0.28 * (1 + juice);
      ctx.drawImage(sprite, p.x - r, p.y - r, r * 2, r * 2);
    }
    ctx.globalAlpha = 1;

    // Ball body: pre-rendered glow sprite + white core
    const s = this.r * (5 + juice * 2);
    ctx.drawImage(sprite, this.x - s, this.y - s, s * 2, s * 2);
    const core = ctx.createRadialGradient(this.x - 3, this.y - 4, 1, this.x, this.y, this.r);
    core.addColorStop(0, '#ffffff');
    core.addColorStop(0.7, juice > 0.5 ? '#aef4ff' : '#eaf9ff');
    core.addColorStop(1, juice > 0.5 ? '#37d5ff' : '#7fc9e8');
    ctx.fillStyle = core;
    ctx.beginPath();
    ctx.arc(this.x, this.y, this.r, 0, TAU);
    ctx.fill();
  }
}

// ----------------------------------------------------------------- Brick
// Every brick breaks in exactly one hit (except ramps; locks clink until
// unlocked; angle/speed bricks clink when the ball doesn't satisfy the gate).

export class Brick {
  constructor(col, row, type, opts = {}) {
    this.col = col;
    this.row = row;
    this.type = type;                 // 'std','glass','angle','speed','ramp',
                                      // 'magnet','mirror','mover','bomb','mini',
                                      // 'key','lock'
    this.pair = opts.pair ?? -1;     // key/lock pair index
    this.w = opts.w || BRICK.W;
    this.h = opts.h || BRICK.H;
    this.x = this.w / 2;
    this.y = this.h / 2;
    this.setPos(col, row);
    this.alive = true;
    // movers
    this.moverDir = opts.moverDir || 1;
    this.moverSpeed = opts.moverSpeed || BRICK_RULES.MOVER_SPEED;
    this.moverMinX = opts.moverMinX ?? BRICK.OX + this.w / 2;
    this.moverMaxX = opts.moverMaxX ?? FIELD.W - BRICK.OX - this.w / 2;
    // ramps
    this.rampDir = opts.rampDir || 1; // -1: / (sends ball up-left), 1: \ (up-right)
    this.miniParent = opts.miniParent || null; // bomb that spawned it
  }

  setPos(col, row) {
    this.x = BRICK.OX + col * (BRICK.W + BRICK.GAP) + this.w / 2;
    this.y = BRICK.OY + row * (BRICK.H + BRICK.GAP) + this.h / 2;
  }

  get isBreakable() { return this.type !== 'ramp'; }
  get points() { return POINTS[this.type] ?? 10; }
  get style() { return BRICK_STYLE[this.type] || BRICK_STYLE.std; }

  // Whether this brick is destructible RIGHT NOW (locks depend on key state).
  canBreak(game) {
    if (this.type === 'ramp') return false;
    if (this.type === 'lock' && game && game.lockedPairs.has(this.pair)) return false;
    return true;
  }

  // Gated bricks: does the current ball qualify to break them?
  gatePasses(ball) {
    if (this.type === 'angle') {
      const angFromVert = Math.atan2(Math.abs(ball.vx), Math.abs(ball.vy));
      return angFromVert <= BRICK_RULES.ANGLE_TOLERANCE_DEG * DEG;
    }
    if (this.type === 'speed') {
      return ball.speed >= BRICK_RULES.SPEED_THRESHOLD;
    }
    return true;
  }

  // Can lasers destroy this?
  laserBreakable(game) {
    switch (this.type) {
      case 'std': case 'glass': case 'bomb': case 'mover': case 'mini': return true;
      case 'lock': return !game.lockedPairs.has(this.pair);
      default: return false;
    }
  }

  update(dt) {
    if (this.type === 'mover' && this.alive) {
      this.x += this.moverDir * this.moverSpeed * dt;
      if (this.x < this.moverMinX) { this.x = this.moverMinX; this.moverDir = 1; }
      if (this.x > this.moverMaxX) { this.x = this.moverMaxX; this.moverDir = -1; }
    }
  }

  get rect() { return { x: this.x - this.w / 2, y: this.y - this.h / 2, w: this.w, h: this.h }; }

  draw(ctx, time, game) {
    const r = this.rect;
    const st = this.style;
    const tint = this.pair >= 0 ? PAIR_TINTS[this.pair % PAIR_TINTS.length] : null;
    ctx.save();
    ctx.shadowColor = st.glow;
    ctx.shadowBlur = 8;

    switch (this.type) {
      case 'ramp': {
        // Indestructible one-way 45° slope: draw wedge + diagonal arrow
        ctx.shadowBlur = 4;
        ctx.fillStyle = st.color;
        ctx.beginPath();
        if (this.rampDir > 0) { // '\': high-left, low-right
          ctx.moveTo(r.x, r.y);
          ctx.lineTo(r.x + r.w, r.y + r.h);
          ctx.lineTo(r.x, r.y + r.h);
        } else { // '/': low-left, high-right
          ctx.moveTo(r.x, r.y + r.h);
          ctx.lineTo(r.x + r.w, r.y);
          ctx.lineTo(r.x + r.w, r.y + r.h);
        }
        ctx.closePath();
        ctx.fill();
        ctx.strokeStyle = 'rgba(255,255,255,0.7)';
        ctx.lineWidth = 2;
        ctx.beginPath();
        const cx = r.x + r.w / 2, cy = r.y + r.h / 2;
        const d = this.rampDir;
        ctx.moveTo(cx - 8 * d, cy + 8);
        ctx.lineTo(cx + 8 * d, cy - 8);
        ctx.lineTo(cx + 8 * d - 6 * d, cy - 8 + 5);
        ctx.moveTo(cx + 8 * d, cy - 8);
        ctx.lineTo(cx + 8 * d + 6 * d, cy - 8 + 5);
        ctx.stroke();
        break;
      }
      case 'glass': {
        ctx.fillStyle = st.color;
        roundRectPath(ctx, r.x, r.y, r.w, r.h, 4);
        ctx.fill();
        ctx.strokeStyle = 'rgba(255,255,255,0.8)';
        ctx.lineWidth = 1.5;
        ctx.beginPath();
        ctx.moveTo(r.x + 6, r.y + r.h - 8);
        ctx.lineTo(r.x + r.w - 10, r.y + 6);
        ctx.stroke();
        break;
      }
      case 'angle': {
        ctx.fillStyle = st.color;
        roundRectPath(ctx, r.x, r.y, r.w, r.h, 4);
        ctx.fill();
        // Chevrons (vertical = OK)
        ctx.strokeStyle = 'rgba(255,255,255,0.9)';
        ctx.lineWidth = 2;
        for (let i = -1; i <= 1; i++) {
          ctx.beginPath();
          ctx.moveTo(r.x + 7, r.y + r.h / 2 + i * 8 + 4);
          ctx.lineTo(r.x + r.w / 2, r.y + r.h / 2 + i * 8 - 4);
          ctx.lineTo(r.x + r.w - 7, r.y + r.h / 2 + i * 8 + 4);
          ctx.stroke();
        }
        break;
      }
      case 'speed': {
        ctx.fillStyle = st.color;
        roundRectPath(ctx, r.x, r.y, r.w, r.h, 4);
        ctx.fill();
        // Lightning bolt
        ctx.strokeStyle = '#fff3b0';
        ctx.lineWidth = 2.5;
        ctx.beginPath();
        ctx.moveTo(r.x + r.w * 0.6, r.y + 5);
        ctx.lineTo(r.x + r.w * 0.3, r.y + r.h / 2);
        ctx.lineTo(r.x + r.w * 0.62, r.y + r.h / 2);
        ctx.lineTo(r.x + r.w * 0.4, r.y + r.h - 5);
        ctx.stroke();
        break;
      }
      case 'magnet': {
        ctx.fillStyle = st.color;
        roundRectPath(ctx, r.x, r.y, r.w, r.h, 4);
        ctx.fill();
        // U-magnet glyph
        ctx.strokeStyle = '#fff';
        ctx.lineWidth = 3;
        ctx.beginPath();
        ctx.arc(r.x + r.w / 2, r.y + r.h / 2, 7, Math.PI, 0, false);
        ctx.stroke();
        ctx.fillStyle = '#fff';
        ctx.fillRect(r.x + r.w / 2 - 10, r.y + r.h / 2, 4, 8);
        ctx.fillRect(r.x + r.w / 2 + 6, r.y + r.h / 2, 4, 8);
        break;
      }
      case 'mirror': {
        const g = ctx.createLinearGradient(r.x, r.y, r.x + r.w, r.y + r.h);
        g.addColorStop(0, '#f4f9ff');
        g.addColorStop(0.5, '#9fb2c8');
        g.addColorStop(1, '#e8f1fa');
        ctx.fillStyle = g;
        roundRectPath(ctx, r.x, r.y, r.w, r.h, 4);
        ctx.fill();
        break;
      }
      case 'bomb': {
        ctx.fillStyle = st.color;
        roundRectPath(ctx, r.x, r.y, r.w, r.h, 4);
        ctx.fill();
        // Fuse spark
        ctx.strokeStyle = '#ffd27a';
        ctx.lineWidth = 2;
        ctx.beginPath();
        ctx.moveTo(r.x + r.w / 2, r.y + 6);
        ctx.quadraticCurveTo(r.x + r.w / 2 + 6, r.y + 1, r.x + r.w / 2 + 8, r.y - 2);
        ctx.stroke();
        const blink = 0.5 + 0.5 * Math.sin(time * 8 + this.col);
        ctx.fillStyle = `rgba(255,120,80,${blink})`;
        ctx.beginPath();
        ctx.arc(r.x + r.w / 2 + 8, r.y - 2, 3 + blink * 2, 0, TAU);
        ctx.fill();
        break;
      }
      case 'mini': {
        ctx.fillStyle = st.color;
        roundRectPath(ctx, r.x, r.y, r.w, r.h, 3);
        ctx.fill();
        break;
      }
      case 'key': {
        ctx.fillStyle = tint || st.color;
        roundRectPath(ctx, r.x, r.y, r.w, r.h, 4);
        ctx.fill();
        // Key glyph
        ctx.strokeStyle = '#5a4300';
        ctx.lineWidth = 2.5;
        const kx = r.x + r.w / 2, ky = r.y + r.h / 2;
        ctx.beginPath();
        ctx.arc(kx, ky - 5, 5, 0, TAU);
        ctx.moveTo(kx, ky);
        ctx.lineTo(kx, ky + 12);
        ctx.moveTo(kx, ky + 8);
        ctx.lineTo(kx + 5, ky + 8);
        ctx.stroke();
        break;
      }
      case 'lock': {
        const locked = game && game.lockedPairs.has(this.pair);
        ctx.fillStyle = locked ? st.color : 'rgba(192,138,78,0.35)';
        roundRectPath(ctx, r.x, r.y, r.w, r.h, 4);
        ctx.fill();
        ctx.strokeStyle = locked ? '#ffce9a' : 'rgba(255,206,154,0.4)';
        ctx.lineWidth = 2;
        ctx.beginPath();
        const lx = r.x + r.w / 2, ly = r.y + r.h / 2;
        ctx.arc(lx, ly - 4, 6, Math.PI, 0);
        if (locked) {
          ctx.moveTo(lx, ly - 4);
          ctx.lineTo(lx, ly + 10);
        }
        ctx.stroke();
        break;
      }
      default: { // std
        ctx.fillStyle = st.color;
        roundRectPath(ctx, r.x, r.y, r.w, r.h, 4);
        ctx.fill();
        ctx.fillStyle = 'rgba(255,255,255,0.18)';
        roundRectPath(ctx, r.x + 2, r.y + 2, r.w - 4, r.h / 2 - 2, 3);
        ctx.fill();
      }
    }
    ctx.restore();
  }
}

// ------------------------------------------------------------- PowerUp

export class PowerUp {
  constructor(x, y, type) {
    this.x = x; this.y = y;
    this.type = type;
    this.size = POWERUPS.SIZE;
    this.vy = POWERUPS.FALL_SPEED;
    this.alive = true;
    this.spin = 0;
  }

  update(dt, fieldH) {
    this.y += this.vy * dt;
    this.spin += dt * 3;
    if (this.y > fieldH + 60) this.alive = false;
  }

  draw(ctx, time) {
    const st = POWERUPS.TYPES[this.type];
    if (!st) return;
    const s = this.size / 2;
    ctx.save();
    ctx.translate(this.x, this.y);
    ctx.rotate(Math.sin(this.spin) * 0.15);
    ctx.shadowColor = st.color;
    ctx.shadowBlur = 14;
    ctx.fillStyle = st.color;
    roundRectPath(ctx, -s, -s, this.size, this.size, 10);
    ctx.fill();
    ctx.shadowBlur = 0;
    ctx.fillStyle = 'rgba(10,14,26,0.85)';
    ctx.font = `bold ${this.size * 0.55}px system-ui, sans-serif`;
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    ctx.fillText(st.label, 0, 2);
    ctx.restore();
  }
}

// ---------------------------------------------------------------- Laser

export class Laser {
  constructor(x, y) {
    this.x = x; this.y = y;
    this.w = LASER.W; this.h = LASER.H;
    this.alive = true;
  }

  update(dt) {
    this.y -= LASER.SPEED * dt;
    if (this.y < -this.h) this.alive = false;
  }

  draw(ctx) {
    ctx.save();
    ctx.shadowColor = '#ff4d5e';
    ctx.shadowBlur = 10;
    const g = ctx.createLinearGradient(this.x, this.y, this.x, this.y + this.h);
    g.addColorStop(0, '#ffd0d5');
    g.addColorStop(1, '#ff2038');
    ctx.fillStyle = g;
    roundRectPath(ctx, this.x - this.w / 2, this.y, this.w, this.h, 3);
    ctx.fill();
    ctx.restore();
  }
}

// --------------------------------------------------- Collision helpers

// Circle vs axis-aligned rect. Returns null or {nx, ny, side, px, py}
// where (px,py) is the closest point on the rect to the circle center,
// (nx,ny) is the collision normal, and side is 'top'|'bottom'|'left'|'right'
// (diagonals resolve along the dominant axis).
export function circleRect(cx, cy, r, rect) {
  const px = clamp(cx, rect.x, rect.x + rect.w);
  const py = clamp(cy, rect.y, rect.y + rect.h);
  let dx = cx - px, dy = cy - py;
  let d2 = dx * dx + dy * dy;
  if (d2 > r * r) return null;
  let d = Math.sqrt(d2);
  if (d < 1e-6) {
    // Center inside rect: push out along smallest penetration axis
    const left = cx - rect.x, right = rect.x + rect.w - cx;
    const top = cy - rect.y, bottom = rect.y + rect.h - cy;
    const m = Math.min(left, right, top, bottom);
    if (m === top) { dx = 0; dy = -1; d = 1; }
    else if (m === bottom) { dx = 0; dy = 1; d = 1; }
    else if (m === left) { dx = -1; dy = 0; d = 1; }
    else { dx = 1; dy = 0; d = 1; }
  } else {
    dx /= d; dy /= d;
  }
  const side = Math.abs(dx) > Math.abs(dy) ? (dx > 0 ? 'right' : 'left') : (dy > 0 ? 'bottom' : 'top');
  return { nx: dx, ny: dy, side, px, py };
}

// Bounce a ball off a collision normal. Keeps speed constant.
export function reflectBall(ball, nx, ny) {
  const dot = ball.vx * nx + ball.vy * ny;
  if (dot >= 0) return false;
  ball.vx -= 2 * dot * nx;
  ball.vy -= 2 * dot * ny;
  return true;
}

// Paddle bounce per SPEC: angle from hit offset (max 65° from vertical),
// vx += paddle.vx * 0.35, slam rule, min |vy| 25%, speed clamp [480,1250].
// Returns {slammed, offset01, speed} for FX/sound.
export function paddleBounce(ball, paddle) {
  const r = paddle.rect;
  const hit = circleRect(ball.x, ball.y, ball.r, r);
  if (!hit) return null;

  // Only bounce when the ball is moving downward onto the top area of the paddle
  if (hit.side === 'top' && ball.vy > 0) {
    const offset = clamp((ball.x - paddle.x) / (paddle.w / 2), -1, 1);
    const angle = offset * PADDLE.MAX_BOUNCE_DEG * DEG - Math.PI / 2; // -65°..+65° from vertical
    const speed = clamp(ball.speed, BALL.MIN_SPEED, BALL.MAX_SPEED);
    ball.vx = Math.cos(angle) * speed;
    ball.vy = Math.sin(angle) * speed;
    // Slam: paddle moving up adds up to -260 vy; moving down weakens up to 60
    if (paddle.vy < -1) {
      ball.vy -= PADDLE.SLAM_DVY * clamp(-paddle.vy / PADDLE.SLAM_SPEED_REF, 0, 1);
    } else if (paddle.vy > 1) {
      const up = -ball.vy; // how upward the ball is
      ball.vy += Math.min(PADDLE.DOWN_PENALTY * clamp(paddle.vy / PADDLE.KEY_SPEED_Y, 0, 1), Math.max(0, up));
    }
    ball.vx += paddle.vx * PADDLE.PADDLE_VX_TRANSFER;
    ball.enforceConstraints();
    ball.y = r.y - ball.r;
    return { slammed: paddle.vy < -120, offset01: (offset + 1) / 2, speed: ball.speed, underside: false };
  }
  if (hit.side === 'bottom' && ball.vy < 0) {
    reflectBall(ball, 0, 1);
    ball.y = r.y + r.h + ball.r;
    ball.enforceConstraints();
    return { slammed: false, offset01: 0.5, speed: ball.speed, underside: true };
  }
  if (hit.side === 'left' || hit.side === 'right') {
    reflectBall(ball, hit.nx > 0 ? -1 : 1, 0);
    ball.enforceConstraints();
    return { slammed: false, offset01: 0.5, speed: ball.speed, underside: true };
  }
  return null;
}

// One-way 45° ramp deflection per SPEC: a downward-moving ball hitting the
// upper face is deflected to a 45° upward diagonal, speed preserved;
// otherwise the ball passes through.
export function rampCollide(ball, ramp) {
  const r = ramp.rect;
  if (ball.vy <= 0) return false; // only affects downward balls
  if (ball.x < r.x - ball.r || ball.x > r.x + r.w + ball.r) return false;
  if (ball.y - ball.r > r.y + r.h) return false;
  if (ball.y + ball.r < r.y) return false;
  // The upper face runs from one top corner to the opposite bottom corner.
  // For rampDir > 0 ('\'): face goes from top-left (r.x, r.y) down to
  // bottom-right. For -1 ('/'): from top-right down to bottom-left.
  const t = clamp((ball.x - r.x) / r.w, 0, 1);
  const faceY = ramp.rampDir > 0
    ? r.y + t * r.h                 // '\' descending left→right
    : r.y + (1 - t) * r.h;          // '/' ascending left→right
  if (ball.y + ball.r < faceY) return false; // below the face (already past)
  // Deflect to 45° upward diagonal, speed preserved
  const speed = ball.speed;
  const dx = ramp.rampDir > 0 ? -1 : 1; // '\' sends ball up-left, '/' up-right
  ball.vx = dx * speed * Math.SQRT1_2;
  ball.vy = -speed * Math.SQRT1_2;
  ball.y = faceY - ball.r;
  return true;
}

// Simple elastic ball-ball collision: swap normal velocity components
// (equal mass), keeping each ball's total speed unchanged.
export function ballBall(a, b) {
  const dx = b.x - a.x, dy = b.y - a.y;
  const d = Math.hypot(dx, dy);
  const minD = a.r + b.r;
  if (d >= minD || d < 1e-6) return false;
  const nx = dx / d, ny = dy / d;
  // Separate
  const overlap = (minD - d) / 2;
  a.x -= nx * overlap; a.y -= ny * overlap;
  b.x += nx * overlap; b.y += ny * overlap;
  // Exchange normal components
  const va = a.vx * nx + a.vy * ny;
  const vb = b.vx * nx + b.vy * ny;
  const dvx = (vb - va) * nx, dvy = (vb - va) * ny;
  a.vx += dvx; a.vy += dvy;
  b.vx -= dvx; b.vy -= dvy;
  // Preserve original speeds (equal-mass elastic keeps them anyway, but be safe)
  const sa = Math.hypot(a.vx, a.vy), sb = Math.hypot(b.vx, b.vy);
  if (sa > 1e-6) a.setSpeed(clamp(sa, BALL.MIN_SPEED, BALL.MAX_SPEED));
  if (sb > 1e-6) b.setSpeed(clamp(sb, BALL.MIN_SPEED, BALL.MAX_SPEED));
  return true;
}
