// Pure particle system: spawn helpers + update. No DOM, no canvas.
// Particle types: 'spark' (glowy dot), 'chunk' (brick shrapnel),
// 'ring' (shockwave), 'glow' (soft light blob).

export function createParticles() {
  return { list: [] };
}

export function spawn(sys, p) {
  if (sys.list.length > 900) sys.list.shift();
  sys.list.push({
    type: 'spark',
    x: 0, y: 0, vx: 0, vy: 0,
    life: 0.5, max: 0.5,
    size: 3, hue: 190, sat: 100, lum: 60,
    grav: 0, drag: 0,
    ...p,
  });
  const part = sys.list[sys.list.length - 1];
  part.max = part.life;
}

export function sparkBurst(sys, x, y, opts = {}) {
  const n = opts.count ?? 14;
  for (let i = 0; i < n; i++) {
    const a = Math.random() * Math.PI * 2;
    const sp = (opts.speed ?? 180) * (0.35 + Math.random() * 0.9);
    spawn(sys, {
      type: 'spark',
      x, y,
      vx: Math.cos(a) * sp + (opts.vx ?? 0),
      vy: Math.sin(a) * sp + (opts.vy ?? 0),
      life: (opts.life ?? 0.55) * (0.6 + Math.random() * 0.7),
      size: 1.5 + Math.random() * 2.5,
      hue: opts.hue ?? 190,
      grav: opts.grav ?? 260,
      drag: 2.2,
    });
  }
}

export function chunkBurst(sys, x, y, opts = {}) {
  const n = opts.count ?? 10;
  for (let i = 0; i < n; i++) {
    const a = Math.random() * Math.PI * 2;
    const sp = (opts.speed ?? 150) * (0.3 + Math.random());
    spawn(sys, {
      type: 'chunk',
      x, y,
      vx: Math.cos(a) * sp,
      vy: Math.sin(a) * sp - 40,
      life: (opts.life ?? 0.8) * (0.7 + Math.random() * 0.6),
      size: 2 + Math.random() * 4,
      hue: opts.hue ?? 190,
      grav: 420,
      drag: 0.6,
      spin: Math.random() * 8 - 4,
    });
  }
}

export function ring(sys, x, y, opts = {}) {
  spawn(sys, {
    type: 'ring',
    x, y,
    life: opts.life ?? 0.45,
    size: opts.size ?? 8,      // start radius
    grow: opts.grow ?? 340,    // px/sec
    hue: opts.hue ?? 190,
    width: opts.width ?? 3,
  });
}

export function glow(sys, x, y, opts = {}) {
  spawn(sys, {
    type: 'glow',
    x, y,
    life: opts.life ?? 0.4,
    size: opts.size ?? 40,
    grow: opts.grow ?? 0,
    hue: opts.hue ?? 190,
  });
}

export function update(sys, dt) {
  const list = sys.list;
  for (let i = list.length - 1; i >= 0; i--) {
    const p = list[i];
    p.life -= dt;
    if (p.life <= 0) { list.splice(i, 1); continue; }
    if (p.type === 'ring' || p.type === 'glow') {
      p.size += (p.grow ?? 0) * dt;
      continue;
    }
    p.x += p.vx * dt;
    p.y += p.vy * dt;
    if (p.grav) p.vy += p.grav * dt;
    if (p.drag) {
      const f = Math.max(0, 1 - p.drag * dt);
      p.vx *= f; p.vy *= f;
    }
  }
}
