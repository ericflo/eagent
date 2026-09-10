/* OVERDRIVE — js/config.js
 * window.CFG: all tunable constants. window.Events: tiny pub/sub.
 * Classic script; attaches to window. Loaded first.
 */
(function () {
  'use strict';

  window.CFG = {
    // --- arena / logical units -------------------------------------------------
    W: 720,                     // logical world width (fixed)
    H_MIN: 900,                 // logical height clamp
    H_MAX: 1500,
    COLS: 12,                   // bricks per row

    // --- ball ------------------------------------------------------------------
    BALL_R: 9,
    BALL_BASE_SPEED: 560,       // level 1 base speed (u/s)
    BALL_SPEED_PER_LEVEL: 0.04, // +4% per level
    BALL_SPEED_CAP: 1100,       // base speed cap
    BALL_HARD_CAP: 1350,        // absolute max speed (overdrive ramp)
    BALL_BOOST_CAP: 1.6,        // cap for paddle-up boost, as multiple of base
    BALL_MIN_ANGLE: 0.82,       // |tan| threshold ≈ 39deg reserved
    SLOW_FACTOR: 0.65,

    // --- paddle -----------------------------------------------------------------
    PADDLE_W: 105,
    PADDLE_H: 16,
    PADDLE_EXPAND: 1.6,
    PADDLE_MAX_VX: 1600,
    PADDLE_SMOOTH: 18,          // lerp speed (/s)
    PADDLE_BAND_TOP: 0.52,      // vertical travel band (fraction of H)
    PADDLE_BAND_BOT: 0.88,
    PADDLE_START_Y: 0.78,
    PADDLE_BOUNCE_ANGLE: 55,    // max exit angle from vertical (deg)
    PADDLE_ADD_VX: 1.2,         // paddle vx added to ball
    PADDLE_ADD_VY: 1.5,         // paddle vy added to ball (up-boost)
    MAG_RANGE: 90,
    MAG_PULL: 900,

    // --- bricks ---------------------------------------------------------------
    BRICK_TOP: 110,             // brick zone start y
    BRICK_H: 40,
    BRICK_GAP: 6,               // horizontal gap between bricks
    BRICK_ROW_GAP: 8,
    ANGLE_DEG: 35,              // angle brick qualifying steepness
    SPEED_FACTOR: 1.5,          // speed brick threshold vs base
    T_MAX_PER_LEVEL: 3,         // steel cap per level

    // --- scoring ------------------------------------------------------------------
    SCORE: { N: 50, A: 150, S: 150, G: 500, B: 250 },
    LASER_SCORE: 0.5,
    COMBO_WINDOW: 3,            // seconds to keep combo alive
    COMBO_MAX: 30,
    LIFE_BONUS_EVERY: 5,        // +1 life every N levels cleared
    CLEAR_LIFE_MULT: 1000,      // lives * tier * 1000
    OVERDRIVE_CLEAR_MULT: 5000, // tier * balls * 5000

    // --- capsules ----------------------------------------------------------------
    CAP_FALL_SPEED: 150,
    CAP_W: 26,
    CAP_H: 34,
    DROP: { N: 0.18, A: 0.25, S: 0.25, G: 1.0, B: 0.40 },
    DROP_SPECIAL_BONUS: 0.10,   // extra chance on A/S/G/B/bomb
    ABILITY_TIMES: {
      laser: 10, expand: 20, catch: 0, slow: 6, mag: 15, gold: 10, ball: 8
    },
    BALL_ABILITIES: ['FIRE', 'FROST', 'BLITZ', 'SPLIT', 'PULSE'],
    PADDLE_ABILITIES: ['LASER', 'MULTI', 'EXPAND', 'CATCH', 'SLOW', 'MAGNET', 'GOLDRUSH', 'LIFE'],
    MULTIBALL_CAP: 10,
    SPLIT_CAP: 10,

    // --- overdrive ------------------------------------------------------------------
    OD_TIER_TIME: 2,            // +1 tier every 2s while on top
    OD_EMBER_TIME: 3,           // ember ball every 3s
    OD_RAIN_TIME: 4,            // capsule rain every 4s
    OD_DRAIN_TIME: 5,           // grace drain (tier -> 0 over 5s)
    OD_MAX_BONUS_BALLS: 6,      // bonus ember balls (7 total)
    OD_SPEED_RAMP: 0.02,        // +2%/s speed ramp
    OD_MULTIS: [1, 2, 4, 8, 16],

    // --- lives / fx -------------------------------------------------------------------
    LIVES_START: 3,
    LIVES_MAX: 9,
    PARTICLE_CAP: 900,
    PARTICLE_BUDGET_MS: 14,     // throttle when frame cost exceeds this
    KEYBOARD_PADDLE_SPEED: 1400,
    TOUCH_SENSITIVITY: 1.15,

    // --- hud --------------------------------------------------------------------------
    HUD_H: 96,
    PILL_H: 22,
    BTN_SIZE: 48,               // touch-sized pause/mute zones
    FONT: 'system-ui, -apple-system, "Segoe UI", Roboto, Helvetica, Arial, sans-serif'
  };

  // ---- Events: tiny pub/sub ----------------------------------------------------
  var handlers = {};
  window.Events = {
    on: function (ev, fn) {
      (handlers[ev] = handlers[ev] || []).push(fn);
      return fn;
    },
    off: function (ev, fn) {
      var list = handlers[ev];
      if (!list) return;
      var i = list.indexOf(fn);
      if (i >= 0) list.splice(i, 1);
    },
    emit: function (ev, data) {
      var list = handlers[ev];
      if (!list) return;
      // copy so handlers can un/sub during emit
      list.slice().forEach(function (fn) { try { fn(data); } catch (e) { /* noop */ } });
    }
  };

  // Angle brick threshold helper: |tan(ANGLE_DEG)| — computed in game.js
  window.CFG.ANGLE_TAN = Math.tan(window.CFG.ANGLE_DEG * Math.PI / 180);
})();
