// engine.js — core simulation for Rooftop Breakout.
// Pure logic: no DOM, no canvas, no audio. Importable from node for tests.
// The engine owns physics, bricks, paddle, balls, lasers, drops, scoring,
// combo/multiplier, the "rooftop" zone and power-ups. Rendering and sound are
// driven by the events it emits each frame (see `events` array).

import { COLS } from './levels.js';
import { LEVELS } from './levels.js';
import { endlessLevel } from './levels.js';
import { LEGEND } from './levels.js';

export const WORLD_W = 480;
export const WORLD_H = 880;

export const BRICK_CELL_W = 45;            // 10 columns across 480
export const BRICK_CELL_H = 20;
export const BRICK_LEFT = 15;              // left margin so bricks span 15..465
export const BRICK_TOP = 132;              // top margin: plenty of air for rooftop play
export const BRICK_COLS = COLS;

export const PADDLE_W = 84;
export const PADDLE_H = 14;
export const PADDLE_MIN_X = 34;
export const PADDLE_MAX_X = WORLD_W - 34;
export const PADDLE_MIN_Y = 620;           // how far up the paddle can climb
export const PADDLE_MAX_Y = 852;

export const BALL_R = 7;
export const MIN_BALL_SPEED = 250;
export const MAX_BALL_SPEED = 780;
export const MAX_FIRE_SPEED = 900;
export const BALL_START_SPEED = 330;

export const MAX_BALLS = 8;
export const LASER_SPEED = 950;
export const LASER_INTERVAL = 0.30;
export const DROP_SPEED = 165;

export const COMBO_WINDOW = 2.1;
export const MULT_PER = 5;                 // +1 multiplier every 5 combos
export const MAX_MULT = 12;

export const EXPLOSION_RADIUS = 1.75;      // in cells

// --- power-up definitions -------------------------------------------------
export const POWERS = {
  multiball:  { name: 'Multiball', icon: '◎', color: '#e040fb' },
  wide:       { name: 'Wide Paddle', icon: '◄►', color: '#69f0ae' },
  laser:      { name: 'Laser', icon: '⚡', color: '#40c4ff' },
  slow:       { name: 'Slow Balls', icon: '◐', color: '#b388ff' },
  fire:       { name: 'Fireball', icon: '▲', color: '#ff6e40' },
  shield:     { name: 'Shield', icon: '◆', color: '#80d8ff' },
  extraLife:  { name: 'Extra Life', icon: '♥', color: '#ff5252' },
};

const DROP_WEIGHTS = {
  multiball: 16, wide: 16, laser: 13, slow: 12, fire: 13, shield: 13, extraLife: 9,
};

// Base score per brick type (gold can never be destroyed).
const BASE_SCORE = {
  n: 10, c: 15, a: 25, s: 25, b: 20, r: 20, t: 30, w: 15, g: 0,
};

export const BRICK_BASE_SCORE = BASE_SCORE;

const BREAKABLE = new Set(['n', 'c', 'a', 's', 'b', 'r', 't', 'w']);
const FIRE_BREAKABLE = new Set(['n', 'c', 'b', 'w']); // fire pierces these
const LASER_BREAKABLE = new Set(['n', 'c', 'a', 's', 'b', 'r', 't', 'w']); // only gold resists lasers

// Which drop consequence each brick type carries (chance on destroy).
const DROP_CHANCE = { n: 0.06, c: 0.18, a: 0.08, s: 0.08, b: 0.12, r: 0.12, t: 0.14, w: 1.0, g: 0 };

// Speed threshold required to break a Speed brick (scales with level a bit).
export function speedThreshold(levelIndex, lvl) {
  return Math.min(420 + lvl * 26, 640) + Math.max(0, levelIndex) * 0;
}

function mulberry32(seed) {
  let a = seed >>> 0;
  return function () {
    a |= 0; a = (a + 0x6D2B79F5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export class Engine {
  constructor(opts = {}) {
    this.rng = mulberry32(opts.seed ?? (Date.now() & 0xffffffff));
    this.mode = opts.mode || 'campaign'; // 'campaign' | 'endless'
    this.events = [];                    // drained by the game layer each frame

    this.frame = 0;
    this.state = 'idle';                 // idle | serving | playing | cleared | over
    this.score = 0;
    this.mult = 1;
    this.combo = 0;
    this.lives = 3;
    this.levelIndex = 0;                 // 0-based
    this.levelNumber = 1;                // for endless (1-based)
    this.waveBricksDestroyed = 0;
    this.maxCombo = 4;

    this.paddle = {
      x: WORLD_W / 2,
      y: PADDLE_MAX_Y,
      w: PADDLE_W,
      h: PADDLE_H,
      vx: 0, vy: 0,
      targetX: WORLD_W / 2,
      targetY: PADDLE_MAX_Y,
      laser: 0,          // seconds remaining
      laserTimer: 0,
      shield: 0,         // shield charges
      wideTimer: 0,
    };

    this.balls = [];
    this.lasers = [];
    this.drops = [];
    this.bricks = [];                    // grid[row][col]
    this.bricksAlive = 0;                // breakable bricks remaining
    this.roofTopY = BRICK_TOP;           // y of the top of the highest alive brick

    this.powerTimers = { wide: 0, laser: 0, slow: 0, fire: 0 };

    // rooftop zone state
    this.rooftop = false;
    this.rooftopTimer = 0;               // time spent on the roof this streak
    this.rooftopStreak = 0;              // chime ladder step
    this.rooftopTextTimer = 0;           // throttle for floating rooftop text
    this.bestStreak = 0;

    this.lastBreakTime = -99;
    this.servingBall = null;
    this.timeSinceServe = 0;

    this.destroyedThisFrame = [];        // for game-layer juice decisions
    this.levelRows = 0;
  }

  // -- lifecycle ------------------------------------------------------------
  loadLevel(index) {
    this.levelIndex = index;
    this.mode = 'campaign';
    this._buildGrid(LEVELS[index]);
  }

  loadEndless(n) {
    this.mode = 'endless';
    this.levelNumber = n;
    this._buildGrid(endlessLevel(n));
  }

  _buildGrid(rows) {
    this.bricks = [];
    this.bricksAlive = 0;
    this.levelRows = rows.length;
    for (let r = 0; r < rows.length; r++) {
      const row = rows[r];
      const cells = [];
      for (let c = 0; c < COLS; c++) {
        const ch = (row[c] || '.').toLowerCase();
        const type = ch === '.' || ch === ' ' ? null : ch;
        cells.push({
          type, alive: type !== null,
          row: r, col: c,
          x: BRICK_LEFT + c * BRICK_CELL_W,
          y: BRICK_TOP + r * BRICK_CELL_H,
          w: BRICK_CELL_W,
          h: BRICK_CELL_H,
          hits: 0,               // reborn: hits landed
          regrow: 0,             // reborn: timer to regrow
          flash: 0,              // visual flash timer (engine keeps, render shows)
        });
        if (type !== null && BREAKABLE.has(type)) this.bricksAlive++;
      }
      this.bricks.push(cells);
    }
    this._recomputeRoof();
    this.resetRound();
  }

  resetRound() {
    this.state = 'serving';
    this.balls = [];
    this.lasers = [];
    this.drops = [];
    this.powerTimers = { wide: 0, laser: 0, slow: 0, fire: 0 };
    this.paddle.shield = 0;
    this.paddle.laserTimer = 0;
    this.combo = 0;
    this.mult = 1;
    this.rooftop = false;
    this.rooftopTimer = 0;
    this.rooftopStreak = 0;
    this.serveBall();
  }

  serveBall() {
    const b = {
      x: this.paddle.x,
      y: this.paddle.y - BALL_R - 3,
      vx: 0, vy: 0,
      r: BALL_R,
      fire: false,
      dead: false,
    };
    this.balls = [b];
    this.servingBall = b;
    this.state = 'serving';
  }

  launch() {
    if (this.state !== 'serving') return;
    const speed = this._baseSpeed();
    const dirx = (this.rng() * 2 - 1) * 0.55;
    const diry = -Math.sqrt(1 - dirx * dirx);
    const b = this.servingBall;
    b.vx = dirx * speed;
    b.vy = diry * speed;
    b.x = this.paddle.x;
    b.y = this.paddle.y - BALL_R - 3;
    this.servingBall = null;
    this.state = 'playing';
    this.events.push({ type: 'launch', x: b.x, y: b.y, speed });
  }

  _baseSpeed() {
    const lvl = this.mode === 'endless' ? this.levelNumber : this.levelIndex + 1;
    let s = 300 + lvl * 22;
    if (this.mode === 'endless') s = 300 + lvl * 14;
    return Math.min(s, 520);
  }

  // -- input ----------------------------------------------------------------
  // game layer sets paddle target each frame from input abstraction
  aim(x, y) {
    this.paddle.targetX = x;
    this.paddle.targetY = y;
  }

  // -- main update ----------------------------------------------------------
  update(dt) {
    if (dt <= 0) return;
    this.frame++;
    this.events.length = 0;
    // sanitise dt
    if (dt > 0.033) dt = 0.033;

    this._updateTimers(dt);
    if (this.state === 'serving' || this.state === 'playing') {
      this._movePaddle(dt);
      this._updateBalls(dt);
      this._updateLasers(dt);
      this._updateDrops(dt);
      this._updateRooftop(dt);
    }
    if (this.state === 'playing' && this.bricksAlive === 0) {
      this.state = 'cleared';
      const bonus = 150 + (this.mode === 'endless' ? this.levelNumber : this.levelIndex + 1) * 100;
      this.score += bonus * this.mult;
      this.events.push({ type: 'levelComplete', bonus: bonus * this.mult, mult: this.mult });
    }
  }

  _updateTimers(dt) {
    const pt = this.powerTimers;
    if (pt.wide > 0) { pt.wide -= dt; if (pt.wide <= 0) { pt.wide = 0; this.paddle.w = PADDLE_W; this.events.push({ type: 'powerEnd', power: 'wide' }); } }
    else if (this.paddle.w !== PADDLE_W) this.paddle.w = PADDLE_W;
    if (pt.laser > 0) { pt.laser -= dt; if (pt.laser <= 0) { pt.laser = 0; this.paddle.laserTimer = 0; this.events.push({ type: 'powerEnd', power: 'laser' }); } }
    if (pt.slow > 0) { pt.slow -= dt; if (pt.slow <= 0) { pt.slow = 0; this.events.push({ type: 'powerEnd', power: 'slow' }); } }
    if (pt.fire > 0) { pt.fire -= dt; if (pt.fire <= 0) { pt.fire = 0; for (const b of this.balls) b.fire = false; this.events.push({ type: 'powerEnd', power: 'fire' }); } }
    // reborn regrow timers
    for (const row of this.bricks) {
      for (const bk of row) {
        if (bk.alive && bk.type === 'r' && bk.hits > 0) {
          bk.regrow -= dt;
          if (bk.regrow <= 0) {
            bk.hits = 0;
            bk.regrow = 0;
            this.events.push({ type: 'regrow', row: bk.row, col: bk.col });
          }
        }
        if (bk.alive && bk.flash > 0) bk.flash -= dt;
      }
    }
  }

  _movePaddle(dt) {
    const p = this.paddle;
    let tx = clamp(p.targetX, PADDLE_MIN_X, PADDLE_MAX_X);
    let ty = clamp(p.targetY, PADDLE_MIN_Y, PADDLE_MAX_Y);
    if (p.wideTimer > 0) {
      // allow wide paddle edges to get closer to the walls for fairness
      tx = clamp(p.targetX, PADDLE_MIN_X - 24, PADDLE_MAX_X + 24);
    }
    if (this.powerTimers.wide > 0) {
      // clamp by actual half width so it still cannot leave the world
      tx = clamp(tx, p.w / 2 + 8, WORLD_W - p.w / 2 - 8);
    } else {
      tx = clamp(tx, p.w / 2 + 8, WORLD_W - p.w / 2 - 8);
    }
    const oldX = p.x, oldY = p.y;
    // smooth-follow with a little easing; velocity emerges from actual motion
    const k = 1 - Math.exp(-22 * dt);
    p.x += (tx - p.x) * k;
    p.y += (ty - p.y) * k;
    p.x = clamp(p.x, p.w / 2 + 8, WORLD_W - p.w / 2 - 8);
    p.y = clamp(p.y, PADDLE_MIN_Y, PADDLE_MAX_Y);
    p.vx = dt > 0 ? (p.x - oldX) / dt : 0;
    p.vy = dt > 0 ? (p.y - oldY) / dt : 0;
    if (this.powerTimers.fire > 0 && !this.balls.some(b => b.fire)) {
      for (const b of this.balls) b.fire = true;
    }
    // serving ball follows paddle
    if (this.state === 'serving' && this.servingBall) {
      this.servingBall.x = p.x;
      this.servingBall.y = p.y - BALL_R - 3;
      this.servingBall.vx = p.vx;
      this.servingBall.vy = p.vy;
    }
  }

  _updateBalls(dt) {
    const p = this.paddle;
    const pt = this.powerTimers;
    const slowFactor = pt.slow > 0 ? 0.72 : 1;
    // trail & fire ambient events
    for (const b of this.balls) {
      if (b.dead) continue;
      if (b.fire) {
        this.events.push({ type: 'flame', x: b.x, y: b.y, color: b.fire === 'gold' ? '#ffd54f' : '#ff6e40' });
      } else {
        if ((this.frame % 3) === 0) {
          this.events.push({ type: 'trail', x: b.x, y: b.y });
        }
      }
    }
    for (const b of this.balls) {
      if (b.dead) continue;
      const speed = Math.hypot(b.vx, b.vy) || MIN_BALL_SPEED;
      // speed limits (slow power lowers the ceiling)
      let maxS = b.fire ? MAX_FIRE_SPEED : MAX_BALL_SPEED;
      if (pt.slow > 0) maxS = Math.min(maxS, 380);
      if (speed > maxS) {
        b.vx *= maxS / speed; b.vy *= maxS / speed;
      }
      if (speed < MIN_BALL_SPEED && stateMoving(b)) {
        const f = MIN_BALL_SPEED / speed;
        b.vx *= f; b.vy *= f;
      }
      this._stepBall(b, dt);
      // floor
      if (b.y > WORLD_H + 30) {
        b.dead = true;
        continue;
      }
      if (b.y - b.r > WORLD_H) { b.dead = true; continue; }
      // shield intercept (only when the ball is still near the floor line)
      if (b.y + b.r >= WORLD_H && pt.fire === 0 && p.shield > 0 && b.vy > 0) {
        this._protectedByShield(b);
      }
    }
    // remove dead balls
    const wasPlaying = this.balls.length;
    this.balls = this.balls.filter(b => !b.dead);
    if (this.state === 'playing' && this.balls.length === 0 && wasPlaying > 0) {
      this._lifeLost();
    }
  }

  _protectedByShield(b) {
    const p = this.paddle;
    if (p.shield > 0 && b.vy > 0 && b.y + b.r >= WORLD_H) {
      p.shield--;
      b.y = WORLD_H - b.r - 2;
      b.vy = -Math.abs(b.vy);
      this.events.push({ type: 'shieldBlock', x: b.x, y: WORLD_H - 6 });
      return true;
    }
    return false;
  }

  _stepBall(b, dt) {
    const p = this.paddle;
    const pt = this.powerTimers;
    // Continuous collision: sub-step so a fast ball cannot tunnel through
    // bricks or the paddle. Max displacement per substep = ~half a cell.
    const maxMove = 18;
    const dist = Math.hypot(b.vx, b.vy) * dt;
    const subs = Math.max(1, Math.ceil(dist / maxMove));
    const h = dt / subs;
    for (let i = 0; i < subs; i++) {
      const beforeY = b.y;
      b.x += b.vx * h;
      b.y += b.vy * h;
      if (!isFinite(b.x) || !isFinite(b.y)) { b.x = WORLD_W / 2; b.y = 300; b.vx = 0; b.vy = -MIN_BALL_SPEED; }

      // ceiling & walls
      if (b.y - b.r < 0) {
        b.y = b.r;
        b.vy = Math.abs(b.vy);
        this._bounceSfx(b);
      }
      if (b.x - b.r < 0) {
        b.x = b.r;
        b.vx = Math.abs(b.vx);
        if (this.state === 'playing') this.events.push({ type: 'bounce', x: b.x, y: b.y, wall: 1, speed01: 0.35 });
      } else if (b.x + b.r > WORLD_W) {
        b.x = WORLD_W - b.r;
        b.vx = -Math.abs(b.vx);
        if (this.state === 'playing') this.events.push({ type: 'bounce', x: b.x, y: b.y, wall: 1, speed01: 0.35 });
      }

      // paddle (only from above / moving down)
      if (this.state === 'playing' && b.vy > 0 && b.y + b.r >= p.y - p.h / 2 && b.x >= p.x - p.w / 2 - b.r && b.x <= p.x + p.w / 2 + b.r) {
        this._paddleBounce(b);
        // after a bounce the ball is moving up; skip remaining substeps
        break;
      }

      // bricks
      if (this.state === 'playing') {
        this._collideBricks(b);
      }
    }
  }

  _bounceSfx(b) {
    if (this.state === 'playing') {
      const sp = Math.hypot(b.vx, b.vy);
      this.events.push({ type: 'bounce', x: b.x, y: b.y, wall: 0, speed01: Math.min(1, sp / MAX_BALL_SPEED) });
    }
  }

  _paddleBounce(b) {
    const p = this.paddle;
    const rel = clamp((b.x - p.x) / (p.w / 2), -1, 1);
    const maxAngle = 1.08; // ~62 degrees from vertical
    const speed = Math.max(MIN_BALL_SPEED, Math.hypot(b.vx, b.vy));
    // upward paddle motion adds "a bit more acceleration" (the whole point of moving up)
    const boost = clamp(-p.vy * 0.85, 0, 300);
    const fireMod = this.powerTimers.fire > 0 ? 1.08 : 1;
    let ns = speed * (1 + boost / 2400) * fireMod + boost * 0.12;
    ns = Math.min(b.fire ? MAX_FIRE_SPEED : MAX_BALL_SPEED, ns);
    ns = Math.max(ns, speed);
    const angle = rel * maxAngle;
    // direction: up, tilted by paddle-relative offset, plus boost pulling it
    // even more vertical (reward for moving up when you hit)
    let dirX = Math.sin(angle);
    let dirY = -Math.cos(angle);
    if (boost > 30) {
      const lift = Math.min(0.45, boost / 700);
      dirY = dirY - lift; // bias the launch more vertical
      const len = Math.hypot(dirX, dirY) || 1;
      dirX /= len; dirY /= len;
    }
    b.vx = dirX * ns + p.vx * 0.06;
    b.vy = dirY * ns;
    b.vy -= boost * 0.35;
    // renormalize to the target speed (the boost has already been folded into ns,
    // so this keeps a consistent speed while preserving the vertical bias)
    const l = Math.hypot(b.vx, b.vy) || 1;
    b.vx *= ns / l;
    b.vy *= ns / l;
    // safety: never leave the ball moving down off the paddle
    if (b.vy > -40) b.vy = -40;
    const yy = p.y - p.h / 2 - b.r - 0.5;
    if (b.y > yy) b.y = yy;
    this.events.push({ type: 'paddleHit', speed01: Math.min(1, ns / MAX_BALL_SPEED), boost });
  }

  _cellAt(px, py) {
    const c = Math.floor((px - BRICK_LEFT) / BRICK_CELL_W);
    const r = Math.floor((py - BRICK_TOP) / BRICK_CELL_H);
    if (c < 0 || c >= COLS || r < 0 || r >= this.bricks.length) return null;
    return this.bricks[r][c];
  }

  _collideBricks(b) {
    // circle-vs-AABB over the cells overlapping the ball's bounding box
    const rMin = Math.floor((b.y - b.r - BRICK_TOP) / BRICK_CELL_H);
    const rMax = Math.floor((b.y + b.r - BRICK_TOP) / BRICK_CELL_H);
    const cMin = Math.floor((b.x - b.r - BRICK_LEFT) / BRICK_CELL_W);
    const cMax = Math.floor((b.x + b.r - BRICK_LEFT) / BRICK_CELL_W);
    for (let r = rMin; r <= rMax; r++) {
      if (r < 0 || r >= this.bricks.length) continue;
      for (let c = cMin; c <= cMax; c++) {
        if (c < 0 || c >= COLS) continue;
        const bk = this.bricks[r][c];
        if (!bk.alive) continue;
        const hit = circleRectHit(b.x, b.y, b.r, bk.x, bk.y, bk.w, bk.h);
        if (!hit) continue;
        this._collideBrick(b, bk, hit);
      }
    }
  }

  _collideBrick(b, bk, hit) {
    const type = bk.type;
    const speed = Math.hypot(b.vx, b.vy) || 1;
    const fire = b.fire === true;

    if (fire && FIRE_BREAKABLE.has(type)) {
      // pierce: destroy without reflecting
      this._destroyBrick(bk, { fire: true });
      return;
    }

    // gate types: reject without breaking (deflect only)
    if (type === 'a') {
      const steep = Math.abs(b.vy) / speed >= 0.55;
      if (!steep) { this._reject(b, bk, hit); bk.flash = 0.18; return; }
    } else if (type === 's') {
      const need = speedThreshold(this.levelIndex, this.levelNumber);
      if (speed < need) { this._reject(b, bk, hit); bk.flash = 0.18; return; }
    } else if (type === 't') {
      if (hit.face !== 'top' || b.vy <= 0) { this._reject(b, bk, hit); bk.flash = 0.18; return; }
    } else if (type === 'g') {
      // gold: deflect + juice
      this._deflect(b, hit);
      const sp = Math.hypot(b.vx, b.vy);
      const ns = Math.min(MAX_BALL_SPEED, sp * 1.035);
      const l = Math.hypot(b.vx, b.vy) || 1;
      b.vx *= ns / l; b.vy *= ns / l;
      if (b.vy > -60 && hit.face === 'bottom') b.vy = -Math.abs(b.vy) * 1.02;
      this.events.push({ type: 'goldTing', x: bk.x + bk.w / 2, y: bk.y + bk.h / 2, speed: sp });
      return;
    } else if (type === 'r') {
      bk.hits++;
      bk.regrow = 2.2;
      if (bk.hits >= 2) {
        this._destroyBrick(bk, {});
      } else {
        bk.flash = 0.15;
        this.events.push({ type: 'brickCrack', row: bk.row, col: bk.col });
        this._deflect(b, hit);
      }
      return;
    }

    // standard destroyable hit
    this._destroyBrick(bk, {});
    this._deflect(b, hit);
  }

  _reject(b, bk, hit) {
    this._deflect(b, hit);
    this.events.push({
      type: 'brickReject', x: bk.x + bk.w / 2, y: bk.y + bk.h / 2,
      brickType: bk.type, row: bk.row, col: bk.col,
      speed: Math.hypot(b.vx, b.vy),
    });
  }

  _deflect(b, hit) {
    if (b.fire === true) return; // piercing balls aren't deflected here
    const { face, nx, ny } = hit;
    // push ball out
    const overlap = hit.depth;
    b.x += nx * overlap;
    b.y += ny * overlap;
    if (face === 'left' || face === 'right') {
      b.vx = -b.vx;
      // give a tiny nudge along the brick so it doesn't get stuck
      b.vy += (this.rng() - 0.5) * 12;
    } else if (face === 'top' || face === 'bottom') {
      b.vy = -b.vy;
      b.vx += (this.rng() - 0.5) * 12;
    } else {
      // corner: flip the dominant axis of velocity
      if (Math.abs(b.vx) > Math.abs(b.vy)) b.vx = -b.vx; else b.vy = -b.vy;
    }
  }

  _destroyBrick(bk, opts) {
    if (!bk.alive) return;
    bk.alive = false;
    if (BREAKABLE.has(bk.type)) this.bricksAlive--;
    this.destroyedThisFrame.push(bk);

    // combo / multiplier
    const now = this.frame / 120;
    if (now - this.lastBreakTime > COMBO_WINDOW) this.combo = 0;
    this.combo++;
    this.lastBreakTime = now;
    const newMult = Math.min(MAX_MULT, 1 + Math.floor(this.combo / MULT_PER));
    if (newMult > this.mult) {
      this.mult = newMult;
      this.events.push({ type: 'comboUp', mult: newMult, combo: this.combo, x: bk.x + bk.w / 2, y: bk.y + bk.h / 2 });
    }

    let pts = (BASE_SCORE[bk.type] || 10) * this.mult;
    let crownBonus = 0;
    if (bk.type === 'c' && this.rooftop) {
      crownBonus = 50 * (this.rooftopStreak + 1);
      pts += crownBonus;
    }
    this.score += pts;
    this.waveBricksDestroyed++;
    this.events.push({
      type: 'brickBreak', x: bk.x + bk.w / 2, y: bk.y + bk.h / 2,
      brickType: bk.type, row: bk.row, col: bk.col,
      pts, crownBonus, mult: this.mult, combo: this.combo, fire: !!opts.fire,
    });

    if (bk.type === 'b' || opts.chain) {
      this._explode(bk, 0, opts.chainFrom || null);
    }

    // power-up drop
    const chance = DROP_CHANCE[bk.type] || 0;
    let roll = this.rng();
    if (chance >= 1 || roll < chance) {
      const ptype = this._pickPower();
      if (ptype) {
        const drop = {
          type: ptype,
          x: bk.x + bk.w / 2,
          y: bk.y + bk.h / 2,
          vy: DROP_SPEED,
          t: 0,
          dead: false,
        };
        this.drops.push(drop);
        this.events.push({ type: 'dropSpawn', power: ptype, x: drop.x, y: drop.y });
      }
    }
    this._recomputeRoof();
  }

  _explode(bk, depth, from) {
    const r0 = bk.row, c0 = bk.col;
    const cx = bk.x + bk.w / 2, cy = bk.y + bk.h / 2;
    this.events.push({ type: 'explosion', x: cx, y: cy, row: r0, col: c0, depth });
    // blast neighbours
    for (let r = 0; r < this.bricks.length; r++) {
      for (let c = 0; c < COLS; c++) {
        const other = this.bricks[r][c];
        if ((r === r0 && c === c0) || !other.alive) continue;
        if (other.type === 'g') continue;
        const ox = other.x + other.w / 2, oy = other.y + other.h / 2;
        const dcells = Math.hypot((ox - cx) / BRICK_CELL_W, (oy - cy) / BRICK_CELL_H);
        if (dcells > EXPLOSION_RADIUS) continue;
        if (other.type === 'b') {
          this._destroyBrick(other, { chain: true, chainFrom: bk });
        } else {
          this._destroyBrick(other, {});
        }
      }
    }
  }

  _pickPower() {
    // avoid duplicates currently active/falling
    const exclude = new Set();
    if (this.powerTimers.wide > 0) exclude.add('wide');
    if (this.powerTimers.laser > 0) exclude.add('laser');
    if (this.powerTimers.slow > 0) exclude.add('slow');
    if (this.powerTimers.fire > 0) exclude.add('fire');
    for (const d of this.drops) exclude.add(d.type);
    if (this.lives >= 4) exclude.add('extraLife');
    let total = 0;
    for (const k of Object.keys(DROP_WEIGHTS)) if (!exclude.has(k)) total += DROP_WEIGHTS[k];
    if (total === 0) return null;
    let r = this.rng() * total;
    for (const k of Object.keys(DROP_WEIGHTS)) {
      if (exclude.has(k)) continue;
      r -= DROP_WEIGHTS[k];
      if (r <= 0) return k;
    }
    return Object.keys(DROP_WEIGHTS)[0];
  }

  _recomputeRoof() {
    let top = Infinity;
    for (const row of this.bricks) {
      for (const bk of row) {
        if (bk.alive) top = Math.min(top, bk.y);
      }
    }
    this.roofTopY = top === Infinity ? BRICK_TOP : top;
  }

  _updateLasers(dt) {
    const p = this.paddle;
    if (this.powerTimers.laser > 0 && this.state === 'playing') {
      p.laserTimer -= dt;
      if (p.laserTimer <= 0) {
        p.laserTimer = LASER_INTERVAL;
        const lx = p.x - p.w * 0.30, rx = p.x + p.w * 0.30;
        const ly = p.y - p.h / 2 - 18;
        this.lasers.push({ x: lx, y: ly, w: 3.5, h: 16 });
        this.lasers.push({ x: rx, y: ly, w: 3.5, h: 16 });
        this.events.push({ type: 'laserFire', x: lx, y: ly });
        this.events.push({ type: 'laserFire', x: rx, y: ly });
      }
    } else {
      this.lasers = [];
    }
    for (let i = this.lasers.length - 1; i >= 0; i--) {
      const l = this.lasers[i];
      l.y -= LASER_SPEED * dt; // lasers travel upward
      if (l.y + l.h < 0) { this.lasers.splice(i, 1); continue; }
      // collide with bricks along the beam span
      const y0 = l.y, y1 = l.y + l.h;
      let hit = null;
      if (l.y > BRICK_TOP) {
        const bkr = this._cellAt(l.x, l.y + l.h / 2);
        if (bkr) hit = bkr;
      } else {
        const r0 = Math.floor((y0 - BRICK_TOP) / BRICK_CELL_H);
        const r1 = Math.floor((y1 - BRICK_TOP) / BRICK_CELL_H);
        for (let r = Math.max(0, r0); r <= r1 && r < this.bricks.length; r++) {
          const cand = this.bricks[r][Math.floor((l.x - BRICK_LEFT) / BRICK_CELL_W)];
          if (cand && cand.alive && LASER_BREAKABLE.has(cand.type)) { hit = cand; break; }
        }
      }
      if (hit && hit.alive && LASER_BREAKABLE.has(hit.type)) {
        this._destroyBrick(hit, {});
        this.lasers.splice(i, 1);
        continue;
      }
      // floor safety: remove beams that sank below the playfield
      if (l.y > WORLD_H + 20) { this.lasers.splice(i, 1); continue; }
    }
  }

  _updateDrops(dt) {
    const p = this.paddle;
    for (let i = this.drops.length - 1; i >= 0; i--) {
      const d = this.drops[i];
      d.t += dt;
      d.y += d.vy * dt;
      if (d.y > WORLD_H + 24) { this.drops.splice(i, 1); continue; }
      // catch check (generous box around paddle)
      const px = p.x, py = p.y, hw = p.w / 2 + 10, hh = p.h / 2 + 12;
      if (d.x >= px - hw && d.x <= px + hw && d.y >= py - hh && d.y <= py + hh) {
        this.drops.splice(i, 1);
        this._applyPower(d.type);
      }
    }
    if (this.drops.length > 6) { // cap active drops
      const oldest = this.drops.reduce((a, b) => a.t < b.t ? a : b);
      this.drops.splice(this.drops.indexOf(oldest), 1);
    }
  }

  _applyPower(type) {
    const p = this.paddle;
    this.events.push({ type: 'catch', power: type, x: p.x, y: p.y - p.h / 2 });
    switch (type) {
      case 'multiball': {
        const clones = [];
        for (const b of this.balls) {
          if (b.dead) continue;
          const count = this.balls.length + clones.length + 1 - 1;
          const canAdd = this.balls.length + clones.length < MAX_BALLS;
          if (!canAdd) break;
          for (const s of [-1, 1]) {
            const ang = Math.atan2(b.vy, b.vx);
            const sp = Math.hypot(b.vx, b.vy);
            const a2 = ang + s * 0.42;
            clones.push({
              x: b.x, y: b.y, r: b.r,
              vx: Math.cos(a2) * sp, vy: Math.sin(a2) * sp,
              fire: b.fire, dead: false,
            });
          }
        }
        this.balls.push(...clones);
        this.events.push({ type: 'multiball' });
        break;
      }
      case 'wide':
        this.powerTimers.wide = 14;
        p.w = Math.min(PADDLE_W * 1.45, 122);
        break;
      case 'laser':
        this.powerTimers.laser = 10;
        p.laserTimer = 0;
        break;
      case 'slow':
        // slow: cut every ball's current speed; engine caps speed at 380 while active
        this.powerTimers.slow = 8;
        for (const b of this.balls) {
          const s = Math.hypot(b.vx, b.vy) || 1;
          if (s > 380) { const f = 380 / s; b.vx *= f; b.vy *= f; }
        }
        break;
      case 'fire':
        this.powerTimers.fire = 8;
        for (const b of this.balls) if (!b.dead) b.fire = true;
        break;
      case 'shield':
        p.shield = Math.min(3, p.shield + 1);
        break;
      case 'extraLife':
        this.lives = Math.min(4, this.lives + 1);
        break;
    }
  }

  _updateRooftop(dt) {
    if (this.state !== 'playing') return;
    // is any ball above the top of the highest brick?
    const anyBall = this.balls.some(b => !b.dead && b.y + b.r <= this.roofTopY + 2);
    if (anyBall && this.bricksAlive > 0) {
      if (!this.rooftop) {
        this.rooftop = true;
        this.rooftopTimer = 0;
        this.rooftopStreak = 0;
        this.events.push({ type: 'rooftopEnter' });
      }
      this.rooftopTimer += dt;
      const streak = Math.floor(this.rooftopTimer / 0.55);
      if (streak > this.rooftopStreak) {
        this.rooftopStreak = streak;
        this.bestStreak = Math.max(this.bestStreak, streak);
        const pts = 5 * this.mult;
        this.score += pts;
        this.rooftopTextTimer = 0;
        this.events.push({ type: 'rooftopTick', streak, pts, x: this.balls.find(b => !b.dead).x, y: this.balls.find(b => !b.dead).y });
      }
      if (this.rooftopTextTimer > 0) this.rooftopTextTimer -= dt;
    } else {
      if (this.rooftop) {
        this.rooftop = false;
        this.rooftopTimer = 0;
        this.events.push({ type: 'rooftopExit' });
      }
    }
  }

  _lifeLost() {
    if (this.rooftop) {
      this.rooftop = false;
      this.rooftopTimer = 0;
    }
    if (this.paddle.shield > 0) {
      // shouldn't happen (shield intercepts), but guard anyway
      this.paddle.shield = 0;
      return;
    }
    this.lives--;
    this.combo = 0;
    this.mult = 1;
    this.events.push({ type: 'lifeLost', lives: this.lives });
    if (this.lives <= 0) {
      this.state = 'over';
      this.events.push({ type: 'gameOver', score: this.score });
    } else {
      // re-serve; keep the level intact
      this.serveBall();
    }
  }

  get ballsAlive() { return this.balls.length; }
}

// -- geometry helpers -------------------------------------------------------
function circleRectHit(cx, cy, r, rx, ry, rw, rh) {
  const nx = cx < rx ? rx : cx > rx + rw ? rx + rw : cx;
  const ny = cy < ry ? ry : cy > ry + rh ? ry + rh : cy;
  const dx = cx - nx, dy = cy - ny;
  const d2 = dx * dx + dy * dy;
  if (d2 >= r * r) return null;
  const depth = r - Math.sqrt(d2);
  // face: compare distances to each side plane
  const distL = cx - (rx - r), distR = (rx + rw + r) - cx;
  const distT = cy - (ry - r), distB = (ry + rh + r) - cy;
  const m = Math.min(distL, distR, distT, distB);
  if (m === distT) return { face: 'top', nx: 0, ny: -1, depth };
  if (m === distB) return { face: 'bottom', nx: 0, ny: 1, depth };
  if (m === distL) return { face: 'left', nx: -1, ny: 0, depth };
  return { face: 'right', nx: 1, ny: 0, depth };
}

function stateMoving(b) {
  return Math.hypot(b.vx, b.vy) > 1;
}

export function clamp(v, a, b) { return v < a ? a : v > b ? b : v; }

// export geometry helper for tests
export function testCircleRect(cx, cy, r, rx, ry, rw, rh) {
  return circleRectHit(cx, cy, r, rx, ry, rw, rh);
}
