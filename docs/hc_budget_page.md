# English

**Updated:** 2026-11-27 (dash_go version 0.2.0). This describes the current implementation of `internal/mods/hc_budget` and the shared helpers in `internal/core/hc`, `internal/core/dashapi`, `internal/core/config`, and `internal/core/shell`. Section 9 lists what was verified and what was not.

## 1. Overview

**HC Budget Setting** (`/dash_go/hc_budget/`) is the Go migration of the Dash `IE.py` HC Budget Setting view (`build_mqpd_hc_budget_div` / `mqpd_hc_data_submit`), extended with administrator management tools.

It reads and writes `t_mqpd_hc_table` in the `Auth` PostgreSQL database — the same master table used by **HC Scan Assign** (`hc_scan`) and the Python Dash HC pages:

- **Shared process picker:** reuses the shell process picker (`dashGo.picker`, `.screen`, `.process-buttons`) and remembers the selected process in `sessionStorage` (`hc.process`) across both `hc_budget` and `hc_scan`.
- **Inline budget and machine editing:** authorized users can edit `machine`, `type`, `category`, and `budget_head_count` inline.
- **Administrator controls (`type=admin`):** administrators can additionally add new machines/activities, hide or restore existing rows (with a **Show hidden** toggle), and adjust per-process `display_seq` via drag-and-drop or up/down arrow buttons.
- **Batch Confirm:** all edits, staged additions, visibility changes, and sequence reorders stay local in the browser until the user clicks **Confirm**, which commits everything in a single PostgreSQL transaction and appends to `MQPDHeadCount.log` so Python Dash pages invalidate their Redis cache.
- **Module icon:** uses the `budget` symbol (person silhouette paired with a 6-tooth setting gear) in `internal/core/shell/web/assets/icons.svg`.

## 2. Permissions and Roles

Process access uses the exact same backend check as `hc_scan`: `internal/core/hc.DashPermissions` calls `POST <api>/user/hc_scan` with `{"func":"hc_user_processes","user":"<ee>"}` (cached 1 minute; bypassed with `fresh=true` on `POST api/save`) and intersects the returned processes with `HC_PROCESS_LIST`.

Administrator status is derived from the optional `type` field on `dashapi.Profile`:

- `is_admin = strings.EqualFold(strings.TrimSpace(profile.Type), "admin")`
- If an older Python backend omits `type`, `is_admin` safely defaults to `false`.

| Capability | Authorized User | Admin (`type = admin`) |
|---|---|---|
| View active (`hidden = false`) rows of permitted processes | Yes | Yes |
| Edit `machine`, `type`, `category`, `budget_head_count` on active rows | Yes | Yes |
| Add new machine / activity | No (HTTP `403`) | Yes |
| Toggle **Show hidden**, hide active rows, restore or edit hidden rows | No (HTTP `403`) | Yes |
| Reorder per-process `display_seq` | No (HTTP `403`) | Yes |

### Pending Python Dash Integration (Not Modified Yet)

Per project rollout order, `D:\Programs\Python\dash` was intentionally left untouched while building `dash_go` v0.2.0. To enable admin detection in `dash_go` against the live Python API, add `'type'` to the `hc_user_processes` response in `appV2.py`:

```python
return jsonify({
    'Response': 'ok',
    'name': user_data.get('Name'),
    'ee_code': user_data.get('EE_Code'),
    'type': user_data.get('Type'),
    'processes': processes,
}), 200
```

Optionally, link a Dash menu button in `IE.py` to `/dash_go/hc_budget/` via `build_dash_go_sso_url(..., next_path='/dash_go/hc_budget/')`.

## 3. Workflow and UI

1. **Process Picker (`data-tour-screen="hc_budget.picker"`):** displays only permitted processes (in `HC_PROCESS_LIST` order) and the signed-in user caption (with `· Admin` for administrators). If a valid process is already stored in `sessionStorage` (`hc.process`), the table opens directly.
2. **Page Bar (`#pagebar`, sticky below the shell header):**
   - Process title, optional **Admin** badge, and **Change process** button (prompts before discarding unsaved edits).
   - Search box filtering across machine name, category, station code, type, cost center, and ID.
   - Admin-only **Show hidden (`n`)** checkbox and **+ Add Machine / Activity** button.
   - Live counters: **Active**, **Total Budget HC** (recalculated instantly as rows are edited, added, hidden, or restored), **Hidden** (admin, when > 0), and **Unsaved** (amber badge when > 0).
3. **Editable Table (`data-tour-screen="hc_budget.table"`):**
   - **Editable columns:** `Machine / Activity` (text, required), `Type` (`Routine` / `Non-routine`), `Category` (free text with `<datalist>` populated from existing process categories and newly entered values), `Budget HC` (non-negative numeric; blank displays placeholder `0` and saves as `0`).
   - **Read-only columns on existing rows:** `Station` (showing `station_code` and `id`), `Cost Center`, `Last Updated` (`update_time` and `update_user`).
   - **Admin-only columns:**
     - **Seq:** drag handle (`⋮⋮`), live per-process position (`1..N`), and `▲` / `▼` move buttons. Drag/move controls are disabled while a search filter is active to prevent reordering a partial list.
     - **Status:** status chip (`Active`, `Edited`, `New`, `Hidden`) and action button (**Hide** / **Restore** for existing rows; **Remove** for unsaved staged rows).
4. **Add Machine / Activity Modal (`#add-dialog`, Admin only):**
   - **Station Code** (required integer `1–32767`): live preview shows generated `id = <process>_<station_code>` and immediately flags collisions against existing station codes (both active and hidden) and staged additions.
   - **Cost Center** (`char(4)`) and **Process Code** (`smallint`) are auto-prefilled from the most frequent values among existing rows of the selected process and remain editable in the modal.
   - Submitting appends a staged row with a green `New` badge; nothing is written until **Confirm**.
5. **Discard and Confirm:**
   - **Discard** reverts all local edits by reloading from the server.
   - **Confirm** opens a summary dialog listing counts of additions, edits, hides, restores, and sequence updates, then submits one `POST api/save` request.
   - Leaving the tab with unsaved changes triggers a browser `beforeunload` confirmation.
6. **Help Page & Interactive Tour:**
   - Public bilingual guide at `/dash_go/hc_budget/help`.
   - Step-by-step spotlight tour (`hc_budget.picker` and `hc_budget.table`), automatically skipping admin-only steps (`hb.tour.add`, `hb.tour.reorder`) for standard users.

## 4. Data Model, Validation, and Save Rules

Table: `t_mqpd_hc_table` in PostgreSQL (`Auth` database; schema is never altered by this module).

| Column | Type | Read / Write Rule |
|---|---|---|
| `id` | `varchar` PK | Generated on create as `<process>_<station_code>`; checked globally unique across all rows of `t_mqpd_hc_table`. Immutable after creation. |
| `process` | `varchar` | Selected process from `HC_PROCESS_LIST`. Immutable. |
| `cost_center` | `char(4)` | Trimmed on read (`btrim`); max 4 characters on create; read-only on edit. |
| `process_code` | `smallint` | Nullable integer (`-32768..32767`); editable on create; read-only on edit. |
| `station_code` | `smallint` | Required `1..32767` on create; unique within `process` across active and hidden rows; read-only on edit. |
| `machine` | `varchar` | Required non-empty trimmed string (max 120 chars). |
| `type` | `varchar` | `Routine` or `Non-routine` (required on create and whenever changed; legacy unchanged values are preserved). |
| `category` | `varchar` | Optional free text (max 80 chars; empty stored as `NULL`). |
| `budget_head_count` | `real` | Validated with `^\d{1,8}(\.\d{1,12})?$` (matching `IE.py`); blank saves as `0`; read cleanly via `::real::text::float8`. |
| `display_seq` | `smallint` | Per-process sequence (`1..N`). New rows default to `max(display_seq of process) + 1` unless placed via `order`. When admin submits `order` with **Show hidden** off, visible rows receive `1..K` and hidden rows remain ordered after them (`K+1..N`). |
| `hidden` | `boolean` | Admin-only toggle (`false` active, `true` hidden). |
| `update_time` | `timestamp` | Naive local timestamp (`YYYY-MM-DD HH:MM:SS.ffffff`) updated on every inserted or modified row. |
| `update_user` | `varchar` | Display name of the signed-in user (fallback employee code). |

**Transaction and Cache Invalidation:**

- `Repository.Apply` opens one PostgreSQL transaction, locks the process's rows with `SELECT ... WHERE process = $1 FOR UPDATE`, verifies candidate new IDs globally with `SELECT id FROM t_mqpd_hc_table WHERE id = ANY($1)`, executes parameterized `INSERT` and `UPDATE` statements only for genuinely changed rows (returning HTTP `409 nothing_to_save` if zero rows changed), and commits atomically.
- After commit, `NewFileChangeLog` appends a timestamped audit line to **`MQPDHeadCount.log`** in `dash_data_dir` and `cdn_data_folder` (matching Python `LocalListV3.py` `read_mqpd_hc_data` cache invalidation). If neither folder is configured or writable, the database save still succeeds and returns a non-fatal `log_warning`.

## 5. HTTP Routes

All routes live below `/dash_go/hc_budget`:

| Method & Path | Auth | Purpose |
|---|---|---|
| `GET /dash_go/hc_budget` | None | `301` redirect to `/dash_go/hc_budget/`. |
| `GET /dash_go/hc_budget/` | Session (redirects to login) | Renders the module page inside the shell (`AppIcon: "budget"`, `ClientPrefixes: ["hb.ui."]`). |
| `GET /dash_go/hc_budget/help` | Public | Renders the bilingual user guide. |
| `GET /dash_go/hc_budget/static/...` | Public | Serves embedded `app.css` and `app.js` (`Cache-Control: no-cache`). |
| `GET /dash_go/hc_budget/api/me` | Session (`401` if absent) | Returns `{user: {ee, name}, processes, is_admin, version}`. |
| `GET /dash_go/hc_budget/api/table?process=&include_hidden=0\|1` | Session | Returns `{process, is_admin, include_hidden, rows, defaults, totals}`. Forces `include_hidden=false` for non-admin. |
| `POST /dash_go/hc_budget/api/save` | Session + `X-Requested-With: dash_go` | Validates fresh permissions/role, commits `{process, creates, updates, order}` in one PG transaction, appends `MQPDHeadCount.log`, returns `{inserted, updated, log_warning?}`. |

## 6. Configuration

`hc_budget` uses the shared `dash_go` configuration with optional per-module overrides under `modules.hc_budget`:

| Key | Location | Default | Meaning |
|---|---|---|---|
| `dash_data_dir` | Top-level `dash_go.json` (or `modules.hc_budget.dash_data_dir`) | none | Python Dash `data` directory holding `MQPDHeadCount.log` (shared with `hc_scan`; per-module value overrides top-level). |
| `cdn_data_folder` | Python `config.json` | none | Secondary folder receiving `MQPDHeadCount.log`. |
| `dash_api_url` | Top-level `dash_go.json` (or `modules.hc_budget.dash_api_url`) | `http://<api_server>/api` | Python Dash API root for `hc_user_processes` and `dash_go.exe -check` (`Dash API (hc_budget)` probe). |
| `HC_PROCESS_LIST` | Python `config.json` | required | Canonical ordered list of HC processes. |

## 7. Build and Local Preview

- Included in default builds (`cmd/dash_go/mod_hc_budget.go`, build constraint `!nomod_hc_budget`).
- Package alone or alongside `hc_scan`: `.\scripts\build.ps1 -Modules hc_budget` or `.\scripts\build.ps1`.
- In-memory local preview server (no database required):

```powershell
go test -tags devserver -run TestDevServer -timeout 0 ./internal/mods/hc_budget
# Open http://127.0.0.1:18751/dev/login-admin (Admin) or /dev/login-user (Standard user)
```

## 8. Troubleshooting

- **Admin controls (+ Add, Show hidden, Seq column) do not appear for an admin account:** check whether the Python `appV2.py` `hc_user_processes` handler returns `"type": "admin"` (Section 2). Until `appV2.py` includes `'type'`, `dash_go` treats all users as standard authorized users.
- **Cannot drag or move rows:** clear the filter box; reordering is disabled while a search filter is active so hidden-by-filter rows are never reordered unexpectedly.
- **"Station code ... already exists" when adding a code not visible on screen:** check **Show hidden** — station code uniqueness applies across both active and hidden rows of that process (matching `id = <process>_<station_code>`).
- **Saved changes are not immediately visible in Python Dash:** check the warning toast or `logs\dash_go-<date>.log` for `MQPDHeadCount.log` write errors and verify `dash_data_dir` / `cdn_data_folder` permissions.

## 9. Verification Status

**Done (2026-11-27, version 0.2.0):**

- Static analysis & modular builds: `go vet ./...`, `go vet -tags devserver ./...`, `go build ./cmd/dash_go` (all modules: `hc_budget, hc_scan`), `-tags nomod_hc_budget` (`hc_scan` only), and `-tags nomod_hc_scan` (`hc_budget` only), verified with `-version`.
- Unit, HTTP, and catalog tests across all packages (`internal/core/dashapi`, `internal/core/config`, `internal/core/hc`, `internal/core/shell`, `internal/mods/hc_budget`, `internal/mods/hc_scan`, etc.):
  - `dashapi.Profile.IsAdmin()` for `"admin"`, mixed case, `"user"`, and omitted `"type"`;
  - Top-level `dash_data_dir` config parsing;
  - `hc.DashPermissions` caching, TTL expiry, `fresh=true` bypass, and `OrderProcesses`;
  - `hc_budget` service logic: permissions, non-admin `403` enforcement on create/hide/edit-hidden/reorder, batch add + edit + hide + restore + reorder, preserving hidden rows' order when **Show hidden** is off, `station_code` and global `id` uniqueness, `budget_head_count` regex and blank-to-zero handling, no-op `409 nothing_to_save`, `MQPDHeadCount.log` append and non-fatal warning;
  - `hc_budget` HTTP routes, auth guards, EN/ZH page and help rendering, tour steps, and strict `hb.*` catalog parity / unused-key / placeholder checks;
  - `shell` palette contrast, `budget` icon sprite presence, `dashGo.picker` presence, and core catalog tests.
- End-to-end headless Chromium verification (Playwright against the real `hc_budget` + shell `devserver` harness with in-memory repository, CSP headers, and visual screenshot inspection):
  - Landing page showing `Version 0.2.0`, `hc_scan` (`#scanner`) and `hc_budget` (`#budget`) icons;
  - Admin login, shared process picker with user/admin caption, Add Machine / Activity modal (prefilled cost center & process code, duplicate check against hidden station `999`, live ID preview, staging `Winder 535`), inline budget edit, Hide action, reordering (`Move up` and HTML5 drag-and-drop), Confirm summary dialog, atomic save + reload persistence, **Show hidden** toggle, dark theme, and Chinese UI;
  - Standard user login confirming all admin-only controls/columns are hidden while inline editing + Confirm succeeds;
  - Public `/dash_go/hc_budget/help` in EN & ZH; zero JS/page/CSP errors.

**Not verified:**

- Live PostgreSQL integration test (`DASH_GO_TEST_PG` in `internal/mods/hc_budget/repo_pg_test.go` was compile-checked and skipped because no scratch PostgreSQL instance was configured in this environment).
- Live integration against the Python `appV2.py` `'type'` addition (not yet applied to `D:\Programs\Python\dash` per rollout plan) and Python `read_mqpd_hc_data` cache refresh via `MQPDHeadCount.log`.
- Live Dash `IE.py` menu hand-over link to `/dash_go/hc_budget/` and production release build (`scripts/release.ps1`).
- User review of Chinese UI wording and help text.

---

# 中文

**更新日期：** 2026-11-27（dash_go 版本 0.2.0）。本文描述 `internal/mods/hc_budget` 以及 `internal/core/hc`、`internal/core/dashapi`、`internal/core/config` 和 `internal/core/shell` 中共享组件的当前实现。第 9 节说明哪些内容已验证、哪些尚未验证。

## 1. 概述

**HC 预算设置**（`/dash_go/hc_budget/`）是 Dash `IE.py` 中 HC Budget Setting 页面（`build_mqpd_hc_budget_div` / `mqpd_hc_data_submit`）在 Go 侧的迁移实现，并扩展了管理员维护能力。

本模块读写 `Auth` PostgreSQL 数据库中的 `t_mqpd_hc_table`——与 **HC 扫码分派**（`hc_scan`）及 Python Dash HC 页面共用同一张主数据表：

- **共用工序选择：** 复用 shell 的工序选择界面（`dashGo.picker`、`.screen`、`.process-buttons`），并在当前浏览器会话中通过 `sessionStorage`（`hc.process`）在 `hc_budget` 与 `hc_scan` 之间共享已选工序。
- **行内编辑机台与预算：** 普通授权用户可在表格中直接编辑 `machine`、`type`、`category` 和 `budget_head_count`。
- **管理员能力（`type=admin`）：** 管理员可额外新增机台/活动、隐藏或恢复已有行（配合**显示已隐藏**开关），并通过拖动或上下箭头按钮调整本工序内的 `display_seq` 显示顺序。
- **批量确认提交：** 所有编辑、待新增条目、隐藏切换与顺序调整均先暂存在浏览器本地，直到用户点击**确认提交**才通过单次 PostgreSQL 事务原子写入，并向 `MQPDHeadCount.log` 追加日志以通知 Python Dash 页面刷新 Redis 缓存。
- **模块图标：** 使用 `internal/core/shell/web/assets/icons.svg` 中的 `budget` 图标（人形剪影搭配六齿设置齿轮）。

## 2. 权限与角色

工序访问权限沿用与 `hc_scan` 完全一致的后端校验：`internal/core/hc.DashPermissions` 调用 `POST <api>/user/hc_scan`（`{"func":"hc_user_processes","user":"<ee>"}`，缓存 1 分钟，`POST api/save` 保存前强制 `fresh=true` 实时校验），并与 `HC_PROCESS_LIST` 取交集排序。

管理员身份取自 `dashapi.Profile` 的可选字段 `type`：

- `is_admin = strings.EqualFold(strings.TrimSpace(profile.Type), "admin")`
- 若旧版 Python 后端未返回 `type` 字段，`is_admin` 安全回退为 `false`。

| 功能 | 普通授权用户 | 管理员（`type = admin`） |
|---|---|---|
| 查看有权限工序下启用中（`hidden = false`）的行 | 可以 | 可以 |
| 编辑启用行的 `machine`、`type`、`category`、`budget_head_count` | 可以 | 可以 |
| 新增机台 / 活动 | 不可（HTTP `403`） | 可以 |
| 切换**显示已隐藏**、隐藏启用行、恢复或编辑已隐藏行 | 不可（HTTP `403`） | 可以 |
| 调整本工序内 `display_seq` 显示顺序 | 不可（HTTP `403`） | 可以 |

### 待配套修改的 Python Dash 后端（暂未修改）

按上线顺序，本次构建 `dash_go` v0.2.0 时保持 `D:\Programs\Python\dash` 原样未改。后续在 Python API 中启用管理员识别时，只需在 `appV2.py` 的 `hc_user_processes` 返回 JSON 中增加 `'type': user_data.get('Type')`（见上方英文版中的代码块）。

此外，可按需在 `IE.py` 中添加菜单入口按钮，通过 `build_dash_go_sso_url(..., next_path='/dash_go/hc_budget/')` 跳转到 `/dash_go/hc_budget/`。

## 3. 工作流程与界面

1. **工序选择页（`data-tour-screen="hc_budget.picker"`）：** 仅列出用户有权限的工序（按 `HC_PROCESS_LIST` 顺序）以及登录信息（管理员附带 `· 管理员` 标识）。若当前浏览器会话的 `sessionStorage`（`hc.process`）已记住有效工序，则直接进入表格。
2. **页面栏（`#pagebar`，吸顶于 shell 页眉下方）：**
   - 工序名称、可选**管理员**徽章、**切换工序**按钮（有未保存改动时先弹确认框）。
   - 筛选框：支持按机台名、分类、工位号、类型、成本中心和 ID 实时过滤。
   - 仅管理员可见的**显示已隐藏（`n`）**复选框和 **+ 新增机台 / 活动** 按钮。
   - 实时统计计数：**启用中**、**预算总人力**（随编辑/新增/隐藏/恢复实时重算）、**已隐藏**（管理员且大于 0 时显示）、**待保存更改**（有改动时琥珀色高亮）。
3. **可编辑表格（`data-tour-screen="hc_budget.table"`）：**
   - **可编辑列：** `机台 / 活动`（必填文本）、`类型`（`Routine` / `Non-routine`）、`分类`（自由文本，通过 `<datalist>` 提供本工序已有分类及新输入分类的下拉建议）、`预算人力`（非负数字；留空显示占位符 `0` 并按 `0` 保存）。
   - **已有行的只读列：** `工位号`（同时显示 `station_code` 与 `id`）、`成本中心`、`最后更新`（`update_time` 与 `update_user`）。
   - **仅管理员可见列：**
     - **顺序：** 拖动手柄（`⋮⋮`）、本工序内实时序号（`1..N`）以及 `▲` / `▼` 上下移动按钮。当筛选框有关键字时自动禁用排序操作，避免对局部筛选结果误排。
     - **状态：** 状态徽章（`启用`、`已修改`、`新增`、`已隐藏`）及操作按钮（已有行显示**隐藏** / **恢复**；尚未保存的新增行显示**移除**）。
4. **新增机台 / 活动弹窗（`#add-dialog`，仅管理员）：**
   - **工位号**（必填整数 `1–32767`）：实时预览生成的 ID `<process>_<station_code>`，并即时校验是否与该工序已有工位号（含已隐藏行）或待提交新增行重复。
   - **成本中心**（`char(4)`）与 **工序代码**（`smallint`）自动预填该工序现有记录中最常见的值，并在弹窗内可编辑。
   - 提交后向列表追加带绿色`新增`徽章的暂存行，在点击**确认提交**前不写库。
5. **撤销更改与确认提交：**
   - **撤销更改**从服务器重新加载并放弃所有本地修改。
   - **确认提交**先弹出变更汇总确认框（列出新增、更新、隐藏、恢复与排序变更），确认后发送单次 `POST api/save`。
   - 存在未保存更改时关闭或刷新标签页会触发浏览器 `beforeunload` 提示。
6. **使用手册与交互式导览：**
   - 公开的中英文使用手册位于 `/dash_go/hc_budget/help`。
   - 分步聚光灯导览覆盖 `hc_budget.picker` 与 `hc_budget.table`，普通用户会自动跳过管理员专属步骤（`hb.tour.add`、`hb.tour.reorder`）。

## 4. 数据模型、校验与保存规则

数据表：PostgreSQL `Auth` 数据库中的 `t_mqpd_hc_table`（本模块不修改表结构）。

| 字段 | 类型 | 读写与校验规则 |
|---|---|---|
| `id` | `varchar` 主键 | 新增时按 `<process>_<station_code>` 生成；在整张 `t_mqpd_hc_table` 内做全局唯一性校验。创建后不可修改。 |
| `process` | `varchar` | 来自 `HC_PROCESS_LIST` 的所选工序。不可修改。 |
| `cost_center` | `char(4)` | 读取时去除尾随空格（`btrim`）；新增时最多 4 个字符；编辑已有行时只读。 |
| `process_code` | `smallint` | 可空整数（`-32768..32767`）；新增时可编辑；编辑已有行时只读。 |
| `station_code` | `smallint` | 新增时必填 `1..32767`；同 `process` 内跨启用与已隐藏行唯一；编辑已有行时只读。 |
| `machine` | `varchar` | 必填非空字符串（去除首尾空格，最多 120 字符）。 |
| `type` | `varchar` | `Routine` 或 `Non-routine`（新增及修改类型时校验；已有行未改类型时保留历史值）。 |
| `category` | `varchar` | 可选自由文本（最多 80 字符；空值存为 `NULL`）。 |
| `budget_head_count` | `real` | 按 `^\d{1,8}(\.\d{1,12})?$` 校验（与 `IE.py` 一致）；留空按 `0` 保存；读取时通过 `::real::text::float8` 避免 `float32` 尾数噪声。 |
| `display_seq` | `smallint` | 按各工序独立编号（`1..N`）。未显式重排时新增行默认排在 `max(display_seq of process) + 1`。若管理员在未勾选**显示已隐藏**时调整顺序，可见行按 `1..K` 编号，隐藏行保持相对顺序排在其后（`K+1..N`）。 |
| `hidden` | `boolean` | 仅管理员可切换（`false` 启用，`true` 隐藏）。 |
| `update_time` | `timestamp` | 本地无时区时间戳（`YYYY-MM-DD HH:MM:SS.ffffff`），每次新增或修改的行都会更新。 |
| `update_user` | `varchar` | 当前登录用户显示名（缺失时回退为工号）。 |

**事务与缓存刷新：**

- `Repository.Apply` 开启单次 PostgreSQL 事务，使用 `SELECT ... WHERE process = $1 FOR UPDATE` 锁定本工序记录，使用 `SELECT id FROM t_mqpd_hc_table WHERE id = ANY($1)` 做全局 ID 查重，仅对真正发生变化的已有行执行参数化 `UPDATE`、对新增行执行参数化 `INSERT`（若无任何实际变更返回 HTTP `409 nothing_to_save`），最后原子提交。
- 事务提交后，`NewFileChangeLog` 向 `dash_data_dir` 与 `cdn_data_folder` 下的 **`MQPDHeadCount.log`** 追加带时间戳的变更日志（触发 Python `LocalListV3.py` `read_mqpd_hc_data` 的 Redis 缓存刷新）。若目录未配置或不可写，数据库保存依然成功，并返回非致命提示 `log_warning`。

## 5. HTTP 路由

所有路由挂载于 `/dash_go/hc_budget` 下：

| 方法与路径 | 鉴权 | 用途 |
|---|---|---|
| `GET /dash_go/hc_budget` | 无 | `301` 重定向到 `/dash_go/hc_budget/`。 |
| `GET /dash_go/hc_budget/` | 会话（未登录跳登录页） | 在 shell 中渲染模块页面（`AppIcon: "budget"`，`ClientPrefixes: ["hb.ui."]`）。 |
| `GET /dash_go/hc_budget/help` | 公开 | 渲染中英文使用手册。 |
| `GET /dash_go/hc_budget/static/...` | 公开 | 提供内嵌的 `app.css` 与 `app.js`（`Cache-Control: no-cache`）。 |
| `GET /dash_go/hc_budget/api/me` | 会话（未登录返回 `401`） | 返回 `{user: {ee, name}, processes, is_admin, version}`。 |
| `GET /dash_go/hc_budget/api/table?process=&include_hidden=0\|1` | 会话 | 返回 `{process, is_admin, include_hidden, rows, defaults, totals}`；非管理员强制 `include_hidden=false`。 |
| `POST /dash_go/hc_budget/api/save` | 会话 + `X-Requested-With: dash_go` | 实时复查权限与管理员角色，在单次 PG 事务中提交 `{process, creates, updates, order}`，追加 `MQPDHeadCount.log`，返回 `{inserted, updated, log_warning?}`。 |

## 6. 配置

`hc_budget` 共用 `dash_go` 顶层配置，并支持在 `modules.hc_budget` 下按模块覆盖：

| 配置键 | 位置 | 默认值 | 说明 |
|---|---|---|---|
| `dash_data_dir` | 顶层 `dash_go.json`（或 `modules.hc_budget.dash_data_dir`） | 无 | 存放 `MQPDHeadCount.log` 的 Python Dash `data` 目录（与 `hc_scan` 共用；模块级设置优先于顶层）。 |
| `cdn_data_folder` | Python `config.json` | 无 | 同步写入 `MQPDHeadCount.log` 的第二目录。 |
| `dash_api_url` | 顶层 `dash_go.json`（或 `modules.hc_budget.dash_api_url`） | `http://<api_server>/api` | 用于 `hc_user_processes` 权限校验与 `dash_go.exe -check`（`Dash API (hc_budget)` 探测）的 Python API 根地址。 |
| `HC_PROCESS_LIST` | Python `config.json` | 必填 | 标准有序 HC 工序列表。 |

## 7. 构建与本地预览

- 默认编译包含本模块（`cmd/dash_go/mod_hc_budget.go`，构建标签 `!nomod_hc_budget`）。
- 单独打包或与 `hc_scan` 一同打包：`.\scripts\build.ps1 -Modules hc_budget` 或 `.\scripts\build.ps1`。
- 内存模式本地预览服务器（无需数据库，命令见上方英文版代码块）。

## 8. 故障排查

- **管理员账号看不到新增、显示已隐藏或排序列：** 检查 Python `appV2.py` 的 `hc_user_processes` 是否已返回 `"type": "admin"`（见第 2 节）。在 `appV2.py` 增加 `'type'` 字段前，`dash_go` 会将所有用户按普通授权用户处理。
- **无法拖动或上下移动行：** 请先清空筛选框；当存在筛选关键字时排序控件会自动禁用，以防对局部列表误排。
- **新增工位号提示“已被占用”，但在当前列表看不到：** 请勾选**显示已隐藏**——工位号唯一性在同工序的启用行与已隐藏行中同时生效（对应主键 `id = <process>_<station_code>`）。
- **保存后 Python Dash 页面未立即刷新：** 查看提示弹窗或 `logs\dash_go-<date>.log` 中的 `MQPDHeadCount.log` 写入告警，确认 `dash_data_dir` / `cdn_data_folder` 路径及写权限。

## 9. 验证状态

**已完成（2026-11-27，版本 0.2.0）：**

- 静态检查与模块化构建：`go vet ./...`、`go vet -tags devserver ./...`、`go build ./cmd/dash_go`（全部模块：`hc_budget, hc_scan`）、`-tags nomod_hc_budget`（仅 `hc_scan`）、`-tags nomod_hc_scan`（仅 `hc_budget`），并通过 `-version` 验证编入模块清单。
- 全量单元测试、HTTP 测试与词条一致性测试（`internal/core/dashapi`、`internal/core/config`、`internal/core/hc`、`internal/core/shell`、`internal/mods/hc_budget`、`internal/mods/hc_scan` 等）：
  - `dashapi.Profile.IsAdmin()` 覆盖 `"admin"`、大小写混合、`"user"` 及省略 `"type"` 场景；
  - 顶层 `dash_data_dir` 配置解析；
  - `hc.DashPermissions` 缓存、TTL 过期、`fresh=true` 实时穿透及 `OrderProcesses`；
  - `hc_budget` 服务层逻辑：工序权限、非管理员对新增/隐藏/编辑隐藏行/重排的 `403` 拦截、单事务批量新增+编辑+隐藏+恢复+排序、未勾选**显示已隐藏**重排时保持隐藏行尾随排序、`station_code` 与全局 `id` 唯一性、`budget_head_count` 正则及留空转 `0`、无变动返回 `409 nothing_to_save`、`MQPDHeadCount.log` 双目录写入与非致命告警；
  - `hc_budget` HTTP 路由、鉴权拦截、中英文页面与使用手册渲染、导览属性，以及 `hb.*` 词条双语一致性/未用键/占位符校验；
  - `shell` 配色对比度、`budget` 图标、`dashGo.picker` 共享选择器与核心词条测试。
- 无头 Chromium 端到端验证（Playwright 对接真实 `hc_budget` + shell `devserver` 内存服务，带 CSP 响应头并人工核对全部渲染截图）：
  - 首页显示 `Version 0.2.0`、`hc_scan`（`#scanner`）与 `hc_budget`（`#budget`）图标；
  - 管理员登录、共享工序选择器与身份提示、新增机台/活动弹窗（自动预填成本中心与工序代码、针对隐藏工位 `999` 的重复提示、实时 ID 预览、暂存 `Winder 535`）、行内编辑预算、隐藏操作、顺序调整（上下按钮与 HTML5 拖放）、确认提交汇总弹窗、保存并重载持久化、**显示已隐藏**开关、深色主题与中文界面；
  - 普通授权用户登录确认所有管理员控件与列均不可见，且行内编辑 + 确认提交正常生效；
  - 公开访问中英文 `/dash_go/hc_budget/help`；零 JS/页面/CSP 报错。

**尚未验证：**

- 真实 PostgreSQL 集成测试（`internal/mods/hc_budget/repo_pg_test.go` 已通过编译检查，但当前环境未配置 `DASH_GO_TEST_PG` 临时库，执行时跳过）。
- 与 Python `appV2.py` 新增 `'type'` 字段的联调（按计划暂未修改 `D:\Programs\Python\dash`）以及 Python `read_mqpd_hc_data` 监听 `MQPDHeadCount.log` 刷新缓存的实测。
- Dash `IE.py` 菜单交接链接跳转 `/dash_go/hc_budget/` 及正式发布打包脚本（`scripts/release.ps1`）。
- 使用者对中文界面文案与使用手册措辞的审阅。
