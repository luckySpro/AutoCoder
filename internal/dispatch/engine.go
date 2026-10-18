// Package dispatch 把任务派发给本机的 CLI 工具，并按 DESIGN §6 观察、验证、提交、集成和交付。
//
// 原则：以看得见的事实为准。工具只在自己的 worktree 里改代码；提交、状态推进、验证、
// 合并都由这里完成，依据是进程状态、事件流、工作区改动和验证命令的结果。
package dispatch

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/luckySpro/AutoCoder/internal/gitops"
	"github.com/luckySpro/AutoCoder/internal/project"
	"github.com/luckySpro/AutoCoder/internal/roles"
	"github.com/luckySpro/AutoCoder/internal/settings"
	"github.com/luckySpro/AutoCoder/internal/toolscan"
)

// Engine 是调度器：运行槽、工具冷却、每次尝试的输出。
type Engine struct {
	Store    *project.Store
	Settings *settings.File
	// Tools 返回当前检测结果（由服务缓存）。
	Tools  func(ctx context.Context) []toolscan.Detection
	LogDir string // ~/.autocoder/logs
	// OnArgError 在工具报"不认识的参数"时调用（例如去检查在线参数清单）；可为空。
	OnArgError func(tool string)
	// VerifyTimeout 是每条验证命令的超时。
	VerifyTimeout time.Duration
	// Tick 是调度与观察的间隔（测试中调小）。
	Tick time.Duration

	mu       sync.Mutex
	runs     map[string]*Run
	cooldown map[string]time.Time
	coolN    map[string]int
	authWait map[string]bool
	wake     chan struct{}
	closing  bool // 正在退出：不再派发新的尝试
}

func (e *Engine) init() {
	if e.runs == nil {
		e.runs = map[string]*Run{}
		e.cooldown = map[string]time.Time{}
		e.coolN = map[string]int{}
		e.authWait = map[string]bool{}
		e.wake = make(chan struct{}, 1)
	}
	if e.VerifyTimeout == 0 {
		e.VerifyTimeout = 15 * time.Minute
	}
	if e.Tick == 0 {
		e.Tick = 2 * time.Second
	}
}

// Start 恢复中断的任务，并开始调度循环。
func (e *Engine) Start(ctx context.Context) {
	e.mu.Lock()
	e.init()
	e.mu.Unlock()
	e.recover()
	go func() {
		t := time.NewTicker(e.Tick)
		defer t.Stop()
		for {
			e.Schedule(ctx)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			case <-e.wake:
			}
		}
	}()
}

// Wake 让调度器立即检查一次。
func (e *Engine) Wake() {
	e.mu.Lock()
	e.init()
	ch := e.wake
	e.mu.Unlock()
	select {
	case ch <- struct{}{}:
	default:
	}
}

// recover 把上次没跑完的任务放回队列（AutoCoder 重启时进程已不在）。
func (e *Engine) recover() {
	e.Store.Lock()
	defer e.Store.Unlock()
	for _, p := range e.Store.Projects() {
		for _, t := range p.Tasks {
			switch t.Status {
			case project.Running, project.Testing, project.Integrating:
				t.Status = project.Ready
				t.LastError = "AutoCoder 重启，上一次尝试被中断"
				_ = project.SaveTask(p, t)
			}
			// review 状态保持不变：调度器会重新派出评审；planning 的需求同样会重新规划

		}
	}
}

// Runs 返回全部已知的尝试（含已结束的最近一次）。
func (e *Engine) Runs() []*Run {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]*Run, 0, len(e.runs))
	for _, r := range e.runs {
		out = append(out, r)
	}
	return out
}

// quitReason 是 AutoCoder 退出时停止尝试的原因。
const quitReason = "AutoCoder 退出"

// Shutdown 停止派发并结束正在运行的尝试：已有改动会作为 WIP 提交保留，
// 任务放回队列，下次启动继续。最多等 timeout。
func (e *Engine) Shutdown(timeout time.Duration) {
	e.mu.Lock()
	e.init()
	e.closing = true
	runs := make([]*Run, 0, len(e.runs))
	for _, r := range e.runs {
		runs = append(runs, r)
	}
	e.mu.Unlock()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		busy := false
		for _, r := range runs {
			if r.Active() {
				busy = true
				r.Stop(quitReason) // 进程可能还没起来，每轮都补一次
			}
		}
		if !busy {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	// 状态提交是后台进行的，拿一次锁等它们写完
	e.Store.Lock()
	ps := e.Store.Projects()
	e.Store.Unlock()
	for _, p := range ps {
		p.GitMu.Lock()
		p.GitMu.Unlock() //nolint:staticcheck // 只为等待
	}
}

// Run 返回 项目/任务 的最近一次尝试。
func (e *Engine) Run(key string) *Run {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.runs[key]
}

// Assignments 返回生效的角色分配：用户保存过的优先，否则用推荐值。
func Assignments(s settings.Settings, tools []toolscan.Detection) []roles.Assignment {
	if len(s.Assignments) > 0 {
		return s.Assignments
	}
	return roles.Suggest(tools)
}

// ---------- 调度 ----------

type pick struct {
	p    *project.Project
	t    *project.Task
	tool toolscan.Detection
	slot int
}

// Schedule 推进依赖、把可派发的任务交给空闲的工具。
func (e *Engine) Schedule(ctx context.Context) {
	e.mu.Lock()
	e.init()
	e.mu.Unlock()
	st := e.Settings.Get()
	tools := e.Tools(ctx)
	assigns := Assignments(st, tools)

	e.mu.Lock()
	busyTools := map[string]bool{}
	usedSlots := map[int]bool{}
	for _, r := range e.runs {
		if r.Active() {
			s := r.Snapshot()
			busyTools[s.Tool] = true
			usedSlots[slotOf(r)] = true
		}
	}
	now := time.Now()
	cooling := map[string]bool{}
	for id, until := range e.cooldown {
		if now.Before(until) {
			cooling[id] = true
		}
	}
	for id := range e.authWait {
		cooling[id] = true
	}
	e.mu.Unlock()

	var picks []pick
	var jobs []func()
	freeSlot := func() (int, bool) {
		if len(usedSlots) >= st.Concurrency {
			return 0, false
		}
		slot := 0
		for usedSlots[slot] {
			slot++
		}
		return slot, true
	}
	e.Store.Lock()
	for _, p := range e.Store.Projects() {
		for _, t := range p.Tasks {
			if t.Status == project.Planned && depsMerged(p, t) {
				t.Status = project.Ready
				_ = project.SaveTask(p, t)
			}
		}
	}
	e.mu.Lock()
	closing := e.closing
	e.mu.Unlock()
	if !st.Paused && !closing {
		for _, p := range e.Store.Projects() {
			if OverBudget(p) {
				continue // 预算用完：这个项目不再派发，等你提高预算（§6.7）
			}
			// 1. 规划：需求交给 lead（只读）
			for _, r := range p.Requests {
				if r.Status != project.ReqPlanning || e.activeRun(p.ID+"/"+r.ID) {
					continue
				}
				slot, ok := freeSlot()
				if !ok {
					break
				}
				tool, ok := chooseTool(&project.Task{Role: string(roles.Lead)}, tools, assigns, busyTools, cooling)
				if !ok {
					continue
				}
				usedSlots[slot], busyTools[tool.ID] = true, true
				r.Attempts++
				r.Tool = tool.ID
				_ = project.SaveRequest(p, r)
				run := e.startRun(Stat{ProjectID: p.ID, TaskID: r.ID, Tool: tool.ID, Role: string(roles.Lead), Attempt: r.Attempts, Phase: "planning"}, slot)
				p, id := p, r.ID
				jobs = append(jobs, func() { e.plan(context.WithoutCancel(ctx), p, id, tool, run, st) })
			}
			// 2. 评审：验证通过的任务交给评审工具（只读）
			for _, t := range p.Tasks {
				if t.Status != project.Review || e.activeRun(p.ID+"/"+t.ID) {
					continue
				}
				slot, ok := freeSlot()
				if !ok {
					break
				}
				author := e.authorOf(p.ID, t)
				tool, ok := chooseReviewer(tools, assigns, busyTools, cooling, author)
				if !ok {
					continue
				}
				usedSlots[slot], busyTools[tool.ID] = true, true
				run := e.reuseRun(Stat{ProjectID: p.ID, TaskID: t.ID, Tool: tool.ID, Role: string(roles.Reviewer), Attempt: t.Attempts, Phase: "reviewing"}, slot)
				p, id := p, t.ID
				jobs = append(jobs, func() { e.review(context.WithoutCancel(ctx), p, id, tool, author, run, st) })
			}
			// 3. 开发任务
			for _, t := range p.Tasks {
				if t.Status != project.Ready {
					continue
				}
				slot, ok := freeSlot()
				if !ok {
					break
				}
				tool, ok := chooseTool(t, tools, assigns, busyTools, cooling)
				if !ok {
					continue
				}
				usedSlots[slot] = true
				busyTools[tool.ID] = true
				t.Status = project.Running
				t.Attempts++
				t.Tool = tool.ID
				_ = project.SaveTask(p, t)
				picks = append(picks, pick{p, t, tool, slot})
			}
		}
	}
	e.Store.Unlock()

	for _, pk := range picks {
		run := e.startRun(Stat{ProjectID: pk.p.ID, TaskID: pk.t.ID, Tool: pk.tool.ID, Role: pk.t.Role, Attempt: pk.t.Attempts, Phase: "running"}, pk.slot)
		go e.attempt(context.WithoutCancel(ctx), pk.p, pk.t.ID, pk.tool, run, st)
	}
	for _, j := range jobs {
		go j()
	}
}

// startRun 登记一次新的运行（替换同一任务之前的记录）。
func (e *Engine) startRun(s Stat, slot int) *Run {
	now := time.Now().UnixMilli()
	s.StartedAt, s.LastActivity = now, now
	run := newRun(s)
	run.slot = slot
	e.mu.Lock()
	e.runs[run.Key()] = run
	e.mu.Unlock()
	return run
}

// reuseRun 在同一任务已有的输出后面接着运行（评审接在开发尝试之后，输出连续可看）。
func (e *Engine) reuseRun(s Stat, slot int) *Run {
	e.mu.Lock()
	run := e.runs[s.ProjectID+"/"+s.TaskID]
	e.mu.Unlock()
	if run == nil {
		return e.startRun(s, slot)
	}
	run.mu.Lock()
	run.slot = slot
	run.stopBy = ""
	run.mu.Unlock()
	run.update(func(st *Stat) {
		now := time.Now().UnixMilli()
		st.Tool, st.Role, st.Attempt, st.Phase = s.Tool, s.Role, s.Attempt, s.Phase
		st.StartedAt, st.LastActivity, st.Exit, st.Result = now, now, nil, ""
	})
	return run
}

func (e *Engine) activeRun(key string) bool {
	e.mu.Lock()
	r := e.runs[key]
	e.mu.Unlock()
	return r != nil && r.Active()
}

// authorOf 返回写这个任务代码的工具（评审时尽量换一个工具）。
func (e *Engine) authorOf(projectID string, t *project.Task) string {
	e.mu.Lock()
	r := e.runs[projectID+"/"+t.ID]
	e.mu.Unlock()
	if r != nil {
		if s := r.Snapshot(); s.Role != string(roles.Reviewer) {
			return s.Tool
		}
		r.mu.Lock()
		a := r.author
		r.mu.Unlock()
		if a != "" {
			return a
		}
	}
	return t.Assignee
}

// argErrorKeys 是各 CLI 报"不认识的参数"时的常见说法（clap、commander、yargs、argparse、cobra）。
var argErrorKeys = []string{"unexpected argument", "unknown option", "unrecognized option", "unknown argument", "unknown arguments", "unrecognized arguments", "unknown flag", "unknown shorthand flag"}

// OverBudget 表示项目的 token 已用完预算（调用方持有锁）。
func OverBudget(p *project.Project) bool {
	return p.TokenBudget > 0 && p.Spent >= p.TokenBudget
}

func slotOf(r *Run) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.slot
}

func depsMerged(p *project.Project, t *project.Task) bool {
	for _, d := range t.DependsOn {
		if dt := p.Task(d); dt == nil || dt.Status != project.Merged {
			return false
		}
	}
	return true
}

// chooseTool：优先计划指定的工具；不可用时交给承担同一角色的下一个工具；都不行就交给 lead（§6.3 改派）。
func chooseTool(t *project.Task, tools []toolscan.Detection, assigns []roles.Assignment, busy, cooling map[string]bool) (toolscan.Detection, bool) {
	byID := map[string]toolscan.Detection{}
	for _, d := range tools {
		byID[d.ID] = d
	}
	var order []string
	if t.Assignee != "" {
		order = append(order, t.Assignee)
	}
	var lead string
	for _, a := range assigns {
		if slices.Contains(a.Roles, roles.Role(t.Role)) {
			order = append(order, a.ToolID)
		}
		if slices.Contains(a.Roles, roles.Lead) {
			lead = a.ToolID
		}
	}
	if lead != "" {
		order = append(order, lead)
	}
	// 已经用完尝试次数的工具排到最后：有别的工具能做就先换人
	usable := func(id string) bool { d, ok := byID[id]; return ok && d.Schedulable() && !d.Disabled }
	var fresh, avoided []string
	for _, id := range order {
		if slices.Contains(t.Avoid, id) {
			avoided = append(avoided, id)
		} else {
			fresh = append(fresh, id)
		}
	}
	if slices.ContainsFunc(fresh, usable) {
		order = fresh // 别的工具只是暂时忙，等它空出来
	} else {
		order = avoided
	}
	for _, id := range order {
		d := byID[id]
		if usable(id) && !busy[id] && !cooling[id] {
			return d, true
		}
	}
	return toolscan.Detection{}, false
}

// ---------- 一次尝试 ----------

type outcome struct {
	exit      int
	final     string
	tail      []string
	usage     project.Tokens
	exact     bool
	stopBy    string
	authErr   bool
	limitErr  bool
	argErr    string // 工具不认识某个参数时的原话
	duration  time.Duration
	startErr  error
	conflicts []string
}

func (e *Engine) attempt(ctx context.Context, p *project.Project, taskID string, tool toolscan.Detection, run *Run, st settings.Settings) {
	e.Store.Lock()
	t := *p.Task(taskID)
	deps := depReports(p, &t)
	e.Store.Unlock()

	logf, logPath := e.openLog(p.ID, t.ID, t.Attempts)
	defer func() {
		if logf != nil {
			logf.Close()
		}
	}()
	say := func(text, tone string) {
		run.emit(Event{Type: "raw", Text: text, Tone: tone})
		writeLog(logf, text, false)
	}
	run.mu.Lock()
	run.author = tool.ID
	run.mu.Unlock()
	say(fmt.Sprintf("%s · 第 %d 次尝试 · %s", tool.Name, t.Attempts, t.Branch), "dim")

	dir := p.TaskDir(t.ID)
	// 1. worktree：从集成分支切出（能拿到已合并依赖的成果）
	fresh := false
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		fresh = true
	}
	if err := gitops.EnsureWorktree(ctx, p.Path, dir, t.Branch, p.IntegrationBranch); err != nil {
		e.finish(p, t.ID, run, "failed", func(t *project.Task) { e.retryOrBlock(t, st, "创建 worktree 失败："+err.Error()) })
		return
	}
	// 重试时先合入集成分支的最新进展；冲突交给工具解决
	var conflicts, fromIntegration []string
	if !fresh && !gitops.IsAncestor(ctx, dir, p.IntegrationBranch, "HEAD") {
		fromIntegration = gitops.ChangedFiles(ctx, dir, "HEAD", p.IntegrationBranch)
		c, err := gitops.MergeKeep(ctx, dir, p.IntegrationBranch, "merge: 合入 "+p.IntegrationBranch)
		if err != nil {
			say("合入集成分支失败："+err.Error(), "warn")
		}
		conflicts = c
		if len(c) > 0 {
			say("合入集成分支有冲突："+strings.Join(c, "、"), "warn")
			run.emit(Event{Type: "act", Kind: "warn", Text: "合入集成分支有冲突，交给工具解决：" + strings.Join(c, "、"), Tone: "warn"})
		}
	}
	startSHA, _ := gitops.RevParse(ctx, dir, "HEAD")

	// 2. 环境准备：新建 worktree 或依赖清单变了（比如上一轮加了依赖）时安装依赖。
	// 失败不拦住任务：搭骨架的任务里 package.json 等还不存在；交给工具处理，验证环节会兜底。
	e.ensureDeps(ctx, p, dir, run, logf, run.slot, false)

	// 3. 运行工具
	prompt := buildPrompt(p, &t, deps, conflicts)
	out := e.exec(ctx, dir, tool, prompt, false, run, logf, st)
	out.conflicts = conflicts
	usage := out.usage
	if !out.exact {
		usage = estimate(prompt, strings.Join(out.tail, "\n")+out.final)
	}
	run.update(func(s *Stat) { s.Tokens = usage.Total() })
	e.recordUsage(p, t.ID, t.Role, t.Attempts, tool.ID, run, out, usage)

	// 4. 判断结果并验证
	e.afterExit(ctx, p, t.ID, tool, run, logf, st, out, usage, startSHA, fromIntegration, logPath)
}

func depReports(p *project.Project, t *project.Task) []project.Report {
	var out []project.Report
	for _, d := range t.DependsOn {
		for _, r := range p.Reports { // 最新的在前
			if r.TaskID == d && r.Summary != "" {
				out = append(out, r)
				break
			}
		}
	}
	return out
}

func (e *Engine) openLog(projectID, taskID string, attempt int) (*os.File, string) {
	if e.LogDir == "" {
		return nil, ""
	}
	path := filepath.Join(e.LogDir, projectID, taskID, fmt.Sprintf("%d.log", attempt))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, ""
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, ""
	}
	return f, path
}

// LogPath 返回某次尝试的日志文件。
func (e *Engine) LogPath(projectID, taskID string, attempt int) string {
	return filepath.Join(e.LogDir, projectID, taskID, fmt.Sprintf("%d.log", attempt))
}

func writeLog(f *os.File, text string, isErr bool) {
	if f == nil {
		return
	}
	tag := " "
	if isErr {
		tag = "!"
	}
	fmt.Fprintf(f, "%s %s %s\n", time.Now().Format("15:04:05"), tag, text)
}

// exec 启动工具进程并观察：输出、文件变化、卡住与超时。
func (e *Engine) exec(ctx context.Context, dir string, tool toolscan.Detection, prompt string, readonly bool, run *Run, logf *os.File, st settings.Settings) outcome {
	start := time.Now()
	args := tool.Args(prompt, readonly)
	cmd := exec.Command(tool.Path, args...)
	cmd.Dir = dir
	cmd.Stdin = nil // 读到的是空设备：工具一旦等待输入会立刻读到结束
	cmd.Env = append(os.Environ(),
		"AUTOCODER_TASK="+run.stat.TaskID,
		fmt.Sprintf("AUTOCODER_PORT_BASE=%d", 20000+run.slot*100),
		"NO_COLOR=1", "CI=1", "GIT_TERMINAL_PROMPT=0",
	)
	setGroup(cmd)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	shown := append([]string{tool.Path}, args...)
	for i, a := range shown {
		if a == prompt {
			shown[i] = "<任务说明>"
		}
	}
	run.emit(Event{Type: "raw", Text: "$ " + strings.Join(shown, " "), Tone: "cmd"})
	if err := cmd.Start(); err != nil {
		run.emit(Event{Type: "raw", Text: "启动失败：" + err.Error(), Tone: "bad", Err: true})
		return outcome{exit: -1, startErr: err, duration: time.Since(start)}
	}

	done := make(chan struct{})
	var stopOnce sync.Once
	run.mu.Lock()
	run.cancel = func(reason string) {
		stopOnce.Do(func() {
			run.emit(Event{Type: "raw", Text: "^C " + reason + "：结束进程组", Tone: "warn"})
			go killGroup(cmd, done)
		})
	}
	run.mu.Unlock()

	ps := newParser()
	var pmu sync.Mutex
	var tail []string
	var authErr, limitErr bool
	var argErr string
	lines := 0 // 参数错误只会出现在开头几行
	authKeys := lower(tool.AuthErrors)
	limitKeys := lower(tool.LimitErrors)
	consume := func(r io.Reader, isErr bool, wg *sync.WaitGroup) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64<<10), 8<<20)
		for sc.Scan() {
			raw := mask(sc.Text())
			lw := strings.ToLower(raw)
			pmu.Lock()
			if containsAny(lw, authKeys) {
				authErr = true
			}
			if containsAny(lw, limitKeys) {
				limitErr = true
			}
			lines++
			if argErr == "" && lines <= 20 && containsAny(lw, argErrorKeys) {
				argErr = strings.TrimSpace(ansiRe.ReplaceAllString(raw, ""))
			}
			var ls []line
			var as []act
			if isErr {
				ls = []line{{text: ansiRe.ReplaceAllString(raw, "")}}
			} else {
				ls, as = ps.feed(raw)
			}
			for _, l := range ls {
				if strings.TrimSpace(l.text) != "" {
					tail = append(tail, l.text)
					if len(tail) > 40 {
						tail = tail[1:]
					}
				}
				run.emit(Event{Type: "raw", Text: clip(l.text, 4000), Tone: l.tone, Err: isErr})
				writeLog(logf, l.text, isErr)
			}
			for _, a := range as {
				run.emit(Event{Type: "act", Kind: a.kind, Text: a.text, Meta: a.meta, Tone: a.tone})
			}
			tok := ps.usage.Total()
			pmu.Unlock()
			if tok > 0 {
				run.update(func(s *Stat) { s.Tokens = tok })
			}
		}
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go consume(stdout, false, &wg)
	go consume(stderr, true, &wg)
	waitErr := make(chan error, 1)
	go func() {
		wg.Wait()
		waitErr <- cmd.Wait()
		close(done)
	}()

	// 观察：每个 tick 统计改动；连续无输出且无文件变化判定卡住；超过总时长判定超时；电脑睡眠的空档不计入
	idle := time.Duration(st.IdleMinutes) * time.Minute
	limit := time.Duration(st.TimeoutMinutes) * time.Minute
	interval := max(e.Tick, time.Second)
	if interval > 15*time.Second {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var excluded time.Duration
	last := time.Now()
	sig := ""
	var err error
loop:
	for {
		select {
		case err = <-waitErr:
			break loop
		case now := <-ticker.C:
			if gap := now.Sub(last); gap > 3*interval+30*time.Second {
				excluded += gap - interval
				run.touch()
			}
			last = now
			if ch, cerr := gitops.Changes(ctx, dir); cerr == nil {
				files, add, del := summarize(ch)
				if s := fmt.Sprint(files, add, del); s != sig {
					sig = s
					run.touch()
					run.update(func(s *Stat) { s.Files, s.Added, s.Removed = files, add, del })
				}
			}
			snap := run.Snapshot()
			if idle > 0 && now.Sub(time.UnixMilli(snap.LastActivity)) > idle {
				run.Stop(fmt.Sprintf("卡住：连续 %d 分钟没有输出也没有文件变化", st.IdleMinutes))
			}
			if limit > 0 && now.Sub(start)-excluded > limit {
				run.Stop(fmt.Sprintf("超时：单次尝试超过 %d 分钟", st.TimeoutMinutes))
			}
		}
	}
	code := 0
	if err != nil {
		code = -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
	}
	run.mu.Lock()
	run.cancel = nil
	run.mu.Unlock()
	pmu.Lock()
	defer pmu.Unlock()
	final := ps.final
	if final == "" {
		final = ps.lastAgent
	}
	o := outcome{exit: code, final: final, tail: tail, usage: ps.usage, exact: ps.exact, stopBy: run.stoppedBy(),
		authErr: authErr && code != 0, limitErr: limitErr && code != 0, duration: time.Since(start)}
	if code != 0 && o.duration < 30*time.Second { // 参数错误会立刻退出
		o.argErr = argErr
	}
	return o
}

func summarize(ch []gitops.Change) ([]string, int, int) {
	files := make([]string, 0, len(ch))
	add, del := 0, 0
	for _, c := range ch {
		files = append(files, c.Path)
		add += c.Added
		del += c.Removed
	}
	return files, add, del
}

func lower(ss []string) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func containsAny(s string, keys []string) bool {
	for _, k := range keys {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

func (e *Engine) recordUsage(p *project.Project, taskID, role string, attempt int, tool string, run *Run, out outcome, usage project.Tokens) {
	if out.startErr != nil {
		return
	}
	u := project.Usage{
		ID: fmt.Sprintf("%s-%s-%s-%d-%d", p.ID, taskID, role, attempt, time.Now().UnixNano()), ProjectID: p.ID, TaskID: taskID,
		Tool: tool, Role: role, At: time.UnixMilli(run.Snapshot().StartedAt), DurationSec: int(out.duration.Seconds()),
		ExitCode: out.exit, Usage: usage, Estimated: !out.exact,
	}
	e.Store.Lock()
	_ = project.AppendUsage(p, u)
	e.Store.Unlock()
	meta := "+" + fmtTok(usage.Total()) + " token"
	if !out.exact {
		meta += "（估算）"
	}
	run.emit(Event{Type: "act", Kind: "usage", Text: "本次用量", Meta: meta})
}

func fmtTok(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.2fM", float64(n)/1e6)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}

// ---------- 进程结束之后 ----------

// 依赖清单、锁文件这类常见的范围外改动默认放行（项目设置里可追加）。
var defaultAllow = []string{"go.mod", "go.sum", "**/package.json", "**/package-lock.json", "**/pnpm-lock.yaml", "**/yarn.lock", "Cargo.toml", "Cargo.lock", "**/requirements*.txt", "**/poetry.lock", "**/pyproject.toml"}

// 改动这些文件需要你确认（§6.5 第 3 步）。
var dangerPaths = []string{".github/workflows/**", ".gitlab-ci.yml", ".circleci/**", "**/.env", "**/.env.*", "**/*.pem", "**/*.key", "**/id_rsa*", "deploy/**", "**/Dockerfile", "**/docker-compose*.yml"}

func (e *Engine) afterExit(ctx context.Context, p *project.Project, taskID string, tool toolscan.Detection, run *Run, logf *os.File, st settings.Settings,
	out outcome, usage project.Tokens, startSHA string, fromIntegration []string, logPath string) {
	dir := p.TaskDir(taskID)
	e.Store.Lock()
	t := *p.Task(taskID)
	e.Store.Unlock()
	tok := usage.Total()
	report := project.Report{TaskID: t.ID, From: tool.ID, At: time.Now(), Attempt: t.Attempts, Exit: out.exit, Tokens: tok}
	summary := strings.TrimSpace(out.final)
	if summary == "" && len(out.tail) > 0 {
		summary = strings.Join(out.tail[max(0, len(out.tail)-6):], "\n")
	}

	switch {
	case out.startErr != nil:
		e.cool(tool.ID, true)
		e.finishReport(p, t.ID, run, "failed", report, "工具无法启动："+out.startErr.Error(), func(t *project.Task) {
			t.Attempts-- // 不算一次尝试，换个工具再派
			t.Status = project.Ready
			t.LastError = tool.Name + " 无法启动：" + out.startErr.Error()
		})
		return
	case out.argErr != "":
		// 参数与当前安装的版本不兼容：不是任务的问题，不算尝试，换工具接着做
		until := e.cool(tool.ID, true)
		if e.OnArgError != nil {
			go e.OnArgError(tool.ID)
		}
		msg := fmt.Sprintf("%s 不认识派发参数（%s），已暂停到 %s 并改派；可在工具清单点「检查参数更新」或修改参数", tool.Name, clip(out.argErr, 200), until.Format("15:04"))
		e.finishReport(p, t.ID, run, "failed", report, msg, func(t *project.Task) {
			t.Attempts--
			t.Status = project.Ready
			t.LastError = msg
		})
		return
	case out.authErr:
		e.mu.Lock()
		e.authWait[tool.ID] = true
		e.mu.Unlock()
		e.finishReport(p, t.ID, run, "failed", report, tool.Name+" 未登录或凭据已过期", func(t *project.Task) {
			t.Attempts--
			t.Status = project.WaitingUser
			t.Waiting = fmt.Sprintf("%s 未登录或凭据已过期：在终端运行 %s 完成登录，然后点「重试」。", tool.Name, tool.Commands[0])
		})
		return
	case out.limitErr:
		until := e.cool(tool.ID, false)
		e.finishReport(p, t.ID, run, "failed", report, fmt.Sprintf("%s 被限流或额度用完，冷却到 %s", tool.Name, until.Format("15:04")), func(t *project.Task) {
			t.Attempts--
			t.Status = project.Ready
			t.LastError = tool.Name + " 被限流，已改派或等待冷却"
		})
		return
	}

	// 用户停止：改动作为 WIP 保留在任务分支上
	if out.stopBy == "用户停止" {
		sha, _ := gitops.CommitAll(ctx, dir, trailers(fmt.Sprintf("wip(%s): 已停止", t.ID), t, tool.ID, "未验证", tok))
		report.Commit = short(sha)
		e.finishReport(p, t.ID, run, "stopped", report, "已停止，改动作为 WIP 保留在任务分支上", func(t *project.Task) {
			t.Status = project.Cancelled
		})
		return
	}

	run.update(func(s *Stat) { s.Phase = "verifying" })
	e.setStatus(p, t.ID, project.Testing)

	// 范围检查：.autocoder/、.git 一律撤回；scope 之外、白名单之外的撤回
	changes, err := gitops.DiffFrom(ctx, dir, startSHA)
	if err != nil {
		e.finishReport(p, t.ID, run, "failed", report, "读取改动失败："+err.Error(), func(t *project.Task) { e.retryOrBlock(t, st, "读取改动失败："+err.Error()) })
		return
	}
	var outside []string
	for _, c := range changes {
		if slices.Contains(fromIntegration, c.Path) {
			continue
		}
		if strings.HasPrefix(c.Path, ".autocoder/") || c.Path == ".autocoder" {
			outside = append(outside, c.Path)
			continue
		}
		if len(t.Scope) > 0 && !matchAny(t.Scope, c.Path) && !matchAny(defaultAllow, c.Path) && !matchAny(p.ScopeAllow, c.Path) {
			outside = append(outside, c.Path)
		}
	}
	if len(outside) > 0 {
		_ = gitops.Revert(ctx, dir, startSHA, outside)
		msg := "撤回了范围外的改动：" + strings.Join(outside, "、")
		run.emit(Event{Type: "act", Kind: "warn", Text: msg, Tone: "warn"})
		writeLog(logf, msg, false)
		changes, _ = gitops.DiffFrom(ctx, dir, startSHA)
	}
	var own []gitops.Change
	for _, c := range changes {
		if !slices.Contains(fromIntegration, c.Path) {
			own = append(own, c)
		}
	}
	files, add, del := summarize(own)
	run.update(func(s *Stat) { s.Files, s.Added, s.Removed = files, add, del })
	report.Files, report.Added, report.Removed = len(own), add, del

	// 冲突必须解决干净
	if leftover := conflictMarkers(dir, out.conflicts); len(leftover) > 0 {
		_, _ = gitops.Run(ctx, dir, "merge", "--abort")
		reason := "合并冲突没有解决：" + strings.Join(leftover, "、")
		e.finishReport(p, t.ID, run, "failed", report, reason, func(t *project.Task) { e.conflictFailed(t, st, reason, leftover) })
		return
	}

	// 卡住、超时、崩溃：保留 WIP 再重试
	failReason := ""
	switch {
	case out.stopBy != "":
		failReason = out.stopBy
	case out.exit != 0:
		failReason = fmt.Sprintf("%s 非零退出（退出码 %d）：%s", tool.Name, out.exit, strings.Join(out.tail[max(0, len(out.tail)-8):], "\n"))
	case len(own) == 0 && !gitops.Merging(ctx, dir) && len(gitops.ChangedFiles(ctx, dir, p.IntegrationBranch, "HEAD")) > 0:
		// 之前的尝试已经把活干完并提交了，这次只是确认：直接去验证，不算失败
		msg := "这次没有新改动，分支上已有之前尝试的提交，直接验证"
		run.emit(Event{Type: "act", Kind: "say", Text: msg})
		writeLog(logf, msg, false)
	case len(own) == 0 && !gitops.Merging(ctx, dir):
		failReason = "没有任何改动"
		if q := strings.TrimSpace(out.final); strings.HasSuffix(q, "?") || strings.HasSuffix(q, "？") {
			failReason = "工具没有改代码，而是在提问：" + clip(q, 500)
		}
	}
	if failReason != "" {
		sha, _ := gitops.CommitAll(ctx, dir, trailers(fmt.Sprintf("wip(%s): 第 %d 次尝试未完成", t.ID, t.Attempts), t, tool.ID, "未验证", tok))
		report.Commit = short(sha)
		e.finishReport(p, t.ID, run, "failed", report, summaryWith(summary, failReason), func(t *project.Task) { e.retryOrBlock(t, st, failReason) })
		return
	}

	// 危险改动：先提交保留，等你确认
	if d := dangerous(own); d != "" {
		sha, _ := gitops.CommitAll(ctx, dir, trailers(fmt.Sprintf("feat(%s): %s", t.ID, t.Title), t, tool.ID, "待确认", tok))
		report.Commit = short(sha)
		e.finishReport(p, t.ID, run, "waiting", report, summaryWith(summary, d+"，等你确认"), func(t *project.Task) {
			t.Status = project.WaitingUser
			t.Waiting = d + "。确认没问题后点「确认改动」继续验证和合并。"
			t.Commits = gitops.CountCommits(ctx, p.Path, p.IntegrationBranch, t.Branch)
		})
		return
	}

	e.verifyAndIntegrate(ctx, p, t.ID, tool.ID, run, logf, st, report, summary)
}

// Approve 确认危险改动后继续验证与合并。
func (e *Engine) Approve(ctx context.Context, projectID, taskID string) error {
	e.Store.Lock()
	p := e.Store.Get(projectID)
	var t *project.Task
	if p != nil {
		t = p.Task(taskID)
	}
	if t == nil || t.Status != project.WaitingUser {
		e.Store.Unlock()
		return errors.New("这个任务不在等待确认的状态")
	}
	tool := t.Assignee
	if r := e.Run(p.ID + "/" + t.ID); r != nil {
		tool = r.Snapshot().Tool
	}
	t.Waiting = ""
	t.Status = project.Testing
	_ = project.SaveTask(p, t)
	attempt := t.Attempts
	role := t.Role
	e.Store.Unlock()

	run := e.Run(projectID + "/" + taskID)
	if run == nil {
		run = newRun(Stat{ProjectID: projectID, TaskID: taskID, Tool: tool, Role: role, Attempt: attempt, StartedAt: time.Now().UnixMilli(), LastActivity: time.Now().UnixMilli()})
		e.mu.Lock()
		e.runs[run.Key()] = run
		e.mu.Unlock()
	}
	run.update(func(s *Stat) { s.Phase = "verifying" })
	logf, _ := e.openLog(projectID, taskID, attempt*100+1)
	go func() {
		defer func() {
			if logf != nil {
				logf.Close()
			}
		}()
		report := project.Report{TaskID: taskID, From: "autocoder", At: time.Now(), Attempt: attempt}
		e.verifyAndIntegrate(context.WithoutCancel(ctx), p, taskID, tool, run, logf, e.Settings.Get(), report, "你确认了改动")
	}()
	return nil
}

func (e *Engine) verifyAndIntegrate(ctx context.Context, p *project.Project, taskID, toolID string, run *Run, logf *os.File, st settings.Settings, report project.Report, summary string) {
	dir := p.TaskDir(taskID)
	e.Store.Lock()
	t := *p.Task(taskID)
	e.Store.Unlock()

	// 验证命令由编排器运行，不信任工具自述
	cmds := append([]string(nil), t.Verify...)
	if p.TestCommand != "" && !slices.Contains(cmds, p.TestCommand) {
		cmds = append(cmds, p.TestCommand)
	}
	pass, failed := 0, ""
	var failOut string
	for _, c := range cmds {
		ok, outText := e.runChecked(ctx, p, dir, c, run, logf, run.slot)
		if ok {
			pass++
		} else if failed == "" {
			failed, failOut = c, outText
		}
	}
	verify := "无验证命令"
	if len(cmds) > 0 {
		verify = fmt.Sprintf("pass %d/%d", pass, len(cmds))
		if failed != "" {
			verify = fmt.Sprintf("fail %d/%d", pass, len(cmds))
		}
	}
	report.Verify = verify

	prefix := "feat"
	switch t.Kind {
	case "test":
		prefix = "test"
	case "infra":
		prefix = "chore"
	}
	sha, err := gitops.CommitAll(ctx, dir, trailers(fmt.Sprintf("%s(%s): %s", prefix, t.ID, t.Title), t, toolID, verify, report.Tokens))
	if err != nil {
		e.finishReport(p, t.ID, run, "failed", report, "提交失败："+err.Error(), func(t *project.Task) { e.retryOrBlock(t, st, "提交失败："+err.Error()) })
		return
	}
	if sha == "" {
		sha, _ = gitops.RevParse(ctx, dir, "HEAD")
	}
	report.Commit = short(sha)
	commits := gitops.CountCommits(ctx, p.Path, p.IntegrationBranch, t.Branch)

	if failed != "" {
		reason := fmt.Sprintf("验证命令失败：%s\n%s", failed, lastLines(failOut, 30))
		e.finishReport(p, t.ID, run, "failed", report, summaryWith(summary, "验证没有通过："+failed), func(t *project.Task) {
			t.Commits = commits
			e.retryOrBlock(t, st, reason)
		})
		return
	}

	// 评审：交给评审工具，通过后再集成（§6.5 第 6 步）
	if st.Review {
		report.Status = project.Review
		e.finishReport(p, t.ID, run, "awaiting-review", report, summaryWith(summary, reviewWaitNote), func(t *project.Task) {
			t.Status = project.Review
			t.Commits = commits
			t.ReviewTries = 0
		})
		return
	}
	e.integrateTask(ctx, p, t.ID, toolID, run, logf, st, report, summary)
}

// integrateTask 把任务合并进集成分支；全部合并后按设置交付到主分支。
func (e *Engine) integrateTask(ctx context.Context, p *project.Project, taskID, toolID string, run *Run, logf *os.File, st settings.Settings, report project.Report, summary string) {
	e.Store.Lock()
	t := *p.Task(taskID)
	e.Store.Unlock()
	commits := gitops.CountCommits(ctx, p.Path, p.IntegrationBranch, t.Branch)
	dir := p.TaskDir(taskID)

	// 集成：每个项目同一时间只合并一个任务
	run.update(func(s *Stat) { s.Phase = "integrating" })
	e.setStatus(p, t.ID, project.Integrating)
	mergeErr := e.integrate(ctx, p, &t, run, logf)
	if mergeErr != nil {
		reason := mergeErr.Error()
		var ce *gitops.ConflictError
		e.finishReport(p, t.ID, run, "failed", report, summaryWith(summary, reason), func(t *project.Task) {
			t.Commits = commits
			if errors.As(mergeErr, &ce) {
				e.conflictFailed(t, st, reason, ce.Files)
				return
			}
			e.retryOrBlock(t, st, reason)
		})
		return
	}
	report.Status = project.Merged
	e.finishReport(p, t.ID, run, "done", report, summary, func(t *project.Task) {
		t.Status = project.Merged
		t.Commits = commits
		t.LastError = ""
		t.ConflictFails = 0
	})
	_ = gitops.RemoveWorktree(ctx, p.Path, dir) // 分支保留，worktree 不再需要
	e.mu.Lock()
	e.coolN[toolID] = 0
	e.mu.Unlock()

	// 全部任务都合并后交付到主分支
	if st.AutoMerge {
		e.Store.Lock()
		ready, _ := deliverable(p)
		e.Store.Unlock()
		if ready && p.TestCommand != "" {
			if _, err := e.Deliver(ctx, p.ID); err != nil {
				run.emit(Event{Type: "act", Kind: "warn", Text: "交付到主分支没有完成：" + err.Error(), Tone: "warn"})
			} else {
				run.emit(Event{Type: "act", Kind: "say", Text: "全部任务已合并，已交付到 " + p.BaseBranch, Tone: "ok"})
			}
		}
	}
}

// conflictFailed：冲突连续 2 次没能解决，转给你处理（§6.6）。
func (e *Engine) conflictFailed(t *project.Task, st settings.Settings, reason string, files []string) {
	t.ConflictFails++
	if t.ConflictFails >= 2 {
		t.LastError = reason
		t.Status = project.WaitingUser
		t.Waiting = fmt.Sprintf("与集成分支连续 %d 次冲突没能自动解决：%s。可以在任务分支 %s 上手动解决并提交后点「重试」，或取消这个任务。",
			t.ConflictFails, strings.Join(files, "、"), t.Branch)
		return
	}
	e.retryOrBlock(t, st, reason)
}

// integrate 把任务分支 --no-ff 合并到集成分支并跑项目测试；失败则把集成分支退回原位。
func (e *Engine) integrate(ctx context.Context, p *project.Project, t *project.Task, run *Run, logf *os.File) error {
	p.GitMu.Lock()
	defer p.GitMu.Unlock()
	ctl := p.ControlDir()
	e.Store.Lock()
	_ = project.CommitState(ctx, p, t.ID+" 进入集成")
	e.Store.Unlock()
	before, err := gitops.RevParse(ctx, ctl, "HEAD")
	if err != nil {
		return err
	}
	msg := fmt.Sprintf("merge(%s): %s\n\nAutoCoder-Task: %s\nAutoCoder-Attempt: %d", t.ID, t.Title, t.ID, t.Attempts)
	if _, err := gitops.Merge(ctx, ctl, t.Branch, msg); err != nil {
		var ce *gitops.ConflictError
		if errors.As(err, &ce) {
			return fmt.Errorf("与集成分支%w。下次尝试会先合入集成分支再解决", ce)
		}
		return fmt.Errorf("合并到集成分支失败：%w", err)
	}
	run.emit(Event{Type: "act", Kind: "run", Text: "合并到 " + p.IntegrationBranch, Meta: "完成", Tone: "ok"})
	if p.TestCommand != "" {
		ok, out := e.runChecked(ctx, p, ctl, p.TestCommand, run, logf, 9)
		if !ok {
			// 不稳定测试：重跑一次，结果不一致时不算任务失败
			ok2, _ := e.shellStep(ctx, ctl, p.TestCommand, run, logf, 9)
			if !ok2 {
				_ = gitops.ResetHard(ctx, ctl, before)
				e.Store.Lock()
				_ = project.Flush(p)
				e.Store.Unlock()
				return fmt.Errorf("与其他任务组合后测试失败，集成分支已退回：\n%s", lastLines(out, 30))
			}
			run.emit(Event{Type: "act", Kind: "warn", Text: "项目测试第一次失败、重跑通过，可能有不稳定的测试", Tone: "warn"})
		}
	}
	return nil
}

// shellStep 在 dir 里运行一条命令，输出进入实时输出与日志。
func (e *Engine) shellStep(ctx context.Context, dir, command string, run *Run, logf *os.File, slot int) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, e.VerifyTimeout)
	defer cancel()
	name, args := shell(command)
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), fmt.Sprintf("AUTOCODER_PORT_BASE=%d", 20000+slot*100), "CI=1", "NO_COLOR=1")
	setGroup(cmd)
	cmd.Cancel = func() error { killGroup(cmd, nil); return nil }
	cmd.WaitDelay = 10 * time.Second
	run.emit(Event{Type: "raw", Text: "$ " + command, Tone: "cmd"})
	writeLog(logf, "$ "+command, false)
	out, err := cmd.CombinedOutput()
	text := mask(string(out))
	for _, l := range strings.Split(strings.TrimRight(lastLines(text, 200), "\n"), "\n") {
		if l != "" {
			run.emit(Event{Type: "raw", Text: clip(ansiRe.ReplaceAllString(l, ""), 4000), Err: err != nil})
			writeLog(logf, l, err != nil)
		}
	}
	meta, tone := "通过", "ok"
	if ctx.Err() == context.DeadlineExceeded {
		meta, tone = "超时", "bad"
		err = ctx.Err()
	} else if err != nil {
		meta, tone = "失败", "bad"
	}
	run.emit(Event{Type: "act", Kind: "run", Text: command, Meta: meta, Tone: tone})
	return err == nil, text
}

// ---------- 状态推进 ----------

func (e *Engine) setStatus(p *project.Project, taskID, status string) {
	e.Store.Lock()
	defer e.Store.Unlock()
	if t := p.Task(taskID); t != nil {
		t.Status = status
		_ = project.SaveTask(p, t)
	}
}

func (e *Engine) retryOrBlock(t *project.Task, st settings.Settings, reason string) {
	if reason == quitReason {
		// 退出打断的这次不算一次尝试，下次启动接着做
		t.Attempts = max(0, t.Attempts-1)
		t.LastError = "AutoCoder 退出时中断，下次启动继续"
		t.Status = project.Ready
		return
	}
	t.LastError = reason
	// 一个工具用完尝试次数：换另一个工具从头再试（最多换两次），不直接停下等你
	if t.Attempts >= st.MaxAttempts && t.Tool != "" && !slices.Contains(t.Avoid, t.Tool) && len(t.Avoid) < 2 && e.hasOtherTool(t, st) {
		t.Avoid = append(t.Avoid, t.Tool)
		t.Attempts = 0
		t.Status = project.Ready
		t.LastError = fmt.Sprintf("%s 尝试 %d 次没有通过，换其他工具接着做。上次的问题：\n%s", t.Tool, st.MaxAttempts, reason)
		return
	}
	if t.Attempts >= st.MaxAttempts {
		t.Status = project.Blocked
		t.Waiting = fmt.Sprintf("已尝试 %d 次仍未通过。可以修改任务说明或验收标准后点「重试」，也可以改派给其他工具。", t.Attempts)
		return
	}
	t.Status = project.Ready
}

// hasOtherTool：除了这次用的和已经试过的，还有别的工具能接这个任务。
func (e *Engine) hasOtherTool(t *project.Task, st settings.Settings) bool {
	if e.Tools == nil {
		return false
	}
	tools := e.Tools(context.Background())
	probe := *t
	probe.Assignee = ""
	probe.Avoid = append(append([]string(nil), t.Avoid...), t.Tool)
	d, ok := chooseTool(&probe, tools, Assignments(st, tools), nil, nil)
	return ok && !slices.Contains(probe.Avoid, d.ID)
}

func (e *Engine) finish(p *project.Project, taskID string, run *Run, phase string, f func(*project.Task)) {
	e.Store.Lock()
	if t := p.Task(taskID); t != nil {
		f(t)
		_ = project.SaveTask(p, t)
	}
	e.Store.Unlock()
	run.update(func(s *Stat) { s.Phase = phase })
	e.Wake()
}

// finishReport 写汇报、更新任务、提交状态，并结束这次尝试。
func (e *Engine) finishReport(p *project.Project, taskID string, run *Run, phase string, r project.Report, summary string, f func(*project.Task)) {
	e.Store.Lock()
	t := p.Task(taskID)
	if t != nil {
		f(t)
		_ = project.SaveTask(p, t)
		if r.Status == "" {
			r.Status = t.Status
		}
	}
	r.Summary = summary
	_ = project.AppendReport(p, r)
	e.Store.Unlock()
	go func() {
		p.GitMu.Lock()
		e.Store.Lock()
		_ = project.CommitState(context.Background(), p, fmt.Sprintf("%s 第 %d 次尝试：%s", taskID, r.Attempt, r.Status))
		e.Store.Unlock()
		p.GitMu.Unlock()
	}()
	run.update(func(s *Stat) {
		s.Phase = phase
		s.Result = clip(oneLine(summary), 200)
		code := r.Exit
		s.Exit = &code
	})
	e.Wake()
}

// cool 让工具冷却：限流从 5 分钟起指数退避，最长 1 小时；无法启动时冷却 10 分钟。
func (e *Engine) cool(tool string, broken bool) time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	d := 10 * time.Minute
	if !broken {
		d = 5 * time.Minute << min(e.coolN[tool], 4)
		d = min(d, time.Hour)
		e.coolN[tool]++
	}
	until := time.Now().Add(d)
	e.cooldown[tool] = until
	return until
}

// ClearAuth 用户登录后点重试时，解除该工具的暂停。
func (e *Engine) ClearAuth(tool string) {
	e.mu.Lock()
	delete(e.authWait, tool)
	delete(e.cooldown, tool)
	e.mu.Unlock()
}

// ToolState 返回工具的冷却与登录状态，供界面提示。
func (e *Engine) ToolState() map[string]string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := map[string]string{}
	for id, until := range e.cooldown {
		if time.Now().Before(until) {
			out[id] = "冷却到 " + until.Format("15:04")
		}
	}
	for id := range e.authWait {
		out[id] = "等待登录"
	}
	return out
}

// ---------- 判断 ----------

func trailers(subject string, t project.Task, tool, verify string, tokens int64) string {
	return fmt.Sprintf("%s\n\nAutoCoder-Task: %s\nAutoCoder-Attempt: %d\nAutoCoder-Tool: %s\nAutoCoder-Verify: %s\nAutoCoder-Tokens: %d",
		subject, t.ID, t.Attempts, tool, verify, tokens)
}

func summaryWith(summary, reason string) string {
	if summary == "" {
		return reason
	}
	return reason + "\n\n" + summary
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func dangerous(ch []gitops.Change) string {
	deleted, lines := 0, 0
	var risky []string
	for _, c := range ch {
		if c.Status == "D" {
			deleted++
		}
		lines += c.Added + c.Removed
		if matchAny(dangerPaths, c.Path) {
			risky = append(risky, c.Path)
		}
	}
	switch {
	case deleted > 10:
		return fmt.Sprintf("删除了 %d 个文件", deleted)
	case lines > 2000:
		return fmt.Sprintf("改动了 %d 行", lines)
	case len(risky) > 0:
		return "改动了 CI、部署或密钥相关的文件：" + strings.Join(risky, "、")
	}
	return ""
}

func conflictMarkers(dir string, files []string) []string {
	var left []string
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			continue
		}
		s := string(data)
		if strings.Contains(s, "\n<<<<<<< ") || strings.HasPrefix(s, "<<<<<<< ") || strings.Contains(s, "\n>>>>>>> ") {
			left = append(left, f)
		}
	}
	return left
}

// matchAny 判断路径是否命中任一 glob（支持 **）；不含通配符的模式按目录前缀匹配。
func matchAny(patterns []string, path string) bool {
	for _, p := range patterns {
		if globMatch(p, path) {
			return true
		}
	}
	return false
}

var globCache sync.Map

func globMatch(pattern, path string) bool {
	pattern = strings.TrimPrefix(strings.TrimSpace(pattern), "./")
	if pattern == "" {
		return false
	}
	if !strings.ContainsAny(pattern, "*?[") {
		p := strings.TrimSuffix(pattern, "/")
		return path == p || strings.HasPrefix(path, p+"/")
	}
	v, ok := globCache.Load(pattern)
	if !ok {
		var b strings.Builder
		b.WriteString("^")
		for i := 0; i < len(pattern); i++ {
			c := pattern[i]
			switch {
			case c == '*' && i+1 < len(pattern) && pattern[i+1] == '*':
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			case c == '*':
				b.WriteString("[^/]*")
			case c == '?':
				b.WriteString("[^/]")
			default:
				b.WriteString(regexp.QuoteMeta(string(c)))
			}
		}
		b.WriteString("$")
		v, _ = globCache.LoadOrStore(pattern, regexp.MustCompile(b.String()))
	}
	return v.(*regexp.Regexp).MatchString(path)
}

// ---------- 用户操作 ----------

// Stop 停止运行中的任务（改动作为 WIP 保留）。
func (e *Engine) Stop(key string) bool {
	r := e.Run(key)
	return r != nil && r.Stop("用户停止")
}

// Retry 把任务放回队列；从受阻、取消、等你处理的状态重试时重新计数。
func (e *Engine) Retry(projectID, taskID string) error {
	e.Store.Lock()
	defer e.Store.Unlock()
	p := e.Store.Get(projectID)
	if p == nil || p.Task(taskID) == nil {
		return errors.New("任务不存在")
	}
	t := p.Task(taskID)
	switch t.Status {
	case project.Running, project.Testing, project.Integrating:
		return errors.New("任务正在运行")
	case project.Review:
		if e.activeRun(projectID + "/" + taskID) {
			return errors.New("任务正在评审")
		}
	case project.Merged:
		return errors.New("任务已经合并")
	}
	if t.Status == project.Blocked || t.Status == project.Cancelled || t.Status == project.WaitingUser {
		t.Attempts = 0
		t.ConflictFails = 0
		t.Avoid = nil
	}
	if t.Assignee != "" {
		e.ClearAuth(t.Assignee)
	}
	if r := e.Run(projectID + "/" + taskID); r != nil {
		e.ClearAuth(r.Snapshot().Tool)
	}
	t.Waiting = ""
	t.Status = project.Planned
	if depsMerged(p, t) {
		t.Status = project.Ready
	}
	err := project.SaveTask(p, t)
	go e.Wake()
	return err
}

// RetryBlocked 把项目里所有受阻、等你处理的任务一次放回派发队列（尝试次数清零）。
// 等你确认改动的任务需要人看过再合并，不在其中。返回重试了的任务编号。
func (e *Engine) RetryBlocked(projectID string) ([]string, error) {
	e.Store.Lock()
	p := e.Store.Get(projectID)
	if p == nil {
		e.Store.Unlock()
		return nil, errors.New("项目不存在")
	}
	var ids []string
	for _, t := range p.Tasks {
		if (t.Status == project.Blocked || t.Status == project.WaitingUser) && !strings.Contains(t.Waiting, "确认改动") {
			ids = append(ids, t.ID)
		}
	}
	e.Store.Unlock()
	done := []string{}
	for _, id := range ids {
		if err := e.Retry(projectID, id); err == nil {
			done = append(done, id)
		}
	}
	return done, nil
}

// Cancel 取消任务；运行中的先停止。
func (e *Engine) Cancel(projectID, taskID string) error {
	if e.Stop(projectID + "/" + taskID) {
		return nil
	}
	e.Store.Lock()
	defer e.Store.Unlock()
	p := e.Store.Get(projectID)
	if p == nil || p.Task(taskID) == nil {
		return errors.New("任务不存在")
	}
	t := p.Task(taskID)
	if t.Status == project.Merged {
		return errors.New("任务已经合并")
	}
	t.Status = project.Cancelled
	return project.SaveTask(p, t)
}
