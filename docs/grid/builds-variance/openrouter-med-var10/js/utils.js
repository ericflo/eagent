/* BREAKTHROUGH — js/utils.js
   Tiny helpers shared by everything. Attached to window.U. */
window.U = (function () {
  const TAU = Math.PI * 2;
  const clamp = (v, a, b) => v < a ? a : v > b ? b : v;
  const lerp = (a, b, t) => a + (b - a) * t;
  const rand = (a = 1, b) => b === undefined ? Math.random() * a : a + Math.random() * (b - a);
  const randInt = (a, b) => Math.floor(rand(a, b + 1));
  const pick = arr => arr[(Math.random() * arr.length) | 0];
  const dist = (x1, y1, x2, y2) => Math.hypot(x2 - x1, y2 - y1);

  // frame-rate independent exponential smoothing: approaches 1 as t->d
  const damp = (a, b, lambda, dt) => lerp(a, b, 1 - Math.exp(-lambda * dt));

  // easings
  const easeOutCubic = t => 1 - Math.pow(1 - t, 3);
  const easeOutBack = t => 1 + 2.7 * Math.pow(t - 1, 3) + 1.7 * Math.pow(t - 1, 2);
  const easeInCubic = t => t * t * t;
  const easeOutElastic = t => t === 0 ? 0 : t === 1 ? 1 :
    Math.pow(2, -10 * t) * Math.sin((t * 10 - 0.75) * (TAU / 3)) + 1;

  // reflect ball velocity off an axis-aligned surface given normal, preserving speed
  function reflect(vx, vy, nx, ny) {
    const d = vx * nx + vy * ny;
    return [vx - 2 * d * nx, vy - 2 * d * ny];
  }

  // rounded rect path helper
  function rr(ctx, x, y, w, h, r) {
    r = Math.min(r, w / 2, h / 2);
    ctx.beginPath();
    ctx.moveTo(x + r, y);
    ctx.arcTo(x + w, y, x + w, y + h, r);
    ctx.arcTo(x + w, y + h, x, y + h, r);
    ctx.arcTo(x, y + h, x, y, r);
    ctx.arcTo(x, y, x + w, y, r);
    ctx.closePath();
  }

  // hsl string shorthand
  const hsl = (h, s, l, a = 1) => `hsla(${h},${s}%,${l}%,${a})`;

  return { TAU, clamp, lerp, rand, randInt, pick, dist, damp, easeOutCubic, easeOutBack, easeInCubic, easeOutElastic, reflect, rr, hsl };
})();