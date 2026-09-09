'use strict';
// Input: mouse, keyboard, touch, pen via unified Pointer Events + optional gamepad.
function Input(canvas){
  this.canvas = canvas;
  this.mouse = {x:CFG.W/2, y:CFG.H*0.9, active:false, down:false};
  this.keys = {};
  this.touch = null;   // active touch/pen pointer {id,x,y}
  this.pointerDown = false;
  this.tapCb = null;   // tap/press event (launch / UI advance)
  this.pauseCb = null;
  this.buttonHit = null;
  this.gpX = 0; this.gpY = 0; this.gpPressed = false;
  var self=this;

  // --- coordinate mapping: screen -> virtual ---
  this.sx = 1; this.sy = 1; this.offX = 0; this.offY = 0;
  this.map = function(px,py){ return { x:(px-self.offX)/self.sx, y:(py-self.offY)/self.sy }; };

  function prevent(e){ e.preventDefault(); }
  canvas.addEventListener('contextmenu', prevent);
  document.addEventListener('contextmenu', prevent);
  document.addEventListener('touchmove', prevent, {passive:false}); // stop scroll/pinch on canvas
  document.addEventListener('gesturestart', prevent);
  // keep keyboard from scrolling the page
  window.addEventListener('keydown', function(e){
    if(['ArrowUp','ArrowDown','ArrowLeft','ArrowRight','Space'].indexOf(e.code)>=0) prevent(e);
  });

  // --- unified pointer events (mouse + touch + pen) ---
  canvas.addEventListener('pointerdown', function(e){
    try{ canvas.setPointerCapture && canvas.setPointerCapture(e.pointerId); }catch(err){}
    var p=self.map(e.clientX,e.clientY);
    self.pointerDown=true;
    self.mouse.x=p.x; self.mouse.y=p.y; self.mouse.active=true;
    if(e.pointerType!=='mouse'){
      self.touch={id:e.pointerId, x:p.x, y:p.y};
    }
    self.buttonHit = (typeof checkButtons==='function')? checkButtons(p.x,p.y) : null;
    if(self.tapCb) self.tapCb();
  });
  canvas.addEventListener('pointermove', function(e){
    var p=self.map(e.clientX,e.clientY);
    self.mouse.x=p.x; self.mouse.y=p.y; self.mouse.active=true;
    if(self.touch && e.pointerId===self.touch.id){ self.touch.x=p.x; self.touch.y=p.y; }
  });
  function release(e){
    self.pointerDown=false;
    if(self.touch && e.pointerId===self.touch.id) self.touch=null;
  }
  canvas.addEventListener('pointerup', release);
  canvas.addEventListener('pointercancel', release);
  window.addEventListener('blur', function(){ self.keys={}; self.touch=null; self.pointerDown=false; });

  // --- keyboard ---
  window.addEventListener('keydown', function(e){
    if(self.keys[e.code]) return; // no key-repeat re-trigger
    self.keys[e.code]=true;
    if(e.code==='Space'){ if(self.tapCb) self.tapCb(); }
    if(e.code==='KeyP'||e.code==='Escape'){ if(self.pauseCb) self.pauseCb(); }
    if(e.code==='KeyM'){ G.muted=!G.muted; if(G.audio) G.audio.setMuted(G.muted); }
  });
  window.addEventListener('keyup', function(e){ self.keys[e.code]=false; });

  window.addEventListener('gamepadconnected', function(){ self.gp=true; });
}
// Where should the paddle target? Priority: touch/pen drag > held keys > gamepad > mouse.
Input.prototype.paddleTarget = function(dt, cur, spd){
  var dx=0,dy=0;
  if(this.keys.ArrowLeft||this.keys.KeyA) dx-=1;
  if(this.keys.ArrowRight||this.keys.KeyD) dx+=1;
  if(this.keys.ArrowUp||this.keys.KeyW) dy-=1;
  if(this.keys.ArrowDown||this.keys.KeyS) dy+=1;
  if(this.touch){ return {x:this.touch.x, y:this.touch.y, direct:true}; }
  if(dx||dy){ return {x:cur.x+dx*spd*dt*3.2, y:cur.y+dy*spd*dt*2.4, direct:false, dx:dx, dy:dy}; }
  if(this.gpX||this.gpY){ return {gp:true}; }
  if(this.mouse.active){ return {x:this.mouse.x, y:this.mouse.y, direct:true}; }
  return {x:cur.x, y:cur.y, direct:false, dx:0, dy:0};
};
Input.prototype.tapHeld = function(){ return this.pointerDown || this.keys.Space || this.gpPressed; };
Input.prototype.updateGamepad=function(){
  this.gpPressed=false;
  var pads=navigator.getGamepads?navigator.getGamepads():[];
  for(var i=0;i<pads.length;i++){ var p=pads[i]; if(!p) continue;
    this.gpX = (Math.abs(p.axes[0])>0.2?p.axes[0]:0);
    this.gpY = (Math.abs(p.axes[1])>0.2?p.axes[1]:0);
    if(p.buttons[0]&&p.buttons[0].pressed) this.gpPressed=true;
    break;
  }
};