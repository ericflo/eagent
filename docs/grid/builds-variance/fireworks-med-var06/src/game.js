// ===== BREAKAWAY — main game module =========================================
import { AudioSys } from './audio.js';
import { Particles } from './particles.js';
import { CanvasMgr, roundedRect, hsl } from './canvas.js';
import { LEVELS, TIER_POINTS, makeBrick } from './bricks.js';

const FIELD = { x: 0.06, y: 0.06 };           // field margin (fraction of canvas)
const BRICK_COLS = 12;
const SUMMIT_ROWS = 2;                         // rows above top row = summit-ish
const PADDLE_BAND = 0.20;                      // bottom 20% of field for Y movement
const BASE_BALL_SPEED = 340;
const SPEED_THRESHOLD = 560;                   // for speed bricks

const POWER_TYPES = {
  multi:  { name: 'MULTI',   color: '#ffd54f' },
  fire:   { name: 'FIRE',    color: '#ff7043' },
  heavy:  { name: 'HEAVY',   color: '#8d6e63' },
  slow:   { name: 'SLOW',    color: '#4dd0e1' },
  wide:   { name: 'WIDE',    color: '#7ce87c' },
  magnet: { name: 'MAGNET',  color: '#b388ff' },
  ghost:  { name: 'GHOST',   color: '#90a4ae' },
};
const POWER_KEYS = Object.keys(POWER_TYPES);
const POWER_DUR = { fire: 9, heavy: 8, slow: 5, wide: 12, magnet: 10, ghost: 9 };

const CALLS = ['UP TOP!', 'RAMPAGE!', 'UNSTOPPABLE!', 'OVERRIDE!', 'GODLIKE!'];
const CALLOUT_TIERS = [2, 4, 6, 8, 10];

const state = {
  mode: 'title',           // title | play | dying | levelclear | gameover | howto | paused(overlay flag)
  paused: false,
  level: 0,
  lives: 3,
  score: 0,
  best: +(localStorage.getItem('breakaway_best') || 0),
  reducedMotion: matchMedia('(prefers-reduced-motion: reduce)').matches,
  shakeEnabled: true,
  soundEnabled: true,
  thumbstick: false,
  time: 0,
};

// ---- up-top / multiplier state ----
const combo = {
  upTop: false,            // any ball above summit line
  timer: 0,                // seconds accumulated up top (current streak)
  bestUpTop: 0,
  totalUpTop: 0,
  mult: 1,
  maxMult: 1,
  streak: 0,               // brick hit streak
  callout: null, calloutT: 0,
};

// entities
let bricks = [];
let balls = [];
let drops = [];
let paddle = { x: 0, y: 0, w: 110, baseW: 110, h: 14, vx: 0, vy: 0, targetX: 0, targetY: 0, squash: 0, boost: 0, magnet: null };
let effects = { fire: 0, heavy: 0, slow: 0, wide: 0, magnet: 0, ghost: 0 };
let serve = { ball: null };
let stats = { bricksBroken: 0, bricksTotal: 0 };
let levelClearT = 0, dyingT = 0, bannerT = 0, bannerText = '', bannerSub = '';
let titleT = 0, ai = { x: 0.5, y: 0.8 }, aiBrickTarget = null;
let errorCount = 0;
export function bumpErrors() { errorCount++; }
let titleBalls = [];
export function getTitleBallsArr() { return titleBalls; }

// systems
const audio = new AudioSys();
export const parts = new Particles(state.reducedMotion ? 220 : 650);
let cm;   // canvas manager
export function setCanvas(c) { cm = c; }
let game = { fw: 0, fh: 0, fx: 0, fy: 0, bw: 0, bh: 0, padMinY: 0, padMaxY: 0 }; // geometry

// ---- geometry helpers -------------------------------------------------------
function layout() {
  if (!cm) return;
  const W = cm.w, H = cm.h;
  game.fw = W * (1 - 2 * FIELD.x);
  game.fh = H * (1 - FIELD.y - 0.04);
  game.fx = W * FIELD.x;
  game.fy = H * FIELD.y;
  game.bw = game.fw / BRICK_COLS;
  game.bh = Math.min(game.bw * 0.42, game.fh * 0.055);
  // summit line: top of current top row of breakable bricks (recomputed as bricks die)
  for (const b of bricks) layoutBrick(b);
  layoutPaddleBounds();
}

function layoutBrick(b) {
  const g = game;
  if (g.bw <= 0) return;
  let colOff = 0;
  if (b.type === 'move') {
    const t = state.time * b.moveSpeed + b.movePhase;
    colOff = Math.sin(t) * b.moveRange;
  }
  b.w = g.bw - 4; b.h = g.bh - 4;
  b.x = g.fx + (b.baseCol + 0.5 + colOff) * g.bw - b.w / 2;
  b.y = g.fy + b.baseRow * g.bh + 2;
}

function topBrickRowY() {
  let top = Infinity;
  for (const b of bricks) if (!b.dead && b.type !== 'solid') top = Math.min(top, b.y);
  if (top === Infinity) { for (const b of bricks) if (!b.dead) top = Math.min(top, b.y); }
  return top === Infinity ? game.fy : top;
}

function summitY() { return topBrickRowY(); }

function layoutPaddleBounds() {
  const g = game;
  g.padMinY = g.fy + g.fh * (1 - PADDLE_BAND);
  g.padMaxY = g.fy + g.fh - paddle.h - 6;
}

function baseSpeed() { return BASE_BALL_SPEED * Math.min(1.35, 1 + state.level * 0.07); }

// ---- balls -------------------------------------------------------------------
function newBall(x, y, vx, vy, special = null) {
  return {
    x, y, vx, vy, r: 7.5, special,
    fire: special === 'fire' ? POWER_DUR.fire : 0,
    heavy: special === 'heavy' ? POWER_DUR.heavy : 0,
    ghost: special === 'ghost' ? POWER_DUR.ghost : 0,
    trail: [], stuck: false,
    hue: 190,
  };
}

function serveBall() {
  const b = newBall(paddle.x, paddle.y - paddle.h / 2 - 10, 0, 0);
  b.stuck = true;
  balls = [b];
  serve.ball = b;
}

function launchBall(b) {
  if (!b.stuck) return;
  b.stuck = false;
  const sp = baseSpeed();
  const a = -Math.PI / 2 + (Math.random() - 0.5) * 0.5;
  b.vx = Math.cos(a) * sp; b.vy = Math.sin(a) * sp;
  serve.ball = null;
  audio.launch();
  parts.burst(b.x, b.y, 8, '#9fe8ff', 120, { shape: 'circle' });
}

function ballSpeed(b) { return Math.hypot(b.vx, b.vy); }

function setSpeed(b, s) {
  const c = ballSpeed(b) || 1;
  b.vx *= s / c; b.vy *= s / c;
}

// ---- power-ups ---------------------------------------------------------------
function maybeDrop(x, y) {
  if (Math.random() > 0.13) return;
  const type = POWER_KEYS[(Math.random() * POWER_KEYS.length) | 0];
  drops.push({ x, y, vy: 90, type, t: 0 });
}

function catchDrop(d) {
  const P = POWER_TYPES[d.type];
  audio.powerup();
  cm.addFlash(0.25, '180,255,220');
  parts.popup(d.x, d.y - 10, P.name, P.color, 18);
  parts.burst(d.x, d.y, 14, P.color, 180, { shape: 'circle' });
  if (state.soundEnabled && navigator.vibrate) { try { navigator.vibrate(30); } catch (e) {} }
  if (d.type === 'multi') {
    const src = balls.filter(b => !b.stuck);
    const base = src[0] || balls[0];
    if (base) {
      for (let i = 0; i < 2; i++) {
        const a = Math.atan2(base.vy, base.vx) + (i === 0 ? 0.5 : -0.5);
        const s = Math.max(baseSpeed(), ballSpeed(base));
        const nb = newBall(base.x, base.y, Math.cos(a) * s, Math.sin(a) * s, base.special);
        balls.push(nb);
      }
    }
  } else if (d.type === 'slow') { effects.slow = POWER_DUR.slow; cm.addChroma(0.4); }
  else if (d.type === 'wide') { effects.wide = POWER_DUR.wide; }
  else if (d.type === 'magnet') { effects.magnet = POWER_DUR.magnet; }
  else { effects[d.type] = POWER_DUR[d.type]; }
  // apply special to existing balls
  if (d.type === 'fire' || d.type === 'heavy' || d.type === 'ghost')
    for (const b of balls) {
      b.special = d.type;
      b[d.type] = POWER_DUR[d.type];
      b.hue = d.type === 'fire' ? 20 : d.type === 'heavy' ? 30 : 200;
    }
  updateHud();
}

function updateEffects(dt) {
  for (const k in effects) {
    if (effects[k] > 0) {
      effects[k] -= dt;
      if (effects[k] <= 0) {
        effects[k] = 0;
        if (k === 'wide') paddle.w = paddle.baseW;
        if (k === 'magnet' && paddle.magnet) { launchBall(paddle.magnet); paddle.magnet = null; }
        if (k === 'fire' || k === 'heavy' || k === 'ghost')
          for (const b of balls) if (b.special === k) { b.special = null; b[k] = 0; b.hue = 190; }
      }
    }
  }
  paddle.w += ((effects.wide > 0 ? paddle.baseW * 1.55 : paddle.baseW) - paddle.w) * Math.min(1, dt * 8);
  if (effects.slow) cm.addChroma(0.08);
}

// ---- physics: swept circle vs field ------------------------------------------
function stepBall(b, dt) {
  if (b.stuck) {
    b.x = paddle.x; b.y = paddle.y - paddle.h / 2 - b.r - 2;
    return;
  }
  const g = game;
  const speed = ballSpeed(b);
  const steps = Math.max(1, Math.ceil((speed * dt) / (Math.min(g.bw, g.bh) * 0.45)));
  const sdt = dt / steps;
  for (let i = 0; i < steps; i++) subStep(b, sdt);
  // trail
  b.trail.push({ x: b.x, y: b.y });
  if (b.trail.length > (b.fire ? 16 : 10)) b.trail.shift();
}

function subStep(b, dt) {
  const g = game;
  b.x += b.vx * dt;
  b.y += b.vy * dt;

  // walls
  if (b.x - b.r < g.fx) { b.x = g.fx + b.r; b.vx = Math.abs(b.vx); wallHit(b); }
  if (b.x + b.r > g.fx + g.fw) { b.x = g.fx + g.fw - b.r; b.vx = -Math.abs(b.vx); wallHit(b); }
  if (b.y - b.r < g.fy) { b.y = g.fy + b.r; b.vy = Math.abs(b.vy); wallHit(b); }

  // paddle (continuous: if the ball crossed the paddle plane this substep and is inside its span, hit it)
  if (b.vy > 0) {
    const px = paddle.x - paddle.w / 2, py = paddle.y - paddle.h / 2;
    const plane = py + paddle.h / 2;
    const crossed = b.y + b.r >= py && b.y - b.vy * dt - b.r <= py + paddle.h;
    if (crossed && b.x > px - b.r && b.x < px + paddle.w + b.r) {
      paddleHit(b);
    }
  }

  // bricks
  brickCollide(b);

  // bottom out
  if (b.y - b.r > g.fy + g.fh + 30) b.dead = true;
}

function wallHit(b) {
  audio.bounce(ballSpeed(b) / (baseSpeed() * 1.8));
  parts.burst(b.x, b.y, 3, '#8fb6ff', 90, { shape: 'circle' });
}

function paddleHit(b) {
  const g = game;
  const py = paddle.y - paddle.h / 2;
  // reflect up with offset-based angle (classic) + paddle horizontal spin
  const off = Math.max(-1, Math.min(1, (b.x - paddle.x) / (paddle.w / 2)));
  const ang = -Math.PI / 2 + off * (Math.PI / 3);
  let sp = Math.max(baseSpeed(), ballSpeed(b) * 0.99);
  // reflect a fast-approaching ball with steep incoming vx: preserve some, avoid pure vertical stall
  const inVx = b.vx;
  const spd = ballSpeed(b) || 1;
  const horizontalCarry = Math.abs(inVx) / spd > 0.85 ? inVx * 0.3 : 0;

  // vertical paddle motion: moving up adds energy, moving down damps
  const boost = Math.max(0, -paddle.vy) * 0.55;
  const damp = Math.max(0, paddle.vy) * 0.35;
  sp += boost - damp;
  if (boost > 60) {
    paddle.boost = 1;
    parts.burst(b.x, py, 10, '#aef6ff', 200, { shape: 'circle', grav: -300 });
    cm.addFlash(0.08, '160,240,255');
  }

  const maxSp = baseSpeed() * 2.6;
  sp = Math.min(maxSp, sp);
  b.vx = Math.cos(ang) * sp + paddle.vx * 0.18 + horizontalCarry;
  b.vy = Math.sin(ang) * sp;
  if (b.vy > -60) b.vy = -60;
  // never let the ball go (near) perfectly vertical: guarantees angle variety
  const minH = 0.06 * sp;
  if (Math.abs(b.vx) < minH) b.vx = (b.x < paddle.x ? -1 : 1) * minH;
  b.y = py - b.r - 0.5;

  paddle.squash = 1;
  audio.bounce(ballSpeed(b) / (baseSpeed() * 1.8));
  parts.burst(b.x, py, 5, '#9fe8ff', 130, { shape: 'circle' });

  // magnet catch
  if (effects.magnet > 0 && !b.stuck) {
    b.stuck = true;
    serve.ball = b;
    paddle.magnet = b;
  }
  // up-top streak resets on paddle touch
  endUpTop();
}

function endUpTop() {
  if (combo.upTop) {
    combo.upTop = false;
    combo.bestUpTop = Math.max(combo.bestUpTop, combo.timer);
  }
  combo.timer = 0;
}

function brickCollide(b) {
  for (const br of bricks) {
    if (br.dead) continue;
    // AABB vs circle
    const cx = Math.max(br.x, Math.min(b.x, br.x + br.w));
    const cy = Math.max(br.y, Math.min(b.y, br.y + br.h));
    const dx = b.x - cx, dy = b.y - cy;
    if (dx * dx + dy * dy > b.r * b.r) continue;

    const incoming = (Math.atan2(b.vy, b.vx) * 180) / Math.PI; // deg, travel dir
    const speed = ballSpeed(b);

    // phase brick invulnerable window
    if (br.type === 'phase') {
      const ph = ((state.time + br.phaseOff) % br.phasePeriod) / br.phasePeriod;
      const vulnerable = ph < 0.55;
      if (!vulnerable) { bounceOff(b, br, true); continue; }
    }
    // angle brick: incoming direction must be within window
    if (br.type === 'angle') {
      let d = Math.abs(((incoming - br.angleCenter) % 360 + 540) % 360 - 180);
      // accept the angle or its exact opposite (coming from either side)
      const d2 = Math.abs(d - 180);
      if (Math.min(d, d2) > br.angleWin) {
        bounceOff(b, br, true);
        br.clankT = 1;
        audio.clank();
        parts.burst(b.x, b.y, 4, '#c9a86a', 110);
        continue;
      }
    }
    // speed brick
    if (br.type === 'speed' && speed < SPEED_THRESHOLD && !b.heavy) {
      bounceOff(b, br, true);
      br.clankT = 1;
      audio.clank();
      parts.popup(br.x + br.w / 2, br.y, 'TOO SLOW', '#ffca7a', 12);
      continue;
    }
    // ghost ball passes through reg bricks of lowest tier
    if (b.ghost && br.type === 'reg' && br.tier === 0) continue;

    // solid: permanent obstacle, always bounces
    if (br.type === 'solid') { bounceOff(b, br, false); audio.bounce(0.4); continue; }

    // FIRE: no bounce, plow through
    if (b.fire) {
      killBrick(br, b, true);
      continue;
    }
    // HEAVY: bounces only off walls/paddle, smashes through column downward... plows any brick
    if (b.heavy) {
      killBrick(br, b, true);
      continue;
    }

    killBrick(br, b, false);
    bounceOff(b, br, false);
    break; // one brick per substep
  }
}

function bounceOff(b, br, dull) {
  // decide side by penetration depths
  const bx = br.x + br.w / 2, by = br.y + br.h / 2;
  const ox = (br.w / 2 + b.r) - Math.abs(b.x - bx);
  const oy = (br.h / 2 + b.r) - Math.abs(b.y - by);
  if (ox < oy) {
    b.vx = b.x < bx ? -Math.abs(b.vx) : Math.abs(b.vx);
    b.x += b.x < bx ? -ox : ox;
  } else {
    b.vy = b.y < by ? -Math.abs(b.vy) : Math.abs(b.vy);
    b.y += b.y < by ? -oy : oy;
  }
  if (dull) setSpeed(b, Math.max(baseSpeed() * 0.8, ballSpeed(b) * 0.85));
}

function killBrick(br, b, plow) {
  br.dead = true;
  stats.bricksBroken++;
  combo.streak++;
  const pts = Math.round(TIER_POINTS[br.tier] * combo.mult * (1 + Math.min(1.5, combo.streak * 0.04)));
  state.score += pts;
  const color = br.type === 'reg' ? ['#4fc3f7', '#7ce87c', '#ffd54f', '#ff6e9c'][br.tier] :
    br.type === 'angle' ? '#ffb74d' : br.type === 'speed' ? '#69f0ae' :
    br.type === 'move' ? '#b388ff' : br.type === 'phase' ? '#ff80ab' : '#90caf9';
  if (!state.reducedMotion || stats.bricksBroken % 3 === 0)
    parts.shatter(br.x + br.w / 2, br.y + br.h / 2, br.w, br.h, color);
  parts.popup(br.x + br.w / 2, br.y, '+' + pts, color, 13 + Math.min(10, combo.mult * 1.5));
  // rising pitch with streak
  audio.brick(combo.streak);
  if (state.soundEnabled && navigator.vibrate) { try { navigator.vibrate(12); } catch (e) {} }
  cm.addShake(state.reducedMotion ? 1 : 2.5 + combo.mult * 0.35);
  if (b.fire) parts.burst(b.x, b.y, 6, '#ff9e40', 200, { shape: 'circle' });
  maybeDrop(br.x + br.w / 2, br.y + br.h / 2);
  updateHud();
}

export { state, combo, effects, balls, drops, paddle, serve, stats };
export { LEVELS, POWER_TYPES, CALLS, CALLOUT_TIERS };
export function getAudio() { return audio; }
export function getCanvas() { return cm; }
export function getBricks() { return bricks; }
export function getErrors() { return errorCount; }
export function getGame() { return game; }
export function setTitleT(v) { titleT = v; }
export function getTitleT() { return titleT; }
export function getTitleBalls() { return titleBalls; }
export function setTitleBalls(v) { titleBalls = v; }
export function getServe() { return serve; }
export function getAI() { return ai; }
export function getDyingT() { return dyingT; }
export function setDyingT(v) { dyingT = v; }
export function getLevelClearT() { return levelClearT; }
export function setLevelClearT(v) { levelClearT = v; }
export function getBanner() { return { bannerT, bannerText, bannerSub }; }
export function setBanner(t, sub) { bannerT = 2.2; bannerText = t; bannerSub = sub || ''; }
export function decayBanner(dt) { bannerT = Math.max(0, bannerT - dt); }
export { layout, summitY, serveBall, launchBall, newBall, baseSpeed, ballSpeed, setSpeed, updateEffects, maybeDrop, catchDrop, layoutPaddleBounds, POWER_DUR, stepBall };


// ---- serving / level flow (kept here so main.js stays thin) ------------------
function startLevel(idx) {
  state.level = idx;
  const def = LEVELS[idx % LEVELS.length];
  bricks = def.build();
  stats = { bricksBroken: 0, bricksTotal: bricks.filter(b => b.type !== 'solid').length };
  combo.streak = 0; combo.timer = 0; combo.upTop = false; combo.mult = 1;
  drops.length = 0;
  balls = [];
  for (const k in effects) effects[k] = 0;
  paddle.baseW = 110; paddle.w = 110;
  layout();
  paddle.x = game.fx + game.fw / 2;
  paddle.y = game.padMaxY;
  paddle.vx = 0; paddle.vy = 0;
  serveBall();
  setBanner(def.name, def.hint);
}

function loseLife() {
  audio.loseLife();
  state.lives--;
  cm.addFlash(0.4, '255,60,60');
  cm.addShake(12);
  combo.streak = 0;
  endUpTop();
  for (const k of ['fire', 'heavy', 'ghost']) {
    effects[k] = 0;
    for (const b of balls) if (b.special === k) { b.special = null; b[k] = 0; b.hue = 190; }
  }
  if (state.lives <= 0) {
    state.mode = 'gameover';
    state.best = Math.max(state.best, state.score);
    localStorage.setItem('breakaway_best', state.best);
  } else {
    state.mode = 'dying';
    dyingT = 1.2;
  }
  updateHud();
}
export { startLevel, loseLife, updateHud };

function updateHud() {
  const el = document.getElementById('hud');
  if (el) el.textContent = `SCORE ${state.score}   x${combo.mult.toFixed(1)}   L${state.level + 1}   ♥${state.lives}`;
}
