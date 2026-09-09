// ---------------------------------------------------------------------------
// audio.js — WebAudio synth engine. Created lazily on first user gesture.
// ---------------------------------------------------------------------------

import { clamp } from './utils.js';

class AudioEngine {
  constructor() {
    this.ctx = null;
    this.master = null;
    this.muted = false;
    this.musicOn = true;
    this._started = false;
    this._musicTimer = null;
    this._step = 0;
    this._layers = 0;      // 0..3 music intensity layers
    this._heat = 0;        // 0..1 overdrive heat (drives music brightness)
  }

  start() {
    if (this._started) return;
    this._started = true;
    const AC = window.AudioContext || window.webkitAudioContext;
    if (!AC) return;
    this.ctx = new AC();
    this.master = this.ctx.createGain();
    this.master.gain.value = this.muted ? 0 : 0.85;
    const comp = this.ctx.createDynamicsCompressor();
    comp.threshold.value = -14;
    comp.ratio.value = 8;
    this.master.connect(comp).connect(this.ctx.destination);
    this.startMusic();
  }

  resume() {
    if (this.ctx && this.ctx.state === 'suspended') this.ctx.resume();
  }

  setMuted(m) {
    this.muted = m;
    if (this.master) {
      const t = this.ctx.currentTime;
      this.master.gain.cancelScheduledValues(t);
      this.master.gain.linearRampToValueAtTime(m ? 0 : 0.85, t + 0.08);
    }
  }

  setHeat(h) {
    this._heat = clamp(h, 0, 1);
  }

  setLayers(n) {
    this._layers = n;
  }

  // ---- primitive ----------------------------------------------------------

  tone(freq, dur, { type = 'sine', gain = 0.2, attack = 0.004, when = 0, slide = 0, pan = 0, detune = 0 } = {}) {
    if (!this.ctx) return;
    const t0 = this.ctx.currentTime + when;
    const o = this.ctx.createOscillator();
    const g = this.ctx.createGain();
    const p = this.ctx.createStereoPanner ? this.ctx.createStereoPanner() : null;
    o.type = type;
    o.frequency.setValueAtTime(freq, t0);
    if (slide) o.frequency.exponentialRampToValueAtTime(Math.max(20, freq + slide), t0 + dur);
    if (detune) o.detune.value = detune;
    g.gain.setValueAtTime(0, t0);
    g.gain.linearRampToValueAtTime(gain, t0 + attack);
    g.gain.exponentialRampToValueAtTime(0.0008, t0 + dur);
    if (p) { p.pan.value = pan; o.connect(g).connect(p).connect(this.master); }
    else o.connect(g).connect(this.master);
    o.start(t0);
    o.stop(t0 + dur + 0.03);
  }

  noise(dur, { gain = 0.2, when = 0, hp = 600, lp = 12000, pan = 0, sweep = 0 } = {}) {
    if (!this.ctx) return;
    const t0 = this.ctx.currentTime + when;
    const len = Math.max(1, (dur * this.ctx.sampleRate) | 0);
    const buf = this.ctx.createBuffer(1, len, this.ctx.sampleRate);
    const d = buf.getChannelData(0);
    for (let i = 0; i < len; i++) d[i] = Math.random() * 2 - 1;
    const src = this.ctx.createBufferSource();
    src.buffer = buf;
    const g = this.ctx.createGain();
    g.gain.setValueAtTime(gain, t0);
    g.gain.exponentialRampToValueAtTime(0.0008, t0 + dur);
    const f = this.ctx.createBiquadFilter();
    f.type = 'bandpass';
    f.frequency.setValueAtTime(hp, t0);
    if (sweep) f.frequency.exponentialRampToValueAtTime(Math.max(50, hp + sweep), t0 + dur);
    f.Q.value = 0.8;
    const p = this.ctx.createStereoPanner ? this.ctx.createStereoPanner() : null;
    src.connect(f).connect(g);
    if (p) { p.pan.value = pan; g.connect(p).connect(this.master); } else g.connect(this.master);
    src.start(t0);
  }

  // ---- SFX ----------------------------------------------------------------

  paddleBounce(speed = 0, lift = 0) {
    const f = 150 + 130 * clamp(speed / 900, 0, 1);
    this.tone(f, 0.09, { type: 'square', gain: 0.09, slide: f * 0.5 });
    this.tone(f * 2.01, 0.05, { type: 'triangle', gain: 0.05 });
    if (lift > 0.3) this.tone(520, 0.1, { type: 'sine', gain: 0.06, slide: 300 }); // lift whoosh
    this.noise(0.03, { gain: 0.05, hp: 900 });
  }

  wallBounce() {
    this.tone(210, 0.06, { type: 'triangle', gain: 0.07, slide: -60 });
  }

  thunk() {
    // dull fail hit
    this.tone(92, 0.1, { type: 'sine', gain: 0.12, slide: -30 });
    this.noise(0.05, { gain: 0.04, hp: 200, lp: 900 });
  }

  brick(combo = 0) {
    const st = Math.min(24, combo);
    const f = 320 * Math.pow(2, st / 12);
    this.tone(f, 0.13, { type: 'triangle', gain: 0.1, slide: f * 0.6 });
    this.tone(f * 1.5, 0.08, { type: 'sine', gain: 0.05, when: 0.012 });
    this.noise(0.07, { gain: 0.07, hp: 1800, sweep: 2400 });
  }

  phaseTick() {
    this.tone(1400, 0.05, { type: 'sine', gain: 0.025, slide: -500 });
  }

  spinnerTick() {
    this.tone(980, 0.03, { type: 'triangle', gain: 0.02 });
  }

  powerup() {
    [660, 880, 1320].forEach((f, i) => this.tone(f, 0.14, { type: 'sine', gain: 0.08, when: i * 0.05 }));
  }

  life() {
    [523, 659, 784, 1046].forEach((f, i) => this.tone(f, 0.22, { type: 'triangle', gain: 0.08, when: i * 0.07 }));
  }

  explode() {
    this.noise(0.3, { gain: 0.2, hp: 90, sweep: -60 });
    this.tone(70, 0.28, { type: 'sine', gain: 0.16, slide: -30 });
  }

  launch() {
    this.tone(340, 0.12, { type: 'triangle', gain: 0.09, slide: 260 });
  }

  loseBall() {
    this.tone(330, 0.4, { type: 'sawtooth', gain: 0.08, slide: -260 });
    this.tone(165, 0.4, { type: 'sine', gain: 0.08, slide: -110, when: 0.05 });
  }

  levelClear() {
    [392, 523, 659, 784, 1046].forEach((f, i) =>
      this.tone(f, 0.3, { type: 'triangle', gain: 0.09, when: i * 0.09 }));
    this.noise(0.6, { gain: 0.05, hp: 2500, when: 0.15 });
  }

  payout(mult) {
    const base = 330;
    [0, 4, 7, 12, 16, 19, 24].forEach((s, i) =>
      this.tone(base * Math.pow(2, s / 12), 0.5, { type: 'triangle', gain: 0.075, when: i * 0.055 }));
    this.tone(base / 2, 0.9, { type: 'sine', gain: 0.12, when: 0 });
    this.noise(0.8, { gain: 0.07, hp: 1800, when: 0.2, sweep: 4000 });
  }

  gameOver() {
    [330, 262, 220, 165].forEach((f, i) =>
      this.tone(f, 0.5, { type: 'sawtooth', gain: 0.07, when: i * 0.18 }));
  }

  ui() {
    this.tone(880, 0.05, { type: 'sine', gain: 0.05 });
  }

  // ---- procedural music ----------------------------------------------------

  startMusic() {
    if (!this.ctx) return;
    const bpm = 118;
    this._musicInterval = (60 / bpm / 2) * 1000; // 8th notes
    this._musicTimer = setInterval(() => this._musicStep(), this._musicInterval);
  }

  _musicStep() {
    if (!this.ctx || this.ctx.state !== 'running' || this.muted) { this._step++; return; }
    const s = this._step++;
    const bar = (s / 8) | 0;
    const heat = this._heat;
    const layers = this._layers;

    // Layer 0 (always): bass pulse
    const roots = [55, 55, 73.4, 61.7]; // A A D B
    const root = roots[bar % 4];
    if (s % 2 === 0) {
      this.tone(root, 0.16, { type: 'sine', gain: 0.09 });
      this.tone(root * 2, 0.1, { type: 'triangle', gain: 0.045 });
    }
    // Layer 1: hats (combo 1+)
    if (layers >= 1 && s % 2 === 1) this.noise(0.035, { gain: 0.028, hp: 7000 });
    // Layer 2: arp (overdrive)
    if (layers >= 2) {
      const arp = [0, 7, 12, 7, 3, 10, 15, 10];
      const semi = arp[s % 8] + (bar % 4 === 2 ? 5 : 0);
      const f = root * 4 * Math.pow(2, semi / 12);
      this.tone(f, 0.09, { type: 'square', gain: 0.026 + heat * 0.014, when: 0 });
    }
    // Layer 3: pad swell + shimmer (deep overdrive)
    if (layers >= 3 && s % 16 === 0) {
      this.tone(root * 3, 1.6, { type: 'sawtooth', gain: 0.02 + heat * 0.02 });
      this.tone(root * 4.5, 1.6, { type: 'sine', gain: 0.016 + heat * 0.02, detune: 6 });
    }
  }
}

export const audio = new AudioEngine();
