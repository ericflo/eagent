// game.js — the game layer: owns the engine, renderer, particle system, audio,
// input and HUD, pumps the loop, translates engine events into FX/sound, and
// drives the UI screens (menu, HUD, game over, level complete).

import { Engine } from './engine.js';
import {
  WORLD_W, WORLD_H, BRICK_TOP, PADDLE_W, PADDLE_H,
  BALL_R, MAX_BALL_SPEED, MAX_FIRE_SPEED, PADDLE_MIN_X, PADDLE_MAX_X,
} from './engine.js';
import { ParticleSystem } from './particles.js';
import { AudioEngine } from './audio.js';
import { Input } from './input.js';
import { Hud } from './hud.js';
import { POWERS } from './engine.js';
import { LEVELS, LEGEND, COLS } from './levels.js';

const POWS = POWERS;

export class Game {
  constructor(canvas, ui, opts = {}) {
    this.canvas = canvas;
    this.ui = ui;                  // DOM helper (screens)
    this.renderer = opts.renderer;
    this.particles = new ParticleSystem();
    this.audio = new AudioEngine();
    this.input = new Input(canvas, document.querySelector('#stick'));
    this.hud = new Hud(document);

    this.engine = new Engine(opts.engineOpts || {});
    this.engine.loadLevel(0);

    this.state = 'menu';           // menu | playing | paused | levelClear | gameover
    this.overlayState = null;      // 'levelClear' | 'gameover' | null
    this.paused = false;

    this.acc = 0;
    this.lastT = performance.now();
    this.time = 0;
    this.flashWhite = 0;
    this.flashColor = null;
    this.comboPop = 0;
    this.roofPop = 0;
    this.bgXt = 0;
    this.msgQueue = [];

    this.renderer && this.renderer.resize();

    this._wire();
    this.loop = this.loop.bind(this);
    requestAnimationFrame(this.loop);
  }

  _wire() {
    // input launch
    this.input.onLaunch = (x, y) => {
      this.audio.resume();
      if (this.state === 'playing' && this.engine.state === 'serving') {
        this.engine.launch();
      } else if (this.state === 'menu') {
        this._start();
      } else if (this.overlayState === 'levelClear' && this.ui) {
        this._nextLevel();
      } else if (this.overlayState === 'gameover') {
        this._restart();
      }
      this.audio.uiClick && y > 0 && this.audio.resume();
    };

    // UI buttons
    if (this.ui) {
      this.ui.on('play', () => this._start());
      this.ui.on('endless', () => this._startEndless());
      this.ui.on('mute', () => {
        this.audio.toggleMute();
        this.ui.setMuteIcon(this.audio.muted);
      });
      this.ui.on('pause', () => this.togglePause());
      this.ui.on('resume', () => this.togglePause());
      this.ui.on('restart', () => this._restart());
      this.ui.on('next', () => this._nextLevel());
      this.ui.on('menu', () => this._toMenu());
    }

    // keyboard
    document.addEventListener('keydown', (e) => {
      if (e.code === 'Space') {
        e.preventDefault();
        this.audio.resume();
        if (this.state === 'playing' && this.engine.state === 'serving') this.engine.launch();
        else if (this.state === 'menu') this._start();
        else if (this.overlayState === 'levelClear') this._nextLevel();
        else if (this.overlayState === 'gameover') this._restart();
      } else if (e.code === 'KeyP' || e.code === 'Escape') {
        if (this.state === 'playing' || this.paused) this.togglePause();
      } else if (e.code === 'ArrowLeft') {
        this._keyX = -1;
      } else if (e.code === 'ArrowRight') {
        this._keyX = 1;
      } else if (e.code === 'ArrowUp') {
        this._keyY = -1;
      } else if (e.code === 'ArrowDown') {
        this._keyY = 1;
      }
    });
    document.addEventListener('keyup', (e) => {
      if (e.code === 'ArrowLeft' && this._keyX === -1) this._keyX = 0;
      if (e.code === 'ArrowRight' && this._keyX === 1) this._keyX = 0;
      if (e.code === 'ArrowUp' && this._keyY === -1) this._keyY = 0;
      if (e.code === 'ArrowDown' && this._keyY === 1) this._keyY = 0;
    });
  }

  _start() {
    if (!this.ui) return;
    this.audio.init();
    this.audio.resume();
    this.state = 'playing';
    this.paused = false;
    this.overlayState = null;
    this.ui.show('game');
    this.engine.loadLevel(0);
    this.engine.resetRound();
    this.audio.musicStart();
    this.audio.setMusicIntensity(0);
  }

  _startEndless() {
    if (!this.ui) return;
    this.audio.init();
    this.audio.resume();
    this.state = 'playing';
    this.paused = false;
    this.overlayState = null;
    this.ui.show('game');
    this.engine.loadEndless(1);
    this.audio.musicStart();
    this.audio.setMusicIntensity(0);
  }

  _nextLevel() {
    const idx = this.engine.levelIndex + 1;
    if (idx < LEVELS.length) {
      this.engine.loadLevel(idx);
      this.engine.resetRound();
      this.overlayState = null;
      this.ui.show('game');
      this.audio.musicStart();
    } else {
      // campaign finished -> endless continues at level = campaign count + 1
      this.engine.loadEndless(LEVELS.length + 1);
      this.engine.resetRound();
      this.overlayState = null;
      this.ui.show('game');
    }
    this.state = 'playing';
    this.paused = false;
  }

  _restart() {
    if (this.engine.mode === 'endless') {
      this.engine.loadEndless(this.engine.levelNumber || 1);
    } else {
      this.engine.loadLevel(this.engine.levelIndex);
    }
    this.engine.resetRound();
    this.overlayState = null;
    this.state = 'playing';
    this.paused = false;
    this.ui.show('game');
    this.audio.musicStart();
  }

  _toMenu() {
    this.state = 'menu';
    this.overlayState = null;
    this.paused = false;
    this.audio.musicStop();
    this.ui.show('menu');
  }

  togglePause() {
    if (this.state !== 'playing') return;
    this.paused = !this.paused;
    this.audio.resume();
    if (this.ui) this.ui.setPaused(this.paused);
  }

  // ==========================================================================
  loop(t) {
    requestAnimationFrame(this.loop);
    const dt = Math.min((t - this.lastT) / 1000, 0.05);
    this.lastT = t;
    this.time += dt;
    this.bgXt += dt * 8;

    if (this.state === 'playing' && !this.paused) {
      this._update(dt);
    }
    this._render();
  }

  _update(dt) {
    const eng = this.engine;

    // input -> engine aim
    const target = this.input.poll(dt);
    let ax = target.x, ay = target.y;
    if (this._keyX || this._keyY) {
      // keyboard overrides with velocity-based movement
      if (this._keyX) ax = eng.paddle.x + this._keyX * 520 * dt;
      if (this._keyY) ay = eng.paddle.y + this._keyY * 380 * dt;
    }
    eng.aim(ax, ay);

    eng.update(dt);

    // --- handle engine events -> FX & audio --------------------------------
    for (const ev of eng.events) {
      this._handleEvent(ev);
    }

    // timers for visual juice
    this.flashWhite = Math.max(0, this.flashWhite - dt * 1.6);
    if (this.flashColor) {
      this.flashColor.a = Math.max(0, this.flashColor.a - dt * 2.2);
      if (this.flashColor.a <= 0) this.flashColor = null;
    }
    this.comboPop = Math.max(0, this.comboPop - dt);
    this.roofPop = Math.max(0, this.roofPop - dt);

    // particles always update
    this.particles.update(dt);

    // HUD
    this.hud.update({
      score: eng.score, mult: eng.mult, combo: eng.combo,
      lives: eng.lives, bestStreak: eng.bestStreak,
      powerTimers: eng.powerTimers, paddle: eng.paddle,
    });

    // state transitions for overlays
    if (eng.state === 'cleared') {
      if (this.overlayState !== 'levelClear') {
        this.overlayState = 'levelClear';
        this.state = 'levelClear';
        this.audio.musicStop();
        if (this.ui) {
          this.ui.show('levelclear');
          const bonus = eng.events.find(e => e.type === 'levelComplete')?.bonus || 0;
          this.ui.setLevelClear({ score: eng.score, bonus, level: eng.levelIndex + 1, mode: eng.mode });
        }
      }
    } else if (eng.state === 'over') {
      if (this.overlayState !== 'gameover') {
        this.overlayState = 'gameover';
        this.state = 'gameover';
        this.audio.musicStop();
        this.audio.gameOver();
        if (this.ui) {
          this.ui.show('gameover');
          this.ui.setGameOver({ score: eng.score, bestStreak: eng.bestStreak, level: eng.levelIndex + 1 });
        }
      }
    }
  }

  // --- engine event -> juice ------------------------------------------------
  _handleEvent(ev) {
    const P = this.particles, A = this.audio;
    switch (ev.type) {
      case 'launch':
        A.launch();
        P.spawn('ring', ev.x, ev.y, { color: '#9bd0ff', size: 46, life: 0.45 });
        P.spawn('burst', ev.x, ev.y, { count: 10, colors: ['#9bd0ff', '#ffffff'], speed: 260, life: 0.4 });
        break;
      case 'paddleHit':
        A.paddleHit(ev.speed01);
        if (ev.boost > 40) {
          P.spawn('spark', ev.x, ev.y, { color: '#aee6ff', size: 3, speed: 120, life: 0.3 });
          P.spawn('burst', ev.x, ev.y, { count: 6, colors: ['#aee6ff', '#ffffff'], speed: 180, life: 0.3 });
        }
        if (ev.speed01 > 0.8) {
          P.spawn('ring', ev.x, ev.y, { color: '#fff3a0', size: 30, life: 0.3 });
        }
        break;
      case 'bounce':
        A.bounce(ev.speed01 || 0.3, this.engine.combo);
        if ((ev.wall === 1)) {
          P.spawn('spark', ev.x, ev.y, { color: '#5fd0ff', size: 2, speed: 60, life: 0.22 });
        }
        break;
      case 'brickBreak': {
        const type = ev.brickType;
        const col = LEGEND[type]?.color || '#fff';
        const pts = ev.pts;
        A.brickBreak(this.engine.combo);
        P.spawn('shards', ev.x, ev.y, { count: 7, colors: [col, shade(col, -22), '#ffffff'], speed: 220, life: 0.9, size: 5 });
        P.spawn('ring', ev.x, ev.y, { color: col, size: 34, life: 0.35 });
        P.spawn('text', ev.x, ev.y, { text: '+' + pts, color: this._multColor(), size: 15 + Math.min(10, this.engine.combo), vy: -82, life: 1.0 });
        if (ev.combo >= 5) P.spawn('burst', ev.x, ev.y, { count: 8, colors: [col, '#fff'], speed: 230, life: 0.4 });
        if (ev.combo >= 10) P.spawn('star', ev.x, ev.y, { color: '#ffe082', size: 10, life: 0.6 });
        if (ev.crownBonus > 0) {
          P.spawn('text', ev.x, ev.y - 14, { text: 'CROWN +' + ev.crownBonus, color: '#ffd54f', size: 15, vy: -70, life: 1.1 });
        }
        break;
      }
      case 'brickReject':
        A.gateClang();
        P.spawn('spark', ev.x, ev.y, { color: '#ff8a80', size: 3, speed: 130, life: 0.3 });
        P.spawn('text', ev.x, ev.y, {
          text: ev.brickType === 'a' ? 'STEEP!' : ev.brickType === 's' ? 'FAST!' : ev.brickType === 't' ? 'ABOVE!' : '',
          color: '#ff8a80', size: 13, vy: -55, life: 0.7,
        });
        break;
      case 'brickCrack':
        P.spawn('spark', ev.x, ev.y, { color: '#ffd180', size: 2, speed: 70, life: 0.3 });
        break;
      case 'regrow':
        P.spawn('ring', ev.x, ev.y, { color: '#26a69a', size: 28, life: 0.4 });
        break;
      case 'comboUp':
        A.sparkle();
        this.comboPop = 1;
        this.flashColor = { color: '#ffe082', a: 0.12 };
        P.spawn('text', ev.x, ev.y - 26, { text: 'MULTIPLIER ×' + ev.mult, color: '#ffd54f', size: 19, vy: -66, life: 1.1 });
        break;
      case 'goldTing':
        A.bounce(ev.speed / MAX_BALL_SPEED, this.engine.combo);
        P.spawn('spark', ev.x, ev.y, { color: '#ffd700', size: 3, speed: 150, life: 0.35 });
        break;
      case 'explosion':
        A.explosion();
        this.flashColor = { color: '#ff8a50', a: 0.16 };
        P.spawn('plume', ev.x, ev.y, { count: 30, colors: ['#ffab40', '#ff7043', '#ffcdd2', '#ffd600'], speed: 480, life: 0.65, size: 6 });
        P.spawn('ring', ev.x, ev.y, { color: '#ffb74d', size: 90, life: 0.5, width: 5 });
        P.spawn('smoke', ev.x, ev.y, { count: 8, speed: 90, life: 1.3, size: 12, gravity: -50 });
        break;
      case 'dropSpawn':
        P.spawn('ring', ev.x, ev.y, { color: '#ffffff', size: 22, life: 0.35, width: 2 });
        break;
      case 'catch': {
        const pw = ev.power;
        const color = POWS[ev.power]?.color || '#fff';
        A.pickup();
        this.flashColor = { color, a: 0.10 };
        P.spawn('burst', ev.x, ev.y, { count: 14, colors: [color, '#fff'], speed: 260, life: 0.5 });
        P.spawn('ring', ev.x, ev.y, { color, size: 44, life: 0.45, width: 3 });
        P.spawn('text', ev.x, ev.y - 16, { text: POWS[ev.power]?.name || 'POWER', color, size: 16, vy: -60, life: 0.9 });
        break;
      }
      case 'multiball':
        A.multiball();
        this.flashColor = { color: '#e040fb', a: 0.12 };
        this.audio.setMusicIntensity(Math.min(3, 1));
        break;
      case 'shieldBlock':
        A.sparkle();
        P.spawn('ring', ev.x, ev.y, { color: '#80d8ff', size: 40, life: 0.4, width: 3 });
        break;
      case 'laserFire':
        P.spawn('spark', ev.x, ev.y, { color: '#40c4ff', size: 2, speed: 30, life: 0.2 });
        break;
      case 'rooftopEnter':
        A.rooftop(0);
        this.roofPop = 1;
        this.audio.setMusicIntensity(2);
        P.spawn('confetti', WORLD_W / 2, BRICK_TOP - 40, { count: 40, colors: ['#ffd54f', '#ff8a65', '#b388ff', '#4dd0e1', '#fff'], life: 2.2 });
        break;
      case 'rooftopTick':
        A.rooftop(ev.streak);
        P.spawn('sparkle', ev.x, ev.y, { color: '#ffd54f', size: 5, life: 1.4 });
        if (ev.streak % 4 === 0) {
          P.spawn('star', ev.x, ev.y, { color: '#ffd54f', size: 9, life: 0.7 });
        }
        if (ev.streak % 8 === 0) {
          this.flashColor = { color: '#ffd54f', a: 0.10 };
          P.spawn('ring', ev.x, ev.y, { color: '#ffd54f', size: 70, life: 0.5 });
        }
        if (ev.streak >= 4 && ev.streak % 4 === 0) {
          P.spawn('text', ev.x, ev.y - 20, { text: `ROOF ×${this.engine.mult}`, color: '#ffd54f', size: 14, vy: -55, life: 0.8 });
        }
        break;
      case 'rooftopExit':
        this.roofPop = 0;
        this.audio.setMusicIntensity(0);
        P.spawn('smoke', 0, 0, { count: 0 });
        break;
      case 'powerEnd':
        A.sparkle();
        break;
      case 'lifeLost':
        A.lifeLost();
        this.flashColor = { color: '#ff5252', a: 0.22 };
        this.flashWhite = 0.3;
        break;
      case 'extraLife':
        A.extraLife();
        break;
      case 'levelComplete':
        A.levelComplete();
        this.flashWhite = 0.45;
        this.flashColor = { color: '#ffe082', a: 0.18 };
        P.spawn('confetti', WORLD_W / 2, WORLD_H / 2, { count: 90, colors: ['#ffd54f', '#ff8a65', '#b388ff', '#4dd0e1', '#69f0ae', '#fff'], life: 2.6 });
        break;
      case 'gameOver':
        this.flashColor = { color: '#444', a: 0.2 };
        break;
      case 'flame':
        P.spawn('flame', ev.x, ev.y, { color: '#ff6e40' });
        break;
      case 'trail':
        P.spawn('trail', ev.x, ev.y, { color: '#9bd0ff' });
        break;
    }
  }

  _multColor() {
    const pal = ['#fff', '#7ef0a6', '#66f2ff', '#7aa5ff', '#c77dff', '#ff6ff2'];
    return pal[Math.min(pal.length - 1, this.engine.mult - 1)];
  }

  // ==========================================================================
  _render() {
    if (!this.renderer) return;
    const eng = this.engine;
    const state = {
      score: eng.score,
      mult: eng.mult,
      combo: eng.combo,
      time: this.time,
      flashWhite: this.flashWhite,
      flashColor: this.flashColor,
    };
    const layers = {
      bg: { xt: this.bgXt },
      bricks: eng.bricks,
      paddle: eng.paddle,
      balls: eng.balls,
      lasers: eng.lasers,
      drops: eng.drops,
      particles: this.particles,
      roof: {
        topY: eng.roofTopY,
        active: eng.rooftop,
        timer: eng.rooftopTimer,
        streak: eng.rooftopStreak,
        pop: this.roofPop,
      },
      vignette: { intensity: this._vignette() },
      cores: {
        score: eng.score, combo: eng.combo, mult: eng.mult,
        bestStreak: eng.bestStreak, rooftopStreak: eng.rooftopStreak,
      },
      hud2: {
        laser: eng.powerTimers.laser > 0,
        slow: eng.powerTimers.slow > 0,
        fire: eng.powerTimers.fire > 0,
        wide: eng.powerTimers.wide > 0,
        shield: eng.paddle.shield,
      },
    };
    // combo pop glow scale
    state.comboPop = this.comboPop;
    this.renderer.render(state, layers);
  }

  _vignette() {
    const m = this.engine.mult;
    return Math.min(1, 0.35 + (m - 1) * 0.18 + this.engine.rooftopStreak * 0.04);
  }
}

// tiny palette helper
function shade(hex, amt) {
  const n = parseInt(hex.slice(1), 16);
  const r = clamp8((n >> 16) + amt), g = clamp8(((n >> 8) & 0xff) + amt), b = clamp8((n & 0xff) + amt);
  return '#' + ((r << 16) | (g << 8) | b).toString(16).padStart(6, '0');
}
function clamp8(v) { return Math.max(0, Math.min(255, v)); }
