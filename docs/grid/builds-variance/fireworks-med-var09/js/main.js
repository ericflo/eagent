/* main.js — game orchestration: state machine, fixed-timestep physics with
 * swept collision, overdrive system, rendering, HUD, screens. */
(function () {
  'use strict';
  var BO = window.BO;

  // ---- config ----
  var CFG = {
    baseSpeed: 430, maxSpeed: 900, minSpeed: 300,
    launchSpeedK: 1.0,
    maxBounceAngle: 62,      // degrees off vertical from paddle
    upBoost: 1.14, downDamp: 0.92, spinFactor: 0.00028,
    fieldW: 900, fieldH: 640,
    paddleH: 14,
    brickRows: 6, brickCols: 13,
    brickW: 60, brickH: 24, brickTop: 84, brickGap: 4,
    physicsHz: 240,
    dropChance: 0.16,
    overdrive: { growth: 1.55, cap: 16 },
    lives: 3
  };

  // ---- canvas setup ----
  var canvas = document.getElementById('game');
  var ctx = canvas.getContext('2d');
  var dpr = Math.min(window.devicePixelRatio || 1, 2.5);
  var view = { w: CFG.fieldW, h: CFG.fieldH, scale: 1, ox: 0, oy: 0 };

  function resize() {
    var vw = window.innerWidth, vh = window.innerHeight;
    canvas.width = Math.round(vw * dpr);
    canvas.height = Math.round(vh * dpr);
    canvas.style.width = vw + 'px';
    canvas.style.height = vh + 'px';
    var scale = Math.min(vw / CFG.fieldW, vh / CFG.fieldH);
    view.scale = scale;
    view.ox = (vw - CFG.fieldW * scale) / 2;
    view.oy = (vh - CFG.fieldH * scale) / 2;
    view.w = vw; view.h = vh;
  }
  window.addEventListener('resize', resize);
  resize();

  function toField(px, py) {
    return { x: (px - view.ox) / view.scale, y: (py - view.oy) / view.scale };
  }

  // ---- state ----
  var S = {
    mode: 'title', // title | howto | playing | paused | levelclear | gameover
    score: 0, best: +(localStorage.getItem('nb_best') || 0),
    lives: CFG.lives, level: 0,
    combo: 0, comboTimer: 0,
    overdrive: 0, overdriveMeter: 0, overdriveFlash: 0,
    timeScale: 1, slowT: 0,
    bricks: [], drops: [], balls: [],
    paddle: null, effects: null,
    levelT: 0, sticky: null,
    activePowers: {}, // id -> remaining time (0 = instant/expired)
    hue: 0, t: 0
  };

  var input = new BO.Input(canvas, {});
  input.view = view; // coordinate transform for pointer→field mapping
  var fx = S.effects = new BO.Effects();
  var audio = BO.audio;
  resetPaddleAndBall(); // so the title screen has something to draw

  // powerup timers
  function powerT(id) { return S.activePowers[id] || 0; }
  function activatePower(id) {
    var def = BO.POWERS[id];
    audio.powerup();
    fx.popup(S.paddle.x, S.paddle.y - 40, def.name, def.color, 16);
    fx.ripple(S.paddle.x, S.paddle.y, def.color, 60);
    if (def.kind === 'ball') {
      for (var i = 0; i < S.balls.length; i++) BO.setBallType(S.balls[i], id);
      S.activePowers[id] = def.duration;
    } else if (id === 'multi') {
      var src = S.balls[0];
      if (src) {
        for (var k = 0; k < 2; k++) {
          var nb = BO.makeBall(src.x, src.y, src.type, CFG.baseSpeed);
          nb.stuck = false;
          var a = Math.atan2(src.vy, src.vx) + (k === 0 ? 0.5 : -0.5);
          var sp = Math.hypot(src.vx, src.vy) || CFG.baseSpeed;
          nb.vx = Math.cos(a) * sp; nb.vy = Math.sin(a) * sp;
          S.balls.push(nb);
        }
      }
    } else if (id === 'wide') {
      S.paddle.wideT = def.duration; S.activePowers[id] = def.duration;
    } else if (id === 'slow') {
      S.slowT = def.duration; S.activePowers[id] = def.duration;
      fx.addHitstop(0.12);
    } else if (id === 'magnet') {
      S.paddle.magnetT = def.duration; S.activePowers[id] = def.duration;
    }
  }

  // ---- level setup ----
  function loadLevel(idx) {
    var lv = BO.LEVELS[idx % BO.LEVELS.length];
    var grid = BO.parse(lv);
    S.bricks.length = 0;
    var rows = grid.length, cols = Math.max.apply(null, grid.map(function (r) { return r.length; }));
    var bw = CFG.brickW, bh = CFG.brickH;
    var totalW = cols * (bw + CFG.brickGap) - CFG.brickGap;
    var startX = (CFG.fieldW - totalW) / 2;
    for (var r = 0; r < rows; r++) {
      for (var c = 0; c < grid[r].length; c++) {
        var type = grid[r][c];
        if (!type) continue;
        S.bricks.push({
          type: type, seed: Math.random(), row: r,
          x: startX + c * (bw + CFG.brickGap),
          y: CFG.brickTop + r * (bh + CFG.brickGap),
          w: bw, h: bh,
          flash: 0, dead: false,
          mx: startX + c * (bw + CFG.brickGap) // mover base x
        });
      }
    }
    resetPaddleAndBall();
  }

  function resetPaddleAndBall() {
    S.paddle = BO.makePaddle({ w: CFG.fieldW, h: CFG.fieldH, x: 0 });
    S.balls.length = 0;
    var b = BO.makeBall(S.paddle.x, S.paddle.y - 20, 'normal', CFG.baseSpeed);
    S.balls.push(b);
    S.sticky = null;
  }

  function launchBalls() {
    var launched = false;
    for (var i = 0; i < S.balls.length; i++) {
      var b = S.balls[i];
      if (b.stuck) {
        var v = BO.launchVelocity(b.baseSpeed, 0.2);
        b.vx = v.x; b.vy = v.y; b.stuck = false;
        launched = true;
      }
    }
    if (launched) audio.launch();
  }

  // ---- brick break ----
  function breakBrick(brick, ball, impactSpeed) {
    brick.dead = true;
    var T = BO.TYPES[brick.type];
    S.combo++;
    S.comboTimer = 1.6;
    var pts = BO.brickScore(T.base, S.combo, S.overdriveMult || 1);
    S.score += pts;
    fx.burst(brick.x + brick.w / 2, brick.y + brick.h / 2, T.color,
      14 + Math.min(14, S.combo), 200 + (S.overdriveMult || 1) * 10);
    fx.ripple(brick.x + brick.w / 2, brick.y + brick.h / 2, T.color, 30 + (S.overdriveMult || 1));
    fx.addShake(2 + Math.min(6, S.combo * 0.4) + (ball.type === 'heavy' ? 5 : 0));
    fx.popup(brick.x + brick.w / 2, brick.y, '+' + pts, T.color, 12 + Math.min(8, S.combo));
    audio.brick(S.combo);
    if (S.combo > 0 && S.combo % 5 === 0) {
      fx.addHitstop(0.06); fx.flash = 0.5;
      fx.popup(CFG.fieldW / 2, CFG.fieldH / 2, 'COMBO x' + S.combo, '#fff', 26);
      audio.hitstop();
    }
    // powerup drop
    if (Math.random() < CFG.dropChance) {
      var id = BO.rollDrop();
      S.drops.push(BO.makeDrop(brick.x + brick.w / 2, brick.y + brick.h / 2, id));
    }
  }

  function refuseBrick(brick, ball, reason) {
    var T = BO.TYPES[brick.type];
    brick.flash = 0.3;
    fx.ripple(ball.x, ball.y, '#888', 18);
    if (reason === 'too-slow') {
      audio.clank();
      fx.popup(brick.x + brick.w / 2, brick.y + brick.h / 2, 'TOO SLOW', T.color, 12);
    } else if (reason === 'bad-angle') {
      audio.clank();
      fx.popup(brick.x + brick.w / 2, brick.y + brick.h / 2, 'ANGLE!', T.color, 12);
    } else if (reason === 'phase-closed') {
      audio.phaseFizzle();
      fx.burst(ball.x, ball.y, '#c46bff', 5, 90);
    } else if (reason === 'shielded') {
      audio.shield();
      fx.ripple(brick.x + brick.w / 2, brick.y, T.color, 50);
      fx.popup(brick.x + brick.w / 2, brick.y, 'SHIELDED', T.color, 12);
    }
  }

  // ---- collision: ball vs bricks (swept, one substep) ----
  function ballBricks(b, dt) {
    // Ghost ball: periodically phases through one brick (consumes the charge).
    if (b.type === 'ghost' && b.ghostReady) {
      var wouldHit = false;
      for (var g = 0; g < S.bricks.length; g++) {
        if (S.bricks[g].dead) continue;
        if (BO.sweptCircleRect(b.px, b.py, b.x, b.y, b.r,
            S.bricks[g].x, S.bricks[g].y, S.bricks[g].w, S.bricks[g].h)) {
          wouldHit = true; break;
        }
      }
      if (wouldHit) {
        b.ghostReady = false; b.ghostT = 0;
        b.ghostBrick = S.bricks[g]; // remember which brick we're phasing through
        fx.ripple(b.x, b.y, BO.BALL_TYPES.ghost.glow, 26);
        return false; // pass straight through, no bounce, no break
      }
    }
    // clear the phase-through link once we're clear of that brick (with a
    // small margin so the borderline-touching frame can't re-register a hit)
    if (b.type === 'ghost' && b.ghostBrick) {
      var gb = b.ghostBrick;
      var m = b.r + 10;
      if (gb.dead ||
          b.x + m < gb.x || b.x - m > gb.x + gb.w ||
          b.y + m < gb.y || b.y - m > gb.y + gb.h) {
        b.ghostBrick = null;
      }
    }
    var x0 = b.px, y0 = b.py, x1 = b.x, y1 = b.y;
    var best = null, bestBrick = null;
    for (var i = 0; i < S.bricks.length; i++) {
      var br = S.bricks[i];
      if (br.dead) continue;
      // ghost mid-phase: ignore the exact brick we are phasing through so the
      // pass-through never registers a face hit on it
      if (b.type === 'ghost' && b.ghostBrick === br) continue;
      var hit = BO.sweptCircleRect(x0, y0, x1, y1, b.r, br.x, br.y, br.w, br.h);
      if (hit && (!best || hit.t < best.t)) { best = hit; bestBrick = br; }
    }
    if (!best) return false;
    var speed = Math.hypot(b.vx, b.vy);
    var face = BO.faceFromNormal(best.nx, best.ny);
    var res = BO.hitTest(bestBrick, b, S.t, face, speed);
    if (res.ok) {
      breakBrick(bestBrick, b, speed);
      // reflect around the hit point (unless piercing)
      if (b.type === 'fire' || b.type === 'ghost') {
        // fireball pierces; ghost that breaks a brick keeps phasing momentum
      } else {
        b.x = x0 + (x1 - x0) * best.t;
        b.y = y0 + (y1 - y0) * best.t;
        var v = BO.reflect(b.vx, b.vy, best.nx, best.ny);
        b.vx = v.x; b.vy = v.y;
        if (b.type !== 'rubber') BO.capSpeed(b, b.baseSpeed * 1.25);
      }
      if (b.type === 'ghost') b.ghostReady = true; // recharge after a break
      return true;
    } else {
      // bounce off harmlessly
      b.x = x0 + (x1 - x0) * best.t;
      b.y = y0 + (y1 - y0) * best.t;
      var rv = BO.reflect(b.vx, b.vy, best.nx, best.ny);
      b.vx = rv.x; b.vy = rv.y;
      refuseBrick(bestBrick, b, res.reason);
      return false;
    }
  }

  // ---- collision: ball vs paddle (swept) ----
  function ballPaddle(b, dt) {
    var p = S.paddle;
    var hit = BO.sweptCircleRect(b.px, b.py, b.x, b.y, b.r,
      p.x - p.w / 2, p.y - p.h / 2, p.w, p.h);
    if (!hit || hit.t === undefined) return;
    if (hit.inside) return;
    var rel = BO.clamp((b.x - p.x) / (p.w / 2), -1, 1);
    var v = BO.paddleBounce(b.vx, b.vy, rel, p.vx, p.vy, CFG);
    b.vx = v.x; b.vy = v.y;
    b.y = p.y - p.h / 2 - b.r - 1;
    b.x = BO.clamp(b.x, p.x - p.w / 2 + b.r, p.x + p.w / 2 - b.r);
    S.combo = 0; // combo resets on paddle touch
    p.squash = 1;
    fx.ripple(b.x, b.y + b.r, '#fff', 24);
    fx.burst(b.x, b.y, '#fff', 4, 100);
    audio.paddle(S.combo);
    if (p.magnetT > 0) {
      b.stuck = true;
      b.stickOff = rel * (p.w / 2 - b.r);
    }
  }

  // ---- overdrive ----
  function fieldTop() { return CFG.brickTop - 40; }
  function updateOverdrive(balls, dt) {
    var above = 0;
    for (var i = 0; i < balls.length; i++) {
      if (!balls[i].stuck && balls[i].y < fieldTop()) above++;
    }
    if (above > 0) {
      if (S.overdrive === 0 && S.overdriveFlash <= 0) {
        // entering overdrive
        S.overdriveFlash = 1;
        audio.overdriveEnter();
        audio.startArp();
        fx.addHitstop(0.14); fx.flash = 1;
        fx.popup(CFG.fieldW / 2, fieldTop() - 30, 'OVERDRIVE!', '#ffd24d', 34);
      }
      S.overdrive += dt;
      var od = BO.overdrive(S.overdrive, CFG.overdrive);
      S.overdriveMeter = od.meter;
      S.overdriveMult = od.mult;
      audio.setArpRate(2 + od.mult * 1.2);
    } else {
      if (S.overdrive > 0) {
        audio.stopArp();
        var kept = Math.round(S.overdrive * 10);
        S.score += kept;
        if (kept > 0) fx.popup(CFG.fieldW / 2, fieldTop(), '+' + kept + ' OVERDRIVE', '#ffd24d', 18);
      }
      S.overdrive = 0; S.overdriveMeter = 0; S.overdriveMult = 1;
    }
  }

  // ---- main step (fixed dt) ----
  function step(dt) {
    S.t += dt;
    var p = S.paddle;

    // thumbstick influence
    var stick = input.consumeStick();
    if (stick && (stick.x || stick.y)) {
      p.targetX += stick.x * 900 * dt;
      p.targetY += stick.y * 500 * dt;
    }

    BO.updatePaddle(p, input, dt, { w: CFG.fieldW, h: CFG.fieldH, x: 0 });

    // stuck ball follows paddle
    for (var i = 0; i < S.balls.length; i++) {
      var b = S.balls[i];
      if (b.stuck) {
        if (b.stickOff !== undefined) b.x = p.x + b.stickOff;
        else b.x = p.x;
        b.y = p.y - p.h / 2 - b.r - 1;
        b.px = b.x; b.py = b.y;
      }
    }

    // drops
    for (i = 0; i < S.drops.length; i++) {
      var d = S.drops[i];
      if (!d.alive) continue;
      d.t += dt;
      d.y += d.vy * dt;
      if (d.y > CFG.fieldH + 30) { d.alive = false; continue; }
      if (Math.abs(d.x - p.x) < p.w / 2 + d.w / 2 &&
          Math.abs(d.y - p.y) < p.h / 2 + d.h / 2 + 6) {
        d.alive = false;
        activatePower(d.id);
      }
    }

    // balls (substep so fast balls don't tunnel)
    var iterations = 3;
    var sub = dt / iterations;
    for (var it = 0; it < iterations; it++) {
      for (i = 0; i < S.balls.length; i++) {
        b = S.balls[i];
        if (!b.alive || b.stuck) continue;
        b.px = b.x; b.py = b.y;
        b.x += b.vx * sub; b.y += b.vy * sub;
        // walls
        if (b.x - b.r < 0) { b.x = b.r; b.vx = Math.abs(b.vx); onWall(b); }
        else if (b.x + b.r > CFG.fieldW) { b.x = CFG.fieldW - b.r; b.vx = -Math.abs(b.vx); onWall(b); }
        if (b.y - b.r < 0) { b.y = b.r; b.vy = Math.abs(b.vy); onWall(b); }
        // paddle
        ballPaddle(b, sub);
        // bricks
        var guard = 0;
        while (ballBricks(b, sub) && guard++ < 4) { /* multi-hit substeps */ }
        // speed cap + degenerate-angle nudge
        BO.capSpeed(b, CFG.maxSpeed);
        BO.nudgeVelocity(b, 8, 6);
        // trails
        if (fx.particles) fx.trailPuff(b.x, b.y, BO.BALL_TYPES[b.type].color, 2 + b.r * 0.3);
        // ghost phase recharge (time-based, so it phases periodically)
        if (b.type === 'ghost' && !b.ghostReady) {
          b.ghostT += sub;
          if (b.ghostT >= 2.5) { b.ghostReady = true; b.ghostT = 0; }
        }
      }
    }

    // ball loss
    for (i = S.balls.length - 1; i >= 0; i--) {
      b = S.balls[i];
      if (!b.stuck && b.y - b.r > CFG.fieldH + 20) {
        S.balls.splice(i, 1);
        fx.addShake(6);
      }
    }
    if (S.balls.length === 0) {
      loseLife();
      return;
    }

    // bricks update (flash decay, mover drift)
    for (i = 0; i < S.bricks.length; i++) {
      var br = S.bricks[i];
      if (br.dead) continue;
      if (br.flash > 0) br.flash -= dt * 3;
      if (br.type === 'mover') {
        var off = BO.moverOffsetAt(br, S.t);
        br.x = BO.clamp(br.mx + off, 2, CFG.fieldW - br.w - 2);
      }
    }

    // combo decay
    if (S.comboTimer > 0) {
      S.comboTimer -= dt;
      if (S.comboTimer <= 0) S.combo = 0;
    }

    updateOverdrive(S.balls, dt);

    // powerup timers
    for (var key in S.activePowers) {
      S.activePowers[key] -= dt;
      if (S.activePowers[key] <= 0) delete S.activePowers[key];
    }
    if (S.slowT > 0) S.slowT -= dt;
    S.timeScale = S.slowT > 0 ? 0.35 : 1;

    // level clear
    var remaining = 0;
    for (i = 0; i < S.bricks.length; i++) if (!S.bricks[i].dead) remaining++;
    if (remaining === 0) {
      S.mode = 'levelclear';
      S.levelT = 0;
      audio.stopArp();
      S.score += 500 + S.lives * 100;
    }

    // hue drift for background — biased into the cohesive dark blue/violet
    // band (200-300°) so play and title share one base tone (no olive cast)
    var hueSpan = 100; // width of the biased band
    S.hue = (200 + hueSpan / 2 +
      Math.sin(S.t * (0.05 + 0.015 * (S.overdriveMult || 1))) * (hueSpan / 2)) % 360;
  }

  function onWall(b) {
    audio.wall();
    fx.ripple(b.x, b.y, '#4d7dff', 16);
    fx.burst(b.x, b.y, '#4d7dff', 3, 70);
  }

  function loseLife() {
    S.lives--;
    audio.lose();
    fx.addShake(14); fx.flash = 0.6;
    audio.stopArp();
    S.overdrive = 0; S.overdriveMult = 1; S.overdriveMeter = 0;
    if (S.lives <= 0) {
      S.mode = 'gameover';
      audio.gameOver();
      if (S.score > S.best) {
        S.best = S.score;
        localStorage.setItem('nb_best', S.best);
      }
    } else {
      resetPaddleAndBall();
    }
  }

  // ================= RENDERING =================
  function draw() {
    var W = view.w, H = view.h;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    // background
    var odGlow = S.overdriveMeter;
    var bgGrad = ctx.createLinearGradient(0, 0, 0, H);
    var hue = S.hue;
    bgGrad.addColorStop(0, 'hsl(' + hue + ',50%,' + (7 + odGlow * 6) + '%)');
    bgGrad.addColorStop(1, 'hsl(' + ((hue + 30) % 360) + ',55%,3%)');
    ctx.fillStyle = bgGrad;
    ctx.fillRect(0, 0, W, H);

    ctx.save();
    ctx.translate(view.ox + fx.shakeX, view.oy + fx.shakeY);
    ctx.scale(view.scale, view.scale);

    // starfield
    ctx.save();
    for (var i = 0; i < fx.stars.length; i++) {
      var st = fx.stars[i];
      var sx = st.x * CFG.fieldW, sy = st.y * CFG.fieldH;
      ctx.fillStyle = 'rgba(255,255,255,' + (0.15 + st.s * 0.2) + ')';
      ctx.fillRect(sx, sy, st.s, st.s);
    }
    ctx.restore();

    // ambience: faint drifting neon grid — always subtly visible, brightens
    // and warms with overdrive meter (additive, ~40 lines, no allocations)
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    ctx.lineWidth = 1;
    var gridStep = 44;
    var gOff = (S.t * 14) % gridStep;
    var gA = 0.028 + odGlow * 0.14;      // vertical lines
    ctx.strokeStyle = 'rgba(96,140,255,' + gA + ')';
    ctx.beginPath();
    for (var gx = gOff; gx < CFG.fieldW; gx += gridStep) {
      ctx.moveTo(gx, 0); ctx.lineTo(gx, CFG.fieldH);
    }
    ctx.stroke();
    ctx.strokeStyle = 'rgba(150,110,255,' + (gA * 0.9) + ')';
    ctx.beginPath();
    for (var gy = gOff; gy < CFG.fieldH; gy += gridStep) {
      ctx.moveTo(0, gy); ctx.lineTo(CFG.fieldW, gy);
    }
    ctx.stroke();
    // one bright scanline shimmer sweeping down the field
    var scanY = (S.t * 90) % (CFG.fieldH + 120) - 60;
    var scanA = 0.03 + odGlow * 0.2;
    var scanG = ctx.createLinearGradient(0, scanY - 30, 0, scanY + 30);
    scanG.addColorStop(0, 'rgba(125,252,255,0)');
    scanG.addColorStop(0.5, 'rgba(125,252,255,' + scanA + ')');
    scanG.addColorStop(1, 'rgba(125,252,255,0)');
    ctx.fillStyle = scanG;
    ctx.fillRect(0, scanY - 30, CFG.fieldW, 60);
    // legacy overdrive energy lines (strong, gold) on top when in overdrive
    if (odGlow > 0.02) {
      ctx.strokeStyle = 'rgba(255,210,77,' + (0.03 + odGlow * 0.06) + ')';
      ctx.beginPath();
      for (var oy2 = (S.t * 60 % 44); oy2 < CFG.fieldH; oy2 += 44) {
        ctx.moveTo(0, oy2); ctx.lineTo(CFG.fieldW, oy2);
      }
      ctx.stroke();
    }
    ctx.restore();

    // bricks
    for (i = 0; i < S.bricks.length; i++) {
      var br = S.bricks[i];
      if (br.dead) continue;
      drawBrick(br);
    }

    // drops
    for (i = 0; i < S.drops.length; i++) {
      var d = S.drops[i];
      if (!d.alive) continue;
      var def = BO.POWERS[d.id];
      ctx.save();
      ctx.translate(d.x, d.y);
      ctx.rotate(Math.sin(d.t * 4) * 0.2);
      ctx.fillStyle = def.color;
      ctx.globalAlpha = 0.9;
      ctx.fillRect(-d.w / 2, -d.h / 2, d.w, d.h);
      ctx.globalAlpha = 1;
      ctx.fillStyle = '#0a0a12';
      ctx.font = 'bold 9px monospace';
      ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
      ctx.fillText(def.name.slice(0, 2), 0, 0.5);
      ctx.restore();
    }

    // particles (additive)
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    fx.particles.each(function (p) {
      var a = p.life / p.maxLife;
      ctx.globalAlpha = a * 0.9;
      ctx.fillStyle = p.color;
      var s = p.size * (0.5 + a * 0.5);
      ctx.fillRect(p.x - s / 2, p.y - s / 2, s, s);
    });
    ctx.restore();

    // ripples
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    fx.ripples.each(function (r) {
      var a = r.life / r.maxLife;
      ctx.globalAlpha = a * 0.6;
      ctx.strokeStyle = r.color;
      ctx.lineWidth = 2;
      ctx.beginPath();
      ctx.arc(r.x, r.y, r.maxR * (1 - a), 0, Math.PI * 2);
      ctx.stroke();
    });
    ctx.restore();

    // ball trails: additive comet streak, colored by ball type, fading
    // alpha + shrinking radius along the trail
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    // soft halo pass behind the streak
    fx.trailDots.each(function (d) {
      var a = d.life / d.maxLife;
      ctx.globalAlpha = a * a * 0.18;
      ctx.fillStyle = d.color;
      ctx.beginPath();
      ctx.arc(d.x, d.y, d.size * (1.1 + a * 1.4), 0, Math.PI * 2);
      ctx.fill();
    });
    // bright core pass
    fx.trailDots.each(function (d) {
      var a = d.life / d.maxLife;
      ctx.globalAlpha = a * a * 0.55;
      ctx.fillStyle = d.color;
      ctx.beginPath();
      ctx.arc(d.x, d.y, Math.max(0.5, d.size * (0.35 + a * 0.65)), 0, Math.PI * 2);
      ctx.fill();
    });
    ctx.globalAlpha = 1;
    ctx.restore();

    // paddle (squash & stretch)
    var p = S.paddle;
    if (p) {
    ctx.save();
    ctx.translate(p.x, p.y);
    var sq = p.squash;
    var pw = p.w * (1 + sq * 0.15), ph = p.h * (1 - sq * 0.35);
    // outer soft halo
    ctx.globalCompositeOperation = 'lighter';
    ctx.fillStyle = 'rgba(90,180,255,0.10)';
    roundRect(-pw / 2 - 8, -ph / 2 - 8, pw + 16, ph + 16, 13);
    ctx.fill();
    ctx.globalCompositeOperation = 'source-over';
    // neon body: bright top → saturated mid → deep base
    var pg = ctx.createLinearGradient(0, -ph / 2, 0, ph / 2);
    pg.addColorStop(0, '#d8fbff');
    pg.addColorStop(0.3, '#8ff7ff');
    pg.addColorStop(1, '#1f5bff');
    ctx.fillStyle = pg;
    ctx.shadowColor = 'rgba(90,180,255,0.9)';
    ctx.shadowBlur = 14 + S.overdriveMeter * 16;
    roundRect(-pw / 2, -ph / 2, pw, ph, 7);
    ctx.fill();
    ctx.shadowBlur = 0;
    // bright neon edge
    ctx.strokeStyle = 'rgba(190,245,255,0.9)';
    ctx.lineWidth = 1.5;
    roundRect(-pw / 2 + 0.5, -ph / 2 + 0.5, pw - 1, ph - 1, 6);
    ctx.stroke();
    // bright center accent segment (hot core)
    var accW = Math.min(34, pw * 0.26);
    var accG = ctx.createLinearGradient(0, -ph / 2, 0, ph / 2);
    accG.addColorStop(0, '#ffffff');
    accG.addColorStop(1, '#7dfcff');
    ctx.fillStyle = accG;
    ctx.shadowColor = 'rgba(255,255,255,0.9)';
    ctx.shadowBlur = 8;
    roundRect(-accW / 2, -ph / 2 + 2, accW, ph - 4, 4);
    ctx.fill();
    ctx.shadowBlur = 0;
    if (p.magnetT > 0) {
      ctx.strokeStyle = 'rgba(196,107,255,' + (0.4 + 0.3 * Math.sin(S.t * 8)) + ')';
      ctx.lineWidth = 2;
      roundRect(-pw / 2 - 4, -ph / 2 - 4, pw + 8, ph + 8, 9);
      ctx.stroke();
    }
    ctx.restore();
    } // end if (p)

    // balls + trails
    for (i = 0; i < S.balls.length; i++) {
      var b = S.balls[i];
      var bt = BO.BALL_TYPES[b.type];
      ctx.save();
      ctx.globalCompositeOperation = 'lighter';
      ctx.fillStyle = bt.glow;
      ctx.beginPath();
      ctx.arc(b.x, b.y, b.r * 1.7, 0, Math.PI * 2);
      ctx.globalAlpha = 0.25;
      ctx.fill();
      ctx.globalAlpha = 1;
      ctx.restore();
      ctx.fillStyle = bt.color;
      ctx.beginPath();
      ctx.arc(b.x, b.y, b.r, 0, Math.PI * 2);
      ctx.fill();
    }

    // ghost phase indicator ring for ghost balls
    for (i = 0; i < S.balls.length; i++) {
      var gb = S.balls[i];
      if (gb.type === 'ghost') {
        ctx.save();
        ctx.strokeStyle = 'rgba(125,252,255,0.5)';
        ctx.lineWidth = 1.5;
        ctx.beginPath();
        ctx.arc(gb.x, gb.y, gb.r + 5 + Math.sin(S.t * 6) * 2, 0, Math.PI * 2);
        ctx.stroke();
        ctx.restore();
      }
    }

    // popups
    fx.popups.each(function (pp) {
      var a = pp.life / pp.maxLife;
      ctx.globalAlpha = a;
      ctx.fillStyle = pp.color;
      ctx.font = 'bold ' + pp.size + 'px monospace';
      ctx.textAlign = 'center';
      ctx.fillText(pp.text, pp.x, pp.y);
      ctx.globalAlpha = 1;
    });

    // overdrive boundary line
    if (S.overdrive > 0) {
      ctx.save();
      ctx.globalCompositeOperation = 'lighter';
      ctx.strokeStyle = 'rgba(255,210,77,' + (0.2 + 0.3 * Math.sin(S.t * 10)) + ')';
      ctx.lineWidth = 2;
      ctx.setLineDash([12, 10]);
      ctx.beginPath();
      ctx.moveTo(0, fieldTop());
      ctx.lineTo(CFG.fieldW, fieldTop());
      ctx.stroke();
      ctx.restore();
    }

    ctx.restore(); // field transform

    // virtual thumbstick (touch)
    var stk = input.thumbstick;
    if (stk.enabled && stk.active) {
      ctx.save();
      ctx.globalAlpha = 0.35;
      ctx.strokeStyle = '#8f9bff';
      ctx.lineWidth = 3;
      ctx.beginPath();
      ctx.arc(stk.ox * view.scale + view.ox, stk.oy * view.scale + view.oy,
        70 * view.scale, 0, Math.PI * 2);
      ctx.stroke();
      ctx.fillStyle = '#8f9bff';
      ctx.beginPath();
      ctx.arc((stk.ox + stk.dx * 70) * view.scale + view.ox,
              (stk.oy + stk.dy * 70) * view.scale + view.oy,
        24 * view.scale, 0, Math.PI * 2);
      ctx.fill();
      ctx.restore();
    }

    drawHUD(S.mode === 'playing');
    drawOverlays();

    // title ambience: paddle drifting slowly with a ball bouncing above it
    if (S.mode === 'title') {
      var tt = S.t;
      var ambP = { x: CFG.fieldW / 2 + Math.sin(tt * 0.5) * (CFG.fieldW * 0.26),
                   y: CFG.fieldH * 0.88, w: 110, h: 14, squash: 0,
                   magnetT: 0, wideT: 0 };
      S.paddle = ambP;
      var bounceT = Math.abs((tt * 0.7) % 2 - 1); // 0..1 triangle wave
      var ambBall = {
        x: ambP.x, r: 7, type: 'normal',
        y: ambP.y - 40 - bounceT * (CFG.fieldH * 0.42),
        vx: 0, vy: 0
      };
      var bt2 = BO.BALL_TYPES.normal;
      ctx.save();
      ctx.translate(view.ox + fx.shakeX, view.oy + fx.shakeY);
      ctx.scale(view.scale, view.scale);
      // ambient neon paddle (drifts with the ball above it)
      var apg = ctx.createLinearGradient(0, ambP.y - 7, 0, ambP.y + 7);
      apg.addColorStop(0, '#d8fbff');
      apg.addColorStop(0.3, '#8ff7ff');
      apg.addColorStop(1, '#1f5bff');
      ctx.fillStyle = apg;
      ctx.shadowColor = 'rgba(90,180,255,0.9)';
      ctx.shadowBlur = 14;
      roundRect(ambP.x - 55, ambP.y - 7, 110, 14, 7);
      ctx.fill();
      ctx.shadowBlur = 0;
      ctx.strokeStyle = 'rgba(190,245,255,0.9)';
      ctx.lineWidth = 1.5;
      roundRect(ambP.x - 54.5, ambP.y - 6.5, 109, 13, 6);
      ctx.stroke();
      ctx.fillStyle = '#ffffff';
      roundRect(ambP.x - 15, ambP.y - 5, 30, 10, 4);
      ctx.fill();
      // trail ghost dots behind the bouncing ball
      ctx.globalCompositeOperation = 'lighter';
      for (var tg = 1; tg <= 4; tg++) {
        var tb = Math.abs(((tt - tg * 0.045) * 0.7) % 2 - 1);
        var ty2 = ambP.y - 40 - tb * (CFG.fieldH * 0.42);
        ctx.globalAlpha = 0.12 - tg * 0.022;
        ctx.fillStyle = '#ffffff';
        ctx.beginPath();
        ctx.arc(ambP.x - tg * 3, ty2, 7 * (1 - tg * 0.12), 0, Math.PI * 2);
        ctx.fill();
      }
      ctx.globalAlpha = 0.3;
      ctx.fillStyle = bt2.glow;
      ctx.beginPath();
      ctx.arc(ambBall.x, ambBall.y, 12, 0, Math.PI * 2);
      ctx.fill();
      ctx.globalAlpha = 1;
      ctx.globalCompositeOperation = 'source-over';
      ctx.fillStyle = bt2.color;
      ctx.beginPath();
      ctx.arc(ambBall.x, ambBall.y, ambBall.r, 0, Math.PI * 2);
      ctx.fill();
      ctx.restore();
      S.paddle = null;
    }

    // vignette pulsing with multiplier
    if (S.overdriveMeter > 0.02) {
      var vg = ctx.createRadialGradient(W / 2, H / 2, H * 0.3, W / 2, H / 2, H * 0.75);
      var va = 0.1 + S.overdriveMeter * 0.35;
      vg.addColorStop(0, 'rgba(255,180,40,0)');
      vg.addColorStop(1, 'rgba(255,160,30,' + va + ')');
      ctx.fillStyle = vg;
      ctx.fillRect(0, 0, W, H);
    }
    // white flash
    if (fx.flash > 0.01) {
      ctx.fillStyle = 'rgba(255,255,255,' + (fx.flash * 0.35) + ')';
      ctx.fillRect(0, 0, W, H);
    }
  }

  function roundRect(x, y, w, h, r) {
    ctx.beginPath();
    ctx.moveTo(x + r, y);
    ctx.arcTo(x + w, y, x + w, y + h, r);
    ctx.arcTo(x + w, y + h, x, y + h, r);
    ctx.arcTo(x, y + h, x, y, r);
    ctx.arcTo(x, y, x + w, y, r);
    ctx.closePath();
  }

  function drawBrick(br) {
    var T = BO.TYPES[br.type];
    var x = br.x, y = br.y, w = br.w, h = br.h;
    var flash = Math.max(0, br.flash);
    // standard bricks pick a per-row neon palette color; specials keep identity
    var col = T.color, glow = T.glow;
    if (br.type === 'standard') {
      var pal = BO.STANDARD_PALETTE[(br.row || 0) % BO.STANDARD_PALETTE.length];
      col = pal.color; glow = pal.glow;
    }
    ctx.save();
    ctx.shadowColor = glow;
    ctx.shadowBlur = 10 + (S.overdriveMeter * 6);
    var g = ctx.createLinearGradient(x, y, x, y + h);
    g.addColorStop(0, shade(col, 42));   // lit top edge
    g.addColorStop(0.45, col);           // saturated body
    g.addColorStop(1, shade(col, -55));  // dark base
    ctx.fillStyle = g;
    ctx.globalAlpha = 1;
    roundRect(x, y, w, h, 4);
    ctx.fill();
    ctx.shadowBlur = 0;
    // thin luminous border
    ctx.strokeStyle = glow;
    ctx.lineWidth = 1;
    roundRect(x + 0.5, y + 0.5, w - 1, h - 1, 4);
    ctx.stroke();
    // bevel: 1px lighter inner top-edge highlight
    ctx.strokeStyle = shade(col, 85);
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.moveTo(x + 3, y + 2.5);
    ctx.lineTo(x + w - 3, y + 2.5);
    ctx.stroke();
    // flash overlay
    if (flash > 0) {
      ctx.globalAlpha = flash;
      ctx.fillStyle = '#fff';
      roundRect(x, y, w, h, 4);
      ctx.fill();
      ctx.globalAlpha = 1;
    }
    // type markers
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    if (br.type === 'phase') {
      var open = BO.phaseOpenAt(br, S.t);
      ctx.globalAlpha = open ? 0.9 : 0.25;
      ctx.fillStyle = open ? '#fff' : '#3a2a4a';
      ctx.font = 'bold 13px monospace';
      ctx.fillText(open ? '◆' : '◇', x + w / 2, y + h / 2);
      ctx.globalAlpha = 1;
      if (open) {
        ctx.strokeStyle = 'rgba(255,255,255,0.7)';
        ctx.lineWidth = 1.5;
        roundRect(x + 1, y + 1, w - 2, h - 2, 4);
        ctx.stroke();
      }
    } else if (br.type === 'speed') {
      var n = BO.chevronsFor(T.threshold);
      ctx.fillStyle = 'rgba(0,0,0,0.55)';
      var cw = 10;
      var startX = x + w / 2 - (n * cw) / 2;
      for (var c = 0; c < n; c++) {
        ctx.beginPath();
        ctx.moveTo(startX + c * cw + 2, y + h / 2 + 5);
        ctx.lineTo(startX + c * cw + 7, y + h / 2);
        ctx.lineTo(startX + c * cw + 2, y + h / 2 - 5);
        ctx.strokeStyle = '#fff';
        ctx.lineWidth = 2.5;
        ctx.stroke();
      }
    } else if (br.type === 'angle') {
      // draw angle wedge indicator (steep hits: |angle from vertical| < window)
      ctx.strokeStyle = 'rgba(255,255,255,0.85)';
      ctx.lineWidth = 1.5;
      ctx.beginPath();
      var half = T.halfWindow * Math.PI / 180;
      ctx.moveTo(x + w / 2, y + h + 2);
      ctx.lineTo(x + w / 2 - Math.sin(half) * 16, y + h + Math.cos(half) * 16);
      ctx.moveTo(x + w / 2, y + h + 2);
      ctx.lineTo(x + w / 2 + Math.sin(half) * 16, y + h + Math.cos(half) * 16);
      ctx.stroke();
    } else if (br.type === 'shielded') {
      // shield bar across the top face
      ctx.fillStyle = 'rgba(255,255,255,0.9)';
      ctx.fillRect(x + 2, y - 2.5, w - 4, 3);
      ctx.fillStyle = 'rgba(255,225,77,0.35)';
      ctx.fillRect(x + 2, y - 2.5, w - 4, 3);
    } else if (br.type === 'mover') {
      ctx.strokeStyle = 'rgba(0,0,0,0.4)';
      ctx.lineWidth = 1;
      ctx.beginPath();
      ctx.moveTo(x + 6, y + h / 2); ctx.lineTo(x + w - 6, y + h / 2);
      ctx.stroke();
      ctx.fillStyle = 'rgba(0,0,0,0.5)';
      ctx.beginPath();
      ctx.arc(x + w / 2, y + h / 2, 4, 0, Math.PI * 2);
      ctx.fill();
    }
    ctx.restore();
  }

  function shade(hex, amt) {
    var n = parseInt(hex.slice(1), 16);
    var r = Math.max(0, Math.min(255, (n >> 16) + amt));
    var g = Math.max(0, Math.min(255, ((n >> 8) & 255) + amt));
    var b = Math.max(0, Math.min(255, (n & 255) + amt));
    return 'rgb(' + r + ',' + g + ',' + b + ')';
  }

  // ================= HUD =================
  function drawHUD(showGame) {
    var W = view.w;
    ctx.save();
    ctx.textBaseline = 'top';
    if (!showGame) { ctx.restore(); return; }
    var fs = Math.max(14, Math.min(20, W * 0.024));
    ctx.font = '700 ' + fs + 'px "Segoe UI", system-ui, sans-serif';
    ctx.fillStyle = 'rgba(255,255,255,0.92)';
    ctx.textAlign = 'left';
    ctx.fillText('SCORE ' + S.score, 16, 12);
    ctx.textAlign = 'center';
    ctx.fillText('LVL ' + (S.level + 1) + '/' + BO.LEVELS.length, W / 2, 12);
    ctx.textAlign = 'right';
    ctx.fillText('♥'.repeat(Math.max(0, S.lives)), W - 16, 12);

    // multiplier — always present for intentional layout; faint x1
    // placeholder at rest, hot gold when overdriving
    var mult = S.overdriveMult || 1;
    ctx.textAlign = 'center';
    if (mult > 1.01) {
      ctx.font = '800 ' + (fs + 6 + S.overdriveMeter * 8) + 'px "Segoe UI", system-ui, sans-serif';
      ctx.fillStyle = 'hsl(45,100%,' + (60 + S.overdriveMeter * 20) + '%)';
      ctx.fillText('x' + mult.toFixed(1), W / 2, 40 + S.overdriveMeter * 4);
    } else {
      ctx.font = '800 ' + (fs + 4) + 'px "Segoe UI", system-ui, sans-serif';
      ctx.fillStyle = 'rgba(255,255,255,0.35)';
      ctx.fillText('x1', W / 2, 42);
    }

    // combo
    if (S.combo > 1) {
      ctx.textAlign = 'left';
      ctx.font = '700 ' + fs + 'px "Segoe UI", system-ui, sans-serif';
      ctx.fillStyle = '#ff9d5c';
      ctx.fillText('COMBO x' + S.combo, 16, 40);
    }

    // active powerups: colored capsule chips on the right edge with a
    // remaining-time bar underneath each
    ctx.textAlign = 'right';
    var chipH = Math.max(16, fs - 1);
    var chipY = 38;
    var chipW = 74;
    for (var key in S.activePowers) {
      var def = BO.POWERS[key];
      if (!def) continue;
      var frac = def.duration ? Math.max(0, Math.min(1, S.activePowers[key] / def.duration)) : 0;
      // chip body (translucent tint)
      ctx.fillStyle = 'rgba(255,255,255,0.07)';
      roundRect(W - 16 - chipW, chipY, chipW, chipH, (chipH - 2) / 2);
      ctx.fill();
      // colored outline + label
      ctx.strokeStyle = def.color;
      ctx.lineWidth = 1;
      roundRect(W - 16 - chipW, chipY, chipW, chipH, (chipH - 2) / 2);
      ctx.stroke();
      ctx.fillStyle = def.color;
      ctx.font = '700 ' + Math.max(10, fs - 5) + 'px "Segoe UI", system-ui, sans-serif';
      ctx.textAlign = 'center';
      ctx.textBaseline = 'middle';
      ctx.fillText(def.name.slice(0, 2), W - 16 - chipW / 2, chipY + chipH / 2 - 3);
      ctx.textBaseline = 'top';
      // remaining-time bar along the chip bottom
      ctx.fillStyle = def.color;
      ctx.globalAlpha = 0.9;
      ctx.fillRect(W - 16 - chipW + 3, chipY + chipH - 3, (chipW - 6) * frac, 2);
      ctx.globalAlpha = 1;
      chipY += chipH + 6;
    }

    // overdrive meter: permanent slim track at top, fills + brightens
    {
      var mw = W * 0.4, mx = (W - mw) / 2, my = 70;
      ctx.fillStyle = 'rgba(255,255,255,0.08)';
      roundRect(mx, my, mw, 6, 3);
      ctx.fill();
      if (S.overdriveMeter > 0.005) {
        var mCol = 'hsl(' + (45 - S.overdriveMeter * 45) + ',100%,' + (55 + S.overdriveMeter * 15) + '%)';
        ctx.fillStyle = mCol;
        ctx.shadowColor = mCol;
        ctx.shadowBlur = 6 + S.overdriveMeter * 8;
        roundRect(mx, my, Math.max(6, mw * S.overdriveMeter), 6, 3);
        ctx.fill();
        ctx.shadowBlur = 0;
      }
    }

    // mute icon
    ctx.textAlign = 'left';
    ctx.font = '600 14px "Segoe UI", system-ui, sans-serif';
    ctx.fillStyle = 'rgba(255,255,255,0.45)';
    ctx.fillText(audio.muted ? '🔇 M' : '🔊 M', 16, view.h - 28);
    ctx.restore();
  }

  // ================= SCREENS =================
  function drawOverlays() {
    var W = view.w, H = view.h;
    ctx.save();
    ctx.textAlign = 'center';
    if (S.mode === 'title') {
      ctx.fillStyle = 'rgba(5,5,15,0.75)';
      ctx.fillRect(0, 0, W, H);
      ctx.fillStyle = '#7dfcff';
      ctx.font = '800 ' + Math.min(64, W * 0.09) + 'px "Segoe UI", system-ui, sans-serif';
      ctx.shadowColor = '#7dfcff';
      ctx.shadowBlur = 24;
      ctx.fillText('NEON BREAKOUT', W / 2, H * 0.28);
      ctx.shadowBlur = 0;
      ctx.fillStyle = 'rgba(255,255,255,0.85)';
      ctx.font = '500 ' + Math.max(14, W * 0.022) + 'px "Segoe UI", system-ui, sans-serif';
      ctx.fillText('Break through the wall. Get ABOVE it. Stay there.', W / 2, H * 0.40);
      ctx.font = '500 ' + Math.max(12, W * 0.018) + 'px "Segoe UI", system-ui, sans-serif';
      ctx.fillStyle = 'rgba(255,255,255,0.6)';
      ctx.fillText('MOUSE move · CLICK/SPACE launch · ARROWS/WASD move (up/down too!)', W / 2, H * 0.52);
      ctx.fillText('TOUCH: drag anywhere to steer · tap to launch · double-tap pause', W / 2, H * 0.57);
      ctx.fillText('Catch capsules: fire · iron · multiball · ghost · rubber · wide · slow-mo · magnet', W / 2, H * 0.63);
      ctx.fillText('T toggles thumbstick · M mutes · P pauses', W / 2, H * 0.665);
      ctx.fillStyle = '#ffd24d';
      ctx.font = '700 ' + Math.max(16, W * 0.028) + 'px "Segoe UI", system-ui, sans-serif';
      ctx.fillText('TAP / CLICK / SPACE TO START', W / 2, H * 0.74);
      ctx.fillStyle = 'rgba(255,255,255,0.4)';
      ctx.font = '500 13px "Segoe UI", system-ui, sans-serif';
      ctx.fillText('BEST: ' + S.best, W / 2, H * 0.82);
    } else if (S.mode === 'paused') {
      ctx.fillStyle = 'rgba(5,5,15,0.6)';
      ctx.fillRect(0, 0, W, H);
      ctx.fillStyle = '#fff';
      ctx.font = '800 ' + Math.min(48, W * 0.08) + 'px "Segoe UI", system-ui, sans-serif';
      ctx.fillText('PAUSED', W / 2, H * 0.42);
      ctx.font = '500 ' + Math.max(13, W * 0.02) + 'px "Segoe UI", system-ui, sans-serif';
      ctx.fillStyle = 'rgba(255,255,255,0.7)';
      ctx.fillText('P / ESC / double-tap to resume · M mutes', W / 2, H * 0.52);
    } else if (S.mode === 'levelclear') {
      ctx.fillStyle = 'rgba(5,5,15,0.6)';
      ctx.fillRect(0, 0, W, H);
      ctx.fillStyle = '#4dffa6';
      ctx.font = '800 ' + Math.min(48, W * 0.07) + 'px "Segoe UI", system-ui, sans-serif';
      ctx.fillText('LEVEL CLEAR!', W / 2, H * 0.4);
      ctx.fillStyle = 'rgba(255,255,255,0.8)';
      ctx.font = '500 ' + Math.max(14, W * 0.024) + 'px "Segoe UI", system-ui, sans-serif';
      ctx.fillText('Bonus +' + (500 + S.lives * 100), W / 2, H * 0.5);
    } else if (S.mode === 'gameover') {
      ctx.fillStyle = 'rgba(5,5,15,0.8)';
      ctx.fillRect(0, 0, W, H);
      ctx.fillStyle = '#ff5d8f';
      ctx.font = '800 ' + Math.min(56, W * 0.08) + 'px "Segoe UI", system-ui, sans-serif';
      ctx.fillText('GAME OVER', W / 2, H * 0.36);
      ctx.fillStyle = '#fff';
      ctx.font = '600 ' + Math.max(16, W * 0.028) + 'px "Segoe UI", system-ui, sans-serif';
      ctx.fillText('SCORE ' + S.score, W / 2, H * 0.48);
      ctx.fillStyle = '#ffd24d';
      ctx.fillText('BEST ' + S.best, W / 2, H * 0.54);
      ctx.fillStyle = 'rgba(255,255,255,0.8)';
      ctx.font = '700 ' + Math.max(15, W * 0.026) + 'px "Segoe UI", system-ui, sans-serif';
      ctx.fillText('TAP / CLICK / SPACE TO RETRY', W / 2, H * 0.66);
    } else if (S.mode === 'playing') {
      // level intro hint
      if (S.levelT < 2.6) {
        var a = Math.min(1, (2.6 - S.levelT) / 0.6);
        ctx.globalAlpha = a;
        var lv = BO.LEVELS[S.level % BO.LEVELS.length];
        ctx.fillStyle = 'rgba(255,255,255,0.9)';
        ctx.font = '700 ' + Math.min(34, W * 0.045) + 'px "Segoe UI", system-ui, sans-serif';
        ctx.fillText(lv.name, W / 2, H * 0.2);
        ctx.font = '500 ' + Math.max(12, W * 0.02) + 'px "Segoe UI", system-ui, sans-serif';
        ctx.fillStyle = 'rgba(255,255,255,0.6)';
        ctx.fillText(lv.hint, W / 2, H * 0.2 + 34);
        ctx.globalAlpha = 1;
      }
    }
    ctx.restore();
  }

  // ================= LOOP (fixed timestep, accumulator) =================
  var STEP = 1 / CFG.physicsHz;
  var acc = 0, last = performance.now();
  function frame(now) {
    requestAnimationFrame(frame);
    var raw = Math.min(0.1, (now - last) / 1000);
    last = now;

    if (input.takeMute()) audio.toggleMute();
    if (input.takeStickToggle()) {
      input.thumbstick.enabled = !input.thumbstick.enabled;
      fx.popup(CFG.fieldW / 2, CFG.fieldH / 2,
        input.thumbstick.enabled ? 'THUMBSTICK ON' : 'THUMBSTICK OFF', '#8f9bff', 18);
    }

    var launch = input.takeLaunch();
    var pause = input.takePause();

    if (S.mode === 'title' || S.mode === 'gameover') {
      if (launch) {
        S.mode = 'playing';
        S.score = 0; S.lives = CFG.lives; S.level = 0;
        S.combo = 0; S.overdrive = 0; S.overdriveMult = 1; S.overdriveMeter = 0;
        S.activePowers = {};
        S.drops.length = 0;
        loadLevel(0);
        audio.resume();
      }
    } else if (S.mode === 'playing') {
      if (pause) { S.mode = 'paused'; audio.stopArp(); }
      else if (launch) {
        if (S.balls.some(function (b) { return b.stuck; })) launchBalls();
      }
    } else if (S.mode === 'paused') {
      if (pause || launch) { S.mode = 'playing'; last = now; }
    } else if (S.mode === 'levelclear') {
      S.levelT += raw;
      if (S.levelT > 2) {
        S.level++;
        S.mode = 'playing';
        loadLevel(S.level);
      }
    }

    if (S.mode === 'playing') {
      S.levelT += raw;
      // hitstop eats time before physics runs
      var scaled = raw * (S.slowT > 0 ? 0.35 : 1);
      if (fx.hitstop > 0) {
        fx.hitstop -= raw;
        acc = 0;
      } else {
        acc += scaled;
      }
      var guard = 0;
      while (acc >= STEP && guard++ < 12) {
        step(STEP);
        acc -= STEP;
        if (S.mode !== 'playing') { acc = 0; break; }
      }
    }

    fx.update(raw);
    draw();
  }
  requestAnimationFrame(frame);

  // debug/testing hook (harmless in production)
  window.__nb = { S: S, CFG: CFG, fieldTop: fieldTop };
})();
