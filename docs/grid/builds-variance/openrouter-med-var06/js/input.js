// Input: mouse, keyboard, touch (drag-anywhere relative thumbstick + tap).
// Emits a normalized paddle direction vector each frame: input.dir {x,y} in [-1,1].

export class Input {
  constructor(canvas) {
    this.canvas = canvas;
    this.keys = new Set();
    this.dir = { x: 0, y: 0 };
    this.pointerActive = false;   // mouse or touch currently steering
    this.steer = { x: 0, y: 0 };  // target position mode (mouse) OR velocity mode (touch)
    this.steerMode = 'none';      // 'mouse' | 'touch' | 'keys'
    this.launchRequested = false; // consumed each frame
    this.mousePos = null;

    // touch state
    this.touchId = null;
    this.touchOrigin = { x: 0, y: 0 };
    this.touchLast = { x: 0, y: 0 };
    this.touchAccum = { x: 0, y: 0 };
    this.touchMoved = false;
    this.touchStartTime = 0;
    this.deadZone = 8; // px before touch counts as steering
    this.stickWidget = document.getElementById('thumbstick');
    this.stickVisible = false;

    this._bind();
  }

  _bind() {
    const c = this.canvas;
    window.addEventListener('keydown', e => {
      if (['ArrowLeft','ArrowRight','ArrowUp','ArrowDown',' '].includes(e.key)) e.preventDefault();
      this.keys.add(e.code);
      if (e.code === 'Space') this.launchRequested = true;
    });
    window.addEventListener('keyup', e => this.keys.delete(e.code));

    c.addEventListener('mousemove', e => {
      const r = c.getBoundingClientRect();
      this.mousePos = { x: e.clientX - r.left, y: e.clientY - r.top };
      this.steerMode = 'mouse';
    });
    c.addEventListener('mousedown', e => {
      this.launchRequested = true;
      const r = c.getBoundingClientRect();
      this.mousePos = { x: e.clientX - r.left, y: e.clientY - r.top };
      this.steerMode = 'mouse';
      e.preventDefault();
    });

    // --- touch: relative drag anywhere ---
    c.addEventListener('touchstart', e => {
      e.preventDefault();
      const t = e.changedTouches[0];
      if (this.touchId === null) {
        this.touchId = t.identifier;
        const r = c.getBoundingClientRect();
        this.touchOrigin = { x: t.clientX - r.left, y: t.clientY - r.top };
        this.touchLast = { ...this.touchOrigin };
        this.touchAccum = { x: 0, y: 0 };
        this.touchMoved = false;
        this.touchStartTime = performance.now();
        this._showStick(this.touchOrigin);
        this._stickCenter = { ...this.touchOrigin };
      }
      this.steerMode = 'touch';
    }, { passive: false });

    c.addEventListener('touchmove', e => {
      e.preventDefault();
      const r = c.getBoundingClientRect();
      for (const t of e.changedTouches) {
        if (t.identifier !== this.touchId) continue;
        const x = t.clientX - r.left, y = t.clientY - r.top;
        const dx = x - this.touchLast.x, dy = y - this.touchLast.y;
        this.touchLast = { x, y };
        this.touchAccum.x += dx; this.touchAccum.y += dy;
        if (Math.hypot(this.touchAccum.x, this.touchAccum.y) > this.deadZone) this.touchMoved = true;
        this._updateStick();
      }
    }, { passive: false });

    const endTouch = e => {
      e.preventDefault();
      for (const t of e.changedTouches) {
        if (t.identifier !== this.touchId) continue;
        const dt = performance.now() - this.touchStartTime;
        if (!this.touchMoved && dt < 350) this.launchRequested = true; // quick tap = launch
        this.touchId = null;
        this.touchAccum = { x: 0, y: 0 };
        this._hideStick();
      }
    };
    c.addEventListener('touchend', endTouch, { passive: false });
    c.addEventListener('touchcancel', endTouch, { passive: false });
  }

  _showStick(pos) {
    if (!this.stickWidget) return;
    this.stickVisible = true;
    this.stickWidget.style.opacity = '1';
    this.stickWidget.style.left = (pos.x - 55) + 'px';
    this.stickWidget.style.top = (pos.y - 55) + 'px';
  }
  _updateStick() {
    if (!this.stickWidget || !this.stickVisible) return;
    // widget follows the finger (drag-anywhere relative stick moves its base)
    this.stickWidget.style.left = (this.touchLast.x - 55) + 'px';
    this.stickWidget.style.top = (this.touchLast.y - 55) + 'px';
    const max = 22;
    let { x, y } = this.touchAccum;
    const len = Math.hypot(x, y);
    if (len > max) { x = x / len * max; y = y / len * max; }
    const knob = this.stickWidget.querySelector('.knob');
    knob.style.transform = `translate(calc(-50% + ${x}px), calc(-50% + ${y}px))`;
  }
  _hideStick() {
    if (!this.stickWidget) return;
    this.stickVisible = false;
    this.stickWidget.style.opacity = '0';
    const knob = this.stickWidget.querySelector('.knob');
    if (knob) knob.style.transform = 'translate(-50%,-50%)';
  }

  // Called once per physics update; returns {x,y} in [-1,1] and consumes launch.
  poll() {
    let x = 0, y = 0;
    if (this.steerMode === 'touch' && this.touchId !== null) {
      // relative joystick: accumulated delta maps to direction
      const sens = 55; // px of drag for full deflection
      x = this.touchAccum.x / sens;
      y = this.touchAccum.y / sens; // +y = drag up = paddle up (game inverts to world)
      const len = Math.hypot(x, y);
      if (len > 1) { x /= len; y /= len; }
      if (Math.hypot(this.touchAccum.x, this.touchAccum.y) < this.deadZone) { x = 0; y = 0; }
      // decay accum so the stick recenters a bit when holding still — keep it simple: no decay
    } else if (this.steerMode === 'mouse' && this.mousePos) {
      // Mouse steers toward pointer position, mapped over lower play area.
      // Game converts this to target position; here emit direction as (0,0) and let game use mousePos.
      x = 0; y = 0;
    } else if (this.steerMode === 'keys' || true) {
      if (this.keys.has('ArrowLeft') || this.keys.has('KeyA')) x -= 1;
      if (this.keys.has('ArrowRight') || this.keys.has('KeyD')) x += 1;
      if (this.keys.has('ArrowUp') || this.keys.has('KeyW')) y += 1;
      if (this.keys.has('ArrowDown') || this.keys.has('KeyS')) y -= 1;
    }
    this.dir = { x, y };
    const launch = this.launchRequested;
    this.launchRequested = false;
    return { dir: this.dir, launch, mouse: this.steerMode === 'mouse' ? this.mousePos : null };
  }

  isPressed(code) { return this.keys.has(code); }
}