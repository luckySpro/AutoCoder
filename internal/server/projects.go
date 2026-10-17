package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/luckySpro/AutoCoder/internal/dispatch"
	"github.com/luckySpro/AutoCoder/internal/gitinfo"
	"github.com/luckySpro/AutoCoder/internal/project"
	"github.com/luckySpro/AutoCoder/internal/settings"
)

// projectRoutes 注册项目、任务、运行与计量相关的接口（DESIGN §6）。
func (s *Server) projectRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PUT /api/settings", s.putSettings)

	mux.HandleFunc("GET /api/projects", s.listProjects)
	mux.HandleFunc("POST /api/projects", s.createProject)
	mux.HandleFunc("PATCH /api/projects/{id}", s.patchProject)
	mux.HandleFunc("DELETE /api/projects/{id}", s.deleteProject)
	mux.HandleFunc("POST /api/projects/{id}/tasks", s.createTask)
	mux.HandleFunc("PATCH /api/projects/{id}/tasks/{task}", s.patchTask)
	mux.HandleFunc("POST /api/projects/{id}/tasks/{task}/{action}", s.taskAction)
	mux.HandleFunc("POST /api/projects/{id}/deliver", s.deliver)
	mux.HandleFunc("POST /api/projects/{id}/requests", s.createRequest)
	mux.HandleFunc("PUT /api/projects/{id}/requests/{req}/plan", s.putPlan)
	mux.HandleFunc("POST /api/projects/{id}/requests/{req}/{action}", s.requestAction)

	mux.HandleFunc("GET /api/runs", s.listRuns)
	mux.HandleFunc("GET /api/runs/{id}/{task}/stream", s.streamRun)
	mux.HandleFunc("GET /api/runs/{id}/{task}/log", s.runLog)
	mux.HandleFunc("POST /api/runs/{id}/{task}/stop", func(w http.ResponseWriter, r *http.Request) {
		if !s.Engine.Stop(r.PathValue("id") + "/" + r.PathValue("task")) {
			writeError(w, http.StatusConflict, errors.New("这个任务没有在运行"))
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	mux.HandleFunc("GET /api/usage", s.listUsage)
}

func decode(r *http.Request, v any) error {
	if err := json.NewDecoder(io.LimitReader(r.Body, 256<<10)).Decode(v); err != nil {
		return errors.New("请求格式错误")
	}
	return nil
}

// ---------- 设置 ----------

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	st := s.Settings.Get()
	tools, _, _ := s.Scan(r.Context(), false)
	writeJSON(w, http.StatusOK, map[string]any{
		"settings":    st,
		"assignments": dispatch.Assignments(st, tools),
		"max":         settings.MaxConcurrency,
	})
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var st settings.Settings
	if err := decode(r, &st); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if _, err := s.Settings.Put(st); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err)
		return
	}
	s.Engine.Wake()
	s.getSettings(w, r)
}

// ---------- 项目 ----------

type projectView struct {
	project.View
	Deliverable bool   `json:"deliverable"`
	DeliverNote string `json:"deliverNote,omitempty"`
	Tokens      int64  `json:"tokens"`
}

func (s *Server) views() []projectView {
	s.Projects.Lock()
	defer s.Projects.Unlock()
	out := []projectView{}
	for _, p := range s.Projects.Projects() {
		ok, why := dispatch.Deliverable(p)
		out = append(out, projectView{View: project.ToView(p), Deliverable: ok, DeliverNote: why})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"projects": s.views(), "errors": s.LoadErrors})
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var req project.CreateRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	gh := func(ctx context.Context, dir, name string, args ...string) (string, error) {
		return gitinfo.ExecRunner(ctx, name, append([]string{}, args...)...)
	}
	p, warnings, err := s.Projects.Create(ctx, req, gh)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err)
		return
	}
	s.Projects.Lock()
	v := project.ToView(p)
	s.Projects.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{"project": projectView{View: v}, "warnings": warnings})
}

func (s *Server) withProject(w http.ResponseWriter, r *http.Request, f func(p *project.Project) (int, any, error)) {
	s.Projects.Lock()
	p := s.Projects.Get(r.PathValue("id"))
	if p == nil {
		s.Projects.Unlock()
		writeError(w, http.StatusNotFound, errors.New("项目不存在"))
		return
	}
	code, v, err := f(p)
	s.Projects.Unlock()
	if err != nil {
		writeError(w, code, err)
		return
	}
	go s.commitState(p, "更新任务")
	s.Engine.Wake()
	writeJSON(w, code, v)
}

func (s *Server) commitState(p *project.Project, msg string) {
	p.GitMu.Lock()
	defer p.GitMu.Unlock()
	s.Projects.Lock()
	defer s.Projects.Unlock()
	_ = project.CommitState(context.Background(), p, msg)
}

func (s *Server) patchProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Description  *string   `json:"description"`
		TestCommand  *string   `json:"testCommand"`
		SetupCommand *string   `json:"setupCommand"`
		ScopeAllow   *[]string `json:"scopeAllow"`
		TokenBudget  *int64    `json:"tokenBudget"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.withProject(w, r, func(p *project.Project) (int, any, error) {
		if body.Description != nil {
			p.Description = strings.TrimSpace(*body.Description)
		}
		if body.TestCommand != nil {
			p.TestCommand = strings.TrimSpace(*body.TestCommand)
		}
		if body.SetupCommand != nil {
			p.SetupCommand = strings.TrimSpace(*body.SetupCommand)
		}
		if body.ScopeAllow != nil {
			p.ScopeAllow = *body.ScopeAllow
		}
		if body.TokenBudget != nil {
			if *body.TokenBudget < 0 {
				return http.StatusUnprocessableEntity, nil, errors.New("预算不能为负数")
			}
			p.TokenBudget = *body.TokenBudget
		}
		if err := project.SaveMeta(p); err != nil {
			return http.StatusInternalServerError, nil, err
		}
		return http.StatusOK, project.ToView(p), nil
	})
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	for _, run := range s.Engine.Runs() {
		if st := run.Snapshot(); st.ProjectID == id && run.Active() {
			writeError(w, http.StatusConflict, errors.New("项目还有任务在运行，先停止"))
			return
		}
	}
	if err := s.Projects.Remove(id); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// taskInput 是界面可编辑的任务字段。
type taskInput struct {
	Title       *string   `json:"title"`
	Description *string   `json:"description"`
	Kind        *string   `json:"kind"`
	Role        *string   `json:"role"`
	Assignee    *string   `json:"assignee"`
	DependsOn   *[]string `json:"dependsOn"`
	Scope       *[]string `json:"scope"`
	Acceptance  *[]string `json:"acceptance"`
	Verify      *[]string `json:"verify"`
}

func (in taskInput) apply(t *project.Task) {
	set := func(dst *string, src *string) {
		if src != nil {
			*dst = strings.TrimSpace(*src)
		}
	}
	list := func(dst *[]string, src *[]string) {
		if src != nil {
			var out []string
			for _, v := range *src {
				if v = strings.TrimSpace(v); v != "" {
					out = append(out, v)
				}
			}
			*dst = out
		}
	}
	set(&t.Title, in.Title)
	set(&t.Description, in.Description)
	set(&t.Kind, in.Kind)
	set(&t.Role, in.Role)
	set(&t.Assignee, in.Assignee)
	list(&t.DependsOn, in.DependsOn)
	list(&t.Scope, in.Scope)
	list(&t.Acceptance, in.Acceptance)
	list(&t.Verify, in.Verify)
}

var validKinds = map[string]bool{"feature": true, "ui": true, "test": true, "infra": true}
var validRoles = map[string]bool{"lead": true, "developer": true, "frontend": true, "tester": true, "reviewer": true}

func checkTask(t *project.Task) error {
	if t.Kind != "" && !validKinds[t.Kind] {
		return fmt.Errorf("未知的任务类型 %q", t.Kind)
	}
	if t.Role != "" && !validRoles[t.Role] {
		return fmt.Errorf("未知的角色 %q", t.Role)
	}
	return nil
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	var in taskInput
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.withProject(w, r, func(p *project.Project) (int, any, error) {
		var t project.Task
		in.apply(&t)
		if err := checkTask(&t); err != nil {
			return http.StatusUnprocessableEntity, nil, err
		}
		nt, err := project.NewTask(p, t)
		if err != nil {
			return http.StatusUnprocessableEntity, nil, err
		}
		return http.StatusCreated, nt, nil
	})
}

func (s *Server) patchTask(w http.ResponseWriter, r *http.Request) {
	var in taskInput
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.withProject(w, r, func(p *project.Project) (int, any, error) {
		t := p.Task(r.PathValue("task"))
		if t == nil {
			return http.StatusNotFound, nil, errors.New("任务不存在")
		}
		switch t.Status {
		case project.Running, project.Testing, project.Integrating, project.Merged:
			return http.StatusConflict, nil, errors.New("运行中或已合并的任务不能修改")
		}
		old := *t
		in.apply(t)
		if t.Title == "" {
			*t = old
			return http.StatusUnprocessableEntity, nil, errors.New("任务标题不能为空")
		}
		if err := checkTask(t); err != nil {
			*t = old
			return http.StatusUnprocessableEntity, nil, err
		}
		for _, d := range t.DependsOn {
			if d == t.ID || p.Task(d) == nil {
				*t = old
				return http.StatusUnprocessableEntity, nil, fmt.Errorf("依赖的任务 %s 不存在", d)
			}
		}
		if err := project.SaveTask(p, t); err != nil {
			return http.StatusInternalServerError, nil, err
		}
		return http.StatusOK, t, nil
	})
}

func (s *Server) taskAction(w http.ResponseWriter, r *http.Request) {
	id, task := r.PathValue("id"), r.PathValue("task")
	var err error
	switch r.PathValue("action") {
	case "retry":
		err = s.Engine.Retry(id, task)
	case "cancel":
		err = s.Engine.Cancel(id, task)
	case "approve":
		err = s.Engine.Approve(r.Context(), id, task)
	default:
		writeError(w, http.StatusNotFound, errors.New("未知操作"))
		return
	}
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---------- 需求与计划 ----------

func (s *Server) createRequest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	req, err := s.Engine.NewRequest(r.PathValue("id"), body.Text)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err)
		return
	}
	s.commitProject(r.PathValue("id"), req.ID+" 新需求")
	writeJSON(w, http.StatusCreated, req)
}

func (s *Server) putPlan(w http.ResponseWriter, r *http.Request) {
	var plan project.Plan
	if err := decode(r, &plan); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Engine.UpdatePlan(r.Context(), r.PathValue("id"), r.PathValue("req"), plan); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err)
		return
	}
	s.commitProject(r.PathValue("id"), r.PathValue("req")+" 修改计划")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) requestAction(w http.ResponseWriter, r *http.Request) {
	id, req := r.PathValue("id"), r.PathValue("req")
	var body struct {
		Text string `json:"text"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	var err error
	switch r.PathValue("action") {
	case "answer":
		err = s.Engine.Answer(id, req, body.Text)
	case "regenerate":
		err = s.Engine.Regenerate(id, req, body.Text)
	case "approve":
		err = s.Engine.ApprovePlan(id, req)
	case "discard":
		err = s.Engine.Discard(id, req)
	default:
		writeError(w, http.StatusNotFound, errors.New("未知操作"))
		return
	}
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	s.commitProject(id, req+" "+r.PathValue("action"))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) commitProject(id, msg string) {
	s.Projects.Lock()
	p := s.Projects.Get(id)
	s.Projects.Unlock()
	if p != nil {
		go s.commitState(p, msg)
	}
}

func (s *Server) deliver(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Minute)
	defer cancel()
	d, err := s.Engine.Deliver(ctx, r.PathValue("id"))
	if errors.Is(err, dispatch.ErrDirty) {
		writeJSON(w, http.StatusAccepted, d)
		return
	}
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// ---------- 运行 ----------

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) {
	runs := []dispatch.Stat{}
	for _, run := range s.Engine.Runs() {
		runs = append(runs, run.Snapshot())
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].StartedAt < runs[j].StartedAt })
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs, "tools": s.Engine.ToolState()})
}

// streamRun 用 SSE 推送一次尝试的实时输出；断线重连时带上 Last-Event-ID 从断点继续（DESIGN §6.4）。
func (s *Server) streamRun(w http.ResponseWriter, r *http.Request) {
	run := s.Engine.Run(r.PathValue("id") + "/" + r.PathValue("task"))
	if run == nil {
		writeError(w, http.StatusNotFound, errors.New("这个任务还没有运行记录"))
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("不支持流式输出"))
		return
	}
	after, _ := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64)
	if q := r.URL.Query().Get("after"); q != "" {
		after, _ = strconv.ParseInt(q, 10, 64)
	}
	backlog, ch := run.Subscribe(after)
	defer run.Unsubscribe(ch)

	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	send := func(e dispatch.Event) {
		data, _ := json.Marshal(e)
		fmt.Fprintf(w, "id: %d\ndata: %s\n\n", e.Seq, data)
	}
	fmt.Fprint(w, "retry: 2000\n\n")
	for _, e := range backlog {
		send(e)
	}
	fl.Flush()

	// 输出很快时每 100 毫秒合并发送一次
	flush := time.NewTicker(100 * time.Millisecond)
	defer flush.Stop()
	keep := time.NewTicker(20 * time.Second)
	defer keep.Stop()
	dirty := false
	for {
		select {
		case <-r.Context().Done():
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			send(e)
			dirty = true
		case <-flush.C:
			if dirty {
				fl.Flush()
				dirty = false
			}
		case <-keep.C:
			fmt.Fprint(w, ": keep-alive\n\n")
			fl.Flush()
		}
	}
}

func (s *Server) runLog(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.URL.Query().Get("attempt"))
	if err != nil || n < 1 {
		writeError(w, http.StatusBadRequest, errors.New("attempt 参数无效"))
		return
	}
	id, task := r.PathValue("id"), r.PathValue("task")
	s.Projects.Lock()
	p := s.Projects.Get(id)
	known := p != nil && (p.Task(task) != nil || p.Request(task) != nil)
	s.Projects.Unlock()
	if !known {
		writeError(w, http.StatusNotFound, errors.New("任务不存在"))
		return
	}
	data, err := os.ReadFile(s.Engine.LogPath(id, task, n))
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("没有这次尝试的日志"))
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(data)
}

// ---------- 计量 ----------

func (s *Server) listUsage(w http.ResponseWriter, r *http.Request) {
	s.Projects.Lock()
	var all []project.Usage
	for _, p := range s.Projects.Projects() {
		all = append(all, project.ReadUsage(p)...)
	}
	s.Projects.Unlock()
	sort.Slice(all, func(i, j int) bool { return all[i].At.After(all[j].At) })
	if all == nil {
		all = []project.Usage{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"records": all})
}
