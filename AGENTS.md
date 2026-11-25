# Project Engineering Instructions

These instructions apply to all AI-assisted development in this repository.

本说明适用于本代码仓库中的所有 AI 辅助开发工作。

---

# English

## 1. General Principles

Prefer correctness, maintainability, compatibility, and clarity over clever solutions.

Before changing existing behavior, understand how the current implementation works.

Reuse existing architecture, patterns, utilities, services, and conventions whenever possible.

Keep changes focused on the requested task.

Avoid unnecessary refactoring, speculative redesign, and unrelated cleanup.

Do not claim that a task, test, verification step, or migration was completed unless it was actually performed.

## 2. Architecture

Do not introduce a new architectural pattern when the existing architecture can reasonably support the requirement.

Prefer existing:

- services
- repositories
- utilities
- components
- configuration mechanisms
- logging systems
- error-handling patterns
- database access patterns
- routing conventions
- background-job patterns
- authentication and authorization mechanisms

Avoid duplicate implementations and parallel frameworks for the same responsibility.

If a major architectural change is necessary, explain why the existing architecture is insufficient before implementing the change.

## 3. Code Changes

Make the smallest complete change that satisfies the requirement.

Do not modify unrelated files.

Do not rename public interfaces, routes, APIs, database objects, or configuration keys without a clear requirement.

Do not remove working behavior simply because another implementation appears cleaner.

Do not leave temporary debug code, temporary files, commented-out obsolete implementations, or unused experimental code.

Preserve backward compatibility unless the approved plan explicitly requires a breaking change.

Before modifying shared code, inspect relevant call sites and assess regression risk.

## 4. Configuration

Do not hard-code:

- machine-specific file paths
- IP addresses
- server hostnames
- credentials
- passwords
- API keys
- access tokens
- environment-specific URLs
- environment-specific database connection strings

Use the project's existing configuration mechanism, such as environment variables or configuration files.

Do not expose secret values in source code, documentation, logs, screenshots, test output, or generated examples.

## 5. Database

Do not perform destructive database operations without explicit approval.

Preserve backward compatibility whenever practical.

For database changes, consider:

- existing data
- migrations
- indexes
- constraints
- nullability
- transactions
- rollback behavior
- concurrency
- duplicate insertion
- data integrity
- performance impact

Use parameterized queries.

Do not construct SQL using untrusted string concatenation.

Do not run production migrations, destructive DDL, bulk data changes, or irreversible operations unless explicitly requested and approved.

When inspecting a database for planning or analysis, prefer read-only queries and minimize query scope.

## 6. API

Preserve existing API contracts unless a breaking change is explicitly required.

Validate external input.

Return predictable error responses.

Do not expose stack traces, credentials, internal file paths, SQL statements containing secrets, or sensitive implementation details to end users.

When modifying an API, inspect known callers and related frontend/backend integrations before finalizing the change.

## 7. Security

Never place secrets in source code.

Do not expose:

- passwords
- API keys
- access tokens
- private keys
- connection credentials
- authentication tokens

Treat authentication and authorization as separate concerns.

Never trust identity, role, permission, path, filename, SQL fragment, shell argument, or other sensitive input supplied directly by an untrusted client.

Use the project's existing security controls where available.

## 8. Error Handling and Logging

Do not silently ignore exceptions.

Handle errors at the appropriate layer.

Use the project's existing logging mechanism.

Provide enough logging to diagnose failures without exposing secrets or sensitive information.

Do not use broad exception handling merely to suppress a real implementation problem.

## 9. Testing and Verification

Changes should be verified proportionally to their risk.

Prefer this order:

1. targeted tests
2. affected integration tests
3. broader regression tests
4. relevant manual workflow verification

Do not modify or weaken tests only to hide a real implementation failure.

Do not claim that a test passed unless it was actually executed and passed.

If verification cannot be completed, clearly state what was not verified and why.

For any non-daemon terminal command or background verification task expected to terminate (tests, builds, browser runs):

- Always configure a bounded timeout / progress notification (for example `NotificationTimeoutSeconds` on `run_command`, plus inner timeouts such as `launch(timeout=...)` and `page.set_default_timeout(...)` in browser scripts) so a stalled task wakes the agent promptly instead of running indefinitely.
- When a timeout notification fires, inspect the task's log and status (`manage_task` `status`), report or diagnose the stall, and cancel stuck tasks (`manage_task` `kill`) rather than leaving them running in the background.
- Keep fast unit tests separate from browser / external-process E2E scripts instead of chaining them into one background command.

## 10. Dependency Management

Do not add a new dependency when the existing stack already provides equivalent functionality.

If a dependency is necessary:

- explain why it is needed
- prefer a maintained and reputable package
- minimize version conflicts
- follow the project's existing dependency-management method

Do not upgrade unrelated dependencies unless necessary for the requested task.

## 11. Documentation Language Requirements

All documentation created or modified by AI agents MUST be bilingual in English and Simplified Chinese.

### 11.1 Language Order

English MUST always appear first.

The required order is:

1. Complete English version
2. Complete Simplified Chinese version

Do not place Chinese before English.

### 11.2 Scope

This rule applies to all documentation content, including but not limited to:

- README files
- Markdown documentation
- implementation plans
- design documents
- architecture documents
- technical specifications
- user guides
- deployment guides
- operation guides
- troubleshooting guides
- API documentation
- database documentation
- migration documents
- release notes
- change logs
- project summaries
- test plans
- handover documents
- generated documentation files

### 11.3 Required Document Structure

Unless there is a strong reason to use another format, documentation MUST use the following structure:

- complete English version first
- horizontal separator if useful
- complete Simplified Chinese version second

The Chinese version MUST mirror the structure and technical content of the English version.

English MUST always be the first complete version.

Recommended structure:

```text
# English

## Overview
...

## Architecture
...

## Database Design
...

---

# 中文

## 概述
...

## 架构
...

## 数据库设计
...
```

### 11.4 Translation Consistency

The English and Chinese versions MUST convey the same technical meaning.

Do not:

- omit information from either language
- simplify one language while keeping the other detailed
- introduce technical information in only one language
- change requirements between language versions
- change code examples between language versions
- change technical identifiers between language versions

Technical terms and identifiers should normally remain unchanged, including:

- product names
- framework names
- file names
- directory paths
- API paths
- database names
- schema names
- table names
- column names
- class names
- function names
- variable names
- environment variables
- configuration keys
- command names

Examples of identifiers that should normally remain unchanged:

- `FastAPI`
- `PostgreSQL`
- `job_id`
- `process_module`
- `DATABASE_URL`
- `/api/jobs`
- `JobService`

### 11.5 Code Blocks

Do not duplicate identical code blocks solely for translation.

Write the English explanation first and the Chinese explanation second, then provide one shared code block when the code itself is identical.

Example:

English:
Configure the database connection using the environment variable below.

中文：
使用以下环境变量配置数据库连接。

```env
DATABASE_URL=postgresql://...
```

Always close fenced code blocks correctly before continuing with normal Markdown content.

Never place unrelated documentation sections inside a code block.

### 11.6 Tables

For small documentation tables, bilingual text may be placed in the same table when it improves readability.

Example:

| Item | Description / 描述 |
|---|---|
| Database | Stores application data. / 存储应用程序数据。 |
| Redis | Used for caching and background task coordination. / 用于缓存和后台任务协调。 |

For large or complex tables, separate English and Chinese tables are preferred. English MUST appear first.

### 11.7 Existing Documentation

When modifying an existing documentation file:

- preserve existing technical content unless the task requires a change
- add the missing language when necessary
- keep English before Chinese
- keep both language versions synchronized
- update both language versions when technical content changes

If an existing document is English-only and the task requires modifying that document, update the affected documentation so that the modified document follows this bilingual requirement whenever practical.

### 11.8 Documentation Generated During Development

When an agent creates documentation as part of a task, the documentation MUST follow this bilingual requirement even if the user does not explicitly repeat the bilingual requirement in that task.

This includes automatically generated planning, design, implementation, review, migration, deployment, and handover documents.

### 11.9 Exceptions

The bilingual documentation requirement does NOT automatically apply to:

- source code
- variable names
- function names
- class names
- database identifiers
- API paths
- configuration keys
- log messages
- exception messages
- Git commit messages
- inline source-code comments

unless the task explicitly requires bilingual content for those items.

## 12. Documentation Quality

Documentation must describe the actual implementation, not an assumed implementation.

Before documenting architecture, APIs, database structures, configuration, or workflows, inspect the relevant implementation when available.

Do not document features as completed if they are only planned.

Do not invent paths, APIs, tables, fields, configuration keys, commands, or behavior that do not exist.

Clearly distinguish among:

- current behavior
- planned behavior
- assumptions
- optional recommendations

Write each kind of documentation where it belongs:

- `AGENTS.md` records only what applies to every app: engineering rules, infrastructure and debugging lessons.
- Documentation of a single module (a page, a crawler, a sync or a task) goes to its own file in `docs/`, together with the lessons learned about that module. Do not add it to `AGENTS.md`.
- `docs/README.md` is the index of those files. Look a module up there before changing or debugging it, and add every new document to it.

## 13. Completion

A task is not complete simply because code was generated.

Completion requires, where applicable:

- implementation
- validation
- regression consideration
- cleanup of temporary artifacts
- synchronized documentation
- clear reporting of unresolved issues

Before finishing, review the final changes for accidental edits, debugging leftovers, hard-coded values, missing error handling, missing tests, and documentation inconsistency.

## 14. Known Pitfalls

Lessons learned from real defects in this project that apply to every app. Check them before debugging a similar symptom. Add new entries as 14.3, 14.4, and so on, in both languages. Lessons about a single module belong in that module's document in `docs/`, not here.

### 14.1 Dash renderer: a page swapped into `#app-content` goes blank or shows stale content after the first click

Found on 2026-11-11 (HC monthly summary by process, Leave button) with the Dash 3.2.0 renderer. Reproduced locally with the real app, fixed in `build_process_summary_div` and confirmed by testing. A similar symptom had been fixed once before without finding the cause, so this may explain other past incidents.

Symptoms:

- The page was opened by a callback that rebuilds `app-content` (tab or mode switch, drill-down, Back), not by F5 or a direct URL.
- The first click inside the page makes the whole page disappear (the banner and tabs stay), or an older build of the page reappears. The page is not dimmed by a loading mask, the click sends no callback request, and the console shows no error.
- F5 fixes it. It looks random because it depends on which pages were opened earlier in the same browser session.

Root cause:

- A component without an `id` gets its React key from its position in the layout. The renderer keeps a change counter per position (`layoutHashes`), and the counter is never reset when another component later takes that position.
- When the counter is not 0, the next re-render of the parent redraws the old component that used to sit at that position instead of the new one. The first click inside `#app-content` triggers it, because Dash counts `n_clicks` on every `html.Div` that has an id, `app-content` included.
- The children of `app-content` are one flat list, `[generate_visit_monitor(data), *build_tabN(data)]`, and `build_tab9` returns `[dmc.Center(...), *hc_div]`. Different HC pages therefore share the same positions from index 2. A page that leaves a non-zero counter there can break the next page opened at that position. HC Data Input does this: its first component, `mqpd-hc-daily-attn-table-container`, is a `FefferyDiv` that updates its own props on mouse and click events.

Rule:

- The root component of a page that a callback swaps into `app-content` must have an `id` that is unique per build, for example `id={'type': 'process-summary-page', 'index': gen_nanoid()}`. React then remounts the page instead of reusing the old wrapper.
- A fixed id is not enough. Rebuilding the same page in place keeps the same key, and the next click brings back the previous build (tested with one id per process).
- Cost: the whole page remounts on every rebuild, including the idle auto refresh, so AG Grid is recreated and the scroll position resets, as after F5. Do not reference this id in callbacks, because it changes on every build.
- Applied so far: `build_process_summary_div` only. Other page builders have not been audited. Apply the rule to a page that shows these symptoms, after checking that losing client state on rebuild is acceptable (for example unsaved edits in an input form).

How to confirm in production: open the page the same way the user did (no F5), do not click yet, and run the code below in the browser console (Dash 3.x exposes `window.store`). Replace `2` with the child index of the blank area under `#app-content`. In the local reproduction the page went blank on the first click whenever the value was not 0, and never when it was 0.

```js
(() => { const s = window.store.getState(); const p = s.paths.strs['app-content'].concat(['props', 'children', 2]).join(','); return s.layoutHashes[p]?.hash || 0 })()
```

Ruled out, so do not spend time on them first: the loading mask, AG Grid data, row count, `rowSpan` and style-function errors (these raise red console errors and the rest of the page stays), server latency and HTTP errors, and the browser or display scaling.

References: plotly/dash#3330 reports a similar problem (children replaced by a callback are not remounted). Per the upstream changelog, #3570 (Dash 4.2.0) addressed it, and #3881 (Dash 4.5.0) fixed a regression of that change and added `dash.remount`. Upgrading Dash was not tried with this project's component libraries.

### 14.2 Popup windows: a popup suddenly shrinks to a small box with scrollbars when a control inside it is used (Plan "Options Settings", Process select)

Found on 2026-11-12 with the Dash 3.2.0 renderer (Plan tab, Options Settings popup, Process select). Reproduced with the real IE app in headless Chrome, fixed with `popup_modal_props()` in `IE.py`, and confirmed in the browser and with `tests/test_popup_window.py`.

Symptoms:

- A popup opened into `popup-window` looks correct when it opens. After the first click on a control inside it (here the Process select), the `.modal` turns into a box of about 300 x 180 px with scrollbars, a transparent background and no page dimming, and the content is squeezed to about 267 px.
- The console shows no error. F5 fixes it. It looks random because it depends on which popups were opened earlier in the same browser session.

Root cause:

- Same renderer defect as 14.1, on the `popup-window` slot. Every popup is rebuilt into that one container as `[generate_visit_monitor(data), html.Div(className='modal', ...)]`. A component without an `id` is keyed by its position, the renderer keeps a change counter per position (`layoutHashes`) that is never reset, and when the counter is not 0 the next re-render of the wrapper redraws the props of the earlier component of that position.
- The Menu popup (`build_menu_window`, `id="menu-window"`), Learn More (`id="markdown"`) and Login (`id="auth-window"`) have an `id` on their `.modal`, so every click inside them raises `n_clicks` and leaves a non-zero counter at `popup-window.props.children[1]`. The `.modal` of `build_popup_window` and `build_right_click_menu` had no `id`, so the next popup reused that wrapper. The first re-render after the new popup was open (the click on the Process select) redrew the old style around the new content, in the reproduction the Kanban right-click menu style (`width: 30rem; height: 18rem; background-color: transparent`).
- Reproduction: open the Menu popup, click inside it twice, close it, open a Kanban right-click menu, dismiss it, open Plan > Options Settings, click the Process select. A fresh page, the Kanban menu alone, and a progress popup before the Plan popup do not reproduce it. It was reproduced with the Menu popup; Learn More and Login have the same structure but were not tried.

Rule:

- The root `.modal` of every popup built into `popup-window` gets a unique `id` per build and `disable_n_clicks=True`, through `**popup_modal_props()`. Do not reference this id in callbacks, because it changes on every build.
- Applied so far: `build_popup_window` and both branches of `build_right_click_menu` in `IE.py`. The fixed-id popups (Menu, Learn More, Login) were left as they are, and the `.modal` builders of `QDB.py`, `MER.py`, `QAQC.py` and `test.py` were not audited. Apply the rule to a popup that shows these symptoms.
- Cost: the whole popup remounts on every rebuild. A callback that returns `build_popup_window(...)` again for a popup that is already open (for example `tab1_hourly_output_input_selection_onchange`) now recreates the popup, so client-only state such as a grid scroll position is lost, as when the popup is opened again.

How to confirm in production: open the popups in the same order as the user, do not click yet, and run the code below in the browser console (Dash 3.x exposes `window.store`). In the reproduction the value was 3 while the Plan popup was open, and the shrink happened on the next click. With the fix the value can still be non-zero and is harmless, because the new `.modal` has its own key, so check the fix by clicking the control instead.

```js
(() => { const s = window.store.getState(); const p = s.paths.strs['popup-window'].concat(['props', 'children', 1]).join(','); return s.layoutHashes[p]?.hash || 0 })()
```

Ruled out, so do not spend time on them first: the Plan settings content and the Process select options (a fresh page does not reproduce it), `plan_table_setting_onclick`, the CSS of `.modal` (the inline style of the element itself changed), and the browser or display scaling.

### 14.3 Dash renderer: a button does nothing at all (no request, no toast, no log) when a `State` of its callback points at a component that is not in the layout (Cell Collection Status, Confirm of a Resp Group member)

Found on 2026-11-17 with the Dash 3.2.0 renderer (LCS Cell Collection Status page, Confirm button of the job popup, used by a member of the Resp Group). Reproduced in headless Chromium with the real popup builder and the real callback of `QAQC.py` (data layer stubbed, nothing written to a database), fixed with `allow_optional=True` and confirmed in the same browser run and with `tests/test_lcs_cell_collection_job.py`.

Symptoms:

- Clicking the button gives no reaction: no toast, no change on the page, no request to `/_dash-update-component`, and no line in the logs of the feature (for the job popup: `data/LCSJob.log`). Nothing reaches the server, so server-side logging cannot show it.
- Only some users or some states of the page are affected. Here: the members of the Resp Group, who get the status-only popup. The admin and the requester get the full popup, where the same button works, so testing as the developer did not show it.
- The browser console shows ``ReferenceError: A nonexistent object was used in an `State` of a Dash callback. The id of this object is {"index":"qaqc-upload","type":"upload-component-result"} and the property is `data`.`` The production apps run without `debug` (`cfg.get('debug', False)`), so there is no error overlay and nobody sees it.

Root cause:

- Before it sends a callback request, the renderer reads the value of every `State`. If a `State` with a fixed id points at a component that is not in the layout at that moment, it throws that `ReferenceError` and the request is never sent. `suppress_callback_exceptions=True` does not prevent it (the reproduction ran with it, like the real apps). A `State` with `ALL` or `ALLSMALLER` that matches nothing is fine and gives an empty list; `MATCH` is not.
- `build_cell_collection_job_window` builds the upload component only when the popup is not status-only (`build_upload_component(...) if status_only is False else html.Div()`), but `lcs_cell_collection_job_confirm` declared `State({'type': 'upload-component-result', 'index': 'qaqc-upload'}, 'data')`.

Rule:

- A `State` or `Input` that points at a component which is not rendered in every state of the page is declared with `allow_optional=True` (Dash 3.1 or later; `requirementsV3.txt` pins 3.2.0). The callback receives `None` for the missing component and its code must accept that.
- Python tests cannot see this, because the browser decides whether the callback runs. Test the declaration, as `tests/test_lcs_cell_collection_job.py` does.
- Applied so far: `lcs_cell_collection_job_confirm`. `lcs_analysis_report_confirm` and `lcs_analysis_report_parse` also read an `upload-component-result` `State` and are not affected, because their buttons only exist when the upload component exists (the read-only report window has no Confirm button). The callbacks of the other apps were not audited.

How to confirm in production: open the popup the way the affected user does, open the browser console (F12) and click the button. The `ReferenceError` appears at the click and the network tab shows no `_dash-update-component` request. To check whether a component exists, run the code below in the console (Dash 3.x exposes `window.store`). The values of an id are listed in the alphabetical order of its keys, here `index` then `type`; replace the text with the id to check. In the reproduction it printed `false` for the status-only popup and `true` for the full popup.

```js
Object.values(window.store.getState().paths.objs).flat().some(o => o.values.join('|') === 'qaqc-upload|upload-component-result')
```

Ruled out, so do not spend time on them first: the Python code of the callback and the permission check (the same code works when the component exists, and refuses a user outside the Resp Group with a toast), the `debounceWait` of the Confirm button, and server errors or logs (the request is never sent).

### 14.4 A refresh is fast on the developer copy and takes tens of seconds in production: one lookup per row in a table with millions of rows (Offline Confirmation refresh)

Found on 2026-11-19 on the Offline Confirmation page and fixed in `read_offline_confirmation_data()`. The numbers, the change and the tests are in [docs/offline_confirmation_page.md](docs/offline_confirmation_page.md) (Refresh Speed). The lesson applies to every reader that looks up or joins a few thousand rows in a big table.

Symptoms:

- After a data update the first visitor waits 20 to 60 s. The same code takes about a second on the developer copy, `EXPLAIN` shows index scans and every index exists, so a missing index looks like the cause and is not.
- The seconds follow the number of rows read, not the size of the data: about 0.1 s for 100 rows, 8 s (median) and up to 63 s for 3,500 to 12,000 rows.

Root cause:

- One primary key lookup per changed row in a table with millions of rows (`t_cd_reject_summary`: 7.2 million rows, 2.7 GB) reads about two pages from disk per row when the table is not in the memory of the database server. The cells in question lay on 11,264 of its 261,296 pages. The developer copy keeps the whole table in memory, so it cannot show the cost. A refresh that reads again the rows it already holds multiplies it.

Rule:

- Do not look up a big table once per changed row when the cache already holds the data of that row. Read the changes of each table with one range query on its `update_time` index, keep what is cached, and look up only the rows that are not cached, in chunks.
- Log the seconds of each step together with the number of rows read, as the refresh log of `read_offline_confirmation_data()` does. Seconds per row tells I/O from CPU.
- Store a big DataFrame that is rewritten often with `redis_hmset(..., compress_level=1)`. The default zlib level 6 of `pickle_dumps()` took 0.4 s to 0.9 s of every refresh; level 1 is about three times faster and the result is read back the same way.

How to see the timings of production without access to it: the developer copy of the `Log` database holds `t_web_log` with the log lines of the web servers up to the date of the copy (check `max("UpdateTime")` first). The rows with `App = 'API'` hold the lines of `fn_timer` (`Function <name> running: <s>(s)`), of `single_thread` (`... single thread running: <s>(s)`) and the refresh lines. Filter on `"UpdateTime"` (indexed) first and on `"Action"` second; 12 days and five patterns took 22 s. Mask the IP addresses before printing anything.

Ruled out, so do not spend time on them first: a missing index (all exist and `EXPLAIN` uses them), the SQL text, the network, and the size of the Redis values (about 2 MB compressed).

### 14.5 A Go service answers with the data of another database: the fallback list of the pgx configuration comes from the connection string that was parsed first (`hc_scan`)

Found on 2026-11-19 by running the real `dash_go` binary against a scratch database. Fixed in `internal/core/module` (`pgDSN`) and covered by `TestPGDSNTargetsOnlyTheConfiguredServer`. It applies to every module that uses the database.

Symptoms:

- A service configured for one database (a non-default port) shows the data of another one, here the developer database on `localhost:5432`. No error is logged and the SQL and the credentials are fine.
- `Get-NetTCPConnection -OwningProcess <pid>` shows the connection of the process on the default port instead of the configured one.
- It only happens when the first connection attempt fails, for example when the server has no TLS and `sslmode` is the default `prefer`.

Root cause:

- `pgxpool.ParseConfig("")` returns a configuration whose `Fallbacks` (`localhost:5432`, with and without TLS) were built from the defaults. Setting `ConnConfig.Host` and `Port` afterwards changes only the first entry. When it fails, pgx silently tries the fallbacks and the service ends up on `localhost:5432`.

Rule:

- Put the whole target (host, port, user, password, database) into one connection string and parse that string. Never patch `Host` or `Port` of a parsed configuration.
- Test a service with the real binary against a scratch database on a non-default port, and look at the connections of the process, not only at the answers.

Ruled out, so do not spend time on them first: the credentials, the SQL text, the pool size and the network.

### 14.6 Dash: a callback that leaves the app by setting `url.href` only changes the address bar, the page stays (`IE.py`, Scan Assign button)

Found on 2026-11-19 when the first version of the Scan Assign button (hand-over to `dash_go`) was tried in the running application. Reproduced in headless Chrome with a stand-in router, fixed in `mqpd_hc_scan_assign_onclick` and covered by `test_the_callback_sets_refresh_together_with_href` (`tests/test_dash_go_link.py`).

Symptoms:

- Clicking the button looks like nothing happens. The address bar shows the new address (here `/dash_go/auth/sso?...`) but the Dash page stays, and no error appears anywhere.
- The nginx access log shows it: the following `POST /ie/_dash-update-component` requests carry the new address as referer.

Root cause:

- The router callback of the app (`rebuild_app_content` in `IE.py`; `MER.py` and `QDB.py` have the same `Output('url', 'refresh')`) answers `url.refresh = False` every time it built a page. With `refresh` False, `dcc.Location` handles a changed `href` with `history.pushState` instead of a page load.

Rule:

- A callback that leaves the app by setting `url.href` must answer `url.refresh = True` in the same answer: `Output("url", "refresh", allow_duplicate=True)` and `Output("url", "href", allow_duplicate=True)`.
- Test it with a stand-in router that answers `refresh = False`. A small app with `refresh=True` fixed in its layout does not show the defect, which is why the first version looked fine in a probe.

Ruled out, so do not spend time on them first: the callback code and its permission checks, the link itself, and the nginx routing.

### 14.7 A new Redis connection of a Python process takes 2 seconds when the host is written as `localhost` (Windows sign-in service, first request after a restart)

Found on 2026-11-23 while the connections of the development machine were tested for `dash_go.exe -check`. Measured with redis-py 5.0.1, built the way `LocalListV3.load_redis` builds it (a `ConnectionPool` with no timeouts), and with the Go client of `dash_go`. Redis 5.0.14.1 on Windows, listening on IPv4 only (`Get-NetTCPConnection -State Listen -LocalPort 6379` shows only `0.0.0.0`).

Symptoms:

- The first command on a new Redis connection of a Python process takes about 2 seconds (2034 to 2058 ms); every later command on that connection takes 0.2 ms. `FastAPI/main.py` (the Windows sign-in service) builds a new `redis.Redis` in `get_redis_client()` for every call, so there every call pays it.
- Redis, its password and the network are fine: the same settings with the host written as `127.0.0.1` answer the first command in 2 ms.
- `dash_go` is not slowed down: its first Redis command on `localhost:6379` takes 2 to 4 ms.

Root cause:

- `localhost` resolves to `::1` first and then to `127.0.0.1`. Nothing listens on `::1:6379`, and a Python `connect()` to it returns the refusal only after about 2 seconds (2022 to 2051 ms, with a blocking socket and with `settimeout(5)` alike). Python tries the addresses one after the other, so the 2 seconds are spent before `127.0.0.1` is tried.
- Go gets the same refusal in 1 ms and connects to `localhost:6379` in 4 ms. Why the two differ in their Windows socket calls was not investigated.

Rule:

- In the Dash configuration, write the host of a Redis on the same server as `127.0.0.1`, not `localhost`. A patch file replaces the whole top-level key of `config.json` (the shallow merge of `LocalListV3.load_config_json`, `FastAPI/main.py` and `dash_go`), so every patch that declares its own `redis` object has to be changed itself: changing `config.json` does not reach it.
- Look at the listening addresses of the server before you change a host: `127.0.0.1` is refused when Redis listens on IPv6 only.
- `dash_go.exe -check` (since 0.1.6) shows the time of a new connection of PostgreSQL and Redis. It tells whether `dash_go` is affected; it says nothing about the Python processes, which have to be measured with a Python client.

How to confirm (no password is needed): time the connection to the refused address and to the good one.

```python
import socket, time
for host, family in (("::1", socket.AF_INET6), ("127.0.0.1", socket.AF_INET)):
    s = socket.socket(family, socket.SOCK_STREAM)
    t0 = time.perf_counter()
    try:
        s.connect((host, 6379))
    except OSError:
        pass
    print(host, round((time.perf_counter() - t0) * 1000), "ms")
    s.close()
```

Ruled out, so do not spend time on them first: Redis itself, its password, the network, the connection pool of redis-py, and `socket_connect_timeout` (a blocking socket takes the same 2 seconds).

### 14.8 Background tasks: a Playwright / headless browser command hangs indefinitely in the Windows terminal sandbox when launched without a timeout notification

Found on 2026-11-24 and 2026-11-25 while running Playwright headless Chromium verification scripts for `hc_scan` (`v0.1.8` and `v0.1.9`). The earlier Go tests chained in the same command finished in ~10 seconds, after which the task remained `RUNNING` for 20+ minutes (or hours) with no output because no timeout notification was configured.

Symptoms:

- A background `run_command` task stays in `RUNNING` state indefinitely after earlier fast steps (such as `go vet` / `go test`) finish and print `ok`.
- The task log shows zero further progress (`Last progress: ... ago`), and the agent is never woken up to inspect or stop it because `NotificationTimeoutSeconds` was omitted.

Root cause:

- Launching Playwright headless Chromium inside the Windows terminal sandbox blocks on Chromium's sandbox/IPC/loopback initialization instead of failing fast with an error.
- Because `NotificationTimeoutSeconds` was not set on `run_command` (and no `timeout` was passed to `chromium.launch(...)`), the background task waited silently until the user intervened or the server restarted.

Rule:

- Always set `NotificationTimeoutSeconds` (for example `30` to `60` seconds) on every non-daemon `run_command` call that is expected to finish, and always set explicit inner timeouts in Playwright scripts (`browser = p.chromium.launch(headless=True, timeout=15000)` and `page.set_default_timeout(10000)`).
- Never chain Playwright / browser E2E scripts onto `go vet` / `go test` in the same command; run them as separate commands so unit-test results are not masked by a browser hang.
- When a timeout notification fires, check `manage_task` (`status`) immediately; if the task is stuck with no progress in the sandbox, cancel it (`kill`) and re-run only the blocked browser/loopback step outside the sandbox with a timeout still enabled.

How to confirm: check `manage_task` with `Action: "status"` on the running task ID; the log shows all prior unit tests passing followed by no new output and a growing `Last progress` age.

Ruled out, so do not spend time on them first: Go unit test deadlocks (`go test` already printed `ok` before Python/Playwright started) and application JavaScript infinite loops (the exact same Playwright script passes in ~10 seconds once run outside the Windows sandbox).

---

# 中文

## 1. 通用原则

优先考虑正确性、可维护性、兼容性和清晰度，而不是追求过度复杂或炫技式的解决方案。

在修改现有行为之前，先理解当前实现的工作方式。

尽可能复用现有架构、模式、工具、服务和项目约定。

修改范围应聚焦于当前任务。

避免不必要的重构、推测性的架构重设计以及与任务无关的清理工作。

除非实际已经执行，否则不得声称某项任务、测试、验证步骤或迁移已经完成。

## 2. 架构

如果现有架构可以合理支持需求，不要引入新的架构模式。

优先使用现有的：

- services
- repositories
- utilities
- components
- 配置机制
- 日志系统
- 错误处理模式
- 数据库访问模式
- 路由约定
- 后台任务模式
- 身份验证和授权机制

避免为相同职责创建重复实现或并行框架。

如果确实需要进行较大的架构调整，应在实施前说明现有架构为什么无法满足需求。

## 3. 代码修改

采用能够完整满足需求的最小修改范围。

不要修改与任务无关的文件。

除非需求明确要求，否则不要重命名公共接口、路由、API、数据库对象或配置项。

不要仅仅因为另一种实现看起来更简洁，就删除目前正常工作的行为。

不要遗留临时调试代码、临时文件、已废弃的注释代码或未使用的实验代码。

除非已批准的方案明确要求破坏性变更，否则应保持向后兼容。

修改共享代码前，应检查相关调用位置并评估回归风险。

## 4. 配置

不要硬编码：

- 特定机器的文件路径
- IP 地址
- 服务器主机名
- 凭据
- 密码
- API key
- access token
- 特定环境 URL
- 特定环境数据库连接字符串

应使用项目现有的配置机制，例如环境变量或配置文件。

不要在源代码、文档、日志、截图、测试输出或生成的示例中暴露机密值。

## 5. 数据库

未经明确批准，不要执行破坏性的数据库操作。

在可行的情况下保持向后兼容。

数据库变更应考虑：

- 现有数据
- migration
- index
- constraint
- nullability
- transaction
- rollback behavior
- concurrency
- duplicate insertion
- data integrity
- performance impact

使用参数化查询。

不要通过拼接不可信字符串构造 SQL。

除非得到明确请求和批准，否则不要执行生产环境 migration、破坏性 DDL、大批量数据修改或不可逆操作。

在规划或分析阶段检查数据库时，应优先使用只读查询，并尽量缩小查询范围。

## 6. API

除非明确需要破坏性变更，否则应保持现有 API contract。

验证外部输入。

返回可预测的错误响应。

不要向最终用户暴露 stack trace、凭据、内部文件路径、包含机密信息的 SQL 或其他敏感实现细节。

修改 API 时，在最终确认前检查已知调用方以及相关前后端集成。

## 7. 安全

绝不要把机密信息写入源代码。

不要暴露：

- 密码
- API key
- access token
- private key
- connection credential
- authentication token

身份验证和授权必须作为两个独立问题处理。

绝不能直接信任来自不可信客户端的身份、角色、权限、路径、文件名、SQL 片段、shell 参数或其他敏感输入。

如果项目已经存在安全控制机制，应优先复用。

## 8. 错误处理与日志

不要静默忽略异常。

应在正确的层级处理错误。

使用项目现有日志机制。

日志应包含足够的诊断信息，但不得暴露机密或敏感内容。

不要为了掩盖真实实现问题而使用过于宽泛的异常捕获。

## 9. 测试与验证

修改应按照风险程度进行相应验证。

推荐顺序：

1. 针对性测试
2. 受影响的集成测试
3. 更广泛的回归测试
4. 相关手工流程验证

不要仅仅为了让测试通过而修改或削弱测试，从而掩盖真实实现问题。

除非测试实际执行并通过，否则不得声称测试已经通过。

如果无法完成某项验证，应明确说明未验证的内容及原因。

对于所有预期会自动结束的非守护进程终端命令或后台验证任务（测试、构建、浏览器脚本等）：

- 必须设置明确的超时与进度监控（例如在 `run_command` 上设置 `NotificationTimeoutSeconds`，并在浏览器脚本中设置内部超时如 `launch(timeout=...)` 和 `page.set_default_timeout(...)`），确保任务卡住时能及时唤醒并处理，而不会无限期挂起。
- 收到超时通知后，应及时查看任务状态和日志（`manage_task` 的 `status`），定位卡住的原因，并终止确实卡死的任务（`manage_task` 的 `kill`），不得让无效任务长期残留在后台。
- 将快速的单元测试与浏览器 / 外部进程端到端脚本拆分为独立命令运行，不要串在同一个后台命令里。

## 10. 依赖管理

如果现有技术栈已经提供等效功能，不要引入新的依赖。

如果确实需要新增依赖：

- 说明为什么需要
- 优先使用维护活跃且可信的包
- 尽量减少版本冲突
- 遵循项目现有的依赖管理方式

除非当前任务确实需要，否则不要升级无关依赖。

## 11. 文档语言要求

所有由 AI Agent 创建或修改的文档都必须同时提供英文和简体中文版本。

### 11.1 语言顺序

英文必须始终放在前面。

固定顺序为：

1. 完整英文版
2. 完整简体中文版

不要把中文放在英文之前。

### 11.2 适用范围

此规则适用于所有文档内容，包括但不限于：

- README 文件
- Markdown 文档
- implementation plan
- design document
- architecture document
- technical specification
- user guide
- deployment guide
- operation guide
- troubleshooting guide
- API documentation
- database documentation
- migration document
- release note
- change log
- project summary
- test plan
- handover document
- 自动生成的文档文件

### 11.3 文档结构要求

除非存在明确理由采用其他格式，否则文档必须使用以下结构：

- 先提供完整英文版
- 必要时使用水平分隔线
- 再提供完整简体中文版

中文版必须与英文版保持相同的结构和技术内容。

英文必须始终作为第一个完整版本。

推荐结构：

```text
# English

## Overview
...

## Architecture
...

## Database Design
...

---

# 中文

## 概述
...

## 架构
...

## 数据库设计
...
```

### 11.4 翻译一致性

英文版和中文版必须表达相同的技术含义。

禁止：

- 任一语言遗漏内容
- 一个版本被简化，而另一个版本保留详细内容
- 只在某一种语言中加入技术信息
- 两种语言中的需求不一致
- 两种语言使用不同的代码示例
- 两种语言修改技术标识符

以下技术术语和标识符通常应保持原样：

- 产品名称
- 框架名称
- 文件名
- 目录路径
- API path
- database name
- schema name
- table name
- column name
- class name
- function name
- variable name
- environment variable
- configuration key
- command name

通常不应翻译的标识符示例：

- `FastAPI`
- `PostgreSQL`
- `job_id`
- `process_module`
- `DATABASE_URL`
- `/api/jobs`
- `JobService`

### 11.5 代码块

不要仅为了翻译而重复完全相同的代码块。

当代码本身相同时，应先写英文说明，再写中文说明，然后只提供一个共享代码块。

示例：

English:
Configure the database connection using the environment variable below.

中文：
使用以下环境变量配置数据库连接。

```env
DATABASE_URL=postgresql://...
```

继续正常 Markdown 内容之前，必须正确关闭 fenced code block。

绝不能因为代码块没有正确关闭而把后续无关文档内容包含在代码块中。

### 11.6 表格

对于较小的文档表格，如果能提升可读性，可以在同一个表格中提供双语内容。

示例：

| Item | Description / 描述 |
|---|---|
| Database | Stores application data. / 存储应用程序数据。 |
| Redis | Used for caching and background task coordination. / 用于缓存和后台任务协调。 |

对于大型或复杂表格，优先使用独立的英文表格和中文表格。英文必须放在前面。

### 11.7 现有文档

修改现有文档时：

- 除非任务要求修改，否则保留现有技术内容
- 必要时补充缺失的语言版本
- 保持英文在前、中文在后
- 保证两个语言版本同步
- 技术内容发生变化时，同时更新两个语言版本

如果现有文档只有英文，而当前任务需要修改该文档，则在可行的情况下，应使修改后的文档符合双语要求。

### 11.8 开发过程中生成的文档

Agent 在任务过程中创建任何文档时，即使用户没有在该任务中再次明确说明双语要求，该文档仍必须遵守本双语规则。

这包括自动生成的 planning、design、implementation、review、migration、deployment 和 handover 文档。

### 11.9 例外

以下内容默认不要求中英文双语：

- source code
- variable name
- function name
- class name
- database identifier
- API path
- configuration key
- log message
- exception message
- Git commit message
- inline source-code comment

除非当前任务明确要求这些内容也使用双语。

## 12. 文档质量

文档必须描述实际实现，而不是假设中的实现。

在记录架构、API、数据库结构、配置或工作流程之前，如果相关实现可用，应先检查真实实现。

如果某项功能仍处于计划阶段，不要把它写成已经完成。

不要虚构不存在的路径、API、表、字段、配置项、命令或行为。

必须清楚区分：

- 当前行为
- 计划行为
- 假设
- 可选建议

文档要写在应该写的位置：

- `AGENTS.md` 只记录适用于所有 app 的内容：工程规则、基础设施和调试经验。
- 单个 module（页面、crawler、同步任务或定时任务）的文档写入 `docs/` 下它自己的文件，该 module 的经验教训也写在同一个文件里，不要加到 `AGENTS.md`。
- `docs/README.md` 是这些文件的索引。修改或排查某个 module 之前先在其中查找，新增文档时也要把它加到索引中。

## 13. 完成标准

仅生成代码并不意味着任务已经完成。

在适用情况下，完成标准包括：

- 实现完成
- 验证完成
- 考虑回归风险
- 清理临时产物
- 文档保持同步
- 清楚报告未解决问题

结束任务前，应检查最终修改是否存在意外变更、调试残留、硬编码值、缺失错误处理、缺失测试或中英文文档不一致的问题。

## 14. 已知问题与经验

来自本项目真实缺陷的经验，仅收录适用于所有 app 的内容。再次排查类似现象前先查阅。新增条目请按 14.3、14.4……编号，并同时更新中英文两个版本。只涉及单个 module 的经验写入 `docs/` 中该 module 的文档，不要写在这里。

### 14.1 Dash 渲染器：通过回调换入 `#app-content` 的页面在第一次点击后变空白或显示旧内容

发现于 2026-11-11（HC monthly summary by process 页面的 Leave 按钮），Dash 3.2.0 渲染器。已用真实应用在本地复现，在 `build_process_summary_div` 中修复，并经测试确认。此前曾出现过类似现象，当时修好了但没有找到原因，所以它可能也能解释其他历史问题。

现象：

- 页面是通过重建 `app-content` 的回调打开的（切换标签页或模式、下钻、Back），而不是 F5 或直接打开 URL。
- 在页面内第一次点击后，整个页面消失（顶部横幅和标签页还在），或者页面的旧版本重新出现。页面没有被加载遮罩变暗，这次点击没有发出任何回调请求，控制台也没有报错。
- F5 后恢复正常。看起来是随机的，因为它取决于同一浏览器会话中之前打开过哪些页面。

根因：

- 没有 `id` 的组件，其 React key 取决于它在布局中的位置。渲染器为每个位置保存一个变更计数（`layoutHashes`），之后即使其他组件占用了该位置，这个计数也不会清零。
- 计数不为 0 时，父组件下一次重新渲染会重绘原先位于该位置的旧组件，而不是新组件。在 `#app-content` 内的第一次点击就会触发，因为 Dash 会对每个带 id 的 `html.Div`（包括 `app-content`）累加 `n_clicks`。
- `app-content` 的子节点是一个扁平列表 `[generate_visit_monitor(data), *build_tabN(data)]`，而 `build_tab9` 返回 `[dmc.Center(...), *hc_div]`。因此不同的 HC 页面从下标 2 开始共用相同的位置。如果某个页面在该位置留下了非 0 的计数，就可能让随后在该位置打开的页面出问题。HC Data Input 就是这样：它的第一个组件 `mqpd-hc-daily-attn-table-container` 是 `FefferyDiv`，会在鼠标和点击事件中更新自己的属性。

规则：

- 由回调换入 `app-content` 的页面，其根组件必须带一个每次构建都唯一的 `id`，例如 `id={'type': 'process-summary-page', 'index': gen_nanoid()}`。这样 React 会重新挂载页面，而不是复用旧的 wrapper。
- 固定 id 不够。原地重建同一页面时 key 不变，下一次点击会让上一次构建的内容重新出现（已用"每个 process 一个固定 id"测试）。
- 代价：每次重建（包括空闲自动刷新）整个页面都会重新挂载，AG Grid 重新创建，滚动位置重置，效果和 F5 一样。不要在回调中引用这个 id，因为它每次构建都会变。
- 目前只应用于 `build_process_summary_div`。其他页面构建函数没有逐一检查。仅对出现上述现象的页面应用该规则，并先确认重建时丢失前端状态是可以接受的（例如输入表单中未保存的编辑内容）。

如何在生产环境确认：按用户的方式打开页面（不要 F5），先不要点击，在浏览器控制台运行下面的代码（Dash 3.x 暴露了 `window.store`）。把 `2` 换成 `#app-content` 下空白区域对应的子节点下标。本地复现中，该值不为 0 时第一次点击必定空白，为 0 时从未空白。

```js
(() => { const s = window.store.getState(); const p = s.paths.strs['app-content'].concat(['props', 'children', 2]).join(','); return s.layoutHashes[p]?.hash || 0 })()
```

已排除，所以不要优先在这些方向上花时间：加载遮罩、AG Grid 数据、行数、`rowSpan` 和样式函数错误（这些会在控制台报红色错误，页面其余部分仍在）、服务器延迟和 HTTP 错误、浏览器或显示缩放。

参考：plotly/dash#3330 报告了类似问题（回调替换的子节点没有重新挂载）。根据上游 changelog，#3570（Dash 4.2.0）处理了它，#3881（Dash 4.5.0）修复了该改动引入的回归并新增了 `dash.remount`。尚未在本项目的组件库上试过升级 Dash。

### 14.2 弹出窗口：操作窗口内的控件时，弹窗突然缩成带滚动条的小框（Plan 的 "Options Settings"，选择 Process 时）

发现于 2026-11-12，Dash 3.2.0 渲染器（Plan 标签页，Options Settings 弹窗，Process 下拉框）。已用真实的 IE 应用在无头 Chrome 中复现，通过 `IE.py` 中的 `popup_modal_props()` 修复，并在浏览器和 `tests/test_popup_window.py` 中确认。

现象：

- 打开到 `popup-window` 中的弹窗，刚打开时是正常的。第一次点击窗口内的控件（这里是 Process 下拉框）后，`.modal` 变成约 300 x 180 px 的小框，带滚动条、背景透明、页面不再变暗，内容被挤到约 267 px 宽。
- 控制台没有报错。F5 后恢复正常。看起来是随机的，因为它取决于同一浏览器会话中之前打开过哪些弹窗。

根因：

- 与 14.1 是同一个渲染器缺陷，发生在 `popup-window` 这个位置上。所有弹窗都被重建到这同一个容器中，结构为 `[generate_visit_monitor(data), html.Div(className='modal', ...)]`。没有 `id` 的组件，其 key 取决于它所在的位置；渲染器为每个位置保存一个从不清零的变更计数（`layoutHashes`）；计数不为 0 时，该位置的 wrapper 在下一次重新渲染时，会重绘这个位置上更早的那个组件的属性。
- Menu 弹窗（`build_menu_window`，`id="menu-window"`）、Learn More（`id="markdown"`）和 Login（`id="auth-window"`）的 `.modal` 带有 `id`，所以在它们里面的每次点击都会累加 `n_clicks`，并在 `popup-window.props.children[1]` 上留下非 0 的计数。`build_popup_window` 和 `build_right_click_menu` 的 `.modal` 没有 `id`，所以下一个弹窗复用了这个 wrapper。新弹窗打开后的第一次重新渲染（点击 Process 下拉框）会在新内容外面重绘旧样式，复现中重绘的是 Kanban 右键菜单的样式（`width: 30rem; height: 18rem; background-color: transparent`）。
- 复现步骤：打开 Menu 弹窗，在里面点击两次，关闭，打开 Kanban 右键菜单，让它消失，打开 Plan > Options Settings，点击 Process 下拉框。全新页面、只打开 Kanban 菜单、以及在 Plan 弹窗之前打开 progress 弹窗，都不会复现。复现用的是 Menu 弹窗；Learn More 和 Login 的结构相同，但没有试过。

规则：

- 所有构建到 `popup-window` 中的弹窗，其根 `.modal` 必须通过 `**popup_modal_props()` 获得每次构建都唯一的 `id`，并设置 `disable_n_clicks=True`。不要在回调中引用这个 id，因为它每次构建都会变。
- 目前已应用于：`IE.py` 中的 `build_popup_window` 和 `build_right_click_menu` 的两个分支。固定 id 的弹窗（Menu、Learn More、Login）保持不变，`QDB.py`、`MER.py`、`QAQC.py` 和 `test.py` 中的 `.modal` 构建函数没有检查过。对出现这些现象的弹窗应用该规则。
- 代价：每次重建都会重新挂载整个弹窗。对已经打开的弹窗再次返回 `build_popup_window(...)` 的回调（例如 `tab1_hourly_output_input_selection_onchange`）现在会重新创建弹窗，所以只存在于前端的状态（如表格滚动位置）会丢失，效果与重新打开弹窗相同。

如何在生产环境确认：按用户的顺序打开弹窗，先不要点击，在浏览器控制台运行下面的代码（Dash 3.x 暴露了 `window.store`）。复现中 Plan 弹窗打开时该值为 3，下一次点击就缩小了。修复后该值仍可能不为 0，但没有影响，因为新的 `.modal` 有自己的 key，所以请通过点击控件来验证修复，而不是看这个计数。

```js
(() => { const s = window.store.getState(); const p = s.paths.strs['popup-window'].concat(['props', 'children', 1]).join(','); return s.layoutHashes[p]?.hash || 0 })()
```

已排除，所以不要优先在这些方向上花时间：Plan settings 的内容和 Process 下拉框选项（全新页面不会复现）、`plan_table_setting_onclick`、`.modal` 的 CSS（变化的是元素自身的 inline style），以及浏览器或显示缩放。

### 14.3 Dash 渲染器：回调的某个 `State` 指向布局中不存在的组件时，按钮完全没有反应（没有请求、没有 toast、没有日志）（Cell Collection Status，Resp Group 成员点击 Confirm）

发现于 2026-11-17，Dash 3.2.0 渲染器（LCS Cell Collection Status 页面，job 弹窗的 Confirm 按钮，由 Resp Group 的成员使用）。已用 `QAQC.py` 中真实的弹窗构建函数和真实的回调在无头 Chromium 中复现（数据层用桩代替，没有写入任何数据库），通过 `allow_optional=True` 修复，并在同一个浏览器测试和 `tests/test_lcs_cell_collection_job.py` 中确认。

现象：

- 点击按钮没有任何反应：没有 toast，页面没有变化，没有向 `/_dash-update-component` 发出请求，该功能的日志里也没有记录（job 弹窗对应 `data/LCSJob.log`）。请求根本没有到达服务器，所以服务器端的日志看不到它。
- 只影响部分用户或页面的部分状态。这里是：Resp Group 的成员，他们得到的是只能改状态的弹窗。管理员和申请人得到的是完整弹窗，同一个按钮可以正常工作，所以开发者用自己的账号测试时看不出来。
- 浏览器控制台有 ``ReferenceError: A nonexistent object was used in an `State` of a Dash callback. The id of this object is {"index":"qaqc-upload","type":"upload-component-result"} and the property is `data`.`` 生产环境的各个 app 不以 `debug` 运行（`cfg.get('debug', False)`），没有错误浮层，所以没人看得到。

根因：

- 渲染器在发出回调请求之前，会读取每个 `State` 的值。如果某个固定 id 的 `State` 指向此刻布局中不存在的组件，它就会抛出这个 `ReferenceError`，请求也就永远不会发出。`suppress_callback_exceptions=True` 不能阻止这一点（复现时就开着它，和真实的 app 一样）。匹配不到任何组件的 `ALL` 或 `ALLSMALLER` 形式的 `State` 没有问题，得到的是空列表；`MATCH` 则不行。
- `build_cell_collection_job_window` 只有在弹窗不是"只改状态"时才会构建上传组件（`build_upload_component(...) if status_only is False else html.Div()`），而 `lcs_cell_collection_job_confirm` 声明了 `State({'type': 'upload-component-result', 'index': 'qaqc-upload'}, 'data')`。

规则：

- 指向"并非页面每种状态下都会渲染的组件"的 `State` 或 `Input`，必须声明 `allow_optional=True`（Dash 3.1 及以上；`requirementsV3.txt` 固定为 3.2.0）。缺失的组件在回调中得到 `None`，回调代码必须能处理它。
- Python 测试看不到这个问题，因为回调是否执行由浏览器决定。要测试声明本身，就像 `tests/test_lcs_cell_collection_job.py` 那样。
- 目前已应用于：`lcs_cell_collection_job_confirm`。`lcs_analysis_report_confirm` 和 `lcs_analysis_report_parse` 也读取 `upload-component-result` 的 `State`，但不受影响，因为它们的按钮只在上传组件存在时才存在（只读的 report 窗口没有 Confirm 按钮）。其他 app 的回调没有检查过。

如何在生产环境确认：按受影响用户的方式打开弹窗，打开浏览器控制台（F12）并点击按钮。点击时会出现 `ReferenceError`，网络面板里也看不到 `_dash-update-component` 请求。要检查某个组件是否存在，在控制台运行下面的代码（Dash 3.x 暴露了 `window.store`）。id 的各个值按其键名的字母顺序排列，这里是先 `index` 后 `type`；把文本换成要检查的 id。复现中，只改状态的弹窗输出 `false`，完整弹窗输出 `true`。

```js
Object.values(window.store.getState().paths.objs).flat().some(o => o.values.join('|') === 'qaqc-upload|upload-component-result')
```

已排除，所以不要优先在这些方向上花时间：回调的 Python 代码和权限检查（组件存在时同一份代码工作正常，并且会用 toast 拒绝不在 Resp Group 中的用户）、Confirm 按钮的 `debounceWait`，以及服务器错误或日志（请求根本没有发出）。

### 14.4 刷新在开发机副本上很快，在生产环境却要几十秒：对有数百万行的表逐行查找（Offline Confirmation 刷新）

发现于 2026-11-19，在 Offline Confirmation 页面上，并在 `read_offline_confirmation_data()` 中修复。数据、改动和测试见 [docs/offline_confirmation_page.md](docs/offline_confirmation_page.md)（Refresh Speed）。这个经验适用于所有要在大表中查找或关联几千行的读取函数。

现象：

- 数据更新后，第一个访问的人要等 20 到 60 秒。同一份代码在开发机副本上只要约一秒，`EXPLAIN` 显示走的是索引扫描，所有索引都存在，所以看起来像是缺索引，其实不是。
- 耗时跟读取的行数成正比，而不是跟数据总量成正比：100 行约 0.1 秒，3,500 到 12,000 行则中位数 8 秒，最长 63 秒。

根因：

- 在有数百万行的表（`t_cd_reject_summary`：720 万行，2.7 GB）里，对每个有变化的行做一次主键查找，在该表不在数据库服务器内存中时，每行要从磁盘读约两个页。涉及的 cell 分散在它 261,296 个页中的 11,264 个页上。开发机副本把整张表放在内存里，所以看不出这笔开销。如果刷新还要重复读取缓存中已有的行，开销会成倍增加。

规则：

- 缓存中已经有某一行的数据时，不要为这一行再去大表里查一次。对每张表用 `update_time` 索引做一次范围查询读取变化的行，保留缓存里已有的内容，只对缓存里没有的行分批查找。
- 记录每个步骤的耗时，并同时记录读取的行数，就像 `read_offline_confirmation_data()` 的刷新日志那样。每行耗时可以区分是 I/O 还是 CPU。
- 经常被重写的大 DataFrame，用 `redis_hmset(..., compress_level=1)` 存储。`pickle_dumps()` 默认的 zlib 级别 6 每次刷新要花 0.4 秒到 0.9 秒，级别 1 大约快三倍，读取方式不变。

如何在无法访问生产环境时查看它的耗时：开发机副本的 `Log` 数据库里有 `t_web_log`，其中是 web 服务器截至副本日期的日志行（先查 `max("UpdateTime")`）。`App = 'API'` 的行里有 `fn_timer` 的行（`Function <name> running: <s>(s)`）、`single_thread` 的行（`... single thread running: <s>(s)`）和刷新日志行。先按 `"UpdateTime"`（有索引）过滤，再按 `"Action"` 过滤；12 天、五个匹配条件用了 22 秒。打印任何内容之前先屏蔽 IP 地址。

已排除，所以不要优先在这些方向上花时间：缺少索引（索引都在，`EXPLAIN` 也用到了它们）、SQL 语句本身、网络，以及 Redis 值的大小（压缩后约 2 MB）。

### 14.5 Go 服务返回了另一个数据库的数据：pgx 配置中的 fallback 列表来自最先解析的连接字符串（`hc_scan`）

发现于 2026-11-19，用真实的 `dash_go` 可执行文件连接临时数据库时发现。已在 `internal/core/module`（`pgDSN`）中修复，并由 `TestPGDSNTargetsOnlyTheConfiguredServer` 覆盖。适用于所有使用数据库的模块。

现象：

- 配置为连接某个数据库（非默认端口）的服务，显示的却是另一个数据库的数据，这里是 `localhost:5432` 上的开发库。没有任何错误日志，SQL 和凭据都没有问题。
- `Get-NetTCPConnection -OwningProcess <pid>` 显示该进程的数据库连接在默认端口，而不是配置的端口。
- 只有在第一次连接尝试失败时才会发生，例如服务器没有启用 TLS 而 `sslmode` 是默认的 `prefer`。

根因：

- `pgxpool.ParseConfig("")` 返回的配置，其 `Fallbacks`（`localhost:5432`，分别带 TLS 和不带 TLS）是根据默认值生成的。之后再设置 `ConnConfig.Host` 和 `Port`，只改变了第一项。第一项失败时，pgx 会悄悄尝试 fallback，于是服务最终连到了 `localhost:5432`。

规则：

- 把完整的目标（host、port、user、password、database）放进同一个连接字符串，再解析这个字符串。绝不要修改已解析配置的 `Host` 或 `Port`。
- 用真实的可执行文件对非默认端口的临时数据库测试服务，并且要查看进程的连接，而不仅仅看返回的结果。

已排除，所以不要优先在这些方向上花时间：凭据、SQL 语句本身、连接池大小和网络。

### 14.6 Dash：通过设置 `url.href` 离开应用的回调只会改变地址栏，页面不会离开（`IE.py`，Scan Assign 按钮）

发现于 2026-11-19，Scan Assign 按钮（交接到 `dash_go`）的第一个版本在运行中的应用里试用时。已在无头 Chrome 中用模拟路由复现，在 `mqpd_hc_scan_assign_onclick` 中修复，并由 `test_the_callback_sets_refresh_together_with_href`（`tests/test_dash_go_link.py`）覆盖。

现象：

- 点击按钮好像什么都没发生。地址栏显示了新地址（这里是 `/dash_go/auth/sso?...`），但 Dash 页面还在，哪里都没有报错。
- nginx 访问日志可以看出来：之后的 `POST /ie/_dash-update-component` 请求的 referer 是新地址。

根因：

- 应用的路由回调（`IE.py` 中的 `rebuild_app_content`；`MER.py` 和 `QDB.py` 有同样的 `Output('url', 'refresh')`）每次构建页面后都返回 `url.refresh = False`。`refresh` 为 False 时，`dcc.Location` 用 `history.pushState` 处理变化的 `href`，而不是加载页面。

规则：

- 通过设置 `url.href` 离开应用的回调，必须在同一次返回中同时返回 `url.refresh = True`：`Output("url", "refresh", allow_duplicate=True)` 和 `Output("url", "href", allow_duplicate=True)`。
- 用返回 `refresh = False` 的模拟路由来测试。布局里固定 `refresh=True` 的小应用看不到这个缺陷，所以第一个版本在探针里看起来是正常的。

已排除，所以不要优先在这些方向上花时间：回调代码及其权限检查、链接本身，以及 nginx 路由。

### 14.7 Python 进程的新 Redis 连接在主机写成 `localhost` 时要 2 秒（Windows 登录服务，以及重启后的第一个请求）

发现于 2026-11-23，当时为 `dash_go.exe -check` 测试开发机的各项连接。用 redis-py 5.0.1 测得，构造方式与 `LocalListV3.load_redis` 相同（不带任何超时的 `ConnectionPool`），同时也用 `dash_go` 的 Go 客户端测过。Windows 上的 Redis 5.0.14.1，只监听 IPv4（`Get-NetTCPConnection -State Listen -LocalPort 6379` 只显示 `0.0.0.0`）。

现象：

- Python 进程在新 Redis 连接上的第一条命令要约 2 秒（2034 到 2058 ms）；该连接上之后的每条命令只要 0.2 ms。`FastAPI/main.py`（Windows 登录服务）每次调用都会在 `get_redis_client()` 中新建一个 `redis.Redis`，所以那里每次调用都要付出这 2 秒。
- Redis、它的密码和网络都没有问题：同样的设置，把主机写成 `127.0.0.1`，第一条命令 2 ms 就应答。
- `dash_go` 没有变慢：它在 `localhost:6379` 上的第一条 Redis 命令只要 2 到 4 ms。

根因：

- `localhost` 先解析到 `::1`，再解析到 `127.0.0.1`。`::1:6379` 上没有任何监听，Python 的 `connect()` 要约 2 秒之后才拿到拒绝（2022 到 2051 ms，阻塞套接字和 `settimeout(5)` 都一样）。Python 是一个地址接一个地址地尝试，所以这 2 秒是在尝试 `127.0.0.1` 之前花掉的。
- Go 在 1 ms 内就拿到同样的拒绝，并在 4 ms 内连上 `localhost:6379`。两者在 Windows 套接字调用上为何不同，没有深究。

规则：

- 在 Dash 配置中，把与本机同一台服务器上的 Redis 的主机写成 `127.0.0.1`，不要写 `localhost`。patch 文件会整体替换 `config.json` 的顶层键（`LocalListV3.load_config_json`、`FastAPI/main.py` 和 `dash_go` 的浅合并），所以每一个自己声明了 `redis` 对象的 patch 都要单独修改：只改 `config.json` 够不到它们。
- 修改主机之前，先查这台服务器的监听地址：Redis 只监听 IPv6 时，`127.0.0.1` 会被拒绝。
- `dash_go.exe -check`（自 0.1.6 起）会显示 PostgreSQL 和 Redis 新连接的耗时。它能说明 `dash_go` 是否受影响；对 Python 进程它什么也说明不了，Python 进程要用 Python 客户端去测。

如何确认（不需要密码）：分别给被拒绝的地址和正常的地址计时。

```python
import socket, time
for host, family in (("::1", socket.AF_INET6), ("127.0.0.1", socket.AF_INET)):
    s = socket.socket(family, socket.SOCK_STREAM)
    t0 = time.perf_counter()
    try:
        s.connect((host, 6379))
    except OSError:
        pass
    print(host, round((time.perf_counter() - t0) * 1000), "ms")
    s.close()
```

已排除，所以不要优先在这些方向上花时间：Redis 本身、它的密码、网络、redis-py 的连接池，以及 `socket_connect_timeout`（阻塞套接字同样要 2 秒）。

### 14.8 后台任务：在 Windows 终端沙盒中运行 Playwright / 无头浏览器命令且未设置超时通知时，会无限期挂起

发现于 2026-11-24 与 2026-11-25，在为 `hc_scan`（`v0.1.8` 和 `v0.1.9`）运行 Playwright 无头 Chromium 验证脚本时出现。串在同一个命令前面的 Go 单元测试在约 10 秒内即已通过，随后任务停留在 `RUNNING` 状态超过 20 分钟（甚至数小时）且无任何输出，因为没有配置超时通知。

现象：

- 后台 `run_command` 任务在前面的快速步骤（如 `go vet` / `go test`）打印 `ok` 后一直停留在 `RUNNING` 状态。
- 任务日志不再有任何进展（`Last progress: ... ago`），由于未设置 `NotificationTimeoutSeconds`，Agent 永远不会被唤醒去检查或终止它。

根因：

- 在 Windows 终端沙盒内启动 Playwright 无头 Chromium 时，会在 Chromium 的沙盒 / IPC / 本地回环初始化阶段阻塞，而不是快速报错退出。
- 由于 `run_command` 未设置 `NotificationTimeoutSeconds`（且未给 `chromium.launch(...)` 传入 `timeout`），后台任务会一直静默等待，直到用户手动介入或服务重启。

规则：

- 对所有预期会结束的非守护 `run_command` 调用，必须设置 `NotificationTimeoutSeconds`（例如 `30` 到 `60` 秒），并在 Playwright 脚本中显式设置内部超时（`browser = p.chromium.launch(headless=True, timeout=15000)` 与 `page.set_default_timeout(10000)`）。
- 不要把 Playwright / 浏览器端到端脚本与 `go vet` / `go test` 串在同一条命令中执行；拆分运行，避免浏览器挂起掩盖单元测试结果。
- 收到超时通知后立即通过 `manage_task`（`status`）查看进度；若确认在沙盒中无进展卡死，立即取消（`kill`），并仅将受沙盒阻塞的浏览器或回环测试步骤在沙盒外单独重试（仍须保持超时配置）。

如何确认：对运行中的任务 ID 调用 `manage_task`（`Action: "status"`），日志会显示前面的单元测试已全部通过，随后无新输出且 `Last progress` 时间持续增长。

已排除，所以不要优先在这些方向上花时间：Go 单元测试死锁（`go test` 在 Python/Playwright 启动前已打印 `ok`），以及应用前端 JavaScript 死循环（同一份 Playwright 脚本在沙盒外约 10 秒即可通过）。
