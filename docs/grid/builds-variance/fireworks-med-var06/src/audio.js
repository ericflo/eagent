// Procedural WebAudio: SFX + layered music loop. No assets, starts on first gesture.
export class AudioSys {
  constructor() {
    this.ctx = null;
    this.enabled = true;       // user setting
    this.started = false;      // context created
    this.intensity = 0;        // 0..1 drives music layers
    this.beatPulse = 0;        // 0..1 flash for visuals
    this._nextStep = 0;
    this._step = 0;
    this._bpm = 116;
  }

  ensure() {
    if (this.started) return;
    const AC = window.AudioContext || window.webkitAudioContext;
    if (!AC) return;
    try { this.ctx = new AC(); } catch (e) { return; }
    this.master = this.ctx.createGain();
    this.master.gain.value = 0.55;
    this.master.connect(this.ctx.destination);
    // music bus (so sfx stay audible when music layers up)
    this.musicGain = this.ctx.createGain();
    this.musicGain.gain.value = 0.5;
    this.musicGain.connect(this.master);
    // reusable noise buffer
    const len = this.ctx.sampleRate * 0.3;
    this.noise = this.ctx.createBuffer(1, len, this.ctx.sampleRate);
    const d = this.noise.getChannelData(0);
    for (let i = 0; i < len; i++) d[i] = Math.random() * 2 - 1;
    this.started = true;
    this._nextStep = this.ctx.currentTime + 0.08;
  }

  resume() { if (this.ctx && this.ctx.state === 'suspended') this.ctx.resume(); }

  get on() { return this.enabled && this.started; }

  setEnabled(v) {
    this.enabled = v;
    if (this.master) this.master.gain.value = v ? 0.55 : 0;
  }

  // ---- low-level voices -------------------------------------------------
  blip(freq, dur, type, vol, slide = 0, dest) {
    if (!this.on) return;
    const t = this.ctx.currentTime;
    const o = this.ctx.createOscillator();
    const g = this.ctx.createGain();
    o.type = type; o.frequency.setValueAtTime(freq, t);
    if (slide) o.frequency.exponentialRampToValueAtTime(Math.max(20, freq + slide), t + dur);
    g.gain.setValueAtTime(vol, t);
    g.gain.exponentialRampToValueAtTime(0.001, t + dur);
    o.connect(g); g.connect(dest || this.master);
    o.start(t); o.stop(t + dur + 0.02);
  }

  noiseHit(dur, vol, hp, dest) {
    if (!this.on) return;
    const t = this.ctx.currentTime;
    const s = this.ctx.createBufferSource();
    s.buffer = this.noise;
    const f = this.ctx.createBiquadFilter();
    f.type = 'highpass'; f.frequency.value = hp;
    const g = this.ctx.createGain();
    g.gain.setValueAtTime(vol, t);
    g.gain.exponentialRampToValueAtTime(0.001, t + dur);
    s.connect(f); f.connect(g); g.connect(dest || this.master);
    s.start(t); s.stop(t + dur + 0.02);
  }

  // ---- sfx ---------------------------------------------------------------
  bounce(speedN) { this.blip(200 + 260 * speedN, 0.06, 'sine', 0.22, 60); }
  clank() {
    this.blip(92, 0.09, 'square', 0.18, -30);
    this.noiseHit(0.05, 0.10, 1800);
  }
  brick(combo) {
    const p = Math.min(combo, 20);
    this.blip(320 * Math.pow(2, p / 12), 0.09, 'square', 0.20, 120);
    this.noiseHit(0.07, 0.14, 900);
  }
  powerup() {
    [523, 659, 784].forEach((f, i) =>
      setTimeout(() => this.blip(f, 0.12, 'triangle', 0.22), i * 70));
  }
  multUp(tier) { this.blip(300 + tier * 90, 0.28, 'sawtooth', 0.18, 500 + tier * 140); }
  loseLife() {
    this.blip(300, 0.4, 'sawtooth', 0.22, -220);
    setTimeout(() => this.blip(180, 0.5, 'sawtooth', 0.2, -120), 160);
  }
  levelClear() {
    [523, 659, 784, 1047].forEach((f, i) =>
      setTimeout(() => this.blip(f, 0.25, 'triangle', 0.25), i * 130));
  }
  launch() { this.blip(240, 0.12, 'sine', 0.2, 240); }

  // ---- music scheduler (16th-note lookahead) -----------------------------
  update() {
    if (!this.on) return;
    const stepDur = 60 / (this._bpm * 4);
    while (this._nextStep < this.ctx.currentTime + 0.12) {
      this._playStep(this._step % 16, this._nextStep);
      this._nextStep += stepDur;
      this._step++;
    }
    this.beatPulse *= 0.9;
  }

  _playStep(s, when) {
    const inten = this.intensity;
    const g = this.musicGain;
    const t = when;
    // kick: every beat (intensity > 0.05)
    if (s % 4 === 0 && inten > 0.02) {
      const o = this.ctx.createOscillator(), gg = this.ctx.createGain();
      o.type = 'sine';
      o.frequency.setValueAtTime(130, t);
      o.frequency.exponentialRampToValueAtTime(38, t + 0.11);
      gg.gain.setValueAtTime(0.5 * Math.min(1, inten * 2), t);
      gg.gain.exponentialRampToValueAtTime(0.001, t + 0.16);
      o.connect(gg); gg.connect(g); o.start(t); o.stop(t + 0.2);
      if (s === 0) {
        // flag the downbeat slightly early for visuals
        const dtms = Math.max(0, (t - this.ctx.currentTime) * 1000 - 16);
        setTimeout(() => { this.beatPulse = 1; }, dtms);
      }
    }
    // hats: offbeats, layer in at intensity > 0.35
    if (s % 4 === 2 && inten > 0.35) {
      const src = this.ctx.createBufferSource(); src.buffer = this.noise;
      const f = this.ctx.createBiquadFilter(); f.type = 'highpass'; f.frequency.value = 7000;
      const gg = this.ctx.createGain();
      gg.gain.setValueAtTime(0.07 + 0.05 * inten, t);
      gg.gain.exponentialRampToValueAtTime(0.001, t + 0.05);
      src.connect(f); f.connect(gg); gg.connect(g); src.start(t); src.stop(t + 0.08);
    }
    // bass line, layer in at > 0.55
    if (inten > 0.55) {
      const notes = [55, 55, 0, 65.4, 55, 0, 49, 0];
      const f0 = notes[(s >> 1) % 8];
      if (f0 && s % 2 === 0) {
        const o = this.ctx.createOscillator(), gg = this.ctx.createGain();
        o.type = 'triangle'; o.frequency.value = f0 * (inten > 0.85 && s % 8 === 6 ? 1.5 : 1);
        gg.gain.setValueAtTime(0.22, t);
        gg.gain.exponentialRampToValueAtTime(0.001, t + 0.18);
        o.connect(gg); gg.connect(g); o.start(t); o.stop(t + 0.22);
      }
    }
    // arp, top layer > 0.8
    if (inten > 0.8) {
      const scale = [440, 523, 659, 784, 880, 1047];
      const f0 = scale[(s * 5) % scale.length] * (s % 8 >= 4 ? 1.5 : 1);
      const o = this.ctx.createOscillator(), gg = this.ctx.createGain();
      o.type = 'square'; o.frequency.value = f0;
      gg.gain.setValueAtTime(0.055, t);
      gg.gain.exponentialRampToValueAtTime(0.001, t + 0.1);
      o.connect(gg); gg.connect(g); o.start(t); o.stop(t + 0.12);
    }
  }
}
