// Package server 提供本地 HTTP API，并托管内嵌的前端界面。
package server

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/luckySpro/AutoCoder/internal/roles"
	"github.com/luckySpro/AutoCoder/internal/toolscan"
)

// dist 由 `make web` 从 web/dist 同步而来；仓库中只保留占位文件。
//
//go:embed all:dist
var dist embed.FS

// Server 持有工具检测结果的缓存。
type Server struct {
	ManifestPath string
	Scanner      *toolscan.Scanner
	Version      string

	mu       sync.Mutex
	scanned  []toolscan.Detection
	scanTime time.Time
}

// Scan 返回检测结果；refresh 为 true 或尚未检测时重新扫描。
func (s *Server) Scan(ctx context.Context, refresh bool) ([]toolscan.Detection, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.scanned != nil && !refresh {
		return s.scanned, s.scanTime, nil
	}
	m, err := toolscan.LoadManifest(s.ManifestPath)
	if err != nil {
		return nil, time.Time{}, err
	}
	s.scanned, s.scanTime = s.Scanner.Scan(ctx, m), time.Now()
	return s.scanned, s.scanTime, nil
}

// Handler 返回完整的路由。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"version": s.Version})
	})
	mux.HandleFunc("GET /api/tools", func(w http.ResponseWriter, r *http.Request) {
		tools, at, err := s.Scan(r.Context(), r.URL.Query().Get("refresh") == "1")
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"manifestPath": s.ManifestPath,
			"scannedAt":    at,
			"tools":        tools,
		})
	})
	mux.HandleFunc("GET /api/roles", func(w http.ResponseWriter, r *http.Request) {
		tools, _, err := s.Scan(r.Context(), false)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"roles":       roles.All,
			"assignments": roles.Suggest(tools),
		})
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	})
	mux.Handle("/", spa())
	return mux
}

// spa 托管前端静态资源；未知的前端路由回落到 index.html。
func spa() http.Handler {
	sub, _ := fs.Sub(dist, "dist")
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if _, err := fs.Stat(sub, p); err == nil {
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		index, err := fs.ReadFile(sub, "index.html")
		if err != nil {
			http.Error(w, "前端尚未构建：请先运行 make web", http.StatusNotFound)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(index)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
