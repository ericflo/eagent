// Breakthrough — audio.js: procedural WebAudio SFX + arpeggiator music
window.BT = window.BT || {};
BT.audio = (function () {
  var ctx = null, master = null, musicGain = null, sfxGain = null;
  var muted = BT.util.store.get('muted', false);
  var unlocked = false;
  var arp = { on: false, step: 0, timer: 0, tempo: 6, tier: 0 };

  function ensure() {
    if (ctx) return true;
    var AC = window.AudioContext || window.webkitAudioContext;
    if (!AC) return false;
    ctx = new AC();
    master = ctx.createGain();
    master.gain.value = muted ? 0 : 0.55;
    master.connect(ctx.destination);
    sfxGain = ctx.createGain(); sfxGain.gain.value = 0.9; sfxGain.connect(master);
    musicGain = ctx.createGain(); musicGain.gain.value = 0.28; musicGain.connect(master);
    return true;
  }

  function unlock() {
    if (!ensure()) return;
    if (ctx.state === 'suspended') { ctx.resume(); }
    unlocked = true;
    arp.on = true;
  }

  function isUnlocked() { return unlocked; }

  function noiseBuffer(dur) {
    var len = Math.floor(ctx.sampleRate * dur);
    var buf = ctx.createBuffer(1, len, ctx.sampleRate);
    var d = buf.getChannelData(0);
    for (var i = 0; i < len; i++) d[i] = Math.random() * 2 - 1;
    return buf;
  }

  function tone(freq, dur, type, vol, slideTo, delay) {
    if (!ctx || muted) return;
    var t = ctx.currentTime + (delay || 0);
    var o = ctx.createOscillator(), g = ctx.createGain();
    o.type = type || 'sine';
    o.frequency.setValueAtTime(freq, t);
    if (slideTo) o.frequency.exponentialRampToValueAtTime(Math.max(20, slideTo), t + dur);
    g.gain.setValueAtTime(0, t);
    g.gain.linearRampToValueAtTime(vol, t + 0.005);
    g.gain.exponentialRampToValueAtTime(0.0001, t + dur);
    o.connect(g); g.connect(sfxGain);
    o.start(t); o.stop(t + dur + 0.05);
  }

  function noise(dur, vol, filterFreq, delay) {
    if (!ctx || muted) return;
    var t = ctx.currentTime + (delay || 0);
    var src = ctx.createBufferSource();
    src.buffer = noiseBuffer(dur);
    var f = ctx.createBiquadFilter();
    f.type = 'lowpass'; f.frequency.value = filterFreq || 1200;
    var g = ctx.createGain();
    g.gain.setValueAtTime(vol, t);
    g.gain.exponentialRampToValueAtTime(0.0001, t + dur);
    src.connect(f); f.connect(g); g.connect(sfxGain);
    src.start(t);
  }

  var SFX = {
    paddle: function () { tone(220, 0.08, 'square', 0.18, 320); noise(0.04, 0.08, 900); },
    smash: function () { tone(160, 0.18, 'sawtooth', 0.28, 660); tone(880, 0.12, 'sine', 0.15, 1760); noise(0.1, 0.15, 3000); },
    wall: function () { tone(180, 0.05, 'triangle', 0.12); },
    brick: function (pitch) {
      var p = pitch || 0;
      tone(420 * Math.pow(1.06, p), 0.09, 'square', 0.16, 220 * Math.pow(1.06, p));
      noise(0.05, 0.1, 2400);
    },
    clang: function () { tone(1240, 0.18, 'square', 0.2, 1180); tone(620, 0.25, 'triangle', 0.14); noise(0.06, 0.12, 5000); },
    prismTing: function () { tone(1560, 0.25, 'sine', 0.15, 1480); tone(2340, 0.18, 'sine', 0.08); },
    shield: function () { tone(300, 0.14, 'sine', 0.2, 420); noise(0.08, 0.1, 800); },
    explode: function () { noise(0.5, 0.4, 400); tone(90, 0.4, 'sawtooth', 0.3, 40); tone(60, 0.5, 'sine', 0.3, 30); },
    boom: function (n) { noise(0.25, 0.25, 600); tone(120 + n * 60, 0.2, 'sawtooth', 0.2, 50); },
    laser: function () { tone(900, 0.08, 'sawtooth', 0.12, 300); },
    powerup: function () { tone(520, 0.1, 'sine', 0.2); tone(780, 0.12, 'sine', 0.2, null, 0.07); tone(1040, 0.16, 'sine', 0.2, null, 0.14); },
    life: function () { [520, 660, 880, 1100].forEach(function (f, i) { tone(f, 0.2, 'triangle', 0.2, null, i * 0.09); }); },
    lose: function () { tone(300, 0.5, 'sawtooth', 0.25, 80); noise(0.4, 0.2, 300); },
    breakthrough: function () {
      [440, 660, 880, 1320].forEach(function (f, i) { tone(f, 0.35, 'square', 0.18, null, i * 0.06); });
      noise(0.3, 0.15, 4000);
    },
    tierUp: function () { tone(660, 0.2, 'square', 0.2, 1320); },
    clear: function () { [523, 659, 784, 1046, 1318].forEach(function (f, i) { tone(f, 0.3, 'triangle', 0.2, null, i * 0.1); }); },
    gameover: function () { [400, 340, 280, 200].forEach(function (f, i) { tone(f, 0.4, 'sawtooth', 0.2, f * 0.7, i * 0.22); }); },
    phase: function () { tone(880, 0.05, 'sine', 0.05); },
    uiClick: function () { tone(600, 0.05, 'square', 0.12); },
    fire: function () { noise(0.15, 0.1, 2000); tone(200, 0.15, 'sawtooth', 0.1, 500); }
  };

  // Arpeggiator music: called with dt; tempo/tier follow rush
  function updateMusic(dt) {
    if (!ctx || muted || !unlocked || !arp.on) return;
    arp.timer += dt;
    var scales = [
      [220, 262, 330, 392, 440, 523],           // calm
      [262, 330, 392, 440, 523, 659],           // breakthrough
      [330, 392, 440, 523, 659, 784],           // unstoppable
      [392, 440, 523, 659, 784, 988],           // obliteration
      [440, 554, 659, 880, 1108, 1318]          // godmode
    ];
    var idx = Math.min(arp.tier, scales.length - 1);
    var interval = 1 / arp.tempo;
    while (arp.timer >= interval) {
      arp.timer -= interval;
      var sc = scales[idx];
      var f = sc[arp.step % sc.length] * (arp.step % 12 >= 6 ? 2 : 1);
      arp.step++;
      var t = ctx.currentTime;
      var o = ctx.createOscillator(), g = ctx.createGain();
      o.type = idx >= 3 ? 'sawtooth' : 'triangle';
      o.frequency.value = f;
      g.gain.setValueAtTime(0.12, t);
      g.gain.exponentialRampToValueAtTime(0.0001, t + 0.22);
      o.connect(g); g.connect(musicGain);
      o.start(t); o.stop(t + 0.3);
      // bass every 4th
      if (arp.step % 4 === 1) {
        var o2 = ctx.createOscillator(), g2 = ctx.createGain();
        o2.type = 'sine'; o2.frequency.value = f / 4;
        g2.gain.setValueAtTime(0.2, t);
        g2.gain.exponentialRampToValueAtTime(0.0001, t + 0.35);
        o2.connect(g2); g2.connect(musicGain);
        o2.start(t); o2.stop(t + 0.4);
      }
    }
  }

  function setRushTier(tier) {
    arp.tier = tier;
    arp.tempo = 6 + tier * 3.5;
  }

  function setMuffle(on) {
    if (!ctx) return;
    musicGain.gain.linearRampToValueAtTime(on ? 0.14 : 0.28, ctx.currentTime + 0.3);
  }

  function toggleMute() {
    muted = !muted;
    BT.util.store.set('muted', muted);
    if (master) master.gain.value = muted ? 0 : 0.55;
    return muted;
  }
  function isMuted() { return muted; }

  return { unlock: unlock, SFX: SFX, updateMusic: updateMusic, setRushTier: setRushTier,
           setMuffle: setMuffle, toggleMute: toggleMute, isMuted: isMuted, isUnlocked: isUnlocked };
})();
