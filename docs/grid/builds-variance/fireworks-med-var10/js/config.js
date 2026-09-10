// SKYBREAK — global tuning constants
const CONFIG = {
  // World is designed at a portrait resolution and scaled to fit the viewport.
  W: 960,
  H: 1280,

  // Physics
  BASE_BALL_SPEED: 640,          // px/s at launch
  MAX_BALL_SPEED: 1280,          // hard cap
  BALL_RAMP_PER_RALLY: 1.012,    // slight acceleration per paddle/brick hit
  MIN_VX_RATIO: 0.18,            // anti-horizontal-trap: min |vx|/|v|
  PADDLE_INFLUENCE: 0.32,        // how much paddle vx bends the ball
  PADDLE_SMASH_BOOST: 220,       // extra speed when paddle moves up at impact
  PADDLE_ABSORB: 0.55,           // speed cut when paddle moves down at impact
  SMASH_VY_THRESHOLD: -140,      // paddle vy faster than this counts as a smash

  // Paddle
  PADDLE_W: 150,
  PADDLE_W_EXPAND: 1.6,
  PADDLE_H: 24,
  PADDLE_BOTTOM_Y: 1180,         // resting (lowest) paddle center y
  PADDLE_TOP_Y: 900,             // highest paddle center y
  PADDLE_SPEED: 1150,            // keyboard speed px/s
  PADDLE_LASER_CD: 0.55,

  // Ball
  BALL_R: 9,
  LAUNCH_ANGLE: -65 * Math.PI / 180, // from vertical, randomized +/- spread
  LAUNCH_SPREAD: 20 * Math.PI / 180,

  // Hype / ON TOP state
  ONTOP_Y: 120,                  // ball above this y counts as "inside the arcade"
  HYPE_RAMP: 1.55,               // multiplier growth per second while on top
  HYPE_MAX_MULT: 50,
  HYPE_DECAY_HALF_LIFE: 0.9,     // seconds for multiplier to halve when off top
  HYPE_MIN_VIS_MULT: 2,          // mult display starts here
  HYPE_TIER_SPEEDS: [1.0, 1.15, 1.3, 1.5, 1.75], // music layer gates by tier

  // Speed brick
  SPEED_BRICK_THRESHOLD: 860,    // |v| needed to break a speed brick
  FAST_BALL_RATIO: 0.78,         // ratio of threshold where ball starts glowing

  // Particles / FX budgets
  MAX_PARTICLES: 900,
  MAX_FLOATERS: 40,
  MAX_TRAIL: 26,

  // Power-up drops
  DROP_CHANCE: 0.16,
  DROP_FALL_SPEED: 210,

  // Lives / levels
  START_LIVES: 3,
  LEVEL_COUNT: 10,

  // UI
  SHAKE_DECAY: 7.5,
};

// Brick category palette (neon on dark)
const PALETTE = {
  standard:   ['#35e0ff', '#1e9dbd'],
  armored:    ['#b7c4d6', '#6b7789'],
  angle:      ['#ffd23f', '#c2901a'],
  speed:      ['#b06bff', '#6b3fb0'],
  mover:      ['#4dffa6', '#1e9d64'],
  bomb:       ['#ff6b35', '#b03a12'],
};

// Power-up definitions: kind -> { color, label, duration (0 = instant), weight }
const POWERUPS = {
  fire:    { color: '#ff6b35', label: 'FIRE',   duration: 9,  weight: 12 },
  multi:   { color: '#35e0ff', label: 'MULTI',  duration: 0,  weight: 11 },
  heavy:   { color: '#ff4d6d', label: 'HEAVY',  duration: 9,  weight: 10 },
  ghost:   { color: '#c9d6ff', label: 'GHOST',  duration: 8,  weight: 9 },
  expand:  { color: '#4dffa6', label: 'EXPAND', duration: 12, weight: 12 },
  sticky:  { color: '#ffd23f', label: 'STICKY', duration: 10, weight: 9 },
  laser:   { color: '#ff9de2', label: 'LASER',  duration: 10, weight: 10 },
  shield:  { color: '#7db8ff', label: 'SHIELD', duration: 0,  weight: 9 },
  life:    { color: '#6dffb0', label: 'LIFE',   duration: 0,  weight: 4 },
};

// What each ball type can do, consulted by collision code
const BALL_TYPES = {
  normal: { breaksArmored: false, breaksSpeed: false, burnsThrough: false, phases: false, radiusMul: 1 },
  fire:   { breaksArmored: true,  breaksSpeed: true,  burnsThrough: true,  phases: false, radiusMul: 1.15 },
  heavy:  { breaksArmored: true,  breaksSpeed: true,  burnsThrough: false, phases: false, radiusMul: 1.45 },
  ghost:  { breaksArmored: false, breaksSpeed: false, burnsThrough: false, phases: true,  radiusMul: 1 },
};
