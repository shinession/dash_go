# English

**Updated:** 2026-11-28 (`dash_go` version `0.2.5`). This document describes the implemented `internal/mods/web_log` module and the shared multi-database support added to `internal/core/module`. Section 9 lists what was verified and what was not.

## 1. Overview

**Web Log** (`/dash_go/web_log/`) is the Go migration of the legacy Python Dash `IE.py` `tab11` (**Web Log**) page (`build_tab11()` / `build_web_log()` in `IE.py` and `generate_web_log()` in `LocalListV3.py`), enhanced with standard calendar grouping, period-distinct IP visitor counts, dynamic App / Tool / Server filtering, an optional **By Server** stacked breakdown view, shared Redis result caching, and a structured administrator-only live Redis `web_log` viewer.

Data sources:

- **Aggregated daily traffic:** PostgreSQL `Log` database, table `t_log_daily` (`log_date`, `ip_addr`, `app`, `category`, `host`, `action_count`, `uid`, `update_time`), refreshed hourly by the Python `log_to_daily()` task on the `cdn` host.
- **Administrator check:** PostgreSQL `Auth` database, table `"T_USER_ALL"` (`ee_code`, `"Type"`, `activated`, `"UpdateDate"`).
- **Real-time log stream (admin only):** Shared Redis list `web_log` written by Python `rd_log()` (`zlib.compress(pickle.dumps(str))`), safely decoded without opcode execution via `internal/core/pyobj.Decode`.

## 2. Differences from Legacy `IE.py` `tab11`

| Area | Legacy `IE.py` `tab11` | `dash_go` `web_log` (`0.2.5`) |
|---|---|---|
| Date range | Silently expanded/clamped `from_date` (30–62d day, ≥12w week, ≥395d month) | Respects the exact `[from, to]` selected by the user; presets (`7D`, `30D`, `90D`, `12M`); safety cap 366d (day) / 1,830d (week/month) |
| Calendar buckets | Manufacturing month shift (`+1 day`) and `MMM-YYYY W1..W5` | Standard calendar days (`YYYY-MM-DD`), ISO-8601 weeks (`YYYY-Www`, Mon–Sun), and calendar months (`YYYY-MM`) |
| VisitorCount | Daily distinct IP summed across week/month (visitor-days) | True period-distinct IP count (`COUNT(DISTINCT ip_addr)`) per day/week/month and across the full selected range |
| App filter | Hardcoded `All, QDB, MER, IE, API, QAQC` | Dynamically discovered from `t_log_daily` (includes `DataBridge`, `Gitea`, `TimeVault`, `SSO`, `Others`, and any future app) |
| Tools (`category`) filter | Drill-down only; `idle` always mixed in | Multi-select `category` filter + one-click **Exclude `idle`** toggle + full ungrouped breakdown table |
| Server (`host`) filter | Not available | Multi-select `host` filter (empty/`NULL` normalized to `prod_eng`) + **Stack By: By Server** mode |
| Query execution & caching | Pulled raw rows into pandas on every callback, no cache | Single-CTE parameterized PostgreSQL aggregation + shared Redis JSON cache (`dash_go:web_log:v1:*`) |
| Admin live log | Raw `<pre>` list of 1,000 lines | Structured table with keyword/host/level filters, 200/500/1000 selector, manual refresh, and optional 5s auto-refresh |

## 3. Permissions and Security

- **Trend analytics (`/dash_go/web_log/`, `/api/meta`, `/api/trend`):** publicly readable without login, matching legacy `IE.py` `tab11`.
- **Live Redis Web Log (`/api/live` and `#live-section`):** strictly restricted to signed-in active system administrators:
  - Must carry a valid `dash_go` session cookie (`env.Auth.Require`; returns HTTP `401` otherwise).
  - `AdminChecker.IsUserAdmin` queries `Auth."T_USER_ALL"` (`WHERE upper(btrim(ee_code::text)) = upper(btrim($1)) ORDER BY "UpdateDate" DESC NULLS LAST LIMIT 1`) and requires `activated` in `{'true', 't', '1'}` and `"Type" = 'admin'` (case-insensitive).
  - Non-admin users receive HTTP `403` (`wl.err.forbidden_admin`); database lookup errors fail closed with HTTP `500`.
  - For anonymous or non-admin visitors, `#live-section` stays hidden in the DOM and `/api/live` is never requested by the client.

## 4. Aggregation and Host Normalization

All SQL queries in `internal/mods/web_log/repo_pg.go` are fully parameterized; period truncation and series grouping expressions come strictly from fixed allow-listed Go constants:

- **Host normalization:** `COALESCE(NULLIF(btrim(host::text), ''), 'prod_eng')` maps historical `NULL` or blank host rows to `prod_eng` consistently across metadata, filters, and stacked server series.
- **App / Category normalization:** blank `app` maps to `Others`; blank `category` maps to `others`.
- **Single-CTE query (`QueryTrend`):** executes one filtered CTE scan over `t_log_daily` and returns four result groups (`UNION ALL`):
  1. Per-period per-series action sums (for stacked bars).
  2. Per-period total actions and `COUNT(DISTINCT ip_addr::text)` (for total labels and the visitor line).
  3. Range-wide total actions and `COUNT(DISTINCT ip_addr::text)` (for KPI cards).
  4. Per-series total actions and `COUNT(DISTINCT ip_addr::text)` across the range (for the full breakdown table).
- **Top-N chart folding:** when more than `max_series` (default `15`) series match, the chart displays the top 15 by action count and folds the tail into a single `Others (N)` bar series; the **Breakdown** table below always renders every series ungrouped.

## 5. Redis Caching Strategy

Shared Redis cache keys live under `dash_go:web_log:v1:` (JSON encoded, shared across `s2` and `s3`):

| Cache Key | Default TTL | Notes |
|---|---|---|
| `dash_go:web_log:v1:meta` | 15 minutes (`meta_cache_minutes`) | Available apps, hosts, per-app categories, and `MIN`/`MAX` `log_date`. |
| `dash_go:web_log:v1:trend:<hash>` (recent: `to >= yesterday`) | 10 minutes (`recent_cache_minutes`) | Matches hourly `log_to_daily()` updates on `cdn`. |
| `dash_go:web_log:v1:trend:<hash>` (historical: `to < yesterday`) | 6 hours (`history_cache_minutes`) | Historical dates rarely change. |
| `dash_go:web_log:v1:cd:<key>` | 15 seconds (`refresh_cooldown_sec`) | Per-key cooldown (`SETNX`) preventing rapid clicks on **Refresh** (`?refresh=1`) from hammering PostgreSQL. |

If Redis is unconfigured, the module falls back to an in-memory TTL cache and disables `/api/live` (HTTP `503`). If Redis returns a transient error on `GET`/`SET`, the service logs a warning and queries PostgreSQL directly so the page stays available.

## 6. Offline Charting (Apache ECharts 5)

`internal/mods/web_log/web/static/vendor/echarts.common.min.js` bundles Apache ECharts `5.6.0` (Apache-2.0 license, ~664 KB) inside the binary via `//go:embed`, so the page works completely offline without external CDNs. A `MutationObserver` on `<html data-theme>` re-themes ECharts immediately when the user switches between light and dark themes.

## 7. HTTP Routes

All routes live below `/dash_go/web_log`:

| Method & Path | Auth | Purpose |
|---|---|---|
| `GET /dash_go/web_log` | Public | `301` redirect to `/dash_go/web_log/`. |
| `GET /dash_go/web_log/` | Public | Renders the Web Log page inside the shell (`AppIcon: "weblog"`, `ClientPrefixes: ["wl.ui."]`). |
| `GET /dash_go/web_log/help` | Public | Renders the bilingual user guide. |
| `GET /dash_go/web_log/static/...` | Public | Serves embedded `app.css`, `app.js`, and `vendor/echarts.common.min.js` (`Cache-Control: no-cache`). |
| `GET /dash_go/web_log/api/meta` | Public (optional session) | Returns filter metadata, date bounds, cache status, and `is_admin` for signed-in users. Accepts `?refresh=1`. |
| `GET /dash_go/web_log/api/trend` | Public | Returns calendar-bucketed stacked bar series, period-distinct IP counts, KPI summary, and full breakdown table. Params: `mode`, `from`, `to`, `app`, `group_by`, `host`, `category`, `exclude_idle`, `refresh`. |
| `GET /dash_go/web_log/api/live` | Session + `T_USER_ALL.Type = admin` | Returns newest parsed entries from Redis list `web_log` (`?limit=1..1000`, default `500`). Returns `401`/`403` for unauthenticated/non-admin callers. |

## 8. Configuration & Build

Optional settings under `modules.web_log` in `dash_go.json`:

| Key | Default | Meaning |
|---|---|---|
| `recent_cache_minutes` | `10` | Redis TTL in minutes for trend queries covering yesterday or today. |
| `history_cache_minutes` | `360` | Redis TTL in minutes for purely historical trend queries. |
| `meta_cache_minutes` | `15` | Redis TTL in minutes for `/api/meta` filter options. |
| `refresh_cooldown_sec` | `15` | Cooldown in seconds between forced refreshes (`refresh=1`) per cache key. |
| `max_series` | `15` | Maximum individual stacked series before folding remaining items into `Others (N)`. |
| `redis_log_key` | `"web_log"` | Redis list key read by the admin live log viewer. |
| `live_log_max` | `1000` | Maximum number of live log lines returned per request. |

`dash_go.exe -check` probes both `PostgreSQL` (`Auth`) and `PostgreSQL (Log)` (`Log`) alongside `Redis`.

Local demo preview without a database:

```powershell
go test -tags devserver -run TestDevServer -timeout 0 ./internal/mods/web_log
```

## 9. Verification Status

Verified on 2026-11-28 (Go `1.27.4`, Windows):

- `go vet ./...`: passed with no warnings or errors.
- `go test -gcflags 'all=-N -l' ./...`: all packages passed, including:
  - `internal/mods/web_log` (20 unit/HTTP/Redis/i18n catalog tests: standard calendar day/ISO week/calendar month bucketing, period-distinct IP counts, NULL/blank host normalization to `prod_eng`, `group_by=host`, Top-N folding with full breakdown, `miniredis` Redis cache hit/miss/TTL/cooldown/fallback, `miniredis` zlib+pickle & UTF-8 `web_log` decoding and line parsing, anonymous/user/admin HTTP access control, EN/ZH i18n parity and tour completeness).
  - `internal/core/module` (9 tests including multi-database `PGDB` / `PostgreSQL (Log)` probe ordering) and `internal/core/shell` (26 tests including `icons.svg#weblog` and core catalog checks).
- Module selection build check: `go build ./cmd/dash_go` (`dash_go 0.2.5`, modules `hc_budget, hc_scan, web_log`), `go build -tags nomod_web_log ./cmd/dash_go` (`hc_budget, hc_scan`), and `scripts/build.ps1 -List` (`hc_budget`, `hc_scan`, `web_log`).
- End-to-end browser & HTTP verification against the in-memory `TestDevServer` (`go test -tags devserver`) with headless Chrome:
  - Anonymous visitor: ECharts `<canvas>` rendered, KPI and 8 breakdown rows populated, `#live-section` hidden, `/api/live` not requested.
  - Regular user (`US01`): `/api/meta` returned `is_admin=false`, `/api/live` returned HTTP `403`.
  - System admin (`AD01`): `/api/meta` returned `is_admin=true`, `/api/live` returned HTTP `200` and rendered all live log rows in `#live-section`.

Not verified in this environment:

- `TestPGRepositoryAgainstScratchPostgres` was skipped because `DASH_GO_TEST_PG` was not set.
- Live queries against the production PostgreSQL `Log` / `Auth` databases and production Redis `web_log` list, as well as `dash_go.exe -check` against production hosts (`s2` / `s3`).

---

# 中文

**更新日期：** 2026-11-28（`dash_go` 版本 `0.2.5`）。本文描述 `internal/mods/web_log` 模块的实际实现以及在 `internal/core/module` 中新增的多数据库支持。第 9 节记录已验证和未验证的内容。

## 1. 概述

**Web 日志**（`/dash_go/web_log/`）是旧版 Python Dash `IE.py` 中 `tab11`（**Web Log**，`IE.py` 的 `build_tab11()` / `build_web_log()` 与 `LocalListV3.py` 的 `generate_web_log()`）的 Go 迁移版本，并在此基础上增强了标准自然日历分组、周期内去重 IP 访客数、动态应用 / 工具 / 服务器筛选、**按服务器**堆叠拆分视图、共享 Redis 查询缓存，以及仅系统管理员可见的结构化 Redis 实时 `web_log` 面板。

数据源：

- **日汇总流量：** PostgreSQL `Log` 数据库中的 `t_log_daily` 表（`log_date`、`ip_addr`、`app`、`category`、`host`、`action_count`、`uid`、`update_time`），由 `cdn` 主机上的 Python `log_to_daily()` 每小时更新。
- **管理员判定：** PostgreSQL `Auth` 数据库中的 `"T_USER_ALL"` 表（`ee_code`、`"Type"`、`activated`、`"UpdateDate"`）。
- **实时日志流（仅管理员）：** 由 Python `rd_log()` 写入的共享 Redis 列表 `web_log`（元素为 `zlib.compress(pickle.dumps(str))`），通过 `internal/core/pyobj.Decode` 安全反序列化（不执行任何 pickle 指令）。

## 2. 与旧版 `IE.py` `tab11` 的差异

| 项目 | 旧版 `IE.py` `tab11` | `dash_go` `web_log`（`0.2.5`） |
|---|---|---|
| 日期范围 | 会静默拉长或截断 `from_date`（按天 30–62 天、按周 ≥12 周、按月 ≥395 天） | 严格遵循用户选择的 `[from, to]`；提供 `7D` / `30D` / `90D` / `12M` 快捷按钮；防超载上限按天 366 天、周/月 1,830 天 |
| 日历分组 | 制造生产月偏移（`+1 天`）及月内 `MMM-YYYY W1..W5` | 标准自然日（`YYYY-MM-DD`）、ISO-8601 标准周（`YYYY-Www`，周一至周日）与自然月（`YYYY-MM`） |
| VisitorCount 口径 | 周/月视图按每日去重 IP 累加（人天） | 严格按周期内去重 IP 数统计（`COUNT(DISTINCT ip_addr)`），KPI 同时给出整段日期范围的去重 IP 数 |
| App 筛选 | 硬编码 `All, QDB, MER, IE, API, QAQC` | 从 `t_log_daily` 动态读取全部 App（包含 `DataBridge`、`Gitea`、`TimeVault`、`SSO`、`Others` 及未来新增应用） |
| Tools（`category`）筛选 | 仅支持点击下钻，`idle` 流量无法排除 | 支持多选 `category` 筛选 + 一键**排除 `idle`** 开关 + 下方完整未折叠明细表 |
| 服务器（`host`）筛选 | 不支持 | 支持多选服务器筛选（空/`NULL` 归入 `prod_eng`）+ **堆叠维度：按服务器**视图 |
| 查询与缓存 | 每次回调拉取原始行到 pandas，无缓存 | PostgreSQL 单 CTE 参数化聚合 + 共享 Redis JSON 缓存（`dash_go:web_log:v1:*`） |
| 管理员实时日志 | 1,000 条原始 `<pre>` 文本 | 结构化表格，支持关键字 / 服务器 / 级别筛选、200/500/1000 条切换、手动刷新与可选 5 秒自动刷新 |

## 3. 权限与安全

- **趋势分析（`/dash_go/web_log/`、`/api/meta`、`/api/trend`）：** 无需登录即可公开查看，保持与旧版 `IE.py` `tab11` 一致。
- **Redis 实时日志（`/api/live` 与 `#live-section`）：** 仅限已登录的活跃系统管理员：
  - 必须携带有效 `dash_go` 会话 Cookie（`env.Auth.Require`，否则返回 HTTP `401`）。
  - `AdminChecker.IsUserAdmin` 查询 `Auth."T_USER_ALL"`（`WHERE upper(btrim(ee_code::text)) = upper(btrim($1)) ORDER BY "UpdateDate" DESC NULLS LAST LIMIT 1`），要求 `activated` 属于 `{'true', 't', '1'}` 且 `"Type" = 'admin'`（不区分大小写）。
  - 非管理员返回 HTTP `403`（`wl.err.forbidden_admin`）；数据库校验异常时默认拒绝并返回 HTTP `500`。
  - 对未登录或普通用户，页面中 `#live-section` 始终隐藏，前端不会请求 `/api/live`。

## 4. 聚合规则与主机归类

`internal/mods/web_log/repo_pg.go` 中的全部 SQL 均采用参数化绑定；周期截断与分组表达式仅取自固定的白名单 Go 常量：

- **主机归类：** `COALESCE(NULLIF(btrim(host::text), ''), 'prod_eng')` 在元数据、筛选条件和按服务器堆叠中统一将历史 `NULL` 或空字符串归入 `prod_eng`。
- **应用 / 类别归类：** 空 `app` 归为 `Others`，空 `category` 归为 `others`。
- **单 CTE 聚合查询（`QueryTrend`）：** 对 `t_log_daily` 过滤范围仅扫描一次，通过 `UNION ALL` 同时返回 4 类汇总：
  1. 按周期与堆叠分组的操作数之和（堆叠柱）；
  2. 按周期的总操作数与 `COUNT(DISTINCT ip_addr::text)`（顶部总计与去重访客折线）；
  3. 整个选定区间的总操作数与 `COUNT(DISTINCT ip_addr::text)`（KPI 卡片）；
  4. 按分组的整个区间总操作数与 `COUNT(DISTINCT ip_addr::text)`（完整明细汇总表）。
- **Top-N 图表折叠：** 当匹配分组超过 `max_series`（默认 `15`）时，图表展示操作数前 15 项并将剩余长尾归入 `其他 (N)`；下方**汇总表**始终展示全部未折叠明细。

## 5. Redis 缓存策略

共享 Redis 缓存键位于 `dash_go:web_log:v1:` 前缀下（JSON 序列化，`s2` 与 `s3` 多实例共用）：

| 缓存键 | 默认 TTL | 说明 |
|---|---|---|
| `dash_go:web_log:v1:meta` | 15 分钟（`meta_cache_minutes`） | 可选应用、服务器、各应用工具列表及 `MIN`/`MAX` 日期范围。 |
| `dash_go:web_log:v1:trend:<hash>`（近期：`to >= 昨天`） | 10 分钟（`recent_cache_minutes`） | 匹配 `cdn` 主机每小时运行 `log_to_daily()` 的更新频率。 |
| `dash_go:web_log:v1:trend:<hash>`（纯历史：`to < 昨天`） | 6 小时（`history_cache_minutes`） | 历史日期数据基本不变。 |
| `dash_go:web_log:v1:cd:<key>` | 15 秒（`refresh_cooldown_sec`） | 基于 `SETNX` 的单键冷却窗口，防止频繁点击**刷新**（`?refresh=1`）击穿数据库。 |

若未配置 Redis，模块自动退化为进程内存 TTL 缓存，并关闭 `/api/live`（返回 HTTP `503`）。若 Redis 在 `GET`/`SET` 时出现瞬时故障，服务记录告警日志并自动降级直查 PostgreSQL，不影响趋势页面访问。

## 6. 离线图表（Apache ECharts 5）

`internal/mods/web_log/web/static/vendor/echarts.common.min.js` 通过 `//go:embed` 将 Apache ECharts `5.6.0`（Apache-2.0 协议，约 664 KB）直接打入二进制文件，在内网完全离线运行，不请求任何外部 CDN。页面通过 `MutationObserver` 监听 `<html data-theme>` 属性变化，在用户切换浅色/深色主题时即时刷新图表配色。

## 7. HTTP 路由

所有路由位于 `/dash_go/web_log` 之下：

| 方法与路径 | 鉴权 | 用途 |
|---|---|---|
| `GET /dash_go/web_log` | 公开 | `301` 重定向到 `/dash_go/web_log/`。 |
| `GET /dash_go/web_log/` | 公开 | 在 shell 框架内渲染 Web 日志页面（`AppIcon: "weblog"`、`ClientPrefixes: ["wl.ui."]`）。 |
| `GET /dash_go/web_log/help` | 公开 | 渲染中英文使用手册页面。 |
| `GET /dash_go/web_log/static/...` | 公开 | 提供内置 `app.css`、`app.js` 与 `vendor/echarts.common.min.js`（`Cache-Control: no-cache`）。 |
| `GET /dash_go/web_log/api/meta` | 公开（可选会话） | 返回筛选项元数据、数据日期范围、缓存状态，以及已登录用户的 `is_admin` 标记。支持 `?refresh=1`。 |
| `GET /dash_go/web_log/api/trend` | 公开 | 返回标准日历分组的堆叠柱数据、周期去重访客数、KPI 汇总与完整明细表。参数：`mode`、`from`、`to`、`app`、`group_by`、`host`、`category`、`exclude_idle`、`refresh`。 |
| `GET /dash_go/web_log/api/live` | 需登录且 `T_USER_ALL.Type = admin` | 返回 Redis `web_log` 列表最新解析记录（`?limit=1..1000`，默认 `500`）。未登录/非管理员返回 `401`/`403`。 |

## 8. 配置与构建

`dash_go.json` 中 `modules.web_log` 可选配置项：

| 配置键 | 默认值 | 说明 |
|---|---|---|
| `recent_cache_minutes` | `10` | 覆盖昨天或今天的趋势查询 Redis 缓存分钟数。 |
| `history_cache_minutes` | `360` | 纯历史趋势查询 Redis 缓存分钟数。 |
| `meta_cache_minutes` | `15` | `/api/meta` 筛选项元数据的 Redis 缓存分钟数。 |
| `refresh_cooldown_sec` | `15` | 单个缓存键允许强制刷新（`refresh=1`）的最小冷却秒数。 |
| `max_series` | `15` | 图表堆叠柱最大独立系列数，超出部分归入 `其他 (N)`。 |
| `redis_log_key` | `"web_log"` | 管理员实时日志面板读取的 Redis 列表键名。 |
| `live_log_max` | `1000` | 单次请求最多返回的实时日志条数。 |

`dash_go.exe -check` 会同时探测 `PostgreSQL`（`Auth`）、`PostgreSQL (Log)`（`Log`）以及 `Redis`。

无需数据库的本地演示预览命令见英文部分第 8 节代码块（内容相同，不重复）。

## 9. 验证状态

于 2026-11-28 验证（Go `1.27.4`，Windows）：

- `go vet ./...`：无警告或错误通过。
- `go test -gcflags 'all=-N -l' ./...`：全部包通过，包括：
  - `internal/mods/web_log`（20 项单元/HTTP/Redis/i18n 词条测试：标准自然日 / ISO 周 / 自然月分组、周期去重 IP 计数、NULL/空 `host` 归入 `prod_eng`、`group_by=host`、Top-N 折叠与完整明细表、`miniredis` 缓存命中/失效/TTL/冷却/故障降级、`miniredis` zlib+pickle 与明文 `web_log` 解码解析、匿名/普通用户/管理员权限控制、中英文词条一致性与导览校验）。
  - `internal/core/module`（9 项测试，含多库 `PGDB` / `PostgreSQL (Log)` 探针顺序）与 `internal/core/shell`（26 项测试，含 `icons.svg#weblog` 与核心词条检查）。
- 模块化构建检查：`go build ./cmd/dash_go`（`dash_go 0.2.5`，模块 `hc_budget, hc_scan, web_log`）、`go build -tags nomod_web_log ./cmd/dash_go`（`hc_budget, hc_scan`）与 `scripts/build.ps1 -List`（`hc_budget`、`hc_scan`、`web_log`）。
- 基于内存 `TestDevServer`（`go test -tags devserver`）的无头 Chrome 与 HTTP 端到端验证：
  - 匿名访客：ECharts `<canvas>` 正常渲染，KPI 与 8 行汇总明细正常展示，`#live-section` 保持隐藏且未请求 `/api/live`。
  - 普通用户（`US01`）：`/api/meta` 返回 `is_admin=false`，`/api/live` 返回 HTTP `403`。
  - 系统管理员（`AD01`）：`/api/meta` 返回 `is_admin=true`，`/api/live` 返回 HTTP `200` 并在 `#live-section` 渲染实时日志行。

本环境未验证项：

- 未设置 `DASH_GO_TEST_PG`，因此 `TestPGRepositoryAgainstScratchPostgres` 被跳过。
- 未直接连接生产环境的 PostgreSQL `Log` / `Auth` 库、生产 Redis `web_log` 列表，也未在生产主机（`s2` / `s3`）上执行 `dash_go.exe -check`。
