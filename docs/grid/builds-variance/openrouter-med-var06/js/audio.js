// Procedural WebAudio: SFX + arpeggio music layer that intensifies with rush.
// Everything is synthesized; created lazily after a user gesture.

export class AudioSys {
  constructor() {
    this.ctx = null;
    this.master = null;
    this.sfxGain = null;
    this.musicGain = null;
    this.muted = false;
    this.musicTimer = null;
    this.musicStep = 0;
    this.rush = 0;          // 0..1 intensity
    this._lastNoise = 0;
  }

  init() {
    if (this.ctx) return;
    try {
      const AC = window.AudioContext || window.webkitAudioContext;
      if (!AC) return;
      this.ctx = new AC();
      this.master = this.ctx.createGain();
      this.master.gain.value = this.muted ? 0 : 0.9;
      this.master.connect(this.ctx.destination);
      this.sfxGain = this.ctx.createGain();
      this.sfxGain.gain.value = 0.8;
      this.sfxGain.connect(this.master);
      this.musicGain = this.ctx.createGain();
      this.musicGain.gain.value = 0.22;
      this.musicGain.connect(this.master);
      this.startMusic();
    } catch (e) { this.ctx = null; }
  }

  resume() {
    if (this.ctx && this.ctx.state === 'suspended') this.ctx.resume().catch(()=>{});
  }

  setMuted(m) {
    this.muted = m;
    if (this.master) this.master.gain.value = m ? 0 : 0.9;
  }

  now() { return this.ctx ? this.ctx.currentTime : 0; }

  _env(node, t, a, d, peak) {
    const g = node.gain;
    g.setValueAtTime(0.0001, t);
    g.linearRampToValueAtTime(peak, t + a);
    g.exponentialRampToValueAtTime(0.0001, t + a + d);
  }

  tone({ freq = 440, type = 'sine', a = 0.005, d = 0.15, peak = 0.3, slide = 0, t = null, dest = null }) {
    if (!this.ctx || this.muted) return;
    t = t ?? this.now();
    const o = this.ctx.createOscillator();
    const g = this.ctx.createGain();
    o.type = type;
    o.frequency.setValueAtTime(freq, t);
    if (slide) o.frequency.exponentialRampToValueAtTime(Math.max(20, freq + slide), t + a + d);
    this._env(g, t, a, d, peak);
    o.connect(g); g.connect(dest || this.sfxGain);
    o.start(t); o.stop(t + a + d + 0.05);
  }

  noise({ d = 0.2, peak = 0.3, filterFreq = 1200, type = 'lowpass', slide = 0, t = null }) {
    if (!this.ctx || this.muted) return;
    t = t ?? this.now();
    const len = Math.ceil(this.ctx.sampleRate * (d + 0.05));
    const buf = this.ctx.createBuffer(1, len, this.ctx.sampleRate);
    const data = buf.getChannelData(0);
    for (let i = 0; i < len; i++) data[i] = Math.random() * 2 - 1;
    const src = this.ctx.createBufferSource();
    src.buffer = buf;
    const f = this.ctx.createBiquadFilter();
    f.type = type;
    f.frequency.setValueAtTime(filterFreq, t);
    if (slide) f.frequency.exponentialRampToValueAtTime(Math.max(40, filterFreq + slide), t + d);
    const g = this.ctx.createGain();
    this._env(g, t, 0.004, d, peak);
    src.connect(f); f.connect(g); g.connect(this.sfxGain);
    src.start(t); src.stop(t + d + 0.05);
  }

  // ------- named SFX -------
  paddleHit(vel = 0) {
    const f = 220 + vel * 90;
    this.tone({ freq: f, type: 'triangle', d: 0.09, peak: 0.35 });
    this.noise({ d: 0.06, peak: 0.1, filterFreq: 900 });
  }
  smash() {
    this.tone({ freq: 180, type: 'sawtooth', d: 0.18, peak: 0.3, slide: 320 });
    this.noise({ d: 0.25, peak: 0.22, filterFreq: 600, slide: 2400, type: 'bandpass' });
  }
  wall() { this.tone({ freq: 160, type: 'sine', d: 0.06, peak: 0.22 }); }
  brick(type = 'std', intensity = 0) {
    const base = 330;
    switch (type) {
      case 'phase': this.tone({ freq: 520, type: 'square', d: 0.12, peak: 0.25, slide: -260 }); this.noise({ d: 0.1, peak: 0.12, filterFreq: 3000, type: 'highpass' }); break;
      case 'shield': this.tone({ freq: 700, type: 'square', d: 0.05, peak: 0.12 }); break; // clank = blocked
      case 'shieldBreak': this.tone({ freq: 420, type: 'triangle', d: 0.2, peak: 0.35, slide: 300 }); this.noise({ d: 0.18, peak: 0.2, filterFreq: 1600 }); break;
      case 'deflect': this.tone({ freq: 900, type: 'sine', d: 0.12, peak: 0.25, slide: 500 }); break;
      case 'bomb': this.noise({ d: 0.5, peak: 0.5, filterFreq: 300, slide: -220 }); this.tone({ freq: 90, type: 'sawtooth', d: 0.4, peak: 0.4, slide: -50 }); break;
      case 'mover': this.tone({ freq: base * 1.5, type: 'triangle', d: 0.12, peak: 0.3, slide: 120 }); break;
      case 'armor': this.noise({ d: 0.08, peak: 0.25, filterFreq: 5000, type: 'highpass' }); this.tone({ freq: 140, type: 'square', d: 0.08, peak: 0.2 }); break; // blocked
      case 'armorBreak': this.tone({ freq: 300, type: 'sawtooth', d: 0.3, peak: 0.4, slide: -160 }); this.noise({ d: 0.3, peak: 0.3, filterFreq: 2000 }); break;
      case 'prism': this.tone({ freq: 1200, type: 'sine', d: 0.2, peak: 0.25, slide: -700 }); break;
      default: {
        const f = base * Math.pow(1.06, Math.min(intensity, 24));
        this.tone({ freq: f, type: 'triangle', d: 0.1, peak: 0.3, slide: f * 0.4 });
      }
    }
  }
  powerup(kind = 'generic') {
    const map = { multi: 520, fire: 300, pierce: 640, sticky: 440, wide: 380, slow: 260, life: 700, speed: 800, magnet: 360, ghost: 560 };
    const f = map[kind] || 500;
    this.tone({ freq: f, type: 'sine', d: 0.12, peak: 0.25, t: this.now() });
    this.tone({ freq: f * 1.5, type: 'sine', d: 0.16, peak: 0.22, t: this.now() + 0.09 });
    this.tone({ freq: f * 2, type: 'sine', d: 0.2, peak: 0.2, t: this.now() + 0.18 });
  }
  loseBall() {
    this.tone({ freq: 400, type: 'sawtooth', d: 0.5, peak: 0.3, slide: -340 });
    this.noise({ d: 0.4, peak: 0.12, filterFreq: 500 });
  }
  levelClear() {
    const t = this.now();
    [523, 659, 784, 1047].forEach((f, i) =>
      this.tone({ freq: f, type: 'triangle', d: 0.3, peak: 0.25, t: t + i * 0.12 }));
  }
  rushStart() {
    this.tone({ freq: 200, type: 'sawtooth', d: 0.6, peak: 0.3, slide: 900 });
    this.noise({ d: 0.6, peak: 0.2, filterFreq: 400, slide: 4000, type: 'bandpass' });
  }
  rushTick(level) {
    this.tone({ freq: 400 + level * 55, type: 'square', d: 0.05, peak: 0.08 });
  }

  // ------- music: arpeggio layer -------
  startMusic() {
    if (!this.ctx || this.musicTimer) return;
    const scale = [0, 3, 5, 7, 10, 12, 15]; // minor pentatonic-ish
    const roots = [110, 110, 146.8, 130.8];
    let bar = 0;
    const stepMs = 130;
    this.musicTimer = setInterval(() => {
      if (!this.ctx || this.ctx.state !== 'running' || this.muted) return;
      const r = this.rush;
      const stepDur = stepMs / (1 + r * 0.8); // faster when frenzied
      // occasionally re-time; simpler: keep interval, vary notes/octaves
      const root = roots[Math.floor(this.musicStep / 8) % roots.length];
      const s = scale[this.musicStep % scale.length];
      const oct = r > 0.4 && (this.musicStep % 4 === 3) ? 4 : 2;
      const f = root * oct * Math.pow(2, s / 12);
      const peak = 0.12 + r * 0.15;
      this.tone({ freq: f, type: r > 0.6 ? 'sawtooth' : 'triangle', d: 0.14, peak, dest: this.musicGain });
      // bass pulse
      if (this.musicStep % 4 === 0) this.tone({ freq: root / 2, type: 'sine', d: 0.22, peak: 0.2 + r * 0.15, dest: this.musicGain });
      // hats when frenzied
      if (r > 0.25 && this.musicStep % 2 === 1) this.noise({ d: 0.03, peak: 0.04 + r * 0.05, filterFreq: 8000, type: 'highpass' });
      this.musicStep++;
    }, stepMs);
  }
}

export const audio = new AudioSys();