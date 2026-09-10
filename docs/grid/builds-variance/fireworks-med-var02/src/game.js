// Core game logic: fixed-timestep sim, ball physics & collisions, brick rules,
// frenzy/breakthrough system, power-ups, scoring, lives, levels.
import { FIELD, WALL, PADDLE, BALL, BRICK, FRENZY, CAPSULE, PHYS, POWER_DEFS, COLORS, POWER_POOL } from './constants.js';
import { BrickField, Brick } from './bricks.js';
import { Paddle, Ball, Capsule, rollPower } from './entities.js';
import { Particles } from './particles.js';
import { AudioSys } from './audio.js';

const clamp = (v, a, b) => Math.max(a, Math.min(b, v));
const VEL_THRESHOLD = 760;      // velocity-lock requirement
const ANGLE_DOT_MAX = 0.45;     // angle-lock: |cos(angle to normal)| must be below this (grazing)
const EXPLODE_RADIUS = 115;

export class Game {
  constructor() {
    this.audio = new AudioSys();
    this.particles = new Particles();
    this.renderer = null; // set by main
    this.ui = null;       // set by main
    this.state = 'menu';  // menu | playing | paused | over
    this.score = 0;
    this.best = +(localStorage.getItem('bt_best') || 0);
    this.lives = 3;
    this.levelIndex = 0;
    this.combo = 0; this.comboTimer = 0;
    this.mult = 1; this.upTimer = 0; this.wasUp = false;
    this.paddle = new Paddle();
    this.balls = [];
    this.capsules = [];
    this.popups = [];
    this.field = null;
    this.ballPower = null; this.ballPowerTimer = 0;
    this.slowTimer = 0;
    this.levelIntroTimer = 0;
    this.clearTimer = 0;
    this.time = 0;
    this._haptics = 'vibrate' in navigator;
  }

  fx = {
    regrow: (b) => {
      this.particles.burst(b.cx, b.cy, COLORS.brick.REGEN, 10, { speed: 90, ttl: 0.5, size: 3 });
      this.audio.bounce(2, 260);
    },
  };

  // ---------- lifecycle ----------
  newRun() {
    this.score = 0; this.lives = 3; this.levelIndex = 0;
    this.bricksDestroyed = 0; this.maxMult = 1;
    this.mult = 1; this.ballPower = null; this.ballPowerTimer = 0;
    this.paddle = new Paddle();
    this.startLevel(0);
  }
  startLevel(i) {
    this.levelIndex = i;
    this.field = new BrickField(i);
    this.capsules = []; this.popups = [];
    this.mult = 1; this.upTimer = 0;
    this.ballPowerTimer = 0; this.ballPower = null;
    this.slowTimer = 0;
    this.levelIntroTimer = 2.2;
    this.resetBall();
    this.ui && this.ui.showLevelBanner(this.field.level.name, this.field.level.hint);
    this.ui && this.ui.syncHud(this);
  }
  resetBall() {
    this.balls = [new Ball(this.paddle.x, this.paddle.y - 20, 0, 0)];
    const b = this.balls[0];
    b.stuck = true; b.stuckOffset = 0;
    this.combo = 0;
  }
  launchBall(ball) {
    if (!ball.stuck) return;
    ball.stuck = false;
    const rel = clamp((ball.x - this.paddle.x) / (this.paddle.w / 2), -1, 1);
    let ang = -Math.PI / 2 + rel * (Math.PI / 5); // ±36°
    ang += clamp(this.paddle.vy * 0.0004, -0.25, 0.25); // vertical position/motion aims serve
    const s = BALL.baseSpeed;
    ball.vx = Math.cos(ang) * s; ball.vy = Math.sin(ang) * s;
    this.audio.paddle(0.5);
  }

  // ---------- main fixed update ----------
  update(dt) {
    this.time += dt;
    this.particles.update(dt);
    for (let i = this.popups.length - 1; i >= 0; i--) {
      const p = this.popups[i];
      p.life += dt; p.y -= 40 * dt;
      if (p.life > p.ttl) this.popups.splice(i, 1);
    }
    if (this.state !== 'playing') {
      // idle menu animation only
      if (this.state === 'menu' && this.field) this.field.update(dt, this, this.fx);
      return;
    }
    if (this.levelIntroTimer > 0) this.levelIntroTimer -= dt;
    if (this.clearTimer > 0) {
      this.clearTimer -= dt;
      if (this.clearTimer <= 0) this.startLevel(this.levelIndex + 1);
      return;
    }

    const slowK = this.slowTimer > 0 ? 0.5 : 1;
    const wdt = dt * slowK;
    if (this.slowTimer > 0) this.slowTimer -= dt;

    // timers
    if (this.comboTimer > 0) { this.comboTimer -= dt; if (this.comboTimer <= 0) this.combo = 0; }
    if (this.ballPowerTimer > 0) { this.ballPowerTimer -= dt; if (this.ballPowerTimer <= 0) this.ballPower = null; }
    if (this.paddle.stickyTimer > 0) { this.paddle.stickyTimer -= dt; if (this.paddle.stickyTimer <= 0) this.paddle.sticky = false; }

    this.paddle.update(wdt, this.input, this.pointerField);
    // stuck ball follows paddle
    for (const b of this.balls) if (b.stuck) { b.x = this.paddle.x + b.stuckOffset; b.y = this.paddle.y - this.paddle.h / 2 - b.r - 2; }

    this.field.update(wdt, this, this.fx);

    // balls
    for (let i = this.balls.length - 1; i >= 0; i--) {
      const b = this.balls[i];
      b.px = b.x; b.py = b.y;
      if (b.stuck) continue;
      if (b.ttl) { b.ttl -= wdt; if (b.ttl <= 0) { this.balls.splice(i, 1); continue; } }
      this.stepBall(b, wdt);
      if (b.y > FIELD.h + 60) {
        this.balls.splice(i, 1);
        if (this.balls.length === 0) this.loseLife();
      }
    }    // trail + squash decay
    for (const b of this.balls) {
      b.trail.push({ x: b.x, y: b.y });
      if (b.trail.length > 14) b.trail.shift();
      if (b.squash > 0) b.squash = Math.max(0, b.squash - dt * 4);
    }

    // capsules
    for (let i = this.capsules.length - 1; i >= 0; i--) {
      const c = this.capsules[i];
      c.update(wdt);
      const p = this.paddle;
      if (!c.dead && Math.abs(c.x - p.x) < p.w / 2 + c.w / 2 && Math.abs(c.y - p.y) < p.h / 2 + c.h / 2) {
        c.dead = true; this.applyPower(c.kind, c.x, c.y);
      }
      if (c.dead) this.capsules.splice(i, 1);
    }

    // frenzy
    this.updateFrenzy(dt);

    // level clear?
    if (this.field.aliveCount === 0 && this.clearTimer <= 0) {
      this.clearTimer = 2.2;
      this.ui && this.ui.showBanner('LEVEL CLEAR!', '+' + this.formatScore(1000 * this.mult), false);
      this.addScore(1000, this.paddle.x, this.paddle.y - 60, false);
      this.audio.levelWin();
      this.renderer.addFlash(0.5);
    }
    this.ui && this.ui.syncHud(this);
  }

  // ---------- frenzy ----------
  _anyBallUp() {
    let minTop = Infinity, count = 0;
    for (const b of this.field.bricks) if (b.alive) { count++; if (b.y < minTop) minTop = b.y; }
    if (count < 3) return false;
    const line = minTop + BRICK.h;
    return this.balls.some((b) => !b.stuck && b.y < line);
  }
  updateFrenzy(dt) {
    const up = this._anyBallUp();
    if (up && !this.wasUp) this.upTimer = 0;
    this.wasUp = up;
    if (up) {
      this.upTimer += dt;
      if (this.mult <= 1 && this.upTimer > FRENZY.buildTime) {
        this.mult = 1.05;
        this.audio.frenzyStart();
        this.renderer.addFlash(0.9);
        this.renderer.addShake(18);
        this.ui && this.ui.showBanner('BREAKTHROUGH!', 'GET ON TOP — KEEP IT UP', true);
        this._buzz(80);
        const b = this.balls[0];
        if (b) for (let i = 0; i < 40; i++)
          this.particles.spawn({ x: b.x, y: b.y, color: ['#fbbf24', '#fb7185', '#5eead4'][i % 3], speed: 420, ttl: 1.1, size: 4, shape: 2, grav: 60 });
      }
    } else {
      this.upTimer = Math.max(0, this.upTimer - dt * 3);
    }
    if (this.mult > 1) {
      this.maxMult = Math.max(this.maxMult, this.mult);
      if (up) {
        this.mult = Math.min(FRENZY.maxMult, this.mult * (1 + FRENZY.rampRate * dt * 3.2));
        this.renderer.addTint((this.mult / FRENZY.maxMult) * 0.5);
      } else {
        this.mult -= FRENZY.decayRate * dt * (1 + this.mult * 0.25);
        if (this.mult <= 1.02) { this.mult = 1; }
      }
      this.audio.setFrenzy(this.mult);
    } else {
      this.audio.setFrenzy(1);
    }
  }

  // ---------- ball physics ----------
  stepBall(ball, dt) {
    const speed = ball.speed();
    const steps = clamp(Math.ceil((speed * dt) / (ball.r * 0.9)), 1, PHYS.ballSubstep);
    const h = dt / steps;
    for (let s = 0; s < steps; s++) {
      if (this._substep(ball, h)) break; // ball lost / stuck
    }
    ball.clampSpeed();
  }
  _substep(ball, h) {
    ball.x += ball.vx * h; ball.y += ball.vy * h;
    const r = ball.r;
    // walls
    if (ball.x < WALL + r) { ball.x = WALL + r; ball.vx = Math.abs(ball.vx); this._wallFx(ball, 1, 0); }
    else if (ball.x > FIELD.w - WALL - r) { ball.x = FIELD.w - WALL - r; ball.vx = -Math.abs(ball.vx); this._wallFx(ball, -1, 0); }
    if (ball.y < WALL + r) { ball.y = WALL + r; ball.vy = Math.abs(ball.vy); this._wallFx(ball, 0, 1); this._antiFlat(ball); }
    // paddle
    const p = this.paddle;
    if (ball.vy > 0 && ball.y + r >= p.y - p.h / 2 && ball.y - r < p.y + p.h + 14 &&
        Math.abs(ball.x - p.x) <= p.w / 2 + r) {
      this._paddleBounce(ball, p);
      return false;
    }
    // bricks
    const cands = this.field.query(ball.x - ball.vx * h, ball.y - ball.vy * h, ball.x, ball.y, r);
    for (const b of cands) {
      if (!BrickField.circleHit(b, ball.x, ball.y, r)) continue;
      const res = this._hitBrick(ball, b);
      if (res === 'pierce') continue;   // fireball burned through, keep going
      break;                            // bounced or destroyed: one brick per substep
    }
    return false;
  }
  _wallFx(ball, nx, ny) {
    this.audio.wall();
    ball.squash = 0.4; ball.squashAng = nx !== 0 ? Math.PI / 2 : 0;
    this.particles.burst(ball.x, ball.y, '#8b95b5', 5, { speed: 130, ttl: 0.3, size: 2.5, shape: 1 });
    this.renderer.addShake(0.8);
  }
  _antiFlat(ball) {
    const s = ball.speed() || 1;
    const minVy = Math.sin(BALL.maxFlatAngle) * s;
    if (Math.abs(ball.vy) < minVy) {
      ball.vy = (ball.vy < 0 ? -1 : 1) * minVy;
      ball.vx = (ball.vx < 0 ? -1 : 1) * Math.sqrt(Math.max(0, s * s - ball.vy * ball.vy));
    }
    // anti-vertical-lock: when above the brick field, keep the ball raining
    // ACROSS the brick tops instead of bouncing straight up/down in a slot
    const topAlive = this._topOfWall();
    if (topAlive !== null && ball.y < topAlive + BRICK.h) {
      const minVx = Math.cos(BALL.maxFlatAngle) * s * 0.6; // ≥ ~34% horizontal
      if (Math.abs(ball.vx) < minVx) {
        const side = ball.vx >= 0 ? 1 : -1;
        ball.vx = side * minVx;
        ball.vy = (ball.vy < 0 ? -1 : 1) * Math.sqrt(Math.max(0, s * s - ball.vx * ball.vx));
      }
    }
  }
  _topOfWall() {
    let minTop = null;
    for (const b of this.field.bricks) {
      if (b.alive && (minTop === null || b.y < minTop)) minTop = b.y;
    }
    return minTop;
  }
  _paddleBounce(ball, p) {
    const vyAtContact = p.vy;
    ball.y = p.y - p.h / 2 - ball.r;
    const rel = clamp((ball.x - p.x) / (p.w / 2 + ball.r), -1, 1);
    let speed = Math.min(BALL.maxSpeed, ball.speed() * (1 + BALL.speedRamp) + 8);
    let smash = false;
    if (vyAtContact < -90) { // moving up at contact → SMASH
      speed = Math.min(BALL.maxSpeed, speed + PADDLE.smashBoost);
      smash = true;
    } else if (vyAtContact > 90) { // moving down → absorb
      speed = Math.max(BALL.minSpeed * 0.8, speed * PADDLE.slowDamp);
    }
    const ang = -Math.PI / 2 + rel * (Math.PI / 3); // ±60°
    ball.vx = Math.cos(ang) * speed;
    ball.vy = Math.sin(ang) * speed;
    ball.squash = smash ? 0.6 : 0.45; ball.squashAng = 0;
    this.combo = 0;
    p.squash = 1;
    if (smash) {
      this.audio.smash();
      this.renderer.addShake(4); this.renderer.addFlash(0.12);
      this.particles.burst(ball.x, p.y - p.h, '#ffe066', 14, { speed: 260, ttl: 0.4, size: 3, shape: 1, grav: 500 });
      this.popup(ball.x, p.y - 30, 'SMASH!', '#fbbf24', 0.7);
      this._buzz(20);
    } else {
      this.audio.paddle(Math.abs(rel));
      this.particles.burst(ball.x, p.y - p.h, '#7dd3fc', 6, { speed: 140, ttl: 0.3, size: 2.5, shape: 1 });
    }
    if (p.sticky && !smash) {
      ball.stuck = true; ball.stuckOffset = clamp(ball.x - p.x, -p.w / 2 + 8, p.w / 2 - 8);
      ball.stuckFromSticky = true;
      this.audio.bounce(4, 500);
    }
  }

  // ---------- brick hit rules ----------
  _hitBrick(ball, b) {
    const type = b.type;
    const power = this.ballPower;
    const speed = ball.speed();
    // collision normal (from closest point on AABB)
    const nx0 = clamp(ball.x, b.x, b.x + b.w), ny0 = clamp(ball.y, b.y, b.y + b.h);
    let nx = ball.x - nx0, ny = ball.y - ny0;
    let d = Math.hypot(nx, ny);
    if (d < 0.001) { nx = -ball.vx; ny = -ball.vy; d = Math.hypot(nx, ny) || 1; }
    nx /= d; ny /= d;
    const vdotn = (ball.vx * nx + ball.vy * ny) / (speed || 1); // <0 approaching

    const bounce = () => {
      const dot = ball.vx * nx + ball.vy * ny;
      ball.vx -= 2 * dot * nx; ball.vy -= 2 * dot * ny;
      // tiny jitter to break loops
      const j = (Math.random() - 0.5) * 0.06;
      const c = Math.cos(j), s = Math.sin(j);
      const vx = ball.vx * c - ball.vy * s; ball.vy = ball.vx * s + ball.vy * c; ball.vx = vx;
      ball.x = nx0 + nx * (ball.r + 0.5); ball.y = ny0 + ny * (ball.r + 0.5);
      ball.squash = 0.45; ball.squashAng = Math.atan2(ny, nx);
      this._antiFlat(ball);
    };

    // GHOST ball vs PHASE brick: passes through and breaks it
    if (type === 'PHASE' && power === 'GHOST') {
      this._destroyBrick(b, ball); return 'pierce';
    }
    if (type === 'PHASE' && !b.phaseSolid) { bounce(); this.audio.clank(); b.flash = 0.5; return 'bounce'; }

    if (type === 'UNBREAK') {
      bounce(); this.audio.clank(); b.flash = 0.6; b.shake = 0.5;
      this.particles.burst(nx0, ny0, COLORS.brick.UNBREAK, 6, { speed: 160, ttl: 0.3, size: 2.5, shape: 1 });
      return 'bounce';
    }
    // HEAVY ball plows through angle/velocity locks and armor at any angle/speed
    if (power === 'HEAVY' && (type === 'ANGLE' || type === 'VELOCITY' || type === 'ARMOR')) {
      this._destroyBrick(b, ball, true);
      return 'pierce';
    }
    if (type === 'EXPLOSIVE') {
      bounce();
      this._explode(b, ball);
      return 'bounce';
    }
    if (type === 'ARMOR') {
      b.hp--;
      if (b.hp > 0) {
        bounce(); this.audio.bounce(1, 200); b.flash = 0.8; b.shake = 0.6;
        this.particles.burst(nx0, ny0, COLORS.brick.ARMOR, 8, { speed: 180, ttl: 0.35, size: 3 });
        this.popup(b.cx, b.y - 14, 'CRACK!', '#cbd5e1', 0.5);
        return 'bounce';
      }
    }
    // FIREBALL pierces everything else, burning a line
    if (power === 'FIRE') {
      this._destroyBrick(b, ball, true);
      return 'pierce';
    }
    if (type === 'ANGLE') {
      const grazing = Math.abs(vdotn) < ANGLE_DOT_MAX;
      if (!grazing) {
        bounce(); this.audio.clank(); b.flash = 0.55; b.shake = 0.4;
        this.particles.burst(nx0, ny0, '#ffffff', 5, { speed: 120, ttl: 0.25, size: 2, shape: 1 });
        return 'bounce';
      }
      // correct angle: spark shower + break
      this.particles.burst(nx0, ny0, '#a7f3d0', 16, { speed: 300, ttl: 0.5, size: 3, shape: 1 });
    }
    if (type === 'VELOCITY') {
      if (speed < VEL_THRESHOLD) {
        bounce(); this.audio.clank(); b.flash = 0.5; b.shake = 0.4;
        this.popup(b.cx, b.y - 14, 'TOO SLOW', '#fdba74', 0.6);
        return 'bounce';
      }
      this.particles.burst(nx0, ny0, '#fdba74', 14, { speed: 300, ttl: 0.45, size: 3, shape: 1 });
    }
    this._destroyBrick(b, ball);
    return 'bounce';
  }

  _explode(b, ball) {
    this.audio.explosion();
    this.renderer.addShake(16); this.renderer.addFlash(0.5); this.renderer.addTint(0.6);
    this._buzz(60);
    this.particles.burst(b.cx, b.cy, '#f87171', 50, { speed: 460, ttl: 0.9, size: 5 });
    this.particles.burst(b.cx, b.cy, '#fbbf24', 30, { speed: 300, ttl: 0.7, size: 4, shape: 2 });
    b.alive = false;
    this._onBrickDestroyed(b, ball, true);
    const targets = [];
    for (const o of this.field.bricks) {
      if (!o.alive || o.type === 'UNBREAK') continue;
      if (Math.hypot(o.cx - b.cx, o.cy - b.cy) <= EXPLODE_RADIUS) targets.push(o);
    }
    for (const o of targets) {
      if (o.type === 'EXPLOSIVE') { // chain
        o.alive = false; this._onBrickDestroyed(o, ball, true);
        this._explodeChain(o, ball);
      } else {
        o.alive = false; this._onBrickDestroyed(o, ball, true);
      }
    }
  }
  _explodeChain(b, ball) {
    for (const o of this.field.bricks) {
      if (!o.alive || o.type === 'UNBREAK') continue;
      if (Math.hypot(o.cx - b.cx, o.cy - b.cy) <= EXPLODE_RADIUS * 0.8) {
        o.alive = false; this._onBrickDestroyed(o, ball, true);
        if (o.type === 'EXPLOSIVE') this._explodeChain(o, ball);
      }
    }
  }

  _destroyBrick(b, ball, pierced = false) {
    b.alive = false;
    this.audio.bounce(this.combo, 340);
    this.renderer.addShake(1.6);
    this.particles.burst(b.cx, b.cy, COLORS.brick[b.type] || '#fff', 16, { speed: 240, ttl: 0.6, size: 4 });
    this.particles.burst(b.cx, b.cy, '#ffffff', 5, { speed: 320, ttl: 0.3, size: 2, shape: 1 });
    if (pierced) this.particles.burst(b.cx, b.cy, '#fb923c', 8, { speed: 180, ttl: 0.5, size: 3, shape: 2 });
    this._onBrickDestroyed(b, ball, pierced);
  }

  _onBrickDestroyed(b, ball, silentRoll = false) {
    this.bricksDestroyed = (this.bricksDestroyed || 0) + 1;
    this.combo++; this.comboTimer = 1.8;
    const pts = Math.round((50 + Math.min(this.combo, 25) * 10) * this.mult / 10) * 10;
    this.addScore(pts, b.cx, b.cy, this.combo > 2);
    if (this.mult > 1) {
      this.audio.frenzyBreak(this.mult);
      this.renderer.addFlash(0.05 + this.mult * 0.01);
    }
    // capsule drop
    if (!silentRoll && Math.random() < CAPSULE.dropChanceBase) {
      this.capsules.push(new Capsule(b.cx, b.cy, rollPower()));
    }
    // regen scheduling
    if (b.type === 'REGEN') { b.regenWaiting = true; b.regenTimer = 6; }
    // splitter
    if (this.ballPower === 'SPLIT' && this.balls.length < 8) {
      const nb = new Ball(b.cx, b.cy, (Math.random() - 0.5) * 500, -(200 + Math.random() * 300));
      nb.ttl = 5.5;
      this.balls.push(nb);
    }
  }

  // ---------- power-ups ----------
  applyPower(kind, x, y) {
    const def = POWER_DEFS[kind];
    this.audio.pickup();
    if (kind === 'SHRINK') this.audio.badPickup();
    if (kind === 'LIFE') {
      this.audio.life();
      this.lives = Math.min(6, this.lives + 1);
      this.popup(x, y - 20, '1-UP!', def.color, 1);
      this.renderer.addFlash(0.3);
      return;
    }
    this.popup(x, y - 20, def.label, def.color, 0.9);
    this.particles.burst(x, y, def.color, 14, { speed: 220, ttl: 0.5, size: 3, shape: 2, grav: 100 });
    if (def.kind === 'ball') { this.ballPower = kind; this.ballPowerTimer = def.dur; }
    else if (kind === 'WIDE') this.paddle.wideTimer = def.dur;
    else if (kind === 'SHRINK') this.paddle.shrinkTimer = def.dur;
    else if (kind === 'STICKY') { this.paddle.sticky = true; this.paddle.stickyTimer = def.dur; }
    else if (kind === 'SLOWMO') this.slowTimer = def.dur;
  }

  // ---------- misc ----------
  addScore(n, x, y, showCombo) {
    this.score += n;
    this.popup(x, y, '+' + this.formatScore(n), this.mult > 1 ? '#fbbf24' : '#e8f0ff', 0.8,
      showCombo && this.combo > 2 ? 'COMBO ×' + this.combo : null);
  }
  formatScore(n) { return n >= 1000 ? (n / 1000).toFixed(n % 1000 === 0 ? 0 : 1) + 'k' : '' + n; }
  popup(x, y, text, color, ttl, sub) {
    // avoid stacking popup text on top of recent popups (unreadable pile-ups in frenzy)
    let dy = 0, dx = 0;
    for (let i = this.popups.length - 1; i >= 0 && i >= this.popups.length - 7; i--) {
      const p = this.popups[i];
      if (Math.abs(p.x - x) < 90 && Math.abs(p.y - y) < 64) {
        dy -= 26; dx += (dx >= 0 ? 22 : -22);
        if (dy < -78) break;
      }
    }
    this.popups.push({ x: x + dx, y: y + dy, text, color, ttl: ttl || 0.8, life: 0, sub });
    if (this.popups.length > 24) this.popups.shift();
  }
  loseLife() {
    this.lives--;
    this.audio.loseLife();
    this.renderer.addShake(10);
    this.ballPower = null; this.ballPowerTimer = 0;
    this.mult = 1;
    if (this.lives <= 0) {
      this.state = 'over';
      this.audio.gameOver();
      if (this.score > this.best) { this.best = this.score; localStorage.setItem('bt_best', '' + this.best); }
      this.ui && this.ui.showGameOver(this);
    } else {
      this.resetBall();
      this.ui && this.ui.showBanner('BALL LOST', this.lives + ' LIVES LEFT', false);
    }
  }
  _buzz(ms) { if (this._haptics) try { navigator.vibrate(ms); } catch (e) {} }
}
