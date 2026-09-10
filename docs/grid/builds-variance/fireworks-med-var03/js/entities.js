'use strict';
// ---------------------------------------------------------------------------
// entities.js — Ball, Paddle, PowerUp, Laser.
// Ball carries the paddle-lift mechanic (upward paddle motion at impact
// grants a speed boost + upward bias — the path to the top).
// ---------------------------------------------------------------------------

const BALL_BASE_SPEED = 340;
const BALL_SPEED_CAP = 720;
const BALL_RADIUS = 7;
const SPEED_PER_LOOP = 8;

class Ball {
  constructor(x, y, vx, vy) {
    this.reset(x, y, vx, vy);
    // launched balls (given nonzero velocity) start free
    if (vx || vy) this.stuck = false;
  }

  reset(x, y, vx, vy) {
    this.x = x; this.y = y;
    this.vx = vx || 0; this.vy = vy || 0;
    this.r = BALL_RADIUS;
    this.stuck = true;           // riding the paddle pre-launch
    this.stickOff = 0;           // offset from paddle center while stuck
    this.fire = 0;               // seconds of pierce remaining
    this.trail = [];             // recent positions for afterimages
    this.aboveTime = 0;          // continuous seconds spent above the zone
    this.onTop = false;
  }

  get speed() { return Math.hypot(this.vx, this.vy); }

  setSpeed(sp) {
    const s = this.speed || 1;
    this.vx = this.vx / s * sp;
    this.vy = this.vy / s * sp;
  }

  launch() {
    if (!this.stuck) return;
    this.stuck = false;
    // default serve: slight random angle upward
    const a = -Math.PI / 2 + (Math.random() - 0.5) * 0.5;
    const sp = Math.max(this.speed, BALL_BASE_SPEED);
    this.vx = Math.cos(a) * sp;
    this.vy = Math.sin(a) * sp;
  }

  update(dt, paddle) {
    if (this.stuck) {
      this.x = paddle.x + this.stickOff;
      this.y = paddle.y - paddle.h / 2 - this.r - 1;
      return;
    }
    this.x += this.vx * dt;
    this.y += this.vy * dt;
    if (this.fire > 0) this.fire = Math.max(0, this.fire - dt);
    // trail afterimages (cap 8)
    this.trail.push(this.x, this.y);
    if (this.trail.length > 16) this.trail.splice(0, 2);
  }

  draw(ctx, time, overdrive) {
    // trail
    const n = this.trail.length / 2;
    for (let i = 0; i < n; i++) {
      const a = (i / n) * (this.fire > 0 ? 0.55 : overdrive ? 0.5 : 0.28);
      ctx.globalAlpha = a;
      ctx.fillStyle = this.fire > 0 ? '#ff8a2a' : '#7ef3ff';
      const sz = this.r * (0.35 + 0.6 * i / n);
      ctx.beginPath();
      ctx.arc(this.trail[i * 2], this.trail[i * 2 + 1], sz, 0, TAU);
      ctx.fill();
    }
    ctx.globalAlpha = 1;
    // core
    ctx.save();
    ctx.shadowBlur = 14 + (overdrive ? 10 : 0);
    ctx.shadowColor = this.fire > 0 ? '#ff7b1f' : '#8ef7ff';
    ctx.fillStyle = this.fire > 0 ? '#ffb347' : '#eaffff';
    ctx.beginPath();
    ctx.arc(this.x, this.y, this.r, 0, TAU);
    ctx.fill();
    if (this.fire > 0) {
      ctx.globalAlpha = 0.5 + 0.3 * Math.sin(time * 20);
      ctx.fillStyle = '#ff5e00';
      ctx.beginPath();
      ctx.arc(this.x, this.y, this.r + 3.5, 0, TAU);
      ctx.fill();
    }
    ctx.restore();
  }
}

// ---------------------------------------------------------------------------
class Paddle {
  constructor() {
    this.w = 90; this.h = 14;
    this.x = W / 2; this.y = H - 90;
    this.vx = 0; this.vy = 0;
    this.targetX = this.x; this.targetY = this.y;
    this.wide = 0;               // seconds of wide remaining
    this.laser = 0;              // seconds of laser remaining
    this.laserCd = 0;
    this.sticky = 0;             // seconds of catch remaining
    this.shield = false;
    this.charge = 0;             // 0..1 recent-speed glow
  }

  get topY() { return this.y - this.h / 2; }
  get halfW() { return this.w / 2 + (this.wide > 0 ? 25 : 0); }
  get isWide() { return this.wide > 0; }

  update(dt, input) {
    const st = input.state;
    // --- horizontal target ---
    if (st.stickActive || Math.abs(st.stickVec.x) > 0.05) {
      this.targetX += st.stickVec.x * 620 * dt;
    }
    if (st.pointerX !== null && input.mode === 'pointer') {
      const lx = toLogicalX(st.pointerX);
      this.targetX = lerp(this.targetX, lx, 1 - Math.pow(0.0001, dt));
    }
    // keyboard handled via stickVec in Input.update; also allow direct keys
    const kl = st.keys.has('left'), kr = st.keys.has('right');
    if (kl) this.targetX -= 520 * dt;
    if (kr) this.targetX += 520 * dt;

    // --- vertical band (H-150 .. H-40) ---
    const vyIntent = st.stickVec.y;
    let ty = this.targetY;
    if (st.stickActive || Math.abs(vyIntent) > 0.05) ty += vyIntent * 300 * dt;
    if (st.keys.has('up')) ty -= 300 * dt;
    if (st.keys.has('down')) ty += 300 * dt;
    if (st.pointerY !== null && input.mode === 'pointer' && !st.stickActive) {
      const ly = toLogicalY(st.pointerY);
      if (ly > H - 260) ty = lerp(ty, clamp(ly, H - 150, H - 40), 1 - Math.pow(0.001, dt));
    }
    this.targetY = clamp(ty, H - 150, H - 40);
    this.targetX = clamp(this.targetX, this.halfW, W - this.halfW);

    // smooth toward target, track velocity
    const nx = lerp(this.x, this.targetX, 1 - Math.pow(0.000001, dt));
    const ny = lerp(this.y, this.targetY, 1 - Math.pow(0.000001, dt));
    this.vx = (nx - this.x) / Math.max(dt, 1e-6);
    this.vy = (ny - this.y) / Math.max(dt, 1e-6);
    this.x = nx; this.y = ny;

    // timers
    if (this.wide > 0) this.wide -= dt;
    if (this.laser > 0) this.laser -= dt;
    if (this.sticky > 0) this.sticky -= dt;
    if (this.laserCd > 0) this.laserCd -= dt;
    this.charge = lerp(this.charge, clamp(Math.abs(this.vy) / 300, 0, 1), dt * 6);
  }

  // Called by Game when a ball hits the paddle. Returns outgoing velocity.
  bounce(ball) {
    const rel = clamp((ball.x - this.x) / this.halfW, -1, 1);
    const ang = -Math.PI / 2 + rel * (Math.PI / 3); // ±60°
    let sp = Math.min(ball.speed * 1.01 + 4, BALL_SPEED_CAP);
    let lift = false;
    // paddle-lift: moving up (screen coords: negative vy) at impact
    if (this.vy < -60) {
      lift = true;
      sp = Math.min(sp * 1.10 + 20, BALL_SPEED_CAP);
    }
    let vx = Math.cos(ang) * sp;
    let vy = Math.sin(ang) * sp;
    if (lift) {
      vy -= 40;                    // upward bias
      const s2 = Math.hypot(vx, vy);
      vx = vx / s2 * sp; vy = vy / s2 * sp;
    }
    // keep some horizontal influence from paddle motion
    vx += clamp(this.vx, -300, 300) * 0.08;
    ball.vx = vx; ball.vy = vy;
    return lift;
  }

  draw(ctx, time) {
    const hw = this.halfW;
    ctx.save();
    // thruster when moving up
    if (this.vy < -40) {
      const f = clamp(-this.vy / 300, 0, 1);
      ctx.globalAlpha = 0.7;
      const g = ctx.createLinearGradient(this.x, this.y + 10, this.x, this.y + 10 + 26 * f);
      g.addColorStop(0, '#8ef7ff');
      g.addColorStop(1, 'rgba(80,200,255,0)');
      ctx.fillStyle = g;
      ctx.beginPath();
      ctx.moveTo(this.x - 8, this.y + 10);
      ctx.lineTo(this.x + 8, this.y + 10);
      ctx.lineTo(this.x, this.y + 10 + 26 * f);
      ctx.closePath();
      ctx.fill();
      ctx.globalAlpha = 1;
    }
    // capsule
    ctx.shadowBlur = 12 + this.charge * 16;
    ctx.shadowColor = '#4ef0ff';
    const grad = ctx.createLinearGradient(this.x, this.y - this.h / 2, this.x, this.y + this.h / 2);
    grad.addColorStop(0, '#bffcff');
    grad.addColorStop(0.5, '#4ef0ff');
    grad.addColorStop(1, '#0e7f9e');
    ctx.fillStyle = grad;
    roundRect(ctx, this.x - hw, this.y - this.h / 2, hw * 2, this.h, 7);
    ctx.fill();
    ctx.shadowBlur = 0;
    // cyan core line
    ctx.fillStyle = 'rgba(255,255,255,0.85)';
    ctx.fillRect(this.x - hw * 0.7, this.y - 1, hw * 1.4, 2);
    // laser cannons
    if (this.laser > 0) {
      ctx.fillStyle = '#ff5470';
      ctx.fillRect(this.x - hw + 2, this.y - this.h / 2 - 6, 6, 6);
      ctx.fillRect(this.x + hw - 8, this.y - this.h / 2 - 6, 6, 6);
      ctx.shadowBlur = 8; ctx.shadowColor = '#ff5470';
    }
    // sticky indicator
    if (this.sticky > 0) {
      ctx.strokeStyle = '#ffe066';
      ctx.lineWidth = 2;
      ctx.setLineDash([4, 4]);
      ctx.beginPath();
      ctx.moveTo(this.x - hw, this.y - this.h / 2 - 4);
      ctx.lineTo(this.x + hw, this.y - this.h / 2 - 4);
      ctx.stroke();
      ctx.setLineDash([]);
    }
    // shield
    if (this.shield) {
      ctx.strokeStyle = 'rgba(120,255,180,0.8)';
      ctx.lineWidth = 2.5;
      ctx.shadowBlur = 10; ctx.shadowColor = '#78ffb4';
      ctx.beginPath();
      ctx.moveTo(0, H - 14); ctx.lineTo(W, H - 14);
      ctx.stroke();
    }
    ctx.restore();
  }
}

// ---------------------------------------------------------------------------
const POWERUPS = {
  wide:   { color: '#4ef0ff', label: 'WIDE', glyph: 'W' },
  multi:  { color: '#ff9de2', label: 'MULTI', glyph: '×3' },
  laser:  { color: '#ff5470', label: 'LASER', glyph: 'LZR' },
  slow:   { color: '#9d7dff', label: 'SLOW', glyph: 'SLW' },
  fire:   { color: '#ff8a2a', label: 'FIRE', glyph: 'FIR' },
  catch:  { color: '#ffe066', label: 'CATCH', glyph: 'CAT' },
  life:   { color: '#78ffb4', label: 'LIFE', glyph: '+1' },
  shield: { color: '#7dffb0', label: 'SHIELD', glyph: 'SHD' },
};
// weighted table for unauthored basic-brick drops
const POWERUP_TABLE = [
  ['wide', 20], ['multi', 16], ['laser', 14], ['slow', 10],
  ['fire', 12], ['catch', 10], ['life', 6], ['shield', 8],
];

class PowerUp {
  constructor(type, x, y) {
    this.type = type;
    this.x = x; this.y = y;
    this.vy = 120;
    this.t = Math.random() * TAU;
    this.w = 26; this.h = 16;
  }

  update(dt) {
    this.y += this.vy * dt;
    this.t += dt * 4;
    this.x += Math.sin(this.t) * 18 * dt;
  }

  get def() { return POWERUPS[this.type]; }

  draw(ctx) {
    const d = this.def;
    ctx.save();
    ctx.translate(this.x + Math.sin(this.t) * 2, this.y);
    ctx.shadowBlur = 12; ctx.shadowColor = d.color;
    ctx.fillStyle = d.color;
    roundRect(ctx, -this.w / 2, -this.h / 2, this.w, this.h, 8);
    ctx.fill();
    ctx.shadowBlur = 0;
    ctx.fillStyle = '#0a0d1f';
    ctx.font = d.glyph.length > 2 ? 'bold 8px monospace' : 'bold 9px monospace';
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    ctx.fillText(d.glyph, 0, 0.5);
    ctx.restore();
  }
}

// ---------------------------------------------------------------------------
class Laser {
  constructor(x, y) {
    this.x = x; this.y = y;
    this.vy = -620;
    this.w = 3; this.h = 14;
  }
  update(dt) { this.y += this.vy * dt; }
  draw(ctx) {
    ctx.save();
    ctx.shadowBlur = 10; ctx.shadowColor = '#ff5470';
    ctx.fillStyle = '#ffd0da';
    ctx.fillRect(this.x - 1.5, this.y - 7, 3, 14);
    ctx.restore();
  }
}
