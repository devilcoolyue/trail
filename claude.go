package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Claude Code 的会话记录：~/.claude/projects/<目录>/<会话 id>.jsonl。
// 规则与插件的 claude-plugin/hooks/transcript.ts 相同。

type claudeRow struct {
	Type              string          `json:"type"`
	UUID              string          `json:"uuid"`
	ParentUUID        *string         `json:"parentUuid"`
	LogicalParentUUID *string         `json:"logicalParentUuid"`
	IsSidechain       bool            `json:"isSidechain"`
	IsMeta            bool            `json:"isMeta"`
	IsCompactSummary  bool            `json:"isCompactSummary"`
	Entrypoint        string          `json:"entrypoint"`
	Timestamp         string          `json:"timestamp"`
	SessionID         string          `json:"sessionId"`
	Cwd               string          `json:"cwd"`
	GitBranch         string          `json:"gitBranch"`
	Message           json.RawMessage `json:"message"`
	ToolUseResult     json.RawMessage `json:"toolUseResult"`
}

type claudeMessage struct {
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
}

type claudeBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Thinking string `json:"thinking"`
	Name     string `json:"name"`
}

var (
	notAPrompt = regexp.MustCompile(`^\s*(<local-command-|<bash-input>|<bash-stdout>|<task-notification>|\[Request interrupted)`)
	commandRe  = regexp.MustCompile(`<command-name>\s*(/?[^<\s]+)\s*</command-name>`)
	argsRe     = regexp.MustCompile(`(?s)<command-args>(.*?)</command-args>`)
	// 老版本把不带参数的斜杠命令（如 /compact）原样记成一条提问
	bareCommandRe = regexp.MustCompile(`^/[A-Za-z][\w:-]*$`)
)

// claudePrompt 取出用户输入的提问。斜杠命令记成 "/命令 参数"，command 为 true：
// 只有模型接着回复了的命令（/goal、技能等）才算一轮，/clear、/compact 这类不算。
func claudePrompt(r *claudeRow, msg *claudeMessage) (text string, command bool, ok bool) {
	if r.Type != "user" || r.IsMeta || r.IsSidechain || r.IsCompactSummary || r.UUID == "" {
		return "", false, false
	}
	if json.Unmarshal(msg.Content, &text) != nil {
		var blocks []claudeBlock
		if json.Unmarshal(msg.Content, &blocks) != nil || len(blocks) == 0 {
			return "", false, false
		}
		var parts []string
		images := 0
		for _, b := range blocks {
			switch b.Type {
			case "tool_result":
				return "", false, false
			case "text":
				parts = append(parts, b.Text)
			case "image":
				images++
			}
		}
		text = strings.Join(parts, " ")
		if images > 0 {
			text = "[图片] " + text
		}
	}
	if m := commandRe.FindStringSubmatch(text); m != nil {
		name := "/" + strings.TrimPrefix(m[1], "/")
		if a := argsRe.FindStringSubmatch(text); a != nil && strings.TrimSpace(a[1]) != "" {
			name += " " + a[1]
		}
		return cleanPrompt(name), true, true
	}
	if notAPrompt.MatchString(text) {
		return "", false, false
	}
	text = cleanPrompt(text)
	return text, bareCommandRe.MatchString(text), text != ""
}

func isToolResult(msg *claudeMessage) bool {
	var blocks []claudeBlock
	if json.Unmarshal(msg.Content, &blocks) != nil {
		return false
	}
	for _, b := range blocks {
		if b.Type == "tool_result" {
			return true
		}
	}
	return false
}

func claudeFileChange(raw json.RawMessage) (FileChange, bool) {
	var r struct {
		FilePath        string `json:"filePath"`
		Type            string `json:"type"`
		Content         string `json:"content"`
		StructuredPatch []struct {
			Lines []string `json:"lines"`
		} `json:"structuredPatch"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &r) != nil || r.FilePath == "" {
		return FileChange{}, false
	}
	if r.Type == "create" {
		n := strings.Count(r.Content, "\n")
		if r.Content != "" && !strings.HasSuffix(r.Content, "\n") {
			n++
		}
		return FileChange{Op: "A", Path: r.FilePath, Add: n}, true
	}
	if r.StructuredPatch == nil {
		return FileChange{}, false
	}
	fc := FileChange{Op: "M", Path: r.FilePath}
	for _, h := range r.StructuredPatch {
		for _, l := range h.Lines {
			if strings.HasPrefix(l, "+") {
				fc.Add++
			} else if strings.HasPrefix(l, "-") {
				fc.Del++
			}
		}
	}
	return fc, true
}

func parseClaude(path string) (Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return Session{}, err
	}
	defer f.Close()

	var rows []claudeRow
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var r claudeRow
		if json.Unmarshal(sc.Bytes(), &r) == nil {
			rows = append(rows, r)
		}
	}

	// 当前分支：从最后一条对话行沿 parentUuid 往回走
	byID := map[string]*claudeRow{}
	var leaf *claudeRow
	for i := range rows {
		r := &rows[i]
		if r.UUID == "" || r.IsSidechain {
			continue
		}
		byID[r.UUID] = r
		if r.Type == "user" || r.Type == "assistant" {
			leaf = r
		}
	}
	onBranch := map[string]bool{}
	for at := leaf; at != nil && !onBranch[at.UUID]; {
		onBranch[at.UUID] = true
		parent := at.ParentUUID
		if parent == nil {
			parent = at.LogicalParentUUID
		}
		if parent == nil {
			break
		}
		at = byID[*parent]
	}

	s := Session{Source: SourceClaude, ID: strings.TrimSuffix(filepath.Base(path), ".jsonl")}
	var turns []Turn
	var command, answered []bool // 每一轮是不是斜杠命令、模型有没有回复
	tools := map[string]int{}
	flush := func() {
		if len(turns) == 0 {
			return
		}
		t := &turns[len(turns)-1]
		for name, n := range tools {
			t.Tools = append(t.Tools, ToolUse{name, n})
		}
		tools = map[string]int{}
	}

	for i := range rows {
		r := &rows[i]
		if r.IsSidechain {
			continue
		}
		if r.Cwd != "" && s.WorkDir == "" {
			s.WorkDir = r.Cwd
		}
		if r.GitBranch != "" {
			s.GitBranch = r.GitBranch
		}
		if r.Entrypoint != "" && s.Originator == "" {
			s.Originator = r.Entrypoint
		}
		ts, _ := time.Parse(time.RFC3339Nano, r.Timestamp)
		var msg claudeMessage
		json.Unmarshal(r.Message, &msg)

		if prompt, cmd, ok := claudePrompt(r, &msg); ok {
			flush()
			turns = append(turns, Turn{ID: r.UUID, Time: ts, Prompt: prompt, Rewound: !onBranch[r.UUID]})
			command, answered = append(command, cmd), append(answered, false)
			continue
		}
		if len(turns) == 0 {
			continue
		}
		// <synthetic> 是 Claude Code 自己补的占位回复（如 "No response requested."），可能晚几个小时才写
		if r.Type == "assistant" && strings.HasPrefix(msg.Model, "<") {
			continue
		}
		t := &turns[len(turns)-1]
		// 只用模型回复和工具结果的时间算用时：排队下一条消息、之后执行本地命令时
		// 写的说明等记录可能晚好几天
		if (r.Type == "assistant" || isToolResult(&msg)) && !ts.IsZero() && ts.After(t.Time) {
			t.Duration = ts.Sub(t.Time)
		}
		switch r.Type {
		case "assistant":
			answered[len(turns)-1] = true
			if msg.Model != "" {
				s.Model = msg.Model
			}
			var blocks []claudeBlock
			json.Unmarshal(msg.Content, &blocks)
			var texts []string
			for _, b := range blocks {
				switch b.Type {
				case "text":
					if strings.TrimSpace(b.Text) != "" {
						texts = append(texts, strings.TrimSpace(b.Text))
					}
				case "thinking":
					t.ThinkTok += len([]rune(b.Thinking))
				case "tool_use":
					tools[b.Name]++
				}
			}
			if len(texts) > 0 {
				t.Answer = strings.Join(texts, "\n\n")
			}
		case "user":
			if fc, ok := claudeFileChange(r.ToolUseResult); ok {
				t.Files = mergeFiles(t.Files, []FileChange{fc})
			}
		}
	}
	flush()
	kept := turns[:0]
	for i, t := range turns {
		if !command[i] || answered[i] {
			kept = append(kept, t)
		}
	}
	turns = kept
	finishTurns(turns)
	s.Turns = turns
	s.Project = shortPath(s.WorkDir)
	s.Title = sessionTitle(turns)
	if len(turns) > 0 {
		s.Start = turns[0].Time
	}
	return s, sc.Err()
}
