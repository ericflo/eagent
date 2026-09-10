// SKYBREAK — core game state, physics, collision, hype system.
// Rendering lives here too (world + background); HUD/FX are separate modules.

const BRICK_FIELD_W = CONFIG.W;
const BRICK_OFFSET_X = (CONFIG.W - (12 * (BRICK.W + BRICK.GAP) - BRICK.GAP)) / 2;

class Game {
  constructor(canvas) {
    this.canvas = canvas;
    this.ctx = canvas.getContext('2d');
    this.fx = new FX();
    this.hud = new HUD();
    this.input = new Input(canvas);

    this.state = 'menu';         // menu | playing | paused | clear | over | victory
    this.levelIndex = 0;
    this.score = 0;
    this.lives = CONFIG.START_LIVES;
    this.mult = 1;
    this.ontop = false;
    this.ontopTime = 0;
    this.hypeTier = 0;

    this.bricks = [];
    this.balls = [];
    this.powerups = [];
    this.lasers = [];
    this.shield = false;
    this.powerupTimers = new Map();  // kind -> seconds remaining
    this.ballType = 'normal';
    this.ballTypeT = 0;              // seconds left on current ball type

    this.waitingLaunch = true;
    this.ripples = [];
    this.time = 0;
    this.chainCount = 0;
    this.chainTimer = 0;
    this.laserCd = 0;
    this.brickCount = 0;
    this.progress = Store.load();
    this.levelStartTime = 0;

    this._resize = this._resize.bind(this);
    window.addEventListener('resize', this._resize);
    this._resize();
  }

  _resize() {
    const dpr = Math.min(window.devicePixelRatio || 1, 2);
    const vw = window.innerWidth, vh = window.innerHeight;
    const scale = Math.min(vw / CONFIG.W, vh / CONFIG.H);
    const cssW = Math.floor(CONFIG.W * scale), cssH = Math.floor(CONFIG.H * scale);
    this.canvas.style.width = cssW + 'px';
    this.canvas.style.height = cssH + 'px';
    this.canvas.width = Math.floor(cssW * dpr);
    this.canvas.height = Math.floor(cssH * dpr);
    this.ctx.setTransform(dpr * scale, 0, 0, dpr * scale, 0, 0);
    this.input.scale = scale;
  }

  // ------------------------------------------------------------------ setup

  startLevel(i) {
    this.levelIndex = clamp(i, 0, LEVELS.length - 1);
    const def = LEVELS[this.levelIndex];
    this.bricks = [];
    for (let r = 0; r < def.rows.length; r++) {
      const line = def.rows[r];
      for (let c = 0; c < 12 && c < line.length; c++) {
        const ch = line[c];
        if (ch === '.') continue;
        const b = new Brick(ch, c, r);
        b.setHome(BRICK_OFFSET_X, BRICK.TOP);
        this.bricks.push(b);
      }
    }
    this.moverSpeed = def.moverSpeed || 0;
    this.brickCount = this.bricks.length;
    this.balls = [];
    this.powerups = [];
    this.lasers = [];
    this.powerupTimers.clear();
    this.ballType = 'normal';
    this.ballTypeT = 0;
    this.shield = false;
    this.mult = 1;
    this.ontop = false;
    this.ontopTime = 0;
    this.chainCount = 0;
    this.laserCd = 0;
    this.fx.reset();
    this.paddle = new Paddle();
    this.spawnBallOnPaddle();
    this.levelStartTime = performance.now();
    this.state = 'playing';
    this.waitingLaunch = true;
    audio.setTier(0);
  }

  spawnBallOnPaddle() {
    this.waitingLaunch = true;
    const b = new Ball(this.paddle.x, this.paddle.top - CONFIG.BALL_R - 2, 0, 0, this.ballType);
    b.stuck = true;
    this.balls = [b];
  }

  launchBall() {
    if (!this.waitingLaunch) return;
    this.waitingLaunch = false;
    const a = -Math.PI / 2 + rand(-CONFIG.LAUNCH_SPREAD, CONFIG.LAUNCH_SPREAD);
    const b = this.balls[0];
    if (b) {
      b.stuck = false;
      b.vx = Math.cos(a) * CONFIG.BASE_BALL_SPEED;
      b.vy = Math.sin(a) * CONFIG.BASE_BALL_SPEED;
    }
    audio.launch();
  }

  // ------------------------------------------------------------------ update

  update(dt) {
    this.time += dt;
    if (this.state !== 'playing') return;

    const scaled = dt * this.fx.timeScale;

    // --- paddle control ---
    const ctl = this.input.poll(scaled, this.paddle);
    let tx = this.input.pointer.x, ty = this.input.pointer.y;
    if (ctl.useStick) {
      tx = this.paddle.x + ctl.dx;
      ty = this.paddle.y + ctl.dy;
    }
    this.paddle.update(scaled, tx, ty);

    if (ctl.launch) {
      if (this.waitingLaunch) this.launchBall();
      else if (this.powerupTimers.has('laser')) this.fireLaser();
    }

    // --- ball type expiry ---
    if (this.ballTypeT > 0) {
      this.ballTypeT -= scaled;
      if (this.ballTypeT <= 0) {
        this.ballTypeT = 0;
        this.setBallType('normal');
      }
    }

    // --- powerup timers ---
    for (const [kind, t] of this.powerupTimers) {
      const nt = t - scaled;
      if (nt <= 0) this.powerupTimers.delete(kind);
      else this.powerupTimers.set(kind, nt);
    }
    this.paddle.sticky = this.powerupTimers.has('sticky');
    this.paddle.expandT = this.powerupTimers.has('expand') ? 1 : 0;

    // --- lasers ---
    if (this.powerupTimers.has('laser') && !this.waitingLaunch) {
      this.laserCd -= scaled;
      if (this.laserCd <= 0) this.fireLaser(true);
    }
    for (const l of this.lasers) l.update(scaled);
    this.lasers = this.lasers.filter((l) => l.alive && !this._laserHitBricks(l));

    // --- balls ---
    for (const b of this.balls) {
      if (b.stuck) {
        b.x = this.paddle.x;
        b.y = this.paddle.top - b.r - 2;
        b.trail.length = 0;
      } else {
        // Sub-step for fast balls so they can't tunnel through bricks.
        const steps = Math.max(1, Math.ceil((b.speed * scaled) / (BRICK.H * 0.8)));
        const sdt = scaled / steps;
        for (let s = 0; s < steps && b.alive !== false; s++) this._moveBall(b, sdt);
      }
    }

    // Lost balls.
    const before = this.balls.length;
    this.balls = this.balls.filter((b) => b.y - b.r < CONFIG.H + 30);
    if (this.balls.length < before) {
      this._onBallLost(before - this.balls.length);
    }
    if (this.balls.length === 0 && this.state === 'playing') this._loseLife();

    // --- powerup capsules ---
    for (const p of this.powerups) p.update(scaled);
    this.powerups = this.powerups.filter((p) => {
      if (p.alive &&
        Math.abs(p.x - this.paddle.x) < this.paddle.w / 2 + p.r &&
        p.y > this.paddle.top - p.r && p.y < this.paddle.bottom + p.r) {
        this._applyPowerup(p.kind, p.x, p.y);
        return false;
      }
      return p.alive;
    });

    // --- bricks ---
    for (const br of this.bricks) br.update(scaled, this.time, this.moverSpeed, BRICK_FIELD_W);
    this.bricks = this.bricks.filter((b) => b.alive);

    // --- hype / on-top ---
    this._updateHype(scaled);

    // --- chain decay ---
    if (this.chainTimer > 0) {
      this.chainTimer -= scaled;
      if (this.chainTimer <= 0) this.chainCount = 0;
    }

    // --- ripples ---
    for (const r of this.ripples) { r.r += 700 * scaled; if (r.r > 900) r.alive = false; }
    this.ripples = this.ripples.filter((r) => r.alive);

    // --- level clear? ---
    if (this.bricks.length === 0) this._levelClear();

    this.fx.update(scaled);
  }

  // -------------------------------------------------------------- hype logic

  _updateHype(dt) {
    // On-top = ball above the brick field, moving freely.
    const anyOnTop = this.balls.some((b) => !b.stuck && b.y - b.r < CONFIG.ONTOP_Y);
    if (anyOnTop) {
      if (!this.ontop) this._onTopStart();
      this.ontopTime += dt;
      this.mult = Math.min(this.mult * Math.pow(CONFIG.HYPE_RAMP, dt), CONFIG.HYPE_MAX_MULT);
    } else if (this.ontop) {
      this.ontop = false;
      if (this.mult >= CONFIG.HYPE_MIN_VIS_MULT) {
        this.fx.float(CONFIG.W / 2, CONFIG.ONTOP_Y + 90, 'OFF TOP', '#8fa0c8', 26);
      }
    }
    // Exponential decay of multiplier while not on top.
    if (!this.ontop && this.mult > 1) {
      this.mult = Math.max(1, this.mult * Math.pow(0.5, dt / CONFIG.HYPE_DECAY_HALF_LIFE));
      if (this.mult < 1.05) this.mult = 1;
    }

    // Tier for audio + visuals.
    const tier = clamp(Math.floor((this.mult - 1) / 2), 0, 4);
    if (tier !== this.hypeTier) {
      this.hypeTier = tier;
      audio.setTier(tier);
      if (tier > 0) {
        this.hud.pulse = 1;
        this.fx.chromatic(0.3 + tier * 0.15);
        for (const b of this.balls) this.ripple(b.x, b.y, '#ff4d6d');
      }
    }
    this.hud.pulse = Math.max(0, this.hud.pulse - dt * 2);
  }

  _onTopStart() {
    this.ontop = true;
    audio.onTop();
    this.fx.flashNow('#ffd23f', 0.4);
    this.fx.chromatic(0.8);
    this.fx.addShake(10);
    this.fx.float(CONFIG.W / 2, CONFIG.ONTOP_Y + 40, 'ON TOP!', '#ffd23f', 40);
    // Celebratory ring from every ball.
    for (const b of this.balls) this.ripple(b.x, b.y, '#ffd23f');
  }

  // -------------------------------------------------------------- ball motion

  _moveBall(b, dt) {
    b.update(dt);
    b.unstickAngles();

    // Walls.
    if (b.x - b.r < 0 && b.vx < 0) { b.x = b.r; b.vx = -b.vx; this._wallHit(b); }
    if (b.x + b.r > CONFIG.W && b.vx > 0) { b.x = CONFIG.W - b.r; b.vx = -b.vx; this._wallHit(b); }
    if (b.y - b.r < 0 && b.vy < 0) { b.y = b.r; b.vy = -b.vy; this._wallHit(b); }

    // Floor: shield save or nothing (loss handled by caller).
    if (b.y + b.r > CONFIG.H && this.shield && b.vy > 0) {
      b.y = CONFIG.H - b.r;
      b.vy = -Math.abs(b.vy);
      this.shield = false;
      audio.shieldBreak();
      this.fx.ringBurst(b.x, CONFIG.H - 6, '#7db8ff');
      this.fx.float(b.x, CONFIG.H - 60, 'SHIELD!', '#7db8ff', 28);
    }

    // Paddle.
    this._ballPaddle(b);

    // Bricks.
    this._ballBricks(b);

    // Slight acceleration over the rally, capped.
    if (!b.stuck) {
      const sp = b.speed;
      if (sp < CONFIG.MAX_BALL_SPEED) b.setSpeed(Math.min(sp * Math.pow(CONFIG.BALL_RAMP_PER_RALLY, dt * 3), CONFIG.MAX_BALL_SPEED));
    }
  }

  _wallHit(b) {
    audio.wallHit();
    this.fx.sparks(b.x, b.y, '#8fb8ff', 5, 180);
  }

  _ballPaddle(b) {
    const p = this.paddle;
    if (b.vy <= 0) return; // only when moving down
    if (b.y + b.r < p.top || b.y - b.r > p.bottom + 8) return;
    if (b.x < p.left - b.r || b.x > p.right + b.r) return;

    // Where on the paddle did we hit? (classic angle control)
    const rel = clamp((b.x - p.x) / (p.w / 2), -1, 1);
    const angle = rel * (Math.PI / 3); // max 60deg from vertical
    const sp = Math.max(b.speed, CONFIG.BASE_BALL_SPEED);

    // Smash (paddle moving up) / absorb (paddle moving down).
    let speed = sp;
    if (p.vy < CONFIG.SMASH_VY_THRESHOLD) {
      speed = Math.min(sp + CONFIG.PADDLE_SMASH_BOOST * Math.min(1, -p.vy / 600), CONFIG.MAX_BALL_SPEED);
      this.fx.float(b.x, p.top - 24, 'SMASH!', '#4dffa6', 24);
      this.fx.addShake(6);
    } else if (p.vy > 140) {
      speed = Math.max(sp * (1 - CONFIG.PADDLE_ABSORB * Math.min(1, p.vy / 700)), CONFIG.BASE_BALL_SPEED * 0.6);
    }
    speed = Math.max(speed + Math.abs(p.vx) * CONFIG.PADDLE_INFLUENCE * 0.5, CONFIG.BASE_BALL_SPEED * 0.75);
    speed = Math.min(speed, CONFIG.MAX_BALL_SPEED);

    b.vx = Math.sin(angle) * speed + p.vx * CONFIG.PADDLE_INFLUENCE;
    b.vy = -Math.cos(angle) * speed;
    const ns = Math.hypot(b.vx, b.vy);
    if (ns > CONFIG.MAX_BALL_SPEED) b.setSpeed(CONFIG.MAX_BALL_SPEED);

    b.y = p.top - b.r - 1;
    b.trail.length = 0;

    if (this.paddle.sticky) {
      b.stuck = true;
      this.waitingLaunch = false; // can re-launch by tap/space
    }

    audio.paddleHit(clamp(speed / CONFIG.MAX_BALL_SPEED, 0, 1));
    this.fx.sparks(b.x, p.top, '#35e0ff', 10, 300);
    this.fx.addShake(2 + speed / 900);
  }

  _ballBricks(b) {
    for (const br of this.bricks) {
      if (!br.alive) continue;
      // Circle-AABB overlap.
      const nx = clamp(b.x, br.x, br.x + br.w);
      const ny = clamp(b.y, br.y, br.y + br.h);
      const dx = b.x - nx, dy = b.y - ny;
      if (dx * dx + dy * dy > b.r * b.r) continue;

      const speed = b.speed;
      const canBreak = br.breakableBy(b, speed);
      if (canBreak) {
        this._breakBrick(br, b);
        if (BALL_TYPES[b.type].burnsThrough || b.type === 'ghost') {
          // burn / phase: no bounce, keep going
        } else {
          this._bounceOffBrick(b, br, nx, ny, dx, dy);
        }
      } else {
        br.flash = 1;
        audio.brickReject();
        this.fx.sparks(b.x, b.y, '#ff4d6d', 6, 200);
        this._bounceOffBrick(b, br, nx, ny, dx, dy);
      }
      break; // one brick per sub-step is enough
    }
  }

  _bounceOffBrick(b, br, nx, ny, dx, dy) {
    // Determine bounce normal from the shallowest penetration axis.
    if (dx !== 0 || dy !== 0) {
      const horiz = Math.abs(dx) / br.w > Math.abs(dy) / br.h;
      if (horiz) { b.vx = Math.abs(b.vx) * Math.sign(dx || 1); b.x = nx + (dx > 0 ? b.r : -b.r); }
      else { b.vy = Math.abs(b.vy) * Math.sign(dy || 1); b.y = ny + (dy > 0 ? b.r : -b.r); }
    } else {
      // Center inside: invert vy (most common case).
      b.vy = -b.vy;
    }
    b.unstickAngles();
  }

  // -------------------------------------------------------------- breaking

  _breakBrick(br, ball, silent = false) {
    br.alive = false;
    this.chainCount++;
    this.chainTimer = 0.9;
    const chainMult = 1 + Math.min(this.chainCount, 12) * 0.25;

    const base = 50 * (1 + this.levelIndex * 0.35);
    const pts = Math.round(base * chainMult * this.mult);
    this.score += pts;

    if (!silent) {
      audio.brickBreak(Math.min(this.chainCount, 24));
      this.fx.brickBreak(br.cx, br.cy, br.colorA, 1 + Math.min(this.chainCount, 6) * 0.2);
    }
    this.fx.float(br.cx, br.cy, `+${fmtScore(pts)}`, br.colorA, 16 + Math.min(this.chainCount * 2, 14));

    if (this.chainCount >= 3 && this.chainCount % 3 === 0) {
      audio.milestone();
      this.fx.float(CONFIG.W / 2, CONFIG.ONTOP_Y + 60, `CHAIN x${this.chainCount}`, '#4dffa6', 30);
      this.fx.chromatic(0.4);
    }

    // Bomb chain.
    if (br.kind === 'B') {
      audio.bomb();
      this.fx.explosion(br.cx, br.cy);
      const EXPLODE_R = 1;
      for (const other of this.bricks) {
        if (!other.alive || other === br) continue;
        if (Math.abs(other.col - br.col) <= EXPLODE_R && Math.abs(other.row - br.row) <= EXPLODE_R) {
          this._breakBrick(other, ball, true);
        }
      }
    }

    // Power-up drop.
    if (Math.random() < CONFIG.DROP_CHANCE) {
      this.powerups.push(new PowerUp(weightedPick(dropTable()), br.cx, br.cy));
    }
  }

  // -------------------------------------------------------------- powerups

  _applyPowerup(kind, x, y) {
    const def = POWERUPS[kind];
    audio.pickup();
    this.ripple(x, y, def.color);
    this.fx.float(x, y - 30, def.label, def.color, 26);
    this.fx.addShake(3);

    switch (kind) {
      case 'multi': {
        const src = this.balls.find((b) => !b.stuck) || this.balls[0];
        if (src) {
          for (const da of [-0.5, 0.5]) {
            const a = Math.atan2(src.vy || -1, src.vx || 0) + da;
            const sp = Math.max(src.speed, CONFIG.BASE_BALL_SPEED);
            this.balls.push(new Ball(src.x, src.y, Math.cos(a) * sp, Math.sin(a) * sp, 'normal'));
          }
        }
        break;
      }
      case 'shield':
        this.shield = true;
        break;
      case 'life':
        this.lives++;
        break;
      case 'expand':
        this.powerupTimers.set('expand', POWERUPS.expand.duration);
        break;
      case 'sticky':
        this.powerupTimers.set('sticky', POWERUPS.sticky.duration);
        break;
      case 'laser':
        this.powerupTimers.set('laser', POWERUPS.laser.duration);
        break;
      default: // ball types
        this.setBallType(kind, def.duration);
        break;
    }
  }

  setBallType(type, duration = 0) {
    this.ballType = type;
    this.ballTypeT = duration;
    for (const b of this.balls) {
      b.type = type;
      b.baseR = CONFIG.BALL_R * BALL_TYPES[type].radiusMul;
    }
  }

  fireLaser(auto = false) {
    if (this.laserCd > 0 && !auto) return;
    if (this.laserCd > 0) return;
    this.laserCd = CONFIG.PADDLE_LASER_CD;
    this.lasers.push(new Laser(this.paddle.left + 14, this.paddle.top));
    this.lasers.push(new Laser(this.paddle.right - 14, this.paddle.top));
    audio.laser();
  }

  _laserHitBricks(l) {
    for (const br of this.bricks) {
      if (!br.alive) continue;
      if (l.x > br.x && l.x < br.x + br.w && l.y < br.y + br.h && l.y > br.y - 8) {
        // Lasers break standard/mover/bomb; armored/angle/speed resist them.
        if (['S', 'M', 'B'].includes(br.kind)) this._breakBrick(br, null);
        else { br.flash = 1; audio.brickReject(); }
        return true;
      }
    }
    return false;
  }

  // -------------------------------------------------------------- life flow

  _onBallLost(n) {
    if (n > 0 && this.balls.length > 0) {
      audio.loseBall();
      this.fx.float(CONFIG.W / 2, CONFIG.H - 120, 'BALL LOST', '#ff4d6d', 22);
    }
  }

  _loseLife() {
    this.lives--;
    this.mult = 1;
    this.ontop = false;
    audio.loseBall();
    this.fx.flashNow('#ff4d6d', 0.35);
    this.fx.addShake(14);
    if (this.lives <= 0) {
      this._gameOver();
    } else {
      this.setBallType('normal');
      this.spawnBallOnPaddle();
      this.waitingLaunch = true;
    }
  }

  _levelClear() {
    if (this.state !== 'playing') return;
    this.state = 'clear';
    audio.levelClear();
    audio.setTier(0);
    // Celebrate: fireworks from brick field.
    for (let i = 0; i < 6; i++) {
      setTimeout(() => this.fx.explosion(rand(200, CONFIG.W - 200), rand(200, 600)), i * 160);
    }
    const prevBest = this.progress.best?.[this.levelIndex] ?? 0;
    const isBest = this.score > prevBest;
    this._markUnlocked();
    document.getElementById('clear-stats').innerHTML =
      `Score: <b>${fmtScore(this.score)}</b><br>` +
      (isBest
        ? `<span class="good">NEW BEST!</span> (previous: ${fmtScore(prevBest)})`
        : `Best this level: ${fmtScore(Math.max(prevBest, this.score))}`);
    showScreen('screen-clear');
  }

  _gameOver() {
    this.state = 'over';
    audio.gameOver();
    audio.setTier(0);
    document.getElementById('over-stats').innerHTML =
      `Final score: <b>${fmtScore(this.score)}</b><br>Reached level ${this.levelIndex + 1} — ${LEVELS[this.levelIndex].name}`;
    showScreen('screen-over');
  }

  _markUnlocked() {
    const p = this.progress;
    p.unlocked = p.unlocked || [];
    p.unlocked[this.levelIndex + 1] = true;
    p.best = p.best || {};
    if (!p.best[this.levelIndex] || this.score > p.best[this.levelIndex]) {
      p.best[this.levelIndex] = this.score;
    }
    Store.save(p);
  }

  // -------------------------------------------------------------- rendering

  render() {
    const ctx = this.ctx;
    ctx.save();
    // Screen shake.
    ctx.translate(this.fx.shakeX, this.fx.shakeY);

    this._drawBackground(ctx);
    if (this.state !== 'menu') {
      this._drawShield(ctx);
      for (const br of this.bricks) br.draw(ctx, this.time);
      for (const p of this.powerups) p.draw(ctx);
      for (const l of this.lasers) l.draw(ctx);
      this.paddle.draw(ctx, this.time, this.powerupTimers.get('laser') || 0);
      const fastThreshold = CONFIG.SPEED_BRICK_THRESHOLD;
      for (const b of this.balls) b.draw(ctx, fastThreshold);
      this._drawRipples(ctx);
      this.fx.draw(ctx);
      ctx.restore();
      this.hud.draw(ctx, this);
      this._drawPostFX(ctx);
    } else {
      this.fx.draw(ctx);
      ctx.restore();
      this._drawPostFX(ctx);
    }
  }

  _drawBackground(ctx) {
    // Base.
    ctx.fillStyle = '#070b16';
    ctx.fillRect(-40, -40, CONFIG.W + 80, CONFIG.H + 80);

    // Beat pulse glow.
    const beat = this.fx.beatFlash * (0.5 + this.hypeTier * 0.2);
    if (beat > 0.01) {
      ctx.save();
      ctx.globalCompositeOperation = 'lighter';
      const g = ctx.createRadialGradient(CONFIG.W / 2, CONFIG.H * 0.35, 0, CONFIG.W / 2, CONFIG.H * 0.35, CONFIG.W * 0.8);
      g.addColorStop(0, `rgba(53,80,255,${0.06 * beat})`);
      g.addColorStop(1, 'rgba(0,0,0,0)');
      ctx.fillStyle = g;
      ctx.fillRect(0, 0, CONFIG.W, CONFIG.H);
      ctx.restore();
    }

    // Grid stars (deterministic).
    ctx.save();
    for (let i = 0; i < 40; i++) {
      const x = (i * 173) % CONFIG.W;
      const y = (i * 419) % CONFIG.H;
      const tw = 0.3 + 0.3 * Math.sin(this.time * 1.5 + i);
      ctx.fillStyle = `rgba(140,170,255,${tw * 0.35})`;
      ctx.fillRect(x, y, 2, 2);
    }
    ctx.restore();

    // "Arcade" zone marker — the space above the bricks, glows while on top.
    if (this.ontop) {
      ctx.save();
      ctx.globalCompositeOperation = 'lighter';
      const a = 0.05 + 0.04 * Math.sin(this.time * 8);
      const g = ctx.createLinearGradient(0, 0, 0, CONFIG.ONTOP_Y + 80);
      g.addColorStop(0, `rgba(255,80,60,${a * 2})`);
      g.addColorStop(1, 'rgba(255,80,60,0)');
      ctx.fillStyle = g;
      ctx.fillRect(0, 0, CONFIG.W, CONFIG.ONTOP_Y + 80);
      ctx.restore();
    }
  }

  _drawRipples(ctx) {
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    for (const r of this.ripples) {
      const a = clamp(1 - r.r / 900, 0, 1) * 0.5;
      ctx.strokeStyle = r.color;
      ctx.globalAlpha = a;
      ctx.lineWidth = 3;
      ctx.beginPath();
      ctx.arc(r.x, r.y, r.r, 0, TAU);
      ctx.stroke();
    }
    ctx.restore();
  }

  _drawShield(ctx) {
    if (!this.shield) return;
    ctx.save();
    ctx.strokeStyle = 'rgba(125,184,255,0.8)';
    ctx.shadowColor = '#7db8ff'; ctx.shadowBlur = 16;
    ctx.lineWidth = 4;
    ctx.beginPath();
    ctx.moveTo(0, CONFIG.H - 6);
    ctx.lineTo(CONFIG.W, CONFIG.H - 6);
    ctx.stroke();
    ctx.restore();
  }

  _drawPostFX(ctx) {
    // Flash overlay.
    if (this.fx.flash > 0.01) {
      ctx.save();
      ctx.globalAlpha = clamp(this.fx.flash, 0, 0.8);
      ctx.fillStyle = this.fx.flashColor;
      ctx.fillRect(0, 0, CONFIG.W, CONFIG.H);
      ctx.restore();
    }
    // Chromatic-ish edge tint.
    if (this.fx.chroma > 0.02) {
      ctx.save();
      ctx.globalCompositeOperation = 'lighter';
      ctx.globalAlpha = this.fx.chroma * 0.25;
      ctx.fillStyle = '#ff2060';
      ctx.fillRect(-6, 0, CONFIG.W, CONFIG.H);
      ctx.fillStyle = '#20c0ff';
      ctx.fillRect(6, 0, CONFIG.W, CONFIG.H);
      ctx.restore();
    }
    // Vignette (subtle, always).
    ctx.save();
    const g = ctx.createRadialGradient(CONFIG.W / 2, CONFIG.H / 2, CONFIG.H * 0.35, CONFIG.W / 2, CONFIG.H / 2, CONFIG.H * 0.75);
    g.addColorStop(0, 'rgba(0,0,0,0)');
    g.addColorStop(1, 'rgba(0,0,0,0.45)');
    ctx.fillStyle = g;
    ctx.fillRect(0, 0, CONFIG.W, CONFIG.H);
    ctx.restore();
  }

  // -------------------------------------------------------------- extra juice

  // Radial ripple rings used for big events (on-top, tier-up, powerup pickup).
  ripple(x, y, color) {
    this.ripples.push({ x, y, r: 6, color, alive: true });
    if (this.ripples.length > 6) this.ripples.shift();
  }
}
