'use strict';
// ---------------------------------------------------------------------------
// util.js — math helpers, RNG, easing, formatting
// ---------------------------------------------------------------------------

const TAU = Math.PI * 2;
const clamp = (v, a, b) => v < a ? a : (v > b ? b : v);
const lerp = (a, b, t) => a + (b - a) * t;
const dist2 = (x1, y1, x2, y2) => {
  const dx = x2 - x1, dy = y2 - y1;
  return dx * dx + dy * dy;
};

// Small seeded RNG so level layouts are reproducible per level number.
function mulberry32(seed) {
  let a = seed >>> 0;
  return function () {
    a |= 0; a = (a + 0x6D2B79F5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const easeOutCubic = t => 1 - Math.pow(1 - t, 3);
const easeInCubic = t => t * t * t;
const easeOutBack = t => { const c = 1.70158; const u = t - 1; return 1 + (c + 1) * u * u * u + c * u * u; };
const easeOutQuint = t => 1 - Math.pow(1 - t, 5);

function fmtScore(n) {
  return Math.floor(n).toLocaleString('en-US');
}

// Grid snapping for level authoring: "C3" → column 3.
function colLetterToIndex(s) {
  if (!s) return 0;
  let n = 0;
  for (const c of s.toUpperCase()) {
    if (c >= 'A' && c <= 'Z') n = n * 26 + (c.charCodeAt(0) - 64);
  }
  return n - 1;
}

// Parse compact level strings, one row per line:
//   letters map to brick types, '.' = empty, digits 0-9 = powerup drop codes.
// Supported: b=basic, s=steel(angle), v=velocity/pressure, g=regen/phase,
//            m=mover, r=redirect, k=key(regen), d=death/regen, p=prism,
//            w=switch, h=hidden/switch, t=trigger, u=unlock, x=regen-reset
function parseLevelRows(rows) {
  const grid = [];
  const POWER_LETTER = {
    '1': 'wide', '2': 'multi', '3': 'laser', '4': 'slow',
    '5': 'fire', '6': 'catch', '7': 'life', '8': 'shield',
  };
  const TYPE = {
    b: 'basic', s: 'steel', v: 'velocity', g: 'regen', m: 'mover',
    r: 'redirect', k: 'key', d: 'death', p: 'prism', w: 'switch',
    h: 'hidden', t: 'trigger', u: 'unlock', x: 'reset',
  };
  for (const row of rows) {
    const line = [];
    for (const ch of row) {
      if (ch === '.' || ch === ' ') { line.push(null); continue; }
      const t = TYPE[ch];
      if (t) line.push({ type: t, drop: null });
      else if (POWER_LETTER[ch]) line.push({ type: 'basic', drop: POWER_LETTER[ch] });
      else line.push(null);
    }
    grid.push(line);
  }
  return grid;
}
