// levels.js — 6 handcrafted levels (ASCII grids) + parser.
// Key: '.' empty, '#' standard, 'F' facet, 'I' impact, 'S' slider,
// 'B' bulwark (shield side toward center), 'V' volatile, 'T' steel,
// 'G' ghost, 'P' prism, 'M' multiplier.
import { WORLD_W } from './engine.js';

export const COLS = 9;
export const CELL_W = WORLD_W / COLS;   // 60
export const CELL_H = 30;
export const GRID_Y0 = 118;             // first brick row (leaves room for frenzy line)

export const LEVELS = [
  {
    name: 'Openings',
    hint: 'Open a gap and get on top — the frenzy line awaits',
    rows: [
      '.##.###..',
      '#########',
      '###...###',
      '#########',
      '.##.#.##.',
      '###...###',
    ],
  },
  {
    name: 'Facets',
    hint: 'Hit facets head-on (within 25°) or they just deflect',
    rows: [
      'T.......T',
      '.FF.F.FF.',
      '.F.....F.',
      'T..MMM..T',
      '.F.....F.',
      '.FF.F.FF.',
      '..T...T..',
    ],
  },
  {
    name: 'Impact',
    hint: 'Impact bricks need speed — smash upward to power through',
    rows: [
      '...TTT...',
      '..T...T..',
      '.T.III.T.',
      '.I.....I.',
      '.T.III.T.',
      '..T.M.T..',
      '...T.T...',
    ],
  },
  {
    name: 'Sliders & Volatiles',
    hint: 'Sliders patrol over volatile chains — one spark and…',
    rows: [
      'S.......S',
      '..V...V..',
      '.VVV.VVV.',
      '..V.M.V..',
      '.VVV.VVV.',
      '..V...V..',
      'S.......S',
    ],
  },
  {
    name: 'Ghosts & Bulwarks',
    hint: 'Ghosts solidify in turns; bulwarks have an exposed side',
    rows: [
      'B..G.G..B',
      '.GG.G.GG.',
      'G..M.M..G',
      '.GG...GG.',
      'B..G.G..B',
      '..G...G..',
    ],
  },
  {
    name: 'The Gauntlet',
    hint: 'Steel tunnels lead to the golden prism trove',
    rows: [
      '.P.P.P.P.',
      'TTT.T.TTT',
      'T..G.G..T',
      'T.F###.FT',
      'T..I.I..T',
      'TTB.M.BTT',
      '..V...V..',
      '.VV.V.VV.',
    ],
  },
];

// Parse rows -> brick descriptors. Returns { bricks, topY }
export function parseLevel(def) {
  const bricks = [];
  let topY = Infinity;
  def.rows.forEach((row, r) => {
    for (let c = 0; c < Math.min(row.length, COLS); c++) {
      const ch = row[c];
      if (ch === '.' || ch === undefined) continue;
      const x = c * CELL_W, y = GRID_Y0 + r * CELL_H;
      const b = {
        col: c, row: r,
        x: x + 2, y: y + 2, w: CELL_W - 4, h: CELL_H - 4,
        type: 'standard', steel: false, alive: true,
        // per-type fields
        facetAngle: 0, shieldSide: null,
        rail: null, railPhase: 0,
        ghostOffset: (c * 0.7 + r * 1.3) % 4.8,
        flash: 0, hintFlash: 0,
      };
      switch (ch) {
        case '#': b.type = 'standard'; break;
        case 'F':
          b.type = 'facet';
          // chevron normal points toward the nearest vertical edge (outward-in mix)
          b.facetAngle = (c % 2 === 0) ? -Math.PI / 5 : Math.PI / 5;
          break;
        case 'I': b.type = 'impact'; break;
        case 'S':
          b.type = 'slider';
          b.rail = railFor(def.rows, r, c);
          break;
        case 'B':
          b.type = 'bulwark';
          b.shieldSide = c < COLS / 2 ? 'right' : 'left'; // shield faces level center
          break;
        case 'V': b.type = 'volatile'; break;
        case 'T': b.type = 'steel'; b.steel = true; break;
        case 'G': b.type = 'ghost'; break;
        case 'P': b.type = 'prism'; break;
        case 'M': b.type = 'multiplier'; break;
        default: b.type = 'standard';
      }
      bricks.push(b);
      if (y < topY) topY = y;
    }
  });
  return { bricks, topY };
}

// A slider patrols between the nearest walls/steel/solid neighbours in its row.
function railFor(rows, r, c) {
  const row = rows[r];
  let x0 = 0, x1 = COLS;
  for (let i = c - 1; i >= 0; i--) {
    if (row[i] === 'T') { x0 = i + 1; break; }
  }
  for (let i = c + 1; i < COLS; i++) {
    if (row[i] === 'T') { x1 = i; break; }
  }
  const speed = 70 + (r % 3) * 22;
  return {
    minX: x0 * CELL_W + 2,
    maxX: x1 * CELL_W - CELL_W + (CELL_W - 4) - 2 + 2, // right edge of last free cell
    dir: (c + r) % 2 === 0 ? 1 : -1,
    speed,
  };
}

export const BRICK_VALUES = {
  standard: 50, facet: 80, impact: 90, slider: 70, bulwark: 110,
  volatile: 60, ghost: 90, prism: 150, multiplier: 250,
};
