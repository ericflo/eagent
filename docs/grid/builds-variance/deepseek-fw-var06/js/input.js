// input.js — unified input: mouse, touch drag, on-screen virtual thumbstick.
// Normalizes everything to a paddle target position in world units (y,x handled
// separately so a mouse can hover while a thumbstick moves). First-class mobile.

import { WORLD_W, WORLD_H, PADDLE_MIN_X, PADDLE_MAX_X, PADDLE_MIN_Y, PADDLE_MAX_Y } from './engine.js';
import { clamp } from './engine.js';

export class Input {
  constructor(canvas, stickEl) {
    this.canvas = canvas;
    this.stickEl = stickEl;
    this.enabled = true;

    this.mouseX = WORLD_W / 2;
    this.mouseY = PADDLE_MAX_Y;
    this.mouseInside = false;

    this.stickActive = false;
    this.stickId = null;
    this.stickOrigin = { x: 0, y: 0 };
    this.stickVector = { x: 0, y: 0 };   // -1..1

    // last blended target
    this.targetX = WORLD_W / 2;
    this.targetY = PADDLE_MAX_Y;

    this._bind();
  }

  _toWorld(evt) {
    const rect = this.canvas.getBoundingClientRect();
    const wx = ((evt.clientX - rect.left) / rect.width) * WORLD_W;
    const wy = ((evt.clientY - rect.top) / rect.height) * WORLD_H;
    return { x: clamp(wx, 0, WORLD_W), y: clamp(wy, 0, WORLD_H) };
  }

  _bind() {
    const c = this.canvas;

    c.addEventListener('mousemove', (e) => {
      if (!this.enabled) return;
      const p = this._toWorld(e);
      this.mouseX = p.x;
      this.mouseY = p.y;
      this.mouseInside = true;
    });
    c.addEventListener('mouseleave', () => {
      this.mouseInside = false;
    });
    c.addEventListener('mousedown', () => {
      if (!this.enabled) return;
      this.mouseInside = true;
    });

    // touch: the first touch anywhere becomes the stick (thumb-friendly);
    // a second finger can still steer by dragging (mouse path).
    c.addEventListener('touchstart', (e) => {
      if (!this.enabled) return;
      for (const t of e.changedTouches) {
        if (this.stickId === null) {
          this.stickId = t.identifier;
          const p = this._toWorld(t);
          this.stickOrigin = { x: p.x, y: p.y };
          this.stickActive = true;
          this.stickVector = { x: 0, y: 0 };
          this._showStick(true, p.x, p.y);
          e.preventDefault();
          return;
        }
      }
    }, { passive: false });

    c.addEventListener('touchmove', (e) => {
      if (!this.enabled) return;
      for (const t of e.changedTouches) {
        if (t.identifier === this.stickId) {
          const p = this._toWorld(t);
          const dx = p.x - this.stickOrigin.x;
          const dy = p.y - this.stickOrigin.y;
          const dead = 6; // world units dead zone
          const mag = Math.hypot(dx, dy);
          const vec = mag > dead ? { x: dx / mag, y: dy / mag, mag: Math.min(1, mag / 90) } : { x: 0, y: 0, mag: 0 };
          this.stickVector = vec;
          this._updateStickVisual(vec);
          e.preventDefault();
          return;
        }
      }
    }, { passive: false });

    const endTouch = (e) => {
      if (this.stickId === null) return;
      for (const t of e.changedTouches) {
        if (t.identifier === this.stickId) {
          this.stickId = null;
          this.stickActive = false;
          this.stickVector = { x: 0, y: 0 };
          this._showStick(false);
          return;
        }
      }
    };
    c.addEventListener('touchend', endTouch);
    c.addEventListener('touchcancel', endTouch);

    // launch gesture: tap on the ball area while serving, or click
    this.onLaunch = null;
    c.addEventListener('pointerdown', (e) => {
      if (!this.enabled) return;
      const p = this._toWorld(e);
      if (this.onLaunch) this.onLaunch(p.x, p.y);
      // also start/prime audio (handled by game layer separately)
    });
  }

  _showStick(on, x, y) {
    if (!this.stickEl) return;
    this.stickEl.style.display = on ? 'block' : 'none';
    if (on) {
      const r = this.stickEl.getBoundingClientRect();
      this.stickEl.style.left = (x / WORLD_W * this.canvas.getBoundingClientRect().width - r.width / 2) + 'px';
      this.stickEl.style.top = (y / WORLD_H * this.canvas.getBoundingClientRect().height - r.height / 2) + 'px';
    }
    this.stickKnob = on ? this.stickEl.querySelector('.knob') : null;
  }

  _updateStickVisual(vec) {
    if (!this.stickEl || !this.stickKnob) return;
    const range = 30;
    this.stickKnob.style.transform = `translate(${vec.x * vec.mag * range}px, ${vec.y * vec.mag * range}px)`;
  }

  // called every frame by game; produces blended target
  poll(dt) {
    if (!this.enabled) {
      return { x: this.targetX, y: this.targetY };
    }
    let tx = this.mouseInside ? this.mouseX : WORLD_W / 2;
    let ty = this.mouseInside ? this.mouseY : PADDLE_MAX_Y;
    if (this.stickActive) {
      const v = this.stickVector;
      const sx = this._toWorld({ clientX: this.stickOrigin.x * 0 + (this.canvas.getBoundingClientRect().left + (this.stickOrigin.x / WORLD_W) * this.canvas.getBoundingClientRect().width), clientY: 0 });
      tx = sx.x;
      ty = sx.y;
      // blend: stick drag from origin + vector offset in world units
      const offX = v.x * 170 * (v.mag || 0.6);
      const offY = v.y * 150 * (v.mag || 0.6);
      tx = clamp(this.stickOrigin.x + offX, 0, WORLD_W);
      ty = clamp(this.stickOrigin.y + offY, PADDLE_MIN_Y, PADDLE_MAX_Y);
      // if the user is also using the mouse, mouse wins for x
      if (this.mouseInside) tx = this.mouseX;
    }
    this.targetX = tx;
    this.targetY = ty;
    return { x: tx, y: ty };
  }

  // mobile detection helper
  static isTouch() {
    return (typeof window !== 'undefined') && ('ontouchstart' in window || navigator.maxTouchPoints > 0);
  }
}
