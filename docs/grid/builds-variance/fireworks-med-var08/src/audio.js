// Procedural WebAudio engine. No sound files. All sound starts only after a
// user gesture (resume()). Intensity parameter (0..1) scales loudness/energy
// with the multiplier/chaos state.

export class AudioEngine {
  constructor() {
    this.ctx = null;
    this.master = null;
    this.musicGain = null;
    this.muted = false;
    this.intensity = 0;
    this._pulseT = 0;
    this._started = false;
  }

  // Must be called from a user gesture.
  start() {
    if (this._started) return;
    const AC = window.AudioContext || window.webkitAudioContext;
    if (!AC) return;
    this.ctx = new AC();
    this.master = this.ctx.createGain();
    this.master.gain.value = this.muted ? 0 : 0.5;
    this.master.connect(this.ctx.destination);

    // Soft limiter-ish compressor to keep bursts from clipping.
    const comp = this.ctx.createDynamicsCompressor();
    comp.threshold.value = -18;
    comp.ratio.value = 6;
    comp.connect(this.master);

    this.musicGain = this.ctx.createGain();
    this.musicGain.gain.value = 0.16;
    this.musicGain.connect(comp);

    this._comp = comp;
    this._started = true;
    this._startPad();
  }

  resume() { if (this.ctx && this.ctx.state === 'suspended') this.ctx.resume(); }

  setMuted(m) {
    this.muted = m;
    if (this.master) {
      this.master.gain.setTargetAtTime(m ? 0 : 0.5, this.ctx.currentTime, 0.05);
    }
  }

  setIntensity(v) {
    this.intensity = Math.max(0, Math.min(1, v));
    if (this.musicGain && this.ctx) {
      this.musicGain.gain.setTargetAtTime(0.14 + 0.22 * this.intensity, this.ctx.currentTime, 0.2);
    }
  }

  // --- ambient pad: two detuned oscillators whose filter follows intensity
  _startPad() {
    const ctx = this.ctx;
    const freqs = [55, 82.4];
    this._padOsc = freqs.map((f, i) => {
      const o = ctx.createOscillator();
      o.type = i ? 'triangle' : 'sawtooth';
      o.frequency.value = f;
      const flt = ctx.createBiquadFilter();
      flt.type = 'lowpass';
      flt.frequency.value = 220;
      const gain = ctx.createGain();
      gain.gain.value = 0.5;
      o.connect(flt); flt.connect(gain); gain.connect(this.musicGain);
      o.start();
      return { o, flt };
    });
    this._padLfo = ctx.createOscillator();
    this._padLfo.frequency.value = 0.12;
    const lfoGain = ctx.createGain();
    lfoGain.gain.value = 90;
    this._padLfo.connect(lfoGain);
    this._padOsc.forEach(p => lfoGain.connect(p.flt.frequency));
    this._padLfo.start();
  }

  // Called each frame; drives the pulsing "heartbeat" of the chaos state.
  frame(dt, chaosHeat) {
    if (!this._started || !this.ctx) return;
    this._padOsc?.forEach(p => {
      p.flt.frequency.setTargetAtTime(200 + 900 * chaosHeat, this.ctx.currentTime, 0.15);
    });
    this._pulseT -= dt;
    if (this._pulseT <= 0) {
      const period = chaosHeat > 0.02 ? 0.5 - 0.3 * chaosHeat : 1.6;
      this._pulseT = period;
      if (chaosHeat > 0.02) {
        this.tone({ freq: 60 + 30 * chaosHeat, dur: 0.14, type: 'sine',
          gain: 0.5 + 0.5 * chaosHeat, sweepTo: 40 });
      }
    }
  }

  // --- one-shot helpers -------------------------------------------------
  tone({ freq = 440, dur = 0.15, type = 'sine', gain = 0.3, sweepTo = null, delay = 0, pan = 0 } = {}) {
    if (!this._started || this.muted) return;
    const ctx = this.ctx;
    const t0 = ctx.currentTime + delay;
    const o = ctx.createOscillator();
    const g = ctx.createGain();
    const p = ctx.createStereoPanner ? ctx.createStereoPanner() : null;
    o.type = type;
    o.frequency.setValueAtTime(freq, t0);
    if (sweepTo) o.frequency.exponentialRampToValueAtTime(Math.max(20, sweepTo), t0 + dur);
    g.gain.setValueAtTime(gain * 0.3, t0);
    g.gain.exponentialRampToValueAtTime(0.0001, t0 + dur);
    o.connect(g);
    if (p) { p.pan.value = pan; g.connect(p); p.connect(this._comp); }
    else g.connect(this._comp);
    o.start(t0);
    o.stop(t0 + dur + 0.05);
  }

  noise({ dur = 0.2, gain = 0.3, hp = 800, lp = 6000, delay = 0 } = {}) {
    if (!this._started || this.muted) return;
    const ctx = this.ctx;
    const t0 = ctx.currentTime + delay;
    const len = Math.max(1, Math.floor(ctx.sampleRate * dur));
    const buf = ctx.createBuffer(1, len, ctx.sampleRate);
    const data = buf.getChannelData(0);
    for (let i = 0; i < len; i++) data[i] = Math.random() * 2 - 1;
    const src = ctx.createBufferSource();
    src.buffer = buf;
    const f1 = ctx.createBiquadFilter(); f1.type = 'highpass'; f1.frequency.value = hp;
    const f2 = ctx.createBiquadFilter(); f2.type = 'lowpass'; f2.frequency.value = lp;
    const g = ctx.createGain();
    g.gain.setValueAtTime(gain * 0.3, t0);
    g.gain.exponentialRampToValueAtTime(0.0001, t0 + dur);
    src.connect(f1); f1.connect(f2); f2.connect(g); g.connect(this._comp);
    src.start(t0);
  }

  // --- game events -------------------------------------------------------
  paddleHit(lift = 0) {
    const e = Math.min(1, Math.abs(lift) / 500);
    this.tone({ freq: 180 + 120 * e, sweepTo: 90, dur: 0.1, type: 'square', gain: 0.25 });
  }
  wallHit() { this.tone({ freq: 240, sweepTo: 200, dur: 0.06, type: 'triangle', gain: 0.15 }); }
  brickBreak(kind, combo, chaos) {
    const base = 300 + Math.min(12, combo) * 45 + (chaos ? 200 : 0);
    this.tone({ freq: base, sweepTo: base * 1.9, dur: 0.16, type: 'sine', gain: 0.35 });
    this.noise({ dur: 0.14, gain: 0.3, hp: 1200 });
    if (kind === 'exploding') {
      this.noise({ dur: 0.5, gain: 0.6, hp: 100, lp: 1400 });
      this.tone({ freq: 90, sweepTo: 30, dur: 0.5, type: 'sawtooth', gain: 0.5 });
    }
  }
  brickReject(reason) {
    this.tone({ freq: reason === 'too-slow' ? 140 : 110, dur: 0.12, type: 'sawtooth', gain: 0.18, sweepTo: 70 });
  }
  powerCatch(hue) {
    [0, 0.07, 0.14].forEach((d, i) =>
      this.tone({ freq: 420 * Math.pow(1.335, i), dur: 0.18, type: 'triangle', gain: 0.3, delay: d }));
  }
  launch() { this.tone({ freq: 300, sweepTo: 700, dur: 0.14, type: 'triangle', gain: 0.3 }); }
  ballLost() { this.tone({ freq: 320, sweepTo: 70, dur: 0.45, type: 'sawtooth', gain: 0.3 }); }
  levelClear() {
    [523, 659, 784, 1046].forEach((f, i) =>
      this.tone({ freq: f, dur: 0.3, type: 'triangle', gain: 0.35, delay: i * 0.09 }));
  }
  gameOver() {
    [392, 311, 233, 155].forEach((f, i) =>
      this.tone({ freq: f, dur: 0.4, type: 'sawtooth', gain: 0.3, delay: i * 0.16 }));
  }
  magnetCatch() { this.tone({ freq: 700, sweepTo: 900, dur: 0.12, type: 'sine', gain: 0.25 }); }
}

export const audio = new AudioEngine();
