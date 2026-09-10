/* ============================================================
   SKYBREAK — js/00-util.js → window.U
   Math helpers, constants, seeded RNG, localStorage guards.
   Dependency-free (no other namespace references).
   ============================================================ */
(function () {
  'use strict';
  var U = {};
  window.U = U;

  /* ---- constants ---- */
  U.VW = 900;               // virtual width (canonical coord space)
  U.VH = 1340;              // virtual height
  U.FIXED_DT = 1 / 120;     // fixed timestep (s)
  U.TAU = Math.PI * 2;

  /* ---- math ---- */
  U.clamp = function (v, a, b) { return v < a ? a : (v > b ? b : v); };
  U.lerp = function (a, b, t) { return a + (b - a) * t; };
  // c1,c2 are [r,g,b] arrays; t in 0..1 → interpolated array
  U.lerpColor = function (c1, c2, t) {
    return [
      (c1[0] + (c2[0] - c1[0]) * t) | 0,
      (c1[1] + (c2[1] - c1[1]) * t) | 0,
      (c1[2] + (c2[2] - c1[2]) * t) | 0
    ];
  };
  U.rand = function (a, b) {
    if (a === undefined) return Math.random();
    if (b === undefined) return Math.random() * a;
    return a + Math.random() * (b - a);
  };
  U.randInt = function (a, b) { return Math.floor(U.rand(a, b + 1)); };
  U.pick = function (arr) { return arr[(Math.random() * arr.length) | 0]; };
  U.dist2 = function (x1, y1, x2, y2) {
    var dx = x2 - x1, dy = y2 - y1;
    return dx * dx + dy * dy;
  };
  U.len = function (x, y) { return Math.sqrt(x * x + y * y); };

  /* ---- seeded RNG: mulberry32 ---- */
  function Rng(seed) {
    // seed may be any number; coerce to uint32
    this.s = (seed | 0) >>> 0;
    if (this.s === 0) this.s = 0x9e3779b9;
  }
  Rng.prototype.rand = function () {
    // mulberry32
    this.s = (this.s + 0x6D2B79F5) >>> 0;
    var t = this.s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
  // inclusive integer in [a, b]
  Rng.prototype.int = function (a, b) {
    return a + Math.floor(this.rand() * (b - a + 1));
  };
  Rng.prototype.pick = function (arr) { return arr[Math.floor(this.rand() * arr.length)]; };
  Rng.prototype.chance = function (p) { return this.rand() < p; };
  // float in [a, b]
  Rng.prototype.range = function (a, b) { return a + this.rand() * (b - a); };
  U.Rng = Rng;

  /* ---- localStorage guards (keys: skybreak.best, skybreak.muted) ---- */
  function storeGet(key) {
    try { return window.localStorage.getItem(key); } catch (e) { return null; }
  }
  function storeSet(key, val) {
    try { window.localStorage.setItem(key, val); } catch (e) { /* private mode / quota */ }
  }
  U.loadBest = function () {
    var v = parseInt(storeGet('skybreak.best'), 10);
    return isNaN(v) ? 0 : v;
  };
  U.saveBest = function (n) {
    var cur = U.loadBest();
    if (n > cur) storeSet('skybreak.best', String(n | 0));
  };
  U.loadMuted = function () { return storeGet('skybreak.muted') === '1'; };
  U.saveMuted = function (b) { storeSet('skybreak.muted', b ? '1' : '0'); };

  /* ---- color helpers ---- */
  // h,s,v in [0,1] → css rgb() string
  U.hsv = function (h, s, v) {
    h = ((h % 1) + 1) % 1;
    var i = Math.floor(h * 6), f = h * 6 - i;
    var p = v * (1 - s), q = v * (1 - f * s), t = v * (1 - (1 - f) * s);
    var r, g, b;
    switch (i % 6) {
      case 0: r = v; g = t; b = p; break;
      case 1: r = q; g = v; b = p; break;
      case 2: r = p; g = v; b = t; break;
      case 3: r = p; g = q; b = v; break;
      case 4: r = t; g = p; b = v; break;
      default: r = v; g = p; b = q;
    }
    return 'rgb(' + ((r * 255) | 0) + ',' + ((g * 255) | 0) + ',' + ((b * 255) | 0) + ')';
  };
})();