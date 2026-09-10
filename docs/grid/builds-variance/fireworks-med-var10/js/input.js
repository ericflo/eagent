// SKYBREAK — input: keyboard, mouse direct, touch direct, touch thumbstick.
//
// Produces a normalized control state consumed by the game:
//   { x, y }        target paddle center in world coords (direct mode)
//   { dx, dy }      stick delta in world units (thumbstick mode)
//   launchQueued    one-shot flag for launching / firing lasers
//   active          which scheme is currently driving

class Input {
  constructor(canvas) {
    this.canvas = canvas;
    this.scheme = 'auto';        // 'auto' | 'direct' | 'thumb'
    this.effective = 'direct';

    this.pointer = { x: CONFIG.W / 2, y: CONFIG.PADDLE_BOTTOM_Y };
    this.pointerDown = false;
    this.stick = { x: 0, y: 0, originX: 0, originY: 0, active: false, id: null };
    this.keys = new Set();
    this.keyVel = { x: 0, y: 0 };
    this.launchQueued = false;
    this.scale = 1;              // canvas CSS -> world scale, set by resize
    this._lastPointerWorld = null;

    this._bind();
  }

  toWorld(e) {
    const r = this.canvas.getBoundingClientRect();
    return {
      x: (e.clientX - r.left) / this.scale,
      y: (e.clientY - r.top) / this.scale,
    };
  }

  wantsThumbstick() {
    if (this.scheme === 'thumb') return true;
    if (this.scheme === 'direct') return false;
    // auto: touch device (and not currently using a mouse)
    return navigator.maxTouchPoints > 0;
  }

  _bind() {
    const c = this.canvas;

    window.addEventListener('keydown', (e) => {
      if (['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', ' '].includes(e.key)) e.preventDefault();
      this.keys.add(e.key.toLowerCase());
      if (e.key === ' ' && !e.repeat) this.launchQueued = true;
      this.effective = 'keys';
    });
    window.addEventListener('keyup', (e) => this.keys.delete(e.key.toLowerCase()));
    window.addEventListener('blur', () => this.keys.clear());

    c.addEventListener('pointerdown', (e) => {
      c.setPointerCapture?.(e.pointerId);
      const w = this.toWorld(e);
      this.pointerDown = true;
      if (this.wantsThumbstick()) {
        this.effective = 'thumb';
        this.stick.active = true;
        this.stick.id = e.pointerId;
        this.stick.originX = w.x; this.stick.originY = w.y;
        this.stick.x = 0; this.stick.y = 0;
      } else {
        this.effective = 'direct';
        this.pointer.x = w.x; this.pointer.y = w.y;
        this._lastPointerWorld = w;
        this.launchQueued = true;
      }
    });

    c.addEventListener('pointermove', (e) => {
      const w = this.toWorld(e);
      if (this.effective === 'thumb' && this.stick.active && e.pointerId === this.stick.id) {
        const maxR = 90;
        let dx = w.x - this.stick.originX;
        let dy = w.y - this.stick.originY;
        const d = Math.hypot(dx, dy);
        if (d > maxR) { dx *= maxR / d; dy *= maxR / d; }
        this.stick.x = dx; this.stick.y = dy;
      } else {
        this.pointer.x = w.x; this.pointer.y = w.y;
        if (this.effective !== 'keys') this.effective = 'direct';
      }
    }, { passive: true });

    const release = (e) => {
      this.pointerDown = false;
      if (this.stick.active && e.pointerId === this.stick.id) {
        this.stick.active = false;
        this.stick.x = 0; this.stick.y = 0;
        this.stick.id = null;
      }
    };
    c.addEventListener('pointerup', release);
    c.addEventListener('pointercancel', release);

    // Mouse click also launches when in direct mode without moving.
    c.addEventListener('click', () => {
      if (this.effective !== 'thumb') this.launchQueued = true;
    });
  }

  // Called by the game every frame before updating the paddle.
  poll(dt, paddle) {
    const out = { mode: this.effective, useStick: false, launch: this.launchQueued };
    this.launchQueued = false;

    if (this.effective === 'thumb' && this.stick.active) {
      out.useStick = true;
      out.dx = this.stick.x * 14; // world px per frame at 60fps baseline
      out.dy = this.stick.y * 14;
    } else if (this.effective === 'keys') {
      const k = this.keys;
      let vx = 0, vy = 0;
      if (k.has('arrowleft') || k.has('a')) vx -= 1;
      if (k.has('arrowright') || k.has('d')) vx += 1;
      if (k.has('arrowup') || k.has('w')) vy -= 1;
      if (k.has('arrowdown') || k.has('s')) vy += 1;
      const sp = CONFIG.PADDLE_SPEED;
      out.dx = vx * sp * dt;
      out.dy = vy * sp * dt;
      out.useStick = true;
      // Keep the draw target in sync so switching schemes doesn't jump.
      this.pointer.x = paddle.x; this.pointer.y = paddle.y;
    } else {
      out.dx = 0; out.dy = 0;
    }
    return out;
  }
}
