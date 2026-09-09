// levels.js — hand-designed layouts (grid DSL) + procedural generator past the last one.
//
// Legend:  . empty   n normal   a Prism▼   h Prism▶   s Armor   g Glass
//          G Phase   t Cap      b Bomb     p Capsule  ^ Launch  # Steel
//
// Every grid is 11 columns wide. Row 0 is the top row of the field; there is always
// open sky above it, plus 28px gutters on both sides.
//
// DESIGN RULES (audited by tools/level-sim.mjs):
//  1. Every level has at least one *readable route overtop*: an open column that lines up
//     with a gap in the top row, an open side lane (col 0 or col 10 empty for the whole
//     grid), a launch pad under a clear column, a bomb that blows the channel open, or a
//     phase row that opens on a timer.
//  2. The reward for being up there is real: Cap bricks (`t`, breakable only from above)
//     and dense top rows live at the top of the field.
//  3. No lone condition bricks stranded in corners — condition kinds are always placed in
//     clusters of 2+ with open space on the side the ball must approach from, so the end
//     of a level never turns into a 60-second hunt.
//  4. One new brick kind per early level (see `newKind` below), which drives the hint
//     system in game.js.

import { makeRng } from './util.js';
import { COLS } from './entities/bricks.js';

export const LEVELS = [
  {
    // L1 — normal bricks only. Center channel + open side lanes: the first overtop is
    // almost free, which is the point.
    name: 'FIRST LIGHT',
    rows: [
      'nnnnn.nnnnn',
      'nnnnn.nnnnn',
      'nnnn...nnnn',
      '.nnnnnnnnn.',
      '.nnn...nnn.',
    ],
  },
  {
    // L2 — launch pads. Hit one from below and you are thrown at the roof.
    name: 'LAUNCH PADS',
    rows: [
      '.nnnnnnnnn.',
      '.nnn.n.nnn.',
      '.nnnnnnnnn.',
      '.nnpn.npnn.',
      '..^.....^..',
    ],
  },
  {
    // L3 — prisms. ▼ on top (steep hits only: come up at them), ▶ in the middle band
    // (shallow hits only: skim along an open row).
    name: 'PRISM GATE',
    rows: [
      'aaa.....aaa',
      'nnnn...nnnn',
      'nnnnn.nnnnn',
      '.hh.nnn.hh.',
      '.nnpnnnpnn.',
      '...^...^...',
    ],
  },
  {
    // L4 — caps. The whole top row can only be broken from above, so the level is a
    // straight lesson in "get up there".
    name: 'THE CAP',
    rows: [
      'ttttt.ttttt',
      'nnnnn.nnnnn',
      'nnn.nnn.nnn',
      '.nnpnnnpnn.',
      '..^.....^..',
    ],
  },
  {
    // L5 — glass (slow ball only) up top, armor (fast ball only) low, so you have to
    // switch gears with the paddle: smash the armor, then coast into the glass.
    name: 'GLASS & ARMOR',
    rows: [
      'ggggg.ggggg',
      'nnnnn.nnnnn',
      '.ss.....ss.',
      '.nnpnnnpnn.',
      '..^.....^..',
    ],
  },
  {
    // L6 — phase bricks: a timing gate across the only road to the roof.
    name: 'PHASE GATE',
    rows: [
      'nnnnn.nnnnn',
      'GGGGG.GGGGG',
      '.nnn...nnn.',
      '.nnpn.npnn.',
      '..^.....^..',
    ],
  },
  {
    // L7 — bombs. The middle bomb blows the channel open in one hit.
    name: 'BOMB ALLEY',
    rows: [
      'nnnnn.nnnnn',
      'nnnnnnnnnnn',
      'nnnnnbnnnnn',
      '.nn.nnn.nn.',
      '.nnpnnnpnn.',
      '..^.....^..',
    ],
  },
  {
    // L8 — steel. Two steel columns turn the outer gutters into express lanes.
    name: 'STEEL LANES',
    rows: [
      'nnnn.n.nnnn',
      '.#nnnnnnn#.',
      '.#nnnnnnn#.',
      '.#nn.n.nn#.',
      '.nnpnnnpnn.',
      '.nn.....nn.',
      '..^.....^..',
    ],
  },
  {
    // L9 — caps over prisms; two symmetric shafts, launch pads at the base of each.
    name: 'CATHEDRAL',
    rows: [
      'ttt.ttt.ttt',
      'aa..nnn..aa',
      'nnn.nnn.nnn',
      'nnnn.n.nnnn',
      '.hh.ppp.hh.',
      '.nn.....nn.',
      '..^.....^..',
    ],
  },
  {
    // L10 — a phase bridge under a capped roof; glass wings you must approach calmly.
    name: 'SKY BRIDGE',
    rows: [
      'tttt.n.tttt',
      '.....G.....',
      'nnnnnnnnnnn',
      'gg.nnnnn.gg',
      '.nnpnnnpnn.',
      '.nn.....nn.',
      '..^.....^..',
    ],
  },
  {
    // L11 — armor spine down the middle: smash through it or take the side lanes.
    name: 'IRON SPINE',
    rows: [
      'nnnn.n.nnnn',
      '.nnnnsnnnn.',
      '.nn.nsn.nn.',
      '.nnnnsnnnn.',
      '.nnpn.npnn.',
      '..^.....^..',
    ],
  },
  {
    // L12 — everything at once, still with two clean shafts and a bomb release.
    name: 'PRESSURE',
    rows: [
      'ttt.ttt.ttt',
      'GGG.nnn.GGG',
      'nnn.nbn.nnn',
      'nnnnnnnnnnn',
      '.aa.ppp.aa.',
      '.gg.....gg.',
      '.#n.....n#.',
      '..^.....^..',
    ],
  },
  {
    // L13 — the roof is caps and the only way in is the phase window.
    name: 'ROOF ACCESS',
    rows: [
      'ttttttttttt',
      'GG.GGGGG.GG',
      'nn.nnnnn.nn',
      'nnnnnnnnnnn',
      '.hh.ppp.hh.',
      '.nn.....nn.',
      '...^...^...',
    ],
  },
  {
    // L14 — wide-open sky lanes, dense cap roof, bombs to keep the tail short.
    name: 'THE GAUNTLET',
    rows: [
      'ttt.ttt.ttt',
      'aaa.sss.aaa',
      'nnn.nnn.nnn',
      'nnnbnnnbnnn',
      '.gg.ppp.gg.',
      '.nnn...nnn.',
      '..^.....^..',
    ],
  },
];

// The brick kind each early level introduces (used for the level-intro banner).
const KIND_ORDER = ['normal', 'boost', 'angle', 'topOnly', 'slow', 'ghost', 'bomb', 'steel'];

const KIND_POOL_EASY = ['n', 'n', 'n', 'n', 'n', 'n', 'p', 'b'];
const KIND_POOL_MID = ['n', 'n', 'n', 'n', 'a', 'h', 'g', 'p', 'b'];
const KIND_POOL_HARD = ['n', 'n', 'n', 'a', 'h', 's', 'g', 'G', 'b', 'p', '#'];

const BREAKABLE_RE = /[nahsgGtbp^]/;

/**
 * Procedural level for index > LEVELS.length. Deterministic per level number.
 *
 * Guarantees (asserted by tools/level-sim.mjs):
 *  - a full-height open lane (side, center or offset) that lines up with a gap in row 0,
 *  - launch pads on the bottom row under open columns,
 *  - caps only in the top two rows,
 *  - at least one capsule,
 *  - no breakable brick fully enclosed by steel,
 *  - a bounded brick count so late levels don't become endurance tests.
 */
export function generateLevel(levelNumber) {
  const rng = makeRng(0x9e3779b9 ^ (levelNumber * 2654435761));
  const diff = Math.min(1, (levelNumber - LEVELS.length) / 16);
  const rowCount = Math.min(9, 5 + Math.floor(rng() * 2) + Math.floor(diff * 3));
  const pool = diff < 0.2 ? KIND_POOL_EASY : diff < 0.55 ? KIND_POOL_MID : KIND_POOL_HARD;

  // Symmetry mode gives each generated level a readable shape.
  const symMode = ['mirror', 'mirror', 'mirror', 'none', 'alternate'][(rng() * 5) | 0];
  // Lane mode decides where the route overtop is.
  const laneMode = ['center', 'side', 'twin', 'offset'][(rng() * 4) | 0];
  const half = Math.ceil(COLS / 2);
  const density = 0.6 + diff * 0.22;

  const grid = [];
  for (let r = 0; r < rowCount; r++) {
    const cells = new Array(COLS).fill('.');
    const span = symMode === 'mirror' ? half : COLS;
    for (let c = 0; c < span; c++) {
      // Row-shape patterns keep generated levels from being uniform noise.
      let want = rng() < density;
      if (symMode === 'alternate' && (r + c) % 3 === 2) want = false;
      if (!want) continue;
      let ch = pool[(rng() * pool.length) | 0];
      if (ch === '#' && (rng() > 0.3 || r < 1)) ch = 'n';
      if (ch === 't' && r > 1) ch = 'n';
      cells[c] = ch;
      if (symMode === 'mirror') cells[COLS - 1 - c] = ch;
    }
    // A cap roof shows up on harder levels: real reward for going overtop.
    if (r === 0 && diff > 0.3 && rng() < 0.5) {
      for (let c = 0; c < COLS; c++) if (cells[c] === 'n' && rng() < 0.7) cells[c] = 't';
    }
    grid.push(cells);
  }

  // ---- guaranteed route(s) overtop -------------------------------------------------
  const lanes = [];
  if (laneMode === 'center') lanes.push(half - 1);
  else if (laneMode === 'side') lanes.push(rng() < 0.5 ? 0 : COLS - 1);
  else if (laneMode === 'twin') lanes.push(2, COLS - 3);
  else lanes.push(1 + ((rng() * (COLS - 2)) | 0));
  for (const lane of lanes) {
    for (let r = 0; r < rowCount; r++) grid[r][lane] = '.';
  }
  // Side lanes: at least one outer column is always climbable.
  if (laneMode !== 'side' && rng() < 0.5) {
    const col = rng() < 0.5 ? 0 : COLS - 1;
    for (let r = 0; r < rowCount; r++) grid[r][col] = '.';
    lanes.push(col);
  }

  // ---- launch pads under open columns ----------------------------------------------
  const bottom = new Array(COLS).fill('.');
  const padCols = [2, COLS - 3];
  for (const c of padCols) bottom[c] = '^';
  grid.push(bottom);

  // ---- no breakable brick fully enclosed by steel -----------------------------------
  const isSteel = (r, c) => r >= 0 && r < grid.length && c >= 0 && c < COLS && grid[r][c] === '#';
  for (let r = 0; r < grid.length; r++) {
    for (let c = 0; c < COLS; c++) {
      const ch = grid[r][c];
      if (ch === '.' || ch === '#') continue;
      const blocked = [[r - 1, c], [r + 1, c], [r, c - 1], [r, c + 1]].every(([rr2, cc]) => isSteel(rr2, cc));
      if (blocked) grid[Math.max(0, r - 1)][c] = 'n';
    }
  }
  // Steel never seals the top row (that would kill every route overtop).
  for (let c = 0; c < COLS; c++) if (grid[0][c] === '#') grid[0][c] = 'n';

  // ---- capsules & brick budget ------------------------------------------------------
  let capsules = 0;
  let breakables = 0;
  for (const row of grid) {
    for (const ch of row) {
      if (ch === 'p') capsules++;
      if (ch !== '.' && ch !== '#') breakables++;
    }
  }
  if (capsules === 0) {
    const r = (rng() * (grid.length - 1)) | 0;
    const c = (rng() * COLS) | 0;
    if (grid[r][c] !== '.') grid[r][c] = 'p';
    else grid[Math.max(0, r - 1)][c] = 'p';
  }
  // Hard budget: past ~72 breakables the tail gets tedious for no extra fun.
  const BUDGET = 72;
  if (breakables > BUDGET) {
    let over = breakables - BUDGET;
    for (let r = grid.length - 2; r >= 0 && over > 0; r--) {
      for (let c = 0; c < COLS && over > 0; c++) {
        if (grid[r][c] !== '.' && grid[r][c] !== '#' && grid[r][c] !== 'p' && rng() < 0.5) {
          grid[r][c] = '.';
          over--;
        }
      }
    }
  }

  const rows = grid.map((r) => r.join(''));
  if (!rows.some((r) => BREAKABLE_RE.test(r))) {
    return { name: `SECTOR ${levelNumber}`, rows: ['nnnnn.nnnnn', 'nnnnn.nnnnn', '.nnnnnnnnn.'] };
  }
  return { name: `SECTOR ${levelNumber}`, rows, generated: true };
}

/** 1-based level number -> {name, rows}. */
export function getLevel(levelNumber) {
  if (levelNumber <= LEVELS.length) return LEVELS[levelNumber - 1];
  return generateLevel(levelNumber);
}

export const HAND_LEVEL_COUNT = LEVELS.length;
export const LEVEL_KIND_ORDER = KIND_ORDER;
