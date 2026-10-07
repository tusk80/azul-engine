// ==UserScript==
// @name         Azul engine overlay
// @namespace    azul-engine
// @version      0.2.0
// @description  Shows the local Azul engine's suggested move on buddyboardgames.com. Alt+A toggles.
// @match        https://buddyboardgames.com/azul*
// @homepageURL  https://github.com/tusk80/azul-engine
// @downloadURL  https://raw.githubusercontent.com/tusk80/azul-engine/main/userscript/azul-overlay.user.js
// @updateURL    https://raw.githubusercontent.com/tusk80/azul-engine/main/userscript/azul-overlay.user.js
// @grant        GM_xmlhttpRequest
// @grant        unsafeWindow
// @connect      127.0.0.1
// @connect      localhost
// @run-at       document-idle
// ==/UserScript==

(function () {
  'use strict';

  // The engine on this computer (double-click azul.exe). To use a hosted
  // engine instead, change this and the @connect lines above; the server
  // must be started with -cors https://buddyboardgames.com.
  const ENGINE_URL = 'http://127.0.0.1:8765/bestmove';
  const THINK_MS = 1500;
  const POLL_MS = 400;
  const COLORS = ['blue', 'yellow', 'red', 'black', 'white'];

  let enabled = true;
  let lastKey = null; // JSON of the last state sent to the engine
  let result = null; // last engine response for lastKey
  let status = 'waiting for your turn';

  // --- state conversion ----------------------------------------------------

  const colorsOf = (tiles) => (tiles || []).map((t) => t && t.type).filter((t) => COLORS.includes(t));

  // toState converts the page's thisGame object into the engine's JSON
  // state, or returns null when there is nothing to analyse.
  function toState(g) {
    if (!g || g.gameState !== 'STARTED') return null;
    if (!Array.isArray(g.players) || g.players.length !== 2) return null;
    if (!Array.isArray(g.factories) || g.factories.length !== 5) return null;

    const factories = g.factories.map(colorsOf);
    const center = colorsOf(g.centerTiles);
    let tokenInCenter = (g.centerTiles || []).some((t) => t && t.type === 'firstPlayer');

    // A move is two clicks: choose tiles, then place them. While a group is
    // chosen it is held on the player; put it back where it came from so
    // the engine sees the position before the move.
    const mover = g.players[g.turnIdx];
    const chosen = colorsOf(mover && mover.tilesChosen);
    if (chosen.length) {
      const src = mover.centerChosen ? center : factories[mover.factoryChosen];
      if (src && !src.includes(chosen[0])) src.push(...chosen);
    }

    const holder = g.players.findIndex((p) => p.hasFirstPlayerToken);
    // Nobody holds the token and it is not in the center: it is part of a
    // chosen center group, so it is still up for grabs.
    if (holder < 0) tokenInCenter = true;

    const players = g.players.map((p) => ({
      score: Math.max(0, p.score | 0),
      wall: p.wall.rows.map((row) => row.map((c) => (c.selected ? 'x' : '.')).join('')),
      // Cells fill from either end depending on the render path, so count
      // selected cells instead of relying on their position.
      lines: p.patternLines.lines.map((cells) => {
        const filled = cells.filter((c) => c.selected && c.value && COLORS.includes(c.value.type));
        return filled.length ? { color: filled[0].value.type, count: filled.length } : {};
      }),
      floor: Math.min(7, p.floorLines.lines.filter((c) => c.selected).length),
    }));

    return {
      toMove: g.turnIdx,
      nextFirst: holder >= 0 ? holder : g.turnIdx,
      tokenInCenter,
      factories,
      center,
      players,
    };
  }

  // --- engine --------------------------------------------------------------

  function ask(state, key) {
    status = 'thinking…';
    render();
    GM_xmlhttpRequest({
      method: 'POST',
      url: ENGINE_URL,
      headers: { 'Content-Type': 'application/json' },
      data: JSON.stringify({ state, timeMs: THINK_MS }),
      timeout: THINK_MS + 5000,
      onload: (res) => {
        if (key !== lastKey) return; // the position moved on meanwhile
        let body;
        try {
          body = JSON.parse(res.responseText);
        } catch (e) {
          status = 'bad engine response';
          render();
          return;
        }
        if (res.status !== 200) {
          status = 'engine: ' + (body.error || res.status);
          result = null;
        } else {
          result = body;
          status = '';
        }
        render();
      },
      onerror: () => {
        status = 'engine not reachable — run: azul serve';
        render();
      },
      ontimeout: () => {
        status = 'engine timed out';
        render();
      },
    });
  }

  function tick() {
    if (!enabled) return;
    const g = unsafeWindow.thisGame;
    const myTurn = g && g.gameState === 'STARTED' && g.meIdx >= 0 && g.turnIdx === g.meIdx;
    const state = myTurn ? toState(g) : null;
    if (!state) {
      if (lastKey !== null) {
        lastKey = null;
        result = null;
        status = g && g.gameState === 'STARTED' ? 'waiting for your turn' : 'waiting for a 2-player game';
      }
      render();
      return;
    }
    const key = JSON.stringify(state);
    if (key !== lastKey) {
      lastKey = key;
      result = null;
      ask(state, key);
    }
    highlight();
  }

  // --- overlay -------------------------------------------------------------

  const css = document.createElement('style');
  css.textContent = `
    #azul-engine-panel { position: fixed; right: 16px; bottom: 16px; z-index: 99999; max-width: 320px;
      font: 13px/1.4 system-ui, sans-serif; color: #f5f5f5; background: rgba(20, 24, 32, .92);
      border-radius: 10px; padding: 10px 12px; box-shadow: 0 4px 16px rgba(0,0,0,.35); pointer-events: none; }
    #azul-engine-panel .move { font-size: 16px; font-weight: 600; }
    #azul-engine-panel .eval { font-variant-numeric: tabular-nums; }
    #azul-engine-panel .dim { opacity: .7; }
    .azul-engine-src { outline: 3px solid #22d3ee !important; outline-offset: 2px; border-radius: 4px;
      animation: azul-engine-pulse 1s ease-in-out infinite alternate; }
    .azul-engine-dst { outline: 3px solid #a3e635 !important; outline-offset: 1px; }
    @keyframes azul-engine-pulse { from { outline-color: #22d3ee; } to { outline-color: transparent; } }
  `;
  document.head.appendChild(css);

  const panel = document.createElement('div');
  panel.id = 'azul-engine-panel';
  document.body.appendChild(panel);

  function describe(m) {
    const src = m.source === 'center' ? 'Center' : 'Factory ' + m.factory;
    const dst = m.floor ? 'floor' : 'line ' + m.line;
    return `${src} · ${m.color} → ${dst}`;
  }

  function el(tag, cls, text) {
    const e = document.createElement(tag);
    if (cls) e.className = cls;
    e.textContent = text;
    return e;
  }

  function render() {
    panel.style.display = enabled ? '' : 'none';
    panel.replaceChildren();
    if (!enabled) return;
    if (result) {
      const evalText = result.outcome ? `${result.outcome} ${result.evalText.split(' ')[1]}` : result.evalText;
      panel.append(
        el('div', 'move', describe(result.move)),
        el('div', 'eval', `eval ${evalText}  ·  depth ${result.depth}${result.exact ? ' (exact)' : ''}`),
        el('div', '', result.reason),
      );
      if (result.lines && result.lines.length > 1) {
        panel.append(el('div', 'dim', 'also: ' + result.lines.slice(1).map((l) => `${l.move.text} (${l.evalText})`).join(', ')));
      }
    } else {
      panel.append(el('div', 'dim', 'Azul engine: ' + status));
    }
    panel.append(el('div', 'dim', 'Alt+A to hide'));
  }

  function clearHighlights() {
    document.querySelectorAll('.azul-engine-src, .azul-engine-dst').forEach((e) =>
      e.classList.remove('azul-engine-src', 'azul-engine-dst'));
  }

  // The page re-renders its board often, so highlights are re-applied on
  // every tick rather than once.
  function highlight() {
    clearHighlights();
    if (!enabled || !result) return;
    const m = result.move;
    const srcSel = m.source === 'center'
      ? `[id^="center-tile-"][tilecolor="${m.color}"]`
      : `[id^="factory-${m.factory - 1}-tile-"][tilecolor="${m.color}"]`;
    document.querySelectorAll(srcSel).forEach((e) => e.classList.add('azul-engine-src'));
    const dstSel = m.floor
      ? '#floor-grid-container .floor-grid-item'
      : `#pattern-lines-grid-container .pattern-lines-tile-item[row="${m.line - 1}"]`;
    document.querySelectorAll(dstSel).forEach((e) => e.classList.add('azul-engine-dst'));
  }

  document.addEventListener('keydown', (e) => {
    if (e.altKey && !e.ctrlKey && !e.metaKey && e.key.toLowerCase() === 'a') {
      enabled = !enabled;
      if (!enabled) clearHighlights();
      lastKey = null; // re-ask when turned back on
      render();
      e.preventDefault();
    }
  });

  render();
  setInterval(tick, POLL_MS);
})();
