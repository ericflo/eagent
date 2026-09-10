/* OVERDRIVE // Neon Breakout — entire game. Plain script, no modules (file:// safe).
   Logical playfield W=480 x H=720, scaled to canvas with devicePixelRatio.
   FEATURES: 2D paddle + up-hit boost, fixed-step ball physics, 5 powerups,
   6 brick types (all 1-hit conditional, NO multi-HP), topside frenzy multiplier,
   juice scaled to multiplier, synthesized audio via AudioSys, 6 levels. */
(function () {
'use strict';

/* ================= utils ================= */
function clamp(v, a, b) { return v < a ? a : v > b ? b : v; }
function rand(a, b) { return a + Math.random() * (b - a); }
function randi(a, b) { return Math.floor(rand(a, b + 1)); }
function choice(arr) { return arr[Math.floor(Math.random() * arr.length)]; }
function $(id) { return document.getElementById(id); }

/* ================= constants ================= */
var W = 480, H = 720;
var STEP = 1 / 60;
var PADDLE_W = 92, PADDLE_H = 14;
var ZONE_TOP = H * 0.78;                 // paddle may roam bottom ~22%
var BALL_R = 7;
var BALL_MIN = 320, BALL_MAX = 900, BALL_BASE = 430;
var VELOCITY_MIN = 560;                  // speed-brick threshold
var PRISM_STEEP = Math.sin(50 * Math.PI / 180); // |vy|/speed must exceed this
var FIRE_TIME = 8, GHOST_TIME = 8, HEAVY_TIME = 10, SLOW_TIME = 6;
var MAX_LIVES = 3;

/* Brick legend: . empty | S standard | P prism(angle) | V velocity | H shield | D drifter | B bomb */
/* ================= levels (6 hand-designed, tunnel-friendly) ================= */
var LEVELS = [
  { name: 'FIRST CONTACT',
    rows: [
      '..........',
      '..SSSSSS..',
      '..SSSSSS..',
      '..SSSSSS..',
      '...SSSS...',
      '..........',
      '..........',
      '..........'
    ] },
  { name: 'THE GATE',
    rows: [
      '..........',
      '.SSSPPSSS.',
      '.SSSPPSSS.',
      'SSSS.SSSS.',
      'SSSS.SSSS.',
      '.SSSBSSSS.',
      '..SSSSSS..',
      '..........'
    ] },
  { name: 'VELOCITY LAB',
    rows: [
      '..........',
      '.VVVSSVVV.',
      '.VSSSSSSV.',
      'SSSP..PSSS',
      'SSSB..BSSS',
      '.VSSSSSSV.',
      '.SSSDDSSS.',
      '..........'
    ] },
  { name: 'SHIELD WALL',
    rows: [
      '..........',
      '.HHHHHHHH.',
      '.HSSSSSSH.',
      'SSSP...PSS',
      'SSSV...VSS',
      '.HSSSSSSH.',
      '.HHBHHHBH.',
      '..SDDDDS..'
    ] },
  { name: 'DRIFT STORM',
    rows: [
      '.D..SS..D.',
      '.DDSSSSDD.',
      'SSSPVVPSBB',
      'SSSHHHHSSS',
      'SSSPVVPSBB',
      '.DDSSSSDD.',
      '.D..SS..D.',
      '...SSSS...'
    ] },
  { name: 'OVERDRIVE CORE',
    rows: [
      'BSSPHVPHSS',
      'SSHHHHHHSS',
      'SPVDDDDVPS',
      'SHBSSSSBHS',
      'SPVSDDVPSB',
      'SSHHHHHHSS',
      'BSSPHVPHSS',
      '.SSSSSSSS.'
    ] }
];

/* ================= state ================= */
var canvas, ctx2d;
var state = 'title'; // title|serve|play|pause|levelclear|over|win
var pausedFrom = null;
var level = 0, score = 0, lives = MAX_LIVES;
var hiScore = parseInt(localStorage.getItem('overdrive_hi') || '0', 10) || 0;
var bricks = [], cols = 10, brickW = 0, brickTop = 64, brickGap = 5;
var balls = [], powerups = [], particles = [], rings = [], floaters = [];
var paddle = {};
var fx = { fire: 0, ghost: 0, heavy: 0, slow: 0 };
var mult = 1, frenzyT = 0, topside = false, wasTopside = false;
var combo = 0, comboT = 0, bestCombo = 0;
var shake = 0, freezeT = 0, deathSlowT = 0, time = 0;
var flashUp = 0;           // paddle up-hit motion flash
var bannerQ = [];
var bricksBrokenThisLevel = 0;

/* ================= canvas / resize ================= */
function fitCanvas() {
  var stage = $('stage');
  var r = stage.getBoundingClientRect();
  var dpr = Math.min(window.devicePixelRatio || 1, 3);
  canvas.width = Math.max(1, Math.round(r.width * dpr));
  canvas.height = Math.max(1, Math.round(r.height * dpr));
  // scale: fit logical W x H into canvas (cover-width, letterbox handled by transform)
  var s = Math.min(canvas.width / W, canvas.height / H);
  view.s = s;
  view.ox = (canvas.width - W * s) / 2;
  view.oy = (canvas.height - H * s) / 2;
  view.dpr = dpr;
}
var view = { s: 1, ox: 0, oy: 0, dpr: 1 };
function toLogical(cx, cy) {
  var r = canvas.getBoundingClientRect();
  var px = (cx - r.left) * (canvas.width / r.width);
  var py = (cy - r.top) * (canvas.height / r.height);
  return { x: (px - view.ox) / view.s, y: (py - view.oy) / view.s };
}

/* ================= input: keyboard + mouse + touch-drag + thumbstick ================= */
var keys = {};
var mouse = { active: false, x: W / 2, y: H - 60 };
var drag = { on: false, id: -1, dx: 0, dy: 0 }; // 1:1 drag offset
var stick = { on: false, id: -1, sx: 0, sy: 0, dx: 0, dy: 0 };

function resetPaddle() {
  paddle.x = W / 2; paddle.y = H - 56;
  paddle.w = PADDLE_W; paddle.h = PADDLE_H;
  paddle.vx = 0; paddle.vy = 0; paddle.px = paddle.x; paddle.py = paddle.y;
  paddle.tx = paddle.x; paddle.ty = paddle.y; // pointer target
  paddle.mode = 'key'; // 'key' | 'pointer' | 'stick'
}

function initInput() {
  // touch-capable device? reveal the thumbstick (CSS keeps it hidden for mouse users)
  if ('ontouchstart' in window || (navigator.maxTouchPoints || 0) > 0) document.body.classList.add('touch');
  window.addEventListener('keydown', function (e) {
    var k = e.key.toLowerCase();
    keys[k] = true;
    AudioSys.unlock();
    if ('arrowup,arrowdown,arrowleft,arrowright,w,a,s,d'.split(',').indexOf(k) >= 0) paddle.mode = 'key';
    if (k === 'm') toggleMute();
    if (k === 'p' || k === 'escape') togglePause();
    if (k === ' ' || k === 'enter') {
      if (state === 'title') startGame();
      else if (state === 'serve') launchBall();
    }
    if (['arrowup', 'arrowdown', 'arrowleft', 'arrowright', ' '].indexOf(k) >= 0) e.preventDefault();
  });
  window.addEventListener('keyup', function (e) { keys[e.key.toLowerCase()] = false; });

  // Mouse: paddle follows with Y clamped to zone
  canvas.addEventListener('mousemove', function (e) {
    var p = toLogical(e.clientX, e.clientY);
    mouse.x = clamp(p.x, 0, W); mouse.y = clamp(p.y, ZONE_TOP, H - 24);
    mouse.active = true;
    if (state === 'play' || state === 'serve') paddle.mode = 'pointer';
    paddle.tx = mouse.x; paddle.ty = mouse.y;
  });
  canvas.addEventListener('mousedown', function (e) {
    AudioSys.unlock(); AudioSys.uiClick();
    if (state === 'serve') launchBall();
  });

  // Touch: drag (1:1) anywhere except stick zone; stick zone drives thumbstick
  var stage = $('stage'), zone = $('stick-zone');
  function touchPos(t) {
    var p = toLogical(t.clientX, t.clientY);
    return { x: clamp(p.x, 0, W), y: clamp(p.y, ZONE_TOP, H - 24) };
  }
  stage.addEventListener('touchstart', function (e) {
    AudioSys.unlock();
    document.body.classList.add('touch'); // first real touch also reveals the stick
    for (var i = 0; i < e.changedTouches.length; i++) {
      var t = e.changedTouches[i];
      // stick zone? compare against zone rect
      var zr = zone.getBoundingClientRect();
      if (t.clientX >= zr.left && t.clientX <= zr.right && t.clientY >= zr.top && t.clientY <= zr.bottom && !stick.on) {
        stick.on = true; stick.id = t.identifier;
        stick.sx = t.clientX; stick.sy = t.clientY; stick.dx = 0; stick.dy = 0;
        zone.classList.add('active');
        moveNub(0, 0);
        paddle.mode = 'stick';
      } else if (!drag.on) {
        var p = touchPos(t);
        drag.on = true; drag.id = t.identifier;
        drag.dx = paddle.x - p.x; drag.dy = paddle.y - p.y; // grab offset => 1:1
        paddle.mode = 'pointer'; paddle.tx = paddle.x; paddle.ty = paddle.y;
      }
    }
    if (state === 'serve' && !stick.on) launchBall();
    e.preventDefault();
  }, { passive: false });
  stage.addEventListener('touchmove', function (e) {
    for (var i = 0; i < e.changedTouches.length; i++) {
      var t = e.changedTouches[i];
      if (stick.on && t.identifier === stick.id) {
        var dx = (t.clientX - stick.sx), dy = (t.clientY - stick.sy);
        var m = Math.hypot(dx, dy), max = 48;
        if (m > max) { dx = dx / m * max; dy = dy / m * max; }
        stick.dx = dx / max; stick.dy = dy / max;
        moveNub(dx, dy);
      } else if (drag.on && t.identifier === drag.id) {
        var p = touchPos(t);
        paddle.tx = clamp(p.x + drag.dx, paddle.w / 2, W - paddle.w / 2);
        paddle.ty = p.y + drag.dy;
        paddle.mode = 'pointer';
      }
    }
    e.preventDefault(); // prevent page scroll
  }, { passive: false });
  function endTouch(e) {
    for (var i = 0; i < e.changedTouches.length; i++) {
      var t = e.changedTouches[i];
      if (stick.on && t.identifier === stick.id) {
        stick.on = false; stick.dx = stick.dy = 0;
        zone.classList.remove('active'); moveNub(0, 0);
        paddle.mode = 'key';
      }
      if (drag.on && t.identifier === drag.id) drag.on = false;
    }
  }
  stage.addEventListener('touchend', endTouch);
  stage.addEventListener('touchcancel', endTouch);

  window.addEventListener('resize', fitCanvas);
  window.addEventListener('orientationchange', function () { setTimeout(fitCanvas, 200); });
}
function moveNub(dx, dy) {
  var nub = $('stick-nub');
  nub.style.transform = 'translate(calc(-50% + ' + dx + 'px), calc(-50% + ' + dy + 'px))';
}

/* ================= level build ================= */
function buildLevel(idx) {
  bricks = []; balls = []; powerups = [];
  frenzyT = 0; mult = 1; topside = false; wasTopside = false;
  combo = 0; comboT = 0;
  fx.fire = fx.ghost = fx.heavy = fx.slow = 0;
  var grid = LEVELS[idx].rows;
  var rows = grid.length;
  brickW = (W - 20 - brickGap * (cols - 1)) / cols;
  var bh = 26;
  for (var r = 0; r < rows; r++) {
    for (var c = 0; c < cols; c++) {
      var ch = (grid[r][c] || '.');
      if (ch === '.') continue;
      var bw = brickW, bx = 10 + c * (brickW + brickGap);
      var by = brickTop + r * (bh + brickGap);
      bricks.push({
        c: c, r: r, x: bx, y: by, w: bw, h: bh,
        type: ch === 'S' ? 'std' : ch === 'P' ? 'prism' : ch === 'V' ? 'vel' :
              ch === 'H' ? 'shield' : ch === 'D' ? 'drift' : ch === 'B' ? 'bomb' : 'std',
        alive: true, driftDir: (c % 2 ? 1 : -1), driftSpd: rand(40, 90),
        hue: (r * 36 + c * 8) % 360,
        hintT: 0, hintKind: '', rippleT: 0, spawnT: 0.35 + Math.random() * 0.3
      });
    }
  }
  resetPaddle();
  balls.push(newBall(paddle.x, paddle.y - 20, true));
  state = 'serve';
  updateOverlays(); updateHud();
  $('level-label').textContent = 'LEVEL ' + (idx + 1) + '/6';
  showBanner(LEVELS[idx].name);
}

function newBall(x, y, stuck) {
  var heavy = fx.heavy > 0;
  return {
    x: x, y: y, vx: 0, vy: 0, r: heavy ? BALL_R + 3 : BALL_R,
    stuck: !!stuck, trail: [], ghostPhased: 0
  };
}
function ballSpeed(b) { return Math.hypot(b.vx, b.vy); }
function setBallSpeed(b, s) {
  var cur = ballSpeed(b) || 1;
  b.vx = b.vx / cur * s; b.vy = b.vy / cur * s;
}
function launchBall() {
  if (state !== 'serve') return;
  AudioSys.launch();
  for (var i = 0; i < balls.length; i++) {
    var b = balls[i];
    if (b.stuck) {
      b.stuck = false;
      var ang = rand(-60, -120) * Math.PI / 180;
      var sp = BALL_BASE;
      b.vx = Math.cos(ang) * sp; b.vy = Math.sin(ang) * sp;
    }
  }
  state = 'play';
  updateOverlays(); updateHud();
  hideLaunchTip();
}

/* ================= paddle update (2D, all inputs) ================= */
function updatePaddle(dt) {
  paddle.px = paddle.x; paddle.py = paddle.y;
  var SPD = 430, SPDY = 330;
  if (paddle.mode === 'stick' && stick.on) {
    paddle.x += stick.dx * SPD * dt;
    paddle.y += stick.dy * SPDY * dt;
    paddle.tx = paddle.x; paddle.ty = paddle.y;
  } else if (paddle.mode === 'pointer') {
    // critically-damped-ish follow for mouse/drag (fast but smooth)
    var k = 1 - Math.pow(0.0001, dt);
    paddle.x += (paddle.tx - paddle.x) * k;
    paddle.y += (paddle.ty - paddle.y) * k;
  } else {
    var ax = 0, ay = 0;
    if (keys['arrowleft'] || keys['a']) ax -= 1;
    if (keys['arrowright'] || keys['d']) ax += 1;
    if (keys['arrowup'] || keys['w']) ay -= 1;
    if (keys['arrowdown'] || keys['s']) ay += 1;
    paddle.x += ax * SPD * dt;
    paddle.y += ay * SPDY * dt;
    paddle.tx = paddle.x; paddle.ty = paddle.y;
  }
  paddle.x = clamp(paddle.x, paddle.w / 2, W - paddle.w / 2);
  paddle.y = clamp(paddle.y, ZONE_TOP, H - 24);
  paddle.vx = (paddle.x - paddle.px) / dt;
  paddle.vy = (paddle.y - paddle.py) / dt;
  if (flashUp > 0) flashUp -= dt;
  // stuck balls ride the paddle
  for (var i = 0; i < balls.length; i++) {
    if (balls[i].stuck) { balls[i].x = paddle.x; balls[i].y = paddle.y - 20; balls[i].trail.length = 0; }
  }
}

/* ================= ball update + wall / paddle physics ================= */
function slowFactor() { return fx.slow > 0 ? 0.55 : 1; }

function updateBall(b, dt) {
  if (b.stuck) return;
  var sf = slowFactor();
  b.x += b.vx * sf * dt;
  b.y += b.vy * sf * dt;
  // walls
  if (b.x < b.r) { b.x = b.r; b.vx = Math.abs(b.vx); AudioSys.wallTick(); spark(b.x, b.y, 4, '#2de2ff'); }
  if (b.x > W - b.r) { b.x = W - b.r; b.vx = -Math.abs(b.vx); AudioSys.wallTick(); spark(b.x, b.y, 4, '#2de2ff'); }
  if (b.y < b.r) { b.y = b.r; b.vy = Math.abs(b.vy); AudioSys.wallTick(); spark(b.x, b.y, 4, '#2de2ff'); }
  // paddle bounce: angle from hit offset + paddle X-velocity influence
  if (b.vy > 0 &&
      b.y + b.r >= paddle.y - paddle.h / 2 && b.y - b.r <= paddle.y + paddle.h / 2 &&
      Math.abs(b.x - paddle.x) <= paddle.w / 2 + b.r) {
    var offset = clamp((b.x - paddle.x) / (paddle.w / 2), -1, 1);
    var sp = ballSpeed(b);
    var ang = -Math.PI / 2 + offset * (Math.PI * 0.42); // up to ~75deg
    var nsp = sp + 12;
    // UP-HIT BOOST: moving paddle up on contact adds speed + juice
    if (paddle.vy < -60) {
      nsp += Math.min(260, -paddle.vy * 0.4);
      flashUp = 0.35;
      shake = Math.min(10, shake + 3);
      ring(paddle.x, paddle.y, '#a6ff3f', 60);
      spark(b.x, b.y, 14, '#a6ff3f');
      addFloater(b.x, b.y - 18, 'POWER HIT!', '#a6ff3f', 15);
      hitStop(0.03);
    }
    nsp = clamp(nsp, BALL_MIN, BALL_MAX);
    b.vx = Math.cos(ang) * nsp + paddle.vx * 0.25;
    // renormalize to nsp after adding paddle influence
    var cur = Math.hypot(b.vx, b.vy) || 1;
    b.vx = b.vx / cur * nsp;
    b.vy = -Math.abs(Math.sin(ang) * nsp);
    if (b.vy > -90) b.vy = -90; // never flat off paddle
    b.y = paddle.y - paddle.h / 2 - b.r - 1;
    AudioSys.paddleBlip((offset + 1) / 2);
    spark(b.x, b.y, 6, '#ffffff');
  }
  // trail (lengthens with multiplier)
  b.trail.push({ x: b.x, y: b.y });
  var maxTrail = Math.round(6 + Math.log2(mult) * 4 + (fx.fire > 0 ? 8 : 0));
  while (b.trail.length > maxTrail) b.trail.shift();
}

/* ================= bricks: collide + conditional 1-hit rules ================= */
function circleRectHit(b, r) {
  var nx = clamp(b.x, r.x, r.x + r.w), ny = clamp(b.y, r.y, r.y + r.h);
  var dx = b.x - nx, dy = b.y - ny;
  return dx * dx + dy * dy <= b.r * b.r;
}
function reflectBall(b, r) {
  // determine bounce side from penetration
  var cx = clamp(b.x, r.x, r.x + r.w), cy = clamp(b.y, r.y, r.y + r.h);
  var dx = b.x - cx, dy = b.y - cy;
  if (Math.abs(dx) > Math.abs(dy)) { b.vx = dx > 0 ? Math.abs(b.vx) : -Math.abs(b.vx); b.x += dx > 0 ? 2 : -2; }
  else if (Math.abs(dy) > 0.01) { b.vy = dy > 0 ? Math.abs(b.vy) : -Math.abs(b.vy); b.y += dy > 0 ? 2 : -2; }
  else { b.vy = -b.vy; }
}
function steepness(b) { var s = ballSpeed(b) || 1; return Math.abs(b.vy) / s; }

function tryHitBrick(b, br) {
  var heavy = fx.heavy > 0, fire = fx.fire > 0, ghost = fx.ghost > 0;
  var sp = ballSpeed(b);
  // FIREBALL: pierces everything, no bounce
  if (fire) { destroyBrick(br, b, true); return; }
  // GHOST: never bounces off bricks; destroys what it may, phases through the rest
  if (ghost) {
    if (br.type === 'shield' || br.type === 'std' || br.type === 'drift' || br.type === 'bomb' ||
        (br.type === 'prism' && (heavy || steepness(b) > PRISM_STEEP)) ||
        (br.type === 'vel' && (heavy || sp > VELOCITY_MIN))) {
      destroyBrick(br, b, true);
      if (br.type === 'shield') { br.phasedNote = true; }
    } else {
      // phase through harmlessly (no bounce, no destroy) + cyan wisp
      spark(b.x, b.y, 3, '#2de2ff');
    }
    return;
  }
  switch (br.type) {
    case 'std': case 'drift': case 'bomb':
      destroyBrick(br, b, false); break;
    case 'prism':
      if (heavy || steepness(b) > PRISM_STEEP) destroyBrick(br, b, false);
      else {
        reflectBall(b, br);
        br.hintT = 1.6; br.hintKind = 'angle';
        toast('TOO SHALLOW! Hit the ▲ brick steeply (&gt;50°)');
        AudioSys.denied();
      }
      break;
    case 'vel':
      if (heavy || sp > VELOCITY_MIN) destroyBrick(br, b, false);
      else {
        reflectBall(b, br);
        br.hintT = 1.6; br.hintKind = 'speed';
        toast('TOO SLOW! Hit the ≫ brick faster (paddle up-hit!)');
        AudioSys.denied();
      }
      break;
    case 'shield':
      // only fire/ghost (handled above) — normal balls bounce with ripple
      reflectBall(b, br);
      br.rippleT = 0.5;
      AudioSys.shieldPing();
      spark(b.x, b.y, 5, '#2de2ff');
      addFloater(br.x + br.w / 2, br.y - 6, 'SHIELDED!', '#2de2ff', 13);
      break;
  }
}

function destroyBrick(br, b, pierce) {
  if (!br.alive) return;
  br.alive = false;
  bricksBrokenThisLevel++;
  combo++; comboT = 2.5; bestCombo = Math.max(bestCombo, combo);
  var gain = 50 * mult;
  if (combo >= 3) gain += combo * 10 * mult;
  score += Math.round(gain);
  addFloater(br.x + br.w / 2, br.y, '+' + Math.round(gain), mult >= 8 ? '#ffd23f' : '#ffffff', 12 + Math.min(14, Math.log2(mult) * 3));
  if (combo >= 3 && combo % 5 === 0) showBanner(combo + 'X COMBO!');
  AudioSys.brickShatter(combo);
  // shatter particles scaled to multiplier
  var n = Math.round(8 + Math.log2(mult) * 6);
  shatter(br.x + br.w / 2, br.y + br.h / 2, n, brickColor(br));
  spark(br.x + br.w / 2, br.y + br.h / 2, Math.round(2 + mult / 4), '#ffffff');
  if (mult >= 4 || br.type === 'bomb') ring(br.x + br.w / 2, br.y + br.h / 2, brickColor(br), 40 + mult * 4);
  shake = Math.min(14, shake + 1.5 + Math.log2(mult));
  if (!pierce && b) reflectBall(b, br);
  if (mult >= 8) hitStop(0.035);
  // bomb: explode neighbors in radius
  if (br.type === 'bomb') explodeBomb(br);
  // maybe drop powerup from random bricks
  if (Math.random() < 0.20) spawnPowerup(br.x + br.w / 2, br.y + br.h / 2);
  updateHud();
}

function explodeBomb(br) {
  AudioSys.explosion();
  shake = Math.min(18, shake + 7);
  ring(br.x + br.w / 2, br.y + br.h / 2, '#ff7a1a', 130);
  flashBomb(br.x + br.w / 2, br.y + br.h / 2);
  hitStop(0.06);
  showBanner('BOOM!');
  var cx = br.x + br.w / 2, cy = br.y + br.h / 2;
  var R = (brickW + brickGap) * 1.7;
  var victims = bricks.filter(function (o) {
    return o.alive && Math.hypot(o.x + o.w / 2 - cx, o.y + o.h / 2 - cy) <= R;
  });
  victims.forEach(function (o) {
    if (!o.alive) return;
    o.alive = false; bricksBrokenThisLevel++;
    var gain = 50 * mult;
    score += Math.round(gain);
    addFloater(o.x + o.w / 2, o.y, '+' + Math.round(gain), '#ff9d3f', 13);
    shatter(o.x + o.w / 2, o.y + o.h / 2, 8, '#ff9d3f');
    if (o.type === 'bomb') explodeBomb(o); // chain (visited via alive flag)
  });
  updateHud();
}

/* ================= powerups ================= */
var PTYPES = ['multi', 'fire', 'ghost', 'heavy', 'slow'];
var PCOLORS = { multi: '#ff2d95', fire: '#ff7a1a', ghost: '#2de2ff', heavy: '#9d4dff', slow: '#4dff88' };
var PLABELS = { multi: '×3', fire: 'F', ghost: 'G', heavy: 'H', slow: 'S' };
function spawnPowerup(x, y) {
  var t = PTYPES[randi(0, PTYPES.length - 1)];
  powerups.push({ x: x, y: y, vy: 150, type: t, wob: rand(0, 6) });
}
function applyPowerup(t) {
  AudioSys.powerFanfare();
  if (t === 'multi') {
    var src = balls.slice(0, 8);
    src.forEach(function (b) {
      for (var k = -1; k <= 1; k += 2) {
        if (balls.length >= 12) break;
        var sp = clamp(ballSpeed(b), BALL_MIN, BALL_MAX);
        var a = Math.atan2(b.vy, b.vx) + k * 0.5;
        var nb = newBall(b.x, b.y, false);
        nb.vx = Math.cos(a) * sp; nb.vy = Math.sin(a) * sp;
        balls.push(nb);
      }
    });
    ring(paddle.x, paddle.y, '#ff2d95', 110);
    addFloater(paddle.x, paddle.y - 40, 'MULTIBALL ×3!', '#ff2d95', 18);
    showBanner('MULTIBALL!');
  } else if (t === 'fire') { fx.fire = FIRE_TIME; addFloater(paddle.x, paddle.y - 40, 'FIREBALL!', '#ff7a1a', 18); }
  else if (t === 'ghost') { fx.ghost = GHOST_TIME; addFloater(paddle.x, paddle.y - 40, 'GHOST PHASE!', '#2de2ff', 18); }
  else if (t === 'heavy') { fx.heavy = HEAVY_TIME; balls.forEach(function (b) { b.r = BALL_R + 3; }); addFloater(paddle.x, paddle.y - 40, 'HEAVY!', '#9d4dff', 18); }
  else if (t === 'slow') { fx.slow = SLOW_TIME; addFloater(paddle.x, paddle.y - 40, 'SLOW-MO!', '#4dff88', 18); }
  shake = Math.min(12, shake + 3);
  updateHud();
}
function updatePowerups(dt) {
  for (var i = powerups.length - 1; i >= 0; i--) {
    var p = powerups[i];
    p.wob += dt * 5; p.y += p.vy * dt; p.x += Math.sin(p.wob) * 20 * dt;
    if (p.y > H + 20) { powerups.splice(i, 1); continue; }
    if (p.y + 12 >= paddle.y - paddle.h / 2 && p.y - 12 <= paddle.y + paddle.h / 2 &&
        Math.abs(p.x - paddle.x) <= paddle.w / 2 + 14) {
      applyPowerup(p.type);
      powerups.splice(i, 1);
    }
  }
}

/* ================= topside / overdrive ================= */
function lowestBrickTop() {
  var low = -1;
  for (var i = 0; i < bricks.length; i++)
    if (bricks[i].alive && bricks[i].y > low) low = bricks[i].y;
  return low;
}
function updateTopside(dt) {
  var low = lowestBrickTop();
  topside = low >= 0 && balls.some(function (b) { return !b.stuck && b.y < low - 2; });
  if (topside) {
    if (!wasTopside) { showBanner('TOPSIDE!'); AudioSys.topsideRiser(); ring(W / 2, low, '#ffd23f', 160); }
    var before = multLevel();
    frenzyT += dt;
    var after = multLevel();
    if (after > before) {
      showBanner('×' + after + '!!');
      AudioSys.powerFanfare();
      shake = Math.min(16, shake + 4);
      ring(W / 2, 120, '#ffd23f', 120 + after * 2);
      hitStop(0.04);
    }
    // extra particles per hit handled in destroyBrick via mult scaling
  } else {
    frenzyT = Math.max(0, frenzyT - dt * 2.5); // decay when leaving topside
  }
  mult = multLevel();
  wasTopside = topside;
  AudioSys.setIntensity(clamp(Math.log2(mult) / 6.6, 0, 1), topside);
}
function multLevel() {
  // exponential: x2,x4,x8... up to x99
  if (frenzyT < 2) return 1;
  var m = Math.pow(2, Math.floor((frenzyT - 2) / 3) + 1);
  return Math.min(99, m);
}

/* ================= juice: particles, rings, floaters, shake, hit-stop ================= */
function shatter(x, y, n, color) {
  for (var i = 0; i < n; i++) {
    particles.push({
      kind: 'rect', x: x, y: y,
      vx: rand(-260, 260), vy: rand(-320, 120),
      life: rand(0.35, 0.8), t: 0, size: rand(2, 6),
      color: color, rot: rand(0, 6), vr: rand(-9, 9)
    });
  }
}
function spark(x, y, n, color) {
  for (var i = 0; i < n; i++) {
    var a = rand(0, Math.PI * 2), s = rand(60, 340);
    particles.push({
      kind: 'spark', x: x, y: y,
      vx: Math.cos(a) * s, vy: Math.sin(a) * s,
      life: rand(0.2, 0.55), t: 0, size: rand(1, 3), color: color
    });
  }
}
function ring(x, y, color, maxR) {
  rings.push({ x: x, y: y, r: 6, maxR: maxR || 80, t: 0, life: 0.45, color: color });
}
function flashBomb(x, y) {
  rings.push({ x: x, y: y, r: 4, maxR: 150, t: 0, life: 0.5, color: '#ffd23f' });
  spark(x, y, 26, '#ff7a1a'); spark(x, y, 14, '#ffd23f');
}
function addFloater(x, y, text, color, size) {
  floaters.push({ x: clamp(x, 50, W - 50), y: y, text: text, color: color, size: size || 13, t: 0, life: 1.1 });
  if (floaters.length > 24) floaters.shift();
}
function hitStop(dur) { freezeT = Math.max(freezeT, dur); }
function updateJuice(dt) {
  for (var i = particles.length - 1; i >= 0; i--) {
    var p = particles[i]; p.t += dt;
    if (p.t >= p.life) { particles.splice(i, 1); continue; }
    p.x += p.vx * dt; p.y += p.vy * dt; p.vy += 500 * dt;
    if (p.vr) p.rot += p.vr * dt;
  }
  if (particles.length > 600) particles.splice(0, particles.length - 600);
  for (var j = rings.length - 1; j >= 0; j--) {
    var r = rings[j]; r.t += dt;
    r.r += (r.maxR - r.r) * Math.min(1, dt * 9);
    if (r.t >= r.life) rings.splice(j, 1);
  }
  for (var k = floaters.length - 1; k >= 0; k--) {
    var f = floaters[k]; f.t += dt; f.y -= 42 * dt;
    if (f.t >= f.life) floaters.splice(k, 1);
  }
  if (comboT > 0) { comboT -= dt; if (comboT <= 0) combo = 0; }
  shake = Math.max(0, shake - dt * 26);
}

/* ================= per-frame update ================= */
function update(dt) {
  time += dt;
  // effect timers
  if (fx.fire > 0) fx.fire -= dt;
  if (fx.ghost > 0) fx.ghost -= dt;
  if (fx.heavy > 0) { fx.heavy -= dt; if (fx.heavy <= 0) balls.forEach(function (b) { b.r = BALL_R; }); }
  if (fx.slow > 0) fx.slow -= dt;

  updatePaddle(dt);

  // drifter bricks glide horizontally
  for (var i = 0; i < bricks.length; i++) {
    var br = bricks[i];
    if (!br.alive) continue;
    if (br.hintT > 0) br.hintT -= dt;
    if (br.rippleT > 0) br.rippleT -= dt;
    if (br.spawnT > 0) br.spawnT -= dt;
    if (br.type === 'drift') {
      br.x += br.driftDir * br.driftSpd * dt;
      if (br.x < 10) { br.x = 10; br.driftDir = 1; }
      if (br.x > W - 10 - br.w) { br.x = W - 10 - br.w; br.driftDir = -1; }
    }
  }

  // balls
  for (var bi = balls.length - 1; bi >= 0; bi--) {
    var b = balls[bi];
    updateBall(b, dt);
    if (b.stuck) continue;
    // brick collisions (first contact per step)
    for (var bj = 0; bj < bricks.length; bj++) {
      var br2 = bricks[bj];
      if (!br2.alive || br2.spawnT > 0) continue;
      if (circleRectHit(b, br2)) { tryHitBrick(b, br2); break; }
    }
    // bottom = life loss
    if (b.y - b.r > H + 10) balls.splice(bi, 1);
  }

  updatePowerups(dt);
  updateTopside(dt);
  updateJuice(dt);

  // level complete?
  if (bricks.every(function (br) { return !br.alive; })) {
    levelClear();
    return;
  }
  // all balls lost?
  if (balls.length === 0 && state === 'play') loseLife();
  updateHud();
}

function loseLife() {
  lives--;
  AudioSys.loseLife();
  shake = 12; deathSlowT = 1.1; hitStop(0.09);
  spark(paddle.x, paddle.y, 24, '#ff5a7a');
  ring(paddle.x, paddle.y, '#ff5a7a', 120);
  updateHud();
  if (lives <= 0) {
    gameOver();
  } else {
    showBanner('BALL LOST');
    balls.push(newBall(paddle.x, paddle.y - 20, true));
    state = 'serve'; showLaunchTip(); updateOverlays();
  }
}

function levelClear() {
  var bonus = lives * 250 + Math.round(mult) * 100;
  score += bonus;
  AudioSys.levelClear();
  ring(W / 2, H / 2, '#a6ff3f', 220);
  saveHi();
  if (level >= LEVELS.length - 1) {
    state = 'win';
    $('win-stats').textContent = 'Final score ' + score + ' • Best combo ×' + bestCombo;
  } else {
    state = 'levelclear';
    $('level-stats').textContent = LEVELS[level].name + ' clear! Bonus +' + bonus + ' • Score ' + score;
  }
  updateOverlays(); updateHud();
}

function gameOver() {
  state = 'over';
  AudioSys.gameOver();
  saveHi();
  $('over-stats').textContent = 'You reached sector ' + (level + 1) + ' with ' + score + ' points.';
  $('over-hi').textContent = hiScore;
  updateOverlays(); updateHud();
}

function saveHi() {
  if (score > hiScore) { hiScore = score; localStorage.setItem('overdrive_hi', String(hiScore)); }
}

/* ================= render ================= */
function brickColor(br) {
  switch (br.type) {
    case 'prism': return '#ff2d95';
    case 'vel': return '#ff7a1a';
    case 'shield': return '#2de2ff';
    case 'drift': return '#9d4dff';
    case 'bomb': return '#ff3b3b';
    default: return 'hsl(' + br.hue + ',90%,60%)';
  }
}

function render() {
  ctx2d.setTransform(1, 0, 0, 1, 0, 0);
  ctx2d.clearRect(0, 0, canvas.width, canvas.height);
  // shake offset (screen shake scaled by events)
  var shx = shake > 0 ? rand(-shake, shake) : 0;
  var shy = shake > 0 ? rand(-shake, shake) : 0;
  ctx2d.setTransform(view.s, 0, 0, view.s, view.ox + shx * view.s, view.oy + shy * view.s);

  drawBackground();
  drawBricks();
  drawPowerups();
  drawPaddle();
  drawBalls();
  drawParticles();
  drawFloaters();
  drawTopsideLine();
}

function drawBackground() {
  var g = ctx2d.createLinearGradient(0, 0, 0, H);
  var hueShift = (Math.log2(mult) * 8 + time * 6) % 360;
  g.addColorStop(0, '#0a0a24'); g.addColorStop(1, '#070716');
  ctx2d.fillStyle = g; ctx2d.fillRect(-20, -20, W + 40, H + 40);
  // pulsing grid (pulse rate + brightness scale with multiplier)
  var glow = Math.log2(mult);
  ctx2d.save();
  ctx2d.strokeStyle = topside ? 'rgba(255,210,63,' + (0.10 + 0.08 * Math.sin(time * 6)) + ')'
                              : 'hsla(' + (190 + hueShift * 0.15) + ',90%,60%,' + (0.07 + glow * 0.02) + ')';
  ctx2d.lineWidth = 1;
  var step = 40;
  ctx2d.beginPath();
  for (var x = 0; x <= W; x += step) { ctx2d.moveTo(x, 0); ctx2d.lineTo(x, H); }
  for (var y = 0; y <= H; y += step) { ctx2d.moveTo(0, y); ctx2d.lineTo(W, y); }
  ctx2d.stroke();
  ctx2d.restore();
  // topside frenzy background pulse
  if (topside) {
    var p = 0.10 + 0.06 * Math.sin(time * 8);
    var rg = ctx2d.createRadialGradient(W / 2, 0, 10, W / 2, 0, H * 0.7);
    rg.addColorStop(0, 'rgba(255,210,63,' + p + ')');
    rg.addColorStop(1, 'rgba(255,210,63,0)');
    ctx2d.fillStyle = rg; ctx2d.fillRect(0, 0, W, H);
    // subtle vignette pulse: depth + rate scale with multiplier
    var va = clamp(0.20 + glow * 0.02 + 0.07 * Math.sin(time * (4 + glow)), 0.12, 0.42);
    var vg = ctx2d.createRadialGradient(W / 2, H / 2, H * 0.28, W / 2, H / 2, H * 0.72);
    vg.addColorStop(0, 'rgba(2,2,10,0)');
    vg.addColorStop(1, 'rgba(2,2,10,' + va.toFixed(3) + ')');
    ctx2d.fillStyle = vg; ctx2d.fillRect(0, 0, W, H);
  }
  // paddle roam-zone hint
  ctx2d.save();
  ctx2d.strokeStyle = 'rgba(45,226,255,0.14)'; ctx2d.setLineDash([6, 8]);
  ctx2d.beginPath(); ctx2d.moveTo(8, ZONE_TOP); ctx2d.lineTo(W - 8, ZONE_TOP); ctx2d.stroke();
  ctx2d.restore();
}

function drawBricks() {
  for (var i = 0; i < bricks.length; i++) {
    var br = bricks[i];
    if (!br.alive) continue;
    var pop = br.spawnT > 0 ? clamp(1 - br.spawnT / 0.5, 0.2, 1) : 1;
    var cx = br.x + br.w / 2, cy = br.y + br.h / 2;
    ctx2d.save();
    ctx2d.translate(cx, cy); ctx2d.scale(pop, pop); ctx2d.translate(-cx, -cy);
    var col = brickColor(br);
    ctx2d.shadowColor = col; ctx2d.shadowBlur = 8 + Math.log2(mult) * 3;
    ctx2d.fillStyle = col;
    if (br.type === 'std') {
      var gg = ctx2d.createLinearGradient(0, br.y, 0, br.y + br.h);
      gg.addColorStop(0, col); gg.addColorStop(1, 'rgba(10,10,40,0.9)');
      ctx2d.fillStyle = gg;
    }
    roundRect(br.x, br.y, br.w, br.h, 5); ctx2d.fill();
    ctx2d.shadowBlur = 0;
    ctx2d.strokeStyle = 'rgba(255,255,255,0.55)'; ctx2d.lineWidth = 1.5;
    roundRect(br.x + 1, br.y + 1, br.w - 2, br.h - 2, 4); ctx2d.stroke();
    // glass highlight
    ctx2d.fillStyle = 'rgba(255,255,255,0.22)';
    roundRect(br.x + 3, br.y + 3, br.w - 6, 5, 2); ctx2d.fill();
    // type icons / labels
    ctx2d.fillStyle = '#fff'; ctx2d.textAlign = 'center'; ctx2d.textBaseline = 'middle';
    if (br.type === 'prism') {
      ctx2d.font = 'bold 13px sans-serif';
      ctx2d.fillText('▲ 50°', cx, cy + 0.5);
      // needed-angle guide arrow (always faint; bright while hinting)
      ctx2d.save();
      ctx2d.globalAlpha = br.hintKind === 'angle' && br.hintT > 0 ? 1 : 0.45;
      ctx2d.strokeStyle = '#ffd23f'; ctx2d.lineWidth = 2;
      ctx2d.beginPath(); ctx2d.moveTo(cx + br.w / 2 + 12, br.y - 6);
      ctx2d.lineTo(cx + br.w / 2 + 2, br.y - 20); ctx2d.stroke();
      ctx2d.fillStyle = '#ffd23f'; ctx2d.font = 'bold 10px sans-serif';
      ctx2d.fillText('STEEP!', cx + br.w / 2 + 16, br.y - 26);
      ctx2d.restore();
    } else if (br.type === 'vel') {
      ctx2d.font = 'bold 13px sans-serif';
      ctx2d.fillText('≫ FAST', cx, cy + 0.5);
      if (br.hintKind === 'speed' && br.hintT > 0) {
        // speed-meter hint: current ball speed vs threshold
        var sp = balls.length ? ballSpeed(balls[0]) : 0;
        var frac = clamp(sp / VELOCITY_MIN, 0, 1);
        ctx2d.fillStyle = 'rgba(0,0,0,0.7)';
        ctx2d.fillRect(cx - 34, br.y - 18, 68, 10);
        ctx2d.fillStyle = frac >= 1 ? '#4dff88' : '#ff7a1a';
        ctx2d.fillRect(cx - 33, br.y - 17, 66 * frac, 8);
        ctx2d.fillStyle = '#ffe9a8'; ctx2d.font = 'bold 9px sans-serif';
        ctx2d.fillText('NEED SPEED!', cx, br.y - 24);
      }
    } else if (br.type === 'shield') {
      ctx2d.font = 'bold 13px sans-serif';
      ctx2d.fillText('⬢', cx, cy + 0.5);
      if (br.rippleT > 0) {
        ctx2d.save();
        ctx2d.globalAlpha = br.rippleT * 2;
        ctx2d.strokeStyle = '#bffaff'; ctx2d.lineWidth = 2 + (0.5 - br.rippleT) * 8;
        roundRect(br.x - 3, br.y - 3, br.w + 6, br.h + 6, 7); ctx2d.stroke();
        ctx2d.restore();
      }
    } else if (br.type === 'drift') {
      ctx2d.font = 'bold 12px sans-serif';
      ctx2d.fillText('⇄', cx, cy + 0.5);
    } else if (br.type === 'bomb') {
      ctx2d.font = 'bold 14px sans-serif';
      ctx2d.fillText('✸', cx, cy + 0.5);
      ctx2d.save();
      ctx2d.globalAlpha = 0.5 + 0.4 * Math.sin(time * 6 + i);
      ctx2d.strokeStyle = '#ffd23f'; ctx2d.lineWidth = 2;
      roundRect(br.x - 1, br.y - 1, br.w + 2, br.h + 2, 6); ctx2d.stroke();
      ctx2d.restore();
    }
    ctx2d.restore();
  }
}

function ballColor(b) {
  if (fx.fire > 0) return '#ff7a1a';
  if (fx.ghost > 0) return '#2de2ff';
  if (fx.heavy > 0) return '#9d4dff';
  return '#ffffff';
}
function drawBalls() {
  var glow = Math.log2(mult);
  for (var i = 0; i < balls.length; i++) {
    var b = balls[i], col = ballColor(b);
    // trail
    if (b.trail.length > 1) {
      ctx2d.save();
      ctx2d.lineCap = 'round';
      for (var t = 1; t < b.trail.length; t++) {
        var a = t / b.trail.length;
        ctx2d.strokeStyle = col;
        ctx2d.globalAlpha = a * 0.55;
        ctx2d.lineWidth = b.r * 1.3 * a + 0.5;
        ctx2d.beginPath();
        ctx2d.moveTo(b.trail[t - 1].x, b.trail[t - 1].y);
        ctx2d.lineTo(b.trail[t].x, b.trail[t].y);
        ctx2d.stroke();
      }
      ctx2d.restore();
    }
    // speed lines when fast (threshold reuses VELOCITY_MIN; count scales with mult)
    var spd = Math.hypot(b.vx, b.vy);
    if (!b.stuck && spd > VELOCITY_MIN) {
      var nl = 2 + Math.round(Math.log2(mult));
      var nvx = b.vx / spd, nvy = b.vy / spd;
      ctx2d.save();
      ctx2d.strokeStyle = col; ctx2d.lineCap = 'round'; ctx2d.lineWidth = 2;
      for (var li = 0; li < nl; li++) {
        var off = (li - (nl - 1) / 2) * 7;
        var ox = -nvy * off, oy = nvx * off;
        var len = (spd - VELOCITY_MIN) * 0.12 + 10 + li * 4;
        ctx2d.globalAlpha = Math.max(0.12, 0.38 - li * 0.06);
        ctx2d.beginPath();
        ctx2d.moveTo(b.x - nvx * (b.r + 4) + ox, b.y - nvy * (b.r + 4) + oy);
        ctx2d.lineTo(b.x - nvx * (b.r + 4 + len) + ox, b.y - nvy * (b.r + 4 + len) + oy);
        ctx2d.stroke();
      }
      ctx2d.restore();
    }
    // chromatic feel at high multiplier: magenta/cyan offset echoes
    if (mult >= 8) {
      ctx2d.save(); ctx2d.globalAlpha = 0.35;
      ctx2d.fillStyle = '#ff2d95';
      ctx2d.beginPath(); ctx2d.arc(b.x - 2.5, b.y, b.r * 0.9, 0, 7); ctx2d.fill();
      ctx2d.fillStyle = '#2de2ff';
      ctx2d.beginPath(); ctx2d.arc(b.x + 2.5, b.y, b.r * 0.9, 0, 7); ctx2d.fill();
      ctx2d.restore();
    }
    ctx2d.save();
    ctx2d.shadowColor = col; ctx2d.shadowBlur = 10 + glow * 4 + (fx.fire > 0 ? 12 : 0);
    var bg = ctx2d.createRadialGradient(b.x - 2, b.y - 2, 1, b.x, b.y, b.r + 3);
    bg.addColorStop(0, '#ffffff'); bg.addColorStop(0.45, col); bg.addColorStop(1, 'rgba(0,0,0,0)');
    ctx2d.fillStyle = bg;
    ctx2d.beginPath(); ctx2d.arc(b.x, b.y, b.r + 2, 0, 7); ctx2d.fill();
    ctx2d.restore();
  }
}

function drawPaddle() {
  var glow = Math.log2(mult);
  // motion streak when paddle is moving fast (esp. upward)
  var spd = Math.hypot(paddle.vx, paddle.vy || 0);
  if (spd > 120) {
    ctx2d.save();
    ctx2d.globalAlpha = clamp((spd - 120) / 600, 0.1, 0.5);
    ctx2d.fillStyle = (paddle.vy || 0) < -60 ? '#a6ff3f' : '#2de2ff';
    var stretch = clamp(spd / 60, 0, 26);
    roundRect(paddle.x - paddle.w / 2 - 4, paddle.y + paddle.h / 2 - 2,
      paddle.w + 8, 3 + stretch * 0.4, 3);
    ctx2d.fill();
    ctx2d.restore();
  }
  ctx2d.save();
  var col = flashUp > 0 ? '#a6ff3f' : '#2de2ff';
  if (flashUp > 0) {
    // up-swing motion stretch: brief squash & stretch, decays with flashUp (0.35 → ~1.5×)
    var sk = 1 + flashUp * 1.4, sq = 1 - (sk - 1) * 0.35;
    ctx2d.translate(paddle.x, paddle.y);
    ctx2d.scale(sq, sk);
    ctx2d.translate(-paddle.x, -paddle.y);
  }
  ctx2d.shadowColor = col; ctx2d.shadowBlur = 12 + glow * 4 + (flashUp > 0 ? 14 : 0);
  var g = ctx2d.createLinearGradient(0, paddle.y - paddle.h / 2, 0, paddle.y + paddle.h / 2);
  g.addColorStop(0, '#ffffff'); g.addColorStop(0.4, col); g.addColorStop(1, '#0b3a55');
  ctx2d.fillStyle = g;
  if (mult >= 8) { // chromatic echo
    ctx2d.globalAlpha = 0.4; ctx2d.fillStyle = '#ff2d95';
    roundRect(paddle.x - paddle.w / 2 - 3, paddle.y - paddle.h / 2, paddle.w, paddle.h, 7); ctx2d.fill();
    ctx2d.globalAlpha = 1; ctx2d.fillStyle = g;
  }
  roundRect(paddle.x - paddle.w / 2, paddle.y - paddle.h / 2, paddle.w, paddle.h, 7);
  ctx2d.fill();
  ctx2d.shadowBlur = 0;
  ctx2d.fillStyle = 'rgba(255,255,255,0.75)';
  roundRect(paddle.x - paddle.w / 2 + 5, paddle.y - paddle.h / 2 + 2, paddle.w - 10, 3, 2);
  ctx2d.fill();
  // active-effect ring under paddle
  if (fx.fire > 0 || fx.ghost > 0 || fx.heavy > 0 || fx.slow > 0) {
    ctx2d.strokeStyle = ballColor(balls[0] || { }); ctx2d.lineWidth = 2;
    ctx2d.globalAlpha = 0.7 + 0.3 * Math.sin(time * 8);
    roundRect(paddle.x - paddle.w / 2 - 4, paddle.y - paddle.h / 2 - 4, paddle.w + 8, paddle.h + 8, 9);
    ctx2d.stroke();
  }
  ctx2d.restore();
}

function drawPowerups() {
  for (var i = 0; i < powerups.length; i++) {
    var p = powerups[i], col = PCOLORS[p.type];
    ctx2d.save();
    ctx2d.shadowColor = col; ctx2d.shadowBlur = 12;
    ctx2d.fillStyle = 'rgba(8,8,24,0.95)';
    roundRect(p.x - 18, p.y - 11, 36, 22, 11); ctx2d.fill();
    ctx2d.strokeStyle = col; ctx2d.lineWidth = 2;
    roundRect(p.x - 18, p.y - 11, 36, 22, 11); ctx2d.stroke();
    ctx2d.fillStyle = col; ctx2d.textAlign = 'center'; ctx2d.textBaseline = 'middle';
    ctx2d.font = '900 12px sans-serif';
    ctx2d.fillText(PLABELS[p.type], p.x, p.y + 0.5);
    ctx2d.restore();
  }
}

function drawParticles() {
  var i, p;
  for (i = 0; i < particles.length; i++) {
    p = particles[i];
    var a = 1 - p.t / p.life;
    ctx2d.save(); ctx2d.globalAlpha = a;
    if (p.kind === 'rect') {
      ctx2d.translate(p.x, p.y); ctx2d.rotate(p.rot || 0);
      ctx2d.fillStyle = p.color; ctx2d.fillRect(-p.size / 2, -p.size / 2, p.size, p.size);
    } else {
      ctx2d.strokeStyle = p.color; ctx2d.lineWidth = p.size;
      ctx2d.beginPath(); ctx2d.moveTo(p.x, p.y);
      ctx2d.lineTo(p.x - p.vx * 0.03, p.y - p.vy * 0.03); ctx2d.stroke();
    }
    ctx2d.restore();
  }
  for (i = 0; i < rings.length; i++) {
    var r = rings[i];
    ctx2d.save();
    ctx2d.globalAlpha = 1 - r.t / r.life;
    ctx2d.strokeStyle = r.color; ctx2d.lineWidth = 3;
    ctx2d.shadowColor = r.color; ctx2d.shadowBlur = 14;
    ctx2d.beginPath(); ctx2d.arc(r.x, r.y, r.r, 0, 7); ctx2d.stroke();
    ctx2d.restore();
  }
}
function drawFloaters() {
  ctx2d.save(); ctx2d.textAlign = 'center'; ctx2d.textBaseline = 'middle';
  for (var i = 0; i < floaters.length; i++) {
    var f = floaters[i], a = 1 - f.t / f.life;
    ctx2d.globalAlpha = Math.min(1, a * 2);
    ctx2d.font = '900 ' + f.size + 'px sans-serif';
    ctx2d.shadowColor = f.color; ctx2d.shadowBlur = 10;
    ctx2d.fillStyle = f.color;
    ctx2d.fillText(f.text, f.x, f.y);
  }
  ctx2d.restore();
}
function drawTopsideLine() {
  if (state !== 'play' && state !== 'serve') return;
  var low = lowestBrickTop();
  if (low < 0) return;
  // lowest live brick bottom: label sits below ALL bricks so it never overlaps them
  var bottom = low, bi;
  for (bi = 0; bi < bricks.length; bi++)
    if (bricks[bi].alive) bottom = Math.max(bottom, bricks[bi].y + bricks[bi].h);
  ctx2d.save();
  ctx2d.setLineDash([8, 8]);
  ctx2d.strokeStyle = topside ? '#ffd23f' : 'rgba(255,210,63,0.25)';
  ctx2d.lineWidth = topside ? 2.5 : 1.5;
  ctx2d.beginPath(); ctx2d.moveTo(10, low); ctx2d.lineTo(W - 10, low); ctx2d.stroke();
  ctx2d.setLineDash([]);
  // centered hint pill below the line: dark background keeps it legible over the grid
  var label = topside ? '▲ TOPSIDE ×' + mult : 'GET ABOVE FOR ×2 ×4 ×8';
  ctx2d.font = 'bold 10px sans-serif';
  var tw = ctx2d.measureText(label).width;
  var pw = Math.min(tw + 20, W - 20), ph = 18;
  var px = (W - pw) / 2, py = bottom + 8;
  if (py + ph > H - 4) py = H - 4 - ph;
  ctx2d.fillStyle = 'rgba(5,5,20,0.82)';
  roundRect(px, py, pw, ph, 9); ctx2d.fill();
  ctx2d.strokeStyle = topside ? '#ffd23f' : 'rgba(255,210,63,0.55)';
  ctx2d.lineWidth = 1;
  roundRect(px, py, pw, ph, 9); ctx2d.stroke();
  ctx2d.fillStyle = topside ? '#ffd23f' : '#ffe9a8';
  ctx2d.textAlign = 'center'; ctx2d.textBaseline = 'middle';
  ctx2d.fillText(label, W / 2, py + ph / 2 + 0.5);
  ctx2d.restore();
}
function roundRect(x, y, w, h, r) {
  ctx2d.beginPath();
  ctx2d.moveTo(x + r, y);
  ctx2d.arcTo(x + w, y, x + w, y + h, r);
  ctx2d.arcTo(x + w, y + h, x, y + h, r);
  ctx2d.arcTo(x, y + h, x, y, r);
  ctx2d.arcTo(x, y, x + w, y, r);
  ctx2d.closePath();
}

/* ================= HUD / overlays / banners ================= */
var toastTimer = null;
function toast(msg) {
  var el = $('hint-toast');
  el.innerHTML = msg; el.classList.remove('hidden');
  if (toastTimer) clearTimeout(toastTimer);
  toastTimer = setTimeout(function () { el.classList.add('hidden'); }, 1800);
}
/* Single banner element: a new major banner REPLACES the current one (never
   stacks/overlaps). Each shown banner keeps a minimum on-screen window so
   rapid hits (TOPSIDE! → ×2!! → ×4!!) stay readable: arrivals inside the
   window wait in a one-deep pending slot (bannerQ) and only the latest wins. */
var bannerShownAt = 0, bannerHideT = null, bannerPendT = null;
var BANNER_MIN = 600, BANNER_HOLD = 1650;
function showBanner(text) {
  var el = $('banner');
  var now = (window.performance && performance.now) ? performance.now() : Date.now();
  if (bannerHideT && now - bannerShownAt < BANNER_MIN) {
    bannerQ[0] = text; // replace pending slot; latest major banner wins
    if (!bannerPendT) {
      var wait = Math.max(0, BANNER_MIN - (now - bannerShownAt));
      bannerPendT = setTimeout(function () {
        bannerPendT = null;
        var t = bannerQ.shift();
        if (t !== undefined) showBanner(t);
      }, wait);
    }
    return;
  }
  bannerQ.length = 0;
  el.textContent = text;
  el.classList.remove('hidden', 'show');
  void el.offsetWidth; // restart CSS animation
  el.classList.add('show');
  bannerShownAt = now;
  if (bannerHideT) clearTimeout(bannerHideT);
  bannerHideT = setTimeout(function () { el.classList.add('hidden'); bannerHideT = null; }, BANNER_HOLD);
}
function hearts() {
  var s = '';
  for (var i = 0; i < MAX_LIVES; i++) s += i < lives ? '♥' : '♡';
  return s;
}
function updateHud() {
  $('hud-score').textContent = score;
  $('hud-hi').textContent = 'BEST ' + Math.max(hiScore, score);
  var ml = $('mult-label');
  ml.textContent = '×' + mult;
  var heat = clamp(Math.log2(mult) / Math.log2(99), 0, 1);
  ml.style.transform = 'scale(' + (1 + heat * 0.45) + ')';
  $('mult-fill').style.width = (heat * 100).toFixed(1) + '%';
  $('topside-tag').classList.toggle('hidden', !topside);
  $('lives-bar').textContent = hearts();
  // active-effect pills with countdowns
  var eb = $('effects-bar'); eb.innerHTML = '';
  var defs = [
    ['fire', 'FIRE', 'fx-fire'], ['ghost', 'GHOST', 'fx-ghost'],
    ['heavy', 'HEAVY', 'fx-heavy'], ['slow', 'SLOW-MO', 'fx-slow']
  ];
  defs.forEach(function (d) {
    if (fx[d[0]] > 0) {
      var s = document.createElement('span');
      s.className = 'fx-pill ' + d[2];
      s.textContent = d[1] + ' ' + Math.ceil(fx[d[0]]) + 's';
      eb.appendChild(s);
    }
  });
  if (combo >= 2) {
    var c = document.createElement('span');
    c.className = 'fx-pill fx-multi';
    c.textContent = combo + 'X COMBO';
    eb.appendChild(c);
  }
}
function updateOverlays() {
  var vis = { 'overlay-title': state === 'title', 'overlay-pause': state === 'pause',
    'overlay-level': state === 'levelclear', 'overlay-over': state === 'over',
    'overlay-win': state === 'win' };
  Object.keys(vis).forEach(function (id) { $(id).classList.toggle('hidden', !vis[id]); });
  $('hud').classList.toggle('hidden', state === 'title');
  $('title-hi').textContent = hiScore;
  if (state === 'serve') showLaunchTip(); else hideLaunchTip();
}
function showLaunchTip() { $('launch-tip').classList.remove('hidden'); }
function hideLaunchTip() { $('launch-tip').classList.add('hidden'); }

/* ================= game flow ================= */
function startGame() {
  AudioSys.unlock(); AudioSys.uiClick();
  score = 0; lives = MAX_LIVES; level = 0; bestCombo = 0;
  buildLevel(0);
  showLaunchTip();
}
function nextLevel() {
  AudioSys.unlock(); AudioSys.uiClick();
  level++;
  if (level >= LEVELS.length) { state = 'win'; updateOverlays(); return; }
  buildLevel(level);
  showLaunchTip();
}
function togglePause() {
  if (state === 'play' || state === 'serve') {
    pausedFrom = state; state = 'pause'; AudioSys.uiClick(); updateOverlays();
  } else if (state === 'pause') {
    state = pausedFrom || 'play'; AudioSys.uiClick(); updateOverlays();
  }
}
function toggleMute() {
  AudioSys.unlock();
  var m = AudioSys.toggleMute();
  $('btn-mute').textContent = m ? 'X' : '♪';
}
function bindButtons() {
  function on(id, fn) {
    var el = $(id);
    el.addEventListener('click', function (e) { e.stopPropagation(); AudioSys.unlock(); fn(); });
  }
  on('btn-start', startGame);
  on('btn-resume', togglePause);
  on('btn-restart-p', function () { buildLevel(level); });
  on('btn-next', nextLevel);
  on('btn-retry', startGame);
  on('btn-again', startGame);
  on('btn-pause', togglePause);
  on('btn-mute', toggleMute);
  // tap canvas to launch while serving (desktop + mobile backup)
  canvas.addEventListener('touchstart', function () { AudioSys.unlock(); }, { passive: true });
  document.addEventListener('pointerdown', function once() {
    AudioSys.unlock();
    document.removeEventListener('pointerdown', once);
  });
}

/* ================= main loop: fixed 60Hz timestep, hit-stop, death slow-mo ================= */
var lastT = 0, acc = 0;
function frame(ts) {
  requestAnimationFrame(frame);
  if (!lastT) lastT = ts;
  var raw = Math.min(0.1, (ts - lastT) / 1000);
  lastT = ts;
  if (state === 'pause' || state === 'title' || state === 'levelclear' || state === 'over' || state === 'win') {
    render(); // keep rendering behind overlays (frozen scene)
    return;
  }
  if (freezeT > 0) { freezeT -= raw; render(); return; } // hit-stop micro-freeze
  var dt = raw;
  if (deathSlowT > 0) { deathSlowT -= raw; dt *= 0.3; }   // life-loss slow-mo
  acc += dt;
  var n = 0;
  while (acc >= STEP && n < 5) { update(STEP); acc -= STEP; n++; }
  render();
}

/* ================= init ================= */
document.addEventListener('DOMContentLoaded', function () {
  canvas = $('game');
  ctx2d = canvas.getContext('2d');
  resetPaddle();
  fitCanvas();
  setTimeout(fitCanvas, 100);
  initInput();
  bindButtons();
  $('title-hi').textContent = hiScore;
  updateOverlays();
  requestAnimationFrame(frame);
});
})();


