/* BreakoutLevels — 6+ hand-designed levels. Grid strings + legend.
   Legend: '.' gap | 'N' normal | 'S' steep/prism | 'F' flat/skimmer |
           'V' velocity | 'B' blink/phaser | 'D' drifter
   Every layout leaves a gap or weak column so the player can tunnel to TOPSIDE.
   Global: window.BreakoutLevels */
(function () {
  'use strict';
  var LEVELS = [
    {
      name: 'WARM UP',
      hint: 'Break a column and slip ABOVE the bricks for OVERDRIVE!',
      rows: [
        '..........',
        '..NNNNNN..',
        '..NNNNNN..',
        '..NNNNNN..',
        '...NNNN...',
        '..........'
      ]
    },
    {
      name: 'PRISM GATE',
      hint: '▲ Prism bricks only break on STEEP hits. Hit them straight-on!',
      rows: [
        '..........',
        '.SS....SS.',
        '.SSNNNNSS.',
        '..NNNNNN..',
        '..NN..NN..',
        '...NNNN...',
        '..........'
      ]
    },
    {
      name: 'SKIMMER ALLEY',
      hint: '▼ Skimmers need GRAZING hits. Steep balls just bounce off!',
      rows: [
        '..........',
        '.FF....FF.',
        '.FFNNNNFF.',
        '..NNSSNN..',
        '..NN..NN..',
        '...NNNN...',
        '..........'
      ]
    },
    {
      name: 'NEED FOR SPEED',
      hint: '◆ Velocity bricks need ball speed 560+. Flick UP for POWER HIT!',
      rows: [
        '..........',
        '..VVVVVV..',
        '..VNNNNV..',
        '.NNS..SNN.',
        '.NNF..FNN.',
        '...NNNN...',
        '..........'
      ]
    },
    {
      name: 'PHASE STORM',
      hint: '👻 Phasers blink solid/intangible. Time your shots — Ghost helps!',
      rows: [
        '..........',
        '.BBNNNNBB.',
        '.BNVDVVNB.',
        '.BNVSSVNB.',
        '..NNFFNN..',
        '...N..N...',
        '..........'
      ]
    },
    {
      name: 'DRIFT KING',
      hint: '↔ Drifters slide sideways. Everything at once — punch through!',
      rows: [
        '..........',
        '.DDBBBDD..',
        '.DNVVVVD..',
        '.DNVSFVND.',
        '..NFFFFN..',
        '.SSN..NSS.',
        '..........'
      ]
    },
    {
      name: 'OVERDRIVE FINALE',
      hint: 'Final wall. Get topside and STAY there. Good luck!',
      rows: [
        '.VSSSSSSV.',
        '.DFBBBBFD.',
        '.DNVVVVND.',
        '.SNVFFVNS.',
        '.FSNSSNSF.',
        '..NNNNNN..',
        '...N..N...',
        '..........'
      ]
    }
  ];

  window.BreakoutLevels = LEVELS;
})();
