package gitops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflow(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	repo := filepath.Join(root, "demo")
	if err := Init(ctx, repo, "main", "demo"); err != nil {
		t.Fatal(err)
	}
	if !IsRepo(ctx, repo) || !HasCommits(ctx, repo) || CurrentBranch(ctx, repo) != "main" {
		t.Fatal("初始化后的仓库状态不对")
	}
	if err := CreateBranch(ctx, repo, "ac/integration", "main"); err != nil {
		t.Fatal(err)
	}
	integ := filepath.Join(root, "wt", "_integration")
	if err := EnsureWorktree(ctx, repo, integ, "ac/integration", "main"); err != nil {
		t.Fatal(err)
	}
	// 再调用一次应当是幂等的
	if err := EnsureWorktree(ctx, repo, integ, "ac/integration", "main"); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(root, "wt", "T1")
	if err := EnsureWorktree(ctx, repo, wt, "ac/T1-a", "ac/integration"); err != nil {
		t.Fatal(err)
	}

	write(t, wt, "a.txt", "1\n2\n3\n")
	write(t, wt, "README.md", "# demo\nmore\n")
	write(t, wt, "secret/x.txt", "x\n")
	ch, err := Changes(ctx, wt)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Change{}
	for _, c := range ch {
		got[c.Path] = c
	}
	if got["a.txt"].Status != "A" || got["a.txt"].Added != 3 {
		t.Errorf("a.txt = %+v", got["a.txt"])
	}
	if got["README.md"].Status != "M" || got["README.md"].Added != 1 {
		t.Errorf("README.md = %+v", got["README.md"])
	}
	if err := Revert(ctx, wt, "HEAD", []string{"secret/x.txt", "README.md"}); err != nil {
		t.Fatal(err)
	}
	ch, _ = Changes(ctx, wt)
	if len(ch) != 1 || ch[0].Path != "a.txt" {
		t.Fatalf("撤回后仍有改动：%+v", ch)
	}
	sha, err := CommitAll(ctx, wt, "feat: a")
	if err != nil || sha == "" {
		t.Fatal(sha, err)
	}
	if n := CountCommits(ctx, repo, "ac/integration", "ac/T1-a"); n != 1 {
		t.Errorf("CountCommits = %d", n)
	}
	if sha, err := CommitAll(ctx, wt, "空提交"); err != nil || sha != "" {
		t.Errorf("没有改动时不应提交：%q %v", sha, err)
	}

	before, _ := RevParse(ctx, integ, "HEAD")
	if _, err := Merge(ctx, integ, "ac/T1-a", "merge T1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(integ, "a.txt")); err != nil {
		t.Fatal("合并后集成分支应有 a.txt")
	}

	// 制造冲突
	wt2 := filepath.Join(root, "wt", "T2")
	if err := EnsureWorktree(ctx, repo, wt2, "ac/T2-b", before); err != nil {
		t.Fatal(err)
	}
	write(t, wt2, "a.txt", "other\n")
	if _, err := CommitAll(ctx, wt2, "feat: b"); err != nil {
		t.Fatal(err)
	}
	_, err = Merge(ctx, integ, "ac/T2-b", "merge T2")
	var ce *ConflictError
	if !errors.As(err, &ce) || len(ce.Files) != 1 || ce.Files[0] != "a.txt" {
		t.Fatalf("应当报告 a.txt 冲突：%v", err)
	}
	if !IsClean(ctx, integ) {
		t.Fatal("冲突后应撤销合并")
	}
	if err := ResetHard(ctx, integ, before); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(integ, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("回退后 a.txt 应当不存在")
	}
	if err := RemoveWorktree(ctx, repo, wt2); err != nil {
		t.Fatal(err)
	}
}
