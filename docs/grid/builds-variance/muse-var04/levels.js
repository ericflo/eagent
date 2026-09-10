/* levels.js — handcrafted layouts. Chars:
  '.' empty | 'N' normal | 'A' prism/angle (needs STEEP hit) | 'K' skimmer (needs SHALLOW hit)
  'V' velocity (needs FAST ball) | 'F' feather/drift (needs SLOW ball)
  'H' shifter (slides sideways) | 'X' volatile/bonus (explodes)
  Every map is 13 columns wide. Tunneling to the top is always possible.
*/
(function () {
  'use strict';
  const LEVELS = [
    {
      name: 'FIRST CONTACT', sub: 'Break through. Get above it all.',
      map: [
        '.............',
        '..NNNNNNNNN..',
        '..NNNNNNNNN..',
        '..NNNXNNNNN..',
        '..NNNNNNNNN..',
        '.............',
        '.............',
      ]
    },
    {
      name: 'THREAD THE NEEDLE', sub: 'Prisms want steep hits — come from above.',
      map: [
        '.............',
        '...AAAAAAA...',
        '..NNNNNNNNN..',
        '..NNNXNNNNN..',
        '..NNNNNNNNN..',
        '.....N.......',
        '.............',
      ]
    },
    {
      name: 'SKIM THE SURFACE', sub: 'Skimmers only break on grazing shots. Bend it.',
      map: [
        '.............',
        '...KKKKKKK...',
        '..NNNNNNNNN..',
        '..NVNNNXNVN..',
        '..NNNNNNNNN..',
        '.............',
        '.............',
      ]
    },
    {
      name: 'NEED FOR SPEED', sub: 'Fiery bricks need SPEED. Smash up. Pale ones need slow.',
      map: [
        '.............',
        '..VVVVVVVVV..',
        '..NHNNNNHNN..',
        '..NXNNNNNXN..',
        '..FFFFFFFFFF.',
        '..NNNNNNNNN..',
        '.............',
      ]
    },
    {
      name: 'SHIFT HAPPENS', sub: 'Moving bricks. Lead your shots. Volatiles chain!',
      map: [
        '.............',
        '..HAANHAAH...',
        '..NNNNNNNNN..',
        '.NXNXNNXNXN..',
        '..NNNNNNNNN..',
        '..KVNNNVNK...',
        '..NNNNNNNNN..',
      ]
    },
    {
      name: 'THE SKY VAULT', sub: 'Everything at once. Punch through, live above.',
      map: [
        '..X.......X..',
        '..AAAKKAA....',
        '.NVHNXNVHK...',
        '..FFFFVVFN...',
        '.NHNNNNNHN...',
        '..XNXNNXNX...',
        '.NNNNNNNNN...',
        '.............',
      ]
    },
  ];
  // Brick visual config per type
  const BRICK_STYLE = {
    N: { name: 'Block', color: '#00c8ff', glow: '#00f0ff', icon: '■', hint: 'NORMAL BLOCK — one hit breaks it' },
    A: { name: 'Prism', color: '#7b5cff', glow: '#b388ff', icon: '▲', hint: 'PRISM — hits must be STEEP (near-vertical)' },
    K: { name: 'Skimmer', color: '#2dffc4', glow: '#2dffc4', icon: '≋', hint: 'SKIMMER — hits must be SHALLOW (grazing)' },
    V: { name: 'Velocity', color: '#ff6a00', glow: '#ffb03a', icon: '🔥', hint: 'VELOCITY — ball must be FAST' },
    F: { name: 'Feather', color: '#f2ecff', glow: '#cfd6ff', icon: '❄', hint: 'FEATHER — ball must be SLOW' },
    H: { name: 'Shifter', color: '#ffe14d', glow: '#ffd23f', icon: '⇔', hint: 'SHIFTER — slides sideways, one hit breaks' },
    X: { name: 'Volatile', color: '#ff2fd6', glow: '#ff7ae8', icon: '✸', hint: 'VOLATILE — explodes, chains + drops a gift' },
  };
  // Power-up config (8 real effects)
  const POWERUPS = {
    multi: { name: 'MULTIBALL', icon: '✚', color: '#00f0ff', desc: 'Every ball splits into 3' },
    fire: { name: 'FIREBALL', icon: '🔥', color: '#ff6a00', desc: 'Pierces bricks ~10s + flame trail' },
    phase: { name: 'GHOST', icon: '👻', color: '#b388ff', desc: 'Passes through bricks ~8s' },
    big: { name: 'BIG BALL', icon: '●', color: '#39ff88', desc: 'Huge ball ~8s' },
    slow: { name: 'SLOW-MO', icon: '◷', color: '#7df9ff', desc: 'Slows all balls ~8s' },
    wide: { name: 'WIDE PADDLE', icon: '▬', color: '#39ff88', desc: 'Wide paddle ~12s' },
    shield: { name: 'SHIELD NET', icon: '⛨', color: '#ffd23f', desc: 'Safety net catches 1 miss ~15s' },
    life: { name: 'EXTRA LIFE', icon: '♥', color: '#ff4d6d', desc: '+1 life (rare)' },
    laser: { name: 'LASERS', icon: '⌁', color: '#ff2fd6', desc: 'Tap/click fires bolts ~10s' },
  };
  window.LEVELS = LEVELS;
  window.BRICK_STYLE = BRICK_STYLE;
  window.POWERUPS = POWERUPS;
})();
