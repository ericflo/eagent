// src/game/bricks.js
//
// Attic Breaker — brick types, behaviour and procedural drawing.
//
// ZERO IMPORTS BY DESIGN. This module does not import ../core/math.js or
// ../core/constants.js (they are written by another worker in parallel) —
// all math helpers and grid metrics are defined locally below and exported
// so other modules can stay in sync without a shared import.
//
// -----------------------------------------------------------------------
// OPTIONAL GAME HOOKS
// This module calls a handful of *optional* methods on the `game` object
// passed into `hitBy`/`update`. All calls are made defensively with `?.()`
// so bricks.js works fine when `game` is undefined (unit tests) or when a
// hook hasn't been implemented yet. The core worker should implement:
//
//   game.explodeAt(x, y, radius, source)
//       Called when an `explosive` brick breaks (and by `super`-balls on
//       steel). Should damage/destroy nearby bricks and spawn FX/SFX.
//       `source` is a string tag ('explosive' | 'super') for FX flavor.
//
//   game.magnetPull(x, y, strength, dt)
//       Called every frame (from Brick#update) for each living `magnet`
//       brick. Should bend nearby ball velocities toward (x,y) scaled by
//       `strength` and `dt`.
//
//   game.repulse(x, y, strength)
//       Called once when a `magnet` brick breaks — a one-shot outward
//       impulse applied to nearby balls/objects.
//
//   game.onBrickBroken(brick, verdict)
//       Called whenever a brick's hitBy() returns a 'break' verdict, after
//       internal state (alive=false, hp, etc.) has already been updated.
//       Useful for scoring, attic bookkeeping, combo counters, etc.
//
//   game.balls: Array<{x,y,vx,vy,r,speed,flags}>
//       Read (never mutated) by `attic` bricks to decide whether a ball is
//       currently resting above them (for the idle glow), and can be used
//       by `magnet`/`speed` bricks for proximity dialling. Optional.
//
// -----------------------------------------------------------------------
// GRID METRICS — 10 columns across the 1000-unit-wide world.
// margin 30 each side, gap 10 between bricks:
//   BRICK_W = (1000 - 2*30 - 9*10) / 10 = 85
export const BRICK_MARGIN = 30;
export const BRICK_GAP = 10;
export const BRICK_W = 85;
export const BRICK_H = 32;
export const BRICK_TOP = 260; // must match src/core/constants.js BRICK_TOP
export const BRICK_COLS = 10;
export const ROW_STEP = BRICK_H + BRICK_GAP; // 42
export const COL_STEP = BRICK_W + BRICK_GAP; // 95

export function colX(col) {
  return BRICK_MARGIN + col * COL_STEP;
}
export function rowY(row) {
  return BRICK_TOP + row * ROW_STEP;
}

// ---- local math helpers (deliberately not imported) -------------------
const TAU = Math.PI * 2;
function clamp(v, lo, hi) { return v < lo ? lo : v > hi ? hi : v; }
function lerp(a, b, t) { return a + (b - a) * t; }
function angleDelta(a, b) {
  let d = a - b;
  while (d > Math.PI) d -= TAU;
  while (d < -Math.PI) d += TAU;
  return d;
}
function hypot(x, y) { return Math.sqrt(x * x + y * y); }

// -----------------------------------------------------------------------
// BRICK_TYPES metadata (for HUD/legend/how-to-play text)
export const BRICK_TYPES = {
  normal:    { name: 'Normal',    color: '#3fd0ff', score: 100, tint: '#3fd0ff',
    desc: 'Breaks in a single hit. Fire balls burn straight through it.' },
  angle:     { name: 'Angle',     color: '#ff5fd1', score: 150, tint: '#ff5fd1',
    desc: 'Only breaks when struck within ±35° of the arrowed direction.' },
  speed:     { name: 'Speed',     color: '#ffd23f', score: 150, tint: '#ffd23f',
    desc: 'Only breaks above a speed threshold — the dial must fill.' },
  attic:     { name: 'Attic',     color: '#8f5bff', score: 400, tint: '#8f5bff',
    desc: 'Only breaks when struck from above. Big score — the core fantasy.' },
  shield:    { name: 'Shield',    color: '#5bffb0', score: 200, tint: '#5bffb0',
    desc: 'Invulnerable while its rotating shield faces the impact side.' },
  mirror:    { name: 'Mirror',    color: '#c9f2ff', score: 250, tint: '#c9f2ff',
    desc: 'Deflects the ball diagonally upward. Survives two hits, then breaks.' },
  mover:     { name: 'Mover',     color: '#5ad1ff', score: 150, tint: '#5ad1ff',
    desc: 'Slides along its row — a moving target, one hit to break.' },
  explosive: { name: 'Explosive', color: '#ff4d4d', score: 200, tint: '#ff4d4d',
    desc: 'Breaks in one hit and detonates neighbouring bricks.' },
  magnet:    { name: 'Magnet',    color: '#4dffee', score: 200, tint: '#4dffee',
    desc: 'Bends nearby ball paths toward it; releases a pulse when broken.' },
  steel:     { name: 'Steel',     color: '#9aa5b1', score: 500, tint: '#9aa5b1',
    desc: 'Indestructible — only super balls or explosions break it.' },
};

// -----------------------------------------------------------------------
function mirrorReflect(vx, vy, orient) {
  const speed = hypot(vx, vy) || 1;
  let nx, ny;
  if (orient === '/') { nx = -vy; ny = -vx; }
  else { nx = vy; ny = vx; }
  if (ny > -1e-6) ny = -Math.abs(ny || speed * 0.5); // guarantee upward bias
  const m = hypot(nx, ny) || 1;
  return { vx: (nx / m) * speed, vy: (ny / m) * speed };
}

export class Brick {
  constructor(col, row, type, opts = {}) {
    this.col = col;
    this.row = row;
    this.type = type;
    this.x = opts.x !== undefined ? opts.x : colX(col);
    this.y = opts.y !== undefined ? opts.y : rowY(row);
    this.w = BRICK_W;
    this.h = BRICK_H;
    this.alive = true;
    this.hp = type === 'mirror' ? 2 : 1;
    this.flashT = 0;
    this.flashStrength = 0;
    this.hintT = 0;
    this.hintStrength = 0;
    this.seed = Math.random() * TAU;

    // visual-only per-cell variance for `normal` bricks (round 2): a small
    // deterministic hue offset derived from grid position so a full wall
    // reads as rich rather than flat, plus optional level-provided tint.
    this.tint = opts.tint || null;
    this.hueOffset = opts.hue !== undefined ? opts.hue : (((col * 29 + row * 53) % 41) - 20);

    // type-specific setup
    this.dir = opts.dir !== undefined ? opts.dir : -Math.PI / 2; // angle bricks: required incoming travel direction
    this.speedThreshold = opts.speedThreshold !== undefined ? opts.speedThreshold : 1050;
    this.lastSpeedFrac = 0;

    this.shieldAngle = opts.shieldAngle !== undefined ? opts.shieldAngle : Math.random() * TAU;
    this.shieldSpeed = opts.shieldSpeed !== undefined ? opts.shieldSpeed : (0.9 + Math.random() * 0.5);
    this.shieldArc = opts.shieldArc !== undefined ? opts.shieldArc : Math.PI * 0.55;

    this.orient = opts.orient || ((col + row) % 2 === 0 ? '/' : '\\');

    this.baseX = this.x;
    this.moveRange = opts.moveRange !== undefined ? opts.moveRange : 200;
    this.moveSpeed = opts.moveSpeed !== undefined ? opts.moveSpeed : 0.6;
    this.movePhase = opts.movePhase !== undefined ? opts.movePhase : Math.random() * TAU;
    this.minX = Math.max(BRICK_MARGIN, this.baseX - this.moveRange / 2);
    this.maxX = Math.min(1000 - BRICK_MARGIN - this.w, this.baseX + this.moveRange / 2);
    this.moveT = 0;

    this.radius = opts.radius !== undefined ? opts.radius : 150; // explosive
    this.pullStrength = opts.pullStrength !== undefined ? opts.pullStrength : 700; // magnet
    this.pullRadius = opts.pullRadius !== undefined ? opts.pullRadius : 260;

    this.ballAbove = false;
  }

  get cx() { return this.x + this.w / 2; }
  get cy() { return this.y + this.h / 2; }

  flash(s = 1) {
    this.flashT = 1;
    this.flashStrength = s;
  }

  /**
   * Make the brick glow invitingly — call when a condition is about to be
   * satisfied (ball approaching a speed brick fast enough, ball above an
   * attic brick, etc). Purely visual; decays on its own via `update(dt)`.
   * @param {number} [strength=1] 0..1+ glow intensity
   */
  hint(strength = 1) {
    this.hintT = 1;
    this.hintStrength = strength;
  }

  update(dt, ctx = {}) {
    if (this.flashT > 0) this.flashT = Math.max(0, this.flashT - dt * 3);
    if (this.hintT > 0) this.hintT = Math.max(0, this.hintT - dt * 1.4);
    const game = ctx.game;

    if (this.type === 'mover') {
      this.moveT += dt;
      const span = Math.max(0, this.maxX - this.minX);
      const s = Math.sin(this.moveT * this.moveSpeed + this.movePhase) * 0.5 + 0.5;
      this.x = this.minX + s * span;
    } else if (this.type === 'shield') {
      this.shieldAngle += this.shieldSpeed * dt;
    } else if (this.type === 'magnet') {
      game?.magnetPull?.(this.cx, this.cy, this.pullStrength, dt);
    } else if (this.type === 'attic') {
      this.ballAbove = false;
      const balls = game?.balls;
      if (balls) {
        for (const b of balls) {
          if (b.x > this.x - this.w * 0.4 && b.x < this.x + this.w * 1.4 && b.y < this.y) {
            this.ballAbove = true;
            break;
          }
        }
      }
    }
  }

  _break(score, sfx, game, fx = 'shatter') {
    this.alive = false;
    if (this.type === 'explosive') game?.explodeAt?.(this.cx, this.cy, this.radius, 'explosive');
    if (this.type === 'magnet') game?.repulse?.(this.cx, this.cy, this.pullStrength * 2);
    const verdict = { result: 'break', score, sfx, fx };
    game?.onBrickBroken?.(this, verdict);
    return verdict;
  }

  // ball: {x,y,vx,vy,r,speed,flags:{fire,super,heavy,ghost}}
  // hit:  {nx,ny} surface normal (points from brick surface toward the ball)
  hitBy(ball, hit, game) {
    const flags = ball.flags || {};
    const speed = ball.speed !== undefined ? ball.speed : hypot(ball.vx, ball.vy);

    if (flags.ghost) return { result: 'pass' };

    if (this.type === 'steel') {
      if (flags.super) return this._break(BRICK_TYPES.steel.score, 'brickHard', game, 'shatterSteel');
      this.flash(1);
      return { result: 'bounce', sfx: 'wall', fx: 'sparkSteel' };
    }

    if (flags.super) {
      return this._break(BRICK_TYPES[this.type].score, 'brickHard', game, 'shatterSuper');
    }

    switch (this.type) {
      case 'normal': {
        const verdict = this._break(BRICK_TYPES.normal.score, 'brick', game, 'shatter');
        if (flags.fire) verdict.pierce = true; // fire burns through: caller should keep the ball moving
        return verdict;
      }

      case 'angle': {
        if (flags.heavy) return this._break(BRICK_TYPES.angle.score, 'brickAngle', game, 'shatterAngle');
        const travel = Math.atan2(ball.vy, ball.vx);
        const diff = Math.abs(angleDelta(travel, this.dir));
        if (diff <= (35 * Math.PI / 180)) return this._break(BRICK_TYPES.angle.score, 'brickAngle', game, 'shatterAngle');
        this.flash(1);
        return { result: 'bounce', sfx: 'wall', fx: 'sparkAngle' };
      }

      case 'speed': {
        this.lastSpeedFrac = clamp(speed / this.speedThreshold, 0, 1.5);
        if (flags.heavy || speed >= this.speedThreshold) {
          return this._break(BRICK_TYPES.speed.score, 'brickSpeed', game, 'shatterSpeed');
        }
        this.flash(0.6);
        return { result: 'bounce', sfx: 'wall', fx: 'sparkSpeed' };
      }

      case 'attic': {
        const fromAbove = hit.ny <= -0.4;
        if (fromAbove) return this._break(BRICK_TYPES.attic.score, 'combo', game, 'shatterAttic');
        this.flash(1);
        return { result: 'bounce', sfx: 'wall', fx: 'sparkAttic' };
      }

      case 'shield': {
        const impactAngle = Math.atan2(hit.ny, hit.nx);
        const diff = Math.abs(angleDelta(impactAngle, this.shieldAngle));
        const facing = diff <= this.shieldArc / 2;
        if (facing) {
          this.flash(0.8);
          return { result: 'bounce', sfx: 'brickShield', fx: 'shieldSpark' };
        }
        return this._break(BRICK_TYPES.shield.score, 'brickShield', game, 'shatterShield');
      }

      case 'mirror': {
        const refl = mirrorReflect(ball.vx, ball.vy, this.orient);
        ball.vx = refl.vx;
        ball.vy = refl.vy;
        this.hp -= 1;
        this.flash(0.5);
        if (this.hp <= 0) return this._break(BRICK_TYPES.mirror.score, 'brickMirror', game, 'shatterMirror');
        return { result: 'bounce', sfx: 'brickMirror', fx: 'mirrorFlare' };
      }

      case 'mover':
        return this._break(BRICK_TYPES.mover.score, 'brick', game, 'shatterMover');

      case 'explosive':
        return this._break(BRICK_TYPES.explosive.score, 'brickExplode', game, 'explosionFx');

      case 'magnet':
        return this._break(BRICK_TYPES.magnet.score, 'brickMagnet', game, 'magnetPulseFx');

      default:
        return this._break(100, 'brick', game, 'shatter');
    }
  }

  draw(g, t) {
    g.save();
    g.translate(this.x, this.y);
    const shakeMag = this.flashT * this.flashStrength * 3.4; // hard shudder on rejected hits
    if (shakeMag > 0.01) {
      g.translate(Math.sin(t * 90 + this.seed) * shakeMag, Math.cos(t * 70 + this.seed) * shakeMag);
    }
    const fn = DRAW[this.type] || DRAW.normal;
    fn(g, this.w, this.h, t, this);
    if (this.flashT > 0.02) {
      drawRimLight(g, this.w, this.h, Math.min(1, this.flashT * this.flashStrength));
    }
    if (this.hintT > 0.02) {
      const glowColor = (BRICK_TYPES[this.type] && BRICK_TYPES[this.type].color) || '#ffffff';
      drawHintGlow(g, this.w, this.h, Math.min(1.4, this.hintT * this.hintStrength), t, glowColor);
    }
    g.restore();
  }
}

// -----------------------------------------------------------------------
// DRAWING
// Every draw fn: (g, w, h, t, state) — state carries live brick fields when
// available (dir, shieldAngle, hp, lastSpeedFrac, ballAbove, orient, flashT).

function baseRect(g, w, h, color, glow) {
  const r = 5;
  g.beginPath();
  g.moveTo(r, 0);
  g.arcTo(w, 0, w, h, r);
  g.arcTo(w, h, 0, h, r);
  g.arcTo(0, h, 0, 0, r);
  g.arcTo(0, 0, w, 0, r);
  g.closePath();
  const grad = g.createLinearGradient(0, 0, 0, h);
  grad.addColorStop(0, lighten(color, 0.35));
  grad.addColorStop(1, lighten(color, -0.25));
  g.fillStyle = grad;
  g.fill();
  if (glow) {
    g.save();
    g.globalCompositeOperation = 'lighter';
    g.strokeStyle = color;
    g.globalAlpha = 0.55;
    g.lineWidth = 2;
    g.stroke();
    g.restore();
  } else {
    g.strokeStyle = 'rgba(0,0,0,0.35)';
    g.lineWidth = 1.5;
    g.stroke();
  }
}

function lighten(hex, amt) {
  const c = hex.replace('#', '');
  const num = parseInt(c.length === 3 ? c.split('').map(x => x + x).join('') : c, 16);
  let r = (num >> 16) & 255, gg = (num >> 8) & 255, b = num & 255;
  const f = (v) => clamp(Math.round(v + (amt > 0 ? (255 - v) * amt : v * amt)), 0, 255);
  r = f(r); gg = f(gg); b = f(b);
  return `rgb(${r},${gg},${b})`;
}

// ---- hue-shift helpers (local, no imports) — used to give `normal` bricks
// a per-grid-cell tint variance instead of a flat uniform color. -----------
function hexToRgb(hex) {
  const c = (hex || '#ffffff').replace('#', '');
  const h = c.length === 3 ? c.split('').map((x) => x + x).join('') : c;
  const num = parseInt(h, 16) || 0xffffff;
  return [(num >> 16) & 255, (num >> 8) & 255, num & 255];
}
function rgbToHex(r, g, b) {
  const h = (v) => clamp(Math.round(v), 0, 255).toString(16).padStart(2, '0');
  return `#${h(r)}${h(g)}${h(b)}`;
}
function rgbToHsl(r, g, b) {
  r /= 255; g /= 255; b /= 255;
  const max = Math.max(r, g, b), min = Math.min(r, g, b);
  let h, s; const l = (max + min) / 2;
  if (max === min) { h = 0; s = 0; }
  else {
    const d = max - min;
    s = l > 0.5 ? d / (2 - max - min) : d / (max + min);
    switch (max) {
      case r: h = (g - b) / d + (g < b ? 6 : 0); break;
      case g: h = (b - r) / d + 2; break;
      default: h = (r - g) / d + 4; break;
    }
    h /= 6;
  }
  return [h * 360, s, l];
}
function hue2rgb(p, q, tt) {
  if (tt < 0) tt += 1;
  if (tt > 1) tt -= 1;
  if (tt < 1 / 6) return p + (q - p) * 6 * tt;
  if (tt < 1 / 2) return q;
  if (tt < 2 / 3) return p + (q - p) * (2 / 3 - tt) * 6;
  return p;
}
function hslToRgb(h, s, l) {
  h = (((h % 360) + 360) % 360) / 360;
  if (s === 0) return [l * 255, l * 255, l * 255];
  const q = l < 0.5 ? l * (1 + s) : l + s - l * s;
  const p = 2 * l - q;
  return [hue2rgb(p, q, h + 1 / 3) * 255, hue2rgb(p, q, h) * 255, hue2rgb(p, q, h - 1 / 3) * 255];
}
function hueShiftHex(hex, deg) {
  if (!deg) return hex;
  const [r, g, b] = hexToRgb(hex);
  const [h, s, l] = rgbToHsl(r, g, b);
  const [nr, ng, nb] = hslToRgb(h + deg, s, l);
  return rgbToHex(nr, ng, nb);
}

function roundRectPath(g, x, y, w, h, r) {
  g.beginPath();
  g.moveTo(x + r, y);
  g.arcTo(x + w, y, x + w, y + h, r);
  g.arcTo(x + w, y + h, x, y + h, r);
  g.arcTo(x, y + h, x, y, r);
  g.arcTo(x, y, x + w, y, r);
  g.closePath();
}

function flashOverlay(g, w, h, state) {
  if (state?.flashT > 0) {
    g.save();
    g.globalCompositeOperation = 'lighter';
    g.globalAlpha = state.flashT * 0.7;
    g.fillStyle = '#ffffff';
    g.fillRect(0, 0, w, h);
    g.restore();
  }
}

/** Hard rim light flashed around a brick's silhouette on a rejected hit. */
function drawRimLight(g, w, h, strength) {
  if (strength <= 0) return;
  g.save();
  g.globalCompositeOperation = 'lighter';
  roundRectPath(g, 0, 0, w, h, 5);
  g.strokeStyle = '#ffffff';
  g.lineWidth = 1.5 + 2.5 * strength;
  g.globalAlpha = Math.min(1, 0.35 + 0.55 * strength);
  g.stroke();
  g.restore();
}

/** Inviting pulsing glow drawn just outside a brick's box (see Brick#hint). */
function drawHintGlow(g, w, h, strength, t, color) {
  if (strength <= 0) return;
  g.save();
  g.globalCompositeOperation = 'lighter';
  const pulse = 0.6 + 0.4 * Math.sin(t * 8);
  roundRectPath(g, -2, -2, w + 4, h + 4, 7);
  g.strokeStyle = color;
  g.lineWidth = 5;
  g.globalAlpha = 0.3 * strength * pulse;
  g.stroke();
  g.lineWidth = 10;
  g.globalAlpha = 0.16 * strength * pulse;
  g.stroke();
  g.restore();
}

function drawNormal(g, w, h, t, state) {
  const baseColor = (state && state.tint) || BRICK_TYPES.normal.color;
  const color = hueShiftHex(baseColor, (state && state.hueOffset) || 0);
  baseRect(g, w, h, color, false);
  g.save();
  g.globalCompositeOperation = 'lighter';
  g.globalAlpha = 0.25 + 0.1 * Math.sin(t * 2 + (state?.seed || 0));
  g.fillStyle = '#ffffff';
  g.fillRect(4, 3, w - 8, h * 0.3);
  g.restore();
  // subtle animated inner sheen sweep so a full wall of normals reads as
  // rich rather than uniform (very low alpha, offset per-brick via seed).
  g.save();
  roundRectPath(g, 0, 0, w, h, 5);
  g.clip();
  g.globalCompositeOperation = 'lighter';
  const sweep = ((t * 24 + (state?.seed || 0) * 40) % (w * 1.8)) - w * 0.4;
  const sheenGrad = g.createLinearGradient(sweep - 8, 0, sweep + 8, h);
  sheenGrad.addColorStop(0, 'rgba(255,255,255,0)');
  sheenGrad.addColorStop(0.5, 'rgba(255,255,255,0.16)');
  sheenGrad.addColorStop(1, 'rgba(255,255,255,0)');
  g.fillStyle = sheenGrad;
  g.fillRect(0, 0, w, h);
  g.restore();
  flashOverlay(g, w, h, state);
}


function drawAngle(g, w, h, t, state) {
  baseRect(g, w, h, BRICK_TYPES.angle.color, true);
  const dir = state?.dir !== undefined ? state.dir : -Math.PI / 2;
  g.save();
  g.translate(w / 2, h / 2);
  g.rotate(dir + Math.PI / 2);
  const pulse = 1 + 0.08 * Math.sin(t * 4);
  g.scale(pulse, pulse);
  g.beginPath();
  g.moveTo(0, -h * 0.32);
  g.lineTo(w * 0.22, h * 0.14);
  g.lineTo(w * 0.08, h * 0.14);
  g.lineTo(w * 0.08, h * 0.32);
  g.lineTo(-w * 0.08, h * 0.32);
  g.lineTo(-w * 0.08, h * 0.14);
  g.lineTo(-w * 0.22, h * 0.14);
  g.closePath();
  g.fillStyle = '#1a0a16';
  g.fill();
  g.save();
  g.globalCompositeOperation = 'lighter';
  g.fillStyle = '#ffffff';
  g.globalAlpha = 0.8;
  g.fill();
  g.restore();
  g.restore();
  flashOverlay(g, w, h, state);
}

function drawSpeed(g, w, h, t, state) {
  baseRect(g, w, h, BRICK_TYPES.speed.color, true);
  const cx = w / 2, cy = h * 0.62, rad = h * 0.5;
  g.save();
  g.lineWidth = 3;
  g.strokeStyle = 'rgba(0,0,0,0.4)';
  g.beginPath();
  g.arc(cx, cy, rad, Math.PI, 0);
  g.stroke();
  const frac = clamp(state?.lastSpeedFrac || 0, 0, 1);
  const needleFrac = frac > 0 ? frac : (0.15 + 0.1 * Math.sin(t * 3));
  g.strokeStyle = '#1a1400';
  g.beginPath();
  g.arc(cx, cy, rad, Math.PI, Math.PI + Math.PI * needleFrac);
  g.lineWidth = 4;
  g.stroke();
  const ang = Math.PI + Math.PI * needleFrac;
  g.save();
  g.globalCompositeOperation = 'lighter';
  g.strokeStyle = '#ffffff';
  g.lineWidth = 2;
  g.beginPath();
  g.moveTo(cx, cy);
  g.lineTo(cx + Math.cos(ang) * rad * 0.85, cy + Math.sin(ang) * rad * 0.85);
  g.stroke();
  // motion flicker ticks (kept fully inside the brick box, no travel-off-edge)
  for (let i = 0; i < 3; i++) {
    const flicker = 0.35 + 0.35 * Math.sin(t * 8 + i * 1.7);
    g.globalAlpha = Math.max(0, flicker - i * 0.05);
    g.beginPath();
    g.moveTo(5 + i * 4, 5 + i * 2.2);
    g.lineTo(11 + i * 4, 5 + i * 2.2);
    g.stroke();
  }
  g.restore();
  g.restore();
  flashOverlay(g, w, h, state);
}

function drawAttic(g, w, h, t, state) {
  baseRect(g, w, h, BRICK_TYPES.attic.color, state?.ballAbove);
  const glow = state?.ballAbove ? (0.5 + 0.3 * Math.sin(t * 10)) : 0.12 + 0.06 * Math.sin(t * 2);
  g.save();
  g.translate(w / 2, h * 0.62);
  // roof silhouette
  g.beginPath();
  g.moveTo(-w * 0.32, h * 0.1);
  g.lineTo(0, -h * 0.42);
  g.lineTo(w * 0.32, h * 0.1);
  g.closePath();
  g.fillStyle = 'rgba(10,4,25,0.85)';
  g.fill();
  g.save();
  g.globalCompositeOperation = 'lighter';
  g.globalAlpha = glow;
  g.fillStyle = '#ffffff';
  g.fill();
  g.restore();
  // up arrow above roof
  g.save();
  g.globalCompositeOperation = 'lighter';
  g.globalAlpha = 0.6 + 0.3 * Math.sin(t * 6);
  g.strokeStyle = '#ffffff';
  g.lineWidth = 2.2;
  const ay = -h * 0.52 - 2 * Math.sin(t * 5);
  g.beginPath();
  g.moveTo(0, ay);
  g.lineTo(0, ay - 8);
  g.moveTo(-4, ay - 4);
  g.lineTo(0, ay - 8);
  g.lineTo(4, ay - 4);
  g.stroke();
  g.restore();
  g.restore();
  flashOverlay(g, w, h, state);
}

function drawShield(g, w, h, t, state) {
  baseRect(g, w, h, BRICK_TYPES.shield.color, true);
  const cx = w / 2, cy = h / 2;
  const ang = state?.shieldAngle !== undefined ? state.shieldAngle : t;
  const arc = state?.shieldArc !== undefined ? state.shieldArc : Math.PI * 0.55;
  // Hug the brick's silhouette: fit an ellipse (rx,ry) inside the box with
  // a small inset, instead of the old fixed radius that overshot into the
  // rows above/below. The collision test (hitBy) is untouched — it compares
  // raw angles, not pixel geometry — so this is purely cosmetic containment.
  const rx = Math.max(4, w / 2 - 6);
  const ry = Math.max(4, h / 2 - 4);
  g.save();
  g.translate(cx, cy);
  // core
  g.beginPath();
  g.arc(0, 0, Math.min(rx, ry) * 0.55, 0, TAU);
  g.fillStyle = 'rgba(5,20,15,0.8)';
  g.fill();
  // orbiting shield arc, traced manually as an ellipse polyline (uniform
  // stroke width regardless of aspect ratio — a scale-transform trick here
  // would make the stroke balloon out near the wide ends of the ellipse).
  g.save();
  g.globalCompositeOperation = 'lighter';
  const segs = 20;
  const tracePath = () => {
    g.beginPath();
    for (let i = 0; i <= segs; i++) {
      const a = ang - arc / 2 + (arc * i) / segs;
      const ex = Math.cos(a) * rx, ey = Math.sin(a) * ry;
      if (i === 0) g.moveTo(ex, ey); else g.lineTo(ex, ey);
    }
  };
  g.strokeStyle = '#bfffe4';
  g.lineCap = 'round';
  g.lineJoin = 'round';
  tracePath();
  g.lineWidth = 3.2;
  g.stroke();
  g.globalAlpha = 0.4;
  tracePath();
  g.lineWidth = 6;
  g.stroke();
  g.restore();
  g.restore();
  flashOverlay(g, w, h, state);
}

function drawMirror(g, w, h, t, state) {
  baseRect(g, w, h, BRICK_TYPES.mirror.color, true);
  const orient = state?.orient || '/';
  g.save();
  g.beginPath();
  g.rect(0, 0, w, h);
  g.clip();
  g.translate(w / 2, h / 2);
  g.rotate(orient === '/' ? -Math.PI / 4 : Math.PI / 4);
  const grad = g.createLinearGradient(-w, 0, w, 0);
  grad.addColorStop(0, 'rgba(255,255,255,0.05)');
  grad.addColorStop(0.5, 'rgba(255,255,255,0.85)');
  grad.addColorStop(1, 'rgba(255,255,255,0.05)');
  g.fillStyle = grad;
  g.fillRect(-w, -h * 0.16, w * 2, h * 0.32);
  // moving sheen
  g.save();
  g.globalCompositeOperation = 'lighter';
  const sheenX = ((t * 60) % (w * 2)) - w;
  g.globalAlpha = 0.5;
  g.fillStyle = '#ffffff';
  g.fillRect(sheenX, -h, 6, h * 2);
  g.restore();
  g.restore();
  // crack on second-hit state (hp reduced to 1)
  if ((state?.hp || 2) <= 1) {
    g.save();
    g.strokeStyle = 'rgba(20,20,30,0.8)';
    g.lineWidth = 1.4;
    g.beginPath();
    g.moveTo(w * 0.5, 2);
    g.lineTo(w * 0.4, h * 0.4);
    g.lineTo(w * 0.6, h * 0.55);
    g.lineTo(w * 0.45, h - 2);
    g.stroke();
    g.restore();
  }
  flashOverlay(g, w, h, state);
}

function drawMover(g, w, h, t, state) {
  // Neon rail/tram brick: a glowing dual-rail track with a sliding capsule
  // "car" running along it — reads immediately as "this one slides" while
  // matching the dark-neon identity (was an off-palette wooden crate).
  baseRect(g, w, h, BRICK_TYPES.mover.color, true);

  g.save();
  // dual rail lines, inset from top/bottom edge
  g.globalCompositeOperation = 'lighter';
  g.strokeStyle = '#e8faff';
  g.globalAlpha = 0.55;
  g.lineWidth = 1.4;
  g.beginPath();
  g.moveTo(4, 5); g.lineTo(w - 4, 5);
  g.moveTo(4, h - 5); g.lineTo(w - 4, h - 5);
  g.stroke();
  g.restore();

  // scrolling tread dashes between the rails, suggesting continuous motion
  g.save();
  g.strokeStyle = 'rgba(6,16,30,0.55)';
  g.lineWidth = 2.2;
  const dashW = 6, gapW = 5, period = dashW + gapW;
  const scroll = (t * 46) % period;
  g.beginPath();
  for (let x = -period + scroll; x < w; x += period) {
    g.moveTo(x, h / 2);
    g.lineTo(x + dashW, h / 2);
  }
  g.stroke();
  g.restore();

  // the tram capsule itself, sliding is implied by the brick's own motion
  // plus a small idle bob so it never looks static even when moveRange=0
  const bob = Math.sin(t * 5 + (state?.seed || 0)) * 1.2;
  const bw = w * 0.6, bh = h * 0.5;
  const bx = (w - bw) / 2, by = (h - bh) / 2 + bob;
  g.save();
  roundRectPath(g, bx, by, bw, bh, bh * 0.5);
  const bodyGrad = g.createLinearGradient(0, by, 0, by + bh);
  bodyGrad.addColorStop(0, '#f1fbff');
  bodyGrad.addColorStop(1, '#8fe0ff');
  g.fillStyle = bodyGrad;
  g.fill();
  g.strokeStyle = 'rgba(10,30,45,0.5)';
  g.lineWidth = 1;
  g.stroke();
  // window lights
  g.save();
  g.globalCompositeOperation = 'lighter';
  const winN = 3;
  for (let i = 0; i < winN; i++) {
    const wx = bx + bw * (0.22 + i * 0.28);
    const glow = 0.5 + 0.5 * Math.sin(t * 5.5 + i * 1.4 + (state?.seed || 0));
    g.fillStyle = `rgba(70,220,255,${(0.35 + 0.45 * glow).toFixed(3)})`;
    g.beginPath();
    g.arc(wx, by + bh / 2, bh * 0.17, 0, TAU);
    g.fill();
  }
  g.restore();
  g.restore();

  // directional motion chevrons at both rail ends
  g.save();
  g.globalCompositeOperation = 'lighter';
  g.globalAlpha = 0.7;
  g.fillStyle = '#bfeeff';
  const chevBob = Math.sin(t * 6) * 2;
  g.beginPath();
  g.moveTo(3, h / 2 + chevBob);
  g.lineTo(9, h / 2 - 4 + chevBob);
  g.lineTo(9, h / 2 + 4 + chevBob);
  g.closePath();
  g.fill();
  g.beginPath();
  g.moveTo(w - 3, h / 2 - chevBob);
  g.lineTo(w - 9, h / 2 - 4 - chevBob);
  g.lineTo(w - 9, h / 2 + 4 - chevBob);
  g.closePath();
  g.fill();
  g.restore();

  flashOverlay(g, w, h, state);
}

function drawExplosive(g, w, h, t, state) {
  baseRect(g, w, h, BRICK_TYPES.explosive.color, true);
  const cx = w / 2, cy = h / 2;
  const pulse = 0.5 + 0.5 * Math.sin(t * 8);
  g.save();
  g.translate(cx, cy);
  g.beginPath();
  g.arc(0, 0, h * 0.28, 0, TAU);
  g.fillStyle = 'rgba(15,0,0,0.85)';
  g.fill();
  g.save();
  g.globalCompositeOperation = 'lighter';
  g.globalAlpha = 0.5 + 0.5 * pulse;
  g.fillStyle = '#ffdd66';
  g.beginPath();
  g.arc(0, 0, h * 0.16 * (0.7 + 0.3 * pulse), 0, TAU);
  g.fill();
  g.restore();
  // fuse
  g.save();
  g.rotate(-0.5);
  g.strokeStyle = '#3a1a1a';
  g.lineWidth = 2;
  g.beginPath();
  g.moveTo(0, -h * 0.28);
  g.quadraticCurveTo(4, -h * 0.5, -2, -h * 0.62);
  g.stroke();
  g.save();
  g.globalCompositeOperation = 'lighter';
  g.fillStyle = '#ffaa33';
  g.globalAlpha = pulse;
  g.beginPath();
  g.arc(-2, -h * 0.62, 2.4, 0, TAU);
  g.fill();
  g.restore();
  g.restore();
  g.restore();
  flashOverlay(g, w, h, state);
}

function drawMagnet(g, w, h, t, state) {
  baseRect(g, w, h, BRICK_TYPES.magnet.color, true);
  const cx = w / 2, cy = h / 2;
  g.save();
  g.translate(cx, cy);
  g.save();
  g.globalCompositeOperation = 'lighter';
  g.strokeStyle = '#bffcf6';
  g.lineWidth = 1.4;
  for (let i = 0; i < 3; i++) {
    const rad = h * 0.28 + i * 6;
    const phase = t * 1.4 + i * 0.7;
    g.globalAlpha = 0.55 - i * 0.13;
    g.beginPath();
    g.arc(0, 0, rad, phase, phase + Math.PI * 1.3);
    g.stroke();
  }
  g.restore();
  // horseshoe magnet body
  g.rotate(Math.PI);
  g.beginPath();
  g.arc(0, 2, h * 0.22, Math.PI * 0.15, Math.PI * 0.85, false);
  g.lineWidth = 6;
  g.strokeStyle = '#e5395c';
  g.stroke();
  g.beginPath();
  g.arc(0, 2, h * 0.22, Math.PI * 0.85, Math.PI * 1.15, false);
  g.strokeStyle = '#e8e8e8';
  g.stroke();
  g.restore();
  flashOverlay(g, w, h, state);
}

function drawSteel(g, w, h, t, state) {
  baseRect(g, w, h, BRICK_TYPES.steel.color, false);
  g.save();
  // brushed metal streaks
  g.globalAlpha = 0.25;
  g.strokeStyle = '#ffffff';
  g.lineWidth = 1;
  for (let i = 0; i < 6; i++) {
    const yy = 3 + i * (h - 6) / 5;
    g.beginPath();
    g.moveTo(3, yy);
    g.lineTo(w - 3, yy + 1.5);
    g.stroke();
  }
  g.globalAlpha = 1;
  // rivets
  const rivets = [[6, 6], [w - 6, 6], [6, h - 6], [w - 6, h - 6]];
  for (const [rx, ry] of rivets) {
    const grad = g.createRadialGradient(rx - 1, ry - 1, 0.4, rx, ry, 3);
    grad.addColorStop(0, '#ffffff');
    grad.addColorStop(1, '#4b525b');
    g.fillStyle = grad;
    g.beginPath();
    g.arc(rx, ry, 2.6, 0, TAU);
    g.fill();
  }
  if (state?.flashT > 0) {
    g.save();
    g.globalCompositeOperation = 'lighter';
    g.globalAlpha = state.flashT * 0.5;
    g.fillStyle = '#ffe08a';
    g.fillRect(0, 0, w, h);
    g.restore();
  }
  g.restore();
}

const DRAW = {
  normal: drawNormal,
  angle: drawAngle,
  speed: drawSpeed,
  attic: drawAttic,
  shield: drawShield,
  mirror: drawMirror,
  mover: drawMover,
  explosive: drawExplosive,
  magnet: drawMagnet,
  steel: drawSteel,
};

// -----------------------------------------------------------------------
// Legend icon: standalone draw for how-to-play screen. `size` = icon box
// side length (square). Uses a synthetic animated time base internally so
// callers don't need to thread one through.
export function drawLegend(g, x, y, size, type) {
  const t = (typeof performance !== 'undefined' ? performance.now() : Date.now()) / 1000;
  const fakeState = {
    seed: 0,
    dir: -Math.PI / 2,
    lastSpeedFrac: 0.35 + 0.25 * Math.sin(t * 1.3),
    ballAbove: Math.sin(t * 0.7) > 0,
    shieldAngle: t * 1.1,
    shieldArc: Math.PI * 0.55,
    orient: '/',
    hp: 2,
    flashT: 0,
    hintT: 0,
    hueOffset: 0,
    tint: null,
  };
  g.save();
  g.translate(x, y);
  const fn = DRAW[type] || DRAW.normal;
  fn(g, size, size, t, fakeState);
  g.restore();
}

// -----------------------------------------------------------------------
// Break-FX helpers — pure functions the game/FX layer can use to spawn
// type-appropriate particles when a brick breaks, without bricks.js needing
// to know anything about the particle system itself.

/**
 * @param {string} type a BRICK_TYPES key
 * @returns {string} css color string appropriate for that brick's break FX
 */
export function brickBreakColor(type) {
  const meta = BRICK_TYPES[type] || BRICK_TYPES.normal;
  return meta.color;
}

/**
 * @param {string} type a BRICK_TYPES key
 * @returns {{color:string, count:number, shards:boolean, ring:boolean, sfx:string}}
 *   A hint bundle the game can pass straight into FX.burst()-style calls
 *   ({count, shards}) plus whether a shockwave ring reads well (`ring`) and
 *   which sfx name pairs with it (mirrors the sfx used by hitBy verdicts).
 */
export function brickBreakStyle(type) {
  const color = brickBreakColor(type);
  switch (type) {
    case 'normal':    return { color, count: 14, shards: true,  ring: false, sfx: 'brick' };
    case 'angle':     return { color, count: 18, shards: true,  ring: true,  sfx: 'brickAngle' };
    case 'speed':     return { color, count: 22, shards: true,  ring: true,  sfx: 'brickSpeed' };
    case 'attic':     return { color, count: 32, shards: true,  ring: true,  sfx: 'combo' };
    case 'shield':    return { color, count: 22, shards: false, ring: true,  sfx: 'brickShield' };
    case 'mirror':    return { color, count: 16, shards: true,  ring: false, sfx: 'brickMirror' };
    case 'mover':     return { color, count: 16, shards: true,  ring: false, sfx: 'brick' };
    case 'explosive': return { color, count: 40, shards: true,  ring: true,  sfx: 'brickExplode' };
    case 'magnet':    return { color, count: 24, shards: false, ring: true,  sfx: 'brickMagnet' };
    case 'steel':     return { color, count: 28, shards: true,  ring: true,  sfx: 'brickHard' };
    default:          return { color, count: 14, shards: true,  ring: false, sfx: 'brick' };
  }
}

export default { Brick, BRICK_TYPES, BRICK_W, BRICK_H, BRICK_GAP, drawLegend, brickBreakColor, brickBreakStyle };

