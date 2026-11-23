/* dash_go page guide: a step-by-step tour that dims the page, highlights one visible part at a time and explains what
   it shows or does (adapted from the TimeVault project). Steps come from elements carrying data-tour, data-tour-title
   and data-tour-text (rendered server-side in the language of the page by shell view.Tour). Nothing is modified and
   nothing is clicked for the visitor. On a visitor's first visit to a page (remembered in localStorage), the tour
   starts automatically once; clicking the Guide button in the header replays the full tour at any time. */
(function () {
  'use strict';

  var doc = document;
  var dg = window.dashGo;
  if (!dg) { return; }
  var t = dg.t;

  var SEEN_PREFIX = 'dash_go.tour.seen.';
  var HEADER_SEEN_KEY = '_header';

  var parts = null;      // the DOM nodes of the tour while open
  var steps = [];
  var index = 0;
  var opener = null;     // the Guide button when opened manually, to restore focus on close
  var frame = 0;
  var settling = 0;
  var settleNode = null;
  var token = 0;

  function make(tag, cls, text) {
    var node = doc.createElement(tag);
    if (cls) { node.className = cls; }
    if (text) { node.textContent = text; }
    return node;
  }

  function visible(node) {
    if (!node || !node.getClientRects().length) { return false; }
    var r = node.getBoundingClientRect();
    return r.width > 0 && r.height > 0;
  }

  // ---- localStorage (first-visit auto-start) -------------------------------------------------------------------

  // storage returns localStorage only when it can be read and written; when storage is blocked we skip auto-start so
  // the guide never pops up on every single reload.
  function storage() {
    try {
      var s = window.localStorage;
      if (!s) { return null; }
      var probe = SEEN_PREFIX + '_probe';
      s.setItem(probe, '1');
      s.removeItem(probe);
      return s;
    } catch (e) {
      return null;
    }
  }

  function isSeen(key) {
    var s = storage();
    if (!s) { return true; }
    return s.getItem(SEEN_PREFIX + key) === '1';
  }

  function markSeen(key) {
    if (!key) { return; }
    var s = storage();
    if (!s) { return; }
    try { s.setItem(SEEN_PREFIX + key, '1'); } catch (e) { /* storage full or blocked */ }
  }

  // ---- collecting steps ----------------------------------------------------------------------------------------

  // Content steps come first in DOM order, followed by header steps. Elements that are hidden (such as inactive
  // screens of a module or a hidden banner) have no bounding box and are skipped automatically.
  function collect(skipHeader) {
    var header = doc.getElementById('dg-header');
    var own = [];
    var rest = [];
    doc.querySelectorAll('[data-tour]').forEach(function (node) {
      var text = node.getAttribute('data-tour-text');
      if (!text || !visible(node)) { return; }
      var inHeader = !!(header && header.contains(node));
      if (skipHeader && inHeader) { return; }
      var step = {
        key: node.getAttribute('data-tour'),
        el: node,
        title: node.getAttribute('data-tour-title') || '',
        text: text
      };
      (inHeader ? rest : own).push(step);
    });
    return own.concat(rest);
  }

  function activeScreenKeys(button) {
    var keys = [];
    if (button) {
      var autoKey = button.getAttribute('data-tour-auto');
      if (autoKey) { keys.push(autoKey); }
    }
    doc.querySelectorAll('[data-tour-screen]').forEach(function (node) {
      var k = node.getAttribute('data-tour-screen');
      if (k && visible(node)) { keys.push(k); }
    });
    return keys;
  }

  // ---- building the overlay ------------------------------------------------------------------------------------

  function build() {
    var root = make('div', 'dg-tour');
    var shield = make('div', 'dg-tour-shield');
    var spot = make('div', 'dg-tour-spot');
    var pop = make('div', 'dg-tour-pop');
    var close = make('button', 'dg-iconbtn dg-tour-close', '\u00d7');
    var said = make('div', 'dg-tour-said');
    var title = make('h2', 'dg-tour-title');
    var text = make('p', 'dg-tour-text');
    var foot = make('div', 'dg-tour-foot');
    var count = make('span', 'dg-tour-count');
    var back = make('button', 'dg-btn dg-btn-small dg-btn-ghost', t('ui.tour_back'));
    var next = make('button', 'dg-btn dg-btn-small dg-btn-primary');

    title.id = 'dg-tour-title';
    text.id = 'dg-tour-text';
    said.setAttribute('aria-live', 'polite');
    said.setAttribute('aria-atomic', 'true');
    pop.setAttribute('role', 'dialog');
    pop.setAttribute('aria-modal', 'true');
    pop.setAttribute('aria-label', t('ui.tour_dialog'));
    pop.setAttribute('aria-labelledby', title.id);
    pop.setAttribute('aria-describedby', text.id);
    [close, back, next].forEach(function (b) { b.type = 'button'; });
    var closeLabel = t('ui.tour_close');
    close.setAttribute('aria-label', closeLabel);
    close.title = closeLabel;

    close.addEventListener('click', stop);
    back.addEventListener('click', function () { go(-1); });
    next.addEventListener('click', function () { go(1); });
    shield.addEventListener('click', function (e) { e.preventDefault(); e.stopPropagation(); });

    said.append(title, text);
    foot.append(count, back, next);
    pop.append(close, said, foot);
    root.append(shield, spot, pop);
    doc.body.appendChild(root);
    return { root: root, spot: spot, pop: pop, title: title, text: text, count: count, back: back, next: next, close: close };
  }

  // ---- navigation and positioning ------------------------------------------------------------------------------

  function go(by) {
    var to = index + by;
    if (to < 0) { return; }
    if (to >= steps.length) { stop(); return; }
    show(to);
  }

  function headerBottom() {
    var header = doc.getElementById('dg-header');
    if (!header || window.getComputedStyle(header).position !== 'sticky') { return 0; }
    return Math.max(0, header.getBoundingClientRect().bottom);
  }

  // stickyTopFor computes how much top viewport space is covered by sticky bars above node (the header, plus any
  // visible [data-tour-sticky] bar that overlaps node horizontally and does not contain node).
  function stickyTopFor(node) {
    var top = headerBottom();
    var header = doc.getElementById('dg-header');
    if (header && header.contains(node)) { return 0; }
    var nr = node.getBoundingClientRect();
    var vh = window.innerHeight;
    doc.querySelectorAll('[data-tour-sticky]').forEach(function (bar) {
      if (bar === node || bar.contains(node) || !visible(bar)) { return; }
      if (window.getComputedStyle(bar).position !== 'sticky') { return; }
      var br = bar.getBoundingClientRect();
      if (br.left >= nr.right || br.right <= nr.left) { return; }
      if (br.bottom > top && br.bottom < vh * 0.55) { top = br.bottom; }
    });
    return top;
  }

  function isPinned(node) {
    var header = doc.getElementById('dg-header');
    if (header && header.contains(node) && headerBottom() > 0) { return true; }
    var bar = node.closest ? node.closest('[data-tour-sticky]') : null;
    if (bar && window.getComputedStyle(bar).position === 'sticky') {
      var br = bar.getBoundingClientRect();
      if (br.top >= 0 && br.bottom <= window.innerHeight) { return true; }
    }
    return false;
  }

  function docked() { return window.innerWidth <= 640; }

  function room(node) {
    var vh = window.innerHeight;
    return {
      top: stickyTopFor(node) + 8,
      bottom: vh - 8 - (docked() ? parts.pop.offsetHeight + 8 : 0)
    };
  }

  function unsettle() {
    cancelAnimationFrame(settling);
    if (settleNode) { settleNode.style.scrollMarginTop = ''; settleNode = null; }
  }

  function reveal(node, done) {
    unsettle();
    var r = node.getBoundingClientRect();
    var box = room(node);
    var tall = r.height > (box.bottom - box.top) * 0.8;
    var inView = r.top >= box.top && r.bottom <= box.bottom;
    var partly = r.top < box.bottom - 40 && r.bottom > box.top + 40;
    if (isPinned(node) || inView || (tall && partly)) { done(); return; }
    var calm = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
    node.style.scrollMarginTop = (box.top + 4) + 'px';
    settleNode = node;
    node.scrollIntoView({ block: tall || docked() ? 'start' : 'center', behavior: calm ? 'auto' : 'smooth' });
    var last = window.scrollY;
    var still = 0;
    var began = Date.now();
    (function look() {
      var y = window.scrollY;
      still = y === last ? still + 1 : 0;
      last = y;
      if (still >= 4 || Date.now() - began > 1000) {
        unsettle();
        done();
      } else {
        settling = requestAnimationFrame(look);
      }
    })();
  }

  function place(node) {
    if (!parts) { return; }
    var r = node.getBoundingClientRect();
    var vw = window.innerWidth;
    var vh = window.innerHeight;
    var pad = 6;
    var spot = parts.spot.style;
    var first = !parts.root.classList.contains('is-shown');
    spot.top = (r.top - pad) + 'px';
    spot.left = (r.left - pad) + 'px';
    spot.width = (r.width + 2 * pad) + 'px';
    spot.height = (r.height + 2 * pad) + 'px';

    var pop = parts.pop;
    var small = docked();
    pop.classList.toggle('is-docked', small);
    pop.classList.toggle('is-top', small && r.bottom > vh * 0.65 && r.top > vh * 0.35);
    if (small) {
      pop.style.top = '';
      pop.style.left = '';
    } else {
      var gap = 14;
      var pw = pop.offsetWidth;
      var ph = pop.offsetHeight;
      var top0 = Math.max(r.top, 0);
      var bottom0 = Math.min(r.bottom, vh);
      var left0 = Math.max(r.left, 0);
      var right0 = Math.min(r.right, vw);
      var top;
      if (vh - bottom0 - pad >= ph + gap) { top = bottom0 + pad + gap; }
      else if (top0 - pad >= ph + gap) { top = top0 - pad - gap - ph; }
      else { top = Math.max(12, vh - ph - 12); }
      var left = left0 + (right0 - left0) / 2 - pw / 2;
      pop.style.top = top + 'px';
      pop.style.left = Math.min(Math.max(left, 12), Math.max(12, vw - pw - 12)) + 'px';
    }
    if (first) {
      parts.root.classList.add('is-shown');
      requestAnimationFrame(function () { if (parts) { parts.root.classList.add('is-placed'); } });
    }
  }

  function show(i) {
    var step = steps[i];
    index = i;
    if (!doc.body.contains(step.el) || !visible(step.el)) {
      steps.splice(i, 1);
      if (!steps.length) { stop(); } else { show(Math.min(i, steps.length - 1)); }
      return;
    }
    parts.title.textContent = step.title;
    parts.text.textContent = step.text;
    parts.count.textContent = t('ui.tour_step', { n: i + 1, total: steps.length });
    parts.back.disabled = i === 0;
    parts.next.textContent = i === steps.length - 1 ? t('ui.tour_done') : t('ui.tour_next');
    var mine = ++token;
    doc.documentElement.style.paddingBottom = docked() ? (parts.pop.offsetHeight + 24) + 'px' : '';
    reveal(step.el, function () {
      if (mine !== token || !parts) { return; }
      place(step.el);
      parts.next.focus({ preventScroll: true });
    });
  }

  function follow() {
    if (frame || !parts) { return; }
    frame = requestAnimationFrame(function () {
      frame = 0;
      if (parts && steps[index]) { place(steps[index].el); }
    });
  }

  // ---- keyboard and lifecycle ----------------------------------------------------------------------------------

  function onKey(e) {
    if (!parts) { return; }
    if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); stop(); return; }
    if (e.key === 'ArrowRight') { e.preventDefault(); e.stopPropagation(); go(1); return; }
    if (e.key === 'ArrowLeft') { e.preventDefault(); e.stopPropagation(); go(-1); return; }
    if (e.key === 'Tab') {
      var items = [parts.close, parts.back, parts.next].filter(function (b) { return !b.disabled; });
      var first = items[0];
      var last = items[items.length - 1];
      var at = doc.activeElement;
      if (!parts.pop.contains(at)) { e.preventDefault(); first.focus(); }
      else if (e.shiftKey && at === first) { e.preventDefault(); last.focus(); }
      else if (!e.shiftKey && at === last) { e.preventDefault(); first.focus(); }
      return;
    }
    if (e.key !== 'Enter' && e.key !== ' ' && !e.ctrlKey && !e.metaKey && !e.altKey) { e.stopPropagation(); }
  }

  function start(button, opts) {
    if (parts) { return false; }
    opts = opts || {};
    var skipHeader = !!(opts.skipHeaderIfSeen && isSeen(HEADER_SEEN_KEY));
    steps = collect(skipHeader);
    if (!steps.length && skipHeader) {
      skipHeader = false;
      steps = collect(false);
    }
    if (!steps.length) { return false; }
    opener = opts.auto ? null : (button || null);
    if (opts.markKey) { markSeen(opts.markKey); }
    activeScreenKeys(button).forEach(markSeen);
    if (!skipHeader) { markSeen(HEADER_SEEN_KEY); }
    parts = build();
    doc.documentElement.classList.add('dg-tour-open');
    doc.addEventListener('keydown', onKey, true);
    window.addEventListener('resize', follow);
    window.addEventListener('scroll', follow, true);
    window.addEventListener('popstate', stop);
    doc.dispatchEvent(new CustomEvent('dg:tour-start'));
    show(0);
    return true;
  }

  function stop() {
    if (!parts) { return; }
    cancelAnimationFrame(frame);
    unsettle();
    frame = 0;
    token++;
    doc.removeEventListener('keydown', onKey, true);
    window.removeEventListener('resize', follow);
    window.removeEventListener('scroll', follow, true);
    window.removeEventListener('popstate', stop);
    parts.root.remove();
    parts = null;
    steps = [];
    doc.documentElement.classList.remove('dg-tour-open');
    doc.documentElement.style.paddingBottom = '';
    var restore = opener;
    opener = null;
    if (restore && doc.body.contains(restore)) { restore.focus({ preventScroll: true }); }
    doc.dispatchEvent(new CustomEvent('dg:tour-stop'));
  }

  // autoStart launches the tour once per browser for key (stored in localStorage under dash_go.tour.seen.<key>).
  // Returns true when the tour started.
  function autoStart(key, opts) {
    if (parts || !key) { return false; }
    var s = storage();
    if (!s || s.getItem(SEEN_PREFIX + key) === '1') { return false; }
    var btn = doc.querySelector('[data-tour-start]');
    if (!btn) { return false; }
    return start(btn, { markKey: key, auto: true, skipHeaderIfSeen: !!(opts && opts.skipHeaderIfSeen) });
  }

  doc.addEventListener('click', function (e) {
    var target = e.target;
    var button = target && target.closest ? target.closest('[data-tour-start]') : null;
    if (!button) { return; }
    e.preventDefault();
    if (parts) { stop(); } else { start(button); }
  });

  function initAuto() {
    var btn = doc.querySelector('[data-tour-start][data-tour-auto]');
    if (!btn) { return; }
    var autoKey = btn.getAttribute('data-tour-auto');
    if (!autoKey) { return; }
    requestAnimationFrame(function () { autoStart(autoKey, { skipHeaderIfSeen: true }); });
  }

  dg.tour = {
    start: function () { return start(doc.querySelector('[data-tour-start]')); },
    stop: stop,
    autoStart: autoStart,
    isActive: function () { return !!parts; }
  };

  if (doc.readyState === 'loading') { doc.addEventListener('DOMContentLoaded', initAuto); } else { initAuto(); }
})();
