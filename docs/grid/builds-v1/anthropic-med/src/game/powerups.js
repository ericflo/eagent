/**
 * src/game/powerups.js — Attic Breaker powerup/drop subsystem.
 *
 * ZERO imports from other src/ modules. Self-contained ES module with its own
 * tiny math helpers. Canvas 2D drawing only, procedural glyphs (no emoji).
 *
 * ---------------------------------------------------------------------------
 * GAME INTERFACE CONTRACT (read/written ONLY via optional chaining, so this
 * module never throws even if `game` is a partial/mock object). The core
 * worker's `src/game/game.js` Game class should implement this shape:
 *
 *   game.balls: Array<{
 *     x, y, vx, vy, r,
 *     flags: { fire?:bool, super?:bool, heavy?:bool, ghost?:bool, sticky?:bool }
 *   }>
 *   game.paddle: {
 *     x, y, w, h, baseW, vy,
 *     flags: { sticky?:bool, magnet?:bool, lasers?:bool }
 *   }
 *   game.lives: number                 (read + written, e.g. extralife)
 *   game.score: number                 (read + written)
 *   game.timeScale: number             (written by slowmo, restored on expiry; default should be 1)
 *   game.shieldCharges: number         (incremented by `shield` powerup)
 *   game.attic: { freeze?: bool }      (attictime sets/clears `freeze`)
 *   game.effects: Map<kind, {t, dur}> (this module owns/ticks this; created lazily)
 *   game.bolts: Array<Bolt>            (this module owns/creates this array lazily)
 *   game.bricks: Array<brick-like>     (read-only here; each brick has x,y,w,h,alive,hitBy?)
 *
 *   Hooks (all optional, called with ?.()):
 *     game.spawnBall(x, y, vx, vy, flags) -> new ball pushed by the caller
 *     game.notify(text, color)            -> HUD toast/banner
 *     game.fx: { burst?, shockwave?, flash?, popup?, spark?, trail?, lightning? , shake? }
 *     game.audio: { sfx?(name, opts) }
 *     game.onBrickBroken?(brick, verdict)  -> called after a laser bolt breaks a brick
 *
 *   brick-like objects (owned by bricks worker) are only touched via:
 *     brick.x, brick.y, brick.w, brick.h, brick.alive
 *     brick.hitBy?.(ball, hit, game) -> {result:'break'|'bounce'|'pass', ...}
 * ---------------------------------------------------------------------------
 */

// ---------------------------------------------------------------------------
// local math helpers (no cross-module imports allowed)
// ---------------------------------------------------------------------------
const TAU = Math.PI * 2;
function clamp(v, a, b) { return v < a ? a : v > b ? b : v; }
function lerp(a, b, t) { return a + (b - a) * t; }
function rand(a = 1, b) { if (b === undefined) { b = a; a = 0; } return a + Math.random() * (b - a); }
function randRange(a, b) { return a + Math.random() * (b - a); }
function pick(arr) { return arr[(Math.random() * arr.length) | 0]; }

// ---------------------------------------------------------------------------
// POWERUPS table
// ---------------------------------------------------------------------------
// weight: relative base weight used by rollDrop's weighted pick (before the
//   contextual smart-weighting adjustments below).
// good: true = beneficial, false = hostile (must look visibly dangerous).
// duration: seconds of active effect (0/undefined = instant, one-shot).
export const POWERUPS = {
  multiball:     { name: 'Multiball',     color: '#5ad8ff', glyph: 'multiball',     desc: 'Splits into extra balls.',              duration: 0,  weight: 14, good: true },
  fireball:      { name: 'Fireball',      color: '#ff7a3c', glyph: 'fireball',      desc: 'Burns straight through bricks.',        duration: 9,  weight: 10, good: true },
  superball:     { name: 'Superball',     color: '#ffe45e', glyph: 'superball',     desc: 'Destroys everything, even steel.',      duration: 5,  weight: 5,  good: true },
  heavyball:     { name: 'Heavyball',     color: '#c9a1ff', glyph: 'heavyball',     desc: 'No speed loss, smashes through.',       duration: 9,  weight: 9,  good: true },
  ghostball:     { name: 'Ghostball',     color: '#bdfff0', glyph: 'ghostball',     desc: 'Passes through one brick layer.',       duration: 9,  weight: 9,  good: true },
  widepaddle:    { name: 'Widepaddle',    color: '#59ffa0', glyph: 'widepaddle',    desc: 'Paddle grows wider.',                   duration: 12, weight: 12, good: true },
  stickypaddle:  { name: 'Stickypaddle',  color: '#ffd166', glyph: 'stickypaddle',  desc: 'Catch and aim the ball.',               duration: 12, weight: 10, good: true },
  lasers:        { name: 'Lasers',        color: '#ff4d9d', glyph: 'lasers',       desc: 'Paddle fires laser bolts.',             duration: 10, weight: 9,  good: true },
  magnetpaddle:  { name: 'Magnetpaddle',  color: '#7dffd6', glyph: 'magnetpaddle',  desc: 'Paddle pulls the ball in gently.',      duration: 12, weight: 8,  good: true },
  slowmo:        { name: 'Slow-Mo',       color: '#8ec5ff', glyph: 'slowmo',        desc: 'Time dilates.',                        duration: 7,  weight: 8,  good: true },
  extralife:     { name: 'Extra Life',    color: '#ff5d7a', glyph: 'extralife',     desc: 'One more life.',                       duration: 0,  weight: 4,  good: true },
  shield:        { name: 'Shield',        color: '#9dff5c', glyph: 'shield',        desc: 'One free save at the death line.',      duration: 0,  weight: 7,  good: true },
  attictime:     { name: 'Attic Time',    color: '#c1ffef', glyph: 'attictime',     desc: 'Freezes attic multiplier decay.',       duration: 8,  weight: 8,  good: true },
  narrowpaddle:  { name: 'Narrowpaddle',  color: '#ff2b3d', glyph: 'narrowpaddle',  desc: 'Paddle shrinks. Hostile.',              duration: 10, weight: 7,  good: false },
  speedup:       { name: 'Speedup',       color: '#ff2b3d', glyph: 'speedup',       desc: 'Ball speed spikes. Hostile.',           duration: 9,  weight: 7,  good: false },
};

const KIND_LIST = Object.keys(POWERUPS);
const GOOD_KINDS = KIND_LIST.filter(k => POWERUPS[k].good);
const BAD_KINDS = KIND_LIST.filter(k => !POWERUPS[k].good);

/** Widepaddle/narrowpaddle are mutually exclusive width modifiers. */
const WIDTH_KINDS = new Set(['widepaddle', 'narrowpaddle']);

export function describePowerup(kind) {
  const p = POWERUPS[kind];
  if (!p) return '';
  return `${p.name} — ${p.desc}`;
}

// ---------------------------------------------------------------------------
// Drop — a falling capsule spawned when a brick breaks.
// ---------------------------------------------------------------------------
const DROP_R = 22;      // capsule half-height-ish radius
const DROP_FALL_SPEED = 260; // units/sec base fall speed

export class Drop {
  /** @param {number} x @param {number} y @param {string} kind */
  constructor(x, y, kind) {
    this.x = x;
    this.y = y;
    this.kind = kind;
    this.vy = DROP_FALL_SPEED;
    this.t = 0;
    this.wobblePhase = rand(TAU);
    this.wobbleSpeed = randRange(2.2, 3.4);
    this.caught = false;
    this.dead = false;
    this.r = DROP_R;
    // shimmer trail history (small ring buffer of positions)
    this._trail = [];
  }

  /** advance falling motion + wobble. dt in seconds. */
  update(dt) {
    this.t += dt;
    this.y += this.vy * dt;
    this.x += Math.sin(this.t * this.wobbleSpeed + this.wobblePhase) * 22 * dt;
    // record shimmer trail every frame, cap length
    this._trail.push({ x: this.x, y: this.y });
    if (this._trail.length > 10) this._trail.shift();
    if (this.y - this.r > 1520) this.dead = true;
  }

  /** true if this drop's capsule overlaps an axis-aligned paddle box */
  overlapsPaddle(paddle) {
    if (!paddle) return false;
    const pw = paddle.w ?? 190, ph = paddle.h ?? 22;
    const left = (paddle.x ?? 0) - pw / 2, right = (paddle.x ?? 0) + pw / 2;
    const top = (paddle.y ?? 0) - ph / 2, bottom = (paddle.y ?? 0) + ph / 2;
    return this.x + this.r > left && this.x - this.r < right &&
           this.y + this.r > top && this.y - this.r < bottom;
  }

  /** draw the capsule + glyph + trail. g = world ctx, t = elapsed seconds */
  draw(g, t) {
    const info = POWERUPS[this.kind] || POWERUPS.multiball;
    const good = info.good;
    // shimmer trail
    g.save();
    g.globalCompositeOperation = 'lighter';
    for (let i = 0; i < this._trail.length; i++) {
      const p = this._trail[i];
      const a = (i / this._trail.length) * 0.25;
      g.globalAlpha = a;
      g.fillStyle = info.color;
      g.beginPath();
      g.arc(p.x, p.y, this.r * 0.55, 0, TAU);
      g.fill();
    }
    g.restore();

    g.save();
    g.translate(this.x, this.y);
    const wob = Math.sin(this.t * this.wobbleSpeed * 1.3) * 0.08;
    g.rotate(wob);

    // glow halo
    g.save();
    g.globalCompositeOperation = 'lighter';
    const glowR = this.r * (good ? 1.9 : 2.15);
    const grad = g.createRadialGradient(0, 0, 1, 0, 0, glowR);
    grad.addColorStop(0, hexA(info.color, 0.55));
    grad.addColorStop(1, hexA(info.color, 0));
    g.fillStyle = grad;
    g.beginPath();
    g.arc(0, 0, glowR, 0, TAU);
    g.fill();
    g.restore();

    // capsule body
    const w = this.r * 1.55, h = this.r * 2.0;
    g.beginPath();
    roundRectPath(g, -w / 2, -h / 2, w, h, this.r * 0.85);
    if (good) {
      const bg = g.createLinearGradient(0, -h / 2, 0, h / 2);
      bg.addColorStop(0, '#12141c');
      bg.addColorStop(1, '#05060a');
      g.fillStyle = bg;
    } else {
      // hostile: dark red-black with warning stripes
      g.fillStyle = '#160406';
    }
    g.fill();
    g.lineWidth = 2.6;
    g.strokeStyle = info.color;
    g.stroke();

    if (!good) {
      // warning stripes clipped to capsule
      g.save();
      g.clip();
      g.strokeStyle = hexA(info.color, 0.55);
      g.lineWidth = 5;
      for (let sx = -w; sx < w; sx += 9) {
        g.beginPath();
        g.moveTo(sx, -h);
        g.lineTo(sx + h * 1.4, h);
        g.stroke();
      }
      g.restore();
      // spiky burrs around the capsule
      g.strokeStyle = info.color;
      g.lineWidth = 2.2;
      const spikes = 8;
      for (let i = 0; i < spikes; i++) {
        const a = (i / spikes) * TAU + this.t * 1.4;
        const rx = Math.cos(a) * (w / 2), ry = Math.sin(a) * (h / 2);
        const rx2 = Math.cos(a) * (w / 2 + 7), ry2 = Math.sin(a) * (h / 2 + 7);
        g.beginPath();
        g.moveTo(rx, ry);
        g.lineTo(rx2, ry2);
        g.stroke();
      }
    }

    // glyph inside, drawn at local origin, scaled to fit
    g.save();
    g.lineWidth = 2.4;
    g.strokeStyle = info.color;
    g.fillStyle = info.color;
    drawGlyph(g, info.glyph, 0, 0, this.r * 1.05, t, info.color);
    g.restore();

    g.restore();
  }
}

function hexA(hex, a) {
  // hex like #rrggbb -> rgba string
  const n = parseInt(hex.slice(1), 16);
  const r = (n >> 16) & 255, gg = (n >> 8) & 255, b = n & 255;
  return `rgba(${r},${gg},${b},${a})`;
}

function roundRectPath(g, x, y, w, h, r) {
  const rr = Math.min(r, w / 2, h / 2);
  g.moveTo(x + rr, y);
  g.arcTo(x + w, y, x + w, y + h, rr);
  g.arcTo(x + w, y + h, x, y + h, rr);
  g.arcTo(x, y + h, x, y, rr);
  g.arcTo(x, y, x + w, y, rr);
  g.closePath();
}

// ---------------------------------------------------------------------------
// Procedural glyphs — one per kind, path-based, animated by `t` (seconds).
// Drawn centered at local origin (0,0) within roughly a `s`-radius box.
// ---------------------------------------------------------------------------
function drawGlyph(g, kind, cx, cy, s, t, color) {
  g.save();
  g.translate(cx, cy);
  switch (kind) {
    case 'multiball': glyphMultiball(g, s, t); break;
    case 'fireball': glyphFireball(g, s, t); break;
    case 'superball': glyphSuperball(g, s, t); break;
    case 'heavyball': glyphHeavyball(g, s, t); break;
    case 'ghostball': glyphGhostball(g, s, t); break;
    case 'widepaddle': glyphWidepaddle(g, s, t); break;
    case 'stickypaddle': glyphStickypaddle(g, s, t); break;
    case 'lasers': glyphLasers(g, s, t); break;
    case 'magnetpaddle': glyphMagnet(g, s, t); break;
    case 'slowmo': glyphSlowmo(g, s, t); break;
    case 'extralife': glyphExtralife(g, s, t); break;
    case 'shield': glyphShield(g, s, t); break;
    case 'attictime': glyphAttictime(g, s, t); break;
    case 'narrowpaddle': glyphNarrowpaddle(g, s, t); break;
    case 'speedup': glyphSpeedup(g, s, t); break;
  }
  g.restore();
}

// three small orbiting balls
function glyphMultiball(g, s, t) {
  g.save();
  g.rotate(t * 1.6);
  for (let i = 0; i < 3; i++) {
    const a = (i / 3) * TAU;
    const r = s * 0.5;
    const x = Math.cos(a) * r, y = Math.sin(a) * r;
    g.beginPath();
    g.arc(x, y, s * 0.22, 0, TAU);
    g.fill();
  }
  g.restore();
}

// ball with flickering flame tongues
function glyphFireball(g, s, t) {
  g.beginPath();
  g.arc(0, 0, s * 0.42, 0, TAU);
  g.fill();
  g.save();
  g.globalCompositeOperation = 'lighter';
  for (let i = 0; i < 5; i++) {
    const a = (i / 5) * TAU + t * 2.2;
    const wob = Math.sin(t * 6 + i) * 0.15;
    const r0 = s * 0.4, r1 = s * (0.85 + wob * 0.3);
    const x0 = Math.cos(a) * r0, y0 = Math.sin(a) * r0;
    const x1 = Math.cos(a + 0.25) * r1, y1 = Math.sin(a + 0.25) * r1;
    g.beginPath();
    g.moveTo(x0, y0);
    g.quadraticCurveTo(Math.cos(a + 0.1) * (r1 * 0.7), Math.sin(a + 0.1) * (r1 * 0.7), x1, y1);
    g.lineWidth = s * 0.16;
    g.stroke();
  }
  g.restore();
}

// bright starburst ball
function glyphSuperball(g, s, t) {
  g.save();
  g.rotate(t * 1.1);
  const spikes = 8;
  g.beginPath();
  for (let i = 0; i < spikes * 2; i++) {
    const a = (i / (spikes * 2)) * TAU;
    const r = i % 2 === 0 ? s * 0.9 : s * 0.4;
    const x = Math.cos(a) * r, y = Math.sin(a) * r;
    if (i === 0) g.moveTo(x, y); else g.lineTo(x, y);
  }
  g.closePath();
  g.fill();
  g.restore();
}

// ball crushed beneath a barbell (universal "heavy" symbol)
function glyphHeavyball(g, s, t) {
  const bob = Math.sin(t * 3) * s * 0.02;
  g.beginPath();
  g.arc(0, s * 0.08 + bob, s * 0.34, 0, TAU);
  g.fill();
  g.save();
  g.fillStyle = '#0a0a0a';
  g.strokeStyle = '#0a0a0a';
  // barbell bar
  g.fillRect(-s * 0.55, -s * 0.08 + bob, s * 1.1, s * 0.14);
  // barbell weight plates at each end
  g.beginPath(); g.ellipse(-s * 0.5, -s * 0.01 + bob, s * 0.1, s * 0.26, 0, 0, TAU); g.fill();
  g.beginPath(); g.ellipse(s * 0.5, -s * 0.01 + bob, s * 0.1, s * 0.26, 0, 0, TAU); g.fill();
  g.restore();
}

// dashed translucent circle (ghost) with wavy hem
function glyphGhostball(g, s, t) {
  g.save();
  g.globalAlpha = 0.55 + Math.sin(t * 3) * 0.15;
  g.beginPath();
  g.arc(0, -s * 0.05, s * 0.45, Math.PI, 0, false);
  const teeth = 5;
  for (let i = 0; i <= teeth; i++) {
    const x = lerp(s * 0.45, -s * 0.45, i / teeth);
    const y = -s * 0.05 + s * 0.35 + (i % 2 === 0 ? 0 : s * 0.12) + Math.sin(t * 4 + i) * s * 0.03;
    g.lineTo(x, y);
  }
  g.closePath();
  g.fill();
  g.restore();
  g.fillStyle = '#0a0a0a';
  g.beginPath(); g.arc(-s * 0.15, -s * 0.08, s * 0.07, 0, TAU); g.fill();
  g.beginPath(); g.arc(s * 0.15, -s * 0.08, s * 0.07, 0, TAU); g.fill();
}

// wide paddle bar with outward arrows
function glyphWidepaddle(g, s, t) {
  const pulse = 1 + Math.sin(t * 4) * 0.08;
  g.fillRect(-s * 0.55 * pulse, -s * 0.12, s * 1.1 * pulse, s * 0.24);
  g.lineWidth = s * 0.09;
  arrow(g, -s * 0.62 * pulse, 0, -s * 0.95 * pulse, 0);
  arrow(g, s * 0.62 * pulse, 0, s * 0.95 * pulse, 0);
}
function arrow(g, x0, y0, x1, y1) {
  g.beginPath(); g.moveTo(x0, y0); g.lineTo(x1, y1); g.stroke();
  const a = Math.atan2(y1 - y0, x1 - x0);
  const h = 6;
  g.beginPath();
  g.moveTo(x1, y1);
  g.lineTo(x1 - Math.cos(a - 0.5) * h, y1 - Math.sin(a - 0.5) * h);
  g.moveTo(x1, y1);
  g.lineTo(x1 - Math.cos(a + 0.5) * h, y1 - Math.sin(a + 0.5) * h);
  g.stroke();
}

// paddle bar with ball stuck on it, faint bounce
function glyphStickypaddle(g, s, t) {
  g.fillRect(-s * 0.5, s * 0.05, s * 1.0, s * 0.2);
  const bob = Math.abs(Math.sin(t * 2.5)) * s * 0.06;
  g.beginPath();
  g.arc(0, -s * 0.18 - bob, s * 0.24, 0, TAU);
  g.fill();
  g.lineWidth = s * 0.05;
  g.beginPath(); g.moveTo(-s * 0.1, s * 0.05); g.lineTo(-s * 0.1, -s * 0.02); g.stroke();
  g.beginPath(); g.moveTo(s * 0.1, s * 0.05); g.lineTo(s * 0.1, -s * 0.02); g.stroke();
}

// paddle with two upward laser bolts, blinking, muzzle sparks at the tips
function glyphLasers(g, s, t) {
  g.fillRect(-s * 0.55, s * 0.32, s * 1.1, s * 0.2);
  // small turret nubs
  g.fillRect(-s * 0.34, s * 0.12, s * 0.14, s * 0.2);
  g.fillRect(s * 0.2, s * 0.12, s * 0.14, s * 0.2);
  const blink = (Math.sin(t * 12) + 1) / 2;
  g.save();
  g.globalAlpha = 0.65 + blink * 0.35;
  g.lineWidth = s * 0.13;
  g.lineCap = 'round';
  g.beginPath(); g.moveTo(-s * 0.27, s * 0.12); g.lineTo(-s * 0.27, -s * 0.85); g.stroke();
  g.beginPath(); g.moveTo(s * 0.27, s * 0.12); g.lineTo(s * 0.27, -s * 0.85); g.stroke();
  // muzzle spark bursts at the top of each beam
  g.save();
  g.globalCompositeOperation = 'lighter';
  for (const sx of [-s * 0.27, s * 0.27]) {
    for (let i = 0; i < 4; i++) {
      const a = (i / 4) * TAU + t * 6;
      g.beginPath();
      g.moveTo(sx, -s * 0.85);
      g.lineTo(sx + Math.cos(a) * s * 0.16, -s * 0.85 + Math.sin(a) * s * 0.16);
      g.lineWidth = s * 0.05;
      g.stroke();
    }
  }
  g.restore();
  g.restore();
}

// horseshoe magnet opening upward, poles pointing up, pulsing field arcs
function glyphMagnet(g, s, t) {
  g.lineWidth = s * 0.18;
  g.lineCap = 'round';
  // arc forms the bottom "U" of the horseshoe
  g.beginPath();
  g.arc(0, s * 0.05, s * 0.32, Math.PI * 0.08, Math.PI * 0.92, false);
  g.stroke();
  // poles rise up from the arc ends
  g.fillRect(-s * 0.42, -s * 0.42, s * 0.16, s * 0.5);
  g.fillRect(s * 0.26, -s * 0.42, s * 0.16, s * 0.5);
  // pole tip caps (polarity bands), slightly lighter via alpha
  g.save();
  g.globalAlpha = 0.85;
  g.fillRect(-s * 0.42, -s * 0.42, s * 0.16, s * 0.12);
  g.fillRect(s * 0.26, -s * 0.42, s * 0.16, s * 0.12);
  g.restore();
  // pulsing attraction field lines rising from the poles
  g.save();
  g.globalAlpha = 0.35 + Math.sin(t * 5) * 0.25;
  g.lineWidth = s * 0.05;
  for (let i = 0; i < 2; i++) {
    const r = s * (0.55 + i * 0.16);
    g.beginPath();
    g.arc(0, s * 0.05, r, Math.PI * 1.15, Math.PI * 1.85, false);
    g.stroke();
  }
  g.restore();
}

// clock with slow hands + radiating dilation rings
function glyphSlowmo(g, s, t) {
  g.lineWidth = s * 0.09;
  g.beginPath(); g.arc(0, 0, s * 0.45, 0, TAU); g.stroke();
  const a = t * 0.6;
  g.beginPath(); g.moveTo(0, 0); g.lineTo(Math.cos(a) * s * 0.28, Math.sin(a) * s * 0.28); g.stroke();
  g.beginPath(); g.moveTo(0, 0); g.lineTo(Math.cos(a * 0.3) * s * 0.16, Math.sin(a * 0.3) * s * 0.16); g.stroke();
  g.save();
  g.globalAlpha = 0.25;
  g.beginPath(); g.arc(0, 0, s * (0.6 + Math.sin(t) * 0.05), 0, TAU); g.stroke();
  g.restore();
}

// beating heart
function glyphExtralife(g, s, t) {
  const beat = 1 + Math.max(0, Math.sin(t * 5)) * 0.12;
  g.save();
  g.scale(beat, beat);
  g.beginPath();
  const w = s * 0.5;
  g.moveTo(0, w * 0.35);
  g.bezierCurveTo(w, -w * 0.5, w * 0.2, -w, 0, -w * 0.25);
  g.bezierCurveTo(-w * 0.2, -w, -w, -w * 0.5, 0, w * 0.35);
  g.closePath();
  g.fill();
  g.restore();
}

// hexagonal shield with inner glow flicker
function glyphShield(g, s, t) {
  g.beginPath();
  for (let i = 0; i < 6; i++) {
    const a = -Math.PI / 2 + (i / 6) * TAU;
    const x = Math.cos(a) * s * 0.48, y = Math.sin(a) * s * 0.48;
    if (i === 0) g.moveTo(x, y); else g.lineTo(x, y);
  }
  g.closePath();
  g.lineWidth = s * 0.1;
  g.stroke();
  g.save();
  g.globalAlpha = 0.3 + Math.sin(t * 4) * 0.15;
  g.fill();
  g.restore();
}

// little attic roof/house shape with an hourglass frozen inside
function glyphAttictime(g, s, t) {
  g.beginPath();
  g.moveTo(-s * 0.45, s * 0.1);
  g.lineTo(0, -s * 0.45);
  g.lineTo(s * 0.45, s * 0.1);
  g.closePath();
  g.lineWidth = s * 0.09;
  g.stroke();
  g.save();
  g.translate(0, s * 0.18);
  g.scale(0.35, 0.35);
  const flow = (Math.sin(t * 2) + 1) / 2;
  g.beginPath();
  g.moveTo(-s * 0.5, -s * 0.5); g.lineTo(s * 0.5, -s * 0.5); g.lineTo(0, 0);
  g.lineTo(s * 0.5, s * 0.5); g.lineTo(-s * 0.5, s * 0.5); g.closePath();
  g.lineWidth = s * 0.12;
  g.stroke();
  g.globalAlpha = 0.6;
  g.beginPath();
  g.arc(0, -s * 0.15 * flow, s * 0.08, 0, TAU);
  g.fill();
  g.restore();
}

// squeezed paddle with inward hostile arrows + jagged edges
function glyphNarrowpaddle(g, s, t) {
  const pulse = 1 - Math.sin(t * 5) * 0.1;
  g.save();
  g.strokeStyle = '#ff2b3d';
  g.fillStyle = '#ff2b3d';
  g.fillRect(-s * 0.32 * pulse, -s * 0.1, s * 0.64 * pulse, s * 0.2);
  g.lineWidth = s * 0.09;
  arrow(g, -s * 0.85, 0, -s * 0.4 * pulse, 0);
  arrow(g, s * 0.85, 0, s * 0.4 * pulse, 0);
  g.beginPath();
  for (let i = -3; i <= 3; i++) {
    g.moveTo(i * s * 0.15, -s * 0.15);
    g.lineTo(i * s * 0.15 + s * 0.07, -s * 0.3);
  }
  g.lineWidth = s * 0.06;
  g.stroke();
  g.restore();
}

// jagged spiked ball with speed lines
function glyphSpeedup(g, s, t) {
  g.save();
  g.strokeStyle = '#ff2b3d';
  g.fillStyle = '#ff2b3d';
  g.rotate(t * 3);
  const spikes = 7;
  g.beginPath();
  for (let i = 0; i < spikes * 2; i++) {
    const a = (i / (spikes * 2)) * TAU;
    const r = i % 2 === 0 ? s * 0.45 : s * 0.22;
    const x = Math.cos(a) * r, y = Math.sin(a) * r;
    if (i === 0) g.moveTo(x, y); else g.lineTo(x, y);
  }
  g.closePath();
  g.fill();
  g.restore();
  g.save();
  g.globalAlpha = 0.7;
  g.lineWidth = s * 0.07;
  for (let i = 0; i < 3; i++) {
    const yy = (i - 1) * s * 0.22;
    g.beginPath();
    g.moveTo(-s * 0.9, yy);
    g.lineTo(-s * 0.55 - ((t * 40) % (s * 0.3)), yy);
    g.stroke();
  }
  g.restore();
}

/**
 * Draw a standalone HUD/legend icon (badge + glyph, no falling capsule chrome).
 * @param {CanvasRenderingContext2D} g
 * @param {number} x @param {number} y @param {number} size - overall diameter
 * @param {string} kind
 */
export function drawPowerupIcon(g, x, y, size, kind) {
  const info = POWERUPS[kind];
  if (!info) return;
  const t = (typeof performance !== 'undefined' ? performance.now() : Date.now()) / 1000;
  g.save();
  g.translate(x, y);
  const r = size / 2;
  g.save();
  g.globalCompositeOperation = 'lighter';
  const grad = g.createRadialGradient(0, 0, 1, 0, 0, r * 1.3);
  grad.addColorStop(0, hexA(info.color, 0.45));
  grad.addColorStop(1, hexA(info.color, 0));
  g.fillStyle = grad;
  g.beginPath(); g.arc(0, 0, r * 1.3, 0, TAU); g.fill();
  g.restore();
  g.beginPath();
  g.arc(0, 0, r * 0.92, 0, TAU);
  g.fillStyle = info.good ? '#0c0e14' : '#160406';
  g.fill();
  g.lineWidth = Math.max(1.5, size * 0.045);
  g.strokeStyle = info.color;
  g.stroke();
  g.strokeStyle = info.color;
  g.fillStyle = info.color;
  drawGlyph(g, info.glyph, 0, 0, r * 0.72, t, info.color);
  g.restore();
}

// ---------------------------------------------------------------------------
// rollDrop — weighted random drop chooser.
// ---------------------------------------------------------------------------
const BASE_DROP_CHANCE = 0.22;
const MAX_BAD_FRACTION = 0.18; // hard cap regardless of multiplier scaling

/**
 * Decide whether a broken brick spawns a Drop, and which kind.
 * @param {number} x @param {number} y
 * @param {object} [ctxInfo] optional context, all fields optional:
 *   { level, mult, ballCount, inAttic, activeKinds, lives, dropChance }
 * @returns {Drop|null}
 */
export function rollDrop(x, y, ctxInfo) {
  const ctx = ctxInfo || {};
  const level = ctx.level ?? 1;
  const mult = ctx.mult ?? 1;
  const ballCount = ctx.ballCount ?? 1;
  const inAttic = !!ctx.inAttic;
  const activeKinds = ctx.activeKinds ?? [];
  const lives = ctx.lives ?? 3;
  const dropChance = ctx.dropChance ?? BASE_DROP_CHANCE;

  if (Math.random() > dropChance) return null;

  // Build weight table per-kind starting from base weights.
  const weights = {};
  for (const k of KIND_LIST) weights[k] = POWERUPS[k].weight;

  // Don't drop multiball when there are already many balls in play.
  if (ballCount >= 4) weights.multiball *= 0.05;
  else if (ballCount >= 2) weights.multiball *= 0.4;

  // Early in a level (low mult, not yet in attic), favour attic-enabling
  // powerups: ghostball/heavyball/superball help carve a path upward, and
  // widepaddle/stickypaddle help control. Attictime is only useful once in
  // the attic, so downweight it before that's reachable.
  if (!inAttic && mult < 2) {
    weights.ghostball *= 1.6;
    weights.heavyball *= 1.5;
    weights.superball *= 1.3;
    weights.widepaddle *= 1.3;
    weights.attictime *= 0.4;
  } else if (inAttic) {
    weights.attictime *= 1.8;
    weights.multiball *= 1.3;
  }

  // Re-picking an already-active kind is fine (refreshes duration) but we
  // slightly favour variety by trimming duplicates' weight a bit, except
  // for stackable instantaneous ones.
  for (const k of activeKinds) {
    if (weights[k] !== undefined && POWERUPS[k].duration > 0) weights[k] *= 0.6;
  }

  // Risk/reward: bad drops become more likely at high multiplier, but the
  // *total* bad-drop probability mass is hard-capped well under 18% to leave
  // margin for sampling noise over finite trials.
  const riskFactor = clamp((mult - 1) / 6, 0, 1); // 0 at mult=1, 1 by mult=7
  const levelFactor = 1 + clamp((level - 1) * 0.03, 0, 0.3);
  const badScale = (1 + riskFactor * 2.2) * levelFactor;
  for (const k of BAD_KINDS) weights[k] *= badScale;

  // Mercy weighting: when lives are low, boost extralife (and shield) hard.
  if (lives <= 1) { weights.extralife *= 8; weights.shield *= 3; }
  else if (lives === 2) { weights.extralife *= 3; weights.shield *= 1.6; }

  // Enforce a hard cap on bad-drop probability mass (target kept safely
  // below the 18% ceiling so finite-sample noise can't push realized rates
  // over the limit).
  const HARD_CAP_TARGET = 0.15;
  let goodTotal = 0, badTotal = 0;
  for (const k of GOOD_KINDS) goodTotal += weights[k];
  for (const k of BAD_KINDS) badTotal += weights[k];
  const total0 = goodTotal + badTotal;
  if (total0 > 0 && badTotal / total0 > HARD_CAP_TARGET) {
    const targetBad = (HARD_CAP_TARGET * goodTotal) / (1 - HARD_CAP_TARGET);
    const scale = badTotal > 0 ? targetBad / badTotal : 0;
    for (const k of BAD_KINDS) weights[k] *= scale;
  }

  // Weighted pick.
  let total = 0;
  for (const k of KIND_LIST) total += Math.max(0, weights[k]);
  if (total <= 0) return null;
  let r = Math.random() * total;
  let chosen = KIND_LIST[0];
  for (const k of KIND_LIST) {
    const w = Math.max(0, weights[k]);
    if (r < w) { chosen = k; break; }
    r -= w;
  }
  return new Drop(x, y, chosen);
}

// ---------------------------------------------------------------------------
// applyPowerup — mutate `game` state per the documented contract.
// ---------------------------------------------------------------------------
function ensureEffects(game) {
  if (game && !(game.effects instanceof Map)) game.effects = new Map();
  return game?.effects;
}

/**
 * Apply the effect of catching `kind`. Safe against a partial/mock `game`.
 * @param {string} kind
 * @param {object} game
 * @returns {{name:string, color:string, text:string}}
 */
export function applyPowerup(kind, game) {
  const info = POWERUPS[kind];
  if (!info) return { name: '', color: '#fff', text: '' };
  const effects = ensureEffects(game);
  const text = describePowerup(kind);

  switch (kind) {
    case 'multiball': {
      const balls = game?.balls ?? [];
      const src = balls[0];
      const bx = src?.x ?? 500, by = src?.y ?? 1100;
      const baseVx = src?.vx ?? 0, baseVy = src?.vy ?? -800;
      const speed = Math.hypot(baseVx, baseVy) || 800;
      const baseAngle = Math.atan2(baseVy, baseVx);
      const spawnCount = 2;
      for (let i = 0; i < spawnCount; i++) {
        const da = (i === 0 ? -1 : 1) * randRange(0.35, 0.6);
        const a = baseAngle + da;
        game?.spawnBall?.(bx, by, Math.cos(a) * speed, Math.sin(a) * speed, {});
      }
      game?.audio?.sfx?.('multiUp');
      break;
    }
    case 'fireball':
      setBallFlagEffect(game, effects, 'fire', kind, info.duration);
      break;
    case 'superball':
      setBallFlagEffect(game, effects, 'super', kind, info.duration);
      break;
    case 'heavyball':
      setBallFlagEffect(game, effects, 'heavy', kind, info.duration);
      break;
    case 'ghostball':
      setBallFlagEffect(game, effects, 'ghost', kind, info.duration);
      break;
    case 'widepaddle': {
      cancelOpposite(effects, 'narrowpaddle');
      const p = game?.paddle;
      if (p) { p.baseW = p.baseW ?? p.w ?? 190; p.w = p.baseW * 1.55; }
      startTimedEffect(effects, kind, info.duration);
      break;
    }
    case 'narrowpaddle': {
      cancelOpposite(effects, 'widepaddle');
      const p = game?.paddle;
      if (p) { p.baseW = p.baseW ?? p.w ?? 190; p.w = p.baseW * 0.6; }
      startTimedEffect(effects, kind, info.duration);
      break;
    }
    case 'stickypaddle': {
      if (game?.paddle) game.paddle.flags = { ...(game.paddle.flags ?? {}), sticky: true };
      startTimedEffect(effects, kind, info.duration);
      break;
    }
    case 'lasers': {
      if (game?.paddle) game.paddle.flags = { ...(game.paddle.flags ?? {}), lasers: true };
      startTimedEffect(effects, kind, info.duration);
      break;
    }
    case 'magnetpaddle': {
      if (game?.paddle) game.paddle.flags = { ...(game.paddle.flags ?? {}), magnet: true };
      startTimedEffect(effects, kind, info.duration);
      break;
    }
    case 'speedup': {
      const balls = game?.balls ?? [];
      for (const b of balls) {
        const sp = Math.hypot(b.vx ?? 0, b.vy ?? 0) || 1;
        const scale = 1.5;
        b.vx = (b.vx ?? 0) * scale; b.vy = (b.vy ?? 0) * scale;
        b._speedupOrigSpeed = b._speedupOrigSpeed ?? sp;
      }
      startTimedEffect(effects, kind, info.duration);
      break;
    }
    case 'slowmo':
      if (game) game.timeScale = 0.45;
      startTimedEffect(effects, kind, info.duration);
      break;
    case 'extralife':
      if (game) game.lives = (game.lives ?? 0) + 1;
      game?.audio?.sfx?.('life');
      break;
    case 'shield':
      if (game) game.shieldCharges = (game.shieldCharges ?? 0) + 1;
      break;
    case 'attictime':
      if (game?.attic) game.attic.freeze = true;
      startTimedEffect(effects, kind, info.duration);
      break;
    default:
      break;
  }

  game?.notify?.(info.name, info.color);
  game?.fx?.popup?.(game?.paddle?.x ?? 500, (game?.paddle?.y ?? 1300) - 40, info.name, { color: info.color });
  game?.fx?.burst?.(game?.paddle?.x ?? 500, game?.paddle?.y ?? 1300, { count: 18, color: info.color, speed: 260, spread: TAU, life: 0.5, glow: true });
  game?.audio?.sfx?.(info.good ? 'powerup' : 'powerdown');

  return { name: info.name, color: info.color, text };
}

function startTimedEffect(effects, kind, dur) {
  if (!effects || !dur) return;
  effects.set(kind, { t: 0, dur });
}

function cancelOpposite(effects, otherKind) {
  if (effects?.has?.(otherKind)) effects.delete(otherKind);
}

/** turn on a ball flag for all current balls + tag new balls implicitly via effect revert list */
function setBallFlagEffect(game, effects, flag, kind, dur) {
  const balls = game?.balls ?? [];
  for (const b of balls) {
    b.flags = b.flags ?? {};
    b.flags[flag] = true;
  }
  startTimedEffect(effects, kind, dur);
  game?.audio?.sfx?.(kind === 'superball' ? 'superball' : 'powerup');
}

// ---------------------------------------------------------------------------
// updatePowerups — tick timers, expire effects & revert them.
// ---------------------------------------------------------------------------

/** Reverts the concrete game-state change made by `kind`. */
function revertEffect(kind, game) {
  const info = POWERUPS[kind];
  switch (kind) {
    case 'fireball': clearBallFlag(game, 'fire'); break;
    case 'superball': clearBallFlag(game, 'super'); break;
    case 'heavyball': clearBallFlag(game, 'heavy'); break;
    case 'ghostball': clearBallFlag(game, 'ghost'); break;
    case 'widepaddle':
    case 'narrowpaddle': {
      const p = game?.paddle;
      if (p) p.w = p.baseW ?? p.w;
      break;
    }
    case 'stickypaddle':
      if (game?.paddle?.flags) game.paddle.flags.sticky = false;
      break;
    case 'lasers':
      if (game?.paddle?.flags) game.paddle.flags.lasers = false;
      break;
    case 'magnetpaddle':
      if (game?.paddle?.flags) game.paddle.flags.magnet = false;
      break;
    case 'slowmo':
      if (game) game.timeScale = 1;
      break;
    case 'speedup': {
      const balls = game?.balls ?? [];
      for (const b of balls) {
        if (b._speedupOrigSpeed) {
          const cur = Math.hypot(b.vx ?? 0, b.vy ?? 0) || 1;
          const scale = b._speedupOrigSpeed / cur;
          b.vx = (b.vx ?? 0) * scale;
          b.vy = (b.vy ?? 0) * scale;
          delete b._speedupOrigSpeed;
        }
      }
      break;
    }
    case 'attictime':
      if (game?.attic) game.attic.freeze = false;
      break;
    default:
      break;
  }
  game?.notify?.(`${info?.name ?? kind} wore off`, info?.color ?? '#888');
}

function clearBallFlag(game, flag) {
  const balls = game?.balls ?? [];
  for (const b of balls) { if (b.flags) b.flags[flag] = false; }
}

/**
 * Advance all active timed effects by `dt` seconds; expire & revert any that
 * finish. Also advances the laser bolt subsystem.
 * @param {object} game
 * @param {number} dt
 */
export function updatePowerups(game, dt) {
  const effects = ensureEffects(game);
  if (effects) {
    for (const [kind, e] of Array.from(effects.entries())) {
      e.t += dt;
      if (e.t >= e.dur) {
        effects.delete(kind);
        revertEffect(kind, game);
      }
    }
  }
  updateBolts(game, dt);
}

/**
 * @param {object} game
 * @returns {Array<{kind,name,color,t,dur,frac}>} sorted by soonest-to-expire first
 */
export function activeList(game) {
  const effects = game?.effects;
  if (!(effects instanceof Map)) return [];
  const out = [];
  for (const [kind, e] of effects.entries()) {
    const info = POWERUPS[kind];
    if (!info) continue;
    out.push({ kind, name: info.name, color: info.color, t: e.t, dur: e.dur, frac: clamp(1 - e.t / e.dur, 0, 1) });
  }
  out.sort((a, b) => (a.dur - a.t) - (b.dur - b.t));
  return out;
}

/** Force-revert and clear all active effects (level transition / death). */
export function clearAllEffects(game) {
  const effects = game?.effects;
  if (effects instanceof Map) {
    for (const kind of Array.from(effects.keys())) revertEffect(kind, game);
    effects.clear();
  }
  if (game) game.timeScale = 1;
  if (game?.attic) game.attic.freeze = false;
  if (game?.paddle) {
    game.paddle.w = game.paddle.baseW ?? game.paddle.w;
    if (game.paddle.flags) { game.paddle.flags.sticky = false; game.paddle.flags.lasers = false; game.paddle.flags.magnet = false; }
  }
  for (const b of game?.balls ?? []) { if (b.flags) { b.flags.fire = false; b.flags.super = false; b.flags.heavy = false; b.flags.ghost = false; } }
}

// ---------------------------------------------------------------------------
// Laser subsystem (owned here since it's a powerup-driven mechanic).
// ---------------------------------------------------------------------------
const BOLT_SPEED = 2400;
const BOLT_W = 6, BOLT_H = 26;

/**
 * Fire a pair of laser bolts from the paddle tips. No-op if paddle has no
 * lasers flag or paddle missing.
 * @param {object} game
 */
export function fireLasers(game) {
  const p = game?.paddle;
  if (!p?.flags?.lasers) return;
  if (!Array.isArray(game.bolts)) game.bolts = [];
  const w = p.w ?? 190, y = p.y ?? 1300;
  game.bolts.push({ x: (p.x ?? 500) - w * 0.32, y, vy: -BOLT_SPEED, alive: true });
  game.bolts.push({ x: (p.x ?? 500) + w * 0.32, y, vy: -BOLT_SPEED, alive: true });
  game?.audio?.sfx?.('laser');
  game?.fx?.spark?.((p.x ?? 500), y, '#ff4d9d', 4);
}

/**
 * Advance bolts and resolve collisions against `game.bricks` via each
 * brick's `hitBy` interface (rect-overlap test done here since bricks are
 * owned by another module).
 * @param {object} game @param {number} dt
 */
export function updateBolts(game, dt) {
  const bolts = game?.bolts;
  if (!Array.isArray(bolts) || bolts.length === 0) return;
  const bricks = game?.bricks ?? [];
  for (const bolt of bolts) {
    if (!bolt.alive) continue;
    bolt.y += bolt.vy * dt;
    if (bolt.y < -40) { bolt.alive = false; continue; }
    for (const brick of bricks) {
      if (!brick?.alive) continue;
      const bx = brick.x, by = brick.y, bw = brick.w, bh = brick.h;
      if (bx === undefined) continue;
      if (bolt.x > bx && bolt.x < bx + bw && bolt.y > by && bolt.y < by + bh) {
        const fakeBall = { x: bolt.x, y: bolt.y, vx: 0, vy: -BOLT_SPEED, r: 4, speed: BOLT_SPEED, flags: {} };
        const verdict = brick.hitBy?.(fakeBall, { nx: 0, ny: 1 }, game);
        if (verdict?.result === 'break') {
          brick.alive = false;
          game?.onBrickBroken?.(brick, verdict);
          game?.fx?.spark?.(bolt.x, bolt.y, '#ff4d9d', 6);
        } else {
          game?.fx?.spark?.(bolt.x, bolt.y, '#ff4d9d', 3);
        }
        bolt.alive = false;
        break;
      }
    }
  }
  // compact dead bolts occasionally (avoid per-frame allocation storms: only
  // filter when there's actually dead weight to remove).
  if (bolts.length > 64) {
    const live = bolts.filter(b => b.alive);
    game.bolts = live;
  } else {
    for (let i = bolts.length - 1; i >= 0; i--) if (!bolts[i].alive) bolts.splice(i, 1);
  }
}

/**
 * Draw all live bolts. g = world ctx, t = elapsed seconds (for flicker).
 * NOTE: the architecture doc's short-form signature is `drawBolts(g,t)`;
 * since bolts live on `game.bolts` (owned by this module but not passed to
 * this pure draw helper otherwise), the actual call site should pass the
 * array explicitly: `drawBolts(g, t, game.bolts)`. The 3rd param is optional
 * and the function is a safe no-op without it.
 * @param {CanvasRenderingContext2D} g @param {number} t @param {Array} [bolts]
 */
export function drawBolts(g, t, bolts) {
  if (!Array.isArray(bolts)) return;
  g.save();
  g.globalCompositeOperation = 'lighter';
  const flicker = 0.7 + Math.sin(t * 30) * 0.3;
  for (const bolt of bolts) {
    if (!bolt.alive) continue;
    g.globalAlpha = flicker;
    const grad = g.createLinearGradient(bolt.x, bolt.y - BOLT_H, bolt.x, bolt.y + BOLT_H * 0.3);
    grad.addColorStop(0, 'rgba(255,77,157,0)');
    grad.addColorStop(0.5, '#ff4d9d');
    grad.addColorStop(1, '#fff');
    g.fillStyle = grad;
    g.fillRect(bolt.x - BOLT_W / 2, bolt.y - BOLT_H, BOLT_W, BOLT_H);
  }
  g.restore();
}
