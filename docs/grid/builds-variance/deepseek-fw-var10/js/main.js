import { Game } from "./game.js";
import * as S from "./audio.js";
import { COLS } from "./levels.js";

const canvas = document.getElementById("game");
const hud = document.getElementById("hud");
const scoreEl = document.getElementById("score");
const bestEl = document.getElementById("best");
const livesEl = document.getElementById("lives");
const levelEl = document.getElementById("level-label");
const comboPill = document.getElementById("combo-pill");
const comboCount = document.getElementById("combo-count");
const comboMult = document.getElementById("combo-mult");
const roofBanner = document.getElementById("roof-banner");
const btnPause = document.getElementById("btn-pause");
const btnMute = document.getElementById("btn-mute");
const touchHint = document.getElementById("touch-hint");
const btnGotIt = document.getElementById("btn-gotit");

// audio init happens on first gesture (autoplay policy)
let audioOK = false;
function ensureAudio() {
  if (!audioOK) {
    audioOK = S.initAudio();
    S.resumeAudio();
  }
}

const game = new Game(canvas, {
  onStats(st) {
    if (scoreEl) scoreEl.textContent = st.score.toLocaleString();
    if (bestEl) bestEl.textContent = Math.max(st.best, st.score).toLocaleString();
    if (livesEl) livesEl.textContent = "❤".repeat(Math.max(0, st.lives)) + "🖤".repeat(Math.max(0, 3 - st.lives));
    if (levelEl) levelEl.textContent = "LV " + st.level;
    if (roofBanner) roofBanner.classList.toggle("hidden", !st.roof);
    if (comboPill) comboPill.classList.toggle("hidden", st.mult <= 1);
    if (comboCount) comboCount.textContent = st.streak || "";
    if (comboMult) comboMult.textContent = st.mult || 1;
  },
});

// ---------------------------- canvas coordinate mapping ----------------------------
function canvasPos(e) {
  const r = canvas.getBoundingClientRect();
  return {
    x: (e.clientX - r.left) * (game.W / r.width),
    y: (e.clientY - r.top) * (game.H / r.height),
  };
}

// ---------------------------- pointer (mouse + touch unified) ----------------------------
function pointerDownAt(x, y) {
  ensureAudio();
  // ignore taps on HUD buttons
  const el = document.elementFromPoint(x, y);
  if (el && el.closest(".hud, #btn-gotit")) return;
  game.pointerDown(x, y);
}

canvas.addEventListener("mousedown", (e) => {
  e.preventDefault();
  const p = canvasPos(e);
  pointerDownAt(p.x, p.y);
});
window.addEventListener("mousemove", (e) => {
  const p = canvasPos(e);
  game.pointerMove(p.x, p.y);
});
window.addEventListener("mouseup", () => game.pointerUp());

canvas.addEventListener("touchstart", (e) => {
  e.preventDefault();
  const t = e.changedTouches[0];
  const p = canvasPos(t);
  pointerDownAt(p.x, p.y);
}, { passive: false });
canvas.addEventListener("touchmove", (e) => {
  e.preventDefault();
  const t = e.changedTouches[0];
  const p = canvasPos(t);
  game.pointerMove(p.x, p.y);
}, { passive: false });
canvas.addEventListener("touchend", (e) => {
  e.preventDefault();
  game.pointerUp();
}, { passive: false });

// keep paddle following even when pointer drags off-canvas
document.addEventListener("touchmove", (e) => {
  const t = e.changedTouches[0];
  const r = canvas.getBoundingClientRect();
  const x = (t.clientX - r.left) * (game.W / r.width);
  const y = (t.clientY - r.top) * (game.H / r.height);
  if (x >= -40 && x <= game.W + 40 && y >= -40 && y <= game.H + 40) {
    game.pointerMove(x, y);
  }
}, { passive: true });

// ---------------------------- keyboard ----------------------------
window.addEventListener("keydown", (e) => {
  const k = e.key.toLowerCase();
  if (k === "arrowleft" || k === "a") { game.setKey("left", true); e.preventDefault(); }
  if (k === "arrowright" || k === "d") { game.setKey("right", true); e.preventDefault(); }
  if (k === "arrowup" || k === "w") { game.setKey("up", true); e.preventDefault(); }
  if (k === "arrowdown" || k === "s") { game.setKey("down", true); e.preventDefault(); }
  if (k === " " || k === "enter") {
    ensureAudio();
    if (game.state === "menu") game.startGame();
    else if (game.state === "serve") game.launchBall(game.paddle.x);
    else if (game.state === "gameover") game.startGame();
    e.preventDefault();
  }
  if (k === "p" || k === "escape") togglePause();
});
window.addEventListener("keyup", (e) => {
  const k = e.key.toLowerCase();
  if (k === "arrowleft" || k === "a") game.setKey("left", false);
  if (k === "arrowright" || k === "d") game.setKey("right", false);
  if (k === "arrowup" || k === "w") game.setKey("up", false);
  if (k === "arrowdown" || k === "s") game.setKey("down", false);
});

// ---------------------------- buttons ----------------------------
function togglePause() {
  if (game.state === "menu" || game.state === "gameover" || game.state === "levelclear") return;
  game.paused = !game.paused;
  btnPause.textContent = game.paused ? "▶" : "⏸";
}
btnPause.addEventListener("click", () => togglePause());

let muted = false;
btnMute.addEventListener("click", () => {
  muted = !muted;
  S.setMuted(muted);
  btnMute.textContent = muted ? "🔇" : "🔊";
});

// ---------------------------- touch hint ----------------------------
if (localStorage.getItem("atb_hint") !== "1") {
  touchHint.classList.remove("hidden");
}
btnGotIt.addEventListener("click", () => {
  touchHint.classList.add("hidden");
  localStorage.setItem("atb_hint", "1");
  ensureAudio();
});

// ---------------------------- resize ----------------------------
function resize() {
  game.resize();
}
window.addEventListener("resize", resize);
window.addEventListener("orientationchange", () => setTimeout(resize, 200));

// pause when tab hidden
document.addEventListener("visibilitychange", () => {
  if (document.hidden && !game.paused && game.state === "play") {
    game.paused = true;
    if (btnPause) btnPause.textContent = "▶";
  }
});

// start
game.setStatsUi();
// expose for debugging / automated verification
window.__atb = game;
