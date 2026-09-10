/* BREAKTHROUGH — js/main.js
   Boot, input (mouse / touch / keyboard / thumbstick), DOM HUD, screens,
   fixed-timestep-ish rAF loop with delta time, canvas scaling. */
(function () {
  const U = window.U;
  const G = window.G;
  const canvas = document.getElementById('game');
  const ctx = canvas.getContext('2d');

  // ---- responsive scaling: canvas is fixed 720x960 logical; CSS scales it.
  // Use devicePixelRatio-aware backing store for crispness.
  function fit() {
    const dpr = Math.min(2, window.devicePixelRatio || 1);
    canvas.width = G.W * dpr;
    canvas.height = G.H * dpr;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.imageSmoothingEnabled = true;
  }
  fit();
  window.addEventListener('resize', fit);

  // ---- audio unlock on first gesture
  function unlock() {
    Audio.init(); Audio.resume();
    window.removeEventListener('pointerdown', unlock);
    window.removeEventListener('keydown', unlock);
  }
  window.addEventListener('pointerdown', unlock);
  window.addEventListener('keydown', unlock);

  // ---- input -> paddle target (logical coords)
  // input -> paddle target (logical coords); pushed to G each frame
  const target = { x: G.W / 2, y: 880 };

  function toLogical(e) {
    const r = canvas.getBoundingClientRect();
    return { x: (e.clientX - r.left) / r.width * G.W, y: (e.clientY - r.top) / r.height * G.H };
  }

  // Mouse: paddle follows cursor x always; y follows when moving vertically in lower half
  window.addEventListener('mousemove', e => {
    if (!G.state.mode.match(/serving|playing/)) return;
    const p = toLogical(e);
    target.x = U.clamp(p.x, 0, G.W);
    // vertical band: paddle follows mouse y when in lower 45% of screen
    if (p.y > G.H * 0.55) target.y = p.y;
  });

  // Touch: drag anywhere to move paddle (x always; y when in lower 55%)
  // Thumbstick mode: left-bottom thumbzone drives paddle by delta
  let touchId = null, lastT = null;
  const thumbzone = document.getElementById('thumbzone');
  const thumbbase = document.getElementById('thumbbase');
  const thumbknob = document.getElementById('thumbknob');
  let stickMode = false, stickOrigin = null;

  window.addEventListener('touchstart', e => {
    if (e.target.closest('button') || e.target.closest('.screen')) return;
    const t = e.changedTouches[0];
    touchId = t.identifier;
    if (stickMode) {
      stickOrigin = { x: t.clientX, y: t.clientY };
      thumbbase.style.left = t.clientX + 'px';
      thumbbase.style.top = t.clientY + 'px';
      thumbzone.classList.add('active');
    } else {
      lastT = { x: t.clientX, y: t.clientY };
    }
    e.preventDefault();
  }, { passive: false });

  window.addEventListener('touchmove', e => {
    for (const t of e.changedTouches) {
      if (t.identifier !== touchId) continue;
      if (stickMode && stickOrigin) {
        let dx = t.clientX - stickOrigin.x, dy = t.clientY - stickOrigin.y;
        const max = 55, len = Math.hypot(dx, dy);
        if (len > max) { dx *= max / len; dy *= max / len; }
        thumbknob.style.transform = `translate(calc(-50% + ${dx}px), calc(-50% + ${dy}px))`;
        target.x = U.clamp(target.x + dx * 3.4, 0, G.W);
        target.y = U.clamp(target.y + dy * 2.6, G.state ? 620 : 620, 900);
      } else if (lastT) {
        const dx = t.clientX - lastT.x, dy = t.clientY - lastT.y;
        target.x = U.clamp(target.x + dx * (G.W / canvas.getBoundingClientRect().width), 0, G.W);
        const scale = G.W / canvas.getBoundingClientRect().width;
        target.y = U.clamp(target.y + dy * scale, 620, 900);
        lastT = { x: t.clientX, y: t.clientY };
      }
      e.preventDefault();
    }
  }, { passive: false });

  window.addEventListener('touchend', e => {
    for (const t of e.changedTouches) {
      if (t.identifier === touchId) {
        touchId = null; lastT = null; stickOrigin = null;
        thumbzone.classList.remove('active');
        thumbknob.style.transform = 'translate(-50%,-50%)';
      }
    }
  });

  // Keyboard: WASD/arrows steer the target
  const keys = {};
  window.addEventListener('keydown', e => {
    keys[e.key.toLowerCase()] = true;
    if (e.key === 'Escape' || e.key.toLowerCase() === 'p') togglePause();
    if (e.key === ' ') { e.preventDefault(); G.launchOrRelease(); }
    if (e.key === 'm') toggleMute();
  });
  window.addEventListener('keyup', e => { keys[e.key.toLowerCase()] = false; });
  function keyboardSteer(dt) {
    const sp = 700 * dt;
    if (keys['arrowleft'] || keys['a']) target.x -= sp;
    if (keys['arrowright'] || keys['d']) target.x += sp;
    if (keys['arrowup'] || keys['w']) target.y -= sp * 0.7;
    if (keys['arrowdown'] || keys['s']) target.y += sp * 0.7;
    target.x = U.clamp(target.x, 0, G.W);
    target.y = U.clamp(target.y, 620, 900);
  }

  // ---- screens & DOM
  const $ = id => document.getElementById(id);
  const overlay = $('overlay');
  const screens = { title: $('titleScreen'), help: $('helpScreen'), state: $('stateScreen') };
  const hud = $('hud'), pu = $('powerups'), topBtns = $('topBtns');

  function show(name) {
    overlay.classList.toggle('hidden', name === 'game');
    Object.entries(screens).forEach(([k, el]) => el.classList.toggle('hidden', k !== name));
    const inGame = name === 'game';
    hud.classList.toggle('hidden', !inGame);
    topBtns.classList.toggle('hidden', !inGame);
    if (name === 'game') pu.classList.remove('hidden');
    if (name !== 'state') { $('stateCard').classList.add('hidden'); $('stateScreen').classList.remove('clear'); }
    if (name !== 'state') $('btnRestart').classList.add('hidden');
  }

  // ---- wiring G events
  G.on('start', () => { show('game'); updateHUD(); });
  G.on('level', i => { $('hudLevel').textContent = 'LV ' + (i + 1) + ' · ' + Levels.LEVELS[i].name; });
  G.on('score', updateHUD);
  G.on('brick', updateHUD);
  G.on('powerups', updatePU);
  G.on('frenzy', on => {
    const m = $('hudMult');
    m.classList.toggle('frenzy', !!on);
    if (on) m.classList.add('pop'), setTimeout(() => m.classList.remove('pop'), 120);
  });
  G.on('gameover', () => {
    $('stateTitle').textContent = 'GAME OVER';
    $('stateSub').textContent = `SCORE ${G.state.score} · HI ${G.state.hi}`;
    $('stateHint').textContent = '';
    $('btnState').textContent = 'PLAY AGAIN';
    show('state');
  });
  G.on('levelcomplete', () => {
    $('stateTitle').textContent = 'LEVEL CLEAR';
    const bestCombo = G.state.bestCombo || 0;
    const bonus = G.state.combo * 250;
    const card = $('stateCard');
    card.innerHTML = `<div><span class="k">LEVEL</span><span class="v">${G.state.level + 1}</span></div>
      <div><span class="k">TOTAL SCORE</span><span class="v">${(G.state.score + bonus).toLocaleString()}</span></div>
      <div><span class="k">COMBO BEST</span><span class="v">×${bestCombo}</span></div>`;
    card.classList.remove('hidden');
    $('stateScreen').classList.add('clear');
    $('stateSub').textContent = `COMBO BONUS +${bonus.toLocaleString()}`;
    G.state.score += bonus;
    $('btnState').textContent = 'NEXT LEVEL';
    updateHUD();
    show('state');
  });
  G.on('gameWin', () => {
    $('stateScreen').classList.add('clear');
    $('stateTitle').textContent = 'YOU BROKE THROUGH EVERYTHING';
    $('stateSub').textContent = `FINAL SCORE ${G.state.score} · HI ${G.state.hi}`;
    $('btnState').textContent = 'PLAY AGAIN';
    show('state');
  });
  G.on('paused', () => {
    pu.classList.add('hidden');
    $('stateTitle').textContent = 'PAUSED';
    $('stateSub').textContent = 'TAKE A BREATH — THE BRICKS WILL WAIT.';
    $('stateHint').textContent = 'P / ESC RESUMES';
    $('btnState').textContent = 'RESUME';
    $('btnRestart').classList.remove('hidden');
    show('state');
  });
  G.on('resumed', () => { $('btnRestart').classList.add('hidden'); show('game'); });

  // buttons
  $('btnStart').addEventListener('click', () => { Audio.init(); Audio.ui(); G.startGame(); });
  $('btnHelp').addEventListener('click', () => { Audio.ui(); show('help'); });
  $('btnBack').addEventListener('click', () => { Audio.ui(); show('title'); });
  $('btnRestart').addEventListener('click', () => { Audio.ui(); G.startGame(); show('game'); });
  $('btnState').addEventListener('click', () => {
    Audio.ui();
    if (G.state.mode === 'paused') { G.resume(); return; }
    if (G.state.mode === 'levelcomplete') { G.state.mode = 'playing'; G.loadLevel(G.state.level + 1); show('game'); return; }
    G.startGame(); show('game');
  });
  $('btnPause').addEventListener('click', togglePause);
  $('btnMute').addEventListener('click', toggleMute);
  $('btnStick').addEventListener('click', () => {
    stickMode = !stickMode;
    $('btnStick').style.color = stickMode ? '#ffd166' : '';
    Audio.ui();
  });
  function togglePause() {
    if (G.state.mode === 'paused') G.resume();
    else G.pause();
  }
  let muted = false;
  function toggleMute() {
    Audio.init();
    muted = Audio.toggleMute();
    $('btnMute').textContent = muted ? '✕♪' : '♪';
    $('btnMute').style.opacity = muted ? .5 : 1;
  }

  // ---- HUD updates
  function updateHUD() {
    const s = G.state;
    $('hudScore').textContent = s.score.toLocaleString();
    $('hudHi').textContent = 'HI ' + s.hi.toLocaleString();
    const mult = s.frenzy > 0 ? (2 + Math.floor(s.frenzyTime)) * (1 + s.combo * 0.25) : (1 + s.combo * 0.25);
    $('hudMultVal').textContent = '×' + (Math.round(mult * 10) / 10);
    $('hudMultBar').firstElementChild.style.width = (Math.min(1, (s.combo % 5) / 5) * 100) + '%';
    $('hudLives').textContent = '♥'.repeat(Math.max(0, Math.min(6, s.lives))) + (s.lives > 6 ? '+' : '');
  }
  const chipIcons = { wide: ['↔', 100, 'WIDE'], sticky: ['⌷', 150, 'STICKY'], laser: ['↑', 350, 'LASER'], slow: ['◷', 200, 'SLOW-MO'] };
  function updatePU() {
    const buffs = G.getBuffs();
    const sig = buffs.map(b => b.key + '|' + b.t.toFixed(1)).join(';');
    if (sig === pu.dataset.sig) return;
    pu.dataset.sig = sig;
    if (!buffs.length) { pu.innerHTML = ''; return; }
    pu.innerHTML = '';
    for (const b of buffs) {
      const [ic, hue, lb] = chipIcons[b.key] || ['★', 200, b.key.toUpperCase()];
      const el = document.createElement('div');
      el.className = 'pu-chip';
      const t = Math.max(0, b.t / b.d * 100);
      el.innerHTML = `<span class="ic" style="background:${U.hsl(hue, 80, 45)}">${ic}</span>
        <span class="lb">${lb}</span>
        <span class="bar"><i style="width:${t}%"></i></span>
        <span class="tm">${Math.ceil(b.t)}s</span>`;
      pu.appendChild(el);
    }
  }

  // ---- title hi score
  function refreshTitle() {
    $('titleHi').textContent = G.state.hi > 0 ? 'HIGH SCORE — ' + G.state.hi.toLocaleString() : '';
  }
  refreshTitle();
  show('title');

  // ---- main loop (delta-time, capped)
  let last = performance.now();
  let acc = 0;
  function frame(now) {
    let dt = (now - last) / 1000;
    last = now;
    dt = Math.min(dt, 1 / 20);          // clamp long frames
    keyboardSteer(dt);
    G.paddle.targetX = target.x; G.paddle.targetY = target.y;
    G.update(dt);
    ctx.setTransform(Math.min(2, window.devicePixelRatio || 1), 0, 0, Math.min(2, window.devicePixelRatio || 1), 0, 0);
    G.draw(ctx);
    // light DOM chip refresh (throttled)
    chipTick -= dt;
    if (chipTick <= 0) { chipTick = 0.25; updatePU(); }
    requestAnimationFrame(frame);
  }
  let chipTick = 0.25;
  requestAnimationFrame(frame);

  // prevent scroll/zoom gestures
  document.addEventListener('gesturestart', e => e.preventDefault());
  document.addEventListener('dblclick', e => e.preventDefault());
})();