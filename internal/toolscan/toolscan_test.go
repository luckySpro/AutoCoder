package toolscan

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestDefaultManifestLoads(t *testing.T) {
	m, err := LoadManifest("")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, tool := range m.Tools {
		if tool.ID == "" || tool.Name == "" || len(tool.Commands) == 0 {
			t.Errorf("条目不完整: %+v", tool)
		}
		if seen[tool.ID] {
			t.Errorf("重复 id: %s", tool.ID)
		}
		seen[tool.ID] = true
	}
	for _, id := range []string{"claude-code", "codex", "cursor"} {
		if !seen[id] {
			t.Errorf("默认清单缺少 %s", id)
		}
	}
}

func TestMergeOverridesAndAppends(t *testing.T) {
	base := Manifest{Tools: []Tool{{ID: "a", Capability: 1}, {ID: "b", Capability: 2}}}
	over := Manifest{Tools: []Tool{{ID: "b", Capability: 9}, {ID: "c", Capability: 3}}}
	got := Merge(base, over)
	if len(got.Tools) != 3 || got.Tools[1].Capability != 9 || got.Tools[2].ID != "c" {
		t.Fatalf("合并结果不符: %+v", got.Tools)
	}
	if base.Tools[1].Capability != 2 {
		t.Fatal("Merge 不应修改 base")
	}
}

func TestScan(t *testing.T) {
	s := &Scanner{
		GOOS: "darwin",
		LookPath: func(c string) (string, error) {
			if c == "claude" {
				return "/usr/local/bin/claude", nil
			}
			return "", errors.New("not found")
		},
		Stat: func(p string) (os.FileInfo, error) {
			if p == "/Applications/Cursor.app" {
				return nil, nil
			}
			return nil, os.ErrNotExist
		},
		RunVersion: func(ctx context.Context, path string, args []string) (string, error) {
			return "2.0.14 (Claude Code)\n", nil
		},
		Timeout: time.Second,
	}
	m := Manifest{Tools: []Tool{
		{ID: "codex", Commands: []string{"codex"}, Capability: 90},
		{ID: "cursor", Kind: KindIDE, Commands: []string{"cursor"}, Paths: map[string][]string{"darwin": {"/Applications/Cursor.app"}}, VersionArgs: []string{"--version"}, Capability: 80},
		{ID: "claude-code", Kind: KindCLI, Commands: []string{"claude"}, VersionArgs: []string{"--version"}, Headless: []string{"-p", "{prompt}"}, Capability: 95},
	}}
	got := s.Scan(context.Background(), m)
	if got[0].ID != "claude-code" || !got[0].Installed || got[0].Version != "2.0.14" || !got[0].CanRunHeadless() {
		t.Errorf("claude-code 检测不符: %+v", got[0])
	}
	if got[1].ID != "cursor" || !got[1].Installed || got[1].Version != "" || got[1].CanRunHeadless() {
		t.Errorf("cursor 检测不符: %+v", got[1])
	}
	if got[2].ID != "codex" || got[2].Installed {
		t.Errorf("codex 应未安装: %+v", got[2])
	}
}

func TestParseVersion(t *testing.T) {
	cases := map[string]string{
		"codex-cli 0.46.0":        "0.46.0",
		"1.7.38\nabc123\nx64":     "1.7.38",
		"aider 0.86.1.dev":        "0.86.1.dev",
		"Gemini CLI v0.9.0-beta1": "0.9.0-beta1",
		"unknown":                 "unknown",
	}
	for in, want := range cases {
		if got := ParseVersion(in); got != want {
			t.Errorf("ParseVersion(%q)=%q, want %q", in, got, want)
		}
	}
}
