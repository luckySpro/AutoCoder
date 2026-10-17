// Package gitinfo 读取本机的 git 身份、GitHub CLI 登录状态、远程仓库列表与本地仓库，
// 供"添加项目"界面像 Claude Code 一样直接从账号中挑选仓库。
package gitinfo

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Runner 执行外部命令并返回标准输出；测试时可替换。
type Runner func(ctx context.Context, name string, args ...string) (string, error)

// ExecRunner 使用真实进程。
func ExecRunner(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// Identity 是本机 git 与 GitHub 账号信息。
type Identity struct {
	GitName     string `json:"gitName"`
	GitEmail    string `json:"gitEmail"`
	GitVersion  string `json:"gitVersion"`
	GhInstalled bool   `json:"ghInstalled"`
	GhLoggedIn  bool   `json:"ghLoggedIn"`
	GhLogin     string `json:"ghLogin,omitempty"`
	GhHost      string `json:"ghHost,omitempty"`
}

// Repo 是一个可添加为项目的远程仓库。
type Repo struct {
	NameWithOwner string    `json:"nameWithOwner"`
	Description   string    `json:"description"`
	IsPrivate     bool      `json:"isPrivate"`
	UpdatedAt     time.Time `json:"updatedAt"`
	URL           string    `json:"url"`
	DefaultBranch string    `json:"defaultBranch"`
}

// LocalRepo 是本机已有的 git 仓库。
type LocalRepo struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Remote string `json:"remote,omitempty"`
	Branch string `json:"branch,omitempty"`
}

// Client 汇总读取逻辑。
type Client struct {
	Run      Runner
	LookPath func(string) (string, error)
	Home     string
}

// New 返回使用真实系统的 Client。
func New() *Client {
	home, _ := os.UserHomeDir()
	return &Client{Run: ExecRunner, LookPath: exec.LookPath, Home: home}
}

var (
	ghLoginRe = regexp.MustCompile(`Logged in to (\S+) (?:account|as) ([A-Za-z0-9-]+)`)
	versionRe = regexp.MustCompile(`\d+\.\d+(\.\d+)?`)
)

// Identity 读取 git 全局身份与 gh 登录状态。
func (c *Client) Identity(ctx context.Context) Identity {
	var id Identity
	if out, err := c.Run(ctx, "git", "--version"); err == nil {
		id.GitVersion = versionRe.FindString(out)
	}
	if out, err := c.Run(ctx, "git", "config", "--global", "--get", "user.name"); err == nil {
		id.GitName = strings.TrimSpace(out)
	}
	if out, err := c.Run(ctx, "git", "config", "--global", "--get", "user.email"); err == nil {
		id.GitEmail = strings.TrimSpace(out)
	}
	if _, err := c.LookPath("gh"); err != nil {
		return id
	}
	id.GhInstalled = true
	// gh auth status 在未登录时返回非零，但输出仍有用。
	out, _ := c.Run(ctx, "gh", "auth", "status", "--hostname", "github.com")
	if m := ghLoginRe.FindStringSubmatch(out); m != nil {
		id.GhLoggedIn, id.GhHost, id.GhLogin = true, m[1], m[2]
	}
	return id
}

// ErrNoGh 表示无法通过 gh 读取远程仓库。
var ErrNoGh = errors.New("未安装或未登录 GitHub CLI（gh）")

// Repos 列出当前账号可访问的仓库（按更新时间倒序）。
func (c *Client) Repos(ctx context.Context, limit int) ([]Repo, error) {
	if _, err := c.LookPath("gh"); err != nil {
		return nil, ErrNoGh
	}
	if limit <= 0 {
		limit = 100
	}
	out, err := c.Run(ctx, "gh", "repo", "list", "--limit", strconv.Itoa(limit),
		"--json", "nameWithOwner,description,isPrivate,updatedAt,url,defaultBranchRef")
	if err != nil {
		return nil, ErrNoGh
	}
	var raw []struct {
		Repo
		DefaultBranchRef struct {
			Name string `json:"name"`
		} `json:"defaultBranchRef"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, err
	}
	repos := make([]Repo, len(raw))
	for i, r := range raw {
		repos[i] = r.Repo
		repos[i].DefaultBranch = r.DefaultBranchRef.Name
	}
	sort.SliceStable(repos, func(a, b int) bool { return repos[a].UpdatedAt.After(repos[b].UpdatedAt) })
	return repos, nil
}

// LocalDirs 是查找本地仓库的常见目录（相对用户主目录）。
var LocalDirs = []string{"code", "Code", "projects", "Projects", "src", "workspace", "dev", "Developer", "go/src", "Documents/GitHub", "GitHub"}

// LocalRepos 在常见目录下两层内查找 git 仓库。
func (c *Client) LocalRepos(ctx context.Context) []LocalRepo {
	seen := map[string]bool{}
	var out []LocalRepo
	for _, d := range LocalDirs {
		root := filepath.Join(c.Home, d)
		for _, pattern := range []string{"*/.git", "*/*/.git"} {
			matches, _ := filepath.Glob(filepath.Join(root, pattern))
			for _, g := range matches {
				p := filepath.Dir(g)
				if seen[p] {
					continue
				}
				seen[p] = true
				r := LocalRepo{Name: filepath.Base(p), Path: p}
				if s, err := c.Run(ctx, "git", "-C", p, "remote", "get-url", "origin"); err == nil {
					r.Remote = strings.TrimSpace(s)
				}
				if s, err := c.Run(ctx, "git", "-C", p, "branch", "--show-current"); err == nil {
					r.Branch = strings.TrimSpace(s)
				}
				out = append(out, r)
				if len(out) >= 200 {
					return out
				}
			}
		}
	}
	return out
}
