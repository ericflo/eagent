// src/game/levels.js
//
// Attic Breaker — level layouts and endless generator.
//
// Imports Brick + grid metrics from './bricks.js' (same worker's module —
// this is not one of the "other worker" modules the no-import rule targets).
//
// -----------------------------------------------------------------------
// ASCII MAP FORMAT
// Each level is authored as an array of strings, one row per brick row
// (top row of the array = topmost brick row = row 0), each row padded/
// truncated to BRICK_COLS (10) characters, one character per column:
//
//   ' ' or '.'   empty cell (no brick) — carve tunnels/ramps with these
//   '#'          normal        (1 hit, breaks)
//   '^'          angle brick, requires the ball to be travelling UP    (dir  -90deg)
//   '<'          angle brick, requires the ball to be travelling LEFT  (dir  180deg)
//   '>'          angle brick, requires the ball to be travelling RIGHT (dir    0deg)
//   'S'          speed        (needs a fast ball)
//   'A'          attic        (only breaks hit from above)
//   'O'          shield       (rotating invulnerability arc)
//   'M'          mirror, '/' orientation (deflects up-left/up-right)
//   'N'          mirror, '\' orientation
//   'W'          mover        (slides horizontally along its row)
//   'E'          explosive    (chain-detonates neighbours)
//   'G'          magnet       (bends nearby ball paths)
//   'X'          steel        (indestructible, sparse!)
//
// No new characters were added — the original 13-symbol alphabet already
// covers every mechanic asked for (chutes/ramps are just '.'/' ' gaps in an
// otherwise-filled map, so no dedicated "gap" glyph is needed beyond space).
//
// `parseMap(rows, opts)` turns such an array into `Brick[]`. `opts.startRow`
// offsets which world row the first ASCII row lands on (default 0, i.e. the
// very top of the brick field at BRICK_TOP). `opts.brickOpts` is merged into
// every brick's ctor opts; `opts.brickOpts.tint` (css color) and
// `opts.brickOpts.hue` (number, base hue-shift in degrees) give each level a
// distinct color identity for its `normal` bricks — per-cell variance is
// still layered on top so a full wall still reads as rich, not flat.

import { Brick, BRICK_COLS } from './bricks.js';

const CHAR_TYPE = {
  '#': 'normal',
  '^': 'angle', '<': 'angle', '>': 'angle',
  'S': 'speed',
  'A': 'attic',
  'O': 'shield',
  'M': 'mirror', 'N': 'mirror',
  'W': 'mover',
  'E': 'explosive',
  'G': 'magnet',
  'X': 'steel',
};

const CHAR_DIR = { '^': -Math.PI / 2, '<': Math.PI, '>': 0 };
const CHAR_ORIENT = { 'M': '/', 'N': '\\' };

// deterministic per-cell hue variance, matches bricks.js's own default so
// levels that pass a `hue` base still look like a coherent family of tiles.
function cellHueVariance(col, row) {
  return ((col * 29 + row * 53) % 41) - 20;
}

export function parseMap(rows, opts = {}) {
  const startRow = opts.startRow || 0;
  const bricks = [];
  for (let r = 0; r < rows.length; r++) {
    const line = rows[r];
    for (let c = 0; c < BRICK_COLS; c++) {
      const ch = line[c] || ' ';
      if (ch === ' ' || ch === '.') continue;
      const type = CHAR_TYPE[ch];
      if (!type) continue;
      const brickOpts = { ...(opts.brickOpts || {}) };
      if (CHAR_DIR[ch] !== undefined) brickOpts.dir = CHAR_DIR[ch];
      if (CHAR_ORIENT[ch] !== undefined) brickOpts.orient = CHAR_ORIENT[ch];
      if (type === 'normal' && brickOpts.hue !== undefined) {
        brickOpts.hue = brickOpts.hue + cellHueVariance(c, startRow + r);
      }
      bricks.push(new Brick(c, startRow + r, type, brickOpts));
    }
  }
  return bricks;
}

function buildBricks(def) {
  const brickOpts = {};
  if (def.tint) brickOpts.tint = def.tint;
  if (def.hue !== undefined) brickOpts.hue = def.hue;
  return parseMap(def.rows, { startRow: 0, brickOpts });
}

// -----------------------------------------------------------------------
// Hand-designed levels. Each row string is exactly 10 characters.
// Ordered as a difficulty ramp: 1-3 are gentle, almost entirely `normal`,
// with an obvious carved seam. Every level after that introduces exactly
// ONE new conditional brick type versus the previous set, and the subtitle
// hints at the mechanic in plain language.

const L = {}; // name -> {rows, name, subtitle, ballSpeed, tint, hue?}

L.firstLight = {
  name: 'First Light',
  subtitle: 'Punch the middle out and look up.',
  ballSpeed: 680,
  tint: '#3fd0ff',
  rows: [
    '##  ##  ##',
    '##      ##',
    '# ###### #',
    '#        #',
    '# ###### #',
    '##      ##',
    '##########',
  ],
};

L.theSeam = {
  name: 'The Seam',
  subtitle: 'A narrow chute on the left flank — thread it.',
  ballSpeed: 700,
  tint: '#3fd0ff',
  hue: 18,
  rows: [
    '  ########',
    '  ########',
    '  ####### ',
    '  ###### #',
    '  #####  #',
    '  ####  ##',
    '  ###  ###',
    '  ##  ####',
  ],
};

L.brokenCeiling = {
  name: 'Broken Ceiling',
  subtitle: 'A zigzag seam — no straight shot, but there is a way.',
  ballSpeed: 720,
  tint: '#4dd2ff',
  hue: -10,
  rows: [
    '#### #####',
    '###  #####',
    '## #####  ',
    '#  ##### #',
    ' # ##### #',
    '## ##### #',
    '##########',
  ],
};

L.angleGate = {
  name: 'Angle Gate',
  subtitle: 'Straight-up hits only — the gate reads your angle.',
  ballSpeed: 740,
  tint: '#ff5fd1',
  rows: [
    '####  ####',
    '###    ###',
    '###    ###',
    '##  ^^  ##',
    '##      ##',
    '#  ####  #',
    '# ###### #',
    '##########',
  ],
};

L.speedTrial = {
  name: 'Speed Trial',
  subtitle: 'Only a hot ball breaks the dial-bricks.',
  ballSpeed: 780,
  tint: '#ffd23f',
  rows: [
    'SSSSSSSSSS',
    '#S      S#',
    '#S # ## S#',
    '#S      S#',
    '# SSSSSS #',
    '##      ##',
    '# ###### #',
  ],
};

L.atticTease = {
  name: 'Attic Tease',
  subtitle: 'The roof only breaks if you hit it from above.',
  ballSpeed: 780,
  tint: '#8f5bff',
  rows: [
    '#  AAAA  #',
    '#  ####  #',
    '#        #',
    '# ###### #',
    '#  ####  #',
    '##      ##',
    '##########',
  ],
};

L.shieldRing = {
  name: 'Shield Ring',
  subtitle: 'The rotating arc guards one side at a time — time it.',
  ballSpeed: 800,
  tint: '#5bffb0',
  rows: [
    'O#O#OO#O#O',
    '#        #',
    '# O##  O #',
    '#        #',
    '## #OO# ##',
    '#        #',
    '##########',
  ],
};

L.mirrorRamp = {
  name: 'Mirror Ramp',
  subtitle: 'Glass panes fling the ball upward — ride the ramp.',
  ballSpeed: 800,
  tint: '#c9f2ff',
  rows: [
    '##########',
    '#    A   #',
    '#   M    #',
    '#  M     #',
    '# M      #',
    '#M       #',
    '##########',
    '## #### ##',
  ],
};

L.commuterGate = {
  name: 'Commuter Gate',
  subtitle: 'The doorway slides — catch it while it is open.',
  ballSpeed: 810,
  tint: '#5ad1ff',
  rows: [
    '#W######W#',
    '#        #',
    '   W  W   ',
    '#        #',
    '#W######W#',
    '##      ##',
    '# ###### #',
  ],
};

L.magnetPit = {
  name: 'Magnet Pit',
  subtitle: 'The core drags you back down — clear it to escape.',
  ballSpeed: 810,
  tint: '#4dffee',
  rows: [
    '#  ####  #',
    '#  #  #  #',
    '   # G#   ',
    '#  #  #  #',
    '#  ####  #',
    '##      ##',
    '# ###### #',
  ],
};

L.detonationChain = {
  name: 'Detonation Chain',
  subtitle: 'One spark, many bricks — start the chain reaction.',
  ballSpeed: 830,
  tint: '#ff4d4d',
  rows: [
    '##########',
    '#E##E##E##',
    '##########',
    '#  ####  #',
    'E   AA   E',
    '#  ####  #',
    '##      ##',
    '# ###### #',
  ],
};

L.fortress = {
  name: 'Fortress',
  subtitle: 'A steel roof, one true gap — earn the attic.',
  ballSpeed: 850,
  tint: '#9aa5b1',
  rows: [
    'XXXX  XXXX',
    'X##A##A##X',
    'X########X',
    'X##    ##X',
    'X## ## ##X',
    'X#      #X',
    'X# #### #X',
    'XX      XX',
    'X XX##XX X',
  ],
};

L.hollowCore = {
  name: 'Hollow Core',
  subtitle: 'Pop the wall, rattle around the empty chamber.',
  ballSpeed: 860,
  tint: '#7dffb0',
  hue: 30,
  rows: [
    '##########',
    '#        #',
    '# ###### #',
    '# #    # #',
    '# #    # #',
    '# ###### #',
    '#        #',
    '##########',
  ],
};

L.chandelier = {
  name: 'Chandelier',
  subtitle: 'A hanging cluster under an open ceiling.',
  ballSpeed: 870,
  tint: '#ffb0ff',
  rows: [
    '          ',
    '  #    #  ',
    '  ##  ##  ',
    ' O#A##A#O ',
    '  ##  ##  ',
    '  #    #  ',
    '#        #',
    '##########',
  ],
};

L.crossfire = {
  name: 'Crossfire',
  subtitle: 'Angles left, angles right — no straight shots survive.',
  ballSpeed: 880,
  tint: '#ff5fd1',
  hue: 12,
  rows: [
    '<# ## #>#>',
    '#>#  #<#< ',
    '<# #> #<# ',
    '#         ',
    '# ###### #',
    '##      ##',
    '##########',
  ],
};

L.latticeworks = {
  name: 'Latticeworks',
  subtitle: 'Steel bars weave through — find where they do not.',
  ballSpeed: 900,
  tint: '#9fb3ff',
  rows: [
    'X#X#  X#X#',
    '#  X##X  #',
    'X#X#  X#X#',
    '#  X##X  #',
    'X#X    X#X',
    '#X######X#',
    '# ###### #',
    '##      ##',
  ],
};

L.speedGauntlet = {
  name: 'Speed Gauntlet',
  subtitle: 'Keep it fast the whole way — uppercut, do not coast.',
  ballSpeed: 960,
  tint: '#ffd23f',
  hue: -14,
  rows: [
    'SSSSSSSSSS',
    'S########S',
    'SS  SS  SS',
    'S########S',
    'SS  SS  SS',
    'S########S',
    '##      ##',
    '# ###### #',
  ],
};

L.doubleMagnet = {
  name: 'Twin Cores',
  subtitle: 'Two magnets fight for the ball — break both to win the sky.',
  ballSpeed: 980,
  tint: '#4dffee',
  hue: 20,
  rows: [
    '#  ####  #',
    '#G##  ##G#',
    '#  ####  #',
    '# #    # #',
    '#  ####  #',
    '#G##  ##G#',
    '#  ####  #',
    '##      ##',
    '# ###### #',
  ],
};

L.summit = {
  name: 'The Summit',
  subtitle: 'Everything the attic has taught you, all at once.',
  ballSpeed: 1020,
  tint: '#8f5bff',
  hue: -8,
  rows: [
    'X#O#AA#O#X',
    '#  ####  #',
    ' M#S  S#N ',
    '#  ####  #',
    'XG      GX',
    '#< #### >#',
    '#  W  W  #',
    '##      ##',
    'X########X',
  ],
};

export const LEVELS = [
  L.firstLight, L.theSeam, L.brokenCeiling, L.angleGate, L.speedTrial,
  L.atticTease, L.shieldRing, L.mirrorRamp, L.commuterGate, L.magnetPit,
  L.detonationChain, L.fortress, L.hollowCore, L.chandelier, L.crossfire,
  L.latticeworks, L.speedGauntlet, L.doubleMagnet, L.summit,
];

// -----------------------------------------------------------------------
// Endless procedural generator (level index LEVELS.length+1 and beyond,
// 0-based idx >= LEVELS.length). Escalates difficulty and rotates through
// distinct archetypes instead of one shape with more junk. Every generated
// level is guaranteed to have a steel-free breach column (real attic route)
// and is re-validated with validateLevel-equivalent guards before return.

function mulberry32(seed) {
  let a = seed >>> 0;
  return function () {
    a |= 0; a = (a + 0x6D2B79F5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const ENDLESS_TINTS = ['#3fd0ff', '#ff5fd1', '#ffd23f', '#8f5bff', '#5bffb0', '#ff9d4d', '#4dffee', '#9fb3ff'];
const ARCHETYPES = ['funnel', 'fortress', 'chandelier', 'lattice', 'pit'];

function emptyGrid(rows) {
  return Array.from({ length: rows }, () => Array(BRICK_COLS).fill(' '));
}

// Defensive post-process: open a side on any interior destructible cell that
// ended up boxed in on all four orthogonal sides by steel. Edge columns and
// the very top/bottom rows are always safe (no neighbour on that side), so
// only true interior traps need fixing.
function dissolveTraps(grid) {
  const rows = grid.length;
  for (let r = 1; r < rows - 1; r++) {
    for (let c = 1; c < BRICK_COLS - 1; c++) {
      const ch = grid[r][c];
      if (ch === ' ' || ch === 'X') continue;
      const up = grid[r - 1][c], down = grid[r + 1][c];
      const left = grid[r][c - 1], right = grid[r][c + 1];
      if (up === 'X' && down === 'X' && left === 'X' && right === 'X') {
        grid[r][c - 1] = '#';
      }
    }
  }
}

// Funnel: walls converge toward a single central breach column.
function archFunnel(grid, rng, breach, condPick) {
  const rows = grid.length;
  for (let r = 0; r < rows; r++) {
    const halfWidth = Math.max(1, Math.round((rows - r) * 0.42));
    for (let c = 0; c < BRICK_COLS; c++) {
      if (c === breach) continue;
      const distFromCenter = Math.abs(c - breach);
      if (distFromCenter > halfWidth) continue;
      if (rng() < 0.12) continue;
      grid[r][c] = rng() < 0.7 ? '#' : condPick();
    }
  }
}

// Fortress: a steel perimeter/roof punctuated by one soft column (breach)
// and a couple of destructible interior rooms.
function archFortress(grid, rng, breach, condPick) {
  const rows = grid.length;
  for (let r = 0; r < rows; r++) {
    for (let c = 0; c < BRICK_COLS; c++) {
      if (c === breach) continue; // breach stays entirely clear of steel
      const isRing = r === 0 || r === rows - 1 || c === 0 || c === BRICK_COLS - 1;
      if (isRing) {
        grid[r][c] = rng() < 0.75 ? 'X' : '#';
      } else if (rng() < 0.55) {
        grid[r][c] = rng() < 0.65 ? '#' : condPick();
      }
    }
  }
}

// Chandelier: a hanging central cluster, wide open ceiling above it.
function archChandelier(grid, rng, breach, condPick) {
  const rows = grid.length;
  const clusterTop = Math.max(2, Math.floor(rows * 0.3));
  for (let r = clusterTop; r < rows; r++) {
    const spread = Math.min(4, r - clusterTop + 2);
    for (let dc = -spread; dc <= spread; dc++) {
      const c = 4 + dc + (dc >= 0 ? 1 : 0);
      if (c < 0 || c >= BRICK_COLS || c === breach) continue;
      if (rng() < 0.15) continue;
      grid[r][c] = rng() < 0.75 ? '#' : condPick();
    }
  }
}

// Lattice: alternating steel/normal weave with the breach column always clear.
function archLattice(grid, rng, breach, condPick) {
  const rows = grid.length;
  for (let r = 0; r < rows; r++) {
    for (let c = 0; c < BRICK_COLS; c++) {
      if (c === breach) continue;
      if ((c + r) % 2 === 0) {
        if (rng() < 0.55) grid[r][c] = 'X';
      } else if (rng() < 0.7) {
        grid[r][c] = rng() < 0.6 ? '#' : condPick();
      }
    }
  }
}

// Pit: a magnet-laden core with a guarded rim; breach column left open.
function archPit(grid, rng, breach, condPick) {
  const rows = grid.length;
  const midRow = Math.floor(rows / 2);
  for (let r = 0; r < rows; r++) {
    for (let c = 0; c < BRICK_COLS; c++) {
      if (c === breach) continue;
      if (rng() < 0.16) continue;
      if (r === midRow && Math.abs(c - 5) <= 1) { grid[r][c] = 'G'; continue; }
      grid[r][c] = rng() < 0.68 ? '#' : condPick();
    }
  }
}

const ARCH_FN = {
  funnel: archFunnel,
  fortress: archFortress,
  chandelier: archChandelier,
  lattice: archLattice,
  pit: archPit,
};

function proceduralLevel(idx) {
  const n = idx - LEVELS.length + 1; // 1-based endless counter
  const rng = mulberry32(1000 + idx * 977);
  const difficulty = Math.min(1, (n - 1) / 20); // ramps to 1 over ~20 endless levels
  const rows = 8 + Math.floor(rng() * 5); // 8..12 rows
  const archName = ARCHETYPES[(n - 1) % ARCHETYPES.length];
  const grid = emptyGrid(rows);

  // The breach column is a hard guarantee: never touched by steel, always a
  // real, climbable route from the paddle box into the attic.
  const breach = 1 + Math.floor(rng() * (BRICK_COLS - 2));

  const condChance = 0.18 + difficulty * 0.32;
  const condTypes = ['^', '<', '>', 'S', 'A', 'O', 'M', 'N', 'E', 'G'];
  const condPick = () => (rng() < condChance ? condTypes[Math.floor(rng() * condTypes.length)] : '#');

  ARCH_FN[archName](grid, rng, breach, condPick);

  // Extra sprinkle of general gaps so no archetype reads as one solid slab.
  for (let r = 0; r < rows; r++) {
    for (let c = 0; c < BRICK_COLS; c++) {
      if (grid[r][c] !== ' ' && rng() < 0.05) grid[r][c] = ' ';
    }
  }

  // A couple of passes in case fixing one trap reveals a neighbouring one.
  dissolveTraps(grid);
  dissolveTraps(grid);
  dissolveTraps(grid);

  // HARD GUARANTEES (defensive, cheap to re-check here even though the
  // archetypes above already respect them):
  // 1. breach column never contains steel.
  for (let r = 0; r < rows; r++) if (grid[r][breach] === 'X') grid[r][breach] = ' ';
  // 2. no fully-steel row.
  for (let r = 0; r < rows; r++) {
    if (grid[r].every((ch) => ch === 'X')) grid[r][breach] = '#';
  }
  // 3. at least a handful of plain normal bricks exist somewhere.
  let normals = grid.flat().filter((ch) => ch === '#').length;
  let guard = 0;
  while (normals < 6 && guard < 80) {
    const r = Math.floor(rng() * rows), c = Math.floor(rng() * BRICK_COLS);
    if (grid[r][c] !== '#' && grid[r][c] !== 'X') { grid[r][c] = '#'; normals++; }
    guard++;
  }

  const rowsStrs = grid.map((row) => row.join(''));
  const tint = ENDLESS_TINTS[(n - 1) % ENDLESS_TINTS.length];
  const archLabel = archName.charAt(0).toUpperCase() + archName.slice(1);
  return {
    name: `Endless ${archLabel} ${n}`,
    subtitle: 'The attic keeps building itself.',
    ballSpeed: Math.min(1500, 900 + difficulty * 500 + n * 8),
    tint,
    hue: ((n * 37) % 60) - 30,
    rows: rowsStrs,
  };
}

// Build a full level object (Brick[] + metadata + spawn) for 1-based index i.
export function buildLevel(i) {
  const idx = i - 1;
  const def = idx < LEVELS.length ? LEVELS[idx] : proceduralLevel(idx);
  const bricks = buildBricks(def);
  return {
    bricks,
    name: def.name,
    subtitle: def.subtitle,
    ballSpeed: def.ballSpeed,
    tint: def.tint,
    hue: def.hue,
    spawn: { x: 500, y: 1300 },
  };
}

// -----------------------------------------------------------------------
// Validation: solvability sanity checks. Returns {ok, errors:[...]}.
export function validateLevel(level) {
  const errors = [];
  const bricks = level.bricks || [];

  // (a) at least one destructible brick.
  const destructible = bricks.filter((b) => b.type !== 'steel');
  if (destructible.length === 0) errors.push('no destructible bricks');

  // (c) all bricks inside world bounds. World is 1000 wide; brick field must
  // stay well clear of the paddle box (paddle range starts at y~1120 on the
  // shortest layout) — see (f) below for the tighter y=980 rule.
  for (const b of bricks) {
    if (b.x < 0 || b.x + b.w > 1000) errors.push(`brick out of x-bounds at (${b.x},${b.y})`);
    if (b.y < 40 - 1e-6) errors.push(`brick out of y-bounds at (${b.x},${b.y})`);
  }

  // (f) no brick placed below y=980 — stay well clear of the paddle box.
  for (const b of bricks) {
    if (b.y + b.h > 980) errors.push(`brick too low (y=${b.y}) — encroaches on paddle box`);
  }

  // (d) no overlapping bricks (grid-index based, exact since all bricks sit
  // on the grid).
  const seen = new Map();
  for (const b of bricks) {
    const key = `${b.col},${b.row}`;
    if (seen.has(key)) errors.push(`overlap at col ${b.col} row ${b.row}`);
    seen.set(key, b);
  }

  // (b) no fully-steel row spanning all columns.
  const rowGroups = new Map();
  for (const b of bricks) {
    if (!rowGroups.has(b.row)) rowGroups.set(b.row, []);
    rowGroups.get(b.row).push(b);
  }
  for (const [row, list] of rowGroups) {
    if (list.length >= BRICK_COLS && list.every((b) => b.type === 'steel')) {
      errors.push(`fully-steel row at row ${row}`);
    }
  }

  // steel pockets: a destructible brick fully boxed in by steel on all four
  // grid neighbours (interior only — edge bricks always have an open side)
  // — flag as unreachable to keep things honest.
  const byPos = new Map();
  for (const b of bricks) byPos.set(`${b.col},${b.row}`, b);
  for (const b of bricks) {
    if (b.type === 'steel') continue;
    const neighbours = [
      byPos.get(`${b.col - 1},${b.row}`),
      byPos.get(`${b.col + 1},${b.row}`),
      byPos.get(`${b.col},${b.row - 1}`),
      byPos.get(`${b.col},${b.row + 1}`),
    ];
    const isEdge = b.col === 0 || b.col === BRICK_COLS - 1;
    const allSteelOrMissingButEdge = neighbours.every((n) => n && n.type === 'steel');
    if (!isEdge && allSteelOrMissingButEdge) {
      errors.push(`trapped destructible brick at col ${b.col} row ${b.row}`);
    }
  }

  // (e) attic reachability heuristic: there must be at least one column, or
  // one adjacent pair of columns, that a normal ball can fully clear on its
  // way up — i.e. never blocked by an indestructible `steel` brick. Angle/
  // speed/shield/mirror/mover/explosive/magnet bricks are all *eventually*
  // destructible under the right condition, so only `steel` counts as a
  // hard wall for this heuristic.
  if (bricks.length > 0) {
    const maxRow = Math.max(...bricks.map((b) => b.row));
    const grid = Array.from({ length: maxRow + 1 }, () => Array(BRICK_COLS).fill(null));
    for (const b of bricks) grid[b.row][b.col] = b.type;

    let routeFound = false;
    // single-column clear route
    for (let c = 0; c < BRICK_COLS && !routeFound; c++) {
      let clear = true;
      for (let r = 0; r <= maxRow; r++) {
        if (grid[r][c] === 'steel') { clear = false; break; }
      }
      if (clear) routeFound = true;
    }
    // adjacent-pair zigzag route: at every row at least one of the two
    // columns is steel-free, so a path can weave between them.
    if (!routeFound) {
      for (let c = 0; c < BRICK_COLS - 1 && !routeFound; c++) {
        let ok = true;
        for (let r = 0; r <= maxRow; r++) {
          if (grid[r][c] === 'steel' && grid[r][c + 1] === 'steel') { ok = false; break; }
        }
        if (ok) routeFound = true;
      }
    }
    if (!routeFound) errors.push('no attic route: every column pair is blocked by steel');
  }

  return { ok: errors.length === 0, errors };
}

export default { LEVELS, buildLevel, validateLevel, parseMap };
