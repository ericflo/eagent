// audio.js — WebAudio synthesized SFX bank + frenzy riser. No samples.
// Initialized lazily on the first user gesture; mute state persisted.
const MUTE_KEY = 'breakthrough.muted';

export class Audio {
  constructor() {
    this.ctx = null;
    this.master = null;
    this.muted = localStorage.getItem(MUTE_KEY) === '1';
    this.riser = null; // { osc, gain, filter, noiseGain }
    this.riserLevel = 0;
  }

  // Call from any user-gesture handler (click/touch/key). Safe to re-call.
  init() {
    if (this.ctx) { if (this.ctx.state === 'suspended') this.ctx.resume(); return; }
    const AC = window.AudioContext || window.webkitAudioContext;
    if (!AC) return;
    this.ctx = new AC();
    this.master = this.ctx.createGain();
    this.master.gain.value = this.muted ? 0 : 0.9;
    this.master.connect(this.ctx.destination);
    this._buildRiser();
  }

  toggleMute() {
    this.muted = !this.muted;
    localStorage.setItem(MUTE_KEY, this.muted ? '1' : '0');
    if (this.master) this.master.gain.setTargetAtTime(this.muted ? 0 : 0.9, this.ctx.currentTime, 0.02);
    return this.muted;
  }

  _now() { return this.ctx ? this.ctx.currentTime : 0; }

  // A single synthesized blip: osc type -> freq envelope.
  _tone({ type = 'sine', f0 = 440, f1 = null, dur = 0.1, vol = 0.2, delay = 0, curve = 'exp' }) {
    if (!this.ctx || this.muted) return;
    const t = this._now() + delay;
    const o = this.ctx.createOscillator();
    const g = this.ctx.createGain();
    o.type = type;
    o.frequency.setValueAtTime(f0, t);
    if (f1 !== null) {
      if (curve === 'exp') o.frequency.exponentialRampToValueAtTime(Math.max(f1, 1), t + dur);
      else o.frequency.linearRampToValueAtTime(f1, t + dur);
    }
    g.gain.setValueAtTime(vol, t);
    g.gain.exponentialRampToValueAtTime(0.0001, t + dur);
    o.connect(g); g.connect(this.master);
    o.start(t); o.stop(t + dur + 0.02);
  }

  // Filtered noise burst (booms, thuds).
  _noise({ dur = 0.2, vol = 0.3, f0 = 900, f1 = 120, q = 1, delay = 0 }) {
    if (!this.ctx || this.muted) return;
    const t = this._now() + delay;
    const len = Math.max(1, Math.floor(this.ctx.sampleRate * dur));
    const buf = this.ctx.createBuffer(1, len, this.ctx.sampleRate);
    const d = buf.getChannelData(0);
    for (let i = 0; i < len; i++) d[i] = (Math.random() * 2 - 1) * (1 - i / len);
    const src = this.ctx.createBufferSource();
    src.buffer = buf;
    const flt = this.ctx.createBiquadFilter();
    flt.type = 'lowpass'; flt.Q.value = q;
    flt.frequency.setValueAtTime(f0, t);
    flt.frequency.exponentialRampToValueAtTime(Math.max(f1, 20), t + dur);
    const g = this.ctx.createGain();
    g.gain.setValueAtTime(vol, t);
    g.gain.exponentialRampToValueAtTime(0.0001, t + dur);
    src.connect(flt); flt.connect(g); g.connect(this.master);
    src.start(t);
  }

  // --- SFX -----------------------------------------------------------------
  brickPop(comboStep) {
    const base = 300 * Math.pow(1.0594631, Math.min(comboStep, 24)); // pitch up per combo
    this._tone({ type: 'square', f0: base * 2, f1: base * 0.9, dur: 0.09, vol: 0.16 });
    this._tone({ type: 'sine', f0: base * 3, dur: 0.06, vol: 0.08 });
  }
  tink() { this._tone({ type: 'triangle', f0: 1900, f1: 1500, dur: 0.07, vol: 0.14 }); }
  clank() {
    this._tone({ type: 'square', f0: 220, f1: 180, dur: 0.12, vol: 0.14 });
    this._noise({ dur: 0.08, vol: 0.18, f0: 3000, f1: 500, q: 6 });
  }
  thunk() { this._tone({ type: 'sine', f0: 140, f1: 90, dur: 0.12, vol: 0.22 }); }
  boom(step = 0) {
    this._noise({ dur: 0.4, vol: 0.4, f0: 700 - step * 60, f1: 60, q: 1 });
    this._tone({ type: 'sine', f0: 90 - step * 8, f1: 40, dur: 0.35, vol: 0.4 });
  }
  thwock(smash) {
    if (smash) {
      this._tone({ type: 'sawtooth', f0: 240, f1: 520, dur: 0.1, vol: 0.22 });
      this._noise({ dur: 0.06, vol: 0.12, f0: 2500, f1: 900 });
    } else {
      this._tone({ type: 'triangle', f0: 190, f1: 120, dur: 0.08, vol: 0.2 });
    }
  }
  chime() {
    [523, 659, 784, 1046].forEach((f, i) =>
      this._tone({ type: 'sine', f0: f, dur: 0.16, vol: 0.14, delay: i * 0.07 }));
  }
  serve() { this._tone({ type: 'sine', f0: 330, f1: 660, dur: 0.12, vol: 0.18 }); }
  lifeLost() {
    this._tone({ type: 'sawtooth', f0: 200, f1: 50, dur: 0.5, vol: 0.3 });
    this._noise({ dur: 0.3, vol: 0.2, f0: 400, f1: 80 });
  }
  uiClick() { this._tone({ type: 'sine', f0: 700, dur: 0.04, vol: 0.08 }); }
  levelClear() {
    [392, 494, 587, 784, 988].forEach((f, i) =>
      this._tone({ type: 'triangle', f0: f, dur: 0.22, vol: 0.15, delay: i * 0.09 }));
  }
  gameOverSound() {
    [330, 262, 208, 165].forEach((f, i) =>
      this._tone({ type: 'sawtooth', f0: f, dur: 0.3, vol: 0.16, delay: i * 0.16 }));
  }

  // --- Frenzy riser (filtered noise + rising oscillator, tied to multiplier) --
  _buildRiser() {
    const ctx = this.ctx;
    const o = ctx.createOscillator();
    o.type = 'sawtooth'; o.frequency.value = 80;
    const oGain = ctx.createGain(); oGain.gain.value = 0;
    const flt = ctx.createBiquadFilter();
    flt.type = 'bandpass'; flt.frequency.value = 300; flt.Q.value = 2;
    // looping noise
    const len = ctx.sampleRate * 2;
    const buf = ctx.createBuffer(1, len, ctx.sampleRate);
    const d = buf.getChannelData(0);
    for (let i = 0; i < len; i++) d[i] = Math.random() * 2 - 1;
    const noise = ctx.createBufferSource();
    noise.buffer = buf; noise.loop = true;
    const nGain = ctx.createGain(); nGain.gain.value = 0;
    o.connect(flt); noise.connect(flt); flt.connect(oGain);
    oGain.connect(this.master);
    o.start(); noise.start();
    this.riser = { o, oGain, flt, nGain, noise };
  }

  // level: 0..1 frenzy intensity (from multiplier ramp x2..x8)
  setFrenzy(level) {
    if (!this.ctx || !this.riser) return;
    level = Math.max(0, Math.min(1, level));
    if (Math.abs(level - this.riserLevel) < 0.01) return;
    this.riserLevel = level;
    const t = this.ctx.currentTime;
    this.riser.oGain.gain.setTargetAtTime(this.muted ? 0 : level * 0.14, t, 0.12);
    this.riser.nGain.gain.setTargetAtTime(this.muted ? 0 : level * 0.05, t, 0.12);
    this.riser.o.frequency.setTargetAtTime(80 + level * 440, t, 0.15);
    this.riser.flt.frequency.setTargetAtTime(300 + level * 2200, t, 0.15);
  }
}
