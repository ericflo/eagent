// input.js — unified input: mouse, touch (thumb model), keyboard, gamepad.
// Produces a normalized state object consumed by the game each frame:
//   {
//     paddleTarget: {x, y},   // pointer-driven paddle target (null if not from pointer)
//     pointerActive: bool,
//     keys: { left, right, up, down },
//     launchHeld: bool,       // mouse down / touch two-finger / space / gamepad btn0
//     launchPressed: bool,    // edge-detect this frame (consumed by game)
//     actions: { pause, mute, restart, confirm } // edge events this frame
//   }
// Also attaches the touch-action/scroll prevention required by the spec.

export class Input {
  constructor(canvas, handlers) {
    this.canvas = canvas;
    this.handlers = handlers || {}; // onAction(name)

    // Pointer state (mouse or single touch)
    this.pointer = null;        // {x, y} in viewport CSS pixels
    this.pointerDown = false;
    this.touchStart = null;     // finger pos (canvas-relative CSS px) at touchstart
    this._paddlePosCSS = null;  // {x,y} paddle center in viewport CSS px (set by game)
    // The game sets this to a closure returning the paddle center in CSS px;
    // the thumb model captures it at touchstart so the finger never covers
    // the paddle.
    this.getPaddlePos = null;
    this.lastTouchTime = 0;
    this.twoFingerLaunch = false;

    this.keys = { left: false, right: false, up: false, down: false };
    this.spaceHeld = false;
    this.spaceWasDown = false;

    this._gamepadConnected = false;
    this._gpPrevButtons = [];
    this._gpPrevAxes = { x: 0, y: 0 };
    this._pendingActions = [];

    this._bind();
  }

  _bind() {
    const c = this.canvas;

    // Mouse
    window.addEventListener('mousemove', (e) => {
      this.pointer = { x: e.clientX, y: e.clientY };
    });
    window.addEventListener('mousedown', (e) => {
      if (e.button !== 0) return;
      this.pointer = { x: e.clientX, y: e.clientY };
      this.pointerDown = true;
      this._launchPressed = true;
    });
    window.addEventListener('mouseup', (e) => {
      if (e.button !== 0) return;
      this.pointerDown = false;
    });

    // Touch — thumb model: capture finger offset at touchstart so the finger
    // never covers the paddle; vertical drag moves paddle y. Two-finger tap
    // launches.
    c.addEventListener('touchstart', (e) => {
      e.preventDefault();
      this.lastTouchTime = performance.now();
      const t = e.changedTouches[0];
      if (e.touches.length >= 2) {
        // Two-finger tap = launch
        this.twoFingerLaunch = true;
        this._launchPressed = true;
        this._onAction('launch');
        return;
      }
      const rect = c.getBoundingClientRect();
      this.touchStart = { x: t.clientX - rect.left, y: t.clientY - rect.top };
      // Capture paddle center (CSS px, canvas-relative) at touchstart so the
      // finger offset is preserved — finger never covers the paddle.
      this._paddlePosCSS = this.getPaddlePos ? this.getPaddlePos() : null;
      this.pointerDown = true;
      this._pointerFromTouch(e.touches[0], rect);
    }, { passive: false });

    c.addEventListener('touchmove', (e) => {
      e.preventDefault(); // prevent scroll/zoom
      if (this.touchStart && e.touches.length === 1) {
        const rect = c.getBoundingClientRect();
        const t = e.touches[0];
        // Move paddle by finger delta (offset preserved from touchstart)
        this._pointerFromTouch(t, rect, true);
      }
    }, { passive: false });

    c.addEventListener('touchend', (e) => {
      e.preventDefault();
      if (e.touches.length === 0) {
        this.pointerDown = false;
        this.touchStart = null;
      }
      this.twoFingerLaunch = false;
    }, { passive: false });

    // Keyboard
    window.addEventListener('keydown', (e) => {
      const k = e.key;
      if (['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', ' '].includes(k)) e.preventDefault();
      if (k === 'a' || k === 'A' || k === 'ArrowLeft') this.keys.left = true;
      if (k === 'd' || k === 'D' || k === 'ArrowRight') this.keys.right = true;
      if (k === 'w' || k === 'W' || k === 'ArrowUp') this.keys.up = true;
      if (k === 's' || k === 'S' || k === 'ArrowDown') this.keys.down = true;
      if (k === ' ') {
        if (!this.spaceWasDown) { this._launchPressed = true; }
        this.spaceWasDown = true;
        this.spaceHeld = true;
      }
      if (k === 'p' || k === 'P' || k === 'Escape') this._onAction('pause');
      if (k === 'm' || k === 'M') this._onAction('mute');
      if (k === 'r' || k === 'R') this._onAction('restart');
      if (k === 'Enter') this._onAction('confirm');
    });
    window.addEventListener('keyup', (e) => {
      const k = e.key;
      if (k === 'a' || k === 'A' || k === 'ArrowLeft') this.keys.left = false;
      if (k === 'd' || k === 'D' || k === 'ArrowRight') this.keys.right = false;
      if (k === 'w' || k === 'W' || k === 'ArrowUp') this.keys.up = false;
      if (k === 's' || k === 'S' || k === 'ArrowDown') this.keys.down = false;
      if (k === ' ') { this.spaceWasDown = false; this.spaceHeld = false; }
    });

    // Prevent double-tap zoom on iOS
    c.addEventListener('dblclick', (e) => e.preventDefault());
    document.addEventListener('gesturestart', (e) => e.preventDefault());
  }

  _pointerFromTouch(t, rect, isMove) {
    const x = t.clientX - rect.left;
    const y = t.clientY - rect.top;
    if (isMove && this.touchStart && this._paddlePosCSS) {
      // Apply finger delta (CSS px) to captured paddle pos. Emitted in
      // viewport CSS pixels: canvas offset + captured position + delta.
      this.pointer = {
        x: rect.left + this._paddlePosCSS.x + (x - this.touchStart.x),
        y: rect.top + this._paddlePosCSS.y + (y - this.touchStart.y)
      };
    } else {
      this.pointer = { x: rect.left + x, y: rect.top + y };
    }
  }

  // Game calls this once with a closure returning the paddle center in
  // canvas-relative CSS pixels; used by the thumb offset model.
  setPaddlePosProvider(fn) {
    this.getPaddlePos = fn;
  }

  _onAction(name) {
    this._pendingActions = this._pendingActions || [];
    this._pendingActions.push(name);
    if (this.handlers.onAction) this.handlers.onAction(name);
  }

  _pollGamepad() {
    let pads = [];
    try { pads = navigator.getGamepads ? navigator.getGamepads() : []; } catch (e) { pads = []; }
    this._gp = null;
    for (let i = 0; i < pads.length; i++) {
      if (pads[i]) { this._gp = pads[i]; break; }
    }
    if (this._gp && !this._gamepadConnected) {
      this._gamepadConnected = true;
      this._pendingActions = this._pendingActions || [];
      this._pendingActions.push('gamepadDetected');
    }
    if (!this._gp) return;
    const ax = (this._gp.axes[0] || 0);
    const ay = (this._gp.axes[1] || 0);
    const dead = (v) => Math.abs(v) < 0.18 ? 0 : v;
    this._gpAxes = { x: dead(ax), y: dead(ay) };
    // Buttons: 0 = launch/hold, 9 = pause
    if (this._gp.buttons[0] && this._gp.buttons[0].pressed && !this._gpPrevButtons[0]) {
      this._launchPressed = true;
    }
    if (this._gp.buttons[9] && this._gp.buttons[9].pressed && !this._gpPrevButtons[9]) {
      this._onAction('pause');
    }
    this._gpPrevButtons = this._gp.buttons.map(b => b && b.pressed);
    this._gpBtn0 = !!(this._gp.buttons[0] && this._gp.buttons[0].pressed);
  }

  // Call once per frame BEFORE the game reads state.
  update(dt) {
    this._pollGamepad();
    this.state = {
      paddleTarget: null,
      pointerActive: false,
      keys: { ...this.keys },
      launchHeld: false,
      launchPressed: false,
      actions: this._pendingActions.splice(0, this._pendingActions.length)
    };

    // Pointer (mouse or touch) target
    if (this.pointer) {
      this.state.paddleTarget = { ...this.pointer };
      this.state.pointerActive = true;
    }
    // Gamepad axes override pointer if active
    if (this._gpAxes && (this._gpAxes.x !== 0 || this._gpAxes.y !== 0)) {
      this.state.paddleTarget = null; // game will integrate velocity
      this.state.gamepadVel = this._gpAxes;
      this.state.pointerActive = false;
    }

    // Launch held: mouse down OR space OR two-finger OR gamepad btn0
    this.state.launchHeld =
      this.pointerDown ||
      this.spaceHeld ||
      this.twoFingerLaunch ||
      !!this._gpBtn0;
    this.state.launchPressed = !!this._launchPressed;
    this._launchPressed = false;
  }

  // Convert a viewport CSS pixel point into field coords given the letterbox
  // transform (ctx scale + translate). Returns {x, y} in field units or null.
  toField(vx, vy, transform) {
    if (!transform) return null;
    return {
      x: (vx - transform.tx) / transform.scale,
      y: (vy - transform.ty) / transform.scale
    };
  }
}
