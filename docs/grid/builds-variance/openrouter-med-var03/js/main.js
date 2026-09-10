/* ============================================================
   Overdrive Breakout — main.js
   Canvas scaling (fixed logical 1080x1920, letterboxed, DPR-aware),
   fixed-timestep game loop (120Hz physics substeps), state wiring.
   ============================================================ */
'use strict';

(function () {
  const canvas = document.getElementById('game');
  const ctx = canvas.getContext('2d');
  const game = new Game(canvas);
  window.__game = game; // test hook

  // ---- resize: fit FIELD into viewport, letterboxed, DPR crisp ----
  let viewScale = 1, viewX = 0, viewY = 0, viewW = 0, viewH = 0;
  function resize() {
    const dpr = Math.min(window.devicePixelRatio || 1, 3);
    const vw = window.innerWidth, vh = window.innerHeight;
    const aspect = FIELD_W / FIELD_H;
    let w = vw, h = vw / aspect;
    if (h > vh) { h = vh; w = vh * aspect; }
    viewW = w; viewH = h;
    viewX = (vw - w) / 2; viewY = (vh - h) / 2;
    viewScale = w / FIELD_W;
    canvas.width = Math.round(vw * dpr);
    canvas.height = Math.round(vh * dpr);
    canvas.style.width = vw + 'px';
    canvas.style.height = vh + 'px';
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  }
  window.addEventListener('resize', resize);
  window.addEventListener('orientationchange', resize);
  resize();

  // map client -> field coords
  game.toField = function (cx, cy) {
    return { x: (cx - viewX) / viewScale, y: (cy - viewY) / viewScale };
  };

  // ---- fixed-timestep loop: 120Hz physics, rAF rendering ----
  const DT = 1 / 120;
  const MAX_FRAME = 0.25; // cap (tab-switch protection)
  let acc = 0, last = performance.now();

  function frame(now) {
    let elapsed = (now - last) / 1000;
    last = now;
    if (elapsed > MAX_FRAME) elapsed = MAX_FRAME;
    acc += elapsed;
    game.time += elapsed;
    game.stateT += elapsed;

    // input poll + stepping (paused/title/howto states don't step physics)
    game.pollKeys();
    const stepping = game.state === 'play' || game.state === 'ready' || game.state === 'levelclear';
    if (stepping) {
      while (acc >= DT) { game.step(DT); acc -= DT; }
    } else {
      acc = 0;
      // keep fx alive on menu screens for polish
      if (game.state !== 'play' && game.state !== 'ready' && game.state !== 'levelclear') game.fx.update(elapsed);
      if (game.state === 'title' && Math.random() < elapsed * 8) {
        game.fx.spawnParticle(Math.random() * FIELD_W, -10, (Math.random() - 0.5) * 2, 3, 2, 4, '#7df9ff', 0);
      }
    }
    // bricks keep animating on all states
    for (const br of game.bricks) if (!stepping) br.update(elapsed);

    // ---- render ----
    ctx.save();
    ctx.fillStyle = '#02030a';
    ctx.fillRect(0, 0, window.innerWidth, window.innerHeight);
    ctx.translate(viewX, viewY);
    ctx.beginPath();
    ctx.rect(0, 0, FIELD_W, FIELD_H);
    ctx.clip();
    ctx.scale(viewScale, viewScale);
    game.render(ctx, acc / DT);
    // thumbstick hint zone (when enabled on touch)
    if (game.stickEnabled && (game.state === 'play' || game.state === 'ready')) {
      ctx.globalAlpha = 0.35;
      ctx.strokeStyle = '#7df9ff';
      ctx.lineWidth = 4;
      ctx.beginPath();
      ctx.arc(FIELD_W / 2, FIELD_H - 260, 150, 0, 6.283);
      ctx.stroke();
      ctx.font = '600 26px system-ui, sans-serif';
      ctx.textAlign = 'center';
      ctx.fillStyle = '#7df9ff';
      ctx.fillText('THUMB ZONE', FIELD_W / 2, FIELD_H - 330);
      ctx.globalAlpha = 1;
    }
    ctx.restore();

    AudioSys.tick();
    requestAnimationFrame(frame);
  }
  requestAnimationFrame(frame);

  // advance interstitial screens on tap/click — wrap the existing handlers
  // by listening on the canvas in capture phase first.
  canvas.addEventListener('pointerdown', () => {
    game.advanceOnTap();
  }, { capture: true });

  // resume audio ctx on any gesture (mobile requirement)
  ['pointerdown', 'touchstart', 'keydown'].forEach(ev =>
    window.addEventListener(ev, () => AudioSys.unlock(), { passive: true }));

  // reflect mute state on the button
  document.getElementById('btn-sound').textContent = AudioSys.muted() ? '🔇' : '🔊';
  document.getElementById('btn-stick').style.opacity = game.stickEnabled ? '1' : '0.6';
})();