// audio.js — WebAudio synth SFX + adaptive 2-layer music. Init on first gesture. Mute persisted.
'use strict';

const AudioSys = (() => {
  let ctx = null, master = null, musicGain = null, sfxGain = null;
  let muted = localStorage.getItem('overkick_mute') === '1';
  let musicOn = false, musicTimer = null, step16 = 0, nextNoteTime = 0;
  let intensity = 0; // 0..1, driven by heat stage

  function init() {
    if (ctx) return;
    const AC = window.AudioContext || window.webkitAudioContext;
    if (!AC) return;
    ctx = new AC();
    master = ctx.createGain();
    master.gain.value = muted ? 0 : 1;
    master.connect(ctx.destination);
    sfxGain = ctx.createGain(); sfxGain.gain.value = 0.5; sfxGain.connect(master);
    musicGain = ctx.createGain(); musicGain.gain.value = 0.35; musicGain.connect(master);
  }
  function resume() { init(); if (ctx && ctx.state === 'suspended') ctx.resume(); }

  function setMuted(m) {
    muted = m; localStorage.setItem('overkick_mute', m ? '1' : '0');
    if (master) master.gain.setTargetAtTime(m ? 0 : 1, ctx.currentTime, 0.02);
  }
  const toggleMute = () => { setMuted(!muted); return muted; };

  // --- low-level voices ---
  function tone(freq, dur, type = 'sine', gain = 0.3, when = 0, glideTo = null) {
    if (!ctx || muted) return;
    const t0 = ctx.currentTime + when;
    const o = ctx.createOscillator(), g = ctx.createGain();
    o.type = type; o.frequency.setValueAtTime(freq, t0);
    if (glideTo) o.frequency.exponentialRampToValueAtTime(Math.max(20, glideTo), t0 + dur);
    g.gain.setValueAtTime(gain, t0);
    g.gain.exponentialRampToValueAtTime(0.001, t0 + dur);
    o.connect(g); g.connect(sfxGain);
    o.start(t0); o.stop(t0 + dur + 0.02);
  }
  function noise(dur, gain = 0.4, filterFreq = 1200, when = 0, sweepTo = null) {
    if (!ctx || muted) return;
    const t0 = ctx.currentTime + when;
    const len = Math.max(1, (dur * ctx.sampleRate) | 0);
    const buf = ctx.createBuffer(1, len, ctx.sampleRate);
    const d = buf.getChannelData(0);
    for (let i = 0; i < len; i++) d[i] = Math.random() * 2 - 1;
    const src = ctx.createBufferSource(); src.buffer = buf;
    const f = ctx.createBiquadFilter(); f.type = 'lowpass';
    f.frequency.setValueAtTime(filterFreq, t0);
    if (sweepTo) f.frequency.exponentialRampToValueAtTime(Math.max(40, sweepTo), t0 + dur);
    const g = ctx.createGain();
    g.gain.setValueAtTime(gain, t0);
    g.gain.exponentialRampToValueAtTime(0.001, t0 + dur);
    src.connect(f); f.connect(g); g.connect(sfxGain);
    src.start(t0); src.stop(t0 + dur + 0.02);
  }

  // --- SFX (pitched by combo where noted) ---
  const sfx = {
    paddle(rally = 0) { tone(220 + Math.min(rally, 16) * 22, 0.09, 'square', 0.22); tone(440 + rally * 30, 0.06, 'sine', 0.12); },
    clink() { tone(1900, 0.05, 'triangle', 0.25); tone(2450, 0.05, 'triangle', 0.15, 0.01); noise(0.05, 0.12, 6000); },
    clank() { tone(140, 0.1, 'square', 0.3); noise(0.07, 0.2, 900); },
    break_(combo = 0) {
      const base = 330 * Math.pow(2, Math.min(combo, 12) / 12);
      tone(base, 0.12, 'triangle', 0.3); tone(base * 1.5, 0.12, 'sine', 0.18, 0.01); noise(0.09, 0.22, 3500);
    },
    punch() { noise(0.18, 0.35, 600, 0, 3000); tone(90, 0.15, 'sine', 0.4, 0, 45); },
    explosion() { noise(0.5, 0.55, 900, 0, 80); tone(70, 0.4, 'sine', 0.45, 0, 35); },
    capsule() { [523, 659, 784, 1046].forEach((f, i) => tone(f, 0.1, 'sine', 0.2, i * 0.05)); },
    loseBall() { tone(400, 0.6, 'sawtooth', 0.3, 0, 60); },
    ott() {
      tone(523, 0.5, 'sawtooth', 0.25, 0, 1046);
      tone(784, 0.6, 'triangle', 0.25, 0.08, 1568);
      noise(0.7, 0.3, 800, 0, 8000);
    },
    levelClear() { [523, 659, 784, 1046, 1318].forEach((f, i) => tone(f, 0.22, 'triangle', 0.25, i * 0.09)); },
    gameOver() { [392, 330, 262, 196].forEach((f, i) => tone(f, 0.4, 'sawtooth', 0.22, i * 0.18)); },
    ui() { tone(880, 0.05, 'square', 0.15); },
    phaseTick() { tone(1500, 0.03, 'sine', 0.08); },
  };

  // --- adaptive 2-layer music: bass pulse + arpeggio, scheduled ahead ---
  function startMusic() {
    resume(); if (!ctx || musicOn) return;
    musicOn = true; step16 = 0; nextNoteTime = ctx.currentTime + 0.05;
    musicTimer = setInterval(schedule, 60);
  }
  function stopMusic() { musicOn = false; if (musicTimer) clearInterval(musicTimer); musicTimer = null; }
  function setIntensity(v) { intensity = Util.clamp(v, 0, 1); }

  function schedule() {
    if (!ctx || !musicOn || muted) return;
    const bpm = 112 + intensity * 40;
    const spb = 60 / bpm / 4; // 16th note
    while (nextNoteTime < ctx.currentTime + 0.25) {
      playStep(step16, nextNoteTime, spb);
      nextNoteTime += spb;
      step16 = (step16 + 1) % 32;
    }
  }
  function mnote(freq, t0, dur, type, gain) {
    const o = ctx.createOscillator(), g = ctx.createGain();
    o.type = type; o.frequency.setValueAtTime(freq, t0);
    g.gain.setValueAtTime(0.001, t0);
    g.gain.linearRampToValueAtTime(gain, t0 + 0.01);
    g.gain.exponentialRampToValueAtTime(0.001, t0 + dur);
    o.connect(g); g.connect(musicGain);
    o.start(t0); o.stop(t0 + dur + 0.02);
  }
  const BASS = [55, 55, 55, 55, 65.4, 65.4, 49, 49];
  const ARP = [220, 262, 330, 262, 220, 330, 440, 330];
  function playStep(i, t0, spb) {
    const bar = (i / 4) | 0;
    if (i % 4 === 0) mnote(BASS[bar % 8] * 2, t0, spb * 3.6, 'square', 0.18 + intensity * 0.12);
    // arp layer fades in with intensity
    if (intensity > 0.05) {
      const g = Math.min(1, intensity * 2) * 0.14;
      mnote(ARP[i % 8] * (intensity > 0.5 ? 2 : 1), t0, spb * 1.6, 'sawtooth', g);
    }
    // hi-hat at high intensity
    if (intensity > 0.6 && i % 2 === 1) {
      const g = ctx.createGain(), src = ctx.createBufferSource();
      src.buffer = noiseBuf(); const f = ctx.createBiquadFilter(); f.type = 'highpass'; f.frequency.value = 7000;
      g.gain.setValueAtTime(0.06, t0); g.gain.exponentialRampToValueAtTime(0.001, t0 + 0.04);
      src.connect(f); f.connect(g); g.connect(musicGain); src.start(t0); src.stop(t0 + 0.06);
    }
  }
  let _nb = null;
  function noiseBuf() {
    if (_nb) return _nb;
    _nb = ctx.createBuffer(1, ctx.sampleRate * 0.1, ctx.sampleRate);
    const d = _nb.getChannelData(0);
    for (let i = 0; i < d.length; i++) d[i] = Math.random() * 2 - 1;
    return _nb;
  }

  return { init, resume, sfx, setMuted, toggleMute, get muted() { return muted; }, startMusic, stopMusic, setIntensity };
})();