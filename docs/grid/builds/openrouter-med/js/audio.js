'use strict';
// Procedural WebAudio: bleeps, drone, beats, jingles. Unlocked on first gesture.
function AudioSys(){ this.ok=false; }
AudioSys.prototype.init=function(){
  if(this.ok) return;
  var AC = window.AudioContext||window.webkitAudioContext; if(!AC) return;
  this.ctx = new AC();
  this.master = this.ctx.createGain(); this.master.gain.value = this.muted!==undefined&&G.muted?0:0.5;
  this.master.connect(this.ctx.destination);
  this.musicGain = this.ctx.createGain(); this.musicGain.gain.value=0.25; this.musicGain.connect(this.master);
  this.ok = true;
  this._startMusic();
};
AudioSys.prototype.setMuted=function(m){ if(this.master) this.master.gain.value = m?0:0.5; };
AudioSys.prototype._env=function(node,t,a,d,peak){
  var g=this.ctx.createGain(); g.gain.setValueAtTime(0.0001,t);
  g.gain.exponentialRampToValueAtTime(peak,t+a); g.gain.exponentialRampToValueAtTime(0.0001,t+a+d);
  node.connect(g); return g;
};
AudioSys.prototype.tone=function(freq,dur,type,vol,dest,slideTo){
  if(!this.ok||G.muted) return;
  var t=this.ctx.currentTime, o=this.ctx.createOscillator();
  o.type=type||'square'; o.frequency.setValueAtTime(freq,t);
  if(slideTo) o.frequency.exponentialRampToValueAtTime(Math.max(30,slideTo),t+dur);
  var g=this._env(o,t,0.005,dur,vol||0.3);
  g.connect(dest||this.master); o.start(t); o.stop(t+dur+0.05);
};
AudioSys.prototype.noise=function(dur,vol,lp){
  if(!this.ok||G.muted) return;
  var t=this.ctx.currentTime, n=this.ctx.sampleRate*dur|0;
  var buf=this.ctx.createBuffer(1,n,this.ctx.sampleRate), d=buf.getChannelData(0);
  for(var i=0;i<n;i++) d[i]=(Math.random()*2-1)*(1-i/n);
  var s=this.ctx.createBufferSource(); s.buffer=buf;
  var f=this.ctx.createBiquadFilter(); f.type='lowpass'; f.frequency.value=lp||3000;
  var g=this.ctx.createGain(); g.gain.value=vol||0.3;
  s.connect(f); f.connect(g); g.connect(this.master); s.start(t);
};
// pitched brick break: higher rows = higher pitch
AudioSys.prototype.brick=function(row,combo){
  var f = 300 + row*28 + clamp(combo,0,30)*18;
  this.tone(f,0.12,'square',0.22,null,f*1.5);
  this.noise(0.08,0.15,5000);
};
AudioSys.prototype.clink=function(){ this.tone(900,0.06,'triangle',0.12); };
AudioSys.prototype.paddle=function(smash){
  this.tone(smash?180:120,0.09,'sine',smash?0.5:0.3,null,smash?90:60);
  this.noise(0.05,0.1,1500);
  if(smash) this.tone(520,0.15,'sawtooth',0.18,null,260);
};
AudioSys.prototype.wall=function(){ this.tone(220,0.05,'triangle',0.12,null,180); };
AudioSys.prototype.pickup=function(){ var t=[523,659,784,1047],self=this;
  t.forEach(function(f,i){ setTimeout(function(){ self.tone(f,0.12,'square',0.2); },i*70); }); };
AudioSys.prototype.powerball=function(){ var t=[440,554,659,880,1109],self=this;
  t.forEach(function(f,i){ setTimeout(function(){ self.tone(f,0.1,'sawtooth',0.16); },i*55); }); };
AudioSys.prototype.frenzy=function(){ this.tone(80,0.8,'sawtooth',0.25,null,320); this.noise(0.5,0.2,900); };
AudioSys.prototype.loseBall=function(){ this.tone(400,0.5,'sawtooth',0.3,null,80); this.noise(0.3,0.2,600); };
AudioSys.prototype.gameover=function(){ var t=[392,311,262,196],self=this;
  t.forEach(function(f,i){ setTimeout(function(){ self.tone(f,0.4,'sawtooth',0.25); },i*220); }); };
AudioSys.prototype.levelclear=function(){ var t=[523,659,784,1047,1319],self=this;
  t.forEach(function(f,i){ setTimeout(function(){ self.tone(f,0.18,'square',0.22); },i*110); }); };
AudioSys.prototype.explode=function(){ this.noise(0.35,0.4,1200); this.tone(90,0.3,'sine',0.4,null,40); };
// ---- music: beat + drone, intensity follows G.frenzy ----
AudioSys.prototype._startMusic=function(){
  var self=this;
  this.beatT=0; this.beatN=0;
  // sustained drone oscillators
  this.droneA=this.ctx.createOscillator(); this.droneA.type='sawtooth'; this.droneA.frequency.value=55;
  this.droneB=this.ctx.createOscillator(); this.droneB.type='sawtooth'; this.droneB.frequency.value=55.7;
  this.droneF=this.ctx.createBiquadFilter(); this.droneF.type='lowpass'; this.droneF.frequency.value=300;
  this.droneG=this.ctx.createGain(); this.droneG.gain.value=0.0;
  this.droneA.connect(this.droneF); this.droneB.connect(this.droneF);
  this.droneF.connect(this.droneG); this.droneG.connect(this.musicGain);
  this.droneA.start(); this.droneB.start();
};
AudioSys.prototype.update=function(dt,intensity){
  if(!this.ok) return;
  // drone volume/filter follow intensity
  this.droneG.gain.value = 0.05 + intensity*0.22;
  this.droneF.frequency.value = 300 + intensity*1800;
  this.droneA.frequency.value = 55 + intensity*30;
  // beat: faster & punchier with intensity
  this.beatT -= dt;
  var period = 0.6 - intensity*0.32;
  if(this.beatT<=0){
    this.beatT = period;
    this.beatN++;
    if(intensity>0.05){
      var kick = this.beatN%4===0 ? 70 : 55;
      this.tone(kick,0.1,'sine',0.25+intensity*0.35,null,40);
      if(intensity>0.4 && this.beatN%2===1) this.tone(2000+intensity*3000,0.04,'square',0.05+intensity*0.1);
      if(intensity>0.7 && this.beatN%4===2){ // arpeggio bursts
        var sc=[523,587,659,784],self=this;
        sc.forEach(function(f,i){ setTimeout(function(){self.tone(f*(1+intensity),0.08,'sawtooth',0.1);},i*45); });
      }
    }
  }
};