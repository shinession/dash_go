# English

**Updated:** 2026-11-28 (dash_go version 0.2.4). This describes the current code of `internal/core/shell`, `internal/core/i18n`, `internal/core/auth`, `internal/core/hc`, `internal/core/pyobj` and the Python calls in `internal/core/dashapi`. Version 0.1.2 changed one rule of the Windows sign-in (a browser on the computer that runs dash_go, section 7.2) and put the address of the Python API in use into the log (sections 8 and 10). Version 0.1.3 made the name of the module in the header stand out and added a **Back** button next to it (section 2); the automatic update of the same version is described in [auto_update.md](auto_update.md). Version 0.1.5 added the **Documentation** and **Guide** buttons to the header between the language switch and the user box, the bilingual `/dash_go/help` user guide page, and the step-by-step page tour (`tour.js`, with first-visit auto-start via `localStorage`, section 2). Version 0.1.9 added module icon support (`module.Info.Icon` on landing cards and `PageSpec.AppIcon` next to the module name in the header, starting with the `scanner` barcode-scan icon for `hc_scan`, updated on 2026-11-26 to a four-corner viewfinder framing vertical barcode bars) and removed per-module version numbers so the version is maintained once at the repository level (`VERSION`). Version 0.2.0 added the `budget` icon (person silhouette paired with a 6-tooth setting gear) for `hc_budget`, promoted the shared HC process picker (`.screen` / `.process-buttons` in `shell.css`, `message_screen` template in `layout.html`, and `dashGo.picker` in `shell.js` sharing `sessionStorage` key `hc.process` across `hc_scan` and `hc_budget`), added top-level `dash_data_dir` configuration, and extracted `internal/core/hc` with `dashapi.Profile.Type` / `IsAdmin()`. Version 0.2.4 moved sessions (with 24-hour sliding expiration and immediate server-side logout revocation), Windows SSO pending flows, and failed password login rate limits into shared Redis so multiple `dash_go` instances (s2 and s3) can run behind weighted round-robin load balancing without `ip_hash` (sections 7–12). Section 12 says what was verified and what was not.

## 1. Overview

Version 0.1.1 gave every page of dash_go one frame, the **shell**:

- a header with the logo, the name of the module with a Back button, a light / dark theme button, an EN | 中文 language switch, the signed-in user (picture or initials, name) and a sign-out button, or a sign-in button;
- a light and a dark theme (the dark one is the look of version 0.1.0), chosen by the person and remembered by the browser;
- English and Chinese for every text, also the warnings and errors that the server writes;
- a login page of its own (account and password, or the Windows sign-in), next to the one-time link that Dash builds.

The shell lives in `internal/core`, so every module (and the landing page `/dash_go/`) gets the same header; a module only brings the content of its page and its texts. The shell files are served below `/dash_go/_shell/`.

| Part | Where | What |
|---|---|---|
| Pages and header | `internal/core/shell` (`web/templates`, `web/assets`) | Rendered by the server (`html/template`), so language, user and first theme are right before any script runs. |
| Texts | `internal/core/i18n` (`catalog/en.json`, `catalog/zh.json`) and `internal/mods/<module>/i18n` | One flat JSON file per language, shared by the Go code and the browser. |
| Sign-in | `internal/core/auth` | Hand-over link, password, Windows sign-in, session cookie, limits. |
| Python calls | `internal/core/dashapi`, `internal/core/pyobj` | `verify_user`, `hc_user_processes`, `sso_result`; the pickle reader. |

## 2. The header

```text
[logo CIM EDGE]  | HC Scan Assign  [<- Back]      (sun/moon)  [EN|中文]  (book) (compass)  (picture) Name  [sign out]  <- shell, every page
 Winder  [date] [AM|PM] [Change process]             Scheduled 20  Assigned 14  Leave 3  To assign 3  Pending 2 ...    <- page bar of the module
```

- The logo is a link to `/dash_go/`. The name of the module is shown next to it (`AppKey` of the page), optionally preceded by its sprite icon (`AppIcon`, since 0.1.9). Since 0.1.3 it is in the accent colour of the theme (`--accent`: `#0b63c5` in the light theme, `#4da3ff` in the dark one), 1.3 rem and weight 600; before it was the muted grey, which was too dim on the dark header. The contrast of the accent on the header is covered by `palette_test.go`.
- The **Back** button (since 0.1.3, only on the pages of a module: the landing page and the login page have no module name and no button) is the link `/`, the root of the server the page was opened on, not `dash_home`. The link has no host, so it follows the address the person typed: `http://127.0.0.1/` on the development machine, `http://<s2>/` on s2, and the host of s1 when the page was opened through s1. Its texts are `ui.back` and `ui.back_title`.
- The theme button shows the icon of the theme that is on now: the sun in the light theme, the moon in the dark one.
- The language switch is a link: it sets the cookie and comes back to the same page (query string included).
- The **Documentation** (book icon) and **Guide** (compass icon) buttons sit between the language switch and the user box / Sign in button (since 0.1.5, adapted from the TimeVault project; hidden on the login page):
  - **Documentation** (`PageSpec.HelpHref`, `target="_blank" rel="noopener"`) opens the bilingual user guide for the current page in a new tab (`/dash_go/help` on the landing page, `/dash_go/<module>/help` on module pages). Help pages follow the current language and light/dark theme, use a sticky table of contents (`.dg-doc`), and do not auto-start the tour when opened.
  - **Guide** (`PageSpec.Guide`, `web/assets/tour.js`) starts a read-only spotlight tour of the visible elements carrying `data-tour` attributes (emitted server-side via `{{.Tour "<key>"}}`, reading `<key>.title` and `<key>.text` from the i18n catalog; page content steps come first, header steps follow). It supports **Next** / **Back** / **Done**, keyboard shortcuts (`→`, `←`, `Esc`, focus-trapped `Tab`), sticky bar awareness (`data-tour-sticky`), and docked layout on narrow screens (`<= 640px`). On a visitor's first visit to a page, the tour starts automatically once (remembered in `localStorage` under `dash_go.tour.seen.<key>`, via `PageSpec.TourAutoKey` or `dashGo.tour.autoStart(key, { skipHeaderIfSeen: true })`; if `localStorage` is unavailable, auto-start is skipped so it never loops). Clicking the Guide button replays the complete tour at any time.
- The user box shows the picture of the person (section 6) or the initials, the name (cut with an ellipsis after 11 rem; the tooltip is "Name (employee code)") and the sign-out button. A visitor without a session sees a **Sign in** button instead (not on the login page itself).
- When the session ends while a page is open, the script of the page turns the user box into the Sign in button and shows "Session expired" with a button to the login page.
- Below 760 px the module name is hidden (the Back button stays) and the logo shrinks to its mark; below 480 px the name of the user is hidden too. The header wraps instead of scrolling.

## 3. The logo

The artwork is in `docs/CIM_Edge_design/` (`CIM_EDGE_editable.svg` is the source of the paths). The header uses a **horizontal lockup** that was made from those paths without new shapes: the mark (the E with the circuit traces) and the wordmark `CIM EDGE`, the wordmark scaled up by 1.65 and centred on the mark. The server icon and the two lines of the original are left out (they cannot be read at 36 px height). The lockup is inline SVG in `web/templates/logo.html`; its colours are CSS variables, so the theme can change them. `web/assets/favicon.svg` is the mark alone, in colours that read on a light and on a dark tab strip.

The navy of the logo (`#00448f`) has a contrast of only 1.9:1 on the dark header. In the dark theme `--logo-navy` is `#4f8fe0` (5.4:1); the teal is unchanged. A test (`palette_test.go`) keeps the logo at 3:1 or better in both themes.

If the logo has to be changed: replace the paths in `logo.html` and `favicon.svg` (the viewBox of the lockup is computed from the bounding boxes of the paths) and check both themes.

## 4. Light and dark theme

- `theme-init.js` runs in the `<head>` before the stylesheet (CSP allows no inline script) and sets `data-theme="light|dark"` on `<html>`: the choice stored in `localStorage` key `dash_go.theme`, otherwise the setting of the system (`prefers-color-scheme`). Without a stored choice the page follows the system, also when it changes while the page is open.
- The button flips the theme and stores it. A blocked `localStorage` only means that the choice does not survive a reload.
- All colours are CSS variables in `shell.css`: `:root` is the light theme, `:root[data-theme="dark"]` the dark one (the palette of version 0.1.0). Module stylesheets use the variables only; they have no colour of their own.
- `palette_test.go` reads the two palettes from `shell.css` and checks that every text colour is readable on every surface it is used on (4.5:1 for text, 3:1 for the logo). A change of a colour that makes a theme unreadable fails the test.

## 5. Language and texts

### 5.1 Catalogs

Every text a person reads is in a catalog, never in Go, JavaScript or HTML:

- `internal/core/i18n/catalog/{en,zh}.json`: the core (`ui.*` the header and common texts, `login.*` the login page, `err.*` errors of the core).
- `internal/mods/<module>/i18n/{en,zh}.json`: the texts of a module, every key with the prefix of the module (hc_scan: `hc.*`). The module registers them in `init()` with `i18n.Register`, so a package without the module does not contain them.
- A file is a flat JSON object of texts. Placeholders are `{name}` (lower case letters, digits, underscore), not `%s`: the word order of Chinese differs. A placeholder without a value stays visible, a missing key shows the key, a missing Chinese text falls back to English.
- Key families of hc_scan: `hc.ui.*` the page and its script (sent to the browser), `hc.err.*` errors, `hc.flag.*` warnings of a line, `hc.reason.*` why a line was placed or not, `hc.msg.*` messages of a scan or a save, `hc.skip.*` why a line was not saved, `hc.step.*` what a failed request was doing. Only the prefixes a page asks for are put into the page (`ui.` always, `login.` on the login page, `hc.ui.` on the hc_scan page); the rest stays on the server.

### 5.2 Which language

The cookie `dash_go_lang` (`en` or `zh`, path `/dash_go/`, one year) wins; without it the first language of the `Accept-Language` header that is Chinese or English; otherwise English. The language switch is the link `/dash_go/lang?set=zh&next=<page>`: it sets the cookie and redirects (303) to `next`, which must be a page below the base path. The server puts the language into the context of every request (`i18n.Middleware`), so the code below the HTTP layer translates with `i18n.T(ctx, key, i18n.P{...})`.

### 5.3 Drafts store messages, not sentences

A draft line in Redis keeps the warnings and the reason as a catalog key plus its values (`{"key": "hc.flag.roster_other_process", "args": {...}}`), and the board turns them into sentences for the language of the request. A line that was scanned while the page was English therefore reads Chinese after the switch. Drafts that version 0.1.0 left in Redis (48 hour TTL) have English sentences and no message; they are shown as they are.

### 5.4 Chinese wording

`HC`, `MTR`, `Lv1` to `Lv3`, `AM` / `PM`, names of machines, processes and people, and the values `Direct` and `Shift Leader` of the direct type stay as they are. Machine = 机台, activity = 活动, process = 工序, shift leader = 领班, draft = 草稿, Confirm = 确认. The wording was written by the developer and is for the users to review: a change is a change of the text in `zh.json` only.

### 5.5 Adding a text or a module

1. Add the key to `en.json` and `zh.json` of the catalog. Use the prefix of the module.
2. Use it: Go `i18n.T(ctx, "hc.msg.x", i18n.P{"name": n})` (or `msg(...)` for a stored message), template `{{.T "hc.ui.x"}}`, script `t('hc.ui.x', { name })`.
3. `go test ./...` checks the rest: both languages have the same keys and placeholders, every key the code names exists, the values the code passes are exactly the placeholders of the text, every `s.internal("...")` step has a text, every `hc.ui.*` text is used, and no catalog text is unused (`catalog_test.go` of the module and of the shell).

## 6. The user box and the pictures

- The picture is `photo_url_template` (default `/api/static/hr/{ee_code}.webp`, the static folder of the Python application, reached through the `/api` location of nginx). The same function (`dashGo.avatar`) draws the user box, the badges and the machine cards: initials in a circle first, the picture when it has loaded.
- The Dash static endpoint is reported (ADR 0015 of the TimeVault project, checked there against the real application) to answer a missing file with **status 202 and a placeholder picture of 480 x 320 pixels**, never "not found". A picture of exactly that size is therefore treated as "no picture" and the initials stay. A picture that is missing (error) is not asked for again for 10 minutes of the page's life. This project did not check that answer on the real server; the 480 x 320 rule was tested with generated images only.

## 7. Signing in

| Way | How |
|---|---|
| **Link from Dash** | The **Scan Assign** button of Dash builds a signed one-time link (`GET /dash_go/auth/sso?t=...&next=...`, see `docs/hc_scan_go_link.md` in the Dash repository). It is unchanged. A link that does not work (invalid, expired, already used, nonce store down) now leads to the login page with the reason (`?error=link_invalid`, `link_expired`, `link_used`, `unavailable`) instead of a dead end. |
| **Account and password** | The login page asks for an account (employee code, name or e-mail address) and the password. `POST /dash_go/auth/login` `{username, password, next}` asks the Python backend (`verify_user`, then `hc_user_processes` for the name and the employee code). The password is passed through, never stored or logged. Switched off with `login_password: false`. |
| **Windows (SSO)** | The button opens a window with the page of the company sign-on service (`http://<sso_server>/sso/api/login?token=<token>`), the same one the Dash application uses. dash_go issues the token itself, remembers it for 5 minutes with the address of the browser, and asks `POST /dash_go/auth/sso/poll` every 2 seconds until the service has recorded the Windows user. The result is read once from the backend (`sso_result`) and mapped to the active Dash account of that employee code. Only offered when `sso_server` is set. |

After any of them (since 0.2.4) the service creates a server-side session in Redis (`dash_go:session:<id>`, JSON `{"ee", "name"}`) and sets the opaque random session cookie `dash_go_session` (256-bit random base64url ID, HttpOnly, SameSite Lax, `Secure` behind https). Expiration is **sliding** (`session_hours`, default **24**): every authenticated request refreshes both the Redis TTL (atomically via Lua `GET` + `PEXPIRE`, compatible with Redis 2.6+) and the browser cookie's `MaxAge`. If Redis is unreachable during verification, the service logs the error (never logging the session ID) and treats the request as unauthenticated (401 on API calls, redirect to login on pages). **Sign out** (`POST /dash_go/auth/logout`) deletes the session key from Redis immediately and clears the browser cookie, so replaying the old cookie fails on every instance right away; the login of the Dash application in the browser is not touched. Legacy dot-separated signed session cookies from 0.2.3 and earlier are rejected locally without querying Redis (users sign in once after upgrading to 0.2.4). When Redis is not configured (`redis.host` empty, e.g. unit tests or standalone local development), sessions, SSO flows and login rate limits fall back to process memory and the server logs a warning at startup. A person without a session who opens `/dash_go/hc_scan/` goes to `/dash_go/login?next=...` and comes back after signing in.

### 7.1 Routes

| Request | Purpose |
|---|---|
| `GET /dash_go/` | Landing page with the modules (public). |
| `GET /dash_go/help` | General user guide (public, English and Chinese, lists compiled-in modules and links to their guides). |
| `GET /dash_go/login?next=&error=&signed_out=&expired=` | Login page. A signed-in person is sent on to `next`. |
| `GET /dash_go/lang?set=en\|zh&next=` | Language switch. |
| `GET /dash_go/_shell/...` | Files of the shell (`shell.css`, `shell.js`, `tour.js`, `theme-init.js`, `login.js`, `icons.svg`, `favicon.svg`). |
| `GET /dash_go/auth/sso?t=&next=` | One-time link of Dash. |
| `POST /dash_go/auth/login`, `.../auth/logout`, `.../auth/sso/start`, `.../auth/sso/poll` | JSON endpoints of the login page. |

### 7.2 What protects the sign-in

- The JSON endpoints need the header `X-Requested-With: dash_go` (a cross-site form cannot send it), refuse a request that the browser marks `Sec-Fetch-Site: cross-site` or whose `Origin` names another host (compared without the port, as the browser compares sites). Logout needs the same, so a link cannot sign somebody out.
- **Limits of failed password sign-ins**: 5 per client address and 10 per account in 10 minutes (the account is compared without case and, for an e-mail address, by the part before the @). The answer is 429 with `Retry-After`. A successful sign-in forgets the failures of the account and gives the address one slot back; an outage of the user directory is not counted against the account. Since 0.2.4, when Redis is configured these counters are stored in Redis (`dash_go:auth:limit:ip:<ip>` and `dash_go:auth:limit:acct:<sha256>`, hashing the normalized account so a password mistyped into the username field is never written to Redis in plain text), so multiple instances enforce the same limits. If Redis is unreachable on password login, the endpoint fails closed with 503 (`unavailable`).
- The same message for an unknown account, a wrong password and a deactivated account (the backend answers 202 for all three).
- **Windows sign-in**: only tokens this server issued are accepted (24 random characters, 120 bits), a result is used once, at most 5 waiting sign-ins per address and 2000 in all, one look at the result per second and flow. Since 0.2.4, when Redis is configured these pending flows live in Redis (`dash_go:sso:flow:<token>`, `dash_go:sso:flows`, `dash_go:sso:flows:ip:<ip>`, 5-minute TTL), so `/auth/sso/start` and `/auth/sso/poll` can land on different instances behind nginx and survive restarts. The address of the browser that started the flow must equal the address the sign-on service recorded (`sso_require_same_ip`, default on): that stops the forced sign-in in which an attacker starts a flow, makes a victim open the sign-on link with the attacker's token and polls for the identity of the victim. The address of the browser is the address of the connection, unless that is one of the `trusted_proxies`: then it is the right-most address of `X-Forwarded-For` that is not a trusted proxy (so a browser cannot choose its own address). **A browser on the computer that runs dash_go** is the one exception (0.1.2): opened as `localhost` it comes from a loopback address (`127.0.0.1`, `::1`), while the sign-on service records the address of that computer. That is one browser on one computer, so when the sign-in started from a loopback address, any address of this computer's network interfaces counts as the same address (`sameBrowser` in `internal/core/auth/ssoflow.go`). A browser on another computer never comes from a loopback address (the left side of `X-Forwarded-For` is not believed), so no other sign-in is relaxed. Before 0.1.2 a sign-in from `localhost` was always refused.
- Nothing secret is logged: the password is never written, the typed account is only written as a short hash (`acct=`), because a person may type a password into the account field. A refused Windows sign-in logs the two addresses that were compared and how the request reached the server (`connection`, `trusted_proxy`, `forwarded`) with a hint.
- `next` is only accepted as a local path below the base path. The content security policy allows scripts, styles and pictures only from the service itself and forbids `base`, plug-ins and forms that post elsewhere.

### 7.3 Calls to the Python application

| Call | Authorization | Used for |
|---|---|---|
| `POST <api>/user/hc_scan` `{"func":"verify_user","user","password"}` | `md5("<api.salt_3>_<yymmddHH>")` | 200 = right and active; 202 = not. |
| `POST <api>/user/hc_scan` `{"func":"hc_user_processes","user"}` | same | Name, employee code and HC processes of an account (also the permission check of the pages). |
| `POST http://<sso_server>/api/sso_result/anything`, body `zlib(pickle({"method":"get","func":"sso_result","sso_token":...}))`, `Content-Type: text/plain` | `md5(<api.salt_2>)_md5(<yymmddHH>)` | The result of a token: `{"status": "ok", "ee_code", "windows_user", "ip", "ts"}`, or `{"status": "pending"}`. The backend deletes it when it is read. |

`<api>` is `dash_api_url`, default `http://<api_server>/api`. The signatures use the local time of the server like the Python code, so the clocks and time zones of both hosts must agree. The answer of the sign-on endpoint is `zlib.compress(pickle.dumps(dict))`: a pickle can run code in the program that loads it, so `internal/core/pyobj` never loads it. It interprets the opcodes into inert data (the decoder is the strict one of the TimeVault project, with limits on size, depth and number of opcodes), accepts only plain values and refuses everything else. Request bodies are written with protocol 2. Both directions were tested with the real `pickle` and `zlib` of Python.

## 8. Configuration

| Key | File | Default | Meaning |
|---|---|---|---|
| `session_hours` | `dash_go.json` | `24` | Idle session lifetime in hours (1–168). Since 0.2.4, expiration is sliding: every active request refreshes both the Redis session TTL and the browser cookie's `MaxAge` to this duration. |
| `photo_url_template` | `dash_go.json` | `/api/static/hr/{ee_code}.webp` | Pictures of people (also the default of the module setting of the same name). |
| `login_password` | `dash_go.json` | `true` | The login page asks for an account and a password. |
| `sso_require_same_ip` | `dash_go.json` | `true` | Windows sign-in: the sign-on service must have seen the address that started it. Turn it off only when no proxy can pass the address of the browser. |
| `trusted_proxies` | `dash_go.json` | `["127.0.0.1", "::1"]` | Reverse proxies (addresses or CIDR ranges) whose `X-Forwarded-For` is believed. `[]` trusts none. |
| `dash_api_url` | `dash_go.json` | `http://<api_server>/api` | Root of the Python API. Without it and without `api_server` the login page is off (the link from Dash still works). `api_server` comes from the Python configuration **including the patch file of the host**: on a development machine that may be the production server, which can be older than dash_go (section 10). The log and `dash_go.exe -check` say which API is used. |
| `sso_server` | Python `config.json` (or `dash_go.json`) | none | Host of the Windows sign-on service (`host`, `host:port` or `http(s)://host[:port]`); without it the Windows sign-in is off. |
| `api.salt_2` | Python `config.json` | none | Secret of the sign-on result endpoint; required when `sso_server` is set (`dash_go.exe` refuses to start without it). Environment variable `DASH_GO_API_SALT_2` wins. |
| `api.salt_3` | Python `config.json` | none | Secret of the `/api/user` endpoint and of the hand-over link (unchanged). |
| `dash_home` | `dash_go.json` | `/ie/` | The login page links to the Dash application. |

`dash_go.exe -check` prints whether the password and the Windows sign-in are on, and since 0.2.4 also prints the session store and sliding TTL (`session store redis (sliding 24h)` or `memory`). Since 0.1.2 the log (and so `-check`) also says which Python API the sign-in and the modules ask and which key it came from: `the Dash API of the sign-in url=http://localhost/api from=dash_api_url`. Since 0.1.3 `-check` also says whether the automatic update is on. Its keys (`update_url`, `update_interval_minutes`, `update_restart`) are in [auto_update.md](auto_update.md), section 6. Since 0.1.6 `-check` also asks that API with a signed call that reads nothing (the line `Dash API (sign-in)`), so a wrong `api.salt_3` (`dash api rejected the request signature`) or a backend without the functions (`answered status 204`) shows up there before anybody tries to sign in; the other checks and the exit code are described in [hc_scan_page.md](hc_scan_page.md), section 11.

## 9. Deployment

- No new location is needed: `/dash_go/` already covers `_shell`, `login`, `lang` and `auth`. The nginx example sets `X-Forwarded-For $proxy_add_x_forwarded_for`, which the sign-in uses; the default `trusted_proxies` trusts nginx on the same computer.
- **Multi-instance behind a front proxy (s1 → s2 + s3, since 0.2.4)**: because sessions, Dash handover nonces, Windows SSO flows, password rate limits, `hc_scan` drafts and `hc_scan` session locks are all kept in shared Redis, s1 (`prod_eng`) can load-balance `/dash_go/` across s2 and s3 with standard weighted round-robin (no `ip_hash` required). Both s2 and s3 must point their `redis` configuration to the **exact same Redis host and DB** (s2's Redis) and share the same `api.salt_3`.
- **Two proxies in a row** (a server in front of the servers that run dash_go): the nginx next to dash_go (on **both s2 and s3**) sees the address of the front server, not the browser. Either list the front server in `trusted_proxies` (the right-most address that is not trusted wins), or let each backend nginx use `set_real_ip_from <front server>; real_ip_header X-Forwarded-For; real_ip_recursive on;`. Without one of them every Windows sign-in routed to that server is refused as `address_mismatch`; the log line says which addresses were compared. `sso_require_same_ip: false` makes it work but removes the protection against the forced sign-in. Example: the development machine `sgmeslt595` behind the front server `prod_eng`. `config/nginx_sgmeslt595.conf` of the Dash repository has the three directives in its `http` block with the address of `prod_eng`, like `nginx_sgmescimai.conf` of s2, which trusts `prod_eng` the same way. nginx believes the header only from that address, so a browser cannot choose its own address; `/tv/` keeps its own `set_real_ip_from` for s2. The nginx that is running reads its own copy of the file: copy it, run `nginx -t`, then reload nginx. **In production** the chain is browser → s1 (`prod_eng`, the front door) → the nginx of s2 / s3 → dash_go. Both s2 and s3 nginx trust `X-Forwarded-For` from s1 (`set_real_ip_from`), s1, s2 and s3 forward `/dash_go/` with `X-Forwarded-For $proxy_add_x_forwarded_for`, and nothing has to be set in dash_go (`trusted_proxies` keeps its default). The `/dash_go/` location on s2 and s3 must not pass the header through (`$http_x_forwarded_for`, the idiom of the other locations of s2): a request that reaches s2 or s3 without s1 would bring its own address, and dash_go would believe it (tested, section 12). s1 has no local backup for `/dash_go/`, because dash_go runs on s2 and s3. The example is `deploy/nginx_dash_go.conf.example`.
- **A browser on the computer that runs dash_go** (a development machine, `http://localhost/...`): the Windows sign-in matches the address of the computer, section 7.2. Nothing has to be configured.
- **The Python API of a development machine.** dash_go asks `dash_api_url`, by default `http://<api_server>/api`, and the patch file of a development machine often sets `api_server` to the production server for the calls of the Python application itself. Set `dash_api_url` in `dash_go.json` there (for example `http://localhost/api`); otherwise the sign-in and the permission check ask a backend that may not have the functions `verify_user` and `hc_user_processes` yet (section 10).
- The sign-on service is opened by the **browser** (the popup), and read by **s2 and s3** through its backend: both s2 and s3 must reach `http://<sso_server>/api/sso_result/anything`.
- Passwords travel from the browser to dash_go and from dash_go to the Python backend over the same http as the rest of the company applications. Use https between the browser and nginx where the site has it; then the cookie is `Secure` automatically (`X-Forwarded-Proto`).

## 10. Troubleshooting

- **The login page says "Signing in on this page is not set up".** Neither `login_password` nor the Windows sign-in is on, or neither `dash_api_url` nor `api_server` is set. `dash_go.exe -check` and the first lines of the log say which.
- **"Invalid account or password" for a right password.** The user may be deactivated (the backend answers the same for all failures), or the clocks of s2/s3 and the backend differ by more than the hour of the signature (`log: dash api rejected the request signature`).
- **Windows sign-in says "could not be matched to this browser".** The two addresses differ. The log line `single sign-on refused` says which: `started_from` is the address dash_go took for the browser, `sso_address` the one the sign-on service saw, and `connection`, `trusted_proxy` and `forwarded` say how the request arrived.
  - `started_from` is the address of a server and `forwarded` ends with it: a proxy in front of the nginx next to dash_go hides the browser. Section 9: `set_real_ip_from` in that nginx, or `trusted_proxies`. This is what happens when `prod_eng` forwards to a server whose nginx does not trust it.
  - `trusted_proxy=false` and `forwarded` is set: the proxy that connects is not in `trusted_proxies`.
  - `started_from` is `127.0.0.1` or `::1`: the page was opened from the computer that runs dash_go (`http://localhost/...`). Found on 2026-11-20 on the development machine: `started_from=127.0.0.1`, `forwarded=::1`, `sso_address=10.135.36.89`, the LAN address of the computer; there was no proxy in front of its nginx, so the proxy settings were not the cause. Until 0.1.1 this was always refused; since 0.1.2 it matches when `sso_address` is an address of this computer. If it is still refused, `sso_address` belongs to another computer (a proxy for browsers, a VPN).
  - The addresses match, but the page then says that the sign-in cannot be completed right now (log: `single sign-on failed: the user directory cannot be reached`, with `answered status 204`): the Python API behind `dash_api_url` or `api_server` is the wrong one, or older than dash_go. See [hc_scan_page.md](hc_scan_page.md), section 11.
- **The popup is blocked.** The page says so; allow pop-ups for the site.
- **The Chinese page shows English text.** A text that is only in English: add it to `zh.json` (the tests fail when a key is missing in one language, so this means a draft of version 0.1.0 or a text written by a person, such as a machine name).
- **After a restart everybody has to sign in again, or users lose login across s2 and s3.** Since 0.2.4 sessions, Windows SSO flows and login limits live in Redis and survive restarts and cross-server routing as long as both s2 and s3 point to the same Redis host and DB (`dash_go.exe -check` prints `session store redis (sliding 24h)` and tests the Redis connection). If Redis becomes unreachable, session lookups log `session store lookup failed` and return unauthenticated until Redis recovers. Upgrading from 0.2.3 to 0.2.4 requires existing users to sign in once because legacy signed session cookies are no longer accepted.
- **Theme or language does not stick.** The browser blocks `localStorage` (theme) or cookies (language).

## 11. Decisions and known limits

- The login logic follows the TimeVault project (same flow, limits and checks); the accounts are not read from Redis as there, but asked from the Python backend, so passwords and the account list stay in one place.
- Sign-out ends the session of dash_go only (removing the key from Redis immediately and clearing the browser cookie).
- There is no "remember me", no password change and no session management UI; sessions slide by `session_hours` (default 24 hours) on every active request and can be revoked directly in Redis (`dash_go:session:*`).
- Since 0.2.4, sessions, nonces, Windows SSO flows and failed-login rate limits are shared in Redis, so multiple instances (s2 and s3) can safely run behind one address without `ip_hash`. Without Redis configured, the service falls back to process memory (single instance only).
- The Windows sign-in needs the browser to reach the sign-on service and every dash_go host (s2, s3) to reach its backend.
- The light palette and the Chinese wording are the developer's proposal.
- A browser on the computer that runs dash_go matches the addresses of that computer (0.1.2). One browser on one computer must not be refused because the two services name the computer differently (loopback for dash_go, the LAN address for the sign-on service); the rule of the check, the same computer for both steps, is unchanged. Only a sign-in that started from a loopback address is affected.
- While the automatic update is installed (a moment, when nobody is clicking or submitting) a request that changes something gets 503 with the code `updating` and the message `err.updating`; requests that only read are never refused ([auto_update.md](auto_update.md), section 3).

## 12. Verification status

Done (2026-11-20):

- Go unit tests of every package: catalogs (same keys and placeholders in both languages, every key and value the code uses, no unused text), language choice, the pickle reader (vectors made by the real Python 3.12 `pickle` and `zlib`, hostile and damaged input, zip bomb, depth), the Python clients (signatures checked against values computed in Python), configuration, the sign-in (password, limits, Windows flow with every outcome, address checks, trusted proxies, cross-site refusals, no password or typed account in the log), the shell pages (header, user box, language, login page notices, redirects, escaping, files), the contrast of both palettes.
- The real binary was run with a Python configuration folder, a stand-in for the Python backend and the sign-on service that uses the real `pickle` and `zlib` (`verify_user`, `hc_user_processes`, `sso_result`, the sign-on page), and the scratch PostgreSQL 18 with demo data. Driven in headless Chrome: landing, login (wrong and right password, by e-mail), redirect back, process picker, scans, manual change, acknowledgement and saving, sign-out, the Windows popup flow (signed in, no Windows user, unknown account), the link from Dash (and its second use), the session ending while the page is open, a 620 px window, the four combinations of theme and language, pictures (placeholder size, real picture, broken link). No console error except the expected 401 / 409 answers.

Done (2026-11-20, version 0.1.2):

- New unit tests: a Windows sign-in that started from a loopback address matches an address of this computer (direct, and through nginx on the same computer as in the log of the development machine) and is refused for the address of another computer or no address, and a browser on another computer gets no relaxation (`ssoflow_test.go`; the first test fails when the rule is switched off); the header of a trusted and of a not trusted front server (`clientip_test.go`); the errors of the Python API (status 204, the address in the message, no credentials in it: `client_test.go`); where the API address comes from (`config_test.go`).
- The real 0.1.2 binary on the development machine, with scratch configuration on port 18750, the real local Python API, the local Redis and a stand-in for the sign-on service that answers with the real `pickle` and `zlib`. With the API taken from the patch file (`api_server` = `prod_eng`) `api/me` answered 502 and the log named the API and status 204; with `dash_api_url` set to `http://localhost/api` it answered 200 with the 10 processes of the account. A Windows sign-in started from `localhost` ended `ok` when the sign-on service reported the LAN address of the computer, and `mismatch` when it reported another computer.
- The nginx configuration of the development machine with a front server in front: an isolated nginx 1.24 instance made from `config/nginx_sgmeslt595.conf` in front of the real binary. Only the listen port, the port of dash_go and the trusted address were changed, because requests cannot be sent from the real `prod_eng` address; a loopback address played the front server. With the three directives the Windows sign-in through the front server ended `ok` and the log showed the address of the browser. Without them it was refused with `started_from` = the front server and `forwarded="<browser>, <front server>"`. In a second run of the same kind with a stub backend that echoes the headers, a forged left part of `X-Forwarded-For` was ignored, and a request from an address that is not trusted kept its own address.
- The production chain, s1 → s2 → dash_go (the `/dash_go/` routes of `config/nginx_prod_eng.conf` and `config/nginx_sgmescimai.conf`): `nginx -t` on both complete files, and two isolated nginx instances made from them (only the listen ports, the port of dash_go and the address that s2 trusts as s1 were changed) in front of the real 0.1.2 binary, the real local Python API and a stand-in for the sign-on service. Through the chain: the redirect of `/dash_go`, the login page, static files, the Windows sign-in and the permission check (`api/me`: 200, 10 processes) worked; a forged `X-Forwarded-For` sent through s1 was ignored; a browser that goes to s2 without s1 worked. A request sent to s2 without s1 that claimed the address of another person was refused (`mismatch`); with the location changed to `$http_x_forwarded_for` (the idiom of the other locations of s2) the same request was accepted and signed in as the other person. So the `/dash_go/` location of s2 uses `$proxy_add_x_forwarded_for`.

Done (2026-11-20, version 0.1.3):

- New unit tests: the Back button is on the pages of a module, goes to `/`, is in both languages and is not on the landing page or the login page (`shell_test.go`); the catalogs have `ui.back`, `ui.back_title` and `err.updating` in both languages (`catalog_test.go`, which also finds every key the code uses); the 503 of the gate in the language of the request (`internal/core/server/gate_test.go`). The contrast of the accent on the header was already covered by `palette_test.go`.
- The real 0.1.3 binary (scratch configuration on port 18754, the real Python configuration and API) in the integrated browser of the editor, signed in with a hand-over link made with a test secret: the header of the module page in the light and the dark theme, in English and in Chinese, and in a window 390 px wide. The name of the module is in the blue of the theme (computed `rgb(11, 99, 197)` in the light theme), 20.8 px, weight 600; the Back button is next to it with the arrow icon; at 390 px the name is hidden and the button stays next to the logo, and the page does not scroll sideways. A click on Back went to `/` of the same host and port.

Done (2026-11-23, version 0.1.5):

- New unit tests: header places Docs and Guide buttons between the language switch and the user box / Sign in link, hides them and `tour.js` on `/dash_go/login`, serves `/dash_go/_shell/tour.js`, serves `/dash_go/help` in both languages with matching section IDs and without `data-tour-auto` (`shell_test.go`); every `{{.Tour}}` step in shell templates has `.title` and `.text` in both languages, no `tour.*` key is unused, `tour.*` stays out of `#dg-i18n`, and every `t('ui.*')` key used by `tour.js` is sent to the browser (`catalog_test.go`); `palette_test.go`, `go vet ./...` and `go test ./...` pass across all packages.
- End-to-end headless Chrome verification (Playwright against an in-process `httptest.Server` running the real shell, CSP headers and `hc_scan` handlers with in-memory test data): visual left-to-right button order (`lang < docs < guide < sign-in / userbox`); landing page first-visit auto-tour, keyboard navigation (`→` / `←` / `Esc`), no auto-tour on reload, manual replay via Guide button, Docs button opening `/dash_go/help` in a new tab without auto-tour; login page hiding both buttons; Chinese language, dark theme and 390 px narrow screen (docked tour bubble, zero horizontal overflow). No JS or CSP errors.

Done (2026-11-28, version 0.2.4):

- New unit and cross-instance tests (`internal/core/auth/redis_test.go`, `login_test.go`, `auth_test.go`, `config_test.go`, `shell_test.go`): two simulated `dash_go` instances (s2 and s3) sharing one `miniredis` verify cross-instance sessions (password, Dash handover link, Windows SSO), 24-hour sliding expiration refreshing both Redis TTL and browser cookie `MaxAge` (staying alive across 18h+18h active windows and expiring after 25h idle), single refreshed cookie per shell page render, immediate server-side revocation across all instances on logout, unauthenticated (401) behavior with logged warning (never leaking session ID) when Redis is down, rejection of legacy dot-separated signed session cookies and damaged Redis session payloads, cross-instance Windows SSO flows (`/auth/sso/start` on s2, `/auth/sso/poll` on s3, 1s poll gap, per-IP limit of 5, single-use drop, `PendingSignIns() == 0` with Redis, `unavailable` on Redis outage), and cross-instance password rate limits (IP cap of 5 and account cap of 10 split across s2/s3, SHA-256 hashed account keys never leaking typed usernames in Redis, window expiry, 503 fail-closed on Redis outage). `go vet ./...`, `go vet -tags devserver ./...` and `go test -count=1 ./...` pass across all packages.

**Not verified:**

- The real Redis server on s2 and real multi-instance deployment across s2 and s3 behind s1 nginx (Redis session/SSO/limit stores were verified against in-process `miniredis` with Redis 2.6+-compatible Lua scripts).

- The real Python backend (`verify_user`, `sso_result` as deployed; `hc_user_processes` was tried against the Python backend of the development machine in 0.1.2, not against s2), the real Windows sign-on service and a real Windows user.
- The real nginx of s1 and s2 (their files were tested with isolated instances, see above, but are not deployed), https, and the real `prod_eng` in front of the development machine (its address was simulated; the nginx that is running on the development machine after the new file is copied and reloaded).
- The 480 x 320 placeholder of the real static endpoint (taken from the TimeVault records) and real photos.
- Browsers other than Chrome (Edge is expected to behave the same), a real scanner.
- A review of the Chinese wording (including the 0.1.5 user guides and tour texts) by the users.
- The Back button behind the nginx of s1 and s2 (0.1.3): the link is `/` and was clicked against the scratch service only, which answers 404 there because it serves `/dash_go/` only.
- Blocked `localStorage` behaviour (tested by code review only, not simulated in a restricted browser profile).

---

# 中文

**更新日期：** 2026-11-28（dash_go 版本 0.2.4）。本文描述 `internal/core/shell`、`internal/core/i18n`、`internal/core/auth`、`internal/core/hc`、`internal/core/pyobj` 的当前代码，以及 `internal/core/dashapi` 中对 Python 的调用。0.1.2 版修改了 Windows 登录的一条规则（运行 dash_go 的那台电脑上的浏览器，第 7.2 节），并把正在使用的 Python API 的地址写进了日志（第 8 节和第 10 节）。0.1.3 版让顶栏中的模块名称更醒目，并在它旁边加了**返回**按钮（第 2 节）；同一版本的自动更新见 [auto_update.md](auto_update.md)。0.1.5 版在顶栏的语言切换和用户框之间加入了**文档**和**导览**按钮、中英文 `/dash_go/help` 使用手册页面以及交互式分步导览（`tour.js`，支持首次访问经 `localStorage` 自动引导，第 2 节）。0.1.9 版新增模块图标支持（首页卡片的 `module.Info.Icon` 和顶栏模块名旁的 `PageSpec.AppIcon`，率先为 `hc_scan` 加入了 `scanner` 扫码图标，并于 2026-11-26 更新为四角取景框包裹竖向条形码的样式），并移除了各模块独立的版本号显示与字段，版本号统一由仓库级 `VERSION` 维护。0.2.0 版为 `hc_budget` 新增 `budget` 图标（人形剪影搭配六齿设置齿轮），将共享的 HC 工序选择器抽入 shell（`shell.css` 的 `.screen` / `.process-buttons`、`layout.html` 的 `message_screen` 模板，以及 `shell.js` 中经 `sessionStorage` 键 `hc.process` 在 `hc_scan` 与 `hc_budget` 间共享工序的 `dashGo.picker`），增加顶层 `dash_data_dir` 配置，并提取 `internal/core/hc` 与 `dashapi.Profile.Type` / `IsAdmin()`。0.2.4 版将会话（24 小时滑动续期，退出登录立即在服务端吊销）、Windows SSO 待完成流程及密码登录失败限流全部迁入共享 Redis，使多台 `dash_go` 实例（s2 与 s3）可在加权轮询负载均衡下运行而无需 `ip_hash`（第 7–12 节）。第 12 节说明哪些内容已验证、哪些没有验证。

## 1. 概述

0.1.1 版给 dash_go 的每个页面加上了统一的外框，即 **shell**：

- 顶栏：logo、模块名称及返回按钮、浅色 / 深色主题按钮、EN | 中文 语言切换、当前登录用户（头像或首字母、姓名）和退出登录按钮；未登录时显示登录按钮；
- 浅色和深色两套主题（深色就是 0.1.0 版的样子），由使用者选择，浏览器记住；
- 所有文字都有中英文，包括服务器生成的警告和错误；
- dash_go 自带的登录页（账号密码，或 Windows 登录），与 Dash 生成的一次性链接并存。

shell 放在 `internal/core` 中，所以每个模块（以及首页 `/dash_go/`）都用同一个顶栏；模块只提供自己页面的内容和文字。shell 的文件在 `/dash_go/_shell/` 之下提供。

| 部分 | 位置 | 内容 |
|---|---|---|
| 页面和顶栏 | `internal/core/shell`（`web/templates`、`web/assets`） | 由服务器渲染（`html/template`），所以语言、用户和首次主题在任何脚本运行之前就是对的。 |
| 文字 | `internal/core/i18n`（`catalog/en.json`、`catalog/zh.json`）和 `internal/mods/<module>/i18n` | 每种语言一个扁平的 JSON 文件，Go 代码和浏览器共用。 |
| 登录 | `internal/core/auth` | 交接链接、密码、Windows 登录、会话 cookie、限流。 |
| 对 Python 的调用 | `internal/core/dashapi`、`internal/core/pyobj` | `verify_user`、`hc_user_processes`、`sso_result`；pickle 读取器。 |

## 2. 顶栏

```text
[logo CIM EDGE]  | HC 扫码分派  [<- 返回]          (太阳/月亮)  [EN|中文]  (书本) (指南针)  (头像) 姓名  [退出]  <- shell，每个页面都有
 Winder  [日期] [AM|PM] [切换工序]                  排班 20  已分派 14  请假 3  待分派 3  待确认 2 ...        <- 模块自己的页面栏
```

- logo 是指向 `/dash_go/` 的链接，旁边显示模块名称（页面的 `AppKey`），自 0.1.9 起可在名称前显示模块图标（`AppIcon`）。自 0.1.3 起它使用主题的强调色（`--accent`：浅色主题 `#0b63c5`，深色主题 `#4da3ff`），1.3 rem，字重 600；之前它是暗淡的灰色，在深色顶栏上太不显眼。强调色在顶栏上的对比度由 `palette_test.go` 覆盖。
- **返回**按钮（自 0.1.3 起，只出现在模块的页面上：首页和登录页没有模块名称，也没有这个按钮）是链接 `/`，即打开页面时所在服务器的根路径，而不是 `dash_home`。链接里没有主机，所以它跟随使用者输入的地址：开发机上是 `http://127.0.0.1/`，s2 上是 `http://<s2>/`，经由 s1 打开页面时则是 s1 的主机。它的文字是 `ui.back` 和 `ui.back_title`。
- 主题按钮显示当前主题的图标：浅色主题是太阳，深色主题是月亮。
- 语言切换是一个链接：设置 cookie 后回到同一页面（包括查询字符串）。
- **文档**（书本图标）和**导览**（指南针图标）按钮位于语言切换与用户框 / 登录按钮之间（自 0.1.5 起，参考 TimeVault 项目实现；登录页不显示）：
  - **文档**（`PageSpec.HelpHref`，`target="_blank" rel="noopener"`）在新标签页打开当前页面的中英文使用手册（首页对应 `/dash_go/help`，模块页面对应 `/dash_go/<module>/help`）。手册页跟随当前语言与浅色 / 深色主题，带左侧吸顶目录（`.dg-doc`），打开时不会自动弹出导览遮罩。
  - **导览**（`PageSpec.Guide`，`web/assets/tour.js`）启动只读聚光灯式分步导览，遍历当前页面所有可见的 `data-tour` 元素（由服务端通过 `{{.Tour "<key>"}}` 渲染，读取词条 `<key>.title` 与 `<key>.text`；页面主体步骤在前，顶栏步骤在后）。支持**下一步** / **上一步** / **完成**、键盘快捷键（`→`、`←`、`Esc` 及对话框内 `Tab` 焦点循环）、吸顶栏避让（`data-tour-sticky`）以及窄屏（`<= 640px`）底部停靠。浏览器首次访问页面时会自动启动一次导览（通过 `localStorage` 键 `dash_go.tour.seen.<key>` 记录，由 `PageSpec.TourAutoKey` 或 `dashGo.tour.autoStart(key, { skipHeaderIfSeen: true })` 触发；若 `localStorage` 不可用则跳过自动启动以避免循环弹出）。随时点击顶栏导览按钮可重看完整导览。
- 用户框显示头像（第 6 节）或首字母、姓名（超过 11 rem 用省略号截断，悬停提示是“姓名（工号）”）和退出按钮。没有会话的访问者看到的是 **登录** 按钮（登录页本身不显示）。
- 页面打开期间会话结束时，页面脚本把用户框换成登录按钮，并显示“登录已过期”和一个去登录页的按钮。
- 窄于 760 px 时隐藏模块名称（返回按钮保留），logo 缩成只剩图标；窄于 480 px 时也隐藏用户名。顶栏会换行而不是出现滚动条。

## 3. Logo

设计稿在 `docs/CIM_Edge_design/`（`CIM_EDGE_editable.svg` 是路径的来源）。顶栏使用由这些路径做成的**横向组合**，没有增加新的图形：图标（带电路走线的 E）加上 `CIM EDGE` 字样，字样放大 1.65 倍并与图标垂直居中。原稿中的服务器图标和两条横线被省略（36 px 高度下看不清）。组合是内嵌 SVG（`web/templates/logo.html`），颜色是 CSS 变量，所以主题可以改变它们。`web/assets/favicon.svg` 只有图标，颜色在浅色和深色标签栏上都看得清。

logo 的深蓝（`#00448f`）在深色顶栏上对比度只有 1.9:1。深色主题中 `--logo-navy` 是 `#4f8fe0`（5.4:1），青色不变。测试（`palette_test.go`）保证 logo 在两个主题中都不低于 3:1。

需要更换 logo 时：替换 `logo.html` 和 `favicon.svg` 中的路径（组合的 viewBox 由路径的包围盒计算得出），并检查两个主题。

## 4. 浅色和深色主题

- `theme-init.js` 在 `<head>` 中、样式表之前运行（CSP 不允许内联脚本），在 `<html>` 上设置 `data-theme="light|dark"`：优先用 `localStorage` 键 `dash_go.theme` 中保存的选择，否则用系统设置（`prefers-color-scheme`）。没有保存的选择时，页面跟随系统，页面打开期间系统切换也会跟着变。
- 按钮切换主题并保存。`localStorage` 被禁用只意味着选择在刷新后不保留。
- 所有颜色都是 `shell.css` 中的 CSS 变量：`:root` 是浅色主题，`:root[data-theme="dark"]` 是深色主题（0.1.0 版的配色）。模块的样式表只用这些变量，不自己定义颜色。
- `palette_test.go` 从 `shell.css` 读取两套配色，检查每个文字颜色在它所用的每个背景上都可读（文字 4.5:1，logo 3:1）。改动颜色而导致某个主题不可读，测试会失败。

## 5. 语言和文字

### 5.1 词条文件

人读到的每一段文字都在词条文件中，不写在 Go、JavaScript 或 HTML 里：

- `internal/core/i18n/catalog/{en,zh}.json`：核心部分（`ui.*` 顶栏和通用文字，`login.*` 登录页，`err.*` 核心的错误）。
- `internal/mods/<module>/i18n/{en,zh}.json`：模块的文字，每个键都带模块的前缀（hc_scan：`hc.*`）。模块在 `init()` 中用 `i18n.Register` 注册，所以不含该模块的发布包也不含这些文字。
- 文件是文字的扁平 JSON 对象。占位符是 `{name}`（小写字母、数字、下划线），不是 `%s`，因为中文的语序不同。没有值的占位符会原样显示，缺失的键显示键本身，缺失的中文回退到英文。
- hc_scan 的键族：`hc.ui.*` 页面及其脚本（发给浏览器），`hc.err.*` 错误，`hc.flag.*` 一行的警告，`hc.reason.*` 一行为什么被分派或没有分派，`hc.msg.*` 一次扫描或保存的消息，`hc.skip.*` 一行为什么没有保存，`hc.step.*` 失败的请求当时在做什么。页面只会收到它要求的前缀（`ui.` 总是发送，登录页发送 `login.`，hc_scan 页面发送 `hc.ui.`），其余的留在服务器上。

### 5.2 用哪种语言

cookie `dash_go_lang`（`en` 或 `zh`，路径 `/dash_go/`，一年）优先；没有时取 `Accept-Language` 头中第一个是中文或英文的语言；否则用英文。语言切换是链接 `/dash_go/lang?set=zh&next=<页面>`：设置 cookie 并重定向（303）到 `next`，`next` 必须是 base path 之下的页面。服务器把语言放进每个请求的 context（`i18n.Middleware`），所以 HTTP 层之下的代码用 `i18n.T(ctx, key, i18n.P{...})` 翻译。

### 5.3 草稿保存的是消息，不是句子

Redis 中草稿行的警告和原因保存为词条键加上它的值（`{"key": "hc.flag.roster_other_process", "args": {...}}`），看板再按请求的语言把它们变成句子。所以页面是英文时扫描的行，切换后显示为中文。0.1.0 版留在 Redis 中的草稿（TTL 48 小时）是英文句子、没有消息，会原样显示。

### 5.4 中文措辞

`HC`、`MTR`、`Lv1` 到 `Lv3`、`AM` / `PM`、机台、工序和人员的名称，以及直接类型的取值 `Direct` 和 `Shift Leader` 保持原样。machine = 机台，activity = 活动，process = 工序，shift leader = 领班，draft = 草稿，Confirm = 确认。措辞由开发者写成，交给使用者审阅：修改只是修改 `zh.json` 中的文字。

### 5.5 新增文字或模块

1. 在词条文件的 `en.json` 和 `zh.json` 中添加键，使用模块的前缀。
2. 使用它：Go 用 `i18n.T(ctx, "hc.msg.x", i18n.P{"name": n})`（保存的消息用 `msg(...)`），模板用 `{{.T "hc.ui.x"}}`，脚本用 `t('hc.ui.x', { name })`。
3. `go test ./...` 检查其余的内容：两种语言的键和占位符相同，代码中出现的每个键都存在，代码传入的值恰好是文字中的占位符，每个 `s.internal("...")` 步骤都有文字，每个 `hc.ui.*` 文字都被使用，词条中没有无人使用的文字（模块和 shell 的 `catalog_test.go`）。

## 6. 用户框和头像

- 头像地址是 `photo_url_template`（默认 `/api/static/hr/{ee_code}.webp`，即 Python 应用的静态文件夹，经 nginx 的 `/api` location 访问）。用户框、徽章和机台卡片用同一个函数（`dashGo.avatar`）绘制：先显示圆圈里的首字母，图片加载成功后再换成图片。
- 据报告（TimeVault 项目的 ADR 0015，在该项目中对真实应用检查过），Dash 的静态文件接口对不存在的文件回答 **状态 202 加一张 480 x 320 像素的占位图**，从不回答“找不到”。所以恰好这个尺寸的图片被当作“没有头像”，保留首字母。加载失败（出错）的头像在页面存活的 10 分钟内不再重复请求。本项目没有在真实服务器上检查这个回答；480 x 320 的规则只用生成的图片测试过。

## 7. 登录

| 方式 | 做法 |
|---|---|
| **Dash 的链接** | Dash 的 **Scan Assign** 按钮生成带签名的一次性链接（`GET /dash_go/auth/sso?t=...&next=...`，见 Dash 仓库中的 `docs/hc_scan_go_link.md`）。没有改变。无法使用的链接（无效、已过期、已使用、nonce 存储不可用）现在会跳到登录页并说明原因（`?error=link_invalid`、`link_expired`、`link_used`、`unavailable`），不再是死胡同。 |
| **账号和密码** | 登录页要求输入账号（工号、姓名或邮箱）和密码。`POST /dash_go/auth/login` `{username, password, next}` 询问 Python 后端（先 `verify_user`，再用 `hc_user_processes` 取姓名和工号）。密码只是传递，不保存也不记日志。用 `login_password: false` 关闭。 |
| **Windows（SSO）** | 按钮打开一个窗口，显示公司登录服务的页面（`http://<sso_server>/sso/api/login?token=<token>`），与 Dash 应用使用的是同一个。令牌由 dash_go 自己签发，连同浏览器的地址记住 5 分钟，并每 2 秒用 `POST /dash_go/auth/sso/poll` 询问，直到服务记录了 Windows 用户。结果从后端读取一次（`sso_result`），并对应到该工号的有效 Dash 账号。只有设置了 `sso_server` 才提供。 |

任何一种方式之后（自 0.2.4 起），服务都会在 Redis 中创建服务端会话（`dash_go:session:<id>`，JSON `{"ee", "name"}`）并设置不透明随机值会话 cookie `dash_go_session`（256 位随机 base64url ID、HttpOnly、SameSite Lax、https 之后为 `Secure`）。过期方式为**滑动续期**（`session_hours`，默认 **24** 小时）：每次已认证的活跃请求都会同时刷新 Redis 中的 TTL（经 Lua `GET` + `PEXPIRE` 原子执行，兼容 Redis 2.6+）以及浏览器 cookie 的 `MaxAge`。验证期间若 Redis 不可用，服务会记录告警日志（绝不记录会话 ID）并按未登录处理（API 返回 401，页面跳转登录页）。**退出登录**（`POST /dash_go/auth/logout`）会立即从 Redis 删除该会话键并清除浏览器 cookie，旧 cookie 即使被复制也会在所有实例上立刻失效；浏览器中 Dash 应用的登录不受影响。0.2.3 及更早版本签发的带点号签名 cookie 会在本地直接拒绝而不查 Redis（升级至 0.2.4 时现有用户重新登录一次即可）。未配置 Redis 时（`redis.host` 为空，例如单元测试或无 Redis 的本地开发），会话、SSO 流程与登录限流自动回退到进程内存并在启动时记录警告。没有会话的人打开 `/dash_go/hc_scan/` 会被带到 `/dash_go/login?next=...`，登录后回到原页面。

### 7.1 路由

| 请求 | 用途 |
|---|---|
| `GET /dash_go/` | 带模块列表的首页（公开）。 |
| `GET /dash_go/help` | 通用使用手册（公开，中英文，列出已编入的模块并链接到各自的使用手册）。 |
| `GET /dash_go/login?next=&error=&signed_out=&expired=` | 登录页。已登录的人会被带到 `next`。 |
| `GET /dash_go/lang?set=en\|zh&next=` | 语言切换。 |
| `GET /dash_go/_shell/...` | shell 的文件（`shell.css`、`shell.js`、`tour.js`、`theme-init.js`、`login.js`、`icons.svg`、`favicon.svg`）。 |
| `GET /dash_go/auth/sso?t=&next=` | Dash 的一次性链接。 |
| `POST /dash_go/auth/login`、`.../auth/logout`、`.../auth/sso/start`、`.../auth/sso/poll` | 登录页的 JSON 接口。 |

### 7.2 登录的保护措施

- JSON 接口需要请求头 `X-Requested-With: dash_go`（跨站表单发不出它），并拒绝浏览器标记为 `Sec-Fetch-Site: cross-site` 的请求，以及 `Origin` 指向其他主机的请求（比较时不含端口，和浏览器判断同站的方式一致）。退出登录也有同样的要求，所以一个链接不能让别人退出登录。
- **失败密码登录的限制**：10 分钟内每个客户端地址 5 次、每个账号 10 次（账号比较时不分大小写，邮箱地址只比较 @ 之前的部分）。回答是 429，并带 `Retry-After`。登录成功会清除该账号的失败记录，并还给该地址一个名额；用户目录不可用不会记到账号头上。自 0.2.4 起，配置了 Redis 时这些计数保存在 Redis 中（`dash_go:auth:limit:ip:<ip>` 和 `dash_go:auth:limit:acct:<sha256>`，对规范化账号做 SHA-256 哈希，确保误输进账号栏的密码绝不会明文写入 Redis），多台实例执行统一限流；密码登录时若 Redis 不可用则按失败关闭原则返回 503（`unavailable`）。
- 账号不存在、密码错误和账号已停用，提示相同（后端对这三种情况都回答 202）。
- **Windows 登录**：只接受本服务签发的令牌（24 个随机字符，120 位），一个结果只能使用一次，每个地址最多同时等待 5 个、总共最多 2000 个，每个流程每秒最多查看一次结果。自 0.2.4 起，配置了 Redis 时这些等待中的流程保存在 Redis 中（`dash_go:sso:flow:<token>`、`dash_go:sso:flows`、`dash_go:sso:flows:ip:<ip>`，TTL 5 分钟），`/auth/sso/start` 和 `/auth/sso/poll` 可以落在 nginx 后面的不同实例上，并在重启后保留。启动流程的浏览器的地址，必须等于登录服务记录的地址（`sso_require_same_ip`，默认开启）：这样可以阻止“强加登录”，即攻击者启动一个流程，让受害者用攻击者的令牌打开登录链接，然后轮询得到受害者的身份。浏览器的地址是连接的地址；如果连接来自 `trusted_proxies` 之一，则取 `X-Forwarded-For` 中最右边的、不是受信任代理的地址（所以浏览器不能自己选择地址）。**运行 dash_go 的那台电脑上的浏览器**是唯一的例外（0.1.2）：以 `localhost` 打开时，它对 dash_go 来说来自回环地址（`127.0.0.1`、`::1`），而登录服务记录的是这台电脑的地址。这是同一台电脑上的同一个浏览器，所以当登录流程从回环地址启动时，这台电脑任何网络接口的地址都算作同一个地址（`internal/core/auth/ssoflow.go` 中的 `sameBrowser`）。另一台电脑上的浏览器绝不会来自回环地址（`X-Forwarded-For` 的左侧不被相信），所以其他登录都没有被放宽。0.1.2 之前，从 `localhost` 发起的登录总是被拒绝。
- 不记录任何机密：密码从不写入日志，输入的账号只以短哈希形式写入（`acct=`），因为有人可能把密码输进账号栏。被拒绝的 Windows 登录会记录被比较的两个地址、请求是怎样到达服务器的（`connection`、`trusted_proxy`、`forwarded`）以及一条提示。
- `next` 只接受 base path 之下的本地路径。内容安全策略只允许脚本、样式和图片来自服务本身，并禁止 `base`、插件以及向其他地方提交的表单。

### 7.3 对 Python 应用的调用

| 调用 | Authorization | 用途 |
|---|---|---|
| `POST <api>/user/hc_scan` `{"func":"verify_user","user","password"}` | `md5("<api.salt_3>_<yymmddHH>")` | 200 = 正确且有效；202 = 否。 |
| `POST <api>/user/hc_scan` `{"func":"hc_user_processes","user"}` | 同上 | 账号的姓名、工号和 HC process（也是页面的权限检查）。 |
| `POST http://<sso_server>/api/sso_result/anything`，请求体 `zlib(pickle({"method":"get","func":"sso_result","sso_token":...}))`，`Content-Type: text/plain` | `md5(<api.salt_2>)_md5(<yymmddHH>)` | 令牌的结果：`{"status": "ok", "ee_code", "windows_user", "ip", "ts"}`，或 `{"status": "pending"}`。后端在读取时把它删除。 |

`<api>` 是 `dash_api_url`，默认 `http://<api_server>/api`。签名与 Python 代码一样使用服务器的本地时间，所以两台主机的时钟和时区必须一致。登录服务接口的回答是 `zlib.compress(pickle.dumps(dict))`：pickle 可以在加载它的程序里运行代码，所以 `internal/core/pyobj` 从不加载它，而是把操作码解释成无害的数据（解码器来自 TimeVault 项目的严格解码器，对大小、深度和操作码数量有限制），只接受普通值，拒绝其他一切。请求体用协议 2 写出。两个方向都用 Python 真实的 `pickle` 和 `zlib` 测试过。

## 8. 配置

| 键 | 文件 | 默认值 | 含义 |
|---|---|---|---|
| `session_hours` | `dash_go.json` | `24` | 空闲会话存活小时数（1–168）。自 0.2.4 起采用滑动续期：每次活跃请求都会同时把 Redis 会话 TTL 与浏览器 cookie 的 `MaxAge` 刷新为该时长。 |
| `photo_url_template` | `dash_go.json` | `/api/static/hr/{ee_code}.webp` | 人员头像（也是同名模块设置的默认值）。 |
| `login_password` | `dash_go.json` | `true` | 登录页要求输入账号和密码。 |
| `sso_require_same_ip` | `dash_go.json` | `true` | Windows 登录：登录服务必须看到启动流程的那个地址。只有在没有代理能传递浏览器地址时才关闭。 |
| `trusted_proxies` | `dash_go.json` | `["127.0.0.1", "::1"]` | 其 `X-Forwarded-For` 会被信任的反向代理（地址或 CIDR 范围）。`[]` 表示谁都不信任。 |
| `dash_api_url` | `dash_go.json` | `http://<api_server>/api` | Python API 的根。既没有它也没有 `api_server` 时，登录页关闭（Dash 的链接仍然可用）。`api_server` 来自 Python 配置，**包括本机的 patch 文件**：在开发机上它可能是生产服务器，而生产服务器可能比 dash_go 旧（第 10 节）。日志和 `dash_go.exe -check` 会说明用的是哪个 API。 |
| `sso_server` | Python `config.json`（或 `dash_go.json`） | 无 | Windows 登录服务的主机（`host`、`host:port` 或 `http(s)://host[:port]`）；没有它则 Windows 登录关闭。 |
| `api.salt_2` | Python `config.json` | 无 | 登录结果接口的密钥；设置了 `sso_server` 时必填（没有它 `dash_go.exe` 拒绝启动）。环境变量 `DASH_GO_API_SALT_2` 优先。 |
| `api.salt_3` | Python `config.json` | 无 | `/api/user` 接口和交接链接的密钥（没有改变）。 |
| `dash_home` | `dash_go.json` | `/ie/` | 登录页链接到 Dash 应用。 |

`dash_go.exe -check` 会打印密码登录和 Windows 登录是否开启，自 0.2.4 起还会打印会话存储与滑动时长（`session store redis (sliding 24h)` 或 `memory`）。自 0.1.2 起，日志（所以 `-check` 也是）还会说明登录和各模块询问的是哪个 Python API，以及它来自哪个键：`the Dash API of the sign-in url=http://localhost/api from=dash_api_url`。自 0.1.3 起，`-check` 还会说明自动更新是否开启。它的键（`update_url`、`update_interval_minutes`、`update_restart`）见 [auto_update.md](auto_update.md) 第 6 节。自 0.1.6 起，`-check` 还会用一个不读取任何东西的带签名调用去询问该 API（`Dash API (sign-in)` 这一行），所以错误的 `api.salt_3`（`dash api rejected the request signature`）或没有这些函数的后端，会在有人尝试登录之前就在那里显示出来；其他检查和退出码见 [hc_scan_page.md](hc_scan_page.md) 第 11 节。

## 9. 部署

- 不需要新的 location：`/dash_go/` 已经覆盖 `_shell`、`login`、`lang` 和 `auth`。nginx 示例设置了 `X-Forwarded-For $proxy_add_x_forwarded_for`，登录会用到它；默认的 `trusted_proxies` 信任同一台电脑上的 nginx。
- **前置代理后多实例部署（s1 → s2 + s3，自 0.2.4 起）**：由于会话、Dash 一次性交接 nonce、Windows SSO 流程、密码限流、`hc_scan` 草稿及 `hc_scan` 会话写锁全部存放在共享 Redis 中，s1（`prod_eng`）可以直接按加权轮询把 `/dash_go/` 分流到 s2 和 s3（无需 `ip_hash`）。s2 和 s3 的 `redis` 配置必须指向**完全相同的 Redis 主机和 DB**（s2 的 Redis），并共享相同的 `api.salt_3`。
- **两层代理**（运行 dash_go 的服务器前面还有一台服务器）：紧挨着 dash_go 的 nginx（**s2 和 s3 两台都要配**）看到的是前面那台服务器的地址，而不是浏览器的。要么把前面的服务器列入 `trusted_proxies`（最右边的、不受信任的地址优先），要么让各后端 nginx 使用 `set_real_ip_from <前面的服务器>; real_ip_header X-Forwarded-For; real_ip_recursive on;`。两者都没有时，落到该服务器的每次 Windows 登录都会因 `address_mismatch` 被拒绝；日志行会说明比较了哪些地址。`sso_require_same_ip: false` 可以让它工作，但会失去对强加登录的保护。示例：开发机 `sgmeslt595` 位于前置服务器 `prod_eng` 之后。Dash 仓库的 `config/nginx_sgmeslt595.conf` 在 `http` 块中用 `prod_eng` 的地址设置了这三条指令，与 s2 的 `nginx_sgmescimai.conf` 相同（s2 以同样的方式信任 `prod_eng`）。nginx 只相信来自该地址的请求头，所以浏览器不能自己选择地址；`/tv/` 仍保留它自己针对 s2 的 `set_real_ip_from`。正在运行的 nginx 读取的是该文件自己的副本：复制过去，运行 `nginx -t`，再 reload nginx。**在生产环境**，链路是 浏览器 → s1（`prod_eng`，前置入口）→ s2 / s3 的 nginx → dash_go。s2 和 s3 的 nginx 都信任来自 s1 的 `X-Forwarded-For`（`set_real_ip_from`），s1、s2 和 s3 都用 `X-Forwarded-For $proxy_add_x_forwarded_for` 转发 `/dash_go/`，dash_go 中不需要设置任何东西（`trusted_proxies` 保持默认）。s2 和 s3 的 `/dash_go/` location 都不能把请求头原样传递（`$http_x_forwarded_for`，即 s2 其他 location 的写法）：不经过 s1 直接到达 s2 或 s3 的请求会带着它自己声称的地址，dash_go 会相信它（已测试，第 12 节）。s1 没有为 `/dash_go/` 设置本机 backup，因为 dash_go 运行在 s2 和 s3 上。示例见 `deploy/nginx_dash_go.conf.example`。
- **运行 dash_go 的那台电脑上的浏览器**（开发机，`http://localhost/...`）：Windows 登录会匹配这台电脑的地址，见第 7.2 节。不需要任何配置。
- **开发机的 Python API。** dash_go 询问 `dash_api_url`，默认是 `http://<api_server>/api`，而开发机的 patch 文件常常为了 Python 应用自己的调用，把 `api_server` 设成生产服务器。在那里请在 `dash_go.json` 中设置 `dash_api_url`（例如 `http://localhost/api`）；否则登录和权限检查会询问一个可能还没有 `verify_user` 和 `hc_user_processes` 函数的后端（第 10 节）。
- 登录服务由**浏览器**打开（弹出窗口），由 **s2 和 s3** 通过它的后端读取：s2 和 s3 都必须能访问 `http://<sso_server>/api/sso_result/anything`。
- 密码从浏览器传到 dash_go，再从 dash_go 传到 Python 后端，使用的是与公司其他应用相同的 http。站点有 https 的地方，浏览器到 nginx 之间应使用 https；这时 cookie 会自动带上 `Secure`（`X-Forwarded-Proto`）。

## 10. 故障排查

- **登录页显示“本服务器没有设置在此页面登录”。** 既没有开启 `login_password` 也没有开启 Windows 登录，或者 `dash_api_url` 和 `api_server` 都没有设置。`dash_go.exe -check` 和日志开头几行会说明是哪一种。
- **密码正确却提示“账号或密码不正确”。** 该用户可能已被停用（后端对所有失败回答相同），或者 s2/s3 与后端的时钟相差超过签名的小时（日志：`dash api rejected the request signature`）。
- **Windows 登录提示“无法与当前浏览器对应上”。** 两个地址不同。日志行 `single sign-on refused` 会说明是哪两个：`started_from` 是 dash_go 当作浏览器的地址，`sso_address` 是登录服务看到的地址，`connection`、`trusted_proxy` 和 `forwarded` 说明请求是怎样到达的。
  - `started_from` 是某台服务器的地址，且 `forwarded` 以它结尾：紧挨着 dash_go 的 nginx 前面的代理隐藏了浏览器。见第 9 节：在那个 nginx 中使用 `set_real_ip_from`，或使用 `trusted_proxies`。`prod_eng` 转发到一台其 nginx 不信任它的服务器时，就是这种情况。
  - `trusted_proxy=false` 且设置了 `forwarded`：连接过来的代理不在 `trusted_proxies` 中。
  - `started_from` 是 `127.0.0.1` 或 `::1`：页面是在运行 dash_go 的那台电脑上打开的（`http://localhost/...`）。2026-11-20 在开发机上发现：`started_from=127.0.0.1`、`forwarded=::1`、`sso_address=10.135.36.89`，即这台电脑的局域网地址；它的 nginx 前面没有代理，所以不是代理设置的问题。0.1.1 为止这种情况总是被拒绝；自 0.1.2 起，只要 `sso_address` 是这台电脑的地址就匹配。如果仍然被拒绝，说明 `sso_address` 属于另一台电脑（浏览器的代理、VPN）。
  - 地址匹配，但页面随后提示现在无法完成登录（日志：`single sign-on failed: the user directory cannot be reached`，并带有 `answered status 204`）：`dash_api_url` 或 `api_server` 指向的 Python API 是错的，或者比 dash_go 旧。见 [hc_scan_page.md](hc_scan_page.md) 第 11 节。
- **弹出窗口被拦截。** 页面会提示；请允许本站的弹出窗口。
- **中文页面里出现英文。** 这是只有英文的文字：把它加入 `zh.json`（某种语言缺少键时测试会失败，所以这意味着它是 0.1.0 版的草稿，或是人输入的文字，例如机台名称）。
- **重启后大家都要重新登录吗？或者在 s2 和 s3 之间切换时掉登录？** 自 0.2.4 起，会话、Windows SSO 流程和登录限流全部保存在 Redis 中，只要 s2 和 s3 指向同一个 Redis 主机和 DB（`dash_go.exe -check` 会打印 `session store redis (sliding 24h)` 并测试 Redis 连通性），重启与跨机分流都不会掉登录。若 Redis 不可用，会话查询会记录 `session store lookup failed` 并返回未登录，直至 Redis 恢复。从 0.2.3 升级到 0.2.4 时，因不再兼容旧签名 cookie，现有用户需重新登录一次。
- **主题或语言没有保存。** 浏览器禁用了 `localStorage`（主题）或 cookie（语言）。

## 11. 决定和已知限制

- 登录逻辑参照 TimeVault 项目（同样的流程、限制和检查）；账号不像那里一样从 Redis 读取，而是向 Python 后端询问，所以密码和账号列表仍然只在一个地方。
- 退出登录只结束 dash_go 的会话（立即从 Redis 删除会话键并清除浏览器 cookie）。
- 没有“记住我”、没有修改密码、没有会话管理页面；会话每次活跃自动按 `session_hours`（默认 24 小时）滑动续期，也可直接在 Redis 中按 `dash_go:session:*` 吊销。
- 自 0.2.4 起，会话、nonce、Windows SSO 流程与失败登录限流均在 Redis 中共享，可在同一入口后安全运行多台实例（s2 与 s3）而无需 `ip_hash`。未配置 Redis 时自动回退到进程内存（仅限单实例）。
- Windows 登录要求浏览器能访问登录服务，并且每台 dash_go 主机（s2、s3）都能访问它的后端。
- 浅色配色和中文措辞是开发者的提案。
- 运行 dash_go 的那台电脑上的浏览器匹配这台电脑的地址（0.1.2）。同一台电脑上的同一个浏览器，不应该因为两个服务对这台电脑的称呼不同（dash_go 看到回环地址，登录服务看到局域网地址）而被拒绝；检查的规则，即两个步骤在同一台电脑上，没有改变。只有从回环地址启动的登录受影响。
- 自动更新在安装的那一刻（没有人在点击或提交时），改变数据的请求会得到 503，代码为 `updating`，消息为 `err.updating`；只读取数据的请求永远不会被拒绝（[auto_update.md](auto_update.md) 第 3 节）。

## 12. 验证状态

已完成（2026-11-20）：

- 每个包的 Go 单元测试：词条（两种语言的键和占位符相同、代码用到的每个键和值、没有未使用的文字）、语言选择、pickle 读取器（用真实 Python 3.12 的 `pickle` 和 `zlib` 生成的测试向量、恶意和损坏的输入、zip 炸弹、深度）、Python 客户端（签名与在 Python 中算出的值核对）、配置、登录（密码、限流、Windows 流程的每种结果、地址检查、受信任代理、跨站拒绝、日志中没有密码和输入的账号）、shell 页面（顶栏、用户框、语言、登录页提示、重定向、转义、文件）、两套配色的对比度。
- 真实的可执行文件配合 Python 配置文件夹、一个使用真实 `pickle` 和 `zlib` 的 Python 后端与登录服务的替身（`verify_user`、`hc_user_processes`、`sso_result`、登录页面），以及带演示数据的临时 PostgreSQL 18 运行。用无头 Chrome 操作过：首页、登录（密码错误和正确、用邮箱）、登录后回到原页面、工序选择、扫描、手动更改、确认和保存、退出登录、Windows 弹窗流程（登录成功、没有 Windows 用户、未知账号）、Dash 的链接（及其第二次使用）、页面打开期间会话结束、620 px 窗口、主题和语言的四种组合、头像（占位图尺寸、真实图片、失效链接）。除预期的 401 / 409 回答外，控制台没有错误。

已完成（2026-11-20，版本 0.1.2）：

- 新的单元测试：从回环地址启动的 Windows 登录，会匹配这台电脑的地址（直接连接，以及通过同一台电脑上的 nginx，就像开发机的日志那样），而另一台电脑的地址或没有地址则被拒绝；另一台电脑上的浏览器没有得到任何放宽（`ssoflow_test.go`；关掉这条规则时第一个测试会失败）；受信任和不受信任的前置服务器的请求头（`clientip_test.go`）；Python API 的错误（状态 204、消息中带有地址、其中没有凭据：`client_test.go`）；API 地址来自哪里（`config_test.go`）。
- 真实的 0.1.2 可执行文件在开发机上运行：使用端口 18750 的临时配置、真实的本地 Python API、本地 Redis，以及一个用真实 `pickle` 和 `zlib` 作答的登录服务替身。API 取自 patch 文件时（`api_server` = `prod_eng`），`api/me` 回答 502，日志写出了 API 的地址和状态 204；把 `dash_api_url` 设为 `http://localhost/api` 后，它回答 200 以及该账号的 10 个 process。从 `localhost` 发起的 Windows 登录，在登录服务报告这台电脑的局域网地址时以 `ok` 结束，在它报告另一台电脑时以 `mismatch` 结束。
- 前面有前置服务器时的开发机 nginx 配置：用 `config/nginx_sgmeslt595.conf` 做出的一个隔离的 nginx 1.24 实例，放在真实的可执行文件前面。只改了监听端口、dash_go 的端口和受信任的地址，因为无法从真实的 `prod_eng` 地址发送请求；用一个回环地址扮演前置服务器。有这三条指令时，经前置服务器的 Windows 登录以 `ok` 结束，日志里是浏览器的地址；没有它们时被拒绝，`started_from` 是前置服务器，`forwarded="<浏览器>, <前置服务器>"`。在用一个回显请求头的桩后端所做的第二次同类运行中，被伪造的 `X-Forwarded-For` 左侧部分被忽略，来自不受信任地址的请求保持了它自己的地址。
- 生产链路 s1 → s2 → dash_go（`config/nginx_prod_eng.conf` 和 `config/nginx_sgmescimai.conf` 的 `/dash_go/` 路由）：对两个完整文件运行 `nginx -t`，并用它们做出两个隔离的 nginx 实例（只改了监听端口、dash_go 的端口和 s2 当作 s1 来信任的地址），放在真实的 0.1.2 可执行文件、真实的本地 Python API 和登录服务的替身前面。经过整条链路：`/dash_go` 的重定向、登录页、静态文件、Windows 登录和权限检查（`api/me`：200，10 个 process）都正常；经 s1 发送的伪造 `X-Forwarded-For` 被忽略；不经 s1 直接访问 s2 的浏览器也正常。不经 s1 直接发给 s2、并声称是另一个人的地址的请求被拒绝（`mismatch`）；把 location 改成 `$http_x_forwarded_for`（s2 其他 location 的写法）之后，同一个请求被接受，并以另一个人的身份登录。所以 s2 的 `/dash_go/` location 使用 `$proxy_add_x_forwarded_for`。

已完成（2026-11-20，版本 0.1.3）：

- 新的单元测试：返回按钮出现在模块的页面上，指向 `/`，有两种语言，并且不出现在首页和登录页上（`shell_test.go`）；词条文件中 `ui.back`、`ui.back_title` 和 `err.updating` 两种语言都有（`catalog_test.go`，它也会找出代码用到的每个键）；闸门的 503 使用请求的语言（`internal/core/server/gate_test.go`）。强调色在顶栏上的对比度，`palette_test.go` 本来就已覆盖。
- 真实的 0.1.3 可执行文件（端口 18754 的临时配置、真实的 Python 配置和 API）在编辑器的内置浏览器中运行，用测试密钥生成的交接链接登录：模块页面的顶栏在浅色和深色主题、英文和中文、以及 390 px 宽的窗口中的样子。模块名称使用主题的蓝色（浅色主题下计算值为 `rgb(11, 99, 197)`），20.8 px，字重 600；返回按钮在它旁边，带箭头图标；在 390 px 宽度下名称被隐藏，按钮留在 logo 旁边，页面不会横向滚动。点击返回后去往了同一主机和端口的 `/`。

已完成（2026-11-23，版本 0.1.5）：

- 新的单元测试：顶栏将文档和导览按钮放在语言切换与用户框 / 登录按钮之间，在 `/dash_go/login` 上隐藏这两个按钮和 `tour.js`，提供 `/dash_go/_shell/tour.js`，提供中英文 `/dash_go/help` 且章节 ID 一致且不带 `data-tour-auto`（`shell_test.go`）；shell 模板中每个 `{{.Tour}}` 步骤在两种语言下都有 `.title` 和 `.text`，没有未使用的 `tour.*` 词条，`tour.*` 不进入 `#dg-i18n`，`tour.js` 使用的每个 `t('ui.*')` 键都会发送到浏览器（`catalog_test.go`）；`palette_test.go`、`go vet ./...` 和 `go test ./...` 全部通过。
- 无头 Chrome 端到端验证（Playwright 对接进程内 `httptest.Server`，运行真实 shell、CSP 响应头和带内存测试数据的 `hc_scan` 路由）：顶栏按钮从左到右视觉顺序（`lang < docs < guide < sign-in / userbox`）；首页首次访问自动启动导览、键盘切换（`→` / `←` / `Esc`）、刷新后不再自动启动、点击导览按钮手动重放、点击文档按钮在新标签页打开 `/dash_go/help` 且不自动弹窗；登录页隐藏两个按钮；中文界面、深色主题与 390 px 窄屏（导览气泡底部停靠、无横向滚动）。无 JS 或 CSP 报错。

已完成（2026-11-28，版本 0.2.4）：

- 新增单元与跨实例测试（`internal/core/auth/redis_test.go`、`login_test.go`、`auth_test.go`、`config_test.go`、`shell_test.go`）：用两个共用同一 `miniredis` 的模拟 `dash_go` 实例（s2 和 s3）验证跨实例共享会话（账号密码、Dash 交接链接、Windows SSO）、24 小时滑动续期同时刷新 Redis TTL 与浏览器 cookie 的 `MaxAge`（在 18h+18h 连续活跃期间保持有效，空闲 25h 后过期）、单次 shell 页面渲染只发出 1 个刷新后的会话 cookie、退出登录立即在所有实例上吊销旧 cookie、Redis 宕机时返回未登录（401）并记告警日志且绝不泄漏会话 ID、拒绝旧版带点号签名 cookie 与损坏的 Redis 记录、跨实例 Windows SSO 流程（s2 发起 `/auth/sso/start`、s3 轮询 `/auth/sso/poll`、跨机 1 秒轮询间隔、单 IP 5 个上限、一次性消费、Redis 模式下 `PendingSignIns() == 0`、Redis 故障返回 `unavailable`），以及跨实例密码限流（跨 s2/s3 累计单 IP 5 次与单账号 10 次上限、账号键经 SHA-256 哈希绝不明文落盘、窗口过期自动解除、Redis 故障返回 503）。全量 `go vet ./...`、`go vet -tags devserver ./...` 与 `go test -count=1 ./...` 均通过。

**没有验证：**

- s2 上真实的 Redis 服务器以及 s1 nginx 后 s2 + s3 真实多机部署（Redis 会话/SSO/限流存储已通过进程内 `miniredis` 及兼容 Redis 2.6+ 的 Lua 脚本验证）。

- 真实的 Python 后端（部署后的 `verify_user`、`sso_result`；0.1.2 中 `hc_user_processes` 是对开发机的 Python 后端试过的，不是对 s2），真实的 Windows 登录服务和真实的 Windows 用户。
- s1 和 s2 上真实的 nginx（它们的文件用隔离实例测试过，见上文，但没有部署）、https，以及真实的 `prod_eng` 位于开发机之前的情况（它的地址是模拟的；新文件复制过去并 reload 之后，开发机上正在运行的 nginx）。
- 真实静态接口的 480 x 320 占位图（取自 TimeVault 的记录）和真实头像。
- Chrome 之外的浏览器（预计 Edge 表现相同）、真实的扫码枪。
- 使用者对中文措辞（包括 0.1.5 新增的使用手册与导览文案）的审阅。
- s1 和 s2 的 nginx 之后的返回按钮（0.1.3）：链接是 `/`，只对临时服务点击过，而临时服务只提供 `/dash_go/`，所以那里回答 404。
- 禁用 `localStorage` 时的表现（仅经代码审阅确认，未在受限浏览器配置中实测）。
