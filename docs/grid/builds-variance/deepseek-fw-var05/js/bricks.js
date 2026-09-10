// bricks.js — brick & special-ball definitions and behaviors.

// Brick kinds:
//  standard  — normal one-hit brick
//  sturdy    — needs more hits (we conceded this is occasionally fine; keep rare & telegraphed)
//  angled    — must be hit with a ball whose |angle from horizontal| is under a threshold
//  spinner   — oscillates side to side; dodging it is the fun
//  metal     — indestructible, acts as a wall (comes with both-spinners setups)
//  gem       — high value, fragile, gives a big bonus + extra ball chance
//  bomb      — explodes on arrival (removed from field), damages nearby bricks
//  multi     — splits into smaller bricks when hit once
//  unbreak   — cannot be broken in this level (backdrop), counts as wall

const BRICK_COLORS = {
  standard: '#ff7de3',
  sturdy:   '#b08cff',
  angled:   '#5ce1ff',
  spinner:  '#ffd23f',
  metal:    '#9aa5c4',
  gem:      '#7dffb2',
  bomb:     '#ff5d73',
  multi:    '#ff9f43',
  unbreak:  '#4a5478',
  gust:     '#c9ff6b',
  wall:     '#6b7699',
};

const BRICK_BASE_H = 22;

function makeBrick(kind, x, y, w, h, opts = {}) {
  const b = {
    kind, x, y, w, h,
    alive: true,
    hp: 1,
    text: '',
    spin: 0,          // spinner phase
    spinSpeed: 0,
    spinRange: 0,
    spinBase: 0,
    color: BRICK_COLORS[kind] || BRICK_COLORS.standard,
    special: null,    // 'bomb' / 'multi'
    extraScores: opts.extraScores || 0,
    gems: opts.gems || 0,
    oneWay: false,
    threshold: opts.threshold || 1.12, // gust brick speed requirement
  };
  switch (kind) {
    case 'standard': b.hp = 1; break;
    case 'sturdy': b.hp = 2; b.text = '2'; break;
    case 'angled': b.hp = 1; b.oneWay = true; break;
    case 'gust': b.hp = 1; b.text = '»'; break;
    case 'wall':
    case 'unbreak':
    case 'metal': b.hp = Infinity; break;
    case 'spinner': {
      b.hp = 1;
      b.spinBase = x;
      b.spinRange = opts.range || 26;
      b.spinSpeed = opts.speed || 2.2;
      b.color = '#ffd23f';
      break;
    }
    case 'gem': b.hp = 1; b.extraScores = opts.extraScores || 1500; b.gems = 1; break;
    case 'bomb': b.hp = 1; b.special = 'bomb'; break;
    case 'multi': b.hp = 1; b.special = 'multi'; break;
    case 'unbreak': b.hp = Infinity; break;
    default: break;
  }
  return b;
}

// helper: is the brick a wall (ball should not pass through)?
function brickIsWall(k) {
  return k === 'metal' || k === 'unbreak';
}

// helper: does this brick react to angle/speed requirements?
function brickNeedsAngle(k) { return k === 'angled'; }

// Special ball kinds:
//  normal   — plain
//  fire     — burns through standard bricks on contact, extra velocity
//  heavy    — smashes rare bricks incl. metal weak spots (still bounces)
//  ghost    — teleports a short distance, phase-shifts through a brick rarely
//  multi    — splits on first paddle hit into 2 (then normal)
const BALL_COLORS = {
  normal: '#ffffff',
  fire:   '#ff9f43',
  heavy:  '#ff5d73',
  ghost:  '#b08cff',
  multi:  '#ffd23f',
};

const BALL_KIND_SEVERITY = {
  normal: 1,
  fire:   1.4,
  heavy:  1.6,
  ghost:  1.0,
  multi:  1.2,
};

// ---- scoring ----
function brickScore(kind, combo) {
  const base = { standard: 100, sturdy: 200, angled: 250, spinner: 300, gem: 1500, multi: 120, metal: 0, unbreak: 0, bomb: 250 }[kind] || 100;
  return base * combo;
}

window.BrickLib = { makeBrick, brickIsWall, brickNeedsAngle, brickScore, BRICK_COLORS, BALL_COLORS, BALL_KIND_SEVERITY, BRICK_BASE_H };
