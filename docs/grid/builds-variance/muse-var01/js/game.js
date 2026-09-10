// Game orchestration: loop, physics, overdrive, powerups, scoring, render.
import { AudioSys } from './audio.js';
import { LEVELS, BRICK_INFO } from './levels.js';
import { FX } from './particles.js';
import { Paddle } from './paddle.js';
import { makeBall, ballSpeed, setSpeed, updateBalls, drawBalls, primaryType } from './balls.js';
import { buildBricks, qualifies, circleBrickNormal, brickBottom, brickTop } from './bricks.js';
import { drawBricks } from './bricks.js';
import { Input } from './input.js';
import { UI } from './ui.js';

const STEP = 1/120;
const CAP_KINDS = [
  {k:'multi', icon:'≡', name:'MULTIBALL ×3', color:'#00f0ff', w:18},
  {k:'fire', icon:'🔥', name:'FIREBALL!', color:'#ff6a00', w:14},
  {k:'ghost', icon:'👻', name:'GHOST PHASE!', color:'#c77dff', w:12},
  {k:'heavy', icon:'🪨', name:'HEAVY BALL!', color:'#ffd400', w:12},
  {k:'volt', icon:'⚡', name:'VOLT BALL!', color:'#fff36e', w:8},
  {k:'slow', icon:'🕐', name:'SLOW-MO!', color:'#7dffea', w:8},
  {k:'wide', icon:'↔', name:'WIDE PADDLE!', color:'#6ec6ff', w:12},
  {k:'shield', icon:'🛡', name:'SHIELD NET!', color:'#9adcff', w:12},
  {k:'life', icon:'♥', name:'EXTRA LIFE!', color:'#ff5d7e', w:4},
];
export class Game {
  constructor(canvas, stage){
    this.canvas=canvas; this.stage=stage;
    this.ctx=canvas.getContext('2d');
    this.audio=new AudioSys();
    this.fx=new FX(); this.paddle=new Paddle(); this.ui=new UI();
    this.input=new Input(stage,canvas,
      document.getElementById('stick-ring'),document.getElementById('stick-nub'));
    this.w=480; this.h=720;
    this.state='menu'; // menu|serve|play|pause|clear|over|win
    this.level=0; this.score=0;
    this.hi=this.loadHi();
    this.lives=3; this.mult=1;
    this.od={active:false,t:0,tick:0};
    this.combo=0; this.comboT=0; this.brickNote=0;
    this.balls=[]; this.bricks=[]; this.caps=[];
    this.pend={fire:0,ghost:0,heavy:0};
    this.slowT=0; this.slowMax=8; this.shieldT=0; this.shieldMax=20;
    this.shake=0; this.freeze=0; this.time=0; this.acc=0; this.last=0;
    this.odBonusFrac=0; this.mute=false;
    this.resize(); window.addEventListener('resize',()=>this.resize());
    document.addEventListener('visibilitychange',()=>{
      if(document.hidden&&(this.state==='play'||this.state==='serve')) this.togglePause();
    });
    window.addEventListener('blur',()=>{
      if(this.state==='play') this.togglePause();
    });
    // Long-press context menu would interrupt touch play on mobile.
    this.stage.addEventListener('contextmenu',e=>e.preventDefault());
    this.input.consumeTap=()=>this.tap();
    this.wireButtons();
    this.ui.buildLegend(); this.ui.buildLevelBtns(LEVELS.length,(i)=>this.startFrom(i));
    this.ui.setStartHi(this.hi);
    this.loadLevel(0,true);
  }
  loadHi(){
    try { return parseInt(localStorage.getItem('neon-overtop-hi')||'0',10)||0; }
    catch(e){ return 0; }
  }
  saveHi(){
    try { localStorage.setItem('neon-overtop-hi',String(this.hi)); } catch(e){}
  }
  bumpHi(){
    if(this.score>this.hi){ this.hi=this.score; this.saveHi(); }
  }
  wireButtons(){
    const on=(id,fn)=>document.getElementById(id).addEventListener('click',()=>{this.audio.ensure();fn();});
    on('btn-start',()=>this.startFrom(this.level));
    on('btn-how',()=>{this.ui.hide('ov-start');this.ui.show('ov-help');});
    on('btn-help-close',()=>{this.ui.hide('ov-help');if(this.state==='menu')this.ui.show('ov-start');});
    on('btn-pause',()=>this.togglePause());
    on('btn-resume',()=>this.togglePause());
    on('btn-restart-p',()=>this.startFrom(this.level));
    on('btn-next',()=>{this.level=(this.level+1)%LEVELS.length;this.startFrom(this.level);});
    on('btn-again',()=>this.startFrom(0));
    on('btn-menu',()=>{this.state='menu';this.ui.hideAll();this.ui.show('ov-start');this.ui.setStartHi(this.hi);});
    on('btn-mute',()=>this.toggleMute());
    on('btn-help',()=>{this.ui.show('ov-help');});
    window.addEventListener('keydown',e=>{
      const k=e.key.toLowerCase();
      if(k==='p'||k==='escape') this.togglePause();
      if(k==='m') this.toggleMute();
      if(k==='h'){ document.getElementById('ov-help').classList.toggle('hidden'); }
    });
  }
  toggleMute(){
    this.mute=!this.mute; this.audio.ensure(); this.audio.setMuted(this.mute);
    document.getElementById('btn-mute').textContent=this.mute?'🔇':'🔊';
  }
  togglePause(){
    if(this.state==='play'||this.state==='serve'){
      this.prevState=this.state; this.state='pause'; this.ui.show('ov-pause');
    } else if(this.state==='pause'){
      this.state=this.prevState||'play'; this.ui.hide('ov-pause'); this.last=performance.now();
    }
  }
  startFrom(i){
    this.audio.ensure();
    this.level=i; this.score=0; this.lives=3; this.mult=1;
    this.od={active:false,t:0,tick:0}; this.combo=0; this.comboT=0; this.brickNote=0;
    this.pend={fire:0,ghost:0,heavy:0}; this.slowT=0; this.shieldT=0;
    this.shake=0; this.freeze=0; this.acc=0; this.odBonusFrac=0;
    this.stage.classList.remove('od');
    this.ui.combo(0); this.ui.hint(false);
    this.loadLevel(i);
    this.ui.hideAll();
    this.state='serve';
    this.ui.banner(`LEVEL ${i+1}<small>${LEVELS[i].name}</small>`,1800);
  }
  loadLevel(i,preview=false){
    this.bricks=buildBricks(LEVELS[i].map,this.w);
    this.caps=[]; this.balls=[];
    this.paddle.reset(this.w,this.h);
    this.spawnServeBall();
    this.fx.initStars(this.w,this.h);
    if(!preview) this.paddle.reset(this.w,this.h);
  }
  spawnServeBall(){
    const b=makeBall(this.paddle.x,this.paddle.y-30,-Math.PI/2,460);
    this.applyPend(b); this.balls=[b];
  }
  applyPend(b){
    if(this.pend.fire>0)b.fireT=Math.max(b.fireT,4);
    if(this.pend.ghost>0)b.ghostT=Math.max(b.ghostT,4);
    if(this.pend.heavy>0)b.heavyT=Math.max(b.heavyT,4);
  }
  tap(){
    this.audio.ensure();
    if(this.state==='serve'){
      for(const b of this.balls) if(b.stuck){
        b.stuck=false;
        const a=-Math.PI/2+(Math.random()*0.5-0.25);
        const sp=460+this.level*18;
        b.vx=Math.cos(a)*sp; b.vy=Math.sin(a)*sp;
        this.audio.launch();
      }
      this.state='play';
    }
  }
  baseSpeed(){ return 450+this.level*22; }
  // ---- scoring ----
  addScore(base,x,y,label=null){
    const pts=Math.round(base*this.mult);
    this.score+=pts;
    this.bumpHi();
    this.fx.pop(x,y,`+${pts}`,this.od.active?'#ff8ef5':'#ffd400',this.od.active?18:14);
    return pts;
  }
  breakBrick(br,ball,viaBlast=false){
    if(!br.alive) return;
    br.alive=false; br.flash=0;
    const info=BRICK_INFO[br.type];
    const cx=br.x+br.w/2, cy=br.y+br.h/2;
    // juice
    this.fx.burst(cx,cy,info.glow,br.type==='B'?40:20,br.type==='B'?420:280,0.8,3.5);
    this.fx.sparks(cx,cy,'#ffffff',6);
    this.fx.ring(cx,cy,info.glow,br.type==='B'?130:60,br.type==='B'?0.55:0.35);
    // combo
    this.combo++; this.comboT=2.5; this.ui.combo(this.combo);
    this.brickNote++;
    this.audio.brick(this.brickNote,this.combo);
    const bonus=this.combo>=4?50*this.combo:0;
    this.addScore(50+bonus+(viaBlast?25:0),cx,cy);
    if(ball&&primaryType(ball)==='heavy'){ this.shake=Math.min(14,this.shake+7); }
    else this.shake=Math.min(10,this.shake+2.5);
    // volt split
    if(ball&&ball.volt&&!ball.splitUsed&&this.balls.length<6&&Math.random()<0.4){
      ball.splitUsed=true;
      const c=makeBall(ball.x,ball.y,0,ballSpeed(ball)*1.05,{volt:true,splitUsed:true,r:7});
      c.vx=-ball.vx*0.9; c.vy=ball.vy; c.stuck=false;
      c.trail=[...ball.trail];
      this.balls.push(c);
      this.fx.ring(ball.x,ball.y,'#fff36e',50,0.3);
    }
    // bomb blast
    if(br.type==='B') this.detonate(br,ball);
    // fire blast radius
    if(ball&&ball.fireT>0&&!viaBlast) this.fireBlast(cx,cy,ball);
    // maybe drop capsule
    if(!viaBlast&&Math.random()<0.20) this.dropCapsule(cx,cy);
    // overdrive entry hit-stop
    if(this.od.active&&this.combo===5){ this.freeze=Math.max(this.freeze,0.06); }
    this.checkClear();
  }
  detonate(br,ball){
    this.audio.explosion();
    this.shake=Math.min(22,this.shake+12);
    this.freeze=Math.max(this.freeze,0.09);
    const dc=br.col, dr=br.row;
    const victims=this.bricks.filter(o=>o.alive&&Math.abs(o.col-dc)<=1&&Math.abs(o.row-dr)<=1&&o!==br);
    for(const v of victims){
      if(v.type==='B'&&Math.random()<0.9){ this.breakBrick(v,ball,true); } // chain
      else if(v.type==='F'||v.type==='G'){
        // blast only breaks elementals if ball qualifies
        if(ball&&qualifies(v,ball).ok) this.breakBrick(v,ball,true);
      }
      else this.breakBrick(v,ball,true);
    }
  }
  fireBlast(x,y,ball){
    for(const o of this.bricks){
      if(!o.alive) continue;
      const ox=o.x+o.w/2, oy=o.y+o.h/2;
      if(Math.hypot(ox-x,oy-y)<78&&o.type!=='B'){
        if(qualifies(o,ball).ok) this.breakBrick(o,ball,true);
      }
    }
  }
  dropCapsule(x,y){
    let total=0; for(const c of CAP_KINDS) total+=c.w;
    let r=Math.random()*total, pick=CAP_KINDS[0];
    for(const c of CAP_KINDS){ r-=c.w; if(r<=0){pick=c;break;} }
    if(pick.k==='life'&&this.lives>=5) pick=CAP_KINDS[0];
    this.caps.push({x,y,vy:120,kind:pick.k,icon:pick.icon,name:pick.name,color:pick.color,t:0});
  }
  collectCap(c){
    this.audio.powerup();
    this.ui.toast(c.name||c.kind);
    this.fx.ring(c.x,this.h-90,'#ffffff',60,0.35);
    switch(c.kind){
      case 'multi': {
        const cur=this.balls.filter(b=>!b.stuck);
        const src=cur.length?cur:[...this.balls];
        for(let i=0;i<2&&this.balls.length<8;i++){
          const s=src[i%src.length]||this.balls[0];
          const nb=makeBall(s.x,s.y,0,Math.max(ballSpeed(s),this.baseSpeed()),{});
          const a=Math.atan2(s.vy,s.vx)+(i===0?0.5:-0.5);
          const sp=ballSpeed(s)||this.baseSpeed();
          nb.vx=Math.cos(a)*sp; nb.vy=Math.sin(a)*sp; nb.stuck=false;
          this.applyPend(nb); this.balls.push(nb);
        }
        break; }
      case 'fire': this.pend.fire=12; for(const b of this.balls)b.fireT=Math.max(b.fireT,12); break;
      case 'ghost': this.pend.ghost=10; for(const b of this.balls)b.ghostT=Math.max(b.ghostT,10); break;
      case 'heavy': this.pend.heavy=12; for(const b of this.balls)b.heavyT=Math.max(b.heavyT,12); break;
      case 'volt': {
        const b=this.balls[0];
        if(b){ const v=makeBall(b.x,b.y-20,-Math.PI/3,720,{volt:true}); v.stuck=false; this.balls.push(v); }
        break; }
      case 'slow': this.slowT=this.slowMax; break;
      case 'wide': this.paddle.wideT=15; this.paddle.w=this.paddle.baseW*1.55; break;
      case 'shield': this.shieldT=this.shieldMax; break;
      case 'life': this.lives=Math.min(6,this.lives+1); this.audio.life(); break;
    }
  }
  checkClear(){
    if(this.bricks.some(b=>b.alive)) return;
    if(this.state!=='play'&&this.state!=='serve') return;
    const bonus=500*(this.level+1);
    this.score+=bonus;
    this.bumpHi();
    this.audio.levelClear();
    this.fx.ring(this.w/2,this.h/2,'#ffd400',300,0.8);
    if(this.level>=LEVELS.length-1){
      this.state='win';
      this.ui.hideAll();
      document.getElementById('over-title').textContent='🏆 YOU BEAT THE GRID!';
      document.getElementById('over-score').textContent=this.score.toLocaleString();
      document.getElementById('over-hi').textContent=this.hi.toLocaleString();
      document.getElementById('over-sub').textContent='Every level cleared. Overtop legend.';
      this.ui.show('ov-over'); this.audio.victory();
    } else {
      this.state='clear';
      document.getElementById('clear-title').textContent=`LEVEL ${this.level+1} CLEAR!`;
      document.getElementById('clear-sub').innerHTML=`Bonus <b>+${bonus}</b> · Score <b>${this.score.toLocaleString()}</b><br/><span class="dim">Next: ${LEVELS[this.level+1].name}</span>`;
      this.ui.show('ov-clear');
    }
    this.od.active=false; this.mult=1;
    this.stage.classList.remove('od');
  }
  onPaddle(b,p){
    const rel=Math.max(-1,Math.min(1,(b.x-p.x)/(p.w/2)));
    let sp=ballSpeed(b);
    const upBoost=p.vy<-180, soften=p.vy>220;
    if(upBoost){ sp*=(1.10+Math.min(0.22,-p.vy/4500)); p.boostFlash=1; p.stretch=1;
      this.ui.boostFlash(); this.audio.boost();
      this.fx.ring(b.x,b.y,'#a6ff00',55,0.3);
      this.addScore(10,b.x,b.y);
    } else if(soften){ sp*=0.92; }
    sp=Math.min(1150,Math.max(300,sp*1.005));
    const maxA=1.05;
    b.vx=Math.sin(rel*maxA)*sp;
    b.vy=-Math.abs(Math.cos(rel*maxA)*sp)-40;
    b.y=p.y-p.h/2-b.r-1;
    p.squash=1;
    this.audio.paddle((rel+1)/2);
    this.fx.sparks(b.x,b.y,'#ffffff',4);
  }
  lifeLost(){
    if(this.state!=='play'&&this.state!=='serve') return;
    this.lives--;
    this.audio.loseLife();
    this.combo=0; this.ui.combo(0);
    this.od.active=false; this.mult=1; this.od.t=0;
    this.stage.classList.remove('od');
    this.shake=8;
    if(this.lives<=0){
      this.state='over';
      this.ui.hideAll();
      document.getElementById('over-title').textContent='GAME OVER';
      document.getElementById('over-score').textContent=this.score.toLocaleString();
      document.getElementById('over-hi').textContent=this.hi.toLocaleString();
      document.getElementById('over-sub').textContent=`Reached level ${this.level+1} · ${LEVELS[this.level].name}`;
      this.ui.show('ov-over'); this.audio.gameOver();
    } else {
      this.state='serve';
      this.pend={fire:0,ghost:0,heavy:0};
      this.spawnServeBall();
      this.ui.toast(`BALL LOST — ${this.lives} LEFT`);
    }
  }
  step(dt){
    this.time+=dt;
    if(this.freeze>0){ this.freeze-=dt; return; }
    const ts=this.slowT>0?0.55:1;
    if(this.slowT>0)this.slowT-=dt;
    for(const k of ['fire','ghost','heavy']) if(this.pend[k]>0)this.pend[k]-=dt;
    if(this.shieldT>0)this.shieldT-=dt;
    if(this.pend.fire>0)for(const b of this.balls)b.fireT=Math.max(b.fireT,dt*3);
    if(this.pend.ghost>0)for(const b of this.balls)b.ghostT=Math.max(b.ghostT,dt*3);
    if(this.pend.heavy>0)for(const b of this.balls)b.heavyT=Math.max(b.heavyT,dt*3);
    const sdt=dt*ts;
    if(this.comboT>0){ this.comboT-=dt; if(this.comboT<=0){this.combo=0;this.ui.combo(0);} }
    for(const b of this.bricks){ b.flash=Math.max(0,(b.flash||0)-dt*4); if(b.denied>0)b.denied-=dt;
      if(b.type==='H'&&b.alive){ b.x=b.baseX+Math.sin(this.time*1.6+b.phase)*Math.min(46,this.w*0.09); } }
    this.input.drive(this.paddle,this.w,this.h,dt);
    this.paddle.update(dt,this.w,this.h);
    if(this.state==='serve'){
      for(const b of this.balls){ b.x=this.paddle.x; b.y=this.paddle.y-this.paddle.h/2-b.r-2; }
    } else if(this.state==='play'){
      this.balls=updateBalls(this.balls,sdt,this.w,this.h,this.paddle,
        {audio:this.audio,slowmo:this.slowT>0},
        ()=>{},(b,p)=>this.onPaddle(b,p));
      this.collideBricks(sdt);
      if(this.balls.length===0){ this.lifeLost(); }
      if(this.shieldT>0){
        for(const b of this.balls){
          if(b.vy>0&&b.y>this.h-26){ b.y=this.h-26; b.vy=-Math.abs(b.vy);
            this.audio.paddle(0.5); this.fx.ring(b.x,this.h-24,'#9adcff',50,0.3); }
        }
      }
      for(const c of this.caps){ c.t+=dt; c.y+=c.vy*sdt;
        if(Math.abs(c.x-this.paddle.x)<this.paddle.w/2+12&&
           Math.abs(c.y-this.paddle.y)<22){ c.got=true; this.collectCap(c); }
      }
      this.caps=this.caps.filter(c=>!c.got&&c.y<this.h+20);
      this.updateOverdrive(dt);
    }
    this.fx.update(dt);
    this.audio.update(this.od.active,this.mult);
  }
  collideBricks(sdt){
    for(const b of this.balls){
      if(b.stuck) continue;
      for(const br of this.bricks){
        if(!br.alive) continue;
        const n=circleBrickNormal(b,br);
        if(!n) continue;
        const q=qualifies(br,b);
        const ghost=b.ghostT>0;
        if(ghost&&br.type!=='G'){
          if(q.ok){ this.breakBrick(br,b); }
          continue;
        }
        if(!q.ok){
          b.x+=n.nx*(n.depth+0.5); b.y+=n.ny*(n.depth+0.5);
          const dot=b.vx*n.nx+b.vy*n.ny;
          if(dot<0){ b.vx-=2*dot*n.nx; b.vy-=2*dot*n.ny; }
          if(br.denied<=0){ br.denied=1.2; br.deniedMsg=q.msg;
            this.fx.pop(b.x,Math.max(30,b.y-14),q.msg.split('—')[0].trim(),'#ff8e9a',12); }
          if(q.sizzle) this.audio.sizzle(); else this.audio.denied();
          this.fx.sparks(b.x,b.y,'#ff5d7e',5);
          break;
        }
        b.x+=n.nx*(n.depth+0.5); b.y+=n.ny*(n.depth+0.5);
        const dot=b.vx*n.nx+b.vy*n.ny;
        if(dot<0){ b.vx-=2*dot*n.nx; b.vy-=2*dot*n.ny; }
        br.flash=1;
        this.breakBrick(br,b);
        break;
      }
    }
  }
  updateOverdrive(dt){
    const alive=this.bricks.some(b=>b.alive);
    const top=alive?brickTop(this.bricks):Infinity;
    const anyAbove=alive&&this.balls.some(b=>!b.stuck&&b.y<top);
    if(anyAbove){
      if(!this.od.active){
        this.od.active=true; this.od.t=0; this.od.tick=0;
        this.stage.classList.add('od');
        this.ui.banner('OVERDRIVE!<small>RIDE IT — ×2 ×4 ×8 … ×99</small>',1500);
        this.audio.odEnter();
        this.freeze=Math.max(this.freeze,0.07);
        this.shake=Math.min(16,this.shake+8);
        this.fx.ring(this.w/2,120,'#ff2fd6',200,0.6);
      }
      this.od.t+=dt;
      this.mult=Math.min(99,2*Math.pow(2,Math.floor(this.od.t/3.5)));
      this.odBonusFrac+=this.mult*10*dt;
      const whole=Math.floor(this.odBonusFrac);
      if(whole>0){ this.odBonusFrac-=whole; this.score+=whole; this.bumpHi(); }
      this.od.tick+=dt;
      if(this.od.tick>0.5){ this.od.tick=0; this.audio.odTick(this.mult);
        const bb=this.balls.find(v=>!v.stuck);
        if(bb) this.fx.pop(bb.x,Math.max(30,bb.y-18),`+${this.mult*5}`,'#ff8ef5',13);
      }
      for(const b of this.balls) if(!b.stuck&&b.y<top&&Math.random()<0.6)
        this.fx.trail(b.x,b.y,`hsl(${(this.time*220)%360} 100% 65%)`,4,0.4);
      this.shake=Math.min(6,this.shake+dt*8);
    } else {
      if(this.od.active){
        this.ui.toast(`OVERDRIVE ×${this.mult} BANKED`);
        this.fx.pop(this.w/2,this.h/2,`×${this.mult}`,'#ffd400',26);
      }
      this.od.active=false; this.od.t=0; this.mult=1;
      this.stage.classList.remove('od');
    }
    this.ui.hint((this.state==='play')&&!this.od.active&&alive);
  }
  render(){
    const ctx=this.ctx;
    ctx.save();
    ctx.setTransform(this.dpr,0,0,this.dpr,0,0);
    if(this.shake>0.2){
      ctx.translate((Math.random()-0.5)*this.shake,(Math.random()-0.5)*this.shake);
      this.shake*=0.88;
    } else this.shake=0;
    const pulse=this.od.active?(1+0.02*Math.sin(this.time*10)):1;
    ctx.translate(this.w/2,this.h/2); ctx.scale(pulse,pulse); ctx.translate(-this.w/2,-this.h/2);
    this.fx.drawBg(ctx,this.w,this.h,this.od.active,this.mult);
    ctx.save(); ctx.globalAlpha=0.10; ctx.fillStyle='#00f0ff';
    const bb=this.paddle.band(this.h);
    ctx.fillRect(8,bb.minY-14,this.w-16,(bb.maxY-bb.minY)+28);
    ctx.restore();
    if(this.shieldT>0){
      ctx.save(); ctx.globalAlpha=0.7+0.3*Math.sin(this.time*8);
      ctx.strokeStyle='#9adcff'; ctx.lineWidth=3; ctx.shadowBlur=12; ctx.shadowColor='#9adcff';
      ctx.beginPath();
      for(let x=0;x<=this.w;x+=14){ ctx.moveTo(x,this.h-18); ctx.lineTo(x+7,this.h-10); ctx.lineTo(x+14,this.h-18); }
      ctx.stroke(); ctx.restore();
    }
    this.drawCaps(ctx);
    drawBricks(ctx,this.bricks,this.time);
    this.paddle.draw(ctx,this.od.active);
    drawBalls(ctx,this.balls,this.od.active,this.time);
    if(this.state==='serve'){
      ctx.save();
      ctx.globalAlpha=0.6+0.4*Math.sin(this.time*5);
      ctx.font='800 15px ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto, sans-serif';
      ctx.textAlign='center'; ctx.textBaseline='middle';
      ctx.fillStyle='#a6ff00';
      ctx.shadowBlur=12; ctx.shadowColor='#a6ff00';
      ctx.fillText('TAP · CLICK · SPACE TO LAUNCH',this.w/2,this.paddle.y-64);
      ctx.restore();
    }
    this.fx.draw(ctx,this.od.active);
    ctx.restore();
  }
  drawCaps(ctx){
    ctx.save(); ctx.textAlign='center'; ctx.textBaseline='middle';
    for(const c of this.caps){
      const bob=Math.sin(c.t*6)*2;
      ctx.shadowBlur=14; ctx.shadowColor=c.color;
      ctx.fillStyle='rgba(8,8,28,.95)';
      ctx.strokeStyle=c.color; ctx.lineWidth=2;
      const w=34,h=24;
      ctx.beginPath();
      if(ctx.roundRect) ctx.roundRect(c.x-w/2,c.y-h/2+bob,w,h,10); else ctx.rect(c.x-w/2,c.y-h/2+bob,w,h);
      ctx.fill(); ctx.stroke();
      ctx.shadowBlur=0; ctx.fillStyle=c.color;
      ctx.font='900 13px ui-sans-serif,system-ui';
      ctx.fillText(c.icon,c.x,c.y+bob);
    }
    ctx.restore();
  }
  hudData(){
    const fx=[];
    if(this.pend.fire>0)fx.push({icon:'🔥',name:'FIRE',t:this.pend.fire,max:12,color:'#ff6a00'});
    if(this.pend.ghost>0)fx.push({icon:'👻',name:'GHOST',t:this.pend.ghost,max:10,color:'#c77dff'});
    if(this.pend.heavy>0)fx.push({icon:'🪨',name:'HEAVY',t:this.pend.heavy,max:12,color:'#ffd400'});
    if(this.slowT>0)fx.push({icon:'🕐',name:'SLOW',t:this.slowT,max:this.slowMax,color:'#7dffea'});
    if(this.paddle.wideT>0)fx.push({icon:'↔',name:'WIDE',t:this.paddle.wideT,max:15,color:'#6ec6ff'});
    if(this.shieldT>0)fx.push({icon:'🛡',name:'SHIELD',t:this.shieldT,max:this.shieldMax,color:'#9adcff'});
    return {score:this.score,hi:this.hi,mult:this.mult,od:this.od.active,
      odT:this.od.active?Math.min(1,this.od.t/21):0,
      lives:this.lives,level:this.level,levelName:LEVELS[this.level].name,effects:fx};
  }
  resize(){
    const r=this.stage.getBoundingClientRect();
    const w=Math.max(300,r.width), h=Math.max(420,r.height||window.innerHeight*0.62);
    this.dpr=Math.min(2,window.devicePixelRatio||1);
    this.canvas.width=Math.round(w*this.dpr); this.canvas.height=Math.round(h*this.dpr);
    this.canvas._w=w; this.canvas._h=h;
    this.w=w; this.h=h;
    const _hadBricks=(this.bricks||[]).length>0;
    const _alive=new Set((this.bricks||[]).filter(b=>b.alive).map(b=>b.row+":"+b.col));
    this.bricks=buildBricks(LEVELS[this.level].map,this.w);
    if(_hadBricks){
      for(const br of this.bricks){
        if(!_alive.has(br.row+":"+br.col)){ br.alive=false; br.flash=0; br.denied=0; }
      }
    }
    const bd=this.paddle.band(this.h);
    this.paddle.ty=Math.max(bd.minY,Math.min(bd.maxY,this.paddle.ty||this.h-70));
    this.paddle.x=Math.max(20,Math.min(this.w-20,this.paddle.x||this.w/2));
    for(const b of (this.balls||[])){
      b.x=Math.max(b.r,Math.min(this.w-b.r,b.x));
      b.y=Math.max(b.r,Math.min(this.h-10,b.y));
    }
    this.fx.initStars(this.w,this.h);
  }
  frame(now){
    requestAnimationFrame(t=>this.frame(t));
    if(!this.last)this.last=now;
    let dt=(now-this.last)/1000; this.last=now;
    if(dt>0.1)dt=0.1;
    if(this.state==='play'||this.state==='serve'){
      this.acc+=dt;
      let n=0;
      while(this.acc>=STEP&&n<10){ this.step(STEP); this.acc-=STEP; n++; }
      if(n===10)this.acc=0;
    } else if(this.state==='menu'||this.state==='clear'||this.state==='over'||this.state==='win'){
      this.time+=dt; this.fx.update(dt);
      for(const br of this.bricks) if(br.type==='H'&&br.alive) br.x=br.baseX+Math.sin(this.time*1.6+br.phase)*Math.min(46,this.w*0.09);
      this.paddle.update(dt,this.w,this.h);
    }
    this.render();
    this.ui.hud(this.hudData());
    if(this.state==='menu') this.ui.hint(false);
  }
}
