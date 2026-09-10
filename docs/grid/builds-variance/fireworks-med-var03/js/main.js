'use strict';
// ---------------------------------------------------------------------------
// main.js — canvas setup, view transform, fixed-timestep RAF loop.
// Defines the globals input.js relies on: `canvas`, `W`, `H`.
// ---------------------------------------------------------------------------

var canvas, W = 480, H = 720;
canvas = document.getElementById('game');
const ctx = canvas.getContext('2d');

// view: logical → canvas px transform (uniform scale, centered letterbox)
const view = { s: 1, offX: 0, offY: 0, dpr: 1 };

function toLogicalX(px) { return (px - view.offX * view.dpr) / (view.s * view.dpr); }
function toLogicalY(py) { return (py - view.offY * view.dpr) / (view.s * view.dpr); }

function resize() {
  view.dpr = Math.min(window.devicePixelRatio || 1, 2);
  const cw = window.innerWidth, ch = window.innerHeight;
  canvas.width = Math.round(cw * view.dpr);
  canvas.height = Math.round(ch * view.dpr);
  view.s = Math.min(canvas.width / W, canvas.height / H);
  view.offX = (canvas.width - W * view.s) / 2 / view.dpr;   // CSS px
  view.offY = (canvas.height - H * view.s) / 2 / view.dpr;
  Backdrop.resize(canvas.width, canvas.height);
}

// wait for DOM (scripts run at end of body, but be safe)
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', () => { resize(); Backdrop.init(canvas.width, canvas.height, view.s); });
} else {
  resize();
  Backdrop.init(canvas.width, canvas.height, view.s);
}
window.addEventListener('resize', resize);

// one-time audio unlock on first gesture
function firstGesture() {
  AudioSys.resume();
  window.removeEventListener('pointerdown', firstGesture);
  window.removeEventListener('keydown', firstGesture);
  window.removeEventListener('touchstart', firstGesture);
}
window.addEventListener('pointerdown', firstGesture);
window.addEventListener('keydown', firstGesture);
window.addEventListener('touchstart', firstGesture, { passive: true });

// prevent context menu / scroll zoom
window.addEventListener('contextmenu', e => e.preventDefault());

// mute persistence
try {
  if (localStorage.getItem('breakpoint_mute') === '1') AudioSys.setEnabled(false);
} catch (e) {}
function toggleMute() {
  AudioSys.setEnabled(!AudioSys.enabled);
  try { localStorage.setItem('breakpoint_mute', AudioSys.enabled ? '0' : '1'); } catch (e) {}
  AudioSys.uiClick();
}

// ---------------------------------------------------------------- loop --
Game.init();

let last = performance.now();
let hudTapCooldown = 0;

function frame(now) {
  requestAnimationFrame(frame);
  let dt = (now - last) / 1000;
  last = now;
  if (dt > 0.05) dt = 0.05; // tab-switch guard

  const fx = FX.update(dt);          // real dt BEFORE game update
  if (fx.frozen) {
    draw(now);
    return;
  }
  const gdt = dt * fx.slowFactor;
  Input.update();

  // UI tap targets (pause / mute) — check raw pointer screen coords
  handleUITaps();

  if (Game.state === 'playing' || Game.state === 'title' ||
      Game.state === 'levelclear' || Game.state === 'gameover' || Game.state === 'paused') {
    Game.update(gdt);
  }
  Particles.update(gdt);
  Backdrop.update(gdt, Game.hype);

  draw(now);
}

function handleUITaps() {
  if (hudTapCooldown > 0) { hudTapCooldown -= 1 / 60; return; }
  // HUD taps only matter while playing/paused; otherwise pass through to Game
  if (Game.state !== 'playing' && Game.state !== 'paused') return;
  const st = Input.state;
  if (!Input.consumeAction()) return;
  const px = st.pointerX !== null ? toLogicalX(st.pointerX) : W / 2;
  const py = st.pointerY !== null ? toLogicalY(st.pointerY) : H / 2;
  const hit = UI.hitTest(px, py);
  if (hit === 'mute') { toggleMute(); hudTapCooldown = 0.3; Input.state.actionPressed = false; return; }
  if (hit === 'pause') {
    if (Game.state === 'playing') { Game.state = 'paused'; AudioSys.uiClick(); }
    else if (Game.state === 'paused') { Game.state = 'playing'; AudioSys.uiClick(); }
    hudTapCooldown = 0.3;
    Input.state.actionPressed = false;
    return;
  }
  // not a HUD tap: hand the action back to the game
  Input.state.actionPressed = true;
}

function draw(now) {
  const cw = canvas.width, ch = canvas.height;
  ctx.setTransform(1, 0, 0, 1, 0, 0);
  ctx.clearRect(0, 0, cw, ch);
  Backdrop.draw(ctx); // full canvas, before camera

  // letterbox bars
  ctx.fillStyle = '#05060f';
  if (view.offX * view.dpr > 0.5) {
    ctx.fillRect(0, 0, view.offX * view.dpr, ch);
    ctx.fillRect(cw - view.offX * view.dpr, 0, view.offX * view.dpr, ch);
  }
  if (view.offY * view.dpr > 0.5) {
    ctx.fillRect(0, 0, cw, view.offY * view.dpr);
    ctx.fillRect(0, ch - view.offY * view.dpr, cw, view.offY * view.dpr);
  }

  FX.applyCamera(ctx, W, H);
  ctx.transform(view.s * view.dpr, 0, 0, view.s * view.dpr,
                view.offX * view.dpr, view.offY * view.dpr);
  // world (camera-shaken)
  ctx.save();
  Game.draw(ctx);
  Particles.draw(ctx);
  ctx.restore();

  // floaters live in game coords → drawn inside the camera/view transform
  FX.drawFloaters(ctx, 1);
  FX.restoreCamera(ctx);

  // screen space HUD
  ctx.setTransform(view.s * view.dpr, 0, 0, view.s * view.dpr,
                   view.offX * view.dpr, view.offY * view.dpr);
  if (Game.state === 'playing') {
    UI.drawHUD(ctx, Game);
    UI.drawLevelIntro(ctx, Game);
    UI.drawHints(ctx, Game);
  } else if (Game.state === 'title') {
    UI.drawHUD(ctx, Game);
    UI.drawTitle(ctx, Game);
  } else if (Game.state === 'paused') {
    UI.drawHUD(ctx, Game);
    UI.drawPause(ctx);
  } else if (Game.state === 'gameover') {
    UI.drawHUD(ctx, Game);
    UI.drawGameOver(ctx, Game);
  } else if (Game.state === 'levelclear') {
    UI.drawHUD(ctx, Game);
    UI.drawLevelClear(ctx, Game);
  }
  UI.drawStick(ctx, Input.state, view);

  FX.drawFlash(ctx, W, H);
  FX.drawChroma(ctx, null, W, H);
}

requestAnimationFrame(frame);
