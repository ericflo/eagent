// Breakthrough — game.js: state machine, fixed-timestep loop, HUD, collisions
window.BT = window.BT || {};
BT.game = (function () {
  var U = BT.util, A = BT.audio, F = BT.fx, E = BT.entities, L = BT.level, I = BT.input;

  var VH = 900; // virtual height
  var VW = 600; // virtual width (desktop letterboxed)
  var canvas, ctx, dpr = 1;
  var view = { scale: 1, offX: 0, offY: 0, W: 0, H: 0 };

  var S = {
    mode: 'menu', paused: false, prevMode: 'playing',
    score: 0, shownScore: 0, best: U.store.get('best', 0), newBest: false,
    lives: 3, level: 0,
    bricks: [], balls: [], powerups: [], lasers: [],
    rushTime: 0, onTop: false, rushMult: 1, combo: 0, totalMult: 1,
    breakthroughDone: false, tier: 0,
    newSpecials: {},
    effects: { wide: 0, laser: 0, sticky: 0, slowmo: 0 },
    time: 0, levelMeta: null, levelBuild: 0, clearTimer: 0, laserCooldown: 0,
    hintDismissed: U.store.get('hintShown', false),
    buttons: [], menuT: 0
  };

  var TIER_NAMES = ['UNSTOPPABLE', 'OBLITERATION', 'GODMODE'];
  var GLOWS = ['#ffffff', '#ffd23f', '#ff6ce7', '#4dd0e1'];
  var BASE_SPEED = 620;

  var paddle = { x: 300, py: 800, y: 800, w: 110, baseW: 110, h: 16, vx: 0, vy: 0 };
  var targetPX = 300, targetPY = 800;
  var pointerActive = false;

  function clientToVirtual(cx, cy) {
    var rect = canvas.getBoundingClientRect();
    return { x: (cx - rect.left - view.offX) / view.scale, y: (cy - rect.top - view.offY) / view.scale };
  }

  function tierColor() { return GLOWS[Math.min(S.tier, GLOWS.length - 1)]; }

  function resize() {
    dpr = Math.min(window.devicePixelRatio || 1, 2);
    var cw = window.innerWidth, ch = window.innerHeight;
    canvas.width = Math.floor(cw * dpr);
    canvas.height = Math.floor(ch * dpr);
    canvas.style.width = cw + 'px';
    canvas.style.height = ch + 'px';
    var mobile = cw / ch < 0.85;
    if (mobile) {
      VW = Math.round(cw / (ch / VH));
      view.scale = ch / VH;
      view.offX = 0; view.offY = 0;
    } else {
      VW = 600;
      var w = Math.min(cw, Math.min(ch * 0.75, (ch * 600) / 900));
      view.scale = w / VW;
      view.offX = (cw - w) / 2;
      view.offY = 0;
    }
    view.W = cw; view.H = ch;
    F.initBackground(view.W, view.H);
  }

  function paddleBand() { return { min: VH * 0.75, max: VH - 30 }; }

  // ================= LEVEL / FLOW =================
  function newGame() {
    S.score = 0; S.shownScore = 0; S.lives = 3; S.level = 0; S.newBest = false;
    S.effects = { wide: 0, laser: 0, sticky: 0, slowmo: 0 };
    F.clear();
    startLevel(0);
  }

  function startLevel(n) {
    S.level = n;
    var gen = L.generate(n, VW);
    S.bricks = gen.bricks; S.levelMeta = gen;
    S.balls = []; S.powerups = []; S.lasers = [];
    S.rushTime = 0; S.onTop = false; S.rushMult = 1; S.rushTime = 0; S.combo = 0; S.totalMult = 1;
    S.breakthroughDone = false; S.tier = 0;
    S.levelBuild = 0; S.clearTimer = 0;
    paddle.w = paddle.baseW;
    paddle.x = paddle.py = VW / 2;
    targetPX = paddle.x; targetPY = paddleBand().max;
    if (gen.newSpecial && !S.newSpecials[gen.newSpecial]) {
      S.newSpecials[gen.newSpecial] = true;
      F.bannerText('NEW BRICK: ' + gen.newSpecial.toUpperCase(), '#d3a9ff');
    }
    serve();
  }

  function serve() {
    S.mode = 'serve';
    var b = new E.Ball(paddle.x, paddle.y - 28, 0, 0);
    b.stuck = true; b.stuckOffX = 0;
    S.balls = [b];
  }

  function launch() {
    var launched = false;
    for (var i = 0; i < S.balls.length; i++) {
      var b = S.balls[i];
      if (b.stuck) {
        b.stuck = false;
        var ang = -Math.PI / 2 + U.clamp(paddle.vx * 0.0006, -0.5, 0.5);
        var sp = baseSpeed();
        b.vx = Math.cos(ang) * sp; b.vy = Math.sin(ang) * sp;
        launched = true;
      }
    }
    if (launched) { A.SFX.paddle(); S.mode = 'playing'; }
  }

  function baseSpeed() { return BASE_SPEED * (1 + S.level * 0.03); }

  // ---------- rush ----------
  function topBrickY() {
    var top = Infinity;
    for (var i = 0; i < S.bricks.length; i++)
      if (S.bricks[i].alive && S.bricks[i].y < top) top = S.bricks[i].y;
    return top;
  }

  function updateRush(dt) {
    var topY = topBrickY();
    var anyOnTop = false;
    for (var i = 0; i < S.balls.length; i++)
      if (S.balls[i].y - 8 < topY) { anyOnTop = true; break; }
    if (anyOnTop) {
      if (!S.onTop) {
        S.onTop = true;
        if (!S.breakthroughDone) {
          S.breakthroughDone = true;
          F.bannerText('BREAKTHROUGH!', '#ffd23f');
          F.addFlash('#ffffff', 0.5, 3);
          F.addShake(14); F.addHitStop(0.06);
          A.SFX.breakthrough();
        }
      }
      S.rushTime += dt;
      S.rushMult = Math.min(Math.pow(2, S.rushTime / 2.5), 32);
    } else {
      if (S.onTop) { S.onTop = false; S.decayFrom = S.rushMult; S.decayT = 0; }
      // fade multiplier to 1 over ~4 s
      S.decayT = (S.decayT || 0) + dt;
      var t = Math.min(1, S.decayT / 4);
      var from = S.decayFrom || 1;
      S.rushMult = U.lerp(from, 1, t * t);
      if (S.rushMult <= 1.01) { S.rushMult = 1; S.rushTime = 0; S.tier = 0; S.decayT = 0; }
      else S.rushTime = 2.5 * Math.log2(Math.max(1.0001, S.rushMult));
    }
    var total = S.rushMult * (1 + S.combo * 0.5);
    S.totalMult = Math.min(total, 99);
    var newTier = S.onTop ? Math.min(3, Math.floor(S.rushTime / 2.5)) : S.tier;
    if (newTier > S.tier) {
      S.tier = newTier;
      F.bannerText(TIER_NAMES[S.tier - 1], tierColor());
      A.SFX.tierUp(); F.addShake(8); F.addFlash(tierColor(), 0.15, 4);
    }
    if (!S.onTop && S.rushMult <= 1.01) S.tier = 0;
    A.setRushTier(S.tier);
  }

  // ---------- collisions ----------
  function rectCircleOverlap(b, ball) {
    var cx = U.clamp(ball.x, b.x, b.x + b.w), cy = U.clamp(ball.y, b.y, b.y + b.h);
    var dx = ball.x - cx, dy = ball.y - cy;
    return dx * dx + dy * dy <= 64; // r=8
  }

  function reflectBallOffBrick(b, ball) {
    var bcx = b.x + b.w / 2, bcy = b.y + b.h / 2;
    var ox = (b.w / 2 + 8) - Math.abs(ball.x - bcx);
    var oy = (b.h / 2 + 8) - Math.abs(ball.y - bcy);
    var nx = 0, ny = 0;
    if (ox < oy) { nx = ball.x < bcx ? -1 : 1; ball.x += nx * ox; }
    else { ny = ball.y < bcy ? -1 : 1; ball.y += ny * oy; }
    var dot = ball.vx * nx + ball.vy * ny;
    if (dot < 0) { ball.vx -= 2 * dot * nx; ball.vy -= 2 * dot * ny; }
    return { nx: nx, ny: ny };
  }

  function hitBrick(ball, b, faceN) {
    var speed = Math.hypot(ball.vx, ball.vy);
    var base = baseSpeed();
    if (b.type === 'armored') {
      if (ball.fire > 0 || speed >= base * 1.45) return destroyBrick(b);
      A.SFX.clang();
      F.spawnSparks(ball.x, ball.y, '#ffe14d', 12, 300);
      F.addShake(3);
      return false;
    }
    if (b.type === 'prism') {
      var vlen = speed || 1;
      var cos = Math.abs((ball.vx * faceN.nx + ball.vy * faceN.ny) / vlen);
      if (ball.fire > 0 || cos >= Math.cos(Math.PI / 9)) return destroyBrick(b);
      A.SFX.prismTing();
      F.spawnSparks(ball.x, ball.y, '#d3a9ff', 8, 260);
      return false;
    }
    return destroyBrick(b);
  }

  function destroyBrick(b) {
    if (!b.alive) return false;
    b.alive = false;
    var base = 100 + (S.levelMeta.rows - b.row) * 10;
    var pts = Math.round(base * S.totalMult);
    S.score += pts; S.combo++;
    var cx = b.x + b.w / 2, cy = b.y + b.h / 2;
    F.spawnShards(cx, cy, b.color, 8, 280);
    F.spawnSparks(cx, cy, '#fff', 6, 320);
    F.addShake(3 + Math.min(9, S.totalMult * 0.15));
    A.SFX.brick(S.combo);
    if (b.type === 'explosive') explode(b);
    if (b.type === 'standard' && Math.random() < 0.16) {
      S.powerups.push(E.makePowerUp(cx, cy));
    }
    F.addFloater(cx, b.y, '+' + U.fmt(pts), tierColor(), 16 + Math.min(12, S.totalMult));
    if (S.combo >= 3 && S.combo % 3 === 0)
      F.addFloater(cx, b.y - 22, 'COMBO ×' + S.combo, '#ffd23f', 14);
    return true;
  }

  var chainQueue = [];
  function explode(b) {
    A.SFX.explode();
    F.addFlash('#ff7b1c', 0.25, 5);
    F.addShake(14); F.addHitStop(0.05);
    F.spawnRing(b.x + b.w / 2, b.y + b.h / 2, '#ff9e3d', 160, 6);
    F.spawnEmbers(b.x + b.w / 2, b.y + b.h / 2, '#ff5c2b', 20);
    var R = 1.6 * L.BRICK_W;
    var cx = b.x + b.w / 2, cy = b.y + b.h / 2, chain = 0;
    for (var i = 0; i < S.bricks.length; i++) {
      var o = S.bricks[i];
      if (!o.alive || o === b) continue;
      var dx = o.x + o.w / 2 - cx, dy = o.y + o.h / 2 - cy;
      if (dx * dx + dy * dy <= R * R) {
        o.alive = false;
        S.score += Math.round(80 * S.totalMult);
        chain++;
        F.spawnShards(o.x + o.w / 2, o.y + o.h / 2, o.color, 6, 240);
        if (o.type === 'explosive') chainQueue.push(o);
      }
    }
    for (var c = 0; c < chainQueue.length; c++) {
      A.SFX.boom(c);
      var chainBrick = chainQueue[c];
      chainQueue = [];
      explode(chainBrick);
    }
    chainQueue.length = 0;
    if (chain > 0) F.addFloater(cx, cy - 20, 'CHAIN ×' + chain, '#ff9e3d', 16);
  }

  // ---------- per-substep physics ----------
  function physStep(dt) {
    paddle.x = U.clamp(paddle.x, paddle.w / 2, VW - paddle.w / 2);
    var pb = paddleBand();
    paddle.py = U.clamp(paddle.py, pb.min, pb.max);
    paddle.y = paddle.py;

    for (var bi = S.balls.length - 1; bi >= 0; bi--) {
      var ball = S.balls[bi];
      if (!isFinite(ball.x) || !isFinite(ball.y) || !isFinite(ball.vx) || !isFinite(ball.vy)) {
        ball.x = paddle.x; ball.y = paddle.y - 9; ball.vx = 0; ball.vy = 0;
        ball.stuck = true; ball.stuckOffX = 0;
        continue;
      }
      if (ball.stuck) {
        ball.x = paddle.x + ball.stuckOffX;
        ball.y = paddle.y - 9;
        ball.trail.length = 0;
        continue;
      }
      var steps = Math.max(1, Math.ceil(Math.hypot(ball.vx, ball.vy) * dt / 6));
      var lost = false;
      for (var s2 = 0; s2 < steps && !lost; s2++) {
        var sdt = dt / steps;
        ball.x += ball.vx * sdt;
        ball.y += ball.vy * sdt;
        if (ball.x < 8) { ball.x = 8; ball.vx = Math.abs(ball.vx); A.SFX.wall(); }
        if (ball.x > VW - 8) { ball.x = VW - 8; ball.vx = -Math.abs(ball.vx); A.SFX.wall(); }
        if (ball.y < 8) { ball.y = 8; ball.vy = Math.abs(ball.vy); A.SFX.wall(); }
        collideBricks(ball);
        if (ball.y > VH + 40) lost = true;
      }
      ball.trail.push({ x: ball.x, y: ball.y });
      if (ball.trail.length > 14) ball.trail.shift();
      if (lost) {
        S.balls.splice(bi, 1);
        if (S.balls.length === 0) loseLife();
        continue;
      }
      if (ball.vy > 0 && ball.y + 8 >= paddle.y - paddle.h / 2 && ball.y - 8 <= paddle.y + paddle.h / 2 &&
          ball.x >= paddle.x - paddle.w / 2 - 8 && ball.x <= paddle.x + paddle.w / 2 + 8) {
        paddleBounce(ball);
      }
    }

    var base = baseSpeed();
    for (var k = 0; k < S.balls.length; k++) {
      var b2 = S.balls[k];
      if (b2.fire > 0) b2.fire -= dt;
      if (b2.stuck) continue;
      var sp = b2.speed();
      if (sp < base * 0.85) b2.setSpeed(base * 0.85);
      if (sp > base * 1.9) b2.setSpeed(base * 1.9);
    }

    for (var p = S.powerups.length - 1; p >= 0; p--) {
      var pu = S.powerups[p];
      pu.y += pu.vy * dt; pu.t += dt;
      if (pu.y > VH + 30) { S.powerups.splice(p, 1); continue; }
      if (Math.abs(pu.y - paddle.y) < 22 && Math.abs(pu.x - paddle.x) < paddle.w / 2 + 16) {
        pickup(pu); S.powerups.splice(p, 1);
      }
    }

    for (var lz = S.lasers.length - 1; lz >= 0; lz--) {
      var lb = S.lasers[lz];
      lb.y += lb.vy * dt;
      if (lb.y < -20) { S.lasers.splice(lz, 1); continue; }
      for (var li = 0; li < S.bricks.length; li++) {
        var bk = S.bricks[li];
        if (!bk.alive) continue;
        if (lb.x >= bk.x && lb.x <= bk.x + bk.w && lb.y <= bk.y + bk.h && lb.y >= bk.y) {
          if (bk.type === 'armored') { A.SFX.clang(); F.spawnSparks(lb.x, bk.y + bk.h, '#ffe14d', 6, 200); }
          else if (bk.type === 'shielded') { A.SFX.shield(); F.spawnSparks(lb.x, bk.y + bk.h, '#7fe9ff', 8, 200); }
          else if (E.brickSolidness(bk, S.time) > 0) destroyBrick(bk);
          S.lasers.splice(lz, 1);
          break;
        }
      }
    }

    var aliveCount = 0;
    for (var aa = 0; aa < S.bricks.length; aa++) if (S.bricks[aa].alive) aliveCount++;
    if (aliveCount === 0 && S.mode === 'playing') levelClear();
  }

  function collideBricks(ball) {
    for (var i = 0; i < S.bricks.length; i++) {
      var b = S.bricks[i];
      if (!b.alive || !rectCircleOverlap(b, ball)) continue;
      if (E.brickSolidness(b, S.time) === 0) continue; // phase ghost
      if (b.type === 'shielded' && ball.fire <= 0 && ball.y > b.y + b.h - 2 && ball.vy < 0) {
        // shield on bottom face blocks ball coming from below
        A.SFX.shield();
        ball.y = b.y + b.h + 9;
        ball.vy = Math.abs(ball.vy);
        F.spawnSparks(ball.x, b.y + b.h, '#7fe9ff', 10, 260);
        F.spawnRing(ball.x, b.y + b.h, '#7fe9ff', 50, 3);
        F.addShake(3);
        return;
      }
      var faceN = reflectBallOffBrick(b, ball);
      hitBrick(ball, b, faceN);
      return; // fireball pierces: reflect once, then continue next substep
    }
  }

  function paddleBounce(ball) {
    var off = U.clamp((ball.x - paddle.x) / (paddle.w / 2), -1, 1);
    var base = baseSpeed();
    var upVel = -paddle.vy;
    var smash = upVel > 60;
    var speed = base, ang;
    if (smash) {
      speed = base * (1 + Math.min(0.28, upVel / 1400));
      ang = -Math.PI / 2 + off * 0.35;
      A.SFX.smash();
      F.spawnRing(ball.x, paddle.y, '#ffffff', 70, 4);
      F.spawnSparks(ball.x, paddle.y, '#fff', 14, 340);
      F.addFlash('#ffffff', 0.12, 8);
      F.addShake(5);
      S.smashFlash = 0.25;
    } else {
      ang = -Math.PI / 2 + off * 1.05;
      A.SFX.paddle();
      F.spawnRing(ball.x, paddle.y, tierColor(), 40, 3);
      F.addShake(1.2);
    }
    ball.vx = Math.cos(ang) * speed + paddle.vx * 0.18;
    ball.vy = Math.sin(ang) * speed;
    if (ball.vy > -speed * 0.35) ball.vy = -speed * 0.35;
    ball.y = paddle.y - 9;
    S.combo = 0;
    if (S.effects.sticky > 0 && !smash) {
      ball.stuck = true;
      ball.stuckOffX = U.clamp(ball.x - paddle.x, -paddle.w / 2 + 8, paddle.w / 2 - 8);
      ball.vx = 0; ball.vy = 0;
      S.mode = 'playing';
    }
  }

  function pickup(pu) {
    A.SFX.powerup();
    var d = E.PU_DEFS[pu.kind];
    F.addFloater(pu.x, pu.y - 24, d.label, d.color, 18);
    F.spawnRing(pu.x, pu.y, d.color, 60, 4);
    var i, b, ang, sp, nb;
    switch (pu.kind) {
      case 'M':
        var src = S.balls.slice();
        for (i = 0; i < src.length; i++) {
          b = src[i];
          for (var j = 0; j < 2 && S.balls.length < 9; j++) {
            ang = Math.atan2(b.vy, b.vx) + (j === 0 ? 0.5 : -0.5);
            sp = b.speed() || baseSpeed();
            nb = new E.Ball(b.x, b.y, Math.cos(ang) * sp, Math.sin(ang) * sp);
            S.balls.push(nb);
          }
          if (S.balls.length >= 9) break;
        }
        break;
      case 'F': for (i = 0; i < S.balls.length; i++) S.balls[i].fire = 10; A.SFX.fire(); break;
      case 'W': S.effects.wide = 12; break;
      case 'L': S.effects.laser = 8; break;
      case 'S': S.effects.sticky = 10; break;
      case 'T': S.effects.slowmo = 6; A.setMuffle(true); break;
      case 'H':
        if (S.lives < 5) { S.lives++; A.SFX.life(); } else S.score += 500;
        break;
    }
  }

  function loseLife() {
    S.lives--;
    S.combo = 0; S.rushMult = 1; S.rushTime = 0; S.tier = 0; S.onTop = false;
    F.addFlash('#ff2b4d', 0.35, 3);
    F.addShake(12);
    A.SFX.lose();
    A.setMuffle(false);
    if (S.lives <= 0) gameOver();
    else serve();
  }

  function levelClear() {
    S.mode = 'levelclear';
    S.clearTimer = 0;
    var bonus = 500 + S.balls.length * 250 + S.lives * 100;
    S.score += bonus;
    F.bannerText('LEVEL ' + (S.level + 1) + ' CLEAR!  +' + U.fmt(bonus), '#5ce65c');
    F.addFlash('#5ce65c', 0.25, 3);
    A.SFX.clear();
    A.setMuffle(false);
  }

  function gameOver() {
    S.mode = 'gameover';
    if (S.score > S.best) { S.best = S.score; S.newBest = true; U.store.set('best', S.best); }
    A.SFX.gameover();
  }

  // ================= INPUT =================
  function setupInput() {
    I.attach(canvas, clientToVirtual);
    I.on(function (evt) {
      if (evt.type === 'down' && evt.clientX != null) {
        pointerActive = true;
        if (evt.pointerType === 'mouse' || evt.pointerType === 'pen') {
          var p = clientToVirtual(evt.clientX, evt.clientY);
          if (isFinite(p.x) && isFinite(p.y)) { targetPX = p.x; targetPY = U.clamp(p.y, 0, VH); }
        }
      }
      if (evt.type === 'up' && evt.tap) {
        handleTap();
      }
      if (evt.type === 'move' && evt.clientX != null && I.state.pointerType !== 'touch' && I.state.down) {
        var p2 = clientToVirtual(evt.clientX, evt.clientY);
        if (isFinite(p2.x) && isFinite(p2.y)) { targetPX = p2.x; targetPY = U.clamp(p2.y, 0, VH); }
      }
      if (evt.type === 'keydown') {
        if (evt.key === 'p') togglePause();
        if (evt.key === 'm') { A.toggleMute(); }
        if (evt.key === 'enter' || evt.key === ' ') {
          if (S.mode === 'menu') beginGame();
          else if (S.mode === 'gameover') newGame();
          else if (S.mode === 'serve') launch();
        }
      }
    });
    window.addEventListener('keydown', function (e) {
      if (e.key === ' ') { I.state.firePressed = true; }
    });
    document.addEventListener('visibilitychange', function () {
      if (document.hidden && S.mode === 'playing') { S.paused = true; }
    });
  }

  function beginGame() {
    A.unlock();
    newGame();
  }

  function handleTap() {
    if (!S.hintDismissed && I.state.isTouch && S.mode === 'menu') { return; }
    if (S.mode === 'menu') { beginGame(); return; }
    if (S.mode === 'serve') { launch(); return; }
    if (S.mode === 'playing') { fireLaser(); if (anyStuck()) launch(); return; }
    if (S.mode === 'gameover') { var g = hitButton(VH / 2); if (!g) newGame(); return; }
    if (S.mode === 'levelclear') return;
    if (S.paused) { S.paused = false; return; }
  }

  function anyStuck() {
    for (var i = 0; i < S.balls.length; i++) if (S.balls[i].stuck) return true;
    return false;
  }

  function fireLaser() {
    if (S.effects.laser <= 0 || S.laserCooldown > 0) return;
    S.laserCooldown = 0.2;
    S.lasers.push(E.makeLaser(paddle.x - paddle.w / 2 + 8, paddle.y - 10));
    S.lasers.push(E.makeLaser(paddle.x + paddle.w / 2 - 8, paddle.y - 10));
    A.SFX.laser();
    F.spawnSparks(paddle.x - paddle.w / 2 + 8, paddle.y - 12, '#ffd23f', 3, 120);
    F.spawnSparks(paddle.x + paddle.w / 2 - 8, paddle.y - 12, '#ffd23f', 3, 120);
  }

  function togglePause() {
    if (S.mode === 'menu' || S.mode === 'gameover') return;
    S.paused = !S.paused;
    A.SFX.uiClick();
  }

  function updatePaddle(dt) {
    var pb = paddleBand();
    var drag = I.consumeDelta();
    var k = I.state;
    var moved = false;

    if (Math.abs(drag.dx) > 0.5 || Math.abs(drag.dy) > 0.5) {
      targetPX += drag.dx;
      targetPY += drag.dy;
      moved = true;
    }
    // keyboard
    var kx = (k.kbRight ? 1 : 0) - (k.kbLeft ? 1 : 0);
    var ky = (k.kbDown ? 1 : 0) - (k.kbUp ? 1 : 0);
    if (kx || ky) {
      targetPX += kx * 900 * dt;
      targetPY += ky * 620 * dt;
      moved = true;
    }
    // mouse absolute (when not touch and pointer moved recently)
    if (!I.state.isTouch && pointerActive && !kx && !ky && !I.state.down &&
        isFinite(lastMouseX) && isFinite(lastMouseY)) {
      var v = clientToVirtual(lastMouseX, lastMouseY);
      if (isFinite(v.x) && isFinite(v.y)) {
        targetPX = v.x; targetPY = U.clamp(v.y, pb.min, pb.max);
      }
    }

    targetPX = U.clamp(targetPX, paddle.w / 2, VW - paddle.w / 2);
    targetPY = U.clamp(targetPY, pb.min, pb.max);

    var smooth = 1 - Math.exp(-22 * dt);
    prevPX = paddle.x; prevPY = paddle.py;
    paddle.x += (targetPX - paddle.x) * smooth;
    paddle.py += (targetPY - paddle.py) * smooth;
    paddle.vx = (paddle.x - prevPX) / dt;
    paddle.vy = (paddle.py - prevPY) / dt;
  }
  var lastMouseX = 0, lastMouseY = 0;
  window.addEventListener('mousemove', function (e) { lastMouseX = e.clientX; lastMouseY = e.clientY; });

  function updateEffects(dt) {
    var fx2 = S.effects;
    if (fx2.wide > 0) fx2.wide -= dt;
    if (fx2.laser > 0) fx2.laser -= dt;
    if (fx2.sticky > 0) fx2.sticky -= dt;
    if (fx2.slowmo > 0) { fx2.slowmo -= dt; if (fx2.slowmo <= 0) A.setMuffle(false); }
    S.laserCooldown -= dt;
    var targetW = paddle.baseW * (fx2.wide > 0 ? 1.55 : 1);
    paddle.w += (targetW - paddle.w) * Math.min(1, dt * 8);
    if (S.smashFlash > 0) S.smashFlash -= dt;
    // launch with space
    if (I.state.launchPressed) { I.state.launchPressed = false; if (S.mode === 'serve') launch(); else fireLaser(); }
  }

  // ================= UPDATE =================
  function update(dt) {
    S.time += dt;
    S.menuT += dt;
    F.update(dt, VW, VH, S.rushMult / 32, S.tier);
    A.updateMusic(dt);

    if (S.paused) return;

    if (S.mode === 'menu') {
      // demo ball bouncing behind title
      if (!S.menuBall) S.menuBall = new E.Ball(VW / 2, 300, 260, 300);
      var mb = S.menuBall;
      mb.x += mb.vx * dt; mb.y += mb.vy * dt;
      if (mb.x < 8 || mb.x > VW - 8) mb.vx *= -1;
      if (mb.y < 8 || mb.y > VH * 0.55) mb.vy *= -1;
      mb.trail.push({ x: mb.x, y: mb.y });
      if (mb.trail.length > 12) mb.trail.shift();
      return;
    }
    if (S.mode === 'levelclear') {
      S.clearTimer += dt;
      if (S.clearTimer > 2.2) startLevel(S.level + 1);
      return;
    }
    if (S.mode === 'gameover' || S.mode === 'serve' || S.mode === 'playing') {
      S.levelBuild = Math.min(1, S.levelBuild + dt * 1.4);
      updatePaddle(dt);
      updateEffects(dt);
      updateRush(dt);
      if (S.mode !== 'serve') physStep(dt * (S.effects.slowmo > 0 ? 0.55 : 1));
    }
    S.shownScore += (S.score - S.shownScore) * Math.min(1, dt * 8);
    if (S.score - S.shownScore < 1) S.shownScore = S.score;
  }

  // ================= RENDER =================
  function render() {
    var c = ctx;
    c.setTransform(dpr, 0, 0, dpr, 0, 0);
    c.clearRect(0, 0, view.W, view.H);
    // background full screen
    F.drawBackground(c, view.W, view.H, S.rushMult / 32, S.tier);

    c.save();
    c.translate(view.offX + F.shakeX, view.offY + F.shakeY);
    c.scale(view.scale, view.scale);

    // clip to field
    c.beginPath();
    c.rect(0, 0, VW, VH);
    c.clip();

    drawField();
    F.drawParticles(c);
    F.drawFloaters(c, VW, VH);
    c.restore();

    F.drawFlashes(c, view.W, view.H);
    S.buttons = [];
    drawHUD(c);
    drawModeOverlays(c);
  }

  function drawField() {
    var c = ctx;
    // bricks
    for (var i = 0; i < S.bricks.length; i++) {
      var b = S.bricks[i];
      if (!b.alive) continue;
      var drop = S.levelBuild < 1.05 ? Math.max(0, (1 - S.levelBuild * 0.95 - b.dropIn) * 4) : 0;
      var dy2 = -drop * drop * 500;
      var saveY = b.y; b.y += dy2;
      c.globalAlpha = U.clamp(1 - drop, 0.15, 1);
      E.drawBrick(c, b, S.time, S.tier > 0 ? tierColor() : null);
      c.globalAlpha = 1;
      b.y = saveY;
    }
    // powerups
    for (var p = 0; p < S.powerups.length; p++) E.drawPowerUp(c, S.powerups[p], S.time);
    // lasers
    c.fillStyle = '#ffd23f';
    for (var lz = 0; lz < S.lasers.length; lz++) {
      var lb = S.lasers[lz];
      c.fillRect(lb.x - 2, lb.y - 10, 4, 16);
    }
    // balls
    for (var bi = 0; bi < S.balls.length; bi++) {
      var ball = S.balls[bi];
      var glow = ball.fire > 0 ? '#ff5c2b' : tierColor();
      c.save();
      c.globalCompositeOperation = 'lighter';
      for (var t = 0; t < ball.trail.length; t++) {
        var tr = ball.trail[t];
        var a = (t / ball.trail.length) * (0.25 + S.tier * 0.1);
        c.globalAlpha = a;
        c.fillStyle = glow;
        c.beginPath(); c.arc(tr.x, tr.y, 8 * (0.4 + 0.6 * t / ball.trail.length), 0, 6.283); c.fill();
      }
      c.globalAlpha = 1;
      c.globalCompositeOperation = 'source-over';
      c.shadowColor = glow; c.shadowBlur = 18;
      c.fillStyle = ball.fire > 0 ? '#ff7b3d' : '#ffffff';
      c.beginPath(); c.arc(ball.x, ball.y, 8, 0, 6.283); c.fill();
      c.shadowBlur = 0;
    }
    // paddle
    drawPaddle(c);
  }

  function drawPaddle(c) {
    var px = paddle.x - paddle.w / 2, py = paddle.y - paddle.h / 2;
    var glow = S.smashFlash > 0 ? '#ffffff' : tierColor();
    c.save();
    c.shadowColor = glow; c.shadowBlur = 12 + S.tier * 6;
    var grad = c.createLinearGradient(px, py, px, py + paddle.h);
    grad.addColorStop(0, '#e8ecf4');
    grad.addColorStop(0.5, '#aab4c4');
    grad.addColorStop(1, '#7d8798');
    c.fillStyle = grad;
    roundRect(c, px, py, paddle.w, paddle.h, 8);
    c.fill();
    c.restore();
    if (S.effects.laser > 0) {
      c.fillStyle = '#ffd23f';
      c.fillRect(px + 4, py - 8, 7, 8);
      c.fillRect(px + paddle.w - 11, py - 8, 7, 8);
    }
    if (S.effects.sticky > 0) {
      c.strokeStyle = '#3fa7ff'; c.lineWidth = 2;
      roundRect(c, px + 2, py + 2, paddle.w - 4, paddle.h - 4, 6);
      c.stroke();
    }
  }

  function roundRect(c, x, y, w, h, r) {
    c.beginPath();
    c.moveTo(x + r, y);
    c.arcTo(x + w, y, x + w, y + h, r);
    c.arcTo(x + w, y + h, x, y + h, r);
    c.arcTo(x, y + h, x, y, r);
    c.arcTo(x, y, x + w, y, r);
    c.closePath();
  }

  // ================= HUD =================
  function drawHUD() {
    var c = ctx;
    var padTop = 10;
    c.textBaseline = 'middle';
    // score
    c.font = '800 24px system-ui, sans-serif';
    c.textAlign = 'left';
    c.fillStyle = '#fff';
    c.shadowColor = tierColor(); c.shadowBlur = S.tier * 6;
    c.fillText(U.fmt(S.shownScore), 14 + view.offX * 0, padTop + 14);
    c.shadowBlur = 0;
    // best
    c.font = '600 13px system-ui, sans-serif';
    c.fillStyle = 'rgba(255,255,255,0.55)';
    c.fillText('BEST ' + U.fmt(S.best), 14, padTop + 38);
    // level
    c.textAlign = 'right';
    c.fillText('LVL ' + (S.level + 1), VW - 60, padTop + 38);
    // lives
    for (var i = 0; i < S.lives; i++) {
      c.fillStyle = '#fff';
      c.beginPath();
      c.arc(VW - 14 - i * 20, padTop + 14, 7, 0, 6.283);
      c.fill();
    }
    // multiplier badge
    if (S.totalMult > 1.05) {
      var pulse2 = 1 + 0.08 * Math.sin(S.time * 10) * Math.min(1, S.totalMult / 8);
      c.save();
      c.translate(VW / 2, padTop + 20);
      c.scale(pulse2 * (1 + S.totalMult / 99), pulse2 * (1 + S.totalMult / 99));
      c.font = '900 20px system-ui, sans-serif';
      c.textAlign = 'center';
      c.fillStyle = tierColor();
      c.shadowColor = tierColor(); c.shadowBlur = 16;
      c.fillText('×' + (Math.round(S.totalMult * 10) / 10), 0, 0);
      c.restore();
      c.shadowBlur = 0;
      // rush meter
      var meterW = 120, frac = Math.min(1, (S.rushMult - 1) / 31);
      c.fillStyle = 'rgba(255,255,255,0.15)';
      c.fillRect(VW / 2 - meterW / 2, padTop + 40, meterW, 6);
      c.fillStyle = tierColor();
      c.fillRect(VW / 2 - meterW / 2, padTop + 40, meterW * frac, 6);
    }
    // active effect chips
    var chips = [];
    if (S.effects.wide > 0) chips.push(['W', S.effects.wide / 12, '#5ce65c']);
    if (S.effects.laser > 0) chips.push(['L', S.effects.laser / 8, '#ffd23f']);
    if (S.effects.sticky > 0) chips.push(['S', S.effects.sticky / 10, '#3fa7ff']);
    if (S.effects.slowmo > 0) chips.push(['T', S.effects.slowmo / 6, '#4dd0e1']);
    if (S.balls.length && S.balls[0].fire > 0) chips.push(['F', S.balls[0].fire / 10, '#ff5c2b']);
    for (var ci = 0; ci < chips.length; ci++) {
      var ch = chips[ci];
      var cx = 70 + ci * 34, cy2 = VH - 24;
      c.beginPath(); c.arc(cx, cy2, 13, 0, 6.283);
      c.fillStyle = 'rgba(0,0,0,0.4)'; c.fill();
      c.beginPath();
      c.moveTo(cx, cy2);
      c.arc(cx, cy2, 13, -Math.PI / 2, -Math.PI / 2 + ch[1] * 6.283);
      c.closePath();
      c.fillStyle = ch[2]; c.globalAlpha = 0.8; c.fill();
      c.globalAlpha = 1;
      c.fillStyle = '#fff'; c.font = '700 12px system-ui, sans-serif';
      c.textAlign = 'center';
      c.fillText(ch[0], cx, cy2 + 1);
    }
    drawButtons();
  }

  function drawButtons() {
    var c = ctx;
    // mute + pause: top-right corner icons in screen space, above HUD (drawn before HUD text)
    var s = 30, gap = 38;
    var x0 = view.W - s - 10, y0 = 50;
    btn(x0, y0, s, 'pause');
    btn(x0 - gap, y0, s, 'mute');
    function btn(x, y, sz, id) {
      S.buttons.push({ x: x, y: y, w: s, h: s, id: id, screen: true });
      c.fillStyle = 'rgba(255,255,255,0.12)';
      roundRect(c, x, y, s, s, 8); c.fill();
      c.strokeStyle = 'rgba(255,255,255,0.3)'; c.lineWidth = 1; c.stroke();
      c.fillStyle = '#fff';
      if (id === 'pause') {
        c.fillRect(x + 10, y + 9, 4, s - 20);
        c.fillRect(x + 19, y + 9, 4, s - 20);
      } else {
        var m = A.isMuted();
        c.font = '700 16px system-ui, sans-serif';
        c.textAlign = 'center'; c.textBaseline = 'middle';
        c.fillText(m ? '🔇' : '🔊', x + s / 2, y + s / 2 + 1);
      }
    }
  }

  function drawModeOverlays() {
    var c = ctx;
    c.textAlign = 'center'; c.textBaseline = 'middle';
    if (S.mode === 'menu') {
      // demo ball behind title
      if (S.menuBall) {
        c.shadowColor = '#4dd0e1'; c.shadowBlur = 18;
        c.fillStyle = '#fff';
        c.beginPath(); c.arc(S.menuBall.x, S.menuBall.y, 8, 0, 6.283); c.fill();
        c.shadowBlur = 0;
      }
      c.font = '900 ' + Math.round(Math.min(VW * 0.09, 54)) + 'px system-ui, sans-serif';
      c.fillStyle = '#fff';
      c.shadowColor = '#ff6ce7'; c.shadowBlur = 26;
      c.fillText('BREAKTHROUGH', VW / 2, VH * 0.3);
      c.shadowBlur = 0;
      c.font = '600 18px system-ui, sans-serif';
      c.fillStyle = 'rgba(255,255,255,0.7)';
      c.fillText('TAP / CLICK TO START', VW / 2, VH * 0.52);
      drawHowTo(c);
      if (S.best > 0) {
        c.font = '700 16px system-ui, sans-serif';
        c.fillStyle = '#ffd23f';
        c.fillText('HIGH SCORE ' + U.fmt(S.best), VW / 2, VH * 0.62);
      }
      drawTouchHint(c);
    } else if (S.mode === 'serve') {
      c.font = '600 20px system-ui, sans-serif';
      c.fillStyle = 'rgba(255,255,255,0.75)';
      c.fillText('TAP / CLICK / SPACE TO LAUNCH', VW / 2, VH * 0.6);
    } else if (S.paused) {
      c.fillStyle = 'rgba(0,0,0,0.6)';
      c.fillRect(0, 0, VW, VH);
      c.font = '900 40px system-ui, sans-serif';
      c.fillStyle = '#fff';
      c.fillText('PAUSED', VW / 2, VH * 0.4);
      c.font = '600 16px system-ui, sans-serif';
      c.fillStyle = 'rgba(255,255,255,0.7)';
      c.fillText('TAP TO RESUME  ·  P PAUSE  ·  M MUTE', VW / 2, VH * 0.5);
    } else if (S.mode === 'gameover') {
      c.fillStyle = 'rgba(0,0,0,0.55)';
      c.fillRect(0, 0, VW, VH);
      c.font = '900 44px system-ui, sans-serif';
      c.fillStyle = '#ff4d6d';
      c.fillText('GAME OVER', VW / 2, VH * 0.32);
      c.font = '700 26px system-ui, sans-serif';
      c.fillStyle = '#fff';
      c.fillText(U.fmt(S.score), VW / 2, VH * 0.44);
      if (S.newBest) {
        var sc = 1 + 0.1 * Math.sin(S.time * 8);
        c.save();
        c.translate(VW / 2, VH * 0.52);
        c.scale(sc, sc);
        c.font = '900 30px system-ui, sans-serif';
        c.fillStyle = '#ffd23f';
        c.shadowColor = '#ffd23f'; c.shadowBlur = 20;
        c.fillText('★ NEW BEST ★', 0, 0);
        c.restore();
        c.shadowBlur = 0;
      } else {
        c.font = '600 16px system-ui, sans-serif';
        c.fillStyle = 'rgba(255,255,255,0.6)';
        c.fillText('BEST ' + U.fmt(S.best), VW / 2, VH * 0.48);
      }
      c.font = '600 18px system-ui, sans-serif';
      c.fillStyle = 'rgba(255,255,255,0.75)';
      c.fillText('TAP TO RETRY', VW / 2, VH * 0.62);
    } else if (S.mode === 'levelclear') {
      c.font = '700 18px system-ui, sans-serif';
      c.fillStyle = 'rgba(255,255,255,0.7)';
      c.fillText('NEXT LEVEL INCOMING…', VW / 2, VH * 0.55);
    }
  }

  function drawHowTo(c) {
    c.textAlign = 'center'; c.textBaseline = 'middle';
    var y = VH * 0.66;
    var bw = VW - 60, bh = VH * 0.2;
    c.fillStyle = 'rgba(10,12,30,0.7)';
    roundRect(c, 30, y - bh / 2, bw, bh, 12); c.fill();
    c.font = '700 ' + Math.max(10, Math.round(Math.min(VW * 0.023, 14))) + 'px system-ui, sans-serif';
    c.fillStyle = '#fff';
    c.fillText('MOVE MOUSE / DRAG · PADDLE-UP SMASH = +SPEED · TAP = LAUNCH', VW / 2, y - bh / 2 + 20);
    // mini brick illustrations
    var names = ['EXPLOSIVE', 'ARMORED ⚡', 'PRISM', 'SHIELDED', 'PHASE'];
    var demo = ['explosive', 'armored', 'prism', 'shielded', 'phase'];
    var n2 = demo.length;
    var cell = Math.min(80, (VW - 40) / n2);
    var bw2 = cell;
    var x0 = VW / 2 - (bw2 * n2) / 2;
    for (var i = 0; i < n2; i++) {
      var b = { type: demo[i], x: x0 + i * bw2 + 4, y: y - 14, w: Math.max(30, bw2 - 12), h: 18, color: '#5ce65c', phaseOff: 0 };
      E.drawBrick(c, b, S.time, null);
      c.font = '600 9px system-ui, sans-serif';
      c.fillStyle = 'rgba(255,255,255,0.6)';
      c.fillText(names[i], b.x + b.w / 2, b.y + 30);
    }
    c.font = '600 12px system-ui, sans-serif';
    c.fillStyle = 'rgba(255,255,255,0.55)';
    c.fillText('GET ABOVE THE BRICKS → BREAKTHROUGH ×MULT!', VW / 2, y + bh / 2 - 14);
  }

  function drawTouchHint(c) {
    if (S.hintDismissed || !I.state.isTouch) return;
    var y = VH * 0.9;
    c.fillStyle = 'rgba(0,0,0,0.6)';
    roundRect(c, 20, y - 30, VW - 40, 60, 10); c.fill();
    c.font = '600 14px system-ui, sans-serif';
    c.fillStyle = '#fff';
    c.fillText('DRAG ANYWHERE = move paddle · QUICK TAP = launch/fire', VW / 2, y - 8);
    c.fillStyle = '#ffd23f';
    c.fillText('[ GOT IT ]', VW / 2, y + 14);
    S.buttons.push({ x: 20, y: y - 30, w: VW - 40, h: 60, id: 'gottap' });
  }

  // ================= LOOP =================
  var acc = 0, last = 0;
  var STEP = 1 / 120;

  function frame(now) {
    requestAnimationFrame(frame);
    if (!last) last = now;
    var dt = Math.min(0.1, (now - last) / 1000);
    last = now;
    if (F.hitStop > 0) {
      F.tickHitStop(dt); // hit-stop: freeze sim, decay the freeze itself
    } else {
      acc += dt;
      var n = 0;
      while (acc >= STEP && n < 12) { update(STEP); acc -= STEP; n++; }
      if (acc > STEP * 12) acc = 0;
    }
    render();
  }

  function handleButtonPress(x, y) {
    for (var i = 0; i < S.buttons.length; i++) {
      var b = S.buttons[i];
      var px = b.screen ? x : x, py = b.screen ? y : y;
      if (px >= b.x && px <= b.x + b.w && py >= b.y && py <= b.y + b.h) {
        if (b.id === 'pause') { togglePause(); return true; }
        if (b.id === 'mute') { A.toggleMute(); A.SFX.uiClick(); return true; }
        if (b.id === 'gottap') { S.hintDismissed = true; U.store.set('hintShown', true); A.SFX.uiClick(); return true; }
      }
    }
    return false;
  }

  function init() {
    canvas = document.getElementById('game');
    ctx = canvas.getContext('2d');
    resize();
    window.addEventListener('resize', function () { resize(); remapOnResize(); });
    window.addEventListener('orientationchange', function () { setTimeout(function () { resize(); remapOnResize(); }, 100); });
    var lastVW = VW;
    function remapOnResize() {
      if (VW === lastVW || S.mode === 'menu' || S.mode === 'gameover') return;
      lastVW = VW;
      var f = VW / (L.lastFieldW() || VW);
      // Rescale bricks, balls, powerups, lasers proportionally to the new virtual width.
      S.bricks.forEach(function (b) { b.x *= f; });
      S.balls.forEach(function (b) { b.x *= f; b.y *= f; });
      S.powerups.forEach(function (p) { p.x *= f; });
      S.lasers.forEach(function (l) { l.x *= f; l.y *= f; });
      paddle.x = U.clamp(paddle.x * f, paddle.w / 2, VW - paddle.w / 2);
      targetPX = paddle.x;
    }
    setupInput();
    // buttons use screen coords: intercept in down handler via screen coords
    window.addEventListener('pointerdown', function (e) {
      var cx = e.clientX, cy = e.clientY;
      // convert to screen-space button check
      for (var i = S.buttons.length; i--;) {
        var b = S.buttons[i];
        if (b.screen && cx >= b.x && cx <= b.x + b.w && cy >= b.y && cy <= b.y + b.h) {
          if (b.id === 'pause') { togglePause(); }
          if (b.id === 'mute') { A.toggleMute(); A.SFX.uiClick(); }
          return;
        }
        if (!b.screen) {
          var v = clientToVirtual(cx, cy);
          if (v.x >= b.x && v.x <= b.x + b.w && v.y >= b.y && v.y <= b.y + b.h) {
            if (b.id === 'gottap') { S.hintDismissed = true; U.store.set('hintShown', true); A.SFX.uiClick(); }
            return;
          }
        }
      }
    }, true);
    requestAnimationFrame(frame);
  }

  window.addEventListener('DOMContentLoaded', init);

  // exports for smoke testing
  return {
    S: S, paddle: paddle, update: update, render: render,
    get mode() { return S.mode; },
    newGame: newGame, launch: launch, resize: resize
  };
})();