/* BREAKTHROUGH — js/render.js
   All HUD-on-canvas drawing helpers: paddle, walls, brick rendering per type,
   frenzy banner, vignette, chromatic aberration flash. */
window.Render = (function () {
  const U = window.U;

  function drawBrick(ctx, b, time) {
    if (!b.alive) {
      if (b.respawnAt) { // regen ghost preview
        ctx.save();
        ctx.globalAlpha = 0.12 + 0.08 * Math.sin(time * 4);
        U.rr(ctx, b.x, b.y, b.w, b.h, 5);
        ctx.fillStyle = U.hsl(b.hue, b.sat, b.lit); ctx.fill();
        ctx.restore();
      }
      return;
    }
    const flash = Math.max(0, b.hitFlash);
    ctx.save();
    let alpha = 1;
    if (b.type === 'phase') alpha = b.solid ? 1 : (b.ghostAlpha || 0.2);
    ctx.globalAlpha = alpha;

    const hue = flash > 0 ? U.lerp(b.hue, 0, flash) : b.hue;
    const lit = b.lit + flash * 30;

    // frenzy heat grade: gold wash that escalates with frenzyLevel, applied to the
    // body fill so ALL brick types warm up; badges drawn on top stay readable
    const fl = (window.G && window.G.state.frenzy > 0) ? window.G.state.frenzyLevel : 0;
    const heat = Math.min(1, fl * 1.6);

    // body
    U.rr(ctx, b.x, b.y, b.w, b.h, 5);
    const g = ctx.createLinearGradient(0, b.y, 0, b.y + b.h);
    g.addColorStop(0, U.hsl(hue, b.sat, Math.min(80, lit + 14)));
    g.addColorStop(1, U.hsl(hue, b.sat, Math.max(20, lit - 16)));
    ctx.fillStyle = g; ctx.fill();
    if (heat > 0.02) {
      // gold wash (multiply-ish source-over), strength = heat, subtle wobble per brick
      ctx.globalAlpha = heat * (0.62 + 0.10 * Math.sin(time * 2.5 + b.row * 0.4 + b.col * 0.25));
      U.rr(ctx, b.x, b.y, b.w, b.h, 5);
      ctx.fillStyle = U.hsl(44, 96, 50);
      ctx.fill();
      ctx.strokeStyle = U.hsl(48, 100, 66, 0.9);
      ctx.lineWidth = 2;
      ctx.stroke();
      ctx.globalAlpha = alpha;
    }
    ctx.strokeStyle = U.hsl(hue, b.sat, Math.min(92, lit + 34), 0.9);
    ctx.lineWidth = 1.5; ctx.stroke();

    // type badges — clear visual language --------------------------------
    if (b.type === 'angle') {           // steep angle required: chevron pointing up/down
      ctx.strokeStyle = U.hsl(hue, 100, 20, .8); ctx.lineWidth = 2.5;
      const cx = b.x + b.w / 2, cy = b.y + b.h / 2, s = Math.min(b.w, b.h) * 0.22;
      ctx.beginPath();
      ctx.moveTo(cx - s, cy + s * 0.6); ctx.lineTo(cx, cy - s * 0.6); ctx.lineTo(cx + s, cy + s * 0.6);
      ctx.stroke();
      ctx.beginPath();
      ctx.moveTo(cx - s, cy + s * 1.4); ctx.lineTo(cx, cy + s * 0.2); ctx.lineTo(cx + s, cy + s * 1.4);
      ctx.stroke();
    } else if (b.type === 'speed') {    // speed gate: horizontal wave lines
      ctx.strokeStyle = 'rgba(240,255,255,.75)'; ctx.lineWidth = 2;
      for (let i = 0; i < 3; i++) {
        const yy = b.y + b.h * (0.28 + i * 0.22);
        ctx.beginPath();
        ctx.moveTo(b.x + 4 + ((time * 30 + i * 6) % 10), yy);
        ctx.lineTo(b.x + b.w - 4 + ((time * 30 + i * 6) % 10), yy);
        ctx.stroke();
      }
    } else if (b.type === 'moving') {   // motion arrows on sides
      ctx.fillStyle = 'rgba(235,255,235,.8)';
      const cy = b.y + b.h / 2, s = Math.min(6, b.w * 0.12);
      ctx.beginPath();
      ctx.moveTo(b.x + 3, cy); ctx.lineTo(b.x + 3 + s, cy - s * 0.7); ctx.lineTo(b.x + 3 + s, cy + s * 0.7);
      ctx.closePath(); ctx.fill();
      ctx.beginPath();
      ctx.moveTo(b.x + b.w - 3, cy); ctx.lineTo(b.x + b.w - 3 - s, cy - s * 0.7); ctx.lineTo(b.x + b.w - 3 - s, cy + s * 0.7);
      ctx.closePath(); ctx.fill();
    } else if (b.type === 'phase') {    // dashed outline = ghost
      if (!b.solid) {
        ctx.setLineDash([4, 4]);
        U.rr(ctx, b.x, b.y, b.w, b.h, 5);
        ctx.strokeStyle = U.hsl(b.hue, 90, 70, .8); ctx.lineWidth = 1.5; ctx.stroke();
        ctx.setLineDash([]);
      } else {
        // shimmer marker
        ctx.fillStyle = U.hsl(b.hue, 100, 85, .5);
        ctx.beginPath(); ctx.arc(b.x + b.w / 2, b.y + b.h / 2, 2.4 + Math.sin(time * 6) * 0.8, 0, U.TAU); ctx.fill();
      }
    } else if (b.type === 'bomb') {     // bomb core
      const cx = b.x + b.w / 2, cy = b.y + b.h / 2;
      const r = Math.min(b.w, b.h) * 0.22 * (1 + Math.sin(time * 5 + b.col) * 0.12);
      ctx.fillStyle = U.hsl(5, 100, 60, .9);
      ctx.beginPath(); ctx.arc(cx, cy, r, 0, U.TAU); ctx.fill();
      ctx.fillStyle = '#fff';
      ctx.beginPath(); ctx.arc(cx - r * 0.3, cy - r * 0.3, r * 0.25, 0, U.TAU); ctx.fill();
    } else if (b.type === 'regen') {    // spiral arrow
      ctx.strokeStyle = 'rgba(255,220,190,.85)'; ctx.lineWidth = 2;
      const cx = b.x + b.w / 2, cy = b.y + b.h / 2, r = Math.min(b.w, b.h) * 0.22;
      ctx.beginPath();
      ctx.arc(cx, cy, r, time * 2 % U.TAU, time * 2 % U.TAU + 4.2);
      ctx.stroke();
    } else if (b.type === 'armored') {  // rivets
      ctx.fillStyle = 'rgba(230,235,255,.6)';
      const m = 4;
      for (const [px, py] of [[b.x + m, b.y + m], [b.x + b.w - m, b.y + m], [b.x + m, b.y + b.h - m], [b.x + b.w - m, b.y + b.h - m]]) {
        ctx.beginPath(); ctx.arc(px, py, 1.8, 0, U.TAU); ctx.fill();
      }
      U.rr(ctx, b.x + 3, b.y + 3, b.w - 6, b.h - 6, 3);
      ctx.strokeStyle = 'rgba(255,255,255,.22)'; ctx.lineWidth = 1; ctx.stroke();
    }

    // frenzy tint: escalating gold hue-shift + glow on alive bricks
    if (window.G && window.G.state.frenzy > 0) {
      const fl = window.G.state.frenzyLevel;
      const hueShift = (b.hue + fl * 120 * (0.5 + 0.5 * Math.sin(time * 2 + b.row * 0.4))) % 360;
      const pulse = 0.14 + 0.22 * fl + 0.10 * Math.sin(time * (9 + fl * 6) + b.col);
      ctx.globalCompositeOperation = 'lighter';
      U.rr(ctx, b.x, b.y, b.w, b.h, 5);
      ctx.fillStyle = U.hsl(45, 100, 60, pulse);
      ctx.fill();
      if (fl > 0.15) {
        U.rr(ctx, b.x - 2, b.y - 2, b.w + 6, b.h + 6, 6);
        ctx.strokeStyle = U.hsl(45, 100, 70, 0.25 + 0.3 * fl);
        ctx.lineWidth = 1.5;
        ctx.stroke();
      }
      ctx.globalCompositeOperation = 'source-over';
    }
    ctx.restore();
  }

  function drawPaddle(ctx, p, time) {
    const { x, y, w, h } = p;
    ctx.save();
    // frenzy: hot gold rim + stronger glow
    if (window.G && window.G.state.frenzy > 0) {
      const fl = window.G.state.frenzyLevel;
      const hue = 45 + Math.sin(time * 6) * 25;
      ctx.save();
      ctx.shadowColor = U.hsl(hue, 100, 60);
      ctx.shadowBlur = 16 + fl * 26;
      U.rr(ctx, x - 2, y - 2, w + 4, h + 4, (h + 4) / 2);
      ctx.strokeStyle = U.hsl(hue, 100, 65, 0.5 + 0.5 * fl);
      ctx.lineWidth = 2 + fl * 2;
      ctx.stroke();
      ctx.restore();
    }
    // under-glow
    const g = ctx.createRadialGradient(x + w / 2, y + h / 2, 4, x + w / 2, y + h / 2, w * 0.7);
    g.addColorStop(0, U.hsl(p.hue, 100, 65, 0.35));
    g.addColorStop(1, U.hsl(p.hue, 100, 60, 0));
    ctx.fillStyle = g;
    ctx.fillRect(x - w * 0.3, y - h, w * 1.6, h * 3);
    // body
    U.rr(ctx, x, y, w, h, h / 2);
    const bg = ctx.createLinearGradient(0, y, 0, y + h);
    bg.addColorStop(0, '#ffffff');
    bg.addColorStop(1, U.hsl(p.hue, 85, 55));
    ctx.fillStyle = bg; ctx.fill();
    // sticky indicator
    if (p.sticky) {
      ctx.strokeStyle = U.hsl(150, 100, 60, .9); ctx.lineWidth = 2;
      ctx.setLineDash([6, 5]);
      U.rr(ctx, x + 1, y + 1, w - 2, h - 2, h / 2 - 1); ctx.stroke();
      ctx.setLineDash([]);
    }
    // laser cannons
    if (p.laser) {
      ctx.fillStyle = U.hsl(350, 100, 60);
      ctx.fillRect(x + 4, y - 6, 6, 6);
      ctx.fillRect(x + w - 10, y - 6, 6, 6);
      const cl = Math.max(0, p.laserCd);
      if (cl <= 0) {
        ctx.fillStyle = '#fff';
        ctx.fillRect(x + 5, y - 8, 4, 3); ctx.fillRect(x + w - 9, y - 8, 4, 3);
      }
    }
    ctx.restore();
  }

  function drawVignette(ctx, intensity, frenzy, time, frenzyLevel) {
    const g = ctx.createRadialGradient(360, 480, 320, 360, 480, 640);
    g.addColorStop(0, 'rgba(0,0,0,0)');
    g.addColorStop(1, `rgba(0,0,10,${0.35 + intensity * 0.1})`);
    ctx.fillStyle = g;
    ctx.fillRect(0, 0, 720, 960);
    if (frenzy > 0) {
      ctx.save();
      ctx.globalCompositeOperation = 'lighter';
      const fl = frenzyLevel || 0;
      const pulse = (0.06 + 0.08 * fl) + 0.05 * Math.sin(time * (6 + fl * 10));
      const fg = ctx.createLinearGradient(0, 0, 0, 960);
      fg.addColorStop(0, U.hsl(30 + fl * 40, 100, 60, pulse));
      fg.addColorStop(1, U.hsl(fl * 40 - 40, 100, 55, 0.35 * fl));
      ctx.fillStyle = fg;
      ctx.fillRect(0, 0, 720, 960);
      ctx.restore();
    }
  }

  return { drawBrick, drawPaddle, drawVignette };
})();