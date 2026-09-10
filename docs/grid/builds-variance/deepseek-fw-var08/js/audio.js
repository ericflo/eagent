/* OVERDRIVE — js/audio.js
 * window.AudioSys: fully synthesized WebAudio. SFX per DESIGN.md +
 * generative synthwave music loop (kick/bass/arp/hats) whose layers and
 * tempo escalate with POWER tier and overdrive. Lookahead scheduler.
 * Resume on first user gesture. Mute persisted to localStorage.
 * Classic script.
 */
(function () {
  'use strict';

  // ---- note helpers -----------------------------------------------------------
  var A4 = 440;
  function midi(n) { return A4 * Math.pow(2, (n - 69) / 12); }
  // pentatonic ladder for brick breaks: C major pentatonic
  var PENTA = [0, 2, 4, 7, 9];

  function clamp(v, a, b) { return v < a ? a : (v > b ? b : v); }

  window.AudioSys = {
    ctx: null, master: null, musicGain: null, sfxGain: null,
    muted: false, initialized: false, unlocked: false,
    tier: 0, overdrive: false, powerTier: 1,
    _musicTimer: null, _nextNoteTime: 0, _step: 0,
    _lastSfxTime: {},       // per-name throttle

    // ---- lifecycle ------------------------------------------------------------
    init: function () {
      if (this.initialized) return;
      try {
        var AC = window.AudioContext || window.webkitAudioContext;
        if (!AC) return;
        this.ctx = new AC();
        this.master = this.ctx.createGain();
        this.master.gain.value = 0.85;
        this.master.connect(this.ctx.destination);
        this.sfxGain = this.ctx.createGain();
        this.sfxGain.gain.value = 0.50;
        this.sfxGain.connect(this.master);
        this.musicGain = this.ctx.createGain();
        this.musicGain.gain.value = 0.55;
        this.musicGain.connect(this.master);
        this.initialized = true;
        try { this.muted = localStorage.getItem('od_muted') === '1'; } catch (e) { this.muted = false; }
        this.applyMute();
      } catch (e) { /* audio unavailable — game must still run */ }
    },

    /** Call on any user gesture. */
    unlock: function () {
      this.init();
      if (!this.ctx) return;
      if (this.ctx.state === 'suspended') this.ctx.resume();
      if (!this.unlocked) {
        this.unlocked = true;
        this.uiClick();          // audible confirmation
        this.startMusic();
      }
    },

    toggleMute: function () {
      this.muted = !this.muted;
      try { localStorage.setItem('od_muted', this.muted ? '1' : '0'); } catch (e) {}
      this.applyMute();
      return this.muted;
    },
    applyMute: function () {
      if (!this.master) return;
      this.master.gain.setTargetAtTime(this.muted ? 0 : 0.85, this.ctx.currentTime, 0.02);
    },

    setMusicState: function (tier, overdrive) {
      this.tier = tier; this.overdrive = overdrive;
    },

    // ===========================================================================
    // GENERATIVE MUSIC — synthwave kick/bass/arp/hats, 16-step loop, lookahead
    // ===========================================================================
    startMusic: function () {
      if (!this.ctx || this._musicOn) return;
      this._musicOn = true;
      this._nextNoteTime = this.ctx.currentTime + 0.06;
      this._step = 0;
      this._musicTimer = setInterval(this._schedule.bind(this), 40);
    },
    stopMusic: function () {
      this._musicOn = false;
      if (this._musicTimer) { clearInterval(this._musicTimer); this._musicTimer = null; }
    },

    _tempo: function () {
      var t = 118 * (1 + 0.08 * (this.powerTier - 1) + (this.overdrive ? 0.22 : 0));
      return clamp(t, 92, 210);
    },
    _beatDur: function () { return 60 / this._tempo(); },
    _stepDur: function () { return this._beatDur() / 4; },

    _schedule: function () {
      if (!this.unlocked || !this.master) return;
      var stepDur = this._stepDur();
      // lookahead ~0.12s
      while (this._nextNoteTime < this.ctx.currentTime + 0.12) {
        this._playStep(this._step, this._nextNoteTime, stepDur);
        this._nextNoteTime += stepDur;
        this._step = (this._step + 1) % 16;
      }
    },

    _playStep: function (s, t, dur) {
      var tier = this.powerTier || 1;
      var od = !!this.overdrive;

      // ---- kick: 4-on-the-floor (steps 0,4,8,12) ------------------------------
      if (s % 4 === 0) {
        this._kick(t);
      }
      // ---- hats: eighths (open on off-beats), extra 16ths when tier >= 3 -------
      if (s % 2 === 0 || tier >= 3) {
        this._hat(t, s % 2 === 1 ? 0.5 : 0.24);
      }
      // ---- bass: driving root pattern (layers from tier 2) ---------------------
      if (tier >= 2) {
        var bassNotes = [0, 0, 12, 0, 3, 3, 10, 5]; // semitone offsets from root
        var bn = bassNotes[s % 8];
        if (od && s % 4 === 2) bn = 24;              // overdrive octave stomp
        this._bass(midi(45 + bn), t, dur * 1.7, tier);
      }
      // ---- arp: light at tier 3, full at tier >= 4, anthemic under OD ----------
      if (tier >= 3 || od) {
        var root = 57; // A minor-ish lead
        var seq = od ? [0, 7, 12, 15, 12, 19, 15, 12, 7, 3, 7, 12] : [0, 3, 7, 12];
        var n = midi(root + seq[s % seq.length] + 12);
        this._arp(n, t, dur * 0.9, tier, od ? 0.30 : 0.16);
      }
      // ---- extra percussive clap on 4 & 12 when overdrive ----------------------
      if (od && (s === 4 || s === 12)) this._clap(t);
    },

    // ---- music voices ----------------------------------------------------------
    _kick: function (t) {
      var o = this.ctx.createOscillator();
      var g = this.ctx.createGain();
      o.type = 'sine';
      o.frequency.setValueAtTime(150, t);
      o.frequency.exponentialRampToValueAtTime(45, t + 0.12);
      g.gain.setValueAtTime(0.9, t);
      g.gain.exponentialRampToValueAtTime(0.001, t + 0.16);
      o.connect(g); g.connect(this.musicGain);
      o.start(t); o.stop(t + 0.2);
    },
    _hat: function (t, vol) {
      var len = this.ctx.sampleRate * 0.05;
      var buf = this.ctx.createBuffer(1, len, this.ctx.sampleRate);
      var d = buf.getChannelData(0);
      for (var i = 0; i < len; i++) d[i] = (Math.random() * 2 - 1) * Math.pow(1 - i / len, 2.5);
      var src = this.ctx.createBufferSource();
      src.buffer = buf;
      var hp = this.ctx.createBiquadFilter();
      hp.type = 'highpass'; hp.frequency.value = 7000;
      var g = this.ctx.createGain();
      g.gain.setValueAtTime(vol * 0.5, t);
      g.gain.exponentialRampToValueAtTime(0.001, t + 0.05);
      src.connect(hp); hp.connect(g); g.connect(this.musicGain);
      src.start(t);
    },
    _clap: function (t) {
      var len = this.ctx.sampleRate * 0.1;
      var buf = this.ctx.createBuffer(1, len, this.ctx.sampleRate);
      var d = buf.getChannelData(0);
      for (var i = 0; i < len; i++) {
        var e = Math.pow(1 - i / len, 3);
        d[i] = (Math.random() * 2 - 1) * e * (0.6 + 0.4 * Math.random());
      }
      var src = this.ctx.createBufferSource(); src.buffer = buf;
      var bp = this.ctx.createBiquadFilter();
      bp.type = 'bandpass'; bp.frequency.value = 1200; bp.Q.value = 0.8;
      var g = this.ctx.createGain();
      g.gain.setValueAtTime(0.5, t); g.gain.exponentialRampToValueAtTime(0.001, t + 0.12);
      src.connect(bp); bp.connect(g); g.connect(this.musicGain);
      src.start(t);
    },
    _bass: function (freq, t, dur, tier) {
      var o = this.ctx.createOscillator();
      o.type = 'sawtooth';
      o.frequency.value = freq;
      var f = this.ctx.createBiquadFilter();
      f.type = 'lowpass'; f.frequency.value = 240 + tier * 60; f.Q.value = 6;
      var g = this.ctx.createGain();
      g.gain.setValueAtTime(0.0001, t);
      g.gain.linearRampToValueAtTime(0.34, t + 0.015);
      g.gain.setValueAtTime(0.34, t + dur * 0.6);
      g.gain.exponentialRampToValueAtTime(0.001, t + dur);
      o.connect(f); f.connect(g); g.connect(this.musicGain);
      o.start(t); o.stop(t + dur + 0.02);
    },
    _arp: function (freq, t, dur, tier, vol) {
      var o = this.ctx.createOscillator();
      o.type = tier >= 4 ? 'square' : 'triangle';
      o.frequency.value = freq;
      var g = this.ctx.createGain();
      var attack = 0.005;
      g.gain.setValueAtTime(0.0001, t);
      g.gain.linearRampToValueAtTime(vol, t + attack);
      g.gain.exponentialRampToValueAtTime(0.001, t + dur * 0.92);
      var delay = this.ctx.createDelay(); delay.delayTime.value = 0.19;
      var fb = this.ctx.createGain(); fb.gain.value = 0.32;
      var wet = this.ctx.createGain(); wet.gain.value = 0.35;
      o.connect(g);
      g.connect(this.musicGain);
      g.connect(delay); delay.connect(fb); fb.connect(delay); delay.connect(wet); wet.connect(this.musicGain);
      o.start(t); o.stop(t + dur + 0.05);
    },

    // ===========================================================================
    // SFX
    // ===========================================================================
    _throttle: function (name, ms) {
      var now = performance.now();
      if (now - (this._lastSfxTime[name] || 0) < ms) return true;
      this._lastSfxTime[name] = now;
      return false;
    },

    _blip: function (opts) {
      if (!this.ctx || this.muted) return;
      var t0 = this.ctx.currentTime;
      var o = this.ctx.createOscillator();
      o.type = opts.type || 'sine';
      o.frequency.setValueAtTime(opts.f0 || 440, t0);
      if (opts.f1) o.frequency.exponentialRampToValueAtTime(opts.f1, t0 + (opts.dur || 0.1));
      var g = this.ctx.createGain();
      var v = opts.vol || 0.3;
      g.gain.setValueAtTime(0.0001, t0);
      g.gain.linearRampToValueAtTime(v, t0 + 0.006);
      g.gain.exponentialRampToValueAtTime(0.001, t0 + (opts.dur || 0.12));
      o.connect(g); g.connect(this.sfxGain);
      o.start(t0); o.stop(t0 + (opts.dur || 0.12) + 0.02);
    },

    _noise: function (dur, vol, filterType, freq, t0) {
      if (!this.ctx || this.muted) return;
      t0 = t0 === undefined ? this.ctx.currentTime : t0;
      var len = Math.max(1, Math.floor(this.ctx.sampleRate * dur));
      var buf = this.ctx.createBuffer(1, len, this.ctx.sampleRate);
      var d = buf.getChannelData(0);
      for (var i = 0; i < len; i++) d[i] = (Math.random() * 2 - 1) * (1 - i / len);
      var src = this.ctx.createBufferSource(); src.buffer = buf;
      var f = this.ctx.createBiquadFilter();
      f.type = filterType || 'lowpass'; f.frequency.value = freq || 1200;
      var g = this.ctx.createGain();
      g.gain.setValueAtTime(vol, t0);
      g.gain.exponentialRampToValueAtTime(0.001, t0 + dur);
      src.connect(f); f.connect(g); g.connect(this.sfxGain);
      src.start(t0);
    },

    // ---- the full SFX list -------------------------------------------------------
    paddleHit: function (combo, boost) {
      if (this._throttle('paddle', 40)) return;
      var base = 220 + combo * 14 + (boost ? 90 : 0);
      this._blip({ type: 'square', f0: base, f1: base * 1.5, dur: 0.09, vol: 0.30 });
    },
    wall: function () {
      if (this._throttle('wall', 30)) return;
      this._blip({ type: 'triangle', f0: 320, f1: 300, dur: 0.05, vol: 0.14 });
    },
    brickBreak: function (combo, odTier, type) {
      if (this._throttle('brick', 35)) return;
      // pentatonic ladder: note = scale[combo % 5], octave jumps with overdrive tier
      var deg = PENTA[combo % 5];
      var oct = Math.min(2, Math.floor(combo / 8)) + (odTier > 1 ? 1 : 0);
      var f0 = midi(60 + deg + oct * 12);
      var dur = 0.16;
      this._blip({ type: type === 'G' ? 'triangle' : 'square', f0: f0, f1: f0 * 1.15, dur: dur, vol: 0.28 });
      // tiny shimmer for golden
      if (type === 'G') this._blip({ type: 'sine', f0: f0 * 2, f1: f0 * 3, dur: 0.2, vol: 0.14 });
    },
    specialMiss: function () {
      if (this._throttle('miss', 60)) return;
      this._blip({ type: 'sine', f0: 150, f1: 70, dur: 0.22, vol: 0.32 });  // thud
    },
    steelHit: function () {
      if (this._throttle('steel', 70)) return;
      this._blip({ type: 'triangle', f0: 900, f1: 700, dur: 0.07, vol: 0.22 });
      this._noise(0.04, 0.12, 'bandpass', 2500);
    },
    laserZap: function () {
      if (this._throttle('laser', 45)) return;
      this._blip({ type: 'sawtooth', f0: 1400, f1: 300, dur: 0.12, vol: 0.20 });
    },
    explosion: function () {
      if (this._throttle('boom', 90)) return;
      this._noise(0.35, 0.45, 'lowpass', 800);
      this._blip({ type: 'sine', f0: 130, f1: 40, dur: 0.35, vol: 0.4 });
    },
    capsuleCatch: function () {
      if (this._throttle('cap', 60)) return;
      // pluck arp
      var t0 = this.ctx.currentTime;
      [0, 4, 7, 12].forEach(function (d, i) {
        var o = this.ctx.createOscillator();
        o.type = 'triangle';
        o.frequency.value = midi(72 + d);
        var g = this.ctx.createGain();
        var tt = t0 + i * 0.05;
        g.gain.setValueAtTime(0.0001, tt);
        g.gain.linearRampToValueAtTime(0.24, tt + 0.01);
        g.gain.exponentialRampToValueAtTime(0.001, tt + 0.18);
        o.connect(g); g.connect(this.sfxGain);
        o.start(tt); o.stop(tt + 0.2);
      }, this);
    },
    abilityStart: function () {
      this._blip({ type: 'sawtooth', f0: 300, f1: 900, dur: 0.18, vol: 0.18 });
      this._blip({ type: 'sine', f0: 600, f1: 1200, dur: 0.22, vol: 0.14 });
    },
    abilityEnd: function () {
      this._blip({ type: 'triangle', f0: 500, f1: 250, dur: 0.15, vol: 0.12 });
    },
    overdriveSting: function () {
      if (!this.ctx) return;
      var t0 = this.ctx.currentTime;
      [0, 4, 7, 12, 16].forEach(function (d, i) {
        var o = this.ctx.createOscillator();
        o.type = 'sawtooth';
        o.frequency.value = midi(57 + d);
        var g = this.ctx.createGain();
        var tt = t0 + i * 0.03;
        g.gain.setValueAtTime(0.0001, tt);
        g.gain.linearRampToValueAtTime(0.26, tt + 0.03);
        g.gain.exponentialRampToValueAtTime(0.001, tt + 0.9);
        o.connect(g); g.connect(this.sfxGain);
        o.start(tt); o.stop(tt + 1.0);
      }, this);
      // rising sweep
      this._blip({ type: 'sine', f0: 200, f1: 1600, dur: 0.8, vol: 0.16 });
    },
    oneUp: function () {
      if (!this.ctx) return;
      var t0 = this.ctx.currentTime;
      [60, 64, 67, 72, 76].forEach(function (n, i) {
        var o = this.ctx.createOscillator();
        o.type = 'square';
        o.frequency.value = midi(n);
        var g = this.ctx.createGain();
        var tt = t0 + i * 0.07;
        g.gain.setValueAtTime(0.0001, tt);
        g.gain.linearRampToValueAtTime(0.18, tt + 0.01);
        g.gain.exponentialRampToValueAtTime(0.001, tt + 0.14);
        o.connect(g); g.connect(this.sfxGain);
        o.start(tt); o.stop(tt + 0.16);
      }, this);
    },
    levelClear: function () {
      if (!this.ctx) return;
      var t0 = this.ctx.currentTime;
      [60, 64, 67, 72, 79, 76, 72, 79].forEach(function (n, i) {
        var o = this.ctx.createOscillator();
        o.type = 'square';
        o.frequency.value = midi(n);
        var g = this.ctx.createGain();
        var tt = t0 + i * 0.1;
        g.gain.setValueAtTime(0.0001, tt);
        g.gain.linearRampToValueAtTime(0.16, tt + 0.015);
        g.gain.exponentialRampToValueAtTime(0.001, tt + 0.16);
        o.connect(g); g.connect(this.sfxGain);
        o.start(tt); o.stop(tt + 0.2);
      }, this);
    },
    gameOver: function () {
      if (!this.ctx) return;
      var t0 = this.ctx.currentTime;
      [72, 71, 69, 67, 64, 60].forEach(function (n, i) {
        var o = this.ctx.createOscillator();
        o.type = 'triangle';
        o.frequency.value = midi(n);
        var g = this.ctx.createGain();
        var tt = t0 + i * 0.13;
        g.gain.setValueAtTime(0.0001, tt);
        g.gain.linearRampToValueAtTime(0.2, tt + 0.02);
        g.gain.exponentialRampToValueAtTime(0.001, tt + 0.3);
        o.connect(g); g.connect(this.sfxGain);
        o.start(tt); o.stop(tt + 0.35);
      }, this);
    },
    uiClick: function () {
      if (this._throttle('ui', 40)) return;
      this._blip({ type: 'square', f0: 700, f1: 900, dur: 0.06, vol: 0.15 });
    },
    countdownTick: function (go) {
      this._blip({ type: 'square', f0: go ? 660 : 440, f1: go ? 880 : 440, dur: go ? 0.25 : 0.08, vol: 0.2 });
    },
    ballLost: function () {
      this._blip({ type: 'sine', f0: 400, f1: 120, dur: 0.35, vol: 0.28 });
    },
    freeze: function () {
      this._blip({ type: 'sine', f0: 1200, f1: 400, dur: 0.25, vol: 0.18 });
    },
    emberSpawn: function () {
      this._blip({ type: 'sawtooth', f0: 200, f1: 900, dur: 0.25, vol: 0.14 });
    }
  };
})();
