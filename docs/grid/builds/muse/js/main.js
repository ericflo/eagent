/* OVERDRIVE BREAKOUT — boot/wiring. Classic script. */
(function () {
  'use strict';
  function $(id) { return document.getElementById(id); }

  var canvas = $('game');
  var game = new window.BreakoutGame(canvas);
  var Audio = window.BreakoutAudio;
  var Input = window.BreakoutInput;

  function firstGesture() { Audio.unlock(); }

  Input.attach(canvas, {
    onLaunch: function () {
      firstGesture();
      if (game.state === 'PLAYING') {
        if (!$('level-card').classList.contains('hidden')) game.dismissLevelCard();
        else game.launchBalls();
      }
    },
    onPause: function () {
      if (game.state === 'PLAYING' || game.state === 'PAUSED') game.togglePause();
    },
    onMute: function () { toggleMute(); },
    onFirstGesture: firstGesture
  });

  function toggleMute() {
    firstGesture();
    var m = Audio.toggleMute();
    $('btn-mute').classList.toggle('off', m);
    $('btn-mute').textContent = m ? '✕' : '♪';
  }

  // Buttons (click + touchend-friendly via click)
  function bind(id, fn) {
    $(id).addEventListener('click', function (e) {
      e.preventDefault(); firstGesture(); Audio.uiClick(); fn();
    });
  }
  bind('btn-play', function () { game.hideAll(); game.newGame(); });
  bind('btn-howto', function () { game.hideAll(); game.state = 'HOWTO'; game.show('howto-screen'); });
  bind('btn-howto-back', function () { game.quitToTitle(); });
  bind('btn-resume', function () { game.togglePause(); });
  bind('btn-restart-p', function () { game.hideAll(); game.newGame(); });
  bind('btn-quit-p', function () { game.quitToTitle(); });
  bind('btn-start-level', function () { game.dismissLevelCard(); });
  bind('btn-retry', function () { game.hideAll(); game.newGame(); });
  bind('btn-quit-g', function () { game.quitToTitle(); });
  bind('btn-endless', function () { game.startEndless(); });
  bind('btn-quit-v', function () { game.quitToTitle(); });
  bind('btn-pause', function () {
    if (game.state === 'PLAYING' || game.state === 'PAUSED') game.togglePause();
  });
  bind('btn-mute', toggleMute);

  window.addEventListener('resize', function () { game.resize(); });
  document.addEventListener('visibilitychange', function () {
    if (document.hidden && game.state === 'PLAYING') game.togglePause();
  });
  window.addEventListener('orientationchange', function () {
    setTimeout(function () { game.resize(); }, 120);
  });

  // Title backdrop: load level 1 behind the title overlay
  game.loadLevel(0);
  game.state = 'TITLE';
  game.showHud(false);
  $('title-best').textContent = game.best;
  game.show('title-screen');

  function loop(t) {
    game.frame(t || 0);
    requestAnimationFrame(loop);
  }
  requestAnimationFrame(loop);
})();
