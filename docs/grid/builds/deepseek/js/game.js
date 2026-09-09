// ROOFTOP — a modern Breakout.
// Core game engine: state machine, physics, special bricks, rooftop mode,
// power-ups, particles, HUD and overlays. All drawing happens in a fixed
// 720x1280 logical space that is scaled to fit the screen.

import { CFG } from './config.js';
import { LEVELS, randomLevel } from './levels.js';
import { Audio } from './audio.js';
import { Particles } from './particles.js';

const TAU = Math.PI * 2;
const clamp = (v, a, b) => (v < a ? a : v > b ? b : v);
const lerp = (a, b, t) => a + (b - a) * t;
const rand = (a, b) => a + Math.random() * (b - a);

const BRICK_COLORS = {
  '#': { c: '#4cc9f0', g: '#4cc9f0' },
  'A': { c: '#f72585', g: '#f72585' },
  'S': { c: '#ffd166', g: '#ffd166' },
  'T': { c: '#8d99ae', g: '#8d99ae' },
  'X': { c: '#ff6b35', g: '#ff6b35' },
  'G': { c: '#ffd700', g: '#ffd700' },
  'C': { c: '#b388ff', g: '#b388ff' },
};

const POWERUP_INFO = {
  F: { name: 'FIRE BALL', color: '#ff6b35' },
  W: { name: 'WIDE PADDLE', color: '#4cc9f0' },
  S: { name: 'SLOW MO', color: '#7df9ff' },
  L: { name: 'LASERS', color: '#ff2d78' },
  M: { name: 'MAGNET', color: '#b388ff' },
  H: { name: 'SHIELD', color: '#a7f3d0' },
  E: { name: 'EXTRA LIFE', color: '#ffd166' },
  B: { name: 'MULTI BALL', color: '#ff9f43' },
};

const POWERUP_TYPES = ['F', 'W', 'S', 'L', 'M', 'H', 'E', 'B'];

function roundRect(ctx, x, y, w, h, r) {
  r = Math.min(r, w / 2, h / 2);
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + w, y, x + w, y + h, r);
  ctx.arcTo(x + w, y + h, x, y + h, r);
  ctx.arcTo(x, y + h, x, y, r);
  ctx.arcTo(x, y, x + w, y, r);
  ctx.closePath();
}

export class Game {
  constructor(canvas) {
    this.canvas = canvas;
    this.ctx = canvas.getContext('2d', { alpha: false });
    this.audio = new Audio();
    this.particles = new Particles();

    this.dpr = 1;
    this.scale = 1;
    this.offsetX = 0;
    this.offsetY = 0;

    this.state = 'menu';
    this.time = 0;
    this.stateTimer = 0;

    this.score = 0;
    this.highScore = 0;
    try { this.highScore = parseInt(localStorage.getItem('rooftop_high') || '0', 10) || 0; } catch (e) {}
    this.lives = CFG.START_LIVES;
    this.level = 0;
    this.nextLifeAt = CFG.LIFE_EVERY;

    this.combo = 0;
    this.comboTimer = 0;
    this.mult = 1;
    this.rooftop = false;
    this.rooftopTimer = 0;
    this.rooftopBalls = 0;
    this.rooftopShown = 0; // for banner animation

    this.shake = 0;
    this.flash = 0;
    this.flashColor = '#ffffff';
    this.floaters = [];
    this.powerups = [];
    this.lasers = [];
    this.balls = [];
    this.bricks = [];
    this.ballStuck = true;

    this.paddle = { x: CFG.W / 2, y: CFG.PADDLE_MAX_Y, w: CFG.PADDLE_W, h: CFG.PADDLE_H, vx: 0, vy: 0, tx: CFG.W / 2, ty: CFG.PADDLE_MAX_Y };
    this.effects = { fire: 0, wide: 0, slow: 0, laser: 0, magnet: 0, shield: 0 };
    this.laserCooldown = 0;

    this.keys = {};
    this.mouse = { x: CFG.W / 2, y: CFG.PADDLE_MAX_Y, down: false };
    this.stick = { active: false, id: null, ox: 0, oy: 0, dx: 0, dy: 0 };
    this.touchDirect = { active: false, id: null, x: 0, y: 0, t0: 0, x0: 0, y0: 0 };

    this.stars = [];
    for (let i = 0; i < 90; i++) {
      this.stars.push({ x: Math.random() * CFG.W, y: Math.random() * CFG.H, r: rand(0.4, 1.6), tw: rand(0, TAU) });
    }

    this._resize();
    this._bindEvents();
    this._buildLevel(0);
    this._resetBall();
    this._loop = this._loop.bind(this);
    requestAnimationFrame(this._loop);
  }

  // Magnet power-up: balls can be caught and re-launched. Per-ball stuck
  // state, exposed as a single flag for convenience.
  get ballStuck() { return this.balls.some((b) => b.stuck); }
  set ballStuck(v) { for (const b of this.balls) b.stuck = v; }

  // ---------------------------------------------------------------- setup --

  _resize() {
    const w = window.innerWidth, h = window.innerHeight;
    this.dpr = Math.min(window.devicePixelRatio || 1, 2);
    this.canvas.width = Math.round(w * this.dpr);
    this.canvas.height = Math.round(h * this.dpr);
    this.scale = Math.min(w / CFG.W, h / CFG.H);
    this.offsetX = (w - CFG.W * this.scale) / 2;
    this.offsetY = (h - CFG.H * this.scale) / 2;
  }

  _bindEvents() {
    window.addEventListener('resize', () => this._resize());
    window.addEventListener('keydown', (e) => this._onKey(e, true));
    window.addEventListener('keyup', (e) => this._onKey(e, false));
    window.addEventListener('blur', () => { if (this.state === 'playing') this._setState('paused'); });

    const c = this.canvas;
    c.addEventListener('pointerdown', (e) => this._onPointerDown(e));
    c.addEventListener('pointermove', (e) => this._onPointerMove(e));
    c.addEventListener('pointerup', (e) => this._onPointerUp(e));
    c.addEventListener('pointercancel', (e) => this._onPointerUp(e));
    c.addEventListener('contextmenu', (e) => e.preventDefault());
  }

  _toGame(e) {
    const r = this.canvas.getBoundingClientRect();
    return { x: (e.clientX - r.left - this.offsetX) / this.scale, y: (e.clientY - r.top - this.offsetY) / this.scale };
  }

  _onKey(e, down) {
    const k = e.key.toLowerCase();
    if (['arrowleft', 'arrowright', 'arrowup', 'arrowdown', ' '].includes(k)) e.preventDefault();
    this.keys[k] = down;
    if (!down) return;
    this.audio.unlock();
    if (k === 'p' || k === 'escape') {
      if (this.state === 'playing') this._setState('paused');
      else if (this.state === 'paused') this._setState('playing');
    }
    if (k === 'm') this._toggleMute();
    if (k === 'enter' || k === ' ') {
      if (this.state === 'menu') this._startGame();
      else if (this.state === 'gameover') this._startGame();
      else if (this.state === 'levelclear') this._nextLevel();
      else if (this.state === 'playing') this._launchOrFire();
    }
  }

  _onPointerDown(e) {
    this.audio.unlock();
    const g = this._toGame(e);
    if (this.state === 'menu' || this.state === 'gameover') { this._startGame(); return; }
    if (this.state === 'levelclear') { this._nextLevel(); return; }
    if (this.state === 'paused') { this._setState('playing'); return; }
    if (this.state !== 'playing') return;

    // Left side: thumbstick. Right side: direct control.
    if (g.x < CFG.W * 0.45 && !this.touchDirect.active) {
      this.stick.active = true;
      this.stick.id = e.pointerId;
      this.stick.ox = g.x; this.stick.oy = g.y;
      this.stick.dx = 0; this.stick.dy = 0;
    } else if (!this.stick.active) {
      this.touchDirect.active = true;
      this.touchDirect.id = e.pointerId;
      this.touchDirect.x = g.x; this.touchDirect.y = g.y;
      this.touchDirect.t0 = performance.now();
      this.touchDirect.x0 = g.x; this.touchDirect.y0 = g.y;
      this.paddle.tx = clamp(g.x, this.paddle.w / 2, CFG.W - this.paddle.w / 2);
      this.paddle.ty = clamp(g.y, CFG.PADDLE_MIN_Y, CFG.PADDLE_MAX_Y);
    }
  }

  _onPointerMove(e) {
    const g = this._toGame(e);
    this.mouse.x = g.x; this.mouse.y = g.y;
    if (this.stick.active && e.pointerId === this.stick.id) {
      let dx = g.x - this.stick.ox, dy = g.y - this.stick.oy;
      const d = Math.hypot(dx, dy);
      const maxR = 70;
      if (d > maxR) { dx = dx / d * maxR; dy = dy / d * maxR; }
      this.stick.dx = dx / maxR;
      this.stick.dy = dy / maxR;
    }
    if (this.touchDirect.active && e.pointerId === this.touchDirect.id) {
      this.touchDirect.x = g.x; this.touchDirect.y = g.y;
      this.paddle.tx = clamp(g.x, this.paddle.w / 2, CFG.W - this.paddle.w / 2);
      this.paddle.ty = clamp(g.y, CFG.PADDLE_MIN_Y, CFG.PADDLE_MAX_Y);
    }
  }

  _onPointerUp(e) {
    if (this.stick.active && e.pointerId === this.stick.id) {
      this.stick.active = false;
      this.stick.dx = 0; this.stick.dy = 0;
    }
    if (this.touchDirect.active && e.pointerId === this.touchDirect.id) {
      const dt = performance.now() - this.touchDirect.t0;
      const moved = Math.hypot(this.touchDirect.x - this.touchDirect.x0, this.touchDirect.y - this.touchDirect.y0);
      this.touchDirect.active = false;
      if (this.state === 'playing' && dt < 250 && moved < 14) this._launchOrFire();
    }
  }

  _toggleMute() {
    this.audio.setMuted(!this.audio.muted);
    this.audio.ui();
  }

  // ------------------------------------------------------------- state ----

  _setState(s) {
    this.state = s;
    this.stateTimer = 0;
  }

  _startGame() {
    this.score = 0;
    this.lives = CFG.START_LIVES;
    this.level = 0;
    this.nextLifeAt = CFG.LIFE_EVERY;
    this.combo = 0;
    this.mult = 1;
    this.rooftop = false;
    this.rooftopTimer = 0;
    this.effects = { fire: 0, wide: 0, slow: 0, laser: 0, magnet: 0, shield: 0 };
    this.powerups.length = 0;
    this.lasers.length = 0;
    this.floaters.length = 0;
    this.particles.clear();
    this._buildLevel(0);
    this._resetBall();
    this._setState('playing');
    this.audio.level();
  }

  _nextLevel() {
    this.level++;
    this._buildLevel(this.level);
    this._resetBall();
    this._setState('playing');
    this.audio.level();
  }

  _buildLevel(n) {
    const layout = n < LEVELS.length ? LEVELS[n] : randomLevel(n - LEVELS.length + 1);
    this.bricks = [];
    const bw = CFG.BRICK_W, bh = CFG.BRICK_H, gap = CFG.BRICK_GAP;
    const totalW = CFG.BRICK_COLS * bw + (CFG.BRICK_COLS - 1) * gap;
    const x0 = (CFG.W - totalW) / 2;
    for (let r = 0; r < layout.length; r++) {
      const row = layout[r];
      for (let c = 0; c < CFG.BRICK_COLS; c++) {
        const t = row[c];
        if (t === '.' || t === undefined) continue;
        this.bricks.push({
          type: t,
          x: x0 + c * (bw + gap),
          y: CFG.BRICK_TOP + r * (bh + gap),
          w: bw, h: bh,
          hp: 1,
          flash: 0,
          wob: rand(0, TAU),
        });
      }
    }
  }

  _resetBall() {
    this.balls = [{ x: this.paddle.x, y: this.paddle.y - CFG.PADDLE_H / 2 - CFG.BALL_R - 2, vx: 0, vy: 0, speed: CFG.BALL_SPEED, trail: [], fire: this.effects.fire > 0, stuck: true }];
  }

  _launchOrFire() {
    const stuck = this.balls.filter((b) => b.stuck);
    if (stuck.length) {
      const b = stuck[0];
      const ang = rand(-0.35, 0.35);
      b.vx = Math.sin(ang) * CFG.BALL_SPEED + this.paddle.vx * 0.25;
      b.vy = -Math.cos(ang) * CFG.BALL_SPEED;
      b.speed = CFG.BALL_SPEED;
      b.stuck = false;
      this.audio.paddle();
      this.particles.burst(b.x, b.y, { count: 10, speed: 180, colors: ['#7df9ff', '#ffffff'] });
    } else if (this.effects.laser > 0 && this.laserCooldown <= 0) {
      this._fireLasers();
    }
  }

  _fireLasers() {
    this.laserCooldown = CFG.LASER_RATE;
    const p = this.paddle;
    for (const off of [-p.w / 2 + 14, p.w / 2 - 14]) {
      this.lasers.push({ x: p.x + off, y: p.y - p.h / 2, vy: -CFG.LASER_SPEED });
    }
    this.audio.laser();
  }

  // ------------------------------------------------------------- bricks ----

  _breakableCount() {
    let n = 0;
    for (const br of this.bricks) if (br.type !== 'T' && br.type !== 'C') n++;
    return n;
  }

  _breakBrick(br, cause) {
    const idx = this.bricks.indexOf(br);
    if (idx < 0) return;
    this.bricks.splice(idx, 1);
    const cx = br.x + br.w / 2, cy = br.y + br.h / 2;
    const col = BRICK_COLORS[br.type].c;
    this.particles.burst(cx, cy, { count: 16, speed: 260, colors: [col, '#ffffff', col] });
    this.particles.ring(cx, cy, { count: 10, speed: 200, colors: [col] });
    this.shake = Math.min(this.shake + 0.06, 0.5);

    let pts = CFG.BRICK_POINTS[br.type] || 100;
    if (br.type === 'G') {
      this._dropPowerup(cx, cy);
      this.audio.golden();
    } else if (br.type === 'X') {
      this._explode(cx, cy);
      this.audio.explode();
      this.flash = Math.max(this.flash, 0.25);
      this.flashColor = '#ff6b35';
      this.shake = Math.min(this.shake + 0.25, 0.9);
    } else if (br.type === 'C') {
      pts = CFG.BRICK_POINTS.core;
      this.audio.core();
      this.flash = Math.max(this.flash, 0.4);
      this.flashColor = '#b388ff';
      this.particles.ring(cx, cy, { count: 40, speed: 420, colors: ['#b388ff', '#ffffff'] });
    } else {
      this.audio.brick();
    }

    this._addScore(pts, cx, cy);
    this.combo++;
    this.comboTimer = 1.5;
    if (this.combo >= 3 && this.combo % 5 === 0) {
      this._addFloater(`COMBO x${this.combo}`, cx, cy - 30, '#ffd166', 30);
      this.audio.combo(this.combo);
    }

    // Core brick: only breaks when it is the last breakable brick.
    // Steel: only fire breaks it. Angle: needs a steep hit. Speed: needs speed.
    // (handled in _collideBallBricks before calling _breakBrick)

    if (this._breakableCount() === 0) {
      this._levelClear();
    }
  }

  _explode(cx, cy) {
    const R = 1.6; // cells
    const bw = CFG.BRICK_W + CFG.BRICK_GAP, bh = CFG.BRICK_H + CFG.BRICK_GAP;
    // Collect targets first: breaking a brick can trigger nested explosions
    // (X bricks), which mutate this.bricks while we iterate.
    const targets = [];
    for (const br of this.bricks) {
      const bcx = br.x + br.w / 2, bcy = br.y + br.h / 2;
      const dx = (bcx - cx) / bw, dy = (bcy - cy) / bh;
      if (dx * dx + dy * dy <= R * R && br.type !== 'T') targets.push(br);
    }
    for (const br of targets) this._breakBrick(br, 'explosion');
  }

  _dropPowerup(x, y) {
    const t = POWERUP_TYPES[(Math.random() * POWERUP_TYPES.length) | 0];
    this.powerups.push({ type: t, x, y, w: CFG.POWERUP_W, h: CFG.POWERUP_H, vy: CFG.POWERUP_SPEED, wob: rand(0, TAU) });
  }

  _maybeDropPowerup(x, y) {
    if (Math.random() < 0.12) {
      const t = POWERUP_TYPES[(Math.random() * POWERUP_TYPES.length) | 0];
      this.powerups.push({ type: t, x, y, w: CFG.POWERUP_W, h: CFG.POWERUP_H, vy: CFG.POWERUP_SPEED, wob: rand(0, TAU) });
    }
  }

  _applyPowerup(type) {
    const info = POWERUP_INFO[type];
    const p = this.paddle;
    switch (type) {
      case 'F': this.effects.fire = CFG.FIRE_TIME; for (const b of this.balls) b.fire = true; break;
      case 'W': this.effects.wide = CFG.WIDE_TIME; break;
      case 'S': this.effects.slow = CFG.SLOW_TIME; break;
      case 'L': this.effects.laser = CFG.LASER_TIME; break;
      case 'M': this.effects.magnet = CFG.MAGNET_TIME; break;
      case 'H': this.effects.shield = CFG.SHIELD_TIME; break;
      case 'E': this.lives++; this.audio.life(); break;
      case 'B': this._multiBall(); break;
    }
    this._addFloater(info.name, p.x, p.y - 60, info.color, 30);
    this.particles.burst(p.x, p.y, { count: 20, speed: 300, colors: [info.color, '#ffffff'] });
    this.audio.powerup();
  }

  _multiBall() {
    const src = this.balls[0] || { x: this.paddle.x, y: this.paddle.y - 40 };
    for (let i = 0; i < 2; i++) {
      if (this.balls.length >= CFG.MAX_BALLS) break;
      const ang = rand(-0.9, 0.9) + (i === 0 ? -1.2 : 1.2);
      const sp = CFG.BALL_SPEED * 0.9;
      this.balls.push({ x: src.x, y: src.y, vx: Math.sin(ang) * sp, vy: -Math.cos(ang) * sp, speed: sp, trail: [], fire: this.effects.fire > 0 });
    }
  }

  _addScore(base, x, y) {
    const gained = base * this.mult + this.combo * 10 * this.mult;
    this.score += gained;
    this._addFloater(`+${gained}`, x, y - 14, '#ffffff', 22);
    if (this.score >= this.nextLifeAt) {
      this.nextLifeAt += CFG.LIFE_EVERY;
      this.lives++;
      this._addFloater('EXTRA LIFE!', CFG.W / 2, CFG.H / 2, '#ffd166', 40);
      this.audio.life();
    }
  }

  _addFloater(text, x, y, color, size) {
    if (this.floaters.length > 40) this.floaters.shift();
    this.floaters.push({ text, x, y, color, size, life: 1.1, maxLife: 1.1, vy: -60 });
  }

  _levelClear() {
    const bonus = this.lives * CFG.LEVEL_CLEAR_BONUS;
    this.score += bonus;
    this._addFloater(`LEVEL BONUS +${bonus}`, CFG.W / 2, CFG.H / 2 - 60, '#7df9ff', 34);
    this.audio.level();
    this._setState('levelclear');
    this.stateTimer = 2.4;
  }

  _loseBall(b) {
    const idx = this.balls.indexOf(b);
    if (idx >= 0) this.balls.splice(idx, 1);
    this.particles.burst(b.x, CFG.H - 20, { count: 14, speed: 200, colors: ['#ff2d78', '#ffffff'] });
    this.audio.lose();
    this.shake = Math.min(this.shake + 0.2, 0.6);
    if (this.balls.length === 0) {
      if (this.lives > 0) {
        this.lives--;
        this._resetBall();
        this._addFloater('BALL LOST', CFG.W / 2, CFG.H / 2, '#ff2d78', 36);
      } else {
        this._gameOver();
      }
    }
  }

  _gameOver() {
    this._setState('gameover');
    this.audio.gameover();
    if (this.score > this.highScore) {
      this.highScore = this.score;
      try { localStorage.setItem('rooftop_high', String(this.highScore)); } catch (e) {}
    }
  }

  // ------------------------------------------------------------ rooftop ----

  _topBrickY() {
    let y = Infinity;
    for (const br of this.bricks) y = Math.min(y, br.y);
    return y === Infinity ? CFG.BRICK_TOP : y;
  }

  _isOnRooftop(b) {
    return b.y - CFG.BALL_R < this._topBrickY() - 6;
  }

  _checkRooftop() {
    const anyUp = this.balls.some((b) => this._isOnRooftop(b));
    if (anyUp && !this.rooftop) {
      this.rooftop = true;
      this.rooftopTimer = 0;
      this.rooftopBalls = 0;
      this.rooftopShown = 1;
      this._addFloater('ROOFTOP!', CFG.W / 2, CFG.BRICK_TOP - 40, '#ffd166', 44);
      this.particles.burst(CFG.W / 2, CFG.BRICK_TOP - 20, { count: 40, speed: 400, colors: ['#ffd166', '#ff9f43', '#ffffff'] });
      this.audio.rooftop();
      this.flash = Math.max(this.flash, 0.2);
      this.flashColor = '#ffd166';
    }
    if (!anyUp && this.rooftop) {
      this.rooftop = false;
      this.rooftopTimer = 0;
      this.mult = 1;
      this._addFloater('ROOFTOP LOST', CFG.W / 2, CFG.BRICK_TOP - 20, '#8d99ae', 26);
    }
  }

  _updateRooftop(dt) {
    if (!this.rooftop) return;
    this.rooftopTimer += dt;
    const newMult = Math.min(1 + Math.floor(this.rooftopTimer / CFG.ROOFTOP_MULT_INTERVAL), CFG.ROOFTOP_MULT_MAX);
    if (newMult > this.mult) {
      this.mult = newMult;
      this._addFloater(`x${this.mult} MULTIPLIER!`, CFG.W / 2, CFG.BRICK_TOP - 70, '#ffd166', 36);
      this.audio.combo(this.mult * 2);
      this.particles.burst(CFG.W / 2, CFG.BRICK_TOP - 30, { count: 24, speed: 320, colors: ['#ffd166', '#ffffff'] });
    }
    // Bonus balls raining onto the rooftop.
    this.rooftopBalls += dt;
    if (this.rooftopBalls >= CFG.ROOFTOP_BALL_INTERVAL) {
      this.rooftopBalls = 0;
      if (this.balls.length < CFG.ROOFTOP_BALL_MAX) {
        const ty = this._topBrickY();
        const x = rand(60, CFG.W - 60);
        const sp = CFG.BALL_SPEED * 0.8;
        const ang = rand(-0.5, 0.5) + (Math.random() < 0.5 ? 0 : Math.PI);
        this.balls.push({ x, y: ty - 40, vx: Math.cos(ang) * sp, vy: -Math.abs(Math.sin(ang)) * sp * 0.6, speed: sp, trail: [], fire: this.effects.fire > 0 });
        this._addFloater('BONUS BALL!', x, ty - 60, '#7df9ff', 26);
        this.audio.powerup();
      }
    }
    // Speed ramps up while you stay on top.
    for (const b of this.balls) {
      if (this._isOnRooftop(b)) {
        b.speed = Math.min(b.speed + CFG.ROOFTOP_SPEED_RAMP * dt, CFG.BALL_SPEED_MAX);
      }
    }
  }

  // ------------------------------------------------------------- update ----

  _update(dt) {
    this.time += dt;
    this.shake = Math.max(0, this.shake - dt * 1.6);
    this.flash = Math.max(0, this.flash - dt * 2.2);
    this.rooftopShown = Math.max(0, this.rooftopShown - dt * 0.8);
    for (const br of this.bricks) br.flash = Math.max(0, br.flash - dt * 4);
    this.particles.update(dt);

    if (this.state === 'playing') {
      this._updatePaddle(dt);
      this._updateBalls(dt);
      this._updatePowerups(dt);
      this._updateLasers(dt);
      this._checkRooftop();
      this._updateRooftop(dt);
      if (this.comboTimer > 0) {
        this.comboTimer -= dt;
        if (this.comboTimer <= 0) this.combo = 0;
      }
      for (const k of Object.keys(this.effects)) {
        if (this.effects[k] > 0) {
          this.effects[k] -= dt;
          if (this.effects[k] <= 0 && k === 'fire') for (const b of this.balls) b.fire = false;
        }
      }
      if (this.laserCooldown > 0) this.laserCooldown -= dt;
    } else if (this.state === 'levelclear') {
      this.stateTimer -= dt;
      if (this.stateTimer <= 0) this._nextLevel();
    }

    // floaters always animate
    for (let i = this.floaters.length - 1; i >= 0; i--) {
      const f = this.floaters[i];
      f.life -= dt;
      f.y += f.vy * dt;
      f.vy *= 0.96;
      if (f.life <= 0) this.floaters.splice(i, 1);
    }
  }

  // Effective paddle width: the "wide" power-up draws 1.5x wider, so
  // collision and clamping must use the same effective width.
  _paddleW() {
    return this.paddle.w * (this.effects.wide > 0 ? 1.5 : 1);
  }

  _updatePaddle(dt) {
    const p = this.paddle;
    const pw = this._paddleW();
    let ax = 0, ay = 0;
    if (this.keys['arrowleft'] || this.keys['a']) ax -= 1;
    if (this.keys['arrowright'] || this.keys['d']) ax += 1;
    if (this.keys['arrowup'] || this.keys['w']) ay -= 1;
    if (this.keys['arrowdown'] || this.keys['s']) ay += 1;
    if (this.stick.active) { ax = this.stick.dx; ay = this.stick.dy; }
    if (ax !== 0 || ay !== 0) {
      const d = Math.hypot(ax, ay) || 1;
      p.tx = clamp(p.x + (ax / d) * CFG.PADDLE_SPEED * dt, pw / 2, CFG.W - pw / 2);
      p.ty = clamp(p.y + (ay / d) * CFG.PADDLE_SPEED * dt, CFG.PADDLE_MIN_Y, CFG.PADDLE_MAX_Y);
    } else if (!this.touchDirect.active && !this.stick.active) {
      // mouse follow
      p.tx = clamp(this.mouse.x, pw / 2, CFG.W - pw / 2);
      p.ty = clamp(this.mouse.y, CFG.PADDLE_MIN_Y, CFG.PADDLE_MAX_Y);
    }
    const prevX = p.x, prevY = p.y;
    p.x = lerp(p.x, p.tx, Math.min(1, dt * 18));
    p.y = lerp(p.y, p.ty, Math.min(1, dt * 18));
    p.vx = (p.x - prevX) / Math.max(dt, 0.0001);
    p.vy = (p.y - prevY) / Math.max(dt, 0.0001);
    p.vx = clamp(p.vx, -CFG.PADDLE_SPEED * 1.5, CFG.PADDLE_SPEED * 1.5);
    p.vy = clamp(p.vy, -CFG.PADDLE_VY_MAX, CFG.PADDLE_VY_MAX);

    const stuck = this.balls.filter((b) => b.stuck);
    if (stuck.length) {
      const spread = 34;
      const off0 = ((stuck.length - 1) / 2) * spread;
      stuck.forEach((b, i) => {
        b.x = p.x - off0 + i * spread;
        b.y = p.y - p.h / 2 - CFG.BALL_R - 2;
      });
    }
  }

  _updateBalls(dt) {
    const slow = this.effects.slow > 0 ? 0.55 : 1;
    const step = 1 / 240;
    let t = dt * slow;
    while (t > 0) {
      const s = Math.min(t, step);
      for (const b of this.balls) {
        if (b.stuck) continue;
        b.x += b.vx * s;
        b.y += b.vy * s;
        b.trail.push({ x: b.x, y: b.y });
        if (b.trail.length > 10) b.trail.shift();
        this._collideBallWalls(b);
        this._collideBallPaddle(b);
        this._collideBallBricks(b);
      }
      t -= s;
    }
    // remove balls that fell off
    for (let i = this.balls.length - 1; i >= 0; i--) {
      if (this.balls[i].y - CFG.BALL_R > CFG.H + 40) this._loseBall(this.balls[i]);
    }
  }

  _collideBallWalls(b) {
    if (b.x - CFG.BALL_R < 0) { b.x = CFG.BALL_R; b.vx = Math.abs(b.vx); this.audio.wall(); }
    if (b.x + CFG.BALL_R > CFG.W) { b.x = CFG.W - CFG.BALL_R; b.vx = -Math.abs(b.vx); this.audio.wall(); }
    if (b.y - CFG.BALL_R < 0) { b.y = CFG.BALL_R; b.vy = Math.abs(b.vy); this.audio.wall(); }
  }

  _collideBallPaddle(b) {
    const p = this.paddle;
    if (b.vy <= 0) return;
    const pw = this._paddleW() + CFG.BALL_R * 2;
    if (b.x < p.x - pw / 2 || b.x > p.x + pw / 2) return;
    if (b.y + CFG.BALL_R < p.y - p.h / 2 || b.y - CFG.BALL_R > p.y + p.h / 2) return;
    if (b.y > p.y + p.h / 2 + CFG.BALL_R) return;

    // Magnet: catch the ball instead of reflecting it.
    if (this.effects.magnet > 0) {
      b.stuck = true;
      b.vx = 0; b.vy = 0;
      b.x = p.x;
      b.y = p.y - p.h / 2 - CFG.BALL_R - 2;
      this.audio.paddle();
      this.particles.burst(b.x, b.y + CFG.BALL_R, { count: 8, speed: 140, colors: ['#b388ff', '#ffffff'], angle: Math.PI, spread: 1.2 });
      return;
    }

    // reflect
    const t = clamp((b.x - p.x) / (this._paddleW() / 2), -1, 1);
    const maxAng = 1.05; // ~60 deg
    const ang = t * maxAng;
    let speed = Math.max(b.speed * CFG.PADDLE_HIT_ACCEL, CFG.BALL_SPEED_MIN);
    speed = Math.min(speed, CFG.BALL_SPEED_MAX);
    b.vx = Math.sin(ang) * speed + p.vx * CFG.PADDLE_BOOST_VX;
    b.vy = -Math.cos(ang) * speed + p.vy * CFG.PADDLE_BOOST_VY;
    // renormalize to keep speed consistent
    const ns = Math.hypot(b.vx, b.vy) || 1;
    b.vx = b.vx / ns * speed;
    b.vy = b.vy / ns * speed;
    b.speed = speed;
    b.y = p.y - p.h / 2 - CFG.BALL_R;
    this.audio.paddle();
    this.particles.burst(b.x, b.y + CFG.BALL_R, { count: 8, speed: 160, colors: ['#7df9ff', '#ffffff'], angle: Math.PI, spread: 1.2 });
    if (p.vy < -200) {
      this.particles.burst(b.x, b.y, { count: 12, speed: 260, colors: ['#ffd166', '#ffffff'] });
    }
  }

  _collideBallBricks(b) {
    const r = CFG.BALL_R;
    for (let i = this.bricks.length - 1; i >= 0; i--) {
      const br = this.bricks[i];
      const cx = clamp(b.x, br.x, br.x + br.w);
      const cy = clamp(b.y, br.y, br.y + br.h);
      const dx = b.x - cx, dy = b.y - cy;
      const d2 = dx * dx + dy * dy;
      if (d2 >= r * r) continue;

      // Determine reflection side.
      const penX = r - Math.abs(dx);
      const penY = r - Math.abs(dy);
      if (penX < penY) {
        b.vx = Math.abs(b.vx) * (dx >= 0 ? 1 : -1);
        b.x += (dx >= 0 ? 1 : -1) * (penX + 0.1);
      } else if (penY < penX) {
        b.vy = Math.abs(b.vy) * (dy >= 0 ? 1 : -1);
        b.y += (dy >= 0 ? 1 : -1) * (penY + 0.1);
      } else {
        b.vx = Math.abs(b.vx) * (dx >= 0 ? 1 : -1);
        b.vy = Math.abs(b.vy) * (dy >= 0 ? 1 : -1);
        b.x += (dx >= 0 ? 1 : -1) * (penX + 0.1);
        b.y += (dy >= 0 ? 1 : -1) * (penY + 0.1);
      }

      const speed = Math.hypot(b.vx, b.vy);
      const type = br.type;
      let breaks = true;
      let clank = false;

      if (type === 'T') {
        breaks = b.fire;
        clank = !breaks;
      } else if (type === 'A') {
        breaks = Math.abs(b.vy) > Math.abs(b.vx) * CFG.ANGLE_STEEP;
        clank = !breaks;
      } else if (type === 'S') {
        breaks = speed >= CFG.SPEED_BRICK_MIN;
        clank = !breaks;
      } else if (type === 'C') {
        breaks = this._breakableCount() === 0;
        clank = !breaks;
        if (clank) this._addFloater('BREAK THE REST FIRST!', br.x + br.w / 2, br.y - 10, '#b388ff', 20);
      }

      if (breaks) {
        br.flash = 1;
        this._breakBrick(br, 'ball');
        if (type !== 'X' && type !== 'G' && type !== 'C') this._maybeDropPowerup(br.x + br.w / 2, br.y + br.h / 2);
      } else if (clank) {
        br.flash = 1;
        this.audio.steel();
        this.particles.burst(b.x, b.y, { count: 6, speed: 120, colors: ['#8d99ae', '#ffffff'] });
      }
      break; // one brick per substep
    }
  }

  _updatePowerups(dt) {
    const p = this.paddle;
    for (let i = this.powerups.length - 1; i >= 0; i--) {
      const pu = this.powerups[i];
      pu.y += pu.vy * dt;
      pu.wob += dt * 6;
      if (pu.y > CFG.H + 30) { this.powerups.splice(i, 1); continue; }
      // catch with paddle
      if (pu.y + pu.h / 2 > p.y - p.h / 2 && pu.y - pu.h / 2 < p.y + p.h / 2 &&
          Math.abs(pu.x - p.x) < this._paddleW() / 2 + pu.w / 2) {
        this.powerups.splice(i, 1);
        this._applyPowerup(pu.type);
      }
    }
  }

  _updateLasers(dt) {
    for (let i = this.lasers.length - 1; i >= 0; i--) {
      const l = this.lasers[i];
      l.y += l.vy * dt;
      if (l.y < -20) { this.lasers.splice(i, 1); continue; }
      for (let j = this.bricks.length - 1; j >= 0; j--) {
        const br = this.bricks[j];
        if (l.x > br.x && l.x < br.x + br.w && l.y > br.y && l.y < br.y + br.h) {
          if (br.type === 'T' || br.type === 'C') {
            this.audio.steel();
            this.particles.burst(l.x, l.y, { count: 5, speed: 100, colors: ['#8d99ae'] });
          } else {
            this._breakBrick(br, 'laser');
          }
          this.lasers.splice(i, 1);
          break;
        }
      }
    }
  }

  // -------------------------------------------------------------- draw ----

  _draw() {
    const ctx = this.ctx;
    ctx.setTransform(this.dpr * this.scale, 0, 0, this.dpr * this.scale, this.dpr * this.offsetX, this.dpr * this.offsetY);
    ctx.clearRect(0, 0, CFG.W, CFG.H);

    // screen shake
    if (this.shake > 0) {
      ctx.translate(rand(-1, 1) * this.shake * 14, rand(-1, 1) * this.shake * 14);
    }

    this._drawBackground(ctx);
    this._drawBricks(ctx);
    this._drawPowerups(ctx);
    this._drawLasers(ctx);
    this._drawPaddle(ctx);
    this._drawBalls(ctx);
    this.particles.draw(ctx);
    this._drawFloaters(ctx);
    this._drawHUD(ctx);
    this._drawOverlays(ctx);

    if (this.flash > 0) {
      ctx.globalAlpha = Math.min(this.flash, 0.5);
      ctx.fillStyle = this.flashColor;
      ctx.fillRect(-20, -20, CFG.W + 40, CFG.H + 40);
      ctx.globalAlpha = 1;
    }
  }

  _drawBackground(ctx) {
    const rt = this.rooftop ? Math.min(this.rooftopShown + 0.2, 1) : 0;
    const g = ctx.createLinearGradient(0, 0, 0, CFG.H);
    g.addColorStop(0, lerpColor('#0d1233', '#2b1a4d', rt));
    g.addColorStop(0.5, lerpColor('#0a0d24', '#4a1f3d', rt));
    g.addColorStop(1, lerpColor('#05060f', '#120a1e', rt));
    ctx.fillStyle = g;
    ctx.fillRect(-20, -20, CFG.W + 40, CFG.H + 40);

    // stars
    ctx.fillStyle = '#ffffff';
    for (const s of this.stars) {
      const a = 0.25 + 0.35 * (0.5 + 0.5 * Math.sin(this.time * 1.5 + s.tw));
      ctx.globalAlpha = a;
      ctx.beginPath();
      ctx.arc(s.x, s.y, s.r, 0, TAU);
      ctx.fill();
    }
    ctx.globalAlpha = 1;

    // rooftop sunrise glow
    if (rt > 0) {
      const rg = ctx.createRadialGradient(CFG.W / 2, CFG.BRICK_TOP - 60, 10, CFG.W / 2, CFG.BRICK_TOP - 60, 500);
      rg.addColorStop(0, `rgba(255, 180, 60, ${0.5 * rt})`);
      rg.addColorStop(1, 'rgba(255, 180, 60, 0)');
      ctx.fillStyle = rg;
      ctx.fillRect(0, 0, CFG.W, CFG.BRICK_TOP + 200);
    }

    // faint grid
    ctx.strokeStyle = 'rgba(120, 160, 255, 0.05)';
    ctx.lineWidth = 1;
    ctx.beginPath();
    for (let x = 0; x <= CFG.W; x += 60) { ctx.moveTo(x, 0); ctx.lineTo(x, CFG.H); }
    for (let y = 0; y <= CFG.H; y += 60) { ctx.moveTo(0, y); ctx.lineTo(CFG.W, y); }
    ctx.stroke();
  }

  _drawBricks(ctx) {
    for (const br of this.bricks) {
      const col = BRICK_COLORS[br.type];
      const x = br.x, y = br.y, w = br.w, h = br.h;
      ctx.save();
      ctx.shadowColor = col.g;
      ctx.shadowBlur = 8 + br.flash * 20;
      const g = ctx.createLinearGradient(0, y, 0, y + h);
      g.addColorStop(0, lighten(col.c, 0.25));
      g.addColorStop(1, col.c);
      ctx.fillStyle = g;
      roundRect(ctx, x, y, w, h, 5);
      ctx.fill();
      ctx.shadowBlur = 0;
      // inner highlight
      ctx.fillStyle = 'rgba(255,255,255,0.18)';
      roundRect(ctx, x + 3, y + 3, w - 6, h * 0.35, 3);
      ctx.fill();
      // glyphs for special bricks
      ctx.fillStyle = 'rgba(0,0,0,0.35)';
      ctx.font = '700 15px Rajdhani, sans-serif';
      ctx.textAlign = 'center';
      ctx.textBaseline = 'middle';
      const cx = x + w / 2, cy = y + h / 2;
      if (br.type === 'A') {
        ctx.font = '700 16px Rajdhani, sans-serif';
        ctx.fillText('↗', cx, cy);
      } else if (br.type === 'S') {
        ctx.fillText('»', cx, cy);
      } else if (br.type === 'T') {
        ctx.fillStyle = 'rgba(255,255,255,0.5)';
        ctx.fillText('◆', cx, cy);
      } else if (br.type === 'X') {
        ctx.fillText('✸', cx, cy);
      } else if (br.type === 'G') {
        ctx.fillText('★', cx, cy);
      } else if (br.type === 'C') {
        ctx.fillStyle = 'rgba(255,255,255,0.7)';
        ctx.font = '700 18px Rajdhani, sans-serif';
        ctx.fillText('◉', cx, cy);
      }
      ctx.restore();
    }
  }

  _drawPaddle(ctx) {
    const p = this.paddle;
    const w = this._paddleW();
    const x = p.x - w / 2, y = p.y - p.h / 2;
    ctx.save();
    ctx.shadowColor = '#7df9ff';
    ctx.shadowBlur = 18;
    const g = ctx.createLinearGradient(0, y, 0, y + p.h);
    g.addColorStop(0, '#9ff5ff');
    g.addColorStop(0.5, '#4cc9f0');
    g.addColorStop(1, '#2a6fd6');
    ctx.fillStyle = g;
    roundRect(ctx, x, y, w, p.h, p.h / 2);
    ctx.fill();
    ctx.shadowBlur = 0;
    ctx.fillStyle = 'rgba(255,255,255,0.5)';
    roundRect(ctx, x + 6, y + 3, w - 12, 4, 2);
    ctx.fill();
    // shield
    if (this.effects.shield > 0) {
      ctx.strokeStyle = `rgba(167, 243, 208, ${0.5 + 0.3 * Math.sin(this.time * 6)})`;
      ctx.lineWidth = 4;
      ctx.beginPath();
      ctx.arc(p.x, p.y, w / 2 + 14, Math.PI, TAU);
      ctx.stroke();
    }
    ctx.restore();
  }

  _drawBalls(ctx) {
    for (const b of this.balls) {
      // trail
      for (let i = 0; i < b.trail.length; i++) {
        const t = b.trail[i];
        const a = (i / b.trail.length) * 0.35;
        ctx.globalAlpha = a;
        ctx.fillStyle = b.fire ? '#ff6b35' : '#7df9ff';
        ctx.beginPath();
        ctx.arc(t.x, t.y, CFG.BALL_R * (i / b.trail.length) * 0.8, 0, TAU);
        ctx.fill();
      }
      ctx.globalAlpha = 1;
      ctx.save();
      ctx.shadowColor = b.fire ? '#ff6b35' : '#7df9ff';
      ctx.shadowBlur = 16;
      const g = ctx.createRadialGradient(b.x - 3, b.y - 3, 2, b.x, b.y, CFG.BALL_R);
      g.addColorStop(0, '#ffffff');
      g.addColorStop(0.5, b.fire ? '#ff9f43' : '#7df9ff');
      g.addColorStop(1, b.fire ? '#ff2d55' : '#2a6fd6');
      ctx.fillStyle = g;
      ctx.beginPath();
      ctx.arc(b.x, b.y, CFG.BALL_R, 0, TAU);
      ctx.fill();
      ctx.restore();
      if (b.fire) {
        this.particles.burst(b.x, b.y, { count: 1, speed: 40, size: 3, life: 0.25, colors: ['#ff6b35', '#ffd166'] });
      }
    }
  }

  _drawPowerups(ctx) {
    for (const pu of this.powerups) {
      const info = POWERUP_INFO[pu.type];
      const wob = Math.sin(pu.wob) * 4;
      const x = pu.x - pu.w / 2, y = pu.y - pu.h / 2 + wob;
      ctx.save();
      ctx.shadowColor = info.color;
      ctx.shadowBlur = 12;
      ctx.fillStyle = 'rgba(10, 14, 34, 0.85)';
      roundRect(ctx, x, y, pu.w, pu.h, 8);
      ctx.fill();
      ctx.strokeStyle = info.color;
      ctx.lineWidth = 2;
      roundRect(ctx, x, y, pu.w, pu.h, 8);
      ctx.stroke();
      ctx.shadowBlur = 0;
      ctx.fillStyle = info.color;
      ctx.font = '700 15px Rajdhani, sans-serif';
      ctx.textAlign = 'center';
      ctx.textBaseline = 'middle';
      ctx.fillText(pu.type, pu.x, pu.y + wob);
      ctx.restore();
    }
  }

  _drawLasers(ctx) {
    ctx.save();
    ctx.strokeStyle = '#ff2d78';
    ctx.shadowColor = '#ff2d78';
    ctx.shadowBlur = 10;
    ctx.lineWidth = 3;
    for (const l of this.lasers) {
      ctx.beginPath();
      ctx.moveTo(l.x, l.y);
      ctx.lineTo(l.x, l.y + 26);
      ctx.stroke();
    }
    ctx.restore();
  }

  _drawFloaters(ctx) {
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    for (const f of this.floaters) {
      const a = clamp(f.life / f.maxLife, 0, 1);
      ctx.globalAlpha = a;
      ctx.font = `700 ${f.size}px Rajdhani, sans-serif`;
      ctx.fillStyle = f.color;
      ctx.shadowColor = f.color;
      ctx.shadowBlur = 8;
      ctx.fillText(f.text, f.x, f.y);
    }
    ctx.globalAlpha = 1;
    ctx.shadowBlur = 0;
  }

  _drawHUD(ctx) {
    ctx.textBaseline = 'alphabetic';
    // score
    ctx.textAlign = 'left';
    ctx.font = '600 20px Rajdhani, sans-serif';
    ctx.fillStyle = 'rgba(160, 200, 255, 0.7)';
    ctx.fillText('SCORE', 24, 40);
    ctx.font = '900 40px Orbitron, sans-serif';
    ctx.fillStyle = '#ffffff';
    ctx.shadowColor = '#7df9ff';
    ctx.shadowBlur = 10;
    ctx.fillText(String(this.score).padStart(7, '0'), 24, 80);
    ctx.shadowBlur = 0;

    // level
    ctx.textAlign = 'center';
    ctx.font = '600 18px Rajdhani, sans-serif';
    ctx.fillStyle = 'rgba(160, 200, 255, 0.7)';
    ctx.fillText(`LEVEL ${this.level + 1}`, CFG.W / 2, 40);

    // multiplier
    if (this.mult > 1) {
      const pulse = 1 + 0.12 * Math.sin(this.time * 8);
      ctx.save();
      ctx.translate(CFG.W / 2, 100);
      ctx.scale(pulse, pulse);
      ctx.font = '900 44px Orbitron, sans-serif';
      ctx.fillStyle = '#ffd166';
      ctx.shadowColor = '#ff9f43';
      ctx.shadowBlur = 18;
      ctx.textAlign = 'center';
      ctx.fillText(`x${this.mult}`, 0, 0);
      ctx.restore();
    }

    // lives (hearts)
    ctx.textAlign = 'right';
    for (let i = 0; i < this.lives; i++) {
      const hx = CFG.W - 30 - i * 34, hy = 40;
      ctx.fillStyle = '#ff2d78';
      ctx.shadowColor = '#ff2d78';
      ctx.shadowBlur = 8;
      ctx.font = '26px sans-serif';
      ctx.fillText('♥', hx, hy);
    }
    ctx.shadowBlur = 0;

    // combo
    if (this.combo >= 3) {
      ctx.textAlign = 'center';
      ctx.font = '700 24px Rajdhani, sans-serif';
      ctx.fillStyle = '#ffd166';
      ctx.fillText(`COMBO x${this.combo}`, CFG.W / 2, 150);
    }

    // power-up timers
    const EFFECT_LETTER = { fire: 'F', wide: 'W', slow: 'S', laser: 'L', magnet: 'M', shield: 'H' };
    let px = 24;
    for (const k of Object.keys(this.effects)) {
      const t = this.effects[k];
      if (t <= 0) continue;
      const letter = EFFECT_LETTER[k];
      const info = POWERUP_INFO[letter];
      const frac = t / CFG[`${letter}_TIME`];
      ctx.save();
      ctx.fillStyle = 'rgba(10,14,34,0.7)';
      roundRect(ctx, px, CFG.H - 64, 44, 44, 10);
      ctx.fill();
      ctx.strokeStyle = info.color;
      ctx.lineWidth = 2;
      ctx.globalAlpha = 0.35;
      roundRect(ctx, px, CFG.H - 64, 44, 44, 10);
      ctx.stroke();
      ctx.globalAlpha = 1;
      ctx.strokeStyle = info.color;
      ctx.beginPath();
      ctx.arc(px + 22, CFG.H - 42, 16, -Math.PI / 2, -Math.PI / 2 + TAU * frac);
      ctx.stroke();
      ctx.fillStyle = info.color;
      ctx.font = '700 16px Rajdhani, sans-serif';
      ctx.textAlign = 'center';
      ctx.textBaseline = 'middle';
      ctx.fillText(k.toUpperCase(), px + 22, CFG.H - 42);
      ctx.restore();
      px += 52;
    }

    // rooftop banner
    if (this.rooftop) {
      const a = clamp(this.rooftopShown + 0.3, 0, 1);
      ctx.save();
      ctx.globalAlpha = a;
      ctx.textAlign = 'center';
      ctx.font = '900 34px Orbitron, sans-serif';
      const grad = ctx.createLinearGradient(0, CFG.BRICK_TOP - 120, 0, CFG.BRICK_TOP - 80);
      grad.addColorStop(0, '#ffd166');
      grad.addColorStop(1, '#ff9f43');
      ctx.fillStyle = grad;
      ctx.shadowColor = '#ff9f43';
      ctx.shadowBlur = 20;
      ctx.fillText('▲ ROOFTOP ▲', CFG.W / 2, CFG.BRICK_TOP - 100);
      ctx.restore();
    }
  }

  _drawOverlays(ctx) {
    const cx = CFG.W / 2, cy = CFG.H / 2;
    if (this.state === 'menu') {
      this._overlayPanel(ctx, 0.55);
      ctx.textAlign = 'center';
      ctx.font = '900 96px Orbitron, sans-serif';
      const grad = ctx.createLinearGradient(0, cy - 220, 0, cy - 100);
      grad.addColorStop(0, '#ffd166');
      grad.addColorStop(0.5, '#ff9f43');
      grad.addColorStop(1, '#ff2d78');
      ctx.fillStyle = grad;
      ctx.shadowColor = '#ff9f43';
      ctx.shadowBlur = 30;
      ctx.fillText('ROOFTOP', cx, cy - 160);
      ctx.shadowBlur = 0;
      ctx.font = '600 26px Rajdhani, sans-serif';
      ctx.fillStyle = '#cfe8ff';
      ctx.fillText('a modern breakout', cx, cy - 100);
      ctx.font = '700 22px Rajdhani, sans-serif';
      ctx.fillStyle = 'rgba(160,200,255,0.8)';
      ctx.fillText('Break through the bricks. Get the ball on top.', cx, cy - 30);
      ctx.fillText('Then let it run wild.', cx, cy + 4);
      if (this.highScore > 0) {
        ctx.fillStyle = '#ffd166';
        ctx.fillText(`HIGH SCORE  ${this.highScore}`, cx, cy + 60);
      }
      this._pulseText(ctx, 'TAP TO START', cx, cy + 150, 30, '#7df9ff');
      ctx.font = '600 18px Rajdhani, sans-serif';
      ctx.fillStyle = 'rgba(160,200,255,0.6)';
      ctx.fillText('mouse / arrows / WASD · drag left side = thumbstick', cx, cy + 210);
      ctx.fillText('move up when hitting the ball to boost it', cx, cy + 236);
    } else if (this.state === 'paused') {
      this._overlayPanel(ctx, 0.5);
      ctx.textAlign = 'center';
      ctx.font = '900 64px Orbitron, sans-serif';
      ctx.fillStyle = '#ffffff';
      ctx.fillText('PAUSED', cx, cy - 30);
      this._pulseText(ctx, 'TAP TO RESUME', cx, cy + 60, 26, '#7df9ff');
    } else if (this.state === 'levelclear') {
      this._overlayPanel(ctx, 0.5);
      ctx.textAlign = 'center';
      ctx.font = '900 56px Orbitron, sans-serif';
      ctx.fillStyle = '#7df9ff';
      ctx.shadowColor = '#7df9ff';
      ctx.shadowBlur = 20;
      ctx.fillText('LEVEL CLEAR!', cx, cy - 40);
      ctx.shadowBlur = 0;
      ctx.font = '700 24px Rajdhani, sans-serif';
      ctx.fillStyle = '#cfe8ff';
      ctx.fillText(`+${this.lives * CFG.LEVEL_CLEAR_BONUS} life bonus`, cx, cy + 20);
    } else if (this.state === 'gameover') {
      this._overlayPanel(ctx, 0.6);
      ctx.textAlign = 'center';
      ctx.font = '900 72px Orbitron, sans-serif';
      ctx.fillStyle = '#ff2d78';
      ctx.shadowColor = '#ff2d78';
      ctx.shadowBlur = 24;
      ctx.fillText('GAME OVER', cx, cy - 140);
      ctx.shadowBlur = 0;
      ctx.font = '700 26px Rajdhani, sans-serif';
      ctx.fillStyle = '#cfe8ff';
      ctx.fillText(`SCORE  ${this.score}`, cx, cy - 60);
      if (this.score >= this.highScore && this.score > 0) {
        ctx.fillStyle = '#ffd166';
        ctx.font = '900 30px Orbitron, sans-serif';
        ctx.fillText('★ NEW HIGH SCORE ★', cx, cy - 10);
      } else {
        ctx.fillStyle = 'rgba(160,200,255,0.8)';
        ctx.fillText(`HIGH SCORE  ${this.highScore}`, cx, cy - 10);
      }
      this._pulseText(ctx, 'TAP TO PLAY AGAIN', cx, cy + 90, 28, '#7df9ff');
    }
  }

  _overlayPanel(ctx, alpha) {
    ctx.fillStyle = `rgba(5, 6, 15, ${alpha})`;
    ctx.fillRect(-20, -20, CFG.W + 40, CFG.H + 40);
  }

  _pulseText(ctx, text, x, y, size, color) {
    const a = 0.6 + 0.4 * Math.sin(this.time * 4);
    ctx.globalAlpha = a;
    ctx.font = `700 ${size}px Rajdhani, sans-serif`;
    ctx.fillStyle = color;
    ctx.textAlign = 'center';
    ctx.fillText(text, x, y);
    ctx.globalAlpha = 1;
  }

  // -------------------------------------------------------------- loop ----

  _loop(ts) {
    const dt = Math.min((ts - (this._last || ts)) / 1000, 1 / 30);
    this._last = ts;
    try {
      this._update(dt);
      this._draw();
    } catch (e) {
      // Never let a single-frame error kill the game loop.
      if (!this._errCount) this._errCount = 0;
      this._errCount++;
      if (this._errCount < 20) console.error('frame error', e);
    }
    requestAnimationFrame(this._loop);
  }
}

// ---- color helpers ---------------------------------------------------------

function lighten(hex, amt) {
  const n = parseInt(hex.slice(1), 16);
  const r = Math.min(255, ((n >> 16) & 255) + amt * 255) | 0;
  const g = Math.min(255, ((n >> 8) & 255) + amt * 255) | 0;
  const b = Math.min(255, (n & 255) + amt * 255) | 0;
  return `rgb(${r},${g},${b})`;
}

function lerpColor(a, b, t) {
  const pa = parseInt(a.slice(1), 16), pb = parseInt(b.slice(1), 16);
  const r = Math.round(lerp((pa >> 16) & 255, (pb >> 16) & 255, t));
  const g = Math.round(lerp((pa >> 8) & 255, (pb >> 8) & 255, t));
  const bl = Math.round(lerp(pa & 255, pb & 255, t));
  return `rgb(${r},${g},${bl})`;
}
