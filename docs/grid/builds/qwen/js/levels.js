// levels.js — level construction. Levels 1-8 are handcrafted per SPEC;
// level 9+ uses a seeded procedural generator (mulberry32) that always
// guarantees a physical path to the overdrive zone (center gap and/or ramps)
// and ramps difficulty with level number.
//
// Grid: 20 cols x 8 rows, brick 30x46, gap 6, origin (45, 300).
// Every brick is 1-hit. Keys/locks are paired by tint index.

import { BRICK, FIELD, BRICK_RULES } from './config.js';
import { Brick } from './entities.js';
import { mulberry32, clamp } from './utils.js';

const COLS = BRICK.COLS;
const ROWS = BRICK.ROWS;

// ---------------------------------------------------------------------------
// Small builder helpers

function b(type, col, row, opts = {}) {
  return new Brick(col, row, type, opts);
}

function makePair(bricks, pairIdx, keyType, keyPos, lockPos) {
  // keyPos/lockPos: [col,row] arrays (lock may be multiple cells)
  bricks.push(b(keyType || 'key', keyPos[0], keyPos[1], { pair: pairIdx }));
  for (const p of lockPos) bricks.push(b('lock', p[0], p[1], { pair: pairIdx }));
}

function rampPair(bricks, dir, col, row) {
  // A ramp occupies one grid cell; dir: 1 = '\' (up-left), -1 = '/' (up-right)
  bricks.push(b('ramp', col, row, { rampDir: dir }));
}

function ballSpeedForLevel(level) {
  const bonus = clamp((level - 1) * 0.04, 0, 0.8);
  return Math.round(520 * (1 + bonus));
}

// ---------------------------------------------------------------------------
// Handcrafted levels (SPEC: L1..L8)

function level1() {
  const bricks = [];
  // 2 solid rows + 1 glass row, center 3-col gap, 1 ramp (teaches the zone)
  for (let row = 0; row < 2; row++) {
    for (let col = 0; col < COLS; col++) {
      if (col >= 9 && col <= 11) continue; // center gap
      bricks.push(b('std', col, row));
    }
  }
  for (let col = 0; col < COLS; col++) {
    if (col >= 9 && col <= 11) continue;
    bricks.push(b('glass', col, 2));
  }
  rampPair(bricks, 1, 9, 3); // single ramp left of center as backup path
  return bricks;
}

function level2() {
  const bricks = [];
  // Pyramid: rows of increasing width, centered. 6 rows (0..5)
  const widths = [4, 6, 8, 10, 12, 14];
  for (let row = 0; row < 6; row++) {
    const w = widths[row];
    const start = Math.floor((COLS - w) / 2);
    for (let col = start; col < start + w; col++) {
      const type = row < 2 ? 'std' : (row % 2 === 0 ? 'glass' : 'std');
      bricks.push(b(type, col, row));
    }
  }
  // Angle gates flanking the center gap at the bottom of the pyramid
  bricks.push(b('angle', 8, 5));
  bricks.push(b('angle', 11, 5));
  return bricks;
}

function level3() {
  const bricks = [];
  // Checkerboard across 6 rows + speed bricks + 2 ramps
  for (let row = 0; row < 6; row++) {
    for (let col = 0; col < COLS; col++) {
      if (col >= 9 && col <= 11) continue; // keep center gap open
      if ((row + col) % 2 === 0) bricks.push(b('std', col, row));
      else bricks.push(b('glass', col, row));
    }
  }
  // Speed-gate row at the bottom of the field
  for (let col = 0; col < COLS; col += 3) {
    if (col >= 9 && col <= 11) continue;
    bricks.push(b('speed', col, 6));
  }
  rampPair(bricks, 1, 9, 6);
  rampPair(bricks, -1, 10, 6);
  return bricks;
}

function level4() {
  const bricks = [];
  // Lock/key maze: 2 pairs + magnets. Locked wall with key pockets.
  const wallCols = [3, 4, 15, 16];
  for (const col of wallCols) {
    for (let row = 2; row < 6; row++) bricks.push(b('std', col, row));
  }
  // Pair 0: plain key top-left, locks on the right wall
  makePair(bricks, 0, 'key', [1, 0], [[15, 2], [16, 2]]);
  // Pair 1: angle-gated key bottom-right, locks on left wall
  makePair(bricks, 1, 'angle', [18, 5], [[3, 4], [4, 4]]);
  // Fill interior with glass + a few standards
  for (let col = 6; col <= 13; col++) {
    if (col >= 9 && col <= 11) continue;
    bricks.push(b('glass', col, 1));
    bricks.push(b('std', col, 3));
  }
  // Magnets steering play away from the edges
  bricks.push(b('magnet', 6, 4));
  bricks.push(b('magnet', 13, 4));
  return bricks;
}

function level5() {
  const bricks = [];
  // Movers + bomb clusters
  bricks.push(b('mover', 5, 0));
  bricks.push(b('mover', 14, 0));
  bricks.push(b('mover', 2, 3, { moverDir: -1 }));
  bricks.push(b('mover', 17, 3));
  // Solid frame with center gap
  for (let col = 0; col < COLS; col++) {
    if (col >= 9 && col <= 11) continue;
    bricks.push(b('std', col, 1));
  }
  // Bomb clusters (crosses of minis spawn on break)
  for (const [c, r] of [[4, 4], [7, 5], [12, 5], [15, 4], [6, 2], [13, 2]]) {
    bricks.push(b('bomb', c, r));
  }
  // Glass fill
  for (let col = 6; col <= 13; col++) {
    if (col >= 9 && col <= 11) continue;
    if (col % 2 === 0) bricks.push(b('glass', col, 4));
    else bricks.push(b('glass', col, 3));
  }
  return bricks;
}

function level6() {
  const bricks = [];
  // Mirror corridors + angle gate
  // Two vertical mirror corridors with a center gap
  for (let col = 4; col <= 15; col++) {
    if (col >= 9 && col <= 11) continue;
    if (col % 4 === 0) {
      bricks.push(b('mirror', col, 0));
      bricks.push(b('mirror', col, 3));
    } else {
      bricks.push(b(col % 2 === 0 ? 'std' : 'glass', col, 0));
      bricks.push(b(col % 2 === 1 ? 'std' : 'glass', col, 3));
    }
  }
  // Angle gate row in the middle
  for (let col = 2; col < COLS - 2; col++) {
    if (col >= 9 && col <= 11) continue;
    bricks.push(b('angle', col, 2));
  }
  // Bottom band
  for (let col = 5; col <= 14; col += 2) {
    if (col >= 9 && col <= 11) continue;
    bricks.push(b('std', col, 5));
  }
  return bricks;
}

function level7() {
  const bricks = [];
  // Dense mixed field, speed-gated key guarding a lock pocket
  for (let row = 0; row < 6; row++) {
    for (let col = 0; col < COLS; col++) {
      if (col >= 9 && col <= 11) continue;
      if ((row * 7 + col * 3) % 5 === 0) continue; // airy gaps
      const roll = (row * 13 + col * 7) % 9;
      const type = roll === 0 ? 'speed' : roll === 1 ? 'magnet' : roll === 2 ? 'mover' : roll === 3 ? 'glass' : 'std';
      bricks.push(b(type, col, row));
    }
  }
  // Speed-gated key + locks
  makePair(bricks, 0, 'speed', [1, 0], [[18, 1], [18, 2]]);
  makePair(bricks, 1, 'key', [0, 5], [[19, 4]]);
  return bricks;
}

function level8() {
  const bricks = [];
  // The gauntlet: everything, narrow gaps (1 col each), 3 ramps
  const gapCols = [4, 7, 9, 12, 15]; // narrow 1-col gaps
  const inGap = (c) => gapCols.includes(c);
  for (let row = 0; row < 7; row++) {
    for (let col = 0; col < COLS; col++) {
      if (inGap(col)) continue;
      const roll = (row * 11 + col * 5) % 10;
      const type =
        roll === 0 ? 'bomb' : roll === 1 ? 'angle' : roll === 2 ? 'speed' :
        roll === 3 ? 'mirror' : roll === 4 ? 'magnet' : roll === 5 ? 'mover' :
        roll === 6 ? 'glass' : 'std';
      bricks.push(b(type, col, row));
    }
  }
  // Key/lock pair on top
  makePair(bricks, 0, 'key', [8, 0], [[16, 0]]);
  // 3 ramps forming a slingshot lane
  rampPair(bricks, 1, 4, 7);
  rampPair(bricks, -1, 7, 7);
  rampPair(bricks, -1, 12, 7);
  return bricks;
}

// ---------------------------------------------------------------------------
// Procedural generator for level 9+

function procedural(level) {
  const rng = mulberry32(level * 2654435761 % 4294967296);
  const bricks = [];
  const density = clamp(0.55 + level * 0.02, 0.55, 0.85);
  const rows = clamp(5 + Math.floor((level - 9) / 2), 5, ROWS);
  const moverSpeed = clamp(BRICK_RULES.MOVER_SPEED + (level - 9) * 5, 60, 140);

  // Center gap: 3 cols always open => guaranteed path
  const gap = [9, 10, 11];
  const inGap = (c) => gap.includes(c);

  for (let row = 0; row < rows; row++) {
    for (let col = 0; col < COLS; col++) {
      if (inGap(col)) continue;
      if (rng() > density) continue;
      const roll = rng();
      const specialChance = clamp(0.18 + level * 0.01, 0.18, 0.35);
      let type;
      if (roll < specialChance) {
        const s = rng();
        type = s < 0.25 ? 'glass' : s < 0.4 ? 'angle' : s < 0.55 ? 'speed' :
               s < 0.7 ? 'mover' : s < 0.8 ? 'magnet' : s < 0.9 ? 'bomb' : 'mirror';
      } else {
        type = 'std';
      }
      const opts = type === 'mover' ? { moverSpeed, moverDir: rng() < 0.5 ? 1 : -1 } : {};
      bricks.push(b(type, col, row, opts));
    }
  }

  // Key/lock pairs: 0-4 pairs, scale with level
  const pairs = Math.min(4, Math.floor((level - 9) / 2));
  const used = new Set();
  for (let p = 0; p < pairs; p++) {
    // Pick a key cell not in the gap and not already used
    let kc, kr, kc2, kr2;
    for (let tries = 0; tries < 50; tries++) {
      kc = Math.floor(rng() * COLS);
      kr = Math.floor(rng() * rows);
      kc2 = Math.floor(rng() * COLS);
      kr2 = Math.floor(rng() * rows);
      const k1 = kc + ',' + kr, k2 = kc2 + ',' + kr2;
      if (!inGap(kc) && !inGap(kc2) && !used.has(k1) && !used.has(k2)) break;
    }
    used.add(kc + ',' + kr);
    used.add(kc2 + ',' + kr2);
    const keyType = rng() < 0.5 ? 'key' : (rng() < 0.5 ? 'angle' : 'speed');
    bricks.push(b(keyType, kc, kr, { pair: p }));
    bricks.push(b('lock', kc2, kr2, { pair: p }));
  }

  // Ramps: always 1-2 (extra guaranteed path + slingshots)
  const rampCount = 1 + Math.floor(rng() * 2);
  for (let i = 0; i < rampCount; i++) {
    const col = i === 0 ? 8 : (Math.floor(rng() * COLS));
    const row = rows < ROWS ? rows : rows - 1;
    rampPair(bricks, i % 2 === 0 ? 1 : -1, col, row);
  }
  return bricks;
}

// ---------------------------------------------------------------------------

const HANDCRAFTED = [level1, level2, level3, level4, level5, level6, level7, level8];

export function buildLevel(level) {
  const bricks = level <= 8 ? HANDCRAFTED[level - 1]() : procedural(level);
  // Deduplicate by grid cell (later entries win) — ramps should not be
  // covered by a brick of another type at the same cell.
  const seen = new Map();
  const priority = { ramp: 9, lock: 5, key: 5 };
  for (const brick of bricks) {
    const k = brick.col + ',' + brick.row;
    const prev = seen.get(k);
    if (!prev) { seen.set(k, brick); continue; }
    const pa = priority[brick.type] || 0, pb = priority[prev.type] || 0;
    if (pa > pb) seen.set(k, brick);
  }
  return [...seen.values()];
}

export function levelBallSpeed(level) { return ballSpeedForLevel(level); }
export function levelMoverSpeed(level) {
  return level < 9 ? BRICK_RULES.MOVER_SPEED : clamp(BRICK_RULES.MOVER_SPEED + (level - 9) * 5, 60, 140);
}
