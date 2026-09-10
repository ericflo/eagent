// game.js — Stratosmash: core engine (canvas, physics, topside system, fever,
// power-up drops, juice, HUD, input). Vanilla ES6, no dependencies beyond the
// other scripts: window.AudioSys, BrickLib, Particles, LevelLib + util.js globals.

'use strict';

// ---------------------------------------------------------------------------
// Tunables — the whole feel of the game lives in here.
// ---------------------------------------------------------------------------
const BONUS_ATK_DUR = 4;   // seconds of double points after a gem (snapshot fx)
const MAX_MULTIPLIER = 9;

const CONFIG = {
  // Speeds are px/sec. Levels declare speeds in "60fps units" so we ×60.
  BALL_R: 7,
  BALL_BASE_SPEED_FRAME: 7.2,   // floor; per-level def.baseSpeed overrides
  BALL_SPEED_CAP_FRAME: 9.8,
  BALL_SPEED_PER_BREAK: 0.12,   // frame-units of speed gained per broken brick
  PADDLE_W: 118,
  PADDLE_H: 16,
  PADDLE_BOTTOM_MARGIN: 20,
  PADDLE_KEY_SPEED: 560,        // keyboard horizontal px/s
  PADDLE_KEY_VSPEED: 260,       // keyboard vertical px/s
  PADDLE_FOLLOW_X: 14,          // pointer-follow lerp (higher = snappier)
  PADDLE_FOLLOW_Y: 9,
  PADDLE_LIFT_BOOST: 0.42,      // paddle upward speed → extra ball upward velocity
  PADDLE_HIT_BOOST: 0.26,       // paddle horizontal speed influence on ball
  PADDLE_MAX_BOUNCE: 0.9,       // max horizontal deflection from paddle offset
  WALL_TOP: 54,                 // ceiling y (below the HUD)
  WALL_SIDE: 8,                 // side walls
  DT_MAX: 1 / 30,               // clamp dt to kill tunneling

  // topside / heat — the core "keep it above the bricks" system
  FIELD_ABOVE_PAD: 8,           // how far above the top brick row counts as topside
  HEAT_PER_MULT: 5,             // heat units per multiplier step
  HEAT_MAX: 42,
  HEAT_AIR_RATE: 1.6,           // heat per second airborne (per ball) — was 0.55, too slow to ever engage
  HEAT_TOP_MULT: 0.8,           // extra heat/sec per ball above the field top (the reward loop!)
  HEAT_DECAY: 0.08,             // heat lost per second when the ball drops back — was 0.22, near-cancelled gains
  TOP_SUSPEND_MAX: 0.7,         // max seconds a ball may spend below a brick row without dropping heat
  AIR_SCORE_SEC: 60,            // pts per continuous second topside (× multiplier)
  AIR_TEXT_EVERY: 3,            // floating "TOPSIDE" text every N sec

  // fever
  FEVER_LENGTH: 8,
  FEVER_SCORE_MULT: 3,          // ×3 = +200%
  HEAT_FEVER: 18,               // heat alone can trigger fever at this level

  // combo
  COMBO_MAX: 99,
  COMBO_DECAY: 2.5,             // s without a break → combo resets
  COMBO_LIT: 4,                 // HUD chip lights from here
  COMBO_MILESTONE: 4,           // vignette pulse every Nth combo

  // brick requirements
  ANGLED_MAX_DEG: 18,           // |angle from horizontal| needed for angled bricks
  GUST_MIN_SPEED: 8.6,          // frame-units speed to pop a gust brick
  EXPLOSION_RADIUS: 110,        // bomb blast radius
  MULTI_SPLIT_FACTOR: 0.6,

  // fire ball
  FIRE_BURN_STANDARD: 0.22,     // chance to torch a standard brick on contact
  FIRE_BURN_GEM: 0.6,

  // ghost ball
  GHOST_CYCLE: 1.0,             // alternate solid / ghost every second

  // power-up drops
  DROP_CHANCE: 0.16,
  DROP_COMBO_BOOST: 1.6,        // × when combo >= 5
  DROP_GRAV: 420,               // px/s²
  DROP_MAX_SPEED: 300,
  DROP_R: 13,

  // lives
  START_LIVES: 3,
  LIFE_MAX: 5,

  // misc
  LEVEL_CLEAR_BONUS: 600,       // × level × multiplier
  LEVEL_CLEAR_DELAY: 2.2,
  STAR_PER_PX: 0.00045,
};
const S60 = 60; // frame-unit → px/s

// ---------------------------------------------------------------------------
// Small canvas helper (module scope, no per-frame allocs in hot paths).
// ---------------------------------------------------------------------------
function rr(c, x, y, w, h, r) {
  r = Math.min(r, w / 2, h / 2);
  c.beginPath();
  c.moveTo(x + r, y);
  c.arcTo(x + w, y, x + w, y + h, r);
  c.arcTo(x + w, y + h, x, y + h, r);
  c.arcTo(x, y + h, x, y, r);
  c.arcTo(x, y, x + w, y, r);
  c.closePath();
}

const BALL_GLYPHS = { normal: '•', fire: '✸', heavy: '⬟', ghost: '◌', multi: '✦' };
const isSolidWall = k => BrickLib.brickIsWall(k) || k === 'wall';

// lighten/darken a #rrggbb hex by t in [-1, 1] (toward white / toward black)
function lighten(hex, t) {
  const r = parseInt(hex.slice(1, 3), 16);
  const g = parseInt(hex.slice(3, 5), 16);
  const b = parseInt(hex.slice(5, 7), 16);
  const f = t >= 0 ? v => v + (255 - v) * t : v => v * (1 + t);
  return `rgb(${Math.round(f(r))},${Math.round(f(g))},${Math.round(f(b))})`;
}

// ===========================================================================
// Stratos — the engine
// ===========================================================================
class Stratos {
  constructor(canvas) {
    this.canvas = canvas;
    this.ctx = canvas.getContext('2d');
    this.W = 0; this.H = 0; this.dpr = 1;
    this.t = 0; this.lastT = 0; this.raf = 0;

    // persistent state (public names are contract for util.js snapshot())
    this.state = 'menu';            // menu | playing | paused | over | levelclear
    this.phase = 'serve';           // serve | live  (within playing)
    this.levelIndex = 0;
    this.levelName = '';
    this.score = 0; this.best = 0; this.combo = 1; this.lives = CONFIG.START_LIVES;
    this.xp = 0;
    this.multiplier = 1; this.heat = 0; this.heatSuspend = 0; this.fever = false; this.feverT = 0;
    this.bonusAtkDur = 0;
    this.airTime = 0; this.airTick = 0; this.airFloats = 0;
    this.comboNeed = 12; this.comboTimer = 0;
    this.pendingKind = 'normal';
    this.clearT = 0; this.levelClearDone = false;
    this.banner = { text: '', sub: '', t: 0, dur: 0 };

    this.bricks = [];
    this.balls = [];
    this.drops = [];
    this.stars = [];
    this.fieldTop = 0;

    this.paddle = {
      x: 0, y: 0, w: CONFIG.PADDLE_W, h: CONFIG.PADDLE_H,
      vx: 0, vy: 0, lastY: 0, glowT: 0,
    };
    this.input = { left: false, right: false, up: false, down: false };
    this.pointer = { active: false, touch: false, x: 0, y: 0, sx: 0, sy: 0, st: 0, moved: 0 };
    this.keys = {};

    this.shakeState = { mag: 0, t: 0 };
    this.flash = { color: '#ffffff', a: 0 };
    this.vignetteT = 0;
    this.scorePopT = 0;
    this.bounceGate = 0;
    this.musicLevel = -1;
    this.lastShownMult = 1;

    this.vignette = document.createElement('canvas');
    this.bgGrad = null;

    this.overlay = document.getElementById('overlay');
    this.overlayCard = document.getElementById('overlayCard');
    this.el = {
      score: document.getElementById('chipScore'),
      combo: document.getElementById('chipCombo'),
      lives: document.getElementById('chipLives'),
      mute: document.getElementById('btnMute'),
      pause: document.getElementById('btnPause'),
    };

    this.bound = {
      resize: () => this.resize(),
      keydown: e => this.onKey(e, true),
      keyup: e => this.onKey(e, false),
      pdown: e => this.onPointer(e, 'down'),
      pmove: e => this.onPointer(e, 'move'),
      pup: e => this.onPointer(e, 'up'),
      mute: () => this.toggleMute(),
      pause: () => this.togglePause(),
      touch: e => { if (e.cancelable) e.preventDefault(); },
    };

    // persistence
    this.best = this.loadBest();
    this.applySettings();

    this.bindEvents();
    this.resize();
    this.paddle.x = this.W / 2;
    this.paddle.y = this.H - CONFIG.PADDLE_H - CONFIG.PADDLE_BOTTOM_MARGIN;
    this.paddle.lastY = this.paddle.y;
    this.showMenu();
    this.lastT = performance.now() / 1000;
    const loop = now => {
      this.raf = requestAnimationFrame(loop);
      const t = now / 1000;
      const raw = t - this.lastT;
      this.lastT = t;
      const dt = clamp(raw, 0, CONFIG.DT_MAX);
      if (this.state !== 'paused') {
        this.t += dt;
        this.update(dt);
      }
      this.render();
    };
    requestAnimationFrame(loop);
  }

  // ---- lifecycle / resolution ----
  resize() {
    this.dpr = Math.min(window.devicePixelRatio || 1, 2);
    this.W = window.innerWidth;
    this.H = window.innerHeight;
    this.canvas.width = Math.round(this.W * this.dpr);
    this.canvas.height = Math.round(this.H * this.dpr);
    this.ctx.setTransform(this.dpr, 0, 0, this.dpr, 0, 0);
    this.bgGrad = null;
    this.buildStars();
    this.buildVignette();
    this.paddle.x = clamp(this.paddle.x, CONFIG.PADDLE_W / 2, this.W - CONFIG.PADDLE_W / 2);
    this.paddle.y = clamp(this.paddle.y, this.paddleYMin(), this.paddleYMax());
  }

  paddleYMin() { return CONFIG.WALL_TOP + CONFIG.PADDLE_H + 6; }
  paddleYMax() { return this.H - CONFIG.PADDLE_H - CONFIG.PADDLE_BOTTOM_MARGIN; }
  ballSpeedCap() { return CONFIG.BALL_SPEED_CAP_FRAME * S60; }

  buildStars() {
    this.stars.length = 0;
    const n = Math.round(this.W * this.H * CONFIG.STAR_PER_PX);
    for (let i = 0; i < n; i++) {
      this.stars.push({
        x: Math.random() * this.W, y: Math.random() * this.H,
        z: rnd(0.25, 1), size: rnd(0.6, 2.2),
      });
    }
  }

  buildVignette() {
    this.vignette.width = Math.max(2, this.W);
    this.vignette.height = Math.max(2, this.H);
    const c = this.vignette.getContext('2d');
    const g = c.createRadialGradient(
      this.W / 2, this.H * 0.45, Math.min(this.W, this.H) * 0.35,
      this.W / 2, this.H * 0.5, Math.max(this.W, this.H) * 0.75,
    );
    g.addColorStop(0, 'rgba(0,0,0,0)');
    g.addColorStop(1, 'rgba(2,4,12,0.55)');
    c.fillStyle = g;
    c.fillRect(0, 0, this.W, this.H);
  }

  // ---- events (named handlers, guard double-call) ----
  bindEvents() {
    if (this._bound) return;
    this._bound = true;
    window.addEventListener('resize', this.bound.resize);
    window.addEventListener('keydown', this.bound.keydown);
    window.addEventListener('keyup', this.bound.keyup);
    this.canvas.addEventListener('pointerdown', this.bound.pdown);
    this.canvas.addEventListener('pointermove', this.bound.pmove);
    this.canvas.addEventListener('pointerup', this.bound.pup);
    this.canvas.addEventListener('pointercancel', this.bound.pup);
    this.canvas.addEventListener('touchmove', this.bound.touch, { passive: false });
    this.el.mute.addEventListener('click', this.bound.mute);
    this.el.pause.addEventListener('click', this.bound.pause);
  }

  onKey(e, down) {
    const k = e.key;
    if (['ArrowLeft', 'a', 'A'].includes(k)) this.input.left = down;
    else if (['ArrowRight', 'd', 'D'].includes(k)) this.input.right = down;
    else if (['ArrowUp', 'w', 'W'].includes(k)) this.input.up = down;
    else if (['ArrowDown', 's', 'S'].includes(k)) this.input.down = down;
    else if (k === ' ' || k === 'Spacebar') {
      if (down) { e.preventDefault(); this.onActionKey(); }
    } else if (k === 'p' || k === 'P' || k === 'Escape') {
      if (down) { e.preventDefault(); this.togglePause(); }
    } else if (k === 'Enter' && down) {
      if (this.state === 'menu' || this.state === 'over') this.startGame();
    }
    if (['ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight', ' '].includes(k) && down) e.preventDefault();
  }

  onActionKey() {
    if (this.state === 'menu' || this.state === 'over') { this.startGame(); return; }
    if (this.state === 'playing') {
      if (this.phase === 'serve') this.serve();
      else this.togglePause();
    } else if (this.state === 'paused') this.togglePause();
  }

  // serve or resume via Space/click
  primaryAction() {
    if (this.state === 'menu' || this.state === 'over') { this.startGame(); return; }
    if (this.state === 'paused') { this.togglePause(); return; }
    if (this.state === 'playing' && this.phase === 'serve') this.serve();
  }

  onPointer(e, kind) {
    const x = e.clientX, y = e.clientY;
    if (kind === 'down') {
      AudioSys.ensure();
      this.pointer.active = true;
      this.pointer.touch = e.pointerType === 'touch';
      this.pointer.x = x; this.pointer.y = y;
      this.pointer.sx = x; this.pointer.sy = y; this.pointer.st = this.t; this.pointer.moved = 0;
      try { this.canvas.setPointerCapture(e.pointerId); } catch (err) { /* ok */ }
      this.handleTap(x, y);
    } else if (kind === 'move') {
      // mouse hover always steers; touch needs a live drag
      if (e.pointerType !== 'touch' || this.pointer.active) {
        this.pointer.x = x; this.pointer.y = y;
        if (this.pointer.active) {
          this.pointer.touch = e.pointerType === 'touch';
          this.pointer.moved = Math.max(this.pointer.moved, Math.hypot(x - this.pointer.sx, y - this.pointer.sy));
        }
      }
    } else {
      if (this.pointer.active) this.handleTapUp(x, y);
      this.pointer.active = false;
    }
  }

  // mouse click / touch tap while in serve → launch
  handleTap(x, y) {
    if (this.state === 'playing' && this.phase === 'serve') {
      this.serve();
    }
  }

  handleTapUp(x, y) {
    // only tap-launch if the pointer barely moved (a click, not a drag)
    if (this.pointer.moved < 10 && this.state === 'playing' && this.phase === 'serve') {
      this.serve();
    }
  }

  // ---- overlay ----
  showOverlay(html) {
    this.overlayCard.innerHTML = html;
    this.overlay.classList.remove('hidden');
  }
  hideOverlay() { this.overlay.classList.add('hidden'); }

  showMenu() {
    this.state = 'menu';
    this.showOverlay(`
      <h1>STRATOSMASH</h1>
      <div class="subtitle">Break through — then keep the ball on top. That part is the fun.</div>
      <ul class="rule-list">
        <li>🖱️ <b>Mouse:</b> move to steer the paddle, click to launch.</li>
        <li>👆 <b>Touch:</b> drag anywhere; pull <b>up</b> to lift the ball for extra speed.</li>
        <li>⌨️ <b>Keys:</b> arrows / A·D + W·S, Space to launch &amp; pause.</li>
        <li>🚀 Moving the paddle <b>up</b> as you hit the ball gives it a boost.</li>
        <li>💥 Angled bricks need shallow hits · gust bricks need speed.</li>
        <li>🔥 Reach the top of the field to build <b>heat</b> and ignite <b>FEVER</b>.</li>
      </ul>
      <button id="btnStart" class="btn">Start</button>
      <div class="hint">best ${fmt(this.best)}</div>
    `);
    const btn = document.getElementById('btnStart');
    if (btn) btn.addEventListener('click', () => this.startGame(), { once: true });
  }

  showPauseOverlay() {
    this.showOverlay(`
      <h1 style="font-size:30px;">PAUSED</h1>
      <div class="subtitle">Take a breath. The topside waits.</div>
      <div style="display:flex;gap:10px;justify-content:center;">
        <button id="btnResume" class="btn">Resume</button>
        <button id="btnRestart" class="btn secondary">Restart</button>
      </div>
    `);
    document.getElementById('btnResume').addEventListener('click', () => this.togglePause(), { once: true });
    document.getElementById('btnRestart').addEventListener('click', () => this.startGame(), { once: true });
  }

  showGameOver() {
    this.state = 'over';
    this.saveBest();
    this.showOverlay(`
      <h1>GAME OVER</h1>
      <div class="big">${fmt(this.score)}</div>
      <div class="result-sub">${this.score >= this.best && this.score > 0 ? '★ new best! ★' : `best ${fmt(this.best)}`}</div>
      <button id="btnAgain" class="btn">Play Again</button>
    `);
    document.getElementById('btnAgain').addEventListener('click', () => this.startGame(), { once: true });
  }

  // ---- start / level flow ----
  startGame() {
    AudioSys.ensure();
    AudioSys.resume();
    AudioSys.setEnabled(true);
    AudioSys.startMusic();
    this.score = 0; this.combo = 1; this.comboTimer = 0;
    this.lives = CONFIG.START_LIVES;
    this.xp = 0; this.heat = 0; this.multiplier = 1;
    this.fever = false; this.feverT = 0; this.bonusAtkDur = 0;
    this.drops.length = 0; this.balls.length = 0;
    Particles.clear();
    this.shakeState.mag = 0; this.flash.a = 0;
    this.musicLevel = -1;
    this.levelIndex = 0;
    this.hideOverlay();
    this.newLevel();
    this.updateHUD();
  }

  newLevel() {
    const { bricks, def } = LevelLib.buildLevel(this.levelIndex + 1, this.W, this.H);
    this.bricks = bricks;
    this.levelName = def.name || '';
    this.comboNeed = def.comboNeed || 12;
    let top = Infinity;
    for (const b of bricks) if (b.alive) top = Math.min(top, b.y - b.h);
    this.fieldTop = top === Infinity ? this.H * 0.2 : top - CONFIG.FIELD_ABOVE_PAD;
    this.banner = { text: `LEVEL ${this.levelIndex + 1} — ${this.levelName}`, sub: def.subtitle || '', t: 0, dur: 2.4 };
    this.phase = 'serve';
    this.state = 'playing';
    this.paddle.x = this.W / 2;
    this.paddle.y = this.paddleYMax();
    this.paddle.vx = 0; this.paddle.vy = 0;
    this.setMusicLevel(0);
    this.resetServeBalls();
  }

  resetServeBalls() {
    this.balls.length = 0;
    this.pushBall(this.paddle.x, this.paddle.y - CONFIG.PADDLE_H / 2 - CONFIG.BALL_R - 1, 0, 0, this.pendingKind || 'normal', true);
  }

  pushBall(x, y, vx, vy, kind, serve) {
    if (!serve) {
      this.balls.push({ x, y, vx, vy, r: CONFIG.BALL_R, kind, solid: true, ghostT: rnd(0, 0.4), trail: [] });
      return;
    }
    // serve: ball rests on paddle, no velocity yet
    this.balls.push({ x, y, vx: 0, vy: 0, r: CONFIG.BALL_R, kind, solid: true, ghostT: rnd(0, 0.4), trail: [] });
  }

  baseSpeed() {
    if (this.levelIndex >= 0) {
      const def = LevelLib.levelParams(this.levelIndex);
      if (def && def.baseSpeed) return def.baseSpeed * S60;
    }
    return CONFIG.BALL_BASE_SPEED_FRAME * S60;
  }

  serve() {
    if (this.state !== 'playing' || this.phase !== 'serve' || this.balls.length === 0) return;
    AudioSys.ensure();
    AudioSys.resume();
    let launched = 0;
    for (const b of this.balls) {
      if (b.vx !== 0 || b.vy !== 0) continue; // already live
      const speed = Math.max(this.baseSpeed(), this.ballSpeedCap() * 0.62);
      const ang = rnd(-0.4, 0.4);
      b.vx = Math.cos(ang) * speed * (chance(0.5) ? 1 : -1);
      b.vy = -Math.sin(ang) * speed;
      launched++;
    }
    if (!launched) return;
    this.phase = 'live';
    this.pendingKind = 'normal'; // consumed
    this.airTime = 0; this.airTick = 0; this.airFloats = 0;
    const first = this.balls[0];
    AudioSys.s.launch();
    if (first) Particles.spawn({ kind: 'ring', x: first.x, y: first.y, size: 14, color: PALETTE.cyan, life: 0.4, vrot: 0 });
  }

  // ---- main update ----
  update(dt) {
    // timers that run in every mode
    if (this.vignetteT > 0) this.vignetteT -= dt;
    if (this.scorePopT > 0) this.scorePopT -= dt;
    if (this.shakeState.t > 0) { this.shakeState.t -= dt; if (this.shakeState.t <= 0) this.shakeState.mag = 0; }
    if (this.flash.a > 0) this.flash.a = Math.max(0, this.flash.a - dt * 2.4);
    if (this.banner.t < this.banner.dur) this.banner.t += dt;
    if (this.airTime > 0) this.airTime = Math.max(0, this.airTime - dt * 0.35);
    if (this.bonusAtkDur > 0) this.bonusAtkDur -= dt;
    if (this.bounceGate > 0) this.bounceGate -= dt;

    if (this.state === 'menu' || this.state === 'over') {
      this.updateStars(dt);
      Particles.update(dt);
      return;
    }
    if (this.state === 'levelclear') {
      this.clearT -= dt;
      if (this.clearT <= 0) this.nextLevel();
      this.updateStars(dt);
      Particles.update(dt);
      return;
    }

    // playing / paused overlay handled by caller; here: playing only
    if (this.state === 'paused') { Particles.update(dt); return; }

    // spinners oscillate; combo timer; heats; fever
    for (const b of this.bricks) {
      if (!b.alive) continue;
      if (b.kind === 'spinner') {
        b.spin = mod(b.spin + b.spinSpeed * dt, TAU);
        b.x = b.spinBase + Math.sin(b.spin) * b.spinRange;
      }
    }
    if (this.comboTimer > 0) {
      this.comboTimer -= dt;
      if (this.comboTimer <= 0) this.loseCombo('combo lost');
    }
    this.updateHeat(dt);
    this.updateFever(dt);
    this.updateAir(dt);
    this.updateMult();

    this.updatePaddle(dt);
    this.updateBalls(dt);
    this.updateDrops(dt);

    if (this.phase === 'serve') {
      let off = -50;
      for (const b of this.balls) {
        if (b.vx === 0 && b.vy === 0) {
          b.x = this.paddle.x + off;
          b.y = this.paddle.y - CONFIG.PADDLE_H / 2 - CONFIG.BALL_R - 1;
          off += 36;
        }
      }
    }

    this.updateStars(dt);
    Particles.update(dt);
    this.updateHUD();
  }

  nextLevel() {
    this.levelIndex++;
    this.bonusAtkDur = 0;
    this.newLevel();
  }

  // ---- paddle ----
  updatePaddle(dt) {
    const p = this.paddle;
    p.lastY = p.y;
    let tx = p.x, ty = p.y;
    const keysActive = this.input.left || this.input.right || this.input.up || this.input.down;

    if (keysActive) {
      p.vx = 0; p.vy = 0;
      if (this.input.left) p.vx -= CONFIG.PADDLE_KEY_SPEED;
      if (this.input.right) p.vx += CONFIG.PADDLE_KEY_SPEED;
      if (this.input.up) p.vy -= CONFIG.PADDLE_KEY_VSPEED;
      if (this.input.down) p.vy += CONFIG.PADDLE_KEY_VSPEED;
      tx = p.x + p.vx * dt;
      ty = p.y + p.vy * dt;
    } else if (this.pointer.touch && this.pointer.active) {
      // thumbstick feel: horizontal = drag delta, vertical = centered dead-zone
      const dx = this.pointer.x - this.pointer.sx;
      tx = clamp(p.x + dx * 1.15, CONFIG.PADDLE_W / 2, this.W - CONFIG.PADDLE_W / 2);
      const cy = this.H / 2, dead = 34;
      const raw = this.pointer.y < cy - dead ? -1 : (this.pointer.y > cy + dead ? 1 : 0);
      ty = clamp(p.y + raw * 240 * dt, this.paddleYMin(), this.paddleYMax());
    } else if (this.pointer.active || this.pointer.x > 0) {
      // mouse: pointer-follow with smoothed velocity (hover steers too)
      tx = clamp(this.pointer.x, CONFIG.PADDLE_W / 2, this.W - CONFIG.PADDLE_W / 2);
      ty = clamp(this.pointer.y, this.paddleYMin(), this.paddleYMax());
      const kx = 1 - Math.exp(-CONFIG.PADDLE_FOLLOW_X * dt);
      const ky = 1 - Math.exp(-CONFIG.PADDLE_FOLLOW_Y * dt);
      p.vx = ((tx - p.x) / Math.max(dt, 1e-4)) * 0.6;
      p.vy = ((ty - p.y) / Math.max(dt, 1e-4)) * 0.35;
      p.x = lerp(p.x, tx, kx);
      p.y = lerp(p.y, ty, ky);
      p.x = clamp(p.x, CONFIG.PADDLE_W / 2, this.W - CONFIG.PADDLE_W / 2);
      p.y = clamp(p.y, this.paddleYMin(), this.paddleYMax());
      if (Math.abs(p.vy) < 14) p.vy = 0;
      return;
    }

    p.x = clamp(tx, CONFIG.PADDLE_W / 2, this.W - CONFIG.PADDLE_W / 2);
    p.y = clamp(ty, this.paddleYMin(), this.paddleYMax());
    if (!keysActive && !(this.pointer.active || this.pointer.x > 0)) {
      p.vx *= Math.max(0, 1 - 10 * dt);
      p.vy *= Math.max(0, 1 - 10 * dt);
    }
    if (keysActive && p.vy !== 0 && Math.abs(p.vy) < 14) { /* keep */ }
  }

  // ---- heat / topside core ----
  updateHeat(dt) {
    // heat ticks up while combos stack and while airborne above the field top,
    // ticks down when the ball drops back into the field.
    // KEY REWORK: heat scales with the NUMBER of balls topside, and brief dips
    // below the field no longer wipe progress — the reward loop must engage.
    const comboHeat = Math.max(0, (this.combo - 1) / 6);
    let airborne = 0;
    for (const b of this.balls) {
      if (b.y < this.fieldTop) airborne++;
    }
    if (airborne > 0) {
      const rate = CONFIG.HEAT_AIR_RATE + comboHeat * 0.6 + (airborne - 1) * CONFIG.HEAT_TOP_MULT;
      this.heat = Math.min(CONFIG.HEAT_MAX, this.heat + rate * dt);
      this.heatSuspend = 0;
    } else if (this.heatSuspend < CONFIG.TOP_SUSPEND_MAX) {
      this.heatSuspend += dt;
    } else {
      this.heat = Math.max(0, this.heat - CONFIG.HEAT_DECAY * dt);
    }
  }

  updateMult() {
    const m = 1 + Math.floor(this.heat / CONFIG.HEAT_PER_MULT);
    if (m !== this.multiplier) {
      this.multiplier = m;
      if (m >= 2 && m % 2 === 1) {
        Particles.spawn({ kind: 'text', x: this.W / 2, y: this.H * 0.4, text: `${m}x`, size: 42, color: PALETTE.yellow, life: 1.1, vy: -60 });
        AudioSys.s.blip();
      }
    }
  }

  updateAir(dt) {
    let any = false;
    // REWORK: topside counts while the ball is ABOVE the field top, regardless
    // of direction — a ball bouncing on top of the bricks is the fantasy.
    for (const b of this.balls) {
      if (b.y < this.fieldTop) { any = true; break; }
    }
    if (any) {
      this.airTime += dt;
      this.airTick += dt;
      while (this.airTick >= 1) {
        this.airTick -= 1;
        this.addScore(Math.round(CONFIG.AIR_SCORE_SEC * this.multiplier * this.pointMult()), this.W / 2, this.H * 0.3, PALETTE.cyan, 15);
      }
      if (this.airFloats < 3 && this.airTick >= 0.9) {
        this.airFloats++;
        this.airTick -= 0.9;
        Particles.spawn({ kind: 'text', x: this.W / 2, y: this.H * 0.26, text: 'TOPSIDE', size: 26, color: PALETTE.cyan, life: 1.2, vy: -50 });
      }
      // every CONFIG.AIR_TEXT_EVERY seconds keep the feedback going up top
      if (this.airTime >= this.airFloats * CONFIG.AIR_TEXT_EVERY && this.airFloats >= 3) {
        this.airFloats++;
        Particles.spawn({ kind: 'text', x: this.W / 2, y: this.H * 0.26, text: 'TOPSIDE!', size: 26, color: PALETTE.cyan, life: 1.2, vy: -50 });
      }
    } else if (this.airTick > 0) {
      this.airTick *= 0.9;
    }
  }

  pointMult() { return (this.fever ? CONFIG.FEVER_SCORE_MULT : 1); }

  updateFever(dt) {
    if (this.fever) {
      this.feverT -= dt;
      if (this.feverT <= 0) {
        this.fever = false;
        this.setMusicLevel(this.feverLevel());
        Particles.spawn({ kind: 'text', x: this.W / 2, y: this.H * 0.32, text: 'FEVER spent', size: 24, color: PALETTE.violet, life: 1.2, vy: -40 });
      } else {
        if (chance(dt * 0.8)) this.shake(4, 0.12);
        this.paddle.glowT = 0.5;
      }
    } else {
      const byHeat = this.heat >= CONFIG.HEAT_FEVER;
      const byCombo = this.combo >= this.comboNeed;
      if ((byHeat || byCombo) && this.balls.length > 0) {
        this.startFever();
      }
    }
  }

  startFever() {
    this.fever = true;
    this.feverT = CONFIG.FEVER_LENGTH;
    this.flashColor(PALETTE.yellow, 0.22);
    this.shake(10, 0.5);
    AudioSys.s.fever();
    this.setMusicLevel(2);
    Particles.spawn({ kind: 'ring', x: this.W / 2, y: this.H * 0.4, size: this.W * 0.3, color: PALETTE.yellow, life: 0.7 });
    Particles.spawn({ kind: 'text', x: this.W / 2, y: this.H * 0.3, text: 'FEVER!', size: 52, color: PALETTE.yellow, life: 1.6, vy: -50 });
    if (this.heat >= CONFIG.HEAT_FEVER) this.heat = Math.max(0, this.heat - CONFIG.HEAT_FEVER); // spend heat for the burst
    this.vignetteT = 1.2;
  }

  setMusicLevel(lvl) {
    if (lvl === this.musicLevel) return;
    this.musicLevel = lvl;
    try { AudioSys.setMusicLevel(lvl); } catch (err) { /* never throw */ }
  }

  feverLevel() { return this.fever ? 2 : (this.multiplier >= 4 ? 1 : (this.heat >= CONFIG.HEAT_PER_MULT * 2 ? 1 : 0)); }

  // ---- balls ----
  updateBalls(dt) {
    const stillAlive = [];
    for (const b of this.balls) {
      if (b.kind === 'ghost') {
        b.ghostT += dt;
        b.solid = Math.floor(b.ghostT / CONFIG.GHOST_CYCLE) % 2 === 0;
      }
      // speed grows with heat and fever (the "exponentially more awesome" bit)
      const speed = Math.hypot(b.vx, b.vy);
      if (speed > 0) {
        const grow = 1 + Math.min(0.9, this.heat / CONFIG.HEAT_MAX);
        const spd = speed + (CONFIG.BALL_SPEED_PER_BREAK * S60 * dt * 4) * (this.fever ? 2 : 1);
        const nx = b.vx / speed, ny = b.vy / speed;
        b.vx = nx * spd * grow;
        b.vy = ny * spd * grow;
      }
      this.moveBall(b, dt);
      if (b.y - b.r > this.H + 96 && b.vy > 0) {
        if (this.phase === 'live') this.loseBall(b); // a serve-phase ball just respawns
        // (removed from stillAlive either way)
      } else {
        stillAlive.push(b);
      }
    }
    this.balls = stillAlive;
    if (this.balls.length === 0) {
      if (this.state === 'playing') {
        if (this.phase === 'live') {
          if (this.lives < 0) this.gameOver();
          else this.spawnServe();
        } else {
          this.resetServeBalls();
        }
      }
    }
  }

  loseBall(b) {
    this.lives--;
    this.loseCombo('ball lost');
    AudioSys.s.bad();
    this.shake(6, 0.3);
    Particles.spawn({ kind: 'ring', x: b.x, y: this.H + 20, size: 22, color: PALETTE.red, life: 0.5 });
    this.airTime = 0; this.airTick = 0; this.airFloats = 0;
    // gameOver() is invoked from updateBalls when lives < 0
  }

  spawnServe() {
    this.phase = 'serve';
    if (this.balls.length === 0) this.resetServeBalls();
  }

  gameOver() {
    if (this.state === 'over') return;   // idempotent
    this.state = 'over';
    this.setMusicLevel(0);
    AudioSys.s.lose();
    this.fever = false;
    this.showGameOver();
  }

  // ---- brick collisions ----
  collideBricks(b) {
    for (const br of this.bricks) {
      if (!br.alive) continue;
      // cheap reject
      if (b.x + b.r < br.x - br.w / 2 || b.x - b.r > br.x + br.w / 2) continue;
      if (b.y + b.r < br.y - br.h / 2 || b.y - b.r > br.y + br.h / 2) continue;
      // actual AABB-circle
      const cx = clamp(b.x, br.x - br.w / 2, br.x + br.w / 2);
      const cy = clamp(b.y, br.y - br.h / 2, br.y + br.h / 2);
      const dx = b.x - cx, dy = b.y - cy;
      if (dx * dx + dy * dy >= b.r * b.r) continue;

      // ---- ghost: phases through everything except solid walls ----
      if (b.kind === 'ghost' && !b.solid) {
        if (isSolidWall(br.kind)) this.bounceOffBrick(b, br);
        continue;
      }

      // ---- fire ball: zips through standard bricks, bounces off hard ones ----
      if (b.kind === 'fire') {
        // fire always detonates bombs on contact
        if (br.kind === 'bomb') { this.breakBrick(br, b, 'fire'); continue; }
        if (br.kind === 'standard' || br.kind === 'multi') {
          // occasionally scorch a brick as it passes
          if (br.kind === 'standard' && chance(CONFIG.FIRE_BURN_STANDARD)) this.breakBrick(br, b, 'fire', false);
          else if (br.kind === 'multi' && chance(0.28)) this.breakBrick(br, b, 'fire', false);
          if (br.alive) Particles.spawn({ kind: 'spark', x: b.x, y: b.y, vx: rnd(-40, 40), vy: rnd(-40, 40), life: 0.25, size: 2.5, color: PALETTE.orange });
          continue;
        }
        this.bounceOffBrick(b, br);
        continue;
      }

      // ---- heavy ball: smashes everything except metal/unbreak walls ----
      if (b.kind === 'heavy') {
        if (isSolidWall(br.kind) || br.kind === 'metal') {
          this.bounceOffBrick(b, br);
          this.shake(5, 0.15);
          AudioSys.s.heavy();
          continue;
        }
        this.breakBrick(br, b, 'heavy');
        continue;
      }

      // ---- solid walls bounce for every remaining kind ----
      if (isSolidWall(br.kind) || br.kind === 'metal') {
        this.bounceOffBrick(b, br);
        continue;
      }

      // ---- normal & multi: respect brick gating ----
      if (br.kind === 'angled') {
        // must hit at a near-horizontal angle (|angle from horizontal| < 18°)
        const ang = Math.abs(Math.atan2(Math.abs(b.vy), Math.abs(b.vx)));
        const deg = ang * 180 / Math.PI;
        if (deg < CONFIG.ANGLED_MAX_DEG) {
          this.breakBrick(br, b, 'normal');
        } else {
          this.bounceOffBrick(b, br);
          AudioSys.s.blip();
          Particles.spawn({ kind: 'text', x: br.x, y: br.y, text: 'SHALLOW!', size: 13, color: PALETTE.cyan, life: 0.8, vy: -60 });
        }
        continue;
      }
      if (br.kind === 'gust') {
        const sp = Math.hypot(b.vx, b.vy) / S60;
        if (sp >= CONFIG.GUST_MIN_SPEED) {
          this.breakBrick(br, b, 'normal');
        } else {
          this.bounceOffBrick(b, br);
          AudioSys.s.blip();
          Particles.spawn({ kind: 'text', x: br.x, y: br.y - 14, text: 'FASTER!', size: 13, color: PALETTE.green, life: 0.8, vy: -60 });
        }
        continue;
      }
      if (br.kind === 'sturdy') {
        // hp2 brick: first hit cracks it, second breaks
        if (!br.cracked) { br.cracked = true; br.hp = 1; br.text = '1'; }
        this.bounceOffBrick(b, br);
        Particles.spawn({ kind: 'spark', x: b.x, y: b.y, vx: rnd(-60, 60), vy: rnd(-60, 60), life: 0.3, size: 3, color: br.color });
        AudioSys.s.paddle();
        continue;
      }
      // standard, gem, spinner, bomb, multi → break
      this.breakBrick(br, b, 'normal');
    }
  }

  bounceOffBrick(b, br) {
    if (this.bounceGate > 0) return;
    // reflect off the brick face the ball hit
    const cx = clamp(b.x, br.x - br.w / 2, br.x + br.w / 2);
    const cy = clamp(b.y, br.y - br.h / 2, br.y + br.h / 2);
    const dx = b.x - cx, dy = b.y - cy;
    if (Math.abs(dx) > Math.abs(dy) * 0.8) {
      b.vx = -b.vx;
      b.x = dx > 0 ? br.x + br.w / 2 + b.r : br.x - br.w / 2 - b.r;
    } else {
      b.vy = -b.vy;
      b.y = dy > 0 ? br.y + br.h / 2 + b.r : br.y - br.h / 2 - b.r;
    }
    AudioSys.s.bounce();
    Particles.spawn({ kind: 'spark', x: b.x, y: b.y, vx: rnd(-50, 50), vy: rnd(-50, 50), life: 0.3, size: 2.5, color: br.color || '#fff' });
    this.shake(1.6, 0.05);
    this.bounceGate = 0.02;
  }

  breakBrick(br, ball, cause, bounce = true) {
    if (!br.alive) return;
    br.alive = false;
    const comboBefore = this.combo;
    this.comboTimer = CONFIG.COMBO_DECAY;

    if (br.kind === 'bomb') {
      // bomb: explode (chain reaction damages nearby bricks)
      this.explodeBomb(br, ball);
      return;
    }
    if (br.kind === 'multi' && !br.split) {
      br.split = true;
      // split into two smaller standard bricks
      const subW = br.w * CONFIG.MULTI_SPLIT_FACTOR;
      const subH = br.h * CONFIG.MULTI_SPLIT_FACTOR;
      this.bricks.push(BrickLib.makeBrick('standard', br.x - subW / 2 - 2, br.y, subW, subH));
      this.bricks.push(BrickLib.makeBrick('standard', br.x + subW / 2 + 2, br.y, subW, subH));
    }
    // bounce the ball off the broken brick (unless it passed through, e.g. fire)
    if (bounce && ball && ball.vx !== undefined) this.bounceOffBrick(ball, br);

    // scoring
    const base = BrickLib.brickScore(br.kind, this.combo);
    this.addScore(base * this.pointMult(), br.x, br.y, br.color, br.kind === 'gem' ? 26 : 16);
    this.combo += 1;
    if (this.combo >= CONFIG.COMBO_MILESTONE && comboBefore % CONFIG.COMBO_MILESTONE < 1) {
      this.vignetteT = 0.5;
      AudioSys.s.blip();
    }
    this.setMusicLevel(this.feverLevel());

    // XP + drop
    this.xp += LevelLib.xpAward(BALL_KIND_SEVERITY[ball && ball.kind] || 1);
    this.maybeDrop(br, ball);

    // juice
    this.breakJuice(br, ball, cause);

    // gem bonus
    if (br.kind === 'gem') {
      this.bonusAtkDur = BONUS_ATK_DUR;
      this.addScore(1500 * this.pointMult(), br.x, br.y, PALETTE.green, 24);
      AudioSys.s.gem();
      this.shake(5, 0.2);
      this.flashColor(PALETTE.green, 0.15);
    }

    this.checkLevelClear();
  }

  // ---- drops ----
  updateDrops(dt) {
    for (let i = this.drops.length - 1; i >= 0; i--) {
      const d = this.drops[i];
      d.t += dt;
      d.vy += CONFIG.DROP_GRAV * dt;
      d.vy = Math.min(d.vy, CONFIG.DROP_MAX_SPEED);
      d.x += d.vx * dt;
      d.y += d.vy * dt;
      // catch by paddle
      const p = this.paddle;
      if (d.y + d.r > p.y - p.h / 2 && d.y - d.r < p.y + p.h / 2 + 6
          && d.x > p.x - p.w / 2 - d.r && d.x < p.x + p.w / 2 + d.r) {
        this.catchDrop(d);
        this.drops.splice(i, 1);
        continue;
      }
      if (d.y - d.r > this.H + 30) { this.drops.splice(i, 1); continue; }
      // gentle bob so drops are visible
      d.x += Math.sin(d.t * 5 + d.y) * 8 * dt;
    }
  }

  catchDrop(d) {
    AudioSys.s.power();
    this.flashColor(d.color, 0.12);
    Particles.spawn({ kind: 'ring', x: d.x, y: d.y, size: 26, color: d.color, life: 0.5 });
    for (let i = 0; i < 10; i++) {
      const a = Math.random() * TAU;
      Particles.spawn({ kind: 'spark', x: d.x, y: d.y, vx: Math.cos(a) * rnd(60, 220), vy: Math.sin(a) * rnd(60, 220), life: rnd(0.3, 0.6), size: 3, color: d.color });
    }
    switch (d.kind) {
      case 'life':
        this.lives = Math.min(CONFIG.LIFE_MAX, this.lives + 1);
        AudioSys.s.life();
        Particles.spawn({ kind: 'text', x: d.x, y: d.y - 20, text: '+1 LIFE', size: 20, color: PALETTE.green, life: 1, vy: -60 });
        break;
      case 'extra':
        this.addExtraBall();
        Particles.spawn({ kind: 'text', x: d.x, y: d.y - 20, text: '+BALL', size: 18, color: PALETTE.yellow, life: 1, vy: -60 });
        break;
      default: // fire / heavy / ghost / multi → next serve ball gets the kind
        this.pendingKind = d.kind;
        Particles.spawn({ kind: 'text', x: d.x, y: d.y - 20, text: d.kind.toUpperCase(), size: 16, color: d.color, life: 1, vy: -60 });
        // if we're mid-rally, apply to the currently lowest ball immediately
        if (this.balls.length > 0 && this.phase === 'live') {
          const lowest = this.balls.reduce((a, b2) => (b2.y > (a ? a.y : -Infinity) ? b2 : a));
          if (lowest && lowest.kind === 'normal') { lowest.kind = d.kind; lowest.solid = true; lowest.trail.length = 0; }
        }
        break;
    }
  }

  addExtraBall() {
    if (this.phase === 'serve') {
      // stack: the next serve will be multi
      const b = this.balls[0];
      if (b) {
        const nb = { x: b.x, y: b.y, vx: 0, vy: 0, r: b.r, kind: 'normal', solid: true, ghostT: 0, trail: [] };
        this.balls.push(nb);
      }
    } else {
      // launch from the paddle (doesn't wait for serve)
      const p = this.paddle;
      const ang = rnd(-0.5, 0.5);
      const sp = Math.max(this.baseSpeed(), this.ballSpeedCap() * 0.6);
      this.balls.push({
        x: p.x, y: p.y - p.h / 2 - CONFIG.BALL_R - 1, r: CONFIG.BALL_R,
        vx: Math.cos(ang) * sp * (chance(0.5) ? 1 : -1), vy: -Math.sin(ang) * sp,
        kind: this.pendingKind, solid: true, ghostT: 0, trail: [],
      });
      this.pendingKind = 'normal';
      AudioSys.s.launch();
    }
  }

  // ---- scoring & combo ----
  addScore(pts, x, y, color, size) {
    const real = Math.round(pts);
    this.score += real;
    this.scorePopT = 0.35;
    Particles.spawn({ kind: 'text', x, y, text: '+' + fmt(real), size: size || 16, color: color || '#fff', life: 1, vy: -70 });
  }

  loseCombo(reason) {
    if (this.combo <= 1) return;
    this.combo = 1;
    Particles.spawn({ kind: 'text', x: this.W / 2, y: this.H * 0.4, text: reason, size: 18, color: 'rgba(255,255,255,0.7)', life: 1, vy: -40 });
  }

  shake(mag, dur) {
    this.shakeState.mag = Math.max(this.shakeState.mag, mag);
    this.shakeState.t = Math.max(this.shakeState.t, dur);
  }

  flashColor(color, a) {
    this.flash.color = color;
    this.flash.a = Math.max(this.flash.a, a);
  }

  // ---- juice & render ----
  render() {
    const c = this.ctx, W = this.W, H = this.H;
    // background
    if (!this.bgGrad) {
      const g = c.createRadialGradient(W / 2, H * 0.35, 60, W / 2, H * 0.5, Math.max(W, H) * 0.8);
      g.addColorStop(0, '#131a33');
      g.addColorStop(0.6, '#0b0f1e');
      g.addColorStop(1, '#060913');
      this.bgGrad = g;
      c.fillStyle = g;
    } else {
      c.fillStyle = this.bgGrad;
    }
    c.fillRect(0, 0, W, H);

    c.save();
    // screen shake
    if (this.shakeState.t > 0) {
      const m = this.shakeState.mag * (this.shakeState.t / Math.max(0.01, this.shakeState.t + this.shakeState.mag));
      c.translate(rnd(-m, m), rnd(-m, m));
    }

    this.drawStars(c);
    this.drawBricks(c);
    this.drawDrops(c);
    this.drawBalls(c);
    this.drawPaddle(c);

    // fever gold overlay pulse
    if (this.fever) {
      c.fillStyle = rgba(PALETTE.yellow, 0.05 + Math.sin(this.t * 6) * 0.03);
      c.fillRect(-20, -20, W + 40, H + 40);
    }

    c.restore();

    // vignette pulse on combo milestones
    if (this.vignetteT > 0) {
      c.drawImage(this.vignette, 0, 0, W, H);
    }
    // screen flash
    if (this.flash.a > 0) {
      c.fillStyle = rgba(this.flash.color, this.flash.a * 0.4);
      c.fillRect(0, 0, W, H);
    }
    // score pop
    if (this.scorePopT > 0) {
      const s = 1 + this.scorePopT * 0.6;
      this.el.score.style.transform = `scale(${s})`;
    } else {
      this.el.score.style.transform = '';
    }

    Particles.draw(c);
    this.drawBanner(c);
    if (this.state === 'playing' && this.phase === 'serve') {
      const a = 0.55 + Math.sin(this.t * 4) * 0.3;
      c.globalAlpha = a;
      c.font = 'bold 15px "Segoe UI", system-ui, sans-serif';
      c.textAlign = 'center';
      c.fillStyle = PALETTE.cyan;
      c.fillText('CLICK / TAP / SPACE to launch', this.W / 2, this.H - 90);
      c.globalAlpha = 1;
    }
  }

  updateStars(dt) {
    const speed = 0.6 + this.multiplier * 0.5;
    for (const s of this.stars) {
      s.y += s.z * speed * dt * 60;
      if (s.y > this.H) { s.y = 0; s.x = Math.random() * this.W; }
    }
  }

  drawStars(c) {
    for (const s of this.stars) {
      c.globalAlpha = 0.25 + s.z * 0.55;
      c.fillStyle = '#cfeaff';
      c.fillRect(s.x, s.y, s.size, s.size);
    }
    c.globalAlpha = 1;
  }

  drawBricks(c) {
    for (const b of this.bricks) {
      if (!b.alive) continue;
      const x = b.x - b.w / 2, y = b.y - b.h / 2;
      c.globalAlpha = 1;
      if (b.kind === 'spinner') {
        c.strokeStyle = b.color;
        c.lineWidth = 2;
        c.strokeRect(x, y, b.w, b.h);
        c.fillStyle = rgba(b.color, 0.15);
        c.fillRect(x, y, b.w, b.h);
        c.fillStyle = b.color;
        c.beginPath(); c.arc(b.x, b.y, 3, 0, TAU); c.fill();
      } else if (b.kind === 'wall' || b.kind === 'unbreak') {
        c.fillStyle = lighten(b.color, 0.1);
        c.fillRect(x, y, b.w, b.h);
        c.strokeStyle = rgba('#ffffff', 0.1);
        c.lineWidth = 1;
        c.strokeRect(x + 0.5, y + 0.5, b.w - 1, b.h - 1);
      } else {
        const grad = c.createLinearGradient(x, y, x, y + b.h);
        grad.addColorStop(0, lighten(b.color, 0.35));
        grad.addColorStop(0.5, b.color);
        grad.addColorStop(1, lighten(b.color, -0.28));
        c.fillStyle = grad;
        rr(c, x, y, b.w, b.h, 4);
        c.fill();
        c.strokeStyle = rgba('#ffffff', 0.18);
        c.lineWidth = 1;
        c.stroke();
        // hp pips for sturdy (cracked = hp 1, text flips to '1')
        if (b.kind === 'sturdy' && b.cracked) {
          c.fillStyle = 'rgba(255,255,255,0.85)';
          c.font = 'bold 12px sans-serif';
          c.textAlign = 'center';
          c.fillText('1', b.x, b.y + 4);
        } else if (b.kind === 'sturdy') {
          c.fillStyle = 'rgba(255,255,255,0.85)';
          c.font = 'bold 12px sans-serif';
          c.textAlign = 'center';
          c.fillText('2', b.x, b.y + 4);
        }
        if (b.text && b.kind !== 'sturdy') {
          c.fillStyle = 'rgba(255,255,255,0.8)';
          c.font = 'bold ' + Math.min(22, b.h * 0.7) + 'px sans-serif';
          c.textAlign = 'center';
          c.fillText(b.text, b.x, b.y + b.h * 0.28 + 8);
        }
      }
    }
  }

  drawDrops(c) {
    for (const d of this.drops) {
      c.save();
      c.translate(d.x, d.y);
      c.rotate(Math.sin(d.t * 3) * 0.2);
      c.fillStyle = d.color;
      rr(c, -d.r, -d.r, d.r * 2, d.r * 2, 6);
      c.fill();
      c.fillStyle = '#0b0f1e';
      c.font = 'bold 13px sans-serif';
      c.textAlign = 'center';
      c.textBaseline = 'middle';
      c.fillText(d.kind === 'extra' ? '+' : d.kind[0].toUpperCase(), 0, 1);
      c.restore();
    }
  }

  drawBalls(c) {
    for (const b of this.balls) {
      // trail
      const col = BALL_COLORS[b.kind] || '#fff';
      const n = b.trail.length / 3;
      for (let i = 0; i < n; i++) {
        const tt = b.trail[i * 3 + 2];
        const a = clamp((this.t - tt) / 0.35, 0, 1) * 0.35;
        c.globalAlpha = a;
        c.fillStyle = col;
        c.beginPath();
        c.arc(b.trail[i * 3], b.trail[i * 3 + 1], b.r * (1 - i / n) * 0.8, 0, TAU);
        c.fill();
      }
      c.globalAlpha = 1;
      if (b.kind === 'ghost' && !b.solid) {
        c.globalAlpha = 0.35;
      }
      // glow
      c.shadowColor = col;
      c.shadowBlur = 12 + this.heat * 0.6;
      c.fillStyle = col;
      c.beginPath();
      c.arc(b.x, b.y, b.r, 0, TAU);
      c.fill();
      c.shadowBlur = 0;
      // glyph
      c.fillStyle = '#0b0f1e';
      c.font = 'bold 10px sans-serif';
      c.textAlign = 'center';
      c.textBaseline = 'middle';
      c.fillText(BALL_GLYPHS[b.kind] || '•', b.x, b.y + 1);
      c.globalAlpha = 1;
    }
  }

  drawPaddle(c) {
    const p = this.paddle;
    const active = p.glowT > 0 || this.fever;
    c.save();
    c.translate(p.x, p.y);
    // subtle tilt on vertical motion
    c.rotate(clamp(p.vy / 1200, -0.12, 0.12));
    const w = p.w, h = p.h;
    if (active) {
      c.shadowColor = PALETTE.cyan;
      c.shadowBlur = 16 + (this.fever ? 10 : 0);
    }
    const grad = c.createLinearGradient(0, -h / 2, 0, h / 2);
    grad.addColorStop(0, this.fever ? '#ffd23f' : '#7ae6ff');
    grad.addColorStop(1, this.fever ? '#ff9f43' : '#2aa8e0');
    c.fillStyle = grad;
    rr(c, -w / 2, -h / 2, w, h, 8);
    c.fill();
    c.shadowBlur = 0;
    // energy bar along the top edge
    const energy = clamp(this.heat / CONFIG.HEAT_MAX, 0, 1);
    if (energy > 0.02) {
      c.fillStyle = rgba(PALETTE.yellow, 0.8);
      c.fillRect(-w / 2 + 6, -h / 2 - 3, (w - 12) * energy, 3);
    }
    if (active) {
      c.strokeStyle = rgba('#ffffff', 0.6);
      c.lineWidth = 1.5;
      rr(c, -w / 2 - 2, -h / 2 - 2, w + 4, h + 4, 10);
      c.stroke();
    }
    c.restore();
    if (p.glowT > 0) p.glowT -= 0.016;
  }

  drawBanner(c) {
    if (this.banner.t >= this.banner.dur) return;
    const a = clamp(this.banner.t / 0.4, 0, 1) * clamp((this.banner.dur - this.banner.t) / 0.5, 0, 1);
    c.globalAlpha = a;
    c.font = 'bold 34px "Segoe UI", system-ui, sans-serif';
    c.textAlign = 'center';
    c.fillStyle = '#fff';
    c.strokeStyle = 'rgba(0,0,0,0.6)';
    c.lineWidth = 4;
    const y = this.H * 0.22;
    c.strokeText(this.banner.text, this.W / 2, y);
    c.fillText(this.banner.text, this.W / 2, y);
    c.globalAlpha = a * 0.85;
    c.font = '15px "Segoe UI", system-ui, sans-serif';
    c.fillStyle = PALETTE.cyan;
    c.fillText(this.banner.sub, this.W / 2, y + 26);
    c.globalAlpha = 1;
  }

  // ---- HUD ----
  updateHUD() {
    this.el.score.textContent = String(Math.round(this.score));
    this.el.lives.textContent = '♥ ' + Math.max(0, this.lives != null ? this.lives : 0);
    const lit = this.combo >= CONFIG.COMBO_LIT;
    this.el.combo.textContent = 'COMBO ' + this.combo + 'x';
    this.el.combo.classList.toggle('lit', lit);
    if (this.multiplier !== this.lastShownMult) {
      this.lastShownMult = this.multiplier;
      this.el.combo.style.transform = 'scale(1.25)';
      setTimeout(() => { this.el.combo.style.transform = ''; }, 160);
    }
  }

  // ---- persistence ----
  loadBest() {
    try { return parseInt(localStorage.getItem('stratosmash-best') || '0', 10) || 0; }
    catch (err) { return 0; }
  }
  saveBest() {
    if (this.score > this.best) {
      this.best = this.score;
      try { localStorage.setItem('stratosmash-best', String(this.best)); } catch (err) { /* ignore */ }
    }
  }
  applySettings() {
    let m = false;
    try { m = localStorage.getItem('stratosmash-muted') === '1'; } catch (err) { /* ignore */ }
    AudioSys.setMuted(m);
    AudioSys.setEnabled(true);
    this.el.mute.textContent = m ? '♪' : '♪';
    this.el.mute.classList.toggle('muted', m);
  }
  toggleMute() {
    const m = !AudioSys.muted;
    AudioSys.setMuted(m);
    this.el.mute.classList.toggle('muted', m);
    try { localStorage.setItem('stratosmash-muted', m ? '1' : '0'); } catch (err) { /* ignore */ }
    if (!m) AudioSys.ensure();
  }
  togglePause() {
    if (this.state === 'playing') {
      this.state = 'paused';
      this.showPauseOverlay();
    } else if (this.state === 'paused') {
      this.state = 'playing';
      this.hideOverlay();
      this.lastT = performance.now() / 1000;
    }
  }

  // util (shared with util.js snapshot)
  aliveCount() {
    let n = 0;
    for (const b of this.bricks) if (b.alive) n++;
    return n;
  }
  getBricksAlive() { return this.aliveCount(); }
  getBricksTotal() { return this.bricks.length; }

  // sturdy has hp 2; breakBrick is only called when it should die (see collide)
  breakJuice(br, ball, cause) {
    Particles.spawn({ kind: 'ring', x: br.x, y: br.y, size: Math.max(br.w, br.h) * 0.6, color: br.color, life: 0.4 });
    const n = 6 + Math.min(10, Math.round(this.heat / 2)) + (this.multiplier > 2 ? 4 : 0) + (this.fever ? 6 : 0);
    for (let i = 0; i < n; i++) {
      const a = Math.random() * TAU, sp = rnd(60, 260 + this.heat * 6);
      Particles.spawn({ kind: 'shard', x: br.x + rnd(-br.w / 2, br.w / 2), y: br.y + rnd(-br.h / 2, br.h / 2), vx: Math.cos(a) * sp, vy: Math.sin(a) * sp - 60, life: rnd(0.4, 0.9), size: rnd(3, 7), color: br.color, grav: 320, drag: 1.4, rot: Math.random() * TAU, vrot: rnd(-8, 8) });
    }
    AudioSys.s.break();
    if (this.fever && chance(0.5)) AudioSys.s.blip();
  }

  explodeBomb(br, ball) {
    // damage bricks within radius; chain breaks possible
    let boom = 0;
    for (const other of this.bricks) {
      if (!other.alive || other === br || BrickLib.brickIsWall(other.kind)) continue;
      if (dist2(br.x, br.y, other.x, other.y) < CONFIG.EXPLOSION_RADIUS * CONFIG.EXPLOSION_RADIUS) {
        boom++;
        this.breakBrick(other, ball, 'bomb');
      }
    }
    if (boom > 0) { this.addScore(250 * boom, br.x, br.y, PALETTE.orange, 18); }
    this.shake(14, 0.45);
    this.flashColor(PALETTE.red, 0.3);
    AudioSys.s.heavy();
    for (let i = 0; i < 26; i++) {
      const a = Math.random() * TAU, sp = rnd(120, 480);
      Particles.spawn({ kind: 'shard', x: br.x, y: br.y, vx: Math.cos(a) * sp, vy: Math.sin(a) * sp, life: rnd(0.5, 1), size: rnd(3, 8), color: PALETTE.red, grav: 400, drag: 1.5, rot: Math.random() * TAU, vrot: rnd(-10, 10) });
    }
    Particles.spawn({ kind: 'ring', x: br.x, y: br.y, size: CONFIG.EXPLOSION_RADIUS, color: PALETTE.red, life: 0.6 });
  }

  maybeDrop(br, ball) {
    let p = CONFIG.DROP_CHANCE;
    if (this.combo >= 5) p *= CONFIG.DROP_COMBO_BOOST;
    if (br.kind === 'gem') p = 1;
    if (br.kind === 'bomb') p = 0;
    if (chance(p)) {
      const kinds = ['fire', 'heavy', 'ghost', 'multi', 'extra', 'extra'];
      if (chance(0.09)) kinds.push('life');
      const kind = kinds[Math.floor(Math.random() * kinds.length)];
      this.drops.push({
        x: br.x, y: br.y, r: CONFIG.DROP_R, kind,
        vy: rnd(-30, 0), vx: rnd(-40, 40), t: 0,
        color: kind === 'life' ? '#7dffb2' : (kind === 'extra' ? '#ffd23f' : (BALL_COLORS[kind] || '#fff')),
      });
    }
  }

  checkLevelClear() {
    let alive = 0;
    for (const b of this.bricks) {
      if (b.alive && b.kind !== 'unbreak' && !BrickLib.brickIsWall(b.kind)) alive++;
    }
    if (alive === 0 && this.state === 'playing' && this.phase === 'live') {
      this.state = 'levelclear';
      this.clearT = CONFIG.LEVEL_CLEAR_DELAY;
      this.setMusicLevel(0);
      AudioSys.s.victory();
      const bonus = CONFIG.LEVEL_CLEAR_BONUS * (this.levelIndex + 1) * this.multiplier;
      this.addScore(bonus, this.W / 2, this.H * 0.4, PALETTE.yellow, 30);
      Particles.spawn({ kind: 'ring', x: this.W / 2, y: this.H * 0.35, size: this.W * 0.45, color: PALETTE.yellow, life: 1 });
      this.fever = false;
    }
  }

  moveBall(b, dt) {
    const speed = Math.hypot(b.vx, b.vy);
    if (speed < 1) return; // resting serve ball / stopped: stays put this frame
    const steps = Math.ceil(Math.max(1, (speed * dt) / (b.r * 0.9)));
    const sdt = dt / steps;
    for (let i = 0; i < steps; i++) {
      b.x += b.vx * sdt;
      b.y += b.vy * sdt;
      this.collideBall(b, sdt);
    }
    // trail
    if (speed > 40) {
      b.trail.push(b.x, b.y, this.t);
      const max = 7 + Math.min(18, this.heat * 0.5) + (this.fever ? 8 : 0);
      if (b.trail.length > max * 3) b.trail.splice(0, b.trail.length - max * 3);
    } else if (b.trail.length) b.trail.length = 0;
  }

  minBallSpeed() { return this.baseSpeed() * 0.72; }

  collideBall(b, dt) {
    // playfield walls
    if (b.x - b.r < CONFIG.WALL_SIDE && b.vx < 0) { b.x = CONFIG.WALL_SIDE + b.r; b.vx = -b.vx; this.wallBounce(b); }
    else if (b.x + b.r > this.W - CONFIG.WALL_SIDE && b.vx > 0) { b.x = this.W - CONFIG.WALL_SIDE - b.r; b.vx = -b.vx; this.wallBounce(b); }
    if (b.y - b.r < CONFIG.WALL_TOP && b.vy < 0) {
      b.y = CONFIG.WALL_TOP + b.r;
      b.vy = -b.vy;
      this.wallBounce(b);
    }

    // paddle
    if (b.vy > 0 && b.y + b.r > this.paddle.y - this.paddle.h / 2 && b.y - b.r < this.paddle.y + this.paddle.h / 2 + 4
        && b.x > this.paddle.x - this.paddle.w / 2 - b.r && b.x < this.paddle.x + this.paddle.w / 2 + b.r
        && this.bounceGate <= 0) {
      this.paddleHit(b);
    }

    // bricks
    if (b.solid) this.collideBricks(b);
  }

  wallBounce(b) {
    AudioSys.s.bounce();
    Particles.spawn({ kind: 'ring', x: b.x, y: b.y, size: 8, color: PALETTE.cyan, life: 0.3 });
    this.shake(2.2, 0.07);
    if (chance(0.5)) Particles.spawn({ kind: 'spark', x: b.x, y: b.y, vx: rnd(-60, 60), vy: rnd(-30, 10), life: 0.4, size: 2.5, color: '#eaf4ff' });
  }

  paddleHit(b) {
    const p = this.paddle;
    this.bounceGate = 0.05;
    // horizontal deflection from where it struck the paddle
    const off = clamp((b.x - p.x) / (p.w / 2), -1, 1);
    const speed = Math.hypot(b.vx, b.vy);
    const rel = b.x - p.x;
    let nx = off * CONFIG.PADDLE_MAX_BOUNCE;
    let ny = -Math.sqrt(Math.max(0.12, 1 - nx * nx));
    if (b.vy > 0) {
      // coming down: reflect up, add paddle velocity influence
      const px = clamp(p.vx / 400, -1, 1) * CONFIG.PADDLE_HIT_BOOST;
      nx = clamp(nx + px, -CONFIG.PADDLE_MAX_BOUNCE - 0.2, CONFIG.PADDLE_MAX_BOUNCE + 0.2);
    }
    // vertical "lift": paddle moving up when hit
    let lift = 0;
    if (p.vy < -40) lift = -p.vy * CONFIG.PADDLE_LIFT_BOOST;
    if (lift > 0) {
      ny -= lift / Math.max(1, speed);
      ny = clamp(ny, -1, -0.22);
      Particles.spawn({ kind: 'text', x: b.x, y: b.y - 20, text: 'LIFT', size: 14, color: PALETTE.cyan, life: 0.6, vy: -40 });
      p.glowT = 0.8;
    }
    const len = Math.hypot(nx, ny);
    nx /= len; ny /= len;
    // final speed: base + fever bonus + lift kick, clamped to the cap
    const ns = Math.min(speed + (this.fever ? 30 : 0) + (lift ? lift * 0.6 : 0), this.ballSpeedCap());
    b.vx = nx * ns;
    b.vy = ny * ns;
    b.y = p.y - p.h / 2 - b.r - 0.5;
    b.trail.length = 0;
    AudioSys.s.paddle();
    p.glowT = 0.45;
    Particles.spawn({ kind: 'ring', x: b.x, y: b.y + b.r, size: 10, color: PALETTE.cyan, life: 0.28 });
    // split multi ball on first paddle hit into two normal balls
    if (b.kind === 'multi' && !b.split) {
      b.split = true;
      b.kind = 'normal';
      this.balls.push({ x: b.x, y: b.y, vx: b.vx + 140, vy: b.vy, r: b.r, kind: 'normal', solid: true, ghostT: 0, trail: [] });
      b.vx -= 140;
    }
    this.comboTimer = CONFIG.COMBO_DECAY;
  }

  getSnapshot() { return snapshot(this); }
}

// ---- boot ----
let __game = null;
function boot() {
  if (__game) return __game;   // guard: double-call safe
  const canvas = document.getElementById('game');
  if (!canvas) return null;
  __game = new Stratos(canvas);
  window.__game = {
    startGame: () => __game.startGame(),
    getSnapshot: () => __game.getSnapshot(),
    _engine: __game,
    get state() { return __game.state; },
    get phase() { return __game.phase; },
    get score() { return __game.score; },
    get combo() { return __game.combo; },
    get lives() { return __game.lives; },
    get level() { return __game.levelIndex + 1; },
    get balls() { return __game.balls.length; },
    get bricksAlive() { return __game.aliveCount(); },
    get bricksTotal() { return __game.bricks.length; },
    get multiplier() { return __game.multiplier; },
    get fever() { return __game.fever; },
  };
  return __game;
}
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', boot, { once: true });
} else {
  boot();
}
