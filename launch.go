package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

// trail codex：在 trail 专用的 tmux 里启动 Codex，按 Ctrl-] 弹出面板。
// 用户不用自己配置 tmux 和 Codex，用法和直接运行 codex 一样。

const (
	popupKey    = "C-]"
	popupKeyTip = "Ctrl-]"
	tmuxSocket  = "trail"
)

func runCodex(args []string) error {
	codex, err := exec.LookPath("codex")
	if err != nil {
		return errors.New("找不到 codex 命令。先安装 Codex：npm i -g @openai/codex 或 brew install --cask codex")
	}
	if err := checkTmux(); err != nil {
		return err
	}
	popup, err := popupCommand()
	if err != nil {
		return err
	}
	// --no-alt-screen：对话留在 tmux 历史里，跳转才搜得到。
	argv := append([]string{"codex", "--no-alt-screen"}, args...)

	if os.Getenv("TMUX") != "" {
		// 已经在 tmux 里：直接在当前 pane 运行。快捷键只绑到正在运行的 tmux，不改配置文件。
		if out, _ := exec.Command("tmux", "list-keys", "-T", "root", popupKey).Output(); len(out) == 0 {
			exec.Command("tmux", append([]string{"bind-key", "-n", popupKey}, popup...)...).Run()
		}
		fmt.Fprintf(os.Stderr, "trail: 按 %s 打开对话索引\n", popupKeyTip)
		return syscall.Exec(codex, argv, os.Environ())
	}

	conf, err := writeTmuxConf(popup)
	if err != nil {
		return err
	}
	dir, _ := os.Getwd()
	cmd := []string{"-L", tmuxSocket, "-f", conf, "new-session", "-c", dir}
	// 服务器可能是从别的终端启动的，环境变量以这次为准（API key、PATH 等）。
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "TMUX", "TMUX_PANE", "TERM", "TERM_PROGRAM", "TERM_PROGRAM_VERSION", "SHLVL", "_", "PWD", "OLDPWD":
			continue
		}
		cmd = append(cmd, "-e", kv)
	}
	cmd = append(cmd, "--", codex)
	cmd = append(cmd, argv[1:]...)

	tmux := exec.Command("tmux", cmd...)
	tmux.Stdin, tmux.Stdout, tmux.Stderr = os.Stdin, os.Stdout, os.Stderr
	err = tmux.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		os.Exit(exit.ExitCode())
	}
	return err
}

// writeTmuxConf 写出 trail 专用 tmux 的配置：先读系统和用户自己的配置，再盖上 trail 需要的设置。
// 配置只在服务器启动时读一次，所以每次都重写，保证 trail 换了位置也能用。
func writeTmuxConf(popup []string) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	dir = filepath.Join(dir, "trail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	bind := strings.Join(popup, " ")
	conf := strings.Join([]string{
		"# trail codex 自动生成，每次启动都会覆盖",
		"source-file -q /etc/tmux.conf",
		"source-file -q ~/.tmux.conf",
		"source-file -q ~/.config/tmux/tmux.conf",
		"set -g history-limit 200000      # 默认只留 2000 行，长对话前面的轮次会跳不到",
		"set -g escape-time 10            # 默认 500ms，Codex 里按 Esc 会明显卡一下",
		"set -g mouse on                  # 滚轮翻看历史",
		"set -g destroy-unattached on     # 关掉终端窗口就结束，和直接运行 codex 一样",
		"set -g status on",
		"set -g status-position bottom",
		"set -g status-style bg=default,fg=colour244",
		"set -g status-left ''",
		"set -g status-right ' " + popupKeyTip + " 对话索引 · 滚轮翻历史 '",
		"set -g window-status-format ''",
		"set -g window-status-current-format ''",
		"bind-key -n " + popupKey + " " + bind,
		"bind-key t " + bind,
		"",
	}, "\n")
	path := filepath.Join(dir, "tmux.conf")
	return path, os.WriteFile(path, []byte(conf), 0o644)
}

// popupCommand 是绑到快捷键上的 tmux 命令，用 trail 自己的完整路径，不依赖 tmux 里的 PATH。
func popupCommand() ([]string, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if p, err := filepath.EvalSymlinks(self); err == nil {
		self = p
	}
	return []string{"display-popup", "-E", "-w", "92%", "-h", "88%", shellQuote(self)}, nil
}

func shellQuote(s string) string {
	if regexp.MustCompile(`^[\w./-]+$`).MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

var tmuxVersionRe = regexp.MustCompile(`(\d+)\.(\d+)`)

// checkTmux：弹窗用的 display-popup 需要 tmux 3.2 及以上。
func checkTmux() error {
	out, err := exec.Command("tmux", "-V").Output()
	if err != nil {
		return errors.New("找不到 tmux。macOS 用 brew install tmux 安装")
	}
	m := tmuxVersionRe.FindStringSubmatch(string(out))
	if m == nil {
		return nil // next-3.5 这类开发版，不拦
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	if major < 3 || (major == 3 && minor < 2) {
		return fmt.Errorf("需要 tmux 3.2 及以上，当前是 %s。macOS 用 brew upgrade tmux 升级", strings.TrimSpace(string(out)))
	}
	return nil
}
