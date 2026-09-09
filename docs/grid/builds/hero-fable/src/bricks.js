// bricks.js — pure brick rules & grid layout. No DOM, no canvas.
//
// Difficulty never comes from hit points. Every special brick asks the player to
// hit it in a particular WAY (steep, fast, from above, while solid...).

import { uid } from './util.js';

/** Grid geometry in logical units (field is 720 wide). 10 columns × up to 14 rows. */
export const GRID = {
  cols: 10,
  maxRows: 14,
  left: 20,
  top: 220, // first row y — everything above is "the attic" (taller attic = longer on-top runs)
  cellW: 68,
  cellH: 34,
  brickW: 62,
  brickH: 28,
};

export const BRICK_TYPES = {
  std: { name: 'Brick', points: 50, breakable: true, color: null },
  angle: { name: 'Prism', points: 150, breakable: true, color: '#7dd3fc' },
  speed: { name: 'Armor', points: 150, breakable: true, color: '#b6bfcf' },
  top: { name: 'Lid', points: 200, breakable: true, color: '#c084fc' },
  ghost: { name: 'Phase', points: 75, breakable: true, color: '#99f6e4' },
  boost: { name: 'Charger', points: 75, breakable: true, color: '#ffe14d' },
  bomb: { name: 'Bomb', points: 100, breakable: true, color: '#ff5d5d' },
  gem: { name: 'Crate', points: 100, breakable: true, color: '#5dff9f' },
  steel: { name: 'Steel', points: 0, breakable: false, color: '#5b6474' },
};

/** ASCII legend used by levels.js. */
export const LEGEND = {
  '.': null,
  '#': 'std',
  A: 'angle',
  S: 'speed',
  T: 'top',
  G: 'ghost',
  B: 'boost',
  X: 'bomb',
  P: 'gem',
  '=': 'steel',
};

/** Standard bricks are coloured by row (warm at the top → cool at the bottom). */
export const ROW_COLORS = [
  '#ff4d6d', '#ff7a45', '#ffb347', '#ffe14d', '#b5f542', '#4dff91',
  '#38e8d8', '#3fb5ff', '#5d7cff', '#8b5cf6', '#c96bff', '#ff5cc8',
  '#ff6b8a', '#ffa07a',
];

// ---- rule constants (referenced by tests and HUD hints) --------------------
export const ANGLE_MIN_DIR_Y = 0.75; // Prism: |dir.y| >= 0.75 (within ~41° of vertical)
export const SPEED_THRESHOLD = 900; // Armor: ball.speed >= 900
export const GHOST_PERIOD = 2; // Phase: 2 s solid, 2 s intangible
export const GHOST_COLUMN_OFFSET = 0.35; // seconds of phase offset per column
export const BOOST_SPEED = 150; // Charger: +150 speed
export const STD_DROP_CHANCE = 0.06; // std bricks drop a capsule 6% of the time
export const BOMB_CHAIN_POINTS = 50; // +50 per brick a bomb takes with it

export function createBrick(type, col, row) {
  const def = BRICK_TYPES[type];
  if (!def) throw new Error(`unknown brick type ${type}`);
  return {
    id: uid(),
    type,
    col,
    row,
    x: GRID.left + col * GRID.cellW + (GRID.cellW - GRID.brickW) / 2,
    y: GRID.top + row * GRID.cellH + (GRID.cellH - GRID.brickH) / 2,
    w: GRID.brickW,
    h: GRID.brickH,
    alive: true,
    color: def.color || ROW_COLORS[row % ROW_COLORS.length],
    flash: 0, // render-only: white flash timer set on deny/hit
  };
}

/** Build a grid from specs [{type,col,row}]. Provides O(1) neighbour lookup. */
export function buildGrid(specs) {
  const map = new Map();
  const bricks = [];
  let rows = 0;
  for (const s of specs) {
    const b = createBrick(s.type, s.col, s.row);
    bricks.push(b);
    map.set(`${s.col},${s.row}`, b);
    rows = Math.max(rows, s.row + 1);
  }
  return {
    cols: GRID.cols,
    rows,
    bricks,
    at: (col, row) => map.get(`${col},${row}`) || null,
  };
}

// ---- ghost phase ----------------------------------------------------------
/** Where a ghost brick is in its 4 s cycle (0..1). Columns ripple by a fixed offset. */
export function ghostCycle(brick, clock) {
  const period = GHOST_PERIOD * 2;
  const t = (clock + brick.col * GHOST_COLUMN_OFFSET) % period;
  return (t < 0 ? t + period : t) / period;
}

/** Ghost bricks are solid for the first half of their cycle. */
export function isGhostSolid(brick, clock) {
  return ghostCycle(brick, clock) < 0.5;
}

/** Can the ball physically collide with this brick right now? */
export function isSolid(brick, clock = 0) {
  if (!brick.alive) return false;
  if (brick.type === 'ghost') return isGhostSolid(brick, clock);
  return true;
}

/**
 * The rulebook. Decide whether a collision breaks the brick.
 *   ball = { speed, dx, dy, ignoresRules }  — (dx,dy) is the unit direction at impact;
 *                                             ignoresRules is true for fire/heavy balls.
 *   hit  = { nx, ny }                        — contact normal pointing out of the brick.
 *   clock                                    — level clock (for ghost phase), optional.
 * Returns { breaks, deny?, pass? }. `deny` names the rule that refused the hit
 * (drives the "tink/clank/thud" feedback); `pass` means the ball goes straight through.
 */
export function canHit(brick, ball, hit, clock) {
  if (!brick.alive) return { breaks: false, pass: true };
  if (brick.type === 'steel') return { breaks: false, deny: 'steel' };
  if (brick.type === 'ghost' && clock !== undefined && !isGhostSolid(brick, clock)) {
    return { breaks: false, pass: true };
  }
  const force = !!ball.ignoresRules;
  switch (brick.type) {
    case 'angle': // Prism: incoming direction must be steep
      if (!force && Math.abs(ball.dy) < ANGLE_MIN_DIR_Y) return { breaks: false, deny: 'angle' };
      break;
    case 'speed': // Armor: ball must be fast
      if (!force && ball.speed < SPEED_THRESHOLD) return { breaks: false, deny: 'speed' };
      break;
    case 'top': {
      // Lid: only the top face, only when the ball is moving downward onto it
      const fromAbove = hit.ny < 0 && ball.dy > 0;
      if (!force && !fromAbove) return { breaks: false, deny: 'top' };
      break;
    }
    default:
      break;
  }
  return { breaks: true };
}

/** The 8 alive neighbours of a brick in the grid. */
export function neighbours(grid, brick) {
  const out = [];
  for (let dr = -1; dr <= 1; dr++) {
    for (let dc = -1; dc <= 1; dc++) {
      if (!dr && !dc) continue;
      const n = grid.at(brick.col + dc, brick.row + dr);
      if (n && n.alive) out.push(n);
    }
  }
  return out;
}

/**
 * Bomb chain: the origin bomb destroys its 8 neighbours; any neighbouring bomb
 * explodes too (breadth-first), so clusters go off together. Steel survives.
 * Returns the list of bricks destroyed, origin first, each exactly once.
 */
export function bombChain(grid, origin) {
  const seen = new Set([origin.id]);
  const result = [origin];
  const queue = [origin];
  while (queue.length) {
    const bomb = queue.shift();
    for (const n of neighbours(grid, bomb)) {
      if (seen.has(n.id) || n.type === 'steel') continue;
      seen.add(n.id);
      result.push(n);
      if (n.type === 'bomb') queue.push(n);
    }
  }
  return result;
}

/** Points for a brick type (0 for steel). */
export const pointsFor = (type) => (BRICK_TYPES[type] ? BRICK_TYPES[type].points : 0);

/** y of the top edge of the highest remaining breakable brick, or Infinity if none. */
export function highestBreakableTop(bricks) {
  let y = Infinity;
  for (const b of bricks) if (b.alive && BRICK_TYPES[b.type].breakable && b.y < y) y = b.y;
  return y;
}

/** How many breakable bricks are left (steel never counts). */
export function breakableRemaining(bricks) {
  let n = 0;
  for (const b of bricks) if (b.alive && BRICK_TYPES[b.type].breakable) n++;
  return n;
}
