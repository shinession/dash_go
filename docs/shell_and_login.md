# English

**Updated:** 2026-11-20 (dash_go version 0.1.1). This describes the current code of `internal/core/shell`, `internal/core/i18n`, `internal/core/auth`, `internal/core/pyobj` and the Python calls in `internal/core/dashapi`. Section 12 says what was verified and what was not.

## 1. Overview

Version 0.1.1 gave every page of dash_go one frame, the **shell**:

- a header with the logo, the name of the module, a light / dark theme button, an EN | 中文 language switch, the signed-in user (picture or initials, name) and a sign-out button, or a sign-in button;
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
[logo CIM EDGE]  | HC Scan Assign                    (sun/moon)  [EN|中文]  (picture) Name  [sign out]     <- shell, every page
 Winder  [date] [AM|PM] [Change process]                            Scanned 3/20  Pending 2 ...          <- page bar of the module
```

- The logo is a link to `/dash_go/`. The name of the module is shown next to it (`AppKey` of the page).
- The theme button shows the icon of the theme that is on now: the sun in the light theme, the moon in the dark one.
- The language switch is a link: it sets the cookie and comes back to the same page (query string included).
- The user box shows the picture of the person (section 6) or the initials, the name (cut with an ellipsis after 11 rem; the tooltip is "Name (employee code)") and the sign-out button. A visitor without a session sees a **Sign in** button instead (not on the login page itself).
- When the session ends while a page is open, the script of the page turns the user box into the Sign in button and shows "Session expired" with a button to the login page.
- Below 760 px the module name is hidden and the logo shrinks to its mark; below 480 px the name of the user is hidden too. The header wraps instead of scrolling.

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

After any of them the service sets the session cookie `dash_go_session` (HttpOnly, SameSite Lax, `Secure` behind https, `session_hours`, default 12). **Sign out** (`POST /dash_go/auth/logout`) clears this cookie only; the login of the Dash application in the browser is not touched. A person without a session who opens `/dash_go/hc_scan/` goes to `/dash_go/login?next=...` and comes back after signing in.

### 7.1 Routes

| Request | Purpose |
|---|---|
| `GET /dash_go/` | Landing page with the modules (public). |
| `GET /dash_go/login?next=&error=&signed_out=&expired=` | Login page. A signed-in person is sent on to `next`. |
| `GET /dash_go/lang?set=en\|zh&next=` | Language switch. |
| `GET /dash_go/_shell/...` | Files of the shell (`shell.css`, `shell.js`, `theme-init.js`, `login.js`, `icons.svg`, `favicon.svg`). |
| `GET /dash_go/auth/sso?t=&next=` | One-time link of Dash. |
| `POST /dash_go/auth/login`, `.../auth/logout`, `.../auth/sso/start`, `.../auth/sso/poll` | JSON endpoints of the login page. |

### 7.2 What protects the sign-in

- The JSON endpoints need the header `X-Requested-With: dash_go` (a cross-site form cannot send it), refuse a request that the browser marks `Sec-Fetch-Site: cross-site` or whose `Origin` names another host (compared without the port, as the browser compares sites). Logout needs the same, so a link cannot sign somebody out.
- **Limits of failed password sign-ins**: 5 per client address and 10 per account in 10 minutes (the account is compared without case and, for an e-mail address, by the part before the @). The answer is 429 with `Retry-After`. A successful sign-in forgets the failures of the account and gives the address one slot back; an outage of the user directory is not counted against the account. The limits are in memory: they restart with the service, which is why only one instance must run.
- The same message for an unknown account, a wrong password and a deactivated account (the backend answers 202 for all three).
- **Windows sign-in**: only tokens this server issued are accepted (24 random characters, 120 bits), a result is used once, at most 5 waiting sign-ins per address and 2000 in all, one look at the result per second and flow. The address of the browser that started the flow must equal the address the sign-on service recorded (`sso_require_same_ip`, default on): that stops the forced sign-in in which an attacker starts a flow, makes a victim open the sign-on link with the attacker's token and polls for the identity of the victim. The address of the browser is the address of the connection, unless that is one of the `trusted_proxies`: then it is the right-most address of `X-Forwarded-For` that is not a trusted proxy (so a browser cannot choose its own address).
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
| `photo_url_template` | `dash_go.json` | `/api/static/hr/{ee_code}.webp` | Pictures of people (also the default of the module setting of the same name). |
| `login_password` | `dash_go.json` | `true` | The login page asks for an account and a password. |
| `sso_require_same_ip` | `dash_go.json` | `true` | Windows sign-in: the sign-on service must have seen the address that started it. Turn it off only when no proxy can pass the address of the browser. |
| `trusted_proxies` | `dash_go.json` | `["127.0.0.1", "::1"]` | Reverse proxies (addresses or CIDR ranges) whose `X-Forwarded-For` is believed. `[]` trusts none. |
| `dash_api_url` | `dash_go.json` | `http://<api_server>/api` | Root of the Python API. Without it and without `api_server` the login page is off (the link from Dash still works). |
| `sso_server` | Python `config.json` (or `dash_go.json`) | none | Host of the Windows sign-on service (`host`, `host:port` or `http(s)://host[:port]`); without it the Windows sign-in is off. |
| `api.salt_2` | Python `config.json` | none | Secret of the sign-on result endpoint; required when `sso_server` is set (`dash_go.exe` refuses to start without it). Environment variable `DASH_GO_API_SALT_2` wins. |
| `api.salt_3` | Python `config.json` | none | Secret of the `/api/user` endpoint and of the hand-over link (unchanged). |
| `dash_home` | `dash_go.json` | `/ie/` | The login page links to the Dash application. |

`dash_go.exe -check` prints whether the password and the Windows sign-in are on.

## 9. Deployment

- No new location is needed: `/dash_go/` already covers `_shell`, `login`, `lang` and `auth`. The nginx example sets `X-Forwarded-For $proxy_add_x_forwarded_for`, which the sign-in uses; the default `trusted_proxies` trusts nginx on the same computer.
- **Two proxies in a row** (a server in front of the server that runs dash_go): the nginx next to dash_go sees the address of the front server, not the browser. Either list the front server in `trusted_proxies` (the right-most address that is not trusted wins), or let that nginx use `set_real_ip_from <front server>; real_ip_header X-Forwarded-For; real_ip_recursive on;`. Without one of them every Windows sign-in is refused as `address_mismatch`; the log line says which addresses were compared. `sso_require_same_ip: false` makes it work but removes the protection against the forced sign-in.
- The sign-on service is opened by the **browser** (the popup), and read by **s2** through its backend: s2 must reach `http://<sso_server>/api/sso_result/anything`.
- Passwords travel from the browser to dash_go and from dash_go to the Python backend over the same http as the rest of the company applications. Use https between the browser and nginx where the site has it; then the cookie is `Secure` automatically (`X-Forwarded-Proto`).

## 10. Troubleshooting

- **The login page says "Signing in on this page is not set up".** Neither `login_password` nor the Windows sign-in is on, or neither `dash_api_url` nor `api_server` is set. `dash_go.exe -check` and the first lines of the log say which.
- **"Invalid account or password" for a right password.** The user may be deactivated (the backend answers the same for all failures), or the clocks of s2 and the backend differ by more than the hour of the signature (`log: dash api rejected the request signature`).
- **Windows sign-in says "could not be matched to this browser".** The two addresses differ: see the log line `single sign-on refused` (`started_from`, `sso_address`, `connection`, `trusted_proxy`, `forwarded`) and section 9.
- **The popup is blocked.** The page says so; allow pop-ups for the site.
- **The Chinese page shows English text.** A text that is only in English: add it to `zh.json` (the tests fail when a key is missing in one language, so this means a draft of version 0.1.0 or a text written by a person, such as a machine name).
- **After a restart everybody has to sign in again.** No: sessions are signed cookies and survive a restart as long as `api.salt_3` is unchanged. Failed-login counters and waiting Windows sign-ins are lost.
- **Theme or language does not stick.** The browser blocks `localStorage` (theme) or cookies (language).

## 11. Decisions and known limits

- The login logic follows the TimeVault project (same flow, limits and checks); the accounts are not read from Redis as there, but asked from the Python backend, so passwords and the account list stay in one place.
- Sign-out ends the session of dash_go only.
- There is no "remember me", no password change and no list of sessions; the cookie lives `session_hours`.
- The limits are per process. Do not run two instances behind one address.
- The Windows sign-in needs the browser to reach the sign-on service and s2 to reach its backend.
- The light palette and the Chinese wording are the developer's proposal.

## 12. Verification status

Done (2026-11-20):

- Go unit tests of every package: catalogs (same keys and placeholders in both languages, every key and value the code uses, no unused text), language choice, the pickle reader (vectors made by the real Python 3.12 `pickle` and `zlib`, hostile and damaged input, zip bomb, depth), the Python clients (signatures checked against values computed in Python), configuration, the sign-in (password, limits, Windows flow with every outcome, address checks, trusted proxies, cross-site refusals, no password or typed account in the log), the shell pages (header, user box, language, login page notices, redirects, escaping, files), the contrast of both palettes.
- The real binary was run with a Python configuration folder, a stand-in for the Python backend and the sign-on service that uses the real `pickle` and `zlib` (`verify_user`, `hc_user_processes`, `sso_result`, the sign-on page), and the scratch PostgreSQL 18 with demo data. Driven in headless Chrome: landing, login (wrong and right password, by e-mail), redirect back, process picker, scans, manual change, acknowledgement and saving, sign-out, the Windows popup flow (signed in, no Windows user, unknown account), the link from Dash (and its second use), the session ending while the page is open, a 620 px window, the four combinations of theme and language, pictures (placeholder size, real picture, broken link). No console error except the expected 401 / 409 answers.

**Not verified:**

- The real Python backend (`verify_user`, `sso_result` as deployed), the real Windows sign-on service and a real Windows user.
- nginx on s2: `X-Forwarded-For` handling, `trusted_proxies` behind two proxies, https.
- The 480 x 320 placeholder of the real static endpoint (taken from the TimeVault records) and real photos.
- Browsers other than Chrome (Edge is expected to behave the same), a real scanner.
- A review of the Chinese wording by the users.

---

# 中文

**更新日期：** 2026-11-20（dash_go 版本 0.1.1）。本文描述 `internal/core/shell`、`internal/core/i18n`、`internal/core/auth`、`internal/core/pyobj` 的当前代码，以及 `internal/core/dashapi` 中对 Python 的调用。第 12 节说明哪些内容已验证、哪些没有验证。

## 1. 概述

0.1.1 版给 dash_go 的每个页面加上了统一的外框，即 **shell**：

- 顶栏：logo、模块名称、浅色 / 深色主题按钮、EN | 中文 语言切换、当前登录用户（头像或首字母、姓名）和退出登录按钮；未登录时显示登录按钮；
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
[logo CIM EDGE]  | HC 扫码分派                         (太阳/月亮)  [EN|中文]  (头像) 姓名  [退出]     <- shell，每个页面都有
 Winder  [日期] [AM|PM] [切换工序]                                  已扫描 3/20  待确认 2 ...          <- 模块自己的页面栏
```

- logo 是指向 `/dash_go/` 的链接，旁边显示模块名称（页面的 `AppKey`）。
- 主题按钮显示当前主题的图标：浅色主题是太阳，深色主题是月亮。
- 语言切换是一个链接：设置 cookie 后回到同一页面（包括查询字符串）。
- 用户框显示头像（第 6 节）或首字母、姓名（超过 11 rem 用省略号截断，悬停提示是“姓名（工号）”）和退出按钮。没有会话的访问者看到的是 **登录** 按钮（登录页本身不显示）。
- 页面打开期间会话结束时，页面脚本把用户框换成登录按钮，并显示“登录已过期”和一个去登录页的按钮。
- 窄于 760 px 时隐藏模块名称，logo 缩成只剩图标；窄于 480 px 时也隐藏用户名。顶栏会换行而不是出现滚动条。

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

任何一种方式之后，服务都会设置会话 cookie `dash_go_session`（HttpOnly、SameSite Lax、https 之后为 `Secure`、`session_hours`，默认 12）。**退出登录**（`POST /dash_go/auth/logout`）只清除这个 cookie；浏览器中 Dash 应用的登录不受影响。没有会话的人打开 `/dash_go/hc_scan/` 会被带到 `/dash_go/login?next=...`，登录后回到原页面。

### 7.1 路由

| 请求 | 用途 |
|---|---|
| `GET /dash_go/` | 带模块列表的首页（公开）。 |
| `GET /dash_go/login?next=&error=&signed_out=&expired=` | 登录页。已登录的人会被带到 `next`。 |
| `GET /dash_go/lang?set=en\|zh&next=` | 语言切换。 |
| `GET /dash_go/_shell/...` | shell 的文件（`shell.css`、`shell.js`、`theme-init.js`、`login.js`、`icons.svg`、`favicon.svg`）。 |
| `GET /dash_go/auth/sso?t=&next=` | Dash 的一次性链接。 |
| `POST /dash_go/auth/login`、`.../auth/logout`、`.../auth/sso/start`、`.../auth/sso/poll` | 登录页的 JSON 接口。 |

### 7.2 登录的保护措施

- JSON 接口需要请求头 `X-Requested-With: dash_go`（跨站表单发不出它），并拒绝浏览器标记为 `Sec-Fetch-Site: cross-site` 的请求，以及 `Origin` 指向其他主机的请求（比较时不含端口，和浏览器判断同站的方式一致）。退出登录也有同样的要求，所以一个链接不能让别人退出登录。
- **失败密码登录的限制**：10 分钟内每个客户端地址 5 次、每个账号 10 次（账号比较时不分大小写，邮箱地址只比较 @ 之前的部分）。回答是 429，并带 `Retry-After`。登录成功会清除该账号的失败记录，并还给该地址一个名额；用户目录不可用不会记到账号头上。限制保存在内存中，随服务重启而清零，所以只能运行一个实例。
- 账号不存在、密码错误和账号已停用，提示相同（后端对这三种情况都回答 202）。
- **Windows 登录**：只接受本服务器签发的令牌（24 个随机字符，120 位），一个结果只能使用一次，每个地址最多同时等待 5 个、总共最多 2000 个，每个流程每秒最多查看一次结果。启动流程的浏览器的地址，必须等于登录服务记录的地址（`sso_require_same_ip`，默认开启）：这样可以阻止“强加登录”，即攻击者启动一个流程，让受害者用攻击者的令牌打开登录链接，然后轮询得到受害者的身份。浏览器的地址是连接的地址；如果连接来自 `trusted_proxies` 之一，则取 `X-Forwarded-For` 中最右边的、不是受信任代理的地址（所以浏览器不能自己选择地址）。
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
| `photo_url_template` | `dash_go.json` | `/api/static/hr/{ee_code}.webp` | 人员头像（也是同名模块设置的默认值）。 |
| `login_password` | `dash_go.json` | `true` | 登录页要求输入账号和密码。 |
| `sso_require_same_ip` | `dash_go.json` | `true` | Windows 登录：登录服务必须看到启动流程的那个地址。只有在没有代理能传递浏览器地址时才关闭。 |
| `trusted_proxies` | `dash_go.json` | `["127.0.0.1", "::1"]` | 其 `X-Forwarded-For` 会被信任的反向代理（地址或 CIDR 范围）。`[]` 表示谁都不信任。 |
| `dash_api_url` | `dash_go.json` | `http://<api_server>/api` | Python API 的根。既没有它也没有 `api_server` 时，登录页关闭（Dash 的链接仍然可用）。 |
| `sso_server` | Python `config.json`（或 `dash_go.json`） | 无 | Windows 登录服务的主机（`host`、`host:port` 或 `http(s)://host[:port]`）；没有它则 Windows 登录关闭。 |
| `api.salt_2` | Python `config.json` | 无 | 登录结果接口的密钥；设置了 `sso_server` 时必填（没有它 `dash_go.exe` 拒绝启动）。环境变量 `DASH_GO_API_SALT_2` 优先。 |
| `api.salt_3` | Python `config.json` | 无 | `/api/user` 接口和交接链接的密钥（没有改变）。 |
| `dash_home` | `dash_go.json` | `/ie/` | 登录页链接到 Dash 应用。 |

`dash_go.exe -check` 会打印密码登录和 Windows 登录是否开启。

## 9. 部署

- 不需要新的 location：`/dash_go/` 已经覆盖 `_shell`、`login`、`lang` 和 `auth`。nginx 示例设置了 `X-Forwarded-For $proxy_add_x_forwarded_for`，登录会用到它；默认的 `trusted_proxies` 信任同一台电脑上的 nginx。
- **两层代理**（运行 dash_go 的服务器前面还有一台服务器）：紧挨着 dash_go 的 nginx 看到的是前面那台服务器的地址，而不是浏览器的。要么把前面的服务器列入 `trusted_proxies`（最右边的、不受信任的地址优先），要么让那个 nginx 使用 `set_real_ip_from <前面的服务器>; real_ip_header X-Forwarded-For; real_ip_recursive on;`。两者都没有时，每次 Windows 登录都会因 `address_mismatch` 被拒绝；日志行会说明比较了哪些地址。`sso_require_same_ip: false` 可以让它工作，但会失去对强加登录的保护。
- 登录服务由**浏览器**打开（弹出窗口），由 **s2** 通过它的后端读取：s2 必须能访问 `http://<sso_server>/api/sso_result/anything`。
- 密码从浏览器传到 dash_go，再从 dash_go 传到 Python 后端，使用的是与公司其他应用相同的 http。站点有 https 的地方，浏览器到 nginx 之间应使用 https；这时 cookie 会自动带上 `Secure`（`X-Forwarded-Proto`）。

## 10. 故障排查

- **登录页显示“本服务器没有设置在此页面登录”。** 既没有开启 `login_password` 也没有开启 Windows 登录，或者 `dash_api_url` 和 `api_server` 都没有设置。`dash_go.exe -check` 和日志开头几行会说明是哪一种。
- **密码正确却提示“账号或密码不正确”。** 该用户可能已被停用（后端对所有失败回答相同），或者 s2 与后端的时钟相差超过签名的小时（日志：`dash api rejected the request signature`）。
- **Windows 登录提示“无法与当前浏览器对应上”。** 两个地址不同：看日志行 `single sign-on refused`（`started_from`、`sso_address`、`connection`、`trusted_proxy`、`forwarded`）和第 9 节。
- **弹出窗口被拦截。** 页面会提示；请允许本站的弹出窗口。
- **中文页面里出现英文。** 这是只有英文的文字：把它加入 `zh.json`（某种语言缺少键时测试会失败，所以这意味着它是 0.1.0 版的草稿，或是人输入的文字，例如机台名称）。
- **重启后大家都要重新登录吗？** 不会：会话是带签名的 cookie，只要 `api.salt_3` 不变，重启后仍然有效。失败登录的计数和等待中的 Windows 登录会丢失。
- **主题或语言没有保存。** 浏览器禁用了 `localStorage`（主题）或 cookie（语言）。

## 11. 决定和已知限制

- 登录逻辑参照 TimeVault 项目（同样的流程、限制和检查）；账号不像那里一样从 Redis 读取，而是向 Python 后端询问，所以密码和账号列表仍然只在一个地方。
- 退出登录只结束 dash_go 的会话。
- 没有“记住我”、没有修改密码、没有会话列表；cookie 的有效期是 `session_hours`。
- 限制是按进程的。不要在同一个地址后面运行两个实例。
- Windows 登录要求浏览器能访问登录服务，并且 s2 能访问它的后端。
- 浅色配色和中文措辞是开发者的提案。

## 12. 验证状态

已完成（2026-11-20）：

- 每个包的 Go 单元测试：词条（两种语言的键和占位符相同、代码用到的每个键和值、没有未使用的文字）、语言选择、pickle 读取器（用真实 Python 3.12 的 `pickle` 和 `zlib` 生成的测试向量、恶意和损坏的输入、zip 炸弹、深度）、Python 客户端（签名与在 Python 中算出的值核对）、配置、登录（密码、限流、Windows 流程的每种结果、地址检查、受信任代理、跨站拒绝、日志中没有密码和输入的账号）、shell 页面（顶栏、用户框、语言、登录页提示、重定向、转义、文件）、两套配色的对比度。
- 真实的可执行文件配合 Python 配置文件夹、一个使用真实 `pickle` 和 `zlib` 的 Python 后端与登录服务的替身（`verify_user`、`hc_user_processes`、`sso_result`、登录页面），以及带演示数据的临时 PostgreSQL 18 运行。用无头 Chrome 操作过：首页、登录（密码错误和正确、用邮箱）、登录后回到原页面、工序选择、扫描、手动更改、确认和保存、退出登录、Windows 弹窗流程（登录成功、没有 Windows 用户、未知账号）、Dash 的链接（及其第二次使用）、页面打开期间会话结束、620 px 窗口、主题和语言的四种组合、头像（占位图尺寸、真实图片、失效链接）。除预期的 401 / 409 回答外，控制台没有错误。

**没有验证：**

- 真实的 Python 后端（部署后的 `verify_user`、`sso_result`）、真实的 Windows 登录服务和真实的 Windows 用户。
- s2 上的 nginx：`X-Forwarded-For` 的处理、两层代理之后的 `trusted_proxies`、https。
- 真实静态接口的 480 x 320 占位图（取自 TimeVault 的记录）和真实头像。
- Chrome 之外的浏览器（预计 Edge 表现相同）、真实的扫码枪。
- 使用者对中文措辞的审阅。
