// Keyboard + mouse + touch (drag anywhere, thumbstick ring, vertical offset).
export class Input {
  constructor(stage, canvas, ring, nub){
    this.stage=stage; this.canvas=canvas; this.ring=ring; this.nub=nub;
    this.keys={}; this.mouse={x:0,y:0,active:false,seen:false};
    this.touch={id:null,x:0,y:0};
    this.consumeTap=null; // callback for tap-to-launch etc
    this.attach();
  }
  toGame(cx,cy){
    const r=this.canvas.getBoundingClientRect();
    return { x:(cx-r.left)*(this.canvas._w/r.width), y:(cy-r.top)*(this.canvas._h/r.height) };
  }
  attach(){
    window.addEventListener('keydown',e=>{
      if(['ArrowLeft','ArrowRight','ArrowUp','ArrowDown',' '].includes(e.key)) e.preventDefault();
      this.keys[e.key.toLowerCase()]=true;
      if(this.consumeTap && (e.key===' '||e.key==='Enter')) this.consumeTap();
    });
    window.addEventListener('keyup',e=>{ this.keys[e.key.toLowerCase()]=false; });
    this.canvas.addEventListener('mousemove',e=>{
      const p=this.toGame(e.clientX,e.clientY);
      this.mouse.x=p.x; this.mouse.y=p.y; this.mouse.active=true; this.mouse.seen=true;
    });
    this.canvas.addEventListener('mouseleave',()=>{ this.mouse.active=false; });
    this.canvas.addEventListener('mousedown',e=>{
      const p=this.toGame(e.clientX,e.clientY);
      this.mouse.x=p.x; this.mouse.y=p.y; this.mouse.active=true;
      if(this.consumeTap) this.consumeTap();
    });
    // touch: drag anywhere; paddle follows with upward offset so visible
    const show=(x,y)=>{
      const r=this.canvas.getBoundingClientRect();
      this.ring.classList.remove('hidden');
      // position ring in stage coords: canvas fills stage so use css px within stage
      this.ring.style.left=(x/this.canvas._w*r.width)+'px';
      this.ring.style.top=(y/this.canvas._h*r.height)+'px';
      this.nub.style.transform='translate(-50%,-50%)';
    };
    this.stage.addEventListener('touchstart',e=>{
      e.preventDefault();
      const t=e.changedTouches[0];
      const p=this.toGame(t.clientX,t.clientY);
      this.touch.id=t.identifier; this.touch.x=p.x; this.touch.y=p.y;
      show(p.x,p.y+40);
      if(this.consumeTap) this.consumeTap();
    },{passive:false});
    this.stage.addEventListener('touchmove',e=>{
      e.preventDefault();
      for(const t of e.changedTouches){
        if(t.identifier===this.touch.id){
          const p=this.toGame(t.clientX,t.clientY);
          const dx=p.x-this.touch.x, dy=p.y-this.touch.y;
          this.touch.x=p.x; this.touch.y=p.y;
          show(p.x,p.y+40);
          this.nub.style.transform=`translate(calc(-50% + ${dx*0.6}px),calc(-50% + ${dy*0.6}px))`;
        }
      }
    },{passive:false});
    const end=e=>{
      for(const t of e.changedTouches){
        if(t.identifier===this.touch.id){ this.touch.id=null; this.ring.classList.add('hidden'); }
      }
    };
    this.stage.addEventListener('touchend',end); this.stage.addEventListener('touchcancel',end);
    document.addEventListener('touchmove',e=>{ if(e.cancelable) e.preventDefault(); },{passive:false});
  }
  // returns desired paddle target each physics step
  drive(paddle,w,h,dt){
    const b={minY:h-190,maxY:h-46};
    const spd=900;
    let kx=0,ky=0;
    const k=this.keys;
    if(k['arrowleft']||k['a'])kx-=1; if(k['arrowright']||k['d'])kx+=1;
    if(k['arrowup']||k['w'])ky-=1; if(k['arrowdown']||k['s'])ky+=1;
    if(kx||ky){ paddle.tx+=kx*spd*dt; paddle.ty+=ky*spd*dt*0.8; this.mouse.active=false; if(this.touch.id===null){} }
    else if(this.touch.id!==null){
      // finger maps with -70px vertical offset so paddle isn't under finger
      paddle.tx=this.touch.x;
      paddle.ty=this.touch.y-80;
      this.mouse.active=false;
    }
    else if(this.mouse.active&&this.mouse.seen){
      paddle.tx=this.mouse.x; paddle.ty=this.mouse.y;
    }
    paddle.tx=Math.max(20,Math.min(w-20,paddle.tx));
    paddle.ty=Math.max(b.minY,Math.min(b.maxY,paddle.ty));
  }
}
