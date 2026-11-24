# English

## Overview

`dash_go` is the Go side of the Python `dash` application. Features are migrated one module at a time. Every module can be built into the executable or left out, so a package contains exactly the modules that were chosen.

Current version: **0.1.8** (see `VERSION`).

| Module | What it does | Document |
|---|---|---|
| `hc_scan` | A shift leader scans employee badges; every scan assigns a machine or activity by the MTR record of the person. The result is a draft that the leader checks and confirms. | [docs/hc_scan_page.md](docs/hc_scan_page.md) |

Every page has the same frame, the **shell**: the header with the CIM EDGE logo, a light / dark theme button, an English / Chinese switch, a Documentation button (user guide for the current page) and a Guide button (interactive step-by-step tour), the signed-in user and sign-in / sign-out; plus a login page of its own (account and password, or the Windows sign-in) next to the link that Dash builds. The pages of a module also show the name of the module and a **Back** button to the root of the server. It is described in [docs/shell_and_login.md](docs/shell_and_login.md).

A server that is told where the releases are (`update_url`) **updates itself**: it reads a manifest from the static folder of the Dash API, downloads and checks the new program, waits until nobody is clicking or submitting, and restarts. See [docs/auto_update.md](docs/auto_update.md).

The documents of the core and of all modules are indexed in [docs/README.md](docs/README.md). Engineering rules for this repository are in [AGENTS.md](AGENTS.md).

## Layout

```text
cmd/dash_go/               entry point; mod_<name>.go links one module in (build tag !nomod_<name>)
internal/core/             shared infrastructure: config, auth (login, session), dashapi (Python backend client),
                           pyobj (reader of the Python pickle), i18n (English / Chinese texts), shell (header,
                           theme, login page, landing page), module registry, HTTP server helpers, logging
internal/mods/<name>/      one folder per module: code, tests, web files (embedded), manifest.json
internal/version/          version of the binary (set by the build script)
scripts/build.ps1          test, build and package the selected modules
scripts/release.ps1        build a release (program + manifest) for the automatic update
config/*.example.json      configuration templates (real configuration files are not committed)
deploy/                    nginx location for the service
docs/                      documentation, one file per module
```

A module registers itself from `init()` with `module.Register`, serves its routes below `/dash_go/<name>/` and reads its settings from `modules.<name>` of `dash_go.json`. Modules depend on `internal/core`, never on each other. To add a module: create `internal/mods/<name>/` with a `manifest.json`, add `cmd/dash_go/mod_<name>.go` (copy the one of `hc_scan` and change the name), document it in `docs/`. A page of a module gives the shell a template that defines its content (`env.Shell.NewPage`), and every text of the module goes into `i18n/en.json` and `i18n/zh.json` with the prefix of the module ([docs/shell_and_login.md](docs/shell_and_login.md), section 5.5).

## Build and package

Go 1.27 or later. If `go` is not on `PATH`, `scripts\build.ps1` finds `C:\Program Files\Go\bin`.

```powershell
.\scripts\build.ps1                         # package every module
.\scripts\build.ps1 -Modules hc_scan        # package only hc_scan
.\scripts\build.ps1 -Modules a,b,c          # several modules
.\scripts\build.ps1 -List                   # list the modules
```

The script runs the tests, builds with a `nomod_<name>` tag for every module that was not selected, checks with `dash_go.exe -version` that exactly the selected modules are inside, and writes `dist\dash_go-<version>-<modules>-windows-amd64\` and a zip next to it: the executable, `config\dash_go.example.json` reduced to the selected modules, the nginx example, the documents of the selected modules and of the core (shell, automatic update), `MODULES.txt`.

A plain `go build ./cmd/dash_go` includes every module.

To publish a version for the servers that update themselves, raise the version (`VERSION`, `internal/version/version.go`, the `version` of `manifest.json` and `ModuleVersion` of the module, the two lines "Current version" below, the "Updated" line of the documents) and run `release.ps1`. It runs the tests, builds like `build.ps1` and writes `dash_go.exe` and the manifest `dash_go.json` into the release folder, the manifest last:

```powershell
.\scripts\release.ps1 -OutDir D:\Programs\Python\static\cd     # the folder the Dash API serves as /api/static/cd/
```

Details, the settings of the servers and what to do when something goes wrong: [docs/auto_update.md](docs/auto_update.md).

## Configuration

Settings come from two layers of JSON files: the config folder of the Python application (`DASH_GO_DASH_CONFIG_DIR`: database, Redis, API secret, process list) and `dash_go.json` (`DASH_GO_CONFIG_DIR`, default `./config`: listen address, module settings). `patch_<hostname>.json` files work like in the Python application. Secrets can be given by the environment variables `DASH_GO_PG_PASSWORD`, `DASH_GO_REDIS_PASSWORD`, `DASH_GO_API_SALT` and `DASH_GO_API_SALT_2`. The login page needs `api_server` (or `dash_api_url`); the Windows sign-in also `sso_server` and `api.salt_2` of the Python configuration. `api_server` comes from the Python configuration including the patch file of the host: on a development machine whose patch file points it at the production server, set `dash_api_url` (for example `http://localhost/api`). Keys of the sign-in, the theme and the pictures: [docs/shell_and_login.md](docs/shell_and_login.md), section 8. Never commit real configuration files or write secrets into documents or logs. Details: [docs/hc_scan_page.md](docs/hc_scan_page.md), section 9.

## Run

```powershell
$env:DASH_GO_DASH_CONFIG_DIR = '<config folder of the Python application>'
.\dash_go.exe -check        # load the configuration and the modules, say which Python API is used, whether the password and Windows sign-in are on and whether the automatic update is on, test the connections (PostgreSQL, Redis, the Dash API), then exit; exit code 1 when a connection does not work
.\dash_go.exe               # serve on the address of "listen", below /dash_go/
.\dash_go.exe -version      # version and compiled-in modules
```

## Tests

```powershell
go vet ./...
go test ./...
```

The PostgreSQL tests are skipped unless `DASH_GO_TEST_PG` names a **scratch** PostgreSQL (they create and drop the schema `hcscan_it`), for example `postgres://postgres@127.0.0.1:55432/Auth?sslmode=disable`. A page development server with demo data: see the comment of `TestDevServer` in `internal/mods/hc_scan/devserver_test.go`.

---

# 中文

## 概述

`dash_go` 是 Python `dash` 应用在 Go 一侧的实现。功能按模块逐个迁移。每个模块都可以编进可执行文件，也可以不编进去，所以一个发布包里只包含所选的模块。

当前版本：**0.1.8**（见 `VERSION`）。

| 模块 | 功能 | 文档 |
|---|---|---|
| `hc_scan` | 领班扫描员工工牌；每次扫描根据此人的 MTR 记录分派 machine 或 activity。结果是草稿，由领班检查并确认。 | [docs/hc_scan_page.md](docs/hc_scan_page.md) |

每个页面都有同样的外框，即 **shell**：带 CIM EDGE logo 的顶栏、浅色 / 深色主题按钮、中英文切换、文档按钮（当前页面的使用手册）和导览按钮（交互式分步导览）、当前登录用户以及登录 / 退出登录；另外还有自带的登录页（账号密码，或 Windows 登录），与 Dash 生成的链接并存。模块的页面还会显示模块名称，以及一个通向服务器根路径的**返回**按钮。详见 [docs/shell_and_login.md](docs/shell_and_login.md)。

知道发布位置的服务器（`update_url`）会**自己更新**：它从 Dash API 的静态文件夹读取清单，下载并检查新程序，等到没有人在点击或提交，然后重启。见 [docs/auto_update.md](docs/auto_update.md)。

核心和所有模块的文档索引在 [docs/README.md](docs/README.md)。本仓库的工程规则见 [AGENTS.md](AGENTS.md)。

## 目录结构

```text
cmd/dash_go/               入口；mod_<name>.go 链接一个模块（构建标签 !nomod_<name>）
internal/core/             共享基础设施：配置、auth（登录、会话）、dashapi（Python 后端客户端）、
                           pyobj（Python pickle 的读取器）、i18n（中英文文字）、shell（顶栏、主题、
                           登录页、首页）、模块注册表、HTTP 服务辅助、日志
internal/mods/<name>/      每个模块一个文件夹：代码、测试、web 文件（嵌入）、manifest.json
internal/version/          可执行文件的版本（由构建脚本设置）
scripts/build.ps1          测试、构建并打包所选模块
scripts/release.ps1        为自动更新构建一个发布（程序 + 清单）
config/*.example.json      配置模板（真实配置文件不提交）
deploy/                    服务的 nginx location
docs/                      文档，每个模块一个文件
```

模块在 `init()` 中用 `module.Register` 注册自己，在 `/dash_go/<name>/` 之下提供路由，并从 `dash_go.json` 的 `modules.<name>` 读取设置。模块只依赖 `internal/core`，互相之间不依赖。新增模块的步骤：创建带 `manifest.json` 的 `internal/mods/<name>/`，添加 `cmd/dash_go/mod_<name>.go`（复制 `hc_scan` 的并改名），并在 `docs/` 中写文档。模块的页面给 shell 一个定义其内容的模板（`env.Shell.NewPage`），模块的每一段文字都放进 `i18n/en.json` 和 `i18n/zh.json`，并带上模块的前缀（[docs/shell_and_login.md](docs/shell_and_login.md) 第 5.5 节）。

## 构建与打包

需要 Go 1.27 或更高版本。如果 `go` 不在 `PATH` 中，`scripts\build.ps1` 会查找 `C:\Program Files\Go\bin`。

```powershell
.\scripts\build.ps1                         # 打包所有模块
.\scripts\build.ps1 -Modules hc_scan        # 只打包 hc_scan
.\scripts\build.ps1 -Modules a,b,c          # 打包多个模块
.\scripts\build.ps1 -List                   # 列出模块
```

脚本会运行测试，对每个未选中的模块加上 `nomod_<name>` 标签进行构建，用 `dash_go.exe -version` 检查里面恰好是所选的模块，并生成 `dist\dash_go-<version>-<modules>-windows-amd64\` 以及旁边的 zip：可执行文件、只保留所选模块的 `config\dash_go.example.json`、nginx 示例、所选模块的文档和核心（shell、自动更新）的文档，以及 `MODULES.txt`。

直接 `go build ./cmd/dash_go` 会包含所有模块。

要为会自己更新的服务器发布一个版本，先提高版本号（`VERSION`、`internal/version/version.go`、模块 `manifest.json` 的 `version` 和 `ModuleVersion`、下面两行“当前版本”、各文档的“更新日期”行），再运行 `release.ps1`。它会运行测试，像 `build.ps1` 一样构建，并把 `dash_go.exe` 和清单 `dash_go.json` 写进发布文件夹，清单最后写：

```powershell
.\scripts\release.ps1 -OutDir D:\Programs\Python\static\cd     # Dash API 作为 /api/static/cd/ 提供的文件夹
```

详细说明、服务器的设置以及出问题时怎么办：[docs/auto_update.md](docs/auto_update.md)。

## 配置

设置来自两层 JSON 文件：Python 应用的配置文件夹（`DASH_GO_DASH_CONFIG_DIR`：数据库、Redis、API 密钥、process 列表）和 `dash_go.json`（`DASH_GO_CONFIG_DIR`，默认 `./config`：监听地址、模块设置）。`patch_<hostname>.json` 的用法与 Python 应用相同。机密信息可以通过环境变量 `DASH_GO_PG_PASSWORD`、`DASH_GO_REDIS_PASSWORD`、`DASH_GO_API_SALT` 和 `DASH_GO_API_SALT_2` 提供。登录页需要 `api_server`（或 `dash_api_url`）；Windows 登录还需要 Python 配置中的 `sso_server` 和 `api.salt_2`。`api_server` 来自 Python 配置，包括本机的 patch 文件：在开发机上，如果 patch 文件把它指向了生产服务器，请设置 `dash_api_url`（例如 `http://localhost/api`）。登录、主题和头像的配置项：[docs/shell_and_login.md](docs/shell_and_login.md) 第 8 节。不要提交真实的配置文件，也不要把机密信息写进文档或日志。详情见 [docs/hc_scan_page.md](docs/hc_scan_page.md) 第 9 节。

## 运行

```powershell
$env:DASH_GO_DASH_CONFIG_DIR = '<Python 应用的配置文件夹>'
.\dash_go.exe -check        # 加载配置和模块，说明使用哪个 Python API、密码登录和 Windows 登录是否开启、自动更新是否开启，测试连接（PostgreSQL、Redis、Dash API），然后退出；有连接不通时退出码为 1
.\dash_go.exe               # 在 "listen" 指定的地址上，于 /dash_go/ 之下提供服务
.\dash_go.exe -version      # 版本和已编入的模块
```

## 测试

```powershell
go vet ./...
go test ./...
```

除非 `DASH_GO_TEST_PG` 指向一个**临时的** PostgreSQL（测试会创建并删除 schema `hcscan_it`），否则 PostgreSQL 测试会被跳过，例如 `postgres://postgres@127.0.0.1:55432/Auth?sslmode=disable`。带演示数据的页面开发服务器：见 `internal/mods/hc_scan/devserver_test.go` 中 `TestDevServer` 的注释。
