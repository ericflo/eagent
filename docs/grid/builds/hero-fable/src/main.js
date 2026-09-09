// main.js — bootstrap: canvas sizing (DPR + letterbox), the loop, overlay DOM,
// persistence, and the bridge that turns game events into FX / audio / haptics.

import { createAudio } from './audio.js';
import { createGame, startGame, updateGame, togglePause, loadLevel, TIER_NAMES } from './game.js';
import { createRenderer } from './render.js';
import { createInput } from './input.js';
import {
  createFx, updateFx, pushTrail, pruneTrails, ember, sparks, ring, popup, banner, confetti, screenFlash, shake,
  brickBreakFx, denyFx, bombFx, cascadeFx, paddleHitFx, powerupFx, ballLostFx, levelClearFx, RAINBOW, POPUP_MIN_Y,
} from './fx.js';
import { POWERS } from './balls.js';
import { FIELD, SPEED } from './physics.js';
import { formatScore, clamp } from './util.js';

// ---- persistence -----------------------------------------------------------
const store = {
  get(key, fallback) {
    try {
      const v = localStorage.getItem(`overtop.${key}`);
      return v === null ? fallback : JSON.parse(v);
    } catch {
      return fallback;
    }
  },
  set(key, value) {
    try {
      localStorage.setItem(`overtop.${key}`, JSON.stringify(value));
    } catch {
      /* private mode etc. — ignore */
    }
  },
};

// ---- objects ---------------------------------------------------------------
const canvas = document.getElementById('game');
const renderer = createRenderer(canvas);
const audio = createAudio();
const fx = createFx();
const game = createGame({ highScore: store.get('highScore', 0) });
const input = createInput(canvas, {
  toLogical: renderer.toLogical,
  getPaddle: () => game.paddle,
  getScreen: () => ({ w: renderer.state.cssW, h: renderer.state.cssH }),
});
input.setScheme(store.get('scheme', 'stick'));
audio.setMuted(!!store.get('muted', false));
loadLevel(game); // level 1 as the backdrop behind the title
game.events.length = 0;

const $ = (id) => document.getElementById(id);
const ui = {
  root: $('ui'), title: $('title'), pause: $('pause'), gameover: $('gameover'), pauseBtn: $('btn-pause'),
  mute: $('btn-mute'), scheme: $('btn-scheme'), titleHigh: $('title-high'), goScore: $('go-score'),
  goHigh: $('go-high'), goStats: $('go-stats'),
};

// ---- sizing ----------------------------------------------------------------
function resize() {
  const cssW = window.innerWidth;
  const cssH = window.innerHeight;
  const dpr = clamp(window.devicePixelRatio || 1, 1, 3);
  renderer.resize(cssW, cssH, dpr);
  const s = renderer.state;
  ui.root.style.left = `${s.ox}px`;
  ui.root.style.top = `${s.oy}px`;
  ui.root.style.width = `${FIELD.w * s.scale}px`;
  ui.root.style.height = `${FIELD.h * s.scale}px`;
  document.documentElement.style.setProperty('--s', s.scale);
}
window.addEventListener('resize', resize);
window.addEventListener('orientationchange', resize);
if (window.visualViewport) window.visualViewport.addEventListener('resize', resize);
resize();

// ---- UI --------------------------------------------------------------------
let shownState = null;
function syncUi() {
  if (shownState === game.state) return;
  shownState = game.state;
  ui.title.classList.toggle('hidden', game.state !== 'title');
  ui.pause.classList.toggle('hidden', game.state !== 'paused');
  ui.gameover.classList.toggle('hidden', game.state !== 'gameover');
  ui.pauseBtn.classList.toggle('hidden', game.state !== 'playing' && game.state !== 'levelclear');
  ui.titleHigh.textContent = formatScore(game.highScore);
  if (game.state === 'gameover') {
    ui.goScore.textContent = formatScore(game.scoring.score);
    ui.goHigh.textContent = formatScore(game.highScore);
    ui.goStats.textContent = `best multiplier ×${game.stats.bestMultiplier} · ${game.stats.topBreaks} bricks broken from the attic`;
  }
  refreshToggles();
}
function refreshToggles() {
  ui.mute.textContent = `SOUND: ${audio.muted ? 'OFF' : 'ON'}`;
  ui.scheme.textContent = `TOUCH: ${input.scheme === 'drag' ? 'DRAG' : 'STICK'}`;
}

function begin() {
  audio.resume();
  audio.uiClick();
  input.clear();
  startGame(game);
  fx.banner = null;
}
function toggleMute() {
  audio.setMuted(!audio.muted);
  store.set('muted', audio.muted);
  refreshToggles();
  audio.uiClick();
}
function toggleScheme() {
  input.setScheme(input.scheme === 'drag' ? 'stick' : 'drag');
  store.set('scheme', input.scheme);
  refreshToggles();
  audio.uiClick();
}
function doPause() {
  if (game.state === 'playing' || game.state === 'paused') {
    togglePause(game);
    input.clear();
    audio.uiClick();
  }
}

const gesture = (el, fn) => {
  el.addEventListener('click', (e) => {
    e.preventDefault();
    audio.resume();
    fn();
  });
};
gesture($('btn-start'), begin);
gesture($('btn-restart'), begin);
gesture($('btn-resume'), doPause);
gesture(ui.pauseBtn, doPause);
gesture(ui.mute, toggleMute);
gesture(ui.scheme, toggleScheme);
gesture($('btn-quit'), () => {
  game.state = 'title';
  input.clear();
});
// Whole title / game-over panels are tappable
ui.title.addEventListener('click', (e) => {
  if (e.target.tagName !== 'BUTTON') begin();
});
ui.gameover.addEventListener('click', (e) => {
  if (e.target.tagName !== 'BUTTON') begin();
});
document.addEventListener('visibilitychange', () => {
  if (document.hidden && game.state === 'playing') doPause();
});

const haptic = (ms) => {
  try {
    if (navigator.vibrate) navigator.vibrate(ms);
  } catch {
    /* ignore */
  }
};

// ---- events → juice ---------------------------------------------------------
function handleEvent(e) {
  const tier = game.tier;
  switch (e.type) {
    case 'brickBreak':
      brickBreakFx(fx, e.brick, { tier, fromTop: e.fromTop, points: e.points });
      if (e.cause !== 'bomb') audio.brickBreak(e.brick.type, e.comboIndex, e.fromTop);
      if (e.cause === 'ball') haptic(8);
      break;
    case 'brickDeny':
      denyFx(fx, e.x, e.y, e.kind);
      if (e.kind !== 'laser') audio.brickDeny(e.kind); // blocked laser shots just spark
      break;
    case 'paddleHit':
      paddleHitFx(fx, e.x, e.y, e.powerHit, e.strength, tier);
      audio.paddleHit(e.strength, e.powerHit);
      if (e.powerHit) {
        popup(fx, e.x, e.y - 40, 'POWER HIT', '#fff2a8', 0.9);
        haptic(15);
      }
      break;
    case 'wallHit':
      audio.wallHit();
      if (tier >= 2) sparks(fx, e.x, e.y, '#ffffff', 3 + tier, 160, 0.3, 2);
      break;
    case 'bomb':
      if (!e.nested) {
        bombFx(fx, e.x, e.y, tier, e.count);
        audio.bomb();
        haptic(40);
      } else ring(fx, e.x, e.y, '#ffb347', 120, 0.4, 6);
      break;
    case 'cascade':
      cascadeFx(fx, e.x, e.y, tier);
      popup(fx, e.x, e.y - 30, 'CASCADE!', '#ffffff', 1.4);
      audio.cascade();
      haptic(30);
      break;
    case 'powerup': {
      const def = POWERS[e.kind];
      powerupFx(fx, e.x, e.y, def.color, def.name);
      audio.powerup(e.kind);
      haptic(20);
      break;
    }
    case 'powerupEnd':
      audio.powerupEnd();
      break;
    case 'laser':
      audio.laser();
      sparks(fx, e.x, e.y, '#ff9d9d', 4, 120, 0.2, 2);
      break;
    case 'boost':
      sparks(fx, e.x, e.y, '#ffe14d', 14, 380, 0.5, 3);
      popup(fx, e.x, e.y - 24, 'CHARGED', '#ffe14d', 0.9);
      break;
    case 'ballLost':
      ballLostFx(fx, e.x);
      audio.ballLost();
      haptic(e.last ? 80 : 30);
      break;
    case 'multInc':
      renderer.pulseMultiplier();
      break;
    case 'tierUp':
      banner(fx, `${e.name.toUpperCase()} ×${e.multiplier}`, TIER_NAMES[e.tier] === 'TRANSCENDENT' ? 'you are the attic now' : 'multiplier tier up', '#ffffff', 1.5);
      screenFlash(fx, '#ffffff', 0.15 + e.tier * 0.03);
      shake(fx, 4 + e.tier);
      renderer.pulseMultiplier();
      audio.tierUp(e.tier);
      haptic(25);
      break;
    case 'overtopEnter':
      audio.overtopEnter();
      popup(fx, FIELD.w / 2, POPUP_MIN_Y, 'ON TOP!', '#fff2a8', 1.5); // just under the HUD, above the attic floor
      break;
    case 'overtopExit':
      audio.overtopExit();
      break;
    case 'halo':
      confetti(fx, e.x, e.y, 40, 500);
      popup(fx, e.x, e.y - 30, 'HALO', '#ffffff', 1.2);
      break;
    case 'topHeavy':
      ring(fx, e.x, e.y, '#cfd6e6', 140, 0.5, 6);
      popup(fx, e.x, e.y - 30, 'WRECKING', '#cfd6e6', 1.2);
      break;
    case 'levelStart':
      banner(fx, `LEVEL ${e.number} · ${e.name.toUpperCase()}`, e.hint, '#3fb5ff', 2.6);
      break;
    case 'levelClear':
      levelClearFx(fx);
      audio.levelClear();
      store.set('highScore', game.highScore);
      haptic(60);
      break;
    case 'gameOver':
      audio.gameOver();
      store.set('highScore', game.highScore);
      break;
    case 'launch':
      ring(fx, e.x, e.y, '#ffffff', 50, 0.3, 3);
      break;
    case 'lifeUp':
      popup(fx, FIELD.w / 2, FIELD.h * 0.5, '+1 LIFE', '#ff5cc8', 1.6);
      break;
    case 'slowmo':
      screenFlash(fx, '#4d8dff', 0.18);
      break;
    case 'magnetCatch':
      ring(fx, e.x, e.y, '#c084fc', 60, 0.3, 4);
      break;
    default:
      break;
  }
}

// ---- loop ------------------------------------------------------------------
let last = performance.now();
let lastTier = -1;
let lastOnTop = null;

function frame(now) {
  const dt = Math.min(0.1, Math.max(0, (now - last) / 1000));
  last = now;

  const snap = input.poll();
  if (snap.any) audio.resume();
  if (snap.mute) toggleMute();
  if (snap.pause) doPause();
  if (game.state === 'title' && snap.fire) begin();
  else if (game.state === 'gameover' && (snap.fire || snap.restart)) begin();
  if (game.state === 'playing' && game.time === 0 && snap.fire) {
    // The press that started the run must not also launch the serve ball.
    snap.fire = false;
    snap.fireHeld = false;
  }

  updateGame(game, dt, snap);
  for (const e of game.events) handleEvent(e);
  game.events.length = 0;

  // per-frame ambient FX
  if (game.state === 'playing' || game.state === 'levelclear') {
    for (const b of game.balls) {
      pushTrail(fx, b, game.tier, clamp((b.speed - SPEED.min) / (SPEED.max - SPEED.min), 0, 1));
      if (b.power === 'fire' && !b.stuck) ember(fx, b.x, b.y);
      if (b.halo && fx.rng() < 0.4) ember(fx, b.x, b.y, RAINBOW[((now / 60) | 0) % RAINBOW.length]);
    }
  }
  pruneTrails(fx, game.balls);
  updateFx(fx, dt);

  const active = game.state === 'playing' || game.state === 'levelclear';
  const onTop = active && game.anyOnTop;
  const tier = active ? game.tier : 0;
  if (tier !== lastTier || onTop !== lastOnTop) {
    lastTier = tier;
    lastOnTop = onTop;
    audio.setIntensity(tier, onTop);
  }
  audio.update(dt);

  renderer.draw(game, fx, input, dt, audio.beatPhase || 0);
  syncUi();
  requestAnimationFrame(frame);
}
requestAnimationFrame(frame);

// Debug hook (also used by scripts/e2e.mjs). Harmless in production.
window.__overtop = { game, fx, input, audio, renderer };
