// audio/sfx.js — one synthesized voice recipe per game event.
// Everything is pitched relative to the running score where it makes musical sense:
// brick breaks walk up the pentatonic of the current chord, so a streak sounds like a melody.

import { clamp } from '../util.js';

/**
 * @param {Engine} eng
 * @param {Music}  music
 * @param {string} name  event name from game.js
 * @param {object} o     { pan, pitch, combo, level, step }
 * @param {number} I     intensity 0..1
 * @param {number|null} when  explicit schedule time (offline render); null = now
 */
export function playSfx(eng, music, name, o, I, when = null) {
  const pan = clamp(o.pan ?? 0, -1, 1);
  const W = when;
  const bright = 1 + I * 0.55;            // pitch/brightness lift as you power up
  const pent = (i, oct = 2) => (music ? music.pentFreq(i, oct) : 220 * Math.pow(2, i / 5));
  const step = o.step ?? 0;               // combo index for melodic breaks

  switch (name) {
    // ---------------------------------------------------------------- bricks
    case 'break': {
      // melodic pluck on the pentatonic + a tiny transient tick
      const f = pent(step, 2) * (1 + I * 0.0);
      eng.tone({ freq: f, type: 'square', dur: 0.1 + I * 0.04, gain: 0.16, when: W, pan, attack: 0.002, filter: 'lowpass', filterQ: 2, filterTo: f * 5 });
      eng.tone({ freq: f * 2, type: 'triangle', dur: 0.07, gain: 0.07 * bright, when: W, pan });
      eng.noise({ dur: 0.05, gain: 0.06, freq: 3200 * bright, sweep: 900, q: 0.8, when: W, pan });
      break;
    }
    case 'breakGlass':                     // shatter: noise burst + high sine ping
      eng.noise({ dur: 0.3, gain: 0.15, freq: 6200, sweep: 2400, q: 1.6, when: W, pan });
      eng.noise({ dur: 0.12, gain: 0.1, freq: 9000, q: 0.6, type: 'highpass', when: W, pan, delay: 0.02 });
      eng.tone({ freq: pent(step + 3, 4), type: 'sine', dur: 0.22, gain: 0.09, when: W, pan });
      eng.tone({ freq: pent(step + 5, 4), type: 'sine', dur: 0.16, gain: 0.06, when: W, pan, delay: 0.045 });
      break;
    case 'breakArmor':                     // metallic FM clank + low body
      eng.fm({ freq: 420, ratio: 3.7, index: 1400, indexEnd: 40, dur: 0.28, gain: 0.15, when: W, pan });
      eng.fm({ freq: 210, ratio: 5.1, index: 900, indexEnd: 20, dur: 0.18, gain: 0.09, when: W, pan, delay: 0.01 });
      eng.tone({ freq: 150, freq2: 62, type: 'sawtooth', dur: 0.2, gain: 0.16, when: W, pan });
      break;
    case 'breakPrism':                     // bright ping, quantized
      eng.fm({ freq: pent(step + 2, 3), ratio: 2.01, index: 500, indexEnd: 8, dur: 0.3, gain: 0.11, when: W, pan });
      eng.tone({ freq: pent(step + 4, 4), type: 'sine', dur: 0.24, gain: 0.06, when: W, pan, delay: 0.03 });
      break;
    case 'breakGhost':                     // airy: soft noise swell + breathy sine
      eng.noise({ dur: 0.34, gain: 0.07, freq: 1400, sweep: 5200, q: 0.7, when: W, pan, attack: 0.09 });
      eng.tone({ freq: pent(step, 3), freq2: pent(step + 3, 3), type: 'sine', dur: 0.34, gain: 0.09, when: W, pan, attack: 0.05 });
      break;
    case 'breakCap':                       // descending blip (the reward brick)
      eng.tone({ freq: pent(step + 5, 3), freq2: pent(step, 2), type: 'square', dur: 0.18, gain: 0.13, when: W, pan, glideAt: 0.8, filter: 'lowpass', filterQ: 2, filterTo: 2400 });
      eng.tone({ freq: pent(step + 7, 4), type: 'triangle', dur: 0.12, gain: 0.07, when: W, pan, delay: 0.02 });
      break;
    case 'boom':                           // bomb: sub thump + explosion noise
      eng.tone({ freq: 130, freq2: 30, type: 'sine', dur: 0.55, gain: 0.5, when: W, glideAt: 0.5 });
      eng.tone({ freq: 90, freq2: 26, type: 'sawtooth', dur: 0.4, gain: 0.18, when: W });
      eng.noise({ dur: 0.6, gain: 0.28, freq: 1200, sweep: 70, q: 0.6, type: 'lowpass', when: W });
      break;
    case 'boost':                          // launch pad: whoosh riser
      eng.noise({ dur: 0.4, gain: 0.13, freq: 300, sweep: 7000, q: 0.9, when: W, pan, attack: 0.1 });
      eng.tone({ freq: 180, freq2: 2000, type: 'sawtooth', dur: 0.36, gain: 0.13, when: W, pan, glideAt: 0.95, filter: 'lowpass', filterQ: 5, filterTo: 6000 });
      break;
    case 'steel':                          // dull thud
      eng.noise({ dur: 0.09, gain: 0.11, freq: 420, q: 1.1, type: 'lowpass', when: W, pan });
      eng.tone({ freq: 120, freq2: 82, type: 'sine', dur: 0.08, gain: 0.13, when: W, pan });
      break;
    case 'clank':                          // armor refusal
      eng.fm({ freq: 300, ratio: 4.3, index: 800, indexEnd: 30, dur: 0.12, gain: 0.11, when: W, pan });
      eng.tone({ freq: 140, freq2: 96, type: 'square', dur: 0.08, gain: 0.08, when: W, pan });
      break;
    case 'ping':                           // prism / cap refusal
      eng.tone({ freq: 1500 * bright, freq2: 2200 * bright, type: 'sine', dur: 0.09, gain: 0.08, when: W, pan });
      break;
    case 'ring':                           // glass refusal — it just rings
      eng.fm({ freq: 2400, ratio: 1.41, index: 260, indexEnd: 4, dur: 0.42, gain: 0.06, when: W, pan });
      break;

    // ---------------------------------------------------------------- paddle & walls
    case 'paddle': {                       // soft thock
      const f = 190 + (o.pitch ?? 0) * 90;
      eng.tone({ freq: f, freq2: f * 0.55, type: 'sine', dur: 0.1, gain: 0.22, when: W, pan, glideAt: 0.4 });
      eng.noise({ dur: 0.045, gain: 0.05, freq: 900, sweep: 300, q: 0.9, type: 'lowpass', when: W, pan });
      break;
    }
    case 'smash': {                        // punchy, pitch bends up
      const f = 200 + (o.pitch ?? 0) * 80;
      eng.tone({ freq: f, freq2: f * 3.4, type: 'sawtooth', dur: 0.16, gain: 0.2, when: W, pan, glideAt: 0.7, filter: 'lowpass', filterQ: 6, filterTo: 5000 });
      eng.tone({ freq: f * 0.5, freq2: f * 1.5, type: 'sine', dur: 0.2, gain: 0.22, when: W, pan });
      eng.noise({ dur: 0.14, gain: 0.1 * bright, freq: 1400, sweep: 5200, q: 0.8, when: W, pan });
      break;
    }
    case 'wall':
      eng.tone({ freq: 320, freq2: 210, type: 'triangle', dur: 0.055, gain: 0.09, when: W, pan });
      break;
    case 'ceiling': {                      // combo climbs the pentatonic
      const n = clamp(Math.round(o.combo ?? 1), 1, 12);
      eng.tone({ freq: pent(n, 2), type: 'triangle', dur: 0.13, gain: 0.13, when: W, pan });
      eng.tone({ freq: pent(n + 2, 3), type: 'sine', dur: 0.1, gain: 0.07 * bright, when: W, pan, delay: 0.03 });
      eng.noise({ dur: 0.05, gain: 0.04, freq: 6000, q: 0.7, type: 'highpass', when: W, pan });
      break;
    }

    // ---------------------------------------------------------------- flow / UI
    case 'launch':
      eng.tone({ freq: 300, freq2: 900, type: 'square', dur: 0.14, gain: 0.13, when: W, glideAt: 0.7, filter: 'lowpass', filterQ: 3, filterTo: 3000 });
      break;
    case 'laser':                          // zap
      eng.tone({ freq: 2400, freq2: 300, type: 'sawtooth', dur: 0.1, gain: 0.11, when: W, pan, glideAt: 0.8 });
      eng.noise({ dur: 0.07, gain: 0.05, freq: 5000, sweep: 900, q: 1.6, when: W, pan });
      break;
    case 'powerup':                        // sparkle
      for (let i = 0; i < 5; i++) {
        eng.tone({ freq: pent(i + 3, 4), type: 'triangle', dur: 0.16, gain: 0.075, when: W, delay: i * 0.045 });
        eng.tone({ freq: pent(i + 5, 4), type: 'sine', dur: 0.1, gain: 0.04, when: W, delay: i * 0.045 + 0.02 });
      }
      break;
    case 'multiball': {                    // three stabs
      const c = music ? music.chord : { root: 45, third: 3 };
      for (let i = 0; i < 3; i++) {
        const base = pent(i * 2, 3);
        eng.tone({ freq: base, type: 'sawtooth', dur: 0.16, gain: 0.12, when: W, delay: i * 0.075, detune: 6, filter: 'lowpass', filterQ: 4, filterTo: 4500 });
        eng.tone({ freq: base * 1.5, type: 'square', dur: 0.12, gain: 0.06, when: W, delay: i * 0.075 });
      }
      void c;
      break;
    }
    case 'multiplier': {                   // bell that climbs with the multiplier
      const lvl = clamp(Math.round(o.level ?? 1), 1, 10);
      eng.fm({ freq: pent(lvl, 3), ratio: 2.0, index: 300, indexEnd: 6, dur: 0.35, gain: 0.1, when: W });
      eng.tone({ freq: pent(lvl + 2, 4), type: 'sine', dur: 0.22, gain: 0.06, when: W, delay: 0.06 });
      break;
    }
    case 'overtop':                        // the moment — music.hit() adds the riser/stab
      for (let i = 0; i < 6; i++) {
        eng.tone({ freq: pent(i, 3), type: 'triangle', dur: 0.3, gain: 0.1, when: W, delay: i * 0.05 });
      }
      eng.noise({ dur: 0.6, gain: 0.07, freq: 800, sweep: 11000, q: 0.6, when: W, attack: 0.25 });
      break;
    case 'overtopEnd':
      eng.tone({ freq: pent(4, 3), freq2: pent(0, 2), type: 'triangle', dur: 0.3, gain: 0.08, when: W, glideAt: 0.8 });
      break;
    case 'lose':                           // descending; the facade also ducks the music
      eng.tone({ freq: 420, freq2: 70, type: 'sawtooth', dur: 0.7, gain: 0.18, when: W, glideAt: 0.85, filter: 'lowpass', filterQ: 4, filterTo: 400 });
      eng.tone({ freq: 210, freq2: 46, type: 'square', dur: 0.6, gain: 0.09, when: W, glideAt: 0.85 });
      eng.noise({ dur: 0.6, gain: 0.07, freq: 1400, sweep: 120, q: 0.7, when: W });
      break;
    case 'levelup':                        // arpeggiated flourish in key
      for (let i = 0; i < 8; i++) {
        eng.tone({ freq: pent(i, 3), type: 'triangle', dur: 0.3, gain: 0.1, when: W, delay: i * 0.075 });
        if (i % 2 === 0) eng.tone({ freq: pent(i + 5, 4), type: 'square', dur: 0.2, gain: 0.05, when: W, delay: i * 0.075 + 0.03 });
      }
      break;
    case 'gameover':
      [0, -2, -5, -9].forEach((s, i) => {
        eng.tone({ freq: 440 * Math.pow(2, s / 12), type: 'sawtooth', dur: 0.55, gain: 0.12, when: W, delay: i * 0.18, filter: 'lowpass', filterQ: 3, filterTo: 700 });
        eng.tone({ freq: 220 * Math.pow(2, s / 12), type: 'triangle', dur: 0.5, gain: 0.07, when: W, delay: i * 0.18 });
      });
      break;
    case 'ui':
      eng.tone({ freq: 760, freq2: 1140, type: 'square', dur: 0.07, gain: 0.08, when: W, glideAt: 0.6 });
      break;
    default:
      break;
  }
}

export default playSfx;
