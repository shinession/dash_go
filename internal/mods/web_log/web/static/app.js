/* Web Log module client: filter controls, ECharts 5 stacked bar + distinct visitor line chart,
   full breakdown table, and administrator-only live Redis web_log viewer. */
(function () {
  'use strict';

  var dg = window.dashGo;
  if (!dg) { return; }
  var t = dg.t;
  var API = dg.base + '/web_log/api';

  var MAX_DAY_SPAN = 366;
  var LIVE_POLL_MS = 5000;

  var PALETTE_LIGHT = [
    '#0b63c5', '#009688', '#7c4dff', '#e65100', '#2e7d32',
    '#d81b60', '#00838f', '#5e35b1', '#c62828', '#3949ab',
    '#0277bd', '#558b2f', '#6d4c41', '#ad1457', '#455a64', '#9e9e9e'
  ];
  var PALETTE_DARK = [
    '#4da3ff', '#26c6da', '#b388ff', '#ff9e40', '#69db7c',
    '#f06595', '#4dd0e1', '#9775fa', '#ff7875', '#748ffc',
    '#3bc9db', '#a9e34b', '#d0bfff', '#ffa8a8', '#90a4ae', '#adb5bd'
  ];

  var state = {
    meta: null,
    trend: null,
    mode: 'day',
    groupBy: 'auto',
    from: '',
    to: '',
    customDates: false,
    preset: '30d',
    app: '',
    hosts: [],
    categories: [],
    excludeIdle: false,
    breakdownFilter: '',
    live: null,
    liveTimer: null,
    liveInFlight: false,
    trendReqSeq: 0
  };

  var chart = null;

  function $(id) { return document.getElementById(id); }

  // ---- date helpers --------------------------------------------------------------------------------------------

  function pad2(n) { return n < 10 ? '0' + n : String(n); }

  function formatDateLocal(d) {
    return d.getFullYear() + '-' + pad2(d.getMonth() + 1) + '-' + pad2(d.getDate());
  }

  function parseYMD(s) {
    if (!s || !/^\d{4}-\d{2}-\d{2}$/.test(s)) { return null; }
    var p = s.split('-');
    var d = new Date(Number(p[0]), Number(p[1]) - 1, Number(p[2]));
    if (d.getFullYear() !== Number(p[0]) || d.getMonth() !== Number(p[1]) - 1 || d.getDate() !== Number(p[2])) {
      return null;
    }
    return d;
  }

  function addDays(d, n) {
    var r = new Date(d.getFullYear(), d.getMonth(), d.getDate() + n);
    return r;
  }

  function mondayOf(d) {
    var offset = (d.getDay() + 6) % 7;
    return addDays(d, -offset);
  }

  function defaultRange(mode, toStr) {
    var toDate = parseYMD(toStr) || new Date();
    var fromDate;
    if (mode === 'week') {
      fromDate = mondayOf(addDays(toDate, -11 * 7));
    } else if (mode === 'month') {
      fromDate = new Date(toDate.getFullYear(), toDate.getMonth() - 11, 1);
    } else {
      fromDate = addDays(toDate, -29);
    }
    return { from: formatDateLocal(fromDate), to: formatDateLocal(toDate) };
  }

  function spanDays(fromStr, toStr) {
    var a = parseYMD(fromStr);
    var b = parseYMD(toStr);
    if (!a || !b) { return 0; }
    return Math.round((b.getTime() - a.getTime()) / 86400000) + 1;
  }

  // ---- number formatting ---------------------------------------------------------------------------------------

  function fmtInt(n) {
    var num = Number(n) || 0;
    return num.toLocaleString(dg.lang === 'zh' ? 'zh-CN' : 'en-US');
  }

  function fmtShort(n) {
    var v = Number(n) || 0;
    if (v >= 1e9) { return (v / 1e9).toFixed(1).replace(/\.0$/, '') + 'B'; }
    if (v >= 1e6) { return (v / 1e6).toFixed(1).replace(/\.0$/, '') + 'M'; }
    if (v >= 1e3) { return (v / 1e3).toFixed(v >= 1e4 ? 0 : 1).replace(/\.0$/, '') + 'K'; }
    return String(v);
  }

  function escHTML(s) {
    return String(s == null ? '' : s)
      .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
  }

  function cssVar(name) {
    return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  }

  function isDark() {
    return document.documentElement.getAttribute('data-theme') === 'dark';
  }

  // ---- URL state sync ------------------------------------------------------------------------------------------

  function readInitialURL() {
    var params = new URLSearchParams(window.location.search);
    var m = (params.get('mode') || '').toLowerCase();
    if (m === 'by_day' || m === 'day') { state.mode = 'day'; }
    else if (m === 'by_week' || m === 'week') { state.mode = 'week'; }
    else if (m === 'by_month' || m === 'month') { state.mode = 'month'; }

    var gb = (params.get('group_by') || '').toLowerCase();
    if (gb === 'host' || gb === 'auto') { state.groupBy = gb; }

    var app = params.get('app') || params.get('db') || '';
    if (app && app.toLowerCase() !== 'all') { state.app = app; }

    var from = params.get('from') || '';
    var to = params.get('to') || '';
    if (parseYMD(from) && parseYMD(to)) {
      state.from = from;
      state.to = to;
      state.customDates = true;
      state.preset = '';
    }

    var hosts = [];
    params.getAll('host').forEach(function (v) {
      v.split(',').forEach(function (item) {
        item = item.trim();
        if (item && hosts.indexOf(item) < 0) { hosts.push(item); }
      });
    });
    state.hosts = hosts;

    var cats = [];
    params.getAll('category').forEach(function (v) {
      v.split(',').forEach(function (item) {
        item = item.trim();
        if (item && cats.indexOf(item) < 0) { cats.push(item); }
      });
    });
    state.categories = cats;

    var ei = params.get('exclude_idle') || '';
    state.excludeIdle = (ei === '1' || ei.toLowerCase() === 'true');
  }

  function writeURL() {
    var p = new URLSearchParams();
    p.set('mode', state.mode);
    if (state.from) { p.set('from', state.from); }
    if (state.to) { p.set('to', state.to); }
    if (state.app) { p.set('app', state.app); }
    if (state.groupBy !== 'auto') { p.set('group_by', state.groupBy); }
    if (state.hosts.length > 0) { p.set('host', state.hosts.join(',')); }
    if (state.categories.length > 0) { p.set('category', state.categories.join(',')); }
    if (state.excludeIdle) { p.set('exclude_idle', '1'); }
    var next = window.location.pathname + '?' + p.toString();
    try { window.history.replaceState(null, '', next); } catch (e) { /* ignore */ }
  }

  // ---- controls rendering --------------------------------------------------------------------------------------

  function syncControlUI() {
    $('date-from').value = state.from;
    $('date-to').value = state.to;

    document.querySelectorAll('[data-preset]').forEach(function (btn) {
      btn.classList.toggle('is-active', btn.getAttribute('data-preset') === state.preset);
    });
    document.querySelectorAll('[data-mode]').forEach(function (btn) {
      btn.classList.toggle('is-active', btn.getAttribute('data-mode') === state.mode);
    });
    document.querySelectorAll('[data-groupby]').forEach(function (btn) {
      btn.classList.toggle('is-active', btn.getAttribute('data-groupby') === state.groupBy);
    });
    $('app-select').value = state.app;
    $('exclude-idle').checked = state.excludeIdle;

    updateMultiTrigger('host', state.hosts, t('wl.ui.host_all'), 'wl.ui.host_selected');
    updateMultiTrigger('cat', state.categories, t('wl.ui.category_all'), 'wl.ui.category_selected');

    var hasCustom = Boolean(state.app || state.hosts.length || state.categories.length || state.excludeIdle || state.groupBy !== 'auto');
    $('btn-reset').classList.toggle('hidden', !hasCustom);
  }

  function updateMultiTrigger(prefix, selected, allLabel, selectedKey) {
    var trig = $(prefix + '-trigger');
    var txt = $(prefix + '-trigger-text');
    if (selected.length === 0) {
      txt.textContent = allLabel;
      trig.classList.remove('has-filter');
    } else if (selected.length === 1) {
      txt.textContent = selected[0];
      trig.classList.add('has-filter');
    } else {
      txt.textContent = t(selectedKey, { n: selected.length });
      trig.classList.add('has-filter');
    }
  }

  function populateAppSelect() {
    var sel = $('app-select');
    var apps = (state.meta && state.meta.apps) || [];
    var frag = document.createDocumentFragment();
    var optAll = document.createElement('option');
    optAll.value = '';
    optAll.textContent = t('wl.ui.all_apps');
    frag.appendChild(optAll);
    apps.forEach(function (a) {
      var o = document.createElement('option');
      o.value = a;
      o.textContent = a;
      frag.appendChild(o);
    });
    if (state.app && apps.indexOf(state.app) < 0) {
      var extra = document.createElement('option');
      extra.value = state.app;
      extra.textContent = state.app;
      frag.appendChild(extra);
    }
    sel.replaceChildren(frag);
    sel.value = state.app;
  }

  function availableCategories() {
    if (!state.meta) { return []; }
    if (state.app && state.meta.categories_by_app && state.meta.categories_by_app[state.app]) {
      return state.meta.categories_by_app[state.app];
    }
    return state.meta.all_categories || [];
  }

  function renderMultiList(prefix, items, selected, searchVal, onChange) {
    var list = $(prefix + '-options');
    var needle = (searchVal || '').trim().toLowerCase();
    var frag = document.createDocumentFragment();
    items.forEach(function (item) {
      if (needle && item.toLowerCase().indexOf(needle) < 0) { return; }
      var label = document.createElement('label');
      label.className = 'multi-item';
      var cb = document.createElement('input');
      cb.type = 'checkbox';
      cb.value = item;
      cb.checked = selected.indexOf(item) >= 0;
      cb.addEventListener('change', function () {
        var idx = selected.indexOf(item);
        if (cb.checked && idx < 0) { selected.push(item); }
        else if (!cb.checked && idx >= 0) { selected.splice(idx, 1); }
        selected.sort();
        onChange();
      });
      var span = document.createElement('span');
      span.textContent = item;
      span.title = item;
      label.appendChild(cb);
      label.appendChild(span);
      frag.appendChild(label);
    });
    list.replaceChildren(frag);
  }

  function populateHostList() {
    var hosts = (state.meta && state.meta.hosts) || [];
    renderMultiList('host', hosts, state.hosts, $('host-search').value, function () {
      syncControlUI();
      loadTrend(false);
    });
  }

  function populateCatList() {
    var cats = availableCategories();
    renderMultiList('cat', cats, state.categories, $('cat-search').value, function () {
      syncControlUI();
      loadTrend(false);
    });
  }

  function closeAllPopovers(exceptId) {
    ['host', 'cat'].forEach(function (prefix) {
      var pop = $(prefix + '-popover');
      var trig = $(prefix + '-trigger');
      if (pop.id !== exceptId) {
        pop.classList.add('hidden');
        trig.setAttribute('aria-expanded', 'false');
      }
    });
  }

  function bindPopover(prefix, getItems, getSelected, setSelected, rerender) {
    var trig = $(prefix + '-trigger');
    var pop = $(prefix + '-popover');
    var search = $(prefix + '-search');

    trig.addEventListener('click', function (e) {
      e.stopPropagation();
      var open = pop.classList.contains('hidden');
      closeAllPopovers(open ? pop.id : '');
      pop.classList.toggle('hidden', !open);
      trig.setAttribute('aria-expanded', open ? 'true' : 'false');
      if (open) {
        rerender();
        search.focus();
      }
    });
    pop.addEventListener('click', function (e) { e.stopPropagation(); });
    search.addEventListener('input', rerender);
    $(prefix + '-select-all').addEventListener('click', function () {
      setSelected(getItems().slice());
      rerender();
      syncControlUI();
      loadTrend(false);
    });
    $(prefix + '-clear').addEventListener('click', function () {
      setSelected([]);
      rerender();
      syncControlUI();
      loadTrend(false);
    });
  }

  // ---- API loading ---------------------------------------------------------------------------------------------

  function loadMeta(refresh) {
    var url = API + '/meta' + (refresh ? '?refresh=1' : '');
    return dg.call('GET', url).then(function (meta) {
      state.meta = meta;
      if (!state.from || !state.to) {
        var def = defaultRange(state.mode, meta.default_to);
        state.from = def.from;
        state.to = def.to;
        state.preset = state.mode === 'day' ? '30d' : (state.mode === 'month' ? '12m' : '');
      }
      if (meta.min_date && meta.max_date) {
        $('data-span').textContent = t('wl.ui.data_span', { min: meta.min_date, max: meta.max_date });
      }
      populateAppSelect();
      populateHostList();
      populateCatList();
      syncControlUI();

      if (meta.is_admin) {
        initAdminLivePanel();
      }
      return meta;
    });
  }

  function buildTrendURL(refresh) {
    var p = new URLSearchParams();
    p.set('mode', state.mode);
    if (state.from) { p.set('from', state.from); }
    if (state.to) { p.set('to', state.to); }
    if (state.app) { p.set('app', state.app); }
    if (state.groupBy !== 'auto') { p.set('group_by', state.groupBy); }
    if (state.hosts.length > 0) { p.set('host', state.hosts.join(',')); }
    if (state.categories.length > 0) { p.set('category', state.categories.join(',')); }
    if (state.excludeIdle) { p.set('exclude_idle', '1'); }
    if (refresh) { p.set('refresh', '1'); }
    return API + '/trend?' + p.toString();
  }

  function showChartOverlay(text) {
    var ov = $('chart-overlay');
    if (!text) {
      ov.classList.add('hidden');
      ov.textContent = '';
    } else {
      ov.textContent = text;
      ov.classList.remove('hidden');
    }
  }

  function loadTrend(refresh) {
    writeURL();
    var reqID = ++state.trendReqSeq;
    var btn = $('btn-refresh');
    btn.disabled = true;
    btn.textContent = t('wl.ui.refreshing');
    if (!state.trend) { showChartOverlay(t('wl.ui.loading')); }

    return dg.call('GET', buildTrendURL(refresh)).then(function (data) {
      if (reqID !== state.trendReqSeq) { return; }
      state.trend = data;
      renderCacheBadge(data);
      renderKPIs(data);
      renderChartHeader(data);
      renderChart(data);
      renderBreakdown(data);
    }, function (err) {
      if (reqID !== state.trendReqSeq) { return; }
      dg.toast(err.message, 'err');
      if (!state.trend) { showChartOverlay(err.message); }
    }).finally(function () {
      if (reqID === state.trendReqSeq) {
        btn.disabled = false;
        btn.textContent = t('wl.ui.refresh');
      }
    });
  }

  function renderCacheBadge(data) {
    var badge = $('cache-badge');
    if (!data || !data.generated_at) {
      badge.classList.add('hidden');
      return;
    }
    var timePart = data.generated_at.split(' ')[1] || data.generated_at;
    badge.textContent = data.cached
      ? t('wl.ui.cache_hit', { at: timePart })
      : t('wl.ui.cache_live', { at: timePart });
    badge.classList.remove('hidden', 'is-cached', 'is-live');
    badge.classList.add(data.cached ? 'is-cached' : 'is-live');
  }

  function modeLabel(mode) {
    if (mode === 'week') { return t('wl.ui.mode_week'); }
    if (mode === 'month') { return t('wl.ui.mode_month'); }
    return t('wl.ui.mode_day');
  }

  function avgKey(mode) {
    if (mode === 'week') { return 'wl.ui.kpi_avg_week'; }
    if (mode === 'month') { return 'wl.ui.kpi_avg_month'; }
    return 'wl.ui.kpi_avg_day';
  }

  function renderKPIs(data) {
    var sum = data.summary || {};
    var ak = avgKey(data.mode);
    $('kpi-actions').textContent = fmtInt(sum.total_actions);
    $('kpi-actions-sub').textContent = t(ak, { n: fmtInt(sum.avg_actions_period) });

    $('kpi-visitors').textContent = fmtInt(sum.unique_visitors);
    $('kpi-visitors-sub').textContent = t(ak, { n: fmtInt(sum.avg_visitors_period) });

    if (sum.peak_period_label) {
      $('kpi-peak').textContent = sum.peak_period_label;
      $('kpi-peak-sub').textContent = t('wl.ui.kpi_peak_actions', { n: fmtInt(sum.peak_period_actions) });
    } else {
      $('kpi-peak').textContent = t('wl.ui.kpi_none');
      $('kpi-peak-sub').textContent = '';
    }

    var scopeKey = data.effective_group_by === 'host' ? 'wl.ui.scope_hosts'
      : (data.effective_group_by === 'category' ? 'wl.ui.scope_categories' : 'wl.ui.scope_apps');
    $('kpi-scope').textContent = t(scopeKey, { n: fmtInt(sum.series_count) });
    $('kpi-scope-sub').textContent = data.from + ' ~ ' + data.to;
  }

  function renderChartHeader(data) {
    var ml = modeLabel(data.mode);
    var titleEl = $('chart-title');
    if (data.effective_group_by === 'host') {
      var scope = data.app || t('wl.ui.all_apps');
      titleEl.textContent = t('wl.ui.chart_title_host', { scope: scope, mode: ml });
      $('drill-hint').textContent = t('wl.ui.drill_hint_host');
    } else if (data.app) {
      titleEl.textContent = t('wl.ui.chart_title_one_app', { app: data.app, mode: ml });
      $('drill-hint').textContent = t('wl.ui.drill_hint_category');
    } else {
      titleEl.textContent = t('wl.ui.chart_title_all_app', { mode: ml });
      $('drill-hint').textContent = t('wl.ui.drill_hint_app');
    }

    $('btn-back-apps').classList.toggle('hidden', !data.app);
    $('btn-clear-host').classList.toggle('hidden', !(data.hosts && data.hosts.length > 0));
  }

  // ---- ECharts 5 rendering -------------------------------------------------------------------------------------

  function seriesDisplayName(s) {
    if (s.is_other) { return t('wl.ui.series_others', { n: s.other_count }); }
    return s.name;
  }

  function ensureChart() {
    if (chart) { return chart; }
    if (!window.echarts) { return null; }
    var el = $('trend-chart');
    chart = window.echarts.init(el, null, { renderer: 'canvas' });
    chart.on('click', handleChartClick);
    if (window.ResizeObserver) {
      new ResizeObserver(function () { if (chart) { chart.resize(); } }).observe(el);
    } else {
      window.addEventListener('resize', function () { if (chart) { chart.resize(); } });
    }
    return chart;
  }

  function handleChartClick(params) {
    if (!state.trend || !params || !params.seriesName) { return; }
    var totalName = t('wl.ui.series_total');
    var visitorName = t('wl.ui.series_visitors');
    var data = state.trend;

    if (params.seriesName === totalName || params.seriesName === visitorName) {
      if (data.app) {
        state.app = '';
        state.categories = [];
        populateCatList();
        syncControlUI();
        loadTrend(false);
      }
      return;
    }

    var matched = (data.series || []).find(function (s) { return seriesDisplayName(s) === params.seriesName; });
    if (!matched || matched.is_other) { return; }

    if (data.effective_group_by === 'app') {
      state.app = matched.name;
      state.categories = [];
      populateAppSelect();
      populateCatList();
      syncControlUI();
      loadTrend(false);
    } else if (data.effective_group_by === 'host') {
      if (state.hosts.length === 1 && state.hosts[0] === matched.name) {
        state.hosts = [];
      } else {
        state.hosts = [matched.name];
      }
      populateHostList();
      syncControlUI();
      loadTrend(false);
    } else if (data.effective_group_by === 'category') {
      if (state.categories.length === 1 && state.categories[0] === matched.name) {
        state.categories = [];
      } else {
        state.categories = [matched.name];
      }
      populateCatList();
      syncControlUI();
      loadTrend(false);
    }
  }

  function renderChart(data) {
    var inst = ensureChart();
    if (!inst) { return; }
    if (!data || !data.periods || data.periods.length === 0 || (data.summary && data.summary.total_actions === 0)) {
      inst.clear();
      showChartOverlay(t('wl.ui.no_data'));
      return;
    }
    showChartOverlay('');

    var dark = isDark();
    var palette = dark ? PALETTE_DARK : PALETTE_LIGHT;
    var textColor = cssVar('--text') || (dark ? '#e9ecef' : '#14181c');
    var mutedColor = cssVar('--muted') || (dark ? '#8b939e' : '#59636e');
    var lineColor = cssVar('--line') || (dark ? '#2a2e35' : '#d3dae3');
    var panelColor = cssVar('--panel') || (dark ? '#16181c' : '#ffffff');
    var visitorColor = dark ? '#ffa940' : '#d96b00';
    var totalLabelColor = cssVar('--green') || (dark ? '#95de64' : '#1a7a37');

    var periods = data.periods;
    var xLabels = periods.map(function (p) { return p.label; });
    var manyPoints = xLabels.length > 18;
    var barMaxWidth = xLabels.length > 45 ? 16 : (xLabels.length > 20 ? 24 : 38);

    var seriesOpts = [];
    var legendNames = [];

    (data.series || []).forEach(function (s, idx) {
      var disp = seriesDisplayName(s);
      legendNames.push(disp);
      seriesOpts.push({
        name: disp,
        type: 'bar',
        stack: 'actions',
        yAxisIndex: 0,
        barMaxWidth: barMaxWidth,
        emphasis: { focus: 'series' },
        itemStyle: { color: s.is_other ? mutedColor : palette[idx % palette.length] },
        data: (s.values || []).map(function (v) { return v > 0 ? v : null; })
      });
    });

    // Transparent line carrying shortened total labels above each stacked bar, preserving IE.py tab11 layout.
    var totalName = t('wl.ui.series_total');
    legendNames.push(totalName);
    seriesOpts.push({
      name: totalName,
      type: 'line',
      yAxisIndex: 0,
      symbol: 'circle',
      symbolSize: 1,
      showSymbol: true,
      lineStyle: { width: 0, opacity: 0 },
      itemStyle: { color: totalLabelColor, opacity: 0 },
      tooltip: { show: false },
      label: {
        show: xLabels.length <= 45,
        position: 'top',
        distance: 4,
        color: totalLabelColor,
        fontWeight: 700,
        fontSize: 11,
        formatter: function (p) { return p.value > 0 ? fmtShort(p.value) : ''; }
      },
      labelLayout: { hideOverlap: true },
      data: (data.total_actions || []).map(function (v) { return v > 0 ? v : null; })
    });

    var visitorName = t('wl.ui.series_visitors');
    legendNames.push(visitorName);
    seriesOpts.push({
      name: visitorName,
      type: 'line',
      yAxisIndex: 1,
      smooth: false,
      symbol: 'circle',
      symbolSize: xLabels.length > 45 ? 4 : 6,
      lineStyle: { width: 2.5, color: visitorColor },
      itemStyle: { color: visitorColor },
      label: {
        show: xLabels.length <= 24,
        position: 'top',
        distance: 6,
        color: visitorColor,
        fontWeight: 600,
        fontSize: 11,
        formatter: function (p) { return p.value > 0 ? fmtInt(p.value) : ''; }
      },
      labelLayout: { hideOverlap: true },
      data: data.visitor_counts || []
    });

    var option = {
      animationDuration: 300,
      backgroundColor: 'transparent',
      tooltip: {
        trigger: 'axis',
        confine: true,
        axisPointer: { type: 'shadow' },
        backgroundColor: panelColor,
        borderColor: lineColor,
        borderWidth: 1,
        textStyle: { color: textColor, fontSize: 12 },
        formatter: function (params) {
          if (!Array.isArray(params) || params.length === 0) { return ''; }
          var idx = params[0].dataIndex;
          var pb = periods[idx] || {};
          var head = '<strong>' + escHTML(pb.label || '') + '</strong>';
          if (pb.start && pb.end && pb.start !== pb.end) {
            head += ' <span style="opacity:.7">(' + escHTML(pb.start + ' ~ ' + pb.end) + ')</span>';
          }
          var totAct = (data.total_actions && data.total_actions[idx]) || 0;
          var totVis = (data.visitor_counts && data.visitor_counts[idx]) || 0;
          var rows = [
            head,
            '<div style="margin:4px 0 6px;padding-bottom:5px;border-bottom:1px solid ' + lineColor + ';display:flex;justify-content:space-between;gap:16px">' +
              '<span>' + escHTML(t('wl.ui.tooltip_total')) + ': <b>' + escHTML(fmtInt(totAct)) + '</b></span>' +
              '<span style="color:' + visitorColor + '">' + escHTML(t('wl.ui.tooltip_visitors')) + ': <b>' + escHTML(fmtInt(totVis)) + '</b></span>' +
            '</div>'
          ];
          var items = [];
          params.forEach(function (p) {
            if (p.seriesName === totalName || p.seriesName === visitorName) { return; }
            if (p.value == null || p.value <= 0) { return; }
            items.push(p);
          });
          items.sort(function (a, b) { return (b.value || 0) - (a.value || 0); });
          items.forEach(function (p) {
            var pct = totAct > 0 ? ((p.value * 100) / totAct).toFixed(1) + '%' : '0%';
            rows.push(
              '<div style="display:flex;align-items:center;justify-content:space-between;gap:14px;line-height:1.5">' +
                '<span>' + p.marker + escHTML(p.seriesName) + '</span>' +
                '<span style="font-variant-numeric:tabular-nums"><b>' + escHTML(fmtInt(p.value)) + '</b> <span style="opacity:.65">(' + pct + ')</span></span>' +
              '</div>'
            );
          });
          return rows.join('');
        }
      },
      toolbox: {
        right: 12,
        top: 0,
        iconStyle: { borderColor: mutedColor },
        emphasis: { iconStyle: { borderColor: textColor } },
        feature: {
          dataZoom: { yAxisIndex: 'none', title: { zoom: t('wl.ui.toolbox_zoom'), back: t('wl.ui.toolbox_zoom_back') } },
          restore: { title: t('wl.ui.toolbox_restore') },
          saveAsImage: { type: 'png', backgroundColor: panelColor, title: t('wl.ui.toolbox_save') }
        }
      },
      legend: {
        type: 'scroll',
        bottom: 4,
        left: 16,
        right: 16,
        textStyle: { color: textColor, fontSize: 12 },
        pageTextStyle: { color: mutedColor },
        pageIconColor: mutedColor,
        data: legendNames
      },
      grid: {
        top: 46,
        left: 62,
        right: 62,
        bottom: manyPoints ? 86 : 64
      },
      xAxis: {
        type: 'category',
        data: xLabels,
        axisLine: { lineStyle: { color: lineColor } },
        axisTick: { alignWithLabel: true, lineStyle: { color: lineColor } },
        axisLabel: {
          color: mutedColor,
          rotate: manyPoints ? 40 : 0,
          fontSize: 11
        }
      },
      yAxis: [
        {
          type: 'value',
          name: t('wl.ui.axis_actions'),
          min: 0,
          nameTextStyle: { color: totalLabelColor, fontWeight: 600, padding: [0, 0, 4, 0] },
          axisLine: { show: true, lineStyle: { color: totalLabelColor } },
          axisLabel: { color: mutedColor, formatter: function (v) { return fmtShort(v); } },
          splitLine: { lineStyle: { color: lineColor, type: 'dashed' } }
        },
        {
          type: 'value',
          name: t('wl.ui.axis_visitors'),
          min: 0,
          minInterval: 1,
          nameTextStyle: { color: visitorColor, fontWeight: 600, padding: [0, 0, 4, 0] },
          axisLine: { show: true, lineStyle: { color: visitorColor } },
          axisLabel: { color: visitorColor, formatter: function (v) { return fmtShort(v); } },
          splitLine: { show: false }
        }
      ],
      series: seriesOpts
    };

    if (xLabels.length > 35) {
      option.dataZoom = [
        { type: 'inside', xAxisIndex: 0 },
        {
          type: 'slider',
          xAxisIndex: 0,
          height: 18,
          bottom: 36,
          borderColor: lineColor,
          textStyle: { color: mutedColor }
        }
      ];
      option.grid.bottom = 104;
    }

    inst.setOption(option, true);
  }

  // ---- Breakdown table -----------------------------------------------------------------------------------------

  function renderBreakdown(data) {
    var eff = data.effective_group_by;
    if (eff === 'host') {
      $('breakdown-title').textContent = t('wl.ui.breakdown_title_host');
      $('col-name-head').textContent = t('wl.ui.col_name_host');
    } else if (eff === 'category') {
      $('breakdown-title').textContent = t('wl.ui.breakdown_title_category', { app: data.app || t('wl.ui.all_apps') });
      $('col-name-head').textContent = t('wl.ui.col_name_category');
    } else {
      $('breakdown-title').textContent = t('wl.ui.breakdown_title_app');
      $('col-name-head').textContent = t('wl.ui.col_name_app');
    }

    var items = (data.breakdown || []).slice();
    var needle = state.breakdownFilter.trim().toLowerCase();
    if (needle) {
      items = items.filter(function (it) { return it.name.toLowerCase().indexOf(needle) >= 0; });
    }
    $('breakdown-count').textContent = t('wl.ui.breakdown_count', { n: items.length });

    var body = $('breakdown-body');
    var empty = $('breakdown-empty');
    if (items.length === 0) {
      body.replaceChildren();
      empty.classList.remove('hidden');
      return;
    }
    empty.classList.add('hidden');

    var frag = document.createDocumentFragment();
    items.forEach(function (it, idx) {
      var tr = document.createElement('tr');
      tr.className = 'is-clickable';
      tr.addEventListener('click', function () {
        if (eff === 'app') {
          state.app = it.name;
          state.categories = [];
          populateAppSelect();
          populateCatList();
        } else if (eff === 'host') {
          state.hosts = (state.hosts.length === 1 && state.hosts[0] === it.name) ? [] : [it.name];
          populateHostList();
        } else {
          state.categories = (state.categories.length === 1 && state.categories[0] === it.name) ? [] : [it.name];
          populateCatList();
        }
        syncControlUI();
        loadTrend(false);
      });

      var tdRank = document.createElement('td');
      tdRank.className = 'col-rank';
      tdRank.textContent = String(idx + 1);

      var tdName = document.createElement('td');
      tdName.className = 'col-name';
      tdName.textContent = it.name;

      var tdAct = document.createElement('td');
      tdAct.className = 'col-num';
      tdAct.textContent = fmtInt(it.actions);

      var tdShare = document.createElement('td');
      tdShare.className = 'col-share';
      var wrap = document.createElement('div');
      wrap.className = 'share-cell';
      var bar = document.createElement('div');
      bar.className = 'share-bar';
      var fill = document.createElement('div');
      fill.className = 'share-fill';
      fill.style.width = Math.min(100, Math.max(0, it.share_pct)) + '%';
      bar.appendChild(fill);
      var pct = document.createElement('span');
      pct.className = 'share-pct';
      pct.textContent = it.share_pct.toFixed(1) + '%';
      wrap.appendChild(bar);
      wrap.appendChild(pct);
      tdShare.appendChild(wrap);

      var tdVis = document.createElement('td');
      tdVis.className = 'col-num';
      tdVis.textContent = fmtInt(it.visitors);

      tr.append(tdRank, tdName, tdAct, tdShare, tdVis);
      frag.appendChild(tr);
    });
    body.replaceChildren(frag);
  }

  // ---- Admin live Redis web_log panel --------------------------------------------------------------------------

  var liveInitialized = false;

  function initAdminLivePanel() {
    var sec = $('live-section');
    sec.classList.remove('hidden');
    if (liveInitialized) { return; }
    liveInitialized = true;

    // Format limit select labels in current language.
    Array.from($('live-limit').options).forEach(function (opt) {
      opt.textContent = t('wl.ui.live_limit_fmt', { n: opt.value });
    });

    $('live-refresh').addEventListener('click', function () { loadLive(); });
    $('live-limit').addEventListener('change', function () { loadLive(); });
    $('live-search').addEventListener('input', renderLiveTable);
    $('live-host').addEventListener('change', renderLiveTable);
    $('live-level').addEventListener('change', renderLiveTable);
    $('live-auto').addEventListener('change', syncLiveTimer);
    document.addEventListener('visibilitychange', syncLiveTimer);

    loadLive();
  }

  function syncLiveTimer() {
    if (state.liveTimer) {
      clearInterval(state.liveTimer);
      state.liveTimer = null;
    }
    if ($('live-auto').checked && !document.hidden) {
      state.liveTimer = setInterval(function () {
        if (!document.hidden && !state.liveInFlight) { loadLive(); }
      }, LIVE_POLL_MS);
    }
  }

  function loadLive() {
    if (state.liveInFlight) { return; }
    state.liveInFlight = true;
    var limit = $('live-limit').value || '500';
    var btn = $('live-refresh');
    btn.disabled = true;

    dg.call('GET', API + '/live?limit=' + encodeURIComponent(limit)).then(function (data) {
      state.live = data;
      populateLiveHostOptions(data.entries || []);
      renderLiveTable();
    }, function (err) {
      if (err.status === 401 || err.status === 403) {
        $('live-section').classList.add('hidden');
        $('live-auto').checked = false;
        syncLiveTimer();
      } else {
        dg.toast(err.message, 'err');
      }
    }).finally(function () {
      state.liveInFlight = false;
      btn.disabled = false;
    });
  }

  function populateLiveHostOptions(entries) {
    var sel = $('live-host');
    var prev = sel.value;
    var set = {};
    if (state.meta && Array.isArray(state.meta.hosts)) {
      state.meta.hosts.forEach(function (h) { set[h] = true; });
    }
    entries.forEach(function (e) { if (e.host) { set[e.host] = true; } });
    var hosts = Object.keys(set).sort();

    var frag = document.createDocumentFragment();
    var all = document.createElement('option');
    all.value = '';
    all.textContent = t('wl.ui.live_host_all');
    frag.appendChild(all);
    hosts.forEach(function (h) {
      var o = document.createElement('option');
      o.value = h;
      o.textContent = h;
      frag.appendChild(o);
    });
    sel.replaceChildren(frag);
    sel.value = set[prev] ? prev : '';
  }

  function statusClass(code) {
    if (!code) { return 'code-other'; }
    if (code.charAt(0) === '2' || code.charAt(0) === '3') { return 'code-ok'; }
    if (code.charAt(0) === '4') { return 'code-warn'; }
    if (code.charAt(0) === '5') { return 'code-err'; }
    return 'code-other';
  }

  function renderLiveTable() {
    var data = state.live;
    if (!data) { return; }
    var entries = data.entries || [];
    var needle = $('live-search').value.trim().toLowerCase();
    var hostF = $('live-host').value;
    var levelF = $('live-level').value;

    var filtered = entries.filter(function (e) {
      if (hostF && e.host !== hostF) { return false; }
      if (levelF && e.level !== levelF) { return false; }
      if (needle && (e.raw || '').toLowerCase().indexOf(needle) < 0) { return false; }
      return true;
    });

    var timePart = (data.fetched_at || '').split(' ')[1] || data.fetched_at || '';
    if (needle || hostF || levelF) {
      $('live-meta').textContent = t('wl.ui.live_meta_filtered', {
        matched: fmtInt(filtered.length),
        shown: fmtInt(entries.length),
        total: fmtInt(data.total),
        at: timePart
      });
    } else {
      $('live-meta').textContent = t('wl.ui.live_meta', {
        shown: fmtInt(entries.length),
        total: fmtInt(data.total),
        at: timePart
      });
    }

    var body = $('live-body');
    var empty = $('live-empty');
    if (filtered.length === 0) {
      body.replaceChildren();
      empty.classList.remove('hidden');
      return;
    }
    empty.classList.add('hidden');

    var frag = document.createDocumentFragment();
    filtered.forEach(function (e) {
      var tr = document.createElement('tr');

      var tdTime = document.createElement('td');
      tdTime.className = 'col-live-time';
      tdTime.textContent = e.timestamp || '—';

      var tdHost = document.createElement('td');
      tdHost.className = 'col-live-host';
      tdHost.textContent = e.host || '—';

      var tdIP = document.createElement('td');
      tdIP.className = 'col-live-ip';
      tdIP.textContent = e.ip || '—';

      var tdLvl = document.createElement('td');
      tdLvl.className = 'col-live-level';
      var lvlSpan = document.createElement('span');
      lvlSpan.className = 'lvl-chip lvl-' + (e.level || 'info');
      lvlSpan.textContent = e.level || 'info';
      tdLvl.appendChild(lvlSpan);

      var tdCode = document.createElement('td');
      tdCode.className = 'col-live-status';
      if (e.status) {
        var cSpan = document.createElement('span');
        cSpan.className = 'code-chip ' + statusClass(e.status);
        cSpan.textContent = e.status;
        tdCode.appendChild(cSpan);
      } else {
        tdCode.textContent = '—';
      }

      var tdMsg = document.createElement('td');
      tdMsg.className = 'col-live-msg';
      if (e.app && e.action) {
        var aSpan = document.createElement('span');
        aSpan.className = 'msg-app';
        aSpan.textContent = e.app + ':';
        tdMsg.appendChild(aSpan);
        tdMsg.appendChild(document.createTextNode(e.action));
      } else {
        tdMsg.textContent = e.message || e.raw || '';
      }

      tr.append(tdTime, tdHost, tdIP, tdLvl, tdCode, tdMsg);
      frag.appendChild(tr);
    });
    body.replaceChildren(frag);
  }

  // ---- event wiring --------------------------------------------------------------------------------------------

  function applyPreset(preset) {
    var baseTo = (state.meta && state.meta.default_to) || formatDateLocal(new Date());
    var toDate = parseYMD(baseTo) || new Date();
    var fromDate;
    if (preset === '7d') {
      fromDate = addDays(toDate, -6);
    } else if (preset === '30d') {
      fromDate = addDays(toDate, -29);
    } else if (preset === '90d') {
      fromDate = addDays(toDate, -89);
    } else if (preset === '12m') {
      fromDate = new Date(toDate.getFullYear(), toDate.getMonth() - 11, 1);
      if (state.mode === 'day' && spanDays(formatDateLocal(fromDate), formatDateLocal(toDate)) > MAX_DAY_SPAN) {
        state.mode = 'month';
      }
    } else {
      return;
    }
    state.from = formatDateLocal(fromDate);
    state.to = formatDateLocal(toDate);
    state.preset = preset;
    state.customDates = false;
    syncControlUI();
    loadTrend(false);
  }

  function bindEvents() {
    document.addEventListener('click', function () { closeAllPopovers(''); });

    bindPopover('host',
      function () { return (state.meta && state.meta.hosts) || []; },
      function () { return state.hosts; },
      function (next) { state.hosts = next; },
      populateHostList
    );
    bindPopover('cat',
      availableCategories,
      function () { return state.categories; },
      function (next) { state.categories = next; },
      populateCatList
    );

    $('date-from').addEventListener('change', onDateChange);
    $('date-to').addEventListener('change', onDateChange);

    document.querySelectorAll('[data-preset]').forEach(function (btn) {
      btn.addEventListener('click', function () {
        applyPreset(btn.getAttribute('data-preset'));
      });
    });

    document.querySelectorAll('[data-mode]').forEach(function (btn) {
      btn.addEventListener('click', function () {
        var nextMode = btn.getAttribute('data-mode');
        if (nextMode === state.mode) { return; }
        state.mode = nextMode;
        if (!state.customDates) {
          var def = defaultRange(nextMode, state.to || (state.meta && state.meta.default_to));
          state.from = def.from;
          state.to = def.to;
          state.preset = nextMode === 'day' ? '30d' : (nextMode === 'month' ? '12m' : '');
        } else if (nextMode === 'day' && spanDays(state.from, state.to) > MAX_DAY_SPAN) {
          var toD = parseYMD(state.to) || new Date();
          state.from = formatDateLocal(addDays(toD, -(MAX_DAY_SPAN - 1)));
          dg.toast(t('wl.ui.range_clamped', { max: MAX_DAY_SPAN }), 'warn');
        }
        syncControlUI();
        loadTrend(false);
      });
    });

    document.querySelectorAll('[data-groupby]').forEach(function (btn) {
      btn.addEventListener('click', function () {
        var next = btn.getAttribute('data-groupby');
        if (next === state.groupBy) { return; }
        state.groupBy = next;
        syncControlUI();
        loadTrend(false);
      });
    });

    $('app-select').addEventListener('change', function () {
      state.app = $('app-select').value;
      state.categories = [];
      populateCatList();
      syncControlUI();
      loadTrend(false);
    });

    $('exclude-idle').addEventListener('change', function () {
      state.excludeIdle = $('exclude-idle').checked;
      syncControlUI();
      loadTrend(false);
    });

    $('btn-reset').addEventListener('click', function () {
      state.app = '';
      state.hosts = [];
      state.categories = [];
      state.excludeIdle = false;
      state.groupBy = 'auto';
      populateAppSelect();
      populateHostList();
      populateCatList();
      syncControlUI();
      loadTrend(false);
    });

    $('btn-refresh').addEventListener('click', function () {
      loadMeta(true).then(function () { return loadTrend(true); }, function (err) { dg.toast(err.message, 'err'); });
    });

    $('btn-back-apps').addEventListener('click', function () {
      state.app = '';
      state.categories = [];
      populateAppSelect();
      populateCatList();
      syncControlUI();
      loadTrend(false);
    });

    $('btn-clear-host').addEventListener('click', function () {
      state.hosts = [];
      populateHostList();
      syncControlUI();
      loadTrend(false);
    });

    $('breakdown-search').addEventListener('input', function () {
      state.breakdownFilter = $('breakdown-search').value;
      if (state.trend) { renderBreakdown(state.trend); }
    });

    // Re-theme ECharts immediately whenever the shell theme toggle flips data-theme.
    if (window.MutationObserver) {
      new MutationObserver(function () {
        if (state.trend) { renderChart(state.trend); }
      }).observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });
    }
  }

  function onDateChange() {
    var f = $('date-from').value;
    var to = $('date-to').value;
    if (!parseYMD(f) || !parseYMD(to)) { return; }
    if (f > to) {
      $('date-to').value = f;
      to = f;
    }
    state.from = f;
    state.to = to;
    state.customDates = true;
    state.preset = '';
    syncControlUI();
    loadTrend(false);
  }

  function start() {
    readInitialURL();
    bindEvents();
    loadMeta(false).then(function () {
      return loadTrend(false);
    }, function (err) {
      dg.toast(err.message, 'err');
      showChartOverlay(err.message);
    }).then(function () {
      if (dg.tour && dg.tour.autoStart) {
        dg.tour.autoStart('web_log');
      }
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', start);
  } else {
    start();
  }
})();
