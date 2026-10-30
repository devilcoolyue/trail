package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Jumper 负责把终端滚动到某一轮对话在回滚缓冲区中的原始位置。
type Jumper interface {
	Name() string
	// Ready 在跳转前检查能不能跳，不能时返回给用户看的原因。
	Ready() error
	Jump(anchor string) error
}

// tmuxJumper：让目标 pane 进入 copy-mode，从历史顶部向下搜索锚点文本。
// 面板通常以 `tmux display-popup` 打开，关闭后底下的 pane 已停在目标位置。
type tmuxJumper struct{ pane string }

func (j tmuxJumper) Name() string { return "tmux " + j.pane }

// Ready：全屏界面（alternate screen）的内容不进 tmux 历史，搜不到。
func (j tmuxJumper) Ready() error {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", j.pane, "#{alternate_on}").Output()
	if err == nil && strings.TrimSpace(string(out)) == "1" {
		return errors.New(`下方终端是全屏界面，对话不在 tmux 历史里。Codex 请在 ~/.codex/config.toml 加上 [tui] alternate_screen = "never"`)
	}
	return nil
}

func (j tmuxJumper) Jump(anchor string) error {
	steps := [][]string{
		{"copy-mode", "-t", j.pane},
		{"send-keys", "-t", j.pane, "-X", "history-top"},
		{"send-keys", "-t", j.pane, "-X", "search-forward-text", anchor},
	}
	for _, args := range steps {
		if out, err := exec.Command("tmux", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("tmux %s: %v %s", strings.Join(args, " "), err, out)
		}
	}
	// 搜索命中后，把视图下滚让提问出现在屏幕顶部，下面紧跟这一轮的回答。
	// 离历史末尾太近时滚不到顶，就让光标停在提问所在的那一行。
	out, err := exec.Command("tmux", "display-message", "-p", "-t", j.pane,
		"#{copy_cursor_y} #{scroll_position}").Output()
	if err != nil {
		return nil
	}
	var y, sp int
	fmt.Sscan(string(out), &y, &sp)
	scroll := min(y, sp)
	if scroll > 0 {
		exec.Command("tmux", "send-keys", "-t", j.pane, "-N", strconv.Itoa(scroll), "-X", "scroll-down").Run()
	}
	exec.Command("tmux", "send-keys", "-t", j.pane, "-X", "top-line").Run()
	if y-scroll > 0 {
		exec.Command("tmux", "send-keys", "-t", j.pane, "-N", strconv.Itoa(y-scroll), "-X", "cursor-down").Run()
	}
	exec.Command("tmux", "send-keys", "-t", j.pane, "-X", "start-of-line").Run()
	return nil
}

// detectJumper 根据当前环境选择跳转方式；返回 nil 表示无法跳转（演示模式）。
func detectJumper(pane string) Jumper {
	if os.Getenv("TMUX") == "" {
		return nil
	}
	if pane == "" || strings.Contains(pane, "#{") { // 老版本 tmux 的 display-popup 不展开格式
		// 在 display-popup 里执行时，这里拿到的是弹窗下方的 pane。
		out, err := exec.Command("tmux", "display-message", "-p", "#{pane_id}").Output()
		if err != nil {
			return nil
		}
		pane = strings.TrimSpace(string(out))
	}
	return tmuxJumper{pane: pane}
}
