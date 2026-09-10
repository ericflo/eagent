/* BREAKTHROUGH — js/game.js
   Core simulation: state machine, paddle, ball physics, brick collisions,
   combo/multiplier, FRENZY ("on top") mechanic, power-ups, lives, levels.
   Exposes window.G. Rendering of world happens here into the logical canvas;
   main.js owns input, DOM HUD and screens.
*/
window.G = (function () {
  const U = window.U;
  const W = 720, H = 960;                 // logical resolution
  const WALL = 12;                        // side wall thickness
  const FIELD_AREA = { x: WALL, y: 120, w: W - WALL * 2, h: 420 };
  const PADDLE_BAND = { min: 620, max: 900 };  // vertical range for paddle y (top of paddle)

  const state = {
    mode: 'title',        // title | serving | playing | balllost | gameover | levelcomplete | paused
    score: 0, hi: +(localStorage.getItem('bt_hi') || 0),
    lives: 3, level: 0,
    combo: 0, mult: 1, bestCombo: 0,
    frenzy: 0, frenzyTime: 0, frenzyLevel: 0,
    timeScale: 1, slowmo: 0,
    shake: 0, flash: 0,
    time: 0,
    muteBtn: null
  };

  const paddle = { x: W / 2 - 60, y: 880, w: 120, h: 16, vx: 0, vy: 0, targetX: W / 2, targetY: 880,
                   baseW: 120, hue: 190, sticky: false, laser: false, laserCd: 0, wideT: 0, stickyT: 0, laserT: 0 };
  let balls = [];
  let field = new Bricks.BrickField();
  let drops = [];
  const lasers = [];
  let serveTimer = 0;       // countdown before ball launches from paddle
  let ballLostTimer = 0;
  let frenzyBeat = 0;
  let pendingStateCb = null; // main.js callback for screen changes

  // HUD chip state (main.js reads via getBuffs)
  function getBuffs() {
    const out = [];
    if (paddle.wideT > 0) out.push({ key: 'wide', t: paddle.wideT, d: 14 });
    if (paddle.stickyT > 0) out.push({ key: 'sticky', t: paddle.stickyT, d: 12 });
    if (paddle.laserT > 0) out.push({ key: 'laser', t: paddle.laserT, d: 10 });
    if (state.slowmo > 0) out.push({ key: 'slow', t: state.slowmo, d: 6 });
    return out;
  }

  // ---------------------------------------------------------------- setup
  function startGame() {
    state.score = 0; state.lives = 3; state.level = 0; state.combo = 0; state.mult = 1; state.bestCombo = 0;
    state.frenzy = 0; state.frenzyTime = 0;
    Particles.clear(); drops.length = 0; lasers.length = 0;
    loadLevel(0);
    state.mode = 'serving'; serveTimer = 0.9;
    emit('start');
  }
  function loadLevel(i) {
    state.level = i;
    const spec = Levels.LEVELS[i];
    field.build({ rows: spec.rows.length, cols: spec.rows[0].length, grid: spec.rows, tier: spec.tier }, FIELD_AREA);
    balls.length = 0; drops.length = 0; lasers.length = 0;
    paddle.w = paddle.baseW; paddle.wideT = paddle.stickyT = paddle.laserT = 0;
    paddle.sticky = paddle.laser = false;
    paddle.x = W / 2 - paddle.w / 2; paddle.y = PADDLE_BAND.min + 200; paddle.targetX = W / 2;
    state.combo = 0; state.mult = 1; setFrenzy(0);
    spawnServeBall();
    emit('level', i);
  }
  function nextLevel() {
    if (state.level + 1 >= Levels.LEVELS.length) { // beat the game — loop with bonus
      state.mode = 'levelcomplete';
      emit('gameWin');
      return;
    }
    Audio.levelWin();
    state.mode = 'levelcomplete';
    Particles.confetti(W / 2, H * 0.28, 260);
    Particles.confetti(W * 0.2, H * 0.15, 140);
    Particles.confetti(W * 0.8, H * 0.15, 140);
    state.flash = 0.5;
    emit('levelcomplete');
  }
  function spawnServeBall() {
    balls.length = 0;
    const b = new Balls.Ball(paddle.x + paddle.w / 2, paddle.y - 14, 0, 0);
    b.stuck = true; b.stuckDx = 0;
    balls.push(b);
    state.mode = 'serving'; serveTimer = 0.6;
  }
  function launchBall() {
    for (const b of balls) {
      if (b.stuck) {
        b.stuck = false;
        const a = -Math.PI / 2 + U.rand(-0.35, 0.35);
        const s = 380;
        b.vx = Math.cos(a) * s; b.vy = Math.sin(a) * s;
        Particles.impact(b.x, b.y, 190, -Math.PI / 2);
        Audio.paddleHit(0.5);
      }
    }
    if (state.mode === 'serving') state.mode = 'playing';
  }

  // ---------------------------------------------------------------- frenzy
  function setFrenzy(v) {
    const was = state.frenzy > 0;
    state.frenzy = v;
    if (v > 0 && !was) {
      Audio.frenzyEnter();
      state.flash = 1; state.shake = Math.max(state.shake, 14);
      Particles.text(W / 2, 300, 'FRENZY!', { hue: 45, size: 64, life: 1.4 });
      Particles.confetti(W / 2, 260, 120);
      emit('frenzy', true);
    } else if (v <= 0 && was) {
      Audio.frenzyExit();
      // delay the "frenzy lost" message so it never overlaps the big FRENZY! banner
      const lvlSnapshot = state.frenzyLevel;
      setTimeout(() => {
        if (state.frenzy <= 0) {
          Particles.text(W / 2, 300, 'frenzy lost…', { hue: 200, size: 26, life: 0.9 });
          Particles.confetti(W / 2, 300, 40);
          state.frenzyLevel = lvlSnapshot; // spawn nothing; keep quiet
          state.frenzyLevel = 0;
        }
      }, 900);
      state.frenzyTime = 0; state.frenzyLevel = 0;
      emit('frenzy', false);
    }
    Audio.setFrenzy(state.frenzyLevel, state.frenzy > 0);
  }

  function checkFrenzy() {
    if (!balls.length) return;
    let onTop = false;
    for (const b of balls) {
      if (!b.dead && field.aliveCount > 0 && b.y < field.topRowY - b.r) onTop = true;
      if (!b.dead && field.aliveCount === 0) onTop = false;
    }
    if (onTop) {
      state.frenzyTime += 1 / 60;
      // multiplier grows the longer the ball stays up: 2x, 3x, 4x...
      const lvl = Math.min(1, state.frenzyTime / 14);
      if (Math.floor(lvl * 10) !== Math.floor(state.frenzyLevel * 10)) {
        Particles.text(W / 2 + U.rand(-160, 160), U.rand(180, 420),
          '×' + (2 + Math.floor(state.frenzyTime)), { hue: 45, size: 30, life: 0.8 });
      }
      state.frenzyLevel = lvl;
      setFrenzy(1);
      // rain sparks from the top
      if (Math.random() < 0.4) Particles.spawn({ x: U.rand(0, W), y: U.rand(0, 100),
        vx: U.rand(-40, 40), vy: U.rand(40, 160), size: 4, life: 1, hue: U.rand(30, 60), shape: 'glow', grav: 60 });
      frenzyBeat -= 1 / 60;
      if (frenzyBeat <= 0) {
        frenzyBeat = Math.max(0.18, 0.5 - lvl * 0.3);
        Background.beat();
      }
    } else if (state.frenzy > 0) {
      setFrenzy(0);
    }
  }

  // ---------------------------------------------------------------- scoring
  function addScore(base, x, y) {
    let mult = 1 + state.combo * 0.25;
    if (state.frenzy > 0) mult *= 2 + Math.floor(state.frenzyTime);
    const pts = Math.round(base * mult);
    state.score += pts;
    if (pts >= 100 * mult) Particles.text(x, y, '+' + pts, { hue: 45, size: 22 });
    emit('score');
    return pts;
  }

  // ---------------------------------------------------------------- collisions
  function breakBrick(b, ball, opts = {}) {
    const destroyed = field.destroy(b, { noRegrow: opts.noRegrow });
    const isGold = ball && ball.type === 'gold';
    for (const d of destroyed) {
      Particles.shatter(d, d.hue);
      if (isGold) {
        Particles.burst(d.x + d.w / 2, d.y + d.h / 2, 14, { hue: 55, shape: 'rect', spin: 8, spMax: 300, glow: .5 });
      }
      addScore(d.score * (isGold ? 3 : 1), d.x + d.w / 2, d.y);
    }
    state.combo++;
    if (state.combo > state.bestCombo) state.bestCombo = state.combo;
    state.mult = 1 + state.combo * 0.25;
    Audio.brickBreak(state.combo, state.frenzy > 0);
    if (state.frenzy > 0) Audio.frenzyBlip(state.combo, state.frenzyLevel);
    state.shake = Math.min(18, state.shake + 1.5 + destroyed.length * 0.8);
    if (state.combo > 0 && state.combo % 5 === 0) {
      Particles.text(b.x + b.w / 2, b.y - 16, 'COMBO ×' + state.combo, { hue: 190 + state.combo * 6, size: 24 + Math.min(18, state.combo), life: 0.9 });
      state.shake = Math.max(state.shake, 6);
    }
    // drops
    if (!opts.noDrop && Math.random() < 0.16) {
      drops.push(new Powerups.Drop(b.x + b.w / 2, b.y + b.h / 2, Powerups.pickKey()));
    }
    // splitter: spawn a child
    if (ball && ball.type === 'splitter' && ball.splitDepth < 2 && balls.length < 12) {
      const sp = ball.speed || 400;
      for (const da of [-0.6, 0.6]) {
        const nb = new Balls.Ball(ball.x, ball.y, ball.vx, ball.vy, 'splitter');
        nb.splitDepth = ball.splitDepth + 1;
        const a = Math.atan2(ball.vy, ball.vx) + da;
        nb.vx = Math.cos(a) * sp; nb.vy = Math.sin(a) * sp;
        balls.push(nb);
      }
      Particles.burst(ball.x, ball.y, 20, { hue: 185, shape: 'spark', spMax: 380, glow: 1, grav: 0 });
    }
    emit('brick');
    return destroyed;
  }

  function collideBallBricks(ball) {
    for (const b of field.bricks) {
      if (!b.alive) continue;
      if (b.type === 'phase' && !b.solid) continue;     // ghost: pass through
      if (ball.x + ball.r < b.x || ball.x - ball.r > b.x + b.w ||
          ball.y + ball.r < b.y || ball.y - ball.r > b.y + b.h) continue;

      const fire = ball.type === 'fire';
      const heavy = ball.type === 'heavy';

      // --- mechanical gates (no multi-hit!) ---
      if (b.type === 'angle' && !fire) {
        const steep = Math.abs(ball.vy) > Math.abs(ball.vx) * 1.4;
        if (!steep) { bounceBall(ball, b); Audio.brickReject(); b.hitFlash = 0.5; continue; }
      }
      if (b.type === 'speed' && !fire && !heavy) {
        if (ball.speed < b.gate) { bounceBall(ball, b); Audio.brickReject(); b.hitFlash = 0.5;
          Particles.impact(ball.x, ball.y, 195); continue; }
      }
      if (b.type === 'armored' && !fire && !heavy && ball.type !== 'splitter') {
        bounceBall(ball, b); Audio.brickReject(); b.hitFlash = 0.5; continue;
      }

      // resolve + break
      const burn = fire; // fireball passes through instead of bouncing
      if (!burn) bounceBall(ball, b);
      breakBrick(b, ball);
      if (burn) {
        Particles.burst(ball.x, ball.y, 12, { hue: 25, shape: 'glow', spMax: 200, grav: -50 });
      }
      state.flash = Math.max(state.flash, 0.25);
      return true; // one brick per step
    }
    return false;
  }

  // reflect ball off a brick using dominant-axis normal
  function bounceBall(ball, b) {
    const bx = b.x + b.w / 2, by = b.y + b.h / 2;
    const dx = ball.x - bx, dy = ball.y - by;
    const ox = (b.w / 2 + ball.r) - Math.abs(dx);
    const oy = (b.h / 2 + ball.r) - Math.abs(dy);
    if (ox < oy) {
      ball.vx = Math.abs(ball.vx) * Math.sign(dx || 1);
      ball.x = bx + Math.sign(dx || 1) * (b.w / 2 + ball.r + 0.1);
    } else {
      ball.vy = Math.abs(ball.vy) * Math.sign(dy || 1);
      ball.y = by + Math.sign(dy || 1) * (b.h / 2 + ball.r + 0.1);
    }
  }

  function collideBallPaddle(ball) {
    const p = paddle;
    if (ball.vy <= 0) return false;
    if (ball.y + ball.r < p.y || ball.y - ball.r > p.y + p.h) return false;
    if (ball.x < p.x - ball.r || ball.x > p.x + p.w + ball.r) return false;

    // paddle momentum: moving UP adds energy
    const upMomentum = Math.max(0, -paddle.vy);
    const energy = U.clamp(upMomentum / 900, 0, 1);

    if (p.sticky) {
      b_stick(ball);
    } else {
      // angle from hit position on paddle
      const rel = U.clamp((ball.x - (p.x + p.w / 2)) / (p.w / 2), -1, 1);
      const ang = -Math.PI / 2 + rel * 1.05;
      const s = U.clamp(ball.speed * (1 + energy * 0.5), 380, 980);
      ball.vx = Math.cos(ang) * s + paddle.vx * 0.15;
      ball.vy = Math.sin(ang) * s;
      // ensure upward
      if (ball.vy > -120) ball.vy = -120;
      ball.vy -= energy * 220;
      ball.setSpeed(Math.max(420, Math.hypot(ball.vx, ball.vy)));
    }
    state.combo = 0;
    state.mult = 1;
    ball.y = p.y - ball.r - 1;
    paddle.hue = 190 + energy * 120;
    Audio.paddleHit(energy);
    Particles.ripple(ball.x, p.y, { hue: 190 + energy * 100, maxR: 40 + energy * 60 });
    Particles.impact(ball.x, p.y, 190, -Math.PI / 2);
    if (energy > 0.4) {
      state.shake = Math.max(state.shake, 5);
      Particles.text(p.x + p.w / 2, p.y - 30, 'BOOST!', { hue: 60, size: 18, life: 0.5 });
    }
    return true;
  }
  function b_stick(ball) {
    ball.stuck = true;
    ball.stuckDx = ball.x - (paddle.x + paddle.w / 2);
    ball.vx = ball.vy = 0;
    Audio.ui();
  }

  function applyPowerup(key) {
    const def = Powerups.DEFS[key];
    Audio.powerup();
    switch (key) {
      case 'multi': {
        const src = balls.find(b => !b.dead) || balls[0];
        if (src) {
          for (const da of [-0.5, 0.5]) {
            const s = src.speed || 420;
            const nb = new Balls.Ball(src.x, src.y, src.vx, src.vy, src.type);
            const a = Math.atan2(src.vy, src.vx) + da;
            nb.vx = Math.cos(a) * s; nb.vy = Math.sin(a) * s;
            balls.push(nb);
          }
          Particles.text(paddle.x + paddle.w / 2, paddle.y - 30, 'MULTIBALL!', { hue: 320, size: 24 });
        }
        break;
      }
      case 'wide': paddle.wideT = 14; break;
      case 'sticky': paddle.stickyT = 12; break;
      case 'laser': paddle.laserT = 10; break;
      case 'slow': state.slowmo = 6; break;
      case 'life': state.lives++; Particles.text(W / 2, 500, '+1 LIFE', { hue: 335, size: 30 }); break;
      default:
        if (def.ball) {
          for (const b of balls) { b.type = def.ball; b.flash = 1; }
          Particles.text(W / 2, 480, def.label + ' BALL!', { hue: def.hue, size: 30 });
          state.flash = 0.6;
        }
    }
    emit('powerups');
  }

  function ballLost() {
    Audio.ballLost();
    state.shake = 12;
    if (state.lives > 1) {
      state.lives--;
      state.mode = 'balllost'; ballLostTimer = 1.2;
      Particles.text(W / 2, 700, 'BALL LOST', { hue: 350, size: 40, life: 1.2 });
    } else {
      state.lives = 0;
      state.mode = 'gameover';
      Audio.gameOver();
      if (state.score > state.hi) { state.hi = state.score; localStorage.setItem('bt_hi', state.hi); }
      emit('gameover');
    }
    emit('score');
  }

  // ---------------------------------------------------------------- update
  function update(rawDt) {
    state.time += rawDt;
    if (state.mode === 'paused' || state.mode === 'title' ||
        state.mode === 'gameover' || state.mode === 'levelcomplete') {
      Particles.update(rawDt);
      return;
    }
    // slow-mo affects balls/drops, not paddle
    state.timeScale = state.slowmo > 0 ? 0.45 : 1;
    if (state.slowmo > 0) { state.slowmo -= rawDt; if (state.slowmo <= 0) emit('powerups'); }
    const dt = rawDt * state.timeScale;

    updatePaddle(rawDt);
    updateBalls(dt, rawDt);
    field.update(dt, state.time);
    updateDrops(dt);
    updateLasers(dt);
    checkFrenzy();

    // level clear: every brick destroyed (pending regen ghosts don't count)
    if ((state.mode === 'playing' || state.mode === 'serving') &&
        field.aliveCount === 0 && !field.bricks.some(b => b.respawnAt)) nextLevel();

    // timers
    if (state.mode === 'balllost') {
      ballLostTimer -= rawDt;
      if (ballLostTimer <= 0) { spawnServeBall(); }
    }
    if (state.mode === 'serving') {
      serveTimer -= rawDt;
      if (serveTimer <= 0) launchBall();
    }
    // buff timers
    if (paddle.wideT > 0) { paddle.wideT -= rawDt; if (paddle.wideT <= 0) paddle.w = paddle.baseW; }
    if (paddle.stickyT > 0) { paddle.stickyT -= rawDt; paddle.sticky = paddle.stickyT > 0; }
    if (paddle.laserT > 0) { paddle.laserT -= rawDt; paddle.laser = paddle.laserT > 0; }

    // audio intensity
    Audio.setPadIntensity(U.clamp((state.mult - 1) / 6 + state.frenzyLevel * 0.7, 0, 1));
    Particles.update(rawDt);
    // decay fx
    state.shake = U.damp(state.shake, 0, 8, rawDt);
    state.flash = Math.max(0, state.flash - rawDt * 2.5);
    Background.update(rawDt, U.clamp((state.mult - 1) / 8, 0, 1), state.frenzyLevel * (state.frenzy > 0 ? 1 : 0), state.frenzyLevel, state.frenzy > 0);
  }

  function updatePaddle(dt) {
    // smooth follow of target with velocity tracking
    const px = paddle.x, py = paddle.y;
    const kx = 14, ky = 14;
    const cx = paddle.targetX - paddle.w / 2;
    const cy = U.clamp(paddle.targetY, PADDLE_BAND.min, PADDLE_BAND.max);
    paddle.x = U.damp(paddle.x, cx, kx, dt);
    paddle.y = U.damp(paddle.y, cy, ky, dt);
    paddle.x = U.clamp(paddle.x, WALL, W - WALL - paddle.w);
    paddle.vx = (paddle.x - px) / Math.max(dt, 0.001);
    paddle.vy = (paddle.y - py) / Math.max(dt, 0.001);
    // stuck balls ride the paddle
    for (const b of balls) {
      if (b.stuck) { b.x = paddle.x + paddle.w / 2 + b.stuckDx; b.y = paddle.y - b.r - 2; }
    }
  }

  function updateBalls(dt, rawDt) {
    if (!balls.length && state.mode === 'playing') { ballLost(); return; }
    for (let i = balls.length - 1; i >= 0; i--) {
      const b = balls[i];
      if (b.stuck) { b.emitTrail(rawDt); continue; }
      // substeps for fast balls
      const speed = b.speed;
      const steps = Math.max(1, Math.ceil(speed * dt / 8));
      const sdt = dt / steps;
      let lost = false;
      for (let s = 0; s < steps; s++) {
        b.x += b.vx * sdt; b.y += b.vy * sdt;
        // walls
        if (b.x < WALL + b.r) { b.x = WALL + b.r; b.vx = Math.abs(b.vx); Audio.wallBounce(); Particles.impact(b.x, b.y, 200); }
        if (b.x > W - WALL - b.r) { b.x = W - WALL - b.r; b.vx = -Math.abs(b.vx); Audio.wallBounce(); Particles.impact(b.x, b.y, 200); }
        if (b.y < WALL + b.r) { b.y = WALL + b.r; b.vy = Math.abs(b.vy); Audio.wallBounce(); Particles.impact(b.x, b.y, 200); }
        if (b.y > H + 30) { lost = true; break; }
        collideBallPaddle(b);
        collideBallBricks(b);
      }
      if (lost) {
        balls.splice(i, 1);
        Particles.burst(b.x, H, 30, { hue: 350, spMax: 400 });
        continue;
      }
      // keep a playable minimum speed
      if (b.speed < 260) b.scaleSpeed(1 + (260 - b.speed) / b.speed * 0.1);
      b.emitTrail(rawDt);
    }
    if (!balls.length && state.mode === 'playing') ballLost();
  }

  function updateDrops(dt) {
    for (let i = drops.length - 1; i >= 0; i--) {
      const d = drops[i];
      d.update(dt);
      if (d.y > H + 30) { drops.splice(i, 1); continue; }
      // catch
      const p = paddle;
      if (d.y + d.r > p.y && d.y - d.r < p.y + p.h && d.x > p.x - d.r && d.x < p.x + p.w + d.r) {
        drops.splice(i, 1);
        applyPowerup(d.key);
        Particles.burst(d.x, d.y, 24, { hue: d.def.hue, shape: 'glow', spMax: 300, grav: 0, life: 0.6 });
        Particles.text(d.x, d.y - 20, d.def.label, { hue: d.def.hue, size: 20 });
      }
    }
  }

  function updateLasers(dt) {
    if (paddle.laser) {
      paddle.laserCd = (paddle.laserCd || 0) - dt;
      if (paddle.laserCd <= 0) {
        paddle.laserCd = 0.35;
        lasers.push({ x: paddle.x + 7, y: paddle.y - 8, vy: -900 });
        lasers.push({ x: paddle.x + paddle.w - 7, y: paddle.y - 8, vy: -900 });
        Audio.laser();
      }
    }
    for (let i = lasers.length - 1; i >= 0; i--) {
      const l = lasers[i];
      l.y += l.vy * dt;
      if (l.y < WALL) { lasers.splice(i, 1); continue; }
      // hit bricks
      let hit = false;
      for (const b of field.bricks) {
        if (!b.alive || (b.type === 'phase' && !b.solid)) continue;
        if (l.x > b.x && l.x < b.x + b.w && l.y < b.y + b.h && l.y > b.y) {
          if (b.type === 'armored' || b.type === 'speed' || b.type === 'angle') {
            Audio.brickReject(); b.hitFlash = 0.5;          // lasers can't beat gates
          } else breakBrick(b, null);
          hit = true; break;
        }
      }
      if (hit) { Particles.impact(l.x, l.y, 350, -Math.PI / 2); lasers.splice(i, 1); }
    }
  }

  // ---------------------------------------------------------------- draw
  function draw(ctx) {
    ctx.save();
    // screenshake
    const sh = state.shake;
    if (sh > 0.2) ctx.translate(U.rand(-sh, sh) * 0.5, U.rand(-sh, sh) * 0.5);

    Background.draw(ctx);

    // field frame
    ctx.fillStyle = 'rgba(150,180,255,.10)';
    ctx.fillRect(0, 0, WALL, H); ctx.fillRect(W - WALL, 0, WALL, H);
    ctx.fillRect(0, 0, W, WALL);

    // bricks
    for (const b of field.bricks) Render.drawBrick(ctx, b, state.time);

    // drops
    for (const d of drops) d.draw(ctx, state.time);

    // lasers
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    for (const l of lasers) {
      const g = ctx.createLinearGradient(0, l.y - 26, 0, l.y + 4);
      g.addColorStop(0, 'rgba(255,80,120,0)');
      g.addColorStop(1, 'rgba(255,120,160,.95)');
      ctx.fillStyle = g;
      ctx.fillRect(l.x - 2, l.y - 26, 4, 30);
    }
    ctx.restore();

    // balls
    for (const b of balls) {
      b.drawTrail(ctx, state.frenzyLevel);
      b.draw(ctx, state.time, 520 + Levels.LEVELS[state.level].tier * 30);
    }

    // top-of-field frenzy marker line
    if (field.aliveCount > 0) {
      ctx.save();
      ctx.strokeStyle = U.hsl(45, 100, 65, 0.25 + 0.2 * Math.sin(state.time * 4));
      ctx.setLineDash([10, 12]);
      ctx.lineWidth = 2;
      ctx.beginPath(); ctx.moveTo(WALL, field.topRowY - 14); ctx.lineTo(W - WALL, field.topRowY - 14); ctx.stroke();
      ctx.restore();
    }

    Render.drawPaddle(ctx, paddle, state.time);
    Particles.draw(ctx);
    Particles.drawTexts(ctx);
    Render.drawVignette(ctx, U.clamp((state.mult - 1) / 8, 0, 1), state.frenzy, state.time, state.frenzyLevel);

    // hit flash
    if (state.flash > 0) {
      ctx.fillStyle = `rgba(255,255,255,${state.flash * 0.25})`;
      ctx.fillRect(0, 0, W, H);
    }
    ctx.restore();

    // serve hint
    if (state.mode === 'serving') {
      const blink = 0.55 + 0.45 * Math.sin(state.time * 5);
      ctx.textAlign = 'center';
      // primary hint: big pulsing "TAP TO LAUNCH"
      ctx.save();
      ctx.globalAlpha = blink;
      ctx.font = '900 30px ui-rounded, system-ui, sans-serif';
      ctx.fillStyle = '#ffd166';
      ctx.shadowColor = 'rgba(255,160,28,.8)'; ctx.shadowBlur = 18;
      ctx.fillText('TAP TO LAUNCH', W / 2, paddle.y - 96);
      ctx.restore();
      // secondary hint
      ctx.fillStyle = 'rgba(230,240,255,.75)';
      ctx.font = '700 20px ui-rounded, system-ui, sans-serif';
      ctx.fillText('MOVE UP AS YOU LAUNCH FOR EXTRA POWER', W / 2, paddle.y - 70);
    }
  }

  // ---------------------------------------------------------------- events
  const listeners = {};
  function on(ev, fn) { (listeners[ev] = listeners[ev] || []).push(fn); }
  function emit(ev, data) { (listeners[ev] || []).forEach(f => f(data)); }

  function launchOrRelease() {  // tap to release sticky / serve
    let released = false;
    for (const b of balls) if (b.stuck) { released = true; }
    if (released) launchBall();
  }

  // pause / resume
  function pause() { if (state.mode === 'playing' || state.mode === 'serving') { state._prev = state.mode; state.mode = 'paused'; emit('paused'); } }
  function resume() { if (state.mode === 'paused') { state.mode = state._prev || 'playing'; emit('resumed'); } }

  // ball-type speed gate for current level (for drawing fast ring)
  function gate() { return 520 + Levels.LEVELS[state.level].tier * 30; }

  return {
    state, paddle, balls, field, drops, W, H,
    startGame, loadLevel, nextLevel, update, draw, pause, resume,
    on, launchOrRelease, launchBall, applyPowerup, getBuffs, gate
  };
})();