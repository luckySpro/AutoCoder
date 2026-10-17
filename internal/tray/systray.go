//go:build tray

package tray

import (
	"runtime"
	"sync"
	"time"

	"fyne.io/systray"

	"github.com/luckySpro/AutoCoder/internal/server"
)

// App 是菜单栏图标需要的外部能力。
type App struct {
	Base     string                // 界面地址
	Summary  func() server.Summary // 当前状态
	SetPause func(bool) error
	Open     func(url string)
	Quit     func() // 用户点了退出：收尾后结束进程
	Every    time.Duration
}

// Run 在当前（主）线程上运行菜单栏图标，直到退出。macOS 要求在主线程调用。
func Run(a App) {
	if a.Every == 0 {
		a.Every = 2 * time.Second
	}
	systray.Run(func() { a.ready() }, func() {})
}

func (a App) ready() {
	systray.SetTooltip("AutoCoder")
	header := systray.AddMenuItem("AutoCoder", "")
	header.Disable()
	running := make([]*systray.MenuItem, MaxRunning)
	for i := range running {
		running[i] = systray.AddMenuItem("", "打开这个项目")
		running[i].Hide()
	}
	systray.AddSeparator()
	waitHead := systray.AddMenuItem("", "")
	waitHead.Disable()
	waitHead.Hide()
	waiting := make([]*systray.MenuItem, MaxWaiting)
	for i := range waiting {
		waiting[i] = systray.AddMenuItem("", "打开这个项目")
		waiting[i].Hide()
	}
	more := systray.AddMenuItem("", "")
	more.Hide()
	usage := systray.AddMenuItem("今日用量", "")
	usage.Disable()
	tools := make([]*systray.MenuItem, MaxTools)
	for i := range tools {
		tools[i] = systray.AddMenuItem("", "")
		tools[i].Disable()
		tools[i].Hide()
	}
	systray.AddSeparator()
	open := systray.AddMenuItem("打开 AutoCoder", "在浏览器中打开界面")
	pause := systray.AddMenuItem("暂停派发", "不再启动新任务，运行中的跑完")
	quit := systray.AddMenuItem("退出 AutoCoder", "")

	var mu sync.Mutex
	var links []string // running 和 waiting 每行对应的链接
	var paused bool
	var lastIcon IconState
	first := true

	apply := func() {
		m := Build(a.Summary(), a.Base)
		next := make([]string, MaxRunning+MaxWaiting)
		for i, it := range m.Running {
			next[i] = it.Link
		}
		for i, it := range m.Waiting {
			next[MaxRunning+i] = it.Link
		}
		mu.Lock()
		links, paused = next, m.Pause == "恢复派发"
		mu.Unlock()
		header.SetTitle(m.Header)
		for i, it := range running {
			if i < len(m.Running) {
				it.SetTitle(m.Running[i].Text)
				it.Show()
			} else {
				it.Hide()
			}
		}
		if m.WaitHeader != "" {
			waitHead.SetTitle(m.WaitHeader)
			waitHead.Show()
		} else {
			waitHead.Hide()
		}
		for i, it := range waiting {
			if i < len(m.Waiting) {
				it.SetTitle(m.Waiting[i].Text)
				it.Show()
			} else {
				it.Hide()
			}
		}
		if m.More != "" {
			more.SetTitle(m.More)
			more.Show()
		} else {
			more.Hide()
		}
		usage.SetTitle(m.Usage)
		for i, it := range tools {
			if i < len(m.Tools) {
				it.SetTitle("    " + m.Tools[i])
				it.Show()
			} else {
				it.Hide()
			}
		}
		pause.SetTitle(m.Pause)
		quit.SetTitle(m.Quit)
		systray.SetTooltip(m.Tooltip)
		if runtime.GOOS == "darwin" {
			systray.SetTitle(m.Title) // 菜单栏图标旁的数字
		}
		if first || m.Icon != lastIcon {
			setIcon(m.Icon)
			lastIcon, first = m.Icon, false
		}
	}
	apply()

	click := func(item *systray.MenuItem, i int) {
		for range item.ClickedCh {
			mu.Lock()
			link := ""
			if i < len(links) {
				link = links[i]
			}
			mu.Unlock()
			if link == "" {
				link = a.Base + "/"
			}
			a.Open(link)
		}
	}
	for i, it := range running {
		go click(it, i)
	}
	for i, it := range waiting {
		go click(it, MaxRunning+i)
	}
	go func() {
		for {
			select {
			case <-open.ClickedCh:
				a.Open(a.Base + "/")
			case <-more.ClickedCh:
				a.Open(a.Base + "/")
			case <-pause.ClickedCh:
				mu.Lock()
				p := paused
				mu.Unlock()
				_ = a.SetPause(!p)
				apply()
			case <-quit.ClickedCh:
				quit.SetTitle("正在退出…")
				quit.Disable()
				a.Quit()
				systray.Quit()
				return
			case <-time.After(a.Every):
				apply()
			}
		}
	}()
}

func setIcon(st IconState) {
	switch runtime.GOOS {
	case "darwin":
		systray.SetTemplateIcon(Icon(st, 44, true), Icon(st, 44, false))
	case "windows":
		systray.SetIcon(ICO(Icon(st, 32, false), 32))
	default:
		systray.SetIcon(Icon(st, 64, false))
	}
}
