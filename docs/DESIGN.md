# AutoCoder 设计与规划

> 状态：v0.2 草案（界面原型 + 工具检测 + 角色分配已实现，编排执行待开发）
>
> v0.2 已确认：统一只用命令行（CLI）工具；记录全部 token 消耗并提供计量明细；同时运行工具数默认 3、最多 5；集成测试通过后由项目管理直接合并到主分支。

## 1. 要解决的问题

一台电脑上往往同时装了 Claude Code、Codex、Cursor 等多个 AI 开发工具，但它们彼此不知道对方的存在：

- 同一个项目只能"选一个工具干活"，其余工具闲置；
- 想并行用多个工具时，它们会在同一个工作目录里互相覆盖文件；
- 每个工具的进度散落在各自的对话里，没有统一的地方看"项目做到哪了"。

AutoCoder 的目标：**把本机的多个开发工具组织成一个小团队**——一个负责架构与项目管理，其余负责开发、前端、测试、评审——并用 **git** 作为团队唯一的协作介质。

所有工具都以**命令行（CLI）方式**接入：由 AutoCoder 统一派发、统一管理进程、统一计量 token。IDE 类工具使用其 CLI 版本（例如 Cursor 使用 Cursor Agent CLI）。

## 2. 核心概念

| 概念 | 说明 | 在 git 中的体现 |
|---|---|---|
| 工具 (Tool) | 本机安装的一个开发工具，来自工具清单 | — |
| 角色 (Role) | 工具在项目中的职责 | `.autocoder/project.yaml` 中的 `roles` |
| 项目 (Project) | 一个被 AutoCoder 管理的 git 仓库 | 仓库本身 + `.autocoder/` 目录 |
| 任务 (Task) | 项目管理拆分出的最小工作单元，构成依赖图 | 一个分支 `ac/<id>-<slug>` + 一个 worktree |
| 汇报 (Report) | 任务执行者向项目管理反馈的进度 | 带 trailer 的提交 + `.autocoder/reports/<id>.md` |
| 集成 (Integration) | 项目管理把完成的任务拼接起来 | 合并到 `ac/integration`，验证后合并到主分支 |
| 计量 (Usage) | 每次调用工具消耗的 token | `.autocoder/usage/*.jsonl`（不提交，见 §7） |

## 3. 角色定义与分配

### 3.1 角色

| 角色 | id | 职责 |
|---|---|---|
| 架构与项目管理 | `lead` | 架构设计；把需求拆成功能任务与视觉任务；生成任务依赖图并派发；接收汇报；按依赖顺序集成分支；处理受阻任务 |
| 开发 | `developer` | 领取功能任务，在自己的分支和 worktree 中实现、提交、汇报 |
| 前端/视觉 | `frontend` | 领取界面、交互、视觉任务 |
| 测试 | `tester` | 为已完成的任务编写测试、执行回归，把质量结果汇报给项目管理 |
| 评审 | `reviewer` | 合并前审查任务分支的 diff，给出通过/修改意见 |

### 3.2 默认分配规则（已实现：`internal/roles`）

0. 只有已安装、且清单中有非交互调用模板（`headless`）的 CLI 工具参与分配。
1. **只有一个可用工具**：它承担全部角色，AutoCoder 退化为"单工具 + git 任务管理"。
2. **多个工具**：**能力评分最高**的担任 `lead`。
3. 其余工具按 **擅长方向匹配 (+100) + 能力评分** 依次认领 开发 → 前端/视觉 → 测试 → 评审。
4. 工具不够分时：评审回落给 `lead`（它本来就负责合并把关）；其余角色由最匹配的工具兼任。
5. 工具多于角色时，多出来的工具都作为额外的开发者，并行领取任务。
6. 用户可以在界面上任意调整，保存后覆盖推荐值；`lead` 只能有一个。

以本机装有 Claude Code、Codex、Cursor Agent CLI 为例，默认分配为：

| 工具 | 角色 |
|---|---|
| Claude Code | 架构与项目管理、评审 |
| Codex CLI | 开发、测试 |
| Cursor Agent CLI | 前端/视觉 |

"能力评分"目前是清单中人工给出的静态值；后续可以结合计量数据（§7）做动态调整：每个工具在本项目中任务的通过率（测试是否通过、评审是否打回）与单位任务 token 消耗。

## 4. 工具清单与启动检测（已实现：`internal/toolscan`）

### 4.1 清单

- 内置清单：`internal/toolscan/tools.yaml`，只收录 CLI 工具：Claude Code、Codex CLI、Cursor Agent CLI、Gemini CLI、Qwen Code、GitHub Copilot CLI、OpenCode、Aider。
- 用户清单：`~/.autocoder/tools.yaml`，按 `id` 覆盖或追加；`disabled: true` 可排除不想参与协同的工具。

每个条目的关键字段：

```yaml
- id: codex
  name: Codex CLI
  commands: [codex]         # 在 PATH 中依次查找
  paths: { darwin: [...] }  # 可选：按系统的额外路径（不在 PATH 中的安装位置）
  version_args: ["--version"]
  headless: ["exec", "{prompt}"]   # 非交互调用模板，编排器用它派发任务
  usage_args: ["--json"]           # 让工具输出带 token 用量的结构化结果（见 §7）
  capability: 90
  strengths: [coding, architecture, testing]
```

### 4.2 检测流程

启动时（`autocoder` 或 `autocoder tools`）：

1. 读取内置清单并合并用户清单；
2. 并发检测每个工具：先查 PATH，再查平台路径；
3. 找到可执行文件后以 5 秒超时执行 `version_args` 取版本号；
4. 结果按"已安装优先、能力评分降序"排序，缓存在服务中，界面"重新检测"可刷新；
5. 根据结果给出默认角色分配。

检测只做 `which` + `--version`，不读取任何工具的凭据或配置。

## 5. 以 git 为核心的项目管理

### 5.1 项目即仓库

创建项目有三种来源：新建仓库（`git init`）、克隆远程（`git clone`）、接管已有目录。创建时：

```text
<repo>/
├── .autocoder/
│   ├── project.yaml     # 项目元数据、角色分配快照、分支约定
│   ├── plan.md          # lead 产出的架构设计与拆分说明
│   ├── tasks/T1.yaml    # 每个任务一个文件：标题、角色、负责工具、依赖、验收标准
│   └── reports/T1.md    # 每个任务的汇报记录（追加写）
└── ...项目代码
```

然后 `git commit` 初始化，并从主分支创建集成分支 `ac/integration`。所有项目状态都在仓库里，**换一台机器 clone 下来状态仍在**，也能直接用 `git log` 审计谁做了什么。

### 5.2 分支与工作区

- 每个任务一个分支：`ac/<task-id>-<slug>`，从 `ac/integration` 的当前位置切出（这样能拿到已合并的依赖任务成果）。
- 每个任务一个 **git worktree**：`../.ac-worktrees/<project>/<task-id>`。多个工具同时工作时各自在独立目录里改文件，互不覆盖。
- 主分支只接受 `lead` 从 `ac/integration` 发起的合并；工具不直接提交到主分支或集成分支。
- 如果配置了远程仓库，任务分支可推送并创建 PR，让人也能在 GitHub 上评审（可选）。

### 5.3 任务文件示例

```yaml
# .autocoder/tasks/T3.yaml
id: T3
title: 订单 API 与状态机
kind: feature          # feature | ui | test | infra
role: developer
assignee: codex
depends_on: [T1]
branch: ac/T3-order-api
acceptance:
  - 订单状态：待支付 → 已支付 → 已发货 → 完成 / 取消
  - go test ./internal/order/... 通过
status: running
```

## 6. 任务节点协同与进度汇报

### 6.1 生命周期

```text
planned ──依赖全部 merged──▶ ready ──派发──▶ running ──提交完成──▶ testing ──测试通过──▶ review ──评审通过并合并──▶ merged
                                              │                       │                    │
                                              └──────────────── blocked（缺信息/冲突/测试失败）◀┘
```

| 状态 | 含义 | 谁推动 |
|---|---|---|
| planned | 依赖未完成 | 编排器自动 |
| ready | 依赖已全部合并，可派发 | 编排器自动 |
| running | 负责工具在 worktree 中开发 | 开发/前端 |
| testing | 测试角色在同一分支上补测试并运行 | 测试 |
| review | 等待评审与集成 | 评审 / lead |
| merged | 已合并到 `ac/integration` | lead |
| blocked | 需要 lead 或用户介入 | 任何角色上报 |

### 6.2 派发与并发

编排器在任务 worktree 中执行清单里的 `headless` 模板（再追加 `usage_args`），`{prompt}` 由任务文件生成（任务描述、验收标准、相关文件、汇报协议）。

- **并发上限**：同时运行的工具进程默认 **3** 个，可在界面调到最多 **5** 个；超出的 `ready` 任务排队，按依赖图关键路径优先派发。
- 每个工具同一时间只跑一个任务进程，避免同一账号的速率限制互相影响。
- 每次派发都有超时、取消和日志；进程退出后无论成败都写一条计量记录（§7）。

### 6.3 汇报协议

工具汇报通过 **git 提交** 完成，不需要额外的通信通道：

```text
feat(order): 实现订单状态机

AutoCoder-Task: T3
AutoCoder-Status: running
AutoCoder-Progress: 60
AutoCoder-Summary: 状态机已实现，正在补退款分支
```

编排器监听各 worktree 的新提交，解析 trailer 更新任务状态，并把摘要追加到 `.autocoder/reports/T3.md`、推送给界面的"进度汇报"时间线。工具进程退出但没有提交时，编排器记录退出码与输出尾部，标记为 `blocked` 交给 lead。

### 6.4 跨任务协同

- **依赖传递**：任务分支从 `ac/integration` 切出，依赖任务合并后，后续任务自然拿到成果。
- **测试反馈**：测试角色发现问题时，在被测任务的报告里写入问题并把任务退回 `running`，开发工具在下一次派发时会收到这些问题。
- **接口约定**：lead 在 `plan.md` 中先写清模块边界和接口，前后端任务可以并行。

### 6.5 集成（拼接）

lead 维护集成队列，按依赖拓扑顺序：

1. `git merge --no-ff ac/<task>` 到 `ac/integration`；
2. 运行项目测试；
3. 通过 → 任务标记 `merged`，解锁下游任务；冲突或失败 → 由 lead 自行解决，或开一个修复任务派给原负责工具；
4. 一个里程碑的任务全部合并后，lead 汇总变更，**集成分支测试通过即直接合并到主分支**（默认开启"自动合并"，可在界面关闭，改为等用户确认）。合并提交里附带本里程碑的 token 用量汇总。

## 7. Token 计量

所有工具消耗的 token 都要记录，并能看到明细。

### 7.1 采集方式

每次派发都会产生一条计量记录。编排器给命令追加清单中的 `usage_args`，让工具输出结构化结果，再从中解析用量：

| 工具 | 追加参数 | 用量来源 |
|---|---|---|
| Claude Code | `--output-format json` | 结果 JSON 中的 `usage`（输入、输出、缓存读写） |
| Codex CLI | `--json` | JSONL 事件流中的 token 统计 |
| Cursor Agent CLI | `--output-format json` | 结果 JSON 中的用量字段 |
| Gemini CLI / Qwen Code | `--output-format json` | 结果 JSON 中的统计字段 |
| 其他（Copilot CLI、OpenCode、Aider） | 无 | 按输入/输出字符数估算，记录标为"估算" |

各工具的字段名会随版本变化，M2 实现解析器时按实际安装版本逐一验证；解析失败时退回估算，而不是丢弃记录。

### 7.2 记录格式

```json
{"id":"r-0192","project":"shop-admin","task":"T3","tool":"codex","role":"developer",
 "start":"2026-10-15T08:04:11+08:00","duration_sec":83,"exit_code":0,
 "usage":{"input":35482,"output":4619,"cache_read":136467,"cache_write":12852},
 "estimated":false}
```

- 记录追加写入 `.autocoder/usage/<日期>.jsonl`。这个目录加入 `.gitignore`，避免每次调用都产生提交；里程碑合并时把汇总写进合并提交说明。
- 失败、超时、被取消的调用同样记录——它们也消耗了 token。
- lead 自己的规划、评审、集成调用也逐条记录，按角色区分。

### 7.3 展示

- **Token 用量**页：合计与输入/输出/缓存读写分项；按工具、按角色、按任务汇总；逐次调用的计量明细（时间、项目、任务、工具、角色、各分项、耗时、来源）；可按项目和工具筛选、导出 CSV。
- **项目详情**：项目总用量与各工具占比；任务详情显示该任务累计 token 与调用次数。

## 8. 系统架构

```text
┌─────────────────────────── autocoder（单个 Go 可执行文件）───────────────────────────┐
│  Web UI (Vue 3, go:embed)  ◀──HTTP/SSE──▶  API 服务                                   │
│                                              ├─ toolscan  工具清单与启动检测   ✅ 已实现 │
│                                              ├─ roles     角色分配             ✅ 已实现 │
│                                              ├─ project   项目/任务文件读写     ⏳        │
│                                              ├─ gitops    分支/worktree/合并    ⏳        │
│                                              ├─ dispatch  派发任务、并发控制     ⏳        │
│                                              ├─ watcher   监听提交、解析汇报     ⏳        │
│                                              └─ meter     解析并记录 token 用量 ⏳        │
└──────────────────────────────────────────────────────────────────────────────────────┘
          │ exec                         │ exec                        │ exec
     claude -p "…"                  codex exec "…"                cursor-agent -p "…"
     (worktree T1)                  (worktree T3)                 (worktree T4)
```

技术选型理由：

- **Go 单文件**：纯 Go、`CGO_ENABLED=0`，一次构建出 macOS / Linux / Windows 全部产物，解压即用；调用本机 CLI 工具、操作 git、管理子进程都是 Go 的强项。
- **Vue 3 + Vite**：界面构建后通过 `go:embed` 打进二进制，默认只监听 `127.0.0.1:7788`。
- **git CLI 而不是 go-git**：worktree、merge 等行为与用户本机 git 完全一致，也能复用用户的凭据配置。
- 后续如需原生窗口，可在同一套前端上加 Wails 外壳，不影响核心逻辑。

## 9. 界面

| 页面 | 内容 | 状态 |
|---|---|---|
| 启动检查 | 已安装/未检测到的工具、版本、路径、是否可派发、用量是精确还是估算、能力评分、清单说明；可重新检测 | 已接真实后端 |
| 角色分配 | 工具 × 角色矩阵，推荐值可调整，提示未覆盖角色，`lead` 单选；运行设置（并发数、自动合并） | 已接真实后端（保存在浏览器本地） |
| Token 用量 | 合计与分项、按工具/角色/任务汇总、计量明细、筛选与导出 CSV | 演示数据 |
| 项目列表 | 项目卡片：路径、主分支/集成分支、完成进度、受阻数；新建项目对话框会预览将执行的 git 命令 | 演示数据 |
| 项目详情 | 任务依赖图 / 看板两种视图；任务详情（分支、worktree、汇报、token）；集成队列；项目用量；进度汇报时间线 | 演示数据 |

截图见 `docs/screenshots/`。

## 10. 路线图

| 阶段 | 内容 | 产出 |
|---|---|---|
| **M0（本次）** | 设计文档；工具清单与启动检测；角色分配；界面原型 | 可运行的 `autocoder` + 界面 |
| M1 项目落地 | `project` + `gitops`：真实创建/接管仓库、写 `.autocoder/`、任务文件读写、分支与 worktree 管理；角色分配保存到服务端 | 界面上的项目/任务变成真实数据 |
| M2 派发、汇报与计量 | `dispatch` + `watcher` + `meter`：用 headless 模板派发给 CLI 工具，并发上限、子进程日志、超时与取消；解析提交 trailer；解析各工具的 token 用量；SSE 推送进度 | 单个任务可以自动跑完一轮，用量页变成真实数据 |
| M3 项目管理闭环 | lead 读需求生成 `plan.md` 与任务依赖图；集成队列自动合并 + 跑测试；失败回派；测试通过后自动合并到主分支 | 从一句需求到主分支的全自动流程 |
| M4 打磨 | 结合通过率与 token 消耗的动态能力评分、用量预算与告警、PR 集成、Wails 桌面壳、全平台发布包 | 可日常使用的版本 |

## 11. 已确认的决定

| 问题 | 决定 |
|---|---|
| 费用 / token | 记录全部工具的全部 token 消耗，提供计量明细（§7） |
| 合并到主分支 | 集成测试通过后由项目管理直接合并，默认不等确认（可在设置中关闭） |
| 并发上限 | 默认同时运行 3 个工具进程，最多 5 个 |
| IDE 工具 | 统一只用 CLI 工具，便于派发、管理和计量 |
