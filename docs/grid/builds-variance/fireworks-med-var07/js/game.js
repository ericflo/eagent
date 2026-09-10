'use strict';
/* ============================================================
   Attic Breakout — game.js
   Game states, fixed-timestep loop, all collision physics,
   fever logic, scoring, power-up handling, persistence and the
   window.__GAME__ debug hook.
   ============================================================ */

const State = {
  TITLE: 'title', SETTINGS: 'settings', INTRO: 'intro', PLAY: 'play',
  STICKY_AIM: 'sticky_aim', PAUSE: 'pause', LEVEL_CLEAR: 'clear',
  DYING: 'dying', GAME_OVER: 'gameover', WIN: 'win',
};

class Game {
  constructor() {
    this.canvas = document.getElementById('game');
    this.ctx = this.canvas.getContext('2d');
    this.version = '1.0.0';

    // Scaling / DPR-aware letterbox
    this.scale = 1; this.offX = 0; this.offY = 0;
    this._resize = this._resize.bind(this);
    window.addEventListener('resize', this._resize);
    this._resize();

    // Systems
    this.audio = new AudioSys();
    this.pool = new ParticlePool(600);
    this.fx = new Effects();
    this.emitter = new Emitter(this.pool);
    this.popups = new Popups();
    this.field = new BrickField();
    this.input = new Input(this.canvas, this);
    this.ui = new UIRenderer(this);
    this.capsules = [];

    // Session
    const params = new URLSearchParams(location.search);
    this.debug = params.get('debug') === '1';
    const lvlParam = parseInt(params.get('level', '') || '0', 10);
    this.startLevel = Number.isFinite(lvlParam) && lvlParam > 0 ? Math.min(lvlParam - 1, CONFIG.LEVELS - 1) : 0;

    this.best = storageGet('ab.best', 0);
    this.resetRun();
    this.state = State.TITLE;
    this.demoBalls = [];
    for (let i = 0; i < 3; i++) {
      this.demoBalls.push(new Ball(rand(100, 620), rand(300, 700), rand(-260, 260), rand(-260, 260)));
    }

    // Loop
    this.time = 0;
    this._last = performance.now();
    this._acc = 0;
    this.fps = 60; this._fpsAcc = 0; this._fpsN = 0; this._fpsT = 0;
    this.pointerOver = null;
    this.pauseBtnRect = null;

    this.canvas.addEventListener('pointermove', (e) => {
      const r = this.canvas.getBoundingClientRect();
      this.pointerOver = {
        x: (e.clientX - r.left) / r.width * CONFIG.W,
        y: (e.clientY - r.top) / r.height * CONFIG.H,
      };
    });

    // Auto-pause when tab hidden
    document.addEventListener('visibilitychange', () => {
      if (document.hidden && (this.state === State.PLAY || this.state === State.STICKY_AIM)) this.pause();
    });
    window.addEventListener('blur', () => {
      if (this.state === State.PLAY || this.state === State.STICKY_AIM) this.pause();
    });

    requestAnimationFrame(this._frame.bind(this));
  }

  _resize() {
    const dpr = Math.min(window.devicePixelRatio || 1, 3);
    const availW = window.innerWidth;
    const availH = window.innerHeight;
    this.scale = Math.min(availW / CONFIG.W, availH / CONFIG.H);
    const cw = Math.round(CONFIG.W * this.scale);
    const ch = Math.round(CONFIG.H * this.scale);
    this.canvas.width = Math.round(cw * dpr);
    this.canvas.height = Math.round(ch * dpr);
    this.canvas.style.width = cw + 'px';
    this.canvas.style.height = ch + 'px';
    this.dpr = dpr;
    this.offX = (availW - cw) / 2;
    this.offY = (availH - ch) / 2;
  }

  // ---- run / level lifecycle ---------------------------------------------

  resetRun() {
    this.score = 0;
    this.lives = CONFIG.LIVES;
    this.level = this.startLevel;
    this.combo = 0;
    this.feverBank = 0;         // attic-seconds banked → multiplier ramp
    this.multBoost = 0;         // ×2 boost timer
    this.balls = [];
    this.capsules.length = 0;
    this.stats = { bricks: 0, maxFever: 0, maxMult: 1, smashes: 0, maxMultCombo: 0 };
    this.shieldActive = false;
    this._feverCd = 0;
    this.levelDef = LEVELS[this.level];
    this.field.build(this.levelDef);
  }

  startGame(level) {
    if (typeof level === 'number') {
      this.startLevel = clamp(level - 1, 0, CONFIG.LEVELS - 1);
    }
    this.resetRun();
    this._startLevel(true);
    this.audio.startHum();
  }

  _startLevel(first) {
    this.balls = [];
    this.capsules.length = 0;
    this.pool.clear();
    this.popups.clear();
    this.combo = 0;
    this.shieldActive = false;
    this.stickyHeld = null;
    this.state = State.INTRO;
    this.stateT = 0;
    this.introDur = 2.2;
    this.levelTime = 0; // elapsed play time, feeds the level-clear time bonus
    this._pendingServe = first ? 0.6 : 0.4;
  }

  _serveBall() {
    const p = this.paddle;
    const speed = CONFIG.BALL_SPEED_BASE + this.level * 20;
    const b = new Ball(p.x, p.y - p.h / 2 - CONFIG.BALL_R - 2, 0, 0);
    b.stuck = true; b.launched = false;
    b._launchSpeed = speed;
    this.balls = [b];
  }

  pause() {
    if (this.state !== State.PLAY && this.state !== State.STICKY_AIM) return;
    this._prevState = this.state;
    this.state = State.PAUSE;
    this.audio.setFeverTier(0);
    this._feverLatched = false;
  }
  resume() {
    if (this.state !== State.PAUSE) return;
    this.state = this._prevState || State.PLAY;
    this._last = performance.now(); // avoid a huge dt spike after resume
  }
  quitToTitle() {
    this.state = State.TITLE;
    this.audio.hushMusic();
    this._saveBest();
  }

  // ---- input callbacks (wired from input.js) -------------------------------

  onTap(p) {
    if (this.state === State.TITLE) {
      // Buttons handled by hit test in _pointerDown routing; tap elsewhere = start
      const act = this.input.hitButton(p);
      if (act === 'launch' || act === 'pause') return;
      this._buttonAction('start');
      return;
    }
    if (this.state === State.PAUSE || this.state === State.GAME_OVER || this.state === State.WIN) {
      const act = this.input.hitButton(p);
      if (act) this._buttonAction(act);
      return;
    }
    if (this.state === State.PLAY || this.state === State.STICKY_AIM) {
      const act = this.input.hitButton(p);
      if (act === 'pause') { this._buttonAction(act); return; }
      if (act === 'launch') { this._launchBalls(); return; }
      this._launchBalls();
      return;
    }
    if (this.state === State.INTRO) {
      // Tap during level intro: skip it, serve, and launch straight away.
      if (this.input.hitButton(p) === 'pause') { this._buttonAction('pause'); return; }
      this.state = State.PLAY;
      this.stateT = 0;
      if (this.balls.length === 0) this._serveBall();
      this._launchBalls();
    }
  }

  onLaunchKey() {
    if (this.state === State.TITLE) this._buttonAction('start');
    else if (this.state === State.GAME_OVER || this.state === State.WIN) this._buttonAction('start');
    else if (this.state === State.PLAY || this.state === State.STICKY_AIM) this._launchBalls();
    else if (this.state === State.PAUSE) this.resume();
  }
  onPauseKey() {
    if (this.state === State.PLAY || this.state === State.STICKY_AIM) this.pause();
    else if (this.state === State.PAUSE) this.resume();
  }
  onMuteKey() {
    this.audio.unlock();
    this.audio.toggleMute();
    this.popups.add(CONFIG.W / 2, 120, this.audio.muted ? 'MUTED' : 'SOUND ON', { color: '#8fb4d9' });
  }

  _buttonAction(action) {
    switch (action) {
      case 'start': this.audio.unlock(); this.startGame(); break;
      case 'settings': this.state = State.SETTINGS; break;
      case 'back': this.state = State.TITLE; break;
      case 'resume': this.resume(); break;
      case 'restart': this._startLevel(false); break;
      case 'quit': this.quitToTitle(); break;
      case 'toggleMute': this.audio.toggleMute(); break;
      case 'toggleFollow': this.input.setFollowFinger(!this.input.followFinger); break;
      case 'cycleScheme': {
        const order = ['auto', 'mouse', 'keyboard', 'touch'];
        const i = order.indexOf(this.input.scheme);
        this.input.setScheme(order[(i + 1) % order.length]);
        break;
      }
    }
  }

  /** Click/tap routing for menu buttons (called from pointerdown). */
  _pointerDown(p) {
    // Buttons first (menus)
    if (this.state !== State.PLAY && this.state !== State.STICKY_AIM) {
      for (const b of UI.buttons) {
        if (p.x > b.x && p.x < b.x + b.w && p.y > b.y && p.y < b.y + b.h) {
          this._buttonAction(b.action);
          return;
        }
      }
    }
    this.onTap(p);
  }

  _launchBalls() {
    let did = false;
    for (const b of this.balls) {
      if (b.stuck) {
        const ang = rand(-0.35, 0.35) - Math.PI / 2;
        const sp = b._launchSpeed || CONFIG.BALL_SPEED_BASE;
        b.vx = Math.cos(ang) * sp; b.vy = Math.sin(ang) * sp;
        b.stuck = false; b.launched = true;
        did = true;
      }
    }
    if (this.stickyHeld) {
      const b = this.stickyHeld;
      const sp = clamp(b.speed || CONFIG.BALL_SPEED_BASE, CONFIG.BALL_SPEED_MIN, CONFIG.BALL_SPEED_MAX);
      b.vx = Math.cos(-Math.PI / 2 + rand(-0.3, 0.3)) * sp;
      b.vy = Math.sin(-Math.PI / 2) * sp;
      b.stuck = false; b.launched = true;
      this.stickyHeld = null;
      did = true;
    }
    if (did) this.audio.launch();
  }

  // ---- scoring / fever ------------------------------------------------------

  get multiplier() {
    const fever = 1 + this.feverBank;
    const boost = this.multBoost > 0 ? 2 : 1;
    return Math.round(fever * boost * 10) / 10;
  }
  multiplierTier() {
    const m = this.multiplier;
    return m >= 6 ? 3 : m >= 4 ? 2 : m >= 2.5 ? 1 : 0;
  }
  anyBallAbove() {
    return this.balls.some((b) => b.launched && b.isAboveBricks);
  }

  /** Track peak multiplier + fever tier. Call whenever either can rise. */
  _trackPeaks() {
    if (this.multiplier > this.stats.maxMult) this.stats.maxMult = this.multiplier;
    const tier = this.fx.feverTier;
    if (tier > this.stats.maxFever) this.stats.maxFever = tier;
  }

  addCombo() {
    this.combo++;
    if (this.combo > this.stats.maxMultCombo) this.stats.maxMultCombo = this.combo;
  }

  awardBrick(brick) {
    this.addCombo();
    const base = brick.pts;
    const gained = Math.round(base * this.multiplier);
    this.score += gained;
    this.popups.add(brick.cx, brick.cy, `+${gained}`, {
      color: this.anyBallAbove() ? '#ffe14f' : '#fff',
      size: 16 + Math.min(14, gained / 40),
    });
    this.stats.maxMult = Math.max(this.stats.maxMult, this.multiplier);
    this._trackPeaks();
  }

  statsLine() {
    return `${this.stats.bricks} bricks · max FEVER ${this.stats.maxFever} · max ×${this.stats.maxMult.toFixed(1)} · ${this.stats.smashes} smashes`;
  }

  /** HUD list of active timed effects: {label, color, frac 0..1 remaining}. */
  activePowerLabels() {
    const out = [];
    const p = this.paddle;
    const push = (label, color, t, dur) => {
      if (t > 0) out.push({ label: `${label} ${t.toFixed(0)}s`, color, frac: t / dur });
    };
    for (const b of this.balls) {
      if (b.fire > 0) { push('FIREBALL', 'rgb(255,110,50)', b.fire, 8); break; }
    }
    for (const b of this.balls) {
      if (b.heavy > 0) { push('HEAVY', 'rgb(200,205,225)', b.heavy, 10); break; }
    }
    for (const b of this.balls) {
      if (b.ghost > 0) { push('GHOST', 'rgb(170,230,255)', b.ghost, 6); break; }
    }
    push('SLOW-MO', 'rgb(150,160,255)', this.slowmoTimer || 0, 4);
    push('×2 SCORE', 'rgb(255,235,130)', this.multBoost, 10);
    if (p) {
      push('WIDE', 'rgb(110,235,150)', p.wide, 12);
      push('SHRINK', 'rgb(200,110,200)', p.shrink, 10);
      push('STICKY', 'rgb(255,180,220)', p.sticky, 10);
    }
    if (this.shieldActive) out.push({ label: 'SHIELD READY', color: 'rgb(90,200,255)', frac: 1 });
    return out;
  }

  _saveBest() {
    if (this.score > this.best) {
      this.best = Math.round(this.score);
      storageSet('ab.best', this.best);
    }
  }

  // ---- main loop -------------------------------------------------------------

  _frame(now) {
    requestAnimationFrame(this._frame.bind(this));
    let dt = (now - this._last) / 1000;
    this._last = now;
    if (dt > 0.25) dt = 0.25; // tab-return guard

    // FPS counter
    this._fpsAcc += dt; this._fpsN++;
    if (this._fpsAcc > 0.5) {
      this.fps = Math.round(this._fpsN / this._fpsAcc);
      this._fpsAcc = 0; this._fpsN = 0;
    }

    this.time += dt;
    const sdt = this.fx.update(dt, this); // scaled dt (0 during hit-stop)
    this.stateT += dt;

    // Fixed-timestep physics substeps
    this._acc += sdt;
    let steps = 0;
    while (this._acc >= CONFIG.PHYS_STEP && steps < 8) {
      this.step(CONFIG.PHYS_STEP);
      this._acc -= CONFIG.PHYS_STEP;
      steps++;
    }
    if (steps === 8) this._acc = 0; // spiral of death guard

    this.render();
  }

  step(dt) {
    switch (this.state) {
      case State.TITLE: this._stepTitle(dt); break;
      case State.INTRO: this._stepIntro(dt); break;
      case State.PLAY: this._stepPlay(dt); break;
      case State.STICKY_AIM: this._stepPlay(dt, true); break;
      case State.LEVEL_CLEAR: this._stepClear(dt); break;
      case State.DYING: this._stepDying(dt); break;
      default: break; // pause/menus: frozen world
    }
    this.popups.update(dt);
    this.pool.update(dt);
  }

  _stepTitle(dt) {
    // Demo balls bounce behind the logo (simple bounds, no bricks).
    for (const b of this.demoBalls) {
      b.x += b.vx * dt; b.y += b.vy * dt;
      if (b.x < b.r || b.x > CONFIG.W - b.r) { b.vx *= -1; b.x = clamp(b.x, b.r, CONFIG.W - b.r); }
      if (b.y < CONFIG.CEIL_Y + b.r || b.y > CONFIG.H - b.r) { b.vy *= -1; b.y = clamp(b.y, CONFIG.CEIL_Y + b.r, CONFIG.H - b.r); }
      b.pushTrail();
    }
    this.paddle = this.paddle || new Paddle();
  }

  _stepIntro(dt) {
    this.paddle = this.paddle || new Paddle();
    this._movePaddle(dt);
    this._pendingServe -= dt;
    if (this._pendingServe <= 0 && this.balls.length === 0) this._serveBall();
    if (this.stateT >= this.introDur) {
      this.state = State.PLAY;
      this.stateT = 0;
    }
  }

  _stepClear(dt) {
    if (this.stateT >= this.introDur) {
      if (this.level + 1 >= CONFIG.LEVELS) {
        this.state = State.WIN;
        this._saveBest();
      } else {
        this.level++;
        this.levelDef = LEVELS[this.level];
        this.field.build(this.levelDef);
        this._startLevel(false);
      }
    }
  }

  _stepDying(dt) {
    if (this.stateT >= this.introDur) {
      this.lives--;
      if (this.lives <= 0) {
        this.state = State.GAME_OVER;
        this._saveBest();
      } else {
        // Reset ball/combo but keep score, bricks and fever progress.
        this.balls = [];
        this.combo = 0;
        this.stickyHeld = null;
        const p = this.paddle;
        p.wide = 0; p.shrink = 0; p.sticky = 0; p.heldBall = null;
        this.state = State.INTRO;
        this.stateT = 0;
        this.introDur = 1.2;
        this._pendingServe = 0.5;
      }
    }
  }

  // ---- play step --------------------------------------------------------------

  _movePaddle(dt) {
    const target = this.input.getPaddleTarget();
    const p = this.paddle;
    if (!target) { p._trackVel(dt); return; }
    if (target.stick) {
      // Thumbstick: velocity-driven movement.
      p.x = clamp(p.x + target.vx * dt, p.halfW, CONFIG.W - p.halfW);
      p.y = clamp(p.y + target.vy * dt, CONFIG.PADDLE_ZONE_TOP, CONFIG.PADDLE_ZONE_BOTTOM);
      p._trackVel(dt);
    } else if (target.vel) {
      p.x = clamp(p.x + target.vel.x * dt, p.halfW, CONFIG.W - p.halfW);
      p.y = clamp(p.y + target.vel.y * dt, CONFIG.PADDLE_ZONE_TOP, CONFIG.PADDLE_ZONE_BOTTOM);
      p._trackVel(dt);
    } else {
      p.seekTo(target.x, target.y, dt);
    }
    p.applyTimers(dt);
  }

  _stepPlay(dt, stickyAim = false) {
    this.paddle = this.paddle || new Paddle();
    // Serve guard: never run play with no ball. Covers intro-skip and any
    // path that reached PLAY before _stepIntro could serve.
    if (this.balls.length === 0 && !this.stickyHeld
        && this.field.breakable > 0 && this._pendingServe > 0) {
      this._pendingServe -= dt;
      if (this._pendingServe <= 0) { this._pendingServe = 0; this._serveBall(); }
    }
    this._movePaddle(dt);
    const healed = this.field.update(dt);
    if (healed) {
      this.emitter.sparks(healed.cx, healed.cy, 8, 140, 255, 140);
      this.audio.shimmer();
      this.popups.add(healed.cx, healed.cy - 18, 'HEALED', { color: '#a0ff8c', size: 14 });
    }

    // Fever bank management
    const above = this.anyBallAbove();
    if (this._feverCd > 0) this._feverCd = Math.max(0, this._feverCd - dt);
    if (above) {
      this.feverBank += dt; // ramps while any ball is up top
      this.emitter.rain(dt, 1 + this.fx.feverTier);
    } else {
      if (this.feverBank > 0) {
        this.feverBank = Math.max(0, this.feverBank - dt * CONFIG.FEVER_DECAY * 0.4);
      }
    }
    this._trackPeaks(); // peak multiplier / fever tier (win + gameover stats)

    // Balls
    const heldBySticky = this.stickyHeld;
    for (let i = this.balls.length - 1; i >= 0; i--) {
      const b = this.balls[i];
      b.update(dt);
      if (b === heldBySticky) {
        b.x = this.paddle.x + (b._holdOff || 0);
        b.y = this.paddle.top - b.r - 2;
        b.pushTrail();
        continue;
      }
      if (b.stuck) { // on-paddle, pre-launch
        b.x = this.paddle.x;
        b.y = this.paddle.top - b.r - 2;
        b.pushTrail();
        continue;
      }
      this._moveBall(b, dt);
      if (b.dead) {
        this.balls.splice(i, 1);
        if (this.balls.length === 0) { this._loseBall(); return; }
      }
    }

    // Capsules
    for (let i = this.capsules.length - 1; i >= 0; i--) {
      const c = this.capsules[i];
      c.update(dt);
      const p = this.paddle;
      if (!c.dead && circleRect(c.x, c.y, c.r, p.x - p.halfW, p.top, p.w, p.h)) {
        this._applyPowerup(c.type);
        c.dead = true;
      }
      if (c.dead) this.capsules.splice(i, 1);
    }

    // Timers
    this.levelTime = (this.levelTime || 0) + dt;
    if (this.multBoost > 0) this.multBoost = Math.max(0, this.multBoost - dt);
    if (this.slowmoTimer > 0) {
      this.slowmoTimer = Math.max(0, this.slowmoTimer - dt);
      if (this.slowmoTimer === 0) this.audio.slowmo(false);
    }
  }

  // ---- ball physics (substepped, robust at any framerate) ---------------------

  _moveBall(b, dt) {
    const wasAbove = b.isAboveBricks;
    b.x += b.vx * dt;
    b.y += b.vy * dt;

    // Walls: left/right
    if (b.x - b.r < 0) { b.x = b.r; b.vx = Math.abs(b.vx); this._wallHit(b, 0, b.y); }
    else if (b.x + b.r > CONFIG.W) { b.x = CONFIG.W - b.r; b.vx = -Math.abs(b.vx); this._wallHit(b, CONFIG.W, b.y); }
    // Ceiling
    if (b.y - b.r < CONFIG.CEIL_Y) {
      b.y = CONFIG.CEIL_Y + b.r;
      b.vy = Math.abs(b.vy);
      this._wallHit(b, b.x, CONFIG.CEIL_Y);
      this._checkFeverEntry(b, wasAbove);
    }
    // Floor (kill or shield bounce)
    if (b.y + b.r >= CONFIG.H) {
      if (this.shieldActive) {
        this.shieldActive = false;
        b.y = CONFIG.H - b.r - 1;
        b.vy = -Math.abs(b.vy);
        this.audio.shieldBounce();
        this.emitter.ring(b.x, CONFIG.H - 6, 60, 90, 200, 255, 0.5);
        this.emitter.sparks(b.x, CONFIG.H - 8, 18, 90, 200, 255);
        this.popups.add(b.x, CONFIG.H - 60, 'SHIELD!', { color: '#5ac8ff', size: 22 });
        this.fx.addShake(5);
      } else {
        b.dead = true;
        this._ballShatter(b);
        return;
      }
    }

    // Paddle
    const p = this.paddle;
    const hit = circleRect(b.x, b.y, b.r, p.x - p.halfW, p.top, p.w, p.h);
    if (hit && b.vy > 0) {
      const speed = clamp(Math.hypot(b.vx, b.vy), CONFIG.BALL_SPEED_MIN, CONFIG.BALL_SPEED_MAX);
      b.y = p.top - b.r - Math.max(0, hit.pen);
      const { smash } = p.bounce(b, speed);
      this._onPaddleHit(b, smash);
    }

    // Bricks
    const near = this.field.near(b.x, b.y, b.r + 4);
    for (const brick of near) {
      const cr = circleRect(b.x, b.y, b.r, brick.x, brick.y, brick.w, brick.h);
      if (!cr) continue;
      const speed = b.speed;
      const preVx = b.vx, preVy = b.vy; // velocity before any reflection below
      const fire = b.fire > 0, heavy = b.heavy > 0, ghost = b.ghost > 0;

      // Fireball burns through standard bricks; Ghost phases through anything
      // destructible (metal still blocks — it's level geometry).
      const phaseThrough = (fire && brick.type === 'S') || (ghost && brick.type !== 'M');

      if (!phaseThrough) {
        // Resolve position + reflect along the collision normal.
        b.x += cr.nx * cr.pen;
        b.y += cr.ny * cr.pen;
        const dot = b.vx * cr.nx + b.vy * cr.ny;
        if (dot < 0) {
          b.vx -= 2 * dot * cr.nx;
          b.vy -= 2 * dot * cr.ny;
        }
      }

      // Impact direction is always the pre-collision velocity — brick.hit()
      // must see the direction the ball was travelling *into* the brick, even
      // if the position/velocity were just resolved above.
      const res = brick.hit(speed, preVx / (speed || 1), preVy / (speed || 1), { fireball: fire, heavy, ghost });
      this._onBrickHit(brick, res, b, phaseThrough);
      if (phaseThrough) continue; // no bounce, keep burning through
      break; // one brick contact per substep is enough
    }

    b.pushTrail();
    this._checkFeverEntry(b, wasAbove);
  }

  _wallHit(b, wx, wy) {
    this.audio.wallBoop();
    this.emitter.sparks(wx, wy, 4, 180, 220, 255, 200);
    if (b.heavy > 0) this.fx.addShake(2);
  }

  /** Detect the moment a ball enters the attic → jackpot. */
  _checkFeverEntry(b, wasAbove) {
    if (wasAbove || !b.isAboveBricks) return;
    if (this._feverCd > 0) return;
    this._feverCd = 2; // re-boost cooldown so repeat ceiling bounces stay special
    this.fx.doFlash('180,230,255', 0.85);
    this.fx.doChroma(1);
    this.fx.doSlowmo(0.15, 0.25); // ~200ms dramatic slow-mo
    this.fx.doHitStop(0.05);
    this.fx.showBanner('FEVER!', 1.6);
    this.fx.addShake(14);
    this.audio.feverEntry();
    this.emitter.ring(b.x, b.y, 120, 200, 230, 255, 0.6);
    this.emitter.sparks(b.x, b.y, 30, 200, 230, 255, 600);
    this.popups.add(CONFIG.W / 2, CONFIG.FIELD_TOP - 40, 'OVERTIME!', { color: '#7fd4ff', size: 30, life: 1.4 });
    this.feverBank = Math.max(this.feverBank, 0.4); // small head start
  }

  _onPaddleHit(b, smash) {
    const p = this.paddle;
    this.audio.paddleBoop(this.combo, smash);
    this.emitter.sparks(b.x, p.top, smash ? 20 : 8, smash ? 255 : 160, 220, 255, smash ? 500 : 260);
    if (smash) {
      this.stats.smashes++;
      this.fx.addShake(9);
      this.fx.doHitStop(0.045);
      this.fx.doFlash('255,255,255', 0.28);
      this.fx.doChroma(0.8);
      this.emitter.ring(b.x, p.top, 80, 255, 255, 255, 0.4);
      this.popups.add(b.x, p.top - 30, 'SMASH!', { color: '#ffe14f', size: 24, life: 0.7 });
    } else {
      this.fx.addShake(2);
    }
    // Combo resets when the ball touches the paddle.
    this.combo = 0;
    // Fever ramp partially resets on paddle touch.
    if (this.feverBank > 0) {
      this.feverBank *= 0.35;
      if (this.fx.feverTier > 0) {
        this.fx.feverTier = 0;
        this.audio.setFeverTier(0);
        this.audio.feverEnd();
      }
    }
    // Sticky catch
    if (p.sticky > 0 && !smash) {
      b.stuck = true;
      b.launched = false;
      b._holdOff = clamp(b.x - p.x, -p.halfW + 10, p.halfW - 10);
      this.stickyHeld = b;
      this.audio.powerup(true);
      this.popups.add(b.x, p.top - 24, 'CAUGHT — TAP TO LAUNCH', { color: '#ffb4dc', size: 14 });
    }
  }

  _onBrickHit(brick, res, b, phased) {
    switch (res) {
      case 'break': {
        brick.alive = false;
        this.field.breakable--;
        this.stats.bricks++;
        const [r, g, bl] = brick.baseColor();
        this.emitter.brickShards(brick.cx, brick.cy, brick.w, brick.h, r, g, bl, phased ? 18 : 14);
        if (phased) {
          // Fire line of destruction
          for (let i = 0; i < 5; i++) this.emitter.flame(brick.cx + rand(-20, 20), brick.cy, b.vx * 0.2, b.vy * 0.2);
          this.audio.brickPop(brick.row, true);
        } else {
          this.audio.brickPop(brick.row);
        }
        this.fx.addShake(b.heavy > 0 ? 5 : 2.5);
        if (b.heavy > 0) this.fx.doHitStop(0.03);
        this.awardBrick(brick);
        // Drops
        if (brick.type === 'P' || chance(CONFIG.DROP_CHANCE)) {
          const type = this._rollPowerup();
          this.capsules.push(new Capsule(type, brick.cx, brick.cy));
        }
        // Level clear?
        if (this.field.breakable <= 0) this._levelCleared();
        break;
      }
      case 'clang':
        brick.flash = 0.6;
        brick.wobble = 1;
        this.audio.clang();
        this.emitter.sparks(b.x, b.y, 10, 255, 240, 180, 420);
        this.fx.addShake(2);
        this.popups.add(brick.cx, brick.cy - 16, 'TOO SLOW!', { color: '#c8d2e0', size: 14, life: 0.7 });
        break;
      case 'shimmer':
        brick.flash = 0.4;
        this.audio.shimmer();
        this.emitter.sparks(b.x, b.y, 6, 96, 210, 255, 240);
        this.popups.add(brick.cx, brick.cy - 16, 'STRAIGHT ON!', { color: '#60d2ff', size: 13, life: 0.7 });
        break;
      case 'crack':
        brick.flash = 0.7;
        brick.wobble = 0.6;
        this.audio.crack();
        this.emitter.sparks(b.x, b.y, 6, 160, 255, 140, 260);
        this.popups.add(brick.cx, brick.cy - 16, 'CRACKED — HIT AGAIN!', { color: '#a0ff8c', size: 13, life: 0.8 });
        this.fx.addShake(1.5);
        break;
      case 'phase':
        // Ghost passes through; tiny shimmer only.
        this.emitter.sparks(b.x, b.y, 3, 170, 230, 255, 160);
        break;
      default: break;
    }
  }

  _rollPowerup() {
    const weights = [
      [PU.MULTIBALL, 12], [PU.FIRE, 10], [PU.HEAVY, 9], [PU.GHOST, 9],
      [PU.WIDE, 10], [PU.STICKY, 8], [PU.SHIELD, 9], [PU.SLOWMO, 7], [PU.BOOST, 10],
      [PU.SHRINK, 5], [PU.FAST, 5],
    ];
    let total = 0;
    for (const w of weights) total += w[1];
    let roll = Math.random() * total;
    for (const [type, w] of weights) {
      roll -= w;
      if (roll <= 0) return type;
    }
    return PU.MULTIBALL;
  }

  _applyPowerup(type) {
    const def = PU_DEFS[type];
    const p = this.paddle;
    this.audio.powerup(def.good);
    this.fx.doFlash(def.good ? '180,255,200' : '255,120,140', 0.3);
    this.popups.add(p.x, p.top - 40, def.label, {
      color: def.good ? `rgb(${def.color.join(',')})` : '#ff8a8a',
      size: 24, life: 1.2,
    });
    this.emitter.ring(p.x, p.y, 60, def.color[0], def.color[1], def.color[2], 0.4);

    switch (type) {
      case PU.MULTIBALL: this._splitBalls(); break;
      case PU.FIRE: for (const b of this.balls) b.fire = PU_DURATION[PU.FIRE]; break;
      case PU.HEAVY: for (const b of this.balls) b.heavy = PU_DURATION[PU.HEAVY]; break;
      case PU.GHOST: for (const b of this.balls) b.ghost = PU_DURATION[PU.GHOST]; break;
      case PU.WIDE: p.wide = PU_DURATION[PU.WIDE]; p.shrink = 0; break;
      case PU.SHRINK: p.shrink = PU_DURATION[PU.SHRINK]; p.wide = 0; break;
      case PU.STICKY: p.sticky = PU_DURATION[PU.STICKY]; break;
      case PU.SHIELD: this.shieldActive = true; break;
      case PU.SLOWMO: this.slowmoTimer = PU_DURATION[PU.SLOWMO]; this.audio.slowmo(true); break;
      case PU.BOOST: this.multBoost = PU_DURATION[PU.BOOST]; break;
      case PU.FAST: for (const b of this.balls) {
        if (!b.stuck) b.setSpeed(clamp(b.speed * 1.35, 0, CONFIG.BALL_SPEED_MAX));
      } break;
    }
  }

  _splitBalls() {
    const src = this.balls.filter((b) => !b.stuck);
    const base = src.length ? src : this.balls;
    for (const b of base) {
      if (this.balls.length >= CONFIG.MAX_BALLS) break;
      const sp = clamp(b.speed || CONFIG.BALL_SPEED_BASE, CONFIG.BALL_SPEED_MIN, CONFIG.BALL_SPEED_MAX);
      for (let k = 0; k < 2 && this.balls.length < CONFIG.MAX_BALLS; k++) {
        const ang = Math.atan2(b.vy, b.vx) + (k === 0 ? 0.5 : -0.5);
        const nb = new Ball(b.x, b.y, Math.cos(ang) * sp, Math.sin(ang) * sp);
        nb.launched = b.launched;
        nb.fire = b.fire; nb.heavy = b.heavy; nb.ghost = b.ghost;
        this.balls.push(nb);
      }
      if (b.stuck) { b.stuck = false; b.launched = true; }
    }
    this.audio.split();
    this.fx.doFlash('255,255,220', 0.25);
  }

  _ballShatter(b) {
    this.audio.ballLost();
    this.fx.doSlowmo(0.25, 0.6);
    this.fx.addShake(10);
    this.emitter.sparks(b.x, Math.min(b.y, CONFIG.H - 20), 30, 255, 120, 120, 600);
    this.emitter.ring(b.x, Math.min(b.y, CONFIG.H - 20), 70, 255, 120, 120, 0.5);
  }

  _loseBall() {
    this.combo = 0;
    this.state = State.DYING;
    this.stateT = 0;
    this.introDur = 1.4;
  }

  _levelCleared() {
    this.fx.showBanner('LEVEL CLEAR!', 1.4);
    this.fx.doFlash('255,240,180', 0.5);
    this.audio.powerup(true);
    this.clearBonus = 500;
    this.timeBonus = Math.max(0, Math.round(300 - this.levelTime * 2));
    this.score += this.clearBonus + this.timeBonus;
    this.state = State.LEVEL_CLEAR;
    this.stateT = 0;
    this.introDur = 2.6;
  }

  // ---- rendering ---------------------------------------------------------------

  render() {
    const ctx = this.ctx;
    // The canvas element is sized/centered by CSS; we apply DPR + uniform
    // stage scale here (no translate — letterboxing is done by CSS centering).
    ctx.setTransform(this.dpr * this.scale, 0, 0, this.dpr * this.scale, 0, 0);
    ctx.clearRect(0, 0, CONFIG.W, CONFIG.H);

    // Background
    const bg = ctx.createLinearGradient(0, 0, 0, CONFIG.H);
    bg.addColorStop(0, '#0a0c18');
    bg.addColorStop(0.5, '#07070f');
    bg.addColorStop(1, '#04040a');
    ctx.fillStyle = bg;
    ctx.fillRect(0, 0, CONFIG.W, CONFIG.H);

    // Subtle animated background grid
    ctx.save();
    ctx.globalAlpha = 0.05;
    ctx.strokeStyle = '#4478aa';
    ctx.lineWidth = 1;
    for (let x = 0; x <= CONFIG.W; x += 60) { ctx.beginPath(); ctx.moveTo(x, 0); ctx.lineTo(x, CONFIG.H); ctx.stroke(); }
    for (let y = 0; y <= CONFIG.H; y += 60) { ctx.beginPath(); ctx.moveTo(0, y); ctx.lineTo(CONFIG.W, y); ctx.stroke(); }
    ctx.restore();

    // Screen shake (game world only)
    ctx.save();
    ctx.translate(this.fx.shakeX, this.fx.shakeY);

    if (this.state === State.TITLE) {
      // Demo balls behind the logo
      for (const b of this.demoBalls) b.draw(ctx, this.time);
      ctx.restore(); // shake
      this.ui.drawTitle();
      this._drawDebug();
      return;
    }
    if (this.state === State.SETTINGS) {
      ctx.restore(); // shake
      this.ui.drawSettings();
      return;
    }

    // ---- playfield ----
    this._drawArena(ctx);

    this.field.draw(ctx, this.time);
    for (const c of this.capsules) c.draw(ctx);
    for (const b of this.balls) b.draw(ctx, this.time);
    this.pool.draw(ctx);
    this.paddle.draw(ctx, this.time, this.multiplier);
    this.popups.draw(ctx);

    ctx.restore(); // shake

    // HUD & overlays in stable screen space
    if (this.state !== State.GAME_OVER && this.state !== State.WIN) this.ui.drawHUD();
    this.ui.reset();
    if (this.state === State.INTRO) this.ui.drawLevelIntro();
    else if (this.state === State.PAUSE) this.ui.drawPause();
    else if (this.state === State.LEVEL_CLEAR) this.ui.drawLevelClear();
    else if (this.state === State.GAME_OVER) this.ui.drawGameOver();
    else if (this.state === State.WIN) this.ui.drawWin();

    // Touch UI on top
    if (this.state === State.PLAY || this.state === State.STICKY_AIM || this.state === State.INTRO) {
      this.input.drawTouchUI(ctx, this);
    }

    this.fx.draw(ctx, this); // post fx

    // Shield indicator
    if (this.shieldActive) {
      ctx.save();
      ctx.shadowColor = '#5ac8ff'; ctx.shadowBlur = 18;
      ctx.strokeStyle = `rgba(90,200,255,${0.6 + Math.sin(this.time * 6) * 0.25})`;
      ctx.lineWidth = 5;
      ctx.beginPath(); ctx.moveTo(0, CONFIG.H - 4); ctx.lineTo(CONFIG.W, CONFIG.H - 4); ctx.stroke();
      ctx.restore();
    }


    this._drawDebug();
  }

  _drawArena(ctx) {
    // Ceiling
    const tier = this.fx.feverTier;
    ctx.save();
    ctx.shadowColor = tier >= 1 ? 'rgba(160,220,255,0.9)' : 'rgba(120,160,255,0.4)';
    ctx.shadowBlur = 8 + tier * 8;
    ctx.strokeStyle = tier >= 3 ? '#ffb0e8' : tier >= 2 ? '#b090ff' : tier >= 1 ? '#7fd4ff' : '#3a4a6a';
    ctx.lineWidth = 4;
    ctx.beginPath(); ctx.moveTo(0, CONFIG.CEIL_Y); ctx.lineTo(CONFIG.W, CONFIG.CEIL_Y); ctx.stroke();
    ctx.restore();
    // Ceiling hatch
    ctx.save();
    ctx.globalAlpha = 0.25;
    ctx.strokeStyle = '#3a4a6a';
    ctx.lineWidth = 1;
    for (let x = 0; x < CONFIG.W; x += 24) {
      ctx.beginPath(); ctx.moveTo(x, CONFIG.CEIL_Y); ctx.lineTo(x - 10, CONFIG.CEIL_Y - 12); ctx.stroke();
    }
    ctx.restore();
    // Paddle zone guides
    ctx.save();
    ctx.globalAlpha = 0.12;
    ctx.strokeStyle = '#6c8cff';
    ctx.setLineDash([6, 10]);
    ctx.beginPath(); ctx.moveTo(0, CONFIG.PADDLE_ZONE_TOP); ctx.lineTo(CONFIG.W, CONFIG.PADDLE_ZONE_TOP); ctx.stroke();
    ctx.beginPath(); ctx.moveTo(0, CONFIG.PADDLE_ZONE_BOTTOM); ctx.lineTo(CONFIG.W, CONFIG.PADDLE_ZONE_BOTTOM); ctx.stroke();
    ctx.restore();
    // Attic zone label (subtle)
    ctx.save();
    ctx.globalAlpha = 0.1 + this.fx.fever * 0.25;
    ctx.font = `900 30px ${UI.FONT}`;
    ctx.textAlign = 'center';
    ctx.fillStyle = '#7fd4ff';
    ctx.fillText('T H E   A T T I C', CONFIG.W / 2, (CONFIG.CEIL_Y + CONFIG.FIELD_TOP) / 2);
    ctx.restore();
  }

  _drawDebug() {
    if (!this.debug) return;
    const ctx = this.ctx;
    ctx.save();
    ctx.font = `bold 14px monospace`;
    ctx.textAlign = 'left';
    ctx.fillStyle = '#0f0';
    ctx.fillText(`FPS ${this.fps}`, 16, CONFIG.H - 60);
    ctx.fillText(`balls ${this.balls.length} particles ${this.pool.alive}`, 16, CONFIG.H - 42);
    ctx.fillText(`state ${this.state} fever ${this.fx.fever.toFixed(2)} tier ${this.fx.feverTier}`, 16, CONFIG.H - 24);
    // Hitboxes
    ctx.strokeStyle = 'rgba(0,255,0,0.6)';
    for (const b of this.balls) {
      ctx.beginPath(); ctx.arc(b.x, b.y, b.r, 0, TAU); ctx.stroke();
    }
    if (this.paddle) {
      const p = this.paddle;
      ctx.strokeStyle = 'rgba(255,0,0,0.6)';
      ctx.strokeRect(p.x - p.halfW, p.top, p.w, p.h);
    }
    ctx.restore();
  }
}

window.State = State;
window.Game = Game;

/* ============================================================
   Bootstrap + debug hook (window.__GAME__)
   ============================================================ */

window.addEventListener('load', () => {
  const game = new Game();

  // Pointer routing: menu buttons take priority over tap actions.
  game.canvas.addEventListener('pointerdown', (e) => {
    const r = game.canvas.getBoundingClientRect();
    const p = {
      x: (e.clientX - r.left) / r.width * CONFIG.W,
      y: (e.clientY - r.top) / r.height * CONFIG.H,
    };
    game._pointerDown(p);
  });

  // Expose test hooks.
  Object.defineProperty(window, '__GAME__', {
    value: {
      version: game.version,
      get state() {
        return {
          mode: game.state,
          score: Math.round(game.score),
          lives: game.lives,
          level: game.level + 1,
          multiplier: game.multiplier,
          balls: game.balls.length,
          bricksRemaining: game.field.remaining,
          ballAboveBricks: game.anyBallAbove(),
          feverTier: game.fx.feverTier,
          combo: game.combo,
          paused: game.state === State.PAUSE,
        };
      },
      startGame(level) {
        game.startGame(level); // optional level number (1-based)
      },
      pause() { game.pause(); },
      resume() { game.resume(); },
      _game: game,
    },
    writable: false,
    configurable: true,
  });
});


