package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

// version 在发布构建时用 -ldflags "-X main.version=v0.1.0" 写入。
var version = "dev"

func main() {
	pane := flag.String("pane", "", "要跳转的 tmux pane（默认取弹窗下方的 pane）")
	demo := flag.Bool("demo", false, "使用内置的演示数据")
	limit := flag.Int("n", 40, "最多读取最近的几个会话")
	showVersion := flag.Bool("version", false, "显示版本号")
	flag.Parse()
	if *showVersion {
		fmt.Println("trail", version)
		return
	}

	jumper := detectJumper(*pane)
	start := 0
	var sessions []Session
	if *demo {
		sessions = demoSessions()
	} else {
		sessions = loadSessions(*limit)
		if tj, ok := jumper.(tmuxJumper); ok {
			if live := markLive(sessions, tj.pane); live >= 0 {
				start = live
			}
		}
	}
	if len(sessions) == 0 {
		fmt.Fprintln(os.Stderr, "trail: 没有找到 Codex 或 Claude Code 的会话记录（~/.codex/sessions、~/.claude/projects）。用 --demo 查看演示。")
		os.Exit(1)
	}

	m := newModel(sessions, jumper, start, *demo)
	res, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "trail:", err)
		os.Exit(1)
	}
	if fm, ok := res.(model); ok && fm.jumpAnchor != "" {
		if err := jumper.Jump(fm.jumpAnchor); err != nil {
			fmt.Fprintln(os.Stderr, "trail: 跳转失败:", err)
			os.Exit(1)
		}
	}
}
