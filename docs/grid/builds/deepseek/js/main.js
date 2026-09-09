// Entry point: wire up the canvas, the game, and the DOM buttons.
import { Game } from './game.js';

const canvas = document.getElementById('game');
const game = new Game(canvas);

const btnPause = document.getElementById('btn-pause');
const btnMute = document.getElementById('btn-mute');

// Keep the icon buttons in sync with the game state.
function updateButtons() {
  btnMute.textContent = game.audio.muted ? '🔇' : '🔊';
  btnPause.textContent = game.state === 'paused' ? '▶' : '❚❚';
}

btnPause.addEventListener('click', () => {
  if (game.state === 'playing') game._setState('paused');
  else if (game.state === 'paused') game._setState('playing');
  updateButtons();
});

btnMute.addEventListener('click', () => {
  game._toggleMute();
  updateButtons();
});

// State changes come from many places (keys, pointer, game logic), so poll.
setInterval(updateButtons, 500);

// Expose for debugging / automated testing.
window.__game = game;
