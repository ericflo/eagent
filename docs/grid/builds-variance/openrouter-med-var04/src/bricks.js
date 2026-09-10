// bricks.js — brick behaviors: facet angle gating, impact speed gating,
// slider rails, bulwark shield side, volatile chains, steel, ghost phasing,
// prism, multiplier. No multi-hit health bricks.
import { CELL_W, CELL_H, BRICK_VALUES } from './levels.js';
import { WORLD_W } from './engine.js';

export const IMPACT_SPEED = 620;      // px/s, logical scale
export const FACET_TOLERANCE = 25 * Math.PI / 180;

export class BrickField {
  constructor(bricks, topY) {
    this.bricks = bricks;
    this.topY = topY;
    this.frenzyLine = Math.max(40, topY - 26);
    this.ghostClock = 0;
    this.sliderDirs = new Map();
  }

  aliveBricks() { return this.bricks.filter(b => b.alive); }
  breakableLeft() { return this.bricks.filter(b => b.alive && !b.steel).length; }

  // Ghost phase state: solid = true on a per-brick timer cycle (4.8s cycle,
  // 3.0s solid / 1.8s intangible, with shimmer telegraph at the transitions).
  ghostSolid(b, tSec) {
    const phase = (tSec + b.ghostOffset) % 4.8;
    return phase < 3.0;
  }
  ghostShimmer(b, tSec) {
    const phase = (tSec + b.ghostOffset) % 4.8;
    return phase > 2.6 || phase < 0.4; // telegraph around transitions
  }

  stepSliders(dt) {
    for (const b of this.aliveBricks()) {
      if (b.type !== 'slider' || !b.rail) continue;
      b.x += b.rail.dir * b.rail.speed * dt;
      if (b.x < b.rail.minX) { b.x = b.rail.minX; b.rail.dir = 1; }
      if (b.x > b.rail.maxX) { b.x = b.rail.maxX; b.rail.dir = -1; }
    }
  }

  // Test one ball against all bricks. Returns list of events for main to act on.
  collide(ball, tSec) {
    const events = [];
    for (const b of this.bricks) {
      if (!b.alive) continue;
      if (b.type === 'ghost' && !this.ghostSolid(b, tSec)) continue;

      // circle-rect overlap test
      const cx = Math.max(b.x, Math.min(ball.x, b.x + b.w));
      const cy = Math.max(b.y, Math.min(ball.y, b.y + b.h));
      const dx = ball.x - cx, dy = ball.y - cy;
      if (dx * dx + dy * dy > ball.r * ball.r) continue;

      const ev = this._hit(b, ball, events);
      if (ev) events.push(ev);
      // fireball passes through broken bricks: if fire melted this one, don't
      // let the ball stop on it — continue to the next brick.
      if (ball.fireActive && ev && ev.kind === 'break' && !ev.brick.steel) {
        // keep flying: restore pre-bounce position is unnecessary since fire
        // bricks break without bouncing (default case), so just continue.
      }
    }
    return events.filter(Boolean);
  }

  _hit(b, ball, priorEvents) {
    const speed = Math.hypot(ball.vx, ball.vy);

    switch (b.type) {
      case 'steel': {
        this._bounce(b, ball);
        b.flash = 0.25;
        return { kind: 'steel', brick: b };
      }
      case 'facet': {
        const fire = ball.fireActive;
        const incoming = Math.atan2(-ball.vy, -ball.vx); // direction of travel reversed = incoming bearing
        let diff = Math.abs(incoming - b.facetAngle);
        if (diff > Math.PI) diff = Math.abs(diff - 2 * Math.PI);
        if (diff <= FACET_TOLERANCE || fire) {
          b.alive = false;
          return { kind: 'break', brick: b, value: BRICK_VALUES.facet, special: fire ? 'fire' : undefined };
        }
        this._bounce(b, ball);
        b.hintFlash = 0.4;
        return { kind: 'glance', brick: b };
      }
      case 'impact': {
        if (speed >= IMPACT_SPEED || ball.fireActive) {
          b.alive = false;
          return { kind: 'break', brick: b, value: BRICK_VALUES.impact };
        }
        this._bounce(b, ball);
        b.hintFlash = 0.4;
        return { kind: 'too-slow', brick: b };
      }
      case 'bulwark': {
        // Shield covers one vertical side; hits on that side deflect — unless
        // the fireball is active, which melts it from either side (per SPEC).
        const hitShield =
          (b.shieldSide === 'right' && ball.x > b.x + b.w / 2) ||
          (b.shieldSide === 'left' && ball.x <= b.x + b.w / 2);
        if (hitShield && !ball.fireActive) {
          this._bounce(b, ball);
          b.hintFlash = 0.4;
          return { kind: 'shield', brick: b };
        }
        b.alive = false;
        return { kind: 'break', brick: b, value: BRICK_VALUES.bulwark };
      }
      default:
        b.alive = false;
        return { kind: 'break', brick: b, value: BRICK_VALUES[b.type] || 50, special: b.type };
    }
  }

  // Standard axis-of-least-penetration reflection.
  _bounce(b, ball) {
    const overlapL = ball.x + ball.r - b.x;
    const overlapR = b.x + b.w - (ball.x - ball.r);
    const overlapT = ball.y + ball.r - b.y;
    const overlapB = b.y + b.h - (ball.y - ball.r);
    const m = Math.min(overlapL, overlapR, overlapT, overlapB);
    if (m === overlapT) { ball.y = b.y - ball.r; ball.vy = -Math.abs(ball.vy); }
    else if (m === overlapB) { ball.y = b.y + b.h + ball.r; ball.vy = Math.abs(ball.vy); }
    else if (m === overlapL) { ball.x = b.x - ball.r; ball.vx = -Math.abs(ball.vx); }
    else { ball.x = b.x + b.w + ball.r; ball.vx = Math.abs(ball.vx); }
  }

  // Volatile explosion: destroys breakable bricks within radius, chains.
  explode(origin, field, radius = 2.2 * CELL_W) {
    const broken = [];
    for (const nb of this.bricks) {
      if (!nb.alive || nb.steel) continue;
      if (nb === origin) continue;
      const nx = nb.x + nb.w / 2, ny = nb.y + nb.h / 2;
      const ox = origin.x + origin.w / 2, oy = origin.y + origin.h / 2;
      if (Math.hypot(nx - ox, ny - oy) <= radius) {
        nb.alive = false;
        broken.push(nb);
      }
    }
    return broken;
  }

  render(ctx, tSec) {
    for (const b of this.bricks) {
      if (!b.alive) continue;
      const ghost = b.type === 'ghost';
      let solidPhase = true;
      if (ghost) solidPhase = this.ghostSolid(b, tSec);
      if (ghost && !solidPhase) {
        if (!this.ghostShimmer(b, tSec)) continue;
      }
      const flash = b.flash > 0 || b.hintFlash > 0;
      if (b.flash > 0) b.flash -= 1 / 120;
      if (b.hintFlash > 0) b.hintFlash -= 1 / 120;
      this.renderBrick(ctx, b, tSec, ghost, solidPhase, flash);
      ctx.globalAlpha = 1;
    }
    // slider rails
    for (const b of this.bricks) {
      if (b.type === 'slider' && b.rail) {
        ctx.strokeStyle = 'rgba(251,191,36,0.35)';
        ctx.setLineDash([3, 5]);
        ctx.beginPath();
        ctx.moveTo(b.rail.minX, b.y + b.h / 2);
        ctx.lineTo(b.rail.maxX + b.w, b.y + b.h / 2);
        ctx.stroke();
        ctx.setLineDash([]);
      }
    }
  }

  renderBrick(ctx, b, tSec, ghost, solidPhase, flash) {
    const x = b.x, y = b.y, w = b.w, h = b.h;
    const cx = x + w / 2, cy = y + h / 2;
    const r = 4;
    ctx.save();
    if (ghost) {
      if (!solidPhase) {
        // intangible: ghostly low alpha + dashed outline
        ctx.globalAlpha = 0.22;
      } else if (this.ghostShimmer(b, tSec)) {
        ctx.globalAlpha = 0.8 + 0.2 * Math.sin(tSec * 14);
      }
    }

    const baseFill = {
      steel: '#9aa8b8', facet: '#48d8c4', impact: flash ? '#fca5a5' : '#f87171',
      slider: '#fbbf24', bulwark: '#b8bec8', volatile: '#fb7185',
      ghost: '#c4b5fd', prism: '#fcd34d', multiplier: '#4f8ef7',
    }[b.type] || '#38bdf8';

    // base body with vertical gradient
    const g = ctx.createLinearGradient(x, y, x, y + h);
    g.addColorStop(0, shade(baseFill, 26));
    g.addColorStop(0.5, baseFill);
    g.addColorStop(1, shade(baseFill, -30));
    ctx.fillStyle = g;
    ctx.beginPath();
    ctx.roundRect(x, y, w, h, r);
    ctx.fill();

    // per-type decoration
    if (b.type === 'steel') {
      // brushed-metal diagonal hatch + corner rivets + bevel
      ctx.save();
      ctx.beginPath(); ctx.roundRect(x, y, w, h, r); ctx.clip();
      ctx.strokeStyle = 'rgba(255,255,255,0.16)';
      ctx.lineWidth = 1.5;
      for (let i = -h; i < w + h; i += 6) {
        ctx.beginPath();
        ctx.moveTo(x + i, y + h + 1);
        ctx.lineTo(x + i + h, y - 1);
        ctx.stroke();
      }
      // darker top/bottom bevel + center highlight
      ctx.strokeStyle = 'rgba(15,23,42,0.45)';
      ctx.lineWidth = 2;
      ctx.beginPath(); ctx.moveTo(x + 2, y + h - 1); ctx.lineTo(x + w - 2, y + h - 1); ctx.stroke();
      ctx.beginPath(); ctx.moveTo(x + 2, y + 1); ctx.lineTo(x + w - 2, y + 1); ctx.stroke();
      ctx.fillStyle = 'rgba(255,255,255,0.30)';
      ctx.fillRect(x + 6, y + h / 2 - 1.5, w - 12, 3);
      ctx.restore();
      // rivets at corners
      for (const [rx, ry] of [[x + 7, y + 7], [x + w - 7, y + 7], [x + 7, y + h - 7], [x + w - 7, y + h - 7]]) {
        ctx.fillStyle = '#5b6774';
        ctx.beginPath(); ctx.arc(rx, ry, 2.4, 0, Math.PI * 2); ctx.fill();
        ctx.fillStyle = 'rgba(255,255,255,0.7)';
        ctx.beginPath(); ctx.arc(rx - 0.7, ry - 0.7, 1.0, 0, Math.PI * 2); ctx.fill();
      }
      ctx.strokeStyle = 'rgba(15,23,42,0.6)';
      ctx.lineWidth = 1.5;
      ctx.beginPath(); ctx.roundRect(x, y, w, h, r); ctx.stroke();
    } else if (b.type === 'bulwark') {
      // shield side: thick glowing cyan-steel plate strip along that edge
      const sw = 7;
      const px = b.shieldSide === 'right' ? x + w - sw : x;
      ctx.save();
      ctx.shadowColor = '#22d3ee';
      ctx.shadowBlur = 10;
      const pg = ctx.createLinearGradient(x, y, x + w, y);
      const bright = '#a5f3fc';
      pg.addColorStop(0, b.shieldSide === 'left' ? bright : '#0e7490');
      pg.addColorStop(0.5, '#67e8f9');
      pg.addColorStop(1, b.shieldSide === 'right' ? bright : '#0e7490');
      ctx.fillStyle = pg;
      ctx.fillRect(px, y + 1, sw, h - 2);
      ctx.restore();
      ctx.fillStyle = 'rgba(255,255,255,0.9)';
      ctx.fillRect(px + (b.shieldSide === 'right' ? 1 : sw - 2), y + 2, 1.5, h - 4);
      // exposed side looks bare: darker, slightly chipped inner shading
      const exL = b.shieldSide === 'right';
      const eg = ctx.createLinearGradient(x, y, x + w, y);
      eg.addColorStop(0, exL ? 'rgba(0,0,0,0.28)' : 'rgba(0,0,0,0)');
      eg.addColorStop(1, exL ? 'rgba(0,0,0,0)' : 'rgba(0,0,0,0.28)');
      ctx.fillStyle = eg;
      ctx.beginPath(); ctx.roundRect(x, y, w, h, r); ctx.fill();
      ctx.strokeStyle = 'rgba(15,23,42,0.5)';
      ctx.lineWidth = 1;
      ctx.beginPath(); ctx.roundRect(x, y, w, h, r); ctx.stroke();
    } else if (b.type === 'facet') {
      // crystalline two-tone glass with diamond facet highlight
      ctx.save();
      ctx.beginPath(); ctx.roundRect(x, y, w, h, r); ctx.clip();
      // gem-like gradient: bright top-left to deep teal bottom-right
      const fg = ctx.createLinearGradient(x, y, x + w, y + h);
      fg.addColorStop(0, '#ccfbf1');
      fg.addColorStop(0.45, '#5eead4');
      fg.addColorStop(1, '#0d9488');
      ctx.fillStyle = fg;
      ctx.fillRect(x, y, w, h);
      // diamond facet highlight (top half, bright)
      ctx.fillStyle = 'rgba(255,255,255,0.55)';
      ctx.beginPath();
      ctx.moveTo(cx, y + 3);
      ctx.lineTo(x + w - 5, cy);
      ctx.lineTo(cx, y + h - 3);
      ctx.lineTo(x + 5, cy);
      ctx.closePath();
      ctx.fill();
      ctx.strokeStyle = 'rgba(240,253,250,0.9)';
      ctx.lineWidth = 1;
      ctx.stroke();
      ctx.restore();
      // chevron glyph showing break-allowed normal
      const dir = b.facetAngle < 0 ? -1 : 1;
      ctx.fillStyle = '#0f172a';
      ctx.beginPath();
      ctx.moveTo(cx - 5 * dir, cy - 5);
      ctx.lineTo(cx + 4 * dir, cy);
      ctx.lineTo(cx - 5 * dir, cy + 5);
      ctx.lineTo(cx - 5 * dir, cy + 2);
      ctx.lineTo(cx - 1 * dir, cy);
      ctx.lineTo(cx - 5 * dir, cy - 2);
      ctx.closePath();
      ctx.fill();
      ctx.strokeStyle = 'rgba(15,23,42,0.55)';
      ctx.lineWidth = 1.5;
      ctx.beginPath(); ctx.roundRect(x, y, w, h, r); ctx.stroke();
    } else if (b.type === 'impact') {
      // pulsing speed stripes (subtle) hinting "needs speed"
      const pulse = 0.35 + 0.35 * Math.sin(tSec * 6);
      ctx.save();
      ctx.beginPath(); ctx.roundRect(x, y, w, h, r); ctx.clip();
      ctx.strokeStyle = `rgba(255,255,255,${pulse})`;
      ctx.lineWidth = 3;
      for (let i = -h; i < w + h; i += 10) {
        ctx.beginPath();
        ctx.moveTo(x + i, y + h + 1);
        ctx.lineTo(x + i + h, y - 1);
        ctx.stroke();
      }
      ctx.restore();
      ctx.strokeStyle = 'rgba(15,23,42,0.5)';
      ctx.lineWidth = 1;
      ctx.beginPath(); ctx.roundRect(x, y, w, h, r); ctx.stroke();
    } else if (b.type === 'volatile') {
      // cracks + pulsing spark core so it reads as dangerous
      const pulse = 0.5 + 0.5 * Math.sin(tSec * 5 + b.ghostOffset);
      ctx.save();
      ctx.beginPath(); ctx.roundRect(x, y, w, h, r); ctx.clip();
      ctx.strokeStyle = 'rgba(69,10,25,0.85)';
      ctx.lineWidth = 1.4;
      // crack lines from center
      for (const [dx, dy] of [[-1, -0.5], [0.6, -1], [1, 0.5], [-0.4, 1]]) {
        ctx.beginPath();
        ctx.moveTo(cx, cy);
        ctx.lineTo(cx + dx * w / 2.6, cy + dy * h / 2.6);
        ctx.lineTo(cx + dx * w / 2.6 + (Math.random() < 0.5 ? -3 : 3) * 0 + dx * 3, cy + dy * h / 2.6 - 2);
        ctx.stroke();
      }
      // glowing core
      ctx.shadowColor = '#fda4af';
      ctx.shadowBlur = 8 + 6 * pulse;
      ctx.fillStyle = `rgba(253,164,175,${0.5 + 0.4 * pulse})`;
      ctx.beginPath(); ctx.arc(cx, cy, 4 + 2 * pulse, 0, Math.PI * 2); ctx.fill();
      ctx.restore();
      ctx.strokeStyle = 'rgba(15,23,42,0.5)';
      ctx.lineWidth = 1;
      ctx.beginPath(); ctx.roundRect(x, y, w, h, r); ctx.stroke();
    } else if (b.type === 'ghost') {
      // soft shimmering body; brighter, more clearly solid in the solid phase
      ctx.fillStyle = `rgba(237,233,254,${solidPhase ? 0.35 + 0.1 * Math.sin(tSec * 3 + b.ghostOffset) : 0.12})`;
      ctx.fillRect(x + 3, y + 3, w - 6, h - 6);
      ctx.strokeStyle = solidPhase ? 'rgba(196,181,253,0.95)' : 'rgba(196,181,253,0.55)';
      ctx.lineWidth = 1.5;
      if (!solidPhase) ctx.setLineDash([5, 4]);
      ctx.beginPath(); ctx.roundRect(x + 1.5, y + 1.5, w - 3, h - 3, r); ctx.stroke();
      ctx.setLineDash([]);
    } else if (b.type === 'prism') {
      // legendary golden: warm glow + bright diamond
      ctx.save();
      ctx.shadowColor = '#fbbf24';
      ctx.shadowBlur = 10;
      ctx.fillStyle = 'rgba(255,251,235,0.9)';
      ctx.beginPath();
      ctx.moveTo(cx, y + 4); ctx.lineTo(x + w / 2 + 8, cy);
      ctx.lineTo(cx, y + h - 4); ctx.lineTo(cx - w / 2 + 8, cy);
      ctx.closePath(); ctx.fill();
      ctx.restore();
      ctx.fillStyle = 'rgba(120,80,0,0.7)';
      ctx.beginPath();
      ctx.moveTo(cx, y + 7); ctx.lineTo(x + w / 2 + 4, cy); ctx.lineTo(cx, y + h - 7); ctx.lineTo(cx - w / 2 + 4, cy);
      ctx.closePath(); ctx.fill();
      ctx.strokeStyle = 'rgba(146,64,14,0.8)';
      ctx.lineWidth = 1.5;
      ctx.beginPath(); ctx.roundRect(x, y, w, h, r); ctx.stroke();
    } else if (b.type === 'multiplier') {
      // sapphire: cool glow + star sparkles
      ctx.save();
      ctx.shadowColor = '#3b82f6';
      ctx.shadowBlur = 10;
      ctx.fillStyle = 'rgba(191,219,254,0.95)';
      ctx.beginPath();
      for (let k = 0; k < 4; k++) {
        const a = Math.PI / 2 * k + tSec * 1.2;
        ctx.moveTo(cx, cy);
        ctx.lineTo(cx + Math.cos(a) * 6, cy + Math.sin(a) * 6);
        ctx.lineTo(cx + Math.cos(a + 0.6) * 2, cy + Math.sin(a + 0.6) * 2);
      }
      ctx.closePath(); ctx.fill();
      ctx.restore();
      ctx.strokeStyle = 'rgba(30,64,175,0.8)';
      ctx.lineWidth = 1.5;
      ctx.beginPath(); ctx.roundRect(x, y, w, h, r); ctx.stroke();
    } else {
      // standard: keep glyph-less flat look, simple inner highlight
      ctx.fillStyle = 'rgba(255,255,255,0.18)';
      ctx.fillRect(x + 2, y + 2, w - 4, (h - 4) * 0.35);
      ctx.strokeStyle = 'rgba(15,23,42,0.35)';
      ctx.lineWidth = 1;
      ctx.beginPath(); ctx.roundRect(x, y, w, h, r); ctx.stroke();
    }
    ctx.restore();
  }
}

function shade(hex, amt) {
  const n = parseInt(hex.slice(1), 16);
  const r = Math.max(0, Math.min(255, (n >> 16) + amt));
  const g = Math.max(0, Math.min(255, ((n >> 8) & 0xff) + amt));
  const b = Math.max(0, Math.min(255, (n & 0xff) + amt));
  return `rgb(${r},${g},${b})`;
}
