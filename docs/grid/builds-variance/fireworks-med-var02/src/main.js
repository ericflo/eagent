// Entry point: canvas setup, RAF loop with fixed-timestep accumulator,
// input wiring, menu/pause flow.
import { FIELD, WALL, PHYS } from './constants.js';
import { Game } from './game.js';
import { Renderer } from './render.js';
import { UI } from './ui.js';
import { Input } from './input.js';

const canvas = document.getElementById('game');
const renderer = new Renderer(canvas);
const game = new Game();
const ui = new UI(game);
const input = new Input(canvas, () => renderer.scale);

game.renderer = renderer;
game.ui = ui;
game.input = input;

// pointer position in field coords for smoothed paddle follow
let pointerField = { x: FIELD.w / 2, y: FIELD.h - 90 };
game.pointerField = pointerField;

const resize = () => renderer.resize();
window.addEventListener('resize', resize);
window.addEventListener('orientationchange', () => setTimeout(resize, 100));
resize();

// ---------- menus / flow ----------
const show = (id) => ui.show(id);
function toMenu() {
  game.state = 'menu';
  ui.show('screen-start');
  document.getElementById('hud').classList.add('hidden');
  document.getElementById('btn-pause').classList.add('hidden');
}
function startPlay() {
  game.audio.resume();
  ui.hideScreens();      // clear screens first — newRun() shows the level banner right after
  game.newRun();
  game.state = 'playing';
  document.getElementById('hud').classList.remove('hidden');
  document.getElementById('btn-pause').classList.remove('hidden');
}
function pause() {
  if (game.state !== 'playing') return;
  game.state = 'paused';
  ui.setPausedUI(game.audio.muted, input.stick.on);
  show('screen-pause');
}
function resume() {
  if (game.state !== 'paused') return;
  game.state = 'playing';
  ui.hideScreens();
}
let legendReturn = 'screen-start';

document.getElementById('btn-play').addEventListener('click', () => { game.audio.resume(); startPlay(); });
document.getElementById('btn-howto').addEventListener('click', () => { legendReturn = 'screen-start'; show('screen-legend'); });
document.getElementById('btn-legend').addEventListener('click', () => { legendReturn = 'screen-pause'; show('screen-legend'); });
document.getElementById('btn-legend-back').addEventListener('click', () => show(legendReturn));
document.getElementById('btn-resume').addEventListener('click', resume);
document.getElementById('btn-restart').addEventListener('click', () => { startPlay(); });
document.getElementById('btn-again').addEventListener('click', () => { startPlay(); });
document.getElementById('btn-pause').addEventListener('click', pause);
document.getElementById('btn-mute').addEventListener('click', () => {
  ui.setPausedUI(game.audio.toggleMute(), input.stick.on);
});
document.getElementById('btn-stick').addEventListener('click', () => {
  input.toggleStick();
  ui.setPausedUI(game.audio.muted, input.stick.on);
});
document.getElementById('btn-shake').addEventListener('click', () => {
  localStorage.setItem('bt_reducedshake', localStorage.getItem('bt_reducedshake') === '1' ? '0' : '1');
  ui.setPausedUI(game.audio.muted, input.stick.on);
});

// first gesture resumes audio (autoplay policy)
const kick = () => game.audio.resume();
window.addEventListener('pointerdown', kick, { once: false });
window.addEventListener('keydown', kick, { once: false });

// pause on tab hide
document.addEventListener('visibilitychange', () => {
  if (document.hidden && game.state === 'playing') pause();
});

// ---------- loop ----------
let last = performance.now();
let acc = 0;
let fpsSmooth = 60;
function frame(now) {
  requestAnimationFrame(frame);
  let dt = (now - last) / 1000;
  last = now;
  if (dt > 0.25) dt = 0.25; // tab-back spike guard
  fpsSmooth += (1 / Math.max(dt, 1e-3) - fpsSmooth) * 0.02;

  // input-derived pointer target (smoothed)
  if (input.pointer.active) {
    const t = renderer.cssToField(input.pointer.x, input.pointer.y);
    pointerField.x += (t.x - pointerField.x) * Math.min(1, dt * 18);
    pointerField.y += (t.y - pointerField.y) * Math.min(1, dt * 18);
    pointerField.y = Math.max(game.paddle.bandTop, Math.min(game.paddle.bandBottom, pointerField.y));
  }

  // pause key / button events
  if (input.consumePause()) {
    if (game.state === 'playing') pause();
    else if (game.state === 'paused') resume();
  }
  if (input.consumeMuteToggle()) {
    game.audio.toggleMute();
    ui.setPausedUI(game.audio.muted, input.stick.on);
  }

  if (game.state === 'playing') {
    acc += dt;
    let steps = 0;
    while (acc >= PHYS.fixedDt && steps < PHYS.maxSubSteps * 3) {
      // launch handling at sim rate
      if (input.consumeLaunch()) {
        for (const b of game.balls) if (b.stuck) game.launchBall(b);
      }
      game.update(PHYS.fixedDt);
      acc -= PHYS.fixedDt;
      steps++;
    }
    if (steps >= PHYS.maxSubSteps * 3) acc = 0; // don't spiral
  } else {
    acc = 0;
    // menu/pause idle: keep capsules etc frozen; still animate particles
    game.update(0);
    input.consumeLaunch();
  }

  // render
  const mult = game.mult || 1;
  renderer.beginFrame(dt, mult);
  renderer.drawField(WALL, 0);
  if (game.field) {
    renderer.drawBricks(game.field, mult);
  }
  renderer.drawCapsules(game.capsules);
  renderer.drawPaddle(game.paddle, game.paddle.sticky);
  if (game.state === 'playing' || game.state === 'paused') {
    renderer.drawBalls(game.balls, game.ballPower, mult);
  }
  game.particles.draw(renderer.ctx);
  drawPopups(renderer.ctx, game);
  renderer.drawStickVisual();
  renderer.endFrame();
}
function drawPopups(ctx, g) {
  ctx.save();
  ctx.textAlign = 'center';
  for (const p of g.popups) {
    const t = 1 - p.life / p.ttl;
    ctx.globalAlpha = Math.min(1, t * 2);
    ctx.font = 'bold 22px system-ui, sans-serif';
    ctx.fillStyle = p.color;
    ctx.shadowColor = p.color; ctx.shadowBlur = 8;
    ctx.fillText(p.text, p.x, p.y);
    if (p.sub) {
      ctx.font = 'bold 14px system-ui, sans-serif';
      ctx.fillStyle = '#fff';
      ctx.fillText(p.sub, p.x, p.y + 18);
    }
  }
  ctx.shadowBlur = 0; ctx.globalAlpha = 1;
  ctx.restore();
}

// draw idle background field on menu before first run
game.field = null;
toMenu();
requestAnimationFrame(frame);

// debug/test hooks (harmless in production)
window.__game = game;
import('./constants.js').then(m => { window.__powerDefs = m.POWER_DEFS; });
import('./bricks.js').then(m => { window.__brickmod = m; });
import('./entities.js').then(m => { window.__capsule = m.Capsule; });
