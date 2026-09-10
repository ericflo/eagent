/* 40-entities.js — namespace window.Entities (SKYBREAK)
   Ball, Paddle, Brick, Capsule + tuning constants. ES2020, no modules.
   Depends (inside functions only): window.U, window.FX, window.window.AudioSys. */
'use strict';
(function () {
  var E = (window.Entities = window.Entities || {});

  E.BALL_R = 11;
  E.PADDLE_W = 128;
  E.PADDLE_H = 22;
  E.PADDLE_Y_REST = 1130;
  E.ZONE_TOP = 1080;
  E.ZONE_BOT = 1180;

  // per-type speed bands [min, max]
  var SPEED = {
    normal:   [560, 880],
    heavy:    [300, 520],
    laser:    [480, 700],
    phantom:  [520, 760],
    splitter: [480, 760]
  };
  var COLORS = {
    normal: '#4fd8ff', heavy: '#ff7a3d', laser: '#ff4fd8',
    splitter: '#52ffa8', phantom: '#b44dff'
  };
  var W = 900, H = 1340;

  // ---------- helpers ----------
  function normVel(vx, vy, target) {
    var l = Math.sqrt(vx * vx + vy * vy) || 1;
    return [vx / l * target, vy / l * target];
  }

  // ======================================================================
  // Ball
  // ======================================================================
  function Ball(x, y, type) {
    this.x = x; this.y = y;
    this.vx = 0; this.vy = 0;
    this.type = type || 'normal';
    this.r = E.BALL_R * (this.type === 'heavy' ? 1.35 : 1);
    this.color = COLORS[this.type];
    this.alive = true;
    this.trailHue = 195;
    this.slam = false;          // next brick hit deals 2 damage + juice
    this.laserCd = 0;
    this.phantomT = 0;          // phantom time left (s)
    this.spin = 0;
    this.boostT = 0;            // sentry-break speed boost
    this.lost = false;
    this.setBase(this.type === 'heavy' ? 520 : this.type === 'laser' ? 700 : 760);
  }
  Ball.prototype.setBase = function (spd) {
    var a = normVel(this.vx, this.vy, spd);
    this.vx = a[0]; this.vy = a[1];
  };
  Ball.prototype.applySpeedCap = function () {
    var band = SPEED[this.type] || SPEED.normal;
    var lo = band[0], hi = band[1];
    if (this.boostT > 0) hi += 80;
    var l = Math.sqrt(this.vx * this.vx + this.vy * this.vy);
    if (l < 1e-6) return;
    if (l > hi) { this.vx *= hi / l; this.vy *= hi / l; }
    else if (l < lo) { this.vx *= lo / l; this.vy *= lo / l; }
  };
  Ball.prototype.speed = function () {
    return Math.sqrt(this.vx * this.vx + this.vy * this.vy);
  };
  Ball.prototype.bounceOff = function (nx, ny, restitution) {
    var r = restitution === undefined ? 1 : restitution;
    var d = this.vx * nx + this.vy * ny;
    this.vx -= 2 * d * nx;
    this.vy -= 2 * d * ny;
    if (r !== 1) { this.vx *= r; this.vy *= r; }
    // caller moves ball out of overlap; we also push slightly along normal
    var l = Math.sqrt(this.vx * this.vx + this.vy * this.vy) || 1;
    this.x += nx * (this.r * 0.6) * (d < 0 ? 1 : -1);
    this.y += ny * (this.r * 0.6) * (d < 0 ? 1 : -1);
    this.spin = (nx * this.vy - ny * this.vx) / l * 0.5;
    this.applySpeedCap();
  };
  Ball.prototype.paddleBounce = function (game) {
    var p = game.paddle;
    var off = U.clamp((this.x - p.x) / (p.w / 2), -1, 1);
    var ang = off * 62 * Math.PI / 180; // 0 = straight up
    var spd = this.speed();
    // renormalize direction to launch angle (up = -y)
    this.vx = Math.sin(ang) * spd;
    this.vy = -Math.cos(ang) * spd;
    // english from paddle motion
    this.vx = U.clamp(this.vx + p.vx * 0.08, this.vx - 60, this.vx + 60);
    var lifted = false;
    if (p.liftIntent > 0 && p.vy < 0) {
      spd *= 1 + 0.18 * p.liftIntent;
      lifted = true;
      this.slam = true;
      // sharper / flatter angle bias upward
      var dir = this.vx >= 0 ? 1 : -1;
      var flat = Math.min(Math.abs(ang) + 0.25 * p.liftIntent, 1.05);
      this.vx = Math.sin(flat) * spd * dir;
      this.vy = -Math.cos(flat) * spd;
      p.slamFlash = 1;
      if (window.FX) window.FX.ring(p.x, p.y - p.h / 2, { r0: 10, r1: 60, color: p.glowHue !== undefined ? 'hsl(' + (p.glowHue % 360) + ',100%,70%)' : '#4fd8ff', life: 0.35, width: 3 });
    } else if (p.vy > 40) {
      // paddle moving down: damping + slight downward bias
      spd *= 0.92;
      this.vy += spd * 0.06 * Math.sign(this.vy || 1);
    }
    var a = normVel(this.vx, this.vy, spd);
    this.vx = a[0]; this.vy = a[1];
    this.y = p.y - p.h / 2 - this.r - 0.5;
    if (this.vy > -140) this.vy = -140; // always leave upward-ish
    this.applySpeedCap();
    return lifted;
  };
  Ball.prototype.update = function (dt, game) {
    var px = this.x, py = this.y;
    this.x += this.vx * dt;
    this.y += this.vy * dt;
    if (this.boostT > 0) this.boostT -= dt;
    if (this.phantomT > 0) {
      this.phantomT -= dt;
      if (this.phantomT <= 0 && this.type === 'phantom') this.setType('normal');
    }
    if (this.laserCd > 0) this.laserCd -= dt;
    this.spin += dt * 6;
    // walls
    if (this.x < this.r) { this.x = this.r; this.vx = Math.abs(this.vx); this.onWall(px, py); }
    else if (this.x > W - this.r) { this.x = W - this.r; this.vx = -Math.abs(this.vx); this.onWall(px, py); }
    if (this.y < this.r) { this.y = this.r; this.vy = Math.abs(this.vy); this.onWall(px, py); }
    if (this.y > H + 60) this.lost = true;
    this.applySpeedCap();
    if (window.FX) window.FX.trail(this.x, this.y, this.r, this.color, this.trailHue);
  };
  Ball.prototype.onWall = function () {
    if (window.FX) window.FX.spark(this.x, this.y, this.color);
    if (window.AudioSys) window.AudioSys.impact('tick');
  };
  Ball.prototype.setType = function (t) {
    this.type = t;
    this.color = COLORS[t];
    this.r = E.BALL_R * (t === 'heavy' ? 1.35 : 1);
    this.setBase(SPEED[t][1]);
  };
  Ball.prototype.draw = function (ctx, t) {
    var r = this.r, c = this.color;
    var glow = this.type !== 'normal' || this.slam;
    // additive glow halo
    ctx.globalCompositeOperation = 'lighter';
    if (glow) {
      ctx.shadowBlur = 14; ctx.shadowColor = c;
    }
    ctx.beginPath();
    ctx.arc(this.x, this.y, r * 1.7, 0, U.TAU);
    ctx.fillStyle = this.type === 'phantom' ? 'rgba(120,60,220,0.18)' : 'rgba(79,216,255,0.10)';
    ctx.fill();
    ctx.shadowBlur = 0;
    // core with radial gradient
    var g = ctx.createRadialGradient(this.x - r * 0.3, this.y - r * 0.3, r * 0.15, this.x, this.y, r);
    g.addColorStop(0, '#ffffff');
    g.addColorStop(0.45, c);
    g.addColorStop(1, this.type === 'phantom' ? 'rgba(180,77,255,0.35)' : 'rgba(10,30,60,0.9)');
    ctx.globalAlpha = this.type === 'phantom' ? 0.75 : 1;
    ctx.beginPath();
    ctx.arc(this.x, this.y, r, 0, U.TAU);
    ctx.fillStyle = g;
    ctx.fill();
    // rim
    ctx.strokeStyle = c; ctx.lineWidth = 1.5; ctx.globalAlpha *= 0.8;
    ctx.stroke();
    ctx.globalAlpha = 1;
    // per-type flourish
    if (this.type === 'laser') {
      ctx.strokeStyle = c; ctx.lineWidth = 1.5;
      ctx.beginPath();
      ctx.moveTo(this.x - r * 2.2, this.y); ctx.lineTo(this.x + r * 2.2, this.y);
      ctx.moveTo(this.x, this.y - r * 2.2); ctx.lineTo(this.x, this.y + r * 2.2);
      ctx.globalAlpha = 0.7; ctx.stroke(); ctx.globalAlpha = 1;
    } else if (this.type === 'splitter') {
      ctx.fillStyle = '#c8ff4f';
      var a0 = this.spin * 2;
      ctx.beginPath(); ctx.arc(this.x + Math.cos(a0) * r * 1.9, this.y + Math.sin(a0) * r * 1.9, 2.2, 0, U.TAU); ctx.fill();
      ctx.beginPath(); ctx.arc(this.x - Math.cos(a0) * r * 1.9, this.y - Math.sin(a0) * r * 1.9, 2.2, 0, U.TAU); ctx.fill();
    } else if (this.type === 'phantom') {
      ctx.setLineDash([4, 5]);
      ctx.strokeStyle = c; ctx.globalAlpha = 0.55;
      ctx.beginPath(); ctx.arc(this.x, this.y, r * 1.5, this.spin, this.spin + U.TAU); ctx.stroke();
      ctx.setLineDash([]); ctx.globalAlpha = 1;
    }
    if (this.slam) {
      ctx.strokeStyle = '#ffb020'; ctx.lineWidth = 2; ctx.globalAlpha = 0.8;
      ctx.beginPath(); ctx.arc(this.x, this.y, r + 4 + Math.sin(t * 12) * 1.5, 0, U.TAU); ctx.stroke();
      ctx.globalAlpha = 1;
    }
    ctx.globalCompositeOperation = 'source-over';
  };

  // ======================================================================
  // Paddle
  // ======================================================================
  function Paddle(x, y) {
    this.x = x || 450; this.y = y || E.PADDLE_Y_REST;
    this.baseW = E.PADDLE_W;
    this.w = this.baseW; this.h = E.PADDLE_H;
    this.vx = 0; this.vy = 0;
    this.liftIntent = 0;
    this.slamFlash = 0;
    this.stickyBallRef = null;
    this.magnetMs = 0;
    this.wideMs = 0;
    this.glowHue = 195;
    this.lastX = this.x; this.lastY = this.y;
  }
  Paddle.prototype.update = function (dt, game) {
    var inp = game.input;
    var tx = U.clamp(inp.px, this.w / 2 + 2, W - this.w / 2 - 2);
    var ty = U.clamp(inp.py, E.ZONE_TOP, E.ZONE_BOT);
    var k = 1 - Math.exp(-dt * 18);
    this.x += (tx - this.x) * k;
    this.y += (ty - this.y) * k;
    this.vx = (this.vx * 0.85) + (this.x - this.lastX) / dt * 0.15;
    this.vy = (this.vy * 0.85) + (this.y - this.lastY) / dt * 0.15;
    this.lastX = this.x; this.lastY = this.y;
    this.liftIntent = inp.lift ? U.clamp(inp.lift(), 0, 1) : 0;
    if (this.wideMs > 0) this.wideMs -= dt * 1000;
    if (this.magnetMs > 0) this.magnetMs -= dt * 1000;
    if (this.slamFlash > 0) this.slamFlash = Math.max(0, this.slamFlash - dt * 3);
    // animated width: target 1 or 1.6
    var target = this.wideMs > 0 ? 1.6 : 1;
    var cur = this.w / this.baseW;
    cur += (target - cur) * (1 - Math.exp(-dt * 8));
    this.w = this.baseW * cur;
  };
  Paddle.prototype.rect = function () {
    return { x: this.x - this.w / 2, y: this.y - this.h / 2, w: this.w, h: this.h };
  };
  Paddle.prototype.draw = function (ctx, t) {
    var x = this.x, y = this.y, w = this.w, h = this.h;
    var hw = w / 2, hh = h / 2;
    var squash = U.clamp(Math.abs(this.vx) / 2000, 0, 0.18);
    // thruster glow below, length ∝ liftIntent
    var lift = this.liftIntent;
    var fl = 14 + lift * 34 + Math.sin(t * 22) * 3 * (0.4 + lift);
    var g = ctx.createLinearGradient(0, y + hh, 0, y + hh + fl);
    g.addColorStop(0, lift > 0.05 ? 'rgba(255,176,32,' + (0.25 + lift * 0.5) + ')' : 'rgba(79,216,255,0.15)');
    g.addColorStop(1, 'rgba(0,0,0,0)');
    ctx.fillStyle = g;
    ctx.beginPath();
    ctx.moveTo(x - hw * 0.7, y + hh);
    ctx.lineTo(x + hw * 0.7, y + hh);
    ctx.lineTo(x, y + hh + fl);
    ctx.closePath(); ctx.fill();
    // wide wings
    if (this.wideMs > 0) {
      ctx.fillStyle = '#52ffa8';
      ctx.beginPath(); ctx.roundRect ? ctx.roundRect(x - hw - 14, y - hh + 3, 14, h - 6, 5) : ctx.rect(x - hw - 14, y - hh + 3, 14, h - 6); ctx.fill();
      ctx.beginPath(); ctx.roundRect ? ctx.roundRect(x + hw, y - hh + 3, 14, h - 6, 5) : ctx.rect(x + hw, y - hh + 3, 14, h - 6); ctx.fill();
    }
    // body
    var hue = this.glowHue % 360;
    var bg = ctx.createLinearGradient(0, y - hh, 0, y + hh);
    bg.addColorStop(0, '#9fe8ff');
    bg.addColorStop(0.5, '#4fd8ff');
    bg.addColorStop(1, '#0f3a5c');
    ctx.beginPath();
    if (ctx.roundRect) ctx.roundRect(x - hw, y - hh, w, h, 11);
    else ctx.rect(x - hw, y - hh, w, h);
    ctx.fillStyle = bg;
    ctx.fill();
    // neon rim
    ctx.lineWidth = 2;
    ctx.strokeStyle = this.slamFlash > 0
      ? 'rgba(255,176,32,' + (0.5 + this.slamFlash * 0.5) + ')'
      : 'hsl(' + hue + ',100%,70%)';
    ctx.stroke();
    // sticky indicator
    if (this.stickyBallRef) {
      ctx.setLineDash([6, 4]);
      ctx.strokeStyle = '#4fd8ff'; ctx.lineWidth = 2;
      ctx.beginPath(); ctx.moveTo(x - hw + 6, y - hh - 3); ctx.lineTo(x + hw - 6, y - hh - 3); ctx.stroke();
      ctx.setLineDash([]);
    }
    // magnet aura
    if (this.magnetMs > 0) {
      ctx.strokeStyle = 'rgba(180,77,255,' + (0.25 + 0.2 * Math.sin(t * 8)) + ')';
      ctx.lineWidth = 1.5;
      ctx.beginPath();
      ctx.arc(x, y, hw + 10 + Math.sin(t * 6) * 4, Math.PI, U.TAU);
      ctx.stroke();
    }
  };

  // ======================================================================
  // Brick
  // ======================================================================
  var KIND_SCORE = { std: 10, armored: 25, wedge: 30, sentry: 40, ghostly: 20, regen: 35, void: 0 };
  E.Brick = Brick;
  function Brick(x, y, w, h, kind, opts) {
    opts = opts || {};
    this.x = x; this.y = y; this.w = w; this.h = h;
    this.row = opts.row | 0; this.col = opts.col | 0;
    this.kind = kind || 'std';
    this.maxHp = this.hp = opts.hp || (this.kind === 'armored' ? 2 : 1);
    this.orient = opts.orient | 0;
    this.phase = opts.phase !== undefined ? opts.phase : Math.random() * U.TAU;
    this.regenTries = 4; // each cycle costs 2 (one on break, one on regrow) → 2 regrows max
    this.regenTimer = 0;
    this.alive = true;
    this.hasCapsule = !!opts.capsule;
    this.seedHue = opts.hue !== undefined ? opts.hue : 195;
    this.wobbleT = 0; this.lastHitT = -9; this.broken = false;
    this.cracks = 0;
    this.squash = 0;
    this._regenGhost = false;
  }
  Brick.fromSpec = function (spec) {
    // spec: {x?, y?, w?, h?, c?, r?, kind, orient?, hp?, capsule?, hue, phase?}
    var cellW = 74, cellH = 26;
    var x = spec.x, y = spec.y, w = spec.w || 70, h = spec.h || 48;
    if (x === undefined) x = 6 + (spec.c | 0) * cellW + (cellW - w) / 2;
    if (y === undefined) y = 180 + (spec.r | 0) * cellH + (cellH * 2 - h) / 2;
    var wide = spec.kind === 'armored' || spec.kind === 'void';
    if (wide && w === 70) w = 144;
    return new Brick(x, y, w, h, spec.kind || 'std', spec);
  };
  Brick.prototype.rect = function () {
    return { x: this.x, y: this.y, w: this.w, h: this.h };
  };
  Brick.prototype.aliveNow = function () {
    return (this.alive && !this.broken) || this.regenTimer > 0;
  };
  Brick.prototype.opacity = function (t) {
    return 0.5 + 0.5 * Math.sin(t * U.TAU / 3 + this.phase); // 3s cycle 0..1
  };
  Brick.prototype.faceNormal = function () {
    // orient is the compass bearing the vulnerable face points toward:
    // 0 = face up (ball must travel down), 90 = face right,
    // 180 = face down (ball must travel up), 270 = face left
    switch (this.orient) {
      case 0: return [0, -1];
      case 90: return [1, 0];
      case 180: return [0, 1];
      default: return [-1, 0];
    }
  };
  // damage handler shared by hittable kinds
  Brick.prototype._damage = function (ball, game, dmg) {
    this.hp -= dmg;
    this.wobbleT = 0.3;
    this.lastHitT = game.time !== undefined ? game.time : 0;
    this.cracks = this.maxHp - this.hp;
    var res = { broken: false, glanced: false, damage: dmg, passthrough: false, deflected: false };
    if (this.hp <= 0) {
      res.broken = true;
      this.alive = false;
      this.broken = true;
    }
    return res;
  };
  // Wedge angle rule: ball must be travelling INTO the vulnerable face,
  // i.e. velocity · faceNormal < -cos(38°) (normal points outward from face).
  Brick.prototype._wedgeOk = function (ball) {
    var n = this.faceNormal();
    var l = ball.speed();
    if (l < 1e-6) return false;
    var dot = (ball.vx * n[0] + ball.vy * n[1]) / l;
    return dot < -Math.cos(38 * Math.PI / 180);
  };
  Brick.prototype._softDeflect = function (ball) {
    var nx = 0, ny = 0;
    // normal = shortest push direction from brick center to ball
    var dx = ball.x - (this.x + this.w / 2);
    var dy = ball.y - (this.y + this.h / 2);
    var ox = this.w / 2 + ball.r - Math.abs(dx);
    var oy = this.h / 2 + ball.r - Math.abs(dy);
    if (oy < ox) ny = dy >= 0 ? 1 : -1; else nx = dx >= 0 ? 1 : -1;
    ball.bounceOff(nx, ny, this.kind === 'void' ? 1 : 0.6);
    return { broken: false, glanced: true, damage: 0, passthrough: false, deflected: true, nx: nx, ny: ny };
  };
  Brick.prototype.hit = function (ball, game) {
    var heavy = ball.type === 'heavy' || ball.slam;
    switch (this.kind) {
      case 'std':
        return this._damage(ball, game, 1);
      case 'armored':
        return this._damage(ball, game, heavy ? 2 : 1);
      case 'wedge':
        if (this._wedgeOk(ball)) {
          var r = this._damage(ball, game, 1);
          r.bonus = true; // +30% score hint for Game
          return r;
        }
        if (window.FX) window.FX.spark(ball.x, ball.y, '#ffb020');
        if (window.AudioSys) window.AudioSys.impact('buzz');
        return this._softDeflect(ball);
      case 'sentry':
        if (ball.speed() >= 760) return this._damage(ball, game, 1);
        this.squash = 1;
        if (window.FX) window.FX.ring(this.x + this.w / 2, this.y + this.h / 2, { r0: 8, r1: 40, color: '#4fd8ff', life: 0.3, width: 2 });
        return this._softDeflect(ball);
      case 'ghostly':
        var op = this.opacity(game.time || 0);
        if (op > 0.5) return this._damage(ball, game, 1);
        return { broken: false, glanced: false, damage: 0, passthrough: true };
      case 'regen':
        var r2 = this._damage(ball, game, 1);
        if (r2.broken && this.regenTries > 0) {
          this.regenTries--;
          this.regenTimer = 4;
          this.alive = false; this.broken = true; this._regenGhost = true;
        }
        return r2;
      case 'void':
        if (window.FX) window.FX.spark(ball.x, ball.y, '#b44dff');
        // chaotic refraction: Game applies bounceOff with returned normal, we add kick here
        var r3 = this._softDeflect(ball);
        // ±10° rotation
        var a = (Math.random() * 20 - 10) * Math.PI / 180;
        var ca = Math.cos(a), sa = Math.sin(a);
        var vx = ball.vx * ca - ball.vy * sa, vy = ball.vx * sa + ball.vy * ca;
        ball.vx = vx * 1.05; ball.vy = vy * 1.05;
        ball.applySpeedCap();
        r3.deflected = true;
        return r3;
      default:
        return this._damage(ball, game, 1);
    }
  };
  // regen tick, called by Game each step
  Brick.prototype.tick = function (dt, neighborsAlive) {
    if (this.regenTimer > 0) {
      this.regenTimer -= dt;
      if (this.regenTimer <= 0) {
        if (neighborsAlive && this.regenTries > 0) {
          this.regenTries--;
          this.alive = true; this.broken = false;
          this.hp = this.maxHp; this.cracks = 0;
          this._regenGhost = false;
          this.wobbleT = 0.4;
          if (window.FX) window.FX.burst(this.x + this.w / 2, this.y + this.h / 2, { count: 8, color: '#52ffa8', spd: 120, size: 3, life: 0.5 });
        } else {
          this._regenGhost = false;
          this.regenTimer = 0;
        }
      }
    }
    if (this.wobbleT > 0) this.wobbleT -= dt;
    if (this.squash > 0) this.squash = Math.max(0, this.squash - dt * 4);
  };
  Brick.prototype.draw = function (ctx, t) {
    var x = this.x, y = this.y, w = this.w, h = this.h;
    var cx = x + w / 2, cy = y + h / 2;
    var wob = this.wobbleT > 0 ? this.wobbleT / 0.3 : 0;
    var hot = (game_time_now(t) - this.lastHitT) < 0.2;
    var sq = this.squash;
    ctx.save();
    if (wob > 0) {
      ctx.translate(cx, cy);
      ctx.rotate(Math.sin(t * 60) * 0.06 * wob);
      ctx.translate(-cx, -cy);
    }
    if (sq > 0) {
      ctx.translate(cx, cy);
      ctx.scale(1 + sq * 0.15, 1 - sq * 0.18);
      ctx.translate(-cx, -cy);
    }
    switch (this.kind) {
      case 'std': this._drawStd(ctx, t, hot); break;
      case 'armored': this._drawArmored(ctx, t, hot); break;
      case 'wedge': this._drawWedge(ctx, t, hot); break;
      case 'sentry': this._drawSentry(ctx, t, hot); break;
      case 'ghostly': this._drawGhost(ctx, t); break;
      case 'regen': this._drawRegen(ctx, t, hot); break;
      case 'void': this._drawVoid(ctx, t); break;
      default: this._drawStd(ctx, t, hot);
    }
    if (this.hasCapsule && this.alive && !this.broken) {
      var pulse = 0.6 + 0.4 * Math.sin(t * 6 + this.phase);
      ctx.fillStyle = 'rgba(255,255,255,' + (0.5 + pulse * 0.5) + ')';
      ctx.beginPath(); ctx.arc(cx, cy, 3 + pulse * 1.5, 0, U.TAU); ctx.fill();
      ctx.strokeStyle = 'rgba(255,176,32,' + pulse + ')';
      ctx.lineWidth = 1.5;
      ctx.beginPath(); ctx.arc(cx, cy, 6 + pulse * 2, 0, U.TAU); ctx.stroke();
    }
    ctx.restore();
  };
  function game_time_now(t) { return t || 0; }
  function rr(ctx, x, y, w, h, r) {
    if (ctx.roundRect) { ctx.beginPath(); ctx.roundRect(x, y, w, h, r); }
    else {
      ctx.beginPath();
      ctx.moveTo(x + r, y);
      ctx.arcTo(x + w, y, x + w, y + h, r);
      ctx.arcTo(x + w, y + h, x, y + h, r);
      ctx.arcTo(x, y + h, x, y, r);
      ctx.arcTo(x, y, x + w, y, r);
    }
  }
  Brick.prototype._drawStd = function (ctx, t, hot) {
    var hue = this.seedHue;
    if (hot) { ctx.shadowBlur = 12; ctx.shadowColor = 'hsl(' + hue + ',100%,60%)'; }
    var g = ctx.createLinearGradient(0, this.y, 0, this.y + this.h);
    g.addColorStop(0, 'hsl(' + hue + ',85%,62%)');
    g.addColorStop(1, 'hsl(' + hue + ',85%,30%)');
    rr(ctx, this.x, this.y, this.w, this.h, 7);
    ctx.fillStyle = g; ctx.fill();
    ctx.shadowBlur = 0;
    ctx.strokeStyle = 'hsl(' + hue + ',100%,75%)'; ctx.lineWidth = 1.5; ctx.stroke();
    // inner highlight
    ctx.strokeStyle = 'rgba(255,255,255,0.35)'; ctx.lineWidth = 1;
    ctx.beginPath(); ctx.moveTo(this.x + 6, this.y + 6); ctx.lineTo(this.x + this.w - 6, this.y + 6); ctx.stroke();
  };
  Brick.prototype._drawArmored = function (ctx, t, hot) {
    var x = this.x, y = this.y, w = this.w, h = this.h;
    if (hot) { ctx.shadowBlur = 14; ctx.shadowColor = '#9fc4ff'; }
    var g = ctx.createLinearGradient(0, y, 0, y + h);
    g.addColorStop(0, '#8fa3bf'); g.addColorStop(0.5, '#4a5a74'); g.addColorStop(1, '#252f42');
    rr(ctx, x, y, w, h, 5);
    ctx.fillStyle = g; ctx.fill();
    ctx.shadowBlur = 0;
    ctx.strokeStyle = '#b9cfe8'; ctx.lineWidth = 2; ctx.stroke();
    // hex bolts
    ctx.fillStyle = '#c8d8ee';
    var bx = [x + 12, x + w / 2, x + w - 12], by = y + h / 2;
    for (var i = 0; i < 3; i++) {
      ctx.beginPath();
      ctx.arc(bx[i], by, 3, 0, U.TAU);
      ctx.fill();
      ctx.strokeStyle = '#2a3548'; ctx.lineWidth = 1;
      ctx.beginPath();
      for (var k = 0; k < 6; k++) {
        var a = k / 6 * U.TAU + 0.5;
        var px = bx[i] + Math.cos(a) * 3.5, py = by + Math.sin(a) * 3.5;
        if (k === 0) ctx.moveTo(px, py); else ctx.lineTo(px, py);
      }
      ctx.closePath(); ctx.stroke();
    }
    // cracks by damage stage
    ctx.strokeStyle = 'rgba(20,26,40,0.9)'; ctx.lineWidth = 1.5;
    for (var c = 0; c < this.cracks; c++) {
      ctx.beginPath();
      var sx = x + w * (0.25 + 0.25 * c);
      ctx.moveTo(sx, y + 4);
      ctx.lineTo(sx - 5, y + h * 0.5);
      ctx.lineTo(sx + 4, y + h - 4);
      ctx.stroke();
    }
  };
  Brick.prototype._drawWedge = function (ctx, t, hot) {
    var x = this.x, y = this.y, w = this.w, h = this.h, ch = 10;
    if (hot) { ctx.shadowBlur = 12; ctx.shadowColor = '#ffb020'; }
    ctx.beginPath();
    ctx.moveTo(x + ch, y);
    ctx.lineTo(x + w - ch, y);
    ctx.lineTo(x + w, y + ch);
    ctx.lineTo(x + w, y + h - ch);
    ctx.lineTo(x + w - ch, y + h);
    ctx.lineTo(x + ch, y + h);
    ctx.lineTo(x, y + h - ch);
    ctx.lineTo(x, y + ch);
    ctx.closePath();
    var g = ctx.createLinearGradient(0, y, 0, y + h);
    g.addColorStop(0, '#7a5a20'); g.addColorStop(1, '#3a2c10');
    ctx.fillStyle = g; ctx.fill();
    ctx.shadowBlur = 0;
    ctx.strokeStyle = '#ffb020'; ctx.lineWidth = 2; ctx.stroke();
    // bright chevron on vulnerable face (orient 0=down,90=left,180=up,270=right)
    var pulse = 0.6 + 0.4 * Math.sin(t * 5 + this.phase);
    ctx.strokeStyle = 'rgba(255,208,110,' + pulse + ')';
    ctx.lineWidth = 3;
    var mx = x + w / 2, my = y + h / 2, s = 8;
    ctx.beginPath();
    if (this.orient === 0) { ctx.moveTo(mx - s, y + ch + 4); ctx.lineTo(mx, y + ch + 10); ctx.lineTo(mx + s, y + ch + 4); }
    else if (this.orient === 180) { ctx.moveTo(mx - s, y + h - ch - 4); ctx.lineTo(mx, y + h - ch - 10); ctx.lineTo(mx + s, y + h - ch - 4); }
    else if (this.orient === 90) { ctx.moveTo(x + w - ch - 4, my - s); ctx.lineTo(x + w - ch - 10, my); ctx.lineTo(x + w - ch - 4, my + s); }
    else { ctx.moveTo(x + ch + 4, my - s); ctx.lineTo(x + ch + 10, my); ctx.lineTo(x + ch + 4, my + s); }
    ctx.stroke();
    // dim glyph opposite
    ctx.strokeStyle = 'rgba(255,176,32,0.25)'; ctx.lineWidth = 1.5;
    ctx.beginPath();
    if (this.orient === 0) { ctx.moveTo(mx - s, y + h - ch - 4); ctx.lineTo(mx, y + h - ch - 10); ctx.lineTo(mx + s, y + h - ch - 4); }
    else if (this.orient === 180) { ctx.moveTo(mx - s, y + ch + 4); ctx.lineTo(mx, y + ch + 10); ctx.lineTo(mx + s, y + ch + 4); }
    else if (this.orient === 90) { ctx.moveTo(x + ch + 4, my - s); ctx.lineTo(x + ch + 10, my); ctx.lineTo(x + ch + 4, my + s); }
    else { ctx.moveTo(x + w - ch - 4, my - s); ctx.lineTo(x + w - ch - 10, my); ctx.lineTo(x + w - ch - 4, my + s); }
    ctx.stroke();
  };
  Brick.prototype._drawSentry = function (ctx, t, hot) {
    var cx = this.x + this.w / 2, cy = this.y + this.h / 2;
    var R = Math.min(this.w, this.h) / 2 - 2;
    if (hot) { ctx.shadowBlur = 14; ctx.shadowColor = '#4fd8ff'; }
    ctx.beginPath();
    for (var i = 0; i < 8; i++) {
      var a = i / 8 * U.TAU + Math.PI / 8;
      var px = cx + Math.cos(a) * R, py = cy + Math.sin(a) * R;
      if (i === 0) ctx.moveTo(px, py); else ctx.lineTo(px, py);
    }
    ctx.closePath();
    ctx.fillStyle = '#0d2438'; ctx.fill();
    ctx.shadowBlur = 0;
    ctx.strokeStyle = '#4fd8ff'; ctx.lineWidth = 2; ctx.stroke();
    // speedometer gauge
    var needleA = Math.PI * 0.75 + (0.5 + 0.5 * Math.sin(t * 2 + this.phase)) * Math.PI * 1.5;
    ctx.strokeStyle = '#52ffa8'; ctx.lineWidth = 2;
    ctx.beginPath();
    ctx.moveTo(cx, cy);
    ctx.lineTo(cx + Math.cos(needleA) * R * 0.6, cy + Math.sin(needleA) * R * 0.6);
    ctx.stroke();
    ctx.strokeStyle = 'rgba(79,216,255,0.4)'; ctx.lineWidth = 1;
    ctx.beginPath(); ctx.arc(cx, cy, R * 0.7, Math.PI * 0.75, Math.PI * 2.25); ctx.stroke();
  };
  Brick.prototype._drawGhost = function (ctx, t) {
    var op = this.opacity(t);
    ctx.globalAlpha = op;
    rr(ctx, this.x, this.y, this.w, this.h, 7);
    ctx.fillStyle = 'rgba(120,190,255,' + (0.18 + op * 0.25) + ')';
    ctx.fill();
    ctx.setLineDash([5, 4]);
    ctx.strokeStyle = 'rgba(160,220,255,0.8)';
    ctx.lineWidth = 1.5;
    ctx.stroke();
    ctx.setLineDash([]);
    ctx.globalAlpha = 1;
  };
  Brick.prototype._drawRegen = function (ctx, t, hot) {
    var regrowing = this.regenTimer > 0;
    var x = this.x, y = this.y, w = this.w, h = this.h;
    var hue = this.seedHue;
    if (regrowing) {
      // ghost + regrow progress: scale pop near end
      var p = 1 - this.regenTimer / 4;
      ctx.globalAlpha = 0.25 + p * 0.5;
      var sc = 0.7 + p * 0.3 + (p > 0.85 ? Math.sin((p - 0.85) / 0.15 * Math.PI) * 0.12 : 0);
      ctx.translate(x + w / 2, y + h / 2);
      ctx.scale(sc, sc);
      ctx.translate(-(x + w / 2), -(y + h / 2));
    }
    if (hot) { ctx.shadowBlur = 12; ctx.shadowColor = '#52ffa8'; }
    var g = ctx.createLinearGradient(0, y, 0, y + h);
    g.addColorStop(0, 'hsl(' + hue + ',70%,45%)');
    g.addColorStop(1, 'hsl(' + hue + ',70%,20%)');
    rr(ctx, x, y, w, h, 7);
    ctx.fillStyle = g; ctx.fill();
    ctx.shadowBlur = 0;
    ctx.strokeStyle = '#52ffa8'; ctx.lineWidth = 1.5; ctx.stroke();
    if (this.cracks > 0 || regrowing) {
      ctx.strokeStyle = 'rgba(0,0,0,0.6)'; ctx.lineWidth = 1.2;
      ctx.beginPath();
      ctx.moveTo(x + w * 0.3, y + 4);
      ctx.lineTo(x + w * 0.42, y + h * 0.5);
      ctx.lineTo(x + w * 0.3, y + h - 4);
      if (this.cracks > 1) {
        ctx.moveTo(x + w * 0.7, y + 4);
        ctx.lineTo(x + w * 0.6, y + h * 0.55);
        ctx.lineTo(x + w * 0.72, y + h - 4);
      }
      ctx.stroke();
    }
    ctx.globalAlpha = 1;
  };
  Brick.prototype._drawVoid = function (ctx, t) {
    var x = this.x, y = this.y, w = this.w, h = this.h;
    var g = ctx.createLinearGradient(0, y, 0, y + h);
    g.addColorStop(0, '#0a0714'); g.addColorStop(1, '#04030a');
    rr(ctx, x, y, w, h, 6);
    ctx.fillStyle = g; ctx.fill();
    ctx.strokeStyle = 'rgba(180,77,255,0.75)'; ctx.lineWidth = 2; ctx.stroke();
    // inner void swirl: cheap rotating arc pair
    var cx = x + w / 2, cy = y + h / 2;
    ctx.strokeStyle = 'rgba(106,61,240,0.55)';
    ctx.lineWidth = 2;
    var a0 = t * 1.4 + this.phase;
    ctx.beginPath(); ctx.arc(cx, cy, Math.min(w, h) * 0.28, a0, a0 + 2.2); ctx.stroke();
    ctx.beginPath(); ctx.arc(cx, cy, Math.min(w, h) * 0.16, a0 + Math.PI, a0 + Math.PI + 2.2); ctx.stroke();
  };

  // ======================================================================
  // Capsule
  // ======================================================================
  var CAP_COLORS = {
    wide: '#52ffa8', magnet: '#b44dff', multiball: '#ffb020', laser: '#ff4fd8',
    heavy: '#ff7a3d', phantom: '#6a3df0', splitter: '#c8ff4f', slowmo: '#4fd8ff',
    shield: '#0f6f9c', lifeup: '#ff4f6e', multiplier: '#ffb020',
    shrink: '#ff4f6e', speedup: '#ff4f6e', invert: '#ff4f6e'
  };
  var CAP_GLYPH = {
    wide: 'W', magnet: 'M', multiball: '3', laser: 'L', heavy: 'H', phantom: 'P',
    splitter: 'S', slowmo: 'T', shield: 'S', lifeup: '+', multiplier: '×2',
    shrink: '▼', speedup: '»', invert: '?'
  };
  function Capsule(x, y, powerId) {
    this.x = x; this.y = y;
    this.powerId = powerId;
    this.t = 0;
    this.vy = 300;
    this.sway = Math.random() * U.TAU;
    this.alive = true;
  }
  Capsule.prototype.update = function (dt) {
    this.t += dt;
    this.y += this.vy * dt;
    this.x += Math.sin(this.t * 3 + this.sway) * 40 * dt;
    if (this.y > 1340 + 40) this.alive = false;
  };
  Capsule.prototype.rect = function () {
    return { x: this.x - 17, y: this.y - 9, w: 34, h: 18 };
  };
  Capsule.prototype.draw = function (ctx, t) {
    var col = CAP_COLORS[this.powerId] || '#4fd8ff';
    var x = this.x, y = this.y;
    // trail
    ctx.globalAlpha = 0.3;
    ctx.fillStyle = col;
    rr(ctx, x - 12, y - 26, 24, 12, 6); ctx.fill();
    ctx.globalAlpha = 1;
    var pulse = 0.5 + 0.5 * Math.sin(t * 8 + this.sway);
    if (pulse > 0.85) { ctx.shadowBlur = 12; ctx.shadowColor = col; }
    rr(ctx, x - 17, y - 9, 34, 18, 9);
    ctx.fillStyle = 'rgba(6,10,24,0.9)'; ctx.fill();
    ctx.strokeStyle = col; ctx.lineWidth = 2; ctx.stroke();
    ctx.shadowBlur = 0;
    ctx.fillStyle = col;
    ctx.font = 'bold 13px monospace';
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    ctx.fillText(CAP_GLYPH[this.powerId] || '?', x, y + 1);
  };

  E.Ball = Ball;
  E.Paddle = Paddle;
  E.Brick = Brick;
  E.Capsule = Capsule;
  E.CAPSULE_COLORS = CAP_COLORS;
  E.BRICK_SCORE = KIND_SCORE;
})();