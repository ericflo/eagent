/* OVERDRIVE — js/levels.js
 * window.Levels: 14 hand-designed levels (12 cols) + infinite "OVERDRIVE
 * FOREVER" generator. Level art symbols: '.' none, N normal, A angle,
 * S speed, G golden, T steel, B bomb.
 * Classic script.
 */
(function () {
  'use strict';

  // ---- the 14 hand-designed levels ------------------------------------------
  var levels = [
    {
      name: 'FIRST LIGHT', sub: 'warm up your paddle',
      rows: [
        '....NNNN....',
        '...NNNNNN...',
        '..NNNNNNNN..',
        '.NNNNNNNNNN.',
        'NNNNNNNNNNNN',
        '.NNNNAANNNN.',
        '..NNNNNNNN..',
        '...NNNNNN...',
        '....NNNN....'
      ]
    },
    {
      name: 'NEON CORNER', sub: 'golden bricks give capsules',
      rows: [
        '..NNNNNNNN..',
        '.NNNNNNNNNN.',
        '.NGNNNNNNGN.',
        'NNNNNNNNNNNN',
        '.NNNGGGGNNN.',
        '..NNNNNNNN..',
        '...NNNNNN...',
        '....NNNN....'
      ]
    },
    {
      name: 'SPEED CHECK', sub: 'faster = broken',
      rows: [
        '...NNNNNN...',
        '..NNNNNNNN..',
        '..NNSSSSNN..',
        '.NNSSSSSSNN.',
        '.NSSSSSSSSN.',
        '.NNSSSSSSNN.',
        '..NNSSSSNN..',
        '..NNNNNNNN..',
        '...NNNNNN...'
      ]
    },
    {
      name: 'HOUSE OF ANGELS', sub: 'steep hits only',
      rows: [
        '...AAAAAA...',
        '..AAAAAAAA..',
        '.AAAAAAAAAA.',
        'AAAANNNNAAAA',
        'AAANNNNNNAAA',
        '.AANNNNNNAA.',
        '..AAANNAAA..',
        '...AAAAAA...'
      ]
    },
    {
      name: 'EL DORADO', sub: 'gold runs the show',
      rows: [
        '..NNNNNNNN..',
        '.NGGNNNNGGN.',
        'NNGGNNNNGGNN',
        'NNGGGGGGGGNN',
        '.NNGGNNGGNN.',
        '..NNGGGGNN..',
        '...NNNNNN...'
      ]
    },
    {
      name: 'STEEL GATES', sub: 'lasers or fire break steel',
      rows: [
        '...NNNNNN...',
        '..NNTTNNNN..',
        '.NNNTTNNNNN.',
        'NNNNNNNNNNNN',
        'NNNANNNNNNNA',
        '.NNNNNNNNNN.',
        '..NNNNNNNN..',
        '...NNNNNN...'
      ]
    },
    {
      name: 'BOMB SQUAD', sub: 'chain the blasts',
      rows: [
        '..NNNNNNNN..',
        '.NNNNNNNNNN.',
        'NNBNNNNNNNBN',
        'NNNNNNBNNNNN',
        'NNBNNNNNBNNN',
        '.NNNNNNNNNN.',
        '..NNNNNNNN..'
      ]
    },
    {
      name: 'MIXED MART', sub: 'every trick in the book',
      rows: [
        '...NNNNNN...',
        '..NSSSNNNN..',
        '.NSASANNSGN.',
        'NNNAAANNNBNN',
        '.NNNGGGNNAN.',
        '..NSANNNSN..',
        '...NNNNNN...'
      ]
    },
    {
      name: 'PYRAMID', sub: 'apex of steel',
      rows: [
        '.....TT.....',
        '....NNNN....',
        '...ANNNNA...',
        '..NNNNNNNN..',
        '.NNNNNNNNNN.',
        'NNNSNNNNNSNN',
        '.NNNNNNNNNN.',
        '..NNNNNNNN..'
      ]
    },
    {
      name: 'SPEEDWAY', sub: 'full throttle',
      rows: [
        '...SSSSSS...',
        '..SSSSSSSS..',
        '.NSSSSSSSSN.',
        'NGGSSSSSSGGN',
        '.NSSSSSSSSN.',
        '..SSSSSSSS..',
        '...SSSSSS...'
      ]
    },
    {
      name: 'DIAMOND', sub: 'core of steel',
      rows: [
        '.....NN.....',
        '....NAAN....',
        '...NAAAAN...',
        '..NNAAAANN..',
        '.NNAATTAANN.',
        '..NNAAAANN..',
        '...NAAAAN...',
        '....NAAN....',
        '.....NN.....'
      ]
    },
    {
      name: 'IRON CURTAIN', sub: 'steel flanks',
      rows: [
        'TNNNNNNNNNNT',
        'TNNNNAANNNNT',
        'TNNNNNNNNNNT',
        'NNNNNGGNNNNN',
        'NNNNNGGNNNNN',
        'NNNNAAANNNNN',
        'NNNNNNNNNNNN'
      ]
    },
    {
      name: 'LAST STAND', sub: 'one wall of trouble',
      rows: [
        '..ASSSSSSA..',
        '.ASSGSSGSSA.',
        'TASSSSSSSSAT',
        'NNNNNNNNNNNN',
        'NNAAAAAAAANN',
        '.NNAAAAAANN.',
        '..NNNNNNNN..',
        '...NNNNNN...'
      ]
    },
    {
      name: 'THE OVERDRIVE', sub: 'earn the top',
      rows: [
        '.AGSGSSSGSA.',
        'TNNNGGGNNNNT',
        '.NNGAAAGNNN.',
        'NNNSGGGSNNNN',
        'NNNBAAABNNNN',
        '.NNNNNNNNNN.',
        '..NNNNNNNN..',
        '...NNNNNN...'
      ]
    }
  ];

  // ---- OVERDRIVE FOREVER generator -------------------------------------------
  var foreverNames = [
    'NEON NIGHTMARE', 'CHROME CANYON', 'LASER LOTUS', 'TURBO TEMPLE',
    'SOLAR STORM', 'MIRROR MAZE', 'PULSE PALACE', 'VOLTAGE VALE',
    'HYPER HELIX', 'NOVA NEXUS', 'QUANTUM QUAY', 'RADIO RIDGE',
    'SILICON SUNSET', 'ELECTRIC EDEN', 'ZERO ZONE', 'PLASMA PLAZA',
    'GALAXY GUTS', 'WIRE WONDERLAND', 'PHOTON FARM', 'SIGNAL SWAMP'
  ];
  var foreverAdjs = ['OVERDRIVE', 'FOREVER', 'ENDLESS', 'TURBO', 'HYPER', 'NOVA'];

  function pick(arr) { return arr[Math.floor(Math.random() * arr.length)]; }

  /**
   * Generate a forever level. diff = levels cleared beyond the 14 (0-based).
   * Rising difficulty: more rows, higher special share, tighter packing.
   */
  function generate(levelIndex) {
    var diff = Math.max(0, levelIndex - 14);
    var rows = 9 + Math.min(3, Math.floor(diff / 2));          // 9..12 rows
    var rowHoles = Math.max(0, 2 - Math.floor(diff / 6));      // tighter gaps
    var tMax = Math.min(3, 1 + Math.floor(diff / 4));          // steel 1..3

    // weights — specials grow with diff
    var baseShare = Math.min(0.50, 0.18 + diff * 0.035);
    var shareS = baseShare * 0.40, shareA = baseShare * 0.30, shareG = Math.min(0.10, baseShare * 0.18);
    var steelPlaced = 0, nCount = 0, cells = [];

    // weight tuning by share
    var wS = Math.max(1, Math.round(shareS * 60)), wA = Math.max(1, Math.round(shareA * 60));
    var wG = Math.max(1, Math.round(shareG * 60));
    var wNames = [];
    for (var i = 0; i < 60 - wS - wA - wG; i++) wNames.push('N');
    for (i = 0; i < wS; i++) wNames.push('S');
    for (i = 0; i < wA; i++) wNames.push('A');
    for (i = 0; i < wG; i++) wNames.push('G');

    // cluster: bias each row toward the previously chosen special (nice columns)
    var lastBias = 'N';
    for (var r = 0; r < rows; r++) {
      var line = '';
      var holes = 0;
      for (var c = 0; c < 12; c++) {
        // random hole (not on the last row, not too many)
        if (holes < rowHoles && c > 0 && c < 11 && Math.random() < 0.10) {
          line += '.'; holes++; continue;
        }
        var ch = 'N';
        if (steelPlaced < tMax && Math.random() < 0.055) {
          ch = 'T'; steelPlaced++;
        } else {
          var rnd = Math.random();
          if (rnd < 0.14 && lastBias !== 'N' && lastBias !== 'A') { ch = lastBias; }
          else {
            ch = wNames[Math.floor(Math.random() * wNames.length)];
          }
          // avoid a full-width wall of S early on
          if (ch === 'S' && c > 6 && Math.random() < 0.55 && lastBias !== 'S') ch = 'N';
          if (ch === 'N' && rnd > 0.9 && diff > 1) ch = 'B';   // occasional bomb
          if (ch !== 'N') lastBias = ch;
        }
        if (ch === 'N') nCount++;
        line += ch;
      }
      cells.push(line);
    }
    // guarantee at least a handful of normal bricks
    if (nCount < 10) {
      var rr = Math.floor(Math.random() * cells.length);
      var ll = cells[rr].split('');
      for (var k = 0; k < ll.length; k++) {
        if (ll[k] !== '.' && ll[k] !== 'N') { ll[k] = 'N'; if (++nCount >= 10) break; }
      }
      cells[rr] = ll.join('');
    }

    var name = pick(foreverNames);
    var tag = pick(foreverAdjs);
    if (Math.random() < 0.3) name = tag + ' ' + name;
    return {
      name: name,
      sub: 'OVERDRIVE FOREVER · depth ' + (diff + 1),
      rows: cells,
      forever: true, diff: diff
    };
  }

  // ---- public API --------------------------------------------------------------
  window.Levels = {
    COLS: 12,

    /** Level definition for 1-based human level index (levelIndex 0-based). */
    get: function (levelIndex) {
      if (levelIndex < levels.length) return levels[levelIndex];
      return generate(levelIndex);
    },

    isForever: function (levelIndex) { return levelIndex >= levels.length; },

    /** Validate one level's rows; returns true if well-formed. */
    valid: function (lv) {
      if (!lv || !lv.rows || !lv.rows.length) return false;
      for (var r = 0; r < lv.rows.length; r++) {
        if (typeof lv.rows[r] !== 'string' || lv.rows[r].length !== window.CFG.COLS) return false;
        for (var c = 0; c < lv.rows[r].length; c++) {
          if ('.NASGTB'.indexOf(lv.rows[r][c]) < 0) return false;
        }
      }
      return true;
    },

    /** Count brick types in a level (object keyed by symbol). */
    count: function (lv) {
      var cnt = { N: 0, A: 0, S: 0, G: 0, T: 0, B: 0 };
      for (var r = 0; r < lv.rows.length; r++) {
        for (var c = 0; c < lv.rows[r].length; c++) {
          var ch = lv.rows[r][c];
          if (ch !== '.') cnt[ch]++;
        }
      }
      return cnt;
    }
  };
})();
