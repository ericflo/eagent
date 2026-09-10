// Input: pointer (mouse + touch drag), optional virtual thumbstick, keyboard.
// Coordinates are reported in logical field space via a transform set by main.
import { PADDLE } from './constants.js';

export class Input {
  constructor(canvas, getScale) {
    this.canvas = canvas;
    this.getScale = getScale; // () => ({sx, sy, ox, oy}) field<-client mapping
    this.pointer = { x: null, y: null, active: false };
    this.stick = { on: localStorage.getItem('bt_stick') === '1', active: false, dx: 0, dy: 0, cx: 0, cy: 0, id: null };
    this.keys = new Set();
    this.launchRequested = false;
    this.pauseRequested = false;
    this._kbX = 0; this._kbY = 0; // keyboard-driven velocity (logical units/sec)
    this._bind();
  }
  _toField(e) {
    const { sx, sy, ox, oy } = this.getScale();
    return { x: (e.clientX - ox) / sx, y: (e.clientY - oy) / sy };
  }
  _bind() {
    const c = this.canvas;
    const onPointer = (e) => {
      if (this.stick.on && e.pointerType === 'touch' && !this.stick.active) return; // stick handles touch
      const p = this._toField(e);
      this.pointer.x = p.x; this.pointer.y = p.y; this.pointer.active = true;
    };
    c.addEventListener('pointermove', (e) => {
      if (this.stick.on && e.pointerType === 'touch') {
        if (this.stick.active && e.pointerId === this.stick.id) this._stickMove(e);
        return;
      }
      onPointer(e);
    });
    c.addEventListener('pointerdown', (e) => {
      if (this.stick.on && e.pointerType === 'touch') { this._stickStart(e); return; }
      onPointer(e);
      this.launchRequested = true;
      this.resumeAudio && this.resumeAudio();
    });
    c.addEventListener('pointerup', (e) => {
      if (this.stick.active && e.pointerId === this.stick.id) this._stickEnd(e);
      else this.pointer.active = false;
    });
    c.addEventListener('pointercancel', (e) => {
      if (this.stick.active && e.pointerId === this.stick.id) this._stickEnd(e);
    });
    // thumbstick zone visuals handled in render via input.stick state
    window.addEventListener('keydown', (e) => {
      if (e.repeat) { if (['Space','ArrowUp','ArrowDown','ArrowLeft','ArrowRight'].includes(e.code)) e.preventDefault(); return; }
      this.keys.add(e.code);
      this.resumeAudio && this.resumeAudio();
      if (e.code === 'Space') { e.preventDefault(); this.launchRequested = true; }
      if (e.code === 'KeyP' || e.code === 'Escape') this.pauseRequested = true;
      if (e.code === 'KeyM') this.muteToggleRequested = true;
    });
    window.addEventListener('keyup', (e) => this.keys.delete(e.code));
    window.addEventListener('blur', () => this.keys.clear());
  }
  _stickStart(e) {
    e.preventDefault();
    this.stick.active = true; this.stick.id = e.pointerId;
    this.stick.bx = e.clientX; this.stick.by = e.clientY; // base (center of stick)
    this.stick.cx = e.clientX; this.stick.cy = e.clientY;
    this.stick.dx = 0; this.stick.dy = 0;
    this.launchRequested = true;
    this.resumeAudio && this.resumeAudio();
  }
  _stickMove(e) {
    this.stick._cx = e.clientX; this.stick._cy = e.clientY;
    this.stick.cx = e.clientX; this.stick.cy = e.clientY;
  }
  _stickEnd(e) {
    if (e.pointerId !== this.stick.id) return;
    this.stick.active = false; this.stick.dx = 0; this.stick.dy = 0; this.stick.id = null;
  }
  toggleStick() {
    this.stick.on = !this.stick.on;
    localStorage.setItem('bt_stick', this.stick.on ? '1' : '0');
    if (!this.stick.on) this._stickEnd({ pointerId: this.stick.id });
    return this.stick.on;
  }
  // dx,dy in [-1,1] desired paddle velocity direction this frame
  sample(dt) {
    let dx = 0, dy = 0;
    if (this.stick.active) {
      const R = 55;
      const mx = (this.stick._cx ?? this.stick.cx) - this.stick.bx;
      const my = (this.stick._cy ?? this.stick.cy) - this.stick.by;
      const d = Math.hypot(mx, my);
      if (d > 6) {
        const k = Math.min(1, d / R);
        dx = (mx / d) * k; dy = (my / d) * k;
      }
    }
    if (!this.stick.active || (dx === 0 && dy === 0)) {
      if (this.keys.has('ArrowLeft') || this.keys.has('KeyA')) dx -= 1;
      if (this.keys.has('ArrowRight') || this.keys.has('KeyD')) dx += 1;
      if (this.keys.has('ArrowUp') || this.keys.has('KeyW')) dy -= 1;
      if (this.keys.has('ArrowDown') || this.keys.has('KeyS')) dy += 1;
    }
    const usePointer = !this.stick.active && this.pointer.active && dx === 0 && dy === 0;
    return { dx, dy, usePointer, pointer: this.pointer };
  }
  consumeLaunch() { const l = this.launchRequested; this.launchRequested = false; return l; }
  consumePause() { const p = this.pauseRequested; this.pauseRequested = false; return p; }
  consumeMuteToggle() { const m = this.muteToggleRequested; this.muteToggleRequested = false; return m; }
}
