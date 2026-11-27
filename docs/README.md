# English

## Overview

This folder holds the documentation of the modules of `dash_go`. [AGENTS.md](../AGENTS.md) records only what applies to every module (engineering rules, infrastructure and debugging lessons). The description of a single module, and the lessons learned about it, are written in that module's file in this folder. Before changing or debugging a module, look it up in the index below. The project overview, the layout and the build and package commands are in the [README](../README.md).

## Conventions

- One file per module, named after the module in `snake_case`, with the kind of module as suffix where it helps (`hc_scan_page.md`).
- Every document has a complete English version first and a complete Simplified Chinese version second, with the same structure ([AGENTS.md](../AGENTS.md), section 11).
- Describe the actual implementation and say what was not verified (section 12 of `AGENTS.md`).
- Write the lessons learned from a real defect of the module in a Troubleshooting section of its file: symptoms, root cause, rule, how to confirm, and what was ruled out.
- A module lists its documents in the `docs` field of its `manifest.json`; `scripts/build.ps1` copies them into the package.
- Add every new document to the index below, in both languages.
- A document about the frame shared by all modules (header, theme, language, sign-in) goes to the "Core" table; a document about one module goes to the "Modules" table.

## Index

### Core (every module)

| Document | Covers |
|---|---|
| [shell_and_login.md](shell_and_login.md) | The frame of every page (`/dash_go/_shell/`): the header with the CIM EDGE logo, the name of the module with the Back button to the root of the server, the light / dark theme, English / Chinese (catalogs, how a module adds texts), the Documentation and Page Guide buttons (user guide pages and interactive tour with first-visit auto-start), the shared HC process picker (`dashGo.picker`) and module icons (`scanner`, `budget`), the user box and the pictures of people, and the sign-in of dash_go itself (link from Dash, account and password, Windows sign-in, limits, configuration, deployment behind proxies, troubleshooting, verification status) |
| [auto_update.md](auto_update.md) | The automatic update (since 0.1.3): a release is `dash_go.exe` and `dash_go.json` in the static folder of the Dash API (`scripts/release.ps1`); a server with `update_url` reads the manifest, downloads and checks the new program, waits until nobody is clicking or submitting, puts it in place and restarts (launcher or exit code 75). Settings, publishing, going back, troubleshooting by log line, limits and verification status |

### Modules

| Document | Covers |
|---|---|
| [hc_scan_page.md](hc_scan_page.md) | `hc_scan`: the HC Scan Assign page (`/dash_go/hc_scan/`). A shift leader scans employee badges, every scan assigns a machine or activity by the MTR level of the person (at least Lv1, Lv3 before Lv2 before Lv1, the last 7 days second; leaders go to the Group Leader row), the result is a draft that is saved to `t_daily_attn_data` only when the leader confirms. The board (which entries count for a process, support from other processes, the counters Scheduled / Assigned / Leave / To assign, the Leave and Elsewhere cards), warnings, write rules, login hand-over from Dash, configuration, deployment on s2, troubleshooting and verification status |
| [hc_budget_page.md](hc_budget_page.md) | `hc_budget`: the HC Budget Setting page (`/dash_go/hc_budget/`, since 0.2.0; updated in 0.2.1). Authorized users share the process picker and HeadCount permissions with `hc_scan` to edit machine/activity names, types, categories, and head count budgets in `t_mqpd_hc_table`; system administrators (`"T_USER_ALL"."Type" = 'admin'`, not `t_auth_group` group admins) can additionally add new machines/activities, toggle hidden visibility, and reorder per-process `display_seq` via drag-and-drop. Batch Confirm in one PostgreSQL transaction, `MQPDHeadCount.log` cache invalidation, configuration, troubleshooting and verification status |

---

# 中文

## 概述

本文件夹存放 `dash_go` 各个模块的文档。[AGENTS.md](../AGENTS.md) 只记录适用于所有模块的内容（工程规则、基础设施和调试经验）。单个模块的说明及其经验教训，写在本文件夹中该模块自己的文件里。修改或排查某个模块之前，先在下面的索引中查找它。项目概述、目录结构以及构建和打包命令见 [README](../README.md)。

## 约定

- 每个模块一个文件，用 `snake_case` 以模块命名，必要时加上模块类型作为后缀（`hc_scan_page.md`）。
- 每份文档先给出完整的英文版，再给出完整的简体中文版，两者结构一致（[AGENTS.md](../AGENTS.md) 第 11 节）。
- 描述实际实现，并说明哪些内容没有验证（`AGENTS.md` 第 12 节）。
- 该模块在真实缺陷中得到的经验，写在它自己文件的“故障排查”部分：现象、根因、规则、如何确认，以及已排除的方向。
- 模块在自己的 `manifest.json` 的 `docs` 字段中列出文档；`scripts/build.ps1` 会把它们复制到发布包里。
- 新增文档时，在下面的索引中同时补充英文和中文两个版本。
- 所有模块共用的外框（顶栏、主题、语言、登录）的文档放入“核心”表；单个模块的文档放入“模块”表。

## 索引

### 核心（所有模块）

| 文档 | 内容 |
|---|---|
| [shell_and_login.md](shell_and_login.md) | 每个页面的外框（`/dash_go/_shell/`）：带 CIM EDGE logo 的顶栏、模块名称及其旁边通向服务器根路径的返回按钮、浅色 / 深色主题、中英文（词条文件、模块如何添加文字）、文档与页面导览按钮（使用手册页面和支持首次访问自动引导的交互式导览）、共用 HC 工序选择器（`dashGo.picker`）及模块图标（`scanner`、`budget`）、用户框和人员头像，以及 dash_go 自己的登录（Dash 的链接、账号密码、Windows 登录、限流、配置、代理之后的部署、故障排查、验证状态） |
| [auto_update.md](auto_update.md) | 自动更新（自 0.1.3 起）：一个发布就是 Dash API 静态文件夹中的 `dash_go.exe` 和 `dash_go.json`（`scripts/release.ps1`）；设置了 `update_url` 的服务器读取清单，下载并检查新程序，等到没有人在点击或提交，再就位并重启（启动器或退出码 75）。设置、发布、回退、按日志行排查、限制和验证状态 |

### 模块

| 文档 | 内容 |
|---|---|
| [hc_scan_page.md](hc_scan_page.md) | `hc_scan`：HC Scan Assign 页面（`/dash_go/hc_scan/`）。领班扫描员工工牌，每次扫描根据此人的 MTR 等级分派 machine 或 activity（至少 Lv1，Lv3 优先于 Lv2 优先于 Lv1，其次是最近 7 天；leader 去 Group Leader 行），结果是草稿，只有领班确认后才保存到 `t_daily_attn_data`。包括看板（哪些 entry 算在某个 process 上、其他 process 的支援、Scheduled / Assigned / Leave / To assign 计数、Leave 和 Elsewhere 卡片）、警告、写入规则、从 Dash 的登录交接、配置、s2 上的部署、故障排查和验证状态 |
| [hc_budget_page.md](hc_budget_page.md) | `hc_budget`：HC 预算设置页面（`/dash_go/hc_budget/`，自 0.2.0 起，0.2.1 更新）。与 `hc_scan` 共用工序选择器和 HeadCount 权限，支持维护 `t_mqpd_hc_table` 中的机台/活动名称、类型、分类与预算人力；系统管理员（`"T_USER_ALL"."Type" = 'admin'`，而非 `t_auth_group` 权限组管理员）可额外新增机台/活动、切换隐藏状态，并拖动调整本工序内的 `display_seq` 显示顺序。包括单事务批量确认、`MQPDHeadCount.log` 缓存刷新、配置、故障排查和验证状态 |
