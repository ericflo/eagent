// src/game/fallback.js — owned by core worker.
// Minimal built-in brick + level generator used ONLY if src/game/bricks.js or
// src/game/levels.js fail to load / export what we need. Keeps the game
// playable even while sibling modules are still being written.

const BRICK_MARGIN = 30, BRICK_GAP = 10, BRICK_W = 85, BRICK_H = 32, BRICK_TOP = 260;
const COL_STEP = BRICK_W + BRICK_GAP, ROW_STEP = BRICK_H + BRICK_GAP, COLS = 10;

const COLORS = ['#5ee6ff', '#ff5ec8', '#b0ff5e', '#ffd15e', '#8a7bff'];

export class FallbackBrick {
  constructor(col, row) {
    this.col = col; this.row = row;
    this.x = BRICK_MARGIN + col * COL_STEP;
    this.y = BRICK_TOP + row * ROW_STEP;
    this.w = BRICK_W; this.h = BRICK_H;
    this.type = 'normal';
    this.alive = true;
    this.hp = 1;
    this.color = COLORS[(col + row) % COLORS.length];
    this.flashT = 0;
  }
  get cx() { return this.x + this.w / 2; }
  get cy() { return this.y + this.h / 2; }
  flash() { this.flashT = 1; }
  update(dt) { if (this.flashT > 0) this.flashT = Math.max(0, this.flashT - dt * 3); }
  draw(g) {
    if (!this.alive) return;
    g.save();
    g.translate(this.x, this.y);
    const glow = 0.5 + this.flashT * 0.5;
    g.fillStyle = this.color;
    g.globalAlpha = glow;
    g.fillRect(1, 1, this.w - 2, this.h - 2);
    g.globalAlpha = 1;
    g.strokeStyle = 'rgba(255,255,255,0.35)';
    g.strokeRect(1, 1, this.w - 2, this.h - 2);
    g.restore();
  }
  hitBy(ball, hit, game) {
    this.alive = false;
    return { result: 'break', score: 100, sfx: 'brick', fx: 'normal' };
  }
}

export function buildFallbackLevel(i) {
  const rows = 5 + Math.min(4, ((i - 1) / 2) | 0);
  const bricks = [];
  for (let r = 0; r < rows; r++) {
    for (let c = 0; c < COLS; c++) {
      if ((c + r + i) % 7 === 0) continue; // a few gaps so it's not a solid wall
      bricks.push(new FallbackBrick(c, r));
    }
  }
  return {
    bricks,
    name: `Fallback Level ${i}`,
    subtitle: 'bricks.js / levels.js unavailable — built-in grid',
    ballSpeed: 720,
    tint: '#2a2140',
    spawn: { x: 500, y: 1300 },
  };
}
