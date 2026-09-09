// src/core/input.js — mouse, touch (first-class), keyboard, gamepad -> paddle intent.
import { W, H } from './constants.js';
import { clamp, hypot } from './math.js';

const STICK_DEADZONE = 0.12;
const STICK_MAX_PX = 70; // css px radius of the visual thumbstick

export const Input = {
  attach(canvasEl, renderer) {
    detach();
    state.canvas = canvasEl;
    state.renderer = renderer;
    state.intent = { x: W / 2, y: 1350, dx: 0, dy: 0, launch: false, action: false, usingTouch: false };
    bind();
  },
  detach,
  get intent() { return state.intent; },
  update(dt) { tick(dt); },
  drawTouchUI(g) { drawTouchUI(g); },
};

const state = {
  canvas: null,
  renderer: null,
  intent: { x: W / 2, y: 1350, dx: 0, dy: 0, launch: false, action: false, usingTouch: false },
  keys: Object.create(null),
  keyEvents: { launch: false, pause: false, mute: false, restart: false },
  pointers: new Map(), // pointerId -> {mode:'stick'|'direct', startX,startY,curX,curY,worldX,worldY}
  mouse: { x: W / 2, y: 1350, down: false, active: false },
  stick: { active: false, cx: 0, cy: 0, dx: 0, dy: 0, id: null },
  bound: false,
  targetWorld: { x: W / 2, y: 1350 },
};

function bind() {
  if (state.bound) return;
  state.bound = true;
  const c = state.canvas;
  c.style.touchAction = 'none';
  c.addEventListener('pointerdown', onPointerDown, { passive: false });
  c.addEventListener('pointermove', onPointerMove, { passive: false });
  c.addEventListener('pointerup', onPointerUp, { passive: false });
  c.addEventListener('pointercancel', onPointerUp, { passive: false });
  c.addEventListener('contextmenu', preventDefault);
  window.addEventListener('keydown', onKeyDown);
  window.addEventListener('keyup', onKeyUp);
  window.addEventListener('blur', onBlur);
  document.addEventListener('touchmove', preventScroll, { passive: false });
  document.addEventListener('gesturestart', preventDefault, { passive: false });
}

function detach() {
  if (!state.bound) return;
  state.bound = false;
  const c = state.canvas;
  if (c) {
    c.removeEventListener('pointerdown', onPointerDown);
    c.removeEventListener('pointermove', onPointerMove);
    c.removeEventListener('pointerup', onPointerUp);
    c.removeEventListener('pointercancel', onPointerUp);
    c.removeEventListener('contextmenu', preventDefault);
  }
  window.removeEventListener('keydown', onKeyDown);
  window.removeEventListener('keyup', onKeyUp);
  window.removeEventListener('blur', onBlur);
  document.removeEventListener('touchmove', preventScroll);
  document.removeEventListener('gesturestart', preventDefault);
  state.pointers.clear();
}

function preventDefault(e) { e.preventDefault(); }
function preventScroll(e) { if (state.pointers.size > 0) e.preventDefault(); }
function onBlur() { state.keys = Object.create(null); state.pointers.clear(); state.mouse.down = false; }

function onKeyDown(e) {
  const k = e.key.toLowerCase();
  state.keys[k] = true;
  if (k === ' ' || k === 'spacebar') { state.keyEvents.launch = true; e.preventDefault(); }
  if (k === 'p') state.keyEvents.pause = true;
  if (k === 'm') state.keyEvents.mute = true;
  if (k === 'r') state.keyEvents.restart = true;
  if (['arrowup', 'arrowdown', 'arrowleft', 'arrowright'].includes(k)) e.preventDefault();
}
function onKeyUp(e) { state.keys[e.key.toLowerCase()] = false; }

function isPaddleBox(worldPt, paddleBox) {
  if (!paddleBox) return false;
  const pad = 40;
  return worldPt.x > paddleBox.x - pad && worldPt.x < paddleBox.x + paddleBox.w + pad &&
    worldPt.y > paddleBox.y - pad * 2 && worldPt.y < paddleBox.y + paddleBox.h + pad * 2;
}

function onPointerDown(e) {
  e.preventDefault();
  const r = state.renderer;
  if (!r) return;
  const world = r.worldFromClient(e.clientX, e.clientY);
  const rect = state.canvas.getBoundingClientRect();
  const localX = e.clientX - rect.left;
  const isTouch = e.pointerType === 'touch';

  if (e.pointerType === 'mouse') {
    state.mouse.active = true;
    state.mouse.down = true;
    state.intent.usingTouch = false;
    state.intent.action = true;
    state.intent.launch = true;
    state.targetWorld.x = world.x;
    state.targetWorld.y = world.y;
    return;
  }

  state.intent.usingTouch = true;
  const halfW = state.canvas.getBoundingClientRect().width / 2;
  const paddleBox = state.paddleBox;
  const directMode = isPaddleBox(world, paddleBox);
  if (directMode) {
    state.pointers.set(e.pointerId, { mode: 'direct', worldX: world.x, worldY: world.y });
  } else if (localX < halfW) {
    state.pointers.set(e.pointerId, { mode: 'stick', startX: e.clientX, startY: e.clientY, curX: e.clientX, curY: e.clientY });
    state.stick.active = true;
    state.stick.cx = e.clientX;
    state.stick.cy = e.clientY;
    state.stick.dx = 0;
    state.stick.dy = 0;
    state.stick.id = e.pointerId;
  } else {
    state.pointers.set(e.pointerId, { mode: 'action', worldX: world.x, worldY: world.y });
    state.intent.action = true;
    state.intent.launch = true;
  }
}

function onPointerMove(e) {
  const p = state.pointers.get(e.pointerId);
  if (!p) return;
  e.preventDefault();
  const r = state.renderer;
  const world = r ? r.worldFromClient(e.clientX, e.clientY) : { x: p.worldX, y: p.worldY };
  if (p.mode === 'direct') {
    p.worldX = world.x; p.worldY = world.y;
    state.targetWorld.x = world.x;
    state.targetWorld.y = world.y;
  } else if (p.mode === 'stick') {
    p.curX = e.clientX; p.curY = e.clientY;
    let dx = e.clientX - p.startX, dy = e.clientY - p.startY;
    const mag = hypot(dx, dy);
    const norm = clamp(mag / STICK_MAX_PX, 0, 1);
    if (mag > 1e-4) { dx = (dx / mag); dy = (dy / mag); }
    const dz = norm < STICK_DEADZONE ? 0 : (norm - STICK_DEADZONE) / (1 - STICK_DEADZONE);
    state.stick.dx = dx * dz;
    state.stick.dy = dy * dz;
    state.stick.cx = p.startX;
    state.stick.cy = p.startY;
    // visual clamp of the nub
    const clampedMag = Math.min(mag, STICK_MAX_PX);
    state.stick.nubX = p.startX + (mag > 0 ? dx * clampedMag : 0);
    state.stick.nubY = p.startY + (mag > 0 ? dy * clampedMag : 0);
  }
}

function onPointerUp(e) {
  const p = state.pointers.get(e.pointerId);
  if (p && p.mode === 'stick') {
    state.stick.active = false;
    state.stick.dx = 0;
    state.stick.dy = 0;
  }
  state.pointers.delete(e.pointerId);
  if (e.pointerType === 'mouse') state.mouse.down = false;
}

function tick(dt) {
  const intent = state.intent;
  intent.launch = false;
  intent.action = false;
  intent.dx = 0; intent.dy = 0;

  // keyboard
  const k = state.keys;
  let kx = 0, ky = 0;
  if (k['arrowleft'] || k['a']) kx -= 1;
  if (k['arrowright'] || k['d']) kx += 1;
  if (k['arrowup'] || k['w']) ky -= 1;
  if (k['arrowdown'] || k['s']) ky += 1;

  // gamepad
  let gx = 0, gy = 0, gpAction = false;
  const pads = navigator.getGamepads ? navigator.getGamepads() : [];
  for (const gp of pads || []) {
    if (!gp) continue;
    const ax0 = gp.axes[0] || 0, ax1 = gp.axes[1] || 0;
    if (Math.abs(ax0) > 0.15) gx = ax0;
    if (Math.abs(ax1) > 0.15) gy = ax1;
    if (gp.buttons[0] && gp.buttons[0].pressed) gpAction = true;
  }

  if (kx || ky) {
    state.targetWorld.x = clamp(state.targetWorld.x + kx * 900 * dt, 0, W);
    state.targetWorld.y = clamp(state.targetWorld.y + ky * 900 * dt, 0, H);
    intent.usingTouch = false;
  }
  if (gx || gy) {
    state.targetWorld.x = clamp(state.targetWorld.x + gx * 900 * dt, 0, W);
    state.targetWorld.y = clamp(state.targetWorld.y + gy * 900 * dt, 0, H);
    intent.usingTouch = false;
  }
  if (state.stick.active && (state.stick.dx || state.stick.dy)) {
    state.targetWorld.x = clamp(state.targetWorld.x + state.stick.dx * 1100 * dt, 0, W);
    state.targetWorld.y = clamp(state.targetWorld.y + state.stick.dy * 1100 * dt, 0, H);
  }

  if (state.keyEvents.launch) intent.launch = true;
  if (state.mouse.down) intent.action = true;
  if (gpAction) { intent.launch = true; intent.action = true; }
  for (const p of state.pointers.values()) {
    if (p.mode === 'action') { intent.action = true; intent.launch = true; }
  }

  state.keyEvents.launch = false;

  intent.x = state.targetWorld.x;
  intent.y = state.targetWorld.y;
};

// exposed via Input for pause/mute/restart edge-triggered events
Object.defineProperty(Input, 'consumeEvents', {
  value() {
    const e = { pause: state.keyEvents.pause, mute: state.keyEvents.mute, restart: state.keyEvents.restart };
    state.keyEvents.pause = false;
    state.keyEvents.mute = false;
    state.keyEvents.restart = false;
    return e;
  },
});
Object.defineProperty(Input, 'setPaddleBox', {
  value(box) { state.paddleBox = box; },
});

function drawTouchUI(g) {
  if (!state.stick.active) return;
  const r = state.renderer;
  if (!r) return;
  const world = r.worldFromClient(state.stick.cx, state.stick.cy);
  const nub = r.worldFromClient(state.stick.nubX ?? state.stick.cx, state.stick.nubY ?? state.stick.cy);
  g.save();
  g.globalCompositeOperation = 'lighter';
  g.strokeStyle = 'rgba(94,230,255,0.55)';
  g.lineWidth = 3;
  g.beginPath();
  g.arc(world.x, world.y, STICK_MAX_PX / r.scale, 0, Math.PI * 2);
  g.stroke();
  g.fillStyle = 'rgba(94,230,255,0.35)';
  g.beginPath();
  g.arc(nub.x, nub.y, 26, 0, Math.PI * 2);
  g.fill();
  g.restore();
}
