/* main.js — OVERDRIVE breakout. Vanilla canvas. Part 1: setup, state, levels, input. */
'use strict';
(() => {
const canvas = document.getElementById('game');
const ctx = canvas.getContext('2d');
let W = 0, H = 0, DPR = 1;

function resize() {
  DPR = Math.min(2, window.devicePixelRatio || 1);
  W = window.innerWidth; H = window.innerHeight;
  canvas.width = Math.round(W * DPR); canvas.height = Math.round(H * DPR);
  canvas.style.width = W + 'px'; canvas.style.height = H + 'px';
  ctx.setTransform(DPR, 0, 0, DPR, 0, 0);
  if (G.bricks.length) layoutBricks(false);
  clampPaddle();
}
window.addEventListener('resize', resize);
window.addEventListener('orientationchange', () => setTimeout(resize, 200));

// ---------- State ----------
const G = {
  state: 'menu', level: 0, endless: false,
  score: 0, lives: 3, combo: 0, maxCombo: 0, bestCombo: 0,
  mult: 1, comboMult: 1, frenzyMult: 1,
  frenzy: false, frenzyTime: 0, frenzyGrace: 0, maxFrenzyMult: 1,
  bricks: [], balls: [], powerups: [], bolts: [],
  paddle: new Paddle(),
  time: 0, timeScale: 1, slowT: 0,
  shake: 0, levelScore: 0, bricksLeft: 0,
  brickTop: 0, brickBottom: 0,
  best: +(localStorage.getItem('overdrive_best') || 0),
  flashTop: 0, frenzyTick: 0, won: false,
};
window.G = G; // for debugging / playtest hooks

const BASE_SPEED = 430, MIN_SPEED = 300, MAX_SPEED = 1080, SPEED_NEED = 620;
const DROP_CHANCE = 0.30;

function paddleBand() {
  const top = H - Math.max(120, H * 0.22);
  return { top, bottom: H - 24 };
}
function clampPaddle() {
  const p = G.paddle, b = paddleBand();
  p.x = Math.max(0, Math.min(W - p.w, p.x));
  p.y = Math.max(b.top, Math.min(b.bottom - p.h, p.y || b.top));
}

// ---------- Level build ----------
function buildLevel(idx) {
  const def = Levels.get(idx);
  G.bricks = []; G.balls = []; G.powerups = []; G.bolts = [];
  G.combo = 0; G.frenzy = false; G.frenzyTime = 0; G.frenzyGrace = 0;
  G.frenzyMult = 1; G.comboMult = 1; G.mult = 1; G.maxFrenzyMult = 1;
  G.timeScale = 1; G.slowT = 0; G.levelScore = 0; G.maxCombo = 0;
  const p = G.paddle;
  p.w = p.baseW = Math.max(90, Math.min(150, W * 0.14));
  p.x = W / 2 - p.w / 2;
  const band = paddleBand();
  p.y = band.bottom - p.h - 14; p.px = p.x; p.py = p.y;
  p.wideT = 0; p.laserT = 0; p.stickyT = 0; p.stuckBall = null; p.trail = [];
  layoutBricks(true);
  const b = new Ball(p.cx, p.y - 12, -Math.PI / 2, BASE_SPEED);
  b.stuck = true; G.balls.push(b);
  document.getElementById('hud-level').textContent = 'LEVEL ' + (idx + 1) + (idx >= Levels.count() ? ' ∞' : '');
  const def2 = Levels.get(idx);
  toast(def2.name, 2000);
  setTimeout(() => { if (G.state === 'play') toast(def2.hint, 2600); }, 900);
  updateHUD();
}
function layoutBricks(fresh) {
  const def = Levels.get(G.level);
  const map = def.map, rows = map.length, cols = map[0].length;
  const marginX = Math.max(10, W * 0.03);
  const topOff = Math.max(84, H * 0.13);
  const availW = W - marginX * 2, gap = 5;
  const bw = (availW - gap * (cols - 1)) / cols;
  const bh = Math.max(20, Math.min(34, (H * 0.34) / rows));
  if (!fresh && G.bricks.length) {
    // reflow existing alive bricks, keep type/phase
    let i = 0;
    for (let r = 0; r < rows; r++) for (let c = 0; c < cols; c++) {
      const ch = map[r][c];
      if (ch === '.') continue;
      const br = G.bricks[i++];
      if (!br) break;
      br.x = marginX + c * (bw + gap); br.y = topOff + r * (bh + gap);
      br.w = bw; br.h = bh; br.ox = br.x;
      br.range = Math.min(bw * 0.45, 40);
    }
  } else {
    G.bricks = [];
    for (let r = 0; r < rows; r++) for (let c = 0; c < cols; c++) {
      const ch = map[r][c];
      if (ch === '.') continue;
      const color = BRICK_COLORS[r % BRICK_COLORS.length];
      const br = new Brick(c, r, ch === 'n' ? 'n' : ch,
        marginX + c * (bw + gap), topOff + r * (bh + gap), bw, bh, color);
      br.range = Math.min(bw * 0.45, 40);
      G.bricks.push(br);
    }
  }
  refreshField();
}
function refreshField() {
  const alive = G.bricks.filter(b => !b.dead);
  G.bricksLeft = alive.length;
  if (alive.length) {
    G.brickTop = Math.min(...alive.map(b => b.y));
    G.brickBottom = Math.max(...alive.map(b => b.y + b.h));
  } else { G.brickTop = 0; G.brickBottom = 0; }
}

// ---------- HUD / screens ----------
const $ = id => document.getElementById(id);
function updateHUD() {
  $('hud-score').textContent = G.score.toLocaleString();
  $('hud-best').textContent = Math.max(G.best, G.score).toLocaleString();
  $('hud-combo').textContent = G.combo;
  const m = $('hud-mult');
  m.textContent = '×' + G.mult.toFixed(1);
  m.classList.toggle('frenzy', G.frenzy);
  $('hud-lives').textContent = '●'.repeat(Math.max(0, G.lives)) + '○'.repeat(Math.max(0, 3 - G.lives));
  $('frenzy-bar').style.width = G.frenzy ? Math.min(100, (G.frenzyTime / 12) * 100) + '%' : '0%';
  $('frenzy-banner').classList.toggle('hidden', !G.frenzy);
  if (G.frenzy) $('frenzy-banner').textContent = `⚡ OVERDRIVE ×${G.mult.toFixed(1)} ⚡`;
}
function toast(msg, ms = 1400) {
  const w = $('toast-wrap');
  const d = document.createElement('div');
  d.className = 'toast'; d.textContent = msg;
  w.appendChild(d);
  while (w.children.length > 3) w.removeChild(w.firstChild);
  setTimeout(() => d.remove(), ms + 200);
}
function show(id) { $(id).classList.remove('hidden'); }
function hide(id) { $(id).classList.add('hidden'); }
function hideAllScreens() { ['screen-menu', 'screen-howto', 'screen-pause', 'screen-clear', 'screen-over', 'screen-win'].forEach(hide); }
function setState(s) {
  G.state = s;
  hideAllScreens();
  $('hud').classList.toggle('hidden', s === 'menu' || s === 'howto');
  $('touch-ui').classList.toggle('hidden', !(s === 'play' && isTouch));
  $('btn-pause-d').classList.toggle('hidden', !(s === 'play' && !isTouch));
  if (s === 'menu') { show('screen-menu'); $('menu-best').textContent = Math.max(G.best, G.score).toLocaleString(); AudioSys.stopMusic(); }
  if (s === 'howto') show('screen-howto');
  if (s === 'pause') show('screen-pause');
  if (s === 'clear') { show('screen-clear'); }
  if (s === 'over') { show('screen-over'); }
  if (s === 'win') { show('screen-win'); }
}

// ---------- Input ----------
const keys = {};
let mouseActive = false, mouseX = 0, mouseY = 0;
let isTouch = ('ontouchstart' in window) || navigator.maxTouchPoints > 0;
window.addEventListener('keydown', e => {
  if (['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', ' '].includes(e.key)) e.preventDefault();
  keys[e.key.toLowerCase()] = true;
  AudioSys.unlock();
  if (e.key === ' ' || e.key === 'Enter') {
    if (G.state === 'menu') startGame(false);
    else if (G.state === 'play') { releaseStuck(); fireLasers(); }
    else if (G.state === 'clear') nextLevel();
  }
  if (e.key.toLowerCase() === 'p' || e.key === 'Escape') {
    if (G.state === 'play') pauseGame();
    else if (G.state === 'pause') resumeGame();
  }
});
window.addEventListener('keyup', e => { keys[e.key.toLowerCase()] = false; });
canvas.addEventListener('mousemove', e => { mouseActive = true; mouseX = e.clientX; mouseY = e.clientY; });
canvas.addEventListener('mousedown', e => {
  AudioSys.unlock();
  if (G.state !== 'play') return;
  mouseX = e.clientX; mouseY = e.clientY;
  releaseStuck(); fireLasers();
});
canvas.addEventListener('touchstart', e => { e.preventDefault(); AudioSys.unlock(); }, { passive: false });
document.addEventListener('touchmove', e => { if (G.state === 'play') e.preventDefault(); }, { passive: false });
document.addEventListener('dblclick', e => e.preventDefault());

// Touch: thumbstick (left zone) + direct drag near paddle
const stick = { active: false, id: -1, ax: 0, ay: 0, dx: 0, dy: 0 };
const dragP = { active: false, id: -1, ox: 0, oy: 0, px: 0, py: 0 };
let optDrag = true, optStick = true;
$('opt-drag').addEventListener('change', e => optDrag = e.target.checked);
$('opt-stick').addEventListener('change', e => optStick = e.target.checked);
const stickZone = $('stick-zone'), stickBase = $('stick-base'), stickNub = $('stick-nub');
function touchPos(t) { return { x: t.clientX, y: t.clientY }; }
stickZone.addEventListener('touchstart', e => {
  e.preventDefault(); AudioSys.unlock();
  if (!optStick || G.state !== 'play') return;
  const t = e.changedTouches[0];
  stick.active = true; stick.id = t.identifier;
  stick.ax = t.clientX; stick.ay = t.clientY; stick.dx = 0; stick.dy = 0;
  stickBase.style.display = 'block';
  stickBase.style.left = t.clientX + 'px'; stickBase.style.top = t.clientY + 'px';
  stickNub.style.transform = 'translate(-50%,-50%)';
}, { passive: false });
canvas.addEventListener('touchstart', e => {
  e.preventDefault(); AudioSys.unlock();
  if (G.state !== 'play') return;
  for (const t of e.changedTouches) {
    const p = touchPos(t), pd = G.paddle;
    const near = Math.abs(p.x - pd.cx) < 130 && Math.abs(p.y - pd.y) < 130;
    if (optDrag && (near || t.clientX > W * 0.45)) {
      if (dragP.active) continue;
      dragP.active = true; dragP.id = t.identifier;
      dragP.ox = p.x; dragP.oy = p.y; dragP.px = pd.x; dragP.py = pd.y;
    }
  }
}, { passive: false });
window.addEventListener('touchmove', e => {
  for (const t of e.changedTouches) {
    if (stick.active && t.identifier === stick.id) {
      stick.dx = t.clientX - stick.ax; stick.dy = t.clientY - stick.ay;
      const max = 60, len = Math.hypot(stick.dx, stick.dy);
      let nx = stick.dx, ny = stick.dy;
      if (len > max) { nx = stick.dx / len * max; ny = stick.dy / len * max; }
      stickNub.style.transform = `translate(calc(-50% + ${nx}px), calc(-50% + ${ny}px))`;
    }
    if (dragP.active && t.identifier === dragP.id) {
      const p = touchPos(t), pd = G.paddle;
      pd.x = dragP.px + (p.x - dragP.ox) * 1.15;
      pd.y = dragP.py + (p.y - dragP.oy) * 1.15;
      clampPaddle();
    }
  }
  if (G.state === 'play') e.preventDefault();
}, { passive: false });
function endTouch(e) {
  for (const t of e.changedTouches) {
    if (stick.active && t.identifier === stick.id) {
      stick.active = false; stick.dx = 0; stick.dy = 0;
      stickBase.style.display = 'none';
    }
    if (dragP.active && t.identifier === dragP.id) dragP.active = false;
  }
}
window.addEventListener('touchend', endTouch);
window.addEventListener('touchcancel', endTouch);

// ---------- Game flow ----------
function startGame(endless) {
  AudioSys.unlock(); AudioSys.startMusic();
  G.level = 0; G.endless = !!endless; G.score = 0; G.lives = 3; G.bestCombo = 0; G.won = false;
  buildLevel(0); setState('play');
}
function nextLevel() {
  G.level++;
  if (!G.endless && G.level >= Levels.count()) {
    // finished level 5 -> victory screen, endless unlocked
    G.won = true;
    $('win-score').textContent = G.score.toLocaleString();
    saveBest(); setState('win'); AudioSys.levelClear();
    return;
  }
  buildLevel(G.level); setState('play'); AudioSys.startMusic();
}
function retry() {
  G.score = Math.max(0, G.score - G.levelScore);
  G.lives = 3; buildLevel(G.level); setState('play'); AudioSys.startMusic();
}
function pauseGame() { if (G.state === 'play') { setState('pause'); } }
function resumeGame() { if (G.state === 'pause') setState('play'); }
function saveBest() {
  if (G.score > G.best) {
    G.best = G.score; localStorage.setItem('overdrive_best', String(G.best));
    return true;
  }
  return false;
}
function gameOver(win) {
  AudioSys.stopMusic(); AudioSys.gameOver();
  saveBest();
  $('over-title').textContent = win ? 'YOU WIN!' : 'GAME OVER';
  $('over-sub').textContent = win ? 'Endless conquered?' : `You reached level ${G.level + 1}.`;
  $('over-score').textContent = G.score.toLocaleString();
  $('over-stats').textContent = `Best combo ${G.bestCombo} · Max OVERDRIVE ×${G.maxFrenzyMult.toFixed(1)}`;
  $('over-newbest').classList.toggle('hidden', !(G.score >= G.best && G.score > 0));
  setState('over');
}
function levelClear() {
  AudioSys.levelClear(); AudioSys.stopMusic();
  FX.ring(W / 2, H / 2, '#ffe45d', 200);
  for (let i = 0; i < 5; i++) FX.burst(W / 2 + (Math.random() - 0.5) * 300, H / 2, BRICK_COLORS[i % 6], 24, 420);
  saveBest();
  $('clear-title').textContent = `LEVEL ${G.level + 1} CLEAR!`;
  $('clear-score').textContent = G.score.toLocaleString();
  $('clear-stats').textContent = `Max OVERDRIVE ×${G.maxFrenzyMult.toFixed(1)} · Best combo ${G.maxCombo}`;
  if (!G.endless && G.level + 1 >= Levels.count()) { nextLevel(); return; } // -> win screen
  setState('clear');
}

function releaseStuck() {
  let did = false;
  for (const b of G.balls) {
    if (b.stuck) {
      b.stuck = false;
      const a = -Math.PI / 2 + (Math.random() - 0.5) * 0.5;
      b.dx = Math.cos(a); b.dy = Math.sin(a);
      b.speed = Math.max(b.speed, BASE_SPEED);
      did = true;
    }
  }
  if (G.paddle.stuckBall) { G.paddle.stuckBall = null; }
  if (did) AudioSys.launch();
}
function fireLasers() {
  const p = G.paddle;
  if (p.laserT <= 0 || p.cool > 0 || G.state !== 'play') return;
  p.cool = 0.22;
  G.bolts.push(new Bolt(p.x + 8, p.y - 14), new Bolt(p.x + p.w - 8, p.y - 14));
  AudioSys.laser(); FX.addShake(2);
}

// ---------- Scoring / powerups ----------
function addScore(base, x, y, color = '#ffe45d') {
  const pts = Math.round(base * G.mult);
  G.score += pts; G.levelScore += pts;
  FX.floatText(x, y, '+' + pts, color, Math.min(30, 15 + Math.log2(G.mult + 1) * 5));
  FX.addPulse(0.12);
  return pts;
}
function bumpCombo(x, y) {
  G.combo++; G.maxCombo = Math.max(G.maxCombo, G.combo);
  G.bestCombo = Math.max(G.bestCombo, G.combo);
  if (G.combo > 0 && G.combo % 10 === 0) {
    toast(`COMBO ×${G.combo}!`, 1200);
    AudioSys.powerup();
  }
}
function dropPowerup(x, y) {
  if (Math.random() > DROP_CHANCE) return;
  const bag = ['M', 'F', 'L', 'B', 'S', 'Z', 'T', 'W', 'M', 'F', 'W', 'L'];
  let kind = bag[(Math.random() * bag.length) | 0];
  if (Math.random() < 0.05) kind = 'E';
  G.powerups.push(new Powerup(x, y, kind));
}
function applyPowerup(kind, x, y) {
  const p = G.paddle;
  AudioSys.powerup();
  FX.ring(x, y, POWERS[kind].color, 46);
  FX.burst(x, y, POWERS[kind].color, 16, 260);
  toast(POWERS[kind].name + '!', 1100);
  const first = G.balls[0];
  switch (kind) {
    case 'M': {
      const src = G.balls.filter(b => !b.stuck);
      const baseBalls = (src.length ? src : G.balls).slice(0, 4);
      for (const s of baseBalls) {
        if (G.balls.length >= 8) break;
        for (const da of [-0.45, 0.45]) {
          if (G.balls.length >= 8) break;
          const nb = new Ball(s.x, s.y, Math.atan2(s.dy, s.dx) + da, s.speed);
          nb.stuck = false; nb.fireT = s.fireT; nb.lightT = s.lightT; nb.bigT = s.bigT;
          G.balls.push(nb);
        }
      }
      break;
    }
    case 'F': for (const b of G.balls) { b.fireT = 7; b.stuck = false; } break;
    case 'L': for (const b of G.balls) { b.lightT = 7; b.speed = Math.min(MAX_SPEED, b.speed + 220); b.stuck = false; } break;
    case 'B': for (const b of G.balls) b.bigT = 10; break;
    case 'S': p.stickyT = 14; break;
    case 'Z': p.laserT = 9; break;
    case 'T': G.slowT = 6; break;
    case 'W': p.w = Math.min(W * 0.32, p.baseW * 1.7); p.wideT = 14; p.x = Math.min(W - p.w, p.x); break;
    case 'E': G.lives = Math.min(5, G.lives + 1); addScore(100, x, y, '#ff9dcf'); break;
  }
  addScore(25, x, y, POWERS[kind].color);
  updateHUD();
}

// ---------- Update ----------
function updatePaddle(dt) {
  const p = G.paddle, band = paddleBand();
  const kbd = 620;
  if (keys['arrowleft'] || keys['a']) p.x -= kbd * dt;
  if (keys['arrowright'] || keys['d']) p.x += kbd * dt;
  if (keys['arrowup'] || keys['w']) p.y -= kbd * dt * 0.8;
  if (keys['arrowdown'] || keys['s']) p.y += kbd * dt * 0.8;
  if (mouseActive && !dragP.active && !isTouch) {
    const tx = mouseX - p.w / 2, ty = mouseY - p.h / 2;
    p.x += (tx - p.x) * Math.min(1, dt * 18);
    p.y += (ty - p.y) * Math.min(1, dt * 14);
  }
  if (stick.active) {
    p.x += stick.dx * dt * 14;
    p.y += stick.dy * dt * 14;
    if (Math.abs(stick.dx) > 4 || Math.abs(stick.dy) > 4)
      FX.trail(p.cx, p.y + p.h / 2, '#00f0ff', 3, 0.25);
  }
  clampPaddle();
  p.update(dt);
  // stuck balls ride the paddle
  for (const b of G.balls) if (b.stuck) { b.x = p.cx; b.y = p.y - b.effR() - 3; }
  if (p.stuckBall && !G.balls.includes(p.stuckBall)) p.stuckBall = null;
}

function bouncePaddle(b) {
  const p = G.paddle;
  const rel = Math.max(-1, Math.min(1, (b.x - p.cx) / (p.w / 2)));
  const maxA = 1.05; // ~60°
  const ang = -Math.PI / 2 + rel * maxA + Math.max(-0.25, Math.min(0.25, p.vx * 0.0004));
  let sp = b.speed + 6; // gradual ramp
  let bonus = false;
  if (p.vy < -120) { // upward flick boost!
    const boost = Math.min(260, -p.vy * 0.35);
    sp += boost; bonus = true;
    FX.ring(b.x, b.y, '#ffe45d', 50);
    FX.burst(b.x, b.y, '#ffe45d', 14, 300);
    addScore(15, b.x, b.y - 20, '#ffe45d');
  }
  if (b.lightT > 0) sp += 40;
  b.speed = Math.max(MIN_SPEED, Math.min(MAX_SPEED, sp));
  b.dx = Math.cos(ang); b.dy = Math.sin(ang);
  if (Math.abs(b.dy) < 0.25) b.dy = -0.25; // avoid flat locks
  const n = Math.hypot(b.dx, b.dy); b.dx /= n; b.dy /= n;
  b.y = p.y - b.effR() - 1;
  AudioSys.paddle(G.mult);
  FX.sparks(b.x, b.y, bonus ? '#ffe45d' : '#00f0ff', bonus ? 14 : 7);
  FX.addShake(bonus ? 5 : 2); FX.addPulse(bonus ? 0.25 : 0.08);
  if (p.stickyT > 0 && !p.stuckBall) {
    b.stuck = true; p.stuckBall = b;
    toast('STICKY — click to launch!', 1000);
    return;
  }
  // reset combo forgiveness: paddle save keeps combo
}

function collideBricks(b) {
  const r = b.effR();
  for (const br of G.bricks) {
    if (br.dead) continue;
    if (br.type === 'H' && !br.solid) continue; // ghost pass-through
    const nx = Math.max(br.x, Math.min(b.x, br.x + br.w));
    const ny = Math.max(br.y, Math.min(b.y, br.y + br.h));
    const dx = b.x - nx, dy = b.y - ny;
    if (dx * dx + dy * dy > r * r) continue;
    // HIT — check special conditions
    const res = br.tryHit(b);
    if (!res.destroy) {
      reflectFromBrick(b, br);
      br.flash = 0.4; br.hitAnim = 0.25;
      AudioSys.brickDenied();
      const msgs = { angle: 'NEED STEEP ANGLE ⇕', speed: 'NEED MORE SPEED ≫', shield: 'HIT FROM BELOW ▼', ghost: '' };
      FX.sparks(b.x, b.y, '#8892aa', 6);
      if (msgs[res.deny]) FX.floatText(br.cx, br.y - 8, msgs[res.deny], '#ff8ba0', 14);
      return;
    }
    // DESTROY
    br.dead = true;
    bumpCombo(br.cx, br.cy);
    const pts = addScore(50, br.cx, br.cy, br.color);
    AudioSys.brick(G.mult, G.combo);
    FX.burst(br.cx, br.cy, br.color, G.frenzy ? 26 : 16, G.frenzy ? 420 : 300);
    FX.ring(br.cx, br.cy, br.color, 40);
    FX.addShake(G.frenzy ? 5 : 3); FX.addPulse(0.15);
    dropPowerup(br.cx, br.cy);
    if (b.fireT > 0) {
      // pierce: no bounce, brief immunity handled by moving through
      FX.trail(b.x, b.y, '#ff7a00', 10, 0.4);
    } else {
      reflectFromBrick(b, br);
    }
    if (b.bigT > 0) FX.addShake(4);
    refreshField();
    if (G.bricksLeft <= 0) { levelClear(); return; }
    if (!G.frenzy) updateHUD();
    return; // one brick per ball per frame
  }
}
function reflectFromBrick(b, br) {
  const r = b.effR();
  const prevX = b.x - b.dx * b.speed * 0.016, prevY = b.y - b.dy * b.speed * 0.016;
  const fromLeft = prevX + r <= br.x, fromRight = prevX - r >= br.x + br.w;
  const fromTop = prevY + r <= br.y, fromBottom = prevY - r >= br.y + br.h;
  if ((fromLeft && !fromRight) || (fromRight && !fromLeft)) b.dx *= -1;
  else if ((fromTop && !fromBottom) || (fromBottom && !fromTop)) b.dy *= -1;
  else b.dy *= -1;
  // nudge out
  b.x += b.dx * 2; b.y += b.dy * 2;
}

function updateBalls(dt) {
  const p = G.paddle;
  for (const b of G.balls) {
    if (b.gone) continue;
    if (b.stuck) continue;
    // speed ramp + lightning decay
    b.speed = Math.min(MAX_SPEED, b.speed + dt * 5);
    b.speed = Math.max(MIN_SPEED, b.speed);
    b.update(dt);
    // trails: color by state, glow scales with mult
    const col = b.fireT > 0 ? '#ff7a00' : b.lightT > 0 ? '#ffe45d' : '#7dffff';
    FX.trail(b.x, b.y, col, 4 + Math.min(6, G.mult * 0.4), 0.35);
    // walls
    const r = b.effR();
    if (b.x - r < 0) { b.x = r; b.dx = Math.abs(b.dx); AudioSys.wall(G.mult); FX.sparks(0, b.y, '#00f0ff', 5); }
    if (b.x + r > W) { b.x = W - r; b.dx = -Math.abs(b.dx); AudioSys.wall(G.mult); FX.sparks(W, b.y, '#00f0ff', 5); }
    if (b.y - r < 0) { b.y = r; b.dy = Math.abs(b.dy); AudioSys.wall(G.mult); FX.sparks(b.x, 0, '#00f0ff', 5); }
    // paddle
    if (b.dy > 0 && b.y + r >= p.y && b.y - r <= p.y + p.h && b.x >= p.x - r && b.x <= p.x + p.w + r) {
      bouncePaddle(b);
    }
    // bricks
    if (b.y < G.brickBottom + 60) collideBricks(b);
    if (G.state !== 'play') return;
    // bolts vs bricks handled separately; lasers ignore deny (below)
    // bottom = lost
    if (b.y - r > H + 10) { b.gone = true; }
  }
  // remove lost
  const before = G.balls.length;
  G.balls = G.balls.filter(b => !b.gone);
  if (G.balls.length < before) {
    FX.addShake(6);
    if (G.balls.length === 0) {
      G.lives--; G.combo = 0; G.frenzy = false; G.frenzyTime = 0; G.frenzyGrace = 0; G.mult = 1;
      AudioSys.loseLife(); FX.burst(W / 2, H - 60, '#ff5d7a', 30, 380);
      updateHUD();
      if (G.lives <= 0) { gameOver(false); return; }
      toast(`BALL LOST — ${G.lives} LEFT`, 1500);
      const nb = new Ball(p.cx, p.y - 12, -Math.PI / 2, BASE_SPEED);
      nb.stuck = true; G.balls.push(nb);
    }
  }
}

function updateFrenzy(dt) {
  const anyAbove = G.bricksLeft > 0 && G.balls.some(b => !b.stuck && b.y < G.brickTop - 4);
  if (anyAbove) {
    if (!G.frenzy) {
      G.frenzy = true; G.frenzyTime = 0;
      AudioSys.frenzyOn(); FX.addShake(8); FX.addPulse(0.6);
      toast('⚡ OVERDRIVE! STAY ON TOP! ⚡', 1600);
      FX.ring(W / 2, G.brickTop, '#00f0ff', 120);
    }
    G.frenzyGrace = 1.6; G.flashTop = 0.5;
  } else if (G.frenzy) {
    G.frenzyGrace -= dt;
    if (G.frenzyGrace <= 0) {
      G.frenzy = false;
      if (G.frenzyTime > 3) toast(`OVERDRIVE ×${G.mult.toFixed(1)} BANKED!`, 1400);
    }
  }
  if (G.frenzy) {
    G.frenzyTime += dt;
    G.frenzyMult = Math.min(32, Math.pow(2, G.frenzyTime / 7));
    G.maxFrenzyMult = Math.max(G.maxFrenzyMult, G.frenzyMult);
    // trickle bonus
    G.frenzyTick += dt;
    if (G.frenzyTick > 0.5) {
      G.frenzyTick = 0;
      G.score += Math.round(5 * G.mult); G.levelScore += Math.round(5 * G.mult);
    }
    // ambient juice while on top
    if (Math.random() < dt * 12) {
      const b = G.balls.find(b2 => !b2.stuck);
      if (b) FX.sparks(b.x, b.y, '#b14dff', 2);
    }
    if (G.flashTop > 0) G.flashTop -= dt;
  } else {
    G.frenzyMult = 1;
  }
  G.comboMult = 1 + Math.min(G.combo, 60) * 0.08;
  G.mult = G.comboMult * G.frenzyMult;
  AudioSys.setIntensity(Math.min(1, (Math.log2(G.mult + 1) / 6) + (G.balls.length > 1 ? 0.15 : 0)));
}

function updateDrops(dt) {
  const p = G.paddle;
  for (const d of G.powerups) {
    d.update(dt);
    if (!d.gone && d.y + d.h / 2 >= p.y && d.y - d.h / 2 <= p.y + p.h && d.x >= p.x - 10 && d.x <= p.x + p.w + 10) {
      d.gone = true; applyPowerup(d.kind, d.x, d.y);
    }
    if (d.y > H + 30) d.gone = true;
  }
  G.powerups = G.powerups.filter(d => !d.gone);
  for (const bl of G.bolts) {
    bl.update(dt);
    // laser bolts destroy ANY brick ignoring deny rules
    for (const br of G.bricks) {
      if (br.dead) continue;
      if (br.type === 'H' && !br.solid) continue;
      if (bl.x > br.x - 3 && bl.x < br.x + br.w + 3 && bl.y > br.y - 8 && bl.y < br.y + br.h + 8) {
        br.dead = true; bl.gone = true;
        bumpCombo(br.cx, br.cy); addScore(50, br.cx, br.cy, br.color);
        AudioSys.brick(G.mult, G.combo);
        FX.burst(br.cx, br.cy, '#ff5d7a', 14, 300);
        refreshField();
        if (G.bricksLeft <= 0) { levelClear(); return; }
        break;
      }
    }
  }
  G.bolts = G.bolts.filter(b => !b.gone);
  if (G.slowT > 0) { G.slowT -= dt; G.timeScale = 0.45; }
  else G.timeScale = 1;
}

// ---------- Render ----------
let stars = [];
function initStars() {
  stars = [];
  for (let i = 0; i < 90; i++) stars.push({ x: Math.random(), y: Math.random(), s: Math.random() * 2 + 0.5, tw: Math.random() * 6 });
}
function drawBG(t) {
  const g = ctx.createLinearGradient(0, 0, 0, H);
  if (G.frenzy) {
    const hue = (t * 60) % 360;
    g.addColorStop(0, `hsl(${280 + Math.sin(t * 3) * 30},60%,14%)`);
    g.addColorStop(1, '#070714');
  } else {
    g.addColorStop(0, '#141432'); g.addColorStop(1, '#070714');
  }
  ctx.fillStyle = g; ctx.fillRect(0, 0, W, H);
  // stars
  ctx.save();
  for (const s of stars) {
    const a = 0.3 + 0.4 * Math.abs(Math.sin(t * 1.5 + s.tw)) + FX.pulse * 0.3;
    ctx.globalAlpha = Math.min(1, a);
    ctx.fillStyle = G.frenzy ? '#7dffff' : '#8ea2c9';
    ctx.fillRect(s.x * W, s.y * H, s.s, s.s);
  }
  ctx.restore();
  // top-zone indicator
  if (G.state === 'play' && G.bricksLeft > 0) {
    ctx.save();
    const pulse = 0.5 + 0.5 * Math.sin(t * (G.frenzy ? 10 : 3));
    ctx.strokeStyle = G.frenzy ? `rgba(0,240,255,${0.7 + pulse * 0.3})` : `rgba(177,77,255,${0.25 + pulse * 0.2})`;
    ctx.lineWidth = G.frenzy ? 3 : 2;
    if (G.frenzy) { ctx.shadowBlur = 18; ctx.shadowColor = '#00f0ff'; }
    ctx.setLineDash([12, 8]);
    ctx.beginPath(); ctx.moveTo(8, G.brickTop - 8); ctx.lineTo(W - 8, G.brickTop - 8); ctx.stroke();
    ctx.setLineDash([]);
    ctx.font = '700 11px system-ui,sans-serif'; ctx.textAlign = 'center';
    ctx.fillStyle = G.frenzy ? '#7dffff' : 'rgba(211,166,255,.7)';
    ctx.fillText(G.frenzy ? '⚡ OVERDRIVE ZONE — STAY UP! ⚡' : '▲ TOP ZONE: GET ABOVE THE BRICKS ▲', W / 2, Math.max(56, G.brickTop - 16));
    ctx.restore();
  }
  // paddle movement band hint
  if (G.state === 'play') {
    const band = paddleBand();
    ctx.save();
    ctx.fillStyle = 'rgba(0,240,255,0.04)';
    ctx.fillRect(0, band.top, W, H - band.top);
    ctx.strokeStyle = 'rgba(0,240,255,0.15)'; ctx.setLineDash([6, 8]);
    ctx.beginPath(); ctx.moveTo(0, band.top); ctx.lineTo(W, band.top); ctx.stroke();
    ctx.restore();
  }
}

function drawBricks(t) {
  for (const br of G.bricks) {
    if (br.dead) continue;
    const ghost = br.type === 'H' && !br.solid;
    ctx.save();
    ctx.globalAlpha = ghost ? 0.22 + 0.1 * Math.sin(t * 6) : 1;
    const breathe = 1 + (br.hitAnim > 0 ? br.hitAnim * 1.6 : 0);
    const bw = br.w * breathe, bh = br.h * breathe;
    const bx = br.cx - bw / 2, by = br.cy - bh / 2;
    // body
    const g = ctx.createLinearGradient(bx, by, bx, by + bh);
    g.addColorStop(0, br.color); g.addColorStop(1, 'rgba(0,0,0,.55)');
    ctx.fillStyle = g;
    if (br.flash > 0) { ctx.shadowBlur = 22; ctx.shadowColor = '#fff'; }
    else { ctx.shadowBlur = ghost ? 0 : 8; ctx.shadowColor = br.color; }
    const rad = 6;
    ctx.beginPath();
    ctx.roundRect ? ctx.roundRect(bx, by, bw, bh, rad) : ctx.rect(bx, by, bw, bh);
    ctx.fill();
    ctx.shadowBlur = 0;
    ctx.strokeStyle = 'rgba(255,255,255,.35)'; ctx.lineWidth = 1; ctx.stroke();
    if (br.flash > 0) { ctx.fillStyle = `rgba(255,255,255,${Math.min(0.8, br.flash * 2)})`; ctx.fill(); }
    // telegraphs
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    const cx = br.cx, cy = br.cy;
    if (br.type === 'G') {
      ctx.fillStyle = '#0a0a18'; ctx.font = `900 ${Math.min(20, bh)}px system-ui,sans-serif`;
      ctx.fillText('⇕', cx, cy - 1);
      ctx.strokeStyle = '#ffe45d'; ctx.lineWidth = 2;
      ctx.strokeRect(bx + 1.5, by + 1.5, bw - 3, bh - 3);
      // steep arrow above
      ctx.fillStyle = '#ffe45d'; ctx.font = '900 10px system-ui,sans-serif';
      ctx.fillText('STEEP!', cx, by - 7 > 60 ? by - 7 : cy);
    } else if (br.type === 'V') {
      // speedometer: border color by fastest ball speed
      const sp = Math.max(...G.balls.map(b => b.speed), 0);
      const ok = sp >= SPEED_NEED;
      ctx.strokeStyle = ok ? '#7dff9a' : '#ff5d7a'; ctx.lineWidth = 2.5;
      ctx.strokeRect(bx + 1.5, by + 1.5, bw - 3, bh - 3);
      ctx.fillStyle = '#0a0a18'; ctx.font = `900 ${Math.min(19, bh)}px system-ui,sans-serif`;
      ctx.fillText('≫', cx, cy);
      // mini speed bar
      const k = Math.min(1, sp / SPEED_NEED);
      ctx.fillStyle = 'rgba(0,0,0,.5)'; ctx.fillRect(bx + 4, by + bh - 6, bw - 8, 3);
      ctx.fillStyle = ok ? '#7dff9a' : '#ff5d7a'; ctx.fillRect(bx + 4, by + bh - 6, (bw - 8) * k, 3);
    } else if (br.type === 'H') {
      ctx.fillStyle = ghost ? 'rgba(255,255,255,.6)' : '#0a0a18';
      ctx.font = `900 ${Math.min(18, bh)}px system-ui,sans-serif`;
      ctx.fillText(ghost ? '◌' : '⬢', cx, cy);
      if (!ghost) { ctx.strokeStyle = '#b14dff'; ctx.lineWidth = 2; ctx.setLineDash([4, 3]); ctx.strokeRect(bx + 1.5, by + 1.5, bw - 3, bh - 3); ctx.setLineDash([]); }
    } else if (br.type === 'M') {
      ctx.fillStyle = '#0a0a18'; ctx.font = `900 ${Math.min(18, bh)}px system-ui,sans-serif`;
      ctx.fillText('↔', cx, cy);
      ctx.strokeStyle = '#00f0ff'; ctx.lineWidth = 2;
      ctx.strokeRect(bx + 1.5, by + 1.5, bw - 3, bh - 3);
    } else if (br.type === 'S') {
      // shield on top+left+right, open bottom
      ctx.strokeStyle = '#7dff9a'; ctx.lineWidth = 3;
      ctx.beginPath();
      ctx.moveTo(bx, by + bh); ctx.lineTo(bx, by + 4); ctx.quadraticCurveTo(bx, by, bx + 4, by);
      ctx.lineTo(bx + bw - 4, by); ctx.quadraticCurveTo(bx + bw, by, bx + bw, by + 4);
      ctx.lineTo(bx + bw, by + bh); ctx.stroke();
      ctx.fillStyle = '#0a0a18'; ctx.font = `900 ${Math.min(17, bh)}px system-ui,sans-serif`;
      ctx.fillText('▼', cx, cy);
    }
    ctx.restore();
  }
}

function drawPaddle(t) {
  const p = G.paddle;
  ctx.save();
  // motion trail
  for (const tr of p.trail) {
    const k = 1 - tr.age / 0.5;
    if (k <= 0) continue;
    ctx.globalAlpha = k * 0.25;
    ctx.fillStyle = '#00f0ff';
    ctx.fillRect(tr.x - p.w / 2, tr.y, p.w, p.h);
  }
  ctx.globalAlpha = 1;
  const stretch = Math.max(-3, Math.min(3, p.vy * 0.004));
  const flick = p.vy < -120;
  ctx.shadowBlur = flick ? 26 : 14; ctx.shadowColor = flick ? '#ffe45d' : '#00f0ff';
  const g = ctx.createLinearGradient(0, p.y, 0, p.y + p.h);
  if (p.laserT > 0) { g.addColorStop(0, '#ff5d7a'); g.addColorStop(1, '#7a1030'); }
  else if (p.stickyT > 0) { g.addColorStop(0, '#d3a6ff'); g.addColorStop(1, '#4d1a99'); }
  else if (flick) { g.addColorStop(0, '#fff3b0'); g.addColorStop(1, '#ff9d00'); }
  else { g.addColorStop(0, '#9ff'); g.addColorStop(1, '#0077aa'); }
  ctx.fillStyle = g;
  ctx.beginPath();
  ctx.roundRect ? ctx.roundRect(p.x, p.y + stretch * 0.4, p.w, p.h, 8) : ctx.rect(p.x, p.y, p.w, p.h);
  ctx.fill();
  ctx.shadowBlur = 0;
  ctx.strokeStyle = 'rgba(255,255,255,.6)'; ctx.lineWidth = 1.5; ctx.stroke();
  // laser cannons
  if (p.laserT > 0) {
    ctx.fillStyle = '#ff5d7a'; ctx.shadowBlur = 10; ctx.shadowColor = '#ff5d7a';
    ctx.fillRect(p.x + 4, p.y - 10, 8, 12); ctx.fillRect(p.x + p.w - 12, p.y - 10, 8, 12);
    ctx.shadowBlur = 0;
    ctx.fillStyle = '#fff'; ctx.font = '900 10px system-ui,sans-serif'; ctx.textAlign = 'center';
    ctx.fillText('Z ' + Math.ceil(p.laserT) + 's', p.cx, p.y - 14);
  }
  if (p.stickyT > 0) {
    ctx.fillStyle = '#d3a6ff'; ctx.font = '900 10px system-ui,sans-serif'; ctx.textAlign = 'center';
    ctx.fillText('STICKY ' + Math.ceil(p.stickyT) + 's', p.cx, p.y - 14);
  }
  ctx.restore();
}

function drawBalls(t) {
  for (const b of G.balls) {
    const r = b.effR();
    ctx.save();
    const col = b.fireT > 0 ? '#ff7a00' : b.lightT > 0 ? '#ffe45d' : '#7dffff';
    const glow = 10 + Math.min(40, G.mult * 2.5) + (b.fireT > 0 ? 14 : 0);
    ctx.shadowBlur = glow; ctx.shadowColor = col;
    const g = ctx.createRadialGradient(b.x - r * 0.3, b.y - r * 0.3, 1, b.x, b.y, r);
    g.addColorStop(0, '#fff'); g.addColorStop(0.45, col); g.addColorStop(1, 'rgba(0,0,0,.4)');
    ctx.fillStyle = g;
    ctx.beginPath(); ctx.arc(b.x, b.y, r, 0, 7); ctx.fill();
    ctx.shadowBlur = 0;
    ctx.strokeStyle = 'rgba(255,255,255,.8)'; ctx.lineWidth = 1.5;
    ctx.beginPath(); ctx.arc(b.x, b.y, r, b.spin, b.spin + 4); ctx.stroke();
    if (b.fireT > 0) {
      ctx.strokeStyle = '#ffce00'; ctx.lineWidth = 2.5;
      ctx.beginPath(); ctx.arc(b.x, b.y, r + 4 + Math.sin(t * 20) * 2, 0, 7); ctx.stroke();
    }
    if (b.stuck) {
      ctx.fillStyle = 'rgba(255,255,255,.85)'; ctx.font = '700 12px system-ui,sans-serif'; ctx.textAlign = 'center';
      ctx.fillText('CLICK / TAP TO LAUNCH', b.x, b.y - r - 12);
    }
    ctx.restore();
  }
}

function drawDrops(t) {
  ctx.save(); ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
  for (const d of G.powerups) {
    const info = POWERS[d.kind];
    ctx.shadowBlur = 12; ctx.shadowColor = info.color;
    ctx.fillStyle = 'rgba(8,8,24,.92)';
    ctx.strokeStyle = info.color; ctx.lineWidth = 2;
    ctx.beginPath();
    ctx.roundRect ? ctx.roundRect(d.x - 23, d.y - 12, 46, 24, 8) : ctx.rect(d.x - 23, d.y - 12, 46, 24);
    ctx.fill(); ctx.stroke();
    ctx.shadowBlur = 0;
    ctx.fillStyle = info.color; ctx.font = '900 13px system-ui,sans-serif';
    ctx.fillText(d.kind === 'E' ? '+1' : d.kind, d.x, d.y + 1);
  }
  ctx.fillStyle = '#ff8ba0'; ctx.shadowBlur = 12; ctx.shadowColor = '#ff5d7a';
  for (const bl of G.bolts) ctx.fillRect(bl.x - 2, bl.y - 8, 5, 16);
  ctx.restore();
}

// ---------- Main loop ----------
let last = performance.now();
function frame(now) {
  requestAnimationFrame(frame);
  let dt = (now - last) / 1000; last = now;
  if (dt > 0.033) dt = 0.033; // clamp
  if (dt <= 0) return;
  G.time += dt;
  const t = G.time;
  if (G.state === 'play') {
    const sdt = dt * G.timeScale;
    updatePaddle(dt);
    for (const br of G.bricks) br.update(sdt, t, W);
    updateBalls(sdt);
    if (G.state === 'play') { updateDrops(sdt); updateFrenzy(sdt); }
    FX.update(dt);
    if (Math.floor(t * 60) % 20 === 0) updateHUD();
  } else {
    FX.update(dt);
    for (const br of G.bricks) br.update(dt, t, W);
  }
  // render
  ctx.save();
  ctx.translate(FX.shakeX, FX.shakeY);
  drawBG(t);
  if (G.state !== 'menu' || G.bricks.length) drawBricks(t);
  if (G.state === 'play' || G.state === 'pause') { drawPaddle(t); drawBalls(t); drawDrops(t); }
  FX.drawParts(ctx);
  FX.drawTexts(ctx);
  ctx.restore();
  // slow-mo vignette
  if (G.slowT > 0 && G.state === 'play') {
    ctx.save(); ctx.strokeStyle = 'rgba(125,255,255,.5)'; ctx.lineWidth = 10;
    ctx.strokeRect(5, 5, W - 10, H - 10); ctx.restore();
  }
  if (G.frenzy && G.state === 'play') {
    ctx.save(); ctx.strokeStyle = `rgba(0,240,255,${0.4 + 0.3 * Math.sin(t * 8)})`;
    ctx.lineWidth = 4; ctx.shadowBlur = 20; ctx.shadowColor = '#00f0ff';
    ctx.strokeRect(3, 3, W - 6, H - 6); ctx.restore();
  }
}

// ---------- Wiring ----------
$('btn-start').addEventListener('click', () => startGame(false));
$('btn-howto').addEventListener('click', () => setState('howto'));
$('btn-howto-back').addEventListener('click', () => startGame(false));
$('btn-resume').addEventListener('click', resumeGame);
$('btn-restart2').addEventListener('click', () => { buildLevel(G.level); setState('play'); });
$('btn-quit').addEventListener('click', () => setState('menu'));
$('btn-next').addEventListener('click', nextLevel);
$('btn-retry').addEventListener('click', () => { G.score = 0; G.lives = 3; buildLevel(G.level); setState('play'); AudioSys.startMusic(); });
$('btn-menu2').addEventListener('click', () => setState('menu'));
$('btn-menu3').addEventListener('click', () => setState('menu'));
$('btn-endless').addEventListener('click', () => { G.endless = true; G.level = Levels.count(); G.lives = 3; buildLevel(G.level); setState('play'); AudioSys.startMusic(); });
$('btn-launch-t').addEventListener('click', e => { e.preventDefault(); releaseStuck(); fireLasers(); });
$('btn-pause-t').addEventListener('click', e => { e.preventDefault(); pauseGame(); });
$('btn-pause-d').addEventListener('click', () => pauseGame());
document.addEventListener('visibilitychange', () => { if (document.hidden) pauseGame(); });
document.addEventListener('pointerdown', AudioSys.unlock, { once: true });

initStars();
resize();
G.paddle.x = W / 2 - 60; G.paddle.y = H - 60;
setState('menu');
requestAnimationFrame(frame);
// Debug / verification hooks (harmless in production)
window.__game = { G, startGame, buildLevel, applyPowerup, releaseStuck, nextLevel, retry };
try {
  const q = new URLSearchParams(location.search);
  if (q.has('play')) {
    const lv = Math.max(0, Math.min(8, parseInt(q.get('level') || '0', 10)));
    G.level = lv; buildLevel(lv); setState('play'); AudioSys.startMusic();
    if (q.has('launch')) releaseStuck();
  }
} catch (e) { /* ignore */ }
})();


