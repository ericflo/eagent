'use strict';
// UI overlays: title, legend/tutorial, HUD, pause, level clear, game over.
function drawHUD(ctx){
  var m=18;
  ctx.save();
  ctx.textAlign='left'; ctx.fillStyle='#cfe4ff';
  ctx.font='bold 26px system-ui,sans-serif';
  ctx.fillText(fmt(G.score), m, 44);
  ctx.font='13px system-ui'; ctx.fillStyle='#6f86ad';
  ctx.fillText('BEST '+fmt(G.best), m, 64);
  // lives
  ctx.textAlign='right';
  ctx.font='bold 22px system-ui'; ctx.fillStyle='#ff6a8a';
  var lv=''; for(var i=0;i<Math.min(G.lives,5);i++) lv+='♥';
  ctx.fillText(lv, CFG.W-m, 44);
  ctx.font='12px system-ui'; ctx.fillStyle='#6f86ad';
  ctx.fillText('LVL '+(G.level+1)+' · '+LEVELS[G.level].name, CFG.W-m, 64);
  // multiplier center
  ctx.textAlign='center';
  var mt = G.mult>1.5;
  ctx.font='bold '+(24+G.frenzy*14)+'px system-ui';
  ctx.fillStyle = mt? mixCol('#ffd45a','#ff5a3d',clamp((G.mult-2)/10,0,1)) : '#5f7396';
  if(mt){ ctx.shadowColor=ctx.fillStyle; ctx.shadowBlur=10+G.frenzy*20; }
  ctx.fillText('×'+(Math.round(G.mult*10)/10), CFG.W/2, 44);
  ctx.shadowBlur=0;
  // frenzy meter
  var mw=180, mx=CFG.W/2-mw/2, my=54;
  ctx.fillStyle='rgba(255,255,255,0.1)'; roundRect(ctx,mx,my,mw,8,4); ctx.fill();
  var fv=clamp(G.topTime/CFG.frenzyThreshold,0,1);
  var fcol= G.frenzy>0? mixCol('#ffd45a','#ff3d6e',G.frenzy) : '#ffd45a';
  ctx.fillStyle=fcol; if(G.frenzy>0){ctx.shadowColor=fcol;ctx.shadowBlur=12;}
  roundRect(ctx,mx,my,mw*clamp(G.frenzy,0,1),8,4); ctx.fill(); ctx.shadowBlur=0;
  if(G.frenzy>0.05){
    ctx.font='bold 13px system-ui'; ctx.fillStyle=fcol;
    ctx.fillText(G.frenzy>=1?'▲ OVERDRIVE ▲':'RAMPING', CFG.W/2, 76);
  }
  // active power-up timers
  var px=m, py=88;
  ctx.textAlign='left'; ctx.font='bold 13px system-ui';
  var icons=[['expand','WIDE',G.pw.expand],['magnet','MAG',G.pw.magnet],['sticky','STICKY',G.pw.sticky],['slow','SLOW',G.slowmo]];
  for(var i=0;i<icons.length;i++){
    var it=icons[i]; if(it[2]<=0) continue;
    ctx.fillStyle='rgba(10,14,22,0.7)'; roundRect(ctx,px,py,86,20,6); ctx.fill();
    ctx.strokeStyle='#7fd0ff'; ctx.lineWidth=1; ctx.stroke();
    ctx.fillStyle='#cfe4ff'; ctx.fillText(it[1]+' '+Math.ceil(it[2]), px+8, py+15);
    px+=94;
  }
  // pause button (thumb reachable)
  ctx.fillStyle='rgba(255,255,255,0.14)';
  roundRect(ctx,CFG.W-64,CFG.H-64,48,48,10); ctx.fill();
  ctx.fillStyle='#cfe4ff';
  ctx.fillRect(CFG.W-64+17,CFG.H-64+14,5,20); ctx.fillRect(CFG.W-64+26,CFG.H-64+14,5,20);
  ctx.restore();
}
function overlayBox(ctx,w,h){
  ctx.fillStyle='rgba(5,8,16,0.86)';
  roundRect(ctx,CFG.W/2-w/2,CFG.H/2-h/2,w,h,18); ctx.fill();
  ctx.strokeStyle='rgba(120,180,255,0.35)'; ctx.lineWidth=2; ctx.stroke();
}
function drawTitle(ctx){
  ctx.save(); ctx.textAlign='center';
  var t=G.time;
  ctx.shadowColor='#5ad1ff'; ctx.shadowBlur=30+Math.sin(t*2)*10;
  ctx.fillStyle='#e8f4ff'; ctx.font='bold 64px system-ui,sans-serif';
  ctx.fillText('OVERDRIVE', CFG.W/2, CFG.H*0.3);
  ctx.fillStyle='#ffd45a';
  ctx.fillText('BREAK', CFG.W/2, CFG.H*0.3+66);
  ctx.shadowBlur=0;
  ctx.font='16px system-ui'; ctx.fillStyle='#8fa3c8';
  ctx.fillText('Get the ball ABOVE the bricks — then it goes wild.', CFG.W/2, CFG.H*0.3+120);
  ctx.fillText('Mouse / drag anywhere to move. Tap or SPACE to launch. P to pause.', CFG.W/2, CFG.H*0.3+146);
  var bl = 0.6+0.4*Math.sin(t*4);
  ctx.globalAlpha=bl; ctx.fillStyle='#e8f4ff'; ctx.font='bold 24px system-ui';
  ctx.fillText('TAP / CLICK TO PLAY', CFG.W/2, CFG.H*0.62);
  ctx.fillText('HOW TO PLAY', CFG.W/2, CFG.H*0.62+44);
  ctx.globalAlpha=1;
  ctx.restore();
}
// legend: draw each brick type with label
function drawLegend(ctx, clickHint){
  ctx.save();
  ctx.fillStyle='rgba(3,5,10,0.92)'; ctx.fillRect(0,0,CFG.W,CFG.H);
  ctx.textAlign='center'; ctx.fillStyle='#e8f4ff'; ctx.font='bold 40px system-ui';
  ctx.fillText('BRICK LEGEND', CFG.W/2, 90);
  ctx.font='14px system-ui'; ctx.fillStyle='#8fa3c8';
  ctx.fillText('All bricks break in ONE hit — they just have rules.', CFG.W/2, 118);
  var items = [
    ['std','Breaks on any hit.'],
    ['angle','Only breaks on a shallow GRAZE (≤25° from horizontal). Steep hits clink off.'],
    ['shock','Only breaks if the ball is FAST (≥640 px/s). Slow hits bounce.'],
    ['move','Slides side to side. Intercept it.'],
    ['boom','Explodes, chain-destroying nearby bricks. Great for breakthrough holes.'],
    ['phase','Fades in and out. Only breakable while solid (bright).'],
    ['core','ARMORED CORE: needs a power ball (FIRE/LASER/GIANT), a second hit while cracked, or lasers.'],
  ];
  var y=170;
  for(var i=0;i<items.length;i++){
    var demo=new Brick(CFG.W/2-260,y-22,80,30,items[i][0],1,{}, true);
    if(items[i][0]==='phase') demo.solid=true;
    demo.draw(ctx);
    ctx.textAlign='left'; ctx.fillStyle='#e8f4ff'; ctx.font='bold 18px system-ui';
    ctx.fillText(BRICK_TYPES[items[i][0]].label, CFG.W/2-160, y-8);
    ctx.fillStyle='#9fb4d8'; ctx.font='14px system-ui';
    ctx.fillText(items[i][1], CFG.W/2-160, y+14);
    ctx.textAlign='center'; y+=56;
  }
  y+=8;
  ctx.fillStyle='#ffd45a'; ctx.font='bold 16px system-ui';
  ctx.fillText('POWER BALLS: FIRE ★ plows through · SPLIT ●●● 3 balls · GIANT huge · LASER shoots · STICKY catches', CFG.W/2, y);
  ctx.fillStyle='#7dff8a'; ctx.font='bold 15px system-ui';
  ctx.fillText('CATCH DROPS: ⬌ wide · ⧗ slow-mo · ●●● multiball · ♥ life · ∪ magnet', CFG.W/2, y+26);
  ctx.fillStyle='#5f7396'; ctx.font='13px system-ui';
  ctx.fillText('Move paddle UP into the ball to SMASH it faster. Paddle moves in the shaded zone.', CFG.W/2, y+56);
  var bl=0.6+0.4*Math.sin(G.time*3);
  ctx.globalAlpha=bl; ctx.fillStyle='#e8f4ff'; ctx.font='bold 22px system-ui';
  ctx.fillText(clickHint||'TAP / CLICK / SPACE TO START', CFG.W/2, y+104);
  ctx.restore();
}
function drawPause(ctx){
  overlayBox(ctx,420,220);
  ctx.save(); ctx.textAlign='center'; ctx.fillStyle='#e8f4ff';
  ctx.font='bold 42px system-ui'; ctx.fillText('PAUSED',CFG.W/2,CFG.H/2-20);
  ctx.font='15px system-ui'; ctx.fillStyle='#8fa3c8';
  ctx.fillText('Tap, click or press P to resume',CFG.W/2,CFG.H/2+30);
  ctx.fillText('M: mute · SPACE: also resumes',CFG.W/2,CFG.H/2+58);
  ctx.restore();
}
function drawLevelClear(ctx){
  ctx.save(); ctx.textAlign='center';
  ctx.globalAlpha=0.85+Math.sin(G.time*6)*0.1;
  ctx.fillStyle='#e8f4ff'; ctx.font='bold 52px system-ui';
  ctx.fillText('LEVEL CLEAR!',CFG.W/2,CFG.H*0.42);
  ctx.font='20px system-ui'; ctx.fillStyle='#ffd45a';
  ctx.fillText('Score '+fmt(G.score),CFG.W/2,CFG.H*0.42+50);
  ctx.globalAlpha=1;
  ctx.font='16px system-ui'; ctx.fillStyle='#8fa3c8';
  ctx.fillText('Next: '+LEVELS[Math.min(G.level+1,LEVELS.length-1)].name,CFG.W/2,CFG.H*0.42+90);
  ctx.restore();
}
function drawGameOver(ctx){
  overlayBox(ctx,520,340);
  ctx.save(); ctx.textAlign='center';
  ctx.fillStyle='#ff6a8a'; ctx.font='bold 54px system-ui';
  ctx.fillText('GAME OVER',CFG.W/2,CFG.H/2-60);
  ctx.fillStyle='#e8f4ff'; ctx.font='bold 30px system-ui';
  ctx.fillText('Score '+fmt(G.score),CFG.W/2,CFG.H/2);
  ctx.fillStyle= G.score>=G.best&&G.score>0?'#7dff8a':'#8fa3c8'; ctx.font='18px system-ui';
  ctx.fillText(G.score>=G.best&&G.score>0?'★ NEW BEST ★':'Best '+fmt(G.best),CFG.W/2,CFG.H/2+36);
  var bl=0.6+0.4*Math.sin(G.time*3);
  ctx.globalAlpha=bl; ctx.fillStyle='#e8f4ff'; ctx.font='bold 22px system-ui';
  ctx.fillText('TAP / CLICK / SPACE TO RETRY',CFG.W/2,CFG.H/2+100);
  ctx.restore(); ctx.globalAlpha=1;
}
function drawLevelIntro(ctx){
  ctx.save(); ctx.textAlign='center';
  ctx.globalAlpha=clamp(1-G.levelIntroT/1.6,0,1);
  ctx.fillStyle='#e8f4ff'; ctx.font='bold 40px system-ui';
  ctx.fillText('LEVEL '+(G.level+1),CFG.W/2,CFG.H*0.4);
  ctx.font='20px system-ui'; ctx.fillStyle='#8fa3c8';
  ctx.fillText(LEVELS[G.level].name,CFG.W/2,CFG.H*0.4+40);
  ctx.restore(); ctx.globalAlpha=1;
}
function drawMuteIcon(ctx){
  ctx.save();
  ctx.fillStyle='rgba(255,255,255,0.12)';
  roundRect(ctx,CFG.W-64-58,CFG.H-64,48,48,10); ctx.fill();
  ctx.strokeStyle= G.muted?'#ff6a8a':'#cfe4ff'; ctx.lineWidth=2.5;
  ctx.fillStyle=ctx.strokeStyle;
  var x=CFG.W-64-58+16, y=CFG.H-64+17;
  ctx.beginPath(); ctx.moveTo(x,y+8); ctx.lineTo(x+7,y+8); ctx.lineTo(x+13,y+2); ctx.lineTo(x+13,y+22); ctx.lineTo(x+7,y+14); ctx.lineTo(x,y+8); ctx.closePath(); ctx.fill();
  if(!G.muted){ ctx.beginPath(); ctx.arc(x+16,y+9,7,-1.1,1.1); ctx.stroke(); }
  else { ctx.beginPath(); ctx.moveTo(x+15,y+4); ctx.lineTo(x+22,y+14); ctx.moveTo(x+22,y+4); ctx.lineTo(x+15,y+14); ctx.stroke(); }
  ctx.restore();
}