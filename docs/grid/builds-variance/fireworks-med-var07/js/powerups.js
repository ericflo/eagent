'use strict';
/* ============================================================
   Attic Breakout — powerups.js
   Falling capsules. ~12% drop chance per destroyed brick (Prisms
   always drop). Two cursed capsules with a spiky dark look.
   ============================================================ */

const PU = {
  MULTIBALL: 'multiball',
  FIRE: 'fire',
  HEAVY: 'heavy',
  GHOST: 'ghost',
  WIDE: 'wide',
  STICKY: 'sticky',
  SHIELD: 'shield',
  SLOWMO: 'slowmo',
  BOOST: 'boost',
  SHRINK: 'shrink',   // cursed
  FAST: 'fast',       // cursed
};

const PU_DEFS = {
  [PU.MULTIBALL]: { label: 'MULTIBALL', color: [255, 220, 90], glyph: '⁙', good: true },
  [PU.FIRE]:      { label: 'FIREBALL', color: [255, 110, 50], glyph: '🔥', glyphAscii: 'F', good: true },
  [PU.HEAVY]:     { label: 'HEAVY BALL', color: [160, 160, 180], glyph: '●', good: true },
  [PU.GHOST]:     { label: 'GHOST BALL', color: [170, 230, 255], glyph: '◐', good: true },
  [PU.WIDE]:      { label: 'WIDE PADDLE', color: [110, 235, 150], glyph: '↔', good: true },
  [PU.STICKY]:    { label: 'STICKY', color: [255, 180, 220], glyph: '◎', good: true },
  [PU.SHIELD]:    { label: 'SHIELD', color: [90, 200, 255], glyph: '▬', good: true },
  [PU.SLOWMO]:    { label: 'SLOW-MO', color: [150, 160, 255], glyph: '◷', good: true },
  [PU.BOOST]:     { label: '2× SCORE', color: [255, 235, 130], glyph: '×2', good: true },
  [PU.SHRINK]:    { label: 'SHRINK!', color: [120, 60, 120], glyph: '↕', good: false },
  [PU.FAST]:      { label: 'SPEED UP!', color: [90, 40, 60], glyph: '»', good: false },
};

const PU_DURATION = {
  [PU.FIRE]: 8, [PU.HEAVY]: 10, [PU.GHOST]: 6, [PU.WIDE]: 12,
  [PU.STICKY]: 10, [PU.SLOWMO]: 4, [PU.BOOST]: 10, [PU.SHRINK]: 10, [PU.FAST]: 8,
};

class Capsule {
  constructor(type, x, y) {
    this.type = type;
    this.x = x; this.y = y;
    this.vy = 130;
    this.vx = rand(-30, 30);
    this.r = 15;
    this.dead = false;
    this.t = rand(TAU);
  }
  update(dt) {
    this.t += dt;
    this.y += this.vy * dt;
    this.x += this.vx * dt;
    this.vx *= 1 - 0.4 * dt;
    if (this.y > CONFIG.H + 30) this.dead = true;
  }
  draw(ctx) {
    const def = PU_DEFS[this.type];
    const [r, g, b] = def.color;
    const pulse = 1 + Math.sin(this.t * 6) * 0.07;
    ctx.save();
    ctx.translate(this.x, this.y);
    // Glow
    ctx.shadowColor = `rgb(${r},${g},${b})`;
    ctx.shadowBlur = 14;
    if (def.good) {
      // Rounded capsule
      ctx.fillStyle = `rgba(${r},${g},${b},0.95)`;
      ctx.beginPath();
      const w = 30 * pulse, h = 22 * pulse;
      if (ctx.roundRect) { ctx.roundRect(-w / 2, -h / 2, w, h, 10); }
      else { ctx.rect(-w / 2, -h / 2, w, h); }
      ctx.fill();
      ctx.shadowBlur = 0;
      ctx.fillStyle = 'rgba(0,0,0,0.8)';
      ctx.font = `bold ${def.glyph.length > 1 ? 11 : 13}px ${UI.FONT}`;
      ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
      ctx.fillText(def.glyph, 0, 1);
    } else {
      // Cursed: dark spiky star
      ctx.rotate(this.t * 1.5);
      ctx.fillStyle = `rgb(${r},${g},${b})`;
      ctx.strokeStyle = `rgba(${r + 90},${g + 70},${b + 70},0.9)`;
      ctx.lineWidth = 2;
      ctx.beginPath();
      for (let i = 0; i < 10; i++) {
        const rad = i % 2 === 0 ? 17 : 8;
        const a = (i / 10) * TAU;
        const px = Math.cos(a) * rad, py = Math.sin(a) * rad;
        i === 0 ? ctx.moveTo(px, py) : ctx.lineTo(px, py);
      }
      ctx.closePath();
      ctx.fill(); ctx.stroke();
      ctx.shadowBlur = 0;
      ctx.rotate(-this.t * 1.5);
      ctx.fillStyle = 'rgba(255,120,120,0.95)';
      ctx.font = `bold 12px ${UI.FONT}`;
      ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
      ctx.fillText(def.glyph, 0, 1);
    }
    ctx.restore();
  }
}

window.PU = PU;
window.PU_DEFS = PU_DEFS;
window.PU_DURATION = PU_DURATION;
window.Capsule = Capsule;
