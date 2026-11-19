# English

## Overview

`dash_go` is the Go side of the Python `dash` application. Features are migrated one module at a time. Every module can be built into the executable or left out, so a package contains exactly the modules that were chosen.

Current version: **0.1.0** (see `VERSION`).

| Module | What it does | Document |
|---|---|---|
| `hc_scan` | A shift leader scans employee badges; every scan assigns a machine or activity by the MTR record of the person. The result is a draft that the leader checks and confirms. | [docs/hc_scan_page.md](docs/hc_scan_page.md) |

The documents of all modules are indexed in [docs/README.md](docs/README.md). Engineering rules for this repository are in [AGENTS.md](AGENTS.md).

## Layout

```text
cmd/dash_go/               entry point; mod_<name>.go links one module in (build tag !nomod_<name>)
internal/core/             shared infrastructure: config, auth (login tokens), dashapi (Python backend client),
                           module registry, HTTP server helpers, logging
internal/mods/<name>/      one folder per module: code, tests, web files (embedded), manifest.json
internal/version/          version of the binary (set by the build script)
scripts/build.ps1          test, build and package the selected modules
config/*.example.json      configuration templates (real configuration files are not committed)
deploy/                    nginx location for the service
docs/                      documentation, one file per module
```

A module registers itself from `init()` with `module.Register`, serves its routes below `/dash_go/<name>/` and reads its settings from `modules.<name>` of `dash_go.json`. Modules depend on `internal/core`, never on each other. To add a module: create `internal/mods/<name>/` with a `manifest.json`, add `cmd/dash_go/mod_<name>.go` (copy the one of `hc_scan` and change the name), document it in `docs/`.

## Build and package

Go 1.27 or later. If `go` is not on `PATH`, `scripts\build.ps1` finds `C:\Program Files\Go\bin`.

```powershell
.\scripts\build.ps1                         # package every module
.\scripts\build.ps1 -Modules hc_scan        # package only hc_scan
.\scripts\build.ps1 -Modules a,b,c          # several modules
.\scripts\build.ps1 -List                   # list the modules
```

The script runs the tests, builds with a `nomod_<name>` tag for every module that was not selected, checks with `dash_go.exe -version` that exactly the selected modules are inside, and writes `dist\dash_go-<version>-<modules>-windows-amd64\` and a zip next to it: the executable, `config\dash_go.example.json` reduced to the selected modules, the nginx example, the documents of the selected modules, `MODULES.txt`.

A plain `go build ./cmd/dash_go` includes every module.

## Configuration

Settings come from two layers of JSON files: the config folder of the Python application (`DASH_GO_DASH_CONFIG_DIR`: database, Redis, API secret, process list) and `dash_go.json` (`DASH_GO_CONFIG_DIR`, default `./config`: listen address, module settings). `patch_<hostname>.json` files work like in the Python application. Secrets can be given by the environment variables `DASH_GO_PG_PASSWORD`, `DASH_GO_REDIS_PASSWORD` and `DASH_GO_API_SALT`. Never commit real configuration files or write secrets into documents or logs. Details: [docs/hc_scan_page.md](docs/hc_scan_page.md), section 9.

## Run

```powershell
$env:DASH_GO_DASH_CONFIG_DIR = '<config folder of the Python application>'
.\dash_go.exe -check        # load the configuration and the modules, then exit
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

当前版本：**0.1.0**（见 `VERSION`）。

| 模块 | 功能 | 文档 |
|---|---|---|
| `hc_scan` | 领班扫描员工工牌；每次扫描根据此人的 MTR 记录分派 machine 或 activity。结果是草稿，由领班检查并确认。 | [docs/hc_scan_page.md](docs/hc_scan_page.md) |

所有模块的文档索引在 [docs/README.md](docs/README.md)。本仓库的工程规则见 [AGENTS.md](AGENTS.md)。

## 目录结构

```text
cmd/dash_go/               入口；mod_<name>.go 链接一个模块（构建标签 !nomod_<name>）
internal/core/             共享基础设施：配置、auth（登录 token）、dashapi（Python 后端客户端）、
                           模块注册表、HTTP 服务辅助、日志
internal/mods/<name>/      每个模块一个文件夹：代码、测试、web 文件（嵌入）、manifest.json
internal/version/          可执行文件的版本（由构建脚本设置）
scripts/build.ps1          测试、构建并打包所选模块
config/*.example.json      配置模板（真实配置文件不提交）
deploy/                    服务的 nginx location
docs/                      文档，每个模块一个文件
```

模块在 `init()` 中用 `module.Register` 注册自己，在 `/dash_go/<name>/` 之下提供路由，并从 `dash_go.json` 的 `modules.<name>` 读取设置。模块只依赖 `internal/core`，互相之间不依赖。新增模块的步骤：创建带 `manifest.json` 的 `internal/mods/<name>/`，添加 `cmd/dash_go/mod_<name>.go`（复制 `hc_scan` 的并改名），并在 `docs/` 中写文档。

## 构建与打包

需要 Go 1.27 或更高版本。如果 `go` 不在 `PATH` 中，`scripts\build.ps1` 会查找 `C:\Program Files\Go\bin`。

```powershell
.\scripts\build.ps1                         # 打包所有模块
.\scripts\build.ps1 -Modules hc_scan        # 只打包 hc_scan
.\scripts\build.ps1 -Modules a,b,c          # 打包多个模块
.\scripts\build.ps1 -List                   # 列出模块
```

脚本会运行测试，对每个未选中的模块加上 `nomod_<name>` 标签进行构建，用 `dash_go.exe -version` 检查里面恰好是所选的模块，并生成 `dist\dash_go-<version>-<modules>-windows-amd64\` 以及旁边的 zip：可执行文件、只保留所选模块的 `config\dash_go.example.json`、nginx 示例、所选模块的文档和 `MODULES.txt`。

直接 `go build ./cmd/dash_go` 会包含所有模块。

## 配置

设置来自两层 JSON 文件：Python 应用的配置文件夹（`DASH_GO_DASH_CONFIG_DIR`：数据库、Redis、API 密钥、process 列表）和 `dash_go.json`（`DASH_GO_CONFIG_DIR`，默认 `./config`：监听地址、模块设置）。`patch_<hostname>.json` 的用法与 Python 应用相同。机密信息可以通过环境变量 `DASH_GO_PG_PASSWORD`、`DASH_GO_REDIS_PASSWORD` 和 `DASH_GO_API_SALT` 提供。不要提交真实的配置文件，也不要把机密信息写进文档或日志。详情见 [docs/hc_scan_page.md](docs/hc_scan_page.md) 第 9 节。

## 运行

```powershell
$env:DASH_GO_DASH_CONFIG_DIR = '<Python 应用的配置文件夹>'
.\dash_go.exe -check        # 加载配置和模块后退出
.\dash_go.exe               # 在 "listen" 指定的地址上，于 /dash_go/ 之下提供服务
.\dash_go.exe -version      # 版本和已编入的模块
```

## 测试

```powershell
go vet ./...
go test ./...
```

除非 `DASH_GO_TEST_PG` 指向一个**临时的** PostgreSQL（测试会创建并删除 schema `hcscan_it`），否则 PostgreSQL 测试会被跳过，例如 `postgres://postgres@127.0.0.1:55432/Auth?sslmode=disable`。带演示数据的页面开发服务器：见 `internal/mods/hc_scan/devserver_test.go` 中 `TestDevServer` 的注释。
