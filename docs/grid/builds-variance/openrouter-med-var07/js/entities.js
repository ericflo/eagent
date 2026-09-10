// Breakthrough — entities.js: Brick, Ball, PowerUp, Laser + collision logic
window.BT = window.BT || {};
BT.entities = (function () {
  var U = BT.util, L = BT.level;

  // ---------- Ball ----------
  function Ball(x, y, vx, vy) {
    this.x = x; this.y = y; this.vx = vx; this.vy = vy;
    this.r = 8;
    this.stuck = false; // parked on paddle (sticky)
    this.stuckOffX = 0;
    this.fire = 0;      // fireball timer (s)
    this.trail = [];    // {x,y}
  }

  Ball.prototype.speed = function () { return Math.hypot(this.vx, this.vy); };

  Ball.prototype.setSpeed = function (s) {
    var cur = this.speed() || 1;
    this.vx *= s / cur; this.vy *= s / cur;
  };

  Ball.prototype.step = function (dt) {
    this.x += this.vx * dt;
    this.y += this.vy * dt;
    this.trail.push({ x: this.x, y: this.y });
    var maxTrail = 10 + Math.floor(this.fire > 0 ? 8 : 0);
    if (this.trail.length > maxTrail) this.trail.shift();
  };

  // ---------- Brick drawing helpers ----------
  function brickSolidness(b, time) {
    if (b.type !== 'phase') return 1;
    var c = Math.sin(time * (6.283 / 2.2) + b.phaseOff);
    return c > -0.15 ? 1 : 0; // solid slightly more than half the cycle
  }

  function drawBrickShape(ctx, b, solid) {
    ctx.globalAlpha = solid < 1 ? 0.25 : 1;
    var x = b.x, y = b.y, w = b.w, h = b.h, rr = 5;
    ctx.beginPath();
    ctx.moveTo(x + rr, y);
    ctx.arcTo(x + w, y, x + w, y + h, rr);
    ctx.arcTo(x + w, y + h, x, y + h, rr);
    ctx.arcTo(x, y + h, x, y, rr);
    ctx.arcTo(x, y, x + w, y, rr);
    ctx.closePath();
  }

  function drawBrick(ctx, b, time, glow) {
    var x = b.x, y = b.y, w = b.w, h = b.h;
    var solid = brickSolidness(b, time);
    if (solid === 0 && b.type === 'phase') {
      // ghost outline
      ctx.globalAlpha = 0.18;
      ctx.strokeStyle = '#b39dff';
      ctx.lineWidth = 1.5;
      drawBrickShape(ctx, b, 0); ctx.stroke();
      ctx.globalAlpha = 1;
      return;
    }
    if (b.type === 'standard') {
      var grad = ctx.createLinearGradient(x, y, x, y + h);
      grad.addColorStop(0, b.color);
      grad.addColorStop(1, shade(b.color, -40));
      drawBrickShape(ctx, b, 1);
      ctx.fillStyle = grad; ctx.fill();
      ctx.fillStyle = 'rgba(255,255,255,0.28)';
      ctx.fillRect(x + 3, y + 3, w - 6, 3);
    } else if (b.type === 'explosive') {
      var pulse = 0.5 + 0.5 * Math.sin(time * 6 + b.phaseOff);
      drawBrickShape(ctx, b, 1);
      ctx.fillStyle = '#ff7b1c'; ctx.fill();
      ctx.fillStyle = 'rgba(30,20,10,0.6)';
      ctx.beginPath(); ctx.arc(x + w / 2, y + h / 2, 6 + pulse * 3, 0, 6.283); ctx.fill();
      ctx.fillStyle = '#ffe14d';
      ctx.beginPath(); ctx.arc(x + w / 2, y + h / 2, 3 + pulse * 2, 0, 6.283); ctx.fill();
      ctx.strokeStyle = '#ffb35c'; ctx.lineWidth = 1.5;
      drawBrickShape(ctx, b, 1); ctx.stroke();
    } else if (b.type === 'armored') {
      drawBrickShape(ctx, b, 1);
      ctx.fillStyle = '#6d7887'; ctx.fill();
      ctx.fillStyle = '#8b96a5';
      ctx.fillRect(x + 3, y + 3, w - 6, h - 6);
      ctx.fillStyle = '#4d5765';
      var rivets = [[x + 8, y + h / 2], [x + w - 8, y + h / 2]];
      for (var i = 0; i < rivets.length; i++) {
        ctx.beginPath(); ctx.arc(rivets[i][0], rivets[i][1], 2.4, 0, 6.283); ctx.fill();
      }
      // speed chevrons
      ctx.strokeStyle = '#ffe14d'; ctx.lineWidth = 2;
      var cx = x + w / 2 - 9;
      for (var c = 0; c < 2; c++) {
        ctx.beginPath();
        ctx.moveTo(cx + c * 8, y + 6);
        ctx.lineTo(cx + 5 + c * 8, y + h / 2);
        ctx.lineTo(cx + c * 8, y + h - 6);
        ctx.stroke();
      }
    } else if (b.type === 'prism') {
      drawBrickShape(ctx, b, 1);
      ctx.fillStyle = 'rgba(180,140,255,0.35)'; ctx.fill();
      ctx.strokeStyle = '#d3a9ff'; ctx.lineWidth = 1.5;
      drawBrickShape(ctx, b, 1); ctx.stroke();
      // down-arrow icon (sweet spot: hit from below straight on)
      ctx.strokeStyle = '#fff'; ctx.lineWidth = 2;
      ctx.beginPath();
      ctx.moveTo(x + w / 2, y + h - 6);
      ctx.lineTo(x + w / 2, y + 6);
      ctx.moveTo(x + w / 2 - 4, y + h - 11);
      ctx.lineTo(x + w / 2, y + h - 6);
      ctx.lineTo(x + w / 2 + 4, y + h - 11);
      ctx.stroke();
    } else if (b.type === 'shielded') {
      drawBrickShape(ctx, b, 1);
      ctx.fillStyle = '#1f6f8f'; ctx.fill();
      ctx.strokeStyle = '#8fd8ef'; ctx.lineWidth = 1.5;
      drawBrickShape(ctx, b, 1); ctx.stroke();
      // glowing shield plate on bottom face
      var sg = ctx.createLinearGradient(x, y + h - 4, x, y + h + 5);
      sg.addColorStop(0, 'rgba(120,230,255,0.95)');
      sg.addColorStop(1, 'rgba(120,230,255,0.15)');
      ctx.fillStyle = sg;
      ctx.fillRect(x + 2, y + h - 2, w - 4, 6);
      ctx.strokeStyle = 'rgba(180,245,255,0.9)';
      ctx.lineWidth = 2;
      ctx.beginPath();
      ctx.moveTo(x + 2, y + h);
      ctx.lineTo(x + w - 2, y + h);
      ctx.stroke();
    } else if (b.type === 'phase') {
      drawBrickShape(ctx, b, 1);
      ctx.fillStyle = '#b39dff'; ctx.fill();
      ctx.strokeStyle = '#e4d9ff'; ctx.lineWidth = 1.5;
      drawBrickShape(ctx, b, 1); ctx.stroke();
      // clock glyph
      ctx.strokeStyle = '#fff'; ctx.lineWidth = 1.5;
      ctx.beginPath();
      ctx.arc(x + w / 2, y + h / 2, 6, 0, 6.283);
      ctx.moveTo(x + w / 2, y + h / 2);
      ctx.lineTo(x + w / 2, y + h / 2 - 5);
      ctx.moveTo(x + w / 2, y + h / 2);
      ctx.lineTo(x + w / 2 + 4, y + h / 2 + 2);
      ctx.stroke();
    }
    if (glow) {
      ctx.save();
      ctx.globalAlpha = 0.35;
      ctx.shadowColor = glow; ctx.shadowBlur = 14;
      drawBrickShape(ctx, b, 1);
      ctx.fillStyle = glow;
      ctx.fill();
      ctx.restore();
    }
    ctx.globalAlpha = 1;
  }

  function shade(hex, amt) {
    var n = parseInt(hex.substr(1), 16);
    var r = U.clamp((n >> 16) + amt, 0, 255), g = U.clamp(((n >> 8) & 255) + amt, 0, 255), b2 = U.clamp((n & 255) + amt, 0, 255);
    return 'rgb(' + r + ',' + g + ',' + b2 + ')';
  }

  // ---------- PowerUp capsule ----------
  var PU_DEFS = {
    M: { color: '#ff6ce7', label: 'MULTIBALL' },
    F: { color: '#ff5c2b', label: 'FIREBALL' },
    W: { color: '#5ce65c', label: 'WIDE' },
    L: { color: '#ffd23f', label: 'LASER' },
    S: { color: '#3fa7ff', label: 'STICKY' },
    T: { color: '#4dd0e1', label: 'SLOW-MO' },
    H: { color: '#ff4d6d', label: 'EXTRA LIFE' }
  };
  function makePowerUp(x, y) {
    // ~16% of drops are life
    var kind = Math.random() < 0.04 ? 'H' : U.pick(['M', 'F', 'W', 'L', 'S', 'T']);
    return { x: x, y: y, vy: 140, kind: kind, t: Math.random() * 6.283 };
  }

  function drawPowerUp(ctx, pu, time) {
    var d = PU_DEFS[pu.kind];
    var sway = Math.sin(pu.t * 3) * 6;
    ctx.save();
    ctx.translate(pu.x + sway, pu.y);
    ctx.shadowColor = d.color; ctx.shadowBlur = 14;
    // capsule
    var w = 34, h = 20, rr = 10;
    ctx.beginPath();
    ctx.moveTo(-w / 2 + rr, -h / 2);
    ctx.arcTo(w / 2, -h / 2, w / 2, h / 2, rr);
    ctx.arcTo(w / 2, h / 2, -w / 2, h / 2, rr);
    ctx.arcTo(-w / 2, h / 2, -w / 2, -h / 2, rr);
    ctx.arcTo(-w / 2, -h / 2, w / 2, -h / 2, rr);
    ctx.closePath();
    ctx.fillStyle = d.color; ctx.fill();
    ctx.shadowBlur = 0;
    ctx.fillStyle = 'rgba(0,0,0,0.75)';
    ctx.font = '800 13px system-ui, sans-serif';
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    var letter = pu.kind === 'H' ? '♥' : pu.kind;
    ctx.fillText(letter, 0, 1);
    ctx.restore();
  }

  // ---------- Laser bolt ----------
  function makeLaser(x, y) {
    return { x: x, y: y, vy: -900, t: 0 };
  }

  return {
    Ball: Ball, makePowerUp: makePowerUp, drawPowerUp: drawPowerUp, makeLaser: makeLaser,
    drawBrick: drawBrick, brickSolidness: brickSolidness, PU_DEFS: PU_DEFS, shade: shade
  };
})();