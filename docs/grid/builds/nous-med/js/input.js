// Input: mouse, touch (relative drag anywhere), keyboard.
export class Input {
  constructor(canvas, game) {
    this.game = game;
    this.keys = {};
    this.pointerActive = false;
    this.touchLast = null;

    canvas.addEventListener('mousemove', e => {
      const p = game.toWorld(e.clientX, e.clientY);
      game.pointerMove(p.x, p.y);
    });
    canvas.addEventListener('mousedown', e => { this.pointerActive = true; game.actionPress(); });
    window.addEventListener('mouseup', () => { this.pointerActive = false; });

    canvas.addEventListener('touchstart', e => {
      e.preventDefault();
      const t = e.touches[0];
      this.touchLast = { x: t.clientX, y: t.clientY };
      game.actionPress();
    }, { passive: false });
    canvas.addEventListener('touchmove', e => {
      e.preventDefault();
      const t = e.touches[0];
      if (this.touchLast) {
        const dx = (t.clientX - this.touchLast.x) * game.pixelsPerPx();
        const dy = (t.clientY - this.touchLast.y) * game.pixelsPerPx();
        game.pointerRel(dx, dy);
      }
      this.touchLast = { x: t.clientX, y: t.clientY };
    }, { passive: false });
    canvas.addEventListener('touchend', e => {
      e.preventDefault();
      this.touchLast = null;
    }, { passive: false });

    window.addEventListener('keydown', e => {
      if (['ArrowUp','ArrowDown','ArrowLeft','ArrowRight',' '].includes(e.key)) e.preventDefault();
      this.keys[e.key.toLowerCase()] = true;
      if (e.key === ' ') game.actionPress();
      if (e.key.toLowerCase() === 'p' || e.key === 'Escape') game.togglePause();
    });
    window.addEventListener('keyup', e => { this.keys[e.key.toLowerCase()] = false; });
    document.addEventListener('visibilitychange', () => {
      if (document.hidden) game.autoPause();
    });
  }

  // returns axis [-1,0,1] each for x,y
  getAxes() {
    let ax = 0, ay = 0;
    const k = this.keys;
    if (k['arrowleft'] || k['a']) ax -= 1;
    if (k['arrowright'] || k['d']) ax += 1;
    if (k['arrowup'] || k['w']) ay -= 1;
    if (k['arrowdown'] || k['s']) ay += 1;
    return { ax, ay };
  }
}
