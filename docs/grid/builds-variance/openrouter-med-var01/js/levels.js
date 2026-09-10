// levels.js — 12 handcrafted levels + endless seeded procedural generation
// Type letters: S STANDARD, W WEDGE, K KINETIC, P PHASE, V VOLATILE, D DRIFT, G GOLD, . empty
'use strict';

const Levels = (() => {
  // Each level: { name, rows }  rows[0] = top row
  const HAND = [
    { name: 'First Contact', rows: [
      'SSSSSSSSSS', 'SSSSSSSSSS', 'SSSSSSSSSS', 'SSSSSSSSSS',
    ]},
    { name: 'The Gap', rows: [
      'SSSS..SSSS', 'SSSS..SSSS', 'SSSS..SSSS', 'SSSSSSSSSS', 'SSSS..SSSS',
    ]},
    { name: 'Pyramid Scheme', rows: [
      '....SS....', '...SSSS...', '..SSSSSS..', '.SSSSSSSS.', 'SSSSSSSSSS',
    ]},
    { name: 'Tunnel Vision', rows: [
      'SSSSSSSSSS', 'S.........', 'S..GGGGG.S', 'S.........', 'SSSSSSSSSS', 'SSSSSSSSSS',
    ]},
    { name: 'Wedge Issues', rows: [
      'WWWWWWWWWW', 'SSSSSSSSSS', 'W.W.W.W.W.', 'SSSSSSSSSS', 'WWWWWWWWWW',
    ]},
    { name: 'Fast Lane', rows: [
      'KKKKKKKKKK', 'SSSSSSSSSS', 'KK..KK..KK', 'SSSSSSSSSS', 'KKKKKKKKKK',
    ]},
    { name: 'Blink of an Eye', rows: [
      'P.P.P.P.P.', 'SSSSSSSSSS', '.P.P.P.P.P', 'SSSSSSSSSS', 'P.P.P.P.P.',
    ]},
    { name: 'Gold Rush', rows: [
      'G.G.G.G.G.', 'SSSSSSSSSS', 'GGSGSGSGSG', 'SSSSSSSSSS', '.G.G.G.G.G',
    ]},
    { name: 'Driftwood', rows: [
      'DDDDDDDDDD', 'SSSSSSSSSS', 'D.DD..DD.D', 'SSSSSSSSSS', 'DDDDDDDDDD',
    ]},
    { name: 'Chain Reaction', rows: [
      'SV.V.SV.V.', 'SSSSSSSSSS', '.V.SS.S.V.', 'SSSSSSSSSS', 'V.S.VV.S.V',
    ]},
    { name: 'The Gauntlet', rows: [
      'WKW..K..WKW'.slice(0, 10), 'SPKPSKPSPK'.slice(0, 10), 'KWDWPDWKWD'.slice(0, 10),
      'SSKKGGKKSS', 'WPKPWKKPKW'.slice(0, 10),
    ]},
    { name: 'Break on Through', rows: [
      'WWWWW..WWW', 'SSSSS..SSS', 'KGGGG..GGK', 'SSSSS..SSS', 'WWWWW..WWW',
      'SS..V..V.S', 'PK..K..KP.',
    ]},
  ];

  // Seeded procedural level generator (level index >= 12)
  function generate(index) {
    const rng = Util.mulberry32(0x0F3A7 + index * 2654435761);
    const rows = [];
    const nRows = 4 + Math.min(5, Math.floor((index - 12) / 2));
    // difficulty weighting
    const weights = [
      ['S', 40], ['W', Math.min(18, 4 + index)], ['K', Math.min(14, index - 4)],
      ['P', Math.min(12, index - 6)], ['V', 6], ['D', Math.min(12, index - 7)], ['G', 4],
    ];
    const pool = [];
    for (const [t, w] of weights) if (w > 0) for (let i = 0; i < w; i++) pool.push(t);
    // pick a guaranteed tunnel column for "get on top" play
    const tunnel = 1 + ((rng() * (CONFIG.BRICK_COLS - 2)) | 0);
    for (let r = 0; r < nRows; r++) {
      let row = '';
      for (let c = 0; c < CONFIG.BRICK_COLS; c++) {
        if (Math.abs(c - tunnel) <= (r % 2 === 0 ? 0 : 0) && r < 3) { row += '.'; continue; }
        row += rng() < 0.12 ? '.' : pool[(rng() * pool.length) | 0];
      }
      rows.push(row);
    }
    // guarantee at least the tunnel hole exists
    for (let r = 0; r < 3; r++) rows[r] = rows[r].substring(0, tunnel) + '.' + rows[r].substring(tunnel + 1);
    return { name: 'Deep ' + (index - 11), rows };
  }

  function get(index) {
    if (index < HAND.length) return HAND[index];
    return generate(index);
  }
  const countHand = () => HAND.length;

  return { get, generate, countHand };
})();