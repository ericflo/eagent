// Bricks: build/draw/collide. All break in 1 QUALIFYING hit; conditions telegraphed.
import { BRICK_INFO, COLS } from './levels.js';
import { ballSpeed } from './balls.js';

export const NEED_FAST = 560;   // velocity brick threshold
export const NEED_SLOW = 540;   // nimbus threshold (must be slower)
export const STEEP_MIN = 0.62;  // prism: |vy|/speed above this
export const SHALLOW_MAX = 0.5; // skimmer: |vy|/speed below this

export function buildBricks(map, w) {
  const bricks = [];
  const rows = map.length;
  const margin = 14, gap = 6;
  const top = 96;
  const bw = (w - margin * 2 - gap * (COLS - 1)) / COLS;
  const bh = Math.max(22, Math.min(30, bw * 0.42));
  map.forEach((rowStr, r) => {
    for (let c = 0; c < Math.min(COLS, rowStr.length); c++) {
      const ch = rowStr[c];
      if (ch === '.' || ch === ' ') continue;
      const type = charType(ch);
      if (!type) continue;
      bricks.push({
        type, col: c, row: r,
        x: margin + c * (bw + gap), y: top + r * (bh + gap),
        w: bw, h: bh, alive: true, flash: 0,
        phase: Math.random() * 6.28, baseX: margin + c * (bw + gap),
        denied: 0, deniedMsg: '',
      });
    }
  });
  return bricks;
}
function charType(ch) {
  return { S:'S', P:'P', K:'K', V:'V', N:'N', F:'F', G:'G', B:'B', H:'H' }[ch] || null;
}
// condition check -> {ok, msg}
export function qualifies(brick, ball) {
  const sp = ballSpeed(ball);
  const steep = Math.abs(ball.vy) / Math.max(sp, 1);
  switch (brick.type) {
    case 'P': return steep >= STEEP_MIN ? {ok:true} : {ok:false,msg:'TOO SHALLOW — HIT STEEP ▲'};
    case 'K': return steep <= SHALLOW_MAX ? {ok:true} : {ok:false,msg:'TOO STEEP — GRAZE IT ◀▶'};
    case 'V': return sp >= NEED_FAST ? {ok:true} : {ok:false,msg:'TOO SLOW ⚡ — BOOST UP!'};
    case 'N': return sp <= NEED_SLOW ? {ok:true} : {ok:false,msg:'TOO FAST 🪶 — SLOW DOWN'};
    case 'F': return ball.fireT > 0 ? {ok:true} : {ok:false,msg:'NEEDS FIREBALL 🔥',sizzle:true};
    case 'G': return ball.ghostT > 0 ? {ok:true} : {ok:false,msg:'NEEDS GHOST 👻',sizzle:true};
    default: return {ok:true};
  }
}
// circle-AABB; returns normal {nx,ny} or null
export function circleBrickNormal(b, br) {
  const cx = Math.max(br.x, Math.min(b.x, br.x + br.w));
  const cy = Math.max(br.y, Math.min(b.y, br.y + br.h));
  const dx = b.x - cx, dy = b.y - cy;
  const d2 = dx * dx + dy * dy;
  if (d2 > b.r * b.r) return null;
  if (d2 > 0.0001) { const d = Math.sqrt(d2); return {nx:dx/d, ny:dy/d, depth: b.r-d}; }
  // center inside: push along smallest penetration axis
  const l=b.x-br.x, r2=br.x+br.w-b.x, t=b.y-br.y, bo=br.y+br.h-b.y;
  const m=Math.min(l,r2,t,bo);
  if(m===t) return {nx:0,ny:-1,depth:1};
  if(m===bo) return {nx:0,ny:1,depth:1};
  if(m===l) return {nx:-1,ny:0,depth:1};
  return {nx:1,ny:0,depth:1};
}
export function brickBottom(bricks) {
  let m = -Infinity;
  for (const b of bricks) if (b.alive) m = Math.max(m, b.y + b.h);
  return m;
}
export function brickTop(bricks) {
  let m = Infinity;
  for (const b of bricks) if (b.alive) m = Math.min(m, b.y);
  return m;
}
const ICON = { S:'', P:'▲', K:'◀▶', V:'⚡', N:'🪶', F:'🔥', G:'👻', B:'💥', H:'⟷' };
export function drawBricks(ctx, bricks, time) {
  ctx.save(); ctx.textAlign='center'; ctx.textBaseline='middle';
  for (const b of bricks) {
    if (!b.alive) continue;
    const info = BRICK_INFO[b.type];
    const deniedF = b.denied > 0 ? Math.sin(time*30)*2 : 0;
    const x = b.x + deniedF;
    // glow body
    ctx.shadowBlur = 12 + (b.flash>0?22:0) + (b.type==='B'?6+4*Math.sin(time*6):0);
    ctx.shadowColor = info.glow;
    const g = ctx.createLinearGradient(x, b.y, x, b.y + b.h);
    if (b.flash > 0) { g.addColorStop(0,'#ffffff'); g.addColorStop(1, info.color); }
    else {
      g.addColorStop(0, shade(info.color, 28));
      g.addColorStop(0.5, info.color);
      g.addColorStop(1, shade(info.color, -34));
    }
    ctx.fillStyle = g;
    const r = 6;
    rr(ctx, x, b.y, b.w, b.h, r); ctx.fill();
    // glass highlight
    ctx.shadowBlur = 0;
    ctx.fillStyle = 'rgba(255,255,255,.28)';
    rr(ctx, x+3, b.y+3, b.w-6, Math.max(3,b.h*0.28), 4); ctx.fill();
    // dark icon band for special bricks
    if (b.type !== 'S') {
      ctx.fillStyle='rgba(0,0,10,.45)';
      rr(ctx, x+b.w*0.16, b.y+b.h*0.3, b.w*0.68, b.h*0.48, 4); ctx.fill();
      ctx.fillStyle = '#fff';
      ctx.shadowBlur=8; ctx.shadowColor=info.glow;
      ctx.font = `900 ${Math.max(10, b.h*0.5)}px ui-sans-serif,system-ui`;
      ctx.fillText(ICON[b.type], x+b.w/2, b.y+b.h*0.56);
      ctx.shadowBlur=0;
    }
    // velocity speed-lines / nimbus feather hint
    if (b.type==='V'){ ctx.fillStyle='rgba(255,255,255,.5)';
      for(let i=0;i<3;i++) ctx.fillRect(x+5+i*5, b.y+b.h-5, 3, 2);
    }
    if (b.denied>0){
      ctx.fillStyle='#fff'; ctx.font='800 10px ui-sans-serif,system-ui';
      ctx.shadowBlur=6; ctx.shadowColor='#ff2f5d';
      ctx.fillText(b.deniedMsg||'', x+b.w/2, b.y-8);
      ctx.shadowBlur=0;
    }
  }
  ctx.restore();
}
function rr(ctx,x,y,w,h,r){ r=Math.min(r,w/2,h/2);
  ctx.beginPath(); ctx.moveTo(x+r,y); ctx.arcTo(x+w,y,x+w,y+h,r);
  ctx.arcTo(x+w,y+h,x,y+h,r); ctx.arcTo(x,y+h,x,y,r); ctx.arcTo(x,y,x+w,y,r); ctx.closePath(); }
function shade(hex,amt){
  const n=parseInt(hex.slice(1),16);
  let r=(n>>16)+amt,g=((n>>8)&255)+amt,b=(n&255)+amt;
  r=Math.max(0,Math.min(255,r)); g=Math.max(0,Math.min(255,g)); b=Math.max(0,Math.min(255,b));
  return `rgb(${r},${g},${b})`;
}
