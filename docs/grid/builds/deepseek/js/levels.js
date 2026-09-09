// Level layouts. Each string is one row of bricks, 10 columns wide.
// Legend:
//   .  empty
//   #  normal brick
//   A  angle brick   — only breaks when hit at a steep (mostly vertical) angle
//   S  speed brick   — only breaks when the ball is fast enough
//   T  steel brick   — indestructible unless hit by a fire ball
//   X  explosive     — blows up neighbours when broken
//   G  golden brick  — big points, always drops a power-up
//   C  core brick    — only breaks once it is the last breakable brick left

export const LEVELS = [
  // 1 — warm up
  [
    '##########',
    '##########',
    '##########',
    '##########',
  ],
  // 2 — angle bricks
  [
    '....AA....',
    '...A##A...',
    '..A####A..',
    '.A######A.',
    'A########A',
  ],
  // 3 — speed bricks
  [
    'SSSSSSSSSS',
    '##########',
    '##########',
    '##########',
  ],
  // 4 — explosives
  [
    '....XX....',
    '...X##X...',
    '..X####X..',
    '.X######X.',
    'X########X',
  ],
  // 5 — steel & gold
  [
    'TGTGTGTGTG',
    '##########',
    '##########',
    '##########',
  ],
  // 6 — first real mix
  [
    'ASXGTSXGAS',
    '#T#G#T#G#T',
    '##########',
    '##########',
  ],
  // 7 — steel fortress
  [
    'TTTTTTTTTT',
    'T..XX..T..',
    'T..XX..T..',
    'TTTTTTTTTT',
    '##########',
  ],
  // 8 — the gauntlet
  [
    'ASASASASAS',
    'SASASASASA',
    '##########',
    '##########',
  ],
  // 9 — gold rush
  [
    'GGGGGGGGGG',
    'XXXXXXXXXX',
    '##########',
    '##########',
  ],
  // 10 — the core
  [
    '....C.....',
    'TTTTTTTTTT',
    '##########',
    '##########',
  ],
  // 11 — chaos
  [
    'ASXGTGXSAX',
    '#G#S#A#T#X',
    'X#T#A#S#G#',
    '##########',
  ],
  // 12 — finale
  [
    'TGTGTGTGTG',
    'ASXGTSXGAS',
    '#T#G#T#G#T',
    'XXXXXXXXXX',
    '##########',
    '##########',
  ],
];

// Endless mode: procedurally generated layouts that get harder over time.
export function randomLevel(n) {
  const rows = Math.min(4 + Math.floor(n / 2), 10);
  const types = ['#', '#', '#', '#', '#', 'A', 'S', 'X', 'G', 'T'];
  const map = [];
  for (let r = 0; r < rows; r++) {
    let row = '';
    for (let c = 0; c < 10; c++) {
      // Later levels get denser and meaner.
      const p = Math.min(0.55 + n * 0.02, 0.92);
      if (Math.random() > p) { row += '.'; continue; }
      const t = types[Math.floor(Math.random() * types.length)];
      row += t;
    }
    map.push(row);
  }
  return map;
}
