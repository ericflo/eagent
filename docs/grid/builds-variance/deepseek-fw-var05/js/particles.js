// particles.js — lightweight particle pool, floating texts, screen shake.

// Particle kinds: spark, ring, text, shard.
const Particles = (() => {
  const list = [];
  const MAX = 420;

  function spawn(o) {
    if (list.length > MAX) list.shift();
    list.push({
      kind: o.kind || 'spark',
      x: o.x, y: o.y,
      vx: o.vx || 0, vy: o.vy || 0,
      life: o.life ?? 1,
      maxLife: o.life ?? 1,
      size: o.size || 3,
      color: o.color || '#ffffff',
      text: o.text || '',
      grav: o.grav ?? 0,
      drag: o.drag ?? 0,
      rot: o.rot || 0,
      vrot: o.vrot || 0,
    });
  }

  function update(dt) {
    for (let i = list.length - 1; i >= 0; i--) {
      const p = list[i];
      p.life -= dt;
      if (p.life <= 0) { list.splice(i, 1); continue; }
      p.x += p.vx * dt;
      p.y += p.vy * dt;
      p.vy += (p.grav || 0) * dt;
      if (p.drag) { p.vx *= 1 - p.drag * dt; p.vy *= 1 - p.drag * dt; }
      p.rot += (p.vrot || 0) * dt;
      // remove offscreen text/rings
      if (p.kind === 'text' && (p.y < -20 || p.y > 9999)) { list.splice(i, 1); continue; }
    }
  }

  function draw(ctx, cam) {
    for (const p of list) {
      const a = clamp(p.life / p.maxLife, 0, 1);
      const x = p.x, y = p.y;
      if (p.kind === 'spark') {
        ctx.globalAlpha = a;
        ctx.fillStyle = p.color;
        ctx.beginPath();
        ctx.arc(x, y, p.size * (0.5 + a * 0.5), 0, TAU);
        ctx.fill();
      } else if (p.kind === 'ring') {
        ctx.globalAlpha = a * 0.8;
        ctx.strokeStyle = p.color;
        ctx.lineWidth = 2.5;
        ctx.beginPath();
        ctx.arc(x, y, p.size * (1.6 - a), 0, TAU);
        ctx.stroke();
      } else if (p.kind === 'text') {
        ctx.globalAlpha = a;
        ctx.font = `bold ${p.size}px "Segoe UI", system-ui, sans-serif`;
        ctx.textAlign = 'center';
        ctx.fillStyle = p.color;
        ctx.strokeStyle = 'rgba(0,0,0,0.55)';
        ctx.lineWidth = 3;
        ctx.strokeText(p.text, x, y);
        ctx.fillText(p.text, x, y);
      } else if (p.kind === 'shard') {
        ctx.globalAlpha = a;
        ctx.save();
        ctx.translate(x, y);
        ctx.rotate(p.rot);
        ctx.fillStyle = p.color;
        ctx.fillRect(-p.size / 2, -p.size / 2, p.size, p.size);
        ctx.restore();
      }
    }
    ctx.globalAlpha = 1;
  }

  function clear() { list.length = 0; }

  return { spawn, update, draw, clear, get count() { return list.length; } };
})();

window.Particles = Particles;
