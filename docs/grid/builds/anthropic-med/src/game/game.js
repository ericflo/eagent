// src/game/game.js — the Game class: state, fixed-timestep update, draw, orchestration.
import {
  W, H, ATTIC_TOP, BRICK_TOP, DEATH_Y,
  PADDLE_Y_MIN, PADDLE_Y_MAX, BALL_R, BALL_SPEED_BASE, BALL_SPEED_MAX,
  LIVES_START, EXTRA_LIFE_SCORE, FIXED_DT, MAX_SUBSTEPS, MAX_DT,
} from '../core/constants.js';
import { clamp, hypot, lerp, randRange } from '../core/math.js';
import { Input } from '../core/input.js';
import { Paddle } from './paddle.js';
import { makeBall, stepBallPhysics, drawBall } from './ball.js';
import { AtticSystem } from './attic.js';
import { drawHUD } from './hud.js';
import { buildFallbackLevel } from './fallback.js';
import { loadHighScore, saveHighScore, loadSettings, saveSettings } from '../core/storage.js';

import { FX } from '../fx/fx.js';
import { Audio } from '../audio/audio.js';

let Bricks = null, Levels = null, Powerups = null;
try { Bricks = await import('./bricks.js'); } catch (e) { console.warn('[game] bricks.js unavailable', e); }
try { Levels = await import('./levels.js'); } catch (e) { console.warn('[game] levels.js unavailable', e); }
try { Powerups = await import('./powerups.js'); } catch (e) { console.warn('[game] powerups.js unavailable', e); }

export class Game {
  constructor(renderer) {
    this.renderer = renderer;
    this.fx = FX;
    this.audio = Audio;
    this.settings = loadSettings();
    this.highScore = loadHighScore();
    this.fx?.setReducedMotion?.(!!this.settings.reducedMotion);
    this.fx?.setPalette?.(this.settings.colorblind ? 'colorblind' : 'neon');


    this.scene = 'title';
    this.time = 0;
    this.acc = 0;
    this.fps = 60;
    this._fpsAcc = 0; this._fpsN = 0;
    this.showFps = false;

    this.paddle = new Paddle();
    this.balls = [];
    this.bricks = [];
    this.drops = [];
    this.bolts = [];
    this.effects = new Map();
    this.attic = new AtticSystem(this);
    this.attic.freeze = false;

    this.lives = LIVES_START;
    this.score = 0;
    this.mult = 1;
    this.combo = 0;
    this.timeScale = 1;
    this.shieldCharges = 0;
    this.level = 1;
    this.levelName = '';
    this.levelSubtitle = '';
    this.notifyList = [];
    this.bannerText = '';
    this.bannerT = 0;
    this.pendingLaunch = false;
    this.cameraZoom = 1;
    this.desatT = 0;
    this.paused = false;
    this.godMode = false;
    this.demo = false;
    this.hitstopT = 0;      // brief timeScale dip on big impacts (uppercuts, tier-ups)
    this._frameTimes = [];  // ring buffer of recent frame deltas (ms) for debug.frameStats()

    this._errHooked = false;
    this._levelStartT = 0;
    this._stats = { bricksBroken: 0, atticEntries: 0, powerupsCollected: 0, maxMult: 1, maxCombo: 0 };

    this.debug = this._makeDebug();
  }

  // ------------------------------------------------------------------
  notify(text, color) {
    this.notifyList.push({ text, color: color || '#fff', t: 2 });
    this.bannerText = text;
    this.bannerT = 1.6;
  }

  addScore(n) {
    const before = this.score;
    this.score += Math.max(0, n);
    if (Math.floor(before / EXTRA_LIFE_SCORE) < Math.floor(this.score / EXTRA_LIFE_SCORE)) {
      this.lives++;
      this.notify('EXTRA LIFE!', '#ffd15e');
      this.audio?.sfx?.('life');
    }
  }

  spawnBall(x, y, vx, vy, flags) {
    // Defensive cap: unbounded ball counts (e.g. a bad multiball chain, or a
    // stress test) would degrade the O(balls * bricks) collision sweep well
    // below 60fps. Cap total live balls generously but finitely.
    if (this.balls.length >= 8) return this.balls[0];
    const b = makeBall(x, y, vx, vy, { flags });
    this.balls.push(b);
    return b;
  }

  explodeAt(x, y, radius, source) {
    for (const b of this.bricks) {
      if (!b.alive) continue;
      const dx = b.cx - x, dy = b.cy - y;
      if (hypot(dx, dy) <= radius) {
        b.alive = false;
        this.onBrickBroken(b, { result: 'break', score: 80, sfx: null, fx: null });
      }
    }
    this.fx?.shockwave?.(x, y, { color: source === 'super' ? '#ffe15e' : '#ff7a3d', r1: radius });
    this.fx?.burst?.(x, y, { count: 40, color: '#ff7a3d', speed: 700, spread: Math.PI * 2 });
    this.fx?.shake?.(9, 0.3);
    this.audio?.sfx?.('brickExplode');
  }

  magnetPull(x, y, strength, dt) {
    for (const b of this.balls) {
      const dx = x - b.x, dy = y - b.y;
      const d = hypot(dx, dy) || 1;
      if (d > 260) continue;
      const k = strength * dt / (d * d) * 4000;
      b.vx += (dx / d) * k;
      b.vy += (dy / d) * k;
    }
  }

  repulse(x, y, strength) {
    for (const b of this.balls) {
      const dx = b.x - x, dy = b.y - y;
      const d = hypot(dx, dy) || 1;
      if (d > 300) continue;
      const k = strength / d * 6;
      b.vx += (dx / d) * k;
      b.vy += (dy / d) * k;
    }
    this.fx?.shockwave?.(x, y, { color: '#c9a1ff' });
  }

  onBrickBroken(brick, verdict) {
    this._stats.bricksBroken++;
    this.combo++;
    this._stats.maxCombo = Math.max(this._stats.maxCombo, this.combo);
    const base = verdict?.score ?? 100;
    const comboMul = 1 + Math.min(2, this.combo * 0.05);
    const inAttic = this.attic.active;
    const scoreGain = base * this.mult * comboMul;
    this.addScore(scoreGain);
    const cx = brick.cx ?? (brick.x + brick.w / 2), cy = brick.cy ?? (brick.y + brick.h / 2);
    const multK = clamp((this.mult - 1) / 20, 0, 1);
    const popSize = clamp(18 + Math.log2(1 + scoreGain) * 3.2, 18, 54);

    this.fx?.burst?.(cx, cy, { count: 16 + multK * 26, color: '#8ffcff', speed: 260 + multK * 260, spread: Math.PI * 2 });
    this.fx?.shockwave?.(cx, cy, { color: '#8ffcff', r0: 6, r1: 60 + multK * 140, life: 0.3 + multK * 0.25, width: 4 + multK * 6 });
    if (multK > 0.15) this.fx?.flash?.('#8ffcff', 0.06 + multK * 0.16, 0.2);
    if (multK > 0.3) this.fx?.chroma?.(multK * 0.6, 0.25);
    this.fx?.shake?.(1.5 + multK * 8, 0.12 + multK * 0.18);
    this.fx?.popup?.(cx, cy, `+${Math.round(scoreGain)}`, { color: '#eafcff', size: popSize });
    if (verdict?.sfx) this.audio?.sfx?.(verdict.sfx, { pitch: 1 + Math.min(0.6, this.combo * 0.02) });
    if (this.combo > 1 && this.combo % 5 === 0) {
      this.audio?.sfx?.('combo', { pitch: 1 + Math.min(1, this.combo * 0.03) });
      this.fx?.popup?.(cx, cy - 30, `COMBO x${this.combo}`, { color: '#ffd15e', size: 24 });
    }
    if (inAttic || brick.type === 'attic') this.attic.onAtticBrickBreak(brick);
    if (Powerups?.rollDrop) {
      const drop = Powerups.rollDrop(cx, cy, { level: this.level, combo: this.combo, inAttic });
      if (drop) this.drops.push(drop);
    }
    this.checkLevelClear();
  }


  checkLevelClear() {
    if (this.scene !== 'playing') return;
    const remaining = this.bricks.some((b) => b.alive && b.type !== 'steel');
    if (!remaining) this.enterLevelClear();
  }

  // ------------------------------------------------------------------
  loadLevel(n) {
    this.level = n;
    let def = null;
    try {
      if (Levels?.buildLevel) def = Levels.buildLevel(n);
    } catch (e) { console.warn('[game] buildLevel failed, using fallback', e); }
    if (!def || !Array.isArray(def.bricks) || def.bricks.length === 0) def = buildFallbackLevel(n);
    this.bricks = def.bricks;
    this.levelName = def.name || `Level ${n}`;
    this.levelSubtitle = def.subtitle || '';
    this.levelTint = def.tint || '#5ee6ff';
    this.baseBallSpeed = def.ballSpeed || BALL_SPEED_BASE;
    this.spawnPoint = def.spawn || { x: W / 2, y: PADDLE_Y_MAX - 60 };
    this.drops = [];
    this.bolts = [];
    this.attic.mult = 1;
    this.attic.chain = 0;
    this.attic.tier = 0;
    this.attic.tierBannerT = 0;
    this.attic.meter = 0;
    this.attic.active = false;
    this.combo = 0;
    this._levelStartT = 1.3;
    this.notify(this.levelName, '#5ee6ff');
    this.audio?.sfx?.('levelStart');
  }

  enterLevelClear() {
    this.scene = 'levelClear';
    this.bannerText = 'LEVEL CLEAR';
    this.bannerT = 3;
    this.audio?.sfx?.('levelClear');
    this.fx?.confetti?.(W / 2, H * 0.4, 80);
    this._levelClearT = 2.2;
  }

  nextLevel() {
    Powerups?.clearAllEffects?.(this);
    this.paddle.w = this.paddle.baseW;
    this.resetBallsOnPaddle();
    this.loadLevel(this.level + 1);
    this.scene = 'playing';
  }

  resetBallsOnPaddle() {
    this.balls = [];
    const sp = this.spawnPoint || { x: this.paddle.x, y: this.paddle.y - 40 };
    this.paddle.x = clamp(sp.x, this.paddle.w / 2, W - this.paddle.w / 2);
    const b = makeBall(this.paddle.x, this.paddle.y - this.paddle.h / 2 - BALL_R - 1, 0, 0, {});
    b.stuckToPaddle = true;
    b.stuckOffset = 0;
    this.balls.push(b);
  }

  startGame() {
    this.lives = LIVES_START;
    this.score = 0;
    this.mult = 1;
    this.combo = 0;
    this.shieldCharges = 0;
    this.attic.mult = 1;
    this.effects = new Map();
    this.bolts = [];
    this.drops = [];
    this._stats = { bricksBroken: 0, atticEntries: 0, powerupsCollected: 0, maxMult: 1, maxCombo: 0 };
    this.paddle = new Paddle();
    this.loadLevel(1);
    this.resetBallsOnPaddle();
    this.scene = 'playing';
    this.audio?.music?.start?.();
  }

  gameOver() {
    this.scene = 'gameOver';
    this.highScore = saveHighScore(this.score);
    this.audio?.sfx?.('death');
    this.audio?.music?.setIntensity?.(0.1);
  }

  loseBall(ball) {
    if (this.shieldCharges > 0 && !this.godMode) {
      this.shieldCharges--;
      ball.y = this.paddle.y - 200;
      ball.vy = -Math.abs(ball.vy || 600);
      this.fx?.shockwave?.(ball.x, DEATH_Y - 20, { color: '#9dff5c', r1: 260 });
      this.fx?.flash?.('#9dff5c', 0.4, 0.3);
      this.audio?.sfx?.('powerup');
      this.notify('SHIELD SAVE!', '#9dff5c');
      return true;
    }
    return false;
  }

  // ------------------------------------------------------------------
  update(dtRaw) {
    const dt = Math.min(dtRaw, MAX_DT);
    this.time += dt;
    this._fpsAcc += dt; this._fpsN++;
    if (this._fpsAcc >= 0.5) { this.fps = this._fpsN / this._fpsAcc; this._fpsAcc = 0; this._fpsN = 0; }
    this._frameTimes.push(dtRaw * 1000);
    if (this._frameTimes.length > 180) this._frameTimes.shift();

    Input.update(dt);
    const ev = Input.consumeEvents ? Input.consumeEvents() : {};
    if (ev.mute) { this.settings.muted = !this.settings.muted; this.audio?.setMuted?.(this.settings.muted); saveSettings(this.settings); }
    if (ev.restart && this.scene !== 'title') this.startGame();
    if (ev.pause && (this.scene === 'playing' || this.scene === 'paused')) {
      this.scene = this.scene === 'paused' ? 'playing' : 'paused';
    }

    if (this.bannerT > 0) this.bannerT = Math.max(0, this.bannerT - dt);
    if (this.desatT > 0) this.desatT = Math.max(0, this.desatT - dt);

    // hitstop: a couple of frames at ~quarter speed on a big impact (uppercut).
    // Purely a game-owned time dilation — FX/audio never alter time themselves.
    let effUpdateDt = dt;
    if (this.hitstopT > 0) {
      this.hitstopT = Math.max(0, this.hitstopT - dt);
      effUpdateDt = dt * 0.25;
    }

    if (this.scene === 'playing') this.updatePlaying(effUpdateDt);
    else if (this.scene === 'levelClear') this.updateLevelClear(dt);
    else if (this.scene === 'title') this.updateAttract(dt);

    this.fx?.update?.(dt);
    this.fx?.aurora?.(dt, this.attic?.intensity || 0);
  }


  updateAttract(dt) {
    // pure cosmetic bouncing demo balls + shimmering bricks for the title screen
    for (const b of this.balls) {
      b.x += b.vx * dt; b.y += b.vy * dt;
      if (b.x < b.r || b.x > W - b.r) { b.vx *= -1; b.x = clamp(b.x, b.r, W - b.r); }
      if (b.y < b.r) { b.vy *= -1; b.y = b.r; }
      if (b.y > H - b.r) { b.vy *= -1; b.y = H - b.r; }
      for (const brick of this.bricks) {
        if (!brick.alive) continue;
        if (b.x + b.r > brick.x && b.x - b.r < brick.x + brick.w && b.y + b.r > brick.y && b.y - b.r < brick.y + brick.h) {
          b.vy *= -1;
          brick.flash?.();
        }
      }
    }
    for (const brick of this.bricks) brick.update?.(dt, { game: this, dt, time: this.time });
  }

  updateLevelClear(dt) {
    if (this._levelClearT > 0) {
      this._levelClearT -= dt;
      if (this._levelClearT <= 0) this.nextLevel();
    }
  }

  updatePlaying(dt) {
    if (this._levelStartT > 0) this._levelStartT -= dt;

    const intent = Input.intent;
    Input.setPaddleBox && Input.setPaddleBox(this.paddle.rect);
    this.paddle.setTarget(intent.x, intent.y);
    this.paddle.update(dt);

    // launch stuck balls
    const stuck = this.balls.filter((b) => b.stuckToPaddle);
    for (const b of stuck) {
      b.x = clamp(this.paddle.x + (b.stuckOffset || 0), b.r, W - b.r);
      b.y = this.paddle.y - this.paddle.h / 2 - b.r - 1;
      if (intent.launch || this.demo) {
        b.stuckToPaddle = false;
        const ang = -Math.PI / 2 + randRange(-0.25, 0.25);
        const speed = this.baseBallSpeed || BALL_SPEED_BASE;
        b.vx = Math.cos(ang) * speed;
        b.vy = Math.sin(ang) * speed;
        this.audio?.sfx?.('launch');
      }
    }

    // effective timeScale: powerup slowmo (this.timeScale, owned by powerups.js) * attic pulse
    const atticScale = this.attic.timeScale;
    const effDt = dt * (this.timeScale || 1) * atticScale;

    this.stepPhysics(effDt);

    Powerups?.updatePowerups?.(this, dt);
    if (this.drops) {
      for (const d of this.drops) d.update?.(dt);
      this.drops = this.drops.filter((d) => {
        if (d.y > H + 40) return false;
        const pr = this.paddle.rect;
        if (d.y + 20 >= pr.y && d.y - 20 <= pr.y + pr.h && d.x > pr.x - 10 && d.x < pr.x + pr.w + 10) {
          this.collectDrop(d);
          return false;
        }
        return true;
      });
    }
    Powerups?.updateBolts?.(this, dt);

    for (const b of this.bricks) b.update?.(dt, { game: this, dt, time: this.time });

    this.attic.update(dt, this.bricks, this.balls);
    this._stats.maxMult = Math.max(this._stats.maxMult, this.attic.mult);
    this.mult = this.attic.mult;
    this.cameraZoom = 1 + (this.attic.cameraPush || 0);

    // lose balls past death line
    const before = this.balls.length;
    this.balls = this.balls.filter((b) => {
      if (b.y - b.r > DEATH_Y) {
        if (this.loseBall(b)) return true;
        return false;
      }
      return true;
    });
    if (this.balls.length === 0 && before > 0) {
      this.lives--;
      this.combo = 0;
      this.audio?.sfx?.('death');
      this.fx?.flash?.('#440000', 0.5, 0.5);
      this.desatT = 0.8;
      if (this.lives <= 0 && !this.godMode) {
        this.gameOver();
      } else {
        this.resetBallsOnPaddle();
      }
    }
  }

  collectDrop(d) {
    this._stats.powerupsCollected++;
    const res = Powerups?.applyPowerup?.(d.kind, this);
    this.audio?.sfx?.(Powerups?.POWERUPS?.[d.kind]?.good === false ? 'powerdown' : 'powerup');
    if (res) this.notify(res.text || res.name || d.kind, res.color || '#5ee6ff');
    this.fx?.burst?.(d.x, d.y, { count: 24, color: res?.color || '#5ee6ff', speed: 300 });
  }

  stepPhysics(dt) {
    this.acc += dt;
    let steps = 0;
    while (this.acc >= FIXED_DT && steps < MAX_SUBSTEPS) {
      this.substep(FIXED_DT);
      this.acc -= FIXED_DT;
      steps++;
    }
    if (steps >= MAX_SUBSTEPS) this.acc = 0; // drop remainder on huge spikes
  }

  substep(dt) {
    const paddleRect = this.paddle.rect;
    const world = {
      W, H,
      paddleRect,
      onPaddleHit: (ball, nx, ny) => this.handlePaddleHit(ball, nx, ny),
      bricks: this.bricks,
      onBrickHit: (brick, ball, nx, ny) => this.handleBrickHit(brick, ball, nx, ny),
    };
    for (const ball of this.balls) {
      if (ball.stuckToPaddle) continue;
      const events = stepBallPhysics(ball, dt, world);
      for (const e of events) {
        if (e.type === 'wall') { this.audio?.sfx?.('wall'); this.fx?.spark?.(ball.x, ball.y, '#8ffcff', 4); }
      }
    }
  }

  handlePaddleHit(ball, nx, ny) {
    if (this.paddle.flags.sticky && ny < 0) {
      ball.stuckToPaddle = true;
      ball.stuckOffset = ball.x - this.paddle.x;
      ball.vx = 0; ball.vy = 0;
      this.audio?.sfx?.('paddle');
      return;
    }
    if (ny >= 0) return; // side/bottom touch, ignore (shouldn't normally happen)
    const res = this.paddle.resolveHit(ball);
    this.combo = 0;
    if (res.uppercut) {
      this.fx?.burst?.(ball.x, ball.y, { count: 36, color: '#ffd15e', speed: 560, spread: Math.PI * 0.7, dir: -Math.PI / 2 });
      this.fx?.shockwave?.(ball.x, this.paddle.y - this.paddle.h / 2, { color: '#ffd15e', r0: 10, r1: 220, life: 0.4, width: 8 });
      this.fx?.shake?.(9, 0.22);
      this.fx?.flash?.('#ffd15e', 0.22, 0.18);
      this.fx?.chroma?.(0.5, 0.2);
      this.fx?.popup?.(ball.x, ball.y - 20, 'UPPERCUT!', { color: '#ffd15e', size: 28 });
      this.audio?.sfx?.('paddleUp');
      this.hitstopT = 0.06; // 2-4 frames at ~60fps
    } else {
      this.audio?.sfx?.('paddle', { pitch: res.softCatch ? 0.85 : 1 });
      this.fx?.spark?.(ball.x, ball.y, '#5ee6ff', 8);
      this.fx?.ribbon?.(ball.id, ball.x, ball.y, { color: '#5ee6ff' });
    }
  }


  handleBrickHit(brick, ball, nx, ny) {
    const verdict = brick.hitBy ? brick.hitBy(ball, { nx, ny }, this) : { result: 'break', score: 100 };
    if (verdict.result === 'break') {
      this.onBrickBroken(brick, verdict);
    } else if (verdict.result === 'bounce') {
      if (verdict.sfx) this.audio?.sfx?.(verdict.sfx);
      this.fx?.spark?.(ball.x, ball.y, '#ff5ec8', 6);
    }
    return verdict.result;
  }

  // ------------------------------------------------------------------
  draw(g, t) {
    const zoom = this.scene === 'playing' ? this.cameraZoom : 1;
    const beatPhase = this.audio?.music?.beatPhase?.() ?? 0;
    this.renderer.drawBackground(g, t, this.attic?.intensity || 0, beatPhase, this.levelTint);

    if (this.scene === 'title') { this.drawTitle(g, t); return; }

    if (this.scene === 'playing') this.drawAtticField(g, t, beatPhase);

    this.fx?.drawBelow?.(g);

    for (const b of this.bricks) b.draw?.(g, t);
    for (const d of this.drops) d.draw?.(g, t);
    Powerups?.drawBolts?.(g, t, this.bolts);
    this.paddle.draw(g, t, { mult: this.mult });

    if (this.scene === 'playing') this.spawnBallTrails(t);
    const rainbow = this.mult >= 20 || (this.attic?.intensity || 0) > 0.85;
    for (const b of this.balls) drawBall(g, b, t, { atticIntensity: this.attic?.intensity || 0, mult: this.mult, rainbow });

    this.fx?.drawAbove?.(g);
    Input.drawTouchUI?.(g);

    drawHUD(g, this, t);

    if (this._levelStartT > 0) this.drawLevelBanner(g);
    if (this.bannerT > 0 && this._levelStartT <= 0 && this.scene === 'playing') this.drawToast(g);
    if (this.attic?.tierBannerT > 0) this.drawTierBanner(g);
    if (this.scene === 'levelClear') this.drawLevelClearBanner(g);
    if (this.scene === 'paused') this.drawPause(g);
    if (this.scene === 'gameOver') this.drawGameOver(g);
  }

  /** Per-ball glowing ribbon trail: length/brightness scale with speed & attic meter; rainbow at high mult. */
  spawnBallTrails(t) {
    const atticIntensity = this.attic?.intensity || 0;
    const rainbow = this.mult >= 20 || atticIntensity > 0.85;
    for (const b of this.balls) {
      if (b.stuckToPaddle) continue;
      const speed = b.speed || hypot(b.vx, b.vy);
      const speedK = clamp(speed / BALL_SPEED_MAX, 0, 1);
      const color = rainbow ? `hsl(${(t * 220 + b.x * 0.3) % 360},95%,62%)` : (b.flags?.fire ? '#ff7a3d' : b.flags?.super ? '#ffe15e' : b.flags?.heavy ? '#c39bff' : '#8ffcff');
      const life = 0.22 + speedK * 0.3 + atticIntensity * 0.35;
      if (this.fx?.ribbon) {
        this.fx.ribbon(b.id, b.x, b.y, { color, life, width: b.r * (0.7 + speedK * 0.5 + atticIntensity * 0.6), rainbow });
      } else {
        this.fx?.trail?.(b.x, b.y, b.r * (0.7 + speedK * 0.5 + atticIntensity * 0.6), color, { life });
      }
      if (b.flags?.fire && Math.random() < 0.5) this.fx?.flame?.(b.x, b.y, { dir: Math.atan2(-b.vy, -b.vx), count: 4 });
    }
  }


  /** Escalating attic-zone background: a vertical "attic zone" light shaft above the bricks that brightens with tier. The full-field aurora + beat rings are handled by FX (drawBelow/beatRing). */
  drawAtticField(g, t, beatPhase) {
    const a = this.attic;
    if (!a) return;
    const k = Math.max(a.meter, a.active ? 0.3 : 0);
    if (k <= 0.01) return;
    g.save();
    g.globalCompositeOperation = 'lighter';
    const shaftH = BRICK_TOP + 20;
    const tierIdx = a.tier || 0;
    const hue = 190 - tierIdx * 30;
    const grad = g.createLinearGradient(0, 0, 0, shaftH);
    grad.addColorStop(0, `hsla(${hue},95%,70%,${0.05 + k * 0.18 + tierIdx * 0.02})`);
    grad.addColorStop(1, `hsla(${hue},95%,60%,0)`);
    g.fillStyle = grad;
    g.fillRect(0, 0, W, shaftH);
    g.restore();
  }


  drawToast(g) {
    g.save();
    g.globalAlpha = clamp(this.bannerT, 0, 1);
    g.font = '700 22px system-ui, sans-serif';
    g.fillStyle = '#fff';
    g.textAlign = 'center';
    g.fillText(this.bannerText, W / 2, 170);
    g.textAlign = 'left';
    g.restore();
  }

  drawTierBanner(g) {
    const a = this.attic;
    const k = a.tierBannerT;
    const zoneTop = BRICK_TOP + 400, zoneBottom = PADDLE_Y_MIN - 40;
    const cy = (zoneTop + zoneBottom) / 2 - 140;
    g.save();
    g.globalAlpha = clamp(k * 2, 0, 1);
    const scale = 1 + (1 - k) * 0.3;
    g.translate(W / 2, cy);
    g.scale(scale, scale);
    g.textAlign = 'center';
    g.font = '900 48px system-ui, sans-serif';
    g.shadowColor = a.tierBannerColor;
    g.shadowBlur = 24;
    g.fillStyle = a.tierBannerColor;
    g.fillText(a.tierBannerText, 0, 0);
    g.font = '700 20px system-ui, sans-serif';
    g.shadowBlur = 0;
    g.fillStyle = 'rgba(255,255,255,0.85)';
    g.fillText(`MULTIPLIER x${a.mult.toFixed(1)}`, 0, 36);
    g.restore();
  }

  drawLevelBanner(g) {
    const dur = 1.3;
    const elapsed = dur - this._levelStartT;
    const fadeOutStart = dur - 0.45;
    let a;
    if (elapsed < 0.12) a = elapsed / 0.12;
    else if (elapsed < fadeOutStart) a = 1;
    else a = clamp(1 - (elapsed - fadeOutStart) / 0.45, 0, 1);
    if (a <= 0.01) return;
    // place it in the empty lower playfield area, well clear of the bricks
    const zoneTop = BRICK_TOP + 400, zoneBottom = PADDLE_Y_MIN - 40;
    const bandY = clamp((zoneTop + zoneBottom) / 2 - 80, BRICK_TOP + 300, PADDLE_Y_MIN - 200);
    g.save();
    g.globalAlpha = a;
    g.fillStyle = 'rgba(5,4,12,0.55)';
    g.fillRect(0, bandY, W, 160);
    g.textAlign = 'center';
    g.font = '800 46px system-ui, sans-serif';
    g.fillStyle = '#8ffcff';
    g.fillText(this.levelName, W / 2, bandY + 60);
    g.font = '500 22px system-ui, sans-serif';
    g.fillStyle = 'rgba(255,255,255,0.8)';
    g.fillText(this.levelSubtitle, W / 2, bandY + 100);
    g.textAlign = 'left';
    g.restore();
  }


  drawLevelClearBanner(g) {
    g.save();
    g.fillStyle = 'rgba(5,4,12,0.6)';
    g.fillRect(0, 0, W, H);
    g.textAlign = 'center';
    g.font = '800 56px system-ui, sans-serif';
    g.fillStyle = '#9dff5c';
    g.fillText('LEVEL CLEAR', W / 2, H / 2 - 20);
    g.font = '600 26px system-ui, sans-serif';
    g.fillStyle = '#fff';
    g.fillText(`Score: ${Math.round(this.score).toLocaleString()}`, W / 2, H / 2 + 30);
    g.textAlign = 'left';
    g.restore();
  }

  drawPause(g) {
    g.save();
    g.fillStyle = 'rgba(5,4,12,0.75)';
    g.fillRect(0, 0, W, H);
    g.textAlign = 'center';
    g.font = '800 50px system-ui, sans-serif';
    g.fillStyle = '#fff';
    g.fillText('PAUSED', W / 2, H / 2 - 60);
    g.font = '500 22px system-ui, sans-serif';
    g.fillStyle = 'rgba(255,255,255,0.75)';
    g.fillText('Tap / Space to resume · R to restart · M to mute', W / 2, H / 2);
    g.textAlign = 'left';
    g.restore();
  }

  drawGameOver(g) {
    g.save();
    g.fillStyle = 'rgba(5,4,12,0.82)';
    g.fillRect(0, 0, W, H);
    g.textAlign = 'center';
    g.font = '800 54px system-ui, sans-serif';
    g.fillStyle = '#ff5ec8';
    g.fillText('GAME OVER', W / 2, H / 2 - 140);
    g.font = '600 28px system-ui, sans-serif';
    g.fillStyle = '#fff';
    g.fillText(`Score: ${Math.round(this.score).toLocaleString()}`, W / 2, H / 2 - 80);
    g.fillText(`High Score: ${Math.round(this.highScore).toLocaleString()}`, W / 2, H / 2 - 40);
    g.font = '500 20px system-ui, sans-serif';
    g.fillStyle = 'rgba(255,255,255,0.75)';
    g.fillText(`Bricks broken: ${this._stats.bricksBroken}   Max combo: ${this._stats.maxCombo}   Max mult: ${this._stats.maxMult.toFixed(1)}`, W / 2, H / 2 + 10);
    g.font = '700 24px system-ui, sans-serif';
    g.fillStyle = '#5ee6ff';
    g.fillText('Tap to play again', W / 2, H / 2 + 90);
    g.textAlign = 'left';
    g.restore();
  }

  drawTitle(g, t) {
    // attract mode: demo bricks shimmer, demo balls bounce (purely cosmetic, uses this.bricks/this.balls if seeded by main.js)
    for (const b of this.bricks) b.draw?.(g, t);
    for (const b of this.balls) drawBall(g, b, t);
    this.fx?.drawAbove?.(g);

    g.save();
    g.textAlign = 'center';
    g.font = '900 64px system-ui, sans-serif';
    const grad = g.createLinearGradient(W / 2 - 220, 0, W / 2 + 220, 0);
    grad.addColorStop(0, '#5ee6ff');
    grad.addColorStop(0.5, '#ff5ec8');
    grad.addColorStop(1, '#b0ff5e');
    g.fillStyle = grad;
    g.fillText('ATTIC BREAKER', W / 2, 210);
    g.font = '500 20px system-ui, sans-serif';
    g.fillStyle = 'rgba(255,255,255,0.75)';
    g.fillText('get up there. let it rip.', W / 2, 250);

    // PLAY button
    const by = 900, bw = 260, bh = 76;
    g.fillStyle = 'rgba(94,230,255,0.15)';
    g.strokeStyle = '#5ee6ff';
    g.lineWidth = 3;
    g.beginPath();
    g.roundRect ? g.roundRect(W / 2 - bw / 2, by, bw, bh, 16) : g.rect(W / 2 - bw / 2, by, bw, bh);
    g.fill(); g.stroke();
    g.font = '800 34px system-ui, sans-serif';
    g.fillStyle = '#fff';
    g.fillText('PLAY', W / 2, by + 48);

    g.font = '500 18px system-ui, sans-serif';
    g.fillStyle = 'rgba(255,255,255,0.6)';
    g.fillText(`High Score: ${Math.round(this.highScore).toLocaleString()}`, W / 2, by - 30);

    g.font = '400 16px system-ui, sans-serif';
    g.fillText('mouse · touch · WASD/arrows · gamepad', W / 2, H - 60);
    g.textAlign = 'left';
    g.restore();
  }

  // ------------------------------------------------------------------
  _makeDebug() {
    const game = this;
    return {
      addBall() {
        const b = game.balls[0];
        const x = b ? b.x : W / 2, y = b ? b.y : PADDLE_Y_MAX - 100;
        game.spawnBall(x, y, randRange(-300, 300), -randRange(500, 800), {});
      },
      setMult(n) { game.attic.mult = n; game.mult = n; },
      jumpToLevel(n) {
        Powerups?.clearAllEffects?.(game);
        game.paddle.w = game.paddle.baseW;
        game.loadLevel(n);
        game.resetBallsOnPaddle();
        game.scene = 'playing';
      },
      forceAttic() {
        for (const b of game.bricks) {
          if (b.alive && b.y > BRICK_TOP + 40) b.alive = false;
        }
        if (game.balls[0]) { game.balls[0].y = BRICK_TOP - 60; game.balls[0].stuckToPaddle = false; game.balls[0].vx = 200; game.balls[0].vy = -300; }
      },
      spawnDrop(kind) {
        if (Powerups?.Drop) game.drops.push(new Powerups.Drop(W / 2, 300, kind));
      },
      killBall() {
        for (const b of game.balls) b.y = DEATH_Y + 100;
      },
      godMode(v) { game.godMode = v === undefined ? !game.godMode : !!v; },
      frameStats() {
        const arr = game._frameTimes.slice().sort((a, b) => a - b);
        const n = arr.length || 1;
        const median = arr[Math.floor(n * 0.5)] || 0;
        const p95 = arr[Math.min(n - 1, Math.floor(n * 0.95))] || 0;
        const avg = arr.reduce((s, v) => s + v, 0) / n;
        return {
          fps: avg > 0 ? 1000 / avg : 0,
          medianFrameMs: median,
          p95FrameMs: p95,
          particles: game.fx?.count ?? 0,
        };
      },
      stats() {
        return {
          score: game.score,
          lives: game.lives,
          level: game.level,
          mult: game.mult,
          combo: game.combo,
          bricksBroken: game._stats.bricksBroken,
          powerupsCollected: game._stats.powerupsCollected,
          maxMult: game._stats.maxMult,
          maxCombo: game._stats.maxCombo,
          scene: game.scene,
          ballCount: game.balls.length,
        };
      },
    };
  }
}
