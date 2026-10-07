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
  edit: false,
  draft: null, // state being edited
  draftErr: '',
  draftDirty: false,
  brush: 'blue',
  bagManual: false,
  error: '',
};

// ---------------------------------------------------------------- server

async function api(path, body) {
  const res = await fetch(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  let data;
  try {
    data = await res.json();
  } catch {
    throw new Error(`${path}: ${res.status} ${res.statusText}`);
  }
  if (res.status === 429) throw new Error('Too many requests. Give it a few seconds and try again.');
  if (res.status === 503) throw new Error('The engine is busy right now. Try again in a moment.');
  if (!res.ok) throw new Error(data.error || res.statusText);
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
  if ($('auto').checked) analyze();
}

async function newDeal() {
  await run(async () => {
    const pos = await api('/new', {});
    ui.history = [];
    ui.pos = null;
    setPosition(pos);
  });
}

async function playMove(text) {
  await run(async () => {
    const r = await api('/apply', { state: ui.pos.state, moves: [text] });
    setPosition(r.positions[1], text);
  });
}

async function roundAction(action) {
  await run(async () => {
    const r = await api('/apply', { state: ui.pos.state, moves: [action] });
    setPosition(r.positions[1], action === 'score' ? 'scored round' : 'dealt next round');
  });
}

function undo() {
  if (ui.edit || ui.pv) return;
  const h = ui.history.pop();
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
  if (!pos || pos.roundOver || pos.gameOver) return;
  const id = ++ui.reqId;
  ui.thinking = true;
  ui.analysis = null;
  ui.analysisErr = '';
  render();
  try {
    const r = await api('/bestmove', { state: pos.state, timeMs: +$('think').value, multiPV: 3 });
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
  ui.pv.step = Math.max(0, Math.min(ui.pv.positions.length - 1, to));
  render();
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

function applyTheme(theme) {
  if (theme === 'light' || theme === 'dark') document.documentElement.dataset.theme = theme;
  else delete document.documentElement.dataset.theme;
}

function toggleTheme() {
  const dark = document.documentElement.dataset.theme
    ? document.documentElement.dataset.theme === 'dark'
    : matchMedia('(prefers-color-scheme: dark)').matches;
  const next = dark ? 'light' : 'dark';
  applyTheme(next);
  store.set('azul.theme', next);
}

function applySymbols(on) {
  document.body.classList.toggle('symbols', on);
  $('opt-symbols').checked = on;
}

// setup reads the server's limits and the visitor's saved preferences.
async function setup() {
  applyTheme(store.get('azul.theme'));
  applySymbols(store.get('azul.symbols') === '1');
  $('intro').hidden = store.get('azul.intro') === 'done';

  EXAMPLES.forEach((e, i) => $('examples').append(h('option', { value: i }, e.name)));

  let cfg = { defaultTimeMs: 1000, maxTimeMs: 30000, version: '' };
  try {
    const res = await fetch('/config');
    if (res.ok) cfg = await res.json();
  } catch { /* older server: keep the defaults */ }
  // Offer only search times this server will honour.
  const times = [300, 1000, 2000, 3000, 10000].filter((ms) => ms <= cfg.maxTimeMs);
  const saved = +store.get('azul.think');
  const pick = times.includes(saved) ? saved : times.reduce((a, b) => (Math.abs(b - cfg.defaultTimeMs) < Math.abs(a - cfg.defaultTimeMs) ? b : a));
  times.forEach((ms) => $('think').append(h('option', { value: ms, selected: ms === pick }, `${ms / 1000} s`)));
  if (cfg.version) $('version').textContent = 'build ' + cfg.version;
}

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
  bar.replaceChildren();
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
  const fill = h('div', { class: 'fill' });
  fill.style.width = (frac * 100).toFixed(1) + '%';
  bar.append(
    h('span', { class: 'label p1c' }, left.trim()),
    h('div', { class: 'track', role: 'meter', 'aria-valuemin': 0, 'aria-valuemax': 100, 'aria-valuenow': Math.round(frac * 100), 'aria-label': 'Evaluation, player 1 share' }, fill, h('div', { class: 'mid' })),
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
        role: 'button',
        tabindex: canPick || ui.edit ? 0 : -1,
        onclick: () => (ui.edit ? editFactory(f, k) : pick(f, c)),
        onkeydown: (e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); ui.edit ? editFactory(f, k) : pick(f, c); } },
      }));
    }
    table.append(fac);
  });

  const center = h('div', { class: 'center', role: 'group', 'aria-label': 'Center' }, h('span', { class: 'num' }, 'center'));
  if (state.tokenInCenter) center.append(tile('token'));
  const sorted = [...state.center].sort((a, b) => COLORS.indexOf(a) - COLORS.indexOf(b));
  sorted.forEach((c) => {
    let cls = '';
    if (ui.sel && ui.sel.src === 'C' && ui.sel.color === c) cls += ' sel';
    if (hint && hint.source === 'center' && hint.color === c) cls += ' hint-src';
    if (canPick || ui.edit) cls += ' clickable';
    center.append(tile(c, cls, {
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
  if (ui.pos.roundOver) return;
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
    const board = h('div', { class: 'pboard' + (toMove ? ' tomove' : '') },
      h('div', { class: 'phead' },
        h('span', { class: `pname p${i + 1}` }, `Player ${i + 1}`),
        toMove ? h('span', { class: 'badge on' }, 'to move') : null,
        hasToken ? h('span', { class: 'badge' }, 'first player') : null,
        h('span', { class: 'pscore' }, scoreEl)),
    );

    const lines = h('div', { class: 'plines', role: 'group', 'aria-label': `Player ${i + 1} pattern lines` });
    const wall = h('div', { class: 'pwall', role: 'group', 'aria-label': `Player ${i + 1} wall` });
    for (let r = 0; r < 5; r++) {
      const l = p.lines[r] || {};
      const count = l.count || 0;
      const legal = choosing && lineLegal(p, r, ui.sel.color);
      const target = hintPlayer === i && hint && !hint.floor && hint.line === r + 1;
      const row = h('div', { class: 'prow' + (legal ? ' legal' : '') + (target ? ' target' : '') });
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
          onclick: ui.edit ? () => editWall(i, r, col) : null,
          'aria-label': `${wallColor(r, col)} ${on ? 'placed' : 'empty'}`,
        }));
      }
    }
    board.append(h('div', { class: 'pgrid' }, lines, wall));

    const floorLegal = choosing;
    const floorTarget = hintPlayer === i && hint && hint.floor;
    const floor = h('div', { class: 'pfloor' + (floorLegal ? ' legal' : '') + (floorTarget ? ' target' : ''), role: 'group', 'aria-label': `Player ${i + 1} floor` });
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
  if (pos.gameOver) return box.append(h('div', { class: 'meta' }, 'The game is over.'));
  if (pos.roundOver) return box.append(h('div', { class: 'meta' }, 'The round is over: score it to continue.'));
  if (ui.analysisErr) return box.append(h('div', { class: 'err' }, ui.analysisErr));
  if (!ui.analysis) {
    return box.append(ui.thinking
      ? h('div', { class: 'thinking' }, 'Thinking…')
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
  box.append(h('div', { class: 'meta' },
    `P${pos.state.toMove + 1}'s view · depth ${a.depth}${a.exact ? ', solved to round end' : ''} · ${(a.nodes / 1e6).toFixed(1)}M nodes in ${(a.timeMs / 1000).toFixed(1)} s`));
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
  ui.history.forEach((e, i) => {
    ol.append(h('li', {}, h('button', { onclick: () => jumpTo(i), title: 'Go back to the position before this' }, e.label)));
  });
  ol.append(h('li', { class: 'now' }, ui.edit ? 'editing…' : 'current position'));
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
$('btn-undo').addEventListener('click', undo);
$('btn-edit').addEventListener('click', toggleEdit);
$('btn-json').addEventListener('click', openJSON);
$('btn-analyze').addEventListener('click', analyze);
$('think').addEventListener('change', () => {
  store.set('azul.think', $('think').value);
  if ($('auto').checked) analyze();
});
$('btn-share').addEventListener('click', share);
$('btn-theme').addEventListener('click', toggleTheme);
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
