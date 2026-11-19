(() => {
  'use strict';

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

  async function call(method, path, body) {
    const opt = { method, credentials: 'same-origin', headers: { 'X-Requested-With': 'dash_go' } };
    if (body !== undefined) {
      opt.headers['Content-Type'] = 'application/json';
      opt.body = JSON.stringify(body);
    }
    let res;
    try {
      res = await fetch(path, opt);
    } catch (_) {
      const err = new Error('The server cannot be reached. Check the network and try again.');
      err.status = 0;
      throw err;
    }
    let data = null;
    try { data = await res.json(); } catch (_) { /* no body */ }
    if (!res.ok) {
      const err = new Error((data && data.error) || `Request failed (${res.status})`);
      err.status = res.status;
      err.code = data && data.code;
      err.data = data;
      throw err;
    }
    return data;
  }

  const list = (v) => (Array.isArray(v) ? v : []);
  const fmt = (n) => (Math.round(n * 100) / 100).toString();
  const initials = (name) => (name || '?').split(/\s+/).filter(Boolean).slice(0, 2).map((w) => w[0]).join('').toUpperCase();

  function avatar(name, url, size) {
    const cls = 'avatar' + (size ? ' ' + size : '');
    const img = el('img', { class: cls, src: url, alt: '', loading: 'lazy' });
    img.addEventListener('error', () => img.replaceWith(el('span', { class: cls, text: initials(name) })), { once: true });
    return img;
  }

  function lvBadge(level) {
    if (level >= 1) return el('span', { class: `lv lv${Math.min(level, 3)}`, text: `Lv${level}` });
    if (level === 0) return el('span', { class: 'lv lv0', text: 'No Rec' });
    return el('span', { class: 'lv lvx', text: 'Activity' });
  }

  function levelText(level) {
    if (level >= 1) return `Lv${level}`;
    if (level === 0) return 'no MTR record';
    return 'activity';
  }

  // ---- feedback: sound, toast, dialog --------------------------------------------------------------------------

  let audioCtx = null;
  function beep(kind) {
    try {
      audioCtx = audioCtx || new (window.AudioContext || window.webkitAudioContext)();
      const tones = { ok: [[880, 0.09]], warn: [[520, 0.1], [520, 0.1]], err: [[220, 0.35]] }[kind] || [];
      let t = audioCtx.currentTime;
      for (const [freq, dur] of tones) {
        const osc = audioCtx.createOscillator();
        const gain = audioCtx.createGain();
        osc.frequency.value = freq;
        gain.gain.value = 0.08;
        osc.connect(gain).connect(audioCtx.destination);
        osc.start(t);
        osc.stop(t + dur);
        t += dur + 0.06;
      }
    } catch (_) { /* audio is optional */ }
  }

  let toastTimer = null;
  function toast(text, kind) {
    const t = $('toast');
    t.className = 'toast ' + (kind || '');
    t.textContent = text;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => t.classList.add('hidden'), kind === 'err' ? 12000 : 7000);
  }

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
          el('button', { class: 'btn primary', type: 'button', text: okText || 'OK', onclick: () => finish(true) })),
      );
      dlg.showModal();
    });
  }

  // ---- state ---------------------------------------------------------------------------------------------------

  const state = { me: null, process: null, date: null, shift: null, board: null, seq: 0, timer: null, idRe: null, last: { id: '', at: 0 }, chain: Promise.resolve() };

  function show(name) {
    for (const id of ['screen-message', 'screen-picker', 'screen-app']) $(id).classList.toggle('hidden', id !== 'screen-' + name);
  }

  function message(title, text) {
    $('message-title').textContent = title;
    $('message-text').textContent = text;
    show('message');
  }

  const key = () => ({ process: state.process, date: state.date, shift: state.shift });
  const focusScan = () => { if (!$('screen-app').classList.contains('hidden') && !$('dialog').open) $('scan').focus({ preventScroll: true }); };

  // ---- start ---------------------------------------------------------------------------------------------------

  async function init() {
    let me;
    try {
      me = await call('GET', 'api/me');
    } catch (err) {
      if (err.status === 401) {
        message('Login required', 'Open this page from the Dash application: MQPD > Head Count > Scan Assign.');
      } else {
        message('Cannot start', err.message);
      }
      return;
    }
    state.me = me;
    state.idRe = new RegExp(me.config.id_pattern, 'i');
    $('date').min = firstOfPreviousMonth(me.config.production_date);
    $('date').max = addDays(me.config.production_date, 1);
    if (me.processes.length === 0) {
      message('No permission', 'You are not allowed to work on any HC process. Ask the HeadCount group admin for access.');
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
    $('picker-user').textContent = `Signed in as ${state.me.user.name} (${state.me.user.ee})`;
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
    refresh();
    startPolling();
    $('scan').value = '';
    $('result').className = 'result idle';
    $('result').replaceChildren(el('div', { class: 'result-idle', text: 'Ready. Scan the first badge.' }));
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
        stopPolling();
        message('Session expired', 'Open this page again from the Dash application: MQPD > Head Count > Scan Assign.');
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
      entries.replaceChildren(el('div', { class: 'empty', text: 'Loading...' }));
      $('machines').replaceChildren();
      $('missing').replaceChildren();
      $('pending-count').textContent = '0';
      $('missing-count').textContent = '0';
      return;
    }
    const t = b.totals;
    const entryList = list(b.entries);
    const missingList = list(b.not_scanned);
    const counter = (label, value, warn) => el('span', { class: 'counter' + (warn ? ' warn' : '') }, label + ' ', el('b', { text: value }));
    counters.replaceChildren(...[
      counter('Scanned', `${t.expected_scanned} / ${t.expected}`),
      counter('Pending', t.pending),
      counter('Saved', t.saved),
      t.unassigned ? counter('No machine', t.unassigned, true) : null,
      t.warnings ? counter('Warnings', t.warnings, true) : null,
    ].filter(Boolean)); // replaceChildren turns null into the text "null"
    $('locked-banner').classList.toggle('hidden', !b.locked);
    $('confirm').disabled = b.locked || t.assigned === 0;
    $('reset').disabled = t.pending === 0;
    $('pending-count').textContent = t.pending;
    $('missing-count').textContent = missingList.length;
    $('mtr-info').textContent = `MTR data: ${b.mtr.data_time || 'unknown'}` + (b.mtr.unmapped_machines ? ` | ${b.mtr.unmapped_machines} machine(s) without MTR mapping` : '');

    entries.replaceChildren(...(entryList.length ? entryList.map(renderEntry) : [el('div', { class: 'empty', text: 'No badge scanned yet.' })]));
    $('machines').replaceChildren(...list(b.machines).map(renderMachine));
    $('missing').replaceChildren(...(missingList.length ? missingList.map(renderMissing) : [el('span', { class: 'muted small', text: 'Everybody expected has scanned.' })]));
  }

  function machineLabel(m, level) {
    return `${m.name} - ${levelText(level)} (${fmt(m.used)}/${fmt(m.budget)})`;
  }

  function renderEntry(e) {
    const cls = ['entry'];
    if (e.warn) cls.push('warn');
    if (!e.machine_id) cls.push('none');
    const select = el('select', { 'aria-label': `Machine of ${e.name}`, onchange: (ev) => assign(e, ev.target.value) },
      el('option', { value: '', selected: !e.machine_id }, '- no machine -'),
      list(state.board.machines).map((m) => el('option', { value: m.id, selected: m.id === e.machine_id }, machineLabel(m, (e.levels || {})[m.id]))),
    );
    const flags = list(e.flags);
    const reason = flags.some((f) => f.text === e.reason) ? '' : (e.reason || ''); // a warning that says the same is enough
    const sub = [e.ee, e.shift ? `shift ${e.shift}` : null, e.schedule ? `day ${e.schedule}` : null].filter(Boolean).join(' | ');
    return el('div', { class: cls.join(' ') },
      avatar(e.name, e.photo, 'sm'),
      el('div', { class: 'entry-main' }, el('div', { class: 'entry-name', text: e.name, title: e.name }), el('div', { class: 'entry-sub', text: sub })),
      el('div', { class: 'entry-main' }, select, el('div', { class: 'entry-sub', text: reason, title: reason })),
      el('button', { class: 'icon-btn', type: 'button', title: `Remove ${e.name} from the draft`, 'aria-label': `Remove ${e.name}`, text: 'x', onclick: () => remove(e) }),
      flags.length ? el('div', { class: 'entry-flags' }, flags.map((f) => el('span', { class: `flag ${f.severity}`, text: f.text }))) : null,
    );
  }

  function renderMachine(m) {
    const full = m.budget > 0 && m.used >= m.budget && !m.over;
    const tags = [
      !m.routine ? el('span', { class: 'tag', text: 'activity' }) : null,
      m.routine && !m.mtr_mapped ? el('span', { class: 'tag warn', text: 'no MTR mapping' }) : null,
      m.budget <= 0 ? el('span', { class: 'tag', text: 'no budget' }) : null,
    ];
    return el('div', { class: 'machine' + (m.over ? ' over' : '') + (full ? ' full' : '') },
      el('div', { class: 'machine-head' }, el('span', { class: 'machine-name', text: m.name }), el('span', { class: 'machine-count', text: `${fmt(m.used)} / ${fmt(m.budget)}` })),
      el('div', { class: 'machine-tags' }, tags),
      el('div', { class: 'members' }, list(m.members).map((p) => el('div', { class: 'member' + (p.saved ? '' : ' pending'), title: p.saved ? 'Saved in the database' : 'Pending: not saved yet' },
        avatar(p.name, p.photo, 'xs'),
        el('span', { class: 'member-name', text: p.hc !== 1 ? `${p.name} (${fmt(p.hc)})` : p.name }),
        m.routine ? lvBadge(p.level) : null,
        p.saved ? el('span', { class: 'saved-mark', text: 'saved' }) : null,
      ))),
    );
  }

  function renderMissing(p) {
    return el('span', { class: 'chip' + (p.assigned ? ' assigned' : ''), title: p.assigned ? `Already assigned to ${p.assigned}` : `${p.ee} | shift ${p.shift || '?'} | day ${p.schedule || '?'}` },
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
      showResult({ error: `"${raw}" is not a valid employee ID` });
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
        message('Session expired', 'Open this page again from the Dash application: MQPD > Head Count > Scan Assign.');
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
        el('div', { class: 'result-name', text: 'Not accepted' }), el('div', { class: 'result-machine', text: res.error }))));
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
        el('div', { class: 'result-id', text: `${e.ee}${e.shift ? ' | shift ' + e.shift : ''}` }),
        el('div', { class: 'result-machine', text: assigned ? e.machine_name : 'No machine assigned' }),
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
      toast(err.message, 'err');
    }
    state.board = null;
    await refresh();
    focusScan();
  }

  async function reset() {
    const ok = await ask('Clear the draft?', el('p', { text: 'All pending lines are removed and those people count as not scanned. Lines that were already saved are not touched.' }), 'Clear draft', 'Cancel');
    if (!ok) return;
    try {
      await call('POST', 'api/reset', key());
    } catch (err) {
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
      if (err.code === 'needs_ack' && err.data) {
        const lines = list(err.data.needs_ack);
        const body = el('div', null,
          el('p', { text: `${lines.length} line(s) have warnings. Check them, then submit anyway or go back and change them.` }),
          lines.map((x) => el('div', { class: 'ack-item' }, el('b', { text: `${x.name} (${x.ee}) -> ${x.machine}` }), list(x.flags).map((f) => el('div', { class: 'flag warn', text: f.text })))));
        const yes = await ask('Submit lines with warnings?', body, 'Submit anyway', 'Go back');
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
    const lines = [`Submit to database successful! (${written.length} member(s))`];
    if (skipped.length) lines.push(`Not saved: ${skipped.map((s) => `${s.name}: ${s.reason}`).join('; ')}`);
    if (res.unassigned) lines.push(`${res.unassigned} scanned line(s) without a machine were not saved.`);
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
        const ok = await ask('Change process?', el('p', { text: 'The pending lines stay in the draft of this process and shift. You can come back to them.' }), 'Change process', 'Stay');
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
