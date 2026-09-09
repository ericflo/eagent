'use strict';
// Falling pickups caught by paddle + power-ball drops.
var PICKUPS = {
  expand: { col:'#7fd0ff', icon:'⬌', label:'WIDE' },
  slow:   { col:'#b48aff', icon:'⧗', label:'SLOW-MO' },
  multi:  { col:'#ffd45a', icon:'●●●', label:'MULTIBALL' },
  life:   { col:'#7dff8a', icon:'♥', label:'LIFE' },
  magnet: { col:'#ff9adf', icon:'∪', label:'MAGNET' },
};
var BALL_DROPS = { fire:'FIREBALL', split:'SPLIT', giant:'GIANT', laser:'LASER', sticky:'STICKY' };
function Pickup(x,y,kind,isBall){
  this.x=x; this.y=y; this.kind=kind; this.ball=!!isBall;
  this.t=0; this.w=this.ball?64:44; this.h=this.ball?26:34;
  this.col= isBall? BALL_COLORS[kind] : PICKUPS[kind].col;
}
Pickup.prototype.update=function(dt){
  this.t+=dt; this.y+=CFG.pickupFall*dt;
  // gentle sway
  this.x+=Math.sin(this.t*4)*20*dt;
};
Pickup.prototype.draw=function(ctx){
  ctx.save(); ctx.translate(this.x,this.y);
  var bob=Math.sin(this.t*6)*2;
  ctx.translate(0,bob);
  ctx.shadowColor=this.col; ctx.shadowBlur=12;
  roundRect(ctx,-this.w/2,-this.h/2,this.w,this.h,8);
  ctx.fillStyle='rgba(10,14,22,0.85)'; ctx.fill();
  ctx.strokeStyle=this.col; ctx.lineWidth=2.5; ctx.stroke(); ctx.shadowBlur=0;
  ctx.fillStyle=this.col; ctx.textAlign='center'; ctx.font='bold 12px system-ui';
  if(this.ball){ ctx.fillText(BALL_DROPS[this.kind],0,4); }
  else { ctx.font='bold 16px system-ui'; ctx.fillText(PICKUPS[this.kind].icon,0,6); }
  ctx.restore();
};