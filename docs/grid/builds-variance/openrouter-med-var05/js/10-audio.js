/* ============================================================
   SKYBREAK — js/10-audio.js → window.AudioSys
   Fully synthesized WebAudio: lo-fi music engine (tiered) +
   one-shot SFX. No assets, no network, no DOM.
   Lazy AudioContext, safe before init (silent no-ops).
   May reference window.U (loads first).
   ============================================================ */
(function () {
  'use strict';
  var A = {};
  window.AudioSys = A;

  /* ---------------- state ---------------- */
  var ctx = null;           // AudioContext (lazy)
  var master = null;        // master gain node
  var musicBus = null;      // music gain (~0.5 of master)
  var sfxBus = null;        // sfx gain
  var muted = false;        // persisted via U.saveMuted
  var initialized = false;
  var musicOn = false;
  var tier = 0;
  var timerId = null;       // lookahead scheduler
  var nextTime = 0;         // next scheduling time (ctx time, s)
  var step16 = 0;           // 16th-note counter
  var padNodes = null;      // live pad {filt, gain} for tier transitions
  var arpGain = null, bassGain = null, hatGain = null, kickGain = null;

  // voice budgeting
  var voiceTimes = [];      // timestamps (ms) of recent voice grants
  var liveVoices = 0;       // scheduled sources not yet freed
  var LIVE_CAP = 48;

  var BPM = 90;
  var STEP = 60 / BPM / 4;  // one 16th note in seconds
  var LOOKAHEAD = 0.12;     // schedule 120ms ahead
  var TICK_MS = 25;

  // chord progression (midi): Am9, Fmaj7, Cmaj7, G6 — one chord per 2 bars (32 sixteenths)
  var CHORDS = [
    [57, 60, 64, 67, 71],   // Am9
    [53, 57, 60, 64, 69],   // Fmaj7
    [48, 52, 55, 59, 64],   // Cmaj7
    [55, 59, 62, 64, 67]    // G6
  ];
  var SUB_ROOTS = [33, 29, 24, 31]; // sub bass roots (A1, F1, C1, G1)

  function mtof(m) { return 440 * Math.pow(2, (m - 69) / 12); }

  /* ---------------- helpers ---------------- */
  function now() { return ctx ? ctx.currentTime : 0; }

  // rolling-window budget: max ~40 short voices per second
  function budget() {
    var t = Date.now();
    while (voiceTimes.length && t - voiceTimes[0] > 1000) voiceTimes.shift();
    if (voiceTimes.length >= 40) return false;
    voiceTimes.push(t);
    return true;
  }
  function slots() { return liveVoices < LIVE_CAP; }
  function release() { if (liveVoices > 0) liveVoices--; }

  // track a source: frees its slot on ended, with a timeout fallback
  function track(src, startAt, dur) {
    liveVoices++;
    var freed = false;
    src.onended = function () { if (!freed) { freed = true; release(); } };
    setTimeout(function () { if (!freed) { freed = true; release(); } }, dur * 1000 + 400);
  }

  function safe(fn) {
    try { return fn(); } catch (e) { /* never throw */ }
  }

  /* ---------------- init / mute ---------------- */
  A.init = function () {
    if (initialized) {
      safe(function () { if (ctx && ctx.state === 'suspended') ctx.resume(); });
      return;
    }
    safe(function () {
      var AC = window.AudioContext || window.webkitAudioContext;
      if (!AC) return;
      ctx = new AC();
      master = ctx.createGain();
      master.gain.value = muted ? 0 : 1;
      master.connect(ctx.destination);
      musicBus = ctx.createGain();
      musicBus.gain.value = 0.5;   // music at ~0.5 of master
      musicBus.connect(master);
      sfxBus = ctx.createGain();
      sfxBus.gain.value = 0.9;
      sfxBus.connect(master);
      buildMusicBuses();
      initialized = true;
      if (ctx.state === 'suspended') ctx.resume();
      if (musicOn) startScheduler();
    });
  };
  A.unlock = A.init;
  A.isInit = function () { return initialized; };

  A.muted = function () { return muted; };

  A.toggleMute = function () {
    muted = !muted;
    if (window.U) safe(function () { window.U.saveMuted(muted); });
    if (master) {
      safe(function () {
        master.gain.cancelScheduledValues(now());
        master.gain.setTargetAtTime(muted ? 0 : 1, now(), 0.05);
      });
    }
    return muted;
  };
  // sync persisted pref at boot without toggling
  A.setMuted = function (b) {
    muted = !!b;
    if (master) safe(function () { master.gain.value = muted ? 0 : 1; });
  };

  /* ---------------- raw voice: blip ---------------- */
  // {freq, dur, type, vol, slide, when} — osc + gain envelope, exp decay
  A.blip = function (o) {
    if (!initialized || !ctx) return;
    o = o || {};
    safe(function () {
      if (!budget() || !slots()) return;
      var t = o.when || now();
      var dur = o.dur || 0.1;
      var osc = ctx.createOscillator();
      osc.type = o.type || 'sine';
      var f0 = Math.max(20, o.freq || 440);
      osc.frequency.setValueAtTime(f0, t);
      if (o.slide) osc.frequency.exponentialRampToValueAtTime(Math.max(20, f0 * o.slide), t + dur);
      var g = ctx.createGain();
      g.gain.setValueAtTime(0.0001, t);
      g.gain.exponentialRampToValueAtTime(Math.max(0.0002, o.vol || 0.2), t + 0.008);
      g.gain.exponentialRampToValueAtTime(0.0001, t + dur);
      osc.connect(g); g.connect(sfxBus);
      osc.start(t); osc.stop(t + dur + 0.02);
      track(osc, t, dur + 0.02);
    });
  };

  // cached 1s white noise buffer
  var noiseBuf = null;
  function noise() {
    if (noiseBuf) return noiseBuf;
    var len = ctx.sampleRate;
    noiseBuf = ctx.createBuffer(1, len, ctx.sampleRate);
    var d = noiseBuf.getChannelData(0);
    for (var i = 0; i < len; i++) d[i] = Math.random() * 2 - 1;
    return noiseBuf;
  }

  // filtered noise burst helper
  function noiseBurst(t, dur, vol, filtType, f0, f1, q, dest) {
    if (!budget() || !slots()) return;
    var src = ctx.createBufferSource();
    src.buffer = noise();
    src.loop = true;
    var filt = ctx.createBiquadFilter();
    filt.type = filtType || 'lowpass';
    filt.frequency.setValueAtTime(f0, t);
    if (f1) filt.frequency.exponentialRampToValueAtTime(Math.max(30, f1), t + dur);
    filt.Q.value = q || 1;
    var g = ctx.createGain();
    g.gain.setValueAtTime(0.0001, t);
    g.gain.exponentialRampToValueAtTime(vol, t + 0.005);
    g.gain.exponentialRampToValueAtTime(0.0001, t + dur);
    src.connect(filt); filt.connect(g); g.connect(dest || sfxBus);
    src.start(t); src.stop(t + dur + 0.02);
    track(src, t, dur + 0.02);
  }

  function det() { return 1 + (Math.random() - 0.5) * 0.06; } // slight per-play detune

  /* ---------------- SFX recipes ---------------- */
  var IMPACTS = {
    big: function (t) {
      // brick cluster boom: filtered noise burst + low sine drop
      A.blip({ freq: 130 * det(), dur: 0.32, type: 'sine', vol: 0.5, slide: 0.35, when: t });
      noiseBurst(t, 0.28, 0.35, 'lowpass', 900, 120, 0.7);
      A.blip({ freq: 70 * det(), dur: 0.4, type: 'triangle', vol: 0.35, slide: 0.6, when: t + 0.01 });
    },
    small: function (t) {
      // normal brick pop: short square blip, pitch down
      A.blip({ freq: 520 * det(), dur: 0.09, type: 'square', vol: 0.16, slide: 0.55, when: t });
    },
    ascend: function (t) {
      // rising sweep + shimmer + boom
      A.blip({ freq: 200 * det(), dur: 0.55, type: 'sawtooth', vol: 0.18, slide: 4, when: t });
      A.blip({ freq: 880 * det(), dur: 0.5, type: 'sine', vol: 0.1, slide: 2.2, when: t + 0.12 });
      A.blip({ freq: 1760, dur: 0.3, type: 'sine', vol: 0.06, slide: 1.5, when: t + 0.2 });
      A.blip({ freq: 90, dur: 0.5, type: 'sine', vol: 0.4, slide: 0.4, when: t + 0.4 });
      noiseBurst(t + 0.4, 0.3, 0.25, 'lowpass', 1200, 150, 0.7);
    },
    powerup: function (t) {
      // rising 2-note chime
      A.blip({ freq: 660 * det(), dur: 0.12, type: 'triangle', vol: 0.2, when: t });
      A.blip({ freq: 990 * det(), dur: 0.22, type: 'triangle', vol: 0.22, when: t + 0.1 });
    },
    powerdown: function (t) {
      // falling detuned wobble
      var d = det();
      A.blip({ freq: 420 * d, dur: 0.3, type: 'square', vol: 0.12, slide: 0.4, when: t });
      A.blip({ freq: 404 * d, dur: 0.3, type: 'square', vol: 0.12, slide: 0.42, when: t + 0.02 });
    },
    'break': function (t) {
      // glassy click
      A.blip({ freq: 2400 * det(), dur: 0.05, type: 'triangle', vol: 0.12, slide: 1.6, when: t });
      noiseBurst(t, 0.06, 0.08, 'highpass', 3000, 5000, 1);
    },
    tick: function (t) {
      A.blip({ freq: 1200 * det(), dur: 0.03, type: 'square', vol: 0.07, when: t });
    },
    buzz: function (t) {
      // denied/glance: lowpassed noise thunk + low clank
      noiseBurst(t, 0.14, 0.28, 'lowpass', 500, 100, 1.2);
      A.blip({ freq: 110 * det(), dur: 0.12, type: 'triangle', vol: 0.2, slide: 0.7, when: t });
    },
    life: function (t) {
      // 1-up arpeggio up
      var f = [523, 659, 784, 1047];
      for (var i = 0; i < f.length; i++) {
        A.blip({ freq: f[i] * det(), dur: 0.14, type: 'square', vol: 0.14, when: t + i * 0.08 });
      }
    },
    ui: function (t) {
      A.blip({ freq: 700 * det(), dur: 0.05, type: 'sine', vol: 0.1, when: t });
    }
  };

  A.impact = function (name) {
    if (!initialized || !ctx) return;
    var fn = IMPACTS[name];
    if (!fn) return;
    safe(function () { fn(now() + 0.001); });
  };

  /* ---------------- music engine ---------------- */
  function buildMusicBuses() {
    kickGain = ctx.createGain();  kickGain.gain.value = 1;   kickGain.connect(musicBus);
    hatGain = ctx.createGain();   hatGain.gain.value = 0.3;  hatGain.connect(musicBus);
    arpGain = ctx.createGain();   arpGain.gain.value = 0;    arpGain.connect(musicBus);
    bassGain = ctx.createGain();  bassGain.gain.value = 0;   bassGain.connect(musicBus);
  }

  // slow filtered saw pad, one chord per 2 bars, gentle attack/release
  function playPad(t, chordIdx) {
    safe(function () {
      var notes = CHORDS[chordIdx];
      var chordDur = STEP * 32;
      var cutoff = tier >= 2 ? 1400 : 600;
      var filt = ctx.createBiquadFilter();
      filt.type = 'lowpass';
      filt.frequency.setValueAtTime(300, t);
      filt.frequency.linearRampToValueAtTime(cutoff, t + 0.4);
      filt.Q.value = 0.7;
      var g = ctx.createGain();
      g.gain.setValueAtTime(0.0001, t);
      g.gain.linearRampToValueAtTime(0.055, t + 0.5);              // gentle attack
      g.gain.setValueAtTime(0.055, t + chordDur - 0.4);
      g.gain.linearRampToValueAtTime(0.0001, t + chordDur);        // gentle release
      filt.connect(g); g.connect(musicBus);
      for (var i = 0; i < notes.length; i++) {
        var o = ctx.createOscillator();
        o.type = 'sawtooth';
        o.frequency.value = mtof(notes[i]) * (1 + (Math.random() - 0.5) * 0.004);
        o.connect(filt);
        o.start(t); o.stop(t + chordDur + 0.05);
        track(o, t, chordDur + 0.05);
      }
      padNodes = { filt: filt, gain: g };
    });
  }

  function kick(t) {
    safe(function () {
      if (!budget()) return;
      var o = ctx.createOscillator();
      o.type = 'sine';
      o.frequency.setValueAtTime(120, t);
      o.frequency.exponentialRampToValueAtTime(45, t + 0.12);
      var g = ctx.createGain();
      g.gain.setValueAtTime(0.5, t);
      g.gain.exponentialRampToValueAtTime(0.001, t + 0.18);
      o.connect(g); g.connect(kickGain);
      o.start(t); o.stop(t + 0.2);
      track(o, t, 0.2);
    });
  }

  function hat(t) {
    safe(function () {
      if (!budget()) return;
      var src = ctx.createBufferSource();
      src.buffer = noise();
      var filt = ctx.createBiquadFilter();
      filt.type = 'highpass';
      filt.frequency.value = 7000;
      var g = ctx.createGain();
      var open = tier >= 2 || (step16 % 8 === 7); // hats open up at tier2 / bar ends
      g.gain.setValueAtTime(open ? 0.06 : 0.03, t);
      g.gain.exponentialRampToValueAtTime(0.001, t + (open ? 0.12 : 0.04));
      src.connect(filt); filt.connect(g); g.connect(hatGain);
      src.start(t); src.stop(t + 0.15);
      track(src, t, 0.15);
    });
  }

  function arpPluck(t, chordIdx) {
    safe(function () {
      if (!budget()) return;
      if (tier >= 3 && (step16 % 2)) return; // tier3: arp goes 8ths double-time feel
      var notes = CHORDS[chordIdx];
      var n = notes[(step16 * 7) % notes.length];
      var o = ctx.createOscillator();
      o.type = 'triangle';
      o.frequency.value = mtof(n + 12) * (1 + (Math.random() - 0.5) * 0.003);
      var g = ctx.createGain();
      g.gain.setValueAtTime(0.0001, t);
      g.gain.exponentialRampToValueAtTime(0.11, t + 0.01);
      g.gain.exponentialRampToValueAtTime(0.0001, t + 0.22);
      var filt = ctx.createBiquadFilter();
      filt.type = 'lowpass'; filt.frequency.value = 2200;
      o.connect(filt); filt.connect(g); g.connect(arpGain);
      o.start(t); o.stop(t + 0.25);
      track(o, t, 0.25);
    });
  }

  function subBass(t, chordIdx) {
    safe(function () {
      if (!budget()) return;
      var o = ctx.createOscillator();
      o.type = 'sine';
      o.frequency.value = mtof(SUB_ROOTS[chordIdx]);
      var g = ctx.createGain();
      var dur = STEP * (tier >= 3 ? 4 : 8);
      g.gain.setValueAtTime(0.0001, t);
      g.gain.linearRampToValueAtTime(0.16, t + 0.1);
      g.gain.setValueAtTime(0.16, t + dur - 0.1);
      g.gain.linearRampToValueAtTime(0.0001, t + dur);
      o.connect(g); g.connect(bassGain);
      o.start(t); o.stop(t + dur + 0.05);
      track(o, t, dur + 0.05);
    });
  }

  // lookahead scheduler: setInterval ~25ms, schedule 120ms ahead
  function schedulerTick() {
    safe(function () {
      if (!ctx || !musicOn) return;
      while (nextTime < now() + LOOKAHEAD) {
        var s16 = step16 % 32;
        var chordIdx = Math.floor(step16 / 32) % 4;
        var swing = (step16 % 2 === 1) ? STEP * 0.14 : 0; // subtle swing on off-16ths

        if (s16 === 0) playPad(nextTime, chordIdx);
        if (s16 === 0 || s16 === 8) kick(nextTime);       // beats 1 & 3
        hat(nextTime + swing);
        if (tier >= 1) arpPluck(nextTime + swing, chordIdx);
        if (tier >= 3 && s16 % 8 === 0) subBass(nextTime, chordIdx);

        nextTime += STEP;
        step16++;
      }
    });
  }

  function startScheduler() {
    if (timerId) return;
    nextTime = now() + 0.05;
    timerId = setInterval(schedulerTick, TICK_MS);
  }
  function stopScheduler() {
    if (timerId) { clearInterval(timerId); timerId = null; }
  }

  A.startMusic = function () {
    musicOn = true;
    if (initialized && ctx) {
      safe(function () {
        if (ctx.state === 'suspended') ctx.resume();
        step16 = 0;
        startScheduler();
      });
    }
  };
  A.stopMusic = function () {
    musicOn = false;
    stopScheduler();
    // scheduled pad tails ring out naturally via their envelope releases
  };

  /* ---------------- intensity tiers ---------------- */
  // tier0: pad + kick/hats. tier1: +arp pluck. tier2: pad cutoff 600→1400 +
  // hats open. tier3: +sub bass, arp double-time. Ramps ~0.4s, no clicks.
  A.setTier = function (t) {
    t = Math.max(0, Math.min(3, t | 0));
    var rose = t > tier;
    tier = t;
    if (!initialized || !ctx) return;
    safe(function () {
      var n = now();
      if (padNodes && padNodes.filt) {
        padNodes.filt.frequency.cancelScheduledValues(n);
        padNodes.filt.frequency.setTargetAtTime(tier >= 2 ? 1400 : 600, n, 0.2);
      }
      hatGain.gain.setTargetAtTime(tier >= 2 ? 0.55 : 0.3, n, 0.2);
      arpGain.gain.setTargetAtTime(tier >= 1 ? 0.5 : 0, n, 0.2);
      bassGain.gain.setTargetAtTime(tier >= 3 ? 0.7 : 0, n, 0.2);
      if (rose) {
        // one-shot subtle swell on tier increase
        var g = ctx.createGain();
        g.gain.setValueAtTime(0.0001, n);
        g.gain.linearRampToValueAtTime(0.12, n + 0.15);
        g.gain.exponentialRampToValueAtTime(0.0001, n + 0.6);
        var o = ctx.createOscillator();
        o.type = 'sine';
        o.frequency.value = 880 + tier * 220;
        o.connect(g); g.connect(musicBus);
        o.start(n); o.stop(n + 0.65);
        track(o, n, 0.65);
      }
    });
  };
  A.getTier = function () { return tier; };
})();