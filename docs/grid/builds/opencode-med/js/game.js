// ---------------------------------------------------------------------------
// game.js — simulation + orchestration (part 1: setup, loop, state)
// ---------------------------------------------------------------------------

import {
  W, H, WALL, CEIL, BRICK, PADDLE, BALL, POWERUP, STEEP, SPEED_GATE,
  OVERDRIVE, COMBO, COLORS, heatColor,
} from './config.js';
import { clamp, lerp, rand, randi, pick, hypot, easeOutCubic, fmt, circleRect, TAU } from './utils.js';
import { LEVELS, TYPE_INFO, brickRect, COLS } from './levels.js';
import { Brick, PHASE_PERIOD } from './bricks.js';
import { Particles } from './particles.js';
import { Starfield } from './starfield.js';
import { Input } from './input.js';
import { audio } from './audio.js';
import { Powerup, rollDrop, PU } from './powerups.js';
import * as R from './render.js';

const STATE = { TITLE: 0, INTRO: 1, PLAY: 2, SERVE: 3, CLEAR: 4, DEAD: 5, GAMEOVER: 6, PAUSE: 7 };

// short per-level names for the HUD pill (full names still used on the serve card)
const HUD_NAMES = ['CONTACT', 'THIN ICE', 'GREEN', 'BLINK', 'SKYWARD', 'VAULT', 'CHAIN', 'SPIN', 'RAMPART', 'GAUNTLET'];

export class Game {
  constructor(canvas) {
    this.canvas = canvas;
    this.ctx = canvas.getContext('2d', { alpha: false });
    this.dpr = 1;
    this.scale = 1;

    this.state = STATE.TITLE;
    this.stateT = 0;
    this.time = 0;
    this.timeScale = 1;      // slow-mo / hitstop
    this._tsTarget = 1;
    this.hitstop = 0;

    // scoring
    this.score = 0;
    this.lives = 3;
    this.levelIdx = 0;
    this.combo = 0;
    this.bestCombo = 0;
    this.best = +(localStorage.getItem('breaktop.best') || 0);
    this.scoreShown = 0;     // display score (eases)

    // overdrive
    this.odActive = false;
    this.odTimer = 0;
    this.odMult = 1;
    this.odCharge = 0;       // 0..100 meter
    this.heat = 0;           // 0..1 visual/audio heat

    // entities
    this.particles = new Particles();
    this.stars = new Starfield();
    this.input = new Input(canvas);
    this.input.onLaunch = () => this.tryLaunch();
    this.input.onPause = () => this.togglePause();
    // touch-capable device? drives platform-appropriate copy (title/serve/pause)
    this._touch = ('ontouchstart' in window) || (navigator.maxTouchPoints || 0) > 0;
    this.powerups = [];
    this.shakeT = 0; this.shakeAmp = 0;
    this.flash = 0; this.flashColor = '#ffffff';
    this.tFlash = 0;

    // paddle
    this.paddle = {
      x: W / 2, y: H - 70, w: PADDLE.w, h: PADDLE.h,
      vx: 0, vy: 0, sx: 1, sy: 1, tilt: 0,
      magnet: false, wideT: 0,
    };

    // balls
    this.balls = [];
    this.spawnBall(true);

    // bricks / level
    this.bricks = [];
    this.remaining = 0;
    this.levelTime = 0;

    // screen
    this._resize();
    window.addEventListener('resize', () => this._resize());
    window.addEventListener('orientationchange', () => setTimeout(() => this._resize(), 60));

    // DOM
    this.$ = (id) => document.getElementById(id);
    this.dom = {
      score: this.$('score'), best: this.$('best'), level: this.$('level'),
      lives: this.$('lives'), odFill: this.$('od-fill'), odMeter: this.$('od-meter'),
      odBanner: this.$('od-banner'), odText: this.$('od-text'), comboTag: this.$('combo-tag'),
      sound: this.$('btn-sound'), pause: this.$('btn-pause'),
    };
    this._setupDom();

    // first gesture → audio
    const kick = () => { audio.start(); audio.resume(); };
    window.addEventListener('pointerdown', kick, { once: false });
    window.addEventListener('keydown', kick, { once: false });
    window.addEventListener('touchstart', kick, { once: false });

    this.setTitleDemo();
    this._last = performance.now();
    requestAnimationFrame((t) => this._frame(t));
  }

  R() { return R; }
  PU() { return PU; }
  STATE() { return STATE; }

  _setupDom() {
    this.dom.sound.addEventListener('click', (e) => {
      e.stopPropagation();
      audio.setMuted(!audio.muted);
      this.dom.sound.textContent = audio.muted ? '♪̸' : '♪';
      this.dom.sound.style.opacity = audio.muted ? 0.4 : 1;
    });
    this.dom.pause.addEventListener('click', (e) => { e.stopPropagation(); this.togglePause(); });

    // touch control mode toggle (appears only on touch devices)
    if (this._touch) {
      const btn = document.createElement('button');
      btn.className = 'hud-btn mode-btn';
      btn.style.width = 'auto';
      btn.style.padding = '0 16px';
      const setLabel = () => { btn.textContent = this.input.mode === 'drag' ? '✥ DRAG' : '✛ STICK'; };
      setLabel();
      btn.addEventListener('click', (e) => {
        e.stopPropagation();
        this.input.mode = this.input.mode === 'drag' ? 'stick' : 'drag';
        try { localStorage.setItem('breaktop.mode', this.input.mode); } catch (_) {}
        setLabel();
        audio.ui();
      });
      // sits with the sound/pause buttons so it shares their inset + rounding
      const right = document.querySelector('#hud-top .hud-right');
      (right || document.getElementById('hud-top')).appendChild(btn);
      this.dom.ctrl = btn;
    }
    this.dom.best.textContent = 'BEST ' + fmt(this.best);
    this.updateLives();
  }

  _resize() {
    const vw = window.innerWidth, vh = window.innerHeight;
    this.dpr = clamp(window.devicePixelRatio || 1, 1, 2.5);
    // Canvas covers the ENTIRE screen (not just the letterboxed field) so the
    // starfield / nebula backdrop is painted to every edge. The playfield is
    // then centered inside at logical 720x1080; the remaining gutters are
    // painted with the themed backdrop plus (on desktop) DOM side panels.
    this.canvas.width = Math.round(vw * this.dpr);
    this.canvas.height = Math.round(vh * this.dpr);
    this.viewW = vw; this.viewH = vh;

    const s = Math.min(vw / W, vh / H);
    this.scale = s;
    this.offX = (vw - W * s) / 2;
    this.offY = (vh - H * s) / 2;
    // gutters around the field in logical units (for canvas-side dressing)
    this.gutterW = Math.max(0, (vw / s - W) / 2);
    this.gutterV = Math.max(0, (vh / s - H) / 2);
    this.input.setScale(s);
    this._applyHudMode();
    this.placeOverlays();
  }

  /** toggles the desktop side panels on/off for the current viewport */
  _applyHudMode() {
    const wide = !!(this.viewW > 0 && this.viewH > 0 && this.viewW >= 1180 && this.viewW / this.viewH > 1.45);
    this.domWide = wide;
    // anchor the panels to the playfield edges so panels + field tile the
    // window with no dead seams between them
    document.documentElement.style.setProperty(
      '--field-half', `${Math.round((W / 2) * this.scale)}px`);
    for (const id of ['panel-left', 'panel-right']) {
      const el = document.getElementById(id);
      if (el) el.classList.toggle('on', wide);
    }
  }

  /** overlays (banner/combo) sit inside a reserved HUD strip below the top
      HUD and clear of the brick field (bricks start at y≈150) */
  placeOverlays() {
    if (!this.viewH || !this.dom) return;
    const strip = H * 0.062;                    // logical y of the strip top
    const toCssPct = (y) => (this.offY + y * this.scale) / this.viewH * 100;
    const topPct = toCssPct(strip);
    this.dom.odBanner.style.top = topPct + '%';
    this.dom.comboTag.style.top = `calc(${topPct}% + 58px)`;
  }

  // ---- lifecycle helpers ---------------------------------------------------

  spawnBall(stuck = false) {
    const b = {
      x: W / 2, y: H - 90, vx: 0, vy: 0, r: BALL.r,
      stuck, stuckDx: 0,
      trail: [],
      power: null, powerT: 0,
      baseR: BALL.r,
    };
    this.balls.push(b);
    return b;
  }

  tryLaunch() {
    audio.resume();
    if (this.state === STATE.TITLE) { this.startGame(); return; }
    if (this.state === STATE.GAMEOVER) { this.startGame(); return; }
    // magnet: release early on tap
    if (this.state === STATE.PLAY) {
      let released = false;
      for (const b of this.balls) {
        if (b.stuck && b._releaseT !== undefined) {
          b.stuck = false; b._releaseT = undefined;
          const a = -Math.PI / 2 + clamp((b.x - this.paddle.x) / 200, -0.7, 0.7);
          const sp = Math.max(BALL.base, 520);
          b.vx = Math.cos(a) * sp; b.vy = Math.sin(a) * sp;
          released = true;
        }
      }
      if (released) audio.launch();
      return;
    }
    if (this.state === STATE.SERVE) {
      this.state = STATE.PLAY;
      for (const b of this.balls) {
        if (b.stuck) {
          b.stuck = false;
          const a = -Math.PI / 2 + rand(-0.22, 0.22);
          const sp = BALL.base;
          b.vx = Math.cos(a) * sp; b.vy = Math.sin(a) * sp;
        }
      }
      audio.launch();
    }
  }

  togglePause() {
    if (this.state === STATE.PAUSE) { this.state = this._prePause; this.dom.pause.textContent = '⏸'; }
    else if (this.state === STATE.PLAY || this.state === STATE.SERVE) {
      this._prePause = this.state;
      this.state = STATE.PAUSE;
      this.dom.pause.textContent = '▶';
    }
  }

  startGame() {
    this.score = 0; this.scoreShown = 0;
    this.lives = 3;
    this.levelIdx = 0;
    this.combo = 0;
    this.odActive = false; this.odTimer = 0; this.odMult = 1; this.odCharge = 0;
    this.heat = 0;
    this.paddle.magnet = false;
    this.loadLevel(0);
  }

  loadLevel(i) {
    this.levelIdx = i;
    const def = LEVELS[i % LEVELS.length];
    // short title for the HUD (long names truncate on narrow phones)
    const shortName = HUD_NAMES[i % LEVELS.length] || def.name;
    this.bricks = [];
    let phaseSeed = Math.random() * PHASE_PERIOD;
    let row = 0;
    for (const line of def.rows) {
      for (let c = 0; c < line.length && c < COLS; c++) {
        const ch = line[c];
        if (ch === '.') continue;
        this.bricks.push(new Brick(ch, c, row, (phaseSeed += 0.21) % PHASE_PERIOD));
      }
      row++;
    }
    this.remaining = this.bricks.length;
    this.levelTime = 0;
    this.balls = [];
    this.powerups = [];
    this.paddle.magnet = false;
    this.paddle.w = PADDLE.w;
    this.demoBall = null;
    this.spawnBall(true);
    this.resetOverdrive();
    this.state = STATE.SERVE;
    this.stateT = 0;
    this.combo = 0;
    this.particles.clear();
    audio.setHeat(0);
    audio.setLayers(0);
    this.dom.level.textContent = `LV ${i + 1} · ${shortName}`;
  }

  setTitleDemo() {
    // decorative brick arrangement + drifting balls for the title screen
    this.loadLevelTitle();
  }

  loadLevelTitle() {
    const def = LEVELS[4]; // SKYWARD for the crown look
    this.bricks = [];
    let phaseSeed = 0;
    let row = 0;
    for (const line of def.rows) {
      for (let c = 0; c < line.length && c < COLS; c++) {
        const ch = line[c];
        if (ch === '.') continue;
        this.bricks.push(new Brick(ch, c, row, (phaseSeed += 0.17) % PHASE_PERIOD));
      }
      row++;
    }
    this.balls = [];
    this.demoBall = null;
    const b = this.spawnBall(false);          // exactly ONE ambient demo ball
    const a = rand(-2.4, -0.7);
    b.x = rand(WALL + 60, W - WALL - 60);
    b.y = rand(260, 460);
    const sp = rand(360, 470);
    b.vx = Math.cos(a) * sp; b.vy = Math.sin(a) * sp;
    b.stuck = false;
    b.demo = true;
    this.demoBall = b;
    this.powerups = [];
    this.particles.clear();
  }
}

// ---------------------------------------------------------------------------
// part 2: main loop, update, overdrive, physics
// ---------------------------------------------------------------------------

Object.assign(Game.prototype, {
  _frame(now) {
    requestAnimationFrame((t) => this._frame(t));
    let dt = (now - this._last) / 1000;
    this._last = now;
    dt = Math.min(dt, 1 / 20);

    // hitstop: brief freeze scaled down over time
    if (this.hitstop > 0) {
      this.hitstop -= dt;
      this.timeScale = 0.06;
    } else if (this._slowmoT > 0) {
      this._slowmoT -= dt;
      this.timeScale = lerp(this.timeScale, this._slowmoTarget || 0.35, 1 - Math.exp(-8 * dt));
    } else {
      this.timeScale = lerp(this.timeScale, this._tsTarget, 1 - Math.exp(-10 * dt));
    }

    const sdt = dt * this.timeScale;
    this.time += dt;
    this.stateT += dt;
    this.update(sdt, dt);
    this.render();
  },

  slowmo(dur, target = 0.3) { this._slowmoT = dur; this._slowmoTarget = target; },

  update(sdt, rdt) {
    const S = STATE;
    this.stars.update(rdt, this.odActive ? 1 : 0, this.gutterV || 0);
    this.updateHud(rdt);

    // decay feedback timers
    if (this.shakeT > 0) this.shakeT = Math.max(0, this.shakeT - rdt * 1.6);
    this.flash = Math.max(0, this.flash - rdt * 2.6);
    this.tFlash = Math.max(0, this.tFlash - rdt * 2.2);

    // score display ease
    if (this.scoreShown < this.score) {
      this.scoreShown = Math.min(this.score, this.scoreShown + Math.max(1, (this.score - this.scoreShown) * 0.14));
    }

    // paddle always animates (squash etc.)
    this.updatePaddle(sdt, rdt);

    // bricks ambient
    for (const br of this.bricks) {
      if (br.shakeT > 0) br.shakeT = Math.max(0, br.shakeT - rdt * 2);
      if (br.flash > 0) br.flash = Math.max(0, br.flash - rdt * 5);
      if (br.deny > 0) br.deny = Math.max(0, br.deny - rdt * 1.4);
      if (br.type === 'W') br.spin += br.spinV * sdt;
      if (br.breaking > 0) {
        br.breaking += rdt * 2.4;
        if (br.frags) {
          for (const f of br.frags) {
            f.vy += 900 * rdt;
            f.x += f.vx * rdt; f.y += f.vy * rdt; f.rot += f.vr * rdt;
          }
        }
        if (br.breaking >= 1) br.breaking = 0;
      }
    }

    // powerups fall
    for (let i = this.powerups.length - 1; i >= 0; i--) {
      const pu = this.powerups[i];
      pu.update(sdt, 1);
      // catch check
      const p = this.paddle;
      const hw = (p.w * p.sx / 2) * POWERUP.catchW + pu.r;
      if (Math.abs(pu.x - p.x) < hw && Math.abs(pu.y - p.y) < p.h * p.sy + pu.r + 6 && pu.vy > 0) {
        this.powerups.splice(i, 1);
        this.catchPowerup(pu.id, pu.x, pu.y);
      } else if (pu.y > H + 40) {
        this.powerups.splice(i, 1);
      }
    }

    // particles (real time for smoothness)
    this.particles.update(rdt);

    if (this.state === S.TITLE) {
      this.updateBallsDemo(sdt);
      this.heat = lerp(this.heat, 0, 1 - Math.exp(-3 * rdt));
      audio.setHeat(0);
      return;
    }
    if (this.state === S.PAUSE || this.state === S.GAMEOVER) return;

    this.levelTime += sdt;

    if (this.state === S.SERVE) {
      // ball follows paddle while stuck
      for (const b of this.balls) {
        if (b.stuck) {
          b.x = this.paddle.x + (b.stuckDx || 0);
          b.y = this.paddle.y - b.r - this.paddle.h / 2 - 2;
          b.trail.length = 0;
        }
      }
      return;
    }

    // overdrive timer
    if (this.odActive) this.updateOverdrive(sdt);

    // slow-mo ball power
    this._slowActive = false;
    for (const b of this.balls) {
      if (b.power === 'slow') this._slowActive = true;
      if (b.power && b.power !== 'giant') {
        b.powerT -= sdt;
        if (b.powerT <= 0) { b.power = null; b.r = b.baseR; }
      }
      if (b.power === 'giant') {
        b.powerT -= sdt;
        if (b.powerT <= 0) { b.power = null; b.r = b.baseR; }
      }
    }
    this._tsTarget = this._slowActive ? 0.62 : 1;

    this.updateBalls(sdt);
  },

  updatePaddle(sdt, rdt) {
    const p = this.paddle;
    const want = this.input.update(rdt, p.x, p.y);
    // ease velocity toward desired (feels weighty but responsive)
    const k = 1 - Math.exp(-22 * rdt);
    p.vx = lerp(p.vx, want.vx, k);
    p.vy = lerp(p.vy, want.vy, k);
    p.x += p.vx * sdt;
    p.y += p.vy * sdt;
    // clamp to band and walls
    const halfW = p.w / 2;
    p.x = clamp(p.x, WALL + halfW, W - WALL - halfW);
    p.y = clamp(p.y, PADDLE.bandTop, PADDLE.bandBottom);
    // squash target from vertical velocity + impact timer
    const syT = 1 - clamp(Math.abs(p.vy) / 2600, 0, 0.28);
    const sxT = 1 + clamp(Math.abs(p.vy) / 2600, 0, 0.28);
    p.sy = lerp(p.sy, syT, 1 - Math.exp(-14 * rdt));
    p.sx = lerp(p.sx, sxT, 1 - Math.exp(-14 * rdt));
    if (p._impT > 0) {
      p._impT = Math.max(0, p._impT - rdt * 5);
      const q = p._impT;
      p.sy = 1 - q * 0.45;
      p.sx = 1 + q * 0.5;
    }
    p.tilt = clamp(p.vx / 26000, -0.09, 0.09);
    const wideTarget = p.wideT > 0 ? PADDLE.w * 1.55 : PADDLE.w;
    if (Math.abs(p.w - wideTarget) > 0.5) p.w = lerp(p.w, wideTarget, 1 - Math.exp(-10 * rdt));
    if (p.wideT > 0) p.wideT -= rdt;
    // stuck ball follows handled in SERVE; magnet attract
    for (const b of this.balls) {
      if (b.stuck) {
        b.x = p.x + (b.stuckDx || 0);
        b.y = p.y - b.r - p.h / 2 - 2;
      }
    }
  },

  resetOverdrive() {
    if (this.odActive) this.payout();
    this.odActive = false; this.odTimer = 0; this.odMult = 1;
    this.odCharge = 0;
    this.heat = lerp(this.heat, 0, 0);
    this.dom.odBanner.classList.add('hidden');
    audio.setLayers(this.combo >= 2 ? 1 : 0);
  },

  updateOverdrive(sdt) {
    const OD = OVERDRIVE;
    this.odTimer += sdt;
    this.odMult = Math.min(OD.multMax, Math.pow(2, this.odTimer / OD.multTime));
    const heatT = clamp(this.odTimer / OD.heatTime, 0, 1);
    this.heat = lerp(this.heat, heatT, 1 - Math.exp(-6 * sdt));
    audio.setHeat(this.heat);
    audio.setLayers(this.heat > 0.5 ? 3 : 2);
    this.odCharge = clamp(this.odCharge + (OD.chargeRate + OD.chargeHeat * this.heat) * sdt, 0, 100);
    // banner text
    this.dom.odText.textContent = `OVERDRIVE ×${this.odMult.toFixed(1)}`;
    this.dom.odBanner.classList.remove('hidden');
    this.dom.odFill.classList.toggle('hot', this.heat > 0.4);
    // ambient embers
    if (Math.random() < 10 * sdt * (1 + this.heat * 4)) {
      this.particles.spawn(rand(WALL, W - WALL), CEIL + 2, rand(-30, 30), rand(40, 130),
        rand(0.5, 1.3), rand(2, 4.5), heatColor(this.heat, 95, 68), { grav: -60, drag: 0.4, glow: 8, shape: 'dot' });
    }
  },

  heating() { return OVERDRIVE.heatTime; },

  addOdCharge(n) {
    this.odCharge = clamp(this.odCharge + n, 0, 100);
  },
});

// ---------------------------------------------------------------------------
// part 3: ball physics — swept circle vs walls/paddle/bricks (sub-stepped)
// ---------------------------------------------------------------------------

Object.assign(Game.prototype, {
  updateBallsDemo(sdt) {
    for (const b of this.balls) {
      this.moveBallDemo(b, sdt);
    }
  },

  moveBallDemo(b, dt) {
    // simple walls + bounce (title demo)
    const steps = Math.max(1, Math.ceil((Math.hypot(b.vx, b.vy) * dt) / (b.r * 0.8)));
    const h = dt / steps;
    for (let i = 0; i < steps; i++) {
      b.x += b.vx * h; b.y += b.vy * h;
      if (b.x < WALL + b.r) { b.x = WALL + b.r; b.vx = Math.abs(b.vx); }
      if (b.x > W - WALL - b.r) { b.x = W - WALL - b.r; b.vx = -Math.abs(b.vx); }
      if (b.y < CEIL + b.r) { b.y = CEIL + b.r; b.vy = Math.abs(b.vy); }
      if (b.y > H - 30) { b.y = H - 30; b.vy = -Math.abs(b.vy); }
      // brick bounce (non destructive)
      for (const br of this.bricks) {
        if (!br.alive) continue;
        const hit = circleRect(b.x, b.y, b.r, br.x, br.y, br.w, br.h);
        if (hit) {
          b.x += hit.nx * hit.depth; b.y += hit.ny * hit.depth;
          if (hit.nx) b.vx *= -1; if (hit.ny) b.vy *= -1;
          br.flash = 0.6;
          break;
        }
      }
      this.demoTrail(b);
    }
  },

  demoTrail(b) {
    b.trail.push({ x: b.x, y: b.y });
    if (b.trail.length > 9) b.trail.shift();
  },

  updateBalls(sdt) {
    const S = STATE;
    for (let bi = this.balls.length - 1; bi >= 0; bi--) {
      const b = this.balls[bi];
      if (b.stuck) continue;
      const speed = Math.hypot(b.vx, b.vy);
      // speed floor/ceiling
      if (speed > 0 && speed < BALL.min) {
        const f = BALL.min / speed;
        b.vx *= f; b.vy *= f;
      }
      if (speed > BALL.max) {
        const f = BALL.max / speed;
        b.vx *= f; b.vy *= f;
      }
      b.ignite = speed >= SPEED_GATE ? 1 : 0;

      // ---- sub-stepped swept movement ----
      const spd = Math.hypot(b.vx, b.vy);
      const steps = Math.max(1, Math.min(12, Math.ceil((spd * sdt) / (b.r * 0.85))));
      const h = sdt / steps;
      let dead = false;
      for (let s = 0; s < steps; s++) {
        b.x += b.vx * h; b.y += b.vy * h;
        if (this.stepBall(b)) { dead = true; break; }
      }
      // trail
      b.trail.push({ x: b.x, y: b.y });
      const maxTrail = 8 + Math.floor(this.heat * 22) + (spd > 700 ? 6 : 0);
      while (b.trail.length > maxTrail) b.trail.shift();

      if (dead) {
        this.balls.splice(bi, 1);
        if (this.balls.length === 0) this.loseBall();
      }
    }
  },

  /** one sub-step of collision. Returns true if ball was lost. */
  stepBall(b) {
    const r = b.r;
    let hitWall = false;

    // --- walls ---
    if (b.x - r < WALL) { b.x = WALL + r; b.vx = Math.abs(b.vx); hitWall = true; }
    else if (b.x + r > W - WALL) { b.x = W - WALL - r; b.vx = -Math.abs(b.vx); hitWall = true; }
    if (b.y - r < CEIL) { b.y = CEIL + r; b.vy = Math.abs(b.vy); hitWall = true; this.onCeilingHit(b); }
    if (hitWall && b.vy !== 0) {
      this.particles.ring(b.x, b.y, 4, 26, 0.3, this.heat > 0.05 ? '#ffcf8a' : '#7dd6ff', 2.5);
      audio.wallBounce();
      if (this.odActive) {
        this.addOdCharge(OVERDRIVE.wallBonus);
      }
      this.shake(1.2);
    }

    // --- paddle ---
    const p = this.paddle;
    const hw = (p.w * p.sx) / 2, hh = (p.h * p.sy) / 2;
    if (b.vy > 0) {
      const hit = circleRect(b.x, b.y, r, p.x - hw, p.y - hh, hw * 2, hh * 2);
      if (hit) {
        b.y = Math.min(b.y, p.y - hh - r - 0.5);
        this.paddleHit(b);
        return false;
      }
    }

    // --- bottom (lost) ---
    if (b.y - r > H + 24) return true;

    // --- bricks ---
    this.ballBricks(b);
    return false;
  },

  onCeilingHit(b) {
    if (this.odActive) {
      this.particles.ring(b.x, b.y, 4, 40, 0.35, '#ffe9a0', 3);
      this.particles.burst(b.x, b.y, '#ffd9a0', 8, { speed: 190, grav: 200 });
    }
  },

  paddleHit(b) {
    const p = this.paddle;
    // magnet: catch
    if (p.magnet) {
      b.stuck = true;
      b.stuckDx = clamp(b.x - p.x, -(p.w / 2 - 10), p.w / 2 - 10);
      b.vy = 0; b.vx = 0;
      b._releaseT = 0.75;
      audio.paddleBounce(400, 0);
      this.particles.ring(b.x, p.y - p.h, 4, 30, 0.3, '#f472b6', 2);
      return;
    }
    // angle from contact offset
    const off = clamp((b.x - p.x) / (p.w / 2), -1, 1);
    const speed = Math.hypot(b.vx, b.vy);
    const maxA = BALL.launchAngle;
    let ang = -Math.PI / 2 + off * maxA;
    // english from paddle velocity
    const pvx = p.vx, pvy = p.vy;
    // horizontal paddle velocity tilts angle
    ang += clamp(pvx / 3400, -0.5, 0.5);
    // vertical lift: moving up adds speed; moving down subtracts
    const lift = clamp(-pvy / 900, -0.5, 1.1);       // up = positive
    let ns = speed + BALL.creep + lift * 150;
    ns = clamp(ns, BALL.min, BALL.max);
    // lift also flattens trajectory slightly when smashing upward
    if (lift > 0.5) ang += clamp(pvx / 6000, -0.1, 0.1);
    b.vx = Math.cos(ang) * ns;
    b.vy = Math.sin(ang) * ns;
    if (b.vy > -140) b.vy = -140; // always send it up meaningfully
    // combo reset on paddle touch (classic)
    if (this.combo > 0 && this.time - (this._lastPaddleComboT || 0) > 0.4) {
      this.endCombo(false);
    }
    // feedback
    p._impT = 1;
    this.particles.ring(b.x, p.y - p.h / 2, 3, 24, 0.25, '#bfe6ff', 2);
    this.particles.burst(b.x, p.y - p.h / 2, '#9fd4ff', 5, { speed: 130, grav: 300 });
    audio.paddleBounce(ns, lift);
    this.shake(1 + lift * 2);
    this._lastPaddleComboT = this.time;
  },

  ballBricks(b) {
    const speed = Math.hypot(b.vx, b.vy);
    for (const br of this.bricks) {
      if (!br.alive) continue;
      if (br.type === 'P' && !br.isSolid(this.levelTime)) continue;
      // quick reject
      if (b.x + b.r < br.x || b.x - b.r > br.x + br.w || b.y + b.r < br.y || b.y - b.r > br.y + br.h) continue;
      let hit = circleRect(b.x, b.y, b.r, br.x, br.y, br.w, br.h);
      if (!hit) continue;

      // spinner: sphere-ish — just resolve & break
      const canBreak = br.canBreak(b.vx, b.vy, speed, b.power);
      // resolve position + reflect
      const goingIn = (hit.nx * b.vx + hit.ny * b.vy) < 0;
      if (goingIn) {
        // reflect around normal (keep energy)
        const d = (b.vx * hit.nx + b.vy * hit.ny);
        b.vx -= 2 * d * hit.nx;
        b.vy -= 2 * d * hit.ny;
        // tiny speed jitter to avoid loops
        b.vx += rand(-8, 8);
      }
      b.x += hit.nx * (hit.depth + 0.5);
      b.y += hit.ny * (hit.depth + 0.5);

      if (canBreak) {
        this.breakBrick(br, b, speed);
      } else {
        br.deny = 0.55;
        br.shakeT = 0.4;
        br.flash = 0.15;
        audio.thunk();
        this.particles.burst(hit.px, hit.py, '#8899bb', 4, { speed: 90, grav: 400, life: 0.5 });
        this.shake(1);
        this.endCombo(false);
        this.particles.text(br.cx, br.y - 12, br.failReason(b.vx, b.vy, speed), '#ff8f9c', 15, 0.7);
      }
      if (b.power !== 'fire' && b.power !== 'pierce') break; // one brick per step
    }
  },
});

// ---------------------------------------------------------------------------
// part 4: breaks, scoring, overdrive payout, powerups, flow
// ---------------------------------------------------------------------------

Object.assign(Game.prototype, {
  breakBrick(br, b, speed) {
    br.alive = false;
    br.breaking = 0.001;
    br.frags = br.breakInto();
    this.remaining--;

    // combo
    this.combo++;
    this.bestCombo = Math.max(this.bestCombo, this.combo);
    const cTag = this.combo >= 3 ? `COMBO ×${this.combo}` : null;
    if (this.combo >= 3) {
      this.dom.comboTag.textContent = cTag;
      this.dom.comboTag.classList.remove('hidden');
      this.dom.comboTag.style.transform = `translateX(-50%) scale(${1 + Math.min(0.5, this.combo * 0.03)})`;
      this._comboTagT = 1.1;
    }

    const pts = br.info.pts;
    const comboBoost = 1 + this.combo * 0.12;
    const gained = Math.round(pts * comboBoost * (this.odActive ? this.odMult : 1));
    this.addScore(gained);
    this.particles.text(br.cx, br.y - 6, '+' + gained, this.odActive ? '#ffd98a' : '#bfe6ff',
      17 + Math.min(14, this.combo * 1.3), 0.85);

    // juice scaled by combo
    audio.brick(this.combo);
    const st = R.brickStyle ? R.brickStyle(br) : R.TYPE_STYLE?.[br.type];
    this.particles.debris(br.x, br.y, br.w, br.h, st?.base || '#3e6cf0', 10 + Math.min(10, this.combo));
    const styleCol = this.brickColor(br.type, br.row);    this.particles.debris(br.x, br.y, br.w, br.h, styleCol, 10);
    this.particles.ring(br.cx, br.cy, 6, 34 + this.combo * 2, 0.32, styleCol, 3);
    this.shake(2 + Math.min(6, this.combo * 0.5) + (speed > 700 ? 1.5 : 0));
    if (this.combo >= COMBO.hitstopAt) {
      this.hitstop = Math.min(COMBO.hitstopMax, 0.012 * (this.combo - COMBO.hitstopAt + 2));
    }
    if (this.combo % 5 === 0) {
      this.flashScreen(0.25, '#ffffff');
      this.slowmo(0.12, 0.4);
    }

    // unstable chain
    if (br.type === 'U') this.chainExplode(br, 0);

    // spinner also spins neighbours? no — keep simple
    // drops
    const dropChance = br.type === '2' ? 1 : br.type === 'U' ? 0.35 : 0.085;
    if (Math.random() < dropChance) {
      this.powerups.push(new Powerup(br.cx, br.cy, rollDrop()));
    }

    // fireball burn-through splash damage
    if (b.power === 'fire') {
      this.splashDamage(br, 70);
    }

    // level clear?
    if (this.remaining <= 0) this.levelCleared();
  },

  brickColor(type, row = 0) {
    if (type === '1') {
      const st = R.rowStyle(row);
      return st.base;
    }
    const map = { '2': '#18b5a8', A: '#e0902c', S: '#33c94e', P: '#9a5cf0', X: '#e0485a', U: '#f0c33e', W: '#4fb8e8' };
    return map[type] || '#3e6cf0';
  },

  splashDamage(br, radius) {
    for (const other of this.bricks) {
      if (!other.alive || other === br) continue;
      const dx = other.cx - br.cx, dy = other.cy - br.cy;
      if (dx * dx + dy * dy < radius * radius) {
        this.breakBrick(other, { power: 'fire' }, 800);
      }
    }
  },

  chainExplode(br, depth) {
    if (depth > 4) return;
    audio.explode();
    this.shake(6);
    this.flashScreen(0.2, '#ffcf7a');
    this.particles.burst(br.cx, br.cy, '#ffe98a', 26, { speed: 330, grav: 260, glow: 10 });
    this.particles.ring(br.cx, br.cy, 8, 90, 0.45, '#ffd98a', 4);
    for (const other of this.bricks) {
      if (!other.alive) continue;
      const dx = other.cx - br.cx, dy = other.cy - br.cy;
      const d2 = dx * dx + dy * dy;
      if (d2 < 90 * 90) {
        other.alive = false;
        other.breaking = 0.001;
        other.frags = other.breakInto();
        this.remaining--;
        const gained = Math.round(other.info.pts * (1 + this.combo * 0.12) * (this.odActive ? this.odMult : 1));
        this.addScore(gained);
        this.particles.text(other.cx, other.y, '+' + gained, '#ffe98a', 15, 0.7);
        if (other.type === 'U' && d2 > 4) this.chainExplode(other, depth + 1);
      }
    }
    if (this.remaining <= 0) this.levelCleared();
  },

  addScore(n) {
    this.score += n;
    this.dom.score.classList.add('pop');
    clearTimeout(this._scorePopT);
    this._scorePopT = setTimeout(() => this.dom.score.classList.remove('pop'), 90);
  },

  endCombo(showText = true) {
    if (this.combo >= 5 && showText) {
      this.particles.text(this.paddle.x, this.paddle.y - 60, `COMBO ×${this.combo}!`, '#ffd98a', 22, 1.1);
      this.particles.burst(this.paddle.x, this.paddle.y - 40, '#ffd98a', 14, { speed: 220 });
    }
    this.combo = 0;
    this.dom.comboTag.classList.add('hidden');
    audio.setLayers(this.odActive ? 2 : 0);
  },

  // ---- overdrive -----------------------------------------------------------

  checkOverdriveEntry(b) {
    // topmost brick row y
    let topMost = Infinity;
    for (const br of this.bricks) {
      if (br.alive) { topMost = Math.min(topMost, br.y); break; }
    }
    if (topMost === Infinity) return; // no bricks left (level clear handles)
    if (b.y + b.r < topMost - OVERDRIVE.lead && b.vy < 0) {
      if (!this.odActive) {
        this.odActive = true;
        this.odTimer = 0;
        this.flashScreen(0.35, '#ffcf8a');
        this.particles.text(W / 2, H * 0.34, 'OVERDRIVE!', '#ffcf8a', 44, 1.4);
        this.shake(8);
        audio.tone(220, 0.7, { type: 'sawtooth', gain: 0.08, slide: 660 });
        audio.setLayers(2);
      }
      // while up there, ball must stay above; if it descends below rim → exit
    }
  },

  updateOverdriveExit(b) {
    if (!this.odActive) return;
    let topMost = Infinity;
    for (const br of this.bricks) {
      if (br.alive) { topMost = Math.min(topMost, br.y); break; }
    }
    const rim = topMost === Infinity ? BRICK_TOP_FALLBACK() : topMost - OVERDRIVE.lead;
    if (b.y - b.r > rim) {
      // ball came back down through the field
      this.payout();
      this.odActive = false;
    }
  },

  payout() {
    const mult = Math.max(1, this.odMult);
    const base = 400 * Math.min(60, Math.pow(mult, 1.85));
    const gained = Math.round(base);
    this.addScore(gained);
    this.flashScreen(0.6, '#ffe9a0');
    this.slowmo(0.7, 0.25);
    this.shake(12);
    audio.payout(mult);
    const px = this.paddle.x, py = H * 0.4;
    this.particles.text(W / 2, py, `PAYOUT ×${mult.toFixed(1)}`, '#ffe9a0', 42, 1.8);
    this.particles.text(W / 2, py + 56, `+${fmt(gained)}`, '#ffffff', 34, 1.8);
    for (let i = 0; i < 60; i++) {
      this.particles.spawn(rand(WALL, W - WALL), rand(CEIL, H * 0.6),
        rand(-120, 120), rand(-200, 40), rand(0.7, 1.6), rand(2.5, 6),
        pick(['#ffd98a', '#ffab5c', '#fff6e0']), { grav: 240, drag: 0.8, glow: 9, shape: 'dot' });
    }
    this.dom.odBanner.classList.add('hidden');
    this.dom.odFill.classList.remove('hot');
    audio.setHeat(0);
    audio.setLayers(this.combo >= 2 ? 1 : 0);
    this.heat = 0;
    // reward: charge meter → powerup ball if significant
    if (this.odCharge >= 55) {
      const id = this.odCharge >= 90 ? 'fire' : pick(['pierce', 'giant', 'multi', 'burst', 'magnet']);
      this.powerups.push(new Powerup(W / 2, this.paddle.y - 220, id));
      this.odCharge = 0;
      this.particles.text(W / 2, this.paddle.y - 260, 'REWARD!', '#8bff9e', 26, 1.2);
    }
  },
});
function BRICK_TOP_FALLBACK() { return 140; }

// settings toggle for touch control mode (drag | stick)


// ---------------------------------------------------------------------------
// part 5: powerups, lose ball, level flow, HUD, render
// ---------------------------------------------------------------------------

Object.assign(Game.prototype, {
  catchPowerup(id, x, y) {
    const info = PU[id] || { name: id, color: '#fff' };
    audio.powerup();
    this.particles.ring(x, y, 6, 44, 0.4, info.color, 3);
    this.particles.burst(x, y, info.color, 14, { speed: 240, glow: 8 });
    this.particles.text(x, y - 30, info.name, info.color, 20, 1.1);
    this.shake(3);

    switch (id) {
      case 'multi': {
        const src = this.balls[0] || this.spawnBall(true);
        for (let i = 0; i < 2; i++) {
          const nb = this.spawnBall(false);
          const a = Math.atan2(src.vy || -1, src.vx || 0) + (i === 0 ? 0.5 : -0.5);
          const sp = Math.max(BALL.base, Math.hypot(src.vx, src.vy) || BALL.base);
          nb.x = src.x; nb.y = src.y;
          nb.vx = Math.cos(a) * sp; nb.vy = Math.sin(a) * sp;
        }
        break;
      }
      case 'fire': this.applyBallPower('fire', 9); break;
      case 'pierce': this.applyBallPower('pierce', 9); break;
      case 'giant': this.applyBallPower('giant', 10); break;
      case 'slow': this.applyBallPower('slow', 7); break;
      case 'burst': {
        for (const b of this.balls) {
          const sp = Math.hypot(b.vx, b.vy) || BALL.base;
          const f = Math.min(BALL.max, sp * 1.45) / sp;
          b.vx *= f; b.vy *= f;
        }
        this.particles.text(W / 2, H * 0.5, 'SPEED BURST!', '#fbbf24', 26, 1);
        this.flashScreen(0.15, '#fbbf24');
        break;
      }
      case 'magnet': this.paddle.magnet = true; this._magnetT = 12; break;
      case 'wide': this.paddle.wideT = 12; break;
      case 'life': this.lives++; this.updateLives(); audio.life(); break;
    }
  },

  applyBallPower(kind, dur) {
    for (const b of this.balls) {
      b.power = kind;
      b.powerT = dur;
      if (kind === 'giant') b.r = b.baseR * 2.1;
      if (kind === 'pierce') b.r = b.baseR * 0.9;
    }
  },

  loseBall() {
    this.lives--;
    this.updateLives();
    this.endCombo(false);
    audio.loseBall();
    this.shake(9);
    this.flashScreen(0.25, '#ff5d6e');
    this.resetOverdrive();
    this.paddle.magnet = false;
    if (this.lives <= 0) {
      this.gameOver();
    } else {
      this.state = STATE.SERVE;
      this.stateT = 0;
      this.spawnBall(true);
      this._tsTarget = 1;
    }
  },

  gameOver() {
    this.state = STATE.GAMEOVER;
    this.stateT = 0;
    audio.gameOver();
    if (this.score > this.best) {
      this.best = this.score;
      localStorage.setItem('breaktop.best', String(this.best));
      this._newBest = true;
    } else this._newBest = false;
    this.dom.best.textContent = 'BEST ' + fmt(this.best);
  },

  levelCleared() {
    this.state = STATE.CLEAR;
    this.stateT = 0;
    this.slowmo(1.0, 0.22);
    this.flashScreen(0.5, '#ffffff');
    this.shake(10);
    audio.levelClear();
    const bonus = 1000 * (this.levelIdx + 1) + Math.round(this.odCharge * 10);
    this.addScore(bonus);
    this.particles.text(W / 2, H * 0.4, 'LEVEL CLEAR!', '#ffffff', 40, 2);
    this.particles.text(W / 2, H * 0.4 + 52, `BONUS +${fmt(bonus)}`, '#bfe6ff', 24, 2);
    for (let i = 0; i < 70; i++) {
      this.particles.spawn(rand(WALL, W - WALL), rand(H * 0.2, H * 0.7),
        rand(-160, 160), rand(-260, -40), rand(0.8, 1.8), rand(2.5, 6),
        pick(['#7dd6ff', '#bfe6ff', '#ffd98a', '#8bff9e']), { grav: 300, drag: 0.7, glow: 8, shape: 'dot' });
    }
  },

  nextLevel() {
    this.loadLevel(this.levelIdx + 1);
  },

  updateLives() {
    // ONE unified design on all platforms: "LIVES" label + 3-slot dot rail.
    const total = Math.max(3, this.lives);
    let s = '<span class="lives-label">LIVES</span>';
    for (let i = 0; i < total; i++) {
      s += i < this.lives ? '<span class="dot"></span>' : '<span class="dot lost"></span>';
    }
    this.dom.lives.innerHTML = s;
  },

  shake(amp) {
    this.shakeAmp = Math.max(this.shakeAmp, amp);
    this.shakeT = Math.max(this.shakeT, 0.22);
  },

  flashScreen(a, color) {
    this.flash = Math.max(this.flash, a);
    this.flashColor = color;
  },

  // ---- HUD -----------------------------------------------------------------

  updateHud(rdt) {
    this.dom.score.textContent = fmt(Math.floor(this.scoreShown));
    const fillPct = this.odActive ? clamp(this.odMult / 12 * 100, 4, 100) : this.odCharge;
    this.dom.odFill.style.width = fillPct + '%';
    // labelled meter state
    const wrap = document.querySelector('.od-meter-wrap');
    if (wrap) {
      wrap.classList.toggle('hot', this.heat > 0.4);
      const label = this.odActive ? 'OVERDRIVE' : 'CHARGE';
      const lab = document.querySelector('.od-meter-head .od-label');
      if (lab && lab.textContent !== label) lab.textContent = label;
      const val = document.getElementById('od-val');
      if (val) val.textContent = this.odActive ? '×' + this.odMult.toFixed(1) : Math.round(fillPct) + '%';
    }
    // desktop side panels (mirror key stats)
    if (this.domWide) {
      const set = (id, txt) => { const el = document.getElementById(id); if (el && el.textContent !== txt) el.textContent = txt; };
      set('p-score', fmt(Math.floor(this.scoreShown)));
      set('p-best', fmt(this.best));
      set('p-lives', String(Math.max(0, this.lives)));
      set('p-level', String(this.levelIdx + 1));
      set('p-combo', '×' + this.bestCombo);
      set('p-mult', '×' + Math.max(1, this.odActive ? this.odMult : 1).toFixed(1));
      const ch = document.getElementById('p-charge');
      if (ch) ch.style.width = fillPct + '%';
    }
    if (this._comboTagT > 0) {
      this._comboTagT -= rdt;
      if (this._comboTagT <= 0) this.dom.comboTag.classList.add('hidden');
    }
    // magnet timer
    if (this.paddle.magnet) {
      this._magnetT -= rdt;
      if (this._magnetT <= 0) this.paddle.magnet = false;
    }
  },
});

// ---------------------------------------------------------------------------
// part 6: render
// ---------------------------------------------------------------------------

Object.assign(Game.prototype, {
  render() {
    const ctx = this.ctx;
    const { dpr, viewW, viewH, offX, offY, scale } = this;
    ctx.setTransform(1, 0, 0, 1, 0, 0);
    // clear entire canvas with the themed deep-space base (never pure black)
    ctx.fillStyle = '#04060d';
    ctx.fillRect(0, 0, this.canvas.width, this.canvas.height);

    // === full-screen ambient backdrop (extends past the letterbox) ==========
    this.drawAmbient(ctx, dpr, viewW, viewH, offX, offY, scale);
    // Desktop (field narrower than the window): scenery stays UNDER the field,
    // exactly as approved. Full-bleed viewports repaint it OVER the field below.
    if (this.gutterW >= 1) this.drawAmbientScenery(ctx, dpr, viewW, viewH, offX, offY, scale);

    // world transform
    ctx.save();
    ctx.translate(offX * dpr, offY * dpr);
    ctx.scale(scale * dpr, scale * dpr);

    // screen shake
    if (this.shakeT > 0) {
      const a = this.shakeAmp * this.shakeT * (1 + this.heat);
      ctx.translate(rand(-a, a), rand(-a, a));
    }

    // background (extended across the full canvas so mobile letterbox bands
    // and desktop gutters share one continuous, themed gradient)
    this.drawBackground(ctx);
    this.stars.draw(ctx, this.heat, this.gutterW, this.gutterV || 0);

    // gutter dressing (inside world transform, so it scales with the field)
    if (this.gutterW > 1) this.drawGutterDressing(ctx);

    // full-bleed viewport (field spans the window): repaint the ambient
    // scenery OVER the field too, so the letterbox bands and the playfield
    // share one continuous shade — no seam where the field's top edge meets
    // the backdrop (previously the field's own fill erased the glows there).
    if (this.gutterW < 1) this.drawAmbientScenery(ctx, dpr, viewW, viewH, offX, offY, scale);

    // fullBleed flag: on narrow screens the frame walls melt into the
    // backdrop instead of drawing dark gutters at the screen edges
    R.drawFrame(ctx, this.heat, this.time, this.gutterW < 1);

    // bricks — hidden on the title screen: the title composition is deliberately
    // clean (logo + starfield + ambient glow), no demo field behind the panel.
    if (this.state !== STATE.TITLE) {
      for (const br of this.bricks) R.drawBrick(ctx, br, this.levelTime, this.heat);
    }

    // serve hint
    if (this.state === STATE.SERVE) this.drawServeHint(ctx);
    if (this.state === STATE.INTRO) this.drawIntro(ctx);

    // powerups
    for (const pu of this.powerups) this.drawPowerupWrapped(ctx, pu);

    // paddle
    R.drawPaddle(ctx, this.paddle, this.heat, this.time);

    // balls — the demo ball is title ambience: hide it for the clean title look
    if (this.state !== STATE.TITLE) {
      for (const b of this.balls) R.drawBall(ctx, b, this.heat, this.time);
    }

    // particles
    this.particles.draw(ctx);

    // stick visual
    this.drawStick(ctx);

    // overlays
    if (this.state === STATE.TITLE) this.drawTitle(ctx);
    if (this.state === STATE.GAMEOVER) this.drawGameOver(ctx);
    if (this.state === STATE.PAUSE) this.drawPause(ctx);
    if (this.state === STATE.CLEAR) this.drawLevelClear(ctx);

    // vignette inside the field (adds depth, keeps edges quiet).
    // Full-bleed viewports: extend it across the entire screen and soften,
    // so its edge can't print a seam at the letterbox boundary.
    ctx.save();
    const gx = this.gutterW || 0, gy = this.gutterV || 0;
    const fullBleed = gx < 1;
    const vg = ctx.createRadialGradient(W / 2, H / 2, H * 0.34, W / 2, H / 2, H * 0.78);
    vg.addColorStop(0, 'rgba(0,0,0,0)');
    vg.addColorStop(1, `rgba(2,4,10,${(0.42 - this.heat * 0.2) * (fullBleed ? 0.45 : 1)})`);
    ctx.fillStyle = vg;
    if (fullBleed) {
      const top = -this.offY / this.scale, bot = top + this.viewH / this.scale;
      ctx.fillRect(-2, top, W + 4, bot - top);
    } else {
      ctx.fillRect(-gx, -gy, W + gx * 2, H + gy * 2);
    }
    ctx.restore();

    ctx.restore();

    // full-screen flash (unshaken)
    if (this.flash > 0) {
      ctx.save();
      ctx.setTransform(1, 0, 0, 1, 0, 0);
      ctx.globalAlpha = Math.min(0.85, this.flash);
      ctx.fillStyle = this.flashColor;
      ctx.fillRect(0, 0, this.canvas.width, this.canvas.height);
      ctx.restore();
    }
    // overdrive heat vignette (clipped to the field so gutters stay clean)
    if (this.heat > 0.02) {
      ctx.save();
      const gx = this.gutterW || 0, gy = this.gutterV || 0;
      ctx.setTransform(dpr * scale, 0, 0, dpr * scale, offX * dpr, offY * dpr);
      const g = ctx.createRadialGradient(W / 2, H / 2, H * 0.2, W / 2, H / 2, H * 0.72);
      g.addColorStop(0, 'rgba(0,0,0,0)');
      g.addColorStop(1, `rgba(255,${(120 - this.heat * 90) | 0},30,${this.heat * 0.28})`);
      ctx.fillStyle = g;
      ctx.fillRect(-gx, -gy, W + gx * 2, H + gy * 2);
      ctx.restore();
    }
    this.shakeAmp *= 0.86;
  },

  /** full-canvas ambient: soft nebula glows + sparse outer stars so the
      letterbox gutters read as designed scenery, not dead black.
      Split into TWO passes: the vertical base gradient (always, everywhere —
      it IS the playfield's own shade) and the scenery glows/dust, which on
      full-bleed viewports is painted over the field as well (seamless). */
  drawAmbient(ctx, dpr, viewW, viewH, offX, offY, scale) {
    ctx.save();
    ctx.setTransform(1, 0, 0, 1, 0, 0);
    // ONE vertical gradient matched to the field's own (drawBackground), so the
    // top/bottom letterbox bands blend seamlessly with the playfield.
    const bg = ctx.createLinearGradient(0, 0, 0, this.canvas.height);
    bg.addColorStop(0, '#070b18');
    bg.addColorStop(0.45, '#0d1430');
    bg.addColorStop(1, '#0a0f22');
    ctx.fillStyle = bg;
    ctx.fillRect(0, 0, this.canvas.width, this.canvas.height);
    ctx.restore();
  },

  /** nebula glows + drifting dust, shared scenery across the whole surface.
      Called under the field (desktop gutters) and over it (full-bleed). */
  drawAmbientScenery(ctx, dpr, viewW, viewH, offX, offY, scale) {
    ctx.save();
    ctx.setTransform(1, 0, 0, 1, 0, 0);
    const cx = (offX + W / 2 * scale) * dpr, cy = (offY + H / 2 * scale) * dpr;
    const r0 = Math.min(this.canvas.width, this.canvas.height);
    // deep blue halo above, violet below — matches the field gradient
    let g = ctx.createRadialGradient(cx, cy - r0 * 0.55, 0, cx, cy - r0 * 0.55, r0 * 1.15);
    g.addColorStop(0, 'rgba(18,34,74,0.85)');
    g.addColorStop(1, 'rgba(18,34,74,0)');
    ctx.fillStyle = g;
    ctx.fillRect(0, 0, this.canvas.width, this.canvas.height);
    g = ctx.createRadialGradient(cx, cy + r0 * 0.8, 0, cx, cy + r0 * 0.8, r0 * 1.2);
    g.addColorStop(0, 'rgba(26,15,51,0.75)');
    g.addColorStop(1, 'rgba(26,15,51,0)');
    ctx.fillStyle = g;
    ctx.fillRect(0, 0, this.canvas.width, this.canvas.height);
    // sparse drifting dust across the whole surface (shared scenery)
    this._dustT = (this._dustT || 0) + 0.008;
    const rnd = this._dust || (this._dust = this._makeDust());
    const dim = this.gutterW < 1 ? 0.4 : 1;   // quieter over the live field
    ctx.globalAlpha = 0.4 * dim;
    for (const d of rnd) {
      const px = d.x * this.canvas.width;
      const py = (d.y + this._dustT * d.sp) % 1.2 - 0.1;
      ctx.fillStyle = d.c;
      ctx.beginPath();
      ctx.arc(px, py * this.canvas.height, d.r * dpr, 0, TAU);
      ctx.fill();
    }
    ctx.globalAlpha = 1;
    ctx.restore();
  },

  _makeDust() {
    const cols = ['#7dd6ff', '#9db8e8', '#bfa8ff', '#8fb4ff'];
    const out = [];
    let seed = 9871;
    const r = () => { seed = (seed * 16807) % 2147483647; return (seed % 10000) / 10000; };
    for (let i = 0; i < 90; i++) {
      out.push({ x: r(), y: r(), r: 0.6 + r() * 1.6, sp: 0.02 + r() * 0.06, c: cols[i % cols.length] });
    }
    return out;
  },

  /** themed dressing on the letterbox gutters, drawn in world coords.
      The heat tint comes from drawBackground (shared gradient), so the gutters
      warm up coherently — this only adds a soft vignette-like falloff that is
      symmetric on both sides (deliberate gradient, no hard band edges). */
  drawGutterDressing(ctx) {
    const heat = this.heat;
    const gw = this.gutterW;
    const col = heat > 0.05 ? '255,170,90' : '125,214,255';
    ctx.save();
    // symmetric warm falloff from each field edge (same shape both sides)
    for (const side of [-1, 1]) {
      const g = ctx.createLinearGradient(side < 0 ? 0 : W, 0, side < 0 ? -gw : W + gw, 0);
      g.addColorStop(0, `rgba(${col},${0.13 + heat * 0.18})`);
      g.addColorStop(0.45, `rgba(${col},0.04)`);
      g.addColorStop(1, `rgba(${col},0)`);
      ctx.fillStyle = g;
      if (side < 0) ctx.fillRect(-gw, 0, gw, H);
      else ctx.fillRect(W, 0, gw, H);
    }
    ctx.restore();
  },

  drawBackground(ctx) {
    // ONE continuous vertical gradient across the whole painted surface
    // (field + gutters + vertical letterbox), so mobile edge-to-edge fill and
    // desktop wall-to-wall shading share a single seamless field. Heat tints
    // the ENTIRE surface coherently — no separately-tinted zones.
    const heat = this.heat;
    const pulse = heat > 0.02 ? 0.5 + 0.5 * Math.sin(this.time * (3 + heat * 9)) : 0;
    const gy = this.gutterV || 0;
    const y0 = -gy - 2, y1 = H + gy + 2;
    // Full-bleed viewport: paint the gradient edge-to-edge in SCREEN space
    // (device pixels), so the playfield and the top/bottom letterbox bands
    // are literally the same fill — zero shade seam. With gutters (desktop),
    // keep the approved field-spanning gradient.
    const fullBleed = (this.gutterW || 0) < 1;
    const l0 = 4.5 + pulse * 2;
    const g = ctx.createLinearGradient(0, y0, 0, y1);
    g.addColorStop(0, heat > 0.02 ? `hsl(${28 - heat * 8} 55% ${l0 + heat * 6}%)` : '#070b18');
    g.addColorStop(0.45, heat > 0.02 ? `hsl(${22 - heat * 6} 48% ${l0 + 3}%)` : '#0d1430');
    g.addColorStop(1, heat > 0.02 ? `hsl(${14 - heat * 4} 42% ${l0 + 1}%)` : '#0a0f22');
    if (fullBleed) {
      const top = -this.offY / this.scale, bot = top + this.viewH / this.scale;
      const sg = ctx.createLinearGradient(0, top, 0, bot);
      sg.addColorStop(0, heat > 0.02 ? `hsl(${28 - heat * 8} 55% ${l0 + heat * 6}%)` : '#070b18');
      sg.addColorStop(0.45, heat > 0.02 ? `hsl(${22 - heat * 6} 48% ${l0 + 3}%)` : '#0d1430');
      sg.addColorStop(1, heat > 0.02 ? `hsl(${14 - heat * 4} 42% ${l0 + 1}%)` : '#0a0f22');
      ctx.fillStyle = sg;
      ctx.fillRect(-2, top, W + 4, bot - top);
    } else {
      ctx.fillStyle = g;
      ctx.fillRect(-(this.gutterW || 0) - 2, y0, W + (this.gutterW || 0) * 2 + 4, y1 - y0);
    }
  },

  drawServeHint(ctx) {
    const p = 0.6 + 0.4 * Math.sin(this.stateT * 4);
    const def = LEVELS[this.levelIdx % LEVELS.length];
    ctx.save();
    ctx.textAlign = 'center';
    // level intro card
    const a = clamp(this.stateT * 3, 0, 1) * clamp((2.6 - this.stateT) * 1.6, 0, 1);
    if (a > 0.01) {
      ctx.globalAlpha = a;
      ctx.fillStyle = '#ffffff';
      ctx.font = '900 44px ui-rounded, system-ui, sans-serif';
      ctx.fillText(def.name, W / 2, H * 0.38);
      ctx.font = '600 19px ui-rounded, system-ui, sans-serif';
      ctx.fillStyle = '#9db8e8';
      ctx.fillText(def.hint, W / 2, H * 0.38 + 40);
    }
    ctx.globalAlpha = p;
    // bold, glowing launch cue — sized up on touch devices (small screens)
    // so it reads clearly on a phone; soft halo keeps it off the dark field.
    ctx.fillStyle = '#bfe6ff';
    ctx.shadowColor = 'rgba(125,214,255,0.75)';
    ctx.shadowBlur = 16;
    const big = this._touch || this.viewW < 900;
    ctx.font = `800 ${big ? 30 : 22}px ui-rounded, system-ui, sans-serif`;
    const hint = this._touch ? 'TAP TO LAUNCH' : 'TAP / SPACE TO LAUNCH';
    ctx.fillText(hint, W / 2, this.paddle.y - 96);
    if (big) {
      ctx.font = '600 15px ui-rounded, system-ui, sans-serif';
      ctx.shadowBlur = 0;
      ctx.fillStyle = '#8aa4d4';
      ctx.fillText('DRAG TO MOVE', W / 2, this.paddle.y - 64);
    }
    ctx.restore();
  },

  drawIntro(ctx) {
    ctx.save();
    ctx.globalAlpha = clamp(this.stateT * 2, 0, 1) * clamp((2.2 - this.stateT) * 2, 0, 1);
    ctx.fillStyle = '#ffffff';
    ctx.font = '900 54px ui-rounded, system-ui, sans-serif';
    ctx.textAlign = 'center';
    ctx.fillText(LEVELS[this.levelIdx % LEVELS.length].name, W / 2, H * 0.42);
    ctx.font = '600 20px ui-rounded, system-ui, sans-serif';
    ctx.fillStyle = '#9db8e8';
    ctx.fillText(LEVELS[this.levelIdx % LEVELS.length].hint, W / 2, H * 0.42 + 44);
    ctx.restore();
  },

  drawLevelClear(ctx) {
    ctx.save();
    ctx.globalAlpha = clamp((this.stateT - 0.9) * 2, 0, 1);
    ctx.fillStyle = '#ffffff';
    ctx.font = '900 58px ui-rounded, system-ui, sans-serif';
    ctx.textAlign = 'center';
    ctx.fillText('LEVEL CLEAR', W / 2, H * 0.42);
    ctx.restore();
    if (this.stateT > 2.2) this.nextLevel();
  },

  drawTitle(ctx) {
    const t = this.stateT;
    const gx = this.gutterW || 0, gy = this.gutterV || 0;
    ctx.save();
    ctx.textAlign = 'center';

    // --- composed title staging --------------------------------------------
    // clean title = logo + starfield + ambient glow only: the demo bricks and
    // demo ball are fully hidden (bricks are skipped in render() while TITLE).
    const dimIn = clamp(t * 2.2, 0, 1);
    ctx.fillStyle = `rgba(4,7,16,${0.6 * dimIn})`;
    ctx.fillRect(-gx, -gy, W + gx * 2, H + gy * 2);

    // 2) glowing title panel (clear, framed composition)
    const pcx = W / 2, pcy = H * 0.27;
    const pw = Math.min(W * 0.86, 720), ph = 268;
    const pop = 1 + 0.012 * Math.sin(t * 1.6);
    ctx.save();
    ctx.globalAlpha = dimIn;
    ctx.translate(pcx, pcy);
    ctx.scale(pop, pop);

    // panel glow (alpha-faded, not color-faded — avoids the alpha=0 ring)
    const pg = ctx.createRadialGradient(0, 0, ph * 0.2, 0, 0, pw * 0.75);
    pg.addColorStop(0, 'rgba(47,111,216,0.32)');
    pg.addColorStop(0.55, 'rgba(47,111,216,0.10)');
    pg.addColorStop(1, 'rgba(47,111,216,0.0001)');
    ctx.fillStyle = pg;
    ctx.fillRect(-pw, -ph, pw * 2, ph * 2);

    // panel body (glassy)
    const body = ctx.createLinearGradient(0, -ph / 2, 0, ph / 2);
    body.addColorStop(0, 'rgba(16,26,56,0.72)');
    body.addColorStop(1, 'rgba(7,11,24,0.55)');
    ctx.fillStyle = body;
    R.roundRect(ctx, -pw / 2, -ph / 2, pw, ph, 22);
    ctx.fill();
    // panel border
    const bcol = 0.5 + 0.3 * Math.sin(t * 1.4);
    ctx.strokeStyle = `rgba(125,214,255,${0.34 + bcol * 0.2})`;
    ctx.lineWidth = 2;
    ctx.shadowColor = 'rgba(125,214,255,0.55)';
    ctx.shadowBlur = 22;
    R.roundRect(ctx, -pw / 2, -ph / 2, pw, ph, 22);
    ctx.stroke();
    ctx.shadowBlur = 0;
    // top edge highlight
    ctx.strokeStyle = 'rgba(255,255,255,0.22)';
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.moveTo(-pw / 2 + 26, -ph / 2 + 1.5);
    ctx.lineTo(pw / 2 - 26, -ph / 2 + 1.5);
    ctx.stroke();

    // logo — auto-fit so it never overflows the panel on narrow fields
    const grad = ctx.createLinearGradient(0, -74, 0, 30);
    grad.addColorStop(0, '#eaf6ff');
    grad.addColorStop(0.5, '#7dd6ff');
    grad.addColorStop(1, '#2f6fd8');
    ctx.fillStyle = grad;
    const fpx = Math.min(96, (pw * 0.86) / 5.4);
    ctx.font = `900 ${fpx}px ui-rounded, system-ui, sans-serif`;
    ctx.shadowColor = 'rgba(125,214,255,0.6)';
    ctx.shadowBlur = 30;
    ctx.fillText('BREAKTOP', 0, 26);
    ctx.shadowBlur = 0;
    // tagline — sits below the wordmark's baseline glow (logo baseline 26,
    // 30px blur halo), so it never collides with the glow pool around it
    const tpx = pw > 560 ? 19 : 14;
    ctx.font = `600 ${tpx}px ui-rounded, system-ui, sans-serif`;
    ctx.fillStyle = '#9db8e8';
    ctx.fillText('BREAK THROUGH · GET ON TOP · GO OVERDRIVE', 0, 86);
    ctx.restore();

    // 3) horizon line under the composition (quiet design anchor)
    ctx.save();
    ctx.globalAlpha = 0.55 * dimIn;
    const hy = H * 0.465;
    const hg = ctx.createLinearGradient(0, 0, W, 0);
    hg.addColorStop(0, 'rgba(125,214,255,0)');
    hg.addColorStop(0.5, 'rgba(125,214,255,0.85)');
    hg.addColorStop(1, 'rgba(125,214,255,0)');
    ctx.fillStyle = hg;
    ctx.fillRect(0, hy, W, 2);
    const beacon = 0.5 + 0.5 * Math.sin(t * 2.1);
    ctx.fillStyle = `rgba(190,235,255,${0.55 + beacon * 0.4})`;
    ctx.beginPath();
    ctx.arc(W / 2, hy + 1, 3.4 + beacon * 1.2, 0, TAU);
    ctx.fill();
    ctx.restore();

    // 4) start prompt — platform-appropriate copy
    const p = 0.55 + 0.45 * Math.sin(t * 3.4);
    ctx.globalAlpha = p * dimIn;
    ctx.font = '800 26px ui-rounded, system-ui, sans-serif';
    ctx.fillStyle = '#ffffff';
    ctx.fillText('TAP TO START', W / 2, H * 0.56);
    ctx.globalAlpha = 0.75 * dimIn;
    ctx.font = '600 15px ui-rounded, system-ui, sans-serif';
    ctx.fillStyle = '#8aa4d4';
    if (this._touch) {
      ctx.fillText('DRAG OR STICK TO MOVE · TAP TO LAUNCH', W / 2, H * 0.56 + 34);
      ctx.fillText('♪ SOUND', W / 2, H * 0.56 + 58);
    } else {
      ctx.fillText('MOUSE OR WASD TO MOVE · CLICK TO LAUNCH', W / 2, H * 0.56 + 34);
      ctx.fillText('P PAUSE · ♪ SOUND', W / 2, H * 0.56 + 58);
    }
    ctx.restore();
  },

  drawGameOver(ctx) {
    const gx = this.gutterW || 0, gy = this.gutterV || 0;
    ctx.save();
    ctx.globalAlpha = clamp(this.stateT * 2, 0, 1);
    ctx.fillStyle = 'rgba(4,6,14,0.72)';
    ctx.fillRect(-gx, -gy, W + gx * 2, H + gy * 2);
    ctx.textAlign = 'center';
    ctx.font = '900 64px ui-rounded, system-ui, sans-serif';
    ctx.fillStyle = '#ff8f9c';
    ctx.shadowColor = 'rgba(255,90,110,0.5)'; ctx.shadowBlur = 24;
    ctx.fillText('GAME OVER', W / 2, H * 0.36);
    ctx.shadowBlur = 0;
    ctx.font = '800 30px ui-rounded, system-ui, sans-serif';
    ctx.fillStyle = '#ffffff';
    ctx.fillText(fmt(Math.floor(this.score)), W / 2, H * 0.36 + 64);
    ctx.font = '600 18px ui-rounded, system-ui, sans-serif';
    ctx.fillStyle = this._newBest ? '#ffd98a' : '#8aa4d4';
    ctx.fillText(this._newBest ? '★ NEW BEST!' : 'BEST ' + fmt(this.best), W / 2, H * 0.36 + 96);
    const p = 0.55 + 0.45 * Math.sin(this.stateT * 3.4);
    ctx.globalAlpha = p;
    ctx.font = '800 24px ui-rounded, system-ui, sans-serif';
    ctx.fillStyle = '#ffffff';
    ctx.fillText('TAP TO PLAY AGAIN', W / 2, H * 0.62);
    ctx.restore();
  },

  drawPause(ctx) {
    const gx = this.gutterW || 0, gy = this.gutterV || 0;
    ctx.save();
    ctx.fillStyle = 'rgba(4,6,14,0.6)';
    ctx.fillRect(-gx, -gy, W + gx * 2, H + gy * 2);
    ctx.textAlign = 'center';
    ctx.font = '900 52px ui-rounded, system-ui, sans-serif';
    ctx.fillStyle = '#ffffff';
    ctx.fillText('PAUSED', W / 2, H * 0.44);
    ctx.font = '600 17px ui-rounded, system-ui, sans-serif';
    ctx.fillStyle = '#8aa4d4';
    ctx.fillText('P / ESC / ▶ TO RESUME', W / 2, H * 0.44 + 40);
    ctx.restore();
  },

  drawPowerupWrapped(ctx, pu) {
    R.drawPowerup(ctx, pu, this.time);
  },

  drawStick(ctx) {
    const sv = this.input.stickVisual();
    if (!sv) return;
    ctx.save();
    // convert css px to logical
    const ox = this.offX, oy = this.offY, sc = this.scale;
    const sx = (sv.sx - ox) / sc, sy = (sv.sy - oy) / sc;
    const kx = (sv.kx - ox) / sc, ky = (sv.ky - oy) / sc;
    ctx.globalAlpha = 0.5;
    ctx.strokeStyle = '#7dd6ff';
    ctx.lineWidth = 2.5;
    const R = 110 / sc;
    ctx.beginPath(); ctx.arc(sx, sy, R, 0, TAU); ctx.stroke();
    ctx.globalAlpha = 0.15;
    ctx.fillStyle = '#7dd6ff';
    ctx.beginPath(); ctx.arc(sx, sy, R, 0, TAU); ctx.fill();
    ctx.globalAlpha = 0.9;
    ctx.fillStyle = '#bfe6ff';
    ctx.beginPath(); ctx.arc(kx, ky, Math.max(10, 34 / sc), 0, TAU); ctx.fill();
    ctx.restore();
  },
});

// ---------------------------------------------------------------------------
// part 7: main.js wiring (entry point appended for single-module simplicity)
// ---------------------------------------------------------------------------

// overdrive entry/exit checks happen inside stepBall via these wrappers.
// Patch stepBall to run overdrive checks after movement.
const _origStepBall = Game.prototype.stepBall;
Game.prototype.stepBall = function (b) {
  const lost = _origStepBall.call(this, b);
  if (!lost && !b.stuck) {
    this.checkOverdriveEntry(b);
    this.updateOverdriveExit(b);
  }
  return lost;
};

// auto-release magnet ball after a moment
const _updatePaddleOrig = Game.prototype.updatePaddle;
Game.prototype.updatePaddle = function (sdt, rdt) {
  _updatePaddleOrig.call(this, sdt, rdt);
  for (const b of this.balls) {
    if (b.stuck && b._releaseT !== undefined && this.state === STATE.PLAY) {
      b._releaseT -= rdt;
      if (b._releaseT <= 0) {
        b.stuck = false;
        b._releaseT = undefined;
        const a = -Math.PI / 2 + clamp((b.x - this.paddle.x) / 200, -0.7, 0.7);
        const sp = Math.max(BALL.base, 520);
        b.vx = Math.cos(a) * sp; b.vy = Math.sin(a) * sp;
        audio.launch();
      }
    }
  }
};

export function boot() {
  const canvas = document.getElementById('game');
  const game = new Game(canvas);
  // expose for debugging / testing
  window.__game = game;
  window.__STATE = STATE;
  return game;
}

boot();
