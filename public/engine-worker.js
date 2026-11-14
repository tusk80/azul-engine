'use strict';

// Runs the WebAssembly engine off the main thread, so the page stays
// responsive while it thinks. Used only when the page is served from a
// static host; a real server answers the same requests over HTTP.

importScripts('wasm_exec.js');

const go = new Go();
const ready = WebAssembly.instantiateStreaming(fetch('engine.wasm'), go.importObject).then((r) => {
  go.run(r.instance); // sets self.azulCall, then waits for calls
});

self.onmessage = async (e) => {
  const { id, path, body } = e.data;
  try {
    await ready;
    const r = self.azulCall(path, JSON.stringify(body || {}));
    self.postMessage({ id, status: r.status, body: r.body });
  } catch (err) {
    self.postMessage({ id, status: 500, body: JSON.stringify({ error: 'The engine failed to load: ' + err }) });
  }
};
