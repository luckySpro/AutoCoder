package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// 需求状态（见 DESIGN §6.1）。
const (
	ReqPlanning  = "planning"  // lead 正在分析仓库、生成计划
	ReqQuestions = "questions" // lead 有问题要你回答
	ReqProposed  = "proposed"  // 计划已生成并通过校验，等你确认
	ReqApproved  = "approved"  // 已确认，任务已生成
	ReqFailed    = "failed"    // 多次生成的计划都不合格，或被停止
	ReqDiscarded = "discarded" // 你放弃了这条需求
)

// Request 是一条需求：.autocoder/requests/<id>.yaml。
type Request struct {
	ID     string `yaml:"id" json:"id"`
	Text   string `yaml:"text" json:"text"`
	Status string `yaml:"status" json:"status"`
	// QA 是之前几轮的问答，每项"问：…\n答：…"，重新规划时交给 lead。
	QA        []string `yaml:"qa,omitempty" json:"qa"`
	Questions []string `yaml:"questions,omitempty" json:"questions"` // 当前等你回答的问题
	Feedback  string   `yaml:"feedback,omitempty" json:"feedback,omitempty"`
	Plan      *Plan    `yaml:"plan,omitempty" json:"plan,omitempty"`
	// Notes 是编排器校验计划时自动做的调整（例如范围重叠改为先后执行）。
	Notes []string `yaml:"notes,omitempty" json:"notes"`
	// Errors 是最近一次计划没通过校验的原因，会交回 lead 重写。
	Errors    []string  `yaml:"errors,omitempty" json:"errors"`
	Attempts  int       `yaml:"attempts,omitempty" json:"attempts"`
	Error     string    `yaml:"error,omitempty" json:"error,omitempty"`
	Tool      string    `yaml:"tool,omitempty" json:"tool,omitempty"`
	Tasks     []string  `yaml:"tasks,omitempty" json:"tasks"` // 确认后生成的任务
	Tokens    int64     `yaml:"tokens,omitempty" json:"tokens"`
	CreatedAt time.Time `yaml:"created_at" json:"createdAt"`
	UpdatedAt time.Time `yaml:"updated_at" json:"updatedAt"`
}

// Plan 是 lead 产出的计划（plan.json 的内容）。
type Plan struct {
	Summary      string     `yaml:"summary,omitempty" json:"summary"` // 架构说明，给人看
	TestCommand  string     `yaml:"test_command,omitempty" json:"testCommand,omitempty"`
	SetupCommand string     `yaml:"setup_command,omitempty" json:"setupCommand,omitempty"`
	Tasks        []PlanTask `yaml:"tasks" json:"tasks"`
}

// PlanTask 是计划中的一个任务；ID 只在计划内有效（P1、P2…），确认后换成正式编号。
// DependsOn 可以引用计划内的编号，也可以引用项目里已有的任务。
type PlanTask struct {
	ID          string   `yaml:"id" json:"id"`
	Title       string   `yaml:"title" json:"title"`
	Description string   `yaml:"description,omitempty" json:"description"`
	Kind        string   `yaml:"kind,omitempty" json:"kind"`
	Role        string   `yaml:"role,omitempty" json:"role"`
	Assignee    string   `yaml:"assignee,omitempty" json:"assignee,omitempty"`
	DependsOn   []string `yaml:"depends_on,omitempty" json:"dependsOn"`
	Scope       []string `yaml:"scope,omitempty" json:"scope"`
	Acceptance  []string `yaml:"acceptance,omitempty" json:"acceptance"`
	Verify      []string `yaml:"verify,omitempty" json:"verify"`
	Manual      bool     `yaml:"manual,omitempty" json:"manual,omitempty"` // 人工验收，没有验证命令
}

// Request 按 id 查找需求。
func (p *Project) Request(id string) *Request {
	for _, r := range p.Requests {
		if r.ID == id {
			return r
		}
	}
	return nil
}

// NewRequest 记录一条需求，交给 lead 规划（调用方持有锁）。
func NewRequest(p *Project, text string) (*Request, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("请写下需求")
	}
	if len(text) > 20000 {
		return nil, errors.New("需求太长了，请精简到 2 万字以内")
	}
	n := 0
	for _, r := range p.Requests {
		var k int
		fmt.Sscanf(strings.TrimPrefix(r.ID, "R"), "%d", &k)
		n = max(n, k)
	}
	now := time.Now()
	r := &Request{ID: fmt.Sprintf("R%d", n+1), Text: text, Status: ReqPlanning, CreatedAt: now}
	p.Requests = append(p.Requests, r)
	return r, SaveRequest(p, r)
}

// SaveRequest 写入需求文件；有计划时同时写一份给人看的 plans/<id>.md（调用方持有锁）。
func SaveRequest(p *Project, r *Request) error {
	r.UpdatedAt = time.Now()
	data, err := yaml.Marshal(r)
	if err != nil {
		return err
	}
	if err := writeFile(filepath.Join(p.stateDir(), "requests", r.ID+".yaml"), data); err != nil {
		return err
	}
	if r.Plan != nil {
		return writeFile(filepath.Join(p.stateDir(), "plans", r.ID+".md"), []byte(planMarkdown(r)))
	}
	return nil
}

func planMarkdown(r *Request) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s 计划\n\n> %s\n\n", r.ID, strings.ReplaceAll(strings.TrimSpace(r.Text), "\n", "\n> "))
	if s := strings.TrimSpace(r.Plan.Summary); s != "" {
		b.WriteString(s + "\n\n")
	}
	if r.Plan.TestCommand != "" {
		fmt.Fprintf(&b, "项目测试：`%s`\n\n", r.Plan.TestCommand)
	}
	b.WriteString("## 任务\n\n")
	for _, t := range r.Plan.Tasks {
		fmt.Fprintf(&b, "### %s %s\n\n", t.ID, t.Title)
		if t.Description != "" {
			b.WriteString(t.Description + "\n\n")
		}
		fmt.Fprintf(&b, "- 角色：%s\n", t.Role)
		if len(t.DependsOn) > 0 {
			fmt.Fprintf(&b, "- 依赖：%s\n", strings.Join(t.DependsOn, "、"))
		}
		if len(t.Scope) > 0 {
			fmt.Fprintf(&b, "- 范围：`%s`\n", strings.Join(t.Scope, "`、`"))
		}
		for _, a := range t.Acceptance {
			fmt.Fprintf(&b, "- [ ] %s\n", a)
		}
		for _, v := range t.Verify {
			fmt.Fprintf(&b, "- 验证：`%s`\n", v)
		}
		b.WriteString("\n")
	}
	for _, n := range r.Notes {
		fmt.Fprintf(&b, "> 调整：%s\n", n)
	}
	return b.String()
}

// ApplyPlan 按依赖顺序把计划变成正式任务；任何一个失败都撤回已生成的（调用方持有锁）。
func ApplyPlan(p *Project, r *Request) error {
	if r.Plan == nil || len(r.Plan.Tasks) == 0 {
		return errors.New("计划里没有任务")
	}
	order, err := PlanOrder(r.Plan.Tasks)
	if err != nil {
		return err
	}
	before := len(p.Tasks)
	idmap := map[string]string{}
	var created []string
	undo := func() {
		for _, id := range created {
			_ = os.Remove(filepath.Join(p.stateDir(), "tasks", id+".yaml"))
		}
		p.Tasks = p.Tasks[:before]
	}
	for _, pt := range order {
		deps := make([]string, 0, len(pt.DependsOn))
		for _, d := range pt.DependsOn {
			if m, ok := idmap[d]; ok {
				deps = append(deps, m)
			} else {
				deps = append(deps, d)
			}
		}
		nt, err := NewTask(p, Task{
			Title: pt.Title, Description: pt.Description, Kind: pt.Kind, Role: pt.Role, Assignee: pt.Assignee,
			DependsOn: deps, Scope: pt.Scope, Acceptance: pt.Acceptance, Verify: pt.Verify, Request: r.ID,
		})
		if err != nil {
			undo()
			return fmt.Errorf("%s：%w", pt.ID, err)
		}
		idmap[pt.ID] = nt.ID
		created = append(created, nt.ID)
	}
	if p.TestCommand == "" && r.Plan.TestCommand != "" {
		p.TestCommand = r.Plan.TestCommand
	}
	if p.SetupCommand == "" && r.Plan.SetupCommand != "" {
		p.SetupCommand = r.Plan.SetupCommand
	}
	if err := saveMeta(p); err != nil {
		undo()
		return err
	}
	r.Tasks = created
	r.Status = ReqApproved
	r.Questions, r.Errors = nil, nil
	return SaveRequest(p, r)
}

// PlanOrder 返回依赖在前的顺序；计划内依赖成环时报错。
func PlanOrder(ts []PlanTask) ([]PlanTask, error) {
	by := map[string]int{}
	for i, t := range ts {
		by[t.ID] = i
	}
	state := make([]int, len(ts))
	var out []PlanTask
	var visit func(i int) error
	visit = func(i int) error {
		switch state[i] {
		case 1:
			return fmt.Errorf("计划中的依赖出现了环（%s）", ts[i].ID)
		case 2:
			return nil
		}
		state[i] = 1
		for _, d := range ts[i].DependsOn {
			if j, ok := by[d]; ok {
				if err := visit(j); err != nil {
					return err
				}
			}
		}
		state[i] = 2
		out = append(out, ts[i])
		return nil
	}
	for i := range ts {
		if err := visit(i); err != nil {
			return nil, err
		}
	}
	return out, nil
}
