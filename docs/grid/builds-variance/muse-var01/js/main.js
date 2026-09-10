import { Game } from './game.js';
const canvas = document.getElementById('game');
const stage = document.getElementById('stage');
const game = new Game(canvas, stage);
// expose for debugging / automated checks
window.__game = game;
requestAnimationFrame(t => game.frame(t));
