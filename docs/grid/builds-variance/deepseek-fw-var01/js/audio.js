/* ============================================================
   TOPSIDE — audio.js
   All sound is synthesized live with Web Audio. No assets.
   Procedural music escalates with combo / charge / danger.
   ============================================================ */
'use strict';

const AUD = (() => {
  let ctx = null;
  let master = null;
  let musicGain = null;
  let sfxGain = null;
  let noiseBuf = null;
  let _muted = false;

  /* ---- midi helpers ---- */
  const midi = m => 440 * Math.pow(2, (m - 69) / 12);
  const PENTA_MINOR = [0, 3, 5, 7, 10];           // semitone offsets
  const SCALE_ROOT = 45;                           // A2

  function ensure() {
    if (ctx) return true;
    const AC = window.AudioContext || window.webkitAudioContext;
    if (!AC) return false;
    ctx = new AC({ latencyHint: 'interactive' });
    master = ctx.createGain();
    master.gain.value = _muted ? 0 : 0.9;
    master.connect(ctx.destination);
    musicGain = ctx.createGain();
    musicGain.gain.value = 0.5;
    musicGain.connect(master);
    sfxGain = ctx.createGain();
    sfxGain.gain.value = 0.9;
    sfxGain.connect(master);
    noiseBuf = (() => {
      const len = ctx.sampleRate * 1.2;
      const buf = ctx.createBuffer(1, len, ctx.sampleRate);
      const d = buf.getChannelData(0);
      for (let i = 0; i < len; i++) d[i] = Math.random() * 2 - 1;
      return buf;
    })();
    return true;
  }

  function resume() {
    if (!ctx) return;
    if (ctx.state === 'suspended') ctx.resume();
  }

  function setMuted(m) {
    _muted = m;
    if (master) master.gain.value = m ? 0 : 0.9;
  }
  const isMuted = () => _muted;

  /* ================= oscillators / helpers ================= */
  function osc(type, f0, f1, dur, vol, when = 0, dest = null) {
    if (!ctx) return;
    const t = ctx.currentTime + when;
    const o = ctx.createOscillator();
    const g = ctx.createGain();
    o.type = type;
    o.frequency.setValueAtTime(Math.max(20, f0), t);
    if (f1 !== undefined && f1 !== null) o.frequency.exponentialRampToValueAtTime(Math.max(20, f1), t + dur);
    g.gain.setValueAtTime(0.0001, t);
    g.gain.exponentialRampToValueAtTime(Math.max(0.0002, vol), t + 0.006);
    g.gain.exponentialRampToValueAtTime(0.0001, t + dur);
    o.connect(g); g.connect(dest || sfxGain);
    o.start(t); o.stop(t + dur + 0.02);
  }
  function noise(dur, filterFrom, filterTo, vol, when = 0, q = 1, dest = null) {
    if (!ctx) return;
    const t = ctx.currentTime + when;
    const src = ctx.createBufferSource();
    src.buffer = noiseBuf;
    src.loop = true;
    const f = ctx.createBiquadFilter();
    f.type = 'bandpass';
    f.Q.value = q;
    f.frequency.setValueAtTime(filterFrom, t);
    if (filterTo) f.frequency.exponentialRampToValueAtTime(Math.max(40, filterTo), t + dur);
    const g = ctx.createGain();
    g.gain.setValueAtTime(0.0001, t);
    g.gain.exponentialRampToValueAtTime(vol, t + 0.008);
    g.gain.exponentialRampToValueAtTime(0.0001, t + dur);
    src.connect(f); f.connect(g); g.connect(dest || sfxGain);
    src.start(t); src.stop(t + dur + 0.02);
  }
  function tone(f, dur, vol = 0.25, type = 'sine') {
    osc(type, f, f, dur, vol);
  }

  /* ================= SFX ================= */
  const sfx = {
    launch() { osc('triangle', 300, 780, 0.14, 0.35); noise(0.08, 1200, 2000, 0.12); },
    paddle() { osc('triangle', 620, 480, 0.05, 0.30); },
    brick(hue) {
      const f = 320 + (hue % 7) * 55;
      osc('square', f, f * 0.4, 0.09, 0.16);
      osc('sine', f * 2, f * 0.8, 0.07, 0.10);
      noise(0.05, 3000, 900, 0.10);
    },
    solid() { osc('square', 110, 70, 0.09, 0.26); },
    wall() { osc('sine', 210, 160, 0.06, 0.16); },
    ceiling() { osc('triangle', 320, 520, 0.09, 0.22); },
    powerup() { [660, 880, 1100].forEach((f, i) => osc('sine', f, f, 0.1, 0.2, i * 0.06)); },
    bomb() { noise(0.4, 2000, 120, 0.5); osc('sine', 160, 40, 0.4, 0.4); },
    loseBall() { osc('sawtooth', 400, 60, 0.35, 0.35); noise(0.3, 1500, 200, 0.2); },
    extraLife() { [523, 659, 784, 1047].forEach((f, i) => osc('triangle', f, f, 0.14, 0.22, i * 0.07)); },
    charged() { [392, 523, 659, 784, 1047].forEach((f, i) => osc('sine', f, f, 0.1, 0.16, i * 0.04)); },
    fire() { osc('sawtooth', 900, 120, 0.3, 0.3); noise(0.25, 4000, 400, 0.3); },
    shatter() { noise(0.5, 2000, 300, 0.35, 0, 0.7); },
    combo(n) {
      const f = 500 + n * 60;
      osc('sine', f, f * 1.5, 0.12, 0.22);
    },
    freeze() { osc('sine', 1400, 900, 0.3, 0.2); osc('sine', 1800, 1200, 0.3, 0.12, 0.03); },
    levelup() { [523, 659, 784, 1047, 1319].forEach((f, i) => osc('triangle', f, f, 0.18, 0.22, i * 0.08)); },
    gameover() { [392, 330, 262, 196].forEach((f, i) => osc('sawtooth', f, f * 0.9, 0.4, 0.2, i * 0.22)); },
    win() { [523, 659, 784, 1047, 784, 1047, 1319].forEach((f, i) => osc('triangle', f, f, 0.16, 0.2, i * 0.1)); },
    countdown() { tone(440, 0.07, 0.2, 'square'); }
  };

  /* ================= procedural music ================= */
  const music = {
    playing: false,
    step: 0,
    nextT: 0,
    timer: null,
    intensity: 0,          // 0..1, drives layers & tempo
    seq: null,

    start() {
      if (!ensure()) return;
      resume();
      this.playing = true;
      this.step = 0;
      this.nextT = ctx.currentTime + 0.1;
      if (!this.timer) {
        this.timer = setInterval(() => {
          if (!this.playing) return;
          const ahead = 0.12;
          while (this.nextT < ctx.currentTime + ahead) {
            this.scheduleStep(this.step, this.nextT);
            this.step = (this.step + 1) % 16;
            this.nextT += this.stepDur();
          }
        }, 80);
      }
    },
    stop() {
      this.playing = false;
      if (this.timer) { clearInterval(this.timer); this.timer = null; }
    },
    setIntensity(v) { this.intensity = clamp(v, 0, 1); },

    bpm() { return 112 + this.intensity * 46; },
    stepDur() { return 60 / this.bpm() / 4; },

    /* pick a note def for a scale degree */
    note(deg, octave, accent = false) {
      const base = SCALE_ROOT + octave * 12 + PENTA_MINOR[deg % PENTA_MINOR.length] + Math.floor(deg / PENTA_MINOR.length) * 12;
      return midi(base + (accent ? 12 : 0));
    },

    scheduleStep(step, t) {
      if (!ctx) return;
      /* osc/noise take an offset from now; convert the absolute future time */
      const when = Math.max(0, t - ctx.currentTime);
      const I = this.intensity;
      const sd = this.stepDur();
      const bassOn = step % 4 === 0 || (I > 0.35 && step % 4 === 2);
      const arpOn = !!(step % 2) || (I > 0.55 && step % 2 === 0 && step % 4 !== 0);
      const padOn = step === 0;

      /* bass */
      if (bassOn && I > 0.05) {
        const root = 45 + (I > 0.7 ? 12 : 0);
        const f = midi(root + (step % 8 === 4 ? 7 : 0));
        osc('triangle', f, f, sd * 1.6, 0.16 * (0.5 + I * 0.6), when, musicGain);
        osc('square', f / 2, f / 2, sd * 1.6, 0.05, when, musicGain);
      }
      /* arp sparkle (intensity driven) */
      if (arpOn && I > 0.2) {
        const degs = [0, 2, 4, 3, 5, 3, 4, 2];
        const deg = degs[step % degs.length];
        const f = this.note(deg, 2 + (I > 0.85 ? 1 : 0), I > 0.6);
        osc('sine', f, f, sd * 0.9, 0.06 * I, when, musicGain);
      }
      /* pad */
      if (padOn && I > 0.4) {
        [0, 3, 7].forEach(d => {
          const f = this.note(d, 1);
          osc('sawtooth', f, f, sd * 8, 0.018, when, musicGain);
        });
      }
      /* hi-hat tick at high intensity */
      if (I > 0.75 && step % 2 === 0) {
        noise(0.02, 8000, 6000, 0.03, when, 1, musicGain);
      }
    }
  };

  return {
    ensure, resume, setMuted, isMuted,
    sfx, music,
    test() { ensure(); resume(); this.sfx.brick(3); },
    muted: _muted
  };
})();
