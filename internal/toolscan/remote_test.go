package toolscan

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestUnknownFlags(t *testing.T) {
	help := `Usage: codex exec [OPTIONS] [PROMPT]
  -s, --sandbox <SANDBOX_MODE>
      --dangerously-bypass-approvals-and-sandbox
      --json
      --skip-git-repo-check`
	tool := Tool{Headless: []string{"exec", "{prompt}"}, AutoArgs: []string{"--full-auto"}, UsageArgs: []string{"--json"}, ReadonlyArgs: []string{"--sandbox", "read-only"}}
	if got := UnknownFlags(tool, help); !slices.Equal(got, []string{"--full-auto"}) {
		t.Fatalf("got %v", got)
	}
	tool.FullArgs = []string{"--dangerously-bypass-approvals-and-sandbox"}
	if got := UnknownFlags(tool, help); len(got) != 0 {
		t.Fatalf("最高权限参数都认识，不应报：%v", got)
	}
	if got := UnknownFlags(tool, "no flags here"); got != nil {
		t.Fatalf("帮助文本几乎没有参数时不判断：%v", got)
	}
}

func TestRemoteManifest(t *testing.T) {
	remote := `tools:
  - id: codex
    name: 改名不生效
    commands: [evil]
    headless: ["exec", "{prompt}"]
    full_args: ["--new-flag"]
  - id: newtool
    commands: [x]
    headless: ["{prompt}"]
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(remote)) }))
	defer srv.Close()
	dir := t.TempDir()
	user := filepath.Join(dir, "tools.yaml")
	changed, err := FetchRemote(context.Background(), srv.URL, RemotePath(user))
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if changed, _ := FetchRemote(context.Background(), srv.URL, RemotePath(user)); changed {
		t.Fatal("内容没变不应算更新")
	}
	m, err := LoadManifest(user)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range m.Tools {
		switch tool.ID {
		case "codex":
			if !slices.Equal(tool.FullArgs, []string{"--new-flag"}) || tool.Commands[0] != "codex" || tool.Name != "Codex CLI" || tool.Source != SourceBuiltin {
				t.Fatalf("在线清单只应更新参数：%+v", tool)
			}
		case "newtool":
			t.Fatal("在线清单不应新增工具")
		}
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("tools: []")) }))
	defer bad.Close()
	if _, err := FetchRemote(context.Background(), bad.URL, RemotePath(user)); err == nil {
		t.Fatal("空清单应拒绝")
	}
	if data, _ := os.ReadFile(RemotePath(user)); string(data) != remote {
		t.Fatal("拒绝的清单不应覆盖缓存")
	}
}

// 用户只改过权限：参数跟着在线清单走；用户自己改过的参数保留。
func TestUserEntryFollowsRemote(t *testing.T) {
	b := Builtin()
	var codex Tool
	for _, t := range b.Tools {
		if t.ID == "codex" {
			codex = t
		}
	}
	dir := t.TempDir()
	user := filepath.Join(dir, "tools.yaml")
	u := codex
	u.Access = "standard"
	u.AutoArgs = []string{"--my-own"}
	if err := WriteUserManifest(user, Manifest{Tools: []Tool{u}}); err != nil {
		t.Fatal(err)
	}
	r := codex
	r.FullArgs = []string{"--new-flag"}
	r.AutoArgs = []string{"--remote-auto"}
	data, _ := MarshalManifest(Manifest{Tools: []Tool{r}})
	if err := os.WriteFile(RemotePath(user), data, 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(user)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range m.Tools {
		if tool.ID == "codex" {
			if !slices.Equal(tool.FullArgs, []string{"--new-flag"}) || !slices.Equal(tool.AutoArgs, []string{"--my-own"}) || tool.Access != "standard" || tool.Source != SourceModified {
				t.Fatalf("%+v", tool)
			}
		}
	}
}

func TestForSaveKeepsFollowing(t *testing.T) {
	dir := t.TempDir()
	user := filepath.Join(dir, "tools.yaml")
	m := Builtin()
	r := *find(m, "codex")
	r.FullArgs = []string{"--v1"}
	data, _ := MarshalManifest(Manifest{Tools: []Tool{r}})
	os.WriteFile(RemotePath(user), data, 0o644)
	// 界面拿到的是 --v1，用户只改了权限后保存
	saved := r
	saved.Access = "standard"
	saved = ForSave(saved, user)
	WriteUserManifest(user, Manifest{Tools: []Tool{saved}})
	// 在线清单又更新到 --v2
	r.FullArgs = []string{"--v2"}
	data, _ = MarshalManifest(Manifest{Tools: []Tool{r}})
	os.WriteFile(RemotePath(user), data, 0o644)
	got, _ := LoadManifest(user)
	if c := find(got, "codex"); !slices.Equal(c.FullArgs, []string{"--v2"}) || c.Access != "standard" {
		t.Fatalf("%+v", c)
	}
}
