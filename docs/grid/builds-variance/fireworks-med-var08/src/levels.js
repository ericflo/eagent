// Level definitions. Pure data — safe to import from Node tests.
//
// Legend (10 columns per row):
//   .  empty            S  standard brick
//   A  ANGLE brick      V  SPEED brick
//   P  PHASE brick      M  MOVING brick
//   X  EXPLODING brick
//
// Deliberate gaps/channels between bricks are how the ball earns its way
// up above the field — the core fantasy of the game.

export const LEVELS = [
  {
    name: 'Warm Up',
    rows: [
      '..SSSSSS..',
      '.SSSSSSSS.',
      'SSS.XX.SSS',
      '.SSSSSSSS.',
      '..SSSSSS..',
    ],
  },
  {
    name: 'Angle Alley',
    rows: [
      '.A.A.A.A.A',
      'SS..SS..SS',
      'SSSSSSSSSS',
      'SS..SS..SS',
      'A..A..A..A',
    ],
  },
  {
    name: 'Speed Bump',
    rows: [
      'VVVVVVVVVV',
      'S..S..S..S',
      'VVXVVVXVVV',
      'SSSSSSSSSS',
      '.S.SS.SS.S',
    ],
  },
  {
    name: 'Ghost Protocol',
    rows: [
      '.PPPPPPPP.',
      'S..S..S..S',
      'PP.PPPP.PP',
      'S..S..S..S',
      '.PPPPPPPP.',
    ],
  },
  {
    name: 'Slipstream',
    rows: [
      'MMMMMMMMMM',
      'S..S..S..S',
      'MXXMMMMXXM',
      'S..S..S..S',
      'MMMMMMMMMM',
    ],
  },
  {
    name: 'The Gauntlet',
    rows: [
      'A.V.P.V.A.',
      '.VXPXVXPX.',
      'MA.SS.S.AM',
      '.PXPXVXPX.',
      '.A.V..V.A.',
    ],
  },
  {
    name: 'Chaos Crown',
    rows: [
      'AVPXMVXMPA',
      'V.X.P.X.V.',
      'MPAVXXVAPM',
      'V.X.P.X.V.',
      'AMVXPXVXMA',
    ],
  },
];

export const LEVEL_COUNT = LEVELS.length;
