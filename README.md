# spm4a

[![test](https://github.com/ravenmk2/spm4a/actions/workflows/test.yml/badge.svg)](https://github.com/ravenmk2/spm4a/actions/workflows/test.yml)

A pm2-style process manager for Spring Boot apps, built for agentic coding.
面向 Agentic Coding 的 Spring Boot 进程管理器：Coding Agent 用它启动 / 调试 / 测试
Spring Boot 应用，人类开发者使用内置 TUI。支持 Windows / Linux / macOS。

## 特性

- daemon 按需自动拉起，CLI 经命名管道（Windows）/ Unix Socket 通讯（HTTP + JSON-RPC 2.0）
- namespace 隔离：同一项目检出支持多会话并行，互不干扰
- 随机端口分配；actuator 就绪检查与优雅停机；devtools 热重启（`spm4a reload`）
- 多 JDK 注册表与按 app 指定（`spm4a jdk scan`）；JDWP 调试注入（`--debug`）
- JVM 参数直配（`xms` / `xmx` / `jvm-opts`）；jar / maven / gradle / custom 四种启动方式
- bubbletea TUI（`spm4a tui`）；全部命令支持 `--json` 与稳定退出码

## 快速开始

```bash
go build -o spm4a ./cmd/spm4a        # 或下载 release 二进制

spm4a start -f spm4a-app.yaml        # 启动（配置文件见 docs/design.md §6）
spm4a ls                             # 查看
spm4a logs -f my-app                 # 跟随日志
spm4a tui                            # 仪表盘
```
