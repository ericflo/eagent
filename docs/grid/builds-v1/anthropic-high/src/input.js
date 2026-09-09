// input.js — mouse, keyboard, first-class touch (dynamic thumbstick or drag mode)
// and gamepad. Produces one command per frame for the paddle:
//   { mode: 'drive',  dx, dy }   velocity-driven (keys / stick / gamepad)
//   { mode: 'target', x, y }     position-driven (mouse / drag mode)

import { clamp } from './util.js';

const STICK_RADIUS = 74;
const STICK_DEAD = 9;
/** A press shorter than this, that moved less than TAP_SLOP, is a tap => fire. */
const TAP_MS = 180;
const TAP_SLOP = 10;
/** Fraction of the screen height (from the bottom) where a thumbstick may spawn. */
const STICK_BAND = 0.6;
const PAD_DEAD = 0.22;

export class Input {
  /**
   * @param {HTMLElement} target  element receiving pointer events (the canvas)
   * @param {object} opts { toLogical(clientX, clientY) -> {x,y}, onFire(), onPause(),
   *                        onButton(id), field, isDragMode(), isMenu(), getPaddle() }
   */
  constructor(target, opts) {
    this.el = target;
    this.opts = opts;
    this.field = opts.field;

    this.keys = new Set();
    this.keyDrive = { x: 0, y: 0 };
    this.mouse = { x: this.field.left + (this.field.right - this.field.left) / 2, y: 0, active: false };
    this.lastMouseMove = -999;
    this.lastKeyTime = -999;

    this.pointers = new Map(); // pointerId -> {role, x, y, t0, x0, y0, moved}
    this.stick = { active: false, ox: 0, oy: 0, x: 0, y: 0, dx: 0, dy: 0, id: null };
    this.drag = { active: false, x: 0, y: 0, id: null, offX: 0, offY: 0 };
    this.buttons = [];   // [{id, x, y, w, h}] in logical coords

    // gamepad
    this.gamepad = { connected: false, dx: 0, dy: 0, index: -1 };
    this._padButtons = new Map();
    this.lastPadTime = -999;

    this._bind();
  }

  setButtons(list) { this.buttons = list; }

  _hitButton(x, y) {
    for (const b of this.buttons) {
      if (x >= b.x && x <= b.x + b.w && y >= b.y && y <= b.y + b.h) return b;
    }
    return null;
  }

  /** Lowest `STICK_BAND` of the screen, where planting a thumb makes a stick. */
  get stickBandTop() {
    return this.field.bottom * (1 - STICK_BAND);
  }

  _bind() {
    const el = this.el;
    const L = (e) => this.opts.toLogical(e.clientX, e.clientY);

    el.style.touchAction = 'none';

    el.addEventListener('pointerdown', (e) => {
      el.setPointerCapture?.(e.pointerId);
      const p = L(e);
      const btn = this._hitButton(p.x, p.y);
      if (btn) {
        this.opts.onButton?.(btn.id);
        e.preventDefault();
        return;
      }
      if (e.pointerType === 'touch') {
        this._touchDown(e.pointerId, p);
      } else {
        this.mouse.x = p.x; this.mouse.y = p.y; this.mouse.active = true;
        this.lastMouseMove = performance.now();
        this.opts.onFire?.();
      }
      e.preventDefault();
    }, { passive: false });

    el.addEventListener('pointermove', (e) => {
      const p = L(e);
      if (e.pointerType === 'touch') {
        this._touchMove(e.pointerId, p);
      } else {
        this.mouse.x = p.x;
        this.mouse.y = p.y;
        this.mouse.active = true;
        this.lastMouseMove = performance.now();
      }
      e.preventDefault();
    }, { passive: false });

    const up = (e) => {
      if (e.pointerType === 'touch') this._touchUp(e.pointerId);
      e.preventDefault();
    };
    el.addEventListener('pointerup', up, { passive: false });
    el.addEventListener('pointercancel', up, { passive: false });
    el.addEventListener('pointerleave', (e) => {
      if (e.pointerType === 'touch') this._touchUp(e.pointerId);
    });
    el.addEventListener('contextmenu', (e) => e.preventDefault());

    // Belt and braces against page scroll / pinch zoom / double-tap zoom / selection.
    for (const type of ['touchstart', 'touchmove', 'touchend']) {
      el.addEventListener(type, (e) => {
        if (e.cancelable) e.preventDefault();
      }, { passive: false });
    }
    const doc = globalThis.document;
    if (doc) {
      doc.addEventListener('touchmove', (e) => {
        if (e.cancelable) e.preventDefault();
      }, { passive: false });
      // Safari pinch gestures + double-tap zoom.
      for (const type of ['gesturestart', 'gesturechange', 'gestureend']) {
        doc.addEventListener(type, (e) => e.preventDefault(), { passive: false });
      }
      doc.addEventListener('dblclick', (e) => e.preventDefault(), { passive: false });
      doc.addEventListener('selectstart', (e) => e.preventDefault());
    }

    globalThis.addEventListener?.('keydown', (e) => {
      if (e.repeat) {
        this.lastKeyTime = performance.now();
        return;
      }
      const k = e.key.toLowerCase();
      this.keys.add(k);
      this.lastKeyTime = performance.now();
      if (k === ' ' || k === 'enter') { this.opts.onFire?.(); e.preventDefault(); }
      if (k === 'p' || k === 'escape') { this.opts.onPause?.(); e.preventDefault(); }
      if (k === 'm') this.opts.onButton?.('mute');
      if (['arrowleft', 'arrowright', 'arrowup', 'arrowdown'].includes(k)) e.preventDefault();
    });
    globalThis.addEventListener?.('keyup', (e) => this.keys.delete(e.key.toLowerCase()));
    globalThis.addEventListener?.('blur', () => this.keys.clear());

    globalThis.addEventListener?.('gamepadconnected', (e) => {
      this.gamepad.connected = true;
      this.gamepad.index = e.gamepad?.index ?? 0;
    });
    globalThis.addEventListener?.('gamepaddisconnected', () => {
      this.gamepad.connected = false;
      this.gamepad.dx = 0;
      this.gamepad.dy = 0;
    });
  }

  // -------------------------------------------------------------- touch

  _touchDown(id, p) {
    const dragMode = this.opts.isDragMode?.() ?? false;
    const now = performance.now();
    const rec = { x: p.x, y: p.y, t0: now, x0: p.x, y0: p.y, moved: 0, role: 'fire' };
    // On menus every touch acts immediately — nobody wants to tap twice to start.
    const menu = this.opts.isMenu?.() ?? false;

    if (dragMode) {
      if (!this.drag.active) {
        rec.role = 'drag';
        this.drag.active = true;
        this.drag.id = id;
        this.drag.x = p.x;
        this.drag.y = p.y;
        // Grab with an offset so the paddle doesn't teleport under the finger (and so
        // the finger doesn't sit on top of the paddle you're trying to watch).
        const pad = this.opts.getPaddle?.();
        this.drag.offX = pad ? clamp(pad.x + pad.w / 2 - p.x, -110, 110) : 0;
        this.drag.offY = pad ? clamp(pad.y + pad.h / 2 - p.y, -90, 90) : 0;
        this.pointers.set(id, rec);
        if (menu) this.opts.onFire?.();
        return;
      }
      // Second finger: always an immediate fire.
      this.pointers.set(id, rec);
      this.opts.onFire?.();
      return;
    }

    if (!this.stick.active && p.y > this.stickBandTop) {
      rec.role = 'stick';
      this.stick.active = true;
      this.stick.id = id;
      this.stick.ox = p.x;
      this.stick.oy = p.y;
      this.stick.x = p.x;
      this.stick.y = p.y;
      this.stick.dx = 0;
      this.stick.dy = 0;
      this.pointers.set(id, rec);
      // Steering thumb: it fires on release if it was a quick tap (see _touchUp).
      if (menu) this.opts.onFire?.();
      return;
    }
    // Any other touch (second finger, or a tap up top) fires / launches right away.
    this.pointers.set(id, rec);
    this.opts.onFire?.();
  }

  _touchMove(id, p) {
    const rec = this.pointers.get(id);
    if (!rec) return;
    rec.moved = Math.max(rec.moved, Math.hypot(p.x - rec.x0, p.y - rec.y0));
    rec.x = p.x; rec.y = p.y;
    if (rec.role === 'stick' && this.stick.id === id) {
      this.stick.x = p.x;
      this.stick.y = p.y;
      let dx = p.x - this.stick.ox;
      let dy = p.y - this.stick.oy;
      const m = Math.hypot(dx, dy);
      if (m > STICK_RADIUS) {
        // Drag the origin along so the stick never saturates awkwardly.
        this.stick.ox += (m - STICK_RADIUS) * (dx / m);
        this.stick.oy += (m - STICK_RADIUS) * (dy / m);
        dx = p.x - this.stick.ox;
        dy = p.y - this.stick.oy;
      }
      const mag = Math.hypot(dx, dy);
      if (mag < STICK_DEAD) {
        this.stick.dx = 0; this.stick.dy = 0;
      } else {
        const t = clamp((mag - STICK_DEAD) / (STICK_RADIUS - STICK_DEAD), 0, 1);
        const curve = t * t * 0.55 + t * 0.45; // gentle near centre, full at the rim
        this.stick.dx = (dx / mag) * curve;
        this.stick.dy = (dy / mag) * curve;
      }
    } else if (rec.role === 'drag' && this.drag.id === id) {
      this.drag.x = p.x;
      this.drag.y = p.y;
    }
  }

  _touchUp(id) {
    const rec = this.pointers.get(id);
    this.pointers.delete(id);
    if (!rec) return;
    if (rec.role === 'stick' && this.stick.id === id) {
      this.stick.active = false;
      this.stick.id = null;
      this.stick.dx = 0;
      this.stick.dy = 0;
    }
    if (rec.role === 'drag' && this.drag.id === id) {
      this.drag.active = false;
      this.drag.id = null;
    }
    // Quick tap anywhere = launch / fire. Steering drags never fire.
    const dur = performance.now() - rec.t0;
    if (rec.role !== 'fire' && dur < TAP_MS && rec.moved < TAP_SLOP) {
      this.opts.onFire?.();
    }
  }

  // -------------------------------------------------------------- gamepad

  /** Poll once per frame; left stick drives both axes, A/X fires, Start pauses. */
  pollGamepad() {
    const getPads = globalThis.navigator?.getGamepads?.bind(globalThis.navigator);
    if (!getPads) return null;
    let pads;
    try { pads = getPads(); } catch (e) { return null; }
    if (!pads) return null;
    let pad = null;
    for (const p of pads) if (p && p.connected) { pad = p; break; }
    if (!pad) {
      this.gamepad.connected = false;
      this.gamepad.dx = 0;
      this.gamepad.dy = 0;
      return null;
    }
    this.gamepad.connected = true;
    this.gamepad.index = pad.index;

    const curve = (v) => {
      const a = Math.abs(v);
      if (a < PAD_DEAD) return 0;
      const t = (a - PAD_DEAD) / (1 - PAD_DEAD);
      return Math.sign(v) * (t * t * 0.55 + t * 0.45);
    };
    let ax = curve(pad.axes?.[0] ?? 0);
    let ay = curve(pad.axes?.[1] ?? 0);
    // D-pad (standard mapping) as a fallback for sticks.
    const b = pad.buttons || [];
    const pressed = (i) => !!(b[i] && (b[i].pressed || b[i].value > 0.5));
    if (pressed(14)) ax = -1;
    if (pressed(15)) ax = 1;
    if (pressed(12)) ay = -1;
    if (pressed(13)) ay = 1;
    this.gamepad.dx = ax;
    this.gamepad.dy = ay;
    if (ax || ay) this.lastPadTime = performance.now();

    // Edge-triggered buttons: A(0) / X(2) fire, Start(9) pauses.
    const edge = (i) => {
      const now = pressed(i);
      const was = this._padButtons.get(i) || false;
      this._padButtons.set(i, now);
      return now && !was;
    };
    if (edge(0) || edge(2)) this.opts.onFire?.();
    if (edge(9)) this.opts.onPause?.();
    return this.gamepad;
  }

  // -------------------------------------------------------------- output

  /** Per-frame paddle command. */
  getCommand() {
    const now = performance.now();
    this.pollGamepad();

    // Keyboard wins while it is being used.
    let kx = 0, ky = 0;
    if (this.keys.has('arrowleft') || this.keys.has('a')) kx -= 1;
    if (this.keys.has('arrowright') || this.keys.has('d')) kx += 1;
    if (this.keys.has('arrowup') || this.keys.has('w')) ky -= 1;
    if (this.keys.has('arrowdown') || this.keys.has('s')) ky += 1;
    if (kx || ky || now - this.lastKeyTime < 220) {
      return { mode: 'drive', dx: kx, dy: ky };
    }
    if (this.stick.active) {
      return { mode: 'drive', dx: this.stick.dx, dy: this.stick.dy };
    }
    if (this.drag.active) {
      return { mode: 'target', x: this.drag.x + this.drag.offX, y: this.drag.y + this.drag.offY };
    }
    if (this.gamepad.connected && (this.gamepad.dx || this.gamepad.dy || now - this.lastPadTime < 250)) {
      return { mode: 'drive', dx: this.gamepad.dx, dy: this.gamepad.dy };
    }
    if (this.mouse.active) {
      return { mode: 'target', x: this.mouse.x, y: this.mouse.y };
    }
    return { mode: 'drive', dx: 0, dy: 0 };
  }
}
