'use strict';
// Paddle: moves in both axes within lower zone. Upward motion smashes ball.
function Paddle(){
  this.w=150; this.h=16;
  this.x=CFG.W/2; this.y=CFG.H*0.9;
  this.vx=0; this.vy=0;
  this.smash=0;        // >0 = recently smashed, for glow/boost
  this.magnet=0;
  this.target={x:this.x,y:this.y};
  this.maxSpeed=1400;
}
Paddle.prototype.zoneTop=function(){ return CFG.H*CFG.paddleZoneTop; };
Paddle.prototype.update=function(dt){
  var prevX=this.x, prevY=this.y;
  var inp=G.input.paddleTarget(dt,{x:this.x,y:this.y},this.maxSpeed);
  if(G.pw.expand>0) this.w=200; else this.w=150;
  if(inp.gp){
    this.x+=G.input.gpX*this.maxSpeed*dt;
    this.y+=G.input.gpY*this.maxSpeed*dt*0.7;
  } else {
    this.x=inp.x; this.y=inp.y;
  }
  this.x=clamp(this.x,this.w/2+8,CFG.W-this.w/2-8);
  this.y=clamp(this.y,this.zoneTop()+this.h,CFG.H-20);
  this.vx=(this.x-prevX)/dt; this.vy=(this.y-prevY)/dt;
  this.smash=Math.max(0,this.smash-dt*2.5);
};
Paddle.prototype.draw=function(ctx){
  var boost=clamp(G.mult/10,0,1);
  var col= this.smash>0? '#ffe08a' : mixCol('#7fd0ff','#ff9a4d',boost*0.6+G.frenzy*0.4);
  ctx.save();
  // movement zone guide
  ctx.globalAlpha=0.07+G.frenzy*0.08;
  ctx.fillStyle='#7fd0ff';
  ctx.fillRect(0,this.zoneTop(),CFG.W,CFG.H-this.zoneTop());
  ctx.globalAlpha=0.25; ctx.setLineDash([6,8]); ctx.strokeStyle='#7fd0ff'; ctx.lineWidth=1;
  ctx.beginPath(); ctx.moveTo(0,this.zoneTop()); ctx.lineTo(CFG.W,this.zoneTop()); ctx.stroke();
  ctx.setLineDash([]);
  ctx.globalAlpha=1;
  // glow
  ctx.shadowColor=col; ctx.shadowBlur=12+boost*30+G.frenzy*25;
  roundRect(ctx,this.x-this.w/2,this.y-this.h/2,this.w,this.h,8);
  ctx.fillStyle=col; ctx.fill();
  ctx.shadowBlur=0;
  // inner highlight + sticky indicator
  roundRect(ctx,this.x-this.w/2+4,this.y-this.h/2+3,this.w-8,4,2);
  ctx.fillStyle='rgba(255,255,255,0.5)'; ctx.fill();
  if(this.magnet>0||G.pw.magnet>0){
    ctx.strokeStyle='rgba(126,255,138,0.6)'; ctx.lineWidth=2; ctx.setLineDash([3,5]);
    ctx.beginPath(); ctx.arc(this.x,this.y,this.w*0.8,Math.PI,0); ctx.stroke(); ctx.setLineDash([]);
  }
  // aim guide while stuck/launch
  ctx.restore();
};
function mixCol(a,b,t){
  var pa=hexRGB(a), pb=hexRGB(b);
  return 'rgb('+Math.round(pa[0]+(pb[0]-pa[0])*t)+','+Math.round(pa[1]+(pb[1]-pa[1])*t)+','+Math.round(pa[2]+(pb[2]-pa[2])*t)+')';
}
function hexRGB(h){ return [parseInt(h.slice(1,3),16),parseInt(h.slice(3,5),16),parseInt(h.slice(5,7),16)]; }