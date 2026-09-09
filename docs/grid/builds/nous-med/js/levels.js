// Handcrafted level layouts as ASCII maps + procedural generator.
// Legend: S standard, A armored, P prism, G ghost, V volt, X vortex, L gel,
//         T titan (indestructible), . empty
// Grid is 13 columns wide; rows render downward from FIELD_TOP.

export const LEVELS = [
  { name: 'FIRST LIGHT', rows: [
    '.............',
    '.SSSSSSSSSSS.',
    '.SSSSSSSSSSS.',
    '.SS.....SSS..',
    '.SS..........',
    '.SS.....SSS..',
  ]},
  { name: 'THE POCKET', rows: [
    'TTTTTTTTTTTTT',
    'T...........T',
    'T.SS.SS.SS..T',
    'T.SS.SS.SS..T',
    'T.SS.SS.SS..T',
    'TTT.......TTT',
    '..SS.....SS..',
  ]},
  { name: 'ARMORY', rows: [
    '.............',
    '.SASASASASAS.',
    '.SSSSSSSSSSS.',
    '.SASASASASAS.',
    '.SSSSSSSSSSS.',
    '..T.......T..',
    '..SS.....SS..',
  ]},
  { name: 'PRISM GATE', rows: [
    '..T.......T..',
    '..SPPPPPPPS..',
    '..S.......S..',
    '..SPPPPPPPS..',
    '..S.......S..',
    '..SS.....SS..',
    '.............',
  ]},
  { name: 'PHASE SHIFT', rows: [
    '.............',
    '.SGSGSGSGSGS.',
    '.SSSSSSSSSSS.',
    '.G.G.G.G.G.G.',
    '.SSSSSSSSSSS.',
    '.SGSGSGSGSGS.',
  ]},
  { name: 'STORM FRONT', rows: [
    'TTT.......TTT',
    'T.........S.T',
    'T.VSS.SS.SV.T',
    'T...SS.SS...T',
    'T.VS.....SV.T',
    'TTT.......TTT',
    '..S.SS.SS.S..',
  ]},
  { name: 'GRAVITY WELL', rows: [
    '.............',
    '..SS..X..SS..',
    '..SS.....SS..',
    '..SS..L..SS..',
    '..SS.....SS..',
    '..SS..X..SS..',
    '..LLLLLLL LL.'.replaceAll(' ', '.'),
  ]},
  { name: 'OZONE BREAK', rows: [
    'T..TTTTTTT..T',
    'T.SS.....SS.T',
    'T.SS.V.V.SS.T',
    'T..SS.P.SS..T',
    'TT..S.S.S..TT',
    'T....S.S....T',
    '..T..LLL..T..',
  ]},
];

const CHARS = 'SAPGVXL';
function rnd(n) { return (Math.random() * n) | 0; }

// Procedural: mirrored composition with a guaranteed funnel gap to the ozone.
export function generateLevel(n) {
  const rows = 6 + (n % 3);
  const grid = Array.from({ length: rows }, () => Array(13).fill('.'));
  const half = 7; // left half + mirror line col 6
  for (let r = 0; r < rows; r++) {
    for (let c = 0; c < half; c++) {
      if (Math.random() < 0.32) continue;
      // keep a funnel column clear sometimes
      if (c === 3 && r < 2 && Math.random() < 0.6) continue;
      let ch = CHARS[rnd(CHARS.length)];
      if (r === rows - 1 && Math.random() < 0.2) ch = 'T';
      grid[r][c] = ch;
      grid[r][12 - c] = ch;
    }
    // always carve at least one opening per row in the middle band
    const gap = 3 + rnd(7);
    grid[r][gap] = '.';
    grid[r][12 - gap] = '.';
  }
  // guaranteed vertical breakthrough channel
  const chan = 2 + rnd(9);
  for (let r = 0; r < rows; r++) { grid[r][chan] = '.'; }
  // a titan pocket on top to reward breakthrough
  grid[0][0] = 'T'; grid[0][12] = 'T';
  grid[0][1] = grid[0][11] = (grid[0][1] === '.' ? 'T' : grid[0][1]);
  return { name: 'DEEP FIELD ' + n, rows: grid.map(r => r.join('')) };
}

export function getLevel(i) {
  return i < LEVELS.length ? LEVELS[i] : generateLevel(i - LEVELS.length + 1);
}
