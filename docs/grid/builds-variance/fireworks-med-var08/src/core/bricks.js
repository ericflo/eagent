// Bricks: creation, rules (angle/speed/phase/moving/exploding), no DOM.

export const BRICK_W = 64;
export const BRICK_H = 26;

// Special brick kinds
export const KIND = {
  STANDARD: 'standard',
  ANGLE: 'angle',
  SPEED: 'speed',
  PHASE: 'phase',
  MOVING: 'moving',
  EXPLODING: 'exploding',
};

export const KIND_HUE = {
  standard: 200,
  angle: 45,
  speed: 350,
  phase: 280,
  moving: 160,
  exploding: 15,
};

// ANGLE bricks: the ball must hit within ±TOLERANCE of the required
// incidence angle. Required angle is derived from brick identity so tests
// can be exact; render derives the same arc.
export const ANGLE_TOLERANCE = Math.PI / 9; // ±20°
export function angleBrickTarget(i) {
  // Deterministic spread of required incidence angles, 30°..150° from +x axis.
  // Golden-ratio stride so consecutive bricks get well-separated, varied
  // targets (a plain modular stride clusters after a few bricks).
  return Math.PI / 6 + (i * Math.PI * 0.618) % (Math.PI * 2 / 3);
}

// SPEED bricks: break only above this speed (world units / sec).
export const SPEED_THRESHOLD = 460;

// PHASE bricks: cycle solid ↔ phased. t is the brick's phase clock (sec).
export const PHASE_PERIOD = 3.2;
export function phaseSolid(t) {
  const p = (t % PHASE_PERIOD) / PHASE_PERIOD; // 0..1
  // Solid 70% of the cycle, intangible 30%.
  return p < 0.7;
}

// MOVING bricks slide horizontally around their home slot.
export const MOVE_RANGE = 26;   // px each side
export const MOVE_SPEED = 1.6;  // rad/sec

export function makeBricks(level, layout) {
  // layout: { cols, top, left, gapX, gapY, width, height }
  const bricks = [];
  level.rows.forEach((row, r) => {
    for (let c = 0; c < row.length; c++) {
      const ch = row[c];
      if (ch === '.' || ch === ' ') continue;
      const kind = charKind(ch);
      const i = bricks.length;
      bricks.push({
        id: i,
        kind,
        col: c,
        row: r,
        homeX: layout.left + c * (layout.width + layout.gapX),
        x: layout.left + c * (layout.width + layout.gapX),
        y: layout.top + r * (layout.height + layout.gapY),
        w: layout.width,
        h: layout.height,
        alive: true,
        phaseT: Math.random() * PHASE_PERIOD,
        movePhase: (c % 2 === 0 ? 0 : Math.PI) + r * 0.35,
        targetAngle: angleBrickTarget(i),
        hue: KIND_HUE[kind] ?? 200,
      });
    }
  });
  return bricks;
}

export function charKind(ch) {
  switch (ch) {
    case 'A': return KIND.ANGLE;
    case 'V': return KIND.SPEED;
    case 'P': return KIND.PHASE;
    case 'M': return KIND.MOVING;
    case 'X': return KIND.EXPLODING;
    default: return KIND.STANDARD;
  }
}

export function updateBricks(bricks, dt) {
  for (const b of bricks) {
    if (!b.alive) continue;
    b.phaseT += dt;
    if (b.kind === KIND.MOVING) {
      b.x = b.homeX + Math.sin(b.phaseT * MOVE_SPEED + b.movePhase) * MOVE_RANGE;
    }
  }
}

export function brickSolid(b) {
  if (!b.alive) return false;
  if (b.kind === KIND.PHASE && !phaseSolid(b.phaseT)) return false;
  return true;
}

// Can this hit actually destroy the brick?
export function hitBreaksBrick(b, ball) {
  if (!brickSolid(b)) return { breaks: false, reason: 'phased' };

  if (b.kind === KIND.SPEED) {
    const sp = Math.hypot(ball.vx, ball.vy);
    if (sp < SPEED_THRESHOLD) return { breaks: false, reason: 'too-slow', speed: sp };
    return { breaks: true };
  }

  if (b.kind === KIND.ANGLE) {
    // Incidence angle of the ball's velocity relative to +x axis, folded to
    // [0, π) so hitting the top or bottom edge counts the same.
    let a = Math.atan2(ball.vy, ball.vx);
    a = ((a % Math.PI) + Math.PI) % Math.PI;
    let diff = Math.abs(a - b.targetAngle);
    diff = Math.min(diff, Math.PI - diff);
    if (diff > ANGLE_TOLERANCE) return { breaks: false, reason: 'wrong-angle', angle: a };
    return { breaks: true };
  }

  return { breaks: true };
}

// Score weight per kind (base points before multiplier).
export function baseScore(kind) {
  switch (kind) {
    case KIND.ANGLE: return 150;
    case KIND.SPEED: return 130;
    case KIND.PHASE: return 120;
    case KIND.MOVING: return 120;
    case KIND.EXPLODING: return 100;
    default: return 50;
  }
}

export const ALIVE = (b) => b.alive;
