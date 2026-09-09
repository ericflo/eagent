'use strict';
// Particles: brick debris, sparks, trails, ripples. Pooled-ish, array based.
function ParticleSys(){ this.list=[]; }
ParticleSys.prototype.clear=function(){ this.list.length=0; };
ParticleSys.prototype.spawn=function(o){ if(this.list.length<900) this.list.push(o); };
ParticleSys.prototype.burst=function(x,y,n,col,spd,life,size){
  for(var i=0;i<n;i++){
    var a=Math.random()*Math.PI*2, s=rand(spd*0.3,spd);
    this.spawn({x:x,y:y,vx:Math.cos(a)*s,vy:Math.sin(a)*s,life:rand(life*0.5,life),t:0,
      col:col,size:rand(size*0.5,size),grav:520,kind:'sq'});
  }
};
ParticleSys.prototype.debris=function(x,y,w,h,col){
  for(var i=0;i<10;i++){
    this.spawn({x:x+rand(-w/2,w/2),y:y+rand(-h/2,h/2),
      vx:rand(-160,160),vy:rand(-260,60),life:rand(0.5,1.0),t:0,col:col,
      size:rand(3,7),grav:600,kind:'sq',rot:rand(0,6),vr:rand(-8,8)});
  }
  this.burst(x,y,6,col,260,0.4,2.5);
};
ParticleSys.prototype.ripple=function(x,y,col){
  this.spawn({x:x,y:y,life:0.5,t:0,col:col,size:6,kind:'ripple',vx:0,vy:0,grav:0});
};
ParticleSys.prototype.spark=function(x,y,col){
  this.burst(x,y,8,col,320,0.3,2.5);
};
ParticleSys.prototype.trail=function(x,y,r,col,frenzy){
  this.spawn({x:x,y:y,vx:rand(-12,12),vy:rand(-12,12),life:0.28+frenzy*0.35,t:0,
    col:col,size:r*(0.75+frenzy*0.5),grav:0,kind:'glow'});
};
ParticleSys.prototype.update=function(dt){
  for(var i=this.list.length-1;i>=0;i--){
    var p=this.list[i]; p.t+=dt;
    if(p.t>=p.life){ this.list.splice(i,1); continue; }
    p.x+=p.vx*dt; p.y+=p.vy*dt; p.vy+=p.grav*dt;
    if(p.vr) p.rot+=p.vr*dt;
  }
};
ParticleSys.prototype.draw=function(ctx,alpha){
  ctx.save();
  for(var i=0;i<this.list.length;i++){
    var p=this.list[i], k=1-p.t/p.life; if(alpha!==undefined) k*=alpha;
    if(p.kind==='sq'){
      ctx.globalAlpha=k; ctx.fillStyle=p.col;
      ctx.save(); ctx.translate(p.x,p.y); if(p.rot) ctx.rotate(p.rot);
      ctx.fillRect(-p.size/2,-p.size/2,p.size,p.size); ctx.restore();
    } else if(p.kind==='glow'){
      ctx.globalAlpha=k*0.55; ctx.fillStyle=p.col;
      ctx.shadowColor=p.col; ctx.shadowBlur=12;
      ctx.beginPath(); ctx.arc(p.x,p.y,p.size*k,0,7); ctx.fill(); ctx.shadowBlur=0;
    } else if(p.kind==='ripple'){
      ctx.globalAlpha=k*0.7; ctx.strokeStyle=p.col; ctx.lineWidth=3*k;
      ctx.beginPath(); ctx.arc(p.x,p.y,p.size+p.t/p.life*46,0,7); ctx.stroke();
    }
  }
  ctx.restore(); ctx.globalAlpha=1;
};

// Floating score popups
function addPopup(x,y,text,col,big){
  G.popups.push({x:x,y:y,text:text,col:col||'#fff',t:0,life:0.9,big:!!big});
  if(G.popups.length>30) G.popups.shift();
}
function updatePopups(dt){
  for(var i=G.popups.length-1;i>=0;i--){
    var p=G.popups[i]; p.t+=dt; p.y-=60*dt;
    if(p.t>=p.life) G.popups.splice(i,1);
  }
}
function drawPopups(ctx){
  ctx.save(); ctx.textAlign='center';
  for(var i=0;i<G.popups.length;i++){
    var p=G.popups[i], k=1-p.t/p.life;
    ctx.globalAlpha=k; ctx.fillStyle=p.col;
    ctx.font=(p.big?'bold 34px ':'bold 20px ')+'system-ui,sans-serif';
    if(p.big){ ctx.shadowColor=p.col; ctx.shadowBlur=16; }
    ctx.fillText(p.text,p.x,p.y); ctx.shadowBlur=0;
  }
  ctx.restore(); ctx.globalAlpha=1;
}