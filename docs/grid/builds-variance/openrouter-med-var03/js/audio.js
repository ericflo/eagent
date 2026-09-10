/* ============================================================
   Overdrive Breakout — audio.js
   Fully synthesized Web Audio API sound. No assets.
   Public API (window.AudioSys):
     unlock(), setMuted(m), toggleMute()
     bounce(combo), brickBreak(combo, kind), clang(), thud(),
     whoosh(), powerup(), jingleLoss(), chord(tier), overdriveStart(),
     setMusicIntensity(0..1), tick(dt)
   ============================================================ */
(function () {
  'use strict';

  let ctx = null;         // AudioContext
  let master = null;      // master gain
  let sfxBus = null, musBus = null;
  let muted = false;
  let unlocked = false;

  // ---- music intensity layers -------------------------------------------
  let musIntensity = 0;           // target 0..1
  let musLevel = 0;               // smoothed actual
  let bassTimer = null, arpTimer = null, pulseTimer = null;
  let arpStep = 0, bassStep = 0;

  function ensure() {
    if (ctx) return true;
    try {
      const AC = window.AudioContext || window.webkitAudioContext;
      ctx = new AC();
      master = ctx.createGain();
      master.gain.value = muted ? 0 : 0.9;
      // gentle limiter-ish compressor so layered juice never clips hard
      const comp = ctx.createDynamicsCompressor();
      comp.threshold.value = -14; comp.ratio.value = 6;
      master.connect(comp); comp.connect(ctx.destination);
      sfxBus = ctx.createGain(); sfxBus.gain.value = 1; sfxBus.connect(master);
      musBus = ctx.createGain(); musBus.gain.value = 0.7; musBus.connect(master);
      return true;
    } catch (e) { return false; }
  }

  function unlock() {
    if (!ensure()) return;
    if (ctx.state === 'suspended') ctx.resume();
    unlocked = true;
  }

  // ---- tiny synth helpers -------------------------------------------------
  function tone(freq, dur, type, vol, dest, when, glideTo) {
    if (!ctx || muted) return;
    const t = when !== undefined ? when : ctx.currentTime;
    const o = ctx.createOscillator();
    const g = ctx.createGain();
    o.type = type || 'sine';
    o.frequency.setValueAtTime(freq, t);
    if (glideTo) o.frequency.exponentialRampToValueAtTime(Math.max(20, glideTo), t + dur);
    g.gain.setValueAtTime(0.0001, t);
    g.gain.exponentialRampToValueAtTime(vol, t + 0.008);
    g.gain.exponentialRampToValueAtTime(0.0001, t + dur);
    o.connect(g); g.connect(dest || sfxBus);
    o.start(t); o.stop(t + dur + 0.05);
  }

  function noise(dur, vol, filterFreq, dest, when, sweepTo) {
    if (!ctx || muted) return;
    const t = when !== undefined ? when : ctx.currentTime;
    const len = Math.max(1, Math.floor(ctx.sampleRate * dur));
    const buf = ctx.createBuffer(1, len, ctx.sampleRate);
    const d = buf.getChannelData(0);
    for (let i = 0; i < len; i++) d[i] = (Math.random() * 2 - 1) * (1 - i / len);
    const src = ctx.createBufferSource(); src.buffer = buf;
    const f = ctx.createBiquadFilter();
    f.type = 'bandpass'; f.frequency.setValueAtTime(filterFreq, t);
    if (sweepTo) f.frequency.exponentialRampToValueAtTime(sweepTo, t + dur);
    f.Q.value = 1.2;
    const g = ctx.createGain();
    g.gain.setValueAtTime(vol, t);
    g.gain.exponentialRampToValueAtTime(0.0001, t + dur);
    src.connect(f); f.connect(g); g.connect(dest || sfxBus);
    src.start(t); src.stop(t + dur + 0.02);
  }

  // musical scale for rising pitch effects (minor pentatonic, C3 base)
  const SCALE = [0, 3, 5, 7, 10, 12, 15, 17, 19, 22, 24, 27, 29, 31, 34, 36];
  function scaleHz(step) {
    const s = SCALE[Math.min(SCALE.length - 1, Math.max(0, step | 0))];
    return 130.81 * Math.pow(2, s / 12);
  }

  // ---- SFX ---------------------------------------------------------------
  const api = {};

  api.unlock = unlock;

  api.setMuted = function (m) {
    muted = !!m;
    if (master) master.gain.setTargetAtTime(muted ? 0 : 0.9, ctx.currentTime, 0.02);
    try { localStorage.setItem('od_muted', muted ? '1' : '0'); } catch (e) {}
  };
  api.toggleMuted = function () { api.setMuted(!muted); return muted; };
  api.muted = function () { return muted; };

  api.bounce = function (combo) {
    if (!ctx || muted) return;
    const hz = scaleHz(Math.min(14, 2 + (combo | 0)));
    tone(hz, 0.07, 'triangle', 0.18);
    noise(0.03, 0.06, 1800);
  };

  api.brickBreak = function (combo, kind) {
    if (!ctx || muted) return;
    const hz = scaleHz(Math.min(15, 3 + (combo | 0)));
    tone(hz, 0.16, 'square', 0.14, sfxBus, ctx.currentTime, hz * 0.5);
    noise(0.14, 0.2, 2400, sfxBus, ctx.currentTime, 500);
    if (kind === 'paydirt') { tone(hz * 2, 0.3, 'sine', 0.2, sfxBus, ctx.currentTime + 0.05); }
  };

  api.clang = function () { // wrong-angle hit
    if (!ctx || muted) return;
    tone(210, 0.24, 'square', 0.16, sfxBus, ctx.currentTime, 140);
    tone(317, 0.2, 'square', 0.1);
    noise(0.08, 0.12, 900);
  };

  api.thud = function () { // speed-brick slow bounce / heavy thump
    if (!ctx || muted) return;
    tone(90, 0.22, 'sine', 0.3, sfxBus, ctx.currentTime, 45);
    noise(0.1, 0.1, 300);
  };

  api.reflect = function () { // armored ping
    if (!ctx || muted) return;
    tone(520, 0.12, 'sine', 0.14, sfxBus, ctx.currentTime, 700);
    noise(0.05, 0.08, 3000);
  };

  api.whoosh = function () { // paddle smash
    if (!ctx || muted) return;
    noise(0.3, 0.25, 400, sfxBus, ctx.currentTime, 4000);
    tone(180, 0.18, 'sawtooth', 0.1, sfxBus, ctx.currentTime, 520);
  };

  api.powerup = function () {
    if (!ctx || muted) return;
    const t = ctx.currentTime;
    [0, 4, 7, 12].forEach((s, i) =>
      tone(440 * Math.pow(2, s / 12), 0.18, 'triangle', 0.16, sfxBus, t + i * 0.07));
  };

  api.jingleLoss = function () {
    if (!ctx || muted) return;
    const t = ctx.currentTime;
    tone(220, 0.5, 'sawtooth', 0.2, sfxBus, t, 60);
    noise(0.4, 0.15, 500, sfxBus, t, 120);
  };

  api.chord = function (tier) { // overdrive tier-up swell
    if (!ctx || muted) return;
    const t = ctx.currentTime;
    const base = 220 * Math.pow(2, Math.min(tier, 4) / 12);
    [1, 1.5, 2, 3].forEach((r, i) => {
      tone(base * r, 1.1, 'sawtooth', 0.08, sfxBus, t + i * 0.03);
      tone(base * r, 1.1, 'sine', 0.1, sfxBus, t + i * 0.03);
    });
    noise(0.8, 0.12, 2000, sfxBus, t, 6000);
  };

  api.overdriveStart = function () {
    if (!ctx || muted) return;
    const t = ctx.currentTime;
    [0, 7, 12].forEach((s, i) =>
      tone(330 * Math.pow(2, s / 12), 0.4, 'square', 0.1, sfxBus, t + i * 0.06));
    noise(0.5, 0.1, 800, sfxBus, t, 5000);
  };

  api.launch = function () {
    if (!ctx || muted) return;
    tone(300, 0.15, 'triangle', 0.16, sfxBus, ctx.currentTime, 700);
  };

  // ---- adaptive overdrive music -------------------------------------------
  function startBass() {
    if (bassTimer) return;
    bassTimer = setInterval(() => {
      if (!ctx || muted) return;
      const t = ctx.currentTime;
      const seq = [0, 0, 5, 0, 3, 3, 7, 5];
      const f = 55 * Math.pow(2, seq[bassStep++ % 8] / 12);
      tone(f, 0.22, 'sawtooth', 0.16 + musLevel * 0.1, musBus, t);
      noise(0.06, 0.05 * musLevel, 150, musBus, t);
    }, 500 - Math.round(musLevel * 120));
  }

  function startArp() {
    if (arpTimer) return;
    arpTimer = setInterval(() => {
      if (!ctx || muted) return;
      const t = ctx.currentTime;
      const seq = [12, 15, 19, 22, 24, 22, 19, 15];
      const f = 220 * Math.pow(2, seq[arpStep++ % 8] / 12);
      tone(f, 0.12, 'square', 0.05 + musLevel * 0.08, musBus, t);
    }, 250 - Math.round(musLevel * 100));
  }

  function startPulse() { // high-tier shimmer
    if (pulseTimer) return;
    pulseTimer = setInterval(() => {
      if (!ctx || muted) return;
      noise(0.25, 0.05 * musLevel, 6000, musBus, ctx.currentTime, 12000);
    }, 200);
  }

  api.setMusicIntensity = function (v) {
    musIntensity = Math.max(0, Math.min(1, v));
    if (!ctx || !unlocked) return;
    if (musIntensity > 0.05) {
      startBass();
      if (musIntensity > 0.3) startArp();
      if (musIntensity > 0.7) startPulse();
    }
  };

  api.tick = function () {
    musLevel += (musIntensity - musLevel) * 0.05;
    if (musIntensity <= 0.05 && musLevel < 0.03) {
      if (bassTimer) { clearInterval(bassTimer); bassTimer = null; }
      if (arpTimer) { clearInterval(arpTimer); arpTimer = null; }
      if (pulseTimer) { clearInterval(pulseTimer); pulseTimer = null; }
      bassStep = arpStep = 0;
    }
  };

  // restore mute pref
  try { muted = localStorage.getItem('od_muted') === '1'; } catch (e) {}

  window.AudioSys = api;
})();