# English

**Updated:** 2026-11-20 (dash_go version 0.1.3). This describes the current code of `internal/core/update`, `internal/core/server/gate.go`, `PendingSignIns` in `internal/core/auth/ssoflow.go`, the settings `update_*` in `internal/core/config/config.go`, the wiring in `cmd/dash_go/main.go` and `cmd/dash_go/update.go`, and `scripts/release.ps1`. Section 10 says what was verified and what was not.

## 1. Overview

Since version 0.1.3 a `dash_go` can replace itself with a newer build that was published to a release folder. It is the "simple CD" of the TimeVault project (its ADR 0014, `internal/update`) adapted to dash_go: nothing is pushed to the server and nobody has to log on to it. The server reads a small manifest file from time to time and fetches the new program when the version in the manifest is not the version that runs.

- It is **off by default**. A server takes part when `update_url` is set in its `dash_go.json` (or in the patch file of its host). The development machine stays off.
- A release is two files, `dash_go.exe` and `dash_go.json`. They are published into the static folder of the Dash API, so the manifest is at `http://<host>/api/static/cd/dash_go.json`.
- The update is installed only when **nobody is clicking or submitting** (section 3), so a person who is scanning is not cut off in the middle of a click.
- The running program is not touched until the new one has been downloaded and checked (size, sha256, the version it says it is, and whether it accepts the settings of this server) and a quiet moment has come.

## 2. The release

A release folder holds two files:

| File | Content |
|---|---|
| `dash_go.exe` | The program with the modules that were chosen for the build (`scripts/build.ps1`). |
| `dash_go.json` | The manifest. UTF-8; a byte order mark is accepted, and so are fields that this version does not know. |

Fields of the manifest:

| Field | Meaning |
|---|---|
| `latest_version` | The version of the program, as `dash_go.exe -version` says it (its first line is `dash_go <version>`). A leading `v` is ignored. |
| `sha256` | The sha256 of `dash_go.exe`, 64 hexadecimal characters. |
| `package_url` | Where the program is: a file name (looked for next to the manifest), a path on the same server (`/api/static/cd/dash_go.exe`) or a full http(s) address. The script writes `dash_go.exe`. |
| `size` | The size of `dash_go.exe` in bytes (1 byte to 512 MiB). |

Rules:

- A version that is **different** from the running one is an update, also a lower one. Publishing an older build under its older version is the way back for all servers (section 7).
- A release with the version of the one before it is no update to any server. `scripts/release.ps1` warns when it sees that (same version, other program).
- The script writes the manifest **last**, so a server that reads it never meets a version whose program is not there yet. A manual copy is not atomic: copy the program first and the manifest last; a server that reads while the copy is going on finds a size or hash that does not match and tries again later.
- The Dash API answers **202 and a picture** for a file that does not exist. The updater takes every answer but 200 for an error, so it never mistakes that picture for a manifest or a program, and the log says `answered 202 Accepted`.
- The address of a manifest and of a program must not contain a user name or a password (the address is written to the log). A manifest that cannot be used is refused with the reason.

## 3. What an update does

1. **Look.** 10 seconds after the start and then every `update_interval_minutes` (default 10), the manifest is read (30 seconds at most).
2. **Download and check.** When the version differs the program is downloaded to `dash_go.new.exe.part` (a download that receives nothing for 2 minutes is given up), checked against the size and the sha256 of the manifest and renamed to `dash_go.new.exe`. Then it is run twice (30 seconds at most each): `dash_go.new.exe -version` must say the version of the manifest, and `dash_go.new.exe <the flags of this program> -check` must succeed. `-check` loads the configuration and the modules and starts no server; it gets the same flags as the running program (for example `-config-dir`), so it reads the same configuration. Any failure is written to the log and tried again at the next look. The running program is not touched.
3. **Ready.** The log says `the new version is ready`. The service goes on as usual. Every 2 seconds it asks whether somebody works with it. The service is **busy** when:
   - a request that changes something (every method but GET, HEAD and OPTIONS: scan, assign, remove, reset, confirm, sign in, sign out) is being served, or ended less than **10 seconds** ago; or
   - a Windows sign-in was started and is not done yet (it is only in memory and a restart would lose it).

   Requests that only read do not count: opening a page and the poll of the board every 3 seconds do not hold an update back. The reason for waiting is logged (`waits for a quiet moment`, again after 5 minutes at the earliest).
4. **Quiet moment.** The service stops taking requests that change something: they get **503** with `{"error": "...", "code": "updating"}` and `Retry-After: 5`, the message is in the language of the request ("dash_go is being updated. Try again in a few seconds."), and the page shows it like any other error. The requests that were already running are waited for, at most 1 minute: if one does not end, the requests are taken again, the log says so, and the update waits for the next quiet moment.
5. **Put in place.** `dash_go.exe` is renamed to `dash_go.old-<version>.exe` (Windows lets a running program be renamed but not overwritten) and `dash_go.new.exe` takes the name `dash_go.exe`. When that fails the old program gets its name back, the requests are taken again, and this release is not tried again until the manifest changes. The newest 3 old programs are kept. A marker, `dash_go.exe.update.json`, is written.
6. **Stop and start.** The service shuts down (the requests that read get up to 10 seconds to end) and the new program is started (section 4). The new program reads and removes the marker and logs `the program was updated` with the old and the new version and how long the release waited.

Between the stop and the start the service does not answer for a second or two (the nginx in front of it answers 502); the pages that poll try again by themselves. People stay signed in (the session is a signed cookie and the secret does not change). The drafts of `hc_scan` are kept in Redis (`draft_store` `redis`, the default) and survive; with `draft_store` `memory` they are lost at every restart (the module logs a warning at the start).

## 4. How the new program is started (`update_restart`)

| Value | What happens | For |
|---|---|---|
| `supervise` (default) | The process that was started stays as a small **launcher**: it starts the new program as a child process in the same console, with the same folder and environment, and starts it again whenever it exits with the code **75**. Ctrl+C stops the child and so the launcher. When the launcher is killed (a task that is ended, `Stop-Process`) the child stops by itself: its standard input is a pipe that only the launcher holds. Later updates are done by the child, which exits with 75 and is started again by the launcher. | A program started by hand, in a console window, or by a scheduled task. |
| `exit` | The process ends with the exit code **75** once the new program is in place. A service manager has to start the program again. | NSSM or a task that restarts a program that exits. |

The launcher is the process that was started first, and it runs from `dash_go.old-<version>.exe` (the file it was started from, renamed). So that file stays until the launcher is stopped, and the launcher itself never gets newer: to move it to the new program, stop the process and start `dash_go.exe` again.

## 5. Publishing a release

1. Raise the version everywhere it is written (the places listed in the commit of a version: `VERSION`, `internal/version/version.go`, `internal/mods/<name>/manifest.json` and `ModuleVersion` of the module, `README.md`, the "Updated" line of the documents).
2. Run the script with the folder that the Dash API serves as `static/cd`. On the development machine that is `D:\Programs\Python\static\cd`. A server reads the API that its `update_url` names: the API of s2 serves the `static\cd` folder of s2, so give `-OutDir` the path of that folder (for example a share), or publish into a scratch folder and copy the two files there, the program first and the manifest last:

```powershell
.\scripts\release.ps1 -OutDir D:\Programs\Python\static\cd
```

It runs `go test`, builds with `scripts/build.ps1` (so the version and the modules are those of a normal build), asks the program for its version, measures it and hashes it, copies `dash_go.exe` into the folder through a temporary name and writes `dash_go.json` last (UTF-8 without a byte order mark, through a temporary file).

| Parameter | Meaning |
|---|---|
| `-OutDir` | The release folder. Default `<repo>\release` (not committed). |
| `-Modules` | The modules to build, as for `build.ps1`. Default: all. |
| `-BaseUrl` | Only when the program is served from another address than the manifest: the address of the folder, for example `http://host/api/static/cd/`. `package_url` then holds the full address. |
| `-SkipTests` | Do not run `go test`. |
| `-Version` | Release this version instead of the one in `VERSION`. For a rehearsal with a scratch folder only. |

3. The servers that read this folder (`update_url`) find the new version at their next look and install it at the next quiet moment. The static folder of the Dash API is on the server that runs the Python API (s2); a server that has the folder on another computer reads it over http.

Do not publish a rehearsal into the real folder: every server that reads it would install it.

## 6. Turning it on for a server

| Key in `dash_go.json` | Default | Meaning |
|---|---|---|
| `update_url` | `""` (off) | Where the manifest is: an `http(s)` address (of the file, or of the folder, with a slash at the end), or the absolute path of the file or of the folder. No user name, password, query or fragment. |
| `update_interval_minutes` | `10` | Minutes between two looks at the manifest, 1 to 1440. |
| `update_restart` | `supervise` | `supervise` or `exit` (section 4). |

A wrong value is refused at the start with the key named (`dash_go.exe -check` says it too). `-check` and the log of the start say what is set: `automatic update http://localhost/api/static/cd/dash_go.json every 10m0s, restart supervise`, or `automatic update off`.

To turn it on **for s2 only**, put the key into the patch file of that host (`dash_go_patch_<hostname>.json` next to `dash_go.json`; the patch file replaces whole top-level keys of `dash_go.json`):

```json
{ "update_url": "http://localhost/api/static/cd/dash_go.json" }
```

On s2 `localhost` is the nginx of s2, which sends `/api` to the Python API of s2 (`api_server` is `localhost` there). Steps for the first time:

1. The first installation of 0.1.3 is by hand: the running 0.1.2 has no updater. Stop dash_go, copy `dash_go.exe` of the 0.1.3 package over it, add the setting, start it.
2. The account that runs dash_go must be allowed to create and rename files in the folder of `dash_go.exe`. The start checks that and the log warns when it cannot: `automatic update cannot put a new program in the folder of this one`.
3. From the next release on, publish with `release.ps1` and wait. The log of the server says what happens.

## 7. Going back

- **For all servers:** publish the older build under its own version number (`release.ps1` on the older source, or copy its `dash_go.exe` with a manifest of the older version). Any different version is an update.
- **For one server, by hand:** stop the process (in `supervise` mode the launcher: the child stops with it), rename `dash_go.exe` to something else, copy `dash_go.old-<version>.exe` to `dash_go.exe`, start it. Then remove `update_url` (or publish the older version) as well; otherwise the server installs the release again at the next look.

## 8. Troubleshooting

All lines are in the log of the service (`logs\dash_go-YYYYMMDD.log` and the console); the ones of the updater start with `automatic update:`.

| What the log says | Meaning and what to do |
|---|---|
| `a step failed ... step=check error="localhost answered 202 Accepted"` | The manifest is not in the folder (the static server answers 202 and a picture for a missing file). Check the address of `update_url` and that `dash_go.json` is there. |
| `a step failed ... step=check` with another text | The manifest cannot be reached or read (server down, address wrong, manifest invalid). The service runs on as it is; the same text is logged once until a look succeeds. |
| `step=download error="... not the one the manifest describes"` | The size or the sha256 of the program is not the one of the manifest: a release that is still being copied, or a manifest that does not belong to the program. Tried again at the next look. |
| `step=program error="the program of the release says it is version X, but the manifest says Y"` | The manifest and the program disagree. Not tried again until the manifest changes. Publish again with `release.ps1`. |
| `step=program error="the new program does not accept the settings of this service: ..."` | `dash_go.new.exe <flags> -check` failed; the text is its reason (a setting the new version does not accept). Fix the setting. Tried again at the next look. |
| `step=install error="the new program could not be put in place ..."` | The files could not be renamed (rights, a virus scanner). The old program is back in place and the requests are taken again. |
| `automatic update cannot put a new program in the folder of this one` | The folder of `dash_go.exe` cannot be written to by this account. |
| `the new version waits for a quiet moment busy="..."` | Somebody is working with the service: `N request(s) that change something are being served`, `the last request that changed something ended Ns ago`, or `N Windows sign-in(s) are under way`. It goes on by itself. Logged once, then every 5 minutes. |
| `requests that were running did not end in time` | A request that changes something did not end within a minute. The requests are taken again and the update waits for the next quiet moment. |
| `the new program is in place; the service restarts ... waited=...` | The update is going on. `previous_program` is the kept old program. |
| `the program was updated from=X to=Y` | The new program has started and found the marker. |
| `the program asks to be started again` | `supervise` mode: the launcher starts the new program. |
| `exiting so that the new version is started code=75` | `exit` mode: the service manager has to start the program again. |
| `the launcher that started this program is gone, so this program stops` | The launcher was killed; the child stops with it. Start `dash_go.exe` again. |
| `the new program could not be started` | `supervise` mode: the file cannot be run. Copy a `dash_go.old-<version>.exe` back (section 7). |
| Nothing at all about updates | `update_url` is not set for this host (`-check` says `automatic update off`), or the first look (10 seconds after the start) has not happened yet. |

A server whose version never changes although the release is newer: look at `-check` (is `update_url` set in the file the service reads?), at the log of the first minutes after the start, and at `latest_version` in the manifest (it must be a version that is different from the one the server says in `dash_go started version=...`).

## 9. Decisions and limits

- **Opt-in, no switch in the pages.** The updater has no page and no button. The log is the interface. To make a server look for a release at once, restart the service: the first look is 10 seconds after the start.
- **"Nobody is clicking or submitting"** means requests that change something. The 10 seconds (`quietFor` in `cmd/dash_go/update.go`) and the looks every 2 seconds are fixed values, not settings. Somebody who only looks at a page does not hold an update back, and a restart ends what a page holds in the browser only (text that was typed and not sent). The browser shows a network error or the 503 message for a moment.
- **Whoever can write to the release folder controls every server that reads it.** The sha256 only protects against damaged copies; the manifest and the program come from the same place. Keep the write access to `static\cd` as tight as the right to run programs on the servers. The address is `http` inside the company network; an `https` address or a share with its own rights is possible.
- **No automatic way back.** A new program that starts and then fails is not replaced by the old one; section 7 is the way back. The new program is only installed after `-version` and `-check` passed, which catches the common reasons (a setting it does not accept).
- **The launcher does not update itself** (section 4).
- **One release for all servers** that read the folder. A server that needs another version reads another folder (`update_url`).
- The updater is stdlib only and does not add a dependency.

## 10. Verification status

Done (2026-11-20, version 0.1.3):

- Unit tests: `internal/core/update` (manifest, sources over http and files including the 202 answer of the static server, download with size and hash, a download that stops, swap of a program that runs, the kept programs, the marker, the checker with the flags of the running service and its reasons, the launcher and the child that stops when the launcher is killed, the updater with a fake service: waiting for a quiet moment, draining, the time limit of the drain, cancel, failures that are not repeated), `internal/core/server` (the gate: counts, quiet time, 503 in both languages), `internal/core/auth` (pending sign-ins), `internal/core/config` (the three keys and their errors), `internal/core/shell` (the Back button).
- The real 0.1.3 binary on the development machine, with scratch configuration on other ports, the real Python configuration, API and Redis, and rehearsal releases 0.1.4 and 0.1.5 built by `scripts/release.ps1` and served by a scratch web server: a quiet service updated 10 seconds after the start (download, check, drain, swap, the launcher started the new program, the marker was read); with a POST every 2 seconds the update waited (`busy="the last request that changed something ended 0s ago"`) and was installed 11 seconds after the last POST (`waited=1m24s`), all 48 POSTs answered 204; the second update was done by the supervised child, which exited with 75 and was started again; killing the launcher stopped the child within seconds; `update_restart` `exit` ended with the log line `code=75` and left the new program and the marker; a release with a wrong sha256 and a release whose program says another version were refused and logged once, and the service went on; the messages of the real `-check` for configurations it does not accept were read by the checker (`configuration error: ...` and the `level=ERROR` line, without the password).
- The same update through the real Dash API and the live nginx of the development machine: a scratch folder `static/cd/dash_go_rehearsal/` (removed afterwards), `update_url` `http://localhost/api/static/cd/dash_go_rehearsal/`: the manifest answered 200, a missing file 202 with `image/jpeg`, HEAD 405 (the updater only uses GET), and the 23 MB program was downloaded and installed.
- The header of a module page in a browser (the integrated browser of the editor) in the light and dark theme, English and Chinese, and 390 px wide: the title in the blue of the theme (`#0b63c5`, `#4da3ff`, 1.3 rem, weight 600), the Back button next to it, which goes to `/` of the same host.

Not verified:

- The deployment on s2: nothing was published to the real `static\cd` and no real server was changed. The first installation of 0.1.3 and `update_url` on s2 are still to be done by hand.
- `update_restart` `exit` with a real service manager (NSSM, scheduled task): the exit code 75 is in the log, but no manager was used to start the program again.
- Ctrl+C in a console window with a launcher and a child (by design the launcher ignores the signal and the child shuts down on it; no test covers it, and the test runs had no console).
- A Windows sign-in that is under way while a release waits (the count is unit-tested, no real sign-on service was used), and an update while people really scan.
- An `https` address, a share (`\\server\share`) as the release folder, a virus scanner that holds a new file.
- The Back button behind the nginx of s1 and s2 (the link is `/`, tested against the scratch service only).

---

# 中文

**更新日期：** 2026-11-20（dash_go 版本 0.1.3）。本文描述 `internal/core/update`、`internal/core/server/gate.go`、`internal/core/auth/ssoflow.go` 中的 `PendingSignIns`、`internal/core/config/config.go` 中的 `update_*` 设置、`cmd/dash_go/main.go` 和 `cmd/dash_go/update.go` 中的接线，以及 `scripts/release.ps1` 的当前代码。第 10 节说明哪些内容已验证、哪些没有验证。

## 1. 概述

自 0.1.3 版起，`dash_go` 可以用发布到发布文件夹中的新版本替换自己。它是 TimeVault 项目的“简易 CD”（它的 ADR 0014，`internal/update`）在 dash_go 上的改写：不向服务器推送任何东西，也不用登录服务器。服务器时不时读取一个小的清单文件，当清单中的版本不是正在运行的版本时，就取回新程序。

- **默认关闭。** 服务器在自己的 `dash_go.json`（或所在主机的 patch 文件）中设置了 `update_url` 才会参与。开发机保持关闭。
- 一次发布是两个文件，`dash_go.exe` 和 `dash_go.json`。它们发布到 Dash API 的静态文件夹，所以清单的地址是 `http://<host>/api/static/cd/dash_go.json`。
- 只有在**没有人在点击或提交**时才安装更新（第 3 节），所以正在扫码的人不会在点击到一半时被打断。
- 在新程序下载完成并通过检查（大小、sha256、它自己报告的版本、它是否接受本服务器的设置），并且出现了安静时刻之前，正在运行的程序不会被改动。

## 2. 发布内容

发布文件夹里有两个文件：

| 文件 | 内容 |
|---|---|
| `dash_go.exe` | 程序，包含构建时选定的模块（`scripts/build.ps1`）。 |
| `dash_go.json` | 清单。UTF-8；接受字节顺序标记，也接受本版本不认识的字段。 |

清单的字段：

| 字段 | 含义 |
|---|---|
| `latest_version` | 程序的版本，与 `dash_go.exe -version` 报告的一致（第一行是 `dash_go <version>`）。开头的 `v` 会被忽略。 |
| `sha256` | `dash_go.exe` 的 sha256，64 个十六进制字符。 |
| `package_url` | 程序的位置：文件名（在清单旁边查找）、同一服务器上的路径（`/api/static/cd/dash_go.exe`）或完整的 http(s) 地址。脚本写入 `dash_go.exe`。 |
| `size` | `dash_go.exe` 的字节数（1 字节到 512 MiB）。 |

规则：

- 与正在运行的版本**不同**的版本就是更新，版本更低也一样。把旧的构建用它自己的旧版本号发布出去，就是所有服务器回退的办法（第 7 节）。
- 与上一次发布版本号相同的发布，对任何服务器都不是更新。`scripts/release.ps1` 发现这种情况（版本相同、程序不同）会发出警告。
- 脚本**最后**才写清单，所以读取清单的服务器不会遇到程序还没就位的版本。手动复制不是原子的：先复制程序，最后复制清单；在复制过程中读取的服务器会发现大小或哈希不匹配，稍后再试。
- Dash API 对不存在的文件返回 **202 和一张图片**。更新程序把 200 以外的所有回答都当作错误，所以绝不会把那张图片当成清单或程序，日志里会写 `answered 202 Accepted`。
- 清单和程序的地址里不能带用户名或密码（地址会写进日志）。不能使用的清单会被拒绝，并说明原因。

## 3. 一次更新做什么

1. **查看。** 启动 10 秒后，以及之后每隔 `update_interval_minutes`（默认 10 分钟），读取清单（最多 30 秒）。
2. **下载并检查。** 版本不同时，把程序下载到 `dash_go.new.exe.part`（2 分钟收不到任何数据就放弃），按清单的大小和 sha256 检查，然后改名为 `dash_go.new.exe`。接着运行它两次（每次最多 30 秒）：`dash_go.new.exe -version` 必须报告清单中的版本，`dash_go.new.exe <本程序的参数> -check` 必须成功。`-check` 加载配置和模块，不启动服务器；它会得到与正在运行的程序相同的参数（例如 `-config-dir`），所以读取的是同一份配置。任何失败都会写进日志，并在下一次查看时重试。正在运行的程序不会被改动。
3. **就绪。** 日志写 `the new version is ready`。服务照常运行。每隔 2 秒它询问是否有人正在使用。服务在以下情况**忙**：
   - 有改变数据的请求（除 GET、HEAD、OPTIONS 以外的所有方法：扫描、分派、移除、重置、确认、登录、退出登录）正在处理，或结束不到 **10 秒**；或
   - 有 Windows 登录已开始但尚未完成（它只在内存里，重启会丢失）。

   只读取数据的请求不算：打开页面和看板每 3 秒一次的轮询，都不会拖住更新。等待的原因会写进日志（`waits for a quiet moment`，之后最早每 5 分钟再写一次）。
4. **安静时刻。** 服务不再接受改变数据的请求：它们会得到 **503** 以及 `{"error": "...", "code": "updating"}` 和 `Retry-After: 5`，消息使用请求的语言（“dash_go 正在更新，请几秒钟后再试。”），页面像显示其他错误一样显示它。已经在运行的请求会被等待，最多 1 分钟：如果有请求不结束，就重新接受请求，日志会说明，更新等待下一个安静时刻。
5. **就位。** `dash_go.exe` 改名为 `dash_go.old-<version>.exe`（Windows 允许给正在运行的程序改名，但不允许覆盖），`dash_go.new.exe` 改名为 `dash_go.exe`。如果失败，旧程序恢复原名，重新接受请求，并且在清单变化之前不再尝试这个版本。保留最新的 3 个旧程序。写入一个标记文件 `dash_go.exe.update.json`。
6. **停止并启动。** 服务关闭（读取数据的请求最多有 10 秒结束），然后启动新程序（第 4 节）。新程序读取并删除标记，并在日志中写 `the program was updated`，包括旧版本、新版本以及这次发布等了多久。

从停止到启动的一两秒内服务没有应答（前面的 nginx 回答 502）；做轮询的页面会自己重试。人们保持登录状态（会话是签名的 cookie，密钥没有变）。`hc_scan` 的草稿保存在 Redis（`draft_store` 为 `redis`，默认值），不会丢失；`draft_store` 为 `memory` 时，每次重启都会丢失（模块在启动时会写警告）。

## 4. 新程序如何启动（`update_restart`）

| 值 | 发生什么 | 适用于 |
|---|---|---|
| `supervise`（默认） | 最初启动的进程留下来，作为一个小的**启动器**：它在同一个控制台中，用同样的文件夹和环境，把新程序作为子进程启动；每当子进程以退出码 **75** 退出，就再次启动它。Ctrl+C 会停止子进程，从而停止启动器。启动器被杀掉时（被结束的计划任务、`Stop-Process`），子进程会自己停止：它的标准输入是一个只有启动器持有的管道。之后的更新由子进程完成，它以 75 退出，由启动器再次启动。 | 手动启动、在控制台窗口中运行，或由计划任务启动的程序。 |
| `exit` | 新程序就位后，进程以退出码 **75** 结束。需要由服务管理器再次启动程序。 | NSSM，或会重启已退出程序的任务。 |

启动器是最先启动的那个进程，它运行的是 `dash_go.old-<version>.exe`（它启动时所用的文件，已被改名）。所以这个文件会一直保留到启动器停止，启动器自己也不会变新：要让它换成新程序，停止这个进程，再次启动 `dash_go.exe`。

## 5. 发布一个版本

1. 在写有版本号的所有地方提高版本（版本提交里列出的地方：`VERSION`、`internal/version/version.go`、`internal/mods/<name>/manifest.json` 以及模块的 `ModuleVersion`、`README.md`、各文档的“更新日期”行）。
2. 运行脚本，指定 Dash API 作为 `static/cd` 提供的文件夹。在开发机上是 `D:\Programs\Python\static\cd`。服务器读取的是它的 `update_url` 所指的 API：s2 的 API 提供的是 s2 自己的 `static\cd` 文件夹，所以要把 `-OutDir` 指向那个文件夹的路径（例如共享路径），或者先发布到一个临时文件夹，再把两个文件复制过去，先复制程序，最后复制清单：

```powershell
.\scripts\release.ps1 -OutDir D:\Programs\Python\static\cd
```

它会运行 `go test`，用 `scripts/build.ps1` 构建（所以版本和模块与正常构建相同），询问程序的版本，计算大小和哈希，通过临时文件名把 `dash_go.exe` 复制进文件夹，最后写入 `dash_go.json`（不带字节顺序标记的 UTF-8，经由临时文件）。

| 参数 | 含义 |
|---|---|
| `-OutDir` | 发布文件夹。默认 `<repo>\release`（不提交）。 |
| `-Modules` | 要构建的模块，同 `build.ps1`。默认：全部。 |
| `-BaseUrl` | 只在程序的地址与清单不同时使用：文件夹的地址，例如 `http://host/api/static/cd/`。此时 `package_url` 写完整地址。 |
| `-SkipTests` | 不运行 `go test`。 |
| `-Version` | 发布这个版本，而不是 `VERSION` 中的版本。只用于在临时文件夹中演练。 |

3. 读取这个文件夹的服务器（`update_url`）在下一次查看时发现新版本，并在下一个安静时刻安装。Dash API 的静态文件夹在运行 Python API 的服务器（s2）上；文件夹在另一台电脑上的服务器通过 http 读取。

不要把演练的发布放进真实的文件夹：所有读取它的服务器都会安装它。

## 6. 为某台服务器打开

| `dash_go.json` 中的键 | 默认值 | 含义 |
|---|---|---|
| `update_url` | `""`（关闭） | 清单的位置：`http(s)` 地址（文件的地址，或文件夹的地址，末尾带斜杠），或者文件或文件夹的绝对路径。不能带用户名、密码、查询串或片段。 |
| `update_interval_minutes` | `10` | 两次查看清单之间的分钟数，1 到 1440。 |
| `update_restart` | `supervise` | `supervise` 或 `exit`（第 4 节）。 |

错误的值会在启动时被拒绝并指出键名（`dash_go.exe -check` 也会说）。`-check` 和启动日志会说明设置：`automatic update http://localhost/api/static/cd/dash_go.json every 10m0s, restart supervise`，或者 `automatic update off`。

要**只为 s2 打开**，把键写进该主机的 patch 文件（与 `dash_go.json` 同目录的 `dash_go_patch_<hostname>.json`；patch 文件会整体替换 `dash_go.json` 中同名的顶层键）：

```json
{ "update_url": "http://localhost/api/static/cd/dash_go.json" }
```

在 s2 上，`localhost` 是 s2 自己的 nginx，它把 `/api` 转发给 s2 的 Python API（那里的 `api_server` 是 `localhost`）。第一次的步骤：

1. 0.1.3 的第一次安装要手动：正在运行的 0.1.2 没有更新程序。停止 dash_go，把 0.1.3 发布包中的 `dash_go.exe` 复制过去覆盖，加上这个设置，再启动。
2. 运行 dash_go 的账号必须有权在 `dash_go.exe` 所在文件夹中创建文件和改名。启动时会检查，不能时日志会警告：`automatic update cannot put a new program in the folder of this one`。
3. 从下一个版本开始，用 `release.ps1` 发布然后等待。服务器的日志会说明发生了什么。

## 7. 回退

- **所有服务器：** 把旧的构建用它自己的版本号发布出去（在旧的源码上运行 `release.ps1`，或把它的 `dash_go.exe` 连同旧版本的清单复制过去）。任何不同的版本都是更新。
- **单台服务器，手动：** 停止进程（`supervise` 模式下是启动器：子进程会随它一起停止），把 `dash_go.exe` 改成别的名字，把 `dash_go.old-<version>.exe` 复制为 `dash_go.exe`，再启动。然后还要去掉 `update_url`（或者发布旧版本）；否则服务器会在下一次查看时重新安装这个发布。

## 8. 故障排查

所有这些行都在服务的日志里（`logs\dash_go-YYYYMMDD.log` 和控制台）；更新程序写的行以 `automatic update:` 开头。

| 日志内容 | 含义和处理 |
|---|---|
| `a step failed ... step=check error="localhost answered 202 Accepted"` | 清单不在文件夹里（静态服务器对不存在的文件回答 202 和一张图片）。检查 `update_url` 的地址，以及 `dash_go.json` 是否在那里。 |
| `a step failed ... step=check`，文字不同 | 清单无法访问或无法读取（服务器宕机、地址错误、清单无效）。服务保持现状运行；相同的文字在查看成功之前只记录一次。 |
| `step=download error="... not the one the manifest describes"` | 程序的大小或 sha256 与清单不一致：发布还在复制中，或者清单与程序不匹配。下一次查看时重试。 |
| `step=program error="the program of the release says it is version X, but the manifest says Y"` | 清单和程序不一致。在清单变化之前不会再试。请用 `release.ps1` 重新发布。 |
| `step=program error="the new program does not accept the settings of this service: ..."` | `dash_go.new.exe <参数> -check` 失败了；文字是它给出的原因（新版本不接受的某个设置）。请修正设置。下一次查看时重试。 |
| `step=install error="the new program could not be put in place ..."` | 文件无法改名（权限、杀毒软件）。旧程序已恢复原位，并重新接受请求。 |
| `automatic update cannot put a new program in the folder of this one` | 这个账号不能写入 `dash_go.exe` 所在的文件夹。 |
| `the new version waits for a quiet moment busy="..."` | 有人正在使用服务：`N request(s) that change something are being served`、`the last request that changed something ended Ns ago`，或 `N Windows sign-in(s) are under way`。它会自己继续。记录一次，之后每 5 分钟一次。 |
| `requests that were running did not end in time` | 有改变数据的请求一分钟内没有结束。重新接受请求，更新等待下一个安静时刻。 |
| `the new program is in place; the service restarts ... waited=...` | 更新正在进行。`previous_program` 是保留下来的旧程序。 |
| `the program was updated from=X to=Y` | 新程序已启动并找到了标记。 |
| `the program asks to be started again` | `supervise` 模式：启动器启动新程序。 |
| `exiting so that the new version is started code=75` | `exit` 模式：需要由服务管理器再次启动程序。 |
| `the launcher that started this program is gone, so this program stops` | 启动器被杀掉了；子进程随之停止。请再次启动 `dash_go.exe`。 |
| `the new program could not be started` | `supervise` 模式：这个文件无法运行。把某个 `dash_go.old-<version>.exe` 复制回去（第 7 节）。 |
| 完全没有关于更新的日志 | 这台主机没有设置 `update_url`（`-check` 会说 `automatic update off`），或者第一次查看（启动 10 秒后）还没发生。 |

如果发布已经更新而某台服务器的版本始终不变：查看 `-check`（服务读取的文件里设置了 `update_url` 吗？）、启动后头几分钟的日志，以及清单中的 `latest_version`（它必须与服务器在 `dash_go started version=...` 里报告的版本不同）。

## 9. 设计决定与限制

- **需要主动开启，页面里没有开关。** 更新程序没有页面，也没有按钮，日志就是界面。要让服务器马上去查看发布，重启服务即可：启动 10 秒后就会第一次查看。
- **“没有人在点击或提交”**指的是改变数据的请求。10 秒（`cmd/dash_go/update.go` 中的 `quietFor`）和每 2 秒一次的查看是固定值，不是设置。只是在看页面的人不会拖住更新；重启只会丢掉页面在浏览器中单独持有的内容（输入了但没有发送的文字）。浏览器会短暂显示网络错误或 503 的消息。
- **谁能写入发布文件夹，谁就控制所有读取它的服务器。** sha256 只能防止损坏的副本；清单和程序来自同一个地方。请把对 `static\cd` 的写权限，控制得和在这些服务器上运行程序的权限一样严。地址在公司内网中是 `http`；也可以使用 `https` 地址，或有独立权限的共享文件夹。
- **没有自动回退。** 启动后失败的新程序不会被旧程序替换；第 7 节是回退的办法。新程序只有在 `-version` 和 `-check` 通过之后才会安装，这能挡住常见的原因（它不接受的某个设置）。
- **启动器不会更新自己**（第 4 节）。
- **一个发布供所有读取该文件夹的服务器使用。** 需要另一个版本的服务器去读另一个文件夹（`update_url`）。
- 更新程序只使用标准库，没有增加依赖。

## 10. 验证状态

已完成（2026-11-20，版本 0.1.3）：

- 单元测试：`internal/core/update`（清单、经由 http 和文件的来源（包括静态服务器的 202 回答）、带大小和哈希检查的下载、中断的下载、正在运行的程序的替换、保留的程序、标记、带运行中服务参数的检查器及其原因、启动器以及启动器被杀掉时会停止的子进程、用假服务测试的更新程序：等待安静时刻、排空、排空的时间限制、取消、不重复记录的失败）、`internal/core/server`（闸门：计数、安静时间、两种语言的 503）、`internal/core/auth`（等待中的登录）、`internal/core/config`（三个键及其错误）、`internal/core/shell`（返回按钮）。
- 真实的 0.1.3 可执行文件在开发机上运行：使用其他端口的临时配置、真实的 Python 配置、API 和 Redis，以及由 `scripts/release.ps1` 构建、由临时 Web 服务器提供的演练发布 0.1.4 和 0.1.5：安静的服务在启动 10 秒后完成了更新（下载、检查、排空、替换、启动器启动新程序、读取标记）；每 2 秒一次 POST 时更新在等待（`busy="the last request that changed something ended 0s ago"`），在最后一次 POST 11 秒后安装（`waited=1m24s`），48 次 POST 全部回答 204；第二次更新由受监督的子进程完成，它以 75 退出并被再次启动；杀掉启动器后子进程在几秒内停止；`update_restart` 为 `exit` 时以日志行 `code=75` 结束，并留下新程序和标记；sha256 错误的发布，以及程序报告的版本与清单不符的发布，都被拒绝并只记录一次，服务继续运行；检查器读取了真实 `-check` 对其不接受的配置给出的消息（`configuration error: ...` 和 `level=ERROR` 行，不含密码）。
- 同样的更新通过开发机上真实的 Dash API 和运行中的 nginx 完成：临时文件夹 `static/cd/dash_go_rehearsal/`（之后已删除），`update_url` 为 `http://localhost/api/static/cd/dash_go_rehearsal/`：清单回答 200，不存在的文件回答 202 和 `image/jpeg`，HEAD 回答 405（更新程序只用 GET），23 MB 的程序被下载并安装。
- 模块页面的顶栏在浏览器（编辑器的内置浏览器）中，浅色和深色主题、英文和中文、以及 390 px 宽度下的样子：标题使用主题的蓝色（`#0b63c5`、`#4da3ff`，1.3 rem，字重 600），旁边是返回按钮，点击后去往同一主机的 `/`。

没有验证：

- s2 上的部署：没有向真实的 `static\cd` 发布任何东西，也没有改动任何真实服务器。0.1.3 的第一次安装以及 s2 上的 `update_url` 仍需手动完成。
- `update_restart` 为 `exit` 时与真实服务管理器（NSSM、计划任务）配合：退出码 75 写在日志里，但没有用任何管理器重新启动程序。
- 在控制台窗口中，对带有启动器和子进程的程序按 Ctrl+C（按设计，启动器忽略该信号，子进程收到后关闭；没有测试覆盖它，测试运行时也没有控制台）。
- 有发布在等待时正在进行的 Windows 登录（计数有单元测试，没有用真实的登录服务），以及人们真正在扫码时的更新。
- `https` 地址、共享文件夹（`\\server\share`）作为发布文件夹、杀毒软件占用新文件。
- s1 和 s2 的 nginx 之后的返回按钮（链接是 `/`，只对临时服务测试过）。
