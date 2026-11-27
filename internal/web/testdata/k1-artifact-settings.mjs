// Runs static/artifact-settings.js in a stub page and checks the window a
// project save through it proposes. Usage: node k1-artifact-settings.mjs <case>
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const here = path.dirname(fileURLToPath(import.meta.url));
const source = readFileSync(path.join(here, '..', 'static', 'artifact-settings.js'), 'utf8');

const glm = { model: { model: 'glm', base_url: 'https://api.together.xyz/v1', context_tokens: 200000 } };
const local = 'http://127.0.0.1:9/v1';
const windowEdit = proposals => (proposals.at(-1)?.edits || []).find(e => e.key === '/model/context_tokens');
// The shell's EAGENT_BASE_URL moves a route whose model the file names to
// a 32k server: the address is the variable's (locked), the window the
// server's, editable.
const pinned = {
  presets: { glm },
  effective: { '/preset': 'glm', '/model/model': 'other', '/model/base_url': local, '/model/context_tokens': 32768 },
  saved: { '/preset': 'glm', '/model/model': 'other' },
  fields: { '/model/base_url': { writable: false, source: 'env:EAGENT_BASE_URL' }, '/model/context_tokens': { source: 'env' } },
};

const cases = {
  // Finding 10: the form moves the route to another address for the same
  // model with the base's window: the window is written, or the server's
  // replaces it on load.
  'moved-address': {
    presets: { glm },
    effective: { '/preset': 'glm', '/model/model': 'glm', '/model/base_url': local, '/model/context_tokens': 200000 },
    saved: { '/preset': 'glm', '/model/base_url': local },
    body: { config: { model: { model: 'glm', base_url: local, context_tokens: 200000 } }, base_preset: 'glm' },
    check(result, proposals, error) {
      if (error) throw error;
      const edit = windowEdit(proposals);
      if (!edit || edit.op !== 'set' || edit.value !== 200000) throw new Error('window not written: ' + JSON.stringify(proposals.at(-1)?.edits));
    },
  },
  // A save of another field under the variable writes no window.
  'pinned-untouched': {
    ...pinned,
    body: { config: { model: { model: 'other', base_url: local, context_tokens: 32768 }, tick_seconds: 15 }, base_preset: 'glm' },
    check(result, proposals, error) {
      if (error) throw error;
      if (windowEdit(proposals)) throw new Error('window written: ' + JSON.stringify(proposals.at(-1)?.edits));
    },
  },
  // A window the user enters under it is written.
  'pinned-entered': {
    ...pinned,
    body: { config: { model: { model: 'other', base_url: local, context_tokens: 16000 } }, base_preset: 'glm' },
    check(result, proposals, error) {
      if (error) throw error;
      const edit = windowEdit(proposals);
      if (!edit || edit.op !== 'set' || edit.value !== 16000) throw new Error('window not written: ' + JSON.stringify(proposals.at(-1)?.edits));
    },
  },
};

const c = cases[process.argv[2]];
if (!c) throw new Error('unknown case ' + process.argv[2]);
const keys = new Set([...Object.keys(c.effective), ...Object.keys(c.saved), '/model/model', '/model/base_url', '/model/context_tokens', '/tick_seconds']);
const resource = {
  generation: '',
  descriptor: { schema_version: 'x', fields: [...keys].map(key => ({ key, writable: true, ...(c.fields?.[key] || {}) })), actions: [] },
  snapshot: { version: 'v1', effective: c.effective, saved: c.saved, details: {} },
};
const element = () => ({ textContent: '', hidden: false, onclick: null, append() {}, replaceChildren() {}, classList: { add() {}, remove() {}, toggle() {} } });
const elements = new Map();
const proposals = [];
let ready;
const finale = {
  text: async () => JSON.stringify({ presets: c.presets, defaults: {}, bundles: {}, catalog: {} }),
  settings: { read: async () => ({ resource, editable: true, presentation: 'page' }), propose: async p => { proposals.push(p); }, clear: async () => {}, onResult() {} },
  on() {}, openLocalFiles: async () => {},
  ready: new Promise(resolve => { ready = resolve; }),
};
const window = { addEventListener() {}, EagentConfig: { reset() {}, render: async () => {}, draftToken: () => '', dirty: () => false } };
const document = {
  querySelector: selector => { if (!elements.has(selector)) elements.set(selector, element()); return elements.get(selector); },
  addEventListener() {}, createElement: element, createTextNode: t => t, body: element(),
};
vm.runInNewContext(source, { window, document, finale, setTimeout, clearTimeout, URL, JSON, Object, Set, Map, Array, Error, String, Promise });
ready(true);
await new Promise(resolve => setTimeout(resolve, 0));
await new Promise(resolve => setTimeout(resolve, 0));
let result, error;
try { result = await window.__eagent.api('/api/config', { method: 'PUT', body: JSON.stringify(c.body) }); } catch (e) { error = e; }
c.check(result, proposals, error);
console.log('ok', process.argv[2]);
