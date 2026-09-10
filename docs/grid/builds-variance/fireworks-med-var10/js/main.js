// SKYBREAK — boot, main loop, menu/screen wiring.

(() => {
  const canvas = document.getElementById('game');
  const game = new Game(canvas);
  window.game = game; // debugging convenience

  // ----- overlay screens -------------------------------------------------

  const overlay = document.getElementById('overlay');
  const screens = {
    'screen-start': document.getElementById('screen-start'),
    'screen-pause': document.getElementById('screen-pause'),
    'screen-clear': document.getElementById('screen-clear'),
    'screen-over': document.getElementById('screen-over'),
    'screen-victory': document.getElementById('screen-victory'),
  };

  window.showScreen = (id) => {
    for (const k in screens) screens[k].classList.toggle('hidden', k !== id);
    overlay.classList.remove('hidden');
    overlay.style.pointerEvents = 'none';
    if (id) overlay.querySelector('.screen:not(.hidden)').style.pointerEvents = 'auto';
  };

  function hideOverlay() {
    overlay.classList.add('hidden');
  }

  // ----- settings ----------------------------------------------------------

  const btnSound = document.getElementById('btn-sound');
  const selScheme = document.getElementById('sel-scheme');

  function refreshSoundBtn() {
    btnSound.textContent = audio.muted ? 'Sound: Off' : 'Sound: On';
  }
  btnSound.addEventListener('click', () => {
    audio.setMuted(!audio.muted);
    refreshSoundBtn();
  });
  selScheme.addEventListener('change', () => {
    game.input.scheme = selScheme.value;
  });
  refreshSoundBtn();

  // ----- level select grid -------------------------------------------------

  const grid = document.getElementById('level-grid');
  function buildGrid() {
    grid.innerHTML = '';
    const p = game.progress;
    for (let i = 0; i < LEVELS.length; i++) {
      const b = document.createElement('button');
      b.textContent = i + 1;
      const unlocked = i === 0 || p.unlocked?.[i];
      if (!unlocked) b.classList.add('locked');
      if (i === game.levelIndex) b.classList.add('current');
      b.addEventListener('click', () => {
        if (!unlocked) return;
        audio.uiClick();
        startLevel(i);
      });
      grid.appendChild(b);
    }
  }

  // ----- flow --------------------------------------------------------------

  function startLevel(i) {
    audio.ensure();
    audio.resume();
    audio.startMusic();
    game.startLevel(i);
    hideOverlay();
  }

  function showMenu() {
    game.state = 'menu';
    audio.stopMusic();
    buildGrid();
    showScreen('screen-start');
  }

  document.getElementById('btn-play').addEventListener('click', () => {
    audio.uiClick();
    startLevel(game.levelIndex);
  });

  document.getElementById('btn-resume').addEventListener('click', () => {
    audio.uiClick();
    game.state = 'playing';
    hideOverlay();
  });

  document.getElementById('btn-restart').addEventListener('click', () => {
    audio.uiClick();
    startLevel(game.levelIndex);
  });

  document.getElementById('btn-quit').addEventListener('click', () => {
    audio.uiClick();
    showMenu();
  });

  document.getElementById('btn-next').addEventListener('click', () => {
    audio.uiClick();
    if (game.levelIndex + 1 < LEVELS.length) startLevel(game.levelIndex + 1);
    else showVictory();
  });

  document.getElementById('btn-menu2').addEventListener('click', showMenu);
  document.getElementById('btn-menu3').addEventListener('click', showMenu);
  document.getElementById('btn-menu4').addEventListener('click', showMenu);

  document.getElementById('btn-retry').addEventListener('click', () => {
    audio.uiClick();
    game.score = 0;
    game.lives = CONFIG.START_LIVES;
    startLevel(game.levelIndex);
  });

  document.getElementById('btn-again').addEventListener('click', () => {
    audio.uiClick();
    game.score = 0;
    game.lives = CONFIG.START_LIVES;
    startLevel(0);
  });

  function showVictory() {
    game.state = 'victory';
    document.getElementById('victory-stats').innerHTML =
      `All levels cleared!<br>Final score: <b>${fmtScore(game.score)}</b>`;
    showScreen('screen-victory');
  }

  // Pause / resume.
  function togglePause() {
    if (game.state === 'playing') {
      game.state = 'paused';
      audio.setTier(0);
      showScreen('screen-pause');
    } else if (game.state === 'paused') {
      game.state = 'playing';
      hideOverlay();
    }
  }
  window.addEventListener('keydown', (e) => {
    const k = e.key.toLowerCase();
    if (k === 'p' || k === 'escape') togglePause();
    if (k === 'm') { audio.setMuted(!audio.muted); refreshSoundBtn(); }
  });
  document.addEventListener('visibilitychange', () => {
    if (document.hidden && game.state === 'playing') togglePause();
  });

  // ----- main loop ----------------------------------------------------------

  let last = performance.now();
  function frame(now) {
    let dt = Math.min((now - last) / 1000, 1 / 20); // clamp big tab-switch gaps
    last = now;
    game.update(dt);
    game.render();
    requestAnimationFrame(frame);
  }
  requestAnimationFrame(frame);

  // Boot: menu with a demo-ish background.
  showMenu();
})();
