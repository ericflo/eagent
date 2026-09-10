'use strict';
/* ============================================================
   Attic Breakout — input.js
   First-class controls:
   • Mouse: paddle seeks the pointer (smoothed), click to launch.
   • Keyboard: WASD/arrows + Space launch, P/Esc pause.
   • Touch: floating thumbstick (default) or follow-finger mode.
   The game object receives normalized intents; input never
   touches game state directly except through callbacks.
   ============================================================ */

const InputMode = { MOUSE: 'mouse', KEYBOARD: 'keyboard', TOUCH: 'touch' };

class Input {
  constructor(canvas, game) {
    this.canvas = canvas;
    this.game = game;
    this.mode = InputMode.MOUSE;
    this.scheme = storageGet('ab.scheme', 'auto'); // auto | mouse | keyboard | touch
    this.followFinger = !!storageGet('ab.followFinger', false);

    // Pointer target in canvas space (game uses with smoothing).
    this.pointerActive = false;
    this.px = CONFIG.W / 2; this.py = CONFIG.PADDLE_ZONE_BOTTOM - 40;

    // Keyboard state
    this.keys = {};

    // Thumbstick state
    this.stick = { active: false, id: null, ox: 0, oy: 0, x: 0, y: 0, flickT: 0 };

    this._bind();
  }

  isTouchDevice() {
    return ('ontouchstart' in window) || navigator.maxTouchPoints > 0;
  }

  /** Effective scheme used this frame. */
  activeScheme() {
    if (this.scheme === 'mouse') return InputMode.MOUSE;
    if (this.scheme === 'keyboard') return InputMode.KEYBOARD;
    if (this.scheme === 'touch') return InputMode.TOUCH;
    return this.mode; // auto: last used
  }

  _bind() {
    const c = this.canvas;

    // Unified pointer events (mouse + pen + touch via pointer events where supported)
    c.addEventListener('pointerdown', (e) => {
      e.preventDefault();
      this.game.audio.unlock();
      const p = this._toCanvas(e);
      if (e.pointerType === 'touch') {
        this.mode = InputMode.TOUCH;
        this._stickStart(e);
      } else {
        this.mode = InputMode.MOUSE;
        this.pointerActive = true;
        this.px = p.x; this.py = p.y;
      }
    });
    c.addEventListener('pointermove', (e) => {
      const p = this._toCanvas(e);
      if (e.pointerType === 'touch') {
        this.mode = InputMode.TOUCH;
        this._stickMove(e);
      } else {
        this.mode = InputMode.MOUSE;
        this.pointerActive = true;
        this.px = p.x; this.py = p.y;
      }
    });
    const up = (e) => {
      if (e.pointerType === 'touch') this._stickEnd(e);
      else this.pointerActive = false;
    };
    c.addEventListener('pointerup', up);
    c.addEventListener('pointercancel', up);
    c.addEventListener('pointerleave', (e) => { if (e.pointerType !== 'touch') this.pointerActive = false; });

    // Block native gestures on touch.
    c.addEventListener('touchstart', (e) => e.preventDefault(), { passive: false });
    c.addEventListener('touchmove', (e) => e.preventDefault(), { passive: false });
    c.addEventListener('touchend', (e) => e.preventDefault(), { passive: false });
    document.addEventListener('gesturestart', (e) => e.preventDefault());
    document.addEventListener('dblclick', (e) => e.preventDefault());

    window.addEventListener('keydown', (e) => {
      this.game.audio.unlock();
      const k = e.key.toLowerCase();
      if (['arrowup', 'arrowdown', 'arrowleft', 'arrowright', ' '].includes(k)) e.preventDefault();
      if (!this.keys[k]) { // fresh press
        if (k === ' ') this.game.onLaunchKey();
        if (k === 'p' || k === 'escape') this.game.onPauseKey();
        if (k === 'm') this.game.onMuteKey();
      }
      this.keys[k] = true;
      if (this.scheme === 'auto' || this.scheme === 'touch') {
        if (['w', 'a', 's', 'd', 'arrowup', 'arrowdown', 'arrowleft', 'arrowright'].includes(k)) {
          this.mode = InputMode.KEYBOARD;
        }
      }
    });
    window.addEventListener('keyup', (e) => { this.keys[e.key.toLowerCase()] = false; });

    window.addEventListener('blur', () => { this.keys = {}; });
  }

  /** Client coords → internal 720×1000 canvas coords. */
  _toCanvas(e) {
    const r = this.canvas.getBoundingClientRect();
    return {
      x: (e.clientX - r.left) / r.width * CONFIG.W,
      y: (e.clientY - r.top) / r.height * CONFIG.H,
    };
  }

  // ---- floating thumbstick ----------------------------------------------

  _stickStart(e) {
    if (this.stick.active) return;
    const p = this._toCanvas(e);
    this.stick.active = true;
    this.stick.id = e.pointerId;
    this.stick.ox = p.x; this.stick.oy = p.y;
    this.stick.x = 0; this.stick.y = 0;
    this.stick.flickT = 0;
    this.stick.lastY = p.y;
    this.stick.movedY = 0;
  }
  _stickMove(e) {
    if (!this.stick.active || e.pointerId !== this.stick.id) return;
    const p = this._toCanvas(e);
    const dx = p.x - this.stick.ox, dy = p.y - this.stick.oy;
    const maxR = 56;
    const d = Math.hypot(dx, dy);
    const k = d > maxR ? maxR / d : 1;
    this.stick.x = dx * k; this.stick.y = dy * k;
    this.stick.movedY += p.y - this.stick.lastY;
    this.stick.lastY = p.y;
    if (this.stick.movedY < -34) this.stick.flickT = 0.25; // flick up = rise
  }
  _stickEnd(e) {
    if (e.pointerId !== this.stick.id) return;
    this.stick.active = false;
    this.stick.id = null;
    this.stick.x = 0; this.stick.y = 0;
  }

  /** Where the paddle should seek this frame (canvas coords), or null. */
  getPaddleTarget() {
    const scheme = this.activeScheme();
    if (scheme === InputMode.TOUCH) {
      if (this.followFinger) {
        // Follow latest touch, offset above the thumb so it stays visible.
        if (this.stick.active || this._lastTouch) {
          const p = this.stick.active
            ? { x: this.stick.ox + this.stick.x, y: this.stick.oy + this.stick.y }
            : this._lastTouch;
          this._lastTouch = p;
          const flick = this.stick.flickT > 0 ? -120 : 0;
          if (this.stick.flickT > 0) this.stick.flickT -= 1 / 60;
          return { x: p.x, y: clamp(p.y - 90 + flick, CONFIG.PADDLE_ZONE_TOP, CONFIG.PADDLE_ZONE_BOTTOM) };
        }
        return null;
      }
      // Thumbstick: paddle velocity = stick deflection * speed.
      this._stickVX = this.stick.x / 56 * 1500;
      this._stickVY = this.stick.y / 56 * 1100 - (this.stick.flickT > 0 ? 700 : 0);
      if (this.stick.flickT > 0) this.stick.flickT -= 1 / 60;
      return { stick: true, vx: this._stickVX, vy: this._stickVY };
    }
    if (scheme === InputMode.MOUSE) {
      if (!this.pointerActive) return null;
      return { x: this.px, y: this.py };
    }
    // Keyboard
    const sp = 1150;
    let dx = 0, dy = 0;
    if (this.keys['a'] || this.keys['arrowleft']) dx -= 1;
    if (this.keys['d'] || this.keys['arrowright']) dx += 1;
    if (this.keys['w'] || this.keys['arrowup']) dy -= 1;
    if (this.keys['s'] || this.keys['arrowdown']) dy += 1;
    if (dx || dy) {
      const n = Math.hypot(dx, dy);
      return { vel: { x: dx / n * sp, y: dy / n * sp } };
    }
    return { vel: { x: 0, y: 0 } };
  }

  /** Draw on-screen touch controls (thumbstick + launch + pause). */
  drawTouchUI(ctx, game) {
    const scheme = this.activeScheme();
    if (scheme !== InputMode.TOUCH) return;
    // Launch / pause buttons (always, thumb-reachable corners)
    const lr = 34;
    const lx = CONFIG.W - 60, ly = CONFIG.H - 70;
    ctx.save();
    ctx.globalAlpha = 0.55;
    ctx.fillStyle = 'rgba(255,255,255,0.12)';
    ctx.strokeStyle = 'rgba(255,255,255,0.5)';
    ctx.lineWidth = 2;
    ctx.beginPath(); ctx.arc(lx, ly, lr, 0, TAU); ctx.fill(); ctx.stroke();
    ctx.fillStyle = '#fff';
    ctx.font = `bold 15px ${UI.FONT}`;
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    ctx.fillText('LAUNCH', lx, ly);
    ctx.restore();
    this.launchBtn = { x: lx, y: ly, r: lr };

    // Thumbstick base + knob while active
    if (this.stick.active) {
      const s = this.stick;
      ctx.save();
      ctx.globalAlpha = 0.5;
      ctx.strokeStyle = 'rgba(140,200,255,0.8)';
      ctx.lineWidth = 2.5;
      ctx.beginPath(); ctx.arc(s.ox, s.oy, 56, 0, TAU); ctx.stroke();
      ctx.fillStyle = 'rgba(140,200,255,0.35)';
      ctx.beginPath();
      ctx.arc(s.ox + s.x, s.oy + s.y, 24, 0, TAU);
      ctx.fill();
      ctx.restore();
    }
    this.pauseBtn = game.pauseBtnRect;
    if (this.activeScheme() !== InputMode.TOUCH) this.launchBtn = null;
  }

  /** Hit test for on-screen buttons; returns 'launch' | 'pause' | null. */
  hitButton(p) {
    if (this.launchBtn) {
      const d = Math.hypot(p.x - this.launchBtn.x, p.y - this.launchBtn.y);
      if (d < this.launchBtn.r + 12) return 'launch';
    }
    if (this.game.pauseBtnRect) {
      const b = this.game.pauseBtnRect;
      if (p.x > b.x && p.x < b.x + b.w && p.y > b.y && p.y < b.y + b.h) return 'pause';
    }
    return null;
  }

  setScheme(s) { this.scheme = s; storageSet('ab.scheme', s); }
  setFollowFinger(f) { this.followFinger = f; storageSet('ab.followFinger', f); }
}

window.Input = Input;
window.InputMode = InputMode;
