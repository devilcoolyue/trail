package main

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// ───────────────────────── 配色 ─────────────────────────

var (
	cText   = lipgloss.Color("#E6EDF3")
	cSub    = lipgloss.Color("#9DA7B3")
	cDim    = lipgloss.Color("#5C6570")
	cLine   = lipgloss.Color("#30363D")
	cSel    = lipgloss.Color("#1E2632")
	cCodeBg = lipgloss.Color("#161B22")
	cCode   = lipgloss.Color("#E6B673")
	cAccent = lipgloss.Color("#F0A97A")
	cDark   = lipgloss.Color("#0D1117")
	cAdd    = lipgloss.Color("#3FB950")
	cDel    = lipgloss.Color("#F85149")
	cMod    = lipgloss.Color("#D29922")
	cStar   = lipgloss.Color("#E3B341")
	cLive   = lipgloss.Color("#3FB950")
	cHL     = lipgloss.Color("#6E5A1E")
)

func kindColor(k Kind) lipgloss.Color {
	switch k {
	case KindPlan:
		return lipgloss.Color("#B392F0")
	case KindChange:
		return lipgloss.Color("#56D364")
	case KindAsk:
		return lipgloss.Color("#79C0FF")
	case KindDebug:
		return lipgloss.Color("#FFA657")
	}
	return cAccent
}

func sourceColor(s Source) lipgloss.Color {
	if s == SourceCodex {
		return lipgloss.Color("#10A37F")
	}
	return lipgloss.Color("#D97757")
}

func fg(c lipgloss.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

// ───────────────────────── 模型 ─────────────────────────

type focusArea int

const (
	focusList focusArea = iota
	focusDetail
)

type viewMode int

const (
	modeTurns viewMode = iota
	modeSessions
)

type toastClearMsg struct{ id int }

type model struct {
	sessions []Session
	cur      int

	filter    Kind
	input     textinput.Model
	searching bool
	query     string

	visible []int // 当前会话中通过筛选的 turn 下标
	sel     int   // visible 中的位置
	top     int   // 列表滚动偏移

	vp       viewport.Model
	focus    focusArea
	expanded bool

	mode    viewMode
	sessSel int

	w, h int

	jumper     Jumper
	jumpAnchor string

	toast   string
	toastID int

	demo         bool
	cache        *summaryCache
	canSummarize bool
	summarizing  map[string]bool
	busy         string
	initCmd      tea.Cmd
}

func newModel(sessions []Session, j Jumper, start int, demo bool) model {
	ti := textinput.New()
	ti.Prompt = "/ "
	ti.Placeholder = "搜索提问、结论、文件…"
	ti.CharLimit = 64
	ti.PromptStyle = fg(cAccent).Bold(true)
	ti.TextStyle = fg(cText)
	ti.PlaceholderStyle = fg(cDim)
	ti.Cursor.Style = fg(cAccent)

	m := model{sessions: sessions, input: ti, jumper: j, vp: viewport.New(0, 0), demo: demo,
		summarizing: map[string]bool{}}
	if !demo {
		m.cache = loadSummaryCache()
		for i := range m.sessions {
			m.cache.applyTo(&m.sessions[i])
		}
		m.canSummarize = summarizerAvailable()
	}
	m.openSession(start)
	m.initCmd = m.summarize()
	return m
}

func (m model) Init() tea.Cmd { return m.initCmd }

// summarize 为当前会话里还没总结过的轮次发起模型总结。
func (m *model) summarize() tea.Cmd {
	if !m.canSummarize {
		return nil
	}
	s := m.session()
	key := sessionKey(s)
	if m.summarizing[key] {
		return nil
	}
	var todo []Turn
	for _, t := range s.Turns {
		if !t.Summarized && !t.Rewound {
			todo = append(todo, t)
		}
	}
	// 会话文件刚被写过：最后一轮可能还在进行，先不总结它，免得缓存半截的结论
	if n := len(todo); n > 0 && time.Since(s.ModTime) < 90*time.Second &&
		todo[n-1].ID == s.Turns[len(s.Turns)-1].ID {
		todo = todo[:n-1]
	}
	if len(todo) == 0 {
		return nil
	}
	m.summarizing[key] = true
	m.busy = fmt.Sprintf("haiku 总结中 0/%d", len(todo))
	return summarizeCmd(key, todo, 0, len(todo))
}

func (m *model) applySummaries(msg summariesMsg) tea.Cmd {
	for si := range m.sessions {
		s := &m.sessions[si]
		if sessionKey(s) != msg.session {
			continue
		}
		for ti := range s.Turns {
			t := &s.Turns[ti]
			if e, ok := msg.results[t.ID]; ok {
				t.Summary = e.Summary
				if k, ok := kindByName[e.Kind]; ok {
					t.Kind = k
				}
				t.Summarized = true
				m.cache.data[summaryKey(s, t)] = e
			}
		}
		if si == m.cur {
			m.refilter(m.curKey())
		}
	}
	if len(msg.results) > 0 {
		m.cache.save()
	}
	if msg.err != nil || len(msg.rest) == 0 {
		delete(m.summarizing, msg.session)
		m.busy = ""
		if msg.err != nil {
			m.busy = "总结失败，显示规则提取的结果"
		}
		return nil
	}
	m.busy = fmt.Sprintf("haiku 总结中 %d/%d", msg.done, msg.total)
	return summarizeCmd(msg.session, msg.rest, msg.done, msg.total)
}

func (m *model) session() *Session { return &m.sessions[m.cur] }

func (m *model) selected() *Turn {
	if m.sel < 0 || m.sel >= len(m.visible) {
		return nil
	}
	return &m.session().Turns[m.visible[m.sel]]
}

func (m *model) openSession(i int) {
	m.cur = i
	m.filter = KindAll
	m.query = ""
	m.input.SetValue("")
	m.refilter(-1)
	m.sel = len(m.visible) - 1 // 默认停在最近一轮
	m.top = 0
	m.ensureVisible()
	m.syncDetail(true)
}

func (m *model) treeMode() bool { return m.filter == KindAll && m.query == "" }

func (m *model) refilter(keep int) {
	q := strings.ToLower(strings.TrimSpace(m.query))
	m.visible = m.visible[:0]
	newSel := -1
	for i := range m.session().Turns {
		t := &m.session().Turns[i]
		if m.filter != KindAll && t.Kind != m.filter {
			continue
		}
		if q != "" && !turnMatches(t, q) {
			continue
		}
		if i == keep {
			newSel = len(m.visible)
		}
		m.visible = append(m.visible, i)
	}
	if newSel >= 0 {
		m.sel = newSel
	} else if m.sel >= len(m.visible) {
		m.sel = len(m.visible) - 1
	}
	if m.sel < 0 && len(m.visible) > 0 {
		m.sel = 0
	}
	m.ensureVisible()
	m.syncDetail(true)
}

func turnMatches(t *Turn, q string) bool {
	hay := []string{t.Prompt, t.Summary, t.Answer}
	for _, f := range t.Files {
		hay = append(hay, f.Path)
	}
	for _, h := range hay {
		if strings.Contains(strings.ToLower(h), q) {
			return true
		}
	}
	return false
}

func (m *model) curKey() int {
	if m.sel >= 0 && m.sel < len(m.visible) {
		return m.visible[m.sel]
	}
	return -1
}

// ───────────────────────── 布局 ─────────────────────────

const bodyTop = 2 // header + tabs

func (m *model) listW() int {
	w := m.w * 44 / 100
	return max(38, min(w, 64))
}
func (m *model) detailW() int { return m.w - m.listW() }
func (m *model) bodyH() int   { return max(6, m.h-3) }
func (m *model) listRows() int {
	return max(1, (m.bodyH()-2)/2)
}

func (m *model) ensureVisible() {
	rows := m.listRows()
	if m.sel < m.top {
		m.top = m.sel
	}
	if m.sel >= m.top+rows {
		m.top = m.sel - rows + 1
	}
	if m.top > len(m.visible)-rows { // 列表能放下时不留空白
		m.top = len(m.visible) - rows
	}
	if m.top < 0 {
		m.top = 0
	}
}

func (m *model) syncDetail(reset bool) {
	if m.w == 0 {
		return
	}
	m.vp.Width = m.detailW() - 4
	m.vp.Height = m.bodyH() - 2
	m.vp.SetContent(m.renderDetail(m.vp.Width))
	if reset {
		m.vp.GotoTop()
	}
}

// ───────────────────────── 更新 ─────────────────────────

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.ensureVisible()
		m.syncDetail(false)
		return m, nil

	case summariesMsg:
		cmd := m.applySummaries(msg)
		return m, cmd

	case toastClearMsg:
		if msg.id == m.toastID {
			m.toast = ""
		}
		return m, nil

	case tea.MouseMsg:
		return m.onMouse(msg)

	case tea.KeyMsg:
		if m.searching {
			return m.onSearchKey(msg)
		}
		if m.mode == modeSessions {
			return m.onSessionsKey(msg)
		}
		return m.onTurnsKey(msg)
	}
	return m, nil
}

func (m *model) move(d int) {
	if len(m.visible) == 0 {
		return
	}
	m.sel = max(0, min(len(m.visible)-1, m.sel+d))
	m.ensureVisible()
	m.syncDetail(true)
}

func (m *model) setFilter(k Kind) {
	m.filter = k
	m.refilter(m.curKey())
}

func (m model) onTurnsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if m.focus == focusDetail {
		switch k {
		case "up", "k":
			m.vp.LineUp(1)
			return m, nil
		case "down", "j":
			m.vp.LineDown(1)
			return m, nil
		}
	}
	switch k {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		switch {
		case m.focus == focusDetail:
			m.focus = focusList
		case m.query != "":
			m.query = ""
			m.input.SetValue("")
			m.refilter(m.curKey())
		default:
			return m, tea.Quit
		}
	case "tab":
		if m.focus == focusList {
			m.focus = focusDetail
		} else {
			m.focus = focusList
		}
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "g", "home":
		m.move(-len(m.visible))
	case "G", "end":
		m.move(len(m.visible))
	case "pgup", "ctrl+u":
		m.vp.HalfPageUp()
	case "pgdown", "ctrl+d":
		m.vp.HalfPageDown()
	case "1", "2", "3", "4", "5":
		m.setFilter(kindOrder[int(k[0]-'1')])
	case "left", "h":
		m.setFilter(kindOrder[(int(m.filter)+len(kindOrder)-1)%len(kindOrder)])
	case "right", "l":
		m.setFilter(kindOrder[(int(m.filter)+1)%len(kindOrder)])
	case "/":
		m.searching = true
		m.input.SetValue(m.query)
		m.input.CursorEnd()
		return m, m.input.Focus()
	case " ":
		m.expanded = !m.expanded
		m.syncDetail(false)
	case "p":
		if t := m.selected(); t != nil {
			t.Pinned = !t.Pinned
			m.syncDetail(false)
		}
	case "s":
		m.mode = modeSessions
		m.sessSel = m.cur
	case "[":
		if m.cur > 0 {
			m.openSession(m.cur - 1)
			cmd := m.summarize()
			return m, cmd
		}
	case "]":
		if m.cur < len(m.sessions)-1 {
			m.openSession(m.cur + 1)
			cmd := m.summarize()
			return m, cmd
		}
	case "enter":
		return m.jump()
	}
	return m, nil
}

func (m model) onSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.searching = false
		m.input.Blur()
		m.query = ""
		m.input.SetValue("")
		m.refilter(m.curKey())
		return m, nil
	case "enter":
		m.searching = false
		m.input.Blur()
		return m, nil
	case "up", "ctrl+p":
		m.move(-1)
		return m, nil
	case "down", "ctrl+n":
		m.move(1)
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if v := m.input.Value(); v != m.query {
		m.query = v
		m.refilter(m.curKey())
	}
	return m, cmd
}

func (m model) onSessionsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc", "s":
		m.mode = modeTurns
	case "up", "k":
		m.sessSel = max(0, m.sessSel-1)
	case "down", "j":
		m.sessSel = min(len(m.sessions)-1, m.sessSel+1)
	case "enter":
		m.mode = modeTurns
		m.openSession(m.sessSel)
		cmd := m.summarize()
		return m, cmd
	}
	return m, nil
}

func (m model) onMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	inList := msg.X < m.listW()
	switch msg.Button {
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		d := 1
		if msg.Button == tea.MouseButtonWheelUp {
			d = -1
		}
		switch {
		case m.mode == modeSessions:
			m.sessSel = max(0, min(len(m.sessions)-1, m.sessSel+d))
		case inList:
			m.move(d)
		case d < 0:
			m.vp.LineUp(3)
		default:
			m.vp.LineDown(3)
		}
		return m, nil
	case tea.MouseButtonLeft:
		if msg.Action != tea.MouseActionPress {
			return m, nil
		}
	default:
		return m, nil
	}

	row := msg.Y - bodyTop - 1
	if m.mode == modeSessions {
		if inList && row >= 0 {
			for _, r := range m.sessionRows() {
				if r.y == row || r.y+1 == row {
					if r.idx == m.sessSel {
						m.mode = modeTurns
						m.openSession(r.idx)
						cmd := m.summarize()
						return m, cmd
					} else {
						m.sessSel = r.idx
					}
				}
			}
		}
		return m, nil
	}
	if !inList {
		m.focus = focusDetail
		return m, nil
	}
	m.focus = focusList
	if row < 0 || row >= m.listRows()*2 {
		return m, nil
	}
	i := m.top + row/2
	if i >= len(m.visible) {
		return m, nil
	}
	if i == m.sel {
		return m.jump() // 再次点击已选中项 = 跳转
	}
	m.sel = i
	m.syncDetail(true)
	return m, nil
}

func (m model) flash(s string) (tea.Model, tea.Cmd) {
	m.toastID++
	m.toast = s
	id := m.toastID
	return m, tea.Tick(4*time.Second, func(time.Time) tea.Msg { return toastClearMsg{id} })
}

func (m model) jump() (tea.Model, tea.Cmd) {
	t := m.selected()
	if t == nil {
		return m, nil
	}
	s := m.session()
	if !s.Live {
		return m.flash(fmt.Sprintf("该会话不在当前终端 · 可执行 %s 恢复", s.ResumeCmd()))
	}
	if !m.demo && s.Source == SourceClaude {
		return m.flash("Claude Code 会话请在 Claude Code 里输入 /trail，点击即可跳回原位置")
	}
	if m.jumper == nil {
		return m.flash(fmt.Sprintf("演示模式（未检测到 tmux）· 将定位第 %d 轮「%s…」", t.Index, t.Anchor))
	}
	if err := m.jumper.Ready(); err != nil {
		return m.flash(err.Error())
	}
	m.jumpAnchor = t.Anchor
	return m, tea.Quit
}

// ───────────────────────── 渲染工具 ─────────────────────────

// row 是带可选背景色的单行拼接器，用于实现整行高亮。
type row struct {
	b  strings.Builder
	w  int
	bg lipgloss.TerminalColor
}

func (r *row) add(s string, st lipgloss.Style) {
	if r.bg != nil {
		st = st.Background(r.bg)
	}
	r.b.WriteString(st.Render(s))
	r.w += ansi.StringWidth(s)
}

func (r *row) pad(to int) {
	if to > r.w {
		r.add(strings.Repeat(" ", to-r.w), lipgloss.NewStyle())
	}
}

func (r *row) String() string { return r.b.String() }

func trunc(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s, w, "…")
}

func padRight(s string, w int) string {
	if d := w - ansi.StringWidth(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

func box(title, info string, lines []string, w, h int, focused bool) string {
	bc, tc := cLine, cSub
	if focused {
		bc, tc = cDim, cAccent
	}
	b := fg(bc)
	inner := w - 2
	t := fg(tc).Bold(true).Render(" " + title + " ")
	in := ""
	if info != "" {
		in = fg(cDim).Render(" " + info + " ")
	}
	fill := inner - 1 - ansi.StringWidth(t) - ansi.StringWidth(in) - 1
	var out []string
	out = append(out, b.Render("╭─")+t+b.Render(strings.Repeat("─", max(0, fill)))+in+b.Render("─╮"))
	for i := 0; i < h-2; i++ {
		l := ""
		if i < len(lines) {
			l = lines[i]
		}
		out = append(out, b.Render("│")+padRight(trunc(l, inner), inner)+b.Render("│"))
	}
	out = append(out, b.Render("╰"+strings.Repeat("─", inner)+"╯"))
	return strings.Join(out, "\n")
}

func fmtDur(d time.Duration) string {
	d = d.Round(time.Minute)
	if d >= time.Hour {
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

func fmtTok(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprint(n)
}

func fileStats(fs []FileChange) (add, del int) {
	for _, f := range fs {
		add += f.Add
		del += f.Del
	}
	return
}

// highlight 把 s 中与 q 匹配的部分加上高亮背景。
func highlight(r *row, s, q string, st lipgloss.Style) {
	if q == "" {
		r.add(s, st)
		return
	}
	ls, lq := strings.ToLower(s), strings.ToLower(q)
	for {
		i := strings.Index(ls, lq)
		if i < 0 {
			r.add(s, st)
			return
		}
		if i > 0 {
			r.add(s[:i], st)
		}
		r.add(s[i:i+len(q)], st.Background(cHL).Foreground(cText))
		s, ls = s[i+len(q):], ls[i+len(q):]
		if s == "" {
			return
		}
	}
}

// ── 轻量 markdown 渲染 + 中英混排折行 ──

type span struct {
	text string
	st   lipgloss.Style
}

var inlineRe = regexp.MustCompile("`[^`]+`|\\*\\*[^*]+\\*\\*")

func inlineSpans(s string, base lipgloss.Style) []span {
	var out []span
	for len(s) > 0 {
		loc := inlineRe.FindStringIndex(s)
		if loc == nil {
			out = append(out, span{s, base})
			break
		}
		if loc[0] > 0 {
			out = append(out, span{s[:loc[0]], base})
		}
		tok := s[loc[0]:loc[1]]
		if tok[0] == '`' {
			out = append(out, span{tok[1 : len(tok)-1], fg(cCode)})
		} else {
			out = append(out, span{tok[2 : len(tok)-2], base.Bold(true).Foreground(cText)})
		}
		s = s[loc[1]:]
	}
	return out
}

// tokens 把文本拆成可折行单元：连续 ASCII 非空白为一个词，其余字符各自成单元。
func tokens(s string) []string {
	var out []string
	for len(s) > 0 {
		r, n := utf8.DecodeRuneInString(s)
		if r < 128 && r != ' ' {
			j := 0
			for j < len(s) && s[j] < 128 && s[j] != ' ' {
				j++
			}
			out = append(out, s[:j])
			s = s[j:]
			continue
		}
		out = append(out, s[:n])
		s = s[n:]
	}
	return out
}

// wrapSpans 将 spans 折成宽度为 w 的多行；第一行前缀 first，后续行前缀 rest（均为已渲染字符串）。
func wrapSpans(spans []span, w int, first, rest string) []string {
	var lines []string
	var cur strings.Builder
	cur.WriteString(first)
	start := ansi.StringWidth(first)
	cw := start
	restW := ansi.StringWidth(rest)
	newline := func() {
		lines = append(lines, cur.String())
		cur.Reset()
		cur.WriteString(rest)
		start, cw = restW, restW
	}
	for _, sp := range spans {
		for _, tok := range tokens(sp.text) {
			tw := ansi.StringWidth(tok)
			if tok == " " && cw == start {
				continue
			}
			if cw+tw > w && cw > start {
				newline()
				if tok == " " {
					continue
				}
			}
			for tw > w-cw && cw == start && w-cw > 0 { // 超长单词硬切
				part := ansi.Truncate(tok, w-cw, "")
				cur.WriteString(sp.st.Render(part))
				tok = tok[len(part):]
				tw = ansi.StringWidth(tok)
				newline()
			}
			cur.WriteString(sp.st.Render(tok))
			cw += tw
		}
	}
	lines = append(lines, cur.String())
	return lines
}

var olRe = regexp.MustCompile(`^(\d+)\. (.*)$`)

func renderMD(src string, w int, indent string) []string {
	var out []string
	base := fg(cText)
	inCode := false
	iw := ansi.StringWidth(indent)
	for _, l := range strings.Split(src, "\n") {
		trim := strings.TrimSpace(l)
		if strings.HasPrefix(trim, "```") {
			inCode = !inCode
			continue
		}
		switch {
		case inCode:
			code := " " + trunc(strings.ReplaceAll(l, "\t", "    "), w-iw-2)
			out = append(out, indent+lipgloss.NewStyle().Background(cCodeBg).Foreground(cSub).
				Render(padRight(code, w-iw)))
		case trim == "":
			out = append(out, "")
		case strings.HasPrefix(trim, "## "):
			out = append(out, indent+fg(cAccent).Bold(true).Render(strings.TrimPrefix(trim, "## ")))
		case strings.HasPrefix(trim, "- "):
			out = append(out, wrapSpans(inlineSpans(trim[2:], base), w,
				indent+fg(cDim).Render("• "), indent+"  ")...)
		case olRe.MatchString(trim):
			mm := olRe.FindStringSubmatch(trim)
			num := mm[1] + ". "
			out = append(out, wrapSpans(inlineSpans(mm[2], base), w,
				indent+fg(cAccent).Render(num), indent+strings.Repeat(" ", len(num)))...)
		default:
			out = append(out, wrapSpans(inlineSpans(trim, base), w, indent, indent)...)
		}
	}
	return out
}

func badge(k Kind) string { return fg(kindColor(k)).Bold(true).Render(k.Label()) }

// ───────────────────────── 视图 ─────────────────────────

func (m model) View() string {
	if m.w == 0 {
		return ""
	}
	var body string
	if m.mode == modeSessions {
		body = m.viewSessions()
	} else {
		body = lipgloss.JoinHorizontal(lipgloss.Top, m.viewList(), m.viewDetail())
	}
	return lipgloss.JoinVertical(lipgloss.Left, m.viewHeader(), m.viewTabs(), body, m.viewFooter())
}

func (m model) viewHeader() string {
	s := &m.sessions[m.cur]
	logo := lipgloss.NewStyle().Bold(true).Foreground(cDark).Background(cAccent).Padding(0, 1).Render("◆ trail")
	left := logo + "  " +
		fg(sourceColor(s.Source)).Bold(true).Render(s.Source.Icon()+" "+s.Source.Name()) + "  " +
		fg(cSub).Render(s.Project) + "  " +
		fg(cDim).Render("⎇ "+s.GitBranch) + "  " +
		fg(cText).Bold(true).Render(s.Title)
	if s.Live {
		left += "  " + fg(cLive).Render("● 当前终端")
	}
	right := fg(cDim).Render(fmt.Sprintf("%d 轮 · %s · %s ", len(s.Turns), fmtDur(s.Elapsed()), s.Model))
	if m.busy != "" {
		right = fg(cAccent).Italic(true).Render("◌ "+m.busy) + fg(cDim).Render(" · ") + right
	}
	gap := m.w - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		return trunc(left, m.w)
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m model) viewTabs() string {
	counts := map[Kind]int{}
	for _, t := range m.session().Turns {
		counts[t.Kind]++
		counts[KindAll]++
	}
	var b strings.Builder
	b.WriteString(" ")
	for i, k := range kindOrder {
		label := fmt.Sprintf(" %d %s %d ", i+1, k.Label(), counts[k])
		if k == m.filter {
			b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(cDark).Background(kindColor(k)).Render(label))
		} else {
			b.WriteString(fg(kindColor(k)).Render(label))
		}
		b.WriteString(" ")
	}
	left := b.String()

	var right string
	switch {
	case m.searching:
		right = m.input.View()
	case m.query != "":
		right = fg(cAccent).Bold(true).Render("/ ") + fg(cText).Render(m.query) +
			fg(cDim).Render(fmt.Sprintf("  %d 条匹配 · esc 清除", len(m.visible)))
	default:
		right = fg(cDim).Render("/ 搜索提问、结论、文件…")
	}
	gap := m.w - ansi.StringWidth(left) - ansi.StringWidth(right) - 1
	if gap < 2 {
		return trunc(left, m.w)
	}
	return left + strings.Repeat(" ", gap) + right + " "
}

func (m *model) gutter(ti int) (string, string) {
	turns := m.session().Turns
	t := &turns[ti]
	var next *Turn
	if ti+1 < len(turns) {
		next = &turns[ti+1]
	}
	if !m.treeMode() {
		if t.Rewound {
			return "○", " "
		}
		return "●", " "
	}
	if t.Rewound {
		l2 := "│ "
		if next != nil && next.Rewound {
			l2 = "││"
		}
		return "│○", l2
	}
	switch {
	case next == nil:
		return "● ", "  "
	case next.Rewound:
		return "● ", "├╮"
	default:
		return "● ", "│ "
	}
}

func (m model) viewList() string {
	w := m.listW()
	inner := w - 2
	rows := m.listRows()
	var lines []string

	if len(m.visible) == 0 {
		lines = append(lines, "", fg(cDim).Render("  没有匹配的对话"))
	}
	for i := m.top; i < len(m.visible) && i < m.top+rows; i++ {
		ti := m.visible[i]
		t := &m.session().Turns[ti]
		sel := i == m.sel
		var r1, r2 row
		if sel {
			r1.bg, r2.bg = cSel, cSel
		}
		bar := " "
		if sel {
			bar = "▌"
		}
		barSt := fg(cAccent)
		if m.focus != focusList {
			barSt = fg(cDim)
		}
		g1, g2 := m.gutter(ti)
		dot := fg(kindColor(t.Kind))
		if t.Rewound {
			dot = fg(cDim)
		}

		// 第一行：序号 · 类型 · 提问 · 时间
		r1.add(bar, barSt)
		for _, ch := range g1 {
			if ch == '●' || ch == '○' {
				r1.add(string(ch), dot)
			} else {
				r1.add(string(ch), fg(cLine))
			}
		}
		r1.pad(4)
		idxSt := fg(cDim)
		if sel {
			idxSt = fg(cText)
		}
		r1.add(fmt.Sprintf("%02d ", t.Index), idxSt)
		if t.Rewound {
			r1.add(t.Kind.Label(), fg(cDim))
		} else {
			r1.add(t.Kind.Label(), fg(kindColor(t.Kind)).Bold(true))
		}
		r1.add(" ", lipgloss.NewStyle())
		if t.Pinned {
			r1.add("★ ", fg(cStar))
		}
		tm := t.Time.Format("15:04")
		room := inner - r1.w - len(tm) - 2
		pst := fg(cText)
		if sel {
			pst = pst.Bold(true)
		}
		if t.Rewound {
			pst = fg(cDim).Strikethrough(true)
		}
		highlight(&r1, trunc(t.Prompt, room), m.query, pst)
		r1.pad(inner - len(tm) - 1)
		r1.add(tm, fg(cDim))
		r1.pad(inner)

		// 第二行：一句话总结 · 改动统计
		r2.add(bar, barSt)
		r2.add(g2, fg(cLine))
		r2.pad(7)
		var meta []span
		if t.Rewound {
			meta = []span{{"↺ 已回退", fg(cDim).Italic(true)}}
		} else if a, d := fileStats(t.Files); len(t.Files) > 0 {
			meta = []span{{fmt.Sprintf("%d 文件 ", len(t.Files)), fg(cDim)}, {fmt.Sprintf("+%d", a), fg(cAdd)}}
			if d > 0 {
				meta = append(meta, span{fmt.Sprintf(" −%d", d), fg(cDel)})
			}
		}
		mw := 0
		for _, s := range meta {
			mw += ansi.StringWidth(s.text)
		}
		r2.add("↳ ", fg(cDim))
		sumSt := fg(cSub)
		if t.Rewound {
			sumSt = fg(cDim)
		}
		highlight(&r2, trunc(t.Summary, inner-r2.w-mw-2), m.query, sumSt)
		r2.pad(inner - mw - 1)
		for _, s := range meta {
			r2.add(s.text, s.st)
		}
		r2.pad(inner)

		lines = append(lines, r1.String(), r2.String())
	}

	info := fmt.Sprintf("%d/%d", max(0, m.sel+1), len(m.visible))
	return box("对话索引", info, lines, w, m.bodyH(), m.focus == focusList)
}

func (m model) renderDetail(w int) string {
	t := m.selected()
	if t == nil {
		return "\n" + fg(cDim).Render("  ← 选择一轮对话查看详情")
	}
	var out []string
	sec := func(title string, c lipgloss.Color) string {
		return fg(c).Render("▍") + fg(cText).Bold(true).Render(title)
	}

	// 标题行
	h := fg(cAccent).Bold(true).Render(fmt.Sprintf("#%02d", t.Index)) + "  " + badge(t.Kind)
	if t.Pinned {
		h += "  " + fg(cStar).Render("★ 关键节点")
	}
	if t.Rewound {
		h += "  " + fg(kindColor(KindDebug)).Render("↺ 已回退的分支")
	}
	h += fg(cDim).Render(fmt.Sprintf("   %s · 用时 %s", t.Time.Format("15:04"), fmtDur(t.Duration)))
	out = append(out, h, fg(cLine).Render(strings.Repeat("─", w)), "")

	out = append(out, sec(" 你", kindColor(KindAsk)))
	out = append(out, wrapSpans([]span{{t.Prompt, fg(cText)}}, w, "  ", "  ")...)
	out = append(out, "")

	out = append(out, sec(" 结论", kindColor(t.Kind)))
	out = append(out, renderMD(t.Answer, w, "  ")...)
	out = append(out, "")

	arrow := func(open bool) string {
		if open {
			return fg(cAccent).Render("▾ ")
		}
		return fg(cDim).Render("▸ ")
	}
	if t.ThinkTok > 0 {
		out = append(out, arrow(m.expanded)+fg(cSub).Render("思考过程")+fg(cDim).Render("  "+fmtTok(t.ThinkTok)+" 字"))
		if m.expanded {
			txt := t.Thinking
			if txt == "" {
				txt = "（思考内容已省略）"
			}
			out = append(out, wrapSpans([]span{{txt, fg(cDim).Italic(true)}}, w, fg(cLine).Render("  │ "), fg(cLine).Render("  │ "))...)
		}
	}
	if len(t.Tools) > 0 {
		n := 0
		var parts []string
		for _, tu := range t.Tools {
			n += tu.Count
			parts = append(parts, fg(cSub).Render(tu.Name)+fg(cDim).Render(fmt.Sprintf("×%d", tu.Count)))
		}
		out = append(out, arrow(m.expanded)+fg(cSub).Render("工具调用")+fg(cDim).Render(fmt.Sprintf("  %d 次  ", n))+strings.Join(parts, fg(cDim).Render(" · ")))
	}
	if len(t.Files) > 0 {
		a, d := fileStats(t.Files)
		out = append(out, fg(cAccent).Render("▾ ")+fg(cSub).Render("改动文件")+
			fg(cDim).Render(fmt.Sprintf("  %d 个  ", len(t.Files)))+fg(cAdd).Render(fmt.Sprintf("+%d", a))+" "+fg(cDel).Render(fmt.Sprintf("−%d", d)))
		for _, f := range t.Files {
			opc := map[string]lipgloss.Color{"A": cAdd, "M": cMod, "D": cDel}[f.Op]
			var r row
			r.add("    ", lipgloss.NewStyle())
			r.add(f.Op+" ", fg(opc).Bold(true))
			stat := fmt.Sprintf("+%d", f.Add)
			if f.Del > 0 {
				stat += fmt.Sprintf(" −%d", f.Del)
			}
			r.add(trunc(f.Path, w-r.w-len(stat)-2), fg(cText))
			r.pad(w - ansi.StringWidth(stat))
			r.add(fmt.Sprintf("+%d", f.Add), fg(cAdd))
			if f.Del > 0 {
				r.add(fmt.Sprintf(" −%d", f.Del), fg(cDel))
			}
			out = append(out, r.String())
		}
	}
	return strings.Join(out, "\n")
}

func (m model) viewDetail() string {
	lines := strings.Split(m.vp.View(), "\n")
	for i := range lines {
		lines[i] = " " + lines[i]
	}
	info := ""
	if m.vp.TotalLineCount() > m.vp.Height {
		info = fmt.Sprintf("%d%%", int(m.vp.ScrollPercent()*100))
	}
	return box("详情", info, lines, m.detailW(), m.bodyH(), m.focus == focusDetail)
}

func (m model) viewFooter() string {
	if m.toast != "" {
		return " " + fg(cAccent).Bold(true).Render("➜ ") + fg(cText).Render(m.toast)
	}
	type kv struct{ k, v string }
	var keys []kv
	switch {
	case m.searching:
		keys = []kv{{"↑↓", "选择"}, {"⏎", "确认"}, {"esc", "取消搜索"}}
	case m.mode == modeSessions:
		keys = []kv{{"↑↓", "选择会话"}, {"⏎", "打开索引"}, {"esc", "返回"}, {"q", "退出"}}
	default:
		keys = []kv{{"↑↓", "选择"}, {"⏎", "跳到原位置"}, {"/", "搜索"}, {"1-5", "筛选"},
			{"space", "展开"}, {"p", "标记"}, {"tab", "焦点"}, {"s", "会话"}, {"q", "退出"}}
	}
	var parts []string
	for _, p := range keys {
		parts = append(parts, fg(cAccent).Render(p.k)+" "+fg(cDim).Render(p.v))
	}
	return trunc(" "+strings.Join(parts, fg(cLine).Render("  │  ")), m.w)
}

// ── 会话切换 ──

type sessRow struct {
	y      int
	idx    int
	header string
}

func dayLabel(t time.Time) string {
	now := time.Now()
	y1, m1, d1 := now.Date()
	today := time.Date(y1, m1, d1, 0, 0, 0, 0, now.Location())
	days := int(today.Sub(time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())).Hours() / 24)
	switch {
	case days <= 0:
		return "今天"
	case days == 1:
		return "昨天"
	default:
		return fmt.Sprintf("%d 天前", days)
	}
}

// sessionRows 计算会话列表的行布局（分组标题 + 每个会话两行），渲染和鼠标点击共用。
func (m *model) sessionRows() []sessRow {
	var rs []sessRow
	y, last := 0, ""
	for i, s := range m.sessions {
		if d := dayLabel(s.LastActive()); d != last {
			if last != "" {
				y++
			}
			rs = append(rs, sessRow{y: y, idx: -1, header: d})
			y++
			last = d
		}
		rs = append(rs, sessRow{y: y, idx: i})
		y += 2
	}
	return rs
}

func (m model) viewSessions() string {
	lw := m.listW()
	inner := lw - 2
	var lines []string
	for _, r := range m.sessionRows() {
		for len(lines) < r.y {
			lines = append(lines, "")
		}
		if r.idx < 0 {
			lines = append(lines, fg(cDim).Render("  "+r.header))
			continue
		}
		s := &m.sessions[r.idx]
		sel := r.idx == m.sessSel
		var r1, r2 row
		if sel {
			r1.bg, r2.bg = cSel, cSel
		}
		bar := " "
		if sel {
			bar = "▌"
		}
		r1.add(bar+" ", fg(cAccent))
		r1.add(s.Source.Icon()+" ", fg(sourceColor(s.Source)).Bold(true))
		tst := fg(cText)
		if sel {
			tst = tst.Bold(true)
		}
		tm := s.LastActive().Format("15:04")
		live := ""
		if s.Live {
			live = "● "
		}
		r1.add(trunc(s.Title, inner-r1.w-len(tm)-4), tst)
		r1.pad(inner - len(tm) - 1 - ansi.StringWidth(live))
		r1.add(live, fg(cLive))
		r1.add(tm, fg(cDim))
		r1.pad(inner)

		r2.add(bar+"   ", fg(cAccent))
		cnt := fmt.Sprintf("%d 轮", len(s.Turns))
		r2.add(trunc(s.Project+"  ⎇ "+s.GitBranch, inner-r2.w-len(cnt)-2), fg(cDim))
		r2.pad(inner - ansi.StringWidth(cnt) - 1)
		r2.add(cnt, fg(cSub))
		r2.pad(inner)
		lines = append(lines, r1.String(), r2.String())
	}
	left := box("全部会话", fmt.Sprintf("%d 个", len(m.sessions)), lines, lw, m.bodyH(), true)
	right := box("会话概览", "", m.renderOverview(m.detailW()-4), m.detailW(), m.bodyH(), false)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func (m model) renderOverview(w int) []string {
	s := &m.sessions[m.sessSel]
	var out []string
	p := func(l string) { out = append(out, " "+l) }

	p(fg(sourceColor(s.Source)).Bold(true).Render(s.Source.Icon()+" "+s.Source.Name()) + fg(cDim).Render(" · "+s.Model+" · "+s.ID))
	p(fg(cText).Bold(true).Render(s.Title))
	p(fg(cSub).Render(s.Project) + fg(cDim).Render("  ⎇ "+s.GitBranch))
	p("")

	var files, add, del, rew int
	counts := map[Kind]int{}
	for _, t := range s.Turns {
		counts[t.Kind]++
		a, d := fileStats(t.Files)
		files += len(t.Files)
		add += a
		del += d
		if t.Rewound {
			rew++
		}
	}
	stat := fmt.Sprintf("%d 轮 · %s · %d 个文件 ", len(s.Turns), fmtDur(s.Elapsed()), files)
	line := fg(cSub).Render(stat) + fg(cAdd).Render(fmt.Sprintf("+%d", add)) + " " + fg(cDel).Render(fmt.Sprintf("−%d", del))
	if rew > 0 {
		line += fg(cDim).Render(fmt.Sprintf(" · %d 轮已回退", rew))
	}
	p(line)

	// 类型分布条
	barW := min(w-2, 48)
	var bar, legend strings.Builder
	used := 0
	kinds := kindOrder[1:]
	for i, k := range kinds {
		n := counts[k] * barW / max(1, len(s.Turns))
		if i == len(kinds)-1 {
			n = barW - used
		}
		used += n
		bar.WriteString(fg(kindColor(k)).Render(strings.Repeat("━", n)))
		if counts[k] > 0 {
			legend.WriteString(fg(kindColor(k)).Render("● ") + fg(cSub).Render(fmt.Sprintf("%s %d   ", k.Label(), counts[k])))
		}
	}
	p(bar.String())
	p(legend.String())
	p("")

	out = append(out, " "+fg(cStar).Render("▍")+fg(cText).Bold(true).Render(" 关键节点"))
	hasPin := false
	for _, t := range s.Turns {
		if !t.Pinned {
			continue
		}
		hasPin = true
		idx := fmt.Sprintf("#%02d", t.Index)
		out = append(out, wrapSpans([]span{{t.Summary, fg(cText)}}, w-len(idx)-1,
			"   "+fg(cStar).Render("★ "), "     ")[0]+"  "+fg(cDim).Render(idx))
	}
	if !hasPin {
		p(fg(cDim).Render("  （按 p 标记重要的一轮）"))
	}
	p("")

	out = append(out, " "+fg(cAccent).Render("▍")+fg(cText).Bold(true).Render(" 时间线"))
	for _, t := range s.Turns {
		var r row
		r.add("   "+t.Time.Format("15:04")+"  ", fg(cDim))
		if t.Rewound {
			r.add("○ ", fg(cDim))
			r.add(trunc(t.Summary, w-r.w), fg(cDim).Strikethrough(true))
		} else {
			r.add("● ", fg(kindColor(t.Kind)))
			r.add(trunc(t.Summary, w-r.w), fg(cSub))
		}
		out = append(out, r.String())
	}
	p("")
	if s.Live {
		p(fg(cAccent).Render("⏎ ") + fg(cDim).Render("打开索引，可直接跳回终端原位置"))
	} else {
		p(fg(cAccent).Render("⏎ ") + fg(cDim).Render("打开索引 · 原生恢复：") + fg(cCode).Render(s.ResumeCmd()))
	}
	return out
}
