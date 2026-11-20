/* The login page: the password form and the Windows sign-in. The server decides everything; this script sends the
   form, opens the window of the sign-on service and asks for the outcome. */
(function () {
  'use strict';

  var d = window.dashGo;
  if (!d) { return; }
  var $ = function (id) { return document.getElementById(id); };

  function show(box, text, kind) {
    box.textContent = text;
    box.className = 'dg-alert ' + (kind || 'err');
  }

  function hide(box) {
    box.textContent = '';
    box.className = 'dg-alert hidden';
  }

  // ---- account and password ------------------------------------------------------------------------------------

  var form = $('login-form');
  if (form) {
    var errorBox = $('login-error');
    var submit = $('login-submit');
    var label = submit.textContent;
    form.addEventListener('submit', function (e) {
      e.preventDefault();
      hide(errorBox);
      submit.disabled = true;
      submit.textContent = d.t('login.submitting');
      d.call('POST', d.base + '/auth/login', {
        username: form.elements.username.value,
        password: form.elements.password.value,
        next: form.getAttribute('data-next') || ''
      }).then(function (res) {
        window.location.assign((res && res.next) || d.base + '/');
      }, function (err) {
        show(errorBox, err.message);
        submit.disabled = false;
        submit.textContent = label;
        form.elements.password.value = '';
        form.elements.password.focus();
      });
    });
  }

  // ---- Windows sign-in -----------------------------------------------------------------------------------------

  var box = $('sso-box');
  if (box) {
    var button = $('sso-login');
    var status = $('sso-status');
    var running = null;

    button.addEventListener('click', function () {
      if (running) { return; }
      // The browser only allows a window that the click itself opens; its address is set once the server has
      // issued the token of this sign-in.
      var popup = window.open('', 'dash-go-sso', 'width=520,height=640');
      var run = running = { stopped: false, timer: 0 };
      button.disabled = true;
      show(status, d.t('login.sso_waiting'), 'info');

      function finish(key, text) {
        run.stopped = true;
        clearTimeout(run.timer);
        running = null;
        button.disabled = false;
        if (popup && !popup.closed) { try { popup.close(); } catch (e) { /* already gone */ } }
        show(status, text || d.t('login.sso_' + key), 'err');
      }

      d.call('POST', d.base + '/auth/sso/start', { next: box.getAttribute('data-next') || '' }).then(function (started) {
        if (!popup) { finish('popup'); return; }
        popup.location.href = started.url;
        var limit = Math.ceil((started.expires_in || 300) / 2);
        var tries = 0;
        var closedLooks = 0;
        (function look() {
          if (run.stopped) { return; }
          if (++tries > limit) { finish('timeout'); return; }
          // The window closes itself a moment after it has recorded the sign-in, so a closed window is only given
          // up on after a few more looks.
          if (popup.closed && ++closedLooks > 3) { finish('cancelled'); return; }
          d.call('POST', d.base + '/auth/sso/poll', { token: started.token }).then(function (res) {
            if (res.status === 'ok' && res.next) {
              run.stopped = true;
              window.location.assign(res.next);
            } else if (res.status === 'pending') {
              run.timer = setTimeout(look, 2000);
            } else {
              finish(res.status || 'error');
            }
          }, function () { finish('error'); });
        })();
      }, function (err) {
        finish('error', err.code === 'busy' ? d.t('login.sso_busy') : (err.message || d.t('login.sso_error')));
      });
    });
  }
})();
