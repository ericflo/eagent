// src/main.js — boot, canvas, resize, RAF loop, scene wiring, debug hooks.
import { Renderer } from './core/renderer.js';
import { Input } from './core/input.js';
import { Game } from './game/game.js';
import { W, H } from './core/constants.js';
import { buildFallbackLevel } from './game/fallback.js';
import { makeBall } from './game/ball.js';

const errors = [];
window.addEventListener('error', (e) => {
  errors.push({ msg: e.message, stack: e.error?.stack || null, t: performance.now() });
});
window.addEventListener('unhandledrejection', (e) => {
  errors.push({ msg: 'unhandledrejection: ' + (e.reason?.message || e.reason), stack: e.reason?.stack || null, t: performance.now() });
});

const canvas = document.getElementById('game');
const renderer = new Renderer(canvas);
Input.attach(canvas, renderer);

const game = new Game(renderer);
game.errors = errors;

// seed a little attract-mode demo content for the title screen
seedAttractMode(game);

window.GAME = game;
game.errors = errors;

const gate = document.getElementById('gate');
function unlockAudio() {
  game.audio?.init?.();
  gate.classList.add('hidden');
  gate.removeEventListener('pointerdown', unlockAudio);
}
gate.addEventListener('pointerdown', unlockAudio, { once: true });

// title/game-over/level-clear tap-anywhere handling
canvas.addEventListener('pointerdown', (e) => {
  game.audio?.init?.();
  if (game.scene === 'title') {
    game.startGame();
  } else if (game.scene === 'gameOver') {
    game.startGame();
  } else if (game.scene === 'paused') {
    game.scene = 'playing';
  }
});

let last = performance.now();
let hadFatalError = false;

function frame(now) {
  requestAnimationFrame(frame);
  const dt = Math.min(0.05, Math.max(0, (now - last) / 1000));
  last = now;
  try {
    game.update(dt);
    const g = renderer.begin(game.fx?.shakeOffset, game.cameraZoom || 1);
    game.draw(g, game.time);
    game.fx?.drawPost?.(g, W, H);
    renderer.end();
  } catch (err) {
    errors.push({ msg: 'frame: ' + (err?.message || err), stack: err?.stack || null, t: now });
    if (!hadFatalError) {
      hadFatalError = true;
      console.error('[AtticBreaker] caught frame error (loop continues):', err);
    }
  }
}
requestAnimationFrame(frame);

document.addEventListener('visibilitychange', () => {
  if (document.hidden && game.scene === 'playing') game.scene = 'paused';
});
window.addEventListener('blur', () => {
  if (game.scene === 'playing') game.scene = 'paused';
});

// ---------------------------------------------------------------------
// Optional scripted autoplay for automated screenshots/tests: add
// ?autoplay=1 to the URL. A decent attract/demo AI: tracks the ball
// nearest to falling past the paddle, moves toward it on both axes, times
// occasional uppercuts (moving up right as it connects), and launches
// stuck balls. Used by automated tests and the title attract mode.
const params = new URLSearchParams(location.search);
if (params.get('autoplay') === '1') {
  gate.classList.add('hidden');
  game.audio?.init?.();
  game.startGame();
  let t = 0;
  let forcedAttic = false;
  let uppercutCooldown = 0;
  setInterval(() => {
    if (window.__pauseAutoplay) return;
    t += 1 / 30;
    if (game.scene === 'gameOver') { game.startGame(); return; }
    if (game.scene === 'levelClear') return;
    if (game.scene !== 'playing') return;

    // pick the "most urgent" ball: lowest on screen (closest to the death
    // line) among balls not stuck to the paddle, falling toward the paddle.
    let target = null;
    for (const b of game.balls) {
      if (b.stuckToPaddle) continue;
      if (!target || b.y > target.y) target = b;
    }
    if (!target) target = game.balls[0];

    uppercutCooldown = Math.max(0, uppercutCooldown - 1 / 30);
    if (target) {
      const paddle = game.paddle;
      // lead the ball's X by its horizontal velocity so we arrive in time.
      const timeToArrive = target.vy > 10 ? Math.max(0, (paddle.y - target.y) / target.vy) : 0.3;
      const leadX = target.x + target.vx * Math.min(0.5, timeToArrive) * 0.6;
      Input.intent.x = leadX + Math.sin(t * 3) * 12;

      const descending = target.vy > 30;
      const closeToPaddle = target.y > paddle.y - 260;
      if (descending && closeToPaddle && uppercutCooldown <= 0 && Math.random() < 0.35) {
        // uppercut: snap the paddle UP right as the ball is about to land,
        // then let it settle back down (mimics a human timing a hit).
        Input.intent.y = Math.max(1150, paddle.y - 90);
        uppercutCooldown = 0.9;
      } else {
        Input.intent.y = clamp(target.y + 40, paddle.y - 20, paddle.y + 20);
      }
      Input.intent.launch = true;
    } else {
      Input.intent.launch = true;
    }
    Input.intent.usingTouch = false;
    if (!forcedAttic && t > 4 && t < 4.1) {
      forcedAttic = true;
      game.debug.forceAttic();
    }
    if (t > 6 && game.balls.length < 4 && Math.random() < 0.01) game.debug.addBall();
  }, 1000 / 30);
}

function clamp(v, lo, hi) { return v < lo ? lo : v > hi ? hi : v; }


function seedAttractMode(game) {
  const def = buildFallbackLevel(1);
  game.bricks = def.bricks;
  game.balls = [
    makeBall(300, 700, 260, 340, {}),
    makeBall(650, 900, -300, 260, {}),
  ];
}
