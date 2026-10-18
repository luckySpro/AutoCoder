// Package toolscan 负责加载开发工具清单，并在启动时检测本机已安装的工具。
package toolscan

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

//go:embed tools.yaml
var defaultManifest []byte

// Tool 是清单中的一个条目。只收录命令行工具，统一由编排器派发与计量。
type Tool struct {
	ID          string              `yaml:"id" json:"id"`
	Name        string              `yaml:"name" json:"name"`
	Vendor      string              `yaml:"vendor" json:"vendor"`
	Commands    []string            `yaml:"commands" json:"commands"`
	Paths       map[string][]string `yaml:"paths" json:"paths,omitempty"`
	VersionArgs []string            `yaml:"version_args" json:"versionArgs,omitempty"`
	// HelpArgs 输出帮助文本，用来核对当前安装版本是否还认识清单里的参数。
	HelpArgs  []string `yaml:"help_args,omitempty" json:"helpArgs,omitempty"`
	Headless  []string `yaml:"headless" json:"headless,omitempty"`
	UsageArgs []string `yaml:"usage_args" json:"usageArgs,omitempty"`
	// 无人值守派发用的参数（见 DESIGN §6.3），都可以在界面上编辑。
	AutoArgs []string `yaml:"auto_args,omitempty" json:"autoArgs,omitempty"` // 免确认、限定在工作目录内修改
	FullArgs []string `yaml:"full_args,omitempty" json:"fullArgs,omitempty"` // 最高权限：直接执行命令、联网，不再询问
	// Access 选择派发时用哪组参数："full"（默认，最高权限，用 full_args）或 "standard"（只改文件，用 auto_args）。
	Access       string   `yaml:"access,omitempty" json:"access,omitempty"`
	StreamArgs   []string `yaml:"stream_args,omitempty" json:"streamArgs,omitempty"`     // 输出可解析的事件流；设置后代替 usage_args
	ReadonlyArgs []string `yaml:"readonly_args,omitempty" json:"readonlyArgs,omitempty"` // 只读模式，用于规划与评审
	AuthErrors   []string `yaml:"auth_errors,omitempty" json:"authErrors,omitempty"`     // 输出中表示"未登录"的关键字
	LimitErrors  []string `yaml:"limit_errors,omitempty" json:"limitErrors,omitempty"`   // 输出中表示"限流 / 额度用完"的关键字
	Capability   int      `yaml:"capability" json:"capability"`
	Strengths    []string `yaml:"strengths" json:"strengths"`
	Disabled     bool     `yaml:"disabled,omitempty" json:"disabled"`
	// Source 标记条目来源：builtin（内置）、modified（内置但被用户修改）、custom（用户新增）。不写入文件。
	Source string `yaml:"-" json:"source"`
}

// Manifest 是工具清单文件。
type Manifest struct {
	Tools []Tool `yaml:"tools"`
}

// Detection 是单个工具的检测结果。
type Detection struct {
	Tool
	Installed bool   `json:"installed"`
	Path      string `json:"path,omitempty"`
	Version   string `json:"version,omitempty"`
	Error     string `json:"error,omitempty"`
	// ArgIssues 是当前版本的帮助里找不到的参数（参数可能已改名或移除）。
	ArgIssues []string `json:"argIssues,omitempty"`
}

// Schedulable 表示该工具能否被编排器派发任务（已安装且有非交互调用模板）。
func (d Detection) Schedulable() bool {
	return d.Installed && len(d.Headless) > 0
}

// ExactUsage 表示该工具能输出结构化的 token 用量；否则按字符数估算。
func (d Detection) ExactUsage() bool {
	return len(d.UsageArgs) > 0 || len(d.StreamArgs) > 0
}

// Args 生成派发时的完整参数：调用模板中的 {prompt} 换成任务说明，
// 无人值守（或只读）参数与事件流/用量参数插在提示词之前（提示词前面紧挨的选项如 -p 一起后移）。
func (t Tool) Args(prompt string, readonly bool) []string {
	extra := append([]string(nil), t.AutoArgs...)
	if t.FullAccess() {
		extra = append([]string(nil), t.FullArgs...)
	}
	if readonly {
		extra = append([]string(nil), t.ReadonlyArgs...)
	}
	if len(t.StreamArgs) > 0 {
		extra = append(extra, t.StreamArgs...)
	} else {
		extra = append(extra, t.UsageArgs...)
	}
	at := len(t.Headless)
	for i, a := range t.Headless {
		if a == "{prompt}" {
			at = i
			if i > 0 && strings.HasPrefix(t.Headless[i-1], "-") {
				at = i - 1
			}
			break
		}
	}
	out := make([]string, 0, len(t.Headless)+len(extra))
	out = append(out, t.Headless[:at]...)
	out = append(out, extra...)
	for _, a := range t.Headless[at:] {
		if a == "{prompt}" {
			a = prompt
		}
		out = append(out, a)
	}
	return out
}

// FullAccess 表示派发时用最高权限参数：没有改成 standard 且配了 full_args。
func (t Tool) FullAccess() bool {
	return t.Access != "standard" && len(t.FullArgs) > 0
}

// Builtin 返回内置清单。
func Builtin() Manifest {
	var m Manifest
	if err := yaml.Unmarshal(defaultManifest, &m); err != nil {
		panic(fmt.Sprintf("内置清单解析失败: %v", err)) // 由测试保证不会发生
	}
	for i := range m.Tools {
		m.Tools[i].Source = SourceBuiltin
	}
	return m
}

// LoadManifest 读取内置清单，并按 id 合并用户清单（userPath 为空或不存在时忽略）。
// 有在线清单缓存（tools.remote.yaml）时，先用它更新内置条目的参数。
func LoadManifest(userPath string) (Manifest, error) {
	builtin := Builtin()
	base := applyRemote(builtin, readRemote(RemotePath(userPath)))
	user, err := ReadUserManifest(userPath)
	if err != nil {
		return base, err
	}
	return Merge(base, followRemote(user, builtin, base)), nil
}

// followRemote：用户只改了权限、启用等设置、没动参数时，参数跟着在线清单走；
// 用户自己改过的参数保持不变。
func followRemote(user, builtin, base Manifest) Manifest {
	out := Manifest{Tools: append([]Tool(nil), user.Tools...)}
	for i := range out.Tools {
		e, b := find(builtin, out.Tools[i].ID), find(base, out.Tools[i].ID)
		if e == nil || b == nil {
			continue
		}
		uf, ef, bf := argFields(&out.Tools[i]), argFields(e), argFields(b)
		for k := range uf {
			if sameArgs(*uf[k], *ef[k]) {
				*uf[k] = append([]string(nil), *bf[k]...)
			}
		}
	}
	return out
}

// ForSave 在写入用户清单前调用：和当前生效的（在线）参数相同的字段记成内置值，
// 这样以后在线清单再更新时，这些参数还能继续跟着更新。
func ForSave(t Tool, userPath string) Tool {
	builtin := Builtin()
	base := applyRemote(builtin, readRemote(RemotePath(userPath)))
	e, b := find(builtin, t.ID), find(base, t.ID)
	if e == nil || b == nil {
		return t
	}
	tf, ef, bf := argFields(&t), argFields(e), argFields(b)
	for k := range tf {
		if sameArgs(*tf[k], *bf[k]) {
			*tf[k] = append([]string(nil), *ef[k]...)
		}
	}
	return t
}

func find(m Manifest, id string) *Tool {
	for i := range m.Tools {
		if m.Tools[i].ID == id {
			return &m.Tools[i]
		}
	}
	return nil
}

func argFields(t *Tool) []*[]string {
	return []*[]string{&t.VersionArgs, &t.HelpArgs, &t.Headless, &t.UsageArgs, &t.AutoArgs, &t.FullArgs,
		&t.StreamArgs, &t.ReadonlyArgs, &t.AuthErrors, &t.LimitErrors}
}

func sameArgs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Merge 以 id 为键，用 override 中的条目替换或追加到 base，并标记来源。
func Merge(base, override Manifest) Manifest {
	idx := make(map[string]int, len(base.Tools))
	out := Manifest{Tools: append([]Tool(nil), base.Tools...)}
	for i, t := range out.Tools {
		idx[t.ID] = i
	}
	for _, t := range override.Tools {
		if i, ok := idx[t.ID]; ok {
			// 旧版本保存的条目没有最高权限参数，沿用内置的，否则默认的最高权限不生效
			if len(t.FullArgs) == 0 {
				t.FullArgs = out.Tools[i].FullArgs
			}
			t.Source = SourceModified
			out.Tools[i] = t
		} else {
			t.Source = SourceCustom
			idx[t.ID] = len(out.Tools)
			out.Tools = append(out.Tools, t)
		}
	}
	return out
}

// DefaultUserManifestPath 返回 ~/.autocoder/tools.yaml。
func DefaultUserManifestPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".autocoder", "tools.yaml")
}

// Scanner 执行检测。LookPath / Stat / RunVersion 可替换，便于测试。
type Scanner struct {
	GOOS       string
	LookPath   func(string) (string, error)
	Stat       func(string) (os.FileInfo, error)
	RunVersion func(ctx context.Context, path string, args []string) (string, error)
	RunHelp    func(ctx context.Context, path string, args []string) (string, error)
	Timeout    time.Duration
}

// NewScanner 返回使用真实系统调用的扫描器。
func NewScanner() *Scanner {
	return &Scanner{
		GOOS:       runtime.GOOS,
		LookPath:   exec.LookPath,
		Stat:       os.Stat,
		RunVersion: runVersion,
		RunHelp:    runHelp,
		Timeout:    5 * time.Second,
	}
}

// Scan 并发检测清单中的所有工具，结果按能力评分从高到低排序。
func (s *Scanner) Scan(ctx context.Context, m Manifest) []Detection {
	out := make([]Detection, len(m.Tools))
	var wg sync.WaitGroup
	for i, t := range m.Tools {
		wg.Add(1)
		go func(i int, t Tool) {
			defer wg.Done()
			out[i] = s.detect(ctx, t)
		}(i, t)
	}
	wg.Wait()
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Installed != out[b].Installed {
			return out[a].Installed
		}
		return out[a].Capability > out[b].Capability
	})
	return out
}

func (s *Scanner) detect(ctx context.Context, t Tool) Detection {
	d := Detection{Tool: t}
	for _, c := range t.Commands {
		if p, err := s.LookPath(c); err == nil {
			d.Installed, d.Path = true, p
			break
		}
	}
	if !d.Installed {
		for _, p := range t.Paths[s.GOOS] {
			p = expandHome(p)
			if _, err := s.Stat(p); err == nil {
				d.Installed, d.Path = true, p
				break
			}
		}
	}
	if d.Installed && len(t.VersionArgs) > 0 {
		vctx, cancel := context.WithTimeout(ctx, s.Timeout)
		defer cancel()
		v, err := s.RunVersion(vctx, d.Path, t.VersionArgs)
		if err != nil {
			d.Error = err.Error()
		} else {
			d.Version = ParseVersion(v)
		}
	}
	if d.Installed && len(t.HelpArgs) > 0 && s.RunHelp != nil {
		hctx, cancel := context.WithTimeout(ctx, s.Timeout)
		defer cancel()
		if help, err := s.RunHelp(hctx, d.Path, t.HelpArgs); err == nil {
			d.ArgIssues = UnknownFlags(t, help)
		}
	}
	return d
}

var flagRe = regexp.MustCompile(`(?:^|[\s,\[(|])(--?[A-Za-z][A-Za-z0-9-]*)`)

// UnknownFlags 返回派发时会用到、但帮助文本里没有出现的参数。
// 帮助文本为空或几乎没有参数时不判断，避免误报。
func UnknownFlags(t Tool, help string) []string {
	known := map[string]bool{}
	for _, m := range flagRe.FindAllStringSubmatch(help, -1) {
		known[m[1]] = true
	}
	if len(known) < 3 {
		return nil
	}
	perm := t.AutoArgs
	if t.FullAccess() {
		perm = t.FullArgs
	}
	var out []string
	seen := map[string]bool{}
	for _, set := range [][]string{t.Headless, perm, t.ReadonlyArgs, t.StreamArgs, t.UsageArgs} {
		for _, a := range set {
			if !strings.HasPrefix(a, "-") || a == "-" || a == "--" {
				continue
			}
			name, _, _ := strings.Cut(a, "=")
			if !known[name] && !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	return out
}

func runHelp(ctx context.Context, path string, args []string) (string, error) {
	out, err := exec.CommandContext(ctx, path, args...).CombinedOutput()
	if ctx.Err() != nil {
		return "", fmt.Errorf("获取帮助超时")
	}
	if len(out) == 0 && err != nil {
		return "", err
	}
	return string(out), nil
}

func runVersion(ctx context.Context, path string, args []string) (string, error) {
	out, err := exec.CommandContext(ctx, path, args...).CombinedOutput()
	if ctx.Err() != nil {
		return "", fmt.Errorf("获取版本超时")
	}
	if err != nil {
		return "", fmt.Errorf("获取版本失败: %v", err)
	}
	return string(out), nil
}

var versionRe = regexp.MustCompile(`\d+\.\d+(\.\d+)?([-+.][0-9A-Za-z.]+)?`)

// ParseVersion 从命令输出中提取第一个版本号；找不到时返回首行。
func ParseVersion(out string) string {
	if v := versionRe.FindString(out); v != "" {
		return v
	}
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	if len(line) > 40 {
		line = line[:40]
	}
	return line
}

func expandHome(p string) string {
	if !strings.HasPrefix(p, "~") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~"))
}
