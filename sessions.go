package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// 与 Claude Code 插件（claude-plugin/hooks/transcript.ts）里的规则保持一致。
var (
	debugRe = regexp.MustCompile(`(?i)报错|错误|失败|异常|崩溃|不生效|不工作|为什么.{0,8}(不|没)|排查|修复|\bbug\b|\berror\b|\bfail|exception|crash|traceback|panic|\bfix\b|doesn'?t work|not working`)
	planRe  = regexp.MustCompile(`(?i)方案|设计|架构|规划|计划|思路|选型|怎么做|如何实现|建议|对比|\bplan\b|\bdesign\b|architect|approach|propos|trade-?off`)
	fenceRe = regexp.MustCompile("(?s)```.*?```")
	linkRe  = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	spaceRe = regexp.MustCompile(`\s+`)
	imageRe = regexp.MustCompile(`\[(?:图片|Image)\s*#\d+[^\]]*\]`)
	// 在终端里粘贴或拖入的图片是一个文件路径
	imagePathRe = regexp.MustCompile(`(?i)(?:/[^\s/]+)+\.(?:png|jpe?g|gif|webp)\b`)
)

func guessKind(prompt string, files []FileChange) Kind {
	switch {
	case debugRe.MatchString(prompt):
		return KindDebug
	case len(files) > 0:
		return KindChange
	case planRe.MatchString(prompt):
		return KindPlan
	}
	return KindAsk
}

// guessSummary 不调模型时的一句话总结：回答的第一句。
func guessSummary(answer, prompt string) string {
	plain := fenceRe.ReplaceAllString(answer, " ")
	plain = linkRe.ReplaceAllString(plain, "$1")
	plain = strings.NewReplacer("`", "", "*", "", "_", "", "#", "", ">", "", "|", "").Replace(plain)
	plain = strings.TrimSpace(spaceRe.ReplaceAllString(plain, " "))
	if plain == "" {
		plain = prompt
	}
	r := []rune(plain)
	for i, c := range r {
		if strings.ContainsRune("。！？!?", c) || (c == '.' && i+1 < len(r) && r[i+1] == ' ') {
			r = r[:i+1]
			break
		}
	}
	if len(r) > 80 {
		r = append(r[:80], '…')
	}
	return string(r)
}

// cleanPrompt 合并空白，把粘贴图片的引用（[图片#1 /path/x.png]、图片路径）缩成 [图片]。
func cleanPrompt(s string) string {
	s = imageRe.ReplaceAllString(s, "[图片]")
	s = imagePathRe.ReplaceAllString(s, "[图片]")
	return strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
}

// finishTurns 补上规则提取的分类、总结和跳转锚点。
func finishTurns(turns []Turn) {
	for i := range turns {
		t := &turns[i]
		t.Index = i + 1
		t.Kind = guessKind(t.Prompt, t.Files)
		t.Summary = guessSummary(t.Answer, t.Prompt)
		// 终端里图片显示成 [Image #1]，和这里的 [图片] 不一样，所以锚点只取文字部分
		t.Anchor = firstRunes(cleanPrompt(strings.ReplaceAll(t.Prompt, "[图片]", " ")), 16)
		if t.Duration == 0 && i+1 < len(turns) {
			t.Duration = turns[i+1].Time.Sub(t.Time)
		}
	}
}

func sessionTitle(turns []Turn) string {
	for _, t := range turns {
		if !t.Rewound {
			return firstRunes(t.Prompt, 40)
		}
	}
	return "（空会话）"
}

func home() string {
	h, _ := os.UserHomeDir()
	return h
}

func shortPath(p string) string {
	if h := home(); h != "" && strings.HasPrefix(p, h) {
		return "~" + strings.TrimPrefix(p, h)
	}
	return p
}

type sessionFile struct {
	source Source
	path   string
	mod    time.Time
}

// recentFiles 列出两边最近修改过的会话文件，新的在前。
func recentFiles(limit int) []sessionFile {
	var files []sessionFile
	claudeDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if claudeDir == "" {
		claudeDir = filepath.Join(home(), ".claude")
	}
	matches, _ := filepath.Glob(filepath.Join(claudeDir, "projects", "*", "*.jsonl"))
	for _, p := range matches {
		if info, err := os.Stat(p); err == nil && info.Size() > 0 {
			files = append(files, sessionFile{SourceClaude, p, info.ModTime()})
		}
	}
	codexDir := os.Getenv("CODEX_HOME")
	if codexDir == "" {
		codexDir = filepath.Join(home(), ".codex")
	}
	filepath.WalkDir(filepath.Join(codexDir, "sessions"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasPrefix(d.Name(), "rollout-") && strings.HasSuffix(p, ".jsonl") {
			if info, err := d.Info(); err == nil && info.Size() > 0 {
				files = append(files, sessionFile{SourceCodex, p, info.ModTime()})
			}
		}
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	if len(files) > limit {
		files = files[:limit]
	}
	return files
}

// loadSessions 读取最近的会话；没有一轮对话的会话不列出。
func loadSessions(limit int) []Session {
	var out []Session
	for _, f := range recentFiles(limit) {
		var s Session
		var err error
		if f.source == SourceCodex {
			s, err = parseCodex(f.path)
		} else {
			s, err = parseClaude(f.path)
		}
		if err != nil || len(s.Turns) == 0 || oneShot(s) {
			continue
		}
		s.Path, s.ModTime = f.path, f.mod
		out = append(out, s)
	}
	return out
}

// oneShot：别的程序发起的一次性非交互调用（比如拿它生成标题），不是终端里的对话。
// Codex 是 codex exec；Claude Code 是 claude -p 这类 SDK 调用里只有一轮、没用工具的会话。
func oneShot(s Session) bool {
	if s.Originator == "codex_exec" {
		return true
	}
	return s.Source == SourceClaude && strings.HasPrefix(s.Originator, "sdk") &&
		len(s.Turns) == 1 && len(s.Turns[0].Tools) == 0
}

// markLive 找出弹窗下方 pane 里正在运行的会话：同一目录下最近修改的那个。
func markLive(sessions []Session, pane string) int {
	if pane == "" {
		return -1
	}
	out, err := exec.Command("tmux", "display-message", "-p", "-t", pane,
		"#{pane_current_command}\t#{pane_current_path}").Output()
	if err != nil {
		return -1
	}
	cmd, dir, _ := strings.Cut(strings.TrimSpace(string(out)), "\t")
	want := SourceCodex
	if strings.Contains(cmd, "claude") {
		want = SourceClaude
	}
	best := -1
	for i, s := range sessions {
		if s.WorkDir != dir {
			continue
		}
		if best < 0 || (s.Source == want && sessions[best].Source != want) {
			best = i
		}
	}
	if best >= 0 {
		sessions[best].Live = true
	}
	return best
}
