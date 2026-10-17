package dispatch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luckySpro/AutoCoder/internal/project"
	"github.com/luckySpro/AutoCoder/internal/roles"
	"github.com/luckySpro/AutoCoder/internal/settings"
	"github.com/luckySpro/AutoCoder/internal/toolscan"
)

// answer 写一个假工具的最终答复（Claude stream-json 的 result 事件），返回文件路径。
func answer(t *testing.T, env, text string) {
	t.Helper()
	line, _ := json.Marshal(map[string]any{"type": "result", "subtype": "success", "result": text,
		"usage": map[string]int{"input_tokens": 500, "output_tokens": 100}})
	path := filepath.Join(t.TempDir(), env)
	if err := os.WriteFile(path, append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(env, path)
}

func (e *env) settings(t *testing.T, f func(s *settings.Settings)) {
	t.Helper()
	s := e.eng.Settings.Get()
	f(&s)
	if _, err := e.eng.Settings.Put(s); err != nil {
		t.Fatal(err)
	}
}

func (e *env) waitRequest(t *testing.T, id string, want ...string) project.Request {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		e.eng.Schedule(e.ctx)
		e.store.Lock()
		r := *e.p.Request(id)
		e.store.Unlock()
		for _, w := range want {
			if r.Status == w && !e.eng.activeRun(e.p.ID+"/"+id) {
				return r
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	e.store.Lock()
	defer e.store.Unlock()
	r := e.p.Request(id)
	t.Fatalf("需求 %s 状态 %s，期望 %v；errors=%v error=%s", id, r.Status, want, r.Errors, r.Error)
	return project.Request{}
}

const goodPlan = "我看过仓库了，计划如下：\n```json\n" + `{
  "summary": "两个任务都写 src/，改为先后执行",
  "test_command": "test -f src/hello.txt",
  "tasks": [
    {"id": "P1", "title": "写 hello", "role": "developer", "scope": ["src/**"], "acceptance": ["有 hello 文件"], "verify": ["grep -q hello src/hello.txt"]},
    {"id": "P2", "title": "再写一遍", "role": "developer", "scope": ["src/hello.txt"], "acceptance": ["内容是 T2"], "verify": ["grep -q T2 src/hello.txt"]}
  ]
}` + "\n```"

func TestPlanReviewDeliver(t *testing.T) {
	answer(t, "FAKE_PLAN", goodPlan)
	answer(t, "FAKE_REVIEW", "```json\n{\"verdict\":\"pass\",\"summary\":\"满足验收标准\",\"issues\":[]}\n```")
	e := setup(t, "")
	e.settings(t, func(s *settings.Settings) { s.Review = true; s.ConfirmPlan = true })

	req, err := e.eng.NewRequest("demo", "写一个 hello 文件")
	if err != nil {
		t.Fatal(err)
	}
	r := e.waitRequest(t, req.ID, project.ReqProposed)
	if r.Plan == nil || len(r.Plan.Tasks) != 2 {
		t.Fatalf("计划不对：%+v", r.Plan)
	}
	// 范围重叠的两个任务被自动改为先后执行
	if !strings.Contains(strings.Join(r.Notes, ""), "重叠") || len(r.Plan.Tasks[1].DependsOn) != 1 {
		t.Fatalf("应自动串行化：notes=%v deps=%v", r.Notes, r.Plan.Tasks[1].DependsOn)
	}
	if _, err := os.Stat(filepath.Join(e.p.ControlDir(), ".autocoder", "plans", "R1.md")); err != nil {
		t.Fatal("应写出给人看的计划")
	}
	// 确认前不派发
	e.store.Lock()
	if len(e.p.Tasks) != 0 {
		t.Fatal("确认前不应生成任务")
	}
	e.store.Unlock()
	if err := e.eng.ApprovePlan("demo", req.ID); err != nil {
		t.Fatal(err)
	}
	e.store.Lock()
	if len(e.p.Tasks) != 2 || e.p.Task("T2").DependsOn[0] != "T1" || e.p.Task("T1").Request != "R1" || e.p.TestCommand != "test -f src/hello.txt" {
		t.Fatalf("任务没按计划生成：%+v", e.p.Tasks)
	}
	e.store.Unlock()

	e.waitStatus(t, "T2", project.Merged)
	e.store.Lock()
	reviews := 0
	for _, rp := range e.p.Reports {
		if rp.Review == "pass" {
			reviews++
		}
	}
	e.store.Unlock()
	if reviews != 2 {
		t.Fatalf("两个任务都应经过评审，实际 %d", reviews)
	}
	// 规划与评审的 token 都计入
	roles := map[string]bool{}
	for _, u := range project.ReadUsage(e.p) {
		roles[u.Role] = true
	}
	if !roles["lead"] || !roles["reviewer"] || !roles["developer"] {
		t.Fatalf("计量应包含规划和评审：%v", roles)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		data, _ := os.ReadFile(filepath.Join(e.p.Path, "src", "hello.txt"))
		if strings.Contains(string(data), "T2") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("应自动交付到主分支")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestReviewRequestsChanges(t *testing.T) {
	answer(t, "FAKE_REVIEW", `{"verdict":"changes","summary":"缺少错误处理","issues":["src/hello.txt 需要以换行结尾"]}`)
	e := setup(t, "")
	e.settings(t, func(s *settings.Settings) { s.Review = true })
	id := e.addTask(t, project.Task{Title: "写 hello", Scope: []string{"src/**"}, Acceptance: []string{"有文件"}, Verify: []string{"true"}})
	// 第一次评审打回：任务带着问题回到队列；把评审结论换成通过，第二次尝试后合并
	deadline := time.Now().Add(20 * time.Second)
	for {
		e.eng.Schedule(e.ctx)
		e.store.Lock()
		err := e.p.Task(id).LastError
		e.store.Unlock()
		if strings.Contains(err, "以换行结尾") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("评审意见应成为下一次尝试的输入")
		}
		time.Sleep(50 * time.Millisecond)
	}
	answer(t, "FAKE_REVIEW", `{"verdict":"pass","summary":"好了","issues":[]}`)
	task := e.waitStatus(t, id, project.Merged)
	if task.Attempts != 2 {
		t.Fatalf("attempts=%d", task.Attempts)
	}
}

func TestPlanInvalidThenFails(t *testing.T) {
	answer(t, "FAKE_PLAN", `{"tasks":[{"id":"P1","title":"没有验收标准","scope":["a/**"]},{"id":"P2","title":"依赖不存在","scope":["b/**"],"acceptance":["x"],"verify":["true"],"depends_on":["P9"]}]}`)
	e := setup(t, "")
	req, _ := e.eng.NewRequest("demo", "随便做点什么")
	r := e.waitRequest(t, req.ID, project.ReqFailed)
	if r.Attempts != maxPlanAttempts || len(r.Errors) < 2 {
		t.Fatalf("attempts=%d errors=%v", r.Attempts, r.Errors)
	}
}

func TestPlanQuestionsThenAnswer(t *testing.T) {
	answer(t, "FAKE_PLAN", `{"questions":["用 MySQL 还是 SQLite？"],"tasks":[]}`)
	e := setup(t, "go version")
	req, _ := e.eng.NewRequest("demo", "加一个数据库")
	r := e.waitRequest(t, req.ID, project.ReqQuestions)
	if len(r.Questions) != 1 {
		t.Fatalf("%+v", r)
	}
	if err := e.eng.Answer("demo", req.ID, "SQLite"); err != nil {
		t.Fatal(err)
	}
	answer(t, "FAKE_PLAN", `{"tasks":[{"title":"接入 SQLite","acceptance":["能建表"]}]}`)
	r = e.waitRequest(t, req.ID, project.ReqProposed)
	if len(r.QA) != 1 || !strings.Contains(r.QA[0], "SQLite") || r.Plan.Tasks[0].ID != "P1" || r.Plan.Tasks[0].Role != "developer" {
		t.Fatalf("%+v", r)
	}
}

func TestValidatePlan(t *testing.T) {
	p := &project.Project{}
	p.Tasks = []*project.Task{{ID: "T1", Title: "已有"}}
	pl := &project.Plan{TestCommand: "make test", Tasks: []project.PlanTask{
		{ID: "P1", Title: "a", Scope: []string{"internal/order/**"}, Acceptance: []string{"x"}},
		{ID: "P2", Title: "b", Scope: []string{"internal/user/**"}, Acceptance: []string{"x"}, DependsOn: []string{"T1"}},
		{ID: "P3", Title: "c", Scope: []string{"internal/order/state.go"}, Acceptance: []string{"x"}},
		{ID: "P4", Title: "d", Scope: []string{"internal/order/**"}, Acceptance: []string{"x"}, DependsOn: []string{"P1"}},
	}}
	errs, notes := validatePlan(p, pl, nil)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	// P3 与 P1 重叠 → P3 依赖 P1；P4 已依赖 P1，但与 P3 可并行且重叠 → P4 依赖 P3
	if len(notes) != 2 || pl.Tasks[2].DependsOn[0] != "P1" || len(pl.Tasks[3].DependsOn) != 2 {
		t.Fatalf("notes=%v tasks=%+v", notes, pl.Tasks)
	}
	bad := &project.Plan{Tasks: []project.PlanTask{{ID: "T1", Title: "撞号", Acceptance: []string{"x"}, Verify: []string{"true"}}}}
	if errs, _ := validatePlan(p, bad, nil); len(errs) == 0 || !strings.Contains(errs[0], "已有") {
		t.Fatalf("应拒绝与已有任务撞号：%v", errs)
	}
	cyc := &project.Plan{TestCommand: "x", Tasks: []project.PlanTask{
		{ID: "P1", Title: "a", Scope: []string{"a"}, Acceptance: []string{"x"}, DependsOn: []string{"P2"}},
		{ID: "P2", Title: "b", Scope: []string{"b"}, Acceptance: []string{"x"}, DependsOn: []string{"P1"}},
	}}
	if errs, _ := validatePlan(p, cyc, nil); len(errs) == 0 || !strings.Contains(errs[0], "环") {
		t.Fatalf("应发现依赖环：%v", errs)
	}
}

func TestBudgetStopsDispatch(t *testing.T) {
	e := setup(t, "")
	e.store.Lock()
	e.p.TokenBudget, e.p.Spent = 1000, 1000
	e.store.Unlock()
	id := e.addTask(t, project.Task{Title: "超预算"})
	for range 5 {
		e.eng.Schedule(e.ctx)
		time.Sleep(20 * time.Millisecond)
	}
	e.store.Lock()
	st := e.p.Task(id).Status
	e.store.Unlock()
	if st != project.Ready {
		t.Fatalf("预算用完后不应派发：%s", st)
	}
}

func TestConflictTwiceGoesToUser(t *testing.T) {
	e := setup(t, "")
	tk := &project.Task{ID: "T1", Branch: "ac/T1-x"}
	s := e.eng.Settings.Get()
	e.eng.conflictFailed(tk, s, "冲突", []string{"a.go"})
	if tk.Status != project.Ready {
		t.Fatalf("第一次冲突应重试：%s", tk.Status)
	}
	e.eng.conflictFailed(tk, s, "冲突", []string{"a.go"})
	if tk.Status != project.WaitingUser || !strings.Contains(tk.Waiting, "a.go") {
		t.Fatalf("连续两次冲突应转给你：%+v", tk)
	}
}

func TestParseVerdict(t *testing.T) {
	if v, ok := parseVerdict("看完了。\n```json\n{\"verdict\":\"通过\"}\n```"); !ok || v.Verdict != "pass" {
		t.Fatal(v, ok)
	}
	if _, ok := parseVerdict(`{"verdict":"changes","issues":[]}`); ok {
		t.Fatal("打回却没有问题清单，不能算有效结论")
	}
	if _, ok := parseVerdict("看起来不错"); ok {
		t.Fatal("没有 JSON 不能算结论")
	}
}

func TestChooseReviewerWaitsForOthers(t *testing.T) {
	mk := func(id string) toolscan.Detection {
		return toolscan.Detection{Tool: toolscan.Tool{ID: id, Headless: []string{"{prompt}"}}, Installed: true, Path: "/bin/" + id}
	}
	tools := []toolscan.Detection{mk("a"), mk("b")}
	assigns := []roles.Assignment{{ToolID: "a", Roles: []roles.Role{roles.Lead, roles.Reviewer}}, {ToolID: "b", Roles: []roles.Role{roles.Developer}}}
	if d, ok := chooseReviewer(tools, assigns, map[string]bool{}, map[string]bool{}, "b"); !ok || d.ID != "a" {
		t.Fatal("应交给不是作者的评审", d.ID, ok)
	}
	if _, ok := chooseReviewer(tools, assigns, map[string]bool{"a": true}, map[string]bool{}, "b"); ok {
		t.Fatal("评审工具只是在忙，应等它，而不是让作者自审")
	}
	if d, ok := chooseReviewer(tools, assigns, map[string]bool{}, map[string]bool{"a": true}, "b"); !ok || d.ID != "b" {
		t.Fatal("别的工具都不可用时由作者自审", d.ID, ok)
	}
	if d, ok := chooseReviewer(tools, assigns, map[string]bool{}, map[string]bool{}, "a"); !ok || d.ID != "a" {
		t.Fatal("只有一个能评审的工具时自审", d.ID, ok)
	}
}
