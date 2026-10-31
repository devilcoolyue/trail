package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

// version 在发布构建时用 -ldflags "-X main.version=v0.1.0" 写入。
var version = "dev"

var subcommands = map[string]func(args []string) error{
	"codex": runCodex,
	"setup": func([]string) error { return runSetup() },
}

func usage() {
	fmt.Fprint(os.Stderr, `用法：
  trail codex [codex 参数]   在 trail 专用的 tmux 里启动 Codex，按 Ctrl-] 打开对话索引
  trail setup               自己用 tmux 的话：配置 ~/.codex/config.toml 和 ~/.tmux.conf
  trail [参数]               打开对话索引面板（通常由快捷键在 tmux 弹窗里打开）

参数：
`)
	flag.PrintDefaults()
}

func main() {
	if len(os.Args) > 1 && subcommands[os.Args[1]] != nil {
		if err := subcommands[os.Args[1]](os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "trail:", err)
			os.Exit(1)
		}
		return
	}
	flag.Usage = usage
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
	// AllMotion：不按键移动鼠标也会收到事件，用来高亮鼠标所在的行
	res, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseAllMotion()).Run()
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
