// engine.js — canvas/DPR/letterbox, fixed-timestep loop with interpolation,
// and the unified pointer/touch-thumbstick/keyboard input abstraction.
export const WORLD_W = 540;
export const WORLD_H = 960;
export const STEP = 1 / 120; // 120 Hz physics

// Paddle vertical band: bottom ~220px of the playfield.
export const BAND_TOP = WORLD_H - 220;
export const BAND_BOTTOM = WORLD_H - 10;

export function clamp(v, a, b) { return v < a ? a : v > b ? b : v; }
export function lerp(a, b, t) { return a + (b - a) * t; }
export function easeOutCubic(t) { return 1 - Math.pow(1 - t, 3); }

export class Engine {
  constructor(canvas) {
    this.canvas = canvas;
    this.ctx = canvas.getContext('2d');
    this.scale = 1; this.ox = 0; this.oy = 0;
    this.update = null;   // fn(dtScaled, dtReal)
    this.render = null;   // fn(alpha, ctx)
    this._acc = 0; this._last = 0; this._raf = 0;
    this.timeScale = 1;
    this.resize();
    window.addEventListener('resize', () => this.resize());
    window.addEventListener('orientationchange', () => setTimeout(() => this.resize(), 60));
  }

  resize() {
    const dpr = Math.min(window.devicePixelRatio || 1, 3);
    const w = window.innerWidth, h = window.innerHeight;
    this.canvas.width = Math.round(w * dpr);
    this.canvas.height = Math.round(h * dpr);
    this.canvas.style.width = w + 'px';
    this.canvas.style.height = h + 'px';
    this.dpr = dpr;
    this.viewW = w; this.viewH = h;
    this.scale = Math.min(w / WORLD_W, h / WORLD_H);
    this.ox = (w - WORLD_W * this.scale) / 2;
    this.oy = (h - WORLD_H * this.scale) / 2;
    if (this.onResize) this.onResize();
  }

  // screen -> world coordinates
  toWorld(sx, sy) {
    return { x: (sx - this.ox) / this.scale, y: (sy - this.oy) / this.scale };
  }

  start() {
    this._last = performance.now();
    const frame = (now) => {
      this._raf = requestAnimationFrame(frame);
      let real = (now - this._last) / 1000;
      this._last = now;
      if (real > 0.1) real = 0.1; // tab-back spike guard
      const scaled = real * this.timeScale;
      this._acc += scaled;
      let guard = 0;
      while (this._acc >= STEP && guard++ < 12) {
        this.update(STEP, real);
        this._acc -= STEP;
      }
      if (guard >= 12) this._acc = 0;
      const ctx = this.ctx;
      ctx.setTransform(this.dpr, 0, 0, this.dpr, 0, 0);
      ctx.clearRect(0, 0, this.viewW, this.viewH);
      // world transform
      ctx.setTransform(this.dpr * this.scale, 0, 0, this.dpr * this.scale,
        this.dpr * this.ox, this.dpr * this.oy);
      this.render(clamp(this._acc / STEP, 0, 1), ctx);
    };
    this._raf = requestAnimationFrame(frame);
  }
}

// ---------------------------------------------------------------------------
// Input: mouse pointer (absolute follow), touch floating thumbstick
// (drag-delta steering + flick-up smash), keyboard.
// All handlers are attached to window so overlays never eat the controls.
// ---------------------------------------------------------------------------
export class Input {
  constructor(engine) {
    this.engine = engine;
    this.pointer = { x: WORLD_W / 2, y: BAND_BOTTOM, active: false, type: 'mouse' };
    // thumbstick state
    this.stick = { active: false, id: null, originX: 0, originY: 0, x: 0, y: 0 };
    this.flickUp = false;       // consumed by paddle (one-shot)
    this.taps = [];             // queued taps (one-shot events)
    this.keys = new Set();
    this._lastTouchDown = 0;

    window.addEventListener('pointerdown', (e) => this._down(e));
    window.addEventListener('pointermove', (e) => this._move(e));
    window.addEventListener('pointerup', (e) => this._up(e));
    window.addEventListener('pointercancel', (e) => this._up(e));
    window.addEventListener('keydown', (e) => this._key(e, true));
    window.addEventListener('keyup', (e) => this._key(e, false));
  }

  _down(e) {
    if (e.target && e.target.closest && e.target.closest('.ui-button')) return;
    const p = this.engine.toWorld(e.clientX, e.clientY);
    if (e.pointerType === 'touch') {
      this.stick.active = true;
      this.stick.id = e.pointerId;
      this.stick.originX = p.x; this.stick.originY = p.y;
      this.stick.x = p.x; this.stick.y = p.y;
      this._lastTouchDown = performance.now();
    } else {
      this.pointer.active = true;
      this.pointer.x = p.x; this.pointer.y = p.y; this.pointer.type = 'mouse';
    }
    this.taps.push({ x: p.x, y: p.y });
    e.preventDefault();
  }

  _move(e) {
    const p = this.engine.toWorld(e.clientX, e.clientY);
    if (e.pointerType === 'touch') {
      if (this.stick.active && e.pointerId === this.stick.id) {
        const dx = p.x - this.stick.originX, dy = p.y - this.stick.originY;
        this.stick.x = p.x; this.stick.y = p.y;
        // flick-up = smash: fast upward stroke of the stick
        if (dy < -46 && !this.flickUp) {
          this.flickUp = true;
          this.stick.originY = p.y + 46; // rebase so a long drag can chain flicks
        }
      }
    } else {
      this.pointer.active = true;
      this.pointer.x = p.x; this.pointer.y = p.y; this.pointer.type = 'mouse';
    }
  }

  _up(e) {
    if (e.pointerType === 'touch' && e.pointerId === this.stick.id) {
      this.stick.active = false; this.stick.id = null;
    } else if (e.pointerType !== 'touch') {
      this.pointer.active = false;
    }
  }

  _key(e, down) {
    this.keys[e.code] = down;
    if (down) this.keys.add(e.code); else this.keys.delete(e.code);
    if (['ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight', 'Space'].includes(e.code)) e.preventDefault();
  }

  consumeTap() {
    return this.taps.length ? this.taps.shift() : null;
  }

  consumeFlick() {
    const f = this.flickUp;
    this.flickUp = false;
    return f;
  }

  // Desired paddle target position for this frame (world coords).
  // Mouse: absolute pointer. Touch: paddle origin + drag delta (floating
  // thumbstick — the thumb never has to cover the paddle).
  paddleTarget(curX, curY, keyboardX, keyboardY) {
    let tx = null, ty = null;
    if (this.stick.active) {
      const dx = this.stick.x - this.stick.originX;
      const dy = this.stick.y - this.stick.originY;
      tx = curX + dx * 1.6;
      ty = curY + dy * 1.6;
    } else if (this.pointer.active && this.pointer.type === 'mouse') {
      tx = this.pointer.x; ty = this.pointer.y;
    }
    if (tx === null && (this.keys.has('ArrowLeft') || this.keys.has('KeyA') ||
        this.keys.has('ArrowRight') || this.keys.has('KeyD') ||
        this.keys.has('ArrowUp') || this.keys.has('KeyW') ||
        this.keys.has('ArrowDown') || this.keys.has('KeyS'))) {
      tx = curX + (keyboardX || 0);
      ty = curY + (keyboardY || 0);
    }
    if (tx === null) { tx = curX; ty = curY; }
    return { x: tx, y: ty };
  }

  // Last known aim position: the most recent mouse pointer or touch knob,
  // even after release — used to aim a sticky ball launch.
  lastAim() {
    if (this.stick.active) return { x: this.stick.x, y: this.stick.y };
    if (this.pointer.x != null) return { x: this.pointer.x, y: this.pointer.y };
    return null;
  }
}
