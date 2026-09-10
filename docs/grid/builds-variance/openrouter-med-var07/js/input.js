// Breakthrough — input.js: unified pointer (mouse+touch), keyboard
window.BT = window.BT || {};
BT.input = (function () {
  var U = BT.util;
  var state = {
    x: 0, y: 0,          // pointer position in virtual coords
    dx: 0, dy: 0,        // per-frame drag delta (virtual px)
    pointerType: 'mouse',
    isTouch: false,      // last active pointer type is touch
    launchPressed: false,
    firePressed: false,
    down: false,
    tapPending: false,
    keys: {},
    kbUp: false, kbDown: false, kbLeft: false, kbRight: false
  };
  var listeners = [];
  var canvas = null;
  var toVirtual = function (x, y) { return { x: x, y: y }; }; // set by game
  var downX = 0, downY = 0, downT = 0;
  var moved = 0;
  var dragDX = 0, dragDY = 0;

  function emit(evt) {
    for (var i = 0; i < listeners.length; i++) listeners[i](evt);
  }

  function pointerPos(e) {
    return toVirtual(e.clientX, e.clientY);
  }

  function onDown(e) {
    BT.audio.unlock();
    var p = pointerPos(e);
    state.x = p.x; state.y = p.y;
    state.down = true;
    state.pointerType = e.pointerType || 'mouse';
    state.isTouch = state.pointerType === 'touch';
    downX = p.x; downY = p.y; downT = performance.now(); moved = 0;
    dragDX = 0; dragDY = 0;
    emit({ type: 'down', x: p.x, y: p.y, pointerType: state.pointerType });
    if (e.pointerType === 'touch') e.preventDefault();
  }

  function onMove(e) {
    var p = pointerPos(e);
    if (state.down) {
      var dx = p.x - state.x, dy = p.y - state.y;
      moved += Math.abs(dx) + Math.abs(dy);
      if (state.isTouch) {
        dragDX += dx * 1.25;
        dragDY += dy * 1.25;
      } else {
        dragDX += dx;
        dragDY += dy;
      }
    }
    state.x = p.x; state.y = p.y;
    emit({ type: 'move', x: p.x, y: p.y });
    if (e.pointerType === 'touch' && state.down) e.preventDefault();
  }

  function onUp(e) {
    var p = pointerPos(e);
    state.down = false;
    var dt = performance.now() - downT;
    var quick = dt < 180 && moved < 12;
    emit({ type: 'up', x: p.x, y: p.y, tap: quick, dt: dt });
    if (quick) { state.tapPending = true; }
    if (e.pointerType === 'touch') e.preventDefault();
  }

  function onKeyDown(e) {
    BT.audio.unlock();
    var k = e.key;
    if (k === 'ArrowLeft' || k === 'a' || k === 'A') state.kbLeft = true;
    if (k === 'ArrowRight' || k === 'd' || k === 'D') state.kbRight = true;
    if (k === 'ArrowUp' || k === 'w' || k === 'W') state.kbUp = true;
    if (k === 'ArrowDown' || k === 's' || k === 'S') state.kbDown = true;
    if (k === ' ' || k === 'Spacebar') { state.firePressed = true; state.launchPressed = true; e.preventDefault(); }
    if (k === 'p' || k === 'P') emit({ type: 'key', key: 'p' });
    if (k === 'm' || k === 'M') emit({ type: 'key', key: 'm' });
    if (k === 'Enter') emit({ type: 'key', key: 'enter' });
    emit({ type: 'keydown', key: k });
  }

  function onKeyUp(e) {
    var k = e.key;
    if (k === 'ArrowLeft' || k === 'a' || k === 'A') state.kbLeft = false;
    if (k === 'ArrowRight' || k === 'd' || k === 'D') state.kbRight = false;
    if (k === 'ArrowUp' || k === 'w' || k === 'W') state.kbUp = false;
    if (k === 'ArrowDown' || k === 's' || k === 'S') state.kbDown = false;
    if (k === ' ') state.launchPressed = false;
  }

  function attach(cv, toVirt) {
    canvas = cv;
    toVirtual = toVirt;
    window.addEventListener('pointerdown', onDown, { passive: false });
    window.addEventListener('pointermove', onMove, { passive: false });
    window.addEventListener('pointerup', onUp, { passive: false });
    window.addEventListener('pointercancel', onUp, { passive: false });
    window.addEventListener('keydown', onKeyDown);
    window.addEventListener('keyup', onKeyUp);
    window.addEventListener('contextmenu', function (e) { e.preventDefault(); });
  }

  // Called once per frame by game; consumes drag delta
  function consumeDelta() {
    var d = { dx: dragDX, dy: dragDY };
    dragDX = 0; dragDY = 0;
    return d;
  }

  function consumeTap() {
    var t = state.tapPending;
    state.tapPending = false;
    return t;
  }

  return { state: state, attach: attach, consumeDelta: consumeDelta, consumeTap: consumeTap,
           on: function (fn) { listeners.push(fn); } };
})();
