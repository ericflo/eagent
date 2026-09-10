/* input.js — unified input: mouse, keyboard, touch (relative drag + optional
 * virtual thumbstick), gestures (tap/flick launch, double-tap pause). */
(function (root) {
  'use strict';
  var BO = root.BO = root.BO || {};

  function Input(canvas, opts) {
    var self = this;
    this.canvas = canvas;
    this.keys = { left: false, right: false, up: false, down: false };
    this.pointer = { active: false, x: 0, y: 0 }; // in field coords (set by main)
    this.pointerMode = false;   // true when last move was mouse/touch
    this.launchQueued = false;
    this.pauseQueued = false;
    this.thumbstick = { enabled: opts && opts.thumbstick !== false, visible: false,
                        active: false, ox: 0, oy: 0, dx: 0, dy: 0, id: null,
                        cx: 0, cy: 0, r: 60 };
    this.usingStick = false;
    this._stickPointerId = null;

    // returns position in CSS pixels relative to the canvas
    function pos(e) {
      var r = canvas.getBoundingClientRect();
      return { x: e.clientX - r.left, y: e.clientY - r.top };
    }

    // --- keyboard ---
    window.addEventListener('keydown', function (e) {
      var k = e.key;
      if (k === 'ArrowLeft' || k === 'a' || k === 'A') self.keys.left = true;
      else if (k === 'ArrowRight' || k === 'd' || k === 'D') self.keys.right = true;
      else if (k === 'ArrowUp' || k === 'w' || k === 'W') self.keys.up = true;
      else if (k === 'ArrowDown' || k === 's' || k === 'S') self.keys.down = true;
      else if (k === ' ') {
        if (e.shiftKey) self.pauseQueued = true;
        else self.launchQueued = true;
        e.preventDefault();
      }
      else if (k === 'p' || k === 'P' || k === 'Escape') self.pauseQueued = true;
      else if (k === 'm' || k === 'M') self.muteQueued = true;
      else if (k === 't' || k === 'T') self.toggleStickQueued = true;
      if (['ArrowLeft','ArrowRight','ArrowUp','ArrowDown',' '].indexOf(k) >= 0) e.preventDefault();
    });
    window.addEventListener('keyup', function (e) {
      var k = e.key;
      if (k === 'ArrowLeft' || k === 'a' || k === 'A') self.keys.left = false;
      else if (k === 'ArrowRight' || k === 'd' || k === 'D') self.keys.right = false;
      else if (k === 'ArrowUp' || k === 'w' || k === 'W') self.keys.up = false;
      else if (k === 'ArrowDown' || k === 's' || k === 'S') self.keys.down = false;
    });

    // --- mouse ---
    canvas.addEventListener('mousemove', function (e) {
      var p = pos(e);
      self.pointer.x = p.x; self.pointer.y = p.y;
      self.pointer.active = true;
      self.pointerMode = true;
    });
    canvas.addEventListener('mousedown', function (e) {
      var p = pos(e);
      self.pointer.x = p.x; self.pointer.y = p.y;
      self.pointer.active = true;
      self.launchQueued = true;
    });

    // --- touch: relative drag anywhere (default scheme), thumbstick optional ---
    this._lastTouch = null;
    canvas.addEventListener('touchstart', function (e) {
      e.preventDefault();
      self.pointerMode = true;
      var now = performance.now();
      for (var i = 0; i < e.changedTouches.length; i++) {
        var t = e.changedTouches[i];
        var p = pos({ clientX: t.clientX, clientY: t.clientY });
        // thumbstick zone: bottom-left quadrant circle
        var rect = canvas.getBoundingClientRect();
        if (self.thumbstick.enabled &&
            self._stickPointerId === null &&
            t.clientX - rect.left < rect.width * 0.4 &&
            t.clientY - rect.top > rect.height * 0.6) {
          self._stickPointerId = t.identifier;
          var st = self.thumbstick;
          st.active = true; st.visible = true;
          st.ox = p.x; st.oy = p.y; st.dx = 0; st.dy = 0;
          continue;
        }
        self._lastTouch = { x: p.x, y: p.y, id: t.identifier, t: now };
        // tap-to-launch (short touch, small move handled in touchend)
        self.launchQueued = true;
      }
    }, { passive: false });
    canvas.addEventListener('touchmove', function (e) {
      e.preventDefault();
      for (var i = 0; i < e.changedTouches.length; i++) {
        var t = e.changedTouches[i];
        var p = pos({ clientX: t.clientX, clientY: t.clientY });
        if (self._stickPointerId === t.identifier) {
          var st = self.thumbstick;
          var R = 70;
          st.dx = (p.x - st.ox) / R;
          st.dy = (p.y - st.oy) / R;
          var len = Math.hypot(st.dx, st.dy);
          if (len > 1) { st.dx /= len; st.dy /= len; }
          continue;
        }
        if (self._lastTouch && t.identifier === self._lastTouch.id) {
          // relative drag: move paddle target by delta so thumb never covers it
          self.pointer.x += (p.x - self._lastTouch.x);
          self.pointer.y += (p.y - self._lastTouch.y);
          self.pointer.active = true;
          self._lastTouch.x = p.x; self._lastTouch.y = p.y;
        }
      }
    }, { passive: false });
    function touchEnd(e) {
      e.preventDefault();
      for (var i = 0; i < e.changedTouches.length; i++) {
        var t = e.changedTouches[i];
        if (self._stickPointerId === t.identifier) {
          self._stickPointerId = null;
          self.thumbstick.active = false;
          self.thumbstick.dx = self.thumbstick.dy = 0;
        }
        if (self._lastTouch && t.identifier === self._lastTouch.id) {
          self._lastTouch = null;
        }
      }
    }
    canvas.addEventListener('touchend', touchEnd, { passive: false });
    canvas.addEventListener('touchcancel', touchEnd, { passive: false });

    // double-tap pause
    this._lastTapTime = 0;
    canvas.addEventListener('touchstart', function () {
      var now = performance.now();
      if (now - self._lastTapTime < 300) self.pauseQueued = true;
      self._lastTapTime = now;
    }, { passive: false });
  }

  // Consume thumbstick axis (normalized -1..1, 0 when inactive)
  Input.prototype.consumeStick = function () {
    var st = this.thumbstick;
    if (!st.active) return null;
    return { x: st.dx, y: st.dy };
  };

  Input.prototype.takeLaunch = function () {
    var v = this.launchQueued; this.launchQueued = false; return v;
  };
  Input.prototype.takePause = function () {
    var v = this.pauseQueued; this.pauseQueued = false; return v;
  };
  Input.prototype.takeMute = function () {
    var v = this.muteQueued; this.muteQueued = false; return v;
  };
  Input.prototype.takeStickToggle = function () {
    var v = this.toggleStickQueued; this.toggleStickQueued = false; return v;
  };

  BO.Input = Input;
})(typeof window !== 'undefined' ? window : globalThis);
