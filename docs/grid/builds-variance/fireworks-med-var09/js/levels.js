/* levels.js — hand-designed levels. Grid layout strings; legend:
 *  . empty   S standard   P phase   V speed   A angle   M mover   H shielded
 * Rows map top→bottom. Field is a normalized play area; main.js scales it. */
(function (root) {
  'use strict';

  var LEVELS = [
    {
      name: 'FIRST LIGHT',
      hint: 'Break through. Get above the wall.',
      rows: [
        'SSSSSSSSSSS',
        'SSSSSSSSSSS',
        'SSSSSSSSSSS',
        'SSSSSSSSSSS'
      ]
    },
    {
      name: 'FLUX',
      hint: 'Purple gates glow when they can be broken — time your shots.',
      rows: [
        'SPPSPPSPPSP',
        'SPSPSPSPSPS',
        'PPSPPSPSPPP',
        'SSSSSSSSSSS',
        '.SS.SSS.SS.'
      ]
    },
    {
      name: 'BREAKPOINT',
      hint: 'Orange gates need speed (chevrons). Green wedges need steep hits.',
      rows: [
        'SVVSVVVSVVS',
        'SAAASAAASAAS',
        'SVSVSVSVSVSV',
        'AAASSSSSSAAA',
        'SSSMMMMMSSSS'
      ]
    },
    {
      name: 'MIRRORWALL',
      hint: 'Gold caps block from above — flank them. Movers drift; lead your shot.',
      rows: [
        'HHSSHSSHSSHSS',
        'HSHSHSHSHSHSH',
        'HSSHSSHSSHHSS',
        'MMMSSSSSSSMMM',
        'SPSPSVSVSPSPS',
        'SSSSSSSSSSSSS'
      ]
    },
    {
      name: 'OVERCLOCK',
      hint: 'Everything at once. Earn your overdrive.',
      rows: [
        'VVPAASVVPAAS',
        'PHSASHSPASHP',
        'AMVMSVMVMSMA',
        'SHVAPPSAVHS',
        'MAMVMVMVMAMA',
        'SSPSSVSPSPSS'
      ]
    }
  ];

  function parse(level) {
    var out = [];
    for (var r = 0; r < level.rows.length; r++) {
      var row = level.rows[r];
      var cells = [];
      for (var c = 0; c < row.length; c++) {
        var ch = row[c];
        var type = null;
        if (ch === 'S') type = 'standard';
        else if (ch === 'P') type = 'phase';
        else if (ch === 'V') type = 'speed';
        else if (ch === 'A') type = 'angle';
        else if (ch === 'M') type = 'mover';
        else if (ch === 'H') type = 'shielded';
        cells.push(type);
      }
      out.push(cells);
    }
    return out;
  }

  var API = {
    LEVELS: LEVELS,
    parse: parse,
    count: function () { return LEVELS.length; }
  };

  var BO = root.BO = root.BO || {};
  for (var k in API) BO[k] = API[k];
  if (typeof module !== 'undefined' && module.exports) module.exports = API;
})(typeof window !== 'undefined' ? window : globalThis);
