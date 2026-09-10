// Above the Bricks — core engine. Canvas game, all coordinates in CSS px;
// the canvas backing store is scaled by devicePixelRatio in resize().

import { LEVELS, generateLevel, COLS } from "./levels.js";
import * as S from "./audio.js";

export const BRICK_TYPES = {
  "#": 0, r: 0, g: 0,      // standard (3 colors by char)
  y: 1,                     // gold  -> drops a power-up
  o: 2,                     // hole  -> break-through path to the roof
  a: 3,                     // angle -> only breaks on a glancing hit
  s: 4,                     // speed -> only breaks on a fast hit
  G: 5,                     // glass -> reveals the mini-map on contact
  "^": 6,                   // gem   -> needs fast AND glancing
};

const BRICK_COLORS = {
  "#": "#4d7cfe",
  r: "#f0506e",
  g: "#2fd07a",
  y: "#ffd166",
  o: "#1b2347",
  a: "#f2a03d",
  s: "#e056fd",
  G: "#9be8ff",
  "^": "#a78bfa",
};

const BASE_PTS = { 0: 100, 1: 50, 2: 120, 3: 200, 4: 250, 5: 150, 6: 350 };
const ROOF_PTS = 25;
const ROOF_MIN_Y = 4;            // physics ceiling
const MAX_BALL_SPEED = 620;      // hard cap: wall boosts + paddle thrust compound energy
const COLORS = {
  cyan: "#4cc9f0",
  pink: "#ff6ec7",
  violet: "#a78bfa",
  gold: "#ffd166",
  orange: "#ff9f43",
  white: "#ffffff",
  red: "#ff4d5a",
};

function clamp(v, a, b) { return v < a ? a : v > b ? b : v; }
function lerp(a, b, t) { return a + (b - a) * t; }
function rnd(a, b) { return a + Math.random() * (b - a); }
function dist2(ax, ay, bx, by) { const dx = ax - bx, dy = ay - by; return dx * dx + dy * dy; }

export class Game {
  constructor(canvas, hooks = {}) {
    this.canvas = canvas;
    this.ctx = canvas.getContext("2d");
    this.hooks = hooks;
    this.dpr = 1;
    this.W = 360;
    this.H = 640;
    this.state = "menu";          // menu | serve | play | levelclear | gameover
    this.paused = false;
    this.t = 0;

    this.score = 0;
    this.best = +(localStorage.getItem("atb_best") || 0);
    this.lives = 3;
    this.level = 1;
    this.streak = 0;              // consecutive brick breaks
    this.mult = 1;
    this.lastBreakAt = 0;
    this.nextLifeAt = 30000;
    this.totalBricks = 0;
    this.powerLevel = 0;          // 0..4 -> drives how crazy the fx get

    this.bricks = [];
    this.balls = [];
    this.drops = [];
    this.bullets = [];
    this.particles = [];
    this.texts = [];
    this.rings = [];
    this.shakeAmt = 0;
    this.flashA = 0;
    this.flashColor = "#ffffff";

    // paddle
    this.paddle = {
      x: this.W / 2, y: 0, w: 76, h: 12, vx: 0, vy: 0,
      targetX: null, targetY: null,
      slapVel: 0, slapT: 0,
      laserT: 0, wideT: 0,
    };
    this.powerRems = { slow: 0 };
    this.multiBalls = 1;

    // layout (recomputed on resize)
    this.cellW = 0; this.cellH = 0;
    this.brickTop = 52;
    this.boardL = 6; this.boardR = 6;
    this.floorY = 0;
    this.roofZone = false;

    // input (main.js feeds this)
    this.pointer = { active: false, x: 0, y: 0, downInGame: false };
    this.keys = new Set();

    // timers
    this.levelMsgT = 0;
    this.serveWait = 0;
    this.mapRevealT = 0;          // glass mini-map reveal

    this.resize();
    this.loadLevel(1, true);
    this.reset();

    this.lastNow = performance.now();
    this.raf = requestAnimationFrame(this.loop);
  }

  // ---------------------------------------------------------------- setup
  resize() {
    const vw = window.innerWidth, vh = window.innerHeight;
    const margin = 10;
    const maxA = 9 / 14;            // tall-ish default, portrait-friendly
    let W = Math.min(vw - margin * 2, 460);
    let H = Math.min(vh - margin * 2, 860);
    if (W / H > maxA) W = H * maxA;
    H = Math.max(H, 460);
    W = Math.max(W, 280);
    this.W = Math.round(W);
    this.H = Math.round(H);
    this.dpr = Math.min(window.devicePixelRatio || 1, 2);
    this.canvas.style.width = this.W + "px";
    this.canvas.style.height = this.H + "px";
    this.canvas.width = Math.round(this.W * this.dpr);
    this.canvas.height = Math.round(this.H * this.dpr);

    this.cellW = (this.W - (this.boardL + this.boardR)) / COLS;
    this.cellH = Math.max(12, this.cellW * 0.46);
    this.floorY = this.H - 52;
    this.paddle.y = this.floorY - 16;

    // rebuild brick geometry
    for (const b of this.bricks) {
      b.w = this.cellW - 4;
      b.h = this.cellH - 4;
      b.x = this.boardL + 2 + b.col * this.cellW;
      b.y = this.brickTop + 2 + b.row * this.cellH;
    }
  }

  reset() {
    this.score = 0;
    this.lives = 3;
    this.streak = 0;
    this.mult = 1;
    this.nextLifeAt = 30000;
    this.resetPaddlePower();
    this.balls.forEach((b) => (b.alive = false));
    this.balls.length = 0;
    this.drops.length = 0;
    this.bullets.length = 0;
    this.texts.length = 0;
    this.particles.length = 0;
    this.setMultUi();
  }

  resetPaddlePower() {
    this.paddle.wideT = 0;
    this.paddle.laserT = 0;
    this.powerRems.slow = 0;
    this.paddle.w = Math.round(clamp(this.W * 0.21, 68, 96));
  }

  loadLevel(n, isFirst) {
    this.level = n;
    const def = LEVELS[n - 1] || generateLevel(n);
    this.bricks = [];
    const rows = def.rows;
    for (let row = 0; row < rows.length; row++) {
      const line = rows[row];
      for (let col = 0; col < line.length && col < COLS; col++) {
        const ch = line[col];
        if (ch === ".") continue;
        const type = BRICK_TYPES[ch] ?? 0;
        this.bricks.push({
          type,
          color: BRICK_COLORS[ch] || "#4d7cfe",
          row, col,
          x: 0, y: 0, w: 0, h: 0,
          alive: true,
          pulse: Math.random() * 6.28,
          originChar: ch,
        });
      }
    }
    this.resize(); // lays out brick geometry
    this.totalBricks = this.bricks.length;
    this.brickCols = rows.length;
    this.roofZone = false;
    if (!isFirst) this.setLevelMsg(`LEVEL ${n}`, def.name || "");
  }

  // ---------------------------------------------------------------- public API
  pointerDown(x, y) {
    this.pointer.active = true;
    this.pointer.downInGame = true;
    this.pointer.x = x; this.pointer.y = y;
    if (this.state === "menu") this.startGame();
    else if (this.state === "serve") this.launchBall(x);
    else if (this.state === "gameover") this.startGame();
  }
  pointerMove(x, y) {
    if (!this.pointer.active) return;
    const dx = x - this.pointer.x, dy = y - this.pointer.y;
    // upward swipe -> slap
    if (dy < -34 && Math.abs(dy) > Math.abs(dx) * 1.3 && this.state !== "gameover") {
      this.paddle.slapVel = clamp(-dy, 0, 620);
      this.paddle.slapT = 0.28;
      this.shake(4);
      S.sfxPaddle();
      this.emitSparks(this.paddle.x, this.paddle.y, COLORS.cyan, 10);
    }
    this.pointer.x = x; this.pointer.y = y;
    this.paddle.targetX = x;
    this.paddle.targetY = y;
  }
  pointerUp() { this.pointer.active = false; }

  setKey(k, down) {
    if (down) this.keys.add(k);
    else this.keys.delete(k);
  }

  launchBall(tx) {
    if (this.state !== "serve") return;
    const p = this.paddle;
    const b = this.balls[0];
    if (!b) return;
    const dirX = clamp((tx - p.x) / (p.w / 2 + 4), -0.95, 0.95);
    const angle = dirX * 1.05; // radians from vertical
    const spd = Math.max(250, this.paddleBase() * 1.05);
    b.vx = Math.sin(angle) * spd;
    b.vy = -Math.cos(angle) * spd;
    b.resting = false;
    this.state = "play";
    this.pulse = 0;
    this.setStatsUi();
    S.sfxRise();
  }

  startGame() {
    this.reset();
    this.loadLevel(1, true);
    this.state = "serve";
    this.nextLifeAt = 30000;
    this.spawnServeBall();
    this.setStatsUi();
    S.sfxLevel();
  }

  spawnServeBall() {
    const p = this.paddle;
    const b = this.makeBall(p.x, p.y - 12, 0, 0, 196);
    b.resting = true;
  }

  makeBall(x, y, vx, vy, hue) {
    const b = {
      x, y, vx, vy, r: 7,
      hue: hue || Math.round(Math.random() * 30 + 10),
      alive: true, resting: false,
      hist: [],
      roofBounces: 0,
    };
    this.balls.push(b);
    return b;
  }

  // ---------------------------------------------------------------- power
  paddleBase() { return 268; }

  activatePower(kind) {
    const p = this.paddle;
    switch (kind) {
      case "WIDE":
        p.wideT = 15;
        p.w = Math.round(clamp(this.W * 0.34, 110, 150));
        this.banner("WIDE PADDLE", COLORS.cyan);
        break;
      case "SLOW":
        this.powerRems.slow = 8;
        this.banner("TIME DILATION", COLORS.violet);
        break;
      case "LASER":
        p.laserT = 10;
        this.banner("LASER PADDLE", COLORS.orange);
        break;
      case "MULTI":
        this.multiBalls = Math.min(this.multiBalls + 2, 5);
        this.banner("+2 BALLS", COLORS.pink);
        // spawn immediately from existing balls
        const srcs = this.balls.filter((b) => b.alive && !b.resting);
        if (srcs.length) {
          const src = srcs[0];
          const spd = Math.hypot(src.vx, src.vy) || this.paddleBase();
          let ang = Math.atan2(src.vy, src.vx);
          for (let i = 0; i < 2; i++) {
            const a = ang + (i === 0 ? -0.42 : 0.42);
            this.makeBall(src.x, src.y, Math.cos(a) * spd, Math.sin(a) * spd, Math.round(Math.random() * 360));
          }
          S.sfxPower();
        }
        this.setStatsUi();
        return;
      case "BOMB":
        // clear the lowest brick row
        for (const b of this.bricks) {
          if (b.alive && b.row === this.maxBrickRow()) {
            this.breakBrick(b, this.balls[0] || null, true);
          }
        }
        this.banner("ROW CLEAR", COLORS.orange);
        S.sfxRico();
        return;
    }
    S.sfxPower();
    this.setStatsUi();
  }

  maxBrickRow() {
    let m = -1;
    for (const b of this.bricks) if (b.alive && b.row > m) m = b.row;
    return m;
  }

  banner(str, color) {
    this.addText(this.W / 2, this.H * 0.32, str, color, 17, 1.6);
  }

  setLevelMsg(str, sub) {
    this.addText(this.W / 2, this.H * 0.36, str, COLORS.gold, 22, 1.8);
    if (sub) this.addText(this.W / 2, this.H * 0.36 + 24, sub, COLORS.cyan, 13, 1.8);
  }

  // ---------------------------------------------------------------- scoring
  setMultUi() {
    const mult = this.mult;
    const pill = document.getElementById("combo-pill");
    if (!pill) return;
    if (mult > 1) {
      pill.classList.remove("hidden");
      document.getElementById("combo-count").textContent = this.streak;
      document.getElementById("combo-mult").textContent = mult;
      pill.classList.remove("pop");
      void pill.offsetWidth;
      pill.classList.add("pop");
    } else {
      pill.classList.add("hidden");
    }
  }

  setStatsUi() {
    const h = this.hooks;
    if (h.onStats) h.onStats({
      score: this.score, best: Math.max(this.best, this.score),
      lives: this.lives, level: this.level, mult: this.mult, roof: this.roofZone,
    });
  }

  addPoints(base) {
    let pts = base * this.mult;
    if (this.roofZone) pts *= 2;
    this.score += pts;
    if (this.score >= this.nextLifeAt) {
      this.nextLifeAt += 30000;
      this.lives = Math.min(this.lives + 1, 5);
      this.banner("1UP!", COLORS.gold);
      S.sfxGold();
    }
    this.setStatsUi();
  }

  onBreak(pts) {
    this.streak++;
    this.lastBreakAt = this.t;
    this.mult = 1 + Math.floor(this.streak / 6);
    if (this.mult > 10) this.mult = 10;
    this.powerLevel = Math.min(4, Math.floor(this.streak / 8));
    this.setMultUi();
    this.setStatsUi();
  }

  // ---------------------------------------------------------------- fx
  shake(p) { this.shakeAmt = Math.min(this.shakeAmt + p, 18); }
  flash(color, a) { this.flashColor = color; this.flashA = Math.max(this.flashA, a); }

  spawnParticles(x, y, color, n, opts = {}) {
    const speed = opts.speed ?? 180;
    for (let i = 0; i < n; i++) {
      const a = rnd(0, Math.PI * 2);
      const v = rnd(speed * 0.3, speed);
      this.particles.push({
        x, y,
        vx: Math.cos(a) * v, vy: Math.sin(a) * v - (opts.up ?? 60),
        life: rnd(0.4, 0.95), maxLife: 1,
        size: rnd(2, opts.size ?? 5),
        color, grav: opts.grav ?? 220, drag: opts.drag ?? 0.9,
      });
    }
  }

  emitSparks(x, y, color, n) {
    for (let i = 0; i < n; i++) {
      const a = rnd(0, Math.PI * 2);
      this.particles.push({
        x, y, vx: Math.cos(a) * rnd(60, 260), vy: Math.sin(a) * rnd(60, 260),
        life: rnd(0.2, 0.5), maxLife: 1, size: rnd(1.5, 3), color,
        grav: 40, drag: 0.85,
      });
    }
  }

  addText(x, y, str, color, size = 14, life = 0.9) {
    this.texts.push({ x, y, str, color, size, life, maxLife: life, vy: -60 });
  }

  addRing(x, y, color) {
    this.rings.push({ x, y, r: 4, vr: 260, life: 0.4, maxLife: 0.4, color });
  }

  // ---------------------------------------------------------------- level flow
  breakBrick(brick, ball, noPoints = false) {
    if (!brick.alive) return;
    brick.alive = false;
    const cx = brick.x + brick.w / 2, cy = brick.y + brick.h / 2;
    if (!noPoints) {
      this.addPoints(BASE_PTS[brick.type] || 100);
      this.onBreak(BASE_PTS[brick.type] || 100);
    }
    const nburst = 14 + this.powerLevel * 5 + (this.roofZone ? 10 : 0);
    this.spawnParticles(cx, cy, brick.color || COLORS.cyan, nburst, { speed: 240 + this.powerLevel * 60 });
    this.addRing(cx, cy, brick.color || COLORS.white);
    if (this.powerLevel >= 2) this.shake(2 + this.powerLevel);
    S.sfxBrick(brick.row, this.powerLevel);
    this.flash(brick.color || "#ffffff", 0.05);

    if (brick.type === 1) { // gold -> drop
      const kind = ["WIDE", "SLOW", "LASER", "MULTI", "BOMB"][Math.floor(Math.random() * 5)];
      this.drops.push({ x: cx, y: cy, vy: 90, kind, alive: true, t: 0 });
    }
    if (brick.type === 5) this.mapRevealT = 2.5; // glass reveals map
    this.checkLevelClear();
  }

  checkLevelClear() {
    for (const b of this.bricks) if (b.alive) return;
    // all gone
    this.state = "levelclear";
    const bonus = this.lives * 500;
    this.score += bonus;
    this.setStatsUi();
    this.addText(this.W / 2, this.H * 0.4, `CLEAR! +${bonus}`, COLORS.gold, 22, 2);
    S.sfxLevel();
    setTimeout(() => {
      this.loadLevel(this.level + 1, false);
      this.state = "serve";
      this.balls.length = 0;
      this.drops.length = 0;
      this.bullets.length = 0;
      this.resetPaddlePower();
      this.spawnServeBall();
      this.roofZone = false;
    }, 1800);
  }

  loseBall(b) {
    b.alive = false;
    b.resting = false;
    this.spawnParticles(b.x, b.y, COLORS.red, 16, { speed: 180 });
    S.sfxLostBall();
  }

  afterBallLost() {
    this.streak = 0;
    this.mult = 1;
    this.powerLevel = 0;
    this.setMultUi();
    this.lives--;
    this.resetPaddlePower();
    const left = this.balls.some((x) => x.alive);
    if (this.lives <= 0 && !left) {
      this.state = "gameover";
      if (this.score > this.best) {
        this.best = this.score;
        localStorage.setItem("atb_best", String(this.best));
      }
      S.sfxDie();
      return;
    }
    if (!left) {
      this.state = "serve";
      this.spawnServeBall();
    }
    this.setStatsUi();
  }

  // ---------------------------------------------------------------- update
  loop = (now) => {
    const dt = clamp((now - this.lastNow) / 1000, 0, 0.033);
    this.lastNow = now;
    if (!this.paused) this.update(dt);
    this.draw();
    this.raf = requestAnimationFrame(this.loop);
  };

  update(dt) {
    this.t += dt;
    this.updateFx(dt);
    if (this.state === "menu") {
      this.menuDrift(dt);
      return;
    }
    if (this.state === "serve") {
      const p = this.paddle;
      const b = this.balls.find((x) => x.resting);
      if (b) { b.x = p.x; b.y = p.y - 12; }
      this.updatePaddle(dt);
      this.setStatsUi();
      return;
    }
    if (this.state === "gameover" || this.state === "levelclear") {
      this.updatePaddle(dt);
      return;
    }
    // play
    this.updatePaddle(dt);
    this.updateDrops(dt);
    this.updateBullets(dt);
    // slow-motion: damp ball velocities once per frame (not per substep)
    if (this.powerRems.slow > 0) {
      const f = Math.pow(0.55, dt);
      for (const b of this.balls) { if (b.alive && !b.resting) { b.vx *= f; b.vy *= f; } }
      this.powerRems.slow = Math.max(0, this.powerRems.slow - dt);
    }
    const steps = Math.max(1, Math.ceil((this.maxBallSpeed() * dt) / 4.2));
    const sdt = dt / steps;
    for (let i = 0; i < steps; i++) {
      this.updateBalls(sdt);
    }
    // streak decay
    if (this.t - this.lastBreakAt > 1.5) {
      this.streak = 0;
      this.mult = 1;
      this.powerLevel = 0;
      this.setMultUi();
      this.setStatsUi();
    }
    this.mapRevealT = Math.max(0, this.mapRevealT - dt);
  }

  maxBallSpeed() {
    let m = 0;
    for (const b of this.balls) if (b.alive) m = Math.max(m, Math.hypot(b.vx, b.vy));
    return Math.max(m, this.paddleBase());
  }

  menuDrift(dt) {
    // ambient particles drifting up
    if (Math.random() < dt * 12) {
      this.particles.push({
        x: rnd(0, this.W), y: this.H + 8,
        vx: rnd(-14, 14), vy: rnd(-70, -30),
        life: rnd(1.5, 3), maxLife: 3, size: rnd(1.5, 3.5),
        color: Math.random() < 0.5 ? COLORS.cyan : COLORS.violet, grav: 0, drag: 1,
      });
    }
  }

  updateFx(dt) {
      this.shakeAmt = Math.max(0, this.shakeAmt - dt * 40);
    this.flashA = Math.max(0, this.flashA - dt * 2.2);
    for (let i = this.particles.length - 1; i >= 0; i--) {
      const p = this.particles[i];
      p.life -= dt;
      if (p.life <= 0) { this.particles.splice(i, 1); continue; }
      p.vy += p.grav * dt;
      p.vx *= Math.pow(p.drag, dt * 60);
      p.vy *= Math.pow(p.drag, dt * 60);
      p.x += p.vx * dt; p.y += p.vy * dt;
    }
    for (let i = this.texts.length - 1; i >= 0; i--) {
      const t = this.texts[i];
      t.life -= dt;
      if (t.life <= 0) { this.texts.splice(i, 1); continue; }
      t.y += t.vy * dt;
    }
    for (let i = this.rings.length - 1; i >= 0; i--) {
      const r = this.rings[i];
      r.life -= dt; r.r += r.vr * dt;
      if (r.life <= 0) this.rings.splice(i, 1);
    }
  }

  updatePaddle(dt) {
    const p = this.paddle;
    const k = 14; // keyboard speed
    let ax = 0, ay = 0;
    if (this.keys.has("left") || this.keys.has("a")) ax -= 1;
    if (this.keys.has("right") || this.keys.has("d")) ax += 1;
    if (this.keys.has("up") || this.keys.has("w")) ay -= 1;
    if (this.keys.has("down") || this.keys.has("s")) ay += 1;

    if (ax !== 0 || ay !== 0) {
      p.targetX = clamp(p.x + ax * k * 12 * dt * 60 * 0.016, p.w / 2, this.W - p.w / 2);
      p.targetY = clamp(p.y + ay * k * 9 * dt * 60 * 0.016, this.paddleYMin(), this.floorY - 11);
    }

    const tx = p.targetX != null ? p.targetX : p.x;
    const ty = p.targetY != null ? p.targetY : p.y;
    const nx = clamp(tx, p.w / 2, this.W - p.w / 2);
    const ny = clamp(ty, this.paddleYMin(), this.floorY - 11);
    const px = p.x, py = p.y;
    p.x = lerp(px, nx, 1 - Math.pow(0.0001, dt));
    p.y = lerp(py, ny, 1 - Math.pow(0.0004, dt));
    p.vx = (p.x - px) / Math.max(dt, 0.001);
    p.vy = (p.y - py) / Math.max(dt, 0.001);
    if (p.slapT > 0) {
      p.slapT -= dt;
      p.y -= p.slapVel * dt;
      if (p.slapT <= 0) p.slapVel = 0;
    }
    // reaction to ball resting
    if (this.balls.some((b) => b.resting)) {
      const b = this.balls.find((x) => x.resting);
      if (b) { b.x = p.x; b.y = p.y - 12; }
    }
  }

  paddleYMin() {
    const topRowY = this.brickTop + Math.max(0, this.brickCols - 0) * this.cellH;
    return Math.min(Math.max(topRowY + 18, this.brickTop + 40), this.floorY - 60);
  }

  updateDrops(dt) {
    const p = this.paddle;
    for (let i = this.drops.length - 1; i >= 0; i--) {
      const d = this.drops[i];
      d.t += dt;
      d.y += d.vy * dt;
      d.x += Math.sin(d.t * 4) * 26 * dt;
      const px = Math.abs(d.x - p.x) < p.w / 2 + 8;
      const py = Math.abs(d.y - p.y) < 18;
      if (px && py) {
        this.drops.splice(i, 1);
        this.activatePower(d.kind);
        this.spawnParticles(d.x, d.y, COLORS.gold, 16, { speed: 160 });
        continue;
      }
      if (d.y > this.H + 20) this.drops.splice(i, 1);
    }
  }

  updateBullets(dt) {
    const p = this.paddle;
    if (p.laserT > 0) {
      p.laserT -= dt;
      const jitter = Math.random();
      if (this.laserCd == null || this.laserCd <= 0) {
        this.laserCd = 0.34;
        if (p.laserT > 0) {
          this.bullets.push({ x: p.x, y: p.y - 8, vy: -640, alive: true });
          this.bullets.push({ x: p.x + 14, y: p.y - 8, vy: -640, alive: true });
          S.sfxGold();
        }
      }
      this.laserCd -= dt;
    } else if (this.laserCd != null) this.laserCd = 0;
    if (p.wideT > 0) p.wideT -= dt;
    if (p.wideT <= 0 && p.laserT <= 0 && this.paddle.w > clamp(this.W * 0.21, 68, 96)) {
      p.w = Math.round(clamp(this.W * 0.21, 68, 96));
    }

    for (let i = this.bullets.length - 1; i >= 0; i--) {
      const bl = this.bullets[i];
      bl.y += bl.vy * dt;
      if (bl.y < -10) { this.bullets.splice(i, 1); continue; }
      // hit bricks
      outer:
      for (const b of this.bricks) {
        if (!b.alive) continue;
        if (Math.abs(bl.x - (b.x + b.w / 2)) < b.w / 2 + 3 && Math.abs(bl.y - (b.y + b.h / 2)) < b.h / 2 + 3) {
          this.bullets.splice(i, 1);
          if (b.type === 0 || b.type === 2 || b.type === 5) {
            this.breakBrick(b, null);
          } else if (b.type === 1) {
            this.breakBrick(b, null);
          } else {
            // angle/speed/gem get debuffed into standard
            b.type = 0;
            b.color = "#b8c4ff";
            this.addText(b.x + b.w / 2, b.y - 6, "DEBUFF", COLORS.white, 10, 0.7);
          }
          break outer;
        }
      }
    }
  }

  updateBalls(dt) {
    const p = this.paddle;
    const slow = this.powerRems.slow > 0 ? 0.55 : 1;
    for (let i = this.balls.length - 1; i >= 0; i--) {
      const b = this.balls[i];
      if (!b.alive) { this.balls.splice(i, 1); continue; }
      if (b.resting) continue;

      b.vx *= slow;
      b.vy *= slow;
      b.x += b.vx * dt;
      b.y += b.vy * dt;

      // history trail
      b.hist.push({ x: b.x, y: b.y });
      if (b.hist.length > 14) b.hist.shift();

      // ensure min horizontal speed (avoid boring vertical-only pins)
      if (Math.abs(b.vx) < 26) b.vx += (b.vx >= 0 ? 1 : -1) * 40;

      // walls
      if (b.x - b.r < 0) { b.x = b.r; b.vx = Math.abs(b.vx) * (1 + Math.abs(p.vx) * 0.004); S.sfxWall(); this.emitSparks(b.x, b.y, COLORS.cyan, 4); }
      if (b.x + b.r > this.W) { b.x = this.W - b.r; b.vx = -Math.abs(b.vx); S.sfxWall(); this.emitSparks(b.x, b.y, COLORS.cyan, 4); }
      if (b.y - b.r < ROOF_MIN_Y) { b.y = ROOF_MIN_Y + b.r; b.vy = Math.abs(b.vy); if (this.roofZone) { this.addPoints(ROOF_PTS); this.emitSparks(b.x, b.y, COLORS.gold, 5); } else { S.sfxWall(); } }

      // roof ramp: speed up while up top
      if (this.roofZone) {
        const sp = Math.hypot(b.vx, b.vy);
        if (sp < 560) {
          const f = 1 + dt * 0.09;
          b.vx *= f; b.vy *= f;
        }
      }

      // paddle
      if (b.vy > 0 && b.y + b.r >= p.y && b.y - b.r <= p.y + p.h + 6 + dt * Math.abs(b.vy) &&
          Math.abs(b.x - p.x) <= p.w / 2 + b.r * 0.9) {
        this.paddleHit(b, p);
      }

      // bricks
      this.collideBricks(b, dt);

      // lost below floor
      if (b.y - b.r > this.floorY + 34) {
        this.loseBall(b);
        this.afterBallLost();
        continue;
      }

      // hard cap on speed so wall boosts + paddle thrust can't compound out of control
      const spd = Math.hypot(b.vx, b.vy);
      if (spd > MAX_BALL_SPEED) {
        const f = MAX_BALL_SPEED / spd;
        b.vx *= f; b.vy *= f;
      }

      // ensure min vertical speed: a ball with vy≈0 can hover above the paddle's
      // reach forever (the level becomes unbeatable), since nothing ever sends it
      // back down. Keep a gentle downward drift so it always comes back to the paddle.
      if (Math.abs(b.vy) < 34) b.vy = b.vy >= 0 ? 34 : -34;

      // roof zone check
      this.roofCheck(b);
    }
    this.setStatsUi();
  }

  roofCheck(b) {
    const wallTop = this.brickTop - 8;
    if (!this.roofZone && b.y < wallTop) {
      this.roofZone = true;
      this.addText(this.W / 2, this.H * 0.28, "ROOF MODE ×2", COLORS.gold, 24, 1.8);
      this.addRing(this.W / 2, this.brickTop, COLORS.gold);
      this.flash("#ffd166", 0.14);
      S.sfxRoof();
    }
  }

  paddleHit(b, p) {
    const rel = clamp((b.x - p.x) / (p.w / 2), -0.8, 0.8);
    const angle = rel * 1.08;
    let spd = Math.max(300, Math.hypot(b.vx, b.vy) * 1.03);
    if (p.slapT > 0) spd *= 1.5;            // upward sweep -> extra oomph
    const sp = Math.sin(angle), cp = -Math.cos(angle);
    b.vx = sp * spd + p.vx * 0.4;
    b.vy = cp * spd + p.vy * 0.55;          // moving up adds upward velocity
    b.y = p.y - b.r - 0.5;
    this.emitSparks(p.x, b.y, COLORS.cyan, 8);
    S.sfxPaddle();
    this.addText(p.x, p.y - 18, p.slapT > 0 ? "SLAP!" : "", COLORS.orange, 13, 0.5);
    if (p.slapT > 0) this.shake(3);
  }

  collideBricks(b, dt) {
    for (const br of this.bricks) {
      if (!br.alive) continue;
      const bw = br.w, bh = br.h;
      const cx = clamp(b.x, br.x, br.x + bw);
      const cy = clamp(b.y, br.y, br.y + bh);
      const dx = b.x - cx, dy = b.y - cy;
      const d2 = dx * dx + dy * dy;
      if (d2 >= b.r * b.r) continue;
      const face = cy === br.y ? "top" : cy === br.y + bh ? "bottom" : cx === br.x || cx === br.x + bw ? "side" : "inside";

      // hole: burst through, no bounce
      if (br.type === 2) {
        this.breakBrick(br, b);
        // boost upward if we came from below
        const spd = Math.hypot(b.vx, b.vy);
        if (b.vy < 0) b.vy -= 60;
        else b.vy = -spd * 0.6;
        const sp2 = Math.hypot(b.vx, b.vy);
        b.vx = (b.vx / sp2) * (spd + 50);
        b.vy = (b.vy / sp2) * (spd + 50);
        this.addText(b.x, b.y - 10, "THROUGH!", COLORS.gold, 11, 0.6);
        continue;
      }

      const dist = Math.sqrt(d2) || 0.001;
      const nx = dx / dist, ny = dy / dist;
      const dot = b.vx * nx + b.vy * ny;
      if (dot >= 0) continue; // moving away

      // decide break vs bounce
      const speed = Math.hypot(b.vx, b.vy);
      const tang = Math.abs(b.vx * -ny + b.vy * nx); // tangential speed
      let breaks = false;

      if (br.type === 0) breaks = true;
      else if (br.type === 1) breaks = true;
      else if (br.type === 3) breaks = tang >= 250;                       // angle
      else if (br.type === 4) breaks = speed >= 430;                      // speed
      else if (br.type === 5) breaks = true;                              // glass
      else if (br.type === 6) breaks = tang >= 250 && speed >= 430;       // gem: fast AND glancing

      if (br.type === 5) this.mapRevealT = 2.5; // glass reveals map on contact even if bounce

      if (breaks) {
        // roof bounce scoring: hitting the BOTTOM face up on the roof
        if (face === "bottom" && this.roofZone) {
          this.addPoints(ROOF_PTS);
          this.emitSparks(b.x, cy, COLORS.gold, 5);
        }
        this.breakBrick(br, b);
        // reflect anyway
        b.vx -= 2 * dot * nx;
        b.vy -= 2 * dot * ny;
      } else {
        // bounce off without breaking
        b.vx -= 2 * dot * nx;
        b.vy -= 2 * dot * ny;
        S.sfxWall();
        this.emitSparks(b.x, b.y, "#8892b0", 4);
        if (br.type === 3) this.addText(b.x, b.y - 8, "TOO STRAIGHT", "#8892b0", 9, 0.5);
        if (br.type === 4) this.addText(b.x, b.y - 8, "TOO SLOW", "#8892b0", 9, 0.5);
        if (br.type === 6) this.addText(b.x, b.y - 8, "NEED ANGLE+SPEED", "#8892b0", 9, 0.5);
      }

      // push out of the brick
      const pushX = nx * (b.r - dist + 0.6);
      const pushY = ny * (b.r - dist + 0.6);
      b.x += Math.abs(pushX) < 0.01 ? 0 : pushX;
      b.y += Math.abs(pushY) < 0.01 ? 0 : pushY;

      // roof drift: on brick-top bounces, nudge sideways + tiny downward bias
      // so the ball eventually finds a way down (it shouldn't live up there forever)
      if (face === "top" && this.roofZone) {
        const nudge = (Math.random() - 0.35) * 40;
        b.vx += nudge;
        b.vy += 14;
        b.roofBounces++;
        if (b.roofBounces % 8 === 0) {
          this.addPoints(ROOF_PTS);
          this.addText(b.x, b.y - 10, "BOUNCE!", COLORS.gold, 10, 0.5);
        }
      }
    }
  }

  // ---------------------------------------------------------------- draw
  draw() {
    const c = this.ctx;
    c.setTransform(this.dpr, 0, 0, this.dpr, 0, 0);
    c.clearRect(0, 0, this.W, this.H);

    // background
    this.drawBg(c);

    // world shake
    c.save();
    if (this.shakeAmt > 0) {
      c.translate(rnd(-this.shakeAmt, this.shakeAmt) * 0.5, rnd(-this.shakeAmt, this.shakeAmt) * 0.5);
    }

    this.drawGrid(c);
    this.drawRoofGlow(c);
    this.drawBricks(c);
    this.drawDrops(c);
    this.drawBullets(c);
    this.drawPaddle(c);
    this.drawBalls(c);
    this.drawParticles(c);
    this.drawTexts(c);
    this.drawRings(c);
    c.restore();

    if (this.roofZone) this.drawRoofBorder(c);
    this.drawFlash(c);
    this.drawVignette(c);
    this.drawMessages(c);
  }

  drawBg(c) {
    const hue = this.roofZone ? 200 + Math.sin(this.t * 0.7) * 30 : 228;
    const g = c.createLinearGradient(0, 0, 0, this.H);
    g.addColorStop(0, `hsl(${hue}, 40%, 7%)`);
    g.addColorStop(1, `hsl(${hue + 20}, 45%, 11%)`);
    c.fillStyle = g;
    c.fillRect(0, 0, this.W, this.H);
    // soft center glow
    const rg = c.createRadialGradient(this.W / 2, this.H / 2, 40, this.W / 2, this.H / 2, this.H * 0.7);
    rg.addColorStop(0, `hsla(${hue + 40}, 70%, 60%, ${this.roofZone ? 0.1 : 0.05})`);
    rg.addColorStop(1, "rgba(0,0,0,0)");
    c.fillStyle = rg;
    c.fillRect(0, 0, this.W, this.H);
  }

  drawGrid(c) {
    c.strokeStyle = "rgba(255,255,255,0.03)";
    c.lineWidth = 1;
    for (let x = 0; x <= this.W; x += 24) {
      c.beginPath(); c.moveTo(x, 0); c.lineTo(x, this.H); c.stroke();
    }
    for (let y = 0; y <= this.H; y += 24) {
      c.beginPath(); c.moveTo(0, y); c.lineTo(this.W, y); c.stroke();
    }
  }

  drawRoofGlow(c) {
    if (!this.roofZone) return;
    const g = c.createLinearGradient(0, 0, 0, this.brickTop);
    g.addColorStop(0, "rgba(255,209,102,0.13)");
    g.addColorStop(1, "rgba(255,209,102,0)");
    c.fillStyle = g;
    c.fillRect(0, 0, this.W, this.brickTop);
  }

  drawRoofBorder(c) {
    c.strokeStyle = `rgba(255,209,102,${0.5 + Math.sin(this.t * 4) * 0.3})`;
    c.lineWidth = 2;
    c.strokeRect(1, 1, this.W - 2, this.brickTop - 2);
  }

  drawBricks(c) {
    for (const br of this.bricks) {
      if (!br.alive) continue;
      const x = br.x, y = br.y, w = br.w, h = br.h;
      const underRoof = y + h < this.brickTop - 2;
      const pulse = underRoof ? 0.08 + Math.sin(this.t * 6 + br.pulse) * 0.05 : 0;

      if (br.type === 2) { // hole — dark window with dashed rim
        c.save();
        c.globalAlpha = 0.35 + pulse * 2;
        c.fillStyle = "rgba(0,0,0,0.5)";
        c.fillRect(x, y, w, h);
        c.strokeStyle = "#ffd166";
        c.setLineDash([3, 4]);
        c.lineWidth = 1.5;
        c.strokeRect(x + 1, y + 1, w - 2, h - 2);
        c.setLineDash([]);
        c.restore();
        continue;
      }

      c.save();
      let fill = br.color;
      const alpha = 0.94 + pulse;
      if (br.type === 5) c.globalAlpha = 0.55; // glass translucent
      const grad = c.createLinearGradient(x, y, x, y + h);
      grad.addColorStop(0, this.lighten(fill, 0.35));
      grad.addColorStop(1, fill);
      c.fillStyle = grad;
      c.beginPath();
      c.roundRect(x, y, w, h, 4);
      c.fill();
      if (this.roofZone && underRoof) {
        c.fillStyle = "rgba(255,255,255,0.12)";
        c.fillRect(x, y, w, 2);
      }
      // icon per special type
      const cx = x + w / 2, cy = y + h / 2;
      c.strokeStyle = "rgba(255,255,255,0.85)";
      c.lineWidth = 1.6;
      if (br.type === 3) { // angle chevron
        c.beginPath();
        c.moveTo(cx - 5, cy - 4); c.lineTo(cx + 4, cy); c.lineTo(cx - 5, cy + 4);
        c.stroke();
      } else if (br.type === 4) { // speed strokes
        c.beginPath();
        c.moveTo(cx - 6, cy - 3); c.lineTo(cx + 5, cy); c.lineTo(cx - 6, cy + 3);
        c.stroke();
        c.beginPath(); c.moveTo(cx - 9, cy); c.lineTo(cx - 6, cy); c.stroke();
        c.beginPath(); c.moveTo(cx + 5, cy); c.lineTo(cx + 8, cy); c.stroke();
      } else if (br.type === 6) { // gem
        c.beginPath();
        c.moveTo(cx, cy - 5); c.lineTo(cx + 4, cy - 2); c.lineTo(cx + 4, cy + 2);
        c.lineTo(cx, cy + 5); c.lineTo(cx - 4, cy + 2); c.lineTo(cx - 4, cy - 2);
        c.closePath();
        c.fillStyle = "rgba(255,255,255,0.9)";
        c.fill();
        c.stroke();
      } else if (br.type === 1) { // gold sparkle
        c.fillStyle = "rgba(255,255,255,0.9)";
        c.beginPath(); c.arc(cx, cy, 2, 0, 7); c.fill();
      }
      if (br.type === 5) { // glass shine line
        c.strokeStyle = "rgba(255,255,255,0.5)";
        c.beginPath(); c.moveTo(x + 3, y + 3); c.lineTo(x + w - 4, y + h - 4); c.stroke();
      }
      c.restore();
    }

    // mini-map reveal (glass)
    if (this.mapRevealT > 0) {
      const a = Math.min(1, this.mapRevealT);
      c.save();
      c.globalAlpha = a * 0.8;
      const mw = 92, mh = this.brickCols * 9 + 4, mx = this.W - mw - 10, my = this.brickTop + 10;
      c.fillStyle = "rgba(5,8,20,0.75)";
      c.beginPath(); c.roundRect(mx - 3, my - 3, mw + 6, mh + 6, 6); c.fill();
      for (const br of this.bricks) {
        if (!br.alive) continue;
        c.fillStyle = br.type === 2 ? "#ffd166" : br.color;
        c.fillRect(mx + br.col * 11 + 1, my + br.row * 9 + 1, 9, 7);
      }
      c.strokeStyle = "rgba(255,255,255,0.35)";
      c.lineWidth = 1;
      c.beginPath(); c.roundRect(mx - 3, my - 3, mw + 6, mh + 6, 6); c.stroke();
      c.restore();
    }
  }

  lighten(hex, amt) {
    const n = parseInt(hex.slice(1), 16);
    let r = (n >> 16) & 255, g = (n >> 8) & 255, b = n & 255;
    r = Math.round(r + (255 - r) * amt);
    g = Math.round(g + (255 - g) * amt);
    b = Math.round(b + (255 - b) * amt);
    return `rgb(${r},${g},${b})`;
  }

  drawDrops(c) {
    for (const d of this.drops) {
      const colors = { WIDE: COLORS.cyan, SLOW: COLORS.violet, LASER: COLORS.orange, MULTI: COLORS.pink, BOMB: COLORS.red };
      const col = colors[d.kind] || COLORS.gold;
      c.save();
      c.translate(d.x, d.y);
      c.rotate(d.t * 4);
      c.fillStyle = col;
      c.beginPath();
      c.roundRect(-8, -8, 16, 16, 4);
      c.fill();
      c.fillStyle = "rgba(255,255,255,0.9)";
      c.font = "700 10px system-ui, sans-serif";
      c.textAlign = "center"; c.textBaseline = "middle";
      const label = { WIDE: "W", SLOW: "S", LASER: "L", MULTI: "M", BOMB: "B" }[d.kind] || "?";
      c.fillText(label, 0, 0.5);
      c.restore();
    }
  }

  drawBullets(c) {
    c.fillStyle = "#ffd166";
    for (const bl of this.bullets) {
      c.save();
      c.translate(bl.x, bl.y);
      c.fillRect(-1.5, -8, 3, 16);
      c.fillStyle = "rgba(255,209,102,0.4)";
      c.fillRect(-3, -12, 6, 24);
      c.restore();
    }
  }

  drawPaddle(c) {
    const p = this.paddle;
    const wide = p.wideT > 0;
    const laser = p.laserT > 0;
    const gradient = c.createLinearGradient(p.x - p.w / 2, 0, p.x + p.w / 2, 0);
    if (laser) {
      gradient.addColorStop(0, "#ffb86b"); gradient.addColorStop(0.5, "#ff9f43"); gradient.addColorStop(1, "#ff6b6b");
    } else if (wide) {
      gradient.addColorStop(0, "#4cc9f0"); gradient.addColorStop(0.5, "#ffffff"); gradient.addColorStop(1, "#4cc9f0");
    } else {
      gradient.addColorStop(0, "#4cc9f0"); gradient.addColorStop(0.5, "#72e0ff"); gradient.addColorStop(1, "#3aa8e0");
    }
    c.save();
    c.shadowColor = laser ? "#ff9f43" : "#4cc9f0";
    c.shadowBlur = 14 + this.powerLevel * 3;
    c.fillStyle = gradient;
    c.beginPath();
    c.roundRect(p.x - p.w / 2, p.y, p.w, p.h, p.h / 2);
    c.fill();
    c.restore();
    // core stripe
    c.fillStyle = "rgba(255,255,255,0.55)";
    c.beginPath();
    c.roundRect(p.x - p.w / 2 + 4, p.y + 3, p.w - 8, 2, 2);
    c.fill();
    if (laser) {
      c.fillStyle = "#ffd166";
      c.fillRect(p.x - p.w / 2 + 6, p.y - 6, 2, 6);
      c.fillRect(p.x + p.w / 2 - 8, p.y - 6, 2, 6);
      c.fillStyle = "#ff9f43";
      c.fillRect(p.x - p.w / 2 + 6, p.y + p.h, 2, 5);
      c.fillRect(p.x + p.w / 2 - 8, p.y + p.h, 2, 5);
    }
    if (p.slapT > 0) {
      // flame trail
      for (let i = 0; i < 4; i++) {
        const a = p.slapT / 0.28;
        c.fillStyle = `rgba(255,159,67,${0.5 * a * (1 - i / 4)})`;
        c.beginPath();
        c.arc(p.x + rnd(-p.w / 2, p.w / 2), p.y + p.h + i * 7 + 4, 6 - i, 0, 7);
        c.fill();
      }
    }
  }

  drawBalls(c) {
    for (const b of this.balls) {
      if (!b.alive) continue;
      // trail
      for (let i = 0; i < b.hist.length; i++) {
        const h = b.hist[i];
        const a = (i / b.hist.length) * 0.28;
        c.fillStyle = `hsla(${b.hue}, 90%, 65%, ${a})`;
        c.beginPath();
        c.arc(h.x, h.y, b.r * (i / b.hist.length) * 0.85, 0, 7);
        c.fill();
      }
      // glow
      const glowCol = this.roofZone ? "255,209,102" : `${90 + b.hue % 100}, 210, 240`;
      c.save();
      c.shadowColor = `rgb(${glowCol})`;
      c.shadowBlur = 16 + this.powerLevel * 4;
      const grad = c.createRadialGradient(b.x - 2, b.y - 2, 1, b.x, b.y, b.r);
      grad.addColorStop(0, "#ffffff");
      grad.addColorStop(0.6, `hsl(${b.hue}, 85%, 70%)`);
      grad.addColorStop(1, `hsl(${b.hue}, 80%, 50%)`);
      c.fillStyle = grad;
      c.beginPath();
      c.arc(b.x, b.y, b.r, 0, 7);
      c.fill();
      c.restore();
      if (b.resting) {
        c.fillStyle = "rgba(255,255,255,0.35)";
        c.font = "11px system-ui, sans-serif";
        c.textAlign = "center";
        c.fillText("TAP to launch", b.x, b.y - 18);
      }
    }
  }

  drawParticles(c) {
    for (const p of this.particles) {
      const a = clamp(p.life / p.maxLife, 0, 1);
      c.globalAlpha = a;
      c.fillStyle = p.color;
      c.beginPath();
      c.arc(p.x, p.y, p.size * (0.5 + a * 0.5), 0, 7);
      c.fill();
    }
    c.globalAlpha = 1;
  }

  drawTexts(c) {
    c.textAlign = "center";
    for (const t of this.texts) {
      const a = clamp(t.life / t.maxLife, 0, 1);
      c.globalAlpha = a;
      c.font = `800 ${t.size}px system-ui, sans-serif`;
      c.strokeStyle = "rgba(0,0,0,0.6)";
      c.lineWidth = 3;
      c.strokeText(t.str, t.x, t.y);
      c.fillStyle = t.color;
      c.fillText(t.str, t.x, t.y);
    }
    c.globalAlpha = 1;
  }

  drawRings(c) {
    for (const r of this.rings) {
      const a = r.life / r.maxLife;
      c.strokeStyle = r.color;
      c.globalAlpha = a;
      c.lineWidth = 2.5 * a + 0.5;
      c.beginPath();
      c.arc(r.x, r.y, r.r, 0, 7);
      c.stroke();
    }
    c.globalAlpha = 1;
  }

  drawFlash(c) {
    if (this.flashA <= 0) return;
    c.globalAlpha = this.flashA;
    c.fillStyle = this.flashColor;
    c.fillRect(0, 0, this.W, this.H);
    c.globalAlpha = 1;
  }

  drawVignette(c) {
    const g = c.createRadialGradient(this.W / 2, this.H / 2, this.H * 0.35, this.W / 2, this.H / 2, this.H * 0.85);
    g.addColorStop(0, "rgba(0,0,0,0)");
    g.addColorStop(1, "rgba(0,0,0,0.45)");
    c.fillStyle = g;
    c.fillRect(0, 0, this.W, this.H);
  }

  drawMessages(c) {
    c.textAlign = "center";
    if (this.state === "menu") {
      const t = this.t;
      c.font = "900 34px system-ui, sans-serif";
      c.fillStyle = "#fff";
      c.shadowColor = "rgba(120,200,255,0.8)";
      c.shadowBlur = 24;
      c.fillText("ABOVE", this.W / 2, this.H * 0.34);
      c.fillText("THE BRICKS", this.W / 2, this.H * 0.34 + 40);
      c.font = "600 13px system-ui, sans-serif";
      c.fillStyle = "#8b93b8";
      c.shadowBlur = 0;
      c.fillText("Break through the wall. Own the roof.", this.W / 2, this.H * 0.34 + 70);
      c.font = "800 15px system-ui, sans-serif";
      c.fillStyle = `rgba(255,209,102,${0.75 + Math.sin(t * 3) * 0.25})`;
      c.fillText("TAP TO PLAY", this.W / 2, this.H * 0.6);
      c.font = "500 11px system-ui, sans-serif";
      c.fillStyle = "#5b6280";
      c.fillText("drag to move · swipe up to slap · mouse + keyboard work too", this.W / 2, this.H - 30);
      return;
    }
    if (this.state === "gameover") {
      c.fillStyle = "rgba(5,8,20,0.6)";
      c.fillRect(0, 0, this.W, this.H);
      c.font = "900 36px system-ui, sans-serif";
      c.fillStyle = "#ff4d5a";
      c.shadowColor = "#ff4d5a";
      c.shadowBlur = 20;
      c.fillText("GAME OVER", this.W / 2, this.H * 0.38);
      c.shadowBlur = 0;
      c.font = "800 20px system-ui, sans-serif";
      c.fillStyle = COLORS.gold;
      c.fillText(`${this.score.toLocaleString()}`, this.W / 2, this.H * 0.46);
      c.font = "500 12px system-ui, sans-serif";
      c.fillStyle = "#8b93b8";
      c.fillText(`best ${this.best.toLocaleString()}`, this.W / 2, this.H * 0.46 + 22);
      c.font = "800 15px system-ui, sans-serif";
      c.fillStyle = COLORS.cyan;
      c.fillText("TAP TO RETRY", this.W / 2, this.H * 0.62);
      return;
    }
    if (this.state === "levelclear") {
      return; // text floats already
    }
    if (this.state === "serve" && this.t % 1 < 0.5 && this.hooks.servedCount === undefined) {
      // handled by ball hint
    }
  }
}
