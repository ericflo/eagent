/* OVERDRIVE — js/game.js
 * window.Game: the entire game. State machine MENU -> COUNTDOWN -> PLAY ->
 * LEVEL_CLEAR -> (next) ... -> GAMEOVER -> MENU. Pause (P / button /
 * auto-pause on hidden). Arena physics with substeps, circle-vs-rect brick
 * collisions with per-axis resolution, paddle physics (x AND y), all brick
 * types, abilities, combo, POWER tier, OVERDRIVE, score/lives, canvas HUD.
 * Classic script. Assumes CFG/Input/AudioSys/FX/Levels at call time.
 */
(function () {
  'use strict';

  var CFG = null;         // resolved in init (window.CFG)
  var TAU = Math.PI * 2;

  function clamp(v, a, b) { return v < a ? a : (v > b ? b : v); }
  function rnd(a, b) { return a + Math.random() * (b - a); }
  function dist2(x1, y1, x2, y2) { var dx = x2 - x1, dy = y2 - y1; return dx * dx + dy * dy; }

  // per-brick symbol → color / behavior
  var BRICK_COLORS = {
    N: '#b44cff', A: '#7df9ff', S: '#ffe066', G: '#ffd23f', T: '#8a93a8', B: '#ff5f7a'
  };
  var CAP_COLORS = {
    L: '#ff5f7a', M: '#ffd23f', E: '#7df9ff', C: '#9fe8ff', S: '#7ab8ff',
    MAG: '#c07dff', G: '#ffb01f', '1': '#7dff8f',
    FIRE: '#ff6a00', FROST: '#9fe8ff', BLITZ: '#ffe066', SPLIT: '#b7aaff', PULSE: '#ff8ad8'
  };

  window.Game = {
    canvas: null, ctx: null,
    state: 'MENU', stateTime: 0,
    W: 720, H: 1100, dpr: 1,
    levelIndex: 0, levelData: null, levelName: '', levelSub: '',
    baseSpeed: 560, bricks: [], brickGrid: null, rowsDone: 0,
    paddle: null, balls: [], capsules: [], lasers: [],
    score: 0, hi: 0, lives: 3, combo: 0, comboT: 0,
    powerTier: 1, od: null,
    abilities: null,       // paddle ability timers
    paused: false, muted: false,
    time: 0, fps: 60, _fpsAcc: 0, _fpsN: 0,
    _lastTime: 0,
    _clear: null, _count: null, _over: null,
    _laserTimer: 0,
    _everCleared: 0, _odEv: false,
    total: 0,

    // ==========================================================================
    // INIT
    // ==========================================================================
    init: function (canvas) {
      CFG = window.CFG;
      this.CFG = CFG;           // exposed for Input touch sensitivity etc.
      this.canvas = canvas;
      this.ctx = canvas.getContext('2d');
      this.W = CFG.W;
      this.H = CFG.H_MIN;
      this.od = {
        active: false, tier: 0, drain: 0, sustaining: false,
        emberT: 0, rainT: 0, nextTier: 0, bonusBalls: 0, ever: false, lastTop: 0
      };

      this.abilities = { laser: 0, expand: 0, catch: 0, slow: 0, mag: 0, gold: 0 };
      try { this.hi = parseInt(localStorage.getItem('od_hi') || '0', 10) || 0; } catch (e) { this.hi = 0; }
      this.loadLevel(0);
      this.resetPaddle();
      this.state = 'MENU';
      this._lastTime = performance.now();
      Events.emit('ready', {});
    },

    // ---- logical mapping ------------------------------------------------------
    logicalW: function () { return this.W; },
    logicalH: function () { return this.H; },

    resize: function (cssW, cssH) {
      var newH = clamp(this.W * (cssH / cssW), CFG.H_MIN, CFG.H_MAX);
      this.H = newH;
      this.dpr = window.devicePixelRatio || 1;
      // rebuild brick positions from level rows (alive flags preserved)
      var alive = {};
      for (var i = 0; i < this.bricks.length; i++) {
        var b = this.bricks[i];
        if (b.alive) alive[b.row + '_' + b.col] = b;
      }
      this.buildBricks(alive);
      if (this.paddle) { this.resetPaddle(); }
      if (window.FX) window.FX.resize(this.W, this.H);
    },

    // ==========================================================================
    // LEVELS
    // ==========================================================================
    loadLevel: function (index) {
      var data = window.Levels.get(index);
      this.levelIndex = index;
      this.levelData = data;
      this.levelName = data.name;
      this.levelSub = data.sub;
      this.baseSpeed = Math.min(CFG.BALL_SPEED_CAP, CFG.BALL_BASE_SPEED * Math.pow(1 + CFG.BALL_SPEED_PER_LEVEL, index));
      this.buildBricks(null);
      this.resetPaddle();
      this.balls.length = 0;      // fresh set of balls for the new level
      this.spawnBall(true);
      this.combo = 0; this.comboT = 0;
      this.od.active = false; this.od.tier = 0; this.od.drain = 0; this.od.bonusBalls = 0;
      this.capsules.length = 0;
      this.lasers.length = 0;
      this._laserTimer = 0;
      this._steelRescueT = 0;
      this.balls.forEach(function (b) { if (b.ability) { b.ability = null; } });
    },

    buildBricks: function (carry) {
      var rows = this.levelData.rows;
      var availH = this.H * 0.5 - CFG.BRICK_TOP;
      // fit ALL rows between the HUD and the paddle band by adjusting brick height
      var brickH = Math.min(this._brickH(), Math.max(22, (availH - (rows.length - 1) * CFG.BRICK_ROW_GAP) / rows.length));
      this.rowsDone = rows.length;
      var brickW = this.W / CFG.COLS - CFG.BRICK_GAP;
      this.bricks = [];
      this.brickGrid = {};
      this.colorIdx = 0;
      for (var r = 0; r < rows.length; r++) {
        var y = CFG.BRICK_TOP + r * (brickH + CFG.BRICK_ROW_GAP);
        var line = rows[r];
        for (var c = 0; c < CFG.COLS; c++) {
          var ch = line[c];
          if (ch === '.') continue;
          var x = CFG.BRICK_GAP / 2 + c * (brickW + CFG.BRICK_GAP);
          var key = r + '_' + c;
          var old = carry ? carry[key] : null;
          this.bricks.push({
            type: ch, row: r, col: c, x: x, y: y, w: brickW, h: brickH,
            alive: true, frozen: old ? !!old.frozen : false, wob: old ? old.wob : rnd(0, TAU), hue: rnd(0, 360)
          });
          this.brickGrid[key] = this.bricks[this.bricks.length - 1];
        }
      }
    },

    _brickH: function () { return CFG.BRICK_H * clamp(this.H / 1150, 0.78, 1.15); },

    // ---- paddle / balls --------------------------------------------------------
    resetPaddle: function () {
      var cx = this.W / 2;
      var cy = this.H * CFG.PADDLE_START_Y;
      if (this.paddle) {
        this.paddle.x = clamp(this.paddle.x, this._padMinX(), this._padMaxX());
        this.paddle.y = clamp(this.paddle.y, this._bandTop(), this._bandBottom());
        this.paddle.vx = 0; this.paddle.vy = 0;
      } else {
        this.paddle = { x: cx, y: cy, vx: 0, vy: 0, w: CFG.PADDLE_W, h: CFG.PADDLE_H, targetX: cx, targetY: cy, catchTimer: 0 };
      }
      this.paddle.w = CFG.PADDLE_W * (this.abilities.expand > 0 ? CFG.PADDLE_EXPAND : 1);
    },

    _padHalf: function () { return this.paddle.w / 2; },
    _bandTop: function () { return this.H * CFG.PADDLE_BAND_TOP; },
    _bandBottom: function () { return this.H * CFG.PADDLE_BAND_BOT; },
    _padMinX: function () { return this._padHalf() + 8; },
    _padMaxX: function () { return this.W - this._padHalf() - 8; },

    spawnBall: function (caught) {
      var b = {
        x: this.paddle.x, y: this.paddle.y - CFG.BALL_R - this.paddle.h / 2 - 2,
        vx: 0, vy: 0, r: CFG.BALL_R,
        caught: !!caught, ability: null, abilityT: 0, ember: false, seed: rnd(0, 100)
      };

      this.balls.push(b);
      return b;
    },

    // ==========================================================================
    // STATE MACHINE
    // ==========================================================================
    startMenu: function () {
      this.state = 'MENU';
      this.stateTime = 0;
    },

    startCountdown: function () {
      this.state = 'COUNTDOWN';
      this.stateTime = 0;
      this._count = { t: 0, dur: 2.8, text: '' };
    },

    startPlay: function () {
      this.state = 'PLAY';
      this.stateTime = 0;
      this._laserTimer = 0;
      // auto-launch any caught balls so play begins (fresh level / run start)
      for (var i = 0; i < this.balls.length; i++) {
        var b = this.balls[i];
        if (b.caught) {
          b.caught = false;
          var up = -Math.PI / 2 + rnd(-0.32, 0.32);
          var sp = this.baseSpeed * (b.ability === 'blitz' ? 2 : 1.02);
          b.vx = Math.cos(up) * sp;
          b.vy = Math.sin(up) * sp;
        }
      }
    },

    gameOver: function () {
      this.state = 'GAMEOVER';
      this.stateTime = 0;
      this._over = { t: 0 };
      // register best
      if (this.score > this.hi) {
        this.hi = this.score;
        try { localStorage.setItem('od_hi', String(this.hi)); } catch (e) {}
      }
      window.AudioSys.gameOver();
      window.FX.clear();
    },

    // pause / mute entry points used by Input + buttons
    togglePause: function () {
      if (this.state === 'PLAY' || this.state === 'COUNTDOWN') {
        this.paused = !this.paused;
        window.AudioSys.uiClick();
      } else if (this.state === 'MENU' || this.state === 'GAMEOVER') {
        window.AudioSys.uiClick();
      }
    },
    toggleMute: function () {
      this.muted = window.AudioSys.toggleMute();
      return this.muted;
    },
    hitPause: function (lx, ly) {
      var r = this._pauseRect();
      return lx >= r.x && lx <= r.x + r.w && ly >= r.y && ly <= r.y + r.h;
    },
    hitMute: function (lx, ly) {
      var r = this._muteRect();
      return lx >= r.x && lx <= r.x + r.w && ly >= r.y && ly <= r.y + r.h;
    },
    _pauseRect: function () { return { x: this.W - 60, y: 10, w: 50, h: 50 }; },
    _muteRect: function () { return { x: this.W - 60, y: 66, w: 50, h: 50 }; },

    // ==========================================================================
    // SCORING
    // ==========================================================================
    odMult: function () {
      if (!CFG) return 1;
      var t = this.od ? clamp(this.od.tier, 0, 4) : 0;
      return CFG.OD_MULTIS[t];
    },
    displayMult: function () {
      var m = this.combo + (this.od && this.od.active ? this.od.tier : 0) + (this.abilities.gold > 0 ? 1 : 0);
      return m;
    },
    addScore: function (base, x, y, label) {
      var mult = Math.max(1, this.combo) * this.odMult() * (this.abilities.gold > 0 ? 2 : 1);
      var pts = Math.round(base * mult);
      this.score += pts;
      if (x !== undefined && y !== undefined) {
        window.FX.popup(x, y, (label || '+' + pts), this.abilities.gold > 0 ? '#ffd23f' : '#fff', pts >= 5000 ? 26 : 19);
      }
      return pts;
    },
    bumpCombo: function () {
      this.combo = Math.min(CFG.COMBO_MAX, this.combo + 1);
      this.comboT = CFG.COMBO_WINDOW;
    },

    // ==========================================================================
    // CAPSULE SYSTEM
    // ==========================================================================
    maybeDrop: function (brick) {
      var chance = CFG.DROP[brick.type] || 0;
      if (brick.type !== 'N') chance += CFG.DROP_SPECIAL_BONUS;
      if (Math.random() < chance) this.dropCapsule(brick.x + brick.w / 2, brick.y + brick.h / 2, brick.type === 'G');
    },

    dropCapsule: function (x, y, double) {
      var kind = this._pickCapsuleKind();
      this.capsules.push(this._mkCapsule(x, y, kind));
      if (double) {
        var k2 = this._pickCapsuleKind();
        if (k2 !== kind) this.capsules.push(this._mkCapsule(x + 14, y + 6, k2));
      }
    },

    _mkCapsule: function (x, y, kind) {
      return {
        kind: kind, x: x, y: y, vy: CFG.CAP_FALL_SPEED,
        wob: rnd(0, TAU), t: 0, glow: 0
      };

    },

    _pickCapsuleKind: function () {
      var r = Math.random();
      // ~40% ball abilities, ~60% paddle abilities (weighted)
      if (r < 0.40) {
        return CFG.BALL_ABILITIES[Math.floor(Math.random() * CFG.BALL_ABILITIES.length)];
      }
      var pool = ['LASER', 'LASER', 'MULTI', 'MULTI', 'EXPAND', 'EXPAND', 'CATCH', 'CATCH', 'SLOW', 'MAGNET', 'GOLDRUSH', 'LIFE'];
      var w = [3, 3, 2, 2, 2, 2, 2, 2, 1.4, 1.4, 1.4, 0.8];
      var tot = 0, i;
      for (i = 0; i < w.length; i++) tot += w[i];
      var rr = Math.random() * tot;
      for (i = 0; i < w.length; i++) { rr -= w[i]; if (rr <= 0) return pool[i]; }
      return 'LASER';
    },

    applyCapsule: function (cap) {
      var kind = cap.kind;
      var A = this.abilities;
      window.AudioSys.capsuleCatch();
      var label = { LASER: 'LASER', MULTI: 'MULTIBALL', EXPAND: 'EXPAND', CATCH: 'CATCH', SLOW: 'SLOW', MAGNET: 'MAGNET', GOLDRUSH: 'GOLDRUSH', LIFE: '1UP', FIRE: 'FIRE BALL', FROST: 'FROST', BLITZ: 'BLITZ', SPLIT: 'SPLIT', PULSE: 'PULSE' }[kind] || kind;
      window.FX.popup(this.paddle.x, this.paddle.y - 30, label, CAP_COLORS[kind] || '#fff', 22);

      if (CFG.PADDLE_ABILITIES.indexOf(kind) >= 0) {
        if (kind === 'MULTI') {
          var n = this.balls.filter(function (b) { return b.caught; });
          for (var i = 0; i < 2 && this.balls.length < CFG.MULTIBALL_CAP; i++) {
            var nb = this.spawnBall(false);
            nb.x = this.paddle.x + rnd(-40, 40);
            nb.y = this.paddle.y - 22;
            var a = rnd(-0.7, 0.7) - Math.PI / 2;
            var sp = this.baseSpeed * 0.9;
            nb.vx = Math.cos(a) * sp; nb.vy = Math.sin(a) * sp;
          }
        } else if (kind === 'LIFE') {
          if (this.lives < CFG.LIVES_MAX) { this.lives++; window.AudioSys.oneUp(); }
          this.bumpCombo();
        } else if (kind === 'CATCH') {
          A.catch = 1; // will deactivate after one catch+release
        } else if (kind === 'GOLDRUSH') {
          A.gold = CFG.ABILITY_TIMES.gold;
        } else if (kind === 'LASER') {
          A.laser = CFG.ABILITY_TIMES.laser;
          this._laserTimer = 0;
        } else if (kind === 'EXPAND') {
          A.expand = CFG.ABILITY_TIMES.expand;
          this.paddle.w = CFG.PADDLE_W * CFG.PADDLE_EXPAND;
        } else if (kind === 'SLOW') {
          if (!A.slow) {
            this.balls.forEach(function (b) { if (!b.caught) { b.vx *= CFG.SLOW_FACTOR; b.vy *= CFG.SLOW_FACTOR; } });
          }
          A.slow = CFG.ABILITY_TIMES.slow;
        } else if (kind === 'MAGNET') {
          A.mag = CFG.ABILITY_TIMES.mag;
        }
        window.AudioSys.abilityStart();
        this.bumpCombo();
      } else {
        // ball ability → apply to oldest ball without one
        var target = null;
        for (var j = 0; j < this.balls.length; j++) {
          if (!this.balls[j].ability) { target = this.balls[j]; break; }
        }
        if (!target) target = this.balls[0];
        if (target) {
          target.ability = kind.toLowerCase();
          target.abilityT = CFG.ABILITY_TIMES.ball;
          window.AudioSys.abilityStart();
        } else {
          window.FX.popup(this.paddle.x, this.paddle.y - 52, 'NO BALL!', '#888', 16);
        }
      }
    },

    // ==========================================================================
    // BRICK BREAKING
    // ==========================================================================
    _breakBrick: function (brick, cause) {
      if (!brick.alive) return;
      brick.alive = false;
      var cx = brick.x + brick.w / 2, cy = brick.y + brick.h / 2;
      var col = BRICK_COLORS[brick.type];
      window.FX.brickShatter(cx, cy, col, brick.w, brick.h);
      window.AudioSys.brickBreak(this.combo, this.od.tier, brick.type);
      var pts = CFG.SCORE[brick.type] || 50;
      if (cause === 'laser') pts = Math.round(pts * CFG.LASER_SCORE);
      this.addScore(pts, cx, cy);
      this.bumpCombo();

      if (brick.type === 'G') {
        window.FX.coinSparkles(cx, cy);
        this.dropCapsule(cx, cy, true);
      } else {
        this.maybeDrop(brick);
      }

      if (brick.type === 'B') {
        this._detonate(brick.row, brick.col);
      } else if (brick.frozen) {
        window.FX.freezeBurst(cx, cy);
      }
      if (brick.frozen && this._frostN > 0) this._frostN--;

      // split-on-hit
      if (cause === 'ball' && this._hitBall && this._hitBall.ability === 'split' && this.balls.length < CFG.SPLIT_CAP) {
        var b = this._hitBall;
        var a = rnd(0, TAU);
        var sp = Math.max(180, Math.min(900, Math.hypot(b.vx, b.vy) * 0.55));
        var nb = {
          x: b.x, y: b.y, vx: Math.cos(a) * sp, vy: Math.sin(a) * sp, r: b.r,
          caught: false, ability: b.ability, abilityT: b.abilityT, ember: b.ember, seed: rnd(0, 100)
        };
        this.balls.push(nb);
        b.vx *= 0.5; b.vy *= 0.5;
        window.FX.popup(cx, cy, 'SPLIT!', '#b7aaff', 18);
      }
    },

    _detonate: function (row, col) {
      window.AudioSys.explosion();
      var cx = this.brickGrid[row + '_' + col];
      cx = cx ? cx.x + cx.w / 2 : this.W / 2;
      var cy = this.brickGrid[row + '_' + col] ? this.brickGrid[row + '_' + col].y + this.brickGrid[row + '_' + col].h / 2 : this.H / 2;
      window.FX.explosion(cx, cy);
      for (var dr = -1; dr <= 1; dr++) {
        for (var dc = -1; dc <= 1; dc++) {
          if (dr === 0 && dc === 0) continue;
          var key = (row + dr) + '_' + (col + dc);
          var nb = this.brickGrid[key];
          if (nb && nb.alive && nb.type !== 'T') {
            this._breakBrick(nb, 'ball');
          }
        }
      }
    },

    // ==========================================================================
    // COLLISIONS
    // ==========================================================================
    _reflectBall: function (ball, nx, ny) {
      var d = nx * ball.vx + ny * ball.vy;
      if (d < 0) { ball.vx -= 2 * d * nx; ball.vy -= 2 * d * ny; }
    },

    _ballVsBrick: function (ball) {
      for (var i = 0; i < this.bricks.length; i++) {
        var b = this.bricks[i];
        if (!b.alive) continue;
        var hit = this._circleRect(ball.x, ball.y, ball.r, b.x, b.y, b.w, b.h);
        if (!hit) continue;

        // determine impact face from previous position
        var prevX = ball.px, prevY = ball.py;
        var face = 'left';
        var minD = 1e9;
        var faces = {
          left: Math.abs(prevX - (b.x - ball.r)),
          right: Math.abs(prevX - (b.x + b.w + ball.r)),
          top: Math.abs(prevY - (b.y - ball.r)),
          bottom: Math.abs(prevY - (b.y + b.h + ball.r))
        };
        for (var f in faces) { if (faces[f] < minD) { minD = faces[f]; face = f; } }

        var nx = 0, ny = 0;
        if (face === 'left') nx = -1; else if (face === 'right') nx = 1;
        else if (face === 'top') ny = -1; else ny = 1;
        var fromAbove = face === 'top';

        var sp = Math.hypot(ball.vx, ball.vy);
        var qualifies = false;
        var passThrough = ball.ability === 'fire';

        if (b.type === 'T') {
          if (passThrough) qualifies = true;
          else {
            window.AudioSys.steelHit();
            window.FX.sparks(ball.x, ball.y, '#aab2c4', 6);
            this._reflectBall(ball, nx, ny);
            return 'bounce';
          }
        } else if (b.frozen) {
          qualifies = true; // brittle
        } else if (b.type === 'A') {
          var steepEnough;
          if (fromAbove) steepEnough = Math.abs(ball.vx) / Math.max(60, sp) <= CFG.ANGLE_TAN;
          else steepEnough = Math.abs(ball.vy) / Math.max(60, sp) >= Math.cos(CFG.ANGLE_DEG * Math.PI / 180);
          qualifies = passThrough || steepEnough;
        } else if (b.type === 'S') {
          qualifies = passThrough || sp >= this.baseSpeed * CFG.SPEED_FACTOR;
        } else {
          qualifies = true;
        }

        this._hitBall = ball;
        this._hitBrickRow = b.row; this._hitBrickCol = b.col;

        if (qualifies) {
          if (ball.ability === 'pulse' && b.type !== 'T') {
            window.FX.shockwave(ball.x, ball.y, 12, '#ff8ad8');
            this._crackNearby(b.row, b.col);
          }
          if (ball.ability === 'frost') {
            window.FX.freezeBurst(ball.x, ball.y);
            window.AudioSys.freeze();
            this._freezeRegion(b.row, b.col);
          }
          this._breakBrick(b, 'ball');
          if (!passThrough) this._reflectBall(ball, nx, ny);
          return 'break';
        } else {
          window.AudioSys.specialMiss();
          window.FX.sparks(ball.x, ball.y, '#889', 5);
          // throttle popup chatter
          if (!this._missPopupT || this.time - this._missPopupT > 0.5) {
            this._missPopupT = this.time;
            window.FX.popup(ball.x, ball.y - 12, b.type === 'S' ? 'TOO SLOW!' : 'NEED STEEP!', '#9aa', 13);
          }
          this._reflectBall(ball, nx, ny);
          return 'bounce';
        }
      }
      return null;
    },

    _freezeRegion: function (r, c) {
      this._frostN = 0;
      for (var dr = -1; dr <= 1; dr++) {
        for (var dc = -1; dc <= 1; dc++) {
          var key = (r + dr) + '_' + (c + dc);
          var b = this.brickGrid[key];
          if (b && b.alive && b.type !== 'T' && !b.frozen) { b.frozen = true; this._frostN++; }
        }
      }
    },

    _crackNearby: function (r, c) {
      for (var dr = -1; dr <= 1; dr++) {
        for (var dc = -1; dc <= 1; dc++) {
          if (dr === 0 && dc === 0) continue;
          var key = (r + dr) + '_' + (c + dc);
          var b = this.brickGrid[key];
          if (b && b.alive && b.type !== 'T' && b.type !== 'B') {
            window.FX.sparks(b.x + b.w / 2, b.y + b.h / 2, '#ff8ad8', 3);
            if (b.type === 'N') {
              // shockwave cracks normal bricks in one hit
              this._breakBrick(b, 'pulse');
            }
          }
        }
      }
    },

    // ==========================================================================
    // PADDLE VS BALL
    // ==========================================================================
    _paddleVsBalls: function () {
      var p = this.paddle;
      var A = this.abilities;
      for (var i = 0; i < this.balls.length; i++) {
        var b = this.balls[i];
        if (b.caught || b.dead) continue;
        // magnet pull
        if (A.mag > 0) {
          var d = Math.sqrt(dist2(b.x, b.y, p.x, p.y - 20));
          if (d < CFG.MAG_RANGE && d > 1) {
            var pull = (1 - d / CFG.MAG_RANGE) * CFG.MAG_PULL * this._dt;
            b.vx -= (b.x - p.x) / d * pull;
            b.vy -= (b.y - (p.y - 20)) / d * pull;
          }
        }
        var px = p.x - p.w / 2 - b.r, py = p.y - p.h / 2 - b.r;
        var pw = p.w + b.r * 2, ph = p.h + b.r * 2;
        if (b.x >= px && b.x <= px + pw && b.y >= py && b.y <= py + ph) {
          if (this._dt > 0) {
            if (A.catch > 0) {
              b.caught = true; b.vx = 0; b.vy = 0;
              A.catch = 0;
              window.AudioSys.capsuleCatch();
              window.FX.popup(p.x, p.y - 30, 'CATCH!', '#9fe8ff', 18);
            } else {
              this._paddleBounce(b);
            }
          }
          if (!b.caught && b.y <= p.y) b.y = p.y - p.h / 2 - b.r - 1;
        }
      }
      for (var j = 0; j < this.balls.length; j++) {
        var cb = this.balls[j];
        if (cb.caught) {
          cb.x = p.x; cb.y = p.y - CFG.BALL_R - p.h / 2 - 2;
        }
      }
    },

    _paddleBounce: function (b) {
      var p = this.paddle;
      var base = this.baseSpeed;
      var rel = clamp((b.x - p.x) / (p.w / 2), -1, 1);
      var ang = rel * CFG.PADDLE_BOUNCE_ANGLE * Math.PI / 180;
      var sp = Math.max(420, Math.hypot(b.vx, b.vy));
      if (this.abilities.slow > 0) sp = Math.min(sp, base * 0.85);
      var nx = Math.sin(ang), ny = -Math.cos(ang);
      b.vx = nx * sp + p.vx * CFG.PADDLE_ADD_VX;
      b.vy = ny * sp + p.vy * CFG.PADDLE_ADD_VY;
      // cap boost at 1.6x base speed
      var total = Math.hypot(b.vx, b.vy);
      var boostMax = base * CFG.BALL_BOOST_CAP;
      if (p.vy < 0 && total > boostMax) {
        b.vx *= boostMax / total;
        b.vy *= boostMax / total;
      }
      if (b.ability === 'blitz') { b.vx *= 2; b.vy *= 2; }
      window.AudioSys.paddleHit(this.combo, p.vy < 0);
      window.FX.sparks(b.x, b.y, '#7df9ff', 8);
      this.bumpCombo();
    },

    // ==========================================================================
    // LASERS
    // ==========================================================================
    _updateLasers: function (dt) {
      var A = this.abilities;
      if (A.laser <= 0) { this.lasers.length = 0; return; }
      this._laserTimer -= dt;
      if (this._laserTimer <= 0) {
        this._laserTimer = 0.7;
        this._fireLasers();
      }
      for (var i = this.lasers.length - 1; i >= 0; i--) {
        var L = this.lasers[i];
        L.h -= 1300 * dt;
        if (L.h <= 0 || L.hit) { this.lasers.splice(i, 1); continue; }
        for (var j = 0; j < this.bricks.length; j++) {
          var b = this.bricks[j];
          if (!b.alive) continue;
          if (L.tx + 4 > b.x && L.tx - 4 < b.x + b.w && L.h > b.y && L.h < b.y + b.h) {
            L.hit = true;
            this._laserBreak(b, L.tx, L.h);
            break;
          }
        }
      }
    },

    _fireLasers: function () {
      var p = this.paddle;
      var offs = [p.w / 2 - 8, -p.w / 2 + 8];
      for (var i = 0; i < offs.length; i++) {
        this.lasers.push({ tx: p.x + offs[i], h: p.y - p.h / 2 - 4, hit: false });
        window.FX.beam(p.x + offs[i], p.y - p.h / 2, p.x + offs[i], 60, '#ff5f7a');
        window.AudioSys.laserZap();
      }
    },

    _laserBreak: function (b, tx, hy) {
      if (!b.alive) return;
      window.FX.laserFlash(tx, hy);
      this._hitBall = null;
      this._hitBrickRow = b.row; this._hitBrickCol = b.col;
      this._breakBrick(b, 'laser');
      if (b.type === 'B') this._detonate(b.row, b.col);
    },

    // ==========================================================================
    // OVERDRIVE
    // ==========================================================================
    _highestBrickTop: function () {
      var top = 1e9;
      for (var i = 0; i < this.bricks.length; i++) {
        if (this.bricks[i].alive && this.bricks[i].y < top) top = this.bricks[i].y;
      }
      return top === 1e9 ? -1 : top;
    },

    _ballOnTop: function (b, top) {
      return !b.caught && !b.dead && top >= 0 && b.y + b.r < top;
    },

    _updateOverdrive: function (dt) {
      var od = this.od;
      var top = this._highestBrickTop();
      if (top < 0) { od.active = false; return; }

      var onTop = false;
      for (var i = 0; i < this.balls.length; i++) {
        var b = this.balls[i];
        if (this._ballOnTop(b, top)) {
          onTop = true;
          if (!b.caught && b.ability !== 'blitz') {
            var sp = Math.hypot(b.vx, b.vy);
            if (sp > 0) {
              var target = Math.min(CFG.BALL_HARD_CAP, sp * (1 + CFG.OD_SPEED_RAMP * dt));
              b.vx *= target / sp; b.vy *= target / sp;
            }
          }
        }
      }

      if (od.drain > 0) {
        od.drain -= dt;
        // tier decays proportional to remaining drain time
        var frac = clamp(od.drain / CFG.OD_DRAIN_TIME, 0, 1);
        od.tier = Math.round(frac * od._tierAtDrain);
        if (od.drain <= 0) od.tier = 0;
      }

      if (onTop && !od.active && od.tier > 0) {
        // resume from drained tier
        this._triggerOverdrive(true);
      } else if (onTop && !od.active) {
        this._triggerOverdrive(false);
      }

      if (!onTop && od.active) {
        this._fallFromOverdrive();
      }

      if (od.active) {
        od.nextTier -= dt;
        if (od.nextTier <= 0 && od.tier < 4) {
          od.nextTier += CFG.OD_TIER_TIME;
          od.tier++;
          window.AudioSys.overdriveSting();
          window.FX.shake(5, 0.25);
          window.FX.flashScreen('255,80,120', 0.16);
          window.FX.popup(this.W / 2, this.H * 0.32, 'OVERDRIVE x' + CFG.OD_MULTIS[od.tier], '#ff5f7a', 34);
        }
        od.emberT -= dt;
        if (od.emberT <= 0) {
          od.emberT += CFG.OD_EMBER_TIME;
          if (od.bonusBalls < CFG.OD_MAX_BONUS_BALLS) {
            var e = {
              x: this.W / 2 + rnd(-60, 60), y: 70,
              vx: rnd(-260, 260), vy: rnd(140, 300), r: CFG.BALL_R,
              caught: false, ability: 'fire', abilityT: 6, ember: true, seed: rnd(0, 100)
            };
            this.balls.push(e);
            od.bonusBalls++;
            window.AudioSys.emberSpawn();
            window.FX.explosion(e.x, e.y);
          }
        }
        od.rainT -= dt;
        if (od.rainT <= 0) {
          od.rainT += CFG.OD_RAIN_TIME;
          var cap = this._mkCapsule(rnd(40, this.W - 40), 44, this._pickCapsuleKind());
          cap.vy = CFG.CAP_FALL_SPEED * 0.9;
          this.capsules.push(cap);
        }
      }
    },

    _triggerOverdrive: function (resume) {
      var od = this.od;
      od.active = true;
      od.drain = 0;
      od.emberT = CFG.OD_EMBER_TIME;
      od.rainT = CFG.OD_RAIN_TIME;
      od.nextTier = CFG.OD_TIER_TIME;
      window.AudioSys.overdriveSting();
      window.FX.flashScreen('255,120,180', 0.4);
      window.FX.shake(14, 0.4);
      window.FX.popup(this.W / 2, this.H * 0.34, resume ? 'OVERDRIVE! RESUMED' : 'OVERDRIVE!', '#ff5f7a', 44);
    },

    _fallFromOverdrive: function () {
      var od = this.od;
      if (!od.active) return;
      od.active = false;
      od.drain = CFG.OD_DRAIN_TIME;
      od._tierAtDrain = od.tier;
      window.AudioSys.specialMiss();
      window.FX.popup(this.W / 2, this.H * 0.44, 'OVERDRIVE DRAINING…', '#ff8ad8', 22);
    },

    _levelCleared: function () {
      this.state = 'LEVEL_CLEAR';
      this._clear = { t: 0 };
      var odActive = this.od.active;
      var bonus;
      if (odActive) {
        bonus = this.od.tier * this.balls.length * CFG.OVERDRIVE_CLEAR_MULT;
      } else {
        var mult = Math.max(1, this.combo, this.odMult());
        bonus = this.lives * mult * CFG.CLEAR_LIFE_MULT;
      }
      this._clearBonus = bonus;
      this.score += bonus;
      window.AudioSys.levelClear();
      window.FX.flashScreen('255,255,255', 0.5);
      window.FX.shake(10, 0.5);
      window.FX.popup(this.W / 2, this.H * 0.35, odActive ? 'BONUS TALLY +' + bonus : 'LEVEL CLEAR!', '#ffe066', 40);
    },

    // ==========================================================================
    // UPDATE
    // ==========================================================================
    update: function (dt) {
      this.time += dt;
      this.stateTime += dt;
      if (this.paused) { return; }

      if (this.combo > 0) {
        this.comboT -= dt;
        if (this.comboT <= 0) this.combo = 0;
      }

      if (window.Input.dragging) window.Input.markDrag();

      switch (this.state) {
        case 'MENU':
          if (window.Input.consumeLaunch()) { window.AudioSys.unlock(); this._beginRun(); }
          break;
        case 'COUNTDOWN':
          if (window.Input.consumeLaunch() && this._count.t > 1.2) this._count.t = 2.8; // snap to GO
          this._count.t += dt;
          var t = this._count.t;
          var targets = [[0.4, '3'], [1.0, '2'], [1.6, '1'], [2.2, 'GO!']];
          for (var i = 0; i < targets.length; i++) {
            if (t >= targets[i][0] && this._count.text !== targets[i][1]) {
              this._count.text = targets[i][1];
              window.AudioSys.countdownTick(targets[i][1] === 'GO!');
            }
          }
          if (t >= 2.8) this.startPlay();
          break;
        case 'PLAY':
          this._updatePlay(dt);
          break;
        case 'LEVEL_CLEAR':
          this._clear.t += dt;
          if ((window.Input.consumeLaunch() && this._clear.t > 1.0) || this._clear.t > 6) this._nextLevel();
          break;
        case 'GAMEOVER':
          this._over.t += dt;
          if (window.Input.consumeLaunch() && this._over.t > 0.8) this._restart();
          if (this._over.t > 25) this.startMenu();
          break;
      }
    },

    // ==========================================================================
    _beginRun: function () {
      this.score = 0;
      this.lives = CFG.LIVES_START;
      this.loadLevel(0);
      this.startCountdown();
      window.AudioSys.unlock();
      window.FX.clear();
      window.FX.shake(8, 0.3);
    },

    _restart: function () {
      this.score = 0;
      this.lives = CFG.LIVES_START;
      this.loadLevel(0);
      this.startCountdown();
      window.FX.clear();
    },

    _nextLevel: function () {
      var next = this.levelIndex + 1;
      if (next > 0 && next % CFG.LIFE_BONUS_EVERY === 0 && this.lives < CFG.LIVES_MAX) {
        this.lives++;
        window.AudioSys.oneUp();
        window.FX.popup(this.W / 2, this.H * 0.4, 'EXTRA LIFE!', '#7dff8f', 28);
      }
      this.loadLevel(next);
      this.startCountdown();
    },

    _updatePlay: function (dt) {
      this._dt = dt;
      var A = this.abilities;

      ['laser', 'expand', 'slow', 'mag', 'gold'].forEach(function (k) {
        if (A[k] > 0) A[k] -= dt;
      });

      var targetW = CFG.PADDLE_W * (A.expand > 0 ? CFG.PADDLE_EXPAND : 1);
      if (Math.abs(this.paddle.w - targetW) > 0.5) {
        this.paddle.w += (targetW - this.paddle.w) * Math.min(1, dt * 7);
        this.paddle.x = clamp(this.paddle.x, this._padMinX(), this._padMaxX());
      }

      this._updatePaddle(dt);

      // balls
      this._frostN = 0;
      var anyCaught = false;
      for (var i = 0; i < this.balls.length; i++) {
        var b = this.balls[i];
        if (b.caught) { anyCaught = true; }
        this._updateBall(b, dt);
      }
      // physically remove dead balls once, after the loop (avoids spliced
      // mid-iteration skipping the next ball's physics for a frame)
      for (var d = this.balls.length - 1; d >= 0; d--) {
        if (this.balls[d].dead) this.balls.splice(d, 1);
      }
      this._frostN = 0;

      // ball lost if none left (not caught, not dead-respawned)
      var inPlay = this.balls.filter(function (b) { return !b.dead; });
      if (inPlay.length === 0) {
        this._loseBall();
      } else if (!anyCaught) {
        // all in-play balls fell below? _updateBall marks dead; handled above.
      }

      this._updateOverdrive(dt);
      this._updateLasers(dt);
      this._updateCapsules(dt);
      this._steelRescue(dt);
      this._updatePower(dt);

      // level clear
      var breakable = this.bricks.filter(function (b) { return b.alive; });
      if (breakable.length === 0) this._levelCleared();

      window.AudioSys.setMusicState(this.od.tier, this.od.active);
      window.AudioSys.powerTier = this.powerTier;
      window.FX.setPower(this.powerTier, this.od.active);

      this._fpsAcc += dt; this._fpsN++;
      if (this._fpsAcc >= 0.5) { this.fps = Math.round(this._fpsN / this._fpsAcc); this._fpsAcc = 0; this._fpsN = 0; }
    },

    _updatePaddle: function (dt) {
      var p = this.paddle;
      var inp = window.Input;
      var ax = inp.axis();
      if (inp.active === 'keys' && (ax.x !== 0 || ax.y !== 0)) {
        p.targetX = p.x + ax.x * CFG.KEYBOARD_PADDLE_SPEED * dt;
        p.targetY = p.y + ax.y * CFG.KEYBOARD_PADDLE_SPEED * dt;
      } else if (inp.active === 'mouse') {
        p.targetX = inp.hoverX;
        p.targetY = clamp(inp.hoverY || p.y, this._bandTop(), this._bandBottom());
      } else if (inp.active === 'touch') {
        p.targetX = inp.targetX;
        p.targetY = clamp(inp.targetY, this._bandTop(), this._bandBottom());
      } else if (inp.active === 'none') {
        p.targetX = p.x; p.targetY = p.y;
      }
      p.targetX = clamp(p.targetX, this._padMinX(), this._padMaxX());
      p.targetY = clamp(p.targetY, this._bandTop(), this._bandBottom());

      var prevX = p.x, prevY = p.y;
      p.x += (p.targetX - p.x) * Math.min(1, CFG.PADDLE_SMOOTH * dt);
      p.y += (p.targetY - p.y) * Math.min(1, CFG.PADDLE_SMOOTH * dt);
      var dtSafe = Math.max(dt, 0.0001);
      p.vx = clamp((p.x - prevX) / dtSafe, -CFG.PADDLE_MAX_VX, CFG.PADDLE_MAX_VX);
      p.vy = clamp((p.y - prevY) / dtSafe, -CFG.PADDLE_MAX_VX, CFG.PADDLE_MAX_VX);
    },

    _updateBall: function (b, dt) {
      if (b.ability) {
        b.abilityT -= dt;
        if (b.abilityT <= 0) { b.ability = null; window.AudioSys.abilityEnd(); }
      }

      if (b.caught) {
        if (window.Input.consumeLaunch()) {
          b.caught = false;
          var up = -Math.PI / 2 + rnd(-0.4, 0.4);
          var sp = this.baseSpeed * (b.ability === 'blitz' ? 2 : 1.02);
          b.vx = Math.cos(up) * sp;
          b.vy = Math.sin(up) * sp;
          window.AudioSys.paddleHit(this.combo, true);
          window.FX.sparks(b.x, b.y, '#7df9ff', 8);
        }
        return;
      }

      var slow = this.abilities.slow > 0 && b.ability !== 'blitz';
      var spd = Math.hypot(b.vx, b.vy);
      var target = slow ? this.baseSpeed * 0.9 : this.baseSpeed * 1.35;
      // decay toward base*1.35 unless overdriving (OD ramps itself) or blitzing
      if (spd > target && !b.dead && !this.od.active && b.ability !== 'blitz') {
        var decay = Math.pow(0.4, dt);
        b.vx *= decay; b.vy *= decay;
      }
      spd = Math.hypot(b.vx, b.vy);

      var brickW = this.W / CFG.COLS - CFG.BRICK_GAP;
      var maxStep = Math.max(5, Math.min(brickW, CFG.BRICK_H) * 0.6);
      var steps = Math.min(4, Math.max(1, Math.ceil(spd * dt / maxStep)));
      var sub = dt / steps;

      for (var s = 0; s < steps; s++) {
        if (b.dead) break;
        b.px = b.x; b.py = b.y;
        b.x += b.vx * sub;
        b.y += b.vy * sub;

        if (b.x - b.r < 0) { b.x = b.r; b.vx = Math.abs(b.vx); window.AudioSys.wall(); }
        if (b.x + b.r > this.W) { b.x = this.W - b.r; b.vx = -Math.abs(b.vx); window.AudioSys.wall(); }
        if (b.y - b.r < 0) { b.y = b.r; b.vy = Math.abs(b.vy); window.AudioSys.wall(); }
        if (b.y - b.r > this.H + 40) { b.dead = true; break; }

        this._ballVsBrick(b);

        if (b.y + b.r > this._bandTop() - 24) {
          this._paddleVsBalls();
        }
      }

      // trails
      if (b.ability === 'fire') window.FX.trail(b.x, b.y, 'fire', b.vx, b.vy);
      else if (b.ability === 'frost') window.FX.trail(b.x, b.y, 'ice', b.vx, b.vy);
      else if (b.ability === 'blitz') window.FX.trail(b.x, b.y, 'electric', b.vx, b.vy);
      else if (b.ember) window.FX.trail(b.x, b.y, 'ember', b.vx, b.vy);
    },

    _loseBall: function () {
      var anyCaught = this.balls.some(function (b) { return b.caught; });
      if (anyCaught) return;
      this._fallFromOverdrive();
      this.lives--;
      window.AudioSys.ballLost();
      window.FX.flashScreen('255,60,60', 0.25);
      window.FX.shake(8, 0.3);
      this.combo = 0; this.comboT = 0;
      if (this.lives <= 0) {
        this.balls.length = 0;
        this.capsules.length = 0;
        this.gameOver();
        return;
      }
      this.spawnBall(true);
    },

    _updateCapsules: function (dt) {
      for (var i = this.capsules.length - 1; i >= 0; i--) {
        var c = this.capsules[i];
        c.t += dt;
        c.x += Math.sin(c.t * 3 + c.wob) * 16 * dt;
        c.y += c.vy * dt;
        var p = this.paddle;
        var cw = CFG.CAP_W / 2 + 6, ch = CFG.CAP_H / 2 + 6;
        if (c.x > p.x - p.w / 2 - cw && c.x < p.x + p.w / 2 + cw &&
            c.y > p.y - p.h / 2 - ch && c.y < p.y + p.h / 2 + ch) {
          this.applyCapsule(c);
          this.capsules.splice(i, 1);
          continue;
        }
        if (c.y - CFG.CAP_H > this.H + 20) this.capsules.splice(i, 1);
      }
    },

    // Anti-softlock: if the only breakables left are STEEL and we hold no
    // laser/fire, rain a rescue capsule from the top so the player always
    // has a way through. (Steel is never destroyed by plain ball bounces.)
    _steelRescue: function (dt) {
      if (this.state !== 'PLAY') return;
      var hasLaser = this.abilities.laser > 0;
      var hasFire = this.balls.some(function (b) { return b.ability === 'fire'; });
      if (hasLaser || hasFire) return;
      var steel = 0, other = 0, i;
      for (i = 0; i < this.bricks.length; i++) {
        if (!this.bricks[i].alive) continue;
        if (this.bricks[i].type === 'T') steel++;
        else other++;
      }
      if (steel === 0 || other > 0) return;
      // no clearable non-steel brick left; offer a way to break steel
      this._steelRescueT -= dt;
      if (this._steelRescueT <= 0) {
        this._steelRescueT = 2.0;
        window.FX.popup(this.W / 2, this.H * 0.3, 'STEEL ONLY — TAKE THE TOOL!', '#ff8ad8', 17);
        var kind = (Math.random() < 0.75) ? 'LASER' : 'FIRE';
        var cap = this._mkCapsule(rnd(120, this.W - 120), 46, kind);
        cap.vy = CFG.CAP_FALL_SPEED * 0.95;
        this.capsules.push(cap);
      }
    },

    _updatePower: function (dt) {
      var A = this.abilities;
      var abilityCount = 0;
      for (var k in A) { if (A[k] > 0) abilityCount++; }
      if (this.balls.some(function (b) { return b.ability; })) abilityCount++;
      var tier = Math.max(this.combo / 6, this.od.active ? this.od.tier : 0, abilityCount);
      tier = Math.max(1, Math.min(5, Math.ceil(tier)));
      if (this.powerTier < tier) this.powerTier = tier;       // instant climb
      else if (this.powerTier > tier) this.powerTier = Math.max(tier, this.powerTier - dt * 1.5);
    },

    // ==========================================================================
    // DRAWING
    // ==========================================================================
    draw: function () {
      var ctx = this.ctx;
      // device-pixel transform: logical space → device pixels
      var sx = this.canvas.width / this.W, sy = this.canvas.height / this.H;
      ctx.setTransform(sx, 0, 0, sy, 0, 0);
      // background
      window.FX.drawBackground(ctx, this);
      // world + post fx
      window.FX.drawScene(ctx, this);
      // HUD
      this.drawHUD(ctx);
      // overlays by state
      if (this.paused && (this.state === 'PLAY' || this.state === 'COUNTDOWN')) this.drawPause(ctx);
      if (this.state === 'MENU') this.drawMenu(ctx);
      if (this.state === 'GAMEOVER') this.drawGameOver(ctx);
      if (this.state === 'COUNTDOWN') this.drawCountdown(ctx);
      if (this.state === 'LEVEL_CLEAR') this.drawLevelClear(ctx);
    },

    drawWorld: function (ctx) {
      // bricks
      for (var i = 0; i < this.bricks.length; i++) {
        this._drawBrick(ctx, this.bricks[i]);
      }
      // capsules
      for (var j = 0; j < this.capsules.length; j++) this._drawCapsule(ctx, this.capsules[j]);
      // lasers
      for (var k = 0; k < this.lasers.length; k++) {
        var L = this.lasers[k];
        if (!L.hit) {
          ctx.strokeStyle = '#fff';
          ctx.lineWidth = 2;
          ctx.globalAlpha = 0.9;
          ctx.beginPath();
          ctx.moveTo(L.tx, this.paddle.y - this.paddle.h / 2);
          ctx.lineTo(L.tx, L.h);
          ctx.stroke();
          ctx.globalAlpha = 1;
        }
      }
      // paddle
      this._drawPaddle(ctx);
      // balls
      for (var m = 0; m < this.balls.length; m++) this._drawBall(ctx, this.balls[m]);
      // overdrive zone marker (subtle)
      var top = this._highestBrickTop();
      if (top >= 0 && this.state === 'PLAY') {
        ctx.strokeStyle = 'rgba(255,95,122,0.25)';
        ctx.setLineDash([6, 8]);
        ctx.beginPath();
        ctx.moveTo(0, top); ctx.lineTo(this.W, top);
        ctx.stroke();
        ctx.setLineDash([]);
      }
    },

    _drawBrick: function (ctx, b) {
      if (!b.alive) return;
      var x = b.x, y = b.y, w = b.w, h = b.h;
      var col = BRICK_COLORS[b.type] || '#888';

      // rounded-ish body
      ctx.fillStyle = col;
      if (b.type === 'T') ctx.fillStyle = '#5a6478';
      // slight per-brick hue wobble
      var bright = ctx.createLinearGradient(x, y, x, y + h);
      bright.addColorStop(0, this._lighten(col, 45));
      bright.addColorStop(1, col);
      ctx.fillStyle = bright;

      if (b.wob > 0 && (b.type === 'G' || b.crack)) {
        var wob = Math.sin(this.time * 6 + b.wob) * 1.5;
        ctx.save();
        ctx.translate(x + w / 2, y + h / 2);
        ctx.rotate(wob * 0.01);
        ctx.fillRect(-w / 2, -h / 2, w, h);
        ctx.restore();
      } else {
        ctx.fillRect(x, y, w, h);
      }

      // inner border
      ctx.strokeStyle = 'rgba(255,255,255,0.28)';
      ctx.lineWidth = 1.5;
      ctx.strokeRect(x + 2, y + 2, w - 4, h - 4);

      // type glyphs
      ctx.fillStyle = 'rgba(0,0,10,0.55)';
      ctx.font = 'bold ' + Math.round(h * 0.52) + 'px ' + CFG.FONT;
      ctx.textAlign = 'center';
      ctx.textBaseline = 'middle';
      var gx = x + w / 2, gy = y + h / 2;
      if (b.type === 'A') {
        this._glyphArrow(ctx, gx, gy, h);
      } else if (b.type === 'S') {
        ctx.fillStyle = '#3a2a00';
        this._glyphChevrons(ctx, gx, gy, h);
      } else if (b.type === 'G') {
        ctx.fillText('◆', gx, gy);
        // sparkle idle
        if (Math.random() < 0.06) window.FX.goldenSparkles(gx, gy);
      } else if (b.type === 'B') {
        ctx.fillText('●', gx, gy);
      } else if (b.type === 'T') {
        ctx.fillStyle = '#c6cddb';
        ctx.fillText('■', gx, gy);
        // rivets
        ctx.fillStyle = 'rgba(0,0,0,0.3)';
        ctx.beginPath(); ctx.arc(x + 6, y + 6, 2.4, 0, TAU); ctx.fill();
        ctx.beginPath(); ctx.arc(x + w - 6, y + h - 6, 2.4, 0, TAU); ctx.fill();
      } else {
        // subtle dot pattern for normal bricks
        ctx.globalAlpha = 0.18;
        ctx.fillStyle = '#000';
        ctx.beginPath(); ctx.arc(gx - 6, gy - 5, 1.6, 0, TAU); ctx.fill();
        ctx.beginPath(); ctx.arc(gx + 6, gy + 5, 1.6, 0, TAU); ctx.fill();
        ctx.globalAlpha = 1;
      }

      // frozen overlay
      if (b.frozen) {
        ctx.fillStyle = 'rgba(159,232,255,0.45)';
        ctx.fillRect(x, y, w, h);
        ctx.strokeStyle = '#dffaff';
        ctx.lineWidth = 2;
        ctx.strokeRect(x, y, w, h);
        // ice crystal
        ctx.strokeStyle = 'rgba(255,255,255,0.8)';
        ctx.beginPath();
        ctx.moveTo(gx, y + 4); ctx.lineTo(gx, y + h - 4);
        ctx.moveTo(x + 4, gy); ctx.lineTo(x + w - 4, gy);
        ctx.stroke();
      }
      ctx.textAlign = 'left';
      ctx.textBaseline = 'alphabetic';
    },

    _glyphArrow: function (ctx, gx, gy, h) {
      // angle brick: arrow up (steep hit required)
      ctx.save();
      ctx.translate(gx, gy);
      ctx.strokeStyle = 'rgba(10,40,60,0.7)';
      ctx.fillStyle = 'rgba(10,40,60,0.7)';
      ctx.lineWidth = 2;
      var s = h * 0.20;
      ctx.beginPath();
      ctx.moveTo(0, -s); ctx.lineTo(-s, 0.9 * s);
      ctx.moveTo(0, -s); ctx.lineTo(s, 0.9 * s);
      ctx.stroke();
      ctx.beginPath();
      ctx.moveTo(0, -s * 0.1); ctx.lineTo(-s * 0.7, 0.15 * s);
      ctx.moveTo(0, -s * 0.1); ctx.lineTo(s * 0.7, 0.15 * s);
      ctx.stroke();
      ctx.restore();
    },

    _glyphChevrons: function (ctx, gx, gy, h) {
      ctx.strokeStyle = 'rgba(40,20,0,0.75)';
      ctx.lineWidth = 2.5;
      ctx.beginPath();
      var s = h * 0.14;
      for (var i = -1; i <= 1; i++) {
        var yy = gy + i * s * 1.4;
        ctx.moveTo(gx - s * 1.5, yy - s); ctx.lineTo(gx, yy);
        ctx.lineTo(gx + s * 1.5, yy - s);
      }
      ctx.stroke();
    },

    _drawCapsule: function (ctx, c) {
      var kind = c.kind;
      var col = CAP_COLORS[kind] || '#fff';
      var glow = 0.6 + 0.4 * Math.sin(c.t * 5);
      ctx.globalAlpha = glow * 0.35;
      ctx.fillStyle = col;
      ctx.beginPath();
      ctx.arc(c.x, c.y, CFG.CAP_W / 2 + 4, 0, TAU);
      ctx.fill();
      ctx.globalAlpha = 1;

      // pill
      var w = CFG.CAP_W, h = CFG.CAP_H;
      ctx.fillStyle = col;
      ctx.beginPath();
      ctx.roundRect ? ctx.roundRect(c.x - w / 2, c.y - h / 2, w, h, 8) : ctx.rect(c.x - w / 2, c.y - h / 2, w, h);
      ctx.fill();
      ctx.strokeStyle = 'rgba(255,255,255,0.7)';
      ctx.lineWidth = 2;
      ctx.beginPath();
      ctx.roundRect ? ctx.roundRect(c.x - w / 2, c.y - h / 2, w, h, 8) : ctx.rect(c.x - w / 2, c.y - h / 2, w, h);
      ctx.stroke();

      // label
      ctx.fillStyle = '#1a0b26';
      ctx.font = 'bold 16px ' + CFG.FONT;
      ctx.textAlign = 'center';
      ctx.textBaseline = 'middle';
      ctx.fillText(this._capLabel(kind), c.x, c.y + 1);
      ctx.textAlign = 'left';
      ctx.textBaseline = 'alphabetic';
    },

    _capLabel: function (kind) {
      var m = {
        FIRE: 'F', FROST: 'R', BLITZ: 'Z', SPLIT: 'P', PULSE: 'U',
        LASER: 'L', MULTI: 'M', EXPAND: 'E', CATCH: 'C', SLOW: 'S',
        MAGNET: 'L', GOLDRUSH: 'G', LIFE: '1'
      };
      return m[kind] || (kind.length <= 2 ? kind : kind[0]);
    },

    _drawPaddle: function (ctx) {
      var p = this.paddle;
      var w = p.w, h = p.h;
      var gold = this.abilities.gold > 0;
      var g = ctx.createLinearGradient(p.x - w / 2, p.y - h / 2, p.x + w / 2, p.y + h / 2);
      if (gold) { g.addColorStop(0, '#ffd700'); g.addColorStop(1, '#ff8a00'); }
      else { g.addColorStop(0, '#7df9ff'); g.addColorStop(1, '#4c7dff'); }
      ctx.fillStyle = g;
      ctx.beginPath();
      ctx.roundRect ? ctx.roundRect(p.x - w / 2, p.y - h / 2, w, h, h / 2) : ctx.rect(p.x - w / 2, p.y - h / 2, w, h);
      ctx.fill();
      ctx.strokeStyle = 'rgba(255,255,255,0.85)';
      ctx.lineWidth = 2;
      ctx.beginPath();
      ctx.roundRect ? ctx.roundRect(p.x - w / 2, p.y - h / 2, w, h, h / 2) : ctx.rect(p.x - w / 2, p.y - h / 2, w, h);
      ctx.stroke();
      // inner glow line
      ctx.strokeStyle = 'rgba(255,255,255,0.35)';
      ctx.lineWidth = 1;
      ctx.beginPath();
      ctx.moveTo(p.x - w / 2 + 8, p.y - 2); ctx.lineTo(p.x + w / 2 - 8, p.y - 2);
      ctx.stroke();
      // catch indicator
      var caught = this.balls.some(function (b) { return b.caught; });
      if (caught) {
        ctx.fillStyle = 'rgba(255,255,255,0.9)';
        ctx.font = 'bold 14px ' + CFG.FONT;
        ctx.textAlign = 'center';
        ctx.fillText('TAP / SPACE', p.x, p.y - h / 2 - 10);
        ctx.textAlign = 'left';
      }
    },

    _drawBall: function (ctx, b) {
      var r = b.r;
      var col = '#fff';
      if (b.ability === 'fire') col = '#ff6a00';
      else if (b.ability === 'frost') col = '#9fe8ff';
      else if (b.ability === 'blitz') col = '#ffe066';
      else if (b.ability === 'split') col = '#b7aaff';
      else if (b.ability === 'pulse') col = '#ff8ad8';
      else if (b.ember) col = '#ff9040';

      var grad = ctx.createRadialGradient(b.x - r * 0.35, b.y - r * 0.35, r * 0.2, b.x, b.y, r);
      grad.addColorStop(0, '#fff');
      grad.addColorStop(0.35, col);
      grad.addColorStop(1, this._darken(col, 40));
      ctx.fillStyle = grad;
      ctx.beginPath();
      ctx.arc(b.x, b.y, r, 0, TAU);
      ctx.fill();
      // halo
      ctx.globalAlpha = 0.4 + 0.2 * Math.sin(this.time * 8 + b.seed);
      ctx.fillStyle = col;
      ctx.beginPath();
      ctx.arc(b.x, b.y, r + 3, 0, TAU);
      ctx.fill();
      ctx.globalAlpha = 1;
    },

    _lighten: function (hex, amt) {
      var c = parseInt(hex.replace('#', ''), 16);
      var r = (c >> 16) & 255, g = (c >> 8) & 255, b = c & 255;
      return 'rgb(' + Math.min(255, r + amt) + ',' + Math.min(255, g + amt) + ',' + Math.min(255, b + amt) + ')';
    },
    _darken: function (hex, amt) {
      var c = parseInt(hex.replace('#', ''), 16);
      var r = (c >> 16) & 255, g = (c >> 8) & 255, b = c & 255;
      return 'rgb(' + Math.max(0, r - amt) + ',' + Math.max(0, g - amt) + ',' + Math.max(0, b - amt) + ')';
    },

    _circleRect: function (cx, cy, r, bx, by, bw, bh) {
      var nx = clamp(cx, bx, bx + bw);
      var ny = clamp(cy, by, by + bh);
      var dx = cx - nx, dy = cy - ny;
      return dx * dx + dy * dy <= r * r;
    },

    // ==========================================================================
    // HUD
    // ==========================================================================
    drawHUD: function (ctx) {
      var hh = 64;
      ctx.fillStyle = 'rgba(8,2,20,0.55)';
      ctx.fillRect(0, 0, this.W, hh + 8);
      ctx.fillStyle = '#fff';
      ctx.font = 'bold 30px ' + CFG.FONT;
      ctx.textAlign = 'left';
      ctx.textBaseline = 'top';
      ctx.fillText(String(this.score).padStart ? String(this.score).padStart(7, '0') : this.score, 14, 12);

      // HI
      ctx.fillStyle = 'rgba(255,255,255,0.6)';
      ctx.font = 'bold 14px ' + CFG.FONT;
      ctx.fillText('HI ' + this.hi, 14, 46);

      // level name
      ctx.fillStyle = 'rgba(255,255,255,0.75)';
      ctx.font = 'bold 13px ' + CFG.FONT;
      ctx.textAlign = 'center';
      ctx.fillText(this.levelName + (this.levelIndex >= 14 ? ' ∞' : ''), this.W / 2, 14);

      // lives as paddle icons
      ctx.textAlign = 'right';
      var lx = this.W - 64;
      for (var i = 0; i < this.lives; i++) {
        ctx.fillStyle = '#4c7dff';
        ctx.fillRect(lx - 11 - i * 14, 46, 16, 5);
        ctx.fillStyle = '#7df9ff';
        ctx.fillRect(lx - 11 - i * 14, 46, 16, 2);
      }
      ctx.textAlign = 'left';

      // POWER meter (center-ish under score)
      this._drawPowerMeter(ctx);

      // combo chip
      if (this.combo >= 2) this._drawComboChip(ctx);

      // overdrive meter
      if (this.od.ever || this.od.active) this._drawOdMeter(ctx);

      // ability pills
      this._drawAbilityPills(ctx);

      // buttons
      this._drawButtons(ctx);
    },

    _drawPowerMeter: function (ctx) {
      var x = 130, y = 18, w = 130, h = 12;
      ctx.fillStyle = 'rgba(255,255,255,0.12)';
      ctx.fillRect(x, y, w, h);
      var frac = (this.powerTier - 1) / 4;
      var g = ctx.createLinearGradient(x, y, x + w, y);
      g.addColorStop(0, '#4c7dff');
      g.addColorStop(1, '#ff5f7a');
      ctx.fillStyle = g;
      ctx.fillRect(x, y, w * frac, h);
      ctx.font = 'bold 10px ' + CFG.FONT;
      ctx.fillStyle = 'rgba(255,255,255,0.7)';
      ctx.textAlign = 'center';
      ctx.fillText('POWER ' + Math.ceil(this.powerTier) + '/5', x + w / 2, y + 15);
      ctx.textAlign = 'left';
      // segments
      for (var i = 1; i < 5; i++) {
        ctx.fillStyle = 'rgba(0,0,0,0.5)';
        ctx.fillRect(x + (w / 5) * i, y, 1, h);
      }
    },

    _drawComboChip: function (ctx) {
      var x = this.W / 2 + 60, y = 12;
      ctx.save();
      ctx.translate(x, y + 12);
      var pop = Math.min(1, this.comboT / 0.3);
      var s = 1 + pop * 0.25;
      ctx.scale(s, s);
      var g = ctx.createLinearGradient(-16, -12, 16, 12);
      g.addColorStop(0, '#ff9030');
      g.addColorStop(1, '#ff3d3d');
      ctx.fillStyle = g;
      ctx.beginPath();
      ctx.roundRect ? ctx.roundRect(-16, -12, 32, 24, 6) : ctx.rect(-16, -12, 32, 24);
      ctx.fill();
      ctx.strokeStyle = 'rgba(255,255,255,0.8)';
      ctx.lineWidth = 1.5;
      ctx.beginPath();
      ctx.roundRect ? ctx.roundRect(-16, -12, 32, 24, 6) : ctx.rect(-16, -12, 32, 24);
      ctx.stroke();
      ctx.fillStyle = '#fff';
      ctx.font = 'bold 15px ' + CFG.FONT;
      ctx.textAlign = 'center';
      ctx.fillText('x' + this.combo, 0, 4);
      ctx.restore();
      ctx.textAlign = 'left';
    },

    _drawOdMeter: function (ctx) {
      var x = 130, y = 38, w = 130, h = 10;
      ctx.fillStyle = 'rgba(255,255,255,0.12)';
      ctx.fillRect(x, y, w, h);
      var frac = this.od.active ? (this.od.tier + 1) / 5 : (this.od.drain > 0 ? this.od.drain / CFG.OD_DRAIN_TIME : 0);
      if (this.od.active) {
        var pulse = 0.6 + 0.4 * Math.sin(this.time * 6);
        ctx.fillStyle = 'rgba(255,' + Math.round(80 + pulse * 60) + ',120,0.9)';
        ctx.fillRect(x, y, w * frac, h);
      } else if (this.od.drain > 0) {
        ctx.fillStyle = 'rgba(255,140,216,0.5)';
        ctx.fillRect(x, y, w * frac, h);
      }
      ctx.font = 'bold 10px ' + CFG.FONT;
      ctx.fillStyle = 'rgba(255,255,255,0.75)';
      ctx.textAlign = 'center';
      ctx.fillText('OVERDRIVE' + (this.od.active ? ' x' + CFG.OD_MULTIS[this.od.tier] : ''), x + w / 2, y + 14);
      ctx.textAlign = 'left';
    },

    _drawAbilityPills: function (ctx) {
      var A = this.abilities;
      var active = [];
      if (A.laser > 0) active.push(['LASER', Math.ceil(A.laser), '#ff5f7a']);
      if (A.expand > 0) active.push(['WIDE', Math.ceil(A.expand), '#7df9ff']);
      if (A.slow > 0) active.push(['SLOW', Math.ceil(A.slow), '#7ab8ff']);
      if (A.mag > 0) active.push(['MAG', Math.ceil(A.mag), '#c07dff']);
      if (A.gold > 0) active.push(['2x', Math.ceil(A.gold), '#ffb01f']);
      if (A.catch > 0) active.push(['CATCH', '', '#9fe8ff']);
      var x = 14, y = 66;
      for (var i = 0; i < active.length; i++) {
        var a = active[i];
        var w = 58;
        ctx.fillStyle = a[2] + '33';
        ctx.fillRect(x, y, w, 20);
        ctx.strokeStyle = a[2];
        ctx.lineWidth = 1.2;
        ctx.strokeRect(x, y, w, 20);
        ctx.fillStyle = '#fff';
        ctx.font = 'bold 11px ' + CFG.FONT;
        ctx.textAlign = 'center';
        ctx.fillText(a[0] + (a[1] !== '' ? ' ' + a[1] : ''), x + w / 2, y + 14);
        ctx.textAlign = 'left';
        x += w + 6;
        if (x > this.W - 160) break;
      }
      // ball abilities on balls
      for (var j = 0; j < this.balls.length; j++) {
        var b = this.balls[j];
        if (b.ability && !b.caught) {
          var label = b.ability.toUpperCase();
          ctx.fillStyle = 'rgba(255,255,255,0.1)';
          ctx.fillRect(b.x - 18, b.y + b.r + 6, 36, 14);
          ctx.strokeStyle = CAP_COLORS[label.toUpperCase()] || '#fff';
          ctx.strokeRect(b.x - 18, b.y + b.r + 6, 36, 14);
          ctx.fillStyle = '#fff';
          ctx.font = 'bold 9px ' + CFG.FONT;
          ctx.textAlign = 'center';
          ctx.fillText(label, b.x, b.y + b.r + 16);
          ctx.textAlign = 'left';
        }
      }
    },

    _drawButtons: function (ctx) {
      var pr = this._pauseRect(), mr = this._muteRect();
      ctx.fillStyle = 'rgba(255,255,255,0.1)';
      ctx.beginPath();
      ctx.roundRect ? ctx.roundRect(pr.x, pr.y, pr.w, pr.h, 8) : ctx.rect(pr.x, pr.y, pr.w, pr.h);
      ctx.fill();
      ctx.strokeStyle = 'rgba(255,255,255,0.35)';
      ctx.lineWidth = 1.5;
      ctx.beginPath();
      ctx.roundRect ? ctx.roundRect(pr.x, pr.y, pr.w, pr.h, 8) : ctx.rect(pr.x, pr.y, pr.w, pr.h);
      ctx.stroke();
      // pause glyph
      ctx.fillStyle = '#fff';
      if (this.paused) {
        ctx.beginPath();
        ctx.moveTo(pr.x + 18, pr.y + 15); ctx.lineTo(pr.x + 32, pr.y + 25); ctx.lineTo(pr.x + 18, pr.y + 35);
        ctx.closePath(); ctx.fill();
      } else {
        ctx.fillRect(pr.x + 17, pr.y + 15, 6, 20);
        ctx.fillRect(pr.x + 28, pr.y + 15, 6, 20);
      }
      // mute glyph
      ctx.strokeStyle = '#fff';
      ctx.lineWidth = 2;
      ctx.beginPath();
      ctx.moveTo(mr.x + 14, mr.y + 18); ctx.lineTo(mr.x + 20, mr.y + 18); ctx.lineTo(mr.x + 26, mr.y + 23);
      ctx.arc(mr.x + 26, mr.y + 26, 3, -Math.PI / 2, Math.PI / 2);
      ctx.stroke();
      if (!this.muted) {
        ctx.beginPath();
        ctx.arc(mr.x + 26, mr.y + 26, 7, -0.9, 0.9);
        ctx.stroke();
      } else {
        ctx.strokeStyle = '#ff5f7a';
        ctx.beginPath();
        ctx.moveTo(mr.x + 18, mr.y + 34); ctx.lineTo(mr.x + 34, mr.y + 18);
        ctx.stroke();
      }
    },

    drawCountdown: function (ctx) {
      var msg = this.levelName;
      var sub = this.levelSub;
      ctx.fillStyle = 'rgba(255,255,255,0.95)';
      ctx.font = 'bold 44px ' + CFG.FONT;
      ctx.textAlign = 'center';
      ctx.strokeStyle = 'rgba(0,0,0,0.6)';
      ctx.lineWidth = 6;
      var cy = this.H * 0.5 - 60;
      ctx.strokeText(msg, this.W / 2, cy);
      ctx.fillText(msg, this.W / 2, cy);
      ctx.font = 'bold 18px ' + CFG.FONT;
      ctx.fillStyle = 'rgba(255,255,255,0.75)';
      var sub2 = sub || '';
      ctx.strokeText(sub2, this.W / 2, cy + 30);
      ctx.fillText(sub2, this.W / 2, cy + 30);

      // countdown number
      var n = this._count.text || '';
      var big = n === 'GO!' ? 110 : 80;
      ctx.font = 'bold ' + big + 'px ' + CFG.FONT;
      ctx.fillStyle = n === 'GO!' ? '#7dff8f' : '#ffe066';
      ctx.strokeStyle = 'rgba(0,0,0,0.65)';
      ctx.lineWidth = 8;
      ctx.strokeText(n, this.W / 2, this.H * 0.5 + 50);
      ctx.fillText(n, this.W / 2, this.H * 0.5 + 50);
      ctx.textAlign = 'left';
    },

    drawLevelClear: function (ctx) {
      var t = this._clear ? this._clear.t : 0;
      var a = Math.min(1, t * 2);
      ctx.fillStyle = 'rgba(0,0,0,' + (0.35 * a) + ')';
      ctx.fillRect(0, 0, this.W, this.H);
      ctx.textAlign = 'center';
      ctx.fillStyle = '#ffe066';
      ctx.font = 'bold 52px ' + CFG.FONT;
      ctx.strokeStyle = 'rgba(0,0,0,0.7)';
      ctx.lineWidth = 6;
      ctx.strokeText('LEVEL CLEAR!', this.W / 2, this.H * 0.42);
      ctx.fillText('LEVEL CLEAR!', this.W / 2, this.H * 0.42);
      ctx.fillStyle = 'rgba(255,255,255,0.85)';
      ctx.font = 'bold 20px ' + CFG.FONT;
      ctx.fillText('BONUS +' + (this._clearBonus || 0), this.W / 2, this.H * 0.42 + 42);
      ctx.font = 'bold 15px ' + CFG.FONT;
      ctx.fillStyle = 'rgba(255,255,255,0.6)';
      ctx.fillText('tap to continue', this.W / 2, this.H * 0.42 + 80);
      ctx.textAlign = 'left';
    },

    drawGameOver: function (ctx) {
      var t = this._over ? this._over.t : 0;
      ctx.fillStyle = 'rgba(0,0,0,' + Math.min(0.75, t * 0.5) + ')';
      ctx.fillRect(0, 0, this.W, this.H);
      ctx.textAlign = 'center';
      ctx.fillStyle = '#ff5f7a';
      ctx.font = 'bold 64px ' + CFG.FONT;
      ctx.strokeStyle = 'rgba(0,0,0,0.8)';
      ctx.lineWidth = 8;
      ctx.strokeText('GAME OVER', this.W / 2, this.H * 0.4);
      ctx.fillText('GAME OVER', this.W / 2, this.H * 0.4);
      ctx.fillStyle = '#fff';
      ctx.font = 'bold 30px ' + CFG.FONT;
      ctx.fillText('SCORE ' + this.score, this.W / 2, this.H * 0.4 + 52);
      ctx.fillStyle = this.score >= this.hi && this.score > 0 ? '#ffe066' : 'rgba(255,255,255,0.6)';
      ctx.font = 'bold 18px ' + CFG.FONT;
      ctx.fillText(this.score >= this.hi && this.score > 0 ? '★ NEW BEST! HI ' + this.hi : 'HI ' + this.hi, this.W / 2, this.H * 0.4 + 84);
      ctx.fillStyle = 'rgba(255,255,255,0.75)';
      ctx.font = 'bold 16px ' + CFG.FONT;
      ctx.fillText('TAP / SPACE TO PLAY AGAIN', this.W / 2, this.H * 0.4 + 140);
      ctx.textAlign = 'left';
    },

    drawPause: function (ctx) {
      ctx.fillStyle = 'rgba(0,0,0,0.55)';
      ctx.fillRect(0, 0, this.W, this.H);
      ctx.textAlign = 'center';
      ctx.fillStyle = '#fff';
      ctx.font = 'bold 44px ' + CFG.FONT;
      ctx.strokeStyle = 'rgba(0,0,0,0.7)';
      ctx.lineWidth = 6;
      ctx.strokeText('PAUSED', this.W / 2, this.H * 0.45);
      ctx.fillText('PAUSED', this.W / 2, this.H * 0.45);
      ctx.font = 'bold 16px ' + CFG.FONT;
      ctx.fillStyle = 'rgba(255,255,255,0.7)';
      ctx.fillText('P / BUTTON TO RESUME', this.W / 2, this.H * 0.45 + 40);
      ctx.textAlign = 'left';
    },

    drawMenu: function (ctx) {
      var t = this.stateTime;
      ctx.fillStyle = 'rgba(0,0,0,0.4)';
      ctx.fillRect(0, 0, this.W, this.H);
      ctx.textAlign = 'center';
      // title
      var pulse = 1 + 0.02 * Math.sin(t * 3);
      ctx.save();
      ctx.translate(this.W / 2, this.H * 0.26);
      ctx.scale(pulse, pulse);
      ctx.font = 'bold 78px ' + CFG.FONT;
      ctx.fillStyle = '#fff';
      ctx.strokeStyle = '#ff5f7a';
      ctx.lineWidth = 3;
      ctx.strokeText('OVERDRIVE', 0, 0);
      var g = ctx.createLinearGradient(-140, -40, 140, 20);
      g.addColorStop(0, '#7df9ff');
      g.addColorStop(0.5, '#ff5f7a');
      g.addColorStop(1, '#ffd23f');
      ctx.fillStyle = g;
      ctx.fillText('OVERDRIVE', 0, 0);
      ctx.restore();
      ctx.font = 'bold 16px ' + CFG.FONT;
      ctx.fillStyle = 'rgba(255,255,255,0.8)';
      ctx.fillText('a modern Brickles', this.W / 2, this.H * 0.26 + 52);

      // fake bricks on the menu background? subtle
      this._drawMenuBricks(ctx);

      ctx.font = 'bold 22px ' + CFG.FONT;
      ctx.fillStyle = '#ffe066';
      ctx.strokeStyle = 'rgba(0,0,0,0.7)';
      ctx.lineWidth = 5;
      ctx.strokeText('TAP OR CLICK TO START', this.W / 2, this.H * 0.6);
      ctx.fillText('TAP OR CLICK TO START', this.W / 2, this.H * 0.6);

      ctx.font = 'bold 14px ' + CFG.FONT;
      ctx.fillStyle = 'rgba(255,255,255,0.65)';
      ctx.fillText('↑ paddle boost: move UP when hitting the ball', this.W / 2, this.H * 0.68);
      ctx.fillText('A / S / G bricks need steep / fast / gold hits · T = steel: lasers or fire', this.W / 2, this.H * 0.71);
      ctx.fillText('Get a ball ABOVE the bricks to trigger OVERDRIVE', this.W / 2, this.H * 0.74);
      ctx.fillStyle = 'rgba(255,255,255,0.4)';
      ctx.font = 'bold 12px ' + CFG.FONT;
      ctx.fillText('mouse / touch / WASD / arrows · SPACE launch · P pause · M mute', this.W / 2, this.H * 0.8);
      ctx.fillText('HI ' + this.hi, this.W / 2, this.H * 0.86);
      ctx.textAlign = 'left';
    },

    _drawMenuBricks: function (ctx) {
      // decorative floating bricks
      ctx.globalAlpha = 0.14;
      var cols = ['#b44cff', '#7df9ff', '#ffe066', '#ffd23f', '#ff5f7a'];
      for (var i = 0; i < 8; i++) {
        var x = ((i * 97 + Math.sin(this.time * 0.5 + i) * 20 + this.W / 2 - 400) % this.W + this.W) % this.W;
        var y = this.H * 0.8 - 80 + ((i * 73) % 160);
        ctx.fillStyle = cols[i % cols.length];
        ctx.fillRect(x, y, 46, 22);
      }
      ctx.globalAlpha = 1;
    }
  };
})();
