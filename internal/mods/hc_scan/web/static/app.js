(() => {
  'use strict';

  // The shell (shell.js) comes first: it reads the texts of the page into dashGo.t, and offers the calls to the server,
  // the toast and the pictures of people.
  const dg = window.dashGo;
  const t = dg.t;
  const $ = (id) => document.getElementById(id);

  // ---- small helpers -------------------------------------------------------------------------------------------

  function el(tag, props, ...kids) {
    const e = document.createElement(tag);
    if (props) {
      for (const [k, v] of Object.entries(props)) {
        if (k === 'class') e.className = v;
        else if (k === 'text') e.textContent = v;
        else if (k.startsWith('on')) e.addEventListener(k.slice(2), v);
        else if (v === true) e.setAttribute(k, '');
        else if (v !== false && v != null) e.setAttribute(k, v);
      }
    }
    for (const kid of kids.flat()) {
      if (kid == null || kid === false) continue;
      e.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
    }
    return e;
  }

  const memory = {
    get(k) { try { return window.sessionStorage.getItem('hcscan.' + k); } catch (_) { return null; } },
    set(k, v) { try { if (v == null) window.sessionStorage.removeItem('hcscan.' + k); else window.sessionStorage.setItem('hcscan.' + k, v); } catch (_) { /* storage may be blocked */ } },
  };

  const call = (method, path, body) => dg.call(method, path, body);
  const list = (v) => (Array.isArray(v) ? v : []);
  const fmt = (n) => (Math.round(n * 100) / 100).toString();
  const avatar = (name, url, size) => dg.avatar(name, url, size);

  function lvBadge(level) {
    if (level >= 1) return el('span', { class: `lv lv${Math.min(level, 3)}`, text: `Lv${level}` });
    if (level === 0) return el('span', { class: 'lv lv0', text: t('hc.ui.badge_norec') });
    return el('span', { class: 'lv lvx', text: t('hc.ui.badge_activity') });
  }

  function levelText(level) {
    if (level >= 1) return `Lv${level}`;
    if (level === 0) return t('hc.ui.level_none');
    return t('hc.ui.level_activity');
  }

  // ---- feedback: sound, toast, dialog --------------------------------------------------------------------------

  let audioCtx = null;
  function beep(kind) {
    try {
      audioCtx = audioCtx || new (window.AudioContext || window.webkitAudioContext)();
      const tones = { ok: [[880, 0.09]], warn: [[520, 0.1], [520, 0.1]], err: [[220, 0.35]] }[kind] || [];
      let at = audioCtx.currentTime;
      for (const [freq, dur] of tones) {
        const osc = audioCtx.createOscillator();
        const gain = audioCtx.createGain();
        osc.frequency.value = freq;
        gain.gain.value = 0.08;
        osc.connect(gain).connect(audioCtx.destination);
        osc.start(at);
        osc.stop(at + dur);
        at += dur + 0.06;
      }
    } catch (_) { /* audio is optional */ }
  }

  const toast = (text, kind) => dg.toast(text, kind);

  function ask(title, body, okText, cancelText) {
    return new Promise((resolve) => {
      const dlg = $('dialog');
      let answer = false;
      const finish = (v) => { answer = v; dlg.close(); };
      dlg.onclose = () => { resolve(answer); focusScan(); }; // also runs when Escape closes the dialog
      dlg.replaceChildren(
        el('h3', { text: title }),
        el('div', { class: 'dlg-body' }, body),
        el('div', { class: 'dlg-actions' },
          cancelText ? el('button', { class: 'btn', type: 'button', text: cancelText, onclick: () => finish(false) }) : null,
          el('button', { class: 'btn primary', type: 'button', text: okText || t('ui.ok'), onclick: () => finish(true) })),
      );
      dlg.showModal();
    });
  }

  // ---- state ---------------------------------------------------------------------------------------------------

  const state = { me: null, process: null, date: null, shift: null, board: null, seq: 0, timer: null, idRe: null, last: { id: '', at: 0 }, chain: Promise.resolve() };

  function show(name) {
    for (const id of ['screen-message', 'screen-picker', 'screen-app']) $(id).classList.toggle('hidden', id !== 'screen-' + name);
  }

  // message shows a full page note; action is { href, label } for a button below it.
  function message(title, text, action) {
    $('message-title').textContent = title;
    $('message-text').textContent = text;
    $('message-action').classList.toggle('hidden', !action);
    if (action) {
      $('message-link').href = action.href;
      $('message-link').textContent = action.label;
    }
    show('message');
  }

  // The login ended while the page was open (or the server was restarted with another secret).
  function sessionExpired() {
    stopPolling();
    dg.signedOut();
    message(t('ui.session_expired_title'), t('ui.session_expired_text'), { href: dg.loginURL(), label: t('ui.sign_in') });
  }

  const key = () => ({ process: state.process, date: state.date, shift: state.shift });
  const focusScan = () => { if (!$('screen-app').classList.contains('hidden') && !$('dialog').open) $('scan').focus({ preventScroll: true }); };

  // The page bar and the scan panel stick below the header and below each other; they need each other's height.
  function trackBars() {
    const bar = $('pagebar');
    const set = () => document.documentElement.style.setProperty('--pagebar-h', bar.offsetHeight + 'px');
    set();
    if (window.ResizeObserver) new ResizeObserver(set).observe(bar); else window.addEventListener('resize', set);
  }

  // ---- start ---------------------------------------------------------------------------------------------------

  async function init() {
    let me;
    try {
      me = await call('GET', 'api/me');
    } catch (err) {
      if (err.status === 401) {
        sessionExpired();
      } else {
        message(t('hc.ui.cannot_start'), err.message);
      }
      return;
    }
    me.processes = list(me.processes); // a person without permission gets an empty list, never null
    state.me = me;
    state.idRe = new RegExp(me.config.id_pattern, 'i');
    $('date').min = firstOfPreviousMonth(me.config.production_date);
    $('date').max = addDays(me.config.production_date, 1);
    if (me.processes.length === 0) {
      message(t('hc.ui.no_permission_title'), t('hc.ui.no_permission_text'));
      return;
    }
    const remembered = memory.get('process');
    if (remembered && me.processes.includes(remembered)) {
      startSession(remembered, memory.get('date'), memory.get('shift'));
    } else {
      renderPicker();
    }
  }

  function renderPicker() {
    stopPolling();
    memory.set('process', null);
    const box = $('process-buttons');
    box.replaceChildren(...state.me.processes.map((p) => el('button', { type: 'button', text: p, onclick: () => startSession(p) })));
    $('picker-user').textContent = t('hc.ui.signed_in_as', { name: state.me.user.name, ee: state.me.user.ee });
    show('picker');
  }

  function startSession(process, date, shift) {
    state.process = process;
    state.date = date || state.me.config.production_date;
    state.shift = shift === 'AM' || shift === 'PM' ? shift : state.me.config.default_shift;
    state.board = null;
    memory.set('process', process);
    $('process-name').textContent = process;
    $('date').value = state.date;
    syncShiftButtons();
    renderBoard(null);
    show('app');
    trackBars();
    refresh();
    startPolling();
    $('scan').value = '';
    $('result').className = 'result idle';
    $('result').replaceChildren(el('div', { class: 'result-idle', text: t('hc.ui.ready') }));
    focusScan();
  }

  function syncShiftButtons() {
    for (const s of ['AM', 'PM']) $('shift-' + s).classList.toggle('active', state.shift === s);
    memory.set('date', state.date);
    memory.set('shift', state.shift);
  }

  function addDays(iso, n) {
    const d = new Date(iso + 'T00:00:00Z');
    d.setUTCDate(d.getUTCDate() + n);
    return d.toISOString().slice(0, 10);
  }

  function firstOfPreviousMonth(iso) {
    const d = new Date(iso + 'T00:00:00Z');
    return new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth() - 1, 1)).toISOString().slice(0, 10);
  }

  // ---- polling and rendering -----------------------------------------------------------------------------------

  function startPolling() {
    stopPolling();
    state.timer = setInterval(() => {
      if (document.hidden || $('dialog').open) return;
      refresh(true);
    }, state.me.config.poll_ms || 3000);
  }

  function stopPolling() {
    clearInterval(state.timer);
    state.timer = null;
  }

  async function refresh(quiet) {
    const seq = ++state.seq;
    const k = key();
    try {
      const board = await call('GET', `api/board?process=${encodeURIComponent(k.process)}&date=${k.date}&shift=${k.shift}`);
      if (seq !== state.seq || k.process !== state.process || k.date !== state.date || k.shift !== state.shift) return;
      const active = document.activeElement;
      if (quiet && active && active.tagName === 'SELECT') return; // do not close a list the leader is using
      const sig = JSON.stringify({ ...board, server_time: null });
      if (state.board && state.board.sig === sig) return;
      board.sig = sig;
      state.board = board;
      renderBoard(board);
    } catch (err) {
      if (err.status === 401) {
        sessionExpired();
      } else if (!quiet) {
        toast(err.message, 'err');
      }
    }
  }

  function renderBoard(b) {
    const counters = $('counters');
    const entries = $('entries');
    if (!b) {
      counters.replaceChildren();
      entries.replaceChildren(el('div', { class: 'empty', text: t('hc.ui.loading') }));
      $('machines').replaceChildren();
      $('missing').replaceChildren();
      $('pending-count').textContent = '0';
      $('missing-count').textContent = '0';
      return;
    }
    const totals = b.totals;
    const entryList = list(b.entries);
    const missingList = list(b.not_scanned);
    const counter = (label, value, warn) => el('span', { class: 'counter' + (warn ? ' warn' : '') }, label + ' ', el('b', { text: value }));
    counters.replaceChildren(...[
      counter(t('hc.ui.counter_scanned'), `${totals.expected_scanned} / ${totals.expected}`),
      counter(t('hc.ui.counter_pending'), totals.pending),
      counter(t('hc.ui.counter_saved'), totals.saved),
      totals.unassigned ? counter(t('hc.ui.counter_no_machine'), totals.unassigned, true) : null,
      totals.warnings ? counter(t('hc.ui.counter_warnings'), totals.warnings, true) : null,
    ].filter(Boolean)); // replaceChildren turns null into the text "null"
    $('locked-banner').classList.toggle('hidden', !b.locked);
    $('confirm').disabled = b.locked || totals.assigned === 0;
    $('reset').disabled = totals.pending === 0;
    $('pending-count').textContent = totals.pending;
    $('missing-count').textContent = missingList.length;
    $('mtr-info').textContent = t('hc.ui.mtr_info', { time: b.mtr.data_time || t('hc.ui.unknown') })
      + (b.mtr.unmapped_machines ? ' | ' + t('hc.ui.mtr_unmapped', { n: b.mtr.unmapped_machines }) : '');

    entries.replaceChildren(...(entryList.length ? entryList.map(renderEntry) : [el('div', { class: 'empty', text: t('hc.ui.no_entries') })]));
    $('machines').replaceChildren(...list(b.machines).map(renderMachine));
    $('missing').replaceChildren(...(missingList.length ? missingList.map(renderMissing) : [el('span', { class: 'muted small', text: t('hc.ui.all_scanned') })]));
  }

  function machineLabel(m, level) {
    return `${m.name} - ${levelText(level)} (${fmt(m.used)}/${fmt(m.budget)})`;
  }

  function renderEntry(e) {
    const cls = ['entry'];
    if (e.warn) cls.push('warn');
    if (!e.machine_id) cls.push('none');
    const select = el('select', { 'aria-label': t('hc.ui.machine_of', { name: e.name }), onchange: (ev) => assign(e, ev.target.value) },
      el('option', { value: '', selected: !e.machine_id }, t('hc.ui.no_machine_option')),
      list(state.board.machines).map((m) => el('option', { value: m.id, selected: m.id === e.machine_id }, machineLabel(m, (e.levels || {})[m.id]))),
    );
    const flags = list(e.flags);
    const reason = flags.some((f) => f.text === e.reason) ? '' : (e.reason || ''); // a warning that says the same is enough
    const sub = [e.ee, e.shift ? t('hc.ui.shift_n', { shift: e.shift }) : null, e.schedule ? t('hc.ui.day_n', { day: e.schedule }) : null].filter(Boolean).join(' | ');
    return el('div', { class: cls.join(' ') },
      avatar(e.name, e.photo, 'sm'),
      el('div', { class: 'entry-main' }, el('div', { class: 'entry-name', text: e.name, title: e.name }), el('div', { class: 'entry-sub', text: sub })),
      el('div', { class: 'entry-main' }, select, el('div', { class: 'entry-sub', text: reason, title: reason })),
      el('button', { class: 'icon-btn', type: 'button', title: t('hc.ui.remove_title', { name: e.name }), 'aria-label': t('hc.ui.remove_aria', { name: e.name }), text: 'x', onclick: () => remove(e) }),
      flags.length ? el('div', { class: 'entry-flags' }, flags.map((f) => el('span', { class: `flag ${f.severity}`, text: f.text }))) : null,
    );
  }

  function renderMachine(m) {
    const full = m.budget > 0 && m.used >= m.budget && !m.over;
    const tags = [
      !m.routine ? el('span', { class: 'tag', text: t('hc.ui.tag_activity') }) : null,
      m.routine && !m.mtr_mapped ? el('span', { class: 'tag warn', text: t('hc.ui.tag_no_mapping') }) : null,
      m.budget <= 0 ? el('span', { class: 'tag', text: t('hc.ui.tag_no_budget') }) : null,
    ];
    return el('div', { class: 'machine' + (m.over ? ' over' : '') + (full ? ' full' : '') },
      el('div', { class: 'machine-head' }, el('span', { class: 'machine-name', text: m.name }), el('span', { class: 'machine-count', text: `${fmt(m.used)} / ${fmt(m.budget)}` })),
      el('div', { class: 'machine-tags' }, tags),
      el('div', { class: 'members' }, list(m.members).map((p) => el('div', { class: 'member' + (p.saved ? '' : ' pending'), title: p.saved ? t('hc.ui.member_saved_title') : t('hc.ui.member_pending_title') },
        avatar(p.name, p.photo, 'xs'),
        el('span', { class: 'member-name', text: p.hc !== 1 ? `${p.name} (${fmt(p.hc)})` : p.name }),
        m.routine ? lvBadge(p.level) : null,
        p.saved ? el('span', { class: 'saved-mark', text: t('hc.ui.saved') }) : null,
      ))),
    );
  }

  function renderMissing(p) {
    const title = p.assigned
      ? t('hc.ui.assigned_to', { machine: p.assigned })
      : [p.ee, t('hc.ui.shift_n', { shift: p.shift || '?' }), t('hc.ui.day_n', { day: p.schedule || '?' })].join(' | ');
    return el('span', { class: 'chip' + (p.assigned ? ' assigned' : ''), title },
      avatar(p.name, p.photo, 'xs'), p.name, p.assigned ? el('span', { class: 'saved-mark', text: p.assigned }) : null);
  }

  // ---- scanning ------------------------------------------------------------------------------------------------

  let debounce = null;
  let dropTimer = null;

  function tryScan(force) {
    const input = $('scan');
    const raw = input.value.trim();
    if (!raw) return;
    const m = raw.toUpperCase().match(state.idRe);
    if (m) {
      input.value = '';
      enqueueScan(m[0].toUpperCase());
      return;
    }
    if (force) {
      input.value = '';
      showResult({ error: t('hc.ui.invalid_id', { input: raw }) });
      return;
    }
    clearTimeout(dropTimer); // an incomplete read is dropped after a short while
    dropTimer = setTimeout(() => { if (input.value.trim() === raw) input.value = ''; }, 2500);
  }

  function enqueueScan(id) {
    const now = Date.now();
    if (state.last.id === id && now - state.last.at < 1500) return; // a scanner that repeats the read
    state.last = { id, at: now };
    state.chain = state.chain.then(() => doScan(id)).catch(() => {});
  }

  async function doScan(id) {
    if (!state.process) return;
    try {
      const res = await call('POST', 'api/scan', { ...key(), input: id });
      showResult(res);
    } catch (err) {
      if (err.status === 401) {
        sessionExpired();
        return;
      }
      showResult({ error: err.message });
    }
    state.board = null; // force the next render
    await refresh();
  }

  function showResult(res) {
    const box = $('result');
    if (res.error) {
      box.className = 'result err';
      box.replaceChildren(el('div', { class: 'result-body' }, el('div', { class: 'result-text' },
        el('div', { class: 'result-name', text: t('hc.ui.not_accepted') }), el('div', { class: 'result-machine', text: res.error }))));
      beep('err');
      return;
    }
    const e = res.entry;
    const flags = list(e.flags);
    const assigned = !!e.machine_id;
    const kind = assigned && !e.warn && res.status !== 'duplicate' ? 'ok' : 'warn';
    box.className = 'result ' + kind;
    box.replaceChildren(el('div', { class: 'result-body' },
      avatar(e.name, e.photo),
      el('div', { class: 'result-text' },
        el('div', { class: 'result-name', text: e.name }),
        el('div', { class: 'result-id', text: `${e.ee}${e.shift ? ' | ' + t('hc.ui.shift_n', { shift: e.shift }) : ''}` }),
        el('div', { class: 'result-machine', text: assigned ? e.machine_name : t('hc.ui.no_machine_assigned') }),
        el('div', { class: 'result-reason', text: res.status === 'duplicate' || res.status === 'saved' ? res.message : (flags.some((f) => f.text === e.reason) ? '' : (e.reason || '')) }),
        flags.length ? el('div', { class: 'entry-flags' }, flags.map((f) => el('span', { class: `flag ${f.severity}`, text: f.text }))) : null,
      ),
      assigned ? lvBadge(e.level) : null,
    ));
    beep(kind === 'ok' ? 'ok' : 'warn');
  }

  // ---- changes by the shift leader -----------------------------------------------------------------------------

  async function assign(entry, machineId) {
    try {
      await call('POST', 'api/assign', { ...key(), ee: entry.ee, machine_id: machineId });
    } catch (err) {
      if (err.status === 401) { sessionExpired(); return; }
      toast(err.message, 'err');
    }
    state.board = null;
    await refresh();
    focusScan();
  }

  async function remove(entry) {
    try {
      await call('POST', 'api/remove', { ...key(), ee: entry.ee });
    } catch (err) {
      if (err.status === 401) { sessionExpired(); return; }
      toast(err.message, 'err');
    }
    state.board = null;
    await refresh();
    focusScan();
  }

  async function reset() {
    const ok = await ask(t('hc.ui.clear_title'), el('p', { text: t('hc.ui.clear_text') }), t('hc.ui.clear_draft'), t('ui.cancel'));
    if (!ok) return;
    try {
      await call('POST', 'api/reset', key());
    } catch (err) {
      if (err.status === 401) { sessionExpired(); return; }
      toast(err.message, 'err');
    }
    state.board = null;
    await refresh();
  }

  async function confirm(ack) {
    $('confirm').disabled = true;
    try {
      const res = await call('POST', 'api/confirm', { ...key(), ack: !!ack });
      reportSaved(res);
    } catch (err) {
      if (err.status === 401) {
        sessionExpired();
        return;
      }
      if (err.code === 'needs_ack' && err.data) {
        const lines = list(err.data.needs_ack);
        const body = el('div', null,
          el('p', { text: t('hc.ui.ack_intro', { n: lines.length }) }),
          lines.map((x) => el('div', { class: 'ack-item' }, el('b', { text: `${x.name} (${x.ee}) -> ${x.machine}` }), list(x.flags).map((f) => el('div', { class: 'flag warn', text: f.text })))));
        const yes = await ask(t('hc.ui.ack_title'), body, t('hc.ui.ack_yes'), t('hc.ui.ack_no'));
        if (yes) {
          await confirm(true);
          return;
        }
      } else {
        toast(err.message, 'err');
        beep('err');
      }
    }
    state.board = null;
    await refresh();
    focusScan();
  }

  function reportSaved(res) {
    const written = list(res.written);
    const skipped = list(res.skipped);
    const lines = [t('hc.ui.saved_ok', { n: written.length })];
    if (skipped.length) lines.push(t('hc.ui.not_saved', { list: skipped.map((s) => `${s.name}: ${s.reason}`).join('; ') }));
    if (res.unassigned) lines.push(t('hc.ui.unassigned_not_saved', { n: res.unassigned }));
    if (res.log_warning) lines.push(res.log_warning);
    toast(lines.join('\n'), skipped.length || res.log_warning ? 'warn' : 'ok');
    beep('ok');
  }

  // ---- wiring --------------------------------------------------------------------------------------------------

  function wire() {
    const input = $('scan');
    input.addEventListener('input', () => { clearTimeout(debounce); debounce = setTimeout(() => tryScan(false), 90); });
    input.addEventListener('keydown', (ev) => {
      if (ev.key === 'Enter') { ev.preventDefault(); clearTimeout(debounce); tryScan(true); }
    });
    input.addEventListener('blur', () => setTimeout(() => { const a = document.activeElement; if (!a || a === document.body) focusScan(); }, 250));
    document.addEventListener('click', (ev) => { if (!ev.target.closest('select, button, input, dialog, a')) focusScan(); });

    $('date').addEventListener('change', (ev) => {
      if (!ev.target.value) return;
      state.date = ev.target.value;
      state.board = null;
      syncShiftButtons();
      refresh();
      focusScan();
    });
    for (const s of ['AM', 'PM']) {
      $('shift-' + s).addEventListener('click', () => { state.shift = s; state.board = null; syncShiftButtons(); refresh(); focusScan(); });
    }
    $('change-process').addEventListener('click', async () => {
      if (state.board && state.board.totals.pending > 0) {
        const ok = await ask(t('hc.ui.change_title'), el('p', { text: t('hc.ui.change_text') }), t('hc.ui.change_process'), t('hc.ui.change_stay'));
        if (!ok) return;
      }
      renderPicker();
    });
    $('confirm').addEventListener('click', () => confirm(false));
    $('reset').addEventListener('click', reset);
  }

  wire();
  init();
})();
