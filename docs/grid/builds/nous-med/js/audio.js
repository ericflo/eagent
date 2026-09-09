// Procedural WebAudio synth. All sounds through a master gain (soft limiter).
export class Audio {
  constructor() {
    this.ctx = null;
    this.master = null;
    this.muted = localStorage.getItem('bt_mute') === '1';
    this.beatLayers = [];
    this.beatTimer = null;
    this.beatOn = false;
    this.intensity = 0; // 0..1 chaos escalation
  }

  ensure() {
    if (this.ctx) { if (this.ctx.state === 'suspended') this.ctx.resume(); return; }
    const AC = window.AudioContext || window.webkitAudioContext;
    if (!AC) return;
    this.ctx = new AC();
    this.master = this.ctx.createGain();
    this.master.gain.value = this.muted ? 0 : 0.55;
    // limiter-ish: waveshaper curve clamps
    const shaper = this.ctx.createWaveShaper();
    const curve = new Float32Array(257);
    for (let i = 0; i <= 256; i++) { const x = i / 128 - 1; curve[i] = Math.tanh(x * 1.4); }
    shaper.curve = curve;
    this.master.connect(shaper).connect(this.ctx.destination);
  }

  toggleMute() {
    this.muted = !this.muted;
    localStorage.setItem('bt_mute', this.muted ? '1' : '0');
    if (this.master) this.master.gain.value = this.muted ? 0 : 0.55;
    return this.muted;
  }

  // one-shot osc helper
  tone({ freq = 440, type = 'sine', dur = 0.12, vol = 0.3, slide = 0, delay = 0, attack = 0.004 }) {
    if (!this.ctx || this.muted) return;
    const t = this.ctx.currentTime + delay;
    const o = this.ctx.createOscillator(), g = this.ctx.createGain();
    o.type = type; o.frequency.setValueAtTime(freq, t);
    if (slide) o.frequency.exponentialRampToValueAtTime(Math.max(20, freq + slide), t + dur);
    g.gain.setValueAtTime(0, t);
    g.gain.linearRampToValueAtTime(vol, t + attack);
    g.gain.exponentialRampToValueAtTime(0.0001, t + dur);
    o.connect(g).connect(this.master);
    o.start(t); o.stop(t + dur + 0.02);
  }

  noise({ dur = 0.15, vol = 0.25, hp = 800, delay = 0 }) {
    if (!this.ctx || this.muted) return;
    const t = this.ctx.currentTime + delay;
    const len = Math.max(1, (dur * this.ctx.sampleRate) | 0);
    const buf = this.ctx.createBuffer(1, len, this.ctx.sampleRate);
    const d = buf.getChannelData(0);
    for (let i = 0; i < len; i++) d[i] = (Math.random() * 2 - 1) * (1 - i / len);
    const src = this.ctx.createBufferSource(); src.buffer = buf;
    const f = this.ctx.createBiquadFilter(); f.type = 'highpass'; f.frequency.value = hp;
    const g = this.ctx.createGain(); g.gain.value = vol;
    src.connect(f).connect(g).connect(this.master);
    src.start(t);
  }

  paddle(vy) { this.tone({ freq: 140 + Math.min(200, Math.abs(vy) * 0.4), type: 'triangle', dur: 0.1, vol: 0.4, slide: 60 }); }
  smash() { this.tone({ freq: 90, type: 'square', dur: 0.18, vol: 0.5, slide: 300 }); this.noise({ dur: 0.2, vol: 0.3, hp: 300 }); }
  wall() { this.tone({ freq: 620, type: 'sine', dur: 0.05, vol: 0.15 }); }

  // pentatonic combo ladder: C D E G A across octaves
  static PENTA = [261.6, 293.7, 329.6, 392, 440, 523.3, 587.3, 659.3, 784, 880, 1046.5, 1174.7];
  brick(combo, chaos) {
    const f = Audio.PENTA[Math.min(Audio.PENTA.length - 1, combo)];
    this.tone({ freq: f, type: 'triangle', dur: 0.12, vol: 0.28 });
    this.tone({ freq: f * 2, type: 'sine', dur: 0.08, vol: 0.12, delay: 0.01 });
    if (chaos > 1) this.tone({ freq: f * 1.5, type: 'sawtooth', dur: 0.06, vol: 0.08 });
  }
  clang() { this.tone({ freq: 220, type: 'square', dur: 0.09, vol: 0.22, slide: -80 }); this.noise({ dur: 0.08, vol: 0.18, hp: 2000 }); }
  tink() { this.tone({ freq: 1300, type: 'sine', dur: 0.07, vol: 0.15, slide: -300 }); }
  volt() { this.tone({ freq: 1800, type: 'sawtooth', dur: 0.15, vol: 0.2, slide: -1500 }); this.noise({ dur: 0.12, vol: 0.15, hp: 1200 }); }
  gel() { this.tone({ freq: 300, type: 'sine', dur: 0.14, vol: 0.3, slide: 260 }); }

  power() { [523, 659, 784, 1046].forEach((f, i) => this.tone({ freq: f, type: 'square', dur: 0.1, vol: 0.2, delay: i * 0.06 })); }
  lifeLost() { [400, 300, 220, 140].forEach((f, i) => this.tone({ freq: f, type: 'sawtooth', dur: 0.25, vol: 0.3, delay: i * 0.12 })); }
  fanfare() { [523, 659, 784, 1046, 1318].forEach((f, i) => this.tone({ freq: f, type: 'triangle', dur: 0.3, vol: 0.3, delay: i * 0.1 })); }
  levelStart() { this.tone({ freq: 330, type: 'triangle', dur: 0.2, vol: 0.25, slide: 220 }); }
  launch() { this.tone({ freq: 220, type: 'square', dur: 0.12, vol: 0.3, slide: 400 }); }

  // --- chaos beat: layers added as intensity rises ---
  startBeat() {
    if (this.beatOn || !this.ctx) return;
    this.beatOn = true;
    this.beatStep = 0;
    const bpm = 132, step = 60 / bpm / 2;
    const tick = () => {
      if (!this.beatOn) return;
      const s = this.beatStep++ % 8;
      const I = this.intensity;
      if (s % 2 === 0) this.tone({ freq: 55, type: 'sine', dur: 0.14, vol: 0.3 + 0.25 * I, slide: -20 }); // kick
      if (I > 0.25 && s === 4) this.noise({ dur: 0.1, vol: 0.12 + 0.1 * I, hp: 4000 }); // snare
      if (I > 0.5) { // hats
        this.noise({ dur: 0.04, vol: 0.05 + 0.08 * I, hp: 7000, delay: step * (s % 2 ? 0 : 0.5) });
      }
      if (I > 0.65 && (s === 0 || s === 3 || s === 6)) { // bass arp
        const notes = [110, 130.8, 146.8, 110, 98, 110, 130.8, 146.8];
        this.tone({ freq: notes[s], type: 'sawtooth', dur: step * 0.9, vol: 0.1 + 0.1 * I });
      }
      if (I > 0.85 && s % 4 === 2) {
        this.tone({ freq: 660, type: 'square', dur: 0.08, vol: 0.07 });
      }
      this.beatTimer = setTimeout(tick, step * 1000);
    };
    tick();
  }

  stopBeat() {
    this.beatOn = false;
    if (this.beatTimer) clearTimeout(this.beatTimer);
  }

  setIntensity(v) { this.intensity = Math.max(0, Math.min(1, v)); }
}
