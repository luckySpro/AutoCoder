package dispatch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/luckySpro/AutoCoder/internal/gitops"
	"github.com/luckySpro/AutoCoder/internal/project"
)

// ErrDirty 表示你的主目录有未提交的改动，交付在等你处理。
var ErrDirty = errors.New("主目录有未提交的改动")

// deliverable 判断项目是否可以交付：有任务、全部已合并或取消，且集成分支领先主分支。
func deliverable(p *project.Project) (bool, string) {
	merged := 0
	for _, t := range p.Tasks {
		switch t.Status {
		case project.Merged:
			if t.UpdatedAt.After(p.DeliveredAt) {
				merged++
			}
		case project.Cancelled:
		default:
			return false, "还有任务没有合并"
		}
	}
	if merged == 0 {
		return false, "没有新合并的任务"
	}
	return true, ""
}

// Delivery 是一次交付的结果。
type Delivery struct {
	Commit string `json:"commit"`
	Note   string `json:"note"`   // 交付说明文件（在仓库中的路径）
	Status string `json:"status"` // merged | waiting
	Detail string `json:"detail,omitempty"`
}

// Deliver 把集成分支合并到主分支（DESIGN §6.6 最终交付）。
// 合并在临时 worktree 里完成，不碰你的工作目录；你的主目录检出主分支且干净时快进过去。
func (e *Engine) Deliver(ctx context.Context, projectID string) (Delivery, error) {
	e.Store.Lock()
	p := e.Store.Get(projectID)
	if p == nil {
		e.Store.Unlock()
		return Delivery{}, errors.New("项目不存在")
	}
	ok, why := deliverable(p)
	e.Store.Unlock()
	if !ok {
		return Delivery{}, errors.New(why)
	}

	p.GitMu.Lock()
	defer p.GitMu.Unlock()
	ctl := p.ControlDir()
	base := p.BaseBranch
	if gitops.CountCommits(ctx, ctl, base, "HEAD") == 0 || onlyState(ctx, ctl, base) {
		return Delivery{}, errors.New("集成分支上没有需要交付的新内容")
	}

	// 1. 主分支在此期间有新提交：先合入集成分支再测
	if !gitops.IsAncestor(ctx, ctl, base, "HEAD") {
		before, _ := gitops.RevParse(ctx, ctl, "HEAD")
		if _, err := gitops.Merge(ctx, ctl, base, "merge: 合入 "+base+" 的新提交"); err != nil {
			return Delivery{}, fmt.Errorf("%s 与集成分支冲突，需要先处理：%w", base, err)
		}
		if p.TestCommand != "" {
			if ok, out := e.quietTest(ctx, p, ctl); !ok {
				_ = gitops.ResetHard(ctx, ctl, before)
				e.Store.Lock()
				_ = project.Flush(p)
				e.Store.Unlock()
				return Delivery{}, fmt.Errorf("合入 %s 的新提交后测试失败：%s", base, lastLines(out, 20))
			}
		}
	}
	// 2. 完整测试
	if p.TestCommand != "" {
		if ok, out := e.quietTest(ctx, p, ctl); !ok {
			return Delivery{}, fmt.Errorf("集成分支测试没有通过：%s", lastLines(out, 20))
		}
	}

	// 3. 交付说明
	e.Store.Lock()
	note, notePath := deliveryNote(p)
	e.Store.Unlock()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(ctl, notePath)), 0o755); err != nil {
		return Delivery{}, err
	}
	if err := os.WriteFile(filepath.Join(ctl, notePath), []byte(note), 0o644); err != nil {
		return Delivery{}, err
	}
	e.Store.Lock()
	err := project.CommitState(ctx, p, "交付说明 "+filepath.Base(notePath))
	e.Store.Unlock()
	if err != nil {
		return Delivery{}, err
	}

	// 4. 在临时 worktree 中生成合并提交
	tmp := filepath.Join(p.WorktreeRoot(), "_deliver")
	_ = gitops.RemoveWorktree(ctx, p.Path, tmp)
	oldBase, err := gitops.RevParse(ctx, p.Path, base)
	if err != nil {
		return Delivery{}, err
	}
	if _, err := gitops.Run(ctx, p.Path, "worktree", "add", "--detach", tmp, base); err != nil {
		return Delivery{}, err
	}
	defer gitops.RemoveWorktree(context.WithoutCancel(ctx), p.Path, tmp)
	subject := fmt.Sprintf("merge: 交付 %s（%s）", p.Name, filepath.Base(notePath))
	sha, err := gitops.Merge(ctx, tmp, p.IntegrationBranch, subject+"\n\n"+firstSection(note))
	if err != nil {
		return Delivery{}, fmt.Errorf("生成交付合并失败：%w", err)
	}

	// 5. 更新主分支：你的主目录正检出主分支时，只有干净才快进
	d := Delivery{Commit: short(sha), Note: notePath, Status: "merged"}
	if gitops.CurrentBranch(ctx, p.Path) == base {
		if !gitops.IsClean(ctx, p.Path) {
			d.Status, d.Detail = "waiting", "主目录有未提交的改动：先提交或暂存，再点「交付到主分支」"
			e.addReport(p, d.Detail, "")
			return d, ErrDirty
		}
		if _, err := gitops.Run(ctx, p.Path, "merge", "--ff-only", sha); err != nil {
			return d, fmt.Errorf("快进主目录失败：%w", err)
		}
	} else if _, err := gitops.Run(ctx, p.Path, "update-ref", "refs/heads/"+base, sha, oldBase); err != nil {
		return d, fmt.Errorf("更新 %s 失败：%w", base, err)
	}
	e.Store.Lock()
	p.DeliveredAt = time.Now()
	_ = project.SaveMeta(p)
	_ = project.CommitState(ctx, p, "记录交付时间")
	e.Store.Unlock()
	e.addReport(p, fmt.Sprintf("已交付到 %s：%s", base, notePath), short(sha))
	return d, nil
}

// onlyState 判断集成分支相对主分支是否只有 .autocoder/ 的变化。
func onlyState(ctx context.Context, dir, base string) bool {
	for _, f := range gitops.ChangedFiles(ctx, dir, base, "HEAD") {
		if !strings.HasPrefix(f, ".autocoder/") {
			return false
		}
	}
	return true
}

func (e *Engine) addReport(p *project.Project, summary, commit string) {
	e.Store.Lock()
	defer e.Store.Unlock()
	_ = project.AppendReport(p, project.Report{TaskID: "delivery", From: "autocoder", At: time.Now(), Status: project.Merged, Summary: summary, Commit: commit})
}

// quietTest 运行项目测试（依赖没装全时先装上），不推送实时输出。
func (e *Engine) quietTest(ctx context.Context, p *project.Project, dir string) (bool, string) {
	return e.runChecked(ctx, p, dir, p.TestCommand, newRun(Stat{}), nil, 9)
}

// deliveryNote 生成 .autocoder/deliveries/D<n>.md：完成了什么、验证结果、各工具用量。
func deliveryNote(p *project.Project) (string, string) {
	n := 1
	files, _ := filepath.Glob(filepath.Join(p.ControlDir(), ".autocoder", "deliveries", "D*.md"))
	n += len(files)
	path := fmt.Sprintf(".autocoder/deliveries/D%d.md", n)

	var b strings.Builder
	fmt.Fprintf(&b, "# %s 交付说明 D%d\n\n%s\n\n## 完成的任务\n\n", p.Name, n, time.Now().Format("2006-01-02 15:04"))
	latest := map[string]project.Report{}
	for _, r := range p.Reports {
		if _, ok := latest[r.TaskID]; !ok {
			latest[r.TaskID] = r
		}
	}
	for _, t := range p.Tasks {
		if t.Status != project.Merged {
			continue
		}
		r := latest[t.ID]
		fmt.Fprintf(&b, "- **%s %s**（%s，%d 次尝试，验证 %s）\n", t.ID, t.Title, r.From, t.Attempts, orDash(r.Verify))
		for _, a := range t.Acceptance {
			fmt.Fprintf(&b, "  - [x] %s\n", a)
		}
	}
	byTool := map[string]int64{}
	var total int64
	for _, u := range project.ReadUsage(p) {
		byTool[u.Tool] += u.Usage.Total()
		total += u.Usage.Total()
	}
	if total > 0 {
		fmt.Fprintf(&b, "\n## Token 用量\n\n合计 %d\n\n", total)
		ids := make([]string, 0, len(byTool))
		for id := range byTool {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			fmt.Fprintf(&b, "- %s：%d\n", id, byTool[id])
		}
	}
	if p.TestCommand == "" {
		b.WriteString("\n## 已知问题\n\n- 项目没有配置测试命令，合并前由你确认\n")
	}
	return b.String(), path
}

func firstSection(note string) string {
	if i := strings.Index(note, "\n## Token"); i > 0 {
		return strings.TrimSpace(note[:i])
	}
	return strings.TrimSpace(note)
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// Deliverable 供界面显示"交付到主分支"按钮。
func Deliverable(p *project.Project) (bool, string) { return deliverable(p) }
