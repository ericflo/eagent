/* audio.js — synthesized WebAudio SFX + adaptive OTT riser. No assets. */
(function () {
  'use strict';
  const AudioSys = {
    ctx: null, master: null, muted: false, riserNodes: null, ottLevel: 0,
    init() {
      if (this.ctx) { if (this.ctx.state === 'suspended') this.ctx.resume(); return; }
      try {
        const AC = window.AudioContext || window.webkitAudioContext;
        if (!AC) return;
        this.ctx = new AC();
        this.master = this.ctx.createGain();
        this.master.gain.value = this.muted ? 0 : 0.5;
        this.master.connect(this.ctx.destination);
      } catch (e) { /* audio unavailable */ }
    },
    setMuted(m) {
      this.muted = m;
      if (this.master && this.ctx) this.master.gain.setValueAtTime(m ? 0 : 0.5, this.ctx.currentTime);
    },
    now() { return this.ctx ? this.ctx.currentTime : 0; },
    // generic blip: freq sweep + decay envelope
    tone(freq, dur, type, vol, slideTo, delay) {
      if (!this.ctx || this.muted) return;
      type = type || 'sine'; vol = vol == null ? 0.3 : vol; delay = delay || 0;
      const t = this.now() + delay;
      const o = this.ctx.createOscillator(), g = this.ctx.createGain();
      o.type = type; o.frequency.setValueAtTime(freq, t);
      if (slideTo) o.frequency.exponentialRampToValueAtTime(Math.max(20, slideTo), t + dur);
      g.gain.setValueAtTime(0.0001, t);
      g.gain.exponentialRampToValueAtTime(vol, t + 0.008);
      g.gain.exponentialRampToValueAtTime(0.0001, t + dur);
      o.connect(g); g.connect(this.master);
      o.start(t); o.stop(t + dur + 0.05);
    },
    noise(dur, vol, filterFreq, delay) {
      if (!this.ctx || this.muted) return;
      dur = dur || 0.2; vol = vol || 0.25; filterFreq = filterFreq || 2000; delay = delay || 0;
      const t = this.now() + delay;
      const len = Math.max(1, Math.floor(this.ctx.sampleRate * dur));
      const buf = this.ctx.createBuffer(1, len, this.ctx.sampleRate);
      const d = buf.getChannelData(0);
      for (let i = 0; i < len; i++) d[i] = (Math.random() * 2 - 1) * (1 - i / len);
      const src = this.ctx.createBufferSource(); src.buffer = buf;
      const f = this.ctx.createBiquadFilter(); f.type = 'lowpass'; f.frequency.value = filterFreq;
      const g = this.ctx.createGain(); g.gain.value = vol;
      src.connect(f); f.connect(g); g.connect(this.master);
      src.start(t);
    },
    paddle(hitPos) {
      // pitch by hit position: edges higher
      const f = 300 + Math.abs(hitPos) * 260;
      this.tone(f, 0.09, 'square', 0.18, f * 1.5);
      this.tone(f * 2, 0.06, 'sine', 0.1);
    },
    smash() { this.noise(0.18, 0.3, 3000); this.tone(150, 0.25, 'sawtooth', 0.25, 600); },
    wall() { this.tone(220, 0.06, 'square', 0.12, 180); },
    brick(combo, mult) {
      // pitch ladder by combo, extra shimmer at high mult
      const base = 420 + Math.min(combo, 16) * 45;
      this.tone(base, 0.12, 'square', 0.2, base * 1.6);
      this.tone(base * 1.5, 0.1, 'sine', 0.14, base * 2, 0.02);
      if (mult >= 4) { this.tone(base * 2.5, 0.14, 'triangle', 0.12, base * 3, 0.04); this.noise(0.08, 0.08, 8000, 0.02); }
      if (mult >= 8) { this.tone(base * 3, 0.12, 'sine', 0.1, base * 4, 0.06); }
    },
    clank() { this.tone(140, 0.14, 'square', 0.2, 90); this.noise(0.06, 0.12, 900); },
    powerup() {
      const seq = [523, 659, 784, 1047];
      for (let i = 0; i < seq.length; i++) this.tone(seq[i], 0.14, 'square', 0.16, null, i * 0.07);
    },
    launch() { this.tone(250, 0.18, 'sawtooth', 0.2, 700); },
    laser() { this.tone(1400, 0.1, 'sawtooth', 0.12, 300); },
    explosion() { this.noise(0.5, 0.4, 1200); this.tone(90, 0.4, 'sawtooth', 0.3, 35); },
    loseLife() {
      const seq = [400, 320, 240, 150];
      for (let i = 0; i < seq.length; i++) this.tone(seq[i], 0.2, 'sawtooth', 0.2, seq[i] * 0.9, i * 0.13);
    },
    banked() {
      const seq = [784, 988, 1175, 1568];
      for (let i = 0; i < seq.length; i++) this.tone(seq[i], 0.16, 'triangle', 0.2, null, i * 0.06);
      this.noise(0.3, 0.1, 9000, 0.1);
    },
    levelClear() {
      const seq = [523, 659, 784, 1047, 1319, 1568];
      for (let i = 0; i < seq.length; i++) this.tone(seq[i], 0.22, 'triangle', 0.2, null, i * 0.1);
    },
    victory() {
      const seq = [523, 659, 784, 1047, 784, 1047, 1319, 2093];
      for (let i = 0; i < seq.length; i++) this.tone(seq[i], 0.25, 'square', 0.15, null, i * 0.14);
    },
    uiClick() { this.tone(700, 0.06, 'sine', 0.15, 900); },
    // continuous riser while over-the-top; level 0..1 controls pitch/rate
    riserStart() {
      if (!this.ctx || this.muted || this.riserNodes) return;
      const o = this.ctx.createOscillator(), g = this.ctx.createGain(), lfo = this.ctx.createOscillator(), lg = this.ctx.createGain();
      o.type = 'sawtooth'; o.frequency.value = 120;
      lfo.type = 'sine'; lfo.frequency.value = 8; lg.gain.value = 60;
      lfo.connect(lg); lg.connect(o.frequency);
      g.gain.value = 0.05;
      o.connect(g); g.connect(this.master);
      o.start(); lfo.start();
      this.riserNodes = { o, g, lfo };
    },
    riserUpdate(level) {
      if (!this.riserNodes || !this.ctx) return;
      this.ottLevel = level;
      const t = this.now();
      this.riserNodes.o.frequency.setTargetAtTime(120 + level * 500, t, 0.1);
      this.riserNodes.g.gain.setTargetAtTime(0.04 + level * 0.06, t, 0.1);
    },
    riserStop() {
      if (!this.riserNodes || !this.ctx) { this.riserNodes = null; return; }
      const n = this.riserNodes; this.riserNodes = null;
      try {
        n.g.gain.setTargetAtTime(0.0001, this.now(), 0.08);
        setTimeout(() => { try { n.o.stop(); n.lfo.stop(); } catch (e) {} }, 400);
      } catch (e) { this.riserNodes = null; }
    }
  };
  window.AudioSys = AudioSys;
})();
