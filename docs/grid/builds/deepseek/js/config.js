// Central tuning constants for ROOFTOP — a modern Breakout.
export const CFG = {
  // Logical playfield size (portrait, 9:16). Everything is drawn in these
  // coordinates and scaled to fit the screen.
  W: 720,
  H: 1280,

  // ---- Paddle -------------------------------------------------------------
  PADDLE_W: 150,          // base width
  PADDLE_H: 24,
  PADDLE_MIN_Y: 1060,     // how high the paddle may travel
  PADDLE_MAX_Y: 1190,     // how low the paddle may travel
  PADDLE_SPEED: 950,      // px/s for keyboard / thumbstick
  PADDLE_VY_MAX: 1400,    // clamp on vertical velocity (used to boost the ball)

  // ---- Ball ---------------------------------------------------------------
  BALL_R: 11,
  BALL_SPEED: 540,        // launch speed
  BALL_SPEED_MIN: 380,
  BALL_SPEED_MAX: 1500,
  PADDLE_HIT_ACCEL: 1.02, // speed multiplier per paddle hit
  PADDLE_BOOST_VY: 0.55,  // how much paddle vertical velocity transfers to the ball
  PADDLE_BOOST_VX: 0.25,
  MAX_BALLS: 8,

  // ---- Bricks -------------------------------------------------------------
  BRICK_W: 66,
  BRICK_H: 30,
  BRICK_GAP: 6,
  BRICK_COLS: 10,
  BRICK_TOP: 150,

  // Special-brick conditions
  ANGLE_STEEP: 1.15,      // |vy| must exceed |vx| * this to break an angle brick
  SPEED_BRICK_MIN: 700,   // ball speed needed to break a speed brick

  // ---- Power-ups ----------------------------------------------------------
  POWERUP_W: 36,
  POWERUP_H: 22,
  POWERUP_SPEED: 200,
  FIRE_TIME: 8,
  WIDE_TIME: 10,
  SLOW_TIME: 6,
  LASER_TIME: 8,
  MAGNET_TIME: 8,
  SHIELD_TIME: 8,
  LASER_SPEED: 950,
  LASER_RATE: 0.22,

  // ---- Rooftop ("on top of the bricks") mode ------------------------------
  ROOFTOP_MULT_INTERVAL: 2.0,   // seconds per multiplier step
  ROOFTOP_MULT_MAX: 10,
  ROOFTOP_BALL_INTERVAL: 3.5,   // seconds between bonus balls
  ROOFTOP_BALL_MAX: 6,
  ROOFTOP_SPEED_RAMP: 40,       // px/s^2 added to ball speed while on top

  // ---- Scoring / lives ----------------------------------------------------
  START_LIVES: 3,
  LIFE_EVERY: 15000,      // extra life every N points
  BRICK_POINTS: { normal: 100, angle: 150, speed: 150, steel: 250, explosive: 200, golden: 500, core: 1000 },
  LEVEL_CLEAR_BONUS: 1000, // per remaining life
};
