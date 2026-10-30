package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseCodex(t *testing.T) {
	s, err := parseCodex("testdata/codex-rollout.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "019a2b7c-0000-7000-8000-000000000001" || s.Model != "gpt-5.5-codex" || s.GitBranch != "fix/pay-callback" {
		t.Fatalf("meta: %+v", s)
	}
	if len(s.Turns) != 3 {
		t.Fatalf("want 3 turns, got %d: %+v", len(s.Turns), s.Turns)
	}
	t1, t2 := s.Turns[0], s.Turns[1]
	if t1.Kind != KindDebug || !strings.Contains(t1.Answer, "RequestLogger") || t1.ThinkTok == 0 {
		t.Errorf("turn 1: %+v", t1)
	}
	if len(t2.Files) != 2 || t2.Files[0].Path != "internal/middleware/logger.go" || t2.Files[0].Add != 3 || t2.Files[0].Del != 2 {
		t.Errorf("turn 2 files: %+v", t2.Files)
	}
	if t2.Files[1].Op != "A" || t2.Files[1].Add != 3 {
		t.Errorf("added file: %+v", t2.Files[1])
	}
	if t2.Kind != KindChange || t2.Anchor != "修一下" {
		t.Errorf("turn 2: kind=%v anchor=%q", t2.Kind, t2.Anchor)
	}
	// 新版格式：图片标签被去掉，回答取 final_answer，exec 工具里的 JS 补丁被还原
	t3 := s.Turns[2]
	if t3.Prompt != "按截图改一下首页 [图片]" || t3.Answer != "已按截图更新首页标题。" {
		t.Errorf("turn 3: prompt=%q answer=%q", t3.Prompt, t3.Answer)
	}
	if len(t3.Files) != 1 || t3.Files[0].Path != "/workspace/index.html" || t3.Files[0].Add != 1 || t3.Files[0].Del != 1 {
		t.Errorf("turn 3 files: %+v", t3.Files)
	}
	names := map[string]int{}
	for _, tu := range t3.Tools {
		names[tu.Name] = tu.Count
	}
	if names["exec_command"] != 1 || names["apply_patch"] != 1 || names["exec"] != 0 {
		t.Errorf("turn 3 tools: %+v", t3.Tools)
	}
}

func TestParseClaudeRewound(t *testing.T) {
	rows := []string{
		`{"type":"user","uuid":"u1","parentUuid":null,"timestamp":"2026-10-30T02:00:00Z","cwd":"/p","gitBranch":"main","message":{"role":"user","content":"给我设计一个幂等方案"}}`,
		`{"type":"assistant","uuid":"a1","parentUuid":"u1","timestamp":"2026-10-30T02:01:00Z","message":{"model":"claude-x","content":[{"type":"text","text":"推荐三层幂等。"}]}}`,
		`{"type":"user","uuid":"u2","parentUuid":"a1","timestamp":"2026-10-30T02:02:00Z","message":{"role":"user","content":"用乐观锁"}}`,
		`{"type":"assistant","uuid":"a2","parentUuid":"u2","timestamp":"2026-10-30T02:03:00Z","message":{"content":[{"type":"text","text":"挡不住插入。"}]}}`,
		`{"type":"user","uuid":"u3","parentUuid":"a1","timestamp":"2026-10-30T02:04:00Z","message":{"role":"user","content":"改用唯一索引"}}`,
		`{"type":"assistant","uuid":"a3","parentUuid":"u3","timestamp":"2026-10-30T02:05:00Z","message":{"content":[{"type":"tool_use","id":"t","name":"Edit"}]}}`,
		`{"type":"user","uuid":"r3","parentUuid":"a3","timestamp":"2026-10-30T02:05:30Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t"}]},"toolUseResult":{"filePath":"/p/repo.go","structuredPatch":[{"lines":[" a","+b","-c"]}]}}`,
	}
	path := filepath.Join(t.TempDir(), "s1.jsonl")
	os.WriteFile(path, []byte(strings.Join(rows, "\n")), 0o644)
	s, err := parseClaude(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Turns) != 3 || s.ID != "s1" || s.WorkDir != "/p" {
		t.Fatalf("%+v", s)
	}
	if s.Turns[0].Rewound || !s.Turns[1].Rewound || s.Turns[2].Rewound {
		t.Errorf("rewound flags: %v %v %v", s.Turns[0].Rewound, s.Turns[1].Rewound, s.Turns[2].Rewound)
	}
	if f := s.Turns[2].Files; len(f) != 1 || f[0].Add != 1 || f[0].Del != 1 {
		t.Errorf("files: %+v", f)
	}
}

func TestParseSummaries(t *testing.T) {
	turns := []Turn{{ID: "a", Index: 1}, {ID: "b", Index: 2}}
	got := parseSummaries("```json\n[{\"i\":1,\"kind\":\"plan\",\"summary\":\"三层幂等\"},{\"i\":2,\"kind\":\"x\",\"summary\":\"\"}]\n```", turns)
	if len(got) != 1 || got["a"].Summary != "三层幂等" {
		t.Errorf("%+v", got)
	}
}

// 真实会话里的几种情况：拖进终端的图片路径、/compact 和压缩摘要、带参数的斜杠命令、
// 排队记录和 <synthetic> 占位回复（都晚几个小时才写）。
func TestParseClaudeCommands(t *testing.T) {
	s, err := parseClaude("testdata/claude-commands.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if s.Originator != "cli" || len(s.Turns) != 2 {
		t.Fatalf("originator=%q turns=%+v", s.Originator, s.Turns)
	}
	a, b := s.Turns[0], s.Turns[1]
	if a.Prompt != "[图片] 这边的类型换回文字" || a.Duration != time.Minute || a.Rewound {
		t.Errorf("turn 1: %q %v rewound=%v", a.Prompt, a.Duration, a.Rewound)
	}
	if b.Prompt != "/goal 修好所有问题" || b.ID != "g1" || b.Answer != "都修好了。" || b.Duration != time.Minute || b.Rewound {
		t.Errorf("turn 2: %q %s %q %v rewound=%v", b.Prompt, b.ID, b.Answer, b.Duration, b.Rewound)
	}
}
