'use strict';

// Azul analysis board. All rules live in the Go engine: the page asks the
// server to validate positions (/apply with no moves), play moves (/apply),
// deal (/new) and search (/bestmove).

const COLORS = ['blue', 'yellow', 'red', 'black', 'white'];
const FLOOR_PEN = [-1, -1, -2, -2, -2, -3, -3];
const wallColor = (r, col) => COLORS[(col - r + 5) % 5];
const $ = (id) => document.getElementById(id);

const ui = {
  pos: null, // current position record from the server: {state, roundOver, gameOver, winner, action}
  history: [], // [{pos, label}]: earlier positions and what happened next
  analysis: null, // /bestmove response for pos
  analysisErr: '',
  thinking: false,
  reqId: 0,
  pv: null, // {line, positions, step} while stepping through a line
  sel: null, // {src: 0-4 | 'C', color} while choosing a move
  vs: null, // {human: 0 | 1} while playing against the engine
  review: null, // {history, done, total, loss: [{loss, worse, mover}]} for the moves played
  edit: false,
  draft: null, // state being edited
  draftErr: '',
  draftDirty: false,
  brush: 'blue',
  bagManual: false,
  error: '',
};

// ---------------------------------------------------------------- server

// The engine is reached in one of two ways, chosen in setup():
//   - over HTTP, when the page is served by the engine's own server;
//   - as WebAssembly in a Web Worker, when the page is on a static host.
// Both take a path and a JSON body and give back {status, data}.
let transport = httpCall;

async function httpCall(path, body) {
  const res = await fetch(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  try {
    return { status: res.status, data: await res.json() };
  } catch {
    return { status: res.status || 500, data: { error: `${path}: ${res.status} ${res.statusText}` } };
  }
}

// The worker answers one request at a time, in order. A search that has
// been overtaken by a newer one is dropped before it starts, so quick moves
// do not queue up a second of thinking each.
const wasm = { worker: null, nextId: 1, waiting: new Map(), chain: Promise.resolve(), latestSearch: 0 };

function wasmCall(path, body) {
  const isSearch = path === '/bestmove';
  const mine = isSearch ? ++wasm.latestSearch : 0;
  const result = wasm.chain.then(() => {
    if (isSearch && mine !== wasm.latestSearch) return { status: 499, data: { error: 'superseded' } };
    return new Promise((resolve) => {
      const id = wasm.nextId++;
      wasm.waiting.set(id, resolve);
      wasm.worker.postMessage({ id, path, body });
    });
  });
  wasm.chain = result.catch(() => {});
  return result;
}

function startWasm() {
  wasm.worker = new Worker('engine-worker.js');
  wasm.worker.onmessage = (e) => {
    const resolve = wasm.waiting.get(e.data.id);
    wasm.waiting.delete(e.data.id);
    let data;
    try {
      data = JSON.parse(e.data.body);
    } catch {
      data = { error: 'bad response from the engine' };
    }
    if (resolve) resolve({ status: e.data.status, data });
  };
  wasm.worker.onerror = () => {
    for (const resolve of wasm.waiting.values()) resolve({ status: 500, data: { error: 'The engine could not be loaded. Reload the page to try again.' } });
    wasm.waiting.clear();
  };
  transport = wasmCall;
}

async function api(path, body) {
  const { status, data } = await transport(path, body);
  if (status === 429) throw new Error('Too many requests. Give it a few seconds and try again.');
  if (status === 503) throw new Error('The engine is busy right now. Try again in a moment.');
  if (status < 200 || status >= 300) throw new Error(data.error || `request failed (${status})`);
  return data;
}

// Browser storage can be blocked or empty (private windows); the page works
// without it.
const store = {
  get(k) { try { return localStorage.getItem(k); } catch { return null; } },
  set(k, v) { try { localStorage.setItem(k, v); } catch { /* ignore */ } },
};

let toastTimer = 0;
function toast(msg) {
  const t = $('toast');
  t.textContent = msg;
  t.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { t.hidden = true; }, 2600);
}

async function run(fn) {
  ui.error = '';
  try {
    await fn();
  } catch (e) {
    ui.error = e.message;
  }
  render();
}

// ------------------------------------------------------------- positions

function setPosition(pos, label) {
  if (ui.pos && label) ui.history.push({ pos: ui.pos, label });
  ui.pos = pos;
  ui.analysis = null;
  ui.analysisErr = '';
  ui.pv = null;
  ui.sel = null;
  render();
  if (ui.vs) vsStep();
  else if ($('auto').checked) analyze();
}

// ----------------------------------------------------------- play vs engine

// In a game against the engine the human keeps the side that was to move
// when it started. The engine answers on its turns, rounds are scored and
// dealt without asking, and the analysis stays hidden.
function toggleVs() {
  if (ui.vs) {
    ui.vs = null;
  } else {
    if (ui.edit) toggleEdit();
    ui.vs = { human: ui.pos.state.toMove };
    ui.pv = null;
    ui.analysis = null;
  }
  render();
  if (ui.vs) vsStep();
  else if ($('auto').checked) analyze();
}

let vsTimer = 0;
function vsStep() {
  clearTimeout(vsTimer);
  const pos = ui.pos;
  if (!ui.vs || ui.edit || pos.gameOver) return;
  if (pos.roundOver) {
    vsTimer = setTimeout(() => roundAction(pos.action === 'score' ? 'deal' : 'score'), 900);
    return;
  }
  if (pos.state.toMove === ui.vs.human) return;
  vsTimer = setTimeout(async () => {
    ui.thinking = true;
    render();
    try {
      const r = await api('/bestmove', { state: pos.state, timeMs: +$('think').value, multiPV: 1, engine: $('engine').value });
      ui.thinking = false;
      // The human may have stopped the game or undone moves meanwhile.
      if (ui.vs && ui.pos === pos) await playMove(r.move.text);
    } catch (e) {
      ui.thinking = false;
      ui.error = e.message;
      render();
    }
  }, 350);
}

async function newDeal() {
  await run(async () => {
    const pos = await api('/new', {});
    ui.history = [];
    ui.pos = null;
    setPosition(pos);
    motion.deal();
  });
}

async function playMove(text) {
  await run(async () => {
    const before = ui.pos.state;
    const from = motion.capture(text);
    const r = await api('/apply', { state: before, moves: [text] });
    setPosition(r.positions[1], text);
    motion.move(text, before, r.positions[1].state, from);
  });
}

async function roundAction(action) {
  await run(async () => {
    const before = ui.pos.state;
    const r = await api('/apply', { state: before, moves: [action] });
    setPosition(r.positions[1], action === 'score' ? 'scored round' : 'dealt next round');
    if (action === 'deal') motion.deal();
    else motion.score(before, r.positions[1].state);
  });
}

function undo() {
  if (ui.edit || ui.pv) return;
  let h = ui.history.pop();
  // Against the engine, go back to the human's own turn.
  while (h && ui.vs && (h.pos.roundOver || h.pos.state.toMove !== ui.vs.human) && ui.history.length) h = ui.history.pop();
  if (!h) return;
  ui.pos = null;
  setPosition(h.pos);
}

function jumpTo(i) {
  const h = ui.history[i];
  ui.history = ui.history.slice(0, i);
  ui.pos = null;
  setPosition(h.pos);
}

// -------------------------------------------------------------- analysis

async function analyze() {
  const pos = ui.pos;
  if (!pos || pos.roundOver || pos.gameOver || ui.vs) return;
  const id = ++ui.reqId;
  ui.thinking = true;
  ui.analysis = null;
  ui.analysisErr = '';
  render();
  try {
    const r = await api('/bestmove', { state: pos.state, timeMs: +$('think').value, multiPV: 3, engine: $('engine').value });
    if (id !== ui.reqId) return;
    ui.analysis = r;
  } catch (e) {
    if (id !== ui.reqId) return;
    ui.analysisErr = e.message;
  }
  ui.thinking = false;
  render();
}

async function openLine(i) {
  const line = ui.analysis.lines[i];
  await run(async () => {
    let r = await api('/apply', { state: ui.pos.state, moves: line.pv });
    const positions = r.positions;
    const last = positions[positions.length - 1];
    if (last.roundOver && !last.gameOver) {
      r = await api('/apply', { state: last.state, moves: ['score'] });
      positions.push(r.positions[1]);
    }
    ui.pv = { line: i, positions, step: 0 };
    ui.sel = null;
  });
}

function pvStep(to) {
  if (!ui.pv) return;
  const { positions, step } = ui.pv;
  const next = Math.max(0, Math.min(positions.length - 1, to));
  const p = positions[next];
  // One step forward is animated like a played move.
  const from = next === step + 1 && p.move ? motion.capture(p.move.text) : null;
  ui.pv.step = next;
  render();
  if (from) motion.move(p.move.text, positions[step].state, p.state, from);
  else if (next === step + 1 && p.action === 'score') motion.score(positions[step].state, p.state);
}

// "Play to here": make the line's moves on the real board.
function pvPlay() {
  const { positions, step } = ui.pv;
  for (let i = 1; i <= step; i++) {
    const p = positions[i];
    ui.history.push({ pos: positions[i - 1], label: p.move ? p.move.text : 'scored round' });
  }
  ui.pos = null;
  setPosition(positions[step]);
}

// ----------------------------------------------------------------- edit

function toggleEdit() {
  if (ui.edit) {
    if (ui.draftDirty && ui.draftErr) {
      if (!confirm(`This position is not valid:\n${ui.draftErr}\n\nDiscard your edits?`)) return;
    }
    ui.edit = false;
    ui.draft = null;
    ui.draftDirty = false;
    ui.draftErr = '';
    render();
    if ($('auto').checked) analyze();
    return;
  }
  ui.edit = true;
  ui.pv = null;
  ui.sel = null;
  ui.draft = structuredClone(ui.pos.state);
  ui.draftDirty = false;
  ui.draftErr = '';
  ui.analysis = null;
  render();
}

let editTimer = 0;
let editPrev = null; // position before the current run of edits

// edited is called after every change to ui.draft.
function edited() {
  if (!ui.draftDirty) editPrev = ui.pos;
  ui.draftDirty = true;
  render();
  clearTimeout(editTimer);
  editTimer = setTimeout(validateDraft, 150);
}

async function validateDraft() {
  const s = structuredClone(ui.draft);
  if (!ui.bagManual) {
    delete s.bag;
    delete s.lid;
  }
  // A new position is being set up: whatever is on the table is this
  // round's deal, so the game is not over.
  delete s.gameOver;
  try {
    const r = await api('/apply', { state: s, moves: [] });
    ui.draftErr = '';
    const pos = r.positions[0];
    if (editPrev && editPrev !== pos) {
      if (!ui.history.length || ui.history[ui.history.length - 1].pos !== editPrev) {
        ui.history.push({ pos: editPrev, label: 'edited board' });
      }
    }
    ui.pos = pos;
    // Keep the draft's own layout, but show inferred bag and lid.
    ui.draft.bag = pos.state.bag;
    ui.draft.lid = pos.state.lid;
  } catch (e) {
    ui.draftErr = e.message;
  }
  render();
}

// ------------------------------------------------------------------ JSON

function openJSON() {
  const state = ui.edit ? ui.draft : ui.pos.state;
  $('json-text').value = JSON.stringify(state, null, 2);
  $('json-err').textContent = '';
  $('json-dialog').showModal();
}

async function loadJSON() {
  let data;
  try {
    data = JSON.parse($('json-text').value);
  } catch (e) {
    $('json-err').textContent = 'Not valid JSON: ' + e.message;
    return;
  }
  const state = data && data.state ? data.state : data;
  try {
    const r = await api('/apply', { state, moves: [] });
    $('json-dialog').close();
    if (ui.edit) {
      ui.edit = false;
      ui.draft = null;
    }
    setPosition(r.positions[0], 'imported position');
  } catch (e) {
    $('json-err').textContent = e.message;
  }
}

// ---------------------------------------------------------- share links

// A position travels in the URL fragment as base64url JSON. Bag and lid are
// left out when the lid is empty, since the server can then infer them.
function encodeState(state) {
  const s = structuredClone(state);
  if (s.lid && Object.values(s.lid).every((n) => !n)) {
    delete s.bag;
    delete s.lid;
  }
  return btoa(JSON.stringify(s)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

function decodeState(text) {
  const b64 = text.replace(/-/g, '+').replace(/_/g, '/');
  return JSON.parse(atob(b64 + '='.repeat((4 - (b64.length % 4)) % 4)));
}

async function share() {
  const state = ui.edit ? ui.draft : ui.pos.state;
  const url = `${location.origin}${location.pathname}#p=${encodeState(state)}`;
  try {
    await navigator.clipboard.writeText(url);
    toast('Link copied. It opens this exact position.');
  } catch {
    location.hash = url.slice(url.indexOf('#'));
    toast('The link is in the address bar: copy it from there.');
  }
}

// loadFromHash opens the position in the URL, if there is one.
async function loadFromHash() {
  if (!location.hash.startsWith('#p=')) return false;
  try {
    const r = await api('/apply', { state: decodeState(location.hash.slice(3)), moves: [] });
    ui.history = [];
    ui.pos = null;
    ui.edit = false;
    ui.draft = null;
    setPosition(r.positions[0]);
    return true;
  } catch {
    toast('That link does not hold a valid position.');
    return false;
  }
}

// --------------------------------------------------------------- examples

const EMPTY_WALL = ['.....', '.....', '.....', '.....', '.....'];
const EXAMPLES = [
  {
    name: 'Opening: first pick',
    state: {
      toMove: 0, nextFirst: 0, tokenInCenter: true,
      factories: [['blue', 'blue', 'red', 'white'], ['yellow', 'yellow', 'yellow', 'black'], ['red', 'red', 'red', 'red'],
        ['blue', 'yellow', 'black', 'white'], ['black', 'black', 'white', 'white']],
      center: [],
      players: [
        { score: 0, wall: EMPTY_WALL, lines: [{}, {}, {}, {}, {}], floor: 0 },
        { score: 0, wall: EMPTY_WALL, lines: [{}, {}, {}, {}, {}], floor: 0 },
      ],
    },
  },
  {
    name: 'Mid-round: solved to the end',
    state: {
      round: 3, toMove: 0, nextFirst: 1, tokenInCenter: false,
      factories: [[], ['red', 'red', 'black', 'white'], [], ['blue', 'yellow', 'yellow', 'white'], []],
      center: ['blue', 'blue', 'black', 'black', 'black', 'white'],
      players: [
        { score: 9, wall: ['B.R..', '.B...', '..B..', '.....', '.....'],
          lines: [{}, { color: 'yellow', count: 1 }, {}, { color: 'black', count: 2 }, {}], floor: 0 },
        { score: 8, wall: ['..R..', '..Y..', 'K....', '.....', '.....'],
          lines: [{ color: 'blue', count: 1 }, {}, { color: 'white', count: 2 }, {}, { color: 'red', count: 3 }], floor: 1 },
      ],
    },
  },
];

async function loadExample(i) {
  await run(async () => {
    const r = await api('/apply', { state: EXAMPLES[i].state, moves: [] });
    if (ui.edit) {
      ui.edit = false;
      ui.draft = null;
    }
    setPosition(r.positions[0], 'loaded example');
  });
}

// ------------------------------------------------------------ preferences

// The light/dark theme is theme.js's job, on every page.

function applySymbols(on) {
  document.body.classList.toggle('symbols', on);
  $('opt-symbols').checked = on;
}

// setStatusNote shows a line under the board before the first position is in.
function setStatusNote(text) {
  $('status').textContent = text;
}

// setup reads the engine's limits and the visitor's saved preferences.
async function setup() {
  applySymbols(store.get('azul.symbols') === '1');
  if (store.get('azul.engine') === 'classic') $('engine').value = 'classic';
  syncBrain();
  $('intro').hidden = store.get('azul.intro') === 'done';

  EXAMPLES.forEach((e, i) => $('examples').append(h('option', { value: i }, e.name)));

  // The static build marks the page (data-engine="wasm"): the engine then
  // runs in the browser. Otherwise the page came from an engine server,
  // which answers /config with its limits.
  let cfg = null;
  if (document.documentElement.dataset.engine !== 'wasm') {
    try {
      const res = await fetch('/config');
      const data = res.ok ? await res.json() : null;
      if (data && data.maxTimeMs) cfg = data;
    } catch { /* fall back to the in-browser engine */ }
  }
  if (!cfg) {
    startWasm();
    setStatusNote('Loading the engine…');
    try {
      cfg = await api('/config', {});
    } catch (e) {
      ui.error = e.message;
      cfg = { defaultTimeMs: 1000, maxTimeMs: 10000, version: '' };
    }
    setStatusNote('');
  }
  // Offer only search times this engine will honour.
  const times = [300, 1000, 2000, 3000, 10000].filter((ms) => ms <= cfg.maxTimeMs);
  const saved = +store.get('azul.think');
  const pick = times.includes(saved) ? saved : times.reduce((a, b) => (Math.abs(b - cfg.defaultTimeMs) < Math.abs(a - cfg.defaultTimeMs) ? b : a));
  times.forEach((ms) => $('think').append(h('option', { value: ms, selected: ms === pick }, `${ms / 1000} s`)));
  if (cfg.version) $('version').textContent = 'build ' + cfg.version;
}

// ------------------------------------------------------------------ photo

// A reference photo of a real board, shown above the editor while its tiles
// are clicked in. It is read from the device as a local object URL. It leaves
// the device only if the visitor presses "Read with AI", and then goes
// straight to the AI provider their own key belongs to.
const photo = { url: '', turns: 0, source: null, data: '' };

function showPhoto(file) {
  if (!file || !file.type.startsWith('image/')) {
    toast('That file is not an image.');
    return;
  }
  const img = new Image();
  img.onload = () => {
    photo.source = img;
    photo.turns = 0;
    drawPhoto();
    $('photo').hidden = false;
    if (!ui.edit && !ui.pv) toggleEdit();
    toast('Photo added. Click its tiles into the board, or press Read with AI.');
  };
  img.onerror = () => toast('That image could not be opened.');
  img.src = URL.createObjectURL(file);
}

// drawPhoto renders the source at the current rotation. Rotating on a canvas
// (rather than with CSS) keeps the layout and scrolling right. Large photos
// are scaled down: 2000 px on the long side is plenty to read tiles.
function drawPhoto() {
  const src = photo.source;
  const scale = Math.min(1, 2000 / Math.max(src.naturalWidth, src.naturalHeight));
  const w = Math.round(src.naturalWidth * scale);
  const hgt = Math.round(src.naturalHeight * scale);
  const sideways = photo.turns % 2 === 1;
  const canvas = document.createElement('canvas');
  canvas.width = sideways ? hgt : w;
  canvas.height = sideways ? w : hgt;
  const ctx = canvas.getContext('2d');
  ctx.translate(canvas.width / 2, canvas.height / 2);
  ctx.rotate((photo.turns * Math.PI) / 2);
  ctx.drawImage(src, -w / 2, -hgt / 2, w, hgt);
  photo.data = canvas.toDataURL('image/jpeg', 0.9).split(',')[1]; // base64, as rotated
  canvas.toBlob((blob) => {
    if (photo.url) URL.revokeObjectURL(photo.url);
    photo.url = URL.createObjectURL(blob);
    $('photo-img').src = photo.url;
  }, 'image/jpeg', 0.9);
}

function removePhoto() {
  if (photo.url) URL.revokeObjectURL(photo.url);
  if (photo.source) URL.revokeObjectURL(photo.source.src);
  photo.url = '';
  photo.source = null;
  $('photo-img').removeAttribute('src');
  $('photo').hidden = true;
  $('photo').classList.remove('big', 'zoomed');
  $('photo-size').textContent = 'Bigger';
  $('photo-zoom').textContent = 'Zoom in';
}

// -------------------------------------------------------------- AI import

// "Read with AI" sends the photo to a vision model using the visitor's own
// API key, straight from the browser. The key's prefix picks the provider:
// Anthropic keys start with "sk-ant-", anything else is treated as Gemini.
const AI_MODELS = { anthropic: 'claude-opus-5-5', gemini: 'gemini-3.8-flash' };

const AI_PROMPT = `This photo shows a 2-player game of Azul in progress. Read the position and reply with JSON only, no other text.

Colors are exactly: "blue", "yellow", "red", "black", "white" (call dark tiles "black", including dark green or teal ones).

{
  "toMove": 0,
  "tokenInCenter": true,
  "nextFirst": 0,
  "factories": [[], [], [], [], []],
  "center": [],
  "players": [
    {"score": 0, "wall": [".....", ".....", ".....", ".....", "....."], "lines": [{}, {}, {}, {}, {}], "floor": 0},
    {"score": 0, "wall": [".....", ".....", ".....", ".....", "....."], "lines": [{}, {}, {}, {}, {}], "floor": 0}
  ]
}

- factories: the 5 round factory displays, each a list of the tile colors on it (0 to 4 tiles).
- center: the loose tiles in the middle of the table. tokenInCenter is true if the first-player marker (the "1" tile) is still there; if a player has it, set it false and nextFirst to that player's index.
- players: index 0 is the board nearest the bottom of the photo (or the left one), index 1 the other.
- wall: the 5x5 grid on the right of a board, top row first, 5 characters per row: "x" where a tile has been placed, "." where the square is empty (a printed pattern with no tile on it is empty).
- lines: the 5 staircase rows on the left of a board, top (1 space) to bottom (5 spaces). Use {"color": "red", "count": 2} for a row holding tiles and {} for an empty row.
- floor: how many spaces of the bottom penalty row are occupied, counting the first-player marker.
- score: the score marked on that board's track, or 0 if you cannot tell.
- toMove: 0 unless the photo makes clear whose turn it is.

If something is hidden or unclear, give your best guess; a person will check it.`;

async function aiFetch(url, headers, body) {
  const res = await fetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json', ...headers }, body: JSON.stringify(body) });
  const data = await res.json().catch(() => ({}));
  const message = (data.error && data.error.message) || '';
  // Gemini reports a bad key as a 400 whose message names the API key.
  if (res.status === 401 || res.status === 403 || /api key/i.test(message)) {
    store.set('azul.aikey', '');
    throw new Error('the key was rejected, so it has been forgotten. Press Read with AI to enter another.');
  }
  if (!res.ok) throw new Error(message || `the provider answered ${res.status}`);
  return data;
}

async function askAnthropic(key, image) {
  const data = await aiFetch('https://api.anthropic.com/v1/messages', {
    'x-api-key': key,
    'anthropic-version': '2023-06-01',
    'anthropic-dangerous-direct-browser-access': 'true', // the key is the visitor's own
  }, {
    model: AI_MODELS.anthropic,
    max_tokens: 16000,
    messages: [{ role: 'user', content: [
      { type: 'image', source: { type: 'base64', media_type: 'image/jpeg', data: image } },
      { type: 'text', text: AI_PROMPT },
    ] }],
  });
  const block = (data.content || []).find((b) => b.type === 'text');
  if (!block) throw new Error(data.stop_reason === 'refusal' ? 'the model declined to read this photo' : 'the model sent no answer');
  return block.text;
}

async function askGemini(key, image) {
  const data = await aiFetch(`https://generativelanguage.googleapis.com/v1beta/models/${AI_MODELS.gemini}:generateContent`, {
    'x-goog-api-key': key,
  }, {
    contents: [{ parts: [{ inline_data: { mime_type: 'image/jpeg', data: image } }, { text: AI_PROMPT }] }],
    generationConfig: { responseMimeType: 'application/json' },
  });
  const parts = (((data.candidates || [])[0] || {}).content || {}).parts || [];
  const text = parts.map((p) => p.text || '').join('');
  if (!text) throw new Error('the model sent no answer');
  return text;
}

// aiState turns whatever the model sent into a well-formed draft: right
// shapes, known colors, counts in range. The engine still checks the result
// like any other edit, and the person checks it against the photo.
function aiState(s) {
  const colors = (a) => (Array.isArray(a) ? a.filter((c) => COLORS.includes(c)) : []);
  const player = (p) => ({
    score: Math.max(0, (p && p.score) | 0),
    wall: Array.from({ length: 5 }, (_, r) => Array.from({ length: 5 }, (_, c) => {
      const ch = String(((p && p.wall) || [])[r] || '')[c];
      return ch && ch !== '.' && ch !== ' ' ? 'x' : '.';
    }).join('')),
    lines: Array.from({ length: 5 }, (_, r) => {
      const l = ((p && p.lines) || [])[r] || {};
      return COLORS.includes(l.color) && l.count > 0 ? { color: l.color, count: Math.min(l.count | 0, r + 1) } : {};
    }),
    floor: Math.min(7, Math.max(0, (p && p.floor) | 0)),
  });
  const players = Array.isArray(s.players) ? s.players : [];
  return {
    toMove: s.toMove === 1 ? 1 : 0,
    nextFirst: s.nextFirst === 1 ? 1 : 0,
    tokenInCenter: s.tokenInCenter !== false,
    factories: Array.from({ length: 5 }, (_, i) => colors((s.factories || [])[i]).slice(0, 4)),
    center: colors(s.center),
    players: [player(players[0]), player(players[1])],
  };
}

async function readPhotoWithAI() {
  let key = store.get('azul.aikey');
  if (!key) {
    key = (prompt('Paste your own API key (Google Gemini or Anthropic).\n\nIt is saved only in this browser. The key and the photo are sent only to that provider.') || '').trim();
    if (!key) return;
    store.set('azul.aikey', key);
  }
  const btn = $('photo-ai');
  btn.disabled = true;
  btn.textContent = 'Reading…';
  try {
    const text = await (key.startsWith('sk-ant-') ? askAnthropic : askGemini)(key, photo.data);
    let parsed;
    try {
      parsed = JSON.parse(text.slice(text.indexOf('{'), text.lastIndexOf('}') + 1));
    } catch {
      throw new Error('the model did not describe a board. Try a clearer photo.');
    }
    const state = aiState(parsed);
    if (!ui.edit) toggleEdit();
    ui.bagManual = false;
    ui.draft = state;
    edited();
    toast('Filled in from the photo. Check every tile against it: AI makes mistakes.');
  } catch (e) {
    toast('AI import failed: ' + e.message);
  } finally {
    btn.disabled = false;
    btn.textContent = 'Read with AI';
  }
}

// --------------------------------------------------------------- motion

// Tiles travel the way they would on a real table: from the factory to the
// pattern line (or floor), the rest of the factory slides to the center, a
// fresh deal drops into the factories and scored tiles set into the wall.
// The board is re-rendered from scratch, so each animation measures where
// tiles were before, then flies copies to where the new render put them.
// Everything is skipped when the visitor asks for reduced motion.
const motion = {
  lastAnalysis: null,

  on() {
    return !matchMedia('(prefers-reduced-motion: reduce)').matches;
  },

  // parse reads "F2 red->3" or "C blue->floor".
  parse(text) {
    const m = /^(C|F(\d)) (\w+)->(\d|floor)$/.exec(text);
    return m && { factory: m[2] ? +m[2] - 1 : -1, color: m[3], line: m[4] === 'floor' ? -1 : +m[4] - 1 };
  },

  // capture records where the move's tiles are now, before the board changes.
  capture(text) {
    const mv = this.parse(text);
    if (!mv || !this.on()) return null;
    const rect = (el) => ({ color: [...el.classList].find((c) => c.startsWith('t-')).slice(2), r: el.getBoundingClientRect() });
    if (mv.factory >= 0) {
      const all = [...document.querySelectorAll(`#table [data-f="${mv.factory}"]`)].map(rect);
      return { taken: all.filter((t) => t.color === mv.color), rest: all.filter((t) => t.color !== mv.color) };
    }
    const taken = [...document.querySelectorAll(`#table [data-c="${mv.color}"]`)].map(rect);
    const token = document.querySelector('#table [data-c="token"]');
    return { taken, rest: [], token: token && token.getBoundingClientRect() };
  },

  // move flies the captured tiles to where the new board shows them.
  move(text, before, after, from) {
    if (!from) return;
    const mv = this.parse(text);
    const p = before.toMove;
    const pairs = [];
    const fb = before.players[p].floor;
    const fa = after.players[p].floor;
    const floorTiles = [...document.querySelectorAll(`.pfloor[data-p="${p}"] .fslot .tile`)].slice(fb, fa);
    let floorQueue = floorTiles;
    if (from.token && floorQueue.length) {
      pairs.push([from.token, floorQueue[0]]);
      floorQueue = floorQueue.slice(1);
    }
    let dests = [];
    if (mv.line >= 0) {
      const placed = (after.players[p].lines[mv.line].count || 0) - (before.players[p].lines[mv.line].count || 0);
      const filled = [...document.querySelectorAll(`.prow[data-p="${p}"][data-r="${mv.line}"] .tile`)];
      dests = filled.slice(0, placed);
    }
    dests = dests.concat(floorQueue);
    from.taken.forEach((t, i) => { if (dests[i]) pairs.push([t.r, dests[i]]); });
    // The rest of the factory joins the center.
    const byColor = {};
    from.rest.forEach((t) => { (byColor[t.color] = byColor[t.color] || []).push(t.r); });
    for (const [color, rects] of Object.entries(byColor)) {
      const inCenter = [...document.querySelectorAll(`#table [data-c="${color}"]`)].slice(-rects.length);
      rects.forEach((r, i) => { if (inCenter[i]) pairs.push([r, inCenter[i]]); });
    }
    pairs.forEach(([r, el], i) => this.fly(r, el, i * 45));
  },

  // fly animates a copy of el from rect r to el's place, on an arc.
  fly(r, el, delay) {
    const to = el.getBoundingClientRect();
    if (!to.width || !r.width) return;
    const ghost = el.cloneNode(true);
    ghost.classList.add('flying');
    ghost.removeAttribute('id');
    Object.assign(ghost.style, { left: to.left + 'px', top: to.top + 'px', width: to.width + 'px', height: to.height + 'px' });
    document.body.append(ghost);
    const dx = r.left - to.left;
    const dy = r.top - to.top;
    const s = r.width / to.width;
    const lift = Math.min(60, 20 + Math.hypot(dx, dy) * 0.12);
    const turn = (dx > 0 ? -1 : 1) * 12;
    const duration = 520;
    ghost.animate([
      { transform: `translate(${dx}px, ${dy}px) scale(${s})` },
      { transform: `translate(${dx / 2}px, ${dy / 2 - lift}px) scale(${(s + 1) / 2 * 1.08}) rotate(${turn}deg)`, offset: 0.5 },
      { transform: 'none' },
    ], { duration, delay, easing: 'cubic-bezier(.45, 0, .2, 1)', fill: 'backwards' }).finished
      .then(() => ghost.remove(), () => ghost.remove());
    // A hidden tab pauses animations; never leave a copy behind.
    setTimeout(() => ghost.remove(), duration + delay + 1500);
    el.animate([{ opacity: 0 }, { opacity: 0, offset: 0.98 }, { opacity: 1 }], { duration: duration + delay });
    el.animate([{ transform: 'scale(1)' }, { transform: 'scale(1.12)' }, { transform: 'scale(1)' }],
      { duration: 220, delay: duration + delay, easing: 'ease-out' });
  },

  // deal drops the factories' new tiles in, one after another.
  deal() {
    if (!this.on()) return;
    document.querySelectorAll('#table .factory').forEach((fac, f) => {
      fac.animate([{ transform: 'scale(.9)', opacity: 0.4 }, { transform: 'none', opacity: 1 }],
        { duration: 400, delay: f * 60, easing: 'cubic-bezier(.2, .8, .2, 1)', fill: 'backwards' });
      fac.querySelectorAll('.tile').forEach((t, k) => {
        t.animate([
          { transform: 'translateY(-18px) rotate(-20deg) scale(.5)', opacity: 0 },
          { transform: 'translateY(2px) rotate(4deg) scale(1.04)', opacity: 1, offset: 0.7 },
          { transform: 'none', opacity: 1 },
        ], { duration: 460, delay: 120 + f * 60 + k * 45, easing: 'cubic-bezier(.2, .8, .2, 1)', fill: 'backwards' });
      });
    });
  },

  // score sets newly placed wall tiles into place and counts the scores up.
  score(before, after) {
    if (!this.on()) return;
    let n = 0;
    after.players.forEach((pl, p) => {
      pl.wall.forEach((row, r) => {
        [...row].forEach((ch, col) => {
          if (ch === '.' || before.players[p].wall[r][col] !== '.') return;
          const el = document.querySelector(`[data-wall="${p}-${r}-${col}"]`);
          if (!el) return;
          el.animate([
            { transform: 'translateX(-14px) scale(1.5)', opacity: 0 },
            { transform: 'scale(.92)', opacity: 1, offset: 0.7 },
            { transform: 'none', opacity: 1 },
          ], { duration: 420, delay: 80 + n++ * 70, easing: 'cubic-bezier(.2, .8, .2, 1)', fill: 'backwards' });
        });
      });
      this.count(document.querySelector(`.pscore[data-p="${p}"]`), before.players[p].score, pl.score, 300 + n * 70);
    });
  },

  // count runs a score from a to b.
  count(el, a, b, delay = 0) {
    if (!el || a === b) return;
    const start = performance.now() + delay;
    const dur = 700;
    const step = (now) => {
      const t = Math.min(1, Math.max(0, (now - start) / dur));
      const e = 1 - Math.pow(1 - t, 3);
      el.textContent = String(Math.round(a + (b - a) * e));
      if (t < 1) requestAnimationFrame(step);
    };
    el.textContent = String(a);
    requestAnimationFrame(step);
  },

  // stagger brings a list in one item after another.
  stagger(items) {
    if (!this.on()) return;
    [...items].forEach((el, i) => el.animate(
      [{ transform: 'translateY(8px)', opacity: 0 }, { transform: 'none', opacity: 1 }],
      { duration: 380, delay: i * 70, easing: 'cubic-bezier(.2, .8, .2, 1)', fill: 'backwards' }));
  },
};

// ------------------------------------------------------------- rendering

function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === false || v == null) continue;
    if (k === 'class') el.className = v;
    else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else el.setAttribute(k, v === true ? '' : v);
  }
  for (const c of children.flat()) {
    if (c == null || c === false) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return el;
}

function tile(color, cls = '', attrs = {}) {
  const label = color === 'token' ? 'first player token' : color === 'floor' ? 'floor tile' : color;
  return h('div', { class: `tile t-${color} ${cls}`, title: label, 'aria-label': label, ...attrs }, color === 'token' ? '1' : null);
}

// shown returns the position the board displays: a step of the open line,
// or the current position.
function shown() {
  if (ui.pv) return ui.pv.positions[ui.pv.step];
  if (ui.edit) return { state: ui.draft, roundOver: false, gameOver: false };
  return ui.pos;
}

// hintMove is the move to highlight: the next move of the open line.
function hintMove() {
  if (!ui.pv) return null;
  const next = ui.pv.positions[ui.pv.step + 1];
  return next && next.move ? next.move : null;
}

function render() {
  if (!ui.pos) return;
  const pos = shown();
  renderEvalBar();
  renderEditBar();
  renderTable(pos.state);
  renderPlayers(pos);
  renderStatus(pos);
  renderAnalysis();
  renderPV();
  renderHistory();
  $('btn-undo').disabled = !ui.history.length || ui.edit || !!ui.pv;
  $('btn-vs').setAttribute('aria-pressed', !!ui.vs);
  $('btn-vs').textContent = ui.vs ? 'Stop playing' : 'Play vs engine';
  $('btn-edit').disabled = !!ui.vs;
  $('btn-edit').setAttribute('aria-pressed', ui.edit);
  $('btn-edit').textContent = ui.edit ? 'Done editing' : 'Edit board';
  $('btn-analyze').disabled = ui.edit || ui.pos.roundOver || ui.pos.gameOver;
}

// p1View converts an engine line's eval (for the side to move) into the
// eval bar: player 1's share, who leads, and by how much.
function p1View(line, toMove) {
  if (line.outcome) {
    const p1wins = (line.outcome === 'win') === (toMove === 0);
    return { frac: p1wins ? 1 : 0, leader: p1wins ? 0 : 1, text: 'wins' };
  }
  const v = toMove === 0 ? line.eval : -line.eval;
  return { frac: 0.5 + 0.5 * Math.tanh(v / 12), leader: v >= 0 ? 0 : 1, text: '+' + Math.abs(v).toFixed(1) };
}

function renderEvalBar() {
  const bar = $('evalbar');
  const pos = ui.pos;
  let frac = 0.5;
  let left = 'P1';
  let right = 'P2';
  if (pos.gameOver) {
    const s = pos.state.players;
    frac = pos.winner === 0 ? 1 : pos.winner === 1 ? 0 : 0.5;
    left = `P1 ${s[0].score}`;
    right = `P2 ${s[1].score}`;
  } else if (ui.analysis) {
    const v = p1View(ui.analysis, pos.state.toMove);
    frac = v.frac;
    if (v.leader === 0) left += ' ' + v.text;
    else right += ' ' + v.text;
  } else if (ui.thinking) {
    left = 'P1 …';
  }
  // A strip of tiles: player 1's share from the left. Segments keep their
  // elements between renders, so a change sweeps across them.
  const SEGS = 24;
  const n1 = Math.round(frac * SEGS);
  let track = bar.querySelector('.track');
  if (!track) {
    track = h('div', { class: 'track', role: 'meter', 'aria-valuemin': 0, 'aria-valuemax': 100, 'aria-label': 'Evaluation, player 1 share' });
    for (let i = 0; i < SEGS; i++) track.append(h('i', { class: 'seg' + (i === SEGS / 2 ? ' mid' : '') }));
  }
  track.setAttribute('aria-valuenow', Math.round(frac * 100));
  [...track.children].forEach((seg, i) => {
    const want = i < n1;
    if (seg.classList.contains('p1') === want) return;
    // Sweep outward from the boundary the bar is moving towards.
    seg.style.transitionDelay = Math.abs(i - n1) * 18 + 'ms';
    seg.classList.toggle('p1', want);
  });
  bar.replaceChildren(
    h('span', { class: 'label p1c' }, left.trim()),
    track,
    h('span', { class: 'label right p2c' }, right.trim()),
  );
}

function renderEditBar() {
  const bar = $('editbar');
  bar.hidden = !ui.edit;
  if (!ui.edit) return;
  const d = ui.draft;
  const brushes = h('div', { class: 'brushes', role: 'group', 'aria-label': 'Brush' },
    COLORS.map((c) => h('button', { class: 'brush', 'aria-pressed': ui.brush === c, title: c, onclick: () => { ui.brush = c; render(); } }, tile(c))),
    h('button', { class: 'brush', 'aria-pressed': ui.brush === 'erase', title: 'eraser', onclick: () => { ui.brush = 'erase'; render(); } },
      h('div', { class: 'tile empty erase' }, '✕')),
  );
  const tokenSel = h('select', { onchange: (e) => {
    const v = e.target.value;
    d.tokenInCenter = v === 'center';
    if (v !== 'center') d.nextFirst = +v;
    edited();
  } },
  h('option', { value: 'center', selected: d.tokenInCenter }, 'in center'),
  h('option', { value: '0', selected: !d.tokenInCenter && d.nextFirst === 0 }, 'P1 has it'),
  h('option', { value: '1', selected: !d.tokenInCenter && d.nextFirst === 1 }, 'P2 has it'));
  const moveSel = h('select', { onchange: (e) => { d.toMove = +e.target.value; edited(); } },
    h('option', { value: '0', selected: d.toMove === 0 }, 'P1'),
    h('option', { value: '1', selected: d.toMove === 1 }, 'P2'));
  const bag = h('div', { class: 'bagrow' },
    h('label', {}, h('input', { type: 'checkbox', checked: ui.bagManual, onchange: (e) => { ui.bagManual = e.target.checked; edited(); } }), 'Set bag/lid by hand'),
    ['bag', 'lid'].map((which) => h('span', { class: 'bagrow' }, which + ':',
      COLORS.map((c) => {
        const v = (d[which] && d[which][c]) || 0;
        return ui.bagManual
          ? h('label', {}, tile(c), h('input', { type: 'number', min: 0, max: 20, value: v, 'aria-label': `${which} ${c}`,
            onchange: (e) => { d[which] = { ...(d[which] || {}), [c]: +e.target.value }; edited(); } }))
          : h('span', {}, tile(c), ' ', v);
      }))),
  );
  bar.replaceChildren(
    brushes,
    h('label', {}, 'To move', moveSel),
    h('label', {}, 'First-player token', tokenSel),
    h('span', { class: 'meta' }, 'The token counts as one floor slot.'),
    bag,
  );
}

function renderTable(state) {
  const table = $('table');
  table.replaceChildren();
  const hint = hintMove();
  const canPick = !ui.edit && !ui.pv && !ui.pos.gameOver;
  state.factories.forEach((tiles, f) => {
    const fac = h('div', { class: 'factory', role: 'group', 'aria-label': `Factory ${f + 1}` }, h('span', { class: 'num' }, f + 1));
    const slots = ui.edit ? 4 : tiles.length;
    for (let k = 0; k < slots; k++) {
      const c = tiles[k];
      if (!c) {
        fac.append(h('div', { class: 'tile empty editable', onclick: () => editFactory(f, k), title: 'empty slot' }));
        continue;
      }
      let cls = '';
      if (ui.sel && ui.sel.src === f && ui.sel.color === c) cls += ' sel';
      if (hint && hint.source === 'factory' && hint.factory === f + 1 && hint.color === c) cls += ' hint-src';
      if (canPick || ui.edit) cls += ' clickable';
      fac.append(tile(c, cls, {
        'data-f': f,
        role: 'button',
        tabindex: canPick || ui.edit ? 0 : -1,
        onclick: () => (ui.edit ? editFactory(f, k) : pick(f, c)),
        onkeydown: (e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); ui.edit ? editFactory(f, k) : pick(f, c); } },
      }));
    }
    table.append(fac);
  });

  const center = h('div', { class: 'center', role: 'group', 'aria-label': 'Center' }, h('span', { class: 'num' }, 'center'));
  if (state.tokenInCenter) center.append(tile('token', '', { 'data-c': 'token' }));
  const sorted = [...state.center].sort((a, b) => COLORS.indexOf(a) - COLORS.indexOf(b));
  sorted.forEach((c) => {
    let cls = '';
    if (ui.sel && ui.sel.src === 'C' && ui.sel.color === c) cls += ' sel';
    if (hint && hint.source === 'center' && hint.color === c) cls += ' hint-src';
    if (canPick || ui.edit) cls += ' clickable';
    center.append(tile(c, cls, {
      'data-c': c,
      role: 'button',
      tabindex: canPick || ui.edit ? 0 : -1,
      onclick: () => (ui.edit ? editCenterRemove(c) : pick('C', c)),
      onkeydown: (e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); ui.edit ? editCenterRemove(c) : pick('C', c); } },
    }));
  });
  if (ui.edit && ui.brush !== 'erase') {
    center.append(h('button', { class: 'add', onclick: () => { ui.draft.center.push(ui.brush); edited(); } }, '+ ' + ui.brush));
  } else if (!sorted.length && !state.tokenInCenter) {
    center.append(h('span', { class: 'hint' }, 'empty'));
  }
  table.append(center);
}

function pick(src, color) {
  if (ui.pos.roundOver || (ui.vs && ui.pos.state.toMove !== ui.vs.human)) return;
  ui.sel = ui.sel && ui.sel.src === src && ui.sel.color === color ? null : { src, color };
  render();
}

function selText(dst) {
  const src = ui.sel.src === 'C' ? 'C' : 'F' + (ui.sel.src + 1);
  return `${src} ${ui.sel.color}->${dst}`;
}

// lineLegal mirrors the engine's rule, only to highlight targets; the
// server still validates every move.
function lineLegal(p, r, color) {
  const l = p.lines[r];
  const onWall = p.wall[r][(COLORS.indexOf(color) + r) % 5] !== '.';
  if (!l.count) return !onWall;
  return l.color === color && l.count < r + 1;
}

function renderPlayers(pos) {
  const wrap = $('players');
  wrap.replaceChildren();
  const s = pos.state;
  const hint = hintMove();
  const hintPlayer = ui.pv && hint ? ui.pv.positions[ui.pv.step].state.toMove : -1;
  s.players.forEach((p, i) => {
    const toMove = !pos.roundOver && !pos.gameOver && s.toMove === i;
    const choosing = toMove && ui.sel && !ui.edit && !ui.pv;
    const hasToken = !s.tokenInCenter && s.nextFirst === i;
    const scoreEl = ui.edit
      ? h('input', { type: 'number', min: 0, max: 500, value: p.score, 'aria-label': `P${i + 1} score`, onchange: (e) => { ui.draft.players[i].score = Math.max(0, +e.target.value | 0); edited(); } })
      : String(p.score);
    const board = h('div', { class: `pboard p${i + 1}b` + (toMove ? ' tomove' : '') },
      h('div', { class: 'phead' },
        h('span', { class: `pname p${i + 1}` }, `Player ${i + 1}`),
        toMove ? h('span', { class: 'badge on' }, 'to move') : null,
        hasToken ? h('span', { class: 'badge' }, 'first player') : null,
        h('span', { class: 'pscore', 'data-p': i }, scoreEl)),
    );

    const lines = h('div', { class: 'plines', role: 'group', 'aria-label': `Player ${i + 1} pattern lines` });
    const wall = h('div', { class: 'pwall', role: 'group', 'aria-label': `Player ${i + 1} wall` });
    for (let r = 0; r < 5; r++) {
      const l = p.lines[r] || {};
      const count = l.count || 0;
      const legal = choosing && lineLegal(p, r, ui.sel.color);
      const target = hintPlayer === i && hint && !hint.floor && hint.line === r + 1;
      const row = h('div', { class: 'prow' + (legal ? ' legal' : '') + (target ? ' target' : ''), 'data-p': i, 'data-r': r });
      for (let j = 0; j < 5; j++) {
        if (j < 4 - r) {
          row.append(h('div', { class: 'lcell' }));
          continue;
        }
        const k = j - (4 - r); // 0..r from the left
        const filled = k >= r + 1 - count;
        const onClick = ui.edit ? () => editLine(i, r, k) : legal ? () => playMove(selText(r + 1)) : null;
        row.append(filled
          ? tile(l.color, onClick ? 'clickable' : '', { onclick: onClick })
          : h('div', { class: 'lcell slot' + (ui.edit ? ' editable' : ''), onclick: onClick, title: `line ${r + 1}` }));
      }
      lines.append(row);
      for (let col = 0; col < 5; col++) {
        const on = p.wall[r][col] !== '.';
        wall.append(tile(wallColor(r, col), (on ? '' : 'ghost') + (ui.edit ? ' clickable' : ''), {
          'data-wall': `${i}-${r}-${col}`,
          onclick: ui.edit ? () => editWall(i, r, col) : null,
          'aria-label': `${wallColor(r, col)} ${on ? 'placed' : 'empty'}`,
        }));
      }
    }
    board.append(h('div', { class: 'pgrid' }, lines, wall));

    const floorLegal = choosing;
    const floorTarget = hintPlayer === i && hint && hint.floor;
    const floor = h('div', { class: 'pfloor' + (floorLegal ? ' legal' : '') + (floorTarget ? ' target' : ''), 'data-p': i, role: 'group', 'aria-label': `Player ${i + 1} floor` });
    for (let k = 0; k < 7; k++) {
      const onClick = ui.edit ? () => editFloor(i, k) : floorLegal ? () => playMove(selText('floor')) : null;
      floor.append(h('div', { class: 'fslot', onclick: onClick },
        k < p.floor ? tile(hasToken && k === 0 ? 'token' : 'floor') : h('div', { class: 'tile empty' }),
        h('span', { class: 'pen' }, FLOOR_PEN[k])));
    }
    board.append(floor);
    wrap.append(board);
  });
}

function renderStatus(pos) {
  const st = $('status');
  st.replaceChildren();
  if (ui.error) st.append(h('span', { class: 'err' }, ui.error));
  if (ui.edit) {
    st.append(ui.draftErr
      ? h('span', { class: 'err' }, 'Not a valid position yet: ' + ui.draftErr)
      : h('span', {}, 'Editing. Pick a brush, then click factory slots, line cells, wall cells or floor slots.'));
    return;
  }
  if (ui.pv) {
    const p = ui.pv.positions[ui.pv.step];
    if (p.action === 'score') st.append(h('span', {}, 'End of round: walls tiled and scored.'));
    return;
  }
  if (ui.vs && pos.gameOver) {
    const s = pos.state.players;
    const me = ui.vs.human;
    const verdict = pos.winner === -1 ? 'A shared victory' : pos.winner === me ? 'You win' : 'The engine wins';
    st.append(h('strong', {}, `${verdict}: ${s[me].score} to ${s[1 - me].score}.`), ' ',
      h('button', { class: 'primary', onclick: () => { newDeal(); } }, 'Play again'), ' ',
      h('button', { onclick: reviewGame }, 'Review game'));
    return;
  }
  if (ui.vs && !pos.roundOver) {
    st.append(pos.state.toMove === ui.vs.human
      ? (ui.sel ? `Place ${ui.sel.color}: click a highlighted line or the floor.` : `You are Player ${ui.vs.human + 1}. Your move: click tiles, then a line.`)
      : 'The engine is thinking…');
    return;
  }
  if (pos.gameOver) {
    const s = pos.state.players;
    const who = pos.winner === -1 ? 'Shared victory' : `Player ${pos.winner + 1} wins`;
    st.append(h('strong', {}, `${who} — ${s[0].score} : ${s[1].score}`));
  } else if (pos.roundOver) {
    const scored = pos.action === 'score';
    st.append(scored ? 'Round scored. ' : 'Round over. ',
      h('button', { class: scored ? '' : 'primary', onclick: () => roundAction('score') }, 'Score round'),
      h('button', { class: scored ? 'primary' : '', onclick: () => roundAction('deal') }, 'Deal next round'),
      h('span', { class: 'meta' }, 'Or use Edit board to enter the real deal.'));
  } else if (ui.sel) {
    st.append(`Place ${ui.sel.color} from ${ui.sel.src === 'C' ? 'the center' : 'factory ' + (ui.sel.src + 1)}: click a highlighted line or the floor. Esc cancels.`);
  } else {
    st.append(`Player ${pos.state.toMove + 1} to move. Click tiles to play, or pick an engine line to step through it.`);
  }
}

function describe(m) {
  const src = m.source === 'center' ? 'Center' : 'F' + m.factory;
  return [h('span', { class: `dot t-${m.color}` }), `${src} ${m.color} → ${m.floor ? 'floor' : 'line ' + m.line}`];
}

function chips(m) {
  const e = m.effect;
  const out = [];
  if (e.completeLine) out.push(h('span', { class: 'chip good', title: 'Points when this line is tiled at round end, judged on the current wall' }, `+${e.wallPoints} wall`));
  else if (e.placed) out.push(h('span', { class: 'chip' }, `${e.placed} into line ${m.line}`));
  if (e.bonus) out.push(h('span', { class: 'chip good', title: 'End-of-game bonus this tile completes' }, `+${e.bonus} bonus`));
  if (e.toFloor) out.push(h('span', { class: 'chip bad' }, `${e.toFloor} to floor`));
  if (e.token) out.push(h('span', { class: 'chip' }, 'takes first player'));
  if (e.floorDelta) out.push(h('span', { class: 'chip bad', title: 'Change in this round\'s floor penalty' }, `${e.floorDelta} floor`));
  return out;
}

function renderAnalysis() {
  const box = $('analysis');
  box.replaceChildren();
  const pos = ui.pos;
  if (ui.edit) return box.append(h('div', { class: 'meta' }, 'Finish editing to analyse.'));
  if (ui.vs) return box.append(h('div', { class: 'meta' }, 'Hidden while you play the engine. Stop playing to analyse.'));
  if (pos.gameOver) return box.append(h('div', { class: 'meta' }, 'The game is over.'));
  if (pos.roundOver) return box.append(h('div', { class: 'meta' }, 'The round is over: score it to continue.'));
  if (ui.analysisErr) return box.append(h('div', { class: 'err' }, ui.analysisErr));
  if (!ui.analysis) {
    return box.append(ui.thinking
      ? h('div', { class: 'thinking' }, h('span', { class: 'loader', 'aria-hidden': 'true' }, h('i'), h('i'), h('i'), h('i'), h('i')), 'Thinking…')
      : h('div', { class: 'meta' }, 'Press Analyze to see the best moves.'));
  }
  const a = ui.analysis;
  box.append(h('p', { class: 'reason' }, a.reason.charAt(0).toUpperCase() + a.reason.slice(1) + '.'));
  const lines = h('div', { class: 'lines' });
  a.lines.forEach((l, i) => {
    const pvText = l.pv.slice(1).join(', ');
    lines.append(h('button', { class: 'eline' + (ui.pv && ui.pv.line === i ? ' open' : ''), onclick: () => openLine(i) },
      h('span', { class: 'rank' }, i + 1),
      h('span', { class: 'mv' }, describe(l.move)),
      h('span', { class: 'ev' }, l.outcome ? l.evalText : (l.eval >= 0 ? '+' : '') + l.eval.toFixed(2)),
      h('span', { class: 'chips' }, i === 0 ? h('span', { class: 'chip best' }, 'best') : null, chips(l.move)),
      pvText ? h('span', { class: 'pvtext' }, 'then ' + pvText) : null,
    ));
  });
  box.append(lines);
  if (a !== motion.lastAnalysis) {
    motion.lastAnalysis = a;
    motion.stagger(lines.children);
  }
  box.append(h('div', { class: 'meta' },
    `${a.engine === 'neural' ? 'Neural network' : 'Classic'} · P${pos.state.toMove + 1}'s view · depth ${a.depth}${a.exact ? ', solved to round end' : ''} · ${(a.nodes / 1e6).toFixed(1)}M nodes in ${(a.timeMs / 1000).toFixed(1)} s`));
}

function renderPV() {
  const panel = $('pv-panel');
  panel.hidden = !ui.pv;
  if (!ui.pv) return;
  const { positions, step } = ui.pv;
  $('pv-pos').textContent = `${step} / ${positions.length - 1}`;
  $('pv-first').disabled = $('pv-prev').disabled = step === 0;
  $('pv-next').disabled = $('pv-last').disabled = step === positions.length - 1;
  $('btn-pv-play').disabled = step === 0;
  const steps = $('pv-steps');
  steps.replaceChildren();
  const mover = ui.pos.state.toMove;
  positions.forEach((p, i) => {
    if (i === 0) return;
    const label = p.move ? p.move.text : 'score';
    const who = p.move ? (positions[i - 1].state.toMove === mover ? 'me' : 'opp') : '';
    steps.append(h('button', { class: (i === step ? 'cur ' : '') + who, onclick: () => pvStep(i), title: p.move ? `P${positions[i - 1].state.toMove + 1}` : 'round end' }, label));
  });
}

function renderHistory() {
  const ol = $('history');
  ol.replaceChildren();
  const rv = ui.review && ui.review.history === ui.history ? ui.review : null;
  ui.history.forEach((e, i) => {
    const r = rv && rv.loss[i];
    ol.append(h('li', {}, h('button', { onclick: () => jumpTo(i), title: 'Go back to the position before this' }, e.label),
      r ? h('span', { class: `verdict ${verdict(r.loss).cls}`, title: `${r.worse ? 'at least ' : ''}${r.loss.toFixed(1)} points worse than the engine's choice` }, verdict(r.loss).text) : null));
  });
  ol.append(h('li', { class: 'now' }, ui.edit ? 'editing…' : 'current position'));

  const box = $('review');
  box.replaceChildren();
  const moves = ui.history.filter((e) => isMove(e.label)).length;
  if (!rv) {
    if (moves >= 2 && !ui.edit) box.append(h('button', { onclick: reviewGame, title: 'Score every move against the engine' }, 'Review game'));
    return;
  }
  if (rv.done < rv.total) {
    box.append(h('span', { class: 'meta' }, `Reviewing… ${rv.done} of ${rv.total} moves`));
    return;
  }
  // Average points lost per move, for each player.
  for (const p of [0, 1]) {
    const mine = rv.loss.filter((r) => r && r.mover === p);
    if (!mine.length) continue;
    const avg = mine.reduce((a, r) => a + r.loss, 0) / mine.length;
    const best = mine.filter((r) => r.loss < 0.5).length;
    box.append(h('span', { class: `rv-player p${p + 1}c` }, `P${p + 1}${ui.vs && ui.vs.human === p ? ' (you)' : ''}: ${avg.toFixed(1)} points lost per move, ${best} of ${mine.length} best`));
  }
}

const isMove = (label) => /^(C|F\d) \w+->/.test(label);

// verdict grades the points a move gave away against the engine's choice.
function verdict(loss) {
  if (loss < 0.5) return { text: 'best', cls: 'v-best' };
  if (loss < 2) return { text: 'good', cls: 'v-good' };
  if (loss < 5) return { text: 'mistake', cls: 'v-mistake' };
  return { text: 'blunder', cls: 'v-blunder' };
}

// reviewGame searches every position in the history and compares the move
// played with the engine's best: the difference in its values is what the
// move gave away. A move outside the engine's top eight is valued by
// searching the position it led to.
async function reviewGame() {
  const history = ui.history;
  const steps = history.map((e, i) => (isMove(e.label) ? i : -1)).filter((i) => i >= 0);
  ui.review = { history, done: 0, total: steps.length, loss: [] };
  render();
  for (const i of steps) {
    if (ui.review.history !== history || ui.history !== history) return; // the game moved on
    const { pos, label } = history[i];
    try {
      const r = await api('/bestmove', { state: pos.state, timeMs: 300, multiPV: 8, engine: $('engine').value });
      const played = r.lines.find((l) => l.move.text === label);
      let value = played && played.eval;
      let worse = false;
      if (!played) {
        const next = (history[i + 1] || { pos: ui.pos }).pos;
        if (next.roundOver) {
          value = r.lines[r.lines.length - 1].eval; // no search there: at least as bad as the eighth
          worse = true;
        } else {
          const after = await api('/bestmove', { state: next.state, timeMs: 300, multiPV: 1, engine: $('engine').value });
          value = next.state.toMove === pos.state.toMove ? after.eval : -after.eval;
        }
      }
      ui.review.loss[i] = { loss: Math.max(0, r.lines[0].eval - value), worse, mover: pos.state.toMove };
    } catch { /* leave this move unmarked */ }
    ui.review.done++;
    renderHistory();
  }
}

// ------------------------------------------------------------- edit ops

function editFactory(f, k) {
  if (!ui.edit) return;
  const list = ui.draft.factories[f];
  if (ui.brush === 'erase') {
    if (k < list.length) list.splice(k, 1);
  } else if (k < list.length) {
    list[k] = ui.brush;
  } else {
    list.push(ui.brush);
  }
  edited();
}

function editCenterRemove(c) {
  const i = ui.draft.center.indexOf(c);
  if (i >= 0) ui.draft.center.splice(i, 1);
  edited();
}

// Clicking cell k (from the left) of line r fills from that cell to the
// right end, which is how lines fill on the real board.
function editLine(p, r, k) {
  const lines = ui.draft.players[p].lines;
  const count = r + 1 - k;
  const l = lines[r] || {};
  if (ui.brush === 'erase' || (l.count === count && l.color === ui.brush)) lines[r] = {};
  else lines[r] = { color: ui.brush, count };
  edited();
}

function editWall(p, r, col) {
  const row = ui.draft.players[p].wall[r].split('');
  row[col] = row[col] === '.' ? 'x' : '.';
  ui.draft.players[p].wall[r] = row.join('');
  edited();
}

function editFloor(p, k) {
  const pl = ui.draft.players[p];
  pl.floor = pl.floor === k + 1 ? k : k + 1;
  edited();
}

// --------------------------------------------------------------- wiring

$('btn-new').addEventListener('click', newDeal);
$('btn-vs').addEventListener('click', toggleVs);
$('btn-undo').addEventListener('click', undo);
$('btn-edit').addEventListener('click', toggleEdit);
$('btn-json').addEventListener('click', openJSON);
$('btn-analyze').addEventListener('click', analyze);
$('think').addEventListener('change', () => {
  store.set('azul.think', $('think').value);
  if ($('auto').checked) analyze();
});
// The brain buttons drive the hidden select, which the rest of the page reads.
function syncBrain() {
  document.querySelectorAll('.brain .seg').forEach((b) => b.setAttribute('aria-pressed', b.dataset.engine === $('engine').value));
}
document.querySelectorAll('.brain .seg').forEach((b) => b.addEventListener('click', () => {
  if ($('engine').value === b.dataset.engine) return;
  $('engine').value = b.dataset.engine;
  $('engine').dispatchEvent(new Event('change'));
}));
$('engine').addEventListener('change', () => {
  syncBrain();
  store.set('azul.engine', $('engine').value);
  if ($('auto').checked) analyze();
});
$('btn-share').addEventListener('click', share);
$('btn-help').addEventListener('click', () => $('help-dialog').showModal());
$('examples').addEventListener('change', (e) => {
  if (e.target.value !== '') loadExample(+e.target.value);
  e.target.value = '';
});
$('intro-close').addEventListener('click', () => {
  $('intro').hidden = true;
  store.set('azul.intro', 'done');
});
$('opt-symbols').addEventListener('change', (e) => {
  applySymbols(e.target.checked);
  store.set('azul.symbols', e.target.checked ? '1' : '0');
});
window.addEventListener('hashchange', loadFromHash);
$('btn-photo').addEventListener('click', () => $('photo-file').click());
$('photo-change').addEventListener('click', () => $('photo-file').click());
$('photo-file').addEventListener('change', (e) => {
  showPhoto(e.target.files[0]);
  e.target.value = ''; // picking the same file again should work
});
$('photo-remove').addEventListener('click', removePhoto);
$('photo-ai').addEventListener('click', readPhotoWithAI);
$('ai-forget').addEventListener('click', () => {
  store.set('azul.aikey', '');
  toast('AI key forgotten.');
});
$('photo-rotate').addEventListener('click', () => {
  photo.turns = (photo.turns + 1) % 4;
  drawPhoto();
});
$('photo-size').addEventListener('click', (e) => {
  e.target.textContent = $('photo').classList.toggle('big') ? 'Smaller' : 'Bigger';
});
$('photo-zoom').addEventListener('click', (e) => {
  e.target.textContent = $('photo').classList.toggle('zoomed') ? 'Zoom out' : 'Zoom in';
});
// Dropping an image anywhere on the page works too.
document.addEventListener('dragover', (e) => e.preventDefault());
document.addEventListener('drop', (e) => {
  const file = e.dataTransfer && e.dataTransfer.files[0];
  if (file && file.type.startsWith('image/')) {
    e.preventDefault();
    showPhoto(file);
  }
});
$('json-load').addEventListener('click', loadJSON);
$('json-copy').addEventListener('click', async () => {
  try {
    await navigator.clipboard.writeText($('json-text').value);
    $('json-err').textContent = 'Copied.';
  } catch {
    $('json-text').select();
    $('json-err').textContent = 'Press Ctrl+C to copy.';
  }
});
$('pv-first').addEventListener('click', () => pvStep(0));
$('pv-prev').addEventListener('click', () => pvStep(ui.pv.step - 1));
$('pv-next').addEventListener('click', () => pvStep(ui.pv.step + 1));
$('pv-last').addEventListener('click', () => pvStep(Infinity));
$('btn-pv-close').addEventListener('click', () => { ui.pv = null; render(); });
$('btn-pv-play').addEventListener('click', pvPlay);

document.addEventListener('keydown', (e) => {
  if (e.target.closest('input, textarea, select, dialog')) return;
  if (ui.pv && e.key === 'ArrowLeft') { pvStep(ui.pv.step - 1); e.preventDefault(); }
  else if (ui.pv && e.key === 'ArrowRight') { pvStep(ui.pv.step + 1); e.preventDefault(); }
  else if (e.key === 'Escape') { ui.pv = null; ui.sel = null; render(); }
  else if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'z') { undo(); e.preventDefault(); }
  else if (e.key === '?') $('help-dialog').showModal();
});

(async () => {
  await setup();
  if (!(await loadFromHash())) await newDeal();
})();
