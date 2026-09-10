// powerups.js — falling drops, paddle pickup, and global effect timers with
// HUD countdown data. Drop chance ~18% base; prisms always drop.
export const DROP_SPEED = 170;
export const BASE_DROP_CHANCE = 0.18;

export const DROPS = {
  multi:    { label: 'MULTI',  color: '#22d3ee', glyph: '⦿', dur: 0 },
  fire:     { label: 'FIRE',   color: '#fb923c', glyph: '🔥', dur: 8 },
  pierce:   { label: 'PIERCE', color: '#a78bfa', glyph: '➤', dur: 8 },
  giant:    { label: 'GIANT',  color: '#ec4899', glyph: '◍', dur: 10 },
  sticky:   { label: 'STICKY', color: '#34d399', glyph: '⚡', dur: 12 },
  slow:     { label: 'SLOW',   color: '#4ade80', glyph: '◐', dur: 5 },
  life:     { label: 'LIFE',   color: '#f87171', glyph: '♥', dur: 0 },
  frenzy:   { label: 'FRENZY', color: '#fbbf24', glyph: '▲', dur: 0 },
};

export class Powerups {
  constructor() {
    this.drops = [];
  }

  maybeDrop(x, y, brickType, rng = Math.random) {
    if (brickType === 'prism') {
      this.drops.push({ x, y, kind: this._pick(rng, true) });
      return;
    }
    if (rng() < BASE_DROP_CHANCE) {
      this.drops.push({ x, y, kind: this._pick(rng, false) });
    }
  }

  _pick(rng, prism) {
    if (prism) {
      const pool = ['multi', 'fire', 'pierce', 'giant', 'sticky', 'slow', 'frenzy', 'life'];
      return pool[Math.floor(rng() * pool.length)];
    }
    if (rng() < 0.06) return 'life'; // life stays rare
    const pool = ['multi', 'fire', 'pierce', 'giant', 'sticky', 'slow', 'frenzy'];
    return pool[Math.floor(rng() * pool.length)];
  }

  // onPickup(d) returns true if the paddle caught the drop
  step(onPickup) {
    for (const d of this.drops) d.y += DROP_SPEED / 120;
    this.drops = this.drops.filter(d => d.y <= 980 && !onPickup(d));
  }

  render(ctx) {
    for (const d of this.drops) {
      const spec = DROPS[d.kind];
      ctx.save();
      ctx.shadowColor = spec.color;
      ctx.shadowBlur = 10;
      ctx.fillStyle = spec.color;
      ctx.beginPath();
      ctx.roundRect(d.x - 16, d.y - 12, 32, 24, 7);
      ctx.fill();
      ctx.shadowBlur = 0;
      ctx.fillStyle = 'rgba(15,23,42,0.9)';
      ctx.font = 'bold 14px system-ui, sans-serif';
      ctx.textAlign = 'center';
      ctx.textBaseline = 'middle';
      ctx.fillText(spec.glyph, d.x, d.y + 1);
      ctx.restore();
    }
  }

  // HUD countdown data: active timed effects with seconds remaining
  hudEffects(balls) {
    const out = [];
    const add = (name, remaining) => {
      if (remaining > 0) out.push({ name, glyph: DROPS[name].glyph, remaining, total: DROPS[name].dur });
    };
    add('fire', balls.fireTimer);
    add('pierce', balls.pierceTimer);
    add('giant', balls.giantTimer);
    add('slow', balls.slowTimer);
    add('sticky', balls.stickyTimer);
    return out;
  }
}
