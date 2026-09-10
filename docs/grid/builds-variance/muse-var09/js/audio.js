/* OVER THE TOP — WebAudio synth manager. No audio files, fully offline. */
(function (global) {
  'use strict';
  class AudioManager {
    constructor() {
      this.ctx = null; this.master = null; this.musicGain = null; this.sfxGain = null;
      this.muted = false; this.intensity = 0; this._musicTimer = null;
      this._step = 0; this._shimmerTimer = 0;
      const m = localStorage.getItem('ott_muted');
      if (m === '1') this.muted = true;
    }
    init() {
      if (this.ctx) { if (this.ctx.state === 'suspended') this.ctx.resume(); return; }
      const AC = window.AudioContext || window.webkitAudioContext;
      if (!AC) return;
      this.ctx = new AC();
      this.master = this.ctx.createGain();
      this.master.gain.value = this.muted ? 0 : 0.9;
      this.master.connect(this.ctx.destination);
      this.sfxGain = this.ctx.createGain(); this.sfxGain.gain.value = 0.9; this.sfxGain.connect(this.master);
      this.musicGain = this.ctx.createGain(); this.musicGain.gain.value = 0.30; this.musicGain.connect(this.master);
      if (this.ctx.state === 'suspended') this.ctx.resume();
      this.startMusic();
    }
    setMuted(m) {
      this.muted = !!m;
      localStorage.setItem('ott_muted', this.muted ? '1' : '0');
      if (this.master && this.ctx) this.master.gain.setTargetAtTime(this.muted ? 0 : 0.9, this.ctx.currentTime, 0.02);
    }
    toggleMute() { this.init(); this.setMuted(!this.muted); return this.muted; }
    setIntensity(x) { this.intensity = Math.max(0, Math.min(1, x)); }

    _now() { return this.ctx ? this.ctx.currentTime : 0; }
    _env(g, t, a, peak, d) {
      g.gain.setValueAtTime(0.0001, t);
      g.gain.exponentialRampToValueAtTime(Math.max(peak, 0.0002), t + a);
      g.gain.exponentialRampToValueAtTime(0.0001, t + a + d);
    }
    tone(freq, dur, type, vol, slideTo, delay) {
      if (!this.ctx || this.muted) return;
      type = type || 'sine'; vol = vol == null ? 0.35 : vol; delay = delay || 0;
      const t = this._now() + delay;
      const o = this.ctx.createOscillator(), g = this.ctx.createGain();
      o.type = type; o.frequency.setValueAtTime(freq, t);
      if (slideTo) o.frequency.exponentialRampToValueAtTime(Math.max(20, slideTo), t + dur);
      this._env(g, t, 0.008, vol, dur);
      o.connect(g); g.connect(this.sfxGain);
      o.start(t); o.stop(t + dur + 0.15);
    }
    noise(dur, vol, filterFreq, delay) {
      if (!this.ctx || this.muted) return;
      dur = dur || 0.3; vol = vol == null ? 0.4 : vol; delay = delay || 0;
      const t = this._now() + delay;
      const len = Math.floor(this.ctx.sampleRate * dur);
      const buf = this.ctx.createBuffer(1, len, this.ctx.sampleRate);
      const d = buf.getChannelData(0);
      for (let i = 0; i < len; i++) d[i] = (Math.random() * 2 - 1) * (1 - i / len);
      const src = this.ctx.createBufferSource(); src.buffer = buf;
      const f = this.ctx.createBiquadFilter(); f.type = 'lowpass'; f.frequency.value = filterFreq || 2000;
      const g = this.ctx.createGain(); this._env(g, t, 0.005, vol, dur);
      src.connect(f); f.connect(g); g.connect(this.sfxGain);
      src.start(t);
    }
    paddle(power) {
      const f = power ? 660 : 440;
      this.tone(f, 0.09, 'square', power ? 0.30 : 0.20, f * 1.5);
      if (power) { this.noise(0.12, 0.25, 4000); this.tone(1320, 0.15, 'sawtooth', 0.15, 1800, 0.02); }
    }
    wall() { this.tone(300, 0.06, 'square', 0.14, 380); }
    brick(combo) {
      const base = 380 * Math.pow(2, Math.min(combo, 24) / 12);
      this.tone(base, 0.12, 'triangle', 0.35, base * 1.6);
      this.tone(base * 2, 0.08, 'sine', 0.18, base * 2.4, 0.02);
    }
    shield() {
      this.tone(180, 0.12, 'sawtooth', 0.28, 120);
      this.tone(2331, 0.05, 'square', 0.10);
      this.noise(0.06, 0.15, 6000);
    }
    explosion(big) {
      this.noise(big ? 0.7 : 0.45, big ? 0.6 : 0.45, big ? 900 : 1400);
      this.tone(big ? 90 : 120, big ? 0.6 : 0.4, 'sine', 0.5, 35);
      this.tone(1500, 0.2, 'sawtooth', 0.12, 200, 0.02);
    }
    powerup() {
      const seq = [523, 659, 784, 1046, 1318];
      seq.forEach((f, i) => this.tone(f, 0.14, 'triangle', 0.30, null, i * 0.06));
    }
    powerdown() { this.tone(400, 0.3, 'sawtooth', 0.2, 120); }
    launch() { this.tone(300, 0.18, 'sawtooth', 0.25, 900); }
    loseLife() {
      const seq = [600, 500, 400, 280, 170];
      seq.forEach((f, i) => this.tone(f, 0.22, 'sawtooth', 0.28, f * 0.9, i * 0.11));
      this.noise(0.5, 0.2, 800, 0.1);
    }
    fanfare() {
      const seq = [523, 659, 784, 1046, 784, 1046, 1318, 1568];
      seq.forEach((f, i) => this.tone(f, 0.22, 'triangle', 0.32, null, i * 0.11));
      this.noise(0.6, 0.12, 8000, 0.5);
    }
    topZoneShimmer() {
      if (!this.ctx || this.muted) return;
      const now = performance.now();
      if (now - this._shimmerTimer < 380) return;
      this._shimmerTimer = now;
      const f = 1800 + Math.random() * 1600;
      this.tone(f, 0.25, 'sine', 0.07, f * 1.8);
    }
    uiClick() { this.tone(880, 0.06, 'sine', 0.2, 1200); }

    /* --- adaptive background pulse: bass + hats, faster/denser with intensity --- */
    startMusic() {
      if (this._musicTimer || !this.ctx) return;
      const bassLine = [55, 55, 65.4, 49, 55, 55, 82.4, 73.4]; // A1 pattern
      this._musicTimer = setInterval(() => {
        if (!this.ctx || this.muted || this.ctx.state !== 'running') return;
        const t = this._now();
        const inten = this.intensity;
        const bpm = 96 + inten * 56;
        const beat = 60 / bpm / 2;
        const root = bassLine[this._step % bassLine.length];
        // bass thump
        const o = this.ctx.createOscillator(), g = this.ctx.createGain();
        o.type = 'sawtooth'; o.frequency.setValueAtTime(root * 2, t);
        g.gain.setValueAtTime(0.0001, t);
        g.gain.exponentialRampToValueAtTime(0.10 + inten * 0.16, t + 0.01);
        g.gain.exponentialRampToValueAtTime(0.0001, t + beat * 1.4);
        const f = this.ctx.createBiquadFilter(); f.type = 'lowpass';
        f.frequency.value = 300 + inten * 1400;
        o.connect(f); f.connect(g); g.connect(this.musicGain);
        o.start(t); o.stop(t + beat * 1.6);
        // sparkle hat at high intensity
        if (inten > 0.35 && this._step % 2 === 0) {
          const len = Math.floor(this.ctx.sampleRate * 0.05);
          const buf = this.ctx.createBuffer(1, len, this.ctx.sampleRate);
          const dd = buf.getChannelData(0);
          for (let i = 0; i < len; i++) dd[i] = (Math.random() * 2 - 1) * (1 - i / len);
          const s = this.ctx.createBufferSource(); s.buffer = buf;
          const hf = this.ctx.createBiquadFilter(); hf.type = 'highpass'; hf.frequency.value = 6000;
          const hg = this.ctx.createGain(); hg.gain.value = 0.10 * inten;
          s.connect(hf); hf.connect(hg); hg.connect(this.musicGain); s.start(t);
        }
        // lead arp at max intensity
        if (inten > 0.7 && this._step % 4 === 0) {
          const scale = [220, 261.6, 329.6, 440, 523.25];
          const lf = scale[(this._step >> 2) % scale.length] * 2;
          const lo = this.ctx.createOscillator(), lg = this.ctx.createGain();
          lo.type = 'triangle'; lo.frequency.value = lf;
          lg.gain.setValueAtTime(0.0001, t);
          lg.gain.exponentialRampToValueAtTime(0.08, t + 0.02);
          lg.gain.exponentialRampToValueAtTime(0.0001, t + 0.3);
          lo.connect(lg); lg.connect(this.musicGain); lo.start(t); lo.stop(t + 0.4);
        }
        this._step++;
      }, 210);
    }
  }
  global.AudioManager = AudioManager;
})(window);
