'use strict';
/* ============================================================
   Attic Breakout — audio.js
   100% procedural WebAudio. No assets, no network.
   Lazily creates the AudioContext and unlocks it on the first
   user gesture. Every sound is a tiny synth patch.
   ============================================================ */

class AudioSys {
  constructor() {
    this.ctx = null;
    this.master = null;
    this.musicGain = null;
    this.muted = !!storageGet('ab.muted', false);
    this.unlocked = false;
    this._humOsc = null;
    this._humGain = null;
    this._arpTimer = null;
    this._arpStep = 0;
    this._arpTier = 0;
    this._arpOn = false;
    this._lastPaddlePitch = 0;
    this._slowActive = false;
  }

  /** Create/resume the context; call from any pointerdown/keydown. */
  unlock() {
    if (this.unlocked && this.ctx && this.ctx.state === 'running') return;
    try {
      if (!this.ctx) {
        const AC = window.AudioContext || window.webkitAudioContext;
        if (!AC) return;
        this.ctx = new AC();
        this.master = this.ctx.createGain();
        this.master.gain.value = this.muted ? 0 : 0.9;
        this.master.connect(this.ctx.destination);
        this.musicGain = this.ctx.createGain();
        this.musicGain.gain.value = 0.5;
        this.musicGain.connect(this.master);
      }
      if (this.ctx.state === 'suspended') this.ctx.resume();
      this.unlocked = true;
    } catch (e) { /* audio unavailable — play silent */ }
  }

  setMuted(m) {
    this.muted = m;
    storageSet('ab.muted', m);
    if (this.master) {
      this.master.gain.setTargetAtTime(m ? 0 : 0.9, this.now(), 0.02);
    }
  }
  toggleMute() { this.setMuted(!this.muted); return this.muted; }

  now() { return this.ctx ? this.ctx.currentTime : 0; }

  get t() { return this.now(); }

  // ---- primitive voices -------------------------------------------------

  /** Basic enveloped oscillator blip. */
  blip(freq, dur, { type = 'sine', vol = 0.3, slide = 0, delay = 0, dest } = {}) {
    if (!this.ctx || this.muted) return;
    const t0 = this.now() + delay;
    const o = this.ctx.createOscillator();
    const g = this.ctx.createGain();
    o.type = type;
    o.frequency.setValueAtTime(freq, t0);
    if (slide) o.frequency.exponentialRampToValueAtTime(Math.max(30, freq + slide), t0 + dur);
    g.gain.setValueAtTime(0, t0);
    g.gain.linearRampToValueAtTime(vol, t0 + 0.006);
    g.gain.exponentialRampToValueAtTime(0.0008, t0 + dur);
    o.connect(g); g.connect(dest || this.master);
    o.start(t0); o.stop(t0 + dur + 0.03);
  }

  /** Filtered noise burst — pops, hisses, whooshes, drops. */
  noise(dur, { vol = 0.3, freq = 1200, q = 1, type = 'bandpass', slide = 0, delay = 0 } = {}) {
    if (!this.ctx || this.muted) return;
    const t0 = this.now() + delay;
    const len = Math.max(1, Math.floor(this.ctx.sampleRate * dur));
    const buf = this.ctx.createBuffer(1, len, this.ctx.sampleRate);
    const d = buf.getChannelData(0);
    for (let i = 0; i < len; i++) d[i] = Math.random() * 2 - 1;
    const src = this.ctx.createBufferSource();
    src.buffer = buf;
    const f = this.ctx.createBiquadFilter();
    f.type = type; f.frequency.setValueAtTime(freq, t0); f.Q.value = q;
    if (slide) f.frequency.exponentialRampToValueAtTime(Math.max(40, freq + slide), t0 + dur);
    const g = this.ctx.createGain();
    g.gain.setValueAtTime(vol, t0);
    g.gain.exponentialRampToValueAtTime(0.001, t0 + dur);
    src.connect(f); f.connect(g); g.connect(this.master);
    src.start(t0); src.stop(t0 + dur + 0.02);
  }

  // ---- game sounds ------------------------------------------------------

  /** Paddle boop; pitch rises with the combo count. */
  paddleBoop(combo = 0, smash = false) {
    const p = 1 + Math.min(combo, 12) * 0.06;
    if (smash) {
      this.blip(180 * p, 0.3, { type: 'sawtooth', vol: 0.5, slide: 500 });
      this.noise(0.25, { vol: 0.35, freq: 900, slide: -600, type: 'lowpass' });
      this.blip(60, 0.35, { type: 'sine', vol: 0.55, slide: 30 }); // sub thump
    } else {
      this.blip(300 * p, 0.1, { type: 'triangle', vol: 0.35, slide: 140 });
    }
  }
  wallBoop() { this.blip(240, 0.06, { type: 'sine', vol: 0.22, slide: 60 }); }

  /** Brick pop; pitch mapped by row (higher rows = higher pitch). */
  brickPop(row = 4, armored = false) {
    const p = 1 + (8 - row) * 0.045;
    this.blip(420 * p, 0.14, { type: 'square', vol: 0.22, slide: 260 });
    this.noise(0.1, { vol: 0.18, freq: 2600 * p, q: 2 });
    if (armored) this.noise(0.14, { vol: 0.25, freq: 4200, q: 6 });
  }

  /** Metallic clang for a too-slow hit on armored brick. */
  clang() {
    [523, 611, 833, 1210].forEach((f, i) =>
      this.blip(f, 0.3, { type: 'triangle', vol: 0.12, slide: -30, delay: i * 0.004 }));
    this.noise(0.08, { vol: 0.2, freq: 5000, q: 8, type: 'highpass' });
  }

  shimmer() { this.blip(1600, 0.2, { type: 'sine', vol: 0.14, slide: 500 }); }

  crack() {
    this.noise(0.07, { vol: 0.25, freq: 800, q: 1.5 });
    this.blip(180, 0.1, { type: 'square', vol: 0.12, slide: -60 });
  }

  powerup(good = true) {
    if (good) {
      this.blip(520, 0.09, { type: 'square', vol: 0.2 });
      this.blip(660, 0.09, { type: 'square', vol: 0.2, delay: 0.07 });
      this.blip(880, 0.14, { type: 'square', vol: 0.22, delay: 0.14 });
    } else {
      this.blip(300, 0.18, { type: 'sawtooth', vol: 0.22, slide: -180 });
      this.blip(220, 0.22, { type: 'sawtooth', vol: 0.2, slide: -140, delay: 0.08 });
    }
  }

  launch() { this.noise(0.18, { vol: 0.2, freq: 500, slide: 1400, q: 2 }); }
  whoosh() { this.noise(0.3, { vol: 0.22, freq: 400, slide: 1800, q: 1.5 }); }
  split() {
    this.blip(700, 0.08, { type: 'square', vol: 0.2 });
    this.blip(1050, 0.1, { type: 'square', vol: 0.2, delay: 0.05 });
  }
  shieldBounce() {
    this.blip(140, 0.25, { type: 'sine', vol: 0.4, slide: 200 });
    this.noise(0.2, { vol: 0.2, freq: 700, q: 3 });
  }
  ballLost() {
    this.noise(0.7, { vol: 0.35, freq: 1400, slide: -1300, q: 1, type: 'lowpass' });
    this.blip(300, 0.7, { type: 'sawtooth', vol: 0.25, slide: -240 });
  }

  /** The FEVER jackpot moment: flash + bass drop + rising zing. */
  feverEntry() {
    this.blip(46, 1.1, { type: 'sine', vol: 0.75, slide: 40 });   // bass drop
    this.noise(0.9, { vol: 0.3, freq: 120, slide: 900, type: 'lowpass' });
    this.blip(1200, 0.5, { type: 'sine', vol: 0.2, slide: 1800, delay: 0.12 });
    [523, 659, 784, 1047].forEach((f, i) =>
      this.blip(f, 0.3, { type: 'triangle', vol: 0.16, delay: 0.15 + i * 0.08 }));
  }
  feverEnd() { this.blip(700, 0.4, { type: 'sine', vol: 0.2, slide: -450 }); }
  slowmo(on) {
    this.blip(on ? 600 : 300, 0.4, { type: 'sine', vol: 0.25, slide: on ? -450 : 300 });
  }

  // ---- ambient hum + fever arpeggio music layer -------------------------

  startHum() {
    if (!this.ctx || this._humOsc) return;
    try {
      this._humOsc = this.ctx.createOscillator();
      this._humGain = this.ctx.createGain();
      const lfo = this.ctx.createOscillator();
      const lfoG = this.ctx.createGain();
      this._humOsc.type = 'sine'; this._humOsc.frequency.value = 55;
      this._humGain.gain.value = 0.035;
      lfo.frequency.value = 0.13; lfoG.gain.value = 8;
      lfo.connect(lfoG); lfoG.connect(this._humOsc.frequency);
      this._humOsc.connect(this._humGain); this._humGain.connect(this.master);
      this._humOsc.start(); lfo.start();
      this._humLfo = lfo;
    } catch (e) { this._humOsc = null; }
  }
  stopHum() {
    try {
      if (this._humOsc) { this._humOsc.stop(); this._humOsc = null; }
      if (this._humLfo) { this._humLfo.stop(); this._humLfo = null; }
    } catch (e) { /* already stopped */ }
  }

  /** Arpeggio loop; tier 0 = off, 1..3 add notes/density. */
  setFeverTier(tier) {
    if (!this.ctx || this.muted) { this._arpTier = tier; return; }
    this._arpTier = tier;
    if (tier > 0 && !this._arpOn) {
      this._arpOn = true;
      this._arpTimer = setInterval(() => this._arpTick(), 125);
    } else if (tier === 0 && this._arpOn) {
      this._arpOff();
    }
  }
  _arpOff() {
    this._arpOn = false;
    if (this._arpTimer) { clearInterval(this._arpTimer); this._arpTimer = null; }
  }
  _arpTick() {
    if (!this._arpOn || this.muted || !this.ctx) return;
    const scale = [523.25, 587.33, 659.25, 783.99, 880, 1046.5, 1174.7, 1318.5];
    const s = this._arpStep++;
    const note = scale[s % scale.length];
    const tier = this._arpTier;
    this.blip(note, 0.12, { type: 'square', vol: tier >= 2 ? 0.09 : 0.06, dest: this.musicGain });
    if (tier >= 2 && s % 2 === 0) {
      this.blip(note / 2, 0.14, { type: 'triangle', vol: 0.07, dest: this.musicGain });
    }
    if (tier >= 3) {
      if (s % 4 === 0) this.noise(0.06, { vol: 0.1, freq: 6000, q: 3, type: 'highpass' });
      if (s % 8 === 0) this.blip(65, 0.18, { type: 'sine', vol: 0.3 });
    }
  }

  /** Stop every looping layer (pause / game over). */
  hushMusic() {
    this._arpOff();
    this._arpTier = 0;
    this.stopHum();
  }
}

window.AudioSys = AudioSys;
