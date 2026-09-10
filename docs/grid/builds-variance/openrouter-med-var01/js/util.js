// util.js — math helpers, tiny seeded RNG, object pool
'use strict';

const Util = (() => {
  const clamp = (v, a, b) => v < a ? a : (v > b ? b : v);
  const lerp = (a, b, t) => a + (b - a) * t;

  function mulberry32(seed) {
    let a = seed >>> 0;
    return function () {
      a |= 0; a = (a + 0x6D2B79F5) | 0;
      let t = Math.imul(a ^ (a >>> 15), 1 | a);
      t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
      return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
  }

  class Pool {
    constructor(factory, cap) {
      this.factory = factory; this.cap = cap;
      this.items = [];
      for (let i = 0; i < cap; i++) { const o = factory(); o.active = false; this.items.push(o); }
    }
    get() {
      for (let i = 0; i < this.items.length; i++) {
        if (!this.items[i].active) { this.items[i].active = true; return this.items[i]; }
      }
      return null; // pool exhausted; caller may recycle oldest
    }
    each(fn) { for (let i = 0; i < this.items.length; i++) if (this.items[i].active) fn(this.items[i]); }
    count() { let n = 0; this.each(() => n++); return n; }
    clear() { for (const o of this.items) o.active = false; }
  }

  // deterministic-ish ease helpers
  const easeOut = t => 1 - (1 - t) * (1 - t);
  const easeIn = t => t * t;

  return { clamp, lerp, mulberry32, Pool, easeOut, easeIn };
})();