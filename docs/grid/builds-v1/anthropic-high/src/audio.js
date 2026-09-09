// audio.js — OVERTOP audio: adaptive procedural music + fully synthesized SFX. No samples.
//
// Public API (used by game.js / main.js):
//   audio.unlock()                 create/resume the AudioContext from a user gesture
//   audio.play(name, opts)         fire an event sound
//   audio.setIntensity(0..1)       how powered-up the player is (drives music layers + brightness)
//   audio.setMuted(b) / toggleMute() / audio.muted
// Extras:
//   audio.setMusicVolume(v) / setSfxVolume(v) / audio.musicVolume / audio.sfxVolume
//   audio.setState('title'|'playing'|'paused'|'gameover')   optional, see README of this file
//   audio.setOvertop(bool)         optional explicit overtop flag (also inferred from events)
//   audio.debug                    { notesScheduled, sfxPlayed, voices, state, intensity }
//   audio.renderOffline(sec)       OfflineAudioContext render, returns { peak, rms, ... }
//
// Layout:  src/audio/engine.js  buses + voice pool + synth primitives
//          src/audio/music.js   lookahead scheduler, intensity-gated layers
//          src/audio/sfx.js     event -> sound recipes

import { clamp, storage } from './util.js';
import { Engine } from './audio/engine.js';
import { Music } from './audio/music.js';
import { playSfx } from './audio/sfx.js';

const MUTE_KEY = 'overtop.muted';
const MUSIC_KEY = 'overtop.musicVol';
const SFX_KEY = 'overtop.sfxVol';

// Per-event minimum interval (ms) so bursts never turn into a wall of noise.
const MIN_MS = {
  break: 26, breakGlass: 30, breakArmor: 30, breakPrism: 30, breakGhost: 30, breakCap: 30,
  ping: 40, clank: 45, ring: 70, steel: 45, wall: 32, ceiling: 30, paddle: 30, smash: 30,
  laser: 55, boom: 70, boost: 60, launch: 40, powerup: 120, multiball: 250, multiplier: 70,
  overtop: 400, overtopEnd: 300, lose: 300, levelup: 400, gameover: 500, ui: 45,
};
const DEFAULT_MIN_MS = 30;

class AudioSystem {
  constructor() {
    this.ctx = null;
    this.eng = null;
    this.music = null;
    this.intensity = 0;
    this.state = 'title';
    this.overtop = false;
    this.available = true;
    this.muted = storage.get(MUTE_KEY, false) === true;
    this.musicVolume = clamp(Number(storage.get(MUSIC_KEY, 0.5)) || 0.5, 0, 1);
    this.sfxVolume = clamp(Number(storage.get(SFX_KEY, 0.75)) || 0.75, 0, 1);
    this._last = Object.create(null);
    this._streak = 0;
    this._streakAt = -1e9;
    this._sfxPlayed = 0;
  }

  // ------------------------------------------------------------ lifecycle

  /** Must be called from a user gesture. Safe to call repeatedly; never throws. */
  unlock() {
    try {
      if (!this.ctx) {
        const AC = globalThis.AudioContext || globalThis.webkitAudioContext;
        if (!AC) { this.available = false; return false; }
        this.ctx = new AC();
        this.eng = new Engine(this.ctx, { musicVolume: this.musicVolume, sfxVolume: this.sfxVolume });
        this.music = new Music(this.eng);
        this.music.setIntensity(this.intensity);
        this._applyMute(true);
      }
      if (this.ctx.state === 'suspended') {
        const p = this.ctx.resume();
        if (p && typeof p.catch === 'function') p.catch(() => {});
      }
      if (!this.muted) this.music.start();
      return true;
    } catch (e) {
      this.available = false;
      this.ctx = null; this.eng = null; this.music = null;
      return false;
    }
  }

  get ready() { return !!(this.ctx && this.eng); }
  get t() { return this.ctx ? this.ctx.currentTime : 0; }

  get debug() {
    return {
      notesScheduled: this.music ? this.music.debug.notesScheduled : 0,
      bars: this.music ? this.music.debug.bars : 0,
      ticks: this.music ? this.music.debug.ticks : 0,
      sfxPlayed: this._sfxPlayed,
      voices: this.eng ? this.eng.voices.length : 0,
      running: !!(this.music && this.music.running),
      ctxState: this.ctx ? this.ctx.state : 'none',
      intensity: this.intensity,
      state: this.state,
      overtop: this.overtop,
      gates: this.music ? { ...this.music.gate } : null,
    };
  }

  // ------------------------------------------------------------ mixer

  setIntensity(v) {
    this.intensity = clamp(Number(v) || 0, 0, 1);
    if (this.music) this.music.setIntensity(this.intensity);
  }

  setMuted(m) {
    this.muted = !!m;
    storage.set(MUTE_KEY, this.muted);
    this._applyMute(false);
  }

  toggleMute() { this.setMuted(!this.muted); return this.muted; }

  _applyMute(initial) {
    if (!this.eng) return;
    try {
      this.eng._ramp(this.eng.master.gain, this.muted ? 0 : 0.92, 0.08);
      if (this.muted) {
        if (this.music) setTimeout(() => { if (this.muted && this.music) this.music.stop(); }, 150);
      } else if (this.music && !initial) {
        this.music.start();
      }
    } catch (e) { /* ignore */ }
  }

  setMusicVolume(v) {
    this.musicVolume = clamp(Number(v) || 0, 0, 1);
    storage.set(MUSIC_KEY, this.musicVolume);
    if (this.eng) this.eng.setMusicVolume(this.musicVolume);
  }

  setSfxVolume(v) {
    this.sfxVolume = clamp(Number(v) || 0, 0, 1);
    storage.set(SFX_KEY, this.sfxVolume);
    if (this.eng) this.eng.setSfxVolume(this.sfxVolume);
  }

  /** Optional hook: 'title' | 'serve' | 'playing' | 'paused' | 'levelclear' | 'gameover'. */
  setState(s) {
    if (s === this.state) return;
    this.state = s;
    if (!this.music || this.muted) return;
    try {
      if (s === 'paused') { this.eng.duck(0.65, 4.0); }
      else if (s === 'gameover') { this.music.release(); this.eng.duck(0.5, 2.5); }
      else { this.eng.duck(0, 0.2); }
    } catch (e) { /* ignore */ }
  }

  /** Optional hook: explicit overtop flag. play('overtop'/'overtopEnd') does this too. */
  setOvertop(on) {
    on = !!on;
    if (on === this.overtop) return;
    this.overtop = on;
    if (!this.music) return;
    try { if (on) this.music.hit(); else this.music.release(); } catch (e) { /* ignore */ }
  }

  // ------------------------------------------------------------ events

  _throttle(name) {
    const ms = MIN_MS[name] ?? DEFAULT_MIN_MS;
    const now = globalThis.performance?.now?.() ?? Date.now();
    const last = this._last[name];
    if (last !== undefined && now - last < ms) return false;
    this._last[name] = now;
    return true;
  }

  /** Melodic index for brick breaks: consecutive breaks walk up the pentatonic. */
  _melodyStep() {
    const now = globalThis.performance?.now?.() ?? Date.now();
    if (now - this._streakAt > 700) this._streak = 0;
    else this._streak++;
    this._streakAt = now;
    const octaveLift = this.intensity > 0.66 ? 5 : 0;   // one pentatonic octave up when hot
    return (this._streak % 8) + octaveLift;
  }

  play(name, opts = {}) {
    if (!this.ready || this.muted || !name) return;
    if (!this._throttle(name)) return;
    try {
      const o = opts || {};
      const isBreak = name === 'break' || name.startsWith('break') || name === 'boost' || name === 'boom';
      const step = isBreak ? this._melodyStep() : 0;

      // musical reactions
      if (name === 'overtop') { this.overtop = true; this.music.hit(); }
      else if (name === 'overtopEnd') { this.overtop = false; this.music.release(); }
      else if (name === 'lose') { this.eng.duck(0.55, 1.0); this.overtop = false; this.music.release(); }
      else if (name === 'gameover') { this.state = 'gameover'; this.music.release(); this.eng.duck(0.45, 2.2); }
      else if (name === 'levelup') { this.eng.duck(0.3, 1.4); }

      playSfx(this.eng, this.music, name, { ...o, step }, this.intensity, null);
      this._sfxPlayed++;
    } catch (e) { /* audio must never break the game */ }
  }

  // ------------------------------------------------------------ offline render (tests)

  /**
   * Render `seconds` of music (+ a sweep of every SFX) with an OfflineAudioContext.
   * Returns { peak, rms, notesScheduled, duration } or null when unavailable.
   */
  async renderOffline(seconds = 6, { intensity = 1, sampleRate = 44100 } = {}) {
    const OAC = globalThis.OfflineAudioContext || globalThis.webkitOfflineAudioContext;
    if (!OAC) return null;
    const ctx = new OAC(2, Math.floor(sampleRate * seconds), sampleRate);
    const eng = new Engine(ctx, { offline: true, musicVolume: this.musicVolume, sfxVolume: this.sfxVolume });
    const music = new Music(eng);
    music.setIntensity(intensity);
    music.start();
    music.scheduleUntil(seconds);
    const names = [
      'break', 'breakGlass', 'breakArmor', 'breakPrism', 'breakGhost', 'breakCap', 'boom', 'boost',
      'steel', 'clank', 'ping', 'ring', 'paddle', 'smash', 'wall', 'ceiling', 'launch', 'laser',
      'powerup', 'multiball', 'multiplier', 'overtop', 'overtopEnd', 'lose', 'levelup', 'gameover', 'ui',
    ];
    names.forEach((n, i) => {
      const when = 0.2 + (i * (seconds - 0.6)) / names.length;
      if (n === 'overtop') music.hit(when);
      if (n === 'overtopEnd') music.release(when);
      playSfx(eng, music, n, { pan: 0, step: i % 8, combo: (i % 8) + 1, level: (i % 8) + 1, pitch: 0 }, intensity, when);
    });
    const buf = await ctx.startRendering();
    let peak = 0, sum = 0, n = 0;
    for (let c = 0; c < buf.numberOfChannels; c++) {
      const d = buf.getChannelData(c);
      for (let i = 0; i < d.length; i++) {
        const a = Math.abs(d[i]);
        if (a > peak) peak = a;
        sum += d[i] * d[i];
        n++;
      }
    }
    return {
      peak,
      rms: Math.sqrt(sum / Math.max(1, n)),
      dbfs: 20 * Math.log10(Math.max(1e-6, peak)),
      notesScheduled: music.debug.notesScheduled,
      duration: buf.duration,
    };
  }
}

export const audio = new AudioSystem();
globalThis.__audio = audio;
export default audio;
