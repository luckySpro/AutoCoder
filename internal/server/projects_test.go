package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/luckySpro/AutoCoder/internal/dispatch"
	"github.com/luckySpro/AutoCoder/internal/project"
	"github.com/luckySpro/AutoCoder/internal/settings"
	"github.com/luckySpro/AutoCoder/internal/toolscan"
)

const fake = `#!/bin/sh
echo '{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"写文件"},{"type":"tool_use","name":"Write","input":{"file_path":"a.txt"}}]}}'
sleep 0.3
echo hi > a.txt
echo '{"type":"result","result":"完成","usage":{"input_tokens":50,"output_tokens":7}}'
`

func TestProjectFlowOverHTTP(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("假工具是 sh 脚本")
	}
	root := t.TempDir()
	script := filepath.Join(root, "fake")
	os.WriteFile(script, []byte(fake), 0o755)
	s, _ := newTestServer(t)
	store, _ := project.Open(context.Background(), filepath.Join(root, "home"))
	s.Projects = store
	s.Settings = &settings.File{Path: filepath.Join(root, "home", "settings.json")}
	tool := toolscan.Detection{Tool: toolscan.Tool{ID: "fake", Name: "Fake", Commands: []string{"fake"}, Headless: []string{"-p", "{prompt}"}, StreamArgs: []string{"--stream"}, Capability: 99, Strengths: []string{"architecture", "coding"}}, Installed: true, Path: script}
	s.Engine = &dispatch.Engine{Store: store, Settings: s.Settings, LogDir: filepath.Join(root, "home", "logs"), Tick: 50 * time.Millisecond,
		Tools: func(context.Context) []toolscan.Detection { return []toolscan.Detection{tool} }}
	h := s.Handler()
	srv := httptest.NewServer(h)
	defer srv.Close()

	// 设置：校验并发上限
	if rec := do(h, "PUT", "/api/settings", `{"concurrency":9}`, local); rec.Code != 422 {
		t.Fatalf("并发超过 5 应拒绝：%d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "PUT", "/api/settings", `{"concurrency":2,"autoMerge":false,"assignments":[{"toolId":"fake","roles":["lead","developer"]}]}`, local); rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body)
	}

	body := `{"source":"new","name":"web","path":"` + filepath.Join(root, "code", "web") + `","baseBranch":"main"}`
	if rec := do(h, "POST", "/api/projects", body, local); rec.Code != 201 {
		t.Fatal(rec.Code, rec.Body)
	}
	if rec := do(h, "POST", "/api/projects/web/tasks", `{"title":"写 a","kind":"weird"}`, local); rec.Code != 422 {
		t.Fatal("未知类型应拒绝", rec.Code)
	}
	if rec := do(h, "POST", "/api/projects/web/tasks", `{"title":"写 a","acceptance":["有 a.txt"," "],"verify":["test -f a.txt"]}`, local); rec.Code != 201 {
		t.Fatal(rec.Code, rec.Body)
	}
	s.Engine.Start(context.Background())

	// 等它开始运行，然后接上实时输出
	var stream *bufio.Scanner
	deadline := time.Now().Add(10 * time.Second)
	for stream == nil && time.Now().Before(deadline) {
		resp, err := srv.Client().Get(srv.URL + "/api/runs/web/T1/stream")
		if err == nil && resp.StatusCode == 200 {
			defer resp.Body.Close()
			stream = bufio.NewScanner(resp.Body)
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(50 * time.Millisecond)
	}
	if stream == nil {
		t.Fatal("没有运行记录")
	}
	sawAct, sawStat := false, false
	for stream.Scan() && !(sawAct && sawStat) {
		line := stream.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var e dispatch.Event
		json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &e)
		sawAct = sawAct || (e.Type == "act" && e.Kind == "edit" && e.Text == "a.txt")
		sawStat = sawStat || (e.Type == "stat" && e.Stat != nil && e.Stat.Tool == "fake")
	}
	if !sawAct || !sawStat {
		t.Fatalf("实时输出缺少动作或状态：act=%v stat=%v", sawAct, sawStat)
	}

	var state struct {
		Projects []struct {
			ID          string `json:"id"`
			Deliverable bool   `json:"deliverable"`
			Tasks       []struct {
				Status   string `json:"status"`
				Progress int    `json:"progress"`
			} `json:"tasks"`
		} `json:"projects"`
	}
	for time.Now().Before(deadline) {
		json.Unmarshal(do(h, "GET", "/api/projects", "", nil).Body.Bytes(), &state)
		if len(state.Projects) == 1 && state.Projects[0].Tasks[0].Status == "merged" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if p := state.Projects[0]; p.Tasks[0].Status != "merged" || p.Tasks[0].Progress != 100 || !p.Deliverable {
		t.Fatalf("%+v", state)
	}
	var usage struct {
		Records []project.Usage `json:"records"`
	}
	json.Unmarshal(do(h, "GET", "/api/usage", "", nil).Body.Bytes(), &usage)
	if len(usage.Records) != 1 || usage.Records[0].Usage.Total() != 57 {
		t.Fatalf("%+v", usage)
	}
	if rec := do(h, "GET", "/api/runs/web/T1/log?attempt=1", "", nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), "写文件") {
		t.Fatalf("日志：%d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/api/runs/web/..%2f..%2fetc/log?attempt=1", "", nil); rec.Code == 200 {
		t.Fatal("不应读到任意文件")
	}
	// 自动合并关闭：手动交付
	if rec := do(h, "POST", "/api/projects/web/deliver", "", local); rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(root, "code", "web", "a.txt")); err != nil {
		t.Fatal("交付后主目录应有 a.txt")
	}
}
