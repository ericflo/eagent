// hud.js — DOM HUD for Rooftop Breakout. Updates text/classes on a speedometer,
// score, multiplier, lives, power chips. Purely DOM; safe to construct anytime.

const POWER_LABEL = {
  wide: 'W I D E',
  laser: 'L A S E R',
  slow: 'S L O W',
  fire: 'F I R E',
  shield: 'S H I E L D',
};

export class Hud {
  constructor(root) {
    this.root = root;
    this.scoreEl = root.querySelector('#hud-score');
    this.multEl = root.querySelector('#hud-mult');
    this.comboEl = root.querySelector('#hud-combo');
    this.livesEl = root.querySelector('#hud-lives');
    this.streakEl = root.querySelector('#hud-streak');
    this.chipsEl = root.querySelector('#hud-chips');
    this.shakeTimer = 0;
  }

  update(s) {
    // s: engine state snapshot {score, mult, combo, lives, bestStreak, powerTimers, paddle}
    if (this.scoreEl) {
      const txt = String(Math.floor(s.score));
      if (this.scoreEl.textContent !== txt) {
        this.scoreEl.textContent = txt;
        this._pop(this.scoreEl);
      }
    }
    if (this.multEl) {
      const txt = '×' + s.mult;
      if (this.multEl.textContent !== txt) {
        this.multEl.textContent = txt;
        this.multEl.style.color = this._multColor(s.mult);
        this._pop(this.multEl);
      }
    }
    if (this.comboEl) {
      this.comboEl.textContent = s.combo > 0 ? `${s.combo} combo` : '';
    }
    if (this.livesEl) {
      this.livesEl.textContent = '♥'.repeat(Math.max(0, s.lives)) + '♡'.repeat(Math.max(0, 4 - Math.max(0, s.lives)));
    }
    if (this.streakEl) {
      const stray = s.bestStreak > 0 ? `roof ${s.bestStreak}s` : '';
      this.streakEl.textContent = stray;
    }
    this._chips(s);
  }

  _multColor(m) {
    const pal = ['#fff', '#7ef0a6', '#66f2ff', '#7aa5ff', '#c77dff', '#ff6ff2'];
    return pal[Math.min(pal.length - 1, m - 1)];
  }

  _chips(s) {
    if (!this.chipsEl) return;
    const active = [];
    if (s.powerTimers?.wide > 0) active.push(['wide', s.powerTimers.wide]);
    if (s.powerTimers?.laser > 0) active.push(['laser', s.powerTimers.laser]);
    if (s.powerTimers?.slow > 0) active.push(['slow', s.powerTimers.slow]);
    if (s.powerTimers?.fire > 0) active.push(['fire', s.powerTimers.fire]);
    if (s.paddle?.shield > 0) active.push(['shield', s.paddle.shield]);
    const key = active.map(a => a[0] + Math.ceil(a[1])).join(',');
    if (key === this._chipKey) return;
    this._chipKey = key;
    this.chipsEl.innerHTML = '';
    for (const [name, val] of active) {
      const span = document.createElement('span');
      span.className = 'chip chip-' + name;
      span.textContent = POWER_LABEL[name] + (name === 'shield' ? ' ×' + val : ' ' + Math.ceil(val) + 's');
      this.chipsEl.appendChild(span);
    }
  }

  _pop(el) {
    if (!el) return;
    el.classList.remove('pop');
    // force reflow so the animation restarts
    void el.offsetWidth;
    el.classList.add('pop');
  }
}
