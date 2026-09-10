// Shared constants and tuning knobs.
export const FIELD = { w: 900, h: 1300 };           // logical field size (portrait)
export const WALL = 10;                              // side wall thickness (visual)
export const PADDLE = {
  w: 130, h: 18, bandTop: 0.72,                      // band = bottom 28% of field
  baseSpeed: 900, accel: 6000, vSmooth: 14,
  smashBoost: 190, slowDamp: 0.62, smashVy: -240,
};
export const BALL = {
  r: 9, minSpeed: 430, maxSpeed: 1250, baseSpeed: 560,
  speedRamp: 0.06,                                   // +6% per paddle hit up to max
  maxFlatAngle: 0.22,                                // radians away from pure-horizontal floor
};
export const BRICK = {
  w: 76, h: 30, gapX: 4, gapY: 4, top: 120, cols: 11,
};
export const FRENZY = {
  zoneTop: 300,            // ball must be above this y and moving up/sideways among bricks
  buildTime: 0.35,         // seconds up there before frenzy triggers
  rampRate: 0.16,          // multiplier gain per second while up (x2.5 @ ~10s)
  maxMult: 8,
  decayRate: 0.55,         // multiplier lost per second back below
};
export const CAPSULE = {
  fallSpeed: 230, w: 40, h: 26, dropChanceBase: 0.19,
};
export const PHYS = {
  fixedDt: 1 / 120, maxSubSteps: 5, ballSubstep: 6,
};
export const COLORS = {
  bg: '#05060f',
  brick: {
    STD:    '#4da3ff',
    ANGLE:  '#2dd4bf',
    VELOCITY:'#f97316',
    PHASE:  '#c084fc',
    EXPLOSIVE:'#ef4444',
    MOVER:  '#facc15',
    REGEN:  '#34d399',
    UNBREAK:'#64748b',
    ARMOR:  '#94a3b8',
  },
};
export const POWER_DEFS = {
  FIRE:    { label: 'FIREBALL', color: '#fb923c', kind: 'ball', dur: 12 },
  HEAVY:   { label: 'HEAVY',    color: '#a3a3a3', kind: 'ball', dur: 10 },
  SPLIT:   { label: 'SPLITTER', color: '#f472b6', kind: 'ball', dur: 10 },
  GHOST:   { label: 'GHOST',    color: '#a5b4fc', kind: 'ball', dur: 10 },
  WIDE:    { label: 'WIDE',     color: '#22d3ee', kind: 'paddle', dur: 14 },
  STICKY:  { label: 'STICKY',   color: '#a3e635', kind: 'paddle', dur: 14 },
  SLOWMO:  { label: 'SLOW-MO',  color: '#93c5fd', kind: 'game', dur: 5 },
  LIFE:    { label: '1-UP',     color: '#fb7185', kind: 'instant' },
  SHRINK:  { label: 'SHRINK',   color: '#f43f5e', kind: 'bad', dur: 10 },
};
// weighted pool for capsule rolls
export const POWER_POOL = [
  ['FIRE', 3], ['HEAVY', 2], ['SPLIT', 2], ['GHOST', 2],
  ['WIDE', 3], ['STICKY', 2], ['SLOWMO', 2], ['LIFE', 1], ['SHRINK', 2],
];
