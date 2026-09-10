'use strict';
// ---------------------------------------------------------------------------
// bricks.js — brick entity + level builder. Each brick type has distinct
// behavior and rendering. All types are destructible except `unlock` (locks
// vanish only via the key brick).
// ---------------------------------------------------------------------------

// Grid geometry (logical units)
const BRICK_COLS = 13;
const BRICK_W = 34;
const BRICK_H = 16;
const BRICK_GAP = 2;
const GRID_TOP = 70;

// Steep-angle threshold for steel (deg from horizontal)
const STEEL_ANGLE_DEG = 50;
// Speed threshold for velocity bricks
const VELOCITY_SPEED = 520;

const BRICK_STYLE = {
  basic:    { color: '#4fd8ff', points: 50 },
  steel:    { color: '#aebfd8', points: 120 },
  velocity: { color: '#ff9a3c', points: 150 },
  regen:    { color: '#c66bff', points: 100 },
  mover:    { color: '#2fe6c8', points: 110 },
  redirect: { color: '#ffd23c', points: 90 },
  prism:    { color: '#ff7ad9', points: 80 },
  key:      { color: '#ffcf4d', points: 200 },
  switch:   { color: '#5dff6a', points: 60 },
  death:    { color: '#ff3355', points: 0 },
  reset:    { color: '#8a93a5', points: 10 },
  unlock:   { color: '#565f70', points: 0 },
};

class Brick {
  constructor(type, col, row, opts = {}) {
    const st = BRICK_STYLE[type] || BRICK_STYLE.basic;
    this.type = type;
    this.row = row;
    this.col = col;
    this.w = BRICK_W;
    this.h = BRICK_H;
    this.x = opts.x !== undefined ? opts.x : 4 + col * (BRICK_W + BRICK_GAP);
    this.y = opts.y !== undefined ? opts.y : GRID_TOP + row * (BRICK_H + BRICK_GAP);
    this.baseX = this.x;           // for movers
    this.alive = true;
    this.color = opts.color || st.color;
    this.points = st.points;
    this.drop = opts.drop || null; // powerup code for basic bricks
    this.flash = 0;                // hit flash 0..1
    this.phase = Math.random() * TAU;        // mover / regen phase
    this.regenSolid = true;                  // regen state
    this.togglesLeft = type === 'switch' ? 1 : 0;
    this.hintCd = 0;               // "TOO SLOW" throttle
  }

  get cx() { return this.x + this.w / 2; }
  get cy() { return this.y + this.h / 2; }

  // Destructible bricks count toward level clear; locks don't (vanish via
  // key) and hidden barriers don't (vanish via switch).
  get clearable() { return this.type !== 'unlock' && this.type !== 'hidden'; }
  // Can the ball damage it right now?
  damageable(ball) {
    switch (this.type) {
      case 'unlock':
      case 'hidden': return false;
      case 'regen': return this.regenSolid;
      default: return true;
    }
  }

  update(dt, time) {
    if (this.flash > 0) this.flash = Math.max(0, this.flash - dt * 5);
    if (this.hintCd > 0) this.hintCd -= dt;
    if (this.type === 'mover') {
      this.phase += dt * 1.1;
      this.x = this.baseX + Math.sin(this.phase) * 40;
    } else if (this.type === 'regen') {
      const p = (time % 1.6) / 1.6 + this.phase / TAU;
      this.regenSolid = (p % 1) < 0.55; // ~55% duty solid
    }
  }

  draw(ctx, time, palette) {
    if (!this.alive) return;
    const { x, y, w, h } = this;
    ctx.save();
    let alpha = 1;
    if (this.type === 'regen' && !this.regenSolid) alpha = 0.18;
    if (this.type === 'hidden') alpha = 0.28;
    ctx.globalAlpha = alpha;

    // color: hue ramp per row within the level palette
    let base = this.color;
    if (this.type === 'basic' && palette) {
      const hue = lerp(palette[0], palette[1], clamp(this.row / 6, 0, 1));
      base = `hsl(${hue}, 85%, 62%)`;
    }
    const r = 4;

    // rounded-rect base with glow (steel gets its own high-contrast gradient)
    if (this.type === 'steel') {
      const sg = ctx.createLinearGradient(x, y, x, y + h);
      sg.addColorStop(0, '#cfd8ea');
      sg.addColorStop(1, '#8fa3c8');
      ctx.shadowBlur = 10 + this.flash * 18;
      ctx.shadowColor = this.flash > 0 ? '#ffffff' : base;
      ctx.fillStyle = this.flash > 0 ? '#ffffff' : sg;
      roundRect(ctx, x, y, w, h, r);
      ctx.fill();
      // strong white edge highlight on top edge
      ctx.shadowBlur = 0;
      ctx.strokeStyle = 'rgba(255,255,255,0.85)';
      ctx.lineWidth = 1.5;
      ctx.beginPath();
      ctx.moveTo(x + 3, y + 1);
      ctx.lineTo(x + w - 3, y + 1);
      ctx.stroke();
    } else {
      ctx.shadowBlur = 10 + this.flash * 18;
      ctx.shadowColor = base;
      ctx.fillStyle = this.flash > 0 ? '#ffffff' : base;
      roundRect(ctx, x, y, w, h, r);
      ctx.fill();
      ctx.shadowBlur = 0;

      // top sheen
      ctx.globalAlpha = alpha * 0.25;
      ctx.fillStyle = '#ffffff';
      roundRect(ctx, x + 2, y + 2, w - 4, h * 0.35, 2);
      ctx.fill();
      ctx.globalAlpha = alpha;
    }

    switch (this.type) {
      case 'steel': {
        // metallic bands
        ctx.fillStyle = 'rgba(255,255,255,0.5)';
        ctx.fillRect(x + 3, y + h * 0.45, w - 6, 1.5);
        ctx.fillStyle = 'rgba(20,40,70,0.5)';
        ctx.fillRect(x + 3, y + h - 5, w - 6, 2);
        if (this.arcT && this.arcT > 0) {
          // brief arc indicator showing required steep angle
          this.arcT -= 1 / 60;
          ctx.strokeStyle = 'rgba(140,255,255,0.95)';
          ctx.lineWidth = 2;
          ctx.shadowBlur = 6; ctx.shadowColor = '#5df0ff';
          ctx.beginPath();
          ctx.arc(this.cx, this.cy, w * 0.62, -Math.PI * 0.75, -Math.PI * 0.25);
          ctx.stroke();
          ctx.shadowBlur = 0;
        }
        break;
      }
      case 'velocity': {
        // 3 chevrons '>>>' (brighter = steeper angle needed)
        ctx.lineWidth = 2;
        ctx.lineCap = 'round';
        ctx.lineJoin = 'round';
        for (let i = 0; i < 3; i++) {
          const cx0 = x + 7 + i * 7.5;
          const g = ctx.createLinearGradient(cx0 - 3, y, cx0 + 3, y + h);
          g.addColorStop(0, '#ffe9a8');
          g.addColorStop(1, '#ff8a2a');
          ctx.strokeStyle = g;
          ctx.beginPath();
          ctx.moveTo(cx0 - 2.5, y + 3.5);
          ctx.lineTo(cx0 + 2.5, y + h / 2);
          ctx.lineTo(cx0 - 2.5, y + h - 3.5);
          ctx.stroke();
        }
        ctx.lineCap = 'butt';
        break;
      }
      case 'regen': {
        ctx.strokeStyle = 'rgba(255,255,255,0.6)';
        ctx.lineWidth = 1;
        ctx.strokeRect(x + 2.5, y + 2.5, w - 5, h - 5);
        break;
      }
      case 'redirect': {
        ctx.strokeStyle = 'rgba(40,30,0,0.8)';
        ctx.lineWidth = 2;
        ctx.beginPath();
        ctx.moveTo(x + 8, y + h - 5); ctx.lineTo(this.cx, y + 4); ctx.lineTo(x + w - 8, y + h - 5);
        ctx.stroke();
        break;
      }
      case 'prism': {
        // iridescent shimmer
        const t = time * 1.4 + this.phase;
        for (let i = 0; i < 3; i++) {
          ctx.globalAlpha = alpha * 0.35;
          ctx.fillStyle = `hsl(${(t * 90 + i * 120) % 360},95%,65%)`;
          roundRect(ctx, x + 2 + i * 3, y + 2, w - 4 - i * 6, h - 4, 3);
          ctx.fill();
        }
        break;
      }
      case 'key': {
        ctx.fillStyle = '#3a2a00';
        ctx.beginPath(); ctx.arc(this.cx, y + 6, 3, 0, TAU); ctx.fill();
        ctx.fillRect(this.cx - 1, y + 7, 2, 6);
        break;
      }
      case 'unlock': {
        // padlock glyph
        ctx.strokeStyle = '#c9d4e8';
        ctx.lineWidth = 1.6;
        ctx.beginPath(); ctx.arc(this.cx, y + 6.5, 3.4, Math.PI, 0); ctx.stroke();
        ctx.fillStyle = '#c9d4e8';
        ctx.fillRect(this.cx - 4.5, y + 6.5, 9, 6);
        break;
      }
      case 'switch': {
        ctx.strokeStyle = '#0a3a10';
        ctx.lineWidth = 2;
        ctx.beginPath();
        ctx.moveTo(x + 7, y + h - 4);
        ctx.lineTo(this.cx + 4, y + 4);
        ctx.stroke();
        break;
      }
      case 'death': {
        ctx.strokeStyle = 'rgba(40,0,10,0.9)';
        ctx.lineWidth = 2.4;
        ctx.beginPath();
        ctx.moveTo(x + 9, y + 4); ctx.lineTo(x + w - 9, y + h - 4);
        ctx.moveTo(x + w - 9, y + 4); ctx.lineTo(x + 9, y + h - 4);
        ctx.stroke();
        break;
      }
      case 'reset': {
        ctx.fillStyle = 'rgba(20,25,35,0.85)';
        ctx.font = 'bold 12px monospace';
        ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
        ctx.fillText('Ø', this.cx, this.cy + 1);
        break;
      }
      case 'hidden': {
        ctx.strokeStyle = 'rgba(120,255,180,0.7)';
        ctx.lineWidth = 1;
        ctx.strokeRect(x + 1.5, y + 1.5, w - 3, h - 3);
        break;
      }
      case 'mover': {
        ctx.fillStyle = 'rgba(0,40,35,0.55)';
        ctx.fillRect(x + 4, y + 6, w - 8, 2);
        ctx.fillRect(x + 4, y + 10, w - 8, 2);
        break;
      }
    }
    ctx.restore();
  }
}

// rounded-rect path helper
function roundRect(ctx, x, y, w, h, r) {
  r = Math.min(r, w / 2, h / 2);
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + w, y, x + w, y + h, r);
  ctx.arcTo(x + w, y + h, x, y + h, r);
  ctx.arcTo(x, y + h, x, y, r);
  ctx.arcTo(x, y, x + w, y, r);
  ctx.closePath();
}

// ---------------------------------------------------------------------------
// BrickField — live brick set for the current level
// ---------------------------------------------------------------------------
const BrickField = {
  bricks: [],
  palette: null,
  time: 0,

  build(levelIndex, speedTier) {
    const level = LEVELS[levelIndex % LEVELS.length];
    this.palette = level.palette;
    const grid = parseLevelRows(level.rows);
    this.bricks.length = 0;
    // deterministic per-level phase scatter
    const rng = mulberry32(1000 + levelIndex);
    for (let r = 0; r < grid.length; r++) {
      for (let c = 0; c < grid[r].length; c++) {
        const cell = grid[r][c];
        if (!cell) continue;
        const b = new Brick(cell.type, c, r, { drop: cell.drop });
        b.phase = rng() * TAU;
        this.bricks.push(b);
      }
    }
    this.time = 0;
  },

  update(dt) {
    this.time += dt;
    for (const b of this.bricks) if (b.alive) b.update(dt, this.time);
  },

  draw(ctx) {
    for (const b of this.bricks) if (b.alive) b.draw(ctx, this.time, this.palette);
  },

  // lowest y of any live brick (for the overdrive zone)
  topY() {
    let y = Infinity;
    for (const b of this.bricks) {
      if (!b.alive) continue;
      // hidden barriers don't count toward the "safe" top zone
      if (b.type === 'hidden') continue;
      if (b.y < y) y = b.y;
    }
    return y === Infinity ? GRID_TOP : y;
  },

  clearRemaining() {
    let n = 0;
    for (const b of this.bricks) if (b.alive && b.clearable) n++;
    return n;
  },

  reset() { this.bricks.length = 0; },
};
