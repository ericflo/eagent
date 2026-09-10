'use strict';
// ---------------------------------------------------------------------------
// game.js — core gameplay: state machine, physics (120Hz substeps),
// ball–brick / ball–paddle collisions, powerups, scoring, and OVERDRIVE —
// the core mechanic: keep a ball above the brick field and multiplier climbs.
// ---------------------------------------------------------------------------

const STEP = 1 / 120;

const Game = {
  state: 'title',
  score: 0, lives: 3, level: 0, loop: 0,
  combo: 0, multiplier: 1,
  hype: 0.2,
  balls: [], powerups: [], lasers: [],
  paddle: null,
  levelName: '',
  introT: 0,
  clearBonus: 0,
  hiScore: 0, bestMulti: 1,
  newBest: false,
  hintDone: false,
  overdrive: false,
  stats: { bricks: 0, maxMulti: 1, bestOverdrive: 0 },
  acc: 0,

  // ---------------------------------------------------------------- init --
  init() {
    try {
      this.hiScore = +(localStorage.getItem('breakpoint_hi') || 0);
      this.bestMulti = +(localStorage.getItem('breakpoint_bm') || 1);
    } catch (e) { /* private mode */ }
    this.paddle = new Paddle();
  },

  start() {
    this.score = 0; this.lives = 3; this.level = 0; this.loop = 0;
    this.newBest = false;
    this.stats = { bricks: 0, maxMulti: 1, bestOverdrive: 0 };
    this.loadLevel(0);
    this.state = 'playing';
  },

  loadLevel(n) {
    this.level = n;
    this.levelIdx = n % LEVELS.length;
    this.levelName = LEVELS[this.levelIdx].name;
    this.loop = Math.floor(n / LEVELS.length);
    BrickField.build(this.levelIdx, this.loop);
    this.paddle.shield = false;
    this.paddle.sticky = 0; this.paddle.laser = 0; this.paddle.wide = 0;
    this.spawnBall();
    this.powerups.length = 0; this.lasers.length = 0;
    this.combo = 0; this.multiplier = 1;
    this.overdrive = false;
    this.introT = 1.2;
    this.recomputeZone();
  },

  spawnBall() {
    const b = new Ball(this.paddle.x, this.paddle.y - 20);
    b.stuck = true;
    b.stickOff = 0;
    this.balls = [b];
  },

  // -------------------------------------------------------------- update --
  update(dt) {
    // hype target: base + multiplier + overdrive bonus
    let target = 0.2 + (this.multiplier / 16) * 0.5;
    if (this.overdrive) target = Math.max(target, 0.85);
    this.hype = lerp(this.hype, target, dt * 3);
    AudioSys.setHype(this.hype);
    if (this.introT > 0) this.introT -= dt;

    if (this.state !== 'playing') {
      // tap handling on overlays
      if (Input.consumeAction()) {
        if (this.state === 'title') { this.start(); AudioSys.uiClick(); }
        else if (this.state === 'gameover') { this.start(); AudioSys.uiClick(); }
        else if (this.state === 'levelclear') { this.loadLevel(this.level + 1); this.state = 'playing'; AudioSys.uiClick(); }
        else if (this.state === 'paused') { this.state = 'playing'; AudioSys.uiClick(); }
      }
      return;
    }

    const paddle = this.paddle;
    paddle.update(dt, Input);

    // action: launch / laser / release sticky
    if (Input.consumeAction()) {
      this.hintDone = true;
      let used = false;
      for (const b of this.balls) {
        if (b.stuck) { b.launch(); AudioSys.paddleHit(this.hype); used = true; }
      }
      if (!used && paddle.laser > 0 && paddle.laserCd <= 0) {
        this.fireLasers();
        used = true;
      }
      if (!used && paddle.sticky > 0) {
        // release stuck balls
        for (const b of this.balls) if (b.stuck) { b.launch(); used = true; }
      }
    }

    // fixed-step physics
    this.acc += dt;
    this.acc = Math.min(this.acc, 0.05);
    while (this.acc >= STEP) {
      this.step(STEP);
      this.acc -= STEP;
    }

    BrickField.update(dt);
    this.updateOverdrive(dt);

    for (let i = this.powerups.length - 1; i >= 0; i--) {
      const p = this.powerups[i];
      p.update(dt);
      if (p.y > H + 30) { this.powerups.splice(i, 1); continue; }
      if (Math.abs(p.x - paddle.x) < paddle.halfW + 12 && Math.abs(p.y - paddle.y) < 14) {
        this.catchPowerup(p);
        this.powerups.splice(i, 1);
      }
    }

    for (let i = this.lasers.length - 1; i >= 0; i--) {
      const l = this.lasers[i];
      l.update(dt);
      if (l.y < -20) { this.lasers.splice(i, 1); continue; }
      this.laserBrick(l, i);
    }

    // level clear?
    if (this.state === 'playing' && BrickField.clearRemaining() === 0) this.levelClear();
  },

  levelClear() {
    this.state = 'levelclear';
    AudioSys.levelWin();
    FX.hitStop(0.3);
    FX.flash(0.4, '120,255,176');
    // bonus: remaining lives + current multiplier
    this.clearBonus = this.lives * 250 + this.multiplier * 100 + Math.floor(this.loop * 500);
    this.score += this.clearBonus;
    for (const b of this.balls) {
      Particles.burst(b.x, b.y, 14, '#78ffb4', { speed: 260, life: 0.6 });
    }
  },

  // one physics substep
  step(dt) {
    const paddle = this.paddle;
    for (let i = this.balls.length - 1; i >= 0; i--) {
      const b = this.balls[i];
      b.update(dt, paddle);
      if (b.stuck) continue;
      this.ballWalls(b, i);
      if (b !== this.balls[i]) continue; // ball removed
      this.ballBricks(b);
      if (b !== this.balls[i]) continue;
      this.ballPaddle(b);
      this.ballLoss(b, i);
    }
  },

  // ------------------------------------------------------------ collision --
  ballWalls(b, idx) {
    if (b.x - b.r < 0) { b.x = b.r; b.vx = Math.abs(b.vx); AudioSys.wallHit(); Particles.spark(b.x, b.y, { color: '#8ef7ff', speed: 90, life: 0.2 }); }
    else if (b.x + b.r > W) { b.x = W - b.r; b.vx = -Math.abs(b.vx); AudioSys.wallHit(); Particles.spark(b.x, b.y, { color: '#8ef7ff', speed: 90, life: 0.2 }); }
    if (b.y - b.r < 0) {
      b.y = b.r; b.vy = Math.abs(b.vy);
      // ceiling hit while onTop: keep the rally going, tiny bonus feel
      if (this.overdrive) { Particles.spark(b.x, b.y, { color: '#ff9de2', speed: 120, life: 0.25 }); }
      AudioSys.wallHit();
    }
  },

  ballBricks(b) {
    for (const br of BrickField.bricks) {
      if (!br.alive) continue;
      // circle-vs-rect (nearest point)
      const nx = clamp(b.x, br.x, br.x + br.w);
      const ny = clamp(b.y, br.y, br.y + br.h);
      const dx = b.x - nx, dy = b.y - ny;
      if (dx * dx + dy * dy > b.r * b.r) continue;

      // ghost-phase regen: ball passes through harmlessly
      if (br.type === 'regen' && !br.regenSolid) continue;
      // hidden barrier handled like normal brick but translucent
      this.hitBrick(br, b);
      break; // one brick per substep
    }
  },

  hitBrick(br, b) {
    const pierce = b.fire > 0 && br.type !== 'steel' && br.type !== 'unlock' && br.type !== 'hidden';
    // resolve bounce (unless pierce or special)
    let bounce = true;
    if (br.type === 'death') {
      this.killBallAt(b, br);
      this.breakBrick(br, b);
      return;
    }
    if (br.type === 'steel') {
      // |ball direction angle from horizontal| > 50° → steep enough to crack it
      const angDeg = Math.abs(Math.atan2(b.vy, b.vx)) * 180 / Math.PI;
      const fromHoriz = Math.min(angDeg, 180 - angDeg); // 0..90, 90 = straight down
      if (fromHoriz > STEEL_ANGLE_DEG) {
        // steep hit: steel shatters below
      } else {
        AudioSys.steelTink();
        br.arcT = 0.5;
        Particles.spark(br.cx, br.cy, { color: '#dfe9ff', speed: 160, life: 0.3 });
        FX.addTrauma(0.05);
        this.breakBounce(br, b);
        return;
      }
    }
    if (br.type === 'velocity' && b.speed < VELOCITY_SPEED && b.fire <= 0) {
      if (br.hintCd <= 0) {
        FX.floatText(br.cx, br.cy, 'TOO SLOW', '#ff9a3c', 12, 0.7);
        br.hintCd = 1.2;
      }
      AudioSys.steelTink();
      this.breakBounce(br, b);
      return;
    }
    if (br.type === 'unlock' || br.type === 'hidden') {
      AudioSys.steelTink();
      this.breakBounce(br, b);
      return;
    }
    if (br.type === 'redirect') {
      // deflect 90° away from brick, then break
      const away = b.y < br.cy ? -1 : 1; // prefer upward
      const nvx = -b.vy * away, nvy = b.vx * away;
      const sp = b.speed;
      b.vx = nvx / (sp || 1) * sp; b.vy = nvy / (sp || 1) * sp;
      b.y += b.vy > 0 ? 2 : -2;
      bounce = false;
    }
    if (br.type === 'prism') {
      // split into 3
      AudioSys.prismZap();
      const sp = Math.max(b.speed, 300);
      for (const da of [-25, 25]) {
        const a = Math.atan2(b.vy, b.vx) + da * Math.PI / 180;
        const nb = new Ball(b.x, b.y, Math.cos(a) * sp, Math.sin(a) * sp);
        nb.fire = b.fire;
        this.balls.push(nb);
      }
      bounce = false;
    }
    if (br.type === 'switch') {
      AudioSys.toggleSwitch();
      this.toggleHidden(br);
      bounce = false;
    }
    if (br.type === 'key') {
      this.popLocks(br);
      bounce = false;
    }
    if (br.type === 'reset') {
      // anti-juicer hazard: combo/multiplier wiped
      this.combo = 0;
      this.multiplier = 1;
      this.decayTimer = 0;
      FX.floatText(br.cx, br.cy, '×1', '#8a93a5', 18, 1.0);
      FX.addTrauma(0.1);
      bounce = false;
    }

    // fire pierce: damage but don't bounce
    if (pierce && br.damageable(b)) {
      this.breakBrick(br, b);
      return;
    }

    if (bounce) {
      // minimal translation + reflect on overlap axis
      const prevCx = b.x - b.vx * STEP, prevCy = b.y - b.vy * STEP;
      const fromSide = prevCx < br.x || prevCx > br.x + br.w;
      if (fromSide) { b.vx = -b.vx; b.x = b.vx > 0 ? br.x - b.r : br.x + br.w + b.r; }
      else { b.vy = -b.vy; b.y = b.vy > 0 ? br.y - b.r : br.y + br.h + b.r; }
    }

    if (br.damageable(b)) this.breakBrick(br, b);
  },

  // reflect off an indestructible brick without breaking it
  breakBounce(br, b) {
    const prevCx = b.x - b.vx * STEP, prevCy = b.y - b.vy * STEP;
    const fromSide = prevCx < br.x || prevCx > br.x + br.w;
    if (fromSide) { b.vx = -b.vx; b.x = b.vx > 0 ? br.x - b.r : br.x + br.w + b.r; }
    else { b.vy = -b.vy; b.y = b.vy > 0 ? br.y - b.r : br.y + br.h + b.r; }
  },

  toggleHidden(sw) {
    for (const h of BrickField.bricks) {
      if (h.type === 'hidden' && h.alive) {
        h.alive = false;
        Particles.brickBurst(h.cx, h.cy, h.w, h.h, '#7dffb0', this.hype);
      }
    }
    FX.addTrauma(0.15);
  },

  popLocks(keyBrick) {
    let i = 0;
    for (const u of BrickField.bricks) {
      if (u.type === 'unlock' && u.alive) {
        const delay = i * 0.07;
        const ux = u.cx, uy = u.cy;
        setTimeout(() => {
          u.alive = false;
          Particles.brickBurst(ux, uy, u.w, u.h, '#ffcf4d', this.hype);
        }, delay * 1000);
        i++;
      }
    }
    FX.addTrauma(0.3);
    FX.flash(0.25, '255,207,77');
  },

  breakBrick(br, b) {
    br.alive = false;
    const hype = this.hype;
    Particles.brickBurst(br.cx, br.cy, br.w, br.h, br.color, hype);
    AudioSys.brickBreak((br.row % 6) / 6, hype, this.multiplier);
    this.combo++;
    this.stats.bricks++;
    const pts = br.points * this.multiplier;
    if (pts > 0) {
      this.score += pts;
      FX.floatText(br.cx, br.cy, '+' + pts, this.multiColor(), 13 + Math.min(8, this.multiplier), 0.8);
    }
    // new multiplier?
    const nm = Math.min(16, 1 + Math.floor(this.combo / 3));
    if (nm > this.multiplier) {
      this.multiplier = nm;
      this.stats.maxMulti = Math.max(this.stats.maxMulti, nm);
      AudioSys.multiplierUp(nm);
      FX.addChroma(0.3);
    }
    // hit-stop: steel/velocity always, else every 5th brick
    if ((br.type === 'steel' || br.type === 'velocity') || this.stats.bricks % 5 === 0) {
      FX.hitStop(0.02 + hype * 0.02);
    }
    FX.addTrauma(0.04 + hype * 0.06);

    // powerup drop: authored drops always, else 22% from basics
    if (br.drop) {
      this.dropPowerup(br, br.drop);
    } else if (br.type === 'basic' && Math.random() < 0.22) {
      this.dropPowerup(br, this.rollPowerup());
    }
    this.recomputeZone();
  },

  multiColor() {
    const m = this.multiplier;
    if (m >= 12) return '#ff4dd8';
    if (m >= 8) return '#ffb347';
    if (m >= 4) return '#ffe066';
    return '#ffffff';
  },

  rollPowerup() {
    let total = 0;
    for (const [, w] of POWERUP_TABLE) total += w;
    let r = Math.random() * total;
    for (const [t, w] of POWERUP_TABLE) { r -= w; if (r <= 0) return t; }
    return 'wide';
  },

  dropPowerup(br, type) {
    this.powerups.push(new PowerUp(type, br.cx, br.cy));
    AudioSys.powerupSpawn();
  },

  killBallAt(b, br) {
    // dramatic ball destruction on death brick
    AudioSys.deathBrick();
    FX.flash(0.4, '255,40,60');
    FX.addTrauma(0.6);
    FX.hitStop(0.15);
    Particles.burst(b.x, b.y, 26, '#ff3355', { speed: 320, life: 0.7 });
    Particles.ring(b.x, b.y, { color: '#ff3355', grow: 500, life: 0.4 });
    const idx = this.balls.indexOf(b);
    if (idx >= 0) this.balls.splice(idx, 1);
  },

  laserBrick(l, idx) {
    for (const br of BrickField.bricks) {
      if (!br.alive) continue;
      if (l.x >= br.x && l.x <= br.x + br.w && l.y - 7 <= br.y + br.h && l.y + 7 >= br.y) {
        if (br.type === 'unlock' || br.type === 'steel') { this.lasers.splice(idx, 1); break; }
        if (br.type === 'death') { this.breakBrick(br, null); } // counterplay: lasers defuse death bricks
        else if (br.damageable(null)) this.breakBrick(br, null);
        this.lasers.splice(idx, 1);
        break;
      }
    }
  },

  // -------------------------------------------------------------- paddle --
  ballPaddle(b) {
    const p = this.paddle;
    if (b.vy <= 0) return;
    if (b.y + b.r < p.y - p.h / 2 || b.y - b.r > p.y + p.h / 2 + 4) return;
    if (b.x < p.x - p.halfW - b.r || b.x > p.x + p.halfW + b.r) return;
    b.y = p.y - p.h / 2 - b.r;
    const lift = p.bounce(b);
    this.combo = 0; // paddle hit resets combo
    AudioSys.paddleHit(this.hype);
    Particles.ring(b.x, p.y - p.h / 2, { color: lift ? '#ffe066' : '#4ef0ff', grow: lift ? 420 : 260, life: 0.3 });
    if (lift) {
      FX.addTrauma(0.12);
      Particles.burst(b.x, p.y - 10, 8, '#ffe066', { speed: 200, life: 0.35 });
      if (!this.hintDone && this.level === 1) FX.floatText(p.x, p.y - 40, 'LIFT!', '#ffe066', 20, 1.0);
      else if (this.level === 1 && !this.liftHintShown) {
        FX.floatText(p.x, p.y - 40, 'LIFT!', '#ffe066', 20, 1.0);
        this.liftHintShown = true;
      }
    }
    // sticky catch
    if (p.sticky > 0) {
      b.stuck = true;
      b.stickOff = clamp(b.x - p.x, -p.halfW + 8, p.halfW - 8);
    }
  },

  ballLoss(b, idx) {
    if (b.y - b.r <= H + 20) return;
    if (this.paddle.shield) {
      // shield bounces the ball back once
      this.paddle.shield = false;
      b.y = H - 24;
      b.vy = -Math.abs(b.vy);
      AudioSys.shieldBlock();
      FX.flash(0.3, '120,255,176');
      Particles.ring(b.x, H - 14, { color: '#78ffb4', grow: 600, life: 0.4 });
      return;
    }
    this.balls.splice(idx, 1);
    if (this.balls.length === 0) this.lifeLost();
  },

  lifeLost() {
    this.lives--;
    AudioSys.lifeLost();
    FX.flash(0.35, '255,60,80');
    FX.addTrauma(0.5);
    FX.hitStop(0.25);
    this.overdrive = false;
    this.combo = 0;
    if (this.lives <= 0) {
      this.gameOver();
    } else {
      this.spawnBall();
      this.paddle.sticky = 0; this.paddle.laser = 0; this.paddle.wide = 0;
    }
  },

  gameOver() {
    this.state = 'gameover';
    AudioSys.gameOver();
    this.newBest = this.score > this.hiScore;
    if (this.newBest) {
      this.hiScore = this.score;
      try { localStorage.setItem('breakpoint_hi', this.score); } catch (e) {}
    }
    if (this.stats.maxMulti > this.bestMulti) {
      this.bestMulti = this.stats.maxMulti;
      try { localStorage.setItem('breakpoint_bm', this.bestMulti); } catch (e) {}
    }
  },

  // ------------------------------------------------------------ powerups --
  catchPowerup(p) {
    AudioSys.powerupCatch();
    const paddle = this.paddle;
    FX.floatText(paddle.x, paddle.y - 34, POWERUPS[p.type].label, POWERUPS[p.type].color, 16, 0.9);
    Particles.burst(p.x, p.y, 10, POWERUPS[p.type].color, { speed: 180, life: 0.4 });
    switch (p.type) {
      case 'wide': paddle.wide = 14; break;
      case 'multi': {
        for (let i = 0; i < 2; i++) {
          const src = this.balls[0] || null;
          const a = -Math.PI / 2 + (i === 0 ? -0.5 : 0.5);
          const sp = src ? src.speed : BALL_BASE_SPEED;
          const nb = new Ball(paddle.x, paddle.y - 30, Math.cos(a) * sp, Math.sin(a) * sp);
          this.balls.push(nb);
        }
        break;
      }
      case 'laser': paddle.laser = 10; break;
      case 'slow': {
        for (const b of this.balls) if (b.speed > 0) b.setSpeed(b.speed * 0.65);
        FX.startSlowmo(0.9);
        AudioSys.slowmoWhoosh();
        break;
      }
      case 'fire': for (const b of this.balls) b.fire = 8; break;
      case 'catch': paddle.sticky = 12; break;
      case 'life': this.lives = Math.min(5, this.lives + 1); break;
      case 'shield': paddle.shield = true; break;
    }
  },

  fireLasers() {
    const p = this.paddle;
    this.lasers.push(new Laser(p.x - p.halfW + 4, p.y - 10));
    this.lasers.push(new Laser(p.x + p.halfW - 4, p.y - 10));
    p.laserCd = 0.25;
    AudioSys.laserShot(this.hype);
  },

  // ------------------------------------------------------------ overdrive --
  recomputeZone() {
    // zone top = top of lowest remaining brick row (min y of live bricks)
    this.zoneY = BrickField.topY();
  },

  updateOverdrive(dt) {
    const zoneY = this.zoneY !== undefined ? this.zoneY : BrickField.topY();
    let anyOnTop = false;
    for (const b of this.balls) {
      if (b.stuck) { b.aboveTime = 0; b.onTop = false; continue; }
      if (b.y < zoneY - BRICK_H) {
        b.aboveTime += dt;
        if (b.aboveTime > 0.6) b.onTop = true;
      } else {
        b.aboveTime = 0;
        b.onTop = false;
      }
      if (b.onTop) anyOnTop = true;
    }

    if (anyOnTop && !this.overdrive) {
      // BREAKOUT moment
      this.overdrive = true;
      this.overdriveT = 0;
      this.multiTimer = 0;
      AudioSys.breakout();
      AudioSys.overdriveStart();
      FX.flash(0.5, '255,120,255');
      FX.addTrauma(0.5);
      FX.addChroma(0.8);
      FX.startSlowmo(0.35);
      FX.floatText(W / 2, zoneY - 60, 'ON TOP!', '#ff9de2', 34, 1.4, { scale: 1.2 });
      Particles.ring(W / 2, zoneY - 30, { color: '#ff9de2', grow: 900, life: 0.7 });
      this.overdriveTime = 0;
    }

    if (this.overdrive) {
      this.overdriveT += dt;
      this.overdriveTime = (this.overdriveTime || 0) + dt;
      this.stats.bestOverdrive = Math.max(this.stats.bestOverdrive, this.overdriveTime);
      this.multiTimer += dt;
      if (this.multiTimer >= 2) {
        this.multiTimer -= 2;
        if (this.multiplier < 16) {
          this.multiplier++;
          this.stats.maxMulti = Math.max(this.stats.maxMulti, this.multiplier);
          AudioSys.multiplierUp(this.multiplier);
          FX.floatText(W / 2, 120, '×' + this.multiplier, this.multiColor(), 24, 0.9, { scale: 1.1 });
          FX.addChroma(0.25);
        }
      }
      // speed floor keeps the rally alive
      for (const b of this.balls) {
        if (!b.stuck && b.speed < 300) b.setSpeed(300);
      }
      if (!anyOnTop) {
        this.overdriveLeave = (this.overdriveLeave || 0) + dt;
        if (this.overdriveLeave > 1) {
          this.overdrive = false;
          this.overdriveLeave = 0;
        }
      } else this.overdriveLeave = 0;
    } else if (this.multiplier > 1 && !this.overdrive) {
      // decay multiplier 1 per 1.5s after leaving overdrive
      if (this.decayTimer === undefined) this.decayTimer = 0;
      // only decay when no ball is on top and combo hasn't grown recently
      this.decayTimer += dt;
      if (this.decayTimer > 1.5) {
        this.decayTimer = 0;
        if (this.combo < this.multiplier * 3 - 2) this.multiplier = Math.max(1, this.multiplier - 1);
      }
    }
  },

  // ---------------------------------------------------------------- draw --
  draw(ctx) {
    // overdrive zone tint
    if (this.overdrive || this.hype > 0.5) {
      const zoneY = this.zoneY !== undefined ? this.zoneY : BrickField.topY();
      const a = this.overdrive ? 0.16 + 0.06 * Math.sin(performance.now() / 200) : 0.05;
      const g = ctx.createLinearGradient(0, 0, 0, zoneY);
      g.addColorStop(0, `rgba(255,120,255,${a})`);
      g.addColorStop(1, 'rgba(255,120,255,0)');
      ctx.fillStyle = g;
      ctx.fillRect(0, 0, W, Math.max(0, zoneY));
      // dashed zone line
      if (this.overdrive) {
        ctx.strokeStyle = 'rgba(255,157,226,0.5)';
        ctx.lineWidth = 1;
        ctx.setLineDash([6, 8]);
        ctx.lineDashOffset = -performance.now() / 40;
        ctx.beginPath();
        ctx.moveTo(0, zoneY - BRICK_H);
        ctx.lineTo(W, zoneY - BRICK_H);
        ctx.stroke();
        ctx.setLineDash([]);
      }
    }

    BrickField.draw(ctx);
    for (const p of this.powerups) p.draw(ctx);
    for (const l of this.lasers) l.draw(ctx);
    this.paddle.draw(ctx, performance.now() / 1000);
    for (const b of this.balls) b.draw(ctx, performance.now() / 1000, this.overdrive);
  },
};

Game.recomputeZone();
