/* OVERDRIVE BREAKOUT — main game engine (canvas, physics, bricks, powerups, juice).
   Classic script, global: window.BreakoutGame (constructor).
   Requires: BreakoutAudio, BreakoutParticles, BreakoutInput, BreakoutLevels. */
(function () {
  'use strict';
  var Audio = window.BreakoutAudio;
  var FX = window.BreakoutParticles;
  var Input = window.BreakoutInput;

  var TAU = Math.PI * 2;
  function clamp(v, a, b) { return v < a ? a : (v > b ? b : v); }
  function lerp(a, b, t) { return a + (b - a) * t; }
  function rnd(a, b) { return a + Math.random() * (b - a); }
  function $(id) { return document.getElementById(id); }

  var BALL_MIN = 320, BALL_MAX = 920;
  var VELOCITY_NEED = 560;
  var DROP_CHANCE = 0.18;
  var SAVE_KEY = 'od_breakout_best';

  // Power-up catalogue: key, weight, color, letter, duration
  var POWERS = [
    { k: 'multiball', w: 16, color: '#7cff6b', letter: '×3', dur: 0 },
    { k: 'fire',      w: 12, color: '#ff7a2d', letter: 'F',  dur: 10 },
    { k: 'ghost',     w: 12, color: '#4df3ff', letter: 'G',  dur: 8 },
    { k: 'heavy',     w: 10, color: '#ff4d6d', letter: 'H',  dur: 12 },
    { k: 'wide',      w: 14, color: '#ffe14d', letter: 'W',  dur: 12 },
    { k: 'slowmo',    w: 8,  color: '#6b9bff', letter: 'S',  dur: 6 },
    { k: 'life',      w: 6,  color: '#ff6b81', letter: '+',  dur: 0 },
    { k: 'shield',    w: 12, color: '#4dffc3', letter: '◓',  dur: 0 }
  ];
  var POWER_NAMES = { multiball: 'MULTIBALL ×3!', fire: 'FIREBALL!', ghost: 'GHOST PHASE!',
    heavy: 'WRECKING BALL!', wide: 'WIDE PADDLE!', slowmo: 'SLOW-MO!',
    life: 'EXTRA LIFE!', shield: 'SHIELD!' };

  var BRICK_COLORS = {
    normal: '#3fa9ff', steep: '#b16bff', flat: '#2de1a8',
    velocity: '#ff9a3d', blink: '#4df3ff', drifter: '#ff5d7e'
  };

  function Game(canvas) {
    this.canvas = canvas;
    this.ctx = canvas.getContext('2d');
    this.W = 0; this.H = 0; this.dpr = 1;
    this.state = 'TITLE';       // TITLE HOWTO PLAYING PAUSED LEVELCLEAR GAMEOVER VICTORY
    this.sub = 'SERVE';         // SERVE | LIVE (when PLAYING)
    this.time = 0; this.levelTime = 0;
    this.score = 0;
    try { this.best = parseInt(localStorage.getItem(SAVE_KEY) || '0', 10) || 0; }
    catch (e) { this.best = 0; }
    this.lives = 3; this.levelIdx = 0; this.endless = false; this.loop = 0;
    this.mult = 1; this.combo = 0; this.bestCombo = 0;
    this.overdrive = false; this.odGrace = 0; this.odTime = 0;
    this.balls = []; this.bricks = []; this.caps = [];
    this.fieldBottom = 0; this.cols = 10;
    this.trauma = 0; this.freeze = 0;
    this.fx = { fire: 0, ghost: 0, heavy: 0, wide: 0, slowmo: 0, shield: false };
    this.paddle = { x: 0, y: 0, w: 92, h: 14, vx: 0, vy: 0, sx: 1, sy: 1, trail: [] };
    this.orbs = []; this.stars = [];
    this.clearTimer = 0; this.serveTimer = 0;
    this.bgHue = 0;
    this.hudCache = {};
    for (var i = 0; i < 14; i++) this.orbs.push({ x: Math.random(), y: Math.random(), r: rnd(20, 70), s: rnd(4, 14) });
    for (var j = 0; j < 90; j++) this.stars.push({ x: Math.random(), y: Math.random(), s: rnd(0.5, 2), v: rnd(20, 90) });
    this.resize();
  }

  Game.prototype.resize = function () {
    var w = window.innerWidth, h = window.innerHeight;
    this.dpr = Math.min(2, window.devicePixelRatio || 1);
    this.canvas.width = Math.round(w * this.dpr);
    this.canvas.height = Math.round(h * this.dpr);
    this.canvas.style.width = w + 'px';
    this.canvas.style.height = h + 'px';
    this.W = w; this.H = h;
    this.ctx.setTransform(this.dpr, 0, 0, this.dpr, 0, 0);
    this.paddleZoneTop = h * 0.72;
    this.layoutBricks();
    this.updateFieldBottom();
    // clamp paddle into new bounds
    var pw = this.paddleW(), ph = this.paddle.h;
    this.paddle.x = clamp(this.paddle.x || this.W / 2, pw / 2, Math.max(pw / 2, this.W - pw / 2));
    this.paddle.y = clamp(this.paddle.y || this.H - 60, this.paddleZoneTop + 20, Math.max(this.paddleZoneTop + 20, this.H - ph / 2 - 10));
    // clamp balls into [r, W-r] x [r, H+10]
    for (var i = 0; i < this.balls.length; i++) {
      var b = this.balls[i];
      b.r = b.r || this.ballR();
      b.x = clamp(b.x, b.r, Math.max(b.r, this.W - b.r));
      b.y = clamp(b.y, b.r, this.H + 10);
    }
  };

  Game.prototype.layoutBricks = function () {
    if (!this.bricks || !this.bricks.length) return;
    var top = Math.max(84, this.H * 0.11);
    var bw = (this.W - 16) / this.cols, bh = clamp(this.H * 0.038, 20, 30);
    for (var i = 0; i < this.bricks.length; i++) {
      var br = this.bricks[i];
      if (typeof br.col !== 'number' || typeof br.row !== 'number') continue;
      br.x = 8 + br.col * bw + 2; br.y = top + br.row * bh + 2;
      br.w = bw - 4; br.h = bh - 4;
    }
  };

  /* ---------- level setup ---------- */
  Game.prototype.loadLevel = function (idx) {
    var defs = window.BreakoutLevels;
    var def = defs[idx % defs.length];
    this.levelIdx = idx;
    this.acc = 0; this._last = 0;
    Input.launched = false;
    this.showBanner(false);
    this.bricks = [];
    this.caps = [];
    this.balls = [];
    this.combo = 0;
    this.overdrive = false; this.odGrace = 0;
    for (var k in this.fx) this.fx[k] = (k === 'shield') ? false : 0;
    FX.clear();
    var rows = def.rows, nRows = rows.length;
    var top = Math.max(84, this.H * 0.11);
    var availW = this.W - 16;
    var bw = availW / this.cols, bh = clamp(this.H * 0.038, 20, 30);
    var y = top;
    for (var r = 0; r < nRows; r++) {
      var line = rows[r];
      for (var c = 0; c < this.cols; c++) {
        var ch = line[c] || '.';
        if (ch === '.' || ch === ' ') continue;
        var type = { N: 'normal', S: 'steep', F: 'flat', V: 'velocity', B: 'blink', D: 'drifter' }[ch] || 'normal';
        this.bricks.push({
          col: c, row: r, type: type, alive: true,
          x: 8 + c * bw + 2, y: y + 2, w: bw - 4, h: bh - 4,
          phase: Math.random() * 2, driftDir: Math.random() < 0.5 ? -1 : 1,
          driftSpd: rnd(50, 95), flash: 0, hintCd: 0
        });
      }
      y += bh;
    }
    this.fieldBottom = y;
    this.levelTime = 0;
    this.resetPaddle();
    this.spawnServeBall();
    this.sub = 'SERVE';
    this.updateFieldBottom();
  };

  Game.prototype.makeEndlessLevel = function () {
    // Random remixed wall for endless mode
    var kinds = ['N', 'N', 'N', 'S', 'F', 'V', 'B', 'D'];
    var rows = ['..........'];
    for (var r = 0; r < 6; r++) {
      var s = '';
      for (var c = 0; c < 10; c++) {
        if (c === 4 + (r % 2) || Math.random() < 0.12) s += '.';
        else s += kinds[(Math.random() * kinds.length) | 0];
      }
      rows.push(s);
    }
    rows.push('..........');
    var defs = window.BreakoutLevels;
    defs.push({ name: 'ENDLESS ' + (this.loop + 1), hint: 'Remixed wall — faster balls!', rows: rows });
  };

  Game.prototype.resetPaddle = function () {
    var p = this.paddle;
    p.x = this.W / 2; p.y = this.H - Math.max(46, this.H * 0.07);
    p.y = Math.max(p.y, this.paddleZoneTop + 20);
    p.vx = 0; p.vy = 0; p.sx = 1; p.sy = 1; p.trail = [];
  };
  Game.prototype.paddleW = function () { return (this.fx.wide > 0 ? 1.5 : 1) * clamp(this.W * 0.19, 64, 120); };
  Game.prototype.ballR = function () { return (this.fx.heavy > 0 ? 1.6 : 1) * clamp(this.W * 0.016, 6, 10); };

  Game.prototype.spawnServeBall = function () {
    Input.launched = false;
    var p = this.paddle;
    this.balls.push({ x: p.x, y: p.y - 20, vx: 0, vy: 0, r: this.ballR(), stuck: true, trail: [] });
  };

  Game.prototype.launchBalls = function () {
    if (this.state !== 'PLAYING' || this.sub !== 'SERVE') return;
    var launched = false;
    for (var i = 0; i < this.balls.length; i++) {
      var b = this.balls[i];
      if (!b.stuck) continue;
      var aim = clamp((this.paddle.x / this.W - 0.5) * 1.4, -0.8, 0.8);
      var sp = 480 + this.loop * 30;
      b.vx = aim * sp; b.vy = -Math.sqrt(Math.max(sp * sp - b.vx * b.vx, 100));
      b.stuck = false; launched = true;
    }
    if (launched) { this.sub = 'LIVE'; Audio.launch(); this.hideServeHint(); }
  };
/* ---------- main loop ---------- */
  Game.prototype.frame = function (t) {
    if (!this._last) this._last = t;
    var raw = Math.min(0.05, (t - this._last) / 1000 || 0.016);
    this._last = t;
    // hit-stop micro-freeze: eat world time but keep rendering
    if (this.freeze > 0) { this.freeze -= raw; raw *= 0.06; }
    if (this.state === 'PLAYING') {
      this.acc = (this.acc || 0) + raw;
      var n = 0;
      while (this.acc >= 1 / 120 && n < 8) { this.update(1 / 120); this.acc -= 1 / 120; n++; }
      if (n === 8) this.acc = 0;
    } else if (this._clearTick) {
      this._clearTick(raw);
      return;
    } else {
      this.time += raw;
      FX.update(raw);
      if (this.state === 'TITLE' || this.state === 'VICTORY') this.updateBg(raw);
    }
    this.render();
  };

  Game.prototype.timeScale = function () { return this.fx.slowmo > 0 ? 0.45 : 1; };

  Game.prototype.update = function (dt) {
    var sdt = dt * this.timeScale();
    this.time += dt; this.levelTime += dt;
    this.updateBg(dt);
    this.updatePaddle(dt);
    // effect timers tick in real dt
    var f = this.fx;
    if (f.fire > 0) f.fire -= dt;
    if (f.ghost > 0) f.ghost -= dt;
    if (f.heavy > 0) f.heavy -= dt;
    if (f.wide > 0) f.wide -= dt;
    if (f.slowmo > 0) f.slowmo -= dt;
    this.updateBlink(dt);
    this.updateDrifters(sdt);
    if (this.sub === 'SERVE') {
      for (var i = 0; i < this.balls.length; i++) {
        var b = this.balls[i];
        if (b.stuck) { b.x = this.paddle.x; b.y = this.paddle.y - this.paddle.h / 2 - b.r - 3; }
      }
      if (Input.consumeLaunch()) this.launchBalls();
    } else {
      Input.consumeLaunch(); // discard stale launch flags queued during LIVE (e.g. touch-drag end)
      this.updateBalls(sdt);
      this.updateCaps(sdt);
      this.updateOverdrive(dt);
      this.updateMult(dt);
    }
    FX.update(sdt);
    this.trauma = Math.max(0, this.trauma - dt * 1.7);
    // squash recovery
    var p = this.paddle;
    p.sx = lerp(p.sx, 1, 1 - Math.exp(-10 * dt));
    p.sy = lerp(p.sy, 1, 1 - Math.exp(-10 * dt));
    // brick flash decay
    for (var j = 0; j < this.bricks.length; j++) {
      var br = this.bricks[j];
      if (br.flash > 0) br.flash -= dt;
      if (br.hintCd > 0) br.hintCd -= dt;
    }
    this.updateHud();
    Audio.setIntensity(this.overdrive ? 1 : clamp((this.mult - 1) / 6, 0, 0.8));
  };

  /* ---------- paddle: 2D movement, mouse / keys / touch ---------- */
  Game.prototype.updatePaddle = function (dt) {
    var p = this.paddle;
    var px = p.x, py = p.y;
    var w = this.paddleW(), h = p.h;
    var minY = this.paddleZoneTop, maxY = this.H - h / 2 - 10;
    var tx = p.x, ty = p.y, useLerp = true;
    var K = Input.keys;
    var kx = ((K.arrowright || K.d) ? 1 : 0) - ((K.arrowleft || K.a) ? 1 : 0);
    var ky = ((K.arrowdown || K.s) ? 1 : 0) - ((K.arrowup || K.w) ? 1 : 0);
    if (kx || ky) {
      // keyboard velocity mode
      var spd = 560;
      p.x = clamp(p.x + kx * spd * dt, w / 2, this.W - w / 2);
      p.y = clamp(p.y + ky * spd * dt, minY, maxY);
      useLerp = false;
    } else if (Input.touchActive) {
      // touch drag: absolute X + Y offset so finger never covers paddle
      tx = clamp(Input.touchX, w / 2, this.W - w / 2);
      ty = clamp(Input.touchY - 72, minY, maxY);
    } else if (Input.mouseActive) {
      tx = clamp(Input.mouseX, w / 2, this.W - w / 2);
      ty = clamp(Input.mouseY, minY, maxY);
    }
    if (useLerp) {
      var k = 1 - Math.exp(-20 * dt);
      // touch gets a touch more smoothing/inertia feel
      if (Input.touchActive) k = 1 - Math.exp(-14 * dt);
      p.x = lerp(p.x, tx, k);
      p.y = lerp(p.y, ty, k);
    }
    p.vx = lerp(p.vx, (p.x - px) / Math.max(dt, 1e-4), 0.35);
    p.vy = lerp(p.vy, (p.y - py) / Math.max(dt, 1e-4), 0.35);
    // glow trail
    p.trail.push({ x: p.x, y: p.y, age: 0 });
    if (p.trail.length > 14) p.trail.shift();
    for (var i = p.trail.length - 1; i >= 0; i--) {
      p.trail[i].age += dt;
      if (p.trail[i].age > 0.3) p.trail.splice(i, 1);
    }
  };

  Game.prototype.ballSpeed = function (b) { return Math.hypot(b.vx, b.vy); };

  /* ---------- balls: integrate + collide (substepped by caller) ---------- */
  Game.prototype.updateBalls = function (dt) {
    Input.consumeLaunch(); // never queue a launch while rally is live
    var steps = 2, sdt = dt / steps;
    for (var s = 0; s < steps; s++) {
      for (var i = this.balls.length - 1; i >= 0; i--) {
        var b = this.balls[i];
        b.r = this.ballR();
        b.x += b.vx * sdt; b.y += b.vy * sdt;
        this.collideWalls(b);
        this.collidePaddle(b);
        this.collideBricks(b);
      }
    }
    // trails, clamps, anti-stall, lost balls
    for (var j = this.balls.length - 1; j >= 0; j--) {
      var c = this.balls[j];
      if (c.stuck) continue;
      c.trail.push({ x: c.x, y: c.y, age: 0 });
      if (c.trail.length > 16) c.trail.shift();
      for (var t = c.trail.length - 1; t >= 0; t--) {
        c.trail[t].age += dt;
        if (c.trail[t].age > 0.28) c.trail.splice(t, 1);
      }
      var sp = this.ballSpeed(c);
      if (sp > BALL_MAX) { c.vx *= BALL_MAX / sp; c.vy *= BALL_MAX / sp; }
      else if (sp < BALL_MIN && sp > 1) {
        var boost = BALL_MIN / sp; c.vx *= boost; c.vy *= boost;
      }
      // anti-stall: nudge near-horizontal / near-vertical movers
      if (Math.abs(c.vx) < 60) c.vx += (c.vx >= 0 ? 1 : -1) * 140 * dt * 8;
      if (Math.abs(c.vy) < 90 && this.ballSpeed(c) > 1) {
        c.vy += (c.vy >= 0 ? 1 : -1) * 160 * dt * 8;
        var s2 = this.ballSpeed(c), cl = clamp(s2, BALL_MIN, BALL_MAX);
        c.vx *= cl / s2; c.vy *= cl / s2;
      }
      if (c.y - c.r > this.H + 10) {
        // shield safety net?
        if (this.fx.shield) {
          this.fx.shield = false;
          c.y = this.H - 26; c.vy = -Math.abs(c.vy) * 1.02;
          FX.ring(c.x, this.H - 14, '#4dffc3', 90, 0.5);
          FX.floatText(c.x, this.H - 90, 'SHIELD SAVE!', '#4dffc3', 18);
          Audio.powerup(); this.updateHud(true);
        } else {
          this.balls.splice(j, 1);
          FX.burst(c.x, this.H - 8, '#ff5d5d', 14, 260, 0.6, 'spark');
        }
      }
    }
    if (this.balls.length === 0) this.onBallLost();
  };

  Game.prototype.collideWalls = function (b) {
    if (b.x - b.r < 0) { b.x = b.r; b.vx = Math.abs(b.vx); Audio.wallHit(); FX.sparks(b.x, b.y, '#9fd8ff', 3); }
    else if (b.x + b.r > this.W) { b.x = this.W - b.r; b.vx = -Math.abs(b.vx); Audio.wallHit(); FX.sparks(b.x, b.y, '#9fd8ff', 3); }
    if (b.y - b.r < 0) { b.y = b.r; b.vy = Math.abs(b.vy); Audio.wallHit(); FX.sparks(b.x, b.y, '#9fd8ff', 3); }
  };

  // circle vs AABB paddle with english + power-hit detection
  Game.prototype.collidePaddle = function (b) {
    if (b.stuck) return;
    var p = this.paddle, w = this.paddleW(), h = p.h;
    var hw = w / 2, hh = h / 2;
    var nx = clamp(b.x, p.x - hw, p.x + hw), ny = clamp(b.y, p.y - hh, p.y + hh);
    var dx = b.x - nx, dy = b.y - ny;
    if (dx * dx + dy * dy > b.r * b.r) return;
    var sp = Math.max(this.ballSpeed(b), BALL_MIN);
    var hitPos = clamp((b.x - p.x) / hw, -1, 1); // -1..1 english
    var powerUp = p.vy < -140, soften = p.vy > 160;
    if (powerUp) sp = Math.min(sp * 1.18, BALL_MAX);
    if (soften) sp = Math.max(sp * 0.92, BALL_MIN);
    // outgoing angle: base upward fan from hit position + paddle velocity
    var ang = -Math.PI / 2 + hitPos * 1.05 + clamp(p.vx / 1400, -0.4, 0.4);
    if (powerUp) ang = -Math.PI / 2 + hitPos * 0.55; // steeper
    // if ball struck the side steeply, reflect more horizontally
    if (b.vy > 0 && Math.abs(dx) > Math.abs(dy) && Math.abs(hitPos) > 0.92) {
      ang = (dx > 0 ? 0 : Math.PI) + clamp(-p.vy / 1400, -0.5, 0.5);
    }
    b.vx = Math.cos(ang) * sp; b.vy = Math.sin(ang) * sp;
    if (b.vy > -80) b.vy = -80;
    b.x = p.x + hitPos * hw; b.y = p.y - hh - b.r - 1;
    // juice
    p.sx = 1.28; p.sy = 0.68;
    Audio.paddleHit(sp);
    FX.sparks(b.x, b.y, '#00e5ff', powerUp ? 16 : 6);
    if (powerUp) {
      FX.ring(b.x, b.y, '#ffe14d', 70, 0.4);
      FX.floatText(b.x, b.y - 30, 'POWER HIT!', '#ffe14d', 19);
      Audio.powerHitSfx();
      this.trauma = Math.min(1, this.trauma + 0.25);
      this.freeze = Math.max(this.freeze, 0.03);
    }
  };
/* ---------- bricks: circle vs AABB with conditional 1-hit types ---------- */
  Game.prototype.brickAt = function (x, y) { return null; };

  Game.prototype.collideBricks = function (b) {
    var ghost = this.fx.ghost > 0;
    var smash = this.fx.fire > 0 || this.fx.heavy > 0;
    var hit = null, hitNx = 0, hitNy = 0, best = 1e9;
    for (var i = 0; i < this.bricks.length; i++) {
      var br = this.bricks[i];
      if (!br.alive) continue;
      if (br.flash < 0) continue;
      // blink intangible → pass through
      if (br.type === 'blink' && this.blinkSolid() < 0.5) continue;
      var nx = clamp(b.x, br.x, br.x + br.w), ny = clamp(b.y, br.y, br.y + br.h);
      var dx = b.x - nx, dy = b.y - ny;
      var d2 = dx * dx + dy * dy;
      if (d2 > b.r * b.r) continue;
      if (d2 < best) { best = d2; hit = br; hitNx = dx; hitNy = dy; }
    }
    if (!hit) return;
    var sp = this.ballSpeed(b);
    var steepHit = Math.abs(b.vy) > Math.abs(b.vx) * 1.1;
    var flatHit = Math.abs(b.vx) > Math.abs(b.vy);
    var fastHit = sp > VELOCITY_NEED;

    // ghost/phase: damage everything in path, never bounce
    if (ghost) {
      this.breakBrick(hit, b, true);
      return;
    }
    // decide break vs resist
    var breaks = true, hint = null;
    if (hit.type === 'steep' && !steepHit && !smash) { breaks = false; hint = 'NEED STEEP ANGLE!'; }
    else if (hit.type === 'flat' && !flatHit && !smash) { breaks = false; hint = 'NEED SHALLOW ANGLE!'; }
    else if (hit.type === 'velocity' && !fastHit && !smash) { breaks = false; hint = 'TOO SLOW!'; }
    // reflect ball (proper circle-AABB normal; center-inside → min axis)
    var nlen = Math.hypot(hitNx, hitNy);
    if (nlen < 0.001) {
      var cx = hit.x + hit.w / 2 - b.x, cy = hit.y + hit.h / 2 - b.y;
      if (Math.abs(cx) / hit.w > Math.abs(cy) / hit.h) { hitNx = cx > 0 ? -1 : 1; hitNy = 0; }
      else { hitNx = 0; hitNy = cy > 0 ? -1 : 1; }
      nlen = 1;
    }
    hitNx /= nlen; hitNy /= nlen;
    var dot = b.vx * hitNx + b.vy * hitNy;
    if (dot < 0) { b.vx -= 2 * dot * hitNx; b.vy -= 2 * dot * hitNy; }
    b.x += hitNx * 1.5; b.y += hitNy * 1.5;

    if (breaks) this.breakBrick(hit, b, false);
    else {
      hit.flash = 0.25;
      Audio.brickResist();
      if (hit.hintCd <= 0 && hint) {
        hit.hintCd = 1.2;
        FX.floatText(hit.x + hit.w / 2, hit.y - 8, hint,
          hit.type === 'velocity' ? '#ff9a3d' : '#4df3ff', 14);
        if (hit.type === 'velocity') FX.burst(b.x, b.y, '#ff9a3d', 6, 200, 0.4, 'spark');
      }
      FX.sparks(b.x, b.y, '#9fd8ff', 4);
    }
  };

  Game.prototype.breakBrick = function (br, b, phased) {
    br.alive = false;
    var cx = br.x + br.w / 2, cy = br.y + br.h / 2;
    var col = BRICK_COLORS[br.type] || '#fff';
    // score with multiplier
    var pts = Math.round(50 * this.mult);
    this.score += pts;
    this.combo++; this.bestCombo = Math.max(this.bestCombo, this.combo);
    var gain = 0.12 + (this.overdrive ? 0.22 : 0);
    this.mult = clamp(this.mult + gain, 1, 12);
    if (this.overdrive) this.mult = clamp(this.mult * 1.015, 1, 12);
    FX.shards(cx, cy, br.w, br.h, col);
    FX.sparks(b.x, b.y, col, 6);
    FX.floatText(cx, cy - 6, '+' + pts, '#fff', 14);
    if (this.combo >= 5) FX.floatText(cx, cy - 26, this.combo + ' COMBO', '#ffe14d', 14);
    Audio.brickBreak(br.type);
    if (this.fx.fire > 0) {
      FX.burst(cx, cy, '#ff7a2d', 12, 300, 0.5, 'spark');
      FX.ring(cx, cy, '#ff7a2d', 55, 0.35);
      this.trauma = Math.min(1, this.trauma + 0.22);
      this.freeze = Math.max(this.freeze, 0.045);
    } else if (this.fx.heavy > 0) {
      FX.ring(cx, cy, '#ff4d6d', 80, 0.45);
      this.trauma = Math.min(1, this.trauma + 0.3);
      this.freeze = Math.max(this.freeze, 0.05);
    } else {
      this.trauma = Math.min(1, this.trauma + 0.10);
      this.freeze = Math.max(this.freeze, 0.028);
    }
    this.maybeDrop(br);
    this.updateFieldBottom();
    if (this.bricksAlive() === 0) this.onLevelClear();
  };

  Game.prototype.bricksAlive = function () {
    var n = 0;
    for (var i = 0; i < this.bricks.length; i++) if (this.bricks[i].alive) n++;
    return n;
  };
  Game.prototype.updateFieldBottom = function () {
    var fb = 0;
    for (var i = 0; i < this.bricks.length; i++) {
      var br = this.bricks[i];
      if (br.alive) fb = Math.max(fb, br.y + br.h);
    }
    this.fieldBottom = fb;
  };

  /* ---------- power-up drops ---------- */
  Game.prototype.maybeDrop = function (br) {
    if (Math.random() > DROP_CHANCE) return;
    var total = 0, i;
    for (i = 0; i < POWERS.length; i++) {
      if (POWERS[i].k === 'life' && this.lives >= 5) continue;
      total += POWERS[i].w;
    }
    var roll = Math.random() * total, pick = POWERS[0];
    for (i = 0; i < POWERS.length; i++) {
      if (POWERS[i].k === 'life' && this.lives >= 5) continue;
      roll -= POWERS[i].w;
      if (roll <= 0) { pick = POWERS[i]; break; }
    }
    this.caps.push({ x: br.x + br.w / 2, y: br.y, vy: 150, kind: pick.k, color: pick.color, rot: 0 });
  };

  Game.prototype.updateCaps = function (dt) {
    var p = this.paddle, w = this.paddleW();
    for (var i = this.caps.length - 1; i >= 0; i--) {
      var c = this.caps[i];
      c.y += c.vy * dt; c.rot += dt * 3;
      if (c.y > this.H + 20) { this.caps.splice(i, 1); continue; }
      if (c.y + 12 > p.y - p.h / 2 && c.y - 12 < p.y + p.h / 2 &&
          Math.abs(c.x - p.x) < w / 2 + 14) {
        this.caps.splice(i, 1);
        this.applyPower(c.kind, c.x, c.y);
      }
    }
  };

  Game.prototype.applyPower = function (kind, x, y) {
    var f = this.fx, P = this;
    Audio.powerup();
    FX.ring(x, y, '#fff', 60, 0.4);
    FX.floatText(x, y - 18, POWER_NAMES[kind] || kind, '#fff', 17);
    this.score += Math.round(25 * this.mult);
    if (kind === 'multiball') {
      var hadLaunchable = this.balls.some(function (b) { return !b.stuck; });
      var src = this.balls.filter(function (b) { return !b.stuck; });
      if (!src.length && this.balls.length) src = [this.balls[0]];
      var add = [];
      for (var i = 0; i < src.length && this.balls.length + add.length < 20; i++) {
        for (var k = 0; k < 2 && this.balls.length + add.length < 20; k++) {
          var s = src[i], sp = Math.max(P.ballSpeed(s), BALL_MIN);
          var a = Math.atan2(s.vy, s.vx) + (k === 0 ? 0.5 : -0.5);
          add.push({ x: s.x, y: s.y, vx: Math.cos(a) * sp, vy: Math.sin(a) * sp, r: s.r, stuck: false, trail: [] });
        }
      }
      if (!add.length && !hadLaunchable && this.balls.length < 20) { // serving: add two spare balls on paddle
        add.push({ x: this.paddle.x - 20, y: this.paddle.y - 20, vx: 0, vy: 0, r: this.ballR(), stuck: true, trail: [] });
      }
      for (var j = 0; j < add.length; j++) this.balls.push(add[j]);
      if (this.sub === 'SERVE' && this.balls.some(function (b) { return !b.stuck; })) this.sub = 'LIVE';
      FX.burst(x, y, '#7cff6b', 16, 280, 0.6, 'dot');
    }
    else if (kind === 'fire') f.fire = 10;
    else if (kind === 'ghost') f.ghost = 8;
    else if (kind === 'heavy') f.heavy = 12;
    else if (kind === 'wide') f.wide = 12;
    else if (kind === 'slowmo') f.slowmo = 6;
    else if (kind === 'life') { this.lives = Math.min(9, this.lives + 1); FX.confetti(x, y, 24); }
    else if (kind === 'shield') f.shield = true;
    this.updateHud(true);
  };

  /* ---------- blink + drifters ---------- */
  Game.prototype.blinkPhase = function () { return (this.time % 2.0) / 2.0; }; // 2s cycle
  Game.prototype.blinkSolid = function () {
    var ph = this.blinkPhase();
    if (ph < 0.55) return 1;          // solid
    if (ph < 0.72) return 0.5 + (0.72 - ph); // warning fade
    return 0;                          // intangible
  };
  Game.prototype.updateBlink = function () {};
  Game.prototype.updateDrifters = function (dt) {
    for (var i = 0; i < this.bricks.length; i++) {
      var br = this.bricks[i];
      if (!br.alive || br.type !== 'drifter') continue;
      br.x += br.driftDir * br.driftSpd * dt;
      if (br.x < 8) { br.x = 8; br.driftDir = 1; }
      if (br.x + br.w > this.W - 8) { br.x = this.W - 8 - br.w; br.driftDir = -1; }
      // bounce off horizontal neighbors
      for (var j = 0; j < this.bricks.length; j++) {
        if (i === j) continue;
        var o = this.bricks[j];
        if (!o.alive || Math.abs(o.y - br.y) > br.h) continue;
        if (br.driftDir > 0 && br.x + br.w > o.x && br.x < o.x) { br.driftDir = -1; br.x = o.x - br.w - 1; }
        else if (br.driftDir < 0 && br.x < o.x + o.w && br.x + br.w > o.x + o.w) { br.driftDir = 1; br.x = o.x + o.w + 1; }
      }
    }
  };

  /* ---------- overdrive + multiplier ---------- */
  Game.prototype.anyTopside = function () {
    if (!this.fieldBottom) return false;
    for (var i = 0; i < this.balls.length; i++) {
      var b = this.balls[i];
      if (!b.stuck && b.y < this.fieldBottom - 4) return true;
    }
    return false;
  };
  Game.prototype.updateOverdrive = function (dt) {
    var top = this.anyTopside();
    if (top) {
      if (!this.overdrive) {
        this.overdrive = true; this.odTime = 0;
        Audio.overdriveEnter();
        this.showBanner(true);
        FX.floatText(this.W / 2, this.H * 0.45, 'OVERDRIVE!', '#ffe14d', 30);
        FX.confetti(this.W / 2, this.H * 0.3, 40);
        this.trauma = Math.min(1, this.trauma + 0.3);
      }
      this.odGrace = 1.2; this.odTime += dt;
      // exponential growth while topside
      this.mult = clamp(this.mult + (0.45 + this.mult * 0.5) * dt, 1, 12);
      if (Math.random() < dt * 20) {
        var b = this.balls[(Math.random() * this.balls.length) | 0];
        if (b) FX.sparks(b.x, b.y, ['#ff2d95', '#ffe14d', '#00e5ff'][(Math.random() * 3) | 0], 2);
      }
    } else if (this.overdrive) {
      this.odGrace -= dt;
      if (this.odGrace <= 0) {
        this.overdrive = false;
        Audio.overdriveExit();
        this.showBanner(false);
        FX.floatText(this.W / 2, this.H * 0.45, 'OVERDRIVE END', '#9fd8ff', 20);
      }
    }
    if (this.overdrive) this.showBanner(true);
  };
  Game.prototype.updateMult = function (dt) {
    if (!this.overdrive && !this.anyTopside()) {
      // decay when play returns below
      this.mult = Math.max(1, this.mult - dt * 0.55);
    }
    if (this.sub === 'LIVE' && this.balls.length) this.combo = this.combo; // combo persists during rally
  };

  /* ---------- life / level flow ---------- */
  Game.prototype.onBallLost = function () {
    this.lives--;
    Input.launched = false;
    Audio.loseLife();
    this.mult = 1; this.combo = 0;
    this.fx.fire = this.fx.ghost = this.fx.heavy = this.fx.slowmo = this.fx.wide = 0;
    this.trauma = Math.min(1, this.trauma + 0.35);
    if (this.lives <= 0) { this.gameOver(); return; }
    if (this.overdrive) { this.overdrive = false; this.showBanner(false); }
    this.resetPaddle();
    this.spawnServeBall();
    this.sub = 'SERVE';
    this.showServeHint();
    this.updateHud(true);
  };
  Game.prototype.onLevelClear = function () {
    this.state = 'LEVELCLEAR';
    this.showBanner(false);
    this.clearTimer = 2.4;
    this.mult = clamp(this.mult + 1, 1, 12);
    this.score += Math.round(500 * this.mult) + this.lives * 100;
    Audio.levelClear();
    FX.confetti(this.W / 2, this.H * 0.35, 120);
    this.saveBest();
    // handled in frame(): countdown then advance — use interval via update hook
    var self = this;
    this._clearTick = function (raw) {
      self.clearTimer -= raw;
      FX.update(raw);
      self.updateBg(raw);
      self.render();
      if (self.clearTimer <= 0) {
        self._clearTick = null;
        self.nextLevel();
      }
    };
  };
/* ---------- state flow ---------- */
  Game.prototype.newGame = function () {
    this.score = 0; this.lives = 3; this.loop = 0; this.endless = false;
    this.mult = 1; this.combo = 0; this.bestCombo = 0;
    this.hideAll();
    this.loadLevel(0);
    this.state = 'PLAYING';
    this.showHud(true);
    this.showLevelCard(0);
  };
  Game.prototype.nextLevel = function () {
    var defs = window.BreakoutLevels;
    var ni = this.levelIdx + 1;
    if (ni >= defs.length && !this.endless) { this.victory(); return; }
    if (this.endless && ni >= defs.length) { this.makeEndlessLevel(); this.loop++; }
    this.hideAll();
    this.loadLevel(ni);
    this.state = 'PLAYING';
    this.showHud(true);
    this.showLevelCard(ni);
  };
  Game.prototype.gameOver = function () {
    this.state = 'GAMEOVER';
    this.saveBest();
    Audio.gameOver();
    this.showBanner(false);
    $('go-score').textContent = this.score;
    $('go-best').textContent = this.best;
    $('newbest').classList.toggle('hidden', this.score < this.best || this.score === 0);
    this.showHud(false);
    this.show('gameover-screen');
  };
  Game.prototype.victory = function () {
    this.state = 'VICTORY';
    this.saveBest();
    this.showBanner(false);
    $('vic-score').textContent = this.score;
    this.showHud(false);
    this.show('victory-screen');
    FX.confetti(this.W / 2, this.H * 0.3, 150);
  };
  Game.prototype.startEndless = function () {
    this.endless = true;
    this.hideAll();
    this.showHud(true);
    this.nextLevel();
  };
  Game.prototype.togglePause = function () {
    if (this.state === 'PLAYING') {
      this.state = 'PAUSED';
      this.show('pause-screen');
      Audio.uiClick();
    } else if (this.state === 'PAUSED') {
      this.state = 'PLAYING';
      this.hide('pause-screen');
      this._last = 0; this.acc = 0;
      Audio.uiClick();
    }
  };
  Game.prototype.quitToTitle = function () {
    this.state = 'TITLE';
    this.hideAll();
    this.showBanner(false);
    this.showHud(false);
    $('title-best').textContent = this.best;
    this.show('title-screen');
  };
  Game.prototype.saveBest = function () {
    if (this.score > this.best) {
      this.best = this.score;
      try { localStorage.setItem(SAVE_KEY, String(this.best)); } catch (e) {}
    }
  };

  /* ---------- DOM helpers ---------- */
  Game.prototype.show = function (id) { $(id).classList.remove('hidden'); };
  Game.prototype.hide = function (id) { $(id).classList.add('hidden'); };
  Game.prototype.hideAll = function () {
    var ids = ['title-screen', 'howto-screen', 'pause-screen', 'level-card', 'gameover-screen', 'victory-screen'];
    for (var i = 0; i < ids.length; i++) $(ids[i]).classList.add('hidden');
  };
  Game.prototype.showHud = function (on) { $('hud').classList.toggle('hidden', !on); if (on) this.updateHud(true); };
  Game.prototype.showServeHint = function () { $('serve-hint').classList.remove('hidden'); };
  Game.prototype.hideServeHint = function () { $('serve-hint').classList.add('hidden'); };
  Game.prototype.showBanner = function (on) {
    try {
      if (typeof $ === 'undefined') return;
      var el = $('overdrive-banner');
      if (!el) return;
      el.classList.toggle('hidden', !on);
      if (on) {
        var t = $('od-text');
        if (t) t.textContent = 'OVERDRIVE x' + this.mult.toFixed(1) + ' — STAY UP THERE!';
      }
    } catch (e) {}
  };
  var toastTimer = null;
  Game.prototype.toast = function (msg, ms) {
    var el = $('toast');
    el.textContent = msg; el.classList.remove('hidden');
    if (toastTimer) clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { el.classList.add('hidden'); }, ms || 1800);
  };
  Game.prototype.showLevelCard = function (idx) {
    var defs = window.BreakoutLevels;
    var def = defs[idx % defs.length];
    $('level-num').textContent = 'LEVEL ' + (idx + 1);
    $('level-name').textContent = def.name;
    $('level-hint').textContent = def.hint;
    this.show('level-card');
    this.showServeHint();
    this.updateHud(true);
  };
  Game.prototype.dismissLevelCard = function () {
    this.hide('level-card');
    Audio.uiClick();
  };

  Game.prototype.updateHud = function (force) {
    var c = this.hudCache;
    function set(id, v) { if (force || c[id] !== v) { c[id] = v; $(id).textContent = v; } }
    set('hud-score', String(this.score));
    set('hud-best', 'BEST ' + this.best);
    set('hud-level', 'LV ' + (this.levelIdx + 1) + (this.endless ? ' ∞' : ''));
    set('mult-label', 'x' + this.mult.toFixed(1) + (this.overdrive ? ' OD!' : ''));
    var hearts = '';
    for (var i = 0; i < Math.min(9, this.lives); i++) hearts += '●';
    set('hud-lives', hearts || '—');
    var pct = clamp((this.mult - 1) / 11 * 100, 0, 100).toFixed(0) + '%';
    if (force || c.fill !== pct) { c.fill = pct; $('mult-fill').style.width = pct; }
    // power timers
    var f = this.fx, html = '';
    function chip(label, t, color) {
      html += '<span class="pow-chip" style="border-color:' + color + ';color:' + color + '">' +
        label + ' ' + Math.ceil(t) + 's</span>';
    }
    if (f.fire > 0) chip('FIRE', f.fire, '#ff7a2d');
    if (f.ghost > 0) chip('GHOST', f.ghost, '#4df3ff');
    if (f.heavy > 0) chip('HEAVY', f.heavy, '#ff4d6d');
    if (f.wide > 0) chip('WIDE', f.wide, '#ffe14d');
    if (f.slowmo > 0) chip('SLOW', f.slowmo, '#6b9bff');
    if (f.shield) html += '<span class="pow-chip" style="border-color:#4dffc3;color:#4dffc3">SHIELD</span>';
    if (force || c.pow !== html) { c.pow = html; $('power-timers').innerHTML = html; }
    if (this.overdrive) $('od-text').textContent = 'OVERDRIVE x' + this.mult.toFixed(1) + ' — STAY UP THERE!';
  };

  /* ---------- animated background ---------- */
  Game.prototype.updateBg = function (dt) {
    var boost = this.overdrive ? 3.2 : (1 + (this.mult - 1) * 0.25);
    for (var i = 0; i < this.orbs.length; i++) {
      var o = this.orbs[i];
      o.y -= o.s * boost * dt / this.H * 4;
      o.x += Math.sin(this.time * 0.4 + i) * dt * 0.01;
      if (o.y < -0.15) { o.y = 1.15; o.x = Math.random(); }
    }
    for (var j = 0; j < this.stars.length; j++) {
      var s = this.stars[j];
      s.y += s.v * boost * dt / this.H;
      if (s.y > 1) { s.y = 0; s.x = Math.random(); }
    }
    this.bgHue = (this.bgHue + dt * (this.overdrive ? 30 : 4)) % 360;
  };
/* ---------- render ---------- */
  Game.prototype.render = function () {
    var ctx = this.ctx, W = this.W, H = this.H;
    ctx.save();
    // screen shake (trauma-based)
    if (this.trauma > 0) {
      var sh = this.trauma * this.trauma * 14;
      ctx.translate(rnd(-sh, sh), rnd(-sh, sh));
    }
    this.drawBg(ctx, W, H);
    this.drawBricks(ctx);
    this.drawCaps(ctx);
    this.drawShield(ctx);
    this.drawPaddle(ctx);
    this.drawBalls(ctx);
    FX.draw(ctx);
    this.drawBreakthrough(ctx);
    ctx.restore();
    this.drawVignette(ctx, W, H);
  };

  Game.prototype.drawBg = function (ctx, W, H) {
    var od = this.overdrive;
    var g = ctx.createLinearGradient(0, 0, 0, H);
    if (od) {
      g.addColorStop(0, '#2b0548'); g.addColorStop(0.5, '#0e1a5e'); g.addColorStop(1, '#3d0a2e');
    } else {
      g.addColorStop(0, '#0b1026'); g.addColorStop(0.6, '#0d1440'); g.addColorStop(1, '#160b2e');
    }
    ctx.fillStyle = g;
    ctx.fillRect(-20, -20, W + 40, H + 40);
    // grid
    ctx.strokeStyle = od ? 'rgba(255,80,180,0.16)' : 'rgba(80,140,255,0.10)';
    ctx.lineWidth = 1;
    var gs = 44, off = (this.time * (od ? 90 : 22)) % gs;
    ctx.beginPath();
    for (var x = 0; x <= W; x += gs) { ctx.moveTo(x, 0); ctx.lineTo(x, H); }
    for (var y = off; y <= H; y += gs) { ctx.moveTo(0, y); ctx.lineTo(W, y); }
    ctx.stroke();
    // floating orbs (speed up with multiplier)
    for (var i = 0; i < this.orbs.length; i++) {
      var o = this.orbs[i];
      var hue = (this.bgHue + i * 30) % 360;
      var gr = ctx.createRadialGradient(o.x * W, o.y * H, 0, o.x * W, o.y * H, o.r);
      gr.addColorStop(0, 'hsla(' + hue + ',90%,60%,' + (od ? 0.20 : 0.10) + ')');
      gr.addColorStop(1, 'hsla(' + hue + ',90%,60%,0)');
      ctx.fillStyle = gr;
      ctx.beginPath(); ctx.arc(o.x * W, o.y * H, o.r, 0, TAU); ctx.fill();
    }
    // starfield (warp streaks in overdrive)
    for (var j = 0; j < this.stars.length; j++) {
      var s = this.stars[j];
      ctx.fillStyle = od ? 'rgba(255,255,255,0.9)' : 'rgba(200,220,255,0.5)';
      if (od) {
        ctx.strokeStyle = 'rgba(255,255,255,0.55)'; ctx.lineWidth = s.s;
        ctx.beginPath(); ctx.moveTo(s.x * W, s.y * H);
        ctx.lineTo(s.x * W, s.y * H + 14 + s.v * 0.15); ctx.stroke();
      } else {
        ctx.fillRect(s.x * W, s.y * H, s.s, s.s);
      }
    }
    // chromatic pulse at high multiplier: neon edge glow
    if (this.mult >= 5 && this.state === 'PLAYING') {
      var a = 0.25 + 0.2 * Math.sin(this.time * 6);
      ctx.strokeStyle = 'hsla(' + (this.bgHue % 360) + ',100%,65%,' + a + ')';
      ctx.lineWidth = 6;
      ctx.strokeRect(3, 3, W - 6, H - 6);
    }
  };

  Game.prototype.drawBricks = function (ctx) {
    var solid = this.blinkSolid();
    var anyFast = false, i, br;
    for (i = 0; i < this.bricks.length; i++) {
      if (!this.bricks[i].alive) continue;
      if (this.bricks[i].type === 'velocity') {
        for (var k = 0; k < this.balls.length; k++) {
          if (!this.balls[k].stuck && this.ballSpeed(this.balls[k]) > VELOCITY_NEED) { anyFast = true; break; }
        }
        break;
      }
    }
    for (i = 0; i < this.bricks.length; i++) {
      br = this.bricks[i];
      if (!br.alive) continue;
      var cx = br.x + br.w / 2, cy = br.y + br.h / 2;
      var col = BRICK_COLORS[br.type];
      ctx.save();
      // blink phasing
      if (br.type === 'blink') {
        if (solid <= 0) { ctx.globalAlpha = 0.13; }
        else if (solid < 1) { ctx.globalAlpha = 0.35 + 0.3 * Math.sin(this.time * 30); }
      }
      // velocity pulse when a ball is fast enough
      var pulse = 0;
      if (br.type === 'velocity' && anyFast) pulse = 0.5 + 0.5 * Math.sin(this.time * 10);
      // drifter slide is positional already; add glow stripe
      var flash = br.flash > 0 ? br.flash * 3 : 0;
      // body
      ctx.shadowColor = col; ctx.shadowBlur = 8 + pulse * 14 + flash * 20;
      ctx.fillStyle = col;
      if (br.type === 'normal') {
        ctx.globalAlpha = Math.min(1, (ctx.globalAlpha || 1));
        roundRect(ctx, br.x, br.y, br.w, br.h, 5); ctx.fill();
        ctx.shadowBlur = 0;
        ctx.fillStyle = 'rgba(255,255,255,0.35)';
        roundRect(ctx, br.x + 3, br.y + 2, br.w - 6, 4, 2); ctx.fill();
      } else {
        roundRect(ctx, br.x, br.y, br.w, br.h, 5); ctx.fill();
        ctx.shadowBlur = 0;
        ctx.fillStyle = 'rgba(0,0,0,0.35)';
        roundRect(ctx, br.x + 2, br.y + br.h - 7, br.w - 4, 5, 2); ctx.fill();
      }
      if (flash > 0) {
        ctx.fillStyle = 'rgba(255,255,255,' + clamp(flash, 0, 0.9) + ')';
        roundRect(ctx, br.x, br.y, br.w, br.h, 5); ctx.fill();
      }
      // icons (procedural, no assets)
      ctx.fillStyle = 'rgba(0,0,0,0.55)';
      ctx.strokeStyle = 'rgba(255,255,255,0.85)';
      ctx.lineWidth = 1.6;
      var s = Math.min(br.w, br.h) * 0.32;
      if (br.type === 'steep') { // ▲ prism
        ctx.beginPath(); ctx.moveTo(cx, cy - s); ctx.lineTo(cx + s, cy + s * 0.8);
        ctx.lineTo(cx - s, cy + s * 0.8); ctx.closePath(); ctx.fill(); ctx.stroke();
      } else if (br.type === 'flat') { // ▼ skimmer
        ctx.beginPath(); ctx.moveTo(cx, cy + s); ctx.lineTo(cx + s, cy - s * 0.8);
        ctx.lineTo(cx - s, cy - s * 0.8); ctx.closePath(); ctx.fill(); ctx.stroke();
      } else if (br.type === 'velocity') { // ◆ + speed lines
        ctx.save(); ctx.translate(cx, cy); ctx.rotate(Math.PI / 4);
        ctx.fillRect(-s * 0.7, -s * 0.7, s * 1.4, s * 1.4); ctx.restore();
        ctx.strokeStyle = 'rgba(255,255,255,' + (0.4 + pulse * 0.6) + ')';
        ctx.beginPath();
        ctx.moveTo(br.x + 4, cy - 4); ctx.lineTo(br.x + 12, cy - 4);
        ctx.moveTo(br.x + 4, cy + 1); ctx.lineTo(br.x + 15, cy + 1);
        ctx.moveTo(br.x + br.w - 12, cy - 4); ctx.lineTo(br.x + br.w - 4, cy - 4);
        ctx.moveTo(br.x + br.w - 15, cy + 1); ctx.lineTo(br.x + br.w - 4, cy + 1);
        ctx.stroke();
      } else if (br.type === 'blink') { // ◎ phaser rings
        ctx.beginPath(); ctx.arc(cx, cy, s, 0, TAU); ctx.stroke();
        ctx.beginPath(); ctx.arc(cx, cy, s * 0.45, 0, TAU); ctx.fill();
      } else if (br.type === 'drifter') { // ↔ arrows
        ctx.beginPath();
        ctx.moveTo(cx - s - 4, cy); ctx.lineTo(cx + s + 4, cy);
        ctx.moveTo(cx - s - 4, cy); ctx.lineTo(cx - s + 1, cy - 4);
        ctx.moveTo(cx - s - 4, cy); ctx.lineTo(cx - s + 1, cy + 4);
        ctx.moveTo(cx + s + 4, cy); ctx.lineTo(cx + s - 1, cy - 4);
        ctx.moveTo(cx + s + 4, cy); ctx.lineTo(cx + s - 1, cy + 4);
        ctx.stroke();
      }
      ctx.restore();
    }
  };

  function roundRect(ctx, x, y, w, h, r) {
    r = Math.min(r, w / 2, h / 2);
    ctx.beginPath();
    ctx.moveTo(x + r, y);
    ctx.arcTo(x + w, y, x + w, y + h, r);
    ctx.arcTo(x + w, y + h, x, y + h, r);
    ctx.arcTo(x, y + h, x, y, r);
    ctx.arcTo(x, y, x + w, y, r);
    ctx.closePath();
  }
Game.prototype.drawCaps = function (ctx) {
    for (var i = 0; i < this.caps.length; i++) {
      var c = this.caps[i];
      ctx.save();
      ctx.translate(c.x, c.y);
      ctx.shadowColor = c.color; ctx.shadowBlur = 12;
      // falling capsule
      ctx.fillStyle = 'rgba(8,12,30,0.95)';
      ctx.strokeStyle = c.color; ctx.lineWidth = 2;
      ctx.beginPath();
      if (ctx.roundRect) ctx.roundRect(-13, -10, 26, 20, 10);
      else ctx.rect(-13, -10, 26, 20);
      ctx.fill(); ctx.stroke();
      ctx.shadowBlur = 0;
      ctx.fillStyle = c.color;
      ctx.font = '800 12px system-ui,sans-serif';
      ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
      ctx.fillText(POWER_LABEL[c.kind] || '?', 0, 1);
      ctx.restore();
    }
  };
  var POWER_LABEL = { multiball: '×3', fire: 'FIR', ghost: 'GHO', heavy: 'HVY', wide: 'WIDE', slowmo: 'SLO', life: '+1', shield: 'SHD' };

  Game.prototype.drawShield = function (ctx) {
    if (!this.fx.shield || this.state !== 'PLAYING') return;
    var y = this.H - 8;
    ctx.save();
    ctx.strokeStyle = '#4dffc3'; ctx.lineWidth = 3;
    ctx.shadowColor = '#4dffc3'; ctx.shadowBlur = 14;
    ctx.beginPath();
    for (var x = 0; x <= this.W; x += 18) {
      ctx.moveTo(x, y);
      ctx.lineTo(x + 9, y - 6 - 2 * Math.sin(this.time * 8 + x * 0.2));
    }
    ctx.stroke();
    ctx.restore();
  };

  Game.prototype.drawPaddle = function (ctx) {
    var p = this.paddle, w = this.paddleW(), h = p.h;
    // glow trail
    for (var i = 0; i < p.trail.length; i++) {
      var t = p.trail[i], a = (1 - t.age / 0.3) * 0.25;
      ctx.fillStyle = 'rgba(0,229,255,' + a.toFixed(3) + ')';
      roundRect(ctx, t.x - w / 2, t.y - h / 2, w, h, 7); ctx.fill();
    }
    ctx.save();
    ctx.translate(p.x, p.y);
    ctx.scale(Math.max(0.5, p.sx), Math.max(0.5, p.sy));
    var wide = this.fx.wide > 0;
    var g = ctx.createLinearGradient(0, -h / 2, 0, h / 2);
    if (wide) { g.addColorStop(0, '#fff6c0'); g.addColorStop(0.5, '#ffe14d'); g.addColorStop(1, '#ff9a3d'); }
    else { g.addColorStop(0, '#d8fbff'); g.addColorStop(0.5, '#00e5ff'); g.addColorStop(1, '#0077ff'); }
    ctx.shadowColor = wide ? '#ffe14d' : '#00e5ff'; ctx.shadowBlur = 18;
    ctx.fillStyle = g;
    roundRect(ctx, -w / 2, -h / 2, w, h, 7); ctx.fill();
    ctx.shadowBlur = 0;
    ctx.fillStyle = 'rgba(255,255,255,0.75)';
    roundRect(ctx, -w / 2 + 5, -h / 2 + 2, w - 10, 3, 1.5); ctx.fill();
    // up-flick indicator: paddle glows gold while moving up fast
    if (p.vy < -140) {
      ctx.strokeStyle = 'rgba(255,225,77,' + clamp(-p.vy / 600, 0, 0.9) + ')';
      ctx.lineWidth = 2.5;
      roundRect(ctx, -w / 2 - 2, -h / 2 - 2, w + 4, h + 4, 9); ctx.stroke();
    }
    ctx.restore();
  };

  Game.prototype.drawBalls = function (ctx) {
    var fire = this.fx.fire > 0, ghost = this.fx.ghost > 0, heavy = this.fx.heavy > 0;
    for (var i = 0; i < this.balls.length; i++) {
      var b = this.balls[i];
      // trail
      for (var j = 0; j < b.trail.length; j++) {
        var t = b.trail[j], a = (1 - t.age / 0.28);
        var tc = fire ? '255,122,45' : (ghost ? '77,243,255' : '0,229,255');
        ctx.fillStyle = 'rgba(' + tc + ',' + (a * 0.35).toFixed(3) + ')';
        ctx.beginPath(); ctx.arc(t.x, t.y, b.r * a * 0.9, 0, TAU); ctx.fill();
      }
      ctx.save();
      if (ghost) ctx.globalAlpha = 0.55;
      var col = fire ? '#ff7a2d' : (ghost ? '#4df3ff' : '#ffffff');
      ctx.shadowColor = fire ? '#ff5d00' : (heavy ? '#ff4d6d' : '#00e5ff');
      ctx.shadowBlur = fire || heavy ? 22 : 12;
      var g = ctx.createRadialGradient(b.x - b.r * 0.3, b.y - b.r * 0.3, b.r * 0.1, b.x, b.y, b.r);
      g.addColorStop(0, '#ffffff'); g.addColorStop(0.55, col); g.addColorStop(1, fire ? '#a32e00' : '#0077ff');
      ctx.fillStyle = g;
      ctx.beginPath(); ctx.arc(b.x, b.y, b.r, 0, TAU); ctx.fill();
      ctx.shadowBlur = 0;
      if (heavy) {
        ctx.strokeStyle = '#ff4d6d'; ctx.lineWidth = 2;
        ctx.beginPath(); ctx.arc(b.x, b.y, b.r + 3 + Math.sin(this.time * 12), 0, TAU); ctx.stroke();
      }
      ctx.restore();
    }
  };

  Game.prototype.drawBreakthrough = function (ctx) {
    // subtle "BREAK THROUGH!" hint arrow until first overdrive of the level
    if (this.state !== 'PLAYING' || this.overdrive || this.odTime > 0 || this.levelTime > 14) return;
    if (this.bricksAlive() === 0) return;
    var x = this.W / 2, y = this.fieldBottom + 34 + Math.sin(this.time * 4) * 6;
    ctx.save();
    ctx.globalAlpha = 0.65 + 0.3 * Math.sin(this.time * 4);
    ctx.fillStyle = '#ffe14d';
    ctx.shadowColor = '#ffe14d'; ctx.shadowBlur = 12;
    ctx.beginPath();
    ctx.moveTo(x - 10, y); ctx.lineTo(x + 10, y); ctx.lineTo(x, y - 14);
    ctx.closePath(); ctx.fill();
    ctx.font = '800 13px system-ui,sans-serif';
    ctx.textAlign = 'center';
    ctx.fillText('BREAK THROUGH!', x, y + 18);
    ctx.restore();
  };

  Game.prototype.drawVignette = function (ctx, W, H) {
    var g = ctx.createRadialGradient(W / 2, H / 2, Math.min(W, H) * 0.42, W / 2, H / 2, Math.max(W, H) * 0.75);
    var edge = this.fx.slowmo > 0 ? 'rgba(60,120,255,0.42)' : 'rgba(0,0,10,0.42)';
    g.addColorStop(0, 'rgba(0,0,0,0)');
    g.addColorStop(1, edge);
    ctx.fillStyle = g;
    ctx.fillRect(0, 0, W, H);
    if (this.overdrive) {
      var p = 0.10 + 0.06 * Math.sin(this.time * 5);
      ctx.strokeStyle = 'rgba(255,60,160,' + p.toFixed(3) + ')';
      ctx.lineWidth = 26;
      ctx.strokeRect(0, 0, W, H);
    }
  };

  window.BreakoutGame = Game;
})();
