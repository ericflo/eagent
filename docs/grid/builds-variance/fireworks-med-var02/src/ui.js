// DOM UI: HUD, overlays, banners, legend content.
import { POWER_DEFS, COLORS, FRENZY } from './constants.js';

const $ = (id) => document.getElementById(id);

export class UI {
  constructor(game) {
    this.game = game;
    this.hud = $('hud');
    this.banner = $('banner');
    this.bannerMain = $('banner-main');
    this.bannerSub = $('banner-sub');
    this._bannerT = null;
    this.buildLegend();
  }
  show(id) {
    document.querySelectorAll('.screen').forEach(s => s.classList.remove('visible'));
    // never let a transient banner (BALL LOST / LEVEL CLEAR) sit on top of a menu
    this.banner.classList.add('hidden');
    if (id) $(id).classList.add('visible');
  }
  hideScreens() { this.show(null); }
  showBanner(main, sub, frenzy) {
    this.bannerMain.textContent = main;
    this.bannerSub.textContent = sub || '';
    this.banner.classList.remove('hidden');
    this.banner.classList.remove('show', 'frenzy');
    void this.banner.offsetWidth; // restart animation
    this.banner.classList.add('show');
    if (frenzy) this.banner.classList.add('frenzy');
    // auto-hide once the 1.6s animation finishes (matches bannerPop in style.css)
    clearTimeout(this._bannerT);
    this._bannerT = setTimeout(() => this.banner.classList.add('hidden'), 1650);
  }
  showLevelBanner(name, hint) {
    this.showBanner(name, hint || '', false);
  }
  showGameOver(g) {
    $('over-score').textContent = g.formatScore(g.score);
    $('over-best').textContent = 'BEST ' + g.formatScore(g.best) + (g.score >= g.best && g.score > 0 ? ' — NEW BEST!' : '');
    const sum = $('over-summary');
    if (sum) sum.textContent = 'LEVEL ' + (g.levelIndex + 1) + ' · ' + (g.bricksDestroyed || 0) + ' BRICKS · MAX ×' + (g.maxMult || 1).toFixed(1);
    this.show('screen-over');
  }
  syncHud(g) {
    $('hud-score').textContent = g.formatScore(g.score);
    $('hud-best').textContent = g.formatScore(g.best);
    $('hud-level').textContent = g.levelIndex + 1;
    $('hud-lives').textContent = '♥'.repeat(g.lives);
    const pct = ((g.mult - 1) / (FRENZY.maxMult - 1)) * 100;
    $('mult-fill').style.width = pct + '%';
    $('mult-label').textContent = '×' + (g.mult < 1.05 ? 1 : g.mult.toFixed(1));
    this.syncPowers(g);
  }
  syncPowers(g) {
    const box = $('power-icons');
    const items = [];
    if (g.ballPower && g.ballPowerTimer > 0) {
      const def = POWER_DEFS[g.ballPower];
      items.push({ label: def.label.slice(0, 4), color: def.color, p: (g.ballPowerTimer / def.dur) * 100 });
    }
    if (g.paddle.wideTimer > 0) items.push({ label: 'WIDE', color: POWER_DEFS.WIDE.color, p: (g.paddle.wideTimer / POWER_DEFS.WIDE.dur) * 100 });
    if (g.paddle.stickyTimer > 0) items.push({ label: 'STCK', color: POWER_DEFS.STICKY.color, p: (g.paddle.stickyTimer / POWER_DEFS.STICKY.dur) * 100 });
    if (g.paddle.shrinkTimer > 0) items.push({ label: 'SHRN', color: POWER_DEFS.SHRINK.color, p: (g.paddle.shrinkTimer / POWER_DEFS.SHRINK.dur) * 100 });
    if (g.slowTimer > 0) items.push({ label: 'SLOW', color: POWER_DEFS.SLOWMO.color, p: (g.slowTimer / POWER_DEFS.SLOWMO.dur) * 100 });
    const sig = items.map(i => i.label + Math.round(i.p)).join('|');
    if (sig !== this._pwSig) {
      this._pwSig = sig;
      box.innerHTML = items.map(i =>
        `<div class="power-icon" style="--c:${i.color};--p:${i.p.toFixed(0)}"><i>${i.label}</i></div>`).join('');
    }
  }
  setPausedUI(muted, stickOn) {
    $('btn-mute').textContent = 'SOUND: ' + (muted ? 'OFF' : 'ON');
    $('btn-stick').textContent = 'THUMBSTICK: ' + (stickOn ? 'ON' : 'OFF');
    $('btn-shake').textContent = 'REDUCED SHAKE: ' + (localStorage.getItem('bt_reducedshake') === '1' ? 'ON' : 'OFF');
  }

  buildLegend() {
    const rows = [
      ['STD', 'Standard', 'One hit, one brick.'],
      ['ANGLE', 'Angle-Lock', 'Only breaks on shallow/glancing hits — direct hits clank off.'],
      ['VELOCITY', 'Velocity-Lock', 'Only a fast ball breaks it. Slow hits bounce.'],
      ['PHASE', 'Phase', 'Blinks solid/ghost. Only breakable while solid.'],
      ['EXPLOSIVE', 'Explosive', 'Blows up neighbours in a radius. Chain reactions!'],
      ['MOVER', 'Mover', 'Slides side to side. Time your shot.'],
      ['REGEN', 'Regen', 'Grows back after 6s unless you cleared its neighbours.'],
      ['UNBREAK', 'Support', 'Indestructible. Used for structure.'],
      ['ARMOR', 'Armored', 'Rare. Takes 3 hits, cracks visibly.'],
    ];
    const mk = (parent) => {
      parent.innerHTML = rows.map(([t, name, desc]) => {
        const c = COLORS.brick[t];
        const glyph = { ANGLE: '◢', VELOCITY: '⚡', PHASE: '◐', EXPLOSIVE: '✸', MOVER: '⇄', REGEN: '♻', UNBREAK: '✕', ARMOR: '3' }[t] || '';
        return `<div class="leg-row"><div class="leg-swatch" style="--c:${c}" data-glyph="${glyph}"></div><div><b>${name}</b><small>${desc}</small></div></div>`;
      }).join('');
    };
    document.querySelectorAll('.legend-bricks').forEach(mk);
    $('legend-powers') && 0;
    const pow = $('screen-legend').querySelector('.legend-powers');
    if (pow) pow.innerHTML = Object.entries(POWER_DEFS).map(([k, d]) =>
      `<div class="leg-row"><div class="leg-dot" style="--c:${d.color}"></div><div><b>${d.label}</b><small>${{
        FIRE: 'Ball pierces standard bricks, burning a line.',
        HEAVY: 'Slow to fall, mighty: breaks velocity & angle locks at any speed/angle.',
        SPLIT: 'Each brick hit spawns a short-lived extra ball.',
        GHOST: 'Passes through phase bricks whether solid or not.',
        WIDE: 'Wider paddle, 14s.',
        STICKY: 'Ball sticks to paddle; tap/space to re-launch aimed.',
        SLOWMO: 'Everything slows down briefly — great for threading gaps.',
        LIFE: 'Extra life.',
        SHRINK: 'Careful: shrinks the paddle. Not everything is a gift.',
      }[k]}</small></div></div>`).join('');
  }
}
