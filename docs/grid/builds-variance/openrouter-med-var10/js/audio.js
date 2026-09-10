/* BREAKTHROUGH — js/audio.js
   Fully synthesized WebAudio engine. Init on first user gesture.
   Everything routed through a master gain + a simple limiter (compressor).
   Layers:
     - bounces / paddle / wall blips
     - brick break (pitch rises with combo)
     - powerup chimes
     - frenzy: rising arpeggio layer + beat pulse
     - ambient pad that intensifies with multiplier
     - ball lost thud, game over, level win jingle
*/
window.Audio = (function () {
  const U = window.U;
  let ctx = null, master = null, muted = false;
  let padOscs = [], padGain = null, padFilter = null;
  let frenzyGain = null, frenzyOsc = null, frenzyLfo = null;
  let started = false;

  function init() {
    if (started) return;
    const AC = window.AudioContext || window.webkitAudioContext;
    if (!AC) return;
    ctx = new AC();
    master = ctx.createGain();
    master.gain.value = 0.55;
    const comp = ctx.createDynamicsCompressor();
    comp.threshold.value = -14; comp.ratio.value = 6;
    master.connect(comp); comp.connect(ctx.destination);
    started = true;
    buildPad(); buildFrenzy();
    setPadIntensity(0);
  }

  function resume() { if (ctx && ctx.state === 'suspended') ctx.resume(); }

  // ---------- generic synth helpers ----------
  function env(g, t, a, peak, d, sustain = 0.0001, rel = 0.05) {
    g.gain.setValueAtTime(0.0001, t);
    g.gain.linearRampToValueAtTime(peak, t + a);
    g.gain.exponentialRampToValueAtTime(Math.max(sustain, 0.0001), t + a + d);
    g.gain.exponentialRampToValueAtTime(0.0001, t + a + d + rel);
  }
  function tone({ freq = 440, type = 'sine', at = 0, a = 0.005, d = 0.15, rel = 0.08, peak = 0.3,
                  slide = 0, dest = null, detune = 0 }) {
    if (!ctx || muted) return;
    const t = ctx.currentTime + at;
    const o = ctx.createOscillator(); o.type = type;
    o.frequency.setValueAtTime(freq, t);
    if (slide) o.frequency.exponentialRampToValueAtTime(Math.max(20, freq * slide), t + a + d);
    if (detune) o.detune.value = detune;
    const g = ctx.createGain();
    env(g, t, a, peak, d, 0.0001, rel);
    o.connect(g); g.connect(dest || master);
    o.start(t); o.stop(t + a + d + rel + 0.05);
  }
  function noise({ at = 0, d = 0.12, peak = 0.25, freq = 1200, q = 1, type = 'bandpass' }) {
    if (!ctx || muted) return;
    const t = ctx.currentTime + at;
    const len = Math.max(1, (ctx.sampleRate * (d + 0.1)) | 0);
    const buf = ctx.createBuffer(1, len, ctx.sampleRate);
    const data = buf.getChannelData(0);
    for (let i = 0; i < len; i++) data[i] = Math.random() * 2 - 1;
    const src = ctx.createBufferSource(); src.buffer = buf;
    const f = ctx.createBiquadFilter(); f.type = type; f.frequency.value = freq; f.Q.value = q;
    const g = ctx.createGain(); env(g, t, 0.002, peak, d);
    src.connect(f); f.connect(g); g.connect(master);
    src.start(t); src.stop(t + d + 0.1);
  }

  // ---------- ambient pad (intensity 0..1 from multiplier) ----------
  function buildPad() {
    padGain = ctx.createGain(); padGain.gain.value = 0.0;
    padFilter = ctx.createBiquadFilter(); padFilter.type = 'lowpass';
    padFilter.frequency.value = 400; padFilter.Q.value = 0.7;
    padGain.connect(padFilter); padFilter.connect(master);
    // Am9-ish drone stack
    const freqs = [110, 164.81, 220, 277.18, 329.63];
    freqs.forEach((f, i) => {
      const o = ctx.createOscillator();
      o.type = i % 2 ? 'triangle' : 'sawtooth';
      o.frequency.value = f;
      o.detune.value = (i - 2) * 6;
      const g = ctx.createGain(); g.gain.value = 0.12 / (i + 1);
      o.connect(g); g.connect(padGain);
      // slow vibrato
      const lfo = ctx.createOscillator(); lfo.frequency.value = 0.07 + i * 0.03;
      const lg = ctx.createGain(); lg.gain.value = 3;
      lfo.connect(lg); lg.connect(o.detune);
      o.start(); lfo.start();
      padOscs.push(o, lfo);
    });
  }
  function setPadIntensity(x) {
    if (!ctx) return;
    x = U.clamp(x, 0, 1);
    const t = ctx.currentTime;
    padGain.gain.setTargetAtTime(x * 0.5, t, 0.6);
    padFilter.frequency.setTargetAtTime(400 + x * 2600, t, 0.6);
  }

  // ---------- frenzy layer: pulsing arpeggio + sub pulse ----------
  let frenzyPulseTimer = null;
  function buildFrenzy() {
    frenzyGain = ctx.createGain(); frenzyGain.gain.value = 0;
    frenzyGain.connect(master);
    frenzyOsc = ctx.createOscillator(); frenzyOsc.type = 'sawtooth';
    frenzyOsc.frequency.value = 220;
    const f = ctx.createBiquadFilter(); f.type = 'lowpass'; f.frequency.value = 900;
    frenzyOsc.connect(f); f.connect(frenzyGain);
    frenzyOsc.start();
    frenzyLfo = ctx.createOscillator(); frenzyLfo.type = 'square';
    frenzyLfo.frequency.value = 4; // beat pulse
    const lg = ctx.createGain(); lg.gain.value = 0.35;
    frenzyLfo.connect(lg); lg.connect(frenzyGain.gain);
    frenzyLfo.start();
  }
  function setFrenzy(level, on) {
    if (!ctx) return;
    const t = ctx.currentTime;
    // level 0..1 drives filter + base pitch; on/off fades the layer
    frenzyGain.gain.setTargetAtTime(on && !muted ? 0.10 + level * 0.14 : 0, t, 0.25);
    frenzyOsc.frequency.setTargetAtTime(110 + level * 220, t, 0.4);
    frenzyLfo.frequency.setTargetAtTime(2.5 + level * 6, t, 0.4);
  }
  // quick arpeggio note used while frenzied, called on each brick break
  function frenzyBlip(step, level) {
    const scale = [0, 3, 5, 7, 10, 12, 15, 17, 19, 22, 24];
    const semi = scale[step % scale.length] + Math.floor(step / scale.length) * 12;
    const f = 440 * Math.pow(2, semi / 12);
    tone({ freq: f, type: 'square', peak: 0.10 + level * 0.08, d: 0.09, a: 0.004 });
    tone({ freq: f * 2, type: 'sine', peak: 0.05, d: 0.07, at: 0.02 });
  }

  // ---------- one-shot sfx ----------
  function wallBounce() { tone({ freq: 620, type: 'sine', d: 0.05, peak: 0.16, slide: 0.7 }); }
  function paddleHit(energy) {
    const f = 260 + energy * 260;
    tone({ freq: f, type: 'triangle', d: 0.08, peak: 0.3 + energy * 0.15, slide: 1.4 });
    noise({ d: 0.05, peak: 0.08 + energy * 0.1, freq: 900 });
  }
  function brickBreak(combo, frenzy) {
    const f = 300 * Math.pow(2, Math.min(combo, 24) / 12); // pitch rises with combo
    tone({ freq: f, type: 'square', d: 0.1, peak: 0.22, slide: 1.5 });
    noise({ d: 0.12, peak: 0.2, freq: 1800 + combo * 60, q: 0.8 });
    if (frenzy) noise({ at: 0.01, d: 0.2, peak: 0.1, freq: 400, type: 'lowpass' });
  }
  function brickReject() { tone({ freq: 140, type: 'square', d: 0.07, peak: 0.15, slide: 0.8 }); }
  function powerup() {
    [523, 659, 784, 1046].forEach((f, i) => tone({ freq: f, at: i * 0.055, type: 'triangle', d: 0.12, peak: 0.2 }));
  }
  function laser() { tone({ freq: 980, type: 'sawtooth', d: 0.07, peak: 0.12, slide: 0.25 }); }
  function ballLost() {
    tone({ freq: 220, type: 'sawtooth', d: 0.5, peak: 0.3, slide: 0.25 });
    noise({ d: 0.4, peak: 0.2, freq: 220, type: 'lowpass' });
  }
  function gameOver() {
    [392, 330, 262, 196].forEach((f, i) => tone({ freq: f, at: i * 0.18, type: 'triangle', d: 0.4, peak: 0.25 }));
  }
  function levelWin() {
    [523, 659, 784, 1046, 1318].forEach((f, i) => tone({ freq: f, at: i * 0.11, type: 'square', d: 0.2, peak: 0.18 }));
  }
  function frenzyEnter() {
    noise({ d: 0.5, peak: 0.25, freq: 3000, q: 0.5 });
    [440, 554, 659, 880].forEach((f, i) => tone({ freq: f, at: i * 0.04, type: 'sawtooth', d: 0.3, peak: 0.14 }));
    tone({ freq: 55, type: 'sine', d: 0.6, peak: 0.4 });
  }
  function frenzyExit() { tone({ freq: 880, type: 'sine', d: 0.4, peak: 0.15, slide: 0.4 }); }
  function ui() { tone({ freq: 700, type: 'sine', d: 0.05, peak: 0.12 }); }

  function toggleMute() {
    muted = !muted;
    if (master) master.gain.value = muted ? 0 : 0.55;
    return muted;
  }
  function isMuted() { return muted; }

  return { init, resume, wallBounce, paddleHit, brickBreak, brickReject, powerup, laser,
           ballLost, gameOver, levelWin, frenzyEnter, frenzyExit, frenzyBlip, ui,
           setPadIntensity, setFrenzy, toggleMute, isMuted, get ready() { return started; } };
})();