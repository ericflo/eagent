'use strict';
// ---------------------------------------------------------------------------
// fx.js — screen shake, chromatic flashes, freeze frames, floating text,
// backdrop pulses. The "juice" layer.
// ---------------------------------------------------------------------------

const FX = (() => {
  // shake: decaying trauma-based camera shake
  let trauma = 0;              // 0..1, shake magnitude
  let shakeX = 0, shakeY = 0, shakeRot = 0;
  let flashA = 0;              // white flash alpha
  let flashColor = '255,255,255';
  let freeze = 0;              // seconds of hit-stop remaining
  let slowmo = 0;              // seconds of slow motion remaining
  let chroma = 0;              // chromatic aberration amount 0..1

  const floaters = [];         // floating score/text popups

  function addTrauma(t) { trauma = clamp(trauma + t, 0, 1); }
  function flash(a = 0.35, color = '255,255,255') {
    flashA = Math.max(flashA, a);
    flashColor = color;
  }
  function hitStop(sec) { freeze = Math.max(freeze, sec); }
  function startSlowmo(sec) { slowmo = Math.max(slowmo, sec); }
  function addChroma(c) { chroma = clamp(chroma + c, 0, 1); }

  function floatText(x, y, text, color = '#fff', size = 18, life = 0.9, opts = {}) {
    floaters.push({
      x, y, text, color, size, life, maxLife: life,
      vy: opts.vy !== undefined ? opts.vy : -60,
      vx: opts.vx || 0,
      scale: opts.scale || 0,
      outline: opts.outline || false,
    });
  }

  // dt in real seconds. Returns {shake, slowmo} state for the caller.
  function update(dt) {
    trauma = Math.max(0, trauma - dt * 1.6);
    const sh = trauma * trauma;
    const t = performance.now() / 1000;
    shakeX = (Math.sin(t * 97.3) + Math.sin(t * 61.7)) * 0.5 * 22 * sh;
    shakeY = (Math.sin(t * 83.1) + Math.sin(t * 47.9)) * 0.5 * 22 * sh;
    shakeRot = Math.sin(t * 70.3) * 0.015 * sh;
    flashA = Math.max(0, flashA - dt * 2.6);
    chroma = Math.max(0, chroma - dt * 1.2);
    if (freeze > 0) freeze = Math.max(0, freeze - dt);
    if (slowmo > 0) slowmo = Math.max(0, slowmo - dt);
    for (let i = floaters.length - 1; i >= 0; i--) {
      const f = floaters[i];
      f.life -= dt;
      f.y += f.vy * dt;
      f.x += f.vx * dt;
      if (f.life <= 0) floaters.splice(i, 1);
    }
    return { frozen: freeze > 0, slowFactor: slowmo > 0 ? 0.35 : 1 };
  }

  // Apply camera transform for world drawing; returns restore fn context
  function applyCamera(ctx, W, H) {
    ctx.save();
    ctx.translate(W / 2, H / 2);
    ctx.rotate(shakeRot);
    ctx.translate(-W / 2 + shakeX, -H / 2 + shakeY);
  }
  function restoreCamera(ctx) { ctx.restore(); }

  function drawFloaters(ctx, scale) {
    for (const f of floaters) {
      const t = 1 - f.life / f.maxLife;
      const alpha = f.life < 0.25 ? f.life / 0.25 : 1;
      const pop = f.scale ? (t < 0.25 ? lerp(1.6, 1, easeOutCubic(t / 0.25)) * f.scale : f.scale * lerp(1, 1.08, (t - 0.25) / 0.75)) : 1;
      ctx.save();
      ctx.globalAlpha = alpha;
      ctx.font = `800 ${Math.round(f.size * pop * scale)}px system-ui, -apple-system, 'Segoe UI', sans-serif`;
      ctx.textAlign = 'center';
      ctx.textBaseline = 'middle';
      if (f.outline) {
        ctx.lineWidth = 3 * scale;
        ctx.strokeStyle = 'rgba(0,0,0,0.7)';
        ctx.strokeText(f.text, f.x, f.y);
      }
      ctx.fillStyle = f.color;
      ctx.strokeStyle = 'rgba(0,0,0,0.55)';
      ctx.lineWidth = 3 * scale;
      ctx.strokeText(f.text, f.x, f.y);
      ctx.fillText(f.text, f.x, f.y);
      ctx.restore();
    }
  }

  function drawFlash(ctx, W, H) {
    if (flashA > 0.003) {
      ctx.fillStyle = `rgba(${flashColor},${flashA})`;
      ctx.fillRect(0, 0, W, H);
    }
  }

  function drawChroma(ctx, drawWorld, W, H) {
    // Cheap chromatic aberration: draw world 3x offset with channel filters
    // is expensive; instead we overlay subtle red/cyan edge vignettes.
    if (chroma < 0.02) return;
    ctx.save();
    ctx.globalCompositeOperation = 'screen';
    ctx.globalAlpha = chroma * 0.25;
    const g1 = ctx.createLinearGradient(0, 0, W, 0);
    g1.addColorStop(0, 'rgba(255,0,64,0.6)');
    g1.addColorStop(0.2, 'rgba(255,0,64,0)');
    g1.addColorStop(0.8, 'rgba(0,255,255,0)');
    g1.addColorStop(1, 'rgba(0,255,255,0.6)');
    ctx.fillStyle = g1;
    ctx.fillRect(0, 0, W, H);
    ctx.restore();
  }

  function reset() {
    trauma = 0; flashA = 0; freeze = 0; slowmo = 0; chroma = 0;
    floaters.length = 0;
  }

  return {
    addTrauma, flash, hitStop, startSlowmo, addChroma, floatText,
    update, applyCamera, restoreCamera, drawFloaters, drawFlash, drawChroma,
    reset,
    get trauma() { return trauma; },
    get isFrozen() { return freeze > 0; },
    get isSlowmo() { return slowmo > 0; },
  };
})();
