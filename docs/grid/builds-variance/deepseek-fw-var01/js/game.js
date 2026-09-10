/* ============================================================
   TOPSIDE — game.js
   Core game logic: physics, collisions, bricks, powerups,
   combos, charge/FIRE (cannon + riding), lives, scoring.
   No rendering here — render.js and input.js only talk to S
   and the exported G / GL globals.
   ============================================================ */
'use strict';

/* ---------- exported timing constants (consistent with CFG) ---------- */
const FLUX_MAX   = CFG.flux.max;      // 700  max flux-ball speed
const FLUX_DUR   = CFG.flux.dur;      // 9s   flux ball duration
const FLUX_POWER = CFG.flux.power;    // 5.5  flux pierce rating
const PEARL_DUR  = CFG.pearl.dur;     // 8s   pearl ball duration
const PEARL_POWER = CFG.pearl.power;  // 5.0  pearl smash rating
const PEARL_MAX  = CFG.pearl.max;     // 650  max pearl-ball speed
const CANNON_DUR = CFG.cannon.wait;   // 1.35s cannon charge/aim
const CANNON_V   = CFG.cannon.v;      // 1500 cannonball climb speed
const CANNON_R   = CFG.cannon.r;      // 7    cannonball radius
const RIDING_DUR = 14;                // s the topside "riding" ball lasts
const TIME_CYCLE = 10;                // s per time-brick open/close cycle

/* render helpers */
const HUE_INDEX = { p: 0, o: 1, y: 2, g: 3, c: 4, b: 5, v: 6, w: 3 };
function brickHueIndex(brick) {
  const h = HUE_INDEX[brick.c];
  return h !== undefined ? h : 3;
}
function popColor() {
  return PAL.trail[(((S.combo / 5) | 0) - 1 + 10) % PAL.trail.length];
}

/* fields game.js adds to S (core defines the rest); initialize eagerly so
   render/input never see undefined before the first resetGame() */
S.ridingT = 0;
S.ridingBall = null;
S.crown = 0;
S.paddle.grabStuck = false;
S.paddle.h = CFG.paddleH;   /* core's S.paddle lacks h — add it */
S.hitStop = 0;
const PADDLE_H = CFG.paddleH;

/* ---------- hi-score (localStorage, safe when unavailable) ---------- */
function loadHi() {
  try {
    if (typeof localStorage !== 'undefined') {
      const v = parseInt(localStorage.getItem('topside_hi'), 10);
      if (v > 0) return v;
    }
  } catch (e) { /* ignore */ }
  return 0;
}
function saveHi() {
  try {
    if (typeof localStorage !== 'undefined') localStorage.setItem('topside_hi', String(S.hi));
  } catch (e) { /* ignore */ }
}

/* ---------- powerup rolling (weighted) ---------- */
const POWERUP_WEIGHTS = [
  ['multi', 18], ['big', 18], ['slow', 14], ['grab', 12],
  ['flux', 10], ['pearl', 10], ['bomb', 8], ['life', 4], ['mega', 6]
];
function rollPowerupType() {
  let r = RNG.next() * 100;
  for (let i = 0; i < POWERUP_WEIGHTS.length; i++) {
    r -= POWERUP_WEIGHTS[i][1];
    if (r <= 0) return POWERUP_WEIGHTS[i][0];
  }
  return 'multi';
}

/* index helper: brick at (col,row) in S.bricks, or null */
function brickAt(col, row) {
  for (let i = 0; i < S.bricks.length; i++) {
    const b = S.bricks[i];
    if (b.col === col && b.row === row) return b;
  }
  return null;
}

/* ---------- game lifecycle ---------- */
function resetGame() {
  S.score = 0;
  S.lives = 3;
  S.bombs = 0;
  S.combo = 0; S.comboT = 0; S.mr = 1;
  S.charge = 0; S.fireReady = false;
  S.hi = loadHi();   /* persistent across runs */
  S.balls = [];
  S.bricks = [];
  S.brickMap.clear();
  S.powerups = [];
  S.cannon = null;
  S.mode.flux = 0; S.mode.pearl = 0; S.mode.slow = 0; S.mode.freeze = 0;
  S.modeCastle = false;
  S.countdown = 0; S.serveT = 0; S.overT = 0; S.winT = 0;
  S.nextBall = null;
  S.shake = 0; S.flash = 0;
  S.comboPops.length = 0;
  S.level = 1; S.bricksLeft = 0;
  S.totals.bricks = 0; S.totals.balls = 0;
  S.ridingT = 0; S.ridingBall = null; S.crown = 0;
  S.paddle.x = CFG.W / 2;
  S.paddle.y = CFG.paddleY;
  S.paddle.w = CFG.paddleW;
  S.paddle.vx = 0; S.paddle.vy = 0;
  S.paddle.target = null;
  S.paddle.grab = false; S.paddle.magnetT = 0; S.paddle.flash = 0;
  S.paddle.grabStuck = false;
  S.state = 'menu';
  FX.clear();
  AUD.music.setIntensity(0.05);
}

function startGame() {
  resetGame();
  startLevel(1, { castle: false });
}

function startLevel(levelIdx, opts) {
  opts = opts || {};
  const idx = clamp(levelIdx | 0, 1, LEVELS.length);
  S.level = idx;
  S.modeCastle = !!opts.castle;
  buildBricks(idx);
  S.balls = [];
  S.powerups = [];
  S.cannon = null;
  S.mode.flux = 0; S.mode.pearl = 0; S.mode.slow = 0; S.mode.freeze = 0;
  S.ridingT = 0; S.ridingBall = null; S.crown = 0;
  S.countdown = 0.9;
  S.serveT = 0;
  S.paddle.x = CFG.W / 2;
  S.paddle.y = CFG.paddleY;
  S.paddle.w = CFG.paddleW;
  S.paddle.vx = 0; S.paddle.vy = 0;
  S.paddle.target = null;
  S.paddle.grab = false; S.paddle.magnetT = 0; S.paddle.flash = 0;
  S.paddle.grabStuck = false;
  S.state = 'playing';
  addServeBall();
  AUD.sfx.levelup();
  FX.confetti();
  AUD.music.setIntensity(clamp(0.15 + idx * 0.1, 0, 1));
}

function buildBricks(levelIdx) {
  const lv = LEVELS[levelIdx - 1];
  S.bricks = [];
  S.brickMap.clear();
  if (!lv) { S.bricksLeft = 0; return; }
  let id = 0;
  const rows = lv.rows;
  for (let r = 0; r < rows.length; r++) {
    const line = rows[r];
    for (let c = 0; c < line.length; c++) {
      const ch = line[c];
      if (ch === '.' || ch === ' ') continue;
      const def = BRICK_DEFS[ch];
      if (!def) continue;
      const b = {
        id: id,
        x: CFG.brickLeft + c * (CFG.brickW + CFG.brickGap),
        y: CFG.brickTop + r * (CFG.brickH + CFG.brickGap),
        w: CFG.brickW, h: CFG.brickH,
        col: c, row: r,
        ch: ch,
        kind: def.kind, c: def.c,
        hp: Math.max(1, def.hp | 0),
        pts: def.pts | 0,
        tol: def.tol !== undefined ? def.tol : 0.55,
        need: def.need || 560,
        phase: def.phase ? [def.phase[0], def.phase[1]] : null,
        big: !!def.big,
        mate: null
      };
      S.bricks.push(b);
      S.brickMap.set(id, b);
      id++;
    }
  }
  S.bricksLeft = S.bricks.length;
  S.totals.bricks += S.bricks.length;
  resolveShieldMates();
}

/* Pair shield bricks per the level-design convention:
   - 'S' (core) is invulnerable while a 's' guard brick is alive (its mate).
   - 's' (guard) bricks are always destructible.
   So level-4 hearts are blocked until you clear the guards below them. */
function resolveShieldMates() {
  const guards = [];
  const cores = [];
  for (const b of S.bricks) {
    if (b.kind !== 'shield') continue;
    if (b.ch === 'S') cores.push(b);
    else guards.push(b);
  }
  /* if a level uses only one shield char, treat all as destructible guards */
  for (const core of cores) {
    let best = null, bestD = 1e9;
    for (const g of guards) {
      if (g.dead) continue;
      const dc = Math.abs(g.col - core.col), dr = Math.abs(g.row - core.row);
      if (dr > 0 && dc <= 1) {           /* guard must be below/around the core */
        const d = dc * 10 + dr;
        if (d < bestD) { bestD = d; best = g; }
      }
    }
    if (best) core.mate = best.id;
  }
}

/* ---------- serving / paddle / ball helpers ---------- */

/* Occupy the paddle: put the next ball on top of the paddle, sticky. */
function placeBallOnPaddle(ball) {
  ball.x = S.paddle.x;
  ball.y = S.paddle.y - PADDLE_H / 2 - ball.r - 2;
  ball.vx = 0; ball.vy = 0;
  ball.deflected = false;
  ball.lastBrick = -1;
}

function serveBall() {
  const ball = S.balls[0];
  if (!ball) return;
  S.countdown = 0;
  /* angle from paddle center: slightly toward where the paddle points */
  let dx = 0;
  if (S.paddle.target) dx = clamp((S.paddle.target.x - S.paddle.x) / (CFG.W / 2), -1, 1) * 0.35;
  else dx = RNG.range(-0.2, 0.2);
  ball.x = S.paddle.x;
  ball.y = S.paddle.y - PADDLE_H / 2 - ball.r;
  ball.vx = dx * CFG.ballLaunch * 0.55;
  ball.vy = -Math.sqrt(Math.max(0, CFG.ballLaunch * CFG.ballLaunch - ball.vx * ball.vx));
  if (S.paddle.vy < 0) ball.vy -= Math.abs(S.paddle.vy) * CFG.slideFactor;
  ball.deflected = false;
  ball.lastBrick = -1;
  S.serveT = 0.2;
  AUD.sfx.launch();
  FX.burst(ball.x, ball.y, {
    colors: ['#9ff6ff', '#ffffff'], count: 10, speed: [40, 180],
    size: [2, 4], life: [0.3, 0.6], glow: true
  });
}

/* The per-frame controller for the paddle (from input target).
   Velocities are px/s; CFG tunings are per-frame so scale by 60. */
function updatePaddle(dt) {
  const p = S.paddle;
  const vmax = CFG.paddleVmax * 60;
  const vmaxY = CFG.paddleVmax * 0.7 * 60;
  const t = p.target;
  if (t) {
    const ax = clamp((t.x - p.x) * CFG.paddleAccel, -vmax, vmax);
    p.vx += ax * 60 * dt;
    if (t.y !== undefined && t.y !== null) {
      const ay = clamp((t.y - p.y) * CFG.paddleAccel * 0.8, -vmaxY, vmaxY);
      p.vy += ay * 60 * dt;
    }
  }
  const drag = Math.pow(CFG.paddleDrag, dt * 60);
  p.vx *= drag;
  p.vy *= drag;
  p.vx = clamp(p.vx, -vmax, vmax);
  p.vy = clamp(p.vy, -vmaxY, vmaxY);
  p.x += p.vx * dt;
  p.y += p.vy * dt;
  const hw = p.w / 2;
  p.x = clamp(p.x, CFG.leftWall + hw, CFG.rightWall - hw);
  p.y = clamp(p.y, CFG.paddleMinY, CFG.paddleMaxY);
  if (p.flash > 0) p.flash -= dt;
  if (Math.abs(p.vx) < 12 && Math.abs(p.vy) < 12 && !t) { p.vx = 0; p.vy = 0; }
}

/* reflect a ball off a wall / general normal with restitution e */
function reflectBall(ball, nx, ny, e) {
  const dot = ball.vx * nx + ball.vy * ny;
  if (dot < 0) {
    ball.vx -= (1 + e) * dot * nx;
    ball.vy -= (1 + e) * dot * ny;
  }
}

function clampBallSpeed(ball) {
  const sp = ball.speed;
  const cap = ball.type === 'flux' ? FLUX_MAX : (ball.type === 'pearl' ? PEARL_MAX : CFG.ballMax);
  if (sp > cap) ball.setSpeed(cap);
  else if (sp < CFG.ballMin && sp > 1e-3) {
    /* keep slow-mo balls alive but not totally stuck */
    ball.setSpeed(CFG.ballMin * 0.8);
  }
  /* never allow perfectly horizontal/vertical motion (anti-stuck) */
  const minC = 90;
  let changed = false;
  if (Math.abs(ball.vx) < minC) { ball.vx = ball.vx >= 0 ? minC : -minC; changed = true; }
  if (Math.abs(ball.vy) < minC && ball.vy !== 0) { ball.vy = ball.vy < 0 ? -minC : minC; changed = true; }
  if (changed) {
    const cur = ball.speed;
    const c2 = ball.type === 'flux' ? FLUX_MAX : (ball.type === 'pearl' ? PEARL_MAX : CFG.ballMax);
    if (cur > c2) ball.setSpeed(c2);
  }
  /* de-pinning: if it's moving almost straight vertical at a fixed x for a while,
     nudge it sideways so it can't tunnel a single column forever */
  if (ball.type === 'flux' && Math.abs(ball.vx) < 130 && Math.abs(ball.vy) > 550) {
    ball.vx += (ball.x > CFG.W / 2 ? 1 : -1) * 140 * RNG.range(0.6, 1);
  }
}

/* paddle-ball collision. Returns true if hit. */
function collidePaddle(ball, dt) {
  const p = S.paddle;
  if (ball.vy <= 0) return false;
  if (ball.y + ball.r < p.y - PADDLE_H / 2) return false;
  if (ball.y > p.y + PADDLE_H / 2 + ball.r + 8) return false;
  if (ball.x < p.x - p.w / 2 - ball.r || ball.x > p.x + p.w / 2 + ball.r) return false;
  /* crossover check: was above paddle top, now at/below it */
  const top = p.y - PADDLE_H / 2;
  if (ball.prevY !== undefined && ball.prevY < top && ball.y >= top) {
    /* hit! */
  } else if (ball.y >= top) {
    /* already deep — allow direct overlap catch */
  } else return false;

  /* grabber: stick */
  if (p.grab || p.magnetT > 0) {
    p.grabStuck = true;
    ball.type = 'normal';
    placeBallOnPaddle(ball);
    ball.vy = 0; ball.vx = 0;
    p.flash = 0.3;
    AUD.sfx.paddle();
    return true;
  }

  const dx = clamp((ball.x - p.x) / (p.w / 2), -1, 1);
  const angle = dx * (Math.PI / 3) - Math.PI / 2; /* up-ish */
  const sp = Math.max(ball.speed, CFG.ballLaunch);
  ball.vx = Math.cos(angle) * sp;
  ball.vy = Math.sin(angle) * sp;
  /* paddle velocity imparts extra energy */
  ball.vx += p.vx * 0.5;
  ball.vy += p.vy * CFG.slideFactor;
  /* ensure moving up */
  if (ball.vy > -90) ball.vy = -90;
  ball.y = top - ball.r - 1;
  ball.deflected = false;
  ball.lastBrick = -1;
  clampBallSpeed(ball);
  p.flash = 0.15;
  AUD.sfx.paddle();
  FX.burst(ball.x, top, {
    colors: ['#9ff6ff', '#ffffff'], count: 8, speed: [40, 160],
    size: [2, 4], life: [0.25, 0.5], glow: true
  });
  return true;
}

function releaseStuckBall() {
  const p = S.paddle;
  if (!p.grabStuck) return;
  const ball = S.balls[0];
  if (!ball) { p.grabStuck = false; return; }
  p.grabStuck = false;
  const sp = CFG.ballLaunch + (p.vy < 0 ? Math.abs(p.vy) * 3 : 0);
  ball.x = p.x;
  ball.y = p.y - PADDLE_H / 2 - ball.r;
  ball.vx = clamp((ball.x - p.x) * 0.5 + RNG.range(-30, 30), -200, 200);
  ball.vy = -sp;
  ball.deflected = false;
  ball.lastBrick = -1;
  clampBallSpeed(ball);
  AUD.sfx.launch();
  FX.burst(ball.x, ball.y, {
    colors: ['#ffd84d', '#ffffff'], count: 12, speed: [60, 240],
    size: [2, 5], life: [0.3, 0.6], glow: true
  });
}

/* ---------- scoring ---------- */
function addScore(n, x, y) {
  const v = Math.max(0, Math.round(n | 0));
  if (v <= 0) return;
  S.score += v;
  if (S.score > S.hi) { S.hi = S.score; }
  if (x !== undefined && y !== undefined) {
    FX.text(x, y, '+' + v, {
      life: 0.8, size: 20, color: '#fffbe0', vy: -60, glow: true
    });
  }
}

/* ---------- bricks ---------- */

/* destroy a brick (with scoring / fx). Called from any destroy path. */
function destroyBrick(b, silent) {
  if (b.dead) return;
  b.dead = true;
  S.bricksLeft--;
  const idx = S.bricks.indexOf(b);
  if (idx >= 0) S.bricks.splice(idx, 1);
  S.brickMap.delete(b.id);
  const col = PAL.brick[b.c] ? PAL.brick[b.c].fill : '#ffffff';
  const colors = [col, PAL.brick[b.c] ? PAL.brick[b.c].edge : '#fff'];
  if (!silent) {
    FX.brickPop(b.x + b.w / 2, b.y + b.h / 2, colors);
    AUD.sfx.brick(brickHueIndex(b));
  }
  /* combo / charge on player-initiated breaks */
  let mrNow = S.mr;
  if (!silent) {
    S.combo++;
    S.comboT = CFG.comboLinger;
    mrNow = Math.min(10, 1 + Math.floor(S.combo / 5));
    S.mr = mrNow;
    /* charge: base rate scaled by brick value & combo so FIRE is reachable */
    const gain = CFG.chargeRate * (3.0 + (b.pts || CFG.pointsBase) / CFG.pointsBase) * (0.9 + mrNow * 0.2);
    S.charge = Math.min(CFG.maxCharge, S.charge + gain);
    if (S.charge >= CFG.maxCharge && !S.fireReady) {
      S.fireReady = true;
      AUD.sfx.charged();
      FX.text(S.paddle.x, S.paddle.y - 60, 'POWER READY', {
        life: 1.4, size: 28, color: '#ffe14d', vy: -40, glow: true
      });
    }
    /* combo milestone popup */
    if (S.combo > 0 && S.combo % 5 === 0) {
      const c = popColor();
      FX.text(b.x + b.w / 2, b.y - 6, 'COMBO x' + mrNow, {
        life: 1.0, size: 22, color: c, vy: -60, glow: true
      });
      AUD.sfx.combo(mrNow);
      FX.burst(b.x + b.w / 2, b.y - 6, { colors: [c], count: 8, speed: [40, 160], size: [2, 4], life: [0.4, 0.8], glow: true });
    }
    maybeSpawnPowerup(b);
  }
  const pts = b.pts || 0;
  addScore(pts * mrNow, b.x + b.w / 2, b.y);
  if (S.bricksLeft <= 0) {
    if (S.modeCastle || S.ridingBall) {
      /* castle or riding victory */
      if (S.ridingBall) endRiding();
      S.state = 'win';
      S.winT = 3;
      AUD.sfx.win();
      FX.confetti();
      AUD.music.setIntensity(0);
      saveHi();
    } else {
      const next = S.level + 1;
      if (next > LEVELS.length) {
        S.state = 'win';
        S.winT = 3;
        AUD.sfx.win();
        FX.confetti();
        AUD.music.setIntensity(0.05);
        saveHi();
      } else {
        startLevel(next, { castle: false });
      }
    }
  }
}

/* rolling for powerups on a destroyed brick */
function maybeSpawnPowerup(b) {
  if (!RNG.chance(0.14)) return;
  const type = rollPowerupType();
  const pu = new Powerup(b.x + b.w / 2, b.y + b.h / 2, type);
  pu.vy = 230;
  S.powerups.push(pu);
}

/* full contact-circle/rect test; returns overlap depth vector or null */
function circleRectHit(ball, b) {
  const cx = clamp(ball.x, b.x, b.x + b.w);
  const cy = clamp(ball.y, b.y, b.y + b.h);
  const dx = ball.x - cx, dy = ball.y - cy;
  const d2 = dx * dx + dy * dy;
  if (d2 >= ball.r * ball.r) return null;
  return { dx, dy, d: Math.sqrt(d2) || 0.001, cx, cy };
}

function reflectOffBrick(ball, b, mode) {
  const r = circleRectHit(ball, b);
  if (!r) return;
  /* dominant axis from the closest point */
  let nx = 0, ny = 0;
  const px = ball.x, py = ball.y;
  const ox = clamp(px, b.x, b.x + b.w), oy = clamp(py, b.y, b.y + b.h);
  if (px < b.x) nx = -1; else if (px > b.x + b.w) nx = 1;
  else nx = 0;
  if (py < b.y) ny = -1; else if (py > b.y + b.h) ny = 1;
  else ny = 0;
  if (nx === 0 && ny === 0) nx = 1;
  const e = mode.e !== undefined ? mode.e : 1;
  reflectBall(ball, nx, ny, e);
  /* push out of the brick */
  const push = ball.r - r.d + 0.5;
  if (Math.abs(nx) > Math.abs(ny)) ball.x += nx * push;
  else ball.y += ny * push;
}

/* override of default restitution per kind */
function kindRes(kind) {
  switch (kind) {
    case 'mirror': return 1.1;
    case 'speed': return 0.85;
    case 'glass': return 0.85;
    case 'shield': return 1.0;
    default: return 1.0;
  }
}

/* The "hit a brick" switch — returns true if the ball should be reflected
   (i.e. did NOT pass through / did NOT destroy on one-touch). */
function hitBrick(ball, b) {
  const kind = b.kind;
  const r = circleRectHit(ball, b);
  if (!r) return false;

  /* riding cannonball obliterates everything */
  if (ball.type === 'cannon') {
    FX.shock(ball.x, ball.y, { r1: b.big ? 260 : 150, life: 0.45, color: '#ffe14d', lw: 6 });
    FX.burst(ball.x, ball.y, { colors: ['#ffe14d', '#ffffff'], count: 20, speed: [120, 420], size: [3, 7], life: [0.4, 0.9], glow: true });
    AUD.sfx.bomb();
    AUD.sfx.shatter();
    destroyBrick(b, true);          // silent: no combo
    return false;                   // cannon passes through
  }
  if (ball.type === 'flux') {
    destroyBrick(b, false);         // pierces: no reflect
    return false;
  }
  /* pearl: smashes the brick AND reflects with amplification */
  if (ball.type === 'pearl') {
    const col = PAL.brick[b.c] ? PAL.brick[b.c].fill : '#ffffff';
    destroyBrick(b, false);
    reflectOffBrick(ball, b, { e: 1.2 });
    FX.burst(ball.x, ball.y, { colors: [col, '#ffffff'], count: 10, speed: [80, 300], size: [2, 5], life: [0.3, 0.7], glow: true });
    AUD.sfx.solid();
    return false; /* already reflected; don't run generic logic */
  }

  /* time brick: solid fog outside phase window */
  if (kind === 'time' && b.phase) {
    const ph = b.phase;
    const tt = (S.time % TIME_CYCLE) / TIME_CYCLE;
    const open = tt >= ph[0] && tt <= ph[1];
    if (!open) {
      reflectOffBrick(ball, b, { e: 1.0 });
      AUD.sfx.solid();
      FX.burst(ball.x, ball.y, { colors: ['#5b8cff', '#8fb2ff'], count: 5, speed: [30, 120], size: [1.5, 3], life: [0.2, 0.4], glow: true });
      return true;
    }
  }

  switch (kind) {
    case 'mirror': {
      reflectOffBrick(ball, b, { e: 1.1 });
      AUD.sfx.solid();
      FX.burst(ball.x + b.w / 2, ball.y, { colors: ['#b05cff', '#cc8fff'], count: 10, speed: [80, 260], size: [2, 4], life: [0.3, 0.6], glow: true });
      return true;
    }
    case 'shield': {
      const mate = b.mate !== null ? S.brickMap.get(b.mate) : null;
      if (mate && !mate.dead) {
        reflectOffBrick(ball, b, { e: 1.0 });
        AUD.sfx.solid();
        FX.burst(ball.x, ball.y, { colors: ['#22d3ee', '#7fecff'], count: 8, speed: [70, 240], size: [2, 4], life: [0.3, 0.6], glow: true });
        return true;
      }
      /* mate dead: behave normal */
      b.hp--;
      if (b.hp <= 0) { destroyBrick(b, false); }
      else {
        reflectOffBrick(ball, b, { e: 1.0 });
        FX.brickHit(ball.x, ball.y, [PAL.brick[b.c] ? PAL.brick[b.c].fill : '#fff'], { dx: -ball.vx, dy: -ball.vy });
      }
      return true;
    }
    case 'angle': {
      /* only take hit near-horizontal */
      const normVx = Math.abs(ball.vx) / (ball.speed || 1);
      const tol = b.tol || 0.55;
      if (normVx >= 1 - tol) {
        destroyBrick(b, false);
        return false;
      }
      reflectOffBrick(ball, b, { e: 1.0 });
      AUD.sfx.brick(0);
      FX.burst(ball.x, ball.y, { colors: ['#4ade80', '#8af0b0'], count: 4, speed: [30, 120], size: [1.5, 3], life: [0.2, 0.4], glow: true });
      return true;
    }
    case 'speed': {
      const need = b.need || 560;
      if (ball.speed >= need) {
        destroyBrick(b, false);
        return false;
      }
      reflectOffBrick(ball, b, { e: 0.85 });
      AUD.sfx.wall();
      FX.burst(ball.x, ball.y, { colors: ['#ffd84d', '#ffe58a'], count: 5, speed: [30, 130], size: [1.5, 3], life: [0.2, 0.4], glow: true });
      return true;
    }
    case 'bomb': {
      /* explode now */
      const big = !!b.big;
      destroyBrick(b, false);
      blastBricks(b.x + b.w / 2, b.y + b.h / 2, big ? 170 : 110, big);
      S.shake = Math.min(CFG.shake.max * 1.4, S.shake + (big ? 14 : 9));
      FX.shock(b.x + b.w / 2, b.y + b.h / 2, {
        r1: big ? 260 : 150, life: 0.55, color: '#ff3b6b', lw: 7
      });
      FX.burst(b.x + b.w / 2, b.y + b.h / 2, {
        colors: ['#ff3b6b', '#ffd84d', '#ffffff'], count: 40,
        speed: [120, 480], size: [3, 7], life: [0.5, 1.1], grav: 300, glow: true
      });
      AUD.sfx.bomb();
      AUD.sfx.shatter();
      return false;
    }
    case 'glass': {
      destroyBrick(b, false);
      glassChain(b);
      return false;
    }
    default: { /* normal / tough */
      b.hp--;
      if (b.hp <= 0) {
        destroyBrick(b, false);
        return false;
      }
      reflectOffBrick(ball, b, { e: 1.0 });
      FX.brickHit(ball.x, ball.y, [PAL.brick[b.c] ? PAL.brick[b.c].fill : '#fff'], { dx: -ball.vx, dy: -ball.vy });
      AUD.sfx.brick(brickHueIndex(b));
      return true;
    }
  }
  return true;
}

/* bomb explosion: destroy every brick within radius (no combo, no powerups) */
function blastBricks(cx, cy, radius, big) {
  const killed = [];
  for (const b of S.bricks) {
    const bx = b.x + b.w / 2, by = b.y + b.h / 2;
    const d = Math.hypot(bx - cx, by - cy);
    if (d <= radius) killed.push(b);
  }
  for (const b of killed) {
    if (b.dead) continue;
    /* don't chain bombs infinitely: bombs in radius just die no extra boom */
    const col = PAL.brick[b.c] ? PAL.brick[b.c].fill : '#ffffff';
    FX.brickPop(b.x + b.w / 2, b.y + b.h / 2, [col, '#fff']);
    AUD.sfx.shatter();
    destroyBrick(b, true);
  }
}

/* glass chain: random neighbor chain, bounded depth */
function glassChain(b, depth) {
  depth = depth || 0;
  if (depth >= 6) return;
  if (S.bricksLeft <= 0) return;
  if (!RNG.chance(0.3)) return;
  /* 4-dir neighbors */
  const cands = [];
  for (const o of S.bricks) {
    const dc = Math.abs(o.col - b.col), dr = Math.abs(o.row - b.row);
    if (dc + dr === 1) cands.push(o);
  }
  if (!cands.length) return;
  const n = RNG.pick(cands);
  if (n.dead || n.kind !== 'glass') return;
  destroyBrick(n, false);
  FX.text(n.x + n.w / 2, n.y + n.h / 2 - 4, 'CRACK!', {
    life: 0.5, size: 14, color: '#7fecff', vy: -50, glow: false
  });
  glassChain(n, depth + 1);
}

/* fresh ball sitting on the paddle, ready for countdown serve */
function addServeBall() {
  const nb = new Ball(S.paddle.x, S.paddle.y - PADDLE_H / 2 - CFG.ballR - 2, 0, 0);
  nb.deflected = false;
  nb.lastBrick = -1;
  S.balls = [nb];
  return nb;
}

/* ---------- ball integration ---------- */

/* update one ball: move, collide with walls/ceiling/paddle/bricks.
   Returns false if the ball died (was removed). */
function updateBall(ball, dt) {
  /* sitting on the paddle (stuck or countdown): no physics */
  if (ball.vx === 0 && ball.vy === 0 && ball.type !== 'cannon') return true;

  /* anti-stuck: a ball pinned in roughly the same vertical band for 4s+
     (e.g. trapped bouncing between a brick underside and the paddle top)
     gets a mild vertical nudge so it climbs out instead of droning forever. */
  const band = Math.round(ball.y / 36);
  if (band !== ball._stkBand) {
    ball._stkBand = band;
    ball._stkT = 0;
  } else {
    ball._stkT += dt;
    if (ball._stkT > 4) {
      ball._stkT = 0;
      ball.vy += (ball.vy >= 0 ? 1 : -1) * 40;
      FX.burst(ball.x, ball.y, { colors: ['#9ff6ff'], count: 3, speed: [20, 80], size: [1.5, 3], life: [0.2, 0.4], glow: true });
    }
  }

  /* trail */
  for (let i = 0; i < CFG.flameCount; i++) {
    ball.trail.push({ x: ball.x - ball.vx * dt * i, y: ball.y - ball.vy * dt * i, life: 0.5 });
  }
  if (ball.trail.length > 46) ball.trail.splice(0, ball.trail.length - 46);

  const fast = ball.speed > 700;
  const steps = fast ? 2 : 1;
  for (let s = 0; s < steps; s++) {
    const sdt = dt / steps;
    ball.x += ball.vx * sdt;
    ball.y += ball.vy * sdt;

    /* --- walls --- */
    if (ball.x - ball.r < CFG.leftWall) {
      ball.x = CFG.leftWall + ball.r;
      ball.vx = Math.abs(ball.vx);
      AUD.sfx.wall();
      FX.burst(ball.x, ball.y, { colors: ['#9ff6ff'], count: 5, speed: [30, 120], size: [1.5, 3], life: [0.2, 0.4], glow: true });
      if (ball.speed > 800) S.shake = Math.min(CFG.shake.max, S.shake + 2.2);
    } else if (ball.x + ball.r > CFG.rightWall) {
      ball.x = CFG.rightWall - ball.r;
      ball.vx = -Math.abs(ball.vx);
      AUD.sfx.wall();
      FX.burst(ball.x, ball.y, { colors: ['#9ff6ff'], count: 5, speed: [30, 120], size: [1.5, 3], life: [0.2, 0.4], glow: true });
      if (ball.speed > 800) S.shake = Math.min(CFG.shake.max, S.shake + 2.2);
    }

    /* --- cannon: exits the brick field -> riding --- */
    if (ball.type === 'cannon') {
      if (ball.y < CFG.brickTop - 40) {
        enterRiding(ball);
        return true;
      }
      /* the cannon smashes any brick it touches (see brick loop below);
         it never bounces off the ceiling and never reaches the paddle */
      if (ball.y - ball.r < 30) { ball.y = 30 + ball.r; ball.vy = Math.abs(ball.vy); }
      /* still hit bricks in this substep */
      if (S.bricks.length) {
        for (const b of S.bricks) {
          if (b.dead) continue;
          if (circleRectHit(ball, b)) {
            const big = !!b.big;
            FX.shock(ball.x, ball.y, { r1: big ? 260 : 150, life: 0.45, color: '#ffe14d', lw: 6 });
            FX.burst(ball.x, ball.y, { colors: ['#ffe14d', '#ffffff'], count: 22, speed: [120, 460], size: [3, 7], life: [0.4, 0.9], glow: true });
            AUD.sfx.bomb();
            AUD.sfx.shatter();
            destroyBrick(b, false);
          }
        }
      }
      clampBallSpeed(ball);
      continue;
    }

    /* --- ceiling --- */
    if (ball.y - ball.r < 200) {
      /* hard ceiling well above the bricks so the riding band owns the top */
      ball.y = 200 + ball.r;
      if (ball.vy < 0) ball.vy = -ball.vy;
      AUD.sfx.ceiling();
      FX.burst(ball.x, ball.y, { colors: ['#b05cff'], count: 5, speed: [30, 120], size: [1.5, 3], life: [0.2, 0.4], glow: true });
    }

    /* --- paddle --- */
    if (collidePaddle(ball, sdt)) return true;

    /* --- bricks --- */
    if (S.bricks.length) {
      const hits = [];
      for (const b of S.bricks) {
        const r = circleRectHit(ball, b);
        if (r) hits.push({ b, r });
      }
      for (const h of hits) {
        if (h.b.dead) continue;
        if (ball.type === 'flux' || ball.type === 'cannon') {
          /* flux & cannon pierce: destroy what they touch, no reflect */
          h.b.hp = 1;
          destroyBrick(h.b, false);
          continue;
        }
        if (ball.type === 'pearl') {
          /* pearl handled fully inside hitBrick (destroy + e=1.2 reflect) */
          hitBrick(ball, h.b);
          break;
        }
        hitBrick(ball, h.b);
        break; /* one brick per substep for normal balls */
      }
    }
    clampBallSpeed(ball);
  }

  /* --- bottom: lose ball --- */
  if (ball.y > CFG.H + 40) {
    loseBall(ball, dt);
    return false;
  }
  return true;
}

function loseBall(ball) {
  const i = S.balls.indexOf(ball);
  if (i >= 0) S.balls.splice(i, 1);
  AUD.sfx.loseBall();
  FX.shock(ball.x, CFG.H - 60, { r1: 160, life: 0.5, color: '#ff3b6b', lw: 6 });
  S.flash = Math.max(S.flash, 0.18);
  if (S.balls.length === 0) {
    S.lives--;
    if (S.lives <= 0) {
      gameOver();
    } else {
      /* fresh ball on the paddle, then a new serve countdown */
      addServeBall();
      S.countdown = 0.9;
      S.serveT = 0;
    }
  }
}

function gameOver() {
  S.state = 'over';
  S.overT = 0;
  AUD.sfx.gameover();
  AUD.music.setIntensity(0);
  saveHi();
}

/* ---------- cannon / riding (the FIRE signature move) ---------- */

function firePower() {
  if (S.state !== 'playing') return;
  if (!S.fireReady) return;
  S.fireReady = false;
  S.charge = Math.max(0, S.charge - CFG.fireCost);
  S.state = 'playing';
  S.cannon = { wait: CFG.cannon.wait, dir: 'up' };
  AUD.sfx.fire();
  FX.text(S.paddle.x, S.paddle.y - 80, 'TOPSIDE!', {
    life: 1.2, size: 34, color: '#ffe14d', vy: -50, glow: true
  });
}

function launchCannonball() {
  const p = S.paddle;
  const b = new Ball(p.x, p.y - PADDLE_H / 2 - 10, 0, -CANNON_V, { r: CANNON_R, type: 'cannon', hue: 46 });
  S.balls.push(b);
  S.cannon = null;
  AUD.sfx.fire();
}

/* enter riding mode: the cannon reached the top band and becomes golden */
function enterRiding(ball) {
  S.ridingT = RIDING_DUR;
  S.ridingBall = ball;
  S.crown = 0;
  ball.type = 'pearl';
  ball.hue = 52;
  S.mode.pearl = 0;
  AUD.music.setIntensity(1);
  FX.shock(ball.x, CFG.brickTop - 40, { r1: 300, life: 0.7, color: '#ffe14d', lw: 8 });
  FX.text(CFG.W / 2, CFG.brickTop - 60, 'RIDING!', {
    life: 1.6, size: 44, color: '#ffe14d', vy: -40, glow: true
  });
}

/* the riding ball bounces in the band above the bricks, guided by tilt */
function updateRiding(ball, dt) {
  const top = CFG.brickTop - 40;
  const bottom = CFG.brickTop + CFG.rows * (CFG.brickH + CFG.brickGap) + 30;

  for (let i = 0; i < CFG.flameCount; i++) {
    ball.trail.push({ x: ball.x - ball.vx * dt * i, y: ball.y - ball.vy * dt * i, life: 0.5 });
  }
  if (ball.trail.length > 46) ball.trail.splice(0, ball.trail.length - 46);

  /* tilt guidance from input target x */
  if (S.paddle.target) {
    const rel = (S.paddle.target.x - CFG.W / 2) / (CFG.W / 2);
    if (Math.abs(rel) > 0.15) {
      ball.vx += clamp(rel, -1, 1) * 300 * dt;
    }
  }
  /* cap horizontal */
  ball.vx = clamp(ball.vx, -400, 400);
  ball.x += ball.vx * dt;
  ball.y += ball.vy * dt;

  /* golden shimmer raining near the crown ball */
  if (RNG.chance(0.55)) FX.ridingShimmer(ball.x, ball.y);

  if (ball.x - ball.r < CFG.leftWall) { ball.x = CFG.leftWall + ball.r; ball.vx = Math.abs(ball.vx); AUD.sfx.wall(); }
  if (ball.x + ball.r > CFG.rightWall) { ball.x = CFG.rightWall - ball.r; ball.vx = -Math.abs(ball.vx); AUD.sfx.wall(); }

  /* prevent escaping through the bottom band */
  if (ball.y + ball.r > bottom && ball.vy > 0) {
    ball.y = bottom - ball.r;
    ball.vy = -Math.abs(ball.vy);
    FX.burst(ball.x, ball.y, { colors: ['#ffe14d'], count: 6, speed: [40, 140], size: [2, 4], life: [0.25, 0.5], glow: true });
    AUD.sfx.ceiling();
  }
  if (ball.y - ball.r < top && ball.vy < 0) {
    ball.y = top + ball.r;
    ball.vy = Math.abs(ball.vy);
    FX.burst(ball.x, ball.y, { colors: ['#ffe14d'], count: 6, speed: [40, 140], size: [2, 4], life: [0.25, 0.5], glow: true });
    AUD.sfx.ceiling();
  }

  /* anti-stuck: a riding ball trapped bouncing between the walls at roughly
     the same height for 4s+ gets its vertical velocity nudged so it climbs
     into new rows instead of vibrating in place forever. */
  const band = Math.round(ball.y / 36);
  if (band !== ball._stkBand) {
    ball._stkBand = band;
    ball._stkT = 0;
  } else {
    ball._stkT += dt;
    if (ball._stkT > 4) {
      ball._stkT = 0;
      ball.vy += (ball.vy >= 0 ? 1 : -1) * 40;
      FX.burst(ball.x, ball.y, { colors: ['#ffe14d'], count: 3, speed: [20, 80], size: [1.5, 3], life: [0.2, 0.4], glow: true });
    }
  }

  /* smash any brick currently touched */
  for (const b of S.bricks) {
    if (b.dead) continue;
    if (circleRectHit(ball, b)) {
      b.hp = 1;
      destroyBrick(b, false);
      S.crown += 5;
      FX.text(ball.x, ball.y, '+5', { life: 0.5, size: 14, color: '#ffe14d', vy: -50, glow: false });
    }
  }
}

function endRiding() {
  if (S.ridingBall) {
    const i = S.balls.indexOf(S.ridingBall);
    if (i >= 0) S.balls.splice(i, 1);
    S.ridingBall = null;
  }
  const bonus = S.crown * 50;
  if (bonus > 0) {
    addScore(bonus, CFG.W / 2, CFG.brickTop - 60);
    FX.text(CFG.W / 2, CFG.brickTop - 40, 'CROWN +' + bonus, {
      life: 1.6, size: 30, color: '#ffe14d', vy: -40, glow: true
    });
  }
  S.ridingT = 0;
  S.crown = 0;
  if (S.balls.length === 0) {
    /* FIRE was used with no other ball in flight: give the player a fresh
       ball on the paddle so the game cannot soft-lock with 0 balls. */
    addServeBall();
    S.countdown = 0.9;
    S.serveT = 0;
  }
  AUD.music.setIntensity(clamp(0.15 + S.level * 0.1, 0, 1));
}

/* ---------- powerups ---------- */
function applyPowerup(pu) {
  pu.caught = true;
  const p = S.paddle;
  const ball = S.balls[0];
  AUD.sfx.powerup();
  FX.burst(pu.x, pu.y, { colors: [pu.color], count: 16, speed: [60, 220], size: [2, 5], life: [0.4, 0.9], glow: true });
  FX.text(pu.x, pu.y - 26, pu.label, { life: 1.0, size: 20, color: pu.color, vy: -50, glow: true });
  switch (pu.type) {
    case 'multi': {
      if (ball) {
        /* clone the lead ball with a small angular spread and ±8% speed
           variance so the clones diverge instead of overlapping forever */
        const baseSp = ball.speed;
        const launchSp = Math.max(baseSp, CFG.ballLaunch);
        for (let k = 0; k < 2; k++) {
          const side = (k === 0 ? -1 : 1);
          const ang = side * RNG.range(0.25, 0.35);
          const spB = launchSp * (1 + side * RNG.range(-0.08, 0.08));
          const nb = new Ball(ball.x + side * 30, ball.y, Math.sin(ang) * spB, -Math.cos(ang) * spB, { type: 'normal', hue: ball.hue });
          if (ball.vy >= 0) nb.vy = Math.abs(nb.vy);   /* falling parent: clones keep falling */
          nb.deflected = false;
          nb.lastBrick = -1;
          S.balls.push(nb);
        }
      }
      break;
    }
    case 'big': {
      p.w = Math.min(CFG.W * 0.75, p.w * 1.45);
      p.flash = 0.6;
      break;
    }
    case 'slow': {
      S.mode.slow = 8;
      break;
    }
    case 'grab': {
      p.grab = true;
      p.magnetT = 10;
      break;
    }
    case 'flux': {
      S.mode.flux = FLUX_DUR;
      if (ball) ball.type = 'flux';
      break;
    }
    case 'pearl': {
      S.mode.pearl = PEARL_DUR;
      if (ball) ball.type = 'pearl';
      break;
    }
    case 'bomb': {
      const c = ball || { x: pu.x, y: pu.y };
      blastBricks(c.x, c.y, 140, false);
      FX.shock(c.x, c.y, { r1: 150, life: 0.5, color: '#ff3b6b', lw: 6 });
      AUD.sfx.bomb();
      S.shake = Math.min(CFG.shake.max * 1.2, S.shake + 8);
      break;
    }
    case 'life': {
      if (S.lives < 5) { S.lives++; }
      AUD.sfx.extraLife();
      FX.text(p.x, p.y - 90, '1UP', { life: 1.2, size: 30, color: '#ffffff', vy: -40, glow: true });
      break;
    }
    case 'mega': {
      p.w = Math.min(CFG.W * 0.85, p.w * 1.8);
      p.grab = true;
      p.magnetT = 6;
      break;
    }
  }
}

/* ---------- combo ---------- */
function updateCombo(dt) {
  if (S.comboT > 0) {
    S.comboT -= dt;
    if (S.comboT <= 0) {
      S.comboT = CFG.comboLinger;
      S.combo = Math.max(0, S.combo - 1);
    }
  }
  S.mr = Math.min(10, 1 + Math.floor(S.combo / 5));
}

/* ---------- pause ---------- */
function pauseGame() {
  if (S.state === 'playing') { S.state = 'pause'; AUD.music.setIntensity(0.1); }
}
function resumeGame() {
  if (S.state === 'pause') { S.state = 'playing'; AUD.music.setIntensity(clamp(0.15 + S.level * 0.1, 0, 1)); }
}
function togglePause() {
  if (S.state === 'pause') resumeGame();
  else if (S.state === 'playing') pauseGame();
}

/* ---------- main tick ---------- */
function tick(dt) {
  dt = clamp(dt || 0, 0, 0.05);
  S.time += dt;

  /* paused: only ambient updates; game logic frozen */
  if (S.state === 'pause') {
    updateShake(dt);
    return;
  }

  if (S.state !== 'playing') {
    /* keep ambient / menus calm */
    if (S.state === 'menu' || S.state === 'over' || S.state === 'win') {
      updateShake(dt);
    }
    return;
  }

  const realDt = dt;
  /* slow-mo scales ball physics only */
  let bdt = dt;
  if (S.mode.slow > 0) {
    S.mode.slow -= dt;
    bdt = dt * 0.6;
    if (S.mode.slow <= 0) S.mode.slow = 0;
  }

  updatePaddle(realDt);

  /* countdown: ball rides the paddle, then serve */
  if (S.countdown > 0) {
    S.countdown -= realDt;
    const ball = S.balls[0];
    if (ball) {
      placeBallOnPaddle(ball);
      ball.x = S.paddle.x;
    }
    if (S.countdown <= 0) serveBall();
  }

  /* cannon charge wait */
  if (S.cannon) {
    S.cannon.wait -= realDt;
    if (S.cannon.wait <= 0) launchCannonball();
  }

  /* balls */
  if (S.ridingBall) {
    /* riding mode: the golden ball bounces on top, smashing bricks.
       Other balls (multiball leftovers) are ignored while riding. */
    S.ridingT -= realDt;
    updateRiding(S.ridingBall, bdt);
    if (S.ridingT <= 0) endRiding();
  } else if (S.countdown <= 0) {
    /* normal ball physics (not during countdown — ball is on the paddle).
       Iterate a snapshot: updateBall → loseBall can replace S.balls with a
       fresh serve ball mid-loop (mass multiball loss), so skip any snapshot
       ball that is no longer in S.balls instead of indexing a stale array. */
    const balls = S.balls.slice();
    for (let i = balls.length - 1; i >= 0; i--) {
      const ball = balls[i];
      if (S.balls.indexOf(ball) === -1) continue;  /* removed mid-loop */
      if (ball.vx === 0 && ball.vy === 0 && ball.type !== 'cannon') {
        /* stuck ball on the paddle: keep it glued (only if it's actually up there) */
        if (S.paddle.grabStuck || Math.abs(ball.y - (S.paddle.y - PADDLE_H / 2 - ball.r)) < 60) {
          placeBallOnPaddle(ball);
        }
        continue;
      }
      updateBall(ball, bdt);
    }
  }

  /* flux / pearl expiry on the primary ball */
  if (S.mode.flux > 0) {
    S.mode.flux -= realDt;
    if (S.mode.flux <= 0) {
      S.mode.flux = 0;
      for (const b of S.balls) if (b.type === 'flux' && b !== S.ridingBall) b.type = 'normal';
    }
  }
  if (S.mode.pearl > 0) {
    S.mode.pearl -= realDt;
    if (S.mode.pearl <= 0) {
      S.mode.pearl = 0;
      for (const b of S.balls) if (b.type === 'pearl' && b !== S.ridingBall) b.type = 'normal';
    }
  }

  /* grabber expiry */
  if (S.paddle.magnetT > 0) {
    S.paddle.magnetT -= realDt;
    if (S.paddle.magnetT <= 0) {
      S.paddle.magnetT = 0;
      S.paddle.grab = false;
      /* don't leave a ball stuck forever: auto-launch it */
      if (S.paddle.grabStuck) releaseStuckBall();
    }
  }

  /* powerups */
  for (let i = S.powerups.length - 1; i >= 0; i--) {
    const pu = S.powerups[i];
    pu.step(realDt);
    if (pu.life <= 0 || pu.y > CFG.H + 60) {
      S.powerups.splice(i, 1);
      continue;
    }
    /* catch */
    const p = S.paddle;
    if (pu.y > p.y - PADDLE_H / 2 - 6 && pu.y < p.y + PADDLE_H / 2 + 24 &&
        pu.x > p.x - p.w / 2 - 20 && pu.x < p.x + p.w / 2 + 20) {
      applyPowerup(pu);
      S.powerups.splice(i, 1);
    }
  }

  /* combo decay */
  updateCombo(realDt);

  /* shake */
  updateShake(realDt);

  /* flash decay */
  if (S.flash > 0) S.flash = Math.max(0, S.flash - realDt * 1.2);

  /* music intensity from level + combo + riding */
  let mi = 0.15 + S.level * 0.08 + Math.min(0.4, S.mr * 0.05);
  if (S.ridingBall) mi += 0.25;
  if (S.mode.slow > 0) mi *= 0.7;
  mi = clamp(mi, 0, 1);
  AUD.music.setIntensity(mi);
}

function updateShake(dt) {
  const decay = Math.exp(-dt * 8);
  S.shake *= decay;
  if (S.shake < 0.01) S.shake = 0;
  if (S.shake > 0) {
    S.shakeDx = RNG.range(-1, 1) * S.shake;
    S.shakeDy = RNG.range(-1, 1) * S.shake;
  } else {
    S.shakeDx = 0; S.shakeDy = 0;
  }
}

/* ---------- exports ---------- */
const GL = { MODES: {
  normal: 1, tough: 1, shield: 1, mirror: 1, angle: 1,
  speed: 1, time: 1, bomb: 1, glass: 1
} };

const G = {
  tick, resetGame, startGame, startLevel,
  serveBall, firePower, pauseGame, resumeGame, togglePause,
  addScore, releaseStuckBall
};

/* defensive: if someone bootstraps, honor module export pattern */
if (typeof module !== 'undefined' && module.exports) {
  module.exports = { G, GL, RIDING_DUR, FLUX_MAX, FLUX_DUR, FLUX_POWER, PEARL_DUR, PEARL_POWER, CANNON_DUR };
}


