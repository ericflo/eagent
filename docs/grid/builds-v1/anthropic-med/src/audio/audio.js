// src/audio/audio.js
// Attic Breaker — synthesized audio engine (SFX + adaptive music).
// Zero dependencies, no external audio files, everything built with WebAudio.
// Every public method is safe to call even if WebAudio is unavailable or the
// context fails to start: in that case all methods become silent no-ops.

// ---------------------------------------------------------------------------
// Internal state
// ---------------------------------------------------------------------------

let ctx = null;
let supported = true;      // false if WebAudio is unavailable at all
let started = false;       // init() has run and built the graph
let muted = false;
let masterVolume = 1;
let sfxVolume = 1;         // independent sfx bus volume 0..1
let musicVolume = 1;       // independent music bus volume 0..1

let masterGain = null;     // master volume (post everything)
let limiter = null;        // DynamicsCompressorNode acting as a "glue" bus compressor
let safetyLimiter = null;  // DynamicsCompressorNode acting as a brick-wall safety limiter
let sfxBus = null;         // gain bus for one-shot sfx (post sfxVolume)
let musicBus = null;       // gain bus for the music engine (post musicVolume)
let musicDuck = null;      // gain node used to duck music under loud sfx bursts (sidechain)
let reverbSend = null;     // gain feeding the convolver
let reverb = null;         // ConvolverNode with a synthesized impulse
let reverbReturn = null;   // gain after the convolver, feeds masterGain
let listenersAttached = false; // guard for visibilitychange/statechange listeners
let unlockPlayed = false;      // guard for the iOS silent-buffer unlock hack
let peakAnalyser = null;       // post-safety-limiter AnalyserNode, metering only
let peakTimeData = null;       // reusable Float32Array scratch buffer for metering
let peakHold = 0;              // running max |sample| seen since last reset (linear 0..~1+)
let ceilingTrim = null;        // fixed -1dBFS output trim after the safety limiter

const HAS_STEREO_PANNER = () =>
  typeof window !== 'undefined' && typeof window.StereoPannerNode !== 'undefined';

// Cached noise buffers for common durations (avoids re-synthesizing every hit).
const noiseCache = new Map();

// Voice accounting -----------------------------------------------------------
const MAX_VOICES = 24;
let liveVoices = 0;

// Per-sfx-name rate limiting / coalescing so 40 simultaneous bricks breaking
// doesn't spam 40 identical voices — extra triggers within the coalesce
// window get folded into one louder hit on the next allowed trigger.
const RATE_LIMIT_MS = 45;
const lastTrigger = new Map(); // name -> { time, skipped }

// ---------------------------------------------------------------------------
// Setup helpers
// ---------------------------------------------------------------------------

function now() {
  return ctx ? ctx.currentTime : 0;
}

function safeCreateContext() {
  const AC = (typeof window !== 'undefined') &&
    (window.AudioContext || window.webkitAudioContext);
  if (!AC) return null;
  try {
    return new AC();
  } catch (e) {
    return null;
  }
}

function buildImpulseResponse(duration = 2.2, decay = 3.2) {
  const sr = ctx.sampleRate;
  const len = Math.max(1, Math.floor(sr * duration));
  const impulse = ctx.createBuffer(2, len, sr);
  for (let ch = 0; ch < 2; ch++) {
    const data = impulse.getChannelData(ch);
    for (let i = 0; i < len; i++) {
      const t = i / len;
      data[i] = (Math.random() * 2 - 1) * Math.pow(1 - t, decay);
    }
  }
  return impulse;
}

function buildNoiseBuffer(duration) {
  const sr = ctx.sampleRate;
  const len = Math.max(1, Math.floor(sr * duration));
  const buf = ctx.createBuffer(1, len, sr);
  const data = buf.getChannelData(0);
  for (let i = 0; i < len; i++) data[i] = Math.random() * 2 - 1;
  return buf;
}

function getNoiseBuffer(duration) {
  const key = Math.round(duration * 200); // quantize to 5ms steps
  let buf = noiseCache.get(key);
  if (!buf) {
    buf = buildNoiseBuffer(Math.max(0.02, key / 200));
    noiseCache.set(key, buf);
  }
  return buf;
}

function makeDistortionCurve(amount = 20) {
  const n = 1024;
  const curve = new Float32Array(n);
  const k = amount;
  for (let i = 0; i < n; i++) {
    const x = (i * 2) / n - 1;
    curve[i] = ((3 + k) * x * 20 * (Math.PI / 180)) / (Math.PI + k * Math.abs(x));
  }
  return curve;
}

function makePanner(pan) {
  if (HAS_STEREO_PANNER()) {
    const p = ctx.createStereoPanner();
    p.pan.value = clamp(pan, -1, 1);
    return p;
  }
  // Fallback: passthrough gain node (no panning support).
  return ctx.createGain();
}

function clamp(v, lo, hi) {
  return v < lo ? lo : v > hi ? hi : v;
}

function rand(a, b) {
  return a + Math.random() * (b - a);
}

// Register a voice's worth of nodes so it counts against MAX_VOICES and is
// guaranteed to be disconnected (never leaks) once it's done playing.
function registerVoice(nodes, stopTime) {
  liveVoices++;
  const delayMs = Math.max(0, (stopTime - now()) * 1000) + 60;
  const timer = setTimeout(() => {
    for (const n of nodes) {
      try { if (n.stop) n.stop(0); } catch (e) { /* already stopped */ }
      try { n.disconnect(); } catch (e) { /* already disconnected */ }
    }
    liveVoices = Math.max(0, liveVoices - 1);
  }, delayMs);
  // Don't keep the process alive in non-browser test harnesses.
  if (timer && typeof timer.unref === 'function') timer.unref();
}

function canSpawnVoice() {
  return liveVoices < MAX_VOICES;
}

// ---------------------------------------------------------------------------
// init / master chain
// ---------------------------------------------------------------------------

/**
 * Lazily create the AudioContext and the master signal chain. Must be called
 * (or at least attempted) from within a user gesture handler for autoplay
 * policies to allow sound. Safe to call multiple times; subsequent calls
 * just try to resume a suspended context. Never throws.
 */
function init() {
  if (!supported) return;
  try {
    if (!ctx) {
      ctx = safeCreateContext();
      if (!ctx) { supported = false; return; }
    }
    if (!started) {
      // Gain-staging chain (loud sfx bursts should duck the music a touch,
      // and everything funnels through a glue compressor then a brick-wall
      // safety limiter so nothing can clip destination even under storms):
      //
      //   sfxBus (sfxVolume) -----------------------------\
      //                                                     -> limiter (glue) -> masterGain (master vol) -> safetyLimiter -> destination
      //   musicBus (musicVolume) -> musicDuck (sidechain) -/
      //   reverbSend -> convolver -> reverbReturn ---------/

      masterGain = ctx.createGain();
      masterGain.gain.value = muted ? 0 : masterVolume;

      safetyLimiter = ctx.createDynamicsCompressor();
      // Brick-wall-ish safety net: fast attack, high ratio, low threshold so
      // the true master output basically cannot exceed roughly -1 dBFS even
      // if every bus below clips together.
      safetyLimiter.threshold.value = -2;
      safetyLimiter.knee.value = 1;
      safetyLimiter.ratio.value = 20;
      safetyLimiter.attack.value = 0.001;
      safetyLimiter.release.value = 0.08;

      // Metering tap (test/HUD only): an AnalyserNode after the safety
      // limiter so tests/debug tools can measure real post-limiter peak
      // level. Adds negligible CPU and never affects the audible signal.
      peakAnalyser = ctx.createAnalyser();
      peakAnalyser.fftSize = 1024;
      peakAnalyser.smoothingTimeConstant = 0;
      peakTimeData = new Float32Array(peakAnalyser.fftSize);
      // Fixed -1dB output ceiling trim after the safety limiter. A
      // DynamicsCompressor never *adds* gain, so a compressor whose
      // steady-state output momentarily still reaches 0 dBFS (fast-attack
      // limiters aren't perfectly brick-wall on very fast transients) is
      // guaranteed to end up at -1 dBFS or below once this fixed trim is
      // applied — this is the hard numeric guarantee behind the "no
      // clipping / peak <= -1 dBFS" requirement.
      ceilingTrim = ctx.createGain();
      ceilingTrim.gain.value = 0.891; // 10^(-1/20) ≈ -1 dBFS
      safetyLimiter.connect(ceilingTrim);
      ceilingTrim.connect(peakAnalyser);
      peakAnalyser.connect(ctx.destination);

      masterGain.connect(safetyLimiter);

      limiter = ctx.createDynamicsCompressor();
      limiter.threshold.value = -14;
      limiter.knee.value = 18;
      limiter.ratio.value = 10;
      limiter.attack.value = 0.004;
      limiter.release.value = 0.22;
      limiter.connect(masterGain);

      sfxBus = ctx.createGain();
      sfxBus.gain.value = sfxVolume;
      sfxBus.connect(limiter);

      musicBus = ctx.createGain();
      musicBus.gain.value = musicVolume;

      // Sidechain duck node: sits between musicBus and the glue limiter so
      // loud sfx (explosions, tier-ups, hitstop, etc.) can briefly pull the
      // music down without touching the sfx path.
      musicDuck = ctx.createGain();
      musicDuck.gain.value = 1;
      musicBus.connect(musicDuck);
      musicDuck.connect(limiter);

      reverbSend = ctx.createGain();
      reverbSend.gain.value = 0.4;
      reverb = ctx.createConvolver();
      try { reverb.buffer = buildImpulseResponse(); } catch (e) { /* ignore */ }
      reverbReturn = ctx.createGain();
      reverbReturn.gain.value = 0.3;
      reverbSend.connect(reverb);
      reverb.connect(reverbReturn);
      reverbReturn.connect(limiter);

      started = true;
    }
    if (ctx.state === 'suspended' || ctx.state === 'interrupted') {
      ctx.resume().catch(() => {});
    }
    playSilentUnlockBuffer();
    attachLifecycleListeners();
  } catch (e) {
    supported = false;
  }
}

/**
 * iOS Safari (and some Android WebViews) require an actual audible-graph
 * touch inside the originating user gesture to fully unlock the context,
 * beyond just `resume()`. Play an ultra-short, silent buffer once per
 * context to satisfy that. Safe / cheap to call repeatedly (no-ops after
 * the first successful attempt per context).
 */
function playSilentUnlockBuffer() {
  if (unlockPlayed || !ctx) return;
  try {
    const buf = ctx.createBuffer(1, 1, ctx.sampleRate);
    const src = ctx.createBufferSource();
    src.buffer = buf;
    src.connect(ctx.destination);
    if (src.start) src.start(0);
    else if (src.noteOn) src.noteOn(0);
    unlockPlayed = true;
  } catch (e) { /* ignore */ }
}

/**
 * Attach visibilitychange (tab backgrounded on mobile browsers often
 * suspends/kills the context) and AudioContext statechange listeners so we
 * resume cleanly instead of staying silently dead. Attached once, ever.
 */
function attachLifecycleListeners() {
  if (listenersAttached || !ctx) return;
  listenersAttached = true;
  try {
    if (typeof document !== 'undefined' && document.addEventListener) {
      document.addEventListener('visibilitychange', () => {
        if (document.visibilityState === 'visible' && ctx) {
          if (ctx.state === 'suspended' || ctx.state === 'interrupted') {
            ctx.resume().catch(() => {});
          }
        }
      });
    }
    ctx.addEventListener('statechange', () => {
      if (ctx && (ctx.state === 'interrupted')) {
        ctx.resume().catch(() => {});
      }
    });
  } catch (e) { /* ignore */ }
}

/** Suspend the AudioContext (e.g. on tab blur) to save CPU/battery. Safe no-op if not ready. */
function suspend() {
  try { if (ctx && ctx.state === 'running') ctx.suspend().catch(() => {}); } catch (e) { /* ignore */ }
}

/** Resume the AudioContext (e.g. on tab focus). Safe no-op if not ready. */
function resume() {
  try { if (ctx && (ctx.state === 'suspended' || ctx.state === 'interrupted')) ctx.resume().catch(() => {}); } catch (e) { /* ignore */ }
}

/**
 * Debug/test-only metering: sample the post-safety-limiter output and
 * return {peak, peakDb, peakHold, peakHoldDb} where peak is the instantaneous
 * |sample| max in the current analyser window (linear 0..~1) and peakHold is
 * the running max since the last debugResetPeak() call. Never throws; safe
 * no-op ({peak:0,...}) if the graph isn't built.
 */
function debugPeak() {
  if (!peakAnalyser || !peakTimeData) return { peak: 0, peakDb: -Infinity, peakHold: 0, peakHoldDb: -Infinity };
  try {
    peakAnalyser.getFloatTimeDomainData(peakTimeData);
    let peak = 0;
    for (let i = 0; i < peakTimeData.length; i++) {
      const v = Math.abs(peakTimeData[i]);
      if (v > peak) peak = v;
    }
    if (peak > peakHold) peakHold = peak;
    const toDb = (v) => (v > 0 ? 20 * Math.log10(v) : -Infinity);
    return { peak, peakDb: toDb(peak), peakHold, peakHoldDb: toDb(peakHold) };
  } catch (e) {
    return { peak: 0, peakDb: -Infinity, peakHold, peakHoldDb: -Infinity };
  }
}

/** Reset the running peakHold tracked by debugPeak(). */
function debugResetPeak() {
  peakHold = 0;
}


/**
 * Mute or unmute all audio output instantly (music keeps scheduling silently).
 */
function setMuted(v) {
  muted = !!v;
  if (masterGain) {
    try { masterGain.gain.setTargetAtTime(muted ? 0 : masterVolume, now(), 0.01); }
    catch (e) { /* ignore */ }
  }
}

/**
 * Set master output volume 0..1 (does not affect the muted flag).
 */
function setMasterVolume(v) {
  masterVolume = clamp(v, 0, 1);
  if (masterGain && !muted) {
    try { masterGain.gain.setTargetAtTime(masterVolume, now(), 0.01); }
    catch (e) { /* ignore */ }
  }
}

/** Set the one-shot sfx bus volume 0..1, independent of music/master. */
function setSfxVolume(v) {
  sfxVolume = clamp(v, 0, 1);
  if (sfxBus) {
    try { sfxBus.gain.setTargetAtTime(sfxVolume, now(), 0.01); }
    catch (e) { /* ignore */ }
  }
}

/** Set the adaptive music bus volume 0..1, independent of sfx/master. */
function setMusicVolume(v) {
  musicVolume = clamp(v, 0, 1);
  if (musicBus) {
    try { musicBus.gain.setTargetAtTime(musicVolume, now(), 0.01); }
    catch (e) { /* ignore */ }
  }
}

/**
 * Gentle sidechain-style duck: briefly pulls the music bus down when a loud
 * sfx fires, then releases it back to unity. `amount` 0..1 is duck depth,
 * `ms` is the release time. Cheap manual envelope (no analyser needed) since
 * we already know exactly when/how loud each sfx voice is at trigger time.
 */
function duckMusic(amount, ms = 220) {
  if (!musicDuck) return;
  try {
    const depth = clamp(amount, 0, 1);
    const target = 1 - depth * 0.65;
    const t = now();
    const current = musicDuck.gain.value;
    musicDuck.gain.cancelScheduledValues(t);
    musicDuck.gain.setValueAtTime(Math.min(current, 1), t);
    musicDuck.gain.linearRampToValueAtTime(Math.min(target, current), t + 0.012);
    musicDuck.gain.linearRampToValueAtTime(1, t + Math.max(0.05, ms / 1000));
  } catch (e) { /* ignore */ }
}

/** Names loud/impactful enough to justify a sidechain duck on the music bus. */
const DUCK_NAMES = {
  brickExplode: 0.35, superball: 0.45, death: 0.5, hitstop: 0.65,
  tierUp: 0.55, dropBad: 0.3, atticEnter: 0.25, timeWarp: 0.2, ballLost: 0.3,
};


// ---------------------------------------------------------------------------
// Envelope / oscillator helpers used by the sfx builders
// ---------------------------------------------------------------------------

function envGain(g, t0, { attack = 0.005, decay = 0.15, sustain = 0.0001, peak = 1 }) {
  g.gain.cancelScheduledValues(t0);
  g.gain.setValueAtTime(0.0001, t0);
  g.gain.exponentialRampToValueAtTime(Math.max(peak, 0.0001), t0 + attack);
  g.gain.exponentialRampToValueAtTime(Math.max(sustain, 0.0001), t0 + attack + decay);
}

function osc(type, freq, t0, detune = 0) {
  const o = ctx.createOscillator();
  o.type = type;
  o.frequency.setValueAtTime(Math.max(1, freq), t0);
  o.detune.value = detune;
  return o;
}

function noiseSrc(duration) {
  const src = ctx.createBufferSource();
  src.buffer = getNoiseBuffer(duration);
  return src;
}

function outChain(pan, gainVal) {
  const g = ctx.createGain();
  g.gain.value = gainVal;
  const p = makePanner(pan);
  g.connect(p);
  p.connect(sfxBus);
  return { g, p };
}

// ---------------------------------------------------------------------------
// Individual sfx builders. Each receives (t0, opts) where opts has
// {pitch, gain, pan, variation} already normalized, and returns nothing —
// it just wires up nodes and schedules their own stop/cleanup.
// ---------------------------------------------------------------------------

const SFX = {
  paddle(t0, o) {
    const { g, p } = outChain(o.pan, 0.55 * o.gain);
    const a = osc('sine', 140 * o.pitch, t0, o.det);
    envGain(g, t0, { attack: 0.003, decay: 0.09, sustain: 0.0001, peak: 1 });
    a.connect(g);
    const click = noiseSrc(0.02);
    const hp = ctx.createBiquadFilter(); hp.type = 'highpass'; hp.frequency.value = 3000;
    const cg = ctx.createGain(); cg.gain.value = 0.25 * o.gain;
    click.connect(hp); hp.connect(cg); cg.connect(p);
    a.start(t0); a.stop(t0 + 0.12);
    click.start(t0); click.stop(t0 + 0.02);
    registerVoice([a, g, p, click, hp, cg], t0 + 0.12);
  },

  paddleUp(t0, o) {
    const { g, p } = outChain(o.pan, 0.5 * o.gain);
    const a = osc('sawtooth', 180 * o.pitch, t0, o.det);
    a.frequency.exponentialRampToValueAtTime(820 * o.pitch, t0 + 0.16);
    const lp = ctx.createBiquadFilter(); lp.type = 'lowpass';
    lp.frequency.setValueAtTime(400, t0);
    lp.frequency.exponentialRampToValueAtTime(4000, t0 + 0.16);
    envGain(g, t0, { attack: 0.01, decay: 0.2, sustain: 0.0001, peak: 0.9 });
    a.connect(lp); lp.connect(g);
    const thump = osc('sine', 90 * o.pitch, t0 + 0.15);
    const tg = ctx.createGain();
    envGain(tg, t0 + 0.15, { attack: 0.005, decay: 0.12, sustain: 0.0001, peak: 0.8 });
    thump.connect(tg); tg.connect(p);
    a.start(t0); a.stop(t0 + 0.2);
    thump.start(t0 + 0.15); thump.stop(t0 + 0.3);
    registerVoice([a, lp, g, p, thump, tg], t0 + 0.3);
  },

  wall(t0, o) {
    const { g, p } = outChain(o.pan, 0.4 * o.gain);
    const a = osc('square', 320 * o.pitch, t0, o.det);
    a.frequency.exponentialRampToValueAtTime(180 * o.pitch, t0 + 0.05);
    envGain(g, t0, { attack: 0.002, decay: 0.05, sustain: 0.0001, peak: 0.7 });
    a.connect(g);
    a.start(t0); a.stop(t0 + 0.06);
    registerVoice([a, g, p], t0 + 0.06);
  },

  brick(t0, o) {
    const { g, p } = outChain(o.pan, 0.5 * o.gain);
    const a = osc('triangle', 640 * o.pitch, t0, o.det);
    a.frequency.exponentialRampToValueAtTime(420 * o.pitch, t0 + 0.09);
    envGain(g, t0, { attack: 0.002, decay: 0.09, sustain: 0.0001, peak: 0.85 });
    a.connect(g);
    a.start(t0); a.stop(t0 + 0.1);
    registerVoice([a, g, p], t0 + 0.1);
  },

  brickHard(t0, o) {
    const { g, p } = outChain(o.pan, 0.55 * o.gain);
    const a = osc('square', 300 * o.pitch, t0, o.det);
    const b = osc('square', 305 * o.pitch, t0, -o.det);
    a.frequency.exponentialRampToValueAtTime(160 * o.pitch, t0 + 0.14);
    b.frequency.exponentialRampToValueAtTime(165 * o.pitch, t0 + 0.14);
    envGain(g, t0, { attack: 0.002, decay: 0.14, sustain: 0.0001, peak: 0.9 });
    a.connect(g); b.connect(g);
    const n = noiseSrc(0.02);
    const ng = ctx.createGain(); ng.gain.value = 0.3 * o.gain;
    n.connect(ng); ng.connect(p);
    a.start(t0); a.stop(t0 + 0.15);
    b.start(t0); b.stop(t0 + 0.15);
    n.start(t0); n.stop(t0 + 0.02);
    registerVoice([a, b, g, p, n, ng], t0 + 0.15);
  },

  brickAngle(t0, o) {
    const { g, p } = outChain(o.pan, 0.5 * o.gain);
    const a = osc('triangle', 500 * o.pitch, t0, o.det);
    a.frequency.setValueAtTime(500 * o.pitch, t0);
    a.frequency.linearRampToValueAtTime(720 * o.pitch, t0 + 0.06);
    a.frequency.linearRampToValueAtTime(560 * o.pitch, t0 + 0.12);
    envGain(g, t0, { attack: 0.003, decay: 0.12, sustain: 0.0001, peak: 0.85 });
    a.connect(g);
    a.start(t0); a.stop(t0 + 0.13);
    registerVoice([a, g, p], t0 + 0.13);
  },

  brickSpeed(t0, o) {
    const { g, p } = outChain(o.pan, 0.5 * o.gain);
    const a = osc('sawtooth', 700 * o.pitch, t0, o.det);
    a.frequency.exponentialRampToValueAtTime(1400 * o.pitch, t0 + 0.08);
    envGain(g, t0, { attack: 0.002, decay: 0.08, sustain: 0.0001, peak: 0.7 });
    a.connect(g);
    const n = noiseSrc(0.12);
    const hp = ctx.createBiquadFilter(); hp.type = 'highpass'; hp.frequency.value = 2000;
    const ng = ctx.createGain(); ng.gain.value = 0.4 * o.gain;
    n.connect(hp); hp.connect(ng); ng.connect(p);
    a.start(t0); a.stop(t0 + 0.1);
    n.start(t0); n.stop(t0 + 0.12);
    registerVoice([a, g, p, n, hp, ng], t0 + 0.12);
  },

  brickShield(t0, o) {
    const { g, p } = outChain(o.pan, 0.5 * o.gain);
    const a = osc('square', 220 * o.pitch, t0, o.det);
    const b = osc('square', 331 * o.pitch, t0, -o.det);
    envGain(g, t0, { attack: 0.001, decay: 0.1, sustain: 0.0001, peak: 0.7 });
    a.connect(g); b.connect(g);
    const n = noiseSrc(0.03);
    const bp = ctx.createBiquadFilter(); bp.type = 'bandpass'; bp.frequency.value = 2500; bp.Q.value = 4;
    const ng = ctx.createGain(); ng.gain.value = 0.3 * o.gain;
    n.connect(bp); bp.connect(ng); ng.connect(p);
    a.start(t0); a.stop(t0 + 0.11);
    b.start(t0); b.stop(t0 + 0.11);
    n.start(t0); n.stop(t0 + 0.03);
    registerVoice([a, b, g, p, n, bp, ng], t0 + 0.11);
  },

  brickExplode(t0, o) {
    const { g, p } = outChain(o.pan, 0.8 * o.gain);
    const n = noiseSrc(0.4);
    const lp = ctx.createBiquadFilter(); lp.type = 'lowpass';
    lp.frequency.setValueAtTime(4000, t0);
    lp.frequency.exponentialRampToValueAtTime(200, t0 + 0.4);
    envGain(g, t0, { attack: 0.004, decay: 0.4, sustain: 0.0001, peak: 1 });
    n.connect(lp); lp.connect(g);
    const boom = osc('sine', 90 * o.pitch, t0);
    boom.frequency.exponentialRampToValueAtTime(40, t0 + 0.35);
    const bg = ctx.createGain();
    envGain(bg, t0, { attack: 0.005, decay: 0.35, sustain: 0.0001, peak: 0.9 });
    boom.connect(bg); bg.connect(p);
    n.start(t0); n.stop(t0 + 0.4);
    boom.start(t0); boom.stop(t0 + 0.4);
    if (reverbSend) { const rs = ctx.createGain(); rs.gain.value = 0.5; g.connect(rs); rs.connect(reverbSend); registerVoice([rs], t0 + 0.4); }
    registerVoice([n, lp, g, p, boom, bg], t0 + 0.4);
  },

  brickMirror(t0, o) {
    const { g, p } = outChain(o.pan, 0.5 * o.gain);
    const a = osc('triangle', 500 * o.pitch, t0, o.det);
    const b = osc('triangle', 760 * o.pitch, t0 + 0.06, o.det);
    envGain(g, t0, { attack: 0.002, decay: 0.06, sustain: 0.0001, peak: 0.8 });
    const g2 = ctx.createGain();
    envGain(g2, t0 + 0.06, { attack: 0.002, decay: 0.06, sustain: 0.0001, peak: 0.8 });
    a.connect(g); b.connect(g2); g2.connect(p);
    a.start(t0); a.stop(t0 + 0.07);
    b.start(t0 + 0.06); b.stop(t0 + 0.13);
    registerVoice([a, g, p, b, g2], t0 + 0.13);
  },

  brickMagnet(t0, o) {
    const { g, p } = outChain(o.pan, 0.5 * o.gain);
    const a = osc('sine', 260 * o.pitch, t0, o.det);
    a.frequency.linearRampToValueAtTime(520 * o.pitch, t0 + 0.15);
    a.frequency.linearRampToValueAtTime(180 * o.pitch, t0 + 0.3);
    envGain(g, t0, { attack: 0.01, decay: 0.3, sustain: 0.0001, peak: 0.75 });
    a.connect(g);
    a.start(t0); a.stop(t0 + 0.3);
    registerVoice([a, g, p], t0 + 0.3);
  },

  launch(t0, o) {
    const { g, p } = outChain(o.pan, 0.6 * o.gain);
    const a = osc('sawtooth', 150 * o.pitch, t0, o.det);
    a.frequency.exponentialRampToValueAtTime(900 * o.pitch, t0 + 0.22);
    envGain(g, t0, { attack: 0.01, decay: 0.22, sustain: 0.0001, peak: 0.9 });
    a.connect(g);
    const n = noiseSrc(0.25);
    const hp = ctx.createBiquadFilter(); hp.type = 'highpass'; hp.frequency.value = 800;
    const ng = ctx.createGain(); ng.gain.value = 0.3 * o.gain;
    n.connect(hp); hp.connect(ng); ng.connect(p);
    a.start(t0); a.stop(t0 + 0.24);
    n.start(t0); n.stop(t0 + 0.25);
    registerVoice([a, g, p, n, hp, ng], t0 + 0.25);
  },

  powerup(t0, o) {
    const notes = [1, 1.25, 1.5, 2];
    const base = 440 * o.pitch;
    notes.forEach((mult, i) => {
      const tn = t0 + i * 0.055;
      const { g, p } = outChain(o.pan, 0.45 * o.gain);
      const a = osc('triangle', base * mult, tn, o.det);
      envGain(g, tn, { attack: 0.003, decay: 0.09, sustain: 0.0001, peak: 0.8 });
      a.connect(g);
      a.start(tn); a.stop(tn + 0.1);
      registerVoice([a, g, p], tn + 0.1);
    });
  },

  powerdown(t0, o) {
    const notes = [2, 1.5, 1.19, 1];
    const base = 420 * o.pitch;
    notes.forEach((mult, i) => {
      const tn = t0 + i * 0.06;
      const { g, p } = outChain(o.pan, 0.4 * o.gain);
      const a = osc('sawtooth', base * mult, tn, o.det);
      envGain(g, tn, { attack: 0.003, decay: 0.1, sustain: 0.0001, peak: 0.7 });
      a.connect(g);
      a.start(tn); a.stop(tn + 0.11);
      registerVoice([a, g, p], tn + 0.11);
    });
  },

  multiUp(t0, o) {
    const chord = [1, 1.25, 1.5];
    chord.forEach((mult) => {
      const { g, p } = outChain(o.pan, 0.3 * o.gain);
      const a = osc('sine', 523 * o.pitch * mult, t0, o.det);
      envGain(g, t0, { attack: 0.005, decay: 0.35, sustain: 0.0001, peak: 0.7 });
      a.connect(g);
      a.start(t0); a.stop(t0 + 0.36);
      registerVoice([a, g, p], t0 + 0.36);
    });
  },

  atticEnter(t0, o) {
    const { g, p } = outChain(o.pan, 0.7 * o.gain);
    const n = noiseSrc(0.6);
    const bp = ctx.createBiquadFilter(); bp.type = 'bandpass'; bp.Q.value = 0.7;
    bp.frequency.setValueAtTime(300, t0);
    bp.frequency.exponentialRampToValueAtTime(5000, t0 + 0.55);
    envGain(g, t0, { attack: 0.05, decay: 0.55, sustain: 0.0001, peak: 0.8 });
    n.connect(bp); bp.connect(g);
    n.start(t0); n.stop(t0 + 0.6);
    const chord = [1, 1.2, 1.5, 2];
    chord.forEach((mult) => {
      const { g: cg, p: cp } = outChain(o.pan, 0.25 * o.gain);
      const a = osc('sawtooth', 220 * o.pitch * mult, t0 + 0.1, o.det);
      envGain(cg, t0 + 0.1, { attack: 0.15, decay: 0.5, sustain: 0.2, peak: 0.6 });
      a.connect(cg);
      a.start(t0 + 0.1); a.stop(t0 + 0.7);
      registerVoice([a, cg, cp], t0 + 0.7);
    });
    registerVoice([n, bp, g, p], t0 + 0.6);
  },

  atticLoop(t0, o) {
    const { g, p } = outChain(o.pan, 0.3 * o.gain);
    const a = osc('sine', 1600 * o.pitch, t0, o.det);
    const lfo = ctx.createOscillator(); lfo.frequency.value = 7;
    const lfoGain = ctx.createGain(); lfoGain.gain.value = 400;
    lfo.connect(lfoGain); lfoGain.connect(a.frequency);
    envGain(g, t0, { attack: 0.02, decay: 0.2, sustain: 0.0001, peak: 0.5 });
    a.connect(g);
    a.start(t0); a.stop(t0 + 0.22);
    lfo.start(t0); lfo.stop(t0 + 0.22);
    registerVoice([a, g, p, lfo, lfoGain], t0 + 0.22);
  },

  atticExit(t0, o) {
    const { g, p } = outChain(o.pan, 0.5 * o.gain);
    const a = osc('sawtooth', 900 * o.pitch, t0, o.det);
    a.frequency.exponentialRampToValueAtTime(150 * o.pitch, t0 + 0.3);
    envGain(g, t0, { attack: 0.01, decay: 0.3, sustain: 0.0001, peak: 0.7 });
    a.connect(g);
    a.start(t0); a.stop(t0 + 0.32);
    registerVoice([a, g, p], t0 + 0.32);
  },

  life(t0, o) {
    const notes = [1, 1.5];
    notes.forEach((mult, i) => {
      const tn = t0 + i * 0.11;
      const { g, p } = outChain(o.pan, 0.5 * o.gain);
      const a = osc('sine', 660 * o.pitch * mult, tn, o.det);
      envGain(g, tn, { attack: 0.005, decay: 0.2, sustain: 0.0001, peak: 0.8 });
      a.connect(g);
      a.start(tn); a.stop(tn + 0.22);
      registerVoice([a, g, p], tn + 0.22);
    });
  },

  death(t0, o) {
    const { g, p } = outChain(o.pan, 0.7 * o.gain);
    const a = osc('sawtooth', 240 * o.pitch, t0, 8);
    const b = osc('sawtooth', 236 * o.pitch, t0, -8);
    a.frequency.exponentialRampToValueAtTime(45, t0 + 0.6);
    b.frequency.exponentialRampToValueAtTime(42, t0 + 0.6);
    const shaper = ctx.createWaveShaper();
    shaper.curve = makeDistortionCurve(30);
    envGain(g, t0, { attack: 0.01, decay: 0.6, sustain: 0.0001, peak: 0.9 });
    a.connect(shaper); b.connect(shaper); shaper.connect(g);
    a.start(t0); a.stop(t0 + 0.62);
    b.start(t0); b.stop(t0 + 0.62);
    registerVoice([a, b, shaper, g, p], t0 + 0.62);
  },

  levelStart(t0, o) {
    const chord = [1, 1.25, 1.5];
    chord.forEach((mult) => {
      const { g, p } = outChain(o.pan, 0.35 * o.gain);
      const a = osc('sawtooth', 261 * o.pitch * mult, t0, o.det);
      envGain(g, t0, { attack: 0.01, decay: 0.4, sustain: 0.0001, peak: 0.7 });
      a.connect(g);
      a.start(t0); a.stop(t0 + 0.42);
      registerVoice([a, g, p], t0 + 0.42);
    });
    const n = noiseSrc(0.15);
    const { g: ng, p: np } = outChain(o.pan, 0.3 * o.gain);
    n.connect(ng);
    n.start(t0); n.stop(t0 + 0.15);
    registerVoice([n, ng, np], t0 + 0.15);
  },

  levelClear(t0, o) {
    const scale = [1, 1.125, 1.25, 1.5, 1.667, 2];
    scale.forEach((mult, i) => {
      const tn = t0 + i * 0.09;
      const { g, p } = outChain(o.pan, 0.4 * o.gain);
      const a = osc('triangle', 392 * o.pitch * mult, tn, o.det);
      envGain(g, tn, { attack: 0.004, decay: 0.25, sustain: 0.0001, peak: 0.85 });
      a.connect(g);
      a.start(tn); a.stop(tn + 0.26);
      registerVoice([a, g, p], tn + 0.26);
    });
    const { g: cg, p: cp } = outChain(o.pan, 0.3 * o.gain);
    const chordFreqs = [1, 1.25, 1.5, 2];
    chordFreqs.forEach((mult) => {
      const a = osc('sawtooth', 392 * o.pitch * mult, t0 + 0.5, o.det);
      a.connect(cg);
      a.start(t0 + 0.5); a.stop(t0 + 1.1);
      registerVoice([a], t0 + 1.1);
    });
    envGain(cg, t0 + 0.5, { attack: 0.02, decay: 0.6, sustain: 0.3, peak: 0.6 });
    registerVoice([cg, cp], t0 + 1.1);
  },

  uiClick(t0, o) {
    const { g, p } = outChain(o.pan, 0.35 * o.gain);
    const a = osc('triangle', 900 * o.pitch, t0, o.det);
    envGain(g, t0, { attack: 0.001, decay: 0.03, sustain: 0.0001, peak: 0.6 });
    a.connect(g);
    a.start(t0); a.stop(t0 + 0.04);
    registerVoice([a, g, p], t0 + 0.04);
  },

  laser(t0, o) {
    const { g, p } = outChain(o.pan, 0.5 * o.gain);
    const a = osc('sawtooth', 1800 * o.pitch, t0, o.det);
    a.frequency.exponentialRampToValueAtTime(180 * o.pitch, t0 + 0.12);
    const shaper = ctx.createWaveShaper();
    shaper.curve = makeDistortionCurve(8);
    envGain(g, t0, { attack: 0.001, decay: 0.12, sustain: 0.0001, peak: 0.7 });
    a.connect(shaper); shaper.connect(g);
    a.start(t0); a.stop(t0 + 0.13);
    registerVoice([a, shaper, g, p], t0 + 0.13);
  },

  split(t0, o) {
    [-1, 1].forEach((dir, i) => {
      const tn = t0 + i * 0.02;
      const { g, p } = outChain(clamp((o.pan || 0) + dir * 0.4, -1, 1), 0.4 * o.gain);
      const a = osc('triangle', 700 * o.pitch * (1 + dir * 0.08), tn, o.det);
      envGain(g, tn, { attack: 0.002, decay: 0.08, sustain: 0.0001, peak: 0.75 });
      a.connect(g);
      a.start(tn); a.stop(tn + 0.09);
      registerVoice([a, g, p], tn + 0.09);
    });
  },

  superball(t0, o) {
    const { g, p } = outChain(o.pan, 0.7 * o.gain);
    const chord = [1, 1.5, 2, 2.5];
    const shaper = ctx.createWaveShaper();
    shaper.curve = makeDistortionCurve(15);
    chord.forEach((mult) => {
      const a = osc('sawtooth', 130 * o.pitch * mult, t0, o.det);
      a.connect(shaper);
      a.start(t0); a.stop(t0 + 0.45);
      registerVoice([a], t0 + 0.45);
    });
    envGain(g, t0, { attack: 0.01, decay: 0.45, sustain: 0.0001, peak: 0.9 });
    shaper.connect(g);
    registerVoice([shaper, g, p], t0 + 0.45);
  },

  timeWarp(t0, o) {
    const { g, p } = outChain(o.pan, 0.5 * o.gain);
    const a = osc('sine', 300 * o.pitch, t0, o.det);
    a.frequency.exponentialRampToValueAtTime(70 * o.pitch, t0 + 0.4);
    a.frequency.exponentialRampToValueAtTime(300 * o.pitch, t0 + 0.8);
    const lp = ctx.createBiquadFilter(); lp.type = 'lowpass';
    lp.frequency.setValueAtTime(3000, t0);
    lp.frequency.exponentialRampToValueAtTime(300, t0 + 0.4);
    lp.frequency.exponentialRampToValueAtTime(3000, t0 + 0.8);
    envGain(g, t0, { attack: 0.02, decay: 0.78, sustain: 0.0001, peak: 0.75 });
    a.connect(lp); lp.connect(g);
    a.start(t0); a.stop(t0 + 0.82);
    registerVoice([a, lp, g, p], t0 + 0.82);
  },

  combo(t0, o) {
    const { g, p } = outChain(o.pan, 0.45 * o.gain);
    const a = osc('triangle', 700 * o.pitch, t0, o.det);
    envGain(g, t0, { attack: 0.001, decay: 0.07, sustain: 0.0001, peak: 0.8 });
    a.connect(g);
    a.start(t0); a.stop(t0 + 0.08);
    registerVoice([a, g, p], t0 + 0.08);
  },

  // -- escalation / juice sfx added for the tier system -----------------------

  /** Big rising stinger, more triumphant the higher opts.tier (1..5) is. */
  tierUp(t0, o) {
    const tier = clamp(Math.round(o.tier != null ? o.tier : 1), 1, 5);
    const baseFreq = 220 * o.pitch;
    const chordSemis = [0, 4, 7, 12, 16, 19].slice(0, 2 + tier);
    const dur = 0.45 + tier * 0.09;
    chordSemis.forEach((semi, i) => {
      const tn = t0 + i * 0.018;
      const { g, p } = outChain(o.pan, (0.32 + 0.09 * tier) * o.gain);
      const a = osc('sawtooth', baseFreq * Math.pow(2, semi / 12), tn, o.det);
      a.frequency.exponentialRampToValueAtTime(baseFreq * Math.pow(2, (semi + 12) / 12), tn + dur);
      envGain(g, tn, { attack: 0.008, decay: dur, sustain: 0.16, peak: 0.85 });
      a.connect(g);
      a.start(tn); a.stop(tn + dur + 0.15);
      registerVoice([a, g, p], tn + dur + 0.15);
    });
    const n = noiseSrc(dur + 0.05);
    const bp = ctx.createBiquadFilter(); bp.type = 'bandpass'; bp.Q.value = 0.9;
    bp.frequency.setValueAtTime(220, t0);
    bp.frequency.exponentialRampToValueAtTime(3500 + tier * 1400, t0 + dur);
    const { g: ng, p: np } = outChain(o.pan, (0.28 + 0.08 * tier) * o.gain);
    n.connect(bp); bp.connect(ng);
    n.start(t0); n.stop(t0 + dur + 0.05);
    registerVoice([n, bp, ng, np], t0 + dur + 0.05);
    // final hit bell for extra shine on the top tiers
    if (tier >= 4) {
      const { g: bg, p: bp2 } = outChain(o.pan, 0.4 * o.gain);
      const bell = osc('sine', baseFreq * 4, t0 + dur * 0.5, o.det);
      envGain(bg, t0 + dur * 0.5, { attack: 0.002, decay: 0.5, sustain: 0.0001, peak: 0.7 });
      bell.connect(bg);
      bell.start(t0 + dur * 0.5); bell.stop(t0 + dur + 0.6);
      registerVoice([bell, bg, bp2], t0 + dur + 0.6);
    }
    duckMusic(0.4 + tier * 0.06, 260 + tier * 60);
  },

  /** Satisfying whump + upward pitch bend for hitting the ball while the paddle moves up. opts.power 0..1. */
  uppercut(t0, o) {
    const power = clamp(o.power != null ? o.power : 0.5, 0, 1);
    const { g, p } = outChain(o.pan, (0.55 + 0.35 * power) * o.gain);
    const a = osc('sine', 65 * o.pitch, t0, o.det);
    a.frequency.exponentialRampToValueAtTime(230 * o.pitch * (1 + power * 0.7), t0 + 0.1 + power * 0.06);
    envGain(g, t0, { attack: 0.002, decay: 0.16 + power * 0.06, sustain: 0.0001, peak: 0.9 + 0.4 * power });
    a.connect(g);
    const n = noiseSrc(0.055);
    const hp = ctx.createBiquadFilter(); hp.type = 'highpass'; hp.frequency.value = 1000;
    const ng = ctx.createGain(); ng.gain.value = (0.28 + 0.32 * power) * o.gain;
    n.connect(hp); hp.connect(ng); ng.connect(p);
    a.start(t0); a.stop(t0 + 0.24);
    n.start(t0); n.stop(t0 + 0.055);
    registerVoice([a, g, p, n, hp, ng], t0 + 0.24);
    if (power > 0.45) duckMusic(0.18 * power, 150);
  },

  /** Very short thick punchy time-freeze impact. */
  hitstop(t0, o) {
    const { g, p } = outChain(o.pan, 0.85 * o.gain);
    const n = noiseSrc(0.035);
    const lp = ctx.createBiquadFilter(); lp.type = 'lowpass'; lp.frequency.value = 1700;
    const shaper = ctx.createWaveShaper(); shaper.curve = makeDistortionCurve(24);
    envGain(g, t0, { attack: 0.001, decay: 0.045, sustain: 0.0001, peak: 1 });
    n.connect(lp); lp.connect(shaper); shaper.connect(g);
    const sub = osc('sine', 55 * o.pitch, t0);
    const sg = ctx.createGain();
    envGain(sg, t0, { attack: 0.001, decay: 0.06, sustain: 0.0001, peak: 0.85 });
    sub.connect(sg); sg.connect(p);
    n.start(t0); n.stop(t0 + 0.035);
    sub.start(t0); sub.stop(t0 + 0.07);
    registerVoice([n, lp, shaper, g, p, sub, sg], t0 + 0.07);
  },

  /** Protective shimmer + thud for a shield save at the death line. */
  shieldSave(t0, o) {
    const { g, p } = outChain(o.pan, 0.5 * o.gain);
    const a = osc('sine', 900 * o.pitch, t0, o.det);
    const lfo = ctx.createOscillator(); lfo.frequency.value = 9;
    const lfoGain = ctx.createGain(); lfoGain.gain.value = 250;
    lfo.connect(lfoGain); lfoGain.connect(a.frequency);
    envGain(g, t0, { attack: 0.02, decay: 0.35, sustain: 0.1, peak: 0.6 });
    a.connect(g);
    const thump = osc('sine', 110 * o.pitch, t0 + 0.02);
    const tg = ctx.createGain();
    envGain(tg, t0 + 0.02, { attack: 0.004, decay: 0.2, sustain: 0.0001, peak: 0.9 });
    thump.connect(tg); tg.connect(p);
    a.start(t0); a.stop(t0 + 0.4); lfo.start(t0); lfo.stop(t0 + 0.4);
    thump.start(t0 + 0.02); thump.stop(t0 + 0.25);
    registerVoice([a, g, p, lfo, lfoGain, thump, tg], t0 + 0.4);
  },

  /** Pleasant ascending chime for catching a good powerup. */
  dropCatch(t0, o) {
    const notes = [1, 1.25, 1.6];
    notes.forEach((m, i) => {
      const tn = t0 + i * 0.045;
      const { g, p } = outChain(o.pan, 0.4 * o.gain);
      const a = osc('sine', 600 * o.pitch * m, tn, o.det);
      envGain(g, tn, { attack: 0.003, decay: 0.14, sustain: 0.0001, peak: 0.75 });
      a.connect(g);
      a.start(tn); a.stop(tn + 0.15);
      registerVoice([a, g, p], tn + 0.15);
    });
  },

  /** Nasty, unmistakable descending buzz for catching a hostile powerup. */
  dropBad(t0, o) {
    const { g, p } = outChain(o.pan, 0.6 * o.gain);
    const a = osc('sawtooth', 260 * o.pitch, t0, o.det);
    const b = osc('sawtooth', 253 * o.pitch, t0, -o.det);
    a.frequency.exponentialRampToValueAtTime(90 * o.pitch, t0 + 0.35);
    b.frequency.exponentialRampToValueAtTime(85 * o.pitch, t0 + 0.35);
    const shaper = ctx.createWaveShaper(); shaper.curve = makeDistortionCurve(35);
    envGain(g, t0, { attack: 0.004, decay: 0.35, sustain: 0.0001, peak: 0.9 });
    a.connect(shaper); b.connect(shaper); shaper.connect(g);
    const n = noiseSrc(0.1);
    const bp = ctx.createBiquadFilter(); bp.type = 'bandpass'; bp.frequency.value = 700; bp.Q.value = 6;
    const ng = ctx.createGain(); ng.gain.value = 0.3 * o.gain;
    n.connect(bp); bp.connect(ng); ng.connect(p);
    a.start(t0); a.stop(t0 + 0.36); b.start(t0); b.stop(t0 + 0.36);
    n.start(t0); n.stop(t0 + 0.1);
    registerVoice([a, b, shaper, g, p, n, bp, ng], t0 + 0.36);
  },

  /** Bright bell-like hit for an attic brick broken from above; opts.chain raises pitch up a scale. */
  brickAttic(t0, o) {
    const chain = Math.max(0, Math.round(o.chain || 0));
    const scale = [0, 2, 4, 7, 9, 12, 14, 16, 19];
    const semis = scale[Math.min(chain, scale.length - 1)];
    const freq = 900 * o.pitch * Math.pow(2, semis / 12);
    const { g, p } = outChain(o.pan, 0.5 * o.gain);
    const a = osc('sine', freq, t0, o.det);
    const b = osc('sine', freq * 2.01, t0, o.det);
    const bg = ctx.createGain(); bg.gain.value = 0.35;
    envGain(g, t0, { attack: 0.002, decay: 0.3, sustain: 0.0001, peak: 0.8 });
    envGain(bg, t0, { attack: 0.001, decay: 0.15, sustain: 0.0001, peak: 0.3 });
    a.connect(g); b.connect(bg); bg.connect(p);
    a.start(t0); a.stop(t0 + 0.32); b.start(t0); b.stop(t0 + 0.16);
    registerVoice([a, g, p, b, bg], t0 + 0.32);
  },

  /** Sad descending tone for losing the ball. */
  ballLost(t0, o) {
    const { g, p } = outChain(o.pan, 0.5 * o.gain);
    const a = osc('sawtooth', 320 * o.pitch, t0, o.det);
    a.frequency.exponentialRampToValueAtTime(60 * o.pitch, t0 + 0.5);
    const lp = ctx.createBiquadFilter(); lp.type = 'lowpass';
    lp.frequency.setValueAtTime(2500, t0);
    lp.frequency.exponentialRampToValueAtTime(200, t0 + 0.5);
    envGain(g, t0, { attack: 0.01, decay: 0.5, sustain: 0.0001, peak: 0.75 });
    a.connect(lp); lp.connect(g);
    a.start(t0); a.stop(t0 + 0.52);
    registerVoice([a, lp, g, p], t0 + 0.52);
  },

  /** Countdown beep; pass a rising opts.pitch for the final tick. */
  countdown(t0, o) {
    const { g, p } = outChain(o.pan, 0.5 * o.gain);
    const a = osc('square', 520 * o.pitch, t0, o.det);
    envGain(g, t0, { attack: 0.002, decay: 0.12, sustain: 0.0001, peak: 0.7 });
    a.connect(g);
    a.start(t0); a.stop(t0 + 0.13);
    registerVoice([a, g, p], t0 + 0.13);
  },

  /** Light UI/timer tick. */
  tick(t0, o) {
    const { g, p } = outChain(o.pan, 0.28 * o.gain);
    const a = osc('sine', 1200 * o.pitch, t0, o.det);
    envGain(g, t0, { attack: 0.001, decay: 0.02, sustain: 0.0001, peak: 0.5 });
    a.connect(g);
    a.start(t0); a.stop(t0 + 0.03);
    registerVoice([a, g, p], t0 + 0.03);
  },

  /** Subtle menu navigation move sound. */
  menuMove(t0, o) {
    const { g, p } = outChain(o.pan, 0.3 * o.gain);
    const a = osc('triangle', 500 * o.pitch, t0, o.det);
    a.frequency.exponentialRampToValueAtTime(650 * o.pitch, t0 + 0.05);
    envGain(g, t0, { attack: 0.001, decay: 0.05, sustain: 0.0001, peak: 0.5 });
    a.connect(g);
    a.start(t0); a.stop(t0 + 0.06);
    registerVoice([a, g, p], t0 + 0.06);
  },
};

/**
 * Play a one-shot sound effect by name. `opts` may include:
 * `{pitch:1, gain:1, pan:0, variation:0}`. Unknown names are silently
 * ignored. Voices are capped and identical names within a short window are
 * coalesced into a single louder hit so mass brick-breaks don't blow out
 * the mix.
 */
function sfx(name, opts = {}) {
  if (!supported) return;
  const builder = SFX[name];
  if (!builder) return;
  try {
    init();
    if (!started || !ctx) return;

    const t = (typeof performance !== 'undefined') ? performance.now() : Date.now();
    let rec = lastTrigger.get(name);
    if (!rec) {
      rec = { time: -Infinity, skipped: 0 };
      lastTrigger.set(name, rec);
    }
    let boost = 0;
    if (t - rec.time < RATE_LIMIT_MS) {
      rec.skipped = Math.min(rec.skipped + 1, 16);
      return; // coalesced away; the next allowed trigger gets boosted
    }
    boost = rec.skipped;
    rec.time = t;
    rec.skipped = 0;

    if (!canSpawnVoice()) return;

    const variation = clamp(opts.variation != null ? opts.variation : 0.12, 0, 1);
    const det = rand(-variation * 25, variation * 25);
    const pitchJitter = 1 + rand(-variation * 0.04, variation * 0.04);
    const o = {
      pitch: (opts.pitch != null ? opts.pitch : 1) * pitchJitter,
      gain: clamp((opts.gain != null ? opts.gain : 1) * (1 + Math.min(boost * 0.12, 1.3)), 0, 4),
      pan: clamp(opts.pan != null ? opts.pan : 0, -1, 1),
      variation,
      det,
    };
    const t0 = now() + 0.001;
    builder(t0, o);
    const duckBase = DUCK_NAMES[name];
    if (duckBase != null) {
      duckMusic(duckBase * clamp(o.gain, 0, 2), 200 + boost * 30);
    } else if (boost > 3) {
      // A big coalesced burst of an otherwise-quiet sfx (e.g. 40 bricks at
      // once) still deserves a small duck so the mix doesn't turn to mush.
      duckMusic(Math.min(0.3, boost * 0.03), 180);
    }
  } catch (e) {
    // Never throw from sfx().
  }
}

// ---------------------------------------------------------------------------
// Adaptive music engine — lookahead scheduler (Chris Wilson pattern), not
// setInterval-per-note. Layers fade in with intensity; an extra shimmer /
// double-time hat layer kicks in when setAttic(true).
// ---------------------------------------------------------------------------

const BPM = 128;
const QUARTER = 60 / BPM;          // seconds per quarter note
const SIXTEENTH = QUARTER / 4;
const STEPS_PER_BAR = 16;
const LOOKAHEAD_MS = 25;
const SCHEDULE_AHEAD = 0.12;       // seconds

// Natural minor / dorian roots for a moody main mode, brighter mode for attic.
const ROOT = 220; // A3
const SCALES = {
  minor: [0, 2, 3, 5, 7, 8, 10],
  dorian: [0, 2, 3, 5, 7, 9, 10],
  lydian: [0, 2, 4, 6, 7, 9, 11],   // brighter attic mode
};

// Chord progressions expressed as scale-degree triads (0-indexed).
const PROGRESSIONS = {
  calm: [[0, 2, 4], [3, 5, 0], [4, 6, 1], [5, 0, 2]],
  attic: [[0, 2, 4], [4, 6, 1], [5, 0, 2], [3, 5, 0]],
};

function degreeFreq(scaleName, degree, octave = 0) {
  const scale = SCALES[scaleName] || SCALES.minor;
  const len = scale.length;
  const idx = ((degree % len) + len) % len;
  const octShift = Math.floor(degree / len) + octave;
  const semis = scale[idx] + octShift * 12;
  return ROOT * Math.pow(2, semis / 12);
}

const music = {
  playing: false,
  mode: 'minor',
  bar: 0,
  step: 0,
  nextNoteTime: 0,
  attic: false,          // applied (bar-aligned) attic flag used by pattern logic
  pendingAttic: false,   // requested via setAttic(), applied at next bar boundary
  intensity: 0,
  targetIntensity: 0,
  chordIdx: 0,

  tier: 0,               // applied (bar-aligned) escalation tier 0..5
  pendingTier: 0,        // requested via setTier(), applied at next bar boundary
  rootOffset: 0,         // semitone offset applied at tier>=4 (whole-step modulation)

  transitionLog: [],     // [{time, bar, tier, attic}] recent applied transitions (debug/tests)

  gains: null,      // { sub, perc, bass, arp, pad, highpad, shimmer, stab }
  schedulerTimer: null,
  beatCheckerRunning: false,
  beatQueue: [],     // [{time, index}]
  beatCallbacks: [],
  lastQuarterTime: 0,
  quarterIndex: 0,
};

function ensureMusicGraph() {
  if (music.gains || !ctx) return;
  const mk = (v) => { const g = ctx.createGain(); g.gain.value = v; g.connect(musicBus); return g; };
  music.gains = {
    sub: mk(0),
    perc: mk(0),
    bass: mk(0),
    arp: mk(0),
    pad: mk(0),
    highpad: mk(0),
    shimmer: mk(0),
    stab: mk(0),
  };
  // High-pass the pad layer so it never builds up low-mid mud under the
  // bass/sub layers during heavy play; it only needs to add air/width.
  const padHP = ctx.createBiquadFilter();
  padHP.type = 'highpass';
  padHP.frequency.value = 260;
  music.gains.pad.disconnect();
  music.gains.pad.connect(padHP);
  padHP.connect(musicBus);
  music._padHP = padHP;
  // The tier-4+ shimmering high pad gets its own steeper highpass so it
  // stays airy and never masks the lead/arp.
  const highpadHP = ctx.createBiquadFilter();
  highpadHP.type = 'highpass';
  highpadHP.frequency.value = 900;
  music.gains.highpad.disconnect();
  music.gains.highpad.connect(highpadHP);
  highpadHP.connect(musicBus);
  music._highpadHP = highpadHP;
}

function scheduleGainTargets() {
  if (!music.gains) return;
  const i = music.intensity;
  const tier = music.tier;
  // Tiers put a floor under the intensity-driven mix so the arrangement
  // still visibly thickens even if gameplay intensity dips momentarily.
  const eff = Math.max(i, tier / 5 * 0.85);
  const t = now();
  const tc = 1.0; // ~1s smoothing per spec
  const set = (node, v) => { try { node.gain.setTargetAtTime(v, t, tc); } catch (e) {} };
  set(music.gains.sub, 0.5 + 0.3 * eff);
  set(music.gains.perc, clamp((eff - 0.05) / 0.5, 0, 1) * 0.55);
  set(music.gains.bass, clamp((eff - 0.2) / 0.5, 0, 1) * 0.5);
  set(music.gains.arp, clamp((eff - 0.4) / 0.5, 0, 1) * 0.4);
  set(music.gains.pad, clamp((eff - 0.55) / 0.5, 0, 1) * 0.35);
  set(music.gains.shimmer, (music.attic ? clamp((eff - 0.3) / 0.6, 0, 1) : 0) * 0.35);
  set(music.gains.highpad, (tier >= 4 ? clamp((eff - 0.3) / 0.6, 0, 1) : 0) * 0.3);
  set(music.gains.stab, tier >= 5 ? 0.4 : 0);
}


// --- note builders (each is fire-and-forget, self-cleaning) -----------------

function playKick(t, gainMul = 1) {
  if (!canSpawnVoice()) return;
  const g = ctx.createGain();
  g.connect(music.gains.perc);
  const o = ctx.createOscillator();
  o.type = 'sine';
  o.frequency.setValueAtTime(150, t);
  o.frequency.exponentialRampToValueAtTime(45, t + 0.12);
  envGain(g, t, { attack: 0.001, decay: 0.16, sustain: 0.0001, peak: 1 * gainMul });
  o.connect(g);
  o.start(t); o.stop(t + 0.2);
  registerVoice([o, g], t + 0.2);
}

function playSnare(t) {
  if (!canSpawnVoice()) return;
  const g = ctx.createGain();
  g.connect(music.gains.perc);
  const n = noiseSrc(0.15);
  const bp = ctx.createBiquadFilter(); bp.type = 'bandpass'; bp.frequency.value = 1800; bp.Q.value = 0.8;
  envGain(g, t, { attack: 0.001, decay: 0.12, sustain: 0.0001, peak: 0.7 });
  n.connect(bp); bp.connect(g);
  n.start(t); n.stop(t + 0.15);
  registerVoice([n, bp, g], t + 0.15);
}

function playHat(t, open = false, gainMul = 1) {
  if (!canSpawnVoice()) return;
  const g = ctx.createGain();
  g.connect(music.gains.perc);
  const n = noiseSrc(open ? 0.16 : 0.05);
  const hp = ctx.createBiquadFilter(); hp.type = 'highpass'; hp.frequency.value = 7000;
  envGain(g, t, { attack: 0.001, decay: open ? 0.15 : 0.04, sustain: 0.0001, peak: 0.35 * gainMul });
  n.connect(hp); hp.connect(g);
  n.start(t); n.stop(t + (open ? 0.16 : 0.05));
  registerVoice([n, hp, g], t + (open ? 0.16 : 0.05));
}

function playSub(t, freq, gainMul = 1) {
  if (!canSpawnVoice()) return;
  const g = ctx.createGain();
  g.connect(music.gains.sub);
  const o = ctx.createOscillator();
  o.type = 'sine';
  o.frequency.value = freq / 2;
  envGain(g, t, { attack: 0.01, decay: 0.4, sustain: 0.15, peak: 0.8 * gainMul });
  g.gain.setTargetAtTime(0.0001, t + 0.3, 0.15);
  o.connect(g);
  o.start(t); o.stop(t + 0.9);
  registerVoice([o, g], t + 0.9);
}

function playBass(t, freq, dur, gainMul = 1) {
  if (!canSpawnVoice()) return;
  const g = ctx.createGain();
  g.connect(music.gains.bass);
  const o = ctx.createOscillator();
  o.type = 'sawtooth';
  o.frequency.value = freq;
  const lp = ctx.createBiquadFilter(); lp.type = 'lowpass'; lp.frequency.value = 900;
  envGain(g, t, { attack: 0.01, decay: dur * 0.8, sustain: 0.2, peak: 0.6 * gainMul });
  g.gain.setTargetAtTime(0.0001, t + dur * 0.6, 0.1);
  o.connect(lp); lp.connect(g);
  o.start(t); o.stop(t + dur + 0.2);
  registerVoice([o, lp, g], t + dur + 0.2);
}

function playPluck(t, freq, gainMul = 1) {
  if (!canSpawnVoice()) return;
  const g = ctx.createGain();
  g.connect(music.gains.arp);
  const o = ctx.createOscillator();
  o.type = 'triangle';
  o.frequency.value = freq;
  envGain(g, t, { attack: 0.002, decay: 0.18, sustain: 0.0001, peak: 0.5 * gainMul });
  o.connect(g);
  o.start(t); o.stop(t + 0.2);
  registerVoice([o, g], t + 0.2);
}

function playPad(t, freqs, dur) {
  freqs.forEach((f) => {
    if (!canSpawnVoice()) return;
    const g = ctx.createGain();
    g.connect(music.gains.pad);
    const o = ctx.createOscillator();
    o.type = 'sine';
    o.frequency.value = f;
    envGain(g, t, { attack: dur * 0.3, decay: dur * 0.5, sustain: 0.25, peak: 0.28 });
    g.gain.setTargetAtTime(0.0001, t + dur * 0.7, dur * 0.3);
    o.connect(g);
    o.start(t); o.stop(t + dur + 0.3);
    registerVoice([o, g], t + dur + 0.3);
  });
}

function playShimmer(t, freq) {
  if (!canSpawnVoice()) return;
  const g = ctx.createGain();
  g.connect(music.gains.shimmer);
  const o = ctx.createOscillator();
  o.type = 'sine';
  o.frequency.value = freq * 4;
  envGain(g, t, { attack: 0.02, decay: 0.4, sustain: 0.05, peak: 0.3 });
  o.connect(g);
  o.start(t); o.stop(t + 0.5);
  registerVoice([o, g], t + 0.5);
}

/** Tier 4+ shimmering high pad: an airy octave-up sustained layer, always
 *  routed through its own steep highpass (see ensureMusicGraph) so it can
 *  never muddy the low end even while everything else is playing. */
function playHighPad(t, freqs, dur) {
  freqs.forEach((f) => {
    if (!canSpawnVoice()) return;
    const g = ctx.createGain();
    g.connect(music.gains.highpad);
    const o = ctx.createOscillator();
    o.type = 'sine';
    o.frequency.value = f * 2;
    const lfo = ctx.createOscillator(); lfo.frequency.value = 5.5;
    const lfoGain = ctx.createGain(); lfoGain.gain.value = 6;
    lfo.connect(lfoGain); lfoGain.connect(o.detune);
    envGain(g, t, { attack: dur * 0.4, decay: dur * 0.4, sustain: 0.18, peak: 0.22 });
    g.gain.setTargetAtTime(0.0001, t + dur * 0.7, dur * 0.3);
    o.connect(g);
    o.start(t); lfo.start(t);
    o.stop(t + dur + 0.3); lfo.stop(t + dur + 0.3);
    registerVoice([o, g, lfo, lfoGain], t + dur + 0.3);
  });
}

/** Tier 5 rave stab: a short filtered, saturated chord hit on the offbeats. */
function playRaveStab(t, freqs) {
  if (!canSpawnVoice()) return;
  const g = ctx.createGain();
  g.connect(music.gains.stab);
  const shaper = ctx.createWaveShaper(); shaper.curve = makeDistortionCurve(10);
  const lp = ctx.createBiquadFilter(); lp.type = 'lowpass';
  lp.frequency.setValueAtTime(5200, t);
  lp.frequency.exponentialRampToValueAtTime(700, t + 0.16);
  envGain(g, t, { attack: 0.002, decay: 0.14, sustain: 0.0001, peak: 0.8 });
  freqs.forEach((f) => {
    if (!canSpawnVoice()) return;
    const o = ctx.createOscillator();
    o.type = 'sawtooth';
    o.frequency.value = f;
    o.connect(shaper);
    o.start(t); o.stop(t + 0.16);
    registerVoice([o], t + 0.16);
  });
  shaper.connect(lp); lp.connect(g);
  registerVoice([shaper, lp, g], t + 0.16);
}

// --- scheduler ---------------------------------------------------------------

function scheduleStep(stepIndex, t) {
  const tier = music.tier;
  const attic = music.attic;
  const scaleName = attic ? 'lydian' : music.mode;
  const prog = attic ? PROGRESSIONS.attic : PROGRESSIONS.calm;
  const bar = music.bar;
  const chord = prog[bar % prog.length];
  const rootMul = Math.pow(2, music.rootOffset / 12);
  const arpPattern = [0, 1, 2, 1];

  // quarter-note beat tracking for beatPhase()/onBeat()
  if (stepIndex % 4 === 0) {
    music.beatQueue.push({ time: t, index: music.quarterIndex });
    music.quarterIndex++;
  }

  // Layer 1: sub bass pulse on beat 1 and 3, tier2+ adds an octave-doubled hit
  if (stepIndex % 8 === 0) {
    playSub(t, degreeFreq(scaleName, chord[0], -1) * rootMul);
  }
  if (tier >= 2 && stepIndex % 8 === 4) {
    playSub(t, degreeFreq(scaleName, chord[0], -2) * rootMul, 0.55);
  }

  // Layer 2: percussion — tier1+ doubles the kick, attic/tier5 doubles hats
  const kickPattern = [0, 0, 0, 1, 0, 0, 1, 0, 0, 0, 0, 1, 0, 1, 0, 0];
  if (kickPattern[stepIndex]) playKick(t);
  if (tier >= 1 && stepIndex % 4 === 2) playKick(t, 0.55);
  if (stepIndex === 4 || stepIndex === 12) playSnare(t);
  const doubleTime = attic || tier >= 5;
  if (doubleTime || stepIndex % 2 === 0) {
    playHat(t, stepIndex % 8 === 6, tier >= 1 ? 1.15 : 1);
  }

  // Layer 3: bass line, one note per beat; tier2+ adds an octave-up doubling
  if (stepIndex % 4 === 0) {
    const deg = chord[(Math.floor(stepIndex / 4)) % chord.length];
    playBass(t, degreeFreq(scaleName, deg, -1) * rootMul, QUARTER * 0.9);
    if (tier >= 2) {
      playBass(t, degreeFreq(scaleName, deg, 0) * rootMul, QUARTER * 0.9, 0.45);
    }
  }

  // Layer 4: arpeggio / pluck lead. Below tier3 it's 8th notes; tier3+ (or
  // attic) it's full 16ths plus a higher counter-melody voice.
  const denseArp = tier >= 3 || attic;
  if (denseArp || stepIndex % 2 === 0) {
    const deg = chord[arpPattern[stepIndex % 4]];
    playPluck(t, degreeFreq(scaleName, deg, 1) * rootMul, 1);
  }
  if (tier >= 3) {
    const cdeg = chord[arpPattern[(stepIndex + 2) % 4]];
    playPluck(t + SIXTEENTH * 0.5, degreeFreq(scaleName, cdeg + 2, 2) * rootMul, 0.5);
  }
  if (attic) {
    // extra octave-up counter-melody (legacy attic behaviour, kept)
    const deg = chord[arpPattern[stepIndex % 4]];
    playPluck(t + SIXTEENTH * 0.5, degreeFreq(scaleName, deg + 2, 2) * rootMul, 0.6);
  }

  // Layer 5: pad chords, once per bar; tier4+ modulates up a whole step
  // (rootOffset) and layers a shimmering high pad on top.
  if (stepIndex === 0) {
    const freqs = chord.map((d) => degreeFreq(scaleName, d, 0) * rootMul);
    playPad(t, freqs, QUARTER * 4);
    if (tier >= 4) playHighPad(t, freqs, QUARTER * 4);
  }

  // Layer 6: attic shimmer, offbeat sparkle
  if (attic && stepIndex % 2 === 1) {
    playShimmer(t, degreeFreq(scaleName, chord[2], 2) * rootMul);
  }

  // Layer 7: tier5 rave stab on the "and" of beats 1 and 3 — full double-time energy
  if (tier >= 5 && (stepIndex === 2 || stepIndex === 10)) {
    playRaveStab(t, chord.map((d) => degreeFreq(scaleName, d, 0) * rootMul));
  }
}

function applyPendingMusicChanges(t) {
  const tierChanged = music.pendingTier !== music.tier;
  const atticChanged = music.pendingAttic !== music.attic;
  if (!tierChanged && !atticChanged) return;
  music.tier = music.pendingTier;
  music.attic = music.pendingAttic;
  music.rootOffset = music.tier >= 4 ? 2 : 0;
  music.transitionLog.push({ time: t, bar: music.bar, tier: music.tier, attic: music.attic });
  if (music.transitionLog.length > 40) music.transitionLog.shift();
  try { scheduleGainTargets(); } catch (e) {}
}

function schedulerTick() {
  if (!music.playing || !ctx) return;
  while (music.nextNoteTime < ctx.currentTime + SCHEDULE_AHEAD) {
    scheduleStep(music.step, music.nextNoteTime);
    music.nextNoteTime += SIXTEENTH;
    music.step = (music.step + 1) % STEPS_PER_BAR;
    if (music.step === 0) {
      music.bar++;
      // evolve mode occasionally
      if (music.bar % 8 === 0) {
        music.mode = music.mode === 'minor' ? 'dorian' : 'minor';
      }
      // Tier/attic pattern changes only ever take effect on the next bar
      // boundary so a change never chops a note or chord mid-phrase.
      applyPendingMusicChanges(music.nextNoteTime);
    }
  }
}


function runBeatChecker() {
  if (music.beatCheckerRunning) return;
  music.beatCheckerRunning = true;
  const step = () => {
    if (!music.playing) { music.beatCheckerRunning = false; return; }
    const t = ctx ? ctx.currentTime : 0;
    while (music.beatQueue.length && music.beatQueue[0].time <= t) {
      const { index } = music.beatQueue.shift();
      music.lastQuarterTime = t;
      for (const cb of music.beatCallbacks) {
        try { cb(index); } catch (e) { /* ignore user cb errors */ }
      }
    }
    if (typeof requestAnimationFrame === 'function') {
      requestAnimationFrame(step);
    } else {
      setTimeout(step, 16);
    }
  };
  step();
}

/** Start the adaptive music engine (idempotent). */
function musicStart() {
  if (!supported) return;
  try {
    init();
    if (!ctx || !started) return;
    ensureMusicGraph();
    if (music.playing) return;
    music.playing = true;
    music.step = 0;
    music.bar = 0;
    music.quarterIndex = 0;
    music.beatQueue.length = 0;
    music.nextNoteTime = ctx.currentTime + 0.05;
    // Sync applied tier/attic to whatever was requested before start() so
    // the arrangement is correct from bar 0 instead of waiting a full bar.
    music.tier = music.pendingTier;
    music.attic = music.pendingAttic;
    music.rootOffset = music.tier >= 4 ? 2 : 0;
    if (music.schedulerTimer) clearInterval(music.schedulerTimer);
    music.schedulerTimer = setInterval(schedulerTick, LOOKAHEAD_MS);
    if (music.schedulerTimer && typeof music.schedulerTimer.unref === 'function') music.schedulerTimer.unref();
    scheduleGainTargets();
    runBeatChecker();
  } catch (e) { /* never throw */ }
}

/** Stop the adaptive music engine and silence all its layers. */
function musicStop() {
  try {
    music.playing = false;
    if (music.schedulerTimer) { clearInterval(music.schedulerTimer); music.schedulerTimer = null; }
    music.beatQueue.length = 0;
    if (music.gains) {
      const t = now();
      for (const key of Object.keys(music.gains)) {
        try { music.gains[key].gain.setTargetAtTime(0, t, 0.05); } catch (e) {}
      }
    }
  } catch (e) { /* never throw */ }
}

/** Set overall music intensity 0..1; smoothed internally over ~1s. */
function musicSetIntensity(v) {
  music.intensity = clamp(v, 0, 1);
  try { scheduleGainTargets(); } catch (e) {}
}

/** Tell the music engine whether a ball is in the attic (adds extra layer).
 *  Composes with tier: the pattern-affecting change is applied on the next
 *  bar boundary (see applyPendingMusicChanges); gain levels still fade
 *  smoothly immediately so it never feels laggy. */
function musicSetAttic(v) {
  music.pendingAttic = !!v;
  if (!music.playing) { music.attic = music.pendingAttic; }
  try { scheduleGainTargets(); } catch (e) {}
}

/**
 * Set the escalation tier 0..5. Each tier thickens the arrangement:
 *  1 = percussion doubles, 2 = bassline octaves, 3 = 16th arp + counter
 *  melody, 4 = whole-step modulation + shimmering high pad, 5 = full
 *  double-time hats + rave stab. The pattern change is deferred to the next
 *  bar boundary so it never chops a note or chord mid-phrase; gain levels
 *  still respond immediately for responsiveness.
 */
function musicSetTier(n) {
  music.pendingTier = clamp(Math.round(n), 0, 5);
  if (!music.playing) {
    music.tier = music.pendingTier;
    music.rootOffset = music.tier >= 4 ? 2 : 0;
  }
  try { scheduleGainTargets(); } catch (e) {}
}

/** Debug/test helper: last few bar-boundary tier/attic transitions with their scheduled AudioContext time. */
function musicDebugTransitions() {
  return music.transitionLog.slice();
}

/** Returns 0..1 progress within the current beat (quarter note), for visual sync. */
function musicBeatPhase() {
  if (!ctx || !music.playing) return 0;
  const t = ctx.currentTime;
  const sinceLast = t - music.lastQuarterTime;
  const phase = (sinceLast / QUARTER) % 1;
  return phase < 0 ? 0 : phase > 1 ? 1 : phase;
}

/** Register a callback fired near-frame-accurately on each quarter note: cb(beatIndex). */
function musicOnBeat(cb) {
  if (typeof cb !== 'function') return;
  music.beatCallbacks.push(cb);
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

/**
 * The Attic Breaker audio engine. See src/audio/audio.js header for the
 * full contract. All methods are safe no-ops if WebAudio is unavailable.
 */
export const Audio = {
  /** Lazily create the AudioContext / graph. Call from a user gesture; safe to call repeatedly. */
  init,
  /** True once the audio graph has been successfully built. */
  get ready() { return !!(supported && started && ctx); },
  /** Mute/unmute all output. */
  setMuted,
  /** Current muted state. */
  get muted() { return muted; },
  /** Set master output volume 0..1. */
  setMasterVolume,
  /** Set the one-shot sfx bus volume 0..1, independent of music/master. */
  setSfxVolume,
  /** Set the adaptive music bus volume 0..1, independent of sfx/master. */
  setMusicVolume,
  /** Suspend the AudioContext (e.g. on tab blur) to save CPU/battery. */
  suspend,
  /** Resume the AudioContext (e.g. on tab focus / after interruption). */
  resume,
  /** Test/debug only: post-limiter peak meter, see debugPeak() above. */
  debugPeak,
  /** Test/debug only: reset the running peak-hold tracked by debugPeak(). */
  debugResetPeak,
  /** Debug/HUD snapshot: {ready, muted, master, sfxVol, musicVol, voices, tier, intensity, attic, bpm}. */
  get state() {
    return {
      ready: !!(supported && started && ctx),
      muted,
      master: masterVolume,
      sfxVol: sfxVolume,
      musicVol: musicVolume,
      voices: liveVoices,
      tier: music.tier,
      intensity: music.intensity,
      attic: music.attic,
      bpm: BPM * (music.tier >= 5 ? 2 : 1),
    };
  },
  /** Play a one-shot named sfx. opts: {pitch, gain, pan, variation}. */
  sfx,
  music: {
    /** Start the generative adaptive music engine. */
    start: musicStart,
    /** Stop the music engine and fade all layers to silence. */
    stop: musicStop,
    /** Set music intensity 0 (calm) .. 1 (full chaos); smoothed ~1s. */
    setIntensity: musicSetIntensity,
    /** Tell music engine the ball is in the attic; adds shimmer + double hats. Bar-aligned. */
    setAttic: musicSetAttic,
    /** Set the escalation tier 0..5; thickens the arrangement layer by layer, bar-aligned. */
    setTier: musicSetTier,
    /** 0..1 phase within the current quarter note, for visual sync. */
    beatPhase: musicBeatPhase,
    /** Register cb(beatIndex) fired on each quarter note. */
    onBeat: musicOnBeat,
    /** Debug/test only: recent [{time, bar, tier, attic}] bar-boundary transitions. */
    debugTransitions: musicDebugTransitions,
  },
};


