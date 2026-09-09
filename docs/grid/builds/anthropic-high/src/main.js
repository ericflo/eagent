// main.js — bootstrap: canvas sizing (devicePixelRatio + letterbox), fixed-timestep loop.

import { Game, W, H, FIELD } from './game.js';
import { Input } from './input.js';
import { render, getButtons } from './render.js';
import { storage } from './util.js';
import audio from './audio.js';
import fx from './fx.js';

const canvas = document.getElementById('game');
const ctx = canvas.getContext('2d', { alpha: false });

const game = new Game();
game.showSettings = false;
audio.setMuted(game.muted);

// ---------------------------------------------------------------- viewport

const view = { scale: 1, offX: 0, offY: 0, dpr: 1, cssW: 0, cssH: 0 };

function resize() {
  const dpr = Math.min(globalThis.devicePixelRatio || 1, 3);
  const cssW = Math.max(1, Math.floor(window.innerWidth));
  const cssH = Math.max(1, Math.floor(window.innerHeight));
  view.dpr = dpr;
  view.cssW = cssW;
  view.cssH = cssH;
  view.scale = Math.min(cssW / W, cssH / H);
  view.offX = (cssW - W * view.scale) / 2;
  view.offY = (cssH - H * view.scale) / 2;
  canvas.width = Math.floor(cssW * dpr);
  canvas.height = Math.floor(cssH * dpr);
  canvas.style.width = `${cssW}px`;
  canvas.style.height = `${cssH}px`;
}
window.addEventListener('resize', resize);
window.addEventListener('orientationchange', () => setTimeout(resize, 60));
resize();

function toLogical(clientX, clientY) {
  const rect = canvas.getBoundingClientRect();
  const x = (clientX - rect.left - view.offX) / view.scale;
  const y = (clientY - rect.top - view.offY) / view.scale;
  return { x, y };
}

// ---------------------------------------------------------------- input

const input = new Input(canvas, {
  field: FIELD,
  toLogical,
  isDragMode: () => game.dragMode,
  isMenu: () => game.showSettings || ['title', 'gameover', 'paused', 'serve'].includes(game.state),
  getPaddle: () => game.paddle,
  onFire: () => {
    audio.unlock();
    if (game.showSettings) return;
    game.onFire();
  },
  onPause: () => {
    audio.unlock();
    if (game.showSettings) { closeSettings(); return; }
    game.togglePause();
  },
  onButton: (id) => {
    audio.unlock();
    switch (id) {
      case 'pause':
        game.togglePause();
        break;
      case 'mute':
      case 'toggleMute':
        game.muted = audio.toggleMute();
        storage.set('overtop.muted', game.muted);
        break;
      case 'settings':
        game.showSettings = true;
        if (game.state === 'playing' || game.state === 'serve') game.togglePause();
        break;
      case 'toggleDrag':
        game.dragMode = !game.dragMode;
        storage.set('overtop.dragMode', game.dragMode);
        break;
      case 'toggleFx':
        fx.toggleLite();
        break;
      case 'musicVolDown':
        audio.setMusicVolume(Math.round((audio.musicVolume - 0.1) * 10) / 10);
        break;
      case 'musicVolUp':
        audio.setMusicVolume(Math.round((audio.musicVolume + 0.1) * 10) / 10);
        break;
      case 'sfxVolDown':
        audio.setSfxVolume(Math.round((audio.sfxVolume - 0.1) * 10) / 10);
        audio.play('ui');
        break;
      case 'sfxVolUp':
        audio.setSfxVolume(Math.round((audio.sfxVolume + 0.1) * 10) / 10);
        audio.play('ui');
        break;
      case 'resetHints':
        game.resetHints();
        game.showToast('HINTS RESET', 190);
        audio.play('ui');
        break;
      case 'closeSettings':
        closeSettings();
        break;
      default:
        break;
    }
  },
});

function closeSettings() {
  game.showSettings = false;
  if (game.state === 'paused') game.togglePause();
}

// ---------------------------------------------------------------- loop

const STEP = 1 / 120;
const MAX_STEPS = 40;
let acc = 0;
let last = performance.now();
let renderTime = 0;

function frame(now) {
  const raw = (now - last) / 1000;
  last = now;
  const dt = Math.min(0.2, Math.max(0, raw));
  renderTime += dt;
  acc += dt;

  input.setButtons(getButtons(game));
  game.setPaddleCommand(input.getCommand());

  let steps = 0;
  while (acc >= STEP && steps < MAX_STEPS) {
    game.update(STEP);
    acc -= STEP;
    steps++;
  }
  if (steps >= MAX_STEPS) acc = 0; // never spiral

  ctx.setTransform(1, 0, 0, 1, 0, 0);
  ctx.fillStyle = '#05060d';
  ctx.fillRect(0, 0, canvas.width, canvas.height);
  const s = view.scale * view.dpr;
  ctx.setTransform(s, 0, 0, s, view.offX * view.dpr, view.offY * view.dpr);
  ctx.beginPath();
  ctx.rect(0, 0, W, H);
  ctx.clip();
  render(ctx, game, input, renderTime);

  requestAnimationFrame(frame);
}

document.addEventListener('visibilitychange', () => {
  if (document.hidden) {
    // Tab hidden -> auto pause, so you never come back to a dead ball.
    if (game.state === 'playing' || game.state === 'serve' || game.state === 'levelclear') {
      game.togglePause();
    }
  } else {
    last = performance.now();
    acc = 0;
  }
});

requestAnimationFrame(frame);

// Debug / automation hooks.
globalThis.__game = game;
globalThis.__input = input;
globalThis.__fx = fx;
globalThis.__audio = audio;
globalThis.__view = view;
globalThis.__toLogical = toLogical;
