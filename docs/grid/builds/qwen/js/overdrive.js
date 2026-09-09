// overdrive.js — the core loop meter.
// Meter 0..100: +24/s while any ball is in the zone (y < 260), -30/s
// otherwise, hard reset to 0 when all balls are lost. Multiplier is
// 2^floor(meter/20) → 1..32. At 100 the meter banks a one-time
// +250×level bonus and stays pinned until it drains below 60.
// Band crossings (20/40/60/80/100) produce tick events for FX/audio.

import { OVERDRIVE } from './config.js';
import { clamp } from './utils.js';

export class Overdrive {
  constructor() {
    this.reset(true);
  }

  reset(instant = true) {
    this.meter = 0;
    this.banked = false;      // has the 100-bank already fired for this fill?
    this.canBank = true;      // re-arms when drained below REARM_BELOW
    this.bands = 0;           // current band index 0..5 (meter/20 floored)
    this.events = [];         // per-frame events: 'tick', 'max', 'drain'
    if (instant) this.events.length = 0;
  }

  get multiplier() { return Math.pow(2, Math.floor(this.meter / OVERDRIVE.MULT_STEP)); }
  get juice() { return this.meter / OVERDRIVE.MAX; } // 0..1
  get band() { return Math.min(5, Math.floor(this.meter / OVERDRIVE.MULT_STEP)); }

  // Returns true if a 100-bank fired this frame (game adds the bonus).
  update(dt, anyBallInZone) {
    this.events.length = 0;
    const prev = this.meter;
    if (this.banked && this.meter >= OVERDRIVE.MAX) {
      // Pinned at 100: drain anyway so it can re-arm
      this.meter -= OVERDRIVE.DRAIN * dt;
      if (this.meter < OVERDRIVE.REARM_BELOW) {
        this.banked = false;
        this.canBank = true;
        this.events.push('drain');
      }
    } else if (anyBallInZone) {
      this.meter += OVERDRIVE.GAIN * dt;
    } else {
      this.meter -= OVERDRIVE.DRAIN * dt;
      if (this.banked && this.meter < OVERDRIVE.REARM_BELOW) {
        this.banked = false;
        this.canBank = true;
        this.events.push('drain');
      }
    }
    this.meter = clamp(this.meter, 0, OVERDRIVE.MAX);

    // Band crossing ticks (only when meter was actually in play)
    const band = Math.min(5, Math.floor(this.meter / OVERDRIVE.MULT_STEP));
    if (band > this.bands && this.meter > 0) this.events.push('tick');
    this.bands = band;

    // 100 bank (fires once per fill)
    if (this.meter >= OVERDRIVE.MAX && !this.banked && this.canBank) {
      this.banked = true;
      this.canBank = false;
      this.events.push('max');
      return true;
    }
    return false;
  }
}