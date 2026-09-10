// Entry point: fixed-timestep loop, wiring, visibility handling.

import { Game } from './game.js';
import { Input } from './input.js';
import { audio } from './audio.js';

const canvas = document.getElementById('game');
const game = new Game(canvas);
const input = new Input(canvas);
game.input = input;
window.__game = game; // debugging hook

// keyboard extras
window.addEventListener('keydown', e => {
  if (e.code === 'KeyP' || e.code === 'Escape') {
    if (game.state === 'play') game.pause();
    else if (game.state === 'pause') game.resume();
  }
  if (e.code === 'KeyM') {
    audio.init(); audio.resume();
    audio.setMuted(!audio.muted);
    document.getElementById('btn-mute').textContent = audio.muted ? '🔇' : '🔊';
  }
  if (e.code === 'Space') {
    if (game.state === 'menu') {
      audio.init(); audio.resume();
      game.state = 'play'; game.ui.showPlay();
    } else if (game.state === 'levelclear') game.nextLevel();
    else if (game.state === 'over') { game.reset(); game.state = 'play'; game.ui.showPlay(); }
  }
});

// pause on tab hidden
document.addEventListener('visibilitychange', () => {
  if (document.hidden) {
    if (game.state === 'play') game.pause();
    audio.ctx && audio.ctx.state === 'running' && audio.ctx.suspend().catch(() => {});
  } else {
    audio.ctx && audio.ctx.resume().catch(() => {});
  }
});

// first user gesture unlocks audio
const unlock = () => { audio.init(); audio.resume(); };
window.addEventListener('pointerdown', unlock, { once: true });
window.addEventListener('keydown', unlock, { once: true });

// fixed timestep loop
const STEP = 1 / 120;
let acc = 0, last = performance.now();
function frame(now) {
  let dt = (now - last) / 1000;
  last = now;
  if (dt > 0.1) dt = 0.1; // clamp after tab switches
  acc += dt;
  let n = 0;
  while (acc >= STEP && n < 8) {
    game.update(STEP);
    acc -= STEP;
    n++;
  }
  game.render();
  requestAnimationFrame(frame);
}
requestAnimationFrame(frame);