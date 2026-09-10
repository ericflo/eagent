'use strict';
/* ============================================================
   Attic Breakout — levels.js
   Levels are readable string maps. Legend:
     .  empty        S  Standard     A  Armored (speed gate)
     L  Angle-lock   R  Regen        M  Metal (indestructible)
     P  Prism (always drops a power-up)
   Early levels keep the attic wide open; later ones add metal
   channels and armored/angle-lock gates that demand technique.
   ============================================================ */

const LEVELS = [
  {
    name: 'Open Attic',
    hint: 'Punch through to the space above — the attic is where the points live.',
    rows: [
      '............',
      '............',
      'SSSSSSSSSSSS',
      'SSSSSSSSSSSS',
      'SSSSSSSSSSSS',
      'SSSSSSSSSSSS',
    ],
  },
  {
    name: 'Prism Path',
    hint: 'Prisms always drop a capsule. Catch them.',
    rows: [
      '............',
      '......P.....',
      'SSSSSSSSSSSS',
      'SS.SSSSSS.SS',
      'SSSSSSSSSSSS',
      'SS.SS.P.SS.SS',
      'SSSSSSSSSSSS',
    ],
  },
  {
    name: 'The Heavy Door',
    hint: 'Armored bricks only break above 640 speed. Move UP into the ball to SMASH.',
    rows: [
      '............',
      '............',
      'SSSSSSSSSSSS',
      'AAASSSSSSAAA',
      'SSSSSSSSSSSS',
      'SSSAAPAAASSS',
      'SSSSSSSSSSSS',
    ],
  },
  {
    name: 'Straight On',
    hint: 'Angle-lock bricks demand a steep, straight-on hit. Ghost Balls ignore angles.',
    rows: [
      '............',
      '............',
      'SSSSSSSSSSSS',
      'SLLSSSSSSLLS',
      'SSSSSSSSSSSS',
      'SSLLLLLLLLSS',
      'SSSSSSSSSSSS',
      'SSLSSS.PSSLS',
    ],
  },
  {
    name: 'The Healing Wall',
    hint: 'Regen bricks heal after 5s. Chain your hits before they close up.',
    rows: [
      '............',
      '......MM....',
      'SSSSSSSSSSSS',
      'SRRSSSSSSRRS',
      'SSSSSSSSSSSS',
      'SSSRRPPRRSSS',
      'SSSSSSSSSSSS',
      'SSSSRLLRSSSS',
    ],
  },
  {
    name: 'The Gauntlet',
    hint: 'Everything at once. Find the channel, live in the attic.',
    rows: [
      'M....MM....M',
      'M..P.....P.M',
      'MSSSSSSSSSSM',
      'MSAASLLSSAAS',
      'MSSSSSSSSSSM',
      'MSSRRSSRRSSM',
      'MSLSSSSSSLSM',
      'MSSSSPSSSSSM',
      'MM..S..S..MM',
    ],
  },
];

window.LEVELS = LEVELS;
