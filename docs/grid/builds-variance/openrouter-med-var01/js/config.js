// OVERKICK configuration — ALL tuning constants live here (top of file), commented.
'use strict';

const CONFIG = {
  // ---- Logical playfield (portrait) ----
  LOGICAL_W: 720,          // logical canvas width
  LOGICAL_H: 1100,         // logical canvas height

  // ---- Physics / timestep ----
  FIXED_DT: 1 / 60,        // fixed simulation step (seconds); deterministic for tests
  MAX_FRAME_DT: 0.1,       // clamp real frame dt to avoid spiral of death

  // ---- Ball ----
  BALL_R: 9,               // ball radius (logical px)
  BALL_BASE_SPEED: 560,    // launch speed
  BALL_MAX_SPEED: 1150,    // speed cap (punch / speedup)
  BALL_MIN_SPEED: 380,     // floor to avoid horizontal traps
  BALL_SPEEDUP_PER_SEC: 4, // gradual speed-up while a level runs
  TRAIL_MAX: 22,           // trail sample count
  SPLIT_CAP: 12,           // max total balls from SPLITTER
  FIRE_DURATION: 10,       // seconds fire ball lasts
  HEAVY_PIERCE_COUNT: 3,   // bricks a HEAVY ball plows through before reverting
  MAGNET_DURATION: 10,     // seconds magnet catch window
  STUCK_LAUNCH_ANGLE: 0.9, // radians from vertical when launching stuck ball

  // ---- Paddle ----
  PADDLE_W: 130,
  PADDLE_H: 22,
  PADDLE_BAND_TOP: 1100 - 260, // vertical band for paddle y
  PADDLE_BAND_BOTTOM: 1100 - 70,
  PADDLE_X_SPEED: 1400,    // keyboard x speed
  PADDLE_Y_SPEED: 760,     // keyboard y speed
  PADDLE_WIDE_TIME: 12,    // seconds of WIDE power-up
  PADDLE_WIDE_MULT: 1.55,
  PUNCH_VY_THRESHOLD: -260, // paddle must move up faster than this at contact
  PUNCH_BOOST: 320,        // extra speed added to ball on punch
  PUNCH_ANGLE_STEER: 0.55, // extra upward-angle steering on punch
  STICK_MAX_SPEED: 1600,   // thumbstick-driven paddle speed cap

  // ---- Heat / "Over the Top" ----
  HEAT_RIDE_RATE: 1.0,     // heat gained per second while >=1 ball rides
  HEAT_DECAY: 0.45,        // heat lost per second while no ball rides
  HEAT_STAGE_SECONDS: 2.0, // multiplier doubles every N seconds of riding
  HEAT_MAX_STAGES: 6,      // cap => 2^6 = x64
  HEAT_SLOWMO_SCALE: 0.3,  // time scale during breakthrough slow-mo
  HEAT_SLOWMO_TIME: 0.8,   // slow-mo duration (real seconds)

  // ---- Score ----
  SCORE_BRICK: 100,        // base points per standard brick
  SCORE_GOLD: 500,
  SCORE_LEVEL_CLEAR_LIFE_BONUS: 250,
  COMBO_WINDOW: 1.6,       // seconds to keep combo chain alive

  // ---- Bricks ----
  BRICK_COLS: 10,
  BRICK_TOP: 120,          // grid top in logical coords
  BRICK_H: 34,
  BRICK_GAP: 5,
  WEDGE_ANGLE: 55 * Math.PI / 180, // impact must be steeper than this vs surface normal
  KINETIC_MIN_SPEED: 760,  // ball must be at least this fast
  VOLATILE_RADIUS: 120,    // px explosion radius
  PHASE_PERIOD: 2.4,       // blink cycle seconds (solid ~60%)
  DRIFT_SPEED: 90,         // px/s drift
  DRIFT_RANGE: 70,

  // ---- Power-ups ----
  DROP_CHANCE: 0.12,       // weighted drop chance per broken brick
  PITY_LIMIT: 20,          // no more than N bricks without a drop
  CAPSULE_FALL_SPEED: 260,
  SLOW_TIME: 8,            // seconds of SLOW effect
  SLOW_FACTOR: 0.7,
  SPLIT_TIME: 12,          // seconds of splitter effect
  SHIELD_Y: 1100 - 40,     // shield net height

  // ---- Lives / flow ----
  START_LIVES: 3,
  BALLS_PER_ROW_RECOVER: 0, // (unused hook)

  // ---- Juice ----
  SHAKE_MAX: 22,           // max shake px at trauma=1
  TRAUMA_DECAY: 1.6,       // trauma units per second
  HITSTOP_BREAK: 0.05,     // s freeze on brick break
  HITSTOP_BIG: 0.09,       // volatile/gold/punch
  PARTICLE_CAP: 900,

  // ---- Audio ----
  MUSIC_BPM: 112,
  MUTE_KEY: 'overkick_mute',

  // ---- Colors (brick palette) ----
  COLORS: {
    bg0: '#07070f',
    bg1: '#0d1024',
    grid: 'rgba(120,140,255,0.07)',
    standard: '#5eead4',
    wedge: '#f4a261',
    kinetic: '#38bdf8',
    phase: '#c084fc',
    volatile: '#f87171',
    drift: '#facc15',
    gold: '#fde047',
    paddle: '#e2e8f0',
    ball: '#ffffff',
    fire: '#fb923c',
    heavy: '#a3a3a3',
    splitter: '#f472b6',
    ghost: '#67e8f9',
  },

  // Level list: names for the 12 handcrafted levels
  LEVEL_NAMES: [
    'First Contact', 'The Gap', 'Pyramid Scheme', 'Tunnel Vision',
    'Wedge Issues', 'Fast Lane', 'Blink of an Eye', 'Gold Rush',
    'Driftwood', 'Chain Reaction', 'The Gauntlet', 'Break on Through',
  ],
};