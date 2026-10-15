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
	Headless    []string            `yaml:"headless" json:"headless,omitempty"`
	UsageArgs   []string            `yaml:"usage_args" json:"usageArgs,omitempty"`
	Capability  int                 `yaml:"capability" json:"capability"`
	Strengths   []string            `yaml:"strengths" json:"strengths"`
	Disabled    bool                `yaml:"disabled,omitempty" json:"disabled"`
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
}

// Schedulable 表示该工具能否被编排器派发任务（已安装且有非交互调用模板）。
func (d Detection) Schedulable() bool {
	return d.Installed && len(d.Headless) > 0
}

// ExactUsage 表示该工具能输出结构化的 token 用量；否则按字符数估算。
func (d Detection) ExactUsage() bool {
	return len(d.UsageArgs) > 0
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
func LoadManifest(userPath string) (Manifest, error) {
	user, err := ReadUserManifest(userPath)
	if err != nil {
		return Builtin(), err
	}
	return Merge(Builtin(), user), nil
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
	Timeout    time.Duration
}

// NewScanner 返回使用真实系统调用的扫描器。
func NewScanner() *Scanner {
	return &Scanner{
		GOOS:       runtime.GOOS,
		LookPath:   exec.LookPath,
		Stat:       os.Stat,
		RunVersion: runVersion,
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
	return d
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
