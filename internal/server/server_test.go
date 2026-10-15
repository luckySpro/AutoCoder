package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/luckySpro/AutoCoder/internal/toolscan"
)

func newTestServer() *Server {
	return &Server{
		Version: "test",
		Scanner: &toolscan.Scanner{
			LookPath: func(c string) (string, error) {
				if c == "claude" || c == "codex" {
					return "/bin/" + c, nil
				}
				return "", errors.New("not found")
			},
			Stat:       func(string) (os.FileInfo, error) { return nil, os.ErrNotExist },
			RunVersion: func(context.Context, string, []string) (string, error) { return "1.0.0", nil },
		},
	}
}

func TestAPI(t *testing.T) {
	h := newTestServer().Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/roles", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/roles status %d", rec.Code)
	}
	var body struct {
		Assignments []struct {
			ToolID string   `json:"toolId"`
			Roles  []string `json:"roles"`
		} `json:"assignments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Assignments) != 2 || body.Assignments[0].ToolID != "claude-code" || body.Assignments[0].Roles[0] != "lead" {
		t.Fatalf("分配不符: %+v", body.Assignments)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("未知 API 应返回 404，实际 %d", rec.Code)
	}
}
