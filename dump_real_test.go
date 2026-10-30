package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 打印一批真实会话的解析结果：
//
//	TRAIL_REAL_CODEX=<目录> go test -run TestDumpReal -v
//	TRAIL_REAL_CLAUDE=<目录> go test -run TestDumpReal -v
func TestDumpRealCodex(t *testing.T)  { dumpReal(t, "TRAIL_REAL_CODEX", parseCodex) }
func TestDumpRealClaude(t *testing.T) { dumpReal(t, "TRAIL_REAL_CLAUDE", parseClaude) }

func dumpReal(t *testing.T, env string, parse func(string) (Session, error)) {
	dir := os.Getenv(env)
	if dir == "" {
		t.Skip("未设置 " + env)
	}
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		s, err := parse(p)
		if err != nil {
			t.Errorf("%s: %v", p, err)
			return nil
		}
		fmt.Printf("== %s  %s  model=%s turns=%d dir=%s  %q\n", firstRunes(filepath.Base(p), 36), s.Originator, s.Model, len(s.Turns), s.WorkDir, s.Title)
		for _, tu := range s.Turns {
			var tools []string
			for _, x := range tu.Tools {
				tools = append(tools, fmt.Sprintf("%s×%d", x.Name, x.Count))
			}
			var files []string
			for _, f := range tu.Files {
				files = append(files, fmt.Sprintf("%s %s +%d-%d", f.Op, filepath.Base(f.Path), f.Add, f.Del))
			}
			rew := ""
			if tu.Rewound {
				rew = " [回退]"
			}
			fmt.Printf("   %02d %s%s %s %q\n      ↳ %q\n      tools=%v files=%v think=%d\n", tu.Index, tu.Kind.Label(), rew, tu.Duration.Round(1e9), firstRunes(tu.Prompt, 40), firstRunes(tu.Summary, 50), tools, files, tu.ThinkTok)
		}
		return nil
	})
}
