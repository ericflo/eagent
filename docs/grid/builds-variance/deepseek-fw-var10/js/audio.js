// Procedural sound engine — everything is synthesized with Web Audio so we
// need zero asset files. A tiny oscillator "blip" plays on launch so the
// user can hear that audio works even before their first brick.

let ctx = null;
let master = null;
let noiseBuf = null;
let muted = false;
let lastFireAt = 0;

export function initAudio() {
  try {
    const AC = window.AudioContext || window.webkitAudioContext;
    if (!AC) return false;
    ctx = new AC();
    master = ctx.createGain();
    master.gain.value = muted ? 0 : 0.8;
    master.connect(ctx.destination);

    // one shared noise buffer
    const len = ctx.sampleRate * 1.2;
    noiseBuf = ctx.createBuffer(1, len, ctx.sampleRate);
    const d = noiseBuf.getChannelData(0);
    for (let i = 0; i < len; i++) d[i] = Math.random() * 2 - 1;
    return true;
  } catch {
    return false;
  }
}

export function setMuted(m) {
  muted = m;
  if (master && ctx) master.gain.value = m ? 0 : 0.8;
}

export function isMuted() { return muted; }

export function resumeAudio() {
  if (ctx && ctx.state === "suspended") ctx.resume().catch(() => {});
}

function env(g, t0, a, peak, dec) {
  const o = g.gain;
  o.setValueAtTime(0.0001, t0);
  o.linearRampToValueAtTime(peak, t0 + a);
  o.exponentialRampToValueAtTime(0.0001, t0 + a + dec);
}

function tone(freq, dur, type = "sine", vol = 0.5, slideTo = null, t0 = null) {
  if (!ctx || muted) return;
  const t = t0 ?? ctx.currentTime;
  const o = ctx.createOscillator();
  const g = ctx.createGain();
  o.type = type;
  o.frequency.setValueAtTime(freq, t);
  if (slideTo) o.frequency.exponentialRampToValueAtTime(slideTo, t + dur);
  env(g, t, 0.004, vol, dur);
  o.connect(g).connect(master);
  o.start(t);
  o.stop(t + dur + 0.05);
}

function noise(dur, vol, filterFreq, q = 1, t0 = null, type = "lowpass") {
  if (!ctx || muted) return;
  const t = t0 ?? ctx.currentTime;
  const src = ctx.createBufferSource();
  src.buffer = noiseBuf;
  src.playbackRate.value = 0.7 + Math.random() * 0.6;
  const f = ctx.createBiquadFilter();
  f.type = type;
  f.frequency.value = filterFreq;
  f.Q.value = q;
  const g = ctx.createGain();
  env(g, t, 0.003, vol, dur);
  src.connect(f).connect(g).connect(master);
  src.start(t);
  src.stop(t + dur + 0.05);
}

export function sfxBounce() {
  tone(300 + Math.random() * 140, 0.07, "triangle", 0.35, 150);
  noise(0.04, 0.1, 1400, 1, null, "highpass");
}

export function sfxPaddle() {
  tone(200 + Math.random() * 120, 0.08, "triangle", 0.4, 120 + Math.random() * 60);
}

export function sfxBrick(colorIdx, comboLevel) {
  const f = 380 + colorIdx * 120 + Math.random() * 60;
  tone(f, 0.1, "square", 0.3, f * 0.7);
  noise(0.06, 0.16, 2500 + comboLevel * 300, 1, null, "highpass");
  tone(f * 1.5, 0.16, "sine", 0.18, f * 2, null, null);
}

export function sfxRico() {
  tone(900, 0.12, "triangle", 0.4, 1600);
  tone(1800, 0.2, "sine", 0.16, 2400);
  noise(0.14, 0.2, 3000, 2, null, "bandpass");
}

export function sfxPower() {
  if (!ctx) return;
  const t = ctx.currentTime;
  [523, 659, 784, 1046].forEach((f, i) => tone(f, 0.14, "triangle", 0.28, null, t + i * 0.07));
}

export function sfxGold() {
  if (!ctx) return;
  const t = ctx.currentTime;
  [784, 988, 1175].forEach((f, i) => tone(f, 0.12, "sine", 0.2, null, t + i * 0.06));
}

export function sfxWall() {
  tone(120, 0.09, "sawtooth", 0.22, 80);
  noise(0.1, 0.14, 700, 1, null, "lowpass");
}

export function sfxLostBall() {
  tone(700, 0.4, "sawtooth", 0.3, 140);
  noise(0.3, 0.2, 500, 1);
}

export function sfxRise() {
  if (!ctx) return;
  const t = ctx.currentTime;
  tone(262, 0.14, "triangle", 0.3, 392);
  tone(392, 0.14, "triangle", 0.3, 523, null, t + 0.12);
  tone(523, 0.2, "triangle", 0.3, 784, null, t + 0.24);
}

export function sfxLevel() {
  if (!ctx) return;
  const t = ctx.currentTime;
  [392, 494, 587, 784, 988, 1175].forEach((f, i) => tone(f, 0.15, "square", 0.22, null, t + i * 0.09));
}

export function sfxRoof() {
  if (!ctx) return;
  const t = ctx.currentTime;
  [523, 659, 784, 1046, 1318, 1568].forEach((f, i) => tone(f, 0.2, "triangle", 0.3, null, t + i * 0.1));
}

export function sfxDie() {
  if (!ctx) return;
  const t = ctx.currentTime;
  [400, 300, 220, 150].forEach((f, i) => tone(f, 0.25, "sawtooth", 0.25, f * 0.6, t + i * 0.13));
}

export function sfxCombo(n) {
  const f = 440 * Math.pow(1.12, n);
  tone(f, 0.1, "triangle", 0.28);
  tone(f * 2, 0.1, "sine", 0.14);
}
