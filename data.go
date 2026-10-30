package main

import "time"

type Source int

const (
	SourceClaude Source = iota
	SourceCodex
)

func (s Source) Name() string {
	if s == SourceCodex {
		return "Codex"
	}
	return "Claude Code"
}

func (s Source) Icon() string {
	if s == SourceCodex {
		return "◎"
	}
	return "✻"
}

// Kind 是每一轮对话的归类，用于筛选和着色。
type Kind int

const (
	KindAll Kind = iota // 仅用于筛选
	KindPlan
	KindChange
	KindAsk
	KindDebug
)

var kindOrder = []Kind{KindAll, KindPlan, KindChange, KindAsk, KindDebug}

func (k Kind) Label() string {
	switch k {
	case KindPlan:
		return "方案"
	case KindChange:
		return "改动"
	case KindAsk:
		return "提问"
	case KindDebug:
		return "排错"
	}
	return "全部"
}

type ToolUse struct {
	Name  string
	Count int
}

type FileChange struct {
	Op   string // A / M / D
	Path string
	Add  int
	Del  int
}

type Turn struct {
	// ID 标识这一轮：Claude Code 是提问那一行的 uuid，Codex 是会话内序号。
	ID       string
	Index    int
	Time     time.Time
	Duration time.Duration
	Kind     Kind
	Prompt   string
	Summary  string // 一句话总结
	Answer   string // AI 最终回复（支持 ``` 代码块）
	Thinking string
	ThinkTok int // 思考内容的字数
	Tools    []ToolUse
	Files    []FileChange
	// 分支：Rewound 表示这一轮后来被回退（Esc Esc / 编辑重发），不在主线上。
	Rewound bool
	// Anchor 是在终端回滚缓冲区里定位这一轮时搜索的文本。
	Anchor string
	Pinned bool
	// Summarized 表示 Kind / Summary 来自模型总结，而不是规则提取。
	Summarized bool
}

type Session struct {
	ID        string
	Source    Source
	Project   string
	GitBranch string
	Model     string
	Title     string
	Start     time.Time
	// Live 表示该会话正运行在弹出面板下方的终端里，可以直接跳转回滚位置；
	// 其它会话只能通过 resume 打开。
	Live  bool
	Turns []Turn
	// Path 是会话记录文件，ModTime 是它最后修改的时间。
	Path    string
	ModTime time.Time
	WorkDir string // 会话的工作目录（绝对路径），用来匹配 tmux pane
	// Originator 是会话的发起方。Codex：codex-tui、codex_exec 等；
	// Claude Code：entrypoint，cli 或 sdk-cli 等。
	Originator string
}

func (s *Session) Elapsed() time.Duration {
	if len(s.Turns) == 0 {
		return 0
	}
	last := s.Turns[len(s.Turns)-1]
	return last.Time.Add(last.Duration).Sub(s.Start)
}

// LastActive 是会话最后一次有动静的时间，会话列表按它排序和分组。
func (s *Session) LastActive() time.Time {
	if !s.ModTime.IsZero() {
		return s.ModTime
	}
	return s.Start
}

func (s *Session) ResumeCmd() string {
	if s.Source == SourceCodex {
		return "codex resume " + s.ID
	}
	return "claude --resume " + s.ID
}
