'use strict';
// Global namespace + shared config for OVERDRIVE BREAK
var CFG = {
  W: 900, H: 1200,                 // virtual design resolution (portrait-ish); rendered fit-to-screen
  topUI: 120,                      // reserved HUD band at top (playfield starts below)
  paddleZoneTop: 0.78,             // paddle can move in lower 22% of playfield
  baseBallSpeed: 460,              // px/s in virtual units
  maxBallSpeed: 900,
  frenzyThreshold: 1.4,            // seconds above bricks before OVERDRIVE kicks in
  frenzyRamp: 0.35,                // multiplier growth per second while on top
  comboDecay: 0.6,                 // multiplier loss per second when below bricks
  grazeAngleMax: 25 * Math.PI/180, // angled-brick requirement (radians from horizontal)
  shockSpeed: 640,                 // velocity-brick threshold (px/s)
  pickupFall: 190,
  ballR: 11,
  brickRows: 14,
  brickH: 34,
  brickGap: 6,
  lives: 3,
};

// Global game state shared across files (simple, no modules)
var G = {
  state: 'title',        // title | legend | playing | paused | launch | levelclear | gameover
  score: 0, best: +(localStorage.getItem('ob_best')||0),
  lives: CFG.lives, level: 0,
  mult: 1, combo: 0, topTime: 0, frenzy: 0,   // frenzy 0..1
  time: 0, shake: 0, flash: 0, hitStop: 0,
  slowmo: 0, muted: false,
  legendShown: false,
  balls: [], bricks: [], pickups: [], particles: null, popups: [],
  paddle: null, lasers: [],
  pw: { expand:0, sticky:0, magnet:0 },   // pickup timers (seconds remaining)
  laserCd: 0,
  msg: '', msgT: 0,
  bricksLeft: 0,
  levelIntroT: 0,
  clearT: 0,
  input: null, audio: null, bg: null,
};

function clamp(v,a,b){ return v<a?a:(v>b?b:v); }
function rand(a,b){ return a + Math.random()*(b-a); }
function fmt(n){ return n.toLocaleString('en-US'); }