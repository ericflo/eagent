// audio.js — WebAudio sound engine: melodic arpeggio notes, SFX,
// dynamic music layering tied to score & multiplier. All synth, no assets.

const AudioSys = (() => {
  let ctx = null;
  let master = null;
  let musicGain = null;
  let sfxGain = null;
  let enabled = true;
  let muted = false;
  let timer = null;
  let noteIndex = 0;
  let nextNoteTime = 0;
  let musicLevel = 0;      // 0 = calm, 1 = charged, 2 = fever
  let bassTimer = null;
  let bassStep = 0;

  const NOTE_NAMES = ['C','C#','D','D#','E','F','F#','G','G#','A','A#','B'];
  function freq(name, oct) {
    const n = NOTE_NAMES.indexOf(name) + 12 * (oct + 1);
    return 440 * Math.pow(2, (n - 69) / 12);
  }

  // A minor pentatonic-ish progression: [root, third, fourth, fifth, sixth]
  const CHORDS = [
    [['A',3], ['C',4], ['E',4], ['G',4], ['B',4]],
    [['F',3], ['A',3], ['C',4], ['E',4], ['G',4]],
    [['C',4], ['E',4], ['G',4], ['A',4], ['D',5]],
    [['G',3], ['B',3], ['D',4], ['F',4], ['A',4]],
  ];
  const BASS_ROOTS = ['A', 'F', 'C', 'G'];

  function ensure() {
    if (ctx) return;
    const AC = window.AudioContext || window.webkitAudioContext;
    if (!AC) return;
    ctx = new AC();
    master = ctx.createGain();
    master.gain.value = 0.9;
    master.connect(ctx.destination);
    musicGain = ctx.createGain();
    musicGain.gain.value = 0.26;
    musicGain.connect(master);
    sfxGain = ctx.createGain();
    sfxGain.gain.value = 0.8;
    sfxGain.connect(master);
    scheduleBass();
  }

  function setEnabled(v) {
    enabled = v;
    if (!v) {
      stopMusic();
    } else {
      ensure();
      if (ctx && ctx.state === 'suspended') ctx.resume();
    }
  }

  function setMuted(m) {
    muted = m;
    if (master && ctx) master.gain.value = m ? 0 : 0.9;
  }

  function resume() {
    ensure();
    if (ctx && ctx.state === 'suspended') ctx.resume();
  }

  // ---- tone helpers ----
  function tone(freqStart, freqEnd, dur, type, dest, vol, delay = 0) {
    if (!ctx || muted) return;
    const t0 = ctx.currentTime + delay;
    const o = ctx.createOscillator();
    const g = ctx.createGain();
    o.type = type;
    o.frequency.setValueAtTime(freqStart, t0);
    if (freqEnd && freqEnd !== freqStart) o.frequency.exponentialRampToValueAtTime(Math.max(1, freqEnd), t0 + dur);
    g.gain.setValueAtTime(0.0001, t0);
    g.gain.exponentialRampToValueAtTime(vol, t0 + 0.01);
    g.gain.exponentialRampToValueAtTime(0.0001, t0 + dur);
    o.connect(g); g.connect(dest || master);
    o.start(t0); o.stop(t0 + dur + 0.02);
  }

  function noise(dur, vol, filterFreq, delay = 0) {
    if (!ctx || muted) return;
    const t0 = ctx.currentTime + delay;
    const len = Math.max(1, Math.floor(ctx.sampleRate * dur));
    const buf = ctx.createBuffer(1, len, ctx.sampleRate);
    const data = buf.getChannelData(0);
    for (let i = 0; i < len; i++) data[i] = Math.random() * 2 - 1;
    const src = ctx.createBufferSource();
    src.buffer = buf;
    const f = ctx.createBiquadFilter();
    f.type = 'lowpass';
    f.frequency.value = filterFreq;
    const g = ctx.createGain();
    g.gain.setValueAtTime(vol, t0);
    g.gain.exponentialRampToValueAtTime(0.0001, t0 + dur);
    src.connect(f); f.connect(g); g.connect(master);
    src.start(t0); src.stop(t0 + dur + 0.02);
  }

  // ---- one-shots ----
  const s = {
    launch()   { tone(220, 440, 0.12, 'triangle', sfxGain, 0.5); },
    blip()     { tone(660, 660, 0.05, 'square', sfxGain, 0.18); },
    bounce()   { tone(300, 240, 0.07, 'square', sfxGain, 0.22); },
    paddle()   { tone(160, 320, 0.09, 'triangle', sfxGain, 0.45); },
    break()    { noise(0.1, 0.32, 2500); tone(320, 120, 0.09, 'square', sfxGain, 0.25); },
    gem()      { tone(880, 1760, 0.16, 'sine', sfxGain, 0.5); tone(1320, 1760, 0.18, 'sine', sfxGain, 0.3, 0.05); },
    power()    { tone(330, 660, 0.18, 'sawtooth', sfxGain, 0.4); tone(220, 440, 0.22, 'triangle', sfxGain, 0.35, 0.03); },
    bad()      { tone(200, 90, 0.3, 'sawtooth', sfxGain, 0.4); },
    life()     { [523, 659, 784].forEach((f, i) => tone(f, f, 0.14, 'sine', sfxGain, 0.4, i * 0.07)); },
    fever()    { [523, 659, 784, 1046].forEach((f, i) => tone(f, f, 0.16, 'sine', sfxGain, 0.5, i * 0.05)); },
    heavy()    { tone(80, 50, 0.35, 'sawtooth', sfxGain, 0.6); noise(0.25, 0.4, 600); },
    maxcombo() { [660, 880, 1108, 1318].forEach((f, i) => tone(f, f, 0.1, 'square', sfxGain, 0.25, i * 0.05)); },
    victory()  { [523, 659, 784, 1046, 1318].forEach((f, i) => tone(f, f, 0.22, 'triangle', sfxGain, 0.45, i * 0.09)); },
    lose()     { [392, 330, 262, 196].forEach((f, i) => tone(f, f, 0.28, 'sawtooth', sfxGain, 0.4, i * 0.12)); },
  };

  // ---- music ----
  function scheduleBass() {
    if (bassTimer) clearInterval(bassTimer);
    if (!ctx) return;
    bassTimer = setInterval(() => {
      if (!ctx || muted || !enabled) return;
      const t = ctx.currentTime;
      const root = BASS_ROOTS[bassStep % 4];
      const oct = musicLevel === 2 ? 2 : 3;
      tone(freq(root, oct), freq(BASS_ROOTS[(bassStep + 1) % 4], oct), 0.5, 'sine', musicGain, 0.10);
      bassStep++;
    }, 380);
  }

  function setMusicLevel(lvl) {
    musicLevel = lvl;
    if (!ctx || !enabled || muted) return;
    nextNoteTime = ctx.currentTime + 0.06;
    noteIndex = 0;
    scheduleBass();
    // brighten the master music gain with level
    musicGain.gain.setTargetAtTime(0.2 + lvl * 0.14, ctx.currentTime, 0.2);
  }

  // Arpeggio scheduler driven by a 16th-note timer (works regardless of game loop).
  function startMusic() {
    if (!ctx || !enabled || muted) return;
    if (timer) return;
    nextNoteTime = ctx.currentTime + 0.05;
    noteIndex = 0;
    timer = setInterval(pumpMusic, 90);
  }
  function stopMusic() {
    if (timer) { clearInterval(timer); timer = null; }
    if (bassTimer) { clearInterval(bassTimer); bassTimer = null; }
  }
  function pumpMusic() {
    if (!ctx || muted || !enabled) return;
    const chord = CHORDS[noteIndex % 4];
    // eighth-note arpeggio of the chord; sequence per level
    const seqOffset = musicLevel === 0 ? Math.floor(noteIndex / 2) % chord.length : noteIndex % chord.length;
    const [name, oct] = chord[seqOffset];
    const dur = musicLevel === 0 ? 0.5 : 0.32;
    tone(freq(name, oct), freq(name, oct + (musicLevel === 2 ? 1 : 0)), dur, 'triangle', musicGain, 0.5);
    noteIndex++;
    // occasionally add a sparkle
    if (musicLevel === 2 && noteIndex % 4 === 0) {
      tone(freq(chord[2][0], chord[2][1] + 1), freq(chord[2][0], chord[2][1] + 2), 0.2, 'sine', musicGain, 0.28);
    }
  }

  return { s, ensure, resume, setEnabled, setMuted, startMusic, stopMusic, setMusicLevel, get enabled() { return enabled; }, get muted() { return muted; } };
})();

window.AudioSys = AudioSys;
