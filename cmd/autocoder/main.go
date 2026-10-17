// autocoder：单机多开发工具协同助手。
//
//	autocoder            启动本地界面（默认 127.0.0.1:7788），在前台运行到 Ctrl+C
//	autocoder serve      同上；--desktop 由桌面外壳（desktop/，Rust + Tauri）启动时使用
//	autocoder tools      检测本机已安装的开发工具并输出默认角色分配
//	autocoder version    输出版本
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/luckySpro/AutoCoder/internal/dispatch"
	"github.com/luckySpro/AutoCoder/internal/gitinfo"
	"github.com/luckySpro/AutoCoder/internal/project"
	"github.com/luckySpro/AutoCoder/internal/roles"
	"github.com/luckySpro/AutoCoder/internal/server"
	"github.com/luckySpro/AutoCoder/internal/settings"
	"github.com/luckySpro/AutoCoder/internal/toolscan"
	"github.com/luckySpro/AutoCoder/internal/tray"
)

var version = "dev"

// desktop 为 true 时由桌面外壳（desktop/src-tauri）作为内置引擎启动。
var desktop bool

func main() {
	addr := flag.String("addr", "127.0.0.1:7788", "监听地址（默认仅本机）")
	manifest := flag.String("tools", toolscan.DefaultUserManifestPath(), "用户工具清单路径")
	open := flag.Bool("open", false, "启动后在浏览器中打开界面")
	desktopMode := flag.Bool("desktop", false, "由桌面外壳启动：日志写文件、读取登录 shell 的 PATH、外壳关闭标准输入时退出")
	flag.Parse()
	desktop = *desktopMode

	switch flag.Arg(0) {
	case "version":
		fmt.Println(version)
	case "tools":
		printTools(*manifest)
	case "", "serve":
		serve(*addr, *manifest, *open)
	default:
		fmt.Fprintf(os.Stderr, "未知命令 %q\n", flag.Arg(0))
		os.Exit(2)
	}
}

// serve 启动服务和调度。命令行版在前台运行到 Ctrl+C；
// 桌面版由外壳管理：外壳退出（标准输入关闭）或调用 /api/desktop/quit 时收尾退出。
func serve(addr, manifest string, openUI bool) {
	home := filepath.Join(userHome(), ".autocoder")
	if desktop {
		// 外壳退出后标准错误的管道会断，写日志不能因此直接结束进程（来不及收尾）
		signal.Ignore(syscall.SIGPIPE)
		logToFile(home)
		loginPath()
	}
	base := "http://" + addr

	// 单实例：端口上已经是 AutoCoder，就直接打开它的界面
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if running(base) {
			log.Printf("AutoCoder 已在运行：%s", base)
			if openUI {
				_ = tray.OpenURL(base + "/")
			}
			return
		}
		log.Fatalf("无法监听 %s：%v", addr, err)
	}

	ctx := context.Background()
	store, loadErrs := project.Open(ctx, home)
	srv := &server.Server{ManifestPath: manifest, Scanner: toolscan.NewScanner(), Git: gitinfo.New(), Version: version, Desktop: desktop,
		Projects: store, Settings: &settings.File{Path: filepath.Join(home, "settings.json")}}
	for _, e := range loadErrs {
		log.Printf("加载项目失败：%v", e)
		srv.LoadErrors = append(srv.LoadErrors, e.Error())
	}
	srv.Engine = &dispatch.Engine{Store: store, Settings: srv.Settings, LogDir: filepath.Join(home, "logs"),
		Tools: func(ctx context.Context) []toolscan.Detection { t, _, _ := srv.Scan(ctx, false); return t }}
	// 启动即检测一次，结果缓存给界面使用。
	tools, _, err := srv.Scan(ctx, true)
	if err != nil {
		log.Fatal(err)
	}
	srv.Engine.Start(ctx)
	log.Printf("已加载 %d 个项目", len(store.Projects()))
	n := 0
	for _, t := range tools {
		if t.Installed {
			n++
		}
	}
	log.Printf("检测到 %d 个开发工具，清单：%s", n, manifest)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	quit := func() {
		select {
		case sig <- syscall.SIGTERM:
		default: // 已经在退出
		}
	}
	handler := srv.Handler()
	if desktop {
		handler = desktopHandler(srv, base, handler, quit)
	}
	hs := &http.Server{Handler: handler}
	go func() {
		if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	log.Printf("AutoCoder 已启动：%s", base)
	if openUI {
		_ = tray.OpenURL(base + "/")
	}

	if desktop {
		go func() {
			_, _ = io.Copy(io.Discard, os.Stdin) // 外壳退出或崩溃时标准输入关闭
			log.Printf("桌面外壳已关闭")
			quit()
		}()
	}
	<-sig
	log.Printf("正在退出：停止派发，正在运行的任务保存进度后放回队列")
	srv.Engine.Shutdown(20 * time.Second)
	sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = hs.Shutdown(sctx)
	log.Printf("已退出")
}

// running 判断地址上是否已经是 AutoCoder。
func running(base string) bool {
	c := http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(base + "/api/version")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var v struct {
		Name string `json:"name"`
	}
	return json.NewDecoder(resp.Body).Decode(&v) == nil && v.Name == "autocoder"
}

// logToFile 让桌面版（没有终端窗口）把日志写到 ~/.autocoder/autocoder.log。
func logToFile(home string) {
	if err := os.MkdirAll(home, 0o755); err != nil {
		return
	}
	path := filepath.Join(home, "autocoder.log")
	if fi, err := os.Stat(path); err == nil && fi.Size() > 10<<20 {
		_ = os.Rename(path, path+".1") // 超过 10MB 轮换一次
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	log.SetOutput(teeWriter{f, os.Stderr})
}

// teeWriter 同时写文件和终端；没有终端时（Windows 图形程序）忽略终端的错误。
type teeWriter struct{ file, term io.Writer }

func (t teeWriter) Write(p []byte) (int, error) {
	_, _ = t.term.Write(p)
	return t.file.Write(p)
}

func userHome() string {
	h, err := os.UserHomeDir()
	if err != nil {
		log.Fatal("找不到用户目录：", err)
	}
	return h
}

func printTools(manifest string) {
	m, err := toolscan.LoadManifest(manifest)
	if err != nil {
		log.Fatal(err)
	}
	ds := toolscan.NewScanner().Scan(context.Background(), m)
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "状态\t工具\t版本\t用量\t路径")
	for _, d := range ds {
		mark, usage := "  -", ""
		if d.Installed {
			mark, usage = "  ✓", "估算"
			if d.ExactUsage() {
				usage = "精确"
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", mark, d.Name, d.Version, usage, d.Path)
	}
	w.Flush()

	names := map[roles.Role]string{}
	for _, r := range roles.All {
		names[r.ID] = r.Name
	}
	fmt.Println("\n默认角色分配：")
	as := roles.Suggest(ds)
	if len(as) == 0 {
		fmt.Println("  未检测到可用工具，请安装后重试或编辑", manifest)
	}
	for _, a := range as {
		var rs []string
		for _, r := range a.Roles {
			rs = append(rs, names[r])
		}
		fmt.Printf("  %-18s %s\n", a.ToolName, strings.Join(rs, "、"))
	}
}
