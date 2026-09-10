/* ============================================================
   TOPSIDE — levels.js
   Brick type definitions and hand-tuned level layouts.
   Each level = list of rows, characters map to brick defs.
   ============================================================ */
'use strict';

/* ---------- difficulty brick types (the "not boring" ones) ----------
   Kind meanings:
     tough   : needs `hp` hits (used sparingly)
     shield  : invulnerable until its shieldmate brick is destroyed
     mirror  : reflects ball hard (doesn't break) -> fun surface
     angle   : only take a hit within its angle tolerance (deflect otherwise)
     speed   : only take a hit above a speed threshold (graze through otherwise)
     time    : only open during its phase window; harmless fog otherwise
     bomb    : explodes nearby bricks when hit once
     glass   : brittle; chain reactions hit it with lower restitution
   ------------------------------------------------------------------ */
const BRICK_DEFS = {
  p: { kind: 'normal',  hp: 1, pts: 25, c: 'p' },
  o: { kind: 'normal',  hp: 1, pts: 30, c: 'o' },
  y: { kind: 'normal',  hp: 1, pts: 35, c: 'y' },
  g: { kind: 'normal',  hp: 1, pts: 40, c: 'g' },
  c: { kind: 'normal',  hp: 1, pts: 45, c: 'c' },
  b: { kind: 'normal',  hp: 1, pts: 50, c: 'b' },
  v: { kind: 'normal',  hp: 1, pts: 60, c: 'v' },
  w: { kind: 'normal',  hp: 1, pts: 75, c: 'w' },

  t: { kind: 'tough',   hp: 2, pts: 60, c: 'o' },      // two hits
  T: { kind: 'tough',   hp: 3, pts: 110, c: 'w' },     // three hits

  s: { kind: 'shield',  hp: 1, pts: 90, c: 'c' },      // shielded until mate dies
  S: { kind: 'shield',  hp: 1, pts: 90, c: 'c', mate: 's' },

  m: { kind: 'mirror',  hp: 1e9, pts: 0, c: 'v' },     // unbreakable, hard reflect

  a: { kind: 'angle',   hp: 1, pts: 85, c: 'g', tol: 0.55 },   // must hit near-horizontal
  A: { kind: 'angle',   hp: 1, pts: 110, c: 'g', tol: 0.42 },

  e: { kind: 'speed',   hp: 1, pts: 85, c: 'y', need: 560 },   // must hit fast
  E: { kind: 'speed',   hp: 1, pts: 110, c: 'y', need: 720 },

  h: { kind: 'time',    hp: 1, pts: 80, c: 'b', phase: [0.30, 0.62] },  // opens in phase window
  H: { kind: 'time',    hp: 1, pts: 95, c: 'b', phase: [0.62, 0.95] },

  x: { kind: 'bomb',    hp: 1, pts: 70, c: 'p' },      // explodes when touched
  X: { kind: 'bomb',    hp: 1, pts: 120, c: 'w', big: true },

  z: { kind: 'glass',   hp: 1, pts: 20, c: 'c' },      // brittle: weak restit, chain-fragment
};

/* palette helper for parsing */
const PAL_BY_CHAR = {};

/* ---------- level data: rows of characters ---------- */
const LEVELS = [
  /* 1 — warmup */
  {
    name: 'Warm-Up', flavor: 'Get the feel. Then get ABOVE.',
    rows: [
      'wwwwwww',
      'bbbbbbb',
      'ccccccc',
      'ooooooo',
      'ppppppp'
    ]
  },
  /* 2 — the angle twins */
  {
    name: 'Angles', flavor: 'Hit them sideways. Or don\u2019t.',
    rows: [
      '.wwwww.',
      'aaaaaaa',
      'ccccccc',
      'ppppppp',
      'ooooo.o'
    ]
  },
  /* 3 — speed gate */
  {
    name: 'Velocity', flavor: 'You gotta be MOVING.',
    rows: [
      '.h.....',
      'eEEEEe.',
      'ppppppp',
      'ggggggg',
      'yyyyyyy'
    ]
  },
  /* 4 — shielded heart */
  {
    name: 'The Heart', flavor: 'Kill the guards before the core.',
    rows: [
      '..SSS..',
      '.sssss.',
      'ccccccc',
      'mmmmmmm',
      'ooooo.o'
    ]
  },
  /* 5 — time gates */
  {
    name: 'Clockwork', flavor: 'The wall opens only for a moment.',
    rows: [
      '.hH.hH.',
      'h. HH h'.replace(/ /g, '.'),
      'c.ac.a.',
      'ooooooo'
    ]
  },
  /* 6 — the gauntlet */
  {
    name: 'Gauntlet', flavor: 'Spin it fast. Rip it open.',
    rows: [
      '.wwwww.',
      'c...h..',
      'e.aea.e',
      'yey.v.y',
      'm.....m',
      'oxoxoxo'
    ]
  },
  /* 7 — bomb garden */
  {
    name: 'Bomb Garden', flavor: 'Chain reactions are the whole point.',
    rows: [
      '.xxxxx.',
      'zzzzzzz',
      'x.g.g.x',
      'zzzzzzz',
      'x.....x'
    ]
  },
  /* 8 — the arc */
  {
    name: 'The Arch', flavor: 'The penultimate wall. Break it open.',
    rows: [
      '.......',
      '.ooooo.',
      'oTTT.To',
      'oAAAAAo',
      'ggggggg',
      'ppppppp'
    ]
  }
];

/* post-process into parseable defs */
(function compileLevels() {
  for (const lv of LEVELS) {
    lv.rows = lv.rows.map(row => row.replace(/ /g, '.'));
  }
})();

/* default brick palette per level index (cycled) */
function brickColorForLevel(levelIdx) {
  const schemes = ['p', 'o', 'y', 'g', 'c', 'b', 'v'];
  return schemes[levelIdx % schemes.length];
}

if (typeof module !== 'undefined' && module.exports) {
  module.exports = { BRICK_DEFS, LEVELS };
}
