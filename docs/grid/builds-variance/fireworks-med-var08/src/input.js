// Input: keyboard, mouse, and touch thumbstick. Produces an `input` object
// consumed by the sim, and forwards UI intents via callbacks.

export function createInput(canvas, opts = {}) {
  const input = {
    left: false, right: false, up: false, down: false,
    pointerActive: false,
    pointerX: 0, pointerY: 0,
    launchQueued: false,
    thumbstick: null, // {active, baseX, baseY, dx, dy} in view px
    intents: opts,
  };

  const key = (e, down) => {
    switch (e.code) {
      case 'ArrowLeft': case 'KeyA': input.left = down; return true;
      case 'ArrowRight': case 'KeyD': input.right = down; return true;
      case 'ArrowUp': case 'KeyW': input.up = down; return true;
      case 'ArrowDown': case 'KeyS': input.down = down; return true;
    }
    return false;
  };

  window.addEventListener('keydown', e => {
    if (key(e, true)) { e.preventDefault(); return; }
    if (e.repeat) return;
    switch (e.code) {
      case 'Space':
        e.preventDefault();
        input.intents.onLaunch?.();
        break;
      case 'Escape': case 'KeyP':
        input.intents.onPause?.();
        break;
      case 'KeyM':
        input.intents.onMute?.();
        break;
      case 'Enter':
        input.intents.onConfirm?.();
        break;
    }
  });
  window.addEventListener('keyup', e => key(e, false));

  // ---- Mouse: paddle follows pointer; click launches
  canvas.addEventListener('pointermove', e => {
    if (e.pointerType === 'touch') return;
    const p = opts.toField(e.clientX, e.clientY);
    if (p) { input.pointerActive = true; input.pointerX = p.x; input.pointerY = p.y; }
  });
  canvas.addEventListener('pointerdown', e => {
    if (e.pointerType === 'touch') return;
    const p = opts.toField(e.clientX, e.clientY);
    if (p) { input.pointerActive = true; input.pointerX = p.x; input.pointerY = p.y; }
    input.intents.onLaunch?.();
  });

  // ---- Touch: relative thumbstick in the lower area; tap to launch.
  const touches = new Map(); // id -> {startX, startY, lastX, lastY}
  const SENS = 1.7;

  canvas.addEventListener('pointerdown', e => {
    if (e.pointerType !== 'touch') return;
    touches.set(e.pointerId, { startX: e.clientX, startY: e.clientY, lastX: e.clientX, lastY: e.clientY, moved: false, t: performance.now() });
    input.thumbstick = { active: true, baseX: e.clientX, baseY: e.clientY, dx: 0, dy: 0 };
  });

  canvas.addEventListener('pointermove', e => {
    if (e.pointerType !== 'touch') return;
    const t = touches.get(e.pointerId);
    if (!t) return;
    t.lastX = e.clientX; t.lastY = e.clientY;
    const dxTotal = e.clientX - t.startX, dyTotal = e.clientY - t.startY;
    if (Math.hypot(dxTotal, dyTotal) > 12) t.moved = true;
    // thumbstick uses the FIRST touch
    const first = touches.entries().next().value;
    if (first && first[0] === e.pointerId && input.thumbstick) {
      input.thumbstick.dx = dxTotal * SENS;
      input.thumbstick.dy = dyTotal * SENS;
    }
  });

  const endTouch = e => {
    const t = touches.get(e.pointerId);
    touches.delete(e.pointerId);
    if (t && !t.moved && performance.now() - t.t < 300) input.intents.onLaunch?.();
    if (touches.size === 0) {
      input.thumbstick && (input.thumbstick.active = false);
      input.thumbstick = null;
      // stop pointer-driving the paddle via touch
    } else {
      // rebase thumbstick to remaining touch
      const first = touches.entries().next().value;
      if (first && input.thumbstick) {
        input.thumbstick.baseX = first[1].startX;
        input.thumbstick.baseY = first[1].startY;
        input.thumbstick.dx = 0; input.thumbstick.dy = 0;
      }
    }
  };
  canvas.addEventListener('pointerup', endTouch);
  canvas.addEventListener('pointercancel', endTouch);

  // Convert thumbstick drag into paddle velocity each frame.
  input.applyThumbstick = (g, dt, toField) => {
    if (!input.thumbstick) return;
    const ts = input.thumbstick;
    // map thumb delta (view px) into field units
    const f = toField(ts.baseX + ts.dx, ts.baseY + ts.dy);
    const base = toField(ts.baseX, ts.baseY);
    if (!f || !base) return;
    // Direct velocity control: paddle velocity proportional to thumb offset.
    const field = g.field;
    g.paddle.vx = (f.x - base.x) / Math.max(dt, 1 / 240);
    g.paddle.vy = (f.y - base.y) / Math.max(dt, 1 / 240);
    g.paddle.x += g.paddle.vx * dt;
    g.paddle.y += g.paddle.vy * dt;
    // keep in bounds
    const half = g.paddle.w / 2;
    g.paddle.x = Math.max(half, Math.min(field.width - half, g.paddle.x));
    g.paddle.y = Math.max(field.height * 0.62, Math.min(field.height * 0.92, g.paddle.y));
  };

  return input;
}
