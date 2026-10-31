package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// trail setup：给自己用 tmux 的人一次配好 Codex 和 ~/.tmux.conf。
// 用 trail codex 启动的话不需要这一步。

func runSetup() error {
	if err := checkTmux(); err != nil {
		return err
	}
	popup, err := popupCommand()
	if err != nil {
		return err
	}

	codexDir := os.Getenv("CODEX_HOME")
	if codexDir == "" {
		codexDir = filepath.Join(home(), ".codex")
	}
	codexConf := filepath.Join(codexDir, "config.toml")
	changed, err := editFile(codexConf, setAlternateScreen)
	if err != nil {
		return err
	}
	report(changed, codexConf, `[tui] alternate_screen = "never"（Codex 不进全屏界面，对话留在 tmux 历史里）`)

	tmuxConf := filepath.Join(home(), ".tmux.conf")
	bind := strings.Join(popup, " ")
	changed, err = editFile(tmuxConf, func(s string) string { return addTmuxBindings(s, bind) })
	if err != nil {
		return err
	}
	report(changed, tmuxConf, fmt.Sprintf("prefix + t 和 %s 打开面板", popupKeyTip))

	// 正在运行的 tmux 不会自己重新读配置。
	if exec.Command("tmux", "has-session").Run() == nil {
		if out, err := exec.Command("tmux", "source-file", tmuxConf).CombinedOutput(); err != nil {
			fmt.Printf("! tmux source-file 失败：%s\n", strings.TrimSpace(string(out)))
		} else {
			fmt.Println("✓ 已让正在运行的 tmux 重新读取配置")
		}
	}
	if _, err := exec.LookPath("claude"); err != nil {
		fmt.Println("! 没找到 claude 命令：每轮总结改用规则提取（装了 Claude Code 才会调用 haiku）")
	}
	fmt.Printf("\n现在运行 tmux，在里面启动 codex，按 %s 打开对话索引。\n", popupKeyTip)
	fmt.Println("不想自己管 tmux 的话，直接用 trail codex 代替 codex 启动也行。")
	return nil
}

func report(changed bool, path, what string) {
	if changed {
		fmt.Printf("✓ 已写入 %s：%s\n", path, what)
	} else {
		fmt.Printf("✓ %s 已经配置好：%s\n", path, what)
	}
}

// editFile 改写文件内容；内容有变化时先备份成 <文件>.trail-bak。
func editFile(path string, edit func(string) string) (bool, error) {
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	next := edit(string(old))
	if next == string(old) {
		return false, nil
	}
	if len(old) > 0 {
		if err := os.WriteFile(path+".trail-bak", old, 0o644); err != nil {
			return false, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, []byte(next), 0o644)
}

var (
	tableRe     = regexp.MustCompile(`^\s*\[`)
	altScreenRe = regexp.MustCompile(`^\s*alternate_screen\s*=`)
)

// setAlternateScreen 在 [tui] 表里设置 alternate_screen = "never"，其余内容原样保留。
func setAlternateScreen(conf string) string {
	const line = `alternate_screen = "never"`
	lines := strings.Split(conf, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) != "[tui]" {
			continue
		}
		for j := i + 1; j < len(lines) && !tableRe.MatchString(lines[j]); j++ {
			if altScreenRe.MatchString(lines[j]) {
				lines[j] = line
				return strings.Join(lines, "\n")
			}
		}
		lines = append(lines[:i+1], append([]string{line}, lines[i+1:]...)...)
		return strings.Join(lines, "\n")
	}
	if conf != "" && !strings.HasSuffix(conf, "\n") {
		conf += "\n"
	}
	if conf != "" {
		conf += "\n"
	}
	return conf + "[tui]\n" + line + "\n"
}

const tmuxMarker = "# trail："

// addTmuxBindings 追加快捷键；已经有 trail 的配置就不动。
func addTmuxBindings(conf, popup string) string {
	if strings.Contains(conf, tmuxMarker) {
		return conf
	}
	if conf != "" && !strings.HasSuffix(conf, "\n") {
		conf += "\n"
	}
	if conf != "" {
		conf += "\n"
	}
	conf += tmuxMarker + "prefix + t 或 " + popupKeyTip + " 打开 Codex 对话索引\n"
	conf += "bind-key t " + popup + "\n"
	conf += "bind-key -n " + popupKey + " " + popup + "\n"
	if !regexp.MustCompile(`(?m)^\s*set(-option)?\s+(-g\s+)?history-limit`).MatchString(conf) {
		conf += "set -g history-limit 200000  # 默认只留 2000 行，长对话前面的轮次会跳不到\n"
	}
	return conf
}
