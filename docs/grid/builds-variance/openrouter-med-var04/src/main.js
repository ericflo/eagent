// main.js — boot, game state machine (menu → levelIntro → playing → paused /
// gameOver / win), fixed-step physics wiring, frenzy + combo + scoring logic,
// debug hooks for the smoke test. Entry point loaded by index.html.
import { Engine, Input, WORLD_W, WORLD_H, BAND_TOP, BAND_BOTTOM, clamp, lerp, STEP } from './engine.js';
import { Paddle } from './paddle.js';
import { BallSet, BASE_SPEED, MAX_SPEED } from './balls.js';
import { BrickField, IMPACT_SPEED } from './bricks.js';
import { Powerups, DROPS } from './powerups.js';
import { Effects } from './effects.js';
import { Audio } from './audio.js';
import { UI } from './ui.js';
import { LEVELS, parseLevel, CELL_W, BRICK_VALUES } from './levels.js';

const HIGH_KEY = 'breakthrough.highscore';

class Game {
  constructor() {
    this.canvas = document.getElementById('game');
    this.engine = new Engine(this.canvas);
    this.input = new Input(this.engine);
    this.audio = new Audio();
    this.ui = new UI();
    this.effects = new Effects();

    this.state = 'menu';        // menu | levelIntro | playing | paused | gameOver | win
    this.score = 0;
    this.lives = 3;
    this.level = 0;             // 0-based index into LEVELS
    this.highScore = parseInt(localStorage.getItem(HIGH_KEY) || '0', 10) || 0;

    this.paddle = new Paddle();
    this.balls = new BallSet();
    this.field = null;
    this.powerups = new Powerups();

    this.combo = 0;             // breaks without paddle contact
    this.multiplier = 1;
    this.frenzyIntensity = 0;   // 0..1, drives audio + HUD meter
    this.frenzyCap = 8;         // prisms raise this by +0.5 each
    this.prismBonus = 0;        // sum of +0.5 per prism broken this level

    this.stateTimer = 0;        // seconds spent in current state
    this.totalBricksBroken = 0; // lifetime counter for the win screen
    this.finalLevel = 1;
    this.bricksBroken = 0;
    this.pendingServe = false;
    this.wakeTime = 0;          // wall-clock when visibility resumes
    this.tSec = 0;              // world clock for ghost phasing etc.

    this.engine.update = (dt) => this.tick(dt);
    this.engine.render = (a, ctx) => this.draw(ctx, a);

    // pause on tab hide
    document.addEventListener('visibilitychange', () => {
      if (document.hidden && this.state === 'playing') this.setState('paused');
    });

    // resume audio context on any gesture
    const gesture = () => { this.audio.init(); };
    window.addEventListener('pointerdown', gesture, { once: false });
    window.addEventListener('keydown', gesture, { once: false });

    this.exposeDebug();
    this.engine.start();
  }

  // --- debug/test contract --------------------------------------------------
  exposeDebug() {
    const g = this;
    window.__game = {
      get state() { return g.state; },
      get score() { return g.score; },
      get lives() { return g.lives; },
      get level() { return g.level + 1; },
      get balls() {
        return g.balls.balls.filter(b => b.alive).map(b => ({
          get x() { return b.x; }, set x(v) { g.setBall(b, { x: v }); },
          get y() { return b.y; }, set y(v) { g.setBall(b, { y: v }); },
          get vx() { return b.vx; }, set vx(v) { g.setBall(b, { vx: v }); },
          get vy() { return b.vy; }, set vy(v) { g.setBall(b, { vy: v }); },
          alive: true,
        }));
      },
      get bricks() {
        return g.field ? g.field.bricks.map(b => ({ alive: b.alive, steel: b.steel, type: b.type })) : [];
      },
      serve: () => g.serveBall(),
      debug: {
        givePowerup: (name) => g.grantPowerup(name),
        loadLevel: (n) => {
          if (typeof n !== 'number' || n < 0 || n >= LEVELS.length) return;
          g.level = Math.floor(n);
          g.loadLevel();
          g.setState('playing');
          g.pendingServe = true;
        },
        // aimed sticky release: launch toward a world-space point, clamped mostly-upward
        aimRelease: (tx, ty) => g.releaseHeldAimed(tx, ty),
        pointer: () => ({ ...g.input.pointer }),
      },
    };
    this.ctxRef = () => this.canvas.getContext('2d');
    // expose ctx for ui.banner
    window.__game.ctx = this.canvas.getContext('2d');
    window.__game.audio = this.audio;
  }

  // Debug teleport: set fields directly and sync speed, no serve/ai override
  setBall(b, patch) {
    if ('x' in patch) { b.x = patch.x; b.prevX = patch.x; }
    if ('y' in patch) { b.y = patch.y; b.prevY = patch.y; }
    if ('vx' in patch) b.vx = patch.vx;
    if ('vy' in patch) b.vy = patch.vy;
    b.speed = Math.hypot(b.vx, b.vy);
  }

  grantPowerup(name) {
    if (name === 'life') { this.lives = Math.min(5, this.lives + 1); return; }
    if (name === 'frenzy') { this.applyFrenzyOrb(); return; }
    if (name === 'multi') { this.applyMulti(); return; }
    if (DROPS[name] && DROPS[name].dur > 0) {
      this.balls.applyEffect(name, DROPS[name].dur);
    }
  }

  // --- state machine --------------------------------------------------------
  setState(s) {
    if (this.state === s) return;
    this.state = s;
    this.stateTimer = 0;
    if (s === 'menu') {
      this.balls = new BallSet();
      this.field = null;
      this.combo = 0; this.multiplier = 1; this.frenzyIntensity = 0;
      this.audio.setFrenzy(0);
    }
  }

  startGame() {
    this.score = 0;
    this.lives = 3;
    this.level = 0;
    this.frenzyCap = 8;
    this.loadLevel();
    this.setState('levelIntro');
  }

  loadLevel() {
    const parsed = parseLevel(LEVELS[this.level]);
    this.field = new BrickField(parsed.bricks, parsed.topY);
    this.balls = new BallSet();
    this.powerups = new Powerups();
    this.combo = 0;
    this.multiplier = 1;
    this.frenzyIntensity = 0;
    this.frenzyCap = 8 + this.prismBonus;   // prism bonus persists within a level only; reset per level
    this.prismBonus = 0;
    this.audio.setFrenzy(0);
    this.pendingServe = true;
  }

  // Sticky release aimed at a world-space target, clamped so the ball always
  // launches mostly upward (vy <= -minUp) — it can never fire sideways/down.
  releaseHeldAimed(tx, ty) {
    const b = this.balls.heldBall;
    if (!b) return null;
    let vx = 0, vy = -BASE_SPEED;
    if (tx != null && ty != null && (tx !== b.x || ty !== b.y)) {
      const dx = tx - b.x, dy = ty - b.y;
      const len = Math.hypot(dx, dy) || 1;
      vx = dx / len * BASE_SPEED;
      vy = dy / len * BASE_SPEED;
      const minUp = -BASE_SPEED * 0.35; // |vy| must be at least 35% of speed
      if (vy > minUp) {
        // re-clamp angle so it's steeply upward, keeping horizontal direction
        const ang = Math.atan2(vy, vx);
        const clamped = Math.max(Math.min(ang, -0.35), -Math.PI + 0.35);
        vx = Math.cos(clamped) * BASE_SPEED;
        vy = Math.sin(clamped) * BASE_SPEED;
      }
      const sp = Math.hypot(vx, vy);
      vx = vx / sp * BASE_SPEED; vy = vy / sp * BASE_SPEED;
    }
    return this.balls.releaseHeld(vx, vy);
  }

  serveBall() {
    if (this.state === 'paused') return;
    if (this.state === 'menu' || this.state === 'gameOver' || this.state === 'win') return;
    if (this.state === 'levelIntro') { this.setState('playing'); }
    if (!this.field) return;
    // launch any held ball, else spawn a fresh one from the paddle
    const held = this.balls.heldBall;
    if (held) {
      // aimed release: toward last pointer position (mouse pointer or touch
      // stick knob) if any, else straight up
      const aim = this.input.lastAim();
      if (aim) this.releaseHeldAimed(aim.x, aim.y);
      else this.balls.releaseHeld(0, -BASE_SPEED);
      this.audio.serve();
      return;
    }
    if (this.balls.live().length === 0) {
      const p = this.paddle;
      this.balls.spawn(p.x + p.w / 2, p.y - 12, (Math.random() - 0.5) * 160, -BASE_SPEED);
      this.audio.serve();
      this.pendingServe = false;
    }
  }

  applyMulti() {
    const live = this.balls.live();
    if (!live.length) return;
    const src = live[0];
    for (let k = 0; k < 2; k++) {
      const ang = Math.atan2(src.vy, src.vx) + (k === 0 ? 0.4 : -0.4);
      const sp = Math.hypot(src.vx, src.vy);
      this.balls.spawn(src.x, src.y, Math.cos(ang) * sp, Math.sin(ang) * sp);
    }
  }

  applyFrenzyOrb() {
    // instant +ball speed boost and launch a second ball aimed upward
    const live = this.balls.live();
    if (live.length) {
      const src = live[0];
      this.balls.spawn(src.x, src.y, (Math.random() - 0.5) * 120, -BASE_SPEED * 1.1);
    }
    this.audio.chime();
    this.effects.doFlash('255,215,0', 0.5);
    this.effects.popup(this.paddle.x + this.paddle.w / 2, this.paddle.y - 40, 'FRENZY ORB', '#fbbf24', 20);
  }

  grantEffect(name) {
    this.audio.init();
    this.audio.chime();
    this.paddle.pickupFlash = 1.2;
    this.paddle.pickupColor = DROPS[name].color;
    if (name === 'life') {
      this.lives = Math.min(5, this.lives + 1);
      this.effects.popup(this.paddle.x + this.paddle.w / 2, this.paddle.y - 30, '+1 LIFE', DROPS.life.color, 20);
      return;
    }
    if (name === 'frenzy') { this.applyFrenzyOrb(); return; }
    if (name === 'multi') {
      this.applyMulti();
      this.effects.popup(this.paddle.x + this.paddle.w / 2, this.paddle.y - 30, 'MULTI BALL', DROPS.multi.color, 20);
      return;
    }
    this.balls.applyEffect(name, DROPS[name].dur);
    this.effects.popup(this.paddle.x + this.paddle.w / 2, this.paddle.y - 30, DROPS[name].label, DROPS[name].color, 20);
  }

  // --- scoring / combo / frenzy ----------------------------------------------
  addScore(base, x, y, opts = {}) {
    this.combo += 1;
    if (opts.brick) this.totalBricksBroken += 1;
    const comboMult = 1 + Math.min(this.combo - 1, 9) * 0.25; // combo multiplies score
    const frenzyMult = this.multiplier;
    const pts = Math.round(base * comboMult * frenzyMult * (opts.bonus || 1));
    this.score += pts;
    if (this.state !== 'levelIntro') {
      this.effects.popup(x, y, `+${pts}`, opts.color || '#fff', opts.big ? 24 : 18, this.state === 'levelIntro' ? 0.6 : 1.0);
    }
    this.audio.brickPop(this.combo);
  }

  breakCombo() {
    this.combo = 0;
  }

  computeFrenzy(dt) {
    // frenzy active if any live ball is above the line
    const line = this.field ? this.field.frenzyLine : 0;
    const up = this.balls.live().filter(b => b.y < line).length;
    const n = this.balls.live().length || 1;
    const target = clamp(up / n, 0, 1);
    if (up > 0) {
      // ramps up every second toward cap; faster with more balls up top
      this.frenzyIntensity = Math.min(1, this.frenzyIntensity + dt * (0.25 + 0.35 * target));
    } else {
      this.frenzyIntensity = Math.max(0, this.frenzyIntensity - dt / 3); // ~3s decay
    }
    this.multiplier = Math.round(lerp(1, this.frenzyCap, this.frenzyIntensity) * 10) / 10;
    if (this.multiplier < 1) this.multiplier = 1;
    this.audio.setFrenzy(this.frenzyIntensity * (this.multiplier / this.frenzyCap));
    this.effects.frenzyGlow = this.frenzyIntensity;
    if (this.frenzyIntensity > 0) this.effects.frenzyRain(this.frenzyIntensity, dt);
  }

  // --- per-step game logic ----------------------------------------------------
  tick(dt) {
    // slow-mo time scale
    this.engine.timeScale = this.effects.slowMo > 0 ? 0.25 : (this.effects.hitStop > 0 ? 0 : 1);
    this.tSec += dt;
    this.stateTimer += dt;
    this.effects.update(dt);
    if (this.state === 'menu') { this.handleMenuTap(); return; }
    if (this.state === 'levelIntro') {
      this.handleIntroTap();
      if (this.stateTimer > 6) this.setState('playing');
      return;
    }
    if (this.state === 'gameOver' || this.state === 'win') {
      const tap = this.input.consumeTap();
      if (tap && this.stateTimer > 1.2) { this.setState('menu'); this.audio.uiClick(); }
      return;
    }
    if (this.state === 'paused') {
      const tap = this.input.consumeTap();
      if (tap) { this.setState('playing'); this.audio.uiClick(); }
      return;
    }

    // playing
    if (this.input.consumeTap() && this.pendingServe) { this.serveBall(); }

    // keyboard serve: guard so a held Space only serves once per press
    if (this.input.keys.has('Space') && !this._spaceWasDown) {
      if (this.pendingServe || this.balls.heldBall) this.serveBall();
    }
    this._spaceWasDown = this.input.keys.has('Space');
    // paddle from input
    const kb = 420 * dt;
    const target = this.input.paddleTarget(this.paddle.x, this.paddle.y, kb, kb * 0.6);
    // pointer marks the paddle's center, not its left edge
    target.x -= this.paddle.w / 2;
    target.y -= this.paddle.h / 2;
    this.paddle.step(target);
    if (this.input.consumeFlick()) this.paddle.charged = 1.5;

    if (this.field) this.field.stepSliders(dt);
    this.computeFrenzy(dt);

    if (this.pendingServe || this.balls.live().length === 0) {
      // waiting for serve; ball may be held
      if (this.balls.heldBall) {
        this.balls.heldBall.prevX = this.balls.heldBall.x = this.paddle.x + this.paddle.w / 2;
        this.balls.heldBall.prevY = this.balls.heldBall.y = this.paddle.y - 12;
      }
      this.checkLevelCleared();
      return;
    }

    this.balls.step(
      this.paddle,
      { left: () => {}, right: () => this.audio.tink(), top: () => {} },
      (b) => this.onBallLost(b),
      (b) => this.ballPaddle(b),
      (b) => this.ballBricks(b),
      this.effects, this.audio, this.tSec
    );

    this.powerups.step((d) => {
      const p = this.paddle;
      if (d.y > p.y - 20 && d.y < p.y + p.h + 26 && d.x > p.x - 18 && d.x < p.x + p.w + 18) {
        this.grantEffect(d.kind);
        return true;
      }
      return false;
    });

    this.checkLevelCleared();
    this.hudEffects = this.powerups.hudEffects(this.balls);
    this.ui.update(this);
  }

  ballPaddle(ball) {
    const p = this.paddle;
    if (this.balls.heldBall) return;
    if (ball.vy <= 0) return; // only when moving down
    const r = p.rect(1);
    if (ball.y + ball.r < r.y || ball.y - ball.r > r.y + r.h ||
        ball.x + ball.r < r.x || ball.x - ball.r > r.x + r.w) return;

    if (this.balls.stickyTimer > 0 && !this.balls.heldBall) {
      this.balls.catchBall(ball, p);
      this.audio.thwock(false);
      return;
    }
    const smash = p.isSmash();
    const v = p.velocity();
    let vx = ball.vx, vy = ball.vy;
    let speed = Math.hypot(vx, vy);
    if (smash) speed = Math.min(MAX_SPEED, speed * 1.25);
    // english: horizontal paddle velocity shifts the reflection angle
    const english = clamp(v.vx / 900, -0.45, 0.45);
    let ang = Math.atan2(-Math.abs(vy), vx * 0.3 + english * speed);
    // avoid too-horizontal angles
    ang = clamp(ang, -Math.PI + 0.35, -0.35);
    vx = Math.cos(ang) * speed;
    vy = Math.sin(ang) * speed;
    this.balls.setBallState(ball, ball.x, r.y - ball.r, vx, vy);
    ball.y = r.y - ball.r - 0.5;
    this.breakCombo();
    this.audio.thwock(smash);
    if (smash) {
      p.charged = 1.5;
      this.effects.burst(ball.x, r.y, '#f59e0b', 16, 320);
      this.effects.ring(ball.x, r.y, '#f59e0b', 70, 0.3);
      this.effects.addShake(4);
      this.effects.addHitStop(0.05);
      this.score += 25;
      this.effects.popup(ball.x, r.y - 30, 'SMASH! +25', '#f59e0b', 18);
    } else {
      this.effects.burst(ball.x, r.y, '#67e8f9', 6, 160);
    }
  }

  ballBricks(ball) {
    if (!this.field) return;
    const events = this.field.collide(ball, this.tSec);
    for (const ev of events) {
      const b = ev.brick;
      const cx = b.x + b.w / 2, cy = b.y + b.h / 2;
      switch (ev.kind) {
        case 'steel':
          this.audio.clank();
          this.effects.burst(ball.x, ball.y, '#cbd5e1', 6, 180);
          this.effects.addShake(2);
          this.breakCombo();
          break;
        case 'glance':
          this.audio.tink();
          this.effects.burst(cx, cy, '#5eead4', 5, 140);
          this.breakCombo();
          break;
        case 'too-slow':
          this.audio.thunk();
          this.effects.burst(cx, cy, '#f87171', 4, 120);
          this.breakCombo();
          break;
        case 'shield':
          this.audio.tink();
          this.effects.burst(cx, cy, '#a3a3a3', 5, 140);
          this.breakCombo();
          break;
        case 'break': {
          const isVolatile = ev.special === 'volatile';
          const color = { prism: '#fcd34d', multiplier: '#60a5fa', volatile: '#fb7185' }[ev.special] || '#38bdf8';
          this.effects.shatter(b.x, b.y, b.w, b.h, color);
          this.effects.ring(cx, cy, color, isVolatile ? 170 : 90, 0.4);
          this.effects.addShake(isVolatile ? 9 : 4);
          if (ev.special === 'facet') this.effects.addHitStop(0.07);
          if (isVolatile) {
            this.effects.addHitStop(0.09);
            this.audio.boom(0);
            const chain = this.field.explode(b, this.field, 2.2 * CELL_W);
            chain.forEach((nb, i) => {
              if (nb.type === 'volatile') {
                // chain: second-level explosion
                setTimeout(() => {
                  this.effects.ring(nb.x + nb.w / 2, nb.y + nb.h / 2, '#fb7185', 200, 0.45);
                  this.audio.boom(1);
                }, 120 + i * 90);
              }
              this.effects.shatter(nb.x, nb.y, nb.w, nb.h, '#fb7185');
              this.addScore(BRICK_VALUES[nb.type] || 50, nb.x + nb.w / 2, nb.y, { bonus: 0.5, brick: true });
              if (nb.type === 'prism') { this.frenzyCap += 0.5; this.powerups.maybeDrop(nb.x + nb.w / 2, nb.y, 'prism'); }
              else this.powerups.maybeDrop(nb.x + nb.w / 2, nb.y, nb.type);
            });
          }
          this.addScore(ev.value, cx, cy - 10, { big: ev.special === 'multiplier', color: ev.special === 'multiplier' ? '#93c5fd' : '#fff', bonus: ev.special === 'multiplier' ? 2 : 1, brick: true });
          if (ev.special === 'prism') {
            this.frenzyCap += 0.5;
            this.powerups.maybeDrop(cx, cy, 'prism');
            this.audio.chime();
          } else {
            this.powerups.maybeDrop(cx, cy, ev.special);
          }
          if (ev.special === 'ghost') this.effects.addHitStop(0.06);
          break;
        }
      }
    }
  }

  onBallLost(b) {
    if (this.balls.live().length > 0) return; // other balls still going
    this.lives -= 1;
    this.breakCombo();
    this.audio.lifeLost();
    this.effects.doSlowMo(0.8);
    this.effects.desat = 1;
    this.effects.addShake(8);
    if (this.lives <= 0) {
      this.endGame(false);
    } else {
      this.pendingServe = true;
    }
  }

  checkLevelCleared() {
    if (!this.field) return;
    if (this.field.breakableLeft() > 0) return;
    // final brick: slow-mo + banner
    this.effects.doSlowMo(0.8);
    this.audio.levelClear();
    this.score += 500 * (this.level + 1);
    if (this.level >= LEVELS.length - 1) {
      this.endGame(true);
    } else {
      this.level += 1;
      this.loadLevel();
      this.setState('levelIntro');
    }
  }

  endGame(won) {
    const isNew = this.score > this.highScore;
    if (isNew) { this.highScore = this.score; localStorage.setItem(HIGH_KEY, String(this.score)); }
    this.won = won;
    this.wasNewHigh = isNew;
    this.finalLevel = this.level + 1;
    this.bricksBroken = this.totalBricksBroken;
    this.setState(won ? 'win' : 'gameOver');
    if (won) this.audio.levelClear(); else this.audio.gameOverSound();
  }

  handleMenuTap() {
    const tap = this.input.consumeTap();
    if (tap) {
      this.audio.init();
      this.audio.uiClick();
      this.startGame();
    }
  }

  handleIntroTap() {
    const tap = this.input.consumeTap();
    if (tap) { this.setState('playing'); this.audio.uiClick(); }
  }

  togglePause() {
    if (this.state === 'playing') { this.setState('paused'); this.audio.uiClick(); }
    else if (this.state === 'paused') { this.setState('playing'); this.audio.uiClick(); }
  }

  // --- render -----------------------------------------------------------------
  draw(ctx, alpha) {
    // background
    const warm = this.effects.frenzyGlow;
    const g = ctx.createLinearGradient(0, 0, 0, WORLD_H);
    g.addColorStop(0, warm > 0 ? mixHex('#0b1120', '#2a1608', warm * 0.6) : '#0b1120');
    g.addColorStop(1, warm > 0 ? mixHex('#1e293b', '#3a2210', warm * 0.5) : '#1e293b');
    ctx.fillStyle = g;
    ctx.fillRect(0, 0, WORLD_W, WORLD_H);

    // pulsing background grid
    if (this.state === 'playing' || this.state === 'levelIntro' || this.state === 'paused') {
      const pulse = 0.04 + warm * 0.10 * (0.6 + 0.4 * Math.sin(this.tSec * 3));
      ctx.strokeStyle = `rgba(148,163,184,${pulse})`;
      ctx.lineWidth = 1;
      for (let x = 0; x <= WORLD_W; x += 60) { ctx.beginPath(); ctx.moveTo(x, 0); ctx.lineTo(x, WORLD_H); ctx.stroke(); }
      for (let y = 0; y <= WORLD_H; y += 60) { ctx.beginPath(); ctx.moveTo(0, y); ctx.lineTo(WORLD_W, y); ctx.stroke(); }
    }

    if (this.state === 'menu') {
      this.ui.drawMenu(ctx, this.highScore);
      this.effects.draw(ctx, alpha);
      return;
    }
    if (this.state === 'gameOver' || this.state === 'win') {
      this.ui.drawEndScreen(ctx, this.state === 'win', this.score, this.highScore, this.wasNewHigh, { level: this.finalLevel, bricks: this.bricksBroken });
      return;
    }

    // frenzy line
    if (this.field) {
      const line = this.field.frenzyLine;
      const active = this.frenzyIntensity > 0.02;
      ctx.save();
      ctx.strokeStyle = active ? `rgba(251,191,36,${0.6 + 0.4 * Math.sin(this.tSec * 4) ** 2})` : 'rgba(251,191,36,0.45)';
      ctx.setLineDash([12, 10]);
      ctx.lineWidth = active ? 3.5 : 2;
      ctx.shadowColor = 'rgba(251,191,36,0.9)';
      ctx.shadowBlur = active ? 14 : 0;
      ctx.beginPath(); ctx.moveTo(0, line); ctx.lineTo(WORLD_W, line); ctx.stroke();
      ctx.setLineDash([]);
      ctx.shadowBlur = 0;
      ctx.fillStyle = active ? '#fbbf24' : 'rgba(251,191,36,0.8)';
      ctx.font = 'bold 13px system-ui, sans-serif';
      ctx.textAlign = 'left';
      ctx.fillText('FRENZY', 8, line - 7);
      ctx.restore();
    }

    if (this.field) this.field.render(ctx, this.tSec);
    this.powerups.render(ctx);
    this.balls.render(ctx, alpha, this.paddle);
    this.paddle.render(ctx, alpha);

    if (this.state === 'levelIntro') {
      this.ui.banner(`Level ${this.level + 1}: ${LEVELS[this.level].name}`, LEVELS[this.level].hint + '  —  tap to serve', this.stateTimer, 6);
    }

    // thumbstick indicator
    if (this.input.stick.active) {
      const s = this.input.stick;
      ctx.save();
      ctx.globalAlpha = 0.85;
      ctx.strokeStyle = '#a5f3fc'; ctx.lineWidth = 3.5;
      ctx.beginPath(); ctx.arc(s.originX, s.originY, 44, 0, Math.PI * 2); ctx.stroke();
      // spoke: thumb vector from origin, always visible
      ctx.globalAlpha = 0.9;
      ctx.beginPath(); ctx.moveTo(s.originX, s.originY); ctx.lineTo(s.x, s.y); ctx.stroke();
      ctx.globalAlpha = 1;
      ctx.shadowColor = '#67e8f9'; ctx.shadowBlur = 16;
      ctx.fillStyle = 'rgba(165,243,252,0.85)';
      ctx.beginPath(); ctx.arc(s.x, s.y, 20, 0, Math.PI * 2); ctx.fill();
      ctx.restore();
    }

    if (this.state === 'paused') {
      this.ui.drawPause(ctx);
    }

    this.effects.draw(ctx, alpha);
  }
}

function mixHex(a, b, t) {
  const pa = parseInt(a.slice(1), 16), pb = parseInt(b.slice(1), 16);
  const r = Math.round(lerp((pa >> 16) & 255, (pb >> 16) & 255, t));
  const g = Math.round(lerp((pa >> 8) & 255, (pb >> 8) & 255, t));
  const bl = Math.round(lerp(pa & 255, pb & 255, t));
  return `rgb(${r},${g},${bl})`;
}

// keyboard P/M handling (needs access to game instance)
window.addEventListener('keydown', (e) => {
  const g = window.__gameRef;
  if (!g) return;
  if (e.code === 'KeyP') g.togglePause();
  if (e.code === 'KeyM') {
    const m = g.audio.toggleMute();
    if (window.__game && window.__game.ui && window.__game.ui.muteBtn) {
      window.__game.ui.muteBtn.textContent = m ? '🔇' : '🔊';
    }
  }
});

const game = new Game();
window.__gameRef = game;
