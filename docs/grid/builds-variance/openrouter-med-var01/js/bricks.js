// bricks.js — brick grid, types (STANDARD/WEDGE/KINETIC/PHASE/VOLATILE/DRIFT/GOLD),
// collision resolution computing contact normal, impact angle, impact speed, contact point.
'use strict';

const Bricks = (() => {
  const TYPE_MAP = { S:'STANDARD', W:'WEDGE', K:'KINETIC', P:'PHASE', V:'VOLATILE', D:'DRIFT', G:'GOLD' };
  const grid = [];       // flat list of live bricks
  const dead = [];       // scratch
  const BW = () => (CONFIG.LOGICAL_W - 2 * 10 - (CONFIG.BRICK_COLS - 1) * CONFIG.BRICK_GAP) / CONFIG.BRICK_COLS;
  let ottY = 0;          // bottom of lowest live brick
  let bricksSinceDrop = 0;

  function makeBrick(col, row, type, seedFn) {
    const w = BW();
    const b = {
      col, row, type: TYPE_MAP[type] || type,
      x: 10 + col * (w + CONFIG.BRICK_GAP), y: CONFIG.BRICK_TOP + row * (CONFIG.BRICK_H + CONFIG.BRICK_GAP),
      w, h: CONFIG.BRICK_H,
      phaseT: seedFn ? seedFn() * CONFIG.PHASE_PERIOD : 0,
      solid: true,
      baseX: 10 + col * (w + CONFIG.BRICK_GAP),
      driftDir: seedFn && seedFn() < 0.5 ? -1 : 1,
      assemble: 0, // stagger-in animation 0..1
      hitFlash: 0,
    };
    return b;
  }

  // rows: array of arrays of brick specs, e.g. [['S'],['S','W']]; spec is type letter
  function build(specs) {
    grid.length = 0;
    for (let r = 0; r < specs.length; r++)
      for (let c = 0; c < specs[r].length; c++) {
        const t = specs[r][c];
        if (t && t !== '.') grid.push(makeBrick(c, r, t, Math.random));
      }
    updateOTT();
    bricksSinceDrop = 0;
  }

  function updateOTT() {
    let bottom = CONFIG.BRICK_TOP;
    for (const b of grid) bottom = Math.max(bottom, b.y + b.h);
    ottY = bottom;
  }

  const live = () => grid;

  // Broad/narrow phase vs a ball. Returns list of hit results; resolves bounce unless pierce.
  function collideBall(ball, game) {
    const hits = [];
    for (let i = grid.length - 1; i >= 0; i--) {
      const b = grid[i];
      if (b.type === 'PHASE' && !b.solid) continue; // intangible while ghosted
      // AABB vs circle
      const cx = Util.clamp(ball.x, b.x, b.x + b.w);
      const cy = Util.clamp(ball.y, b.y, b.y + b.h);
      const dx = ball.x - cx, dy = ball.y - cy;
      const d2 = dx * dx + dy * dy;
      if (d2 > ball.r * ball.r) continue;

      // contact normal: from closest point toward ball; if center inside, use min-axis
      let nx, ny;
      if (d2 > 1e-9) { const d = Math.sqrt(d2); nx = dx / d; ny = dy / d; }
      else { // inside: pick smallest penetration axis
        const px = Math.min(ball.x - b.x, b.x + b.w - ball.x);
        const py = Math.min(ball.y - b.y, b.y + b.h - ball.y);
        if (px < py) { nx = (ball.x < b.x + b.w / 2) ? -1 : 1; ny = 0; }
        else { nx = 0; ny = (ball.y < b.y + b.h / 2) ? -1 : 1; }
      }
      // impact angle: angle between incoming velocity and the surface TANGENT
      // (0 = grazing along the surface, PI/2 = steep/head-on)
      const speed = Balls.speedOf(ball);
      const vx = ball.vx, vy = ball.vy;
      const vdotn = vx * nx + vy * ny;
      const cosA = speed > 0 ? Math.abs(vdotn) / speed : 1;
      const impactAngle = Math.asin(Util.clamp(cosA, 0, 1)); // 0 = grazing, PI/2 = head-on
      const contact = { x: Util.clamp(ball.x, b.x, b.x + b.w), y: Util.clamp(ball.y, b.y, b.y + b.h) };

      hits.push({ brick: b, index: i, nx, ny, impactAngle, speed, contact, vdotn });
      if (hits.length >= 3) break; // enough per substep
    }
    return hits;
  }

  // bounce ball off a brick using normal reflection; pushes ball out of penetration
  function reflectBall(ball, nx, ny) {
    const s = Balls.speedOf(ball);
    const dot = ball.vx * nx + ball.vy * ny;
    if (dot < 0) { // moving into surface
      ball.vx -= 2 * dot * nx; ball.vy -= 2 * dot * ny;
      Balls.setSpeed(ball, s);
    }
    // push out
    const cx = Util.clamp(ball.x, 0, CONFIG.LOGICAL_W), cy = ball.y;
    ball.x += nx * (ball.r + 0.5);
    ball.y += ny * (ball.r + 0.5);
  }

  function remove(brick) {
    const i = grid.indexOf(brick);
    if (i >= 0) grid.splice(i, 1);
    updateOTT();
  }

  function count() { return grid.length; }

  // does any ball currently ride above all bricks?
  function ottYValue() { return ottY; }

  function update(dt, game) {
    for (const b of grid) {
      if (b.assemble < 1) b.assemble = Math.min(1, b.assemble + dt * 3);
      if (b.type === 'PHASE') {
        b.phaseT += dt;
        const c = (b.phaseT % CONFIG.PHASE_PERIOD) / CONFIG.PHASE_PERIOD;
        const wasSolid = b.solid;
        b.solid = c < 0.6; // solid 60% of cycle
        if (!wasSolid && b.solid) AudioSys.sfx.phaseTick();
        b.hitFlash = Math.max(0, b.hitFlash - dt * 4);
      } else {
        if (b.hitFlash > 0) b.hitFlash -= dt * 4;
      }
      if (b.type === 'DRIFT') {
        b.x += b.driftDir * CONFIG.DRIFT_SPEED * dt;
        if (b.x > b.baseX + CONFIG.DRIFT_RANGE) { b.x = b.baseX + CONFIG.DRIFT_RANGE; b.driftDir = -1; }
        if (b.x < b.baseX - CONFIG.DRIFT_RANGE) { b.x = b.baseX - CONFIG.DRIFT_RANGE; b.driftDir = 1; }
        b.x = Util.clamp(b.x, 0, CONFIG.LOGICAL_W - b.w);
      }
    }
    updateOTT();
  }

  function noteBroken() { bricksSinceDrop++; }

  function draw(ctx) {
    for (const b of grid) {
      const a = b.assemble;
      const bob = b.type === 'PHASE' && !b.solid;
      ctx.save();
      ctx.globalAlpha = bob ? 0.25 : a;
      ctx.translate(b.x + b.w / 2, b.y + b.h / 2);
      ctx.scale(0.2 + a * 0.8, 0.2 + a * 0.8);
      const x = -b.w / 2, y = -b.h / 2, w = b.w, h = b.h;
      const col = CONFIG.COLORS[b.type.toLowerCase()] || '#888';
      ctx.fillStyle = b.hitFlash > 0 ? '#ffffff' : col;
      if (b.type === 'WEDGE') {
        // hazard-striped angled brick
        ctx.beginPath();
        ctx.moveTo(x, y + h); ctx.lineTo(x + w * 0.18, y); ctx.lineTo(x + w, y); ctx.lineTo(x + w * 0.82, y + h);
        ctx.closePath(); ctx.fill();
        ctx.fillStyle = 'rgba(0,0,0,0.35)';
        for (let sx = x; sx < x + w; sx += 12) {
          ctx.save(); ctx.beginPath(); ctx.clip();
          ctx.fillRect(sx, y - 2, 5, h + 4); ctx.restore();
        }
        ctx.strokeStyle = '#fff'; ctx.lineWidth = 1.5; ctx.stroke();
      } else if (b.type === 'KINETIC') {
        ctx.beginPath(); ctx.roundRect ? ctx.roundRect(x, y, w, h, 6) : ctx.rect(x, y, w, h); ctx.fill();
        // coil motif
        ctx.strokeStyle = 'rgba(255,255,255,0.5)'; ctx.lineWidth = 2;
        ctx.beginPath();
        for (let sx = x + 6; sx < x + w - 6; sx += 8) { ctx.moveTo(sx, y + 4); ctx.lineTo(sx + 5, y + h - 4); }
        ctx.stroke();
      } else if (b.type === 'VOLATILE') {
        ctx.beginPath(); ctx.arc(0, 0, h / 2 + 2, 0, 7); ctx.fillStyle = col; ctx.fill();
        ctx.fillStyle = '#1a0000';
        ctx.beginPath(); ctx.arc(0, 0, h / 2 - 6, 0, 7); ctx.fill();
        ctx.fillStyle = col;
        ctx.beginPath(); ctx.arc(0, 0, h / 2 - 10, 0, 7); ctx.fill();
        ctx.fillStyle = b.hitFlash > 0 ? '#fff' : '#ffd6d6';
        ctx.font = '800 16px system-ui'; ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
        ctx.fillText('!', 0, 0);
      } else if (b.type === 'GOLD') {
        const g = ctx.createLinearGradient(0, y, 0, y + h);
        g.addColorStop(0, '#fff7ae'); g.addColorStop(1, col);
        ctx.fillStyle = g;
        ctx.beginPath(); ctx.roundRect ? ctx.roundRect(x, y, w, h, 4) : ctx.rect(x, y, w, h); ctx.fill();
        ctx.shadowColor = '#fde047'; ctx.shadowBlur = 10; ctx.fill(); ctx.shadowBlur = 0;
      } else {
        ctx.beginPath(); ctx.roundRect ? ctx.roundRect(x, y, w, h, 4) : ctx.rect(x, y, w, h); ctx.fill();
        if (b.type === 'PHASE') {
          ctx.strokeStyle = 'rgba(255,255,255,0.6)'; ctx.lineWidth = 1.5;
          ctx.strokeRect(x + 2, y + 2, w - 4, h - 4);
        }
        if (b.type === 'DRIFT') { // arrow motif
          ctx.strokeStyle = 'rgba(0,0,0,0.4)'; ctx.lineWidth = 2;
          ctx.beginPath();
          ctx.moveTo(b.driftDir * -8, -4); ctx.lineTo(b.driftDir * 8, 0); ctx.lineTo(b.driftDir * -8, 4);
          ctx.stroke();
        }
      }
      ctx.restore();
    }
  }

  function reset() { grid.length = 0; bricksSinceDrop = 0; }

  return { grid, build, reset, update, draw, collideBall, reflectBall, remove, count,
    ottYValue, noteBroken, get sinceDrop() { return bricksSinceDrop; },
    set sinceDrop(v) { bricksSinceDrop = v; } };
})();