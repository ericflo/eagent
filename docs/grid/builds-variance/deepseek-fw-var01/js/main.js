/* ============================================================
   TOPSIDE — main.js
   Entry point: canvas setup, resize handling, main loop.
   Scripts are loaded at the end of <body>, so DOM is ready on parse.
   Depends on: core/audio/fx/levels/render/input/game.
   ============================================================ */
'use strict';

(function boot() {
  const canvas = document.getElementById('game');
  if (!canvas) { console.error('TOPSIDE: #game canvas not found'); return; }

  const ctx = canvas.getContext('2d', { alpha: false });
  if (!ctx) { console.error('TOPSIDE: 2d context unavailable'); return; }

  /* ---- resize with dpr tracking ---- */
  R.resize(canvas);

  /* ---- input (attaches pointer/keyboard/HUD listeners) ---- */
  I.init(canvas);

  /* ---- audio (lazy; unlocks on first gesture) ---- */
  if (typeof AUD !== 'undefined' && AUD.ensure) AUD.ensure();

  /* ---- boot sequence: first rAF snapshots the world into menu state ---- */
  if (typeof G !== 'undefined' && G.resetGame) G.resetGame();

  /* defensive: ensure paddle.h exists (contract field; game.js may not set it) */
  if (S.paddle && !(S.paddle.h > 0)) S.paddle.h = CFG.paddleH;

  /* ---- main loop ---- */
  let last = (typeof performance !== 'undefined' && performance.now) ? performance.now() : Date.now();
  let acc = 0;
  let lastDPR = window.devicePixelRatio || 1;
  let lastIW = window.innerWidth, lastIH = window.innerHeight;
  let lastMusic = -1;

  function musicIntensity() {
    if (S.state !== 'playing') return 0;
    let v = 0.45;
    v += clamp((S.combo || 0) * 0.05, 0, 0.3);
    v += clamp((S.charge || 0) / 100, 0, 1) * 0.25;
    if (S.mode && S.mode.riding) v += 0.2;
    if (S.ridingBall) v += 0.2;
    if (S.lives === 1) v += 0.1;
    return clamp(v, 0, 1);
  }

  function frame(now) {
    requestAnimationFrame(frame);

    let dt = (now - last) / 1000;
    last = now;
    if (!(dt >= 0)) dt = 0;

    /* smooth with a tiny accumulator, hard-clamped */
    acc += Math.min(dt, 0.1);
    let step = Math.min(acc, 1 / 30);
    if (acc > 1 / 30) acc = 0;      // drop backlog (we only step once)
    if (step <= 0) step = 0.0001;
    step = Math.min(step, 1 / 30);
    S.dt = step;

    try {
      if (typeof G !== 'undefined' && G.tick) G.tick(step);
      if (typeof FX !== 'undefined' && FX.update) FX.update(step);
      if (typeof I !== 'undefined' && I.updateKeys) I.updateKeys(step);
    } catch (err) {
      console.error('TOPSIDE tick error:', err);
      throw err;                    // surface loudly for debugging
    }

    R.render(ctx);

    /* UI state sync (cheap: only touches DOM when changed) */
    if (typeof I !== 'undefined' && I.syncUI) I.syncUI();

    /* music intensity only when it changes */
    if (typeof AUD !== 'undefined' && AUD.music && AUD.music.setIntensity) {
      const mi = musicIntensity();
      if (mi !== lastMusic) {
        lastMusic = mi;
        AUD.music.setIntensity(mi);
        if (mi > 0 && !AUD.music.playing) AUD.music.start();
        else if (mi === 0 && AUD.music.playing) AUD.music.stop();
      }
    }

    /* dpr / size changes */
    const dpr = window.devicePixelRatio || 1;
    if (dpr !== lastDPR || window.innerWidth !== lastIW || window.innerHeight !== lastIH) {
      lastDPR = dpr; lastIW = window.innerWidth; lastIH = window.innerHeight;
      R.resize(canvas);
      if (typeof I !== 'undefined' && I.onResize) I.onResize();
    }
  }

  window.addEventListener('resize', () => {
    R.resize(canvas);
    if (typeof I !== 'undefined' && I.onResize) I.onResize();
  });

  window.addEventListener('orientationchange', () => {
    setTimeout(() => { R.resize(canvas); if (typeof I !== 'undefined' && I.onResize) I.onResize(); }, 120);
  });

  requestAnimationFrame(frame);
})();
