// main.js — boot, DPR-aware canvas sizing, fixed-timestep loop, rendering, __game debug API
'use strict';

(() => {
  const canvas = document.getElementById('game');
  const ctx = canvas.getContext('2d');
  let scale = 1, ox = 0, oy = 0, dpr = 1;

  function resize() {
    dpr = Math.min(window.devicePixelRatio || 1, 2);
    const w = window.innerWidth, h = window.innerHeight;
    const s = Math.min(w / CONFIG.LOGICAL_W, h / CONFIG.LOGICAL_H);
    const cw = Math.max(1, Math.floor(CONFIG.LOGICAL_W * s * dpr));
    const ch = Math.max(1, Math.floor(CONFIG.LOGICAL_H * s * dpr));
    canvas.width = cw; canvas.height = ch;
    canvas.style.width = (cw / dpr) + 'px'; canvas.style.height = (ch / dpr) + 'px';
    scale = s * dpr;
    ox = (cw - CONFIG.LOGICAL_W * scale) / 2;
    oy = (ch - CONFIG.LOGICAL_H * scale) / 2;
  }
  window.addEventListener('resize', resize);

  Input.setup(canvas);
  Input.attachStick(document.getElementById('stick'));
  Input.setStickVisible(('ontouchstart' in window) || navigator.maxTouchPoints > 0);

  // ---------- background ----------
  function drawBackground() {
    const S = Game.S;
    const g = ctx.createLinearGradient(0, 0, 0, CONFIG.LOGICAL_H);
    g.addColorStop(0, CONFIG.COLORS.bg1); g.addColorStop(1, CONFIG.COLORS.bg0);
    ctx.fillStyle = g; ctx.fillRect(0, 0, CONFIG.LOGICAL_W, CONFIG.LOGICAL_H);

    // starfield
    const pulse = 0.5 + 0.5 * Math.sin(Game.S.time * (2 + Game.S.stage * 1.5));
    for (const s of Game.stars) {
      ctx.globalAlpha = (0.15 + s.z * 0.3) * (1 + Game.S.stage * 0.12);
      ctx.fillStyle = '#cdd6ff';
      const r = s.z * (1.2 + pulse * Game.S.stage * 0.4);
      ctx.fillRect(s.x, s.y, r, r);
    }
    ctx.globalAlpha = 1;

    // perspective grid, hue shifts with heat stage
    const hue = 220 + Game.S.stage * 25;
    ctx.strokeStyle = `hsla(${hue},80%,65%,${0.06 + Game.S.stage * 0.02})`;
    ctx.lineWidth = 1;
    for (let x = 0; x <= CONFIG.LOGICAL_W; x += 60) {
      ctx.beginPath(); ctx.moveTo(x, CONFIG.LOGICAL_H); ctx.lineTo(CONFIG.LOGICAL_W / 2 + (x - CONFIG.LOGICAL_W / 2) * 0.4, 0); ctx.stroke();
    }
    for (let i = 0; i < 10; i++) {
      const y = CONFIG.LOGICAL_H - Math.pow(i + (Game.gridPhase % 1), 2) * 12;
      if (y < 0) continue;
      ctx.globalAlpha = 0.05 + i * 0.012;
      ctx.beginPath(); ctx.moveTo(0, y); ctx.lineTo(CONFIG.LOGICAL_W, y); ctx.stroke();
    }
    ctx.globalAlpha = 1;

    // beat pulse at high heat
    if (Game.S.stage >= 2) {
      const bp = Math.max(0, 1 - ((Game.S.time * (2 + Game.S.stage)) % 1)) * 0.06 * Game.S.stage;
      ctx.fillStyle = `hsla(${hue},90%,70%,${bp})`;
      ctx.fillRect(0, 0, CONFIG.LOGICAL_W, CONFIG.LOGICAL_H);
    }
  }

  function drawField() {
    const S = Game.S;
    // border glow by stage
    if (S.stage > 0 || S.riding) {
      const glow = 6 + S.stage * 5;
      ctx.save();
      ctx.shadowColor = `hsl(${220 + S.stage * 25},90%,65%)`;
      ctx.shadowBlur = glow;
      ctx.strokeStyle = `hsla(${220 + S.stage * 25},90%,70%,${0.4 + S.stage * 0.1})`;
      ctx.lineWidth = 3;
      ctx.strokeRect(1.5, 1.5, CONFIG.LOGICAL_W - 3, CONFIG.LOGICAL_H - 3);
      ctx.restore();
    }
    // shield
    if (S.shield) {
      ctx.fillStyle = 'rgba(167,139,250,0.5)';
      ctx.fillRect(0, CONFIG.SHIELD_Y, CONFIG.LOGICAL_W, 6);
      ctx.strokeStyle = 'rgba(167,139,250,0.8)'; ctx.lineWidth = 2;
      ctx.strokeRect(0, CONFIG.SHIELD_Y, CONFIG.LOGICAL_W, 6);
    }
    // OTT line (bottom of lowest brick) — subtle
    if (Bricks.count()) {
      const oy2 = Bricks.ottYValue();
      ctx.strokeStyle = S.riding ? 'rgba(94,234,212,0.55)' : 'rgba(255,255,255,0.08)';
      ctx.setLineDash([8, 10]);
      ctx.beginPath(); ctx.moveTo(0, oy2); ctx.lineTo(CONFIG.LOGICAL_W, oy2); ctx.stroke();
      ctx.setLineDash([]);
    }
  }

  // chromatic ghost: redraw field with offsets on flash
  function chromaticPass() {
    const S = Game.S;
    if (S.chroma <= 0.01) return;
    ctx.save();
    ctx.globalCompositeOperation = 'screen';
    ctx.globalAlpha = S.chroma * 0.35;
    ctx.translate(-6 * S.chroma, 0);
    Bricks.draw(ctx);
    ctx.fillStyle = 'rgba(255,0,0,0.5)'; ctx.fillRect(0, 0, CONFIG.LOGICAL_W, CONFIG.LOGICAL_H);
    ctx.restore();
    ctx.save();
    ctx.globalCompositeOperation = 'screen';
    ctx.globalAlpha = S.chroma * 0.35;
    ctx.translate(6 * S.chroma, 0);
    ctx.fillStyle = 'rgba(0,255,255,0.5)'; ctx.fillRect(0, 0, CONFIG.LOGICAL_W, CONFIG.LOGICAL_H);
    ctx.restore();
  }

  function drawBanner() {
    const S = Game.S;
    if (S.bannerT > 0 && S.banner) {
      const a = Math.min(1, S.bannerT * 2);
      const grow = 1 + Math.max(0, (0.4 - S.bannerT)) * 1.2;
      ctx.save();
      ctx.globalAlpha = a;
      ctx.translate(CONFIG.LOGICAL_W / 2, CONFIG.LOGICAL_H / 2.4);
      ctx.scale(grow, grow);
      ctx.font = '900 56px system-ui, sans-serif';
      ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
      ctx.shadowColor = S.banner.color; ctx.shadowBlur = 30;
      ctx.fillStyle = S.banner.color;
      ctx.fillText(S.banner.text, 0, 0);
      ctx.restore();
    }
    // multiplier display
    if (S.stage > 0) {
      const size = 22 + S.stage * 5;
      const pulse = 1 + 0.08 * Math.sin(S.time * 8);
      ctx.save();
      ctx.translate(CONFIG.LOGICAL_W / 2, 62);
      ctx.scale(pulse, pulse);
      ctx.font = `900 ${size}px system-ui, sans-serif`;
      ctx.textAlign = 'center';
      ctx.fillStyle = `hsl(${140 - S.stage * 18},95%,65%)`;
      ctx.shadowColor = ctx.fillStyle; ctx.shadowBlur = 10 + S.stage * 4;
      ctx.fillText('x' + S.mult, 0, 0);
      ctx.restore();
    }
  }

  // ---------- render ----------
  function render() {
    ctx.setTransform(1, 0, 0, 1, 0, 0);
    ctx.clearRect(0, 0, canvas.width, canvas.height);
    ctx.fillStyle = '#000'; ctx.fillRect(0, 0, canvas.width, canvas.height);
    ctx.setTransform(scale, 0, 0, scale, ox + Game.S.shakeX * dpr, oy + Game.S.shakeY * dpr);

    drawBackground();
    drawField();
    Bricks.draw(ctx);
    Powerups.draw(ctx);
    Balls.draw(ctx, Game.S.stage / CONFIG.HEAT_MAX_STAGES);
    Paddle.draw(ctx);
    Particles.draw(ctx);
    chromaticFlashIfBig();
    drawBanner();

    // vignette
    const v = ctx.createRadialGradient(CONFIG.LOGICAL_W / 2, CONFIG.LOGICAL_H / 2, 300,
      CONFIG.LOGICAL_W / 2, CONFIG.LOGICAL_H / 2, 700);
    v.addColorStop(0, 'rgba(0,0,0,0)'); v.addColorStop(1, 'rgba(0,0,0,0.45)');
    ctx.fillStyle = v; ctx.fillRect(0, 0, CONFIG.LOGICAL_W, CONFIG.LOGICAL_H);

    // white flash
    if (Game.S.flash > 0) {
      ctx.fillStyle = `rgba(255,255,255,${Game.S.flash * 0.6})`;
      ctx.fillRect(0, 0, CONFIG.LOGICAL_W, CONFIG.LOGICAL_H);
    }
  }
  function chromaticFlashIfBig() { /* integrated into chromaticPass called after particles */ }

  // ---------- fixed timestep loop ----------
  let last = performance.now(), acc = 0;
  function frame(now) {
    let dt = (now - last) / 1000; last = now;
    dt = Math.min(dt, CONFIG.MAX_FRAME_DT);
    if (Game.S.state !== 'paused') {
      acc += dt;
      let n = 0;
      while (acc >= CONFIG.FIXED_DT && n < 8) { Game.step(CONFIG.FIXED_DT); acc -= CONFIG.FIXED_DT; n++; }
      if (acc > CONFIG.FIXED_DT * 8) acc = 0;
    }
    render();
    requestAnimationFrame(frame);
  }

  // ---------- DOM wiring ----------
  const $ = id => document.getElementById(id);
  function showScreen(name) {
    for (const el of document.querySelectorAll('.screen')) el.classList.add('hidden');
    if (name) $(name + 'Screen').classList.remove('hidden');
  }
  function syncHUD() {
    const hud = $('hud');
    const show = Game.S.state === 'playing' || Game.S.state === 'paused';
    hud.classList.toggle('hidden', !show);
    $('hudScore').textContent = Game.S.score.toLocaleString();
    $('hudLives').textContent = '♥'.repeat(Math.max(0, Game.S.lives)) || '—';
    $('hudLevel').textContent = (Game.S.level + 1);
    $('hudMult').textContent = 'x' + Game.S.mult;
    $('hudMult').classList.toggle('multHot', Game.S.mult >= 2);
  }
  Game.showScreen = showScreen;
  Game.syncHUD = syncHUD;

  $('startBtn').addEventListener('click', () => { AudioSys.resume(); AudioSys.sfx.ui(); showScreen(null); Game.startGame(); });
  $('resumeBtn').addEventListener('click', () => { AudioSys.sfx.ui(); Game.togglePause(); });
  $('againBtn').addEventListener('click', () => { AudioSys.sfx.ui(); showScreen(null); Game.startGame(); });
  $('muteBtn').addEventListener('click', () => { const m = AudioSys.toggleMute(); $('muteBtn').textContent = m ? '🔇' : '🔊'; });
  $('stickToggle').addEventListener('change', e => Input.setStickVisible(e.target.checked));
  $('muteBtn').textContent = AudioSys.muted ? '🔇' : '🔊';
  document.addEventListener('visibilitychange', () => {
    if (document.hidden && Game.S.state === 'playing') Game.togglePause();
  });
  window.addEventListener('keydown', e => {
    if (e.key === 'Escape' || e.key.toLowerCase() === 'p') {
      if (Game.S.state === 'playing' || Game.S.state === 'paused') Game.togglePause();
    }
  });
  setInterval(() => { if (Game.S.state === 'playing') syncHUD(); }, 200);

  // title screen animated demo bricks
  const demo = ['SSS.SSS.', '.WKW.KW.', 'PGSG.GSP', 'V.DD.D.V'];
  (function titleAnim() {
    Bricks.build(demo);
    let t = 0;
    function tick() {
      t += 0.016;
      if (Game.S.state === 'title') {
        Bricks.update(0.016);
        for (const b of Bricks.grid) b.assemble = (Math.sin(t * 2 + b.col * 0.6 + b.row * 0.5) + 1) / 2 * 0.7 + 0.3;
      }
      requestAnimationFrame(tick);
    }
    tick();
  })();

  // ---------- debug / test API ----------
  window.__game = {
    startGame: () => { showScreen(null); Game.startGame(); },
    step: (dt) => Game.step(dt),
    get state() { return Game.S; },
    balls: Balls.list,
    bricks: Bricks.grid,
    config: CONFIG,
    Game, Balls, Bricks, Powerups, Paddle, Particles,
    Input,
    launch: () => Game.launch(),
  };

  resize();
  requestAnimationFrame(frame);
})();