// Runs static/artifact-settings.js in a stub page and checks what a
// project save through it proposes. Usage: node r7settings-artifact-settings.mjs <case>
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const here = path.dirname(fileURLToPath(import.meta.url));
const source = readFileSync(path.join(here, '..', 'static', 'artifact-settings.js'), 'utf8');

const cases = {
  // The preset's fallback is in effect and the file overrides one of its
  // fields: a save without the fallback cannot remove it.
  fallback: {
    presets: { 'openai-high': { model: { model: 'gpt', fallback: { model: 'fb', reasoning_effort: 'xhigh' } } } },
    effective: { '/preset': 'openai-high', '/model/model': 'gpt', '/model/fallback/model': 'fb', '/model/fallback/reasoning_effort': 'medium' },
    saved: { '/preset': 'openai-high', '/model/fallback/reasoning_effort': 'medium' },
    body: { config: { model: { model: 'gpt' } }, base_preset: 'openai-high' },
    check(result, proposals, error) {
      if (!error || !/fallback/.test(error.message)) throw new Error('saved without the fallback: ' + JSON.stringify(proposals));
    },
  },
  // A new model with the window the form shows, equal to the preset's:
  // the window is written, or the new model's own replaces it on load.
  window: {
    presets: { glm: { model: { model: 'glm', context_tokens: 200000 } } },
    effective: { '/preset': 'glm', '/model/model': 'glm', '/model/context_tokens': 200000 },
    saved: { '/preset': 'glm' },
    body: { config: { model: { model: 'big', context_tokens: 200000 } }, base_preset: 'glm' },
    check(result, proposals, error) {
      if (error) throw error;
      const edits = proposals.at(-1)?.edits || [];
      if (!edits.some(e => e.op === 'set' && e.key === '/model/context_tokens' && e.value === 200000)) throw new Error('window not written: ' + JSON.stringify(edits));
    },
  },
};

const c = cases[process.argv[2]];
if (!c) throw new Error('unknown case ' + process.argv[2]);
const keys = new Set([...Object.keys(c.effective), ...Object.keys(c.saved), '/model/model', '/model/context_tokens', '/model/fallback/model', '/model/fallback/reasoning_effort']);
const resource = {
  generation: '',
  descriptor: { schema_version: 'x', fields: [...keys].map(key => ({ key, writable: true })), actions: [] },
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
