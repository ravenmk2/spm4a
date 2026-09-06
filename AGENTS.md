# AGENTS

spm4a — 面向 Agentic Coding 的 Spring Boot 进程管理器（类 pm2）。Go 实现，单一二进制，
CLI + daemon + TUI 三形态。支持 Windows / Linux / macOS。

## 常用命令

- 构建：`go build ./...`；发布构建（全平台矩阵）：`./build.sh`
- 测试：`go test ./...`（e2e 需要 JDK 17+ 与 Maven；无 gradle 时 gradle 用例自动 skip）
- 质量门槛：`go vet ./...` + `gofmt -l .` 必须为空
- e2e 隔离：测试通过 `SPM4A_HOME`（临时目录）+ `SPM4A_NAMESPACE` 获得独立 daemon 实例

## 约定

- `docs/design.md` 是设计权威；改动行为时先同步文档对应小节
- yaml 配置字段 kebab-case；JSON-RPC / Go JSON 字段 camelCase
- CLI 退出码语义固定为 0/1/2/3/4/5/6（见 design.md §14），新增错误路径要归档
- Windows/Unix 平台差异走 build tag 分文件（`*_windows.go` / `*_unix.go`）
- 通用逻辑抽成无 build tag 的纯函数，保证 Windows 本机也能单测 unix 分支

## 文档索引

- `docs/design.md` : 系统设计全貌
