package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestHoverFollowsMouse(t *testing.T) {
	var m tea.Model = newModel(demoSessions(), nil, 0, true)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	cur := m.(model)
	if cur.hover != -1 {
		t.Fatalf("hover = %d before any mouse event", cur.hover)
	}
	// 列表第一轮的第二行
	move := tea.MouseMsg{X: 5, Y: bodyTop + 2, Action: tea.MouseActionMotion, Button: tea.MouseButtonNone}
	m, _ = m.Update(move)
	if got := m.(model).hover; got != cur.top {
		t.Fatalf("hover = %d, want %d", got, cur.top)
	}
	// 移到右边的详情上：不再悬停
	m, _ = m.Update(tea.MouseMsg{X: 130, Y: bodyTop + 2, Action: tea.MouseActionMotion, Button: tea.MouseButtonNone})
	if got := m.(model).hover; got != -1 {
		t.Fatalf("hover = %d over the detail pane", got)
	}
	// 按键清掉悬停
	m, _ = m.Update(move)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := m.(model).hover; got != -1 {
		t.Fatalf("hover = %d after a key", got)
	}
}
