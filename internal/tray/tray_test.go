package tray

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luckySpro/AutoCoder/internal/server"
)

func TestBuildMenu(t *testing.T) {
	s := server.Summary{
		Slots:    3,
		Projects: 1,
		Running: []server.RunningItem{
			{ProjectID: "shop admin", ProjectName: "shop-admin", TaskID: "T2", Title: "商品列表页", ToolName: "Codex CLI", Phase: "reviewing"},
		},
		Waiting:     []server.WaitingItem{{ProjectID: "shop admin", ProjectName: "shop-admin", ID: "R3", Text: "计划等你确认"}},
		TodayTokens: 85712, TodayCalls: 7,
		TodayByTool: []server.ToolTokens{{Tool: "claude-code", Name: "Claude Code", Tokens: 73300}, {Tool: "codex", Name: "Codex CLI", Tokens: 12412}},
	}
	m := Build(s, "http://127.0.0.1:7788")
	if m.Header != "运行中 1 / 3" || m.Title != "1" {
		t.Fatalf("%q %q", m.Header, m.Title)
	}
	if got := m.Running[0]; got.Text != "T2 商品列表页 · Codex CLI 评审中 · shop-admin" || got.Link != "http://127.0.0.1:7788/#/projects/shop%20admin" {
		t.Fatalf("%+v", got)
	}
	if m.WaitHeader != "等你处理 1" || m.Waiting[0].Text != "shop-admin · R3 计划等你确认" {
		t.Fatalf("%+v", m)
	}
	if m.Usage != "今日用量 85.7K token · 7 次调用" || m.Tools[0] != "Claude Code  73.3K" {
		t.Fatalf("%q %q", m.Usage, m.Tools)
	}
	if !strings.Contains(m.Quit, "1 个任务会中断") || m.Pause != "暂停派发" {
		t.Fatal(m.Quit, m.Pause)
	}
	if m.Tooltip != "AutoCoder：1 个运行中，1 件等你处理，今日 85.7K token" {
		t.Fatal(m.Tooltip)
	}

	idle := Build(server.Summary{Slots: 3, Projects: 2, Paused: true}, "http://x")
	if idle.Header != "已暂停派发" || idle.Title != "" || idle.Pause != "恢复派发" || idle.WaitHeader != "" || idle.Quit != "退出 AutoCoder" {
		t.Fatalf("%+v", idle)
	}

	var many server.Summary
	for range 8 {
		many.Waiting = append(many.Waiting, server.WaitingItem{ProjectName: "p", Text: strings.Repeat("长", 100)})
	}
	mm := Build(many, "http://x")
	if len(mm.Waiting) != MaxWaiting || mm.More != "还有 3 件，打开界面查看" || len([]rune(mm.Waiting[0].Text)) != 64 {
		t.Fatalf("%+v", mm)
	}
}

func TestTokens(t *testing.T) {
	for n, want := range map[int64]string{0: "0", 999: "999", 1234: "1.2K", 1234567: "1.23M"} {
		if got := Tokens(n); got != want {
			t.Errorf("%d → %s，期望 %s", n, got, want)
		}
	}
}

func TestIcon(t *testing.T) {
	for _, tpl := range []bool{true, false} {
		data := Icon(IconState{Slots: 3, Busy: 2, Waiting: true}, 44, tpl)
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil || img.Bounds().Dx() != 44 {
			t.Fatal(err)
		}
		ico := ICO(data, 44)
		if !bytes.Equal(ico[:6], []byte{0, 0, 1, 0, 1, 0}) || !bytes.Equal(ico[22:], data) {
			t.Fatal("ICO 头不对")
		}
		if dir := os.Getenv("TRAY_ICON_DIR"); dir != "" { // 手动看图
			name := "color"
			if tpl {
				name = "template"
			}
			for _, st := range []IconState{{Slots: 3}, {Slots: 3, Busy: 2, Waiting: true}, {Slots: 3, Busy: 1, Paused: true}} {
				_ = os.WriteFile(filepath.Join(dir, name+"-"+Tokens(int64(st.Busy))+map[bool]string{true: "w", false: ""}[st.Waiting]+map[bool]string{true: "p", false: ""}[st.Paused]+".png"), Icon(st, 128, tpl), 0o644)
			}
		}
	}
}
