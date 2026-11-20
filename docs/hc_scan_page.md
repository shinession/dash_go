# English

**Updated:** 2026-11-20 (module version 0.1.4). This describes the current code of `internal/mods/hc_scan` and of the Python side in the `dash` repository. Version 0.1.1 put the page into the shell (header with logo, light / dark theme, English / Chinese, login page): that part is described in [shell_and_login.md](shell_and_login.md). Version 0.1.2 made a wrong or outdated Python API visible in the log (sections 9 and 11). Version 0.1.3 changed nothing in the page; the header got a Back button and the program can update itself ([auto_update.md](auto_update.md)). Version 0.1.4 fixed the board, which showed the people of other processes on its cards, and changed what the page says about the people of the shift: leave is shown, and somebody who already has a task is no longer listed as "not scanned" (sections 2.1 and 11). Section 12 says what was verified and what was not.

## 1. Overview

HC Scan Assign lets a shift leader scan the badges of the employees at the start of a shift. Every scan assigns the person to a machine or activity of the selected process, automatically, from the person's MTR (training record) levels. The result is only a draft. The shift leader checks it, changes what is wrong and presses **Confirm**, which is the only moment anything is written to the database.

The scan is a quick way to assign, not a duty: a person whom the shift leader has already assigned on another page of the Python application (HC Input, Area Assign) needs no scan. The page shows that person on their machine, and scanning them only shows the task (nothing is drafted for the database).

It is the badge driven relative of the HC Allocation > Area Assign page of the Python application: the same tables, the same budget head counts and the same write rules, but one person at a time instead of one batch for a whole area.

Entry point: IE dashboard > MQPD > Head Count > **Scan Assign** (button `mqpd-hc-scan-assign-button` in `IE.py`). The page is served by the Go service at `/dash_go/hc_scan/` on server s2.

Scope:

- In scope: scanning, the automatic assignment, the draft and its editing, Confirm, the login hand-over from Dash, the login page, English and Chinese, light and dark theme.
- Out of scope (0.1.0 and 0.1.1): the HC Allocation matrix (areas and OP positions are not shown, the machines of the process are listed flat), clock attendance (`t_mqpd_hc_attend`), budget editing, any other HC page.

## 2. How it works

1. The leader opens **Scan Assign** in Dash. `mqpd_hc_scan_assign_onclick` checks the login and that the user has at least one HeadCount permission, builds a signed one-time link (`build_dash_go_sso_url`) and navigates the browser to it. (Since 0.1.1 the page can also be opened directly, for example from a bookmark on the scanning computer: without a session it leads to the login page of dash_go, see [shell_and_login.md](shell_and_login.md), section 7.)
2. The Go service verifies the signature, consumes the one-time nonce, sets its own session cookie and redirects to the page.
3. The page asks `api/me` for the processes the user may work on (answered by the Python backend, section 8). The leader **chooses the process first**; scanning is not possible before that. The production date (now minus 7 hours, like `get_shift_today`) and the shift (AM from 07:00 to 19:00, otherwise PM) are preset and can be changed.
4. The scan box always has the focus. A barcode scanner types the employee ID; the page does not depend on the scanner pressing Enter. It looks for an employee ID in what was typed (pattern `employee_id_pattern`) after a 90 ms pause, so prefix or suffix characters of the barcode and lower case are tolerated. A manual entry works with Enter.
5. Every scan is sent to `api/scan`. The answer is shown at once in a large card (name, machine, MTR level, reason, warnings) with a sound: one beep for a clean assignment, two for a yellow one, a low tone for an error.
6. The page shows the draft (the "Scanned" list), the machine cards with used / budget head count, the people on leave, and the people who still have no task ("Not assigned yet"); section 2.1 says what each of them holds. It refreshes every 3 seconds, so several terminals of the same process, date and shift see the same draft, and a task that the shift leader enters on another page appears within seconds.
7. The leader can change the machine of a line with its list, remove a line (the person has no task again, unless the database has one) or clear the draft.
8. **Confirm** writes the lines that have a machine. Lines with a yellow warning are listed first and are only written after the leader acknowledges them ("Submit anyway").

### 2.1 What the board shows

**Which entries count for a process.** `t_daily_attn_data` holds the records of every process, and machine names repeat between processes: `Group Leader` is a row of nine processes, `Training` of eight, and a production line is written `T20` by Winder, `Taping T20` by Taping and `T20 : Can Assy P2(OP1)` by HSL. Every entry of `machine_hc_json` names the process of its machine, so since 0.1.4 a machine card, its used head count (and with it the budget check of the automatic assignment) and the days that rank the machines (section 3) count only the entries whose `process` is the process of the page. CSV and NWC are one: the page of either takes the entries of `CSV`, `NWC` and `CSV & NWC`. An entry without a process of its own (empty, or `All Process`) belongs to the process of the person. Names are compared with every run of spaces counted as one space, because the machine table has `Fix-Shipment/ Logistic  - 4pax` with two spaces where the records have one.

**Support.** Somebody who is not a member of the process but works on one of its machines (for example the "Others" group, or a person the shift leader placed with the yellow `NOT_IN_ROSTER` warning) is on the card of that machine, with the process of the person next to the name (for example `LSL`). The seat counts for the head count and the budget. They are not HC of the shift: the counters below leave them out.

**The people of the shift** are the members of the roster (Direct and Shift Leader of the process on the date) who belong to the shift. A person with a machine belongs to the shift of their record, or of their pending line; everybody else belongs to the shift the calendar gives them. The counters of the page bar are in HC and follow the Daily Attn Summary popup of the Python application:

| Counter | Meaning | Daily Attn Summary |
|---|---|---|
| Scheduled | The people who belong to the shift. | Schedule HC |
| Assigned | HC of them on machines: saved records and pending lines. | Actual HC |
| Leave | HC of them on leave, all kinds together. | MC HC and Other Leave HC |
| To assign | HC of them that still has no task. | To Be Recorded HC |

Pending, Saved, No machine and Warnings describe the draft. When every person has exactly one day of HC, Scheduled is Assigned plus Leave plus To assign. The popup counts a person in the shift of the calendar; the page counts a person who has a task in the shift of their record, because that is where the scan put them. The two differ only for a swap (scheduled AM, recorded PM) and for overtime on a day off (day type `R`, recorded AM), see section 13.

**Lists.**

- **Not assigned yet:** the people who have no task: no machine in the database, no pending line with a machine, and less than a whole day of leave. The HC is shown in brackets when a part-day leave leaves less than a whole day. A person whose pending line has no machine is in it as well, because they still have no task. Somebody who already has a task, entered on another page or saved by this one, is never in it: they need no scan.
- **Leave:** the people on leave, whatever the kind, with the code and the HC of each leave (`MCL`, `ANL 0.5`). A part-day leave also leaves the person on their machine, or in "Not assigned yet", for the rest of the day. A full-day leave that the shift leader overrides with a pending line is not leave any more (Confirm drops it).
- **Elsewhere:** the people of the process whose task is on none of the cards: it is on a machine of another process, or its name is in no machine list of the process (a name that was written wrongly in the record is shown as it is).

## 3. Assignment rules

For one scanned person on the selected process, date and shift:

1. **Who is it.** The employee ID is looked up like the HC Input grid does: the membership range of `t_daily_attn_member` valid on the date overrides the MES snapshot `t_mes_ee` (process from the cost center, direct type, shift code). An ID that is in neither is rejected ("not found"), because no record can be built without a shift code.
2. **Already assigned.** If the person already has a record on that date with a machine, nothing is assigned. The line is shown as "existing" and counts on its machine. The leader can still change it by hand. Such a line has nothing to save, so it does not switch on the Confirm button. When the machine of the record belongs to another process, the result names it and the line is on no card (section 2.1).
3. **HC to place.** 1 minus the HC of leave options in the record. A part-day leave leaves the rest (for example 0.5). A full-day leave is treated as 1 and flagged (section 4).
4. **Leaders.** A leader is placed on the "Group Leader" row of the process (the non-routine row named in `leader_activity_names`), not by MTR level. A person is a leader when the direct type of the date is `Shift Leader` (maintained with HC Input > Tools > Member; this is the target) or, until that is maintained for everybody (`leader_by_history`, on by default), when the most recent record of the person in the last 7 days that has a machine is on that row ("same as last time": a leave day is skipped, and somebody who covered the row once and went back to a machine is not a leader). The budget of the row applies: when it is full the line stays without machine and the shift leader decides. A process without a Group Leader row ranks everybody by MTR. Switch `leader_by_history` off when the direct types are maintained.
5. **Eligible machines.** A machine is eligible only when all of these are true:
   - it is a routine machine (`type` is not `Non-routine`) of the process (CSV and NWC share their machines);
   - it has an MTR mapping: a machine with the same name (trimmed) exists in the LMS records of that process in `t_mtr_checkpoint`;
   - the person holds **MTR level 1 or higher** on it. Without a record the person is never assigned automatically, because the MTR data is used by other systems and an automatic assignment must not hide a missing record;
   - the budget head count left on it (`budget_head_count` minus the HC already on it, in the database and in the draft) is at least the HC to place, so an automatic assignment never creates an over-budget line.
6. **Order.** The machines are ranked by:
   1. MTR level, 3 before 2 before 1;
   2. the number of the last 7 days (`recent_days`) on which the person worked on that machine (records of `t_daily_attn_data`, any writer; entries of this process only, section 2.1); more days first;
   3. the larger remaining budget;
   4. the display order (`display_seq`) and the machine id.

   The first machine is assigned. This is **not** the order of the Python Auto Assign button, which puts the last 7 days before the level and also fills people who have no record.
7. **Nothing eligible.** The line stays without machine ("No machine assigned") with the reason: no MTR record on any machine of the process, no machine has an MTR mapping, or all machines the person is qualified for are full. The shift leader assigns by hand. Activities (non-routine rows such as `Winder_9014`) and machines without MTR mapping are never assigned automatically, but can be chosen by hand; the only automatic use of an activity is the Group Leader row for leaders (item 4).

A person who scans while a seat is free is placed at once, so the result depends on the scan order (the first Lv1 person can take the seat a later Lv3 person would have got). 0.1.0 has no "rebalance" function; the leader corrects by hand.

## 4. Warnings

Anybody can scan, also people who are not on the roster, are on another shift or are on leave. They are assigned like everybody else and marked yellow with the reason, so the shift leader decides:

| Code | Shown when |
|---|---|
| `NOT_IN_ROSTER` | The person is not a Direct or Shift Leader member of the process on the date (other process, other direct type, or resigned before the date). |
| `SHIFT_MISMATCH` | The day type of the calendar (or the recorded actual shift) is not the selected AM/PM shift: scheduled in the other shift, a day off, or no calendar entry for the shift code. |
| `FULL_DAY_LEAVE` | The record of the date is a full-day leave. Saving replaces the leave with the machine. |
| `PART_DAY_LEAVE` (info) | Part of the day is leave; the rest goes to the machine. Not yellow. |
| `HAS_RECORD` | The person already has a record with a machine (yellow only when it is recorded for the other shift). |
| `NO_MACHINE` | No machine could be assigned (section 3, item 7). |
| `NO_MTR` | A machine chosen by hand has no MTR record of the person, or no MTR mapping. |
| `OVER_BUDGET` | A machine chosen by hand goes over its budget head count. When no budget is maintained the message is only informational. |

A line with a yellow flag that has a machine is written only after the acknowledgement. A line without machine is never written; the page says how many were skipped.

## 5. Data

Tables of the `Auth` database (names and columns are the ones the Python code uses; this module never changes a schema):

| Table | Use |
|---|---|
| `t_mqpd_hc_table` | Machines and activities of the process: `id`, `process`, `machine`, `type`, `budget_head_count`, `display_seq`, `hidden`. Read. |
| `t_mtr_checkpoint` | MTR proficiency: latest version of every checkpoint row (`row_id`, `updated_at`), hidden rows removed, newest row per employee, checkpoint and machine, highest level per employee and machine (same reduction as `_sync_mtr_checkpoint` in `LocalListV3.py`). Read. The table is filled by the Python application from LMS. |
| `t_mes_ee` | MES employee snapshot of the month (group `SP9200`). Read. |
| `t_daily_attn_member` | Membership ranges. Read. |
| `t_daily_attn_calendar` | Day type of every shift column for the date. Read. |
| `t_daily_attn_data` | Attendance and machine records. Read for the day and for the previous 7 days; **written** by Confirm. |

Redis (JSON, written and read only by this module): the draft of a session `dash_go:hc_scan:v1:<process>:<yyyymmdd>:<AM|PM>:entries` (pending lines) and `...:scans` (who scanned, also after saving), 48 hour TTL; the one-time login nonces `dash_go:sso:nonce:<nonce>`. Since 0.1.1 a line keeps its warnings (`flags[]`) and its reason (`reason_msg`) as a message, a catalog key and the values of its placeholders (`{"key": "hc.flag.over_budget", "args": {"machine": "...", "used": "2", "budget": "1"}}`), not as an English sentence; the sentence is made when the board is read, in the language of the request. Lines that version 0.1.0 stored (`reason`, `flags[].text`) are still shown, in English.

In memory (60 seconds, 5 minutes for the calendar): machines, MTR matrix, calendar and the employee directory. When the database does not answer, the last cached values are used.

## 6. Confirm: what is written

Confirm writes `t_daily_attn_data` with one upsert per line on `uid = <ee_code>_<yyyymmdd>`, in one transaction, after locking the current rows (`SELECT ... FOR UPDATE`):

- `actual_shift` is the scanned session, `AM` or `PM`, like the HC Input page stores it. (The Python Area Assign page stores the schedule code when the person has no actual shift yet; this module does not copy that behaviour.)
- `machine_hc_json` keeps the leave entries of the current record, followed by `{process, machine, hc}` for the machine with the rest of the day. A confirmed full-day leave exception drops the leave entry. `process` is the process of the page, `machine` the name in `t_mqpd_hc_table`.
- `shift` and `schedule_shift` come from the membership and the calendar of that date; `build_in_ot` is recalculated with the same function as the Python code (`cal_build_in_ot`, checked against its output in a test table), `addt_ot` and `remark` are kept, `total_ot` is their sum.
- `update_user` is the display name of the shift leader (the Python code stores names, not employee codes), `update_time` the server time, `hidden` false.
- A line is skipped, with a reason on the page, when the data changed since the scan: an automatic line whose person now has a machine record, a person who is now on full-day leave without having been flagged, an unknown machine or a missing shift code. Manual lines may replace a newer record because the leader chose them knowingly.
- After the commit a line is appended to `DailyAttnData.log` in `dash_data_dir` and in `cdn_data_folder`. The Python readers compare the modification time of that file with their Redis cache, so this is what makes the Python pages show the saved data. If a folder cannot be written the data is saved anyway and the page shows a warning.
- The previous-month lock of the Python pages applies: from the 8th of a month the previous month cannot be saved (`MonthLocked`, port of `check_mqpd_hc_daily_attn_last_month_locked`).
- The permission of the user is checked again, without the cache, before writing.

After saving, the lines leave the draft and the people count as scanned. A person who scans again is told "already scanned and saved" and keeps the saved record; change it on the HC Input or Area Assign page.

## 7. HTTP API

All paths are below `/dash_go/hc_scan/`. Answers are JSON with `Cache-Control: no-store`. Errors are `{"error": "<message>", "code": "<code>"}`; technical details are only written to the log. Every sentence in an answer (`error`, `message`, `reason`, `flags[].text`, the reasons of skipped lines, `log_warning`) is in the language of the request (cookie `dash_go_lang`, then `Accept-Language`, then English); the `code` values do not change with the language. The page itself (`GET /dash_go/hc_scan/`) is rendered by the shell and sends a visitor without a session to `/dash_go/login?next=...`.

| Request | Purpose |
|---|---|
| `GET api/me` | User, permitted processes, client settings. |
| `GET api/board?process=&date=&shift=` | The whole session: machines, draft lines, the people without a task (`not_assigned`), on leave (`leave`) and elsewhere (`elsewhere`), the counters (`totals`: `scheduled`, `placed_hc`, `leave_hc`, `to_assign_hc`, and for the draft `pending`, `saved`, `assigned`, `unassigned`, `warnings`), month lock, MTR freshness. |
| `POST api/scan` `{process, date, shift, input}` | One scan. Statuses `assigned`, `unassigned`, `existing`, `duplicate`, `saved`. |
| `POST api/assign` `{process, date, shift, ee, machine_id}` | Change the machine of a pending line (empty `machine_id` removes it). |
| `POST api/remove` `{process, date, shift, ee}` | Remove a pending line. |
| `POST api/reset` `{process, date, shift}` | Clear all pending lines. |
| `POST api/confirm` `{process, date, shift, ack}` | Save. Answer 409 with `code` `needs_ack` and the lines with warnings when `ack` is false; 409 `month_locked`; 409 `nothing_to_save`. |

POST requests need the header `X-Requested-With: dash_go` and are refused when the browser marks them cross-site. The date must be between the first day of the previous month and tomorrow's production date.

Version 0.1.4 renamed the list `not_scanned` to `not_assigned` and replaced `totals.expected` and `totals.expected_scanned` by `scheduled`, `placed_hc`, `leave_hc` and `to_assign_hc`; `totals.assigned` now counts only the pending lines that Confirm would write. The page of the module is the only client of this API.

## 8. Login and permissions

- Dash cannot give the Go service its browser login, so the link carries a token signed with `api.salt_3` (HMAC-SHA256, key derived with the label `dash_go/sso/v1`, valid 5 minutes, one use). The Python builder is `build_dash_go_sso_url` in `LocalListV3.py`; the Go verifier is `internal/core/auth`. A test vector fixes the format on both sides.
- The service answers with a session cookie `dash_go_session` (HttpOnly, SameSite Lax, `Secure` behind https, `session_hours`, default 12). The link is only accepted for a local `next` path below `/dash_go/`.
- Permissions are not copied to Go. The service asks the Python backend: `POST /api/user/hc_scan` with `{"func": "hc_user_processes", "user": "<ee_code>"}` and the header `Authorization` = md5 of `<salt_3>_<yymmddHH>` (the rule of the existing `/api/user` endpoint). The backend (`api_user_mgmt` in `appV2.py`) answers the display name and the processes returned by `check_daily_attn_permission` (HeadCount groups, `HeadCount-All`, `MQPD_AUTH_LIST`). The answer is cached for 60 seconds, except before saving.
- Without a valid session every API call answers 401. The page then shows "Session expired" with a button to the login page (until 0.1.0 it told the user to open the page again from Dash). The password and the Windows sign-in of 0.1.1 give the same session cookie; see [shell_and_login.md](shell_and_login.md).

## 9. Configuration

Two layers of JSON files are read, with the patch convention of the Python application (`patch_<hostname>.json` replaces whole top-level keys of the base file):

1. The config folder of the Python application (`config.json` + `patch_<hostname>.json`), given by `DASH_GO_DASH_CONFIG_DIR` or `dash_config_dir`. Used keys: `postgresql`, `redis`, `api.salt_3`, `api_server`, `cdn_data_folder`, `HC_PROCESS_LIST`, `SHIFT_LEAVE_PATTERN` (the leave options are its keys from the third on, in file order).
2. `dash_go.json` + `dash_go_patch_<hostname>.json` in `DASH_GO_CONFIG_DIR` (default `./config`). It may override any key of layer 1. Example: `config/dash_go.example.json`.

Service keys (top level of `dash_go.json`): `listen` (default `:8750`), `base_path` (`/dash_go`), `log_dir` (`logs`), `dash_home` (`/ie/`), `session_hours` (12), `dash_config_dir`, and since 0.1.1 `photo_url_template`, `dash_api_url`, `login_password`, `sso_require_same_ip`, `trusted_proxies` (see [shell_and_login.md](shell_and_login.md), section 8; the Windows sign-in also needs `sso_server` and `api.salt_2` in the Python configuration). Since 0.1.3 also `update_url`, `update_interval_minutes` and `update_restart` (the automatic update is off unless `update_url` is set, see [auto_update.md](auto_update.md), section 6).

Which Python API is asked: `dash_api_url` if it is set (here as `modules.hc_scan.dash_api_url`, or at the top level), otherwise `http://<api_server>/api`. `api_server` comes from the Python configuration **including the patch file of the host**, and the patch file of a development machine often sets it to the production server for the calls of the Python application itself. dash_go would then ask a backend that does not have `hc_user_processes` until the Python changes are deployed there (section 11). Since 0.1.2 the log, and so `dash_go.exe -check`, says which API is used and which key it came from: `hc_scan: the Dash API of the permission check url=http://localhost/api from=dash_api_url`. On a development machine set `dash_api_url` in `dash_go.json`, for example to `http://localhost/api`.

Module keys (`modules.hc_scan`):

| Key | Default | Meaning |
|---|---|---|
| `employee_id_pattern` | `[A-Z]{2,3}\d{2}[A-Z0-9]\d{2}` | What an employee ID looks like inside the scanner input (the format the Python code validates for new members). |
| `draft_store` | `redis` | `redis`, or `memory` (lost at a restart, only for development). |
| `draft_ttl_hours` | 48 | Lifetime of a draft in Redis. |
| `recent_days` | 7 | Days of history that rank machines and that are searched for the last record of a leader. |
| `leader_activity_names` | `["Group Leader"]` | Names of the non-routine rows leaders are placed on (case and spaces are ignored). |
| `leader_by_history` | `true` | Recognise leaders by their most recent record until their direct type is `Shift Leader`. Set `false` once the direct types are maintained. |
| `photo_url_template` | the service setting of the same name (`/api/static/hr/{ee_code}.webp`) | Photo of the employee; initials are shown when it is missing. Set it here only to override the service setting. |
| `dash_api_url` | the service setting of the same name (`http://<api_server>/api`) | Root of the Python API. Set it here only to override the service setting. |
| `dash_data_dir` | none | Data folder of the Python application that holds `DailyAttnData.log`. Set it on every host where Python pages cache the data. |
| `production_day_offset_hours` | 7 | The production day starts at 07:00. |
| `am_from_hour`, `pm_from_hour` | 7, 19 | Default shift by the clock. |
| `poll_ms` | 3000 | Refresh rate of the page. |

Environment variables: `DASH_GO_CONFIG_DIR`, `DASH_GO_DASH_CONFIG_DIR`, `DASH_GO_LISTEN`, and for secrets `DASH_GO_PG_PASSWORD`, `DASH_GO_REDIS_PASSWORD`, `DASH_GO_API_SALT`, `DASH_GO_API_SALT_2`, which win over the files. Secrets are never logged.

Python side: the optional key `dash_go_url` of `config.json` (default `/dash_go/`; an absolute URL when the service runs on another host than the Dash application).

## 10. Deployment (server s2)

1. Build the package: `scripts\build.ps1 -Modules hc_scan` (or without `-Modules` for every module) and unpack it on s2.
2. Create `config\dash_go.json` from the example. Point `dash_config_dir` to the config folder of the Python application on s2 and `modules.hc_scan.dash_data_dir` to its `data` folder. Check the connection settings with `dash_go.exe -check`, which also says which Python API is used.
3. nginx. Add the location of `deploy\nginx_dash_go.conf.example` to the nginx site of s2 and reload nginx; the service serves everything below `/dash_go/` itself. s1 (`prod_eng`, the front door) only forwards `/dash_go/` to s2: it needs the upstream and the location of the second part of the example, with s2 as its only server (no local backup, because dash_go runs on s2 only). The nginx of s2 must trust `X-Forwarded-For` from s1, and its `/dash_go/` location must send `X-Forwarded-For $proxy_add_x_forwarded_for`, not `$http_x_forwarded_for` ([shell_and_login.md](shell_and_login.md), section 9). Both files are in the Dash repository: `config/nginx_sgmescimai.conf` (s2) and `config/nginx_prod_eng.conf` (s1).
4. Start `dash_go.exe` (a console process; wrap it with a service manager if it has to survive log-offs). The log is `logs\dash_go-<date>.log`.
5. Deploy the Python changes (`IE.py`, `LocalListV3.py`, `appV2.py`). The new menu button works as soon as the Dash application is restarted; the permission function must be deployed to the server that answers `/api`.

Order: dash_go and the Python function on s2 first, then the nginx of s2, the nginx of s1 last. A route whose backend is not running answers 502. Test `http://<s2>/dash_go/` before the front server is used. The copy `config/nginx_prod_eng.conf` also carries the prepared, not deployed step 1 of the API migration (`/api` to s2, `migration_step1_api_to_s2.md` in the Dash repository): compare it with the live file before replacing it, or add only the `dash_go_backend` upstream and the two `/dash_go` locations.

Local debugging on a development machine: the Dash repository has the same location in the local debug server block of `config/nginx_asrock.conf` (`http://localhost/dash_go/` to `127.0.0.1:8750`). Copy it to the nginx that is running, reload nginx and start `dash_go.exe` with `DASH_GO_DASH_CONFIG_DIR` pointing at the config folder of the Dash repository. Set `dash_api_url` to the local API (`http://localhost/api`) when the patch file of the machine points `api_server` at another server (section 9).

Run one instance only: the lock that keeps two terminals from taking the last seat twice is in the process.

## 11. Operations and troubleshooting

- **A machine never gets anybody automatically.** Open the page: a machine card with "no MTR mapping" has no machine of the same name in the LMS records of that process. Fix the name in `t_mqpd_hc_table` or in LMS; the match is exact (trimmed).
- **MTR changes are not seen.** `MTR data:` on the page is the newest `updated_at` of `t_mtr_checkpoint`. The Python application refreshes the table from LMS when its pages read the MTR data, so it can be stale when nobody opened them. Cached for 60 seconds.
- **Saved data is not shown on the Python pages.** The save answered "The data is saved, but the Python pages may show it late": the change log folder cannot be written (`dash_data_dir`, `cdn_data_folder`). Fix the permission; touching `DailyAttnData.log` refreshes the pages.
- **"Cannot start: Your permission could not be verified right now" (`api/me` answers 502).** The log line `permission check failed` has the reason. Found on 2026-11-20 on a development machine:
  - *Symptoms:* the page cannot start for anybody. The log says `permission check failed ... answered status 204` (in 0.1.0 and 0.1.1 only `dash api answered status 204`, without the address). The API log of the machine never shows `user_mgmt: hc_user_processes`.
  - *Root cause:* dash_go asks `dash_api_url`, by default `http://<api_server>/api`, and the patch file of the machine (`patch_sgmeslt595.json`) sets `api_server` to `prod_eng`. According to `nginx_prod_eng.conf`, `prod_eng` forwards `/api` to s2. The API reached that way answered 204 to this call, and `/api/user` answers 204 to a function it does not have, so the Python changes (`hc_user_processes`) are not deployed behind `prod_eng` yet. The signed call was right; it went to another server than the one with the new function.
  - *Rule:* where `api_server` is not the server that has `hc_user_processes`, set `dash_api_url` in `dash_go.json` (development machine: `http://localhost/api`). Deploy `appV2.py` to a server before dash_go is pointed at its API.
  - *How to confirm:* `dash_go.exe -check`, or the first lines of the log, say `the Dash API of the permission check url=... from=...`; then look for `hc_user_processes` in the API log of the machine at that address. The same signed call answered 200 with the processes from `http://localhost/api` and 204 from `http://prod_eng.co.murata.local/api`.
  - *Ruled out:* the signature (`api.salt_3` and the hour were right, the local API accepted the same call), the permissions of the user, and nginx on the development machine.
  - *Other causes of the same message:* `dash api rejected the request signature` (status 401): `api.salt_3` differs between the two sides, or the clocks and time zones of both hosts differ (the signature contains the hour). `dash api request failed`: the API cannot be reached from this computer (address, firewall, nginx).
- **"The login link from Dash has expired / was already used".** The login page of dash_go shows this since 0.1.1 (before, a plain error page): sign in there, or open **Scan Assign** from Dash again.
- **"No permission" after signing in.** The account is valid, but the Python backend lists no HC process for it (HeadCount groups, `HeadCount-All`, `MQPD_AUTH_LIST`): ask the HeadCount group admin for access. In 0.1.0 this case showed a blank page because the list of processes was `null`; 0.1.1 sends an empty list and the page says so (found by the browser test of 0.1.1).
- **A person is flagged yellow although everything looks right.** The reason is written on the line. Roster and shift follow `t_daily_attn_member`, `t_mes_ee` and `t_daily_attn_calendar`, exactly like the HC Input page.
- **A card shows people who are not on that machine, or its budget is used up by people who are not there.** Found on 2026-11-20 on the Slitter page; it was in every version from 0.1.0 to 0.1.3 and is fixed in 0.1.4.
  - *Symptoms:* the board of a process shows people of other processes on its cards. The Group Leader row of Slitter had 10 people where the Daily Attn Summary popup counts 1. On the development copy, the cards of Slitter AM of 2026-11-17 held 25 people, 17 of them from other processes (Group Leader 7, Others 6, Training 3, QC 1), and on HSL PM all 31 were. The used head counts are too high, rows look full or over budget, and the automatic assignment says that all machines are full.
  - *Root cause:* `DayRecords` returns the records of every process of the day, and the cards matched an entry to a machine by name only (`machineIndex.Resolve`, a port of `resolve_mqpd_hc_machine_id`: exact id, exact name, then the loose rule "the machine name contains the recorded name"). Machine names repeat between processes: 19 names are in the rows of more than one process of `t_mqpd_hc_table` (`Group Leader` in 9, `Training` in 8, then `Others`, `QC`, `OJT`, `Maintenance`, ...). Winder records its lines as `T20`, which `Taping T20` and `T20 : Can Assy P2(OP1)` contain. The use of the machines (`usage`), which the budget check of the automatic assignment reads, matched the same way.
  - *Rule:* first decide whether an entry is on this process (`dayData.entryHere`: the `process` of the entry; an entry without one belongs to the process of the person), then look at the name. Never match a name against the machines of a process before that. Whatever else reads `t_daily_attn_data` for one process must do the same.
  - *How to confirm:* `TestDevCopyBoardAgreesWithTheDailyAttnSummary` (section 12) on a copy of the database. With the matching of 0.1.3 it counted 458 people of other processes on the cards of one day; with the matching of 0.1.4 it counts none.
  - *Ruled out:* the budgets in `t_mqpd_hc_table`, the MTR data, and the date or the shift of the records (the records shown were of the right day and shift).
- **People of the process are on no card although they have a task.** Found on 2026-11-20 with the same copy. The machine table has names with two spaces (`CSV-10/13 Set2  (SV#9/10|SD#12/13/15|TCW#12/13/14/18)`, `Fix-Shipment/ Logistic  - 4pax ( AM Shift only)`, `Fix-Tapyrus preparation  2 pax (HSL) - AM Shift (7 days)`) where the records have one, so the names did not match. Since 0.1.4 names are compared with every run of spaces counted as one (`normName`). What still does not match is shown in the "Elsewhere" card with the name of the record: on the copy it is `Rework - Sorting (Cell)` of Packing, whose records before 2026-11-12 hold the text `â€“` in place of the dash (an encoding error of whatever wrote them; the machine row was changed on 2026-11-12). Correct the name in the machine table or in the record, not in this module.
- **Where to look.** `logs\dash_go-<date>.log` (requests without query strings, failures with the step that failed). The log lines in `DailyAttnData.log` are marked `dash_go hc_scan`.
- **Resetting a session.** Delete the Redis keys `dash_go:hc_scan:v1:<process>:<yyyymmdd>:<shift>:*`, or press "Clear draft". Get approval before touching a production Redis.

## 12. Verification status

Done (2026-11-19):

- Go unit tests: assignment order and gates, every warning, leave handling, Confirm rules, month lock, HTTP flow with login guard, Redis draft store (miniredis), tokens (including the value produced by the Python code), configuration layers.
- The SQL of the repository and of the write ran against a throw-away PostgreSQL 18 with the real column types of the six tables (`internal/mods/hc_scan/testdata/schema.sql`, read from a development copy), in `repo_pg_test.go`. The write path ran only there, never on the real tables.
- The read side and the scan logic ran on a copy of the real database (`TestDevCopyReadOnly`, read-only session, drafts in memory, Confirm never called): all 10 processes, 150 scans, no SQL error with the real column types, no automatic line without MTR level 1 or a machine, no automatic line over a budget. Leaders were placed on the Group Leader row in 8 of the 10 processes (none among the first 15 people of CDC and QA). The first board takes about 70 to 180 ms and one scan about 150 ms. What it found about the data: QA has no MTR records at all (every QA scan ends as "No machine assigned") and many routine machines have no MTR mapping (for example 4 of 14 Slitter, 19 of 28 LSL, 9 of 15 Packing machines), so they are never assigned automatically.
- The real binary was run with a Python configuration folder, a fake `/api/user` endpoint and the scratch database: login with a link made by the real `build_dash_go_sso_url`, the process list, scans, Confirm, rows in `t_daily_attn_data`, change log lines in both folders, a replayed link refused, the page without login.
- The page was driven in headless Chrome against that database (scan with and without Enter, barcode wrapper, duplicate, unknown ID, every warning, manual change, removal, acknowledgement dialog, saving, scan after saving). No JavaScript error was reported.
- The Python additions have unit tests (`tests/test_dash_go_link.py`); `tests/test_callback_registration.py` still passes.
- 0.1.1 (2026-11-20): the module was moved into the shell and every text into the catalogs. The old tests still pass; new tests cover the catalogs (`catalog_test.go`: both languages complete, every key and value the code uses, no unused text) and the languages (`language_test.go`: page, API errors, a draft read in both languages, drafts of 0.1.0). The real binary was run again with the scratch database and a stand-in for the Python backend, and the page was driven in headless Chrome in both themes and both languages: details in [shell_and_login.md](shell_and_login.md), section 12.
- 0.1.2 (2026-11-20): the cause of the failure in section 11 was found with the real binary on the development machine (scratch configuration, the real local Python API and Redis): with the API taken from the patch file (`api_server` = `prod_eng`) `api/me` answered 502 and the log named the API and status 204; with `dash_api_url` set to `http://localhost/api` it answered 200 with the 10 processes of the account. New unit tests: the status 204 and the address in the error messages (`internal/core/dashapi/client_test.go`), where the API address comes from (`internal/core/config/config_test.go`). The Windows sign-in checks of 0.1.2, and the test of the nginx chain s1 → s2 → dash_go, are in [shell_and_login.md](shell_and_login.md), section 12. The page itself was not driven in a browser again.
- 0.1.3 (2026-11-20): the page itself is unchanged; the module version follows the version of the program. The change of the header (a more visible module name and the Back button) is in [shell_and_login.md](shell_and_login.md), sections 2 and 12; the automatic update is in [auto_update.md](auto_update.md). An update restarts the service: the drafts are in Redis (the default of `draft_store`) and survive, with `draft_store` `memory` they are lost. An update is only installed when nobody is clicking or submitting, so a scan that is going on is not cut off. The page was not driven again with real data under the new header (only the header of the module page and the "Cannot start" notice were looked at).
- 0.1.4 (2026-11-20): the board and the counters changed (section 2.1) and were tested:
  - Unit tests (`board_scope_test.go`): a machine with the name of another process; a short name that a machine of another process contains; support from another process (shown with its process, takes the seat for the budget); entries of `All Process` and without a process; a member whose task is on another process (Elsewhere); leave of a whole day and of part of a day; the counters adding up (Scheduled = Assigned + Leave + To assign); somebody already assigned (never "not assigned"; scanning shows the task and leaves the Confirm button off); a pending line replacing a saved record; a person belonging to the shift of their record; the days that rank the machines; runs of spaces in a machine name; the JSON of the board; and that the script reads only keys the board has. With the matching of 0.1.3 put back (`entryHere` answering true), four of them fail.
  - On the development copy of the real database (read-only session, `TestDevCopyBoardAgreesWithTheDailyAttnSummary`): 13 days from 2026-11-02 to 2026-11-18, 20 boards each (10 processes, AM and PM), 260 boards. On none of them is there a person on a card who is not a member of the process, unless an entry of their record names the process (a supporter), and the counters equal the logic of the Daily Attn Summary (`cal_daily_summary`) written again from the raw rows: exactly on every board without swapped or overtime people, and where there are such people the Scheduled count differs by exactly their number: minus those scheduled for the shift but recorded in the other one, plus those recorded in the shift but not scheduled for it (section 13). The comparison is with that logic, not with the popup of a running Python application.
  - The page was driven in headless Chrome against the same copy, through a temporary copy of the service that was not kept (the real handlers and page, drafts in memory, a read-only database session): the boards of Slitter, HSL, CSV and Packing, in English and Chinese, in the light and the dark theme; the Leave card, the "Elsewhere" card, the process tag of a supporter and the people of "Not assigned yet"; a scan of somebody without a task (Assigned 0 to 1, To assign 22 to 21, Pending 1, Confirm on) and of somebody who was already assigned (counters unchanged, the task shown, Confirm stays off). No JavaScript error.
  - The PostgreSQL tests (`repo_pg_test.go`) ran again on a throw-away PostgreSQL 18; `go vet ./...` and `go test ./...` pass.

**Not verified:**

- Writing to the real tables (only a scratch database with the real column types was written), and the production database.
- Real photos, a real barcode scanner (whether it presses Enter, which characters it adds), and a real Redis server (the Redis store was tested with an in-process Redis).
- The call `hc_user_processes` against the Python backend on s2 itself (in 0.1.2 it was tried against the backend of the development machine, and through `prod_eng`, which answered 204: the function is not deployed behind it), and the Dash button in a running Dash application (only the callback function, the layout text and the Dash `Location.href` mechanism were tested separately).
- The nginx of s1 and s2 as deployed (their files were tested with isolated instances, [shell_and_login.md](shell_and_login.md), section 12) and the port on s2.
- The Chinese wording has not been reviewed by the users; the pictures of the people (the page was only tried without pictures).
- 0.1.4: the real binary and the real database. The page was driven through a temporary service on the copy. The day of the screenshot that led to 0.1.4 (2026-11-20) is not in the copy (5 records), so the defect was reproduced on 2026-11-17, which has the same pattern. Confirm itself did not change in 0.1.4 (only the condition that switches its button on); it was not run against a real database again. The Chinese texts of the new cards and counters have not been reviewed by the users.

## 13. Decisions and known limits

- The assignment order (level first, the last 7 days second) follows the shift leader's decision that missing MTR records must never be hidden by the automatic assignment. It is isolated in `RankMachines` (`assign.go`).
- Automatic assignment never exceeds the budget; manual choices may, with a warning.
- A full-day leave that the leader confirms is replaced by the machine, and the replaced leave is not kept anywhere except the database history of the Python side. Remove the line if the leave is right.
- Leaders (direct type `Shift Leader`, or for now the last record on the Group Leader row) only go to the Group Leader row, also when they have MTR records, and stay without machine when the row is full. The history rule is a stop-gap: switch it off with `leader_by_history` when the direct types are maintained.
- One machine per person. A person with a part-day leave gets the remaining HC on one machine.
- Clock attendance is not used, and the HC Allocation areas are not shown.
- The order of the scans influences who gets which seat (greedy, no rebalance).
- One service instance, one shared draft per process, date and shift.
- Texts live in the catalogs (`internal/mods/hc_scan/i18n/{en,zh}.json`); a draft stores messages, not sentences, so it reads in the language of whoever looks at it.
- A person with a task belongs to the shift of their record; the Daily Attn Summary of the Python application counts a person in the shift of the calendar. They are the same except for a swap (scheduled AM, recorded PM) and for overtime on a day off (day type `R`, recorded AM): the popup counts the swapped person in AM and the overtime in the actual shift as OT HC, the page counts both in the shift where they work, which is where the scan put them. The Scheduled of the page therefore also counts people who are not scheduled: on the development copy of 2026-11-17 it was higher by 1 to 5 on six of the 20 boards.
- The roster has the Direct and the Shift Leader members; the popup counts Direct only. A process that has Shift Leader members shows more people than the popup.
- A part-day leave without a task leaves the rest of the day in "Not assigned yet" and in To assign; the popup does not call such a person "to be recorded".
- Support from another process is shown on the card and takes the seat, but it is not in the counters.
- A task whose name is in no machine list (or which belongs to another process) is only shown in the "Elsewhere" card: its head count is in no used / budget number.

---

# 中文

**更新日期：** 2026-11-20（模块版本 0.1.4）。本文描述 `internal/mods/hc_scan` 以及 `dash` 仓库中 Python 一侧的当前代码。0.1.1 版把页面放进了 shell（带 logo 的顶栏、浅色 / 深色主题、中英文、登录页）：这一部分见 [shell_and_login.md](shell_and_login.md)。0.1.2 版让错误的或过旧的 Python API 在日志中变得可见（第 9 节和第 11 节）。0.1.3 版没有改动页面；顶栏多了返回按钮，程序可以自己更新（[auto_update.md](auto_update.md)）。0.1.4 版修复了看板（它的卡片上出现了其他 process 的人），并改变了页面对本班人员的说明：显示请假，已经有任务的人不再被列为“未扫描”（第 2.1 节和第 11 节）。第 12 节说明哪些内容已验证、哪些没有验证。

## 1. 概述

HC Scan Assign 让领班在班次开始时扫描员工工牌。每次扫描都会根据此人的 MTR（培训记录）等级，自动把他分派到所选 process 的某个 machine 或 activity。结果只是草稿，领班检查并修改后按 **Confirm**，这是唯一会写数据库的时刻。

扫描只是快速分派的一种方式，并不是义务：领班已经在 Python 应用的其他页面（HC Input、Area Assign）分派过的人不需要扫描。页面会把这个人显示在他的 machine 上，扫描他只会显示这个任务（不会为数据库生成任何草稿）。

它是 Python 应用中 HC Allocation > Area Assign 页面的“工牌驱动版”：使用相同的表、相同的 budget head count 和相同的写入规则，但一次处理一个人，而不是对整个 area 批量处理。

入口：IE 看板 > MQPD > Head Count > **Scan Assign**（`IE.py` 中的按钮 `mqpd-hc-scan-assign-button`）。页面由 s2 服务器上的 Go 服务提供，地址为 `/dash_go/hc_scan/`。

范围：

- 包含：扫描、自动分派、草稿及其编辑、Confirm、从 Dash 跳转时的登录交接、登录页、中英文、浅色和深色主题。
- 不包含（0.1.0 和 0.1.1）：HC Allocation 矩阵（不显示 area 和 OP 位置，process 的 machine 平铺列出）、打卡考勤（`t_mqpd_hc_attend`）、预算编辑以及其他 HC 页面。

## 2. 工作流程

1. 领班在 Dash 中点击 **Scan Assign**。`mqpd_hc_scan_assign_onclick` 检查登录状态和用户至少拥有一个 HeadCount 权限，生成带签名的一次性链接（`build_dash_go_sso_url`），并让浏览器跳转过去。（自 0.1.1 起也可以直接打开页面，例如扫描用电脑上的书签：没有会话时会跳到 dash_go 的登录页，见 [shell_and_login.md](shell_and_login.md) 第 7 节。）
2. Go 服务验证签名，消耗一次性 nonce，设置自己的 session cookie，并重定向到页面。
3. 页面通过 `api/me` 获取用户可操作的 process（由 Python 后端回答，见第 8 节）。领班**必须先选择 process**，之前无法扫描。生产日期（当前时间减 7 小时，与 `get_shift_today` 相同）和班次（07:00 到 19:00 为 AM，其余为 PM）有默认值，可以修改。
4. 扫描框始终保持焦点。扫码枪输入员工 ID；页面不依赖扫码枪是否发送 Enter。输入停顿 90 毫秒后，页面在输入内容中查找员工 ID（规则 `employee_id_pattern`），所以条码的前后缀字符和小写都可以容忍。手动输入按 Enter。
5. 每次扫描都发送到 `api/scan`。结果立即以大卡片显示（姓名、machine、MTR 等级、原因、警告）并伴有声音：正常分派一声，黄色警告两声，错误一个低音。
6. 页面显示草稿（“Scanned”列表）、各 machine 卡片上的已用/预算 head count、请假的人，以及仍然没有任务的人（“Not assigned yet”）；它们各自包含什么见第 2.1 节。每 3 秒刷新一次，所以同一 process、日期、班次的多个终端看到同一份草稿，领班在其他页面录入的任务也会在几秒内出现。
7. 领班可以用下拉列表修改某一行的 machine，删除某一行（此人重新变为没有任务，数据库里已有任务的除外），或清空草稿。
8. **Confirm** 写入有 machine 的行。带黄色警告的行会先列出来，领班确认后（“Submit anyway”）才会写入。

### 2.1 看板显示什么

**哪些 entry 算在某个 process 上。** `t_daily_attn_data` 存放所有 process 的记录，而 machine 名称在不同 process 之间会重复：`Group Leader` 是九个 process 都有的行，`Training` 有八个，一条生产线在 Winder 写作 `T20`，在 Taping 写作 `Taping T20`，在 HSL 写作 `T20 : Can Assy P2(OP1)`。`machine_hc_json` 的每个 entry 都写明它的 machine 属于哪个 process，所以自 0.1.4 起，machine 卡片、它的已用 head count（自动分派的预算检查也用它），以及用于 machine 排序的天数（第 3 节），都只计算 `process` 是页面所属 process 的 entry。CSV 和 NWC 算作一个：任何一个的页面都接受 `CSV`、`NWC` 和 `CSV & NWC` 的 entry。没有自己 process 的 entry（空，或 `All Process`）归此人所在的 process。比较名称时，连续的空格按一个空格计算，因为 machine 表里的 `Fix-Shipment/ Logistic  - 4pax` 有两个空格，而记录里只有一个。

**支援。** 不是本 process 的成员、却在本 process 的某台 machine 上工作的人（例如 “Others” 组，或者领班带着黄色 `NOT_IN_ROSTER` 警告安排的人），会显示在那台 machine 的卡片上，姓名旁边写着此人所在的 process（例如 `LSL`）。这个位置计入 head count 和预算。他们不是本班的 HC：下面的计数不包括他们。

**本班的人** 是名册（该日期该 process 的 Direct 和 Shift Leader 成员）中属于本班的人。有 machine 的人属于他的记录所在的班次，或他的待确认行所在的班次；其他人属于日历给他的班次。页面栏的计数以 HC 计，对应 Python 应用的 Daily Attn Summary 弹窗：

| 计数 | 含义 | Daily Attn Summary |
|---|---|---|
| Scheduled | 属于本班的人。 | Schedule HC |
| Assigned | 其中在 machine 上的 HC：已保存的记录和待确认行。 | Actual HC |
| Leave | 其中请假的 HC，所有假别合在一起。 | MC HC 和 Other Leave HC |
| To assign | 其中仍然没有任务的 HC。 | To Be Recorded HC |

Pending、Saved、No machine 和 Warnings 描述的是草稿。每个人恰好有一天的 HC 时，Scheduled 等于 Assigned 加 Leave 加 To assign。弹窗把一个人算在日历所给的班次；页面把有任务的人算在他记录所在的班次，因为扫描就是把他放在那里。两者只在换班（排 AM、记录为 PM）和休息日加班（日类型 `R`，记录为 AM）时不同，见第 13 节。

**列表。**

- **Not assigned yet（尚未分派）：** 没有任务的人：数据库里没有 machine，没有带 machine 的待确认行，请假不足一整天。半天假留下不足一天时，括号里会显示 HC。待确认行没有 machine 的人也在其中，因为他仍然没有任务。已经有任务的人（在其他页面录入的，或由本页保存的）永远不在其中：他们不需要扫描。
- **Leave（请假）：** 请假的人，不分假别，显示每种假的代码和 HC（`MCL`、`ANL 0.5`）。半天假会让此人剩余的时间仍然在他的 machine 上，或在 “Not assigned yet” 中。被领班用待确认行覆盖的全天假不再算请假（Confirm 会去掉它）。
- **Elsewhere（其他去向）：** 本 process 中任务不在任何卡片上的人：任务在其他 process 的 machine 上，或者名称不在该 process 的任何 machine 列表中（记录里写错的名称会原样显示）。

## 3. 分派规则

对所选 process、日期和班次下扫描的一个人：

1. **他是谁。** 员工 ID 的查找方式与 HC Input 网格相同：日期当天有效的 `t_daily_attn_member` 归属时间段覆盖 MES 快照 `t_mes_ee`（process 由 cost center 得出，还有 direct type 和班次代码）。两处都没有的 ID 会被拒绝（“not found”），因为没有班次代码就无法生成记录。
2. **已经分派。** 如果此人当天已经有带 machine 的记录，就不再分派。该行显示为 “existing”，并计入其 machine 的占用。领班仍可手动修改。这样的行没有东西可保存，所以不会让 Confirm 按钮亮起来。如果记录里的 machine 属于其他 process，结果里会写出它的名称，该行不在任何卡片上（第 2.1 节）。
3. **要安排的 HC。** 1 减去记录中请假选项的 HC。半天假会留下剩余部分（例如 0.5）。全天假按 1 处理并标记警告（第 4 节）。
4. **Leader。** leader 被安排到该 process 的 “Group Leader” 行（`leader_activity_names` 中指定名称的非 routine 行），不按 MTR 等级。满足以下任一条件的人是 leader：当天的 direct type 是 `Shift Leader`（用 HC Input > Tools > Member 维护，这是目标做法）；或者在所有人的 direct type 都维护好之前（`leader_by_history`，默认开启），此人最近 7 天内最近一条带 machine 的记录就在该行上（“和上次一样”：请假日会被跳过，只顶替过一次该行又回到 machine 的人不算 leader）。该行的预算同样适用：满了的话该行保持无 machine，由领班决定。没有 Group Leader 行的 process，所有人都按 MTR 排序。direct type 维护好之后请把 `leader_by_history` 关掉。
5. **可选 machine。** 只有同时满足以下条件的 machine 才可选：
   - 它是该 process 的 routine machine（`type` 不是 `Non-routine`）（CSV 和 NWC 共用 machine）；
   - 它有 MTR 对应关系：该 process 在 `t_mtr_checkpoint` 的 LMS 记录中有同名（去掉首尾空格）的 machine；
   - 此人在该 machine 上持有 **MTR 1 级或以上**。没有记录的人永远不会被自动分派，因为 MTR 数据被其他系统使用，自动分派不能掩盖缺失的记录；
   - 该 machine 剩余的 budget head count（`budget_head_count` 减去数据库和草稿中已占用的 HC）不少于要安排的 HC，因此自动分派不会产生超编的行。
6. **排序。** 按以下顺序对 machine 排序：
   1. MTR 等级，3 级优先于 2 级优先于 1 级；
   2. 最近 7 天中此人在该 machine 工作的天数（`recent_days`，来自 `t_daily_attn_data`，不论由谁写入；只算本 process 的 entry，见第 2.1 节）；天数多的优先；
   3. 剩余预算较大的优先；
   4. 显示顺序（`display_seq`）和 machine id。

   取排在第一位的 machine。这**不是** Python Auto Assign 按钮的顺序：它把最近 7 天放在等级之前，并且也会补入没有记录的人。
7. **没有可选的。** 该行保持无 machine（“No machine assigned”）并给出原因：该 process 的任何 machine 上都没有 MTR 记录、没有任何 machine 有 MTR 对应关系，或者此人有资格的 machine 都已满。由领班手动分派。activity（非 routine 的行，例如 `Winder_9014`）和没有 MTR 对应关系的 machine 永远不会自动分派，但可以手动选择；唯一自动使用 activity 的情况是 leader 的 Group Leader 行（第 4 条）。

有空位时扫描的人会立即被安排，所以结果取决于扫描顺序（第一个 Lv1 的人可能占掉后来的 Lv3 本该得到的位置）。0.1.0 没有“重新平衡”功能，由领班手动纠正。

## 4. 警告

任何人都可以扫描，包括不在名单中、在其他班次或在请假的人。他们和其他人一样被分派，并用黄色标出原因，由领班决定：

| 代码 | 出现条件 |
|---|---|
| `NOT_IN_ROSTER` | 此人当天不是该 process 的 Direct 或 Shift Leader 成员（其他 process、其他 direct type，或在该日期之前已离职）。 |
| `SHIFT_MISMATCH` | 日历的 day type（或已记录的 actual shift）不是所选的 AM/PM 班次：排在另一个班次、休息日，或日历中没有该班次代码的条目。 |
| `FULL_DAY_LEAVE` | 当天记录是全天请假。保存时会用 machine 替换请假。 |
| `PART_DAY_LEAVE`（提示） | 一天中有一部分是请假，其余分给 machine。不是黄色。 |
| `HAS_RECORD` | 此人已经有带 machine 的记录（仅当记录在另一个班次时才是黄色）。 |
| `NO_MACHINE` | 没有 machine 可分派（第 3 节第 7 条）。 |
| `NO_MTR` | 手动选择的 machine 上此人没有 MTR 记录，或该 machine 没有 MTR 对应关系。 |
| `OVER_BUDGET` | 手动选择的 machine 超出其 budget head count。没有维护预算时只是提示信息。 |

带黄色标记且有 machine 的行，只有在确认后才会写入。没有 machine 的行永远不会写入，页面会说明跳过了多少行。

## 5. 数据

`Auth` 数据库中的表（名称和列与 Python 代码使用的一致；本模块不会修改任何表结构）：

| 表 | 用途 |
|---|---|
| `t_mqpd_hc_table` | process 的 machine 和 activity：`id`、`process`、`machine`、`type`、`budget_head_count`、`display_seq`、`hidden`。读取。 |
| `t_mtr_checkpoint` | MTR 熟练度：每个 checkpoint 行的最新版本（`row_id`、`updated_at`），去掉隐藏行，每个员工、checkpoint 和 machine 取最新一行，每个员工和 machine 取最高等级（与 `LocalListV3.py` 中 `_sync_mtr_checkpoint` 的归并方式相同）。读取。该表由 Python 应用从 LMS 填充。 |
| `t_mes_ee` | 当月 MES 员工快照（group `SP9200`）。读取。 |
| `t_daily_attn_member` | 归属时间段。读取。 |
| `t_daily_attn_calendar` | 当天各班次列的 day type。读取。 |
| `t_daily_attn_data` | 考勤和 machine 记录。读取当天和前 7 天；由 Confirm **写入**。 |

Redis（JSON，仅由本模块写入和读取）：会话草稿 `dash_go:hc_scan:v1:<process>:<yyyymmdd>:<AM|PM>:entries`（待保存的行）和 `...:scans`（谁扫描过，保存之后也保留），TTL 48 小时；一次性登录 nonce `dash_go:sso:nonce:<nonce>`。自 0.1.1 起，一行的警告（`flags[]`）和原因（`reason_msg`）保存为消息，即词条键加上占位符的值（`{"key": "hc.flag.over_budget", "args": {"machine": "...", "used": "2", "budget": "1"}}`），而不是英文句子；句子在读取看板时按请求的语言生成。0.1.0 版保存的行（`reason`、`flags[].text`）仍会显示，为英文。

内存缓存（60 秒，日历为 5 分钟）：machine、MTR 矩阵、日历和员工目录。数据库不响应时使用最近一次的缓存值。

## 6. Confirm：写入什么

Confirm 对每一行按 `uid = <ee_code>_<yyyymmdd>` 做一次 upsert 写入 `t_daily_attn_data`，在同一个事务中，并先锁定当前行（`SELECT ... FOR UPDATE`）：

- `actual_shift` 是扫描的班次 `AM` 或 `PM`，与 HC Input 页面的存储方式一致。（Python 的 Area Assign 页面在此人还没有 actual shift 时会存入班次计划代码；本模块不沿用这种行为。）
- `machine_hc_json` 保留当前记录的请假项，后面是 `{process, machine, hc}`，即 machine 和当天剩余的 HC。已确认的全天请假例外会去掉请假项。`process` 是页面的 process，`machine` 是 `t_mqpd_hc_table` 中的名称。
- `shift` 和 `schedule_shift` 来自当天的归属和日历；`build_in_ot` 用与 Python 代码相同的函数重新计算（`cal_build_in_ot`，用测试表与其输出比对过），`addt_ot` 和 `remark` 保持不变，`total_ot` 为两者之和。
- `update_user` 是领班的显示姓名（Python 代码存的是姓名而不是员工编号），`update_time` 是服务器时间，`hidden` 为 false。
- 如果扫描之后数据发生变化，该行会被跳过，并在页面上给出原因：自动分派的行而此人现在已有 machine 记录、此人现在是全天请假而之前没有被标记、machine 未知或缺少班次代码。手动行可以替换更新的记录，因为这是领班明知而选择的。
- 提交之后，在 `dash_data_dir` 和 `cdn_data_folder` 中的 `DailyAttnData.log` 各追加一行。Python 的读取函数把该文件的修改时间与自己的 Redis 缓存比较，所以这就是让 Python 页面显示已保存数据的方式。如果某个文件夹无法写入，数据仍会保存，页面会显示警告。
- Python 页面的上月锁定同样适用：从每月 8 号起不能再保存上个月（`MonthLocked`，是 `check_mqpd_hc_daily_attn_last_month_locked` 的移植）。
- 写入前会再次检查用户权限，不使用缓存。

保存之后，这些行离开草稿，这些人算作已扫描。此人再次扫描时会提示“already scanned and saved”，并保留已保存的记录；要修改请到 HC Input 或 Area Assign 页面。

## 7. HTTP 接口

所有路径都在 `/dash_go/hc_scan/` 之下。响应为 JSON，带 `Cache-Control: no-store`。错误格式为 `{"error": "<message>", "code": "<code>"}`；技术细节只写入日志。响应中的每个句子（`error`、`message`、`reason`、`flags[].text`、被跳过的行的原因、`log_warning`）都使用请求的语言（先看 cookie `dash_go_lang`，再看 `Accept-Language`，最后用英文）；`code` 的值不随语言变化。页面本身（`GET /dash_go/hc_scan/`）由 shell 渲染，没有会话的访问者会被带到 `/dash_go/login?next=...`。

| 请求 | 用途 |
|---|---|
| `GET api/me` | 用户、允许的 process、客户端设置。 |
| `GET api/board?process=&date=&shift=` | 整个会话：machine、草稿行、没有任务的人（`not_assigned`）、请假的人（`leave`）和任务在别处的人（`elsewhere`）、计数（`totals`：`scheduled`、`placed_hc`、`leave_hc`、`to_assign_hc`，以及草稿的 `pending`、`saved`、`assigned`、`unassigned`、`warnings`）、月份锁定、MTR 新鲜度。 |
| `POST api/scan` `{process, date, shift, input}` | 一次扫描。状态有 `assigned`、`unassigned`、`existing`、`duplicate`、`saved`。 |
| `POST api/assign` `{process, date, shift, ee, machine_id}` | 修改待保存行的 machine（`machine_id` 为空表示取消分派）。 |
| `POST api/remove` `{process, date, shift, ee}` | 删除一条待保存的行。 |
| `POST api/reset` `{process, date, shift}` | 清空所有待保存的行。 |
| `POST api/confirm` `{process, date, shift, ack}` | 保存。`ack` 为 false 且有警告行时返回 409，`code` 为 `needs_ack` 并附带这些行；还有 409 `month_locked` 和 409 `nothing_to_save`。 |

POST 请求需要请求头 `X-Requested-With: dash_go`，浏览器标记为跨站的请求会被拒绝。日期必须在上个月第一天到明天的生产日期之间。

0.1.4 版把列表 `not_scanned` 改名为 `not_assigned`，并用 `scheduled`、`placed_hc`、`leave_hc` 和 `to_assign_hc` 取代了 `totals.expected` 和 `totals.expected_scanned`；`totals.assigned` 现在只统计 Confirm 会写入的待确认行。模块自己的页面是这个 API 唯一的客户端。

## 8. 登录与权限

- Dash 无法把浏览器的登录状态交给 Go 服务，所以链接中带有用 `api.salt_3` 签名的 token（HMAC-SHA256，密钥由标签 `dash_go/sso/v1` 派生，有效期 5 分钟，只能使用一次）。Python 端的生成函数是 `LocalListV3.py` 中的 `build_dash_go_sso_url`；Go 端的验证在 `internal/core/auth`。一个测试向量在两端固定了格式。
- 服务返回 session cookie `dash_go_session`（HttpOnly、SameSite Lax、https 下带 `Secure`、有效期 `session_hours`，默认 12 小时）。链接只接受 `/dash_go/` 之下的本地 `next` 路径。
- 权限不复制到 Go。服务向 Python 后端查询：`POST /api/user/hc_scan`，内容为 `{"func": "hc_user_processes", "user": "<ee_code>"}`，请求头 `Authorization` 为 `<salt_3>_<yymmddHH>` 的 md5（沿用现有 `/api/user` 接口的规则）。后端（`appV2.py` 中的 `api_user_mgmt`）返回显示姓名以及 `check_daily_attn_permission` 得出的 process 列表（HeadCount 组、`HeadCount-All`、`MQPD_AUTH_LIST`）。结果缓存 60 秒，保存之前除外。
- 没有有效 session 时，所有 API 调用返回 401。页面随后显示“登录已过期”和一个去登录页的按钮（0.1.0 为止，它会提示用户重新从 Dash 打开页面）。0.1.1 的密码登录和 Windows 登录得到的是同一个 session cookie；见 [shell_and_login.md](shell_and_login.md)。

## 9. 配置

读取两层 JSON 文件，沿用 Python 应用的补丁约定（`patch_<hostname>.json` 整体替换基础文件的顶层 key）：

1. Python 应用的配置文件夹（`config.json` + `patch_<hostname>.json`），由 `DASH_GO_DASH_CONFIG_DIR` 或 `dash_config_dir` 指定。使用的 key：`postgresql`、`redis`、`api.salt_3`、`api_server`、`cdn_data_folder`、`HC_PROCESS_LIST`、`SHIFT_LEAVE_PATTERN`（请假选项是它从第三个 key 起的各个 key，按文件中的顺序）。
2. `DASH_GO_CONFIG_DIR`（默认 `./config`）中的 `dash_go.json` + `dash_go_patch_<hostname>.json`。它可以覆盖第 1 层的任何 key。示例见 `config/dash_go.example.json`。

服务 key（`dash_go.json` 顶层）：`listen`（默认 `:8750`）、`base_path`（`/dash_go`）、`log_dir`（`logs`）、`dash_home`（`/ie/`）、`session_hours`（12）、`dash_config_dir`，以及自 0.1.1 起的 `photo_url_template`、`dash_api_url`、`login_password`、`sso_require_same_ip`、`trusted_proxies`（见 [shell_and_login.md](shell_and_login.md) 第 8 节；Windows 登录还需要 Python 配置中的 `sso_server` 和 `api.salt_2`）。自 0.1.3 起还有 `update_url`、`update_interval_minutes` 和 `update_restart`（不设置 `update_url` 就不会自动更新，见 [auto_update.md](auto_update.md) 第 6 节）。

询问哪个 Python API：设置了 `dash_api_url` 就用它（在这里是 `modules.hc_scan.dash_api_url`，或顶层的），否则用 `http://<api_server>/api`。`api_server` 来自 Python 配置，**包括本机的 patch 文件**，而开发机的 patch 文件常常为了 Python 应用自己的调用，把它设成生产服务器。这样 dash_go 询问的后端在 Python 的改动部署到那里之前，并没有 `hc_user_processes`（第 11 节）。自 0.1.2 起，日志（所以 `dash_go.exe -check` 也是）会说明用的是哪个 API 以及它来自哪个 key：`hc_scan: the Dash API of the permission check url=http://localhost/api from=dash_api_url`。在开发机上请在 `dash_go.json` 中设置 `dash_api_url`，例如 `http://localhost/api`。

模块 key（`modules.hc_scan`）：

| Key | 默认值 | 含义 |
|---|---|---|
| `employee_id_pattern` | `[A-Z]{2,3}\d{2}[A-Z0-9]\d{2}` | 员工 ID 在扫码输入中的样子（Python 代码校验新成员时使用的格式）。 |
| `draft_store` | `redis` | `redis`，或 `memory`（重启即丢失，仅用于开发）。 |
| `draft_ttl_hours` | 48 | 草稿在 Redis 中的存活时间。 |
| `recent_days` | 7 | 用于 machine 排序的历史天数，也是查找 leader 最近一条记录的范围。 |
| `leader_activity_names` | `["Group Leader"]` | leader 被安排到的非 routine 行的名称（忽略大小写和空格）。 |
| `leader_by_history` | `true` | 在 direct type 变成 `Shift Leader` 之前，用最近一条记录来识别 leader。direct type 维护好之后设为 `false`。 |
| `photo_url_template` | 同名的服务设置（`/api/static/hr/{ee_code}.webp`） | 员工照片；缺失时显示姓名首字母。只有想覆盖服务设置时才在这里设置。 |
| `dash_api_url` | 同名的服务设置（`http://<api_server>/api`） | Python API 的根地址。只有想覆盖服务设置时才在这里设置。 |
| `dash_data_dir` | 无 | 存放 `DailyAttnData.log` 的 Python 应用 data 文件夹。在每台由 Python 页面缓存数据的主机上都要设置。 |
| `production_day_offset_hours` | 7 | 生产日从 07:00 开始。 |
| `am_from_hour`、`pm_from_hour` | 7、19 | 按时钟决定的默认班次。 |
| `poll_ms` | 3000 | 页面刷新间隔。 |

环境变量：`DASH_GO_CONFIG_DIR`、`DASH_GO_DASH_CONFIG_DIR`、`DASH_GO_LISTEN`，以及用于机密信息的 `DASH_GO_PG_PASSWORD`、`DASH_GO_REDIS_PASSWORD`、`DASH_GO_API_SALT`、`DASH_GO_API_SALT_2`，它们优先于文件。机密信息不会写入日志。

Python 一侧：`config.json` 的可选 key `dash_go_url`（默认 `/dash_go/`；当服务运行在与 Dash 应用不同的主机上时填写绝对 URL）。

## 10. 部署（服务器 s2）

1. 构建包：`scripts\build.ps1 -Modules hc_scan`（不带 `-Modules` 则包含所有模块），并在 s2 上解压。
2. 用示例创建 `config\dash_go.json`。把 `dash_config_dir` 指向 s2 上 Python 应用的配置文件夹，把 `modules.hc_scan.dash_data_dir` 指向它的 `data` 文件夹。用 `dash_go.exe -check` 检查连接设置，它也会说明用的是哪个 Python API。
3. nginx。把 `deploy\nginx_dash_go.conf.example` 中的 location 加入 s2 的 nginx 站点并 reload；服务自己处理 `/dash_go/` 之下的所有路径。s1（`prod_eng`，前置入口）只负责把 `/dash_go/` 转发给 s2：它需要示例第二部分的 upstream 和 location，其中 s2 是唯一的服务器（没有本机 backup，因为 dash_go 只运行在 s2 上）。s2 的 nginx 必须信任来自 s1 的 `X-Forwarded-For`，并且它的 `/dash_go/` location 必须发送 `X-Forwarded-For $proxy_add_x_forwarded_for`，而不是 `$http_x_forwarded_for`（[shell_and_login.md](shell_and_login.md) 第 9 节）。两个文件都在 Dash 仓库中：`config/nginx_sgmescimai.conf`（s2）和 `config/nginx_prod_eng.conf`（s1）。
4. 启动 `dash_go.exe`（控制台进程；如果需要在注销后继续运行，请用服务管理工具包装）。日志为 `logs\dash_go-<date>.log`。
5. 部署 Python 的改动（`IE.py`、`LocalListV3.py`、`appV2.py`）。Dash 应用重启后新菜单按钮即可使用；权限函数必须部署到回答 `/api` 的服务器上。

顺序：先在 s2 上部署 dash_go 和 Python 函数，然后是 s2 的 nginx，最后是 s1 的 nginx。后面没有运行中的后端的路由会返回 502。在使用前置服务器之前，先测试 `http://<s2>/dash_go/`。副本 `config/nginx_prod_eng.conf` 里还带着已准备、尚未部署的 API 迁移第 1 步（`/api` 转到 s2，Dash 仓库中的 `migration_step1_api_to_s2.md`）：替换之前先把它和线上文件对比，或者只加入 `dash_go_backend` upstream 和两个 `/dash_go` location。

在开发机上本地调试：Dash 仓库的 `config/nginx_asrock.conf` 的本地调试 server 块里有同样的 location（`http://localhost/dash_go/` 转发到 `127.0.0.1:8750`）。把它复制到正在运行的 nginx，reload nginx，并在启动 `dash_go.exe` 时让 `DASH_GO_DASH_CONFIG_DIR` 指向 Dash 仓库的配置文件夹。如果这台机器的 patch 文件把 `api_server` 指向了另一台服务器，请把 `dash_api_url` 设为本地 API（`http://localhost/api`，见第 9 节）。

只运行一个实例：防止两个终端重复占用最后一个位置的锁在进程内。

## 11. 运维与故障排查

- **某台 machine 一直没有人被自动分派。** 打开页面：machine 卡片上有 “no MTR mapping” 说明该 process 的 LMS 记录中没有同名 machine。请修正 `t_mqpd_hc_table` 或 LMS 中的名称；匹配是精确的（去掉首尾空格）。
- **看不到 MTR 的变更。** 页面上的 `MTR data:` 是 `t_mtr_checkpoint` 最新的 `updated_at`。Python 应用在它的页面读取 MTR 数据时才从 LMS 刷新该表，所以没人打开那些页面时可能是旧的。缓存 60 秒。
- **Python 页面看不到已保存的数据。** 保存时提示 “The data is saved, but the Python pages may show it late”：变更日志文件夹无法写入（`dash_data_dir`、`cdn_data_folder`）。修复权限；对 `DailyAttnData.log` 做一次追加或 touch 即可刷新页面。
- **“Cannot start: Your permission could not be verified right now”（`api/me` 回答 502）。** 日志行 `permission check failed` 里有原因。2026-11-20 在一台开发机上发现：
  - *现象：* 页面对任何人都无法启动。日志写着 `permission check failed ... answered status 204`（0.1.0 和 0.1.1 中只有 `dash api answered status 204`，没有地址）。这台机器的 API 日志里从未出现过 `user_mgmt: hc_user_processes`。
  - *根因：* dash_go 询问 `dash_api_url`，默认是 `http://<api_server>/api`，而这台机器的 patch 文件（`patch_sgmeslt595.json`）把 `api_server` 设成了 `prod_eng`。根据 `nginx_prod_eng.conf`，`prod_eng` 把 `/api` 转发给 s2。用这种方式到达的 API 对这个调用回答了 204，而 `/api/user` 对它没有的函数回答 204，所以 Python 的改动（`hc_user_processes`）还没有部署到 `prod_eng` 后面。带签名的调用本身是对的；它只是发到了另一台服务器，而不是有新函数的那一台。
  - *规则：* 凡是 `api_server` 不是拥有 `hc_user_processes` 的那台服务器的地方，都要在 `dash_go.json` 中设置 `dash_api_url`（开发机：`http://localhost/api`）。先把 `appV2.py` 部署到一台服务器上，再让 dash_go 指向它的 API。
  - *如何确认：* `dash_go.exe -check` 或日志开头几行会写出 `the Dash API of the permission check url=... from=...`；然后在该地址所在机器的 API 日志里查找 `hc_user_processes`。同一个带签名的调用，从 `http://localhost/api` 得到 200 和 process 列表，从 `http://prod_eng.co.murata.local/api` 得到 204。
  - *已排除：* 签名（`api.salt_3` 和小时都是对的，本地 API 接受了同一个调用）、用户的权限，以及开发机上的 nginx。
  - *同一条消息的其他原因：* `dash api rejected the request signature`（状态 401）：两边的 `api.salt_3` 不同，或者两台主机的时钟和时区不一致（签名包含小时）。`dash api request failed`：这台电脑访问不到该 API（地址、防火墙、nginx）。
- **“The login link from Dash has expired / was already used”。** 自 0.1.1 起由 dash_go 的登录页显示（以前是一个纯错误页）：在那里登录，或重新从 Dash 打开 **Scan Assign**。
- **登录后提示“没有权限”。** 账号有效，但 Python 后端没有给它列出任何 HC process（HeadCount 组、`HeadCount-All`、`MQPD_AUTH_LIST`）：请联系 HeadCount 组的管理员申请权限。0.1.0 中这种情况会显示空白页，因为 process 列表是 `null`；0.1.1 返回空列表，页面会说明情况（由 0.1.1 的浏览器测试发现）。
- **某人明明正常却被标黄。** 原因写在该行上。名单和班次与 HC Input 页面一样，来自 `t_daily_attn_member`、`t_mes_ee` 和 `t_daily_attn_calendar`。
- **某张卡片上出现了不在那台 machine 上的人，或者它的预算被不在那里的人占满。** 2026-11-20 在 Slitter 页面上发现；0.1.0 到 0.1.3 的每个版本都有这个问题，已在 0.1.4 修复。
  - *现象：* 某个 process 的看板在它的卡片上显示了其他 process 的人。Slitter 的 Group Leader 行有 10 个人，而 Daily Attn Summary 弹窗只算 1 个。在开发库副本上，2026-11-17 Slitter AM 的卡片上有 25 个人，其中 17 个来自其他 process（Group Leader 7、Others 6、Training 3、QC 1），HSL PM 的 31 个人则全部是外来的。已用 head count 偏高，行看起来是满的或超预算，自动分派提示所有 machine 都已满。
  - *根因：* `DayRecords` 返回当天所有 process 的记录，而卡片只按名称把 entry 匹配到 machine（`machineIndex.Resolve`，是 `resolve_mqpd_hc_machine_id` 的移植：精确 id、精确名称，然后是宽松规则“machine 名称包含记录里的名称”）。machine 名称在不同 process 之间会重复：`t_mqpd_hc_table` 中有 19 个名称出现在多个 process 的行里（`Group Leader` 有 9 个，`Training` 有 8 个，然后是 `Others`、`QC`、`OJT`、`Maintenance` 等）。Winder 把生产线记录为 `T20`，而 `Taping T20` 和 `T20 : Can Assy P2(OP1)` 都包含它。machine 的占用（`usage`，自动分派的预算检查读取它）也用同样的方式匹配。
  - *规则：* 先判断 entry 是否属于这个 process（`dayData.entryHere`：看 entry 的 `process`；没有 process 的 entry 归此人所在的 process），再看名称。在此之前绝不要用名称去匹配某个 process 的 machine。凡是为某一个 process 读取 `t_daily_attn_data` 的代码都要这样做。
  - *如何确认：* 在数据库副本上运行 `TestDevCopyBoardAgreesWithTheDailyAttnSummary`（第 12 节）。用 0.1.3 的匹配方式，它在一天的卡片上数出 458 个其他 process 的人；用 0.1.4 的匹配方式，一个也没有。
  - *已排除：* `t_mqpd_hc_table` 中的预算、MTR 数据，以及记录的日期和班次（显示出来的记录是正确的日期和班次）。
- **本 process 的人有任务，却不在任何卡片上。** 2026-11-20 用同一份副本发现。machine 表里有些名称含两个空格（`CSV-10/13 Set2  (SV#9/10|SD#12/13/15|TCW#12/13/14/18)`、`Fix-Shipment/ Logistic  - 4pax ( AM Shift only)`、`Fix-Tapyrus preparation  2 pax (HSL) - AM Shift (7 days)`），而记录里只有一个，所以名称对不上。自 0.1.4 起，比较名称时连续的空格按一个计算（`normName`）。仍然对不上的会在 “Elsewhere” 卡片中按记录里的名称显示：在副本上是 Packing 的 `Rework - Sorting (Cell)`，它在 2026-11-12 之前的记录里把短横线写成了 `â€“`（写入这些记录的程序的编码错误；该 machine 行在 2026-11-12 被修改过）。请在 machine 表或记录里更正名称，而不是在本模块里。
- **去哪里看。** `logs\dash_go-<date>.log`（请求不含查询字符串，失败记录包含失败的步骤）。`DailyAttnData.log` 中的日志行带有 `dash_go hc_scan` 标记。
- **重置会话。** 删除 Redis key `dash_go:hc_scan:v1:<process>:<yyyymmdd>:<shift>:*`，或者按 “Clear draft”。操作生产环境 Redis 之前先取得批准。

## 12. 验证状态

已完成（2026-11-19）：

- Go 单元测试：分派顺序和门槛、每一种警告、请假处理、Confirm 规则、月份锁定、带登录保护的 HTTP 流程、Redis 草稿存储（miniredis）、token（包括 Python 代码生成的值）、配置分层。
- 仓库读取和写入的 SQL 在一个临时的 PostgreSQL 18 上运行过，使用六张表的真实列类型（`internal/mods/hc_scan/testdata/schema.sql`，读取自开发库副本），测试在 `repo_pg_test.go`。写入路径只在那里运行过，从未在真实表上运行。
- 读取部分和扫描逻辑在真实数据库的副本上运行过（`TestDevCopyReadOnly`，只读会话，草稿在内存中，从不调用 Confirm）：全部 10 个 process、150 次扫描，在真实列类型下没有 SQL 错误，没有 MTR 不到 1 级或没有 machine 的自动行，没有超出预算的自动行。在 10 个 process 中有 8 个把 leader 安排到了 Group Leader 行（CDC 和 QA 的前 15 个人中没有）。第一次加载看板约 70 到 180 毫秒，一次扫描约 150 毫秒。它对数据的发现：QA 完全没有 MTR 记录（每次 QA 扫描都是 “No machine assigned”），而且很多 routine machine 没有 MTR 对应关系（例如 Slitter 14 台中有 4 台，LSL 28 台中有 19 台，Packing 15 台中有 9 台），所以它们永远不会被自动分派。
- 真实的可执行文件用 Python 配置文件夹、模拟的 `/api/user` 接口和临时数据库运行过：用真实的 `build_dash_go_sso_url` 生成的链接登录、process 列表、扫描、Confirm、`t_daily_attn_data` 中的行、两个文件夹中的变更日志行、重复使用的链接被拒绝、未登录的页面。
- 页面在无头 Chrome 中对该数据库做了操作（带和不带 Enter 的扫描、条码前后缀、重复扫描、未知 ID、每一种警告、手动修改、删除、确认对话框、保存、保存后再次扫描）。没有 JavaScript 报错。
- Python 新增部分有单元测试（`tests/test_dash_go_link.py`）；`tests/test_callback_registration.py` 仍然通过。
- 0.1.1（2026-11-20）：模块被移入 shell，所有文字被移入词条文件。原有的测试仍然通过；新的测试覆盖词条（`catalog_test.go`：两种语言完整、代码用到的每个键和值、没有未使用的文字）和语言（`language_test.go`：页面、API 错误、一份草稿在两种语言下阅读、0.1.0 的草稿）。真实的可执行文件配合临时数据库和一个 Python 后端的替身再次运行，页面在两种主题和两种语言下用无头 Chrome 操作过：详情见 [shell_and_login.md](shell_and_login.md) 第 12 节。
- 0.1.2（2026-11-20）：第 11 节中那次失败的原因，是用真实的可执行文件在开发机上找到的（临时配置、真实的本地 Python API 和 Redis）：API 取自 patch 文件时（`api_server` = `prod_eng`），`api/me` 回答 502，日志写出了 API 的地址和状态 204；把 `dash_api_url` 设为 `http://localhost/api` 后，它回答 200 以及该账号的 10 个 process。新的单元测试：状态 204 和错误消息中的地址（`internal/core/dashapi/client_test.go`），API 地址来自哪里（`internal/core/config/config_test.go`）。0.1.2 的 Windows 登录检查，以及 nginx 链路 s1 → s2 → dash_go 的测试，见 [shell_and_login.md](shell_and_login.md) 第 12 节。页面本身没有再用浏览器操作过。
- 0.1.3（2026-11-20）：页面本身没有改动，模块版本跟随程序版本。顶栏的变化（更醒目的模块名称和返回按钮）见 [shell_and_login.md](shell_and_login.md) 第 2 节和第 12 节；自动更新见 [auto_update.md](auto_update.md)。更新会重启服务：草稿在 Redis（`draft_store` 默认值）中，会保留；`draft_store` 为 `memory` 时会丢失。更新只在没有人在点击或提交时安装，所以不会打断正在进行的扫描。模块页面在带有返回按钮的新顶栏下没有用真实数据再操作过（只在顶栏上看了模块页面和“Cannot start”的提示）。
- 0.1.4（2026-11-20）：看板和计数发生了变化（第 2.1 节），并做了测试：
  - 单元测试（`board_scope_test.go`）：与其他 process 同名的 machine；其他 process 的 machine 名称所包含的短名称；其他 process 的支援（带着它的 process 显示，并为预算占用位置）；`All Process` 和没有 process 的 entry；任务在其他 process 的成员（Elsewhere）；全天和部分时间的请假；计数相加一致（Scheduled = Assigned + Leave + To assign）；已经分派的人（永远不在 “not assigned” 中；扫描只显示任务，并让 Confirm 按钮保持关闭）；待确认行替换已保存的记录；人属于其记录所在的班次；用于 machine 排序的天数；machine 名称中的连续空格；看板的 JSON；以及脚本只读取看板里有的键。把 0.1.3 的匹配方式放回去（`entryHere` 一律回答 true）时，其中四个会失败。
  - 在真实数据库的开发副本上（只读会话，`TestDevCopyBoardAgreesWithTheDailyAttnSummary`）：2026-11-02 到 2026-11-18 的 13 天，每天 20 个看板（10 个 process、AM 和 PM），共 260 个看板。在任何一个看板上，卡片上都没有“不是本 process 成员、记录里又没有 entry 写明本 process”的人（有这样 entry 的是支援人员），并且计数与按原始行重新写出的 Daily Attn Summary 逻辑（`cal_daily_summary`）一致：没有换班或加班的人的看板完全一致，有这类人的看板，Scheduled 的差正好等于他们的人数：减去排了本班却记录在另一班的人，加上记录在本班却没有排本班的人（第 13 节）。对比的是这套逻辑，而不是一个运行中的 Python 应用的弹窗。
  - 页面在无头 Chrome 中对同一份副本做了操作，经过一个没有保留的临时服务副本（真实的处理函数和页面、草稿在内存中、只读数据库会话）：Slitter、HSL、CSV 和 Packing 的看板，英文和中文，浅色和深色主题；Leave 卡片、“Elsewhere” 卡片、支援人员的 process 标签以及 “Not assigned yet” 中的人；扫描一个没有任务的人（Assigned 0 变 1，To assign 22 变 21，Pending 1，Confirm 亮起）和一个已经分派的人（计数不变，显示他的任务，Confirm 保持关闭）。没有 JavaScript 报错。
  - PostgreSQL 测试（`repo_pg_test.go`）在一个临时的 PostgreSQL 18 上再次运行；`go vet ./...` 和 `go test ./...` 通过。

**没有验证：**

- 对真实表的写入（只对带真实列类型的临时数据库写过），以及生产数据库。
- 真实的照片、真实的扫码枪（是否发送 Enter、会加哪些字符）以及真实的 Redis 服务器（Redis 存储只用进程内的 Redis 测试过）。
- 针对 s2 本身的 Python 后端调用 `hc_user_processes`（0.1.2 中它是对开发机的后端试的，以及通过 `prod_eng` 试的，后者回答 204：该函数没有部署到它后面），以及在运行中的 Dash 应用里点击按钮（只单独测试了回调函数、布局文本和 Dash `Location.href` 机制）。
- 已部署的 s1 和 s2 上的 nginx（它们的文件用隔离实例测试过，[shell_and_login.md](shell_and_login.md) 第 12 节）以及 s2 上的端口。
- 使用者对中文措辞的审阅；人员的头像（页面只在没有头像的情况下试过）。
- 0.1.4：真实的可执行文件和真实的数据库。页面是通过副本上的一个临时服务操作的。导致 0.1.4 的那张截图所在的日期（2026-11-20）不在副本里（只有 5 条记录），所以缺陷是在 2026-11-17 复现的，那一天是同样的情形。Confirm 本身在 0.1.4 中没有改动（只改了让它的按钮亮起的条件）；没有再对真实数据库运行过。新卡片和新计数的中文文字没有经过使用者审阅。

## 13. 决定与已知限制

- 分派顺序（先等级，后最近 7 天）遵循领班的决定：自动分派不能掩盖缺失的 MTR 记录。它被隔离在 `RankMachines`（`assign.go`）中。
- 自动分派永远不超出预算；手动选择可以，但会有警告。
- 领班确认的全天请假会被 machine 替换，被替换的请假除了 Python 一侧数据库的历史之外不会在别处保留。如果请假是对的，请删除该行。
- leader（direct type 为 `Shift Leader`，目前也包括最近一条记录在 Group Leader 行上的人）只会去 Group Leader 行，即使他们有 MTR 记录也一样，该行满了就保持无 machine。历史规则只是过渡办法：direct type 维护好之后用 `leader_by_history` 关掉它。
- 每人一台 machine。半天假的人在一台 machine 上获得剩余的 HC。
- 不使用打卡考勤，也不显示 HC Allocation 的 area。
- 扫描顺序会影响谁得到哪个位置（贪心，没有重新平衡）。
- 只有一个服务实例；每个 process、日期、班次共用一份草稿。
- 文字在词条文件中（`internal/mods/hc_scan/i18n/{en,zh}.json`）；草稿保存的是消息而不是句子，所以它按查看者的语言显示。
- 有任务的人属于他记录所在的班次；Python 应用的 Daily Attn Summary 把人算在日历所给的班次。两者相同，只有换班（排 AM、记录为 PM）和休息日加班（日类型 `R`，记录为 AM）例外：弹窗把换班的人算在 AM，把加班算在实际班次的 OT HC 里，页面把两者都算在他们工作的班次，也就是扫描把他们放进去的那个班次。所以页面的 Scheduled 也会包含没有排班的人：在 2026-11-17 的开发库副本上，20 个看板中有 6 个比弹窗多 1 到 5。
- 名册包含 Direct 和 Shift Leader 成员；弹窗只算 Direct。有 Shift Leader 成员的 process，页面显示的人比弹窗多。
- 部分时间请假而没有任务的人，剩余的时间留在 “Not assigned yet” 和 To assign 中；弹窗不会把这样的人叫作“待记录”。
- 其他 process 的支援显示在卡片上并占用位置，但不计入计数。
- 名称不在任何 machine 列表中（或属于其他 process）的任务，只显示在 “Elsewhere” 卡片中：它的 head count 不在任何已用/预算数字里。
