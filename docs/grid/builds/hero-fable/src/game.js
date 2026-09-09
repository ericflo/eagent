// game.js — state machine, level flow, simulation, scoring, multiplier, overtop.
// Pure: no DOM/canvas/audio. Everything the presentation layer needs to react to
// is pushed into `game.events` (drained by main.js each frame).

import {
  FIELD, SPEED, sweepCircleAABB, earliestHit, reflect, enforceMinVertical, paddleBounce, aabbOverlap,
} from './physics.js';
import {
  buildGrid, canHit, isSolid, bombChain, neighbours, pointsFor, highestBreakableTop, breakableRemaining,
  BOOST_SPEED, STD_DROP_CHANCE, BOMB_CHAIN_POINTS, BRICK_TYPES,
} from './bricks.js';
import {
  createBall, applyBallPower, updateBallPower, ignoresRules, pierces, effectiveRadius, effectiveSpeed,
  splitBall, rollPower, POWERS, MAGNET_AUTO_LAUNCH, clampSpeed, rotateDir,
} from './balls.js';
import { createPaddle, updatePaddle, paddleBox, applyPaddlePower, PADDLE } from './paddle.js';
import { LEVELS, parseLevel, levelAt, loopSpeedScale } from './levels.js';
import { clamp, createRng, normalize, uid } from './util.js';

export const STEP = 1 / 120; // fixed simulation step (seconds of game time)
export const LIVES = 3;
export const MAX_BALLS = 8;
export const TIER_NAMES = ['Warm', 'Hot', 'Blazing', 'Plasma', 'Nova', 'Supernova', 'TRANSCENDENT'];
export const MULT_MAX = 99;
export const MULT_DECAY_INTERVAL = 1.5; // s without a brick hit → multiplier -1
export const TOP_RAMP_INTERVAL = 0.75; // s on top → multiplier +1
export const TOP_POINT_MULT = 2; // bricks broken from above while on top score ×2
export const CASCADE_EVERY = 5; // consecutive top breaks per cascade
export const HALO_TIME = 4; // s on top → halo + confetti
export const TOP_HEAVY_TIME = 8; // s on top → ball becomes heavy while up there
export const OVERTOP_WIND_DOWN = 1; // s for the overdrive to fade after dropping
export const SLOWMO_SCALE = 0.3;
export const SLOWMO_TIME = 0.4;
export const HITSTOP = 0.04;
export const CAPSULE_SPEED = 230;
export const LASER_SPEED = 1500;
export const LEVEL_CLEAR_TIME = 3.2;
// Stuck-ball watchdog: a ball that has not broken a brick or touched the paddle
// for this long gets its direction nudged a few degrees on every bounce until it
// escapes (steel pockets, vertical ping-pong under the top wall, ...).
export const WATCHDOG_TIME = 10;
export const WATCHDOG_NUDGE = (6 * Math.PI) / 180;

// ---------------------------------------------------------------------------
// Scoring & multiplier (pure, unit-tested)
// ---------------------------------------------------------------------------

/** Tier 0..6 from the multiplier: ×1-4 Warm, ×5-9 Hot, ... ×30+ TRANSCENDENT. */
export const tierFor = (multiplier) => clamp(Math.floor(multiplier / 5), 0, 6);

export function createScoring() {
  return {
    score: 0,
    multiplier: 1,
    decayTimer: 0, // time since the last brick hit
    topRamp: 0, // accumulator for the on-top ramp
    comboIndex: 0, // consecutive brick hits (drives the brick-break pitch)
  };
}

/**
 * Award points for a brick. Base points come from the type (or the flat chain
 * bonus for bricks a bomb took with it), doubled when broken from above while
 * on top, then multiplied. Returns the points added.
 */
export function scoreBrick(s, type, { fromTop = false, chained = false } = {}) {
  let base = chained ? BOMB_CHAIN_POINTS : pointsFor(type);
  if (fromTop) base *= TOP_POINT_MULT;
  const pts = base * s.multiplier;
  s.score += pts;
  return pts;
}

/**
 * Combo rule: every brick hit adds +1 to the multiplier (cap 99) and resets the
 * decay clock. Touching the paddle does NOT reset anything — the multiplier only
 * drifts down through `tickScoring` when no bricks are being hit.
 * Returns true when the multiplier actually changed.
 */
export function comboHit(s) {
  s.decayTimer = 0;
  s.comboIndex++;
  if (s.multiplier >= MULT_MAX) return false;
  s.multiplier++;
  return true;
}

/**
 * Per-step multiplier drift.
 *  - While any ball is on top: +1 every 0.75 s (the whole point of the game).
 *  - Otherwise: -1 every 1.5 s without a brick hit, down to ×1.
 * Returns the net multiplier change this tick.
 */
export function tickScoring(s, dt, anyOnTop) {
  let delta = 0;
  if (anyOnTop) {
    s.topRamp += dt;
    s.decayTimer = 0;
    while (s.topRamp >= TOP_RAMP_INTERVAL) {
      s.topRamp -= TOP_RAMP_INTERVAL;
      if (s.multiplier < MULT_MAX) {
        s.multiplier++;
        delta++;
      }
    }
  } else {
    s.topRamp = 0;
    s.decayTimer += dt;
    while (s.decayTimer >= MULT_DECAY_INTERVAL) {
      s.decayTimer -= MULT_DECAY_INTERVAL;
      if (s.multiplier > 1) {
        s.multiplier--;
        delta--;
      }
    }
  }
  return delta;
}

/** Losing the last ball resets the multiplier to ×1. */
export function resetMultiplier(s) {
  s.multiplier = 1;
  s.decayTimer = 0;
  s.topRamp = 0;
  s.comboIndex = 0;
}

/**
 * A ball is "on top" when its centre is above the top edge of the highest
 * remaining breakable (non-steel) brick and below the top wall — in the attic.
 */
export function isOnTop(ball, bricks) {
  const top = highestBreakableTop(bricks);
  if (!Number.isFinite(top)) return false;
  return ball.y > 0 && ball.y < top;
}

// ---------------------------------------------------------------------------
// Game
// ---------------------------------------------------------------------------

const WALLS = [
  { id: 'left', x: -400, y: -400, w: 400, h: FIELD.h + 800 },
  { id: 'right', x: FIELD.w, y: -400, w: 400, h: FIELD.h + 800 },
  { id: 'top', x: -400, y: -400, w: FIELD.w + 800, h: 400 },
];
const NUDGE = 0.05;

export function createGame(opts = {}) {
  const g = {
    state: 'title', // title | playing | paused | levelclear | gameover
    rng: createRng(opts.seed),
    events: [],
    scoring: createScoring(),
    tier: 0,
    highScore: opts.highScore || 0,
    lives: LIVES,
    levelIndex: 0,
    loop: 0,
    level: null,
    grid: null,
    bricks: [],
    balls: [],
    capsules: [],
    lasers: [],
    paddle: createPaddle(),
    clock: 0, // level clock (ghost phase)
    time: 0, // total play time
    anyOnTop: false,
    overtop: 0, // 0..1 overdrive intensity (winds down over 1 s)
    topTime: 0, // longest current on-top time among balls (camera/FX)
    slowmo: 0,
    slowmoUsed: false,
    hitstop: 0,
    stateTimer: 0,
    accumulator: 0,
    levelBonus: 0,
    stats: { topBreaks: 0, bestMultiplier: 1 },
  };
  return g;
}

const emit = (g, type, data = {}) => g.events.push({ type, ...data });

/** Start a fresh run from the title (or after game over). */
export function startGame(g) {
  g.scoring = createScoring();
  g.tier = 0;
  g.lives = LIVES;
  g.levelIndex = 0;
  g.loop = 0;
  g.time = 0;
  g.stats = { topBreaks: 0, bestMultiplier: 1 };
  g.paddle = createPaddle();
  loadLevel(g);
  g.state = 'playing';
}

export function loadLevel(g) {
  g.level = levelAt(g.levelIndex);
  g.grid = buildGrid(parseLevel(g.level));
  g.bricks = g.grid.bricks;
  g.balls = [];
  g.capsules = [];
  g.lasers = [];
  g.clock = 0;
  g.anyOnTop = false;
  g.overtop = 0;
  g.topTime = 0;
  g.slowmo = 0;
  g.hitstop = 0;
  g.paddle.wideTime = g.paddle.magnetTime = g.paddle.laserTime = 0;
  spawnServeBall(g);
  emit(g, 'levelStart', {
    index: g.levelIndex,
    number: g.levelIndex + 1 + g.loop * LEVELS.length,
    name: g.level.name,
    hint: g.level.hint,
  });
}

/** Ball speeds scale +8% per loop through the authored levels (endless mode). */
export const baseSpeed = (g) => clampSpeed(SPEED.base * loopSpeedScale(g.loop));

function spawnServeBall(g) {
  const p = g.paddle;
  const b = createBall(p.x, p.y - p.h / 2 - 10, { x: 0, y: -1 }, baseSpeed(g));
  b.stuck = true;
  b.stuckOffset = 0;
  b.stuckTime = 0;
  b.serve = true; // waiting for the player's launch (no auto-launch)
  g.balls.push(b);
  g.slowmoUsed = false;
}

export function togglePause(g) {
  if (g.state === 'playing') g.state = 'paused';
  else if (g.state === 'paused') g.state = 'playing';
}

/**
 * Advance the game by `dtReal` seconds of wall-clock time.
 * input = { paddleCmd, fire (edge), fireHeld, aim:{x,y}|null }
 */
export function updateGame(g, dtReal, input) {
  if (g.state === 'levelclear') {
    g.stateTimer -= dtReal;
    stepFx(g, dtReal);
    if (g.stateTimer <= 0) nextLevel(g);
    return;
  }
  if (g.state !== 'playing') return;

  if (g.hitstop > 0) {
    g.hitstop -= dtReal;
    return;
  }
  const scale = g.slowmo > 0 ? SLOWMO_SCALE : 1;
  if (g.slowmo > 0) g.slowmo -= dtReal;

  if (input && input.fire) handleFire(g, input);
  if (input && input.fireHeld && g.paddle.laserTime > 0 && g.paddle.laserCooldown <= 0) fireLaser(g);

  g.accumulator += Math.min(dtReal, 0.1) * scale;
  const cmd = input ? input.paddleCmd : null;
  let steps = 0;
  while (g.accumulator >= STEP && steps++ < 12 && g.state === 'playing') {
    g.accumulator -= STEP;
    stepGame(g, STEP, cmd);
  }
}

/** Timers that keep ticking during the level-clear celebration. */
function stepFx(g, dt) {
  for (const b of g.balls) b.squash *= Math.exp(-dt * 8);
}

function handleFire(g, input) {
  const stuck = g.balls.filter((b) => b.stuck);
  if (stuck.length) {
    for (const b of stuck) launchBall(g, b, input.aim);
    return;
  }
  if (g.paddle.laserTime > 0 && g.paddle.laserCooldown <= 0) fireLaser(g);
}

/** Launch a resting/magnetised ball. Aim point (logical coords) picks the angle. */
function launchBall(g, ball, aim) {
  const p = g.paddle;
  let dir;
  if (aim && aim.y < ball.y - 40 && Math.abs(aim.x - ball.x) > 30) {
    dir = normalize(aim.x - ball.x, aim.y - ball.y);
  } else {
    // keyboard / stick: mostly up, tilted by the paddle's motion or a light random
    const tilt = clamp(p.vx / 1500, -0.5, 0.5) || g.rng.range(-0.25, 0.25);
    dir = normalize(tilt, -1);
  }
  if (dir.y > -0.35) dir = normalize(Math.sign(dir.x || 1) * 0.94, -0.35); // never launch flat
  // The release happens in followPaddle() on the next step, after the paddle has
  // moved, so the ball always leaves from where the paddle actually is.
  ball.launchDir = dir;
}

function releaseBall(g, ball) {
  const dir = ball.launchDir;
  ball.launchDir = null;
  ball.dx = dir.x;
  ball.dy = dir.y;
  ball.stuck = false;
  ball.serve = false;
  ball.stuckTime = 0;
  emit(g, 'launch', { x: ball.x, y: ball.y });
}

function fireLaser(g) {
  const p = g.paddle;
  p.laserCooldown = PADDLE.laserInterval;
  const y = p.y - p.h / 2 - 4;
  for (const sx of [-1, 1]) {
    g.lasers.push({ id: uid(), x: p.x + sx * p.w * 0.36, y, w: 4, h: 22 });
  }
  emit(g, 'laser', { x: p.x, y });
}

// ---------------------------------------------------------------------------
// Fixed step
// ---------------------------------------------------------------------------

function stepGame(g, dt, cmd) {
  g.clock += dt;
  g.time += dt;
  updatePaddle(g.paddle, cmd, dt);

  for (const ball of g.balls) {
    if (ball.stuck) followPaddle(g, ball, dt);
    else moveBall(g, ball, dt);
  }
  updatePowers(g, dt);
  updateCapsules(g, dt);
  updateLasers(g, dt);
  updateOvertop(g, dt);

  const delta = tickScoring(g.scoring, dt, g.anyOnTop);
  if (delta !== 0) onMultiplierChanged(g, delta > 0 ? { x: FIELD.w / 2, y: 90 } : null);

  handleLostBalls(g);
  if (g.state === 'playing' && breakableRemaining(g.bricks) === 0) completeLevel(g);
}

function followPaddle(g, ball, dt) {
  const p = g.paddle;
  ball.x = clamp(p.x + ball.stuckOffset, ball.r, FIELD.w - ball.r);
  ball.y = p.y - p.h / 2 - ball.r;
  ball.stuckTime += dt;
  if (ball.launchDir) {
    releaseBall(g, ball);
    return;
  }
  // Magnetised balls auto-launch after 2 s; the serve ball waits for the player.
  if (!ball.serve && ball.stuckTime >= MAGNET_AUTO_LAUNCH) launchBall(g, ball, null);
}

/**
 * Move one ball for dt seconds, sub-stepping through every collision along the
 * way (walls, paddle, bricks) so nothing can be tunnelled through at any speed.
 */
function moveBall(g, ball, dt) {
  const spd = effectiveSpeed(ball);
  ball.idle = (ball.idle || 0) + dt;
  let remaining = dt;
  let lastHit = null;
  for (let iter = 0; iter < 12 && remaining > 1e-7; iter++) {
    const r = ball.r;
    const dx = ball.dx * spd * remaining;
    const dy = ball.dy * spd * remaining;

    let best = null;
    for (const wall of WALLS) {
      if (wall === lastHit) continue;
      const h = sweepCircleAABB(ball.x, ball.y, r, dx, dy, wall);
      if (h && (!best || h.t < best.t)) best = { ...h, kind: 'wall', box: wall };
    }
    // The paddle only matters to a ball that is coming DOWN. A paddle sweeping
    // over/into a rising ball must never slap it back to the floor.
    if (lastHit !== g.paddle && ball.dy > 0) {
      const pb = paddleBox(g.paddle);
      const h = sweepCircleAABB(ball.x, ball.y, r, dx, dy, pb);
      if (h && (!best || h.t < best.t)) best = { ...h, kind: 'paddle', box: pb };
    }
    const hb = earliestHit(ball.x, ball.y, r, dx, dy, g.bricks, (b) => b !== lastHit && isSolid(b, g.clock));
    if (hb && (!best || hb.t < best.t)) best = { ...hb, kind: 'brick' };

    if (!best) {
      ball.x += dx;
      ball.y += dy;
      break;
    }

    ball.x += dx * best.t;
    ball.y += dy * best.t;
    remaining *= 1 - best.t;
    if (best.overlap) pushOut(ball, best.box, best.nx, best.ny);

    if (best.kind === 'wall') {
      bounce(g, ball, best.nx, best.ny);
      emit(g, 'wallHit', { x: ball.x, y: ball.y, side: best.box.id });
      lastHit = best.box;
    } else if (best.kind === 'paddle') {
      hitPaddle(g, ball, best);
      lastHit = g.paddle;
      if (ball.stuck) return;
    } else {
      const brick = best.box;
      const verdict = canHit(
        brick,
        { speed: ball.speed, dx: ball.dx, dy: ball.dy, ignoresRules: ignoresRules(ball) },
        best,
        g.clock,
      );
      if (verdict.breaks) {
        const fromTop = ball.onTop && best.ny < 0 && ball.dy > 0;
        breakBrick(g, brick, { ball, fromTop });
        ball.idle = 0;
        if (!pierces(ball)) bounce(g, ball, best.nx, best.ny);
      } else if (verdict.deny) {
        bounce(g, ball, best.nx, best.ny);
        brick.flash = 1;
        emit(g, 'brickDeny', { brick, kind: verdict.deny, x: ball.x, y: ball.y });
      }
      lastHit = brick;
    }
  }
  // Safety net: never leave the ball inside a side/top wall.
  ball.x = clamp(ball.x, ball.r, FIELD.w - ball.r);
  if (ball.y < ball.r) {
    ball.y = ball.r;
    if (ball.dy < 0) ball.dy = -ball.dy;
  }
}

/**
 * Reflect off a surface with normal (nx,ny). While the watchdog is active the
 * outgoing direction is rotated a few degrees away from whichever axis it hugs,
 * so a ball ping-ponging vertically (top wall ↔ steel) grows a sideways drift and
 * a ball skimming horizontally grows a vertical one. The nudged direction must
 * still leave the surface (dot with the normal ≥ 0.2) — otherwise we keep the
 * plain reflection — so a nudge can never point the ball back into a brick.
 */
function bounce(g, ball, nx, ny) {
  let d = reflect(ball.dx, ball.dy, nx, ny);
  if (ball.idle >= WATCHDOG_TIME) {
    if (!ball.watchdog) {
      ball.watchdog = true;
      emit(g, 'watchdog', { x: ball.x, y: ball.y, ball });
    }
    const a = WATCHDOG_NUDGE * g.rng.range(0.6, 1.4);
    // rotate toward the weaker axis (away from the axis the ball is hugging)
    const weakSign = Math.abs(d.x) < Math.abs(d.y) ? (Math.sign(d.x) || g.rng.sign()) : 0;
    let n;
    if (weakSign !== 0) {
      // grow |x| in its own direction: rotate so x moves toward weakSign
      const s = weakSign * (d.y > 0 ? -1 : 1);
      n = rotateDir(d.x, d.y, s * a);
    } else {
      const sy = Math.sign(d.y) || 1;
      const s = sy * (d.x > 0 ? 1 : -1);
      n = rotateDir(d.x, d.y, s * a);
    }
    if (n.x * nx + n.y * ny >= 0.2) d = n;
  } else if (ball.watchdog) {
    ball.watchdog = false;
  }
  d = enforceMinVertical(d);
  ball.dx = d.x;
  ball.dy = d.y;
  ball.x += nx * NUDGE;
  ball.y += ny * NUDGE;
}

function pushOut(ball, box, nx, ny) {
  if (nx < 0) ball.x = box.x - ball.r - NUDGE;
  else if (nx > 0) ball.x = box.x + box.w + ball.r + NUDGE;
  if (ny < 0) ball.y = box.y - ball.r - NUDGE;
  else if (ny > 0) ball.y = box.y + box.h + ball.r + NUDGE;
}

/** Paddle contact: top face → the 2D bounce; sides → plain reflection; bottom → pass. */
function hitPaddle(g, ball, hit) {
  const p = g.paddle;
  if (hit.ny > 0) return; // came from below (paddle dropped onto the ball): let it through
  if (hit.ny < 0 || hit.overlap) {
    const res = paddleBounce(ball, p);
    ball.dx = res.dir.x;
    ball.dy = res.dir.y;
    ball.speed = res.speed;
    ball.y = p.y - p.h / 2 - ball.r - NUDGE;
    ball.squash = 1;
    ball.lastPaddleHit = g.time;
    ball.topBreaks = 0;
    ball.idle = 0; // the watchdog only cares about balls the player cannot reach
    ball.offTop = OFF_TOP_GRACE; // touching the paddle always ends the on-top state
    p.recoil = res.powerHit ? 1 : 0.5;
    p.squash = 1;
    emit(g, 'paddleHit', { x: ball.x, y: ball.y, strength: res.strength, powerHit: res.powerHit, speed: ball.speed });
    if (p.magnetTime > 0) {
      ball.stuck = true;
      ball.serve = false;
      ball.stuckOffset = ball.x - p.x;
      ball.stuckTime = 0;
      emit(g, 'magnetCatch', { x: ball.x, y: ball.y });
    }
  } else {
    bounce(g, ball, hit.nx, hit.ny);
  }
}

// ---------------------------------------------------------------------------
// Bricks
// ---------------------------------------------------------------------------

/**
 * Destroy a brick and apply every consequence: score/combo, bombs, cascades,
 * chargers, capsule drops. `opts.ball` is the ball responsible (null for lasers).
 */
function breakBrick(g, brick, { ball = null, fromTop = false, chained = false, cause = 'ball' } = {}) {
  if (!brick.alive) return;
  brick.alive = false;
  const s = g.scoring;
  const multChanged = comboHit(s);
  const points = scoreBrick(s, brick.type, { fromTop, chained });
  const cx = brick.x + brick.w / 2;
  const cy = brick.y + brick.h / 2;
  emit(g, 'brickBreak', {
    brick, x: cx, y: cy, points, fromTop, chained, cause, comboIndex: s.comboIndex, multiplier: s.multiplier,
  });
  if (multChanged) onMultiplierChanged(g, { x: cx, y: cy });

  if (fromTop && ball) {
    ball.topBreaks++;
    g.stats.topBreaks++;
    if (ball.topBreaks % CASCADE_EVERY === 0) cascade(g, brick);
  }
  if (brick.type === 'bomb') explode(g, brick);
  if (brick.type === 'boost' && ball) {
    ball.speed = clampSpeed(ball.speed + BOOST_SPEED);
    emit(g, 'boost', { x: cx, y: cy, ball });
  }
  if (brick.type === 'gem' || (brick.type === 'std' && !chained && g.rng.chance(STD_DROP_CHANCE))) {
    dropCapsule(g, cx, cy);
  }
}

/** Bomb: the chain of neighbours goes too, +50 each, with a shockwave + hit-stop. */
function explode(g, origin) {
  const chain = bombChain(g.grid, origin).filter((b) => b !== origin && b.alive);
  emit(g, 'bomb', { x: origin.x + origin.w / 2, y: origin.y + origin.h / 2, count: chain.length });
  g.hitstop = Math.max(g.hitstop, HITSTOP);
  for (const b of chain) {
    if (b.type === 'bomb') {
      // nested bombs already contributed their neighbours via bombChain; score them as chained
      b.alive = false;
      const pts = scoreBrick(g.scoring, b.type, { chained: true });
      emit(g, 'brickBreak', { brick: b, x: b.x + b.w / 2, y: b.y + b.h / 2, points: pts, chained: true, cause: 'bomb', comboIndex: g.scoring.comboIndex, multiplier: g.scoring.multiplier });
      emit(g, 'bomb', { x: b.x + b.w / 2, y: b.y + b.h / 2, count: 0, nested: true });
    } else {
      breakBrick(g, b, { chained: true, cause: 'bomb' });
    }
  }
}

/** Every 5 consecutive from-above breaks: a shockwave that pops one random adjacent brick. */
function cascade(g, brick) {
  const cx = brick.x + brick.w / 2;
  const cy = brick.y + brick.h / 2;
  emit(g, 'cascade', { x: cx, y: cy });
  g.hitstop = Math.max(g.hitstop, HITSTOP);
  const candidates = neighbours(g.grid, brick).filter((b) => BRICK_TYPES[b.type].breakable);
  if (candidates.length) breakBrick(g, g.rng.pick(candidates), { fromTop: true, cause: 'cascade' });
}

function dropCapsule(g, x, y) {
  g.capsules.push({ id: uid(), kind: rollPower(g.rng), x, y, w: 46, h: 26, vy: CAPSULE_SPEED, spin: 0 });
}

// ---------------------------------------------------------------------------
// Power-ups, capsules, lasers
// ---------------------------------------------------------------------------

function updateCapsules(g, dt) {
  const pb = paddleBox(g.paddle);
  for (let i = g.capsules.length - 1; i >= 0; i--) {
    const c = g.capsules[i];
    c.y += c.vy * dt;
    c.spin += dt;
    const box = { x: c.x - c.w / 2, y: c.y - c.h / 2, w: c.w, h: c.h };
    if (aabbOverlap(box, pb)) {
      g.capsules.splice(i, 1);
      applyPower(g, c.kind, c.x, c.y);
    } else if (c.y - c.h > FIELD.h) {
      g.capsules.splice(i, 1);
    }
  }
}

/** Apply a caught power-up to the balls / paddle / game. */
export function applyPower(g, kind, x = g.paddle.x, y = g.paddle.y) {
  const def = POWERS[kind];
  if (!def) return;
  if (def.target === 'ball') {
    for (const b of g.balls) applyBallPower(b, kind);
  } else if (def.target === 'paddle') {
    applyPaddlePower(g.paddle, kind);
  } else if (kind === 'split') {
    const extras = [];
    for (const b of g.balls) {
      if (b.stuck) continue;
      if (g.balls.length + extras.length + 2 > MAX_BALLS) break;
      extras.push(...splitBall(b));
    }
    g.balls.push(...extras);
  } else if (kind === 'life') {
    g.lives++;
    emit(g, 'lifeUp', { lives: g.lives });
  }
  g.paddle.glow = 1;
  emit(g, 'powerup', { kind, x, y });
}

function updatePowers(g, dt) {
  for (const b of g.balls) {
    const ended = updateBallPower(b, dt);
    if (ended) emit(g, 'powerupEnd', { kind: ended });
  }
  const p = g.paddle;
  for (const [kind, key] of [['wide', 'wideTime'], ['magnet', 'magnetTime'], ['laser', 'laserTime']]) {
    const was = p[`_${key}`] || 0;
    if (was > 0 && p[key] <= 0) emit(g, 'powerupEnd', { kind });
    p[`_${key}`] = p[key];
  }
  if (p.magnetTime <= 0) for (const b of g.balls) if (b.stuck && !b.serve) launchBall(g, b, null);
}

/** Laser shots break the first soft brick they touch (std/boost/gem/solid ghost). */
function updateLasers(g, dt) {
  const soft = new Set(['std', 'boost', 'gem', 'ghost']);
  for (let i = g.lasers.length - 1; i >= 0; i--) {
    const l = g.lasers[i];
    const dy = LASER_SPEED * dt;
    const path = { x: l.x - l.w / 2, y: l.y - dy - l.h, w: l.w, h: dy + l.h };
    let hit = null;
    for (const b of g.bricks) {
      if (!isSolid(b, g.clock)) continue;
      if (aabbOverlap(path, b) && (!hit || b.y > hit.y)) hit = b;
    }
    l.y -= dy;
    if (hit) {
      g.lasers.splice(i, 1);
      if (soft.has(hit.type)) breakBrick(g, hit, { cause: 'laser' });
      else {
        hit.flash = 1;
        emit(g, 'brickDeny', { brick: hit, kind: hit.type === 'steel' ? 'steel' : 'laser', x: l.x, y: hit.y + hit.h });
      }
    } else if (l.y < -l.h) {
      g.lasers.splice(i, 1);
    }
  }
}

// ---------------------------------------------------------------------------
// Overtop
// ---------------------------------------------------------------------------

const OFF_TOP_GRACE = 1.0; // s a ball may dip below the brick line (inside the field) and still count as on top

/** Bottom edge of the lowest alive brick (steel included), or -Infinity if none. */
function brickFieldBottom(bricks) {
  let y = -Infinity;
  for (const b of bricks) if (b.alive && b.y + b.h > y) y = b.y + b.h;
  return y;
}

function updateOvertop(g, dt) {
  let any = false;
  let longest = 0;
  const fieldBottom = brickFieldBottom(g.bricks);
  for (const b of g.balls) {
    // Hysteresis: a ball skimming the top row dips into the holes it digs and
    // pops straight back up. Keep it "on top" while it is still inside the brick
    // field and comes back within the grace period. Falling out of the bottom of
    // the field (or touching the paddle, see hitPaddle) ends the state at once.
    const inAttic = !b.stuck && isOnTop(b, g.bricks);
    if (inAttic) b.offTop = 0;
    else b.offTop = (b.offTop || 0) + dt;
    const insideField = b.y - b.r < fieldBottom;
    const on = inAttic || (b.onTop && !b.stuck && insideField && b.offTop < OFF_TOP_GRACE);
    if (on) {
      if (!b.onTop) b.topBreaks = 0;
      b.topTime += dt;
      any = true;
      longest = Math.max(longest, b.topTime);
      if (b.topTime >= HALO_TIME && !b.halo) {
        b.halo = true;
        emit(g, 'halo', { x: b.x, y: b.y, ball: b });
      }
      if (b.topTime >= TOP_HEAVY_TIME && !b.forceHeavy) {
        b.forceHeavy = true;
        b.r = effectiveRadius(b);
        emit(g, 'topHeavy', { x: b.x, y: b.y, ball: b });
      }
    } else if (b.onTop) {
      b.topTime = 0;
      b.halo = false;
      b.forceHeavy = false;
      b.r = effectiveRadius(b);
    }
    b.onTop = on;
  }
  g.topTime = longest;
  if (any && !g.anyOnTop) emit(g, 'overtopEnter');
  if (!any && g.anyOnTop) emit(g, 'overtopExit');
  g.anyOnTop = any;
  g.overtop = any ? Math.min(1, g.overtop + dt * 3) : Math.max(0, g.overtop - dt / OVERTOP_WIND_DOWN);
}

function onMultiplierChanged(g, at) {
  const m = g.scoring.multiplier;
  g.stats.bestMultiplier = Math.max(g.stats.bestMultiplier, m);
  if (at) emit(g, 'multInc', { multiplier: m, x: at.x, y: at.y });
  const t = tierFor(m);
  if (t !== g.tier) {
    const up = t > g.tier;
    g.tier = t;
    emit(g, up ? 'tierUp' : 'tierDown', { tier: t, name: TIER_NAMES[t], multiplier: m });
  }
}

// ---------------------------------------------------------------------------
// Lives & level flow
// ---------------------------------------------------------------------------

function handleLostBalls(g) {
  // Dramatic slow-mo when the last ball is about to fall past the paddle band.
  if (g.balls.length === 1 && !g.slowmoUsed) {
    const b = g.balls[0];
    if (!b.stuck && b.dy > 0 && b.y > PADDLE.yMax + g.paddle.h) {
      g.slowmoUsed = true;
      g.slowmo = SLOWMO_TIME;
      emit(g, 'slowmo', { x: b.x, y: b.y });
    }
  }
  for (let i = g.balls.length - 1; i >= 0; i--) {
    const b = g.balls[i];
    if (b.y - b.r > FIELD.h + 30) {
      g.balls.splice(i, 1);
      emit(g, 'ballLost', { x: b.x, y: FIELD.h, last: g.balls.length === 0 });
    }
  }
  if (g.balls.length === 0) loseLife(g);
}

function loseLife(g) {
  g.lives--;
  resetMultiplier(g.scoring);
  onMultiplierChanged(g, null);
  g.paddle.magnetTime = 0;
  if (g.lives <= 0) {
    g.state = 'gameover';
    g.highScore = Math.max(g.highScore, g.scoring.score);
    emit(g, 'gameOver', { score: g.scoring.score, highScore: g.highScore });
    return;
  }
  spawnServeBall(g);
}

function completeLevel(g) {
  const number = g.levelIndex + 1 + g.loop * LEVELS.length;
  g.levelBonus = 1000 * number + 500 * g.lives;
  g.scoring.score += g.levelBonus;
  g.highScore = Math.max(g.highScore, g.scoring.score);
  g.state = 'levelclear';
  g.stateTimer = LEVEL_CLEAR_TIME;
  g.hitstop = HITSTOP;
  g.slowmo = 0;
  emit(g, 'levelClear', { number, bonus: g.levelBonus, name: g.level.name });
}

function nextLevel(g) {
  g.levelIndex++;
  if (g.levelIndex >= LEVELS.length) {
    g.levelIndex = 0;
    g.loop++;
  }
  loadLevel(g);
  g.state = 'playing';
}

export { LEVELS };
