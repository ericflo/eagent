// ---------------------------------------------------------------------------
// config.js — central tuning constants
// ---------------------------------------------------------------------------

export const W = 720;               // logical width
export const H = 1080;              // logical height (portrait)
export const WALL = 24;             // side wall thickness (playfield inset)
export const CEIL = 12;             // ceiling y (ball bounces here)

export const BRICK = {
  cols: 13,
  w: 48,
  h: 24,
  gap: 4,
  top: 150,                         // y of first brick row
};

export const PADDLE = {
  w: 122,
  h: 16,
  bandTop: H - 292,                 // top of paddle movement band (~25% of H)
  bandBottom: H - 46,               // bottom of band
  ease: 14,                         // how fast paddle eases to target (per s)
  maxV: 1500,                       // max paddle velocity (stick/keys)
};

export const BALL = {
  r: 8,
  base: 480,                        // launch speed
  min: 300,
  max: 1150,
  launchAngle: 62 * Math.PI / 180,  // max angle from vertical on paddle hit
  creep: 7,                         // speed gained per paddle hit
  creepCap: 780,                    // creep ceiling
};

export const POWERUP = {
  r: 19,
  fall: 190,
  catchW: 1.35,                     // catch box width multiplier vs paddle
};

// Impact-angle gate: ball must be travelling "steeply" downward.
// |vx/vy| must be <= STEEP (0 == perfectly vertical). ~29 degrees.
export const STEEP = 0.55;

// Speed gate for SPEED / ARMORED bricks.
export const SPEED_GATE = 640;

// Overdrive
export const OVERDRIVE = {
  lead: 26,                         // ball must clear topmost brick by this much
  multTime: 2.6,                    // seconds for multiplier to double
  multMax: 99,
  heatTime: 9,                      // seconds to reach full heat
  chargeRate: 52,                   // charge pts / s at heat 0
  chargeHeat: 55,                   // extra pts / s at full heat
  wallBonus: 9,                     // charge per wall bounce while up top
};

export const COMBO = {
  hitstopAt: 5,                     // combo count where hitstop starts
  hitstopMax: 0.075,
};

export const COLORS = {
  bg0: '#070b18',
  bg1: '#0d1430',
  wall: '#233156',
  ball: '#eaf4ff',
  paddle: '#7dd6ff',
  text: '#dceaff',
};

// Palette shift for heat: 0 = cool blue, 1 = white-hot.
// Hue travels 205 → 28 the *long* way (up through violet/magenta) so it never
// passes through sickly green mid-ramp.
export function heatColor(heat, sat = 85, light = 62) {
  const h = (205 + 183 * Math.min(1, heat)) % 360;
  const s = sat * (1 - Math.max(0, heat - 0.75) * 2.2);
  const l = light + 22 * Math.max(0, heat - 0.6);
  return `hsl(${h} ${Math.max(0, s)}% ${Math.min(96, l)}%)`;
}
