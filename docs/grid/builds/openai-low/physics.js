/* Dependency-free continuous collision helpers used by Orbit Breaker. */
(function (root, factory) {
  const api = factory();
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
  if (root) root.OrbitPhysics = api;
}(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  // Sweeps a circle along (dx, dy) against an axis-aligned rectangle. The
  // rectangle is expanded by the radius, so the returned normal is the true
  // contact face and fast balls cannot skip a thin brick.
  function sweepCircleAabb(sx, sy, dx, dy, radius, rect) {
    const minX = rect.x - radius, maxX = rect.x + rect.w + radius;
    const minY = rect.y - radius, maxY = rect.y + rect.h + radius;
    if (sx >= minX && sx <= maxX && sy >= minY && sy <= maxY) {
      const left = sx - minX, right = maxX - sx, top = sy - minY, bottom = maxY - sy;
      const least = Math.min(left, right, top, bottom);
      if (least === left) return { t: 0, nx: -1, ny: 0 };
      if (least === right) return { t: 0, nx: 1, ny: 0 };
      if (least === top) return { t: 0, nx: 0, ny: -1 };
      return { t: 0, nx: 0, ny: 1 };
    }
    let enter = -Infinity, exit = Infinity, nx = 0, ny = 0;
    const slab = (start, delta, low, high, axis) => {
      if (Math.abs(delta) < 1e-9) return start >= low && start <= high;
      let near = (low - start) / delta, far = (high - start) / delta;
      let nearNormal = axis === 'x' ? -1 : 0;
      if (near > far) {
        const temp = near; near = far; far = temp;
        nearNormal = axis === 'x' ? 1 : 0;
      }
      if (near > enter) {
        enter = near;
        if (axis === 'x') { nx = nearNormal; ny = 0; }
        else { nx = 0; ny = nearNormal === 0 ? (delta > 0 ? -1 : 1) : nearNormal; }
      }
      exit = Math.min(exit, far);
      return enter <= exit;
    };
    if (!slab(sx, dx, minX, maxX, 'x') || !slab(sy, dy, minY, maxY, 'y')) return null;
    if (exit < 0 || enter > 1) return null;
    const t = Math.max(0, enter);
    return { t, nx, ny };
  }

  function reflect(vx, vy, nx, ny) {
    const dot = vx * nx + vy * ny;
    return { vx: vx - 2 * dot * nx, vy: vy - 2 * dot * ny };
  }

  return { sweepCircleAabb, reflect };
}));
