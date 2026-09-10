// input.js — mouse, touch (absolute drag), keyboard, virtual thumbstick; punch gesture.
'use strict';

const Input = (() => {
  const state = {
    // pointer position in logical coords (-1 = inactive)
    px: -1, py: -1, pointerActive: false,
    launch: false,        // one-shot: tap/click/space pressed
    keyLeft: false, keyRight: false, keyUp: false, keyDown: false,
    punch: false,         // set true briefly on up-flick (thumbstick) or fast up-drag
    useStick: false,      // virtual thumbstick mode
    stickVX: 0, stickVY: 0, // stick output (logical px/s)
  };

  const stick = { ox: 0, oy: 0, dx: 0, dy: 0, drag: false, R: 60 };
  let lastTouchY = 0, lastTouchT = 0, canvas = null, stickEl = null;

  function toLogical(clientX, clientY) {
    const c = canvas;
    const r = c.getBoundingClientRect();
    const s = Math.min(r.width / CONFIG.LOGICAL_W, r.height / CONFIG.LOGICAL_H);
    const ox = (r.width - CONFIG.LOGICAL_W * s) / 2;
    const oy = (r.height - CONFIG.LOGICAL_H * s) / 2;
    return { x: (clientX - r.left - ox) / s, y: (clientY - r.top - oy) / s };
  }

  function setup(canvasEl) {
    canvas = canvasEl;
    const opts = { passive: false };

    canvas.addEventListener('mousemove', e => {
      const p = toLogical(e.clientX, e.clientY);
      state.px = p.x; state.py = p.y; state.pointerActive = true;
    });
    canvas.addEventListener('mousedown', () => {
      Input.resumeAudio(); state.launch = true;
    });
    canvas.addEventListener('touchstart', e => {
      e.preventDefault(); Input.resumeAudio();
      const t = e.touches[0], p = toLogical(t.clientX, t.clientY);
      state.px = p.x; state.py = p.y; state.pointerActive = true;
      state.launch = true;
      lastTouchY = p.y; lastTouchT = performance.now();
      if (stick.drag) stickTouch(t);
    }, opts);
    canvas.addEventListener('touchmove', e => {
      e.preventDefault();
      const t = e.touches[0], p = toLogical(t.clientX, t.clientY);
      // fast upward flick => punch gesture
      const dt = (performance.now() - lastTouchT) / 1000;
      if (dt > 0 && dt < 0.12 && (lastTouchY - p.y) / dt > 700) state.punch = true;
      lastTouchY = p.y; lastTouchT = performance.now();
      if (stick.drag) { stickTouch(t); return; }
      state.px = p.x; state.py = p.y;
    }, opts);
    canvas.addEventListener('touchend', e => { e.preventDefault(); if (!e.touches.length) stick.drag = false; }, opts);

    window.addEventListener('keydown', e => {
      const k = e.key.toLowerCase();
      if (['arrowleft', 'a'].includes(k)) state.keyLeft = true;
      else if (['arrowright', 'd'].includes(k)) state.keyRight = true;
      else if (['arrowup', 'w'].includes(k)) state.keyUp = true;
      else if (['arrowdown', 's'].includes(k)) state.keyDown = true;
      else if (k === ' ') { state.launch = true; e.preventDefault(); }
      else if (k === 'm') { AudioSys.toggleMute(); }
      else return;
      if (['arrowleft', 'arrowright', 'arrowup', 'arrowdown', ' '].includes(k)) e.preventDefault();
      Input.resumeAudio();
    });
    window.addEventListener('keyup', e => {
      const k = e.key.toLowerCase();
      if (['arrowleft', 'a'].includes(k)) state.keyLeft = false;
      else if (['arrowright', 'd'].includes(k)) state.keyRight = false;
      else if (['arrowup', 'w'].includes(k)) state.keyUp = false;
      else if (['arrowdown', 's'].includes(k)) state.keyDown = false;
    });
  }

  function stickTouch(t) {
    const p = toLogical(t.clientX, t.clientY);
    let dx = p.x - stick.ox, dy = p.y - stick.oy;
    const d = Math.hypot(dx, dy);
    if (d > stick.R) { dx *= stick.R / d; dy *= stick.R / d; }
    stick.dx = dx; stick.dy = dy;
    // flick up on the stick = punch
    const dt = (performance.now() - lastTouchT) / 1000;
    if (dt > 0 && dt < 0.15 && (lastTouchY - p.y) / dt > 600) state.punch = true;
    lastTouchY = p.y; lastTouchT = performance.now();
  }

  // called each frame: resolve paddle target velocity from all sources
  function resolve(dt) {
    state.punch = state.punch; // consumed by game
    let vx = 0, vy = 0;
    if (state.keyLeft) vx -= CONFIG.PADDLE_X_SPEED;
    if (state.keyRight) vx += CONFIG.PADDLE_X_SPEED;
    if (state.keyUp) vy -= CONFIG.PADDLE_Y_SPEED;
    if (state.keyDown) vy += CONFIG.PADDLE_Y_SPEED;
    if (stick.drag) {
      const nx = stick.dx / stick.R, ny = stick.dy / stick.R;
      vx += nx * CONFIG.STICK_MAX_SPEED;
      vy += ny * CONFIG.STICK_MAX_SPEED * 0.7;
    }
    state.stickVX = vx; state.stickVY = vy;
    return state;
  }
  function consumeLaunch() { const l = state.launch; state.launch = false; return l; }
  function consumePunch() { const p = state.punch; state.punch = false; return p; }

  // Virtual thumbstick DOM element (bottom-left nub)
  function attachStick(el) {
    stickEl = el;
    el.addEventListener('touchstart', e => {
      e.preventDefault(); e.stopPropagation(); Input.resumeAudio();
      const t = e.touches[0];
      const r = el.getBoundingClientRect();
      stick.ox = r.left + r.width / 2; stick.oy = r.top + r.height / 2;
      stick.drag = true; state.useStick = true;
      lastTouchY = toLogical(t.clientX, t.clientY).y; lastTouchT = performance.now();
    }, { passive: false });
    el.addEventListener('touchmove', e => {
      e.preventDefault();
      if (!stick.drag) return;
      stickTouch(e.touches[0]);
    }, { passive: false });
    el.addEventListener('touchend', e => { e.preventDefault(); stick.drag = false; stick.dx = stick.dy = 0; }, { passive: false });
  }
  function setStickVisible(v) { if (stickEl) stickEl.style.display = v ? 'flex' : 'none'; }

  function clearKeys() {
    state.keyLeft = state.keyRight = state.keyUp = state.keyDown = false;
    stick.drag = false; stick.dx = stick.dy = 0;
  }

  return Object.assign(state, { setup, attachStick, setStickVisible, resolve,
    consumeLaunch, consumePunch, clearKeys, stick,
    resumeAudio: () => AudioSys.resume() });
})();