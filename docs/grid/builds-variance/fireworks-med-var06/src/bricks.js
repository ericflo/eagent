// Brick types + 5 handcrafted levels. Grid: 12 cols. No multi-hit armored bricks.
// type: reg (color tier), angle (break only within angle window), speed (break only if ball fast),
// move (slides horizontally), phase (blinks vulnerable/invulnerable), solid (invulnerable obstacle).

export const TYPES = {
  reg:    { label: 'REG',    hue: 200 },
  angle:  { label: 'ANGLE',  hue: 40 },
  speed:  { label: 'SPEED',  hue: 120 },
  move:   { label: 'MOVE',   hue: 280 },
  phase:  { label: 'PHASE',  hue: 330 },
  solid:  { label: 'SOLID',  hue: 220 },
};

// tier: 0..3 point value and color
const TIER_COLORS = [ ['#4fc3f7', '#0288d1'], ['#7ce87c', '#2e9d4e'], ['#ffd54f', '#e09b0a'], ['#ff6e9c', '#c2185b'] ];
export const TIER_POINTS = [50, 90, 140, 200];

export function makeBrick(o) {
  return {
    type: o.type || 'reg',
    tier: o.tier ?? 0,
    col: o.col, row: o.row,
    // angle brick: window center in degrees (0 = horizontal). ball incoming direction deg within +-win
    angleCenter: o.angleCenter ?? 45,
    angleWin: o.angleWin ?? 26,
    phasePeriod: 2.4,        // seconds per blink cycle for phase
    phaseOff: (o.col ?? 0) * 0.7 + (o.row ?? 0) * 0.4,
    baseCol: o.col, baseRow: o.row,
    moveRange: o.moveRange ?? 1.6,   // in cells
    moveSpeed: o.moveSpeed ?? 1.1,
    movePhase: (o.col ?? 0) * 0.9,
    dead: false,
    flashT: 0,
    clankT: 0,
    x: 0, y: 0, w: 0, h: 0,  // set in layout
  };
}

function row(type, rowIdx, tier, opts = {}) {
  return Array.from({ length: 12 }, (_, col) => makeBrick({ type, row: rowIdx, col, tier, ...opts }));
}

// ---- LEVELS ----------------------------------------------------------------
// Each level: { name, hint, bricks: fn(cellW) => [brick] }
// Grid is 12 cols x up to 9 rows in the brick field area.

export const LEVELS = [
  {
    name: 'FIRST CONTACT',
    hint: 'Break through the wall — get the ball UP TOP!',
    build: () => [
      ...row('reg', 0, 2),
      ...row('reg', 1, 1),
      ...row('reg', 2, 0),
      ...row('reg', 3, 0).slice(0, 8),
    ],
  },
  {
    name: 'SLANT',
    hint: 'Angle bricks only break on the shown incoming angle',
    build: () => {
      const bs = [...row('reg', 0, 2), ...row('reg', 1, 1)];
      // diagonal angle bricks
      for (let i = 0; i < 6; i++)
        bs.push(makeBrick({ type: 'angle', row: 2, col: 3 + i, tier: 3, angleCenter: -50 + i * 20, angleWin: 30 }));
      bs.push(...row('reg', 3, 0));
      return bs;
    },
  },
  {
    name: 'OVERDRIVE',
    hint: 'Speed bricks need a FAST ball — boost the paddle upward on contact!',
    build: () => {
      const bs = [...row('reg', 0, 3)];
      for (let i = 0; i < 12; i += 2)
        bs.push(makeBrick({ type: 'speed', row: 1, col: i, tier: 2 }));
      bs.push(...row('reg', 2, 1));
      for (let i = 1; i < 12; i += 2)
        bs.push(makeBrick({ type: 'speed', row: 3, col: i, tier: 2 }));
      return bs;
    },
  },
  {
    name: 'DRIFT',
    hint: 'Moving bricks — time your shots',
    build: () => {
      const bs = [];
      for (const r of [0, 2, 4]) {
        for (let c = 0; c < 12; c += 1) {
          if (r === 0) bs.push(makeBrick({ type: 'move', row: r, col: c, tier: 2, moveRange: 1.8, moveSpeed: 1.0 + c * 0.05 }));
          else if ((c + r) % 3 !== 2) bs.push(makeBrick({ type: r === 2 ? 'move' : 'reg', row: r, col: c, tier: r === 2 ? 1 : 0 }));
        }
      }
      return bs;
    },
  },
  {
    name: 'PULSE',
    hint: 'Phase bricks blink — strike when solid',
    build: () => {
      const bs = [...row('reg', 0, 3)];
      for (let i = 0; i < 12; i++)
        bs.push(makeBrick({ type: 'phase', row: 1, col: i, tier: 2 }));
      bs.push(makeBrick({ type: 'solid', row: 2, col: 5 }), makeBrick({ type: 'solid', row: 2, col: 6 }));
      for (let i = 0; i < 12; i++)
        if (i < 4 || i > 7) bs.push(makeBrick({ type: 'phase', row: 3, col: i, tier: 2 }));
      bs.push(...row('reg', 4, 1));
      return bs;
    },
  },
];
