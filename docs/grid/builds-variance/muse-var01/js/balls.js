// Balls: 5 types via mods. Standard / Fire / Ghost / Heavy / Volt(lightning splitter).
export const BALL_STYLE = {
  standard:{color:'#eafcff', glow:'#00f0ff', r:8},
  fire:{color:'#ffb03c', glow:'#ff6a00', r:9},
  ghost:{color:'#d9a7ff', glow:'#c77dff', r:8},
  heavy:{color:'#ffffff', glow:'#ffd400', r:12},
  volt:{color:'#fff36e', glow:'#ffd400', r:7},
};
export function primaryType(b){
  if(b.fireT>0) return 'fire';
  if(b.ghostT>0) return 'ghost';
  if(b.heavyT>0) return 'heavy';
  if(b.volt) return 'volt';
  return 'standard';
}
export function makeBall(x,y,angle,speed,opts={}){
  return {
    x,y, vx:Math.cos(angle)*speed, vy:Math.sin(angle)*speed,
    r:opts.r||8, fireT:opts.fireT||0, ghostT:opts.ghostT||0, heavyT:opts.heavyT||0,
    volt:!!opts.volt, splitUsed:!!opts.splitUsed, stuck:true, trail:[],
    ghostPhase:Math.random()*6.28, dead:false,
  };
}
export function ballSpeed(b){ return Math.hypot(b.vx,b.vy); }
export function setSpeed(b,s){
  const c=Math.hypot(b.vx,b.vy)||1;
  b.vx=b.vx/c*s; b.vy=b.vy/c*s;
}
export function updateBalls(balls,dt,w,h,paddle,effects,onMiss,onPaddle){
  const minS=effects.slowmo?200:300, maxS=effects.slowmo?900:1150;
  for(const b of balls){
    if(b.dead) continue;
    if(b.fireT>0)b.fireT-=dt; if(b.ghostT>0)b.ghostT-=dt; if(b.heavyT>0)b.heavyT-=dt;
    const st=BALL_STYLE[primaryType(b)]; b.r=st.r;
    if(b.stuck){ b.x=paddle.x; b.y=paddle.y-paddle.h/2-b.r-2; continue; }
    b.x+=b.vx*dt; b.y+=b.vy*dt;
    // clamp speed
    let sp=ballSpeed(b);
    const wantMin=b.volt?520:minS;
    if(sp>maxS) setSpeed(b,maxS);
    else if(sp<wantMin&&sp>1) setSpeed(b,wantMin);
    // min vertical so no infinite horizontal loops
    if(Math.abs(b.vy)<sp*0.12){ b.vy+=(b.vy>=0?1:-1)*sp*0.35*dt*8; }
    // walls
    if(b.x<b.r){ b.x=b.r; b.vx=Math.abs(b.vx); effects.audio?.wall(); }
    if(b.x>w-b.r){ b.x=w-b.r; b.vx=-Math.abs(b.vx); effects.audio?.wall(); }
    if(b.y<b.r){ b.y=b.r; b.vy=Math.abs(b.vy); effects.audio?.wall(); }
    // paddle: only when moving down & overlapping
    if(b.vy>0){
      const pw=paddle.w/2+b.r, ph=paddle.h/2+b.r;
      if(Math.abs(b.x-paddle.x)<pw && Math.abs(b.y-paddle.y)<ph){
        onPaddle(b,paddle);
      }
    }
    // bottom miss
    if(b.y>h+30){ b.dead=true; onMiss(b); }
    // trail
    b.trail.push({x:b.x,y:b.y,t:primaryType(b)});
    if(b.trail.length>22) b.trail.shift();
  }
  return balls.filter(b=>!b.dead);
}
export function drawBalls(ctx,balls,od,time){
  for(const b of balls){
    const t=primaryType(b), st=BALL_STYLE[t];
    // trail
    ctx.save(); ctx.globalCompositeOperation='lighter';
    const n=b.trail.length;
    for(let i=0;i<n;i++){
      const p=b.trail[i], f=i/n;
      const c = od ? `hsl(${(time*220+i*9)%360} 100% 65%)` : (BALL_STYLE[p.t]?.glow||st.glow);
      ctx.globalAlpha=f*0.55;
      ctx.fillStyle=c; ctx.shadowBlur=10; ctx.shadowColor=c;
      const s=b.r*1.1*f+0.5;
      ctx.beginPath(); ctx.arc(p.x,p.y,s,0,6.283); ctx.fill();
    }
    ctx.restore(); ctx.globalAlpha=1;
    // body
    ctx.save();
    const col = od?`hsl(${(time*220)%360} 100% 72%)`:st.color;
    const glow = od?col:st.glow;
    if(t==='ghost') ctx.globalAlpha=0.55+0.3*Math.sin(time*10+b.ghostPhase);
    ctx.globalCompositeOperation='lighter';
    ctx.shadowBlur=t==='fire'?26:18; ctx.shadowColor=glow;
    const g=ctx.createRadialGradient(b.x-2,b.y-2,1,b.x,b.y,b.r+2);
    g.addColorStop(0,'#ffffff'); g.addColorStop(0.45,col); g.addColorStop(1,'transparent');
    ctx.fillStyle=g;
    ctx.beginPath(); ctx.arc(b.x,b.y,b.r+2,0,6.283); ctx.fill();
    ctx.fillStyle='#fff';
    ctx.beginPath(); ctx.arc(b.x,b.y,Math.max(2,b.r*0.4),0,6.283); ctx.fill();
    if(t==='fire'){ // flame flicker
      ctx.globalAlpha=0.8;
      ctx.fillStyle='#ff6a00';
      ctx.beginPath(); ctx.arc(b.x-b.vx*0.012,b.y-b.vy*0.012,b.r*0.55+Math.random()*2,0,6.283); ctx.fill();
    }
    if(t==='heavy'){
      ctx.globalAlpha=1; ctx.strokeStyle='#ffd400'; ctx.lineWidth=2;
      ctx.beginPath(); ctx.arc(b.x,b.y,b.r+3+Math.sin(time*14)*1.5,0,6.283); ctx.stroke();
    }
    ctx.restore(); ctx.globalAlpha=1; ctx.shadowBlur=0;
  }
}
