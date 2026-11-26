/* dash_go shell script: what every page needs. It reads the catalog the server put into the page (#dg-i18n), offers
   the helpers the modules use (window.dashGo) and wires the header: the theme button, the sign-out button and the
   picture of the user. */
(function () {
  'use strict';

  var doc = document;
  var META = function (name) { var m = doc.querySelector('meta[name="' + name + '"]'); return m ? (m.getAttribute('content') || '') : ''; };
  var BASE = META('dg-base');
  var PHOTO = META('dg-photo');
  var catalog = {};
  try { catalog = JSON.parse(doc.getElementById('dg-i18n').textContent) || {}; } catch (e) { /* a page without texts shows the keys */ }

  // ---- texts ---------------------------------------------------------------------------------------------------

  // t returns the text of a key in the language of the page; {name} placeholders are filled from params.
  function t(key, params) {
    var s = catalog[key];
    if (s == null) { return key; }
    if (!params) { return s; }
    return s.replace(/\{([a-z][a-z0-9_]*)\}/g, function (m, k) { return params[k] != null ? String(params[k]) : m; });
  }

  // ---- calls to the server -------------------------------------------------------------------------------------

  // call sends a request the way the server expects it from this page (the header a cross-site form post cannot add)
  // and returns the JSON answer. A failure is an Error with status, code and data; its message is the text the
  // server wrote in the language of the page.
  function call(method, url, body) {
    var opt = { method: method, credentials: 'same-origin', headers: { 'X-Requested-With': 'dash_go' } };
    if (body !== undefined) {
      opt.headers['Content-Type'] = 'application/json';
      opt.body = JSON.stringify(body);
    }
    return fetch(url, opt).then(function (res) {
      return res.text().then(function (text) {
        var data = null;
        try { data = text ? JSON.parse(text) : null; } catch (e) { /* not JSON */ }
        if (!res.ok) {
          var err = new Error((data && data.error) || t('ui.request_failed', { status: res.status }));
          err.status = res.status;
          err.code = data && data.code;
          err.data = data;
          throw err;
        }
        return data;
      });
    }, function () {
      var err = new Error(t('ui.network_error'));
      err.status = 0;
      throw err;
    });
  }

  // ---- toast ---------------------------------------------------------------------------------------------------

  var toastTimer = null;
  function toast(text, kind) {
    var el = doc.getElementById('toast');
    if (!el) {
      el = doc.createElement('div');
      el.id = 'toast';
      el.setAttribute('role', 'status');
      doc.body.appendChild(el);
    }
    el.className = 'toast ' + (kind || '');
    el.textContent = text;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { el.classList.add('hidden'); }, kind === 'err' ? 12000 : 7000);
  }

  // ---- pictures of people --------------------------------------------------------------------------------------

  function photoURL(ee) {
    return PHOTO && ee ? PHOTO.replace('{ee_code}', encodeURIComponent(ee)) : '';
  }

  function initials(name) {
    return (name || '?').split(/\s+/).filter(Boolean).slice(0, 2).map(function (w) { return Array.from(w)[0]; }).join('').toUpperCase();
  }

  // The employee pictures are served by the Dash application, which answers a missing file with a picture of
  // 480 x 320 pixels (status 202), never with "not found". An image that size is therefore "no picture".
  var PLACEHOLDER = { w: 480, h: 320 };
  var MISSING_FOR = 10 * 60 * 1000;
  var missing = {};

  // avatar returns the initials in a circle, and swaps in the picture when it is found. size is "sm", "xs" or "".
  function avatar(name, url, size) {
    var cls = 'avatar' + (size ? ' ' + size : '');
    var holder = doc.createElement('span');
    holder.className = cls;
    holder.textContent = initials(name);
    if (!url || (missing[url] && Date.now() - missing[url] < MISSING_FOR)) { return holder; }
    var img = new Image();
    img.className = cls;
    img.alt = '';
    img.onload = function () {
      if (img.naturalWidth === PLACEHOLDER.w && img.naturalHeight === PLACEHOLDER.h) { missing[url] = Date.now(); return; }
      if (holder.parentNode) { holder.replaceWith(img); }
    };
    img.onerror = function () { missing[url] = Date.now(); };
    img.src = url;
    return holder;
  }

  // ---- pages ---------------------------------------------------------------------------------------------------

  function loginURL(next) {
    return BASE + '/login?next=' + encodeURIComponent(next || (window.location.pathname + window.location.search));
  }

  function logout() {
    return call('POST', BASE + '/auth/logout').then(function () {
      window.location.assign(BASE + '/login?signed_out=1');
    }, function (err) { toast(err.message, 'err'); });
  }

  // signedOut shows the header the way a visitor without a session sees it: the session ended while the page was open.
  function signedOut() {
    var box = doc.querySelector('.dg-userbox');
    if (!box) { return; }
    var link = doc.createElement('a');
    link.className = 'dg-btn dg-btn-small';
    link.href = loginURL();
    link.textContent = t('ui.sign_in');
    box.replaceWith(link);
  }

  // ---- theme ---------------------------------------------------------------------------------------------------

  function toggleTheme() {
    var root = doc.documentElement;
    var next = root.getAttribute('data-theme') === 'dark' ? 'light' : 'dark';
    root.setAttribute('data-theme', next);
    var key = window.dashGoTheme ? window.dashGoTheme.key : 'dash_go.theme'; // the key is defined by theme-init.js
    try { window.localStorage.setItem(key, next); } catch (e) { /* the choice just does not survive a reload */ }
  }

  // ---- shared HC process picker --------------------------------------------------------------------------------

  var PROCESS_KEY = 'hc.process';
  var LEGACY_PROCESS_KEY = 'hcscan.process';

  // getProcess reads the remembered HC process from sessionStorage, migrating the legacy hcscan.process key.
  function getProcess() {
    try {
      var v = window.sessionStorage.getItem(PROCESS_KEY);
      if (v) { return v; }
      var legacy = window.sessionStorage.getItem(LEGACY_PROCESS_KEY);
      if (legacy) {
        window.sessionStorage.setItem(PROCESS_KEY, legacy);
        return legacy;
      }
    } catch (e) { /* storage may be blocked */ }
    return null;
  }

  // setProcess stores or clears the remembered HC process shared across HC modules (hc_scan, hc_budget, ...).
  function setProcess(v) {
    try {
      if (v == null || v === '') {
        window.sessionStorage.removeItem(PROCESS_KEY);
        window.sessionStorage.removeItem(LEGACY_PROCESS_KEY);
      } else {
        window.sessionStorage.setItem(PROCESS_KEY, v);
      }
    } catch (e) { /* storage may be blocked */ }
  }

  // renderProcessPicker fills the shared #process-buttons list and #picker-user caption.
  function renderProcessPicker(opts) {
    opts = opts || {};
    var box = doc.getElementById(opts.buttonsId || 'process-buttons');
    if (box) {
      var frag = doc.createDocumentFragment();
      (Array.isArray(opts.processes) ? opts.processes : []).forEach(function (p) {
        var btn = doc.createElement('button');
        btn.type = 'button';
        btn.textContent = p;
        btn.addEventListener('click', function () {
          if (opts.onSelect) { opts.onSelect(p); }
        });
        frag.appendChild(btn);
      });
      box.replaceChildren(frag);
    }
    var userEl = doc.getElementById(opts.userId || 'picker-user');
    if (userEl && opts.signedInText != null) {
      userEl.textContent = opts.signedInText;
    }
  }

  // ---- header --------------------------------------------------------------------------------------------------

  // The pages below the header stick to its bottom edge, so they need its height.
  function trackHeader() {
    var header = doc.getElementById('dg-header');
    if (!header) { return; }
    var set = function () { doc.documentElement.style.setProperty('--dg-header-h', header.offsetHeight + 'px'); };
    set();
    if (window.ResizeObserver) { new ResizeObserver(set).observe(header); } else { window.addEventListener('resize', set); }
  }

  function initHeader() {
    doc.querySelectorAll('[data-photo-for]').forEach(function (el) {
      var ee = el.getAttribute('data-photo-for');
      el.replaceWith(avatar(el.getAttribute('data-name') || ee, photoURL(ee), ''));
    });
    trackHeader();
  }

  doc.addEventListener('click', function (e) {
    var target = e.target && e.target.closest ? e.target : null;
    if (!target) { return; }
    if (target.closest('[data-theme-toggle]')) { toggleTheme(); return; }
    if (target.closest('[data-logout]')) { e.preventDefault(); logout(); }
  });

  window.dashGo = {
    t: t, call: call, toast: toast, avatar: avatar, photoURL: photoURL, initials: initials,
    loginURL: loginURL, logout: logout, signedOut: signedOut, base: BASE, lang: doc.documentElement.lang,
    picker: { get: getProcess, set: setProcess, render: renderProcessPicker }
  };

  if (doc.readyState === 'loading') { doc.addEventListener('DOMContentLoaded', initHeader); } else { initHeader(); }
})();
