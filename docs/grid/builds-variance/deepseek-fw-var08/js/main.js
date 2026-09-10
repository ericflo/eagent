/* OVERDRIVE — js/main.js
 * Boot: resize handling, RAF loop (fixed-ish dt clamp), FPS tracking,
 * visibility auto-pause, first-user-gesture audio unlock, boot splash.
 * Classic script; loaded last.
 */
(function () {
  'use strict';

  function boot() {
    var canvas = document.getElementById('game');
    var bootEl = document.getElementById('boot');
    var ctx = canvas.getContext('2d');

    // ---- game init (needs window sizes) ---------------------------------------
    window.Game.init(canvas);
    window.Input.init(window.Game);
    window.Game.resize(window.innerWidth, window.innerHeight);
    window.FX.init(window.Game.ctx, window.Game.W, window.Game.H);
    window.Game.state = 'MENU';
    window.Game.stateTime = 0;
    function resize() {
      var cw = window.innerWidth, ch = window.innerHeight;
      var dpr = window.devicePixelRatio || 1;
      // preserve aspect: logical W=720, H derived & clamped
      var logicalH = Math.min(1500, Math.max(900, 720 * (ch / cw)));
      var scale = Math.min(cw / 720, ch / logicalH);
      var dispW = Math.round(720 * scale);
      var dispH = Math.round(logicalH * scale);
      canvas.style.width = dispW + 'px';
      canvas.style.height = dispH + 'px';
      canvas.width = Math.round(dispW * dpr);
      canvas.height = Math.round(dispH * dpr);
      window.Game.resize(720, logicalH);
      window.Game.dpr = dpr;
      window.Game.cssW = dispW;
      window.Game.cssH = dispH;
    }
    resize();
    window.addEventListener('resize', resize);

    // ---- visibility auto-pause --------------------------------------------------
    document.addEventListener('visibilitychange', function () {
      if (document.hidden) {
        window.Game.paused = true;
      }
    });

    // ---- first-user-gesture audio unlock -----------------------------------------
    function unlock() {
      window.AudioSys.unlock();
      window.removeEventListener('pointerdown', unlock);
      window.removeEventListener('keydown', unlock);
    }
    window.addEventListener('pointerdown', unlock);
    window.addEventListener('keydown', unlock);

    // ---- boot splash fade ----------------------------------------------------------
    if (bootEl) {
      setTimeout(function () { bootEl.classList.add('hidden'); }, 500);
      setTimeout(function () { if (bootEl.parentNode) bootEl.parentNode.removeChild(bootEl); }, 1200);
    }

    // ---- main loop ------------------------------------------------------------------
    var last = performance.now();
    var acc = 0;

    function frame(now) {
      var rawDt = (now - last) / 1000;
      last = now;
      // clamp dt to avoid spiral of death when tab hidden / hitch
      var dt = Math.min(rawDt, 0.05);
      // pause updating (but keep rendering) when hidden
      if (document.hidden) dt = 0;

      // fixed-ish stepping: run update in 1/120s slices for stable physics
      acc += dt;
      var maxSteps = 4;
      while (acc > 0 && maxSteps-- > 0) {
        var step = Math.min(acc, 1 / 120);
        window.Game.update(step);
        acc -= step;
      }
      if (acc > 0.05) acc = 0;

      // fx + render
      window.FX.update(dt);
      window.Game.draw();

      requestAnimationFrame(frame);
    }
    requestAnimationFrame(frame);
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', boot);
  } else {
    boot();
  }
})();
