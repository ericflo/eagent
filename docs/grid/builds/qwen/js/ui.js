// ui.js — DOM HUD and overlay screens.
// Screens: MENU (title, subtitle, "TAP / CLICK / ENTER TO START", controls,
// high score), PAUSED (resume/restart/menu + key hints), LEVELCLEAR splash
// (2.2s, +500×level bonus), GAMEOVER (final/best + NEW BEST! tag).
// Also: score, multiplier, lives, level, power-up timer icons (radial ticks),
// "gamepad detected" toast, first-launch tip, portrait rotate hint.

import { FIELD, STORAGE_KEYS, POWERUPS } from './config.js';

function lsGet(key, fallback) {
  try {
    const v = localStorage.getItem(key);
    return v === null ? fallback : v;
  } catch (e) { return fallback; }
}
function lsSet(key, value) {
  try { localStorage.setItem(key, String(value)); } catch (e) { /* private mode */ }
}

export class UI {
  constructor(root, canvas, callbacks) {
    this.root = root;
    this.canvas = canvas;
    this.cb = callbacks || {};
    this.el = {};
    this.muted = lsGet('odbreakout.muted', '0') === '1';
    this._build();
    this._bind();
    this.hideAllScreens();
    this._isPortrait = this._checkPortrait();
    window.addEventListener('resize', () => this._onResize());
    window.addEventListener('orientationchange', () => this._onResize());
  }

  _checkPortrait() {
    return typeof window.matchMedia === 'function'
      ? window.matchMedia('(orientation: portrait)').matches
      : window.innerHeight > window.innerWidth;
  }

  _onResize() {
    const p = this._checkPortrait();
    if (p !== this._isPortrait) {
      this._isPortrait = p;
      this.el.rotateHint.style.display = p ? 'flex' : 'none';
    }
  }

  _build() {
    const r = this.root;

    // --- HUD ---
    this.el.hud = document.createElement('div');
    this.el.hud.className = 'hud';
    this.el.hud.innerHTML = `
      <div class="hud-top">
        <div class="hud-score"><span class="hud-label">SCORE</span><span id="uiScore">0</span></div>
        <div class="hud-mul" id="uiMul" hidden><span class="hud-mul-x" id="uiMulX">x2</span></div>
        <div class="hud-right">
          <div class="hud-lives" id="uiLives"></div>
          <div class="hud-level" id="uiLevel">L1</div>
        </div>
      </div>
      <div class="hud-od" id="uiOD"><div class="hud-od-fill" id="uiODFill"></div><span class="hud-od-txt">OVERDRIVE</span></div>
      <div class="hud-powerups" id="uiPups"></div>`;
    r.appendChild(this.el.hud);

    // --- Tip (once per session-ish, localStorage flag) ---
    this.el.tip = document.createElement('div');
    this.el.tip.className = 'tip';
    this.el.tip.textContent = 'Slam UP to launch! Send it OVER the bricks!';
    this.el.tip.hidden = true;
    r.appendChild(this.el.tip);

    // --- Toast ---
    this.el.toast = document.createElement('div');
    this.el.toast.className = 'toast';
    this.el.toast.textContent = 'Gamepad detected';
    this.el.toast.hidden = true;
    r.appendChild(this.el.toast);

    // --- Screens ---
    const screen = (cls, html) => {
      const d = document.createElement('div');
      d.className = 'screen ' + cls;
      d.innerHTML = html;
      d.hidden = true;
      r.appendChild(d);
      return d;
    };

    this.el.menu = screen('menu', `
      <div class="menu-box">
        <h1 class="title">OVERDRIVE<br>BREAKOUT</h1>
        <p class="subtitle">Send the ball OVER the bricks. Ride the overdrive.</p>
        <div class="blink start-hint">TAP / CLICK / ENTER TO START</div>
        <p class="best">BEST <span id="menuBest">0</span></p>
        <div class="controls">
          <span><b>MOUSE / THUMB</b> move paddle & up-down</span>
          <span><b>CLICK / TAP (2 FINGERS)</b> launch & hold</span>
          <span><b>A D / ← →</b> move &nbsp; <b>W S / ↑ ↓</b> up-down &nbsp; <b>SPACE</b> launch/hold</span>
          <span><b>P</b> pause &nbsp; <b>M</b> mute &nbsp; <b>R</b> restart level</span>
        </div>
      </div>`);

    this.el.paused = screen('paused', `
      <div class="menu-box small">
        <h2 class="title2">PAUSED</h2>
        <button class="btn primary" data-act="resume">RESUME</button>
        <button class="btn" data-act="restart">RESTART LEVEL</button>
        <button class="btn" data-act="menu">MAIN MENU</button>
        <p class="keys">P resume &nbsp; R restart &nbsp; M mute</p>
      </div>`);

    this.el.levelClear = screen('levelclear', `
      <div class="splash">
        <h2 class="title2 splash-title" id="lcTitle">LEVEL 1 CLEAR</h2>
        <p class="splash-bonus" id="lcBonus">+500</p>
      </div>`);

    this.el.gameover = screen('gameover', `
      <div class="menu-box">
        <h2 class="title2 gameover-title">GAME OVER</h2>
        <p class="final">SCORE <span id="goScore">0</span></p>
        <p class="best">BEST <span id="goBest">0</span></p>
        <div class="newbest" id="goNew" hidden>NEW BEST!</div>
        <div class="blink start-hint">TAP / CLICK / ENTER FOR MENU</div>
      </div>`);

    // --- Touch buttons (pause + mute), big touch targets ---
    this.el.touchBtns = document.createElement('div');
    this.el.touchBtns.className = 'touch-btns';
    this.el.touchBtns.innerHTML = `
      <button class="tbtn" id="btnMute" aria-label="mute">🔊</button>
      <button class="tbtn" id="btnPause" aria-label="pause">⏸</button>`;
    r.appendChild(this.el.touchBtns);

    // --- Portrait rotate hint ---
    this.el.rotateHint = document.createElement('div');
    this.el.rotateHint.className = 'rotate-hint';
    this.el.rotateHint.innerHTML = '↻ &nbsp;ROTATE FOR BEST EXPERIENCE';
    this.el.rotateHint.style.display = this._isPortrait ? 'flex' : 'none';
    r.appendChild(this.el.rotateHint);

    // Cache ids
    this.el.score = document.getElementById('uiScore');
    this.el.mul = document.getElementById('uiMul');
    this.el.mulX = document.getElementById('uiMulX');
    this.el.lives = document.getElementById('uiLives');
    this.el.level = document.getElementById('uiLevel');
    this.el.od = document.getElementById('uiOD');
    this.el.odFill = document.getElementById('uiODFill');
    this.el.pups = document.getElementById('uiPups');
    this.el.menuBest = document.getElementById('menuBest');
    this.el.goScore = document.getElementById('goScore');
    this.el.goBest = document.getElementById('goBest');
    this.el.goNew = document.getElementById('goNew');
    this.el.lcTitle = document.getElementById('lcTitle');
    this.el.lcBonus = document.getElementById('lcBonus');
    this.el.btnMute = document.getElementById('btnMute');
    this.el.btnPause = document.getElementById('btnPause');
    this._lastLives = -1;
    this._lastScore = -1;
    this._lastMul = -1;
    this._lastLevel = -1;
  }

  _bind() {
    const act = (fn) => (e) => {
      if (e) { e.preventDefault(); e.stopPropagation(); }
      if (this.cb.onUI) this.cb.onUI(fn);
    };
    this.el.paused.addEventListener('click', (e) => {
      const b = e.target.closest('[data-act]');
      if (b && this.cb.onUI) {
        e.preventDefault();
        this.cb.onUI(b.dataset.act);
      }
    });
    this.el.btnPause.addEventListener('click', act('pause'));
    this.el.btnPause.addEventListener('touchstart', (e) => { e.preventDefault(); e.stopPropagation(); }, { passive: false });
    this.el.btnMute.addEventListener('click', act('mute'));
    this.el.btnMute.addEventListener('touchstart', (e) => { e.preventDefault(); e.stopPropagation(); }, { passive: false });
  }

  // ---------------------------------------------------------------- screens

  hideAllScreens() {
    for (const d of [this.el.menu, this.el.paused, this.el.levelClear, this.el.gameover]) d.hidden = true;
    this.el.hud.hidden = true;
  }

  showMenu(best) {
    this.hideAllScreens();
    this.el.menuBest.textContent = Math.floor(best).toLocaleString();
    this.el.menu.hidden = false;
  }

  showPaused() {
    this.hideAllScreens();
    this.el.hud.hidden = false;
    this.el.paused.hidden = false;
  }

  showLevelClear(level, bonus) {
    this.el.lcTitle.textContent = `LEVEL ${level} CLEAR`;
    this.el.lcBonus.textContent = `+${Math.floor(bonus).toLocaleString()}`;
    this.hideAllScreens();
    this.el.hud.hidden = false;
    this.el.levelClear.hidden = false;
  }

  showGameOver(score, best, newBest) {
    this.hideAllScreens();
    this.el.goScore.textContent = Math.floor(score).toLocaleString();
    this.el.goBest.textContent = Math.floor(best).toLocaleString();
    this.el.goNew.hidden = !newBest;
    this.el.gameover.hidden = false;
  }

  showHUD() {
    this.hideAllScreens();
    this.el.hud.hidden = false;
  }

  showTip() {
    this.el.tip.hidden = false;
    clearTimeout(this._tipTO);
    this._tipTO = setTimeout(() => { this.el.tip.hidden = true; }, 4200);
  }

  dismissTip() { this.el.tip.hidden = true; }

  showToast(msg) {
    this.el.toast.textContent = msg;
    this.el.toast.hidden = false;
    clearTimeout(this._toastTO);
    this._toastTO = setTimeout(() => { this.el.toast.hidden = true; }, 2500);
  }

  // -------------------------------------------------------------------- HUD

  setMuted(m) {
    this.muted = m;
    this.el.btnMute.textContent = m ? '🔇' : '🔊';
    lsSet('odbreakout.muted', m ? '1' : '0');
  }

  update(score, multiplier, lives, level, meter) {
    if (score !== this._lastScore) {
      this.el.score.textContent = Math.floor(score).toLocaleString();
      this._lastScore = score;
    }
    if (multiplier !== this._lastMul) {
      this.el.mulX.textContent = 'x' + multiplier;
      this.el.mul.hidden = multiplier <= 1;
      this.el.mul.classList.toggle('hot', multiplier >= 8);
      this._lastMul = multiplier;
    }
    if (lives !== this._lastLives) {
      this.el.lives.innerHTML = '';
      for (let i = 0; i < lives; i++) {
        const s = document.createElement('span');
        s.className = 'life';
        s.textContent = '▬';
        this.el.lives.appendChild(s);
      }
      this._lastLives = lives;
    }
    if (level !== this._lastLevel) {
      this.el.level.textContent = 'L' + level;
      this._lastLevel = level;
    }
    const k = clamp01(meter / 100);
    this.el.odFill.style.width = (k * 100).toFixed(1) + '%';
    this.el.od.style.setProperty('--odk', k.toFixed(3));
    this.el.od.classList.toggle('max', meter >= 99.9);
  }

  // Power-up timer icons (bottom-right). activePups: {TYPE: {remaining, total}}
  updatePowerups(active) {
    const names = new Set(Object.keys(active));
    for (const el of [...this.el.pups.children]) {
      if (!names.has(el.dataset.type)) el.remove();
    }
    for (const [type, info] of Object.entries(active)) {
      let el = this.el.pups.querySelector(`[data-type="${type}"]`);
      if (!el) {
        el = document.createElement('div');
        el.className = 'pup-icon';
        el.dataset.type = type;
        const st = POWERUPS.TYPES[type] || { label: '?', color: '#fff' };
        el.style.setProperty('--pup-color', st.color);
        el.innerHTML = `<svg viewBox="0 0 40 40" class="pup-ring"><circle cx="20" cy="20" r="17" class="pup-bg"/><circle cx="20" cy="20" r="17" class="pup-fg"/></svg><span class="pup-letter">${st.label}</span>`;
        this.el.pups.appendChild(el);
      }
      const frac = info.total > 0 ? clamp01(info.remaining / info.total) : 1;
      const fg = el.querySelector('.pup-fg');
      const C = 2 * Math.PI * 17;
      fg.style.strokeDasharray = C;
      fg.style.strokeDashoffset = C * (1 - frac);
    }
  }
}

function clamp01(v) { return v < 0 ? 0 : (v > 1 ? 1 : v); }