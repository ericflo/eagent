/* BREAKTHROUGH — js/levels.js
   Level layouts. Legend: . empty, N normal, A angle, S speed-gated, M moving,
   P phase, B bomb, R regen, X armored.
   Each level = { name, rows (array of strings), tier (difficulty scalar) }.
   Rows are top-to-bottom. Difficulty rises via special types + tighter layouts.
*/
window.Levels = (function () {
  const LEVELS = [
    { name: 'FIRST CONTACT', tier: 0, rows: [
      'NNNNANNBNNN',
      'NNNNNNNNNNN',
      'NNANNBNNNNN'
    ]},
    { name: 'THE GATES', tier: 1, rows: [
      'NNNNNNNNNNN',
      'NSSNNSSNNSN',
      'NNNNNNNNNNN',
      'NANNNNNNANN'
    ]},
    { name: 'DRIFT', tier: 1, rows: [
      'NMMNNNMNMMN',
      'NNNNNNNNNNN',
      'NSNSNNNSNSN',
      'NNNAMMMNANN'
    ]},
    { name: 'PHASE SHIFT', tier: 2, rows: [
      'NPPNNPPNNPP',
      'NNNNNNNNNNN',
      'NSNNMMNNSNN',
      'NPNNPPNNPNP',
      'NNNNAANNAAA'
    ]},
    { name: 'DEMOLITION', tier: 2, rows: [
      'NBNNBNNBNNB',
      'NNNNNNNNNNN',
      'NBNXNNNXNBN',
      'NNNNNNNNNNN',
      'NSNNSNNSNNS',
      'NNNMNNNMNNN'
    ]},
    { name: 'HYDRA', tier: 3, rows: [
      'NRNNRNNRNNR',
      'NNNXNNNXNNN',
      'NPNNPPNNPNN',
      'NSNMMMMMNSN',
      'NBNNNBNNNBN',
      'NAANNNNNAAN'
    ]},
    { name: 'FORTRESS', tier: 3, rows: [
      'XNNNNNNNNNX',
      'NXNNXXXXNNX',
      'NNNPPNNPNNN',
      'NXNRRRRRNXN',
      'NNNSNNNSNNN',
      'NBMMNNMMNBN',
      'NAAANNNAAAN'
    ]},
    { name: 'OVERTIME', tier: 4, rows: [
      'NPNPNNPNPNN',
      'XMNXMNXMNXM',
      'NNNSNNNSNNN',
      'XRNXRNXRNRX',
      'NPPNPPPPNPP',
      'NBNNBXNBNNB',
      'NSAANNAANNS',
      'NNMMNNNNMMN'
    ]},
    { name: 'CHAOS ENGINE', tier: 4, rows: [
      'XRXNXNXNXNRX',
      'NPNPPNNPPNPN',
      'XMMXMMMMXMMX',
      'NNNSNXXNSNNN',
      'XBXNXBXNXBXN',
      'NRRNRRRRNRRN',
      'NAASNNNNSAAN',
      'XPXNXPXPXNXP'
    ]},
    { name: 'BREAKTHROUGH', tier: 5, rows: [
      'XNXXBXXBXXNX',
      'XPXRXRRXRPXN',
      'XMSXSNNSXSMX',
      'NXNXAXAXNXNN',
      'XRXNRXXRNXRX',
      'NPPNPPPPNPNX',
      'XBXNXXXXNXBX',
      'NMSNAANASNMN',
      'XRXNXPPXNXRX'
    ]},
  ];

  // sanity: all rows within a level must be the same width
  function validate() {
    LEVELS.forEach((L, i) => {
      const w = L.rows[0].length;
      L.rows.forEach((r, ri) => { if (r.length !== w) console.error('Level', i, 'row', ri, 'length mismatch'); });
    });
  }
  validate();

  return { LEVELS };
})();
