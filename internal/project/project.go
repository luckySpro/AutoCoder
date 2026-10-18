// Package project 管理被 AutoCoder 接管的仓库：登记、`.autocoder/` 状态文件、任务、汇报与计量记录。
//
// 目录约定（见 docs/DESIGN.md §5）：
//
//	<仓库>                          用户自己的工作目录，AutoCoder 不在这里写任何文件
//	<仓库上级>/.ac-worktrees/<项目>/
//	  ├── _integration/             检出 ac/integration 的集成 worktree，.autocoder/ 状态在这里读写并提交
//	  └── <任务>/                   每个任务一个 worktree，检出 ac/<任务>-<slug>
//
// 状态随 ac/integration 提交进仓库，最终交付时一并合入主分支；换台机器 clone 后状态仍在。
package project

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/luckySpro/AutoCoder/internal/gitops"
	"gopkg.in/yaml.v3"
)

// 任务状态（见 DESIGN §6.2）。
const (
	Planned     = "planned"
	Ready       = "ready"
	Running     = "running"
	Testing     = "testing"
	Review      = "review"
	Integrating = "integrating"
	Merged      = "merged"
	Blocked     = "blocked"
	WaitingUser = "waiting-user"
	Cancelled   = "cancelled"
)

// IntegrationBranch 是编排器独占的集成分支。
const IntegrationBranch = "ac/integration"

// Meta 是 .autocoder/project.yaml。
type Meta struct {
	ID                string    `yaml:"id" json:"id"`
	Name              string    `yaml:"name" json:"name"`
	Description       string    `yaml:"description,omitempty" json:"description"`
	BaseBranch        string    `yaml:"base_branch" json:"baseBranch"`
	IntegrationBranch string    `yaml:"integration_branch" json:"integrationBranch"`
	Remote            string    `yaml:"remote,omitempty" json:"remote,omitempty"`
	TestCommand       string    `yaml:"test_command,omitempty" json:"testCommand"`
	SetupCommand      string    `yaml:"setup_command,omitempty" json:"setupCommand"`
	ScopeAllow        []string  `yaml:"scope_allow,omitempty" json:"scopeAllow"`
	CreatedAt         time.Time `yaml:"created_at" json:"createdAt"`
	DeliveredAt       time.Time `yaml:"delivered_at,omitempty" json:"deliveredAt,omitempty"` // 最近一次交付到主分支的时间
	TokenBudget       int64     `yaml:"token_budget,omitempty" json:"tokenBudget"`           // 项目 token 预算，0 表示不限
}

// Task 是 .autocoder/tasks/<id>.yaml。
type Task struct {
	ID          string   `yaml:"id" json:"id"`
	Title       string   `yaml:"title" json:"title"`
	Description string   `yaml:"description,omitempty" json:"description"`
	Kind        string   `yaml:"kind" json:"kind"` // feature | ui | test | infra
	Role        string   `yaml:"role" json:"role"`
	Assignee    string   `yaml:"assignee,omitempty" json:"assignee"`
	DependsOn   []string `yaml:"depends_on,omitempty" json:"dependsOn"`
	Branch      string   `yaml:"branch" json:"branch"`
	Scope       []string `yaml:"scope,omitempty" json:"scope"`
	Acceptance  []string `yaml:"acceptance,omitempty" json:"acceptance"`
	Verify      []string `yaml:"verify,omitempty" json:"verify"`
	Status      string   `yaml:"status" json:"status"`
	Attempts    int      `yaml:"attempts,omitempty" json:"attempts"`
	Commits     int      `yaml:"commits,omitempty" json:"commits"`
	LastError   string   `yaml:"last_error,omitempty" json:"lastError,omitempty"`
	Waiting     string   `yaml:"waiting,omitempty" json:"waiting,omitempty"` // waiting-user 时需要你处理的事
	Request     string   `yaml:"request,omitempty" json:"request,omitempty"` // 由哪条需求的计划生成
	// ConflictFails 是连续合并冲突的次数，达到 2 次转给你（§6.6）。
	ConflictFails int `yaml:"conflict_fails,omitempty" json:"conflictFails,omitempty"`
	// ReviewTries 是评审结论无法解析时已重跑的次数。
	ReviewTries int `yaml:"review_tries,omitempty" json:"-"`
	// Tool 是最近一次尝试用的工具；Avoid 是已经用完尝试次数的工具，换别的工具接着做。
	Tool      string    `yaml:"tool,omitempty" json:"tool,omitempty"`
	Avoid     []string  `yaml:"avoid,omitempty" json:"avoid,omitempty"`
	UpdatedAt time.Time `yaml:"updated_at" json:"updatedAt"`
}

// Report 是一次尝试结束后由编排器写下的汇报，追加在 .autocoder/reports/<任务>.md。
type Report struct {
	ID      string    `json:"id"`
	TaskID  string    `json:"taskId"`
	From    string    `json:"from"` // 工具 id；编排器自己的记录为 autocoder
	At      time.Time `json:"at"`
	Status  string    `json:"status"`
	Summary string    `json:"summary"`
	Commit  string    `json:"commit,omitempty"`
	Attempt int       `json:"attempt,omitempty"`
	Exit    int       `json:"exit"`
	Verify  string    `json:"verify,omitempty"`
	Tokens  int64     `json:"tokens,omitempty"`
	Files   int       `json:"files,omitempty"`
	Added   int       `json:"added,omitempty"`
	Removed int       `json:"removed,omitempty"`
	Review  string    `json:"review,omitempty"` // 评审结论：pass | changes
	Issues  []string  `json:"issues,omitempty"` // 评审提出的问题
}

// Usage 是一次工具调用的计量记录（DESIGN §7.2）。
type Usage struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"projectId"`
	TaskID      string    `json:"taskId"`
	Tool        string    `json:"tool"`
	Role        string    `json:"role"`
	At          time.Time `json:"at"`
	DurationSec int       `json:"durationSec"`
	ExitCode    int       `json:"exitCode"`
	Usage       Tokens    `json:"usage"`
	Estimated   bool      `json:"estimated"`
}

// Tokens 是 token 分项。
type Tokens struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
}

// Total 返回合计。
func (t Tokens) Total() int64 { return t.Input + t.Output + t.CacheRead + t.CacheWrite }

// Project 是一个已接管的仓库在内存中的状态。
type Project struct {
	Meta
	Path     string // 用户的仓库目录
	Tasks    []*Task
	Requests []*Request
	Reports  []Report
	// Spent 是该项目累计的 token（启动时从计量记录汇总，之后随记录累加）。
	Spent int64

	// GitMu 串行化集成 worktree 中的 git 操作（状态提交、合并、回退）。
	GitMu sync.Mutex
}

// WorktreeRoot 返回该项目所有 worktree 的上级目录。
func (p *Project) WorktreeRoot() string {
	return filepath.Join(filepath.Dir(p.Path), ".ac-worktrees", p.ID)
}

// ControlDir 返回集成 worktree（.autocoder/ 所在处）。
func (p *Project) ControlDir() string { return filepath.Join(p.WorktreeRoot(), "_integration") }

// TaskDir 返回任务 worktree。
func (p *Project) TaskDir(id string) string { return filepath.Join(p.WorktreeRoot(), id) }

func (p *Project) stateDir() string { return filepath.Join(p.ControlDir(), ".autocoder") }

// Task 按 id 查找。
func (p *Project) Task(id string) *Task {
	for _, t := range p.Tasks {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// Store 是全部项目的登记处，登记表在 ~/.autocoder/projects.json。
type Store struct {
	Home string

	mu       sync.Mutex
	projects []*Project
}

type entry struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

// Open 读取登记表并加载每个项目；单个项目加载失败不影响其他项目。
func Open(ctx context.Context, home string) (*Store, []error) {
	s := &Store{Home: home}
	var errs []error
	data, err := os.ReadFile(filepath.Join(home, "projects.json"))
	if err != nil && !os.IsNotExist(err) {
		return s, []error{err}
	}
	var list []entry
	if len(data) > 0 {
		if err := json.Unmarshal(data, &list); err != nil {
			return s, []error{fmt.Errorf("projects.json 解析失败：%w", err)}
		}
	}
	for _, e := range list {
		p, err := load(ctx, e.ID, e.Path)
		if err != nil {
			errs = append(errs, fmt.Errorf("项目 %s：%w", e.ID, err))
			continue
		}
		s.projects = append(s.projects, p)
	}
	return s, errs
}

func (s *Store) saveRegistry() error {
	list := make([]entry, 0, len(s.projects))
	for _, p := range s.projects {
		list = append(list, entry{p.ID, p.Path})
	}
	data, _ := json.MarshalIndent(list, "", "  ")
	return writeFile(filepath.Join(s.Home, "projects.json"), data)
}

// Lock / Unlock 保护全部项目的内存状态；引擎在读改任务时持有它。
func (s *Store) Lock()   { s.mu.Lock() }
func (s *Store) Unlock() { s.mu.Unlock() }

// Projects 返回项目列表（调用方需持有锁才能读写其内容）。
func (s *Store) Projects() []*Project { return s.projects }

// Get 按 id 查找项目（调用方需持有锁）。
func (s *Store) Get(id string) *Project {
	for _, p := range s.projects {
		if p.ID == id {
			return p
		}
	}
	return nil
}

// ---------- 创建与接管 ----------

// CreateRequest 是"添加项目"的请求。
type CreateRequest struct {
	Source       string `json:"source"` // github | url | local | new
	Repo         string `json:"repo"`   // owner/name（source=github）
	URL          string `json:"url"`    // 仓库地址（source=url）
	Path         string `json:"path"`   // 克隆目标 / 本机目录 / 新建目录
	Name         string `json:"name"`
	BaseBranch   string `json:"baseBranch"`
	CreateRemote bool   `json:"createRemote"`
	Description  string `json:"description"`
	TestCommand  string `json:"testCommand"`
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,80}$`)

// Runner 执行外部命令（用于 gh），测试时可替换。
type Runner func(ctx context.Context, dir, name string, args ...string) (string, error)

// Create 按来源克隆、接管或新建仓库，然后初始化 .autocoder/ 和集成分支。
// 返回的 warnings 是不影响结果的问题（例如创建远程仓库失败）。
func (s *Store) Create(ctx context.Context, req CreateRequest, gh Runner) (*Project, []string, error) {
	path := ExpandHome(strings.TrimSpace(req.Path))
	if path == "" {
		return nil, nil, errors.New("请填写目录")
	}
	if !filepath.IsAbs(path) {
		return nil, nil, errors.New("目录必须是绝对路径或以 ~ 开头")
	}
	path = filepath.Clean(path)
	var warnings []string

	switch req.Source {
	case "github", "url":
		if _, err := os.Stat(path); err == nil {
			if !gitops.IsRepo(ctx, path) {
				return nil, nil, fmt.Errorf("目录 %s 已存在且不是 git 仓库", path)
			}
			warnings = append(warnings, "目录已存在，直接接管已有的仓库")
		} else if req.Source == "github" {
			if !regexp.MustCompile(`^[\w.-]+/[\w.-]+$`).MatchString(req.Repo) {
				return nil, nil, errors.New("仓库应为 owner/name 格式")
			}
			if out, err := gh(ctx, filepath.Dir(path), "gh", "repo", "clone", req.Repo, path); err != nil {
				return nil, nil, fmt.Errorf("克隆失败：%s", strings.TrimSpace(out+" "+err.Error()))
			}
		} else {
			if req.URL == "" || strings.HasPrefix(req.URL, "-") {
				return nil, nil, errors.New("请填写仓库地址")
			}
			if err := gitops.Clone(ctx, req.URL, path); err != nil {
				return nil, nil, fmt.Errorf("克隆失败：%w", err)
			}
		}
	case "local":
		if !gitops.IsRepo(ctx, path) {
			return nil, nil, fmt.Errorf("%s 不是 git 仓库", path)
		}
		top, err := gitops.TopLevel(ctx, path)
		if err == nil {
			path = filepath.Clean(top)
		}
	case "new":
		if !nameRe.MatchString(req.Name) {
			return nil, nil, errors.New("项目名称只能包含字母、数字、点、下划线和连字符")
		}
		if entries, err := os.ReadDir(path); err == nil && len(entries) > 0 {
			return nil, nil, fmt.Errorf("目录 %s 已存在且不为空", path)
		}
		branch := req.BaseBranch
		if branch == "" {
			branch = "main"
		}
		if err := gitops.Init(ctx, path, branch, req.Name); err != nil {
			return nil, nil, err
		}
		if req.CreateRemote {
			out, err := gh(ctx, path, "gh", "repo", "create", req.Name, "--private", "--source", path, "--remote", "origin", "--push")
			if err != nil {
				warnings = append(warnings, "创建远程仓库失败："+strings.TrimSpace(out+" "+err.Error()))
			}
		}
	default:
		return nil, nil, errors.New("未知的项目来源")
	}

	if !gitops.HasCommits(ctx, path) {
		return nil, nil, errors.New("仓库还没有任何提交，请先提交一次再添加")
	}
	id := req.Name
	if id == "" || req.Source != "new" {
		id = filepath.Base(path)
	}
	if !nameRe.MatchString(id) {
		id = regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(id, "-")
	}
	base := req.BaseBranch
	if base == "" || !gitops.BranchExists(ctx, path, base) {
		if cur := gitops.CurrentBranch(ctx, path); cur != "" && !strings.HasPrefix(cur, "ac/") {
			base = cur
		} else {
			base = "main"
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.projects {
		if p.ID == id {
			return nil, nil, fmt.Errorf("已经有名为 %s 的项目", id)
		}
		if p.Path == path {
			return nil, nil, fmt.Errorf("这个仓库已经添加过了（项目 %s）", p.ID)
		}
	}
	remote, _ := gitops.Run(ctx, path, "remote", "get-url", "origin")
	p := &Project{Path: path, Meta: Meta{
		ID: id, Name: id, Description: req.Description, BaseBranch: base, IntegrationBranch: IntegrationBranch,
		Remote: remote, TestCommand: req.TestCommand, CreatedAt: time.Now(),
	}}
	if err := initState(ctx, p); err != nil {
		return nil, nil, err
	}
	s.projects = append(s.projects, p)
	if err := s.saveRegistry(); err != nil {
		return nil, nil, err
	}
	return p, warnings, nil
}

// initState 创建集成分支与集成 worktree；已有 .autocoder/ 时直接读取（例如换了台机器）。
func initState(ctx context.Context, p *Project) error {
	from := p.BaseBranch
	if !gitops.BranchExists(ctx, p.Path, IntegrationBranch) && gitops.RefExists(ctx, p.Path, "origin/"+IntegrationBranch) {
		from = "origin/" + IntegrationBranch
	}
	if err := gitops.CreateBranch(ctx, p.Path, IntegrationBranch, from); err != nil {
		return err
	}
	if err := gitops.EnsureWorktree(ctx, p.Path, p.ControlDir(), IntegrationBranch, p.BaseBranch); err != nil {
		return err
	}
	if existing, err := readMeta(p.stateDir()); err == nil {
		id := p.ID
		p.Meta = existing
		p.ID = existingOr(id, filepath.Base(p.Path))
		return loadState(p)
	}
	if err := saveMeta(p); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(p.stateDir(), ".gitignore"), []byte("# 计量明细只留在本机\nusage/\n.tmp-*\n")); err != nil {
		return err
	}
	_, err := gitops.CommitAll(ctx, p.ControlDir(), "chore(autocoder): 接管项目 "+p.ID, ".autocoder")
	return err
}

func existingOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func load(ctx context.Context, id, path string) (*Project, error) {
	p := &Project{Path: path, Meta: Meta{ID: id}}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("仓库目录 %s 不存在", path)
	}
	if _, err := os.Stat(p.ControlDir()); err != nil {
		// 集成 worktree 被删掉时重建
		if err := gitops.EnsureWorktree(ctx, path, p.ControlDir(), IntegrationBranch, "HEAD"); err != nil {
			return nil, err
		}
	}
	m, err := readMeta(p.stateDir())
	if err != nil {
		return nil, err
	}
	p.Meta = m
	p.ID = id
	return p, loadState(p)
}

func readMeta(dir string) (Meta, error) {
	var m Meta
	data, err := os.ReadFile(filepath.Join(dir, "project.yaml"))
	if err != nil {
		return m, err
	}
	if err := yaml.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("project.yaml 解析失败：%w", err)
	}
	if m.IntegrationBranch == "" {
		m.IntegrationBranch = IntegrationBranch
	}
	return m, nil
}

func loadState(p *Project) error {
	p.Tasks = nil
	p.Reports = nil
	files, _ := filepath.Glob(filepath.Join(p.stateDir(), "tasks", "*.yaml"))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		var t Task
		if err := yaml.Unmarshal(data, &t); err != nil {
			return fmt.Errorf("%s 解析失败：%w", filepath.Base(f), err)
		}
		p.Tasks = append(p.Tasks, &t)
	}
	sortTasks(p.Tasks)
	p.Requests = nil
	reqs, _ := filepath.Glob(filepath.Join(p.stateDir(), "requests", "*.yaml"))
	for _, f := range reqs {
		data, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		var r Request
		if err := yaml.Unmarshal(data, &r); err != nil {
			return fmt.Errorf("%s 解析失败：%w", filepath.Base(f), err)
		}
		p.Requests = append(p.Requests, &r)
	}
	sort.SliceStable(p.Requests, func(i, j int) bool { return p.Requests[i].CreatedAt.Before(p.Requests[j].CreatedAt) })
	p.Spent = 0
	for _, u := range ReadUsage(p) {
		p.Spent += u.Usage.Total()
	}
	reports, _ := filepath.Glob(filepath.Join(p.stateDir(), "reports", "*.md"))
	for _, f := range reports {
		rs, err := readReports(f)
		if err != nil {
			return err
		}
		p.Reports = append(p.Reports, rs...)
	}
	sort.Slice(p.Reports, func(i, j int) bool { return p.Reports[i].At.After(p.Reports[j].At) })
	return nil
}

var taskNum = regexp.MustCompile(`\d+`)

func sortTasks(ts []*Task) {
	sort.SliceStable(ts, func(i, j int) bool {
		a, b := taskNum.FindString(ts[i].ID), taskNum.FindString(ts[j].ID)
		if len(a) != len(b) {
			return len(a) < len(b)
		}
		if a != b {
			return a < b
		}
		return ts[i].ID < ts[j].ID
	})
}

// Remove 取消登记（不删除仓库、分支和 worktree）。
func (s *Store) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, p := range s.projects {
		if p.ID == id {
			s.projects = append(s.projects[:i], s.projects[i+1:]...)
			return s.saveRegistry()
		}
	}
	return errors.New("项目不存在")
}

// ---------- 状态文件 ----------

func saveMeta(p *Project) error {
	data, err := yaml.Marshal(p.Meta)
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(p.stateDir(), "project.yaml"), append([]byte("# AutoCoder 项目设置，由界面维护\n"), data...))
}

// SaveMeta 写入 project.yaml（调用方持有锁）。
func SaveMeta(p *Project) error { return saveMeta(p) }

// SaveTask 写入任务文件（调用方持有锁）。
func SaveTask(p *Project, t *Task) error {
	t.UpdatedAt = time.Now()
	data, err := yaml.Marshal(t)
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(p.stateDir(), "tasks", t.ID+".yaml"), data)
}

// Flush 把内存中的全部任务重新写盘（集成分支回退后使用）。
func Flush(p *Project) error {
	if err := saveMeta(p); err != nil {
		return err
	}
	for _, t := range p.Tasks {
		data, err := yaml.Marshal(t)
		if err != nil {
			return err
		}
		if err := writeFile(filepath.Join(p.stateDir(), "tasks", t.ID+".yaml"), data); err != nil {
			return err
		}
	}
	byTask := map[string][]Report{}
	for _, r := range p.Reports {
		byTask[r.TaskID] = append(byTask[r.TaskID], r)
	}
	for id, rs := range byTask {
		sort.Slice(rs, func(i, j int) bool { return rs[i].At.Before(rs[j].At) })
		var b strings.Builder
		b.WriteString("# " + id + " 汇报记录\n")
		for _, r := range rs {
			b.WriteString(formatReport(r))
		}
		if err := writeFile(filepath.Join(p.stateDir(), "reports", id+".md"), []byte(b.String())); err != nil {
			return err
		}
	}
	return nil
}

// NewTask 校验并加入任务（调用方持有锁）。
func NewTask(p *Project, t Task) (*Task, error) {
	t.Title = strings.TrimSpace(t.Title)
	if t.Title == "" {
		return nil, errors.New("任务标题不能为空")
	}
	if t.ID == "" {
		max := 0
		for _, x := range p.Tasks {
			var n int
			fmt.Sscanf(strings.TrimPrefix(x.ID, "T"), "%d", &n)
			if n > max {
				max = n
			}
		}
		t.ID = fmt.Sprintf("T%d", max+1)
	}
	if !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]{0,19}$`).MatchString(t.ID) || p.Task(t.ID) != nil {
		return nil, fmt.Errorf("任务编号 %q 无效或已存在", t.ID)
	}
	if t.Kind == "" {
		t.Kind = "feature"
	}
	if t.Role == "" {
		t.Role = "developer"
	}
	for _, d := range t.DependsOn {
		if p.Task(d) == nil {
			return nil, fmt.Errorf("依赖的任务 %s 不存在", d)
		}
	}
	if t.Branch == "" {
		slug := Slug(t.Title)
		if slug == "task" {
			slug = t.Kind // 中文标题没有可用的英文名，用任务类型
		}
		t.Branch = "ac/" + t.ID + "-" + slug
	}
	t.Status = Planned
	t.Attempts, t.Commits = 0, 0
	nt := &t
	p.Tasks = append(p.Tasks, nt)
	if hasCycle(p.Tasks) {
		p.Tasks = p.Tasks[:len(p.Tasks)-1]
		return nil, errors.New("任务依赖出现了环")
	}
	return nt, SaveTask(p, nt)
}

func hasCycle(ts []*Task) bool {
	by := map[string]*Task{}
	for _, t := range ts {
		by[t.ID] = t
	}
	state := map[string]int{}
	var visit func(string) bool
	visit = func(id string) bool {
		switch state[id] {
		case 1:
			return true
		case 2:
			return false
		}
		state[id] = 1
		if t := by[id]; t != nil {
			for _, d := range t.DependsOn {
				if visit(d) {
					return true
				}
			}
		}
		state[id] = 2
		return false
	}
	for _, t := range ts {
		if visit(t.ID) {
			return true
		}
	}
	return false
}

// Slug 把标题转成分支名后缀；中文标题退回 task。
func Slug(title string) string {
	s := strings.ToLower(title)
	s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 30 {
		s = strings.Trim(s[:30], "-")
	}
	if s == "" {
		return "task"
	}
	return s
}

// AppendReport 追加一条汇报（调用方持有锁）。
func AppendReport(p *Project, r Report) error {
	if r.ID == "" {
		r.ID = fmt.Sprintf("%s-%d", r.TaskID, time.Now().UnixNano())
	}
	p.Reports = append([]Report{r}, p.Reports...)
	path := filepath.Join(p.stateDir(), "reports", r.TaskID+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	head := ""
	if _, err := os.Stat(path); os.IsNotExist(err) {
		head = "# " + r.TaskID + " 汇报记录\n"
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(head + formatReport(r))
	return err
}

const reportMark = "<!-- report "

// formatReport 输出给人看的 Markdown，并在注释里带上机器可读的 JSON。
func formatReport(r Report) string {
	data, _ := json.Marshal(r)
	var b strings.Builder
	fmt.Fprintf(&b, "\n%s%s -->\n", reportMark, data)
	fmt.Fprintf(&b, "## %s · %s", r.At.Format("2006-01-02 15:04"), r.From)
	if r.Attempt > 0 {
		fmt.Fprintf(&b, " · 第 %d 次尝试", r.Attempt)
	}
	fmt.Fprintf(&b, " · %s\n\n%s\n", r.Status, strings.TrimSpace(r.Summary))
	var facts []string
	if r.Files > 0 {
		facts = append(facts, fmt.Sprintf("改动 %d 个文件（+%d −%d）", r.Files, r.Added, r.Removed))
	}
	if r.Verify != "" {
		facts = append(facts, "验证："+r.Verify)
	}
	if r.Tokens > 0 {
		facts = append(facts, fmt.Sprintf("token：%d", r.Tokens))
	}
	if r.Commit != "" {
		facts = append(facts, "提交："+r.Commit)
	}
	if len(facts) > 0 {
		b.WriteString("\n- " + strings.Join(facts, "\n- ") + "\n")
	}
	return b.String()
}

func readReports(path string) ([]Report, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Report
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, reportMark) {
			continue
		}
		var r Report
		if json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(line, reportMark), " -->")), &r) == nil {
			out = append(out, r)
		}
	}
	return out, sc.Err()
}

// AppendUsage 追加一条计量记录到 .autocoder/usage/<日期>.jsonl（不提交）。
func AppendUsage(p *Project, u Usage) error {
	path := filepath.Join(p.stateDir(), "usage", u.At.Format("2006-01-02")+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, _ := json.Marshal(u)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.Write(append(data, '\n')); err == nil {
		p.Spent += u.Usage.Total()
	}
	return err
}

// ReadUsage 读取该项目的全部计量记录。
func ReadUsage(p *Project) []Usage { return readUsage(p, "*") }

// ReadUsageOn 只读某一天（本地日期）的计量记录。
func ReadUsageOn(p *Project, day time.Time) []Usage {
	return readUsage(p, day.Format("2006-01-02"))
}

func readUsage(p *Project, name string) []Usage {
	files, _ := filepath.Glob(filepath.Join(p.stateDir(), "usage", name+".jsonl"))
	sort.Strings(files)
	var out []Usage
	for _, path := range files {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			var u Usage
			if json.Unmarshal(sc.Bytes(), &u) == nil {
				out = append(out, u)
			}
		}
		f.Close()
	}
	return out
}

// CommitState 把 .autocoder/ 的改动提交到集成分支（调用方持有 p.GitMu）。
func CommitState(ctx context.Context, p *Project, msg string) error {
	_, err := gitops.CommitAll(ctx, p.ControlDir(), "chore(autocoder): "+msg, ".autocoder")
	return err
}

// ---------- 对外视图 ----------

// TaskView 是界面使用的任务结构。
type TaskView struct {
	Task
	Progress int `json:"progress"`
}

// View 是界面使用的项目结构。
type View struct {
	Meta
	Path      string     `json:"path"`
	UpdatedAt time.Time  `json:"updatedAt"`
	Tasks     []TaskView `json:"tasks"`
	Requests  []Request  `json:"requests"`
	Reports   []Report   `json:"reports"`
	Spent     int64      `json:"spent"`
}

// 各阶段对应的进度条位置（只表示阶段，不是工作量估计）。
var stageProgress = map[string]int{
	Planned: 0, Ready: 5, Running: 35, Testing: 65, Review: 80, Integrating: 90, Merged: 100,
	Blocked: 50, WaitingUser: 60, Cancelled: 0,
}

// ToView 生成项目视图（调用方持有锁）。
func ToView(p *Project) View {
	v := View{Meta: p.Meta, Path: p.Path, UpdatedAt: p.CreatedAt, Tasks: []TaskView{}, Requests: []Request{}, Reports: []Report{}, Spent: p.Spent}
	for i := len(p.Requests) - 1; i >= 0; i-- { // 最新的在前
		v.Requests = append(v.Requests, *p.Requests[i])
		if p.Requests[i].UpdatedAt.After(v.UpdatedAt) {
			v.UpdatedAt = p.Requests[i].UpdatedAt
		}
	}
	for _, t := range p.Tasks {
		v.Tasks = append(v.Tasks, TaskView{Task: *t, Progress: stageProgress[t.Status]})
		if t.UpdatedAt.After(v.UpdatedAt) {
			v.UpdatedAt = t.UpdatedAt
		}
	}
	n := min(len(p.Reports), 200)
	v.Reports = append(v.Reports, p.Reports[:n]...)
	return v
}

// ---------- 工具函数 ----------

// ExpandHome 展开 ~。
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}

// writeFile 原子写入。
func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
