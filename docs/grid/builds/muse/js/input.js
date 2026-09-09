/* BreakoutInput — unified mouse + keyboard + touch with virtual thumbstick visual.
   Global: window.BreakoutInput (singleton state object + attach()).
   Canvas-relative coordinates (CSS px). */
(function () {
  'use strict';
  var Input = {
    mouseX: 0, mouseY: 0, mouseActive: false,
    keys: {},
    touchActive: false, touchX: 0, touchY: 0,
    stickOX: 0, stickOY: 0, stickDX: 0, stickDY: 0, stickOn: false,
    launched: false, // edge-trigger queue: set true on tap/click/space; game consumes
    canvas: null
  };

  function pos(e, canvas) {
    var r = canvas.getBoundingClientRect();
    var t = (e.touches && e.touches[0]) || (e.changedTouches && e.changedTouches[0]) || e;
    return { x: (t.clientX - r.left), y: (t.clientY - r.top) };
  }

  function showStick(x, y, dx, dy) {
    var base = document.getElementById('stick-base');
    var knob = document.getElementById('stick-knob');
    if (!base) return;
    base.classList.remove('hidden');
    base.style.left = x + 'px'; base.style.top = y + 'px';
    var len = Math.hypot(dx, dy), max = 40;
    if (len > max) { dx *= max / len; dy *= max / len; }
    knob.style.transform = 'translate(calc(-50% + ' + dx + 'px), calc(-50% + ' + dy + 'px))';
  }
  function hideStick() {
    var base = document.getElementById('stick-base');
    if (base) base.classList.add('hidden');
  }

  Input.attach = function (canvas, opts) {
    Input.canvas = canvas;
    opts = opts || {};
    var onLaunch = opts.onLaunch || function () {};
    var onPause = opts.onPause || function () {};
    var onMute = opts.onMute || function () {};
    var onFirstGesture = opts.onFirstGesture || function () {};

    var wrap = document.getElementById('game-wrap');

    // --- Mouse: move paddle target; click = launch ---
    window.addEventListener('mousemove', function (e) {
      var r = canvas.getBoundingClientRect();
      Input.mouseX = e.clientX - r.left;
      Input.mouseY = e.clientY - r.top;
      Input.mouseActive = true;
    });
    canvas.addEventListener('mousedown', function (e) {
      onFirstGesture();
      var r = canvas.getBoundingClientRect();
      Input.mouseX = e.clientX - r.left;
      Input.mouseY = e.clientY - r.top;
      Input.mouseActive = true;
      Input.launched = true; onLaunch();
    });

    // --- Keyboard ---
    window.addEventListener('keydown', function (e) {
      var k = (e.key === ' ' ? ' ' : e.key.toLowerCase());
      if (['arrowup', 'arrowdown', 'arrowleft', 'arrowright', ' '].indexOf(k) >= 0) e.preventDefault();
      if (e.repeat) { Input.keys[k] = true; return; }
      Input.keys[k] = true;
      if (k === 'arrowup' || k === 'arrowdown' || k === 'arrowleft' || k === 'arrowright' ||
          k === 'w' || k === 'a' || k === 's' || k === 'd') Input.mouseActive = false; // keyboard takes over
      onFirstGesture();
      if (k === ' ' || k === 'spacebar') { Input.launched = true; onLaunch(); }
      if (k === 'p' || k === 'escape') onPause();
      if (k === 'm') onMute();
    });
    window.addEventListener('keyup', function (e) { Input.keys[e.key.toLowerCase()] = false; });
    window.addEventListener('blur', function () { Input.keys = {}; });

    // --- Touch: drag moves paddle (absolute X + Y offset so finger doesn't cover paddle) ---
    var touchId = null;
    function uiTarget(e) {
      // Let menu buttons/overlays handle their own taps (click synthesis intact)
      if (e.target && e.target.closest && e.target.closest('button, .overlay')) return true;
      return false;
    }
    function tStart(e) {
      if (uiTarget(e)) return;
      e.preventDefault(); onFirstGesture();
      var t = e.changedTouches[0];
      var r = canvas.getBoundingClientRect();
      var x = t.clientX - r.left, y = t.clientY - r.top;
      if (touchId === null) {
        touchId = t.identifier;
        Input.touchActive = true; Input.touchX = x; Input.touchY = y;
        Input.stickOX = x; Input.stickOY = y;
        Input.stickDX = 0; Input.stickDY = 0; Input.stickOn = true;
        showStick(x, y, 0, 0);
      }
    }
    function tMove(e) {
      if (touchId === null || uiTarget(e)) return;
      e.preventDefault();
      for (var i = 0; i < e.changedTouches.length; i++) {
        var t = e.changedTouches[i];
        if (t.identifier === touchId) {
          var r = canvas.getBoundingClientRect();
          var x = t.clientX - r.left, y = t.clientY - r.top;
          Input.touchX = x; Input.touchY = y;
          Input.stickDX = x - Input.stickOX; Input.stickDY = y - Input.stickOY;
          showStick(Input.stickOX, Input.stickOY, Input.stickDX, Input.stickDY);
        }
      }
    }
    function tEnd(e) {
      if (touchId === null) return;
      if (uiTarget(e)) {
        // lifted over a menu/button: release stick, let click happen
        touchId = null; Input.touchActive = false; Input.stickOn = false;
        hideStick();
        return;
      }
      e.preventDefault();
      for (var i = 0; i < e.changedTouches.length; i++) {
        var t = e.changedTouches[i];
        if (t.identifier === touchId) {
          touchId = null; Input.touchActive = false; Input.stickOn = false;
          hideStick();
          // tap (short, small move) = launch
          Input.launched = true; onLaunch();
        }
      }
    }
    wrap.addEventListener('touchstart', tStart, { passive: false });
    wrap.addEventListener('touchmove', tMove, { passive: false });
    wrap.addEventListener('touchend', tEnd, { passive: false });
    wrap.addEventListener('touchcancel', function (e) {
      touchId = null; Input.touchActive = false; Input.stickOn = false; hideStick();
    });
    document.addEventListener('gesturestart', function (e) { e.preventDefault(); });
    document.addEventListener('dblclick', function (e) { e.preventDefault(); }, { passive: false });
  };

  // Consume the launch edge trigger
  Input.consumeLaunch = function () {
    var l = Input.launched;
    Input.launched = false;
    return l;
  };

  window.BreakoutInput = Input;
})();
