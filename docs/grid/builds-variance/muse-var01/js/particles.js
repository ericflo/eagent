// Particles, shockwaves, popups, starfield.
export class FX {
  constructor(){ this.parts=[]; this.rings=[]; this.pops=[]; this.stars=[]; this.t=0; }
  initStars(w,h){
    this.stars=[];
    const n = Math.floor((w*h)/9000);
    for(let i=0;i<n;i++) this.stars.push({x:Math.random()*w,y:Math.random()*h,
      z:0.2+Math.random()*0.8, s:Math.random()*2+0.5, tw:Math.random()*6.28});
  }
  burst(x,y,color,n=18,speed=260,life=0.7,size=3){
    for(let i=0;i<n;i++){
      const a=Math.random()*6.283, sp=speed*(0.3+Math.random()*0.9);
      this.parts.push({x,y,vx:Math.cos(a)*sp,vy:Math.sin(a)*sp-60,
        life:life*(0.5+Math.random()*0.7),max:life,color,size:size*(0.5+Math.random()),
        grav:500,add:true});
      if(this.parts.length>700) this.parts.shift();
    }
  }
  sparks(x,y,color,n=8){ this.burst(x,y,color,n,420,0.4,2); }
  trail(x,y,color,size=3,life=0.35){
    this.parts.push({x,y,vx:(Math.random()-0.5)*30,vy:(Math.random()-0.5)*30,
      life,max:life,color,size,grav:0,add:true});
  }
  ring(x,y,color,maxR=70,dur=0.4){
    this.rings.push({x,y,r:6,maxR,t:0,dur,color});
  }
  pop(x,y,text,color='#ffd400',size=15){
    this.pops.push({x,y,text,color,size,t:0,dur:1.0});
    if(this.pops.length>24) this.pops.shift();
  }
  update(dt){
    this.t+=dt;
    for(const p of this.parts){ p.life-=dt; p.x+=p.vx*dt; p.y+=p.vy*dt; p.vy+=(p.grav||0)*dt; p.vx*=0.99; }
    this.parts=this.parts.filter(p=>p.life>0);
    for(const r of this.rings){ r.t+=dt; r.r=6+(r.maxR-6)*(r.t/r.dur); }
    this.rings=this.rings.filter(r=>r.t<r.dur);
    for(const p of this.pops){ p.t+=dt; p.y-=46*dt; }
    this.pops=this.pops.filter(p=>p.t<p.dur);
    for(const s of this.stars){ s.tw+=dt*3; }
  }
  drawBg(ctx,w,h,od,mult){
    // gradient wash
    const g=ctx.createLinearGradient(0,0,0,h);
    if(od){ g.addColorStop(0,'#2b0a33'); g.addColorStop(0.55,'#120a30'); g.addColorStop(1,'#05051a'); }
    else { g.addColorStop(0,'#121242'); g.addColorStop(0.55,'#0a0a26'); g.addColorStop(1,'#05051a'); }
    ctx.fillStyle=g; ctx.fillRect(0,0,w,h);
    // stars
    ctx.save();
    for(const s of this.stars){
      const a=0.25+0.55*Math.abs(Math.sin(s.tw))+(od?0.2:0);
      ctx.globalAlpha=Math.min(1,a);
      ctx.fillStyle=od?'#ffd7f5':'#9adcff';
      const sz=s.s*(od?1.6:1)*(1+mult/200);
      ctx.fillRect(s.x,s.y,sz,sz*(2+s.z*3));
    }
    ctx.restore(); ctx.globalAlpha=1;
    // slow nebula blobs
    ctx.save(); ctx.globalCompositeOperation='lighter'; ctx.globalAlpha=od?0.16:0.08;
    const t=this.t*0.4;
    blob(ctx,w*0.2+Math.sin(t)*40,h*0.3,120,od?'#ff2fd6':'#00f0ff');
    blob(ctx,w*0.85+Math.cos(t*0.7)*50,h*0.55,150,od?'#ffd400':'#3a3aff');
    ctx.restore(); ctx.globalAlpha=1;
  }
  draw(ctx,od){
    ctx.save();
    for(const p of this.parts){
      const a=Math.max(0,p.life/p.max);
      ctx.globalAlpha=a;
      if(p.add) ctx.globalCompositeOperation='lighter';
      else ctx.globalCompositeOperation='source-over';
      ctx.fillStyle=p.color;
      ctx.shadowBlur=8; ctx.shadowColor=p.color;
      ctx.fillRect(p.x-p.size/2,p.y-p.size/2,p.size,p.size);
    }
    ctx.restore(); ctx.globalAlpha=1; ctx.shadowBlur=0;
    ctx.save();
    for(const r of this.rings){
      const a=1-r.t/r.dur;
      ctx.globalAlpha=a; ctx.globalCompositeOperation='lighter';
      ctx.strokeStyle=r.color; ctx.lineWidth=3;
      ctx.shadowBlur=14; ctx.shadowColor=r.color;
      ctx.beginPath(); ctx.arc(r.x,r.y,r.r,0,6.283); ctx.stroke();
    }
    ctx.restore(); ctx.globalAlpha=1; ctx.shadowBlur=0;
    ctx.save(); ctx.textAlign='center';
    for(const p of this.pops){
      const a=1-p.t/p.dur;
      ctx.globalAlpha=a;
      ctx.font=`900 ${p.size}px ui-sans-serif,system-ui`;
      ctx.fillStyle=p.color; ctx.shadowBlur=10; ctx.shadowColor=p.color;
      ctx.fillText(p.text,p.x,p.y);
    }
    ctx.restore(); ctx.globalAlpha=1; ctx.shadowBlur=0;
  }
}
function blob(ctx,x,y,r,c){ const g=ctx.createRadialGradient(x,y,0,x,y,r);
  g.addColorStop(0,c); g.addColorStop(1,'transparent'); ctx.fillStyle=g;
  ctx.fillRect(x-r,y-r,r*2,r*2); }
