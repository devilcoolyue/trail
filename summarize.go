package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// 用 `claude -p --model haiku` 给每一轮生成一句话总结和分类，结果缓存在
// <用户缓存目录>/trail/summaries.json，同一轮只总结一次。提示词与插件
// claude-plugin/hooks/summarize.ts 相同。

const summaryBatch = 12

const summarySystem = `You index a coding conversation between a developer and an AI assistant.
For each turn you get the developer's prompt, the assistant's final reply, the tools it ran and the files it changed.
Answer with a JSON array only, one object per turn, in the order given: {"i": <turn number>, "kind": "plan"|"change"|"ask"|"debug", "summary": "..."}.
kind: plan = a design, plan or decision was worked out; change = code or files were changed; debug = an error or failure was diagnosed or fixed; ask = a question was answered or something was explained.
summary: what was decided, found or done, in at most 30 Chinese characters or 18 English words. Write it in the language of that turn's Prompt: a Chinese prompt gets a Chinese summary. State the outcome, not the request. No trailing period.`

type summaryEntry struct {
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
}

var kindByName = map[string]Kind{"plan": KindPlan, "change": KindChange, "ask": KindAsk, "debug": KindDebug}

type summaryCache struct {
	path string
	data map[string]summaryEntry
}

func summaryKey(s *Session, t *Turn) string {
	return fmt.Sprintf("%d:%s:%s", s.Source, s.ID, t.ID)
}

func loadSummaryCache() *summaryCache {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	c := &summaryCache{path: filepath.Join(dir, "trail", "summaries.json"), data: map[string]summaryEntry{}}
	if b, err := os.ReadFile(c.path); err == nil {
		json.Unmarshal(b, &c.data)
	}
	return c
}

func (c *summaryCache) save() {
	if b, err := json.Marshal(c.data); err == nil {
		os.MkdirAll(filepath.Dir(c.path), 0o755)
		os.WriteFile(c.path, b, 0o644)
	}
}

func (c *summaryCache) applyTo(s *Session) {
	for i := range s.Turns {
		t := &s.Turns[i]
		if e, ok := c.data[summaryKey(s, t)]; ok {
			t.Summary = e.Summary
			if k, ok := kindByName[e.Kind]; ok {
				t.Kind = k
			}
			t.Summarized = true
		}
	}
}

// summarizerAvailable：设置 TRAIL_SUMMARIES=off 或找不到 claude 命令时只用规则提取。
func summarizerAvailable() bool {
	if os.Getenv("TRAIL_SUMMARIES") == "off" {
		return false
	}
	_, err := exec.LookPath("claude")
	return err == nil
}

type summariesMsg struct {
	session string // Source:ID
	results map[string]summaryEntry
	rest    []Turn
	done    int
	total   int
	err     error
}

func sessionKey(s *Session) string { return fmt.Sprintf("%d:%s", s.Source, s.ID) }

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func summaryPrompt(turns []Turn) string {
	var b strings.Builder
	for _, t := range turns {
		fmt.Fprintf(&b, "### Turn %d\nPrompt: %s\n", t.Index, clip(t.Prompt, 600))
		answer := t.Answer
		if answer == "" {
			answer = "(no text reply)"
		}
		fmt.Fprintf(&b, "Reply: %s\n", clip(answer, 1200))
		if len(t.Tools) > 0 {
			var parts []string
			for _, tu := range t.Tools {
				parts = append(parts, fmt.Sprintf("%s×%d", tu.Name, tu.Count))
			}
			fmt.Fprintf(&b, "Tools: %s\n", strings.Join(parts, ", "))
		}
		if len(t.Files) > 0 {
			var parts []string
			for _, f := range t.Files {
				parts = append(parts, fmt.Sprintf("%s %s +%d -%d", f.Op, f.Path, f.Add, f.Del))
			}
			fmt.Fprintf(&b, "Files: %s\n", strings.Join(parts, "; "))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func parseSummaries(text string, turns []Turn) map[string]summaryEntry {
	out := map[string]summaryEntry{}
	start, end := strings.Index(text, "["), strings.LastIndex(text, "]")
	if start < 0 || end <= start {
		return out
	}
	var items []struct {
		I       int    `json:"i"`
		Kind    string `json:"kind"`
		Summary string `json:"summary"`
	}
	if json.Unmarshal([]byte(text[start:end+1]), &items) != nil {
		return out
	}
	for _, it := range items {
		for _, t := range turns {
			if t.Index == it.I && strings.TrimSpace(it.Summary) != "" {
				out[t.ID] = summaryEntry{Kind: it.Kind, Summary: clip(strings.TrimSpace(it.Summary), 80)}
			}
		}
	}
	return out
}

// summarizeCmd 总结 todo 里的前一批，剩下的放进消息里，由 Update 接着发起下一批。
func summarizeCmd(key string, todo []Turn, done, total int) tea.Cmd {
	return func() tea.Msg {
		n := min(summaryBatch, len(todo))
		batch, rest := todo[:n], todo[n:]
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "claude", "-p", "--model", "haiku",
			"--no-session-persistence", "--tools", "", "--strict-mcp-config",
			"--system-prompt", summarySystem, summaryPrompt(batch))
		cmd.Dir = os.TempDir()
		out, err := cmd.Output()
		msg := summariesMsg{session: key, rest: rest, done: done + n, total: total, err: err}
		if err == nil {
			msg.results = parseSummaries(string(out), batch)
		}
		return msg
	}
}
