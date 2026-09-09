// src/game/attic.js — the Attic detection + multiplier escalation engine.
// The core fantasy of the whole game: get a ball above the brick field and
// let the chaos ramp up. Escalates in visible TIERS (mult thresholds) so the
// player feels distinct "level-ups" the longer they stay up there.
import { BRICK_TOP, ATTIC_TOP } from '../core/constants.js';
import { clamp, lerp } from '../core/math.js';

const BRICK_MARGIN = 30, BRICK_GAP = 10, BRICK_W = 85, COLS = 10;
const COL_STEP = BRICK_W + BRICK_GAP;

// Escalation tiers: at each mult threshold, announce + add a visual layer.
export const TIERS = [
  { mult: 2, name: 'HEATING UP', color: '#5ee6ff', layer: 1 },
  { mult: 5, name: 'ON FIRE', color: '#ffd15e', layer: 2 },
  { mult: 10, name: 'RAMPAGE', color: '#ff5ec8', layer: 3 },
  { mult: 20, name: 'UNSTOPPABLE', color: '#b0ff5e', layer: 4 },
  { mult: 40, name: 'GODLIKE', color: '#ffffff', layer: 5 },
];

export class AtticSystem {
  constructor(game) {
    this.game = game;
    this.colTop = new Array(COLS).fill(Infinity);
    this.colBottom = new Array(COLS).fill(-Infinity);
    this.active = false;
    this.meter = 0;       // 0..1 ramps while active
    this.mult = 1;        // continuously-growing score multiplier
    this.chain = 0;       // consecutive attic bricks broken
    this.freeze = false;  // 'attictime' powerup: pause decay
    this.timeDilT = 0;    // brief slow-mo pulse timer
    this.enteredThisFrame = false;
    this.activeBallCount = 0;
    this.bannerT = 0;
    this.tier = 0;          // highest tier index reached (0 = none)
    this.tierBannerT = 0;
    this.tierBannerText = '';
    this.tierBannerColor = '#fff';
  }

  colIndexForX(x) {
    return clamp(Math.floor((x - BRICK_MARGIN) / COL_STEP), 0, COLS - 1);
  }

  /** Recompute per-column top/bottom of the surviving brick field. Call whenever bricks change (or once a frame — cheap). */
  rebuild(bricks) {
    this.colTop.fill(Infinity);
    this.colBottom.fill(-Infinity);
    if (!bricks) return;
    for (const b of bricks) {
      if (!b.alive) continue;
      const c0 = this.colIndexForX(b.x + 1);
      const c1 = this.colIndexForX(b.x + b.w - 1);
      for (let c = c0; c <= c1; c++) {
        if (b.y < this.colTop[c]) this.colTop[c] = b.y;
        if (b.y + b.h > this.colBottom[c]) this.colBottom[c] = b.y + b.h;
      }
    }
  }

  /** Is this ball currently resting/bouncing above the brick field? */
  isInAttic(ball) {
    const c = this.colIndexForX(ball.x);
    const top = this.colTop[c];
    if (!Number.isFinite(top)) return false;
    return ball.y + ball.r < top - 0.5 && ball.y > ATTIC_TOP - 20;
  }

  update(dt, bricks, balls) {
    this.rebuild(bricks);
    this.enteredThisFrame = false;
    let count = 0;
    for (const b of balls) {
      const inA = this.isInAttic(b);
      b.inAttic = inA;
      if (inA) count++;
    }
    this.activeBallCount = count;
    const wasActive = this.active;
    this.active = count > 0;
    if (this.active && !wasActive) {
      this.enteredThisFrame = true;
      this.chain = 0;
      this.bannerT = 1;
      this.game?.fx?.flash?.('#8ffcff', 0.45, 0.35);
      this.game?.fx?.shake?.(6, 0.25);
      this.game?.audio?.sfx?.('atticEnter');
    }
    if (!this.active && wasActive) {
      this.game?.audio?.sfx?.('atticExit');
    }
    if (this.bannerT > 0) this.bannerT = Math.max(0, this.bannerT - dt * 0.5);
    if (this.tierBannerT > 0) this.tierBannerT = Math.max(0, this.tierBannerT - dt * 0.6);

    if (this.active) {
      this.meter = Math.min(1, this.meter + dt / 1.2);
      const rate = 1.6 * (1 + 0.45 * count) * (1 + 0.06 * this.chain);
      this.mult += rate * dt;
    } else {
      this.meter = Math.max(0, this.meter - dt / 1.6);
      if (!this.freeze && this.mult > 1) {
        this.mult -= ((this.mult - 1) / 2.5) * dt;
        if (this.mult < 1.001) this.mult = 1;
        if (this.mult <= 1.001) this.tier = 0; // reset tiers once fully decayed
      }
    }

    this.checkTiers();
    this.game?.fx?.tier?.(this.tier);

    // beat-synced pulse ring at the attic zone while active
    const beatPhase = this.game?.audio?.music?.beatPhase?.() ?? 0;
    if (this.active && beatPhase < (this._lastBeatPhase ?? 0)) {
      this.game?.fx?.beatRing?.(500, BRICK_TOP - 20, { color: this.tierBannerColor || '#8ffcff', r0: 10, r1: 70 + this.tier * 16, life: 0.45, width: 2 + this.tier * 0.6 });
    }
    this._lastBeatPhase = beatPhase;

    if (this.timeDilT > 0) this.timeDilT = Math.max(0, this.timeDilT - dt * 4);


    const intensity = clamp(this.meter * 0.6 + Math.min(1, (this.mult - 1) / 25) * 0.4, 0, 1);
    this.intensity = intensity;
    this.game?.fx?.setIntensity?.(intensity);
    this.game?.audio?.music?.setIntensity?.(intensity);
    this.game?.audio?.music?.setAttic?.(this.active);
    this.cameraPush = this.meter * 0.045;
  }

  /** Fire a tier-up announcement the first time `mult` crosses each threshold. */
  checkTiers() {
    let newTierIdx = this.tier;
    for (let i = TIERS.length - 1; i >= 0; i--) {
      if (this.mult >= TIERS[i].mult) { newTierIdx = Math.max(newTierIdx, i + 1); break; }
    }
    if (newTierIdx > this.tier) {
      this.tier = newTierIdx;
      const t = TIERS[newTierIdx - 1];
      if (t) {
        this.tierBannerT = 1;
        this.tierBannerText = t.name;
        this.tierBannerColor = t.color;
        const cx = this.game ? 500 : 0, cy = this.game ? 700 : 0;
        this.game?.fx?.flash?.(t.color, 0.5, 0.4);
        this.game?.fx?.shockwave?.(cx, cy, { color: t.color, r0: 20, r1: 480, life: 0.55, width: 8 });
        this.game?.fx?.bloomPulse?.(cx, cy, { color: t.color, r: 500 + newTierIdx * 80, life: 1.1, strength: 1.3 });
        this.game?.fx?.shake?.(6 + newTierIdx * 2, 0.3);
        this.game?.audio?.sfx?.('multiUp', { pitch: 1 + newTierIdx * 0.08 });
        this.game?.notify?.(`TIER UP: ${t.name}!`, t.color);
      }
    }
  }

  /** Call when a brick breaks while a ball is in the attic (or is an 'attic'-type brick). */
  onAtticBrickBreak(brick) {
    this.chain++;
    const bonus = 60 * this.chain;
    this.game?.addScore?.(bonus);
    const cx = brick.cx ?? brick.x, cy = brick.cy ?? brick.y;
    const size = clamp(18 + this.chain * 1.4, 18, 46);
    const pitch = clamp(1 + this.chain * 0.035, 1, 2.4);
    this.game?.fx?.popup?.(cx, cy, `CHAIN x${this.chain}`, { color: '#ffe15e', size });
    this.game?.fx?.shockwave?.(cx, cy, { color: '#8ffcff' });
    this.game?.fx?.bloomPulse?.(cx, cy, { color: '#8ffcff', size: 60 + this.chain * 6 });
    this.game?.audio?.sfx?.('combo', { pitch });
    this.timeDilT = 1;
  }

  /** Multiply game dt by this to get a brief time-dilation pulse on attic breaks. */
  get timeScale() {
    if (this.timeDilT <= 0) return 1;
    return lerp(1, 0.82, Math.sin(this.timeDilT * Math.PI));
  }
}
