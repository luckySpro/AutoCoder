// Package tray 是菜单栏（托盘）图标：显示运行状态、等你处理的事和今天的用量。
//
// menu.go 和 icon.go 不依赖系统托盘库，可以在任何构建里测试；
// 真正画到菜单栏的代码在 systray.go，只在带 tray 标签的桌面版里编译。
package tray

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/luckySpro/AutoCoder/internal/server"
)

// 菜单里预留的行数：系统托盘菜单项不能删除，只能显示或隐藏。
const (
	MaxRunning = 5
	MaxWaiting = 5
	MaxTools   = 4
)

// Item 是一行可点击的菜单项，点开界面里对应的页面。
type Item struct {
	Text string
	Link string
}

// Menu 是某一时刻菜单的全部内容。
type Menu struct {
	Title      string // 图标旁的文字（macOS 菜单栏显示）：运行中的数量
	Tooltip    string
	Header     string
	Running    []Item
	WaitHeader string
	Waiting    []Item
	More       string // 等你处理的还有几件没列出
	Usage      string
	Tools      []string
	Pause      string // 暂停或恢复派发
	Quit       string
	Icon       IconState
}

var phases = map[string]string{
	"running": "开发中", "verifying": "验证中", "reviewing": "评审中", "integrating": "集成中", "planning": "规划中",
}

// Build 由整体状态生成菜单；base 是界面地址，例如 http://127.0.0.1:7788。
func Build(s server.Summary, base string) Menu {
	n := len(s.Running)
	m := Menu{Icon: IconState{Slots: max(s.Slots, 1), Busy: n, Waiting: len(s.Waiting) > 0, Paused: s.Paused}}
	switch {
	case s.Paused && n > 0:
		m.Header = fmt.Sprintf("已暂停派发，还有 %d 个在收尾", n)
	case s.Paused:
		m.Header = "已暂停派发"
	case n > 0:
		m.Header = fmt.Sprintf("运行中 %d / %d", n, s.Slots)
	case s.Projects == 0:
		m.Header = "还没有项目"
	default:
		m.Header = fmt.Sprintf("空闲 · %d 个运行槽", s.Slots)
	}
	if n > 0 {
		m.Title = fmt.Sprint(n)
	}
	for i, r := range s.Running {
		if i == MaxRunning {
			break
		}
		phase := phases[r.Phase]
		if phase == "" {
			phase = r.Phase
		}
		text := fmt.Sprintf("%s %s · %s %s", r.TaskID, r.Title, r.ToolName, phase)
		if r.ProjectName != "" {
			text += " · " + r.ProjectName
		}
		m.Running = append(m.Running, Item{clip(text, 64), projectLink(base, r.ProjectID)})
	}

	if w := len(s.Waiting); w > 0 {
		m.WaitHeader = fmt.Sprintf("等你处理 %d", w)
		for i, it := range s.Waiting {
			if i == MaxWaiting {
				m.More = fmt.Sprintf("还有 %d 件，打开界面查看", w-MaxWaiting)
				break
			}
			text := it.ProjectName + " · " + it.Text
			if it.ID != "" {
				text = it.ProjectName + " · " + it.ID + " " + it.Text
			}
			m.Waiting = append(m.Waiting, Item{clip(text, 64), projectLink(base, it.ProjectID)})
		}
	}

	m.Usage = "今日用量 " + Tokens(s.TodayTokens) + " token"
	if s.TodayCalls > 0 {
		m.Usage += fmt.Sprintf(" · %d 次调用", s.TodayCalls)
	}
	for i, t := range s.TodayByTool {
		if i == MaxTools {
			break
		}
		m.Tools = append(m.Tools, fmt.Sprintf("%s  %s", t.Name, Tokens(t.Tokens)))
	}

	m.Pause = "暂停派发"
	if s.Paused {
		m.Pause = "恢复派发"
	}
	m.Quit = "退出 AutoCoder"
	if n > 0 {
		m.Quit = fmt.Sprintf("退出 AutoCoder（%d 个任务会中断，下次启动继续）", n)
	}

	var tip []string
	if n > 0 {
		tip = append(tip, fmt.Sprintf("%d 个运行中", n))
	} else if s.Paused {
		tip = append(tip, "已暂停")
	} else {
		tip = append(tip, "空闲")
	}
	if w := len(s.Waiting); w > 0 {
		tip = append(tip, fmt.Sprintf("%d 件等你处理", w))
	}
	tip = append(tip, "今日 "+Tokens(s.TodayTokens)+" token")
	m.Tooltip = "AutoCoder：" + strings.Join(tip, "，")
	return m
}

// Tokens 与界面一致：1234 → 1.2K，1234567 → 1.23M。
func Tokens(n int64) string {
	switch {
	case n >= 1e6:
		return fmt.Sprintf("%.2fM", float64(n)/1e6)
	case n >= 1e3:
		return fmt.Sprintf("%.1fK", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}

func projectLink(base, id string) string {
	if id == "" {
		return base + "/"
	}
	return base + "/#/projects/" + url.PathEscape(id)
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
