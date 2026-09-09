// Entry point: wires input, game loop, and clears bricks check.
import { Game } from './game.js';
import { Input } from './input.js';

const canvas = document.getElementById('game');
const game = new Game(canvas);
window.game = game;
game.input = new Input(canvas, game);

// wrap updateBalls to run clear-check each frame while playing
const orig = game.updateBalls.bind(game);
game.updateBalls = dt => { orig(dt); game.checkClear(); };
