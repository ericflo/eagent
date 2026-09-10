/* audio.js — synthesized WebAudio SFX + adaptive music, no assets. */
'use strict';
const AudioSys = (() => {
  let ctx = null, master = null, sfxBus = null, musicBus = null;
  let musicOn = false, musicTimer = null, step = 0, intensity = 0;
  let muted = false;

  function ensure() {
    if (ctx) { if (ctx.state === 'suspended') ctx.resume(); return true; }
    try {
      const AC = window.AudioContext || window.webkitAudioContext;
      if (!AC) return false;
      ctx = new AC();
      master = ctx.createGain(); master.gain.value = 0.8; master.connect(ctx.destination);
      sfxBus = ctx.createGain(); sfxBus.gain.value = 0.9; sfxBus.connect(master);
      musicBus = ctx.createGain(); musicBus.gain.value = 0.34; musicBus.connect(master);
    } catch (e) { return false; }
    return true;
  }
  function unlock() { ensure(); }
  function now() { return ctx ? ctx.currentTime : 0; }

  function tone({ freq = 440, freq2 = null, type = 'square', dur = 0.12, vol = 0.3, when = 0, slideT = null, bus = null }) {
    if (!ensure() || muted) return;
    const t = now() + when;
    const o = ctx.createOscillator(), g = ctx.createGain();
    o.type = type; o.frequency.setValueAtTime(freq, t);
    if (freq2 !== null) o.frequency.exponentialRampToValueAtTime(Math.max(20, freq2), t + (slideT || dur));
    g.gain.setValueAtTime(0.0001, t);
    g.gain.exponentialRampToValueAtTime(vol, t + 0.008);
    g.gain.exponentialRampToValueAtTime(0.0001, t + dur);
    o.connect(g); g.connect(bus || sfxBus);
    o.start(t); o.stop(t + dur + 0.05);
  }
  function noise({ dur = 0.2, vol = 0.25, when = 0, hp = null, lp = null }) {
    if (!ensure() || muted) return;
    const t = now() + when;
    const len = Math.max(1, (dur * ctx.sampleRate) | 0);
    const buf = ctx.createBuffer(1, len, ctx.sampleRate);
    const d = buf.getChannelData(0);
    for (let i = 0; i < len; i++) d[i] = (Math.random() * 2 - 1) * (1 - i / len);
    const src = ctx.createBufferSource(); src.buffer = buf;
    let node = src;
    if (hp) { const f = ctx.createBiquadFilter(); f.type = 'highpass'; f.frequency.value = hp; node.connect(f); node = f; }
    if (lp) { const f = ctx.createBiquadFilter(); f.type = 'lowpass'; f.frequency.value = lp; node.connect(f); node = f; }
    const g = ctx.createGain(); g.gain.value = vol;
    node.connect(g); g.connect(sfxBus); src.start(t);
  }

  // ---- SFX (pitch scales with multiplier m) ----
  function paddle(m = 1) { tone({ freq: 220 * (1 + 0.08 * Math.min(m, 16)), freq2: 440, type: 'square', dur: 0.09, vol: 0.22 }); }
  function wall(m = 1) { tone({ freq: 180 + 20 * Math.min(m, 10), type: 'triangle', dur: 0.07, vol: 0.18 }); }
  function brick(m = 1, combo = 0) {
    const base = 340 * Math.pow(2, Math.min(combo, 24) / 24);
    tone({ freq: base, freq2: base * 1.5, type: 'square', dur: 0.1, vol: 0.25 });
    noise({ dur: 0.08, vol: 0.12, hp: 2000 });
  }
  function brickDenied() { tone({ freq: 160, freq2: 90, type: 'sawtooth', dur: 0.16, vol: 0.22 }); }
  function powerup() {
    tone({ freq: 523, type: 'square', dur: 0.09, vol: 0.22 });
    tone({ freq: 659, type: 'square', dur: 0.09, vol: 0.22, when: 0.08 });
    tone({ freq: 880, type: 'square', dur: 0.16, vol: 0.24, when: 0.16 });
  }
  function launch() { tone({ freq: 300, freq2: 700, type: 'sawtooth', dur: 0.18, vol: 0.2 }); }
  function loseLife() {
    tone({ freq: 400, freq2: 80, type: 'sawtooth', dur: 0.5, vol: 0.3 });
    noise({ dur: 0.3, vol: 0.15, lp: 1200 });
  }
  function frenzyOn() {
    tone({ freq: 200, freq2: 1200, type: 'sawtooth', dur: 0.5, vol: 0.28 });
    tone({ freq: 1200, freq2: 2400, type: 'square', dur: 0.3, vol: 0.14, when: 0.15 });
  }
  function levelClear() { [523, 659, 784, 1047, 1319].forEach((f, i) => tone({ freq: f, type: 'square', dur: 0.22, vol: 0.22, when: i * 0.11 })); }
  function gameOver() { [392, 330, 262, 196].forEach((f, i) => tone({ freq: f, freq2: f * 0.94, type: 'sawtooth', dur: 0.3, vol: 0.24, when: i * 0.22 })); }
  function laser() { tone({ freq: 1400, freq2: 300, type: 'sawtooth', dur: 0.14, vol: 0.2 }); }
  function smash() { noise({ dur: 0.25, vol: 0.3, lp: 3000 }); tone({ freq: 120, freq2: 40, type: 'sine', dur: 0.3, vol: 0.4 }); }

  // ---- Adaptive music: 8-step bass + arp, rate/brightness scale with intensity ----
  const BASS = [55, 55, 65.4, 49, 55, 55, 73.4, 65.4]; // A1 C2 G1 pattern
  const ARP = [220, 261.6, 329.6, 440, 329.6, 261.6];
  function scheduleStep() {
    if (!ctx || muted) return;
    const t = now();
    const b = BASS[step % BASS.length];
    const o = ctx.createOscillator(), g = ctx.createGain(), f = ctx.createBiquadFilter();
    o.type = 'sawtooth'; o.frequency.value = b;
    f.type = 'lowpass'; f.frequency.value = 300 + intensity * 1600;
    g.gain.setValueAtTime(0.0001, t);
    g.gain.exponentialRampToValueAtTime(0.5, t + 0.02);
    g.gain.exponentialRampToValueAtTime(0.0001, t + 0.22);
    o.connect(f); f.connect(g); g.connect(musicBus); o.start(t); o.stop(t + 0.3);
    if (intensity > 0.25) {
      const a = ARP[(step * 2) % ARP.length] * (1 + intensity * 0.5);
      const o2 = ctx.createOscillator(), g2 = ctx.createGain();
      o2.type = 'square'; o2.frequency.value = a;
      g2.gain.setValueAtTime(0.0001, t + 0.05);
      g2.gain.exponentialRampToValueAtTime(0.12 + intensity * 0.1, t + 0.07);
      g2.gain.exponentialRampToValueAtTime(0.0001, t + 0.18);
      o2.connect(g2); g2.connect(musicBus); o2.start(t + 0.05); o2.stop(t + 0.25);
    }
    if (intensity > 0.6) { // hats
      const len = (0.05 * ctx.sampleRate) | 0;
      const buf = ctx.createBuffer(1, len, ctx.sampleRate);
      const d = buf.getChannelData(0);
      for (let i = 0; i < len; i++) d[i] = (Math.random() * 2 - 1) * (1 - i / len);
      const s = ctx.createBufferSource(); s.buffer = buf;
      const hf = ctx.createBiquadFilter(); hf.type = 'highpass'; hf.frequency.value = 6000;
      const hg = ctx.createGain(); hg.gain.value = 0.15;
      s.connect(hf); hf.connect(hg); hg.connect(musicBus); s.start(t + 0.12);
    }
    step++;
  }
  function startMusic() {
    if (!ensure() || musicOn) return; musicOn = true; step = 0;
    const tick = () => {
      if (!musicOn) return;
      scheduleStep();
      const interval = Math.max(110, 240 - intensity * 130);
      musicTimer = setTimeout(tick, interval);
    };
    tick();
  }
  function stopMusic() { musicOn = false; if (musicTimer) clearTimeout(musicTimer); musicTimer = null; }
  function setIntensity(v) { intensity = Math.max(0, Math.min(1, v)); }

  return { unlock, ensure, startMusic, stopMusic, setIntensity, paddle, wall, brick, brickDenied, powerup, launch, loseLife, frenzyOn, levelClear, gameOver, laser, smash };
})();
