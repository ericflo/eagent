/* OVERDRIVE audio — 100% synthesized WebAudio SFX + adaptive music. No assets.
   Plain script (no modules) so it works over file://. Exposes window.AudioSys. */
(function () {
  'use strict';
  var ctx = null, master = null, musicBus = null, sfxBus = null;
  var muted = false, started = false;
  var musicTimer = null, step = 0, intensity = 0; // 0..1 from multiplier
  var topsideOn = false, frenzyT = 0;

  function ensure() {
    if (ctx) { if (ctx.state === 'suspended') ctx.resume(); return true; }
    var AC = window.AudioContext || window.webkitAudioContext;
    if (!AC) return false;
    ctx = new AC();
    master = ctx.createGain(); master.gain.value = muted ? 0 : 0.9; master.connect(ctx.destination);
    musicBus = ctx.createGain(); musicBus.gain.value = 0.34; musicBus.connect(master);
    sfxBus = ctx.createGain(); sfxBus.gain.value = 0.9; sfxBus.connect(master);
    startMusic();
    started = true;
    return true;
  }

  function env(g, t0, a, peak, d) {
    g.gain.setValueAtTime(0.0001, t0);
    g.gain.linearRampToValueAtTime(peak, t0 + a);
    g.gain.exponentialRampToValueAtTime(0.0001, t0 + a + d);
  }
  function osc(type, freq, t0, dur, peak, bus, slideTo) {
    if (!ctx) return;
    var o = ctx.createOscillator(), g = ctx.createGain();
    o.type = type; o.frequency.setValueAtTime(freq, t0);
    if (slideTo) o.frequency.exponentialRampToValueAtTime(Math.max(20, slideTo), t0 + dur);
    env(g, t0, 0.005, peak, dur);
    o.connect(g); g.connect(bus || sfxBus);
    o.start(t0); o.stop(t0 + dur + 0.1);
  }
  function noise(t0, dur, peak, filterFreq, type) {
    if (!ctx) return;
    var len = Math.max(1, Math.floor(ctx.sampleRate * dur));
    var buf = ctx.createBuffer(1, len, ctx.sampleRate);
    var d = buf.getChannelData(0);
    for (var i = 0; i < len; i++) d[i] = Math.random() * 2 - 1;
    var src = ctx.createBufferSource(); src.buffer = buf;
    var f = ctx.createBiquadFilter(); f.type = type || 'highpass'; f.frequency.value = filterFreq || 3000;
    var g = ctx.createGain(); env(g, t0, 0.004, peak, dur);
    src.connect(f); f.connect(g); g.connect(sfxBus);
    src.start(t0); src.stop(t0 + dur + 0.1);
  }

  // ---- SFX ----
  function paddleBlip(pos01) {
    if (!ctx) return; var t = ctx.currentTime;
    var f = 300 + pos01 * 500; // pitch by hit position
    osc('square', f, t, 0.09, 0.25, sfxBus, f * 1.5);
    osc('sine', f * 2, t, 0.07, 0.12, sfxBus);
  }
  function wallTick() { if (!ctx) return; osc('square', 220, ctx.currentTime, 0.05, 0.12, sfxBus); }
  function brickShatter(combo) {
    if (!ctx) return; var t = ctx.currentTime;
    var base = 480 * Math.pow(1.059, Math.min(combo, 24)); // pitch climbs with combo
    osc('triangle', base, t, 0.16, 0.35, sfxBus, base * 2);
    osc('sawtooth', base * 0.5, t, 0.12, 0.15, sfxBus, base);
    noise(t, 0.12, 0.22, 2500);
  }
  function denied() { if (!ctx) return; var t = ctx.currentTime; osc('sawtooth', 160, t, 0.18, 0.25, sfxBus, 90); }
  function shieldPing() { if (!ctx) return; var t = ctx.currentTime; osc('sine', 1200, t, 0.25, 0.2, sfxBus, 500); }
  function explosion() {
    if (!ctx) return; var t = ctx.currentTime;
    osc('sawtooth', 120, t, 0.5, 0.4, sfxBus, 30);
    noise(t, 0.5, 0.4, 400, 'lowpass');
    noise(t, 0.2, 0.3, 4000);
  }
  function powerFanfare() {
    if (!ctx) return; var t = ctx.currentTime;
    var seq = [523, 659, 784, 1046];
    for (var i = 0; i < seq.length; i++) osc('square', seq[i], t + i * 0.07, 0.14, 0.2, sfxBus);
  }
  function launch() { if (!ctx) return; var t = ctx.currentTime; osc('sine', 300, t, 0.15, 0.3, sfxBus, 700); }
  function loseLife() {
    if (!ctx) return; var t = ctx.currentTime;
    var seq = [440, 349, 262, 175];
    for (var i = 0; i < seq.length; i++) osc('sawtooth', seq[i], t + i * 0.12, 0.18, 0.25, sfxBus);
  }
  function levelClear() {
    if (!ctx) return; var t = ctx.currentTime;
    var seq = [523, 659, 784, 1046, 1318];
    for (var i = 0; i < seq.length; i++) { osc('triangle', seq[i], t + i * 0.09, 0.25, 0.3, sfxBus); }
  }
  function gameOver() {
    if (!ctx) return; var t = ctx.currentTime;
    var seq = [392, 330, 262, 196, 131];
    for (var i = 0; i < seq.length; i++) osc('sawtooth', seq[i], t + i * 0.16, 0.25, 0.25, sfxBus);
  }
  function uiClick() { if (!ctx) return; osc('sine', 800, ctx.currentTime, 0.06, 0.2, sfxBus, 1200); }
  function topsideRiser() {
    if (!ctx) return; var t = ctx.currentTime;
    osc('sawtooth', 150, t, 0.8, 0.22, sfxBus, 1200);
    osc('sine', 75, t, 0.8, 0.25, sfxBus, 600);
  }

  // ---- Adaptive music: minor groove, layers added with intensity ----
  var bassLine = [55, 55, 65.4, 49, 55, 55, 82.4, 73.4];
  var arpNotes = [220, 261.6, 329.6, 440, 523.2, 440, 329.6, 261.6];
  function startMusic() {
    if (musicTimer) return;
    musicTimer = setInterval(function () {
      if (!ctx || muted) { step++; return; }
      var t = ctx.currentTime;
      var bar = Math.floor(step / 8) % 4;
      // Bass: always on, gets louder/faster feel with intensity
      var b = bassLine[step % 8];
      var o = ctx.createOscillator(), g = ctx.createGain();
      o.type = 'sawtooth'; o.frequency.value = b;
      var f = ctx.createBiquadFilter(); f.type = 'lowpass'; f.frequency.value = 300 + intensity * 900;
      env(g, t, 0.01, 0.16 + intensity * 0.12, 0.22);
      o.connect(f); f.connect(g); g.connect(musicBus);
      o.start(t); o.stop(t + 0.35);
      // Kick-ish thump each beat
      if (step % 2 === 0) {
        var k = ctx.createOscillator(), kg = ctx.createGain();
        k.type = 'sine'; k.frequency.setValueAtTime(120, t);
        k.frequency.exponentialRampToValueAtTime(40, t + 0.12);
        env(kg, t, 0.004, 0.5, 0.14); k.connect(kg); kg.connect(musicBus);
        k.start(t); k.stop(t + 0.25);
      }
      // Arp layer fades in with intensity > 0.15
      if (intensity > 0.15) {
        var n = arpNotes[(step + bar * 2) % 8] * (topsideOn ? 2 : 1);
        var a = ctx.createOscillator(), ag = ctx.createGain();
        a.type = 'square'; a.frequency.value = n;
        env(ag, t, 0.01, 0.05 + intensity * 0.09, 0.18);
        a.connect(ag); ag.connect(musicBus); a.start(t); a.stop(t + 0.3);
      }
      // Sparkle lead when frenzy is hot
      if (intensity > 0.55 && step % 4 === 3) {
        var s = ctx.createOscillator(), sg = ctx.createGain();
        s.type = 'triangle'; s.frequency.value = arpNotes[(step * 3) % 8] * 4;
        env(sg, t, 0.01, 0.08, 0.3); s.connect(sg); sg.connect(musicBus);
        s.start(t); s.stop(t + 0.45);
      }
      step++;
    }, 165);
  }

  window.AudioSys = {
    unlock: ensure,
    get muted() { return muted; },
    toggleMute: function () {
      muted = !muted;
      if (master) master.gain.value = muted ? 0 : 0.9;
      return muted;
    },
    setIntensity: function (x, top) { intensity = Math.max(0, Math.min(1, x)); topsideOn = !!top; },
    paddleBlip: paddleBlip, wallTick: wallTick, brickShatter: brickShatter,
    denied: denied, shieldPing: shieldPing, explosion: explosion,
    powerFanfare: powerFanfare, launch: launch, loseLife: loseLife,
    levelClear: levelClear, gameOver: gameOver, uiClick: uiClick,
    topsideRiser: topsideRiser
  };
})();
