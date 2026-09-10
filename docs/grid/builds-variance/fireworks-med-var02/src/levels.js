// Level definitions. Each row: array of chars, one char per column.
// Legend: . empty  1 standard  A angle-lock  V velocity-lock  P phase
//         X explosive  M mover  R regen  U unbreakable  O armored (rare multi-hit)
const L1 = {
  name: 'FIRST CONTACT',
  hint: 'Standard bricks. A few EXPLOSIVE to open the wall fast.',
  rows: [
    '...........',
    '...........',
    '.111111111.',
    '.111X11111.',
    '.111111111.',
    '.1111111X1.',
    '.111111111.',
  ],
};
const L2 = {
  name: 'THIN ICE',
  hint: 'ANGLE-LOCK: hit them at a shallow angle — direct hits CLANK off.',
  rows: [
    '...........',
    '.A1111111A.',
    '.111111111.',
    '.11A111A11.',
    '.111111111.',
    '.A1111111A.',
  ],
};
const L3 = {
  name: 'HEAVY TRAFFIC',
  hint: 'VELOCITY-LOCK: only fast balls crack them. Smash up through!',
  rows: [
    '...........',
    '.V11V11V11.',
    '.111111111.',
    '.11V111V11.',
    '.11111X111.',
    '.V11V11V11.',
  ],
};
const L4 = {
  name: 'BLINK',
  hint: 'PHASE bricks flicker solid/ghost. Only solid ones can break.',
  rows: [
    '...........',
    '.P11P11P11.',
    '.111111111.',
    '.P1P111P1P.',
    '.111111111.',
    '.11P111P11.',
  ],
};
const L5 = {
  name: 'SHIFTWORK',
  hint: 'MOVER bricks slide. REGEN bricks grow back — clear their neighbours!',
  rows: [
    '...........',
    '.M1111111M.',
    '.11R111R11.',
    '.111111111.',
    '.M11R1R11M.',
    '.111111111.',
  ],
};
const L6 = {
  name: 'THE FORTRESS',
  hint: 'Everything at once. Punch through, get on top, FRENZY.',
  rows: [
    'U.........U',
    '.AVPVXVPVA.',
    '.1M1R1R1M1.',
    '.VAPAPAPAV.',
    '.11P1M1P11.',
    '.RX1AVA1XR.',
    '.111111111.',
  ],
};
const L7 = {
  name: 'GRIDLOCK',
  hint: 'Dense lattice. Angles and explosions are your friends.',
  rows: [
    'A.A.A.A.A.A',
    '.1.1.1.1.1.',
    'V.V.V.V.V.V',
    '.1.1.1.1.1.',
    'P.P.P.P.P.P',
    '.1.1.1.1.1.',
    'M...X.X...M',
  ],
};
const L8 = {
  name: 'LONG HAUL',
  hint: 'Tall wall, armored core. Endurance run — stack that multiplier.',
  rows: [
    '...........',
    '.111111111.',
    '.1A1V1V1A1.',
    '.1O1R1R1O1.',
    '.111111111.',
    '.1P1M1M1P1.',
    '.111X1X111.',
    '.111111111.',
  ],
};

export const LEVELS = [L1, L2, L3, L4, L5, L6, L7, L8];
export const BRICK_CHARS = {
  '1': 'STD', 'A': 'ANGLE', 'V': 'VELOCITY', 'P': 'PHASE', 'X': 'EXPLOSIVE',
  'M': 'MOVER', 'R': 'REGEN', 'U': 'UNBREAK', 'O': 'ARMOR',
};
