// Synthesized audio via WebAudio. No files, no external deps.
// Context is created/resumed lazily on first user gesture.
export class AudioSys {
  constructor() {
    this.ctx = null;
    this.master = null;
    this.muted = localStorage.getItem('bt_muted') === '1';
    this.frenzyGain = null;
    this.frenzyOsc = null;
    this._noiseBuf = null;
  }
  ensure() {
    if (!this.ctx) {
      try {
        this.ctx = new (window.AudioContext || window.webkitAudioContext)();
        this.master = this.ctx.createGain();
        this.master.gain.value = this.muted ? 0 : 0.5;
        this.master.connect(this.ctx.destination);
      } catch (e) { return false; }
    }
    if (this.ctx.state === 'suspended') this.ctx.resume();
    return true;
  }
  resume() { this.ensure(); }
  toggleMute() {
    this.muted = !this.muted;
    localStorage.setItem('bt_muted', this.muted ? '1' : '0');
    if (this.master) this.master.gain.value = this.muted ? 0 : 0.5;
    return this.muted;
  }
  _noise() {
    if (!this._noiseBuf) {
      const len = this.ctx.sampleRate * 0.6;
      this._noiseBuf = this.ctx.createBuffer(1, len, this.ctx.sampleRate);
      const d = this._noiseBuf.getChannelData(0);
      for (let i = 0; i < len; i++) d[i] = Math.random() * 2 - 1;
    }
    return this._noiseBuf;
  }
  _tone({ freq = 440, freqEnd, type = 'sine', dur = 0.1, vol = 0.3, delay = 0, attack = 0.002 }) {
    if (!this.ctx || this.muted) return;
    const t = this.ctx.currentTime + delay;
    const o = this.ctx.createOscillator(), g = this.ctx.createGain();
    o.type = type; o.frequency.setValueAtTime(freq, t);
    if (freqEnd !== undefined) o.frequency.exponentialRampToValueAtTime(Math.max(20, freqEnd), t + dur);
    g.gain.setValueAtTime(0, t);
    g.gain.linearRampToValueAtTime(vol, t + attack);
    g.gain.exponentialRampToValueAtTime(0.0001, t + dur);
    o.connect(g); g.connect(this.master);
    o.start(t); o.stop(t + dur + 0.05);
  }
  _burst({ dur = 0.25, vol = 0.3, freq = 1200, delay = 0, q = 1 }) {
    if (!this.ctx || this.muted) return;
    const t = this.ctx.currentTime + delay;
    const src = this.ctx.createBufferSource(); src.buffer = this._noise();
    const f = this.ctx.createBiquadFilter(); f.type = 'bandpass'; f.frequency.value = freq; f.Q.value = q;
    const g = this.ctx.createGain();
    g.gain.setValueAtTime(vol, t);
    g.gain.exponentialRampToValueAtTime(0.0001, t + dur);
    src.connect(f); f.connect(g); g.connect(this.master);
    src.start(t); src.stop(t + dur + 0.05);
  }
  // --- game sounds ---
  paddle(hit = 1) { this._tone({ freq: 220 + hit * 60, freqEnd: 160, type: 'triangle', dur: 0.08, vol: 0.3 }); }
  smash() { this._tone({ freq: 300, freqEnd: 520, type: 'square', dur: 0.14, vol: 0.22 }); this._burst({ dur: 0.1, vol: 0.15, freq: 2400 }); }
  clank() { this._tone({ freq: 1750, freqEnd: 900, type: 'square', dur: 0.09, vol: 0.16 }); this._tone({ freq: 2380, freqEnd: 1750, type: 'square', dur: 0.07, vol: 0.09 }); }
  bounce(combo = 0, base = 340) {
    const step = Math.min(combo, 16);
    this._tone({ freq: base * Math.pow(2, step / 12), type: 'square', dur: 0.09, vol: 0.2 });
  }
  wall() { this._tone({ freq: 190, freqEnd: 150, type: 'triangle', dur: 0.06, vol: 0.22 }); }
  explosion() {
    this._burst({ dur: 0.5, vol: 0.5, freq: 320, q: 0.6 });
    this._tone({ freq: 90, freqEnd: 34, type: 'sine', dur: 0.5, vol: 0.5 });
  }
  pickup() { this._tone({ freq: 520, type: 'sine', dur: 0.09, vol: 0.25 }); this._tone({ freq: 780, type: 'sine', dur: 0.1, vol: 0.25, delay: 0.07 }); this._tone({ freq: 1040, type: 'sine', dur: 0.14, vol: 0.25, delay: 0.14 }); }
  badPickup() { this._tone({ freq: 300, freqEnd: 120, type: 'sawtooth', dur: 0.3, vol: 0.22 }); }
  life() { [523, 659, 784, 1047].forEach((f, i) => this._tone({ freq: f, type: 'triangle', dur: 0.16, vol: 0.25, delay: i * 0.09 })); }
  loseLife() { [420, 320, 220].forEach((f, i) => this._tone({ freq: f, freqEnd: f * 0.7, type: 'sawtooth', dur: 0.22, vol: 0.22, delay: i * 0.14 })); }
  gameOver() { [392, 311, 233, 155].forEach((f, i) => this._tone({ freq: f, type: 'sawtooth', dur: 0.4, vol: 0.2, delay: i * 0.22 })); }
  levelWin() { [523, 659, 784, 1047, 1319].forEach((f, i) => this._tone({ freq: f, type: 'triangle', dur: 0.24, vol: 0.26, delay: i * 0.1 })); }
  frenzyStart() {
    this._tone({ freq: 130, freqEnd: 780, type: 'sawtooth', dur: 0.9, vol: 0.3 });
    this._burst({ dur: 0.9, vol: 0.3, freq: 900, q: 0.7 });
  }
  frenzyBreak(mult) {
    const f = 300 + Math.min(mult, 8) * 90;
    this._tone({ freq: f, type: 'square', dur: 0.08, vol: 0.16 });
  }
  // Continuous frenzy riser (looping filtered noise + LFO'd osc, gain tied to mult)
  setFrenzy(mult) {
    if (!this.ctx) return;
    if (mult > 1 && !this.frenzyOsc) {
      this.frenzyOsc = this.ctx.createOscillator();
      this.frenzyGain = this.ctx.createGain();
      const lfo = this.ctx.createOscillator(), lfoG = this.ctx.createGain();
      lfo.frequency.value = 3; lfoG.gain.value = 40;
      this.frenzyOsc.type = 'sawtooth'; this.frenzyOsc.frequency.value = 55;
      lfo.connect(lfoG); lfoG.connect(this.frenzyOsc.frequency);
      this.frenzyOsc.connect(this.frenzyGain); this.frenzyGain.connect(this.master);
      this.frenzyOsc.start(); lfo.start();
      this._frenzyLfo = lfo;
      this.frenzyGain.gain.value = 0;
    }
    if (this.frenzyGain) {
      const t = this.ctx.currentTime;
      const target = mult > 1 ? Math.min(0.06 * mult, 0.16) : 0;
      this.frenzyGain.gain.setTargetAtTime(this.muted ? 0 : target, t, 0.2);
      if (this.frenzyOsc) this.frenzyOsc.frequency.setTargetAtTime(55 + mult * 12, t, 0.3);
    }
    if (mult <= 1 && this.frenzyOsc) {
      const osc = this.frenzyOsc, lfo = this._frenzyLfo;
      this.frenzyGain.gain.setTargetAtTime(0, this.ctx.currentTime, 0.3);
      setTimeout(() => { try { osc.stop(); lfo.stop(); } catch (e) {} }, 900);
      this.frenzyOsc = null; this.frenzyGain = null; this._frenzyLfo = null;
    }
  }
}
