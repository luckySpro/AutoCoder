// Package gitops 封装 AutoCoder 用到的 git 操作：建仓、分支、worktree、改动统计、提交与合并。
// 直接调用本机 git 命令行，行为与用户手动操作一致，也能复用用户的凭据配置。
package gitops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrConflict 表示合并出现冲突（已自动撤销）。
var ErrConflict = errors.New("合并冲突")

// ConflictError 带上冲突文件列表。
type ConflictError struct{ Files []string }

func (e *ConflictError) Error() string {
	return "合并冲突：" + strings.Join(e.Files, "、")
}

func (e *ConflictError) Unwrap() error { return ErrConflict }

// Run 在 dir 中执行 git，返回去掉首尾空白的标准输出；失败时错误里带上标准错误。
func Run(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := run(ctx, dir, args...)
	return strings.TrimSpace(out), err
}

func run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_EDITOR=true", "GIT_MERGE_AUTOEDIT=no", "LC_ALL=C")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		return out.String(), fmt.Errorf("git %s: %s", strings.Join(args[:min(len(args), 2)], " "), firstLine(msg, err))
	}
	return out.String(), nil
}

func firstLine(msg string, err error) string {
	if msg == "" {
		return err.Error()
	}
	if len(msg) > 600 {
		msg = msg[:600] + "…"
	}
	return msg
}

// IsRepo 判断目录是否在 git 仓库中。
func IsRepo(ctx context.Context, dir string) bool {
	out, err := Run(ctx, dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && out == "true"
}

// TopLevel 返回仓库根目录。
func TopLevel(ctx context.Context, dir string) (string, error) {
	return Run(ctx, dir, "rev-parse", "--show-toplevel")
}

// HasCommits 判断仓库是否已有提交。
func HasCommits(ctx context.Context, dir string) bool {
	_, err := Run(ctx, dir, "rev-parse", "--verify", "-q", "HEAD")
	return err == nil
}

// CurrentBranch 返回当前检出的分支；分离头指针时返回空。
func CurrentBranch(ctx context.Context, dir string) string {
	out, _ := Run(ctx, dir, "symbolic-ref", "--short", "-q", "HEAD")
	return out
}

// BranchExists 判断本地分支是否存在。
func BranchExists(ctx context.Context, dir, branch string) bool {
	_, err := Run(ctx, dir, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// RefExists 判断任意引用是否存在。
func RefExists(ctx context.Context, dir, ref string) bool {
	_, err := Run(ctx, dir, "rev-parse", "--verify", "-q", ref+"^{commit}")
	return err == nil
}

// RevParse 解析提交号。
func RevParse(ctx context.Context, dir, ref string) (string, error) {
	return Run(ctx, dir, "rev-parse", "--verify", ref+"^{commit}")
}

// CreateBranch 从 from 创建分支（已存在时不动）。
func CreateBranch(ctx context.Context, dir, branch, from string) error {
	if BranchExists(ctx, dir, branch) {
		return nil
	}
	_, err := Run(ctx, dir, "branch", branch, from)
	return err
}

// Init 新建仓库并生成第一次提交。
func Init(ctx context.Context, dir, branch, name string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if _, err := Run(ctx, dir, "init", "-b", branch); err != nil {
		return err
	}
	readme := filepath.Join(dir, "README.md")
	if _, err := os.Stat(readme); os.IsNotExist(err) {
		if err := os.WriteFile(readme, []byte("# "+name+"\n"), 0o644); err != nil {
			return err
		}
	}
	_, err := CommitAll(ctx, dir, "chore: 初始化仓库")
	return err
}

// Clone 克隆仓库到 dest。
func Clone(ctx context.Context, url, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	_, err := Run(ctx, filepath.Dir(dest), "clone", url, dest)
	return err
}

// Worktree 是 `git worktree list` 中的一项。
type Worktree struct {
	Path   string
	Branch string
}

// Worktrees 列出仓库的全部 worktree。
func Worktrees(ctx context.Context, dir string) ([]Worktree, error) {
	out, err := Run(ctx, dir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var list []Worktree
	var cur Worktree
	for _, line := range strings.Split(out+"\n", "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			cur = Worktree{Path: strings.TrimPrefix(line, "worktree ")}
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "" && cur.Path != "":
			list = append(list, cur)
			cur = Worktree{}
		}
	}
	return list, nil
}

// EnsureWorktree 保证 path 处有一个检出 branch 的 worktree；分支不存在时从 base 创建。
func EnsureWorktree(ctx context.Context, repo, path, branch, base string) error {
	if wts, err := Worktrees(ctx, repo); err == nil {
		for _, w := range wts {
			if samePath(w.Path, path) {
				if w.Branch != branch {
					return fmt.Errorf("目录 %s 已被分支 %s 占用", path, w.Branch)
				}
				return nil
			}
		}
	}
	_, _ = Run(ctx, repo, "worktree", "prune")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if BranchExists(ctx, repo, branch) {
		_, err := Run(ctx, repo, "worktree", "add", path, branch)
		return err
	}
	_, err := Run(ctx, repo, "worktree", "add", "-b", branch, path, base)
	return err
}

// SyncDetached 准备一个只读用途的分离 worktree，内容与 ref 完全一致（规划、评审用）。
// 已存在时丢弃里面的任何改动再切到 ref。
func SyncDetached(ctx context.Context, repo, path, ref string) error {
	if _, err := os.Stat(path); err == nil {
		if _, err := Run(ctx, path, "checkout", "--detach", "--force", ref); err == nil {
			_, err = Run(ctx, path, "clean", "-fdq")
			return err
		}
		_ = RemoveWorktree(ctx, repo, path)
		_ = os.RemoveAll(path)
	}
	_, _ = Run(ctx, repo, "worktree", "prune")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	_, err := Run(ctx, repo, "worktree", "add", "--detach", path, ref)
	return err
}

// Discard 丢弃工作区的全部未提交改动（含未跟踪文件）。
func Discard(ctx context.Context, dir string) error {
	if _, err := Run(ctx, dir, "reset", "--hard", "-q", "HEAD"); err != nil {
		return err
	}
	_, err := Run(ctx, dir, "clean", "-fdq")
	return err
}

// RemoveWorktree 删除 worktree（分支保留）。
func RemoveWorktree(ctx context.Context, repo, path string) error {
	_, err := Run(ctx, repo, "worktree", "remove", "--force", path)
	return err
}

func samePath(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 == nil && err2 == nil {
		return ra == rb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// Change 是一个改动的文件。
type Change struct {
	Path    string `json:"path"`
	Status  string `json:"status"` // A 新增 / M 修改 / D 删除 / R 重命名
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
}

// Changes 统计工作区相对 HEAD 的全部改动（含未跟踪文件），不修改暂存区。
func Changes(ctx context.Context, dir string) ([]Change, error) {
	out, err := run(ctx, dir, "status", "--porcelain=v1", "-uall", "--no-renames")
	if err != nil {
		return nil, err
	}
	byPath := map[string]*Change{}
	var list []*Change
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		code, p := line[:2], unquote(line[3:])
		st := "M"
		switch {
		case code == "??" || strings.Contains(code, "A"):
			st = "A"
		case strings.Contains(code, "D"):
			st = "D"
		}
		c := &Change{Path: p, Status: st}
		byPath[p] = c
		list = append(list, c)
	}
	if HasCommits(ctx, dir) {
		if ns, err := Run(ctx, dir, "diff", "--numstat", "--no-renames", "HEAD"); err == nil {
			for _, line := range strings.Split(ns, "\n") {
				f := strings.SplitN(line, "\t", 3)
				if len(f) == 3 {
					if c := byPath[unquote(f[2])]; c != nil {
						c.Added, _ = strconv.Atoi(f[0])
						c.Removed, _ = strconv.Atoi(f[1])
					}
				}
			}
		}
	}
	res := make([]Change, 0, len(list))
	for _, c := range list {
		if c.Status == "A" && c.Added == 0 {
			c.Added = countLines(filepath.Join(dir, c.Path))
		}
		res = append(res, *c)
	}
	return res, nil
}

func countLines(path string) int {
	data, err := os.ReadFile(path)
	if err != nil || bytes.IndexByte(data, 0) >= 0 {
		return 0
	}
	n := bytes.Count(data, []byte("\n"))
	if len(data) > 0 && data[len(data)-1] != '\n' {
		n++
	}
	return n
}

func unquote(p string) string {
	if strings.HasPrefix(p, `"`) {
		if s, err := strconv.Unquote(p); err == nil {
			return s
		}
	}
	return p
}

// Revert 把指定文件恢复到 ref 中的状态；ref 中不存在的文件直接删除。
func Revert(ctx context.Context, dir, ref string, paths []string) error {
	for _, p := range paths {
		if _, err := Run(ctx, dir, "cat-file", "-e", ref+":"+p); err == nil {
			if _, err := Run(ctx, dir, "checkout", ref, "--", p); err != nil {
				return err
			}
			continue
		}
		_, _ = Run(ctx, dir, "rm", "--cached", "-q", "-f", "--ignore-unmatch", "--", p)
		if err := os.Remove(filepath.Join(dir, p)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// DiffFrom 暂存工作区全部改动，返回相对 ref 的改动（含工具自己做的提交）。
func DiffFrom(ctx context.Context, dir, ref string) ([]Change, error) {
	if _, err := Run(ctx, dir, "add", "-A"); err != nil {
		return nil, err
	}
	ns, err := run(ctx, dir, "diff", "--cached", "--numstat", "--no-renames", ref)
	if err != nil {
		return nil, err
	}
	st, err := run(ctx, dir, "diff", "--cached", "--name-status", "--no-renames", ref)
	if err != nil {
		return nil, err
	}
	status := map[string]string{}
	for _, l := range strings.Split(st, "\n") {
		if f := strings.SplitN(l, "\t", 2); len(f) == 2 {
			status[unquote(f[1])] = f[0][:1]
		}
	}
	var out []Change
	for _, l := range strings.Split(ns, "\n") {
		f := strings.SplitN(l, "\t", 3)
		if len(f) != 3 {
			continue
		}
		c := Change{Path: unquote(f[2]), Status: status[unquote(f[2])]}
		c.Added, _ = strconv.Atoi(f[0])
		c.Removed, _ = strconv.Atoi(f[1])
		out = append(out, c)
	}
	return out, nil
}

// ChangedFiles 返回 a...b（自共同祖先起 b 一侧）改动过的文件。
func ChangedFiles(ctx context.Context, dir, a, b string) []string {
	out, err := Run(ctx, dir, "diff", "--name-only", a+"..."+b)
	if err != nil || out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// MergeKeep 把 branch 合并进当前分支；有冲突时保留冲突标记（交给工具解决），返回冲突文件。
func MergeKeep(ctx context.Context, dir, branch, msg string) ([]string, error) {
	args := identityArgs(ctx, dir)
	args = append(args, "merge", "--no-ff", "--no-edit", "-m", msg, branch)
	if _, err := Run(ctx, dir, args...); err != nil {
		files, _ := Run(ctx, dir, "diff", "--name-only", "--diff-filter=U")
		if files != "" {
			return strings.Split(files, "\n"), nil
		}
		_, _ = Run(ctx, dir, "merge", "--abort")
		return nil, err
	}
	return nil, nil
}

// Merging 判断是否有未完成的合并。
func Merging(ctx context.Context, dir string) bool {
	return RefExists(ctx, dir, "MERGE_HEAD")
}

// CommitAll 暂存全部改动并提交；没有改动时返回空提交号且不报错。
// 本机没有配置 git 身份时，以 AutoCoder 的身份提交。
func CommitAll(ctx context.Context, dir, msg string, paths ...string) (string, error) {
	add := []string{"add", "-A"}
	if len(paths) > 0 {
		add = append(add, "--")
		add = append(add, paths...)
	}
	if _, err := Run(ctx, dir, add...); err != nil {
		return "", err
	}
	if _, err := Run(ctx, dir, "diff", "--cached", "--quiet"); err == nil && HasCommits(ctx, dir) {
		return "", nil
	}
	args := identityArgs(ctx, dir)
	args = append(args, "commit", "-q", "--no-verify", "-m", msg)
	if _, err := Run(ctx, dir, args...); err != nil {
		return "", err
	}
	return RevParse(ctx, dir, "HEAD")
}

func identityArgs(ctx context.Context, dir string) []string {
	var args []string
	if v, _ := Run(ctx, dir, "config", "user.name"); v == "" {
		args = append(args, "-c", "user.name=AutoCoder")
	}
	if v, _ := Run(ctx, dir, "config", "user.email"); v == "" {
		args = append(args, "-c", "user.email=autocoder@localhost")
	}
	return args
}

// Merge 把 branch 以 --no-ff 合并到当前分支。有冲突时撤销合并并返回 *ConflictError。
func Merge(ctx context.Context, dir, branch, msg string) (string, error) {
	args := identityArgs(ctx, dir)
	args = append(args, "merge", "--no-ff", "--no-edit", "-m", msg, branch)
	if _, err := Run(ctx, dir, args...); err != nil {
		files, _ := Run(ctx, dir, "diff", "--name-only", "--diff-filter=U")
		if files != "" {
			_, _ = Run(ctx, dir, "merge", "--abort")
			return "", &ConflictError{Files: strings.Split(files, "\n")}
		}
		_, _ = Run(ctx, dir, "merge", "--abort")
		return "", err
	}
	return RevParse(ctx, dir, "HEAD")
}

// ResetHard 把当前分支退回到 sha（只用于编排器独占的分支）。
func ResetHard(ctx context.Context, dir, sha string) error {
	_, err := Run(ctx, dir, "reset", "-q", "--hard", sha)
	return err
}

// CountCommits 返回 base..head 之间的提交数。
func CountCommits(ctx context.Context, dir, base, head string) int {
	out, err := Run(ctx, dir, "rev-list", "--count", base+".."+head)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(out)
	return n
}

// IsClean 判断工作区是否没有未提交的改动（忽略未跟踪文件）。
func IsClean(ctx context.Context, dir string) bool {
	out, err := Run(ctx, dir, "status", "--porcelain", "-uno")
	return err == nil && out == ""
}

// IsAncestor 判断 a 是否是 b 的祖先。
func IsAncestor(ctx context.Context, dir, a, b string) bool {
	_, err := Run(ctx, dir, "merge-base", "--is-ancestor", a, b)
	return err == nil
}
