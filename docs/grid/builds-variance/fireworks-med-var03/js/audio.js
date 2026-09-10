'use strict';
// ---------------------------------------------------------------------------
// audio.js — procedural WebAudio sound engine with adaptive intensity.
// Everything is synthesized (no assets). Master volume ties to the
// multiplier/hype level so the game literally sounds more powered up.
// ---------------------------------------------------------------------------

const AudioSys = (() => {
  let ctx = null;
  let master = null;
  let comp = null;
  let noiseBuf = null;
  let enabled = true;
  let unlocked = false;

  function ensure() {
    if (ctx) return true;
    try {
      const AC = window.AudioContext || window.webkitAudioContext;
      if (!AC) return false;
      ctx = new AC({ latencyHint: 'interactive' });
      comp = ctx.createDynamicsCompressor();
      comp.threshold.value = -18;
      comp.knee.value = 24;
      comp.ratio.value = 12;
      comp.attack.value = 0.002;
      comp.release.value = 0.15;
      master = ctx.createGain();
      master.gain.value = 0.85;
      master.connect(comp);
      comp.connect(ctx.destination);
      // 1s of white noise for reuse
      noiseBuf = ctx.createBuffer(1, ctx.sampleRate, ctx.sampleRate);
      const d = noiseBuf.getChannelData(0);
      for (let i = 0; i < d.length; i++) d[i] = Math.random() * 2 - 1;
      return true;
    } catch (e) {
      return false;
    }
  }

  function resume() {
    if (!ensure()) return;
    if (ctx.state === 'suspended') ctx.resume();
    unlocked = true;
  }

  function ready() {
    return enabled && ctx && ctx.state === 'running';
  }

  function tone({ freq = 440, type = 'sine', dur = 0.1, vol = 0.2,
                  attack = 0.004, decay = null, slide = 0, when = 0,
                  detune = 0, filter = null, filterFreq = 0, q = 1 }) {
    if (!ready()) return;
    const t0 = ctx.currentTime + when;
    const osc = ctx.createOscillator();
    osc.type = type;
    osc.frequency.setValueAtTime(Math.max(20, freq), t0);
    if (slide) osc.frequency.exponentialRampToValueAtTime(Math.max(20, freq + slide), t0 + dur);
    if (detune) osc.detune.value = detune;
    const g = ctx.createGain();
    g.gain.setValueAtTime(0, t0);
    g.gain.linearRampToValueAtTime(vol, t0 + attack);
    const d = decay !== null ? decay : dur;
    g.gain.exponentialRampToValueAtTime(0.0001, t0 + attack + d);
    let node = osc;
    if (filter) {
      const f = ctx.createBiquadFilter();
      f.type = filter;
      f.frequency.value = filterFreq || 800;
      f.Q.value = q;
      osc.connect(f); node = f;
    }
    node.connect(g); g.connect(master);
    osc.start(t0); osc.stop(t0 + attack + d + 0.05);
  }

  function noise({ dur = 0.15, vol = 0.2, filter = 'lowpass', freq = 1200,
                   q = 1, attack = 0.002, when = 0, slideTo = 0 }) {
    if (!ready()) return;
    const t0 = ctx.currentTime + when;
    const src = ctx.createBufferSource();
    src.buffer = noiseBuf;
    src.loop = true;
    const f = ctx.createBiquadFilter();
    f.type = filter;
    f.frequency.setValueAtTime(freq, t0);
    if (slideTo) f.frequency.exponentialRampToValueAtTime(Math.max(20, slideTo), t0 + dur);
    f.Q.value = q;
    const g = ctx.createGain();
    g.gain.setValueAtTime(0, t0);
    g.gain.linearRampToValueAtTime(vol, t0 + attack);
    g.gain.exponentialRampToValueAtTime(0.0001, t0 + dur);
    src.connect(f); f.connect(g); g.connect(master);
    src.start(t0); src.stop(t0 + dur + 0.05);
  }

  // ---- named sounds -------------------------------------------------------
  // hype: 0..1 global intensity; multi: current score multiplier
  let hype = 0;
  function setHype(h) { hype = clamp(h, 0, 1); }

  function paddleHit(hype) {
    const f = 180 + 140 * hype;
    tone({ freq: f, type: 'triangle', dur: 0.09, vol: 0.25, slide: 60 });
    tone({ freq: f * 2, type: 'sine', dur: 0.05, vol: 0.08 });
    if (hype > 0.4) tone({ freq: f * 3, type: 'sine', dur: 0.04, vol: 0.05 * hype });
  }

  function wallHit() {
    tone({ freq: 120, type: 'triangle', dur: 0.06, vol: 0.12 });
  }

  function brickHit(strength, hype) {
    // Pitch rises with brick row pitch + hype: the "combo instrument"
    const base = 300 + strength * 60 + hype * 300;
    tone({ freq: base, type: 'square', dur: 0.07, vol: 0.12, filter: 'lowpass', filterFreq: 1400 + hype * 2200 });
    tone({ freq: base * 1.5, type: 'sine', dur: 0.05, vol: 0.07 });
  }

  function brickBreak(strength, hype, multi) {
    const base = 320 + strength * 50 + hype * 260;
    tone({ freq: base, type: 'square', dur: 0.12, vol: 0.16, slide: base * 0.6, filter: 'lowpass', filterFreq: 2000 + hype * 3000 });
    noise({ dur: 0.1, vol: 0.12 + hype * 0.1, freq: 900 + hype * 1800, filter: 'bandpass', q: 0.8 });
    if (multi >= 4) tone({ freq: base * 2, type: 'sine', dur: 0.09, vol: 0.07 * Math.min(1, multi / 8) });
  }

  function steelTink() {
    tone({ freq: 950, type: 'triangle', dur: 0.05, vol: 0.14, slide: -80 });
    tone({ freq: 1900, type: 'sine', dur: 0.04, vol: 0.06 });
  }

  function powerupSpawn() {
    tone({ freq: 660, type: 'sine', dur: 0.12, vol: 0.14, slide: 300 });
  }

  function powerupCatch() {
    const notes = [523, 659, 784];
    notes.forEach((f, i) => tone({ freq: f, type: 'triangle', dur: 0.12, vol: 0.16, when: i * 0.06 }));
  }

  function breakout() {
    // THE moment: ball gets above the bricks
    const t0 = ctx ? ctx.currentTime : 0;
    tone({ freq: 392, type: 'sawtooth', dur: 0.5, vol: 0.2, slide: 400, filter: 'lowpass', filterFreq: 900 });
    [392, 494, 587, 784].forEach((f, i) => tone({ freq: f, type: 'triangle', dur: 0.3, vol: 0.15, when: 0.05 + i * 0.08 }));
    noise({ dur: 0.6, vol: 0.1, freq: 400, slideTo: 4000, filter: 'bandpass', q: 1.5 });
  }

  function overdriveStart() {
    if (!ready()) return;
    [261, 329, 392, 523, 659].forEach((f, i) => tone({ freq: f, type: 'square', dur: 0.4, vol: 0.1, when: i * 0.07, filter: 'lowpass', filterFreq: 1600 }));
    noise({ dur: 0.8, vol: 0.12, freq: 200, slideTo: 6000, filter: 'bandpass', q: 1 });
  }

  function laserShot(hype) {
    tone({ freq: 1400 + hype * 400, type: 'sawtooth', dur: 0.08, vol: 0.1, slide: -900, filter: 'lowpass', filterFreq: 2600 });
  }

  function lifeLost() {
    [440, 330, 247, 165].forEach((f, i) => tone({ freq: f, type: 'triangle', dur: 0.25, vol: 0.16, when: i * 0.12 }));
    noise({ dur: 0.5, vol: 0.08, freq: 800, slideTo: 100, filter: 'lowpass' });
  }

  function levelWin() {
    [523, 659, 784, 1047, 1319].forEach((f, i) => tone({ freq: f, type: 'triangle', dur: 0.35, vol: 0.16, when: i * 0.1 }));
  }

  function gameOver() {
    [330, 262, 220, 165, 110].forEach((f, i) => tone({ freq: f, type: 'sawtooth', dur: 0.5, vol: 0.12, when: i * 0.18, filter: 'lowpass', filterFreq: 1200 }));
  }

  function uiClick() {
    tone({ freq: 800, type: 'sine', dur: 0.05, vol: 0.1, slide: 200 });
  }

  function slowmoWhoosh() {
    noise({ dur: 0.4, vol: 0.14, freq: 3000, slideTo: 200, filter: 'lowpass' });
  }

  function shieldBlock() {
    tone({ freq: 200, type: 'sine', dur: 0.3, vol: 0.2, slide: -100 });
    tone({ freq: 500, type: 'triangle', dur: 0.15, vol: 0.1 });
  }

  function multiplierUp(step) {
    const f = 440 * Math.pow(1.122, step); // +2 semitones per step
    tone({ freq: f, type: 'triangle', dur: 0.15, vol: 0.14, slide: 80 });
  }

  function prismZap() {
    tone({ freq: 700, type: 'square', dur: 0.1, vol: 0.1, slide: 500, filter: 'lowpass', filterFreq: 1800 });
  }

  function deathBrick() {
    tone({ freq: 90, type: 'sawtooth', dur: 0.3, vol: 0.22, filter: 'lowpass', filterFreq: 300 });
    noise({ dur: 0.2, vol: 0.15, freq: 300, filter: 'lowpass' });
  }

  function toggleSwitch() {
    tone({ freq: 1200, type: 'square', dur: 0.04, vol: 0.12 });
    tone({ freq: 600, type: 'square', dur: 0.05, vol: 0.1, when: 0.05 });
  }

  return {
    resume, setHype, paddleHit, wallHit, brickHit, brickBreak, steelTink,
    powerupSpawn, powerupCatch, breakout, overdriveStart, laserShot, lifeLost,
    levelWin, gameOver, uiClick, slowmoWhoosh, shieldBlock, multiplierUp,
    prismZap, deathBrick, toggleSwitch,
    get unlocked() { return unlocked; },
    setEnabled(v) { enabled = v; if (master) master.gain.value = v ? 0.85 : 0; },
    get enabled() { return enabled; },
  };
})();
