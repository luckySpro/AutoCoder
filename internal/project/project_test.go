package project

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luckySpro/AutoCoder/internal/gitops"
)

func TestCreateAdoptReload(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	repo := filepath.Join(root, "code", "shop")
	if err := gitops.Init(ctx, repo, "main", "shop"); err != nil {
		t.Fatal(err)
	}
	s, errs := Open(ctx, home)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	p, _, err := s.Create(ctx, CreateRequest{Source: "local", Path: repo, TestCommand: "go test ./..."}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "shop" || p.BaseBranch != "main" || !gitops.BranchExists(ctx, repo, IntegrationBranch) {
		t.Fatalf("%+v", p.Meta)
	}
	// 不碰用户的工作目录
	if _, err := os.Stat(filepath.Join(repo, ".autocoder")); !os.IsNotExist(err) {
		t.Fatal("主目录里不应出现 .autocoder")
	}
	if _, _, err := s.Create(ctx, CreateRequest{Source: "local", Path: repo}, nil); err == nil {
		t.Fatal("重复添加应报错")
	}

	s.Lock()
	a, err := NewTask(p, Task{Title: "Order API"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewTask(p, Task{Title: "界面", DependsOn: []string{a.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != "T1" || b.ID != "T2" || a.Branch != "ac/T1-order-api" || b.Branch != "ac/T2-task" {
		t.Fatalf("%s %s %s %s", a.ID, b.ID, a.Branch, b.Branch)
	}
	a.DependsOn = []string{b.ID}
	if !hasCycle(p.Tasks) {
		t.Fatal("应检测到环")
	}
	a.DependsOn = nil
	if _, err := NewTask(p, Task{Title: "x", DependsOn: []string{"T9"}}); err == nil {
		t.Fatal("依赖不存在应报错")
	}
	_ = AppendReport(p, Report{TaskID: "T1", From: "codex", Status: Running, Summary: "开始了"})
	_ = AppendUsage(p, Usage{ID: "u1", ProjectID: "shop", TaskID: "T1", Tool: "codex", Usage: Tokens{Input: 10, Output: 2}})
	p.GitMu.Lock()
	if err := CommitState(ctx, p, "测试"); err != nil {
		t.Fatal(err)
	}
	p.GitMu.Unlock()
	s.Unlock()

	// 计量不进仓库
	files, _ := gitops.Run(ctx, p.ControlDir(), "ls-files", ".autocoder")
	if strings.Contains(files, "usage/") || !strings.Contains(files, "tasks/T1.yaml") || !strings.Contains(files, "reports/T1.md") {
		t.Fatalf("提交的文件不对：\n%s", files)
	}

	s2, errs := Open(ctx, home)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	p2 := s2.Get("shop")
	if p2 == nil || len(p2.Tasks) != 2 || len(p2.Reports) != 1 || p2.Reports[0].Summary != "开始了" || p2.TestCommand != "go test ./..." {
		t.Fatalf("重新加载后状态丢失：%+v", p2)
	}
	if u := ReadUsage(p2); len(u) != 1 || u[0].Usage.Total() != 12 {
		t.Fatalf("%+v", u)
	}
	v := ToView(p2)
	if v.Tasks[0].Progress != 0 || v.Path != repo {
		t.Fatalf("%+v", v)
	}
}

func TestCreateNewValidates(t *testing.T) {
	ctx := context.Background()
	s, _ := Open(ctx, t.TempDir())
	if _, _, err := s.Create(ctx, CreateRequest{Source: "new", Name: "bad name", Path: filepath.Join(t.TempDir(), "x")}, nil); err == nil {
		t.Fatal("非法名称应报错")
	}
	if _, _, err := s.Create(ctx, CreateRequest{Source: "local", Path: "relative/path"}, nil); err == nil {
		t.Fatal("相对路径应报错")
	}
	if _, _, err := s.Create(ctx, CreateRequest{Source: "local", Path: t.TempDir()}, nil); err == nil {
		t.Fatal("不是仓库应报错")
	}
}
