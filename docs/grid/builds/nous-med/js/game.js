// Game state & simulation for BREAKTHROUGH.
import { W, H, OZONE_H, FIELD_TOP, FIELD_LEFT, ZONE_TOP, ZONE_BOT,
  BRICK_COLS, BRICK_W, BRICK_H, BRICK_GAP, PADDLE_W, PADDLE_H,
  BALL_R, BALL_SPEED_MIN, BALL_SPEED_MAX, SMASH_BONUS, PADDLE_UP_SMASH_V,
  CHAOS_DECAY_TIME, CHAOS_CAP, CHAOS_DOUBLING, DROP_CHANCE, DROP_TABLE, BRICK_STYLE, PT_STYLE } from './constants.js';
import { getLevel } from './levels.js';
import { Brick, Paddle, Ball, Capsule } from './entities.js';
import { FX } from './fx.js';
import { Audio } from './audio.js';
import { Render } from './render.js';

export class Game {
  constructor(canvas) {
    this.canvas = canvas;
    this.ctx = canvas.getContext('2d');
    this.fx = new FX();
    this.audio = new Audio();
    this.render = new Render(this.ctx, this);
    this.scale = 1; this.offX = 0; this.offY = 0;
    this.best = +(localStorage.getItem('bt_best') || 0);
    this.maxLives = 3;
    this.reset();
    this.resize();
    addEventListener('resize', () => this.resize());
    // starfield
    this.stars = Array.from({ length: 70 }, () => ({
      x: Math.random() * W, y: Math.random() * H, tw: 1 + Math.random() * 3, ph: Math.random() * 7,
    }));
    this.last = performance.now();
    requestAnimationFrame(t => this.frame(t));
  }

  reset() {
    this.state = 'title'; this.stateTimer = 0;
    this.score = 0; this.lives = this.maxLives;
    this.levelIndex = 0; this.levelName = '';
    this.bricks = []; this.balls = []; this.capsules = [];
    this.paddle = new Paddle();
    this.powerTimers = { M: 0, F: 0, H: 0, W: 0, S: 0, G: 0, D: 0 };
    this.shield = false;
    this.combo = 0; this.comboTimer = 0;
    this.chaosOn = false; this.chaosMult = 1; this.chaosTime = 0;
    this.chaosDecay = 0; this.chaosDecayMax = CHAOS_DECAY_TIME;
    this.topBalls = 0;
    this.time = 0;
    this.slowmoT = 0;
    this.launchedOnce = false;
    this.levelSpeedRamp = 0;
  }

  resize() {
    const dpr = Math.min(2, devicePixelRatio || 1);
    const availW = innerWidth, availH = innerHeight;
    const s = Math.min(availW / W, availH / H);
    this.scale = s;
    this.canvas.style.width = (W * s) + 'px';
    this.canvas.style.height = (H * s) + 'px';
    this.canvas.width = W * s * dpr;
    this.canvas.height = H * s * dpr;
    this.ctx.setTransform(s * dpr, 0, 0, s * dpr, 0, 0);
    const r = this.canvas.getBoundingClientRect();
    this.offX = r.left; this.offY = r.top;
  }

  pixelsPerPx() { return this.scale; }
  toWorld(cx, cy) { return { x: (cx - this.offX) / this.scale, y: (cy - this.offY) / this.scale }; }

  pointerMove(x, y) {
    if (this.state !== 'playing' || this.paddle.magnet) return;
    this.movePaddleTo(x, y);
  }
  pointerRel(dx, dy) {
    if (this.state !== 'playing' || this.paddle.magnet) return;
    this.movePaddleTo(this.paddle.cx + dx, this.paddle.cy + dy);
  }
  movePaddleTo(x, y) {
    const p = this.paddle;
    const nx = Math.max(p.w / 2, Math.min(W - p.w / 2, x)) - p.w / 2;
    const ny = Math.max(ZONE_TOP, Math.min(ZONE_BOT - p.h, y)) - p.h / 2;
    p.vx = (nx - p.x) / 0.016; p.vy = (ny - p.y) / 0.016;
    p.x = nx; p.y = ny;
  }

  actionPress() {
    this.audio.ensure();
    if (this.state === 'title') { this.startGame(); return; }
    if (this.state === 'gameover') { this.reset(); this.startGame(); return; }
    if (this.state !== 'playing') return;
    // launch stuck/serve ball, or release magnet
    for (const b of this.balls) {
      if (b.stuck) {
        b.stuck = false;
        const p = this.paddle;
        const a = -Math.PI / 2 + (b.x - p.cx) / p.w * 1.2;
        const sp = this.targetSpeed();
        b.vx = Math.cos(a) * sp; b.vy = Math.sin(a) * sp;
        this.audio.launch();
        this.fx.spawn(b.x, b.y, { n: 10, color: '#ffd166', speed: 150 });
        return;
      }
    }
  }

  startGame() {
    this.score = 0; this.lives = this.maxLives; this.levelIndex = 0;
    this.loadLevel(0);
    this.state = 'levelintro'; this.stateTimer = 0;
    this.audio.levelStart();
  }

  loadLevel(i) {
    this.levelIndex = i;
    const lvl = getLevel(i);
    this.levelName = lvl.name;
    this.bricks = [];
    let rows = lvl.rows;
    for (let r = 0; r < rows.length; r++) {
      for (let c = 0; c < Math.min(BRICK_COLS, rows[r].length); c++) {
        const ch = rows[r][c];
        if (ch === '.' || !BRICK_STYLE[ch]) continue;
        this.bricks.push(new Brick(ch, c, r,
          FIELD_LEFT + c * (BRICK_W + BRICK_GAP),
          FIELD_TOP + r * (BRICK_H + BRICK_GAP)));
      }
    }
    this.levelSpeedRamp = Math.min(200, i * 25);
    this.resetBallServe();
    this.capsules = [];
    this.powerTimers = { M: 0, F: 0, H: 0, W: 0, S: 0, G: 0, D: 0 };
    this.shield = false;
    this.paddle.w = PADDLE_W;
    this.paddle.magnet = false;
    this.combo = 0;
    this.chaosOn = false; this.chaosMult = 1; this.chaosTime = 0; this.chaosDecay = 0;
    this.audio.stopBeat(); this.audio.setIntensity(0);
  }

  targetSpeed() { return Math.min(BALL_SPEED_MAX, BALL_SPEED_MIN + 60 + this.levelSpeedRamp); }

  resetBallServe() {
    this.balls = [new Ball(this.paddle.cx, this.paddle.y - BALL_R - 2, 0, 0)];
    this.balls[0].stuck = true;
  }

  togglePause() {
    if (this.state === 'playing') { this.state = 'paused'; this.audio.stopBeat(); }
    else if (this.state === 'paused') { this.state = 'playing'; }
  }
  autoPause() { if (this.state === 'playing') { this.state = 'paused'; this.audio.stopBeat(); } }

  // ------- main loop -------
  frame(t) {
    let dt = Math.min(0.033, (t - this.last) / 1000);
    this.last = t;
    if (this.fx.hitStop > 0) dt *= 0.05;
    if (this.slowmoT > 0) dt *= 0.45;
    this.update(dt);
    this.render.draw(dt);
    requestAnimationFrame(tt => this.frame(tt));
  }

  update(dt) {
    this.time += dt;
    this.stateTimer += dt;
    if (this.state === 'title' || this.state === 'gameover' || this.state === 'paused') {
      this.fx.update(dt);
      return;
    }
    if (this.state === 'levelintro') {
      if (this.stateTimer > 2) { this.state = 'playing'; this.stateTimer = 0; }
      this.fx.update(dt);
      return;
    }
    if (this.state === 'clear') {
      if (this.stateTimer > 2.2) { this.loadLevel(this.levelIndex + 1); this.state = 'levelintro'; this.stateTimer = 0; this.audio.levelStart(); }
      this.fx.update(dt); this.updateBalls(dt * 0.3); return;
    }

    // keyboard paddle
    const { ax, ay } = this.input ? this.input.getAxes() : { ax: 0, ay: 0 };
    if (ax || ay) {
      const p = this.paddle;
      const nx = p.cx + ax * 900 * dt, ny = p.cy + ay * 700 * dt;
      this.movePaddleTo(nx, ny);
    } else {
      this.paddle.vx *= Math.pow(0.0001, dt); this.paddle.vy *= Math.pow(0.0001, dt);
    }

    // powerup timers
    for (const k of Object.keys(this.powerTimers)) {
      if (this.powerTimers[k] > 0) {
        this.powerTimers[k] -= dt;
        if (this.powerTimers[k] <= 0) this.expirePower(k);
      }
    }
    if (this.slowmoT > 0) this.slowmoT -= dt / 0.45; // compensate dt scaling
    this.comboTimer -= dt;
    if (this.comboTimer <= 0 && this.combo > 0) this.combo = 0;

    this.updateBricks(dt);
    this.updateBalls(dt);
    this.updateCapsules(dt);
    this.updateChaos(dt);
    this.fx.update(dt);
  }

  expirePower(k) {
    if (k === 'W') this.paddle.w = PADDLE_W;
    if (k === 'G') { this.paddle.magnet = false; for (const b of this.balls) b.stuck = false; }
    this.audio.tink();
  }

  // ------- chaos -------
  updateChaos(dt) {
    let onTop = 0;
    for (const b of this.balls) if (b.y - b.r < this.brickTopY()) onTop++;
    const wasOn = this.chaosOn;
    this.chaosOn = onTop > 0;
    if (this.chaosOn) {
      this.chaosDecay = 0;
      this.chaosTime += dt;
      const m = Math.min(CHAOS_CAP, Math.pow(2, Math.floor(this.chaosTime / CHAOS_DOUBLING)));
      if (m > this.chaosMult) {
        this.chaosMult = m;
        this.fx.popup(W / 2, FIELD_TOP - 60, 'CHAOS ×' + m, '#ffe14d', true);
        this.fx.doFlash('#ffe14d', 0.15);
        this.fx.doShake(4);
      }
      this.audio.startBeat();
      this.audio.setIntensity(Math.log2(this.chaosMult) / Math.log2(CHAOS_CAP));
    } else if (wasOn || this.chaosMult > 1) {
      if (this.chaosMult > 1) {
        this.chaosDecay += dt;
        if (this.chaosDecay >= CHAOS_DECAY_TIME) {
          this.chaosMult = 1; this.chaosTime = 0; this.chaosDecay = 0;
          this.audio.stopBeat(); this.audio.setIntensity(0);
          this.fx.popup(W / 2, FIELD_TOP - 40, 'CHAOS LOST', '#ff2d78');
        }
      }
      this.audio.setIntensity(this.chaosMult > 1 ? 0.15 : 0);
    }
  }

  brickTopY() {
    let top = Infinity;
    for (const b of this.bricks) if (b.alive && b.type !== 'T') top = Math.min(top, b.y);
    return top === Infinity ? FIELD_TOP : top;
  }

  // ------- balls -------
  updateBalls(dt) {
    const p = this.paddle;
    for (let i = this.balls.length - 1; i >= 0; i--) {
      const b = this.balls[i];
      if (b.stuck) {
        b.x = p.cx + b.stuckDx; b.y = p.y - b.r - 2;
        continue;
      }
      // vortex pull
      for (const br of this.bricks) {
        if (br.alive && br.pull) {
          const dx = br.cx - b.x, dy = br.cy - b.y, d = Math.hypot(dx, dy);
          if (d < 260 && d > 1) {
            const f = 220 * (1 - d / 260) * dt;
            b.vx += dx / d * f; b.vy += dy / d * f;
          }
        }
      }
      // speed clamp
      const sp = b.speed;
      const base = this.targetSpeed();
      const max = BALL_SPEED_MAX, min = base;
      if (sp > max) { b.vx *= max / sp; b.vy *= max / sp; }
      else if (sp < min && sp > 0) { b.vx *= min / sp; b.vy *= min / sp; }
      // substep for fast balls
      const steps = Math.ceil(b.speed * dt / 8);
      for (let s = 0; s < steps; s++) this.stepBall(b, dt / steps);
      // trail
      b.trail.push({ x: b.x, y: b.y });
      const maxTrail = this.chaosOn ? 14 + Math.min(14, Math.log2(this.chaosMult) * 3) : 8;
      while (b.trail.length > maxTrail) b.trail.shift();
      // lost
      if (b.y - b.r > H + 20) {
        if (this.shield) {
          this.shield = false; this.powerTimers.D = 0;
          b.y = H - 20; b.vy = -Math.abs(b.vy);
          this.fx.doFlash('#c77dff', 0.3); this.audio.power();
        } else {
          this.balls.splice(i, 1);
        }
      }
    }
    if (this.balls.length === 0 && this.state === 'playing') this.loseLife();
  }

  stepBall(b, dt) {
    b.x += b.vx * dt; b.y += b.vy * dt;
    // walls
    if (b.x - b.r < 0) { b.x = b.r; b.vx = Math.abs(b.vx); this.audio.wall(); this.fx.spawn(0, b.y, { n: 4, color: '#00f6ff', speed: 100 }); }
    if (b.x + b.r > W) { b.x = W - b.r; b.vx = -Math.abs(b.vx); this.audio.wall(); this.fx.spawn(W, b.y, { n: 4, color: '#00f6ff', speed: 100 }); }
    if (b.y - b.r < 0) { b.y = b.r; b.vy = Math.abs(b.vy); this.audio.wall(); this.fx.spawn(b.x, 0, { n: 4, color: '#8f6bff', speed: 100 }); }
    this.ballBrickCollide(b);
    this.ballPaddleCollide(b);
  }

  ballPaddleCollide(b) {
    const p = this.paddle;
    if (b.vy <= 0) return;
    if (b.y + b.r < p.y || b.y - b.r > p.y + p.h) return;
    if (b.x < p.x - b.r || b.x > p.x + p.w + b.r) return;
    b.y = p.y - b.r;
    const rel = (b.x - p.cx) / (p.w / 2); // -1..1
    const ang = -Math.PI / 2 + rel * 1.05;
    let sp = Math.max(this.targetSpeed(), Math.min(BALL_SPEED_MAX, b.speed));
    let smash = false;
    if (p.vy < -PADDLE_UP_SMASH_V * 0.4) {
      const boost = SMASH_BONUS * Math.min(1, -p.vy / PADDLE_UP_SMASH_V);
      sp = Math.min(BALL_SPEED_MAX, sp + boost);
      smash = true;
      this.audio.smash();
      this.fx.doHitStop(0.06);
      this.fx.doFlash('#ff2d78', 0.25);
      this.fx.doShake(8);
      this.fx.spawn(b.x, p.y, { n: 24, color: '#ff2d78', speed: 420, glow: true });
      this.fx.popup(b.x, p.y - 30, 'SMASH!', '#ff2d78', true);
    } else {
      if (p.vy > 100) sp = Math.max(BALL_SPEED_MIN * 0.9, sp - 80); // soften
      this.audio.paddle(p.vy);
      this.fx.spawn(b.x, p.y, { n: 6, color: '#00f6ff', speed: 180 });
    }
    if (this.powerTimers.S > 0) sp *= 0.75;
    b.vx = Math.cos(ang) * sp + p.vx * 0.12;
    b.vy = Math.sin(ang) * sp;
    this.combo = 0;
    if (this.paddle.magnet) {
      b.stuck = true; b.stuckDx = b.x - p.cx; b.vx = 0; b.vy = 0;
      b.trail.length = 0;
    }
  }

  ballBrickCollide(b) {
    for (const br of this.bricks) {
      if (!br.alive) continue;
      if (br.type === 'G' && !br.solid) continue;
      const hx = b.x - Math.max(br.x, Math.min(b.x, br.x + br.w));
      const hy = b.y - Math.max(br.y, Math.min(b.y, br.y + br.h));
      if (hx * hx + hy * hy > b.r * b.r) continue;
      // resolve bounce axis
      const overL = b.x + b.r - br.x, overR = br.x + br.w - (b.x - b.r);
      const overT = b.y + b.r - br.y, overB = br.y + br.h - (b.y - b.r);
      const minO = Math.min(overL, overR, overT, overB);
      const firePass = b.fire > 0 && br.type !== 'T' && br.type !== 'A';
      const phasePass = b.phase > 0 && br.type !== 'T';
      if (!firePass && !phasePass) {
        if (minO === overT) { b.vy = -Math.abs(b.vy); b.y = br.y - b.r; }
        else if (minO === overB) { b.vy = Math.abs(b.vy); b.y = br.y + br.h + b.r; }
        else if (minO === overL) { b.vx = -Math.abs(b.vx); b.x = br.x - b.r; }
        else { b.vx = Math.abs(b.vx); b.x = br.x + br.w + b.r; }
      }
      this.hitBrick(br, b, minO === overT || minO === overB);
      if (firePass || phasePass) continue; // no bounce, plow through
      break;
    }
  }

  hitBrick(br, ball, verticalHit) {
    const st = BRICK_STYLE[br.type];
    const speed = ball.speed;
    if (br.type === 'T') {
      br.hitFx = 0.2;
      this.audio.clang();
      this.fx.spawn(ball.x, ball.y, { n: 5, color: '#8fa8d8', speed: 160 });
      return;
    }
    if (br.type === 'A' && speed < BALL_SPEED_MIN + 120 + this.levelSpeedRamp * 0.5) {
      br.hitFx = 0.3;
      this.audio.clang();
      this.fx.spawn(ball.x, ball.y, { n: 12, color: '#cfd8ff', speed: 260, glow: true, dir: Math.atan2(ball.y - br.cy, ball.x - br.cx) * -1 });
      this.fx.popup(br.cx, br.y - 6, 'TOO SLOW', '#8fa8d8');
      return;
    }
    if (br.type === 'P' && !verticalHit) {
      br.hitFx = 0.3;
      this.audio.tink();
      this.fx.spawn(ball.x, ball.y, { n: 8, color: '#ffd166', speed: 200, glow: true });
      this.fx.popup(br.cx, br.y - 6, 'NEED ANGLE', '#ffd166');
      return;
    }
    this.breakBrick(br, ball);
  }

  breakBrick(br, causeBall, chain = 0) {
    if (!br.alive) return;
    br.alive = false;
    const st = BRICK_STYLE[br.type];
    this.combo++;
    this.comboTimer = 2.2;
    const mult = this.chaosMult * (1 + Math.min(20, this.combo) * 0.1);
    const pts = Math.round(st.pts * mult);
    this.score += pts;
    if (this.score > this.best) { this.best = this.score; localStorage.setItem('bt_best', this.best); }
    this.audio.brick(this.combo, this.chaosMult);
    this.fx.spawn(br.cx, br.cy, { n: chain > 0 ? 10 : 16, color: st.color, speed: 320, grav: 500, glow: true });
    this.fx.spawn(br.cx, br.cy, { n: 6, color: '#fff', speed: 180 });
    this.fx.popup(br.cx, br.y, '+' + pts, chain > 0 ? '#ffe14d' : '#eafcff');
    this.fx.doShake(2 + Math.min(6, chain * 2));
    if (this.chaosOn) {
      this.fx.spawn(br.cx, FIELD_TOP + 10, { n: 3, color: '#8f6bff', speed: 120, grav: 300, dir: Math.PI / 2, spread: 1 });
    }
    // type effects
    if (br.type === 'V') this.voltChain(br, causeBall, chain);
    if (br.type === 'X') this.vortexBoom(br);
    if (br.type === 'L' && causeBall) {
      const sp = Math.min(BALL_SPEED_MAX, causeBall.speed * 1.25);
      const a = Math.atan2(causeBall.vy, causeBall.vx) + (Math.random() - 0.5) * 0.8;
      causeBall.vx = Math.cos(a) * sp; causeBall.vy = Math.sin(a) * sp;
      this.audio.gel();
    }
    // drop
    if (Math.random() < DROP_CHANCE && this.capsules.length < 4) {
      const kinds = DROP_TABLE.split(' ');
      this.capsules.push(new Capsule(br.cx, br.cy, kinds[(Math.random() * kinds.length) | 0]));
    }
  }

  voltChain(br, ball, chain) {
    if (chain > 6) return;
    this.audio.volt();
    this.fx.doFlash('#c77dff', 0.12);
    const R = 150;
    for (const other of this.bricks) {
      if (!other.alive || other === br) continue;
      const d = Math.hypot(other.cx - br.cx, other.cy - br.cy);
      if (d < R) {
        this.fx.spawn(br.cx, br.cy, { n: 6, color: '#e0b3ff', speed: 500, dir: Math.atan2(other.cy - br.cy, other.cx - br.cx), spread: 0.3, glow: true });
        if (other.type === 'T') { other.hitFx = 0.3; continue; }
        this.breakBrick(other, ball, chain + 1);
      }
    }
  }

  vortexBoom(br) {
    this.audio.volt();
    this.fx.doShake(10);
    this.fx.doFlash('#ff6b9d', 0.2);
    this.fx.doHitStop(0.04);
    this.fx.spawn(br.cx, br.cy, { n: 40, color: '#ff6b9d', speed: 500, glow: true });
    // shockwave: push balls outward
    for (const b of this.balls) {
      const dx = b.x - br.cx, dy = b.y - br.cy, d = Math.hypot(dx, dy) || 1;
      if (d < 300) { b.vx += dx / d * 300; b.vy += dy / d * 300; }
    }
  }

  updateBricks(dt) {
    for (const b of this.bricks) {
      if (b.hitFx > 0) b.hitFx -= dt;
      if (b.type === 'G') {
        b.phase += dt;
        if (b.phase >= 1.6) { b.phase = 0; b.solid = !b.solid; }
      }
    }
  }

  updateCapsules(dt) {
    const p = this.paddle;
    for (let i = this.capsules.length - 1; i >= 0; i--) {
      const c = this.capsules[i];
      c.t += dt; c.y += c.vy * dt;
      if (c.y - c.h > H) { this.capsules.splice(i, 1); continue; }
      if (c.y + c.h / 2 > p.y && c.y - c.h / 2 < p.y + p.h && c.x > p.x - 10 && c.x < p.x + p.w + 10) {
        this.capsules.splice(i, 1);
        this.applyPower(c.kind);
      }
    }
  }

  applyPower(k) {
    const st = PT_STYLE[k];
    this.audio.power();
    this.fx.popup(this.paddle.cx, this.paddle.y - 24, st.name, st.color, true);
    this.fx.doFlash(st.color, 0.12);
    this.fx.spawn(this.paddle.cx, this.paddle.y, { n: 16, color: st.color, speed: 260, glow: true });
    switch (k) {
      case 'M': {
        const cur = [...this.balls];
        for (const b of cur) {
          if (this.balls.length >= 12) break;
          if (b.stuck) continue;
          for (let j = 0; j < 2 && this.balls.length < 12; j++) {
            const a = Math.atan2(b.vy, b.vx) + (j ? 0.5 : -0.5);
            const nb = new Ball(b.x, b.y, Math.cos(a) * b.speed, Math.sin(a) * b.speed);
            nb.fire = b.fire; nb.phase = b.phase;
            this.balls.push(nb);
          }
        }
        break;
      }
      case 'F': this.powerTimers.F = 8; for (const b of this.balls) b.fire = 1; break;
      case 'H': this.powerTimers.H = 6; for (const b of this.balls) b.phase = 1; break;
      case 'W': this.powerTimers.W = 10; this.paddle.w = PADDLE_W * 1.55; break;
      case 'S': if (!this.chaosOn) { this.powerTimers.S = 5; this.slowmoT = 5; } break;
      case 'G': this.powerTimers.G = 10; this.paddle.magnet = true; break;
      case 'D': this.shield = true; break;
    }
  }

  loseLife() {
    this.lives--;
    this.audio.lifeLost();
    this.fx.doShake(14);
    this.fx.doFlash('#ff2d78', 0.35);
    this.fx.doHitStop(0.12);
    this.chaosMult = 1; this.chaosTime = 0; this.chaosDecay = 0; this.chaosOn = false;
    this.audio.stopBeat(); this.audio.setIntensity(0);
    this.combo = 0;
    for (const k of ['F', 'H', 'W', 'S', 'G']) { this.powerTimers[k] = 0; }
    this.paddle.w = PADDLE_W; this.paddle.magnet = false; this.shield = false;
    if (this.lives <= 0) {
      this.state = 'gameover'; this.stateTimer = 0;
    } else {
      this.resetBallServe();
    }
  }

  checkClear() {
    const remaining = this.bricks.some(b => b.alive && b.type !== 'T');
    if (remaining) return;
    if (this.state !== 'playing') return;
    this.state = 'clear'; this.stateTimer = 0;
    this.audio.fanfare();
    this.fx.doFlash('#2de2a6', 0.3);
    for (let i = 0; i < 8; i++) {
      setTimeout(() => {
        const x = Math.random() * W, y = 100 + Math.random() * 400;
        this.fx.spawn(x, y, { n: 30, color: ['#2de2a6', '#ffe14d', '#ff2d78', '#00f6ff'][(Math.random() * 4) | 0], speed: 400, grav: 300, glow: true });
        this.audio.power();
      }, i * 180);
    }
    this.score += 1000 * (this.levelIndex + 1);
    this.fx.popup(W / 2, H / 2 - 100, 'LEVEL CLEAR +' + (1000 * (this.levelIndex + 1)), '#2de2a6', true);
  }
}
