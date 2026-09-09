// demo.js — attract-mode autopilot used behind MENU and GAMEOVER.
// Drives the real game state: AI paddle tracks the lowest live ball,
// occasionally slams up (moves up just before contact), auto-launches held
// balls, and cheats after ~20s without a brick break by force-breaking the
// lowest brick. Loops forever across levels.

import { PADDLE } from './config.js';

export class DemoPilot {
  constructor(game) {
    this.game = game;
    this.time = 0;
    this.sinceBreak = 0;
    this.slamTimer = 0;
    this.slamming = false;
    this.launchCooldown = 0;
  }

  // Called every frame while a demo is running. Mutates game.input.state
  // so the normal game path handles all movement/collision.
  update(dt) {
    const g = this.game;
    this.time += dt;
    this.sinceBreak += dt;
    this.launchCooldown -= dt;

    const inp = g.input.state;
    inp.actions.length = 0;
    inp.launchPressed = false;

    // Find the ball to track: lowest y (closest to the paddle) among live balls
    let target = null;
    for (const b of g.balls) {
      if (b.held) continue;
      if (!target || b.y > target.y) target = b;
    }

    const pad = g.paddle;
    if (target) {
      // Predict a bit ahead
      const predict = clampTime(target.y - pad.y, 0, 0.6);
      let aimX = target.x + target.vx * predict * 0.6;
      // Keep paddle reachable
      aimX = clampNum(aimX, pad.w / 2, g.FIELD.W - pad.w / 2);

      // Slam logic: start rising when the ball is ~180u above and closing in
      if (!this.slamming) {
        const closing = target.vy > 100 && (target.y - pad.y) < 260;
        if (closing && this.slamTimer <= 0) {
          this.slamming = true;
          this.slamTimer = 0.55; // rise for up to 0.55s
        }
      }
      if (this.slamming) {
        this.slamTimer -= dt;
        if (this.slamTimer <= 0 || (target.y - pad.y) < pad.h) {
          this.slamming = false;
          this.slamTimer = 1 + Math.random() * 2; // cooldown before next slam
        }
      } else if (this.slamTimer > 0) {
        this.slamTimer -= dt;
      }

      // Paddle target: smooth follow with a little dead-zone
      const dx = aimX - pad.x;
      inp.pointerActive = true;
      inp.paddleTarget = {
        x: aimX,
        y: this.slamming
          ? PADDLE.MIN_Y
          : clampNum(pad.y + (target.y < pad.y - 400 ? 40 : 0), PADDLE.MIN_Y, PADDLE.MAX_Y)
      };
      // Nudge to keep velocity sane
      void dx;
    } else {
      // No live ball: drift to center
      inp.pointerActive = true;
      inp.paddleTarget = { x: g.FIELD.W / 2, y: (PADDLE.MIN_Y + PADDLE.MAX_Y) / 2 };
      this.slamming = false;
    }

    // Auto-launch held balls
    const held = g.balls.find((b) => b.held);
    if (held && this.launchCooldown <= 0) {
      inp.launchPressed = true;
      inp.launchHeld = false; // release immediately (demo does not hold)
      this.launchCooldown = 0.4 + Math.random() * 0.5;
    } else if (held) {
      inp.launchHeld = false;
    }

    // Cheat: after 20s without a break, force-break the lowest breakable brick
    if (this.sinceBreak > 20) {
      let lowest = null;
      for (const br of g.bricks) {
        if (!br.alive || !br.canBreak(g)) continue;
        // Skip gated bricks the ball could never satisfy
        if (br.type === 'angle' || br.type === 'speed') continue;
        if (!lowest || br.y > lowest.y) lowest = br;
      }
      if (lowest) {
        g.breakBrick(lowest, { cheat: true, x: lowest.x, y: lowest.y, ball: null });
      } else {
        // Nothing breakable left (gated bricks only): reset the attract level
        g.loadLevel(1);
      }
      this.sinceBreak = 0;
    }

    // Auto-advance: if demo is in LEVELCLEAR the main loop times it;
    // if demo lost a life repeatedly with no balls, respawn is handled by main.
  }

  onBrickBroke() { this.sinceBreak = 0; }

  stop() {
    const inp = this.game.input.state;
    if (inp) { inp.pointerActive = false; inp.paddleTarget = null; }
  }
}

function clampNum(v, lo, hi) { return v < lo ? lo : (v > hi ? hi : v); }
function clampTime(v, lo, hi) { return v < lo ? lo : (v > hi ? hi : v); }