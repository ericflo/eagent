// main.js — game state machine, fixed-timestep loop, rendering, and wiring.
// States: BOOT -> MENU (attract demo) -> PLAY (BALL_HELD / BALL_LIVE) ->
// PAUSED -> LEVELCLEAR (2.2s) -> next level ... -> GAMEOVER (attract demo).
// Render: rAF with dt clamp (max 1/20s). Physics: 240Hz fixed substeps.
// Canvas: logical 1500x1000 field, letterboxed, DPR clamped to 2.

import {
  FIELD, PADDLE, BALL, PHYSICS, BRICK_RULES, POWERUPS,
  OVERDRIVE, FX as FXCFG, STORAGE_KEYS
} from './config.js';
import { clamp, lerp, rand } from './utils.js';
import { Input } from './input.js';
import { Audio } from './audio.js';
import {
  Paddle, Ball, Brick, PowerUp, Laser,
  circleRect, reflectBall, paddleBounce, rampCollide, ballBall
} from './entities.js';
import { buildLevel, levelBallSpeed } from './levels.js';
import { Overdrive } from './overdrive.js';
import { FX } from './fx.js';
import { UI } from './ui.js';
import { DemoPilot } from './demo.js';

const STATE = {
  BOOT: 'BOOT',
  MENU: 'MENU',
  PLAY: 'PLAY',
  PAUSED: 'PAUSED',
  LEVELCLEAR: 'LEVELCLEAR',
  GAMEOVER: 'GAMEOVER'
};
const BALL_STATE = { HELD: 'HELD', LIVE: 'LIVE' };

class Game {
  constructor(canvas) {
    this.canvas = canvas;
    this.ctx = canvas.getContext('2d', { alpha: false });
    if (!this.ctx) {
      document.body.insertAdjacentHTML('beforeend',
        '<div style="color:#fff;font-family:sans-serif;padding:2em">Canvas 2D is not available in this browser.</div>');
      return;
    }

    this.state = STATE.MENU;
    this.ballState = BALL_STATE.HELD;
    this.time = 0;

    // Entities
    this.paddle = new Paddle();
    this.balls = [];
    this.bricks = [];
    this.powerups = [];
    this.lasers = [];
    this.lockedPairs = new Set();
    this.magnetBricks = [];

    // Meta
    this.level = 1;
    this.score = 0;
    this.lives = BALL.LIVES;
    this.combo = 0;
    this.best = this._loadBest();
    this.newBest = false;

    // Power-up expiry times (game clock)
    this.wideUntil = 0;
    this.slowUntil = 0;
    this.laserUntil = 0;
    this.magnetPowerUntil = 0;
    this.laserTimer = 0;

    this.overdrive = new Overdrive();
    this.fx = new FX();
    this.audio = new Audio();

    // Demo autopilot (demo.js) reads g.FIELD for field bounds.
    this.FIELD = FIELD;
    this.levelClearTimer = 0;
    this.holdBall = null;
    this._tipShown = false;

    // Letterbox transform in canvas-relative CSS px: {scale, tx, ty}
    this.transform = { scale: 1, tx: 0, ty: 0 };
    this.dpr = 1;

    // Input
    this.input = new Input(canvas, { onAction: (a) => this.onAction(a) });
    this.input.setPaddlePosProvider(() => ({
      x: this.transform.tx + this.paddle.x * this.transform.scale,
      y: this.transform.ty + this.paddle.y * this.transform.scale
    }));

    // UI (DOM HUD + screens)
    const root = document.getElementById('game-root');
    this.ui = new UI(root || document.body, canvas, {
      onUI: (act) => this.onAction(act)
    });
    this.audio.ensure(); // create the context up-front; muted until gesture
    this.ui.setMuted(this.ui.muted);
    this.audio.setMuted(this.ui.muted);

    this.demo = new DemoPilot(this);

    this._resize();
    window.addEventListener('resize', () => this._resize());

    // Lazy audio unlock on first gesture
    const wake = () => { this.audio.ensure(); this.audio.resume(); };
    window.addEventListener('pointerdown', wake);
    window.addEventListener('keydown', wake);

    // Enter / click on MENU & GAMEOVER start / return (screen taps)
    canvas.addEventListener('pointerdown', () => {
      if (this.state === STATE.MENU) this._startGame();
      else if (this.state === STATE.GAMEOVER) this.setState(STATE.MENU);
    });

    this.loadLevel(1);
    this.setState(STATE.MENU);

    this._last = performance.now();
    this._acc = 0;
    requestAnimationFrame((t) => this._loop(t));
  }

  // ---------------------------------------------------------------- state

  setState(s) {
    this.state = s;
    switch (s) {
      case STATE.MENU:
        this._resetRun();
        this.loadLevel(this.level);
        this.ui.showMenu(this.best);
        break;
      case STATE.PLAY:
        this.demo.stop();
        this.ui.showHUD();
        this.ui.dismissTip();
        break;
      case STATE.PAUSED:
        this.ui.showPaused();
        break;
      case STATE.LEVELCLEAR: {
        const bonus = FXCFG.LEVEL_BONUS_PER_LEVEL * this.level;
        this.score += bonus;
        this.audio.levelClear(this.overdrive.band);
        this.fx.confetti(FIELD.W / 2, FIELD.H * 0.4, 40);
        this.fx.confetti(FIELD.W * 0.3, FIELD.H * 0.3, 30);
        this.fx.confetti(FIELD.W * 0.7, FIELD.H * 0.3, 30);
        this.fx.addShake(FXCFG.SHAKE_MAX);
        this.ui.showLevelClear(this.level, bonus);
        this.levelClearTimer = FXCFG.LEVELCLEAR_TIME;
        break;
      }
      case STATE.GAMEOVER: {
        this.newBest = this.score > this.best;
        if (this.newBest) { this.best = this.score; this._saveBest(); }
        this.audio.gameOver();
        this.ui.showGameOver(this.score, this.best, this.newBest);
        break;
      }
    }
  }

  _startGame() {
    this._resetRun();
    this.loadLevel(1);
    this.setState(STATE.PLAY);
  }

  _resetRun() {
    this.score = 0;
    this.lives = BALL.LIVES;
    this.level = 1;
    this.newBest = false;
    this.fx.clear();
  }

  loadLevel(n) {
    this.level = n;
    this.bricks = buildLevel(n);
    this.lockedPairs = new Set();
    for (const br of this.bricks) if (br.type === 'lock') this.lockedPairs.add(br.pair);
    this._refreshMagnetBricks();
    this.powerups.length = 0;
    this.lasers.length = 0;
    this.balls.length = 0;
    this.holdBall = null;
    this.combo = 0;
    this.wideUntil = 0;
    this.slowUntil = 0;
    this.laserUntil = 0;
    this.magnetPowerUntil = 0;
    this.overdrive.reset();
    this.paddle.x = FIELD.W / 2;
    this.paddle.y = (PADDLE.MIN_Y + PADDLE.MAX_Y) / 2;
    this._acc = 0;
    this.spawnHeldBall();
  }

  _refreshMagnetBricks() {
    this.magnetBricks = this.bricks.filter((b) => b.type === 'magnet' && b.alive);
  }

  spawnHeldBall() {
    const speed = levelBallSpeed(this.level);
    const ball = new Ball(this.paddle.x, this.paddle.y - PADDLE.H / 2 - BALL.R - 2, speed, -Math.PI / 2);
    ball.held = true;
    ball.paddle = this.paddle;
    ball.heldOffset = 0;
    this.balls.push(ball);
    this.holdBall = ball;
    this.ballState = BALL_STATE.HELD;
  }

  // ---------------------------------------------------------------- input

  onAction(name) {
    this.audio.ensure();
    this.audio.resume();
    switch (name) {
      case 'pause':
        if (this.state === STATE.PLAY) this.setState(STATE.PAUSED);
        else if (this.state === STATE.PAUSED) this.setState(STATE.PLAY);
        break;
      case 'mute':
        this.ui.setMuted(!this.ui.muted);
        this.audio.setMuted(this.ui.muted);
        break;
      case 'restart':
        if (this.state === STATE.PLAY || this.state === STATE.PAUSED) {
          this.loadLevel(this.level);
          this.setState(STATE.PLAY);
        }
        break;
      case 'confirm':
        if (this.state === STATE.MENU) this._startGame();
        else if (this.state === STATE.GAMEOVER) this.setState(STATE.MENU);
        else if (this.state === STATE.PAUSED) this.setState(STATE.PLAY);
        break;
      case 'launch':
        if (this.state === STATE.PLAY) this._tryLaunchOrHold();
        break;
      case 'gamepadDetected':
        this.ui.showToast('Gamepad detected');
        break;
    }
  }

  _tryLaunchOrHold() {
    if (this.ballState === BALL_STATE.HELD && this.holdBall) {
      this._launchBall(this.holdBall);
      return;
    }
    // While live: stick the lowest ball to the paddle (launch/hold model)
    let lowest = null;
    for (const b of this.balls) {
      if (b.held) continue;
      if (!lowest || b.y > lowest.y) lowest = b;
    }
    if (lowest) {
      lowest.held = true;
      lowest.paddle = this.paddle;
      lowest.heldOffset = clamp(lowest.x - this.paddle.x, -this.paddle.w / 2 + 10, this.paddle.w / 2 - 10);
      this.holdBall = lowest;
      this.ballState = BALL_STATE.HELD;
    }
  }

  _launchBall(ball) {
    const offset = clamp((ball.x - this.paddle.x) / (this.paddle.w / 2), -1, 1);
    const speed = clamp(levelBallSpeed(this.level), BALL.MIN_SPEED, BALL.MAX_SPEED);
    const angle = -Math.PI / 2 + offset * 0.5;
    ball.vx = Math.cos(angle) * speed;
    ball.vy = Math.sin(angle) * speed;
    ball.held = false;
    ball.paddle = null;
    this.holdBall = null;
    if (!this.balls.every((b) => b.held)) this.ballState = BALL_STATE.LIVE;
    this.audio.launch(this.overdrive.band);
    this.fx.sparks(ball.x, ball.y, '#7df9ff', 8);
    if (!this._tipShown) {
      this._tipShown = true;
      this.ui.showTip();
      try { localStorage.setItem(STORAGE_KEYS.TIP, '1'); } catch (e) { /* ignore */ }
    }
  }

  // -------------------------------------------------------------- main loop

  _loop(now) {
    requestAnimationFrame((t) => this._loop(t));
    let dt = (now - this._last) / 1000;
    this._last = now;
    if (dt > PHYSICS.MAX_DT) dt = PHYSICS.MAX_DT;
    if (dt < 0) dt = 0;

    this.input.update(dt);
    const inp = this.input.state;

    // Pointer paddle target arrives in viewport CSS px; convert to field
    // units. (The demo overwrites paddleTarget directly in field units.)
    if (inp.pointerActive && inp.paddleTarget &&
        this.state !== STATE.MENU && this.state !== STATE.GAMEOVER) {
      const rect = this.canvas.getBoundingClientRect();
      const t = this.input.toField(
        inp.paddleTarget.x - rect.left, inp.paddleTarget.y - rect.top, this.transform);
      if (t) inp.paddleTarget = t;
    }

    const simulating =
      this.state === STATE.PLAY ||
      this.state === STATE.MENU ||
      this.state === STATE.GAMEOVER ||
      this.state === STATE.LEVELCLEAR;

    // Demo runs before launch handling so its launchPressed edge is honored.
    if (simulating &&
        (this.state === STATE.MENU || this.state === STATE.GAMEOVER) && this.demo) {
      this.demo.update(dt);
    }

    if ((this.state === STATE.PLAY || this.state === STATE.MENU) && inp.launchPressed) {
      this._tryLaunchOrHold();
    }

    if (simulating) {
      const frozen = this.fx.consumeHitStop(dt);
      if (!frozen) {
        if (this.state === STATE.LEVELCLEAR) {
          this.levelClearTimer -= dt;
          this._coastBalls(dt);
          if (this.levelClearTimer <= 0) {
            this.loadLevel(this.level + 1);
            this.setState(STATE.PLAY);
          }
        } else {
          this._stepSim(dt);
        }
      }
      this.fx.update(dt);
      this.time += dt;
    } else if (this.state === STATE.PAUSED) {
      this.fx.update(dt * 0.3);
    }

    this.audio.setHum(this.state === STATE.PAUSED ? 0 : this.overdrive.juice);

    for (const ev of this.overdrive.events) {
      if (ev === 'tick') {
        this.audio.overdriveTick(this.overdrive.band);
        this.fx.addShake(FXCFG.SHAKE_MIN);
        this.fx.text(FIELD.W / 2, 190, 'OVERDRIVE x' + this.overdrive.multiplier,
          { color: '#9df2ff', size: 34, bold: true, life: 1.0 });
      } else if (ev === 'max') {
        const bonus = OVERDRIVE.BANK_BONUS_PER_LEVEL * this.level;
        if (this.state === STATE.PLAY || this.state === STATE.LEVELCLEAR) this.score += bonus;
        this.audio.overdriveMax(this.overdrive.band);
        this.fx.flashScreen('180,240,255', 0.8);
        this.fx.addShake(FXCFG.SHAKE_MAX);
        this.fx.text(FIELD.W / 2, 160, 'MAX OVERDRIVE +' + bonus,
          { color: '#ffe27a', size: 44, bold: true, life: 1.4 });
        this.fx.confetti(FIELD.W / 2, 220, 30);
      }
    }

    this._render();

    if (this.state === STATE.PLAY || this.state === STATE.PAUSED || this.state === STATE.LEVELCLEAR) {
      this.ui.update(this.score, this.overdrive.multiplier, this.lives, this.level, this.overdrive.meter);
      this.ui.updatePowerups(this._activePowerups());
    }
  }

  // Fixed 240Hz substeps; capped so a long stall can't spiral.
  _stepSim(dt) {
    this._acc += dt;
    const h = 1 / PHYSICS.HZ;
    let steps = 0;
    while (this._acc >= h && steps < 40) {
      this._physicsStep(h);
      this._acc -= h;
      steps++;
    }
    if (this._acc > h * 4) this._acc = 0;

    this._updateFrame(dt);
    for (const b of this.balls) b.pushTrail();
    this._removeLostBalls(true);
    this._checkLifeLost();
    this._checkLevelClear();
  }

  // LEVELCLEAR: balls keep coasting & bouncing, bricks are gone.
  _coastBalls(dt) {
    this.paddle.update(dt, this);
    const h = 1 / PHYSICS.HZ;
    let t = dt, steps = 0;
    while (t > 1e-6 && steps < 40) {
      const s = Math.min(h, t);
      for (const b of this.balls) {
        if (b.held) { this._updateHeldBall(b); continue; }
        const r = b.step(s, this);
        this._onWallHits(b, r.hits);
        const pb = paddleBounce(b, this.paddle);
        if (pb) {
          if (pb.underside) this.audio.paddle(b.x, pb.offset01, this.overdrive.band, false);
          else this._onPaddleBounce(b, pb);
        }
      }
      t -= s;
      steps++;
    }
    for (const b of this.balls) b.pushTrail();
    this._removeLostBalls(false);
  }

  _physicsStep(h) {
    for (const br of this.bricks) br.update(h);
    this.paddle.update(h, this);

    for (const b of this.balls) {
      if (b.held) { this._updateHeldBall(b); continue; }
      const res = b.step(h, this);
      this._onWallHits(b, res.hits);

      // Paddle (top and side/bottom bounces)
      const pb = paddleBounce(b, this.paddle);
      if (pb) {
        if (pb.underside) {
          this.audio.paddle(b.x, pb.offset01, this.overdrive.band, false);
        } else {
          this._onPaddleBounce(b, pb);
        }
      }

      this._ballBrickCollide(b);

      // Ball-ball elastic
      if (this.balls.length > 1) {
        for (const o of this.balls) {
          if (o === b || o.held) continue;
          if (ballBall(b, o)) {
            this.audio.wall((b.x + o.x) / 2, this.overdrive.band, 0.25);
            this.fx.sparks((b.x + o.x) / 2, (b.y + o.y) / 2, '#bfe9ff', 4);
          }
        }
      }
      b.enforceConstraints();
    }
  }

  _updateHeldBall(b) {
    const p = b.paddle;
    if (!p) { b.held = false; return; }
    b.heldOffset = clamp(b.heldOffset, -p.w / 2 + 10, p.w / 2 - 10);
    b.x = p.x + b.heldOffset;
    b.y = p.y - p.h / 2 - b.r - 2;
    b.vx = p.vx;
    b.vy = 0;
  }

  _onWallHits(b, hits) {
    const band = this.overdrive.band;
    for (const hit of hits) {
      this.audio.wall(hit.x, band, hit.side === 'top' ? 0.3 : 0.2);
      this.fx.sparks(hit.x, hit.y, '#8fd8ff', 3);
      if (band >= 4) this.fx.ring(hit.x, hit.y, '#9df2ff', 40);
    }
  }

  _onPaddleBounce(b, pb) {
    this.combo = 0;
    this.paddle.squash = 1;
    const band = this.overdrive.band;
    this.audio.paddle(b.x, pb.offset01, band, pb.slammed);
    if (pb.slammed) {
      this.fx.addShake(6 + this.overdrive.juice * 4);
      this.fx.sparks(b.x, b.y + b.r, '#ffffff', 16);
      this.fx.text(b.x, b.y - 34, 'SLAM!', { color: '#ffffff', size: 26, bold: true, life: 0.5 });
    } else {
      this.fx.addShake(2);
      this.fx.sparks(b.x, b.y + b.r, '#7df9ff', 6);
    }
    if (this.overdrive.juice >= 0.6) this.fx.sparks(b.x, b.y + b.r, '#ffe27a', 8);
  }

  // ------------------------------------------------------- brick collisions

  _ballBrickCollide(b) {
    const list = this.bricks;
    for (let i = 0; i < list.length; i++) {
      const br = list[i];
      if (!br.alive) continue;
      if (Math.abs(br.x - b.x) > br.w / 2 + b.r + 6) continue;
      if (Math.abs(br.y - b.y) > br.h / 2 + b.r + 6) continue;

      // One-way ramps: no break, deflection only
      if (br.type === 'ramp') {
        if (rampCollide(b, br)) {
          this.audio.clink(b.x, this.overdrive.band, false);
          this.fx.sparks(b.x, b.y + b.r, '#c9d4e0', 4);
          b.enforceConstraints();
        }
        continue;
      }

      const hit = circleRect(b.x, b.y, b.r, br.rect);
      if (!hit) continue;

      // Locked lock: clink, normal bounce, no break
      if (br.type === 'lock' && this.lockedPairs.has(br.pair)) {
        this.audio.clink(b.x, this.overdrive.band, true);
        this.fx.sparks(hit.px, hit.py, br.style.glow, 4);
        if (hit.nx || hit.ny) reflectBall(b, hit.nx, hit.ny);
        b.x += hit.nx * 0.6; b.y += hit.ny * 0.6;
        b.enforceConstraints();
        continue;
      }

      // Gated bricks (angle / speed): fail the gate -> clink + normal bounce
      const gated = (br.type === 'angle' && !br.gatePasses(b)) ||
        (br.type === 'speed' && b.speed < BRICK_RULES.SPEED_THRESHOLD) ||
        (br.type === 'key' && !br.gatePasses(b));
      if (gated) {
        this.audio.clink(b.x, this.overdrive.band, br.type === 'speed');
        this.fx.sparks(hit.px, hit.py, br.style.glow, 4);
        if (hit.nx || hit.ny) reflectBall(b, hit.nx, hit.ny);
        b.x += hit.nx * 0.6; b.y += hit.ny * 0.6;
        b.enforceConstraints();
        continue;
      }

      // Mirror: vx = -vx, then breaks
      if (br.type === 'mirror') {
        b.vx = -b.vx;
        b.enforceConstraints();
        this.breakBrick(br, { x: br.x, y: br.y, ball: b });
        break;
      }

      // Normal breakable: reflect off the face, then break
      if (hit.nx || hit.ny) reflectBall(b, hit.nx, hit.ny);
      b.x += hit.nx * 0.6; b.y += hit.ny * 0.6;
      b.enforceConstraints();
      this.breakBrick(br, { x: br.x, y: br.y, ball: b });
      break;
    }
  }

  // Break a brick: scoring (× multiplier), combo, FX, sound, key/lock unlock,
  // bomb -> minis, and a possible power-up drop. `cheat` = demo force-break.
  breakBrick(br, opts = {}) {
    if (!br || !br.alive) return;
    const { x = br.x, y = br.y, ball } = opts;
    const band = this.overdrive.band;
    const mult = this.overdrive.multiplier;
    const pts = br.points * mult;
    this.score += pts;

    // Audio + FX per type
    this.audio.brick(br.type, x, band);
    const juice = this.overdrive.juice;
    if (br.type === 'glass') {
      this.fx.shards(x, y, '#cfeaff', 10, juice);
      this.fx.sparks(x, y, '#ffffff', 8);
    } else if (br.type === 'bomb') {
      this.fx.shards(x, y, '#ff5a6a', 14, juice);
      this.fx.smoke(x, y, 8);
      this.fx.sparks(x, y, '#ffb08a', 14);
      this.fx.addShake(FXCFG.SHAKE_MAX);
      this.fx.addHitStop(FXCFG.HITSTOP_BOMB);
    } else if (br.type === 'key') {
      this.fx.shards(x, y, '#ffe98a', 12, juice);
      this.fx.sparks(x, y, '#fff2b0', 12);
      this.fx.addShake(5);
    } else {
      this.fx.shards(x, y, br.style.glow, 9, juice);
      this.fx.addShake(FXCFG.SHAKE_MIN + juice * 4);
    }
    this.fx.addHitStop(FXCFG.HITSTOP_BRICK);

    // Floating score text (bigger in high bands)
    this.fx.text(x, y - 8, '+' + pts, {
      color: br.style.glow,
      size: 18 + band * 2 + juice * 8,
      life: 0.7 + juice * 0.3
    });

    br.alive = false;

    // Key unlocks matching locks
    if ((br.type === 'key' || br.type === 'angle' || br.type === 'speed') && br.pair >= 0) {
      this.lockedPairs.delete(br.pair);
      this.fx.text(x, y - 40, 'LOCK OPEN!', { color: '#ffce9a', size: 24, bold: true, life: 0.9 });
      this._refreshMagnetBricks();
    }
    // Magnet removal refresh
    if (br.type === 'magnet') this._refreshMagnetBricks();

    // Bomb spawns 4 mini-bricks in a cross (offset from grid, floating)
    if (br.type === 'bomb') this._spawnMinis(br);

    // Combo (consecutive breaks without a paddle touch)
    if (!opts.cheat) {
      this.combo++;
      if (this.combo > 0 && this.combo % FXCFG.COMBO_EVERY === 0) {
        const bonus = FXCFG.COMBO_BONUS_PER_LEVEL * this.level * mult;
        this.score += bonus;
        this.fx.text(FIELD.W / 2, 320, 'COMBO x' + this.combo, {
          color: '#7dffa0', size: 30 + band * 2, bold: true, life: 1.0
        });
        this.audio.powerup(x, band);
        this.fx.addShake(4);
      }
    }

    // Demo hook
    if (this.demo) this.demo.onBrickBroke();

    // Power-up drop (~22% of breakables, not mini/ramp)
    if (!opts.cheat && br.type !== 'mini' && br.type !== 'ramp' &&
        Math.random() < POWERUPS.DROP_CHANCE) {
      this._dropPowerUp(br);
    }
  }

  _spawnMinis(br) {
    const cols = [1, -1, 0, 0], rows = [0, 0, 1, -1];
    for (let i = 0; i < 4; i++) {
      const mc = br.col + cols[i], mr = br.row + rows[i];
      if (mc < 0 || mc >= 20 || mr < 0 || mr >= 8) continue;
      // Don't clobber an existing brick at that cell
      const occupied = this.bricks.find((o) => o.alive && o.col === mc && o.row === mr);
      if (occupied) continue;
      const mini = new Brick(mc, mr, 'mini', {
        w: BRICK_RULES.MINI_W, h: BRICK_RULES.MINI_H
      });
      mini.setPos(mc, mr);
      this.bricks.push(mini);
    }
  }

  _dropPowerUp(br) {
    const keys = Object.keys(POWERUPS.TYPES);
    // Weighted-ish: bias toward MULTI/WIDE/BOOST early, LASER/MAGNET later
    const type = keys[Math.floor(Math.random() * keys.length)];
    this.powerups.push(new PowerUp(br.x, br.y, type));
  }

  // -------------------------------------------------- frame-rate updates

  _updateFrame(dt) {
    // Power-up drops
    for (let i = this.powerups.length - 1; i >= 0; i--) {
      const p = this.powerups[i];
      p.update(dt, FIELD.H);
      if (!p.alive) { this.powerups.splice(i, 1); continue; }
      // Catch on paddle
      const pr = this.paddle.rect;
      if (p.y + p.size / 2 >= pr.y && p.y - p.size / 2 <= pr.y + pr.h &&
          p.x + p.size / 2 >= pr.x && p.x - p.size / 2 <= pr.x + pr.w) {
        this._applyPowerUp(p.type, p.x, p.y);
        this.powerups.splice(i, 1);
      }
    }

    // Lasers
    if (this.laserUntil > this.time) {
      this.laserTimer -= dt;
      if (this.laserTimer <= 0) {
        this.laserTimer = POWERUPS.TYPES.LASER.interval;
        const hx = this.paddle.w / 2 - 14;
        this.lasers.push(new Laser(this.paddle.x - hx, this.paddle.y - this.paddle.h / 2));
        this.lasers.push(new Laser(this.paddle.x + hx, this.paddle.y - this.paddle.h / 2));
        this.audio.laser(this.overdrive.band);
      }
    }
    for (let i = this.lasers.length - 1; i >= 0; i--) {
      const l = this.lasers[i];
      l.update(dt);
      if (!l.alive) { this.lasers.splice(i, 1); continue; }
      // Laser-brick collisions
      for (const br of this.bricks) {
        if (!br.alive || !br.laserBreakable(this)) continue;
        if (Math.abs(br.x - l.x) > br.w / 2 + l.w / 2) continue;
        if (l.y > br.y + br.h / 2 || l.y + l.h < br.y - br.h / 2) continue;
        this.breakBrick(br, { x: br.x, y: br.y, ball: null });
        l.alive = false;
        break;
      }
    }

    // Overdrive meter: does any ball sit in the zone (y < 260) AND above bricks?
    const anyInZone = this.balls.some((b) => !b.held && b.y < 260);
    const banked = this.overdrive.update(dt, anyInZone);
    if (banked) {
      // (bonus + fanfare handled in the events loop in _loop)
    }
  }

  _applyPowerUp(type, x, y) {
    const band = this.overdrive.band;
    this.audio.powerup(x, band);
    this.paddle.flash = 1;
    this.fx.sparks(x, y, POWERUPS.TYPES[type].color, 14);
    this.fx.text(x, y - 30, POWERUPS.TYPES[type].name, {
      color: POWERUPS.TYPES[type].color, size: 26, bold: true, life: 1.0
    });
    switch (type) {
      case 'MULTI': {
        // +2 balls, varied angles (cap 6)
        for (let i = 0; i < 2; i++) {
          if (this.balls.length >= BALL.MAX_BALLS) break;
          const src = this.balls[0] || this.holdBall;
          const speed = clamp(levelBallSpeed(this.level), BALL.MIN_SPEED, BALL.MAX_SPEED);
          const ang = -Math.PI / 2 + (i === 0 ? -0.35 : 0.35) + rand(-0.15, 0.15);
          const nb = new Ball(
            this.paddle.x + (i === 0 ? -20 : 20),
            this.paddle.y - PADDLE.H / 2 - BALL.R - 2,
            speed, ang);
          if (src) { nb.vx = src.vx * 0.5 + Math.cos(ang) * speed * 0.5; nb.vy = -Math.abs(Math.sin(ang)) * speed; }
          this.balls.push(nb);
        }
        this.ballState = BALL_STATE.LIVE;
        break;
      }
      case 'WIDE':
        this.wideUntil = this.time + POWERUPS.TYPES.WIDE.duration;
        break;
      case 'SLOW':
        this.slowUntil = this.time + POWERUPS.TYPES.SLOW.duration;
        for (const b of this.balls) b.setSpeed(Math.max(BALL.MIN_SPEED, b.speed * POWERUPS.TYPES.SLOW.factor));
        break;
      case 'LASER':
        this.laserUntil = this.time + POWERUPS.TYPES.LASER.duration;
        this.laserTimer = 0;
        break;
      case 'MAGNET':
        this.magnetPowerUntil = this.time + POWERUPS.TYPES.MAGNET.duration;
        break;
      case 'BOOST':
        // Instant +20 overdrive meter (may bank)
        this.overdrive.meter = clamp(this.overdrive.meter + POWERUPS.TYPES.BOOST.meter, 0, 100);
        if (this.overdrive.meter >= 100 && this.overdrive.canBank) {
          const bonus = OVERDRIVE.BANK_BONUS_PER_LEVEL * this.level;
          this.score += bonus;
          this.overdrive.banked = true;
          this.overdrive.canBank = false;
          this.fx.text(x, y - 60, 'BOOST!', { color: '#ffa24d', size: 30, bold: true, life: 1.0 });
        }
        break;
    }
  }

  // -------------------------------------------------------- life & level

  _removeLostBalls(handleDeath = true) {
    for (let i = this.balls.length - 1; i >= 0; i--) {
      const b = this.balls[i];
      if (!b.held && b.y > FIELD.BALL_LOST_Y) {
        this.balls.splice(i, 1);
        if (b === this.holdBall) this.holdBall = null;
      }
    }
  }

  _checkLifeLost() {
    if (this.state === STATE.PLAY) {
      if (this.balls.length === 0) {
        this._onAllBallsLost();
      } else if (this.holdBall === null && this.balls.length > 0) {
        this.ballState = BALL_STATE.LIVE;
      }
      return;
    }
    // Demo (MENU / GAMEOVER): keep the attract running — respawn a held
    // ball after a beat so the demo pilot can launch it.
    if (this.balls.length === 0 && this.holdBall === null) {
      this._demoRespawn = (this._demoRespawn || 0) + 1;
      if (this._demoRespawn >= 45) { // ~0.75s at 60fps
        this._demoRespawn = 0;
        this.spawnHeldBall();
      }
    } else {
      this._demoRespawn = 0;
    }
  }

  _onAllBallsLost() {
    this.lives--;
    this.combo = 0;
    this.overdrive.reset();       // all balls lost -> meter to 0
    this.audio.lifeLost(this.overdrive.band);
    this.fx.flashScreen('255,80,90', 0.6);
    this.fx.addShake(FXCFG.SHAKE_MAX);
    if (this.lives <= 0) {
      this.balls.length = 0;
      this.setState(STATE.GAMEOVER);
    } else {
      // Respawn held on paddle
      this.balls.length = 0;
      this.holdBall = null;
      this.wideUntil = 0; this.laserUntil = 0; this.slowUntil = 0; this.magnetPowerUntil = 0;
      this.spawnHeldBall();
    }
  }

  _checkLevelClear() {
    if (this.state !== STATE.PLAY) return;
    // Level clears when every breakable brick is gone (locks count once
    // their key has been collected).
    const anyBreakable = this.bricks.some((b) => {
      if (!b.alive) return false;
      if (b.type === 'ramp') return false;
      if (b.type === 'lock' && this.lockedPairs.has(b.pair)) return false;
      return true;
    });
    if (!anyBreakable) {
      this.setState(STATE.LEVELCLEAR);
    }
  }

  _activePowerups() {
    const out = {};
    const t = this.time;
    if (this.wideUntil > t) out.WIDE = { remaining: this.wideUntil - t, total: POWERUPS.TYPES.WIDE.duration };
    if (this.slowUntil > t) out.SLOW = { remaining: this.slowUntil - t, total: POWERUPS.TYPES.SLOW.duration };
    if (this.laserUntil > t) out.LASER = { remaining: this.laserUntil - t, total: POWERUPS.TYPES.LASER.duration };
    if (this.magnetPowerUntil > t) out.MAGNET = { remaining: this.magnetPowerUntil - t, total: POWERUPS.TYPES.MAGNET.duration };
    return out;
  }

  _loadBest() {
    try { return parseInt(localStorage.getItem(STORAGE_KEYS.BEST) || '0', 10) || 0; }
    catch (e) { return 0; }
  }
  _saveBest() {
    try { localStorage.setItem(STORAGE_KEYS.BEST, String(Math.floor(this.best))); }
    catch (e) { /* ignore */ }
  }

  // ---------------------------------------------------------------- layout

  _resize() {
    const dpr = Math.min(2, (window.devicePixelRatio || 1));
    this.dpr = dpr;
    const cssW = this.canvas.clientWidth || window.innerWidth;
    const cssH = this.canvas.clientHeight || window.innerHeight;
    this.canvas.width = Math.max(1, Math.round(cssW * dpr));
    this.canvas.height = Math.max(1, Math.round(cssH * dpr));
    // Letterbox the 1500x1000 field into the CSS viewport (canvas-relative px)
    const scale = Math.min(cssW / FIELD.W, cssH / FIELD.H);
    this.transform = {
      scale,
      tx: (cssW - FIELD.W * scale) / 2,
      ty: (cssH - FIELD.H * scale) / 2
    };
  }

  // --------------------------------------------------------------- render

  _render() {
    const ctx = this.ctx;
    const { scale, tx, ty } = this.transform;
    ctx.setTransform(this.dpr, 0, 0, this.dpr, 0, 0);
    // Clear full canvas (letterbox bars)
    ctx.fillStyle = '#05070f';
    ctx.fillRect(0, 0, this.canvas.width / this.dpr, this.canvas.height / this.dpr);

    // Field space (with screen shake)
    const sx = this.fx.shakeX, sy = this.fx.shakeY;
    ctx.setTransform(
      this.dpr * scale, 0, 0, this.dpr * scale,
      this.dpr * (tx + sx * scale), this.dpr * (ty + sy * scale));

    const juice = this.overdrive.juice;

    // Background (clipped to field)
    ctx.save();
    ctx.beginPath();
    ctx.rect(0, 0, FIELD.W, FIELD.H);
    ctx.clip();

    this.fx.drawBackground(ctx, juice, this.time);

    // Bricks
    for (const br of this.bricks) if (br.alive) br.draw(ctx, this.time, this);

    // Power-ups
    for (const p of this.powerups) p.draw(ctx, this.time);
    // Lasers
    for (const l of this.lasers) l.draw(ctx);

    // Particles (normal + additive pass)
    this.fx.drawParticles(ctx, true);

    // Paddle
    this.paddle.draw(ctx, juice);

    // Balls (glow sprite uses additive for the halo)
    for (const b of this.balls) {
      ctx.save();
      ctx.globalCompositeOperation = 'lighter';
      b.draw(ctx, this.fx.ballGlow, juice, this.time);
      ctx.restore();
    }

    // Floating text
    this.fx.drawTexts(ctx);

    this.fx.drawVignette(ctx);
    this.fx.drawFlash(ctx);
    ctx.restore();

    // Field border (subtle neon frame)
    ctx.strokeStyle = `rgba(90,200,255,${0.25 + juice * 0.4})`;
    ctx.lineWidth = 2;
    ctx.strokeRect(1, 1, FIELD.W - 2, FIELD.H - 2);
  }
}

export default Game;
