'use strict';

// Reveals the results page as it scrolls into view: cards rise, bars grow
// from zero and the headline numbers count up. theme.js marks the page with
// the "motion" class first, so without this script nothing stays hidden.
(function () {
  if (!document.documentElement.classList.contains('motion')) return;

  function countUp(el) {
    const m = /^([+−-]?)(\d+(?:\.\d+)?)(.*)$/.exec(el.textContent.trim());
    if (!m) return;
    const [, sign, num, rest] = m;
    const target = parseFloat(num);
    const decimals = (num.split('.')[1] || '').length;
    const start = performance.now();
    const step = (now) => {
      const t = Math.min(1, (now - start) / 1100);
      const e = 1 - Math.pow(1 - t, 3);
      el.textContent = sign + (target * e).toFixed(decimals) + rest;
      if (t < 1) requestAnimationFrame(step);
    };
    requestAnimationFrame(step);
  }

  const io = new IntersectionObserver((entries) => {
    for (const e of entries) {
      if (!e.isIntersecting) continue;
      io.unobserve(e.target);
      e.target.classList.add('in');
      e.target.querySelectorAll('.num').forEach(countUp);
    }
  }, { threshold: 0.2 });

  document.querySelectorAll('.stat').forEach((el, i) => {
    el.style.transitionDelay = i * 90 + 'ms';
    io.observe(el);
  });
  document.querySelectorAll('.cmp-card, .chart').forEach((el) => io.observe(el));
  document.querySelectorAll('.chart').forEach((chart) => {
    chart.querySelectorAll('.bar rect').forEach((r, i) => { r.style.transitionDelay = 150 + i * 70 + 'ms'; });
  });
})();
