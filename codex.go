package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

// Codex 的会话记录：~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl，每行一个
// {"timestamp", "type", "payload"}。type 为 session_meta / turn_context /
// event_msg / response_item / compacted。

type codexLine struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type codexContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type codexPayload struct {
	Type string `json:"type"`
	// session_meta
	ID         string `json:"id"`
	Cwd        string `json:"cwd"`
	Originator string `json:"originator"`
	Git        *struct {
		Branch string `json:"branch"`
	} `json:"git"`
	// turn_context
	Model string `json:"model"`
	// event_msg: agent_message / task_complete
	Message          string `json:"message"`
	LastAgentMessage string `json:"last_agent_message"`
	// response_item: message / reasoning / function_call / custom_tool_call
	Role    string         `json:"role"`
	Phase   string         `json:"phase"`
	Content []codexContent `json:"content"`
	// reasoning 里是 [{text}]，turn_context 里是字符串（"auto"），所以先不解开
	Summary   json.RawMessage `json:"summary"`
	Name      string          `json:"name"`
	Arguments string          `json:"arguments"`
	Input     string          `json:"input"`
	Action    *struct {
		Command []string `json:"command"`
	} `json:"action"`
}

// Codex 自己注入、不是用户输入的消息片段。
var codexInjected = []string{"<environment_context>", "<user_instructions>", "<permissions", "# AGENTS.md", "<turn_aborted>", "<user_shell_command>"}

func isInjected(s string) bool {
	t := strings.TrimSpace(s)
	for _, p := range codexInjected {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// codexPrompt 从一条 user 消息里取出用户真正输入的文字：去掉注入的上下文和
// 图片的 <image> 标签，有图片时加上 [图片]。不是用户输入时返回 false。
func codexPrompt(content []codexContent) (string, bool) {
	var parts []string
	images := 0
	for _, c := range content {
		t := strings.TrimSpace(c.Text)
		switch {
		case c.Type == "input_image":
			images++
		case strings.HasPrefix(t, "<image") || t == "</image>" || t == "":
		case isInjected(t):
		default:
			parts = append(parts, t)
		}
	}
	text := cleanPrompt(strings.Join(parts, " "))
	if images > 0 && !strings.Contains(text, "[图片]") {
		text = cleanPrompt("[图片] " + text)
	}
	return text, text != ""
}

// 新版 Codex 的工具调用包在一段 JS（exec 工具）里，形如 tools.exec_command({...})。
var nestedToolRe = regexp.MustCompile(`tools\.(\w+)\(`)

func parseCodex(path string) (Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return Session{}, err
	}
	defer f.Close()

	s := Session{Source: SourceCodex}
	var turns []Turn
	tools := map[string]int{}
	hasFinal := false // 这一轮已经拿到 final_answer，后面的 commentary 不再覆盖
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

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var l codexLine
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		var p codexPayload
		if json.Unmarshal(l.Payload, &p) != nil {
			continue
		}
		ts, _ := time.Parse(time.RFC3339Nano, l.Timestamp)

		switch {
		case l.Type == "session_meta":
			s.ID, s.WorkDir, s.Originator = p.ID, p.Cwd, p.Originator
			if p.Git != nil {
				s.GitBranch = p.Git.Branch
			}
			if s.Start.IsZero() {
				s.Start = ts
			}
			continue
		case l.Type == "turn_context":
			if p.Model != "" {
				s.Model = p.Model
			}
			continue
		case l.Type == "response_item" && p.Type == "message" && p.Role == "user":
			if prompt, ok := codexPrompt(p.Content); ok {
				flush()
				hasFinal = false
				turns = append(turns, Turn{ID: fmt.Sprint(len(turns) + 1), Time: ts, Prompt: prompt})
			}
			continue
		}
		if len(turns) == 0 {
			continue
		}
		t := &turns[len(turns)-1]
		// 只用模型输出和工具调用的时间算用时：恢复会话时 Codex 会在文件末尾追加
		// 新时间戳的设置记录，不能算进最后一轮
		if l.Type == "response_item" && !ts.IsZero() && ts.After(t.Time) {
			t.Duration = ts.Sub(t.Time)
		}

		switch l.Type {
		case "event_msg":
			switch p.Type {
			case "agent_message":
				if !hasFinal && strings.TrimSpace(p.Message) != "" {
					t.Answer = strings.TrimSpace(p.Message)
				}
			case "task_complete":
				if strings.TrimSpace(p.LastAgentMessage) != "" {
					t.Answer = strings.TrimSpace(p.LastAgentMessage)
					hasFinal = true
				}
			}
		case "response_item":
			switch p.Type {
			case "message":
				var text strings.Builder
				for _, c := range p.Content {
					text.WriteString(c.Text)
				}
				answer := strings.TrimSpace(text.String())
				if p.Role == "assistant" && answer != "" && (p.Phase == "final_answer" || !hasFinal) {
					t.Answer = answer
					hasFinal = hasFinal || p.Phase == "final_answer"
				}
			case "reasoning":
				var summary []struct {
					Text string `json:"text"`
				}
				json.Unmarshal(p.Summary, &summary)
				for _, r := range summary {
					t.ThinkTok += len([]rune(r.Text))
				}
			case "function_call", "custom_tool_call", "local_shell_call":
				nested := nestedToolRe.FindAllStringSubmatch(p.Input+p.Arguments, -1)
				for _, m := range nested {
					tools[m[1]]++
				}
				if len(nested) == 0 {
					name := p.Name
					if name == "" {
						name = "shell"
					}
					tools[name]++
				}
				if patch := patchText(p); patch != "" {
					t.Files = mergeFiles(t.Files, parsePatch(patch))
				}
			}
		}
	}
	flush()
	finishTurns(turns)
	s.Turns = turns
	s.Project = shortPath(s.WorkDir)
	s.Title = sessionTitle(turns)
	if s.Start.IsZero() && len(turns) > 0 {
		s.Start = turns[0].Time
	}
	return s, sc.Err()
}

// patchText 取出 apply_patch 的补丁文本。补丁可能直接是工具的 input，也可能
// 在函数参数、shell 命令里，或者作为 JS 字符串写在 exec 工具的代码里（换行被转义成 \n）。
func patchText(p codexPayload) string {
	src := p.Input + "\n" + p.Arguments
	if p.Action != nil {
		src += "\n" + strings.Join(p.Action.Command, "\n")
	}
	i := strings.Index(src, "*** Begin Patch")
	if i < 0 {
		return ""
	}
	patch := src[i:]
	if j := strings.Index(patch, "*** End Patch"); j >= 0 {
		patch = patch[:j+len("*** End Patch")]
	}
	if !strings.Contains(patch, "\n") || strings.Contains(patch, `\n*** `) {
		// JSON 或 JS 字符串里的补丁：还原转义
		var unq string
		if json.Unmarshal([]byte(`"`+patch+`"`), &unq) == nil {
			return unq
		}
		return strings.NewReplacer(`\n`, "\n", `\"`, `"`, `\\`, `\`).Replace(patch)
	}
	return patch
}

// parsePatch 统计 apply_patch 格式补丁里每个文件增删的行数。
func parsePatch(patch string) []FileChange {
	var out []FileChange
	var cur *FileChange
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "*** Add File: "):
			out = append(out, FileChange{Op: "A", Path: strings.TrimPrefix(line, "*** Add File: ")})
		case strings.HasPrefix(line, "*** Update File: "):
			out = append(out, FileChange{Op: "M", Path: strings.TrimPrefix(line, "*** Update File: ")})
		case strings.HasPrefix(line, "*** Delete File: "):
			out = append(out, FileChange{Op: "D", Path: strings.TrimPrefix(line, "*** Delete File: ")})
		case strings.HasPrefix(line, "*** Move to: "):
			if cur != nil {
				cur.Path = strings.TrimPrefix(line, "*** Move to: ")
			}
			continue
		case strings.HasPrefix(line, "***"):
			continue
		case cur != nil && strings.HasPrefix(line, "+"):
			cur.Add++
			continue
		case cur != nil && strings.HasPrefix(line, "-"):
			cur.Del++
			continue
		default:
			continue
		}
		cur = &out[len(out)-1]
	}
	return out
}

func mergeFiles(into, add []FileChange) []FileChange {
	for _, a := range add {
		found := false
		for i := range into {
			if into[i].Path == a.Path {
				into[i].Add += a.Add
				into[i].Del += a.Del
				found = true
				break
			}
		}
		if !found {
			into = append(into, a)
		}
	}
	return into
}
