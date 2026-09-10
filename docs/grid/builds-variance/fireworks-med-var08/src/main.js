// Bootstrap: wires sim + renderer + audio + input, runs the main loop,
// maps sim events to juice, and drives overlay screens.

import { createGame, step, launch, pauseToggle, nextLevel, STATE, LEVEL_COUNT } from './game.js';
import { Renderer } from './renderer.js';
import { createParticles, sparkBurst, chunkBurst, ring, glow, update as updateParticles } from './core/particles.js';
import { createInput } from './input.js';
import { audio } from './audio.js';

const canvas = document.getElementById('game');
const overlay = document.getElementById('overlay');
const overlayTitle = document.getElementById('overlay-title');
const overlaySub = document.getElementById('overlay-sub');
const overlayStats = document.getElementById('overlay-stats');
const overlayBtn = document.getElementById('overlay-btn');
const overlayHint = document.getElementById('overlay-hint');
const btnMute = document.getElementById('btn-mute');
const btnPause = document.getElementById('btn-pause');

const renderer = new Renderer(canvas);
const parts = createParticles();
let game = createGame(0);

// ---------------------------------------------------------------- input
const toField = (px, py) => renderer.toField(px, py, game.field);
const input = createInput(canvas, {
  toField,
  onLaunch: () => { userGesture(); tryLaunch(); },
  onPause: () => { userGesture(); togglePause(); },
  onMute: () => { userGesture(); toggleMute(); },
  onConfirm: () => { userGesture(); overlayAction(); },
});

// Pause / mute buttons
btnPause.addEventListener('click', () => { userGesture(); togglePause(); });
btnMute.addEventListener('click', () => { userGesture(); toggleMute(); });

let muted = false;
function toggleMute() {
  muted = !muted;
  audio.setMuted(muted);
  btnMute.classList.toggle('muted', muted);
  btnMute.textContent = muted ? '🔇' : '🔊';
}

function tryLaunch() {
  if (!game) return;
  if (game.state === STATE.SERVE || game.state === STATE.PLAY) {
    if (launch(game)) audio.launch();
  }
}

function togglePause() {
  if (game.state === STATE.PAUSED) {
    pauseToggle(game);
    showOverlay(false);
  } else if (game.state === STATE.PLAY || game.state === STATE.SERVE) {
    pauseToggle(game);
    showOverlay(true, {
      title: 'PAUSED',
      sub: 'Take a breath. The bricks will wait.',
      btn: 'RESUME',
      action: 'resume',
    });
  }
}

function userGesture() {
  audio.start();
  audio.resume();
}

// ------------------------------------------------------------- overlay
let overlayAction = null;
function showOverlay(show, opts = null) {
  overlay.classList.toggle('hidden', !show);
  if (!opts) return;
  overlayTitle.textContent = opts.title;
  overlaySub.textContent = opts.sub || '';
  overlayStats.textContent = opts.stats || '';
  overlayBtn.textContent = opts.btn || 'PLAY';
  overlayBtn.classList.toggle('hidden', !opts.btn);
  overlayHint.textContent = opts.hint || '';
  overlayAction = opts.action || null;
}

function showMenu() {
  game = createGame(0);
  showOverlay(true, {
    title: 'NEONBRICK',
    sub: 'Break through. Get the ball ABOVE the bricks. Watch the chaos unfold.',
    stats: '',
    btn: 'PLAY',
    hint: 'Drag / mouse to move • Space or tap to launch • Esc/P pause • M mute\n\nPaddle moves up AND down — hit while rising to launch the ball harder.',
    action: 'start',
  });
  game.state = STATE.MENU;
}

function overlayActionHandler() {
  userGesture();
  if (overlayAction === 'start' || overlayAction === 'next' || overlayAction === 'retry' || overlayAction === 'again') {
    if (overlayAction === 'start') {
      game = createGame(0);
    } else if (overlayAction === 'next') {
      nextLevel(game);
    } else if (overlayAction === 'retry') {
      game = createGame(0);
    } else if (overlayAction === 'again') {
      game = createGame(0);
    }
    showOverlay(false);
    tryLaunch();
  } else if (overlayAction === 'resume') {
    togglePause();
  }
}
overlayBtn.addEventListener('click', overlayActionHandler);
overlay.addEventListener('pointerdown', e => { if (e.target === overlay) overlayActionHandler(); });

// --------------------------------------------------------- event → juice
function handleEvents(g) {
  for (const ev of g.events) {
    switch (ev.type) {
      case 'brickBreak': {
        const hue = ev.hue;
        chunkBurst(parts, ev.x, ev.y, { hue, count: ev.chain ? 6 : 12 });
        sparkBurst(parts, ev.x, ev.y, { hue, count: ev.chain ? 8 : 16, speed: 220 });
        ring(parts, ev.x, ev.y, { hue, size: 6, grow: ev.kind === 'exploding' ? 520 : 300, life: 0.4 });
        glow(parts, ev.x, ev.y, { hue, size: 26, life: 0.3 });
        renderer.addPopup(ev.x, ev.y, `+${ev.pts}`, hue);
        if (g.mult.combo >= 3) {
          renderer.addPopup(ev.x, ev.y - 18, `×${g.mult.combo} COMBO`, 55, true);
        }
        if (g.chaos) {
          renderer.addPopup(ev.x, ev.y - 36, 'ABOVE!', 170);
        }
        audio.brickBreak(ev.kind, g.mult.combo, g.chaos);
        break;
      }
      case 'brickReject':
        sparkBurst(parts, ev.x, ev.y, { hue: 220, count: 5, speed: 90, life: 0.3 });
        audio.brickReject(ev.reason);
        break;
      case 'paddleHit':
        sparkBurst(parts, ev.x, ev.y, { hue: 190, count: 6, speed: 120, life: 0.3, grav: 100 });
        audio.paddleHit(ev.lift);
        break;
      case 'wallHit':
        audio.wallHit();
        break;
      case 'powerCatch': {
        glow(parts, g.paddle.x, g.paddle.y, { hue: ev.hue, size: 60, life: 0.5 });
        sparkBurst(parts, g.paddle.x, g.paddle.y, { hue: ev.hue, count: 26, speed: 260, life: 0.7 });
        renderer.addPopup(g.paddle.x, g.paddle.y - 30, ev.label, ev.hue, true);
        audio.powerCatch(ev.hue);
        break;
      }
      case 'launch': audio.launch(); break;
      case 'magnetCatch': audio.magnetCatch(); break;
      case 'ballLost':
        audio.ballLost();
        renderer.addPopup(g.field.width / 2, g.field.height * 0.6, 'BALL LOST', 0, true);
        break;
      case 'lifeLost':
        renderer.addPopup(g.field.width / 2, g.field.height * 0.55, `♥ ${ev.lives} LIVES LEFT`, 0, true);
        break;
      case 'levelClear':
        audio.levelClear();
        // slow-mo feel: particles burst across the field
        for (let i = 0; i < 40; i++) {
          sparkBurst(parts, Math.random() * g.field.width, Math.random() * g.field.height * 0.5,
            { hue: Math.random() * 360, count: 3, speed: 180 });
        }
        showOverlay(true, {
          title: `LEVEL ${g.levelIndex + 1} CLEAR`,
          sub: 'Beautifully done.',
          stats: `Score: ${g.score}\nLives: ${g.lives}`,
          btn: g.levelIndex + 1 >= LEVEL_COUNT ? 'FINAL' : 'NEXT LEVEL',
          hint: '',
          action: 'next',
        });
        break;
      case 'gameOver':
        audio.gameOver();
        showOverlay(true, {
          title: 'GAME OVER',
          sub: 'The bricks won this round.',
          stats: `Final score: ${g.score}\nLevel reached: ${g.levelIndex + 1}`,
          btn: 'RETRY',
          action: 'retry',
        });
        break;
      case 'win':
        audio.levelClear();
        showOverlay(true, {
          title: 'YOU WIN!',
          sub: 'Every brick demolished. Absolute legend.',
          stats: `Final score: ${g.score}`,
          btn: 'PLAY AGAIN',
          action: 'again',
        });
        break;
      case 'levelStart':
        renderer.addPopup(g.field.width / 2, g.field.height / 2, ev.name, 190, true);
        break;
    }
  }
}

// -------------------------------------------------------------- main loop
let last = performance.now();
function frame(now) {
  const dt = Math.min((now - last) / 1000, 0.05);
  last = now;

  // thumbstick drives the paddle directly before the sim step
  input.applyThumbstick(game, dt, toField);

  step(game, dt, input);

  // audio intensity follows the chaos heat
  audio.frame(dt, game.mult.chaosHeat);
  audio.setIntensity(game.mult.chaosHeat);

  handleEvents(game);
  updateParticles(parts, dt);
  renderer.render(game, parts, dt);

  requestAnimationFrame(frame);
}

window.addEventListener('resize', () => renderer.resize());
window.addEventListener('orientationchange', () => setTimeout(() => renderer.resize(), 120));

// stop page scroll/pinch on iOS
document.addEventListener('gesturestart', e => e.preventDefault());
document.addEventListener('touchmove', e => e.preventDefault(), { passive: false });

showMenu();
requestAnimationFrame(frame);
