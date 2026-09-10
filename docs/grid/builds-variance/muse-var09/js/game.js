/* OVER THE TOP — main game engine (vanilla canvas, offline). */
(function () {
'use strict';
const $ = id => document.getElementById(id);
const clamp = (v, a, b) => v < a ? a : v > b ? b : v;
const rand = (a, b) => a + Math.random() * (b - a);
const TAU = Math.PI * 2;

// ---------- canvas / sizing ----------
const canvas = $('game'), ctx = canvas.getContext('2d');
let W = 0, H = 0, DPR = 1;
function resize() {
  DPR = Math.min(window.devicePixelRatio || 1, 2);
  W = window.innerWidth; H = window.innerHeight;
  canvas.width = Math.floor(W * DPR); canvas.height = Math.floor(H * DPR);
  canvas.style.width = W + 'px'; canvas.style.height = H + 'px';
  ctx.setTransform(DPR, 0, 0, DPR, 0, 0);
  layoutBricks();
}
window.addEventListener('resize', resize);
window.addEventListener('orientationchange', () => setTimeout(resize, 200));

// ---------- audio ----------
const audio = new AudioManager();
function ensureAudio() { audio.init(); }
window.addEventListener('pointerdown', ensureAudio, { passive: true });
window.addEventListener('keydown', ensureAudio);

// ---------- persistent ----------
let highScore = parseInt(localStorage.getItem('ott_high') || '0', 10) || 0;
function saveHigh() { localStorage.setItem('ott_high', String(highScore)); }

// ---------- game state ----------
const S = { TITLE: 0, INTRO: 1, SERVE: 2, PLAY: 3, PAUSE: 4, CLEAR: 5, OVER: 6, WIN: 7 };
let state = S.TITLE, prevState = S.TITLE;
let levelIndex = 0, score = 0, lives = 3;
let combo = 0, comboTimer = 0, multiplier = 1, maxMult = 1;
let topZone = false, topZoneTime = 0, topZoneMult = 1;
let shake = 0, hitStop = 0, slowMo = 0, timeScale = 1, elapsed = 0, bgPulse = 0;
let clearTimer = 0, introTimer = 0, serveDir = 0;
let catchTimer = 0, slowTimer = 0;
let isTouch = ('ontouchstart' in window) || navigator.maxTouchPoints > 0;
let touchMode = localStorage.getItem('ott_touchmode') || 'drag'; // drag | stick

// ---------- entities ----------
let bricks = [], balls = [], particles = [], popups = [], pickups = [], floaters = [];
let brickCols = 12, brickW = 40, brickH = 22, gridX = 0, gridY = 0;
const paddle = { x: 0, y: 0, w: 110, h: 16, vx: 0, vy: 0, px: 0, py: 0, tx: null, ty: null, squash: 0, glow: 0, wideT: 0, kvx: 0, kvy: 0 };

const BALL_DEF = {
  normal: { r: 8, speed: 500, color: '#ffffff', glow: '#9ad7ff', score: 1 },
  fire:   { r: 9, speed: 520, color: '#ff9a3d', glow: '#ff5a00', score: 1.5 },
  ghost:  { r: 8, speed: 500, color: '#c9a6ff', glow: '#8f4fff', score: 1.25 },
  volt:   { r: 7, speed: 720, color: '#fff94f', glow: '#ffe600', score: 2 },
  big:    { r: 14, speed: 460, color: '#6afff3', glow: '#00e6c8', score: 1 }
};
const PRISM_MIN_ANGLE = 35 * Math.PI / 180; // from horizontal
const VELOCITY_MIN = 520;

// ---------- DOM ----------
const hud = $('hud'), hudScore = $('hud-score'), hudHigh = $('hud-high'), hudLevel = $('hud-level'),
  hudCombo = $('hud-combo'), hudComboFill = $('hud-combo-fill'), hudLives = $('hud-lives'), hudBalls = $('hud-balls'),
  tzBanner = $('topzone-banner'), tzN = $('topzone-n'), topHint = $('top-hint'), toast = $('toast');
let toastT = 0;
function showToast(msg, ms) {
  toast.textContent = msg; toast.classList.remove('hidden');
  toastT = (ms || 1600) / 1000;
}

// legend build
(function buildLegend() {
  const L = $('legend'), B = window.OTT.BRICKS;
  const order = ['standard', 'prism', 'velocity', 'drifter', 'phase', 'bomb', 'titanium', 'bonus'];
  L.innerHTML = order.map(k => `<div class="leg-row"><span class="sw" style="background:linear-gradient(135deg,${B[k].color},${B[k].color2});box-shadow:0 0 8px ${B[k].color}"></span><span><b>${B[k].name}</b> — ${B[k].desc}</span></div>`).join('');
  const BL = $('legend-balls'), BD = window.OTT.BALLS;
  BL.innerHTML = Object.keys(BD).map(k => `<div class="leg-row"><span class="sw" style="background:${BD[k].color};box-shadow:0 0 8px ${BD[k].color};width:14px;height:14px;border-radius:50%"></span><span><b>${BD[k].name}</b> — ${BD[k].desc}</span></div>`).join('')
    + `<div class="leg-row"><span>🎁</span><span><b>Pickups</b> — Multiball, Fire, Ghost, Volt, Big, Wide, Slow-mo, +1 Life, Catch. Catch with paddle!</span></div>`;
})();

function updateTouchHint() {
  $('controls-hint-desktop').classList.toggle('hidden', isTouch);
  $('controls-hint-touch').classList.toggle('hidden', !isTouch);
  $('btn-touch-mode').textContent = 'Touch controls: ' + touchMode.toUpperCase() + ' (tap to switch)';
}
updateTouchHint();
$('btn-touch-mode').addEventListener('click', e => {
  e.stopPropagation(); audio.init(); audio.uiClick();
  touchMode = touchMode === 'drag' ? 'stick' : 'drag';
  localStorage.setItem('ott_touchmode', touchMode); updateTouchHint();
  showToast(touchMode === 'stick' ? 'THUMBSTICK MODE: hold & push' : 'DRAG MODE: drag anywhere');
});

// ---------- levels / bricks ----------
function charToType(ch) {
  return { '#': 'standard', T: 'titanium', P: 'prism', V: 'velocity', D: 'drifter', H: 'phase', B: 'bomb', O: 'bonus' }[ch] || null;
}
function loadLevel(idx) {
  const L = window.OTT.LEVELS[idx];
  bricks = []; particles = []; popups = []; pickups = []; balls = [];
  brickCols = 12;
  brickW = clamp((Math.min(W, 760) - 24) / brickCols, 22, 62);
  brickH = clamp(brickW * 0.52, 16, 30);
  gridX = (W - brickW * brickCols) / 2;
  gridY = H < 560 ? 84 : 104;
  const rows = L.map;
  for (let r = 0; r < rows.length; r++) {
    for (let c = 0; c < Math.min(rows[r].length, brickCols); c++) {
      const t = charToType(rows[r][c]);
      if (!t) continue;
      bricks.push({
        c, r, type: t, alive: true,
        x: gridX + c * brickW, y: gridY + r * brickH, w: brickW - 3, h: brickH - 3,
        phase: Math.random() * TAU, shieldT: 0, flash: 0, born: elapsed + (r * 0.03 + c * 0.008),
        driftDir: (c % 2 === 0 ? 1 : -1), driftOff: 0
      });
    }
  }
  paddle.w = clamp(W * 0.16, 84, 130); paddle.h = 16;
  paddle.x = W / 2; paddle.y = H - Math.max(56, H * 0.10);
  paddle.vx = 0; paddle.vy = 0; paddle.tx = null; paddle.ty = null;
  paddle.wideT = 0; catchTimer = 0; slowTimer = 0;
  combo = 0; comboTimer = 0; multiplier = 1; topZone = false; topZoneTime = 0; topZoneMult = 1;
  spawnServeBall('normal');
  updateHUD();
}
function layoutBricks() {
  if (!bricks.length && state !== S.TITLE) return;
  // recompute geometry on resize, keep types
  brickW = clamp((Math.min(W, 760) - 24) / brickCols, 22, 62);
  brickH = clamp(brickW * 0.52, 16, 30);
  gridX = (W - brickW * brickCols) / 2;
  gridY = H < 560 ? 84 : 104;
  for (const b of bricks) { b.x = gridX + b.c * brickW; b.y = gridY + b.r * brickH; b.w = brickW - 3; b.h = brickH - 3; }
  paddle.y = clamp(paddle.y || H - 80, H * 0.80, H - 34);
  paddle.x = clamp(paddle.x || W / 2, paddle.w / 2, W - paddle.w / 2);
}
function lowestBrickBottom() {
  let m = -Infinity;
  for (const b of bricks) if (b.alive && b.type !== 'titanium') m = Math.max(m, b.y + b.h);
  return m === -Infinity ? gridY : m;
}
function remainingBreakable() { let n = 0; for (const b of bricks) if (b.alive && b.type !== 'titanium') n++; return n; }

// ---------- balls ----------
function makeBall(x, y, type, angle) {
  const d = BALL_DEF[type] || BALL_DEF.normal;
  const a = angle != null ? angle : (-Math.PI / 2 + rand(-0.5, 0.5));
  const sp = d.speed * rand(0.98, 1.02) * (slowTimer > 0 ? 0.7 : 1);
  return { x, y, vx: Math.cos(a) * sp, vy: Math.sin(a) * sp, r: d.r, type, trail: [], stuck: false, born: elapsed, hitCd: 0 };
}
function spawnServeBall(type) {
  const b = makeBall(paddle.x, paddle.y - paddle.h / 2 - 10, type || 'normal', -Math.PI / 2);
  b.stuck = true; b.vx = 0; b.vy = 0;
  balls.push(b);
}
function ballSpeed(b) { return Math.hypot(b.vx, b.vy); }
function setBallSpeed(b, s) {
  const c = Math.max(ballSpeed(b), 0.001); b.vx *= s / c; b.vy *= s / c;
}
function transformBalls(type) {
  for (const b of balls) {
    const sp = ballSpeed(b) || BALL_DEF[type].speed;
    b.type = type; b.r = BALL_DEF[type].r;
    setBallSpeed(b, Math.max(sp, BALL_DEF[type].speed * 0.85));
  }
}

// ---------- powerups ----------
function dropPickup(x, y, forceKind) {
  const pool = window.OTT.POWERUPS;
  let kind;
  if (forceKind) kind = forceKind;
  else {
    const r = Math.random();
    if (r < 0.20) kind = 'multiball';
    else if (r < 0.32) kind = 'fire'; else if (r < 0.44) kind = 'ghost';
    else if (r < 0.54) kind = 'volt'; else if (r < 0.64) kind = 'big';
    else if (r < 0.76) kind = 'wide'; else if (r < 0.84) kind = 'slow';
    else if (r < 0.90) kind = 'life'; else kind = 'catch';
  }
  const info = pool.find(p => p.kind === kind);
  pickups.push({ x, y, vy: 130, kind, color: info.color, label: info.label, t: 0 });
}
function applyPickup(p) {
  audio.powerup();
  addPopup(p.x, p.y - 10, p.label, p.color, 15);
  burst(p.x, p.y, p.color, 18, 260);
  switch (p.kind) {
    case 'multiball': {
      const src = balls.filter(b => !b.stuck).slice(0, 4);
      const base = src.length ? src : balls.slice(0, 1);
      for (const b of base) {
        for (let i = 0; i < 2 && balls.length < 9; i++) {
          const nb = makeBall(b.x, b.y, b.type, Math.atan2(b.vy, b.vx) + rand(-0.7, 0.7));
          balls.push(nb);
        }
      }
      if (!balls.length) spawnServeBall('normal');
      break;
    }
    case 'fire': case 'ghost': case 'volt': case 'big': transformBalls(p.kind); break;
    case 'wide': paddle.wideT = 14; paddle.w = clamp(W * 0.16, 84, 130) * 1.5; break;
    case 'slow': slowTimer = 10; for (const b of balls) setBallSpeed(b, Math.min(ballSpeed(b), 430)); break;
    case 'life': lives = Math.min(lives + 1, 5); break;
    case 'catch': catchTimer = 8; break;
  }
  updateHUD();
}

// ---------- particles / popups / shake ----------
function burst(x, y, color, n, spd) {
  const tier = effectTier();
  n = Math.round(n * (1 + tier * 0.5 + (topZone ? 0.8 : 0)));
  for (let i = 0; i < n; i++) {
    if (particles.length > 900) return;
    const a = Math.random() * TAU, s = rand(spd * 0.2, spd);
    particles.push({ x, y, vx: Math.cos(a) * s, vy: Math.sin(a) * s - 60, life: rand(0.35, 0.9), age: 0, color, size: rand(1.5, 4.5), shard: false, drag: 0.98 });
  }
}
function shards(x, y, w, h, color, color2) {
  const n = 7 + effectTier() * 3;
  for (let i = 0; i < n; i++) {
    if (particles.length > 900) return;
    particles.push({ x: x + rand(0, w), y: y + rand(0, h), vx: rand(-260, 260), vy: rand(-380, -40),
      life: rand(0.5, 1.1), age: 0, color: Math.random() < 0.5 ? color : color2, size: rand(2, 6),
      shard: true, rot: Math.random() * TAU, vr: rand(-9, 9), drag: 0.985 });
  }
}
function addPopup(x, y, text, color, size) {
  if (popups.length > 40) popups.shift();
  popups.push({ x: clamp(x, 50, W - 50), y, text, color: color || '#fff', age: 0, life: 1.0, size: size || 13 });
}
function addShake(amount) { shake = Math.min(shake + amount, 26); }
function effectTier() { // 0..4 rises with multiplier
  if (multiplier >= 10) return 4; if (multiplier >= 7) return 3;
  if (multiplier >= 4) return 2; if (multiplier >= 2) return 1; return 0;
}

// ---------- input: mouse + keys + touch (drag & stick) + gamepad ----------
const keys = {};
let mouseActive = false, mouseX = 0, mouseY = 0;
const stick = { active: false, id: null, bx: 0, by: 0, dx: 0, dy: 0 };
const dragTouch = { id: null, offY: 70 };
const stickBaseEl = $('stick-base'), stickKnobEl = $('stick-knob');

canvas.addEventListener('mousemove', e => { mouseActive = true; mouseX = e.clientX; mouseY = e.clientY; });
canvas.addEventListener('mousedown', e => { ensureAudio(); mouseActive = true; mouseX = e.clientX; mouseY = e.clientY; onLaunchPress(); });
window.addEventListener('keydown', e => {
  if (['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', ' '].includes(e.key)) e.preventDefault();
  if (e.repeat) { keys[e.key.toLowerCase()] = true; return; }
  keys[e.key.toLowerCase()] = true; ensureAudio();
  if (e.key === ' ' || e.key === 'Enter') onLaunchPress();
  if (e.key === 'p' || e.key === 'P' || e.key === 'Escape') togglePause();
  if (e.key === 'm' || e.key === 'M') toggleMute();
});
window.addEventListener('keyup', e => { keys[e.key.toLowerCase()] = false; });

function canvasPos(t) { return { x: t.clientX, y: t.clientY }; }
canvas.addEventListener('touchstart', e => {
  e.preventDefault(); ensureAudio(); isTouch = true; updateTouchHint(); showMobileBtns();
  for (const t of e.changedTouches) {
    const p = canvasPos(t);
    if (touchMode === 'stick' && !stick.active) {
      stick.active = true; stick.id = t.identifier; stick.bx = p.x; stick.by = p.y; stick.dx = 0; stick.dy = 0;
      stickBaseEl.style.left = p.x + 'px'; stickBaseEl.style.top = p.y + 'px';
      stickBaseEl.classList.remove('hidden');
    } else if (dragTouch.id === null) {
      dragTouch.id = t.identifier;
      paddle.tx = clamp(p.x, paddle.w / 2, W - paddle.w / 2);
      paddle.ty = clamp(p.y - dragTouch.offY, H * 0.80, H - 34);
      moveDrag(p);
    }
  }
}, { passive: false });
function moveDrag(p) {
  paddle.tx = clamp(p.x, paddle.w / 2, W - paddle.w / 2);
  paddle.ty = clamp(p.y - dragTouch.offY, H * 0.80, H - 34);
}
canvas.addEventListener('touchmove', e => {
  e.preventDefault();
  for (const t of e.changedTouches) {
    const p = canvasPos(t);
    if (stick.active && t.identifier === stick.id) {
      stick.dx = clamp(p.x - stick.bx, -55, 55); stick.dy = clamp(p.y - stick.by, -55, 55);
      stickKnobEl.style.transform = `translate(calc(-50% + ${stick.dx}px), calc(-50% + ${stick.dy}px))`;
    } else if (t.identifier === dragTouch.id) moveDrag(p);
  }
}, { passive: false });
function endTouch(e) {
  for (const t of e.changedTouches) {
    if (stick.active && t.identifier === stick.id) {
      stick.active = false; stick.id = null; stick.dx = 0; stick.dy = 0;
      stickBaseEl.classList.add('hidden');
      stickKnobEl.style.transform = 'translate(-50%,-50%)';
    }
    if (t.identifier === dragTouch.id) { dragTouch.id = null; paddle.tx = null; paddle.ty = null; }
  }
}
canvas.addEventListener('touchend', endTouch); canvas.addEventListener('touchcancel', endTouch);
// prevent scroll/zoom gestures
document.addEventListener('gesturestart', e => e.preventDefault());
document.addEventListener('dblclick', e => e.preventDefault(), { passive: false });
let lastTap = 0;
canvas.addEventListener('touchend', e => {
  const now = performance.now();
  if (now - lastTap < 300) onLaunchPress();
  lastTap = now;
});

function pollGamepad(dt) {
  const gps = navigator.getGamepads ? navigator.getGamepads() : [];
  for (const g of gps) {
    if (!g || !g.connected) continue;
    const ax = g.axes[0] || 0, ay = g.axes[1] || 0;
    const b = g.buttons;
    if (Math.abs(ax) > 0.15 || Math.abs(ay) > 0.15) {
      paddle.x = clamp(paddle.x + ax * 560 * dt, paddle.w / 2, W - paddle.w / 2);
      paddle.y = clamp(paddle.y + ay * 320 * dt, H * 0.80, H - 34);
      paddle.tx = null; paddle.ty = null; mouseActive = false;
    }
    if (b[0] && b[0].pressed && !pollGamepad._a) onLaunchPress();
    if (b[9] && b[9].pressed && !pollGamepad._s) togglePause();
    pollGamepad._a = b[0] && b[0].pressed; pollGamepad._s = b[9] && b[9].pressed;
    break;
  }
}

function onLaunchPress() {
  if (state === S.TITLE) { startGame(); return; }
  if (state === S.SERVE || (state === S.PLAY && balls.some(b => b.stuck))) launchStuck();
  else if (state === S.OVER || state === S.WIN) { startGame(); }
}
function launchStuck() {
  audio.launch();
  for (const b of balls) {
    if (!b.stuck) continue;
    b.stuck = false;
    const aim = clamp(paddle.vx / 900, -0.6, 0.6) + rand(-0.12, 0.12);
    const a = -Math.PI / 2 + aim;
    const sp = (BALL_DEF[b.type] || BALL_DEF.normal).speed;
    b.vx = Math.cos(a) * sp; b.vy = Math.sin(a) * sp;
    burst(b.x, b.y, '#ffffff', 10, 200);
  }
  if (state === S.SERVE) state = S.PLAY;
  if (state === S.PLAY) updateHUD();
}

// ---------- scoring ----------
function addScore(base, x, y, label) {
  const d = 1;
  const pts = Math.round(base * multiplier * topZoneMult * d);
  score += pts;
  if (score > highScore) { highScore = score; saveHigh(); }
  combo++; comboTimer = topZone ? 6 : 3.2;
  const newMult = Math.min(12, 1 + Math.floor(combo / 4));
  if (newMult > multiplier) {
    multiplier = newMult; maxMult = Math.max(maxMult, multiplier);
    addPopup(paddle.x, paddle.y - 60, 'MULTIPLIER ×' + multiplier + '!', '#ffd94f', 20);
    audio.powerup(); addShake(5);
  }
  addPopup(x, y, '+' + pts + (label ? ' ' + label : ''), topZone ? '#ffd94f' : '#ffffff', topZone ? 16 : 13);
}

// ---------- brick hit logic (no multi-HP; difficulty via angle/speed/timing) ----------
function brickSolidAt(b) {
  if (b.type !== 'phase') return true;
  return Math.sin(elapsed * 3.2 + b.phase) > -0.15; // visible ~55% of cycle
}
function tryBreakBrick(b, ball, nx, ny) {
  const spd = ballSpeed(ball);
  const angFromH = Math.abs(Math.asin(clamp(Math.abs(ball.vy) / Math.max(spd, 1), -1, 1))); // 0=grazing, 90°=vertical
  if (b.type === 'titanium') { b.flash = 0.15; audio.wall(); burst(ball.x, ball.y, '#8f9bb3', 5, 160); return 'bounce'; }
  if (b.type === 'phase' && !brickSolidAt(b)) return 'pass';
  if (b.type === 'prism' && angFromH < PRISM_MIN_ANGLE) {
    b.shieldT = 0.5;
    audio.shield();
    addPopup(b.x + b.w / 2, b.y - 6, 'STEEP ANGLE NEEDED!', '#b06bff', 12);
    burst(ball.x, ball.y, '#b06bff', 8, 200);
    return 'bounce';
  }
  if (b.type === 'velocity' && spd < VELOCITY_MIN) {
    b.shieldT = 0.5;
    audio.shield();
    addPopup(b.x + b.w / 2, b.y - 6, 'FASTER!', '#ffd94f', 12);
    burst(ball.x, ball.y, '#ffd94f', 8, 200);
    return 'bounce';
  }
  breakBrick(b, ball);
  return ball.type === 'ghost' ? 'pass' : 'bounce';
}
function breakBrick(b, ball) {
  if (!b.alive) return;
  b.alive = false; b.flash = 0.2;
  const B = window.OTT.BRICKS[b.type];
  const cx = b.x + b.w / 2, cy = b.y + b.h / 2;
  shards(b.x, b.y, b.w, b.h, B.color, B.color2);
  burst(cx, cy, '#ffffff', 8, 240);
  const baseScore = { standard: 50, prism: 90, velocity: 90, drifter: 80, phase: 90, bomb: 120, bonus: 70 }[b.type] || 50;
  const ballMult = (BALL_DEF[ball.type] || BALL_DEF.normal).score;
  audio.brick(combo);
  addScore(Math.round(baseScore * ballMult), cx, cy, '');
  bgPulse = Math.min(bgPulse + 0.25, 1.4);
  if (effectTier() >= 2 || topZone) addShake(2.5); else addShake(1);
  if (combo > 0 && combo % 12 === 0) hitStop = Math.max(hitStop, 0.06);
  // fire AoE
  if (ball.type === 'fire') explodeAt(cx, cy, 70, ball);
  if (b.type === 'bomb') explodeAt(cx, cy, 95, ball);
  if (b.type === 'bonus' || Math.random() < (topZone ? 0.16 : 0.09)) dropPickup(cx, cy, b.type === 'bonus' ? undefined : undefined);
  if (b.type === 'bomb') { audio.explosion(false); addShake(9); hitStop = Math.max(hitStop, 0.05); }
}
function explodeAt(x, y, radius, srcBall, depth) {
  depth = depth || 0;
  audio.explosion(true); addShake(8);
  burst(x, y, '#ff7a2f', 30, 420); burst(x, y, '#ffd94f', 20, 320);
  flashRing(x, y, radius);
  const chained = [];
  for (const b of bricks) {
    if (!b.alive || b.type === 'titanium') continue;
    const cx = b.x + b.w / 2, cy = b.y + b.h / 2;
    if (Math.hypot(cx - x, cy - y) < radius + 20) {
      if (b.type === 'phase' && !brickSolidAt(b)) continue;
      const wasBomb = b.type === 'bomb';
      b.alive = false;
      const BI = window.OTT.BRICKS[b.type];
      shards(b.x, b.y, b.w, b.h, BI.color, BI.color2);
      addScore(60, cx, cy, 'BOOM');
      if (wasBomb && depth < 2) chained.push([cx, cy]);
    }
  }
  // chain-detonate with slight delay for a rolling explosion feel
  chained.forEach(([cx, cy], i) => {
    setTimeout(() => { burst(cx, cy, '#ff7a2f', 20, 360); }, 90 * (i + 1));
    if (depth < 2) explodeAt(cx, cy, radius * 0.8, srcBall, depth + 1);
  });
}
function flashRing(x, y, r) { floaters.push({ x, y, r: 8, max: r + 26, age: 0, life: 0.35 }); }

// ---------- update ----------
function update(dt) {
  elapsed += dt;
  if (toastT > 0) { toastT -= dt; if (toastT <= 0) toast.classList.add('hidden'); }
  // timers
  if (paddle.wideT > 0) { paddle.wideT -= dt; if (paddle.wideT <= 0) paddle.w = clamp(W * 0.16, 84, 130); }
  if (catchTimer > 0) catchTimer -= dt;
  if (slowTimer > 0) slowTimer -= dt;
  if (comboTimer > 0) { comboTimer -= dt; if (comboTimer <= 0) { combo = 0; multiplier = 1; } }
  for (const b of bricks) { if (b.shieldT > 0) b.shieldT -= dt; if (b.flash > 0) b.flash -= dt; }

  if (state === S.INTRO) { introTimer -= dt; if (introTimer <= 0) { hideAll(); state = S.SERVE; syncOverlays(); } }
  if (state === S.CLEAR) {
    clearTimer -= dt;
    updateFx(dt);
    if (clearTimer <= 0) {
      levelIndex++;
      if (levelIndex >= window.OTT.LEVELS.length) { winGame(); } else { introLevel(levelIndex); }
    }
    return;
  }
  if (state !== S.PLAY && state !== S.SERVE) { updateFx(dt); return; }

  pollGamepad(dt);
  updatePaddle(dt);
  updateBalls(dt);
  updatePickups(dt);
  updateFx(dt);

  // top-zone detection: any live ball above lowest brick row
  const lowY = lowestBrickBottom();
  const anyTop = balls.some(b => !b.stuck && b.y < lowY - 4);
  if (anyTop && remainingBreakable() > 0) {
    if (!topZone) { topZone = true; showToast('⚡ TOP ZONE! MULTIPLIER RAMPING ⚡', 1500); }
    topZone = true; topZoneTime += dt;
    topZoneMult = Math.min(1 + topZoneTime * 0.35, 5);
    audio.topZoneShimmer();
    bgPulse = Math.min(bgPulse + dt * 2.2, 1.6);
  } else {
    if (topZone && remainingBreakable() > 0) addPopup(W / 2, H * 0.4, 'LEFT TOP ZONE', '#9aa3c7', 13);
    topZone = false; topZoneTime = 0; topZoneMult = 1;
  }
  audio.setIntensity(clamp((multiplier - 1) / 9 + (topZone ? 0.3 : 0), 0, 1));
  updateHUD();

  // level clear?
  if (remainingBreakable() <= 0 && state === S.PLAY) {
    state = S.CLEAR; clearTimer = 2.2;
    audio.fanfare();
    const bonus = 500 * (levelIndex + 1) + lives * 250;
    score += bonus; if (score > highScore) { highScore = score; saveHigh(); }
    $('clear-name').textContent = window.OTT.LEVELS[levelIndex].name + ' CLEARED!';
    $('clear-sub').textContent = `Level bonus +${bonus} · Multiplier peak ×${maxMult}`;
    syncOverlays(); updateHUD();
    for (let i = 0; i < 5; i++) burst(rand(W * 0.2, W * 0.8), rand(H * 0.2, H * 0.5), ['#ffd94f', '#ff4fd8', '#38f8ff'][i % 3], 30, 400);
  }
  // all balls lost?
  if (!balls.length && (state === S.PLAY || state === S.SERVE)) loseLife();
}

function updatePaddle(dt) {
  paddle.px = paddle.x; paddle.py = paddle.y;
  const SPD = 620, SPDY = 420;
  let kvx = 0, kvy = 0;
  if (keys['arrowleft'] || keys['a']) kvx -= 1;
  if (keys['arrowright'] || keys['d']) kvx += 1;
  if (keys['arrowup'] || keys['w']) kvy -= 1;
  if (keys['arrowdown'] || keys['s']) kvy += 1;
  paddle.kvx = kvx; paddle.kvy = kvy;
  if (kvx || kvy) { paddle.tx = null; paddle.ty = null; mouseActive = false; }
  if (kvx) paddle.x = clamp(paddle.x + kvx * SPD * dt, paddle.w / 2, W - paddle.w / 2);
  if (kvy) paddle.y = clamp(paddle.y + kvy * SPDY * dt, H * 0.80, H - 34);
  if (stick.active) {
    paddle.x = clamp(paddle.x + (stick.dx / 55) * SPD * dt, paddle.w / 2, W - paddle.w / 2);
    paddle.y = clamp(paddle.y + (stick.dy / 55) * SPDY * dt, H * 0.80, H - 34);
    paddle.tx = null; paddle.ty = null; mouseActive = false;
  } else if (paddle.tx != null && dragTouch.id !== null) {
    const k = Math.min(1, dt * 18);
    paddle.x += (paddle.tx - paddle.x) * k;
    if (paddle.ty != null) paddle.y += (paddle.ty - paddle.y) * k;
  } else if (mouseActive && !isTouch) {
    const k = Math.min(1, dt * 22);
    paddle.x += (clamp(mouseX, paddle.w / 2, W - paddle.w / 2) - paddle.x) * k;
    const yBand = clamp(mouseY, H * 0.80, H - 34);
    if (Math.abs(mouseY - paddle.y) < H * 0.45) paddle.y += (yBand - paddle.y) * Math.min(1, dt * 10);
  }
  paddle.vx = dt > 0 ? (paddle.x - paddle.px) / dt : 0;
  paddle.vy = dt > 0 ? (paddle.y - paddle.py) / dt : 0;
  paddle.squash *= Math.pow(0.02, dt); paddle.glow *= Math.pow(0.05, dt);
  // stuck balls ride paddle (lean with aim)
  for (const b of balls) if (b.stuck) { b.x = paddle.x + clamp(paddle.vx / 900, -0.6, 0.6) * 24; b.y = paddle.y - paddle.h / 2 - b.r - 2; }
}

function updateBalls(dt) {
  const sub = ballSubSteps();
  const sdt = dt / sub;
  for (let s = 0; s < sub; s++) for (const b of balls) stepBall(b, sdt);
  // remove balls below screen
  for (let i = balls.length - 1; i >= 0; i--) {
    const b = balls[i];
    if (b.y - b.r > H + 20) {
      burst(clamp(b.x, 0, W), H - 10, '#ff4f4f', 14, 260);
      balls.splice(i, 1);
    }
  }
  // trail trim + min speed guard
  for (const b of balls) {
    if (b.stuck) continue;
    b.trail.push({ x: b.x, y: b.y });
    const maxTrail = 6 + effectTier() * 5 + (topZone ? 6 : 0);
    while (b.trail.length > maxTrail) b.trail.shift();
    const sp = ballSpeed(b), minS = slowTimer > 0 ? 300 : 360, maxS = 1150;
    if (sp < minS && sp > 1) setBallSpeed(b, minS);
    if (sp > maxS) setBallSpeed(b, maxS);
    if (Math.abs(b.vy) < sp * 0.12) b.vy += (b.vy >= 0 ? 1 : -1) * sp * 0.35 * dt * 4; // anti-flat-loop
  }
}
function ballSubSteps() {
  let max = 0; for (const b of balls) max = Math.max(max, ballSpeed(b));
  return max > 800 ? 3 : max > 560 ? 2 : 1;
}
function stepBall(b, dt) {
  if (b.stuck) return;
  if (b.hitCd > 0) b.hitCd -= dt;
  b.x += b.vx * dt; b.y += b.vy * dt;
  // walls
  if (b.x - b.r < 0) { b.x = b.r; b.vx = Math.abs(b.vx); audio.wall(); }
  if (b.x + b.r > W) { b.x = W - b.r; b.vx = -Math.abs(b.vx); audio.wall(); }
  if (b.y - b.r < 0) { b.y = b.r; b.vy = Math.abs(b.vy); audio.wall(); }
  // paddle collision (top face priority + sides)
  paddleCollide(b);
  // bricks
  brickCollide(b);
  // drifter motion applied globally in updateFx? handle here per-frame-ish
}
function paddleCollide(b) {
  const pw = paddle.w / 2, ph = paddle.h / 2;
  const dx = b.x - paddle.x, dy = b.y - paddle.y;
  if (Math.abs(dx) > pw + b.r || Math.abs(dy) > ph + b.r) return;
  // catch powerup
  if (catchTimer > 0 && b.vy > 0 && Math.abs(dx) < pw) {
    b.stuck = true; b.vx = 0; b.vy = 0; audio.paddle(false); return;
  }
  const wasAbove = (b.y - b.vy * 0.016) < paddle.y - ph;
  comboTimer = Math.max(comboTimer, 1.2); // paddle save keeps combo breathing
  const upV = -paddle.vy; // positive when paddle flicks up
  const power = upV > 260;
  if (wasAbove && b.vy > 0) {
    const rel = clamp(dx / pw, -1, 1);
    const maxBounce = 1.05;
    const ang = -Math.PI / 2 + rel * maxBounce;
    let sp = ballSpeed(b);
    if (upV > 40) sp += upV * 0.55;          // upward flick accelerates
    else if (paddle.vy > 120) sp *= 0.88;    // downward drift softens
    sp = clamp(sp, 340, 1100);
    if (power) sp = Math.min(sp + 90, 1120);
    b.vx = Math.cos(ang) * sp + paddle.vx * 0.25;
    setBallSpeed(b, sp);
    b.vy = -Math.abs(b.vy);
    b.y = paddle.y - ph - b.r - 1;
    paddle.squash = power ? 1 : 0.55; paddle.glow = power ? 1 : 0.5;
    audio.paddle(power);
    burst(b.x, paddle.y - 6, power ? '#ffd94f' : '#38f8ff', power ? 22 : 10, power ? 380 : 240);
    if (power) {
      addPopup(b.x, b.y - 26, 'POWER HIT!', '#ffd94f', 17);
      addShake(4); hitStop = Math.max(hitStop, 0.03);
      audio.topZoneShimmer();
    }
    if (topZone) topZoneTime += 0.35;
  } else {
    // side/bottom nudge out
    if (Math.abs(dx) > Math.abs(dy)) { b.vx = (dx > 0 ? 1 : -1) * Math.abs(b.vx); b.x = paddle.x + (dx > 0 ? 1 : -1) * (pw + b.r + 1); }
    else { b.vy = (dy > 0 ? 1 : -1) * Math.abs(b.vy); b.y = paddle.y + (dy > 0 ? 1 : -1) * (ph + b.r + 1); }
    audio.paddle(false);
  }
}
function brickCollide(b) {
  for (const br of bricks) {
    if (!br.alive) continue;
    const bx = br.x + (br.driftOff || 0), by = br.y;
    if (b.x + b.r < bx || b.x - b.r > bx + br.w || b.y + b.r < by || b.y - b.r > by + br.h) continue;
    if (br.type === 'phase' && !brickSolidAt(br)) continue; // ghosted: pass through
    // ghost ball: damage & pass through (with cooldown)
    if (b.type === 'ghost' && b.hitCd <= 0) {
      b.hitCd = 0.09;
      tryBreakBrick(br, b, 0, 0);
      burst(b.x, b.y, '#b06bff', 8, 220);
      continue;
    }
    // compute normal
    const cx = clamp(b.x, bx, bx + br.w), cy = clamp(b.y, by, by + br.h);
    let nx = b.x - cx, ny = b.y - cy;
    if (nx === 0 && ny === 0) ny = b.vy > 0 ? -1 : 1;
    const len = Math.hypot(nx, ny) || 1; nx /= len; ny /= len;
    const res = tryBreakBrick(br, b, nx, ny);
    if (res === 'pass') continue;
    // reflect
    if (Math.abs(nx) > Math.abs(ny)) b.vx = (nx > 0 ? Math.abs(b.vx) : -Math.abs(b.vx));
    else b.vy = (ny > 0 ? Math.abs(b.vy) : -Math.abs(b.vy));
    b.x += nx * 2; b.y += ny * 2;
    return; // one brick per substep
  }
}
function updatePickups(dt) {
  for (let i = pickups.length - 1; i >= 0; i--) {
    const p = pickups[i]; p.t += dt; p.y += p.vy * dt; p.vy = Math.min(p.vy + 60 * dt, 240);
    if (p.y > H + 20) { pickups.splice(i, 1); continue; }
    if (Math.abs(p.x - paddle.x) < paddle.w / 2 + 12 && Math.abs(p.y - paddle.y) < 22) {
      applyPickup(p); pickups.splice(i, 1);
    }
  }
}
function updateFx(dt) {
  // drifter slide + phase anim always (even paused? no—only in play-ish states)
  if (state === S.PLAY || state === S.SERVE || state === S.CLEAR) {
    for (const br of bricks) {
      if (!br.alive) continue;
      if (br.type === 'drifter') {
        br.driftOff = (br.driftOff || 0) + br.driftDir * 55 * dt;
        const max = brickW * 1.4;
        if (br.driftOff > max) { br.driftOff = max; br.driftDir = -1; }
        if (br.driftOff < -max) { br.driftOff = -max; br.driftDir = 1; }
      }
    }
  }
  for (let i = particles.length - 1; i >= 0; i--) {
    const p = particles[i]; p.age += dt;
    if (p.age >= p.life) { particles.splice(i, 1); continue; }
    p.vy += 500 * dt; p.vx *= p.drag; p.vy *= p.drag;
    p.x += p.vx * dt; p.y += p.vy * dt;
    if (p.shard) p.rot += p.vr * dt;
  }
  for (let i = popups.length - 1; i >= 0; i--) {
    const p = popups[i]; p.age += dt; p.y -= 44 * dt;
    if (p.age >= p.life) popups.splice(i, 1);
  }
  for (let i = floaters.length - 1; i >= 0; i--) {
    const f = floaters[i]; f.age += dt; f.r += 320 * dt;
    if (f.age >= f.life) floaters.splice(i, 1);
  }
  shake = Math.max(0, shake - dt * 60);
  bgPulse = Math.max(0, bgPulse - dt * 1.6);
  if (hitStop > 0) hitStop -= dt;
}
function loseLife() {
  lives--; audio.loseLife();
  combo = 0; multiplier = 1; topZone = false; topZoneTime = 0; topZoneMult = 1;
  updateHUD();
  if (lives <= 0) { gameOver(); return; }
  slowMo = 1.1; // slow-mo on life lost
  showToast(`BALL LOST — ${lives} ${lives === 1 ? 'LIFE' : 'LIVES'} LEFT`, 1500);
  transformKeepBalls('normal');
  spawnServeBall('normal');
  state = S.SERVE; syncOverlays();
}
function transformKeepBalls(t) { for (const b of balls) { b.type = t; b.r = BALL_DEF[t].r; } }

// ---------- render ----------
function render() {
  ctx.save();
  if (shake > 0.2) ctx.translate(rand(-shake, shake) * 0.5, rand(-shake, shake) * 0.5);
  // background pulse reacting to multiplier/combo
  const tier = effectTier();
  const pulse = bgPulse * 0.5 + (topZone ? 0.25 + 0.15 * Math.sin(elapsed * 8) : 0) + tier * 0.05;
  const g = ctx.createRadialGradient(W / 2, H * 0.35, 40, W / 2, H * 0.5, Math.max(W, H) * 0.8);
  const hot = topZone || tier >= 2;
  g.addColorStop(0, hot ? `rgba(90,20,90,${0.55 + pulse * 0.3})` : 'rgba(26,26,74,0.9)');
  g.addColorStop(0.6, '#0a0a1f'); g.addColorStop(1, '#050510');
  ctx.fillStyle = g; ctx.fillRect(-30, -30, W + 60, H + 60);
  // starfield grid
  ctx.strokeStyle = `rgba(56,248,255,${0.05 + pulse * 0.10})`; ctx.lineWidth = 1;
  const gs = 44;
  ctx.beginPath();
  for (let x = (elapsed * 8) % gs; x < W; x += gs) { ctx.moveTo(x, 0); ctx.lineTo(x, H); }
  for (let y = 0; y < H; y += gs) { ctx.moveTo(0, y); ctx.lineTo(W, y); }
  ctx.stroke();
  // top-zone line
  if (state === S.PLAY || state === S.SERVE) {
    const ly = lowestBrickBottom();
    if (remainingBreakable() > 0) {
      ctx.save();
      ctx.setLineDash([10, 8]);
      ctx.strokeStyle = topZone ? `rgba(255,217,79,${0.7 + 0.3 * Math.sin(elapsed * 8)})` : 'rgba(255,217,79,0.22)';
      ctx.lineWidth = topZone ? 3 : 1.5;
      ctx.shadowColor = '#ffd94f'; ctx.shadowBlur = topZone ? 16 : 4;
      const lx0 = gridX - 6, lx1 = gridX + brickCols * brickW + 6;
      ctx.beginPath(); ctx.moveTo(lx0, ly + 8); ctx.lineTo(lx1, ly + 8); ctx.stroke();
      ctx.setLineDash([]);
      ctx.globalAlpha = 0.8;
      ctx.shadowBlur = 0;
      ctx.fillStyle = '#ffd94f'; ctx.font = '10px sans-serif'; ctx.textAlign = 'right';
      ctx.fillText('▲ TOP ZONE', lx1, ly + 8 - 5);
      ctx.restore();
    }
  }
  drawBricks(); drawPickups(); drawPaddle(); drawBalls(); drawParticles(); drawFloaters(); drawPopups();
  ctx.restore();
}
function brickColor(b) {
  const B = window.OTT.BRICKS[b.type];
  return B;
}
function drawBricks() {
  for (const br of bricks) {
    if (!br.alive) continue;
    const bx = br.x + (br.driftOff || 0), by = br.y;
    const cx = bx + br.w / 2, cy = by + br.h / 2;
    // enter animation
    const age = elapsed - br.born;
    const scaleIn = age < 0 ? 0 : age < 0.3 ? age / 0.3 : 1;
    // phase ghost
    let alpha = 1;
    if (br.type === 'phase') {
      const s = Math.sin(elapsed * 3.2 + br.phase);
      alpha = s > -0.15 ? 1 : 0.22;
    }
    ctx.save();
    ctx.globalAlpha = alpha;
    ctx.translate(cx, cy); ctx.scale(Math.max(scaleIn, 0.01), Math.max(scaleIn, 0.01)); ctx.translate(-cx, -cy);
    const B = brickColor(br);
    const tier = effectTier();
    ctx.shadowColor = B.color; ctx.shadowBlur = 10 + tier * 5 + (br.flash > 0 ? 22 : 0) + (topZone ? 8 : 0);
    const grad = ctx.createLinearGradient(bx, by, bx, by + br.h);
    grad.addColorStop(0, B.color); grad.addColorStop(1, B.color2);
    ctx.fillStyle = grad;
    roundRect(bx, by, br.w, br.h, 5); ctx.fill();
    ctx.shadowBlur = 0;
    ctx.fillStyle = 'rgba(255,255,255,0.28)';
    roundRect(bx + 2, by + 2, br.w - 4, br.h * 0.32, 4); ctx.fill();
    // type glyphs
    ctx.fillStyle = 'rgba(0,0,0,0.55)'; ctx.font = `900 ${Math.max(10, br.h * 0.5)}px sans-serif`;
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    const glyph = { prism: '◣', velocity: '≫', drifter: '⇄', phase: '◌', bomb: '✸', bonus: '★', titanium: '⬢' }[br.type];
    if (glyph) ctx.fillText(glyph, cx, cy + 1);
    // shield flash on resisted bricks
    if (br.shieldT > 0) {
      ctx.globalAlpha = Math.min(1, alpha + 0.0);
      ctx.strokeStyle = `rgba(140,220,255,${0.4 + 0.6 * Math.abs(Math.sin(elapsed * 20))})`;
      ctx.lineWidth = 3; ctx.shadowColor = '#8cdcff'; ctx.shadowBlur = 18;
      roundRect(bx - 2, by - 2, br.w + 4, br.h + 4, 6); ctx.stroke();
      ctx.shadowBlur = 0;
      // hint arrow for prism: show steep arrow
      if (br.type === 'prism') {
        ctx.fillStyle = '#fff'; ctx.font = '900 11px sans-serif';
        ctx.fillText('⭣ steep!', cx, by - 10);
      } else if (br.type === 'velocity') {
        ctx.fillStyle = '#fff'; ctx.font = '900 11px sans-serif';
        ctx.fillText('⚡ faster!', cx, by - 10);
      }
    }
    if (br.type === 'phase' && alpha < 0.5) {
      ctx.globalAlpha = 0.8; ctx.fillStyle = '#ffb36b'; ctx.font = '10px sans-serif';
      ctx.fillText('···', cx, cy);
      ctx.globalAlpha = alpha;
    }
    ctx.restore();
  }
}
function roundRect(x, y, w, h, r) {
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + w, y, x + w, y + h, r); ctx.arcTo(x + w, y + h, x, y + h, r);
  ctx.arcTo(x, y + h, x, y, r); ctx.arcTo(x, y, x + w, y, r);
  ctx.closePath();
}
function drawPickups() {
  for (const p of pickups) {
    ctx.save();
    const bob = Math.sin(p.t * 8) * 2;
    ctx.translate(p.x, p.y + bob);
    ctx.shadowColor = p.color; ctx.shadowBlur = 14;
    ctx.fillStyle = p.color;
    ctx.beginPath(); ctx.arc(0, 0, 10, 0, TAU); ctx.fill();
    ctx.shadowBlur = 0;
    ctx.fillStyle = '#0a0a1a'; ctx.font = '900 11px sans-serif';
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    const icon = { multiball: '×3', fire: '🔥', ghost: '👻', volt: '⚡', big: '●', wide: '⇔', slow: '◷', life: '+1', catch: '🧤' }[p.kind] || '★';
    ctx.fillText(icon, 0, 1);
    ctx.fillStyle = '#fff'; ctx.font = '700 9px sans-serif';
    ctx.shadowColor = p.color; ctx.shadowBlur = 8;
    ctx.fillText(p.label, 0, -18);
    ctx.restore();
  }
}
function drawPaddle() {
  if (state === S.TITLE) return;
  const sq = paddle.squash;
  const pw = paddle.w * (1 + sq * 0.25), ph = paddle.h * (1 - sq * 0.35);
  const spd = Math.hypot(paddle.vx, paddle.vy);
  paddle.glow = Math.max(paddle.glow, clamp(spd / 1400, 0, 0.7));
  const tier = effectTier();
  ctx.save();
  ctx.translate(paddle.x, paddle.y);
  // velocity stretch skew
  const skew = clamp(paddle.vx / 4000, -0.3, 0.3);
  ctx.transform(1, 0, skew, 1, 0, 0);
  ctx.shadowColor = paddle.vy < -260 ? '#ffd94f' : (catchTimer > 0 ? '#ffd94f' : '#38f8ff');
  ctx.shadowBlur = 16 + paddle.glow * 30 + tier * 6;
  const grad = ctx.createLinearGradient(-pw / 2, 0, pw / 2, 0);
  if (paddle.vy < -260) { grad.addColorStop(0, '#ff4fd8'); grad.addColorStop(0.5, '#ffd94f'); grad.addColorStop(1, '#ff4fd8'); }
  else { grad.addColorStop(0, '#1a8fb5'); grad.addColorStop(0.5, '#bffbff'); grad.addColorStop(1, '#ff4fd8'); }
  ctx.fillStyle = grad;
  roundRect(-pw / 2, -ph / 2, pw, ph, ph / 2); ctx.fill();
  ctx.shadowBlur = 0;
  ctx.fillStyle = 'rgba(255,255,255,0.85)';
  roundRect(-pw / 2 + 4, -ph / 2 + 2, pw - 8, 3, 2); ctx.fill();
  // power-hit glow ring
  if (paddle.vy < -260) {
    ctx.strokeStyle = `rgba(255,217,79,${clamp(-paddle.vy / 700, 0.3, 1)})`;
    ctx.lineWidth = 2.5; ctx.shadowColor = '#ffd94f'; ctx.shadowBlur = 14;
    roundRect(-pw / 2 - 4, -ph / 2 - 4, pw + 8, ph + 8, 10); ctx.stroke();
    ctx.shadowBlur = 0;
  }
  if (catchTimer > 0) {
    ctx.fillStyle = '#ffd94f'; ctx.font = '900 10px sans-serif'; ctx.textAlign = 'center';
    ctx.fillText('CATCH ' + Math.ceil(catchTimer) + 's', 0, -ph);
  }
  ctx.restore();
}
function drawBalls() {
  for (const b of balls) {
    const d = BALL_DEF[b.type] || BALL_DEF.normal;
    const tier = effectTier();
    // trail (additive)
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    for (let i = 0; i < b.trail.length; i++) {
      const t = b.trail[i], f = (i + 1) / b.trail.length;
      ctx.globalAlpha = f * (0.38 + tier * 0.08 + (b.type === 'fire' ? 0.25 : 0));
      ctx.fillStyle = d.glow;
      const r = b.r * f * (b.type === 'fire' ? 1.1 : 0.9);
      ctx.beginPath(); ctx.arc(t.x, t.y, Math.max(r, 0.5), 0, TAU); ctx.fill();
    }
    ctx.restore();
    // serve arrow aim
    if (b.stuck) {
      ctx.save();
      ctx.setLineDash([6, 5]);
      ctx.strokeStyle = 'rgba(255,255,255,0.7)'; ctx.lineWidth = 2;
      const aim = clamp(paddle.vx / 900, -0.6, 0.6);
      const a = -Math.PI / 2 + aim;
      ctx.beginPath(); ctx.moveTo(b.x, b.y);
      ctx.lineTo(b.x + Math.cos(a) * 60, b.y + Math.sin(a) * 60); ctx.stroke();
      ctx.setLineDash([]);
      ctx.fillStyle = 'rgba(255,255,255,0.9)'; ctx.font = '12px sans-serif'; ctx.textAlign = 'center';
      ctx.fillText(isTouch ? 'Tap LAUNCH!' : 'Click / Space to launch!', b.x, b.y - 66);
      ctx.restore();
    }
    ctx.save();
    if (b.type === 'ghost') ctx.globalAlpha = 0.65 + 0.3 * Math.sin(elapsed * 10);
    ctx.shadowColor = d.glow; ctx.shadowBlur = 14 + tier * 6 + (topZone ? 10 : 0);
    const g2 = ctx.createRadialGradient(b.x - b.r * 0.3, b.y - b.r * 0.3, 1, b.x, b.y, b.r);
    g2.addColorStop(0, '#ffffff'); g2.addColorStop(0.45, d.color); g2.addColorStop(1, d.glow);
    ctx.fillStyle = g2;
    ctx.beginPath(); ctx.arc(b.x, b.y, b.r, 0, TAU); ctx.fill();
    ctx.shadowBlur = 0;
    if (b.type === 'fire') {
      ctx.fillStyle = 'rgba(255,120,20,0.5)';
      ctx.beginPath(); ctx.arc(b.x + rand(-3, 3), b.y + rand(-3, 3), b.r * 1.5, 0, TAU); ctx.fill();
    }
    if (b.type === 'volt') {
      ctx.strokeStyle = '#fff'; ctx.lineWidth = 1.5;
      ctx.beginPath(); ctx.moveTo(b.x - 3, b.y - 4); ctx.lineTo(b.x + 1, b.y); ctx.lineTo(b.x - 2, b.y + 5); ctx.stroke();
    }
    ctx.restore();
  }
}
function drawParticles() {
  ctx.save();
  ctx.globalCompositeOperation = 'lighter';
  for (const p of particles) {
    const f = 1 - p.age / p.life;
    ctx.globalAlpha = f;
    ctx.fillStyle = p.color;
    if (p.shard) {
      ctx.save(); ctx.translate(p.x, p.y); ctx.rotate(p.rot);
      ctx.fillRect(-p.size / 2, -p.size / 4, p.size, p.size / 2); ctx.restore();
    } else {
      ctx.beginPath(); ctx.arc(p.x, p.y, p.size * f + 0.4, 0, TAU); ctx.fill();
    }
  }
  ctx.restore();
}
function drawFloaters() {
  ctx.save();
  for (const f of floaters) {
    const t = f.age / f.life;
    ctx.globalAlpha = 1 - t;
    ctx.strokeStyle = '#ffd94f'; ctx.lineWidth = 3 * (1 - t) + 1;
    ctx.shadowColor = '#ff7a2f'; ctx.shadowBlur = 16;
    ctx.beginPath(); ctx.arc(f.x, f.y, f.r, 0, TAU); ctx.stroke();
  }
  ctx.restore();
}
function drawPopups() {
  ctx.save(); ctx.textAlign = 'center';
  for (const p of popups) {
    const t = p.age / p.life;
    ctx.globalAlpha = t < 0.15 ? t / 0.15 : 1 - Math.max(0, (t - 0.5) / 0.5);
    ctx.font = `900 ${p.size}px sans-serif`;
    ctx.shadowColor = p.color; ctx.shadowBlur = 12;
    ctx.fillStyle = '#fff';
    ctx.fillText(p.text, p.x, p.y);
  }
  ctx.restore();
}

// ---------- HUD / overlays ----------
function updateHUD() {
  hudScore.textContent = score.toLocaleString();
  hudHigh.textContent = 'BEST ' + highScore.toLocaleString();
  hudLevel.textContent = 'LEVEL ' + (levelIndex + 1) + ' · ' + window.OTT.LEVELS[levelIndex].name;
  hudCombo.textContent = '×' + multiplier + (topZone ? ' + TOP×' + topZoneMult.toFixed(1) : '') + '  (' + combo + ' combo)';
  hudComboFill.style.width = clamp(8 + (multiplier - 1) / 11 * 92, 8, 100) + '%';
  hudLives.textContent = '●'.repeat(Math.max(lives, 0)) + '○'.repeat(Math.max(0, 3 - lives));
  const n = balls.filter(b => !b.stuck).length, s = balls.filter(b => b.stuck).length;
  hudBalls.textContent = s && !n ? 'SERVE!' : (n + s) + (n + s === 1 ? ' BALL' : ' BALLS');
  if (topZone) { tzBanner.classList.remove('hidden'); tzN.textContent = (multiplier * topZoneMult).toFixed(1); }
  else tzBanner.classList.add('hidden');
  const showHint = (state === S.PLAY || state === S.SERVE) && !topZone && remainingBreakable() > 0 && levelIndex === 0;
  topHint.classList.toggle('hidden', !showHint);
}
const OV = ['overlay-title', 'overlay-howto', 'overlay-intro', 'overlay-pause', 'overlay-clear', 'overlay-over', 'overlay-win'];
function hideAll() { for (const id of OV) $(id).classList.add('hidden'); }
function syncOverlays() {
  hideAll();
  hud.classList.toggle('hidden', state === S.TITLE);
  $('corner-btns').classList.toggle('hidden', state === S.TITLE);
  if (state === S.TITLE) { $('overlay-title').classList.remove('hidden'); $('title-high').textContent = 'BEST: ' + highScore.toLocaleString(); hideMobileBtns(); }
  else if (state === S.PAUSE) $('overlay-pause').classList.remove('hidden');
  else if (state === S.OVER) {
    $('overlay-over').classList.remove('hidden');
    $('over-score').textContent = 'SCORE ' + score.toLocaleString();
    const isBest = score >= highScore && score > 0;
    $('over-best').childNodes[0].textContent = 'BEST ' + highScore.toLocaleString() + ' · ';
    $('over-newbest').classList.toggle('hidden', !isBest);
    hideMobileBtns();
  } else if (state === S.WIN) {
    $('overlay-win').classList.remove('hidden');
    $('win-score').textContent = 'SCORE ' + score.toLocaleString();
    $('win-best').textContent = 'BEST ' + highScore.toLocaleString();
    hideMobileBtns();
  } else if (state === S.CLEAR) $('overlay-clear').classList.remove('hidden');
  if ((state === S.PLAY || state === S.SERVE) && isTouch) showMobileBtns(); else if (!isTouch) hideMobileBtns();
  updateMuteBtn();
}
function showMobileBtns() { if (isTouch && state !== S.TITLE) $('mobile-btns').classList.remove('hidden'); }
function hideMobileBtns() { $('mobile-btns').classList.add('hidden'); }

// ---------- flow ----------
function startGame() {
  audio.init(); audio.uiClick();
  levelIndex = 0; score = 0; lives = 3; maxMult = 1;
  introLevel(0);
}
function introLevel(idx) {
  levelIndex = idx;
  loadLevel(idx);
  state = S.INTRO; introTimer = 2.0;
  hideAll();
  $('overlay-intro').classList.remove('hidden');
  $('intro-kicker').textContent = 'LEVEL ' + (idx + 1) + ' / ' + window.OTT.LEVELS.length;
  $('intro-name').textContent = window.OTT.LEVELS[idx].name;
  $('intro-sub').textContent = window.OTT.LEVELS[idx].sub + ' — ' + window.OTT.LEVELS[idx].hint;
  hud.classList.remove('hidden'); $('corner-btns').classList.remove('hidden');
  updateHUD();
}
function gameOver() {
  state = S.OVER;
  if (score > highScore) { highScore = score; saveHigh(); }
  audio.powerdown && audio.powerdown();
  syncOverlays(); updateHUD();
}
function winGame() {
  state = S.WIN;
  if (score > highScore) { highScore = score; saveHigh(); }
  audio.fanfare(); syncOverlays(); updateHUD();
}
function togglePause() {
  if (state === S.PLAY || state === S.SERVE) { prevState = state === S.PLAY ? S.PLAY : S.SERVE; state = S.PAUSE; audio.uiClick(); syncOverlays(); }
  else if (state === S.PAUSE) { state = balls.some(b => b.stuck) ? S.SERVE : S.PLAY; audio.uiClick(); syncOverlays(); }
}
function toggleMute() { audio.init(); const m = audio.toggleMute(); updateMuteBtn(); showToast(m ? '🔇 MUTED' : '🔊 SOUND ON', 900); }
function updateMuteBtn() { $('btn-mute').textContent = audio.muted ? '🔇' : '🔊'; }

// buttons
$('btn-start').addEventListener('click', startGame);
$('btn-howto').addEventListener('click', () => { audio.init(); audio.uiClick(); hideAll(); $('overlay-howto').classList.remove('hidden'); });
$('btn-howto-back').addEventListener('click', () => { audio.uiClick(); syncOverlays(); });
$('btn-help').addEventListener('click', () => { audio.uiClick(); prevState = state; state = S.PAUSE; hideAll(); $('overlay-howto').classList.remove('hidden'); $('btn-howto-back').onclick = () => { $('overlay-howto').classList.add('hidden'); state = prevState === S.TITLE ? S.TITLE : S.PAUSE; if (state === S.PAUSE) togglePause(); else syncOverlays(); $('btn-howto-back').onclick = () => { audio.uiClick(); syncOverlays(); }; }; });
$('btn-resume').addEventListener('click', togglePause);
$('btn-restart2').addEventListener('click', () => { startGame(); });
$('btn-quit').addEventListener('click', () => { state = S.TITLE; syncOverlays(); });
$('btn-retry').addEventListener('click', startGame);
$('btn-again').addEventListener('click', startGame);
$('btn-title2').addEventListener('click', () => { state = S.TITLE; syncOverlays(); });
$('btn-title3').addEventListener('click', () => { state = S.TITLE; syncOverlays(); });
$('btn-pause').addEventListener('click', togglePause);
$('btn-mute').addEventListener('click', toggleMute);
$('btn-launch').addEventListener('click', e => { e.preventDefault(); ensureAudio(); onLaunchPress(); });
document.addEventListener('visibilitychange', () => {
  if (document.hidden && (state === S.PLAY || state === S.SERVE)) togglePause();
});

// ---------- main loop with hit-stop + slow-mo ----------
let last = performance.now();
function frame(now) {
  requestAnimationFrame(frame);
  let raw = Math.min((now - last) / 1000, 0.05);
  last = now;
  if (state === S.PAUSE || state === S.TITLE) { if (state === S.TITLE) { elapsed += raw; renderTitleBg(raw); } return; }
  if (hitStop > 0) { hitStop -= raw; render(); return; } // freeze frames
  slowMo = Math.max(0, slowMo - raw);
  timeScale = slowMo > 0 ? 0.25 : 1;
  const dt = raw * timeScale;
  if (state === S.OVER || state === S.WIN) { updateFx(raw); render(); return; }
  if ($('overlay-howto').classList.contains('hidden') === false && state === S.PAUSE) { render(); return; }
  update(dt);
  render();
}
function renderTitleBg(dt) {
  // gentle animated backdrop behind title
  if (Math.random() < 0.12) burst(rand(0, W), rand(0, H * 0.7), ['#38f8ff', '#ff4fd8', '#ffd94f'][Math.floor(Math.random() * 3)], 2, 120);
  updateFx(dt);
  render();
}

// ---------- init ----------
resize();
paddle.x = W / 2; paddle.y = H - 80;
if (audio.muted) updateMuteBtn();
syncOverlays();
updateMuteBtn();
if (isTouch) showMobileBtns(); else hideMobileBtns();
requestAnimationFrame(frame);
})();





