// UI: HUD, screens, powerup tray, hint, buttons wiring.

import { audio } from './audio.js';

export class UI {
  constructor(game) {
    this.game = game;
    this.$ = id => document.getElementById(id);
    this.el = {
      hud: this.$('hud'), score: this.$('hud-score'), mult: this.$('hud-mult'),
      combo: this.$('hud-combo'), lives: this.$('hud-lives'), levelLabel: this.$('hud-level'),
      rushFill: this.$('rush-fill'), rushWrap: this.$('rush-wrap'), rushLabel: this.$('rush-label'),
      tray: this.$('powerup-tray'), hint: this.$('hint'),
      start: this.$('screen-start'), pause: this.$('screen-pause'),
      levelScr: this.$('screen-level'), over: this.$('screen-over'),
      lvlNum: this.$('lvl-num'), lvlBonus: this.$('lvl-bonus'),
      overScore: this.$('over-score'), overBest: this.$('over-best'),
      hiscoreVal: this.$('hiscore-val'),
    };
    this._bindButtons();
    this.el.hiscoreVal.textContent = this.game.best;
  }

  _bindButtons() {
    const g = this.game;
    this.$('btn-start').addEventListener('click', () => { audio.init(); audio.resume(); g.state = 'play'; this.showPlay(); g.resetBall(); });
    this.$('btn-resume').addEventListener('click', () => g.resume());
    this.$('btn-restart').addEventListener('click', () => { g.reset(); g.state = 'play'; this.showPlay(); });
    this.$('btn-next').addEventListener('click', () => g.nextLevel());
    this.$('btn-again').addEventListener('click', () => { g.reset(); g.state = 'play'; this.showPlay(); });
    this.$('btn-pause').addEventListener('click', () => g.pause());
    const mute = this.$('btn-mute');
    mute.addEventListener('click', () => {
      audio.init(); audio.resume();
      audio.setMuted(!audio.muted);
      mute.textContent = audio.muted ? '🔇' : '🔊';
    });
  }

  updateHUD() {
    const g = this.game;
    this.el.score.textContent = g.score.toLocaleString();
    this.el.mult.textContent = 'x' + g.rushMult;
    this.el.combo.textContent = g.combo;
    this.el.lives.textContent = '♥'.repeat(Math.max(0, Math.min(6, g.lives)));
    this.el.levelLabel.textContent = g.level + 1;
    // tray
    const chips = [];
    if (g.slowT > 0) chips.push(['SLOW', '#c8b4ff', g.slowT.toFixed(1)]);
    if (g.ghostT > 0) chips.push(['GHOST', '#9affee', g.ghostT.toFixed(1)]);
    if (g.bomberArmed) chips.push(['BOMBER', '#ffae42', 'armed']);
    if (g.speedBoostT > 0) chips.push(['SPEED', '#ffd76c', '']);
    const seen = new Set(['ghost']);
    const types = new Set(g.balls.map(b => b.type).filter(t => t !== 'normal'));
    for (const t of types) if (!seen.has(t)) { seen.add(t); chips.push([t.toUpperCase(), g.balls[0]?.color || '#fff', '']); }
    if (g.paddle.wideT > 0) chips.push(['WIDE', '#8fd4ff', Math.ceil(g.paddle.wideT) + 's']);
    if (g.paddle.sticky) chips.push(['STICKY', '#ff6cc4', '']);
    this.el.tray.innerHTML = chips.map(([l, c, t]) =>
      `<div class="pu-chip" style="border-color:${c}"><span style="color:${c}">${l}</span><span class="t">${t}</span></div>`
    ).join('');
  }

  updateRush(rush, mult) {
    this.el.rushFill.style.width = (rush * 100).toFixed(0) + '%';
    this.el.rushWrap.classList.toggle('hot', rush > 0.4);
    this.el.rushLabel.textContent = rush > 0.4 ? 'RUSH x' + mult : 'RUSH';
    if (this._lastMult !== mult || this._lastLives !== this.game.lives) {
      this._lastMult = mult; this._lastLives = this.game.lives;
      this.updateHUD();
    }
  }

  showHint(str) {
    this.el.hint.textContent = str;
    this.el.hint.classList.remove('hidden');
    clearTimeout(this._hintT);
    this._hintT = setTimeout(() => this.el.hint.classList.add('hidden'), 4200);
  }

  hideAll() {
    for (const s of ['start', 'pause', 'levelScr', 'over']) this.el[s].classList.add('hidden');
    this.el.hud.classList.remove('hidden');
  }

  showMenu() {
    this.el.hud.classList.add('hidden');
    this.el.start.classList.remove('hidden');
    for (const s of ['pause', 'levelScr', 'over']) this.el[s].classList.add('hidden');
  }

  showPlay() {
    this.hideAll();
    this._lastMult = -1;
    this.updateHUD();
  }

  showPause() { this.el.pause.classList.remove('hidden'); }

  showLevelClear(level, bonus) {
    this.el.lvlNum.textContent = level + 1;
    this.el.lvlBonus.textContent = `BONUS +${bonus}   SCORE ${this.game.score.toLocaleString()}`;
    this.el.levelScr.classList.remove('hidden');
  }

  showGameOver(score, best) {
    this.el.overScore.textContent = `SCORE ${score.toLocaleString()}`;
    this.el.overBest.innerHTML = score >= best ? '<b>NEW HIGH SCORE!</b>' : `HIGH SCORE <b>${best.toLocaleString()}</b>`;
    this.el.over.classList.remove('hidden');
  }
}