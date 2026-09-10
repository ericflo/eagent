// Breakthrough — util.js
window.BT = window.BT || {};
BT.util = (function () {
  function clamp(v, a, b) { return v < a ? a : (v > b ? b : v); }
  function lerp(a, b, t) { return a + (b - a) * t; }
  function rand(a, b) { return a + Math.random() * (b - a); }
  function randInt(a, b) { return Math.floor(rand(a, b + 1)); }
  function pick(arr) { return arr[Math.floor(Math.random() * arr.length)]; }
  function dist2(x1, y1, x2, y2) { var dx = x2 - x1, dy = y2 - y1; return dx * dx + dy * dy; }
  function easeOutCubic(t) { return 1 - Math.pow(1 - t, 3); }
  function easeOutBack(t) { var c = 1.70158; return 1 + (c + 1) * Math.pow(t - 1, 3) + c * Math.pow(t - 1, 2); }
  function easeInQuad(t) { return t * t; }

  var store = {
    get: function (k, def) {
      try {
        var v = localStorage.getItem('bt_' + k);
        return v === null ? def : JSON.parse(v);
      } catch (e) { return def; }
    },
    set: function (k, v) {
      try { localStorage.setItem('bt_' + k, JSON.stringify(v)); } catch (e) {}
    }
  };

  function fmt(n) {
    return String(Math.floor(n)).replace(/\B(?=(\d{3})+(?!\d))/g, ',');
  }

  // Mix two hex colors
  function mix(c1, c2, t) {
    function h(c) { return [parseInt(c.substr(1, 2), 16), parseInt(c.substr(3, 2), 16), parseInt(c.substr(5, 2), 16)]; }
    var a = h(c1), b = h(c2);
    return 'rgb(' + Math.round(lerp(a[0], b[0], t)) + ',' + Math.round(lerp(a[1], b[1], t)) + ',' + Math.round(lerp(a[2], b[2], t)) + ')';
  }

  return { clamp: clamp, lerp: lerp, rand: rand, randInt: randInt, pick: pick, dist2: dist2,
           easeOutCubic: easeOutCubic, easeOutBack: easeOutBack, easeInQuad: easeInQuad,
           store: store, fmt: fmt, mix: mix };
})();
