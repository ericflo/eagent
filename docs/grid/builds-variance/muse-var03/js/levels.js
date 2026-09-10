/* levels.js — 5 designed levels + endless generator.
   Brick type codes:
   '.' empty | 'n' normal | 'G' angle-gate | 'V' velocity | 'M' mover | 'H' ghost/phase | 'S' shielded
   Color letters are cosmetic variety: colors auto-assigned by row. */
'use strict';
const Levels = (() => {
  const L = [
    {
      name: 'FIRST CONTACT',
      hint: 'Punch a hole, get ABOVE the bricks for OVERDRIVE!',
      map: [
        '........',
        '.nnnnnn.',
        '.nnnnnn.',
        '.nnnnnn.',
        '........',
        '........',
      ]
    },
    {
      name: 'GATE CRASHERS',
      hint: 'Yellow GATE bricks only break on STEEP hits — come down on them!',
      map: [
        '........',
        '.nGnnGnn.',
        '.nnnnnnn.',
        '.nGnnGnn.',
        '..nnnnn..',
        '........',
      ]
    },
    {
      name: 'NEED FOR SPEED',
      hint: 'Red SPEED bricks need a FAST ball — flick UP on the paddle!',
      map: [
        '........',
        '.nVnMnVn.',
        '.nnnnnnn.',
        '.nHnnnHn.',
        '..nnSnn..',
        '........',
      ]
    },
    {
      name: 'HAUNTED ARCADE',
      hint: 'Ghost bricks phase in/out. Shielded bricks: hit from BELOW only!',
      map: [
        '........',
        '.HnMnMnH.',
        '.nGnVnGn.',
        '.nSnMnSn.',
        '..nnnnn..',
        '...nGn...',
      ]
    },
    {
      name: 'THE GAUNTLET',
      hint: 'Everything at once. Get on top and stay there!',
      map: [
        '.MHGnHGn.',
        '.nVnSnVn.',
        '.GnMnMnG.',
        '.nHnSnHn.',
        '..VnMnV..',
        '...GGG...',
      ]
    },
  ];

  function endless(idx) {
    // idx = level number beyond 5 (1-based overall). Density + specials scale.
    const rows = 6, cols = 8;
    const diff = Math.min(1, (idx - 5) * 0.15 + 0.3);
    const pool = ['n', 'n', 'n', 'G', 'V', 'M', 'H', 'S'];
    const map = [];
    for (let r = 0; r < rows; r++) {
      let row = '';
      for (let c = 0; c < cols; c++) {
        if (r === 0 && (c === 0 || c === cols - 1)) { row += '.'; continue; }
        if (Math.random() < 0.18 - diff * 0.06) { row += '.'; continue; }
        row += Math.random() < 0.45 ? 'n' : pool[(Math.random() * pool.length) | 0];
      }
      map.push(row);
    }
    return { name: 'ENDLESS ' + (idx - 5), hint: 'Survive the infinite arcade!', map };
  }

  function get(idx) { // idx 0-based; 5+ => endless
    if (idx < L.length) return L[idx];
    return endless(idx + 1);
  }
  function count() { return L.length; }
  return { get, count };
})();
