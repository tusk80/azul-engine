'use strict';

// The light/dark choice for pages without the board, shared with it through
// the same saved setting.
(function () {
  const root = document.documentElement;
  let saved = null;
  try { saved = localStorage.getItem('azul.theme'); } catch { /* private mode */ }
  if (saved === 'light' || saved === 'dark') root.dataset.theme = saved;
  document.addEventListener('DOMContentLoaded', () => {
    const btn = document.getElementById('btn-theme');
    if (!btn) return;
    btn.addEventListener('click', () => {
      const dark = root.dataset.theme ? root.dataset.theme === 'dark' : matchMedia('(prefers-color-scheme: dark)').matches;
      root.dataset.theme = dark ? 'light' : 'dark';
      try { localStorage.setItem('azul.theme', root.dataset.theme); } catch { /* ignore */ }
    });
  });
})();
