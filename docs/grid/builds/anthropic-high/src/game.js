// game.js — state machine, level flow, scoring, OVERTOP tracking, power-ups.

import { clamp, damp, rand, pick, chance, storage, setLength, sign } from './util.js';
import { moveBall, paddleBounce, avoidDegenerate, resolveMovingOverlap } from './physics.js';
import { Ball } from './entities/ball.js';
import { Paddle } from './entities/paddle.js';
import { buildLevel, countBreakable, topOfBricks, neighborsOf, BRICK_KINDS } from './entities/bricks.js';
import { getLevel } from './levels.js';
import fx from './fx.js';
import audio from './audio.js';

export const W = 540;
export const H = 960;
export const FIELD = { left: 0, top: 92, right: W, bottom: H };

export const OVERTOP_MARGIN = 16;
// Hysteresis: you go OVERTOP by climbing above the brick tops, and you only lose it by
// dropping properly back down into the field. Skimming along the roof, dipping to smash
// the top row and popping back up all counts as one continuous overtop run.
export const OVERTOP_EXIT = 58;
export const MULT_CAP = 10;

/** Anti-frustration tuning. */
export const ASSIST_AFTER = 20;      // s with <= 2 bricks left before homing help
export const DOWNGRADE_AFTER = 45;   // s without a kill before condition bricks soften
export const COYOTE_PX = 6;          // ball-save window past the paddle edge
const CONDITION_KINDS = new Set(['angle', 'angleH', 'speed', 'slow', 'ghost', 'topOnly']);


export const POWERUPS = {
  multiball: { label: 'MULTIBALL', hue: 195, glyph: '⁘', weight: 12, desc: 'three balls' },
  fireball: { label: 'FIREBALL', hue: 22, glyph: '✹', weight: 9, desc: 'plow through' },
  heavy: { label: 'HEAVY BALL', hue: 280, glyph: '●', weight: 8, desc: 'big + splash' },
  ghostball: { label: 'GHOST BALL', hue: 250, glyph: '◌', weight: 8, desc: 'rise through bricks' },
  magnet: { label: 'MAGNET', hue: 330, glyph: '⊂', weight: 7, desc: 'catch & aim' },
  wide: { label: 'WIDE PADDLE', hue: 140, glyph: '▬', weight: 10, desc: '1.6x paddle' },
  slowmo: { label: 'SLOW-MO', hue: 200, glyph: '◐', weight: 7, desc: 'time 0.6x' },
  laser: { label: 'LASER', hue: 0, glyph: '↑', weight: 9, desc: 'tap to fire' },
  life: { label: 'EXTRA LIFE', hue: 50, glyph: '♥', weight: 4, desc: '+1 life' },
};

const POWER_KEYS = Object.keys(POWERUPS);
const POWER_BAG = POWER_KEYS.flatMap((k) => Array(POWERUPS[k].weight).fill(k));

/** How long each timed power-up lasts. Re-picking one refreshes it to this value. */
export const POWER_DURATION = {
  fireball: 12, heavy: 12, ghostball: 12, magnet: 14, wide: 12, slowmo: 6, laser: 9,
};
/** Ball modifiers that cannot coexist — picking one cancels the others. */
const BALL_MODS = { fireball: 'fire', heavy: 'heavy', ghostball: 'ghost' };
/** power key on `game.powers` -> POWERUPS key, for the HUD. */
const POWER_STATE_KEYS = [
  ['fire', 'fireball'], ['heavy', 'heavy'], ['ghost', 'ghostball'],
  ['magnet', 'magnet'], ['wide', 'wide'], ['laser', 'laser'],
];
// When the player is on their last life, the bag is re-weighted toward rescue items.
const HELPFUL_WEIGHTS = {
  life: 16, multiball: 15, wide: 15, magnet: 11, slowmo: 10, laser: 8,
  ghostball: 7, fireball: 6, heavy: 3,
};
const POWER_BAG_HELP = POWER_KEYS.flatMap((k) => Array(HELPFUL_WEIGHTS[k] || 1).fill(k));

/** One-line teaching text per brick kind, shown the first time you meet one. */
export const BRICK_HINTS = {
  normal: { title: 'NORMAL BRICK', text: 'Breaks on any hit. Kills without touching the paddle build a COMBO.' },
  boost: { title: 'LAUNCH PAD', text: 'Hit it from BELOW and it slings the ball at the roof.' },
  angle: { title: 'PRISM ▼', text: 'Breaks only from a STEEP angle — come at it from above or below.' },
  angleH: { title: 'PRISM ▶', text: 'Breaks only from a SHALLOW angle — skim it sideways.' },
  speed: { title: 'ARMOR', text: 'Only a FAST ball cracks it. Rise into the ball to SMASH.' },
  slow: { title: 'GLASS', text: 'Only a SLOW ball shatters it. Drop the paddle away to soften your shot.' },
  ghost: { title: 'PHASE', text: 'Solid, then transparent. A timing gate — shoot it on the solid beat.' },
  topOnly: { title: 'CAP', text: 'Breaks only from ABOVE. This is what going OVERTOP is for.' },
  bomb: { title: 'BOMB', text: 'Takes out every neighbour, conditions and all. Chains into other bombs.' },
  powerup: { title: 'CAPSULE', text: 'Drops a power-up. Catch it with the paddle.' },
  steel: { title: 'STEEL', text: 'Indestructible. Bank shots off it — it never blocks a level clear.' },
};

export const CAPSULE_FALL_SPEED = 104;   // slower than a ball, catchable on a phone
export const CAPSULE_DRIFT = 58;         // px/s of drift toward a nearby paddle
export const EXTRA_LIFE_EVERY = 25000;

export class Capsule {
  constructor(x, y, type) {
    this.x = x; this.y = y;
    this.w = 34; this.h = 18;
    this.vy = CAPSULE_FALL_SPEED;
    this.type = type;
    this.dead = false;
    this.t = 0;
  }
  update(dt, paddle) {
    this.t += dt;
    this.y += this.vy * dt;
    // Drift toward the paddle when it is close: on a phone you cannot always reach.
    if (paddle) {
      const dx = paddle.cx - (this.x + this.w / 2);
      const ad = Math.abs(dx);
      if (ad > 1 && ad < 150) {
        const k = 1 - ad / 150;
        this.x += sign(dx) * Math.min(ad, CAPSULE_DRIFT * k * dt);
      }
    }
  }
}

export class Bolt {
  constructor(x, y) {
    this.x = x; this.y = y;
    this.w = 4; this.h = 16;
    this.vy = -1050;
    this.dead = false;
  }
}

export class Game {
  constructor() {
    this.field = FIELD;
    this.w = W;
    this.h = H;
    this.paddle = new Paddle(FIELD);
    this.balls = [];
    this.bricks = [];
    this.capsules = [];
    this.bolts = [];
    this.activeBricks = [];

    this.state = 'title'; // title | serve | playing | levelclear | paused | gameover
    this.prevState = 'title';
    this.score = 0;
    this.lives = 3;
    this.level = 1;
    this.levelName = '';
    this.highScore = storage.get('overtop.highscore', 0) || 0;
    this.newHighScore = false;

    this.multiplier = 1;
    this.displayMult = 1;
    this.multPulse = 0;
    this.overtop = false;
    this.overtopTime = 0;
    this.overtopStreak = 0;
    this.offTopTimer = 0;
    this.bestOvertop = 0;
    this.ceilingCombo = 0;
    this.intensity = 0;

    this.timeScale = 1;
    this.overtopSlowmo = 0;
    this.streakDecay = 0;
    this.brickTop = Infinity;
    this.slowmoTimer = 0;
    this.slowmoScale = 1;
    this.hitstop = 0;
    this.time = 0;
    this.realTime = 0;

    this.powers = { fire: 0, heavy: 0, ghost: 0, magnet: 0, wide: 0, slowmo: 0, laser: 0 };
    this.laserCooldown = 0;

    this.banner = null;      // {title, sub, t, life, hue}
    this.toast = null;       // small power-up banner
    this.hint = null;        // {title, text, kind, t, life} — first-encounter brick lesson
    this.hintQueue = [];
    this.hintGap = 0;
    this.levelIntro = null;  // {name, level, newKinds:[kind], t, life}
    this.seenKinds = new Set(storage.get('overtop.seenKinds', []) || []);
    this.activePowers = [];  // [{type, remaining, duration}] for the HUD
    this.levelClearTimer = 0;
    this.levelBonus = 0;     // last level-clear bonus, shown on the level-clear card
    this.minSpeed = 320;
    this.maxSpeed = 900;
    this.cmd = { mode: 'drive', x: W / 2, y: 0, dx: 0, dy: 0 };
    this.combo = 0;          // brick kills without touching the paddle
    this.comboBest = 0;
    this.nextLifeAt = EXTRA_LIFE_EVERY;
    this.stallTimer = 0;     // seconds since the last brick died
    this.fewTimer = 0;       // seconds spent with <= 2 breakable bricks left
    this.assist = false;     // last-brick helper active
    this.assistBricks = [];
    this.assistPulse = 0;
    this.remainingBreakable = 0;
    this.stats = this.freshStats();
    this.dragMode = storage.get('overtop.dragMode', false) || false;
    this.muted = storage.get('overtop.muted', false) || false;

    // Decorative backdrop for the title screen.
    this.bricks = buildLevel(getLevel(1).rows, FIELD);
    for (const b of this.bricks) b.spawnT = 1;
  }

  freshStats() {
    return {
      bricksBroken: 0,
      overtopBricks: 0,
      overtopTotal: 0,     // total seconds spent overtop this run
      longestOvertop: 0,   // longest single overtop run, seconds
      maxMultiplier: 1,
      bestCombo: 0,
      saves: 0,            // coyote ball-saves
      powerups: 0,
      levelsCleared: 0,
      time: 0,             // seconds of actual play
    };
  }

  // ---------------------------------------------------------------- flow

  startGame() {
    this.score = 0;
    this.lives = 3;
    this.level = 1;
    this.newHighScore = false;
    this.nextLifeAt = EXTRA_LIFE_EVERY;
    this.bestOvertop = 0;
    this.stats = this.freshStats();
    fx.clear();
    this.loadLevel(1);
    audio.play('ui');
  }

  loadLevel(n) {
    const def = getLevel(n);
    this.level = n;
    this.levelName = def.name;
    this.bricks = buildLevel(def.rows, FIELD);
    this.capsules = [];
    this.bolts = [];
    this.clearPowers();
    this.paddle.setWide(false);
    // Speed floor creeps up with the level but stays under the 'fast' tier line (470)
    // so Glass bricks remain breakable at any level.
    this.minSpeed = Math.min(440, 380 + (n - 1) * 7);
    this.maxSpeed = 900;
    this.combo = 0;
    this.stallTimer = 0;
    this.fewTimer = 0;
    this.assist = false;
    this.assistBricks = [];
    this.resetMultiplier();
    this.serve();

    // Which brick kinds does this level teach?
    const kinds = [];
    for (const b of this.bricks) if (!kinds.includes(b.kind)) kinds.push(b.kind);
    const newKinds = kinds.filter((k) => !this.seenKinds.has(k));
    // `levelIntro` IS the level-start banner (render draws it); no second showBanner.
    this.levelIntro = { name: def.name, level: n, newKinds, t: 2.8, life: 2.8 };
    this.banner = null;
    this.hint = null;
    this.hintQueue = newKinds.slice();
    this.hintGap = 3.0;      // let the level banner read first
  }


  nextLevel() {
    this.loadLevel(this.level + 1);
    audio.play('levelup');
  }

  serve() {
    this.state = 'serve';
    this.combo = 0;
    const b = new Ball(this.paddle.cx, this.paddle.y - 14);
    b.stuck = true;
    b.stickOffset = 0;
    b.stickTimer = 2.0;
    this.balls = [b];
    this.syncBallPowers();
  }

  gameOver() {
    this.state = 'gameover';
    this.stats.longestOvertop = Math.max(this.stats.longestOvertop, this.bestOvertop);
    this.stats.bestCombo = Math.max(this.stats.bestCombo, this.comboBest);
    if (this.score > this.highScore) {
      this.highScore = Math.floor(this.score);
      this.newHighScore = true;
      storage.set('overtop.highscore', this.highScore);
    }
    audio.play('gameover');
    fx.flash(0, 0.35, 0.6);
    fx.shake(14, 0.5);
  }

  togglePause() {
    if (this.state === 'paused') {
      this.state = this.prevState || 'playing';
      audio.play('ui');
    } else if (this.state === 'playing' || this.state === 'serve' || this.state === 'levelclear') {
      this.prevState = this.state;
      this.state = 'paused';
      audio.play('ui');
    }
  }

  /** Primary action: start / restart / launch / fire. */
  onFire() {
    audio.unlock();
    if (this.state === 'title') { this.startGame(); return; }
    if (this.state === 'gameover') { this.startGame(); return; }
    if (this.state === 'paused') { this.togglePause(); return; }
    if (this.state === 'serve') { this.launch(); return; }
    if (this.state === 'playing') {
      let launched = false;
      for (const b of this.balls) if (b.stuck) { this.launchBall(b); launched = true; }
      if (!launched && this.powers.laser > 0) this.fireLaser();
    }
  }

  launch() {
    for (const b of this.balls) this.launchBall(b);
    this.state = 'playing';
  }

  launchBall(b) {
    if (!b.stuck) return;
    b.stuck = false;
    const aim = clamp((this.paddle.vx || 0) / 900, -1, 1) * 0.5 + b.stickOffset / (this.paddle.w * 0.9);
    const angle = clamp(aim, -0.7, 0.7) + rand(-0.06, 0.06);
    const speed = this.minSpeed * 1.12;
    b.vx = Math.sin(angle) * speed;
    b.vy = -Math.cos(angle) * speed;
    b.y = this.paddle.y - b.r - 2;
    audio.play('launch');
    fx.sparks(b.x, b.y, 8, 190, 160);
    if (this.state === 'serve') this.state = 'playing';
  }

  fireLaser() {
    if (this.laserCooldown > 0) return;
    this.laserCooldown = 0.17;
    const p = this.paddle;
    this.bolts.push(new Bolt(p.x + 8, p.y - 4));
    this.bolts.push(new Bolt(p.x + p.w - 12, p.y - 4));
    audio.play('laser');
    p.flash = 1;
  }

  showBanner(title, sub, hue = 190, life = 1.8) {
    this.banner = { title, sub, hue, t: 0, life };
  }
  showToast(text, hue = 140) {
    this.toast = { text, hue, t: 0, life: 1.5 };
  }

  /**
   * First-encounter brick lesson. `game.hint = {title, text, kind, t, life}` where `t`
   * counts DOWN to zero (render can fade with t/life). Gameplay never pauses for it.
   */
  showHint(kind) {
    const h = BRICK_HINTS[kind];
    if (!h) return;
    this.hint = { title: h.title, text: h.text, kind, t: 3.5, life: 3.5 };
    this.markSeen(kind);
    audio.play('ui');
  }

  markSeen(kind) {
    if (this.seenKinds.has(kind)) return;
    this.seenKinds.add(kind);
    this.hintQueue = this.hintQueue.filter((k) => k !== kind);
    storage.set('overtop.seenKinds', [...this.seenKinds]);
  }

  /** Forget the tutorial state (used by the settings screen / tests). */
  resetHints() {
    this.seenKinds = new Set();
    storage.set('overtop.seenKinds', []);
  }

  setPaddleCommand(cmd) { this.cmd = cmd; }


  // ---------------------------------------------------------------- powers

  clearPowers() {
    for (const k of Object.keys(this.powers)) this.powers[k] = 0;
    this.paddle.setWide(false);
    this.slowmoTimer = 0;
    this.slowmoScale = 1;
    this.syncBallPowers();
    this.refreshActivePowers();
  }

  syncBallPowers() {
    for (const b of this.balls) {
      b.fire = this.powers.fire;
      b.heavy = this.powers.heavy;
      b.ghost = this.powers.ghost;
      b.magnet = this.powers.magnet;
      const targetR = this.powers.heavy > 0 ? 12 : b.baseR;
      b.r = damp(b.r, targetR, 0.0001, 1 / 60);
      if (Math.abs(b.r - targetR) < 0.2) b.r = targetR;
    }
  }

  get ballMaxSpeed() {
    return this.powers.heavy > 0 ? 640 : this.maxSpeed;
  }

  randomPowerType() {
    // On the last life the bag tilts toward rescue items (extra life, wide, multiball).
    return pick(this.lives <= 1 ? POWER_BAG_HELP : POWER_BAG);
  }

  applyPower(type) {
    const def = POWERUPS[type];
    if (!def) return;
    this.stats.powerups++;
    audio.play(type === 'multiball' ? 'multiball' : 'powerup');
    this.showToast(def.label, def.hue);
    fx.flash(def.hue, 0.22, 0.3);
    fx.shake(4, 0.18);
    // Ball modifiers are mutually exclusive: a new one replaces the old one outright.
    if (BALL_MODS[type]) {
      for (const [k, flag] of Object.entries(BALL_MODS)) {
        if (k !== type) this.powers[flag] = 0;
      }
    }
    const dur = POWER_DURATION[type] || 0;
    switch (type) {
      case 'multiball': {
        const spawn = [];
        for (const b of this.balls) {
          if (this.balls.length + spawn.length >= 9) break;
          for (const a of [-0.42, 0.42]) {
            const nb = new Ball(b.x, b.y);
            const s = Math.max(this.minSpeed, Math.hypot(b.vx, b.vy) || this.minSpeed);
            const base = Math.atan2(b.vy, b.vx || 0.001) + a;
            nb.vx = Math.cos(base) * s;
            nb.vy = Math.sin(base) * s;
            nb.stuck = false;
            spawn.push(nb);
          }
        }
        this.balls.push(...spawn);
        break;
      }
      case 'fireball': this.powers.fire = dur; break;
      case 'heavy': this.powers.heavy = dur; break;
      case 'ghostball': this.powers.ghost = dur; break;
      case 'magnet': this.powers.magnet = dur; break;
      case 'wide': this.powers.wide = dur; this.paddle.setWide(true); break;
      case 'slowmo': this.slowmoTimer = dur; this.slowmoScale = 0.6; break;
      case 'laser': this.powers.laser = dur; break;
      case 'life': this.lives++; break;
      default: break;
    }
    this.syncBallPowers();
    this.refreshActivePowers();
  }

  /** `game.activePowers = [{type, remaining, duration}]` — the HUD's source of truth. */
  refreshActivePowers() {
    const out = [];
    for (const [flag, type] of POWER_STATE_KEYS) {
      if (this.powers[flag] > 0) {
        out.push({ type, remaining: this.powers[flag], duration: POWER_DURATION[type] || 12 });
      }
    }
    if (this.slowmoTimer > 0) {
      out.push({ type: 'slowmo', remaining: this.slowmoTimer, duration: POWER_DURATION.slowmo });
    }
    this.activePowers = out;
    return out;
  }

  updatePowerTimers(dt) {
    for (const k of Object.keys(this.powers)) {
      if (this.powers[k] > 0) {
        this.powers[k] = Math.max(0, this.powers[k] - dt);
        if (this.powers[k] === 0 && k === 'wide') this.paddle.setWide(false);
      }
    }
    if (this.slowmoTimer > 0) {
      this.slowmoTimer = Math.max(0, this.slowmoTimer - dt);
      if (this.slowmoTimer === 0) this.slowmoScale = 1;
    }
    if (this.laserCooldown > 0) this.laserCooldown -= dt;
    this.syncBallPowers();
    this.refreshActivePowers();
  }


  // ---------------------------------------------------------------- scoring / overtop

  resetMultiplier() {
    this.multiplier = 1;
    this.displayMult = 1;
    this.overtopTime = 0;
    this.overtopStreak = 0;
    this.offTopTimer = 0;
    this.ceilingCombo = 0;
    this.overtop = false;
  }

  computeMultiplier() {
    const m = 1 + Math.floor(this.overtopTime / 2) + Math.floor(this.overtopStreak / 3);
    return clamp(m, 1, MULT_CAP);
  }

  addScore(pts) {
    this.score += pts;
    while (this.score >= this.nextLifeAt) {
      this.nextLifeAt += EXTRA_LIFE_EVERY;
      this.lives++;
      audio.play('powerup');
      // One notice only: the toast. (There used to be a floating fx.text here too.)
      fx.flash(50, 0.25, 0.4);
      this.showToast('EXTRA LIFE', 50);
    }
    if (this.score > this.highScore) {
      this.highScore = Math.floor(this.score);
      this.newHighScore = true;
      storage.set('overtop.highscore', this.highScore);
    }
  }

  triggerOvertop() {
    // THE moment. Fanfare + flash + a slice of slow motion.
    audio.play('overtop');
    fx.flash(190, 0.4, 0.45);
    fx.shake(7, 0.3);
    this.overtopSlowmo = 0.25;
    this.showToast('OVERTOP!', 190);
    for (const b of this.balls) {
      if (!b.overtop) continue;
      fx.ring(b.x, b.y, { r0: 6, r1: 120, life: 0.5, hue: 190, width: 5 });
      fx.burst(b.x, b.y, 18, { hue: 190, speed: 300, life: 0.6, size: 3, grav: 120, drag: 0.92 });
    }
  }

  // ---------------------------------------------------------------- update

  update(dt) {
    this.realTime += dt;

    // UI timers run on unscaled time.
    if (this.banner) {
      this.banner.t += dt;
      if (this.banner.t > this.banner.life) this.banner = null;
    }
    if (this.toast) {
      this.toast.t += dt;
      if (this.toast.t > this.toast.life) this.toast = null;
    }
    // Brick lessons: `hint` counts down; the queue feeds one at a time, never pausing.
    if (this.hint) {
      this.hint.t -= dt;
      if (this.hint.t <= 0) { this.hint = null; this.hintGap = 0.7; }
    } else if (this.hintQueue.length && (this.state === 'playing' || this.state === 'serve')) {
      this.hintGap -= dt;
      if (this.hintGap <= 0) this.showHint(this.hintQueue.shift());
    }
    if (this.levelIntro) {
      this.levelIntro.t -= dt;
      if (this.levelIntro.t <= 0) this.levelIntro = null;
    }

    if (this.multPulse > 0) this.multPulse -= dt * 2.2;
    this.displayMult = damp(this.displayMult, this.multiplier, 0.0002, dt);
    this.intensity = clamp(
      ((this.multiplier - 1) / (MULT_CAP - 1)) * 0.82 + (this.overtop ? 0.18 : 0),
      0, 1,
    );
    fx.setIntensity(this.intensity);
    audio.setIntensity(this.intensity);
    audio.setState?.(this.state);
    audio.setOvertop?.(this.overtop);

    if (this.state === 'title' || this.state === 'gameover' || this.state === 'paused') {
      for (const b of this.bricks) b.update(dt);
      fx.update(dt);
      return;
    }

    if (this.hitstop > 0) {
      this.hitstop -= dt;
      fx.update(dt * 0.3);
      return;
    }

    let ts = this.slowmoScale;
    if (this.overtopSlowmo > 0) {
      this.overtopSlowmo -= dt;
      ts = Math.min(ts, 0.35);
    }
    this.timeScale = ts;
    const sdt = dt * ts;
    this.time += sdt;

    this.paddle.update(sdt, this.cmd);
    this.updatePowerTimers(sdt);
    for (const b of this.bricks) b.update(sdt);

    if (this.state === 'levelclear') {
      this.levelClearTimer -= dt;
      for (const b of this.balls) if (!b.stuck) b.updateTrail(sdt);
      fx.update(sdt);
      if (this.levelClearTimer <= 0) this.nextLevel();
      return;
    }

    this.updateOvertop(sdt);
    this.updateAssist(sdt);
    this.updateBalls(sdt);
    this.updateCapsules(sdt);
    this.updateBolts(sdt);
    fx.update(sdt);
    this.stats.time += sdt;
    this.checkLevelClear();
  }

  /**
   * Anti-frustration systems.
   *  - <= 2 breakable bricks left for more than 20 s: they pulse (`brick.assist = true`)
   *    and rising balls get a tiny homing curve toward them.
   *  - 45 s with no brick destroyed: every remaining *condition* brick downgrades to a
   *    plain brick, so a level can never become unwinnable or a war of attrition.
   */
  updateAssist(sdt) {
    const remaining = [];
    for (const b of this.bricks) if (!b.dead && b.breakable) remaining.push(b);
    this.remainingBreakable = remaining.length;

    if (remaining.length > 0 && remaining.length <= 2) this.fewTimer += sdt;
    else this.fewTimer = 0;

    const wasAssist = this.assist;
    this.assist = this.fewTimer > ASSIST_AFTER;
    this.assistBricks = this.assist ? remaining : [];
    for (const b of this.bricks) b.assist = false;
    if (this.assist) {
      this.assistPulse += sdt;
      for (const b of this.assistBricks) b.assist = true;
      if (!wasAssist) {
        this.showToast('HOMING ASSIST', 190);
        for (const b of this.assistBricks) {
          fx.ring(b.cx, b.cy, { r0: 4, r1: 60, life: 0.5, hue: 190, width: 2 });
        }
      }
      // A visible slow pulse even without any render changes.
      if (this.assistPulse > 1.15) {
        this.assistPulse = 0;
        for (const b of this.assistBricks) {
          fx.ring(b.cx, b.cy, { r0: 8, r1: 44, life: 0.7, hue: 190, width: 1.5 });
        }
      }
    }

    this.stallTimer += sdt;
    if (this.stallTimer > DOWNGRADE_AFTER && remaining.length > 0) {
      this.stallTimer = 0;
      this.downgradeConditionBricks();
    }
  }

  /** Turn every surviving condition brick into a plain brick. */
  downgradeConditionBricks() {
    let n = 0;
    for (const b of this.bricks) {
      if (b.dead || !b.breakable || !CONDITION_KINDS.has(b.kind)) continue;
      b.kind = 'normal';
      b.def = BRICK_KINDS.normal;
      b.solidity = 1;
      b.hue = 45;
      b.downgraded = true;
      b.hitFlash = 1;
      fx.ring(b.cx, b.cy, { r0: 4, r1: 40, life: 0.45, hue: 45, width: 2 });
      n++;
    }
    if (n > 0) {
      audio.play('ui');
      fx.text(W / 2, this.field.top + 150, 'BRICKS SOFTENED', { hue: 45, size: 20, merge: false });
      this.showToast('BRICKS SOFTENED', 45);
    }
  }

  updateOvertop(sdt) {
    const top = topOfBricks(this.bricks);
    this.brickTop = top;
    let any = false;
    for (const b of this.balls) {
      if (!Number.isFinite(top) || b.stuck) {
        b.overtop = false;
        continue;
      }
      if (b.overtop) b.overtop = b.y < top + OVERTOP_EXIT;
      else b.overtop = b.y < top - OVERTOP_MARGIN;
      if (b.overtop) any = true;
    }
    if (any) {
      if (!this.overtop) this.triggerOvertop();
      this.overtop = true;
      this.offTopTimer = 0;
      this.overtopTime += sdt;
      this.bestOvertop = Math.max(this.bestOvertop, this.overtopTime);
      this.stats.overtopTotal += sdt;
      this.stats.longestOvertop = Math.max(this.stats.longestOvertop, this.overtopTime);
    } else {
      if (this.overtop) {
        this.overtop = false;
        this.ceilingCombo = 0;
        audio.play('overtopEnd');
      }
      this.offTopTimer += sdt;
      if (this.offTopTimer > 1.5) {
        this.overtopTime = Math.max(0, this.overtopTime - sdt * 2.2);
        this.streakDecay += sdt;
        while (this.streakDecay > 0.3 && this.overtopStreak > 0) {
          this.overtopStreak--;
          this.streakDecay -= 0.3;
        }
      }
    }
    const m = this.computeMultiplier();
    if (m !== this.multiplier) {
      if (m > this.multiplier) {
        this.multPulse = 1;
        audio.play('multiplier', { level: m });
        fx.flash(40 + m * 12, 0.1, 0.18);
      }
      this.multiplier = m;
      this.stats.maxMultiplier = Math.max(this.stats.maxMultiplier, m);
    }
  }

  updateBalls(sdt) {
    // Only bricks that can be touched right now enter the broadphase.
    this.activeBricks.length = 0;
    for (const b of this.bricks) if (b.collidable()) this.activeBricks.push(b);

    const world = {
      bounds: this.field,
      bricks: this.activeBricks,
      paddle: this.paddle,
      onWall: (ball, side, info) => this.onWall(ball, side, info),
      onBrick: (ball, brick, info) => this.onBrick(ball, brick, info),
      onPaddle: (ball, paddle, info) => this.onPaddle(ball, paddle, info),
      onBottom: (ball) => { if (!this.tryCoyoteSave(ball)) ball.dead = true; },
    };

    const minSpeed = this.minSpeed;
    const maxSpeed = this.ballMaxSpeed;

    for (const ball of this.balls) {
      if (ball.dead) continue;
      if (ball.stuck) {
        ball.x = clamp(this.paddle.cx + ball.stickOffset, this.field.left + ball.r, this.field.right - ball.r);
        ball.y = this.paddle.y - ball.r - 2;
        ball.vx = 0; ball.vy = 0;
        ball.stickTimer -= sdt;
        if (ball.stickTimer <= 0) this.launchBall(ball);
        continue;
      }

      // Anti-stall: a ball skating flat for too long gets nudged. The threshold scales
      // with speed so a fast ball can't sit in a multi-second horizontal loop either.
      if (Math.abs(ball.vy) < Math.max(48, ball.speed * 0.17)) {
        ball.flatTime += sdt;
        if (ball.flatTime > 2) {
          avoidDegenerate(ball, 0.34);
          ball.flatTime = 0;
          fx.sparks(ball.x, ball.y, 6, 60, 120);
        }
      } else ball.flatTime = 0;

      moveBall(ball, sdt, world);
      if (!ball.stuck) {
        // Speed bleeds back toward the level's cruising speed, so a smash is a burst
        // rather than a permanent state (and Glass bricks stay reachable).
        const cruise = Math.min(minSpeed * 1.06, 455);
        const sp = ball.speed;
        if (sp > cruise) ball.setSpeed(sp + (cruise - sp) * (1 - Math.exp(-0.35 * sdt)));
        ball.clampSpeed(minSpeed, maxSpeed);
        avoidDegenerate(ball, 0.1);
        this.trackNearMiss(ball);
        this.homingAssist(ball, sdt);
      }

      ball.updateTrail(sdt);

      // Paddle sweeping up into a resting ball must never embed it.
      if (!ball.stuck && !ball.dead) {
        const pen = resolveMovingOverlap(ball, this.paddle);
        if (pen) {
          if (pen.ny < -0.3 && ball.vy > -40) {
            const [vx, vy] = paddleBounce(ball, this.paddle, {
              minSpeed, maxSpeed, maxAngle: 1.05,
            });
            ball.vx = vx;
            ball.vy = Math.min(vy, -minSpeed * 0.6);
          }
        }
      }
    }

    // Reap dead balls.
    let lost = false;
    for (let i = this.balls.length - 1; i >= 0; i--) {
      if (this.balls[i].dead) {
        const b = this.balls[i];
        fx.burst(b.x, this.field.bottom - 6, 12, { hue: 0, speed: 200, life: 0.5, size: 3, grav: -60 });
        this.balls.splice(i, 1);
        lost = true;
      }
    }
    if (lost && this.balls.length > 0) audio.play('wall');
    if (this.balls.length === 0 && (this.state === 'playing' || this.state === 'serve')) {
      this.loseLife();
    }
  }

  // ---------------------------------------------------------------- assists

  /**
   * Remember how close the ball came to the paddle on its way past, so a miss by a
   * hair can be forgiven when it finally leaves the field (coyote time, spatially).
   */
  trackNearMiss(ball) {
    const p = this.paddle;
    if (ball.y + ball.r < p.y - 60) { ball.nearMiss = Infinity; return; }
    if (ball.vy <= 0) return;
    if (ball.y + ball.r < p.y - 4 || ball.y - ball.r > p.y + p.h + 14) return;
    const nx = clamp(ball.x, p.x, p.x + p.w);
    const gap = Math.abs(ball.x - nx) - ball.r;
    ball.nearMiss = Math.min(ball.nearMiss ?? Infinity, gap);
  }

  /** Ball-save: missed the paddle by <= COYOTE_PX? Call it a hit. */
  tryCoyoteSave(ball) {
    if (ball.dead || ball.stuck) return false;
    const gap = ball.nearMiss;
    if (!(gap <= COYOTE_PX)) return false;
    const p = this.paddle;
    ball.nearMiss = Infinity;
    ball.x = clamp(ball.x, p.x + 4, p.x + p.w - 4);
    ball.y = p.y - ball.r - 1;
    const [vx, vy] = paddleBounce(ball, p, {
      minSpeed: this.minSpeed, maxSpeed: this.ballMaxSpeed, maxAngle: 1.0,
    });
    ball.vx = vx;
    ball.vy = Math.min(vy, -this.minSpeed * 0.85);
    this.stats.saves++;
    this.combo = 0;
    p.squash = 1;
    p.flash = 1;
    audio.play('ping');
    fx.text(ball.x, p.y - 26, 'SAVE!', { hue: 150, size: 16, merge: false });
    fx.ring(ball.x, p.y, { r0: 4, r1: 52, life: 0.35, hue: 150, width: 3 });
    fx.sparks(ball.x, p.y, 10, 150, 200);
    return true;
  }

  /** Tiny curvature toward the last stubborn bricks (only while rising, only close by). */
  homingAssist(ball, sdt) {
    if (!this.assist || ball.vy >= 0) return;
    let best = null;
    let bestD = 120;
    for (const br of this.assistBricks) {
      if (br.dead || br.cy > ball.y) continue;
      const d = Math.hypot(br.cx - ball.x, br.cy - ball.y);
      if (d < bestD) { bestD = d; best = br; }
    }
    if (!best) return;
    const cur = Math.atan2(ball.vy, ball.vx);
    let want = Math.atan2(best.cy - ball.y, best.cx - ball.x);
    let d = want - cur;
    while (d > Math.PI) d -= Math.PI * 2;
    while (d < -Math.PI) d += Math.PI * 2;
    const step = clamp(d, -1.1 * sdt, 1.1 * sdt);
    const sp = ball.speed;
    const a = cur + step;
    const nvy = Math.sin(a) * sp;
    if (nvy > -20) return;      // never curve the ball back downward
    ball.vx = Math.cos(a) * sp;
    ball.vy = nvy;
  }

  // ---------------------------------------------------------------- collisions

  onWall(ball, side, info) {
    const pan = clamp((ball.x / this.w) * 2 - 1, -1, 1);
    if (side === 'top') {
      if (this.overtop && ball.overtop) {
        this.ceilingCombo++;
        const bonus = Math.round(20 * this.ceilingCombo * this.multiplier);
        this.addScore(bonus);
        fx.text(ball.x, this.field.top + 26, `+${bonus}`, { hue: 190, size: 15 });
        fx.sparks(ball.x, this.field.top + 4, 8 + this.ceilingCombo, 190, 200);
        fx.ring(ball.x, this.field.top + 2, { r0: 3, r1: 28 + this.ceilingCombo * 4, life: 0.3, hue: 190, width: 2 });
        audio.play('ceiling', { combo: this.ceilingCombo, pan });
      } else {
        audio.play('wall', { pan });
        fx.sparks(ball.x, this.field.top + 3, 4, 200, 120);
      }
    } else {
      audio.play('wall', { pan });
      fx.sparks(ball.x, ball.y, 4, 200, 120);
    }
  }

  onPaddle(ball, paddle, info) {
    const pan = clamp((ball.x / this.w) * 2 - 1, -1, 1);
    ball.nearMiss = Infinity;
    if (this.combo >= 3) {
      fx.text(paddle.cx, paddle.y - 30, `COMBO x${this.combo}`, { hue: 45, size: 14, merge: false });
    }
    this.combo = 0;
    // Struck on the side or underside: plain bounce, no english.
    if (info.ny > -0.35) {
      audio.play('wall', { pan });
      return 'reflect';
    }
    if (this.powers.magnet > 0) {
      ball.stuck = true;
      ball.stickOffset = clamp(ball.x - paddle.cx, -paddle.w / 2 + 8, paddle.w / 2 - 8);
      ball.stickTimer = 3.0;
      ball.vx = 0; ball.vy = 0;
      audio.play('ping', { pan });
      paddle.flash = 1;
      return 'stick';
    }
    // Effective vertical paddle speed, with a short memory of a recent upward flick.
    const pvy = paddle.vy > 0 ? paddle.vy : -Math.max(paddle.smashVel, -paddle.vy);
    const smashing = pvy < -70;
    const soft = pvy > 70;
    const [vx, vy] = paddleBounce(ball, paddle, {
      maxAngle: smashing ? 0.96 : 1.13,
      minSpeed: this.minSpeed,
      maxSpeed: this.ballMaxSpeed,
      english: 0.14,
      pvy,
    });
    ball.vx = vx;
    ball.vy = vy;
    paddle.squash = 1;
    paddle.flash = 1;
    this.ceilingCombo = 0;
    audio.play(smashing ? 'smash' : 'paddle', { pitch: (ball.x - paddle.cx) / paddle.w, pan });
    if (smashing) {
      fx.sparks(ball.x, paddle.y, 16, 28, 320);
      fx.ring(ball.x, paddle.y, { r0: 4, r1: 46, life: 0.3, hue: 28, width: 3 });
      fx.shake(3.5, 0.16);
    } else if (soft) {
      fx.sparks(ball.x, paddle.y, 5, 200, 90);
    } else {
      fx.sparks(ball.x, paddle.y, 7, 190, 170);
    }
    return 'custom';
  }

  onBrick(ball, brick, info) {
    const pan = clamp((brick.cx / this.w) * 2 - 1, -1, 1);
    // Meeting a brick kind for the first time teaches it, without pausing the game.
    if (!this.seenKinds.has(brick.kind)) this.showHint(brick.kind);
    const isSteel = brick.kind === 'steel';
    let res;
    if (isSteel) {
      res = { action: 'bounce', sfx: 'steel', deny: true };
    } else if (ball.fire > 0) {
      res = { action: 'breakThru', sfx: 'break' };
    } else if (ball.ghost > 0 && ball.vy < 0) {
      res = { action: 'pass' };
    } else {
      res = brick.hit(ball, { brick, game: this, info });
    }

    if (res.action === 'pass') return 'pass';

    if (res.action === 'bounce') {
      brick.denyFlash = 1;
      brick.shake = 1;
      if (res.sfx) audio.play(res.sfx, { pan, combo: this.overtopStreak });
      fx.sparks(info.nx ? brick.cx + info.nx * brick.w / 2 : ball.x,
        info.ny ? brick.cy + info.ny * brick.h / 2 : ball.y, 5, brick.hue, 140);
      return 'reflect';
    }

    // --- destruction ---
    if (res.sfx) audio.play(res.sfx, { pan, combo: this.overtopStreak });
    this.destroyBrick(brick, ball);

    if (res.explode) this.explode(brick, ball, 0);
    if (res.boost) {
      const s = Math.max(760, Math.hypot(ball.vx, ball.vy) * 1.25);
      const [vx, vy] = setLength(ball.vx * 0.25, -Math.abs(ball.vy) - 400, Math.min(s, this.maxSpeed));
      ball.vx = vx;
      ball.vy = vy;
      fx.ring(brick.cx, brick.cy, { r0: 6, r1: 70, life: 0.35, hue: 95, width: 4 });
      fx.burst(brick.cx, brick.cy, 16, { hue: 95, speed: 300, dir: -Math.PI / 2, spread: 1.2, life: 0.5, size: 3 });
      fx.shake(5, 0.2);
      return 'destroy'; // keep the boosted velocity, don't reflect
    }
    if (ball.heavy > 0) {
      const ns = neighborsOf(this.bricks, brick, 1).filter((n) => n.kind !== 'steel');
      if (ns.length) {
        const n = ns[(Math.random() * ns.length) | 0];
        this.destroyBrick(n, ball);
        fx.sparks(n.cx, n.cy, 8, n.hue, 200);
      }
    }
    return res.action === 'breakThru' ? 'destroy' : 'destroyBounce';
  }

  destroyBrick(brick, ball, silent = false) {
    if (brick.dead || brick.kind === 'steel') return;
    brick.dead = true;
    brick.assist = false;
    this.stats.bricksBroken++;
    this.stallTimer = 0;
    const overtop = !!(ball && ball.overtop);
    if (overtop) {
      this.overtopStreak++;
      this.stats.overtopBricks++;
      this.offTopTimer = 0;
    }
    // Combo: consecutive kills without the paddle intervening.
    this.combo++;
    this.comboBest = Math.max(this.comboBest, this.combo);
    this.stats.bestCombo = Math.max(this.stats.bestCombo, this.combo);
    let pts = Math.round((brick.def.score || 100) * this.multiplier);
    if (this.combo >= 3) {
      pts += Math.round(Math.min(this.combo - 2, 12) * 15 * this.multiplier);
    }
    this.addScore(pts);

    fx.brickBurst(brick, brick.hue, overtop || this.multiplier >= 4);
    if (overtop) {
      fx.burst(brick.cx, brick.cy, 10, {
        hue: (brick.hue + 40) % 360, speed: 260, life: 0.55, size: 3, grav: 260, drag: 0.93,
      });
    }
    fx.text(brick.cx, brick.cy, `+${pts}`, {
      hue: brick.hue, size: 12 + Math.min(16, this.multiplier * 1.7),
    });
    if (this.multiplier >= 4) fx.shake(1 + this.multiplier * 0.35, 0.12);
    if (this.multiplier >= 6) this.hitstop = Math.min(0.035, 0.012 * (this.multiplier - 5));

    // Drop rate: capsule bricks always, ~1 in 6 normal bricks, rarely anything else.
    // Never more than three capsules in the air at once.
    if (this.capsules.length < 3) {
      const p = brick.kind === 'powerup' ? 1 : brick.kind === 'normal' ? 1 / 6 : 0.05;
      if (chance(p)) this.spawnCapsule(brick);
    }
  }

  explode(brick, ball, depth = 0) {
    fx.shockwave(brick.cx, brick.cy, 140, 18);
    fx.shake(9, 0.3);
    fx.flash(20, 0.16, 0.25);
    const ns = neighborsOf(this.bricks, brick, 1);
    for (const n of ns) {
      if (n.kind === 'steel') continue;
      const chain = n.kind === 'bomb';
      this.destroyBrick(n, ball);
      if (chain && depth < 4) {
        audio.play('boom');
        this.explode(n, ball, depth + 1);
      }
    }
  }

  spawnCapsule(brick) {
    const type = this.randomPowerType();
    const c = new Capsule(brick.cx - 17, brick.cy - 9, type);
    this.capsules.push(c);
  }

  // ---------------------------------------------------------------- entities

  updateCapsules(sdt) {
    const p = this.paddle;
    for (let i = this.capsules.length - 1; i >= 0; i--) {
      const c = this.capsules[i];
      c.update(sdt, p);
      if (c.y > this.field.bottom) { this.capsules.splice(i, 1); continue; }
      if (c.x < p.x + p.w && c.x + c.w > p.x && c.y + c.h > p.y && c.y < p.y + p.h) {
        this.capsules.splice(i, 1);
        this.applyPower(c.type);
        fx.burst(c.x + c.w / 2, c.y, 16, {
          hue: POWERUPS[c.type].hue, speed: 220, life: 0.5, size: 3, grav: 120,
        });
      }
    }
  }

  updateBolts(sdt) {
    for (let i = this.bolts.length - 1; i >= 0; i--) {
      const b = this.bolts[i];
      b.y += b.vy * sdt;
      if (b.y + b.h < this.field.top) { this.bolts.splice(i, 1); continue; }
      for (const brick of this.bricks) {
        if (!brick.collidable()) continue;
        if (b.x + b.w > brick.x && b.x < brick.x + brick.w &&
            b.y < brick.y + brick.h && b.y + b.h > brick.y) {
          if (brick.kind === 'steel') {
            fx.sparks(b.x, brick.y + brick.h, 6, 200, 160);
            audio.play('steel');
          } else {
            audio.play('break');
            this.destroyBrick(brick, null);
          }
          b.dead = true;
          break;
        }
      }
      if (b.dead) this.bolts.splice(i, 1);
    }
  }

  loseLife() {
    this.lives--;
    this.combo = 0;
    audio.play('lose');
    fx.flash(0, 0.3, 0.45);
    fx.shake(11, 0.4);
    this.resetMultiplier();
    this.clearPowers();
    this.capsules.length = 0;
    this.bolts.length = 0;
    if (this.lives <= 0) {
      this.lives = 0;
      this.gameOver();
    } else {
      this.serve();
      this.showBanner('BALL LOST', `${this.lives} BALL${this.lives === 1 ? '' : 'S'} LEFT`, 0, 1.3);
    }
  }

  checkLevelClear() {
    if (this.state !== 'playing' && this.state !== 'serve') return;
    if (countBreakable(this.bricks) > 0) return;
    this.state = 'levelclear';
    this.levelClearTimer = 2.4;
    this.stats.levelsCleared++;
    const bonus = 500 + this.level * 150 + Math.floor(this.bestOvertop) * 50;
    this.levelBonus = bonus;
    this.addScore(bonus);
    // The level-clear CARD is the presentation (render.js draws the bonus row) — no banner.
    this.banner = null;
    audio.play('levelup');
    fx.flash(140, 0.3, 0.6);
    fx.shake(8, 0.4);
    for (let i = 0; i < 6; i++) {
      fx.burst(rand(60, this.w - 60), rand(this.field.top + 60, this.field.top + 320), 14, {
        hue: rand(0, 360), speed: 300, life: 0.9, size: 3.5, grav: 300,
      });
    }
  }
}
