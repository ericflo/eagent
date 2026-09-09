// ---------------------------------------------------------------------------
// powerups.js — capsule drops
// ---------------------------------------------------------------------------

import { W, POWERUP } from './config.js';
import { rand, pick } from './utils.js';

// id: internal key
export const PU = {
  multi:   { label: '3X',   name: 'MULTIBALL',    color: '#ffd166', icon: 'multi' },
  fire:    { label: 'FIRE', name: 'FIREBALL',     color: '#ff5c39', icon: 'fire' },
  pierce:  { label: 'P',    name: 'PIERCE BALL',  color: '#b8f742', icon: 'pierce' },
  giant:   { label: 'BIG',  name: 'GIANT BALL',   color: '#c084fc', icon: 'giant' },
  slow:    { label: 'SLOW', name: 'SLOW-MO',      color: '#67e8f9', icon: 'slow' },
  magnet:  { label: 'MAG',  name: 'MAGNET PADDLE',color: '#f472b6', icon: 'magnet' },
  sticky:  { label: 'MAG',  name: 'MAGNET PADDLE',color: '#f472b6', icon: 'magnet' }, // alias
  burst:   { label: 'ZOOM', name: 'SPEED BURST',  color: '#fbbf24', icon: 'burst' },
  wide:    { label: 'WIDE', name: 'WIDE PADDLE',  color: '#4ade80', icon: 'wide' },
  life:    { label: '1UP',  name: 'EXTRA LIFE',   color: '#ff6b8a', icon: 'life' },
};

export const DROP_TABLE = [
  'multi', 'multi', 'multi',
  'fire', 'fire',
  'pierce',
  'giant', 'slow', 'magnet', 'burst', 'wide',
  'life',
];

export class Powerup {
  constructor(x, y, id) {
    this.id = id;
    this.x = x; this.y = y;
    this.vx = rand(-40, 40);
    this.vy = rand(-60, -10);
    this.r = POWERUP.r;
    this.t = 0;
    this.dead = false;
  }

  update(dt, speedScale) {
    this.t += dt;
    this.vy += 300 * dt;
    this.vy = Math.min(this.vy, POWERUP.fall * speedScale + 130);
    this.x += this.vx * dt;
    this.y += this.vy * dt;
    if (this.x < this.r) { this.x = this.r; this.vx = Math.abs(this.vx); }
    if (this.x > W - this.r) { this.x = W - this.r; this.vx = -Math.abs(this.vx); }
    return this.y > 9999; // never leaves except by catch
  }
}

export function rollDrop(unlockedWeights = null) {
  return pick(DROP_TABLE);
}
