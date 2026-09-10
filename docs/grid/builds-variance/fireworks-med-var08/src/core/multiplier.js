// Score multiplier / combo / "chaos" math. Pure — no DOM.

export const MULT_CAP = 50;
export const COMBO_WINDOW = 1.6; // seconds between breaks to keep a combo alive

export function createMultiplier() {
  return {
    value: 1,      // the multiplier itself (x1 .. x50)
    combo: 0,      // bricks broken within the combo window
    comboT: 0,     // time left in the current combo window
    chaosHeat: 0,  // 0..1 — how "awesome" the moment is (drives visuals/audio)
  };
}

// Called whenever a brick breaks.
// chaosActive: any ball is currently above the brick field.
export function registerBreak(m, chaosActive) {
  m.combo += 1;
  m.comboT = COMBO_WINDOW;
  const comboBonus = Math.min(m.combo, 10) * 0.12;
  m.value += (chaosActive ? 0.9 : 0.3) + comboBonus;
  m.value = Math.min(m.value, MULT_CAP);
  bumpHeat(m, chaosActive ? 0.25 : 0.1);
}

// Per-tick update. decayRate lets slow-motion etc. influence pacing.
export function update(m, dt, chaosActive) {
  // Ramp while the ball lives above the bricks — the core reward loop.
  if (chaosActive) {
    m.value += dt * 1.1;
    m.value = Math.min(m.value, MULT_CAP);
    bumpHeat(m, dt * 0.55);
  } else {
    m.value -= dt * (0.35 + m.value * 0.012);
    m.value = Math.max(1, m.value);
    bumpHeat(m, -dt * 0.45);
  }

  if (m.comboT > 0) {
    m.comboT -= dt;
    if (m.comboT <= 0) m.combo = 0;
  }
  m.chaosHeat = Math.max(0, Math.min(1, m.chaosHeat));
}

function bumpHeat(m, amount) {
  m.chaosHeat = Math.max(0, Math.min(1, m.chaosHeat + amount));
}

// Score for a brick, already multiplied.
export function scoreFor(m, base) {
  return Math.round(base * m.value);
}
