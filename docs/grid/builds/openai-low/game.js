/* Orbit Breaker — a dependency-free kinetic arcade game. */
(() => {
  'use strict';
  const canvas = document.getElementById('game');
  if (!canvas) return;
  const ctx = canvas.getContext('2d', { alpha: false });
  const physics = globalThis.OrbitPhysics;
  const rules = globalThis.OrbitRules;
  const portraitQuery = matchMedia('(max-width: 650px) and (orientation: portrait)');
  const PORTRAIT = portraitQuery.matches;
  // A rotation changes the logical world as well as the CSS aspect ratio. A
  // clean reload avoids ever showing a stretched intermediate frame.
  const reloadForOrientation = () => location.reload();
  if (portraitQuery.addEventListener) portraitQuery.addEventListener('change',reloadForOrientation);
  else portraitQuery.addListener?.(reloadForOrientation);
  // Portrait has its own logical viewport. The canvas is scaled uniformly, so
  // bricks, balls, and collision geometry never get stretched on a phone.
  const W = PORTRAIT ? 640 : 960, H = PORTRAIT ? 960 : 640, TAU = Math.PI * 2;
  const FIELD = { left:20, right:W-20, top:38, brickTop:PORTRAIT ? 128 : 97, bottom:H };
  const PADDLE_MIN_Y = PORTRAIT ? 650 : 475, PADDLE_MAX_Y = H - (PORTRAIT ? 56 : 56);
  const STEP = 1 / 120, MAX_STEPS = 12, MAX_SPEED = 1080;
  const MAX_BALLS = 8, MAX_PARTICLES = 720, MAX_RINGS = 130, MAX_TEXTS = 70, MAX_DROPS = 5;
  const $ = id => document.getElementById(id);
  const ui = { score:$('score'), combo:$('combo'), wave:$('wave'), lives:$('lives'), best:$('bestScore'), final:$('finalScore'), victoryWave:$('victoryWave'), toast:$('toast'), orbit:$('orbit'), power:$('power'), mute:$('muteBtn'), pause:$('pauseBtn'), touchPause:$('touchPause') };
  const COLORS = { cyan:'#55e9ff', pink:'#ff4dbe', yellow:'#ffd166', purple:'#9d7bff', green:'#63f4b2', white:'#eef2ff', red:'#ff657d' };
  const clamp = (v,a,b) => Math.max(a, Math.min(b,v));
  const lerp = (a,b,t) => a + (b-a)*t;
  const rand = (a,b) => a + Math.random()*(b-a);
  const pick = a => a[(Math.random()*a.length)|0];
  let savedBest = 0, savedMuted = false;
  try { savedBest = Number(localStorage.getItem('orbit-breaker-best') || 0) || 0; savedMuted = localStorage.getItem('orbit-breaker-muted') === '1'; } catch (e) {}
  const state = {
    mode:'menu', score:0, wave:1, lives:3, combo:0, multiplier:1, best:savedBest,
    elapsed:0, comboClock:0, shake:0, flash:0, waveIntro:0, reduced:matchMedia('(prefers-reduced-motion: reduce)').matches,
    muted:savedMuted, autoPaused:false, physicsTick:0,
    orbit:{active:false,level:0,chain:0,pulse:0,lastSound:0,peak:0}
  };
  const startX = W / 2, startY = H - (PORTRAIT ? 100 : 92);
  const input = { left:false,right:false,up:false,down:false, x:startX,y:startY-5, pointerId:null, stickX:0,stickY:0 };
  const stars = Array.from({length:PORTRAIT ? 170 : 130}, () => ({x:rand(0,W), y:rand(20,H), r:rand(.25,1.6), z:rand(.2,1), tw:rand(0,TAU)}));
  let dpr=1, last=0, accumulator=0, audio=null, toastTimer=0;
  let bricks=[], balls=[], particles=[], drops=[], texts=[], rings=[];
  const paddle = {x:startX,y:startY,w:PORTRAIT ? 170 : 154,h:18,vx:0,vy:0,targetX:startX,targetY:startY,tilt:0};

  function resize() {
    const rect=canvas.getBoundingClientRect(); dpr=Math.min(devicePixelRatio||1,2);
    canvas.width=Math.max(1,Math.floor(rect.width*dpr)); canvas.height=Math.max(1,Math.floor(rect.height*dpr));
  }
  addEventListener('resize',resize); resize();

  function tone(freq,duration=.08,type='sine',volume=.035,slide=0) {
    if (!audio || state.muted) return;
    const now=audio.currentTime, osc=audio.createOscillator(), gain=audio.createGain();
    osc.type=type; osc.frequency.setValueAtTime(freq,now); osc.frequency.exponentialRampToValueAtTime(Math.max(35,freq+slide),now+duration);
    gain.gain.setValueAtTime(.0001,now); gain.gain.exponentialRampToValueAtTime(volume,now+.008); gain.gain.exponentialRampToValueAtTime(.0001,now+duration);
    osc.connect(gain); gain.connect(audio.destination); osc.start(now); osc.stop(now+duration+.02);
  }
  function noise(duration=.08,volume=.025) {
    if (!audio || state.muted) return;
    const len=Math.max(1,Math.floor(audio.sampleRate*duration)), buf=audio.createBuffer(1,len,audio.sampleRate), data=buf.getChannelData(0);
    for(let i=0;i<len;i++) data[i]=(Math.random()*2-1)*(1-i/len);
    const src=audio.createBufferSource(),gain=audio.createGain();src.buffer=buf;gain.gain.value=volume;src.connect(gain);gain.connect(audio.destination);src.start();
  }
  function initAudio() {
    if (state.muted) return;
    if (!audio) { try { audio=new (window.AudioContext||window.webkitAudioContext)(); } catch(e) {} }
    if (audio && audio.state==='suspended') audio.resume();
  }
  function sfx(name) {
    if(name==='hit') tone(190,.045,'triangle',.025,130);
    if(name==='brick') { tone(390+state.combo*8,.07,'square',.028,190); if(state.combo>4) tone(650,.1,'sine',.016,90); }
    if(name==='special') { tone(240,.12,'sawtooth',.035,500); tone(720,.22,'sine',.018,-130); }
    if(name==='drop') { tone(570,.08,'triangle',.03,250); tone(880,.13,'sine',.018,0); }
    if(name==='orbit') { tone(180,.18,'sawtooth',.04,500); tone(440,.3,'sine',.025,420); }
    if(name==='cascade') { const f=260+state.orbit.level*80; tone(f,.08,'square',.028,220); if(state.orbit.level>=4) tone(f*1.5,.13,'sine',.02,160); }
    if(name==='lose') { tone(210,.24,'sawtooth',.04,-150); noise(.16,.02); }
    if(name==='level') [0,4,7,12].forEach((n,i)=>setTimeout(()=>tone(320*Math.pow(2,n/12),.2,'sine',.035,80),i*80));
  }
  function setMuted(value,startAudio=true) {
    state.muted=!!value;
    try { localStorage.setItem('orbit-breaker-muted',state.muted?'1':'0'); } catch(e) {}
    if (state.muted && audio) audio.suspend?.(); else if (!state.muted && startAudio) initAudio();
    ui.mute.textContent=state.muted?'🔇':'♪'; ui.mute.setAttribute('aria-label',state.muted?'Unmute sound':'Mute sound'); ui.mute.setAttribute('aria-pressed',String(state.muted)); ui.mute.title=state.muted?'Unmute sound':'Mute sound';
  }

  function generateBricks() {
    bricks=[]; const cols=PORTRAIT ? 8 : 12,gap=8,margin=PORTRAIT ? 32 : 48,bw=(W-margin*2-gap*(cols-1))/cols,rows=Math.min(6+state.wave,9),bh=PORTRAIT ? 32 : 29;
    const palette=[COLORS.cyan,'#5fb7ff',COLORS.purple,COLORS.pink,'#ff7a9e',COLORS.yellow,COLORS.green];
    let id=0;
    for(let row=0;row<rows;row++) for(let col=0;col<cols;col++) {
      const roll=Math.random(), wave=state.wave; let type='normal';
      if(row<rows-1 && roll<.10+wave*.012) type='angle';
      else if(row<rows-2 && roll<.19+wave*.014) type='speed';
      else if(row>0 && roll<.27+wave*.01) type='phase';
      bricks.push({id:id++,x:margin+col*(bw+gap),y:FIELD.brickTop+row*(bh+gap),w:bw,h:bh,row,col,type,color:palette[(row+col+state.wave)%palette.length],alive:true,phase:rand(0,TAU),open:true,hitFlash:0,lastHitTick:-1});
    }
  }
  function makeBall(x=paddle.x,y=paddle.y-25,vx=rand(-210,210),vy=-480,mode='core') {
    if (balls.length>=MAX_BALLS) return null;
    const raw=Math.hypot(vx,vy)||480, speed=clamp(raw,330,MAX_SPEED);
    const b={x,y,vx:vx/raw*speed,vy:vy/raw*speed,r:PORTRAIT ? 9 : 8,mode,trail:[],alive:true,age:0,spin:rand(0,TAU),powers:{},wallContact:false,skipBrickId:-1,skipTick:-1};
    balls.push(b); return b;
  }
  function resetOrbit() { state.orbit.active=false;state.orbit.level=0;state.orbit.chain=0;state.orbit.pulse=0;state.orbit.peak=0; }
  function resetWave() {
    generateBricks();balls=[];drops=[];particles=[];texts=[];rings=[];accumulator=0;resetOrbit();
    paddle.x=startX;paddle.y=startY;paddle.targetX=startX;paddle.targetY=startY;paddle.vx=0;paddle.vy=0;makeBall();
    state.combo=0;state.multiplier=1;state.comboClock=0;state.waveIntro=1.7;updateHud();
  }
  function newRun() {
    state.score=0;state.wave=1;state.lives=3;state.combo=0;state.multiplier=1;state.elapsed=0;state.cleared=false;state.autoPaused=false;resetWave();state.mode='playing';updatePauseA11y();hideScreens();canvas.focus();render();sfx('level');showToast('BREAK THE WALL  •  RISE ABOVE IT',1.8);
  }
  function nextWave() { state.wave++;state.mode='playing';updatePauseA11y();hideScreens();resetWave();render();sfx('level');showToast(`WAVE ${String(state.wave).padStart(2,'0')}  •  FRENZY LIMIT RISING`,1.8); }
  function hideScreens() { document.querySelectorAll('.screen').forEach(e=>e.classList.remove('active')); }
  function screen(id) { hideScreens();$(id).classList.add('active'); }
  function updatePauseA11y() {
    const paused=state.mode==='paused';
    ui.pause.setAttribute('aria-pressed',String(paused));ui.pause.setAttribute('aria-label',paused?'Resume game':'Pause game');ui.pause.title=paused?'Resume (P)':'Pause (P)';
    ui.touchPause.setAttribute('aria-pressed',String(paused));ui.touchPause.setAttribute('aria-label',paused?'Resume':'Pause');
  }
  function togglePause(auto=false) {
    if(state.mode==='playing') { state.mode='paused';state.autoPaused=!!auto;input.left=input.right=input.up=input.down=false;screen('pause'); }
    else if(state.mode==='paused') { state.mode='playing';state.autoPaused=false;hideScreens();last=performance.now();canvas.focus(); }
    updatePauseA11y();
  }
  function endRun() {
    const wasBest=state.score>state.best;state.mode='gameover';state.best=Math.max(state.best,state.score);
    try { localStorage.setItem('orbit-breaker-best',state.best); } catch(e) {}
    updateHud();ui.final.textContent=String(Math.floor(state.score)).padStart(6,'0');$('runNote').textContent=wasBest?'New record.':'The wall holds—for now.';screen('gameover');sfx('lose');
  }
  function victory() {
    state.mode='victory';state.cleared=true;ui.victoryWave.textContent=String(state.wave).padStart(2,'0');screen('victory');sfx('level');
    for(let i=0;i<18;i++) burst(rand(FIELD.left+30,W-FIELD.left-30),rand(FIELD.brickTop-20,Math.min(H-120,FIELD.brickTop+420)),pick([COLORS.cyan,COLORS.pink,COLORS.yellow,COLORS.green]),18,220);
  }
  function updateHud() {
    const powerPriority=['plasma','magnet','overdrive','split'],powerCode={plasma:'P',magnet:'M',overdrive:'O',split:'S'};
    const activePowers=[];
    for(const mode of powerPriority){const until=balls.reduce((latest,b)=>Math.max(latest,b.powers?.[mode]||0),0);if(until>state.elapsed)activePowers.push(powerCode[mode]+Math.ceil(until-state.elapsed));}
    const values={score:String(Math.floor(state.score)).padStart(6,'0'),combo:'x'+state.multiplier,wave:String(state.wave).padStart(2,'0'),lives:'●'.repeat(Math.max(0,state.lives))+'○'.repeat(Math.max(0,3-state.lives)),best:String(Math.floor(state.best)).padStart(6,'0'),orbit:state.orbit.active?`L${state.orbit.level}`:'OFF',power:activePowers.join(' ')||'—'};
    for(const [key,value] of Object.entries(values)){if(ui[key].textContent!==value)ui[key].textContent=value;}
    ui.orbit.classList.toggle('orbit-live',state.orbit.active);ui.power.classList.toggle('power-live',activePowers.length>0);
  }
  function showToast(message,seconds=1.6) { ui.toast.textContent=message;ui.toast.classList.add('show');clearTimeout(toastTimer);toastTimer=setTimeout(()=>ui.toast.classList.remove('show'),seconds*1000); }
  function cap(list,max) { if(list.length>max) list.splice(0,list.length-max); }
  function burst(x,y,color,count=10,force=120) {
    count=Math.min(count,48);
    for(let i=0;i<count;i++){const a=rand(0,TAU),s=rand(force*.25,force),life=rand(.28,.8);particles.push({x,y,vx:Math.cos(a)*s,vy:Math.sin(a)*s,life,max:life,size:rand(1,4),color,drag:.92});}
    cap(particles,MAX_PARTICLES);
  }
  function ring(x,y,color,r=10) { rings.push({x,y,r,life:.45,max:.45,color});cap(rings,MAX_RINGS); }
  function floatText(x,y,text,color=COLORS.white,scale=1) { texts.push({x,y,text,color,life:1,max:1,scale,vy:-30});cap(texts,MAX_TEXTS); }
  function triggerOrbit(b) {
    if(state.orbit.active) return;
    state.orbit.active=true;state.orbit.level=1;state.orbit.chain=0;state.orbit.peak=1;state.orbit.pulse=1;state.shake=Math.max(state.shake,11);state.flash=Math.max(state.flash,.18);
    floatText(b.x,Math.max(72,b.y-28),'BREAKTHROUGH!',COLORS.cyan,1.55);showToast('BREAKTHROUGH  //  ORBIT CASCADE ONLINE',2.2);sfx('orbit');
    ring(b.x,Math.max(FIELD.brickTop, b.y),COLORS.cyan,18);burst(b.x,b.y,COLORS.cyan,34,270);burst(b.x,b.y,COLORS.pink,18,180);
  }
  function orbitBrickEvent(brick,b) {
    if(!state.orbit.active) return;
    const old=state.orbit.level;state.orbit.level=clamp(state.orbit.level+(brick.type==='normal'?1:2),1,8);state.orbit.peak=Math.max(state.orbit.peak,state.orbit.level);state.orbit.chain++;
    const frenzy=Math.min(4,Math.pow(1.22,state.orbit.level-1)), bonus=Math.round(55*state.multiplier*frenzy);state.score+=bonus;state.orbit.pulse=1;
    burst(brick.x+brick.w/2,brick.y+brick.h/2, state.orbit.level>=6?COLORS.yellow:COLORS.pink, Math.min(12+state.orbit.level*3,36),150+state.orbit.level*16);
    ring(brick.x+brick.w/2,brick.y+brick.h/2,COLORS.yellow,12+state.orbit.level*2);floatText(brick.x+brick.w/2,brick.y-17,`CASCADE +${bonus}`,COLORS.yellow,Math.min(1.45,1+state.orbit.level*.06));
    if(state.orbit.level!==old || state.elapsed-state.orbit.lastSound>.09){sfx('cascade');state.orbit.lastSound=state.elapsed;}
    if(state.orbit.level>=3 && state.orbit.level!==old) showToast(state.orbit.level>=7?'APEX FRENZY  //  BOUNDED POWER':'CASCADE LEVEL '+state.orbit.level+'  //  KEEP IT UP',.95);
  }

  function spawnDrop(brick) {
    if(drops.length>=MAX_DROPS || Math.random()>.14+Math.min(.08,state.wave*.01)) return;
    const mode=pick(['split','plasma','magnet','overdrive']);drops.push({x:brick.x+brick.w/2,y:brick.y+brick.h/2,vy:95,spin:0,mode,w:24,h:28,life:12,telegraph:2.5});sfx('drop');
  }
  // activeMode controls appearance only; each effect checks its own timer below.
  function syncPowerMode(b) {
    b.mode=rules.activeMode(b.powers,state.elapsed);
  }
  function activatePower(mode) {
    const names={split:'SPLIT ORB',plasma:'PLASMA CORE',magnet:'MAGNET ARC',overdrive:'OVERDRIVE'};
    const duration=rules.POWER_DURATIONS[mode] || 0;
    showToast(names[mode]+'  //  '+duration+'s',1.6);sfx('special');ring(paddle.x,paddle.y,COLORS.yellow,10);burst(paddle.x,paddle.y,COLORS.yellow,24,190);
    if(mode==='split') {
      const source=balls[0];
      if(source) for(const sign of [-1,1]) {
        const b=makeBall(source.x,source.y,source.vx+sign*170,source.vy*.85,'split');
        if(b){b.powers=rules.extendPower(b.powers,'split',state.elapsed,duration);syncPowerMode(b);}
      }
    } else balls.forEach(b=>{
      const wasActive=rules.powerActive(b.powers,mode,state.elapsed);
      b.powers=rules.extendPower(b.powers,mode,state.elapsed,duration);
      // A refresh extends the timer but must not multiply the velocity again.
      // At the exact expiry boundary powerActive is false, allowing a new boost.
      if(mode==='overdrive'&&!wasActive){b.vx*=1.17;b.vy*=1.17;limitSpeed(b);}
      syncPowerMode(b);
    });
  }
  function validBrickHit(brick,b) {
    const breaker=rules.powerActive(b.powers,'plasma',state.elapsed)?'plasma':b.mode;
    return rules.canBreakSpecial(brick.type,b.vx,b.vy,brick.open,breaker);
  }
  function limitSpeed(b) { const speed=Math.hypot(b.vx,b.vy);if(speed>MAX_SPEED){b.vx=b.vx/speed*MAX_SPEED;b.vy=b.vy/speed*MAX_SPEED;} }
  function reflectBall(b,nx,ny) { const v=physics.reflect(b.vx,b.vy,nx,ny);b.vx=v.vx;b.vy=v.vy;limitSpeed(b); }
  function brickHit(brick,b,nx,ny) {
    if(!brick.alive || brick.lastHitTick===state.physicsTick) return {destroyed:false,ignored:true};
    brick.lastHitTick=state.physicsTick;
    if(!validBrickHit(brick,b)) {
      brick.hitFlash=.35;state.shake=Math.max(state.shake,3);ring(b.x,b.y,brick.type==='phase'?COLORS.purple:COLORS.yellow,8);burst(b.x,b.y,brick.type==='phase'?COLORS.purple:COLORS.yellow,5,70);floatText(b.x,b.y-10,brick.type==='phase'?'PHASED':'NEED '+(brick.type==='angle'?'STEEP ANGLE':'SPEED'),COLORS.yellow,.65);sfx('hit');return {destroyed:false,blocked:true};
    }
    brick.alive=false;brick.hitFlash=.5;state.combo++;state.comboClock=2.5;state.multiplier=clamp(1+Math.floor(state.combo/4)+Math.max(0,state.wave-2),1,9);
    const base=100*(brick.type==='normal'?1:2),points=Math.round(base*state.multiplier*Math.pow(1.35,state.wave-1));state.score+=points;
    const col=brick.color;burst(brick.x+brick.w/2,brick.y+brick.h/2,col,brick.type==='normal'?12:22,brick.type==='normal'?130:220);ring(brick.x+brick.w/2,brick.y+brick.h/2,col,10);floatText(brick.x+brick.w/2,brick.y-5,'+'+points,col,state.multiplier>2?1.15:1);sfx('brick');
    if(brick.type!=='normal') showToast(brick.type==='angle'?'ANGLE BREAK  //  CLEAN':brick.type==='speed'?'SPEED BREAK  //  MOMENTUM':'PHASE BREAK  //  OPEN WINDOW',.7);
    spawnDrop(brick);state.shake=Math.max(state.shake,brick.type==='normal'?2:6);state.flash=Math.max(state.flash,brick.type==='normal'?.035:.1);orbitBrickEvent(brick,b);
    return {destroyed:true};
  }

  function paddleBounce(b) {
    const top=paddle.y-paddle.h/2, offset=clamp((b.x-paddle.x)/(paddle.w/2),-1,1), movingUp=paddle.vy < -25;
    const boost=movingUp?clamp(-paddle.vy*.58,0,250):0, speed=clamp(Math.hypot(b.vx,b.vy)+boost,330,MAX_SPEED), angle=offset*1.05-Math.PI/2;
    b.vx=Math.sin(angle)*speed+paddle.vx*.18;b.vy=-Math.cos(angle)*speed;b.y=top-b.r-1;b.age+=.08;limitSpeed(b);
    state.shake=Math.max(state.shake,movingUp?7:2);ring(b.x,top,COLORS.cyan,9);burst(b.x,top,movingUp?COLORS.yellow:COLORS.cyan,movingUp?16:6,movingUp?220:90);sfx('hit');
    if(movingUp){const points=25*state.multiplier;state.score+=points;floatText(b.x,top-18,'UPDRAFT +'+points,COLORS.yellow,.8);showToast('UPDRAFT  //  KINETIC BOOST',.8);}
  }
  function advancePaddle(dt) {
    const digitalX=(input.right?1:0)-(input.left?1:0),digitalY=(input.down?1:0)-(input.up?1:0),ax=digitalX||input.stickX,ay=digitalY||input.stickY;
    if(ax) paddle.targetX+=ax*520*dt;if(ay) paddle.targetY+=ay*310*dt;
    paddle.targetX=clamp(paddle.targetX,paddle.w/2+8,W-paddle.w/2-8);paddle.targetY=clamp(paddle.targetY,PADDLE_MIN_Y,PADDLE_MAX_Y);
    const oldX=paddle.x,oldY=paddle.y,follow=1-Math.pow(.0001,dt);paddle.x=lerp(paddle.x,paddle.targetX,follow);paddle.y=lerp(paddle.y,paddle.targetY,follow);
    paddle.vx=(paddle.x-oldX)/Math.max(dt,.0001);paddle.vy=(paddle.y-oldY)/Math.max(dt,.0001);paddle.tilt=lerp(paddle.tilt,clamp(paddle.vx/700,-.5,.5),dt*7);
  }
  function candidateCollision(b,sx,sy,dx,dy) {
    let best=null;
    const consider=(hit,kind,data)=>{if(hit&&hit.t>=-1e-6&&hit.t<=1.000001&&(!best||hit.t<best.t))best={...hit,kind,...data};};
    if(dx>0 && sx+dx+b.r>FIELD.right) consider({t:(FIELD.right-b.r-sx)/dx,nx:-1,ny:0},'wall',{});
    if(dx<0 && sx+dx-b.r<FIELD.left) consider({t:(FIELD.left+b.r-sx)/dx,nx:1,ny:0},'wall',{});
    if(dy<0 && sy+dy-b.r<FIELD.top) consider({t:(FIELD.top+b.r-sy)/dy,nx:0,ny:1},'wall',{});
    for(const brick of bricks) {
      if(!brick.alive || (b.skipTick===state.physicsTick&&b.skipBrickId===brick.id)) continue;
      consider(physics.sweepCircleAabb(sx,sy,dx,dy,b.r,brick),'brick',{brick});
    }
    if(b.vy>0) {
      const pRect={x:paddle.x-paddle.w/2,y:paddle.y-paddle.h/2,w:paddle.w,h:paddle.h};
      const hit=physics.sweepCircleAabb(sx,sy,dx,dy,b.r,pRect);
      if(hit&&hit.ny<0) consider(hit,'paddle',{});
    }
    return best;
  }
  function moveBall(b,dt) {
    let remaining=dt,guard=0;b.skipBrickId=-1;b.skipTick=-1;
    while(remaining>1e-7 && guard++<10 && b.alive) {
      syncPowerMode(b);
      if(rules.powerActive(b.powers,'magnet',state.elapsed)) { b.vx+=clamp((paddle.x-b.x)*.35,-90,90)*remaining;b.vy+=clamp((paddle.y-b.y)*.14,-35,35)*remaining;limitSpeed(b); }
      const sx=b.x,sy=b.y,dx=b.vx*remaining,dy=b.vy*remaining,event=candidateCollision(b,sx,sy,dx,dy);
      if(!event) { b.x+=dx;b.y+=dy;break; }
      const t=clamp(event.t,0,1);b.x=sx+dx*t;b.y=sy+dy*t;
      if(event.kind==='paddle') { b.x+=event.nx*.5;b.y+=event.ny*.5;paddleBounce(b);remaining*=1-t;continue; }
      if(event.kind==='wall') { b.x+=event.nx*.45;b.y+=event.ny*.45;reflectBall(b,event.nx,event.ny);remaining*=1-t;continue; }
      const brick=event.brick;b.x+=event.nx*.55;b.y+=event.ny*.55;
      const result=brickHit(brick,b,event.nx,event.ny);b.skipBrickId=brick.id;b.skipTick=state.physicsTick;
      if(result.destroyed) b.wallContact=true;
      if(result.destroyed && rules.powerActive(b.powers,'plasma',state.elapsed)) { remaining*=1-t;b.x+=event.nx*.5;continue; }
      reflectBall(b,event.nx,event.ny);remaining*=1-t;
    }
    if(b.y-b.r>H+18) b.alive=false;
    if(!state.orbit.active && rules.breakthroughReady(b,FIELD.brickTop)) triggerOrbit(b);
  }
  function loseBall() {
    state.lives--;state.combo=0;state.multiplier=1;resetOrbit();updateHud();burst(paddle.x,paddle.y,COLORS.pink,22,180);ring(paddle.x,paddle.y,COLORS.pink,12);sfx('lose');
    if(state.lives<=0){balls=[];endRun();return;}
    balls=[];makeBall(paddle.x,paddle.y-25,rand(-210,210),-500);showToast('ORBIT LOST  //  '+state.lives+' LIVES REMAIN',1.4);
  }
  function simulateStep(dt) {
    state.physicsTick++;
    for(const br of bricks) if(br.alive){br.open=br.type!=='phase'||Math.sin(state.elapsed*2.5+br.phase)>.18;br.hitFlash=Math.max(0,br.hitFlash-dt);}
    for(let i=balls.length-1;i>=0;i--) { const b=balls[i];b.age+=dt;b.spin+=dt*4;b.trail.push({x:b.x,y:b.y});if(b.trail.length>18)b.trail.shift();moveBall(b,dt);if(!b.alive)balls.splice(i,1); }
    for(let i=drops.length-1;i>=0;i--) {
      const d=drops[i];d.y+=d.vy*dt;d.spin+=dt*4;d.life-=dt;d.telegraph=Math.max(0,d.telegraph-dt);
      if(d.y>H+30||d.life<0){drops.splice(i,1);continue;}
      if(d.y+d.h/2>paddle.y-paddle.h/2&&d.y-d.h/2<paddle.y+paddle.h/2&&Math.abs(d.x-paddle.x)<paddle.w/2+d.w/2){activatePower(d.mode);drops.splice(i,1);}
    }
    // Clearing the wall is terminal and wins ties against a ball crossing the
    // drain in this same fixed step. This keeps victory deterministic at 0 lives.
    const cleared=bricks.length>0 && bricks.every(b=>!b.alive);
    const result=rules.outcome(cleared,balls.length,state.lives);
    if(result==='victory') victory();
    else if(result==='life-lost'||result==='gameover') loseBall();
  }
  function update(dt) {
    if(state.mode==='playing'||state.mode==='menu') state.elapsed+=dt;
    if(state.mode!=='playing'){updateFx(dt);return;}
    advancePaddle(dt);state.comboClock-=dt;if(state.comboClock<=0&&state.combo){state.combo=0;state.multiplier=1;showToast('COMBO RESET',.55);}
    accumulator=Math.min(accumulator+dt,.2);let steps=0;
    while(accumulator>=STEP&&steps<MAX_STEPS){simulateStep(STEP);accumulator-=STEP;steps++;if(state.mode!=='playing')break;}
    if(steps===MAX_STEPS) accumulator=0;
    updateFx(dt);updateHud();
  }
  function updateFx(dt) {
    state.shake=Math.max(0,state.shake-dt*18);state.flash=Math.max(0,state.flash-dt*.8);state.waveIntro=Math.max(0,state.waveIntro-dt);state.orbit.pulse=Math.max(0,state.orbit.pulse-dt*1.7);
    for(let i=particles.length-1;i>=0;i--){const p=particles[i];p.life-=dt;p.x+=p.vx*dt;p.y+=p.vy*dt;p.vx*=Math.pow(p.drag,dt*60);p.vy*=Math.pow(p.drag,dt*60);p.vy+=20*dt;if(p.life<=0)particles.splice(i,1);}
    for(let i=rings.length-1;i>=0;i--){rings[i].life-=dt;rings[i].r+=100*dt;if(rings[i].life<=0)rings.splice(i,1);}
    for(let i=texts.length-1;i>=0;i--){texts[i].life-=dt;texts[i].y+=texts[i].vy*dt;if(texts[i].life<=0)texts.splice(i,1);}
  }
  function rr(x,y,w,h,r) {
    ctx.beginPath();if(ctx.roundRect)ctx.roundRect(x,y,w,h,r);else{ctx.moveTo(x+r,y);ctx.lineTo(x+w-r,y);ctx.quadraticCurveTo(x+w,y,x+w,y+r);ctx.lineTo(x+w,y+h-r);ctx.quadraticCurveTo(x+w,y+h,x+w-r,y+h);ctx.lineTo(x+r,y+h);ctx.quadraticCurveTo(x,y+h,x,y+h-r);ctx.lineTo(x,y+r);ctx.quadraticCurveTo(x,y,x+r,y);ctx.closePath();}
  }
  function hexToRgb(hex){const n=parseInt(hex.slice(1),16);return `${n>>16},${n>>8&255},${n&255}`;}
  function drawBackground() {
    const g=ctx.createLinearGradient(0,0,0,H);g.addColorStop(0,'#0b1230');g.addColorStop(.55,'#090d20');g.addColorStop(1,'#070916');ctx.fillStyle=g;ctx.fillRect(0,0,W,H);
    const glow=ctx.createRadialGradient(W*.5,FIELD.brickTop,20,W*.5,FIELD.brickTop, PORTRAIT ? 360 : 510);glow.addColorStop(0,'#1e3d6640');glow.addColorStop(1,'#1e3d6600');ctx.fillStyle=glow;ctx.fillRect(0,0,W,H);
    for(const s of stars){const alpha=.18+s.z*.3+Math.sin(state.elapsed*1.5+s.tw)*.1;ctx.fillStyle=`rgba(142,206,255,${alpha})`;ctx.beginPath();ctx.arc(s.x,s.y,s.r*(.8+s.z*.3),0,TAU);ctx.fill();}
    ctx.strokeStyle='#25315a2f';ctx.lineWidth=1;for(let y=82;y<H;y+=PORTRAIT?48:40){ctx.beginPath();ctx.moveTo(20,y);ctx.lineTo(W-20,y);ctx.stroke();}for(let x=20;x<W;x+=40){ctx.beginPath();ctx.moveTo(x,80);ctx.lineTo(x,H);ctx.stroke();}
    const floorY=PORTRAIT ? H-270 : 500;const floor=ctx.createLinearGradient(0,floorY,0,H);floor.addColorStop(0,'#0d183011');floor.addColorStop(1,'#55e9ff0b');ctx.fillStyle=floor;ctx.fillRect(20,floorY,W-40,H-floorY);
    ctx.strokeStyle='#30406b';ctx.lineWidth=2;ctx.beginPath();ctx.moveTo(20,38);ctx.lineTo(W-20,38);ctx.moveTo(20,38);ctx.lineTo(20,H-16);ctx.moveTo(W-20,38);ctx.lineTo(W-20,H-16);ctx.stroke();
    ctx.fillStyle='#7280ad';ctx.font='700 10px Space Mono, monospace';ctx.fillText('TUNNEL ZONE',30,61);ctx.fillStyle='#43537e';ctx.fillText('KEEP THE ORBIT ALIVE',W-185,61);ctx.fillStyle='#4e608c';ctx.font='700 8px Space Mono, monospace';ctx.fillText('ANGLE // STEEP    SPEED // FAST    PHASE // OPEN WINDOWS',30,H-7);
  }
  function drawOrbitLayer() {
    if(!state.orbit.active) return;
    const level=state.orbit.level, pulse=state.orbit.pulse;
    const orbitY=PORTRAIT ? FIELD.brickTop-18 : 100;
    ctx.save();ctx.globalCompositeOperation='lighter';ctx.globalAlpha=.11+level*.018+pulse*.12;ctx.strokeStyle=level>=6?COLORS.yellow:COLORS.cyan;ctx.lineWidth=2+pulse*3;
    ctx.beginPath();ctx.arc(W/2,orbitY,Math.min(PORTRAIT?190:300,150+level*22+pulse*18),Math.PI*1.08,Math.PI*1.92);ctx.stroke();ctx.beginPath();ctx.arc(W/2,orbitY,Math.min(PORTRAIT?285:330,230+level*15+pulse*25),Math.PI*1.12,Math.PI*1.88);ctx.stroke();ctx.restore();
    ctx.save();ctx.textAlign='center';ctx.fillStyle=level>=6?COLORS.yellow:COLORS.cyan;ctx.globalAlpha=.7+pulse*.3;ctx.font='800 11px Space Mono, monospace';ctx.fillText(level>=7?'APEX FRENZY':`ORBIT CASCADE  //  LEVEL ${level}`,W/2,FIELD.brickTop-45);ctx.globalAlpha=.35;ctx.fillRect(W/2-90,FIELD.brickTop-39,180,2);ctx.globalAlpha=.8;ctx.fillStyle=level>=6?COLORS.yellow:COLORS.pink;ctx.fillRect(W/2-90,FIELD.brickTop-39,180*(level/8),2);ctx.restore();
  }
  function drawBricks() {
    for(const b of bricks){if(!b.alive)continue;const rgb=hexToRgb(b.color),pulse=b.type==='phase'&&b.open?Math.sin(state.elapsed*5+b.phase)*.12:0,alpha=b.type==='phase'&&!b.open?.3:1;
      ctx.save();ctx.globalAlpha=alpha;rr(b.x,b.y,b.w,b.h,5);ctx.fillStyle=`rgba(${rgb},${.12+pulse})`;ctx.fill();ctx.strokeStyle=b.hitFlash>0?COLORS.white:b.color;ctx.lineWidth=b.hitFlash>0?2:1;ctx.stroke();
      const shine=ctx.createLinearGradient(b.x,b.y,b.x,b.y+b.h);shine.addColorStop(0,`rgba(255,255,255,${.18+pulse})`);shine.addColorStop(.35,'rgba(255,255,255,0)');ctx.fillStyle=shine;rr(b.x+1,b.y+1,b.w-2,b.h-2,4);ctx.fill();ctx.fillStyle=b.color;ctx.globalAlpha=alpha*.9;ctx.fillRect(b.x+8,b.y+6,3,b.h-12);
      ctx.strokeStyle=b.color;ctx.globalAlpha=alpha*.9;ctx.lineWidth=1.5;ctx.beginPath();if(b.type==='angle'){ctx.moveTo(b.x+b.w-18,b.y+8);ctx.lineTo(b.x+b.w-10,b.y+15);ctx.lineTo(b.x+b.w-18,b.y+22);}else if(b.type==='speed'){ctx.moveTo(b.x+b.w-20,b.y+9);ctx.lineTo(b.x+b.w-13,b.y+14);ctx.lineTo(b.x+b.w-20,b.y+19);ctx.moveTo(b.x+b.w-14,b.y+9);ctx.lineTo(b.x+b.w-7,b.y+14);ctx.lineTo(b.x+b.w-14,b.y+19);}else if(b.type==='phase')ctx.arc(b.x+b.w-14,b.y+14,6,Math.PI*.25,Math.PI*1.75);ctx.stroke();ctx.restore();
    }
  }
  function drawPaddle() {
    ctx.save();ctx.translate(paddle.x,paddle.y);ctx.rotate(paddle.tilt*.08);ctx.shadowBlur=24;ctx.shadowColor=COLORS.cyan;rr(-paddle.w/2,-paddle.h/2,paddle.w,paddle.h,7);const g=ctx.createLinearGradient(0,-10,0,10);g.addColorStop(0,'#b8f9ff');g.addColorStop(.23,COLORS.cyan);g.addColorStop(1,'#157490');ctx.fillStyle=g;ctx.fill();ctx.shadowBlur=0;ctx.strokeStyle='#d9fdff';ctx.lineWidth=1;ctx.stroke();ctx.fillStyle='#e8ffff';rr(-paddle.w*.25,-3,paddle.w*.5,3,1);ctx.fill();ctx.fillStyle='#061526';ctx.font='700 8px Space Mono';ctx.textAlign='center';ctx.fillText('UPDRAFT',0,3);ctx.restore();
  }
  function ballColor(b){return b.mode==='plasma'?COLORS.pink:b.mode==='magnet'?COLORS.purple:b.mode==='overdrive'?COLORS.yellow:b.mode==='split'?COLORS.green:COLORS.cyan;}
  function drawBalls() {
    for(const b of balls){const col=ballColor(b);ctx.save();for(let i=0;i<b.trail.length;i++){const t=b.trail[i],a=(i/b.trail.length)*.22;ctx.fillStyle=col;ctx.globalAlpha=a;ctx.beginPath();ctx.arc(t.x,t.y,b.r*(i/b.trail.length)*.8,0,TAU);ctx.fill();}ctx.globalAlpha=1;ctx.shadowBlur=22;ctx.shadowColor=col;ctx.fillStyle='#f8ffff';ctx.beginPath();ctx.arc(b.x,b.y,b.r,0,TAU);ctx.fill();ctx.shadowBlur=0;ctx.strokeStyle=col;ctx.lineWidth=2;ctx.beginPath();ctx.arc(b.x,b.y,b.r+3+Math.sin(b.spin)*1.5,0,TAU);ctx.stroke();if(b.mode!=='core'){ctx.font='700 7px Space Mono';ctx.fillStyle=col;ctx.textAlign='center';ctx.fillText(b.mode==='plasma'?'P':b.mode==='magnet'?'M':b.mode==='overdrive'?'O':'S',b.x,b.y+2.5);}ctx.restore();}
  }
  function drawDrops() {
    const labels={split:'S',plasma:'P',magnet:'M',overdrive:'O'},cols={split:COLORS.green,plasma:COLORS.pink,magnet:COLORS.purple,overdrive:COLORS.yellow};
    for(const d of drops){const c=cols[d.mode];ctx.save();if(d.telegraph>0){ctx.globalAlpha=.12+Math.sin(state.elapsed*8)*.06;ctx.strokeStyle=c;ctx.setLineDash([3,6]);ctx.beginPath();ctx.moveTo(d.x,d.y);ctx.lineTo(d.x,paddle.y-20);ctx.stroke();ctx.setLineDash([]);}ctx.globalAlpha=1;ctx.translate(d.x,d.y);ctx.rotate(Math.sin(d.spin)*.1);ctx.shadowBlur=18;ctx.shadowColor=c;ctx.fillStyle='#101a38';ctx.strokeStyle=c;ctx.lineWidth=2;rr(-d.w/2,-d.h/2,d.w,d.h,5);ctx.fill();ctx.stroke();ctx.shadowBlur=0;ctx.fillStyle=c;ctx.font='800 12px Space Mono';ctx.textAlign='center';ctx.fillText(labels[d.mode],0,4);ctx.restore();}
  }
  function drawFx() {
    ctx.save();ctx.globalCompositeOperation='lighter';for(const r of rings){ctx.globalAlpha=Math.max(0,r.life/r.max);ctx.strokeStyle=r.color;ctx.lineWidth=2;ctx.beginPath();ctx.arc(r.x,r.y,r.r,0,TAU);ctx.stroke();}for(const p of particles){ctx.globalAlpha=Math.max(0,p.life/p.max);ctx.fillStyle=p.color;ctx.fillRect(p.x,p.y,p.size,p.size);}ctx.restore();
    for(const t of texts){ctx.save();ctx.globalAlpha=Math.min(1,t.life*2);ctx.fillStyle=t.color;ctx.font=`800 ${Math.round(11*t.scale)}px Space Mono, monospace`;ctx.textAlign='center';ctx.shadowBlur=8;ctx.shadowColor=t.color;ctx.fillText(t.text,t.x,t.y);ctx.restore();}
  }
  function render() {
    ctx.setTransform(canvas.width/W,0,0,canvas.height/H,0,0);ctx.clearRect(0,0,W,H);drawBackground();
    ctx.save();if(state.shake&&!state.reduced)ctx.translate(rand(-state.shake,state.shake),rand(-state.shake,state.shake));drawOrbitLayer();drawBricks();drawDrops();drawPaddle();drawBalls();drawFx();ctx.restore();
    if(state.waveIntro>0&&state.mode==='playing'&&!state.orbit.active){ctx.save();ctx.globalAlpha=clamp(state.waveIntro/.7,0,1);ctx.textAlign='center';ctx.fillStyle=COLORS.cyan;ctx.font='700 12px Space Mono';ctx.fillText(`WAVE ${String(state.wave).padStart(2,'0')}  //  BREAK ABOVE TO ACTIVATE ORBIT`,W/2,84);ctx.restore();}
    if(state.flash&&!state.reduced){ctx.fillStyle=`rgba(125,230,255,${state.flash})`;ctx.fillRect(0,0,W,H);}
  }
  function loop(now){const dt=Math.min(.05,(now-last)/1000||.016);last=now;update(dt);render();requestAnimationFrame(loop);}
  requestAnimationFrame(loop);

  function pointFromEvent(e){const r=canvas.getBoundingClientRect();return {x:clamp((e.clientX-r.left)/Math.max(1,r.width)*W,0,W),y:clamp((e.clientY-r.top)/Math.max(1,r.height)*H,0,H)};}
  function setPointer(e){const p=pointFromEvent(e);input.x=p.x;input.y=p.y;input.pointerId=e.pointerId;paddle.targetX=p.x;paddle.targetY=clamp(p.y,PADDLE_MIN_Y,PADDLE_MAX_Y);}
  canvas.addEventListener('pointerdown',e=>{initAudio();canvas.setPointerCapture?.(e.pointerId);setPointer(e);});
  canvas.addEventListener('pointermove',e=>{if(e.pointerType==='mouse'||e.pointerId===input.pointerId||e.buttons||e.pressure)setPointer(e);});
  canvas.addEventListener('pointerup',e=>{if(e.pointerId===input.pointerId)input.pointerId=null;});
  canvas.addEventListener('pointercancel',e=>{if(e.pointerId===input.pointerId)input.pointerId=null;});
  addEventListener('keydown',e=>{
    const k=e.key.toLowerCase();if(['arrowleft','a'].includes(k))input.left=true;if(['arrowright','d'].includes(k))input.right=true;if(['arrowup','w'].includes(k))input.up=true;if(['arrowdown','s'].includes(k))input.down=true;
    if(['arrowleft','arrowright','arrowup','arrowdown',' '].includes(k)||['a','d','w','s'].includes(k))e.preventDefault();
    if(e.key===' '||k==='p'){initAudio();if(state.mode==='playing'||state.mode==='paused')togglePause();}
    if(e.key==='Enter'&&state.mode==='menu'){initAudio();newRun();}
  });
  addEventListener('keyup',e=>{const k=e.key.toLowerCase();if(['arrowleft','a'].includes(k))input.left=false;if(['arrowright','d'].includes(k))input.right=false;if(['arrowup','w'].includes(k))input.up=false;if(['arrowdown','s'].includes(k))input.down=false;});
  function bindStart(id,fn){$(id).addEventListener('click',()=>{initAudio();fn();});}
  bindStart('startBtn',newRun);bindStart('againBtn',newRun);bindStart('nextBtn',nextWave);bindStart('resumeBtn',togglePause);bindStart('restartBtn',newRun);
  $('menuBtn').addEventListener('click',()=>{state.mode='menu';state.autoPaused=false;generateBricks();balls=[];resetOrbit();updatePauseA11y();screen('menu');updateHud();});
  $('pauseBtn').addEventListener('click',()=>{initAudio();if(state.mode==='playing'||state.mode==='paused')togglePause();});$('touchPause').addEventListener('click',()=>{if(state.mode==='playing'||state.mode==='paused')togglePause();});
  ui.mute.addEventListener('click',()=>setMuted(!state.muted));
  const stick=$('stick'),knob=$('knob');let stickId=null;
  function stickPoint(e){const r=stick.getBoundingClientRect(),cx=r.left+r.width/2,cy=r.top+r.height/2,dx=e.clientX-cx,dy=e.clientY-cy,max=r.width*.34,len=Math.hypot(dx,dy),m=Math.min(1,len/Math.max(1,max));input.stickX=dx/Math.max(1,len)*m;input.stickY=dy/Math.max(1,len)*m;knob.style.transform=`translate(${input.stickX*max}px,${input.stickY*max}px)`;}
  stick.addEventListener('pointerdown',e=>{initAudio();stickId=e.pointerId;stick.setPointerCapture?.(stickId);stickPoint(e);});stick.addEventListener('pointermove',e=>{if(e.pointerId===stickId)stickPoint(e);});
  function clearStick(){stickId=null;input.stickX=0;input.stickY=0;knob.style.transform='translate(0,0)';}stick.addEventListener('pointerup',clearStick);stick.addEventListener('pointercancel',clearStick);stick.addEventListener('lostpointercapture',clearStick);
  document.addEventListener('visibilitychange',()=>{if(document.hidden){if(state.mode==='playing')togglePause(true);last=performance.now();}else{last=performance.now();}});
  generateBricks();updateHud();setMuted(state.muted,false);updatePauseA11y();
})();
