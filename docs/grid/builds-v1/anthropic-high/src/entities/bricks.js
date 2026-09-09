// entities/bricks.js — brick types, per-kind hit rules, and grid → brick-list building.
//
// Actions a hit can produce (interpreted by game.js -> physics.js):
//   'break'       destroy the brick AND bounce the ball
//   'breakThru'   destroy the brick, ball keeps going (fireball)
//   'bounce'      brick survives, ball reflects
//   'pass'        no interaction at all (ghost-phase, ghostball rising)

import { clamp } from '../util.js';

export const COLS = 11;
export const ROWS_MAX = 14;

/** Each kind: color palette, score, breakable (counts toward level clear), hit rule. */
export const BRICK_KINDS = {
  normal: {
    char: 'n',
    label: 'Normal',
    hue: 205,
    score: 100,
    breakable: true,
    hit: () => ({ action: 'break', sfx: 'break' }),
  },

  angle: {
    // Prism (vertical): needs a steep approach — hit from (nearly) straight above/below.
    char: 'a',
    label: 'Prism ▼',
    hue: 280,
    score: 220,
    breakable: true,
    hit: (ball) => {
      const steep = Math.abs(ball.vy) > Math.abs(ball.vx);
      return steep
        ? { action: 'break', sfx: 'breakPrism' }
        : { action: 'bounce', sfx: 'ping', deny: true };
    },
  },

  angleH: {
    // Prism (horizontal): needs a shallow, skimming approach — natural when running
    // along the top of the field.
    char: 'h',
    label: 'Prism ▶',
    hue: 315,
    score: 220,
    breakable: true,
    hit: (ball) => {
      const shallow = Math.abs(ball.vx) > Math.abs(ball.vy);
      return shallow
        ? { action: 'break', sfx: 'breakPrism' }
        : { action: 'bounce', sfx: 'ping', deny: true };
    },
  },

  speed: {
    // Armor: only a fast ball cracks it. Smash upward with the paddle.
    char: 's',
    label: 'Armor',
    hue: 20,
    score: 260,
    breakable: true,
    hit: (ball) => (ball.tierIndex >= 2
      ? { action: 'break', sfx: 'breakArmor' }
      : { action: 'bounce', sfx: 'clank', deny: true }),
  },

  slow: {
    // Glass: shatters only when the ball is calm; a fast ball just rings it.
    char: 'g',
    label: 'Glass',
    hue: 170,
    score: 240,
    breakable: true,
    hit: (ball) => (ball.tierIndex <= 1
      ? { action: 'break', sfx: 'breakGlass' }
      : { action: 'bounce', sfx: 'ring', deny: true }),
  },

  ghost: {
    // Phases solid 2s / transparent 1.5s. Only collides while solid.
    char: 'G',
    label: 'Phase',
    hue: 250,
    score: 180,
    breakable: true,
    hit: (ball, ctx) => (ctx.brick.solidity > 0.5
      ? { action: 'break', sfx: 'breakGhost' }
      : { action: 'pass' }),
  },

  topOnly: {
    // Cap: only breaks when struck from above (ball travelling downward).
    char: 't',
    label: 'Cap',
    hue: 55,
    score: 340,
    breakable: true,
    hit: (ball) => (ball.vy > 0
      ? { action: 'break', sfx: 'breakCap' }
      : { action: 'bounce', sfx: 'ping', deny: true }),
  },

  bomb: {
    char: 'b',
    label: 'Bomb',
    hue: 0,
    score: 200,
    breakable: true,
    hit: () => ({ action: 'break', sfx: 'boom', explode: true }),
  },

  powerup: {
    char: 'p',
    label: 'Capsule',
    hue: 130,
    score: 150,
    breakable: true,
    hit: () => ({ action: 'break', sfx: 'break', drop: true }),
  },

  boost: {
    // Launch pad: struck from below it breaks and slings the ball straight up, fast.
    char: '^',
    label: 'Launch',
    hue: 95,
    score: 200,
    breakable: true,
    hit: (ball) => (ball.vy < 0
      ? { action: 'break', sfx: 'boost', boost: true }
      : { action: 'break', sfx: 'break' }),
  },

  steel: {
    char: '#',
    label: 'Steel',
    hue: 210,
    score: 0,
    breakable: false,
    indestructible: true,
    hit: () => ({ action: 'bounce', sfx: 'steel', deny: true }),
  },
};

export const CHAR_TO_KIND = (() => {
  const m = {};
  for (const [k, v] of Object.entries(BRICK_KINDS)) m[v.char] = k;
  return m;
})();

export class Brick {
  constructor(kind, x, y, w, h, col, row) {
    this.kind = kind;
    this.def = BRICK_KINDS[kind];
    this.x = x;
    this.y = y;
    this.w = w;
    this.h = h;
    this.col = col;
    this.row = row;
    this.dead = false;
    // Plain bricks get a gentle per-row hue ramp (classic rainbow rows, restrained
    // enough that the special kinds still read as special).
    this.hue = kind === 'normal' ? 190 + (row % 9) * 8 : this.def.hue;
    this.hitFlash = 0;
    this.denyFlash = 0;
    this.shake = 0;
    this.phase = 0;             // ghost timer
    this.solidity = 1;          // 0..1, ghost only
    this.spawnT = 0;            // 0..1 entrance animation
    this.assist = false;        // last-brick helper: render may pulse these
    this.downgraded = false;    // condition brick that was softened to a normal one
    this.wobble = Math.random() * Math.PI * 2;
  }

  get breakable() {
    return !!this.def.breakable;
  }

  get cx() { return this.x + this.w / 2; }
  get cy() { return this.y + this.h / 2; }

  /** Can the ball collide with this brick at all right now? */
  collidable() {
    if (this.dead) return false;
    if (this.kind === 'ghost' && this.solidity <= 0.5) return false;
    return true;
  }

  update(dt) {
    if (this.spawnT < 1) this.spawnT = Math.min(1, this.spawnT + dt * 2.2);
    if (this.hitFlash > 0) this.hitFlash -= dt * 3.5;
    if (this.denyFlash > 0) this.denyFlash -= dt * 2.6;
    if (this.shake > 0) this.shake -= dt * 8;
    if (this.kind === 'ghost') {
      this.phase += dt;
      const period = 3.5; // 2s solid, 1.5s ghosted
      const t = this.phase % period;
      // smooth cross-fade at the boundaries
      if (t < 2) this.solidity = t < 1.8 ? 1 : 1 - (t - 1.8) / 0.2;
      else this.solidity = t < 3.3 ? 0 : (t - 3.3) / 0.2;
      this.solidity = clamp(this.solidity, 0, 1);
    }
  }

  hit(ball, ctx) {
    return this.def.hit(ball, ctx);
  }
}

/**
 * Geometry of the brick field inside a playfield.
 * `field` = {left, top, right, bottom} of the playfield.
 */
export function brickMetrics(field) {
  const gutter = 28;                       // side lanes so the ball can climb the edges
  const usable = field.right - field.left - gutter * 2;
  const w = usable / COLS;
  const h = 22;
  const headroom = 140;                    // ≈ 6 brick rows of open sky under the top wall
  return { gutter, w, h, originX: field.left + gutter, originY: field.top + headroom };
}

/**
 * Build a brick list from an array of strings (grid DSL). Unknown chars are ignored.
 */
export function buildLevel(rows, field) {
  const m = brickMetrics(field);
  const bricks = [];
  for (let r = 0; r < rows.length && r < ROWS_MAX; r++) {
    const line = rows[r];
    for (let c = 0; c < COLS; c++) {
      const ch = line[c] || '.';
      if (ch === '.' || ch === ' ') continue;
      const kind = CHAR_TO_KIND[ch];
      if (!kind) continue;
      const b = new Brick(
        kind,
        m.originX + c * m.w + 1,
        m.originY + r * m.h + 1,
        m.w - 2,
        m.h - 2,
        c,
        r,
      );
      b.spawnT = 0;
      bricks.push(b);
    }
  }
  return bricks;
}

/** Neighbours within Chebyshev radius `rad` in grid space. */
export function neighborsOf(bricks, brick, rad = 1) {
  const out = [];
  for (const b of bricks) {
    if (b === brick || b.dead) continue;
    if (Math.abs(b.col - brick.col) <= rad && Math.abs(b.row - brick.row) <= rad) out.push(b);
  }
  return out;
}

export function countBreakable(bricks) {
  let n = 0;
  for (const b of bricks) if (!b.dead && b.breakable) n++;
  return n;
}

/** Y of the top edge of the highest surviving brick (Infinity when none). */
export function topOfBricks(bricks) {
  let top = Infinity;
  for (const b of bricks) {
    if (b.dead) continue;
    if (b.y < top) top = b.y;
  }
  return top;
}
