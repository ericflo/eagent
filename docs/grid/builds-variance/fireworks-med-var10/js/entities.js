// SKYBREAK — game entities: Brick, Ball, Paddle, PowerUp, Laser.

// ---------------------------------------------------------------------------
// Brick
// ---------------------------------------------------------------------------

const BRICK = {
  W: 68,
  H: 28,
  GAP: 4,
  TOP: 150,          // top edge of row 0
};

class Brick {
  constructor(kind, col, row) {
    this.kind = kind;
    this.col = col;
    this.row = row;
    this.alive = true;
    this.x = 0; this.y = 0;
    this.w = BRICK.W; this.h = BRICK.H;
    this.vx = 0;
    this.flash = 0;          // 0..1 white flash after a rejected hit
    this.mover = kind === 'M';
    this.movePhase = col * 0.35 + row * 0.7;
    // Angle-locked bricks: each has a required incoming direction band.
    if (kind === 'G') {
      this.angle = rand(0, TAU);       // required direction of ball travel (radians)
      this.band = 0.55;                // +/- radians accepted
    }
    this.colorA = PALETTE[this.kindName][0];
    this.colorB = PALETTE[this.kindName][1];
  }

  get kindName() {
    return { S: 'standard', A: 'armored', G: 'angle', V: 'speed', M: 'mover', B: 'bomb' }[this.kind];
  }

  setHome(offsetX, offsetY) {
    this.homeX = offsetX + this.col * (BRICK.W + BRICK.GAP);
    this.homeY = offsetY + this.row * (BRICK.H + BRICK.GAP);
    this.x = this.homeX; this.y = this.homeY;
  }

  get cx() { return this.x + this.w / 2; }
  get cy() { return this.y + this.h / 2; }

  update(dt, time, moverSpeed, fieldW) {
    if (this.mover) {
      const prevX = this.x;
      const half = fieldW / 2 - this.w / 2 - BRICK.GAP;
      const center = fieldW / 2;
      this.x = clamp(center + Math.sin(time * 0.8 + this.movePhase) * half - this.w / 2,
        BRICK.GAP, fieldW - this.w - BRICK.GAP);
      this.vx = (this.x - prevX) / Math.max(dt, 1e-4);
    } else {
      this.vx = 0;
    }
    if (this.flash > 0) this.flash -= dt * 4;
  }

  // Can a ball with this type/state break me on a normal hit?
  breakableBy(ball, speed) {
    const type = BALL_TYPES[ball.type];
    switch (this.kind) {
      case 'S': case 'M': case 'B':
        return true;
      case 'A':
        return type.breaksArmored;
      case 'V':
        return type.breaksSpeed || speed >= CONFIG.SPEED_BRICK_THRESHOLD;
      case 'G': {
        if (type.burnsThrough) return true;
        const dir = Math.atan2(ball.vy, ball.vx);
        return Math.abs(angleDiff(dir, this.angle)) <= this.band;
      }
      default:
        return true;
    }
  }

  draw(ctx, time) {
    const [ca, cb] = [this.colorA, this.colorB];
    const flash = Math.max(0, this.flash);
    ctx.save();

    // Body with vertical gradient.
    const g = ctx.createLinearGradient(0, this.y, 0, this.y + this.h);
    g.addColorStop(0, ca);
    g.addColorStop(1, cb);
    ctx.fillStyle = g;
    ctx.shadowColor = ca;
    ctx.shadowBlur = this.kind === 'B' ? 14 : 8;
    roundRect(ctx, this.x, this.y, this.w, this.h, 5);
    ctx.fill();
    ctx.shadowBlur = 0;

    // Inner detail line.
    ctx.strokeStyle = 'rgba(0,0,0,0.35)';
    ctx.lineWidth = 1;
    roundRect(ctx, this.x + 3, this.y + 3, this.w - 6, this.h - 6, 3);
    ctx.stroke();

    // Type markers.
    switch (this.kind) {
      case 'A': // rivets
        ctx.fillStyle = 'rgba(255,255,255,0.55)';
        for (const rx of [8, this.w - 8]) {
          ctx.beginPath(); ctx.arc(this.x + rx, this.cy, 2.2, 0, TAU); ctx.fill();
        }
        break;
      case 'G': { // arrow showing required angle
        ctx.translate(this.cx, this.cy);
        ctx.rotate(this.angle);
        ctx.fillStyle = 'rgba(0,0,0,0.75)';
        ctx.beginPath();
        ctx.moveTo(11, 0); ctx.lineTo(-5, -6); ctx.lineTo(-5, 6); ctx.closePath();
        ctx.fill();
        break;
      }
      case 'V': // chevrons
        ctx.strokeStyle = 'rgba(255,255,255,0.8)';
        ctx.lineWidth = 2;
        for (const dx of [-10, 0, 10]) {
          ctx.beginPath();
          ctx.moveTo(this.cx + dx - 4, this.y + 7);
          ctx.lineTo(this.cx + dx + 4, this.cy);
          ctx.lineTo(this.cx + dx - 4, this.y + this.h - 7);
          ctx.stroke();
        }
        break;
      case 'B': // pulsing core
        const pulse = 0.5 + 0.5 * Math.sin(time * 6 + this.col);
        ctx.fillStyle = `rgba(255,255,255,${0.5 + 0.4 * pulse})`;
        ctx.beginPath(); ctx.arc(this.cx, this.cy, 5 + pulse * 2, 0, TAU); ctx.fill();
        break;
      case 'M': // side arrows
        ctx.fillStyle = 'rgba(0,0,0,0.5)';
        for (const s of [-1, 1]) {
          ctx.beginPath();
          ctx.moveTo(this.cx + s * 14, this.cy);
          ctx.lineTo(this.cx + s * 7, this.cy - 5);
          ctx.lineTo(this.cx + s * 7, this.cy + 5);
          ctx.fill();
        }
        break;
    }

    // Rejected-hit flash.
    if (flash > 0) {
      ctx.fillStyle = `rgba(255,80,80,${flash * 0.7})`;
      roundRect(ctx, this.x, this.y, this.w, this.h, 5);
      ctx.fill();
    }
    ctx.restore();
  }
}

// ---------------------------------------------------------------------------
// Ball
// ---------------------------------------------------------------------------

class Ball {
  constructor(x, y, vx, vy, type = 'normal') {
    this.x = x; this.y = y;
    this.vx = vx; this.vy = vy;
    this.type = type;
    this.stuck = false;          // resting on sticky paddle
    this.trail = [];             // [{x,y}]
    this.baseR = CONFIG.BALL_R * BALL_TYPES[type].radiusMul;
    this.r = this.baseR;
    this.speedAtHit = 0;
  }

  get speed() { return Math.hypot(this.vx, this.vy); }

  setSpeed(s) {
    const cur = this.speed || 1;
    this.vx = this.vx / cur * s;
    this.vy = this.vy / cur * s;
  }

  // Anti-horizontal-trap nudge.
  unstickAngles() {
    const sp = this.speed;
    if (sp === 0) return;
    const a = Math.atan2(this.vy, this.vx);
    if (Math.abs(Math.cos(a)) < CONFIG.MIN_VX_RATIO) {
      // Too horizontal: enforce a minimum horizontal component.
      const sign = this.vx >= 0 ? 1 : -1;
      const minVx = sp * CONFIG.MIN_VX_RATIO;
      const vyMag = Math.sqrt(Math.max(0, sp * sp - minVx * minVx));
      this.vx = sign * minVx;
      this.vy = Math.sign(this.vy || 1) * vyMag;
    } else if (Math.abs(Math.sin(a)) < CONFIG.MIN_VX_RATIO) {
      // Too vertical: enforce a minimum vertical component.
      const sign = this.vy >= 0 ? 1 : -1;
      const minVy = sp * CONFIG.MIN_VX_RATIO;
      const vxMag = Math.sqrt(Math.max(0, sp * sp - minVy * minVy));
      this.vy = sign * minVy;
      this.vx = Math.sign(this.vx || 1) * vxMag;
    }
  }

  update(dt) {
    this.x += this.vx * dt;
    this.y += this.vy * dt;
    // Trail (ring buffer style, capped length).
    this.trail.push({ x: this.x, y: this.y });
    if (this.trail.length > CONFIG.MAX_TRAIL) this.trail.shift();
  }

  color() {
    return { normal: '#ffffff', fire: '#ff6b35', heavy: '#ff4d6d', ghost: '#c9d6ff' }[this.type];
  }

  draw(ctx, fastThreshold) {
    const col = this.color();
    const fast = fastThreshold > 0 && this.speed > fastThreshold * CONFIG.FAST_BALL_RATIO;

    // Trail.
    if (this.trail.length > 1) {
      ctx.save();
      ctx.globalCompositeOperation = 'lighter';
      for (let i = 1; i < this.trail.length; i++) {
        const t = i / this.trail.length;
        ctx.globalAlpha = t * 0.5;
        ctx.fillStyle = col;
        ctx.beginPath();
        ctx.arc(this.trail[i].x, this.trail[i].y, this.r * t * 0.9, 0, TAU);
        ctx.fill();
      }
      ctx.restore();
    }

    ctx.save();
    ctx.shadowColor = col;
    ctx.shadowBlur = fast ? 26 : 14;
    if (fast) { // speed-glow halo
      ctx.fillStyle = 'rgba(255,255,180,0.35)';
      ctx.beginPath(); ctx.arc(this.x, this.y, this.r * 1.9, 0, TAU); ctx.fill();
    }
    const g = ctx.createRadialGradient(this.x - this.r * 0.3, this.y - this.r * 0.3, this.r * 0.2, this.x, this.y, this.r);
    g.addColorStop(0, '#ffffff');
    g.addColorStop(1, col);
    ctx.fillStyle = g;
    ctx.beginPath();
    ctx.arc(this.x, this.y, this.r, 0, TAU);
    ctx.fill();
    ctx.restore();
  }
}

// ---------------------------------------------------------------------------
// Paddle
// ---------------------------------------------------------------------------

class Paddle {
  constructor() {
    this.w = CONFIG.PADDLE_W;
    this.h = CONFIG.PADDLE_H;
    this.x = CONFIG.W / 2;
    this.y = CONFIG.PADDLE_BOTTOM_Y;
    this.vx = 0; this.vy = 0;
    this.sticky = false;
    this.expandT = 0;      // seconds of expand remaining
  }

  get left() { return this.x - this.w / 2; }
  get right() { return this.x + this.w / 2; }
  get top() { return this.y - this.h / 2; }
  get bottom() { return this.y + this.h / 2; }

  update(dt, targetX, targetY, laserTime) {
    const oldX = this.x, oldY = this.y;
    const w = this.w;
    this.x = clamp(targetX, w / 2 + 6, CONFIG.W - w / 2 - 6);
    this.y = clamp(targetY, CONFIG.PADDLE_TOP_Y, CONFIG.PADDLE_BOTTOM_Y);
    // Velocities smoothed for good feel.
    this.vx = lerp(this.vx, (this.x - oldX) / Math.max(dt, 1e-4), 0.5);
    this.vy = lerp(this.vy, (this.y - oldY) / Math.max(dt, 1e-4), 0.5);
    if (this.expandT > 0) this.expandT -= dt;
  }

  draw(ctx, time, laserTime) {
    const expanded = this.expandT > 0;
    const col = this.sticky ? '#ffd23f' : laserTime > 0 ? '#ff9de2' : '#35e0ff';
    ctx.save();
    ctx.shadowColor = col;
    ctx.shadowBlur = 16;

    // Motion trail smear when moving fast.
    const speed = Math.hypot(this.vx, this.vy);
    if (speed > 700) {
      ctx.globalAlpha = 0.25;
      ctx.fillStyle = col;
      roundRect(ctx, this.left - this.vx * 0.012, this.top - this.vy * 0.012, this.w, this.h, 12);
      ctx.fill();
      ctx.globalAlpha = 1;
    }

    const g = ctx.createLinearGradient(0, this.top, 0, this.bottom);
    g.addColorStop(0, '#ffffff');
    g.addColorStop(0.35, col);
    g.addColorStop(1, 'rgba(20,40,80,0.9)');
    ctx.fillStyle = g;
    roundRect(ctx, this.left, this.top, this.w, this.h, 12);
    ctx.fill();

    // End caps hinting the up/down ability.
    ctx.fillStyle = 'rgba(255,255,255,0.9)';
    roundRect(ctx, this.left, this.top, 8, this.h, 8);
    ctx.fill();
    roundRect(ctx, this.right - 8, this.top, 8, this.h, 8);
    ctx.fill();
    ctx.restore();
  }
}

// ---------------------------------------------------------------------------
// PowerUp capsule & Laser bolt
// ---------------------------------------------------------------------------

class PowerUp {
  constructor(kind, x, y) {
    this.kind = kind;
    this.def = POWERUPS[kind];
    this.x = x; this.y = y;
    this.vy = CONFIG.DROP_FALL_SPEED;
    this.r = 17;
    this.alive = true;
    this.spin = rand(0, TAU);
  }
  update(dt) {
    this.y += this.vy * dt;
    this.spin += dt * 3;
    if (this.y > CONFIG.H + 40) this.alive = false;
  }
  draw(ctx) {
    const c = this.def.color;
    ctx.save();
    ctx.translate(this.x, this.y);
    ctx.shadowColor = c; ctx.shadowBlur = 14;
    ctx.fillStyle = 'rgba(10,16,32,0.9)';
    ctx.strokeStyle = c; ctx.lineWidth = 2.5;
    roundRect(ctx, -19, -12, 38, 24, 10); ctx.fill(); ctx.stroke();
    ctx.shadowBlur = 0;
    ctx.fillStyle = c;
    ctx.font = '800 11px "Avenir Next", "Segoe UI", system-ui, sans-serif';
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    ctx.fillText(this.def.label, 0, 1);
    ctx.restore();
  }
}

class Laser {
  constructor(x, y) {
    this.x = x; this.y = y;
    this.vy = -1200;
    this.alive = true;
  }
  update(dt) {
    this.y += this.vy * dt;
    if (this.y < -20) this.alive = false;
  }
  draw(ctx) {
    ctx.save();
    ctx.shadowColor = '#ff9de2'; ctx.shadowBlur = 12;
    ctx.strokeStyle = '#ff9de2'; ctx.lineWidth = 3.5;
    ctx.beginPath();
    ctx.moveTo(this.x, this.y); ctx.lineTo(this.x, this.y + 16);
    ctx.stroke();
    ctx.restore();
  }
}

// ---------------------------------------------------------------------------
// Shared drawing helper
// ---------------------------------------------------------------------------

function roundRect(ctx, x, y, w, h, r) {
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + w, y, x + w, y + h, r);
  ctx.arcTo(x + w, y + h, x, y + h, r);
  ctx.arcTo(x, y + h, x, y, r);
  ctx.arcTo(x, y, x + w, y, r);
  ctx.closePath();
}
