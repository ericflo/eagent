// Brick field: creation, per-type behaviour, collision queries.
import { BRICK, FIELD, COLORS } from './constants.js';
import { BRICK_CHARS, LEVELS } from './levels.js';

let _id = 0;
export class Brick {
  constructor(type, col, row, x, y) {
    this.id = ++_id;
    this.type = type;
    this.col = col; this.row = row;
    this.w = BRICK.w; this.h = BRICK.h;
    this.x = x; this.y = y; // top-left
    this.alive = true;
    this.flash = 0;              // white hit flash
    this.shake = 0;              // tiny offset wobble
    this.hp = type === 'ARMOR' ? 3 : 1;
    // per-type state
    this.phaseSolid = true; this.phaseTimer = Math.random() * 4; // desync
    this.moverDir = (col % 2 === 0) ? 1 : -1;
    this.moverT = Math.random() * Math.PI * 2;
    this.baseX = x;
    this.regenTimer = 0; this.regenWaiting = false;
    this.hitFlashT = 0;
  }
  get cx() { return this.x + this.w / 2; }
  get cy() { return this.y + this.h / 2; }
}

export class BrickField {
  constructor(levelIndex) {
    this.bricks = [];
    this.grid = new Map(); // "c,r" -> brick
    this.level = LEVELS[levelIndex % LEVELS.length];
    this.levelIndex = levelIndex;
    const rows = this.level.rows;
    const totalW = BRICK.cols * BRICK.w + (BRICK.cols - 1) * BRICK.gapX;
    const ox = (FIELD.w - totalW) / 2;
    for (let r = 0; r < rows.length; r++) {
      const line = rows[r];
      for (let c = 0; c < BRICK.cols; c++) {
        const ch = line[c] || '.';
        if (ch === '.') continue;
        const type = BRICK_CHARS[ch] || 'STD';
        const x = ox + c * (BRICK.w + BRICK.gapX);
        const y = BRICK.top + r * (BRICK.h + BRICK.gapY);
        const b = new Brick(type, c, r, x, y);
        this.bricks.push(b);
        this.grid.set(c + ',' + r, b);
      }
    }
  }
  neighbors(b) {
    const out = [];
    for (const [dc, dr] of [[1,0],[-1,0],[0,1],[0,-1]]) {
      const n = this.grid.get((b.col + dc) + ',' + (b.row + dr));
      if (n && n.alive) out.push(n);
    }
    return out;
  }
  get aliveCount() { return this.bricks.reduce((n, b) => n + (b.alive && b.type !== 'UNBREAK' ? 1 : 0), 0); }
  get breakableLeft() { return this.aliveCount; }

  update(dt, field, fx) {
    for (const b of this.bricks) {
      if (b.flash > 0) b.flash = Math.max(0, b.flash - dt * 6);
      if (!b.alive) {
        // regen logic
        if (b.type === 'REGEN' && b.regenWaiting) {
          b.regenTimer -= dt;
          if (b.regenTimer <= 0) { this._tryRegrow(b, fx); }
        }
        continue;
      }
      if (b.type === 'PHASE') {
        b.phaseTimer += dt;
        const cyc = b.phaseTimer % 4.2;
        b.phaseSolid = cyc < 2.8;
      } else if (b.type === 'MOVER') {
        b.moverT += dt * 1.1;
        b.x = b.baseX + Math.sin(b.moverT) * 34;
      }
    }
  }
  _tryRegrow(b, fx) {
    // don't regrow if all neighbours are already dead (wall around cleared)
    const nbs = [];
    for (const [dc, dr] of [[1,0],[-1,0],[0,1],[0,-1]]) {
      const n = this.grid.get((b.col + dc) + ',' + (b.row + dr));
      if (n && n.alive) nbs.push(n);
    }
    if (nbs.length >= 2) {
      b.alive = true; b.regenWaiting = false; b.hp = 1;
      b.flash = 1;
      if (fx) fx.regrow(b);
    } else {
      b.regenWaiting = false; // gone for good
    }
  }
  // returns list of bricks whose AABBs overlap the segment path of a circle
  query(x0, y0, x1, y1, r) {
    const res = [];
    const minX = Math.min(x0, x1) - r, maxX = Math.max(x0, x1) + r;
    const minY = Math.min(y0, y1) - r, maxY = Math.max(y0, y1) + r;
    for (const b of this.bricks) {
      if (!b.alive) continue;
      if (b.type === 'PHASE' && !b.phaseSolid) continue;
      if (b.x + b.w < minX || b.x > maxX || b.y + b.h < minY || b.y > maxY) continue;
      res.push(b);
    }
    return res;
  }
  // circle-vs-AABB overlap test
  static circleHit(b, cx, cy, r) {
    const nx = Math.max(b.x, Math.min(cx, b.x + b.w));
    const ny = Math.max(b.y, Math.min(cy, b.y + b.h));
    const dx = cx - nx, dy = cy - ny;
    return dx * dx + dy * dy <= r * r;
  }
}
