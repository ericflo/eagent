/* ============================================================
   Constants
   ============================================================ */
const STAGE_W = 540, STAGE_H = 960;
const BRICK_X0 = 20, BRICK_TOP = 150, BRICK_W = 52, BRICK_H = 20, BRICK_GAP = 4;
const COLS = 9, ROWS = 10, ROW_H = BRICK_H + BRICK_GAP;
const PADDLE_W = 96, PADDLE_WIDE = 140, PADDLE_H = 16;
const BAND_TOP = 520, BAND_BOTTOM = 890;
const BALL_R = 10, BALL_BIG_R = 16;
const BALL_SPEED = 330, MAX_SPEED = 980, DEATH_LINE = 950;
const STEP = 1 / 120, MAX_FRAME_MS = 50;
const TAU = Math.PI * 2, DEG = Math.PI / 180;
const MAX_BALLS = 8;
const SAVE_BEST = 'skybreak.best', SAVE_SET = 'skybreak.settings';
const FAKE_DOM = typeof document === 'undefined';
const isTouch = !FAKE_DOM && (('ontouchstart' in window) || navigator.maxTouchPoints > 0);
const reducedMotion = !FAKE_DOM && matchMedia('(prefers-reduced-motion: reduce)').matches;
const IS_MOBILE = isTouch && Math.min(window.innerWidth, window.innerHeight) < 700;
const PARTICLE_CAP = IS_MOBILE ? 360 : 600;

/* ============================================================
   Settings + persistence
   ============================================================ */
const Settings = { sound: true, music: true, control: 'follow' };
function loadSettings() {
  try {
    const raw = localStorage.getItem(SAVE_SET);
    if (raw) Object.assign(Settings, JSON.parse(raw));
  } catch (e) { /* storage unavailable: defaults stay */ }
}
function saveSettings() {
  try { localStorage.setItem(SAVE_SET, JSON.stringify(Settings)); } catch (e) {}
}
function saveBest() {
  try { localStorage.setItem(SAVE_BEST, String(G.best)); } catch (e) {}
}

/* ============================================================
   Game state
   ============================================================ */
const G = {
  phase: 'menu',                 // menu | playing | paused | gameover
  level: 1, lives: 3,
  score: 0, showScore: 0, best: 0,
  airStreak: 0, mult: 1, maxMult: 1, sky: false,
  skyEntered: false, skyLeaveT: 0,
  bricks: [], balls: [], drops: [], lasers: [], popups: [], rings: [],
  paddle: null,
  shake: 0, flash: 0, flashColor: [255, 255, 255], hitstop: 0, freezeT: 0,
  slowT: 0, catchT: 0, laserT: 0, wideT: 0,
  speedMul: 1, debugSpeed: 1, laserCooldown: 0, catchArmed: false,
  time: 0, lastSky: 0,
  bricksBroken: 0, maxMult: 1,
  milestone: 10000, tiles: 0,
  seenTypes: {}, newBest: false,
  chainQueue: [], levelClearT: 0, bannerT: 0,
  fps: 60, fpsAcc: 0, fpsN: 0,
  mutedFlag: false, musicFlag: true,
  draining: false
};

/* ============================================================
   Utils
   ============================================================ */
function clamp(v, a, b) { return v < a ? a : v > b ? b : v; }
function lerp(a, b, t) { return a + (b - a) * t; }
function rand(a, b) { return a + Math.random() * (b - a); }
function randInt(a, b) { return Math.floor(rand(a, b + 1)); }
function pick(arr) { return arr[Math.floor(Math.random() * arr.length)]; }
function dist2(ax, ay, bx, by) { const dx = bx - ax, dy = by - ay; return dx * dx + dy * dy; }
function dist(ax, ay, bx, by) { return Math.sqrt(dist2(ax, ay, bx, by)); }
function easeOutCubic(t) { return 1 - Math.pow(1 - t, 3); }
function formatScore(n) { return Math.floor(n).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ','); }
let _idSeq = 1;

/* Row base score: top row ~100 ... bottom ~20 */
function rowScore(row) { return Math.max(20, Math.round(100 - row * (80 / 9))); }
/* Row hue: rainbow, top rows brighter */
function rowHue(row) { return (196 + row * 14) % 360; }

/* ============================================================
   Canvas / view (letterboxed stage inside full-canvas bg)
   ============================================================ */
const VIEW = { w: 0, h: 0, dpr: 1, scale: 1, ox: 0, oy: 0 };
let canvas, ctx;
function resize() {
  VIEW.w = window.innerWidth; VIEW.h = window.innerHeight;
  VIEW.dpr = Math.min(2, (window.devicePixelRatio || 1));
  canvas.width = Math.round(VIEW.w * VIEW.dpr);
  canvas.height = Math.round(VIEW.h * VIEW.dpr);
  canvas.style.width = VIEW.w + 'px'; canvas.style.height = VIEW.h + 'px';
  const s = Math.min(VIEW.w / STAGE_W, VIEW.h / STAGE_H);
  VIEW.scale = s;
  VIEW.ox = (VIEW.w - STAGE_W * s) / 2;
  VIEW.oy = (VIEW.h - STAGE_H * s) / 2;
  layoutOverlay();
}
/* Map client (CSS px) coords to stage coords */
function toStage(clientX, clientY) {
  return { x: (clientX - VIEW.ox) / VIEW.scale, y: (clientY - VIEW.oy) / VIEW.scale };
}
function layoutOverlay() {
  const wrap = document.getElementById('stage-wrap');
  if (!wrap) return;
  wrap.style.left = VIEW.ox + 'px'; wrap.style.top = VIEW.oy + 'px';
  wrap.style.width = STAGE_W * VIEW.scale + 'px'; wrap.style.height = STAGE_H * VIEW.scale + 'px';
  wrap.style.display = 'block';
}

/* ============================================================
   Background: starfield + nebula (screen space)
   ============================================================ */
const stars = [];
let nebulas = [];
function initBackground() {
  stars.length = 0;
  for (let i = 0; i < 130; i++) stars.push({ x: Math.random(), y: Math.random(), size: rand(0.6, 2.2), tw: rand(0, TAU), sp: rand(0.5, 2.2), l: randInt(0, 2) });
  nebulas.length = 0;
  for (let i = 0; i < (isTouch ? 5 : 8); i++) nebulas.push({ x: Math.random(), y: Math.random(), r: rand(180, 420), hue: pick([222, 268, 195, 305, 190]) });
}

/* ============================================================
   Cached glow sprites (avoid per-frame shadowBlur)
   ============================================================ */
const GLOW_COLORS = ['#ffffff', '#46f2ff', '#ffc94d', '#ff5ad4', '#ff9f43', '#57ff8c', '#4d79ff', '#ff4d5e', '#c86bff', '#ffb347', '#aef2ff'];
const GLOWS = {};
function makeGlowSprites() {
  for (let i = 0; i < GLOW_COLORS.length; i++) {
    const c = document.createElement('canvas');
    c.width = c.height = 96;
    const g = c.getContext('2d');
    const gr = g.createRadialGradient(48, 48, 0, 48, 48, 48);
    gr.addColorStop(0, GLOW_COLORS[i]);
    gr.addColorStop(0.35, GLOW_COLORS[i] + '99');
    gr.addColorStop(1, GLOW_COLORS[i] + '00');
    g.fillStyle = gr;
    g.fillRect(0, 0, 96, 96);
    GLOWS[i] = c;
  }
}
function glowIdx(name) {
  switch (name) {
    case 'white': return 0;
    case 'cyan': return 1;
    case 'gold': return 2;
    case 'pink': return 3;
    case 'orange': return 4;
    case 'green': return 5;
    case 'blue': return 6;
    case 'red': return 7;
    case 'purple': return 8;
    case 'amber': return 9;
    default: return 1;
  }
}
function drawGlow(x, y, radius, name, alpha) {
  const img = GLOWS[glowIdx(name)];
  if (!img || radius <= 0 || alpha <= 0) return;
  ctx.globalAlpha = alpha;
  ctx.drawImage(img, x - radius, y - radius, radius * 2, radius * 2);
  ctx.globalAlpha = 1;
}

/* ============================================================
   Vignette cache (hue shifts with multiplier tier)
   ============================================================ */
const VIGNETTES = {};
function vignette(tier) {
  if (VIGNETTES[tier]) return VIGNETTES[tier];
  const c = document.createElement('canvas');
  c.width = c.height = 128;
  const g = c.getContext('2d');
  const hues = ['#071226', '#0b1f33', '#0d2a35', '#14352e', '#241f33'];
  const col = hues[tier] || hues[4];
  const gr = g.createRadialGradient(64, 64, 30, 64, 64, 64);
  gr.addColorStop(0, 'rgba(0,0,0,0)');
  gr.addColorStop(1, col);
  g.fillStyle = gr;
  g.fillRect(0, 0, 128, 128);
  VIGNETTES[tier] = c;
  return c;
}

/* ============================================================
   Brick types
   ============================================================ */
const BRICK_TYPES = {
  b: { glyph: 'B', name: 'BASIC', value: 1,   color: '#46f2ff', dark: '#0e5f6e', hint: null },
  p: { glyph: 'P', name: 'PRISM', value: 3,   color: '#c86bff', dark: '#38115e', hint: 'Prism bricks only break on GLANCING hits!' },
  f: { glyph: 'F', name: 'FORGE', value: 3,   color: '#ffb347', dark: '#5e2c0e', hint: 'Forge bricks need SPEED — get a running start!' },
  s: { glyph: 'S', name: 'SENTINEL', value: 0, color: '#ff5ad4', dark: '#4a0f33', hint: 'Sentinels fall only to fire, lasers, or bombs.' },
  a: { glyph: 'A', name: 'ANCHOR', value: 2,  color: '#4d6fff', dark: '#151e57', hint: 'Anchor bricks pull the ball toward them.' },
  t: { glyph: 'T', name: 'BOUNCE', value: 5,  color: '#57ff8c', dark: '#0e5e2e', hint: 'Bounce bricks BOOST your ball speed!' },
  m: { glyph: 'M', name: 'DRIFTER', value: 4, color: '#ff9f43', dark: '#5e2c0e', hint: 'Drifter bricks patrol their lane.' },
  x: { glyph: 'X', name: 'BOMB', value: 0,    color: '#ff4d5e', dark: '#5e0e18', hint: 'Bombs explode and chain-react nearby bombs!' },
  i: { glyph: 'I', name: 'IRON', value: 0,    color: '#b9c6e2', dark: '#3a4359', hint: 'Iron needs fire, lasers, or bombs to fall.' },
  g: { glyph: 'G', name: 'GEM', value: 10,    color: '#aef2ff', dark: '#14505e', hint: null }
};
const TYPE_KEYS = ['b', 'p', 'f', 's', 'a', 't', 'm', 'x', 'i', 'g'];

/* ============================================================
   Bricks
   ============================================================ */
function makeBrick(type, col, row) {
  const b = {
    id: _idSeq++, type: type,
    col: col, row: row,
    x: BRICK_X0 + col * (BRICK_W + BRICK_GAP),
    y: BRICK_TOP + row * ROW_H,
    w: BRICK_W, h: BRICK_H,
    baseX: 0, baseY: 0,
    flash: 0, shake: 0, wob: rand(0, TAU),
    movDir: Math.random() < 0.5 ? -1 : 1,
    sparkle: []
  };
  b.baseX = b.x; b.baseY = b.y;
  if (type === 'm') b.x += (col - (COLS - 1) / 2) * 3;
  for (let i = 0; i < 3; i++) b.sparkle.push({ x: rand(6, 46), y: rand(4, 16), ph: rand(0, TAU) });
  return b;
}

function brickDef(b) { return BRICK_TYPES[b.type]; }

/* brick damage: returns true if it broke, sets pending removal flag */
function brickHit(b, ball, side) {
  const d = brickDef(b);
  b.flash = 0.12;
  if (b.type === 'i') {         // iron: immune to normal hits
    Audio.clang(); addSparks(b.x + b.w / 2, b.y + b.h / 2, 6, '#cdd6ea');
    return false;
  }
  if (b.type === 's') {         // sentinel: immune to normal hits
    Audio.clang();
    addSparks(b.x + b.w / 2, b.y + b.h / 2, 6, '#ff5ad4');
    return false;
  }
  if (b.type === 'p') {         // prism: glancing only
    const nx = side[0], ny = side[1];
    const vx = -ball.vx, vy = -ball.vy;
    const vm = Math.hypot(vx, vy) || 1;
    const cosA = (vx * nx + vy * ny) / vm;
    if (Math.abs(cosA) >= Math.cos(30 * DEG)) {  // too head-on
      Audio.prismFizz();
      b.shake = 0.5;
      addSparks(b.x + b.w / 2, b.y + b.h / 2, 4, '#c86bff');
      return false;
    }
  }
  if (b.type === 'f' && ball.speed < 560) {      // forge: needs speed
    Audio.forgeDing();
    b.shake = 0.4;
    addSparks(b.x + b.w / 2, b.y + b.h / 2, 5, '#ffb347');
    return false;
  }
  return true;
}

/* full destroy (from fire/laser/bomb/explosion) — marks dead, compacted at step end */
function destroyBrick(b, cause) {
  if (b.dead) return;
  b.dead = true;
  G.bricksBroken++;
  const d = brickDef(b);
  const cx = b.x + b.w / 2, cy = b.y + b.h / 2;
  addShatter(cx, cy, brickColor(b, 1));
  const val = d.value * rowScore(b.row) * G.mult;
  if (val > 0) {
    addScore(val, cx, cy);
    scoreAdd(val);
  }
  if (b.type === 'g') spawnDrop(cx, cy, Math.random() < 0.4);
  else if (b.type === 'x') { b._counted = true; queueBomb(b, cause === 'chain' ? 0.12 : 0); }
  else if (Math.random() < 0.12) spawnDrop(cx, cy, false);
  if (cause === 'laser') addSparks(cx, cy, 4, '#7df3ff');
  Audio.breakSound(b.row, d.value === 0);
  hitStop(cause === 'bomb' ? 40 : 55);
  G.shake = Math.min(26, G.shake + (b.type === 'x' ? 16 : 6));
  if (G.sky) airGain();
}
function compactBricks() {
  let w = 0;
  for (let i = 0; i < G.bricks.length; i++) if (!G.bricks[i].dead) G.bricks[w++] = G.bricks[i];
  G.bricks.length = w;
}
function liveBrickCount() {
  let n = 0;
  for (let i = 0; i < G.bricks.length; i++) if (!G.bricks[i].dead) n++;
  return n;
}

/* convert passed pixel (or row color) to readable color */
function brickColor(b, lit) {
  const d = brickDef(b);
  if (b.type === 'b') {
    const h = rowHue(b.row);
    return 'hsl(' + h + ',' + (lit ? 90 : 70) + '%,' + (lit ? 62 : 48) + '%)';
  }
  return d.color;
}

/* ============================================================
   Particles (pooled)
   ============================================================ */
const MAX_PARTS = PARTICLE_CAP;
const POOL = [];
for (let i = 0; i < MAX_PARTS; i++) POOL.push({ on: false, kind: 0, x: 0, y: 0, vx: 0, vy: 0, rot: 0, vr: 0, life: 0, maxLife: 1, size: 2, color: '#fff', grav: 0, drag: 0 });
const parts = [];
function palloc() {
  for (let i = 0; i < POOL.length; i++) if (!POOL[i].on) return POOL[i];
  return null; // pool exhausted
}
function pSpawn(kind, x, y, vx, vy, life, size, color, opts) {
  const p = palloc();
  if (!p) return;
  p.on = true; p.kind = kind; p.x = x; p.y = y; p.vx = vx; p.vy = vy;
  p.life = life; p.maxLife = life; p.size = size; p.color = color;
  p.rot = rand(0, TAU); p.vr = rand(-8, 8);
  p.grav = opts && opts.grav !== undefined ? opts.grav : 0;
  p.drag = opts && opts.drag !== undefined ? opts.drag : 0;
  parts.push(p);
  return p;
}
/* shards for brick shatter */
function addShatter(x, y, color) {
  const n = IS_MOBILE ? 8 : 12;
  for (let i = 0; i < n; i++) {
    const a = rand(0, TAU), sp = rand(60, 320);
    pSpawn(0, x, y, Math.cos(a) * sp, Math.sin(a) * sp - 60, rand(0.5, 1.0), rand(3, 8), color, { grav: 700, drag: 0.6 });
  }
}
function addSparks(x, y, n, color, spread) {
  for (let i = 0; i < n; i++) {
    const a = rand(0, TAU), sp = rand(80, (spread || 260));
    pSpawn(1, x, y, Math.cos(a) * sp, Math.sin(a) * sp, rand(0.2, 0.5), rand(1.5, 3.5), color, { drag: 2 });
  }
}
function addSmoke(x, y, n, color) {
  for (let i = 0; i < n; i++) {
    const a = rand(0, TAU), sp = rand(12, 60);
    pSpawn(2, x, y, Math.cos(a) * sp, Math.sin(a) * sp - 30, rand(0.5, 1.1), rand(6, 14), color, { drag: 1.2 });
  }
}
function addConfetti(x, y, n, hue) {
  for (let i = 0; i < n; i++) {
    const a = rand(0, TAU), sp = rand(60, 380);
    pSpawn(0, x, y, Math.cos(a) * sp, Math.sin(a) * sp - 140, rand(0.8, 1.6), rand(3, 7), 'hsl(' + (hue || rand(0, 360)) + ',90%,60%)', { grav: 500, drag: 0.4 });
  }
}
function addStreak(x, y, vx, vy, color, life) {
  const p = pSpawn(3, x, y, vx, vy, life || 0.4, 1, color, {});
  if (p) { p.maxLife = p.life; }
}
function addFire(x, y) {
  pSpawn(1, x, y, rand(-40, 40), rand(40, 140), 0.35, rand(2, 5), pick(['#ffb347', '#ff7b4d', '#ffc94d']), { drag: 1 });
}
function addBoostRing(x, y, color) {
  pSpawn(4, x, y, 0, 0, 0.35, 10, color, {});
}
/* updates all particles (called from step) */
function updateParticles(dt) {
  for (let i = parts.length - 1; i >= 0; i--) {
    const p = parts[i];
    p.life -= dt;
    if (p.life <= 0 || !p.on) {
      p.on = false;
      parts.splice(i, 1);
      continue;
    }
    if (p.kind !== 3 && p.kind !== 4) {
      p.vy += p.grav * dt;
      if (p.drag) { p.vx *= Math.max(0, 1 - p.drag * dt); p.vy *= Math.max(0, 1 - p.drag * dt); }
      p.x += p.vx * dt; p.y += p.vy * dt;
    }
    p.rot += p.vr * dt;
  }
  if (parts.length > MAX_PARTS) parts.splice(0, parts.length - MAX_PARTS);
}
function particleCount() { return parts.length; }

/* ============================================================
   Floating score popups
   ============================================================ */
function addScore(v, x, y) {
  G.popups.push({ x: x, y: y, v: v, life: 1.0, max: 1.0, big: v >= 1000 });
  if (G.popups.length > 40) G.popups.shift();
}
function updatePopups(dt) {
  for (let i = G.popups.length - 1; i >= 0; i--) {
    const p = G.popups[i];
    p.life -= dt * 1.1;
    p.y -= 40 * dt;
    if (p.life <= 0) G.popups.splice(i, 1);
  }
}

/* ============================================================
   Sky mode (the core fantasy)
   ============================================================ */
function multForStreak(s) { return 1 << Math.min(4, Math.floor(s / 3)); }

function updateSky(dt) {
  const highest = topBrickY();  let anyAbove = false;
  for (let i = 0; i < G.balls.length; i++) {
    const b = G.balls[i];
    if (highest === null || (b.y - b.r) < highest) anyAbove = true;
  }
  if (anyAbove) {
    if (!G.sky) {
      G.sky = true;
      G.skyEntered = true;
      G.lastSky = G.time;
      banner('SKY MODE', 'You broke through!');
      Audio.skyEnter();
      G.flash = 0.5; G.flashColor = [70, 242, 255];
    }
    G.skyLeaveT = 0;
  } else if (G.sky) {
    G.skyLeaveT += dt;
    if (G.skyLeaveT > 1) {
      G.sky = false;
      G.skyLeaveT = 0;
    }
  }
}
function topBrickY() {
  let best = null;
  for (let i = 0; i < G.bricks.length; i++) if (best === null || G.bricks[i].y < best) best = G.bricks[i].y;
  return best;
}
/* called when a brick breaks while in sky mode */
function airGain() {
  G.airStreak++;
  const newMult = multForStreak(G.airStreak);
  if (newMult > G.mult) {
    G.mult = newMult;
    G.maxMult = Math.max(G.maxMult, newMult);
    banner('SKY x' + newMult + '!', 'The sky is falling!');
    Audio.skyTier(newMult);
    G.flash = 0.55; G.flashColor = [255, 201, 77];
    addConfetti(stageX(0.5), stageY(280), 30, 190);
    if (newMult >= 16) G.shake = Math.max(G.shake, 12);
  }
  updateSkyBadge();
}
function airDecay(dt) {
  if (G.airStreak <= 0) return;
  G.airStreak -= dt / 0.6;
  if (G.airStreak < 0) G.airStreak = 0;
  const newMult = multForStreak(Math.floor(G.airStreak));
  if (newMult < G.mult) { G.mult = newMult; updateSkyBadge(); }
}
function updateSkyBadge() {
  const badge = document.getElementById('skybadge');
  const lbl = document.getElementById('sky-mult');
  if (!badge) return;
  if (G.mult > 1) {
    badge.classList.remove('hidden');
    lbl.textContent = 'x' + G.mult;
    badge.classList.remove('pulse');
    void badge.offsetWidth;
    badge.classList.add('pulse');
  } else {
    badge.classList.add('hidden');
  }
}

/* ============================================================
   Power-up drops (falling capsules)
   ============================================================ */
const DROP_DEFS = {
  W: { name: 'WIDEN', color: '#ff9f43', glyph: 'W' },
  L: { name: 'LASER', color: '#46f2ff', glyph: 'L' },
  C: { name: 'CATCH', color: '#c86bff', glyph: 'C' },
  F: { name: 'FIRE', color: '#ff4d3f', glyph: 'F' },
  G: { name: 'GHOST', color: '#b0b7ff', glyph: 'G' },
  B: { name: 'BIG', color: '#57ff8c', glyph: 'B' },
  M: { name: 'MULTI', color: '#ffc94d', glyph: 'M' },
  S: { name: 'SLOW', color: '#4d79ff', glyph: 'S' },
  H: { name: 'LIFE', color: '#57ff8c', glyph: '+' }
};
function spawnDrop(x, y, forceSpecial) {
  if (G.drops.length > 6) return;
  let key;
  if (forceSpecial) key = pick(['F', 'G', 'B', 'M', 'W', 'L']);
  else key = pick(['W', 'W', 'L', 'C', 'F', 'G', 'B', 'M', 'S', 'S', 'S', 'H', 'H', 'H']); // weightedish
  /* adjust: make LIFE rare */
  const roll = Math.random();
  if (key === 'H' && roll > 0.18) key = pick(['W', 'L', 'C']);
  G.drops.push({
    x: x, y: y, key: key,
    vy: 120, wob: rand(0, TAU), wobSp: rand(2, 4),
    seed: rand(0, TAU)
  });
}
function catchDrop(d) {
  const def = DROP_DEFS[d.key];
  hitStop(30);
  Audio.powerArp();
  G.flash = 0.35; G.flashColor = [255, 255, 255];
  addBoostRing(G.paddle.x + G.paddle.w / 2, G.paddle.y, '#ffffff');
  switch (d.key) {
    case 'W': G.wideT = 12; break;
    case 'L': G.laserT = 8; break;
    case 'C': G.catchT = 6; G.catchArmed = true; break;
    case 'F': applyPowerBall('fire'); break;
    case 'G': applyPowerBall('ghost'); break;
    case 'B': applyPowerBall('big'); break;
    case 'M': multiBall(); break;
    case 'S': G.slowT = 5; break;
    case 'H': G.lives = Math.min(5, G.lives + 1); bandText('+1 LIFE'); break;
  }
  addPopup(def.name, d.x, d.y);
  bannerTiny(def.name + '!');
}
function addPopup(txt, x, y) {
  G.popups.push({ x: x, y: y, text: txt, life: 1.2, max: 1.2, big: true });
}
function bannerTiny(txt) {
  const el = document.getElementById('banner-sub');
  if (!el) return;
  el.textContent = txt;
}
/* power ball state on every ball */
function applyPowerBall(kind) {
  for (let i = 0; i < G.balls.length; i++) {
    G.balls[i].pb = kind;
    G.balls[i].pbT = kind === 'big' ? 10 : 8;
  }
}
function multiBall() {
  const src = [];
  for (let i = 0; i < G.balls.length; i++) if (G.balls[i].active) src.push(G.balls[i]);
  if (!src.length) return;
  if (G.balls.length >= MAX_BALLS) return;
  const count = Math.min(2, MAX_BALLS - G.balls.length);
  for (let i = 0; i < count; i++) {
    const a = src[0];
    const ang = Math.atan2(a.vy, a.vx) + (i === 0 ? 35 : -35) * DEG;
    const nb = spawnBall(a.x, a.y);
    nb.vx = Math.cos(ang) * a.speed; nb.vy = Math.sin(ang) * a.speed;
    nb.speed = a.speed;
    nb.pb = a.pb; nb.pbT = a.pbT;
    nb.catchable = false; // new balls can be caught after first paddle touch
    nb.trail.length = 0;
  }
  Audio.chime();
  G.flash = 0.3; G.flashColor = [255, 201, 77];
}

/* ============================================================
   Paddle
   ============================================================ */
function makePaddle() {
  G.paddle = {
    x: (STAGE_W - PADDLE_W) / 2, y: 860,   // y = top of paddle
    w: PADDLE_W, h: PADDLE_H,
    tx: (STAGE_W - PADDLE_W) / 2, ty: 860,
    vx: 0, vy: 0,
    movingUp: false, thrust: 0,
    catchGlow: 0
  };
}
function updatePaddle(dt) {
  const p = G.paddle;
  const halfW = p.w / 2;
  /* keyboard acceleration */
  const kx = (Input.keys.left ? -1 : 0) + (Input.keys.right ? 1 : 0);
  const ky = (Input.keys.up ? -1 : 0) + (Input.keys.down ? 1 : 0);
  if (kx || ky) {
    const sp = 1500 * dt;
    p.tx += kx * sp; p.ty += ky * sp;
    p.tx = clamp(p.tx, halfW, STAGE_W - halfW);
    p.ty = clamp(p.ty, BAND_TOP, BAND_BOTTOM - p.h);
  }
  /* pointer follow (only when no keys) */
  if (Input.pointerActive && !kx && !ky) {
    p.tx = clamp(Input.pointerX, halfW, STAGE_W - halfW);
    p.ty = clamp(Input.pointerY, BAND_TOP, BAND_BOTTOM - p.h);
  }
  /* relative drag mode */
  if (Input.relativeDelta) {
    p.tx = clamp(p.tx + Input.relativeDelta.x, halfW, STAGE_W - halfW);
    p.ty = clamp(p.ty + Input.relativeDelta.y, BAND_TOP, BAND_BOTTOM - p.h);
    Input.relativeDelta = null;
  }
  /* ease toward target */
  const ease = 1 - Math.pow(0.0015, dt);
  const px = p.x, py = p.y;
  p.x = lerp(p.x, p.tx, ease * 0.9);
  p.y = lerp(p.y, p.ty, ease * 0.8);
  p.x = clamp(p.x, halfW, STAGE_W - halfW);
  p.y = clamp(p.y, BAND_TOP, BAND_BOTTOM - p.h);
  p.vx = dt > 0 ? (p.x - px) / dt : 0;
  p.vy = dt > 0 ? (p.y - py) / dt : 0;
  p.movingUp = p.vy < -60;
  p.thrust = clamp(Math.abs(p.vy) / 1400, 0, 1);
  /* thruster particles when moving up */
  if (p.movingUp && !reducedMotion) {
    const cx = p.x + p.w / 2;
    if (Math.random() < 0.8) pSpawn(1, cx + rand(-p.w / 2, p.w / 2), p.y + p.h - 2, rand(-30, 30), rand(120, 260) + Math.abs(p.vy) * 0.4, rand(0.15, 0.35), rand(2, 4.5), pick(['#46f2ff', '#7df3ff', '#ffc94d']), { drag: 1.5 });
  }
  /* wide timer */
  const wantW = G.wideT > 0 ? PADDLE_WIDE : PADDLE_W;
  p.w = lerp(p.w, wantW, 1 - Math.pow(0.002, dt));
  if (G.wideT > 0) G.wideT -= dt;
  if (G.catchT > 0) {
    G.catchT -= dt;
    p.catchGlow = Math.min(1, p.catchGlow + dt * 4);
  } else {
    p.catchGlow = Math.max(0, p.catchGlow - dt * 4);
    G.catchArmed = false;
  }
  /* clamp paddle fully inside stage */
  p.x = clamp(p.x, p.w / 2, STAGE_W - p.w / 2);
}

/* ============================================================
   Balls
   ============================================================ */
function spawnBall(x, y) {
  const b = {
    x: x, y: y, vx: 0, vy: 0, speed: BALL_SPEED,
    r: BALL_R, active: true,
    stuck: false,
    pb: null, pbT: 0,
    trail: [], catchFlash: 0, boostFlash: 0, thump: 0,
    catchable: true
  };
  G.balls.push(b);
  return b;
}
function launchBall(b) {
  b.stuck = false;
  b.catchable = false;   // must be re-caught by a later paddle touch
  const ang = (80 * DEG) * (Math.random() < 0.5 ? 1 : -1);
  b.vx = Math.cos(ang) * BALL_SPEED;
  b.vy = -Math.sin(ang) * BALL_SPEED;
  b.speed = BALL_SPEED;
  b.active = true;
}
function resetBallCount() {
  /* safety: if no balls, spawn a fresh stuck one */
  if (!G.balls.some(function (b) { return b.active; })) {
    const b = spawnBall(G.paddle.x + G.paddle.w / 2, G.paddle.y - BALL_R - 2);
    b.stuck = true;
    b.speed = BALL_SPEED;
  }
}
/* returns true if the ball died */
function stepBall(b, dt) {
  if (!b.active) return false;
  /* follow paddle while stuck */
  if (b.stuck) {
    b.x = G.paddle.x + G.paddle.w / 2;
    b.y = G.paddle.y - b.r - 2;
    b.speed = BALL_SPEED;
    return false;
  }
  /* power ball timers */
  if (b.pb) {
    b.pbT -= dt;
    if (b.pbT <= 0) { b.pb = null; b.pbT = 0; }
  }
  /* trail */
  b.trail.push({ x: b.x, y: b.y, life: 0.28, r: b.r });
  if (b.trail.length > 10) b.trail.shift();
  /* power-up trails */
  if (b.pb === 'fire' && !reducedMotion && Math.random() < 0.6) {
    addFire(b.x, b.y);
    if (Math.random() < 0.3) addStreak(b.x, b.y, b.vx, b.vy, '#ff7b4d', 0.25);
  }
  if (b.pb === 'ghost' && !reducedMotion && Math.random() < 0.35) {
    pSpawn(1, b.x, b.y, rand(-30, 30), rand(-30, 30), 0.3, rand(2, 4), '#b0b7ff', { drag: 1 });
  }

  /* integrate */
  const s = b.speed * G.speedMul;
  b.x += b.vx / b.speed * s * dt;
  b.y += b.vy / b.speed * s * dt;
  /* anchor pull */
  for (let i = 0; i < G.bricks.length; i++) {
    const br = G.bricks[i];
    if (br.type !== 'a') continue;
    const cxp = br.x + br.w / 2, cyp = br.y + br.h / 2;
    const d = dist(b.x, b.y, cxp, cyp);
    if (d < 110 && d > 1) {
      const pull = (1 - d / 110) * 220 * dt;
      b.vx += (cxp - b.x) / d * pull;
      b.vy += (cyp - b.y) / d * pull;
      const ns = Math.hypot(b.vx, b.vy);
      b.speed = clamp(ns, BALL_SPEED * 0.6, MAX_SPEED);
      if (ns > 0) { b.vx = b.vx / ns * b.speed; b.vy = b.vy / ns * b.speed; }
    }
  }

  /* walls */
  if (b.x - b.r < 0) { b.x = b.r; b.vx = Math.abs(b.vx); Audio.wall(); }
  else if (b.x + b.r > STAGE_W) { b.x = STAGE_W - b.r; b.vx = -Math.abs(b.vx); Audio.wall(); }
  if (b.y - b.r < 0) { b.y = b.r; b.vy = Math.abs(b.vy); Audio.wall(); }

  /* paddle */
  if (b.vy > 0) collidePaddle(b);

  /* bricks (skip for ghost passthrough) */
  if (b.pb !== 'ghost') collideBricks(b);

  /* death */
  if (b.y - b.r > DEATH_LINE) {
    b.active = false;
    Audio.lostBall();
    G.shake = Math.max(G.shake, 8);
    addSparks(b.x, b.y, 8, '#7df3ff');
    if (reducedMotion) G.flash = Math.max(G.flash, 0.15);
    return true;
  }
  /* clamp speed headroom guard */
  b.speed = clamp(b.speed, 80, MAX_SPEED + 60);
  const ns = Math.hypot(b.vx, b.vy);
  if (ns > 0.001) { b.vx = b.vx / ns * b.speed; b.vy = b.vy / ns * b.speed; }
  return false;
}

/* ============================================================
   Paddle collision (circle vs rounded rect)
   ============================================================ */
function collidePaddle(b) {
  const p = G.paddle;
  const cx = p.x + p.w / 2;
  const px = clamp(b.x, p.x + 6, p.x + p.w - 6);
  const py = clamp(b.y, p.y, p.y + p.h);
  if (dist2(b.x, b.y, px, py) >= b.r * b.r) return;
  if (b.y + b.r < p.y - 4) return; // only from above-ish
  /* reflect: bounce angle depends on horizontal hit offset (max 60° from vertical) */
  const rel = (b.x - cx) / (p.w / 2);
  const off = clamp(rel, -1, 1);
  const maxAng = 60 * DEG;
  let ang = off * maxAng;
  if (b.pb === 'fire') ang *= 0.5; // fireballs fly straighter
  b.vx = Math.sin(ang) * b.speed;
  b.vy = -Math.cos(ang) * b.speed;   // always upward
  /* lift boost: paddle moving UP at hit moment adds upward acceleration + speed bonus */
  if (p.vy < 0) {
    const boost = Math.abs(p.vy) * 0.6;
    b.vy -= boost;
    b.speed = clamp(b.speed * 1.05, 0, MAX_SPEED);
    const ns = Math.hypot(b.vx, b.vy);
    if (ns > 0) { b.vx = b.vx / ns * b.speed; b.vy = b.vy / ns * b.speed; }
    b.boostFlash = 0.35;
    Audio.boost();
    addSparks(cx, p.y - 4, 8, '#46f2ff', 320);
    addBoostRing(cx, p.y - 4, '#46f2ff');
    addStreak(b.x, b.y, b.vx, b.vy, '#46f2ff', 0.3);
  } else if (p.vy > 0 && b.speed > 80) {
    /* moving down: slight dampening of the upward launch */
    b.vy = Math.min(-1, b.vy + Math.abs(p.vy) * 0.25);
    const ns = Math.hypot(b.vx, b.vy);
    if (ns > 0) { b.vx = b.vx / ns * b.speed; b.vy = b.vy / ns * b.speed; }
  }
  /* position above paddle */
  b.y = p.y - b.r - 1;
  Audio.paddle();
  addBoostRing(cx, p.y - 2, '#ffffff');
  /* catch mode */
  if (G.catchArmed && !b.catchable) {
    /* only stick on the first touch after a launch */
    b.stuck = true;
    b.catchable = true;
    b.catchFlash = 0.5;
    Audio.catchBlip();
    addBoostRing(cx, p.y - 2, '#c86bff');
  }
}

/* ============================================================
   Brick collision (circle vs AABB, closest-point resolution)
   ============================================================ */
function collideBricks(b) {
  const r = b.r;
  if (b.pb === 'ghost') return; // phaser passes through, never breaks
  for (let i = 0; i < G.bricks.length; i++) {
    const br = G.bricks[i];
    if (br.dead) continue;
    /* resolve drift animation offsets for moving bricks */
    const bx = br.x, by = br.y;
    /* closest point on rect to ball center */
    const cx = clamp(b.x, bx, bx + br.w);
    const cy = clamp(b.y, by, by + br.h);
    const dx = b.x - cx, dy = b.y - cy;
    const d2 = dx * dx + dy * dy;
    if (d2 > r * r) continue;
    if (d2 < 1e-6) {
      /* center inside brick: push out along smallest axis penetration */
      const pl = b.x - bx, prr = bx + br.w - b.x, pt = b.y - by, pb = by + br.h - b.y;
      const m = Math.min(pl, prr, pt, pb);
      if (m === pl) { b.x = bx - r; bounceX(b, -1); }
      else if (m === prr) { b.x = bx + br.w + r; bounceX(b, 1); }
      else if (m === pt) { b.y = by - r; bounceY(b, -1); }
      else { b.y = by + br.h + r; bounceY(b, 1); }
      if (b.pb === 'fire') { destroyBrick(br, 'fire'); continue; }
      if (b.pb === 'big') { destroyRadius(b.x, b.y, 26); continue; }
      resolveBrickHit(br, b, m === pt ? [0, -1] : m === pb ? [0, 1] : m === pl ? [-1, 0] : [1, 0]);
      continue;
    }
    const d = Math.sqrt(d2);
    const nx = dx / d, ny = dy / d;   // normal pointing from brick toward ball
    /* ball must be moving toward the brick (negative dot with normal) */
    const dot = b.vx * nx + b.vy * ny;
    if (dot >= 0) continue;
    /* push ball out and reflect */
    b.x = cx + nx * (r + 0.5);
    b.y = cy + ny * (r + 0.5);
    const side = [nx, ny];
    /* power-up fire: pierce through, break everything in its path */
    if (b.pb === 'fire') {
      destroyBrick(br, 'fire');
      continue;
    }
    /* power-up big: smash a radius, then bounce */
    if (b.pb === 'big') {
      destroyRadius(b.x, b.y, 26);
      bounceBall(b, nx, ny, dot);
      continue;
    }
    resolveBrickHit(br, b, side);
    bounceBall(b, nx, ny, dot);
  }
}
function destroyRadius(x, y, rad) {
  const hit = [];
  for (let i = 0; i < G.bricks.length; i++) {
    const o = G.bricks[i];
    if (o.dead) continue;
    const ox = clamp(x, o.x, o.x + o.w), oy = clamp(y, o.y, o.y + o.h);
    if (dist2(x, y, ox, oy) <= rad * rad) hit.push(o);
  }
  for (let i = 0; i < hit.length; i++) destroyBrick(hit[i], 'big');
}
function bounceX(b, s) { b.vx = Math.abs(b.vx) * s; }
function bounceY(b, s) { b.vy = Math.abs(b.vy) * s; }
/* reflect velocity along normal */
function bounceBall(b, nx, ny, dot) {
  b.vx = b.vx - 2 * dot * nx;
  b.vy = b.vy - 2 * dot * ny;
  const ns = Math.hypot(b.vx, b.vy);
  if (ns > 0.001) { b.vx = b.vx / ns * b.speed; b.vy = b.vy / ns * b.speed; }
  b.speed = clamp(b.speed, 80, MAX_SPEED + 60);
}
/* process an impact: break checks, fire pierce handled by caller */
function resolveBrickHit(br, b, side) {
  if (br.dead) return;
  if (b.pb === 'fire' || b.pb === 'big' || b.pb === 'ghost') {
    /* all handled by caller/invoker */
    return;
  }
  if (brickHit(br, b, side)) {
    destroyBrick(br, 'ball');
  }
}

/* ============================================================
   Bomb chaining
   ============================================================ */
function queueBomb(b, delay) {
  b.dead = true;
  G.chainQueue.push({ b: b, t: delay });
}
function explodeBomb(b) {
  /* the bomb itself was queued (marked dead); it's removed by compactation */
  b.dead = true;
  if (!b._counted) {
    b._counted = true;
    G.bricksBroken++;
    const val = 25 * G.mult;   // bombs pay a flat bonus
    addScore(val, b.x + b.w / 2, b.y + b.h / 2);
    scoreAdd(val);
  }
  const cx = b.x + b.w / 2, cy = b.y + b.h / 2;
  const victims = [];
  for (let i = 0; i < G.bricks.length; i++) {
    const o = G.bricks[i];
    if (o === b || o.dead) continue;
    const ox = o.x + o.w / 2, oy = o.y + o.h / 2;
    if (dist(cx, cy, ox, oy) <= 80 + o.w / 2) victims.push(o);
  }
  Audio.explosion();
  hitStop(120);
  G.shake = Math.max(G.shake, 22);
  addConfetti(cx, cy, 14, 0);
  addSmoke(cx, cy, 8, '#888');
  addSparks(cx, cy, 16, '#ff4d5e', 420);
  addBoostRing(cx, cy, '#ff4d5e');
  G.rings.push({ x: cx, y: cy, r: 10, max: 80, life: 0.5 });
  for (let i = 0; i < victims.length; i++) {
    const v = victims[i];
    if (v.type === 'x') queueBomb(v, 0.12);
    else destroyBrick(v, 'chain');
  }
}
function updateChain(dt) {
  if (!G.chainQueue.length) return;
  for (let i = G.chainQueue.length - 1; i >= 0; i--) {
    const q = G.chainQueue[i];
    q.t -= dt;
    if (q.t <= 0) {
      G.chainQueue.splice(i, 1);
      explodeBomb(q.b);
    }
  }
}

/* ============================================================
   Lasers (paddle, shoot up)
   ============================================================ */
function fireLaser() {
  const p = G.paddle;
  const cx = p.x + p.w / 2;
  for (let s = -1; s <= 1; s += 2) {
    const x = cx + s * p.w * 0.28;
    G.lasers.push({ x: x, y: p.y, w: 3, h: 18, vy: -1400, life: 0.5 });
  }
  Audio.laser();
  addSparks(cx, p.y - 2, 4, '#46f2ff');
}
function updateLasers(dt) {
  for (let i = G.lasers.length - 1; i >= 0; i--) {
    const l = G.lasers[i];
    l.y += l.vy * dt;
    l.life -= dt;
    if (l.life <= 0) { G.lasers.splice(i, 1); continue; }
    /* hit topmost brick in column */
    let best = null;
    for (let j = 0; j < G.bricks.length; j++) {
      const br = G.bricks[j];
      if (br.dead) continue;
      if (l.x >= br.x && l.x <= br.x + br.w && l.y >= br.y && l.y <= br.y + br.h) {
        if (!best || br.y < best.y) best = br;
      }
    }
    if (best) {
      if (best.type === 's' || best.type === 'i') destroyBrick(best, 'laser');
      else destroyBrick(best, 'laser');
      G.lasers.splice(i, 1);
    } else if (l.y < -30) G.lasers.splice(i, 1);
  }
}

/* ============================================================
   Drops update
   ============================================================ */
function updateDrops(dt) {
  for (let i = G.drops.length - 1; i >= 0; i--) {
    const d = G.drops[i];
    d.wob += d.wobSp * dt;
    d.y += d.vy * dt;
    d.x += Math.sin(d.wob * 2) * 20 * dt;
    /* caught by paddle */
    const p = G.paddle;
    if (d.y + 14 >= p.y && d.y - 14 <= p.y + p.h &&
        d.x >= p.x - 12 && d.x <= p.x + p.w + 12) {
      catchDrop(d);
      G.drops.splice(i, 1);
      continue;
    }
    if (d.y > STAGE_H + 20) G.drops.splice(i, 1);
  }
}

/* ============================================================
   Timers / banner / milestones
   ============================================================ */
function hitStop(ms) { G.hitstop = Math.max(G.hitstop, ms / 1000); }
function banner(title, sub) {
  const t = document.getElementById('banner-title');
  const s = document.getElementById('banner-sub');
  const wrap = document.getElementById('overlay-banner');
  if (t) t.textContent = title;
  if (s) s.textContent = sub || '';
  if (wrap) {
    wrap.classList.remove('hidden');
    wrap.classList.add('show');
    /* restart child animations */
    if (t) { t.classList.remove('replay'); void t.offsetWidth; t.classList.add('replay'); }
    if (s) { s.classList.remove('replay'); void s.offsetWidth; s.classList.add('replay'); }
  }
  G.bannerT = 1.4;
}
function hideBanner() {
  const wrap = document.getElementById('overlay-banner');
  if (wrap) wrap.classList.add('hidden');
}
function bandText(txt) { bannerTiny(txt); }
function updateTimers(dt) {
  if (G.slowT > 0) G.slowT -= dt;
  if (G.laserT > 0) G.laserT -= dt;
  if (G.wideT > 0) G.wideT -= dt;
  if (G.bannerT > 0) { G.bannerT -= dt; if (G.bannerT <= 0) hideBanner(); }
  /* milestones */
  if (G.score >= G.milestone) {
    Audio.fanfare();
    G.flash = 0.5; G.flashColor = [255, 255, 255];
    addConfetti(stageX(0.5), stageY(120), 26, 0);
    G.milestone += 10000;
    stringBanner('MILESTONE!');
  }
}
function stringBanner(txt) {
  const s = document.getElementById('banner-sub');
  if (s) s.textContent = txt;
}

/* ============================================================
   Level clear
   ============================================================ */
function checkLevelClear() {
  if (G.phase !== 'playing') return;
  if (G.balls.some(function (b) { return b.active; }) && G.bricks.length === 0) {
    G.phase = 'clear';
    G.levelClearT = 2.2;
    Audio.levelClear();
    G.flash = 0.6; G.flashColor = [255, 255, 255];
    addConfetti(stageX(0.5), stageY(200), 60, 0);
    addConfetti(stageX(0.3), stageY(260), 40, 130);
    addConfetti(stageX(0.7), stageY(260), 40, 300);
    banner('LEVEL CLEAR!', 'Lifting off…');
  }
}
function updateClear(dt) {
  if (G.phase !== 'clear') return;
  G.levelClearT -= dt;
  if (G.levelClearT <= 0) {
    G.phase = 'playing';
    G.level++;
    startLevel(G.level, true);
  }
}
function stageX(t) { return 20 + t * (STAGE_W - 40); }
function stageY(t) { return 120 + t * (STAGE_H - 240); }

/* ============================================================
   Audio (WebAudio, all synthesized)
   ============================================================ */
const Audio = {
  ctx: null, master: null, musicOn: true, soundOn: true,

  _noiseBuf: null, _started: false,
  musicTimer: 0, musicStep: 0, musicNext: 0,

  init() {
    if (FAKE_DOM) { this.ctx = null; return; }
    if (this.ctx) { if (this.ctx.state === 'suspended') this.ctx.resume(); return; }
    const AC = window.AudioContext || window.webkitAudioContext;
    if (!AC) { this.ctx = null; return; }
    try {
      this.ctx = new AC();
      this.master = this.ctx.createGain();
      this.master.gain.value = 0.6;
      this.master.connect(this.ctx.destination);
      const len = this.ctx.sampleRate * 1;
      this._noiseBuf = this.ctx.createBuffer(1, len, this.ctx.sampleRate);
      const d = this._noiseBuf.getChannelData(0);
      for (let i = 0; i < len; i++) d[i] = Math.random() * 2 - 1;
    } catch (e) { this.ctx = null; }
  },
  ensure() { this.init(); },
  setSound(on) { this.soundOn = on; if (this.master) this.master.gain.value = on ? 0.6 : 0; },
  setMusic(on) { this.musicOn = on; },

  _t() { return this.ctx ? this.ctx.currentTime : 0; },
  _env(g, t0, a, d, peak) {
    g.gain.setValueAtTime(0.0001, t0);
    g.gain.exponentialRampToValueAtTime(peak, t0 + a);
    g.gain.exponentialRampToValueAtTime(0.0001, t0 + a + d);
  },
  _tone(type, freq, dur, peak, t0, slide) {
    if (!this.ctx || !this.soundOn) return;
    const c = this.ctx;
    const t = t0 === undefined ? c.currentTime : t0;
    const o = c.createOscillator();
    const g = c.createGain();
    o.type = type;
    o.frequency.setValueAtTime(freq, t);
    if (slide) o.frequency.exponentialRampToValueAtTime(Math.max(20, slide), t + dur);
    this._env(g, t, 0.004, dur, peak);
    o.connect(g); g.connect(this.master);
    o.start(t); o.stop(t + dur + 0.05);
  },
  _noise(dur, peak, t0, filterFreq, type) {
    if (!this.ctx || !this.soundOn || !this._noiseBuf) return;
    const c = this.ctx;
    const t = t0 === undefined ? c.currentTime : t0;
    const src = c.createBufferSource();
    src.buffer = this._noiseBuf;
    src.loop = true;
    const f = c.createBiquadFilter();
    f.type = type || 'lowpass';
    f.frequency.value = filterFreq || 1200;
    const g = c.createGain();
    this._env(g, t, 0.004, dur, peak);
    src.connect(f); f.connect(g); g.connect(this.master);
    src.start(t); src.stop(t + dur + 0.05);
  },

  /* --- SFX --- */
  uiClick() { this._tone('square', 660, 0.06, 0.12, undefined, 880); },
  wall() { this._tone('triangle', 180, 0.08, 0.2, undefined, 140); },
  paddle() { this._tone('sine', 320, 0.09, 0.3, undefined, 500); },
  boost() { this._noise(0.18, 0.25, undefined, 2400, 'bandpass'); this._tone('sawtooth', 300, 0.16, 0.14, undefined, 700); },
  clang() { this._tone('square', 120, 0.12, 0.3, undefined, 60); this._noise(0.1, 0.2, undefined, 2000); },
  prismFizz() { this._noise(0.12, 0.18, undefined, 3500, 'highpass'); },
  forgeDing() { this._tone('sine', 980, 0.18, 0.25); this._tone('sine', 1470, 0.22, 0.12, undefined, 1450); },
  breakSound(row, special) {
    if (!this.soundOn) return;
    /* pitch rises with airStreak */
    const st = G.airStreak;
    const f = 220 * Math.pow(1.12, (10 - row)) * (1 + st * 0.02);
    this._tone('square', f, 0.09, 0.22, undefined, f * 1.3);
    if (!special) this._noise(0.06, 0.12, undefined, 2600);
  },
  powerArp() {
    const notes = [523, 659, 784, 1047];
    const t = this._t();
    for (let i = 0; i < notes.length; i++) this._tone('triangle', notes[i], 0.16, 0.2, t + i * 0.07);
  },
  chime() { this._tone('sine', 1319, 0.2, 0.2); this._tone('sine', 1760, 0.3, 0.15, undefined, 1760); },
  explosion() { this._noise(0.6, 0.5, undefined, 800); this._tone('sine', 90, 0.5, 0.4, undefined, 30); },
  laser() { this._tone('sawtooth', 1400, 0.12, 0.12, undefined, 200); this._noise(0.1, 0.1, undefined, 4000, 'highpass'); },
  catchBlip() { this._tone('sine', 880, 0.08, 0.2, undefined, 1200); },
  lostBall() {
    const t = this._t();
    this._tone('sawtooth', 400, 0.5, 0.25, t, 120);
    this._tone('triangle', 300, 0.6, 0.2, t + 0.05, 80);
  },
  fanfare() {
    const notes = [523, 659, 784, 1047, 1319];
    const t = this._t();
    for (let i = 0; i < notes.length; i++) this._tone('triangle', notes[i], 0.25, 0.25, t + i * 0.09);
  },
  levelClear() {
    const notes = [523, 659, 784, 1047, 784, 1047, 1319];
    const t = this._t();
    for (let i = 0; i < notes.length; i++) this._tone('triangle', notes[i], 0.3, 0.28, t + i * 0.13);
  },
  skyEnter() {
    const notes = [784, 988, 1175, 1568];
    const t = this._t();
    for (let i = 0; i < notes.length; i++) this._tone('sine', notes[i], 0.3, 0.2, t + i * 0.08);
  },
  skyTier(mult) {
    const base = [0, 523, 659, 784, 1047, 1319];
    const f = base[Math.min(5, Math.round(Math.log2(mult)) + 1)];
    const t = this._t();
    for (let i = 0; i < 4; i++) this._tone('triangle', f * Math.pow(1.125, i), 0.22, 0.24, t + i * 0.06);
  },
  gameOver() {
    const t = this._t();
    const notes = [440, 349, 262, 196];
    for (let i = 0; i < notes.length; i++) this._tone('sawtooth', notes[i], 0.4, 0.2, t + i * 0.22);
  },
  gemSparkle() { this._tone('sine', 2093, 0.25, 0.12, undefined, 2794); },
  boing() { this._tone('square', 200, 0.22, 0.3, undefined, 400); },

  /* --- procedural ambient music --- */
  updateMusic(dt) {
    if (!this.ctx || !this.musicOn || !Settings.music) return;
    if (G.phase !== 'playing') return;
    if (!this._started) { this._started = true; this.musicNext = this._t() + 0.1; }
    const now = this._t();
    if (now < this.musicNext) return;
    const bpm = 90, spb = 60 / bpm;
    const tier = G.mult >= 16 ? 4 : G.mult >= 8 ? 3 : G.mult >= 4 ? 2 : G.mult >= 2 ? 1 : 0;
    const scale = [0, 3, 5, 7, 10, 12]; // minor pentatonic-ish
    const root = 110;
    const bar = Math.floor(this.musicStep / 4) % 8;
    const stepInBar = this.musicStep % 4;
    /* bass */
    const bassNotes = [0, 0, 7, 5];
    this._tone('triangle', root * 2 * Math.pow(2, bassNotes[bar % 4] / 12), spb * 2, 0.16, now);
    /* arp, layers with tier */
    if (tier >= 1) {
      const n = scale[(this.musicStep) % scale.length] + 12;
      if (stepInBar !== 3 || tier >= 2) this._tone('sine', root * Math.pow(2, n / 12), spb * 0.5, 0.06, now);
    }
    if (tier >= 2) {
      const n2 = scale[(this.musicStep + 2) % scale.length] + 24;
      this._tone('sine', root * 2 * Math.pow(2, n2 / 12), spb * 0.4, 0.04, now + spb * 0.5);
    }
    if (tier >= 3) {
      this._noise(0.05, 0.03, now, 6000, 'highpass');
    }
    if (tier >= 4) {
      this._tone('sine', root * 4 * Math.pow(2, (scale[(this.musicStep + 4) % scale.length] + 12) / 12), spb * 0.9, 0.04, now);
    }
    this.musicStep = (this.musicStep + 1) % 32;
    this.musicNext = now + spb * 0.5;
  }
};




/* ============================================================
   Levels (12 handcrafted)
   Each entry: name, and rows (top -> bottom) of 9 chars.
   b basic · p prism · f forge · s sentinel · t bounce (T) · a anchor
   m drifter · x bomb · i iron · g gem · . empty
   ============================================================ */
const LEVELS = [
  { name: 'WARM-UP', rows: [
    'bbbbbbbbb',
    'bbbbbbbbb',
    'bbgbbbgbg',
    'bbbbbbbbb',
    'bbbbbbbbb',
    'bbbbbbbbb',
    'bbbbbbbbb'
  ]},
  { name: 'SIDE STEPS', rows: [
    'b.b.b.b.b',
    'bbbbbbbbb',
    'bgb.b.bgb',
    'bbbbbbbbb',
    'b.b.b.b.b',
    'bbbbbbbbb'
  ]},
  { name: 'THE PRISM', rows: [
    '..p.p.p..',
    '.ppppppp.',
    'bbbbbbbbb',
    'b.b.b.b.b',
    'bbbbbbbbb'
  ]},
  { name: 'FIRST FORGE', rows: [
    '.f.f.f.f.',
    '..f.f.f..',
    'bbbbbbbbb',
    'bgb.b.bgb',
    'bbbbbbbbb'
  ]},
  { name: 'SENTINEL WALL', rows: [
    'sssssssss',
    '..p.p.p..',
    'bbbbbbbbb',
    'b.b.b.b.b',
    'bbbbbbbbb'
  ]},
  { name: 'ANCHOR BAY', rows: [
    '..a.a.a..',
    'bbbbbbbbb',
    's.b.b.b.s',
    'bbbbbbbbb'
  ]},
  { name: 'DRIFT LANES', rows: [
    'm.m.m.m.m',
    '..f.f.f..',
    'bbbbbbbbb',
    'p.b.b.b.p',
    'bbbbbbbbb'
  ]},
  { name: 'BOMB DEFUSE', rows: [
    '..b.b.b..',
    '.x.x.x.x.',
    'ssbbbbbss',
    'bbbbbbbbb'
  ]},
  { name: 'TWIN TOWERS', rows: [
    's.ggggg.s',
    's..p.p..s',
    's.bbbbb.s',
    'bbb.b.bbb',
    'bbbbbbbbb'
  ]},
  { name: 'IRON CURTAIN', rows: [
    'i.bbbbb.i',
    'i..p.p..i',
    'i.b.f.b.i',
    'b.b.b.b.b',
    'bbbbbbbbb'
  ]},
  { name: 'THE GAUNTLET', rows: [
    's.f.b.f.s',
    '.i.p.p.i.',
    'x.bbbbb.x',
    'm.b.g.b.m',
    'bbbbbbbbb'
  ]},
  { name: 'SKY THRONE', rows: [
    'g.g.g.g.g',
    '.s.b.b.s.',
    'f.x.p.x.f',
    'm.a.i.a.m',
    'bbbbbbbbb'
  ]}
];

const LEVEL_HINTS = {
  'THE PRISM': 'Prism bricks only break on GLANCING hits!',
  'FIRST FORGE': 'Forge bricks need SPEED — get a running start!',
  'SENTINEL WALL': 'Sentinels fall only to fire, lasers, or bombs.',
  'ANCHOR BAY': 'Anchor bricks pull the ball toward them.',
  'DRIFT LANES': 'Drifter bricks patrol their lane.',
  'BOMB DEFUSE': 'Bombs explode and chain nearby bombs.',
  'IRON CURTAIN': 'Iron needs fire, lasers, or bombs to fall.'
};

function buildLevel(n) {
  const L = LEVELS[(n - 1) % LEVELS.length];
  G.bricks.length = 0;
  G.balls.length = 0;
  G.drops.length = 0;
  G.lasers.length = 0;
  G.chainQueue.length = 0;
  G.popups.length = 0;
  G.airStreak = 0; G.mult = 1; G.sky = false;
  G.skyEntered = false; G.skyLeaveT = 0;
  G.wideT = 0; G.catchT = 0; G.laserT = 0; G.slowT = 0;
  makePaddle();
  for (let r = 0; r < L.rows.length; r++) {
    const row = L.rows[r];
    for (let c = 0; c < row.length; c++) {
      const ch = row[c];
      if (ch === '.' || ch === ' ') continue;
      if (BRICK_TYPES[ch]) G.bricks.push(makeBrick(ch, c, r));
    }
  }
  G.bannerT = 0;
  return L;
}

/* guaranteed drops so sentinel/iron levels are always winnable */
function scheduleGuaranteed() {
  const types = {};
  for (let i = 0; i < G.bricks.length; i++) types[G.bricks[i].type] = true;
  const plan = [];
  if (types.s && !types.i) plan.push('F');
  else if (types.i && !types.s) plan.push('F');
  else if (types.s && types.i) plan.push('F');
  const hasFireBreakerMake = types.s || types.i;
  /* drop a FIRE drop at the start so fire-ball is obtainable; if no s/i, drop a balanced freebie */
  if (hasFireBreakerMake) {
    G.drops.push({ x: 270, y: 200, key: 'F', vy: 120, wob: rand(0, TAU), wobSp: rand(2, 4), seed: rand(0, TAU), guaranteed: true });
    G.scheduledFire = true;
  }
  return plan;
}

function startLevel(n, keepScore) {
  G.level = n;
  buildLevel(n);
  scheduleGuaranteed();
  if (n === 1 || !keepScore) {
    G.score = 0; G.showScore = 0; G.lives = 3;
    G.bricksBroken = 0; G.milestone = 10000;
    G.best = parseInt(localStorage.getItem(SAVE_BEST), 10) || 0;
  }
  G.maxMult = 1;
  G.phase = 'playing';
  G.levelClearT = 0;
  banner('LEVEL ' + n + ' — ' + LEVELS[(n - 1) % LEVELS.length].name, LEVEL_HINTS[LEVELS[(n - 1) % LEVELS.length].name] || 'Get the ball above the bricks for SKY MODE!');
  const b = spawnBall(G.paddle.x + G.paddle.w / 2, G.paddle.y - BALL_R - 2);
  b.stuck = true;
  b.speed = BALL_SPEED;
  updateHUD();
}

/* ============================================================
   Score
   ============================================================ */
function scoreAdd(v) {
  G.score += v;
  /* milestone check happens in updateTimers */
}

/* ============================================================
   Simulation (fixed timestep)
   ============================================================ */
let lastTime = 0, acc = 0, frameCount = 0, fpsWindow = 0;

function update(dt) {
  G.time += dt;
  if (G.phase === 'playing' || G.phase === 'clear') {
    updateSky(dt);
    if (!G.sky) airDecay(dt);
    updatePaddle(dt);
    updateDrifters(dt);
    updateChain(dt);
    updateLasers(dt);
    updateDrops(dt);
    updateTimers(dt);
    if (G.laserT > 0) {
      G.laserCooldown -= dt;
      if (G.laserCooldown <= 0) { fireLaser(); G.laserCooldown = 0.4; }
    }
    updateClear(dt);
    /* balls move */
    for (let i = 0; i < G.balls.length; i++) stepBall(G.balls[i], dt);
    /* remove dead balls */
    for (let i = G.balls.length - 1; i >= 0; i--) {
      if (!G.balls[i].active) G.balls.splice(i, 1);
    }
    /* compact destroyed bricks (deferred, safe after loops) */
    if (G.bricks.length !== liveBrickCount()) compactBricks();
    /* score count-up */
    G.showScore = lerp(G.showScore, G.score, 1 - Math.pow(0.0001, dt));
    if (Math.abs(G.showScore - G.score) < 1) G.showScore = G.score;
    updateHUD();
    if (G.balls.length === 0) {
      /* safety: never dead-end */
      G.lives--;
      updateHUD();
      if (G.lives <= 0) gameOver();
      else {
        const b = spawnBall(G.paddle.x + G.paddle.w / 2, G.paddle.y - BALL_R - 2);
        b.stuck = true;
        b.speed = BALL_SPEED;
        resetBallCount();
      }
    }
    checkLevelClear();
  }
  updateParticles(dt);
  updatePopups(dt);
  updateRings(dt);
  Audio.updateMusic(dt);
}
function updateRings(dt) {
  for (let i = G.rings.length - 1; i >= 0; i--) {
    const r = G.rings[i];
    r.life -= dt;
    r.r = lerp(r.r, r.max, 1 - Math.pow(0.0001, dt));
    if (r.life <= 0) G.rings.splice(i, 1);
  }
}
function updateDrifters(dt) {
  for (let i = 0; i < G.bricks.length; i++) {
    const b = G.bricks[i];
    if (b.type !== 'm' || b.dead) continue;
    const range = (STAGE_W - 2 * BRICK_X0) / 2;
    const lane = b.baseX;
    b.x = lane + Math.sin(G.time * 1.2 + b.col * 0.9) * range * 0.4;
  }
}

function loop(now) {
  requestAnimationFrame(loop);
  if (!lastTime) lastTime = now;
  let dt = (now - lastTime) / 1000;
  lastTime = now;
  if (dt > MAX_FRAME_MS / 1000) dt = MAX_FRAME_MS / 1000;
  if (dt <= 0) dt = 0.001;
  if (G.phase === 'paused' || G.phase === 'menu') {
    /* still render background + animate */
    draw();
    return;
  }
  /* hitstop: freeze physics briefly */
  if (G.hitstop > 0) {
    G.hitstop -= dt;
    updateParticles(dt * 0.3);
    draw();
    return;
  }
  /* skip physics entirely during freeze */
  if (G.freezeT > 0) { G.freezeT -= dt; draw(); return; }

  acc += dt;
  let steps = 0;
  while (acc >= STEP && steps < 5) {
    update(STEP);
    acc -= STEP;
    steps++;
  }
  if (steps === 5) acc = 0; // clamp spiral

  /* fps */
  fpsWindow += dt; frameCount++;
  if (fpsWindow >= 0.5) { G.fps = Math.round(frameCount / fpsWindow); fpsWindow = 0; frameCount = 0; }

  /* shake decay */
  G.shake = Math.max(0, G.shake - dt * 40);
  G.flash = Math.max(0, G.flash - dt * 2.2);

  draw();
}

/* ============================================================
   Renderer
   ============================================================ */
function draw() {
  ctx.setTransform(VIEW.dpr, 0, 0, VIEW.dpr, 0, 0);
  ctx.clearRect(0, 0, VIEW.w, VIEW.h);
  drawBackground();
  ctx.save();
  ctx.translate(VIEW.ox, VIEW.oy);
  ctx.scale(VIEW.scale, VIEW.scale);
  if (G.shake > 0 && !reducedMotion) {
    ctx.translate(rand(-G.shake, G.shake), rand(-G.shake, G.shake));
  }
  drawSkyEffects();
  drawBricks();
  drawDrops();
  drawLasers();
  drawRings();
  if (G.paddle) drawPaddle();
  drawBalls();
  drawParticles();
  drawPopups();
  ctx.restore();
  /* vignette + flash in screen space */
  const tier = G.mult >= 16 ? 4 : G.mult >= 8 ? 3 : G.mult >= 4 ? 2 : G.mult >= 2 ? 1 : 0;
  ctx.globalAlpha = 1;
  ctx.drawImage(vignette(tier), 0, 0, VIEW.w, VIEW.h);
  if (G.flash > 0) {
    ctx.fillStyle = 'rgba(' + G.flashColor[0] + ',' + G.flashColor[1] + ',' + G.flashColor[2] + ',' + Math.min(0.55, G.flash) + ')';
    ctx.fillRect(0, 0, VIEW.w, VIEW.h);
  }
}

/* ---------- stars + nebula (screen space) ---------- */
function drawBackground() {
  ctx.fillStyle = '#070b1d';
  ctx.fillRect(0, 0, VIEW.w, VIEW.h);
  /* nebula blobs */
  for (let i = 0; i < nebulas.length; i++) {
    const n = nebulas[i];
    const x = n.x * VIEW.w, y = n.y * VIEW.h;
    const r = n.r * Math.min(VIEW.w, VIEW.h) / 600;
    const g = ctx.createRadialGradient(x, y, 0, x, y, r);
    const bright = G.sky ? 0.16 : 0.10;
    g.addColorStop(0, 'hsla(' + n.hue + ',60%,55%,' + bright + ')');
    g.addColorStop(1, 'hsla(' + n.hue + ',60%,55%,0)');
    ctx.fillStyle = g;
    ctx.fillRect(x - r, y - r, r * 2, r * 2);
  }
  /* stars */
  const t = G.time;
  for (let i = 0; i < stars.length; i++) {
    const s = stars[i];
    const alpha = 0.35 + 0.5 * ((0.5 + 0.5 * Math.sin(t * s.sp + s.tw)));
    const x = s.x * VIEW.w, y = s.y * VIEW.h;
    ctx.fillStyle = 'rgba(220,235,255,' + (alpha * (G.sky ? 1.5 : 1)) + ')';
    ctx.fillRect(x, y, s.size, s.size);
  }
  /* stage border */
  ctx.strokeStyle = 'rgba(120,160,255,0.12)';
  ctx.lineWidth = 1;
  ctx.strokeRect(VIEW.ox, VIEW.oy, STAGE_W * VIEW.scale, STAGE_H * VIEW.scale);
}

/* ---------- sky-mode effects (stage coords) ---------- */
function drawSkyEffects() {
  if (!G.sky) return;
  const t = G.time;
  const tier = G.mult >= 16 ? 4 : G.mult >= 8 ? 3 : G.mult >= 4 ? 2 : G.mult >= 2 ? 1 : 0;
  /* god rays */
  for (let i = 0; i < 3 + tier; i++) {
    const x = (i * 0.31 + 0.2) * STAGE_W;
    const g = ctx.createLinearGradient(x, BRICK_TOP - 20, x, STAGE_H);
    g.addColorStop(0, 'rgba(255,240,200,' + (0.05 + tier * 0.02) + ')');
    g.addColorStop(1, 'rgba(255,240,200,0)');
    ctx.fillStyle = g;
    ctx.beginPath();
    ctx.moveTo(x - 10, BRICK_TOP - 30); ctx.lineTo(x + 40, BRICK_TOP - 30); ctx.lineTo(x + 120, STAGE_H); ctx.lineTo(x - 40, STAGE_H);
    ctx.closePath(); ctx.fill();
  }
  /* light rain */
  for (let i = 0; i < 18 + tier * 6; i++) {
    const x = ((i * 47 + Math.floor(t * 40)) % 560) - 10;
    const y = ((i * 137 + Math.floor(t * 300)) % STAGE_H);
    const len = 8 + tier * 4;
    ctx.strokeStyle = 'rgba(180,240,255,' + 0.25 + ')';
    ctx.lineWidth = 1;
    ctx.beginPath(); ctx.moveTo(x, y); ctx.lineTo(x, y + len); ctx.stroke();
  }
  /* radial beams at 16x */
  if (tier >= 4) {
    ctx.save();
    ctx.translate(STAGE_W / 2, BRICK_TOP);
    for (let i = 0; i < 12; i++) {
      const a = t * 0.4 + i * TAU / 12;
      ctx.rotate(TAU / 12);
      const g = ctx.createLinearGradient(0, 0, 0, STAGE_H / 2);
      g.addColorStop(0, 'rgba(255,230,160,0.10)');
      g.addColorStop(1, 'rgba(255,230,160,0)');
      ctx.fillStyle = g;
      ctx.beginPath();
      ctx.moveTo(-8, 0); ctx.lineTo(8, 0); ctx.lineTo(50, STAGE_H / 2); ctx.lineTo(-50, STAGE_H / 2);
      ctx.closePath(); ctx.fill();
    }
    ctx.restore();
  }
}

/* ---------- bricks ---------- */
function drawBricks() {
  for (let i = 0; i < G.bricks.length; i++) {
    const b = G.bricks[i];
    if (b.dead) continue;
    const d = brickDef(b);
    let x = b.x, y = b.y;
    /* shake */
    if (b.shake > 0) {
      b.shake -= 1 / 60;
      x += rand(-2.5, 2.5) * b.shake; y += rand(-2.5, 2.5) * b.shake;
    }
    /* flash (white overlay after hit) */
    const f = b.flash;
    if (f > 0) b.flash -= 1 / 60;
    const base = brickColor(b, 1);
    const dark = brickDef(b).dark;
    ctx.save();
    ctx.translate(x + b.w / 2, y + b.h / 2);
    /* glow under brick */
    drawGlow(0, 0, 44, glowFor(b.type), 0.35);
    /* body */
    ctx.fillStyle = base;
    roundRect(-b.w / 2, -b.h / 2, b.w, b.h, 5);
    ctx.fill();
    /* inner shading */
    const grd = ctx.createLinearGradient(0, -b.h / 2, 0, b.h / 2);
    grd.addColorStop(0, 'rgba(255,255,255,0.35)');
    grd.addColorStop(0.5, 'rgba(255,255,255,0.08)');
    grd.addColorStop(1, 'rgba(0,0,0,0.25)');
    ctx.fillStyle = grd;
    roundRect(-b.w / 2, -b.h / 2, b.w, b.h, 5);
    ctx.fill();
    /* per-type face */
    drawBrickFace(b, d);
    /* flash overlay */
    if (f > 0) {
      ctx.fillStyle = 'rgba(255,255,255,' + (f * 6) + ')';
      roundRect(-b.w / 2, -b.h / 2, b.w, b.h, 5);
      ctx.fill();
    }
    ctx.restore();
  }
}
function glowFor(type) {
  switch (type) {
    case 'p': return 'purple';
    case 'f': return 'amber';
    case 's': return 'pink';
    case 'a': return 'blue';
    case 't': return 'green';
    case 'm': return 'orange';
    case 'x': return 'red';
    case 'i': return 'white';
    case 'g': return 'cyan';
    default: return 'cyan';
  }
}
/* draw the type glyph/symbol on the brick face */
function drawBrickFace(b, d) {
  ctx.fillStyle = 'rgba(10,15,30,0.85)';
  ctx.font = 'bold 13px sans-serif';
  ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
  let txt = d.glyph;
  /* prisms: sideways arrows */
  if (b.type === 'p') {
    ctx.font = 'bold 12px sans-serif';
    drawChevrons(b);
    txt = '';
  }
  /* forge: flame-ish up arrows */
  if (b.type === 'f') {
    ctx.font = 'bold 11px sans-serif';
    drawTriangles(b);
    txt = '';
  }
  /* sentinel: shield-ish ring */
  if (b.type === 's') {
    ctx.strokeStyle = 'rgba(10,15,30,0.9)';
    ctx.lineWidth = 2.5;
    ctx.beginPath(); ctx.arc(0, 0, 7, 0, TAU); ctx.stroke();
    ctx.fillStyle = 'rgba(10,15,30,0.9)';
    ctx.fillRect(-1.5, -1.5, 3, 3);
    txt = '';
  }
  /* anchor: gravity ring + dot */
  if (b.type === 'a') {
    ctx.strokeStyle = 'rgba(255,255,255,0.9)';
    ctx.lineWidth = 2;
    ctx.beginPath(); ctx.arc(0, 0, 6, 0, TAU); ctx.stroke();
    ctx.fillStyle = 'rgba(255,255,255,0.9)';
    ctx.fillRect(-1.5, -1.5, 3, 3);
    txt = '';
  }
  /* iron: rivets */
  if (b.type === 'i') {
    ctx.fillStyle = 'rgba(80,95,120,0.9)';
    for (let dx = -16; dx <= 16; dx += 16) for (let dy = -6; dy <= 6; dy += 12) {
      ctx.beginPath(); ctx.arc(dx, dy, 1.8, 0, TAU); ctx.fill();
    }
    ctx.fillStyle = 'rgba(30,40,60,0.9)';
    ctx.fillRect(-1, -1, 2, 2);
    txt = '';
  }
  if (txt) ctx.fillText(txt, 0, 0);
}
function drawChevrons(b) {
  ctx.strokeStyle = 'rgba(10,15,30,0.9)';
  ctx.lineWidth = 2.5;
  for (let i = -1; i <= 1; i++) {
    const x = i * 10;
    ctx.beginPath();
    ctx.moveTo(x - 4, 5); ctx.lineTo(x, -2); ctx.lineTo(x + 4, 5);
    ctx.stroke();
  }
}
function drawTriangles(b) {
  ctx.fillStyle = 'rgba(10,15,30,0.9)';
  for (let i = -1; i <= 1; i++) {
    const x = i * 12;
    ctx.beginPath();
    ctx.moveTo(x - 5, 6); ctx.lineTo(x + 5, 6); ctx.lineTo(x, -4);
    ctx.closePath(); ctx.fill();
  }
}

/* ---------- drops ---------- */
function drawDrops() {
  for (let i = 0; i < G.drops.length; i++) {
    const d = G.drops[i];
    const def = DROP_DEFS[d.key];
    const wob = Math.sin(d.wob);
    ctx.save();
    ctx.translate(d.x, d.y);
    drawGlow(0, 0, 18, d.key === 'F' ? 'orange' : d.key === 'G' ? 'purple' : d.key === 'B' ? 'green' : d.key === 'M' ? 'gold' : d.key === 'S' ? 'blue' : d.key === 'H' ? 'green' : 'cyan', 0.5);
    ctx.rotate(wob * 0.25);
    ctx.fillStyle = def.color;
    roundRect(-10, -10, 20, 20, 5);
    ctx.fill();
    ctx.fillStyle = '#0a0f22';
    ctx.font = 'bold 12px sans-serif';
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    ctx.fillText(def.glyph, 0, 1);
    ctx.restore();
  }
}

/* ---------- lasers ---------- */
function drawLasers() {
  ctx.fillStyle = '#7df3ff';
  for (let i = 0; i < G.lasers.length; i++) {
    const l = G.lasers[i];
    drawGlow(l.x, l.y, 12, 'cyan', 0.6);
    ctx.fillRect(l.x - l.w / 2, l.y - l.h, l.w, l.h);
  }
}

/* ---------- bomb shockwave rings ---------- */
function drawRings() {
  for (let i = 0; i < G.rings.length; i++) {
    const r = G.rings[i];
    const a = clamp(r.life / 0.5, 0, 1);
    ctx.globalAlpha = a * 0.8;
    ctx.strokeStyle = '#ff4d5e';
    ctx.lineWidth = 3 + 4 * a;
    ctx.beginPath(); ctx.arc(r.x, r.y, r.r, 0, TAU); ctx.stroke();
    ctx.globalAlpha = a * 0.25;
    ctx.fillStyle = '#ff4d5e';
    ctx.beginPath(); ctx.arc(r.x, r.y, r.r * 0.9, 0, TAU); ctx.fill();
  }
  ctx.globalAlpha = 1;
}

/* ---------- paddle ---------- */
function drawPaddle() {
  const p = G.paddle;
  const cx = p.x + p.w / 2;
  ctx.save();
  /* thruster flame below when moving up */
  if (p.thrust > 0) {
    const fc = p.thrust * 20;
    const grd = ctx.createLinearGradient(cx, p.y + p.h, cx, p.y + p.h + fc);
    grd.addColorStop(0, 'rgba(70,242,255,0.8)');
    grd.addColorStop(1, 'rgba(70,242,255,0)');
    ctx.fillStyle = grd;
    ctx.beginPath();
    ctx.moveTo(cx - p.w * 0.35, p.y + p.h);
    ctx.lineTo(cx + p.w * 0.35, p.y + p.h);
    ctx.lineTo(cx, p.y + p.h + fc);
    ctx.closePath(); ctx.fill();
  }
  /* glow */
  const glowCol = G.catchT > 0 ? 'purple' : 'cyan';
  drawGlow(cx, p.y + p.h / 2, p.w * 0.7, glowCol, 0.4);
  /* body */
  const grd = ctx.createLinearGradient(0, p.y, 0, p.y + p.h);
  grd.addColorStop(0, '#cffaff');
  grd.addColorStop(0.5, '#46f2ff');
  grd.addColorStop(1, '#12b8d8');
  ctx.fillStyle = grd;
  roundRect(p.x, p.y, p.w, p.h, p.h / 2);
  ctx.fill();
  /* inner streak */
  ctx.fillStyle = 'rgba(255,255,255,0.5)';
  roundRect(p.x + 8, p.y + 3, p.w - 16, 4, 2);
  ctx.fill();
  /* catch ring */
  if (p.catchGlow > 0) {
    ctx.strokeStyle = 'rgba(200,107,255,' + (p.catchGlow * 0.6) + ')';
    ctx.lineWidth = 2;
    roundRect(p.x - 4, p.y - 4, p.w + 8, p.h + 8, p.h / 2 + 4);
    ctx.stroke();
  }
  ctx.restore();
}

/* ---------- balls ---------- */
function drawBalls() {
  for (let i = 0; i < G.balls.length; i++) {
    const b = G.balls[i];
    if (!b.active) continue;
    /* trail */
    for (let j = 0; j < b.trail.length; j++) {
      const t = b.trail[j];
      const a = (t.life / 0.28) * 0.4;
      ctx.globalAlpha = a;
      ctx.fillStyle = b.pb === 'fire' ? '#ff7b4d' : b.pb === 'ghost' ? '#b0b7ff' : '#7df3ff';
      ctx.beginPath(); ctx.arc(t.x, t.y, t.r * 0.7, 0, TAU); ctx.fill();
    }
    ctx.globalAlpha = 1;
    const r = b.pb === 'big' ? BALL_BIG_R : BALL_R;
    const col = b.pb === 'fire' ? 'orange' : b.pb === 'ghost' ? 'purple' : b.pb === 'big' ? 'green' : 'cyan';
    drawGlow(b.x, b.y, r * 3, col, 0.5);
    /* body */
    const grd = ctx.createRadialGradient(b.x - r * 0.3, b.y - r * 0.3, r * 0.2, b.x, b.y, r);
    grd.addColorStop(0, '#ffffff');
    grd.addColorStop(0.4, b.pb === 'fire' ? '#ffb347' : b.pb === 'ghost' ? '#d0d6ff' : '#9df6ff');
    grd.addColorStop(1, b.pb === 'fire' ? '#ff4d3f' : b.pb === 'ghost' ? '#8f9bff' : '#189fd6');
    ctx.fillStyle = grd;
    ctx.globalAlpha = b.pb === 'ghost' ? 0.55 : 1;
    ctx.beginPath(); ctx.arc(b.x, b.y, r, 0, TAU); ctx.fill();
    ctx.globalAlpha = 1;
    /* boost flash */
    if (b.boostFlash > 0) {
      b.boostFlash -= 1 / 60;
      ctx.strokeStyle = 'rgba(255,255,255,' + (b.boostFlash * 3) + ')';
      ctx.lineWidth = 2;
      ctx.beginPath(); ctx.arc(b.x, b.y, r + 4, 0, TAU); ctx.stroke();
    }
  }
}

/* ---------- particles ---------- */
function drawParticles() {
  for (let i = 0; i < parts.length; i++) {
    const p = parts[i];
    const a = clamp(p.life / p.maxLife, 0, 1);
    if (p.kind === 0) {      // shard / confetti rect
      ctx.save();
      ctx.translate(p.x, p.y);
      ctx.rotate(p.rot);
      ctx.globalAlpha = a;
      ctx.fillStyle = p.color;
      ctx.fillRect(-p.size / 2, -p.size / 2, p.size, p.size);
      ctx.restore();
    } else if (p.kind === 1) { // spark
      ctx.globalAlpha = a;
      ctx.fillStyle = p.color;
      ctx.beginPath(); ctx.arc(p.x, p.y, p.size * a, 0, TAU); ctx.fill();
    } else if (p.kind === 2) { // smoke
      ctx.globalAlpha = a * 0.5;
      ctx.fillStyle = p.color;
      ctx.beginPath(); ctx.arc(p.x, p.y, p.size * (1.6 - a * 0.6), 0, TAU); ctx.fill();
    } else if (p.kind === 3) { // streak
      ctx.strokeStyle = p.color;
      ctx.globalAlpha = a;
      ctx.lineWidth = 3;
      ctx.beginPath();
      ctx.moveTo(p.x, p.y);
      ctx.lineTo(p.x + p.vx * 0.02, p.y + p.vy * 0.02);
      ctx.stroke();
    } else if (p.kind === 4) { // ring
      ctx.globalAlpha = a;
      ctx.strokeStyle = p.color;
      ctx.lineWidth = 2;
      ctx.beginPath(); ctx.arc(p.x, p.y, p.size + (1 - a) * 40, 0, TAU); ctx.stroke();
    }
  }
  ctx.globalAlpha = 1;
}

/* ---------- popups ---------- */
function drawPopups() {
  for (let i = 0; i < G.popups.length; i++) {
    const p = G.popups[i];
    const a = clamp(p.life / p.max, 0, 1);
    ctx.globalAlpha = a;
    ctx.fillStyle = p.big ? '#ffc94d' : '#ffffff';
    ctx.font = (p.big ? 'bold 20px' : 'bold 14px') + ' sans-serif';
    ctx.textAlign = 'center';
    ctx.shadowColor = '#000'; ctx.shadowBlur = 4;
    ctx.fillText(p.text !== undefined ? p.text : '+' + p.v, p.x, p.y);
    ctx.shadowBlur = 0;
  }
  ctx.globalAlpha = 1;
}

function roundRect(x, y, w, h, r) {
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + w, y, x + w, y + h, r);
  ctx.arcTo(x + w, y + h, x, y + h, r);
  ctx.arcTo(x, y + h, x, y, r);
  ctx.arcTo(x, y, x + w, y, r);
  ctx.closePath();
}

/*  */


/* ============================================================
   Input (pointer + keyboard)
   ============================================================ */
const Input = {
  keys: { left: false, right: false, up: false, down: false },
  pointerActive: false,
  pointerX: STAGE_W / 2, pointerY: 860,
  relativeDelta: null,
  pointerDown: false,
  autoLaunch: false,
  lastClientX: 0, lastClientY: 0
};

/* touch thumb comfort: lift pivot up so the finger doesn't cover the paddle */
function pivotOffset() {
  const d = 44; // px in stage space
  if (isTouch) return -d;
  /* desktop: only offset near the very bottom edge */
  return -d * 0.5;
}

function onPointerDown(e) {
  Audio.init(); // first gesture creates/resumes context
  const p = toStage(e.clientX, e.clientY);
  Input.pointerActive = true;
  Input.pointerDown = true;
  Input.lastClientX = e.clientX; Input.lastClientY = e.clientY;
  Input.pointerX = clamp(p.x, 20, STAGE_W - 20);
  Input.pointerY = clamp(p.y + pivotOffset(), BAND_TOP, BAND_BOTTOM - PADDLE_H);
  /* catch launch */
  if (G.phase === 'playing' && G.catchArmed) {
    for (let i = 0; i < G.balls.length; i++) {
      if (G.balls[i].stuck) { launchBall(G.balls[i]); break; }
    }
  }
  /* menu / gameover: buttons already handle; but also allow canvas-tap launch from stuck ball */
}
function onPointerMove(e) {
  if (Input.pointerDown || isTouch) {
    const p = toStage(e.clientX, e.clientY);
    if (Settings.control === 'relative') {
      const dx = e.clientX - Input.lastClientX, dy = e.clientY - Input.lastClientY;
      Input.relativeDelta = { x: dx / VIEW.scale, y: dy / VIEW.scale };
    } else {
      Input.pointerX = clamp(p.x, 20, STAGE_W - 20);
      Input.pointerY = clamp(p.y + pivotOffset(), BAND_TOP, BAND_BOTTOM - PADDLE_H);
    }
    Input.lastClientX = e.clientX; Input.lastClientY = e.clientY;
  }
}
function onPointerUp() {
  Input.pointerActive = false;
  Input.pointerDown = false;
}
function onKeyDown(e) {
  const k = e.key.toLowerCase();
  if (k === 'arrowleft' || k === 'a') Input.keys.left = true;
  else if (k === 'arrowright' || k === 'd') Input.keys.right = true;
  else if (k === 'arrowup' || k === 'w') Input.keys.up = true;
  else if (k === 'arrowdown' || k === 's') Input.keys.down = true;
  else if (k === ' ' || k === 'enter') {
    Audio.init();
    if (G.phase === 'playing' && G.catchArmed) {
      for (let i = 0; i < G.balls.length; i++) if (G.balls[i].stuck) launchBall(G.balls[i]);
    }
    e.preventDefault();
  } else if (k === 'p' || k === 'escape') {
    togglePause();
  } else if (k === 'm') {
    toggleSound();
  }
}
function onKeyUp(e) {
  const k = e.key.toLowerCase();
  if (k === 'arrowleft' || k === 'a') Input.keys.left = false;
  else if (k === 'arrowright' || k === 'd') Input.keys.right = false;
  else if (k === 'arrowup' || k === 'w') Input.keys.up = false;
  else if (k === 'arrowdown' || k === 's') Input.keys.down = false;
}

/* ============================================================
   UI / screens
   ============================================================ */
const $ = function (id) { return document.getElementById(id); };
function screen(name) {
  ['menu', 'pause', 'gameover', 'howto'].forEach(function (id) {
    $(id).classList.toggle('hidden', id !== name);
  });
  $('hud').classList.toggle('hidden', !(G.phase === 'playing' || G.phase === 'clear'));
}
function updateHUD() {
  const el = $('hud-score');
  const wantScore = formatScore(Math.round(G.showScore));
  if (el && el._last !== wantScore) { el._last = wantScore; el.textContent = wantScore; }
  const lv = $('hud-level');
  const wantLv = 'LEVEL ' + G.level;
  if (lv && lv._last !== wantLv) { lv._last = wantLv; lv.textContent = wantLv; }
  /* lives pips */
  const lives = $('hud-lives');
  if (lives && lives._lastDom !== G.lives) {
    lives._lastDom = G.lives;
    let html = '';
    for (let i = 0; i < 5; i++) html += '<div class="life-pip' + (i < G.lives ? '' : ' off') + '"></div>';
    lives.innerHTML = html;
  }
}
function showBanner(title, sub) { banner(title, sub); }
function showMenu() {
  G.phase = 'menu';
  screen('menu');
  $('menu-best').innerHTML = 'BEST&nbsp;&nbsp;<b>' + formatScore(G.best) + '</b>';
}
function startGame() {
  Audio.init();
  G.level = 1;
  G.score = 0; G.showScore = 0; G.lives = 3; G.bricksBroken = 0;
  G.maxMult = 1; G.newBest = false;
  startLevel(1, false);
  screen('playing');
}
function restartLevel() {
  Audio.init();
  const keep = G.score;
  startLevel(G.level, true);
  G.score = keep;
  screen('playing');
}
function gameOver() {
  G.phase = 'gameover';
  if (G.score > G.best) { G.best = G.score; G.newBest = true; saveBest(); }
  Audio.gameOver();
  screen('gameover');
  $('go-score').textContent = formatScore(G.score);
  $('go-best').textContent = formatScore(G.best);
  $('go-level').textContent = String(G.level);
  $('go-bricks').textContent = String(G.bricksBroken);
  $('go-mult').textContent = 'x' + G.maxMult;
  $('go-newbest').classList.toggle('hidden', !G.newBest);
}
function togglePause() {
  if (G.phase === 'playing' || G.phase === 'clear') {
    G.phase = 'paused';
    screen('pause');
    Input.pointerDown = false; Input.pointerActive = false;
  } else if (G.phase === 'paused') {
    G.phase = 'playing';
    screen('playing');
  }
}
function toggleSound() {
  Settings.sound = !Settings.sound;
  Audio.setSound(Settings.sound);
  saveSettings();
  document.body.classList.toggle('muted', !Settings.sound);
}
function toggleMusic() {
  Settings.music = !Settings.music;
  Audio.setMusic(Settings.music);
  saveSettings();
}
function setControl(mode) { Settings.control = mode; saveSettings(); syncSettingsUI(); }

function syncSettingsUI() {
  document.querySelectorAll('.toggle').forEach(function (t) {
    const s = t.getAttribute('data-setting');
    const on = s === 'sound' ? Settings.sound : Settings.music;
    t.classList.toggle('on', on);
  });
  document.querySelectorAll('.segmented').forEach(function (seg) {
    const s = seg.getAttribute('data-setting');
    if (s !== 'control') return;
    seg.querySelectorAll('button').forEach(function (b) {
      b.classList.toggle('on', b.getAttribute('data-value') === Settings.control);
    });
  });
  document.body.classList.toggle('muted', !Settings.sound);
}

/* how-to content */
function buildHowto() {
  const el = $('howto-body');
  /* brick legend */
  let legend = '<h3>THE BRICKS</h3><div class="legend-grid">';
  const items = [
    ['b', 'BASIC', 'One hit. Row-colored.'],
    ['g', 'GEM', 'Precious! +1000 × mult, 40% drop chance.'],
    ['p', 'PRISM', 'Breaks only on GLANCING (side) hits.'],
    ['f', 'FORGE', 'Needs ball speed of 560+.'],
    ['s', 'SENTINEL', 'Immune to balls. Fire / laser / bomb.'],
    ['i', 'IRON', 'Immune to balls. Fire / laser / bomb.'],
    ['a', 'ANCHOR', 'Pulls the ball toward it.'],
    ['m', 'DRIFTER', 'Patrols its lane.'],
    ['x', 'BOMB', 'Explodes! Chains nearby bombs.'],
    ['t', 'BOUNCE', 'Boosts ball speed ×1.4.']
  ];
  for (let i = 0; i < items.length; i++) {
    const it = items[i];
    const d = BRICK_TYPES[it[0]];
    legend += '<div class="legend-item"><div class="legend-glyph" style="background:' + d.color + '">' + d.glyph + '</div><div class="txt"><b>' + it[1] + '</b>' + it[2] + '</div></div>';
  }
  legend += '</div>';
  el.innerHTML =
    '<h3>CONTROLS</h3>' +
    '<ul><li><b>Drag / move anywhere</b> — the paddle follows your pointer (also reaches high).</li>' +
    '<li><b>Lift boost:</b> keep moving the paddle <b>UP</b> when the ball hits — it launches with extra speed. Thruster glow shows when you are lifting.</li>' +
    '<li>Keyboard: <span class="kbd">←→</span>/<span class="kbd">A D</span> move, <span class="kbd">↑↓</span>/<span class="kbd">W S</span> climb/descend. <span class="kbd">P</span> pause, <span class="kbd">M</span> mute.</li>' +
    '<li><b>CATCH</b> power-up: ball sticks on contact — tap / click / <span class="kbd">Space</span> to throw it.</li></ul>' +
    '<h3>SKY MODE — the heart of the game</h3>' +
    '<p>Get the ball <b>ABOVE the highest brick</b>. Then every brick you break raises your <b>AIR STREAK</b> and multiplier up to <b>16×</b>. Drop back below for too long and the streak decays. Break through, rise above.</p>' +
    '<h3>POWER-UPS</h3>' +
    '<ul><li><b>FIRE</b> — ball pierces bricks, smashes sentinel & iron.</li>' +
    '<li><b>GHOST</b> — phases through bricks without breaking (sneak up to the rooftop).</li>' +
    '<li><b>BIG</b> — huge ball, radius smash.</li>' +
    '<li><b>MULTI</b> — splits every ball in two.</li>' +
    '<li><b>WIDEN / LASER / CATCH / SLOW / LIFE</b> — paddle length, auto-lasers, sticky catch, slow-mo, extra life.</li></ul>' +
    '<h3>STRATEGY</h3><p>Prism, forge, sentinel and iron bricks resist plain hits. Use the lift boost to carve straight up the middle, or grab a FIRE or GHOST capsule to break the roof. Bombs chain — use them to tear open the sky.</p>' +
    legend;
}

/* ============================================================
   Main bootstrap
   ============================================================ */
function boot() {
  if (FAKE_DOM) return; // headless test stub
  canvas = document.getElementById('game');
  ctx = canvas.getContext('2d');
  loadSettings();
  G.best = parseInt(localStorage.getItem(SAVE_BEST), 10) || 0;
  makeGlowSprites();
  initBackground();
  resize();
  window.addEventListener('resize', resize);
  /* visibility auto-pause */
  document.addEventListener('visibilitychange', function () {
    if (document.hidden && (G.phase === 'playing' || G.phase === 'clear')) togglePause();
  });
  /* pointer */
  canvas.addEventListener('pointerdown', onPointerDown);
  window.addEventListener('pointermove', onPointerMove);
  window.addEventListener('pointerup', onPointerUp);
  window.addEventListener('pointercancel', onPointerUp);
  /* keyboard */
  window.addEventListener('keydown', onKeyDown);
  window.addEventListener('keyup', onKeyUp);
  window.addEventListener('blur', function () { Input.keys = { left: false, right: false, up: false, down: false }; });
  /* buttons */
  $('btn-play').addEventListener('click', function () { Audio.uiClick(); Audio.init(); startGame(); });
  $('btn-howto').addEventListener('click', function () { Audio.uiClick(); buildHowto(); $('howto').classList.remove('hidden'); });
  $('btn-howto-close').addEventListener('click', function () { Audio.uiClick(); $('howto').classList.add('hidden'); });
  $('btn-resume').addEventListener('click', function () { Audio.uiClick(); togglePause(); });
  $('btn-restart').addEventListener('click', function () { Audio.uiClick(); restartLevel(); });
  $('btn-menu').addEventListener('click', function () { Audio.uiClick(); showMenu(); });
  $('btn-again').addEventListener('click', function () { Audio.uiClick(); startGame(); });
  $('btn-go-menu').addEventListener('click', function () { Audio.uiClick(); showMenu(); });
  $('btn-pause').addEventListener('click', function () { Audio.uiClick(); togglePause(); });
  $('btn-mute').addEventListener('click', function () { Audio.uiClick(); toggleSound(); });
  /* settings toggles */
  document.querySelectorAll('.toggle').forEach(function (t) {
    t.addEventListener('click', function () {
      const s = t.getAttribute('data-setting');
      if (s === 'sound') toggleSound(); else toggleMusic();
      Audio.uiClick();
    });
  });
  document.querySelectorAll('.segmented').forEach(function (seg) {
    seg.querySelectorAll('button').forEach(function (b) {
      b.addEventListener('click', function () { setControl(b.getAttribute('data-value')); Audio.uiClick(); });
    });
  });
  syncSettingsUI();
  updateHUD();
  showMenu();
  lastTime = 0; acc = 0;
  requestAnimationFrame(loop);
}

/* ============================================================
   Debug hook (window.__game)
   ============================================================ */
if (!FAKE_DOM) {
  window.__game = {
    getState: function () {
      let balls = 0, bricks = 0;
      for (let i = 0; i < G.balls.length; i++) if (G.balls[i].active) balls++;
      for (let i = 0; i < G.bricks.length; i++) if (!G.bricks[i].dead) bricks++;
      return {
        score: G.score, best: G.best, level: G.level, lives: G.lives,
        multiplier: G.mult, airStreak: Math.floor(G.airStreak), skyMode: G.sky,
        balls: balls, bricks: bricks, phase: G.phase, fps: G.fps
      };
    },
    debug: {
      clearBricks: function () {
        const copy = G.bricks.slice();
        for (let i = 0; i < copy.length; i++) if (!copy[i].dead) destroyBrick(copy[i], 'ball');
        compactBricks();
      },
      breakAllBricks: function () {
        const copy = G.bricks.slice();
        for (let i = 0; i < copy.length; i++) if (!copy[i].dead) destroyBrick(copy[i], 'chain');
        compactBricks();
      },
      spawnPowerup: function (name) {
        spawnDrop(G.paddle ? G.paddle.x + G.paddle.w / 2 : 270, 300, false);
        const d = G.drops[G.drops.length - 1];
        d.key = String(name).toUpperCase() === 'LIFE' ? 'H' : String(name).toUpperCase().slice(0, 1);
      },
      gotoLevel: function (n) { startLevel(n, true); },
      addLives: function (n) { G.lives = clamp(G.lives + (n || 1), 0, 5); updateHUD(); },
      setSpeed: function (n) { G.speedMul = clamp(n || 1, 0.1, 5); },
      toggleSky: function () {
        G.sky = !G.sky;
        if (G.sky) { G.airStreak = 1; G.mult = 2; G.flash = 0.4; G.flashColor = [70, 242, 255]; }
        else { G.airStreak = 0; G.mult = 1; }
        updateSkyBadge();
      }
    }
  };
}

/* headless test runner (node test.js can call these) */
function __testHook() { return { G: G, collideBricks: collideBricks, stepBall: stepBall }; }

/* start the game (script is at end of <body>, DOM is ready) */
if (!FAKE_DOM) {
  var __bootEl = typeof document !== 'undefined' ? document.getElementById('game') : null;
  if (__bootEl && typeof __bootEl.getContext === 'function') boot();
}


