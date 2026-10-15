package dispatch

import (
	"sync"
	"time"
)

// 内存中每次尝试保留的输出条数（DESIGN §6.4：每个进程最近 5000 行）。
const (
	maxRaw  = 5000
	maxActs = 1000
	replay  = 200
)

// Event 是推给界面的一条输出。raw 是原始输出的一行，act 是解析出的动作，stat 是状态栏数字的变化。
type Event struct {
	Seq  int64  `json:"seq"`
	At   int64  `json:"at"` // 毫秒时间戳
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	Tone string `json:"tone,omitempty"` // ok | bad | dim | cmd | warn
	Err  bool   `json:"err,omitempty"`  // 来自标准错误
	Kind string `json:"kind,omitempty"` // act：say | read | edit | run | usage | warn
	Meta string `json:"meta,omitempty"`
	Stat *Stat  `json:"stat,omitempty"`
}

// Stat 是一次尝试的状态栏数据。
type Stat struct {
	ProjectID    string   `json:"projectId"`
	TaskID       string   `json:"taskId"`
	Tool         string   `json:"tool"`
	Role         string   `json:"role"`
	Attempt      int      `json:"attempt"`
	StartedAt    int64    `json:"startedAt"`
	LastActivity int64    `json:"lastActivity"`
	Phase        string   `json:"phase"` // running | verifying | integrating | done | failed | stopped
	Files        []string `json:"files"`
	Added        int      `json:"added"`
	Removed      int      `json:"removed"`
	Tokens       int64    `json:"tokens"`
	Exit         *int     `json:"exit,omitempty"`
	Result       string   `json:"result,omitempty"` // 结束后的一句话结论
}

// Run 是一次尝试：输出缓冲、订阅者与状态。
type Run struct {
	mu     sync.Mutex
	stat   Stat
	events []Event // raw 与 act 混排，按 seq 递增
	nRaw   int
	nAct   int
	seq    int64
	subs   map[chan Event]struct{}
	cancel func(reason string)
	stopBy string // 用户停止 / 卡住 / 超时
	slot   int    // 运行槽编号，决定 AUTOCODER_PORT_BASE
}

func newRun(st Stat) *Run {
	return &Run{stat: st, subs: map[chan Event]struct{}{}}
}

// Key 是 项目/任务。
func (r *Run) Key() string { return r.stat.ProjectID + "/" + r.stat.TaskID }

// Snapshot 返回当前状态。
func (r *Run) Snapshot() Stat {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.stat
	s.Files = append([]string(nil), r.stat.Files...)
	return s
}

// Active 表示进程仍在运行或在验证中（占用运行槽）。
func (r *Run) Active() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch r.stat.Phase {
	case "running", "verifying", "integrating":
		return true
	}
	return false
}

func (r *Run) emit(e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.emitLocked(e)
}

func (r *Run) emitLocked(e Event) {
	r.seq++
	e.Seq = r.seq
	if e.At == 0 {
		e.At = time.Now().UnixMilli()
	}
	if e.Type != "stat" {
		r.stat.LastActivity = e.At
		r.events = append(r.events, e)
		if e.Type == "raw" {
			r.nRaw++
		} else {
			r.nAct++
		}
		r.trim()
	}
	for ch := range r.subs {
		select {
		case ch <- e:
		default:
			// 浏览器跟不上：断开，让它带着序号重连补齐
			delete(r.subs, ch)
			close(ch)
		}
	}
}

func (r *Run) trim() {
	if r.nRaw <= maxRaw && r.nAct <= maxActs {
		return
	}
	out := r.events[:0]
	dropRaw, dropAct := r.nRaw-maxRaw, r.nAct-maxActs
	for _, e := range r.events {
		if e.Type == "raw" && dropRaw > 0 {
			dropRaw--
			r.nRaw--
			continue
		}
		if e.Type == "act" && dropAct > 0 {
			dropAct--
			r.nAct--
			continue
		}
		out = append(out, e)
	}
	r.events = out
}

// update 修改状态栏并推送一条 stat 事件。
func (r *Run) update(f func(*Stat)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f(&r.stat)
	s := r.stat
	s.Files = append([]string(nil), r.stat.Files...)
	r.emitLocked(Event{Type: "stat", Stat: &s})
}

// touch 记录一次活动（例如文件有变化）。
func (r *Run) touch() {
	r.mu.Lock()
	r.stat.LastActivity = time.Now().UnixMilli()
	r.mu.Unlock()
}

// Subscribe 返回 after 之后的缓冲事件，并订阅后续事件。after 为 0 时只补发最近一段。
func (r *Run) Subscribe(after int64) ([]Event, chan Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var backlog []Event
	if after > r.seq {
		after = 0 // 已经是新的一次尝试，序号重新开始
	}
	if after > 0 {
		for _, e := range r.events {
			if e.Seq > after {
				backlog = append(backlog, e)
			}
		}
	} else {
		// 最近 200 行原始输出 + 同一时间段之后的全部动作
		raws := 0
		start := 0
		for i := len(r.events) - 1; i >= 0; i-- {
			if r.events[i].Type == "raw" {
				raws++
				if raws > replay {
					start = i + 1
					break
				}
			}
		}
		for _, e := range r.events[:start] {
			if e.Type == "act" {
				backlog = append(backlog, e)
			}
		}
		backlog = append(backlog, r.events[start:]...)
	}
	s := r.stat
	s.Files = append([]string(nil), r.stat.Files...)
	backlog = append([]Event{{Seq: r.seq, At: time.Now().UnixMilli(), Type: "stat", Stat: &s}}, backlog...)
	ch := make(chan Event, 2048)
	r.subs[ch] = struct{}{}
	return backlog, ch
}

// Unsubscribe 取消订阅。
func (r *Run) Unsubscribe(ch chan Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.subs[ch]; ok {
		delete(r.subs, ch)
		close(ch)
	}
}

// Stop 结束进程组；reason 写进汇报。
func (r *Run) Stop(reason string) bool {
	r.mu.Lock()
	cancel := r.cancel
	if cancel == nil || r.stopBy != "" {
		r.mu.Unlock()
		return false
	}
	r.stopBy = reason
	r.mu.Unlock()
	cancel(reason)
	return true
}

func (r *Run) stoppedBy() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stopBy
}
