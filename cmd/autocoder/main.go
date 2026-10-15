// autocoder：单机多开发工具协同助手。
//
//	autocoder            启动本地界面（默认 127.0.0.1:7788）
//	autocoder tools      检测本机已安装的开发工具并输出默认角色分配
//	autocoder version    输出版本
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/luckySpro/AutoCoder/internal/dispatch"
	"github.com/luckySpro/AutoCoder/internal/gitinfo"
	"github.com/luckySpro/AutoCoder/internal/project"
	"github.com/luckySpro/AutoCoder/internal/roles"
	"github.com/luckySpro/AutoCoder/internal/server"
	"github.com/luckySpro/AutoCoder/internal/settings"
	"github.com/luckySpro/AutoCoder/internal/toolscan"
)

var version = "dev"

func main() {
	addr := flag.String("addr", "127.0.0.1:7788", "监听地址（默认仅本机）")
	manifest := flag.String("tools", toolscan.DefaultUserManifestPath(), "用户工具清单路径")
	flag.Parse()

	switch flag.Arg(0) {
	case "version":
		fmt.Println(version)
	case "tools":
		printTools(*manifest)
	case "", "serve":
		ctx := context.Background()
		home := filepath.Join(userHome(), ".autocoder")
		store, loadErrs := project.Open(ctx, home)
		srv := &server.Server{ManifestPath: *manifest, Scanner: toolscan.NewScanner(), Git: gitinfo.New(), Version: version,
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
		log.Printf("检测到 %d 个开发工具，清单：%s", n, *manifest)
		log.Printf("AutoCoder 已启动：http://%s", *addr)
		log.Fatal(http.ListenAndServe(*addr, srv.Handler()))
	default:
		fmt.Fprintf(os.Stderr, "未知命令 %q\n", flag.Arg(0))
		os.Exit(2)
	}
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
