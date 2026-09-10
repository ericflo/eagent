/* ============================================================
   Overdrive Breakout — game.js
   Core game: states, fixed-step physics, collision, overdrive,
   power-ups, combo/score, input (mouse/keyboard/touch).
   Field is 1080x1920 logical, scaled + letterboxed by main.js.
   ============================================================ */
'use strict';

// ---- power-up definitions --------------------------------------------------
const PU = {
  FIRE:   { kind: 'ball', type: 'fire',   label: 'FIRE',    color: '#ff7a1a' },
  PLASMA: { kind: 'ball', type: 'plasma', label: 'PLASMA',  color: '#7df9ff' },
  HEAVY:  { kind: 'ball', type: 'heavy',  label: 'HEAVY',   color: '#ff4444' },
  MULTI:  { kind: 'multi', label: 'MULTI',  color: '#7dff8a' },
  WIDE:   { kind: 'wide', label: 'WIDE',   color: '#4dc9ff', dur: 12 },
  SLOWMO: { kind: 'slow', label: 'SLOW-MO',color: '#e58cff', dur: 6 },
  MAGNET: { kind: 'magnet', label: 'MAGNET', color: '#ffd23f', dur: 14 },
  SHIELD: { kind: 'shield', label: 'SHIELD', color: '#ffffff' }
};

class Game {
  constructor(canvas) {
    this.canvas = canvas;
    this.ctx = canvas.getContext('2d');
    this.fx = new FX();
    this.state = 'title';            // title | howto | ready | play | levelclear | gameover | pause
    this.stateT = 0;                 // time in current state (s)
    this.time = 0;                   // global time (s)
    this.level = 0;
    this.bricks = [];
    this.balls = [];
    this.powerups = [];              // falling capsules
    this.score = 0;
    this.combo = 0;                  // unbroken streak counter (bricks w/o paddle touch or loss)
    this.mult = 1;                   // score multiplier
    this.hiScore = 0;
    try { this.hiScore = parseInt(localStorage.getItem('od_hi') || '0', 10) || 0; } catch (e) {}

    // overdrive
    this.overdrive = false;
    this.odTime = 0;                 // continuous time ball spent up top
    this.odTier = 0;                 // 0..3 (none / HOT / BLAZING / SUPERNOVA)
    this.TIERS = ['HOT', 'BLAZING', 'SUPERNOVA'];

    // paddle
    this.paddle = {
      x: FIELD_W / 2, y: FIELD_H - 260,
      w: 200, baseW: 200, h: 28,
      vx: 0, vy: 0,
      prevY: 0,
      minX: 130, maxX: FIELD_W - 130,
      yBandTop: FIELD_H - 420, yBandBot: FIELD_H - 200,
      magnet: 0, wide: 0, lungeT: 0, lungeTarget: 0
    };
    this.smashReqT = 0;              // >0 = a flick-up smash intent is pending (0.35s window)
    this.shield = false;
    this.slowmo = 0;                 // seconds remaining
    this.lives = 3;
    this.input = { px: FIELD_W / 2, py: FIELD_H - 260, usePointer: false,
                   kx: 0, ky: 0, keys: {}, touch: null, smashReq: false };
    this.stickEnabled = false;       // on-screen thumbstick toggle
    this.stickZone = { x: FIELD_W / 2, y: FIELD_H - 260, active: false };
    this.bgStars = [];
    for (let i = 0; i < 90; i++) {
      this.bgStars.push({ x: Math.random() * FIELD_W, y: Math.random() * FIELD_H,
        s: 0.5 + Math.random() * 2, v: 6 + Math.random() * 30 });
    }
    this.bgHue = 220;
    this.startLevel(0);
    this.state = 'title'; this.stateT = 0; // show title screen first (startLevel set 'ready')
    this.bindInput();
  }

  // ---- level setup ---------------------------------------------------------
  startLevel(idx) {
    this.level = idx % LEVELS.length;
    const L = LEVELS[this.level];
    this.bricks = [];
    for (let r = 0; r < L.rows.length; r++) {
      const row = L.rows[r];
      for (let c = 0; c < COLS; c++) {
        const ch = row[c] || '.';
        let kind = B_EMPTY;
        if (ch === 'X') kind = B_STD;
        else if (ch === 'A') kind = B_ANGLE_STEEP;
        else if (ch === 'a') kind = B_ANGLE_SHALLOW;
        else if (ch === 'S') kind = B_SPEED;
        else if (ch === 'P') kind = B_PHASE;
        else if (ch === 'R') kind = B_REFLECT;
        else if (ch === '$') kind = B_PAYDIRT;
        else if (ch === '#') kind = B_STEEL;
        if (kind !== B_EMPTY) this.bricks.push(new Brick(kind, c, r));
      }
    }
    this.balls = [];
    const b = new Ball(this.paddle.x, this.paddle.y - 40, 0, 0, 'std');
    b.stuck = true;                  // stuck to paddle until launch
    this.balls.push(b);
    this.powerups = [];
    this.overdrive = false; this.odTime = 0; this.odTier = 0;
    this.combo = 0; this.mult = 1;
    this.state = 'ready'; this.stateT = 0;
    this.shield = false; this.slowmo = 0;
    this.paddle.w = this.paddle.baseW; this.paddle.magnet = 0;
    this.paddle.lungeT = 0; this.paddle.lungeTarget = 0;
    this.smashReqT = 0; this.input.smashReq = false;
    AudioSys.setMusicIntensity(0);
  }

  topBrickY() {
    // y of the topmost alive brick (for overdrive detection)
    let top = Infinity;
    for (const br of this.bricks) {
      if (br.alive && br.y < top) top = br.y;
    }
    return top;
  }

  // ---- state transitions ---------------------------------------------------
  launchBall() {
    for (const b of this.balls) {
      if (b.stuck) {
        b.stuck = false;
        const a = -Math.PI / 2 + (Math.random() - 0.5) * 0.6;
        b.vx = Math.cos(a) * b.baseSpeed;
        b.vy = Math.sin(a) * b.baseSpeed;
        AudioSys.launch();
        this.fx.burst(b.x, b.y, 10, '#9cf', 3);
      }
    }
    if (this.state === 'ready') { this.state = 'play'; this.stateT = 0; }
  }

  loseBall() {
    // shield save
    if (this.shield) {
      this.shield = false;
      const b = this.balls[0] || (this.balls[0] = new Ball(this.paddle.x, this.paddle.y - 40, 0, 0, 'std'));
      {
        b.dead = false; b.stuck = false;
        b.y = this.paddle.y - 60;
        b.vy = -Math.abs(b.vy || 8); b.vx *= 0.5;
        b.setSpeed(b.baseSpeed);
      }
      this.fx.ring(this.paddle.x, FIELD_H - 140, 260, '#fff', 8);
      AudioSys.powerup();
      this.fx.popup(this.paddle.x, FIELD_H - 260, 'SHIELD SAVE!', '#fff', 34);
      return;
    }
    // multiball: only a real loss when the LAST ball drops
    if (this.balls.length > 1) return;

    this.lives--;
    this.combo = 0; this.mult = 1;
    this.overdrive = false; this.odTime = 0; this.odTier = 0;
    AudioSys.setMusicIntensity(0);
    AudioSys.jingleLoss();
    this.fx.addShake(18);
    this.fx.doFlash(0);
    if (this.lives <= 0) {
      this.state = 'gameover'; this.stateT = 0;
      if (this.score > this.hiScore) {
        this.hiScore = this.score;
        try { localStorage.setItem('od_hi', String(this.score)); } catch (e) {}
      }
    } else {
      const b = new Ball(this.paddle.x, this.paddle.y - 40, 0, 0, 'std');
      b.stuck = true;
      this.balls = [b];
      this.state = 'ready'; this.stateT = 0;
    }
  }

  addScore(base, x, y) {
    const pts = Math.round(base * this.mult);
    this.score += pts;
    this.fx.popup(x, y, '+' + pts, this.mult > 3 ? '#ffd23f' : '#cfe6ff', 22 + Math.min(20, this.mult * 2));
    return pts;
  }

  bumpCombo() {
    this.combo++;
    // multiplier grows from streak + overdrive time
    const streakMult = 1 + Math.floor(this.combo / 4) * 0.5;
    const odMult = 1 + this.odTime * 1.2;
    this.mult = Math.min(20, streakMult + odMult);
  }

  // ---- brick hit resolution -------------------------------------------------
  hitBrick(br, ball) {
    const speed = ball.speed();
    // angle from vertical: 0 deg = straight-on (steep), 90 = pure horizontal
    const ang = Math.atan2(Math.abs(ball.vx), Math.abs(ball.vy)) * 180 / Math.PI;
    const cx = br.x + br.w / 2, cy = br.y + br.h / 2;

    // wrong-angle / too-slow feedback: bounce, no break
    if (br.kind === B_ANGLE_STEEP || br.kind === B_ANGLE_SHALLOW || br.kind === B_SPEED) {
      if (!br.canBreak(speed, ang, ball.type)) {
        br.wobble = 0.35; br.flashT = 0.15;
        AudioSys.clang(); AudioSys.thud();
        this.fx.burst(cx, cy, 6, '#ffd23f', 2);
        this.fx.popup(cx, cy - 40, br.kind === B_SPEED ? 'TOO SLOW' : 'WRONG ANGLE', '#ffd23f', 20);
        this.fx.addShake(3);
        return false;
      }
    }
    if (br.kind === B_REFLECT && !(ball.type === 'fire' || ball.type === 'plasma' || ball.type === 'heavy')) {
      br.crackSeed = 0.4;
      AudioSys.reflect();
      this.fx.burst(cx, cy, 5, '#cfe0ff', 2);
      return false;
    }
    if (br.kind === B_STEEL) {
      AudioSys.thud();
      this.fx.burst(cx, cy, 4, '#9aa', 2);
      return false;
    }

    // ---- BREAK! ----
    br.alive = false;
    this.bumpCombo();
    this.addScore(br.score(), cx, cy);
    AudioSys.brickBreak(this.combo, br.kind);
    this.fx.shards(cx, cy, 10 + Math.min(14, this.combo), br.color(), 4 + this.mult * 0.2);
    this.fx.burst(cx, cy, 8, '#fff', 3);
    this.fx.addShake(2 + Math.min(10, this.mult * 0.6));
    this.fx.ring(cx, cy, 40 + this.mult * 6, br.color(), 4);
    if (this.mult > 4) this.fx.doFlash(45);
    if (this.mult > 8) this.fx.addHitPause(3);

    // high overdrive: chain ignition of nearby bricks
    if (this.odTier >= 2) {
      this.chainIgnite(cx, cy, this.odTier - 1);
    }

    if (br.kind === B_PAYDIRT) {
      this.spawnPowerup(cx, cy);
      this.addScore(200, cx, cy - 50);
      this.fx.ring(cx, cy, 200, '#ffd23f', 10);
      this.fx.burst(cx, cy, 26, '#ffd23f', 6);
      AudioSys.powerup();
    } else if (Math.random() < 0.06) {
      this.spawnPowerup(cx, cy);
    }
    return true;
  }

  chainIgnite(cx, cy, count) {
    let n = 0;
    for (const br of this.bricks) {
      if (!br.alive || br.kind === B_STEEL || br.kind === B_REFLECT) continue;
      if (br.kind === B_ANGLE_STEEP || br.kind === B_ANGLE_SHALLOW || br.kind === B_SPEED) continue;
      const d = Math.hypot(br.x + br.w / 2 - cx, br.y + br.h / 2 - cy);
      if (d < 220) {
        br.alive = false;
        this.bumpCombo();
        this.addScore(br.score(), br.x + br.w / 2, br.y);
        this.fx.shards(br.x + br.w / 2, br.y + br.h / 2, 8, br.color(), 4);
        this.fx.ring(br.x + br.w / 2, br.y + br.h / 2, 60, '#ff7a1a', 4);
        AudioSys.brickBreak(this.combo, 'chain');
        if (++n >= count) break;
      }
    }
    if (n > 0) { this.fx.addShake(6); this.fx.doFlash(30); }
  }

  spawnPowerup(x, y) {
    const keys = Object.keys(PU);
    // guarantee variety: force a ball-type powerup if the last 3 drops weren't
    this.puStreak = (this.puStreak || 0) + 1;
    let k = keys[(Math.random() * keys.length) | 0];
    if (k === 'SLOWMO' && Math.random() < 0.5) k = 'MULTI';
    const isBall = PU[k].kind === 'ball' || k === 'MULTI';
    if (!isBall && this.puStreak > 3) {
      const ballKeys = ['FIRE', 'PLASMA', 'HEAVY', 'MULTI'];
      k = ballKeys[(Math.random() * ballKeys.length) | 0];
    }
    if (PU[k].kind === 'ball' || k === 'MULTI') this.puStreak = 0;
    if (this.powerups.length > 6) return;
    this.powerups.push({ x, y, vy: 4, def: PU[k], t: 0 });
  }

  // ---- overdrive detection & escalation ------------------------------------
  updateOverdrive(dt) {
    const top = this.topBrickY();
    let anyAbove = false;
    for (const b of this.balls) {
      if (b.y < top - b.r) { anyAbove = true; break; }
    }
    if (anyAbove) {
      if (!this.overdrive) {
        this.overdrive = true;
        AudioSys.overdriveStart();
        this.fx.ring(FIELD_W / 2, top - 100, 500, '#7df9ff', 14);
        this.fx.doFlash(200);
        this.fx.popup(FIELD_W / 2, FIELD_H * 0.52, 'OVERDRIVE!', '#7df9ff', 64);
        this.fx.addShake(10);
      }
      this.odTime += dt;
      // tier: HOT(1) BLAZING(2) SUPERNOVA(3) at 2s / 5s / 9s up top
      const tier = this.odTime > 9 ? 3 : this.odTime > 5 ? 2 : this.odTime > 2 ? 1 : 0;
      if (tier > this.odTier) {
        this.odTier = tier;
        AudioSys.chord(tier);
        this.fx.doFlash(tier * 40);
        this.fx.addShake(8 + tier * 4);
        this.fx.popup(FIELD_W / 2, FIELD_H * 0.35, this.TIERS[tier - 1], 'hsl(' + (20 + tier * 40) + ',100%,60%)', 70);
        this.fx.ring(FIELD_W / 2, FIELD_H * 0.4, 700, '#ff7a1a', 16);
        this.fx.addHitPause(4);
      }
      // music intensity ramps with overdrive
      AudioSys.setMusicIntensity(Math.min(1, 0.2 + this.odTime * 0.09));
    } else {
      // grace: multiplier and meter decay smoothly when ball falls back down
      if (this.overdrive) {
        this.odTime = Math.max(0, this.odTime - dt * 0.8);
        if (this.odTime === 0) this.overdrive = false;
      }
      this.odTier = Math.min(this.odTier, this.odTime > 9 ? 3 : this.odTime > 5 ? 2 : this.odTime > 2 ? 1 : 0);
      AudioSys.setMusicIntensity(this.odTime > 0.2 ? Math.min(1, 0.2 + this.odTime * 0.09) : 0);
    }
    // SUPERNOVA ignites the ball itself (kills bricks near its path)
    if (this.odTier >= 3) {
      for (const b of this.balls) {
        if (Math.random() < dt * 8) {
          this.fx.spawnParticle(b.x, b.y, (Math.random() - 0.5) * 4, -2, 0.5, 5, '#ff9a3a', 2);
        }
      }
    }
  }

  // ---- physics: one substep (dt ~ 1/120 s) ---------------------------------
  step(dt) {
    this.fx.update(dt); // particles, shake, flash decay every substep
    const P = this.paddle;
    P.prevY = P.y;

    // slow-mo time dilation (also used for dramatic last-ball saves)
    let scale = 1;
    if (this.slowmo > 0) scale = 0.4;
    const d = dt * scale;

    // ---- paddle movement ----
    const inp = this.input;
    let tx = P.x, ty = P.y;
    if (inp.usePointer) { tx = inp.px; ty = inp.py; }
    else {
      // keyboard / thumbstick axes
      tx = P.x + inp.kx * 18;
      ty = P.y + inp.ky * 10;
      ty = Math.max(P.yBandTop, Math.min(P.yBandBot, ty));
    }
    // touch absolute drag
    if (inp.touch && inp.touch.activated) {
      tx = inp.touch.tx; ty = inp.touch.ty;
      if (inp.touch.smash) {
        inp.touch.smash = false;
        inp.smashReq = true;
        // visual/feel feedback: brief upward paddle lunge + rim flash, even with no ball nearby
        P.lungeT = 0.12; P.lungeTarget = Math.max(P.yBandTop, P.y - 46);
        this.fx.ring(P.x, P.y, 70, '#ffd23f', 5);
        AudioSys.whoosh();
      }
    }
    // flick-up lunge: paddle darts upward briefly, then returns to the touch target
    if (P.lungeT > 0) {
      P.lungeT -= dt;
      if (P.lungeT <= 0 || P.y <= P.lungeTarget) { P.lungeT = 0; }
      else ty = Math.min(ty, P.lungeTarget);
    }
    // pending smash intent decays: unused within 0.35s, it expires (no stockpiling)
    if (inp.smashReq && this.smashReqT === 0) this.smashReqT = 0.35;
    if (this.smashReqT > 0) {
      this.smashReqT -= dt;
      if (this.smashReqT <= 0) { this.smashReqT = 0; inp.smashReq = false; }
    }
    const nx = Math.max(P.minX, Math.min(P.maxX, tx));
    const ny = Math.max(P.yBandTop, Math.min(P.yBandBot, ty));
    P.vx = (nx - P.x) / d;
    P.vy = (ny - P.y) / d;
    P.x = nx; P.y = ny;
    if (P.wide > 0) { P.wide -= d; if (P.wide <= 0) P.w = P.baseW; }
    if (P.magnet > 0) P.magnet -= d;

    // ---- balls ----
    for (const b of this.balls) {
      b.updateTimer(d);
      if (b.stuck) {
        b.x = P.x + (b.stuckDx || 0);
        b.y = P.y - P.h / 2 - b.radius() - 4;
        continue;
      }
      // move with substep collisions (split into small moves to avoid tunneling)
      const spd = b.speed();
      const steps = Math.max(1, Math.ceil(spd / 12));
      const sd = d / steps;
      for (let s = 0; s < steps; s++) this.moveBall(b, sd);

      // anti-stuck: near-pure-horizontal or near-vertical runs get tiny jitter
      const vx = b.vx, vy = b.vy;
      if (Math.abs(vy) < 0.6 || Math.abs(vx) < 0.6) {
        b.straightT += d;
        if (b.straightT > 0.8) {
          b.vx += (Math.random() - 0.5) * 0.9;
          b.vy += (Math.random() - 0.5) * 0.9;
          if (Math.abs(b.vy) < 1.4) b.vy = b.vy >= 0 ? 1.4 : -1.4;
          b.setSpeed(b.baseSpeed * 1.05);
          b.straightT = 0;
        }
      } else b.straightT = 0;

      // walls
      const r = b.radius();
      if (b.x < r) { b.x = r; b.vx = Math.abs(b.vx); AudioSys.bounce(this.combo); this.fx.burst(b.x, b.y, 4, '#8cf', 2); }
      if (b.x > FIELD_W - r) { b.x = FIELD_W - r; b.vx = -Math.abs(b.vx); AudioSys.bounce(this.combo); this.fx.burst(b.x, b.y, 4, '#8cf', 2); }
      if (b.y < r) { b.y = r; b.vy = Math.abs(b.vy); AudioSys.bounce(this.combo); this.fx.burst(b.x, b.y, 4, '#8cf', 2); }
      // floor
      if (b.y > FIELD_H + r) {
        b.dead = true;
      }
      // magnet paddle hold
      if (P.magnet > 0 && !b.stuck && b.vy > 0 && b.y > P.y - P.h / 2 - r - 8 && b.y < P.y + 30 && Math.abs(b.x - P.x) < P.w / 2 + r) {
        b.stuck = true; b.stuckDx = b.x - P.x;
        AudioSys.launch();
      }
    }
    // remove dead balls; multi-ball handling
    for (let i = this.balls.length - 1; i >= 0; i--) if (this.balls[i].dead) this.balls.splice(i, 1);
    if (this.balls.length === 0) this.loseBall();

    // nudge overlapping balls apart
    for (let i = 0; i < this.balls.length; i++) for (let j = i + 1; j < this.balls.length; j++) {
      const a = this.balls[i], c = this.balls[j];
      const dx = c.x - a.x, dy = c.y - a.y, dist = Math.hypot(dx, dy) || 1;
      const min = a.radius() + c.radius();
      if (dist < min) {
        const push = (min - dist) / 2, ux = dx / dist, uy = dy / dist;
        a.x -= ux * push; a.y -= uy * push; c.x += ux * push; c.y += uy * push;
        // gentle velocity separation
        const dot = (a.vx * ux + a.vy * uy) - (c.vx * ux + c.vy * uy);
        if (dot > 0) { a.vx -= ux * dot * 0.5; a.vy -= uy * dot * 0.5; c.vx += ux * dot * 0.5; c.vy += uy * dot * 0.5; }
      }
    }

    // ---- paddle collision (with smash) ----
    for (const b of this.balls) {
      if (b.stuck) continue;
      const r = b.radius();
      if (b.vy > 0 && b.y + r >= P.y - P.h / 2 && b.y - r <= P.y + P.h / 2 && Math.abs(b.x - P.x) <= P.w / 2 + r) {
        b.y = P.y - P.h / 2 - r;
        // classic: hit position adjusts angle
        const off = Math.max(-1, Math.min(1, (b.x - P.x) / (P.w / 2)));
        const speedNow = Math.max(b.speed(), b.baseSpeed);
        // smash: upward paddle velocity transfers
        const paddleRise = Math.max(0, -P.vy); // vy negative = moving up
        let smash = Math.min(1, paddleRise / 700);
        // one-shot smash intent (touch flick-up / similar): if a ball contacts the
        // paddle while the request is pending, guarantee a strong smash and consume it
        if (this.input.smashReq) {
          this.input.smashReq = false; this.smashReqT = 0;
          smash = Math.max(smash, 0.8);
        }
        let ang = -Math.PI / 2 + off * 1.0;                     // up, fanned by offset
        ang += (Math.random() - 0.5) * 0.06;                    // tiny jitter
        let outSpeed = speedNow * (1 + smash * 0.55);
        outSpeed = Math.min(outSpeed, b.baseSpeed * 2.2);
        b.vx = Math.cos(ang) * outSpeed + P.vx * 0.12;
        b.vy = Math.sin(ang) * outSpeed;
        b.smashBoost = smash;
        this.combo = 0; // paddle touch breaks the streak (overdrive mult persists)
        AudioSys.bounce(Math.round(this.mult * 2));
        if (smash > 0.25) {
          AudioSys.whoosh();
          this.fx.burst(b.x, b.y, 14, '#ffd23f', 5);
          this.fx.ring(b.x, b.y, 120, '#ffd23f', 6);
          this.fx.addShake(6 * smash);
          this.fx.addHitPause(2);
          this.fx.popup(b.x, b.y - 50, 'SMASH!', '#ffd23f', 30);
        }
        this.fx.burst(b.x, b.y, 5, '#fff', 2);
      }
      // paddle moving UP can catch/redirect a ball from below its rim
      else if (P.vy < -200 && b.vy > 0 && b.y >= P.y - P.h / 2 && b.y <= P.y + P.h / 2 + r && Math.abs(b.x - P.x) <= P.w / 2 + r) {
        b.y = P.y - P.h / 2 - r;
        const off = Math.max(-1, Math.min(1, (b.x - P.x) / (P.w / 2)));
        const outSpeed = Math.min(b.baseSpeed * 1.9, b.speed() + 3);
        const ang = -Math.PI / 2 + off * 1.1;
        b.vx = Math.cos(ang) * outSpeed; b.vy = Math.sin(ang) * outSpeed;
        AudioSys.whoosh();
        this.fx.ring(b.x, b.y, 90, '#ffd23f', 5);
        this.fx.addShake(4);
      }
    }

    // ---- powerup capsules ----
    for (let i = this.powerups.length - 1; i >= 0; i--) {
      const u = this.powerups[i];
      u.y += u.vy; u.t += d;
      // catch
      if (u.y > P.y - P.h / 2 - 26 && u.y < P.y + 30 && Math.abs(u.x - P.x) < P.w / 2 + 30) {
        this.applyPowerup(u.def);
        this.powerups.splice(i, 1);
      } else if (u.y > FIELD_H + 40) this.powerups.splice(i, 1);
    }

    if (this.slowmo > 0) this.slowmo -= dt;

    // safety valve: if only REFLECT/STEEL bricks remain and no powerup is on
    // the field, rain down a ball-type powerup so the level stays finishable
    if (this.state === 'play' && this.powerups.length === 0) {
      let breakable = 0, reflect = 0;
      for (const br of this.bricks) {
        if (!br.alive || br.kind === B_STEEL) continue;
        if (br.kind === B_REFLECT) reflect++; else breakable++;
      }
      if (breakable === 0 && reflect > 0 && Math.random() < dt * 0.5) {
        const keys = ['FIRE', 'PLASMA', 'HEAVY'];
        const k = keys[(Math.random() * keys.length) | 0];
        this.powerups.push({ x: 100 + Math.random() * (FIELD_W - 200), y: -30, vy: 4, def: PU[k], t: 0 });
        this.fx.popup(FIELD_W / 2, FIELD_H * 0.3, 'AID INCOMING!', '#7df9ff', 30);
      }
    }

    // bricks anim + phase
    for (const br of this.bricks) br.update(dt);

    this.updateOverdrive(dt);

    // level clear?
    let remaining = 0;
    for (const br of this.bricks) if (br.alive && br.kind !== B_STEEL) remaining++;
    if (remaining === 0 && this.state === 'play') {
      this.state = 'levelclear'; this.stateT = 0;
      AudioSys.chord(3);
      this.fx.doFlash(60); this.fx.addShake(10);
      for (let i = 0; i < 40; i++)
        this.fx.spawnParticle(Math.random() * FIELD_W, Math.random() * FIELD_H * 0.5, (Math.random() - 0.5) * 6, -3, 1.5, 5, '#ffd23f', 2);
      if (this.score > this.hiScore) { this.hiScore = this.score; try { localStorage.setItem('od_hi', String(this.score)); } catch (e) {} }
    }
  }

  moveBall(b, d) {
    const r = b.radius();
    b.x += b.vx * d * 120;   // vx is px per 120Hz substep; d in seconds
    b.y += b.vy * d * 120;
    // brick collisions (circle vs rect, axis resolution)
    for (const br of this.bricks) {
      if (!br.alive) continue;
      if (br.kind === B_PHASE && !br.solidNow) continue;
      const hw = br.w / 2 + r, hh = br.h / 2 + r;
      const bcx = br.x + br.w / 2, bcy = br.y + br.h / 2;
      const dx = b.x - bcx, dy = b.y - bcy;
      if (Math.abs(dx) > hw || Math.abs(dy) > hh) continue;
      // penetration on each axis
      const px = hw - Math.abs(dx), py = hh - Math.abs(dy);
      if (px < py) {
        // horizontal side hit
        if (b.x < bcx) { b.x -= px; b.vx = -Math.abs(b.vx); } else { b.x += px; b.vx = Math.abs(b.vx); }
      } else {
        // vertical side hit
        if (b.y < bcy) { b.y -= py; b.vy = -Math.abs(b.vy); } else { b.y += py; b.vy = Math.abs(b.vy); }
      }
      const broke = this.hitBrick(br, b);
      // fireball pierces standard bricks (keeps going)
      if (broke && b.type === 'fire' && br.kind === B_STD) continue;
      break;
    }
    // trail
    if (Math.random() < 0.7) this.fx.spawnParticle(b.x, b.y, (Math.random() - 0.5) * 0.6, (Math.random() - 0.5) * 0.6,
      0.25 + this.mult * 0.02, 3 + (b.type !== 'std' ? 2 : 0), b.color(), 0);
  }

  applyPowerup(def) {
    AudioSys.powerup();
    this.fx.doFlash(120);
    this.fx.popup(this.paddle.x, this.paddle.y - 70, def.label + '!', def.color, 36);
    this.fx.burst(this.paddle.x, this.paddle.y - 30, 18, def.color, 5);
    if (def.kind === 'ball') {
      for (const b of this.balls) { b.type = def.type; b.timer = 10; }
    } else if (def.kind === 'multi') {
      const src = this.balls[0];
      if (!src) return;
      const baseType = src.type;
      for (let i = 0; i < 2 && this.balls.length < 6; i++) {
        const nb = new Ball(src.x, src.y, 0, 0, baseType);
        nb.timer = src.timer;
        const a = -Math.PI / 2 + (i === 0 ? -0.7 : 0.7) + (Math.random() - 0.5) * 0.2;
        nb.vx = Math.cos(a) * src.baseSpeed; nb.vy = Math.sin(a) * src.baseSpeed;
        this.balls.push(nb);
      }
    } else if (def.kind === 'wide') {
      this.paddle.wide = def.dur; this.paddle.w = this.paddle.baseW * 1.6;
    } else if (def.kind === 'slow') {
      this.slowmo = def.dur;
    } else if (def.kind === 'magnet') {
      this.paddle.magnet = def.dur;
    } else if (def.kind === 'shield') {
      this.shield = true;
    }
  }

  // ---- input ---------------------------------------------------------------
  bindInput() {
    const cv = this.canvas;
    const inp = this.input;

    // map client coords to field coords via letterbox transform (set by main.js)
    this.toField = (cx, cy) => ({ x: cx, y: cy }); // replaced by main.js
    const F = () => this.toField;

    cv.addEventListener('mousemove', (e) => {
      const f = F()(e.clientX, e.clientY);
      inp.px = f.x; inp.py = Math.max(this.paddle.yBandTop, Math.min(this.paddle.yBandBot, f.y));
      inp.usePointer = true;
    });
    cv.addEventListener('mousedown', (e) => {
      AudioSys.unlock();
      if (this.state === 'title') { this.state = 'howto'; this.stateT = 0; return; }
      if (this.state === 'howto') { this.startRun(); return; }
      if (this.state === 'ready') { this.launchBall(); return; }
      if (this.state === 'play') {
        // magnet launch
        for (const b of this.balls) {
          if (b.stuck) {
            b.stuck = false;
            const a = -Math.PI / 2 + Math.max(-1, Math.min(1, (b.x - this.paddle.x) / 200));
            b.vx = Math.cos(a) * b.baseSpeed; b.vy = Math.sin(a) * b.baseSpeed;
            AudioSys.launch();
          }
        }
      }
    });
    window.addEventListener('keydown', (e) => {
      inp.keys[e.code] = true;
      AudioSys.unlock();
      if (e.code === 'Space') {
        e.preventDefault();
        if (this.state === 'title') { this.state = 'howto'; this.stateT = 0; }
        else if (this.state === 'howto') this.startRun();
        else if (this.state === 'ready') this.launchBall();
        else if (this.state === 'play') {
          for (const b of this.balls) if (b.stuck) {
            b.stuck = false;
            b.vx = (Math.random() - 0.5) * 4; b.vy = -b.baseSpeed;
            AudioSys.launch();
          }
        }
      }
      if (e.code === 'KeyP' || e.code === 'Escape') this.togglePause();
      if (e.code === 'KeyM') this.muteToggle();
      if (['ArrowLeft','ArrowRight','ArrowUp','ArrowDown'].includes(e.code)) e.preventDefault();
    });
    window.addEventListener('keyup', (e) => { inp.keys[e.code] = false; });

    // ---- touch: drag anywhere = absolute paddle control ----
    let touchId = null;
    const touchMove = (t) => {
      const f = F()(t.clientX, t.clientY);
      // offset so the paddle rides ABOVE the thumb (thumb never covers it)
      inp.touch.tx = f.x;
      inp.touch.ty = Math.max(this.paddle.yBandTop, Math.min(this.paddle.yBandBot, f.y - 320));
      inp.touch.activated = true;
      inp.usePointer = false;
    };
    cv.addEventListener('touchstart', (e) => {
      e.preventDefault(); AudioSys.unlock();
      const t = e.changedTouches[0];
      if (touchId === null) { touchId = t.identifier; inp.touch = { id: touchId, tx: 0, ty: 0, activated: false, smash: false, startY: t.clientY, startX: t.clientX }; touchMove(t); }
      if (this.state === 'title') { this.state = 'howto'; this.stateT = 0; }
      else if (this.state === 'howto') this.startRun();
      else if (this.state === 'ready') this.launchBall();
      else if (this.state === 'play') {
        for (const b of this.balls) if (b.stuck) {
          b.stuck = false;
          b.vx = (Math.random() - 0.5) * 4; b.vy = -b.baseSpeed;
          AudioSys.launch();
        }
      }
    }, { passive: false });
    cv.addEventListener('touchmove', (e) => {
      e.preventDefault();
      for (const t of e.changedTouches) if (t.identifier === touchId) {
        touchMove(t);
        // flick up (fast upward thumb move) = SMASH request: boost paddle upward briefly
        const inp = this.input;
        if (inp.touch && inp.touch.startY - t.clientY > 140) inp.touch.smash = true;
      }
    }, { passive: false });
    const endTouch = (e) => {
      for (const t of e.changedTouches) if (t.identifier === touchId) { touchId = null; inp.touch = null; }
    };
    cv.addEventListener('touchend', endTouch);
    cv.addEventListener('touchcancel', endTouch);

    // HUD buttons
    document.getElementById('btn-sound').addEventListener('click', () => { AudioSys.unlock(); this.muteToggle(); });
    document.getElementById('btn-pause').addEventListener('click', () => this.togglePause());
    document.getElementById('btn-stick').addEventListener('click', () => {
      this.stickEnabled = !this.stickEnabled;
      try { localStorage.setItem('od_stick', this.stickEnabled ? '1' : '0'); } catch (e) {}
      AudioSys.launch();
    });
    try { this.stickEnabled = localStorage.getItem('od_stick') === '1'; } catch (e) {}
  }

  muteToggle() {
    const m = AudioSys.toggleMuted();
    document.getElementById('btn-sound').textContent = m ? '🔇' : '🔊';
  }

  togglePause() {
    if (this.state === 'play') { this.state = 'pause'; this.stateT = 0; AudioSys.setMusicIntensity(0); }
    else if (this.state === 'pause') { this.state = 'play'; }
  }

  startRun() {
    this.score = 0; this.lives = 3;
    this.startLevel(0);
  }

  // keyboard axes for step()
  pollKeys() {
    const k = this.input.keys;
    let x = 0, y = 0;
    if (k['ArrowLeft'] || k['KeyA']) x -= 1;
    if (k['ArrowRight'] || k['KeyD']) x += 1;
    if (k['ArrowUp'] || k['KeyW']) y -= 1;
    if (k['ArrowDown'] || k['KeyS']) y += 1;
    this.input.kx = x; this.input.ky = y;
  }

  // ---- render ---------------------------------------------------------------
  render(ctx, alpha) {
    const t = this.time;
    // interpolated paddle/ball positions could use alpha; at 120Hz substeps
    // we just render latest state (visually identical at 60fps).
    const fx = this.fx;
    ctx.save();
    ctx.translate(fx.shakeX, fx.shakeY);

    // ---- background: hue-shifting gradient + parallax stars ----
    const odGlow = this.odTime;
    this.bgHue += 0;
    const hue = 220 + odGlow * 6 + Math.sin(t * 0.7) * 12;
    const bg = ctx.createLinearGradient(0, 0, 0, FIELD_H);
    bg.addColorStop(0, `hsl(${hue}, 55%, ${8 + odGlow * 1.6}%)`);
    bg.addColorStop(1, `hsl(${hue + 40}, 60%, ${4 + odGlow * 1.2}%)`);
    ctx.fillStyle = bg;
    ctx.fillRect(-40, -40, FIELD_W + 80, FIELD_H + 80);

    // background pulse on overdrive
    if (this.overdrive) {
      ctx.globalCompositeOperation = 'lighter';
      ctx.globalAlpha = 0.05 + Math.min(0.14, odGlow * 0.02) * (0.6 + 0.4 * Math.sin(t * (2 + this.odTier * 3)));
      ctx.fillStyle = `hsl(${30 + this.odTier * 30},100%,50%)`;
      ctx.fillRect(0, 0, FIELD_W, FIELD_H);
      ctx.globalAlpha = 1;
      ctx.globalCompositeOperation = 'source-over';
    }

    // parallax stars reacting to combo
    ctx.fillStyle = 'rgba(200,220,255,0.5)';
    for (const s of this.bgStars) {
      const yy = (s.y + t * s.v * (1 + this.mult * 0.05)) % FIELD_H;
      ctx.globalAlpha = 0.15 + s.s / 4;
      ctx.fillRect(s.x, yy, s.s, s.s * 3);
    }
    ctx.globalAlpha = 1;

    // subtle grid reacting to combo
    ctx.strokeStyle = `hsla(${hue + 30},80%,60%,${0.04 + Math.min(0.1, this.mult * 0.01)})`;
    ctx.lineWidth = 1;
    ctx.beginPath();
    for (let x = 0; x <= FIELD_W; x += 120) { ctx.moveTo(x, 0); ctx.lineTo(x, FIELD_H); }
    for (let y = 0; y <= FIELD_H; y += 120) { ctx.moveTo(0, y); ctx.lineTo(FIELD_W, y); }
    ctx.stroke();

    // ---- bricks ----
    for (const br of this.bricks) br.draw(ctx, t);

    // ---- powerup capsules ----
    for (const u of this.powerups) {
      ctx.save();
      ctx.translate(u.x, u.y);
      ctx.rotate(Math.sin(u.t * 5) * 0.2);
      ctx.fillStyle = u.def.color;
      ctx.shadowColor = u.def.color; ctx.shadowBlur = 14;
      ctx.beginPath();
      ctx.roundRect(-26, -16, 52, 32, 8);
      ctx.fill();
      ctx.shadowBlur = 0;
      ctx.fillStyle = '#0a0a14';
      ctx.font = '700 15px system-ui, sans-serif';
      ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
      ctx.fillText(u.def.label, 0, 1);
      ctx.restore();
    }

    // ---- ball trails (additive) ----
    ctx.globalCompositeOperation = 'lighter';
    for (const b of this.balls) {
      // trail via recent particles handled in fx; draw glow streak:
      ctx.globalAlpha = 0.25 + Math.min(0.4, this.mult * 0.03);
      ctx.strokeStyle = b.color();
      ctx.lineWidth = b.radius() * 1.2;
      ctx.lineCap = 'round';
      ctx.beginPath();
      ctx.moveTo(b.x, b.y);
      ctx.lineTo(b.x - b.vx * 3 - this.odTier * 4 * Math.sign(b.vx || 1),
                 b.y - b.vy * 3 - this.odTier * 4 * Math.sign(b.vy || 1));
      ctx.stroke();
    }
    ctx.globalAlpha = 1;
    ctx.globalCompositeOperation = 'source-over';

    // ---- paddle ----
    this.drawPaddle(ctx, t);

    // ---- balls ----
    for (const b of this.balls) b.draw(ctx);

    // ---- fx layers ----
    fx.drawParticles(ctx);
    fx.drawRings(ctx);
    fx.drawPopups(ctx);

    // ---- HUD ----
    this.drawHUD(ctx, t);

    // vignette + scanlines at high overdrive
    if (this.odTier >= 1) {
      const v = ctx.createRadialGradient(FIELD_W / 2, FIELD_H / 2, FIELD_H * 0.3, FIELD_W / 2, FIELD_H / 2, FIELD_H * 0.75);
      v.addColorStop(0, 'rgba(0,0,0,0)');
      v.addColorStop(1, `rgba(0,0,20,${0.3 + this.odTier * 0.12})`);
      ctx.fillStyle = v;
      ctx.fillRect(0, 0, FIELD_W, FIELD_H);
      if (this.odTier >= 2) {
        ctx.globalAlpha = 0.05;
        ctx.fillStyle = '#fff';
        for (let y = (t * 120) % 8; y < FIELD_H; y += 8) ctx.fillRect(0, y, FIELD_W, 2);
        ctx.globalAlpha = 1;
      }
    }

    fx.drawFlash(ctx, FIELD_W, FIELD_H);

    // ---- state overlays ----
    this.drawStateOverlay(ctx, t);
    ctx.restore();
  }

  drawPaddle(ctx, t) {
    const P = this.paddle;
    ctx.save();
    ctx.translate(P.x, P.y);
    // energy rim intensity tracks multiplier + magnet
    const inten = Math.min(1, (this.mult - 1) / 8) + (P.magnet > 0 ? 0.4 : 0);
    const hue = 20 + Math.min(160, this.mult * 14) + (P.magnet > 0 ? 40 * Math.sin(t * 8) : 0);
    ctx.shadowColor = `hsl(${hue},100%,60%)`;
    ctx.shadowBlur = 10 + inten * 24;
    ctx.fillStyle = `hsl(${hue},80%,${55 + inten * 15}%)`;
    ctx.beginPath();
    ctx.roundRect(-P.w / 2, -P.h / 2, P.w, P.h, 12);
    ctx.fill();
    ctx.shadowBlur = 0;
    // inner core
    ctx.fillStyle = '#eaf4ff';
    ctx.beginPath();
    ctx.roundRect(-P.w / 2 + 6, -P.h / 2 + 5, P.w - 12, P.h - 12, 8);
    ctx.fill();
    // smash intent pending: bright rim flash so the flick-up gesture visibly registers
    if (this.input.smashReq) {
      ctx.strokeStyle = '#ffd23f'; ctx.lineWidth = 3 + 2 * Math.sin(t * 40);
      ctx.beginPath(); ctx.roundRect(-P.w / 2 - 3, -P.h / 2 - 3, P.w + 6, P.h + 6, 14); ctx.stroke();
    }
    // magnet indicator
    if (P.magnet > 0) {
      ctx.strokeStyle = '#ffd23f'; ctx.lineWidth = 3;
      ctx.beginPath(); ctx.arc(0, 0, P.w / 2 + 10 + Math.sin(t * 10) * 4, Math.PI, 0); ctx.stroke();
    }
    ctx.restore();
    // shield line
    if (this.shield) {
      ctx.strokeStyle = 'rgba(255,255,255,0.7)';
      ctx.lineWidth = 4; ctx.setLineDash([14, 10]);
      ctx.beginPath();
      ctx.moveTo(40, FIELD_H - 130); ctx.lineTo(FIELD_W - 40, FIELD_H - 130);
      ctx.stroke(); ctx.setLineDash([]);
    }
  }

  drawHUD(ctx, t) {
    ctx.textBaseline = 'top';
    // score
    ctx.textAlign = 'left';
    ctx.font = '700 40px system-ui, sans-serif';
    ctx.fillStyle = '#eaf4ff';
    ctx.fillText(String(this.score), 30, 24);
    ctx.font = '400 24px system-ui, sans-serif';
    ctx.fillStyle = '#8fa3c8';
    ctx.fillText('HI ' + this.hiScore, 30, 74);
    // lives
    ctx.textAlign = 'right';
    ctx.fillStyle = '#eaf4ff';
    for (let i = 0; i < this.lives; i++) {
      ctx.beginPath(); ctx.arc(FIELD_W - 40 - i * 44, 44, 14, 0, 6.283);
      ctx.fillStyle = '#7df9ff'; ctx.fill();
    }
    // multiplier
    if (this.mult > 1) {
      ctx.textAlign = 'center';
      ctx.font = '700 ' + (34 + Math.min(26, this.mult * 2)) + 'px system-ui, sans-serif';
      ctx.fillStyle = `hsl(${20 + Math.min(120, this.mult * 10)},100%,60%)`;
      ctx.globalAlpha = 0.75 + 0.25 * Math.sin(t * 9);
      ctx.fillText('x' + this.mult.toFixed(1), FIELD_W / 2, 30);
      ctx.globalAlpha = 1;
    }
    // overdrive meter / banner (only during play)
    if ((this.overdrive || this.odTime > 0) && (this.state === 'play' || this.state === 'pause')) {
      const p = Math.min(1, this.odTime / 9);
      const bw = FIELD_W * 0.5, bx = (FIELD_W - bw) / 2, by = FIELD_H - 110;
      ctx.fillStyle = 'rgba(20,26,48,0.7)';
      ctx.beginPath(); ctx.roundRect(bx, by, bw, 22, 11); ctx.fill();
      const g = ctx.createLinearGradient(bx, 0, bx + bw, 0);
      g.addColorStop(0, '#ff7a1a'); g.addColorStop(0.5, '#ffd23f'); g.addColorStop(1, '#ff4444');
      ctx.fillStyle = g;
      ctx.beginPath(); ctx.roundRect(bx, by, bw * p, 22, 11); ctx.fill();
      ctx.textAlign = 'center';
      ctx.font = '700 30px system-ui, sans-serif';
      const tierName = this.odTier > 0 ? this.TIERS[this.odTier - 1] : 'OVERDRIVE';
      ctx.fillStyle = `hsl(${20 + this.odTier * 35},100%,${60 + 10 * Math.sin(t * 12)}%)`;
      ctx.fillText(tierName, FIELD_W / 2, by - 40);
    }
    // active timers
    ctx.textAlign = 'left';
    let ty = 110;
    const timers = [];
    if (this.paddle.wide > 0) timers.push(['WIDE ' + Math.ceil(this.paddle.wide), PU.WIDE.color]);
    if (this.slowmo > 0) timers.push(['SLOW-MO ' + Math.ceil(this.slowmo), PU.SLOWMO.color]);
    if (this.paddle.magnet > 0) timers.push(['MAGNET ' + Math.ceil(this.paddle.magnet), PU.MAGNET.color]);
    for (const b of this.balls) if (b.type !== 'std' && b.timer > 0) timers.push([b.type.toUpperCase() + ' ' + Math.ceil(b.timer), b.color()]);
    if (timers.length) {
      ctx.fillStyle = 'rgba(10,14,30,0.65)';
      ctx.beginPath(); ctx.roundRect(18, 100, 260, 14 + timers.length * 28, 10); ctx.fill();
      ctx.font = '600 22px system-ui, sans-serif';
      for (const [txt, col] of timers) {
        ctx.fillStyle = col;
        ctx.fillText(txt, 30, ty);
        ctx.fillStyle = '#eaf4ff';
        ctx.fillText(txt.split(' ')[1], 30 + ctx.measureText(txt.split(' ')[0] + ' ').width, ty);
        ty += 28;
      }
    }
  }

  drawStateOverlay(ctx, t) {
    const st = this.state, T = this.stateT;
    ctx.textAlign = 'center';
    if (st === 'title') {
      // animated demo bricks behind title
      for (let i = 0; i < 8; i++) {
        const x = FIELD_W * 0.15 + i * FIELD_W * 0.1;
        const y = FIELD_H * 0.3 + Math.sin(t * 2 + i) * 20;
        ctx.fillStyle = `hsl(${i * 40},80%,55%)`;
        ctx.beginPath(); ctx.roundRect(x, y, 70, 30, 6); ctx.fill();
      }
      ctx.font = '900 110px system-ui, sans-serif';
      const g = ctx.createLinearGradient(0, 0, FIELD_W, 0);
      g.addColorStop(0, '#7df9ff'); g.addColorStop(0.5, '#ffd23f'); g.addColorStop(1, '#ff4444');
      ctx.fillStyle = g;
      ctx.fillText('OVERDRIVE', FIELD_W / 2, FIELD_H * 0.42);
      ctx.font = '700 46px system-ui, sans-serif';
      ctx.fillStyle = '#eaf4ff';
      ctx.fillText('BREAKOUT', FIELD_W / 2, FIELD_H * 0.42 + 90);
      ctx.font = '500 30px system-ui, sans-serif';
      ctx.fillStyle = 0.5 + 0.5 * Math.sin(t * 4) > 0.5 ? '#ffffff' : '#9fb8e0';
      ctx.fillText('TAP / CLICK / SPACE TO START', FIELD_W / 2, FIELD_H * 0.75);
    } else if (st === 'howto') {
      ctx.fillStyle = 'rgba(5,6,14,0.88)';
      ctx.fillRect(0, 0, FIELD_W, FIELD_H);
      ctx.font = '800 64px system-ui, sans-serif';
      ctx.fillStyle = '#7df9ff';
      ctx.fillText('HOW TO PLAY', FIELD_W / 2, 260);
      const lines = [
        'Goal: break through the wall — get the ball UP TOP.',
        'While the ball bounces above the bricks you earn',
        'OVERDRIVE: rising multiplier, escalating chaos.',
        '',
        'Bricks: ▼ angle-lock (hit steep)  ≡ speed (hit fast)',
        '⊙ phase (time it)  ▨ reflect (needs power ball)',
        '★ paydirt (drops power-ups)  ▩ steel (indestructible)',
        '',
        'Paddle moves LEFT/RIGHT and UP/DOWN — hit the ball',
        'while moving UP for a SMASH (extra speed).',
        '',
        'Mouse: move + click.  Keys: WASD/arrows + Space.',
        'Touch: drag anywhere (paddle rides above thumb),',
        'flick up = smash, tap = launch. 🕹 = thumbstick mode.'
      ];
      ctx.font = '500 34px system-ui, sans-serif';
      ctx.fillStyle = '#cfe6ff';
      lines.forEach((l, i) => ctx.fillText(l, FIELD_W / 2, 380 + i * 58));
      ctx.fillStyle = 0.5 + 0.5 * Math.sin(t * 4) > 0.5 ? '#fff' : '#6f87b0';
      ctx.font = '700 34px system-ui, sans-serif';
      ctx.fillText('TAP / CLICK / SPACE TO PLAY', FIELD_W / 2, FIELD_H - 180);
    } else if (st === 'ready') {
      const L = LEVELS[this.level];
      ctx.fillStyle = 'rgba(5,6,14,0.55)';
      ctx.fillRect(0, 0, FIELD_W, FIELD_H);
      ctx.font = '900 84px system-ui, sans-serif';
      ctx.fillStyle = '#ffd23f';
      ctx.fillText('LEVEL ' + (this.level + 1), FIELD_W / 2, FIELD_H * 0.34);
      ctx.font = '700 44px system-ui, sans-serif';
      ctx.fillStyle = '#eaf4ff';
      ctx.fillText(L.name, FIELD_W / 2, FIELD_H * 0.34 + 90);
      ctx.font = '500 30px system-ui, sans-serif';
      ctx.fillStyle = '#9fb8e0';
      ctx.fillText(L.intro, FIELD_W / 2, FIELD_H * 0.34 + 160);
      if (T > 0.8) {
        ctx.fillStyle = 0.5 + 0.5 * Math.sin(t * 6) > 0.5 ? '#fff' : '#6f87b0';
        ctx.font = '700 36px system-ui, sans-serif';
        ctx.fillText('LAUNCH: TAP / CLICK / SPACE', FIELD_W / 2, FIELD_H * 0.34 + 300);
      }
    } else if (st === 'pause') {
      ctx.fillStyle = 'rgba(5,6,14,0.7)';
      ctx.fillRect(0, 0, FIELD_W, FIELD_H);
      ctx.font = '900 90px system-ui, sans-serif';
      ctx.fillStyle = '#eaf4ff';
      ctx.fillText('PAUSED', FIELD_W / 2, FIELD_H / 2);
      ctx.font = '500 30px system-ui, sans-serif';
      ctx.fillStyle = '#9fb8e0';
      ctx.fillText('P to resume  ·  M mute', FIELD_W / 2, FIELD_H / 2 + 80);
    } else if (st === 'levelclear') {
      ctx.fillStyle = 'rgba(5,6,14,0.5)';
      ctx.fillRect(0, 0, FIELD_W, FIELD_H);
      ctx.font = '900 96px system-ui, sans-serif';
      ctx.fillStyle = '#ffd23f';
      ctx.fillText('LEVEL CLEAR!', FIELD_W / 2, FIELD_H * 0.4);
      ctx.font = '700 44px system-ui, sans-serif';
      ctx.fillStyle = '#eaf4ff';
      ctx.fillText('Score: ' + this.score, FIELD_W / 2, FIELD_H * 0.4 + 100);
      if (T > 1.6) {
        ctx.fillStyle = 0.5 + 0.5 * Math.sin(t * 5) > 0.5 ? '#7df9ff' : '#2a5a70';
        ctx.font = '700 36px system-ui, sans-serif';
        ctx.fillText('NEXT LEVEL…', FIELD_W / 2, FIELD_H * 0.4 + 220);
      }
    } else if (st === 'gameover') {
      ctx.fillStyle = 'rgba(5,6,14,0.8)';
      ctx.fillRect(0, 0, FIELD_W, FIELD_H);
      ctx.font = '900 100px system-ui, sans-serif';
      ctx.fillStyle = '#ff4444';
      ctx.fillText('GAME OVER', FIELD_W / 2, FIELD_H * 0.36);
      ctx.font = '700 40px system-ui, sans-serif';
      ctx.fillStyle = '#eaf4ff';
      ctx.fillText('Score: ' + this.score + (this.score >= this.hiScore ? '  ★ NEW BEST' : ''), FIELD_W / 2, FIELD_H * 0.36 + 100);
      ctx.fillText('Best: ' + this.hiScore, FIELD_W / 2, FIELD_H * 0.36 + 160);
      ctx.font = '500 28px system-ui, sans-serif';
      ctx.fillStyle = '#9fb8e0';
      ctx.fillText('Level reached: ' + (this.level + 1), FIELD_W / 2, FIELD_H * 0.36 + 220);
      ctx.fillStyle = 0.5 + 0.5 * Math.sin(t * 4) > 0.5 ? '#fff' : '#6f87b0';
      ctx.font = '700 34px system-ui, sans-serif';
      ctx.fillText('TAP / CLICK / SPACE TO RETRY', FIELD_W / 2, FIELD_H * 0.36 + 320);
      if (T > 0.6 && (this.input.usePointer || true)) {
        // advance on tap: handled in input by checking state; ensure startRun on click
      }
    }
    // allow tap-to-continue from levelclear / gameover
    if (st === 'levelclear' && T > 1.6) {
      ctx.fillStyle = 0.5 + 0.5 * Math.sin(t * 5) > 0.5 ? '#7df9ff' : '#2a5a70';
    }
  }

  // advance from interstitial states on tap (called by main on pointerdown)
  advanceOnTap() {
    if (this.state === 'levelclear' && this.stateT > 1.6) {
      this.startLevel(this.level + 1);
      return true;
    }
    if (this.state === 'gameover' && this.stateT > 1) { this.startRun(); return true; }
    return false;
  }
}
window.Game = Game;