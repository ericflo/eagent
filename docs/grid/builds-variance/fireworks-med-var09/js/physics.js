/* physics.js — pure math for Neon Breakout.
 * UMD-ish: loaded via <script> in the browser (attaches to BO namespace)
 * and require()-able in node for unit tests. No DOM, no state. */
(function (root) {
  'use strict';
  var API = {};

  API.clamp = function (v, a, b) { return v < a ? a : (v > b ? b : v); };
  API.lerp = function (a, b, t) { return a + (b - a) * t; };

  /* ---------- Swept circle vs AABB ----------
   * Circle of radius r moves from (x0,y0) to (x1,y1) this substep.
   * Equivalent to a point (the circle center) sweeping against the AABB
   * expanded by r. Returns the earliest hit:
   *   { t, nx, ny }   t in [0,1]; (nx,ny) = outward face normal of entry
   * or null if no hit. Handles start-inside (t=0, normal 0,0). */
  API.sweptCircleRect = function (x0, y0, x1, y1, r, rx, ry, rw, rh) {
    var ex = rx - r, ey = ry - r, ew = rw + 2 * r, eh = rh + 2 * r;
    var dx = x1 - x0, dy = y1 - y0;
    var tEnter = 0, tExit = 1, nx = 0, ny = 0;

    if (dx !== 0) {
      var invx = 1 / dx;
      var tx1 = (ex - x0) * invx, tx2 = (ex + ew - x0) * invx;
      var sx = -1;
      if (tx1 > tx2) { var tt = tx1; tx1 = tx2; tx2 = tt; sx = 1; }
      if (tx1 > tEnter) { tEnter = tx1; nx = sx; ny = 0; }
      if (tx2 < tExit) tExit = tx2;
    } else if (x0 < ex || x0 > ex + ew) return null;

    if (dy !== 0) {
      var invy = 1 / dy;
      var ty1 = (ey - y0) * invy, ty2 = (ey + eh - y0) * invy;
      var sy = -1;
      if (ty1 > ty2) { var t2 = ty1; ty1 = ty2; ty2 = t2; sy = 1; }
      if (ty1 > tEnter) { tEnter = ty1; ny = sy; nx = 0; }
      if (ty2 < tExit) tExit = ty2;
    } else if (y0 < ey || y0 > ey + eh) return null;

    if (tEnter > tExit || tExit < 0 || tEnter > 1) return null;
    if (tEnter === 0 && nx === 0 && ny === 0) {
      // started overlapping: only a "hit" if the center is inside the
      // expanded box right now (slab test passed with zero travel handled above)
      if (x0 >= ex && x0 <= ex + ew && y0 >= ey && y0 <= ey + eh) {
        return { t: 0, nx: 0, ny: 0, inside: true };
      }
      return null;
    }
    return { t: tEnter, nx: nx, ny: ny, inside: false };
  };

  /* Reflect velocity (vx,vy) about a unit normal (nx,ny). */
  API.reflect = function (vx, vy, nx, ny) {
    var d = vx * nx + vy * ny;
    return { x: vx - 2 * d * nx, y: vy - 2 * d * ny };
  };

  /* ---------- Paddle bounce ----------
   * Classic breakout angle control + paddle motion transfer.
   *   rel        : -1..1 where the ball hit across the paddle face
   *   paddleVx/Vy: paddle velocity at impact (world units/s)
   * Returns {vx, vy} — new ball velocity, never downward. */
  API.paddleBounce = function (vx, vy, rel, paddleVx, paddleVy, cfg) {
    var speed = Math.hypot(vx, vy) || cfg.baseSpeed;
    // Vertical paddle motion: moving up boosts, moving down dampens.
    if (paddleVy < 0) speed *= cfg.upBoost;
    else if (paddleVy > 0) speed *= cfg.downDamp;
    speed = API.clamp(speed * cfg.launchSpeedK, cfg.minSpeed, cfg.maxSpeed);

    var maxA = cfg.maxBounceAngle * Math.PI / 180;
    // Hit position sets angle; horizontal paddle velocity adds spin/angle.
    var a = rel * maxA + paddleVx * cfg.spinFactor;
    a = API.clamp(a, -maxA * 1.08, maxA * 1.08);
    // |a| <= maxA < 90° guarantees a minimum upward component off the paddle.
    return { x: Math.sin(a) * speed, y: -Math.abs(Math.cos(a)) * speed };
  };

  /* Launch velocity from paddle: near-vertical with slight spread. */
  API.launchVelocity = function (speed, spread) {
    var a = (Math.random() * 2 - 1) * (spread || 0.18);
    return { x: Math.sin(a) * speed, y: -Math.cos(a) * speed };
  };

  /* Nudge a velocity away from degenerate (near-vertical / near-horizontal)
   * trajectories. Ensures |vx| >= speed*sin(minHorizDeg) and similarly for vy.
   * Mutates and returns the passed object {x,y}. */
  API.nudgeVelocity = function (v, minHorizDeg, minVertDeg) {
    var speed = Math.hypot(v.x, v.y);
    if (speed < 1e-6) return v;
    var minHx = speed * Math.sin((minHorizDeg || 8) * Math.PI / 180);
    var minVy = speed * Math.sin((minVertDeg || 6) * Math.PI / 180);
    if (Math.abs(v.x) < minHx) {
      var sgnx = v.x >= 0 ? 1 : -1;
      v.x = sgnx * minHx;
      v.y = (v.y >= 0 ? 1 : -1) * Math.sqrt(Math.max(0, speed * speed - v.x * v.x));
    }
    if (Math.abs(v.y) < minVy) {
      var sgny = v.y >= 0 ? 1 : -1;
      v.y = sgny * minVy;
      v.x = (v.x >= 0 ? 1 : -1) * Math.sqrt(Math.max(0, speed * speed - v.y * v.y));
    }
    return v;
  };

  /* Cap speed magnitude in place, returns speed. */
  API.capSpeed = function (v, maxSpeed) {
    var s = Math.hypot(v.x, v.y);
    if (s > maxSpeed && s > 0) { var k = maxSpeed / s; v.x *= k; v.y *= k; return maxSpeed; }
    return s;
  };

  /* ---------- Gates (pure predicates) ---------- */

  // Speed gate: impact speed must meet threshold.
  API.speedGateOk = function (vx, vy, threshold) {
    return Math.hypot(vx, vy) >= threshold;
  };

  // Angle gate: direction of travel measured from straight up (ball rising
  // into the brick field). ok if within halfWindowDeg of centerDeg.
  API.hitAngleDeg = function (vx, vy) {
    // 0° = moving straight up, ±90° horizontal, 180° straight down
    return Math.atan2(vx, -vy) * 180 / Math.PI;
  };
  API.angleDiffDeg = function (a, b) {
    var d = (a - b) % 360;
    if (d > 180) d -= 360;
    if (d < -180) d += 360;
    return d;
  };
  API.angleGateOk = function (vx, vy, centerDeg, halfWindowDeg) {
    return Math.abs(API.angleDiffDeg(API.hitAngleDeg(vx, vy), centerDeg)) <= halfWindowDeg;
  };

  /* ---------- Overdrive multiplier ramp ----------
   * Exponential ramp while a ball is above the brick field, hard cap.
   *   cfg.growth  — multiplier per second (e.g. 1.5 → x1.5 every second)
   *   cfg.cap     — maximum multiplier (e.g. 16)
   * Returns { mult, meter } where meter is 0..1 ramp progress. */
  API.overdrive = function (timeAbove, cfg) {
    var t = Math.max(0, timeAbove);
    var mult = Math.min(cfg.cap, Math.pow(cfg.growth, t));
    var frac = Math.log(Math.max(mult, 1)) / Math.log(cfg.cap);
    return { mult: mult, meter: API.clamp(frac, 0, 1) };
  };

  // Score for one brick = base * combo * multiplier, with combo growth.
  API.brickScore = function (baseScore, combo, mult) {
    return Math.round(baseScore * (1 + Math.max(0, combo - 1) * 0.25) * mult);
  };

  // Which face did the ball hit, from the swept normal?
  //   'top' | 'bottom' | 'left' | 'right' | 'side'
  API.faceFromNormal = function (nx, ny) {
    if (ny < 0) return 'top';
    if (ny > 0) return 'bottom';
    if (nx !== 0) return 'side';
    return 'side';
  };

  var BO = root.BO = root.BO || {};
  for (var k in API) BO[k] = API[k];
  if (typeof module !== 'undefined' && module.exports) module.exports = API;
})(typeof window !== 'undefined' ? window : globalThis);
