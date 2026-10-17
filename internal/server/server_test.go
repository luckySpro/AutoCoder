package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luckySpro/AutoCoder/internal/gitinfo"
	"github.com/luckySpro/AutoCoder/internal/toolscan"
)

func newTestServer(t *testing.T) (*Server, http.Handler) {
	s := &Server{
		Version:      "test",
		ManifestPath: filepath.Join(t.TempDir(), "tools.yaml"),
		Git:          &gitinfo.Client{Run: func(context.Context, string, ...string) (string, error) { return "", errors.New("no") }, LookPath: func(string) (string, error) { return "", errors.New("no") }},
		Scanner: &toolscan.Scanner{
			LookPath: func(c string) (string, error) {
				if c == "claude" || c == "codex" || c == "my-agent" {
					return "/bin/" + c, nil
				}
				return "", errors.New("not found")
			},
			Stat:       func(string) (os.FileInfo, error) { return nil, os.ErrNotExist },
			RunVersion: func(context.Context, string, []string) (string, error) { return "1.0.0", nil },
		},
	}
	return s, s.Handler()
}

func do(h http.Handler, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "127.0.0.1:7788"
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

var local = map[string]string{"X-AutoCoder": "1", "Content-Type": "application/json"}

func TestRoles(t *testing.T) {
	_, h := newTestServer(t)
	rec := do(h, "GET", "/api/roles", "", nil)
	var body struct {
		Assignments []struct {
			ToolID string   `json:"toolId"`
			Roles  []string `json:"roles"`
		} `json:"assignments"`
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != 200 || len(body.Assignments) != 2 || body.Assignments[0].ToolID != "claude-code" || body.Assignments[0].Roles[0] != "lead" {
		t.Fatalf("分配不符: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/api/nope", "", nil); rec.Code != 404 {
		t.Fatalf("未知 API 应返回 404，实际 %d", rec.Code)
	}
}

type toolsResp struct {
	Tools []toolscan.Detection `json:"tools"`
}

func find(t *testing.T, rec *httptest.ResponseRecorder, id string) *toolscan.Detection {
	t.Helper()
	var r toolsResp
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatalf("响应解析失败: %v %s", err, rec.Body)
	}
	for i := range r.Tools {
		if r.Tools[i].ID == id {
			return &r.Tools[i]
		}
	}
	return nil
}

func TestManageTools(t *testing.T) {
	s, h := newTestServer(t)

	// 新增自定义工具，立即被检测到
	rec := do(h, "PUT", "/api/tools/my-agent", `{"name":"My Agent","commands":["my-agent"],"headless":["run","{prompt}"],"capability":60,"strengths":["coding"]}`, local)
	if rec.Code != 200 {
		t.Fatalf("新增失败: %d %s", rec.Code, rec.Body)
	}
	if d := find(t, rec, "my-agent"); d == nil || !d.Installed || d.Source != toolscan.SourceCustom {
		t.Fatalf("自定义工具不符: %+v", d)
	}

	// 停用内置工具
	rec = do(h, "PUT", "/api/tools/codex", `{"name":"Codex CLI","commands":["codex"],"headless":["exec","{prompt}"],"capability":90,"disabled":true}`, local)
	if d := find(t, rec, "codex"); d == nil || !d.Disabled || d.Source != toolscan.SourceModified {
		t.Fatalf("停用后不符: %+v", d)
	}
	if data, _ := os.ReadFile(s.ManifestPath); !strings.Contains(string(data), "disabled: true") {
		t.Fatalf("用户清单未写入: %s", data)
	}

	// 内置工具"删除"即恢复默认
	rec = do(h, "DELETE", "/api/tools/codex", "", local)
	if d := find(t, rec, "codex"); d == nil || d.Disabled || d.Source != toolscan.SourceBuiltin {
		t.Fatalf("恢复默认后不符: %+v", d)
	}

	// 校验失败
	rec = do(h, "PUT", "/api/tools/bad", `{"name":"x","commands":["x"],"headless":["run"]}`, local)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "{prompt}") {
		t.Fatalf("应拒绝缺少占位符的模板: %d %s", rec.Code, rec.Body)
	}

	// 导出再导入
	rec = do(h, "GET", "/api/tools/export", "", nil)
	if !strings.Contains(rec.Body.String(), "my-agent") {
		t.Fatalf("导出内容不符: %s", rec.Body)
	}
	body, _ := json.Marshal(map[string]string{"yaml": "tools:\n  - id: other\n    name: Other\n    commands: [other]\n    headless: [\"{prompt}\"]\n"})
	rec = do(h, "POST", "/api/tools/import", string(body), local)
	if find(t, rec, "other") == nil || find(t, rec, "my-agent") != nil {
		t.Fatalf("导入应替换用户清单: %s", rec.Body)
	}
}

func TestLocalOnlyGuard(t *testing.T) {
	_, h := newTestServer(t)
	body := `{"name":"x","commands":["x"],"headless":["{prompt}"]}`
	if rec := do(h, "PUT", "/api/tools/x", body, map[string]string{"Content-Type": "application/json"}); rec.Code != http.StatusForbidden {
		t.Errorf("缺少请求头应被拒绝，实际 %d", rec.Code)
	}
	if rec := do(h, "PUT", "/api/tools/x", body, map[string]string{"X-AutoCoder": "1", "Origin": "https://evil.example"}); rec.Code != http.StatusForbidden {
		t.Errorf("跨站来源应被拒绝，实际 %d", rec.Code)
	}
	req := httptest.NewRequest("GET", "/api/tools", nil)
	req.Host = "evil.example:7788"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("非回环 Host 应被拒绝，实际 %d", rec.Code)
	}
}
