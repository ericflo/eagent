// audio/engine.js — WebAudio plumbing: buses, master compressor, voice pool, synth primitives.
//
// Bus graph
//
//   sfx voices ─────────────────────────────► sfxBus ┐
//   music layers ─► musicPump ─► musicFilter ─► musicDuck ─► musicBus ┤
//                                                                     ├─► master ─► comp ─► out
//
// Everything is generated: oscillators, filtered noise, FM pairs. No samples.

import { clamp } from '../util.js';

export const mtof = (m) => 440 * Math.pow(2, (m - 69) / 12);
const EPS = 0.0001;

/** Shared white-noise buffer (1 s, mono). */
function makeNoise(ctx) {
  const len = Math.max(1, Math.floor(ctx.sampleRate * 1.0));
  const buf = ctx.createBuffer(1, len, ctx.sampleRate);
  const d = buf.getChannelData(0);
  for (let i = 0; i < len; i++) d[i] = Math.random() * 2 - 1;
  return buf;
}

export class Engine {
  constructor(ctx, { offline = false, musicVolume = 0.5, sfxVolume = 0.75, maxVoices = 16 } = {}) {
    this.ctx = ctx;
    this.offline = offline;
    this.maxVoices = maxVoices;
    this.voices = [];
    this.musicVolume = musicVolume;
    this.sfxVolume = sfxVolume;

    // ---- master: gain -> compressor (safety glue) -> destination
    this.master = ctx.createGain();
    this.master.gain.value = 0.92;
    this.comp = ctx.createDynamicsCompressor();
    this.comp.threshold.value = -10;
    this.comp.knee.value = 24;
    this.comp.ratio.value = 6;
    this.comp.attack.value = 0.004;
    this.comp.release.value = 0.18;
    this.master.connect(this.comp);
    this.comp.connect(ctx.destination);

    // ---- music bus
    this.musicBus = ctx.createGain();
    this.musicBus.gain.value = musicVolume;
    this.musicBus.connect(this.master);
    this.musicDuck = ctx.createGain();      // ducked by 'lose' etc.
    this.musicDuck.gain.value = 1;
    this.musicDuck.connect(this.musicBus);
    this.musicFilter = ctx.createBiquadFilter(); // opens up with intensity
    this.musicFilter.type = 'lowpass';
    this.musicFilter.frequency.value = 620;
    this.musicFilter.Q.value = 0.9;
    this.musicFilter.connect(this.musicDuck);
    this.musicPump = ctx.createGain();      // sidechain-style pumping
    this.musicPump.gain.value = 1;
    this.musicPump.connect(this.musicFilter);

    // ---- sfx bus
    this.sfxBus = ctx.createGain();
    this.sfxBus.gain.value = sfxVolume;
    this.sfxBus.connect(this.master);

    this.noiseBuf = makeNoise(ctx);
  }

  now() { return this.ctx.currentTime; }

  /** Clamp a filter cutoff into the legal (sub-Nyquist) range — WebAudio warns otherwise. */
  hz(v) {
    const max = Math.min(20000, this.ctx.sampleRate * 0.5 - 200);
    return clamp(v, 20, max);
  }

  setMusicVolume(v) {
    this.musicVolume = clamp(v, 0, 1);
    this._ramp(this.musicBus.gain, this.musicVolume, 0.05);
  }

  setSfxVolume(v) {
    this.sfxVolume = clamp(v, 0, 1);
    this._ramp(this.sfxBus.gain, this.sfxVolume, 0.05);
  }

  _ramp(param, value, time) {
    const t = this.now();
    try {
      param.cancelScheduledValues(t);
      param.setValueAtTime(param.value, t);
      param.linearRampToValueAtTime(value, t + Math.max(0.005, time));
    } catch (e) { param.value = value; }
  }

  /** Duck the music (0..1 amount) for `dur` seconds — used when a ball is lost. */
  duck(amount = 0.45, dur = 1.0, when = null) {
    const t = when ?? this.now();
    const g = this.musicDuck.gain;
    try {
      g.cancelScheduledValues(t);
      g.setValueAtTime(g.value, t);
      g.linearRampToValueAtTime(clamp(1 - amount, 0.02, 1), t + 0.04);
      g.linearRampToValueAtTime(1, t + Math.max(0.2, dur));
    } catch (e) { /* ignore */ }
  }

  // ---------------------------------------------------------------- voices

  /** Register a transient sfx voice so we can cap polyphony. */
  _voice(endTime, gainNode, sources) {
    if (this.offline) return null;   // offline renders schedule everything up front
    const t = this.now();
    // prune finished
    if (this.voices.length) {
      this.voices = this.voices.filter((v) => v.end > t - 0.05);
    }
    const v = { end: endTime, gainNode, sources };
    this.voices.push(v);
    while (this.voices.length > this.maxVoices) {
      const old = this.voices.shift();
      this._kill(old);
    }
    return v;
  }

  _kill(v) {
    const t = this.now();
    try {
      v.gainNode.gain.cancelScheduledValues(t);
      v.gainNode.gain.setValueAtTime(Math.max(EPS, v.gainNode.gain.value), t);
      v.gainNode.gain.exponentialRampToValueAtTime(EPS, t + 0.02);
      for (const s of v.sources) { try { s.stop(t + 0.03); } catch (e) { /* already stopped */ } }
    } catch (e) { /* ignore */ }
  }

  /** Destination for a voice: sfx bus, optionally through a stereo panner. */
  _out(pan) {
    if (pan && this.ctx.createStereoPanner) {
      const p = this.ctx.createStereoPanner();
      p.pan.value = clamp(pan, -1, 1);
      p.connect(this.sfxBus);
      return p;
    }
    return this.sfxBus;
  }

  // ---------------------------------------------------------------- primitives

  /**
   * One oscillator, AD envelope, optional glide + vibrato.
   * `bus` overrides the destination (music layers pass their own gain node).
   */
  tone({
    freq = 440, freq2 = null, type = 'square', dur = 0.12, gain = 0.2, when = null, delay = 0,
    attack = 0.004, hold = 0, detune = 0, pan = 0, bus = null, glideAt = 0.5, curve = 'exp',
    filter = null, filterQ = 1, filterTo = null,
  }) {
    const ctx = this.ctx;
    const t0 = (when ?? this.now()) + delay;
    const osc = ctx.createOscillator();
    osc.type = type;
    osc.detune.value = detune;
    const f0 = Math.max(15, freq);
    osc.frequency.setValueAtTime(f0, t0);
    if (freq2) {
      const f1 = Math.max(15, freq2);
      osc.frequency.exponentialRampToValueAtTime(f1, t0 + Math.max(0.01, dur * glideAt));
    }
    const g = ctx.createGain();
    const peak = Math.max(EPS * 2, gain);
    g.gain.setValueAtTime(EPS, t0);
    g.gain.exponentialRampToValueAtTime(peak, t0 + Math.max(0.001, attack));
    if (hold > 0) g.gain.setValueAtTime(peak, t0 + attack + hold);
    if (curve === 'lin') g.gain.linearRampToValueAtTime(EPS, t0 + dur);
    else g.gain.exponentialRampToValueAtTime(EPS, t0 + dur);

    let node = osc;
    if (filter) {
      const bq = ctx.createBiquadFilter();
      bq.type = typeof filter === 'string' ? filter : 'lowpass';
      const ff = typeof filter === 'number' ? filter : (filterTo ? f0 * 6 : 3000);
      bq.frequency.setValueAtTime(this.hz(ff), t0);
      if (filterTo) bq.frequency.exponentialRampToValueAtTime(this.hz(filterTo), t0 + dur);
      bq.Q.value = filterQ;
      node.connect(bq);
      node = bq;
    }
    node.connect(g);
    g.connect(bus || this._out(pan));
    osc.start(t0);
    osc.stop(t0 + dur + 0.06);
    if (!bus) this._voice(t0 + dur, g, [osc]);
    return g;
  }

  /** Filtered noise burst — impacts, shatters, hats, whooshes. */
  noise({
    dur = 0.15, gain = 0.2, freq = 1200, sweep = null, q = 1, type = 'bandpass',
    when = null, delay = 0, attack = 0.002, pan = 0, bus = null, curve = 'exp',
  }) {
    const ctx = this.ctx;
    const t0 = (when ?? this.now()) + delay;
    const src = ctx.createBufferSource();
    src.buffer = this.noiseBuf;
    src.loop = true;
    src.playbackRate.value = 0.8 + Math.random() * 0.4;
    const f = ctx.createBiquadFilter();
    f.type = type;
    f.frequency.setValueAtTime(this.hz(freq), t0);
    if (sweep) f.frequency.exponentialRampToValueAtTime(this.hz(sweep), t0 + dur);
    f.Q.value = q;
    const g = ctx.createGain();
    const peak = Math.max(EPS * 2, gain);
    g.gain.setValueAtTime(EPS, t0);
    g.gain.exponentialRampToValueAtTime(peak, t0 + Math.max(0.001, attack));
    if (curve === 'lin') g.gain.linearRampToValueAtTime(EPS, t0 + dur);
    else g.gain.exponentialRampToValueAtTime(EPS, t0 + dur);
    src.connect(f);
    f.connect(g);
    g.connect(bus || this._out(pan));
    src.start(t0);
    src.stop(t0 + dur + 0.06);
    if (!bus) this._voice(t0 + dur, g, [src]);
    return g;
  }

  /** Two-operator FM — metallic clanks, bells, prism pings. */
  fm({
    freq = 300, ratio = 2.7, index = 400, indexEnd = 10, type = 'sine', modType = 'sine',
    dur = 0.2, gain = 0.15, when = null, delay = 0, attack = 0.003, pan = 0, bus = null, freq2 = null,
  }) {
    const ctx = this.ctx;
    const t0 = (when ?? this.now()) + delay;
    const car = ctx.createOscillator();
    car.type = type;
    car.frequency.setValueAtTime(Math.max(15, freq), t0);
    if (freq2) car.frequency.exponentialRampToValueAtTime(Math.max(15, freq2), t0 + dur);
    const mod = ctx.createOscillator();
    mod.type = modType;
    mod.frequency.setValueAtTime(Math.max(15, freq * ratio), t0);
    const modGain = ctx.createGain();
    modGain.gain.setValueAtTime(index, t0);
    modGain.gain.exponentialRampToValueAtTime(Math.max(1, indexEnd), t0 + dur);
    mod.connect(modGain);
    modGain.connect(car.frequency);
    const g = ctx.createGain();
    const peak = Math.max(EPS * 2, gain);
    g.gain.setValueAtTime(EPS, t0);
    g.gain.exponentialRampToValueAtTime(peak, t0 + Math.max(0.001, attack));
    g.gain.exponentialRampToValueAtTime(EPS, t0 + dur);
    car.connect(g);
    g.connect(bus || this._out(pan));
    car.start(t0); mod.start(t0);
    car.stop(t0 + dur + 0.06); mod.stop(t0 + dur + 0.06);
    if (!bus) this._voice(t0 + dur, g, [car, mod]);
    return g;
  }
}

export default Engine;
