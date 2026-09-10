// levels.js — level-data module for Rooftop Breakout.
//
// Pure ES module: no DOM, no window, no imports, and zero side effects at
// import time. It is imported both by the game engine (browser) and by node
// tests, so everything here is plain data plus one pure function.
//
// Grid format
// -----------
//   Every level is an array of row strings. Each row is EXACTLY 10 characters
//   (COLS). Columns run left-to-right, rows top-to-bottom (row index 0 is the
//   top row of bricks). A '.' is an empty cell; every other character is a
//   brick type from LEGEND (see below).
//
// Design rules followed by both the handcrafted and procedural levels:
//   • crowns ('c') only on the top two rows (they "sit on the roof");
//   • 't' bricks are never placed on the bottom row;
//   • no row is ever a solid wall of indestructible gold;
//   • the lower rows keep some normal bricks so the player always has
//     something simple to break near the paddle.

export const COLS = 10;

export const LEGEND = {
  n: { name: 'Standard', color: '#4fc3f7', desc: 'Breaks on any hit.' },
  c: { name: 'Crown', color: '#ffd54f', desc: 'Sits on the roof. Breaks easily; clearing crowns grows your Rooftop bonus.' },
  a: { name: 'Angle', color: '#ba68c8', desc: 'Breaks only when hit at a steep angle.' },
  s: { name: 'Speed', color: '#ff7043', desc: 'Breaks only when hit fast enough.' },
  g: { name: 'Gold', color: '#ffd700', desc: 'Indestructible. Deflects and speeds the ball up.' },
  b: { name: 'Bomb', color: '#f44336', desc: 'Explodes, blasting nearby bricks.' },
  r: { name: 'Reborn', color: '#26a69a', desc: 'Breaks only if hit again quickly, otherwise it regrows.' },
  t: { name: 'Top', color: '#7e57c2', desc: 'Breaks only when hit from above — get on the roof!' },
  w: { name: 'Wild', color: '#ffca28', desc: 'Always drops a power-up.' },
};

// ---------------------------------------------------------------------------
// Handcrafted levels
// ---------------------------------------------------------------------------
// Six levels that teach the mechanics one at a time and grow from 6 rows to
// 11 rows. Early levels leave the bottom rows empty so the ball has room near
// the paddle.

export const LEVELS = [
  // ---- Level 1 — "First Ramp" ---------------------------------------------
  // Only standard bricks in a clean pyramid. Teaches basic breaking; the
  // whole playfield stays open at the bottom.
  [
    'nnnnnnnnnn',
    '.nnnnnnnn.',
    '..nnnnnn..',
    '...nnnn...',
    '....nn....',
    '..........',
  ],

  // ---- Level 2 — "Crown Jewel Temple" --------------------------------------
  // A full crown row on the roof (the Rooftop bonus treat), then a temple of
  // standard bricks with a pair of angle bricks low on the flanks to teach
  // the first special brick type.
  [
    'cccccccccc',
    'nnnnnnnnnn',
    'nnnnnnnnnn',
    'nannnnnnan',
    'nn.nnnn.nn',
    '.nnnnnnnn.',
    '..........',
    '..........',
  ],

  // ---- Level 3 — "Speed Diamond" -------------------------------------------
  // Two indestructible gold pillars act as deflectors, while a diamond of
  // speed bricks in the center teaches 's'. The gold columns keep the ball
  // bouncing hard — which is exactly what you need to break the diamond.
  [
    'ngnnnnnngn',
    'ngnnnnnngn',
    'ngnnnnnngn',
    'nssnnnnssn',
    '.nnssssnn.',
    '..nnssnn..',
    '..........',
    '..........',
    '..........',
  ],

  // ---- Level 4 — "Bomb Fortress" -------------------------------------------
  // A symmetric fortress: twin bomb towers (2x2 bomb clusters) guarding a
  // keep of reborn bricks. Bombs blast their neighbors, and the reborn row
  // punishes slow play. A second bomb pair sits in the bailey below.
  [
    'nnnnnnnnnn',
    'nbbnnnnbbn',
    'nbbnnnnbbn',
    'nnrrrrrrnn',
    'nnnnnnnnnn',
    'nnbbnnbbnn',
    '.nnnnnnnn.',
    '..........',
    '..........',
  ],

  // ---- Level 5 — "The Rooftop Puzzle" --------------------------------------
  // THE rooftop level. A roof of 't' bricks covers a standard wall, with
  // crowns sitting on the roof above it. The only way onto the roof is up the
  // two side channels (cols 0 and 9 are open through rows 0-1), so you must
  // thread the ball up one side, clear the crowns, and only then can you drop
  // down onto the 't' roof from above to break it open. Angle bricks guard
  // the lower flanks.
  [
    '.cccccccc.',
    '.tttttttt.',
    'nnnnnnnnnn',
    'nnnnnnnnnn',
    'nannnnnnan',
    '.nnnnnnnn.',
    '..nnnnnn..',
    '...nnnn...',
    '..........',
    '..........',
  ],

  // ---- Level 6 — "Grand Finale" --------------------------------------------
  // A rich mix: crowns and wild bricks on top, gold pillars flanking bomb
  // sentinels, a reborn keep, speed chevrons, bombs deep in the walls, and an
  // angle row near the bottom. Everything the player has learned, one stage.
  [
    'ccwccccwcc',
    '.nnnnnnnn.',
    'gbnnnnnnbg',
    'gbrrrrrrbg',
    'gssnnnnssg',
    'nbwsssswbn',
    'nnbwsswbnn',
    'nnnnbbnnnn',
    '.annnnnnn.',
    '..nnnnnn..',
    '..........',
  ],
];

// ---------------------------------------------------------------------------
// Procedural endless levels
// ---------------------------------------------------------------------------

// Deterministic seeded PRNG (mulberry32). Same seed -> same sequence.
function mulberry32(seed) {
  let a = seed >>> 0;
  return function () {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// Pick a brick type for a cell, using the difficulty probabilities of the
// current level. Crowns only come from the top-row callers.
const TYPE_FOR = { bomb: 'b', gold: 'g', angle: 'a', speed: 's', reborn: 'r', wild: 'w', top: 't' };

function pickSpecialType(rand, p) {
  const roll = rand();
  let acc = 0;
  for (const key of ['bomb', 'gold', 'angle', 'speed', 'reborn', 'wild', 'top']) {
    acc += p[key];
    if (roll < acc) return TYPE_FOR[key];
  }
  return 'n';
}

// Choose what a filled cell becomes, given its row. Row 0 is all crowns;
// row 1 mixes crowns with the regular mix; the bottom two rows stay mostly
// normal so there is always something easy to hit near the paddle.
function chooseCellType(rand, p, row, rows) {
  if (row === 0) return 'c';
  if (row === 1) return rand() < 0.45 ? 'c' : pickSpecialType(rand, p);
  if (row >= rows - 2) return rand() < 0.7 ? 'n' : pickSpecialType(rand, p);
  return pickSpecialType(rand, p);
}

// Occasionally nudge the grid back inside the 50-65% fill band.
// New cells use the same per-row type logic, so density fixing never disturbs
// the level's character (top row crowns etc.).
function adjustDensity(grid, rows, rand, p) {
  const total = rows * COLS;
  const filled = () => grid.reduce((sum, row) => sum + row.filter((c) => c !== '.').length, 0);
  let count = filled();
  let guard = 0;
  while (count / total < 0.5 && guard++ < rows * COLS) {
    const r = Math.floor(rand() * rows);
    const c = Math.floor(rand() * COLS);
    if (grid[r][c] === '.') {
      grid[r][c] = chooseCellType(rand, p, r, rows);
    }
    count = filled();
  }
  guard = 0;
  while (count / total > 0.65 && guard++ < rows * COLS) {
    const r = 1 + Math.floor(rand() * (rows - 2)); // keep row 0 and the last two rows intact
    const c = Math.floor(rand() * COLS);
    if (grid[r][c] !== '.') {
      grid[r][c] = '.';
    }
    count = filled();
  }
}

// Enforce the level invariants (deterministic: uses only rand and grid).
function sanitize(grid, rows, rand, level) {
  // Crowns only on the top two rows.
  for (let r = 2; r < rows; r++) {
    for (let c = 0; c < COLS; c++) {
      if (grid[r][c] === 'c') grid[r][c] = 'n';
    }
  }
  // No row is a gold wall: cap gold at 4 per row, converting extras to normal.
  for (let r = 0; r < rows; r++) {
    let gold = 0;
    for (let c = 0; c < COLS; c++) {
      if (grid[r][c] === 'g') {
        gold++;
        if (gold > 4) grid[r][c] = 'n';
      }
    }
  }
  // Top row must offer a real rooftop treat (>= 3 crowns).
  let crowns = grid[0].filter((c) => c === 'c').length;
  let guard = 0;
  while (crowns < 3 && guard++ < COLS) {
    const c = Math.floor(rand() * COLS);
    if (grid[0][c] === '.') {
      grid[0][c] = 'c';
      crowns++;
    }
  }
  // Bottom row keeps at least two normal bricks.
  let normals = grid[rows - 1].filter((c) => c === 'n').length;
  guard = 0;
  let c = 0;
  while (normals < 2 && c < COLS && guard++ < COLS * 2) {
    if (grid[rows - 1][c] !== 'n') {
      grid[rows - 1][c] = 'n';
      normals++;
    }
    c++;
  }
  // 't' bricks only break when hit from above, so they never go on the bottom
  // row (a last-row 't' would be nearly unbreakable).
  for (let c = 0; c < COLS; c++) {
    if (grid[rows - 1][c] === 't') grid[rows - 1][c] = 'n';
  }
  // As the level number grows, special types must actually show up.
  const joined = () => grid.map((row) => row.join('')).join('');
  let gridStr = joined();
  if (level >= 3 && !gridStr.includes('b')) {
    for (let r = 2; r <= rows - 2 && !gridStr.includes('b'); r++) {
      if (grid[r][0] === '.') {
        grid[r][0] = 'b';
        gridStr = joined();
      }
    }
  }
  if (level >= 4 && !gridStr.includes('g')) {
    for (let r = 2; r <= rows - 2 && !gridStr.includes('g'); r++) {
      if (grid[r][1] === '.') {
        grid[r][1] = 'g';
        gridStr = joined();
      }
    }
  }
}

// Return the brick rows for endless level number n (n >= 1).
// Deterministic: the grid depends only on n (mulberry32 seeded with n), so
// endlessLevel(n) always returns the identical layout.
//
// - rows: clamp(8 + floor(n/2), 8, 12)
// - the top row is crowns, row 1 mixes crowns into the regular mix
// - fill density is kept around 50-65% per level
// - bombs, gold, angle, speed and wild all become more frequent as n grows
// - layouts are generated symmetrically (mirror across the middle column
//   gap), so rows read as structures rather than noise
export function endlessLevel(n) {
  const level = Math.max(1, Math.floor(n));
  const rows = Math.min(12, Math.max(8, 8 + Math.floor(level / 2)));
  const rand = mulberry32(level);

  // Difficulty probabilities, scaled with the level number and capped.
  const p = {
    bomb: Math.min(0.02 + (level - 1) * 0.006, 0.1),
    gold: Math.min(0.02 + (level - 1) * 0.006, 0.09),
    angle: Math.min(0.08 + (level - 1) * 0.008, 0.15),
    speed: Math.min(0.08 + (level - 1) * 0.008, 0.15),
    reborn: Math.min(0.01 + (level - 1) * 0.004, 0.06),
    wild: Math.min(0.01 + (level - 1) * 0.004, 0.05),
    top: Math.min(0.01 + (level - 1) * 0.004, 0.05),
  };
  const fillP = 0.7; // chance each cell inside a row's span is filled

  const grid = Array.from({ length: rows }, () => new Array(COLS).fill('.'));

  // Fill the spans symmetrically: decide once per column pair (c, 9-c), so
  // every generated row reads as a balanced castle/vessel silhouette.
  for (let r = 0; r < rows; r++) {
    // Per-row side margins produce a varied silhouette.
    let margin;
    if (r === 0) margin = rand() < 0.5 ? 1 : 0; // crown row: open side lanes
    else if (r === 1) margin = rand() < 0.3 ? 1 : 0;
    else if (r >= rows - 2) margin = 1 + Math.floor(rand() * 3); // 1..3, tapers down
    else margin = Math.floor(rand() * 3); // 0..2
    for (let c = margin; c <= 4; c++) {
      const mirror = COLS - 1 - c;
      if (rand() >= fillP) continue;
      const ch = chooseCellType(rand, p, r, rows);
      grid[r][c] = ch;
      grid[r][mirror] = ch;
    }
  }

  sanitize(grid, rows, rand, level);
  adjustDensity(grid, rows, rand, p);
  sanitize(grid, rows, rand, level); // re-enforce after density tweaks

  return grid.map((row) => row.join(''));
}
