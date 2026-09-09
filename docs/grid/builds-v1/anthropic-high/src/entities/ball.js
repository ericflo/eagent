// entities/ball.js
import { clamp, setLength } from '../util.js';
import { speedTier, speedTierIndex } from '../physics.js';

export const BASE_RADIUS = 8;

let nextId = 1;

export class Ball {
  constructor(x = 0, y = 0) {
    this.id = nextId++;
    this.x = x;
    this.y = y;
    this.vx = 0;
    this.vy = 0;
    this.r = BASE_RADIUS;
    this.baseR = BASE_RADIUS;
    this.dead = false;
    this.stuck = true;          // stuck to paddle before launch / magnet
    this.stickOffset = 0;       // x offset from paddle center while stuck
    this.stickTimer = 0;        // auto-launch countdown
    this.trail = [];            // [{x,y,age}]
    this.flatTime = 0;          // time spent with near-zero vy (anti-stall)
    this.nearMiss = Infinity;   // closest horizontal gap to the paddle on the way past
    this.overtop = false;
    this.hue = 190;
    this.spawnFlash = 0.25;
    // per-ball power flags (set/cleared by game power-up system)
    this.fire = 0;
    this.heavy = 0;
    this.ghost = 0;
    this.magnet = 0;
  }

  get speed() {
    return Math.hypot(this.vx, this.vy);
  }
  get tier() {
    return speedTier(this.speed);
  }
  get tierIndex() {
    return speedTierIndex(this.speed);
  }

  setSpeed(s) {
    const [vx, vy] = setLength(this.vx, this.vy, s);
    this.vx = vx;
    this.vy = vy;
  }

  clampSpeed(min, max) {
    const s = this.speed;
    if (s < 1e-6) {
      this.vx = 0;
      this.vy = -min;
      return;
    }
    const t = clamp(s, min, max);
    if (t !== s) this.setSpeed(t);
  }

  updateTrail(dt) {
    this.trail.unshift({ x: this.x, y: this.y, a: 1 });
    const maxLen = this.fire > 0 ? 26 : 16;
    if (this.trail.length > maxLen) this.trail.length = maxLen;
    for (let i = 0; i < this.trail.length; i++) {
      this.trail[i].a -= dt * 3.2;
    }
    while (this.trail.length && this.trail[this.trail.length - 1].a <= 0) this.trail.pop();
  }
}
