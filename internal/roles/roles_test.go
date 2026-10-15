package roles

import (
	"reflect"
	"testing"

	"github.com/luckySpro/AutoCoder/internal/toolscan"
)

func tool(id string, cap int, kind toolscan.Kind, strengths ...string) toolscan.Detection {
	d := toolscan.Detection{Tool: toolscan.Tool{ID: id, Name: id, Kind: kind, Capability: cap, Strengths: strengths}, Installed: true}
	if kind == toolscan.KindCLI {
		d.Headless = []string{"-p", "{prompt}"}
	}
	return d
}

func rolesOf(as []Assignment) map[string][]Role {
	m := map[string][]Role{}
	for _, a := range as {
		m[a.ToolID] = a.Roles
	}
	return m
}

func TestNoTools(t *testing.T) {
	if got := Suggest(nil); got != nil {
		t.Fatalf("want nil, got %+v", got)
	}
}

func TestSingleToolTakesAllRoles(t *testing.T) {
	got := Suggest([]toolscan.Detection{tool("claude-code", 95, toolscan.KindCLI), {Tool: toolscan.Tool{ID: "codex"}}})
	if len(got) != 1 || len(got[0].Roles) != len(All) {
		t.Fatalf("单工具应承担全部角色: %+v", got)
	}
}

func TestClaudeCodexCursor(t *testing.T) {
	got := rolesOf(Suggest([]toolscan.Detection{
		tool("cursor", 80, toolscan.KindIDE, "frontend", "coding"),
		tool("codex", 90, toolscan.KindCLI, "coding", "architecture", "testing"),
		tool("claude-code", 95, toolscan.KindCLI, "architecture", "coding", "review", "testing"),
	}))
	want := map[string][]Role{
		"claude-code": {Lead, Reviewer},    // 评审回落给 lead
		"codex":       {Developer, Tester}, // 测试由最匹配的 CLI 兼任
		"cursor":      {Frontend},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestHeadlessPreferredForLead(t *testing.T) {
	got := Suggest([]toolscan.Detection{
		tool("ide", 99, toolscan.KindIDE),
		tool("cli", 50, toolscan.KindCLI),
	})
	if got[0].ToolID != "cli" || got[0].Roles[0] != Lead || !got[1].Manual {
		t.Fatalf("lead 应为可无人值守工具: %+v", got)
	}
}

func TestExtraToolsBecomeDevelopers(t *testing.T) {
	ds := []toolscan.Detection{tool("lead", 99, toolscan.KindCLI)}
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		ds = append(ds, tool(id, 50, toolscan.KindCLI))
	}
	got := Suggest(ds)
	if got[len(got)-1].Roles[0] != Developer {
		t.Fatalf("多余的工具应为开发: %+v", got)
	}
}
