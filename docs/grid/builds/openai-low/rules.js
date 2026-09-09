/* Small, dependency-free rule helpers kept separate so game semantics are testable. */
(function (root, factory) {
  const api = factory();
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
  if (root) root.OrbitRules = api;
}(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  const POWER_DURATIONS = { split: 10, plasma: 12, magnet: 10, overdrive: 9 };
  // Every launched ball starts below this speed. Reaching the threshold takes
  // an Updraft strike (or another deliberate acceleration), so speed bricks
  // are a real movement challenge rather than differently coloured normals.
  const SPEED_THRESHOLD = 600;

  function powerActive(powers, mode, now) {
    return (powers && Number(powers[mode]) || 0) > now;
  }

  function canBreakSpecial(type, vx, vy, open, mode) {
    if (mode === 'plasma') return true;
    if (type === 'phase') return !!open;
    if (type === 'angle') return Math.abs(vy) > Math.abs(vx) * 0.72;
    if (type === 'speed') return Math.hypot(vx, vy) > SPEED_THRESHOLD;
    return true;
  }

  // A pickup refreshes an existing power but never shortens it. Different
  // powers coexist; activeMode supplies a deterministic visual priority.
  function extendPower(powers, mode, now, duration) {
    const next = Object.assign({}, powers || {});
    next[mode] = Math.max(Number(next[mode]) || 0, now + duration);
    return next;
  }

  function activeMode(powers, now) {
    const priority = ['plasma', 'magnet', 'overdrive', 'split'];
    for (const mode of priority) if (powerActive(powers, mode, now)) return mode;
    return 'core';
  }

  function breakthroughReady(ball, brickTop) {
    return !!ball.wallContact && ball.vy < 0 && ball.y - ball.r <= brickTop - 4;
  }

  function outcome(bricksCleared, ballsRemaining, lives) {
    if (bricksCleared) return 'victory';
    if (ballsRemaining) return 'playing';
    return lives <= 1 ? 'gameover' : 'life-lost';
  }

  return { POWER_DURATIONS, SPEED_THRESHOLD, powerActive, canBreakSpecial, extendPower, activeMode, breakthroughReady, outcome };
}));
