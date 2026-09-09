# spm4a 系统设计

spm4a 是一个为 Agentic Coding 场景设计的 Spring Boot 应用进程管理器（类似 pm2），
支持 Windows / Linux / macOS。Coding Agent 通过它管理 Spring Boot 应用的进程，
完成调试与测试；人类开发者使用内置 TUI。

技术栈：Go；TUI 使用 bubbletea。

## 1. 设计目标与非目标

目标：

- CLI 对 agent 友好：每条命令一次请求-响应、结构化输出（`--json`）、稳定退出码、
  幂等语义、无交互式提问。
- daemon 由 CLI 按需自动拉起，对调用方透明。
- Spring Boot 特色：自动注入配置、随机端口、基于 actuator 的就绪检查与优雅停机、
  与 devtools 热重启共存。

非目标（v1）：

- 多实例 / 集群（同 namespace 内 app 名唯一）。
- 开机自启（pm2 startup 类能力）。
- 远程管理、容器支持。
- 从 pom.xml / gradle toolchain 自动匹配 JDK。

## 2. 总体架构

```
┌─────────────┐  ┌─────────────┐       ┌──────────────┐
│  spm4a CLI  │  │  spm4a TUI  │  ...  │  Coding Agent │
└──────┬──────┘  └──────┬──────┘       └───────┬──────┘
       └────────────────┼──────────────────────┘
          HTTP + JSON-RPC（Named Pipe / Unix Socket）
                        │
                 ┌──────┴──────┐
                 │    daemon    │  每用户全局单例，按需自动拉起
                 └──────┬──────┘
           ┌────────────┼────────────┐
       ┌───┴───┐    ┌───┴───┐    ┌───┴───┐
       │ java  │    │ java  │    │ java  │   Spring Boot 子进程（可能带构建工具父进程）
       └───────┘    └───────┘    └───────┘
```

单一二进制：`spm4a` 既是 CLI 也是 daemon（隐藏子命令 `spm4a daemon`）。

## 3. Daemon

### 3.1 作用域与发现

- 每用户全局单例。通过 namespace 实现命名空间隔离（见 §4）。
- 运行目录：`SPM4A_HOME` 环境变量可重定位，缺省 `~/.spm4a`
  （e2e/测试用它获得完全隔离的 daemon 实例）。
- 套接字路径由纯函数计算（Listen/Dial 共用，两端必然一致）；
  hash8 = SPM4A_HOME 绝对路径 sha256 前 4 字节 hex：
  - Linux/macOS 三级规则：
    1. `$XDG_RUNTIME_DIR` 非空 → `$XDG_RUNTIME_DIR/spm4a/spm4a-<hash8>.sock`
       （目录 0700，文件 0600；XDG 目录同用户共享，不同 SPM4A_HOME 实例靠 hash 区分）；
    2. 否则 `<SPM4A_HOME>/run/spm4a.sock`（run 目录 0700，文件 0600），
       完整路径 ≤100 字节时；
    3. 仍超 100 → `/tmp/spm4a-<username>-<hash8>.sock` 兜底。
    约束来源：unix socket 的 `sun_path` 上限 104 字节（macOS）/108（Linux），
    而 macOS `$TMPDIR` 可达 50+ 字符，故长 home 必须回退短路径。
    注记：socket 路径依赖进程 XDG_RUNTIME_DIR 环境变量，daemon 由 CLI 拉起时继承
    其环境，同会话一致；跨会话 XDG 不同（罕见）会触发安全重拉（spawn 锁兜底）。
  - Windows：命名管道 `\\.\pipe\spm4a-<username>-<hash8>`
    （go-winio，ACL 限当前用户；默认 SPM4A_HOME 下保持每用户单例，
    测试设置 SPM4A_HOME 即获得独立管道）
- 元信息文件 `<SPM4A_HOME>/run/daemon.json`：`{pid, version, startedAt}`，用于校验存活与版本。

### 3.2 自动拉起

CLI 每次执行：

1. 尝试连接套接字，成功则直接发请求。
2. 失败则获取 spawn 锁（`<SPM4A_HOME>/run/spawn.lock`，gofrs/flock，跨平台）。
3. 持锁后二次尝试连接（其他 CLI 可能已完成拉起）。
4. 仍失败则以 detach 方式启动 `spm4a daemon`
   （Unix：`Setsid`；Windows：`DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP`），
   标准输出重定向到 `<SPM4A_HOME>/run/daemon.log`。
5. 轮询等待 ready（默认 5s 超时），释放锁，重新连接。

### 3.3 版本协商

客户端连接后调用 `daemon.ping`，daemon 返回 `{version, pid, startedAt}`。
主版本不一致时：CLI 先请求旧 daemon 优雅退出，再拉起新 daemon。

### 3.4 空闲退出

由 `<SPM4A_HOME>/config.yaml` 的 `idle-exit` 字段配置：

- `immediate`（默认）：无 app 且无客户端连接时立即退出；
- `never`：永不因空闲退出（`spm4a kill` 始终可关闭）；
- `10m` / `30s` 等 duration：条件满足后延迟到点退出；期间条件被破坏
  （新 app / 新客户端连接）取消计时。

判定时机：app 数变为 0 或客户端断开时检查。TUI / `logs -f` 等长连接视为客户端。
缺文件/缺字段按 `immediate`；非法值记 daemon.log 警告并按 `immediate`。

### 3.5 状态持久化与进程收养

- daemon 内存中持有全部 AppState，变更即原子写（tmp + rename）到
  `<SPM4A_HOME>/namespaces/<namespace>/state.json`；文件顶层为 `{"apps": [...]}` 包装
  （为将来顶层元数据留位），旧格式（裸数组）读取兼容，下次保存自动迁移。
- daemon 崩溃/重启后重新收养仍存活的子进程：按 state.json 中的 PID 逐一探测，
  存活则恢复管理（日志改为文件 tail，健康检查重新挂上）；已死则标记 `stopped`。
- 收养校验 **PID + 进程启动时间**（spawn 时经 gopsutil `CreateTime` 记录，unix ms）：
  启动时间不匹配说明 PID 已被复用，按已死处理；无该字段的旧记录回退纯 PID 探测。
- `ephemeral`（默认 true）记录不超越进程存亡：stop 完成后删除（发 `app.deleted`
  事件）；daemon 重启时仍存活的进程照常收养，已退出（含 error）的记录直接丢弃。
  进程管理本身不受 ephemeral 影响。长驻应用显式 `ephemeral: false` 以保留
  停止/错误记录。
- 收养语义全平台一致：Windows 的 Job Object 不设 `KILL_ON_JOB_CLOSE`，
  daemon 退出/崩溃不连带杀 app，重启后正常收养；杀树一律显式 TerminateJobObject。

## 4. Namespace

- namespace 是 app 的**纯命名标签，与目录解耦**：app 名在 namespace 内唯一，
  跨 namespace 可重名。
- 与目录解耦支持**同一项目检出的并行开发**：多个 agent 会话 / 人工 + agent
  在同一目录同时工作时，各自使用不同 namespace（如 `task-a` / `task-b`），
  app 名互不冲突。
- 默认值探测：从 cwd 向上找 `.git` → `pom.xml`/`build.gradle`，取根目录名；
  都没有则 `default`。默认规则不解决同目录并行——并行场景必须显式指定。
- 推荐实践：agent 每个任务设置 `SPM4A_NAMESPACE` 环境变量（如任务 ID），
  之后所有命令免 flag 自动隔离。
- 优先级链：

```
CLI --namespace > SPM4A_NAMESPACE 环境变量 > spm4a-app.yaml 的 namespace: > 探测 > default
```

- 命名约束：`[A-Za-z0-9._-]+`（namespace 名会落到 state 目录路径）。
- 各命令默认只作用于当前 namespace；全局 `-A` / `--all-namespace` 跨 namespace：
  `ls`/`tui` 展示全部；单 app 命令按名跨 namespace 解析——恰好一个匹配才执行，
  多个报歧义错误（退出码 2）并列出候选，零个按 app 不存在（退出码 3）。
- 端口池、JDK 注册表为机器级资源，全局共享；daemon 侧状态（state.json）按 namespace
  分目录。应用日志不跟随 namespace，而是基于每个 app 的 workdir（见 §13）。

## 5. IPC：HTTP + JSON-RPC 2.0

### 5.1 传输

- Windows：`github.com/Microsoft/go-winio` 命名管道；Linux/macOS：标准库 `net` Unix Socket。
- 两者都作为 `net.Listener` 交给 `http.Server`，跑标准 HTTP/1.1。
- 收益：无自定义帧协议；`curl --unix-socket` 可直接调试；未来若支持远程管理可平移 TCP。

### 5.2 JSON-RPC 2.0

单端点 `POST /v1/rpc`（`Content-Type: application/json`），支持 batch 数组。

请求/响应：

```json
{"jsonrpc":"2.0","id":1,"method":"app.stop","params":{"namespace":"mall","name":"order"}}
{"jsonrpc":"2.0","id":1,"result":{"status":"stopping"}}
{"jsonrpc":"2.0","id":1,"error":{"code":-32001,"message":"app not found","data":{"namespace":"mall","name":"order"}}}
```

方法集：

| method | 说明 |
|---|---|
| `daemon.ping` | 握手：返回 version / pid / startedAt |
| `daemon.shutdown` | 关闭 daemon；`params.all=true` 时先停全部 app |
| `app.start` | params 含单个 spec 或 specs 数组（对应多 app 文件） |
| `app.stop` | `now`、`timeout` 可选 |
| `app.restart` / `app.reload` / `app.delete` | 进程重启 / 热重启 / 移除 |
| `app.list` | `namespace` 或 `all=true` |
| `app.status` / `app.health` | status 含注入结果与实际 jar 路径；health 透传 actuator JSON |
| `logs.get` | `lines` 参数，一次性返回 |
| `logs.follow` | 流式，见下 |
| `events.subscribe` | 事件总线，流式 |

错误码：标准码（`-32700` 解析错误、`-32600` 无效请求、`-32601` 方法不存在、
`-32602` 参数错误、`-32603` 内部错误）+ 应用自定义码（`-32001` APP_NOT_FOUND、
`-32002` APP_EXISTS、`-32003` INVALID_STATE、`-32004` 端口冲突、
`-32006` 就绪检查超时）。CLI 退出码由这些码映射（见 §14）。

### 5.3 流式（logs.follow / events.subscribe）

- 响应为 `200 text/event-stream`（SSE）。
- 首条 SSE 事件是 JSON-RPC response（含 `subscriptionId`）。
- 后续每条是 JSON-RPC notification（`logs.chunk` / `event`）。
- 结束发 `stream.end` notification；客户端断连即取消订阅。

## 6. 应用模型与配置

### 6.1 数据模型

```go
type AppSpec struct {
    Name           string            // namespace 内唯一
    Namespace      string
    Workdir        string            // 必填；jar/command/log 相对路径的解析基准
    Launcher       string            // jar | maven | gradle | custom
    Jar            string            // launcher=jar；绝对路径 | 相对 workdir | glob
    Command        []string          // launcher=custom
    JDK            string            // 绝对路径 | 注册表名 | major 版本号, 空=默认
    Port           int               // 0 = 随机分配
    Debug          bool
    DebugPort      int               // 0 = 随机
    Env            map[string]string // 用户 env（注入结果另算，不回写这里）
    Args           []string          // 用户显式参数，永远赢过注入
    HealthPath     string            // 默认 /actuator/health
    ShutdownTimeout time.Duration    // 默认 15s
    RestartPolicy  string            // 默认 never
    LogFile        string            // 默认 ./logs/${name}.log（相对 workdir）
    Xms            string            // 物化值如 "32M"；"" = 不注入
    Xmx            string            // 物化值如 "256M"；"" = 不注入
    // （RPC/yaml 层用 *string 区分"未设置"与"显式关闭"，daemon 校验期物化）
    JvmOpts        []string          // 自由 JVM 参数
    Ephemeral      bool              // 临时实例（默认 true）：stop 后删除记录；收养不受影响（§3.5）
}

type AppState struct {
    Spec        AppSpec
    PID         int                 // 进程树根 PID
    ProcStartedAt int64              // 进程启动时间(unix ms)，收养时防 PID 复用
    StartedAt   time.Time           // 启动时刻
    Status      string              // starting|ready|unready|stopping|stopped|error
    ActualPort  int
    DebugPort   int
    JavaBin     string              // 实际生效的 java 路径（含 JDK 解析结果）
    ResolvedJvmOpts []string        // 实际生效的 JVM 参数（jdwp + xms/xmx + jvm-opts 合成结果）
    ResolvedJar string              // jar glob/相对路径展开后的绝对路径
    LogPath     string              // 实际生效的日志文件绝对路径
    Restarts    int
    LastExit    *ExitInfo
}
```

### 6.2 配置文件

默认文件名 **`spm4a-app.yaml`**（cwd，或 `-f` 显式指定）。**字段一律 kebab-case**；
HTTP JSON 侧保持 camelCase。支持单文件多 app：

```yaml
namespace: mall                  # 可选，缺省探测
apps:
  - name: order-service          # 缺省取 workdir 目录名
    workdir: services/order      # 必填；相对路径相对本 yaml 所在目录解析
    launcher: jar
    jar: target/*.jar            # 相对 workdir；支持绝对路径与 glob
    jdk: "17"                    # 版本号写法建议加引号，避免被解析成数字
    port: random                 # random | 8080
    debug: true                  # true=随机调试端口 | 5005 | false
    env: { SPRING_PROFILES_ACTIVE: dev }
    args: ["--my.flag=x"]
    health-path: /actuator/health
    shutdown-timeout: 15s
    restart-policy: never
    ephemeral: false               # 缺省 true：stop 后不保留记录（收养不受影响）；长驻应用显式 false
    log-file: ./logs/${name}.log # 可省，默认值即如此
    xms: 64M                     # 可省，缺省 32M；显式 "" 关闭注入
    xmx: 512M                    # 可省，缺省 256M；显式 "" 关闭注入
    jvm-opts: ["-XX:TieredStopAtLevel=1"]
  - name: pay-service
    workdir: ../artifacts        # workdir 与 jar 可以完全不重叠
    launcher: jar
    jar: /abs/path/pay.jar       # 绝对路径原样使用
    port: 8082
```

单 app 文件允许省略 `apps:` 直接写字段。CLI 参数覆盖 yaml 同名字段。
`spm4a start -f spm4a-app.yaml` 启动全部 app；`--only <name>` 选子集。

**workdir 必填**：不做隐式猜测。缺省时 `app.start` 返回 `-32602` 参数错误，
让 agent 显式修正。

**jar 解析规则**（start 时执行）：

1. 绝对路径：原样使用（jar 与 workdir 不重叠的场景，如统一构建产物目录）。
2. 相对路径：相对 workdir 解析。
3. 含通配符（`*`、`?`、`[`）：按 glob 展开（相对 pattern 相对 workdir），
   **必须恰好命中 1 个文件**——0 个报"jar 未找到，请先构建"，多个报歧义错误
   并列出候选。展开结果记入 `AppState.ResolvedJar`，`status` 可见。

### 6.3 优先级链

```
CLI 显式参数 > spm4a-app.yaml > 自动探测（namespace 名 / 端口随机化等） > 内置默认
```

除 `start` 外的命令不读配置文件，只靠显式参数（name + `--namespace`）定位 app。

## 7. Launcher 与进程树

| launcher | 启动命令 | 说明 |
|---|---|---|
| jar | `<jdk>/bin/java [jdwp] -jar <resolvedJar> [args]` | 直接拼 java 命令行 |
| maven | `mvn spring-boot:run -Dspring-boot.run.jvmArguments=... -Dspring-boot.run.arguments=...` | 两层进程：Maven JVM → fork 的应用 JVM |
| gradle | `gradle bootRun [--args=...] [-I spm4a-init.gradle]` | JVM 参数经临时 init script 注入 `bootRun.jvmArgs` |
| custom | 用户命令原样执行 | 注入仅通过 env |

进程树管理（maven/gradle 模式存在孙进程）：

- Unix：子进程 `Setpgid`，信号发 `-pgid` 覆盖整棵树。
- Windows：子进程挂入 Job Object，需要杀树时显式 `TerminateJobObject`
  （不使用 `KILL_ON_JOB_CLOSE`，否则 daemon 退出会连带杀光 app，收养失效）。
- Windows 子进程一律以 `CREATE_NO_WINDOW` 创建：daemon 自身以 DETACHED_PROCESS
  运行（无控制台），否则系统会为控制台子系统子进程新分配一个可见终端窗口。
  覆盖 app 启动、reload 编译、daemon 侧 JDK 探测全部 spawn 路径。

## 8. Spring Boot 注入

### 8.1 注入通道

主通道为 **`SPRING_APPLICATION_JSON` 环境变量**：四种 launcher 统一（只传 env，
不用各自拼参数），优先级高于 `application.properties`、低于命令行参数——
用户显式 `args` 永远生效。用户已自带该变量时解析 JSON 后合并（用户键优先）。

### 8.2 默认注入清单（用户未显式指定才注入）

| 键 | 值 | 目的 |
|---|---|---|
| `server.port` | 分配端口 | 端口控制 |
| `management.endpoints.web.exposure.include` | 用户值 ∪ `health,shutdown` | 就绪检查 + 停机端点 |
| `management.endpoint.shutdown.enabled` | `true` | 优雅停机主通道 |
| `server.shutdown` | `graceful` | 排空在途请求 |

最终注入结果在 `spm4a status --json` 中可见，可审计。

### 8.3 debug 注入（JDWP）

`--debug` 时追加 `-agentlib:jdwp=transport=dt_socket,server=y,suspend=n,address=<addr>`：

- JDK ≥ 9：`address=*:<port>`；JDK 8：`address=<port>`（仅 localhost）。
  依据 JDK 注册表缓存的 major version 选语法；未注册的默认 JDK 仅在开启 debug 时
  现场探测一次。
- jar：直接拼 java 参数；maven：`-Dspring-boot.run.jvmArguments`；
  gradle：临时 init script 设置 `bootRun.jvmArgs`。
- 不使用 `JAVA_TOOL_OPTIONS`（会污染 Maven/Gradle 自身 JVM 造成端口冲突）。

### 8.4 JVM 参数

- 直接项 `xms` / `xmx`：缺省注入 `-Xms32M -Xmx256M`（agent 并行起多实例时 JVM 默认
  xmx=物理内存 1/4 太浪费；最小 Boot 4 应用堆占用百兆级，256M 有余量）。
  显式空串关闭该项注入（回退 JVM 自身默认）。校验 `^\d+[kKmMgG]$`，非法值在
  start 校验期报 -32602。其余项（gc、xss 等）走 jvm-opts。
- 自由项 `jvm-opts`（yaml 列表）/ `--jvm-opt`（CLI 可重复 flag，一 flag 一参数，
  避免引号内空格切分歧义）。
- 合成顺序：`[jdwp agent（若 debug）] + [xms/xmx] + [jvm-opts]`，按 launcher 走
  §8.3 的同一通道（jar 直拼 / maven `spring-boot.run.jvmArguments` /
  gradle init script；custom 不生效，仅有 SPRING_APPLICATION_JSON env）。
- 注意：`-Dspring-boot.run.jvmArguments` 会覆盖 pom 中的 `<jvmArguments>`——
  JVM 参数应统一由 spm4a 管理，勿在 pom 中重复配置。
- 合成结果记入 `AppState.ResolvedJvmOpts`，`status` 可见可审计。

## 9. 端口管理

- daemon 内置端口池，默认范围 10000–60000（含随机端口与 debug 端口）。
- 分配流程：候选端口 bind 探测 → 释放并内存预留 → 注入启动参数 →
  app ready 或失败后释放预留。
- 已知竞态：释放到 JVM 绑定之间端口可能被抢。检测到启动早期因绑定失败退出时，
  自动换新端口重试一次（仅阻塞等待模式下重试；`--no-wait` 时首候选失败直接转 error）。

## 10. 就绪检查与优雅停机

### 10.1 就绪检查（actuator）

- 就绪：先等 TCP 可连，再轮询 `GET http://127.0.0.1:<port><healthPath>` 直到 UP
  （默认 60s 超时，可配），状态转 `ready`。
- 存活：周期探测（默认 5s）；连续失败超过容忍窗口（默认 3 次）标记 `unready`，
  不自动杀——进程异常由 agent 看日志决策。
- 容忍窗口同时覆盖 devtools 热重启期间 context 短暂不可用，避免误判。
- `spm4a health <name>` 透传 actuator health 原始 JSON。

### 10.2 停机序列

1. `POST /actuator/shutdown`（主通道，全平台统一；Spring 走优雅关闭后 JVM 自行退出）。
2. HTTP 无响应（应用卡死）→ 信号兜底：Unix `SIGTERM`（进程组）；
   Windows 对整棵 Job Object 树 TerminateProcess。
3. 等待 `shutdownTimeout`（默认 15s）后仍未退出 → 强杀（`SIGKILL` / TerminateProcess）。
4. `stop --now` 跳过优雅阶段直接强杀。

注记：

- 停机端点路径固定 `/actuator/shutdown`，不跟随自定义 management base-path。
- `POST /actuator/shutdown` 成功但应用在 shutdownTimeout 内未退出时，不再补信号、
  直接强杀。

## 11. 热重启（devtools 共存）

`spring-boot:run`/`bootRun` 本身不做热加载；推荐路径是 spring-boot-devtools
（双 classloader 自动重启 context，JVM 不死，注入端口保持稳定）。

- `spm4a reload <name>`：在 workdir 执行编译（maven → `mvn compile`，gradle →
  `gradle classes`），触发 devtools 热重启；阻塞返回编译结果与随后 health 状态。
- JDWP HotSwap（方法体级）随 `--debug` 注入的 JDWP 天然可用，spm4a 不额外处理。

## 12. 多 JDK 支持

- **注册表**：`<SPM4A_HOME>/jdks.json`，由 CLI 本地维护；daemon 侧只读，
  唯一例外是绝对路径 JDK 现场探测后缓存写入：
  - `spm4a jdk scan`：扫描 JAVA_HOME 及其版本号变体
    （`JAVA8_HOME`、`JAVA_11_HOME`、`JAVA_HOME_17`、`JAVA_HOME21` 等，
    统一正则 `^JAVA_?(\d+)?_?HOME_?(\d+)?$`，变量名里的数字仅作候选提示，
    版本以 `java -version` 实测为准）、PATH、`~/.sdkman`、`~/.jdks`、
    `/usr/lib/jvm`、`Program Files\Java`、mise/asdf 等常见位置。
  - `spm4a jdk ls` / `spm4a jdk add <path>`。
  - 登记时执行一次 `java -version` 并缓存 major version 与完整版本号（不缓存架构）。
- **引用**：AppSpec `jdk` 字段接受绝对路径 / 注册表名 / major 版本号
  （多个匹配取最高 patch）；缺省回退 `JAVA_HOME` → PATH。
- **生效路径**：jar 模式直接用 `<jdkHome>/bin/java`；maven/gradle 模式给子进程
  设置 `JAVA_HOME` env（Gradle toolchain 可能覆盖，实际生效值在 `status` 中展示）。

## 13. 日志

- **应用日志默认写到 `<workdir>/logs/<name>.log`**——基于显式配置的 workdir，
  不做任何根目录探测。建议用户把 `logs/` 加进 `.gitignore`。
- `log-file` 可覆盖默认路径，支持表达式变量：
  `${name}` `${namespace}` `${workdir}` `${pid}` `${ts}`（启动时间 `yyyyMMdd-HHmmss`）；
  相对路径相对 workdir 解析，也允许绝对路径；无法解析的变量在 start 校验期报错。
- 轮转：单文件 10MB，保留 3 份。文件名含 `${pid}`/`${ts}` 时每次启动都是新文件，
  轮转天然不触发。
- `spm4a logs <name> [--lines 200]` 读文件；`-f` 走 `logs.follow` SSE 流，
  由 daemon tail 推送（支持 `lines` 参数：先送尾部 N 行再跟随）。
  daemon 在 AppState.LogPath 中记录实际生效的绝对路径，CLI 无需知道路径规则。
- daemon 自身日志：`<SPM4A_HOME>/run/daemon.log`。

## 14. CLI 命令全集

```
spm4a start [dir] [-f spm4a-app.yaml] [--only name] [--name n] [--namespace ns]
            [--workdir path] [--launcher jar|maven|gradle|custom] [--jar path]
            [--jdk 17] [--port N|random] [--debug[=port]] [--env K=V]...
            [--xms 64M] [--xmx 512M] [--jvm-opt "-XX:..."]... [--ephemeral=false]
            [--health-path p] [--log-file path] [--timeout 60s] [--no-wait]
            [--] [args...]
spm4a stop <name> [--now] [--timeout 15s]   # 或 stop --all：当前 namespace 全部（-A 跨全部）
spm4a restart <name>            # 完整进程重启
spm4a reload <name>             # 编译触发 devtools 热重启
spm4a ls [-A]                   # 默认当前 namespace
spm4a status <name>
spm4a logs <name> [-f] [--lines 200]
spm4a health <name>
spm4a rm <name>                 # 删除已停止 app 的记录；rm --all 批量（-A 跨全部）
spm4a jdk scan|ls|add
spm4a kill [--all]              # 关闭 daemon
spm4a tui [-A]
全局: --json  --namespace  -A/--all-namespace   # namespace 亦可用 SPM4A_NAMESPACE 环境变量
```

- `start` 默认阻塞至 `ready`（agent 友好），`--no-wait` 立即返回。
- `-A/--all-namespace`：`ls`/`tui` 跨 namespace 展示；单 app 命令跨 namespace 按名解析，
  恰好一个匹配才执行，多个报歧义错误（退出码 2）并列出候选。
- 退出码：`0` 成功；`1` 通用错误；`2` 用法/参数错误（含 -32602）；`3` app 不存在（-32001）；
  `4` 冲突类（-32002 app 已存在、-32004 端口冲突）；`5` daemon 不可用；`6` 就绪检查未通过/超时（-32006）。
- `--json` 模式下错误以 JSON 输出到 stderr：
  `{"error":{"code":-32001,"message":"...","exitCode":3}}`，stdout 保持纯数据。

## 15. TUI（bubbletea）

`spm4a tui`：k9s 风格边框布局——顶部 header 条（`SPM4A` 标识 + namespace/版本/计数
上下文）；主体左右分栏：左侧窄栏（≤36 列）上为 Apps 面板（紧凑列表：名字 + 彩色
状态），下为 Detail 面板（选中 app 的配置与状态属性，可滚动）；右侧为 Logs 面板
（stdout/stderr 实时输出，viewport 支持滚动与翻页）。各面板均为直角边框内嵌标题、
内容留一列左边距
（` Apps(ns) ` / ` Detail: ns/name ` / ` Logs: ns/name `），聚焦的面板标题加 `*`
标记且边框变色；终端小于 60x10 时整体退化为"terminal too small"提示。
app 资源占用经 gopsutil 采集（进程树含子进程求和）；
日志走 `logs.follow(lines=200)`（先送尾部 N 行再跟随）；快捷键：`s` stop、
`r` restart、`l` reload、`d` rm（仅 stopped/error，y/n 确认）、`tab`/`shift+tab`
循环聚焦 Apps/Detail/Logs（聚焦面板内 `↑↓/jk` 滚动、`pgup/pgdn` 翻页）、
`enter` 聚焦日志（esc 返回）、`A` 切换全 namespace、`q` 退出。数据经 `app.list` +
`events.subscribe` SSE 驱动，2s tick 兜底刷新指标；非 TTY 环境运行报清晰错误。

## 16. 工程结构

运行时（用户目录；应用日志不在此，落在各 app 的 workdir）：

```
~/.spm4a/                    # 即 SPM4A_HOME
  run/                       # daemon.json, daemon.log, spawn.lock（socket 位置见 §3.1）
  config.yaml                # daemon 配置：idle-exit（见 §3.4）
  jdks.json
  namespaces/<ns>/
    state.json               # 顶层 {"apps": [...]} 包装
```

仓库：

```
cmd/spm4a/main.go
internal/
  ipc/       # HTTP over pipe/UDS 传输、JSON-RPC 编解码、SSE 流、socket 路径纯函数
  daemon/    # daemon server、生命周期、事件总线、自动拉起
  proc/      # 进程树监督（pgid / Job Object）、端口池、日志重定向与轮转
  launcher/  # jar / maven / gradle / custom、jar 路径与 glob 解析、JVM 参数合成
  inject/    # SPRING_APPLICATION_JSON 合并、JDWP
  health/    # actuator 客户端
  state/     # 数据模型与持久化
  jdk/       # JDK 注册表与探测
  cli/       # 各命令实现
  tui/       # bubbletea 界面
e2e/
  demo-app/                # Maven 构建的 Spring Boot 4 测试应用（附 spm4a-app.yaml 样例）
  demo-app-gradle/         # Gradle 构建的同款测试应用
  *_test.go                # 端到端测试：驱动真实 spm4a 二进制 + demo 应用
build.sh                   # 发布构建：全平台二进制 + 版本戳 + checksums，输出到 dist/
.github/workflows/
  test.yml                 # push/PR(main/master/develop)：三平台 build/vet/gofmt/test
  release.yml              # tag v*：构建并发布 GitHub Release
```

### 16.1 测试应用 demo-app

- 极简 Spring Boot 4 应用（Java 17+）：依赖仅 `spring-boot-starter-web` +
  `spring-boot-starter-actuator` + `spring-boot-devtools`，提供一个 `GET /hello` 端点。
- 故意不预设任何配置：`application.properties` 不写 `server.port`、不写
  management 暴露——e2e 借此验证 spm4a 的随机端口与注入清单真实生效。
- e2e 主流程：构建 → `start`（随机端口、阻塞至 ready）→ `health` 断言 UP →
  调 `/hello` → `reload`（maven/gradle 模式，PID 不变的热重启）→
  `stop` 断言优雅停机 → 断言日志文件与进程树清理。无 Maven/JDK/Gradle 的环境
  自动 skip 对应用例。
- 日常调试可直接 `spm4a start -f e2e/demo-app/spm4a-app.yaml`。

## 17. 技术选型

| 用途 | 库 |
|---|---|
| Windows 命名管道 | `github.com/Microsoft/go-winio` |
| CLI 框架 | `spf13/cobra` |
| TUI | `charmbracelet/bubbletea` + `bubbles` + `lipgloss` |
| 文件锁 | `gofrs/flock` |
| 资源监控 | `shirou/gopsutil` |
| YAML | `gopkg.in/yaml.v3` |
| 日志 | 标准库 `log/slog` |
| Windows Job Object | `golang.org/x/sys/windows` |

## 18. 已知限制

- gradle init script：鸭子类型匹配（init classpath 拿不到 Boot 插件类），jvmArgs 为
  覆盖语义，仅匹配名为 `bootRun` 的任务；重度定制 bootRun（多 task / 改名 task）无效。
- devtools restart 激活时（`spring-boot:run` / `bootRun` 从源码运行），actuator 优雅停机
  完成后 JVM 以退出码 1 结束（已查明：`spring.devtools.restart.enabled=false` 须以 JVM
  系统属性关闭后退出码归 0；打包 jar 运行 devtools 自动失效，退出码为 0）。spm4a 自行
  跟踪进程存亡，功能不受影响；gradle 侧由 init script 的 `ignoreExitValue = true` 兜底，
  避免构建被记为失败（CI job summary 红叉的来源）。
- custom launcher 的就绪检查走 actuator health：非 Spring 进程会超时失败（v1 的
  就绪模型面向 Spring Boot）。
- maven/gradle 的 args 与 jvmArguments 通道均为空格切分单字符串，含空格的参数不支持。
- 日志表达式含 `${pid}` 时走管道转发（启动后才知 pid），daemon 崩溃后子进程
  写满管道会阻塞；默认表达式不含 pid 不受影响。
- gradle daemon 驻留：stop 后 Gradle Daemon JVM 按 Gradle 设计保持存活（不占应用端口）；
  强杀路径（TerminateJobObject）会连带 gradle daemon，属可接受的强制语义。
- ephemeral（默认）+ immediate 空闲退出（默认）的组合下：app 崩溃后 daemon 会因无活跃
  app 立即退出，error 记录在下一次 daemon 拉起的收养阶段被清除——即崩溃后 `ls` 可能
  直接看不到该 app；其日志文件仍在 `<workdir>/logs/` 可查。需要留痕排查的场景用
  `ephemeral: false` 或 `idle-exit: never`。
