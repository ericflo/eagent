/* ============================================================
   TOPSIDE — render.js
   All canvas drawing in the logical 720x1280 coordinate space.
   Depends on: core.js (CFG, PAL, S), fx.js (FX.render).
   Exposes: R = { render(ctx), resize(canvas), update(dt),
                  toLogical(x,y), scale, offset }
   ============================================================ */
'use strict';

const R = (() => {
  let _scale = 1, _dpr = 1, _offX = 0, _offY = 0, _cw = 2, _ch = 2;

  const FONT = "'Avenir Next','Segoe UI',system-ui,-apple-system,sans-serif";

  /* ---------------- sizing / pointer mapping ---------------- */

  function resize(canvas) {
    _dpr = Math.max(1, (typeof window !== 'undefined' && window.devicePixelRatio) || 1);
    _scale = Math.min(window.innerWidth / CFG.W, window.innerHeight / CFG.H);
    if (!(_scale > 0)) _scale = 1;
    _cw = Math.max(2, Math.floor(CFG.W * _scale * _dpr));
    _ch = Math.max(2, Math.floor(CFG.H * _scale * _dpr));
    if (canvas) { canvas.width = _cw; canvas.height = _ch; }
    _offX = (window.innerWidth - CFG.W * _scale) / 2;
    _offY = (window.innerHeight - CFG.H * _scale) / 2;
    return { scale: _scale, dpr: _dpr, offX: _offX, offY: _offY, w: _cw, h: _ch };
  }

  /* client px -> logical coords (for input mapping) */
  function toLogical(cx, cy) {
    return { x: (cx - _offX) / _scale, y: (cy - _offY) / _scale };
  }

  function resetState(ctx) {
    ctx.shadowBlur = 0; ctx.shadowColor = 'transparent';
    ctx.globalAlpha = 1; ctx.globalCompositeOperation = 'source-over';
    ctx.lineWidth = 1; ctx.lineCap = 'butt'; ctx.lineJoin = 'miter';
    ctx.textAlign = 'left'; ctx.textBaseline = 'alphabetic';
  }

  /* rounded-rect path helper with fallback */
  function rr(ctx, x, y, w, h, r) {
    r = Math.min(r, w / 2, h / 2);
    if (typeof ctx.roundRect === 'function') { ctx.roundRect(x, y, w, h, r); return; }
    ctx.moveTo(x + r, y);
    ctx.arcTo(x + w, y, x + w, y + h, r);
    ctx.arcTo(x + w, y + h, x, y + h, r);
    ctx.arcTo(x, y + h, x, y, r);
    ctx.arcTo(x, y, x + w, y, r);
    ctx.closePath();
  }

  function shade(hex, f) {          // lighten(f>0) / darken(f<0) a hex color
    try {
      const c = hexToRgb(hex);
      const t = f > 0 ? 1 : 0;
      const k = Math.abs(f);
      return rgbStr(lerp(c.r, t * 255, k), lerp(c.g, t * 255, k), lerp(c.b, t * 255, k), 1);
    } catch (e) { return hex; }
  }

  /* ---------------- ambient background ---------------- */

  function drawBackground(ctx) {
    const g = ctx.createLinearGradient(0, 0, 0, CFG.H);
    g.addColorStop(0, PAL.bg0);
    g.addColorStop(1, PAL.bg1);
    ctx.fillStyle = g;
    ctx.fillRect(0, 0, CFG.W, CFG.H);

    /* nebula blobs (additive) */
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    for (const n of S.neb || []) {
      const a = (n.a || 0.08) * (0.75 + 0.25 * Math.sin(S.time * 0.4 + n.x));
      const ng = ctx.createRadialGradient(n.x, n.y, 0, n.x, n.y, n.r);
      ng.addColorStop(0, `hsla(${n.hue | 0},70%,45%,${a.toFixed(3)})`);
      ng.addColorStop(1, 'hsla(0,0%,0%,0)');
      ctx.fillStyle = ng;
      ctx.beginPath(); ctx.arc(n.x, n.y, n.r, 0, TAU); ctx.fill();
    }
    /* parallax stars */
    for (const s of S.stars || []) {
      const tw = 0.30 + 0.70 * (0.5 + 0.5 * Math.sin(s.tw + S.time * 2.2 / (s.z + 0.3)));
      const sz = 0.8 + s.z * 1.7;
      ctx.globalAlpha = tw * 0.85;
      ctx.fillStyle = s.z > 0.7 ? '#dbe6ff' : '#ffffff';
      ctx.fillRect(s.x, s.y, sz, sz);
    }
    /* shooting stars */
    for (const sh of S.shooting || []) {
      if (sh.life <= 0 && sh.tw <= 0) continue;
      const a = Math.max(0, Math.min(1, (sh.life || 0) * 1.4)) * 0.9;
      const tx = sh.x - (sh.vx || 0) * 0.13;
      const ty = sh.y - (sh.vy || 0) * 0.13;
      const sg = ctx.createLinearGradient(sh.x, sh.y, tx, ty);
      sg.addColorStop(0, `rgba(255,255,255,${a.toFixed(3)})`);
      sg.addColorStop(1, 'rgba(255,255,255,0)');
      ctx.strokeStyle = sg;
      ctx.lineWidth = 2;
      ctx.beginPath(); ctx.moveTo(sh.x, sh.y); ctx.lineTo(tx, ty); ctx.stroke();
    }
    ctx.restore();
    resetState(ctx);
  }
  /* ---------------- bricks ---------------- */

  function mateAlive(b) {
    if (b.kind !== 'shield' || !b.mate) return false;
    try {
      if (S.brickMap && S.brickMap.has(b.mate)) return true;
    } catch (e) { /* fall through to scan */ }
    for (const m of S.bricks) {
      if (m !== b && m.mate === b.mate) {
        if (S.brickMap && S.brickMap.has(m.id)) return true;
      }
    }
    return false;
  }

  function drawBrick(ctx, b) {
    const w = b.w || CFG.brickW, h = b.h || CFG.brickH;
    const x = b.x, y = b.y, r = 7;
    const pal = PAL.brick[b.c] || PAL.brick.p;
    const kind = b.kind || 'normal';
    const shieldUp = kind === 'shield' && mateAlive(b);
    const timeOpen = (() => {
      if (kind !== 'time' || !Array.isArray(b.phase)) return true;
      const t = ((S.time % 1) + 1) % 1;
      return t >= b.phase[0] && t < b.phase[1];
    })();

    ctx.save();
    ctx.beginPath();
    rr(ctx, x, y, w, h, r);
    ctx.clip();

    /* body */
    if (shieldUp) {
      ctx.globalAlpha = 0.32;
    } else if (kind === 'glass') {
      ctx.globalAlpha = 0.5;
    } else if (kind === 'time' && !timeOpen) {
      ctx.globalAlpha = 0.3;
    } else {
      ctx.globalAlpha = 1;
    }
    const grad = ctx.createLinearGradient(x, y, x, y + h);
    grad.addColorStop(0, shade(pal.fill, 0.28));
    grad.addColorStop(0.45, pal.fill);
    grad.addColorStop(1, shade(pal.fill, -0.38));
    ctx.fillStyle = grad;
    ctx.fillRect(x, y, w, h);
    ctx.globalAlpha = 1;

    /* top highlight + inner glow border */
    ctx.fillStyle = cc('#ffffff', 0.16);
    ctx.fillRect(x + 3, y + 2, w - 6, 2.5);
    ctx.shadowColor = pal.glow;
    ctx.shadowBlur = 6;
    ctx.strokeStyle = cc(pal.edge, shieldUp ? 0.55 : 0.95);
    ctx.lineWidth = 1.4;
    ctx.strokeRect(x + 0.7, y + 0.7, w - 1.4, h - 1.4);
    ctx.shadowBlur = 0;

    /* living sheen sweep (not on tough / shield-up) */
    if (kind !== 'tough' && !shieldUp) {
      const sweep = ((S.time * 0.25 + (b.row || 0) * 0.13) % 1 + 1) % 1;
      const sx = x + sweep * w - w * 0.11;
      const sg = ctx.createLinearGradient(sx, y, sx + w * 0.22, y);
      sg.addColorStop(0, 'rgba(255,255,255,0)');
      sg.addColorStop(0.5, `rgba(255,255,255,${shieldUp ? 0.05 : 0.14})`);
      sg.addColorStop(1, 'rgba(255,255,255,0)');
      ctx.fillStyle = sg;
      ctx.fillRect(sx, y, w * 0.22, h);
    }

    /* ------------ class-specific markers ------------ */
    const mx = x + w / 2, my = y + h / 2;

    if (kind === 'tough') {
      const hp = Math.max(1, b.hp | 0);
      const gap = 0.2 / Math.max(1, hp - 1);
      for (let i = 0; i < hp; i++) {
        const px = x + w * (0.5 + (hp === 1 ? 0 : (i - (hp - 1) / 2) * gap));
        ctx.beginPath();
        ctx.arc(px, y + h - 8, 3.2, 0, TAU);
        ctx.fillStyle = cc(pal.edge, 0.95);
        ctx.fill();
      }
      if (b.hp < b.maxHp) {
        ctx.strokeStyle = cc('#000000', 0.5);
        ctx.lineWidth = 1.6;
        ctx.beginPath();
        ctx.moveTo(x + w * 0.18, y + h * 0.4);
        ctx.lineTo(x + w * 0.5, y + h * 0.62);
        ctx.lineTo(x + w * 0.42, y + h * 0.9);
        ctx.moveTo(x + w * 0.8, y + h * 0.3);
        ctx.lineTo(x + w * 0.6, y + h * 0.55);
        ctx.stroke();
      }
    } else if (kind === 'shield') {
      if (shieldUp) {
        /* hex ring + lock */
        ctx.globalAlpha = 0.95;
        ctx.strokeStyle = cc(pal.edge, 0.9);
        ctx.lineWidth = 2;
        ctx.beginPath();
        for (let i = 0; i < 6; i++) {
          const a = S.time * 0.6 + i * (TAU / 6);
          const pr = Math.min(w, h) * 0.34;
          const px = mx + Math.cos(a) * pr, py = my + Math.sin(a) * pr;
          if (i === 0) ctx.moveTo(px, py); else ctx.lineTo(px, py);
        }
        ctx.closePath(); ctx.stroke();
        ctx.globalAlpha = 1;
        ctx.fillStyle = pal.edge;
        ctx.fillRect(mx - 6, my - 1, 12, 8);
        ctx.fillStyle = cc(pal.edge, 0.25);
        ctx.fillRect(mx - 8, my - 1, 16, 3);
      } else {
        /* exposed: glint + cracks */
        ctx.strokeStyle = cc('#ffffff', 0.8);
        ctx.lineWidth = 2;
        ctx.beginPath();
        ctx.moveTo(x + w * 0.25, y + h * 0.2);
        ctx.lineTo(x + w * 0.42, y + h * 0.35);
        ctx.lineTo(x + w * 0.3, y + h * 0.55);
        ctx.stroke();
        ctx.strokeStyle = cc('#000000', 0.35);
        ctx.lineWidth = 1.4;
        ctx.beginPath();
        ctx.moveTo(x + w * 0.6, y + h * 0.25);
        ctx.lineTo(x + w * 0.72, y + h * 0.6);
        ctx.moveTo(x + w * 0.5, y + h * 0.8);
        ctx.lineTo(x + w * 0.85, y + h * 0.7);
        ctx.stroke();
      }
    } else if (kind === 'mirror') {
      ctx.save();
      ctx.beginPath();
      rr(ctx, x + 3, y + 3, w - 6, h - 6, r - 3);
      ctx.clip();
      const mg = ctx.createLinearGradient(x + w * 0.1, y, x + w * 0.9, y);
      mg.addColorStop(0, 'rgba(255,255,255,0)');
      mg.addColorStop(0.4, 'rgba(255,255,255,0.75)');
      mg.addColorStop(0.6, 'rgba(255,255,255,0.75)');
      mg.addColorStop(1, 'rgba(255,255,255,0)');
      ctx.fillStyle = mg;
      ctx.fillRect(x + w * 0.08, y, w * 0.84, h);
      ctx.restore();
    } else if (kind === 'angle') {
      ctx.fillStyle = cc(pal.edge, 0.95);
      ctx.font = `800 19px ${FONT}`;
      ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
      ctx.fillText('⇄', mx, my + 1);
    } else if (kind === 'speed') {
      ctx.fillStyle = cc(pal.edge, 0.95);
      const tw = Math.min(w, h) * 0.4;
      ctx.beginPath();
      ctx.moveTo(mx + tw * 0.12, my - tw * 0.5);
      ctx.lineTo(mx - tw * 0.38, my + tw * 0.12);
      ctx.lineTo(mx - tw * 0.02, my + tw * 0.12);
      ctx.lineTo(mx - tw * 0.12, my + tw * 0.5);
      ctx.lineTo(mx + tw * 0.38, my - tw * 0.12);
      ctx.lineTo(mx + tw * 0.02, my - tw * 0.12);
      ctx.closePath(); ctx.fill();
    } else if (kind === 'time') {
      if (timeOpen) {
        ctx.globalAlpha = 1;
        ctx.strokeStyle = pal.edge;
        ctx.lineWidth = 2;
        ctx.beginPath(); ctx.arc(mx, my, 8, 0, TAU); ctx.stroke();
        const ang = S.time * 3;
        ctx.beginPath();
        ctx.moveTo(mx, my); ctx.lineTo(mx + Math.cos(ang - 1.2) * 6, my + Math.sin(ang - 1.2) * 6);
        ctx.moveTo(mx, my); ctx.lineTo(mx + Math.cos(ang) * 4, my + Math.sin(ang) * 4);
        ctx.stroke();
      } else {
        ctx.globalAlpha = 0.5;
        ctx.fillStyle = pal.edge;
        ctx.font = `600 15px ${FONT}`;
        ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
        ctx.fillText('◌', mx, my);
      }
    } else if (kind === 'bomb') {
      const big = !!b.big;
      const br = big ? 13 : 9;
      ctx.fillStyle = '#1a1030';
      ctx.beginPath(); ctx.arc(mx, my + 2, br, 0, TAU); ctx.fill();
      ctx.strokeStyle = cc(pal.edge, 0.8); ctx.lineWidth = 1.5;
      ctx.stroke();
      const fuse = S.time * 6;
      ctx.strokeStyle = '#e8d9c0'; ctx.lineWidth = 2;
      ctx.beginPath();
      ctx.moveTo(mx, my - br + 3);
      ctx.quadraticCurveTo(mx + 4, my - br - 6, mx + 8 * Math.cos(fuse), my - br - 8 - 2 * Math.sin(fuse));
      ctx.stroke();
      const sx = mx + 8 * Math.cos(fuse), sy = my - br - 8 - 2 * Math.sin(fuse);
      ctx.shadowColor = '#ffb347'; ctx.shadowBlur = 10;
      ctx.fillStyle = '#ffb347';
      ctx.beginPath(); ctx.arc(sx, sy, 3, 0, TAU); ctx.fill();
      ctx.shadowBlur = 0;
      ctx.fillStyle = cc('#ff5c8a', 0.5);
      ctx.beginPath(); ctx.arc(mx - 2, my, 2, 0, TAU); ctx.fill();
    } else if (kind === 'glass') {
      ctx.strokeStyle = cc('#ffffff', 0.55);
      ctx.lineWidth = 1.2;
      ctx.beginPath();
      ctx.moveTo(x + w * 0.25, y + h * 0.2);
      ctx.lineTo(x + w * 0.45, y + h * 0.55);
      ctx.lineTo(x + w * 0.3, y + h * 0.85);
      ctx.moveTo(x + w * 0.72, y + h * 0.15);
      ctx.lineTo(x + w * 0.6, y + h * 0.5);
      ctx.lineTo(x + w * 0.78, y + h * 0.72);
      ctx.stroke();
      const rg = ctx.createRadialGradient(x + w * 0.32, y + h * 0.3, 1, x + w * 0.32, y + h * 0.3, w * 0.5);
      rg.addColorStop(0, 'rgba(255,255,255,0.35)');
      rg.addColorStop(1, 'rgba(255,255,255,0)');
      ctx.fillStyle = rg;
      ctx.fillRect(x, y, w, h);
    }

    ctx.restore();
    resetState(ctx);
  }

  function drawBricks(ctx) {
    for (const b of S.bricks || []) drawBrick(ctx, b);
  }

  /* ---------------- powerups ---------------- */

  function drawPowerups(ctx) {
    for (const p of S.powerups || []) {
      const col = (typeof p.color === 'string' ? p.color : '#ffffff') || '#fff';
      const bob = Math.sin(p.t * 2 || 0) * 3;
      const px = p.x, py = p.y + bob;
      ctx.save();
      ctx.shadowColor = col; ctx.shadowBlur = 14;
      ctx.globalAlpha = 0.22;
      ctx.fillStyle = col;
      ctx.beginPath(); ctx.arc(px, py, 22, 0, TAU); ctx.fill();
      ctx.shadowBlur = 0;
      ctx.globalAlpha = 0.95;
      ctx.fillStyle = '#120a2e';
      ctx.beginPath(); ctx.arc(px, py, 15, 0, TAU); ctx.fill();
      ctx.strokeStyle = col; ctx.lineWidth = 2;
      ctx.stroke();
      ctx.fillStyle = col;
      ctx.font = `800 14px ${FONT}`;
      ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
      ctx.fillText(p.icon || '?', px, py + 1);
      ctx.restore();
      resetState(ctx);
    }
  }
  /* ---------------- balls ---------------- */

  function ballColor(ball) {
    if (ball.type === 'flux') return { c: '#ff9d5c', glow: '#ff6a3d' };
    if (ball.type === 'pearl') return { c: '#7fe9ff', glow: '#2fb8e8' };
    if (ball.type === 'cannon') return { c: '#ffd84d', glow: '#ffb347' };
    const hue = (S.time * 90 + ball.hue) % 360;
    return { c: `hsl(${hue},95%,68%)`, glow: `hsl(${hue},90%,60%)` };
  }

  function drawBall(ctx, ball) {
    const col = ballColor(ball);
    const r = ball.r || CFG.ballR;
    const sp = ball.speed || 0;

    /* trail (fades) */
    const trail = ball.trail || [];
    for (let i = 0; i < trail.length; i++) {
      const t = trail[i];
      if (!t || !isFinite(t.x) || !isFinite(t.y)) continue;
      const k = i / Math.max(1, trail.length - 1);
      const a = (t.life || 0.5) * 0.55 * (0.35 + 0.65 * k);
      ctx.globalAlpha = a;
      ctx.fillStyle = col.c;
      ctx.beginPath(); ctx.arc(t.x, t.y, r * (0.25 + 0.75 * k), 0, TAU); ctx.fill();
    }
    ctx.globalAlpha = 1;

    /* motion streak */
    if (sp > 340) {
      const ss = clamp(sp / CFG.ballMax, 0.4, 1.6);
      ctx.strokeStyle = cc(col.c, 0.35);
      ctx.lineWidth = r * 0.5;
      ctx.lineCap = 'round';
      ctx.beginPath();
      ctx.moveTo(ball.x, ball.y);
      ctx.lineTo(ball.x - ball.vx * 0.022 * ss, ball.y - ball.vy * 0.022 * ss);
      ctx.stroke();
      ctx.lineCap = 'butt';
    }

    /* glow + core */
    if (ball.type === 'cannon') {
      const pg = ctx.createRadialGradient(ball.x, ball.y, r * 0.2, ball.x, ball.y, r * 3.4);
      pg.addColorStop(0, cc(col.c, 0.9));
      pg.addColorStop(0.5, cc(col.c, 0.35));
      pg.addColorStop(1, cc(col.c, 0));
      ctx.fillStyle = pg;
      ctx.beginPath(); ctx.arc(ball.x, ball.y, r * 3.4, 0, TAU); ctx.fill();
    } else {
      ctx.save();
      ctx.shadowColor = col.glow;
      ctx.shadowBlur = 18;
      ctx.fillStyle = col.c;
      ctx.beginPath(); ctx.arc(ball.x, ball.y, r, 0, TAU); ctx.fill();
      ctx.restore();
    }

    /* core highlight */
    const cg = ctx.createRadialGradient(ball.x - r * 0.35, ball.y - r * 0.35, 1, ball.x, ball.y, r);
    cg.addColorStop(0, 'rgba(255,255,255,0.9)');
    cg.addColorStop(1, 'rgba(255,255,255,0)');
    ctx.fillStyle = cg;
    ctx.beginPath(); ctx.arc(ball.x, ball.y, r, 0, TAU); ctx.fill();

    /* riding crown glow */
    if (S.ridingBall === ball) {
      const pulse = 0.6 + 0.4 * Math.sin(S.time * 6);
      ctx.save();
      ctx.shadowColor = '#ffd84d';
      ctx.shadowBlur = 26 * pulse + 12;
      ctx.strokeStyle = cc('#ffd84d', 0.95);
      ctx.lineWidth = 3;
      ctx.beginPath(); ctx.arc(ball.x, ball.y, r + 6 + pulse * 2, 0, TAU); ctx.stroke();
      ctx.restore();
      /* inner golden halo */
      ctx.save();
      ctx.globalCompositeOperation = 'lighter';
      ctx.shadowColor = '#ffe14d';
      ctx.shadowBlur = 20 + 10 * pulse;
      ctx.strokeStyle = cc('#ffe14d', 0.45 + 0.3 * pulse);
      ctx.lineWidth = 1.6;
      ctx.beginPath(); ctx.arc(ball.x, ball.y, r + 3, 0, TAU); ctx.stroke();
      ctx.restore();
      ctx.font = `800 ${r + 8}px ${FONT}`;
      ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
      ctx.fillStyle = '#ffe14d';
      ctx.shadowColor = '#ffd84d';
      ctx.shadowBlur = 10;
      ctx.fillText('♛', ball.x, ball.y - r - 6);
      ctx.shadowBlur = 0;
    }

    resetState(ctx);
  }

  function drawBalls(ctx) {
    for (const b of S.balls || []) {
      if (!b || !isFinite(b.x) || !isFinite(b.y)) continue;
      drawBall(ctx, b);
    }
  }

  /* ---------------- paddle ---------------- */

  function drawPaddle(ctx) {
    const p = S.paddle;
    if (!p) return;
    const w = p.w || CFG.paddleW;
    const h = p.h || CFG.paddleH;
    const x = p.x - w / 2, y = p.y - h / 2;

    /* engine flames when moving fast */
    if (Math.abs(p.vx || 0) > 18) {
      const dir = p.vx > 0 ? 1 : -1;
      const f = S.time * 22;
      for (let i = 0; i < 4; i++) {
        const fx = x - dir * (6 + (i % 2) * 10) - ((f + i * 12) % 20);
        const fy = y + h / 2 + (i % 2) * 10;
        const fl = ctx.createRadialGradient(fx, fy, 1, fx, fy, 9);
        fl.addColorStop(0, 'rgba(255,170,80,0.55)');
        fl.addColorStop(1, 'rgba(255,170,80,0)');
        ctx.fillStyle = fl;
        ctx.beginPath(); ctx.arc(fx, fy, 9, 0, TAU); ctx.fill();
      }
    }

    /* body */
    const bodyGrad = ctx.createLinearGradient(x, y, x, y + h);
    bodyGrad.addColorStop(0, '#c8fbff');
    bodyGrad.addColorStop(0.35, PAL.paddle);
    bodyGrad.addColorStop(1, '#2ec4e8');
    ctx.save();
    ctx.beginPath();
    rr(ctx, x, y, w, h, h / 2);
    ctx.shadowColor = PAL.paddle;
    ctx.shadowBlur = 16;
    ctx.fillStyle = bodyGrad;
    ctx.fill();
    ctx.restore();

    /* top sheen */
    ctx.save();
    ctx.beginPath();
    rr(ctx, x, y, w, h, h / 2);
    ctx.clip();
    ctx.fillStyle = 'rgba(255,255,255,0.35)';
    ctx.fillRect(x + 4, y + 1.5, w - 8, h * 0.32);
    ctx.strokeStyle = cc('#ffffff', 0.5);
    ctx.lineWidth = 1;
    ctx.strokeRect(x + 1.5, y + 1.5, w - 3, h - 3);
    ctx.restore();

    /* white hit flash */
    if ((p.flash || 0) > 0) {
      ctx.save();
      ctx.beginPath();
      rr(ctx, x, y, w, h, h / 2);
      ctx.clip();
      ctx.fillStyle = cc('#ffffff', p.flash * 0.8);
      ctx.fillRect(x, y, w, h);
      ctx.restore();
    }

    /* grabber claws */
    if (p.grab || p.grabStuck) {
      const cy = y + h / 2;
      const r2 = h * 0.55;
      ctx.strokeStyle = cc('#ffe14d', 0.9);
      ctx.lineWidth = 2.5;
      for (const sx of [1, -1]) {
        const px = x + w / 2 + sx * (w / 2 - 5);
        ctx.beginPath();
        ctx.arc(px, cy, r2, sx > 0 ? Math.PI * 0.05 : Math.PI * 0.55, sx > 0 ? Math.PI * 0.95 : Math.PI * 0.45);
        ctx.stroke();
        ctx.beginPath();
        ctx.arc(px, cy - 4, r2 * 0.75, sx > 0 ? Math.PI * 0.1 : Math.PI * 0.55, sx > 0 ? Math.PI * 0.9 : Math.PI * 0.45);
        ctx.stroke();
      }
    }

    resetState(ctx);
  }

  /* ---------------- cannon & countdown ---------------- */

  function drawCannon(ctx) {
    const c = S.cannon;
    const p = S.paddle;
    if (!c || !p) return;
    const gx = p.x;
    /* c = {wait, dir:'up'}: charge grows as wait -> 0, then it launches (cannon null) */
    const t = clamp(1 - (c.wait || 0) / Math.max(0.001, CFG.cannon.wait || 1.35), 0, 1);
    const grow = t;
    const gy = p.y - 28 - t * 10;
    const pr = 6 + grow * 16;
    const pulse = 1 + 0.15 * Math.sin(S.time * 12);

    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    /* charging glow pillar from paddle up to orb */
    const pg = ctx.createLinearGradient(gx - 40, 0, gx + 40, 0);
    pg.addColorStop(0, cc('#ffd84d', 0));
    pg.addColorStop(0.5, cc('#ffd84d', 0.08 + grow * 0.18));
    pg.addColorStop(1, cc('#ffd84d', 0));
    ctx.fillStyle = pg;
    ctx.fillRect(gx - 40, 0, 80, gy);
    ctx.shadowColor = '#ffd84d';
    ctx.shadowBlur = 26;
    const cg2 = ctx.createRadialGradient(gx, gy, 1, gx, gy, pr * pulse);
    cg2.addColorStop(0, '#fff8dd');
    cg2.addColorStop(0.6, '#ffe14d');
    cg2.addColorStop(1, cc('#ffb347', 0.15));
    ctx.fillStyle = cg2;
    ctx.beginPath(); ctx.arc(gx, gy, pr * pulse, 0, TAU); ctx.fill();
    ctx.restore();

    /* outer ring */
    ctx.save();
    ctx.strokeStyle = cc('#ffd84d', 0.7);
    ctx.lineWidth = 2.5;
    ctx.beginPath(); ctx.arc(gx, gy, pr * pulse + 10 + Math.sin(S.time * 10) * 2, 0, TAU); ctx.stroke();
    ctx.restore();
    resetState(ctx);
  }

  function drawCountdown(ctx) {
    if (S.state !== 'playing') return;
    const cd = S.countdown || 0;
    if (cd <= 0) return;
    const x = S.paddle ? S.paddle.x : CFG.W / 2;
    const y = (S.paddle ? S.paddle.y : CFG.H * 0.9) - 90;
    const pulse = 1 + Math.max(0, (cd % 1)) * 0.35;
    ctx.save();
    ctx.font = `800 ${Math.round(52 * pulse)}px ${FONT}`;
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    ctx.shadowColor = '#ffffff'; ctx.shadowBlur = 22;
    ctx.fillStyle = '#ffffff';
    ctx.fillText(cd.toFixed(cd < 10 ? 1 : 0), x, y);
    ctx.restore();
    resetState(ctx);
  }
  /* ---------------- HUD helpers ---------------- */

  function textOut(ctx, t, x, y, size, color, opts = {}) {
    ctx.save();
    ctx.font = `${opts.weight || 800} ${size}px ${FONT}`;
    ctx.textAlign = opts.align || 'center';
    ctx.textBaseline = opts.baseline || 'middle';
    if (opts.shadow) { ctx.shadowColor = opts.shadow; ctx.shadowBlur = opts.blur || 12; }
    ctx.fillStyle = color;
    ctx.fillText(t, x, y);
    ctx.restore();
  }

  function medalFor(score) { return score >= 50000 ? 2 : score >= 20000 ? 1 : 0; }
  function scoreLabel(s) { return s >= 100000 ? '★' : s >= 50000 ? '++' : ''; }

  function drawScore(ctx) {
    const pad = 22;
    let x = pad, y = 26;
    const score = (S.score | 0).toLocaleString('en-US');
    const hi = Math.max(S.score | 0, S.hi | 0).toLocaleString('en-US');
    ctx.textAlign = 'left'; ctx.textBaseline = 'top';
    ctx.font = `800 30px ${FONT}`;
    ctx.save();
    ctx.shadowColor = '#ffffff'; ctx.shadowBlur = 14;
    ctx.fillStyle = '#ffffff';
    ctx.fillText(score, x, y);
    ctx.restore();
    const sw = ctx.measureText(score).width;
    if (sw > 0) {
      ctx.fillStyle = cc('#9ff6ff', 0.85);
      ctx.font = `700 13px ${FONT}`;
      ctx.fillText('HI ' + hi, x, y + 35);
    }
  }

  function drawChargeHUD(ctx) {
    const w = 300, h = 14, x = CFG.W / 2 - w / 2, y = 124;
    const cv = clamp(S.charge || 0, 0, 100);
    const ready = !!S.fireReady;
    const pulse = ready ? 0.7 + 0.3 * Math.sin(S.time * 8) : 1;

    ctx.save();
    /* glow behind when ready */
    if (ready) {
      ctx.shadowColor = '#ffd84d';
      ctx.shadowBlur = 18 * pulse;
    }
    ctx.globalAlpha = 0.35;
    ctx.fillStyle = '#120a2e';
    ctx.beginPath(); rr(ctx, x - 2, y - 2, w + 4, h + 4, (h + 4) / 2); ctx.fill();
    ctx.globalAlpha = 1;
    ctx.shadowBlur = 0;

    /* fill */
    if (cv > 0.5) {
      const g = ctx.createLinearGradient(x, y, x + w, y);
      const t = cv / 100;
      g.addColorStop(0, ready ? '#ff8fb2' : '#ff5c8a');
      g.addColorStop(1, ready ? '#ffd84d' : '#ff9d5c');
      ctx.fillStyle = g;
      ctx.beginPath(); rr(ctx, x, y, Math.max(8, w * t), h, h / 2); ctx.fill();
      ctx.save();
      ctx.beginPath(); rr(ctx, x, y, Math.max(8, w * t), h, h / 2); ctx.clip();
      ctx.globalAlpha = 0.45;
      ctx.fillStyle = '#ffffff';
      ctx.fillRect(x + w * t - 14, y, 14, h);
      ctx.restore();
    }

    /* tick marks */
    ctx.strokeStyle = cc('#ffffff', 0.16);
    ctx.lineWidth = 1;
    for (let i = 1; i < 10; i++) {
      const tx = x + w * i / 10;
      ctx.beginPath();
      ctx.moveTo(tx, y + 2); ctx.lineTo(tx, y + h - 2);
      ctx.stroke();
    }
    ctx.restore();

    /* labels */
    ctx.font = `800 13px ${FONT}`;
    ctx.textAlign = 'left'; ctx.textBaseline = 'top';
    ctx.fillStyle = cc('#ffffff', 0.75);
    ctx.fillText('POWER', x, y - 20);
    ctx.textAlign = 'right';
    ctx.fillStyle = cc(ready ? '#ffd84d' : '#ffffff', ready ? 0.95 : 0.75);
    ctx.fillText(Math.round(cv) + '%', x + w, y - 20);

    /* ready label */
    if (ready) {
      ctx.save();
      ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
      ctx.font = `800 17px ${FONT}`;
      ctx.shadowColor = '#ffd84d'; ctx.shadowBlur = 14;
      ctx.fillStyle = '#ffe14d';
      ctx.fillText('⚡ POWER READY  —  FIRE', x + w / 2, y + h + 15);
      ctx.restore();
    }
  }

  function drawCombo(ctx) {
    const c = S.combo || 0;
    if (c < 2) return;
    const mr = Math.max(1, S.mr || 1);
    const w = 30 + c * 3, h = 16;
    const x = 20, y = 68;
    ctx.save();
    ctx.globalAlpha = 0.85;
    ctx.fillStyle = '#120a2e';
    ctx.beginPath(); rr(ctx, x, y, w, h, h / 2); ctx.fill();
    ctx.restore();
    ctx.font = `800 11px ${FONT}`;
    ctx.textAlign = 'left'; ctx.textBaseline = 'middle';
    ctx.fillStyle = c >= 5 ? '#ffd84d' : '#ffffff';
    ctx.fillText('COMBO ×' + mr, x + 7, y + h / 2 + 0.5);
  }

  function drawLives(ctx) {
    const n = Math.max(0, Math.min(8, S.lives | 0));
    if (n <= 0) return;
    const x = CFG.W - 22, y = 26;
    const size = 18;
    const startX = x - (size * n + 3 * (n - 1)) + size / 2;
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    for (let i = 0; i < n; i++) {
      ctx.font = `800 13px ${FONT}`;
      ctx.save();
      ctx.shadowColor = '#ff5c8a'; ctx.shadowBlur = 8;
      ctx.fillStyle = i === 0 ? '#ffffff' : cc('#ffd9e6', 0.9);
      ctx.fillText('●', startX + i * (size + 3), y);
      ctx.restore();
    }
    ctx.font = `700 12px ${FONT}`;
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    ctx.fillStyle = cc('#ffb9d0', 0.9);
    ctx.fillText('♥', startX + n * (size + 3) - 3, y);
  }

  function drawModeTint(ctx) {
    if ((S.mode && S.mode.slow > 0) || (S.mode && S.mode.freeze > 0)) {
      ctx.save();
      ctx.fillStyle = cc('#3b7bff', 0.12);
      ctx.fillRect(0, 0, CFG.W, CFG.H);
      /* snow flurries for freeze */
      if (S.mode.freeze > 0 && Math.random() < 0.2) {
        FX.snowBurst(2);
      }
      ctx.restore();
    }
    if (S.freezeFlash > 0) {
      ctx.save();
      ctx.fillStyle = cc('#bfeaff', Math.min(0.5, S.freezeFlash * 4));
      ctx.fillRect(0, 0, CFG.W, CFG.H);
      ctx.restore();
    }
    /* low-lives vignette pulse */
    if ((S.lives | 0) === 1 && S.state === 'playing') {
      const a = 0.10 + 0.08 * Math.sin(S.time * 5);
      const vg = ctx.createRadialGradient(CFG.W / 2, CFG.H / 2, CFG.H * 0.35, CFG.W / 2, CFG.H / 2, CFG.H * 0.72);
      vg.addColorStop(0, 'rgba(255,59,107,0)');
      vg.addColorStop(1, `rgba(255,59,107,${a.toFixed(3)})`);
      ctx.save();
      ctx.fillStyle = vg;
      ctx.fillRect(0, 0, CFG.W, CFG.H);
      ctx.restore();
    }
  }

  function drawFlash(ctx) {
    if ((S.flash || 0) > 0) {
      ctx.save();
      ctx.fillStyle = rgbStr(S.flashColor[0], S.flashColor[1], S.flashColor[2], Math.min(0.65, S.flash));
      ctx.fillRect(0, 0, CFG.W, CFG.H);
      ctx.restore();
    }
  }

  /* ---------------- state overlays ---------------- */

  function glowTitle(ctx, str, x, y, size, color, blur = 30) {
    ctx.save();
    ctx.font = `800 ${size}px ${FONT}`;
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    ctx.shadowColor = color; ctx.shadowBlur = blur;
    ctx.fillStyle = color;
    ctx.fillText(str, x, y);
    ctx.fillStyle = cc('#ffffff', 0.9);
    ctx.fillText(str, x, y);
    ctx.restore();
    resetState(ctx);
  }

  function pulseText(ctx, str, x, y, size, color, t, opts = {}) {
    const k = 0.5 + 0.5 * Math.sin(t * (opts.speed || 3.2));
    ctx.save();
    ctx.font = `${opts.weight || 800} ${size * (1 + k * (opts.amp || 0.06))}px ${FONT}`;
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    if (opts.shadow) { ctx.shadowColor = opts.shadow; ctx.shadowBlur = 16; }
    ctx.globalAlpha = 0.65 + 0.35 * k;
    ctx.fillStyle = color;
    ctx.fillText(str, x, y);
    ctx.restore();
    resetState(ctx);
  }

  function panel(ctx, x, y, w, h) {
    ctx.save();
    ctx.fillStyle = 'rgba(8,4,24,0.72)';
    ctx.beginPath(); rr(ctx, x, y, w, h, 18); ctx.fill();
    ctx.strokeStyle = 'rgba(255,255,255,0.14)';
    ctx.lineWidth = 1.5;
    ctx.beginPath(); rr(ctx, x, y, w, h, 18); ctx.stroke();
    ctx.restore();
  }

  function drawMenu(ctx) {
    const cycled = ((S.time * 0.08) % 1);
    const hue = Math.floor(cycled * 360);

    /* title */
    ctx.save();
    ctx.font = `800 92px ${FONT}`;
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    const tg = ctx.createLinearGradient(0, 250, 0, 440);
    tg.addColorStop(0, '#ffffff');
    tg.addColorStop(0.6, '#ffd84d');
    tg.addColorStop(1, '#ff9d5c');
    ctx.fillStyle = tg;
    ctx.shadowColor = `hsl(${hue},90%,60%)`;
    ctx.shadowBlur = 42;
    ctx.fillText('TOPSIDE', CFG.W / 2, 330);
    ctx.restore();

    ctx.save();
    ctx.font = `600 22px ${FONT}`;
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    ctx.fillStyle = cc('#ffffff', 0.8);
    ctx.fillText('a modern breakout', CFG.W / 2, 402);
    ctx.restore();

    /* how-to panel */
    const pw = 560, ph = 292, px = CFG.W / 2 - pw / 2, py = 452;
    panel(ctx, px, py, pw, ph);
    ctx.save();
    ctx.font = `800 17px ${FONT}`;
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    ctx.fillStyle = cc('#9ff6ff', 0.95);
    ctx.fillText('HOW TO PLAY', CFG.W / 2, py + 30);
    ctx.restore();

    const rows = [
      ['🖱 / 👆  drag', 'move paddle (up & down too)'],
      ['tap', 'while ball serves / fires power'],
      ['← → ↑ ↓', 'keyboard move   ·   SPACE fire'],
      ['P pause · M mute · R restart']
    ];
    ctx.textAlign = 'left'; ctx.textBaseline = 'middle';
    for (let i = 0; i < rows.length; i++) {
      const ry = py + 68 + i * 48;
      const label = rows[i][0];
      const desc = rows[i][1];
      ctx.font = `800 15px ${FONT}`;
      ctx.fillStyle = cc('#ffd84d', 0.9);
      ctx.fillText(label, px + 26, ry);
      if (desc) {
        ctx.font = `500 14px ${FONT}`;
        ctx.fillStyle = cc('#ffffff', 0.72);
        ctx.fillText(desc, px + 180, ry);
      }
    }
    /* charge / fire hint */
    ctx.save();
    ctx.font = `600 14px ${FONT}`;
    ctx.textAlign = 'center';
    ctx.fillStyle = cc('#ffd84d', 0.85);
    ctx.shadowColor = '#ffd84d'; ctx.shadowBlur = 8;
    ctx.fillText('⚡ charge by hitting bricks — at 100% FIRE a cannon to break through and RIDE on top!', CFG.W / 2, py + ph + 34);
    ctx.restore();

    pulseText(ctx, 'TAP / PRESS SPACE TO START', CFG.W / 2, 1000, 26, '#ffffff', S.time, { shadow: '#ffffff', amp: 0.05 });

    /* small hint for fire button */
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    ctx.font = `600 13px ${FONT}`;
    ctx.fillStyle = cc('#ffffff', 0.5);
    ctx.fillText(`high score  ${Math.max(S.hi | 0, S.score | 0).toLocaleString('en-US')}`, CFG.W / 2, 1080);
  }

  function drawPaused(ctx) {
    ctx.save();
    ctx.fillStyle = 'rgba(5,2,16,0.72)';
    ctx.fillRect(0, 0, CFG.W, CFG.H);
    ctx.restore();
    glowTitle(ctx, 'PAUSED', CFG.W / 2, CFG.H * 0.42, 64, '#9ff6ff', 30);
    pulseText(ctx, 'press P to resume', CFG.W / 2, CFG.H * 0.42 + 70, 20, '#ffffff', S.time, { shadow: '#ffffff' });
  }

  function drawGameOver(ctx) {
    ctx.save();
    ctx.fillStyle = 'rgba(5,2,16,0.8)';
    ctx.fillRect(0, 0, CFG.W, CFG.H);
    ctx.restore();
    glowTitle(ctx, 'GAME OVER', CFG.W / 2, 430, 64, PAL.danger, 36);
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    ctx.font = `800 46px ${FONT}`;
    ctx.save();
    ctx.shadowColor = '#ffffff'; ctx.shadowBlur = 16;
    ctx.fillStyle = '#ffffff';
    ctx.fillText((S.score | 0).toLocaleString('en-US'), CFG.W / 2, 530);
    ctx.restore();
    ctx.font = `600 19px ${FONT}`;
    ctx.fillStyle = cc('#ffffff', 0.75);
    ctx.fillText('high score  ' + Math.max(S.score | 0, S.hi | 0).toLocaleString('en-US'), CFG.W / 2, 585);
    ctx.font = `600 16px ${FONT}`;
    ctx.fillStyle = cc('#ffffff', 0.55);
    ctx.fillText('level ' + (S.level | 0) + '   ·   ' + (S.totals ? S.totals.bricks : 0) + ' bricks broken', CFG.W / 2, 620);
    pulseText(ctx, 'TAP TO RESTART', CFG.W / 2, 720, 26, '#ffffff', S.time, { shadow: '#ffffff' });
  }

  function drawWin(ctx) {
    const gold = (S.time * 0.35) % 1;
    ctx.save();
    ctx.fillStyle = 'rgba(40,22,4,0.55)';
    ctx.fillRect(0, 0, CFG.W, CFG.H);
    ctx.restore();
    ctx.save();
    ctx.font = `800 74px ${FONT}`;
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    const tg = ctx.createLinearGradient(0, 380, 0, 560);
    tg.addColorStop(0, '#ffffff');
    tg.addColorStop(0.6, '#ffe14d');
    tg.addColorStop(1, '#ffb347');
    ctx.shadowColor = '#ffd84d'; ctx.shadowBlur = 40;
    ctx.fillStyle = tg;
    ctx.fillText('VICTORY', CFG.W / 2, 470);
    ctx.restore();
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    ctx.font = `700 20px ${FONT}`;
    ctx.save();
    ctx.shadowColor = '#ffd84d'; ctx.shadowBlur = 12;
    ctx.fillStyle = '#ffe8b0';
    ctx.fillText('stage ' + ((S.level | 0) - 1) + ' cleared', CFG.W / 2, 560);
    ctx.restore();
    ctx.font = `800 42px ${FONT}`;
    ctx.fillStyle = '#ffffff';
    ctx.fillText((S.score | 0).toLocaleString('en-US'), CFG.W / 2, 625);
    ctx.font = `600 16px ${FONT}`;
    ctx.fillStyle = cc('#ffffff', 0.7);
    ctx.fillText('next stage unlocks stronger bricks', CFG.W / 2, 680);
    pulseText(ctx, 'TAP TO CONTINUE', CFG.W / 2, 760, 26, '#ffe14d', S.time, { shadow: '#ffd84d' });
  }

  function drawBoot(ctx) {
    drawMenu(ctx);
    ctx.save();
    ctx.fillStyle = 'rgba(5,2,16,0.5)';
    ctx.fillRect(0, 0, CFG.W, CFG.H);
    ctx.restore();
    glowTitle(ctx, 'TOPSIDE', CFG.W / 2, CFG.H * 0.32, 84, '#9ff6ff', 42);
    pulseText(ctx, 'click / tap / press a key to begin', CFG.W / 2, CFG.H * 0.5, 22, '#ffffff', S.time, { shadow: '#ffffff' });
  }
  /* ---------------- touch indicator ---------------- */

  function drawTouchIndicator(ctx) {
    if (typeof I === 'undefined' || !I.lastTouch) return;
    const active = (I.isTouchActive ? I.isTouchActive() : I.touchActive);
    if (!active) return;
    const t = I.lastTouch;
    if (t.x === undefined || t.y === undefined || t.x === null || t.y === null) return;
    const k = S.time;
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    ctx.strokeStyle = cc('#9ff6ff', 0.55);
    ctx.lineWidth = 2.5;
    ctx.beginPath(); ctx.arc(t.x, t.y, 26 + Math.sin(k * 6) * 3, 0, TAU); ctx.stroke();
    ctx.globalAlpha = 0.25;
    ctx.fillStyle = '#9ff6ff';
    ctx.beginPath(); ctx.arc(t.x, t.y, 26, 0, TAU); ctx.fill();
    ctx.globalAlpha = 1;
    ctx.fillStyle = cc('#ffffff', 0.9);
    ctx.beginPath(); ctx.arc(t.x, t.y, 5, 0, TAU); ctx.fill();
    ctx.restore();
    resetState(ctx);
  }

  /* ---------------- ambient bloom near ball ---------------- */

  function drawBallBloom(ctx) {
    const b = S.balls && S.balls.length ? S.balls[0] : null;
    if (!b || !isFinite(b.x) || !isFinite(b.y)) return;
    const col = ballColor(b);
    ctx.save();
    ctx.globalCompositeOperation = 'lighter';
    const bb = ctx.createRadialGradient(b.x, b.y, 10, b.x, b.y, 190);
    bb.addColorStop(0, cc(col.c, 0.06));
    bb.addColorStop(1, cc(col.c, 0));
    ctx.fillStyle = bb;
    ctx.fillRect(b.x - 190, b.y - 190, 380, 380);
    ctx.restore();
    resetState(ctx);
  }

  /* ---------------- main render ---------------- */

  function render(ctx) {
    if (!ctx) return;
    /* letterbox fill */
    ctx.fillStyle = PAL.bg0;
    ctx.fillRect(0, 0, _cw, _ch);
    ctx.save();
    let shx = 0, shy = 0;
    if ((S.shake || 0) > 0.01) {
      shx = (S.shakeDx || 0);
      shy = (S.shakeDy || 0);
    }
    ctx.translate(_offX + shx, _offY + shy);
    ctx.scale(_scale, _scale);

    /* 1. background */
    drawBackground(ctx);
    drawBallBloom(ctx);

    /* 2. game world */
    drawBricks(ctx);
    drawPowerups(ctx);
    drawBalls(ctx);
    drawCannon(ctx);
    drawPaddle(ctx);
    drawCountdown(ctx);
    drawTouchIndicator(ctx);

    /* 3. FX */
    FX.render(ctx);

    /* 4. HUD */
    if (S.state === 'playing' || S.state === 'pause') {
      drawScore(ctx);
      drawChargeHUD(ctx);
      drawCombo(ctx);
      drawLives(ctx);
    }

    /* 5. state overlays */
    if (S.state === 'boot' || S.state === 'menu') drawMenu(ctx);
    else if (S.state === 'pause') drawPaused(ctx);
    else if (S.state === 'over') drawGameOver(ctx);
    else if (S.state === 'win') drawWin(ctx);

    /* golden TOPSIDE banner while riding (crown moment) */
    if (S.ridingBall) {
      const pulse = 0.55 + 0.45 * Math.sin(S.time * 4);
      const y = 208 - 8 * pulse;
      ctx.save();
      ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
      const tg = ctx.createLinearGradient(0, y - 20, 0, y + 20);
      tg.addColorStop(0, '#fff6d8');
      tg.addColorStop(0.55, '#ffe14d');
      tg.addColorStop(1, '#ffb347');
      ctx.shadowColor = '#ffd84d';
      ctx.shadowBlur = 26;
      ctx.fillStyle = tg;
      ctx.font = `900 32px ${FONT}`;
      ctx.fillText('TOPSIDE!', CFG.W / 2, y);
      ctx.font = `700 13px ${FONT}`;
      ctx.fillStyle = cc('#ffe14d', 0.88);
      ctx.shadowBlur = 12;
      ctx.fillText('♛ ' + Math.floor(S.crown / 5) + ' crowns ♛', CFG.W / 2, y + 28);
      ctx.restore();
      resetState(ctx);
    }

    drawModeTint(ctx);
    drawFlash(ctx);

    ctx.restore();
    resetState(ctx);
  }

  /* advance render-side time (ambient loops) */
  function update(dt) {
    /* no-op: we rely on S.time from game tick; keep for symmetry */
  }

  return { render, resize, toLogical, update, scale: () => _scale, offset: () => ({ x: _offX, y: _offY }) };
})();
