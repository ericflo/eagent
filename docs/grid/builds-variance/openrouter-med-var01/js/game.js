// game.js — state machine, heat/OTT system, scoring, collision orchestration, juice director
'use strict';

const Game = (() => {
  const S = {
    state: 'title', // title | levelintro | playing | levelclear | gameover | paused
    score: 0, best: +(localStorage.getItem('overkick_best') || 0),
    lives: CONFIG.START_LIVES, level: 0, levelName: '',
    combo: 0, comboTimer: 0, rally: 0,
    heat: 0, mult: 1, stage: 0, riding: false,
    everRode: false, breakthroughPending: false,
    slowmo: 0, hitstop: 0, flash: 0, chroma: 0,
    trauma: 0, shakeX: 0, shakeY: 0,
    shield: false, slowTimer: 0, splitterTimer: 0,
    banner: null, bannerT: 0,
    introT: 0, clearT: 0,
    time: 0,
    rng: Math.random,
  };
  const stars = [];
  let gridPhase = 0;
  let lastBreakthrough = false;

  function initStars() {
    stars.length = 0;
    for (let i = 0; i < 90; i++)
      stars.push({ x: Math.random() * CONFIG.LOGICAL_W, y: Math.random() * CONFIG.LOGICAL_H,
        z: 0.3 + Math.random() * 0.7 });
  }
  initStars();

  // ---------- flow ----------
  function startGame() {
    S.state = 'playing'; S.score = 0; S.lives = CONFIG.START_LIVES;
    S.level = 0; S.heat = 0; S.mult = 1; S.stage = 0; S.everRode = false;
    S.shield = false; S.slowTimer = 0; S.splitterTimer = 0;
    AudioSys.resume(); AudioSys.startMusic();
    startLevel(0);
    syncHUD();
  }

  function startLevel(i) {
    S.level = i;
    const L = Levels.get(i);
    S.levelName = L.name;
    Bricks.build(L.rows);
    Powerups.reset(); Balls.reset(); Paddle.reset();
    S.heat = 0; S.mult = 1; S.stage = 0; S.everRode = false; S.combo = 0; S.rally = 0;
    S.shield = false; S.slowTimer = 0; S.splitterTimer = 0;
    spawnServeBall();
    S.state = 'playing';
    syncHUD();
  }

  function spawnServeBall() {
    const b = Balls.make(Paddle.x, Paddle.y - 40, -Math.PI / 2, CONFIG.BALL_BASE_SPEED);
    b.stuck = true; b.stuckOff = 0;
    return b;
  }

  function launch() {
    let any = false;
    for (const b of Balls.list) if (b.stuck) {
      const a = -Math.PI / 2 + (Math.random() - 0.5) * CONFIG.STUCK_LAUNCH_ANGLE;
      Balls.setSpeed(b, CONFIG.BALL_BASE_SPEED);
      b.vx = Math.cos(a) * CONFIG.BALL_BASE_SPEED;
      b.vy = Math.sin(a) * CONFIG.BALL_BASE_SPEED;
      b.stuck = false; any = true;
    }
    if (any) AudioSys.sfx.ui();
    return any;
  }

  function togglePause() {
    if (S.state === 'playing') { S.state = 'paused'; showScreen('pause'); }
    else if (S.state === 'paused') { S.state = 'playing'; showScreen(null); }
  }

  // ---------- heat / OTT ----------
  function updateHeat(dt) {
    let riding = false;
    const ott = Bricks.ottYValue();
    for (const b of Balls.list)
      if (!b.stuck && b.y < ott - b.r) { riding = true; break; }
    S.riding = riding && Bricks.count() > 0;
    const wasAbove = riding;

    if (S.riding) {
      if (!S.everRode) {
        // breakthrough moment
        S.everRode = true;
        S.slowmo = CONFIG.HEAT_SLOWMO_TIME;
        S.flash = 1; S.chroma = 1;
        S.trauma = Math.min(1, S.trauma + 0.7);
        showBanner('OVER THE TOP!', 1.6, '#5eead4');
        AudioSys.sfx.ott();
        Particles.confetti(CONFIG.LOGICAL_W / 2, ott, 40);
        Particles.ring(CONFIG.LOGICAL_W / 2, ott, { vr: 900, life: 0.7, width: 6, color: '#5eead4' });
      }
      S.heat += dt * CONFIG.HEAT_RIDE_RATE;
    } else if (!Bricks.count()) {
      // level clear — keep heat until clear screen
    } else {
      S.heat = Math.max(0, S.heat - dt * CONFIG.HEAT_DECAY);
      if (S.heat === 0) S.everRode = false;
    }
    const stages = Math.min(CONFIG.HEAT_MAX_STAGES, Math.floor(S.heat / CONFIG.HEAT_STAGE_SECONDS));
    S.stage = stages;
    S.mult = Math.pow(2, stages);
    AudioSys.setIntensity(stages / CONFIG.HEAT_MAX_STAGES);
    if (S.stage >= 3) Particles.rain(0.1 + (S.stage - 3) * 0.12);
  }

  // ---------- juice ----------
  function addTrauma(v) { S.trauma = Math.min(1, S.trauma + v); }
  function hitstop(t) { S.hitstop = Math.max(S.hitstop, t); }
  function showBanner(text, dur, color) { S.banner = { text, color }; S.bannerT = dur; }

  // ---------- brick break ----------
  function breakBrick(brick, ball, hit, chain = false) {
    const cx = brick.x + brick.w / 2, cy = brick.y + brick.h / 2;
    const col = CONFIG.COLORS[brick.type.toLowerCase()] || '#fff';
    Bricks.remove(brick);
    Bricks.noteBroken();
    const pts = (brick.type === 'GOLD' ? CONFIG.SCORE_GOLD : CONFIG.SCORE_BRICK) * S.mult;
    S.score += pts;
    S.combo++; S.comboTimer = CONFIG.COMBO_WINDOW;
    Particles.shards(cx, cy, col, brick.type === 'GOLD' ? 22 : 14);
    Particles.text(cx, cy, '+' + pts, { color: col, size: 20 + Math.min(18, S.stage * 3) });
    if (S.combo >= 3) Particles.confetti(cx, cy, Math.min(12, S.combo));
    AudioSys.sfx.break_(S.combo);
    addTrauma(brick.type === 'GOLD' ? 0.35 : 0.18);
    hitstop(brick.type === 'GOLD' ? CONFIG.HITSTOP_BIG : CONFIG.HITSTOP_BREAK);

    if (brick.type === 'VOLATILE') {
      AudioSys.sfx.explosion();
      Particles.ring(cx, cy, { vr: 700, life: 0.5, width: 6, color: '#f87171' });
      Particles.shards(cx, cy, '#f87171', 26, 1.6);
      addTrauma(0.45); hitstop(CONFIG.HITSTOP_BIG);
      // chain: destroy STANDARD (+ others? spec says standard bricks) in radius, chain recursively
      const R = CONFIG.VOLATILE_RADIUS;
      const toBreak = [];
      for (const b2 of [...Bricks.grid]) {
        if (b2 === brick) continue;
        const bx = b2.x + b2.w / 2, by = b2.y + b2.h / 2;
        if (Math.hypot(bx - cx, by - cy) <= R && (b2.type === 'STANDARD' || b2.type === 'VOLATILE'))
          toBreak.push(b2);
      }
      for (const b2 of toBreak) breakBrick(b2, ball, hit, true);
      if (toBreak.length) {
        Particles.text(cx, cy - 26, 'CHAIN x' + (toBreak.length + 1), { color: '#f87171', size: 26 });
      }
    }

    // power-up drops with pity timer
    if (!chain || Math.random() < 0.3) {
      if (Bricks.sinceDrop >= CONFIG.PITY_LIMIT || Math.random() < CONFIG.DROP_CHANCE) {
        Powerups.spawn(cx, cy);
        Bricks.sinceDrop = 0;
      }
    }
    // splitter ball splits
    if (ball && ball.type === 'SPLITTER' && !chain && Balls.list.length < CONFIG.SPLIT_CAP) {
      const a = Math.atan2(ball.vy, ball.vx) + (Math.random() < 0.5 ? 0.6 : -0.6);
      Balls.make(ball.x, ball.y, a, Balls.speedOf(ball), 'SPLITTER');
    }
    if (Bricks.count() === 0) levelClear();
  }

  // ---------- ball vs brick resolution ----------
  function resolveBrickHits(ball, dt) {
    const hits = Bricks.collideBall(ball, this);
    for (const hit of hits) {
      const b = hit.brick;
      if (!Bricks.grid.includes(b)) continue; // already chain-destroyed this substep
      const impactAngle = hit.impactAngle, speed = hit.speed;
      let broke = false, bounced = true;

      // ball-type counters first
      if (ball.type === 'GHOST' && b.type === 'WEDGE') { bounced = false; } // ghost passes
      else if (ball.type === 'FIRE' && (b.type === 'STANDARD' || b.type === 'GOLD')) { broke = true; bounced = false; }
      else if (ball.type === 'FIRE' && b.type === 'VOLATILE') { broke = true; bounced = false; }
      else if (ball.type === 'HEAVY' && ball.pierceCount > 0) {
        broke = true; bounced = false; ball.pierceCount--;
        if (ball.pierceCount === 0 && ball.type === 'HEAVY') ball.type = 'NORMAL';
      } else {
        // brick-type gates
        switch (b.type) {
          case 'WEDGE':
            if (impactAngle > CONFIG.WEDGE_ANGLE) broke = true;
            else { AudioSys.sfx.clink(); Particles.sparks(hit.contact.x, hit.contact.y, 6); b.hitFlash = 1; addTrauma(0.08); }
            break;
          case 'KINETIC':
            if (speed >= CONFIG.KINETIC_MIN_SPEED) broke = true;
            else { AudioSys.sfx.clank(); Particles.sparks(hit.contact.x, hit.contact.y, 5); b.hitFlash = 1; addTrauma(0.08); }
            break;
          case 'PHASE':
            if (b.solid) broke = true; else bounced = false; // intangible
            break;
          default: broke = true;
        }
      }

      if (broke) {
        breakBrick(b, ball, hit);
        if (bounced) { /* pierced types also may bounce if bounced stays true */ }
      }
      if (bounced) Bricks.reflectBall(ball, hit.nx, hit.ny);
      // push ball along path if pierced to avoid re-hit
      if (bounced === false && broke) {
        ball.x += ball.vx * dt * 1.5; ball.y += ball.vy * dt * 1.5;
      }
      if (broke || bounced) break; // one hit per substep
    }
  }

  // ---------- ball vs paddle ----------
  function resolvePaddle(ball) {
    if (ball.stuck || ball.vy <= 0) return;
    const pw = Paddle.w, ph = Paddle.h;
    if (Math.abs(ball.x - Paddle.x) > pw / 2 + ball.r) return;
    if (ball.y + ball.r < Paddle.y - ph / 2 || ball.y - ball.r > Paddle.y + ph / 2) return;
    // contact offset -> reflection angle
    const off = Util.clamp((ball.x - Paddle.x) / (pw / 2), -1, 1);
    let a = -Math.PI / 2 + off * 1.05;
    const punched = Paddle.onBallHit();
    let speed = Balls.speedOf(ball) + CONFIG.BALL_SPEEDUP_PER_SEC * 6;
    if (punched) {
      speed += CONFIG.PUNCH_BOOST;
      a -= Math.sign(off || 0.001) * CONFIG.PUNCH_ANGLE_STEER * -1; // steer outward+up
      AudioSys.sfx.punch();
      Particles.streaks(ball.x, Paddle.y - 10, 8);
      Particles.text(ball.x, Paddle.y - 40, 'PUNCH!', { color: '#fb923c', size: 30 });
      addTrauma(0.4); hitstop(CONFIG.HITSTOP_BIG);
      S.flash = Math.max(S.flash, 0.35);
    }
    AudioSys.sfx.paddle(S.rally++);
    // paddle vy influence
    const vyInfluence = Util.clamp(Paddle.vy / 1200, -0.25, 0.25);
    a += vyInfluence * 0.5 * Math.sign(off || 1) * -1;
    a = Util.clamp(a, -Math.PI + 0.35, -0.35);
    ball.vx = Math.cos(a) * speed; ball.vy = Math.sin(a) * speed;
    ball.y = Paddle.y - ph / 2 - ball.r - 1;
    // magnet catch
    if (Paddle.magnetTimer > 0) { ball.stuck = true; ball.stuckOff = ball.x - Paddle.x; }
    addTrauma(0.12);
    Paddle.squash = 1;
  }

  // ---------- ball loss / shield ----------
  function resolveFloor() {
    for (let i = Balls.list.length - 1; i >= 0; i--) {
      const b = Balls.list[i];
      if (b.stuck) continue;
      if (b.y + b.r >= CONFIG.SHIELD_Y && S.shield && b.vy > 0) {
        S.shield = false; b.vy = -Math.abs(b.vy); b.y = CONFIG.SHIELD_Y - b.r - 2;
        AudioSys.sfx.capsule(); Particles.ring(b.x, CONFIG.SHIELD_Y, { color: '#a78bfa', vr: 600 });
        Particles.text(b.x, CONFIG.SHIELD_Y - 24, 'SHIELD!', { color: '#a78bfa' });
        addTrauma(0.3);
      }
      if (b.y - b.r > CONFIG.LOGICAL_H) Balls.list.splice(i, 1);
    }
    if (Balls.list.length === 0) loseLife();
  }

  function loseLife() {
    S.lives--; S.rally = 0;
    S.heat = Math.max(0, S.heat - 1.5);
    AudioSys.sfx.loseBall();
    addTrauma(0.5);
    if (S.lives <= 0) {
      S.state = 'gameover';
      S.best = Math.max(S.best, S.score);
      localStorage.setItem('overkick_best', String(S.best));
      showScreen('gameover');
      AudioSys.sfx.gameOver(); AudioSys.stopMusic();
      document.getElementById('goScore').textContent = S.score.toLocaleString();
      document.getElementById('goBest').textContent = S.best.toLocaleString();
      document.getElementById('goLevel').textContent = S.level + 1;
    } else {
      Particles.text(CONFIG.LOGICAL_W / 2, CONFIG.LOGICAL_H / 2, 'BALL LOST', { color: '#f87171', size: 34 });
      spawnServeBall();
      S.everRode = false;
    }
    syncHUD();
  }

  function levelClear() {
    S.state = 'levelclear';
    S.clearT = 0;
    const bonus = S.lives * CONFIG.SCORE_LEVEL_CLEAR_LIFE_BONUS;
    S.score += bonus;
    AudioSys.sfx.levelClear();
    Particles.confetti(CONFIG.LOGICAL_W / 2, CONFIG.LOGICAL_H / 3, 60);
    syncHUD();
    setTimeout(() => {
      if (S.state === 'levelclear') { AudioSys.setIntensity(0); startLevel(S.level + 1); }
    }, 1800);
  }

  // ---------- main step ----------
  function step(dt) {
    S.time += dt;
    // decay juice timers always (real-time-ish)
    S.flash = Math.max(0, S.flash - dt * 3);
    S.chroma = Math.max(0, S.chroma - dt * 2);
    if (S.bannerT > 0) S.bannerT -= dt;
    S.trauma = Math.max(0, S.trauma - CONFIG.TRAUMA_DECAY * dt);
    const sh = S.trauma * S.trauma * CONFIG.SHAKE_MAX * (1 + S.stage * 0.06);
    S.shakeX = (Math.random() * 2 - 1) * sh; S.shakeY = (Math.random() * 2 - 1) * sh;

    if (S.state !== 'playing') return;

    // time scaling: slow-mo & slow power-up & hitstop
    let scale = 1;
    if (S.slowTimer > 0) { scale *= CONFIG.SLOW_FACTOR; S.slowTimer -= dt; }
    if (S.slowmo > 0) { S.slowmo -= dt; scale *= CONFIG.HEAT_SLOWMO_SCALE; }
    if (S.hitstop > 0) { S.hitstop -= dt; scale = 0; }
    const sdt = dt * scale;

    // input / paddle
    Input.resolve(dt);
    const punchedGesture = Input.consumePunch();
    Paddle.update(dt, Input);
    if (punchedGesture) { /* gesture punch nudges paddle vy for next contact */ Paddle.vy = Math.min(Paddle.vy, CONFIG.PUNCH_VY_THRESHOLD * 1.2); }

    if (Input.consumeLaunch()) launch();

    // stuck balls follow paddle
    for (const b of Balls.list) {
      if (b.stuck) { b.x = Paddle.x + b.stuckOff; b.y = Paddle.y - Paddle.h / 2 - b.r - 2; b.trail.length = 0; }
    }

    if (sdt > 0) {
      Balls.update(sdt);
      Bricks.update(sdt, this);
      for (const b of Balls.list) {
        resolveBrickHits(b, sdt);
        resolvePaddle(b);
      }
      resolveFloor();
      Powerups.update(sdt, this);
      if (S.splitterTimer > 0) { S.splitterTimer -= sdt; if (S.splitterTimer <= 0) for (const b of Balls.list) if (b.type === 'SPLITTER') b.type = 'NORMAL'; }
      // gradual speedup
      if (Balls.list.length) {
        const sp = Balls.speedOf(Balls.list[0]);
        if (sp < CONFIG.BALL_MAX_SPEED && !Balls.list[0].stuck)
          Balls.setSpeed(Balls.list[0], Math.min(CONFIG.BALL_MAX_SPEED, sp + CONFIG.BALL_SPEEDUP_PER_SEC * sdt * 4));
      }
      updateHeat(sdt);
      if (S.comboTimer > 0) { S.comboTimer -= sdt; if (S.comboTimer <= 0) S.combo = 0; }
    }
    Particles.update(dt);

    gridPhase += dt * (1 + S.stage);
  }

  function syncHUD() { if (Game.syncHUD) Game.syncHUD(); }
  function showScreen(name) { if (Game.showScreen) Game.showScreen(name); }

  return { S, step, startGame, startLevel, launch, togglePause, stars, breakBrick, spawnServeBall,
    get gridPhase() { return gridPhase; } };
})();