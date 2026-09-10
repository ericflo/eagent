// DOM HUD + overlays + toasts + banners.
import { BRICK_INFO } from './levels.js';
const $ = id => document.getElementById(id);
export class UI {
  constructor(){
    this.bannerT=null; this.hintOn=false;
  }
  buildLevelBtns(n, go){
    const w=$('lvl-btns'); w.innerHTML='';
    for(let i=0;i<n;i++){
      const b=document.createElement('button');
      b.textContent=i+1; b.title='Start at level '+(i+1);
      b.onclick=()=>go(i);
      w.appendChild(b);
    }
  }
  buildLegend(){
    const w=$('legend'); w.innerHTML='';
    for(const k of Object.keys(BRICK_INFO)){
      const i=BRICK_INFO[k];
      const s=document.createElement('span');
      s.innerHTML=`<b style="color:${i.color}">${i.icon||'▮'} ${i.name}</b><br/>${i.desc}`;
      w.appendChild(s);
    }
  }
  hud(s){
    $('hud-score').textContent=s.score.toLocaleString();
    $('hud-hi').textContent=s.hi.toLocaleString();
    const m=$('hud-mult');
    m.textContent='×'+s.mult;
    m.classList.toggle('hot',s.od);
    $('hud-level').textContent='LV '+(s.level+1)+' · '+s.levelName;
    $('hud-lives').textContent='●'.repeat(Math.max(0,s.lives))+'○'.repeat(Math.max(0,3-Math.max(0,s.lives)))||'—';
    $('od-fill').style.width=(s.odT*100).toFixed(1)+'%';
    $('od-label').textContent=s.od?`OVERDRIVE ×${s.mult}`:'OVERDRIVE';
    // fx timers
    const fx=$('fx-timers'); fx.innerHTML='';
    for(const e of s.effects){
      const d=document.createElement('div'); d.className='fx';
      d.innerHTML=`<span>${e.icon} ${e.name}</span><span class="bar"><i style="width:${(e.t/e.max*100).toFixed(0)}%;background:${e.color}"></i></span>`;
      fx.appendChild(d);
    }
  }
  banner(html,ms=1600){
    const b=$('banner');
    b.innerHTML=html; b.classList.remove('hidden');
    clearTimeout(this.bannerT);
    if(ms>0) this.bannerT=setTimeout(()=>b.classList.add('hidden'),ms);
  }
  hideBanner(){ clearTimeout(this.bannerT); $('banner').classList.add('hidden'); }
  hint(show){ $('hint-top').classList.toggle('hidden',!show); }
  toast(text){
    const w=$('toast-wrap');
    const d=document.createElement('div'); d.className='toast'; d.textContent=text;
    w.appendChild(d);
    while(w.children.length>3) w.firstChild.remove();
    setTimeout(()=>d.remove(),2100);
  }
  combo(n){
    const c=$('combo');
    if(n>=2){ c.textContent=`COMBO ×${n}`; c.classList.remove('hidden'); }
    else c.classList.add('hidden');
  }
  boostFlash(){
    const f=$('flash-boost');
    f.classList.remove('hidden');
    clearTimeout(this._bt);
    this._bt=setTimeout(()=>f.classList.add('hidden'),380);
  }
  show(id){ $(id).classList.remove('hidden'); }
  hide(id){ $(id).classList.add('hidden'); }
  hideAll(){ ['ov-start','ov-help','ov-pause','ov-clear','ov-over'].forEach(i=>$(i).classList.add('hidden')); }
  setStartHi(v){ $('start-hi').textContent=v.toLocaleString(); }
}
