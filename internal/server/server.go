// Package server 提供本地 HTTP API，并托管内嵌的前端界面。
package server

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/luckySpro/AutoCoder/internal/dispatch"
	"github.com/luckySpro/AutoCoder/internal/gitinfo"
	"github.com/luckySpro/AutoCoder/internal/project"
	"github.com/luckySpro/AutoCoder/internal/roles"
	"github.com/luckySpro/AutoCoder/internal/settings"
	"github.com/luckySpro/AutoCoder/internal/toolscan"
	"gopkg.in/yaml.v3"
)

// dist 由 `make web` 从 web/dist 同步而来；仓库中只保留占位文件。
//
//go:embed all:dist
var dist embed.FS

// Server 持有工具检测结果的缓存。
type Server struct {
	ManifestPath string
	Scanner      *toolscan.Scanner
	Git          *gitinfo.Client
	Version      string
	Desktop      bool // 桌面版：有菜单栏图标，关掉浏览器也在后台运行
	// 以下三项为空时只提供工具清单相关接口（测试中使用）。
	Projects   *project.Store
	Engine     *dispatch.Engine
	Settings   *settings.File
	LoadErrors []string // 启动时加载失败的项目

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
		writeJSON(w, http.StatusOK, map[string]any{"name": "autocoder", "version": s.Version, "desktop": s.Desktop})
	})

	// 工具清单：查询、在界面内增删改、恢复默认、导入导出
	mux.HandleFunc("GET /api/tools", s.listTools)
	mux.HandleFunc("PUT /api/tools/{id}", s.saveTool)
	mux.HandleFunc("DELETE /api/tools/{id}", s.deleteTool)
	mux.HandleFunc("GET /api/tools/export", s.exportTools)
	mux.HandleFunc("POST /api/tools/import", s.importTools)

	mux.HandleFunc("GET /api/roles", func(w http.ResponseWriter, r *http.Request) {
		tools, _, err := s.Scan(r.Context(), false)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		as := roles.Suggest(tools)
		saved := false
		if s.Settings != nil {
			st := s.Settings.Get()
			saved = len(st.Assignments) > 0
			as = dispatch.Assignments(st, tools)
		}
		writeJSON(w, http.StatusOK, map[string]any{"roles": roles.All, "assignments": as, "suggested": roles.Suggest(tools), "saved": saved})
	})

	if s.Projects != nil && s.Engine != nil && s.Settings != nil {
		s.projectRoutes(mux)
	}

	// 本机 git / GitHub 身份与仓库，用于添加项目
	mux.HandleFunc("GET /api/git/identity", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.Git.Identity(r.Context()))
	})
	mux.HandleFunc("GET /api/git/repos", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		repos, err := s.Git.Repos(ctx, 200)
		if errors.Is(err, gitinfo.ErrNoGh) {
			writeError(w, http.StatusPreconditionFailed, err)
			return
		}
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"repos": repos})
	})
	mux.HandleFunc("GET /api/git/local", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"repos": s.Git.LocalRepos(r.Context())})
	})

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	})
	mux.Handle("/", spa())
	return localOnly(mux)
}

func (s *Server) listTools(w http.ResponseWriter, r *http.Request) {
	tools, at, err := s.Scan(r.Context(), r.URL.Query().Get("refresh") == "1")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"manifestPath": s.ManifestPath,
		"searchPath":   filepath.SplitList(os.Getenv("PATH")), // 按这些目录查找工具命令
		"scannedAt":    at,
		"strengths":    toolscan.Strengths,
		"tools":        tools,
	})
}

// saveTool 新增或修改一个工具，写入用户清单后立即重新检测。
func (s *Server) saveTool(w http.ResponseWriter, r *http.Request) {
	var t toolscan.Tool
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&t); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("请求格式错误"))
		return
	}
	t.ID = r.PathValue("id")
	if err := toolscan.Validate(t); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err)
		return
	}
	s.updateUser(w, r, func(m toolscan.Manifest) toolscan.Manifest { return toolscan.Upsert(m, t) })
}

// deleteTool 删除自定义工具；内置工具则恢复默认（删除用户覆盖）。
func (s *Server) deleteTool(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.updateUser(w, r, func(m toolscan.Manifest) toolscan.Manifest { return toolscan.Remove(m, id) })
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request, f func(toolscan.Manifest) toolscan.Manifest) {
	s.mu.Lock()
	user, err := toolscan.ReadUserManifest(s.ManifestPath)
	if err == nil {
		err = toolscan.WriteUserManifest(s.ManifestPath, f(user))
	}
	s.mu.Unlock()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	r.URL.RawQuery = "refresh=1"
	s.listTools(w, r)
}

func (s *Server) exportTools(w http.ResponseWriter, r *http.Request) {
	m, err := toolscan.ReadUserManifest(s.ManifestPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	data, err := toolscan.MarshalManifest(m)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="autocoder-tools.yaml"`)
	w.Write(data)
}

// importTools 用上传的 YAML 替换用户清单；每个条目都要通过校验。
func (s *Server) importTools(w http.ResponseWriter, r *http.Request) {
	var body struct {
		YAML string `json:"yaml"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 256<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("请求格式错误"))
		return
	}
	var m toolscan.Manifest
	if err := yaml.Unmarshal([]byte(body.YAML), &m); err != nil {
		writeError(w, http.StatusUnprocessableEntity, errors.New("清单不是有效的 YAML："+err.Error()))
		return
	}
	for _, t := range m.Tools {
		if err := toolscan.Validate(t); err != nil {
			writeError(w, http.StatusUnprocessableEntity, errors.New(t.ID+"："+err.Error()))
			return
		}
	}
	s.updateUser(w, r, func(toolscan.Manifest) toolscan.Manifest { return m })
}

// localOnly 只接受本机访问：Host 必须是回环地址（防 DNS 重绑定）；
// 写请求必须带自定义请求头且来源为本机（浏览器跨站请求无法带上该请求头）。
func localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackHost(r.Host) {
			writeError(w, http.StatusForbidden, errors.New("只允许通过 127.0.0.1 或 localhost 访问"))
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.Header.Get("X-AutoCoder") != "1" {
				writeError(w, http.StatusForbidden, errors.New("缺少 X-AutoCoder 请求头"))
				return
			}
			if o := r.Header.Get("Origin"); o != "" && !isLoopbackHost(strings.TrimPrefix(strings.TrimPrefix(o, "http://"), "https://")) {
				writeError(w, http.StatusForbidden, errors.New("拒绝来自其他网站的请求"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// spa 托管前端静态资源；未知路径回落到 index.html。
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

func writeError(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}
