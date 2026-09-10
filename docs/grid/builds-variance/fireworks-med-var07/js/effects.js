'use strict';
/* ============================================================
   Attic Breakout — effects.js
   Screen-space juice: shake, hit-stop, flashes, vignette/fever
   tints, chromatic double-draw, edge glow, fever banner.
   ============================================================ */

class Effects {
  constructor() {
    this.shake = 0; this.shakeX = 0; this.shakeY = 0;
    this.hitStop = 0;
    this.flash = 0; this.flashColor = '255,255,255';
    this.chroma = 0;        // chromatic offset amount
    this.fever = 0;         // 0..1+ attic escalation (seconds-based ramp)
    this.feverTier = 0;
    this.feverMaxTier = 0;
    this.slowmo = 0;        // slow-mo effect timer (visual)
    this.slowFactor = 1;
    this.edgeGlow = 0;      // 0..1 by multiplier tier
    this.bannerText = '';
    this.bannerT = 0;
    this.raysT = 0;
  }

  addShake(mag) { this.shake = Math.min(26, this.shake + mag); }
  doHitStop(sec) { this.hitStop = Math.max(this.hitStop, sec); }
  doFlash(color = '255,255,255', a = 1) { this.flashColor = color; this.flash = Math.max(this.flash, a); }
  doChroma(a = 1) { this.chroma = Math.max(this.chroma, a); }
  showBanner(text, dur = 1.6) { this.bannerText = text; this.bannerT = dur; this.bannerMax = dur; }
  doSlowmo(factor, dur) { this.slowFactor = factor; this.slowmo = Math.max(this.slowmo, dur); }

  /** Called each frame with real dt; returns the scaled dt for game logic. */
  update(dt, game) {
    if (this.hitStop > 0) {
      this.hitStop -= dt;
      this.shake *= 0.9;
      return 0; // world frozen
    }
    // Escalation (fever ramp, tiers, banners, music tier) only runs while the
    // game is in an active play state. Paused/menus keep the world frozen;
    // one-off visual timers (shake/flash/slow-mo/banner) still decay above.
    if (this.shake > 0) {
      this.shake = Math.max(0, this.shake - dt * 34);
      const m = this.shake;
      this.shakeX = rand(-m, m); this.shakeY = rand(-m, m);
    } else { this.shakeX = this.shakeY = 0; }
    if (this.flash > 0) this.flash = Math.max(0, this.flash - dt * 3.2);
    if (this.chroma > 0) this.chroma = Math.max(0, this.chroma - dt * 2.4);
    if (this.slowmo > 0) {
      this.slowmo -= dt;
      this.slowFactor = lerp(this.slowFactor, 1, dt * 2);
    } else this.slowFactor = 1;
    if (this.bannerT > 0) this.bannerT -= dt;

    // Fever escalation (ramp, tiers, banners, music tier) only runs while the
    // game is in an active play state. In pause/menus/end-screens nothing
    // escalates; one-off visuals above still decay. World dt still flows so
    // intro serve timers keep working.
    const active = !!game && (game.state === 'play' || game.state === 'sticky_aim');
    if (!active) {
      this.edgeGlow = 0;
      return dt;
    }

    // Fever escalation drives global tint / music / rain.
    const inFever = game && game.anyBallAbove();
    if (inFever) {
      this.fever += dt * CONFIG.FEVER_RAMP;
      const tier = this.fever >= CONFIG.FEVER_TIERS[2] ? 3
        : this.fever >= CONFIG.FEVER_TIERS[1] ? 2
        : this.fever >= CONFIG.FEVER_TIERS[0] ? 1 : 0;
      if (tier > this.feverTier) {
        this.feverTier = tier;
        this.feverMaxTier = Math.max(this.feverMaxTier, tier);
        if (game) game.stats.maxFever = Math.max(game.stats.maxFever, tier);
        if (tier >= 1) {
          game.audio.blip(700 + tier * 180, 0.25, { type: 'triangle', vol: 0.3, slide: 300 });
          this.doFlash('150,220,255', 0.25 * tier);
          this.showBanner(`FEVER ${tier}!`, 1);
          this.addShake(4);
        }
      }
      this.raysT += dt * (1 + tier);
    } else {
      this.fever = Math.max(0, this.fever - dt * CONFIG.FEVER_DECAY);
      if (this.feverTier > 0 && this.fever < CONFIG.FEVER_TIERS[0] * 0.5) {
        this.feverTier = 0;
        this.audio_setTier0(game);
      }
    }
    if (game) game.audio.setFeverTier(this.feverTier);
    this.edgeGlow = clamp((game ? game.multiplierTier() : 0) / 3, 0, 1);

    return dt * this.slowFactor;
  }

  audio_setTier0(game) { if (game) game.audio.setFeverTier(0); }

  /** Post-processing over the whole canvas (drawn last, in screen space). */
  draw(ctx, game) {
    const W = CONFIG.W, H = CONFIG.H;
    const tier = this.feverTier;

    // Fever tint + vignette escalation
    if (this.fever > 0.01) {
      const f = clamp(this.fever / 4, 0, 1);
      const hue = tier >= 3 ? 'rgba(255,110,190,' : tier >= 2 ? 'rgba(140,110,255,' : 'rgba(90,190,255,';
      const grad = ctx.createRadialGradient(W / 2, H * 0.42, H * 0.22, W / 2, H * 0.42, H * 0.75);
      grad.addColorStop(0, hue + (f * 0.10).toFixed(3) + ')');
      grad.addColorStop(1, hue + (f * 0.42).toFixed(3) + ')');
      ctx.fillStyle = grad;
      ctx.fillRect(0, 0, W, H);
    }

    // Ceiling light rays during fever
    if (tier >= 1 && game && game.anyBallAbove()) {
      const n = tier * 3;
      ctx.save();
      ctx.globalCompositeOperation = 'lighter';
      for (let i = 0; i < n; i++) {
        const x = ((i * 173 + this.raysT * 60) % (W + 200)) - 100;
        const a = 0.05 + 0.04 * Math.sin(this.raysT * 2 + i);
        const g2 = ctx.createLinearGradient(x, CONFIG.CEIL_Y, x + 60, H * 0.7);
        g2.addColorStop(0, `rgba(200,230,255,${a.toFixed(3)})`);
        g2.addColorStop(1, 'rgba(200,230,255,0)');
        ctx.fillStyle = g2;
        ctx.beginPath();
        ctx.moveTo(x - 26, CONFIG.CEIL_Y);
        ctx.lineTo(x + 26, CONFIG.CEIL_Y);
        ctx.lineTo(x + 110, H * 0.7);
        ctx.lineTo(x - 60, H * 0.7);
        ctx.closePath();
        ctx.fill();
      }
      ctx.restore();
    }

    // Screen-edge glow by multiplier tier
    if (this.edgeGlow > 0.01) {
      const pulse = 0.6 + Math.sin(game ? game.time * 4 : 0) * 0.15;
      const a = this.edgeGlow * 0.5 * pulse;
      ctx.save();
      ctx.globalCompositeOperation = 'lighter';
      const g3 = ctx.createRadialGradient(W / 2, H / 2, H * 0.3, W / 2, H / 2, H * 0.72);
      g3.addColorStop(0, 'rgba(0,0,0,0)');
      g3.addColorStop(1, `rgba(120,190,255,${a.toFixed(3)})`);
      ctx.fillStyle = g3;
      ctx.fillRect(0, 0, W, H);
      ctx.restore();
    }

    // Full-screen flash
    if (this.flash > 0.01) {
      ctx.fillStyle = `rgba(${this.flashColor},${clamp(this.flash, 0, 1).toFixed(3)})`;
      ctx.fillRect(0, 0, W, H);
    }

    // Slow-mo desaturation hint (blue-grey wash + darker vignette)
    if (this.slowmo > 0 && this.slowFactor < 0.95) {
      ctx.fillStyle = `rgba(90,110,150,${(1 - this.slowFactor) * 0.28})`;
      ctx.fillRect(0, 0, W, H);
    }

    // Banner (FEVER! / announcements)
    if (this.bannerT > 0 && this.bannerText) {
      const t = this.bannerT / (this.bannerMax || 1);
      const a = t < 0.25 ? t / 0.25 : Math.min(1, t / 0.2);
      const scale = 1 + (1 - Math.min(1, t * 4)) * 0.5;
      ctx.save();
      ctx.translate(W / 2, H * 0.34);
      ctx.scale(scale, scale);
      ctx.globalAlpha = clamp(a, 0, 1);
      ctx.font = `900 64px ${UI.FONT}`;
      ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
      ctx.lineWidth = 10; ctx.strokeStyle = 'rgba(0,0,0,0.7)';
      ctx.strokeText(this.bannerText, 0, 0);
      const g4 = ctx.createLinearGradient(0, -40, 0, 40);
      g4.addColorStop(0, '#fff');
      g4.addColorStop(1, '#7fd4ff');
      ctx.fillStyle = g4;
      ctx.fillText(this.bannerText, 0, 0);
      ctx.restore();
    }
  }
}

window.Effects = Effects;
