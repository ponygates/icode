// Test bootstrap for vitest (node environment).
//
// The store touches browser-only globals at runtime:
//   - localStorage: language/security-level/session-list persistence
//   - window: Electron-bridge feature detection (window.icode)
// Without these stubs the calls throw ReferenceError inside the store methods.

import { beforeEach } from 'vitest';

const storage = new Map<string, string>();

const localStorageStub: Storage = {
  getItem: (key) => (storage.has(key) ? storage.get(key)! : null),
  setItem: (key, value) => {
    storage.set(String(key), String(value));
  },
  removeItem: (key) => {
    storage.delete(String(key));
  },
  clear: () => {
    storage.clear();
  },
  key: (index) => [...storage.keys()][index] ?? null,
  get length() {
    return storage.size;
  },
};

beforeEach(() => {
  storage.clear();
  (globalThis as Record<string, unknown>).localStorage = localStorageStub;
  (globalThis as Record<string, unknown>).window = {};
});
