// ---------------------------------------------------------------------------
// bricks.js — brick entity + type behaviours
// ---------------------------------------------------------------------------

import { TYPE_INFO, brickRect } from './levels.js';
import { STEEP, SPEED_GATE } from './config.js';
import { rand, pick, TAU } from './utils.js';

export const PHASE_PERIOD = 1.7;   // solid 1.15s / ghost 0.55s

export class Brick {
  constructor(type, col, row, phaseOffset = 0) {
    this.type = type;
    this.col = col;
    this.row = row;
    const r = brickRect(col, row);
    this.x = r.x; this.y = r.y; this.w = r.w; this.h = r.h;
    this.info = TYPE_INFO[type] || { name: 'standard', pts: 100 };
    this.alive = true;
    // breakup state
    this.breaking = 0;          // 0..1 break animation
    this.shakeT = 0;
    this.flash = 0;             // hit flash
    this.deny = 0;              // failed-condition feedback timer
    this.denyMsg = '';
    // phase
    this.phaseOff = phaseOffset;
    this.solid = true;
    this.solidT = 0;
    // spinner
    this.spin = rand(TAU);
    this.spinV = (Math.random() < 0.5 ? -1 : 1) * rand(1.6, 3.2);
    // unstable armed pulse
    this.pulse = rand(TAU);
    // shatter fragments (filled on break)
    this.frags = null;
    this.ignite = 0;            // speed-brick glow 0..1
  }

  get cx() { return this.x + this.w / 2; }
  get cy() { return this.y + this.h / 2; }

  get speedGate() { return SPEED_GATE; }
  get steepGate() { return STEEP; }

  /** is the brick currently hittable? (phase ghost = pass-through) */
  isSolid(t) {
    if (this.type !== 'P') return true;
    const ph = (t + this.phaseOff) % PHASE_PERIOD;
    return ph < 1.15;
  }

  /** Can the ball break this brick given impact velocity? */
  canBreak(vx, vy, speed, ballPower) {
    if (ballPower === 'fire' || ballPower === 'pierce') return true;
    const steep = speed > 1 && Math.abs(vx / vy) <= STEEP;
    switch (this.type) {
      case '1': case '2': case 'U': case 'W':
        return true;
      case 'A': case 'B':
        return steep;
      case 'S':
        return speed >= SPEED_GATE;
      case 'P':
        return true; // only reachable while solid
      case 'X':
        return speed >= SPEED_GATE && steep;
      default:
        return true;
    }
  }

  /** reason string when unbreakable (for feedback) */
  failReason(vx, vy, speed) {
    const steep = speed > 1 && Math.abs(vx / vy) <= STEEP;
    if (this.type === 'A' || this.type === 'B') return 'STEEP ONLY';
    if (this.type === 'S') return 'NEED SPEED';
    if (this.type === 'X') return (!steep && speed < SPEED_GATE) ? 'SPEED + STEEP' : (steep ? 'NEED SPEED' : 'STEEP ONLY');
    return '';
  }

  breakInto() {
    const frags = [];
    const n = 7;
    for (let i = 0; i < n; i++) {
      frags.push({
        x: this.x + rand(0, this.w), y: this.y + rand(0, this.h),
        vx: rand(-190, 190), vy: rand(-260, 60),
        w: rand(5, 13), h: rand(4, 9),
        rot: rand(TAU), vr: rand(-9, 9),
      });
    }
    return frags;
  }
}
