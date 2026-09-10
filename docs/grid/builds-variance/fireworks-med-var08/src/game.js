// Game simulation: pure logic, no DOM. Emits an `events` array each step
// that the renderer/audio layers consume. Also used by Node tests.

import { LEVELS } from './levels.js';
import * as B from './core/bricks.js';
import { makeBall, effectiveSpeed, circleVsRect, reflect } from './core/physics.js';
import { createPaddle, updatePaddle, paddleReflect, PADDLE, clamp } from './core/paddle.js';
import { createMultiplier, registerBreak, update as updateMult, scoreFor } from './core/multiplier.js';
import { POWER, POWER_META, rollPower, makeCapsule, updateCapsules, TIMERS } from './core/powerups.js';

export const FIXED_DT = 1 / 120;
export const MAX_FRAME_DT = 1 / 20;
export const LEVEL_COUNT = LEVELS.length;

export const STATE = {
  MENU: 'menu',
  SERVE: 'serve',
  PLAY: 'play',
  LEVEL_CLEAR: 'levelClear',
  GAME_OVER: 'gameOver',
  WIN: 'win',
  PAUSED: 'paused',
};

export function createGame(levelIndex = 0) {
  const field = { width: 480, height: 800 }; // logical units; renderer scales
  const g = {
    field,
    state: STATE.SERVE,
    levelIndex,
    score: 0,
    lives: 3,
    balls: [],
    bricks: [],
    capsules: [],
    paddle: createPaddle(field),
    mult: createMultiplier(),
    events: [],
    time: 0,
    slowT: 0,          // slow-motion power-up timer
    fireT: 0,
    heavyT: 0,
    shake: 0,          // screen shake energy 0..1
    flash: 0,          // white flash on power-up catch
    chaos: false,      // any ball above the brick field
    chaosT: 0,
    topY: 0,           // top of the brick field (set by layout)
    layout: null,
    launchedOnce: false,
    accum: 0,
  };
  loadLevel(g, levelIndex);
  return g;
}

export function loadLevel(g, index) {
  const level = LEVELS[index % LEVELS.length];
  g.levelIndex = index;
  const cols = Math.max(...level.rows.map(r => r.length));
  const gapX = 8, gapY = 8;
  const pad = 20;
  const width = (g.field.width - pad * 2 - (cols - 1) * gapX) / cols;
  const layout = {
    cols, gapX, gapY,
    left: pad,
    top: 70,
    width, height: 24,
  };
  g.layout = layout;
  g.topY = layout.top;
  g.bricks = B.makeBricks(level, layout);
  g.capsules = [];
  resetBalls(g);
  g.state = STATE.SERVE;
}

export function resetBalls(g) {
  const p = g.paddle;
  g.balls = [makeBall(p.x, p.y - 16, 0, 0, { stuck: true, stuckOffset: 0 })];
  g.fireT = 0; g.heavyT = 0;
}

// Launch all stuck balls (space / click / tap).
export function launch(g) {
  let did = false;
  for (const b of g.balls) {
    if (b.stuck) {
      b.stuck = false;
      const a = -Math.PI / 2 + (Math.random() * 0.5 - 0.25);
      const sp = 420;
      b.vx = Math.cos(a) * sp;
      b.vy = Math.sin(a) * sp;
      did = true;
      g.launchedOnce = true;
      g.events.push({ type: 'launch', x: b.x, y: b.y });
    }
  }
  return did;
}

// --------------------------------------------------------------- step
// Advance the simulation by dt seconds (wall time). Uses fixed timestep
// with a per-game accumulator; collision substeps inside moveBall.
export function step(g, dt, input) {
  g.events.length = 0;
  if (g.state === STATE.PAUSED || g.state === STATE.MENU ||
      g.state === STATE.GAME_OVER || g.state === STATE.WIN ||
      g.state === STATE.LEVEL_CLEAR) {
    g.shake = Math.max(0, g.shake - dt * 2);
    g.flash = Math.max(0, g.flash - dt * 3);
    return;
  }

  const scale = g.slowT > 0 ? 0.45 : 1;
  g.accum += Math.min(dt, MAX_FRAME_DT);
  const h = FIXED_DT * scale;

  while (g.accum >= FIXED_DT) {
    g.accum -= FIXED_DT;
    tick(g, h, input);
  }
}

function tick(g, dt, input) {
  g.time += dt;
  if (g.slowT > 0) g.slowT -= dt;
  if (g.fireT > 0) g.fireT -= dt;
  if (g.heavyT > 0) g.heavyT -= dt;
  g.shake = Math.max(0, g.shake - dt * 2.2);
  g.flash = Math.max(0, g.flash - dt * 3);

  updatePaddle(g.paddle, input, dt, g.field);
  B.updateBricks(g.bricks, dt);
  updateCapsules(g.capsules, dt, g.field);

  // Stuck balls ride the paddle
  for (const b of g.balls) {
    if (b.stuck) {
      b.x = g.paddle.x + b.stuckOffset;
      b.y = g.paddle.y - b.r - PADDLE.h / 2 - 1;
    }
  }

  // Physics substeps: split motion so even 2000px/s balls never skip a brick.
  for (const b of g.balls) {
    if (b.stuck) continue;
    const speed = Math.hypot(b.vx, b.vy);
    const maxMove = 4; // px per substep
    const n = Math.max(1, Math.ceil((speed * dt) / maxMove));
    const sdt = dt / n;
    for (let s = 0; s < n; s++) moveBall(g, b, sdt);
  }

  // Remove fallen balls
  const before = g.balls.length;
  g.balls = g.balls.filter(b => b.y < g.field.height + 40);
  if (g.balls.length < before) {
    g.events.push({ type: 'ballLost', count: before - g.balls.length });
    if (g.balls.length === 0) loseLife(g);
  }

  // Chaos state: any ball above the top of the brick field
  g.chaos = g.balls.some(b => !b.stuck && b.y + b.r < g.topY - 2);
  if (g.chaos) g.chaosT += dt; else g.chaosT = 0;

  updateMult(g.mult, dt, g.chaos);

  // Capsule catching
  const p = g.paddle;
  for (const c of g.capsules) {
    if (!c.alive) continue;
    if (Math.abs(c.x - p.x) < p.w / 2 + c.w / 2 &&
        Math.abs(c.y - p.y) < PADDLE.h / 2 + c.h / 2 + 4) {
      c.alive = false;
      applyPower(g, c.kind);
    }
  }
  g.capsules = g.capsules.filter(c => c.alive);

  // Level clear?
  if ((g.state === STATE.PLAY || g.state === STATE.SERVE) && !g.bricks.some(b => b.alive)) {
    g.state = STATE.LEVEL_CLEAR;
    g.events.push({ type: 'levelClear', level: g.levelIndex });
  }
}

function moveBall(g, ball, dt) {
  const dx = ball.vx * dt;
  const dy = ball.vy * dt;

  // Walls
  if (ball.x - ball.r + dx < 0) { ball.x = ball.r; ball.vx = Math.abs(ball.vx); wallHit(g, ball, 1); }
  if (ball.x + ball.r + dx > g.field.width) { ball.x = g.field.width - ball.r; ball.vx = -Math.abs(ball.vx); wallHit(g, ball, -1); }
  if (ball.y - ball.r + dy < 0) { ball.y = ball.r; ball.vy = Math.abs(ball.vy); wallHit(g, ball, -1); }

  // Paddle (only below the paddle center, moving down)
  const p = g.paddle;
  if (ball.vy > 0 && ball.y < p.y + 40) {
    const hit = circleVsRect(ball, { x: p.x, y: p.y, w: p.w, h: PADDLE.h }, dx, dy);
    if (hit && hit.ny < 0) {
      // magnet: catch and hold
      if (p.magnetT > 0) {
        ball.stuck = true;
        ball.stuckOffset = clamp(ball.x - p.x, -p.w / 2 + 8, p.w / 2 - 8);
        g.events.push({ type: 'magnetCatch', x: ball.x, y: ball.y });
      } else {
        const out = paddleReflect(ball, p);
        ball.vx = out.vx; ball.vy = out.vy;
      }
      ball.y = p.y - PADDLE.h / 2 - ball.r - 0.5;
      g.events.push({ type: 'paddleHit', x: ball.x, y: ball.y, lift: -p.vy });
    }
  }

  // Bricks — sweep against every alive brick, resolve the earliest hit.
  const bricks = g.bricks;
  let best = null, bestB = null;
  for (const br of bricks) {
    if (!B.brickSolid(br)) continue;
    const rect = { x: br.x + br.w / 2, y: br.y + br.h / 2, w: br.w, h: br.h };
    const hit = circleVsRect(ball, rect, ball.vx * dt, ball.vy * dt);
    if (hit && (!best || hit.t < best.t)) { best = hit; bestB = br; }
  }
  if (best && bestB) {
    resolveBrickHit(g, ball, bestB, best);
  } else {
    ball.x += dx; ball.y += dy;
  }
}

function wallHit(g, ball, nx) {
  g.events.push({ type: 'wallHit', x: ball.x, y: ball.y, nx });
}

function resolveBrickHit(g, ball, brick, hit) {
  const verdict = B.hitBreaksBrick(brick, ball);
  const speed = Math.hypot(ball.vx, ball.vy);

  if (!verdict.breaks) {
    // Bounce off (solid brick, wrong angle / too slow / phased is skipped
    // earlier by brickSolid so only angle & speed reach here).
    const r = reflect(ball.vx, ball.vy, hit.nx, hit.ny);
    ball.vx = r.vx; ball.vy = r.vy;
    ball.x = hit.x + hit.nx * (ball.r + 0.5);
    ball.y = hit.y + hit.ny * (ball.r + 0.5);
    g.events.push({
      type: 'brickReject', x: hit.x, y: hit.y, nx: hit.nx, ny: hit.ny,
      reason: verdict.reason, kind: brick.kind, hue: brick.hue,
    });
    return;
  }

  // Fireball pierces standard bricks without bouncing.
  const pierce = ball.fire && brick.kind === B.KIND.STANDARD;
  if (!pierce) {
    const r = reflect(ball.vx, ball.vy, hit.nx, hit.ny);
    ball.vx = r.vx; ball.vy = r.vy;
    ball.x = hit.x + hit.nx * (ball.r + 0.5);
    ball.y = hit.y + hit.ny * (ball.r + 0.5);
  }

  brick.alive = false;
  const base = B.baseScore(brick.kind);
  const pts = scoreFor(g.mult, base);
  g.score += pts;
  registerBreak(g.mult, g.chaos);

  g.events.push({
    type: 'brickBreak', x: hit.x, y: hit.y, nx: hit.nx, ny: hit.ny,
    kind: brick.kind, hue: brick.hue, pts, base,
    speed, pierce, chaos: g.chaos,
  });

  // Exploding bricks chain: destroy neighbours (ignores special rules),
  // and neighbours' neighbours chain recursively — a true chain reaction.
  if (brick.kind === B.KIND.EXPLODING) {
    const R = 78;
    const queue = [brick];
    const detonated = new Set([brick.id]);
    while (queue.length) {
      const cur = queue.shift();
      for (const other of g.bricks) {
        if (!other.alive || detonated.has(other.id)) continue;
        const ox = other.x + other.w / 2, oy = other.y + other.h / 2;
        if (Math.hypot(ox - hit.x, oy - hit.y) >= R &&
            Math.hypot(ox - (cur.x + cur.w / 2), oy - (cur.y + cur.h / 2)) >= R) continue;
        other.alive = false;
        detonated.add(other.id);
        const pts2 = scoreFor(g.mult, B.baseScore(other.kind));
        g.score += pts2;
        registerBreak(g.mult, g.chaos);
        g.events.push({ type: 'brickBreak', x: ox, y: oy, nx: 0, ny: -1,
          kind: other.kind, hue: other.hue, pts: pts2, base: B.baseScore(other.kind),
          speed: 0, pierce: false, chain: true, chaos: g.chaos });
        if (other.kind === B.KIND.EXPLODING) queue.push(other); // chain on
      }
    }
    g.shake = Math.min(1, g.shake + 0.35);
  }

  // Maybe drop a capsule
  if (Math.random() < 0.16) {
    g.capsules.push(makeCapsule(hit.x, hit.y, rollPower()));
  }

  g.shake = Math.min(1, g.shake + (g.chaos ? 0.06 : 0.03) + (pierce ? 0.05 : 0));
}

export function applyPower(g, kind) {
  g.events.push({ type: 'powerCatch', kind, label: POWER_META[kind].label, hue: POWER_META[kind].hue });
  g.flash = 1;
  g.shake = Math.min(1, g.shake + 0.12);
  switch (kind) {
    case POWER.MULTI: {
      const src = g.balls[0];
      if (src) {
        const sp = Math.max(360, Math.hypot(src.vx, src.vy));
        const a = Math.atan2(src.vy, src.vx);
        for (const da of [-0.5, 0.5]) {
          const nb = makeBall(src.x, src.y, Math.cos(a + da) * sp, Math.sin(a + da) * sp,
            { fire: g.fireT > 0, heavy: g.heavyT > 0 });
          g.balls.push(nb);
        }
      }
      break;
    }
    case POWER.FIRE: g.fireT = TIMERS[POWER.FIRE]; break;
    case POWER.HEAVY: g.heavyT = TIMERS[POWER.HEAVY]; break;
    case POWER.WIDE: g.paddle.wideT = TIMERS[POWER.WIDE]; break;
    case POWER.SLOW: g.slowT = TIMERS[POWER.SLOW]; break;
    case POWER.MAGNET: g.paddle.magnetT = TIMERS[POWER.MAGNET]; break;
    case POWER.LIFE: g.lives += 1; break;
  }
}

export function loseLife(g) {
  g.lives -= 1;
  if (g.lives <= 0) {
    g.state = STATE.GAME_OVER;
    g.events.push({ type: 'gameOver' });
  } else {
    g.state = STATE.SERVE;
    resetBalls(g);
    g.events.push({ type: 'lifeLost', lives: g.lives });
  }
}

export function nextLevel(g) {
  if (g.levelIndex + 1 >= LEVELS.length) {
    g.state = STATE.WIN;
    g.events.push({ type: 'win' });
  } else {
    loadLevel(g, g.levelIndex + 1);
    g.events.push({ type: 'levelStart', level: g.levelIndex, name: LEVELS[g.levelIndex].name });
  }
}

export function pauseToggle(g) {
  if (g.state === STATE.PLAY || g.state === STATE.SERVE) {
    g.prevState = g.state;
    g.state = STATE.PAUSED;
  } else if (g.state === STATE.PAUSED) {
    g.state = g.prevState === STATE.SERVE ? STATE.SERVE : STATE.PLAY;
  }
}
