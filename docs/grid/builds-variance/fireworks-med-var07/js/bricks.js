'use strict';
/* ============================================================
   Attic Breakout — bricks.js
   Brick types (interesting difficulty, no plain multi-hits):
     S  Standard    1 hit, colored by row
     A  Armored     breaks only if impact speed > CONFIG.ARMOR_SPEED
     L  Angle-lock  breaks only on near-perpendicular impact
     R  Regen       cracks on hit, heals after ~5s unless re-hit
     M  Metal       indestructible level geometry
     P  Prism       1 hit, always drops a power-up, refracts light
   ============================================================ */

const BRICK_TYPES = {
  S: { pts: 50,  hp: 1 },
  A: { pts: 150, hp: 1 },
  L: { pts: 150, hp: 1 },
  R: { pts: 120, hp: 1 },
  M: { pts: 0,   hp: Infinity },
  P: { pts: 200, hp: 1 },
};

// Row palette (top → bottom), used for standard bricks & base tint.
const ROW_COLORS = [
  [255, 92, 110], [255, 138, 76], [255, 196, 64], [172, 216, 82],
  [80, 210, 160], [70, 190, 230], [108, 140, 255], [186, 110, 240],
  [240, 100, 200],
];

class Brick {
  constructor(type, col, row, x, y) {
    this.type = type;
    this.col = col; this.row = row;
    this.x = x; this.y = y;
    this.w = CONFIG.CELL_W - CONFIG.BRICK_PAD * 2;
    this.h = CONFIG.BRICK_H;
    this.alive = true;
    this.phase = rand(TAU);       // idle shimmer phase
    this.flash = 0;               // hit flash 0..1
    this.wobble = 0;              // pre-break wobble amplitude
    this.crackT = 0;              // regen: >0 while cracked
    this.crackHeal = 0;           // regen: visual heal animation
    const spec = BRICK_TYPES[type];
    this.pts = spec.pts;
  }

  get cx() { return this.x + this.w / 2; }
  get cy() { return this.y + this.h / 2; }

  baseColor() {
    if (this.type === 'A') return [130, 142, 160];
    if (this.type === 'L') return [96, 210, 255];
    if (this.type === 'R') return [160, 255, 140];
    if (this.type === 'M') return [110, 116, 132];
    if (this.type === 'P') return [225, 160, 255];
    const c = ROW_COLORS[this.row % ROW_COLORS.length];
    return c;
  }

  update(dt) {
    this.phase += dt * 2;
    if (this.flash > 0) this.flash = Math.max(0, this.flash - dt * 5);
    if (this.wobble > 0) this.wobble = Math.max(0, this.wobble - dt * 4);
    if (this.type === 'R' && this.crackT > 0) {
      this.crackT -= dt;
      if (this.crackT <= 0) {
        this.crackT = 0;
        this.crackHeal = 0.45; // heal animation
        return true; // healed — game may play sound/particles
      }
    }
    if (this.crackHeal > 0) this.crackHeal = Math.max(0, this.crackHeal - dt);
    return false;
  }

  /** Register a hit; returns 'break' | 'crack' | 'bounce' | 'clang' | 'shimmer'. */
  hit(speed, dirx, diry, { fireball = false, heavy = false, ghost = false } = {}) {
    if (this.type === 'M') return 'bounce';
    if (this.type === 'A') {
      if (fireball || heavy || speed > CONFIG.ARMOR_SPEED) return 'break';
      return 'clang';
    }
    if (this.type === 'L') {
      // Angle-lock checks impact direction vs the struck face normal.
      // Ghost passes through (no angle check, no bounce).
      if (ghost) return 'phase';
      const face = this.hitFace(dirx, diry);
      const nx = face === 0 ? -1 : face === 1 ? 1 : 0;
      const ny = face === 2 ? -1 : 1;
      const dot = dirx * nx + diry * ny; // cos of angle to normal
      if (-dot >= CONFIG.ANGLE_BREAK_COS) return 'break';
      return 'shimmer';
    }
    if (this.type === 'R') {
      if (ghost) return 'phase';
      if (this.crackT > 0) return 'break';   // chained hit while cracked
      this.crackT = 5;                        // start the 5s heal timer
      return 'crack';
    }
    return 'break'; // S, P
  }

  /** Which face (0 left,1 right,2 top,3 bottom) the ball struck, judged
   *  geometrically from the ball's approach: the face it came *into* is the
   *  one opposing its dominant travel component. */
  hitFace(dx, dy) {
    return Math.abs(dx) > Math.abs(dy) ? (dx > 0 ? 0 : 1) : (dy > 0 ? 2 : 3);
  }

  draw(ctx, time) {
    if (!this.alive) return;
    const [r, g, b] = this.baseColor();
    const wx = this.wobble > 0 ? Math.sin(time * 60) * this.wobble * 2.2 : 0;
    ctx.save();
    ctx.translate(this.cx + wx, this.cy);
    ctx.rotate(this.wobble > 0 ? Math.sin(time * 47) * this.wobble * 0.06 : 0);

    const wobScale = 1 + this.wobble * 0.06;
    const w = this.w * wobScale, h = this.h * wobScale;
    const shade = this.flash;

    // Body
    const grad = ctx.createLinearGradient(0, -h / 2, 0, h / 2);
    const lift = 0.25 + shade * 0.75;
    grad.addColorStop(0, `rgb(${Math.min(255, r + 70 * lift) | 0},${Math.min(255, g + 70 * lift) | 0},${Math.min(255, b + 70 * lift) | 0})`);
    grad.addColorStop(1, `rgb(${(r * (0.62 + shade * 0.3)) | 0},${(g * (0.62 + shade * 0.3)) | 0},${(b * (0.62 + shade * 0.3)) | 0})`);
    ctx.fillStyle = grad;
    this._roundRect(ctx, -w / 2, -h / 2, w, h, 5);
    ctx.fill();

    // Top edge highlight
    ctx.fillStyle = `rgba(255,255,255,${0.18 + shade * 0.4})`;
    this._roundRect(ctx, -w / 2 + 2, -h / 2 + 2, w - 4, 5, 3);
    ctx.fill();

    // Type markings
    if (this.type === 'A') {
      // Rivets
      ctx.fillStyle = 'rgba(255,255,255,0.45)';
      for (const px of [-w / 2 + 9, w / 2 - 9]) {
        ctx.beginPath(); ctx.arc(px, 0, 2.4, 0, TAU); ctx.fill();
      }
      ctx.strokeStyle = 'rgba(255,255,255,0.3)';
      ctx.lineWidth = 1.5;
      ctx.beginPath(); ctx.moveTo(-w / 2 + 15, 0); ctx.lineTo(w / 2 - 15, 0); ctx.stroke();
    } else if (this.type === 'L') {
      // Perpendicular slit/notch markings — the required impact angle
      ctx.strokeStyle = 'rgba(255,255,255,0.85)';
      ctx.lineWidth = 3;
      ctx.beginPath();
      ctx.moveTo(0, -h / 2 + 4); ctx.lineTo(0, h / 2 - 4);
      ctx.stroke();
      ctx.strokeStyle = 'rgba(255,255,255,0.4)';
      ctx.beginPath();
      ctx.moveTo(-w / 2 + 6, 0); ctx.lineTo(-6, 0);
      ctx.moveTo(w / 2 - 6, 0); ctx.lineTo(6, 0);
      ctx.stroke();
    } else if (this.type === 'R') {
      if (this.crackT > 0) {
        // Crack lines + heal timer arc
        ctx.strokeStyle = 'rgba(20,30,20,0.85)';
        ctx.lineWidth = 1.8;
        ctx.beginPath();
        ctx.moveTo(-w * 0.3, -h * 0.3); ctx.lineTo(-w * 0.1, 0); ctx.lineTo(-w * 0.28, h * 0.32);
        ctx.moveTo(w * 0.32, -h * 0.25); ctx.lineTo(w * 0.1, h * 0.05); ctx.lineTo(w * 0.3, h * 0.3);
        ctx.stroke();
        const frac = this.crackT / 5;
        ctx.strokeStyle = frac < 0.3 ? 'rgba(255,90,90,0.95)' : 'rgba(255,255,255,0.8)';
        ctx.lineWidth = 2.4;
        ctx.beginPath();
        ctx.arc(0, 0, 8, -Math.PI / 2, -Math.PI / 2 + TAU * (1 - frac));
        ctx.stroke();
      }
    } else if (this.type === 'M') {
      ctx.strokeStyle = 'rgba(255,255,255,0.18)';
      ctx.lineWidth = 1;
      for (let i = 1; i < 4; i++) {
        ctx.beginPath();
        ctx.moveTo(-w / 2 + (w / 4) * i, -h / 2 + 3);
        ctx.lineTo(-w / 2 + (w / 4) * i, h / 2 - 3);
        ctx.stroke();
      }
    } else if (this.type === 'P') {
      // Prism: rotating refraction sparkle
      const t = time * 1.6 + this.phase;
      ctx.strokeStyle = 'rgba(255,255,255,0.9)';
      ctx.lineWidth = 2.2;
      for (let k = 0; k < 3; k++) {
        const a = t + (k * TAU) / 3;
        ctx.beginPath();
        ctx.moveTo(Math.cos(a) * 4, Math.sin(a) * 4);
        ctx.lineTo(Math.cos(a) * (w * 0.36), Math.sin(a) * (h * 0.36));
        ctx.stroke();
      }
    }

    // Idle shimmer sweep
    const sh = (Math.sin(time * 1.7 + this.phase) + 1) / 2;
    if (sh > 0.86) {
      const sx = -w / 2 + w * ((time * 0.6 + this.phase) % 1);
      ctx.fillStyle = 'rgba(255,255,255,0.22)';
      ctx.beginPath();
      ctx.moveTo(sx - 6, -h / 2); ctx.lineTo(sx, -h / 2);
      ctx.lineTo(sx + 6, h / 2); ctx.lineTo(sx, h / 2);
      ctx.closePath(); ctx.fill();
    }

    ctx.restore();
  }

  _roundRect(ctx, x, y, w, h, r) {
    r = Math.min(r, w / 2, h / 2);
    ctx.beginPath();
    ctx.moveTo(x + r, y);
    ctx.arcTo(x + w, y, x + w, y + h, r);
    ctx.arcTo(x + w, y + h, x, y + h, r);
    ctx.arcTo(x, y + h, x, y, r);
    ctx.arcTo(x, y, x + w, y, r);
    ctx.closePath();
  }
}

/** The whole field: owns bricks, spatial lookup, and per-frame updates. */
class BrickField {
  constructor() { this.bricks = []; this.grid = []; this.total = 0; this.breakable = 0; }

  get remaining() { return this.breakable; }

  build(levelDef) {
    this.bricks.length = 0;
    this.grid = [];
    let breakable = 0;
    const rows = levelDef.rows;
    const offsetX = (CONFIG.W - CONFIG.COLS * CONFIG.CELL_W) / 2;
    for (let r = 0; r < rows.length && r < CONFIG.ROWS; r++) {
      this.grid[r] = [];
      const rowStr = rows[r];
      for (let c = 0; c < CONFIG.COLS; c++) {
        const ch = c < rowStr.length ? rowStr[c] : '.';
        if (ch === '.' || ch === ' ') { this.grid[r][c] = null; continue; }
        const type = BRICK_TYPES[ch] ? ch : 'S';
        const b = new Brick(type, c, r,
          offsetX + c * CONFIG.CELL_W + CONFIG.BRICK_PAD,
          CONFIG.FIELD_TOP + r * CONFIG.CELL_H + CONFIG.BRICK_PAD);
        this.bricks.push(b);
        this.grid[r][c] = b;
        if (type !== 'M') breakable++;
      }
    }
    this.breakable = breakable;
    this.total = breakable;
  }

  update(dt) {
    let healed = null;
    for (const b of this.bricks) {
      if (b.update(dt) && b.type === 'R') healed = b;
    }
    return healed;
  }

  /** Bricks whose bounding box may overlap the circle — small field, cheap scan. */
  near(cx, cy, r) {
    const out = [];
    for (const b of this.bricks) {
      if (!b.alive) continue;
      if (cx + r < b.x || cx - r > b.x + b.w || cy + r < b.y || cy - r > b.y + b.h) continue;
      out.push(b);
    }
    return out;
  }

  draw(ctx, time) {
    for (const b of this.bricks) b.draw(ctx, time);
  }
}

window.BrickField = BrickField;
window.Brick = Brick;
window.BRICK_TYPES = BRICK_TYPES;
window.ROW_COLORS = ROW_COLORS;
