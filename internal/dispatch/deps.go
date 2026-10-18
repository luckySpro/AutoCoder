package dispatch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/luckySpro/AutoCoder/internal/gitops"
	"github.com/luckySpro/AutoCoder/internal/project"
)

// 这些文件变了，说明依赖需要重新安装（npm、pnpm、yarn、bun、Python）。
// Cargo、Go 在构建时自己拉依赖，不需要单独安装。
var depsFiles = []string{"package.json", "package-lock.json", "npm-shrinkwrap.json", "pnpm-lock.yaml", "yarn.lock", "bun.lock", "bun.lockb",
	"requirements.txt", "requirements-dev.txt", "pyproject.toml", "poetry.lock", "uv.lock", "Pipfile.lock"}

// depsHash 是依赖清单内容的摘要；一个都没有时返回空串。
func depsHash(dir string) string {
	h := sha256.New()
	found := false
	for _, name := range depsFiles {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		found = true
		h.Write([]byte(name + "\x00"))
		h.Write(data)
		h.Write([]byte{0})
	}
	if !found {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// installCommand 返回安装依赖的命令：优先用项目设置，否则按锁文件推断。
func installCommand(p *project.Project, dir string) string {
	if p.SetupCommand != "" {
		return p.SetupCommand
	}
	has := func(n string) bool { _, err := os.Stat(filepath.Join(dir, n)); return err == nil }
	switch {
	case !has("package.json"):
		return ""
	case has("pnpm-lock.yaml"):
		return "pnpm install"
	case has("yarn.lock"):
		return "yarn install"
	case has("bun.lock") || has("bun.lockb"):
		return "bun install"
	default:
		return "npm install"
	}
}

// depsStamp 记录上次装依赖时清单的摘要，放在 worktree 自己的 git 目录里，不进版本库。
func depsStamp(ctx context.Context, dir string) string {
	p, err := gitops.Run(ctx, dir, "rev-parse", "--path-format=absolute", "--git-path", "autocoder-deps")
	if err != nil || strings.TrimSpace(p) == "" {
		return ""
	}
	return strings.TrimSpace(p)
}

// ensureDeps 在依赖清单变化后（或 force）重新安装依赖。返回是否运行了安装命令。
// 安装失败只提示，不拦住任务：后面的验证会给出明确结果。
func (e *Engine) ensureDeps(ctx context.Context, p *project.Project, dir string, run *Run, logf *os.File, slot int, force bool) bool {
	cmd := installCommand(p, dir)
	if cmd == "" {
		return false
	}
	sum := depsHash(dir)
	stamp := depsStamp(ctx, dir)
	if !force {
		if sum == "" && p.SetupCommand == "" {
			return false
		}
		if stamp != "" {
			if old, err := os.ReadFile(stamp); err == nil && strings.TrimSpace(string(old)) == sum {
				return false
			}
		}
	}
	msg := "依赖清单有变化，先安装依赖：" + cmd
	if force {
		msg = "验证缺少命令或模块，重新安装依赖后再试：" + cmd
	}
	run.emit(Event{Type: "act", Kind: "say", Text: msg})
	writeLog(logf, msg, false)
	ok, _ := e.shellStep(ctx, dir, cmd, run, logf, slot)
	if !ok {
		warn := "安装依赖失败（" + cmd + "），继续往下走"
		run.emit(Event{Type: "act", Kind: "warn", Text: warn, Tone: "warn"})
		writeLog(logf, warn, true)
		return true
	}
	if stamp != "" {
		_ = os.WriteFile(stamp, []byte(sum), 0o644)
	}
	return true
}

// missingDepRe 匹配"命令或模块找不到"的常见报错：多半是依赖没装全，而不是代码问题。
var missingDepRe = regexp.MustCompile(`(?i)(command not found|: not found|is not recognized as an internal or external command|cannot find module|err_module_not_found|module not found|no module named|cannot find package)`)

// runChecked 运行测试 / 验证命令：先确保依赖装好；失败且像是缺依赖时，重装依赖再跑一次。
func (e *Engine) runChecked(ctx context.Context, p *project.Project, dir, command string, run *Run, logf *os.File, slot int) (bool, string) {
	installed := e.ensureDeps(ctx, p, dir, run, logf, slot, false)
	ok, out := e.shellStep(ctx, dir, command, run, logf, slot)
	if ok || installed || !missingDepRe.MatchString(out) || installCommand(p, dir) == "" {
		return ok, out
	}
	e.ensureDeps(ctx, p, dir, run, logf, slot, true)
	return e.shellStep(ctx, dir, command, run, logf, slot)
}
