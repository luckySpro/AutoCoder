package dispatch

import (
	"os"
	"testing"
	"time"

	"github.com/luckySpro/AutoCoder/internal/project"
)

// 退出时正在跑的尝试被停下：任务放回队列、不计次数，之后不再派发。
func TestShutdownRequeues(t *testing.T) {
	e := setup(t, "")
	t.Setenv("FAKE_MODE", "slow")
	id := e.addTask(t, project.Task{Title: "慢任务", Acceptance: []string{"x"}, Verify: []string{"true"}})
	e.eng.Schedule(e.ctx)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if r := e.eng.Run(e.p.ID + "/" + id); r != nil && r.Active() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("任务没有开始运行")
		}
		e.eng.Schedule(e.ctx)
		time.Sleep(50 * time.Millisecond)
	}
	start := time.Now()
	e.eng.Shutdown(10 * time.Second)
	if d := time.Since(start); d > 8*time.Second {
		t.Fatalf("退出等了 %s", d)
	}
	e.store.Lock()
	task := *e.p.Task(id)
	e.store.Unlock()
	if task.Status != project.Ready || task.Attempts != 0 {
		t.Fatalf("退出后任务应回到 ready 且不计次数，实际 %s / %d（%s）", task.Status, task.Attempts, task.LastError)
	}
	os.Unsetenv("FAKE_MODE")
	e.eng.Schedule(e.ctx)
	time.Sleep(200 * time.Millisecond)
	if r := e.eng.Run(e.p.ID + "/" + id); r != nil && r.Active() {
		t.Fatal("退出后不应再派发")
	}
}
