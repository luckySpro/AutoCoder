package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luckySpro/AutoCoder/internal/dispatch"
	"github.com/luckySpro/AutoCoder/internal/project"
	"github.com/luckySpro/AutoCoder/internal/server"
	"github.com/luckySpro/AutoCoder/internal/settings"
	"github.com/luckySpro/AutoCoder/internal/tray"
)

func TestDesktopHandler(t *testing.T) {
	home := t.TempDir()
	store, _ := project.Open(context.Background(), home)
	srv := &server.Server{Projects: store, Settings: &settings.File{Path: filepath.Join(home, "settings.json")}}
	srv.Engine = &dispatch.Engine{Store: store, Settings: srv.Settings}
	t.Setenv("AUTOCODER_DESKTOP_TOKEN", "secret")
	quits := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ui")) })
	h := desktopHandler(srv, "http://127.0.0.1:7788", next, func() { quits++ })

	do := func(method, path, body, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			req.Header.Set("X-AutoCoder-Token", token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// 没有或给错令牌：拒绝，尤其不能让别的程序叫它退出
	for _, tok := range []string{"", "wrong"} {
		if rec := do("POST", "/api/desktop/quit", "", tok); rec.Code != http.StatusForbidden {
			t.Fatalf("令牌 %q：%d", tok, rec.Code)
		}
	}
	if quits != 0 {
		t.Fatal("没有令牌也退出了")
	}
	if rec := do("GET", "/", "", ""); rec.Body.String() != "ui" {
		t.Fatal("界面路由应照常可用")
	}

	var m tray.Menu
	rec := do("GET", "/api/desktop/menu", "", "secret")
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil || m.Header != "还没有项目" || m.Pause != "暂停派发" || m.Paused {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := do("POST", "/api/desktop/pause", `{"paused":true}`, "secret"); rec.Code != http.StatusNoContent || !srv.Summary().Paused {
		t.Fatalf("暂停：%d", rec.Code)
	}
	if rec := do("GET", "/api/desktop/icon.png?template=1&size=44", "", "secret"); rec.Code != 200 || !strings.HasPrefix(rec.Body.String(), "\x89PNG") {
		t.Fatalf("图标：%d", rec.Code)
	}
	if rec := do("POST", "/api/desktop/quit", "", "secret"); rec.Code != http.StatusAccepted || quits != 1 {
		t.Fatalf("退出：%d %d", rec.Code, quits)
	}
}
