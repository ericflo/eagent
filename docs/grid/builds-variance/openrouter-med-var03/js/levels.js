/* ============================================================
   Overdrive Breakout — levels.js
   Hand-designed ASCII level maps. 30 columns wide.
   Legend:
     .  empty            X  standard brick
     A  ANGLE-LOCK (steep entry required)   a  ANGLE-LOCK (shallow required)
     S  SPEED brick (momentum shell)
     P  PHASE brick (solid/blinking)
     R  REFLECT (armored; only powered balls shatter it)
     $  PAYDIRT (drops power-up, big points)
     #  STEEL (indestructible deflector)
   ============================================================ */
'use strict';

const LEVELS = [
  {
    name: 'WARM UP',
    intro: 'Break the wall. Get the ball UP TOP for OVERDRIVE.',
    rows: [
      '..............................',
      '..............................',
      '....XXXXXXXXXXXXXXXXXXXXXX....',
      '....X$X.X$X.X$X.X$X.X$X.X$X..',
      '....XXXXXXXXXXXXXXXXXXXXXX....',
      '..............................',
      '..........RX............XR....'
    ]
  },
  {
    name: 'STEEL GATES',
    intro: 'STEEL bricks never break. Deflect around them.',
    rows: [
      '..............................',
      '......RRRRRRRRRRRRRRRRRRRR....',
      '......XSXSXSXSXSXSXSXSXSXR....',
      '......R....................R..',
      '......R.#..#..#..#..#..#..R...',
      '......R....................R..',
      '......RRRRRR....RRRRRRRRRRRR..',
      '.........X$X.X$X.X$X.X$X......'
    ]
  },
  {
    name: 'MOMENTUM SHELLS',
    intro: 'SPEED bricks need a fast ball. Move the paddle UP while hitting to SMASH.',
    rows: [
      '..............................',
      '....RRRRRRRRRRRRRRRRRRRRRR....',
      '....SSSSSSSSSSSSSSSSSSSSSS....',
      '....S$S.SX$X..SX$X..S$S.....',
      '....SSSSSSSSSSSSSSSSSSSSSS....',
      '....AAAAAAA..........AAAAAAA..',
      '..............................',
      '.........R..........R.........'
    ]
  },
  {
    name: 'PHASE SHIFT',
    intro: 'PHASE bricks blink solid/ghost. Time your shots.',
    rows: [
      '..............................',
      '....RPPPPPPPPPPPPPPPPPPPPR....',
      '....P$P.XXXX..XXXX..P$P.....',
      '....PPPPPPPPPPPPPPPPPPPPPP....',
      '....A...A...#...#...A...A.....',
      '....PPPPPPPPPPPPPPPPPPPPPP....',
      '......S$S..SSSSSSSS..S$S.....',
      '..............................'
    ]
  },
  {
    name: 'ANGLE FORTRESS',
    intro: 'ANGLE-LOCK bricks only break at the right approach. A needs steep, a needs shallow.',
    rows: [
      '..............................',
      '.....RRRRRRRRRRRRRRRRRRRR.....',
      '.....RAAAR.RRRRRR.RAAAR.....',
      '.....RRRRR.R$$$.RRRRR.....',
      '.....RAAAR.a....a.RAAAR.....',
      '.....RRRRRRRRRRRRRRRRRRRR.....',
      '......SSSSSS...SSSSSS.........',
      '.........X$X.X$X.X$X.........'
    ]
  },
  {
    name: 'SUPERNOVA VAULT',
    intro: 'The vault: REFLECT everywhere. Earn a power ball to break through and go OVERDRIVE.',
    rows: [
      '..............................',
      '....RRRRRRRRRRRRRRRRRRRRRR....',
      '....RPPPPPPR.RR.RRPPPPPPR....',
      '....RPSR.X$X.SS.X$X.RSPR....',
      '....RPPPPPPR.RR.RRPPPPPPR....',
      '....RRRRRRRRRRRRRRRRRRRRRR....',
      '......#..AAAA..RR..SSS..#.....',
      '......X$X.X$X.XXXX.X$X.X$X....'
    ]
  }
];