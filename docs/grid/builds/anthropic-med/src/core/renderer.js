// src/core/renderer.js — canvas sizing, world<->screen transform, letterbox,
// and the animated background (parallax star/grid field).
//
// World HEIGHT is dynamic (see constants.js). On phone-portrait aspect
// ratios we solve for the world height that makes a WIDTH-fit scale cover
// the whole viewport height exactly (no letterbox at all — the bricks stay
// anchored to the top via BRICK_TOP, and the paddle's floor area just grows
// taller). On wide/short (desktop landscape) viewports that would require
// an absurdly squashed world, so we fall back to a HEIGHT-fit scale and
// decorate the resulting side bars instead of leaving them flat black.
import { W, H, H_MIN, H_MAX, setWorldHeight } from './constants.js';

const PORTRAIT_ASPECT_MAX = 0.82; // cssW/cssH <= this -> treat as phone-portrait (width-fit)

export class Renderer {
  constructor(canvas) {
    this.canvas = canvas;
    this.ctx = canvas.getContext('2d', { alpha: false });
    this.dpr = Math.min(2.5, window.devicePixelRatio || 1);
    this.scale = 1;
    this.offX = 0;
    this.offY = 0;
    this.cssW = 0;
    this.cssH = 0;
    this.zoom = 1;
    this.mode = 'portrait'; // 'portrait' (width-fit, dynamic H) | 'landscape' (height-fit)
    this.shakeOffset = { x: 0, y: 0 };
    // safe-area insets, in WORLD units (converted from env(safe-area-inset-*)
    // px via the hidden #safeAreaProbe element) so HUD layout never gets
    // clipped by a notch/home-indicator/rounded corner on real devices.
    this.safeTop = 0; this.safeRight = 0; this.safeBottom = 0; this.safeLeft = 0;
    this._safeProbe = document.getElementById('safeAreaProbe') || null;
    this.stars = [];
    // seed stars across the FULL possible height range so nothing needs
    // reseeding when H changes at runtime; drawBackground only draws the
    // ones currently within [0, H].
    for (let i = 0; i < 180; i++) {
      this.stars.push({
        x: Math.random() * W,
        y: Math.random() * H_MAX,
        r: Math.random() * 1.8 + 0.4,
        depth: Math.random() * 0.8 + 0.2,
        tw: Math.random() * Math.PI * 2,
      });
    }
    this.resize();
    this._onResize = () => this.resize();
    window.addEventListener('resize', this._onResize);
    window.addEventListener('orientationchange', this._onResize);
  }

  destroy() {
    window.removeEventListener('resize', this._onResize);
    window.removeEventListener('orientationchange', this._onResize);
  }

  resize() {
    const cssW = window.innerWidth;
    const cssH = window.innerHeight;
    this.cssW = cssW;
    this.cssH = cssH;
    this.canvas.width = Math.round(cssW * this.dpr);
    this.canvas.height = Math.round(cssH * this.dpr);
    this.canvas.style.width = cssW + 'px';
    this.canvas.style.height = cssH + 'px';

    const aspect = cssW / Math.max(1, cssH);

    if (aspect <= PORTRAIT_ASPECT_MAX) {
      // --- Phone/portrait: fill the width, stretch H to exactly fill the
      // viewport height too. No letterbox on typical phone aspect ratios.
      this.mode = 'portrait';
      const desiredH = cssH * (W / cssW);
      const h = setWorldHeight(desiredH);
      const scale = cssW / W;
      this.scale = scale;
      this.offX = 0;
      // if desiredH got clamped (extremely tall/narrow device) there may be
      // a small residual top/bottom gap — center it rather than pin it.
      this.offY = (cssH - h * scale) / 2;
    } else {
      // --- Desktop / landscape / tablet-wide: fall back to a fixed,
      // comfortable world height and fit by HEIGHT, decorating the side
      // bars instead of leaving them flat black.
      this.mode = 'landscape';
      setWorldHeight(1500);
      const pad = 0.04;
      const availH = cssH * (1 - pad * 2);
      const scale = availH / H;
      this.scale = scale;
      this.offX = (cssW - W * scale) / 2;
      this.offY = (cssH - H * scale) / 2;
    }

    this._readSafeArea();
  }

  /** Read env(safe-area-inset-*) (via the hidden probe element's computed
   * padding) and convert to world units so callers (HUD) can reserve a top
   * inset that works on notched phones as well as plain rectangles. */
  _readSafeArea() {
    const el = this._safeProbe;
    if (!el || !this.scale) return;
    let cs;
    try { cs = getComputedStyle(el); } catch { return; }
    const px = (v) => parseFloat(v) || 0;
    this.safeTop = px(cs.paddingTop) / this.scale;
    this.safeRight = px(cs.paddingRight) / this.scale;
    this.safeBottom = px(cs.paddingBottom) / this.scale;
    this.safeLeft = px(cs.paddingLeft) / this.scale;
  }

  /** Convert a client-space (pointer/touch) coordinate to world-space coordinates. */
  worldFromClient(clientX, clientY) {
    const rect = this.canvas.getBoundingClientRect();
    const cx = clientX - rect.left;
    const cy = clientY - rect.top;
    return {
      x: (cx - this.offX) / this.scale,
      y: (cy - this.offY) / this.scale,
    };
  }

  /** Begin a frame: clear, apply DPR + letterbox + shake + zoom transform. Returns the world-space ctx. */
  begin(shakeOffset, zoom = 1) {
    const g = this.ctx;
    const cssW = this.cssW, cssH = this.cssH;
    g.setTransform(this.dpr, 0, 0, this.dpr, 0, 0);
    g.fillStyle = '#05040c';
    g.fillRect(0, 0, cssW, cssH);
    this.drawLetterboxDecor(g, cssW, cssH);
    g.save();
    g.beginPath();
    g.rect(this.offX, this.offY, W * this.scale, H * this.scale);
    g.clip();
    g.translate(cssW / 2, cssH / 2);
    g.scale(zoom, zoom);
    g.translate(-cssW / 2, -cssH / 2);
    g.translate(this.offX + (shakeOffset?.x || 0), this.offY + (shakeOffset?.y || 0));
    g.scale(this.scale, this.scale);
    return g;
  }

  end() {
    this.ctx.restore();
  }

  /**
   * Reset the transform to the plain world<->screen mapping (dpr + letterbox
   * offset + scale), WITHOUT camera shake or attic zoom. Used for HUD/overlay
   * drawing so score/lives/banners never get pushed out of frame by a big
   * screenshake or zoom pulse (they stay screen-locked at a safe inset).
   * Must be called between `begin()` and `end()`; the clip region from
   * `begin()` remains in effect (setTransform doesn't touch the clip path).
   */
  beginOverlay() {
    const g = this.ctx;
    g.setTransform(this.dpr, 0, 0, this.dpr, 0, 0);
    g.translate(this.offX, this.offY);
    g.scale(this.scale, this.scale);
    return g;
  }


  drawLetterboxDecor(g, cssW, cssH) {
    // Ambient side-panel glow (mirrored energy bloom) instead of flat black
    // bars — most visible in landscape/desktop mode.
    const t = (this._t = (this._t || 0) + 0.008);
    const worldW = W * this.scale, worldH = H * this.scale;
    const barW = (cssW - worldW) / 2;
    if (barW > 4) {
      const pulse = 0.5 + 0.5 * Math.sin(t * 1.7);
      const grad = g.createLinearGradient(0, 0, barW, 0);
      grad.addColorStop(0, `rgba(94,230,255,${0.04 + pulse * 0.03})`);
      grad.addColorStop(0.6, `rgba(120,90,255,${0.08 + pulse * 0.05})`);
      grad.addColorStop(1, `rgba(94,230,255,${0.18 + pulse * 0.08})`);
      g.fillStyle = grad;
      g.fillRect(0, 0, barW, cssH);
      const grad2 = g.createLinearGradient(cssW, 0, cssW - barW, 0);
      grad2.addColorStop(0, `rgba(255,94,200,${0.04 + pulse * 0.03})`);
      grad2.addColorStop(0.6, `rgba(120,90,255,${0.08 + pulse * 0.05})`);
      grad2.addColorStop(1, `rgba(255,94,200,${0.18 + pulse * 0.08})`);
      g.fillStyle = grad2;
      g.fillRect(cssW - barW, 0, barW, cssH);
      // soft vertical scan streaks for a mirrored "ambient glow" feel
      g.save();
      g.globalCompositeOperation = 'lighter';
      for (let i = 0; i < 5; i++) {
        const yy = ((t * 90 + i * (cssH / 5)) % (cssH + 200)) - 100;
        g.fillStyle = `rgba(180,220,255,${0.05 + 0.04 * Math.sin(t * 2 + i)})`;
        g.fillRect(0, yy, barW, 60);
        g.fillRect(cssW - barW, yy, barW, 60);
      }
      g.restore();
    }
    const barH = (cssH - worldH) / 2;
    if (barH > 4) {
      g.fillStyle = 'rgba(138,123,255,0.08)';
      g.fillRect(0, 0, cssW, barH);
      g.fillRect(0, cssH - barH, cssW, barH);
    }
  }

  drawBackground(g, t, intensity = 0, beatPhase = 0, tint = null) {
    // Escalation identity: DARK neon that gets more ELECTRIC, never paler.
    // The base fill actively DARKENS toward near-black as intensity climbs
    // (no more "mix toward a pale tint" — that was the bug: it raised mean
    // luminance every tier). Accent elements (grid lines, stars, beat
    // pulses) get punchier/more saturated, but they cover a tiny fraction
    // of screen area so they can't outweigh the darker base. A final
    // multiplicative darkening scrim (bigger at high intensity) guarantees
    // the measured mean luminance never increases tier-over-tier, even
    // with every additive FX layer (aurora/bloom/ribbons) stacked on top.
    const k = clamp01(intensity);
    const grad = g.createLinearGradient(0, 0, 0, H);
    grad.addColorStop(0, mixColor('#0a0820', '#020107', k));
    grad.addColorStop(0.55, mixColor('#0d0b26', '#020106', k));
    grad.addColorStop(1, mixColor('#040309', '#000000', k));
    g.fillStyle = grad;
    g.fillRect(0, 0, W, H);

    // A faint hint of the level tint only very early (k small), fully
    // gone by mid-tier so escalation reads as darkening, not brightening.
    if (tint && k < 0.35) {
      g.save();
      g.globalAlpha = (0.06 * (1 - k / 0.35));
      g.fillStyle = tint;
      g.fillRect(0, 0, W, H);
      g.restore();
    }

    // grid — thin, sparse lines get more saturated/electric with intensity;
    // negligible screen-area coverage so it can't raise the mean luminance.
    const beatBoost = 0.5 + 0.5 * Math.sin(beatPhase * Math.PI * 2);
    g.save();
    g.globalAlpha = (0.035 + k * 0.09) * (0.7 + 0.3 * beatBoost);
    g.strokeStyle = k > 0.6 ? '#ff5ec8' : '#5ee6ff';
    g.lineWidth = 1;
    const gridSize = 60;
    const shift = (t * (12 + k * 30)) % gridSize;
    for (let x = -shift; x <= W; x += gridSize) {
      g.beginPath(); g.moveTo(x, 0); g.lineTo(x, H); g.stroke();
    }
    for (let y = 0; y < H; y += gridSize) {
      g.beginPath(); g.moveTo(0, y); g.lineTo(W, y); g.stroke();
    }
    g.restore();

    g.save();
    g.globalCompositeOperation = 'lighter';
    for (const s of this.stars) {
      if (s.y > H) continue; // outside current dynamic world height
      const tw = 0.5 + 0.5 * Math.sin(t * (0.6 + s.depth) + s.tw);
      // stars get a touch more sparkle but also dimmer baseline at high k
      // (small individual dots — visually reads as "sharper", not brighter overall)
      const a = (0.12 + tw * 0.22 + k * 0.14 * beatBoost) * s.depth * (1 - k * 0.3);
      g.fillStyle = `rgba(180,220,255,${a})`;
      g.beginPath();
      g.arc(s.x, s.y, s.r * (1 + k * 0.5), 0, Math.PI * 2);
      g.fill();
    }
    g.restore();

    // Deep-black darkening scrim: scales up hard with intensity so the
    // scene provably reads darker at high tiers (deeper blacks, isolated
    // bright neon accents) instead of an overall brightness increase.
    // This also re-darkens anything the additive grid/star passes above
    // added, guaranteeing net luminance is monotonically non-increasing.
    if (k > 0.01) {
      g.save();
      g.fillStyle = `rgba(0,0,0,${Math.min(0.6, 0.12 + k * 0.5)})`;
      g.fillRect(0, 0, W, H);
      g.restore();
    }
  }

}

function clamp01(v) { return v < 0 ? 0 : v > 1 ? 1 : v; }

function mixColor(base, tint, amt) {
  if (!tint || amt <= 0) return base;
  const c0 = hexToRgb(base), c1 = hexToRgb(tint);
  const k = Math.min(1, amt);
  const r = Math.round(c0.r + (c1.r - c0.r) * k);
  const gg = Math.round(c0.g + (c1.g - c0.g) * k);
  const b = Math.round(c0.b + (c1.b - c0.b) * k);
  return `rgb(${r},${gg},${b})`;
}
function hexToRgb(hex) {
  const h = hex.replace('#', '');
  const n = parseInt(h.length === 3 ? h.split('').map((c) => c + c).join('') : h, 16);
  return { r: (n >> 16) & 255, g: (n >> 8) & 255, b: n & 255 };
}
