package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/luckySpro/AutoCoder/internal/gitops"
	"github.com/luckySpro/AutoCoder/internal/project"
	"github.com/luckySpro/AutoCoder/internal/roles"
	"github.com/luckySpro/AutoCoder/internal/settings"
	"github.com/luckySpro/AutoCoder/internal/toolscan"
)

// 评审结论无法解析时最多评审几次，之后按通过处理并在汇报中注明。
const maxReviewTries = 2

// chooseReviewer：优先承担评审角色、且不是写这段代码的工具；没有就交给 lead。
// 只有在没有别的工具能评审时才由作者自审；别的工具只是暂时在忙，就等它空出来。
func chooseReviewer(tools []toolscan.Detection, assigns []roles.Assignment, busy, cooling map[string]bool, author string) (toolscan.Detection, bool) {
	var order []string
	for _, want := range []roles.Role{roles.Reviewer, roles.Lead} {
		for _, a := range assigns {
			if slices.Contains(a.Roles, want) && a.ToolID != author && !slices.Contains(order, a.ToolID) {
				order = append(order, a.ToolID)
			}
		}
	}
	byID := map[string]toolscan.Detection{}
	for _, d := range tools {
		byID[d.ID] = d
	}
	usable := func(id string) (toolscan.Detection, bool) {
		d, ok := byID[id]
		return d, ok && d.Schedulable() && !d.Disabled && !cooling[id]
	}
	others := false
	for _, id := range order {
		if d, ok := usable(id); ok {
			others = true
			if !busy[id] {
				return d, true
			}
		}
	}
	if !others && author != "" {
		if d, ok := usable(author); ok && !busy[author] {
			return d, true
		}
	}
	return toolscan.Detection{}, false
}

type verdict struct {
	Verdict string    `json:"verdict"`
	Summary string    `json:"summary"`
	Issues  issueList `json:"issues"`
}

// issueList 接受字符串数组，也接受对象数组：评审工具常写成
// {"severity": "info", "issue": "…"}，以前这种写法会被当成"没有结论"反复重评。
type issueList []string

func (l *issueList) UnmarshalJSON(data []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		var one string
		if json.Unmarshal(data, &one) == nil { // 单个字符串
			*l = issueList{one}
			return nil
		}
		if string(data) == "null" {
			*l = nil
			return nil
		}
		return err
	}
	out := make(issueList, 0, len(raw))
	for _, r := range raw {
		var s string
		if json.Unmarshal(r, &s) == nil {
			out = append(out, s)
			continue
		}
		var m map[string]any
		if json.Unmarshal(r, &m) != nil {
			continue
		}
		text := ""
		for _, k := range []string{"issue", "description", "message", "text", "detail", "problem", "summary", "title"} {
			if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
				text = strings.TrimSpace(v)
				break
			}
		}
		if text == "" {
			b, _ := json.Marshal(m)
			text = string(b)
		}
		if f, ok := m["file"].(string); ok && f != "" && !strings.Contains(text, f) {
			text = f + "：" + text
		}
		if sev, ok := m["severity"].(string); ok && sev != "" {
			text = "[" + sev + "] " + text
		}
		out = append(out, text)
	}
	*l = out
	return nil
}

// parseVerdict 读出评审结论：pass 或 changes。
func parseVerdict(text string) (verdict, bool) {
	var v verdict
	if extractJSON(text, &v) != nil {
		return v, false
	}
	v.Issues = clean(v.Issues)
	switch strings.ToLower(strings.TrimSpace(v.Verdict)) {
	case "pass", "approve", "approved", "lgtm", "ok", "通过":
		v.Verdict = "pass"
	case "changes", "request_changes", "changes_requested", "reject", "rejected", "需要修改", "打回":
		v.Verdict = "changes"
	default:
		return v, false
	}
	if v.Verdict == "changes" && len(v.Issues) == 0 {
		return v, false // 打回却没说问题，没法据此修改
	}
	return v, true
}

// review 让评审工具在只读模式下审阅任务分支相对集成分支的改动（§6.5 第 6 步）。
// 通过后进入集成；需要修改时问题清单作为下一次尝试的输入。
func (e *Engine) review(ctx context.Context, p *project.Project, taskID string, tool toolscan.Detection, author string, run *Run, st settings.Settings) {
	e.Store.Lock()
	t := *p.Task(taskID)
	devSummary, verifyText := "", ""
	for _, r := range p.Reports { // 最新的在前
		if r.TaskID == taskID && r.Status == project.Review {
			devSummary = strings.TrimSpace(strings.TrimPrefix(r.Summary, reviewWaitNote))
			verifyText = r.Verify
			break
		}
	}
	e.Store.Unlock()

	logf := e.appendLog(p.ID, taskID, t.Attempts)
	defer func() {
		if logf != nil {
			logf.Close()
		}
	}()
	say := func(text, tone string) {
		run.emit(Event{Type: "raw", Text: text, Tone: tone})
		writeLog(logf, text, false)
	}
	say(fmt.Sprintf("── 评审 · %s · 第 %d 次尝试的改动", tool.Name, t.Attempts), "dim")

	dir := p.TaskDir(taskID)
	if _, err := os.Stat(dir); err != nil {
		if err := gitops.EnsureWorktree(ctx, p.Path, dir, t.Branch, p.IntegrationBranch); err != nil {
			e.reviewDone(p, taskID, run, "failed", project.Report{TaskID: taskID, From: tool.ID, At: time.Now(), Attempt: t.Attempts},
				"准备评审目录失败："+err.Error(), func(t *project.Task) { t.LastError = "准备评审目录失败：" + err.Error() })
			return
		}
	}
	diffStat, _ := gitops.Run(ctx, dir, "diff", "--stat", p.IntegrationBranch+"...HEAD")
	diff, _ := gitops.Run(ctx, dir, "diff", p.IntegrationBranch+"...HEAD")
	prompt := buildReviewPrompt(p, &t, devSummary, verifyText, diffStat, diff)

	out := e.exec(ctx, dir, tool, prompt, true, run, logf, st)
	usage := out.usage
	if !out.exact {
		usage = estimate(prompt, strings.Join(out.tail, "\n")+out.final)
	}
	run.update(func(s *Stat) { s.Tokens = usage.Total() })
	e.recordUsage(p, taskID, string(roles.Reviewer), t.Attempts, tool.ID, run, out, usage)
	// 评审只读：工具不顾规则改了文件的，全部丢弃
	if ch, err := gitops.Changes(ctx, dir); err == nil && len(ch) > 0 {
		_ = gitops.Discard(ctx, dir)
		run.emit(Event{Type: "act", Kind: "warn", Text: "评审工具改动了文件，已全部丢弃", Tone: "warn"})
	}
	report := project.Report{TaskID: taskID, From: tool.ID, At: time.Now(), Attempt: t.Attempts, Exit: out.exit, Tokens: usage.Total()}

	switch {
	case out.startErr != nil:
		e.cool(tool.ID, true)
		e.reviewDone(p, taskID, run, "failed", report, tool.Name+" 无法启动，换一个工具评审", func(t *project.Task) {})
		return
	case out.authErr:
		e.mu.Lock()
		e.authWait[tool.ID] = true
		e.mu.Unlock()
		e.reviewDone(p, taskID, run, "failed", report, tool.Name+" 未登录，换一个工具评审", func(t *project.Task) {
			t.LastError = fmt.Sprintf("评审工具 %s 未登录：在终端运行 %s 完成登录", tool.Name, tool.Commands[0])
		})
		return
	case out.limitErr:
		until := e.cool(tool.ID, false)
		e.reviewDone(p, taskID, run, "failed", report, fmt.Sprintf("%s 被限流，冷却到 %s 后再评审", tool.Name, until.Format("15:04")), func(t *project.Task) {})
		return
	case out.stopBy == quitReason:
		e.reviewDone(p, taskID, run, "stopped", report, "AutoCoder 退出时中断，下次启动重新评审", func(t *project.Task) {})
		return
	case out.stopBy == "用户停止":
		e.reviewDone(p, taskID, run, "stopped", report, "评审被停止", func(t *project.Task) { t.Status = project.Cancelled })
		return
	}

	v, ok := parseVerdict(out.final)
	if out.stopBy != "" || out.exit != 0 {
		ok = false
	}
	if !ok {
		e.Store.Lock()
		tries := p.Task(taskID).ReviewTries + 1
		e.Store.Unlock()
		if tries < maxReviewTries {
			why := "评审没有给出可解析的结论，重新评审"
			if out.stopBy != "" {
				why = "评审" + out.stopBy + "，重新评审"
			}
			e.reviewDone(p, taskID, run, "failed", report, why, func(t *project.Task) { t.ReviewTries = tries })
			return
		}
		v = verdict{Verdict: "pass", Summary: fmt.Sprintf("评审连续 %d 次没有给出可解析的结论，按通过处理", tries)}
		run.emit(Event{Type: "act", Kind: "warn", Text: v.Summary, Tone: "warn"})
	}

	report.Review = v.Verdict
	report.Issues = v.Issues
	summary := strings.TrimSpace(v.Summary)
	if v.Verdict == "changes" {
		reason := "评审要求修改：\n- " + strings.Join(v.Issues, "\n- ")
		run.emit(Event{Type: "act", Kind: "warn", Text: fmt.Sprintf("评审要求修改（%d 个问题）", len(v.Issues)), Tone: "warn"})
		e.reviewDone(p, taskID, run, "failed", report, summaryWith(summary, reason), func(t *project.Task) {
			t.ReviewTries = 0
			e.retryOrBlock(t, st, reason)
		})
		return
	}

	// 通过：写评审汇报，接着由编排器集成
	if summary == "" {
		summary = "评审通过"
	}
	run.emit(Event{Type: "act", Kind: "say", Text: "评审通过", Tone: "ok"})
	e.Store.Lock()
	report.Status = project.Review
	report.Summary = summary
	_ = project.AppendReport(p, report)
	if tt := p.Task(taskID); tt != nil {
		tt.ReviewTries = 0
		tt.Status = project.Integrating
		_ = project.SaveTask(p, tt)
	}
	e.Store.Unlock()
	run.update(func(s *Stat) { s.Phase = "integrating"; s.Tool = author })
	if devSummary == "" {
		devSummary = summary
	}
	e.integrateTask(ctx, p, taskID, author, run, logf, st, project.Report{TaskID: taskID, From: "autocoder", At: time.Now(), Attempt: t.Attempts, Verify: verifyText}, devSummary)
}

// reviewDone 写评审汇报并结束这次评审；任务状态默认保持 review，由调度器重新派出。
func (e *Engine) reviewDone(p *project.Project, taskID string, run *Run, phase string, r project.Report, summary string, f func(*project.Task)) {
	if r.Status == "" {
		r.Status = project.Review
	}
	e.finishReport(p, taskID, run, phase, r, summary, f)
}

const reviewWaitNote = "验证通过，等待评审"

func buildReviewPrompt(p *project.Project, t *project.Task, devSummary, verify, stat, diff string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "你是项目 %s 的代码评审。请审阅任务 %s「%s」在当前分支上的改动（相对 %s），判断能否合并。当前目录是任务的 worktree，只读，不要修改任何文件。\n", p.Name, t.ID, t.Title, p.IntegrationBranch)
	if t.Description != "" {
		fmt.Fprintf(&b, "\n## 任务说明\n%s\n", t.Description)
	}
	if len(t.Acceptance) > 0 {
		b.WriteString("\n## 验收标准\n")
		for i, a := range t.Acceptance {
			fmt.Fprintf(&b, "%d. %s\n", i+1, a)
		}
	}
	if verify != "" {
		fmt.Fprintf(&b, "\n验证命令已由 AutoCoder 运行：%s。\n", verify)
	}
	if devSummary != "" {
		fmt.Fprintf(&b, "\n## 开发者的总结\n%s\n", clip(devSummary, 3000))
	}
	fmt.Fprintf(&b, "\n## 改动统计\n```\n%s\n```\n", clip(stat, 4000))
	if diff != "" {
		const limit = 60000
		fmt.Fprintf(&b, "\n## 改动\n```diff\n%s\n```\n", clip(diff, limit))
		if len(diff) > limit {
			b.WriteString("（改动较长，已截断；需要时用 git diff " + p.IntegrationBranch + "...HEAD 查看完整内容）\n")
		}
	}
	b.WriteString(`
## 评审要点
- 是否满足每一条验收标准；
- 正确性：边界、错误处理、并发、数据一致性；
- 安全：注入、越权、敏感信息；
- 是否破坏已有功能或接口约定；
- 只提会影响合并的问题，风格偏好不算。

## 输出
最后的答复只包含一个 ` + "```json" + ` 代码块：
` + "```json" + `
{"verdict": "pass", "summary": "一两句话的结论", "issues": []}
` + "```" + `
verdict 为 "pass"（可以合并）或 "changes"（需要修改）；需要修改时 issues 逐条写出问题和修改建议，每条要具体到文件或行为。
`)
	return b.String()
}

// appendLog 以追加方式打开某次尝试的日志（评审接在开发输出之后）。
func (e *Engine) appendLog(projectID, taskID string, attempt int) *os.File {
	if e.LogDir == "" {
		return nil
	}
	path := e.LogPath(projectID, taskID, attempt)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil
	}
	return f
}
