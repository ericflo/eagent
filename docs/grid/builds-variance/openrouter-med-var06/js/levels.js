// Level definitions. Grid strings, 11 cols. Legend:
// . empty | 1/2/3 standard (color tiers) | T shield open-top | B shield open-bottom
// P phase (needs speed) | D deflector | M mover | X bomb | A armored | R regenerating | V prism

import { Brick, PU_META } from './entities.js';

export const LEVELS = [
  {
    name: 'WARM UP',
    hint: 'Smash upward with the paddle to add speed. Break the wall!',
    rows: [
      '...........',
      '.111111111.',
      '.122222221.',
      '.111111111.',
    ],
  },
  {
    name: 'THE ROOF',
    hint: 'Shields only break from the OPEN side. Hit them from above — break through to the roof!',
    rows: [
      '.111111111.',
      '.2TTTTTTT2.',
      '.111111111.',
      '...........',
    ],
  },
  {
    name: 'PYRAMID',
    hint: 'PHASE bricks need a FAST ball. Smash upward with the paddle to boost speed!',
    rows: [
      '.....3.....',
      '....3A3....',
      '...33333...',
      '..3222223..',
      '.3TTTTTTT3.',
      '...........',
    ],
  },
  {
    name: 'DEFLECTORS',
    hint: 'DEFLECTORS bounce the ball at odd angles. Use them to bank into the corners!',
    rows: [
      '1D.......D1',
      '.1..XXX..1.',
      '.2P.....P2.',
      '.111111111.',
      '...........',
      '.T.......T.',
    ],
  },
  {
    name: 'THE CORRIDOR',
    hint: 'Open a hole in the corridor roof and let the ball run loose on top. RUSH!',
    rows: [
      'TTTTT.TTTTT',
      '22222222222',
      'M.........M',
      '11P11111P11',
      '...........',
      '.A.......A.',
    ],
  },
  {
    name: 'BOMB SQUAD',
    hint: 'BOMBS chain-explode. Set one off under the shield row and watch it cascade.',
    rows: [
      'A.X.X.X.X.A',
      '.2M...M2..',
      '11X11111X11',
      '.TTT.T.TTT.',
      '...........',
      'P.........P',
    ],
  },
  {
    name: 'GAUNTLET',
    hint: 'REGenerating walls rebuild if you wait. Keep the pressure on!',
    rows: [
      'RR.RRRRR.RR',
      '.D.......D.',
      '2A22222A2..',
      'TTTTT.TTTTT',
      '.M.......M.',
      '11P111P11..',
    ],
  },
  {
    name: 'BREAKTHROUGH',
    hint: 'Everything at once. Punch through, get on top, and let the frenzy take over.',
    rows: [
      'T.R.V.T.A.T',
      '2M2M2M2M2M2',
      '1P1X1X1P1X1',
      '.TTT.T.TTT.',
      'A.........A',
      '11D11111D11',
    ],
  },
];

// ---- endless procedural levels ----
export function makeEndlessLevel(n) { // n = 9, 10, ...
  const rng = mulberry(n * 7919 + 13);
  const rows = [];
  const kinds = ['1','2','3','T','B','P','D','M','X','A','R','V'];
  const density = 0.5 + Math.min(0.35, (n - 8) * 0.04);
  const rowCount = 5 + Math.min(4, (n - 8) >> 1);
  for (let r = 0; r < rowCount; r++) {
    let row = '';
    for (let c = 0; c < 11; c++) {
      row += rng() < density ? kinds[(rng() * kinds.length) | 0] : '.';
    }
    rows.push(row);
  }
  // guarantee a breakthrough lane: carve a gap near the middle top row
  const top = rows[0].split('');
  top[5] = top[4] = top[6] = '.';
  rows[0] = top.join('');
  return { name: 'ENDLESS ' + (n - 8), rows, endless: true };
}

export function buildBricks(level, w, h, x0) {
  const bricks = [];
  const cols = 11;
  level.rows.forEach((row, r) => {
    for (let c = 0; c < cols; c++) {
      const ch = row[c];
      if (!ch || ch === '.') continue;
      const o = { x: x0 + c * (w + 0.7), y: 22 + r * (h + 0.7), w, h };
      switch (ch) {
        case '1': bricks.push(new Brick({ ...o, kind: 'std', color: '#4ea6ff' })); break;
        case '2': bricks.push(new Brick({ ...o, kind: 'std', color: '#b06cff' })); break;
        case '3': bricks.push(new Brick({ ...o, kind: 'std', color: '#ff6c8a' })); break;
        case 'T': bricks.push(new Brick({ ...o, kind: 'shield', shieldSide: 'top', color: '#6cf7d0' })); break;
        case 'B': bricks.push(new Brick({ ...o, kind: 'shield', shieldSide: 'bottom', color: '#6cf7d0' })); break;
        case 'P': bricks.push(new Brick({ ...o, kind: 'phase', color: '#ffe06c', phaseSpeed: 90 })); break;
        case 'D': bricks.push(new Brick({ ...o, kind: 'deflect', color: '#8cff9e' })); break;
        case 'M': bricks.push(new Brick({ ...o, kind: 'mover', color: '#ff9e6c', moveAmp: 4 + Math.random() * 3, moveAxis: c % 2 ? 'x' : 'y', moveSpeed: 1.2 })); break;
        case 'X': bricks.push(new Brick({ ...o, kind: 'bomb', color: '#ffae42' })); break;
        case 'A': bricks.push(new Brick({ ...o, kind: 'armor', color: '#9aa7c8' })); break;
        case 'R': bricks.push(new Brick({ ...o, kind: 'regen', color: '#c86cff' })); break;
        case 'V': bricks.push(new Brick({ ...o, kind: 'prism', color: '#e0e0ff' })); break;
      }
    }
  });
  return bricks;
}

export const BRICK_HINTS = {
  shield: 'SHIELD BRICK: only breaks from the open side — get the ball on top!',
  phase:  'PHASE BRICK: needs a FAST ball — smash upward with the paddle!',
  deflect:'DEFLECTOR: bounces the ball at a weird angle!',
  mover:  'MOVING BRICK: it drifts — time your shots!',
  bomb:   'BOMB BRICK: explodes and chains to neighbours!',
  armor:  'ARMORED BRICK: only FIRE or PIERCE balls can break it!',
  regen:  'REGENERATOR: rebuilds over time — keep attacking!',
  prism:  'PRISM: scatters the ball into a new direction!',
};

function mulberry(a) {
  return function () {
    a |= 0; a = a + 0x6D2B79F5 | 0;
    let t = Math.imul(a ^ a >>> 15, 1 | a);
    t = t + Math.imul(t ^ t >>> 7, 61 | t) ^ t;
    return ((t ^ t >>> 14) >>> 0) / 4294967296;
  };
}