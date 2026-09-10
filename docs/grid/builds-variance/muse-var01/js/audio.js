// Synthesized WebAudio SFX + adaptive music. No external files.
export class AudioSys {
  constructor() {
    this.ctx = null; this.master = null; this.musicGain = null;
    this.muted = false; this.musicOn = false;
    this.step = 0; this.nextT = 0; this.intensity = 0; // 0..1
    this.riserOsc = null; this.riserGain = null;
  }
  ensure() {
    if (this.ctx) { if (this.ctx.state === 'suspended') this.ctx.resume(); return; }
    const AC = window.AudioContext || window.webkitAudioContext;
    if (!AC) return;
    this.ctx = new AC();
    this.master = this.ctx.createGain(); this.master.gain.value = 0.9;
    this.master.connect(this.ctx.destination);
    this.musicGain = this.ctx.createGain(); this.musicGain.gain.value = 0.16;
    this.musicGain.connect(this.master);
    this.startMusic();
  }
  setMuted(m) {
    this.muted = m;
    if (this.master && this.ctx) this.master.gain.setValueAtTime(m ? 0 : 0.9, this.ctx.currentTime);
  }
  now() { return this.ctx ? this.ctx.currentTime : 0; }
  env(g, t, a, peak, d) {
    g.gain.setValueAtTime(0.0001, t);
    g.gain.linearRampToValueAtTime(peak, t + a);
    g.gain.exponentialRampToValueAtTime(0.0001, t + a + d);
  }
  tone(freq, dur = 0.12, type = 'square', vol = 0.25, slideTo = null, delay = 0) {
    if (!this.ctx || this.muted) return;
    const t = this.now() + delay;
    const o = this.ctx.createOscillator(), g = this.ctx.createGain();
    o.type = type; o.frequency.setValueAtTime(freq, t);
    if (slideTo) o.frequency.exponentialRampToValueAtTime(Math.max(20, slideTo), t + dur);
    this.env(g, t, 0.005, vol, dur);
    o.connect(g); g.connect(this.master);
    o.start(t); o.stop(t + dur + 0.05);
  }
  noise(dur = 0.3, vol = 0.3, filterFreq = 1200, delay = 0) {
    if (!this.ctx || this.muted) return;
    const t = this.now() + delay;
    const len = Math.floor(this.ctx.sampleRate * dur);
    const buf = this.ctx.createBuffer(1, len, this.ctx.sampleRate);
    const d = buf.getChannelData(0);
    for (let i = 0; i < len; i++) d[i] = (Math.random() * 2 - 1) * (1 - i / len);
    const src = this.ctx.createBufferSource(); src.buffer = buf;
    const f = this.ctx.createBiquadFilter(); f.type = 'lowpass'; f.frequency.value = filterFreq;
    const g = this.ctx.createGain();
    g.gain.setValueAtTime(vol, t);
    g.gain.exponentialRampToValueAtTime(0.0001, t + dur);
    src.connect(f); f.connect(g); g.connect(this.master);
    src.start(t);
  }
  paddle(pos = 0.5) { // pos 0..1 across paddle -> pitch
    this.tone(300 + pos * 500, 0.07, 'square', 0.18, 220 + pos * 400);
  }
  wall() { this.tone(240, 0.06, 'square', 0.12, 180); }
  brick(n = 0, combo = 0) { // pitch ladder
    const scale = [523, 587, 659, 784, 880, 1046, 1174, 1318, 1396, 1568, 1760, 2093];
    const f = scale[(n + combo) % scale.length];
    this.tone(f, 0.12, 'square', 0.2, f * 0.99);
    this.tone(f * 2, 0.08, 'sine', 0.1);
  }
  denied() { this.tone(140, 0.12, 'sawtooth', 0.2, 90); }
  sizzle() { this.noise(0.25, 0.22, 4000); this.tone(180, 0.2, 'sawtooth', 0.12, 70); }
  explosion() {
    this.noise(0.6, 0.5, 900); this.tone(90, 0.5, 'sine', 0.5, 35);
    this.tone(55, 0.7, 'triangle', 0.4, 28);
  }
  powerup() { [660, 830, 990, 1320].forEach((f, i) => this.tone(f, 0.1, 'square', 0.16, null, i * 0.06)); }
  life() { [523, 659, 784, 1046, 784, 1046].forEach((f, i) => this.tone(f, 0.12, 'triangle', 0.2, null, i * 0.08)); }
  loseLife() { [400, 340, 280, 200, 140].forEach((f, i) => this.tone(f, 0.16, 'sawtooth', 0.18, f * 0.9, i * 0.1)); }
  launch() { this.tone(300, 0.15, 'square', 0.15, 700); }
  laser() { this.tone(1400, 0.12, 'sawtooth', 0.12, 200); }
  boost() { this.noise(0.25, 0.25, 2500); this.tone(250, 0.25, 'sawtooth', 0.2, 900); }
  levelClear() { [523, 659, 784, 1046, 1318].forEach((f, i) => this.tone(f, 0.16, 'square', 0.18, null, i * 0.09)); }
  gameOver() { [330, 262, 208, 165, 110].forEach((f, i) => this.tone(f, 0.3, 'triangle', 0.22, null, i * 0.16)); }
  victory() { [523, 587, 659, 784, 880, 1046, 1318, 1568].forEach((f, i) => this.tone(f, 0.18, 'square', 0.16, null, i * 0.1)); }
  odEnter() {
    // riser
    if (!this.ctx || this.muted) return;
    this.tone(150, 0.7, 'sawtooth', 0.25, 1200);
    this.noise(0.7, 0.2, 3000);
    [880, 1108, 1318].forEach((f, i) => this.tone(f, 0.3, 'square', 0.12, null, 0.5 + i * 0.05));
  }
  odTick(mult) { this.tone(700 + Math.min(99, mult) * 12, 0.05, 'sine', 0.07); }
  // --- adaptive music: scheduled loop, call update() each frame ---
  startMusic() {
    if (!this.ctx || this.musicOn) return;
    this.musicOn = true; this.nextT = this.ctx.currentTime + 0.1;
    this.step = 0;
  }
  update(overdrive, mult) {
    if (!this.ctx || !this.musicOn || this.muted) return;
    this.intensity += (((overdrive ? 1 : 0.25) + Math.min(0.6, mult / 150)) - this.intensity) * 0.02;
    const ct = this.ctx.currentTime;
    if (this.nextT < ct - 0.3) this.nextT = ct;
    const base = overdrive ? 0.135 : 0.21; // faster in overdrive
    while (this.nextT < ct + 0.25) {
      const s = this.step % 16;
      const minor = [110, 0, 130.8, 0, 98, 0, 146.8, 0, 110, 0, 130.8, 164.8, 98, 146.8, 123.5, 0];
      if (minor[s] && (s % 2 === 0 || this.intensity > 0.4)) this.mnote(minor[s], base * 0.9, 'triangle', 0.5);
      // arp layer when intense
      if (this.intensity > 0.35) {
        const arp = [220, 261.6, 329.6, 440, 523.25, 440, 329.6, 261.6];
        if (s % 2 === 1) this.mnote(arp[(this.step >> 1) % arp.length] * 2, base * 0.4, 'sine', 0.18);
      }
      // hat
      if (s % 2 === 1) this.mhat(this.nextT);
      // kick-ish on beats
      if (s % 4 === 0) this.mnote(55, 0.12, 'sine', 0.7);
      this.nextT += base; this.step++;
    }
  }
  mnote(freq, dur, type, vol) {
    const t = this.nextT, o = this.ctx.createOscillator(), g = this.ctx.createGain();
    o.type = type; o.frequency.value = freq;
    g.gain.setValueAtTime(0.0001, t);
    g.gain.linearRampToValueAtTime(vol * 0.35, t + 0.01);
    g.gain.exponentialRampToValueAtTime(0.0001, t + dur);
    o.connect(g); g.connect(this.musicGain);
    o.start(t); o.stop(t + dur + 0.05);
  }
  mhat(t) {
    const len = 400, buf = this.ctx.createBuffer(1, len, this.ctx.sampleRate);
    const d = buf.getChannelData(0);
    for (let i = 0; i < len; i++) d[i] = (Math.random() * 2 - 1) * (1 - i / len);
    const s = this.ctx.createBufferSource(); s.buffer = buf;
    const f = this.ctx.createBiquadFilter(); f.type = 'highpass'; f.frequency.value = 6000;
    const g = this.ctx.createGain(); g.gain.value = 0.25;
    s.connect(f); f.connect(g); g.connect(this.musicGain); s.start(t);
  }
}
