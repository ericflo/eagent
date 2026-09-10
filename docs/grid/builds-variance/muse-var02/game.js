/* OVERDRIVE — Neon Breakout. Vanilla Canvas2D + WebAudio. Zero deps. */
'use strict';
/* ================= UTILS ================= */
const TAU=Math.PI*2;
const clamp=(v,a,b)=>v<a?a:v>b?b:v;
const lerp=(a,b,t)=>a+(b-a)*t;
const rand=(a,b)=>a+Math.random()*(b-a);
const randi=(a,b)=>Math.floor(rand(a,b+1));
const choice=a=>a[Math.floor(Math.random()*a.length)];
const $=id=>document.getElementById(id);
const reducedMotion=()=>window.matchMedia&&window.matchMedia('(prefers-reduced-motion: reduce)').matches;
function fmt(n){return n>=1e6?(n/1e6).toFixed(1)+'M':n>=1e4?(n/1e3).toFixed(1)+'K':''+Math.floor(n);}
/* ================= CONFIG ================= */
const CFG={
  ballR:8, baseBallSpeed:430, maxBallSpeed:1050, minBallSpeed:300,
  paddleW:110, paddleH:16, paddleBand:0.22, upBoost:170, downDampen:0.9,
  lives:3, speedBrickThresh:560, prismSteep:0.5, blinkPeriod:2.4, blinkSolid:0.55,
  odGrowth:1.9, odMax:999, magnetR:90,
};
const BALL_DEFS={
  normal:{name:'Normal',color:'#ffffff',glow:'#29f6ff',score:1,speed:1},
  fire:{name:'Fireball',color:'#ffb14d',glow:'#ff5a00',score:1,speed:1.02,aoe:52},
  volt:{name:'Volt',color:'#fff38a',glow:'#ffe93d',score:1,speed:1.05,chain:2},
  ghost:{name:'Ghost',color:'#d8f4ff',glow:'#9fd8ff',score:1,speed:1.08,phase:3},
  gold:{name:'Golden',color:'#ffe27a',glow:'#ffd23d',score:2,speed:1.12},
};
const BRICK_SCORE={normal:50,prism:120,velo:140,blink:150,drift:110,bomb:80};
const PU_DEFS={
  multi:{label:'MULTI',icon:'✚',color:'#39e6a3',w:0.16},
  enlarge:{label:'WIDE',icon:'⟷',color:'#29f6ff',w:0.15},
  fire:{label:'FIRE',icon:'🔥',color:'#ff6a00',w:0.12},
  volt:{label:'VOLT',icon:'⚡',color:'#ffe93d',w:0.12},
  ghost:{label:'GHOST',icon:'👻',color:'#bfe9ff',w:0.10},
  slow:{label:'SLOW',icon:'🕐',color:'#7db4ff',w:0.10},
  shield:{label:'AEGIS',icon:'⛨',color:'#8affda',w:0.08},
  life:{label:'1UP',icon:'♥',color:'#ff4d7e',w:0.03},
  laser:{label:'LASER',icon:'⌖',color:'#ff3df0',w:0.09},
  gold:{label:'GOLD',icon:'★',color:'#ffd23d',w:0.05},
};
/* ================= LEVELS (funnel ball above) =================
   Legend: . empty  N normal  P prism  V velocity  B blink  D drifter  X volatile
   Each level: {name, hint, map[]} — wide maps scale to fit. */
const LEVELS=[
 {name:'FIRST CONTACT',hint:'Break the middle to send the ball ABOVE the bricks. Overdrive lives up top!',
  rows:['........','..NNNN..','..NNNN..','...NN...','........']},
 {name:'THE ARCH',hint:'Prism bricks (purple ⌃) only break on STEEP hits. Come down on them from above!',
  rows:['...PP...','..NNNN..','.NNNNNN.','.NN..NN.','...NN...']},
 {name:'NEED FOR SPEED',hint:'Orange ⌁ bricks only break when the ball is FAST. Flick UP into the ball!',
  rows:['...VV...','..NNNN..','.NXNNXN.','.NNNNNN.','...NN...']},
 {name:'BLINK GATE',hint:'Gray blink bricks are solid only when LIT. Time your shots!',
  rows:['..BBBb..'.replace(/b/g,'B'),'..NNNN..','.NNDDNN.','.NXNNXN.','...NN...']},
 {name:'DRIFT STORM',hint:'Green sliders drift sideways. Lead your shots — Volatile reds help!',
  rows:['.DD..DD.','..PPPP..','.NVVVVN.','.NXNNXN.','..BBBB..']},
 {name:'OVERDRIVE HEAVEN',hint:'Everything at once. Get up top and stay there. Good luck, pilot!',
  rows:['.PVDDBVP.'.replace(/V/g,'V'),'..XPPPX..','.NDBBBDN.','.NVVVVVN.','...XXX..']},
 {name:'FINAL FURNACE',hint:'The furnace. Volatile chains are your friends. Bank huge overdrive!',
  rows:['DDBPPBDDB'.slice(0,8),'VPXNNXPV'.slice(0,8),'NBBDDbbN'.replace(/b/g,'B').slice(0,8),'.NXVVXN.'.slice(0,8),'...XX...']},
];
/* normalize level maps: pad rows to equal width */
LEVELS.forEach(L=>{const w=Math.max.apply(null,L.rows.map(r=>r.length));L.rows=L.rows.map(r=>r.padEnd(w,'.'));L.w=w;L.h=L.rows.length;});
/* ================= GAME STATE ================= */
const canvas=$('game'),ctx=canvas.getContext('2d');
let W=0,H=0,DPR=1;
let state='menu'; // menu|playing|paused|clear|over|victory
let helpOpen=false;
let score=0,high=0,lives=3,levelIdx=0,mult=1,combo=0,comboTimer=0;
let overdrive=false,odMult=1,odTime=0,maxOd=1,timeAbove=0,bankFlash=0;
let shake=0,hitFlash=0,slowmo=0,slowmoT=0,timeScale=1,chroma=0;
let balls=[],bricks=[],powerups=[],particles=[],floaters=[],shockwaves=[],lasers=[],stars=[];
let brickTop=Infinity,brickBottom=0,playW=0,playH=0,topZoneY=0;
let stats={maxOverdrive:1,timeAbove:0,bricks:0};
let attractT=0,clearT=0,fireworks=[],hintShown={};
let motionOK=true,stickEnabled=true;
try{high=parseInt(localStorage.getItem('overdrive_high')||'0',10)||0;}catch(e){high=0;}
function saveHigh(){try{if(score>high){high=score;localStorage.setItem('overdrive_high',''+high);return true;}}catch(e){}return false;}
/* ================= AUDIO (WebAudio synth, no assets) ================= */
const AudioSys={
  ctx:null,master:null,musicG:null,sfxG:null,muted:false,vol:0.8,started:false,
  bassT:0,arpT:0,step:0,nextNote:0,
  init(){if(this.ctx)return;try{
    this.ctx=new (window.AudioContext||window.webkitAudioContext)();
    this.master=this.ctx.createGain();this.master.gain.value=this.vol;this.master.connect(this.ctx.destination);
    this.sfxG=this.ctx.createGain();this.sfxG.gain.value=0.9;this.sfxG.connect(this.master);
    this.musicG=this.ctx.createGain();this.musicG.gain.value=0.34;this.musicG.connect(this.master);
    this.started=true;
  }catch(e){}},
  unlock(){this.init();if(this.ctx&&this.ctx.state==='suspended')this.ctx.resume();},
  setMute(m){this.muted=m;if(this.master)this.master.gain.value=m?0:this.vol;},
  tone(f,dur,type,vol,slide,when){if(!this.ctx||this.muted)return;type=type||'square';vol=vol||0.2;when=when||0;
    const t=this.ctx.currentTime+when,o=this.ctx.createOscillator(),g=this.ctx.createGain();
    o.type=type;o.frequency.setValueAtTime(f,t);
    if(slide)o.frequency.exponentialRampToValueAtTime(Math.max(20,f+slide),t+dur);
    g.gain.setValueAtTime(vol,t);g.gain.exponentialRampToValueAtTime(0.0001,t+dur);
    o.connect(g);g.connect(this.sfxG);o.start(t);o.stop(t+dur+0.02);},
  noise(dur,vol,ff,when){if(!this.ctx||this.muted)return;when=when||0;
    const t=this.ctx.currentTime+when,len=Math.max(1,(dur*this.ctx.sampleRate)|0);
    const buf=this.ctx.createBuffer(1,len,this.ctx.sampleRate),d=buf.getChannelData(0);
    for(let i=0;i<len;i++)d[i]=(Math.random()*2-1)*(1-i/len);
    const s=this.ctx.createBufferSource();s.buffer=buf;
    const f=this.ctx.createBiquadFilter();f.type='lowpass';f.frequency.value=ff||2000;
    const g=this.ctx.createGain();g.gain.setValueAtTime(vol||0.25,t);g.gain.exponentialRampToValueAtTime(0.0001,t+dur);
    s.connect(f);f.connect(g);g.connect(this.sfxG);s.start(t);},
  paddle(vy){this.tone(220+Math.abs(vy||0)*0.12,0.09,'square',0.22,180);this.noise(0.05,0.08,4000);},
  wall(){this.tone(160,0.06,'triangle',0.15,-40);},
  brick(big){this.tone(big?520:660,0.1,'square',0.2,big?260:340);this.noise(0.08,0.12,5000);},
  resist(){this.tone(140,0.16,'sawtooth',0.2,-40);this.tone(110,0.2,'square',0.12,0,0.03);},
  launch(){this.tone(300,0.18,'sawtooth',0.22,500);},
  power(){[523,659,784,1046].forEach((f,i)=>this.tone(f,0.14,'square',0.18,0,i*0.06));},
  lose(){[400,300,220,150].forEach((f,i)=>this.tone(f,0.22,'sawtooth',0.22,-60,i*0.11));this.noise(0.4,0.2,800);},
  odOn(){this.tone(200,0.5,'sawtooth',0.25,800);this.noise(0.4,0.15,6000);},
  bank(n){const b=Math.min(n,8);for(let i=0;i<b;i++)this.tone(600+i*160,0.12,'square',0.16,80,i*0.05);},
  ui(){this.tone(700,0.06,'square',0.14,120);},
  laser(){this.tone(1200,0.12,'sawtooth',0.18,-700);},
  explode(){this.noise(0.35,0.3,1200);this.tone(90,0.3,'sine',0.3,-40);},
  music(dt){ // adaptive bass + arp; brightness/tempo rise with mult & overdrive
    if(!this.ctx||this.muted||state!=='playing')return;
    const c=this.ctx,t=c.currentTime;
    const heat=clamp(Math.log2(Math.max(1,mult))/4,0,1)+(overdrive?0.5:0);
    this.bassT-=dt;
    if(this.bassT<=0){this.bassT=overdrive?0.21:0.32;
      const scale=[55,55,65.4,49];const f=scale[this.step%4]*(overdrive?2:1);
      const o=c.createOscillator(),g=c.createGain(),fl=c.createBiquadFilter();
      o.type='sawtooth';o.frequency.value=f;fl.type='lowpass';fl.frequency.value=300+heat*2200;
      g.gain.setValueAtTime(0.5,t);g.gain.exponentialRampToValueAtTime(0.001,t+0.28);
      o.connect(fl);fl.connect(g);g.connect(this.musicG);o.start(t);o.stop(t+0.3);}
    this.arpT-=dt;
    if(this.arpT<=0){this.arpT=overdrive?0.09:0.16;
      const penta=[220,261.6,293.7,329.6,392,440,523.2,587.3];
      const f=penta[(this.step*3+((this.step>>2)%3))%penta.length]*(overdrive?2:1);
      const o=c.createOscillator(),g=c.createGain();
      o.type=overdrive?'sawtooth':'square';o.frequency.value=f;
      g.gain.setValueAtTime(0.16,t);g.gain.exponentialRampToValueAtTime(0.001,t+0.14);
      o.connect(g);g.connect(this.musicG);o.start(t);o.stop(t+0.16);
      this.step++;}}
};
/* ================= RESIZE / CANVAS ================= */
function resize(){
  DPR=Math.min(window.devicePixelRatio||1,2);
  W=window.innerWidth;H=window.innerHeight;
  canvas.width=Math.round(W*DPR);canvas.height=Math.round(H*DPR);
  ctx.setTransform(DPR,0,0,DPR,0,0);
  playW=W;playH=H;
  stars=[];
  const n=Math.round(W*H/9000);
  for(let i=0;i<n;i++)stars.push({x:Math.random()*W,y:Math.random()*H,z:rand(0.2,1),tw:rand(0,TAU)});
  if(paddle)paddle.clamp();
}
window.addEventListener('resize',resize);
window.addEventListener('orientationchange',()=>setTimeout(resize,120));
/* ================= PADDLE ================= */
const paddle={x:0,y:0,w:CFG.paddleW,h:CFG.paddleH,vx:0,vy:0,px:0,py:0,tx:null,ty:null,
  wide:0,laser:0,squash:0,glowV:0,shield:false,
  reset(){this.w=CFG.paddleW;this.x=W/2;this.y=H-90;
    this.vx=0;this.vy=0;this.tx=null;this.wide=0;this.laser=0;this.squash=0;this.shield=paddleShieldPersist;},
  effW(){return this.w*(this.wide>0?1.5:1);},
  top(){return H*(1-CFG.paddleBand);},
  clamp(){this.x=clamp(this.x,this.effW()/2+4,W-this.effW()/2-4);this.y=clamp(this.y,this.top(),H-46);},
  update(dt){
    this.px=this.x;this.py=this.y;
    const spd=620;
    if(keys.left)this.x-=spd*dt;if(keys.right)this.x+=spd*dt;
    if(keys.up)this.y-=spd*0.8*dt;if(keys.down)this.y+=spd*0.8*dt;
    if(this.tx!==null){ // pointer / stick target
      const k=1-Math.pow(0.0001,dt);
      this.x+=(this.tx-this.x)*Math.min(1,k*1.4);
      this.y+=(this.ty-this.y)*Math.min(1,k*1.4);
      if(Math.abs(this.tx-this.x)<0.6)this.tx=null;
    }
    if(stickVec.active){this.x+=stickVec.x*760*dt;this.y+=stickVec.y*620*dt;}
    this.clamp();
    this.vx=(this.x-this.px)/Math.max(dt,1e-4);this.vy=(this.y-this.py)/Math.max(dt,1e-4);
    this.glowV=lerp(this.glowV,clamp(-this.vy/700,-1,1),0.2);
    if(this.wide>0)this.wide-=dt;if(this.laser>0)this.laser-=dt;
    this.squash=Math.max(0,this.squash-dt*4);
  }};
let paddleShieldPersist=false;
/* ================= INPUT: mouse + keys + touch drag + thumbstick ================= */
const keys={left:false,right:false,up:false,down:false};
const stickVec={x:0,y:0,active:false,id:null,ox:0,oy:0};
let pointerPos={x:0,y:0},touchMode=false,stickBase=null;
const stickEl=$('stick'),stickNub=$('stick-nub');
function canvasPos(t){return{x:t.clientX,y:t.clientY};}
function pressLaunch(){AudioSys.unlock();if(state==='playing')launchBalls();else if(state==='menu')startGame();}
canvas.addEventListener('mousemove',e=>{if(state!=='playing')return;
  pointerPos={x:e.clientX,y:e.clientY};
  if(!stickVec.active){paddle.tx=clamp(e.clientX,0,W);paddle.ty=clamp(e.clientY,paddle.top(),H-46);}});
canvas.addEventListener('mousedown',e=>{AudioSys.unlock();if(state!=='playing')return;
  if(paddle.laser>0)fireLaser();else launchBalls();});
window.addEventListener('keydown',e=>{
  if(['ArrowLeft','ArrowRight','ArrowUp','ArrowDown',' '].includes(e.key))e.preventDefault();
  AudioSys.unlock();
  const k=e.key.toLowerCase();
  if(k==='arrowleft'||k==='a')keys.left=true;
  if(k==='arrowright'||k==='d')keys.right=true;
  if(k==='arrowup'||k==='w')keys.up=true;
  if(k==='arrowdown'||k==='s')keys.down=true;
  if(k===' '){if(state==='playing'){if(paddle.laser>0)fireLaser();else launchBalls();}else if(state==='menu')startGame();}
  if(k==='p'||k==='escape')togglePause();
  if(k==='m')toggleMute();
});
window.addEventListener('keyup',e=>{const k=e.key.toLowerCase();
  if(k==='arrowleft'||k==='a')keys.left=false;
  if(k==='arrowright'||k==='d')keys.right=false;
  if(k==='arrowup'||k==='w')keys.up=false;
  if(k==='arrowdown'||k==='s')keys.down=false;});
/* touch: drag moves paddle; hold-still becomes floating thumbstick; 2nd finger taps launch */
let dragId=null,dragMoved=0;
canvas.addEventListener('touchstart',e=>{e.preventDefault();AudioSys.unlock();touchMode=true;showMobileBtns();
  for(const t of e.changedTouches){
    const p=canvasPos(t);
    if(stickEnabled&&e.touches.length===1){ // potential stick: show base, decide after move
      stickBase={id:t.identifier,ox:p.x,oy:p.y,moved:false};
      stickEl.classList.remove('hidden');
      stickEl.style.left=p.x+'px';stickEl.style.top=p.y+'px';
      stickNub.style.transform='translate(-50%,-50%)';
    }else if(e.touches.length>=2){pressLaunch();}
    else{dragId=t.identifier;dragMoved=0;paddle.tx=p.x;paddle.ty=clamp(p.y,paddle.top(),H-46);}
  }},{passive:false});
canvas.addEventListener('touchmove',e=>{e.preventDefault();
  for(const t of e.changedTouches){const p=canvasPos(t);
    if(stickBase&&t.identifier===stickBase.id){
      const dx=p.x-stickBase.ox,dy=p.y-stickBase.oy,d=Math.hypot(dx,dy);
      if(d>14)stickBase.moved=true;
      if(stickEnabled&&stickBase.moved){
        const m=Math.min(d,52),nx=d>0?dx/d:0,ny=d>0?dy/d:0;
        stickVec.x=nx*(m/52);stickVec.y=ny*(m/52);stickVec.active=true;
        stickNub.style.transform=`translate(calc(-50% + ${nx*m}px),calc(-50% + ${ny*m}px))`;
      }else{paddle.tx=p.x;paddle.ty=clamp(p.y,paddle.top(),H-46);}
    }else{paddle.tx=p.x;paddle.ty=clamp(p.y,paddle.top(),H-46);}
  }},{passive:false});
function touchEnd(e){e.preventDefault();
  for(const t of e.changedTouches){
    if(stickBase&&t.identifier===stickBase.id){
      if(!stickBase.moved){// tap = launch/fire
        if(state==='playing'){if(paddle.laser>0)fireLaser();else launchBalls();}
      }
      stickBase=null;stickVec.active=false;stickVec.x=0;stickVec.y=0;stickEl.classList.add('hidden');
    }
    if(t.identifier===dragId)dragId=null;
  }}
canvas.addEventListener('touchend',touchEnd,{passive:false});
canvas.addEventListener('touchcancel',touchEnd,{passive:false});
document.addEventListener('gesturestart',e=>e.preventDefault());
document.addEventListener('dblclick',e=>e.preventDefault());
function showMobileBtns(){if(touchMode)$('mobile-btns').classList.remove('hidden');}
/* ================= BRICKS =================
   types: normal, prism(angle-gated), velo(speed-gated), blink(timed), drift(mover), bomb(volatile AoE) */
function buildLevel(idx){
  bricks=[];balls=[];powerups=[];lasers=[];particles=[];floaters=[];shockwaves=[];
  overdrive=false;odMult=1;odTime=0;bankFlash=0;mult=Math.max(1,Math.round(mult));combo=0;
  const L=LEVELS[idx%LEVELS.length];
  const topPad=Math.max(118,H*0.16),sidePad=16;
  const availW=W-sidePad*2,cellW=availW/L.w,cellH=clamp((H*0.42)/L.h,22,40);
  const bw=cellW-6,bh=cellH-6;
  const y0=topPad;
  for(let r=0;r<L.h;r++)for(let c=0;c<L.w;c++){
    const ch=L.rows[r][c];if(ch==='.'||ch===' ')continue;
    const cx=sidePad+cellW*c+cellW/2,cy=y0+cellH*r+cellH/2;
    const t=ch==='N'?'normal':ch==='P'?'prism':ch==='V'?'velo':ch==='B'?'blink':ch==='D'?'drift':'bomb';
    bricks.push({x:cx,y:cy,hx:cx,w:bw,h:bh,type:t,phase:rand(0,TAU),flash:0,resist:0,dead:false,
      hue:hueFor(t),born:perfT+ (r*0.05+c*0.02)});
  }
  brickTop=y0-cellH/2;brickBottom=y0+cellH*L.h;topZoneY=brickTop-14;
  paddle.reset();spawnStuckBall();
  if(!hintShown[L.name]){$('hint-bar');showHint('LVL '+(idx+1)+' — '+L.name+': '+L.hint);hintShown[L.name]=1;}
  $('hud-level').textContent='LEVEL '+(idx+1);
}
function hueFor(t){return{normal:212,prism:278,velo:28,blink:230,drift:150,bomb:0}[t]||212;}
function brickSolidAt(b,t){
  if(b.type!=='blink')return true;
  const ph=((t+b.phase)%CFG.blinkPeriod)/CFG.blinkPeriod;
  return ph<CFG.blinkSolid;
}
function brickRect(b,t){
  let x=b.x;
  if(b.type==='drift')x=b.hx+Math.sin(t*1.6+b.phase)*(Math.max(20,b.w*0.9));
  return{x:x,y:b.y,w:b.w,h:b.h};
}
/* ================= BALLS ================= */
function spawnStuckBall(type){
  balls.push({x:paddle.x,y:paddle.y-paddle.h/2-CFG.ballR-2,vx:0,vy:0,r:CFG.ballR,
    type:type||'normal',stuck:true,trail:[],ghostHits:0,ghostT:0,spin:rand(0,TAU),flatT:0,horizT:0});
}
function launchBalls(){
  let any=false;
  for(const b of balls)if(b.stuck){
    const a=rand(-0.5,0.5)-Math.PI/2;
    const sp=CFG.baseBallSpeed*BALL_DEFS[b.type].speed;
    b.vx=Math.cos(a)*sp;b.vy=Math.sin(a)*sp;b.stuck=false;any=true;}
  if(any){AudioSys.launch();spawnShock(paddle.x,paddle.y,40,'#29f6ff');}
}
function setBallType(b,t){b.type=t;b.ghostHits=0;b.ghostT=t==='ghost'?4:0;
  addText(b.x,b.y-14,BALL_DEFS[t].name.toUpperCase()+'!',BALL_DEFS[t].glow,16);}
function ballSpeed(b){return Math.hypot(b.vx,b.vy);}
function enforceSpeed(b){
  let sp=ballSpeed(b);if(sp<1)return;
  const slowT=slowmo>0?0.55:1;
  const min=CFG.minBallSpeed*slowT,max=CFG.maxBallSpeed*(overdrive?1.25:1)*(slowmo>0?0.7:1);
  if(sp<min){b.vx*=min/sp;b.vy*=min/sp;}
  else if(sp>max){b.vx*=max/sp;b.vy*=max/sp;}
  // anti-stuck: prevent near-horizontal infinite loops
  if(Math.abs(b.vy)<Math.max(40,sp*0.12)&&!b.stuck){
    b.horizT=(b.horizT||0)+1/60;
    if(b.horizT>1.6){b.vy+=(b.vy>=0?1:-1)*rand(90,160);b.vx*=0.97;b.horizT=0;
      addText(b.x,b.y,'NUDGE','#ffd23d',12);}
  }else b.horizT=0;
}
/* ================= PARTICLES / FLOATERS / SHOCKWAVES ================= */
function spawnParticles(x,y,color,n,spd,life,size){
  if(!motionOK)n=Math.min(n,6);
  for(let i=0;i<n;i++){if(particles.length>900)break;
    const a=rand(0,TAU),s=rand(spd*0.3,spd);
    particles.push({x,y,vx:Math.cos(a)*s,vy:Math.sin(a)*s-60,life:rand(life*0.5,life),maxLife:life,
      color,size:rand(size*0.5,size),grav:520,shard:Math.random()<0.45,rot:rand(0,TAU),vr:rand(-8,8)});}
}
function spawnSparks(x,y,color,n){for(let i=0;i<n;i++){if(particles.length>900)break;
  const a=rand(0,TAU),s=rand(120,520);
  particles.push({x,y,vx:Math.cos(a)*s,vy:Math.sin(a)*s,life:rand(0.15,0.4),maxLife:0.4,color,size:rand(1,2.6),grav:0,shard:false,rot:0,vr:0});}}
function spawnTrailDot(b){if(b.stuck)return;
  b.trail.push({x:b.x,y:b.y,life:0.35});if(b.trail.length>22)b.trail.shift();}
function spawnShock(x,y,r,color){if(!motionOK&&shockwaves.length>2)return;
  shockwaves.push({x,y,r:6,maxR:r,color,life:0.35,t:0});}
function spawnConfetti(x,y){const cols=['#ff3df0','#29f6ff','#ffd23d','#39e6a3','#ffffff'];
  for(let i=0;i<70;i++){if(particles.length>1000)break;
    particles.push({x:x+rand(-30,30),y,vx:rand(-320,320),vy:rand(-520,-60),life:rand(0.8,1.8),maxLife:1.8,
      color:choice(cols),size:rand(2,5),grav:600,shard:true,rot:rand(0,TAU),vr:rand(-10,10)});}}
function addText(x,y,str,color,size){floaters.push({x:clamp(x,50,W-50),y,str,color:color||'#fff',size:size||15,life:1.1,t:0});
  if(floaters.length>40)floaters.shift();}
function toast(msg,ms){const t=$('toast');t.textContent=msg;t.classList.remove('hidden');
  clearTimeout(t._h);t._h=setTimeout(()=>t.classList.add('hidden'),ms||1800);}
function showHint(msg){const h=$('hint-bar');h.textContent=msg;h.classList.remove('hidden');
  clearTimeout(h._h);h._h=setTimeout(()=>h.classList.add('hidden'),5200);}
/* ================= POWER-UPS ================= */
function maybeDrop(x,y){
  const r=Math.random();if(r>0.34)return;
  let pool=Object.entries(PU_DEFS),tot=pool.reduce((s,e)=>s+e[1].w,0),pick=Math.random()*tot,type='multi';
  for(const[k,d]of pool){pick-=d.w;if(pick<=0){type=k;break;}}
  if(type==='shield'&&paddleShieldPersist)return;
  powerups.push({x,y,vx:rand(-30,30),vy:120,type,t:0,rot:rand(0,TAU)});
}
function catchPower(p){
  const d=PU_DEFS[p.type];AudioSys.power();
  spawnShock(p.x,p.y,54,d.color);spawnSparks(p.x,p.y,d.color,16);
  addText(p.x,p.y-10,d.label+' '+d.icon,d.color,16);
  switch(p.type){
    case 'multi':{const src=balls.filter(b=>!b.stuck);const from=src.length?src:balls;
      from.slice(0,4).forEach(b=>{if(balls.length>=8)return;
        const a=rand(0,TAU),sp=ballSpeed(b)||CFG.baseBallSpeed;
        balls.push({x:b.x,y:b.y,vx:Math.cos(a)*sp,vy:-Math.abs(Math.sin(a)*sp)-80,r:b.r,type:b.type,
          stuck:false,trail:[],ghostHits:0,ghostT:b.type==='ghost'?4:0,spin:0,flatT:0,horizT:0});});
      if(!balls.length)spawnStuckBall();break;}
    case 'enlarge':paddle.wide=14;toast('WIDE PADDLE!');break;
    case 'fire':{const b=firstFreeBall();if(b)setBallType(b,'fire');break;}
    case 'volt':{const b=firstFreeBall();if(b)setBallType(b,'volt');break;}
    case 'ghost':{const b=firstFreeBall();if(b)setBallType(b,'ghost');break;}
    case 'gold':{const b=firstFreeBall();if(b)setBallType(b,'gold');break;}
    case 'slow':slowmo=7;toast('SLOW-MO!');break;
    case 'shield':paddle.shield=true;paddleShieldPersist=true;toast('AEGIS NET ARMED ⛨');break;
    case 'life':lives++;toast('EXTRA LIFE ♥');updateHUD();break;
    case 'laser':paddle.laser=20;toast('LASER — TAP / CLICK TO FIRE ⌖');break;
  }
}
function firstFreeBall(){return balls.find(b=>!b.stuck)||balls[0];}
function fireLaser(){
  if(paddle.laser<=0||lasers.length>=6)return;
  AudioSys.laser();paddle.laser-=0; // ammo = time-based
  lasers.push({x:paddle.x-14,y:paddle.y-10,vy:-900},{x:paddle.x+14,y:paddle.y-10,vy:-900});
  paddle.squash=0.35;shake=Math.max(shake,3);
}
/* ================= SCORING / MULTIPLIER / OVERDRIVE ================= */
function award(base,x,y,label){
  const od=overdrive?odMult:1;
  const pts=Math.round(base*mult*od*(combo>=5?1+combo*0.05:1));
  score+=pts;stats.bricks++;
  combo++;comboTimer=3;
  mult=Math.min(64,1+Math.floor(combo/4));
  if(odMult>maxOd)maxOd=odMult;
  addText(x,y,'+'+fmt(pts)+(od>1?'  OD×'+Math.floor(od):''),'#ffd23d',overdrive?18:14);
  chroma=Math.min(1,chroma+0.06);
  return pts;
}
function updateOverdrive(dt){
  const anyAbove=balls.some(b=>!b.stuck&&b.y<topZoneY);
  if(state!=='playing')return;
  if(anyAbove){
    if(!overdrive){overdrive=true;odMult=2;odTime=0;AudioSys.odOn();
      toast('⚡ OVERDRIVE ⚡');spawnShock(W/2,topZoneY,W*0.4,'#ff3df0');}
    odTime+=dt;timeAbove+=dt;stats.timeAbove+=dt;
    odMult=Math.min(CFG.odMax,odMult*Math.pow(2,dt*CFG.odGrowth*0.5));
    if(odMult>maxOd)maxOd=odMult;
    $('od-x').textContent='x'+Math.floor(odMult);
    $('overdrive-banner').classList.remove('hidden');
    if(Math.random()<dt*8)AudioSys.tone(700+Math.min(1200,odMult*4),0.08,'square',0.05,120);
  }else if(overdrive){
    overdrive=false;
    const bonus=Math.round(odMult*25*mult);
    score+=bonus;bankFlash=1;
    AudioSys.bank(5);spawnConfetti(W/2,H*0.3);
    addText(W/2,H*0.32,'BANKED +'+fmt(bonus),'#39e6a3',26);
    toast('OVERDRIVE BANKED +'+fmt(bonus));
    odMult=1;$('overdrive-banner').classList.add('hidden');
  }
}
/* ================= COLLISION HELPERS ================= */
function circleRectCollide(bx,by,r,rc){
  const nx=clamp(bx,rc.x-rc.w/2,rc.x+rc.w/2),ny=clamp(by,rc.y-rc.h/2,rc.y+rc.h/2);
  let dx=bx-nx,dy=by-ny;const d2=dx*dx+dy*dy;
  if(d2>r*r)return null;
  if(d2>1e-6){const d=Math.sqrt(d2);return{nx:dx/d,ny:dy/d,pen:r-d};}
  // center inside: push out along smallest axis
  const ox=rc.w/2-Math.abs(bx-rc.x),oy=rc.h/2-Math.abs(by-rc.y);
  if(ox<oy)return{nx:bx>=rc.x?1:-1,ny:0,pen:ox+r};
  return{nx:0,ny:by>=rc.y?1:-1,pen:oy+r};
}
function reflectVel(b,nx,ny){
  const d=b.vx*nx+b.vy*ny;
  if(d<0){b.vx-=2*d*nx;b.vy-=2*d*ny;}
}
function breakBrick(b,ball,silent){
  if(b.dead)return;b.dead=true;
  const t=perfT,rc=brickRect(b,t);
  const pts=award(BRICK_SCORE[b.type]||50,rc.x,rc.y);
  const cols={normal:'#3fa9ff',prism:'#b06bff',velo:'#ff9f2e',blink:'#9fb4ff',drift:'#39e6a3',bomb:'#ff4d5e'};
  spawnParticles(rc.x,rc.y,cols[b.type]||'#fff',b.type==='bomb'?34:18,340,0.8,4);
  spawnSparks(rc.x,rc.y,'#ffffff',8);
  spawnShock(rc.x,rc.y,44,cols[b.type]||'#fff');
  AudioSys.brick(b.type==='bomb');
  shake=Math.max(shake,b.type==='bomb'?9:4);hitFlash=Math.max(hitFlash,0.25);
  if(!silent)maybeDrop(rc.x,rc.y);
  // Fireball AoE
  if(ball&&ball.type==='fire'){
    spawnParticles(rc.x,rc.y,'#ff6a00',22,420,0.6,5);AudioSys.explode();
    shake=Math.max(shake,7);
    for(const o of bricks){if(o.dead)continue;const or2=brickRect(o,t);
      if(Math.hypot(or2.x-rc.x,or2.y-rc.y)<BALL_DEFS.fire.aoe+o.w/2&&o!==b){
        if(o.type==='bomb'){detonate(o);}else{o.dead=true;award(BRICK_SCORE[o.type]||50,or2.x,or2.y);
          spawnParticles(or2.x,or2.y,'#ffb14d',10,300,0.6,4);maybeDrop(or2.x,or2.y);}}}
  }
  // Volt chain
  if(ball&&ball.type==='volt'){
    let n=0;
    for(const o of bricks){if(o.dead||o===b)continue;const or2=brickRect(o,t);
      if(Math.hypot(or2.x-rc.x,or2.y-rc.y)<190){o.dead=true;award(BRICK_SCORE[o.type]||50,or2.x,or2.y);
        spawnSparks(or2.x,or2.y,'#ffe93d',10);spawnShock(or2.x,or2.y,36,'#ffe93d');
        lightningFx(rc.x,rc.y,or2.x,or2.y);maybeDrop(or2.x,or2.y);
        if(++n>=BALL_DEFS.volt.chain)break;}}
    AudioSys.tone(1500,0.1,'sawtooth',0.15,-900);
  }
  if(b.type==='bomb')detonate(b,true);
}
function detonate(b,skipSelf){
  const t=perfT,rc=brickRect(b,t);
  AudioSys.explode();spawnParticles(rc.x,rc.y,'#ff4d5e',30,460,0.7,5);
  spawnShock(rc.x,rc.y,110,'#ff4d5e');shake=Math.max(shake,10);hitFlash=0.5;
  for(const o of bricks){if(o.dead||o===b)continue;const or2=brickRect(o,t);
    if(Math.hypot(or2.x-rc.x,or2.y-rc.y)<120){
      if(o.type==='bomb'&&!o.dead){o.dead=true;award(80,or2.x,or2.y);detonate(o,true);}
      else{o.dead=true;award(BRICK_SCORE[o.type]||50,or2.x,or2.y);
        spawnParticles(or2.x,or2.y,'#ff8a5e',10,300,0.6,4);maybeDrop(or2.x,or2.y);}}}
}
function lightningFx(x1,y1,x2,y2){for(let i=0;i<8;i++){const t=Math.random();
  particles.push({x:lerp(x1,x2,t)+rand(-8,8),y:lerp(y1,y2,t)+rand(-8,8),vx:0,vy:0,
    life:0.2,maxLife:0.2,color:'#ffe93d',size:2,grav:0,shard:false,rot:0,vr:0});}}
/* ================= BALL PHYSICS (substepped, swept-ish) ================= */
function updateBalls(dt){
  const shieldY=H-24;
  for(let bi=balls.length-1;bi>=0;bi--){
    const b=balls[bi];
    if(b.stuck){b.x=paddle.x;b.y=paddle.y-paddle.h/2-b.r-2;continue;}
    if(b.ghostT>0)b.ghostT-=dt;
    const sp=ballSpeed(b)||CFG.baseBallSpeed;
    const steps=Math.min(6,Math.max(1,Math.ceil(sp*dt/(b.r*0.9))));
    const sdt=dt/steps;
    for(let s=0;s<steps;s++){
      b.x+=b.vx*sdt;b.y+=b.vy*sdt;
      // walls
      if(b.x<b.r){b.x=b.r;b.vx=Math.abs(b.vx);AudioSys.wall();spawnSparks(b.x,b.y,'#29f6ff',4);}
      if(b.x>W-b.r){b.x=W-b.r;b.vx=-Math.abs(b.vx);AudioSys.wall();spawnSparks(b.x,b.y,'#29f6ff',4);}
      if(b.y<b.r){b.y=b.r;b.vy=Math.abs(b.vy);AudioSys.wall();spawnSparks(b.x,b.y,'#29f6ff',4);}
      // paddle (circle vs rect, only when moving down & overlapping)
      const pw=paddle.effW()/2,ph=paddle.h/2;
      if(b.vy>0&&b.y+b.r>=paddle.y-ph&&b.y-b.r<=paddle.y+ph&&Math.abs(b.x-paddle.x)<=pw+b.r){
        const off=clamp((b.x-paddle.x)/pw,-1,1);
        const maxA=1.05,ang=-Math.PI/2+off*maxA;
        let nsp=sp+paddle.vx*0.12;
        if(paddle.vy<-60){nsp+=CFG.upBoost*(clamp(-paddle.vy/700,0,1.4)); // upward flick = ENERGY
          spawnSparks(b.x,paddle.y,'#ffd23d',14);spawnShock(b.x,paddle.y,50,'#ffd23d');
          addText(b.x,paddle.y-26,'BOOST!','#ffd23d',15);paddle.squash=0.5;}
        else if(paddle.vy>140){nsp*=CFG.downDampen;}
        nsp=clamp(nsp,CFG.minBallSpeed,CFG.maxBallSpeed*1.2);
        b.vx=Math.cos(ang)*nsp+paddle.vx*0.15;b.vy=Math.sin(ang)*nsp;
        b.y=paddle.y-ph-b.r-0.5;
        AudioSys.paddle(paddle.vy);paddle.squash=Math.max(paddle.squash,0.3);
        spawnSparks(b.x,paddle.y-ph,'#29f6ff',6);
        combo=Math.max(0,combo-0);comboTimer=Math.max(comboTimer,1);
      }
      // bricks
      const t=perfT;
      for(const br of bricks){
        if(br.dead)continue;
        const rc=brickRect(br,t);
        if(b.y+ b.r<rc.y-rc.h/2-4||b.y-b.r>rc.y+rc.h/2+4)continue;
        if(b.x+b.r<rc.x-rc.w/2-4||b.x-b.r>rc.x+rc.w/2+4)continue;
        const ghost=b.type==='ghost'&&(b.ghostT>0||b.ghostHits<BALL_DEFS.ghost.phase);
        const solid=brickSolidAt(br,t);
        if(!solid){ // phased blink: pass through with shimmer
          if(Math.random()<0.1)spawnSparks(b.x,b.y,'rgba(159,180,255,.8)',1);
          continue;
        }
        const hit=circleRectCollide(b.x,b.y,b.r,rc);
        if(!hit)continue;
        const spd=ballSpeed(b);
        // --- gate checks (NO hp bricks; difficulty = angle/speed/timing) ---
        if(br.type==='prism'){
          const steep=Math.abs(b.vy)/Math.max(1,spd);
          if(steep<CFG.prismSteep){ // RESIST
            reflectVel(b,hit.nx,hit.ny);b.x+=hit.nx*(hit.pen+0.5);b.y+=hit.ny*(hit.pen+0.5);
            br.resist=0.8;AudioSys.resist();
            addText(rc.x,rc.y,'RESISTED','#9fb4ff',13);
            if(!br.hintDone){br.hintDone=true;showHint('Prism ⌃ bricks need a STEEP angle — attack from above/below!');}
            break;
          }
        }
        if(br.type==='velo'){
          if(spd<CFG.speedBrickThresh){
            reflectVel(b,hit.nx,hit.ny);b.x+=hit.nx*(hit.pen+0.5);b.y+=hit.ny*(hit.pen+0.5);
            br.resist=0.8;AudioSys.resist();
            addText(rc.x,rc.y,'TOO SLOW','#ff9f2e',13);
            if(!br.hintDone){br.hintDone=true;showHint('Velocity ⌁ bricks need SPEED — flick UP into the ball for a boost!');}
            break;
          }
        }
        if(!br.hintDone&&br.type!=='normal'){br.hintDone=true;
          if(br.type==='blink')showHint('Blink bricks: solid only while LIT. Time your hit!');
          if(br.type==='drift')showHint('Drifters slide — lead your shot!');
          if(br.type==='bomb')showHint('Volatile! Blow it up for a chain bonus!');}
        if(ghost){ // phase through, damage, no bounce
          b.ghostHits++;breakBrick(br,b);break;
        }
        // normal break + bounce
        reflectVel(b,hit.nx,hit.ny);b.x+=hit.nx*(hit.pen+0.5);b.y+=hit.ny*(hit.pen+0.5);
        // golden keeps energy
        if(b.type==='gold'){b.vx*=1.03;b.vy*=1.03;}
        breakBrick(br,b);break;
      }
    }
    enforceSpeed(b);spawnTrailDot(b);
    for(const tr of b.trail)tr.life-=dt;
    while(b.trail.length&&b.trail[0].life<=0)b.trail.shift();
    // floor: shield net or lose
    if(b.y>H+b.r*2){
      if(paddle.shield){ // bounce once off the net
        paddle.shield=false;paddleShieldPersist=false;
        b.y=H-30;b.vy=-Math.abs(b.vy)*1.05||-500;b.vx+=rand(-80,80);
        AudioSys.power();spawnShock(b.x,H-24,80,'#8affda');
        addText(b.x,H-70,'SAVED BY AEGIS!','#8affda',18);toast('AEGIS NET SPENT ⛨');
        continue;
      }
      balls.splice(bi,1);
      if(!balls.length)loseLife();
    }
  }
  // slow-mo decay
  if(slowmo>0)slowmo-=dt;
  timeScale=lerp(timeScale,slowmoT>0?0.3:(slowmo>0?0.6:1),0.15);
}
function updatePowerups(dt){
  for(let i=powerups.length-1;i>=0;i--){const p=powerups[i];
    p.t+=dt;p.vy=Math.min(320,p.vy+140*dt);p.x+=p.vx*dt;p.y+=p.vy*dt;p.rot+=dt*2;
    // magnet-ish toward paddle when close
    const dx=paddle.x-p.x,dy=paddle.y-p.y,d=Math.hypot(dx,dy);
    if(d<CFG.magnetR&&d>1){p.x+=dx/d*160*dt;p.y+=dy/d*160*dt;}
    const pw=paddle.effW()/2;
    if(p.y>paddle.y-paddle.h&&p.y<paddle.y+paddle.h+8&&Math.abs(p.x-paddle.x)<pw+12){
      catchPower(p);powerups.splice(i,1);continue;}
    if(p.y>H+20)powerups.splice(i,1);
  }
}
function updateLasers(dt){
  const t=perfT;
  for(let i=lasers.length-1;i>=0;i--){const l=lasers[i];l.y+=l.vy*dt;
    if(l.y<-20){lasers.splice(i,1);continue;}
    for(const br of bricks){if(br.dead)continue;if(!brickSolidAt(br,t))continue;
      const rc=brickRect(br,t);
      if(Math.abs(l.x-rc.x)<rc.w/2+3&&Math.abs(l.y-rc.y)<rc.h/2+6){
        if(br.type==='prism'||br.type==='velo'){br.resist=0.5;AudioSys.resist();}
        else breakBrick(br,null);
        spawnSparks(l.x,l.y,'#ff3df0',8);lasers.splice(i,1);break;}}
  }
}
function loseLife(){
  lives--;updateHUD();AudioSys.lose();
  shake=14;hitFlash=0.9;slowmoT=0.7; // brief slow-mo drama
  spawnParticles(paddle.x,paddle.y,'#ff4d7e',40,420,1,4);
  combo=0;mult=1;overdrive=false;odMult=1;$('overdrive-banner').classList.add('hidden');
  if(lives<=0){gameOver(false);}
  else{spawnStuckBall();toast(lives+(lives===1?' LIFE':' LIVES')+' LEFT');}
}
/* ================= FLOW ================= */
let perfT=0,lastT=0;
function startGame(){
  AudioSys.unlock();AudioSys.ui();
  score=0;lives=CFG.lives;levelIdx=0;mult=1;combo=0;maxOd=1;stats={maxOverdrive:1,timeAbove:0,bricks:0};
  paddleShieldPersist=false;hintShown={};
  buildLevel(0);setState('playing');updateHUD();
  toast('GET IT ABOVE THE BRICKS!');
}
function nextLevel(){
  levelIdx++;
  if(levelIdx>=LEVELS.length){gameOver(true);return;}
  buildLevel(levelIdx);setState('playing');updateHUD();
}
function gameOver(win){
  const nb=saveHigh();
  $('over-high').textContent=fmt(high);
  $('newbest').classList.toggle('hidden',!nb);
  $('over-title').textContent=win?'🏆 YOU WIN! 🏆':'GAME OVER';
  $('over-stats').innerHTML='Score <b>'+fmt(score)+'</b> • Level '+(levelIdx+1)+
    ' • Max overdrive <b>x'+Math.floor(maxOd)+'</b> • '+stats.timeAbove.toFixed(1)+'s above';
  setState(win?'victory':'over');
  if(win){spawnConfetti(W/2,H/3);AudioSys.bank(8);}else AudioSys.lose();
}
function levelClear(){
  const nb=saveHigh();
  $('clear-title').textContent='LEVEL '+(levelIdx+1)+' CLEAR!';
  $('clear-stats').innerHTML='Score <b>'+fmt(score)+'</b> • Max OD <b>x'+Math.floor(maxOd)+
    '</b> • Best '+fmt(high);
  setState('clear');AudioSys.bank(6);
  fireworks=[];for(let i=0;i<5;i++)fireworks.push({x:rand(W*0.15,W*0.85),y:rand(H*0.15,H*0.45),t:-i*0.4});
  saveHigh();
}
function setState(s){
  state=s;
  // hide all overlays:
  ['menu','help','pause','clear','over'].forEach(id=>$(id).classList.add('hidden'));
  $('hud').classList.toggle('hidden',!(s==='playing'||s==='paused'));
  if(s==='menu'){$('menu').classList.remove('hidden');$('menu-high').textContent=fmt(high);
    if(helpOpen){$('help').classList.remove('hidden');}}
  if(s==='paused')$('pause').classList.remove('hidden');
  if(s==='clear')$('clear').classList.remove('hidden');
  if(s==='over'||s==='victory')$('over').classList.remove('hidden');
  if(s!=='playing')$('overdrive-banner').classList.add('hidden');
}
function togglePause(){
  if(state==='playing'){setState('paused');}
  else if(state==='paused'){setState('playing');AudioSys.ui();}
}
function toggleMute(){AudioSys.unlock();AudioSys.setMute(!AudioSys.muted);
  $('btn-mute').innerHTML=AudioSys.muted?'✕':'♫';$('btn-sound').textContent='SOUND: '+(AudioSys.muted?'OFF':'ON');}
/* ---- attract-mode demo background ---- */
const demo={ball:null,bricks:[]};
function demoInit(){
  demo.bricks=[];const n=Math.ceil(W/70);
  for(let i=0;i<n;i++)demo.bricks.push({x:40+i*70,y:H*0.3+Math.sin(i)*20,c:'hsl('+(i*37%360)+',80%,60%)'});
  demo.ball={x:W/2,y:H/2,vx:260,vy:-220};
}
function demoUpdate(dt){
  attractT+=dt;const b=demo.ball;if(!b)return;
  b.x+=b.vx*dt;b.y+=b.vy*dt;
  if(b.x<10||b.x>W-10)b.vx*=-1;if(b.y<10||b.y>H-10)b.vy*=-1;
}
/* ================= UPDATE ================= */
function update(rawDt){
  const dt=Math.min(rawDt,0.033)*timeScale;
  perfT+=dt;
  // bg anim always
  for(const s of stars){s.tw+=dt*2;}
  chroma=Math.max(0,chroma-dt*0.4);
  shake=Math.max(0,shake-dt*30);hitFlash=Math.max(0,hitFlash-dt*2.2);
  bankFlash=Math.max(0,bankFlash-dt);if(slowmoT>0)slowmoT-=rawDt;
  // particles
  for(let i=particles.length-1;i>=0;i--){const p=particles[i];
    p.life-=dt;if(p.life<=0){particles.splice(i,1);continue;}
    p.vy+=(p.grav||0)*dt;p.x+=p.vx*dt;p.y+=p.vy*dt;p.rot+=(p.vr||0)*dt;}
  for(let i=floaters.length-1;i>=0;i--){const f=floaters[i];f.t+=dt;f.y-=34*dt;if(f.t>f.life)floaters.splice(i,1);}
  for(let i=shockwaves.length-1;i>=0;i--){const s=shockwaves[i];s.t+=dt;if(s.t>s.life)shockwaves.splice(i,1);}
  AudioSys.music(rawDt);
  if(state==='menu'||state==='over'||state==='victory'){demoUpdate(rawDt);return;}
  if(state!=='playing')return;
  if(comboTimer>0){comboTimer-=dt;if(comboTimer<=0){combo=0;mult=Math.max(1,mult-1);}}
  paddle.update(dt);
  updateBalls(dt);updatePowerups(dt);updateLasers(dt);updateOverdrive(rawDt);
  if(paddle.wide<0)paddle.wide=0;
  // level clear?
  if(bricks.length&&bricks.every(b=>b.dead)){levelClear();}
  updateHUD();
}
function updateHUD(){
  $('hud-score').textContent=fmt(score);
  $('hud-high').textContent=fmt(Math.max(high,score));
  const m=overdrive?Math.floor(odMult)*mult:mult;
  $('hud-mult').textContent='x'+m;
  $('hud-mult').style.color=overdrive?'#ff3df0':(mult>=8?'#ffd23d':'#eaf6ff');
  $('hud-lives').textContent='●'.repeat(Math.max(0,lives))+'○'.repeat(Math.max(0,3-lives));
}
/* ================= RENDER ================= */
function render(){
  ctx.save();
  if(shake>0&&!reducedMotion())ctx.translate(rand(-shake,shake)*0.5,rand(-shake,shake)*0.5);
  drawBG();
  if(state==='menu'||state==='over'||state==='victory'){drawDemo();drawParticles();ctx.restore();return;}
  drawTopZone();
  drawBricks();
  drawPowerups();drawLasers();
  drawPaddle();
  drawBalls();
  drawParticles();drawFloaters();drawShocks();
  if(state==='clear')drawFireworks();
  drawFxOverlays();
  ctx.restore();
}
function drawBG(){
  const od=overdrive?1:0;
  const heat=clamp(Math.log2(Math.max(1,mult))/5,0,1);
  const g=ctx.createLinearGradient(0,0,0,H);
  const hue=232+od*40+heat*20;
  g.addColorStop(0,`hsl(${hue},70%,${7+od*6}%)`);
  g.addColorStop(0.6,'#0a0e27');g.addColorStop(1,'#12041f');
  ctx.fillStyle=g;ctx.fillRect(-20,-20,W+40,H+40);
  // stars
  for(const s of stars){const a=0.25+s.z*0.6*(0.6+0.4*Math.sin(s.tw));
    ctx.fillStyle=`rgba(255,255,255,${a})`;ctx.fillRect(s.x,s.y,s.z>0.7?2:1,s.z>0.7?2:1);}
  // grid floor
  ctx.strokeStyle=`rgba(41,246,255,${0.10+heat*0.12+od*0.15})`;ctx.lineWidth=1;
  ctx.beginPath();
  const hz=H*0.55;
  for(let i=0;i<=12;i++){const x=(i/12)*W;ctx.moveTo(lerp(W/2,x,0.4),hz);ctx.lineTo(x,H);}
  for(let i=0;i<6;i++){const y=hz+((H-hz)/6)*i;ctx.moveTo(0,y);ctx.lineTo(W,y);}
  ctx.stroke();
  // nebula blobs (overdrive intensity)
  const nB=reducedMotion()?0:(od?5:2+Math.round(heat*2));
  for(let i=0;i<nB;i++){
    const x=(Math.sin(perfT*0.2+i*2.4)*0.5+0.5)*W,y=(Math.cos(perfT*0.16+i*1.7)*0.5+0.5)*H*0.7;
    const r=140+od*80,gr=ctx.createRadialGradient(x,y,0,x,y,r);
    const c=i%2?'255,61,240':'41,246,255';
    gr.addColorStop(0,`rgba(${c},${0.05+od*0.10+heat*0.04})`);gr.addColorStop(1,'rgba(0,0,0,0)');
    ctx.fillStyle=gr;ctx.fillRect(x-r,y-r,r*2,r*2);}
  // vignette + hit flash + chroma
  if(hitFlash>0){ctx.fillStyle=`rgba(255,80,120,${hitFlash*0.25})`;ctx.fillRect(-20,-20,W+40,H+40);}
  if(bankFlash>0){ctx.fillStyle=`rgba(57,230,163,${bankFlash*0.2})`;ctx.fillRect(-20,-20,W+40,H+40);}
  if(chroma>0.02){ctx.strokeStyle=`rgba(255,61,240,${chroma*0.5})`;ctx.lineWidth=3;
    ctx.strokeRect(4,4,W-8,H-8);}
}
function drawDemo(){
  for(const b of demo.bricks){ctx.fillStyle=b.c;ctx.globalAlpha=0.5+0.3*Math.sin(attractT*2+b.x);
    roundRect(b.x-26,b.y-9,52,18,5);ctx.fill();}
  ctx.globalAlpha=1;
  const d=demo.ball;
  if(d){ctx.fillStyle='#fff';ctx.shadowColor='#29f6ff';ctx.shadowBlur=18;
    ctx.beginPath();ctx.arc(d.x,d.y,9,0,TAU);ctx.fill();ctx.shadowBlur=0;}
  ctx.fillStyle='rgba(234,246,255,.55)';ctx.font='13px sans-serif';ctx.textAlign='center';
  ctx.fillText('— demo signal — get the ball ABOVE the bricks —',W/2,H*0.72);
}
function drawTopZone(){
  ctx.save();
  ctx.strokeStyle=overdrive?'#ff3df0':'rgba(255,61,240,.35)';
  ctx.setLineDash([10,8]);ctx.lineWidth=overdrive?3:1.5;
  ctx.shadowColor='#ff3df0';ctx.shadowBlur=overdrive?16:4;
  ctx.beginPath();ctx.moveTo(8,topZoneY);ctx.lineTo(W-8,topZoneY);ctx.stroke();
  ctx.setLineDash([]);ctx.shadowBlur=0;
  ctx.fillStyle=overdrive?'#ffd7fa':'rgba(255,61,240,.6)';ctx.font='700 11px sans-serif';ctx.textAlign='center';
  ctx.fillText(overdrive?'★ OVERDRIVE ZONE ★':'▲ OVERDRIVE ZONE — GET ABOVE ▲',W/2,topZoneY-6);
  ctx.restore();
}
function roundRect(x,y,w,h,r){ctx.beginPath();ctx.moveTo(x+r,y);ctx.arcTo(x+w,y,x+w,y+h,r);
  ctx.arcTo(x+w,y+h,x,y+h,r);ctx.arcTo(x,y+h,x,y,r);ctx.arcTo(x,y,x+w,y,r);ctx.closePath();}
function drawBricks(){
  const t=perfT;
  const icons={prism:'⌃',velo:'⌁',blink:'◉',drift:'⇄',bomb:'✸'};
  for(const b of bricks){
    if(b.dead)continue;
    const rc=brickRect(b,t);
    const solid=brickSolidAt(b,t);
    const age=clamp((t-b.born)*2,0,1);
    ctx.save();ctx.globalAlpha=(solid?1:0.28)*age;
    const cols={normal:['#123a6b','#3fa9ff'],prism:['#3a1160','#b06bff'],velo:['#6b2f10','#ff9f2e'],
      blink:['#232c4d','#9fb4ff'],drift:['#0b4d33','#39e6a3'],bomb:['#5e0f16','#ff4d5e']}[b.type];
    const g=ctx.createLinearGradient(rc.x,rc.y-rc.h/2,rc.x,rc.y+rc.h/2);
    g.addColorStop(0,cols[1]);g.addColorStop(1,cols[0]);
    ctx.shadowColor=cols[1];ctx.shadowBlur=b.resist>0?18:8;
    roundRect(rc.x-rc.w/2,rc.y-rc.h/2,rc.w,rc.h,6);ctx.fillStyle=g;ctx.fill();
    ctx.shadowBlur=0;ctx.strokeStyle='rgba(255,255,255,.5)';ctx.lineWidth=1;ctx.stroke();
    if(b.resist>0){ctx.fillStyle=`rgba(160,170,200,${clamp(b.resist,0,0.7)})`;
      roundRect(rc.x-rc.w/2,rc.y-rc.h/2,rc.w,rc.h,6);ctx.fill();
      ctx.fillStyle='#fff';ctx.font='700 11px sans-serif';ctx.textAlign='center';
      ctx.fillText('RESIST',rc.x,rc.y+4);
      if(b.type==='prism'){ctx.font='11px sans-serif';ctx.fillText('▲ ▲',rc.x,rc.y-12);}
      if(b.type==='velo'){ctx.font='11px sans-serif';ctx.fillText('» »',rc.x,rc.y-12);}
    }else{
      ctx.fillStyle='rgba(255,255,255,.92)';ctx.font='700 13px sans-serif';ctx.textAlign='center';
      if(b.type==='normal'){ctx.fillRect(rc.x-8,rc.y-1.5,16,3);}
      else ctx.fillText(icons[b.type]||'',rc.x,rc.y+5);
      if(b.type==='blink'&&!solid){/* phased */}
      if(b.type==='drift'){ctx.fillStyle='rgba(255,255,255,.35)';ctx.font='10px sans-serif';
        ctx.fillText('‹ ›',rc.x,rc.y+rc.h/2-3);}
    }
    if(b.flash>0){ctx.fillStyle=`rgba(255,255,255,${b.flash})`;
      roundRect(rc.x-rc.w/2,rc.y-rc.h/2,rc.w,rc.h,6);ctx.fill();}
    ctx.restore();
    if(b.resist>0)b.resist-=1/60;if(b.flash>0)b.flash-=1/60;
  }
}
function drawPaddle(){
  const w=paddle.effW(),h=paddle.h+(paddle.squash>0?paddle.squash*10:0);
  const wob=reducedMotion()?0:Math.sin(perfT*6)*1.5;
  ctx.save();ctx.translate(paddle.x,paddle.y);
  // engine glow shows vertical velocity
  const vv=clamp(-paddle.vy/800,-1,1);
  if(vv>0.08){const g=ctx.createLinearGradient(0,h/2,0,h/2+30);
    g.addColorStop(0,`rgba(255,210,61,${vv*0.9})`);g.addColorStop(1,'rgba(255,90,0,0)');
    ctx.fillStyle=g;ctx.beginPath();
    ctx.moveTo(-w/2+8,h/2);ctx.lineTo(w/2-8,h/2);ctx.lineTo(0,h/2+14+vv*22+wob);ctx.closePath();ctx.fill();}
  if(vv<-0.08){ctx.fillStyle=`rgba(41,246,255,${-vv*0.35})`;
    roundRect(-w/2,-h/2-8-(-vv)*10,w,6,3);ctx.fill();}
  const g2=ctx.createLinearGradient(0,-h/2,0,h/2);
  g2.addColorStop(0,'#bff6ff');g2.addColorStop(0.4,'#29f6ff');g2.addColorStop(1,'#0d5a80');
  ctx.shadowColor=paddle.laser>0?'#ff3df0':'#29f6ff';ctx.shadowBlur=16;
  roundRect(-w/2,-h/2,w,h,8);ctx.fillStyle=g2;ctx.fill();
  ctx.shadowBlur=0;ctx.strokeStyle='rgba(255,255,255,.8)';ctx.lineWidth=1.5;ctx.stroke();
  // laser pods
  if(paddle.laser>0){ctx.fillStyle='#ff3df0';ctx.shadowColor='#ff3df0';ctx.shadowBlur=10;
    ctx.fillRect(-w/2+4,-h/2-10,6,10);ctx.fillRect(w/2-10,-h/2-10,6,10);ctx.shadowBlur=0;}
  ctx.restore();
  // shield net
  if(paddle.shield){ctx.save();ctx.strokeStyle='#8affda';ctx.lineWidth=2.5;
    ctx.shadowColor='#8affda';ctx.shadowBlur=12;ctx.globalAlpha=0.8+0.2*Math.sin(perfT*5);
    ctx.beginPath();for(let x=8;x<=W-8;x+=14){ctx.moveTo(x,H-24);ctx.lineTo(x+7,H-16);}ctx.stroke();ctx.restore();}
}
function drawBalls(){
  for(const b of balls){
    const D=BALL_DEFS[b.type];
    // ribbon trail
    for(let i=1;i<b.trail.length;i++){const p=b.trail[i],q=b.trail[i-1];
      ctx.strokeStyle=D.glow;ctx.globalAlpha=clamp(p.life*2.4,0,0.55);ctx.lineWidth=b.r*1.1*(i/b.trail.length)+1;
      ctx.beginPath();ctx.moveTo(q.x,q.y);ctx.lineTo(p.x,p.y);ctx.stroke();}
    ctx.globalAlpha=1;
    ctx.save();ctx.translate(b.x,b.y);
    ctx.shadowColor=D.glow;ctx.shadowBlur=b.type==='normal'?10:20;
    if(b.type==='ghost')ctx.globalAlpha=0.55+0.25*Math.sin(perfT*10);
    if(b.type==='fire'){ // flickering fireball
      const fr=b.r+Math.sin(perfT*30+b.spin)*2;
      const g=ctx.createRadialGradient(0,0,1,0,0,fr+8);
      g.addColorStop(0,'#fff2c0');g.addColorStop(0.4,D.color);g.addColorStop(1,'rgba(255,60,0,0)');
      ctx.fillStyle=g;ctx.beginPath();ctx.arc(0,0,fr+8,0,TAU);ctx.fill();}
    const g2=ctx.createRadialGradient(-b.r*0.3,-b.r*0.3,1,0,0,b.r);
    g2.addColorStop(0,'#ffffff');g2.addColorStop(0.5,D.color);g2.addColorStop(1,D.glow);
    ctx.fillStyle=g2;ctx.beginPath();ctx.arc(0,0,b.r,0,TAU);ctx.fill();
    ctx.shadowBlur=0;
    if(b.type==='volt'){ctx.strokeStyle='#fff';ctx.lineWidth=1.4;ctx.beginPath();
      ctx.moveTo(-4,-5);ctx.lineTo(1,-1);ctx.lineTo(-2,2);ctx.lineTo(4,5);ctx.stroke();}
    if(b.type==='gold'){ctx.fillStyle='#7a4d00';ctx.font='700 10px sans-serif';ctx.textAlign='center';ctx.fillText('★',0,3.5);}
    ctx.restore();
    if(b.stuck&&state==='playing'){
      ctx.fillStyle='#fff';ctx.font='700 14px sans-serif';ctx.textAlign='center';
      ctx.fillText(touchMode?'TAP TO LAUNCH':'CLICK / SPACE TO LAUNCH',W/2,paddle.y-44);
      ctx.strokeStyle='rgba(255,255,255,.4)';ctx.setLineDash([4,4]);ctx.beginPath();
      ctx.moveTo(b.x,b.y);ctx.lineTo(b.x,b.y-60);ctx.stroke();ctx.setLineDash([]);}
  }
}
function drawPowerups(){
  for(const p of powerups){const d=PU_DEFS[p.type];
    ctx.save();ctx.translate(p.x,p.y);
    ctx.shadowColor=d.color;ctx.shadowBlur=12;
    ctx.fillStyle='rgba(8,14,40,.95)';
    roundRect(-30,-13,60,26,13);ctx.fill();
    ctx.strokeStyle=d.color;ctx.lineWidth=2;ctx.stroke();ctx.shadowBlur=0;
    ctx.fillStyle=d.color;ctx.font='700 12px sans-serif';ctx.textAlign='center';
    ctx.fillText(d.icon+' '+d.label,0,4.5);
    ctx.restore();}
}
function drawLasers(){
  ctx.save();ctx.shadowColor='#ff3df0';ctx.shadowBlur=12;ctx.fillStyle='#ffd7fa';
  for(const l of lasers){ctx.fillRect(l.x-2,l.y-14,4,14);}ctx.restore();
}
function drawParticles(){
  for(const p of particles){const a=clamp(p.life/p.maxLife,0,1);
    ctx.save();ctx.globalAlpha=a;ctx.translate(p.x,p.y);ctx.rotate(p.rot||0);
    ctx.fillStyle=p.color;
    if(p.shard)ctx.fillRect(-p.size/2,-p.size/4,p.size,p.size/2);
    else{ctx.beginPath();ctx.arc(0,0,p.size,0,TAU);ctx.fill();}
    ctx.restore();}
  ctx.globalAlpha=1;
}
function drawFloaters(){
  ctx.save();ctx.textAlign='center';
  for(const f of floaters){const a=1-f.t/f.life;
    ctx.globalAlpha=a;ctx.font='800 '+f.size+'px sans-serif';
    ctx.shadowColor=f.color;ctx.shadowBlur=8;ctx.fillStyle=f.color;
    ctx.fillText(f.str,f.x,f.y);}
  ctx.restore();ctx.globalAlpha=1;
}
function drawShocks(){
  for(const s of shockwaves){const k=s.t/s.life;
    ctx.save();ctx.globalAlpha=1-k;ctx.strokeStyle=s.color;ctx.lineWidth=3*(1-k)+1;
    ctx.shadowColor=s.color;ctx.shadowBlur=10;
    ctx.beginPath();ctx.arc(s.x,s.y,lerp(s.r,s.maxR,k),0,TAU);ctx.stroke();ctx.restore();}
}
function drawFireworks(){
  clearT+=1/60;
  for(const f of fireworks){f.t+=1/60;
    if(f.t>0&&!f.done){f.done=true;spawnConfetti(f.x,f.y);AudioSys.explode();spawnShock(f.x,f.y,90,'#ffd23d');}
    if(f.t>0&&f.t<0.5){ctx.fillStyle='#fff';ctx.beginPath();ctx.arc(f.x,f.y-f.t*200,3,0,TAU);ctx.fill();}}
}
function drawFxOverlays(){
  // slow-mo tint
  if(timeScale<0.9){ctx.fillStyle='rgba(80,140,255,.12)';ctx.fillRect(0,0,W,H);}
  // active effect chips (bottom-left)
  ctx.save();ctx.font='700 12px sans-serif';ctx.textAlign='left';
  let y=H-70;
  const chip=(txt,c)=>{ctx.fillStyle='rgba(8,12,36,.8)';const w=ctx.measureText(txt).width+18;
    roundRect(10,y-14,w,20,10);ctx.fill();ctx.strokeStyle=c;ctx.lineWidth=1;ctx.stroke();
    ctx.fillStyle=c;ctx.fillText(txt,19,y);y-=26;};
  if(slowmo>0)chip('SLOW-MO '+slowmo.toFixed(0)+'s','#7db4ff');
  if(paddle.wide>0)chip('WIDE '+paddle.wide.toFixed(0)+'s','#29f6ff');
  if(paddle.laser>0)chip('LASER '+paddle.laser.toFixed(0)+'s','#ff3df0');
  if(paddle.shield)chip('AEGIS ⛨','#8affda');
  if(combo>=2)chip(combo+' COMBO','#ffd23d');
  ctx.restore();
}
/* ================= MAIN LOOP ================= */
function loop(t){
  requestAnimationFrame(loop);
  if(lastT===0)lastT=t;
  let dt=(t-lastT)/1000;lastT=t;
  if(dt>0.25)dt=0.25;
  update(dt);
  render();
}
/* ================= UI WIRING ================= */
function wire(){
  $('btn-start').onclick=()=>startGame();
  $('btn-help').onclick=()=>{AudioSys.unlock();AudioSys.ui();helpOpen=true;$('help').classList.remove('hidden');};
  $('btn-help-close').onclick=()=>{AudioSys.ui();helpOpen=false;$('help').classList.add('hidden');};
  $('btn-resume').onclick=()=>togglePause();
  $('btn-quit').onclick=()=>{setState('menu');};
  $('btn-next').onclick=()=>{AudioSys.ui();nextLevel();};
  $('btn-retry').onclick=()=>{AudioSys.ui();startGame();};
  $('btn-menu2').onclick=()=>{setState('menu');};
  $('btn-pause').onclick=()=>togglePause();
  $('btn-pause-m').onclick=()=>togglePause();
  $('btn-launch').onclick=()=>{AudioSys.unlock();if(paddle.laser>0)fireLaser();else launchBalls();};
  $('btn-mute').onclick=()=>toggleMute();
  $('btn-sound').onclick=()=>toggleMute();
  $('btn-motion').onclick=e=>{motionOK=!motionOK;
    e.target.textContent='MOTION: '+(motionOK?'FULL':'CALM');};
  $('btn-stick').onclick=e=>{stickEnabled=!stickEnabled;
    e.target.textContent='STICK: '+(stickEnabled?'ON':'OFF');};
  if(reducedMotion()){motionOK=false;$('btn-motion').textContent='MOTION: CALM';}
  document.addEventListener('visibilitychange',()=>{if(document.hidden&&state==='playing')setState('paused');});
  window.addEventListener('blur',()=>{if(state==='playing')setState('paused');});
  window.addEventListener('pointerdown',()=>AudioSys.unlock(),{once:false});
}
/* ================= INIT ================= */
function init(){
  resize();wire();
  paddle.x=W/2;paddle.y=H-90;
  $('menu-high').textContent=fmt(high);
  demoInit();setState('menu');
  requestAnimationFrame(t=>{lastT=t;requestAnimationFrame(loop);});
}
/* Hidden test/automation hook (no UI effect). */
try{
Object.defineProperty(window,'__game',{value:{
  get state(){return state;}, get score(){return score;}, get lives(){return lives;},
  get level(){return levelIdx;}, get balls(){return balls;}, get bricks(){return bricks;},
  get powerups(){return powerups;}, get overdrive(){return overdrive;}, get odMult(){return odMult;},
  get paddle(){return paddle;}, get stats(){return stats;}, get mult(){return mult;},
  start:startGame, launch:launchBalls, next:nextLevel, load:i=>{levelIdx=i;buildLevel(i);},
},configurable:true});}catch(e){}
init();
