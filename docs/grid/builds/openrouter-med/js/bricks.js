'use strict';
// Brick types (NO multi-hit HP):
//  std, angle (graze-only), shock (velocity-only), move, boom (explosive),
//  phase (solid/intangible timer), core (armored-core: needs power ball or laser/two-different-hits)
var BRICK_TYPES = {
  std:    { col:'#5ad1ff', label:'STANDARD' },
  angle:  { col:'#ffd45a', label:'GRAZE' },
  shock:  { col:'#ff7ae0', label:'SHOCK' },
  move:   { col:'#7dff8a', label:'MOVER' },
  boom:   { col:'#ff6a4d', label:'BLASTER' },
  phase:  { col:'#b48aff', label:'PHASE' },
  core:   { col:'#9fb8c8', label:'ARMORED CORE' },
};
function Brick(x,y,w,h,type,row,opts){
  opts=opts||{};
  this.x=x; this.y=y; this.w=w; this.h=h; this.type=type; this.row=row;
  this.alive=true; this.hitA=0;
  this.vx = type==='move' ? (opts.dir||1)*(opts.speed||110) : 0;
  this.phase = type==='phase';
  this.solid = !this.phase || (opts.startSolid!==false);
  this.phaseT = rand(0,2.2);           // desync timers
  this.phasePeriod = 2.4;
  this.power = opts.power||null;       // guaranteed pickup drop
  this.coreState = 0;                  // 0 fresh, 1 cracked (hit once by power ball/laser)
  this.coreT = 0;                  // 0 fresh, 1 cracked (hit once by power ball/laser)
  this.minX = opts.minX!==undefined?opts.minX:0;
  this.maxX = opts.maxX!==undefined?opts.maxX:CFG.W-w;
}
Brick.prototype.update=function(dt){
  if(this.type==='move'){
    this.x+=this.vx*dt;
    if(this.x<=this.minX){ this.x=this.minX; this.vx=Math.abs(this.vx); }
    if(this.x>=this.maxX){ this.x=this.maxX; this.vx=-Math.abs(this.vx); }
  }
  if(this.phase){
    this.phaseT+=dt;
    if(this.phaseT>=this.phasePeriod){
      this.phaseT=0; this.solid=!this.solid;
      if(G.particles) G.particles.spawn({x:this.x+this.w/2,y:this.y+this.h/2,vx:0,vy:0,
        life:0.3,t:0,col:'#b48aff',size:8,kind:'ripple',grav:0});
    }
  }
  if(this.hitA>0) this.hitA=Math.max(0,this.hitA-dt*6);
  // armored-core crack heals after a short window (no plain HP grinding)
  if(this.type==='core'&&this.coreState===1){
    this.coreT-=dt;
    if(this.coreT<=0) this.coreState=0;
  }
};
Brick.prototype.cx=function(){ return this.x+this.w/2; };
Brick.prototype.cy=function(){ return this.y+this.h/2; };
// AABB vs circle test, returns collision point + normal-ish, or null
Brick.prototype.collide=function(bx,by,r){
  if(!this.alive||!this.solid) return null;
  var nx=clamp(bx,this.x,this.x+this.w), ny=clamp(by,this.y,this.y+this.h);
  var dx=bx-nx, dy=by-ny;
  if(dx*dx+dy*dy > r*r) return null;
  return {nx:nx,ny:ny};
};
Brick.prototype.canBreak=function(ball,impactSpeed){
  switch(this.type){
    case 'std': case 'move': case 'boom': return true;
    case 'angle': {
      // angle of impact velocity vs horizontal; require shallow graze
      var a=Math.atan2(Math.abs(ball.vy),Math.abs(ball.vx));
      return a<=CFG.grazeAngleMax;
    }
    case 'shock': return impactSpeed>=CFG.shockSpeed;
    case 'phase': return this.solid;
    case 'core':
      return ball.kind==='fire'||ball.kind==='laser'||ball.giant||this.coreState===1;
  }
  return true;
};
Brick.prototype.deflectReason=function(){
  switch(this.type){
    case 'angle': return 'GRAZE IT!';
    case 'shock': return 'SPEED UP!';
    case 'core': return 'NEED POWER!';
  }
  return '';
};
Brick.prototype.draw=function(ctx){
  if(!this.alive) return;
  var t=BRICK_TYPES[this.type], x=this.x,y=this.y,w=this.w,h=this.h;
  var col=t.col, alpha=1;
  if(this.phase&&!this.solid) alpha=0.18;
  ctx.save(); ctx.globalAlpha=alpha;
  if(this.hitA>0){ ctx.translate(this.x+w/2,this.y+h/2); ctx.scale(1+this.hitA*0.15,1+this.hitA*0.15); ctx.translate(-(this.x+w/2),-(this.y+h/2)); }
  switch(this.type){
    case 'std':
      roundRect(ctx,x,y,w,h,5); ctx.fillStyle=col; ctx.fill();
      ctx.fillStyle='rgba(255,255,255,0.35)'; ctx.fillRect(x+3,y+3,w-6,3);
      break;
    case 'angle': {
      // wedge shape: sloped top telegraphs graze rule
      ctx.beginPath();
      ctx.moveTo(x,y+h); ctx.lineTo(x+w,y+h); ctx.lineTo(x+w,y+h*0.25);
      ctx.lineTo(x,y); ctx.closePath();
      ctx.fillStyle=col; ctx.fill();
      // angle stripes
      ctx.strokeStyle='rgba(0,0,0,0.35)'; ctx.lineWidth=2;
      for(var i=1;i<4;i++){ ctx.beginPath(); ctx.moveTo(x+w*i/4,y+h); ctx.lineTo(x+w*i/4+w*0.1,y+h*0.25); ctx.stroke(); }
      ctx.fillStyle='rgba(0,0,0,0.55)'; ctx.font='bold 10px system-ui'; ctx.textAlign='center';
      ctx.fillText('∠',x+w/2,y+h-7);
      break;
    }
    case 'shock':
      roundRect(ctx,x,y,w,h,5); ctx.fillStyle='#3a2038'; ctx.fill();
      ctx.strokeStyle=col; ctx.lineWidth=2.5; ctx.stroke();
      // crack marks
      ctx.strokeStyle=col; ctx.lineWidth=1.5;
      ctx.beginPath(); ctx.moveTo(x+w*0.25,y+4); ctx.lineTo(x+w*0.4,y+h*0.5); ctx.lineTo(x+w*0.28,y+h-4); ctx.stroke();
      ctx.beginPath(); ctx.moveTo(x+w*0.7,y+4); ctx.lineTo(x+w*0.6,y+h*0.5); ctx.lineTo(x+w*0.75,y+h-4); ctx.stroke();
      ctx.fillStyle=col; ctx.font='bold 10px system-ui'; ctx.textAlign='center';
      ctx.fillText('⚡',x+w/2,y+h-7);
      break;
    case 'move':
      roundRect(ctx,x,y,w,h,5); ctx.fillStyle=col; ctx.fill();
      ctx.fillStyle='rgba(0,0,0,0.5)';
      ctx.beginPath(); ctx.moveTo(x+6,y+h/2-4); ctx.lineTo(x+2,y+h/2); ctx.lineTo(x+6,y+h/2+4); ctx.fill();
      ctx.beginPath(); ctx.moveTo(x+w-6,y+h/2-4); ctx.lineTo(x+w-2,y+h/2); ctx.lineTo(x+w-6,y+h/2+4); ctx.fill();
      break;
    case 'boom':
      ctx.shadowColor=col; ctx.shadowBlur=10+Math.sin(G.time*6)*4;
      ctx.beginPath(); ctx.arc(x+w/2,y+h/2,h*0.42,0,7);
      ctx.fillStyle=col; ctx.fill(); ctx.shadowBlur=0;
      ctx.fillStyle='#2a0d06'; ctx.font='bold 13px system-ui'; ctx.textAlign='center';
      ctx.fillText('✸',x+w/2,y+h/2+5);
      break;
    case 'phase':
      ctx.shadowColor=col; ctx.shadowBlur=this.solid?8:0;
      roundRect(ctx,x,y,w,h,5);
      if(this.solid){ ctx.fillStyle=col; ctx.fill(); }
      else { ctx.strokeStyle=col; ctx.lineWidth=2; ctx.stroke(); }
      ctx.shadowBlur=0;
      ctx.fillStyle=this.solid?'rgba(0,0,0,0.5)':col;
      ctx.font='bold 11px system-ui'; ctx.textAlign='center';
      ctx.fillText('◌',x+w/2,y+h-7);
      break;
    case 'core':
      roundRect(ctx,x,y,w,h,5);
      ctx.fillStyle= this.coreState? '#2b3540':'#44525f'; ctx.fill();
      ctx.strokeStyle=col; ctx.lineWidth=2.5; ctx.stroke();
      // hex core
      ctx.beginPath();
      var cx=x+w/2, cy=y+h/2, r=h*0.3;
      for(var j=0;j<6;j++){ var a=Math.PI/3*j+Math.PI/6;
        j?ctx.lineTo(cx+Math.cos(a)*r,cy+Math.sin(a)*r):ctx.moveTo(cx+Math.cos(a)*r,cy+Math.sin(a)*r); }
      ctx.closePath();
      ctx.fillStyle=this.coreState?'#ffb347':col; ctx.fill();
      ctx.fillStyle='#0b0e14'; ctx.font='bold 9px system-ui'; ctx.textAlign='center';
      ctx.fillText('CORE',cx,cy+3.5);
      break;
  }
  ctx.restore();
};
function roundRect(ctx,x,y,w,h,r){
  ctx.beginPath();
  ctx.moveTo(x+r,y); ctx.arcTo(x+w,y,x+w,y+h,r); ctx.arcTo(x+w,y+h,x,y+h,r);
  ctx.arcTo(x,y+h,x,y,r); ctx.arcTo(x,y,x+w,y,r); ctx.closePath();
}