/* BREAKTHROUGH — js/powerups.js
   Falling drops with icons drawn on canvas + paddle buff state.
   Required set (>=4): MULTI (multiball x3), STICKY (catch & aim), WIDE (wider paddle),
   LASER (shoot), SLOW (slow-mo). Plus ball capsules: FIRE, HEAVY, SPLIT, GOLD.
   HUD chips are DOM-managed by game.js using Powerups.describe.
*/
window.Powerups = (function () {
  const U = window.U;

  // drop definitions: key, label, icon (drawn), hue, weight (spawn chance)
  const DEFS = {
    multi:   { label: 'MULTI',   icon: '✦', hue: 320, w: 3, buff: false },
    sticky:  { label: 'STICKY',  icon: '⌷', hue: 150, w: 2, buff: true,  dur: 12 },
    wide:    { label: 'WIDE',    icon: '↔', hue: 100, w: 3, buff: true,  dur: 14 },
    laser:   { label: 'LASER',   icon: '↑', hue: 350, w: 2, buff: true,  dur: 10 },
    slow:    { label: 'SLOW-MO', icon: '◷', hue: 200, w: 2, buff: true,  dur: 6 },
    fire:    { label: 'FIRE',    icon: '♨', hue: 20,  w: 2, ball: 'fire' },
    heavy:   { label: 'HEAVY',   icon: '◉', hue: 45,  w: 2, ball: 'heavy' },
    split:   { label: 'SPLIT',   icon: '⁂', hue: 185, w: 2, ball: 'splitter' },
    gold:    { label: '×3',      icon: '¤', hue: 55,  w: 2, ball: 'gold' },
    life:    { label: 'LIFE',    icon: '♥', hue: 335, w: 1, buff: false }
  };
  const KEYS = Object.keys(DEFS);

  class Drop {
    constructor(x, y, key) {
      this.key = key;
      this.def = DEFS[key];
      this.x = x; this.y = y;
      this.vy = 130; this.vx = U.rand(-20, 20);
      this.r = 15; this.dead = false;
      this.t = 0;
    }
    update(dt) { this.t += dt; this.x += this.vx * dt; this.y += this.vy * dt; }
    draw(ctx, time) {
      const d = this.def;
      const pulse = 1 + Math.sin(time * 7 + this.t) * 0.08;
      ctx.save();
      ctx.translate(this.x, this.y);
      // glow capsule
      const g = ctx.createRadialGradient(0, 0, 2, 0, 0, this.r + 8);
      g.addColorStop(0, U.hsl(d.hue, 100, 70, 0.85));
      g.addColorStop(1, U.hsl(d.hue, 100, 60, 0));
      ctx.fillStyle = g;
      ctx.beginPath(); ctx.arc(0, 0, this.r + 8, 0, U.TAU); ctx.fill();
      U.rr(ctx, -this.r, -this.r * 0.8, this.r * 2, this.r * 1.6, 8);
      ctx.fillStyle = U.hsl(d.hue, 70, 30, 0.95); ctx.fill();
      ctx.strokeStyle = U.hsl(d.hue, 100, 70); ctx.lineWidth = 2;
      ctx.stroke();
      ctx.fillStyle = '#fff';
      ctx.font = '800 14px ui-rounded, system-ui, sans-serif';
      ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
      ctx.scale(pulse, pulse);
      ctx.fillText(d.icon, 0, 1);
      ctx.restore();
      // sparkle
      if (Math.random() < 0.3) Particles.spawn({ x: this.x + U.rand(-10, 10), y: this.y - 10,
        vx: 0, vy: -40, size: 3, life: 0.4, hue: d.hue, shape: 'spark', grav: 0 });
    }
  }

  function pickKey() {
    let total = 0;
    for (const k of KEYS) total += DEFS[k].w;
    let r = Math.random() * total;
    for (const k of KEYS) { r -= DEFS[k].w; if (r <= 0) return k; }
    return 'multi';
  }

  return { Drop, DEFS, pickKey };
})();