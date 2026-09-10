/* audio.js — WebAudio synth. All sounds generated procedurally; no assets.
 * Lazily created on first user gesture (autoplay policy). */
(function (root) {
  'use strict';

  function Audio() {
    this.ctx = null;
    this.master = null;
    this.muted = false;
    this._arpTimer = null;
    this._arpRate = 0.5;
    this._arpStep = 0;
    this._arpOn = false;
  }

  Audio.prototype.init = function () {
    if (this.ctx) return;
    var AC = window.AudioContext || window.webkitAudioContext;
    if (!AC) return;
    this.ctx = new AC();
    var comp = this.ctx.createDynamicsCompressor();
    this.master = this.ctx.createGain();
    this.master.gain.value = 0.5;
    this.master.connect(comp);
    comp.connect(this.ctx.destination);
  };

  Audio.prototype.resume = function () {
    this.init();
    if (this.ctx && this.ctx.state === 'suspended') this.ctx.resume();
  };

  Audio.prototype.toggleMute = function () {
    this.muted = !this.muted;
    if (this.master) this.master.gain.value = this.muted ? 0 : 0.5;
    return this.muted;
  };

  Audio.prototype._env = function (node, t0, a, d, peak) {
    var g = this.ctx.createGain();
    g.gain.setValueAtTime(0.0001, t0);
    g.gain.linearRampToValueAtTime(peak, t0 + a);
    g.gain.exponentialRampToValueAtTime(0.0001, t0 + a + d);
    node.connect(g);
    g.connect(this.master);
    return g;
  };

  Audio.prototype.blip = function (freq, dur, type, vol, slideTo) {
    if (!this.ctx || this.muted) return;
    var t0 = this.ctx.currentTime;
    var o = this.ctx.createOscillator();
    o.type = type || 'square';
    o.frequency.setValueAtTime(freq, t0);
    if (slideTo) o.frequency.exponentialRampToValueAtTime(slideTo, t0 + dur);
    this._env(o, t0, 0.004, dur, vol || 0.15);
    o.start(t0); o.stop(t0 + dur + 0.02);
  };

  Audio.prototype.noise = function (dur, vol, filterFreq, q) {
    if (!this.ctx || this.muted) return;
    var t0 = this.ctx.currentTime;
    var len = Math.max(1, (dur * this.ctx.sampleRate) | 0);
    var buf = this.ctx.createBuffer(1, len, this.ctx.sampleRate);
    var d = buf.getChannelData(0);
    for (var i = 0; i < len; i++) d[i] = Math.random() * 2 - 1;
    var src = this.ctx.createBufferSource();
    src.buffer = buf;
    var f = this.ctx.createBiquadFilter();
    f.type = 'bandpass';
    f.frequency.value = filterFreq || 800;
    f.Q.value = q || 1;
    src.connect(f);
    this._env(f, t0, 0.002, dur, vol || 0.2);
    src.start(t0);
  };

  // --- game events ---
  Audio.prototype.paddle = function (combo) {
    this.blip(220 + Math.min(combo, 12) * 28, 0.07, 'square', 0.12);
  };
  Audio.prototype.wall = function () {
    this.blip(160, 0.045, 'triangle', 0.08);
  };
  Audio.prototype.brick = function (combo) {
    this.blip(340 + Math.min(combo, 16) * 42, 0.09, 'square', 0.14, 520 + combo * 30);
    this.noise(0.05, 0.08, 2400, 2);
  };
  Audio.prototype.clank = function () { // gate refused
    this.blip(90, 0.1, 'sawtooth', 0.12, 70);
    this.noise(0.06, 0.1, 300, 1.5);
  };
  Audio.prototype.phaseFizzle = function () {
    this.blip(700, 0.12, 'sine', 0.1, 220);
  };
  Audio.prototype.shield = function () {
    this.blip(500, 0.14, 'sawtooth', 0.1, 380);
  };
  Audio.prototype.launch = function () {
    this.blip(180, 0.14, 'square', 0.14, 520);
  };
  Audio.prototype.lose = function () {
    this.blip(300, 0.5, 'sawtooth', 0.16, 60);
    this.noise(0.4, 0.12, 400, 1);
  };
  Audio.prototype.gameOver = function () {
    var self = this;
    [330, 262, 196, 131].forEach(function (f, i) {
      setTimeout(function () { self.blip(f, 0.32, 'triangle', 0.16); }, i * 170);
    });
  };
  Audio.prototype.powerup = function () {
    var self = this;
    [523, 659, 784, 1047].forEach(function (f, i) {
      setTimeout(function () { self.blip(f, 0.09, 'square', 0.12); }, i * 60);
    });
  };
  Audio.prototype.overdriveEnter = function () {
    if (!this.ctx || this.muted) return;
    var t0 = this.ctx.currentTime;
    var o = this.ctx.createOscillator();
    o.type = 'sawtooth';
    o.frequency.setValueAtTime(110, t0);
    o.frequency.exponentialRampToValueAtTime(880, t0 + 0.8);
    this._env(o, t0, 0.02, 0.85, 0.14);
    o.start(t0); o.stop(t0 + 0.9);
  };
  Audio.prototype.hitstop = function () {
    this.blip(60, 0.18, 'sine', 0.2, 40);
  };

  // --- overdrive arp (rising pattern whose rate follows multiplier) ---
  var ARP_SCALE = [0, 3, 7, 10, 12, 15, 19, 22];
  Audio.prototype.setArpRate = function (hz) {
    this._arpRate = hz;
  };
  Audio.prototype.startArp = function () {
    if (this._arpOn || !this.ctx) return;
    this._arpOn = true;
    var self = this;
    function tick() {
      if (!self._arpOn) return;
      if (self.ctx && !self.muted) {
        var semi = ARP_SCALE[self._arpStep % ARP_SCALE.length] + 12 * Math.floor(self._arpStep / ARP_SCALE.length);
        var f = 330 * Math.pow(2, semi / 12);
        self.blip(f, 0.06, 'square', 0.05);
        self._arpStep++;
      }
      self._arpTimer = setTimeout(tick, Math.max(60, 1000 / self._arpRate));
    }
    tick();
  };
  Audio.prototype.stopArp = function () {
    this._arpOn = false;
    if (this._arpTimer) clearTimeout(this._arpTimer);
    this._arpTimer = null;
    this._arpStep = 0;
  };

  var BO = root.BO = root.BO || {};
  BO.audio = new Audio();
  if (typeof module !== 'undefined' && module.exports) module.exports = Audio;
})(typeof window !== 'undefined' ? window : globalThis);
