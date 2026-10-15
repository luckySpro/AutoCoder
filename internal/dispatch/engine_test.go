package dispatch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/luckySpro/AutoCoder/internal/gitops"
	"github.com/luckySpro/AutoCoder/internal/project"
	"github.com/luckySpro/AutoCoder/internal/roles"
	"github.com/luckySpro/AutoCoder/internal/settings"
	"github.com/luckySpro/AutoCoder/internal/toolscan"
)

// 假的 CLI 工具：模仿 Claude Code 的 stream-json 输出，按环境变量决定行为。
const fakeTool = `#!/bin/sh
case "$FAKE_MODE" in
  noop)  echo '{"type":"result","result":"我需要更多信息，要用哪个数据库？","usage":{"input_tokens":10,"output_tokens":5}}'; exit 0;;
  crash) echo "boom" >&2; exit 3;;
  auth)  echo "Error: not logged in" >&2; exit 1;;
  scope) echo "x" > outside.txt;;
esac
echo '{"type":"system","subtype":"init","model":"fake-1"}'
echo '{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"开始写 hello"},{"type":"tool_use","name":"Write","input":{"file_path":"src/hello.txt"}}],"usage":{"input_tokens":100,"output_tokens":20}}}'
mkdir -p src && echo "hello $AUTOCODER_TASK" > src/hello.txt
echo '{"type":"result","subtype":"success","result":"写好了 src/hello.txt","usage":{"input_tokens":1200,"output_tokens":300,"cache_read_input_tokens":5000,"cache_creation_input_tokens":40}}'
`

type env struct {
	ctx   context.Context
	store *project.Store
	eng   *Engine
	p     *project.Project
	home  string
}

func setup(t *testing.T, testCmd string) *env {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("假工具是 sh 脚本")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("需要 git")
	}
	ctx := context.Background()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	script := filepath.Join(root, "fake-claude")
	if err := os.WriteFile(script, []byte(fakeTool), 0o755); err != nil {
		t.Fatal(err)
	}
	store, errs := project.Open(ctx, home)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	p, _, err := store.Create(ctx, project.CreateRequest{Source: "new", Name: "demo", Path: filepath.Join(root, "code", "demo"), BaseBranch: "main", TestCommand: testCmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tool := toolscan.Detection{
		Tool: toolscan.Tool{ID: "fake", Name: "Fake", Commands: []string{"fake-claude"}, Headless: []string{"-p", "{prompt}"},
			StreamArgs: []string{"--output-format", "stream-json"}, AuthErrors: []string{"not logged in"}, Capability: 90},
		Installed: true, Path: script,
	}
	sf := &settings.File{Path: filepath.Join(home, "settings.json")}
	s := settings.Default()
	s.Assignments = []roles.Assignment{{ToolID: "fake", Roles: []roles.Role{roles.Lead, roles.Developer}}}
	if _, err := sf.Put(s); err != nil {
		t.Fatal(err)
	}
	eng := &Engine{Store: store, Settings: sf, LogDir: filepath.Join(home, "logs"), Tick: 50 * time.Millisecond,
		Tools: func(context.Context) []toolscan.Detection { return []toolscan.Detection{tool} }}
	return &env{ctx, store, eng, p, home}
}

func (e *env) addTask(t *testing.T, task project.Task) string {
	t.Helper()
	e.store.Lock()
	defer e.store.Unlock()
	nt, err := project.NewTask(e.p, task)
	if err != nil {
		t.Fatal(err)
	}
	return nt.ID
}

func (e *env) waitStatus(t *testing.T, id string, want ...string) *project.Task {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		e.eng.Schedule(e.ctx)
		e.store.Lock()
		task := *e.p.Task(id)
		e.store.Unlock()
		for _, w := range want {
			if task.Status == w {
				if r := e.eng.Run(e.p.ID + "/" + id); r == nil || !r.Active() {
					return &task
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	e.store.Lock()
	defer e.store.Unlock()
	t.Fatalf("任务 %s 状态 %s，期望 %v；最后错误：%s", id, e.p.Task(id).Status, want, e.p.Task(id).LastError)
	return nil
}

func TestHappyPathToDelivery(t *testing.T) {
	e := setup(t, "test -f src/hello.txt")
	id := e.addTask(t, project.Task{Title: "写 hello", Scope: []string{"src/**"}, Verify: []string{"grep -q hello src/hello.txt"}, Acceptance: []string{"有 hello 文件"}})
	task := e.waitStatus(t, id, project.Merged)
	if task.Attempts != 1 || task.Commits != 1 {
		t.Errorf("attempts=%d commits=%d", task.Attempts, task.Commits)
	}
	ctl := e.p.ControlDir()
	if data, err := os.ReadFile(filepath.Join(ctl, "src", "hello.txt")); err != nil || !strings.Contains(string(data), "hello T1") {
		t.Fatalf("集成分支上应有任务产出：%q %v", data, err)
	}
	// 提交由编排器完成，带 trailer
	msg, _ := gitops.Run(e.ctx, e.p.Path, "log", "-1", "--format=%B", task.Branch)
	for _, want := range []string{"AutoCoder-Task: T1", "AutoCoder-Tool: fake", "AutoCoder-Verify: pass 2/2", "AutoCoder-Tokens: 6540"} {
		if !strings.Contains(msg, want) {
			t.Errorf("提交说明缺少 %q：\n%s", want, msg)
		}
	}
	// 计量：取 result 事件里的精确用量
	us := project.ReadUsage(e.p)
	if len(us) != 1 || us[0].Estimated || us[0].Usage.Total() != 6540 || us[0].Usage.CacheRead != 5000 {
		t.Fatalf("计量记录不对：%+v", us)
	}
	// 实时输出里有解析出的动作
	run := e.eng.Run("demo/T1")
	backlog, ch := run.Subscribe(0)
	run.Unsubscribe(ch)
	kinds := map[string]bool{}
	for _, ev := range backlog {
		if ev.Type == "act" {
			kinds[ev.Kind] = true
		}
	}
	for _, k := range []string{"say", "edit", "run", "usage"} {
		if !kinds[k] {
			t.Errorf("动作中缺少 %s：%v", k, kinds)
		}
	}
	// 汇报写进了仓库
	e.store.Lock()
	reports := len(e.p.Reports)
	e.store.Unlock()
	if reports == 0 {
		t.Fatal("没有汇报记录")
	}
	// 开启自动合并且有测试命令：全部任务合并后自动交付到主分支（主目录检出 main 且干净，快进）
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(e.p.Path, "src", "hello.txt")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("主目录应快进到交付结果")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(e.p.Path, ".autocoder", "deliveries", "D1.md")); err != nil {
		t.Fatal("应有交付说明")
	}
	if !gitops.IsClean(e.ctx, e.p.Path) {
		t.Fatal("交付后主目录应保持干净")
	}
	if _, err := e.eng.Deliver(e.ctx, "demo"); err == nil {
		t.Fatal("没有新内容时不应再次交付")
	}
}

func TestNoChangeQuestionRetriesThenBlocks(t *testing.T) {
	t.Setenv("FAKE_MODE", "noop")
	e := setup(t, "")
	id := e.addTask(t, project.Task{Title: "问问题"})
	task := e.waitStatus(t, id, project.Blocked)
	if task.Attempts != 3 || !strings.Contains(task.LastError, "提问") {
		t.Fatalf("attempts=%d err=%q", task.Attempts, task.LastError)
	}
}

func TestScopeRevert(t *testing.T) {
	t.Setenv("FAKE_MODE", "scope")
	e := setup(t, "")
	id := e.addTask(t, project.Task{Title: "范围", Scope: []string{"src/"}})
	e.waitStatus(t, id, project.Merged)
	if _, err := os.Stat(filepath.Join(e.p.ControlDir(), "outside.txt")); !os.IsNotExist(err) {
		t.Fatal("范围外的文件应被撤回")
	}
	if _, err := os.Stat(filepath.Join(e.p.ControlDir(), "src", "hello.txt")); err != nil {
		t.Fatal("范围内的文件应保留")
	}
}

func TestAuthErrorWaitsForUser(t *testing.T) {
	t.Setenv("FAKE_MODE", "auth")
	e := setup(t, "")
	id := e.addTask(t, project.Task{Title: "登录"})
	task := e.waitStatus(t, id, project.WaitingUser)
	if !strings.Contains(task.Waiting, "未登录") || task.Attempts != 0 {
		t.Fatalf("%+v", task)
	}
	if st := e.eng.ToolState()["fake"]; st != "等待登录" {
		t.Fatalf("工具应暂停：%q", st)
	}
}

func TestDependenciesAndIntegrationTestFailure(t *testing.T) {
	// 项目测试要求出现 never.txt：合并后测试必定失败，集成分支应退回
	e := setup(t, "test -f never.txt")
	a := e.addTask(t, project.Task{Title: "a", Verify: []string{"true"}})
	b := e.addTask(t, project.Task{Title: "b", DependsOn: []string{a}})
	e.store.Lock()
	if e.p.Task(b).Status != project.Planned {
		t.Fatal("有依赖的任务应先是 planned")
	}
	e.store.Unlock()
	head, _ := gitops.RevParse(e.ctx, e.p.ControlDir(), "HEAD")
	e.waitStatus(t, a, project.Blocked)
	e.store.Lock()
	verifyFailed := false
	for _, r := range e.p.Reports {
		if r.TaskID == a && strings.HasPrefix(r.Verify, "fail 1/2") {
			verifyFailed = true
		}
	}
	e.store.Unlock()
	if !verifyFailed {
		t.Fatal("汇报中应记录验证失败")
	}
	if gitops.CountCommits(e.ctx, e.p.Path, head, project.IntegrationBranch) > 10 {
		t.Fatal("集成分支不应有任务合并")
	}
	e.store.Lock()
	defer e.store.Unlock()
	if e.p.Task(b).Status != project.Planned {
		t.Fatal("依赖没合并时 b 不应派发")
	}
}

func TestGlob(t *testing.T) {
	cases := []struct {
		p, path string
		want    bool
	}{
		{"internal/order/**", "internal/order/a/b.go", true},
		{"internal/order/**", "internal/orders/x.go", false},
		{"internal/server/order*.go", "internal/server/order_api.go", true},
		{"src/", "src/a.txt", true},
		{"src", "srcx/a.txt", false},
		{"**/package.json", "package.json", true},
		{"**/package.json", "web/package.json", true},
		{"*.md", "docs/a.md", false},
	}
	for _, c := range cases {
		if got := globMatch(c.p, c.path); got != c.want {
			t.Errorf("globMatch(%q, %q) = %v", c.p, c.path, got)
		}
	}
}

func TestMask(t *testing.T) {
	in := "key sk-ant-abcdefghijklmnopqrstuvwxyz token=abcdefghijklmnop ghp_abcdefghijklmnopqrstuvwxyz0123"
	out := mask(in)
	if strings.Contains(out, "abcdefghijklmnopqrstuvwxyz") || !strings.Contains(out, "token=***") {
		t.Fatal(out)
	}
}

func TestCodexEvents(t *testing.T) {
	p := newParser()
	feed := []string{
		`{"type":"thread.started","thread_id":"x"}`,
		`{"type":"item.started","item":{"id":"1","type":"command_execution","command":"go test ./...","status":"in_progress"}}`,
		`{"type":"item.completed","item":{"id":"1","type":"command_execution","command":"go test ./...","aggregated_output":"FAIL x","exit_code":1}}`,
		`{"type":"item.completed","item":{"id":"2","type":"file_change","changes":[{"path":"a.go","kind":"update"}]}}`,
		`{"type":"item.completed","item":{"id":"3","type":"agent_message","text":"完成"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":1000,"cached_input_tokens":600,"output_tokens":50}}`,
	}
	var acts []act
	for _, l := range feed {
		_, as := p.feed(l)
		acts = append(acts, as...)
	}
	if len(acts) != 3 || acts[0].tone != "bad" || acts[1].kind != "edit" || acts[2].kind != "say" {
		t.Fatalf("%+v", acts)
	}
	if p.final != "完成" || p.usage.Input != 400 || p.usage.CacheRead != 600 || p.usage.Output != 50 {
		t.Fatalf("final=%q usage=%+v", p.final, p.usage)
	}
}
