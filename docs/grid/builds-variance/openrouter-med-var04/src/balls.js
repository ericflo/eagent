// balls.js — ball physics, trails, ball effect types, launch/serve, paddle collisions.
import { WORLD_W, WORLD_H, STEP, clamp, lerp } from './engine.js';

export const BASE_SPEED = 430;
export const MAX_SPEED = 980;

export class BallSet {
  constructor() {
    this.balls = [];
    this.trails = new Map();
    this.fireTimer = 0;
    this.pierceTimer = 0;
    this.giantTimer = 0;
    this.slowTimer = 0;
    this.stickyTimer = 0;
    this.heldBall = null;
    this.paddleCooldown = new Set(); // ball ids too close to paddle this tick
  }

  live() { return this.balls.filter(b => b.alive); }

  spawn(x, y, vx, vy) {
    const b = {
      id: Math.random().toString(36).slice(2), alive: true,
      x, y, vx, vy, prevX: x, prevY: y, r: 8,
      speed: Math.hypot(vx, vy),
    };
    this.balls.push(b);
    return b;
  }

  remove(b) {
    b.alive = false;
    this.trails.delete(b.id);
    this.paddleCooldown.delete(b.id);
  }

  applyEffect(name, dur) {
    // ball effects are global; picking a different ball effect replaces the old one
    if (name === 'fire') { this.fireTimer = dur; this.pierceTimer = 0; this.giantTimer = 0; }
    else if (name === 'pierce') { this.pierceTimer = dur; this.fireTimer = 0; this.giantTimer = 0; }
    else if (name === 'giant') {
      this.giantTimer = dur; this.fireTimer = 0; this.pierceTimer = 0;
      for (const b of this.live()) b.r = 8 * 2.2;
    }
    else if (name === 'slow') this.slowTimer = dur;
    else if (name === 'sticky') this.stickyTimer = dur;
  }

  setBallState(b, x, y, vx, vy) {
    b.x = x; b.y = y; b.prevX = x; b.prevY = y;
    b.vx = vx; b.vy = vy;
    b.speed = Math.hypot(vx, vy);
  }

  step(paddle, walls, onLeaveBottom, onPaddleHit, onBrickHit, effects, audio, tSec) {
    for (const b of this.balls) {
      if (!b.alive) continue;
      b.prevX = b.x; b.prevY = b.y;
      b.fireActive = this.fireTimer > 0;

      if (b === this.heldBall) continue; // held by sticky paddle; rides along

      let speedMul = 1;
      if (this.slowTimer > 0) speedMul *= 0.6;
      const dt = STEP * speedMul;

      b.x += b.vx * dt; b.y += b.vy * dt;
      b.speed = Math.hypot(b.vx, b.vy);

      // walls: left, right, top
      if (b.x - b.r < 0) { b.x = b.r; b.vx = Math.abs(b.vx); walls.left(b); }
      if (b.x + b.r > WORLD_W) { b.x = WORLD_W - b.r; b.vx = -Math.abs(b.vx); walls.right(b); }
      if (b.y - b.r < 0) { b.y = b.r; b.vy = Math.abs(b.vy); walls.top(b); }

      // death below the playfield
      if (b.y - b.r > WORLD_H) {
        this.remove(b);
        onLeaveBottom(b);
        continue;
      }

      // trail
      let tr = this.trails.get(b.id);
      if (!tr) { tr = []; this.trails.set(b.id, tr); }
      tr.push({ x: b.x, y: b.y });
      if (tr.length > 14) tr.shift();

      // fireball embers: sparks along the fire trail
      if (this.fireTimer > 0 && effects && Math.random() < STEP * 55) {
        effects.ember(b.x + (Math.random() - 0.5) * b.r * 2, b.y + (Math.random() - 0.5) * b.r * 2);
      }

      onPaddleHit(b);
      if (b.alive) onBrickHit(b);
    }

    // effect timers
    for (const k of ['fireTimer', 'pierceTimer', 'giantTimer', 'slowTimer', 'stickyTimer']) {
      if (this[k] > 0) {
        this[k] = Math.max(0, this[k] - STEP);
        if (this[k] === 0 && k === 'giantTimer') for (const b of this.live()) b.r = 8;
        if (this[k] === 0 && k === 'stickyTimer' && this.heldBall) this.releaseHeld();
      }
    }
  }

  releaseHeld(aimVx = 0, aimVy = -BASE_SPEED) {
    const b = this.heldBall;
    if (!b) return;
    const sp = clamp(Math.hypot(aimVx, aimVy) || BASE_SPEED, BASE_SPEED * 0.7, MAX_SPEED);
    const ang = Math.atan2(aimVy, aimVx);
    this.setBallState(b, b.x, b.y, Math.cos(ang) * sp, Math.sin(ang) * sp);
    this.heldBall = null;
    return b;
  }

  catchBall(b, paddle) {
    if (this.stickyTimer > 0 && !this.heldBall) {
      this.heldBall = b;
      this.setBallState(b, paddle.x + paddle.w / 2, paddle.y - b.r - 1, 0, 0);
      return true;
    }
    return false;
  }

  render(ctx, alpha, paddle) {
    for (const b of this.balls) {
      if (!b.alive) continue;
      const x = lerp(b.prevX, b.x, alpha);
      const y = lerp(b.prevY, b.y, alpha);

      // trail: gradient ribbon
      const tr = this.trails.get(b.id) || [];
      for (let i = 0; i < tr.length; i++) {
        const heat = i / tr.length; // 0 = oldest, 1 = newest
        if (this.fireTimer > 0) {
          // fire trail: deep red embers fading to bright orange/yellow at the head
          ctx.fillStyle = heat > 0.66 ? '#fde047' : heat > 0.33 ? '#fb923c' : '#dc2626';
          ctx.globalAlpha = heat * 0.8;
        } else {
          ctx.globalAlpha = heat * 0.5;
          ctx.fillStyle = this.pierceTimer > 0 ? '#a78bfa'
            : this.giantTimer > 0 ? '#f472b6' : '#67e8f9';
        }
        ctx.beginPath();
        ctx.arc(tr[i].x, tr[i].y, b.r * (0.25 + 0.55 * i / tr.length), 0, Math.PI * 2);
        ctx.fill();
      }
      ctx.globalAlpha = 1;

      // held ball rides the paddle
      let bx = x, by = y;
      if (b === this.heldBall) {
        bx = paddle.x + paddle.w / 2;
        by = paddle.y - b.r - 1;
      }

      let core = '#e0f2fe', edge = '#38bdf8';
      if (this.fireTimer > 0) { core = '#fffbeb'; edge = '#f97316'; }
      else if (this.pierceTimer > 0) { core = '#ede9fe'; edge = '#8b5cf6'; }
      else if (this.giantTimer > 0) { core = '#fce7f3'; edge = '#ec4899'; }
      else if (this.slowTimer > 0) { core = '#dcfce7'; edge = '#22c55e'; }

      ctx.shadowColor = this.fireTimer > 0 ? '#f97316' : edge;
      ctx.shadowBlur = this.fireTimer > 0 ? 22 : 12;
      const g = ctx.createRadialGradient(bx - b.r * 0.3, by - b.r * 0.3, 1, bx, by, b.r);
      g.addColorStop(0, core); g.addColorStop(1, edge);
      ctx.fillStyle = g;
      ctx.beginPath(); ctx.arc(bx, by, b.r, 0, Math.PI * 2); ctx.fill();
      ctx.shadowBlur = 0;
    }
  }
}
