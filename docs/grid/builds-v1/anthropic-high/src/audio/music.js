// audio/music.js — adaptive procedural score. Lookahead scheduler (25 ms tick / 0.12 s window),
// five layers gated by `intensity`, A-minor synthwave progression at 120 BPM.
//
//   intensity   layers
//   0.00        soft pad + sparse root bass                  (filter mostly closed)
//   0.25        walking bass line + four-on-the-floor kick
//   0.50        hats + snare + 16th arpeggio
//   0.75        lead melody
//   1.00        double-time hats, octave-doubled arp, sidechain pumping, filter wide open
//
// Layer volumes ramp continuously (~1 beat); drum/arp/lead *entries* are quantized to the bar.

import { mtof } from './engine.js';
import { clamp } from '../util.js';

const BPM = 120;
const SPB = 60 / BPM;          // seconds per beat
const STEP = SPB / 4;          // 16th note
const BAR = 16;                // steps per bar

// i - VI - III - VII in A minor (classic synthwave loop).
const PROG = [
  { root: 45, third: 3 },  // Am
  { root: 41, third: 4 },  // F
  { root: 48, third: 4 },  // C
  { root: 43, third: 4 },  // G
];

const MINOR = [0, 2, 3, 5, 7, 8, 10];
const PENT_MIN = [0, 3, 5, 7, 10];
const PENT_MAJ = [0, 2, 4, 7, 9];

// Lead motif: [step, scale-degree] per bar, degrees index A-minor from A4.
const LEAD = [
  [[0, 7], [3, 9], [6, 8], [10, 7], [12, 5]],
  [[0, 5], [4, 7], [7, 9], [11, 10]],
  [[0, 9], [3, 8], [6, 7], [9, 5], [12, 4]],
  [[0, 4], [2, 5], [6, 7], [8, 9], [12, 11], [14, 10]],
];
const BASS_STEPS = [0, 3, 6, 8, 11, 14];

export class Music {
  constructor(engine) {
    this.eng = engine;
    this.ctx = engine.ctx;
    this.bpm = BPM;
    this.stepDur = STEP;
    this.step = 0;              // global 16th counter
    this.nextTime = 0;
    this.running = false;
    this.timer = null;
    this.lookahead = 0.12;
    this.tickMs = 25;
    this.intensity = 0;
    this.open = 0;              // 0..1 overtop filter-open amount
    this.debug = { notesScheduled: 0, bars: 0, ticks: 0 };

    const mk = () => { const g = this.ctx.createGain(); g.connect(engine.musicPump); return g; };
    this.g = {
      pad: mk(), bass: mk(), drums: mk(), arp: mk(), lead: mk(), fx: mk(),
    };
    this.g.pad.gain.value = 0.0001;
    this.g.bass.gain.value = 0.0001;
    this.g.drums.gain.value = 0.0001;
    this.g.arp.gain.value = 0.0001;
    this.g.lead.gain.value = 0.0001;
    this.g.fx.gain.value = 1;
    this.gate = { drums: false, hats: false, arp: false, lead: false, dbl: false };
  }

  // ------------------------------------------------------------ transport

  start() {
    if (this.running) return;
    this.running = true;
    this.nextTime = Math.max(this.nextTime, this.ctx.currentTime + 0.08);
    this._mix(this.nextTime);
    if (this.eng.offline) return;      // offline renders via scheduleUntil()
    const tick = () => {
      if (!this.running) return;
      this.debug.ticks++;
      try { this.scheduleUntil(this.ctx.currentTime + this.lookahead); } catch (e) { /* never throw */ }
    };
    this.timer = setInterval(tick, this.tickMs);
    tick();
  }

  stop() {
    this.running = false;
    if (this.timer) { clearInterval(this.timer); this.timer = null; }
  }

  setIntensity(v) { this.intensity = clamp(v, 0, 1); }

  /** Schedule every step whose start time falls before `horizon`. */
  scheduleUntil(horizon) {
    // If the tab was backgrounded the timer stalls; resync instead of dumping a
    // pile of past-due notes into the graph all at once (which would blast).
    const now = this.ctx.currentTime;
    if (!this.eng.offline && this.nextTime < now - 0.1) {
      const skip = Math.ceil((now + 0.05 - this.nextTime) / this.stepDur);
      this.nextTime += skip * this.stepDur;
      this.step += skip;
    }
    let guard = 0;
    while (this.nextTime < horizon && guard++ < 512) {
      this._step(this.step, this.nextTime);
      this.nextTime += this.stepDur;
      this.step++;
    }
  }

  chordAt(step) { return PROG[Math.floor(step / BAR) % PROG.length]; }
  get chord() { return this.chordAt(this.step); }

  /** Frequency of the i-th pentatonic degree of the current chord (for melodic brick breaks). */
  pentFreq(i, octave = 2) {
    const c = this.chord;
    const scale = c.third === 3 ? PENT_MIN : PENT_MAJ;
    const n = ((i % scale.length) + scale.length) % scale.length;
    const oct = Math.floor(i / scale.length);
    let midi = c.root + 12 * (octave + oct) + scale[n];
    while (midi > 105) midi -= 12;   // keep streaks out of dog-whistle territory
    while (midi < 33) midi += 12;
    return mtof(midi);
  }

  /** Frequency of a diatonic degree above A (deg 0 = A4 at octave 0). */
  scaleFreq(deg, base = 69) {
    const n = ((deg % 7) + 7) % 7;
    const oct = Math.floor(deg / 7);
    return mtof(base + 12 * oct + MINOR[n]);
  }

  // ------------------------------------------------------------ mixing

  _mix(t) {
    const I = this.intensity;
    const set = (param, target, tc = SPB * 0.45) => {
      try { param.setTargetAtTime(Math.max(0.0001, target), t, tc); } catch (e) { param.value = target; }
    };
    set(this.g.pad.gain, 0.55 - 0.16 * I);
    set(this.g.bass.gain, 0.4 + 0.55 * I);
    set(this.g.drums.gain, this.gate.drums ? 0.7 + 0.55 * clamp((I - 0.25) / 0.4, 0, 1) : 0.0001);
    set(this.g.arp.gain, this.gate.arp ? 0.45 + 0.4 * clamp((I - 0.5) / 0.5, 0, 1) : 0.0001);
    set(this.g.lead.gain, this.gate.lead ? 0.5 + 0.4 * clamp((I - 0.75) / 0.25, 0, 1) : 0.0001);

    // Brightness opens up with power. Overtop blows it wide open.
    const base = 480 + 5200 * Math.pow(I, 1.35);
    const target = this.eng.hz(clamp(base * (1 + 2.2 * this.open), 200, 15000));
    try {
      this.eng.musicFilter.frequency.setTargetAtTime(target, t, SPB * 0.4);
      this.eng.musicFilter.Q.setTargetAtTime(0.8 + 2.4 * this.open + 1.2 * I, t, SPB * 0.4);
    } catch (e) { /* ignore */ }
  }

  _gates(I) {
    const on = (cur, lo) => (cur ? I > lo - 0.06 : I >= lo);
    this.gate.drums = on(this.gate.drums, 0.22);
    this.gate.hats = on(this.gate.hats, 0.45);
    this.gate.arp = on(this.gate.arp, 0.5);
    this.gate.lead = on(this.gate.lead, 0.72);
    this.gate.dbl = on(this.gate.dbl, 0.88);
  }

  // ------------------------------------------------------------ the sequencer

  _step(i, t) {
    const s = i % BAR;
    const barIx = Math.floor(i / BAR) % PROG.length;
    const c = PROG[barIx];
    const I = this.intensity;

    if (s === 0) { this._gates(I); this.debug.bars++; }
    if (s % 4 === 0) this._mix(t);

    // ---- pad: one sustained chord per bar, always audible
    if (s === 0) this._pad(t, c);

    // ---- bass
    if (this.gate.drums) {
      if (BASS_STEPS.includes(s)) {
        const oct = s === 0 ? 0 : (s === 11 ? 12 : 0);
        this._bass(t, c.root - 12 + oct, STEP * (s === 0 ? 2.4 : 1.6));
      }
    } else if (s === 0 || s === 8) {
      this._bass(t, c.root - 12, SPB * 1.2, 0.7);
    }

    // ---- drums
    if (this.gate.drums) {
      if (s % 4 === 0) this._kick(t, s === 0 ? 1 : 0.85);
      if (s === 14 && barIx === 3) this._kick(t, 0.7);
    }
    if (this.gate.hats) {
      if (s === 4 || s === 12) this._snare(t);
      const every = this.gate.dbl ? 1 : 2;
      if (s % every === 0) this._hat(t, s % 8 === 6, s % 4 === 0 ? 0.5 : 0.34);
    }

    // ---- arpeggio (16ths, up-down through the chord)
    if (this.gate.arp) {
      const tones = [0, c.third, 7, 12, 7, c.third];
      const n = c.root + 24 + tones[i % tones.length];
      this._arp(t, n, STEP * 1.1, 0.6);
      if (this.gate.dbl) this._arp(t + STEP * 0.5, n + 12, STEP * 0.6, 0.36);
    }

    // ---- lead melody
    if (this.gate.lead) {
      for (const [ls, deg] of LEAD[barIx]) {
        if (ls === s) this._lead(t, this.scaleFreq(deg, 57), SPB * 0.55);
      }
    }
  }

  // ------------------------------------------------------------ voices

  _n() { this.debug.notesScheduled++; }

  _pad(t, c) {
    const dur = SPB * 4.1;
    const notes = [c.root + 12, c.root + 12 + c.third, c.root + 19, c.root + 24];
    for (let k = 0; k < notes.length; k++) {
      const f = mtof(notes[k]);
      this.eng.tone({
        freq: f, type: k % 2 ? 'triangle' : 'sawtooth', dur, gain: 0.055 / (1 + k * 0.3),
        when: t, attack: SPB * 0.6, hold: SPB * 1.4, curve: 'lin', detune: (k - 1.5) * 7,
        bus: this.g.pad,
      });
      this._n();
    }
  }

  _bass(t, midi, dur, amp = 1) {
    const f = mtof(midi);
    this.eng.tone({
      freq: f, type: 'sawtooth', dur, gain: 0.24 * amp, when: t, attack: 0.008,
      filter: 'lowpass', filterQ: 6, filterTo: Math.max(90, f * 3), bus: this.g.bass,
    });
    this.eng.tone({ freq: f / 2, type: 'sine', dur: dur * 0.9, gain: 0.16 * amp, when: t, attack: 0.006, bus: this.g.bass });
    this._n();
  }

  _kick(t, amp = 1) {
    this.eng.tone({
      freq: 150, freq2: 42, type: 'sine', dur: 0.24, gain: 0.5 * amp, when: t,
      attack: 0.002, glideAt: 0.22, bus: this.g.drums,
    });
    this.eng.noise({ dur: 0.03, gain: 0.12 * amp, freq: 2200, q: 0.7, when: t, bus: this.g.drums });
    this._n();
    // sidechain-style pump, only once we are properly powered up
    const amt = clamp((this.intensity - 0.6) / 0.4, 0, 1) * 0.5;
    if (amt > 0.01) {
      const g = this.eng.musicPump.gain;
      try {
        g.setValueAtTime(1 - amt, t);
        // makeup on the rebound so the pump adds punch instead of eating level
        g.linearRampToValueAtTime(1 + amt * 0.55, t + SPB * 0.62);
      } catch (e) { /* ignore */ }
    }
  }

  _snare(t) {
    this.eng.noise({ dur: 0.16, gain: 0.24, freq: 1800, sweep: 900, q: 0.8, when: t, bus: this.g.drums });
    this.eng.tone({ freq: 220, freq2: 160, type: 'triangle', dur: 0.09, gain: 0.14, when: t, bus: this.g.drums });
    this._n();
  }

  _hat(t, open, amp) {
    this.eng.noise({
      dur: open ? 0.12 : 0.035, gain: 0.14 * amp, freq: 8200, q: 1.1, type: 'highpass',
      when: t, bus: this.g.drums,
    });
    this._n();
  }

  _arp(t, midi, dur, amp) {
    this.eng.tone({
      freq: mtof(midi), type: 'square', dur, gain: 0.1 * amp, when: t, attack: 0.003,
      filter: 'lowpass', filterQ: 3, filterTo: 4200, bus: this.g.arp,
    });
    this._n();
  }

  _lead(t, freq, dur) {
    this.eng.tone({
      freq, type: 'sawtooth', dur, gain: 0.12, when: t, attack: 0.02, detune: -6,
      filter: 'lowpass', filterQ: 4, filterTo: 5200, bus: this.g.lead,
    });
    this.eng.tone({ freq: freq * 1.005, type: 'triangle', dur: dur * 0.9, gain: 0.07, when: t, attack: 0.02, bus: this.g.lead });
    this._n();
  }

  // ------------------------------------------------------------ musical events

  /** OVERTOP: riser + chord stab, and hold the filter wide open. */
  hit(when = null) {
    const t = when ?? this.ctx.currentTime;
    this.open = 1;
    this._mix(t);
    const c = this.chord;
    // riser
    this.eng.tone({ freq: mtof(c.root), freq2: mtof(c.root + 36), type: 'sawtooth', dur: 0.75, gain: 0.13, when: t, attack: 0.05, glideAt: 0.95, bus: this.g.fx });
    this.eng.noise({ dur: 0.7, gain: 0.09, freq: 400, sweep: 9000, q: 0.7, when: t, attack: 0.2, bus: this.g.fx });
    // stab on the beat-ish
    const stab = t + 0.42;
    for (const semi of [0, c.third, 7, 12, 19]) {
      this.eng.tone({ freq: mtof(c.root + 12 + semi), type: 'sawtooth', dur: 0.5, gain: 0.1, when: stab, attack: 0.006, detune: 8, bus: this.g.fx });
      this.eng.tone({ freq: mtof(c.root + 12 + semi), type: 'square', dur: 0.34, gain: 0.05, when: stab, attack: 0.006, detune: -8, bus: this.g.fx });
      this._n();
    }
    this.eng.tone({ freq: 90, freq2: 40, type: 'sine', dur: 0.5, gain: 0.4, when: stab, bus: this.g.fx });
    this._n();
  }

  /** Leaving overtop: close the filter back down. */
  release(when = null) {
    const t = when ?? this.ctx.currentTime;
    this.open = 0;
    this._mix(t);
    this.eng.noise({ dur: 0.5, gain: 0.05, freq: 6000, sweep: 300, q: 0.8, when: t, bus: this.g.fx });
  }
}

export default Music;
