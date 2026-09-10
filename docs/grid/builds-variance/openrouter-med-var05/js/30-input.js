/* SKYBREAK — 30-input.js → window.Input
 * Unified input singleton: mouse pointer, touch virtual joystick, keyboard.
 * Virtual space 900×1340 (U.VW/U.VH). Maps real client px → virtual units via
 * the letterbox transform (scale = min(cw/900, ch/1340), centered).
 * No DOM creation, no styles. Game draws joystick overlay from Input.joy.
 */
(function () {
  'use strict';
  var W = typeof window !== 'undefined' ? window : global;
  var U = W.U || { VW: 900, VH: 1340, clamp: function (v, a, b) { return v < a ? a : (v > b ? b : v); } };

  var VW = U.VW || 900, VH = U.VH || 1340;
  var ZONE_TOP = 1080, ZONE_BOT = 1180;
  var PX_MIN = 30, PX_MAX = 870;
  var JOY_R = 70, JOY_DEAD = 12, JOY_START_FRAC = 0.62; // bottom 38%
  var TAP_MS = 250, TAP_PX = 10;
  var KEY_RAMP = 120; // ms to full lift

  function Input() {
    this.canvas = null;
    this.hud = null;
    // paddle target (virtual units)
    this.px = VW / 2;
    this.py = 1130;
    this._pxT = VW / 2;
    this._pyT = 1130;
    // smoothed horizontal axis from keys
    this.axisX = 0;
    this._axisT = 0;
    // pointer state
    this.pointerDown = false;
    this._lastCX = 0;
    this._lastCY = 0;
    this._pointerActive = true; // pointer controls paddle until keyboard used
    // joystick state (real client px)
    this.joy = { active: false, bx: 0, by: 0, tx: 0, ty: 0, id: -1 };
    this._joyLift = 0;
    // keyboard
    this._keys = Object.create(null);
    this._keyLift = 0;
    // edge-triggered flags (Game polls, they auto-clear on read)
    this.launchPressed = false;
    this.pausePressed = false;
    this.mutePressed = false;
    this.onBlur = null;
    this._tapT = 0; this._tapX = 0; this._tapY = 0;
  }

  var inp = W.Input = new Input();
  var P = Input.prototype;

  /* ---- mapping real px → virtual units ---- */
  // The canvas fills the window; letterbox transform: scale=min(cw/VW,ch/VH),
  // centered. Inverse: vx = (cx - ox) / scale, vy = (cy - oy) / scale.
  P.transform = function () {
    var el = this.canvas;
    var cw, ch;
    if (el && el.getBoundingClientRect) {
      var r = el.getBoundingClientRect();
      cw = r.width; ch = r.height;
    } else {
      cw = (typeof W.innerWidth === 'number') ? W.innerWidth : VW;
      ch = (typeof W.innerHeight === 'number') ? W.innerHeight : VH;
    }
    var s = Math.min(cw / VW, ch / VH);
    return { s: s, ox: (cw - VW * s) / 2, oy: (ch - VH * s) / 2, cw: cw, ch: ch };
  };

  P.toVirtual = function (clientX, clientY) {
    var t = this.transform();
    return { x: (clientX - t.ox) / t.s, y: (clientY - t.oy) / t.s };
  };

  /* ---- public reads ---- */
  // 0..1 upward intent = max of active sources.
  P.lift = function () {
    var v = 0;
    // (a) pointer drag inside paddle zone
    if (this.pointerDown) {
      var py = this.py;
      if (py >= ZONE_TOP && py <= ZONE_BOT) {
        v = Math.max(v, U.clamp((ZONE_BOT - py) / (ZONE_BOT - ZONE_TOP), 0, 1));
      }
    }
    // (b) joystick thumb
    if (this._joyLift > 0) v = Math.max(v, this._joyLift);
    // (c) keys
    if (this._keyLift > 0) v = Math.max(v, this._keyLift);
    return v;
  };

  P.consumeLaunch = function () { var v = this.launchPressed; this.launchPressed = false; return v; };
  P.consumePause = function () { var v = this.pausePressed; this.pausePressed = false; return v; };
  P.consumeMute = function () { var v = this.mutePressed; this.mutePressed = false; return v; };

  // per-frame smoothing; Game may call, but lift/axis keys ramp need dt too.
  P.update = function (dtMs) {
    var k = dtMs > 0 ? U.clamp(dtMs / KEY_RAMP, 0, 1) : 1;
    var keys = this._keys;
    var upTgt = (keys.KeyW || keys.ArrowUp) ? 1 : 0;
    var dn = (keys.KeyS || keys.ArrowDown);
    if (upTgt && !dn) this._keyLift = this._keyLift + (upTgt - this._keyLift) * k;
    else if (dn) this._keyLift = this._keyLift * (1 - k); // sink
    else this._keyLift = 0;
    // horizontal axis inertia
    var aTgt = 0;
    if (keys.KeyA || keys.ArrowLeft) aTgt -= 1;
    if (keys.KeyD || keys.ArrowRight) aTgt += 1;
    this._axisT = aTgt;
    if (aTgt !== 0) this._pointerActive = false; // keyboard takes over
    var ax = this.axisX + (this._axisT - this.axisX) * U.clamp(dtMs / 90, 0, 1);
    this.axisX = U.clamp(ax, -1, 1);
    // smooth paddle target toward pointer target
    var f = 1 - Math.exp(-(dtMs / 1000) * 20);
    this.px += (this._pxT - this.px) * f;
    this.py += (this._pyT - this.py) * f;
  };

  /* ---- internal setters ---- */
  function setPointerTarget(x, y) {
    inp._pxT = U.clamp(x, PX_MIN, PX_MAX);
    inp._pyT = U.clamp(y, ZONE_TOP, ZONE_BOT);
    inp.px = inp._pxT; inp.py = inp._pyT; // deliver sane values immediately
    inp._pointerActive = true;
  }

  function joystickLift() {
    var j = inp.joy;
    if (!j.active) { inp._joyLift = 0; return; }
    var dx = j.tx - j.bx, dy = j.ty - j.by;
    var m = Math.sqrt(dx * dx + dy * dy);
    if (m <= JOY_DEAD) { inp._joyLift = 0; return; }
    // normalized upward component, -down/+up
    var up = -dy / JOY_R;
    inp._joyLift = U.clamp(up, 0, 1);
    // horizontal maps across field
    var hx = U.clamp(dx / JOY_R, -1, 1);
    setPointerTarget((hx * 0.5 + 0.5) * VW, ZONE_BOT - inp._joyLift * (ZONE_BOT - ZONE_TOP));
  }

  /* ---- event helpers ---- */
  function isUI(e) {
    var t = e.target;
    if (t && t.closest) {
      try { return t.closest('.hud-btn,.big-btn') !== null; } catch (_) { return false; }
    }
    return false;
  }
  function buttonFocused() {
    var a = (typeof document !== 'undefined') && document.activeElement;
    if (!a || !a.tagName) return false;
    var tag = a.tagName.toLowerCase();
    return tag === 'button' || tag === 'a' || tag === 'input' || tag === 'select' || tag === 'textarea';
  }
  function gameKeyAllowed() { return !buttonFocused(); }

  function endJoystick(j) {
    j.active = false; j.id = -1;
    inp._joyLift = 0;
  }

  /* ---- pointer events (preferred) ---- */
  var HAS_POINTER = typeof W.PointerEvent !== 'undefined';

  function onPointerDown(e) {
    if (e.button !== undefined && e.button !== 0) return; // primary only
    if (isUI(e)) return;
    inp.pointerDown = true;
    inp._lastCX = e.clientX; inp._lastCY = e.clientY;
    var v = inp.toVirtual(e.clientX, e.clientY);
    setPointerTarget(v.x, v.y);
    inp._tapT = Date.now(); inp._tapX = e.clientX; inp._tapY = e.clientY;
    // touch via pointer events: first touch in bottom 38% = joystick
    if (e.pointerType === 'touch') {
      var t = inp.transform();
      if (!inp.joy.active && e.clientY >= t.ch * JOY_START_FRAC) {
        inp.joy.active = true; inp.joy.id = e.pointerId;
        inp.joy.bx = e.clientX; inp.joy.by = e.clientY;
        inp.joy.tx = e.clientX; inp.joy.ty = e.clientY;
      }
    }
  }
  function onPointerMove(e) {
    if (isUI(e)) return;
    var j = inp.joy;
    if (j.active && e.pointerId === j.id) {
      j.tx = e.clientX; j.ty = e.clientY;
      joystickLift();
      var dx = j.tx - j.bx, dy = j.ty - j.by;
      if (dx * dx + dy * dy > TAP_PX * TAP_PX) { inp._tapT = 0; }
      return;
    }
    if (e.pointerType === 'touch' && e.pointerId !== j.id && !inp.pointerDown) return;
    if (!inp.pointerDown && e.pointerType === 'touch') return; // touch hover n/a
    if (e.buttons !== undefined && e.buttons === 0 && e.pointerType === 'mouse') {
      // hover steering
    }
    inp._lastCX = e.clientX; inp._lastCY = e.clientY;
    var v = inp.toVirtual(e.clientX, e.clientY);
    setPointerTarget(v.x, v.y);
  }
  function onPointerUp(e) {
    if (isUI(e)) return;
    var j = inp.joy;
    if (j.active && e.pointerId === j.id) {
      endJoystick(j);
      inp.pointerDown = false;
      return;
    }
    var dt = Date.now() - inp._tapT;
    var mdx = e.clientX - inp._tapX, mdy = e.clientY - inp._tapY;
    if (inp._tapT && dt < TAP_MS && mdx * mdx + mdy * mdy < TAP_PX * TAP_PX) {
      inp.launchPressed = true;
    }
    inp.pointerDown = false;
    inp._tapT = 0;
  }

  /* ---- touch fallback (non-pointer browsers) ---- */
  function onTouchStart(e) {
    if (isUI(e)) return;
    if (e.preventDefault) e.preventDefault();
    var t = inp.transform();
    var ch = t.ch;
    for (var i = 0; i < e.changedTouches.length; i++) {
      var tc = e.changedTouches[i];
      if (!inp.joy.active && tc.clientY >= ch * JOY_START_FRAC) {
        inp.joy.active = true; inp.joy.id = tc.identifier;
        inp.joy.bx = tc.clientX; inp.joy.by = tc.clientY;
        inp.joy.tx = tc.clientX; inp.joy.ty = tc.clientY;
        inp.pointerDown = true;
        inp._tapT = 0; // joystick start is not a tap
      } else if (inp.joy.active && inp._touchCount() >= 1) {
        // second simultaneous touch = launch
        inp.launchPressed = true;
      } else {
        // single tap candidate
        inp.pointerDown = true;
        inp._tapT = Date.now(); inp._tapX = tc.clientX; inp._tapY = tc.clientY;
        var v = inp.toVirtual(tc.clientX, tc.clientY);
        setPointerTarget(v.x, v.y);
      }
    }
  }
  inp._touchCount = function () {
    // approx: joystick counts as 1
    return inp.joy.active ? 1 : 0;
  };
  function onTouchMove(e) {
    if (isUI(e)) return;
    if (e.preventDefault) e.preventDefault();
    var j = inp.joy;
    for (var i = 0; i < e.changedTouches.length; i++) {
      var tc = e.changedTouches[i];
      if (j.active && tc.identifier === j.id) {
        j.tx = tc.clientX; j.ty = tc.clientY;
        joystickLift();
        var dx = j.tx - j.bx, dy = j.ty - j.by;
        if (dx * dx + dy * dy > TAP_PX * TAP_PX) inp._tapT = 0;
      } else if (inp.pointerDown) {
        var v = inp.toVirtual(tc.clientX, tc.clientY);
        setPointerTarget(v.x, v.y);
      }
    }
  }
  function onTouchEnd(e) {
    if (isUI(e)) return;
    var j = inp.joy;
    for (var i = 0; i < e.changedTouches.length; i++) {
      var tc = e.changedTouches[i];
      if (j.active && tc.identifier === j.id) {
        endJoystick(j);
        inp.pointerDown = false;
      } else if (inp._tapT && inp.pointerDown) {
        var dt = Date.now() - inp._tapT;
        var mdx = tc.clientX - inp._tapX, mdy = tc.clientY - inp._tapY;
        if (dt < TAP_MS && mdx * mdx + mdy * mdy < TAP_PX * TAP_PX) inp.launchPressed = true;
        inp.pointerDown = false;
        inp._tapT = 0;
      }
    }
  }

  /* ---- keyboard ---- */
  var GAME_KEYS = {
    Space: 1, ArrowUp: 1, ArrowDown: 1, ArrowLeft: 1, ArrowRight: 1,
    KeyW: 1, KeyA: 1, KeyS: 1, KeyD: 1
  };
  function onKeyDown(e) {
    var code = e.code || e.key;
    if (isUI(e)) return;
    if (code === 'KeyP') {
      if (!e.repeat) inp.pausePressed = true;
      return;
    }
    if (code === 'KeyM') {
      if (!e.repeat) inp.mutePressed = true;
      return;
    }
    if (code === 'Space' || code === 'Enter') {
      if (!e.repeat && gameKeyAllowed()) {
        inp.launchPressed = true;
        if (e.preventDefault && GAME_KEYS[code]) e.preventDefault();
      }
      return;
    }
    if (GAME_KEYS[code]) {
      if (gameKeyAllowed() && e.preventDefault) e.preventDefault();
      if (!e.repeat) { /* hold semantics */ }
      inp._keys[code] = true;
      if (code === 'ArrowLeft' || code === 'KeyA') inp._axisT = -1;
      if (code === 'ArrowRight' || code === 'KeyD') inp._axisT = 1;
    }
  }
  function onKeyUp(e) {
    var code = e.code || e.key;
    inp._keys[code] = false;
    if ((code === 'ArrowLeft' || code === 'KeyA') && !inp._keys.ArrowRight && !inp._keys.KeyD) inp._axisT = 0;
    if ((code === 'ArrowRight' || code === 'KeyD') && !inp._keys.ArrowLeft && !inp._keys.KeyA) inp._axisT = 0;
    if ((code === 'KeyW' || code === 'ArrowUp') && !inp._keys.KeyS && !inp._keys.ArrowDown) inp._keyLiftHold = false;
    if ((code === 'KeyS' || code === 'ArrowDown') && !inp._keys.KeyW && !inp._keys.ArrowUp) { }
  }

  /* ---- blur ---- */
  function onBlurEvt() { if (typeof inp.onBlur === 'function') inp.onBlur(); inp._keys = {}; inp.axisX = 0; inp._axisT = 0; inp._keyLift = 0; inp.pointerDown = false; endJoystick(inp.joy); }

  /* ---- attach ---- */
  P.attach = function (canvasEl, hudEl) {
    this.canvas = canvasEl || null;
    this.hud = hudEl || null;
    var el = canvasEl;
    if (!el) return;
    var doc = (typeof document !== 'undefined') ? document : null;
    var opts = { passive: false };

    if (HAS_POINTER) {
      el.addEventListener('pointerdown', onPointerDown, opts);
      el.addEventListener('pointermove', onPointerMove, opts);
      el.addEventListener('pointerup', onPointerUp, opts);
      el.addEventListener('pointercancel', onPointerUp, opts);
    } else {
      // mouse fallback
      el.addEventListener('mousedown', function (e) { onPointerDown({ button: e.button, clientX: e.clientX, clientY: e.clientY, target: e.target, pointerType: 'mouse' }); }, opts);
      doc && doc.addEventListener('mousemove', function (e) { onPointerMove({ clientX: e.clientX, clientY: e.clientY, target: e.target, pointerType: 'mouse' }); }, opts);
      doc && doc.addEventListener('mouseup', function (e) { onPointerUp({ clientX: e.clientX, clientY: e.clientY, target: e.target, pointerType: 'mouse' }); }, opts);
      // touch fallback
      if ('ontouchstart' in W) {
        el.addEventListener('touchstart', onTouchStart, opts);
        el.addEventListener('touchmove', onTouchMove, opts);
        el.addEventListener('touchend', onTouchEnd, opts);
        el.addEventListener('touchcancel', onTouchEnd, opts);
      }
    }
    // prevent context menu on long press
    el.addEventListener('contextmenu', function (e) { if (e.preventDefault) e.preventDefault(); });

    // wheel preventDefault (page scroll)
    el.addEventListener('wheel', function (e) { if (e.preventDefault) e.preventDefault(); }, opts);

    if (doc) {
      doc.addEventListener('keydown', onKeyDown, false);
      doc.addEventListener('keyup', onKeyUp, false);
      W.addEventListener && W.addEventListener('blur', onBlurEvt, false);
      doc.addEventListener && doc.addEventListener('visibilitychange', function () { if (doc.hidden && typeof inp.onBlur === 'function') inp.onBlur(); }, false);
    }
  };

  // expose a tick for keyboard ramping (Game calls with dt ms)
  P.tick = function (dtMs) { this.update(dtMs); };

  // default init of tap state
  inp._tapY = 0;
})();