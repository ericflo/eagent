// main.js — bootstraps the game. Loads UI, renderer, starts the loop.

import { Renderer } from './renderer.js';
import { Game } from './game.js';
import { UI } from './ui.js';

function boot() {
  const ui = new UI(document);
  const canvas = document.getElementById('game');
  const renderer = new Renderer(canvas, {});
  const game = new Game(canvas, ui, { renderer });
  // expose for debugging
  window.__game = game;
  ui.setMuteIcon(game.audio.muted);
}

if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', boot);
} else {
  boot();
}
