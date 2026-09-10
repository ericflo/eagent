// util.js — math helpers, safe random, color/fmt utilities, state querier.

const TAU = Math.PI * 2;

const clamp  = (v, a, b) => v < a ? a : (v > b ? b : v);
const lerp   = (a, b, t) => a + (b - a) * t;
const dist2  = (ax, ay, bx, by) => { const dx = ax - bx, dy = ay - by; return dx * dx + dy * dy; };
const rnd    = (a = 1, b) => b === undefined ? Math.random() * a : a + Math.random() * (b - a);
const chance = p => Math.random() < p;

// Deterministic-ish, glitch-free shuffle
function shuffle(arr) {
  for (let i = arr.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    [arr[i], arr[j]] = [arr[j], arr[i]];
  }
  return arr;
}

// Modulo that handles negatives like a cyclist
function mod(n, m) { return ((n % m) + m) % m; }

// Round to a multiple (for angular snapping), wrapping around PI
function snapAngle(a, step) {
  const half = step / 2;
  return Math.round(a / step) * step;
}

// Pick a weight from a weighted table {weight: value}
function pickWeighted(table) {
  let total = 0;
  for (const k in table) total += Number(k);
  let r = Math.random() * total;
  for (const k in table) {
    r -= Number(k);
    if (r <= 0) return table[k];
  }
  const keys = Object.keys(table);
  return table[keys[keys.length - 1]];
}

const fmt = n => n.toLocaleString('en-US');

// Named palette
const PALETTE = {
  cyan:   '#5ce1ff',
  yellow: '#ffd23f',
  pink:   '#ff7de3',
  orange: '#ff9f43',
  green:  '#7dffb2',
  violet: '#b08cff',
  red:    '#ff5d73',
  ice:    '#cfeaff',
};

// Hex with alpha
function rgba(hex, a) {
  const r = parseInt(hex.slice(1, 3), 16);
  const g = parseInt(hex.slice(3, 5), 16);
  const b = parseInt(hex.slice(5, 7), 16);
  return `rgba(${r},${g},${b},${a})`;
}

// State snapshot for the narrator / debugging / headless verification.
function snapshot(game) {
  if (!game) return null;
  const balls = [...game.balls].map(b => ({
    x: +b.x.toFixed(1), y: +b.y.toFixed(1),
    vx: +b.vx.toFixed(1), vy: +b.vy.toFixed(1),
    r: b.r, kind: b.kind,
    owner: b.owner || null,
  }));
  const bricks = game.bricks.map(b => ({
    x: b.x, y: b.y, w: b.w, h: b.h,
    kind: b.kind, hp: b.hp, alive: b.alive,
    spin: b.kind === 'spinner' ? +(b.spin || 0).toFixed(2) : undefined,
  }));
  const fxs = playerFX(game, {});
  return {
    t: +game.t.toFixed(2),
    level: game.levelIndex + 1,
    phase: game.phase,
    state: game.state,
    score: game.score,
    combo: game.combo,
    lives: game.lives,
    paddle: {
      x: +game.paddle.x.toFixed(1), y: +game.paddle.y.toFixed(1),
      w: game.paddle.w, vy: game.paddle.vy,
      right: game.input.right, left: game.input.left, up: game.input.up, down: game.input.down,
    },
    balls,
    bricksAlive: game.aliveCount(),
    bricksTotal: game.bricks.length,
    fx: fxs,
  };
}

// A stub used by snapshot to list active fxs (game defines its own).
function playerFX(game, out) {
  const fx = [];
  const vis = (p, a) => ({ name: p, alpha: Math.min(1, +a.toFixed(2)) });
  if (game.bonusAtkDur > 0) fx.push(vis('bonus_atk', game.bonusAtkDur / BONUS_ATK_DUR));
  if (game.multiplier > 1)   fx.push(vis('multiplier', (game.multiplier - 1) / MAX_MULTIPLIER));
  if (game.fever)            fx.push(vis('fever', 1));
  out.type = 'snapshot';
  return fx;
}
