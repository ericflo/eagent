/* ============================================================
   TOPSIDE — input.js
   Pointer (mouse+touch), keyboard, and HUD button input.
   Depends on: core.js (S), audio.js (AUD), render.js (R),
               game.js (G) — G only called at event time, never at load.
   Exposes: I = { init(canvas), setMode(mode), onResize(), updateKeys(dt),
                  syncUI(), touchActive, lastTouch }
   ============================================================ */
'use strict';

const I = (() => {
  const pointers = new Map();     // pointerId -> {llx, lly (client px), isPrimary}
  let primaryId = null;
  let canvas = null;
  let mode = 'play';

  let touchActive = false;
  const lastTouch = { x: null, y: null, active: false };

  /* ---------------- helpers ---------------- */

  function unlockAudio() {
    if (typeof AUD !== 'undefined' && AUD.ensure) { AUD.ensure(); AUD.resume(); }
  }

  /* route a primary action: start / serve / fire / release / restart */
  function primaryAction() {
    unlockAudio();
    if (typeof G === 'undefined') return;
    const st = S.state;

    if (st === 'boot') { S.state = 'menu'; return; }

    if (st === 'menu') { if (G.startGame) G.startGame(); return; }

    if (st === 'over' || st === 'win') {
      if (st === 'over' && G.resetGame) G.resetGame();   // full restart on game over
      if (G.startGame) G.startGame();
      return;
    }

    /* playing / pause-ish states */
    if (S.countdown > 0) { if (G.serveBall) G.serveBall(); return; }
    if (S.paddle && S.paddle.grabStuck) { if (G.releaseStuckBall) G.releaseStuckBall(); return; }
    if (S.fireReady) { if (G.firePower) G.firePower(); return; }
  }

  function activePointers() {
    return Array.from(pointers.keys());
  }

  function setPrimary(id) {
    primaryId = id || null;
    for (const [pid, p] of pointers) p.isPrimary = (pid === primaryId);
  }

  function releaseTarget() {
    if (S.paddle) S.paddle.target = null;
  }

  /* ---------------- pointer events ---------------- */

  function toLogicalFromClient(cx, cy) {
    if (typeof R !== 'undefined' && R.toLogical) return R.toLogical(cx, cy);
    return { x: cx, y: cy };
  }

  function onPointerDown(e) {
    unlockAudio();
    if (!canvas) return;
    const id = e.pointerId;
    const lp = toLogicalFromClient(e.clientX, e.clientY);
    const isTouch = e.pointerType === 'touch';

    if (isTouch) { touchActive = true; lastTouch.x = lp.x; lastTouch.y = lp.y; }

    /* single tap advances from menu / over / win */
    if (pointers.size === 0 && (S.state === 'menu' || S.state === 'boot' || S.state === 'over' || S.state === 'win')) {
      pointers.set(id, { llx: e.clientX, lly: e.clientY, isPrimary: true });
      setPrimary(id);
      if (S.paddle) S.paddle.target = { x: lp.x, y: lp.y };
      primaryAction();
      e.preventDefault();
      return;
    }
    /* single tap serves / releases while waiting */
    if (pointers.size === 0 && S.state === 'playing') {
      if (S.countdown > 0 || (S.paddle && S.paddle.grabStuck)) {
        pointers.set(id, { llx: e.clientX, lly: e.clientY, isPrimary: true });
        setPrimary(id);
        if (S.paddle) S.paddle.target = { x: lp.x, y: lp.y };
        primaryAction();
        e.preventDefault();
        return;
      }
    }

    /* second (or later) pointer => action (fire/serve/start) */
    if (pointers.size > 0) {
      pointers.set(id, { llx: e.clientX, lly: e.clientY, isPrimary: false });
      primaryAction();
      lastTouch.active = true;
      e.preventDefault();
      return;
    }

    pointers.set(id, { llx: e.clientX, lly: e.clientY, isPrimary: true });
    setPrimary(id);

    /* move target under this pointer */
    let tx = lp.x, ty = lp.y;
    if (isTouch) ty = Math.min(CFG.H, ty - 80);   // keep finger clear of paddle
    if (S.paddle) S.paddle.target = { x: tx, y: ty };

    e.preventDefault();
  }

  function onPointerMove(e) {
    const p = pointers.get(e.pointerId);
    const lp = toLogicalFromClient(e.clientX, e.clientY);

    /* mouse: hover moves the paddle while playing, even without a button */
    const isMouse = e.pointerType === 'mouse';
    if (isMouse && !p) {
      if (S.state === 'playing' && S.paddle) {
        S.paddle.target = { x: lp.x, y: lp.y };
      }
      return;
    }

    if (!p || !p.isPrimary) return;
    p.llx = e.clientX; p.lly = e.clientY;

    let tx = lp.x, ty = lp.y;
    if (e.pointerType === 'touch') ty = Math.min(CFG.H, ty - 80);
    if (S.paddle) S.paddle.target = { x: tx, y: ty };

    if (e.pointerType === 'touch') {
      lastTouch.x = lp.x; lastTouch.y = lp.y;
    }
    if (isMouse) e.preventDefault();
  }

  function onPointerUp(e) {
    const wasPrimary = pointers.get(e.pointerId) && pointers.get(e.pointerId).isPrimary;
    pointers.delete(e.pointerId);

    if (wasPrimary) {
      /* promote another pointer if any remain */
      const rest = activePointers();
      if (rest.length) setPrimary(rest[0]);
      else {
        setPrimary(null);
        releaseTarget();
        lastTouch.active = false;
        touchActive = false;
      }
    }
  }

  /* ---------------- keyboard ---------------- */

  const MOVE_KEYS = {
    ArrowLeft: 'left', ArrowRight: 'right', ArrowUp: 'up', ArrowDown: 'down'
  };

  function onKeyDown(e) {
    unlockAudio();
    const code = e.code || e.key;

    /* record raw + normalized */
    if (code in MOVE_KEYS) {
      S.keys[code] = true;
      S.keys[MOVE_KEYS[code]] = true;
      e.preventDefault();
      return;
    }

    S.keys[code] = true;

    if (code === 'Space') {
      e.preventDefault();
      primaryAction();
      return;
    }
    if (code === 'KeyP' || code === 'Escape') {
      if (typeof G !== 'undefined' && G.togglePause) G.togglePause();
      return;
    }
    if (code === 'KeyM') {
      if (typeof AUD !== 'undefined' && AUD.setMuted) {
        AUD.setMuted(!AUD.isMuted());
        syncUI();
      }
      return;
    }
    if (code === 'KeyR') {
      if (typeof G !== 'undefined' && G.startGame) G.startGame();
      return;
    }
  }

  function onKeyUp(e) {
    const code = e.code || e.key;
    if (code in MOVE_KEYS) {
      S.keys[code] = false;
      S.keys[MOVE_KEYS[code]] = false;
      return;
    }
    S.keys[code] = false;
  }

  /* keyboard moves the paddle target by velocity (held keys) */
  function updateKeys(dt) {
    if (S.state !== 'playing') return;
    if (!S.paddle) return;
    const k = S.keys || {};
    const dx = ((k.right || k.ArrowRight) ? 1 : 0) - ((k.left || k.ArrowLeft) ? 1 : 0);
    const dy = ((k.down || k.ArrowDown) ? 1 : 0) - ((k.up || k.ArrowUp) ? 1 : 0);
    if (!dx && !dy) return;
    /* no pointer target yet (keyboard-only player): create an implicit one at
       the paddle's current position so arrows can steer without a tap/click */
    let t = S.paddle.target;
    if (!t) {
      t = { x: S.paddle.x, y: S.paddle.y };
      S.paddle.target = t;
    }
    const v = 940 * Math.min(dt, 0.05);
    t.x = clamp(t.x + dx * v, 40, CFG.W - 40);
    t.y = clamp(t.y + dy * v, 60, CFG.H);
  }

  /* ---------------- HUD buttons ---------------- */

  function syncUI() {
    if (!document) return;
    const fireBtn = document.getElementById('btnFire');
    const muteBtn = document.getElementById('btnMute');
    const pauseBtn = document.getElementById('btnPause');
    const playing = S.state === 'playing';

    if (fireBtn) {
      const want = playing && !!S.fireReady;
      const has = fireBtn.classList.contains('on');
      if (want !== has) fireBtn.classList.toggle('on', want);
    }
    if (muteBtn) {
      const muted = typeof AUD !== 'undefined' && AUD.isMuted ? AUD.isMuted() : false;
      const wantIcon = muted ? '\u{1F507}' : '\u{1F50A}';          // 🔇 / 🔊,
      if (muteBtn.textContent !== wantIcon) muteBtn.textContent = wantIcon;
    }
    if (pauseBtn) {
      const wantIcon = (S.state === 'pause') ? '\u25B6' : '\u23F8'; // ▶ / ⏸
      if (pauseBtn.textContent !== wantIcon) pauseBtn.textContent = wantIcon;
    }
  }

  function onVisibility() {
    if (document.hidden && S.state === 'playing' && typeof G !== 'undefined' && G.pauseGame) {
      G.pauseGame();
    }
  }

  /* ---------------- lifecycle ---------------- */

  function init(cv) {
    canvas = cv;
    S.keys = S.keys || {};
    for (const c of ['left', 'right', 'up', 'down', 'ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', 'Space']) {
      if (!(c in S.keys)) S.keys[c] = false;
    }

    cv.addEventListener('pointerdown', onPointerDown);
    cv.addEventListener('pointermove', onPointerMove);
    cv.addEventListener('pointerup', onPointerUp);
    cv.addEventListener('pointercancel', onPointerUp);

    /* keep mouse target in sync while playing even before first click */
    cv.addEventListener('pointermove', e => {
      if (e.pointerType !== 'mouse') return;
      if (S.state === 'playing' && S.paddle && !pointers.has(e.pointerId)) {
        const lp = toLogicalFromClient(e.clientX, e.clientY);
        S.paddle.target = { x: lp.x, y: lp.y };
      }
    });

    window.addEventListener('keydown', onKeyDown);
    window.addEventListener('keyup', onKeyUp);
    document.addEventListener('visibilitychange', onVisibility);
    window.addEventListener('blur', onVisibility);
    window.addEventListener('contextmenu', e => { if (e.target === cv) e.preventDefault(); });

    /* HUD buttons */
    const fireBtn = document.getElementById('btnFire');
    const muteBtn = document.getElementById('btnMute');
    const pauseBtn = document.getElementById('btnPause');

    if (fireBtn) {
      fireBtn.addEventListener('pointerdown', e => { e.stopPropagation(); e.preventDefault(); primaryAction(); });
      fireBtn.addEventListener('click', e => { e.stopPropagation(); e.preventDefault(); primaryAction(); });
    }
    if (muteBtn) {
      muteBtn.addEventListener('click', e => {
        e.stopPropagation(); e.preventDefault();
        if (typeof AUD !== 'undefined' && AUD.setMuted) {
          AUD.setMuted(!AUD.isMuted());
          syncUI();
        }
      });
    }
    if (pauseBtn) {
      pauseBtn.addEventListener('click', e => {
        e.stopPropagation(); e.preventDefault();
        if (typeof G !== 'undefined' && G.togglePause) G.togglePause();
        syncUI();
      });
    }

    syncUI();
  }

  function setMode(m) { mode = m; }

  /* fix active touch mapping after a resize (coords re-derived on move anyway) */
  function onResize() {
    /* stored client coords stay valid in CSS px; nothing else to remap */
  }

  return {
    init, setMode, onResize, updateKeys, syncUI,
    get touchActive() { return touchActive; },
    lastTouch,
    isTouchActive: () => touchActive
  };
})();
