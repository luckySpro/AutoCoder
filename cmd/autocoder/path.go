package main

import (
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// loginPath 让从访达、启动台或桌面菜单打开的桌面版也能找到开发工具：
// 这样启动的程序只有系统默认的 PATH，没有 Homebrew、npm、nvm 等目录。
// 这里读一次登录 shell 的 PATH，再补上常见的安装目录和 npm 全局目录，和当前的合并。
func loginPath() {
	if runtime.GOOS == "windows" {
		return
	}
	sh := userShell()
	// 交互式登录 shell 会读 .zshrc / .bashrc（nvm 等通常写在这里），有的配置较慢，给足时间；
	// 失败时退回非交互式登录 shell（只读 .zprofile / .bash_profile）。
	login := shellPath(sh, "-ilc", 15*time.Second)
	if login == "" {
		login = shellPath(sh, "-lc", 5*time.Second)
	}
	home, _ := os.UserHomeDir()
	merged := mergePath(login, os.Getenv("PATH"), strings.Join(knownDirs(home), string(os.PathListSeparator)))
	os.Setenv("PATH", merged)
	// npm 自定义了全局目录（npm config set prefix）时，codex、claude 等装在 <prefix>/bin
	if p := npmGlobalBin(); p != "" {
		os.Setenv("PATH", mergePath(merged, p))
	}
	log.Printf("查找开发工具的目录（PATH）：%s", os.Getenv("PATH"))
}

func userShell() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	if runtime.GOOS == "darwin" {
		// 从访达打开时可能没有 SHELL，读账户设置里的登录 shell
		if u := os.Getenv("USER"); u != "" {
			if out, err := exec.Command("/usr/bin/dscl", ".", "-read", "/Users/"+u, "UserShell").Output(); err == nil {
				if f := strings.Fields(string(out)); len(f) == 2 && strings.HasPrefix(f[1], "/") {
					return f[1]
				}
			}
		}
		return "/bin/zsh"
	}
	return "/bin/sh"
}

// shellPath 在登录 shell 里输出 PATH；拿不到时返回空。
func shellPath(sh, flags string, timeout time.Duration) string {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	const mark = "__AUTOCODER_PATH__"
	cmd := exec.CommandContext(ctx, sh, flags, "echo "+mark+"${PATH}"+mark)
	cmd.Env = append(os.Environ(), "TERM=dumb")
	out, err := cmd.Output() // 配置文件报错时退出码可能非零，但 PATH 已经输出了
	if parts := strings.Split(string(out), mark); len(parts) >= 3 {
		return parts[1]
	}
	log.Printf("读取登录 shell（%s %s）的 PATH 失败：%v", sh, flags, err)
	return ""
}

// knownDirs 是常见的命令行工具安装目录，只返回存在的。
func knownDirs(home string) []string {
	dirs := []string{"/opt/homebrew/bin", "/usr/local/bin"}
	for _, d := range []string{
		".local/bin", ".npm-global/bin", ".npm-packages/bin", ".bun/bin", ".volta/bin", ".yarn/bin", ".deno/bin", ".cargo/bin",
		"Library/pnpm", ".local/share/pnpm", ".asdf/shims", ".local/share/mise/shims",
		".fnm/aliases/default/bin", "Library/Application Support/fnm/aliases/default/bin", ".nodenv/shims",
	} {
		dirs = append(dirs, filepath.Join(home, d))
	}
	// nvm：所有已装的 Node 版本，新的在前
	nvm, _ := filepath.Glob(filepath.Join(home, ".nvm", "versions", "node", "*", "bin"))
	sort.Slice(nvm, func(i, j int) bool { return versionLess(nodeVersion(nvm[j]), nodeVersion(nvm[i])) })
	dirs = append(dirs, nvm...)
	var out []string
	for _, d := range dirs {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			out = append(out, d)
		}
	}
	return out
}

func nodeVersion(bin string) string { return strings.TrimPrefix(filepath.Base(filepath.Dir(bin)), "v") }

// versionLess 按数字比较 22.3.0 与 9.11.2 这样的版本号。
func versionLess(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		x, _ := strconv.Atoi(as[i])
		y, _ := strconv.Atoi(bs[i])
		if x != y {
			return x < y
		}
	}
	return len(as) < len(bs)
}

func npmGlobalBin() string {
	npm, err := exec.LookPath("npm")
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, npm, "prefix", "-g").Output()
	if err != nil {
		return ""
	}
	p := strings.TrimSpace(string(out))
	if p == "" {
		return ""
	}
	return filepath.Join(p, "bin")
}

// mergePath 按先后顺序合并多个 PATH，去掉重复和空项。
func mergePath(paths ...string) string {
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		for _, d := range filepath.SplitList(p) {
			if d != "" && !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	return strings.Join(out, string(os.PathListSeparator))
}
