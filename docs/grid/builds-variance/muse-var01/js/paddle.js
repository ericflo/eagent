// Paddle: 2D movement in a vertical band, lerp smoothing, squash/stretch, boost.
export class Paddle {
  constructor(){
    this.x=0; this.y=0; this.tx=0; this.ty=0;
    this.vx=0; this.vy=0; this.px=0; this.py=0;
    this.w=112; this.h=15; this.baseW=112;
    this.wideT=0; this.squash=0; this.stretch=0; this.boostFlash=0;
    this.caughtShield=false;
  }
  reset(w,h){
    this.baseW=Math.max(84,Math.min(130,w*0.23));
    this.w=this.baseW;
    this.tx=w/2; this.ty=h-70; this.x=this.tx; this.y=this.ty;
    this.vx=this.vy=0; this.wideT=0; this.boostFlash=0;
  }
  band(h){ return {minY:h-190, maxY:h-46}; }
  update(dt,w,h){
    const b=this.band(h);
    this.tx=Math.max(this.w/2+4,Math.min(w-this.w/2-4,this.tx));
    this.ty=Math.max(b.minY,Math.min(b.maxY,this.ty));
    const k=1-Math.pow(0.0001,dt); // smooth lerp
    const nx=this.x+(this.tx-this.x)*k, ny=this.y+(this.ty-this.y)*k;
    this.vx=(nx-this.x)/Math.max(dt,1e-4); this.vy=(ny-this.y)/Math.max(dt,1e-4);
    this.px=this.x; this.py=this.y; this.x=nx; this.y=ny;
    // clamp velocity readouts
    this.vx=Math.max(-2400,Math.min(2400,this.vx));
    this.vy=Math.max(-2400,Math.min(2400,this.vy));
    this.squash=Math.max(0,this.squash-dt*5);
    this.stretch=Math.max(0,this.stretch-dt*5);
    this.boostFlash=Math.max(0,this.boostFlash-dt*2.2);
    if(this.wideT>0){ this.wideT-=dt; this.w=this.baseW*1.55; if(this.wideT<=0) this.w=this.baseW; }
  }
  draw(ctx,od){
    const blur=Math.min(26,Math.abs(this.vx)/60);
    ctx.save();
    ctx.translate(this.x,this.y);
    ctx.globalCompositeOperation='lighter';
    // motion blur ghosts
    if(Math.abs(this.vx)>120){
      ctx.globalAlpha=0.25;
      ctx.fillStyle='#00f0ff';
      roundRect(ctx,-this.w/2-Math.sign(this.vx)*blur*0.4,-this.h/2,this.w,this.h,8); ctx.fill();
    }
    ctx.globalAlpha=1;
    const sx=1+this.squash*0.35+Math.min(0.25,Math.abs(this.vx)/4000);
    const sy=1-this.squash*0.3+this.stretch*0.4;
    ctx.scale(sx,sy);
    const g=ctx.createLinearGradient(-this.w/2,0,this.w/2,0);
    if(this.boostFlash>0){ g.addColorStop(0,'#ffffff'); g.addColorStop(0.5,'#a6ff00'); g.addColorStop(1,'#ffffff'); }
    else if(od){ g.addColorStop(0,'#ff2fd6'); g.addColorStop(0.5,'#ffffff'); g.addColorStop(1,'#ffd400'); }
    else { g.addColorStop(0,'#00f0ff'); g.addColorStop(0.5,'#ffffff'); g.addColorStop(1,'#ff2fd6'); }
    ctx.shadowBlur=18+this.boostFlash*30; ctx.shadowColor=this.boostFlash>0?'#a6ff00':'#00f0ff';
    ctx.fillStyle=g;
    roundRect(ctx,-this.w/2,-this.h/2,this.w,this.h,8); ctx.fill();
    ctx.shadowBlur=0;
    ctx.fillStyle='rgba(255,255,255,.85)';
    roundRect(ctx,-this.w/2+6,-2.5,this.w-12,5,3); ctx.fill();
    ctx.restore();
    // boost flash ring
    if(this.boostFlash>0){
      ctx.save(); ctx.globalAlpha=this.boostFlash; ctx.globalCompositeOperation='lighter';
      ctx.strokeStyle='#a6ff00'; ctx.lineWidth=3; ctx.shadowBlur=16; ctx.shadowColor='#a6ff00';
      ctx.beginPath(); ctx.ellipse(this.x,this.y,this.w*0.7,20+this.boostFlash*22,0,0,6.283); ctx.stroke();
      ctx.restore();
    }
  }
}
export function roundRect(ctx,x,y,w,h,r){
  r=Math.min(r,w/2,h/2);
  ctx.beginPath();
  ctx.moveTo(x+r,y); ctx.arcTo(x+w,y,x+w,y+h,r); ctx.arcTo(x+w,y+h,x,y+h,r);
  ctx.arcTo(x,y+h,x,y,r); ctx.arcTo(x,y,x+w,y,r); ctx.closePath();
}
