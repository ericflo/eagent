// Procedural WebAudio sound engine — no audio files, everything synthesized.
// A tiny "mixer" with a master gain, a compressor, and a noise buffer.

export class Audio {
  constructor() {
    this.ctx = null;
    this.master = null;
    this.noiseBuf = null;
    this.muted = false;
    this._unlocked = false;
  }

  unlock() {
    if (this._unlocked) return;
    this._unlocked = true;
    try {
      this.ctx = new (window.AudioContext || window.webkitAudioContext)();
      const comp = this.ctx.createDynamicsCompressor();
      comp.threshold.value = -18;
      comp.ratio.value = 8;
      comp.connect(this.ctx.destination);
      this.master = this.ctx.createGain();
      this.master.gain.value = this.muted ? 0 : 0.5;
      this.master.connect(comp);
      // 1s of white noise, reused for percussive sounds.
      const len = this.ctx.sampleRate;
      this.noiseBuf = this.ctx.createBuffer(1, len, this.ctx.sampleRate);
      const d = this.noiseBuf.getChannelData(0);
      for (let i = 0; i < len; i++) d[i] = Math.random() * 2 - 1;
      if (this.ctx.state === 'suspended') this.ctx.resume();
    } catch (e) { /* audio unavailable */ }
  }

  setMuted(m) {
    this.muted = m;
    if (this.master) this.master.gain.value = m ? 0 : 0.5;
  }

  _tone({ type = 'sine', f0 = 440, f1 = null, dur = 0.15, vol = 0.3, delay = 0, curve = 'exp' }) {
    if (!this.ctx || this.muted) return;
    const t0 = this.ctx.currentTime + delay;
    const osc = this.ctx.createOscillator();
    const g = this.ctx.createGain();
    osc.type = type;
    osc.frequency.setValueAtTime(f0, t0);
    if (f1 != null) {
      if (curve === 'exp') osc.frequency.exponentialRampToValueAtTime(Math.max(f1, 1), t0 + dur);
      else osc.frequency.linearRampToValueAtTime(f1, t0 + dur);
    }
    g.gain.setValueAtTime(0.0001, t0);
    g.gain.exponentialRampToValueAtTime(vol, t0 + 0.008);
    g.gain.exponentialRampToValueAtTime(0.0001, t0 + dur);
    osc.connect(g); g.connect(this.master);
    osc.start(t0); osc.stop(t0 + dur + 0.02);
  }

  _noise({ dur = 0.1, vol = 0.2, f0 = 800, f1 = 200, delay = 0, q = 1 }) {
    if (!this.ctx || this.muted) return;
    const t0 = this.ctx.currentTime + delay;
    const src = this.ctx.createBufferSource();
    src.buffer = this.noiseBuf;
    src.loop = true;
    const filt = this.ctx.createBiquadFilter();
    filt.type = 'bandpass';
    filt.Q.value = q;
    filt.frequency.setValueAtTime(f0, t0);
    filt.frequency.exponentialRampToValueAtTime(Math.max(f1, 20), t0 + dur);
    const g = this.ctx.createGain();
    g.gain.setValueAtTime(0.0001, t0);
    g.gain.exponentialRampToValueAtTime(vol, t0 + 0.005);
    g.gain.exponentialRampToValueAtTime(0.0001, t0 + dur);
    src.connect(filt); filt.connect(g); g.connect(this.master);
    src.start(t0); src.stop(t0 + dur + 0.02);
  }

  // ---- game events --------------------------------------------------------

  paddle() { this._tone({ type: 'triangle', f0: 300, f1: 420, dur: 0.07, vol: 0.25 }); }
  wall()   { this._tone({ type: 'sine', f0: 240, f1: 200, dur: 0.05, vol: 0.12 }); }
  brick()  { this._tone({ type: 'square', f0: 520, f1: 300, dur: 0.08, vol: 0.16 }); this._noise({ dur: 0.06, vol: 0.1, f0: 2000, f1: 600 }); }
  steel()  { this._tone({ type: 'square', f0: 180, f1: 120, dur: 0.09, vol: 0.2 }); this._noise({ dur: 0.08, vol: 0.18, f0: 900, f1: 300 }); }
  angle()  { this._tone({ type: 'sawtooth', f0: 700, f1: 1100, dur: 0.1, vol: 0.14 }); }
  speed()  { this._tone({ type: 'sawtooth', f0: 200, f1: 900, dur: 0.12, vol: 0.14 }); }
  explode(){ this._noise({ dur: 0.4, vol: 0.4, f0: 500, f1: 60, q: 0.6 }); this._tone({ type: 'sine', f0: 120, f1: 40, dur: 0.35, vol: 0.3 }); }
  golden() { [880, 1108, 1318, 1760].forEach((f, i) => this._tone({ type: 'triangle', f0: f, dur: 0.12, vol: 0.2, delay: i * 0.05 })); }
  core()   { [523, 659, 784, 1046, 1318].forEach((f, i) => this._tone({ type: 'triangle', f0: f, dur: 0.16, vol: 0.22, delay: i * 0.07 })); }
  powerup(){ this._tone({ type: 'sine', f0: 500, f1: 1000, dur: 0.12, vol: 0.2 }); this._tone({ type: 'sine', f0: 1000, f1: 1600, dur: 0.12, vol: 0.15, delay: 0.08 }); }
  laser()  { this._tone({ type: 'sawtooth', f0: 1400, f1: 300, dur: 0.09, vol: 0.12 }); }
  life()   { [660, 880, 1320].forEach((f, i) => this._tone({ type: 'triangle', f0: f, dur: 0.14, vol: 0.2, delay: i * 0.09 })); }
  lose()   { this._tone({ type: 'sawtooth', f0: 300, f1: 60, dur: 0.5, vol: 0.25 }); this._noise({ dur: 0.4, vol: 0.2, f0: 400, f1: 80 }); }
  level()  { [392, 523, 659, 784].forEach((f, i) => this._tone({ type: 'triangle', f0: f, dur: 0.18, vol: 0.22, delay: i * 0.1 })); }
  gameover(){ [440, 349, 293, 220].forEach((f, i) => this._tone({ type: 'sawtooth', f0: f, dur: 0.3, vol: 0.18, delay: i * 0.22 })); }
  rooftop() { [523, 659, 784, 1046, 1318, 1568].forEach((f, i) => this._tone({ type: 'triangle', f0: f, dur: 0.2, vol: 0.2, delay: i * 0.06 })); }
  combo(n) {
    const base = 300 + Math.min(n, 12) * 60;
    this._tone({ type: 'square', f0: base, f1: base * 1.5, dur: 0.09, vol: 0.14 });
  }
  ui() { this._tone({ type: 'sine', f0: 600, f1: 800, dur: 0.06, vol: 0.12 }); }
}
