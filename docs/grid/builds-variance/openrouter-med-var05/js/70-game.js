/* 70-game.js — namespace window.Game (+ bootstrap)
   SKYBREAK integrator: fixed-timestep loop, rendering, collisions, scoring,
   states, HUD/DOM wiring, Powerups facade. ES2020, no modules, file:// safe. */
'use strict';
(function () {
  var W = 900, H = 1340, DT = (typeof U !== 'undefined' && U.FIXED_DT) || 1 / 120;
  var VOID_Y = 1290; // shield line
  var LIFE_STEPS = [15000, 40000, 75000];

  function clamp(v, a, b) { return v < a ? a : (v > b ? b : v); }

  // ============================ Game ============================
  function Game() {
    this.canvas = null; this.ctx = null;
    this.dpr = 1; this.cw = 0; this.ch = 0;
    this.scale = 1; this.ox = 0; this.oy = 0;
    this.debug = /debug=1/.test(typeof location !== 'undefined' ? location.search : '');

    this.state = 'title';
    this.time = 0;            // simulated time (scaled), drives bricks
    this.frameT = 0;          // wall time for render anims
    this.acc = 0; this.lastNow = 0;
    this.fps = 60; this.fpsAcc = 0; this.fpsN = 0;

    this.score = 0; this.best = 0; this.lives = 3; this.level = 1;
    this.mult = 1; this.maxMult = 1; this.lastBreakT = -99; this.nextLifeIdx = 0;
    this.timers = {};         // id -> {t, total}
    this.balls = []; this.bricks = []; this.capsules = [];
    this.paddle = null;
    this.ballsTypeT = 0;      // ball-morph revert timer (max of active morphs)
    this.ballsType = 'normal';
    this.shieldActive = false;
    this.invertT = 0;
    this.slowT = 0;           // slowmo active timer
    this.ascendPauseT = 0;
    this.ascendY = 900;
    this.ascended = false; this.ascendEndT = 0;
    this.heldSticky = null;

    this.rng = null;          // per-level seeded RNG (capsule rolls)
    this.cellBricks = null;   // spatial buckets
    this._cols = 12; this._rows = 26;

    this._hudScore = -1; this._hudLevel = -1; this._hudLives = -1;
    this._hudMult = ''; this._hudMute = '';
    this._rafBound = this.frame.bind(this);
  }

  var GP = Game.prototype;

  // ---------------- bootstrap ----------------
  Game.prototype.start = function () {
    var d = typeof document !== 'undefined' ? document : null;
    if (d && !this.canvas) {
      this.canvas = d.getElementById('game');
    }
    if (!this.canvas) return;
    this.ctx = this.canvas.getContext('2d');
    if (typeof FX !== 'undefined' && FX.init) FX.init(this.ctx);
    if (typeof Input !== 'undefined') Input.attach(this.canvas, d.getElementById('hud'));
    this.best = U.loadBest();
    this._wireDom();
    this._resize();
    var self = this;
    if (typeof window !== 'undefined' && window.addEventListener) {
      window.addEventListener('resize', function () { self._resize(); });
      if (typeof Input !== 'undefined') Input.onBlur = function () { if (self.state === 'play' || self.state === 'serve') self.setPause(true); };
    }
    this.showScreen('screen-title');
    this._syncHud(true);
    this.lastNow = (typeof performance !== 'undefined' && performance.now) ? performance.now() : Date.now();
    if (typeof requestAnimationFrame !== 'undefined') requestAnimationFrame(this._rafBound || (this._rafBound = function (t) { self.frame(t); }));
  };

  Game.prototype._wireDom = function () {
    var d = typeof document !== 'undefined' ? document : null;
    if (!d) return;
    var self = this;
    function on(id, fn) { var e = d.getElementById(id); if (e) e.addEventListener('click', fn); }
    on('btn-start', function () { self.userStart(); });
    on('btn-resume', function () { self.setPause(false); });
    on('btn-restart-p', function () { self.fullRestart(); });
    on('btn-next', function () { self.nextLevel(); });
    on('btn-restart', function () { self.fullRestart(); });
    on('btn-pause', function () { self.togglePause(); });
    on('btn-mute', function () { self.toggleMute(); });
    if (this.debug && typeof window !== 'undefined' && window.addEventListener) {
      window.addEventListener('keydown', function (e) {
        if (e.code.indexOf('Digit') === 0) {
          var n = +e.code.slice(5);
          if (n >= 1 && n <= 8) { self.loadLevel(n); }
        } else if (e.code === 'KeyL') { self.addLife(); }
        else if (e.code === 'KeyX') {
          for (var id in Powerups.POOL) { try { Powerups.apply(id, self); } catch (err) {} }
        }
      });
    }
  };

  Game.prototype.userStart = function () {
    if (typeof AudioSys !== 'undefined') { AudioSys.init(); AudioSys.unlock(); }
    this.fullRestart();
  };
  Game.prototype.fullRestart = function () {
    if (typeof AudioSys !== 'undefined' && AudioSys.isInit && AudioSys.isInit()) AudioSys.startMusic();
    this.score = 0; this.lives = Levels.START_LIVES; this.nextLifeIdx = 0;
    this.mult = 1; this.maxMult = 1; this.timers = {};
    this.shieldActive = false; this.invertT = 0; this.slowT = 0;
    this.loadLevel(1);
  };
  Game.prototype.nextLevel = function () { this.loadLevel(this.level + 1); };
  Game.prototype.togglePause = function () { this.setPause(this.state === 'play' || this.state === 'serve'); };
  Game.prototype.setPause = function (p) {
    if (p && (this.state === 'play' || this.state === 'serve')) { this._prePause = this.state; this.state = 'pause'; this.showScreen('screen-pause'); }
    else if (!p && this.state === 'pause') { this.state = this._prePause || 'serve'; this._prePause = null; this.showScreen(null); }
  };
  Game.prototype.showScreen = function (id) {
    var d = typeof document !== 'undefined' ? document : null;
    if (!d) return;
    ['screen-title', 'screen-pause', 'screen-clear', 'screen-over'].forEach(function (s) {
      var e = d.getElementById(s); if (e) e.classList.toggle('show', s === id);
    });
  };
  Game.prototype.toggleMute = function () {
    if (typeof AudioSys === 'undefined') return;
    AudioSys.toggleMute(); this._syncMuteIcon();
  };
  Game.prototype._syncMuteIcon = function () {
    var e = document.getElementById('btn-mute');
    if (e && typeof AudioSys !== 'undefined') {
      e.textContent = '♪';
      if (AudioSys.muted) { e.style.textDecoration = 'line-through'; e.style.opacity = '0.45'; }
      else { e.style.textDecoration = ''; e.style.opacity = ''; }
    }
  };

  // ---------------- levels ----------------
  Game.prototype.loadLevel = function (n) {
    this.level = n;
    this.rng = new U.Rng(1337 + n);
    var spec = Levels.get(n);
    this.bricks = [];
    var minY = 1e9;
    for (var i = 0; i < spec.bricks.length; i++) {
      var s = spec.bricks[i];
      var b = new Entities.Brick(s.x, s.y, s.w, s.h, s.kind, {
        row: s.r, col: s.c, orient: s.orient, hp: s.hp,
        capsule: s.capsule, hue: s.hue, phase: s.phase
      });
      if (s.kind !== 'void' && b.y < minY) minY = b.y;
      this.bricks.push(b);
    }
    this.ascendY = minY === 1e9 ? 170 : minY - 30;
    this._buildIndex();
    this.capsules = [];
    this.ballSpeed = spec.ballSpeed;
    this._serve();
    this.state = 'serve';
    this.showScreen(null);
    this.ascended = false; this.ascendPauseT = 0;
    if (typeof FX !== 'undefined') { FX.energy = 0; FX.ambientHue = 0; }
    this._hudLevel = -1; this._syncHud(true);
  };
  Game.prototype._serve = function () {
    if (this.lives <= 0) { this._gameOver(); return; }
    // reset balls to one on paddle; clear ball morphs
    this.balls = [];
    for (var id in this.timers) { if (id === 'laser' || id === 'heavy' || id === 'phantom' || id === 'splitter') delete this.timers[id]; }
    this.ballsType = 'normal'; this.ballsTypeT = 0;
    var b = new Entities.Ball(450, 1090, 'normal');
    b.held = true; this.balls.push(b);
    this.mult = 1; this._syncHud(true);
  };
  Game.prototype._launch = function () {
    var b = this.balls[0];
    if (!b) return;
    b.held = false;
    var ang = (U.rand(-15, 15)) * Math.PI / 180;
    b.vx = Math.sin(ang) * this.ballSpeed;
    b.vy = -Math.cos(ang) * this.ballSpeed;
    b.applySpeedCap();
    this.state = 'play';
    if (typeof AudioSys !== 'undefined') AudioSys.impact('small');
    if (typeof FX !== 'undefined') FX.ring(b.x, b.y, { r0: 6, r1: 44, color: '#4fd8ff', life: 0.3, width: 2 });
  };
  Game.prototype._buildIndex = function () {
    this.cellBricks = [];
    for (var i = 0; i < this._cols * this._rows; i++) this.cellBricks.push([]);
    for (i = 0; i < this.bricks.length; i++) {
      var b = this.bricks[i];
      var c0 = clamp(Math.floor((b.x - 6) / 74), 0, this._cols - 1);
      var c1 = clamp(Math.floor((b.x + b.w - 6) / 74), 0, this._cols - 1);
      var r0 = clamp(Math.floor((b.y - 180) / 52), 0, this._rows - 1);
      var r1 = clamp(Math.floor((b.y + b.h - 180) / 52), 0, this._rows - 1);
      for (var r = r0; r <= r1; r++) for (var c = c0; c <= c1; c++) this.cellBricks[r * this._cols + c].push(b);
    }
  };
  Game.prototype._neighborsAlive = function (brick) {
    // any alive brick touching/adjacent to brick's cells (excl. void for anchor purposes: void counts)
    var found = 0;
    var c0 = clamp(Math.floor((brick.x - 6) / 74) - 1, 0, this._cols - 1);
    var c1 = clamp(Math.floor((brick.x + brick.w - 6) / 74) + 1, 0, this._cols - 1);
    var r0 = clamp(Math.floor((brick.y - 180) / 52) - 1, 0, this._rows - 1);
    var r1 = clamp(Math.floor((brick.y + brick.h - 180) / 52) + 1, 0, this._rows - 1);
    for (var r = r0; r <= r1; r++) for (var c = c0; c <= c1; c++) {
      var cell = this.cellBricks[r * this._cols + c];
      for (var i = 0; i < cell.length; i++) {
        var o = cell[i];
        if (o !== brick && o.aliveNow()) found++;
      }
    }
    return found > 0;
  };

  // ---------------- loop ----------------
  Game.prototype.frame = function (nowOrT) {
    var now = (typeof nowOrT === 'number' && nowOrT > 1e5) ? nowOrT
      : ((typeof performance !== 'undefined' && performance.now) ? performance.now() : Date.now());
    var frameDt = (now - this.lastNow) / 1000;
    this.lastNow = now;
    if (frameDt < 0) frameDt = 0;
    if (frameDt > 0.1) frameDt = 0.1;
    this.frameT += frameDt;
    // fps EMA
    if (frameDt > 0) { this.fpsAcc += 1 / frameDt; this.fpsN++; if (this.fpsN >= 20) { this.fps = this.fpsAcc / this.fpsN; this.fpsAcc = 0; this.fpsN = 0; } }

    if (typeof Input !== 'undefined' && Input.update) Input.update(frameDt * 1000);
    this._handleInput();
    if (typeof FX !== 'undefined') FX.update(frameDt);

    if (this.state === 'play' || this.state === 'serve') {
      this.acc += frameDt;
      var steps = 0;
      while (this.acc >= DT && steps < 4) { this.step(DT); this.acc -= DT; steps++; }
      if (steps === 4) this.acc = 0;
    } else {
      this.acc = 0;
    }
    this.render(frameDt);
    if (typeof requestAnimationFrame !== 'undefined') requestAnimationFrame(this._rafBound);
  };

  Game.prototype._handleInput = function () {
    if (typeof Input === 'undefined') return;
    var launch = Input.consumeLaunch();
    if (Input.consumeMute()) this.toggleMute();
    if (Input.consumePause()) this.togglePause();
    switch (this.state) {
      case 'title': if (launch) this.userStart(); break;
      case 'serve': if (launch) { this._ensureAudio(); this._launch(); } break;
      case 'play':
        if (launch) this._releaseSticky();
        break;
      case 'pause': if (launch) this.setPause(false); break;
      case 'levelclear': if (launch) this.nextLevel(); break;
      case 'over': if (launch) this.fullRestart(); break;
    }
  };
  Game.prototype._ensureAudio = function () {
    if (typeof AudioSys !== 'undefined') {
      AudioSys.init(); AudioSys.unlock();
      if (AudioSys.isInit && AudioSys.isInit() && !AudioSys._musicStarted) AudioSys.startMusic();
    }
  };

  Game.prototype._timeScale = function () {
    if (this.ascendPauseT > 0) return 0.2;
    if (this.slowT > 0) return Powerups.SLOWMO_TS;
    return 1;
  };

  Game.prototype.step = function (dt) {
    var ts = this._timeScale();
    var edt = dt * ts;
    this.time += edt;
    if (this.ascendPauseT > 0) this.ascendPauseT -= dt;

    // timers (real time)
    for (var id in this.timers) {
      var t = this.timers[id];
      t.t -= dt;
      if (t.t <= 0) { delete this.timers[id]; this._timerExpired(id); }
    }
    if (this.slowT > 0) this.slowT -= dt;
    if (this.invertT > 0) this.invertT -= dt;
    if (this.ballsTypeT > 0) {
      this.ballsTypeT -= dt;
      if (this.ballsTypeT <= 0 && this.ballsType !== 'normal') {
        this.ballsType = 'normal';
        for (var i = 0; i < this.balls.length; i++) if (this.balls[i].type !== 'normal') this.balls[i].setType('normal');
      }
    }

    // input → paddle shim
    if (typeof Input !== 'undefined') {
      if (Input.axisX !== 0) {
        var ax = Input.axisX * (this.invertT > 0 ? -1 : 1);
        Input.px = clamp(Input.px + ax * 1400 * edt, 30, 870);
        Input._pxT = Input.px;
      }
    }
    var inp = {
      get px() { return typeof Input !== 'undefined' ? (this._inv ? 900 - Input.px : Input.px) : 450; },
      get py() { return typeof Input !== 'undefined' ? Input.py : 1130; },
      lift: function () { return typeof Input !== 'undefined' ? Input.lift() : 0; }
    };
    inp._inv = this.invertT > 0;
    this.paddle.update(edt, { input: inp });

    // held sticky ball follows paddle
    if (this.heldSticky && this.heldSticky.alive) {
      this.heldSticky.x = this.paddle.x;
      this.heldSticky.y = this.paddle.y - this.paddle.h / 2 - this.heldSticky.r - 1;
      this.heldSticky.vx = 0; this.heldSticky.vy = 0;
    }

    // balls
    var anyAbove = false;
    for (i = 0; i < this.balls.length; i++) {
      var ball = this.balls[i];
      if (!ball.alive) continue;
      if (ball.held) { anyAbove = false; continue; }
      ball.update(edt, { paddle: this.paddle });
      // shield save
      if (this.shieldActive && ball.vy > 0 && ball.y > VOID_Y - ball.r && ball.y < VOID_Y + 40) {
        ball.y = VOID_Y - ball.r; ball.vy = -Math.abs(ball.vy);
        this.shieldActive = false;
        if (typeof FX !== 'undefined') { FX.flash(0.15); FX.ring(ball.x, VOID_Y, { r0: 10, r1: 80, color: '#4fd8ff', life: 0.4, width: 4 }); }
        if (typeof AudioSys !== 'undefined') AudioSys.impact('powerup');
      }
      if (ball.y < this.ascendY) anyAbove = true;
    }
    this._ballsVsPaddle();
    this._ballsVsBricks();
    this._lasers();
    this._capsules(edt);

    // cull lost balls
    for (i = this.balls.length - 1; i >= 0; i--) {
      if (this.balls[i].lost || !this.balls[i].alive) {
        this.balls.splice(i, 1);
      }
    }

    // ascension
    this._ascend(anyAbove);

    // bricks tick (regen)
    for (i = 0; i < this.bricks.length; i++) {
      var br = this.bricks[i];
      if (br.kind === 'regen' && (br.regenTimer > 0 || br.kind === 'regen')) {
        br.tick(edt, this._neighborsAlive(br));
      } else br.tick(edt, false);
    }

    // combo decay
    if (this.mult > 1 && this.time - this.lastBreakT > 2.5) {
      this.mult = Math.max(1, this.mult - 0.8 * edt);
    }
    this._musicEnergy();

    // life milestones
    while (this.nextLifeIdx < LIFE_STEPS.length && this.score >= LIFE_STEPS[this.nextLifeIdx]) {
      this.nextLifeIdx++;
      this.addLife();
      if (typeof FX !== 'undefined') FX.text(450, 900, '1-UP!', { color: '#52ffa8', size: 40, life: 1.4, rise: 60 });
      if (typeof AudioSys !== 'undefined') AudioSys.impact('life');
    }

    // level clear?
    if (this.state === 'play' && this._levelCleared()) this._levelClear();

    // all balls gone?
    if ((this.state === 'play' || this.state === 'serve') && this.balls.length === 0) this._loseLife();
    this._syncHud(false);
  };

  Game.prototype._timerExpired = function (id) {
    if (id === 'magnet' || id === 'wide') { /* paddle fields tick themselves */ }
  };

  Game.prototype._ascend = function (anyAbove) {
    if (anyAbove) {
      if (!this.ascended) {
        this.ascended = true;
        this.ascendPauseT = 0.12;
        // one-shot +10% speed for every ball currently above the bricks
        for (var k = 0; k < this.balls.length; k++) {
          var kb = this.balls[k];
          if (kb.alive && !kb.held && kb.y < this.ascendY) { kb.vx *= 1.1; kb.vy *= 1.1; kb.applySpeedCap(); }
        }
        var b = document.getElementById('banner');
        if (b) { b.classList.remove('ascend'); void b.offsetWidth; b.classList.add('ascend'); }
        if (typeof AudioSys !== 'undefined') AudioSys.impact('ascend');
        if (typeof AudioSys !== 'undefined') AudioSys.setTier(3);
        if (typeof FX !== 'undefined') { FX.flash(0.25); FX.ambientHue = 0.08; FX.energy = 1; }
      }
      var ab = this._anyBallAbove();
      if (ab) {
        if (this.ascendPauseT <= 0) {
          // +10% speed once per trigger — only right at trigger:
        }
        if (typeof FX !== 'undefined') FX.lightning(ab.x, this.ascendY, 90, '#ffb020');
      }
      this.ascendEndT = 0;
    } else if (this.ascended) {
      this.ascendEndT += DT;
      if (this.ascendEndT > 1.5) {
        this.ascended = false; this.ascendEndT = 0;
        if (typeof FX !== 'undefined') FX.ambientHue = 0;
        this._musicEnergy();
      }
    }
  };
  Game.prototype._anyBallAbove = function () {
    for (var i = 0; i < this.balls.length; i++) {
      var b = this.balls[i];
      if (b.alive && !b.held && b.y < this.ascendY) return b;
    }
    return null;
  };
  Game.prototype._musicEnergy = function () {
    var m = this.mult;
    var tier = m < 1.5 ? 0 : m < 2 ? 1 : m < 3 ? 2 : 3;
    if (this.ascended) { tier = 3; if (typeof FX !== 'undefined') { FX.energy = 1; FX.ambientHue = 0.08; } }
    else if (typeof FX !== 'undefined') FX.energy = clamp((m - 1) / 7, 0, 1);
    if (typeof AudioSys !== 'undefined' && AudioSys.isInit && AudioSys.isInit()) AudioSys.setTier(tier);
  };

  // ---------------- collisions ----------------
  Game.prototype._ballsVsPaddle = function () {
    var p = this.paddle;
    for (var i = 0; i < this.balls.length; i++) {
      var b = this.balls[i];
      if (!b.alive || b.held || b.vy <= 0) continue;
      var r = p.rect();
      var cx = clamp(b.x, r.x, r.x + r.w), cy = clamp(b.y, r.y, r.y + r.h);
      var dx = b.x - cx, dy = b.y - cy;
      if (dx * dx + dy * dy > b.r * b.r) continue;
      var stickyNow = p.magnetMs > 0 || this._timerActive('magnet');
      if (stickyNow && !p.stickyBallRef) {
        p.stickyBallRef = b;
        b.held = true; b.vx = 0; b.vy = 0;
        this.heldSticky = b;
        b._holdT = 1.5;
        if (typeof AudioSys !== 'undefined') AudioSys.impact('tick');
        continue;
      }
      b.paddleBounce({ paddle: p });
      if (typeof AudioSys !== 'undefined') AudioSys.impact('small');
      if (typeof FX !== 'undefined') {
        FX.ring(b.x, p.y - p.h / 2, { r0: 4, r1: 26, color: '#4fd8ff', life: 0.22, width: 2 });
        FX.burst(b.x, p.y - p.h / 2, { count: 4, color: '#4fd8ff', spd: 160, size: 2, life: 0.3 });
      }
    }
    // sticky auto-release
    if (this.heldSticky) {
      this.heldSticky._holdT -= DT;
      if (this.heldSticky._holdT <= 0) this._releaseSticky();
    }
  };
  Game.prototype._timerActive = function (id) { return !!(this.timers[id] && this.timers[id].t > 0); };
  Game.prototype._releaseSticky = function () {
    var b = this.heldSticky;
    if (!b || !b.alive) { this.heldSticky = null; this.paddle.stickyBallRef = null; return; }
    b.held = false;
    this.paddle.stickyBallRef = null;
    this.heldSticky = null;
    b.paddleBounce({ paddle: this.paddle });
    if (typeof AudioSys !== 'undefined') AudioSys.impact('small');
  };

  Game.prototype._ballsVsBricks = function () {
    for (var i = 0; i < this.balls.length; i++) {
      var b = this.balls[i];
      if (!b.alive || b.held) continue;
      // nearby cells
      var c = clamp(Math.floor((b.x - 6) / 74), 0, this._cols - 1);
      var r = clamp(Math.floor((b.y - 180) / 52), 0, this._rows - 1);
      for (var dr = -1; dr <= 1; dr++) {
        var rr = r + dr; if (rr < 0 || rr >= this._rows) continue;
        for (var dc = -1; dc <= 1; dc++) {
          var cc = c + dc; if (cc < 0 || cc >= this._cols) continue;
          var cell = this.cellBricks[rr * this._cols + cc];
          for (var k = 0; k < cell.length; k++) {
            var br = cell[k];
            if (!br.alive || br.broken || br.regenTimer > 0) continue;
            if (this._ballBrick(b, br)) k = cell.length; // one brick per cell per step
          }
        }
      }
    }
  };
  // returns true if resolved (bounce or damage)
  Game.prototype._ballBrick = function (b, br) {
    var cx = clamp(b.x, br.x, br.x + br.w), cy = clamp(b.y, br.y, br.y + br.h);
    var dx = b.x - cx, dy = b.y - cy;
    var d2 = dx * dx + dy * dy;
    if (d2 > b.r * b.r) return false;
    var res = br.hit(b, { time: this.time });
    if (res.passthrough) return true;
    if (res.broken) {
      this._onBrickBreak(br, b, res);
      b.slam = false;
      return true;
    }
    if (res.damage > 0) {
      // bounce off face + push out of overlap
      var l = Math.sqrt(d2) || 1;
      var nx, ny;
      if (l > 0.0001) { nx = dx / l; ny = dy / l; }
      else { ny = b.vy > 0 ? 1 : -1; nx = 0; }
      b.bounceOff(nx, ny);
      // push fully out
      var ox = br.w / 2 + b.r - Math.abs(b.x - (br.x + br.w / 2));
      var oy = br.h / 2 + b.r - Math.abs(b.y - (br.y + br.h / 2));
      if (oy < ox) b.y = b.y >= br.y + br.h / 2 ? br.y + br.h / 2 + oy : br.y + br.h / 2 - oy;
      else b.x = b.x >= br.x + br.w / 2 ? br.x + br.w / 2 + ox : br.x + br.w / 2 - ox;
      if (res.deflected) { /* wedge/void handled internally */ }
      if (typeof FX !== 'undefined') {
        FX.burst(cx, cy, { count: 6, color: 'hsl(' + br.seedHue + ',100%,70%)', spd: 200, size: 3, life: 0.35 });
        FX.shake(2);
      }
      if (typeof AudioSys !== 'undefined') AudioSys.impact('tick');
      b.slam = false;
      return true;
    }
    // glanced (already bounced internally by brick)
    return true;
  };

  Game.prototype._onBrickBreak = function (br, ball, res) {
    var kindScore = Entities.BRICK_SCORE[br.kind] || 10;
    var asc = this.ascended ? 3 : 1;
    var pts = kindScore * this.mult * asc;
    if (res.bonus) pts *= 1.3;
    pts = Math.round(pts);
    this.score += pts;
    // combo
    if (this.time - this.lastBreakT <= 1.4) this.mult = Math.min(8, this.mult + 0.1);
    this.lastBreakT = this.time;
    if (this.mult > this.maxMult) this.maxMult = this.mult;
    if (pts >= 30 && typeof FX !== 'undefined') {
      FX.text(br.x + br.w / 2, br.y + br.h / 2, '+' + pts, { color: this.ascended ? '#ffd98a' : '#aef4ff', size: pts > 150 ? 30 : 22, life: 0.9, rise: 60 });
    }
    // juice
    if (typeof FX !== 'undefined') {
      var col = 'hsl(' + (br.seedHue || 195) + ',100%,65%)';
      FX.burst(br.x + br.w / 2, br.y + br.h / 2, { count: ball && ball.slam ? 26 : 14, color: col, spd: ball && ball.slam ? 420 : 300, size: 3.5, life: 0.6, grav: 300 });
      FX.shake(ball && ball.slam ? 8 : 4);
      FX.ring(br.x + br.w / 2, br.y + br.h / 2, { r0: 6, r1: 50, color: col, life: 0.35, width: 3 });
      if (ball && ball.slam) FX.flash(0.08);
    }
    if (typeof AudioSys !== 'undefined') AudioSys.impact('break');
    // capsule drop
    var pid = Powerups.roll(br, { level: this.level, rng: this.rng });
    if (pid) this.capsules.push(new Entities.Capsule(br.x + br.w / 2, br.y + br.h / 2, pid));
    // sentry boost
    if (br.kind === 'sentry' && ball) { ball.boostT = 1.2; ball.applySpeedCap(); }
    // splitter child
    if (ball && ball.type === 'splitter' && Math.random() < 0.35 && this.balls.length < 6) {
      var nb = new Entities.Ball(br.x + br.w / 2, br.y + br.h / 2, 'normal');
      var base = Math.atan2(ball.vy, ball.vx) + (Math.random() - 0.5) * (80 * Math.PI / 180);
      var sp = Math.max(560, Math.min(880, ball.speed()));
      nb.vx = Math.cos(base) * sp; nb.vy = Math.sin(base) * sp;
      nb.applySpeedCap();
      this.balls.push(nb);
    }
  };
  // splitter spawns use brick center; keep name used above consistent:
  Game.prototype._onBrickBreak = Game.prototype._onBrickBreak; // (single def)

  // ---------------- laser ----------------
  Game.prototype._lasers = function () {
    for (var i = 0; i < this.balls.length; i++) {
      var b = this.balls[i];
      if (!b.alive || b.type !== 'laser' || b.laserCd > 0 || b.held) continue;
      b.laserCd = 0.35;
      // first alive brick above ball in x-band
      var target = null, ty = 0;
      for (var k = 0; k < this.bricks.length; k++) {
        var br = this.bricks[k];
        if (!br.alive || br.broken || br.regenTimer > 0) continue;
        if (b.x < br.x - 4 || b.x > br.x + br.w + 4) continue;
        if (br.y + br.h > b.y) continue; // must be above ball
        if (!target || br.y + br.h > ty) { target = br; ty = br.y + br.h; }
      }
      var endY = target ? ty : 0;
      if (typeof FX !== 'undefined') FX.beam(b.x, b.y, b.x, endY, { color: '#ff4fd8', life: 0.15, width: 3 });
      if (!target) continue;
      var fake = {
        x: b.x, y: ty, vx: 0, vy: -900, r: 4, type: 'laser', slam: false,
        speed: function () { return 900; }
      };
      var res = target.hit(fake, { time: this.time });
      if (res.broken) this._onBrickBreak(target, b, res);
      else if (res.damage > 0) {
        if (typeof FX !== 'undefined') FX.spark(b.x, ty, '#ff4fd8');
      }
    }
  };

  // ---------------- capsules ----------------
  Game.prototype._capsules = function (edt) {
    var p = this.paddle;
    var magnet = p.magnetMs > 0 || this._timerActive('magnet');
    for (var i = this.capsules.length - 1; i >= 0; i--) {
      var c = this.capsules[i];
      if (magnet) {
        var dx = p.x - c.x, dy = (p.y - 20) - c.y;
        var l = Math.sqrt(dx * dx + dy * dy);
        if (l < 240 && l > 1) {
          var pull = 900 * edt;
          if (pull > l) pull = l;
          c.x += dx / l * pull; c.y += dy / l * pull;
        }
      }
      c.update(edt);
      var cr = c.rect(), pr = p.rect();
      if (cr.x < pr.x + pr.w && cr.x + cr.w > pr.x && cr.y < pr.y + pr.h && cr.y + cr.h > pr.y) {
        this._catchCapsule(c);
        this.capsules.splice(i, 1);
        continue;
      }
      if (!c.alive) this.capsules.splice(i, 1);
    }
  };
  Game.prototype._catchCapsule = function (c) {
    var def = Powerups.POOL[c.powerId];
    var neg = Powerups.isNegative(c.powerId);
    Powerups.apply(c.powerId, this);
    if (typeof FX !== 'undefined') {
      FX.burst(c.x, c.y, { count: 12, color: def ? def.color : '#4fd8ff', spd: 260, size: 3, life: 0.5 });
      FX.text(c.x, c.y - 20, def ? def.label : c.powerId, { color: neg ? '#ff4f6e' : '#aef4ff', size: 20, life: 1, rise: 50 });
    }
    // AudioSys.impact fired inside Powerups.apply
    if (c.powerId === 'shield') this.shieldActive = true;
  };

  // ---------------- lives / clear ----------------
  Game.prototype._loseLife = function () {
    this.lives--;
    this.heldSticky = null; this.paddle.stickyBallRef = null;
    if (this.lives <= 0) { this._gameOver(); return; }
    this._serve();
    this.state = 'serve';
    if (typeof AudioSys !== 'undefined') AudioSys.impact('buzz');
    if (typeof FX !== 'undefined') FX.shake(10);
  };
  Game.prototype._gameOver = function () {
    this.state = 'over';
    var nb = this.score > this.best;
    if (nb) { this.best = this.score; U.saveBest(this.best); }
    var e;
    e = document.getElementById('over-score'); if (e) e.textContent = String(this.score);
    e = document.getElementById('over-level'); if (e) e.textContent = String(this.level);
    e = document.getElementById('new-best'); if (e) e.classList.toggle('show', nb);
    this.showScreen('screen-over');
    if (typeof AudioSys !== 'undefined') { AudioSys.setTier(0); if (AudioSys.stopMusic) AudioSys.stopMusic(); }
    this._syncHud(true);
  };
  Game.prototype._levelCleared = function () {
    for (var i = 0; i < this.bricks.length; i++) {
      var b = this.bricks[i];
      if (b.kind === 'void') continue;
      if (b.aliveNow()) return false;
    }
    return true;
  };
  Game.prototype._levelClear = function () {
    this.state = 'levelclear';
    var bonus = 250 + this.level * 50 + this.lives * 100;
    this.score += bonus;
    var e;
    e = document.getElementById('clear-score'); if (e) e.textContent = String(this.score);
    e = document.getElementById('clear-maxcombo'); if (e) e.textContent = '×' + this.maxMult.toFixed(1);
    e = document.getElementById('clear-bonus'); if (e) e.textContent = String(bonus);
    this.showScreen('screen-clear');
    if (typeof AudioSys !== 'undefined') AudioSys.impact('powerup');
    if (typeof FX !== 'undefined') { FX.flash(0.2); FX.vignettePulse(0.8); }
  };
  // ---------------- facade (Powerups.apply) ----------------
  Game.prototype.setPaddleWide = function (d) { this.paddle.wideMs = d * 1000; };
  Game.prototype.setMagnet = function (d) { this.paddle.magnetMs = d * 1000; };
  Game.prototype.setSticky = function (d) { this.paddle.stickyBallRef = this.paddle.stickyBallRef || null; this._stickyT = d; };
  Game.prototype.spawnBalls = function (n) {
    var src = this.balls.filter(function (b) { return b.alive; })[0];
    for (var i = 0; i < n && this.balls.length < 6; i++) {
      var b = new Entities.Ball(src ? src.x : 450, src ? src.y : 1090, 'normal');
      var a = Math.random() * U.TAU;
      var sp = src ? src.speed() : this.ballSpeed;
      b.vx = Math.cos(a) * sp; b.vy = -Math.abs(Math.sin(a) * sp) - 200;
      if (b.vy > -140) b.vy = -Math.abs(b.vy) - 100;
      b.applySpeedCap();
      this.balls.push(b);
      if (typeof FX !== 'undefined') FX.ring(b.x, b.y, { r0: 6, r1: 50, color: '#ffb020', life: 0.4, width: 3 });
    }
  };
  Game.prototype.setBallType = function (type, d) {
    this.ballsType = type; this.ballsTypeT = Math.max(this.ballsTypeT || 0, d || 0);
    for (var i = 0; i < this.balls.length; i++) this.balls[i].setType(type);
    if (type === 'phantom') for (i = 0; i < this.balls.length; i++) this.balls[i].phantomT = d;
  };
  Game.prototype.setTimeScale = function (ts, d) { if (ts < 1) this.slowT = Math.max(this.slowT, d || 0); };
  Game.prototype.giveShield = function () { this.shieldActive = true; };
  Game.prototype.addLife = function () {
    if (this.lives < Levels.MAX_LIVES) this.lives++;
    if (typeof AudioSys !== 'undefined') AudioSys.impact('life');
    this._hudLives = -1;
  };
  Game.prototype.addMultiplier = function (n) {
    this.mult = Math.min(8, this.mult + n);
    if (this.mult > this.maxMult) this.maxMult = this.mult;
    this._musicEnergy();
  };
  Game.prototype.shrinkPaddle = function (d) { this.paddle.baseW = Math.max(64, Entities.PADDLE_W * 0.55); this._shrinkT = d; };
  Game.prototype.speedBalls = function (n) {
    for (var i = 0; i < this.balls.length; i++) {
      var b = this.balls[i];
      b.vx *= 1 + n; b.vy *= 1 + n; b.applySpeedCap();
    }
  };
  Game.prototype.invert = function (d) { this.invertT = Math.max(this.invertT, d); };
  Game.prototype.setTimer = function (id, d) { this.timers[id] = { t: d, total: d }; };

  // ---------------- render ----------------
  Game.prototype.render = function (frameDt) {
    var ctx = this.ctx;
    if (!ctx) return;
    var t = this.frameT;
    this._resize();
    var dpr = this.dpr;
    ctx.setTransform(1, 0, 0, 1, 0, 0);
    ctx.fillStyle = '#04050c';
    ctx.fillRect(0, 0, this.cw, this.ch);
    // letterbox + shake
    ctx.setTransform(dpr * this.scale, 0, 0, dpr * this.scale, dpr * (this.ox + FX.shakeOffset.x * this.scale), dpr * (this.oy + FX.shakeOffset.y * this.scale));
    FX.drawBg(ctx, t);

    // title state: only the animated starfield behind the DOM overlay
    if (this.state === 'title') {
      FX.drawFx(ctx, t);
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      return;
    }

    var i, s;
    // bricks
    for (i = 0; i < this.bricks.length; i++) {
      if (this.bricks[i].aliveNow()) this.bricks[i].draw(ctx, t);
    }
    // capsules
    for (i = 0; i < this.capsules.length; i++) this.capsules[i].draw(ctx, t);
    // shield line
    if (this.shieldActive) {
      ctx.save();
      ctx.setLineDash([16, 10]);
      ctx.lineDashOffset = -t * 60;
      ctx.strokeStyle = 'rgba(79,216,255,0.9)';
      ctx.lineWidth = 3;
      ctx.beginPath(); ctx.moveTo(0, VOID_Y); ctx.lineTo(900, VOID_Y); ctx.stroke();
      ctx.restore();
    }
    // paddle
    this.paddle.draw(ctx, t);
    // balls
    for (i = 0; i < this.balls.length; i++) {
      var b = this.balls[i];
      if (b.held) { b.x = this.paddle.x; b.y = this.paddle.y - this.paddle.h / 2 - b.r - 2; }
      b.draw(ctx, t);
    }
    // serve hint
    if (this.state === 'serve') {
      var pulse = 0.65 + 0.35 * Math.sin(t * 5);
      var px = 450, py = 1030 + Math.sin(t * 3) * 4;
      // dark backing pill
      ctx.fillStyle = 'rgba(10,15,34,0.75)';
      ctx.strokeStyle = 'rgba(120,160,255,0.35)';
      ctx.lineWidth = 1;
      if (ctx.beginPath && ctx.roundRect) {
        ctx.beginPath(); ctx.roundRect(px - 150, py - 30, 300, 44, 22);
        ctx.fill(); ctx.stroke();
      } else { ctx.fillRect(px - 150, py - 30, 300, 44); }
      // animated up-pointing chevron above the text
      var chY = py - 40 - Math.sin(t * 5) * 4;
      ctx.strokeStyle = 'rgba(214,246,255,' + pulse + ')';
      ctx.lineWidth = 3;
      ctx.lineCap = 'round';
      ctx.beginPath();
      ctx.moveTo(px - 10, chY + 6); ctx.lineTo(px, chY - 4); ctx.lineTo(px + 10, chY + 6);
      ctx.stroke();
      ctx.lineCap = 'butt';
      // text
      ctx.fillStyle = 'rgba(214,246,255,' + pulse + ')';
      ctx.font = '800 26px system-ui, sans-serif';
      ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
      ctx.fillText('TAP TO LAUNCH', px, py + 3);
      ctx.textBaseline = 'alphabetic';
    }
    FX.drawFx(ctx, t);

    // overlays in real px
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    this._drawPills(ctx);
    this._drawJoystick(ctx);
    if (this.debug) this._drawDebug(ctx);
  };

  Game.prototype._drawPills = function (ctx) {
    var list = Powerups.activeList(this);
    var n = Math.min(list.length, 8);
    for (var i = 0; i < n; i++) {
      var it = list[i];
      var col = i % 4, row = Math.floor(i / 4);
      var x = 12 + col * 132, y = 1000 + row * 28;
      ctx.globalAlpha = 0.92;
      ctx.fillStyle = 'rgba(10,14,30,0.92)';
      if (ctx.beginPath && ctx.roundRect) { ctx.beginPath(); ctx.roundRect(x, y, 128, 24, 12); ctx.fill(); }
      else { ctx.fillRect(x, y, 128, 24); }
      ctx.strokeStyle = it.color;
      ctx.globalAlpha = 0.6;
      if (ctx.beginPath && ctx.roundRect) { ctx.beginPath(); ctx.roundRect(x, y, 128, 24, 12); ctx.stroke(); }
      else { ctx.strokeRect(x, y, 128, 24); }
      ctx.globalAlpha = 0.92;
      // remaining-time bar 3px tall at pill bottom
      ctx.fillStyle = it.color;
      ctx.fillRect(x + 4, y + 24 - 4, Math.max(2, 120 * (it.timeLeft / it.totalTime)), 3);
      ctx.font = 'bold 11px system-ui, sans-serif';
      ctx.textAlign = 'left'; ctx.textBaseline = 'middle';
      ctx.fillText(it.label, x + 8, y + 10);
      ctx.globalAlpha = 1;
    }
  };
  Game.prototype._drawJoystick = function (ctx) {
    var j = Input.joy;
    if (!j || !j.active) return;
    ctx.strokeStyle = 'rgba(120,160,255,0.4)';
    ctx.lineWidth = 2;
    ctx.beginPath(); ctx.arc(j.bx, j.by, 70, 0, U.TAU); ctx.stroke();
    ctx.fillStyle = 'rgba(79,216,255,0.35)';
    ctx.beginPath(); ctx.arc(j.tx, j.ty, 30, 0, U.TAU); ctx.fill();
  };

  Game.prototype._drawDebug = function (ctx) {
    ctx.fillStyle = 'rgba(0,255,0,0.8)';
    ctx.font = '12px monospace'; ctx.textAlign = 'left'; ctx.textBaseline = 'top';
    ctx.fillText('fps ' + this.fps.toFixed(0) + ' state ' + this.state + ' balls ' + this.balls.length + ' bricks ' + this.bricks.length + ' caps ' + this.capsules.length + ' mult ' + this.mult.toFixed(1) + ' score ' + this.score, 8, 60);
    ctx.strokeStyle = 'rgba(255,0,0,0.35)'; ctx.lineWidth = 1;
    for (var i = 0; i < this.bricks.length; i++) {
      var b = this.bricks[i];
      if (b.alive && !b.broken) ctx.strokeRect(b.x, b.y, b.w, b.h);
    }
    ctx.setLineDash([6, 6]);
    ctx.strokeStyle = '#ffb020';
    ctx.beginPath(); ctx.moveTo(0, this.ascendY); ctx.lineTo(900, this.ascendY); ctx.stroke();
    ctx.setLineDash([]);
  };

  Game.prototype._resize = function () {
    var rect = { w: window.innerWidth || 900, h: window.innerHeight || 1340 };
    this.cw = Math.max(1, rect.w); this.ch = Math.max(1, rect.h);
    this.dpr = clamp(window.devicePixelRatio || 1, 1, 3);
    if (this.canvas.width !== Math.round(this.cw * this.dpr) || this.canvas.height !== Math.round(this.ch * this.dpr)) {
      this.canvas.width = Math.round(this.cw * this.dpr);
      this.canvas.height = Math.round(this.ch * this.dpr);
    }
    this.scale = Math.min(this.cw / W, this.ch / H);
    this.ox = (this.cw - W * this.scale) / 2;
    this.oy = (this.ch - H * this.scale) / 2;
  };

  // ---------------- HUD ----------------
  Game.prototype._syncHud = function (force) {
    if (this._hudScore !== this.score) {
      this._hudScore = this.score;
      var e = document.getElementById('score-val'); if (e) e.textContent = String(this.score);
    }
    if (this._hudLevel !== this.level) {
      this._hudLevel = this.level;
      var e2 = document.getElementById('level-val'); if (e2) e2.textContent = String(this.level);
    }
    var ms = '×' + this.mult.toFixed(1);
    if (this._hudMult !== ms) {
      this._hudMult = ms;
      var mb = document.getElementById('mult-badge');
      if (mb) { mb.textContent = ms; mb.classList.toggle('hot', this.mult >= 2); }
    }
    if (this._hudLives !== this.lives) {
      this._hudLives = this.lives;
      var lr = document.getElementById('lives-row');
      if (lr) {
        while (lr.firstChild) lr.removeChild(lr.firstChild);
        for (var i = 0; i < Levels.MAX_LIVES; i++) {
          var d = document.createElement('div');
          d.className = 'life' + (i < this.lives ? '' : ' lost');
          lr.appendChild(d);
        }
      }
    }
    var be = document.getElementById('best-val');
    if (be && this.state === 'title') be.textContent = String(this.best);
    if (typeof AudioSys !== 'undefined') {
      var mi = AudioSys.muted ? '1' : '0';
      if (this._hudMute !== mi) { this._hudMute = mi; this._syncMuteIcon(); }
    }
  };

  // ---------------- init ----------------
  Game.prototype.initEntities = function () {
    this.paddle = new Entities.Paddle(450, Entities.PADDLE_Y_REST);
    this._serve();
  };

  // expose
  window.Game = Game;

  // ---------------- bootstrap ----------------
  function boot() {
    if (!window.Game || window.G) return;
    var g = new Game();
    g.initEntities();
    window.G = g;
    window.game = g;
    g.start();
  }
  if (typeof document !== 'undefined') {
    if (document.readyState === 'loading') window.addEventListener('load', boot);
    else boot();
  }
})();
