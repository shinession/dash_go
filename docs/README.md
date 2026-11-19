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

## Index

### Modules

| Document | Covers |
|---|---|
| [hc_scan_page.md](hc_scan_page.md) | `hc_scan`: the HC Scan Assign page (`/dash_go/hc_scan/`). A shift leader scans employee badges, every scan assigns a machine or activity by the MTR level of the person (at least Lv1, Lv3 before Lv2 before Lv1, the last 7 days second; leaders go to the Group Leader row), the result is a draft that is saved to `t_daily_attn_data` only when the leader confirms. Warnings, write rules, login hand-over from Dash, configuration, deployment on s2, troubleshooting and verification status |

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

## 索引

### 模块

| 文档 | 内容 |
|---|---|
| [hc_scan_page.md](hc_scan_page.md) | `hc_scan`：HC Scan Assign 页面（`/dash_go/hc_scan/`）。领班扫描员工工牌，每次扫描根据此人的 MTR 等级分派 machine 或 activity（至少 Lv1，Lv3 优先于 Lv2 优先于 Lv1，其次是最近 7 天；leader 去 Group Leader 行），结果是草稿，只有领班确认后才保存到 `t_daily_attn_data`。包括警告、写入规则、从 Dash 的登录交接、配置、s2 上的部署、故障排查和验证状态 |
