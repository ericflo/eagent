/* OVER THE TOP — brick legend + hand-designed levels.
   Map chars: .=empty  #=standard  T=titanium  P=prism(angle)  V=velocity
                D=drifter  H=phase/blink  B=bomb  O=bonus */
(function (global) {
  'use strict';
  const BRICKS = {
    standard: { name: 'Standard', color: '#38f8ff', color2: '#1a8fb5', desc: 'Breaks on any hit. +50 pts.' },
    prism:    { name: 'Prism (Angle)', color: '#b06bff', color2: '#5b2bb5', desc: 'Breaks only on STEEP hits (≥35° from horizontal). Shallow hits deflect + shield flash.' },
    velocity: { name: 'Velocity', color: '#ffd94f', color2: '#b57e1a', desc: 'Breaks only if ball is FAST (≥520 px/s). Slow balls bounce off + shield.' },
    drifter:  { name: 'Drifter', color: '#7dff6a', color2: '#2b9e4b', desc: 'Slides side-to-side. Breaks on any hit.' },
    phase:    { name: 'Phase / Blink', color: '#ff9a3d', color2: '#b5541a', desc: 'Only solid while VISIBLE. Passes through while ghosted — time your shots!' },
    bomb:     { name: 'Bomb', color: '#ff4f4f', color2: '#8f1a1a', desc: 'Explodes: damages all neighbours + AoE particles & shake.' },
    titanium: { name: 'Titanium', color: '#8f9bb3', color2: '#4a5266', desc: 'INDESTRUCTIBLE. Level architecture — bounce off it, funnel around it.' },
    bonus:    { name: 'Bonus', color: '#ff4fd8', color2: '#a11a8f', desc: 'Always drops a random power-up pickup. ★' }
  };
  const BALLS = {
    normal: { name: 'Normal', color: '#ffffff', desc: 'Reliable classic.' },
    fire:   { name: 'Fire', color: '#ff7a2f', desc: 'Explosive AoE on every hit + trail.' },
    ghost:  { name: 'Ghost', color: '#b06bff', desc: 'Phases THROUGH bricks, damaging each.' },
    volt:   { name: 'Volt', color: '#fff94f', desc: 'Super-fast, double score.' },
    big:    { name: 'Big', color: '#6afff3', desc: 'Huge & heavy, easy bounces.' }
  };
  const POWERUPS = [
    { kind: 'multiball', label: 'MULTIBALL ×3', color: '#38f8ff' },
    { kind: 'fire', label: 'FIRE BALL', color: '#ff7a2f' },
    { kind: 'ghost', label: 'GHOST BALL', color: '#b06bff' },
    { kind: 'volt', label: 'VOLT BALL', color: '#fff94f' },
    { kind: 'big', label: 'BIG BALL', color: '#6afff3' },
    { kind: 'wide', label: 'WIDE PADDLE', color: '#7dff6a' },
    { kind: 'slow', label: 'SLOW-MO', color: '#9ad7ff' },
    { kind: 'life', label: '+1 LIFE', color: '#ff4fd8' },
    { kind: 'catch', label: 'CATCH (8s)', color: '#ffd94f' }
  ];

  const LEVELS = [
    {
      name: 'FIRST CONTACT', sub: 'One gap in the wall. Thread it — then live ABOVE the bricks.',
      hint: 'Aim for the middle gap. Once a ball is above the lowest brick row: TOP ZONE ×bonus!',
      map: [
        '............',
        '............',
        '.....OO.....',
        '............',
        '...######...',
        '..########..',
        '..###..###..',
        '..###..###..',
        '...######...'
      ]
    },
    {
      name: 'FUNNEL CAKE', sub: 'Titanium funnels pour balls into the kill box.',
      hint: 'Titanium is indestructible — use the funnels! Fire balls crack groups open.',
      map: [
        '............',
        '.....BB.....',
        '....####....',
        '...######...',
        '..T######T..',
        '..TT#OO#TT..',
        '....#..#....',
        '....#..#....',
        '.....##.....'
      ]
    },
    {
      name: 'PRISM PRISON', sub: 'Angle-gated prisms. Come down STEEP or bounce off.',
      hint: 'Prism bricks need steep hits (≥35°). Hit them from above/below, not the side!',
      map: [
        '............',
        '............',
        '..PPPPPPPP..',
        '..P######P..',
        '..P#OHHO#P..',
        '..P#HBBH#P..',
        '...######...',
        '.....##.....',
        '............'
      ]
    },
    {
      name: 'NEED FOR SPEED', sub: 'Velocity bricks laugh at slow balls. POWER HIT them!',
      hint: 'Yellow bricks need speed ≥ threshold. Flick the paddle UP into the ball for POWER HIT!',
      map: [
        '............',
        '.....OO.....',
        '..VVVVVVVV..',
        '..V######V..',
        '..V#DBBD#V..',
        '..TT#HH#TT..',
        '...######...',
        '..###..###..',
        '............'
      ]
    },
    {
      name: 'BLINK & DRIFT', sub: 'Movers and blinkers. Time it — Ghost balls ignore timing.',
      hint: 'Orange bricks are solid only when visible. Green drifters slide. Ghost powerups phase through!',
      map: [
        '............',
        '..D......D..',
        '..HHHHHHHH..',
        '..H######H..',
        '..T#OPPO#T..',
        '..T#VBBV#T..',
        '...######...',
        '....DDDD....',
        '............'
      ]
    },
    {
      name: 'OVER THE TOP', sub: 'The gauntlet. Bomb the core, live in the TOP ZONE.',
      hint: 'Chain bombs for huge combos. Everything you learned — now at full speed.',
      map: [
        '.....OO.....',
        '..PHVVVHP...',
        '..P######P..',
        '.TB#OBBO#BT.',
        '.TT#DHHDT.T.',
        '..P#HBBH#P..',
        '..P######P..',
        '..TTTTTTTT..',
        '...##..##...'
      ]
    }
  ];
  global.OTT = global.OTT || {};
  global.OTT.BRICKS = BRICKS;
  global.OTT.BALLS = BALLS;
  global.OTT.POWERUPS = POWERUPS;
  global.OTT.LEVELS = LEVELS;
})(window);
