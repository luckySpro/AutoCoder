# AutoCoder

单台设备上多个 AI 开发工具（Claude Code、Codex、Cursor……）的协同助手：
启动时检测本机已安装的工具，按能力分配 **架构与项目管理 / 开发 / 前端 / 测试 / 评审** 角色，
以 **git 仓库** 为核心管理项目——任务即分支、汇报即提交、集成即合并。

设计与规划见 [docs/DESIGN.md](docs/DESIGN.md)。

## 当前进度（M0）

- ✅ 工具清单与启动检测，清单在界面上直接增删改、启停、导入导出（`internal/toolscan`）
- ✅ 默认角色分配（`internal/roles`）
- ✅ 读取本机 git 身份与 GitHub 账号仓库，像 Claude Code 一样添加项目（`internal/gitinfo`）
- ✅ 界面原型：项目列表、项目详情（分支泳道/依赖图/看板）、Token 用量、角色与运行、工具清单，深浅双主题
- ⏳ 项目与任务的真实 git 操作、任务派发与汇报解析（见路线图）

## 快速开始

需要 Go 1.24+、Node 20+。

```bash
make build          # 构建前端并打进单文件 bin/autocoder
./bin/autocoder     # 打开 http://127.0.0.1:7788
./bin/autocoder tools   # 只在终端里做工具检测
```

前端开发：

```bash
go run ./cmd/autocoder          # 后端 API（7788）
npm --prefix web run dev        # Vite 开发服务器，/api 自动转发
```

其他命令：`make test`、`make release`（交叉编译 linux/darwin/windows × amd64/arm64）。

## 截图

| 项目 | 分支泳道（深色） |
|---|---|
| ![](docs/screenshots/projects.png) | ![](docs/screenshots/project-lanes-dark.png) |
| **添加项目** | **工具清单** |
| ![](docs/screenshots/add-project.png) | ![](docs/screenshots/tools.png) |
| **编辑工具** | **Token 用量** |
| ![](docs/screenshots/tool-editor.png) | ![](docs/screenshots/usage.png) |
