// ---------------------------------------------------------------------------
// input.js — keyboard, mouse, touch (drag + virtual stick modes)
// ---------------------------------------------------------------------------

import { W, H, PADDLE, WALL } from './config.js';
import { audio } from './audio.js';
import { clamp } from './utils.js';

export class Input {
  constructor(canvas) {
    this.canvas = canvas;
    this.mode = 'drag';                       // 'drag' | 'stick'
    this.scale = 1;                           // css px -> logical px
    // paddle target position (logical coords)
    this.tx = W / 2;
    this.ty = (PADDLE.bandTop + PADDLE.bandBottom) / 2;
    this.usingPointer = false;

    // keyboard state
    this.keys = new Set();
    this.onLaunch = null;
    this.onPause = null;

    // touch / pointer session
    this._pid = null;
    this._startX = 0; this._startY = 0;       // finger origin (css px)
    this._grabX = 0; this._grabY = 0;         // paddle pos at grab (logical)
    this._stick = { active: false, x: 0, y: 0, dx: 0, dy: 0, id: null, sx: 0, sy: 0 };
    this._stickOriginX = 0; this._stickOriginY = 0;

    this._bind();
  }

  setScale(scale) { this.scale = scale; }

  _bind() {
    const c = this.canvas;
    const opt = { passive: false };

    window.addEventListener('keydown', (e) => {
      if (e.repeat) return;
      const k = e.key.toLowerCase();
      this.keys.add(k);
      if (k === ' ' || k === 'enter') { e.preventDefault(); this.onLaunch && this.onLaunch(); }
      if (k === 'p' || k === 'escape') { this.onPause && this.onPause(); }
    });
    window.addEventListener('keyup', (e) => this.keys.delete(e.key.toLowerCase()));
    window.addEventListener('blur', () => this.keys.clear());

    // ---- mouse -------------------------------------------------------------
    c.addEventListener('mousemove', (e) => {
      const r = c.getBoundingClientRect();
      const x = (e.clientX - r.left) * this.scale;
      const y = (e.clientY - r.top) * this.scale;
      // In the brick region (upper area) keep paddle at last y; else follow.
      if (y > H * 0.55 || y >= PADDLE.bandTop - 40) this._pointerTo(x, y);
      else this._pointerTo(x, null);
    });

    // ---- touch -------------------------------------------------------------
    c.addEventListener('touchstart', (e) => {
      e.preventDefault();
      for (const t of e.changedTouches) {
        if (this._pid !== null || this._stick.active) continue;
        this._pid = t.identifier;
        this._startX = t.clientX; this._startY = t.clientY;
        this._grabX = this.tx; this._grabY = this.ty;
        if (this.mode === 'stick') {
          this._stick.active = true;
          this._stick.id = t.identifier;
          this._stickOriginX = t.clientX; this._stickOriginY = t.clientY;
          this._stick.sx = t.clientX; this._stick.sy = t.clientY;
          this._stick.dx = 0; this._stick.dy = 0;
        }
      }
    }, opt);

    c.addEventListener('touchmove', (e) => {
      e.preventDefault();
      for (const t of e.changedTouches) {
        if (t.identifier !== this._pid) continue;
        const dx = (t.clientX - this._startX) * this.scale;
        const dy = (t.clientY - this._startY) * this.scale;
        if (this.mode === 'drag') {
          const nx = clamp(this._grabX + dx, WALL + PADDLE.w / 2, W - WALL - PADDLE.w / 2);
          const ny = clamp(this._grabY + dy, PADDLE.bandTop, PADDLE.bandBottom);
          this.tx = nx; this.ty = ny;
        } else {
          // stick: recenter origin if finger travels beyond radius (sliding stick)
          const R = 110; // css px
          let vx = t.clientX - this._stickOriginX;
          let vy = t.clientY - this._stickOriginY;
          const d = Math.hypot(vx, vy);
          if (d > R) {
            this._stickOriginX = t.clientX - (vx / d) * R;
            this._stickOriginY = t.clientY - (vy / d) * R;
            vx = (vx / d) * R; vy = (vy / d) * R;
          }
          this._stick.dx = vx / R; this._stick.dy = vy / R;
          this._stick.sx = t.clientX; this._stick.sy = t.clientY;
        }
      }
    }, opt);

    const end = (e) => {
      e.preventDefault();
      for (const t of e.changedTouches) {
        if (t.identifier === this._pid) { this._pid = null; }
        if (t.identifier === this._stick.id) {
          this._stick.active = false; this._stick.id = null;
          this._stick.dx = 0; this._stick.dy = 0;
        }
      }
    };
    c.addEventListener('touchend', end, opt);
    c.addEventListener('touchcancel', end, opt);

    // tap / click anywhere: launch, start, or restart.
    // Guarded so a single tap produces ONE launch: touch devices fire both
    // pointerdown and touchstart, which previously double-fired tryLaunch
    // and skipped straight through SERVE (no launch hint ever visible).
    let lastTapAt = 0;
    const tap = (e) => {
      const now = performance.now();
      if (now - lastTapAt < 400) return;   // swallow the synthetic twin
      lastTapAt = now;
      try { audio.start(); audio.resume(); } catch (_) {}
      if (this.onLaunch) this.onLaunch();
    };
    c.addEventListener('pointerdown', tap);
    c.addEventListener('touchstart', tap, { passive: false });

    // Prevent gestures
    c.addEventListener('touchstart', (e) => e.preventDefault(), opt);
    document.addEventListener('gesturestart', (e) => e.preventDefault());
    document.addEventListener('dblclick', (e) => e.preventDefault());
  }

  _pointerTo(x, y) {
    this.tx = clamp(x, WALL + PADDLE.w / 2, W - WALL - PADDLE.w / 2);
    if (y !== null) this.ty = clamp(y, PADDLE.bandTop, PADDLE.bandBottom);
    this.usingPointer = true;
  }

  /** per-frame update. Returns desired paddle velocity {vx, vy}. */
  update(dt, px, py) {
    // keyboard steer
    const kx = (this.keys.has('arrowright') || this.keys.has('d') ? 1 : 0)
      - (this.keys.has('arrowleft') || this.keys.has('a') ? 1 : 0);
    const ky = (this.keys.has('arrowdown') || this.keys.has('s') ? 1 : 0)
      - (this.keys.has('arrowup') || this.keys.has('w') ? 1 : 0);
    if (kx || ky) {
      this.usingPointer = false;
      const spd = PADDLE.maxV;
      this.tx = clamp(this.tx + kx * spd * dt, WALL + PADDLE.w / 2, W - WALL - PADDLE.w / 2);
      this.ty = clamp(this.ty + ky * spd * dt, PADDLE.bandTop, PADDLE.bandBottom);
    } else if (this.mode === 'stick' && this._stick.active) {
      const spd = PADDLE.maxV * 0.85;
      this.tx = clamp(this.tx + this._stick.dx * spd * dt, WALL + PADDLE.w / 2, W - WALL - PADDLE.w / 2);
      this.ty = clamp(this.ty + this._stick.dy * spd * dt, PADDLE.bandTop, PADDLE.bandBottom);
    }

    // desired velocity = (target - current) / dt, clamped
    const vx = clamp((this.tx - px) / Math.max(dt, 1e-4), -PADDLE.maxV * 1.4, PADDLE.maxV * 1.4);
    const vy = clamp((this.ty - py) / Math.max(dt, 1e-4), -PADDLE.maxV * 1.4, PADDLE.maxV * 1.4);
    return { vx, vy };
  }

  /** stick visual state for rendering (css px + normalized). */
  stickVisual() {
    if (!this._stick.active) return null;
    return { sx: this._stickOriginX, sy: this._stickOriginY, kx: this._stick.sx, ky: this._stick.sy, dx: this._stick.dx, dy: this._stick.dy };
  }

  consumeTap() {
    // treat any fresh pointer/touch as launch (checked by game via event)
  }
}
