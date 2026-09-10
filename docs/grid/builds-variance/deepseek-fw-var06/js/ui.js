// ui.js — DOM screen manager for Rooftop Breakout: menu, level clear, game over,
// pause, mute button, and simple event registration. Pure DOM.

export class UI {
  constructor(root) {
    this.root = root;
    this.screens = {};
    for (const el of root.querySelectorAll('[data-screen]')) {
      this.screens[el.dataset.screen] = el;
    }
    this.events = {};
    this._bind();
  }

  on(name, fn) {
    (this.events[name] ||= []).push(fn);
  }

  _emit(name, payload) {
    for (const fn of (this.events[name] || [])) fn(payload);
  }

  _btn(sel, name) {
    const el = this.root.querySelector(sel);
    if (el) el.addEventListener('click', () => this._emit(name));
  }

  _bind() {
    this._btn('#btn-play', 'play');
    this._btn('#btn-endless', 'endless');
    this._btn('#btn-mute', 'mute');
    this._btn('#btn-pause', 'pause');
    this._btn('#btn-resume', 'resume');
    this._btn('#btn-restart', 'restart');
    this._btn('#btn-next', 'next');
    this._btn('#btn-menu', 'menu');
    this._btn('#btn-menu2', 'menu');

    // keyboard-level enter on menu play etc. handled by game
  }

  show(name) {
    for (const [k, el] of Object.entries(this.screens)) {
      el.classList.toggle('active', k === name);
    }
    if (name === 'game') {
      this.root.querySelector('#pause-btn-wrap')?.classList.add('active');
      this.root.querySelector('#hud')?.classList.add('active');
    } else {
      this.root.querySelector('#pause-btn-wrap')?.classList.remove('active');
      this.root.querySelector('#hud')?.classList.remove('active');
    }
  }

  setPaused(p) {
    const ov = this.screens['pause'];
    if (ov) ov.classList.toggle('active', p);
    this.root.querySelector('#pause-btn-wrap')?.classList.toggle('active', !p);
  }

  setMuteIcon(muted) {
    const btn = this.root.querySelector('#btn-mute');
    if (btn) btn.textContent = muted ? '🔇' : '🔊';
  }

  setLevelClear({ score, bonus, level, mode }) {
    const el = this.screens['levelclear'];
    if (!el) return;
    const modeTxt = mode === 'endless' ? 'ENDLESS' : `LEVEL ${level}`;
    el.querySelector('.lc-title') ? null : null;
    const t = el.querySelector('[data-lc-title]');
    if (t) t.textContent = modeTxt + ' COMPLETE';
    const b = el.querySelector('[data-lc-bonus]');
    if (b) b.textContent = 'ROOFTOP BONUS +' + bonus;
    const s = el.querySelector('[data-lc-score]');
    if (s) s.textContent = 'SCORE ' + score;
  }

  setGameOver({ score, bestStreak, level }) {
    const el = this.screens['gameover'];
    if (!el) return;
    const s = el.querySelector('[data-go-score]');
    if (s) s.textContent = 'SCORE ' + score;
    const st = el.querySelector('[data-go-streak]');
    if (st) st.textContent = 'BEST ROOFTOP STREAK ' + bestStreak + 's';
  }
}
