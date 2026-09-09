// src/core/storage.js — localStorage-backed settings + high score persistence.
// Never throws (private browsing / disabled storage safe).

const KEY_SETTINGS = 'atticbreaker.settings.v1';
const KEY_HISCORE = 'atticbreaker.hiscore.v1';

const DEFAULT_SETTINGS = {
  muted: false,
  shake: true,
  reducedMotion: false,
  colorblind: false,
  sfxVolume: 0.8,
  musicVolume: 0.8,
};

function safeGet(key) {
  try { return localStorage.getItem(key); } catch { return null; }
}
function safeSet(key, val) {
  try { localStorage.setItem(key, val); } catch { /* ignore */ }
}

export function loadSettings() {
  try {
    const raw = safeGet(KEY_SETTINGS);
    if (!raw) return { ...DEFAULT_SETTINGS };
    return { ...DEFAULT_SETTINGS, ...JSON.parse(raw) };
  } catch {
    return { ...DEFAULT_SETTINGS };
  }
}

export function saveSettings(s) {
  safeSet(KEY_SETTINGS, JSON.stringify(s));
}

export function loadHighScore() {
  const raw = safeGet(KEY_HISCORE);
  const n = raw ? parseInt(raw, 10) : 0;
  return Number.isFinite(n) ? n : 0;
}

export function saveHighScore(score) {
  const cur = loadHighScore();
  if (score > cur) safeSet(KEY_HISCORE, String(score | 0));
  return Math.max(cur, score | 0);
}
