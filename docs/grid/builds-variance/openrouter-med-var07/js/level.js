// Breakthrough — level.js: procedural brick grid generation
window.BT = window.BT || {};
BT.level = (function () {
  var U = BT.util;
  var BRICK_W = 64, BRICK_H = 26;
  var ROW_COLORS = ['#ff4d6d', '#ff9e3d', '#ffd23f', '#5ce65c', '#3fa7ff', '#a56cff', '#ff6ce7', '#4dd0e1'];
  var SPECIALS = ['explosive', 'armored', 'prism', 'shielded', 'phase'];
  var SPECIAL_LEVEL = { explosive: 0, armored: 3, prism: 6, shielded: 9, phase: 12 };

  // Generate a level: levelNum 0-based. Returns bricks array + metadata.
  function generate(levelNum, fieldW) {
    var cols = Math.max(5, Math.floor(fieldW / (BRICK_W + 4)));
    var gridW = cols * (BRICK_W + 4) - 4;
    var offX = (fieldW - gridW) / 2;
    BT.level.lastFieldW = function () { return fieldW; };
    var topOff = 90;
    var rows = Math.min(6 + Math.floor(levelNum / 2), 11);
    var bricks = [];
    var avail = ['standard'];
    for (var s = 0; s < SPECIALS.length; s++) {
      if (levelNum >= SPECIAL_LEVEL[SPECIALS[s]]) avail.push(SPECIALS[s]);
    }
    var spChance = Math.min(0.08 + levelNum * 0.025, 0.4);
    var specialChance = function (r) { return spChance * (1 + (r / rows) * 0.6); };

    // Weak-point gaps: pick a few gap columns to encourage breakthrough
    var gaps = [];
    var gapCount = Math.max(1, Math.min(3, Math.floor(cols / 5)));
    for (var g = 0; g < gapCount; g++) gaps.push(U.randInt(1, cols - 2));

    for (var r = 0; r < rows; r++) {
      for (var c = 0; c < cols; c++) {
        if (gaps.indexOf(c) >= 0 && r < rows - 1) continue; // vertical channels
        if (Math.random() < 0.08 && r > 0) continue; // small holes
        var type = 'standard';
        if (Math.random() < specialChance(r)) {
          type = U.pick(avail);
        }
        var b = {
          type: type,
          x: offX + c * (BRICK_W + 4),
          y: topOff + r * (BRICK_H + 5),
          w: BRICK_W, h: BRICK_H,
          col: c, row: r,
          color: ROW_COLORS[r % ROW_COLORS.length],
          alive: true,
          phaseOff: Math.random() * 6.283,
          dropIn: r * 0.06 + c * 0.015 + Math.random() * 0.05
        };
        if (type === 'shielded') {
          // shielded often as ceiling of pockets: place pairs
          b.color = '#7fe9ff';
        }
        bricks.push(b);
      }
    }
    // Meta: new special introduced this level?
    var newSpecial = null;
    for (var k = 0; k < SPECIALS.length; k++) {
      if (SPECIAL_LEVEL[SPECIALS[k]] === levelNum) newSpecial = SPECIALS[k];
    }
    return { bricks: bricks, rows: rows, cols: cols, newSpecial: newSpecial, topOff: topOff };
  }


  return { generate: generate, BRICK_W: BRICK_W, BRICK_H: BRICK_H, ROW_COLORS: ROW_COLORS,
           SPECIALS: SPECIALS, SPECIAL_LEVEL: SPECIAL_LEVEL };
})();