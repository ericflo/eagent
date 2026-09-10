'use strict';
// ---------------------------------------------------------------------------
// input.js — unified controls: mouse, touch (drag + virtual thumbstick),
// keyboard. Produces a control state: paddle target x/y, vertical "lift"
// impulse, action button (launch/laser).
// ---------------------------------------------------------------------------

const Input = (() => {
  // canvas is defined in main.js (loads later) — resolve it lazily so this
  // module can attach listeners before main.js runs.
  const canvas = document.getElementById('game');
  // control modes
  const MODE = { POINTER: 'pointer', STICK: 'stick', KEYBOARD: 'keyboard' };
  let mode = MODE.POINTER;

  const state = {
    // paddle position target in game coords (set by game via mapping)
    pointerX: null, pointerY: null,    // latest pointer pos in canvas px
    stickActive: false,
    stickVec: { x: 0, y: 0 },          // -1..1 each axis
    stickOrigin: null,                 // {x,y} in canvas px
    stickPos: null,                    // current thumb pos in canvas px
    keys: new Set(),
    actionPressed: false,              // edge-triggered
    actionHeld: false,
    lift: 0,                           // -1..1 vertical velocity intent (for paddle up-boost)
    lastInputWasTouch: false,
  };

  // ---- keyboard -----------------------------------------------------------
  const KEYMAP = {
    ArrowLeft: 'left', ArrowRight: 'right', ArrowUp: 'up', ArrowDown: 'down',
    KeyA: 'left', KeyD: 'right', KeyW: 'up', KeyS: 'down',
    Space: 'action', Enter: 'action', KeyZ: 'action',
  };

  window.addEventListener('keydown', e => {
    const m = KEYMAP[e.code];
    if (m) {
      e.preventDefault();
      if (m === 'action' && !state.keys.has('action')) {
        state.actionPressed = true;
      }
      state.keys.add(m);
      mode = MODE.KEYBOARD;
    }
  });
  window.addEventListener('keyup', e => {
    const m = KEYMAP[e.code];
    if (m) state.keys.delete(m);
  });

  // ---- pointer (mouse) ----------------------------------------------------
  function canvasPos(e) {
    const r = canvas.getBoundingClientRect();
    const cx = (e.clientX - r.left) * (canvas.width / r.width);
    const cy = (e.clientY - r.top) * (canvas.height / r.height);
    return { x: cx, y: cy };
  }

  canvas.addEventListener('mousemove', e => {
    if (state.lastInputWasTouch) return;
    const p = canvasPos(e);
    state.pointerX = p.x; state.pointerY = p.y;
    if (mode !== MODE.KEYBOARD) mode = MODE.POINTER;
  });

  // ---- touch: direct-drag OR virtual thumbstick (auto by touch location) --
  // Touching the lower-left sixth of the screen spawns a floating thumbstick;
  // anywhere else drags the paddle directly. This makes thumb play first-class.
  const STICK_ZONE = 0.28; // fraction of width from left where stick spawns
  const STICK_R = 70;      // stick radius in canvas px (scaled later)

  function isStickZone(p) {
    return p.x < W * STICK_ZONE && p.y > H * 0.45;
  }

  function handleTouchStart(e) {
    e.preventDefault();
    state.lastInputWasTouch = true;
    for (const t of e.changedTouches) {
      const p = canvasPos(t);
      if (!state.stickActive && isStickZone(p)) {
        state.stickActive = true;
        state.stickOrigin = p;
        state.stickPos = p;
        state.stickId = t.identifier;
        mode = MODE.STICK;
      } else {
        state.pointerX = p.x; state.pointerY = p.y;
        state.dragId = t.identifier;
        if (mode !== MODE.STICK) mode = MODE.POINTER;
        state.actionPressed = true; // tap = launch/action
      }
    }
  }

  function handleTouchMove(e) {
    e.preventDefault();
    for (const t of e.changedTouches) {
      const p = canvasPos(t);
      if (state.stickActive && t.identifier === state.stickId) {
        state.stickPos = p;
        const dx = p.x - state.stickOrigin.x;
        const dy = p.y - state.stickOrigin.y;
        const len = Math.hypot(dx, dy) || 1;
        const dead = 8;
        const eff = clamp((len - dead) / (STICK_R - dead), 0, 1);
        state.stickVec.x = len > dead ? (dx / len) * eff : 0;
        state.stickVec.y = len > dead ? (dy / len) * eff : 0;
      } else if (t.identifier === state.dragId) {
        state.pointerX = p.x; state.pointerY = p.y;
      }
    }
  }

  function handleTouchEnd(e) {
    e.preventDefault();
    for (const t of e.changedTouches) {
      if (state.stickActive && t.identifier === state.stickId) {
        state.stickActive = false;
        state.stickVec.x = 0; state.stickVec.y = 0;
      }
      if (t.identifier === state.dragId) {
        state.dragId = null;
      }
    }
  }

  canvas.addEventListener('touchstart', handleTouchStart, { passive: false });
  canvas.addEventListener('touchmove', handleTouchMove, { passive: false });
  canvas.addEventListener('touchend', handleTouchEnd, { passive: false });
  canvas.addEventListener('touchcancel', handleTouchEnd, { passive: false });

  // mouse click = action
  canvas.addEventListener('mousedown', e => {
    if (state.lastInputWasTouch) return;
    const p = canvasPos(e);
    state.pointerX = p.x; state.pointerY = p.y;
    state.actionPressed = true;
    state.actionHeld = true;
  });
  window.addEventListener('mouseup', () => { state.actionHeld = false; });

  // Generic gamepad support (bonus)
  window.addEventListener('gamepadconnected', e => {
    console.log('Gamepad connected:', e.gamepad.id);
  });

  function pollGamepad() {
    const pads = navigator.getGamepads ? navigator.getGamepads() : [];
    for (const pad of pads) {
      if (!pad) continue;
      const ax = pad.axes[0] || 0, ay = pad.axes[1] || 0;
      if (Math.abs(ax) > 0.15 || Math.abs(ay) > 0.15) {
        state.stickVec.x = Math.abs(ax) > 0.15 ? ax : 0;
        state.stickVec.y = Math.abs(ay) > 0.15 ? ay : 0;
        state.stickActive = true;
        mode = MODE.STICK;
      } else if (mode === MODE.STICK) {
        state.stickVec.x = 0; state.stickVec.y = 0;
        state.stickActive = false;
      }
      if (pad.buttons[0] && pad.buttons[0].pressed) {
        if (!state._padAction) state.actionPressed = true;
        state._padAction = true;
      } else state._padAction = false;
      return;
    }
  }

  // Called once per frame by main loop, before game update.
  function update() {
    pollGamepad();
    // keyboard → stick vec
    if (mode === MODE.KEYBOARD) {
      state.stickVec.x = (state.keys.has('right') ? 1 : 0) - (state.keys.has('left') ? 1 : 0);
      state.stickVec.y = (state.keys.has('down') ? 1 : 0) - (state.keys.has('up') ? 1 : 0);
    }
    state.lift = state.stickVec.y;
  }

  function consumeAction() {
    const a = state.actionPressed;
    state.actionPressed = false;
    return a;
  }

  return { state, update, consumeAction, MODE, get mode() { return mode; } };
})();
