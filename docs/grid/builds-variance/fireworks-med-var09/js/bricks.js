/* bricks.js — brick type definitions + pure gate/predicate logic.
 * NO multi-hit HP bricks: every brick here is a one-hit mechanic puzzle.
 * UMD-ish for node tests. */
(function (root) {
  'use strict';
  var P = root.BO || (typeof require !== 'undefined' ? require('./physics.js') : null);
  if (!P) throw new Error('physics.js must load first');

  /* Brick type table. All are 1-hit; difficulty comes from the gate.
   *  standard : plain, 1 hit.
   *  phase    : only breakable while its phase gate is OPEN (glowing).
   *  speed    : needs impact speed >= threshold (chevrons show tier).
   *  angle    : needs impact direction within an angle window (steep hits).
   *  mover    : drifts side to side; timing the approach matters.
   *  shielded : shield on the TOP face only — immune from above (no
   *             overdrive farming), must be flanked from the side/below. */
  var TYPES = {
    standard: {
      id: 'standard', base: 50, difficult: false,
      color: '#3fe0ff', glow: 'rgba(63,224,255,0.55)'
    },
    phase: {
      id: 'phase', base: 90, difficult: true,
      color: '#c46bff', glow: 'rgba(196,107,255,0.6)',
      period: 2.6, openFrac: 0.5
    },
    speed: {
      id: 'speed', base: 110, difficult: true,
      color: '#ffb340', glow: 'rgba(255,179,64,0.6)',
      threshold: 560            // vs base ball speed ~420, boosted ~600+
    },
    angle: {
      id: 'angle', base: 100, difficult: true,
      color: '#4dffa6', glow: 'rgba(77,255,166,0.6)',
      centerDeg: 0, halfWindow: 26   // steep hits only
    },
    mover: {
      id: 'mover', base: 80, difficult: true,
      color: '#ff5d8f', glow: 'rgba(255,93,143,0.6)',
      amp: 78, period: 3.8
    },
    shielded: {
      id: 'shielded', base: 130, difficult: true,
      color: '#ffe14d', glow: 'rgba(255,225,77,0.6)'
    }
  };

  /* Row palette for STANDARD bricks: curated neon ramp (cyan→teal→blue→
   * violet→magenta→coral) indexed by grid row, so level 1 reads as a
   * cohesive rainbow wall instead of one flat color. */
  var STANDARD_PALETTE = [
    { color: '#3fe0ff', glow: 'rgba(63,224,255,0.60)' },
    { color: '#2ef2c8', glow: 'rgba(46,242,200,0.60)' },
    { color: '#4d8dff', glow: 'rgba(77,141,255,0.60)' },
    { color: '#8f6bff', glow: 'rgba(143,107,255,0.60)' },
    { color: '#d24dff', glow: 'rgba(210,77,255,0.60)' },
    { color: '#ff6a8a', glow: 'rgba(255,106,138,0.60)' }
  ];

  /* Phase gate timing: open during the first `openFrac` of each cycle. */
  function phaseOpenAt(brick, now) {
    var t = TYPES.phase;
    var cyc = ((now / t.period) + (brick.seed || 0)) % 1;
    return cyc < t.openFrac;
  }

  /* Mover horizontal offset (pure function of time). */
  function moverOffsetAt(brick, now) {
    var t = TYPES.mover;
    return Math.sin((now / t.period) * Math.PI * 2 + (brick.seed || 0) * Math.PI * 2) * t.amp;
  }

  /* Speed tier → chevron count shown on speed-gate bricks. */
  function chevronsFor(threshold) {
    if (threshold < 480) return 1;
    if (threshold < 620) return 2;
    return 3;
  }

  /* Core predicate: can THIS hit break THIS brick?
   *   brick   : {type, seed, x, y, w, h}
   *   ball    : {vx, vy, x, y, type}   (velocity AT impact)
   *   now     : seconds
   *   face    : 'top' | 'bottom' | 'side'  (face of the BRICK that was hit)
   *   speed   : impact speed (may exceed |v| after boosts; passed in)
   * Returns { ok, reason } — reason used for feedback (clank, fizzle...). */
  function hitTest(brick, ball, now, face, speed) {
    var T = TYPES[brick.type] || TYPES.standard;
    switch (brick.type) {
      case 'phase':
        if (!phaseOpenAt(brick, now)) {
          return { ok: false, reason: 'phase-closed' };
        }
        return { ok: true, reason: 'ok' };
      case 'speed':
        if (ball.type === 'heavy') return { ok: true, reason: 'ok' }; // iron smashes gates
        if (speed < T.threshold) return { ok: false, reason: 'too-slow' };
        return { ok: true, reason: 'ok' };
      case 'angle':
        if (!P.angleGateOk(ball.vx, ball.vy, T.centerDeg, T.halfWindow)) {
          return { ok: false, reason: 'bad-angle' };
        }
        return { ok: true, reason: 'ok' };
      case 'shielded':
        if (face === 'top') return { ok: false, reason: 'shielded' };
        return { ok: true, reason: 'ok' };
      case 'mover':
      case 'standard':
      default:
        return { ok: true, reason: 'ok' };
    }
  }

  /* Power-up drop roll (pure given rng): returns drop id or null.
   * Weighted table; only some bricks drop, callers gate by chance first. */
  var DROPS = [
    { id: 'fire',   w: 12 },
    { id: 'heavy',  w: 10 },
    { id: 'multi',  w: 10 },
    { id: 'ghost',  w: 8 },
    { id: 'rubber', w: 10 },
    { id: 'wide',   w: 14 },
    { id: 'slow',   w: 8 },
    { id: 'magnet', w: 10 }
  ];
  var DROP_TOTAL = DROPS.reduce(function (s, d) { return s + d.w; }, 0);
  function rollDrop(rng) {
    var r = (rng || Math.random)() * DROP_TOTAL;
    for (var i = 0; i < DROPS.length; i++) {
      r -= DROPS[i].w;
      if (r <= 0) return DROPS[i].id;
    }
    return DROPS[DROPS.length - 1].id;
  }

  var API = {
    TYPES: TYPES,
    STANDARD_PALETTE: STANDARD_PALETTE,
    phaseOpenAt: phaseOpenAt,
    moverOffsetAt: moverOffsetAt,
    chevronsFor: chevronsFor,
    hitTest: hitTest,
    rollDrop: rollDrop,
    DROPS: DROPS
  };

  var BO = root.BO = root.BO || {};
  for (var k in API) BO[k] = API[k];
  if (typeof module !== 'undefined' && module.exports) module.exports = API;
})(typeof window !== 'undefined' ? window : globalThis);
