'use strict';
// Ball entities: normal, fire, split children, giant, laser, sticky.
function Ball(x,y,vx,vy,kind){
  this.x=x; this.y=y; this.vx=vx; this.vy=vy;
  this.kind=kind||'normal';       // normal|fire|giant|laser|sticky
  this.r = CFG.ballR;
  this.trail=[]; this.stuck=false;
  this.fireT=0;                   // timer for temporary kinds
  this.split = false;             // split flag (SPLIT ball splits once on next brick hit or paddle hit)
}
Ball.prototype.isPower=function(){ return this.kind!=='normal'||this.giant; };
Ball.prototype.speed=function(){ return Math.hypot(this.vx,this.vy); };
Ball.prototype.setSpeed=function(s){
  var sp=this.speed()||1; this.vx=this.vx/sp*s; this.vy=this.vy/sp*s;
};
Ball.prototype.update=function(dt){
  if(this.stuck) return;
  // clamp speed
  var sp=this.speed();
  if(sp>CFG.maxBallSpeed) this.setSpeed(CFG.maxBallSpeed);
  if(sp<CFG.baseBallSpeed*0.85) this.setSpeed(CFG.baseBallSpeed*0.85);
  this.x+=this.vx*dt; this.y+=this.vy*dt;
  this.trail.push({x:this.x,y:this.y});
  if(this.trail.length>14) this.trail.shift();
  if(this.fireT>0){ this.fireT-=dt; if(this.fireT<=0){ this.kind='normal'; this.giant=false; this.r=CFG.ballR; } }
};
Ball.prototype.draw=function(ctx,interp){
  var col = BALL_COLORS[this.kind]||'#fff';
  var px = this.x + (this.vx)*0, py=this.y;
  ctx.save();
  // trail
  var tl=this.trail.length;
  for(var i=0;i<tl;i++){
    var t=this.trail[i], k=i/tl;
    ctx.globalAlpha=k*0.35+G.frenzy*0.15;
    ctx.fillStyle=col;
    ctx.shadowColor=col; ctx.shadowBlur=6+G.frenzy*18+clamp(G.mult-1,0,8)*3;
    ctx.beginPath(); ctx.arc(t.x,t.y,this.r*(0.3+k*0.6),0,7); ctx.fill();
  }
  ctx.shadowBlur=0; ctx.globalAlpha=1;
  // ball
  var r=this.r;
  ctx.shadowColor=col; ctx.shadowBlur=10+G.frenzy*30;
  var g=ctx.createRadialGradient(px-r*0.3,py-r*0.3,r*0.2,px,py,r);
  g.addColorStop(0,'#ffffff'); g.addColorStop(0.4,col); g.addColorStop(1,shade(col,-40));
  ctx.fillStyle=g;
  ctx.beginPath(); ctx.arc(px,py,r,0,7); ctx.fill();
  ctx.shadowBlur=0;
  // kind markers
  if(this.kind==='fire'){
    ctx.fillStyle='rgba(255,200,80,0.9)'; ctx.font='bold 11px system-ui'; ctx.textAlign='center';
    ctx.fillText('★',px,py+4);
  }
  ctx.restore();
};
var BALL_COLORS={ normal:'#8fd6ff', fire:'#ff8a3d', giant:'#ffd45a', laser:'#ff5ad1', sticky:'#7dff8a' };
function shade(hex,amt){
  var n=parseInt(hex.slice(1),16), r=clamp((n>>16)+amt,0,255),g=clamp((n>>8&255)+amt,0,255),b=clamp((n&255)+amt,0,255);
  return 'rgb('+r+','+g+','+b+')';
}