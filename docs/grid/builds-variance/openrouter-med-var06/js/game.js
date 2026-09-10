// Game: state machine, physics, collisions, rush/breakthrough logic.

import { W, H, BALL_BASE_SPEED, BALL_MAX_SPEED, PADDLE_ZONE, PADDLE_SPEED,
         PADDLE_FRICTION, RUSH_MULT_CAP, RUSH_RAMP_TIME, POWERUP_DROP_CHANCE,
         POWERUP_FALL, START_LIVES, SLOWMO_SCALE, SLOWMO_TIME, BRICK_TOP } from './config.js';
import { Paddle, Ball, Brick, PowerUp, PU_META } from './entities.js';
import { Particles } from './particles.js';
import { audio } from './audio.js';
import { LEVELS, makeEndlessLevel, buildBricks, BRICK_HINTS } from './levels.js';
import { FX } from './fx.js';
import { UI } from './ui.js';

export class Game {
  constructor(canvas) {
    this.canvas = canvas;
    this.ctx = canvas.getContext('2d');
    this.particles = new Particles();
    this.fx = new FX();
    this.ui = new UI(this);
    this.state = 'menu'; // menu | play | pause | levelclear | over
    this.scale = 1; this.offX = 0; this.offY = 0;
    this.time = 0;
    this._resize();
    window.addEventListener('resize', () => this._resize());
    this.reset();
  }

  _resize() {
    const dpr = Math.min(window.devicePixelRatio || 1, 2.5);
    const w = window.innerWidth, h = window.innerHeight;
    this.canvas.width = w * dpr; this.canvas.height = h * dpr;
    this.canvas.style.width = w + 'px'; this.canvas.style.height = h + 'px';
    this.vw = w; this.vh = h; this.dpr = dpr;
    // fit world W x H into viewport, centered
    const s = Math.min(w / W, h / H);
    this.scale = s * dpr;
    this.offX = (w * dpr - W * this.scale) / 2;
    this.offY = (h * dpr - H * this.scale) / 2;
  }

  // world coords -> canvas pixels
  sx(x) { return this.offX + x * this.scale; }
  sy(y) { return this.offY + y * this.scale; }
  // canvas px -> world
  wx(px) { return (px - this.offX) / this.scale; }
  wy(py) { return (py - this.offY) / this.scale; }

  reset() {
    this.score = 0; this.mult = 1; this.combo = 0; this.lives = START_LIVES;
    this.level = 0; this.best = +(localStorage.getItem('bt_best') || 0);
    this.paddle = new Paddle();
    this.balls = []; this.powerups = [];
    this.rush = 0; this.rushMult = 1; this.rushTime = 0;
    this.slowT = 0; this.ghostT = 0; this.bomberT = 0; this.bomberArmed = false;
    this.speedBoostT = 0;
    this.hintT = 0; this.hitStop = 0;
    this.seenHints = new Set();
    this.shakeOn = true;
    this.startLevel(0);
  }

  startLevel(i) {
    this.level = i;
    const def = i < LEVELS.length ? LEVELS[i] : makeEndlessLevel(i + 1);
    const bw = (W - 6) / 11 - 0.7;
    this.bricks = buildBricks(def, bw, 4.2, 3);
    this.levelName = def.name;
    this.powerups.length = 0;
    this.resetBall();
    this.rush = 0; this.rushMult = 1; this.rushTime = 0;
    this.ui.updateHUD();
    if (def.hint) this.showHint(def.hint);
  }

  resetBall() {
    this.balls = [new Ball(this.paddle.x, this.paddle.y - 4, 0, 0)];
    this.balls[0].launchPending = true;
    this.rush = 0; this.rushMult = 1; this.rushTime = 0;
  }

  launch() {
    const b = this.balls.find(b => b.launchPending);
    if (!b) return;
    b.launchPending = false;
    const a = -Math.PI / 2 + (Math.random() - 0.5) * 0.5;
    b.vx = Math.cos(a) * BALL_BASE_SPEED; b.vy = Math.sin(a) * BALL_BASE_SPEED;
    b.setSpeed(BALL_BASE_SPEED * 1.1);
    audio.paddleHit(0.4);
    this.particles.burst(b.x, b.y, 10, { color: ['#cfeaff', '#6cf'], speed: 26, grav: 20 });
  }

  // ---------- main update ----------
  update(dt) {
    this.time += dt;
    this.fx.update(dt);
    this.particles.update(dt);
    if (this.state !== 'play') return;

    // slow time powerup
    let ts = dt;
    if (this.slowT > 0) { this.slowT -= dt; ts = dt * SLOWMO_SCALE; }
    if (this.ghostT > 0) this.ghostT -= dt;

    if (this.hintT > 0) this.hintT -= dt;

    this.updatePaddle(dt);
    // ball(s)
    const iter = this.hitStop > 0 ? 0 : (1 + Math.ceil(ts / 0.008));
    if (this.hitStop > 0) this.hitStop -= dt;
    for (let k = 0; k < iter; k++) {
      const sub = ts / iter;
      for (const ball of this.balls) this.updateBall(ball, sub);
    }
    this.updateRush(dt);
    this.updatePowerups(ts);
    this.updateBricks(ts);

    if (this.balls.length === 0 && this.state === 'play') {
      this.lives--;
      audio.loseBall();
      this.fx.flash('#ff4466', 0.4);
      this.shake(6);
      this.ui.updateHUD();
      if (this.lives <= 0) this.gameOver();
      else this.resetBall();
    }
  }

  updatePaddle(dt) {
    const p = this.paddle;
    const poll = this.input.poll();
    if (poll.launch) {
      if (this.balls.some(b => b.launchPending)) this.launch();
      else {
        // release sticky ball
        for (const b of this.balls) if (b.stuckTo) {
          b.stuckTo = null;
          b.setSpeed(Math.max(b.speed, BALL_BASE_SPEED));
          audio.paddleHit(0.5);
        }
      }
    }
    if (poll.mouse) {
      // mouse steers paddle toward pointer x/y (limited to paddle zone); CSS px -> device px
      const mx = poll.mouse.x * this.dpr, my = poll.mouse.y * this.dpr;
      const tx = Math.max(p.w / 2, Math.min(W - p.w / 2, this.wx(mx)));
      const tyZoneTop = H - PADDLE_ZONE;
      const ty = Math.max(tyZoneTop + p.h / 2, Math.min(H - p.h / 2 - 1, this.wy(my)));
      p.vx = Math.max(-PADDLE_SPEED * 1.6, Math.min(PADDLE_SPEED * 1.6, (tx - p.x) * 14));
      p.vy = Math.max(-PADDLE_SPEED * 1.6, Math.min(PADDLE_SPEED * 1.6, (ty - p.y) * 14));
    } else {
      const { x, y } = poll.dir;
      const ax = x * PADDLE_SPEED * 8, ay = -y * PADDLE_SPEED * 8; // screen y is down; +input = up
      p.vx += (ax - p.vx * PADDLE_FRICTION) * dt;
      p.vy += (ay - p.vy * PADDLE_FRICTION) * dt;
      if (x === 0) p.vx *= Math.max(0, 1 - PADDLE_FRICTION * dt * 1.5);
      if (y === 0) p.vy *= Math.max(0, 1 - PADDLE_FRICTION * dt * 1.5);
    }
    p.x += p.vx * dt; p.y += p.vy * dt;
    // clamp
    const half = p.w / 2;
    if (p.x < half + 1) { p.x = half + 1; p.vx = Math.max(0, p.vx); }
    if (p.x > W - half - 1) { p.x = W - half - 1; p.vx = Math.min(0, p.vx); }
    const zoneTop = H - PADDLE_ZONE + p.h / 2, zoneBot = H - p.h / 2 - 0.5;
    if (p.y < zoneTop) { p.y = zoneTop; p.vy = Math.max(0, p.vy); }
    if (p.y > zoneBot) { p.y = zoneBot; p.vy = Math.min(0, p.vy); }
    if (p.wideT > 0) { p.wideT -= dt; if (p.wideT <= 0) p.w = 20; }
    if (p.smashCharge > 0) p.smashCharge -= dt;

    // sticky-carry ball
    for (const b of this.balls) if (b.stuckTo) {
      b.x = p.x + b.stuckOffset; b.y = p.y - p.h / 2 - b.r - 0.2;
      b.trail.length = 0;
    }
  }

  updateBall(ball, dt) {
    if (ball.launchPending || ball.stuckTo) return;
    ball.prevX = ball.x; ball.prevY = ball.y;
    // magnet pull: gently steer toward nearest brick cluster when below bricks
    if (ball.type === 'magnet') {
      let best = null, bd = 1e9;
      for (const br of this.bricks) {
        if (!br.alive) continue;
        const d = Math.hypot(br.cx - ball.x, br.cy - ball.y);
        if (d < bd) { bd = d; best = br; }
      }
      if (best && bd > 2) {
        const nx = (best.cx - ball.x) / bd, ny = (best.cy - ball.y) / bd;
        const pull = 26 * dt;
        ball.vx += nx * pull; ball.vy += ny * pull;
        ball.setSpeed(ball.speed); // renormalize
      }
    }
    ball.x += ball.vx * dt; ball.y += ball.vy * dt;
    // speed boost powerup slowly decays extra speed
    if (this.speedBoostT > 0) this.speedBoostT -= dt;

    // trail
    ball.trail.push({ x: ball.x, y: ball.y });
    if (ball.trail.length > (this.rush > 0.5 ? 26 : 14)) ball.trail.shift();

    // walls
    if (ball.x < ball.r) { ball.x = ball.r; ball.vx = Math.abs(ball.vx); this.wallHit(ball); }
    if (ball.x > W - ball.r) { ball.x = W - ball.r; ball.vx = -Math.abs(ball.vx); this.wallHit(ball); }
    if (ball.y < ball.r) { ball.y = ball.r; ball.vy = Math.abs(ball.vy); this.wallHit(ball); }

    // bottom: miss
    if (ball.y > H + ball.r * 2) { this.removeBall(ball); return; }

    // paddle collision (moving paddle)
    this.paddleCollide(ball);

    // brick collisions
    if (ball.type !== 'ghost') this.brickCollide(ball);
    else {
      // ghost passes through bricks but still shatters std bricks it touches
      for (const br of this.bricks) {
        if (!br.alive || br.kind === 'armor' || br.kind === 'shield') continue;
        if (this.ballInBrick(ball, br)) { this.breakBrick(br, ball, 'top', true); }
      }
    }
  }

  wallHit(ball) {
    audio.wall();
    this.particles.burst(ball.x, ball.y, 5, { color: '#8891b8', speed: 18, grav: 0, size: 1.5 });
  }

  removeBall(ball) {
    const i = this.balls.indexOf(ball);
    if (i >= 0) this.balls.splice(i, 1);
    this.particles.burst(ball.x, Math.min(ball.y, H - 1), 16, { color: ['#ff6c8a', '#8891b8'], speed: 30 });
    if (this.balls.length === 0) { /* handled in update */ }
    else this.ui.updateHUD();
  }

  paddleCollide(ball) {
    const p = this.paddle;
    const hw = p.w / 2 + ball.r, hh = p.h / 2 + ball.r;
    if (Math.abs(ball.x - p.x) > hw || Math.abs(ball.y - p.y) > hh) return;
    if (ball.vy < 0 && ball.y < p.y) return; // only when coming down onto paddle region

    const off = Math.max(-1, Math.min(1, (ball.x - p.x) / (p.w / 2)));
    const movingUp = p.vy < -18;
    const horiz = Math.abs(p.vx) > 12;

    if (p.sticky) {
      ball.stuckTo = p; ball.stuckOffset = off * (p.w / 2 - 1);
      ball.vx = 0; ball.vy = 0;
      audio.paddleHit(0.3);
      this.particles.burst(ball.x, p.y - p.h, 8, { color: ['#ff6cc4', '#fff'], speed: 20 });
      return;
    }

    // base bounce: angle from offset, always upward
    const ang = off * 1.05; // radians from vertical, ~60deg max
    let speed = ball.speed;
    // SMASH: paddle moving up adds speed
    let smashed = false;
    if (movingUp) {
      const boost = 1 + Math.min(0.9, -p.vy / 90);
      speed = Math.min(BALL_MAX_SPEED, speed * boost + 6);
      smashed = true;
    }
    // english: horizontal paddle velocity skews the angle
    let a = -Math.PI / 2 + ang;
    if (horiz) a += Math.max(-0.5, Math.min(0.5, p.vx / 160));
    ball.vx = Math.cos(a) * speed; ball.vy = Math.sin(a) * speed;
    if (Math.abs(ball.vy) < 8) ball.vy = -8; // avoid ultra-flat
    ball.speed = Math.hypot(ball.vx, ball.vy);
    ball.y = p.y - hh - 0.1;

    audio.paddleHit(Math.min(1, (speed - BALL_BASE_SPEED) / 80));
    this.combo = 0; // paddle touch resets combo
    if (smashed) {
      audio.smash();
      p.smashCharge = 0.25;
      this.fx.whoosh(p.x, p.y - 3);
      this.particles.burst(p.x, p.y - p.h, 22, { color: ['#8fd4ff', '#fff', '#ffd76c'], speed: 46, grav: 60, glow: 1 });
      this.shake(3);
    } else {
      this.particles.burst(ball.x, p.y - p.h, 8, { color: ['#8fd4ff', '#fff'], speed: 22, grav: 40 });
    }
    this.ui.updateHUD();
  }

  ballInBrick(ball, br) {
    return ball.x + ball.r > br.x && ball.x - ball.r < br.x + br.w &&
           ball.y + ball.r > br.y && ball.y - ball.r < br.y + br.h;
  }

  brickCollide(ball) {
    for (const br of this.bricks) {
      if (!br.alive) continue;
      if (!this.ballInBrick(ball, br)) continue;
      const side = br.hitSide(ball.x, ball.y, ball.prevX, ball.prevY, ball.r);
      const verdict = br.canBreak(ball, side);

      if (br.kind === 'deflect') {
        // redirect at odd angle: reflect off diagonal
        const fromX = ball.x - br.cx, fromY = ball.y - br.cy;
        let ang = Math.atan2(ball.vy, ball.vx);
        ang = ang + (fromX >= 0 ? 1 : -1) * (1.1 + Math.random() * 0.5);
        ball.vx = Math.cos(ang) * ball.speed; ball.vy = Math.sin(ang) * ball.speed;
        br.flash = 0.2;
        audio.brick('deflect');
        this.particles.burst(br.cx, br.cy, 10, { color: ['#8cff9e', '#fff'], speed: 30, glow: 0.6 });
        this.addScore(10, br.cx, br.cy, false);
        // push ball out of brick
        ball.x = ball.prevX; ball.y = ball.prevY;
        return;
      }
      if (br.kind === 'prism') {
        const ang = (Math.random() - 0.5) * Math.PI * 1.4 + (ball.vy > 0 ? 0 : Math.PI);
        ball.vx = Math.cos(ang) * ball.speed; ball.vy = Math.sin(ang) * ball.speed;
        br.flash = 0.2;
        audio.brick('prism');
        this.particles.burst(br.cx, br.cy, 16, { color: ['#e0e0ff', '#6cf', '#f6a'], speed: 40, glow: 1 });
        this.addScore(15, br.cx, br.cy, false);
        ball.x = ball.prevX; ball.y = ball.prevY;
        return;
      }

      if (verdict.blocked) {
        // bounce off, no damage
        this.reflectBall(ball, br, side);
        br.flash = 0.15;
        audio.brick(verdict.reason === 'armor' ? 'armor' : 'shield');
        this.particles.burst(ball.x, ball.y, 6, { color: '#9aa7c8', speed: 20, grav: 30 });
        if (verdict.reason === 'shield' && !this.seenHints.has('shieldBlock')) {
          this.seenHints.add('shieldBlock');
          this.showHint('Blocked! That brick is shielded — hit it from the OPEN side.');
        }
        return;
      }

      // break it
      this.breakBrick(br, ball, side);
      if (ball.type !== 'pierce') {
        this.reflectBall(ball, br, side);
        return; // one brick per frame unless piercing
      }
    }
  }

  reflectBall(ball, br, side) {
    if (side === 'top') { ball.vy = Math.abs(ball.vy); ball.y = br.y - ball.r - 0.05; }
    else if (side === 'bottom') { ball.vy = -Math.abs(ball.vy); ball.y = br.y + br.h + ball.r + 0.05; }
    else if (side === 'left') { ball.vx = Math.abs(ball.vx); ball.x = br.x - ball.r - 0.05; }
    else { ball.vx = -Math.abs(ball.vx); ball.x = br.x + br.w + ball.r + 0.05; }
    // tiny speed nudge keeps game lively
    ball.setSpeed(ball.speed * 1.01);
  }

  breakBrick(br, ball, side, viaGhost = false) {
    br.alive = false;
    const cy = br.cy, cx = br.cx;
    const isSpecial = br.kind !== 'std';
    audio.brick(isSpecial ? (br.kind + 'Break') : 'std', this.combo);
    this.combo++;
    const pts = (isSpecial ? 30 : 10) * this.rushMult;
    this.addScore(pts, cx, cy, true);

    // particles: shatter
    const cols = [br.color, '#fff', br.color];
    this.particles.burst(cx, cy, this.rush > 0.5 ? 26 : 14, { color: cols, speed: 40 + this.rush * 30, glow: 0.8, decay: 1.4 });
    this.shake(isSpecial ? 2.5 : 1.2 + this.rush * 2);
    if (this.rush > 0.5) this.hitStop = Math.min(0.05, 0.02 + this.rush * 0.03);

    // bomb chain
    if (br.kind === 'bomb' || (this.bomberArmed && br.kind !== 'armor')) {
      if (this.bomberArmed && br.kind !== 'bomb') { /* armed shot explodes too */ }
      this.explode(cx, cy, ball);
      if (this.bomberArmed) { this.bomberArmed = false; this.bomberT = 0; this.ui.updateHUD(); }
    }

    // regen brick: schedule neighbor awareness (regen handled in updateBricks)

    // powerup drop
    if (Math.random() < POWERUP_DROP_CHANCE + (this.rush > 0.5 ? 0.1 : 0)) {
      const kinds = Object.keys(PU_META);
      const kind = kinds[(Math.random() * kinds.length) | 0];
      this.powerups.push(new PowerUp(cx, cy, kind));
    }

    // hints first time
    if (isSpecial && !this.seenHints.has(br.kind)) {
      this.seenHints.add(br.kind);
      this.showHint(BRICK_HINTS[br.kind] || '');
    }

    // level clear?
    if (this.bricks.every(b => !b.alive)) this.levelClear();
  }

  explode(x, y, ball) {
    audio.brick('bomb');
    this.fx.blast(x, y);
    this.shake(6);
    this.hitStop = 0.06;
    const R = 12;
    for (const br of this.bricks) {
      if (!br.alive) continue;
      const d = Math.hypot(br.cx - x, br.cy - y);
      if (d < R) {
        if (br.kind === 'bomb') {
          br.alive = false;
          this.addScore(30 * this.rushMult, br.cx, br.cy, true);
          this.particles.burst(br.cx, br.cy, 20, { color: ['#ffae42', '#fff'], speed: 44, glow: 1 });
          // chain with slight delay via setTimeout-free recursion
          setTimeout(() => this.explode(br.cx, br.cy, ball), 70);
        } else if (br.kind !== 'armor') {
          br.alive = false;
          this.addScore(10 * this.rushMult, br.cx, br.cy, true);
          this.particles.burst(br.cx, br.cy, 14, { color: [br.color, '#fff'], speed: 34 });
        }
      }
    }
    this.particles.burst(x, y, 40, { color: ['#ffae42', '#ff6c3d', '#fff'], speed: 70, glow: 1, decay: 1.1 });
    if (this.bricks.every(b => !b.alive)) this.levelClear();
  }

  addScore(n, x, y, float) {
    this.score += Math.round(n);
    if (float && x != null) {
      this.particles.text(x, y, '+' + Math.round(n), this.rush > 0.5 ? '#ffd76c' : '#cfeaff', this.rush > 0.5 ? 6 : 4.6);
    }
    this.ui.updateHUD();
  }

  // ---------- RUSH / BREAKTHROUGH ----------
  ballIsAboveWall(ball) {
    // above the top of the brick field AND moving around up there
    return ball.y < BRICK_TOP - 1 && !ball.launchPending;
  }

  updateRush(dt) {
    const above = this.balls.some(b => this.ballIsAboveWall(b));
    if (above) {
      const wasZero = this.rush === 0;
      this.rushTime += dt;
      this.rush = Math.min(1, this.rushTime / RUSH_RAMP_TIME);
      if (wasZero) {
        audio.rushStart();
        this.fx.flash('#c86cff', 0.5);
        this.showHint('BREAKTHROUGH! The ball is ON TOP — RUSH multiplier compounding!');
      }
      const prev = this.rushMult;
      this.rushMult = Math.min(RUSH_MULT_CAP, 1 + Math.floor(this.rushTime * 2.5) * (1 + Math.floor(this.rushTime / 8)));
      if (this.rushMult > prev) {
        audio.rushTick(this.rushMult);
        this.particles.text(W / 2, BRICK_TOP - 6, 'x' + this.rushMult + ' RUSH!', '#ffd76c', 7);
        this.fx.flash('#ffd76c', 0.2);
      }
      audio.rush = this.rush;
      // particle rain from the top when frenzied
      if (this.rush > 0.3 && Math.random() < this.rush * 0.5) {
        this.particles.spawn({
          x: Math.random() * W, y: -2, vx: (Math.random() - 0.5) * 6, vy: 30 + Math.random() * 30,
          life: 1, decay: 0.8, size: 1.6, color: ['#ffd76c', '#f6a', '#6cf'][(Math.random() * 3) | 0], grav: 20, glow: 0.7,
        });
      }
    } else {
      if (this.rush > 0) {
        // rush ends: convert accumulated mult into a final bonus and reset
        const bonus = this.rushMult * 25;
        this.score += bonus;
        this.particles.text(W / 2, H * 0.45, 'RUSH END  +' + bonus, '#8891b8', 5.5);
        audio.rush = 0;
      }
      this.rush = 0; this.rushTime = 0; this.rushMult = 1;
    }
    this.ui.updateRush(this.rush, this.rushMult);
  }

  updatePowerups(dt) {
    const p = this.paddle;
    for (let i = this.powerups.length - 1; i >= 0; i--) {
      const u = this.powerups[i];
      u.vy = POWERUP_FALL; u.y += u.vy * dt; u.wobble += dt * 4;
      if (u.y > H + 4) { this.powerups.splice(i, 1); continue; }
      // catch
      if (Math.abs(u.x - p.x) < p.w / 2 + u.r && Math.abs(u.y - p.y) < p.h / 2 + u.r + 1.5) {
        this.powerups.splice(i, 1);
        this.applyPowerup(u.kind);
      }
    }
  }

  applyPowerup(kind) {
    audio.powerup(kind);
    const p = this.paddle;
    this.particles.burst(p.x, p.y - 4, 18, { color: [PU_META[kind].color, '#fff'], speed: 40, glow: 1 });
    this.particles.text(p.x, p.y - 8, PU_META[kind].label, PU_META[kind].color, 5.5);
    switch (kind) {
      case 'multi': {
        const src = this.balls[0];
        if (src) for (let i = 0; i < 2; i++) {
          const nb = new Ball(src.x, src.y, 0, 0);
          nb.type = src.type;
          const a = Math.atan2(src.vy, src.vx) + (i ? 0.6 : -0.6);
          nb.vx = Math.cos(a) * src.speed; nb.vy = Math.sin(a) * src.speed;
          nb.speed = src.speed;
          nb.launchPending = false;
          this.balls.push(nb);
        }
        break;
      }
      case 'fire': this.forEachBall(b => b.type = 'fire'); break;
      case 'pierce': this.forEachBall(b => b.type = 'pierce'); break;
      case 'sticky': p.sticky = true; this.forEachBall(b => b.type = 'sticky'); break;
      case 'wide': p.w = 32; p.wideT = 14; break;
      case 'slow': this.slowT = SLOWMO_TIME; break;
      case 'life': this.lives++; break;
      case 'speed': this.forEachBall(b => b.setSpeed(Math.min(BALL_MAX_SPEED, b.speed * 1.35))); this.speedBoostT = 6; break;
      case 'magnet': this.forEachBall(b => b.type = 'magnet'); break;
      case 'ghost': this.forEachBall(b => b.type = 'ghost'); this.ghostT = 6; break;
      case 'bombP': this.bomberArmed = true; this.bomberT = 999; break;
    }
    // clear ball-type powerups after a while (except sticky on paddle)
    if (['fire','pierce','sticky','magnet','ghost'].includes(kind)) {
      clearTimeout(this._typeT);
      this._typeT = setTimeout(() => this.forEachBall(b => b.type = 'normal'), 12000);
    }
    this.ui.updateHUD();
  }

  forEachBall(fn) { this.balls.forEach(fn); }

  updateBricks(dt) {
    for (const br of this.bricks) {
      if (br.flash > 0) br.flash -= dt;
      if (!br.alive && br.kind === 'regen') {
        br.regenT += dt;
        if (br.regenT > 6) { br.alive = true; br.regenT = 0; this.particles.burst(br.cx, br.cy, 10, { color: ['#c86cff', '#fff'], speed: 20 }); }
      }
      if (!br.alive) continue;
      if (br.kind === 'mover') {
        br.moveT += dt * br.moveSpeed;
        if (br.moveAxis === 'x') br.x = br.baseX + Math.sin(br.moveT) * br.moveAmp;
        else br.y = br.baseY + Math.sin(br.moveT) * br.moveAmp * 0.6;
      }
    }
  }

  // ---------- flow ----------
  levelClear() {
    if (this.state !== 'play') return;
    this.state = 'levelclear';
    audio.levelClear();
    const bonus = 100 * (this.level + 1) + this.lives * 50;
    this.score += bonus;
    this.ui.showLevelClear(this.level, bonus);
    this.saveBest();
  }

  nextLevel() { this.startLevel(this.level + 1); this.state = 'play'; this.ui.showPlay(); }

  gameOver() {
    this.state = 'over';
    audio.loseBall();
    this.ui.showGameOver(this.score, this.saveBest());
  }

  saveBest() {
    if (this.score > this.best) { this.best = this.score; localStorage.setItem('bt_best', String(this.best)); }
    return this.best;
  }

  showHint(str) {
    this.hintT = 4;
    this.ui.showHint(str);
  }

  pause() { if (this.state === 'play') { this.state = 'pause'; this.ui.showPause(); } }
  resume() { if (this.state === 'pause') { this.state = 'play'; this.ui.showPlay(); } }

  // ---------- rendering ----------
  render() {
    const ctx = this.ctx;
    ctx.setTransform(1, 0, 0, 1, 0, 0);
    // background
    this.fx.drawBG(ctx, this.vw * this.dpr, this.vh * this.dpr, this.time, this.rush);
    // shake offset
    const sh = this.fx.shakeOffset();
    ctx.setTransform(this.scale, 0, 0, this.scale, this.offX + sh.x * this.scale, this.offY + sh.y * this.scale);

    this.drawWalls(ctx);
    this.drawBricks(ctx);
    this.drawPowerups(ctx);
    this.drawPaddle(ctx);
    this.drawBalls(ctx);
    this.particles.draw(ctx, 1); // scale already in transform
    this.fx.drawOverlays(ctx, W, H, this.rush, this.time);
  }

  drawWalls(ctx) {
    ctx.fillStyle = '#2a3050';
    ctx.fillRect(-2, -2, W + 4, 2);          // top
    ctx.fillRect(-2, 0, 2, H);               // left
    ctx.fillRect(W, 0, 2, H);                // right
    // rush glow line at brick top
    if (this.rush > 0) {
      ctx.save();
      ctx.globalAlpha = 0.35 + this.rush * 0.4;
      ctx.strokeStyle = '#ffd76c'; ctx.lineWidth = 0.5;
      ctx.shadowColor = '#ffd76c'; ctx.shadowBlur = 12;
      ctx.beginPath(); ctx.moveTo(0, BRICK_TOP); ctx.lineTo(W, BRICK_TOP); ctx.stroke();
      ctx.restore();
    }
  }

  drawBricks(ctx) {
    for (const br of this.bricks) {
      if (!br.alive) {
        if (br.kind === 'regen' && br.regenT > 3.5) {
          ctx.globalAlpha = 0.25 + 0.15 * Math.sin(this.time * 6);
          ctx.strokeStyle = br.color; ctx.lineWidth = 0.4;
          ctx.strokeRect(br.x, br.y, br.w, br.h);
          ctx.globalAlpha = 1;
        }
        continue;
      }
      ctx.save();
      let c = br.color;
      if (br.flash > 0) c = '#fff';
      ctx.fillStyle = c;
      ctx.shadowColor = c; ctx.shadowBlur = 6 + (this.rush > 0.4 ? 6 : 0);
      if (br.kind === 'phase') {
        // dashed outline when ball is too slow: visual cue
        const fast = this.balls.some(b => b.speed >= br.phaseSpeed);
        ctx.globalAlpha = fast ? 1 : 0.55 + 0.15 * Math.sin(this.time * 5 + br.wobble);
      }
      if (br.kind === 'armor') {
        ctx.fillRect(br.x, br.y, br.w, br.h);
        ctx.fillStyle = '#5a6580';
        ctx.fillRect(br.x + 1, br.y + 1, br.w - 2, br.h - 2);
        ctx.strokeStyle = br.color; ctx.lineWidth = 0.5;
        ctx.strokeRect(br.x + 1, br.y + 1, br.w - 2, br.h - 2);
      } else {
        ctx.fillRect(br.x, br.y, br.w, br.h);
      }
      // type glyphs
      ctx.shadowBlur = 0;
      ctx.fillStyle = 'rgba(0,0,0,0.5)';
      if (br.kind === 'shield') {
        const sx = br.shieldSide;
        ctx.globalAlpha = 0.85;
        ctx.fillStyle = '#dffcf4';
        if (sx === 'top') ctx.fillRect(br.x, br.y, br.w, 1);
        else if (sx === 'bottom') ctx.fillRect(br.x, br.y + br.h - 1, br.w, 1);
        else if (sx === 'left') ctx.fillRect(br.x, br.y, 1, br.h);
        else ctx.fillRect(br.x + br.w - 1, br.y, 1, br.h);
      } else if (br.kind === 'phase') {
        ctx.font = `bold ${2.6}px system-ui`; ctx.textAlign = 'center';
        ctx.fillText('⚡', br.cx, br.cy + 1);
      } else if (br.kind === 'deflect') {
        ctx.font = `bold ${2.6}px system-ui`; ctx.textAlign = 'center';
        ctx.fillText('⤡', br.cx, br.cy + 1);
      } else if (br.kind === 'bomb') {
        ctx.font = `bold ${2.6}px system-ui`; ctx.textAlign = 'center';
        ctx.fillText('✸', br.cx, br.cy + 1);
      } else if (br.kind === 'mover') {
        ctx.font = `bold ${2.6}px system-ui`; ctx.textAlign = 'center';
        ctx.fillText('↔', br.cx, br.cy + 1);
      } else if (br.kind === 'prism') {
        ctx.font = `bold ${2.4}px system-ui`; ctx.textAlign = 'center';
        ctx.fillText('◇', br.cx, br.cy + 1);
      } else if (br.kind === 'regen') {
        ctx.globalAlpha = 0.5; ctx.fillStyle = '#fff';
        ctx.font = `bold ${2.4}px system-ui`; ctx.textAlign = 'center';
        ctx.fillText('♻', br.cx, br.cy + 1);
      }
      ctx.restore();
    }
    ctx.globalAlpha = 1;
  }

  drawPowerups(ctx) {
    for (const u of this.powerups) {
      ctx.save();
      const bob = Math.sin(u.wobble) * 0.8;
      ctx.translate(u.x, u.y + bob);
      ctx.shadowColor = u.color; ctx.shadowBlur = 10;
      ctx.fillStyle = u.color;
      ctx.globalAlpha = 0.9;
      ctx.beginPath(); ctx.arc(0, 0, u.r, 0, Math.PI * 2); ctx.fill();
      ctx.shadowBlur = 0;
      ctx.fillStyle = '#07070f';
      ctx.font = `bold ${2.4}px system-ui`; ctx.textAlign = 'center';
      ctx.fillText(u.glyph, 0, 0.9);
      ctx.restore();
    }
    ctx.globalAlpha = 1;
  }

  drawPaddle(ctx) {
    const p = this.paddle;
    ctx.save();
    const glow = 8 + p.smashCharge * 60 + this.rush * 10;
    ctx.shadowColor = p.sticky ? '#ff6cc4' : '#8fd4ff';
    ctx.shadowBlur = glow;
    ctx.fillStyle = p.sticky ? '#ff6cc4' : (p.smashCharge > 0 ? '#fff' : '#8fd4ff');
    const r = 1.2;
    const x = p.x - p.w / 2, y = p.y - p.h / 2, w = p.w, h = p.h;
    ctx.beginPath();
    ctx.moveTo(x + r, y);
    ctx.arcTo(x + w, y, x + w, y + h, r);
    ctx.arcTo(x + w, y + h, x, y + h, r);
    ctx.arcTo(x, y + h, x, y, r);
    ctx.arcTo(x, y, x + w, y, r);
    ctx.fill();
    ctx.restore();
  }

  drawBalls(ctx) {
    for (const b of this.balls) {
      const col = b.color;
      // trail
      ctx.save();
      for (let i = 0; i < b.trail.length; i++) {
        const t = b.trail[i];
        const a = (i / b.trail.length) * (0.5 + this.rush * 0.5);
        ctx.globalAlpha = a * 0.6;
        ctx.fillStyle = col;
        const s = b.r * (i / b.trail.length);
        ctx.beginPath();
        ctx.arc(t.x, t.y, Math.max(0.2, s * 0.9), 0, Math.PI * 2);
        ctx.fill();
      }
      ctx.restore();
      // ball
      ctx.save();
      ctx.shadowColor = col; ctx.shadowBlur = 10 + this.rush * 14;
      ctx.fillStyle = col;
      ctx.beginPath(); ctx.arc(b.x, b.y, b.r, 0, Math.PI * 2); ctx.fill();
      // launch indicator
      if (b.launchPending) {
        ctx.strokeStyle = '#ffd76c'; ctx.lineWidth = 0.4;
        ctx.globalAlpha = 0.5 + 0.4 * Math.sin(this.time * 6);
        ctx.beginPath(); ctx.moveTo(b.x, b.y);
        ctx.lineTo(b.x, b.y - 8); ctx.stroke();
        ctx.font = `bold ${2.6}px system-ui`; ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
        ctx.fillStyle = '#ffd76c';
        const tx = Math.max(12, Math.min(W - 12, b.x));
        const ty = (b.y - 12 < 8) ? Math.max(b.y + 10, 16) : b.y - 12;
        ctx.fillText('TAP / SPACE', tx, ty);
      }
      ctx.restore();
    }
    ctx.globalAlpha = 1;
  }

  shake(n) { this.fx.shake(n); }
  setFrenzyBg() {}
}