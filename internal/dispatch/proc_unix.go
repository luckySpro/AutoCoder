//go:build !windows

package dispatch

import (
	"os/exec"
	"syscall"
	"time"
)

// setGroup 让工具进程自成一个进程组，结束时连同它启动的子进程一起结束。
func setGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killGroup 先发 SIGTERM，5 秒后仍未退出再发 SIGKILL。
func killGroup(cmd *exec.Cmd, done <-chan struct{}) {
	if cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
}

func shell(command string) (string, []string) { return "sh", []string{"-c", command} }
