/**
 * audio.js — procedural audio engine for a Breakout-style game with heavy
 * "game juice". Pure ES module.
 *
 * IMPORTANT: this file touches NO browser APIs at import time. Every browser
 * API (AudioContext, AudioParam, oscillators, buffers, setInterval for the
 * music scheduler) is created/accessed lazily inside methods, and every such
 * access is wrapped in try/catch so it degrades to a safe no-op in Node,
 * headless environments, or if the AudioContext is unavailable.
 */

// ---------------------------------------------------------------------------
// A-minor pentatonic helper tables (all offsets given in semitones from A2=110Hz)
// ---------------------------------------------------------------------------

const SEMITONE = 2 ** (1 / 12);
const NOTE_A2 = 110; // A2 = 110 Hz — reference root for the pentatonic ladder

/** A minor pentatonic scale degrees (in semitones): A C D E G (and octave). */
const MINOR_PENT_SEMIS = [0, 3, 5, 7, 10];

/** A minor pentatonic ladder up to 2 octaves above the roof note (26 semis max). */
const PENT_LADDER = [];
for (let oct = 0; oct <= 2; oct++) {
  for (const semi of MINOR_PENT_SEMIS) {
    const s = semi + oct * 12;
    if (s <= 26) PENT_LADDER.push(s);
  }
}

/**
 * The chugging four-bar chord progression in A minor. Chord = array of
 * semitone offsets from A2 (110 Hz). Bass = its root (first index).
 */
const PROGRESSION = [
  { name: 'Am', chord: [0, 3, 7, 12] }, // i
  { name: 'F',  chord: [8, 12, 15, 19] }, // VI
  { name: 'C',  chord: [3, 7, 10, 15] }, // III
  { name: 'G',  chord: [10, 14, 17, 22] }, // VII
];

const CHORD_WINDOW = 4; // bars per chord
const NOISE_SECONDS = 2; // shared white-noise buffer length

const safe = (fn) => { try { return fn(); } catch { return undefined; } };

/**
 * The music engine: a self-contained procedural sequencer driven by a
 * lookahead scheduler (setInterval ~40ms, scheduling ~0.15s ahead).
 * It runs only while started; all of its state lives inside the closure and
 * every entry point is idempotent and exception-safe.
 * Layers (by intensity) — see setIntensity().
 */
function createMusicEngine(host) {
  const eng = {
    running: false,
    timerId: null,
    intensity: 0,
    nextNoteTime: 0,
    step: 0, // 16th-note step index within a 4-bar = 64-step loop
    bassGain: null,
    arpGain: null,
    padGain: null,
  };

  const setIntensity = (n) => {
    eng.intensity = Math.max(0, Math.min(3, Math.round(n) || 0));
  };

  const beatLength = () =>
    60 / (104 + 16 * eng.intensity); // seconds per beat, 104..152 BPM

  // Per-chord state for bass/arp, stable for the whole chord window so the
  // 64-step loop does not need re-computation every step.
  const chordState = () => {
    const bar = Math.floor(eng.step / 16) % 4;
    return PROGRESSION[bar];
  };

  const start = () => {
    if (eng.running) return;
    const ctx = host.ctx();
    if (!ctx) return;
    if (ctx.state === 'closed') return;
    eng.running = true;
    eng.nextNoteTime = ctx.currentTime + 0.08;
    eng.step = 0;
    // Pre-create per-layer submix gains so layers can be toggled seamlessly.
    eng.bassGain = ctx.createGain();
    eng.bassGain.gain.value = 0;
    eng.bassGain.connect(host.musicGain());
    eng.arpGain = ctx.createGain();
    eng.arpGain.gain.value = 0;
    eng.arpGain.connect(host.musicGain());
    eng.padGain = ctx.createGain();
    eng.padGain.gain.value = 0;
    eng.padGain.connect(host.musicGain());
    eng.timerId = setInterval(() => { try { scheduler(); } catch {} }, 40);
  };

  const stop = () => {
    if (!eng.running) return;
    eng.running = false;
    clearInterval(eng.timerId);
    eng.timerId = null;
    safe(() => {
      const ctx = host.ctx();
      const now = ctx.currentTime;
      for (const g of [eng.bassGain, eng.arpGain, eng.padGain]) {
        if (g) g.gain.cancelScheduledValues(now);
        if (g) g.gain.setTargetAtTime(0, now, 0.06);
      }
    });
  };

  const scheduler = () => {
    const ctx = host.ctx();
    if (!ctx) return;
    const ahead = ctx.currentTime + 0.16;
    let guard = 0;
    while (eng.nextNoteTime < ahead) {
      if (++guard > 64) break;
      kickAt(eng.nextNoteTime);
      hihatAt(eng.nextNoteTime);
      if (eng.intensity >= 1) bassAt(eng.step, eng.nextNoteTime);
      if (eng.intensity >= 2) arpAt(eng.step, eng.nextNoteTime);
      if (eng.intensity >= 3) padAt(eng.step, eng.nextNoteTime);
      advanceStep();
    }
  };

  const advanceStep = () => {
    const step16 = beatLength() / 4; // 16th-note length
    eng.step = (eng.step + 1) % 64;
    eng.nextNoteTime += step16;
  };

  // Layer 0 — kick drum on every beat (step % 4 === 0).
  const kickAt = (t) => {
    if (eng.step % 4 !== 0) return;
    const ctx = host.ctx();
    const osc = ctx.createOscillator();
    const g = ctx.createGain();
    osc.frequency.setValueAtTime(130, t);
    osc.frequency.exponentialRampToValueAtTime(44, t + 0.1);
    g.gain.setValueAtTime(0.5, t);
    g.gain.exponentialRampToValueAtTime(0.001, t + 0.16);
    osc.connect(g).connect(host.musicGain());
    osc.start(t);
    osc.stop(t + 0.2);
  };

  // Layer 0 — closed hi-hat on offbeats (step % 4 === 2), short highpass noise.
  const hihatAt = (t) => {
    if (eng.step % 4 !== 2) return;
    const ctx = host.ctx();
    const src = ctx.createBufferSource();
    src.buffer = host.noiseBuffer();
    const hp = ctx.createBiquadFilter();
    hp.type = 'highpass';
    hp.frequency.value = 7000;
    const g = ctx.createGain();
    g.gain.setValueAtTime(0.12, t);
    g.gain.exponentialRampToValueAtTime(0.001, t + 0.05);
    src.connect(hp).connect(g).connect(host.musicGain());
    src.start(t);
    src.stop(t + 0.06);
  };

  // Layer 1 — bass: short root notes (square + sine through a lowpass).
  const bassAt = (step, t) => {
    if (step % 8 !== 0) return;
    const ctx = host.ctx();
    const root = NOTE_A2 * SEMITONE ** chordState().chord[0];
    const lp = ctx.createBiquadFilter();
    lp.type = 'lowpass';
    lp.frequency.setValueAtTime(320, t);
    lp.frequency.exponentialRampToValueAtTime(200, t + 0.15);
    lp.Q.value = 4;
    lp.connect(eng.bassGain);
    for (const type of ['square', 'sine']) {
      const osc = ctx.createOscillator();
      osc.type = type;
      osc.frequency.value = root * (type === 'square' ? 1 : 0.5);
      const g = ctx.createGain();
      g.gain.setValueAtTime(type === 'square' ? 0.07 : 0.3, t);
      g.gain.exponentialRampToValueAtTime(0.001, t + 0.18);
      osc.connect(g).connect(lp);
      osc.start(t);
      osc.stop(t + 0.2);
    }
  };

  // Layer 2 — 16th-note pentatonic arpeggio: cycling up the ladder, with a
  // little density modifier so notes only hit on a few steps per bar.
  const arpAt = (step, t) => {
    const ctx = host.ctx();
    const { chord } = chordState();
    const inChord = chord.map((s) => MINOR_PENT_SEMIS.includes(s % 12)).filter(Boolean).length;
    const density = 1 + inChord / 10 + eng.intensity / 20; // ~1.3..1.5
    if ((step * density) % 1 > 0.28) return;
    const arp = [chord[0], chord[1], chord[2], chord[3], chord[0] + 12];
    const idx = step % arp.length;
    const freq = NOTE_A2 * SEMITONE ** arp[idx];
    const osc = ctx.createOscillator();
    osc.type = 'triangle';
    osc.frequency.value = freq * 2; // two octaves up
    const g = ctx.createGain();
    const dur = 0.09;
    g.gain.setValueAtTime(0.001, t);
    g.gain.exponentialRampToValueAtTime(0.06, t + 0.006);
    g.gain.exponentialRampToValueAtTime(0.001, t + dur);
    osc.connect(g).connect(eng.arpGain);
    osc.start(t);
    osc.stop(t + dur + 0.02);
  };

  // Layer 3 — sustained pad: detuned saw pair through a quiet lowpass.
  const padAt = (step, t) => {
    if (step % 16 !== 0) return;
    const ctx = host.ctx();
    const { chord } = chordState();
    const dur = CHORD_WINDOW * beatLength() - 0.05;
    const lp = ctx.createBiquadFilter();
    lp.type = 'lowpass';
    lp.frequency.value = 900;
    lp.Q.value = 0.5;
    lp.connect(eng.padGain);
    for (const semi of chord) {
      const base = NOTE_A2 * SEMITONE ** (semi + 12);
      for (const detune of [-7, 7]) {
        const osc = ctx.createOscillator();
        osc.type = 'sawtooth';
        osc.frequency.value = base;
        osc.detune.value = detune;
        const g = ctx.createGain();
        g.gain.setValueAtTime(0.001, t);
        g.gain.linearRampToValueAtTime(0.018, t + 0.5);
        g.gain.setValueAtTime(0.018, t + dur - 0.2);
        g.gain.linearRampToValueAtTime(0.001, t + dur);
        osc.connect(g).connect(lp);
        osc.start(t);
        osc.stop(t + dur + 0.05);
      }
    }
  };

  return { start, stop, setIntensity, scheduler };
}

// ---------------------------------------------------------------------------
// The public engine
// ---------------------------------------------------------------------------

export class AudioEngine {
  /**
   * Constructor — strictly no browser API access. All Web Audio objects are
   * created lazily inside init()/resume().
   */
  constructor() {
    this._ctx = null;
    this._masterGain = null;
    this._musicGain = null;
    this._fxGain = null;
    this._compressor = null;
    this._noiseBuffer = null;
    this._muted = false;
    this._prevVolume = 1;
    this._music = createMusicEngine({
      ctx: () => this._ctx,
      musicGain: () => this._musicGain,
      noiseBuffer: () => this._noiseBuffer,
    });
  }

  /** Whether audio output is muted (master gain at 0). */
  get muted() {
    return !!this._muted;
  }

  /** Master gain 0 when muted, previous value otherwise. Safe no-op pre-init. */
  setMuted(m) {
    this._muted = !!m;
    const now = safe(() => this._ctx && this._ctx.currentTime);
    safe(() => {
      if (!this._masterGain) return;
      const g = this._masterGain.gain;
      if (now !== undefined && now !== null && typeof now === 'number') {
        g.cancelScheduledValues(now);
        if (this._muted) {
          g.setTargetAtTime(0, now, 0.02);
        } else {
          g.setTargetAtTime(this._prevVolume || 1, now, 0.02);
        }
      } else {
        g.value = this._muted ? 0 : this._prevVolume || 1;
      }
    });
    if (!this._muted) {
      // remember the restored level
      const v = safe(() => this._masterGain && this._masterGain.gain.value);
      if (typeof v === 'number' && v > 0) this._prevVolume = v;
    }
    return this._muted;
  }

  /** Toggle mute; returns the new muted state. */
  toggleMute() {
    return this.setMuted(!this._muted);
  }

  /**
   * Create (or resume) the AudioContext and build the whole signal graph.
   * Called from a user gesture (start button/pointer/key). Safe to call any
   * number of times; it never tears down an already-working graph.
   */
  init() {
    try {
      if (!this._ctx) {
        const AC = window.AudioContext || window.webkitAudioContext;
        if (!AC) return false;
        this._ctx = new AC();
        // master -> compressor -> destination
        this._compressor = this._ctx.createDynamicsCompressor();
        this._compressor.threshold.value = -12;
        this._compressor.knee.value = 22;
        this._compressor.ratio.value = 8;
        this._compressor.attack.value = 0.004;
        this._compressor.release.value = 0.24;
        this._compressor.connect(this._ctx.destination);

        this._masterGain = this._ctx.createGain();
        this._masterGain.gain.value = this._muted ? 0 : this._prevVolume;
        this._masterGain.connect(this._compressor);

        // fxGain + musicGain both feed masterGain
        this._fxGain = this._ctx.createGain();
        this._fxGain.gain.value = 1;
        this._fxGain.connect(this._masterGain);
        this._musicGain = this._ctx.createGain();
        this._musicGain.gain.value = 0.85;
        this._musicGain.connect(this._masterGain);

        // Shared 2s white-noise buffer for hats, whooshes, crunches.
        const len = Math.floor(this._ctx.sampleRate * NOISE_SECONDS);
        const buf = this._ctx.createBuffer(1, len, this._ctx.sampleRate);
        const data = buf.getChannelData(0);
        for (let i = 0; i < len; i++) data[i] = Math.random() * 2 - 1;
        this._noiseBuffer = buf;
      }
      if (this._ctx.state === 'suspended') {
        if (typeof this._ctx.resume === 'function') this._ctx.resume();
      }
      return this._ctx.state === 'running' || this._ctx.state === 'suspended';
    } catch {
      return false;
    }
  }

  /** Resume a suspended context (call on pointer/key events). Never throws. */
  resume() {
    if (!this._ctx) return false;
    try {
      if (this._ctx.state === 'suspended' && typeof this._ctx.resume === 'function') {
        this._ctx.resume();
      }
      return true;
    } catch {
      return false;
    }
  }

  /**
   * Music layer: idempotently start the procedural music sequencer.
   * Uses the currently configured intensity/tempo; the engine begins
   * scheduling with its first iteration.
   */
  musicStart() {
    try {
      this._music.start();
    } catch {}
  }

  /** Stop the music sequencer; layers fade out quickly. Idempotent. */
  musicStop() {
    try {
      this._music.stop();
    } catch {}
  }

  /**
   * Change music intensity live: 0 (drums only) .. 3 (full). Also drives the
   * tempo: BPM = 104 + 16*intensity. Layer mapping:
   *   >=0 kick every beat + hi-hat on offbeats
   *   >=1 bass line (root notes, square/sine through lowpass)
   *   >=2 16th-note A-minor-pentatonic arpeggio
   *   >=3 sustained detuned-saw pad chords through lowpass
   * Safe no-op before init (engine simply won't schedule).
   */
  setMusicIntensity(n) {
    try {
      this._music.setIntensity(n);
    } catch {}
  }

  // --- internal helpers ------------------------------------------------------

  /** Current time, or 0 if context missing. Never throws. */
  _now() {
    try {
      return this._ctx ? this._ctx.currentTime : 0;
    } catch {
      return 0;
    }
  }

  /** Ready-to-schedule one-shot sound. */
  _ready() {
    try {
      return !!(this._ctx && this._masterGain && this._fxGain);
    } catch {
      return false;
    }
  }

  // --- one-shot sound effects (all safe no-ops if not initialized) -----------

  /**
   * launch(): soft rising whoosh as the ball leaves the paddle — filtered
   * noise whose cutoff climbs steeply, layered with a quiet detuned-sine rise.
   */
  launch() {
    if (!this._ready()) return;
    try {
      const t = this._now();
      const src = this._ctx.createBufferSource();
      src.buffer = this._noiseBuffer;
      src.loop = true;
      const bp = this._ctx.createBiquadFilter();
      bp.type = 'bandpass';
      bp.Q.value = 0.8;
      bp.frequency.setValueAtTime(300, t);
      bp.frequency.exponentialRampToValueAtTime(2400, t + 0.16);
      const g = this._ctx.createGain();
      g.gain.setValueAtTime(0.001, t);
      g.gain.exponentialRampToValueAtTime(0.22, t + 0.05);
      g.gain.exponentialRampToValueAtTime(0.001, t + 0.3);
      src.connect(bp).connect(g).connect(this._fxGain);
      src.start(t);
      src.stop(t + 0.32);

      const o = this._ctx.createOscillator();
      o.type = 'sine';
      o.frequency.setValueAtTime(220, t);
      o.frequency.exponentialRampToValueAtTime(880, t + 0.14);
      const og = this._ctx.createGain();
      og.gain.setValueAtTime(0.001, t);
      og.gain.exponentialRampToValueAtTime(0.06, t + 0.05);
      og.gain.exponentialRampToValueAtTime(0.001, t + 0.22);
      o.connect(og).connect(this._fxGain);
      o.start(t);
      o.stop(t + 0.24);
    } catch {}
  }

  /**
   * paddleHit(speed01): thump on ball<->paddle impact. The impact brightness
   * (volume + filter cutoff) scales with speed01, so fast rebounds hit harder.
   */
  paddleHit(speed01) {
    if (!this._ready()) return;
    try {
      const t = this._now();
      const speed = Math.max(0, Math.min(1, Number(speed01) || 0));
      const vol = 0.12 + 0.5 * speed;
      const osc = this._ctx.createOscillator();
      osc.type = 'sine';
      osc.frequency.setValueAtTime(200, t);
      osc.frequency.exponentialRampToValueAtTime(55, t + 0.09);
      const g = this._ctx.createGain();
      g.gain.setValueAtTime(vol, t);
      g.gain.exponentialRampToValueAtTime(0.001, t + 0.12);
      osc.connect(g).connect(this._fxGain);
      osc.start(t);
      osc.stop(t + 0.14);
      // click transient, brighter with speed
      const src = this._ctx.createBufferSource();
      src.buffer = this._noiseBuffer;
      const hp = this._ctx.createBiquadFilter();
      hp.type = 'highpass';
      hp.frequency.value = 1000 + 5000 * speed;
      const ng = this._ctx.createGain();
      ng.gain.setValueAtTime(0.04 + 0.14 * speed, t);
      ng.gain.exponentialRampToValueAtTime(0.001, t + 0.04);
      src.connect(hp).connect(ng).connect(this._fxGain);
      src.start(t);
      src.stop(t + 0.05);
    } catch {}
  }

  /**
   * bounce(speed01, combo): blip for wall/brick bounces. Pitch walks up a
   * pentatonic ladder with combo (which resets each life) and is nudged
   * upward by speed, so rarer high-combo bounces sound brighter.
   */
  bounce(speed01, combo) {
    if (!this._ready()) return;
    try {
      const t = this._now();
      const speed = Math.max(0, Math.min(1, Number(speed01) || 0));
      const c = Math.max(0, Math.floor(Number(combo) || 0));
      const ladder = PENT_LADDER[(c + 3) * 5 + Math.floor(speed * 5)] || PENT_LADDER[PENT_LADDER.length - 1];
      const freq = NOTE_A2 * SEMITONE ** ladder;
      const vol = 0.05 + 0.2 * speed;
      const o = this._ctx.createOscillator();
      o.type = 'triangle';
      o.frequency.setValueAtTime(freq, t);
      o.frequency.exponentialRampToValueAtTime(freq * 1.25, t + 0.05);
      const g = this._ctx.createGain();
      g.gain.setValueAtTime(0.001, t);
      g.gain.exponentialRampToValueAtTime(vol, t + 0.004);
      g.gain.exponentialRampToValueAtTime(0.001, t + 0.07);
      o.connect(g).connect(this._fxGain);
      o.start(t);
      o.stop(t + 0.08);
    } catch {}
  }

  /**
   * brickBreak(combo): layered crunch — noise burst through a lowpass (kinda
   * like breaking a tile) plus a low sine thump. When the combo is high
   * (>= combo/8 full steps), a shimmering arpeggio chord is layered on top so
   * long runs audibly escalate.
   */
  brickBreak(combo) {
    if (!this._ready()) return;
    try {
      const t = this._now();
      const c = Math.max(0, Math.floor(Number(combo) || 0));

      // noise crunch
      const src = this._ctx.createBufferSource();
      src.buffer = this._noiseBuffer;
      const lp = this._ctx.createBiquadFilter();
      lp.type = 'lowpass';
      lp.frequency.setValueAtTime(4200, t);
      lp.frequency.exponentialRampToValueAtTime(400, t + 0.09);
      const ng = this._ctx.createGain();
      ng.gain.setValueAtTime(0.001, t);
      ng.gain.exponentialRampToValueAtTime(0.22, t + 0.01);
      ng.gain.exponentialRampToValueAtTime(0.001, t + 0.1);
      src.connect(lp).connect(ng).connect(this._fxGain);
      src.start(t);
      src.stop(t + 0.12);

      // low thump
      const o = this._ctx.createOscillator();
      o.type = 'sine';
      o.frequency.setValueAtTime(170, t);
      o.frequency.exponentialRampToValueAtTime(60, t + 0.08);
      const g = this._ctx.createGain();
      g.gain.setValueAtTime(0.3, t);
      g.gain.exponentialRampToValueAtTime(0.001, t + 0.12);
      o.connect(g).connect(this._fxGain);
      o.start(t);
      o.stop(t + 0.14);

      // high-combo shimmer: pentatonic chordal sparkle on top
      if (Math.floor(c / 8) >= 1 && Math.floor(c / 8) <= 4) {
        const sh = Math.floor(c / 8) - 1;
        for (let i = 0; i < 4; i++) {
          const semi = MINOR_PENT_SEMIS[i] + 24 + sh * 12; // 2+ octaves up, rising per step
          const so = this._ctx.createOscillator();
          so.type = 'sine';
          so.frequency.value = NOTE_A2 * SEMITONE ** semi;
          const sg = this._ctx.createGain();
          const dur = 0.16 + i * 0.05;
          sg.gain.setValueAtTime(0.001, t + 0.02 + i * 0.01);
          sg.gain.exponentialRampToValueAtTime(0.05, t + 0.03 + i * 0.01);
          sg.gain.exponentialRampToValueAtTime(0.001, t + 0.02 + dur);
          so.connect(sg).connect(this._fxGain);
          so.start(t + 0.02 + i * 0.01);
          so.stop(t + 0.05 + dur);
        }
      }
    } catch {}
  }

  /** gateClang(): bright metallic clang when a gate brick rejects a ball. */
  gateClang() {
    if (!this._ready()) return;
    try {
      const t = this._now();
      const partials = [0.5, 1, 1.5, 2, 2.7, 3.9, 5.4];
      for (let i = 0; i < partials.length; i++) {
        const o = this._ctx.createOscillator();
        o.type = 'square';
        const f = 520 * partials[i];
        o.frequency.setValueAtTime(f, t);
        o.frequency.exponentialRampToValueAtTime(f * 0.92, t + 0.5);
        const g = this._ctx.createGain();
        const dur = 0.16 + i * 0.04;
        g.gain.setValueAtTime(0.001, t);
        g.gain.exponentialRampToValueAtTime(0.05 / (1 + i * 0.25), t + 0.003);
        g.gain.exponentialRampToValueAtTime(0.001, t + dur);
        o.connect(g).connect(this._fxGain);
        o.start(t);
        o.stop(t + dur + 0.02);
      }
    } catch {}
  }

  /**
   * rooftop(n): sparkly ascension for the "ball escaped above the bricks"
   * streak. Each successive rooftop hit climbs one pentatonic step; capped at
   * 2 octaves above the ladder base so it never gets shrill.
   */
  rooftop(n) {
    if (!this._ready()) return;
    try {
      const t = this._now();
      const s = Math.max(0, Math.min(PENT_LADDER.length - 1, Math.floor(Number(n) || 0)));
      const semi = PENT_LADDER[s] + 12; // start an octave up from A2
      const f = NOTE_A2 * SEMITONE ** semi;
      for (let i = 0; i < 3; i++) {
        const o = this._ctx.createOscillator();
        o.type = 'sine';
        o.frequency.setValueAtTime(f * (i === 1 ? 2 : 3), t + i * 0.05);
        o.frequency.exponentialRampToValueAtTime(f * (i === 1 ? 3 : 4), t + 0.12 + i * 0.05);
        const g = this._ctx.createGain();
        const dur = 0.2 + i * 0.08;
        g.gain.setValueAtTime(0.001, t + i * 0.05);
        g.gain.exponentialRampToValueAtTime(0.05, t + 0.06 + i * 0.05);
        g.gain.exponentialRampToValueAtTime(0.001, t + i * 0.05 + dur);
        o.connect(g).connect(this._fxGain);
        o.start(t + i * 0.05);
        o.stop(t + i * 0.05 + dur + 0.02);
      }
    } catch {}
  }

  /** sparkle(): tiny chime for collecting rooftop bonus orbs. */
  sparkle() {
    if (!this._ready()) return;
    try {
      const t = this._now();
      const notes = [0, 7, 12];
      for (let i = 0; i < notes.length; i++) {
        const o = this._ctx.createOscillator();
        o.type = 'sine';
        o.frequency.value = 880 * SEMITONE ** notes[i];
        const g = this._ctx.createGain();
        const dur = 0.12 + i * 0.03;
        g.gain.setValueAtTime(0.001, t + i * 0.06);
        g.gain.exponentialRampToValueAtTime(0.06, t + i * 0.06 + 0.01);
        g.gain.exponentialRampToValueAtTime(0.001, t + i * 0.06 + dur);
        o.connect(g).connect(this._fxGain);
        o.start(t + i * 0.06);
        o.stop(t + i * 0.06 + dur + 0.02);
      }
    } catch {}
  }

  /** pickup(): cheerful 3-note arpeggio when a power-up is caught. */
  pickup() {
    if (!this._ready()) return;
    try {
      const t = this._now();
      const notes = [12, 16, 19]; // A4 C#5 E5 — major third gives it lift
      for (let i = 0; i < notes.length; i++) {
        const o = this._ctx.createOscillator();
        o.type = 'triangle';
        o.frequency.value = 440 * SEMITONE ** (notes[i] - 12);
        const g = this._ctx.createGain();
        const dur = 0.12 + i * 0.04;
        g.gain.setValueAtTime(0.001, t + i * 0.08);
        g.gain.exponentialRampToValueAtTime(0.09, t + i * 0.08 + 0.012);
        g.gain.exponentialRampToValueAtTime(0.001, t + i * 0.08 + dur);
        o.connect(g).connect(this._fxGain);
        o.start(t + i * 0.08);
        o.stop(t + i * 0.08 + dur + 0.02);
      }
    } catch {}
  }

  /** multiball(): energetic 4-note rising fanfare for the multiball power-up. */
  multiball() {
    if (!this._ready()) return;
    try {
      const t = this._now();
      const notes = [0, 5, 10, 15, 19];
      for (let i = 0; i < notes.length; i++) {
        const o = this._ctx.createOscillator();
        o.type = 'square';
        o.frequency.value = 330 * SEMITONE ** notes[i];
        const g = this._ctx.createGain();
        const dur = 0.09 + i * 0.03;
        g.gain.setValueAtTime(0.001, t + i * 0.06);
        g.gain.exponentialRampToValueAtTime(0.06, t + i * 0.06 + 0.01);
        g.gain.exponentialRampToValueAtTime(0.001, t + i * 0.06 + dur);
        o.connect(g).connect(this._fxGain);
        o.start(t + i * 0.06);
        o.stop(t + i * 0.06 + dur + 0.02);
      }
    } catch {}
  }

  /** lifeLost(): sad descending two-note fall. */
  lifeLost() {
    if (!this._ready()) return;
    try {
      const t = this._now();
      const seq = [0, -4];
      for (let i = 0; i < seq.length; i++) {
        const o = this._ctx.createOscillator();
        o.type = 'triangle';
        o.frequency.setValueAtTime(440 * SEMITONE ** seq[i], t + i * 0.22);
        o.frequency.exponentialRampToValueAtTime(440 * SEMITONE ** (seq[i] - 3), t + i * 0.22 + 0.3);
        const g = this._ctx.createGain();
        const dur = 0.35;
        g.gain.setValueAtTime(0.001, t + i * 0.22);
        g.gain.exponentialRampToValueAtTime(0.09, t + i * 0.22 + 0.02);
        g.gain.exponentialRampToValueAtTime(0.001, t + i * 0.22 + dur);
        o.connect(g).connect(this._fxGain);
        o.start(t + i * 0.22);
        o.stop(t + i * 0.22 + dur + 0.02);
      }
    } catch {}
  }

  /** extraLife(): bright major arpeggio, a fast rising fanfare. */
  extraLife() {
    if (!this._ready()) return;
    try {
      const t = this._now();
      const notes = [0, 4, 7, 12, 16];
      for (let i = 0; i < notes.length; i++) {
        const o = this._ctx.createOscillator();
        o.type = 'triangle';
        o.frequency.value = 523 * SEMITONE ** notes[i];
        const g = this._ctx.createGain();
        const dur = 0.1 + i * 0.02;
        g.gain.setValueAtTime(0.001, t + i * 0.07);
        g.gain.exponentialRampToValueAtTime(0.07, t + i * 0.07 + 0.01);
        g.gain.exponentialRampToValueAtTime(0.001, t + i * 0.07 + dur);
        o.connect(g).connect(this._fxGain);
        o.start(t + i * 0.07);
        o.stop(t + i * 0.07 + dur + 0.02);
      }
    } catch {}
  }

  /** explosion(): deep boom + big noise burst (bomb bricks hit hardest). */
  explosion() {
    if (!this._ready()) return;
    try {
      const t = this._now();
      // sub boom
      const o = this._ctx.createOscillator();
      o.type = 'sine';
      o.frequency.setValueAtTime(130, t);
      o.frequency.exponentialRampToValueAtTime(28, t + 0.5);
      const g = this._ctx.createGain();
      g.gain.setValueAtTime(0.8, t);
      g.gain.exponentialRampToValueAtTime(0.001, t + 0.7);
      o.connect(g).connect(this._fxGain);
      o.start(t);
      o.stop(t + 0.75);

      // big noise blast, lowpassed and long
      const src = this._ctx.createBufferSource();
      src.buffer = this._noiseBuffer;
      const lp = this._ctx.createBiquadFilter();
      lp.type = 'lowpass';
      lp.frequency.setValueAtTime(6000, t);
      lp.frequency.exponentialRampToValueAtTime(200, t + 0.5);
      const ng = this._ctx.createGain();
      ng.gain.setValueAtTime(0.45, t);
      ng.gain.exponentialRampToValueAtTime(0.001, t + 0.6);
      src.connect(lp).connect(ng).connect(this._fxGain);
      src.start(t);
      src.stop(t + 0.65);
    } catch {}
  }

  /** death(): long, low descending tone — game-over-ish gloom. */
  death() {
    if (!this._ready()) return;
    try {
      const t = this._now();
      const o = this._ctx.createOscillator();
      o.type = 'sawtooth';
      o.frequency.setValueAtTime(220, t);
      o.frequency.exponentialRampToValueAtTime(64, t + 1.4);
      const lp = this._ctx.createBiquadFilter();
      lp.type = 'lowpass';
      lp.frequency.setValueAtTime(1200, t);
      lp.frequency.exponentialRampToValueAtTime(300, t + 1.4);
      const g = this._ctx.createGain();
      g.gain.setValueAtTime(0.001, t);
      g.gain.exponentialRampToValueAtTime(0.22, t + 0.08);
      g.gain.exponentialRampToValueAtTime(0.001, t + 1.5);
      o.connect(lp).connect(g).connect(this._fxGain);
      o.start(t);
      o.stop(t + 1.55);
    } catch {}
  }

  /** levelComplete(): triumphant ascending fanfare, two quick arpeggio rounds. */
  levelComplete() {
    if (!this._ready()) return;
    try {
      const t = this._now();
      const rounds = [
        [0, 3, 7, 12],
        [12, 15, 19, 24],
      ];
      const base = 330;
      for (let r = 0; r < rounds.length; r++) {
        for (let i = 0; i < rounds[r].length; i++) {
          const o = this._ctx.createOscillator();
          o.type = 'triangle';
          o.frequency.value = base * SEMITONE ** rounds[r][i];
          const g = this._ctx.createGain();
          const start = t + r * 0.2 + i * 0.05;
          const dur = 0.14;
          g.gain.setValueAtTime(0.001, start);
          g.gain.exponentialRampToValueAtTime(0.08, start + 0.01);
          g.gain.exponentialRampToValueAtTime(0.001, start + dur);
          o.connect(g).connect(this._fxGain);
          o.start(start);
          o.stop(start + dur + 0.02);
        }
      }
    } catch {}
  }

  /** gameOver(): slower, melancholy, minor descending run. */
  gameOver() {
    if (!this._ready()) return;
    try {
      const t = this._now();
      const seq = [0, -2, -5, -9, -12, -16]; // A minor descent, ending low
      const base = 440;
      for (let i = 0; i < seq.length; i++) {
        const o = this._ctx.createOscillator();
        o.type = 'sine';
        o.frequency.value = base * SEMITONE ** seq[i];
        const g = this._ctx.createGain();
        const start = t + i * 0.18;
        const dur = 0.28;
        g.gain.setValueAtTime(0.001, start);
        g.gain.exponentialRampToValueAtTime(0.08, start + 0.02);
        g.gain.exponentialRampToValueAtTime(0.001, start + dur);
        o.connect(g).connect(this._fxGain);
        o.start(start);
        o.stop(start + dur + 0.02);
      }
      // and a low mournful final tone
      const f = this._ctx.createOscillator();
      f.type = 'triangle';
      f.frequency.setValueAtTime(110, t + 1.1);
      f.frequency.exponentialRampToValueAtTime(82, t + 1.9);
      const fg = this._ctx.createGain();
      fg.gain.setValueAtTime(0.001, t + 1.1);
      fg.gain.exponentialRampToValueAtTime(0.1, t + 1.2);
      fg.gain.exponentialRampToValueAtTime(0.001, t + 2.1);
      f.connect(fg).connect(this._fxGain);
      f.start(t + 1.1);
      f.stop(t + 2.15);
    } catch {}
  }

  /** uiClick(): tiny click for UI buttons — short filtered blip. */
  uiClick() {
    if (!this._ready()) return;
    try {
      const t = this._now();
      const o = this._ctx.createOscillator();
      o.type = 'square';
      o.frequency.value = 700;
      const g = this._ctx.createGain();
      const dur = 0.05;
      g.gain.setValueAtTime(0.05, t);
      g.gain.exponentialRampToValueAtTime(0.001, t + dur);
      o.connect(g).connect(this._fxGain);
      o.start(t);
      o.stop(t + dur + 0.02);
    } catch {}
  }
}
