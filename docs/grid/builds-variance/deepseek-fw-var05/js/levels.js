// levels.js — level definitions, generation, difficulty & XP curve.

// The game is built around the "topside" fantasy: concentric rings of bricks
// floating high above the paddle, walls sealing the sides, so once a ball
// breaks through it can orbit the ring tops forever.
//
// Level 1 is an approachable "corridor" so players learn single-ball play.
// Levels 2+ get progressively more rings, walls, spinners and metallic
// barriers that FORCE the player to carve a route, then let the ball feast
// on the topside.

const LEVEL_BASE = {
  brickW: 88,
  ringGap: 3,
  minRingRadius: 96,
  ringRadiusStep: 66,
  rowAlign: 0.5,
  cols: 8,
};

const LEVEL_DEFS = [
  // L1 — corridor intro
  {
    name: 'The Corridor',
    subtitle: 'Crack it and let the ball loose',
    rings: 1,
    cols: 10,
    brickW: 74,
    ringGap: 6,
    minRingRadius: 110,
    ringRadiusStep: 72,
    rowAlign: 0.5,
    walls: false,
    spawn: { spinners: 0, metal: 0, gem: 1, bombs: 0, multi: 0 },
    baseSpeed: 7.2,
    comboNeed: 12,
    specialBalls: 0,
  },
  // L2 — two rings, side spinners, gem
  {
    name: 'The Halo',
    subtitle: 'Rings of fire',
    rings: 2,
    cols: 10,
    brickW: 74,
    ringGap: 6,
    minRingRadius: 96,
    ringRadiusStep: 66,
    rowAlign: 0.45,
    walls: true,
    spawn: { spinners: 3, metal: 0, gem: 2, bombs: 0, multi: 0 },
    baseSpeed: 7.8,
    comboNeed: 14,
    specialBalls: 3,
  },
  // L3 — three rings, walls, metal wedge
  {
    name: 'The Bulwark',
    subtitle: 'Carve your way up',
    rings: 3,
    cols: 10,
    brickW: 74,
    ringGap: 5,
    minRingRadius: 84,
    ringRadiusStep: 62,
    rowAlign: 0.4,
    walls: true,
    spawn: { spinners: 3, metal: 2, gem: 2, bombs: 2, multi: 2 },
    baseSpeed: 8.3,
    comboNeed: 16,
    specialBalls: 4,
  },
  // L4 — double-corridor, more spinners
  {
    name: 'The Spiny Crown',
    subtitle: 'They orbit, you weave',
    rings: 3,
    cols: 12,
    brickW: 68,
    ringGap: 4,
    minRingRadius: 82,
    ringRadiusStep: 58,
    rowAlign: 0.5,
    walls: true,
    spawn: { spinners: 5, metal: 2, gem: 3, bombs: 2, multi: 3 },
    baseSpeed: 8.8,
    comboNeed: 18,
    specialBalls: 5,
  },
  // L5 — inner gem ring
  {
    name: 'The Vault',
    subtitle: 'Protect the core',
    rings: 4,
    cols: 10,
    brickW: 72,
    ringGap: 5,
    minRingRadius: 72,
    ringRadiusStep: 56,
    rowAlign: 0.5,
    walls: true,
    spawn: { spinners: 4, metal: 0, gem: 4, bombs: 3, multi: 4 },
    baseSpeed: 9.0,
    comboNeed: 20,
    specialBalls: 6,
  },
];

// Difficulty scaling repeats from L5 onward, slightly cranking params each loop.
function levelParams(rawIndex) {
  const def = LEVEL_DEFS[Math.min(rawIndex, LEVEL_DEFS.length - 1)];
  const loop = Math.max(0, rawIndex - LEVEL_DEFS.length + 1);
  const factor = 1 + loop * 0.14;
  return {
    ...def,
    name: def.name + (loop ? ' II' : loop ? '' : ''),
    // very slight speed increase per loop
    baseSpeed: def.baseSpeed * (1 + loop * 0.04),
    spawn: {
      spinners: Math.round(def.spawn.spinners + loop * 0.6),
      metal: Math.round(def.spawn.metal + loop * 0.4),
      gem: def.spawn.gem,
      bombs: Math.round(def.spawn.bombs + loop * 0.3),
      multi: Math.round(def.spawn.multi + loop * 0.4),
    },
  };
}

// Build a level's bricks given level index (1-based) and canvas dimensions.
function buildLevel(levelIdx, W, H) {
  const def = levelParams(levelIdx - 1);
  const bricks = [];
  const centerX = W / 2;
  const top = H * 0.12;
  const rows = def.rings;
  const cols = def.cols;

  for (let r = 0; r < rows; r++) {
    const ringRadius = def.minRingRadius + r * def.ringRadiusStep;
    const y = top + ringRadius;
    const count = cols;
    const cell = def.brickW + def.ringGap;
    const total = cell * count;
    const startX = centerX - total / 2;
    const brickH = def.brickW * 0.42;
    for (let c = 0; c < count; c++) {
      const x = startX + c * cell;
      const kind = pickRingBrickKind(def, r, c, count);
      if (!kind) continue;
      bricks.push(makeBrick(kind, x, y, def.brickW, brickH, { range: 20, speed: 2.0 + r * 0.3 }));
    }
    // side "wall" columns that seal the ring ends
    if (def.walls) {
      const wy = top + ringRadius - brickH * 0.8;
      const w = 14;
      bricks.push(makeBrick('wall', startX - w - 2, wy, w, brickH * 1.6));
      bricks.push(makeBrick('wall', startX + total + 2, wy, w, brickH * 1.6));
    }
  }

  // ---- TOP-SIDE SHELF: the game's namesake ----
  // A horizontal "ceiling" of breakable bricks near the top. Punch through it
  // and the ball is ON TOP of the brick field, bouncing side to side — the
  // "it goes by itself" moment. Present from LEVEL 1 so the fantasy is
  // reachable immediately. The shelf is deliberately sparse (gaps between
  // bricks = the corridors you carve), and the top is a gentler surface so a
  // broken-through ball stays up there instead of dropping straight back out.
  const shelfY = H * 0.055 + def.brickW * 0.42;    // just under the HUD
  const shelfW = def.brickW * 1.05;
  const brickH = def.brickW * 0.42;                // shelf brick height
  const shelfGap = Math.max(10, def.brickW * 0.35);
  const shelfN = Math.max(6, Math.floor(W * 0.82 / (shelfW + shelfGap)));
  const shelfTotal = shelfN * shelfW + (shelfN - 1) * shelfGap;
  const shelfStart = centerX - shelfTotal / 2;
  for (let i = 0; i < shelfN; i++) {
    // leave 2 deliberate corridors (near 1/4 and 3/4) so there are always
    // approachable gaps; everything else on the shelf is scorable
    const frac = i / (shelfN - 1);
    const isGap = Math.abs(frac - 0.25) < 0.06 || Math.abs(frac - 0.75) < 0.06;
    if (isGap) continue;
    const x = shelfStart + i * (shelfW + shelfGap);
    let kind = 'standard';
    // difficulty carrots on the shelf: gust (speed) and angled (shallow) mix in
    if (def.rings >= 2 && i % 7 === 3) kind = chance(0.5) ? 'gust' : 'standard';
    if (def.rings >= 2 && i % 9 === 5) kind = chance(0.5) ? 'angled' : 'standard';
    if (def.rings >= 3 && i % 11 === 8) kind = chance(0.35) ? 'sturdy' : 'standard';
    bricks.push(makeBrick(kind, x, shelfY, shelfW, brickH * 0.85, { threshold: 6.2 }));
  }
  // rising side walls that CORRAL the ball on top of the shelf (the playground)
  if (def.walls) {
    const wallTop = shelfY - brickH * 3.2;
    const w = 14;
    for (let yy = wallTop; yy < shelfY + brickH; yy += brickH * 0.95) {
      bricks.push(makeBrick('wall', shelfStart - w - 2, yy, w, brickH * 1.1));
      bricks.push(makeBrick('wall', shelfStart + shelfTotal + 2, yy, w, brickH * 1.1));
    }
  }
  return { bricks, def };
}

function pickRingBrickKind(def, row, col, count) {
  const spawn = def.spawn;
  const edge = col === 0 || col === count - 1;
  // Gems live on the inner ring / edges as prizes
  if (spawn.gem && row === 0 && (col === Math.floor(count / 2) || col === Math.floor(count / 2) - 1)) {
    return chance(0.7) ? 'gem' : 'standard';
  }
  // Metals as "gates" on outer rings: place at 1/3 and 2/3 for mid rows
  if (spawn.metal && row >= 1 && (col === Math.floor(count / 3) || col === Math.floor(count * 2 / 3))) {
    return chance(0.55) ? 'metal' : 'standard';
  }
  // SPECIAL DIFFICULT BRICKS (the instructions' headline asks):
  //  - angled: a shallow-hit gate. Lives on the OUTER rings at the 1/4 and 3/4
  //    positions, where a descending ball naturally arrives near-horizontal.
  //  - gust:  a speed gate. Sits on outer rings as "air pockets"; force the
  //    player to hit them fast.
  //  - sturdy: multi-hit (hp2). A rare durable in the middle rings.
  // These kick in from L3 onward so L1-L2 stay approachable.
  if (row >= 1 && !edge) {
    if (col === Math.floor(count / 4) || col === Math.floor(count * 3 / 4)) {
      if (row >= 2 && chance(0.35)) return 'angled';
    }
    if (col === Math.floor(count / 2)) {
      if (row >= 1 && row <= 2 && chance(0.3)) return 'gust';
    }
    if (row >= 2 && (col === Math.floor(count / 3) + 1 || col === Math.floor(count * 2 / 3) - 1)) {
      if (chance(0.22)) return 'sturdy';
    }
  }
  // Spinners replace bricks on the outer rings (avoid dupes)
  if (spawn.spinners && row >= 2 && col % 3 === 0) {
    return chance(0.5) ? 'spinner' : 'standard';
  }
  // Bombs on outer rings occasionally
  if (spawn.bombs && row === rowsLast(def) && col % 4 === 0) {
    return chance(0.5) ? 'bomb' : 'standard';
  }
  if (spawn.multi && (row === 0 || (row === 1 && col % 3 === 1))) {
    return chance(0.5) ? 'multi' : 'standard';
  }
  return 'standard';
}

function rowsLast(def) { return def.rings - 1; }

// XP / difficulty mechanics (kept pure & testable)
const xpToLevel = lvl => Math.round(30 * Math.pow(lvl, 1.35));

const xpAward = eff => Math.round(8 * eff);

window.LevelLib = { buildLevel, levelParams, xpToLevel, xpAward, LEVEL_DEFS, LEVEL_BASE };
