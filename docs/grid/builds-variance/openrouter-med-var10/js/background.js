/* BREAKTHROUGH — js/background.js
   Parallax starfield + grid that reacts to multiplier/frenzy:
   speeds up, hue-shifts, adds pulse waves on beat.
*/
window.Background = (function () {
  const U = window.U;

  const stars = [];
  const W = 720, H = 960; // logical
  for (let i = 0; i < 130; i++) {
    stars.push({
      x: Math.random() * W, y: Math.random() * H,
      z: Math.random(),                       // depth 0..1
      s: 0.6 + Math.random() * 1.8
    });
  }
  let hue = 220;
  let time = 0;
  let pulses = [];   // expanding grid glow rings on beat

  function update(dt, intensity, frenzy, frenzyLevel, frenzyActive) {
    time += dt;
    updateOrbs(dt, frenzyLevel || 0, !!frenzyActive);
    // hue drifts with intensity
    hue = U.damp(hue, 220 + intensity * 160 + frenzy * 60, 2, dt);
    const speed = 20 + intensity * 140 + frenzy * 260;
    for (const s of stars) {
      s.y += speed * (0.3 + s.z) * dt;
      if (s.y > H + 5) { s.y = -5; s.x = Math.random() * W; }
    }
    for (let i = pulses.length - 1; i >= 0; i--) {
      pulses[i].t += dt;
      if (pulses[i].t > 0.7) pulses.splice(i, 1);
    }
  }

  function beat() { pulses.push({ t: 0 }); if (pulses.length > 5) pulses.shift(); }

  // Frenzy bokeh orbs: spawn while frenzy active, escalating with level.
  // Drawn on canvas during Background.draw (which renders beneath all DOM HUD), never as DOM.
  const orbs = [];
  function updateOrbs(dt, frenzyLevel, active) {
    if (active) {
      const target = 8 + Math.round(frenzyLevel * 14);
      while (orbs.length < target) {
        orbs.push({ x: U.rand(0, W), y: U.rand(0, H), r: U.rand(14, 42), vx: U.rand(-18, 18), vy: U.rand(-30, -6), ph: Math.random() * U.TAU });
      }
    }
    for (let i = orbs.length - 1; i >= 0; i--) {
      const o = orbs[i];
      o.x += o.vx * dt; o.y += o.vy * dt; o.ph += dt;
      if (!active || o.y < -60 || o.x < -60 || o.x > W + 60) orbs.splice(i, 1);
    }
  }
  function drawOrbs(ctx) {
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    for (const o of orbs) {
      const a = 0.10 + 0.06 * Math.sin(o.ph * 2);
      const g = ctx.createRadialGradient(o.x, o.y, 0, o.x, o.y, o.r);
      g.addColorStop(0, U.hsl(45, 100, 70, a));
      g.addColorStop(1, U.hsl(45, 100, 60, 0));
      ctx.fillStyle = g;
      ctx.beginPath(); ctx.arc(o.x, o.y, o.r, 0, U.TAU); ctx.fill();
    }
    ctx.restore();
  }

  function draw(ctx) {
    // vertical gradient base
    const g = ctx.createLinearGradient(0, 0, 0, H);
    g.addColorStop(0, U.hsl(hue, 60, 8));
    g.addColorStop(1, U.hsl(hue + 40, 55, 5));
    ctx.fillStyle = g;
    ctx.fillRect(0, 0, W, H);

    // perspective grid
    ctx.save();
    ctx.globalAlpha = 0.16;
    ctx.strokeStyle = U.hsl(hue + 30, 90, 60);
    ctx.lineWidth = 1;
    const gy = H * 0.55, off = (time * 40) % 48;
    for (let i = 0; i < 14; i++) {
      const y = gy + Math.pow(i + off / 48, 1.7) * 26;
      if (y > H) break;
      ctx.beginPath(); ctx.moveTo(0, y); ctx.lineTo(W, y); ctx.stroke();
    }
    for (let i = 0; i <= 12; i++) {
      const x = (i / 12) * W;
      ctx.beginPath();
      ctx.moveTo(x, gy);
      ctx.lineTo(W / 2 + (x - W / 2) * 2.4, H);
      ctx.stroke();
    }
    ctx.restore();

    // stars
    for (const s of stars) {
      ctx.fillStyle = U.hsl(hue + s.z * 80, 80, 50 + s.z * 35, 0.25 + s.z * 0.6);
      ctx.fillRect(s.x, s.y, s.s, s.s * (1 + s.z * 2));
    }

    // beat pulses
    for (const p of pulses) {
      const k = p.t / 0.7;
      ctx.strokeStyle = U.hsl(hue + 40, 100, 70, (1 - k) * 0.25);
      ctx.lineWidth = 2;
      ctx.beginPath();
      ctx.arc(W / 2, gy, 40 + k * 900, 0, U.TAU);
      ctx.stroke();
    }

    // frenzy bokeh orbs — canvas layer, always beneath DOM HUD
    drawOrbs(ctx);
  }

  return { update, draw, beat };
})();