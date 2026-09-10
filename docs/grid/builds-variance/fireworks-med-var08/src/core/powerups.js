// Power-up capsules dropped by bricks. Pure definitions + effect helpers.

export const POWER = {
  MULTI: 'multi',     // ×2 balls (split)
  FIRE: 'fire',       // fireball pierces standard bricks
  HEAVY: 'heavy',     // heavy ball: slow but breaks speed bricks
  WIDE: 'wide',       // wider paddle (timed)
  SLOW: 'slow',       // slow-motion (timed)
  MAGNET: 'magnet',   // catch & aim (timed)
  LIFE: 'life',       // extra life
};

export const POWER_META = {
  [POWER.MULTI]:  { label: 'MULTI',  hue: 320, weight: 16, good: true },
  [POWER.FIRE]:   { label: 'FIRE',   hue: 20,  weight: 13, good: true },
  [POWER.HEAVY]:  { label: 'HEAVY',  hue: 30,  weight: 10, good: true },
  [POWER.WIDE]:   { label: 'WIDE',   hue: 140, weight: 15, good: true },
  [POWER.SLOW]:   { label: 'SLOW',   hue: 200, weight: 12, good: true },
  [POWER.MAGNET]: { label: 'MAGNET', hue: 265, weight: 12, good: true },
  [POWER.LIFE]:   { label: 'LIFE',   hue: 0,   weight: 6,  good: true },
};

// Choose a power-up kind by weighted random.
export function rollPower(rand = Math.random) {
  const entries = Object.entries(POWER_META);
  const total = entries.reduce((s, [, m]) => s + m.weight, 0);
  let r = rand() * total;
  for (const [kind, m] of entries) {
    r -= m.weight;
    if (r <= 0) return kind;
  }
  return POWER.MULTI;
}

export function makeCapsule(x, y, kind, vy = 150) {
  return { x, y, vy, kind, w: 34, h: 18, alive: true, t: Math.random() * 6 };
}

export function updateCapsules(caps, dt, field) {
  for (const c of caps) {
    c.y += c.vy * dt;
    c.t += dt;
    if (c.y > field.height + 30) c.alive = false;
  }
}

export function capsuleActive(c) { return c.alive; }

export const TIMERS = {
  [POWER.FIRE]: 9,
  [POWER.HEAVY]: 9,
  [POWER.WIDE]: 14,
  [POWER.SLOW]: 7,
  [POWER.MAGNET]: 10,
};
