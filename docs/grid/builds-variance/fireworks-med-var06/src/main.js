// ===== BREAKAWAY — main entry: loop, input, render, test hook ================
import * as G from './game.js';
import { parts, drops, stats, POWER_DUR, bumpErrors } from './game.js';

import { CanvasMgr, roundedRect, hsl } from './canvas.js';
import { LEVELS, POWER_TYPES, CALLS, CALLOUT_TIERS } from './game.js';
import { TYPES } from './bricks.js';

const canvas = document.getElementById('game');
const cm = new CanvasMgr(canvas);
G.setCanvas(cm);
window.addEventListener('resize', () => { cm.resize(); G.layout(); });
window.addEventListener('orientationchange', () => setTimeout(() => { cm.resize(); G.layout(); }, 120));

const isTest = new URLSearchParams(location.search).has('test');
let errorCount = 0;
let aiBrickTarget = null;
const testFired = { launch: false };
let simTime = 0;

// ---------- helpers ----------------------------------------------------------
function newGame() {
  stateRef().lives = 3;
  stateRef().score = 0;
  const c = comboRef();
  c.mult = 1; c.maxMult = 1; c.streak = 0; c.timer = 0; c.upTop = false; c.totalUpTop = 0; c.bestUpTop = 0;
  startLevelAt(0);
}

function startLevelAt(i) {
  G.startLevel(i);
  stateRef().mode = 'play';
}

function stateRef() { return G.state; }
function comboRef() { return G.combo; }

// ---------- input ------------------------------------------------------------
const keys = {};
window.addEventListener('keydown', e => {
  keys[e.code] = true;
  if (e.code === 'KeyM') toggleMute();
  if (e.code === 'KeyP' || e.code === 'Escape') togglePause();
  if (e.code === 'Space') { e.preventDefault(); action(); }
  audioRef().ensure(); audioRef().resume();
});
window.addEventListener('keyup', e => keys[e.code] = false);

function audioRef() { return G.getAudio(); }

// pointer: mouse move steers paddle absolutely; click = action
// touch: relative drag anywhere; swipe up fast = boost happens via vy naturally; tap = action
const touch = { active: false, id: null, lastX: 0, lastY: 0, vx: 0, vy: 0, tapT: 0 };
canvas.addEventListener('pointerdown', e => {
  audioRef().ensure(); audioRef().resume();
  if (e.pointerType === 'touch') {
    if (!touch.active) { touch.active = true; touch.id = e.pointerId; touch.lastX = e.clientX; touch.lastY = e.clientY; }
  } else {
    mouse.x = e.clientX; mouse.y = e.clientY; mouse.active = true;
    action();
  }
});
canvas.addEventListener('pointermove', e => {
  if (e.pointerType === 'touch') {
    if (touch.active && e.pointerId === touch.id) {
      const g = G.getGame();
      const dx = e.clientX - touch.lastX, dy = e.clientY - touch.lastY;
      touch.lastX = e.clientX; touch.lastY = e.clientY;
      const p = G.paddle;
      p.targetX += dx * 1.6;
      p.targetY += dy * 1.6;
      clampPaddleTarget();
    }
  } else {
    mouse.x = e.clientX; mouse.y = e.clientY; mouse.active = true;
  }
});
const endTouch = e => {
  if (e.pointerType === 'touch') { if (e.pointerId === touch.id) touch.active = false; }
};
canvas.addEventListener('pointerup', endTouch);
canvas.addEventListener('pointercancel', endTouch);

const mouse = { x: 0, y: 0, lastX: -1, lastY: -1, active: false };

function clampPaddleTarget() {
  const g = G.getGame();
  const p = G.paddle;
  const half = p.w / 2 + 4;
  p.targetX = Math.max(g.fx + half, Math.min(g.fx + g.fw - half, p.targetX));
  p.targetY = Math.max(g.padMinY, Math.min(g.padMaxY, p.targetY));
}

function action() {
  const s = G.state;
  if (s.mode === 'title') { newGame(); return; }
  if (s.mode === 'gameover') { newGame(); return; }
  if (s.mode === 'levelclear') { startLevelAt(s.level + 1); return; }
  if (s.paused) { togglePause(); return; }
  // release magnet or serve
  const serve = G.getServe();
  if (serve.ball) {
    if (G.paddle.magnet === serve.ball && G.effects.magnet > 0) {
      G.paddle.magnet = null;
      G.launchBall(serve.ball);
    } else if (!serve.ball.stuck) { /* noop */ }
    else G.launchBall(serve.ball);
  }
}

function togglePause() {
  const s = G.state;
  if (s.mode !== 'play' && s.mode !== 'dying') return;
  s.paused = !s.paused;
  document.getElementById('btnPause').textContent = s.paused ? '▶' : '⏸';
}
function toggleMute() {
  const a = audioRef();
  a.setEnabled(!a.enabled);
  document.getElementById('btnMute').textContent = a.enabled ? '🔊' : '🔇';
}

document.getElementById('btnPause').addEventListener('click', () => { audioRef().ensure(); togglePause(); });
document.getElementById('btnMute').addEventListener('click', () => { audioRef().ensure(); toggleMute(); });
document.getElementById('btnGear').addEventListener('click', () =>
  document.getElementById('settings').classList.toggle('open'));
document.getElementById('setClose').addEventListener('click', () =>
  document.getElementById('settings').classList.remove('open'));
document.getElementById('setSound').addEventListener('change', e => { audioRef().setEnabled(e.target.checked); });
document.getElementById('setStick').addEventListener('change', e => { stateRef().thumbstick = e.target.checked; });
document.getElementById('setShake').addEventListener('change', e => { cm.shakeEnabled = e.target.checked; });

document.addEventListener('visibilitychange', () => {
  if (document.hidden && G.state.mode === 'play' && !G.state.paused) togglePause();
});

// ---------- thumbstick (optional, bottom-left) --------------------------------
const stick = { active: false, id: null, cx: 0, cy: 0, dx: 0, dy: 0 };
function stickArea() { return { x: 0, y: cm.h - cm.w * 0.45, w: cm.w * 0.45, h: cm.w * 0.45 }; }
canvas.addEventListener('touchstart', e => {
  if (!G.state.thumbstick) return;
  const a = stickArea();
  for (const t of e.changedTouches) {
    if (!stick.active && t.clientX < a.w && t.clientY > a.y) {
      stick.active = true; stick.id = t.identifier;
      stick.cx = t.clientX; stick.cy = t.clientY; stick.dx = 0; stick.dy = 0;
      e.preventDefault();
    }
  }
}, { passive: false });
canvas.addEventListener('touchmove', e => {
  if (!stick.active) return;
  for (const t of e.changedTouches) {
    if (t.identifier === stick.id) {
      stick.dx = Math.max(-1, Math.min(1, (t.clientX - stick.cx) / 60));
      stick.dy = Math.max(-1, Math.min(1, (t.clientY - stick.cy) / 60));
      e.preventDefault();
    }
  }
}, { passive: false });
const stickEnd = e => {
  for (const t of e.changedTouches) if (t.identifier === stick.id) { stick.active = false; stick.dx = 0; stick.dy = 0; }
};
canvas.addEventListener('touchend', stickEnd);
canvas.addEventListener('touchcancel', stickEnd);

// ---------- update -------------------------------------------------------------
function updatePaddle(dt) {
  const g = G.getGame(), p = G.paddle, s = G.state;
  const kbSpeed = 720;
  let kb = 0, kbv = 0;
  if (keys.ArrowLeft || keys.KeyA) kb -= 1;
  if (keys.ArrowRight || keys.KeyD) kb += 1;
  if (keys.ArrowUp || keys.KeyW) kbv -= 1;
  if (keys.ArrowDown || keys.KeyS) kbv += 1;

  if (kb || kbv) { p.targetX += kb * kbSpeed * dt; p.targetY += kbv * kbSpeed * dt; clampPaddleTarget(); mouse.active = false; }

  if (s.thumbstick && stick.active) {
    p.targetX += stick.dx * kbSpeed * dt * 1.2;
    p.targetY += stick.dy * kbSpeed * dt * 1.2;
    clampPaddleTarget();
  } else if (mouse.active && !s.thumbstick && !touch.active) {
    // map mouse to paddle band (only when the mouse actually moved)
    if (mouse.x !== mouse.lastX || mouse.y !== mouse.lastY) {
      mouse.lastX = mouse.x; mouse.lastY = mouse.y;
      p.targetX = mouse.x;
      p.targetY = Math.max(g.padMinY, Math.min(g.padMaxY, mouse.y * 0.25 + g.padMaxY * 0.75));
    }
  }

  const oldX = p.x, oldY = p.y;
  p.x += (p.targetX - p.x) * Math.min(1, dt * 16);
  p.y += (p.targetY - p.y) * Math.min(1, dt * 12);
  p.vx = (p.x - oldX) / dt;
  p.vy = (p.y - oldY) / dt;

  p.squash = Math.max(0, p.squash - dt * 6);
  p.boost = Math.max(0, p.boost - dt * 4);
  // thrust particles when boosting upward fast
  if (p.vy < -260 && !G.state.reducedMotion && Math.random() < 0.5) {
    parts.spawn(p.x + (Math.random() - 0.5) * p.w, p.y + p.h, (Math.random() - 0.5) * 60, 120 + Math.random() * 100, 0.3, 3, '#7ee8ff', { shape: 'circle', grav: 200 });
  }
}



// ---------- combo / up-top logic -----------------------------------------------
function updateCombo(dt) {
  const g = G.getGame(), c = G.combo, s = G.state;
  if (s.mode !== 'play') return;
  const sy = G.summitY();
  let anyUp = false, upCount = 0;
  for (const b of G.balls) if (!b.stuck && !b.dead && b.y - b.r > sy + G.getGame().bh) { anyUp = true; upCount++; }
  if (anyUp) {
    if (!c.upTop) {
      c.upTop = true;
      c.mult = Math.max(c.mult, 2);
      showCallout(0);
      G.getCanvas().addFlash(0.3, '120,255,200');
    }
    c.timer += dt * Math.max(1, upCount * 0.7);
    c.totalUpTop += dt;
    // multiplier climbs with time up top: x2 at start, +1 each 2.2s
    const newMult = Math.min(12, 2 + Math.floor(c.timer / 2.2));
    if (newMult > c.mult) {
      c.mult = newMult;
      const tier = CALLOUT_TIERS.findIndex(t => c.mult < t);
      showCallout(tier < 0 ? CALLS.length - 1 : Math.max(0, tier - 1));
      audioRef().multUp(c.mult);
      G.getCanvas().addFlash(0.18, '255,240,140');
      G.getCanvas().addChroma(0.3);
    }
    c.maxMult = Math.max(c.maxMult, c.mult);
  } else {
    if (c.upTop) {
      c.upTop = false;
      c.bestUpTop = Math.max(c.bestUpTop, c.timer);
    }
    // decay multiplier when ball back below
    c.timer = Math.max(0, c.timer - dt * 1.5);
    const decayed = Math.min(12, 2 + Math.floor(c.timer / 2.2));
    if (decayed < c.mult) c.mult = Math.max(1, c.mult - dt * 1.2);
    if (c.mult < 1.01) c.mult = 1;
  }
  // music intensity from multiplier
  audioRef().intensity = Math.min(1, (c.mult - 1) / 7);
  // escalate visuals
  const glow = Math.min(1, (c.mult - 1) / 8);
  G.getCanvas().starSpeed = 1 + glow * 3;
  if (c.mult >= 3 && !G.state.reducedMotion && Math.random() < dt * c.mult * 2) {
    parts.spawn(Math.random() * cm.w, -10, (Math.random() - 0.5) * 40, 120 + Math.random() * 160, 1.4, 3 + Math.random() * 3,
      hsl(50 + c.mult * 18, 95, 65), { shape: 'circle', grav: 60 });
  }
  // big mult glow overlay
  if (c.mult >= 2) {
    G.getCanvas().addChroma(dt * 0.05 * c.mult);
  }
}

function showCallout(i) {
  const c = G.combo;
  c.callout = CALLS[Math.min(i, CALLS.length - 1)];
  c.calloutT = 1.4;
}

// ---------- drops ----------------------------------------------------------------
function updateDrops(dt) {
  const g = G.getGame(), p = G.paddle;
  for (let i = drops.length - 1; i >= 0; i--) {
    const d = drops[i];
    d.t += dt;
    d.y += d.vy * dt;
    d.vy += 60 * dt;
    if (d.y > g.fy + g.fh + 30) { drops.splice(i, 1); continue; }
    if (Math.abs(d.x - p.x) < p.w / 2 + 12 && Math.abs(d.y - p.y) < 16) {
      G.catchDrop(d);
      drops.splice(i, 1);
    }
  }
}

// ---------- ball housekeeping ---------------------------------------------------
function updateBalls(dt) {
  const g = G.getGame(), s = G.state, c = G.combo;
  for (const b of G.balls) {
    if (b.fire > 0) b.fire -= dt;
    if (b.heavy > 0) b.heavy -= dt;
    if (b.ghost > 0) b.ghost -= dt;
    // expire specials -> revert
    if (b.special === 'fire' && b.fire <= 0) { b.special = null; b.hue = 190; }
    if (b.special === 'heavy' && b.heavy <= 0) { b.special = null; b.hue = 190; }
    if (b.special === 'ghost' && b.ghost <= 0) { b.special = null; b.hue = 190; }
    // slow-mo time dilation per-ball via global effects.slow handled outside
    G.stepBall(b, dt);
    // trail particles
    if (!s.reducedMotion && !b.stuck && Math.random() < 0.4) {
      parts.spawn(b.x, b.y, (Math.random() - 0.5) * 20, (Math.random() - 0.5) * 20, 0.25, 2.5,
        b.fire ? '#ff9e40' : `hsl(${b.hue},90%,70%)`, { shape: 'circle' });
    }
  }
  // remove dead balls
  const before = G.balls.length;
  for (let i = G.balls.length - 1; i >= 0; i--) if (G.balls[i].dead) G.balls.splice(i, 1);
  if (before > 0 && G.balls.length === 0 && s.mode === 'play') {
    G.loseLife();
  }
}

// ---------- level clear / dying --------------------------------------------------
function checkLevelClear(dt) {
  const g = G.getGame(), s = G.state;
  if (s.mode !== 'play') return;
  const remaining = G.getBricks().filter(b => !b.dead && b.type !== 'solid').length;
  if (remaining === 0) {
    s.mode = 'levelclear';
    G.setLevelClearT(0);
    audioRef().levelClear();
    // confetti
    if (!s.reducedMotion) {
      for (let i = 0; i < 90; i++) {
        parts.spawn(Math.random() * cm.w, -10, (Math.random() - 0.5) * 80, 60 + Math.random() * 180, 1.8, 3 + Math.random() * 4,
          hsl(Math.random() * 360, 90, 65), { grav: 40, spin: 6 });
      }
    }
    const c = G.combo;
    s.best = Math.max(s.best, s.score);
    localStorage.setItem('breakaway_best', s.best);
  }
}

// ---------- test AI ---------------------------------------------------------------
function testAI(dt) {
  const g = G.getGame(), p = G.paddle, s = G.state;
  let predX = null, bestT = Infinity, predBall = null;
  for (const b of G.balls) {
    if (b.stuck) continue;
    if (b.vy <= 0) continue;
    let t = (p.y - b.y) / b.vy;
    if (t < 0 || t > 4) continue;
    let px = b.x + b.vx * t;
    const fw = g.fw;
    let rel = (px - g.fx) % (2 * fw);
    if (rel < 0) rel += 2 * fw;
    px = rel < fw ? g.fx + rel : g.fx + 2 * fw - rel;
    if (t < bestT) { bestT = t; predX = px; predBall = b; }
  }
  let aimX = predX !== null ? predX : g.fx + g.fw / 2;
  // aggression: bias the contact point off-center so the ball flies steeper angles
  if (predBall && predX !== null) aimX = predX + (Math.sin(predBall.x * 0.01 + predBall.y * 0.003) > 0 ? 32 : -32);
  if (predBall && predX !== null) {
    if (!aiBrickTarget || aiBrickTarget.dead || Math.random() < 0.02) {
      let best = null, bd = Infinity;
      for (const br of G.getBricks()) {
        if (br.dead || br.type === 'solid') continue;
        const d = Math.abs(br.x + br.w / 2 - predX) + (br.baseRow) * 40;
        if (d < bd) { bd = d; best = br; }
      }
      aiBrickTarget = best;
    }
    if (aiBrickTarget) {
      const bcx = aiBrickTarget.x + aiBrickTarget.w / 2, bcy = aiBrickTarget.y + aiBrickTarget.h / 2;
      const outAng = Math.atan2(bcy - predBall.y, bcx - predBall.x);
      let off = ((outAng + Math.PI / 2) / (Math.PI / 3)) * 1.15;
      off += (predBall.vx / Math.max(80, Math.abs(predBall.vy))) * 0.5;
      off = Math.max(-0.85, Math.min(0.85, off));
      aimX = predX - off * (p.w / 2) * 0.9;
    }
  }
  p.targetX = aimX + Math.sin(simTime * 0.9) * 3;
  // hover above rest: paddle.vy < 0 at contact adds speed (and looks like skill)
  const hoverT = simTime % 2.2;
  p.targetY = g.padMaxY - (hoverT < 0.5 ? (0.5 - hoverT) * 90 : 0);
  clampPaddleTarget();
  const serve = G.getServe();
  if (serve.ball) G.launchBall(serve.ball);
}

// ---------- render ---------------------------------------------------------------
function drawBrick(b) {
  const ctx = cm.ctx;
  const g = G.getGame();
  let color, hi;
  if (b.type === 'reg') { [color, hi] = [['#4fc3f7', '#b3e5fc'], ['#7ce87c', '#d0f8d0'], ['#ffd54f', '#fff0b3'], ['#ff6e9c', '#ffd0e0']][b.tier]; }
  else if (b.type === 'angle') { color = '#ffb74d'; hi = '#ffe0b2'; }
  else if (b.type === 'speed') { color = '#69f0ae'; hi = '#c8ffd8'; }
  else if (b.type === 'move') { color = '#b388ff'; hi = '#e5d4ff'; }
  else if (b.type === 'phase') {
    const ph = ((G.state.time + b.phaseOff) % b.phasePeriod) / b.phasePeriod;
    const vuln = ph < 0.55;
    // telegraph: solid color when vulnerable, outline when not
    if (vuln) { color = '#ff80ab'; hi = '#ffd0e0'; }
    else { color = 'rgba(255,128,171,0.13)'; hi = 'rgba(255,180,200,0.35)'; }
    if (ph > 0.42 && ph < 0.55) { color = '#ff80ab'; } // warning blink
  }
  else if (b.type === 'solid') { color = '#37474f'; hi = '#78909c'; }

  ctx.save();
  if (b.flashT > 0) { ctx.translate(1, 0); }
  // glow
  ctx.shadowColor = color; ctx.shadowBlur = 7;
  ctx.fillStyle = color;
  roundedRect(ctx, b.x, b.y, b.w, b.h, 4);
  ctx.fill();
  ctx.shadowBlur = 0;
  // top highlight
  ctx.fillStyle = hi;
  ctx.globalAlpha = 0.5;
  ctx.fillRect(b.x + 3, b.y + 2, b.w - 6, 3);
  ctx.globalAlpha = 1;
  ctx.restore();

  // type indicators
  if (b.type === 'angle') drawAngleIndicator(b, ctx);
  if (b.type === 'speed') drawSpeedIndicator(b, ctx);
  if (b.clankT > 0) {
    const ctx2 = cm.ctx;
    ctx2.save();
    ctx2.globalAlpha = b.clankT;
    ctx2.strokeStyle = '#ffe082';
    ctx2.lineWidth = 2;
    ctx2.strokeRect(b.x - 3, b.y - 3, b.w + 6, b.h + 6);
    ctx2.restore();
  }
}

function drawAngleIndicator(b, ctx) {
  // arrow showing required incoming angle
  const cx = b.x + b.w / 2, cy = b.y + b.h / 2;
  const a = (b.angleCenter * Math.PI) / 180;
  ctx.save();
  ctx.translate(cx, cy); ctx.rotate(a);
  ctx.strokeStyle = 'rgba(30,20,0,.75)';
  ctx.lineWidth = 2;
  ctx.beginPath(); ctx.moveTo(-b.w * 0.3, 0); ctx.lineTo(b.w * 0.3, 0);
  ctx.lineTo(b.w * 0.3 - 5, -4); ctx.moveTo(b.w * 0.3, 0); ctx.lineTo(b.w * 0.3 - 5, 4);
  ctx.stroke();
  ctx.restore();
}

function drawSpeedIndicator(b, ctx) {
  // speed streaks (needs-speed look)
  ctx.save();
  ctx.strokeStyle = 'rgba(0,40,20,.8)';
  ctx.lineWidth = 1.6;
  for (let i = -1; i <= 1; i++) {
    ctx.beginPath();
    ctx.moveTo(b.x + b.w * 0.3, b.y + b.h / 2 + i * 4);
    ctx.lineTo(b.x + b.w * 0.7, b.y + b.h / 2 + i * 4);
    ctx.stroke();
  }
  ctx.restore();
}

function drawPaddle() {
  const ctx = cm.ctx, p = G.paddle;
  const g = G.getGame();
  const sq = p.squash * 0.35;
  const w = p.w * (1 + sq * 0.5), h = p.h * (1 - sq * 0.3);
  const mag = G.effects.magnet > 0;
  ctx.save();
  ctx.shadowColor = mag ? '#b388ff' : '#7ee8ff'; ctx.shadowBlur = 14;
  const grad = ctx.createLinearGradient(p.x - w / 2, p.y - h, p.x + w / 2, p.y);
  grad.addColorStop(0, mag ? '#b388ff' : '#3fd0ff');
  grad.addColorStop(1, mag ? '#7c4dff' : '#19b8e6');
  ctx.fillStyle = grad;
  roundedRect(ctx, p.x - w / 2, p.y - h / 2, w, h, h / 2);
  ctx.fill();
  // magnet field arcs
  if (mag) {
    ctx.strokeStyle = 'rgba(179,136,255,0.5)';
    ctx.lineWidth = 1.5;
    for (let i = 1; i <= 2; i++) {
      ctx.beginPath();
      ctx.arc(p.x, p.y, p.w / 2 + i * 7 + Math.sin(G.state.time * 5) * 2, Math.PI, 2 * Math.PI);
      ctx.stroke();
    }
  }
  ctx.restore();
}

function drawBall(b) {
  const ctx = cm.ctx;
  // trail
  for (let i = 0; i < b.trail.length; i++) {
    const t = b.trail[i];
    const a = (i / b.trail.length) * 0.5;
    ctx.fillStyle = b.fire ? `rgba(255,140,50,${a})` : `hsla(${b.hue},90%,70%,${a})`;
    ctx.beginPath();
    ctx.arc(t.x, t.y, b.r * (i / b.trail.length) * 0.9, 0, 6.283);
    ctx.fill();
  }
  ctx.save();
  ctx.shadowColor = b.fire ? '#ff7043' : `hsl(${b.hue},95%,65%)`;
  ctx.shadowBlur = 14;
  ctx.fillStyle = b.fire ? '#ffab91' : '#e8f6ff';
  if (b.heavy) ctx.fillStyle = '#d7ccc8';
  ctx.beginPath();
  ctx.arc(b.x, b.y, b.r * (b.heavy ? 1.4 : 1), 0, 6.283);
  ctx.fill();
  ctx.restore();
  // speed streaks when fast enough (speed brick hint)
  if (G.ballSpeed(b) >= 560) {
    ctx.save();
    ctx.strokeStyle = 'rgba(105,240,174,.9)';
    ctx.lineWidth = 2;
    const sp = G.ballSpeed(b);
    ctx.beginPath();
    ctx.moveTo(b.x - b.vx / sp * 16, b.y - b.vy / sp * 16);
    ctx.lineTo(b.x, b.y);
    ctx.stroke();
    ctx.restore();
  }
}

function drawDrop(d) {
  const ctx = cm.ctx;
  const P = POWER_TYPES[d.type];
  ctx.save();
  ctx.translate(d.x, d.y);
  ctx.rotate(Math.sin(d.t * 4) * 0.25);
  ctx.shadowColor = P.color; ctx.shadowBlur = 12;
  ctx.fillStyle = 'rgba(10,16,40,.85)';
  roundedRect(ctx, -13, -13, 26, 26, 7); ctx.fill();
  ctx.strokeStyle = P.color; ctx.lineWidth = 2;
  roundedRect(ctx, -13, -13, 26, 26, 7); ctx.stroke();
  ctx.shadowBlur = 0;
  ctx.fillStyle = P.color;
  ctx.strokeStyle = P.color; ctx.lineWidth = 2.2;
  // icon glyphs (procedural)
  switch (d.type) {
    case 'multi':
      for (const [ox, oy, r] of [[-5, 3, 3.4], [5, 3, 3.4], [0, -5, 3.4]]) { ctx.beginPath(); ctx.arc(ox, oy, r, 0, 6.283); ctx.fill(); }
      break;
    case 'fire':
      ctx.beginPath(); ctx.moveTo(0, -8); ctx.quadraticCurveTo(7, -1, 0, 8); ctx.quadraticCurveTo(-7, -1, 0, -8); ctx.fill();
      break;
    case 'heavy':
      ctx.fillRect(-7, -3, 14, 6); ctx.fillRect(-3, -7, 6, 14);
      break;
    case 'slow':
      ctx.beginPath(); ctx.arc(0, 0, 7, 0, 6.283); ctx.stroke();
      ctx.beginPath(); ctx.moveTo(0, 0); ctx.lineTo(0, -4); ctx.moveTo(0, 0); ctx.lineTo(3, 2); ctx.stroke();
      break;
    case 'wide':
      ctx.beginPath(); ctx.moveTo(-8, 0); ctx.lineTo(8, 0); ctx.moveTo(-8, 0); ctx.lineTo(-4, -4); ctx.moveTo(-8, 0); ctx.lineTo(-4, 4);
      ctx.moveTo(8, 0); ctx.lineTo(4, -4); ctx.moveTo(8, 0); ctx.lineTo(4, 4); ctx.stroke();
      break;
    case 'magnet':
      ctx.beginPath(); ctx.arc(0, 2, 6, Math.PI, 0, true); ctx.stroke();
      ctx.fillRect(-6, 2, 4, 5); ctx.fillRect(2, 2, 4, 5);
      break;
    case 'ghost':
      ctx.globalAlpha = 0.65;
      ctx.beginPath(); ctx.arc(0, -1, 6.5, Math.PI, 0); ctx.lineTo(6.5, 6); ctx.lineTo(3, 3.5); ctx.lineTo(0, 6.5); ctx.lineTo(-3, 3.5); ctx.lineTo(-6.5, 6); ctx.closePath(); ctx.fill();
      break;
  }
  ctx.restore();
}

function drawHUD() {
  const ctx = cm.ctx, s = G.state, c = G.combo;
  const g = G.getGame();
  ctx.save();
  ctx.font = '600 15px "Segoe UI", system-ui, sans-serif';
  ctx.textAlign = 'left';
  ctx.fillStyle = '#aebfff';
  ctx.shadowColor = '#000'; ctx.shadowBlur = 4;
  ctx.fillText(`SCORE ${s.score}`, 14, 26);
  ctx.fillText(`BEST ${s.best}`, 14, 48);
  ctx.textAlign = 'right';
  // keep LEVEL/hearts clear of the fixed top-right buttons (gear 106px right + 40px wide)
  const btnBlock = 134 + 26; // button block width + breathing room
  const hudRight = Math.max(120, cm.w - 14 - btnBlock);
  ctx.fillText(`LEVEL ${s.level + 1}`, hudRight, 26);
  ctx.fillText('♥'.repeat(Math.max(0, s.lives)), hudRight, 48);
  ctx.restore();

  // multiplier badge (big, center-top, scales with mult)
  if (c.mult > 1) {
    const scale = 1 + Math.min(0.5, c.mult * 0.04) + Math.sin(s.time * 8) * 0.03;
    ctx.save();
    ctx.translate(cm.w / 2, 40);
    ctx.scale(scale, scale);
    ctx.font = '800 26px "Segoe UI", system-ui, sans-serif';
    ctx.textAlign = 'center';
    ctx.fillStyle = hsl(50 - c.mult * 4, 100, 62);
    ctx.shadowColor = ctx.fillStyle; ctx.shadowBlur = 14 + c.mult * 2;
    ctx.fillText(`x${Math.floor(c.mult)}`, 0, 0);
    if (c.upTop) {
      ctx.font = '700 11px "Segoe UI", sans-serif';
      ctx.fillStyle = '#8ffcd0';
      ctx.fillText('UP TOP', 0, 16);
    }
    ctx.restore();
  }

  // power-up timer indicators (top-right, clear of the fixed buttons)
  const pwrRight = Math.max(120, cm.w - 14 - (134 + 26));
  let y = 62;
  ctx.save();
  ctx.textAlign = 'right';
  for (const k in G.effects) {
    if (G.effects[k] > 0) {
      const P = POWER_TYPES[k];
      ctx.font = '700 12px "Segoe UI", sans-serif';
      ctx.fillStyle = P.color;
      ctx.shadowColor = P.color; ctx.shadowBlur = 6;
      const label = `${P.name} ${G.effects[k].toFixed(1)}s`;
      ctx.fillText(label, pwrRight, y);
      // timer bar
      ctx.shadowBlur = 0;
      ctx.globalAlpha = 0.5;
      ctx.fillRect(pwrRight - 110, y + 4, 110 * Math.min(1, G.effects[k] / (POWER_DUR[k] || 10)), 3);
      ctx.globalAlpha = 1;
      y += 22;
    }
  }
  ctx.restore();

  // summit zone tint when any ball is up top
  if (c.upTop) {
    const sy = G.summitY();
    ctx.save();
    ctx.globalAlpha = 0.12 + Math.sin(s.time * 10) * 0.04;
    const grad = ctx.createLinearGradient(0, g.fy, 0, sy);
    grad.addColorStop(0, '#aefccf'); grad.addColorStop(1, 'transparent');
    ctx.fillStyle = grad;
    ctx.fillRect(g.fx, g.fy, g.fw, Math.max(4, sy - g.fy));
    ctx.restore();
  }
}

function drawCallout() {
  const c = G.combo;
  if (c.calloutT <= 0 || !c.callout) return;
  const ctx = cm.ctx;
  const a = Math.min(1, c.calloutT / 0.4);
  const pop = 1 + (1 - a) * 0.8;
  ctx.save();
  ctx.translate(cm.w / 2, cm.h * 0.3);
  ctx.scale(pop, pop);
  ctx.globalAlpha = a;
  ctx.font = '900 44px "Segoe UI", system-ui, sans-serif';
  ctx.textAlign = 'center';
  const hue = (G.combo.mult * 30) % 360;
  ctx.fillStyle = hsl(hue, 100, 65);
  ctx.shadowColor = ctx.fillStyle; ctx.shadowBlur = 26;
  ctx.fillText(c.callout, 0, 0);
  ctx.restore();
}

function drawBanner() {
  const b = G.getBanner();
  if (b.bannerT <= 0) return;
  const ctx = cm.ctx;
  const a = Math.min(1, b.bannerT / 0.5);
  ctx.save();
  ctx.globalAlpha = a;
  ctx.textAlign = 'center';
  ctx.font = '900 34px "Segoe UI", system-ui, sans-serif';
  ctx.fillStyle = '#eafcff';
  ctx.shadowColor = '#39c5ff'; ctx.shadowBlur = 22;
  ctx.fillText(b.bannerText, cm.w / 2, cm.h * 0.42);
  if (b.bannerSub) {
    ctx.font = '500 15px "Segoe UI", sans-serif';
    ctx.shadowBlur = 8;
    ctx.fillStyle = '#9fb4ff';
    ctx.fillText(b.bannerSub, cm.w / 2, cm.h * 0.42 + 28);
  }
  ctx.restore();
}

// ---------- title / overlays ------------------------------------------------------
let titleBalls = [];
function drawTitle(dt) {
  const ctx = cm.ctx, s = G.state;
  const titleT = G.getTitleT() + dt;
  G.setTitleT(titleT);
  // demo balls bouncing in bg
  if (titleBalls.length < 5 && Math.random() < dt * 0.8) {
    titleBalls.push({ x: Math.random() * cm.w, y: cm.h * (0.04 + Math.random() * 0.1), vx: (Math.random() - 0.5) * 260, vy: -180 - Math.random() * 120, r: 6 });
  }
  for (const b of titleBalls) {
    b.x += b.vx * dt; b.y += b.vy * dt;
    if (b.x < 0 || b.x > cm.w) b.vx *= -1;
    if (b.y < 0) b.vy = Math.abs(b.vy);
    if (b.y > cm.h * 0.2) b.vy = -Math.abs(b.vy); // keep demo balls above the title text
    if (!s.reducedMotion && Math.random() < 0.3) parts.spawn(b.x, b.y, 0, 0, 0.4, 3, '#5fd8ff', { shape: 'circle' });
  }
  for (const b of titleBalls) {
    ctx.save();
    ctx.shadowColor = '#5fd8ff'; ctx.shadowBlur = 16;
    ctx.fillStyle = '#dff4ff';
    ctx.beginPath(); ctx.arc(b.x, b.y, b.r, 0, 6.283); ctx.fill();
    ctx.restore();
  }

  ctx.save();
  ctx.textAlign = 'center';
  const grad = ctx.createLinearGradient(0, cm.h * 0.24, 0, cm.h * 0.4);
  grad.addColorStop(0, '#7ef3ff'); grad.addColorStop(1, '#b388ff');
  ctx.fillStyle = grad;
  ctx.font = `900 ${Math.min(84, cm.w * 0.13)}px "Segoe UI", system-ui, sans-serif`;
  ctx.shadowColor = '#4fc3f7'; ctx.shadowBlur = 30 + Math.sin(titleT * 2) * 8;
  ctx.fillText('BREAKAWAY', cm.w / 2, cm.h * 0.34);
  ctx.font = '600 15px "Segoe UI", sans-serif';
  ctx.shadowBlur = 8;
  ctx.fillStyle = '#8fa5ff';
  ctx.fillText('BREAK THE WALL. GET UP TOP. GO RAMPAGE.', cm.w / 2, cm.h * 0.4);

  // buttons
  const bw = 210, bh = 52, bx = cm.w / 2 - bw / 2, by = cm.h * 0.52;
  drawNeonButton(ctx, bx, by, bw, bh, 'PLAY', '#39d2ff', titleT);
  drawNeonButton(ctx, bx, by + 70, bw, bh, 'HOW TO PLAY', '#b388ff', titleT);
  ctx.font = '500 13px "Segoe UI", sans-serif';
  ctx.fillStyle = '#7285c0';
  ctx.fillText(`BEST  ${s.best}`, cm.w / 2, by + 160);
  ctx.restore();

  // store button hitboxes for pointer handling
  titleButtons = [
    { x: bx, y: by, w: bw, h: bh, act: 'play' },
    { x: bx, y: by + 70, w: bw, h: bh, act: 'howto' },
  ];
}
let titleButtons = [];

function drawNeonButton(ctx, x, y, w, h, label, color, t) {
  ctx.save();
  const hover = mouse.x > x && mouse.x < x + w && mouse.y > y && mouse.y < y + h;
  ctx.shadowColor = color; ctx.shadowBlur = hover ? 24 : 12;
  ctx.fillStyle = 'rgba(14,22,54,.85)';
  roundedRect(ctx, x, y, w, h, 14); ctx.fill();
  ctx.strokeStyle = color; ctx.lineWidth = 2;
  roundedRect(ctx, x, y, w, h, 14); ctx.stroke();
  ctx.shadowBlur = 0;
  ctx.fillStyle = color;
  ctx.font = '800 20px "Segoe UI", sans-serif';
  ctx.textAlign = 'center';
  ctx.fillText(label, x + w / 2, y + h / 2 + 7);
  ctx.restore();
}

function drawHowTo() {
  const ctx = cm.ctx;
  ctx.save();
  ctx.fillStyle = 'rgba(4,8,22,.92)';
  ctx.fillRect(0, 0, cm.w, cm.h);
  ctx.textAlign = 'center';
  ctx.fillStyle = '#eafcff';
  ctx.font = '900 30px "Segoe UI", sans-serif';
  ctx.shadowColor = '#4fc3f7'; ctx.shadowBlur = 16;
  ctx.fillText('HOW TO PLAY', cm.w / 2, 70);
  ctx.shadowBlur = 0;
  ctx.font = '500 14px "Segoe UI", sans-serif';
  ctx.fillStyle = '#9fb4ff';
  const lines = [
    'Move: mouse / drag / WASD-arrows — paddle moves in X AND Y (bottom band)',
    'Tap or click to launch. SPACE also works.',
    '',
    '★ THE GOAL: break through the wall so the ball gets UP TOP —',
    '   it then bounces freely above the bricks, racking up a huge multiplier.',
    '',
    '★ Vertical boost: moving the paddle UP at contact fires the ball faster.',
    '   Fast balls break SPEED bricks and score more.',
  ];
  lines.forEach((l, i) => ctx.fillText(l, cm.w / 2, 120 + i * 26));

  // diagrams
  const dy = cm.h - 190;
  ctx.textAlign = 'left';
  ctx.font = '700 13px "Segoe UI", sans-serif';
  // angle brick diagram
  let dx = cm.w * 0.18;
  ctx.fillStyle = '#ffb74d';
  roundedRect(ctx, dx, dy, 74, 26, 5); ctx.fill();
  ctx.strokeStyle = '#1a1206'; ctx.lineWidth = 2;
  ctx.beginPath(); ctx.moveTo(dx + 12, dy + 13); ctx.lineTo(dx + 62, dy + 13);
  ctx.lineTo(dx + 54, dy + 8); ctx.moveTo(dx + 62, dy + 13); ctx.lineTo(dx + 54, dy + 18); ctx.stroke();
  ctx.fillStyle = '#cfd8ff';
  ctx.fillText('ANGLE: hit at the', dx - 10, dy + 48);
  ctx.fillText('shown angle only', dx - 10, dy + 66);
  // speed brick diagram
  dx = cm.w * 0.45;
  ctx.fillStyle = '#69f0ae';
  roundedRect(ctx, dx, dy, 74, 26, 5); ctx.fill();
  ctx.strokeStyle = '#032018'; ctx.lineWidth = 2;
  for (let i = -1; i <= 1; i++) { ctx.beginPath(); ctx.moveTo(dx + 20, dy + 13 + i * 5); ctx.lineTo(dx + 54, dy + 13 + i * 5); ctx.stroke(); }
  ctx.fillStyle = '#cfd8ff';
  ctx.fillText('SPEED: fast ball', dx - 10, dy + 48);
  ctx.fillText('only — boost up!', dx - 10, dy + 66);
  // phase brick diagram
  dx = cm.w * 0.72;
  const ph = (performance.now() / 1000) % 2.4 / 2.4;
  ctx.fillStyle = ph < 0.55 ? '#ff80ab' : 'rgba(255,128,171,.15)';
  roundedRect(ctx, dx, dy, 74, 26, 5); ctx.fill();
  ctx.strokeStyle = ph < 0.55 ? '#ffd0e0' : 'rgba(255,180,200,.5)';
  roundedRect(ctx, dx, dy, 74, 26, 5); ctx.stroke();
  ctx.fillStyle = '#cfd8ff';
  ctx.fillText('PHASE: strikes when', dx - 10, dy + 48);
  ctx.fillText('solid, thunks when not', dx - 10, dy + 66);

  ctx.textAlign = 'center';
  ctx.fillStyle = '#7285c0';
  ctx.fillText('Tap / click anywhere to start', cm.w / 2, cm.h - 40);
  ctx.restore();
}

function drawStats() {
  const ctx = cm.ctx, c = G.combo, s = G.state;
  const w = Math.min(400, cm.w * 0.86), h = 250;
  const x = cm.w / 2 - w / 2, y = cm.h / 2 - h / 2;
  ctx.save();
  ctx.shadowColor = '#000'; ctx.shadowBlur = 30;
  ctx.fillStyle = 'rgba(12,18,44,.96)';
  roundedRect(ctx, x, y, w, h, 18); ctx.fill();
  ctx.shadowBlur = 0;
  ctx.strokeStyle = c.upTop ? '#7ef3c0' : '#4fc3f7'; ctx.lineWidth = 2;
  roundedRect(ctx, x, y, w, h, 18); ctx.stroke();

  ctx.textAlign = 'center';
  ctx.fillStyle = '#eafcff';
  ctx.font = '900 26px "Segoe UI", sans-serif';
  ctx.fillText(s.mode === 'gameover' ? 'GAME OVER' : 'LEVEL CLEAR!', cm.w / 2, y + 48);
  ctx.font = '600 15px "Segoe UI", sans-serif';
  ctx.fillStyle = '#8fa5ff';
  const rows = [
    ['SCORE', s.score],
    ['MAX MULTIPLIER', 'x' + Math.floor(c.maxMult)],
    ['TIME UP TOP', c.totalUpTop.toFixed(1) + 's'],
    ['BRICKS BROKEN', stats.bricksBroken],
  ];
  rows.forEach((r, i) => {
    ctx.textAlign = 'left'; ctx.fillStyle = '#7285c0';
    ctx.fillText(r[0], x + 40, y + 95 + i * 30);
    ctx.textAlign = 'right'; ctx.fillStyle = '#eafcff';
    ctx.fillText(r[1], x + w - 40, y + 95 + i * 30);
  });
  ctx.textAlign = 'center';
  ctx.fillStyle = '#7285c0';
  ctx.font = '500 13px "Segoe UI", sans-serif';
  ctx.fillText(s.mode === 'gameover' ? 'tap / click / space to retry' : 'tap / click / space for next level', cm.w / 2, y + h - 24);
  ctx.restore();
}

// ---------- main loop (fixed timestep, sub-stepped physics) -----------------------
const STEP = 1 / 120;
let acc = 0, lastT = 0;

function tick(tms) {
  requestAnimationFrame(tick);
  if (!lastT) lastT = tms;
  let frame = Math.min(0.05, (tms - lastT) / 1000);
  lastT = tms;
  const s = G.state;

  if (isTest) {
    // deterministic-ish fixed steps for the test hook
    simTime += frame;
    if (simTime > 15 && !report.done) finishReport();
  }

  if (!s.paused) {
    acc += frame;
    let steps = 0;
    const timeScale = G.effects.slow > 0 ? 0.45 : 1;
    while (acc >= STEP && steps < 8) {
      update(STEP * timeScale);
      acc -= STEP; steps++;
    }
  }
  cm.update(frame);
  audioRef().update();
  render(frame);
}

function update(dt) {
  const s = G.state, g = G.getGame(), c = G.combo;
  s.time += dt;
  for (const b of G.getBricks()) { if (b.clankT > 0) b.clankT -= dt * 3; }

  if (isTest && s.mode === 'play' && !s.paused) testAI(dt);

  if (s.mode === 'title') return;

  if (s.mode === 'play') {
    updatePaddle(dt);
    updateBalls(dt);
    updateDrops(dt);
    G.updateEffects(dt);
    updateCombo(dt);
    checkLevelClear();
    G.decayBanner(dt);
  } else if (s.mode === 'dying') {
    G.setDyingT(G.getDyingT() - dt);
    if (G.getDyingT() <= 0) {
      s.mode = 'play';
      G.serveBall();
    }
  } else if (s.mode === 'levelclear') {
    G.setLevelClearT(G.getLevelClearT() + dt);
  }
  parts.update(dt);
  G.combo.calloutT = Math.max(0, G.combo.calloutT - dt);
}

function render(frame) {
  const ctx = cm.ctx, s = G.state, g = G.getGame(), c = G.combo;
  const [ox, oy] = cm.offset();
  cm.drawBackground(audioRef().beatPulse, Math.min(1, (c.mult - 1) / 8), Math.min(1, (c.mult - 1) / 10));
  ctx.save();
  ctx.translate(ox, oy);

  if (s.mode === 'title') {
    drawTitle(frame);
  } else {
    // field frame
    ctx.strokeStyle = 'rgba(120,150,255,0.25)';
    ctx.lineWidth = 2;
    ctx.strokeRect(g.fx - 4, g.fy - 4, g.fw + 8, g.fh + 8);

    for (const b of G.getBricks()) if (!b.dead) drawBrick(b);
    for (const d of drops) drawDrop(d);
    drawPaddle();
    for (const b of G.balls) drawBall(b);
    drawHUD();
    drawCallout();
    drawBanner();

    if (s.mode === 'levelclear' || s.mode === 'gameover') drawStats();
    if (s.mode === 'dying') {
      ctx.save();
      ctx.globalAlpha = 0.5 * (G.getDyingT() / 1.2);
      ctx.fillStyle = '#f43';
      ctx.fillRect(0, 0, cm.w, cm.h);
      ctx.restore();
    }
  }
  parts.draw(ctx);
  ctx.restore();

  // slow-mo filter
  if (G.effects.slow > 0) {
    ctx.fillStyle = `rgba(60,200,230,${0.08 + Math.sin(s.time * 6) * 0.02})`;
    ctx.fillRect(0, 0, cm.w, cm.h);
  }
  if (s.mode === 'howto') drawHowTo();
  if (s.paused) {
    ctx.fillStyle = 'rgba(4,8,20,.6)';
    ctx.fillRect(0, 0, cm.w, cm.h);
    ctx.fillStyle = '#eafcff';
    ctx.font = '900 36px "Segoe UI", sans-serif';
    ctx.textAlign = 'center';
    ctx.fillText('PAUSED', cm.w / 2, cm.h / 2);
  }
  cm.applyOverlays();
}

// title screen click handling (buttons + howto close)
canvas.addEventListener('click', e => {
  const s = G.state;
  if (s.mode === 'howto') { s.mode = 'title'; return; }
  if (s.mode !== 'title') return;
  for (const b of titleButtons) {
    if (e.clientX > b.x && e.clientX < b.x + b.w && e.clientY > b.y && e.clientY < b.y + b.h) {
      if (b.act === 'play') newGame();
      else if (b.act === 'howto') s.mode = 'howto';
      return;
    }
  }
});
// howto accessible from gameover too via action() default (restart)

// ---------- test hook ---------------------------------------------------------------
const report = { done: false, value: null };
window.__gameReport = () => report.value;
window.__gameReport.__done = false;

function finishReport() {
  report.done = true;
  const c = G.combo, s = G.state;
  report.value = {
    framesSimulated: Math.round(simTime * 60),
    bricksBroken: stats.bricksBroken,
    ballsInPlay: G.balls.length,
    maxMultiplierReached: +c.maxMult.toFixed(1),
    upTopSeconds: +c.totalUpTop.toFixed(1),
    score: s.score,
    level: s.level + 1,
    lives: s.lives,
    mode: s.mode,
    consoleErrorCount: errorCount,
  };
  window.__gameReport.__done = true;
}

window.__game = {
  pause: () => { G.state.paused = true; },
  resume: () => { G.state.paused = false; },
  state: () => ({ mode: G.state.mode, level: G.state.level, score: G.state.score, lives: G.state.lives, mult: G.combo.mult, balls: G.balls.length }),
  start: () => newGame(),
  launch: () => { const sv = G.getServe(); if (sv.ball) G.launchBall(sv.ball); },
  setBall: (x, y, vx, vy) => { const b = G.balls[0]; if (b) { b.x = x; b.y = y; b.vx = vx; b.vy = vy; b.stuck = false; if (G.state.mode === 'dying') G.state.mode = 'play'; } },
  debugSample: () => ({
    ball: G.balls[0] ? { x: G.balls[0].x | 0, y: G.balls[0].y | 0, vx: G.balls[0].vx | 0, vy: G.balls[0].vy | 0 } : null,
    paddleX: G.paddle.x | 0, aim: aiBrickTarget ? (aiBrickTarget.baseCol) : null,
    score: G.state.score, mode: G.state.mode,
    summitY: G.summitY() | 0, mult: G.combo.mult, upTop: G.combo.upTop,
  }),
  debug: () => ({
    balls: G.balls.map(b => ({ x: b.x | 0, y: b.y | 0, vx: b.vx | 0, vy: b.vy | 0, stuck: b.stuck })),
    paddle: { x: G.paddle.x | 0, y: G.paddle.y | 0, tx: G.paddle.targetX | 0, ty: G.paddle.targetY | 0, vy: G.paddle.vy | 0 },
    field: { fx: G.getGame().fx | 0, fy: G.getGame().fy | 0, fw: G.getGame().fw | 0, fh: G.getGame().fh | 0, padMaxY: G.getGame().padMaxY | 0 },
  }),
};

window.onerror = (msg, src, line, col, err) => { errorCount++; G.bumpErrors(); return false; };
window.addEventListener('unhandledrejection', () => { errorCount++; G.bumpErrors(); });

// auto-start in test mode: begin playing immediately with AI
if (isTest) {
  newGame();
  G.state.mode = 'play';
}

// ---------- boot ---------------------------------------------------------------------
requestAnimationFrame(tick);
