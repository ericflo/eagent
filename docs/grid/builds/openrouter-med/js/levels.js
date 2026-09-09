'use strict';
// 6 handcrafted levels. Grid: 12 cols. Each entry: type char + optional object.
// Legend: . empty  s std  a angle  k shock  m mover  b boom  p phase  c core
// Uppercase variants give a guaranteed pickup/ball drop: S s w/ power, etc.
var LEVELS = [
{ name:'FIRST CONTACT',
  // gentle intro: big gaps, a couple funnels, easy breakthrough
  rows:[
    '..ssssssss..',
    '..s......s..',
    '..s..bb..s..',
    '..s......s..',
    '..ss.ss.ss..',
    '....ssss....',
    '.....mm.....',
    '..a......a..',
    '..s......s..',
    '....ssss....',
  ],
  drops:{}
},
{ name:'THE ROOF',
  // roof of angled bricks encourages going over the top
  rows:[
    'aaaaaaaaaaaa',
    '.s..s..s..s.',
    '.p..b..m..p.',
    '.s..s..s..s.',
    '....ssssss..',
    '..k......k..',
    '..s..cc..s..',
    '.....ss.....',
    '..m..ss..m..',
    '...ss..ss...',
  ],
  drops:{ 3:'split' }
},
{ name:'SHOCK TUNNEL',
  // shock bricks + funnel walls; speed matters
  rows:[
    '.k........k.',
    '.kk..ss..kk.',
    '.k..sass..k.',
    '.k.s....s.k.',
    '.k.s.bb.s.k.',
    '.k.s....s.k.',
    '.k..ssss..k.',
    '.kk..pp..kk.',
    '.k........k.',
    '....mmmm....',
  ],
  drops:{ 7:'giant' }
},
{ name:'CORE VAULT',
  // armored cores guard the interior, explosive corners
  rows:[
    'cc.s....s.cc',
    '.p.a.bb.a.p.',
    '..ss....ss..',
    '..s..cc..s..',
    '.k........k.',
    '.k.b.cc.b.k.',
    '..ss....ss..',
    '.p.a.bb.a.p.',
    'cc.s....s.cc',
    '...ssssss...',
  ],
  drops:{ 13:'laser', 20:'fire' }
},
{ name:'PHASE MAZE',
  rows:[
    'p.p.p.p.p.p.',
    'ss.s.ss.s.ss',
    '..m......m..',
    'aa.k.bb.k.aa',
    's...s..s...s',
    's.b.c..c.b.s',
    's...s..s...s',
    '..kk.mm.kk..',
    'p.p.p.p.p.p.',
    '..s.aaaa.s..',
  ],
  drops:{ 5:'sticky', 18:'multi' }
},
{ name:'OVERDRIVE ARENA',
  // dense, mix of everything; huge breakthrough potential with blasters
  rows:[
    'abkscpsckbma',
    '.s.k.s.s.k.s',
    'm.b.c..c.b.m',
    '.p.a.aa.a.p.',
    'ss.k.ss.sk..',
    '..c.bb..c...',
    'a.p.s..s.p.a',
    '.k.m.aa.m.k.',
    'b.s.k..k.s.b',
    'cc.s.pp.s.cc',
  ],
  drops:{ 2:'fire', 11:'laser', 21:'split' }
},
];
function parseLevel(idx){
  var L=LEVELS[idx], bricks=[], dropsUsed={};
  var cols=12, colW=(CFG.W-40)/cols, bw=colW-2, bh=CFG.brickH;
  var top=CFG.topUI+30;
  for(var r=0;r<L.rows.length;r++){
    var row=L.rows[r];
    for(var c=0;c<row.length;c++){
      var ch=row[c]; if(ch==='.'||ch===' ') continue;
      var type, power=null;
      var lower=ch.toLowerCase();
      type = {s:'std',a:'angle',k:'shock',m:'move',b:'boom',p:'phase',c:'core'}[lower];
      if(!type) continue;
      // numbering for drops: count bricks, assign numbered drops
      var n=bricks.length;
      if(L.drops[n]!==undefined && dropsUsed[n]===undefined){
        // decide: ball drop vs pickup drop
        var d=L.drops[n];
        if(BALL_DROPS[d]) power=d;   // ball drops attach to bricks
        else power=d;
        dropsUsed[n]=true;
      }
      var x=20+c*colW+1, y=top+r*(bh+CFG.brickGap);
      var opts={};
      if(type==='move') opts={dir:c%2?1:-1, speed:110+idx*20, minX:20, maxX:CFG.W-20-bw};
      bricks.push(new Brick(x,y,bw,bh,type,r,opts));
      if(power) bricks[bricks.length-1].power=power;
    }
  }
  return bricks;
}
function countBreakable(bricks){
  var n=0;
  for(var i=0;i<bricks.length;i++) if(bricks[i].alive && bricks[i].type!=='core') n++;
  return n;
}