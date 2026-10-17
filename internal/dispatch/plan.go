package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/luckySpro/AutoCoder/internal/gitops"
	"github.com/luckySpro/AutoCoder/internal/project"
	"github.com/luckySpro/AutoCoder/internal/roles"
	"github.com/luckySpro/AutoCoder/internal/settings"
	"github.com/luckySpro/AutoCoder/internal/toolscan"
)

// 计划规模的上限（DESIGN §6.1）。
const (
	maxPlanTasks    = 30
	maxPlanAttempts = 3 // 第一次生成 + 最多 2 次按校验错误重写
)

// ---------- 由 lead 生成计划（§6.1） ----------

// plan 让 lead 在集成分支的只读副本里分析仓库，产出计划；校验不通过时带着错误重写。
func (e *Engine) plan(ctx context.Context, p *project.Project, reqID string, tool toolscan.Detection, run *Run, st settings.Settings) {
	e.Store.Lock()
	r := *p.Request(reqID)
	existing := make([]project.Task, 0, len(p.Tasks))
	for _, t := range p.Tasks {
		existing = append(existing, *t)
	}
	e.Store.Unlock()
	tools := e.Tools(ctx)
	assigns := Assignments(st, tools)

	logf, _ := e.openLog(p.ID, reqID, r.Attempts)
	defer func() {
		if logf != nil {
			logf.Close()
		}
	}()
	run.emit(Event{Type: "raw", Text: fmt.Sprintf("%s · 规划 %s · 第 %d 次", tool.Name, reqID, r.Attempts), Tone: "dim"})

	dir := filepath.Join(p.WorktreeRoot(), "_plan")
	p.GitMu.Lock()
	err := gitops.SyncDetached(ctx, p.Path, dir, p.IntegrationBranch)
	p.GitMu.Unlock()
	if err != nil {
		e.finishPlan(p, reqID, run, tool.ID, 0, func(r *project.Request) string {
			r.Status, r.Error = project.ReqFailed, "准备规划目录失败："+err.Error()
			return r.Error
		})
		return
	}

	prompt := buildPlanPrompt(p, &r, existing, assigns, tools)
	out := e.exec(ctx, dir, tool, prompt, true, run, logf, st)
	usage := out.usage
	if !out.exact {
		usage = estimate(prompt, strings.Join(out.tail, "\n")+out.final)
	}
	run.update(func(s *Stat) { s.Tokens = usage.Total() })
	e.recordUsage(p, reqID, string(roles.Lead), r.Attempts, tool.ID, run, out, usage)
	_ = gitops.Discard(ctx, dir) // 只读模式失效时工具可能写了文件：规划目录每次都还原
	tok := usage.Total()

	switch {
	case out.startErr != nil:
		e.cool(tool.ID, true)
		e.finishPlan(p, reqID, run, tool.ID, tok, func(r *project.Request) string {
			r.Attempts--
			r.Error = tool.Name + " 无法启动：" + out.startErr.Error()
			return r.Error
		})
		return
	case out.authErr:
		e.mu.Lock()
		e.authWait[tool.ID] = true
		e.mu.Unlock()
		e.finishPlan(p, reqID, run, tool.ID, tok, func(r *project.Request) string {
			r.Attempts--
			r.Status = project.ReqFailed
			r.Error = fmt.Sprintf("%s 未登录或凭据已过期：在终端运行 %s 完成登录，然后点「重新生成」。", tool.Name, tool.Commands[0])
			return r.Error
		})
		return
	case out.limitErr:
		until := e.cool(tool.ID, false)
		e.finishPlan(p, reqID, run, tool.ID, tok, func(r *project.Request) string {
			r.Attempts--
			r.Error = fmt.Sprintf("%s 被限流，%s 后重新规划", tool.Name, until.Format("15:04"))
			return r.Error
		})
		return
	case out.stopBy == "用户停止":
		e.finishPlan(p, reqID, run, tool.ID, tok, func(r *project.Request) string {
			r.Status, r.Error = project.ReqFailed, "规划被停止"
			return r.Error
		})
		return
	}

	var errs, notes []string
	var plan *project.Plan
	var questions []string
	switch {
	case out.stopBy != "":
		errs = []string{out.stopBy}
	case out.exit != 0:
		errs = []string{fmt.Sprintf("%s 非零退出（退出码 %d）：%s", tool.Name, out.exit, strings.Join(out.tail[max(0, len(out.tail)-8):], "\n"))}
	default:
		raw, perr := parsePlan(out.final)
		if perr != nil {
			errs = []string{perr.Error()}
			break
		}
		questions = raw.Questions
		if len(questions) == 0 || len(raw.Tasks) > 0 {
			plan = raw.toPlan()
			e.Store.Lock()
			errs, notes = validatePlan(p, plan, tools)
			e.Store.Unlock()
		}
	}

	var apply bool
	e.finishPlan(p, reqID, run, tool.ID, tok, func(r *project.Request) string {
		r.Error = ""
		switch {
		case len(questions) > 0:
			r.Status, r.Questions, r.Errors = project.ReqQuestions, questions, nil
			return fmt.Sprintf("有 %d 个问题需要你回答", len(questions))
		case len(errs) > 0:
			r.Errors = errs
			if r.Attempts >= maxPlanAttempts {
				r.Status = project.ReqFailed
				r.Error = fmt.Sprintf("连续 %d 次生成的计划都没有通过校验", r.Attempts)
				return r.Error + "：" + strings.Join(errs, "；")
			}
			return "计划没有通过校验，交回重写：" + strings.Join(errs, "；")
		}
		r.Plan, r.Notes, r.Errors, r.Questions = plan, notes, nil, nil
		r.Status = project.ReqProposed
		apply = !st.ConfirmPlan
		return fmt.Sprintf("生成了计划：%d 个任务", len(plan.Tasks))
	})
	if apply {
		if err := e.ApprovePlan(p.ID, reqID); err != nil {
			e.finishPlan(p, reqID, run, tool.ID, 0, func(r *project.Request) string {
				r.Status, r.Error = project.ReqFailed, "生成任务失败："+err.Error()
				return r.Error
			})
		}
	}
}

// finishPlan 更新需求、写汇报、提交状态并结束这次运行。f 返回写进汇报的一句话。
func (e *Engine) finishPlan(p *project.Project, reqID string, run *Run, tool string, tokens int64, f func(*project.Request) string) {
	e.Store.Lock()
	r := p.Request(reqID)
	var summary, status string
	var attempt int
	if r != nil {
		summary = f(r)
		r.Tokens += tokens
		_ = project.SaveRequest(p, r)
		status, attempt = r.Status, r.Attempts
		_ = project.AppendReport(p, project.Report{TaskID: reqID, From: tool, At: time.Now(), Status: status, Summary: summary, Attempt: attempt, Tokens: tokens})
	}
	e.Store.Unlock()
	go func() {
		p.GitMu.Lock()
		e.Store.Lock()
		_ = project.CommitState(context.Background(), p, fmt.Sprintf("%s 规划：%s", reqID, status))
		e.Store.Unlock()
		p.GitMu.Unlock()
	}()
	phase := "done"
	if status == project.ReqFailed || status == project.ReqPlanning {
		phase = "failed"
	}
	run.update(func(s *Stat) {
		s.Phase = phase
		s.Result = clip(oneLine(summary), 200)
	})
	e.Wake()
}

// ---------- 计划的格式 ----------

type rawTask struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Kind        string   `json:"kind"`
	Role        string   `json:"role"`
	Assignee    string   `json:"assignee"`
	DependsOn   []string `json:"depends_on"`
	DependsOn2  []string `json:"dependsOn"`
	Scope       []string `json:"scope"`
	Acceptance  []string `json:"acceptance"`
	Verify      []string `json:"verify"`
	Manual      bool     `json:"manual"`
}

type rawPlan struct {
	Questions     []string  `json:"questions"`
	Summary       string    `json:"summary"`
	TestCommand   string    `json:"test_command"`
	TestCommand2  string    `json:"testCommand"`
	SetupCommand  string    `json:"setup_command"`
	SetupCommand2 string    `json:"setupCommand"`
	Tasks         []rawTask `json:"tasks"`
}

func (r rawPlan) toPlan() *project.Plan {
	pl := &project.Plan{Summary: strings.TrimSpace(r.Summary), TestCommand: firstNonEmpty(r.TestCommand, r.TestCommand2), SetupCommand: firstNonEmpty(r.SetupCommand, r.SetupCommand2)}
	for _, t := range r.Tasks {
		deps := t.DependsOn
		if len(deps) == 0 {
			deps = t.DependsOn2
		}
		pl.Tasks = append(pl.Tasks, project.PlanTask{
			ID: strings.TrimSpace(t.ID), Title: strings.TrimSpace(t.Title), Description: strings.TrimSpace(t.Description),
			Kind: strings.TrimSpace(t.Kind), Role: strings.TrimSpace(t.Role), Assignee: strings.TrimSpace(t.Assignee),
			DependsOn: clean(deps), Scope: clean(t.Scope), Acceptance: clean(t.Acceptance), Verify: clean(t.Verify), Manual: t.Manual,
		})
	}
	return pl
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return ""
}

func clean(ss []string) []string {
	var out []string
	for _, s := range ss {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

var fenceRe = regexp.MustCompile("(?s)```(?:json|JSON)?\\s*\\n(.*?)\\n?```")

// parsePlan 从 lead 的最终答复里取出计划 JSON：优先最后一个 ```json 代码块，否则取最外层的 {…}。
func parsePlan(text string) (rawPlan, error) {
	var p rawPlan
	if err := extractJSON(text, &p); err != nil {
		return p, err
	}
	if len(p.Tasks) == 0 && len(p.Questions) == 0 {
		return p, errors.New("计划里既没有任务也没有问题")
	}
	return p, nil
}

func extractJSON(text string, v any) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("最终答复是空的，没有找到 JSON")
	}
	var cands []string
	ms := fenceRe.FindAllStringSubmatch(text, -1)
	for i := len(ms) - 1; i >= 0; i-- {
		cands = append(cands, ms[i][1])
	}
	if i, j := strings.Index(text, "{"), strings.LastIndex(text, "}"); i >= 0 && j > i {
		cands = append(cands, text[i:j+1])
	}
	var lastErr error
	for _, c := range cands {
		if err := json.Unmarshal([]byte(strings.TrimSpace(c)), v); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	if lastErr == nil {
		return errors.New("最终答复里没有找到 JSON")
	}
	return fmt.Errorf("JSON 格式不正确：%v", lastErr)
}

// ---------- 计划校验（§6.1 第 3 步） ----------

var planKinds = map[string]bool{"feature": true, "ui": true, "test": true, "infra": true}
var planRoles = map[string]bool{"developer": true, "frontend": true, "tester": true, "reviewer": true, "lead": true}

// validatePlan 校验计划并做可以自动完成的调整（补默认值、范围重叠的任务改为先后执行）。
// 返回交回 lead 重写的错误，以及编排器做过的调整（调用方持有锁）。
func validatePlan(p *project.Project, pl *project.Plan, tools []toolscan.Detection) (errs, notes []string) {
	n := len(pl.Tasks)
	switch {
	case n == 0:
		return []string{"计划里没有任务"}, nil
	case n > maxPlanTasks:
		return []string{fmt.Sprintf("任务太多（%d 个），单个计划不超过 %d 个，请合并粒度过细的任务", n, maxPlanTasks)}, nil
	}
	known := map[string]bool{}
	for _, d := range tools {
		known[d.ID] = true
	}
	ids := map[string]bool{}
	for i := range pl.Tasks {
		t := &pl.Tasks[i]
		if t.ID == "" {
			t.ID = fmt.Sprintf("P%d", i+1)
			for ids[t.ID] || p.Task(t.ID) != nil {
				t.ID += "x"
			}
		}
		if ids[t.ID] {
			errs = append(errs, fmt.Sprintf("任务编号 %s 重复", t.ID))
		}
		if p.Task(t.ID) != nil {
			errs = append(errs, fmt.Sprintf("任务编号 %s 与项目里已有的任务重复，请用 P1、P2… 编号", t.ID))
		}
		ids[t.ID] = true
	}
	testCmd := firstNonEmpty(pl.TestCommand, p.TestCommand)
	for i := range pl.Tasks {
		t := &pl.Tasks[i]
		if t.Title == "" {
			errs = append(errs, fmt.Sprintf("%s 缺少标题", t.ID))
		}
		if t.Kind == "" || !planKinds[t.Kind] {
			t.Kind = "feature"
		}
		if t.Role == "" {
			t.Role = "developer"
		}
		if !planRoles[t.Role] {
			errs = append(errs, fmt.Sprintf("%s 的角色 %q 不存在，只能是 developer、frontend、tester、reviewer", t.ID, t.Role))
		}
		if t.Assignee != "" && !known[t.Assignee] {
			notes = append(notes, fmt.Sprintf("%s 指定的工具 %s 不在清单里，改为按角色分配", t.ID, t.Assignee))
			t.Assignee = ""
		}
		for _, d := range t.DependsOn {
			switch {
			case d == t.ID:
				errs = append(errs, fmt.Sprintf("%s 依赖了自己", t.ID))
			case !ids[d] && p.Task(d) == nil:
				errs = append(errs, fmt.Sprintf("%s 依赖的 %s 不存在", t.ID, d))
			}
		}
		if len(t.Acceptance) == 0 {
			errs = append(errs, fmt.Sprintf("%s 缺少验收标准", t.ID))
		}
		if len(t.Verify) == 0 && !t.Manual && testCmd == "" {
			errs = append(errs, fmt.Sprintf("%s 没有验证命令；请给出可执行的 verify 命令、项目的 test_command，或标注 manual: true（人工验收）", t.ID))
		}
		if len(t.Scope) == 0 && n > 1 {
			errs = append(errs, fmt.Sprintf("%s 缺少修改范围（scope），并行的任务需要靠它避免冲突", t.ID))
		}
	}
	if len(errs) > 0 {
		return errs, notes
	}
	if _, err := project.PlanOrder(pl.Tasks); err != nil {
		return []string{err.Error()}, notes
	}
	notes = append(notes, serializeOverlaps(pl.Tasks)...)
	return nil, notes
}

// serializeOverlaps：可以并行的两个任务修改范围重叠时，让后一个依赖前一个，避免合并冲突。
func serializeOverlaps(ts []project.PlanTask) []string {
	var notes []string
	idx := map[string]int{}
	for i, t := range ts {
		idx[t.ID] = i
	}
	reach := func(from, to int) bool { // from 是否（间接）依赖 to
		seen := map[int]bool{}
		stack := []int{from}
		for len(stack) > 0 {
			i := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if i == to {
				return true
			}
			if seen[i] {
				continue
			}
			seen[i] = true
			for _, d := range ts[i].DependsOn {
				if j, ok := idx[d]; ok {
					stack = append(stack, j)
				}
			}
		}
		return false
	}
	for j := range ts {
		for i := 0; i < j; i++ {
			if reach(i, j) || reach(j, i) {
				continue
			}
			if a, b, ok := scopesOverlap(ts[i].Scope, ts[j].Scope); ok {
				ts[j].DependsOn = append(ts[j].DependsOn, ts[i].ID)
				notes = append(notes, fmt.Sprintf("%s 与 %s 的修改范围重叠（%s / %s），改为 %s 在 %s 合并后开始", ts[i].ID, ts[j].ID, a, b, ts[j].ID, ts[i].ID))
			}
		}
	}
	return notes
}

func scopesOverlap(a, b []string) (string, string, bool) {
	for _, x := range a {
		for _, y := range b {
			bx, by := scopeBase(x), scopeBase(y)
			if under(bx, by) || under(by, bx) {
				return x, y, true
			}
		}
	}
	return "", "", false
}

// scopeBase 返回通配符之前的固定部分：带通配符的取所在目录（以 / 结尾），不带的就是路径本身。
func scopeBase(p string) string {
	p = strings.TrimPrefix(strings.TrimSpace(p), "./")
	if i := strings.IndexAny(p, "*?["); i >= 0 {
		p = p[:i]
		if j := strings.LastIndex(p, "/"); j >= 0 {
			return p[:j+1]
		}
		return ""
	}
	return strings.TrimSuffix(p, "/")
}

// under 判断 a 是否落在 b 之内（b 为空表示整个仓库）。
func under(a, b string) bool {
	if b == "" || a == b {
		return true
	}
	if !strings.HasSuffix(b, "/") {
		b += "/"
	}
	return strings.HasPrefix(a, b) || a+"/" == b
}

// ---------- 提示词 ----------

func buildPlanPrompt(p *project.Project, r *project.Request, existing []project.Task, assigns []roles.Assignment, tools []toolscan.Detection) string {
	var b strings.Builder
	fmt.Fprintf(&b, "你是项目 %s 的架构师和项目管理。当前目录是项目集成分支的只读副本。请先阅读仓库，理解现有结构，再把下面的需求拆成可以交给其他开发工具并行完成的任务。\n", p.Name)
	if p.Description != "" {
		fmt.Fprintf(&b, "\n## 项目简介\n%s\n", p.Description)
	}
	fmt.Fprintf(&b, "\n## 需求 %s\n%s\n", r.ID, strings.TrimSpace(r.Text))
	if len(r.QA) > 0 {
		b.WriteString("\n## 之前的问答\n")
		for _, qa := range r.QA {
			b.WriteString(qa + "\n\n")
		}
	}
	if r.Feedback != "" {
		b.WriteString("\n## 对上一版计划的修改意见\n" + r.Feedback + "\n")
		if r.Plan != nil {
			if data, err := json.Marshal(r.Plan); err == nil {
				b.WriteString("\n上一版计划：\n```json\n" + clip(string(data), 12000) + "\n```\n")
			}
		}
	}
	if len(r.Errors) > 0 {
		b.WriteString("\n## 上一次输出没有通过校验，请修正\n")
		for _, e := range r.Errors {
			b.WriteString("- " + e + "\n")
		}
	}
	if len(existing) > 0 {
		b.WriteString("\n## 项目里已有的任务（新任务可以依赖它们，不要重复）\n")
		for _, t := range existing {
			fmt.Fprintf(&b, "- %s [%s] %s；范围：%s\n", t.ID, t.Status, t.Title, strings.Join(t.Scope, "、"))
		}
	}
	if p.TestCommand != "" {
		fmt.Fprintf(&b, "\n项目测试命令已设置为 `%s`，每个任务合并前都会运行。\n", p.TestCommand)
	}
	b.WriteString("\n## 可用的角色与工具\n")
	names := map[string]string{}
	for _, d := range tools {
		names[d.ID] = d.Name
	}
	for _, a := range assigns {
		var rs []string
		for _, x := range a.Roles {
			rs = append(rs, string(x))
		}
		fmt.Fprintf(&b, "- %s（%s）：%s\n", names[a.ToolID], a.ToolID, strings.Join(rs, "、"))
	}
	b.WriteString("\n## 输出\n不要修改任何文件。最后的答复只包含一个 ```json 代码块，格式如下：\n")
	b.WriteString("```json\n" + `{
  "questions": [],
  "summary": "架构说明（Markdown）：整体思路、模块划分、接口约定、任务之间如何衔接",
  "test_command": "go test ./...",
  "setup_command": "",
  "tasks": [
    {
      "id": "P1",
      "title": "订单 API 与状态机",
      "description": "要做什么、接口约定、注意事项，写得足够具体，让没看过需求的人也能完成",
      "kind": "feature",
      "role": "developer",
      "depends_on": [],
      "scope": ["internal/order/**"],
      "acceptance": ["订单状态：待支付 → 已支付 → 已发货 → 完成 / 取消"],
      "verify": ["go test ./internal/order/..."]
    }
  ]
}` + "\n```\n")
	b.WriteString(`规则：
- 任务编号用 P1、P2…；depends_on 可以引用计划内的编号，也可以引用上面已有任务的编号。
- kind 只能是 feature、ui、test、infra；role 只能是 developer、frontend、tester、reviewer。界面与视觉相关的任务用 frontend。
- 每个任务都要有 scope（允许修改的路径，支持 ** 通配）、至少一条 acceptance，以及可执行的 verify 命令；确实只能人工验收的任务写 "manual": true。
- 可以并行的任务 scope 不要重叠；重叠的会被自动改为先后执行。
- 单个任务控制在 45 分钟以内能完成，整个计划不超过 30 个任务。
- test_command 是整个项目的测试命令；项目已经设置过就留空。setup_command 是每个新 worktree 需要的依赖安装命令，没有就留空。
- 需求有关键信息不清楚、无法合理假设时，把问题写进 questions，tasks 留空；能合理假设的就直接规划，并在 summary 里写明假设。
`)
	return b.String()
}

// ---------- 你对需求的操作 ----------

// NewRequest 记录需求并交给 lead 规划。
func (e *Engine) NewRequest(projectID, text string) (project.Request, error) {
	e.Store.Lock()
	defer e.Store.Unlock()
	p := e.Store.Get(projectID)
	if p == nil {
		return project.Request{}, errors.New("项目不存在")
	}
	r, err := project.NewRequest(p, text)
	if err != nil {
		return project.Request{}, err
	}
	go e.Wake()
	return *r, nil
}

func (e *Engine) withRequest(projectID, reqID string, f func(p *project.Project, r *project.Request) error) error {
	e.Store.Lock()
	defer e.Store.Unlock()
	p := e.Store.Get(projectID)
	if p == nil {
		return errors.New("项目不存在")
	}
	r := p.Request(reqID)
	if r == nil {
		return errors.New("需求不存在")
	}
	if err := f(p, r); err != nil {
		return err
	}
	go e.Wake()
	return project.SaveRequest(p, r)
}

// Answer 回答 lead 的问题，重新规划。
func (e *Engine) Answer(projectID, reqID, answer string) error {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return errors.New("请填写回答")
	}
	return e.withRequest(projectID, reqID, func(p *project.Project, r *project.Request) error {
		if r.Status != project.ReqQuestions {
			return errors.New("这条需求现在没有待回答的问题")
		}
		r.QA = append(r.QA, "问：\n- "+strings.Join(r.Questions, "\n- ")+"\n答：\n"+answer)
		r.Questions, r.Errors, r.Error = nil, nil, ""
		r.Status, r.Attempts = project.ReqPlanning, 0
		return nil
	})
}

// Regenerate 带着你的意见重新生成计划。
func (e *Engine) Regenerate(projectID, reqID, feedback string) error {
	return e.withRequest(projectID, reqID, func(p *project.Project, r *project.Request) error {
		switch r.Status {
		case project.ReqProposed, project.ReqFailed, project.ReqQuestions:
		default:
			return errors.New("这条需求现在不能重新生成")
		}
		if r.Tool != "" {
			e.ClearAuth(r.Tool)
		}
		r.Feedback = strings.TrimSpace(feedback)
		r.Questions, r.Errors, r.Error = nil, nil, ""
		r.Status, r.Attempts = project.ReqPlanning, 0
		return nil
	})
}

// Discard 放弃需求；正在规划的先停下来。
func (e *Engine) Discard(projectID, reqID string) error {
	e.Stop(projectID + "/" + reqID)
	return e.withRequest(projectID, reqID, func(p *project.Project, r *project.Request) error {
		if r.Status == project.ReqApproved {
			return errors.New("计划已经确认，任务可以逐个取消")
		}
		r.Status = project.ReqDiscarded
		return nil
	})
}

// UpdatePlan 保存你在界面上改过的计划（例如删掉不要的任务），重新校验。
func (e *Engine) UpdatePlan(ctx context.Context, projectID, reqID string, plan project.Plan) error {
	tools := e.Tools(ctx)
	return e.withRequest(projectID, reqID, func(p *project.Project, r *project.Request) error {
		if r.Status != project.ReqProposed {
			return errors.New("只有等你确认的计划可以修改")
		}
		// 删掉的任务不能再被依赖
		keep := map[string]bool{}
		for _, t := range plan.Tasks {
			keep[t.ID] = true
		}
		for i := range plan.Tasks {
			plan.Tasks[i].DependsOn = slices.DeleteFunc(plan.Tasks[i].DependsOn, func(d string) bool {
				return !keep[d] && p.Task(d) == nil
			})
		}
		errs, notes := validatePlan(p, &plan, tools)
		if len(errs) > 0 {
			return errors.New(strings.Join(errs, "；"))
		}
		r.Plan = &plan
		r.Notes = append(r.Notes, notes...)
		return nil
	})
}

// ApprovePlan 确认计划：生成正式任务，依赖满足的立即进入派发队列。
func (e *Engine) ApprovePlan(projectID, reqID string) error {
	err := e.withRequest(projectID, reqID, func(p *project.Project, r *project.Request) error {
		if r.Status != project.ReqProposed {
			return errors.New("这条需求没有等待确认的计划")
		}
		return project.ApplyPlan(p, r)
	})
	if err != nil {
		return err
	}
	e.Store.Lock()
	p := e.Store.Get(projectID)
	e.Store.Unlock()
	if p != nil {
		go func() {
			p.GitMu.Lock()
			e.Store.Lock()
			_ = project.CommitState(context.Background(), p, reqID+" 计划确认，生成任务")
			e.Store.Unlock()
			p.GitMu.Unlock()
		}()
	}
	return nil
}
