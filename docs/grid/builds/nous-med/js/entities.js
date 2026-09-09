// Entities: bricks, paddle, balls, capsules.
import { BRICK_W, BRICK_H, PADDLE_W, PADDLE_H, BALL_R, FIELD_TOP } from './constants.js';

export class Brick {
  constructor(type, col, row, colX, rowY) {
    this.type = type; this.col = col; this.row = row;
    this.x = colX; this.y = rowY; this.w = BRICK_W; this.h = BRICK_H;
    this.alive = true;
    this.phase = 0;          // ghost phase timer
    this.solid = type !== 'G'; // ghost solid flag
    this.breaking = 0;       // shatter animation timer
    this.hitFx = 0;          // clang/tink flash
    this.pull = type === 'X';
  }
  get cx() { return this.x + this.w / 2; }
  get cy() { return this.y + this.h / 2; }
}

export class Paddle {
  constructor() {
    this.w = PADDLE_W; this.h = PADDLE_H;
    this.x = 450 - PADDLE_W / 2; this.y = 900;
    this.vx = 0; this.vy = 0;
    this.baseW = PADDLE_W;
    this.magnet = 0; // 0 none, 1 holding
    this.indicator = 0;
  }
  get cx() { return this.x + this.w / 2; }
  get cy() { return this.y + this.h / 2; }
}

export class Ball {
  constructor(x, y, vx, vy) {
    this.x = x; this.y = y; this.vx = vx; this.vy = vy;
    this.r = BALL_R;
    this.trail = [];
    this.fire = 0; this.phase = 0;
    this.stuck = false; // magnet-held
    this.stuckDx = 0;
  }
  get speed() { return Math.hypot(this.vx, this.vy); }
}

export class Capsule {
  constructor(x, y, kind) {
    this.x = x; this.y = y; this.kind = kind;
    this.vy = 140; this.w = 40; this.h = 24; this.t = 0;
  }
}
