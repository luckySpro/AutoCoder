package server

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/luckySpro/AutoCoder/internal/dispatch"
	"github.com/luckySpro/AutoCoder/internal/project"
)

// Summary 是菜单栏和概览用的整体状态：正在跑什么、什么在等你、今天用了多少。
type Summary struct {
	Paused      bool          `json:"paused"`
	Slots       int           `json:"slots"`
	Running     []RunningItem `json:"running"`
	Waiting     []WaitingItem `json:"waiting"`
	TodayTokens int64         `json:"todayTokens"`
	TodayCalls  int           `json:"todayCalls"`
	TodayByTool []ToolTokens  `json:"todayByTool"` // 用量从多到少
	Projects    int           `json:"projects"`
}

type RunningItem struct {
	ProjectID   string `json:"projectId"`
	ProjectName string `json:"projectName"`
	TaskID      string `json:"taskId"`
	Title       string `json:"title"`
	Tool        string `json:"tool"`
	ToolName    string `json:"toolName"`
	Phase       string `json:"phase"`
	StartedAt   int64  `json:"startedAt"`
}

type WaitingItem struct {
	ProjectID   string `json:"projectId"`
	ProjectName string `json:"projectName"`
	ID          string `json:"id"` // 任务或需求编号；预算用完时为空
	Text        string `json:"text"`
}

type ToolTokens struct {
	Tool   string `json:"tool"`
	Name   string `json:"name"`
	Tokens int64  `json:"tokens"`
}

// Summary 汇总当前状态。
func (s *Server) Summary() Summary {
	st := s.Settings.Get()
	out := Summary{Paused: st.Paused, Slots: st.Concurrency, Running: []RunningItem{}, Waiting: []WaitingItem{}, TodayByTool: []ToolTokens{}}
	names := map[string]string{}
	if s.Engine.Tools != nil {
		for _, t := range s.Engine.Tools(context.Background()) {
			names[t.ID] = t.Name
		}
	}
	nameOf := func(id string) string {
		if n := names[id]; n != "" {
			return n
		}
		return id
	}

	var runs []dispatch.Stat
	for _, r := range s.Engine.Runs() {
		if r.Active() {
			runs = append(runs, r.Snapshot())
		}
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].StartedAt < runs[j].StartedAt })

	today := time.Now()
	y, m, d := today.Date()
	byTool := map[string]int64{}
	s.Projects.Lock()
	ps := s.Projects.Projects()
	out.Projects = len(ps)
	byID := map[string]*project.Project{}
	for _, p := range ps {
		byID[p.ID] = p
		for _, t := range p.Tasks {
			if t.Status == project.WaitingUser || t.Status == project.Blocked {
				text := t.Waiting
				if text == "" {
					text = t.LastError
				}
				out.Waiting = append(out.Waiting, WaitingItem{p.ID, p.Name, t.ID, t.Title + "：" + firstLine(text)})
			}
		}
		for _, r := range p.Requests {
			switch r.Status {
			case project.ReqQuestions:
				out.Waiting = append(out.Waiting, WaitingItem{p.ID, p.Name, r.ID, "lead 有问题要你回答"})
			case project.ReqProposed:
				out.Waiting = append(out.Waiting, WaitingItem{p.ID, p.Name, r.ID, "计划等你确认"})
			case project.ReqFailed:
				out.Waiting = append(out.Waiting, WaitingItem{p.ID, p.Name, r.ID, "规划没有成功"})
			}
		}
		if dispatch.OverBudget(p) {
			out.Waiting = append(out.Waiting, WaitingItem{p.ID, p.Name, "", "Token 预算已用完，暂停派发"})
		}
		for _, u := range project.ReadUsageOn(p, today) {
			if uy, um, ud := u.At.Local().Date(); uy != y || um != m || ud != d {
				continue
			}
			n := u.Usage.Total()
			out.TodayTokens += n
			out.TodayCalls++
			byTool[u.Tool] += n
		}
	}
	for _, r := range runs {
		item := RunningItem{ProjectID: r.ProjectID, TaskID: r.TaskID, Tool: r.Tool, ToolName: nameOf(r.Tool), Phase: r.Phase, StartedAt: r.StartedAt}
		if p := byID[r.ProjectID]; p != nil {
			item.ProjectName = p.Name
			if t := p.Task(r.TaskID); t != nil {
				item.Title = t.Title
			} else if q := p.Request(r.TaskID); q != nil {
				item.Title = q.Text
			}
		}
		item.Title = clipRunes(firstLine(item.Title), 40)
		out.Running = append(out.Running, item)
	}
	s.Projects.Unlock()

	for id, n := range byTool {
		out.TodayByTool = append(out.TodayByTool, ToolTokens{id, nameOf(id), n})
	}
	sort.Slice(out.TodayByTool, func(i, j int) bool {
		a, b := out.TodayByTool[i], out.TodayByTool[j]
		return a.Tokens > b.Tokens || a.Tokens == b.Tokens && a.Tool < b.Tool
	})
	return out
}

// SetPaused 暂停或恢复派发（运行中的跑完）。
func (s *Server) SetPaused(paused bool) error {
	st := s.Settings.Get()
	st.Paused = paused
	if _, err := s.Settings.Put(st); err != nil {
		return err
	}
	s.Engine.Wake()
	return nil
}

func (s *Server) getStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Summary())
}

func firstLine(s string) string {
	for i, c := range s {
		if c == '\n' {
			return s[:i]
		}
	}
	return s
}

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
