// Global tunables. All coordinates in "world units": the playfield is
// W x H virtual units, letterboxed/scaled to the real canvas.
export const W = 100;
export const H = 170; // portrait-ish world; landscape uses wider aspect

export const BALL_R = 1.6;
export const BALL_BASE_SPEED = 62;      // units/sec
export const BALL_MAX_SPEED = 170;
export const PADDLE_W = 20;
export const PADDLE_H = 2.6;
export const PADDLE_ZONE = 26;          // vertical zone above bottom the paddle may travel in
export const PADDLE_SPEED = 95;         // input-driven max velocity
export const PADDLE_ACCEL = 900;
export const PADDLE_FRICTION = 12;      // exponential decay when no input

// Breakthrough / rush
export const RUSH_RAMP_TIME = 1.9;      // sec of ball-above-wall before full frenzy visuals
export const RUSH_MULT_CAP = 20;
export const RUSH_DECAY_ON_DROP = 0.0;  // rush resets instantly when ball falls below wall

// Brick grid
export const BRICK_COLS = 11;
export const BRICK_TOP = 22;            // y of top of brick field
export const BRICK_H = 4.2;
export const BRICK_GAP = 0.7;

export const POWERUP_DROP_CHANCE = 0.16;
export const POWERUP_R = 2.4;
export const POWERUP_FALL = 17;

export const START_LIVES = 3;
export const SLOWMO_SCALE = 0.45;
export const SLOWMO_TIME = 5;

export const COLORS = {
  ball: '#cfeaff',
  ballFire: '#ff9b3d',
  ballPierce: '#b4ff6c',
  ballSticky: '#ff6cc4',
  ballMagnet: '#7c8cff',
  ballGhost: '#9affee',
  paddle: '#8fd4ff',
  paddleGlow: 'rgba(140,212,255,0.55)',
  wall: '#2a3050',
  bg0: '#07070f',
};