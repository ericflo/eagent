/* BreakoutAudio — all synthesized WebAudio SFX + procedural music. No files.
   Global: window.BreakoutAudio */
(function () {
  'use strict';
  var ctx = null, master = null, musicGain = null, sfxGain = null;
  var muted = false, musicOn = true, musicTimer = null, nextNoteTime = 0;
  var intensity = 0; // 0..1 set by game (multiplier/overdrive)
  var step = 0;

  function ensure() {
    if (ctx) { if (ctx.state === 'suspended') ctx.resume(); return true; }
    try {
      var AC = window.AudioContext || window.webkitAudioContext;
      if (!AC) return false;
      ctx = new AC();
      master = ctx.createGain(); master.gain.value = 0.9; master.connect(ctx.destination);
      sfxGain = ctx.createGain(); sfxGain.gain.value = 0.9; sfxGain.connect(master);
      musicGain = ctx.createGain(); musicGain.gain.value = 0.30; musicGain.connect(master);
      startMusic();
      return true;
    } catch (e) { return false; }
  }

  function now() { return ctx ? ctx.currentTime : 0; }
  function env(g, t, a, peak, d) {
    g.gain.setValueAtTime(0.0001, t);
    g.gain.exponentialRampToValueAtTime(Math.max(peak, 0.0002), t + a);
    g.gain.exponentialRampToValueAtTime(0.0001, t + a + d);
  }
  // Basic osc blip helper
  function blip(freq0, freq1, dur, type, vol, when) {
    if (!ctx || muted) return;
    var t = (when || now());
    var o = ctx.createOscillator(), g = ctx.createGain();
    o.type = type || 'square';
    o.frequency.setValueAtTime(freq0, t);
    o.frequency.exponentialRampToValueAtTime(Math.max(freq1, 1), t + dur);
    env(g, t, 0.005, vol || 0.25, dur);
    o.connect(g); g.connect(sfxGain);
    o.start(t); o.stop(t + dur + 0.05);
  }
  function noise(dur, vol, filterFreq, type, when) {
    if (!ctx || muted) return;
    var t = (when || now());
    var len = Math.max(1, Math.floor(ctx.sampleRate * dur));
    var buf = ctx.createBuffer(1, len, ctx.sampleRate);
    var d = buf.getChannelData(0);
    for (var i = 0; i < len; i++) d[i] = (Math.random() * 2 - 1) * (1 - i / len);
    var src = ctx.createBufferSource(); src.buffer = buf;
    var f = ctx.createBiquadFilter(); f.type = type || 'highpass'; f.frequency.value = filterFreq || 3000;
    var g = ctx.createGain(); env(g, t, 0.004, vol || 0.2, dur);
    src.connect(f); f.connect(g); g.connect(sfxGain);
    src.start(t);
  }

  // ---- SFX ----
  function paddleHit(speed) {
    if (!ctx || muted) return;
    var s = Math.min(1, (speed || 400) / 800);
    blip(240 + s * 260, 140, 0.09, 'square', 0.16 + s * 0.14);
    noise(0.05, 0.08, 4000, 'highpass');
  }
  function wallHit() { blip(300, 200, 0.06, 'square', 0.12); }
  function brickBreak(type) {
    if (!ctx || muted) return;
    var base = { normal: 520, steep: 660, flat: 600, velocity: 760, blink: 880, drifter: 560 }[type] || 520;
    blip(base, base * 1.8, 0.10, 'square', 0.22);
    blip(base * 1.5, base * 2.6, 0.14, 'sine', 0.18, now() + 0.03);
    noise(0.09, 0.14, 2500, 'bandpass');
  }
  function brickResist() { blip(180, 120, 0.14, 'sawtooth', 0.18); }
  function powerup() {
    if (!ctx || muted) return;
    var t = now();
    [440, 554, 659, 880].forEach(function (f, i) { blip(f, f, 0.12, 'square', 0.16, t + i * 0.06); });
  }
  function launch() { blip(300, 700, 0.16, 'sawtooth', 0.2); noise(0.08, 0.08, 2000, 'highpass'); }
  function loseLife() {
    if (!ctx || muted) return;
    var t = now();
    [400, 320, 240, 150].forEach(function (f, i) { blip(f, f * 0.9, 0.16, 'sawtooth', 0.2, t + i * 0.11); });
  }
  function gameOver() {
    if (!ctx || muted) return;
    var t = now();
    [330, 262, 196, 131, 98].forEach(function (f, i) { blip(f, f * 0.95, 0.3, 'triangle', 0.24, t + i * 0.2); });
  }
  function levelClear() {
    if (!ctx || muted) return;
    var t = now();
    [523, 659, 784, 1047, 1319].forEach(function (f, i) { blip(f, f, 0.22, 'square', 0.16, t + i * 0.09); });
  }
  function overdriveEnter() {
    if (!ctx || muted) return;
    var t = now();
    blip(150, 1200, 0.5, 'sawtooth', 0.22, t);
    noise(0.5, 0.10, 1200, 'bandpass', t);
    [660, 880, 1320].forEach(function (f, i) { blip(f, f, 0.2, 'square', 0.14, t + 0.15 + i * 0.08); });
  }
  function overdriveExit() { blip(800, 200, 0.4, 'sawtooth', 0.15); }
  function uiClick() { blip(700, 900, 0.06, 'sine', 0.15); }
  function powerHitSfx() {
    if (!ctx || muted) return;
    var t = now();
    blip(200, 900, 0.18, 'sawtooth', 0.28, t);
    noise(0.15, 0.16, 1800, 'bandpass', t);
  }

  // ---- Procedural music: bass + arp scheduled loop ----
  var bassLine = [55, 55, 65.4, 49];      // A1 A1 C2 G1
  var arpScale = [220, 261.6, 329.6, 440, 523.3, 659.3];
  function schedule() {
    if (!ctx || !musicOn || muted) { nextNoteTime = Math.max(nextNoteTime, now() + 0.1); return; }
    var tempo = 132 + intensity * 40;      // intensify with overdrive/multiplier
    var sixteenth = 60 / tempo / 2;        // 8th notes
    while (nextNoteTime < now() + 0.25) {
      var s = step % 16;
      // bass on quarters
      if (s % 4 === 0) {
        var bf = bassLine[(step / 4 | 0) % 4];
        mnote(bf, nextNoteTime, sixteenth * 3, 'sawtooth', 0.20);
        mnote(bf * 2, nextNoteTime, sixteenth * 2, 'triangle', 0.10);
      }
      // arp 8ths, denser + higher octave when intense
      if (s % 2 === 0 || intensity > 0.5) {
        var idx = (step * 5 + (step >> 2)) % arpScale.length;
        var af = arpScale[idx] * (intensity > 0.6 ? 2 : 1);
        mnote(af, nextNoteTime, sixteenth * 1.2, 'square', 0.05 + intensity * 0.05);
      }
      // hat tick
      mnoise(nextNoteTime, 0.03, 0.03 + intensity * 0.02);
      nextNoteTime += sixteenth;
      step++;
    }
  }
  function mnote(freq, t, dur, type, vol) {
    var o = ctx.createOscillator(), g = ctx.createGain(), f = ctx.createBiquadFilter();
    o.type = type; o.frequency.value = freq;
    f.type = 'lowpass'; f.frequency.value = 900 + intensity * 2500;
    env(g, t, 0.01, vol, dur);
    o.connect(f); f.connect(g); g.connect(musicGain);
    o.start(t); o.stop(t + dur + 0.05);
  }
  function mnoise(t, dur, vol) {
    var len = Math.max(1, Math.floor(ctx.sampleRate * dur));
    var buf = ctx.createBuffer(1, len, ctx.sampleRate);
    var d = buf.getChannelData(0);
    for (var i = 0; i < len; i++) d[i] = (Math.random() * 2 - 1);
    var src = ctx.createBufferSource(); src.buffer = buf;
    var f = ctx.createBiquadFilter(); f.type = 'highpass'; f.frequency.value = 7000;
    var g = ctx.createGain(); env(g, t, 0.002, vol, dur);
    src.connect(f); f.connect(g); g.connect(musicGain);
    src.start(t);
  }
  function startMusic() {
    if (musicTimer) return;
    nextNoteTime = ctx.currentTime + 0.1;
    musicTimer = setInterval(schedule, 80);
  }

  window.BreakoutAudio = {
    unlock: ensure,
    resume: ensure,
    setIntensity: function (v) { intensity = Math.max(0, Math.min(1, v)); },
    toggleMute: function () {
      muted = !muted;
      if (master) master.gain.value = muted ? 0 : 0.9;
      return muted;
    },
    isMuted: function () { return muted; },
    paddleHit: paddleHit, brickBreak: brickBreak, brickResist: brickResist,
    wallHit: wallHit, powerup: powerup, launch: launch, loseLife: loseLife,
    gameOver: gameOver, levelClear: levelClear,
    overdriveEnter: overdriveEnter, overdriveExit: overdriveExit,
    uiClick: uiClick, powerHitSfx: powerHitSfx
  };
})();
