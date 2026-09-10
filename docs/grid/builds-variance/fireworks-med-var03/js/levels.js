'use strict';
// ---------------------------------------------------------------------------
// levels.js — handcrafted level layouts. Strings are parsed by util.js
// parseLevelRows: letters = brick types, digits = powerup drops on a basic
// brick, '.' = empty. palette = [hueTop, hueBottom] for the row color ramp.
// Escalating difficulty; every mechanic is taught in isolation first.
// ---------------------------------------------------------------------------

const LEVELS = [
  {
    name: 'First Steps',
    palette: [190, 300],
    rows: [
      'bbbbbb.bbbbbb',
      'bbb2bb.bb2bbb',
      'bbbbbb.bbbbbb',
      '..bbbb.bbbb..',
    ],
  },
  {
    name: 'The Gate',
    palette: [45, 200],
    rows: [
      'uuuuuuuuuuuuu',
      'uuuuuuuuuuuuu',
      'bb2bb.bbb.2bb',
      'bbbbbbbbbbbbb',
      'bb.bbb.bbb.bb',
      'k...b2b...b..',
    ],
  },
  {
    name: 'Steel Rain',
    palette: [210, 260],
    rows: [
      '..s.......s..',
      '..ss.....ss..',
      '...ss...ss...',
      '....ss.ss....',
      '.....sss.....',
      '..b2b...b2b..',
      '..bbbbbbbbb..',
    ],
  },
  {
    name: 'Full Throttle',
    palette: [20, 55],
    rows: [
      'vvv.vvvvv.vvv',
      'v5vvv.vvvv.5v',
      'vvv.vvvvv.vvv',
      '.bbbbbbbbbbb.',
      'bbb2b.b.b2bbb',
      'b...........b',
    ],
  },
  {
    name: 'Ghost Town',
    palette: [270, 320],
    rows: [
      'ggggggggggggg',
      '..ggggggggg..',
      '.gggg.g.gggg.',
      'ggggggggggggg',
      '..b2b...b2b..',
      '....bb.bb....',
    ],
  },
  {
    name: 'Crossfire',
    palette: [55, 0],
    rows: [
      '..d.......d..',
      '.rrd..d..drr.',
      '..rr.r.r.rr..',
      'bbbbbbbbbbbbb',
      'b2b.bbbbb.b2b',
      '..bb..b..bb..',
    ],
  },
  {
    name: 'Prism Vault',
    palette: [170, 330],
    rows: [
      'sss...p...sss',
      '.sp.s.p.s.ps.',
      's..p..2..p..s',
      'ppp.ppppp.ppp',
      '..s.......s..',
      '.bbbbbbbbbbb.',
    ],
  },
  {
    name: 'The Escalator',
    palette: [160, 220],
    rows: [
      '..mmmmmmmmm..',
      '.m.m.m.m.m.m.',
      'mmmmmmmmmmmmm',
      '..m.m.m.m.m..',
      '.mm.mm2mm.mm.',
      '..bbbbbbbbb..',
    ],
  },
  {
    name: 'Switchboard',
    palette: [130, 190],
    rows: [
      '..d.......d..',
      'hhhhhhhhhhhhh',
      'hhhhhhhhhhhhh',
      'w.bbbbbbbbb.w',
      '..bvb.b.bvb..',
      '..b2b...b2b..',
    ],
  },
  {
    name: 'Overdrive Gauntlet',
    palette: [300, 180],
    rows: [
      'u.u.u.u.u.u.u',
      'pvgrmsdrmrgvp',
      'vvvvvvvvvvvvv',
      'sggwbkbwggsss',
      'mm.b2b.b2b.mm',
      '..d..bbb..d..',
    ],
  },
];
