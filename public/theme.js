'use strict';

// The light/dark choice for every page. It runs in the <head>, before the
// page is drawn, so there is no flash. Dark unless the visitor picked light.
(function () {
  const root = document.documentElement;
  let saved = null;
  try { saved = localStorage.getItem('azul.theme'); } catch { /* private mode */ }
  root.dataset.theme = saved === 'light' ? 'light' : 'dark';
  // compare.js animates the page in; without it (or with reduced motion) nothing is hidden.
  if (!matchMedia('(prefers-reduced-motion: reduce)').matches && 'IntersectionObserver' in window) root.classList.add('motion');
  document.addEventListener('DOMContentLoaded', () => {
    const btn = document.getElementById('btn-theme');
    if (!btn) return;
    btn.addEventListener('click', () => {
      root.dataset.theme = root.dataset.theme === 'dark' ? 'light' : 'dark';
      try { localStorage.setItem('azul.theme', root.dataset.theme); } catch { /* ignore */ }
    });
  });
})();
