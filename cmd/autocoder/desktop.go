package main

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"os"
	"strconv"

	"github.com/luckySpro/AutoCoder/internal/server"
	"github.com/luckySpro/AutoCoder/internal/tray"
)

// desktopHandler 在界面路由之外，给桌面外壳加几个接口：
//
//	GET  /api/desktop/menu       菜单栏内容（tray.Menu）
//	GET  /api/desktop/icon.png   菜单栏图标；template=1 为 macOS 模板图标，size 为边长
//	POST /api/desktop/pause      {"paused":true|false}
//	POST /api/desktop/quit       收尾后退出：运行中的任务放回队列
//
// 这些接口要求请求头 X-AutoCoder-Token 等于外壳启动引擎时给的环境变量 AUTOCODER_DESKTOP_TOKEN，
// 本机其他程序和网页拿不到这个值，不能借此让 AutoCoder 退出。
func desktopHandler(srv *server.Server, base string, next http.Handler, quit func()) http.Handler {
	token := os.Getenv("AUTOCODER_DESKTOP_TOKEN")
	os.Unsetenv("AUTOCODER_DESKTOP_TOKEN") // 不传给开发工具等子进程
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/desktop/menu", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(tray.Build(srv.Summary(), base))
	})
	mux.HandleFunc("GET /api/desktop/icon.png", func(w http.ResponseWriter, r *http.Request) {
		size, _ := strconv.Atoi(r.URL.Query().Get("size"))
		if size < 16 || size > 256 {
			size = 32
		}
		st := tray.Build(srv.Summary(), base).Icon
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(tray.Icon(st, size, r.URL.Query().Get("template") == "1"))
	})
	mux.HandleFunc("POST /api/desktop/pause", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Paused bool `json:"paused"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body); err != nil {
			http.Error(w, "请求格式不对", http.StatusBadRequest)
			return
		}
		if err := srv.SetPaused(body.Paused); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/desktop/quit", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		quit()
	})
	guarded := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("X-AutoCoder-Token")
		if token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			http.Error(w, "只有桌面外壳能调用", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
	outer := http.NewServeMux()
	outer.Handle("/api/desktop/", guarded)
	outer.Handle("/", next)
	return outer
}
