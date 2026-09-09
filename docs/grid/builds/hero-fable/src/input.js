// input.js — mouse, keyboard and touch (virtual thumbstick / drag) → paddle commands.
// Produces a per-frame snapshot via poll(); the game never touches DOM events.

import { clamp } from './util.js';
import { PADDLE } from './paddle.js';

export const STICK = {
  radius: 64, // CSS px of thumb travel for full deflection
  deadZone: 7, // CSS px before the paddle starts moving (horizontal)
  deadZoneY: 16, // vertical dead-zone is wider: a slightly tilted thumb must not drift the paddle up/down
  floatBeyond: 1.35, // stick base slides after the thumb if pulled past radius × this
  speedX: 1200, // logical units / s at full deflection (whole field width in ~0.6 s)
  speedY: 900,
  curve: 1.35, // response exponent (>1 = finer control near the centre)
  tapTime: 0.22, // s; a shorter primary touch that barely moved counts as a fire tap
  tapMove: 12, // CSS px
};

/**
 * Thumb displacement (CSS px from the stick origin) → normalised deflection
 * {nx, ny} in [-1, 1]. Each axis has its own dead-zone and the same response
 * curve, so pure sideways thumb motion gives pure horizontal paddle motion.
 * Pure function (exported for tests).
 */
export function stickDeflection(dx, dy, s = STICK) {
  const axis = (v, dz) => {
    const mag = Math.abs(v);
    if (mag <= dz) return 0;
    const n = Math.pow(clamp((mag - dz) / (s.radius - dz), 0, 1), s.curve);
    return Math.sign(v) * n;
  };
  return { nx: axis(dx, s.deadZone), ny: axis(dy, s.deadZoneY) };
}

/**
 * createInput(target, { toLogical, getPaddle, getScreen })
 *   toLogical(clientX, clientY) → {x,y} in field units
 *   getPaddle() → paddle (for drag scheme offset)
 *   getScreen() → { w, h } CSS px of the canvas
 */
export function createInput(target, { toLogical, getPaddle, getScreen }) {
  const st = {
    scheme: 'stick', // 'stick' | 'drag' — touch scheme (pause menu toggle)
    mode: 'mouse', // last device that moved the paddle: 'mouse' | 'keys' | 'touch'
    pointer: { x: 360, y: PADDLE.restY, has: false },
    keys: new Set(),
    fireHeld: false,
    firePointers: new Set(),
    edges: { fire: 0, pause: 0, mute: 0, restart: 0, any: 0 },
    aim: null,
    stick: { active: false, id: null, ox: 0, oy: 0, x: 0, y: 0, nx: 0, ny: 0, t0: 0, moved: 0 },
    drag: { active: false, id: null, lx: 0, ly: 0, tx: 0, ty: 0, t0: 0, moved: 0 },
    touchSeen: false,
    lastMouseMove: 0,
  };

  const now = () => performance.now() / 1000;
  const isTouch = (e) => e.pointerType === 'touch' || e.pointerType === 'pen';

  // ---- mouse -------------------------------------------------------------
  const onMove = (e) => {
    if (isTouch(e)) return onTouchMove(e);
    const p = toLogical(e.clientX, e.clientY);
    st.pointer.x = p.x;
    st.pointer.y = p.y;
    st.pointer.has = true;
    st.mode = 'mouse';
    st.lastMouseMove = now();
  };
  const onDown = (e) => {
    st.edges.any++;
    if (isTouch(e)) return onTouchStart(e);
    if (e.button !== 0) return;
    const p = toLogical(e.clientX, e.clientY);
    st.pointer.x = p.x;
    st.pointer.y = p.y;
    st.pointer.has = true;
    st.mode = 'mouse';
    st.edges.fire++;
    st.fireHeld = true;
    st.aim = p;
  };
  const onUp = (e) => {
    if (isTouch(e)) return onTouchEnd(e);
    if (e.button === 0) st.fireHeld = false;
  };

  // ---- touch -------------------------------------------------------------
  const screen = () => getScreen();
  const inLowerZone = (clientY) => clientY >= screen().h * 0.4;

  function onTouchStart(e) {
    st.touchSeen = true;
    st.mode = 'touch';
    const primaryFree = !st.stick.active && !st.drag.active;
    if (primaryFree && inLowerZone(e.clientY)) {
      if (st.scheme === 'stick') {
        Object.assign(st.stick, {
          active: true, id: e.pointerId, ox: e.clientX, oy: e.clientY, x: e.clientX, y: e.clientY,
          nx: 0, ny: 0, t0: now(), moved: 0,
        });
      } else {
        const pd = getPaddle();
        Object.assign(st.drag, {
          active: true, id: e.pointerId, lx: e.clientX, ly: e.clientY, tx: pd.x, ty: pd.y, t0: now(), moved: 0,
        });
      }
      return;
    }
    // Second finger, or a touch on the upper part of the screen → fire / launch.
    st.firePointers.add(e.pointerId);
    st.fireHeld = true;
    st.edges.fire++;
    st.aim = toLogical(e.clientX, e.clientY);
  }

  function onTouchMove(e) {
    const s = st.stick;
    if (s.active && e.pointerId === s.id) {
      s.x = e.clientX;
      s.y = e.clientY;
      let dx = s.x - s.ox;
      let dy = s.y - s.oy;
      const d = Math.hypot(dx, dy);
      s.moved = Math.max(s.moved, d);
      // floating base: drag the origin along when the thumb wanders far away
      const maxD = STICK.radius * STICK.floatBeyond;
      if (d > maxD) {
        s.ox = s.x - (dx / d) * maxD;
        s.oy = s.y - (dy / d) * maxD;
        dx = s.x - s.ox;
        dy = s.y - s.oy;
      }
      const n = stickDeflection(dx, dy);
      s.nx = n.nx;
      s.ny = n.ny;
      return;
    }
    const dr = st.drag;
    if (dr.active && e.pointerId === dr.id) {
      const a = toLogical(dr.lx, dr.ly);
      const b = toLogical(e.clientX, e.clientY);
      dr.tx += b.x - a.x;
      dr.ty += b.y - a.y;
      dr.moved += Math.hypot(e.clientX - dr.lx, e.clientY - dr.ly);
      dr.lx = e.clientX;
      dr.ly = e.clientY;
    }
  }

  function onTouchEnd(e) {
    const s = st.stick;
    if (s.active && e.pointerId === s.id) {
      // a quick, still tap with the primary finger also launches/fires
      if (now() - s.t0 < STICK.tapTime && s.moved < STICK.tapMove) {
        st.edges.fire++;
        st.aim = null;
      }
      s.active = false;
      s.nx = s.ny = 0;
    }
    const dr = st.drag;
    if (dr.active && e.pointerId === dr.id) {
      if (now() - dr.t0 < STICK.tapTime && dr.moved < STICK.tapMove) {
        st.edges.fire++;
        st.aim = null;
      }
      dr.active = false;
    }
    if (st.firePointers.delete(e.pointerId) && st.firePointers.size === 0) st.fireHeld = false;
  }

  // ---- keyboard ----------------------------------------------------------
  const KEYMAP = {
    ArrowLeft: 'left', a: 'left', A: 'left',
    ArrowRight: 'right', d: 'right', D: 'right',
    ArrowUp: 'up', w: 'up', W: 'up',
    ArrowDown: 'down', s: 'down', S: 'down',
  };
  const onKeyDown = (e) => {
    if (e.repeat) return;
    const k = e.key;
    if (KEYMAP[k]) {
      st.keys.add(KEYMAP[k]);
      st.mode = 'keys';
      e.preventDefault();
    } else if (k === ' ' || k === 'Enter') {
      st.edges.fire++;
      st.fireHeld = true;
      st.aim = null;
      st.edges.any++;
      e.preventDefault();
    } else if (k === 'p' || k === 'P' || k === 'Escape') st.edges.pause++;
    else if (k === 'm' || k === 'M') st.edges.mute++;
    else if (k === 'r' || k === 'R') st.edges.restart++;
    st.edges.any++;
  };
  const onKeyUp = (e) => {
    const k = e.key;
    if (KEYMAP[k]) st.keys.delete(KEYMAP[k]);
    if (k === ' ' || k === 'Enter') st.fireHeld = false;
  };
  const onBlur = () => {
    st.keys.clear();
    st.fireHeld = false;
    st.firePointers.clear();
    st.stick.active = false;
    st.drag.active = false;
  };

  const prevent = (e) => e.preventDefault();
  target.addEventListener('pointermove', onMove);
  target.addEventListener('pointerdown', onDown);
  window.addEventListener('pointerup', onUp);
  window.addEventListener('pointercancel', onUp);
  target.addEventListener('contextmenu', prevent);
  target.addEventListener('touchstart', prevent, { passive: false });
  target.addEventListener('touchmove', prevent, { passive: false });
  document.addEventListener('gesturestart', prevent);
  window.addEventListener('keydown', onKeyDown);
  window.addEventListener('keyup', onKeyUp);
  window.addEventListener('blur', onBlur);

  /** Build the paddle command for this frame. */
  function paddleCommand() {
    const k = st.keys;
    if (k.size) {
      st.mode = 'keys';
      const vx = ((k.has('right') ? 1 : 0) - (k.has('left') ? 1 : 0)) * PADDLE.keySpeedX;
      const vy = ((k.has('down') ? 1 : 0) - (k.has('up') ? 1 : 0)) * PADDLE.keySpeedY;
      return { mode: 'velocity', vx, vy };
    }
    if (st.stick.active) return { mode: 'velocity', vx: st.stick.nx * STICK.speedX, vy: st.stick.ny * STICK.speedY };
    if (st.drag.active) return { mode: 'absolute', x: st.drag.tx, y: st.drag.ty };
    if (st.mode === 'mouse' && st.pointer.has) return { mode: 'absolute', x: st.pointer.x, y: st.pointer.y };
    return null;
  }

  /** Snapshot for this frame; edge counters are consumed. */
  function poll() {
    const snap = {
      paddleCmd: paddleCommand(),
      fire: st.edges.fire > 0,
      fireHeld: st.fireHeld,
      aim: st.edges.fire > 0 ? (st.aim || (st.mode === 'mouse' && st.pointer.has ? { ...st.pointer } : null)) : null,
      pause: st.edges.pause > 0,
      mute: st.edges.mute > 0,
      restart: st.edges.restart > 0,
      any: st.edges.any > 0,
    };
    // While a laser is held with the mouse, keep aiming at the pointer.
    if (st.fireHeld && !snap.aim && st.mode === 'mouse' && st.pointer.has) snap.aim = { ...st.pointer };
    st.edges.fire = st.edges.pause = st.edges.mute = st.edges.restart = st.edges.any = 0;
    st.aim = null;
    return snap;
  }

  return {
    state: st,
    poll,
    setScheme(s) {
      st.scheme = s === 'drag' ? 'drag' : 'stick';
      st.stick.active = false;
      st.drag.active = false;
    },
    get scheme() {
      return st.scheme;
    },
    /** Reset all transient state (used when a menu opens). */
    clear() {
      onBlur();
      st.edges.fire = st.edges.pause = st.edges.mute = st.edges.restart = st.edges.any = 0;
    },
    destroy() {
      target.removeEventListener('pointermove', onMove);
      target.removeEventListener('pointerdown', onDown);
      window.removeEventListener('pointerup', onUp);
      window.removeEventListener('pointercancel', onUp);
      window.removeEventListener('keydown', onKeyDown);
      window.removeEventListener('keyup', onKeyUp);
      window.removeEventListener('blur', onBlur);
    },
  };
}
