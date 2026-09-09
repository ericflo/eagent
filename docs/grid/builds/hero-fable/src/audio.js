/**
 * OVERTOP — procedural audio (Web Audio API, zero asset files).
 *
 * Everything is synthesized on the fly: oscillators, a shared noise buffer,
 * biquad filters, gain envelopes and a small generated-impulse convolver reverb.
 *
 * Public contract (see DESIGN.md "Audio contract"):
 *   const audio = createAudio();
 *   audio.resume()                       // from a user gesture; lazily builds the AudioContext
 *   audio.setMuted(bool) / audio.muted
 *   audio.setIntensity(tier 0..6, onTop) // crossfades the background layers
 *   audio.paddleHit(strength, powerHit) audio.wallHit()
 *   audio.brickBreak(type, comboIndex, fromTop) audio.brickDeny(type)
 *   audio.bomb() audio.cascade() audio.powerup(kind) audio.powerupEnd() audio.laser()
 *   audio.ballLost() audio.tierUp(tier) audio.overtopEnter() audio.overtopExit()
 *   audio.levelClear() audio.gameOver() audio.uiClick()
 *   audio.update(dt)                     // per frame: beat clock + layer scheduler
 *   audio.beatPhase                      // 0..1, ~120 bpm, for visuals
 *
 * Every method is a safe no-op before resume() and never throws.
 *
 * Musical design: everything lives in C major pentatonic (C D E G A) so brick
 * notes, arpeggio and pad always agree. The beat clock runs at 120 bpm on the
 * AudioContext clock (a look-ahead scheduler on a 16th-note grid); before the
 * context exists the clock free-runs on dt so visuals can still pulse.
 */

const BPM = 120;
const BEAT = 60 / BPM;            // 0.5 s per beat
const STEP = BEAT / 4;            // scheduler grid: 16th notes (0.125 s)
const STEPS_PER_LOOP = 32;        // two bars of 4/4
const LOOKAHEAD = 0.2;            // seconds of audio scheduled ahead of "now"

const PENTA = [0, 2, 4, 7, 9];    // major pentatonic, semitones above the root
const C4 = 261.63;                // root of the note table (degree 0)
const MAX_VOICES = 56;            // soft cap on simultaneously sounding one-shots
const BURST_WINDOW = 0.1;         // seconds
const BURST_MAX = 12;             // max brickBreak voices per BURST_WINDOW

// Background layer levels per tier (0..6). Tier 0 is a whisper of pad.
const PAD_LEVEL = [0.035, 0.07, 0.09, 0.10, 0.11, 0.12, 0.13];
const BASS_LEVEL = [0, 0.12, 0.15, 0.18, 0.20, 0.22, 0.24];
const HAT_LEVEL = [0, 0, 0, 0.05, 0.06, 0.08, 0.10];
const ARP_LEVEL = (tier) => 0.09 + tier * 0.012;
const PAD_CUTOFF = (tier) => 350 + tier * 380;
const BASS_CUTOFF = (tier) => 160 + tier * 150;

// Bass line: semitone offsets from C2, one entry per 8th note, two bars.
// Low tiers only play the even entries (quarter notes); tier >= 3 plays all.
const BASS_PATTERN = [0, 0, 0, 7, 0, 0, 7, 9, 0, 0, 0, 7, 0, 0, 4, 7];

const clamp = (v, lo, hi) => Math.min(hi, Math.max(lo, v));
const mod1 = (v) => ((v % 1) + 1) % 1;

/** Frequency of pentatonic degree `deg` (0 = C4, 5 = C5, 10 = C6 ...). */
function pentaFreq(deg) {
  const d = Math.max(0, Math.floor(deg));
  const oct = Math.floor(d / PENTA.length);
  return C4 * Math.pow(2, oct + PENTA[d % PENTA.length] / 12);
}

/**
 * Brick-break note for a combo index: rises through the pentatonic scale,
 * then after ~3 octaves wraps back so it never turns into a dog whistle.
 */
function comboFreq(comboIndex) {
  const i = Math.max(0, Math.floor(comboIndex || 0));
  let oct = Math.floor(i / PENTA.length);
  if (oct > 2) oct = 1 + ((oct - 2) % 2); // wrap: 3->1, 4->2, 5->1 ...
  return C4 * Math.pow(2, oct + PENTA[i % PENTA.length] / 12);
}

export function createAudio() {
  // ---- context + shared graph -------------------------------------------
  let ctx = null;        // AudioContext (created lazily in resume())
  let bus = null;        // master gain (also used for mute) -> compressor -> out
  let reverbIn = null;   // send here for the generated-impulse reverb
  let noiseBuf = null;   // 2 s of white noise, shared by every noise burst

  let muted = false;
  let beatPhase = 0;
  let activeVoices = 0;
  let warnCount = 0;
  const burstTimes = [];  // recent brickBreak timestamps for thinning

  // Background layers (built once in buildLayers()).
  const L = {};           // gain nodes: pad, padHigh, bass, bassEnv, arp, hat
  let padFilter = null, bassFilter = null, bassOsc = null, bassSub = null;
  let curTier = -1, curOnTop = null;
  let arpFadeUntil = 0, arpStep = 0;

  // Scheduler state (all in AudioContext seconds).
  let gridOrigin = null, nextStepTime = 0, stepIndex = 0;

  /** Wrap a method so it can never throw into the game loop. */
  const safe = (fn) => (...args) => {
    try { return fn(...args); } catch (err) {
      if (warnCount++ < 5) console.warn('[audio]', err);
      return undefined;
    }
  };

  const now = () => ctx.currentTime;
  /** One-shots need a live context and are skipped while muted (saves CPU). */
  const live = () => !!ctx && !muted && ctx.state === 'running';
  /** Soft polyphony cap; `important` sounds always get through. */
  const hasVoice = (important) => important || activeVoices < MAX_VOICES;

  // ---- setup ---------------------------------------------------------------

  function resume() {
    if (!ctx) {
      const AC = (typeof window !== 'undefined') && (window.AudioContext || window.webkitAudioContext);
      if (!AC) return;
      ctx = new AC({ latencyHint: 'interactive' });
      buildMaster();
      buildLayers();
      // Apply any intensity the game requested before the context existed.
      if (curTier >= 0) { const t = curTier, top = curOnTop; curTier = -1; curOnTop = null; setIntensity(t, top); }
    }
    if (ctx.state === 'suspended' || ctx.state === 'interrupted') {
      const p = ctx.resume();
      if (p && p.catch) p.catch(() => {});
    }
  }

  /** master gain -> gentle limiter-style compressor -> speakers, plus reverb bus. */
  function buildMaster() {
    const comp = ctx.createDynamicsCompressor();
    comp.threshold.value = -12;
    comp.knee.value = 8;
    comp.ratio.value = 12;
    comp.attack.value = 0.002;
    comp.release.value = 0.15;
    comp.connect(ctx.destination);

    bus = ctx.createGain();
    bus.gain.value = muted ? 0 : 0.8;
    bus.connect(comp);

    // Reverb: short generated impulse, low-passed return so it stays soft.
    const conv = ctx.createConvolver();
    conv.buffer = makeImpulse(1.7, 3.2);
    const lp = ctx.createBiquadFilter();
    lp.type = 'lowpass'; lp.frequency.value = 4200;
    const ret = ctx.createGain();
    ret.gain.value = 0.35;
    reverbIn = ctx.createGain();
    reverbIn.connect(conv); conv.connect(lp); lp.connect(ret); ret.connect(bus);

    noiseBuf = makeNoise(2);
  }

  function makeImpulse(seconds, power) {
    const rate = ctx.sampleRate, len = Math.floor(rate * seconds);
    const buf = ctx.createBuffer(2, len, rate);
    for (let ch = 0; ch < 2; ch++) {
      const d = buf.getChannelData(ch);
      for (let i = 0; i < len; i++) d[i] = (Math.random() * 2 - 1) * Math.pow(1 - i / len, power);
    }
    return buf;
  }

  function makeNoise(seconds) {
    const len = Math.floor(ctx.sampleRate * seconds);
    const buf = ctx.createBuffer(1, len, ctx.sampleRate);
    const d = buf.getChannelData(0);
    for (let i = 0; i < len; i++) d[i] = Math.random() * 2 - 1;
    return buf;
  }

  /** Persistent background voices, all starting silent. */
  function buildLayers() {
    const t = now();
    const gain = (v, dest) => { const g = ctx.createGain(); g.gain.value = v; if (dest) g.connect(dest); return g; };
    const send = (from, amount) => { const s = gain(amount, reverbIn); from.connect(s); };

    // Pad: detuned saw/tri chord (Cadd9: C3 E3 G3 D4) through a slow-moving lowpass.
    L.pad = gain(0, bus); send(L.pad, 0.5);
    padFilter = ctx.createBiquadFilter();
    padFilter.type = 'lowpass'; padFilter.frequency.value = PAD_CUTOFF(0); padFilter.Q.value = 0.8;
    padFilter.connect(L.pad);
    const padVoices = [
      { f: 130.81, type: 'sawtooth', det: -7, g: 0.45 },
      { f: 196.00, type: 'sawtooth', det: 7, g: 0.35 },
      { f: 164.81, type: 'triangle', det: 0, g: 0.6 },
      { f: 293.66, type: 'triangle', det: 4, g: 0.35 },
    ];
    for (const v of padVoices) {
      const o = ctx.createOscillator();
      o.type = v.type; o.frequency.value = v.f; o.detune.value = v.det;
      o.connect(gain(v.g, padFilter)); o.start(t);
    }
    // Upper pad voices (G4, C5) that only open up at tier >= 4.
    L.padHigh = gain(0, padFilter);
    for (const v of [{ f: 392.0, det: -5, g: 0.3 }, { f: 523.25, det: 5, g: 0.25 }]) {
      const o = ctx.createOscillator();
      o.type = 'sine'; o.frequency.value = v.f; o.detune.value = v.det;
      o.connect(gain(v.g, L.padHigh)); o.start(t);
    }
    // Slow LFO wobbles the pad cutoff so it breathes.
    const lfo = ctx.createOscillator();
    lfo.frequency.value = 0.08;
    lfo.connect(gain(110, padFilter.frequency)); lfo.start(t);

    // Bass: saw + sine sub through a resonant lowpass, gated per note by bassEnv.
    L.bass = gain(0, bus);
    L.bassEnv = gain(0, L.bass);
    bassFilter = ctx.createBiquadFilter();
    bassFilter.type = 'lowpass'; bassFilter.frequency.value = BASS_CUTOFF(0); bassFilter.Q.value = 5;
    bassFilter.connect(L.bassEnv);
    bassOsc = ctx.createOscillator(); bassOsc.type = 'sawtooth'; bassOsc.frequency.value = 65.41;
    bassSub = ctx.createOscillator(); bassSub.type = 'sine'; bassSub.frequency.value = 65.41;
    bassOsc.connect(gain(0.7, bassFilter)); bassSub.connect(gain(0.6, bassFilter));
    bassOsc.start(t); bassSub.start(t);

    // Arpeggio + hats are transient voices routed through their own buses.
    L.arp = gain(0, bus); send(L.arp, 0.35);
    L.hat = gain(0, bus);
  }

  // ---- synthesis helpers ---------------------------------------------------

  /** Linear attack to `peak`, optional hold, exponential decay to silence. */
  function envelope(param, t0, attack, hold, decay, peak) {
    const p = Math.max(0.0001, peak);
    param.setValueAtTime(0, t0);
    param.linearRampToValueAtTime(p, t0 + attack);
    if (hold > 0) param.setValueAtTime(p, t0 + attack + hold);
    param.exponentialRampToValueAtTime(0.0004, t0 + attack + hold + decay);
  }

  /** Count a voice and tear the whole node chain down when the source ends. */
  function track(src, nodes) {
    activeVoices++;
    src.onended = () => {
      activeVoices--;
      for (const n of nodes) { try { n.disconnect(); } catch (_) { /* already gone */ } }
    };
  }

  /** Optional biquad in front of the envelope gain; supports a cutoff sweep. */
  function insertFilter(head, spec, t0, dur, extras) {
    if (!spec) return head;
    const f = ctx.createBiquadFilter();
    f.type = spec.type || 'lowpass';
    f.frequency.setValueAtTime(clamp(spec.freq, 20, 20000), t0);
    if (spec.endFreq) f.frequency.exponentialRampToValueAtTime(clamp(spec.endFreq, 20, 20000), t0 + (spec.sweep || dur));
    f.Q.value = spec.Q ?? 1;
    head.connect(f);
    extras.push(f);
    return f;
  }

  /**
   * One oscillator voice.
   * o = { freq, endFreq, sweep, type, detune, attack, hold, decay, gain,
   *       filter:{type,freq,endFreq,Q,sweep}, vibrato:{rate,depth}, send, when, dest }
   */
  function tone(o) {
    const t0 = o.when ?? now();
    const attack = Math.max(0.002, o.attack ?? 0.005);
    const hold = o.hold ?? 0;
    const decay = Math.max(0.01, o.decay ?? 0.2);
    const dur = attack + hold + decay;
    const extras = [];

    const osc = ctx.createOscillator();
    osc.type = o.type || 'sine';
    osc.frequency.setValueAtTime(clamp(o.freq, 20, 20000), t0);
    if (o.endFreq) osc.frequency.exponentialRampToValueAtTime(clamp(o.endFreq, 20, 20000), t0 + (o.sweep || dur));
    if (o.detune) osc.detune.setValueAtTime(o.detune, t0);
    if (o.vibrato) {
      const lfo = ctx.createOscillator(), depth = ctx.createGain();
      lfo.frequency.value = o.vibrato.rate; depth.gain.value = o.vibrato.depth;
      lfo.connect(depth); depth.connect(osc.frequency);
      lfo.start(t0); lfo.stop(t0 + dur + 0.05);
      extras.push(lfo, depth);
    }

    const head = insertFilter(osc, o.filter, t0, dur, extras);
    const g = ctx.createGain();
    envelope(g.gain, t0, attack, hold, decay, o.gain ?? 0.2);
    head.connect(g);
    g.connect(o.dest || bus);
    if (o.send) { const s = ctx.createGain(); s.gain.value = o.send; g.connect(s); s.connect(reverbIn); extras.push(s); }

    osc.start(t0);
    osc.stop(t0 + dur + 0.03);
    track(osc, [osc, g, ...extras]);
  }

  /** Filtered burst from the shared noise buffer. Same option names as tone(). */
  function noiseBurst(o) {
    const t0 = o.when ?? now();
    const attack = Math.max(0.001, o.attack ?? 0.002);
    const hold = o.hold ?? 0;
    const decay = Math.max(0.01, o.decay ?? 0.1);
    const dur = attack + hold + decay;
    const extras = [];

    const src = ctx.createBufferSource();
    src.buffer = noiseBuf; src.loop = true;
    const head = insertFilter(src, o.filter, t0, dur, extras);
    const g = ctx.createGain();
    envelope(g.gain, t0, attack, hold, decay, o.gain ?? 0.1);
    head.connect(g);
    g.connect(o.dest || bus);
    if (o.send) { const s = ctx.createGain(); s.gain.value = o.send; g.connect(s); s.connect(reverbIn); extras.push(s); }

    src.start(t0, Math.random() * (noiseBuf.duration - 0.5));
    src.stop(t0 + dur + 0.03);
    track(src, [src, g, ...extras]);
  }

  /** Several tones sharing options. */
  function chord(freqs, o) { for (const f of freqs) tone({ ...o, freq: f }); }

  /** Sequence of tones, `gap` seconds apart, optionally with per-note overrides. */
  function run(freqs, gap, o, perNote) {
    const t0 = o.when ?? now();
    freqs.forEach((f, i) => tone({ ...o, ...(perNote ? perNote(i, freqs.length) : null), freq: f, when: t0 + i * gap }));
  }

  /** Smoothly move an AudioParam (no clicks): ~63% of the way per `tc` seconds. */
  function glide(param, value, tc = 0.25) {
    const t = now();
    param.cancelScheduledValues(t);
    param.setTargetAtTime(value, t, tc);
  }

  // ---- background layers + beat clock ---------------------------------------

  function setMuted(flag) {
    muted = !!flag;
    if (bus) glide(bus.gain, muted ? 0 : 0.8, 0.03);
  }

  /**
   * Crossfade the background layers for a multiplier tier (0..6) and the
   * overtop state. Cheap to call every frame: only acts when something changes.
   */
  function setIntensity(tier, onTop) {
    const t = clamp(Math.round(tier || 0), 0, 6);
    const top = !!onTop;
    if (!ctx || (t === curTier && top === curOnTop)) { curTier = t; curOnTop = top; return; }
    const tierChanged = t !== curTier;
    curTier = t; curOnTop = top;

    if (tierChanged) {
      glide(L.pad.gain, PAD_LEVEL[t], 0.35);
      glide(L.padHigh.gain, t >= 4 ? 1 : 0, 0.8);
      glide(padFilter.frequency, PAD_CUTOFF(t), 0.4);
      glide(L.bass.gain, BASS_LEVEL[t], 0.3);
      glide(bassFilter.frequency, BASS_CUTOFF(t), 0.3);
      glide(L.hat.gain, HAT_LEVEL[t], 0.3);
    }
    if (top) {
      glide(L.arp.gain, ARP_LEVEL(t), 0.15);
    } else {
      glide(L.arp.gain, 0, 0.3);
      arpFadeUntil = now() + 1.0; // keep scheduling notes while the bus fades out
    }
  }

  /** Per-frame: advance the beat clock and schedule the next grid steps. */
  function update(dt) {
    const step = clamp(Number(dt) || 0, 0, 0.1);
    if (ctx && ctx.state === 'running') {
      const t = now();
      if (gridOrigin === null) { gridOrigin = t + 0.05; nextStepTime = gridOrigin; stepIndex = 0; }
      // Fell far behind (tab hidden)? Jump forward without losing grid alignment.
      if (nextStepTime < t - 0.05) {
        const missed = Math.ceil((t - nextStepTime) / STEP);
        nextStepTime += missed * STEP;
        stepIndex = (stepIndex + missed) % STEPS_PER_LOOP;
      }
      while (nextStepTime < t + LOOKAHEAD) {
        scheduleStep(stepIndex, nextStepTime);
        stepIndex = (stepIndex + 1) % STEPS_PER_LOOP;
        nextStepTime += STEP;
      }
      beatPhase = mod1((t - gridOrigin) / BEAT);
    } else {
      beatPhase = mod1(beatPhase + step * (BPM / 60));
    }
  }

  /** Schedule everything that happens on one 16th-note grid step at time t. */
  function scheduleStep(step, t) {
    const tier = Math.max(0, curTier);

    // Bass pulse on 8th notes (quarter notes below tier 3).
    if (step % 2 === 0 && tier >= 1) {
      const idx = (step / 2) % BASS_PATTERN.length;
      if (tier >= 3 || idx % 2 === 0) {
        let semis = BASS_PATTERN[idx];
        if (tier >= 5 && idx % 8 === 7) semis += 12; // octave pops at high tiers
        const f = 65.41 * Math.pow(2, semis / 12);
        bassOsc.frequency.setValueAtTime(f, t);
        bassSub.frequency.setValueAtTime(f, t);
        const len = tier >= 3 ? 0.2 : 0.3;
        const g = L.bassEnv.gain;
        g.cancelScheduledValues(t);
        g.setValueAtTime(0, t);
        g.linearRampToValueAtTime(1, t + 0.008);
        g.exponentialRampToValueAtTime(0.001, t + len);
      }
    }

    // Arpeggio while on top: pentatonic up/down run that widens and climbs with tier.
    const arpOn = curOnTop || t < arpFadeUntil;
    if (arpOn && (tier >= 4 || step % 2 === 0)) {
      const len = 4 + tier, period = 2 * len - 2;
      const pos = arpStep % period;
      const deg = 5 + Math.floor(tier / 2) + (pos < len ? pos : period - pos);
      arpStep++;
      tone({
        when: t, freq: pentaFreq(deg), type: tier >= 3 ? 'square' : 'triangle',
        attack: 0.004, decay: 0.16, gain: 0.35, dest: L.arp,
        filter: { type: 'lowpass', freq: 1200 + 600 * tier, Q: 1.5 },
      });
    }

    // Hats: off-beat ticks from tier 3, extra 16th ticks from tier 5.
    if (tier >= 3 && (step % 4 === 2 || (tier >= 5 && step % 2 === 1))) {
      noiseBurst({
        when: t, attack: 0.001, decay: step % 4 === 2 ? 0.05 : 0.03,
        gain: step % 4 === 2 ? 0.5 : 0.22, dest: L.hat,
        filter: { type: 'highpass', freq: 7000 },
      });
    }
  }

  // ---- one-shots ----------------------------------------------------------------

  function paddleHit(strength = 0.5, powerHit = false) {
    if (!live() || !hasVoice(true)) return;
    const s = clamp(Number(strength) || 0, 0, 1);
    // Body thump: sine dropping fast in pitch, plus a low-passed click for the attack.
    tone({ freq: 170 + 60 * s, endFreq: 70, sweep: 0.09, type: 'sine', attack: 0.003, decay: 0.16 + 0.08 * s, gain: 0.35 + 0.3 * s });
    noiseBurst({ attack: 0.001, decay: 0.03 + 0.03 * s, gain: 0.12 + 0.15 * s, filter: { type: 'lowpass', freq: 1800 + 1500 * s } });
    if (powerHit) {
      // Brighter: square + triangle sweeping UP with an opening filter, and a whoosh.
      tone({ freq: 320, endFreq: 1100, sweep: 0.14, type: 'square', attack: 0.004, decay: 0.22, gain: 0.12, send: 0.3,
        filter: { type: 'lowpass', freq: 900, endFreq: 3800, sweep: 0.14, Q: 2 } });
      tone({ freq: 640, endFreq: 2200, sweep: 0.14, type: 'triangle', attack: 0.004, decay: 0.2, gain: 0.1, send: 0.3 });
      noiseBurst({ attack: 0.002, decay: 0.12, gain: 0.1, filter: { type: 'bandpass', freq: 1500, endFreq: 6000, Q: 1.2 } });
    }
  }

  function wallHit() {
    if (!live() || !hasVoice(false)) return;
    // Soft tick: tiny sine blip + a wisp of high-passed noise.
    tone({ freq: 1400, endFreq: 900, sweep: 0.03, type: 'sine', attack: 0.001, decay: 0.045, gain: 0.09 });
    noiseBurst({ attack: 0.001, decay: 0.02, gain: 0.05, filter: { type: 'highpass', freq: 3000 } });
  }

  /** Thin out machine-gun brick breaks: at most BURST_MAX voices per 100 ms. */
  function burstAllowed() {
    const t = now();
    while (burstTimes.length && burstTimes[0] < t - BURST_WINDOW) burstTimes.shift();
    if (burstTimes.length >= BURST_MAX) return false;
    burstTimes.push(t);
    return true;
  }

  function brickBreak(type, comboIndex = 0, fromTop = false) {
    if (!live() || !hasVoice(false) || !burstAllowed()) return;
    const f = comboFreq(comboIndex);
    switch (type) {
      case 'gem':   // bell: sine + inharmonic partial, lots of reverb
        tone({ freq: f * 2, type: 'sine', attack: 0.002, decay: 0.5, gain: 0.18, send: 0.45 });
        tone({ freq: f * 2 * 2.756, type: 'sine', attack: 0.002, decay: 0.25, gain: 0.07, send: 0.45 });
        break;
      case 'ghost': // airy: two detuned triangles + breathy band-passed noise
        tone({ freq: f, type: 'triangle', detune: 9, attack: 0.01, decay: 0.3, gain: 0.11, send: 0.4, filter: { type: 'lowpass', freq: 2000 } });
        tone({ freq: f, type: 'triangle', detune: -9, attack: 0.01, decay: 0.3, gain: 0.11, send: 0.4 });
        noiseBurst({ attack: 0.005, decay: 0.15, gain: 0.05, send: 0.4, filter: { type: 'bandpass', freq: f * 2, Q: 2 } });
        break;
      case 'boost': // bright chirp: saw sweeping up an octave with an opening filter
        tone({ freq: f, endFreq: f * 2, sweep: 0.08, type: 'sawtooth', attack: 0.003, decay: 0.25, gain: 0.14, send: 0.2,
          filter: { type: 'lowpass', freq: 3000, endFreq: 6000, sweep: 0.1 } });
        tone({ freq: f * 2, type: 'square', attack: 0.003, decay: 0.12, gain: 0.05 });
        break;
      case 'angle': // glassy: pure high partials
        tone({ freq: f * 2, type: 'sine', attack: 0.002, decay: 0.35, gain: 0.16, send: 0.35 });
        tone({ freq: f * 3.01, type: 'sine', attack: 0.002, decay: 0.15, gain: 0.07, send: 0.35 });
        break;
      case 'speed': // punchy metallic: band-passed square + noise crack
        tone({ freq: f, type: 'square', attack: 0.002, decay: 0.18, gain: 0.12, filter: { type: 'bandpass', freq: f * 2, Q: 3 } });
        noiseBurst({ attack: 0.001, decay: 0.08, gain: 0.08, filter: { type: 'bandpass', freq: 2500, Q: 1 } });
        break;
      case 'top':   // solid: note plus weight an octave down
        tone({ freq: f, type: 'triangle', attack: 0.003, decay: 0.25, gain: 0.2, send: 0.15 });
        tone({ freq: f / 2, type: 'sine', attack: 0.003, decay: 0.18, gain: 0.15 });
        break;
      case 'bomb':  // short pluck; bomb() supplies the boom
        tone({ freq: f, type: 'triangle', attack: 0.003, decay: 0.15, gain: 0.15 });
        break;
      default:      // 'std' and friends: warm pluck (triangle body + faint square edge)
        tone({ freq: f, type: 'triangle', attack: 0.003, decay: 0.22, gain: 0.2, send: 0.15, filter: { type: 'lowpass', freq: 2200 + f } });
        tone({ freq: f, type: 'square', attack: 0.002, decay: 0.08, gain: 0.05 });
        noiseBurst({ attack: 0.001, decay: 0.02, gain: 0.04, filter: { type: 'highpass', freq: 4000 } });
    }
    if (fromTop) {
      // Sparkle layer: high partials + a glittery high-passed hiss, wet.
      tone({ freq: Math.min(f * 4, 9000), type: 'sine', attack: 0.002, decay: 0.3, gain: 0.08, send: 0.5 });
      tone({ freq: Math.min(f * 3, 7000), type: 'sine', attack: 0.002, decay: 0.2, gain: 0.04, send: 0.5 });
      noiseBurst({ attack: 0.002, decay: 0.12, gain: 0.05, send: 0.4, filter: { type: 'highpass', freq: 6000 } });
    }
  }

  function brickDeny(type) {
    if (!live() || !hasVoice(false)) return;
    switch (type) {
      case 'angle': // glassy tink
        tone({ freq: 2400, type: 'sine', attack: 0.001, decay: 0.15, gain: 0.14, send: 0.3 });
        tone({ freq: 3600, type: 'sine', attack: 0.001, decay: 0.08, gain: 0.06, send: 0.3 });
        break;
      case 'speed': // metallic clank: inharmonic partials + noise
        chord([420, 630, 1120], { type: 'square', attack: 0.001, decay: 0.2, gain: 0.06, filter: { type: 'bandpass', freq: 900, Q: 2 } });
        noiseBurst({ attack: 0.001, decay: 0.06, gain: 0.12, filter: { type: 'bandpass', freq: 1800, Q: 1 } });
        break;
      case 'top':   // dull thud
        tone({ freq: 120, endFreq: 70, sweep: 0.08, type: 'sine', attack: 0.002, decay: 0.14, gain: 0.3 });
        noiseBurst({ attack: 0.001, decay: 0.05, gain: 0.1, filter: { type: 'lowpass', freq: 500 } });
        break;
      case 'steel': // metallic ring: bar-like partials, long tail, wet
        chord([800, 1210, 1890, 2670], { type: 'sine', attack: 0.001, decay: 0.5, gain: 0.07, send: 0.45 });
        noiseBurst({ attack: 0.001, decay: 0.04, gain: 0.1, filter: { type: 'highpass', freq: 2500 } });
        break;
      default:      // plain click
        noiseBurst({ attack: 0.001, decay: 0.015, gain: 0.1, filter: { type: 'bandpass', freq: 2000, Q: 0.8 } });
        tone({ freq: 700, type: 'sine', attack: 0.001, decay: 0.03, gain: 0.06 });
    }
  }

  function bomb() {
    if (!live()) return;
    // Low boom (sine dive to sub) + weight + noise blast whose cutoff collapses + crack.
    tone({ freq: 110, endFreq: 28, sweep: 0.5, type: 'sine', attack: 0.004, decay: 0.7, gain: 0.9, send: 0.3 });
    tone({ freq: 60, endFreq: 40, sweep: 0.4, type: 'triangle', attack: 0.004, decay: 0.4, gain: 0.4 });
    noiseBurst({ attack: 0.002, decay: 0.55, gain: 0.5, send: 0.4, filter: { type: 'lowpass', freq: 3500, endFreq: 150, sweep: 0.5 } });
    noiseBurst({ attack: 0.001, decay: 0.08, gain: 0.3, filter: { type: 'highpass', freq: 1500 } });
  }

  function cascade() {
    if (!live()) return;
    const t = now();
    // Rising saw sweep through an opening band-pass, a rising hiss, then a bright chord lands.
    tone({ freq: 220, endFreq: 1760, sweep: 0.35, type: 'sawtooth', attack: 0.01, hold: 0.3, decay: 0.15, gain: 0.12, send: 0.4,
      filter: { type: 'bandpass', freq: 600, endFreq: 4000, sweep: 0.35, Q: 2 } });
    noiseBurst({ attack: 0.01, decay: 0.4, gain: 0.12, send: 0.4, filter: { type: 'highpass', freq: 800, endFreq: 6000, sweep: 0.35 } });
    chord([5, 7, 8, 11].map(pentaFreq), { when: t + 0.33, type: 'triangle', attack: 0.01, decay: 0.7, gain: 0.12, send: 0.6 });
    chord([10, 12].map(pentaFreq), { when: t + 0.33, type: 'sine', attack: 0.01, decay: 0.5, gain: 0.06, send: 0.6 });
  }

  // Per-kind flavour for the power-up arpeggio: waveform, scale shift, pattern.
  const POWERUP_KINDS = {
    fire:   { type: 'sawtooth', shift: 0, cutoff: 5000 },
    heavy:  { type: 'square', shift: -5, cutoff: 1500, gap: 0.08 },
    split:  { type: 'triangle', shift: 2, double: true },
    magnet: { type: 'sine', shift: 1, pattern: [0, 3, 5, 7] },
    wide:   { type: 'triangle', shift: 0, pattern: [0, 1, 3, 5] },
    slow:   { type: 'sine', shift: -2, gap: 0.1, decay: 0.4 },
    laser:  { type: 'square', shift: 3, gap: 0.05, cutoff: 6000 },
    life:   { type: 'triangle', shift: 4, pattern: [0, 2, 3, 5, 7], decay: 0.35 },
  };

  function powerup(kind) {
    if (!live()) return;
    const k = POWERUP_KINDS[kind] || { type: 'triangle', shift: 0 };
    const pattern = k.pattern || [0, 2, 3, 5]; // C E G C — a bright ascending arpeggio
    const freqs = pattern.map((d) => pentaFreq(5 + d + k.shift));
    const o = { type: k.type, attack: 0.005, decay: k.decay || 0.25, gain: 0.14, send: 0.3,
      filter: { type: 'lowpass', freq: k.cutoff || 3500 } };
    run(freqs, k.gap || 0.065, o);
    if (k.double) run(freqs, k.gap || 0.065, { ...o, detune: 8, gain: 0.08 });
  }

  function powerupEnd() {
    if (!live()) return;
    // Three descending notes with a closing filter.
    run([10, 8, 5].map(pentaFreq), 0.1, { type: 'triangle', attack: 0.005, decay: 0.3, gain: 0.12, send: 0.25,
      filter: { type: 'lowpass', freq: 2500, endFreq: 1000, sweep: 0.4 } });
  }

  function laser() {
    if (!live() || !hasVoice(false)) return;
    // Zap: fast downward square sweep plus a thin saw layer.
    tone({ freq: 1800, endFreq: 220, sweep: 0.12, type: 'square', attack: 0.002, decay: 0.13, gain: 0.13, filter: { type: 'highpass', freq: 400 } });
    tone({ freq: 2600, endFreq: 500, sweep: 0.1, type: 'sawtooth', attack: 0.002, decay: 0.09, gain: 0.06 });
  }

  function ballLost() {
    if (!live()) return;
    // Descending wobble: vibrato triangle + square diving over 0.7 s under a closing filter.
    const o = { freq: 420, endFreq: 70, sweep: 0.7, attack: 0.01, hold: 0.2, decay: 0.55, send: 0.3,
      vibrato: { rate: 11, depth: 35 }, filter: { type: 'lowpass', freq: 2200, endFreq: 300, sweep: 0.7 } };
    tone({ ...o, type: 'triangle', gain: 0.3 });
    tone({ ...o, type: 'square', gain: 0.08, detune: -6 });
  }

  function tierUp(tier) {
    if (!live()) return;
    const tr = clamp(Math.round(tier || 0), 0, 6);
    const t0 = now();
    // Fanfare: a pentatonic run that gets longer, brighter and louder with tier...
    const count = 3 + tr, gap = 0.07;
    const runFreqs = Array.from({ length: count }, (_, i) => pentaFreq(5 + i + (tr >= 4 ? 2 : 0)));
    run(runFreqs, gap, { attack: 0.004, decay: 0.16, gain: 0.1 + 0.01 * tr, send: 0.25,
      filter: { type: 'lowpass', freq: 2500 + 500 * tr } }, (i) => ({ type: i % 2 ? 'triangle' : 'square' }));
    // ...landing on a chord with more voices, a longer tail, a bass hit and shimmer at high tiers.
    const tc = t0 + count * gap;
    const voices = [5, 7, 8, 10, 12, 13].slice(0, 2 + Math.ceil(tr / 2)).map(pentaFreq);
    chord(voices, { when: tc, type: 'triangle', attack: 0.01, decay: 0.5 + 0.1 * tr, gain: 0.1, send: 0.5 });
    if (tr >= 3) chord(voices, { when: tc, type: 'sawtooth', attack: 0.01, decay: 0.4, gain: 0.03, send: 0.5, filter: { type: 'lowpass', freq: 1800 } });
    if (tr >= 2) tone({ when: tc, freq: 110, endFreq: 55, sweep: 0.3, type: 'sine', attack: 0.004, decay: 0.4, gain: 0.3 });
    if (tr >= 4) noiseBurst({ when: tc, attack: 0.05, decay: 0.4, gain: 0.08 + 0.02 * (tr - 4), send: 0.5,
      filter: { type: 'highpass', freq: 3000, endFreq: 9000, sweep: 0.4 } });
  }

  function overtopEnter() {
    if (!live()) return;
    // Rising shimmer: a C-E-G sine cluster gliding up an octave with a rising hiss and a ping on arrival.
    const o = { attack: 0.02, hold: 0.3, decay: 0.4, type: 'sine', gain: 0.09, send: 0.6, sweep: 0.45 };
    [523.25, 659.25, 783.99].forEach((f, i) => tone({ ...o, freq: f, endFreq: f * 2, detune: (i - 1) * 6 }));
    noiseBurst({ attack: 0.02, hold: 0.2, decay: 0.3, gain: 0.07, send: 0.5, filter: { type: 'highpass', freq: 2000, endFreq: 9000, sweep: 0.45 } });
    tone({ when: now() + 0.4, freq: 2093, type: 'sine', attack: 0.003, decay: 0.4, gain: 0.07, send: 0.6 });
  }

  function overtopExit() {
    if (!live()) return;
    // Mirror of overtopEnter: the cluster glides back down, hiss closes, softer.
    const o = { attack: 0.02, hold: 0.25, decay: 0.45, type: 'sine', gain: 0.07, send: 0.5, sweep: 0.5 };
    [1046.5, 1318.5, 1568].forEach((f, i) => tone({ ...o, freq: f, endFreq: f / 2, detune: (i - 1) * 6 }));
    noiseBurst({ attack: 0.02, hold: 0.15, decay: 0.35, gain: 0.05, send: 0.4, filter: { type: 'lowpass', freq: 8000, endFreq: 600, sweep: 0.5 } });
  }

  function levelClear() {
    if (!live()) return;
    const t0 = now();
    // Victory phrase: three quick pentatonic steps, then a long top note over a full chord.
    run([5, 7, 8].map(pentaFreq), 0.12, { type: 'square', attack: 0.005, decay: 0.18, gain: 0.1, send: 0.3, filter: { type: 'lowpass', freq: 3000 } });
    tone({ when: t0 + 0.36, freq: pentaFreq(10), type: 'square', attack: 0.005, hold: 0.3, decay: 0.7, gain: 0.1, send: 0.5, filter: { type: 'lowpass', freq: 3000 } });
    chord([0, 2, 3, 5, 7].map(pentaFreq), { when: t0 + 0.36, type: 'triangle', attack: 0.02, hold: 0.2, decay: 0.9, gain: 0.08, send: 0.5 });
    chord([0, 3].map(pentaFreq), { when: t0 + 0.36, type: 'sawtooth', attack: 0.02, hold: 0.2, decay: 0.7, gain: 0.03, send: 0.5, filter: { type: 'lowpass', freq: 1500 } });
    noiseBurst({ when: t0 + 0.36, attack: 0.02, decay: 0.5, gain: 0.06, send: 0.5, filter: { type: 'highpass', freq: 4000, endFreq: 10000, sweep: 0.5 } });
  }

  function gameOver() {
    if (!live()) return;
    const t0 = now();
    // Somber: slow descending A-F-E-C melody over a dark A-minor pad, ending on a low sine.
    run([440, 349.23, 329.63, 261.63], 0.35, { type: 'triangle', attack: 0.02, decay: 0.6, gain: 0.15, send: 0.5, filter: { type: 'lowpass', freq: 1400 } });
    chord([110, 130.81, 164.81], { type: 'sawtooth', attack: 0.3, hold: 1.0, decay: 1.2, gain: 0.05, send: 0.6, filter: { type: 'lowpass', freq: 600 } });
    tone({ when: t0 + 1.4, freq: 65.41, type: 'sine', attack: 0.05, decay: 1.2, gain: 0.25 });
  }

  function uiClick() {
    if (!live()) return;
    // Tiny neutral click.
    tone({ freq: 900, endFreq: 600, sweep: 0.03, type: 'sine', attack: 0.001, decay: 0.05, gain: 0.08 });
    noiseBurst({ attack: 0.001, decay: 0.015, gain: 0.04, filter: { type: 'highpass', freq: 2500 } });
  }

  // ---- public API ---------------------------------------------------------------

  const api = {
    resume: safe(resume),
    setMuted: safe(setMuted),
    setIntensity: safe(setIntensity),
    update: safe(update),
    paddleHit: safe(paddleHit),
    wallHit: safe(wallHit),
    brickBreak: safe(brickBreak),
    brickDeny: safe(brickDeny),
    bomb: safe(bomb),
    cascade: safe(cascade),
    powerup: safe(powerup),
    powerupEnd: safe(powerupEnd),
    laser: safe(laser),
    ballLost: safe(ballLost),
    tierUp: safe(tierUp),
    overtopEnter: safe(overtopEnter),
    overtopExit: safe(overtopExit),
    levelClear: safe(levelClear),
    gameOver: safe(gameOver),
    uiClick: safe(uiClick),
  };
  Object.defineProperties(api, {
    beatPhase: { get: () => beatPhase, enumerable: true },
    muted: { get: () => muted, set: (v) => api.setMuted(v), enumerable: true },
    /** True once resume() has created a running/suspended context. */
    ready: { get: () => !!ctx, enumerable: true },
    /** Number of one-shot voices currently sounding (debug / test bench). */
    voices: { get: () => activeVoices, enumerable: true },
  });
  return api;
}
