/* OVERDRIVE — js/input.js
 * window.Input: mouse + keyboard + touch (relative drag, thumb-friendly),
 * tap-to-launch/release, PAUSE and MUTE canvas hit zones (touch ≥48px),
 * pointer-vs-touch arbitration. Space / P / M keys.
 * Classic script. Deferred access: Game must exist at call time.
 */
(function () {
  'use strict';

  function clamp(v, a, b) { return v < a ? a : (v > b ? b : v); }

  window.Input = {
    // state read by Game
    targetX: 360, targetY: 700,   // logical-space target for paddle (if mouse/pointer)
    active: 'none',               // 'mouse' | 'touch' | 'keys'
    keys: {},
    dragging: false,
    launched: false,              // tap/click/space consume → launch pending
    pressed: false,               // raw button state

    hoverX: 0, hoverY: 0,
    _touchStart: null,
    _lastMouseX: 0, _lastMouseY: 0,
    _hasMouse: false,
    _pausedByButton: false,

    init: function (game) {
      this._game = game;
      var el = game.canvas;
      var self = this;

      // ---- mouse ---------------------------------------------------------
      el.addEventListener('mousemove', function (e) {
        if (self._recentTouch()) return;      // suppress synthetic mouse after touch
        if (self.active === 'touch' && !self._hasMouse) return;
        self._hasMouse = true;
        self.active = 'mouse';
        var p = self._toLogical(e.clientX, e.clientY);
        self.hoverX = p.x; self.hoverY = p.y;
        self._lastMouseX = p.x; self._lastMouseY = p.y;
      });
      el.addEventListener('mousedown', function (e) {
        if (self._recentTouch()) return;
        if (self.active === 'touch') return;
        self.active = 'mouse';
        var p = self._toLogical(e.clientX, e.clientY);
        self._lastMouseX = p.x; self._lastMouseY = p.y;
        self.pressed = true;
        self._handleDown(p.x, p.y, 'mouse');
      });
      window.addEventListener('mouseup', function (e) {
        if (self._recentTouch()) return;
        var p = self._toLogical(e.clientX, e.clientY);
        self.pressed = false;
        self._handleUp(p.x, p.y, 'mouse');
      });
      el.addEventListener('mouseleave', function () {
        self.pressed = false;
      });
      el.addEventListener('contextmenu', function (e) { e.preventDefault(); });

      // ---- keyboard ------------------------------------------------------
      window.addEventListener('keydown', function (e) {
        var k = e.key.toLowerCase();
        self.keys[k] = true;
        self.active = 'keys';
        if (k === ' ') {
          e.preventDefault();
          if (!e.repeat) self.launched = true;
        } else if (k === 'p') {
          if (!e.repeat) game.togglePause();
        } else if (k === 'm') {
          if (!e.repeat) game.toggleMute();
        }
        // arrows should not scroll the page
        if (['arrowleft', 'arrowright', 'arrowup', 'arrowdown', ' '].indexOf(k) >= 0) {
          e.preventDefault();
        }
      });
      window.addEventListener('keyup', function (e) { self.keys[e.key.toLowerCase()] = false; });

      // touch
      el.addEventListener('touchstart', function (e) {
        e.preventDefault();
        self._lastTouchTime = performance.now();
        self.active = 'touch';
        self._hasMouse = false;
        var t = e.changedTouches[0];
        var p = self._toLogical(t.clientX, t.clientY);
        self._touchStart = { x: t.clientX, y: t.clientY, lx: p.x, ly: p.y, time: performance.now() };
        // start the drag target from the paddle's current spot (no jump)
        if (game && game.paddle) {
          self.targetX = game.paddle.x;
          self.targetY = game.paddle.y;
        }
        self.dragging = true;
        self.pressed = true;
        // buttons handled via down/up in logical space
        self._handleDown(p.x, p.y, 'touch');
      }, { passive: false });
      el.addEventListener('touchmove', function (e) {
        e.preventDefault();
        if (!self.dragging) return;
        var t = e.changedTouches[0];
        var start = self._touchStart;
        if (!start) return;
        self._dragHappened = true;
        // relative drag: paddle follows finger delta * sensitivity — no jump
        var sx = game.canvas.clientWidth ? game.logicalW() / game.canvas.clientWidth : 1;
        var sy = game.canvas.clientHeight ? game.logicalH() / game.canvas.clientHeight : 1;
        var dx = (t.clientX - start.x) * sx;
        var dy = (t.clientY - start.y) * sy;
        self.targetX = clamp(self.targetX + dx * game.CFG.TOUCH_SENSITIVITY, 40, game.logicalW() - 40);
        self.targetY = clamp(self.targetY + dy * game.CFG.TOUCH_SENSITIVITY, game.logicalH() * 0.45, game.logicalH() * 0.92);
        start.x = t.clientX; start.y = t.clientY;
      }, { passive: false });
      el.addEventListener('touchend', function (e) {
        e.preventDefault();
        self.dragging = false;
        self.pressed = false;
        var t = e.changedTouches[0];
        var p = self._toLogical(t.clientX, t.clientY);
        self._handleUp(p.x, p.y, 'touch');
      }, { passive: false });
      el.addEventListener('touchcancel', function (e) {
        e.preventDefault();
        self.dragging = false;
        self.pressed = false;
      }, { passive: false });
    },

    // tap/click handling: buttons first, else launch/release
    _handleDown: function (lx, ly, kind) {
      var g = this._game;
      if (g.hitPause(lx, ly)) { this._downOnButton = true; this._uiAction = 'pause'; return; }
      if (g.hitMute(lx, ly)) { this._downOnButton = true; this._uiAction = 'mute'; return; }
      this._downOnButton = false;
      if (kind === 'mouse') {
        this.launched = true;          // click = launch/release
      } else if (kind === 'touch') {
        // A tap should launch; a drag should not. Remember down; launch
        // fires on touchend only if the finger barely moved.
        this._tapCandidate = { x: lx, y: ly, time: performance.now() };
        this._dragHappened = false;
      }
    },

    _handleUp: function (lx, ly, kind) {
      if (this._downOnButton) {
        if (this._uiAction === 'pause') this._game.togglePause();
        else if (this._uiAction === 'mute') this._game.toggleMute();
        this._downOnButton = false; this._uiAction = null;
        return;
      }
      if (kind === 'mouse') {
        this.launched = true;
      }
      if (kind === 'touch') {
        var c = this._tapCandidate;
        this._tapCandidate = null;
        if (c && !this._dragHappened) {
          var dx = lx - c.x, dy = ly - c.y;
          var dist = Math.sqrt(dx * dx + dy * dy);
          if (dist < 14 && performance.now() - c.time < 350) {
            this.launched = true;
          }
        }
        this._dragHappened = false;
      }
    },

    // called from touchmove when real dragging occurs
    markDrag: function () { this._dragHappened = true; },

    _toLogical: function (cx, cy) {
      var g = this._game;
      if (!g || !g.canvas) return { x: cx, y: cy };
      var r = g.canvas.getBoundingClientRect();
      return {
        x: (cx - r.left) * (g.logicalW() / r.width),
        y: (cy - r.top) * (g.logicalH() / r.height)
      };
    },

    // ignore synthetic mouse events for a beat after real touch input
    _recentTouch: function () {
      return this._lastTouchTime && performance.now() - this._lastTouchTime < 400;
    },

    // called by Game each frame to consume a launch request
    consumeLaunch: function () {
      var l = this.launched;
      this.launched = false;
      return l;
    },

    // which direction keys are held (as a vector)
    axis: function () {
      var k = this.keys;
      var x = (k['arrowright'] || k['d'] ? 1 : 0) - (k['arrowleft'] || k['a'] ? 1 : 0);
      var y = (k['arrowdown'] || k['s'] ? 1 : 0) - (k['arrowup'] || k['w'] ? 1 : 0);
      return { x: x, y: y };
    }
  };
})();
