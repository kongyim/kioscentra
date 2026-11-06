// Vitest 3 may retain Node's native storage global instead of jsdom's storage.
// Use the actual browser implementation for persistence and storage-error tests.
Object.defineProperty(globalThis, "localStorage", {
  configurable: true,
  value: jsdom.window.localStorage,
});
