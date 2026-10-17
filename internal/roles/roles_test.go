package roles

import (
	"reflect"
	"testing"

	"github.com/luckySpro/AutoCoder/internal/toolscan"
)

func tool(id string, cap int, strengths ...string) toolscan.Detection {
	return toolscan.Detection{Tool: toolscan.Tool{ID: id, Name: id, Capability: cap, Strengths: strengths, Headless: []string{"-p", "{prompt}"}}, Installed: true}
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
	got := Suggest([]toolscan.Detection{tool("claude-code", 95), {Tool: toolscan.Tool{ID: "codex"}}})
	if len(got) != 1 || len(got[0].Roles) != len(All) {
		t.Fatalf("单工具应承担全部角色: %+v", got)
	}
}

func TestClaudeCodexCursor(t *testing.T) {
	got := rolesOf(Suggest([]toolscan.Detection{
		tool("cursor-agent", 85, "coding", "frontend"),
		tool("codex", 90, "coding", "architecture", "testing"),
		tool("claude-code", 95, "architecture", "coding", "review", "testing"),
	}))
	want := map[string][]Role{
		"claude-code":  {Lead, Reviewer},    // 评审回落给 lead
		"codex":        {Developer, Tester}, // 测试由最匹配的 CLI 兼任
		"cursor-agent": {Frontend},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestUnschedulableIgnored(t *testing.T) {
	noTemplate := tool("no-template", 99)
	noTemplate.Headless = nil
	got := Suggest([]toolscan.Detection{noTemplate, tool("cli", 50)})
	if len(got) != 1 || got[0].ToolID != "cli" || len(got[0].Roles) != len(All) {
		t.Fatalf("没有调用模板的工具不应参与分配: %+v", got)
	}
}

func TestExtraToolsBecomeDevelopers(t *testing.T) {
	ds := []toolscan.Detection{tool("lead", 99)}
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		ds = append(ds, tool(id, 50))
	}
	got := Suggest(ds)
	if got[len(got)-1].Roles[0] != Developer {
		t.Fatalf("多余的工具应为开发: %+v", got)
	}
}
