/* Sets data-theme on <html> before the first paint, so the page never flashes the other theme.
   The choice of the person (localStorage "dash_go.theme": "light" or "dark") wins; without one the page follows the
   setting of the system, and keeps following it until the person picks a theme with the button. */
(function () {
  'use strict';
  var KEY = 'dash_go.theme';
  var root = document.documentElement;
  var query = window.matchMedia ? window.matchMedia('(prefers-color-scheme: dark)') : null;

  function stored() {
    try {
      var v = window.localStorage.getItem(KEY);
      return v === 'light' || v === 'dark' ? v : null;
    } catch (e) {
      return null; // storage may be blocked: the system setting is fine
    }
  }

  function apply() {
    root.setAttribute('data-theme', stored() || (query && query.matches ? 'dark' : 'light'));
  }

  apply();
  if (query) {
    var follow = function () { if (!stored()) { apply(); } };
    if (query.addEventListener) { query.addEventListener('change', follow); } else if (query.addListener) { query.addListener(follow); }
  }
  window.dashGoTheme = { key: KEY, apply: apply, current: function () { return root.getAttribute('data-theme'); } };
})();
