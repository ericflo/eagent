'use strict';
// Main game: loop, physics, collisions, frenzy, level flow.
var canvas, ctx;
var VW=0, VH=0;            // view size in CSS px
var scale=1, offX=0, offY=0;

function resize(){
  var dpr=Math.min(window.devicePixelRatio||1,2);
  VW=window.innerWidth; VH=window.innerHeight;
  canvas.width=Math.round(VW*dpr); canvas.height=Math.round(VH*dpr);
  canvas.style.width=VW+'px'; canvas.style.height=VH+'px';
  // letterbox-fit virtual CFG.W x CFG.H
  scale=Math.min(VW/CFG.W, VH/CFG.H);
  offX=(VW-CFG.W*scale)/2; offY=(VH-CFG.H*scale)/2;
  if(G.input){ G.input.sx=scale; G.input.sy=scale; G.input.offX=offX; G.input.offY=offY; }
}
function setup(){
  canvas=document.getElementById('game');
  ctx=canvas.getContext('2d');
  G.input=new Input(canvas);
  G.audio=new AudioSys();
  G.bg=new Background();
  G.particles=new ParticleSys();
  G.paddle=new Paddle();
  resize();
  window.addEventListener('resize',resize);
  window.addEventListener('orientationchange',function(){ setTimeout(resize,120); });
  // audio unlock + begin on first gesture
  function unlock(){ G.audio.init(); }
  window.addEventListener('pointerdown',unlock,{once:false});
  window.addEventListener('keydown',unlock,{once:false});
  G.input.tapCb=onTap;
  G.input.pauseCb=onPauseKey;
  var boot=document.getElementById('boot');
  boot.addEventListener('pointerdown',function(){ unlock(); boot.style.display='none'; });
  requestAnimationFrame(loop);
}
function onTap(){
  if(G.state==='playing'||G.state==='launch'||G.state==='paused'){
    var btn=G.input.buttonHit;
    if(btn==='mute'){ G.muted=!G.muted; G.audio.setMuted(G.muted); G.input.buttonHit=null; return; }
    if(btn==='pause'){ G.input.buttonHit=null; onPauseKey(); return; }
  }
  if(G.state==='title'){ G.state='legend'; G.legendShown=true; }
  else if(G.state==='legend'){ startGame(); }
  else if(G.state==='launch'){ launchBalls(); }
  else if(G.state==='playing'){ launchBalls(); }  // release a sticky-attached ball
  else if(G.state==='paused'){ G.state= G.statePrev||'playing'; }
  else if(G.state==='levelclear' && G.clearT>0.8){ nextLevel(); }
  else if(G.state==='gameover'){ toTitle(); }
}
// tap on mute/pause icons handled here: check overlay buttons in virtual coords
function checkButtons(x,y){
  if(x>CFG.W-64&&x<CFG.W-16&&y>CFG.H-64&&y<CFG.H-16) return 'pause';
  if(x>CFG.W-64-58&&x<CFG.W-58&&y>CFG.H-64&&y<CFG.H-16) return 'mute';
  return null;
}
function onPauseKey(){
  if(G.state==='playing'||G.state==='launch'){ G.statePrev=G.state; G.state='paused'; }
  else if(G.state==='paused'){ G.state=G.statePrev||'playing'; }
}
function startGame(){
  G.score=0; G.lives=CFG.lives; G.level=0; G.mult=1; G.combo=0; G.topTime=0; G.frenzy=0;
  G.pw={expand:0,sticky:0,magnet:0}; G.slowmo=0;
  loadLevel();
}
function loadLevel(){
  G.bricks=parseLevel(G.level);
  G.bricksLeft=countBreakable(G.bricks);
  G.pickups=[]; G.lasers=[]; G.balls=[]; G.popups=[];
  G.particles.clear();
  G.mult=1; G.combo=0; G.topTime=0; G.frenzy=0;
  resetPaddleAndBall();
  G.state='launch'; G.levelIntroT=0;
}
function nextLevel(){
  G.level++;
  if(G.level>=LEVELS.length){ G.level=0; G.lives++; addPopup(CFG.W/2,CFG.H/2,'ALL LEVELS CLEARED! +1 ♥','#7dff8a',true); }
  loadLevel();
}
function resetPaddleAndBall(){
  G.paddle.x=CFG.W/2; G.paddle.y=CFG.H*0.9;
  G.balls=[new Ball(CFG.W/2,G.paddle.y-20,0,0,'normal')];
  G.balls[0].stuck=true;
}
function launchBalls(){
  var launched=false;
  for(var i=0;i<G.balls.length;i++){
    var b=G.balls[i];
    if(b.stuck){
      // aim: launch direction based on paddle position relative to center, slight angle
      var dx=(b.x-CFG.W/2)/(CFG.W/2);
      var ang=(-80+dx*50)*Math.PI/180;
      var spd=CFG.baseBallSpeed*(1+G.level*0.04);
      b.vx=Math.cos(ang)*spd; b.vy=Math.sin(ang)*spd;
      b.stuck=false; launched=true;
      G.audio.paddle(false);
    }
  }
  if(launched) G.state='playing';
}
function toTitle(){ G.state='title'; }

// ---------- FRENZY logic ----------
function brickFieldTop(){
  var min=CFG.H;
  for(var i=0;i<G.bricks.length;i++)
    if(G.bricks[i].alive) min=Math.min(min,G.bricks[i].y);
  return min;
}
function updateFrenzy(dt){
  var top=brickFieldTop();
  var anyAbove=false;
  for(var i=0;i<G.balls.length;i++){
    if(!G.balls[i].stuck && G.balls[i].y<top-CFG.ballR) anyAbove=true;
  }
  if(anyAbove && G.state==='playing'){
    G.topTime+=dt;
    if(G.topTime>=CFG.frenzyThreshold && G.frenzy<1){
      G.frenzy=Math.min(1,(G.topTime-CFG.frenzyThreshold)/2);
    }
    if(G.frenzy>=1 && !G.wasFrenzy){
      G.wasFrenzy=true; G.audio.frenzy(); G.flash=0.6;
      addPopup(CFG.W/2,CFG.H*0.3,'OVERDRIVE!','#ff5a3d',true);
      G.shake=Math.max(G.shake,10);
    }
    G.mult += CFG.frenzyRamp*dt*(1+G.frenzy*2.2);
    if(Math.random()<dt*8*G.frenzy)
      G.particles.burst(rand(0,CFG.W),rand(CFG.topUI,brickFieldTop()-40),2,'#ffd45a',180,0.5,3);
  } else {
    G.topTime=Math.max(0,G.topTime-dt*2);
    if(G.frenzy>0) G.frenzy=Math.max(0,G.frenzy-dt*1.5);
    if(G.frenzy===0) G.wasFrenzy=false;
    if(G.mult>1) G.mult=Math.max(1,G.mult-CFG.comboDecay*dt);
  }
  G.audio.update(dt, clamp(G.frenzy*0.8+clamp((G.mult-1)/12,0,0.5)+G.topTime*0.05,0,1));
}
// ---------- brick breaking ----------
function breakBrick(b,ball,cause){
  b.alive=false;
  G.combo++;
  var base=100;
  var gain=Math.round(base*G.mult*(1+G.combo*0.05));
  G.score+=gain;
  G.mult=Math.min(50,G.mult+0.08);
  addPopup(b.cx(),b.cy(),'+'+gain, BRICK_TYPES[b.type].col);
  G.particles.debris(b.cx(),b.cy(),b.w,b.h,BRICK_TYPES[b.type].col);
  G.audio.brick(b.row,G.combo);
  G.hitStop=0.03; G.shake=Math.max(G.shake,4+G.frenzy*4);
  if(b.type==='boom') explodeAt(b);
  // drops
  if(b.power){ spawnPickup(b.cx(),b.cy(),b.power); b.power=null; }
  else if(Math.random()<0.13) spawnPickup(b.cx(),b.cy(), randomPickupKind());
  else if(Math.random()<0.045) spawnPickup(b.cx(),b.cy(), randomBallKind(), true);
  G.bricksLeft=countBreakable(G.bricks);
  if(G.bricksLeft===0){ levelCleared(); }
}
function levelCleared(){
  G.state='levelclear'; G.clearT=0;
  G.audio.levelclear(); G.flash=0.5;
  if(G.score>G.best){ G.best=G.score; localStorage.setItem('ob_best',G.best); }
  for(var i=0;i<40;i++)
    G.particles.burst(rand(0,CFG.W),rand(0,CFG.H),2,'#ffd45a',300,1,3);
}
function explodeAt(b){
  G.audio.explode(); G.shake=Math.max(G.shake,12); G.flash=Math.max(G.flash,0.4);
  var R=110;
  G.particles.burst(b.cx(),b.cy(),30,'#ff6a4d',420,0.7,5);
  for(var i=0;i<G.bricks.length;i++){
    var o=G.bricks[i];
    if(o===b||!o.alive) continue;
    var d=Math.hypot(o.cx()-b.cx(),o.cy()-b.cy());
    if(d<R && o.type!=='core'){
      o.alive=false;
      if(o.type==='boom'&&!o._det){ o._det=true; setTimeout(function(){ if(G.state==='playing') explodeAt(o); },60); }
      G.score+=Math.round(80*G.mult);
      addPopup(o.cx(),o.cy(),'+'+Math.round(80*G.mult),BRICK_TYPES[o.type].col);
      G.particles.debris(o.cx(),o.cy(),o.w,o.h,BRICK_TYPES[o.type].col);
      if(o.power) spawnPickup(o.cx(),o.cy(),o.power);
    }
  }
  G.bricksLeft=countBreakable(G.bricks);
  if(G.bricksLeft===0) levelCleared();
}
function randomPickupKind(){
  var kinds=['expand','slow','multi','life','magnet'];
  return kinds[Math.floor(Math.random()*kinds.length)];
}
function randomBallKind(){
  var kinds=['fire','split','giant','laser','sticky'];
  return kinds[Math.floor(Math.random()*kinds.length)];
}
function spawnPickup(x,y,kind,isBall){
  G.pickups.push(new Pickup(x,y,kind,isBall));
}
function applyPickup(p){
  G.audio.pickup();
  addPopup(p.x,p.y-20,PICKUPS[p.kind].label,PICKUPS[p.kind].col,true);
  switch(p.kind){
    case 'expand': G.pw.expand=10; break;
    case 'slow': G.slowmo=4; break;
    case 'multi': multiball(); break;
    case 'life': G.lives++; break;
    case 'magnet': G.pw.magnet=8; break;
  }
}
function applyBallDrop(p){
  G.audio.powerball();
  addPopup(p.x,p.y,BALL_DROPS[p.kind]+'!',BALL_COLORS[p.kind],true);
  var b=G.balls[0]; if(!b) return;
  if(p.kind==='split'){ multiball(); return; }
  if(p.kind==='giant'){ b.kind='normal'; b.giant=true; b.r=CFG.ballR*2.1; b.fireT=10; }
  else { b.kind=p.kind; b.giant=false; b.r=CFG.ballR; b.fireT=10; }
}
function multiball(){
  var src=G.balls[0]; if(!src) return;
  for(var i=0;i<2;i++){
    var nb=new Ball(src.x,src.y,0,0,'normal');
    var a=Math.atan2(src.vy,src.vx)+(i?0.5:-0.5);
    var s=src.speed();
    if(s<1){ s=CFG.baseBallSpeed; a=(i?0.5:-0.5)+Math.PI/2; }
    nb.vx=Math.cos(a)*s; nb.vy=Math.sin(a)*s;
    G.balls.push(nb);
  }
}
// ---------- laser ball firing ----------
function updateLasers(dt){
  G.laserCd-=dt;
  for(var i=0;i<G.balls.length;i++){
    var b=G.balls[i];
    if(b.kind==='laser'&&!b.stuck&&G.laserCd<=0){
      G.laserCd=0.5;
      G.lasers.push({x:b.x,y:b.y-10,vy:-900});
      G.audio.clink();
    }
  }
  for(var j=G.lasers.length-1;j>=0;j--){
    var l=G.lasers[j]; l.y+=l.vy*dt;
    if(l.y<CFG.topUI){ G.lasers.splice(j,1); continue; }
    for(var k=0;k<G.bricks.length;k++){
      var br=G.bricks[k];
      if(!br.alive||!br.solid) continue;
      if(l.x>=br.x&&l.x<=br.x+br.w&&l.y>=br.y&&l.y<=br.y+br.h){
        if(br.type==='core'){ br.coreState=1; br.coreT=1.4; }
        else breakBrick(br,null,'laser');
        G.particles.spark(l.x,l.y,'#ff5ad1');
        G.lasers.splice(j,1);
        break;
      }
    }
  }
}
function drawLasers(ctx){
  ctx.save(); ctx.fillStyle='#ff5ad1'; ctx.shadowColor='#ff5ad1'; ctx.shadowBlur=8;
  for(var i=0;i<G.lasers.length;i++){ ctx.fillRect(G.lasers[i].x-1.5,G.lasers[i].y-14,3,16); }
  ctx.restore();
}
// ---------- ball physics + collisions ----------
function physics(dt){
  var p=G.paddle;
  updateLasers(dt);
  for(var i=G.balls.length-1;i>=0;i--){
    var b=G.balls[i];
    if(b.stuck){
      b.x=p.x; b.y=p.y-p.h/2-b.r-2;
      continue;
    }
    b.update(dt);
    // walls
    if(b.x<b.r){ b.x=b.r; b.vx=Math.abs(b.vx); G.audio.wall(); G.particles.spark(b.x,b.y,'#7fd0ff'); }
    if(b.x>CFG.W-b.r){ b.x=CFG.W-b.r; b.vx=-Math.abs(b.vx); G.audio.wall(); G.particles.spark(b.x,b.y,'#7fd0ff'); }
    if(b.y<CFG.topUI+b.r){ b.y=CFG.topUI+b.r; b.vy=Math.abs(b.vy); G.audio.wall(); G.particles.spark(b.x,b.y,'#7fd0ff'); }
    // paddle collision
    if(b.vy>0 && b.y+b.r>=p.y-p.h/2 && b.y-b.r<p.y+p.h/2 && Math.abs(b.x-p.x)<p.w/2+b.r){
      var rel=clamp((b.x-p.x)/(p.w/2),-1,1);
      var ang=(-90+rel*62)*Math.PI/180;
      var movingUp=p.vy<-100;
      var spd=b.speed()+ (movingUp? clamp(-p.vy,0,900)*0.55 : (p.vy>100? -60:0));
      spd=clamp(spd,CFG.baseBallSpeed,CFG.maxBallSpeed);
      var smash=movingUp&&clamp(-p.vy/600,0,1)>0.25;
      b.vx=Math.cos(ang)*spd; b.vy=Math.sin(ang)*spd;
      b.y=p.y-p.h/2-b.r-1;
      p.smash=smash?1:0;
      G.audio.paddle(smash);
      G.particles.ripple(b.x,p.y-p.h/2, smash?'#ffe08a':'#7fd0ff');
      if(smash){ G.shake=Math.max(G.shake,5); addPopup(b.x,b.y-30,'SMASH!','#ffe08a'); }
      if(b.kind==='sticky'){ b.stuck=true; }
      continue;
    }
    // brick collisions
    for(var k=0;k<G.bricks.length;k++){
      var br=G.bricks[k];
      var hit=br.collide(b.x,b.y,b.r);
      if(!hit) continue;
      var sp=b.speed();
      // fireball plows through std/move bricks
      if(b.kind==='fire'&&(br.type==='std'||br.type==='move')){
        breakBrick(br,b);
        G.particles.spark(b.x,b.y,'#ff8a3d');
        continue;
      }
      // decide break vs deflect
      if(br.canBreak(b,sp)){
        breakBrick(br,b);
        // reflect
        reflectBall(b,br);
      } else {
        reflectBall(b,br);
        G.audio.clink();
        G.particles.spark(hit.nx,hit.ny,'#ffd45a');
        var rsn=br.deflectReason();
        if(rsn) addPopup(hit.nx,hit.ny,rsn,'#ffd45a');
        if(br.type==='core'&&!b.isPower()&&b.kind!=='fire'){ br.coreState=1; br.coreT=1.4; }
        br.hitA=1;
      }
      break; // one brick per frame per ball
    }
    // fell below
    if(b.y>CFG.H+30){
      G.balls.splice(i,1);
      if(G.balls.length===0) loseLife();
    }
  }
}
function reflectBall(b,br){
  // reflect based on closest-point side
  var cx=clamp(b.x,br.x,br.x+br.w), cy=clamp(b.y,br.y,br.y+br.h);
  var dx=b.x-cx, dy=b.y-cy;
  if(Math.abs(dx)>Math.abs(dy)){ b.vx=Math.abs(b.vx)*(dx>0?1:-1); b.x+= (dx>=0?2:-2); }
  else { b.vy=Math.abs(b.vy)*(dy>=0?1:-1); b.y+=(dy>=0?2:-2); }
  if(br.type==='angle'){ // wedges deflect downward-ish from steep hits: add a nudge
    if(dy===0&&Math.abs(dx)<0.001) b.vy=Math.abs(b.vy);
  }
  G.particles.spark(cx,cy,BRICK_TYPES[br.type].col);
}
function loseLife(){
  G.lives--;
  G.audio.loseBall(); G.shake=Math.max(G.shake,8); G.flash=0.5;
  G.mult=1; G.combo=0; G.topTime=0; G.frenzy=0; G.wasFrenzy=false;
  G.pickups=[];
  if(G.lives<=0){
    G.state='gameover';
    if(G.score>G.best){ G.best=G.score; localStorage.setItem('ob_best',G.best); }
    G.audio.gameover();
  } else {
    resetPaddleAndBall();
    G.state='launch';
  }
}
function handlePickups(dt){
  var p=G.paddle;
  for(var i=G.pickups.length-1;i>=0;i--){
    var pk=G.pickups[i];
    pk.update(dt);
    if(pk.y>p.y-p.h/2-pk.h/2 && pk.y<p.y+30 && Math.abs(pk.x-p.x)<p.w/2+pk.w/2){
      if(pk.ball) applyBallDrop(pk); else applyPickup(pk);
      G.particles.burst(pk.x,pk.y,14,pk.col,260,0.5,3);
      G.pickups.splice(i,1); continue;
    }
    if(pk.y>CFG.H+40) G.pickups.splice(i,1);
  }
  // timers
  for(var k in G.pw) if(G.pw[k]>0) G.pw[k]=Math.max(0,G.pw[k]-dt);
  if(G.slowmo>0) G.slowmo-=dt;
}
// ---------- main loop (fixed timestep + accumulator) ----------
var last=0, acc=0;
var STEP=1/120;
function loop(ts){
  requestAnimationFrame(loop);
  G.input.updateGamepad();
  if(G.input.gpPressed&&!G.input._gpTapPrev) onTap();
  G.input._gpTapPrev=G.input.gpPressed;
  if(!last) last=ts;
  var dtRaw=Math.min((ts-last)/1000, 0.1);
  last=ts;
  var scaleT = G.slowmo>0? 0.45 : 1;
  if(G.hitStop>0){ G.hitStop-=dtRaw; dtRaw=0; }
  var dt=dtRaw*scaleT;
  acc+=dt;
  var steps=0;
  while(acc>=STEP && steps<6){
    if(G.state==='playing'||G.state==='launch'){
      G.time+=STEP;
      G.paddle.update(STEP);
      physics(STEP);
      for(var i=0;i<G.bricks.length;i++) G.bricks[i].update(STEP);
      handlePickups(STEP);
      G.particles.update(STEP);
      G.bg.update(STEP);
      updateFrenzy(STEP);
      updatePopups(STEP);
      if(G.levelIntroT<1.6) G.levelIntroT+=STEP;
    } else {
      G.time+=STEP;
      G.bg.update(STEP);
      G.particles.update(STEP);
      updatePopups(STEP);
      if(G.state==='levelclear') G.clearT+=STEP;
      G.audio.update(STEP, G.state==='gameover'?0.1:0.2);
    }
    acc-=STEP; steps++;
  }
  render(dtRaw);
}
function render(dt){
  ctx.setTransform(1,0,0,1,0,0);
  ctx.clearRect(0,0,canvas.width,canvas.height);
  ctx.save();
  ctx.translate(offX,offY); ctx.scale(scale,scale);
  // screen shake
  if(G.shake>0){
    G.shake=Math.max(0,G.shake-dt*30);
    var sx=rand(-G.shake,G.shake), sy=rand(-G.shake,G.shake);
    ctx.translate(sx,sy);
  }
  G.bg.draw(ctx);
  // arena side walls glow with frenzy
  if(G.frenzy>0){
    ctx.save(); ctx.globalAlpha=G.frenzy*0.6;
    var g=ctx.createLinearGradient(0,0,40,0); g.addColorStop(0,'#ff5a3d'); g.addColorStop(1,'rgba(255,90,61,0)');
    ctx.fillStyle=g; ctx.fillRect(0,0,40,CFG.H);
    g=ctx.createLinearGradient(CFG.W,0,CFG.W-40,0); g.addColorStop(0,'#ff5a3d'); g.addColorStop(1,'rgba(255,90,61,0)');
    ctx.fillStyle=g; ctx.fillRect(CFG.W-40,0,40,CFG.H);
    ctx.globalAlpha=1;
    ctx.strokeStyle='rgba(255,90,61,'+ (0.3+G.frenzy*0.5) +')'; ctx.lineWidth=3+G.frenzy*3;
    ctx.strokeRect(1,CFG.topUI,CFG.W-2,CFG.H-CFG.topUI-2);
    ctx.restore();
  }
  // top boundary line of playfield
  ctx.strokeStyle='rgba(120,160,220,0.25)'; ctx.lineWidth=1;
  ctx.strokeRect(0.5,CFG.topUI+0.5,CFG.W-1,CFG.H-CFG.topUI-1);
  if(G.state==='title'){ drawTitle(ctx); ctx.restore(); return; }
  if(G.state==='legend'){ drawLegend(ctx); ctx.restore(); return; }
  // game entities
  for(var i=0;i<G.bricks.length;i++) G.bricks[i].draw(ctx);
  G.paddle.draw(ctx);
  drawLasers(ctx);
  for(i=0;i<G.pickups.length;i++) G.pickups[i].draw(ctx);
  for(i=0;i<G.balls.length;i++) G.balls[i].draw(ctx);
  G.particles.draw(ctx);
  drawPopups(ctx);
  drawHUD(ctx);
  drawMuteIcon(ctx);
  if(G.levelIntroT<1.6&&(G.state==='launch')) drawLevelIntro(ctx);
  if(G.state==='paused') drawPause(ctx);
  if(G.state==='levelclear') drawLevelClear(ctx);
  if(G.state==='gameover') drawGameOver(ctx);
  // flash pulse
  if(G.flash>0){
    G.flash=Math.max(0,G.flash-dt*2.5);
    ctx.globalAlpha=G.flash*0.5; ctx.fillStyle='#fff';
    ctx.fillRect(0,0,CFG.W,CFG.H); ctx.globalAlpha=1;
  }
  ctx.restore();
}
setup();