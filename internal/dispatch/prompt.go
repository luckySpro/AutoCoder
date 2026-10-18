package dispatch

import (
	"fmt"
	"strings"

	"github.com/luckySpro/AutoCoder/internal/project"
)

// buildPrompt 生成派发给工具的任务说明（DESIGN §6.3）。
func buildPrompt(p *project.Project, t *project.Task, deps []project.Report, conflicts []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "你在项目 %s 中负责任务 %s：%s\n", p.Name, t.ID, t.Title)
	if t.Description != "" {
		fmt.Fprintf(&b, "\n## 任务说明\n%s\n", strings.TrimSpace(t.Description))
	}
	if len(t.Acceptance) > 0 {
		b.WriteString("\n## 验收标准\n")
		for i, a := range t.Acceptance {
			fmt.Fprintf(&b, "%d. %s\n", i+1, a)
		}
	}
	if len(t.Verify) > 0 || p.TestCommand != "" {
		b.WriteString("\n## 完成后 AutoCoder 会运行这些命令验证\n")
		for _, v := range t.Verify {
			fmt.Fprintf(&b, "- `%s`\n", v)
		}
		if p.TestCommand != "" {
			fmt.Fprintf(&b, "- `%s`（项目测试）\n", p.TestCommand)
		}
		b.WriteString("请先自己运行这些命令确认通过。缺依赖或命令（如 command not found）时自己安装（npm install 等），并把新依赖写进依赖清单，不要留给别人处理。\n")
	}
	if len(t.Scope) > 0 {
		fmt.Fprintf(&b, "\n## 修改范围\n只修改这些路径：%s\n", strings.Join(t.Scope, "、"))
	}
	if len(deps) > 0 {
		b.WriteString("\n## 依赖任务已完成的内容\n")
		for _, d := range deps {
			fmt.Fprintf(&b, "- %s：%s\n", d.TaskID, clip(oneLine(d.Summary), 300))
		}
	}
	if len(conflicts) > 0 {
		fmt.Fprintf(&b, "\n## 先解决合并冲突\n集成分支的最新改动已合入你的分支，以下文件有冲突标记（<<<<<<< / >>>>>>>），请先保留双方意图解决冲突：%s\n", strings.Join(conflicts, "、"))
	}
	if (t.Attempts > 1 || len(t.Avoid) > 0) && t.LastError != "" {
		fmt.Fprintf(&b, "\n## 上一次尝试没有通过\n这是第 %d 次尝试。上次的问题：\n%s\n请在已有改动的基础上修正。\n", t.Attempts, clip(t.LastError, 3000))
	}
	b.WriteString(`
## 工作规则
- 只在当前目录（任务专用的 git worktree）中工作，只修改修改范围内的文件；确需改动范围外的文件（例如依赖清单），在最后的答复中说明原因。
- 不要执行 git commit、git push、切换分支，也不要修改 .autocoder/ 目录；提交由 AutoCoder 完成。
- 不要等待任何人回复，遇到不确定的地方按最合理的方式处理并在答复中说明。
- 完成后用几句话总结：做了什么、还有什么没做。
`)
	return b.String()
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
