// Shared constants & tuning for BREAKTHROUGH
export const W = 900, H = 1000;

export const OZONE_H = 78;            // glowing band at the very top
export const FIELD_TOP = OZONE_H + 62; // top line of brick field (y)
export const BRICK_COLS = 13;
export const BRICK_W = 62, BRICK_H = 30, BRICK_GAP = 4;
export const FIELD_LEFT = (W - BRICK_COLS * (BRICK_W + BRICK_GAP) + BRICK_GAP) / 2;

export const ZONE_TOP = H * 0.72;     // paddle movement zone
export const ZONE_BOT = H - 14;

export const PADDLE_W = 130, PADDLE_H = 16;
export const BALL_R = 8;
export const BALL_SPEED_MIN = 380, BALL_SPEED_MAX = 980;
export const SMASH_BONUS = 260;       // extra speed from upward paddle momentum
export const PADDLE_UP_SMASH_V = 260; // paddle vy needed for full smash

export const CHAOS_DECAY_TIME = 4.0;  // seconds of grace below the field
export const CHAOS_CAP = 64;
export const CHAOS_DOUBLING = 3.5;    // seconds on top to double multiplier

export const DROP_CHANCE = 0.12;

export const COLORS = {
  ozone: '#8f6bff',
  ozoneGlow: '#c9b6ff',
  ball: '#eafcff',
  paddle: '#00f6ff',
  hud: '#9fb4d8',
  accent: '#ff2d78',
  bgTop: '#0b1030',
};

export const BRICK_STYLE = {
  S:  { color: '#2de2a6', color2: '#0f8f68', pts: 50 },
  A:  { color: '#5b6a8c', color2: '#2c3450', pts: 120 },   // armored (speed)
  P:  { color: '#ffd166', color2: '#b8860b', pts: 120 },   // prism (angle)
  G:  { color: '#9ad7ff', color2: '#3d6fa8', pts: 90 },    // ghost
  V:  { color: '#c77dff', color2: '#6a2ca0', pts: 100 },   // volt
  X:  { color: '#ff6b9d', color2: '#a12a5c', pts: 100 },   // vortex
  L:  { color: '#7dffb2', color2: '#2f9e6a', pts: 70 },    // gel
  T:  { color: '#46536e', color2: '#232a3d', pts: 0 },     // titan indestructible
};

export const PT_STYLE = {
  M: { name: 'MULTIBALL',  color: '#ff2d78', glyph: '≡' },
  F: { name: 'FIREBALL',   color: '#ff7b2d', glyph: '★' },
  H: { name: 'PHASE BALL', color: '#b8fffa', glyph: '◌' },
  W: { name: 'WIDE',       color: '#2de2a6', glyph: '↔' },
  S: { name: 'SLOW-MO',    color: '#7da7ff', glyph: '◷' },
  G: { name: 'MAGNET',     color: '#ffd166', glyph: '⊗' },
  D: { name: 'SHIELD',     color: '#c77dff', glyph: '▭' },
};
// weighted drop table
export const DROP_TABLE = 'M M M F F F F W W W W H H H H G G S S D D';
