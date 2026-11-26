(() => {
  'use strict';

  const dg = window.dashGo;
  const t = dg.t;
  const $ = (id) => document.getElementById(id);

  const BUDGET_RE = /^\d{1,8}(\.\d{1,12})?$/;

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

  const list = (v) => (Array.isArray(v) ? v : []);
  const round4 = (n) => Math.round(n * 10000) / 10000;
  const fmtBudgetInput = (n) => (n == null || n === 0 ? '' : String(round4(Number(n))));
  const parseBudgetVal = (raw) => {
    const s = String(raw == null ? '' : raw).trim();
    if (s === '') return { ok: true, value: 0 };
    if (!BUDGET_RE.test(s)) return { ok: false, value: 0 };
    const n = Number(s);
    if (!Number.isFinite(n) || n < 0) return { ok: false, value: 0 };
    return { ok: true, value: n };
  };

  // ---- state ---------------------------------------------------------------------------------------------------

  const state = {
    me: null,
    process: null,
    isAdmin: false,
    includeHidden: false,
    defaults: null,
    serverTotals: null,
    // Each row item in state.rows:
    // { id, isNew, station_code, cost_center, process_code, machine, type, category, budget_text, hidden,
    //   orig: { machine, type, category, budget_val, hidden, display_seq } | null, update_time, update_user }
    rows: [],
    origOrder: [],
    filter: '',
    saving: false,
    dragID: null,
  };

  const tourOpen = () => !!(dg.tour && dg.tour.isActive());

  function show(name) {
    if (tourOpen()) dg.tour.stop();
    for (const id of ['screen-message', 'screen-picker', 'screen-app']) {
      $(id).classList.toggle('hidden', id !== 'screen-' + name);
    }
  }

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

  function sessionExpired() {
    dg.signedOut();
    message(t('ui.session_expired_title'), t('ui.session_expired_text'), { href: dg.loginURL(), label: t('ui.sign_in') });
  }

  function ask(title, body, okText, cancelText) {
    return new Promise((resolve) => {
      const dlg = $('dialog');
      let answer = false;
      const finish = (v) => { answer = v; dlg.close(); };
      dlg.onclose = () => resolve(answer);
      dlg.replaceChildren(
        el('h3', { text: title }),
        el('div', { class: 'dlg-body' }, body),
        el('div', { class: 'dlg-actions' },
          cancelText ? el('button', { class: 'btn', type: 'button', text: cancelText, onclick: () => finish(false) }) : null,
          el('button', { class: 'btn primary', type: 'button', text: okText || t('ui.ok'), onclick: () => finish(true) }),
        ),
      );
      dlg.showModal();
    });
  }

  // ---- dirty & validation tracking -----------------------------------------------------------------------------

  function rowErrors(row) {
    const errs = {};
    if (!row.machine.trim()) errs.machine = t('hb.ui.err_machine_required');
    if (!parseBudgetVal(row.budget_text).ok) errs.budget = t('hb.ui.err_budget_format');
    return errs;
  }

  function isRowModified(row) {
    if (row.isNew) return true;
    const o = row.orig;
    if (!o) return false;
    const bp = parseBudgetVal(row.budget_text);
    const bChanged = !bp.ok || Math.abs(bp.value - o.budget_val) > 1e-6;
    return row.machine.trim() !== o.machine
      || row.type !== o.type
      || row.category.trim() !== o.category
      || row.hidden !== o.hidden
      || bChanged;
  }

  function isOrderChanged() {
    if (!state.isAdmin) return false;
    const cur = state.rows.map((r) => r.id);
    if (cur.length !== state.origOrder.length) return true;
    for (let i = 0; i < cur.length; i++) {
      if (cur[i] !== state.origOrder[i]) return true;
    }
    return false;
  }

  function computeChanges() {
    let added = 0;
    let updated = 0;
    let fieldEdited = 0;
    let hiddenCount = 0;
    let restoredCount = 0;
    let invalid = 0;
    for (const r of state.rows) {
      const errs = rowErrors(r);
      if (errs.machine || errs.budget) invalid++;
      if (r.isNew) {
        added++;
      } else if (isRowModified(r)) {
        updated++;
        const o = r.orig;
        const bp = parseBudgetVal(r.budget_text);
        const nonHiddenChanged = r.machine.trim() !== o.machine
          || r.type !== o.type
          || r.category.trim() !== o.category
          || !bp.ok || Math.abs(bp.value - o.budget_val) > 1e-6;
        if (nonHiddenChanged) fieldEdited++;
        if (r.hidden && !o.hidden) hiddenCount++;
        if (!r.hidden && o.hidden) restoredCount++;
      }
    }
    // Distinguish pure reorder from content edits so counter and summary stay clear.
    const reordered = isOrderChanged() && !(added > 0 && isOnlyAppendedNewRows());
    const totalChanges = added + updated + (reordered ? 1 : 0);
    return { added, updated, fieldEdited, hiddenCount, restoredCount, reordered, invalid, totalChanges };
  }

  function isOnlyAppendedNewRows() {
    if (state.rows.length < state.origOrder.length) return false;
    for (let i = 0; i < state.origOrder.length; i++) {
      if (state.rows[i].id !== state.origOrder[i]) return false;
    }
    return true;
  }

  function allUsedStationCodes() {
    const set = new Set(list(state.defaults && state.defaults.used_station_codes));
    for (const r of state.rows) {
      if (r.station_code != null) set.add(Number(r.station_code));
    }
    return set;
  }

  function refreshCategoryDatalist() {
    const cats = new Set(list(state.defaults && state.defaults.categories));
    for (const r of state.rows) {
      const c = r.category.trim();
      if (c) cats.add(c);
    }
    const sorted = Array.from(cats).sort((a, b) => a.localeCompare(b));
    $('category-suggestions').replaceChildren(...sorted.map((c) => el('option', { value: c })));
  }

  // ---- start & picker ------------------------------------------------------------------------------------------

  async function init() {
    let me;
    try {
      me = await dg.call('GET', 'api/me');
    } catch (err) {
      if (err.status === 401) sessionExpired();
      else message(t('hb.ui.cannot_start'), err.message);
      return;
    }
    me.processes = list(me.processes);
    state.me = me;
    state.isAdmin = !!me.is_admin;
    if (me.processes.length === 0) {
      message(t('hb.ui.no_permission_title'), t('hb.ui.no_permission_text'));
      return;
    }
    const remembered = dg.picker.get();
    if (remembered && me.processes.includes(remembered)) {
      openProcess(remembered);
    } else {
      renderPicker();
    }
  }

  function renderPicker() {
    dg.picker.set(null);
    state.process = null;
    state.rows = [];
    state.origOrder = [];
    dg.picker.render({
      processes: state.me.processes,
      signedInText: state.isAdmin
        ? t('hb.ui.signed_in_admin', { name: state.me.user.name, ee: state.me.user.ee })
        : t('hb.ui.signed_in_as', { name: state.me.user.name, ee: state.me.user.ee }),
      onSelect: (p) => openProcess(p),
    });
    show('picker');
    if (dg.tour) requestAnimationFrame(() => dg.tour.autoStart('hc_budget.picker', { skipHeaderIfSeen: true }));
  }

  async function openProcess(process) {
    state.process = process;
    state.filter = '';
    $('search-input').value = '';
    dg.picker.set(process);
    $('process-name').textContent = process;
    show('app');
    await loadTable(true);
  }

  async function loadTable(firstOpen) {
    $('table-empty').textContent = t('hb.ui.loading');
    $('table-empty').classList.remove('hidden');
    $('table-body').replaceChildren();
    try {
      const qs = `api/table?process=${encodeURIComponent(state.process)}&include_hidden=${state.includeHidden ? '1' : '0'}`;
      const data = await dg.call('GET', qs);
      state.isAdmin = !!data.is_admin;
      state.includeHidden = !!data.include_hidden;
      state.defaults = data.defaults || { cost_center: '', process_code: null, categories: [], used_station_codes: [], next_seq: 1 };
      state.serverTotals = data.totals || { visible_count: 0, hidden_count: 0, total_budget_hc: 0 };
      state.rows = list(data.rows).map((r) => ({
        id: r.id,
        isNew: false,
        station_code: r.station_code,
        cost_center: r.cost_center || '',
        process_code: r.process_code,
        machine: r.machine || '',
        type: r.type || 'Routine',
        category: r.category || '',
        budget_text: fmtBudgetInput(r.budget_head_count),
        hidden: !!r.hidden,
        update_time: r.update_time || '',
        update_user: r.update_user || '',
        orig: {
          machine: (r.machine || '').trim(),
          type: r.type || 'Routine',
          category: (r.category || '').trim(),
          budget_val: Number(r.budget_head_count || 0),
          hidden: !!r.hidden,
          display_seq: r.display_seq,
        },
      }));
      state.origOrder = state.rows.map((r) => r.id);
      syncRoleUI();
      refreshCategoryDatalist();
      renderAll();
      if (firstOpen && dg.tour) {
        requestAnimationFrame(() => dg.tour.autoStart('hc_budget.table', { skipHeaderIfSeen: true }));
      }
    } catch (err) {
      if (err.status === 401) {
        sessionExpired();
        return;
      }
      dg.toast(err.message, 'err');
      $('table-empty').textContent = err.message;
      $('table-empty').classList.remove('hidden');
    }
  }

  function syncRoleUI() {
    const admin = state.isAdmin;
    $('role-badge').classList.toggle('hidden', !admin);
    $('show-hidden-wrap').classList.toggle('hidden', !admin);
    $('btn-add').classList.toggle('hidden', !admin);
    $('th-order').classList.toggle('hidden', !admin);
    $('th-status').classList.toggle('hidden', !admin);
    $('show-hidden').checked = state.includeHidden;
  }

  // ---- rendering -----------------------------------------------------------------------------------------------

  function filteredRows() {
    const q = state.filter.trim().toLowerCase();
    if (!q) return state.rows;
    return state.rows.filter((r) =>
      r.machine.toLowerCase().includes(q)
      || r.category.toLowerCase().includes(q)
      || r.type.toLowerCase().includes(q)
      || r.id.toLowerCase().includes(q)
      || (r.station_code != null && String(r.station_code).includes(q))
      || r.cost_center.toLowerCase().includes(q),
    );
  }

  function renderHeaderState() {
    const changes = computeChanges();

    // Active / hidden counts and live total budget HC
    let visibleCount = 0;
    let totalBudget = 0;
    let hiddenCount = state.includeHidden ? 0 : (state.serverTotals ? state.serverTotals.hidden_count : 0);
    for (const r of state.rows) {
      if (r.hidden) {
        hiddenCount++;
      } else {
        visibleCount++;
        const bp = parseBudgetVal(r.budget_text);
        if (bp.ok) totalBudget += bp.value;
      }
    }
    totalBudget = round4(totalBudget);

    $('show-hidden-label').textContent = t('hb.ui.show_hidden', { n: hiddenCount });

    const counters = [
      counterChip(t('hb.ui.counter_visible'), visibleCount),
      counterChip(t('hb.ui.counter_budget'), totalBudget),
    ];
    if (state.isAdmin && hiddenCount > 0) {
      counters.push(counterChip(t('hb.ui.counter_hidden'), hiddenCount));
    }
    if (changes.totalChanges > 0) {
      counters.push(counterChip(t('hb.ui.counter_changes'), changes.totalChanges, true));
    }
    $('counters').replaceChildren(...counters);

    const reorderBlocked = state.isAdmin && state.filter.trim() !== '';
    $('mode-hint').textContent = reorderBlocked
      ? t('hb.ui.hint_reorder_disabled')
      : (state.isAdmin ? t('hb.ui.hint_admin') : t('hb.ui.hint_standard'));

    $('btn-discard').disabled = state.saving || changes.totalChanges === 0;
    $('btn-confirm').disabled = state.saving || changes.totalChanges === 0 || changes.invalid > 0;
    $('btn-confirm').textContent = state.saving ? t('hb.ui.saving') : t('hb.ui.confirm');
  }

  function counterChip(label, val, warn) {
    return el('span', { class: 'counter' + (warn ? ' warn' : '') }, label + ' ', el('b', { text: String(val) }));
  }

  function renderAll() {
    renderHeaderState();
    const visible = filteredRows();
    const tbody = $('table-body');
    const empty = $('table-empty');
    if (visible.length === 0) {
      tbody.replaceChildren();
      empty.textContent = state.rows.length === 0 ? t('hb.ui.empty_rows') : t('hb.ui.empty_filter');
      empty.classList.remove('hidden');
      return;
    }
    empty.classList.add('hidden');
    const canReorder = state.isAdmin && state.filter.trim() === '';
    const frag = document.createDocumentFragment();
    visible.forEach((row) => {
      const idx = state.rows.indexOf(row);
      frag.appendChild(buildRowElement(row, idx, canReorder));
    });
    tbody.replaceChildren(frag);
  }

  function buildRowElement(row, idx, canReorder) {
    const errs = rowErrors(row);
    const modified = isRowModified(row);
    const tr = el('tr', {
      'data-row-id': row.id,
      class: [
        modified ? 'is-dirty' : '',
        row.isNew ? 'is-new' : '',
        row.hidden ? 'is-hidden-row' : '',
      ].filter(Boolean).join(' '),
    });

    if (state.isAdmin) {
      if (canReorder) wireDragAndDrop(tr, row);
      const handle = el('span', {
        class: 'drag-handle' + (canReorder ? '' : ' is-disabled'),
        title: canReorder ? t('hb.ui.drag_title') : t('hb.ui.hint_reorder_disabled'),
        text: '⋮⋮',
        'aria-hidden': 'true',
      });
      const upBtn = el('button', {
        type: 'button',
        class: 'move-btn',
        text: '▲',
        title: t('hb.ui.move_up'),
        'aria-label': t('hb.ui.move_up'),
        disabled: !canReorder || idx === 0,
        onclick: () => moveRow(idx, idx - 1),
      });
      const downBtn = el('button', {
        type: 'button',
        class: 'move-btn',
        text: '▼',
        title: t('hb.ui.move_down'),
        'aria-label': t('hb.ui.move_down'),
        disabled: !canReorder || idx === state.rows.length - 1,
        onclick: () => moveRow(idx, idx + 1),
      });
      const orderCell = el('div', { class: 'order-cell' },
        handle,
        el('span', { class: 'seq-num', text: String(idx + 1) }),
        el('span', { class: 'move-btns' }, upBtn, downBtn),
      );
      tr.appendChild(el('td', { class: 'col-order' }, orderCell));
    }

    // Station + ID
    tr.appendChild(el('td', { class: 'col-station' },
      el('span', { class: 'station-code', text: row.station_code != null ? String(row.station_code) : '—' }),
      el('span', { class: 'row-id', text: row.id }),
    ));

    // Machine input
    const machineErr = el('span', { class: 'cell-err' + (errs.machine ? '' : ' hidden'), text: errs.machine || '' });
    const machineInput = el('input', {
      type: 'text',
      class: 'cell-input' + (errs.machine ? ' is-invalid' : ''),
      maxlength: '120',
      value: row.machine,
      'aria-label': t('hb.ui.col_machine'),
      oninput: (e) => {
        row.machine = e.target.value;
        updateRowVisuals(tr, row, machineInput, machineErr, budgetInput, budgetErr);
      },
    });
    tr.appendChild(el('td', { class: 'col-machine' }, machineInput, machineErr));

    // Type select
    const typeSelect = el('select', {
      class: 'cell-select',
      'aria-label': t('hb.ui.col_type'),
      onchange: (e) => {
        row.type = e.target.value;
        updateRowVisuals(tr, row, machineInput, machineErr, budgetInput, budgetErr);
      },
    },
      el('option', { value: 'Routine', text: t('hb.ui.type_routine') }),
      el('option', { value: 'Non-routine', text: t('hb.ui.type_non_routine') }),
    );
    if (row.type !== 'Routine' && row.type !== 'Non-routine' && row.type) {
      typeSelect.appendChild(el('option', { value: row.type, text: row.type }));
    }
    typeSelect.value = row.type;
    tr.appendChild(el('td', { class: 'col-type' }, typeSelect));

    // Category input with shared datalist
    const catInput = el('input', {
      type: 'text',
      class: 'cell-input',
      maxlength: '80',
      list: 'category-suggestions',
      value: row.category,
      'aria-label': t('hb.ui.col_category'),
      oninput: (e) => {
        row.category = e.target.value;
        updateRowVisuals(tr, row, machineInput, machineErr, budgetInput, budgetErr);
      },
      onchange: () => refreshCategoryDatalist(),
    });
    tr.appendChild(el('td', { class: 'col-category' }, catInput));

    // Budget HC input
    const budgetErr = el('span', { class: 'cell-err' + (errs.budget ? '' : ' hidden'), text: errs.budget || '' });
    const budgetInput = el('input', {
      type: 'text',
      inputmode: 'decimal',
      class: 'cell-input num' + (errs.budget ? ' is-invalid' : ''),
      placeholder: '0',
      value: row.budget_text,
      'aria-label': t('hb.ui.col_budget'),
      oninput: (e) => {
        row.budget_text = e.target.value;
        updateRowVisuals(tr, row, machineInput, machineErr, budgetInput, budgetErr);
      },
    });
    tr.appendChild(el('td', { class: 'col-budget' }, budgetInput, budgetErr));

    // Cost Center (read-only)
    tr.appendChild(el('td', { class: 'col-cc' }, el('span', { class: 'cc-val', text: row.cost_center || '—' })));

    // Updated (read-only)
    tr.appendChild(el('td', { class: 'col-updated' },
      row.isNew
        ? el('span', { class: 'chip ok', text: t('hb.ui.not_saved_yet') })
        : [
          el('span', { class: 'upd-time', text: row.update_time || '—' }),
          el('span', { class: 'upd-user', text: row.update_user || '', title: row.update_user || '' }),
        ],
    ));

    // Status & Hide/Restore/Remove (Admin only)
    if (state.isAdmin) {
      const chip = statusChip(row);
      const actionBtn = row.isNew
        ? el('button', {
          type: 'button',
          class: 'row-btn danger',
          text: t('hb.ui.btn_remove_new'),
          onclick: () => {
            state.rows = state.rows.filter((x) => x.id !== row.id);
            renderAll();
          },
        })
        : el('button', {
          type: 'button',
          class: 'row-btn',
          text: row.hidden ? t('hb.ui.btn_restore') : t('hb.ui.btn_hide'),
          onclick: () => {
            row.hidden = !row.hidden;
            renderAll();
          },
        });
      tr.appendChild(el('td', { class: 'col-status' }, el('div', { class: 'status-cell' }, chip, actionBtn)));
    }

    return tr;
  }

  function statusChip(row) {
    if (row.isNew) return el('span', { class: 'chip ok', text: t('hb.ui.status_new') });
    if (row.hidden) return el('span', { class: 'chip warn', text: t('hb.ui.status_hidden') });
    if (isRowModified(row)) return el('span', { class: 'chip info', text: t('hb.ui.status_modified') });
    return el('span', { class: 'chip muted', text: t('hb.ui.status_active') });
  }

  function updateRowVisuals(tr, row, machineInput, machineErr, budgetInput, budgetErr) {
    const errs = rowErrors(row);
    machineInput.classList.toggle('is-invalid', !!errs.machine);
    machineErr.textContent = errs.machine || '';
    machineErr.classList.toggle('hidden', !errs.machine);

    budgetInput.classList.toggle('is-invalid', !!errs.budget);
    budgetErr.textContent = errs.budget || '';
    budgetErr.classList.toggle('hidden', !errs.budget);

    const dirty = isRowModified(row);
    tr.classList.toggle('is-dirty', dirty);
    if (state.isAdmin) {
      const statusCell = tr.querySelector('.status-cell');
      if (statusCell && statusCell.firstChild) {
        statusCell.replaceChild(statusChip(row), statusCell.firstChild);
      }
    }
    renderHeaderState();
  }

  // ---- drag-and-drop & move up/down ----------------------------------------------------------------------------

  function moveRow(fromIdx, toIdx) {
    if (fromIdx < 0 || toIdx < 0 || fromIdx >= state.rows.length || toIdx >= state.rows.length || fromIdx === toIdx) return;
    const [item] = state.rows.splice(fromIdx, 1);
    state.rows.splice(toIdx, 0, item);
    renderAll();
  }

  function wireDragAndDrop(tr, row) {
    tr.draggable = true;
    tr.addEventListener('dragstart', (e) => {
      // Do not hijack text selection inside inputs or selects.
      if (e.target && (e.target.tagName === 'INPUT' || e.target.tagName === 'SELECT' || e.target.tagName === 'BUTTON')) {
        e.preventDefault();
        return;
      }
      state.dragID = row.id;
      tr.classList.add('is-dragging');
      if (e.dataTransfer) {
        e.dataTransfer.effectAllowed = 'move';
        e.dataTransfer.setData('text/plain', row.id);
      }
    });
    tr.addEventListener('dragend', () => {
      state.dragID = null;
      tr.classList.remove('is-dragging');
      clearDropIndicators();
    });
    tr.addEventListener('dragover', (e) => {
      if (!state.dragID || state.dragID === row.id) return;
      e.preventDefault();
      if (e.dataTransfer) e.dataTransfer.dropEffect = 'move';
      clearDropIndicators();
      const rect = tr.getBoundingClientRect();
      const after = e.clientY > rect.top + rect.height / 2;
      tr.classList.add(after ? 'drop-after' : 'drop-before');
    });
    tr.addEventListener('dragleave', () => {
      tr.classList.remove('drop-before', 'drop-after');
    });
    tr.addEventListener('drop', (e) => {
      e.preventDefault();
      const fromID = state.dragID || (e.dataTransfer && e.dataTransfer.getData('text/plain'));
      const after = tr.classList.contains('drop-after');
      clearDropIndicators();
      if (!fromID || fromID === row.id) return;
      const fromIdx = state.rows.findIndex((x) => x.id === fromID);
      let toIdx = state.rows.findIndex((x) => x.id === row.id);
      if (fromIdx < 0 || toIdx < 0) return;
      const [moved] = state.rows.splice(fromIdx, 1);
      if (fromIdx < toIdx) toIdx--;
      if (after) toIdx++;
      state.rows.splice(toIdx, 0, moved);
      renderAll();
    });
  }

  function clearDropIndicators() {
    document.querySelectorAll('#table-body tr.drop-before, #table-body tr.drop-after').forEach((node) => {
      node.classList.remove('drop-before', 'drop-after');
    });
  }

  // ---- Add Machine / Activity modal ----------------------------------------------------------------------------

  function openAddModal() {
    const def = state.defaults || {};
    $('add-error').classList.add('hidden');
    $('add-error').textContent = '';
    $('add-station').value = '';
    $('add-machine').value = '';
    $('add-type').value = 'Routine';
    $('add-budget').value = '';
    $('add-category').value = '';
    $('add-cost-center').value = def.cost_center || '';
    $('add-process-code').value = def.process_code != null ? String(def.process_code) : '';
    updateStationPreview();
    $('add-dialog').showModal();
    $('add-station').focus();
  }

  function updateStationPreview() {
    const preview = $('add-id-preview');
    const raw = $('add-station').value.trim();
    if (!raw) {
      preview.className = 'field-hint';
      preview.textContent = t('hb.ui.add_station_hint', { process: state.process }) + ` ${state.process}_<station_code>`;
      return;
    }
    const sc = Number(raw);
    if (!Number.isInteger(sc) || sc < 1 || sc > 32767) {
      preview.className = 'field-hint err';
      preview.textContent = t('hb.ui.err_station_required');
      return;
    }
    if (allUsedStationCodes().has(sc)) {
      preview.className = 'field-hint err';
      preview.textContent = t('hb.ui.err_station_dup', { code: sc, process: state.process });
      return;
    }
    preview.className = 'field-hint';
    preview.textContent = t('hb.ui.add_station_hint', { process: state.process }) + ` ${state.process}_${sc}`;
  }

  function submitAddModal(e) {
    e.preventDefault();
    const showErr = (msg) => {
      $('add-error').textContent = msg;
      $('add-error').classList.remove('hidden');
    };
    const rawStation = $('add-station').value.trim();
    const sc = Number(rawStation);
    if (!rawStation || !Number.isInteger(sc) || sc < 1 || sc > 32767) {
      showErr(t('hb.ui.err_station_required'));
      $('add-station').focus();
      return;
    }
    if (allUsedStationCodes().has(sc)) {
      showErr(t('hb.ui.err_station_dup', { code: sc, process: state.process }));
      $('add-station').focus();
      return;
    }
    const machine = $('add-machine').value.trim();
    if (!machine) {
      showErr(t('hb.ui.err_machine_required'));
      $('add-machine').focus();
      return;
    }
    const budgetText = $('add-budget').value.trim();
    if (!parseBudgetVal(budgetText).ok) {
      showErr(t('hb.ui.err_budget_format'));
      $('add-budget').focus();
      return;
    }
    const cc = $('add-cost-center').value.trim();
    if (cc.length > 4) {
      showErr(t('hb.ui.err_cost_center_len'));
      $('add-cost-center').focus();
      return;
    }
    let procCode = null;
    const rawPC = $('add-process-code').value.trim();
    if (rawPC !== '') {
      const pc = Number(rawPC);
      if (!Number.isInteger(pc) || pc < -32768 || pc > 32767) {
        showErr(t('hb.ui.err_process_code'));
        $('add-process-code').focus();
        return;
      }
      procCode = pc;
    }

    const newID = `${state.process}_${sc}`;
    state.rows.push({
      id: newID,
      isNew: true,
      station_code: sc,
      cost_center: cc,
      process_code: procCode,
      machine: machine,
      type: $('add-type').value || 'Routine',
      category: $('add-category').value.trim(),
      budget_text: budgetText,
      hidden: false,
      update_time: '',
      update_user: '',
      orig: null,
    });
    $('add-dialog').close();
    refreshCategoryDatalist();
    renderAll();
    const addedEl = document.querySelector(`tr[data-row-id="${CSS.escape(newID)}"]`);
    if (addedEl) addedEl.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
  }

  // ---- Confirm / Discard / Switch process ----------------------------------------------------------------------

  async function onConfirm() {
    const ch = computeChanges();
    if (ch.totalChanges === 0) return;
    if (ch.invalid > 0) {
      dg.toast(t('hb.ui.err_fix_before_save'), 'err');
      return;
    }

    const items = [];
    if (ch.added > 0) items.push(el('li', { text: t('hb.ui.confirm_added', { n: ch.added }) }));
    if (ch.fieldEdited > 0) items.push(el('li', { text: t('hb.ui.confirm_updated', { n: ch.fieldEdited }) }));
    if (ch.hiddenCount > 0) items.push(el('li', { text: t('hb.ui.confirm_hidden', { n: ch.hiddenCount }) }));
    if (ch.restoredCount > 0) items.push(el('li', { text: t('hb.ui.confirm_restored', { n: ch.restoredCount }) }));
    if (ch.reordered) items.push(el('li', { text: t('hb.ui.confirm_reordered', { process: state.process }) }));

    const body = el('div', null,
      el('p', { text: t('hb.ui.confirm_summary', { process: state.process }) }),
      el('ul', { class: 'summary-list' }, ...items),
    );
    const ok = await ask(t('hb.ui.confirm_title'), body, t('hb.ui.confirm_yes'), t('ui.cancel'));
    if (!ok) return;

    const payload = {
      process: state.process,
      creates: [],
      updates: [],
    };
    for (const r of state.rows) {
      if (r.isNew) {
        payload.creates.push({
          station_code: r.station_code,
          cost_center: r.cost_center,
          process_code: r.process_code,
          type: r.type,
          machine: r.machine.trim(),
          category: r.category.trim(),
          budget_head_count: r.budget_text.trim(),
          hidden: r.hidden,
        });
      } else if (isRowModified(r)) {
        const up = {
          id: r.id,
          machine: r.machine.trim(),
          type: r.type,
          category: r.category.trim(),
          budget_head_count: r.budget_text.trim(),
        };
        if (state.isAdmin) up.hidden = r.hidden;
        payload.updates.push(up);
      }
    }
    if (state.isAdmin && ch.reordered) {
      payload.order = state.rows.map((r) => r.id);
    }

    state.saving = true;
    renderHeaderState();
    try {
      const res = await dg.call('POST', 'api/save', payload);
      state.saving = false;
      const msg = t('hb.ui.saved_ok', { inserted: res.inserted || 0, updated: res.updated || 0 });
      if (res.log_warning) {
        dg.toast(msg + '\n' + res.log_warning, 'warn');
      } else {
        dg.toast(msg, 'ok');
      }
      await loadTable(false);
    } catch (err) {
      state.saving = false;
      renderHeaderState();
      if (err.status === 401) sessionExpired();
      else dg.toast(err.message, 'err');
    }
  }

  async function onDiscard() {
    if (computeChanges().totalChanges === 0) return;
    const ok = await ask(t('hb.ui.discard_title'), el('p', { text: t('hb.ui.discard_text') }), t('hb.ui.discard_yes'), t('ui.cancel'));
    if (ok) await loadTable(false);
  }

  async function onChangeProcess() {
    if (computeChanges().totalChanges > 0) {
      const ok = await ask(t('hb.ui.leave_title'), el('p', { text: t('hb.ui.leave_text', { process: state.process }) }), t('hb.ui.leave_yes'), t('ui.cancel'));
      if (!ok) return;
    }
    renderPicker();
  }

  async function onToggleHidden() {
    const next = $('show-hidden').checked;
    if (computeChanges().totalChanges > 0) {
      const ok = await ask(t('hb.ui.discard_title'), el('p', { text: t('hb.ui.discard_text') }), t('hb.ui.discard_yes'), t('ui.cancel'));
      if (!ok) {
        $('show-hidden').checked = state.includeHidden;
        return;
      }
    }
    state.includeHidden = next;
    await loadTable(false);
  }

  // ---- event bindings ------------------------------------------------------------------------------------------

  $('change-process').addEventListener('click', onChangeProcess);
  $('search-input').addEventListener('input', (e) => {
    state.filter = e.target.value;
    renderAll();
  });
  $('show-hidden').addEventListener('change', onToggleHidden);
  $('btn-add').addEventListener('click', openAddModal);
  $('btn-discard').addEventListener('click', onDiscard);
  $('btn-confirm').addEventListener('click', onConfirm);

  $('add-station').addEventListener('input', updateStationPreview);
  $('add-cancel').addEventListener('click', () => $('add-dialog').close());
  $('add-form').addEventListener('submit', submitAddModal);

  window.addEventListener('beforeunload', (e) => {
    if (state.process && computeChanges().totalChanges > 0) {
      e.preventDefault();
      e.returnValue = '';
    }
  });

  init();
})();
