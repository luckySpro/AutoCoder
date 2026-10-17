package gitinfo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fake(outputs map[string]string) Runner {
	return func(_ context.Context, name string, args ...string) (string, error) {
		key := name + " " + strings.Join(args, " ")
		for k, v := range outputs {
			if strings.HasPrefix(key, k) {
				return v, nil
			}
		}
		return "", errors.New("exit 1")
	}
}

func TestIdentity(t *testing.T) {
	c := &Client{
		Run: fake(map[string]string{
			"git --version":                        "git version 2.47.1\n",
			"git config --global --get user.name":  "Lucky\n",
			"git config --global --get user.email": "lucky@example.com\n",
			"gh auth status":                       "github.com\n  ✓ Logged in to github.com account luckySpro (keyring)\n",
		}),
		LookPath: func(string) (string, error) { return "/usr/bin/gh", nil },
	}
	id := c.Identity(context.Background())
	if id.GitName != "Lucky" || id.GitEmail != "lucky@example.com" || id.GitVersion != "2.47.1" {
		t.Errorf("git 身份不符: %+v", id)
	}
	if !id.GhLoggedIn || id.GhLogin != "luckySpro" || id.GhHost != "github.com" {
		t.Errorf("gh 登录信息不符: %+v", id)
	}
}

func TestIdentityWithoutGh(t *testing.T) {
	c := &Client{Run: fake(nil), LookPath: func(string) (string, error) { return "", errors.New("no") }}
	if id := c.Identity(context.Background()); id.GhInstalled || id.GhLoggedIn {
		t.Errorf("未安装 gh 时不应显示登录: %+v", id)
	}
	if _, err := c.Repos(context.Background(), 10); !errors.Is(err, ErrNoGh) {
		t.Errorf("want ErrNoGh, got %v", err)
	}
}

func TestRepos(t *testing.T) {
	c := &Client{
		Run: fake(map[string]string{"gh repo list": `[
			{"nameWithOwner":"luckySpro/old","isPrivate":false,"updatedAt":"2025-01-01T00:00:00Z","url":"https://github.com/luckySpro/old","defaultBranchRef":{"name":"master"}},
			{"nameWithOwner":"luckySpro/AutoCoder","description":"协同","isPrivate":true,"updatedAt":"2026-10-15T00:00:00Z","url":"https://github.com/luckySpro/AutoCoder","defaultBranchRef":{"name":"main"}}]`}),
		LookPath: func(string) (string, error) { return "/usr/bin/gh", nil },
	}
	repos, err := c.Repos(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 2 || repos[0].NameWithOwner != "luckySpro/AutoCoder" || repos[0].DefaultBranch != "main" || !repos[0].IsPrivate {
		t.Fatalf("仓库列表不符: %+v", repos)
	}
}

func TestLocalRepos(t *testing.T) {
	home := t.TempDir()
	for _, p := range []string{"code/a/.git", "code/group/b/.git", "projects/c/.git", "code/not-a-repo"} {
		if err := os.MkdirAll(filepath.Join(home, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	c := &Client{Run: fake(map[string]string{"git -C": "main\n"}), Home: home}
	got := c.LocalRepos(context.Background())
	names := map[string]bool{}
	for _, r := range got {
		names[r.Name] = true
	}
	if len(got) != 3 || !names["a"] || !names["b"] || !names["c"] {
		t.Fatalf("本地仓库不符: %+v", got)
	}
}
