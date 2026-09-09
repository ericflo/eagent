// config.js — ALL tunable numbers for Overdrive Breakout live here.
// Every gameplay/feel constant referenced by other modules is defined once
// in this file so feel can be tuned in one place.

export const FIELD = {
  W: 1500,          // logical field width
  H: 1000,          // logical field height (landscape)
  BALL_LOST_Y: 1080 // ball is lost when it falls past this
};

export const ZONE = {
  TOP: 260          // overdrive zone: ball y < this fills the meter
};

export const PADDLE = {
  W: 180,
  H: 26,
  R: 13,            // corner radius
  MIN_Y: 820,
  MAX_Y: 950,
  WIDE_W: 280,
  WIDE_TIME: 12,
  KEY_SPEED_X: 900, // keyboard horizontal max speed
  KEY_ACCEL: 4000,
  KEY_FRICTION: 8,  // exp decay per second on keyboard velocity
  KEY_SPEED_Y: 420,
  POINTER_LERP: 22, // lerp rate for pointer follow (per second, ~0.4 @60fps)
  SLAM_DVY: 260,    // extra upward velocity added by slam (paddle moving up)
  SLAM_SPEED_REF: 420,
  DOWN_PENALTY: 60, // paddle moving down: reduce launch up to this much
  PADDLE_VX_TRANSFER: 0.35, // vx += paddle.vx * this on bounce
  MAX_BOUNCE_DEG: 65 // max angle from vertical on paddle bounce
};

export const BALL = {
  R: 11,
  BASE_SPEED: 520,
  SPEED_PER_LEVEL: 0.04, // +4% per level
  SPEED_MAX_BONUS: 0.80, // cap +80%
  MIN_SPEED: 480,
  MAX_SPEED: 1250,
  MIN_VY_FRACTION: 0.25, // min |vy| as fraction of speed
  TRAIL_LEN: 14,
  MAX_BALLS: 6,
  LIVES: 3
};

export const PHYSICS = {
  HZ: 240,          // fixed physics substeps per second
  MAX_DT: 1 / 20    // render dt clamp
};

export const BRICK = {
  COLS: 20,
  ROWS: 8,
  W: 30,
  H: 46,
  GAP: 6,
  OX: 45,
  OY: 300
};

// Per-type brick points (base, before multiplier)
export const POINTS = {
  std: 10,
  glass: 5,
  angle: 25,
  speed: 25,
  mover: 20,
  bomb: 15,
  mini: 5,
  key: 40,
  lock: 30,
  magnet: 20,
  mirror: 20
};

export const BRICK_RULES = {
  ANGLE_TOLERANCE_DEG: 35,  // angle bricks: within ±35° of vertical
  SPEED_THRESHOLD: 640,     // speed bricks: needs ball speed >= this
  MAGNET_RADIUS: 220,
  MAGNET_STEER: 300,        // u/s^2
  MOVER_SPEED: 60,
  MINI_W: 15,
  MINI_H: 23
};

export const OVERDRIVE = {
  GAIN: 24,     // +24/s while any ball in zone
  DRAIN: 30,    // -30/s otherwise
  MULT_STEP: 20, // multiplier = 2^floor(meter/20)
  MAX: 100,
  BANK_BONUS_PER_LEVEL: 250, // +250 x level when meter banks at 100
  REARM_BELOW: 60,           // can bank again once drained below 60
  BANDS: [20, 40, 60, 80, 100]
};

export const POWERUPS = {
  FALL_SPEED: 140,
  DROP_CHANCE: 0.22,
  SIZE: 44,
  TYPES: {
    MULTI: { label: 'M', color: '#00e5ff', name: 'MULTI' },
    WIDE: { label: 'W', color: '#4dff6a', name: 'WIDE', duration: 12 },
    SLOW: { label: 'S', color: '#4d8dff', name: 'SLOW', duration: 8, factor: 0.75, floor: 360 },
    LASER: { label: 'L', color: '#ff4d5e', name: 'LASER', duration: 8, interval: 0.35, speed: 1400 },
    MAGNET: { label: 'P', color: '#c44dff', name: 'MAGNET', duration: 8, radius: 260 },
    BOOST: { label: 'B', color: '#ffa24d', name: 'BOOST', meter: 20 }
  }
};

export const LASER = {
  W: 6,
  H: 26,
  SPEED: 1400
};

export const FX = {
  PARTICLE_CAP: 400,
  SHAKE_MIN: 2,
  SHAKE_MAX: 10,
  HITSTOP_BRICK: 0.015,
  HITSTOP_BOMB: 0.045,
  COMBO_EVERY: 5,
  COMBO_BONUS_PER_LEVEL: 50,
  LEVEL_BONUS_PER_LEVEL: 500,
  LEVELCLEAR_TIME: 2.2
};

export const AUDIO = {
  MASTER: 0.5
};

export const STORAGE_KEYS = {
  BEST: 'odbreakout.best',
  TIP: 'odbreakout.tipShown'
};

// Brick type metadata (colors / draw hints)
export const BRICK_STYLE = {
  std:   { color: '#3d7bff', glow: '#6ea0ff' },
  glass: { color: 'rgba(190,230,255,0.55)', glow: '#cfeaff' },
  angle: { color: '#ff9a2e', glow: '#ffc37a' },
  speed: { color: '#ff4444', glow: '#ff8a8a' },
  ramp:  { color: '#8b93a7', glow: '#aeb6c9' },
  magnet:{ color: '#b14dff', glow: '#d18aff' },
  mirror:{ color: '#c9d4e0', glow: '#eef4fb' },
  mover: { color: '#3ddc6a', glow: '#7dffa0' },
  bomb:  { color: '#8e2430', glow: '#ff5a6a' },
  mini:  { color: '#b04a58', glow: '#ff8a9a' },
  key:   { color: '#ffce3d', glow: '#ffe98a' },
  lock:  { color: '#c08a4e', glow: '#ffce9a' }
};

// Key/lock pair tints (gold keys, bronze locks tinted per pair)
export const PAIR_TINTS = ['#ffe066', '#66ffe0', '#ff9ac2', '#b0ff66'];
