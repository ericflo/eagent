/* 60-levels.js — namespace window.Levels
   SKYBREAK level definitions: 8 hand-authored ASCII levels + procedural
   "SECTOR n" beyond. Only references window.U.Rng (inside functions).
   FINAL GEOMETRY (single source of truth: Levels.GRID):
     12 cols, x0=6, cell width 74 → col c box x = 6 + c*74 (+2 pad) → w=70.
     Brick rows 52 apart starting y0=180, brick height 48.
       brick pixel box: x = 6 + c*74 + 2, y = 180 + r*52 + 2, w=70, h=48.
     Wide bricks (armored/void) occupy 2 cols: x same, w = 144. In ASCII the
     wide char sits in its LEFT cell and the next cell MUST be '.' (parse
     downgrades to 1-wide otherwise, as a safety valve).
   BrickSpec: { c, r, kind, orient?, hp?, capsule?, hue?, phase?, x,y,w,h }
     c = column 0..11 (LEFT col for wide bricks), r = band row index.
   ASCII legend: . # A B ^ v < > s g r V C M (see LEGEND).
   ES2020, no modules, file:// safe. */

(function () {
  'use strict';
  var L = {};
  if (typeof window !== 'undefined') window.Levels = L;

  L.GRID = { cols: 12, x0: 6, y0: 180, cellW: 74, rowH: 52, brickW: 70, brickH: 48 };

  L.START_LIVES = 3;
  L.MAX_LIVES = 6;
  L.COUNT = 8;

  var WIDE_KINDS = { armored: true, void: true };

  var LEGEND = {
    '#': { kind: 'std' },
    'A': { kind: 'armored', hp: 2 },
    'B': { kind: 'armored', hp: 3 },
    's': { kind: 'sentry' },
    'g': { kind: 'ghostly' },
    'r': { kind: 'regen' },
    'C': { kind: 'std', capsule: true },
    'M': { kind: 'armored', hp: 2, capsule: true },
    'V': { kind: 'void' }
  };
  var WEDGE = { '^': 0, '<': 90, 'v': 180, '>': 270 };

  function hash2(a, b) {
    var h = (a * 73856093) ^ (b * 19349663);
    h = (h ^ (h >>> 13)) * 0x5bd1e995;
    return ((h ^ (h >>> 15)) >>> 0) / 4294967296;
  }

  // parse(asciiRows, hues) -> BrickSpec[]
  L.parse = function (rows, hues) {
    var G = L.GRID, specs = [];
    for (var r = 0; r < rows.length; r++) {
      var row = rows[r];
      var hue = hues ? hues[r % hues.length] : null;
      for (var c = 0; c < row.length && c < G.cols; c++) {
        var ch = row[c];
        if (ch === '.' || ch === ' ') continue;
        var spec;
        if (WEDGE[ch] !== undefined) {
          spec = { c: c, r: r, kind: 'wedge', orient: WEDGE[ch], hue: hue };
        } else if (LEGEND[ch]) {
          spec = { c: c, r: r, kind: LEGEND[ch].kind, hue: ch === 'V' ? null : hue };
          if (LEGEND[ch].hp) spec.hp = LEGEND[ch].hp;
          if (LEGEND[ch].capsule) spec.capsule = true;
        } else {
          continue;
        }
        var wide = !!WIDE_KINDS[spec.kind] && row[c + 1] === '.' && c < G.cols - 1;
        spec.x = G.x0 + c * G.cellW + 2;
        spec.y = G.y0 + r * G.rowH + 2;
        spec.w = wide ? G.cellW * 2 - 4 : G.brickW;
        spec.h = G.brickH;
        if (wide) spec.wide = true;
        if (spec.kind === 'ghostly') spec.phase = hash2(c, r) * Math.PI * 2;
        specs.push(spec);
      }
    }
    return specs;
  };

  // ---------- Authored levels ----------
  // Wide chars (A/B/M/V) always followed by '.' in these rows.
  var HUES = {
    cyan:   [190, 195, 200, 205],
    amber:  [35, 30, 25, 20],
    pink:   [320, 315, 330, 335],
    green:  [140, 150, 155, 160],
    violet: [270, 275, 280, 265],
    blue:   [210, 215, 220, 225],
    red:    [350, 345, 355, 340],
    gold:   [45, 40, 50, 35]
  };

  var LEVELS = [
    { name: 'FIRST FLIGHT', speed: 620, tier: 1, hues: [190, 195, 170, 150], rows: [
      '....C##C....',
      '...######...',
      '..###..###..',
      '.###C##C###.',
      '.##....##...',
      '.#......#...'
    ] },
    { name: 'THE WALL', speed: 640, tier: 1, hues: HUES.amber, rows: [
      'A.A.A.A.A.A.',
      '.####..####.',
      'A.A.A.A.A.A.',
      '..##.CC.##..',
      '.###.##.###.',
      'A..A..A..A..',
      '...#....#...'
    ] },
    { name: 'RAZOR TEETH', speed: 660, tier: 2, hues: HUES.pink, rows: [
      '..s..ss..s..',
      '.>v.vvv.v<..',
      '.<^...^.>...',
      'v..C###C...^',
      '.<#.###.#>..',
      'v...vvv...^.',
      '.<..s..s.>..'
    ] },
    { name: 'GHOST TOWN', speed: 680, tier: 2, hues: HUES.green, rows: [
      '.gg.....gg..',
      '.gg..#..gg..',
      '..gg.#.gg...',
      '.#ggggggg#..',
      '..#.ggg.#...',
      '.C#.ggg.#C..',
      '...#####....'
    ] },
    { name: 'HYDRA', speed: 700, tier: 2, hues: HUES.violet, rows: [
      'A...rrrr..A.',
      '.A..r##r..M.',
      '..A.rrrr.A..',
      '...rrrrr....',
      '..r..CC..r..',
      '.rr..##..rr.',
      'A..r.##.r.A.'
    ] },
    { name: 'THE MAZE', speed: 720, tier: 3, hues: HUES.blue, rows: [
      '..V..##..V..',
      '..V.s..s.V..',
      '.....##.....',
      '.s.V..V..s..',
      '...V.#..V...',
      '.#...s...#..',
      '..V..CC.V...'
    ] },
    { name: 'OVERDRIVE', speed: 740, tier: 3, hues: HUES.red, rows: [
      '.vv..V..vv..',
      '.^^.....^^..',
      'g..sgggs..g.',
      '.gg.CCC.gg..',
      '..ggg#ggg...',
      '.A.ggggg.A..',
      '..s.###.s...'
    ] },
    { name: 'THE VAULT', speed: 760, tier: 3, hues: HUES.gold, rows: [
      'A.A.A.A.A.A.',
      'B.B.C..C.B.B',
      '.A.s....s.A.',
      '..rB....Br..',
      's.r..MM..r.s',
      '..rA.CC.Ar..',
      '...r.##.r...'
    ] }
  ];

  // ---------- Procedural ----------
  function mulberry32(seed) {
    var a = seed >>> 0;
    return function () {
      a = (a + 0x6D2B79F5) >>> 0;
      var t = a;
      t = Math.imul(t ^ (t >>> 15), t | 1);
      t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
      return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
  }
  function Rng(seed) {
    var r = mulberry32(seed);
    return {
      rand: r,
      int: function (a, b) { return a + Math.floor(r() * (b - a + 1)); },
      pick: function (arr) { return arr[Math.floor(r() * arr.length)]; },
      chance: function (p) { return r() < p; },
      range: function (a, b) { return a + r() * (b - a); }
    };
  }

  function genSector(n, seedOffset) {
    var rng = Rng(n * 7919 + seedOffset * 104729);
    var depth = n - L.COUNT;
    var rows = [];
    var ROWS = 8 + Math.min(4, Math.floor(depth / 3));
    var capsuleChance = Math.min(0.14, 0.03 + depth * 0.012);
    function pickKind(rr, depthRows) {
      var rel = 1 - rr / depthRows;
      var bag = ['#', '#', '#'];
      if (depth >= 1) bag.push('g');
      if (depth >= 2) bag.push('A', 'A');
      if (depth >= 3) bag.push('^', 'v', 's');
      if (depth >= 4) bag.push('r', 'B');
      if (depth >= 5) bag.push('s', '>');
      if (rel > 0.75 && depth >= 3) bag.push('s', '^');
      return rng.pick(bag);
    }
    for (var r = 0; r < ROWS; r++) {
      var row = '';
      for (var c = 0; c < 12; c++) {
        var dens = 0.55 + 0.35 * (r / ROWS);
        if (!rng.chance(dens)) { row += '.'; continue; }
        var k = pickKind(r, ROWS);
        if (k === 'A' || k === 'B') {
          if (c < 10 && row[c + 1] === undefined && rng.chance(0.5)) {
            row += (k === 'B' ? 'V.' : k + '.'); c++; // wide pair (left+' ')
          } else {
            row += '#'; // downgrade to std to avoid wide collisions
          }
        } else if (rng.chance(capsuleChance)) {
          row += 'C';
        } else {
          row += k;
        }
      }
      rows.push(row);
    }
    return rows;
  }

  // ---------- isClearable ----------
  // For each band row: pass if there is ANY column not covered by a brick
  // (a gap) OR at least one breakable brick. A band fails only when voids
  // span the full width with no gaps and no breakables.
  L.isClearable = function (specs) {
    if (!specs || !specs.length) return false;
    var bands = {};
    var i, s;
    for (i = 0; i < specs.length; i++) {
      s = specs[i];
      var b = bands[s.r];
      if (!b) b = bands[s.r] = { covered: [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0], breakable: false };
      var wcols = s.wide ? 2 : 1;
      for (var cc = s.c; cc < Math.min(12, s.c + wcols); cc++) b.covered[cc] = 1;
      if (s.kind !== 'void') b.breakable = true;
    }
    for (var r in bands) {
      var band = bands[r];
      var gap = false;
      for (var c2 = 0; c2 < 12; c2++) if (!band.covered[c2]) { gap = true; break; }
      if (!gap && !band.breakable) return false;
    }
    return true;
  };
  // string-level check used by the generator (same rule)
  function bandClearable(rowStr) {
    var covered = 0;
    for (var c = 0; c < rowStr.length; c++) {
      var ch = rowStr[c];
      if (ch === '.' || ch === ' ') return true;
      if (ch === 'V') { covered += 2; c++; continue; }
      return true; // breakable
    }
    return covered < 12;
  }
  function rowsClearable(rows) {
    for (var r = 0; r < rows.length; r++) if (!bandClearable(rows[r])) return false;
    return true;
  }

  // ---------- get ----------
  L.get = function (n) {
    n = Math.max(1, n | 0);
    if (n <= L.COUNT) {
      var def = LEVELS[n - 1];
      return {
        bricks: L.parse(def.rows, def.hues),
        ballSpeed: def.speed,
        name: def.name,
        tier: def.tier
      };
    }
    var spec = null;
    for (var attempt = 0; attempt < 8; attempt++) {
      var rows = genSector(n, attempt);
      if (rowsClearable(rows)) { spec = rows; break; }
    }
    if (!spec) spec = genSector(n, 0);
    var depth = n - L.COUNT;
    var speed = Math.min(800, 760 + depth * 8);
    return {
      bricks: L.parse(spec, HUES.blue),
      ballSpeed: speed,
      name: 'SECTOR ' + n,
      tier: 3
    };
  };

  // expose internals for tests/debug
  L._levels = LEVELS;
  L._rowsClearable = rowsClearable;
})();