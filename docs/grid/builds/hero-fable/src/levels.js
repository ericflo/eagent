// levels.js — 8 authored levels as ASCII maps. Pure: parses into brick specs.
//
// Legend: . empty  # std  A angle(Prism)  S speed(Armor)  T top(Lid)  G ghost(Phase)
//         B boost(Charger)  X bomb  P gem(crate)  = steel
// 10 columns × up to 14 rows. Every level is built around one idea: find the
// way UP so the ball can live in the attic.

import { LEGEND, GRID } from './bricks.js';

export const LEVELS = [
  {
    name: 'Warm-up',
    hint: 'Phase bricks blink in and out. Slip up the lane to reach the attic.',
    // A 2-wide phase lane: wide enough to thread with a near-centre paddle hit.
    map: [
      '####GG####',
      '####GG####',
      '####GG####',
      '#P##GG##P#',
      '####GG####',
      '####GG####',
    ],
  },
  {
    name: 'Lid',
    hint: 'Lids only break from above. Get on top first.',
    // The gap in the lid sits over a phase lane; dig three rows to reach it.
    map: [
      'TTTT..TTTT',
      '##.#GG#.##',
      '#.##GG##.#',
      '##.####.##',
      '###.##.###',
      '.###PP###.',
    ],
  },
  {
    name: 'Prism',
    hint: 'Prisms need steep shots. Bombs up top blow a hole.',
    // Starter holes under the prism columns invite the steep shot up to the bombs.
    map: [
      '###X##X###',
      '#A#A#A#A#A',
      '#A#A#A#A#A',
      '#A#A#A#A#A',
      'A#A#A#A#A#',
      '#.#.##.#.#',
      '.P......P.',
    ],
  },
  {
    name: 'Armor',
    hint: 'Armor needs speed. Flick the paddle UP on the hit, or grab chargers.',
    map: [
      '##########',
      '##########',
      'SSSSSSSSSS',
      'SSSSSSSSSS',
      '..B....B..',
      '#B######B#',
      '..B....B..',
      '####PP####',
    ],
  },
  {
    name: 'Chimney',
    hint: 'Steel funnels into a chimney. The ghost plug at the top blinks.',
    map: [
      '====GG====',
      '#==#..#==#',
      '##=#..#=##',
      '###=..=###',
      '####..####',
      '#B##..##B#',
      '##########',
      '##P####P##',
    ],
  },
  {
    name: 'Two attics',
    hint: 'A steel shelf splits the sky. Two places to bounce on top.',
    map: [
      '###T..T###',
      '##########',
      '#G######G#',
      '..........',
      '====..====',
      '..........',
      '##########',
      '#A#A##A#A#',
      '##########',
      '..P....P..',
    ],
  },
  {
    name: 'Minefield',
    hint: 'Bombs everywhere. Let them open the lid for you.',
    map: [
      'TTTTTTTTTT',
      '#X#A#A#X##',
      '#A#X##X#A#',
      '##########',
      'X#A####A#X',
      '#####X####',
      '##P####P##',
    ],
  },
  {
    name: 'Cathedral',
    hint: 'Everything at once. Break through, get up, stay up.',
    // The lids seal the nave; the only ways up are the phase plugs in the outer
    // aisles (rows of std bricks below them) — lids then break from above.
    map: [
      '..TTTTTT..',
      '.T#GAAG#T.',
      'G##S##S##G',
      '#=#X##X#=#',
      '#=A####A=#',
      '#=B#PP#B=#',
      '#=######=#',
      '##SS##SS##',
      'X###GG###X',
      '##########',
    ],
  },
];

/**
 * Parse a level's ASCII map into brick specs [{type,col,row}].
 * Throws on malformed maps so a typo shows up in tests, not in play.
 */
export function parseLevel(level) {
  const rows = level.map;
  if (!Array.isArray(rows) || rows.length === 0 || rows.length > GRID.maxRows) {
    throw new Error(`level "${level.name}": needs 1..${GRID.maxRows} rows`);
  }
  const specs = [];
  rows.forEach((line, row) => {
    if (line.length !== GRID.cols) {
      throw new Error(`level "${level.name}" row ${row}: expected ${GRID.cols} chars, got ${line.length}`);
    }
    for (let col = 0; col < line.length; col++) {
      const ch = line[col];
      if (!(ch in LEGEND)) throw new Error(`level "${level.name}" row ${row} col ${col}: unknown char '${ch}'`);
      const type = LEGEND[ch];
      if (type) specs.push({ type, col, row });
    }
  });
  return specs;
}

/** Level index wraps (endless mode). */
export const levelAt = (index) => LEVELS[((index % LEVELS.length) + LEVELS.length) % LEVELS.length];

/** Ball speed scale for the n-th loop through the authored levels: +8% per loop. */
export const loopSpeedScale = (loop) => Math.pow(1.08, Math.max(0, loop));
