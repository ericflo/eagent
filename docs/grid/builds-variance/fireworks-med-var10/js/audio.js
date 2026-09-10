// SKYBREAK — procedural WebAudio: layered music + SFX. No external assets.
//
// Music is a step sequencer at BPM. Layers (kick, hats, bass, arp, lead) unmute
// as hype tier rises; layer gains also scale with hype. Started only after a
// user gesture.

class AudioEngine {
  constructor() {
    this.ctx = null;
    this.muted = false;
    this.master = null;
    this.musicGain = null;
    this.sfxGain = null;
    this.timer = null;
    this.step = 0;
    this.nextTime = 0;
    this.bpm = 112;
    this.tier = 0;              // 0..4, set by game from hype multiplier
    this.beatFlash = 0;         // pulses to the kick, read by the renderer
    this._noise = null;
  }

  ensure() {
    if (this.ctx) return true;
    const AC = window.AudioContext || window.webkitAudioContext;
    if (!AC) return false;
    this.ctx = new AC();
    this.master = this.ctx.createGain();
    this.master.gain.value = this.muted ? 0 : 0.9;
    this.master.connect(this.ctx.destination);

    this.musicGain = this.ctx.createGain();
    this.musicGain.gain.value = 0.62;
    this.musicGain.connect(this.master);

    this.sfxGain = this.ctx.createGain();
    this.sfxGain.gain.value = 0.8;
    this.sfxGain.connect(this.master);

    this._makeNoiseBuffer();
    return true;
  }

  // Shared white-noise buffer for hats / noise SFX (1 second).
  _makeNoiseBuffer() {
    const len = this.ctx.sampleRate;
    this._noise = this.ctx.createBuffer(1, len, this.ctx.sampleRate);
    const data = this._noise.getChannelData(0);
    for (let i = 0; i < len; i++) data[i] = Math.random() * 2 - 1;
  }

  resume() {
    if (this.ctx && this.ctx.state === 'suspended') this.ctx.resume();
  }

  setMuted(m) {
    this.muted = m;
    if (this.master) {
      this.master.gain.setTargetAtTime(m ? 0 : 0.9, this.ctx.currentTime, 0.05);
    }
  }

  // ---------- music sequencer ----------

  startMusic() {
    if (!this.ensure()) return;
    this.resume();
    if (this.timer) return;
    this.step = 0;
    this.nextTime = this.ctx.currentTime + 0.08;
    // Poll frequently; each tick schedules any steps whose time has arrived.
    this.timer = setInterval(() => this._scheduler(), 40);
  }

  stopMusic() {
    if (this.timer) { clearInterval(this.timer); this.timer = null; }
    this.beatFlash = 0;
  }

  setTier(t) {
    this.tier = clamp(t | 0, 0, 4);
    if (this.ctx) {
      // Faster pulse when hyped.
      this.bpm = 112 + this.tier * 10;
    }
  }

  _scheduler() {
    if (!this.ctx) return;
    const stepDur = (60 / this.bpm) / 4; // 16th notes
    while (this.nextTime < this.ctx.currentTime + 0.15) {
      this._playStep(this.step, this.nextTime, stepDur);
      this.nextTime += stepDur;
      this.step = (this.step + 1) % 64;
    }
  }

  _playStep(s, t, dur) {
    const bar = (s / 16) | 0;
    const i16 = s % 16;
    const tier = this.tier;

    // Kick: every beat from tier 0
    if (i16 % 4 === 0) {
      this._kick(t);
      if (i16 === 0) {
        const delay = Math.max(0, (t - this.ctx.currentTime) * 1000);
        setTimeout(() => { this.beatFlash = 1; }, delay);
      }
    }
    // Hats: off-beats from tier 1, 16ths from tier 3
    if (tier >= 1 && (i16 % 4 === 2 || (tier >= 3 && i16 % 2 === 1))) this._hat(t, tier >= 3 ? 0.5 : 0.35);

    // Bass: simple minor riff, from tier 1
    if (tier >= 1) {
      const root = [55, 55, 65.4, 49][bar % 4];
      if ([0, 3, 6, 8, 11, 14].includes(i16)) {
        this._bass(t, dur * 1.6, root * ([3, 6, 11].includes(i16) ? 1.5 : 1));
      }
    }
    // Arp: 8ths, from tier 2
    if (tier >= 2) {
      const scale = [220, 261.6, 329.6, 392, 440, 523.2];
      if (i16 % 2 === 0) this._arp(t, dur * 1.2, scale[(s * 5) % scale.length] * (bar % 2 ? 1 : 1.122));
    }
    // Lead sparkle: tier 4
    if (tier >= 4 && (i16 === 6 || i16 === 12 || i16 === 0)) {
      const freq = [880, 1046.5, 1318.5][bar % 3];
      this._lead(t, freq * (i16 === 6 ? 1 : 0.749));
    }
  }

  _env(node, t, a, d, peak) {
    const g = this.ctx.createGain();
    g.gain.setValueAtTime(0.0001, t);
    g.gain.exponentialRampToValueAtTime(peak, t + a);
    g.gain.exponentialRampToValueAtTime(0.0001, t + a + d);
    node.connect(g);
    g.connect(this.musicGain);
    return g;
  }

  _kick(t) {
    const o = this.ctx.createOscillator();
    o.frequency.setValueAtTime(150, t);
    o.frequency.exponentialRampToValueAtTime(40, t + 0.12);
    this._env(o, t, 0.002, 0.16, 0.9);
    o.start(t); o.stop(t + 0.2);
  }

  _hat(t, vol) {
    const src = this.ctx.createBufferSource();
    src.buffer = this._noise;
    const hp = this.ctx.createBiquadFilter();
    hp.type = 'highpass'; hp.frequency.value = 7000;
    src.connect(hp);
    this._env(hp, t, 0.001, 0.05, vol * 0.4);
    src.start(t); src.stop(t + 0.07);
  }

  _bass(t, dur, freq) {
    const o = this.ctx.createOscillator();
    o.type = 'sawtooth';
    o.frequency.value = freq;
    const lp = this.ctx.createBiquadFilter();
    lp.type = 'lowpass';
    lp.frequency.setValueAtTime(900 + this.tier * 500, t);
    o.connect(lp);
    this._env(lp, t, 0.005, dur, 0.5);
    o.start(t); o.stop(t + dur + 0.05);
  }

  _arp(t, dur, freq) {
    const o = this.ctx.createOscillator();
    o.type = 'square';
    o.frequency.value = freq;
    const g = this.ctx.createGain();
    g.gain.setValueAtTime(0.0001, t);
    g.gain.exponentialRampToValueAtTime(0.10, t + 0.01);
    g.gain.exponentialRampToValueAtTime(0.0001, t + dur);
    o.connect(g); g.connect(this.musicGain);
    o.start(t); o.stop(t + dur + 0.02);
  }

  _lead(t, freq) {
    const o = this.ctx.createOscillator();
    o.type = 'triangle';
    o.frequency.value = freq;
    this._env(o, t, 0.01, 0.4, 0.16);
    o.start(t); o.stop(t + 0.5);
  }

  // ---------- SFX ----------

  _sfxOsc(type, f0, f1, dur, vol, t0) {
    const t = t0 ?? this.ctx.currentTime;
    const o = this.ctx.createOscillator();
    o.type = type;
    o.frequency.setValueAtTime(f0, t);
    if (f1 !== f0) o.frequency.exponentialRampToValueAtTime(Math.max(1, f1), t + dur);
    const g = this.ctx.createGain();
    g.gain.setValueAtTime(vol, t);
    g.gain.exponentialRampToValueAtTime(0.0001, t + dur);
    o.connect(g); g.connect(this.sfxGain);
    o.start(t); o.stop(t + dur + 0.02);
  }

  _sfxNoise(dur, vol, hpFreq, t0) {
    const t = t0 ?? this.ctx.currentTime;
    const src = this.ctx.createBufferSource();
    src.buffer = this._noise;
    const f = this.ctx.createBiquadFilter();
    f.type = 'highpass'; f.frequency.value = hpFreq;
    const g = this.ctx.createGain();
    g.gain.setValueAtTime(vol, t);
    g.gain.exponentialRampToValueAtTime(0.0001, t + dur);
    src.connect(f); f.connect(g); g.connect(this.sfxGain);
    src.start(t); src.stop(t + dur + 0.02);
  }

  // Named SFX called by the game. All tolerate audio not being initialized.
  paddleHit(strength = 0) {
    if (!this.ctx) return;
    this._sfxOsc('square', 240 + strength * 90, 140, 0.09, 0.35);
  }
  wallHit() {
    if (!this.ctx) return;
    this._sfxOsc('square', 170, 150, 0.06, 0.2);
  }
  brickBreak(pitch = 0) {
    if (!this.ctx) return;
    const base = 420 * Math.pow(1.0595, Math.min(pitch, 24));
    this._sfxOsc('square', base, base * 0.6, 0.1, 0.3);
    this._sfxNoise(0.08, 0.18, 2500);
  }
  brickReject() {
    if (!this.ctx) return;
    this._sfxOsc('sawtooth', 90, 60, 0.12, 0.28);
  }
  bomb() {
    if (!this.ctx) return;
    this._sfxNoise(0.5, 0.6, 300);
    this._sfxOsc('sine', 130, 30, 0.5, 0.6);
  }
  pickup() {
    if (!this.ctx) return;
    this._sfxOsc('sine', 520, 1040, 0.15, 0.35);
    this._sfxOsc('sine', 780, 1560, 0.18, 0.22);
  }
  laser() {
    if (!this.ctx) return;
    this._sfxOsc('sawtooth', 900, 300, 0.12, 0.22);
  }
  onTop() {
    if (!this.ctx) return;
    const t = this.ctx.currentTime;
    [523.2, 659.2, 784, 1046.5].forEach((f, i) => this._sfxOsc('triangle', f, f, 0.3, 0.3, t + i * 0.07));
  }
  milestone() {
    if (!this.ctx) return;
    const t = this.ctx.currentTime;
    [784, 988, 1318.5].forEach((f, i) => this._sfxOsc('square', f, f, 0.22, 0.26, t + i * 0.06));
  }
  loseBall() {
    if (!this.ctx) return;
    this._sfxOsc('sawtooth', 300, 60, 0.6, 0.4);
  }
  levelClear() {
    if (!this.ctx) return;
    const t = this.ctx.currentTime;
    [523.2, 659.2, 784, 1046.5, 1318.5].forEach((f, i) =>
      this._sfxOsc('square', f, f, 0.35, 0.28, t + i * 0.1));
  }
  gameOver() {
    if (!this.ctx) return;
    const t = this.ctx.currentTime;
    [392, 311, 233, 155.6].forEach((f, i) =>
      this._sfxOsc('sawtooth', f, f * 0.94, 0.5, 0.3, t + i * 0.18));
  }
  uiClick() {
    if (!this.ctx) return;
    this._sfxOsc('square', 700, 700, 0.05, 0.15);
  }
  launch() {
    if (!this.ctx) return;
    this._sfxOsc('square', 320, 640, 0.12, 0.25);
  }
  shieldBreak() {
    if (!this.ctx) return;
    this._sfxNoise(0.25, 0.4, 1200);
  }
}

const audio = new AudioEngine();
