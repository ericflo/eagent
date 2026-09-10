/* 50-powerups.js — namespace window.Powerups
   SKYBREAK powerup/capsule registry + application logic.
   Thin layer: all semantics are driven through the Game facade methods
   listed in apply(). Powerups NEVER stores its own timers; Game owns
   game.timers (id -> {t, total}) and Powerups.activeList(game) just reads it.
   Drop model: Powerups.roll(brick, game) -> powerId|null (dropFromBrick is an
   alias). Game constructs the Entities.Capsule(x, y, powerId) itself.
   Load order: after 40-entities.js. Only references other namespaces inside
   functions. ES2020, no modules, file:// safe. */

(function () {
  'use strict';
  var P = {};
  if (typeof window !== 'undefined') window.Powerups = P;

  P.SLOWMO_TS = 0.55;

  // ---------- Registry ----------
  // w: drop weight, d: duration seconds (0 = instant), neg: negative pill
  var POOL = {
    wide:       { id: 'wide',       glyph: 'W',  label: 'WIDE',    color: '#52ffa8', w: 10, d: 18, neg: false },
    magnet:     { id: 'magnet',     glyph: 'M',  label: 'MAGNET',  color: '#b44dff', w: 8,  d: 12, neg: false },
    multiball:  { id: 'multiball',  glyph: '3',  label: 'MULTI',   color: '#ffb020', w: 9,  d: 0,  neg: false },
    laser:      { id: 'laser',      glyph: 'L',  label: 'LASER',   color: '#ff4fd8', w: 7,  d: 10, neg: false },
    heavy:      { id: 'heavy',      glyph: 'H',  label: 'HEAVY',   color: '#ff7a3d', w: 7,  d: 12, neg: false },
    phantom:    { id: 'phantom',    glyph: 'P',  label: 'PHANTOM', color: '#6a3df0', w: 5,  d: 7,  neg: false },
    splitter:   { id: 'splitter',   glyph: 'S',  label: 'SPLIT',   color: '#c8ff4f', w: 6,  d: 10, neg: false },
    slowmo:     { id: 'slowmo',     glyph: 'T',  label: 'SLOW-MO', color: '#4fd8ff', w: 6,  d: 5,  neg: false },
    shield:     { id: 'shield',     glyph: 'Sh', label: 'SHIELD',  color: '#0f6f9c', w: 7,  d: 0,  neg: false },
    lifeup:     { id: 'lifeup',     glyph: '+',  label: '1-UP',    color: '#ff4f6e', w: 2,  d: 0,  neg: false },
    multiplier: { id: 'multiplier', glyph: 'x2', label: 'x2',      color: '#ffb020', w: 6,  d: 0,  neg: false },
    // negative pills (level 6+ only)
    shrink:     { id: 'shrink',     glyph: '!',  label: 'SHRINK',  color: '#ff4f6e', w: 10, d: 10, neg: true },
    speedup:    { id: 'speedup',    glyph: '>>', label: 'SPEED UP',color: '#ff4f6e', w: 9,  d: 8,  neg: true },
    invert:     { id: 'invert',     glyph: '?',  label: 'INVERT',  color: '#ff4f6e', w: 8,  d: 6,  neg: true }
  };
  P.POOL = POOL;

  // Weighted positive ids (negative drawn from same table but only at lvl>=6).
  var POS_IDS = Object.keys(POOL).filter(function (k) { return !POOL[k].neg; });
  var NEG_IDS = ['shrink', 'speedup', 'invert'];

  // ---------- Negative check ----------
  P.isNegative = function (id) { return !!(POOL[id] && POOL[id].neg); };

  // ---------- Drop logic ----------
  // module-level anti-streak history: last 2 granted ids cannot repeat.
  var history = [];
  P._history = history; // exposed for tests/debug

  function pickWeighted(ids, rng) {
    var total = 0, i;
    for (i = 0; i < ids.length; i++) total += POOL[ids[i]].w;
    var r = (rng ? rng.rand() : Math.random()) * total;
    for (i = 0; i < ids.length; i++) {
      r -= POOL[ids[i]].w;
      if (r <= 0) return ids[i];
    }
    return ids[ids.length - 1];
  }

  // Filter anti-streak: last 2 granted ids cannot repeat.
  function pickAntiStreak(ids, rng) {
    var cand = ids.filter(function (id) { return history.indexOf(id) === -1; });
    if (!cand.length) cand = ids;
    return pickWeighted(cand, rng);
  }

  // roll(brick, game) -> powerId | null
  P.roll = function (brick, game) {
    if (!brick || !brick.alive) return null;
    var level = (game && game.level) || 1;
    if (brick.hasCapsule) {
      return pickAntiStreak(POS_IDS, game && game.rng);
    }
    var rate = 0.12;
    if (brick.kind === 'wedge' || brick.kind === 'sentry') rate = 0.22;
    var rng = game && game.rng;
    var roll = rng ? rng.rand() : Math.random();
    if (roll >= rate) return null;
    // 18% of drops are negative pills, only level >= 6
    var negRoll = rng ? rng.rand() : Math.random();
    var id, ids;
    if (level >= 6 && negRoll < 0.18) {
      var cand = NEG_IDS.filter(function (nid) { return history.indexOf(nid) === -1; });
      ids = cand.length ? cand : NEG_IDS;
      id = pickWeighted(ids, rng);
    } else {
      id = pickAntiStreak(POS_IDS, rng);
    }
    // record anti-streak history (cap length 2)
    history.push(id);
    if (history.length > 2) history.shift();
    return id;
  };
  // Contract name; identical behavior (returns powerId|null).
  P.dropFromBrick = P.roll;

  // ---------- Apply ----------
  // Drives semantics through the Game facade. Game MUST implement (see report):
  // setPaddleWide(d), setMagnet(d), setSticky(d), spawnBalls(n),
  // setBallType(type,d), setTimeScale(ts,d), giveShield(), addLife(),
  // addMultiplier(n), shrinkPaddle(d), speedBalls(n), invert(d),
  // plus balls[], level, timers{}.
  P.apply = function (id, game) {
    if (!id || !game) return null;
    var def = POOL[id];
    if (!def) return null;
    // Game tracks timers: refresh (max) semantics live in game.setTimer.
    function timer(d) {
      if (game.setTimer) game.setTimer(id, d);
    }
    switch (id) {
      case 'wide':       game.setPaddleWide(18); timer(18); break;
      case 'magnet':     game.setMagnet(12); game.setSticky(12); timer(12); break;
      case 'multiball':  game.spawnBalls(2); break;
      case 'laser':      game.setBallType('laser', 10); timer(10); break;
      case 'heavy':      game.setBallType('heavy', 12); timer(12); break;
      case 'phantom':    game.setBallType('phantom', 7); timer(7); break;
      case 'splitter':   game.setBallType('splitter', 10); timer(10); break;
      case 'slowmo':     game.setTimeScale(P.SLOWMO_TS, 5); timer(5); break;
      case 'shield':     game.giveShield(); break;
      case 'lifeup':     game.addLife(); break;
      case 'multiplier': game.addMultiplier(0.5); break;
      case 'shrink':     game.shrinkPaddle(10); timer(10); break;
      case 'speedup':    game.speedBalls(0.18); timer(8); break;
      case 'invert':     game.invert(6); timer(6); break;
      default: break;
    }
    // FX/Audio hooks (only inside functions; safe if namespaces missing).
    if (typeof AudioSys !== 'undefined' && AudioSys && AudioSys.impact) {
      try { AudioSys.impact(def.neg ? 'powerdown' : 'powerup'); } catch (e) {}
    }
    if (typeof FX !== 'undefined' && FX && FX.flash) {
      try {
        FX.flash(def.neg ? 0.12 : 0.2);
        FX.vignettePulse(def.neg ? 0.4 : 0.7);
      } catch (e) {}
    }
    return id;
  };

  // ---------- HUD pill list ----------
  // Reads game.timers: { id -> {t: secondsLeft, total: seconds} }.
  P.activeList = function (game) {
    if (!game || !game.timers) return [];
    var out = [], id, t;
    for (id in game.timers) {
      t = game.timers[id];
      if (!t || t.t <= 0) continue;
      var def = POOL[id];
      out.push({
        id: id,
        label: def ? def.label : id.toUpperCase(),
        color: def ? def.color : '#4fd8ff',
        timeLeft: t.t,
        totalTime: t.total || 1
      });
    }
    out.sort(function (a, b) { return b.timeLeft - a.timeLeft; });
    return out;
  };
})();