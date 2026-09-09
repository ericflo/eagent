// audio.js — 100% WebAudio-synthesized sound for Overdrive Breakout.
// Lazy AudioContext on first user gesture. Short envelopes, x-position panning,
// volume/brightness scaling with overdrive multiplier band. Feature-detected:
// if WebAudio is unavailable every method is a safe no-op.

import { AUDIO } from './config.js';
import { clamp } from './utils.js';

export class Audio {
  constructor() {
    this.ctx = null;
    this.master = null;
    this.muted = false;
    this.humOsc = null;
    this.humGain = null;
    this.humLfo = null;
    this._supported = typeof window !== 'undefined' &&
      !!(window.AudioContext || window.webkitAudioContext);
    this._lastSfx = {}; // per-name rate limit (ms)
  }

  // Must be called from a user gesture at least once.
  ensure() {
    if (this.ctx || !this._supported) return;
    try {
      const AC = window.AudioContext || window.webkitAudioContext;
      this.ctx = new AC();
      this.master = this.ctx.createGain();
      this.master.gain.value = this.muted ? 0 : AUDIO.MASTER;
      const comp = this.ctx.createDynamicsCompressor();
      this.master.connect(comp);
      comp.connect(this.ctx.destination);
      this._startHum();
    } catch (e) {
      this._supported = false;
      this.ctx = null;
    }
  }

  resume() {
    if (this.ctx && this.ctx.state === 'suspended') this.ctx.resume().catch(() => {});
  }

  setMuted(m) {
    this.muted = m;
    if (this.master) {
      this.master.gain.setTargetAtTime(m ? 0 : AUDIO.MASTER, this.ctx.currentTime, 0.02);
    }
  }

  // Band: 0..5 (meter/20). SFX get slightly louder + brighter in higher bands.
  bandGain(band) { return 1 + band * 0.06; }
  bandBright(band) { return 1 + band * 0.12; }

  _pan(x, fieldW) {
    if (!this.ctx) return null;
    const v = clamp((x / fieldW) * 2 - 1, -1, 1);
    if (this.ctx.createStereoPanner) {
      const p = this.ctx.createStereoPanner();
      p.pan.value = v;
      return p;
    }
    return null;
  }

  _out(x, fieldW, band, vol) {
    // Returns the node to connect a source to, or null if muted/unsupported.
    if (!this.ctx || this.muted) return null;
    let node = this.ctx.createGain();
    node.gain.value = vol * this.bandGain(band || 0);
    const p = this._pan(x, fieldW || 1500);
    if (p) { node.connect(p); p.connect(this.master); }
    else node.connect(this.master);
    return node;
  }

  // One-shot oscillator tone with exponential decay.
  _tone({ freq, type = 'sine', dur = 0.1, vol = 0.5, x = 0, band = 0, sweep = 0, delay = 0 }) {
    if (!this.ctx || this.muted) return;
    const t0 = this.ctx.currentTime + delay;
    const out = this._out(x, 1500, band, vol);
    if (!out) return;
    const o = this.ctx.createOscillator();
    o.type = type;
    o.frequency.setValueAtTime(freq * this.bandBright(band), t0);
    if (sweep) o.frequency.exponentialRampToValueAtTime(Math.max(30, freq + sweep), t0 + dur);
    const g = this.ctx.createGain();
    g.gain.setValueAtTime(vol, t0);
    g.gain.exponentialRampToValueAtTime(0.001, t0 + dur);
    o.connect(g); g.connect(out);
    try { o.start(t0); o.stop(t0 + dur + 0.02); } catch (e) { /* ctx suspended */ }
  }

  // Short noise burst (for slams, booms).
  _noise({ dur = 0.08, vol = 0.4, x = 0, band = 0, delay = 0, lowpass = 3000 }) {
    if (!this.ctx || this.muted) return;
    const t0 = this.ctx.currentTime + delay;
    const len = Math.max(1, Math.floor(this.ctx.sampleRate * dur));
    const buf = this.ctx.createBuffer(1, len, this.ctx.sampleRate);
    const d = buf.getChannelData(0);
    for (let i = 0; i < len; i++) d[i] = (Math.random() * 2 - 1) * (1 - i / len);
    const src = this.ctx.createBufferSource();
    src.buffer = buf;
    const f = this.ctx.createBiquadFilter();
    f.type = 'lowpass';
    f.frequency.value = lowpass * this.bandBright(band);
    const out = this._out(x, 1500, band, vol);
    if (!out) return;
    src.connect(f); f.connect(out);
    try { src.start(t0); } catch (e) { /* ctx suspended */ }
  }

  _limit(name, ms) {
    const now = performance.now();
    if (this._lastSfx[name] && now - this._lastSfx[name] < ms) return true;
    this._lastSfx[name] = now;
    return false;
  }

  // ---- Public SFX ----------------------------------------------------------

  paddle(x, offset01, band, slammed) {
    if (this._limit('paddle', 30)) return;
    const f = 160 + 140 * Math.abs(offset01 - 0.5) * 2;
    if (slammed) {
      this._tone({ freq: f * 0.7, type: 'triangle', dur: 0.14, vol: 0.7, x, band });
      this._noise({ dur: 0.1, vol: 0.5, x, band, lowpass: 1200 });
    } else {
      this._tone({ freq: f, type: 'triangle', dur: 0.08, vol: 0.5, x, band });
    }
  }

  brick(type, x, band) {
    const t = {
      std:   { f: 440, ty: 'square',    d: 0.07, v: 0.4 },
      glass: { f: 1500, ty: 'sine',    d: 0.09, v: 0.35 },
      angle: { f: 660, ty: 'square',   d: 0.08, v: 0.45 },
      speed: { f: 330, ty: 'sawtooth', d: 0.09, v: 0.45 },
      mover: { f: 520, ty: 'square',   d: 0.08, v: 0.4 },
      bomb:  { f: 90,  ty: 'sawtooth', d: 0.3,  v: 0.7 },
      mini:  { f: 700, ty: 'square',   d: 0.06, v: 0.3 },
      key:   { f: 880, ty: 'sine',     d: 0.16, v: 0.5 },
      lock:  { f: 550, ty: 'square',   d: 0.1,  v: 0.45 },
      magnet:{ f: 480, ty: 'sine',     d: 0.1,  v: 0.4 },
      mirror:{ f: 1100, ty: 'sine',    d: 0.1,  v: 0.4 },
      ramp:  { f: 220, ty: 'triangle', d: 0.05, v: 0.3 }
    }[type] || { f: 400, ty: 'square', d: 0.07, v: 0.4 };
    if (type === 'bomb') {
      this._noise({ dur: 0.25, vol: 0.6, x, band, lowpass: 800 });
    }
    this._tone({ freq: t.f, type: t.ty, dur: t.d, vol: t.v, x, band });
  }

  clink(x, band, dull) {
    if (this._limit('clink', 50)) return;
    if (dull) {
      this._tone({ freq: 180, type: 'square', dur: 0.04, vol: 0.3, x, band });
    } else {
      this._tone({ freq: 2200, type: 'sine', dur: 0.05, vol: 0.35, x, band });
      this._tone({ freq: 3300, type: 'sine', dur: 0.04, vol: 0.2, x, band });
    }
  }

  powerup(x, band) {
    this._tone({ freq: 660, type: 'sine', dur: 0.08, vol: 0.45, x, band });
    this._tone({ freq: 990, type: 'sine', dur: 0.12, vol: 0.45, x, band, delay: 0.07 });
    this._tone({ freq: 1320, type: 'sine', dur: 0.16, vol: 0.35, x, band, delay: 0.14 });
  }

  lifeLost(band) {
    this._tone({ freq: 440, type: 'sawtooth', dur: 0.5, vol: 0.5, sweep: -340 });
    this._noise({ dur: 0.3, vol: 0.3, lowpass: 600 });
  }

  levelClear(band) {
    const notes = [523.25, 659.25, 783.99, 1046.5, 1318.5];
    notes.forEach((f, i) => this._tone({ freq: f, type: 'square', dur: 0.22, vol: 0.4, band, delay: i * 0.09 }));
  }

  overdriveTick(band) {
    this._tone({ freq: 880 + band * 220, type: 'sine', dur: 0.09, vol: 0.5 });
    this._tone({ freq: (880 + band * 220) * 1.5, type: 'sine', dur: 0.12, vol: 0.35, delay: 0.05 });
  }

  overdriveMax(band) {
    const notes = [523.25, 659.25, 783.99, 1046.5, 1568];
    notes.forEach((f, i) => this._tone({ freq: f, type: 'sawtooth', dur: 0.3, vol: 0.4, band, delay: i * 0.06 }));
    this._noise({ dur: 0.4, vol: 0.25, lowpass: 4000 });
  }

  wall(x, band, vol) {
    if (this._limit('wall', 40)) return;
    this._tone({ freq: 200, type: 'triangle', dur: 0.04, vol: vol || 0.2, x, band });
  }

  launch(band) {
    this._tone({ freq: 300, type: 'square', dur: 0.1, vol: 0.4, sweep: 500 });
  }

  laser(band) {
    if (this._limit('laser', 90)) return;
    this._tone({ freq: 1800, type: 'sawtooth', dur: 0.09, vol: 0.18, sweep: -1200, band });
  }

  gameOver() {
    const notes = [392, 329.63, 261.63, 196];
    notes.forEach((f, i) => this._tone({ freq: f, type: 'sawtooth', dur: 0.4, vol: 0.4, delay: i * 0.22 }));
  }

  // ---- Ambient overdrive hum ----------------------------------------------
  // Looped oscillator; pitch + gain follow the meter (0..1).
  _startHum() {
    try {
      this.humOsc = this.ctx.createOscillator();
      this.humOsc.type = 'sawtooth';
      this.humOsc.frequency.value = 55;
      const f = this.ctx.createBiquadFilter();
      f.type = 'lowpass';
      f.frequency.value = 300;
      this.humGain = this.ctx.createGain();
      this.humGain.gain.value = 0;
      this.humOsc.connect(f); f.connect(this.humGain); this.humGain.connect(this.master);
      this.humOsc.start();
    } catch (e) { /* no hum, no harm */ }
  }

  setHum(meter01) {
    if (!this.ctx || !this.humGain) return;
    const t = this.ctx.currentTime;
    const g = meter01 > 0.15 ? (meter01 - 0.15) * 0.16 : 0;
    this.humGain.gain.setTargetAtTime(g, t, 0.1);
    this.humOsc.frequency.setTargetAtTime(55 + meter01 * 110, t, 0.15);
  }
}
