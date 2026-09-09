'use strict';
// Animated starfield / nebula background; vibrancy scales with multiplier + frenzy.
function Background(){
  var n=110; this.stars=[];
  for(var i=0;i<n;i++) this.stars.push({
    x:Math.random()*CFG.W, y:Math.random()*CFG.H,
    z:rand(0.2,1), tw:rand(0,6), s:rand(0.6,2.2)
  });
  this.nebula=[];
  for(i=0;i<7;i++) this.nebula.push({
    x:Math.random()*CFG.W, y:Math.random()*CFG.H,
    r:rand(180,420), hue:rand(180,320), ph:rand(0,6)
  });
  this.t=0;
}
Background.prototype.update=function(dt){
  this.t+=dt;
  for(var i=0;i<this.stars.length;i++){
    var s=this.stars[i]; s.y+=(10+s.z*40+G.frenzy*260)*dt;
    if(s.y>CFG.H){ s.y=-5; s.x=Math.random()*CFG.W; }
  }
};
Background.prototype.draw=function(ctx){
  var energy=clamp((G.mult-1)/8,0,1)*0.7 + G.frenzy*0.6;
  ctx.fillStyle='#05060f'; ctx.fillRect(0,0,CFG.W,CFG.H);
  // nebula blobs
  ctx.save(); ctx.globalCompositeOperation='lighter';
  for(var i=0;i<this.nebula.length;i++){
    var nb=this.nebula[i];
    var ox=Math.sin(this.t*0.13+nb.ph)*40, oy=Math.cos(this.t*0.11+nb.ph)*30;
    var a=0.05+energy*0.16;
    var g=ctx.createRadialGradient(nb.x+ox,nb.y+oy,0,nb.x+ox,nb.y+oy,nb.r);
    g.addColorStop(0,'hsla('+(nb.hue+energy*60)+',80%,60%,'+a+')');
    g.addColorStop(1,'hsla('+nb.hue+',80%,50%,0)');
    ctx.fillStyle=g;
    ctx.beginPath(); ctx.arc(nb.x+ox,nb.y+oy,nb.r,0,7); ctx.fill();
  }
  // stars
  for(i=0;i<this.stars.length;i++){
    var s=this.stars[i];
    var twk=0.5+0.5*Math.sin(this.t*3+s.tw);
    ctx.globalAlpha=(0.25+s.z*0.55)*twk+energy*0.25;
    ctx.fillStyle=energy>0.5?'#cfe9ff':'#9fb8dd';
    ctx.fillRect(s.x,s.y,s.s+energy*s.z,s.s+energy*s.z);
  }
  ctx.restore(); ctx.globalAlpha=1;
};