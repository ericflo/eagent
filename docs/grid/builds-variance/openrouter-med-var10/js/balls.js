/* BREAKTHROUGH — js/balls.js
   Ball entity + ball types + physics vs paddle/walls/bricks live in game.js;
   this file owns the Ball class, type table, trails and drawing.
   Ball types (visually distinct, each with unique particles):
     normal  — white/cyan comet
     fire    — burns through armored/angle/speed bricks & ignores them, ember trail
     heavy   — smashes speed gates & armored, dense dark-gold ball, shockwave on hit
     splitter — splits on every brick hit (cap), icy blue shards
     gold    — "bouncy": every brick it breaks is worth x3 and spawns coin sparks
*/
window.Balls = (function () {
  const U = window.U;

  const BTYPES = {
    normal:   { r: 9,  hue: 190, name: '',            speedMul: 1 },
    fire:     { r: 10, hue: 20,  name: 'FIRE',        speedMul: 1.05 },
    heavy:    { r: 12, hue: 45,  name: 'HEAVY',       speedMul: 1 },
    splitter: { r: 8,  hue: 185, name: 'SPLITTER',    speedMul: 1 },
    gold:     { r: 9,  hue: 55,  name: 'BOUNCE ×3',   speedMul: 1 }
  };

  class Ball {
    constructor(x, y, vx, vy, type = 'normal') {
      this.reset(x, y, vx, vy, type);
      this.trail = [];             // recent positions {x,y}
      this.stuck = false;          // caught by sticky paddle
      this.stuckDx = 0;
      this.splitDepth = 0;
      this.lostFx = false;
    }
    reset(x, y, vx, vy, type = 'normal') {
      this.type = type;
      const t = BTYPES[type];
      this.x = x; this.y = y;
      this.vx = vx; this.vy = vy;
      this.r = t.r;
      this.speedBoost = 1;         // grows with paddle vertical momentum
      this.dead = false;
      this.flash = 0;
    }
    get speed() { return Math.hypot(this.vx, this.vy); }
    get tdef() { return BTYPES[this.type]; }

    setSpeed(s) {
      const cur = Math.hypot(this.vx, this.vy) || 1;
      this.vx = this.vx / cur * s; this.vy = this.vy / cur * s;
    }
    scaleSpeed(k) { this.setSpeed(this.speed * k); }

    emitTrail(dt) {
      this.trail.push({ x: this.x, y: this.y });
      const maxLen = 10 + Math.min(14, (this.speed / 60) | 0);
      if (this.trail.length > maxLen) this.trail.shift();
      // type-specific continuous particles
      const p = this.type;
      if (p === 'fire' && Math.random() < 0.6) {
        Particles.spawn({ x: this.x + U.rand(-4, 4), y: this.y + U.rand(-4, 4),
          vx: U.rand(-40, 40), vy: U.rand(-90, -30), size: U.rand(3, 7), life: U.rand(0.25, 0.55),
          hue: U.rand(15, 45), shape: 'glow', grav: -60, drag: 0.92 });
      } else if (p === 'splitter' && Math.random() < 0.4) {
        Particles.spawn({ x: this.x, y: this.y, vx: U.rand(-60, 60), vy: U.rand(-60, 60),
          size: 3, life: 0.35, hue: 185, shape: 'spark', grav: 0, glow: 1 });
      } else if (p === 'gold' && Math.random() < 0.35) {
        Particles.spawn({ x: this.x, y: this.y, vx: U.rand(-50, 50), vy: U.rand(-50, 50),
          size: 4, life: 0.5, hue: U.rand(45, 60), shape: 'rect', spin: 6, grav: 60, glow: 0.4 });
      } else if (p === 'heavy' && Math.random() < 0.25) {
        Particles.spawn({ x: this.x, y: this.y, vx: U.rand(-20, 20), vy: U.rand(-20, 20),
          size: 5, life: 0.4, hue: 40, sat: 40, lit: 50, shape: 'glow', grav: 0 });
      }
    }

    drawTrail(ctx, frenzy) {
      // smooth fading comet tail: single tapered polygon + gradient glow over trail points
      const t = this.tdef;
      const n = this.trail.length;
      if (n < 2) return;
      ctx.globalCompositeOperation = 'lighter';
      const hue = frenzy ? (t.hue + (frenzy || 0) * 80) % 360 : t.hue;
      // left/right offsets along the tail for a tapering comet shape
      const left = [], right = [];
      for (let i = 0; i < n; i++) {
        const pt = this.trail[i];
        const k = i / (n - 1);                  // 0 old .. 1 new
        const rad = t.r * (0.05 + 0.95 * k * k);
        // direction from previous point
        const prev = this.trail[Math.max(0, i - 1)];
        const next = this.trail[Math.min(n - 1, i + 1)];
        let dx = next.x - prev.x, dy = next.y - prev.y;
        const len = Math.hypot(dx, dy) || 1;
        dx /= len; dy /= len;
        left.push({ x: pt.x - dy * rad, y: pt.y + dx * rad });
        right.push({ x: pt.x + dy * rad, y: pt.y - dx * rad });
      }
      const grad = ctx.createLinearGradient(this.trail[0].x, this.trail[0].y, this.x, this.y);
      grad.addColorStop(0, U.hsl(hue, 95, 65, 0));
      grad.addColorStop(0.7, U.hsl(hue, 95, 65, 0.22));
      grad.addColorStop(1, U.hsl(hue, 95, 70, 0.45));
      ctx.fillStyle = grad;
      ctx.beginPath();
      ctx.moveTo(left[0].x, left[0].y);
      for (let i = 1; i < n; i++) ctx.lineTo(left[i].x, left[i].y);
      for (let i = n - 1; i >= 0; i--) ctx.lineTo(right[i].x, right[i].y);
      ctx.closePath();
      ctx.fill();
      ctx.globalCompositeOperation = 'source-over';
    }

    draw(ctx, time, speedGate) {
      const t = this.tdef;
      const fast = speedGate && this.speed >= speedGate;
      ctx.save();
      // aura
      const auraR = t.r + 6 + Math.sin(time * 8) * 1.5;
      const g = ctx.createRadialGradient(this.x, this.y, t.r * 0.4, this.x, this.y, auraR + 6);
      g.addColorStop(0, U.hsl(t.hue, 100, 75, 0.9));
      g.addColorStop(1, U.hsl(t.hue, 100, 60, 0));
      ctx.fillStyle = g;
      ctx.beginPath(); ctx.arc(this.x, this.y, auraR + 6, 0, U.TAU); ctx.fill();
      // core
      ctx.fillStyle = fast ? '#ffffff' : U.hsl(t.hue, 90, 72);
      ctx.beginPath(); ctx.arc(this.x, this.y, t.r, 0, U.TAU); ctx.fill();
      // type-specific shells
      if (this.type === 'heavy') {
        ctx.strokeStyle = '#2a2118'; ctx.lineWidth = 3;
        ctx.beginPath(); ctx.arc(this.x, this.y, t.r - 1, 0, U.TAU); ctx.stroke();
      } else if (this.type === 'fire') {
        ctx.fillStyle = U.hsl(U.rand(10, 40), 100, 60, 0.8);
        ctx.beginPath(); ctx.arc(this.x, this.y - 2, t.r * 0.55, 0, U.TAU); ctx.fill();
      } else if (this.type === 'splitter') {
        ctx.strokeStyle = '#dffaff'; ctx.lineWidth = 2;
        for (let i = 0; i < 3; i++) {
          const a = time * 4 + i * (U.TAU / 3);
          ctx.beginPath();
          ctx.moveTo(this.x + Math.cos(a) * t.r, this.y + Math.sin(a) * t.r);
          ctx.lineTo(this.x + Math.cos(a) * (t.r + 5), this.y + Math.sin(a) * (t.r + 5));
          ctx.stroke();
        }
      } else if (this.type === 'gold') {
        ctx.strokeStyle = '#fff3b0'; ctx.lineWidth = 2;
        ctx.beginPath(); ctx.arc(this.x, this.y, t.r + 3 + Math.sin(time * 9) * 1.5, 0, U.TAU); ctx.stroke();
      }
      if (fast) { // speed-gate hint: white ring when fast enough to break gated bricks
        ctx.strokeStyle = 'rgba(255,255,255,.85)'; ctx.lineWidth = 2;
        ctx.beginPath(); ctx.arc(this.x, this.y, t.r + 7, 0, U.TAU); ctx.stroke();
      }
      ctx.restore();
    }
  }

  return { Ball, BTYPES };
})();