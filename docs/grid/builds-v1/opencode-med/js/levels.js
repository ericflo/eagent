// ---------------------------------------------------------------------------
// levels.js — designed level layouts
//
// legend:
//   '.' empty
//   '1' standard
//   'A' ANGLE  (breakable only when impact is steep; |vx/vy| <= 0.55)
//   'B' ANGLE side walls (SAME gate as 'A' but column-flipped look; steep too,
//       exists to widen layouts, purely cosmetic variant)
//   'S' SPEED  (breakable only when ball speed >= 640)
//   'P' PHASE  (solid/flicker: solid 1.15s, ethereal 0.55s, cycle 1.7s)
//   'X' ARMORED (speed >= 640 AND steep; else just thunks)
//   'U' UNSTABLE (chain-explodes neighbours when broken)
//   'W' SPINNER (rotating disc brick; breaks on any hit)
//   '2' BRICK bonus (worth 500, breakable like standard)
// ---------------------------------------------------------------------------

import { BRICK } from './config.js';

export const TYPE_INFO = {
  '1': { name: 'standard', pts: 100 },
  '2': { name: 'bonus', pts: 500 },
  A: { name: 'angle', pts: 180 },
  S: { name: 'speed', pts: 220 },
  P: { name: 'phase', pts: 200 },
  X: { name: 'armored', pts: 350 },
  U: { name: 'unstable', pts: 150 },
  W: { name: 'spinner', pts: 120 },
};

const L = (name, hint, rows) => ({ name, hint, rows });

export const LEVELS = [
  L('FIRST CONTACT', 'Break through. Get the ball on top of the wall.', [
    '.............',
    '.11111111111.',
    '.11111111111.',
    '.11111111111.',
    '.11111111111.',
    '.11111111111.',
  ]),

  L('THIN ICE', 'Angle bricks only break on a steep hit — come in vertical.', [
    '.............',
    '.A.A.A.A.A.A.',
    '.11111111111.',
    '.11111111111.',
    '.A.A.A.A.A.A.',
    '.11111111111.',
    '.A.A.A.A.A.A.',
  ]),

  L('GREEN LIGHT', 'Speed bricks ignite when the ball is fast. Hit them hard.', [
    '.............',
    '.11111111111.',
    '.S.S.S.S.S.S.',
    '.11111111111.',
    '.S.S.S.S.S.S.',
    '.11111111111.',
  ]),

  L('BLINK', 'Phase bricks flicker between solid and ghost. Time your shots.', [
    '.............',
    '.P.P.P.P.P.P.',
    '.11111111111.',
    '.P1P1P1P1P1P.',
    '.11111111111.',
    '.P.P.P.P.P.P.',
    '.............',
  ]),

  L('SKYWARD', 'A maze with an open crown. Reach the rim and go OVERDRIVE.', [
    '.............',
    'A.A.A.A.A.A.A',
    '.............',
    '.111.111.111.',
    '.1S1.1S1.1S1.',
    '.111.111.111.',
    '.............',
    'A.A.A.A.A.A.A',
  ]),

  L('THE VAULT', 'Armored bricks need speed AND a steep angle. Punch the core.', [
    '.............',
    '.11111111111.',
    '.1SXX1X1XXS1.',
    '.1X.X1X1X.X1.',
    '.1SXX1X1XXS1.',
    '.11111111111.',
    '..A.........A',
  ]),

  L('CHAIN REACTION', 'Unstable bricks take their neighbours with them.', [
    '.............',
    '..A...A...A..',
    '.11111111111.',
    '.1U1111U11U1.',
    '.11111111111.',
    '..U...U...U..',
    '.11111111111.',
  ]),

  L('SPIN CYCLE', 'Spinner bricks rotate. Every hit counts, but they can be coy.', [
    '.............',
    '.1W1W1W1W1W1.',
    '.11111111111.',
    '.W1W1W1W1W1W.',
    '.11111111111.',
    '.1W1W1W1W1W1.',
  ]),

  L('RAMPART', 'Everything at once. The crown is open — finish the job up top.', [
    '.............',
    'S.A.P.X.U.W.S',
    '.............',
    '.11111111111.',
    '.1P111U111S1.',
    '.11111111111.',
    '..A.A.....A.A',
  ]),

  L('THE GAUNTLET', 'Deep and defended. Endurance pays.', [
    '.............',
    '.11111111111.',
    '.XSXSPXPSXSX.',
    '.11111111111.',
    '.AUA1P1P1AUA.',
    '.11111111111.',
    '.S1X1S1S1X1S.',
    '.11111111111.',
  ]),
];

export function brickRect(col, row) {
  return {
    x: BRICK.gap + col * (BRICK.w + BRICK.gap) + 1,
    y: BRICK.top + row * (BRICK.h + BRICK.gap),
    w: BRICK.w - 2,
    h: BRICK.h,
  };
}

export const COLS = BRICK.cols;
export const LEVEL_ROWS = Math.max(...LEVELS.map((l) => l.rows.length));
