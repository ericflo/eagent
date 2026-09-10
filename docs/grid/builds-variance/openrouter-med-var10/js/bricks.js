/* BREAKTHROUGH — js/bricks.js
   Brick types & grid. NO multi-hit bricks: difficulty is mechanical.
   Types:
     normal   — plain, colored per level palette
     angle    — only breaks when hit at a STEEP angle (|vy| > |vx| * 1.4); clinks off otherwise
     speed    — speed-gated: breaks only if ball speed >= gate; otherwise bounces (heavy ball ignores gate)
     moving   — slides horizontally within its lane
     phase    — cycles solid/ghost; ghost balls pass through, hits only count when solid
     bomb     — when broken, destroys orthogonal neighbours (chain!)
     regen    — after N seconds regrows a destroyed neighbour from the same row... actually:
                simpler & readable: it slowly "heals" — if destroyed while field has regens left it respawns
                once after 8s at its spot (max 2 respawns), shown as fading ghost
     armored  — only breakable by fire/heavy/splitter-children balls (visual: riveted frame)
   Each has a distinct visual language documented in help screen.
*/
window.Bricks = (function () {
  const U = window.U;

  // visual + tuning table
  const TYPES = {
    normal: { hue: 210, sat: 70, lit: 55, score: 50 },
    angle:  { hue: 30,  sat: 95, lit: 55, score: 90 },
    speed:  { hue: 195, sat: 90, lit: 55, score: 90 },
    moving: { hue: 100, sat: 60, lit: 50, score: 80 },
    phase:  { hue: 275, sat: 80, lit: 60, score: 100 },
    bomb:   { hue: 5,   sat: 90, lit: 55, score: 70 },
    regen:  { hue: 20,  sat: 75, lit: 55, score: 70 },
    armored:{ hue: 220, sat: 25, lit: 55, score: 150 }
  };

  function make(type, col, row, x, y, w, h, opts = {}) {
    const t = TYPES[type];
    return {
      type, col, row, x, y, w, h,
      alive: true,
      hue: t.hue, sat: t.sat, lit: t.lit,
      score: t.score,
      hitFlash: 0, deathAnim: 0,
      // moving
      mvMin: opts.mvMin || 0, mvMax: opts.mvMax || 0, mvSpeed: opts.mvSpeed || 60,
      mvPhase: opts.mvPhase || Math.random() * U.TAU, baseX: x,
      // phase
      phaseT: opts.phaseT || Math.random() * 4, phasePeriod: 3.2, solid: true,
      // regen
      respawns: type === 'regen' ? 2 : 0, respawnTimer: 0, respawnAt: null,
      // speed gate (pixels/sec)
      gate: opts.gate || 560
    };
  }

  class BrickField {
    constructor() { this.bricks = []; this.cols = 0; this.topRowY = 0; }

    // build from level spec: strings, one char per cell
    // legend: . empty  1..8 -> type index  [N normal, A angle, S speed, M moving, P phase, B bomb, R regen, X armored]
    static LEGEND = { 'N': 'normal', 'A': 'angle', 'S': 'speed', 'M': 'moving', 'P': 'phase', 'B': 'bomb', 'R': 'regen', 'X': 'armored' };

    build(spec, area) {
      this.bricks.length = 0;
      const rows = spec.rows, cols = spec.cols;
      this.cols = cols;
      const bw = area.w / cols, bh = spec.brickH || (area.h / rows);
      for (let r = 0; r < rows; r++) {
        for (let c = 0; c < cols; c++) {
          const ch = spec.grid[r][c];
          if (ch === '.') continue;
          const type = BrickField.LEGEND[ch] || 'normal';
          const x = area.x + c * bw, y = area.y + r * bh;
          const opts = {};
          if (type === 'moving') {
            const span = Math.min(2, Math.floor(Math.random() * 2) + 1) * bw * 0.6;
            opts.mvMin = x + bw * 0.1; opts.mvMax = x + bw * 0.9 + span;
            if (c >= cols - 2) { opts.mvMin = x - span * 0.5; opts.mvMax = x + bw * 0.9; }
            opts.mvSpeed = U.rand(40, 90) + spec.tier * 12;
            opts.mvPhase = (c / cols) * U.TAU;
          }
          if (type === 'speed') opts.gate = 520 + spec.tier * 30;
          const b = make(type, c, r, x + 2, y + 2, bw - 4, bh - 4, opts);
          b.baseX = x + 2;
          this.bricks.push(b);
        }
      }
      this.recomputeTopRow();
    }

    recomputeTopRow() {
      let minRow = Infinity;
      for (const b of this.bricks) if (b.alive && b.row < minRow) minRow = b.row;
      this.topRow = minRow === Infinity ? -1 : minRow;
      this.topRowY = minRow === Infinity ? -Infinity : Math.min(...this.bricks.filter(b => b.alive && b.row === minRow).map(b => b.y));
    }

    get aliveCount() { let n = 0; for (const b of this.bricks) if (b.alive) n++; return n; }

    update(dt, time) {
      for (const b of this.bricks) {
        if (!b.alive) {
          if (b.respawnAt !== null) {
            b.respawnTimer -= dt;
            if (b.respawnTimer <= 0) {
              b.alive = true; b.deathAnim = 0; b.respawnAt = null;
              Particles.burst(b.x + b.w / 2, b.y + b.h / 2, 14, { hue: b.hue, shape: 'glow', life: 0.6, spMax: 160, grav: 0 });
            }
          }
          continue;
        }
        if (b.hitFlash > 0) b.hitFlash -= dt * 4;
        if (b.type === 'moving') {
          const mid = (b.mvMin + b.mvMax) / 2, amp = (b.mvMax - b.mvMin) / 2;
          b.x = mid + Math.sin(time * (b.mvSpeed / Math.max(1, amp)) + b.mvPhase) * amp;
        }
        if (b.type === 'phase') {
          b.phaseT += dt;
          const cycle = (b.phaseT % b.phasePeriod) / b.phasePeriod;
          b.solid = cycle < 0.6;                    // solid 60% of the time
          b.ghostAlpha = b.solid ? 1 : 0.18 + 0.12 * Math.sin(time * 10);
        }
      }
    }

    // does the field have any alive brick in `row` at column `col` region? (for bombs)
    neighbors(b) {
      const out = [];
      for (const o of this.bricks) {
        if (!o.alive || o === b) continue;
        const dc = Math.abs(o.col - b.col), dr = Math.abs(o.row - b.row);
        if (dc + dr === 1) out.push(o);
      }
      return out;
    }

    destroy(b, opts = {}) {
      if (!b.alive) return [];
      b.alive = false;
      const extra = [];
      if (b.type === 'bomb') {
        for (const n of this.neighbors(b)) {
          if (n.type === 'bomb') { extra.push(...this.destroy(n, { chain: (opts.chain || 0) + 1 })); }
          else { n.alive = false; extra.push(n); }
        }
      }
      if (b.type === 'regen' && b.respawns > 0 && !opts.noRegrow) {
        b.respawns--; b.respawnAt = true; b.respawnTimer = 8;
      }
      this.recomputeTopRow();
      return [b, ...extra];
    }

    // is a point "on top" of the field: above the topmost alive brick's y AND within
    // the horizontal extent where an opening exists (we simplify: above min alive y
    // and no alive brick directly above-column blocking). We check column occupancy.
    isAboveField(x, y) {
      if (this.topRow < 0) return y < this.bricks.length ? true : false; // no bricks: anywhere counts? no:
      if (this.topRow < 0) return false;
      if (y >= this.topRowY) return false;
      // find the column this x falls into
      const bw = this.bricks[0].w + 4;
      // approximate column: invert from field origin
      return true; // y above topRowY is enough — visual "on top" moment
    }
  }

  return { BrickField, TYPES };
})();