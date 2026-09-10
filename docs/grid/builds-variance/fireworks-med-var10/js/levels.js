// SKYBREAK — level definitions.
//
// Each level is a grid of characters (12 columns wide):
//   .  empty          S  standard (1 hit, any hit breaks)
//   A  armored — only Heavy or Fire ball breaks it
//   G  angle-locked — breaks only when hit inside its angle band
//   V  speed brick — breaks only above SPEED_BRICK_THRESHOLD
//   M  mover — whole row slides side to side
//   B  bomb — explodes, taking its 8 neighbors
//
// Design rule: every level has a deliberate route "on top" (gap, corridor,
// or fragile canopy) because the on-top state is the heart of the game.

const LEVELS = [
  {
    name: 'FIRST LIGHT',
    rows: [
      '............',
      '..SSSSSSSS..',
      '..SSSSSSSS..',
      '..SSSSSSSS..',
    ],
  },
  {
    name: 'THE CANOPY',
    rows: [
      'SSSSSSSSSSSS',
      'SSS......SSS',
      'SSSSSBSSSSSS',
      'SSSSSSSSSSSS',
      '..SS.SS.SS..',
    ],
  },
  {
    name: 'PILLARS',
    rows: [
      'S.S.AA.A.S.S',
      'S.S.AA.A.S.S',
      'SSSSSSSSSSSS',
      '...S.BB.S...',
      'SSSSSSSSSSSS',
    ],
  },
  {
    name: 'ANGLED GATES',
    rows: [
      'G.G.G.G.G.G.',
      'SSSSSSSSSSSS',
      '....S..S....',
      'GSGSSGSSGGSS',
      'SSSSSSSSSSSS',
    ],
  },
  {
    name: 'VELOCITY',
    rows: [
      'SSSVSSSSVSSS',
      'SSSSSSSSSSSS',
      '..V.SSSS.V..',
      'SSSSSBSSSSSS',
      'SSSVSSSSVSSS',
    ],
  },
  {
    name: 'DRIFTWOOD',
    moverSpeed: 40,
    rows: [
      'MMMMMMMMMMMM',
      '..S.SSSS.S..',
      'MMMMMMMMMMMM',
      'SSSS.BB.SSSS',
      '..S.SSSS.S..',
    ],
  },
  {
    name: 'CORRIDOR',
    rows: [
      'SSSSSSSSSSSS',
      'SSA......ASS',
      'SSS.SSSS.SSS',
      '............',
      'SSSSBSSBSSSS',
      'GG.VGSSGV.GG',
    ],
  },
  {
    name: 'FORTRESS',
    rows: [
      'AA.SSSSSS.AA',
      'A..S.GG.S..A',
      'A.SSSSSSSS.A',
      '...S.BB.S...',
      'VVSVSSSSVSVV',
      'SSSSSSSSSSSS',
    ],
  },
  {
    name: 'STORMFRONT',
    moverSpeed: 55,
    rows: [
      'M.M.M.M.M.M.',
      'SSSSSSSSSSSS',
      '.GV.VBBV.VG.',
      'SSSSSSSSSSSS',
      'M.M.M.M.M.M.',
      '..SS.GG.SS..',
    ],
  },
  {
    name: 'SKYFALL',
    rows: [
      'SSSSSBSSSSSS',
      'GAVSSMMSSVAG',
      'SSSSSSSSSSSS',
      'SSA.B..B.ASS',
      'SSSSSSSSSSSS',
      'VSGSMMMSSGSV',
      'SSSSSSSSSSSS',
    ],
  },
];

// Power-up drop weights (from CONFIG/POWERUPS weights)
function dropTable() {
  const t = {};
  for (const k in POWERUPS) t[k] = POWERUPS[k].weight;
  return t;
}
