package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
	zone "github.com/lrstanley/bubblezone/v2"
)

type screen int

const (
	scrQuestion screen = iota
	scrReview
	scrReframe
	scrExpired
)

type focus int

const (
	fOptions focus = iota
	fOther
	fChat
	fNote
	fReason
)

type chatMsg struct {
	You     bool
	Text    string
	Status  string // what the assistant is doing right now
	Live    bool
	Failed  bool
	Copied  bool
}

type qstate struct {
	cursor      int
	picked      map[int]bool
	other       string
	thread      []chatMsg
	unread      int
	note        string
	noteByAgent bool
	noteEdited  bool
	drafting    bool
	draftedAt   int // len(thread) when the note was last drafted
	showMore    bool
}

type pending struct {
	q    int
	kind string // "chat", "note", "reason"
	buf  strings.Builder
}

type model struct {
	req       *Request
	dir       string
	w, h      int
	dark      bool
	scr       screen
	cur       int // question in focus
	qs        []qstate
	focus     focus
	askTab    bool // narrow layout: showing "Ask about it"
	editNote  int  // review: question whose note is being edited, -1 none
	chat      textarea.Model
	notes     textarea.Model
	otherIn   textarea.Model
	reason    textarea.Model
	inclSum   bool
	agent     *Agent
	model     string
	reqs      map[int]*pending
	nextReq   int
	leftOff   int
	threadOff int // lines scrolled up from the bottom
	md        map[string]string
	flash     string
	expired   bool
}

type tickMsg time.Time
type agentClosed struct{ a *Agent }

const otherLabel = "Something else…"

var clickables []string

func newModel(req *Request, dir string) *model {
	m := &model{req: req, dir: dir, dark: true, editNote: -1, model: "sonnet", reqs: map[int]*pending{}, md: map[string]string{}, inclSum: true}
	m.qs = make([]qstate, len(req.Questions))
	for i := range m.qs {
		m.qs[i].picked = map[int]bool{}
	}
	m.chat = newInput("Ask a follow-up…", 1, 8)
	m.notes = newInput("A note for the agent…", 2, 10)
	m.otherIn = newInput("Your answer…", 1, 6)
	m.reason = newInput("What is wrong with the question?", 3, 12)
	return m
}

func newInput(placeholder string, minH, maxH int) textarea.Model {
	t := textarea.New()
	t.Placeholder = placeholder
	t.ShowLineNumbers = false
	t.Prompt = ""
	t.DynamicHeight, t.MinHeight, t.MaxHeight, t.MaxContentHeight = true, minH, maxH, 10000
	t.CharLimit = 0
	t.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("shift+enter", "alt+enter", "ctrl+j"))
	return t
}

func (m *model) Init() tea.Cmd { return tea.Batch(tick(), tea.RequestBackgroundColor) }

func tick() tea.Cmd { return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }) }

func waitAgent(a *Agent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-a.events
		if !ok {
			return agentClosed{a}
		}
		return ev
	}
}

// ---------- update ----------

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil
	case tea.BackgroundColorMsg:
		m.dark = msg.IsDark()
		for _, t := range []*textarea.Model{&m.chat, &m.notes, &m.otherIn, &m.reason} {
			t.SetStyles(inputStyles(m.dark))
		}
		m.md = map[string]string{}
		return m, nil
	case tickMsg:
		if !m.expired && m.timeLeft() <= 0 || !m.expired && !alive(m.req.HookPID) {
			m.expired = true
			m.scr = scrExpired
		}
		return m, tick()
	case agentMsg:
		return m, tea.Batch(m.onAgent(msg), waitAgent(m.agent))
	case agentClosed:
		if msg.a == m.agent {
			m.agent = nil
		}
		return m, nil
	case tea.MouseClickMsg:
		return m, m.click(msg)
	case tea.MouseWheelMsg:
		m.wheel(msg)
		return m, nil
	case tea.PasteMsg:
		return m, m.toInput(msg)
	case tea.KeyPressMsg:
		return m, m.key(msg)
	}
	return m, m.toInput(msg)
}

func (m *model) timeLeft() time.Duration { return time.Until(time.Unix(m.req.Deadline, 0)) }

func alive(pid int) bool { return pid <= 0 || syscall.Kill(pid, 0) == nil }

func (m *model) input() *textarea.Model {
	switch m.focus {
	case fChat:
		return &m.chat
	case fNote:
		return &m.notes
	case fOther:
		return &m.otherIn
	case fReason:
		return &m.reason
	}
	return nil
}

func (m *model) toInput(msg tea.Msg) tea.Cmd {
	in := m.input()
	if in == nil {
		return nil
	}
	var cmd tea.Cmd
	*in, cmd = in.Update(msg)
	if m.focus == fOther {
		m.qs[m.cur].other = m.otherIn.Value()
	}
	return cmd
}

func (m *model) setFocus(f focus) tea.Cmd {
	for _, t := range []*textarea.Model{&m.chat, &m.notes, &m.otherIn, &m.reason} {
		t.Blur()
	}
	m.focus = f
	if in := m.input(); in != nil {
		return in.Focus()
	}
	return nil
}

func (m *model) key(k tea.KeyPressMsg) tea.Cmd {
	s := k.String()
	if s == "ctrl+c" {
		m.stopAgent()
		return tea.Quit
	}
	if m.scr == scrExpired {
		if s == "enter" {
			m.copyAll()
		}
		if s == "q" || s == "esc" {
			return tea.Quit
		}
		return nil
	}
	if in := m.input(); in != nil {
		switch s {
		case "esc":
			if m.focus == fReason {
				m.scr = scrQuestion
			}
			if m.focus == fNote {
				m.finishNote()
			}
			return m.setFocus(fOptions)
		case "tab":
			return m.toggleTab()
		case "enter":
			v := in.Value()
			if strings.HasSuffix(v, `\`) { // Claude Code's newline escape
				in.SetValue(strings.TrimSuffix(v, `\`) + "\n")
				return nil
			}
			return m.submitInput()
		}
		return m.toInput(k)
	}
	q := &m.qs[m.cur]
	multi := m.req.Questions[m.cur].MultiSelect
	n := len(m.req.Questions[m.cur].Options) + 1
	switch s {
	case "tab":
		return m.toggleTab()
	case "up":
		if m.scr == scrQuestion && q.cursor > 0 {
			q.cursor--
			q.showMore = false
		}
	case "down":
		if m.scr == scrQuestion && q.cursor < n-1 {
			q.cursor++
			q.showMore = false
		}
	case "space":
		if m.scr == scrQuestion {
			return m.pick(m.cur, q.cursor)
		}
	case "enter":
		switch {
		case m.scr == scrReview:
			return m.submit()
		case m.scr != scrQuestion:
		case multi:
			return m.next()
		default:
			return m.choose(q.cursor)
		}
	case "right":
		return m.next()
	case "left":
		return m.back()
	default:
		if m.scr != scrQuestion {
			return nil
		}
		if len(s) == 1 && s[0] >= '1' && s[0] <= '9' && int(s[0]-'1') < n {
			if multi {
				return m.pick(m.cur, int(s[0]-'1'))
			}
			return m.choose(int(s[0] - '1'))
		}
		if t := k.Key().Text; t != "" && t != " " {
			cmd := m.startNote(m.cur) // typing on the options starts a note
			m.notes.InsertString(t)
			return cmd
		}
	}
	return nil
}

// choose picks a single-select option and moves on, unless it needs typed text first.
func (m *model) choose(oi int) tea.Cmd {
	if cmd := m.pick(m.cur, oi); m.focus == fOther {
		return cmd
	}
	return m.next()
}

func (m *model) toggleTab() tea.Cmd {
	if m.scr != scrQuestion {
		return nil
	}
	if m.wide() {
		if m.focus == fChat {
			return m.setFocus(fOptions)
		}
		return m.setFocus(fChat)
	}
	m.askTab = !m.askTab
	if m.askTab {
		m.qs[m.cur].unread = 0
		return m.setFocus(fChat)
	}
	return m.setFocus(fOptions)
}

func (m *model) pick(qi, oi int) tea.Cmd {
	q := &m.qs[qi]
	opts := m.req.Questions[qi].Options
	q.cursor = oi
	if m.req.Questions[qi].MultiSelect {
		q.picked[oi] = !q.picked[oi]
	} else {
		q.picked = map[int]bool{oi: true}
	}
	if oi == len(opts) && q.picked[oi] {
		m.otherIn.SetValue(q.other)
		return m.setFocus(fOther)
	}
	return nil
}

func (m *model) submitInput() tea.Cmd {
	switch m.focus {
	case fChat:
		text := strings.TrimSpace(m.chat.Value())
		if text == "" {
			return nil
		}
		m.chat.Reset()
		q := &m.qs[m.cur]
		q.thread = append(q.thread, chatMsg{You: true, Text: text}, chatMsg{Live: true, Status: "Starting…"})
		m.threadOff = 0
		return m.ask(m.cur, "chat", fmt.Sprintf("[Q%d: %s] %s", m.cur+1, m.header(m.cur), text))
	case fOther:
		if !m.req.Questions[m.cur].MultiSelect && strings.TrimSpace(m.otherIn.Value()) != "" {
			return tea.Batch(m.setFocus(fOptions), m.next())
		}
		return m.setFocus(fOptions)
	case fNote:
		m.finishNote()
		return m.setFocus(fOptions)
	case fReason:
		return m.sendBack()
	}
	return nil
}

func (m *model) header(i int) string {
	if h := m.req.Questions[i].Header; h != "" {
		return h
	}
	return fmt.Sprintf("Question %d", i+1)
}

// ask sends a request to the side agent, starting it on first use.
func (m *model) ask(q int, kind, text string) tea.Cmd {
	var cmd tea.Cmd
	if m.agent == nil {
		a, err := startAgent(m.req, m.model, "")
		if err != nil {
			m.fail(q, kind, "Could not start the assistant: "+err.Error())
			return nil
		}
		m.agent = a
		cmd = waitAgent(a)
	}
	id := m.nextReq
	m.nextReq++
	m.reqs[id] = &pending{q: q, kind: kind}
	if err := m.agent.Send(id, text); err != nil {
		m.fail(q, kind, "Could not reach the assistant: "+err.Error())
	}
	return cmd
}

func (m *model) fail(q int, kind, text string) {
	if kind == "chat" {
		t := m.qs[q].thread
		if len(t) > 0 && t[len(t)-1].Live {
			t[len(t)-1] = chatMsg{Text: text, Failed: true}
		}
	}
	m.qs[q].drafting = false
}

func (m *model) onAgent(ev agentMsg) tea.Cmd {
	p := m.reqs[ev.Req]
	if p == nil {
		return nil
	}
	q := &m.qs[p.q]
	var live *chatMsg
	if p.kind == "chat" && len(q.thread) > 0 && q.thread[len(q.thread)-1].Live {
		live = &q.thread[len(q.thread)-1]
	}
	switch ev.Kind {
	case "delta":
		p.buf.WriteString(ev.Text)
		if live != nil {
			live.Text = p.buf.String()
			live.Status = "writing…"
		}
	case "tool":
		if live != nil {
			live.Status = ev.Text + "…"
		}
	case "error":
		delete(m.reqs, ev.Req)
		if live != nil {
			live.Live, live.Failed = false, true
			live.Text = strings.TrimSpace(p.buf.String() + "\n\n" + ev.Text)
		}
		q.drafting = false
	case "done":
		delete(m.reqs, ev.Req)
		out := strings.TrimSpace(p.buf.String())
		switch p.kind {
		case "chat":
			if live != nil {
				live.Live, live.Status, live.Text = false, "", out
			}
			if p.q != m.cur || (!m.wide() && !m.askTab) {
				q.unread++
			}
		case "note":
			q.drafting = false
			if !q.noteEdited && out != "NONE" {
				q.note, q.noteByAgent = out, true
			}
		case "reason":
			q.drafting = false
			if out != "NONE" {
				m.reason.SetValue(out)
			}
		}
	}
	return nil
}

// draftNote asks the agent for question q's note when its discussion has moved on.
func (m *model) draftNote(q int) tea.Cmd {
	s := &m.qs[q]
	if s.noteEdited || s.drafting || len(s.thread) == 0 || s.draftedAt == len(s.thread) || s.thread[len(s.thread)-1].Live {
		return nil
	}
	s.drafting, s.draftedAt = true, len(s.thread)
	return m.ask(q, "note", fmt.Sprintf("[DRAFT NOTE Q%d] Write the note for question %d (%s).", q+1, q+1, m.header(q)))
}

func (m *model) goTo(i int) tea.Cmd {
	cmd := m.draftNote(m.cur)
	m.cur, m.scr, m.leftOff, m.threadOff = i, scrQuestion, 0, 0
	m.qs[i].unread = 0
	return tea.Batch(cmd, m.setFocus(fOptions))
}

func (m *model) next() tea.Cmd {
	if m.scr != scrQuestion {
		return nil
	}
	if m.cur+1 < len(m.qs) {
		return m.goTo(m.cur + 1)
	}
	return m.review()
}

func (m *model) back() tea.Cmd {
	switch m.scr {
	case scrReview:
		return m.goTo(len(m.qs) - 1)
	case scrReframe:
		m.scr = scrQuestion
		return m.setFocus(fOptions)
	case scrQuestion:
		if m.cur > 0 {
			return m.goTo(m.cur - 1)
		}
	}
	return nil
}

func (m *model) review() tea.Cmd {
	cmds := []tea.Cmd{m.setFocus(fOptions)}
	for i := range m.qs {
		cmds = append(cmds, m.draftNote(i))
	}
	m.scr, m.editNote = scrReview, -1
	return tea.Batch(cmds...)
}

func (m *model) answered(i int) bool {
	q := m.qs[i]
	for oi, on := range q.picked {
		if on && (oi < len(m.req.Questions[i].Options) || strings.TrimSpace(q.other) != "") {
			return true
		}
	}
	return false
}

func (m *model) answerText(i int) string {
	q := m.qs[i]
	opts := m.req.Questions[i].Options
	var parts []string
	for oi := range opts {
		if q.picked[oi] {
			parts = append(parts, opts[oi].Label)
		}
	}
	if q.picked[len(opts)] && strings.TrimSpace(q.other) != "" {
		parts = append(parts, strings.TrimSpace(q.other))
	}
	return strings.Join(parts, ", ")
}

func (m *model) answeredCount() (n int) {
	for i := range m.qs {
		if m.answered(i) {
			n++
		}
	}
	return n
}

func (m *model) submit() tea.Cmd {
	if m.answeredCount() < len(m.qs) || m.expired {
		return nil
	}
	resp := Response{Kind: "answer", Answers: map[string]string{}, Annotations: map[string]map[string]string{}}
	for i, q := range m.req.Questions {
		resp.Answers[q.Question] = m.answerText(i)
		if n := strings.TrimSpace(m.qs[i].note); n != "" {
			resp.Annotations[q.Question] = map[string]string{"notes": n}
		}
	}
	return m.finish(resp)
}

func (m *model) sendBack() tea.Cmd {
	reason := strings.TrimSpace(m.reason.Value())
	if reason == "" || m.expired {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Question: %s\n\nWhat is wrong with it: %s\n", m.req.Questions[m.cur].Question, reason)
	if n := strings.TrimSpace(m.qs[m.cur].note); m.inclSum && n != "" {
		fmt.Fprintf(&b, "\nSummary of the user's discussion: %s\n", n)
	}
	var kept []string
	for i, q := range m.req.Questions {
		if i != m.cur && m.answered(i) {
			kept = append(kept, fmt.Sprintf("- %s → %s", q.Question, m.answerText(i)))
		}
	}
	if len(kept) > 0 {
		b.WriteString("\nThe user's answers to the other questions, to keep when you ask again:\n" + strings.Join(kept, "\n") + "\n")
	}
	return m.finish(Response{Kind: "reframe", Reason: b.String()})
}

func (m *model) finish(resp Response) tea.Cmd {
	if err := writeJSON(filepath.Join(m.dir, "response.json"), resp); err != nil {
		m.flash = "Could not save the answer: " + err.Error()
		return nil
	}
	recordHistory(m.req, resp, m)
	m.stopAgent()
	return tea.Quit
}

func (m *model) stopAgent() {
	if m.agent != nil {
		m.agent.Stop()
	}
}

func (m *model) switchModel() tea.Cmd {
	next := map[string]string{"sonnet": "opus", "opus": "sonnet"}[m.model]
	if m.agent == nil {
		m.model = next
		return nil
	}
	if m.agent.Busy() {
		m.flash = "Wait for the assistant to finish before switching models."
		return nil
	}
	sid := m.agent.sessionID
	m.agent.Stop()
	a, err := startAgent(m.req, next, sid)
	if err != nil {
		m.flash = "Could not switch models: " + err.Error()
		m.agent = nil
		return nil
	}
	m.agent, m.model = a, next
	return waitAgent(a)
}

func (m *model) finishNote() {
	if m.editNote < 0 {
		m.editNote = m.cur
	}
	q := &m.qs[m.editNote]
	v := strings.TrimSpace(m.notes.Value())
	if v != q.note {
		q.note, q.noteEdited, q.noteByAgent = v, true, false
	}
	m.editNote = -1
}

func (m *model) startNote(i int) tea.Cmd {
	m.editNote = i
	m.notes.SetValue(m.qs[i].note)
	return m.setFocus(fNote)
}

func copyText(s string) { c := exec.Command("pbcopy"); c.Stdin = strings.NewReader(s); _ = c.Run() }

func (m *model) copyAll() {
	var b strings.Builder
	for i, q := range m.req.Questions {
		fmt.Fprintf(&b, "## %s\nQuestion: %s\nAnswer: %s\n", m.header(i), q.Question, or(m.answerText(i), "(none)"))
		if n := m.qs[i].note; n != "" {
			fmt.Fprintf(&b, "Note: %s\n", n)
		}
		for _, c := range m.qs[i].thread {
			who := "Assistant"
			if c.You {
				who = "You"
			}
			fmt.Fprintf(&b, "\n%s: %s\n", who, c.Text)
		}
		b.WriteString("\n")
	}
	copyText(b.String())
	m.flash = "Copied. Paste it into the agent's terminal."
}

func or(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// ---------- mouse ----------

func (m *model) click(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Mouse().Button != tea.MouseLeft {
		return nil
	}
	for i := len(clickables) - 1; i >= 0; i-- {
		id := clickables[i]
		if zone.Get(id).InBounds(msg) {
			return m.action(id)
		}
	}
	if m.scr == scrQuestion && (m.askTab || (m.wide() && msg.Mouse().X >= m.leftWidth())) {
		return m.setFocus(fChat)
	}
	if m.scr == scrQuestion && m.focus != fOptions {
		if m.focus == fNote {
			m.finishNote()
		}
		return m.setFocus(fOptions)
	}
	return nil
}

func (m *model) action(id string) tea.Cmd {
	id, _, _ = strings.Cut(id, "#")
	var a, b int
	switch {
	case sscan(id, "opt-%d-%d", &a, &b):
		if m.focus != fOptions {
			m.setFocus(fOptions)
		}
		return m.pick(a, b)
	case sscan(id, "step-%d", &a):
		if a == len(m.qs) {
			return m.review()
		}
		return m.goTo(a)
	case sscan(id, "change-%d", &a):
		return m.goTo(a)
	case sscan(id, "edit-%d", &a):
		return m.startNote(a)
	case sscan(id, "copy-%d", &a):
		t := m.qs[m.cur].thread
		if a < len(t) {
			copyText(t[a].Text)
			for i := range t {
				t[i].Copied = i == a
			}
		}
	}
	switch id {
	case "next":
		return m.next()
	case "back":
		return m.back()
	case "send-chat":
		m.setFocus(fChat)
		return m.submitInput()
	case "chat-box":
		return m.setFocus(fChat)
	case "other-box":
		return m.setFocus(fOther)
	case "tab-q":
		if m.askTab {
			return m.toggleTab()
		}
	case "tab-ask":
		if !m.askTab {
			return m.toggleTab()
		}
	case "add-note":
		return m.startNote(m.cur)
	case "note-box":
		return m.setFocus(fNote)
	case "note-done":
		m.finishNote()
		return m.setFocus(fOptions)
	case "note-clear":
		m.notes.SetValue("")
	case "note-rewrite":
		i := m.editNote
		m.qs[i].noteEdited, m.qs[i].draftedAt = false, -1
		m.finishNote()
		m.qs[i].noteEdited = false
		m.setFocus(fOptions)
		return m.draftNote(i)
	case "showmore":
		m.qs[m.cur].showMore = !m.qs[m.cur].showMore
	case "model":
		return m.switchModel()
	case "submit":
		return m.submit()
	case "sendback":
		m.scr = scrReframe
		if m.cur >= len(m.qs) {
			m.cur = 0
		}
		return m.setFocus(fReason)
	case "reason-box":
		return m.setFocus(fReason)
	case "reason-draft":
		if len(m.qs[m.cur].thread) > 0 && !m.qs[m.cur].drafting {
			m.qs[m.cur].drafting = true
			return m.ask(m.cur, "reason", fmt.Sprintf("[DRAFT REASON Q%d] Write why question %d should be asked differently.", m.cur+1, m.cur+1))
		}
	case "reason-incl":
		m.inclSum = !m.inclSum
	case "reason-send":
		return m.sendBack()
	case "reason-cancel":
		m.scr = scrQuestion
		return m.setFocus(fOptions)
	case "copyall":
		m.copyAll()
	case "close":
		return tea.Quit
	}
	return nil
}

func sscan(s, format string, args ...any) bool {
	n, err := fmt.Sscanf(s, format, args...)
	return err == nil && n == len(args)
}

func (m *model) wheel(msg tea.MouseWheelMsg) {
	mouse := msg.Mouse()
	d := 0
	switch mouse.Button {
	case tea.MouseWheelUp:
		d = -3
	case tea.MouseWheelDown:
		d = 3
	}
	inThread := m.askTab || (m.wide() && mouse.X >= m.leftWidth())
	if m.scr == scrQuestion && inThread {
		m.threadOff = max(0, m.threadOff-d)
	} else {
		m.leftOff = max(0, m.leftOff+d)
	}
}

// choiceGlyph draws Nerd Font Material icons, the same family tick uses.
func choiceGlyph(multi, on bool) string {
	switch {
	case multi && on:
		return "\U000F0135" // checkbox-marked-outline
	case multi:
		return "\U000F0131" // checkbox-blank-outline
	case on:
		return "\U000F043E" // radiobox-marked
	}
	return "\U000F043D" // radiobox-blank
}

func (m *model) wide() bool     { return m.w >= 130 }
func (m *model) leftWidth() int { if m.wide() { return m.w * 56 / 100 }; return m.w }
func (m *model) chatWidth() int { if m.wide() { return m.w - m.leftWidth() - 3 }; return m.w - 4 }

// ---------- rendering helpers ----------

type palette struct{ accent, muted, ok, warn, bad, text, onAccent lipgloss.Style }

func (m *model) pal() palette {
	ld := lipgloss.LightDark(m.dark)
	st := func(light, dark string) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(ld(lipgloss.Color(light), lipgloss.Color(dark)))
	}
	return palette{
		accent:   st("4", "12"),
		muted:    st("8", "245"),
		ok:       st("2", "10"),
		warn:     st("3", "11"),
		bad:      st("1", "9"),
		text:     lipgloss.NewStyle(),
		onAccent: lipgloss.NewStyle().Bold(true).Background(ld(lipgloss.Color("4"), lipgloss.Color("12"))).Foreground(ld(lipgloss.Color("15"), lipgloss.Color("0"))),
	}
}

func inputStyles(dark bool) textarea.Styles {
	s := textarea.DefaultStyles(dark)
	s.Focused.CursorLine = lipgloss.NewStyle()
	s.Blurred.CursorLine = lipgloss.NewStyle()
	return s
}

func mark(id, s string) string { clickables = append(clickables, id); return zone.Mark(id, s) }

// markLines marks each line on its own, so clipping a tall block never leaves a zone unclosed.
func markLines(id, block string) string {
	lines := strings.Split(block, "\n")
	for i, l := range lines {
		lines[i] = mark(fmt.Sprintf("%s#%d", id, i), l)
	}
	return strings.Join(lines, "\n")
}

func link(id, s string) string { return mark(id, lipgloss.NewStyle().Underline(true).Render(s)) }

func (m *model) button(id, s string) string { return mark(id, m.pal().onAccent.Render(" "+s+" ")) }

func (m *model) markdown(s string, width int) string {
	key := fmt.Sprintf("%d|%v|%s", width, m.dark, s)
	if out, ok := m.md[key]; ok {
		return out
	}
	cfg := styles.DarkStyleConfig
	if !m.dark {
		cfg = styles.LightStyleConfig
	}
	zero := uint(0)
	cfg.Document.Margin = &zero
	cfg.Document.BlockPrefix, cfg.Document.BlockSuffix = "", ""
	cfg.Document.StylePrimitive.Color = nil
	cfg.CodeBlock.Margin = &zero
	r, err := glamour.NewTermRenderer(glamour.WithStyles(cfg), glamour.WithWordWrap(max(width, 20)))
	out := s
	if err == nil {
		if o, err := r.Render(s); err == nil {
			out = strings.Trim(o, "\n")
		}
	}
	m.md[key] = out
	return out
}

func wrap(s string, w int) string { return lipgloss.NewStyle().Width(max(w, 10)).Render(s) }

func bar(style lipgloss.Style, glyph, block string) string {
	lines := strings.Split(block, "\n")
	for i, l := range lines {
		lines[i] = style.Render(glyph) + " " + l
	}
	return strings.Join(lines, "\n")
}

func indent(block string, n int) string {
	pad := strings.Repeat(" ", n)
	lines := strings.Split(block, "\n")
	for i, l := range lines {
		lines[i] = pad + l
	}
	return strings.Join(lines, "\n")
}

func clip(block string, off, h int) (string, int) {
	lines := strings.Split(block, "\n")
	off = max(0, min(off, len(lines)-h))
	end := min(len(lines), off+h)
	out := lines[off:end]
	for len(out) < h {
		out = append(out, "")
	}
	return strings.Join(out, "\n"), off
}

func spread(w int, left, right string) string {
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	return left + strings.Repeat(" ", max(1, gap)) + right
}

func stripRecommended(label string) (string, bool) {
	for _, tag := range []string{" (Recommended)", " (recommended)"} {
		if strings.Contains(label, tag) {
			return strings.Replace(label, tag, "", 1), true
		}
	}
	return label, false
}

// ---------- views ----------

func (m *model) View() tea.View {
	clickables = clickables[:0]
	v := tea.NewView("")
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.ProgramStatus = &tea.ProgramStatus{State: tea.ProgramStateBlocked, App: "huddle", Kind: tea.ProgramStatusKindQuestion,
		Title: "Question waiting", Message: m.req.Questions[0].Question}
	v.WindowTitle = "huddle · " + filepath.Base(m.req.Cwd)
	if m.expired {
		v.ProgramStatus = &tea.ProgramStatus{State: tea.ProgramStateError, App: "huddle", Message: "The agent stopped waiting"}
	}
	if m.w == 0 {
		return v
	}
	var body string
	switch m.scr {
	case scrQuestion:
		body = m.viewQuestion()
	case scrReview:
		body = m.viewReview()
	case scrReframe:
		body = m.viewReframe()
	case scrExpired:
		body = m.viewExpired()
	}
	header := m.viewHeader()
	footer := m.viewFooter()
	bh := m.h - lipgloss.Height(header) - lipgloss.Height(footer)
	body, _ = clip(body, 0, bh)
	v.SetContent(zone.Scan(lipgloss.JoinVertical(lipgloss.Left, header, body, footer)))
	return v
}

func (m *model) rule() string { return m.pal().muted.Render(strings.Repeat("─", m.w)) }

func (m *model) viewHeader() string {
	p := m.pal()
	left := " " + p.accent.Bold(true).Render("huddle") + "  " + filepath.Base(m.req.Cwd)
	if m.req.Branch != "" {
		left += p.muted.Render(" · ") + m.req.Branch
	}
	if m.req.Title != "" && m.wide() {
		left += p.muted.Render(" · " + m.req.Title)
	}
	var right string
	switch left := m.timeLeft(); {
	case m.expired:
		right = p.bad.Bold(true).Render("✗ time is up")
	case left < 5*time.Minute:
		right = p.warn.Bold(true).Render(fmt.Sprintf("▲ %s left · send soon", fmtDur(left)))
	default:
		right = fmtDur(left) + " left"
	}
	lines := []string{spread(m.w-1, left, right)}
	if !m.wide() && m.scr == scrQuestion {
		qt, at := "Question", "Ask about it"
		if n := m.qs[m.cur].unread; n > 0 {
			at += p.accent.Render(fmt.Sprintf(" · %d new", n))
		}
		sel := lipgloss.NewStyle().Reverse(true).Bold(true)
		if m.askTab {
			at = sel.Render(" " + at + " ")
			qt = lipgloss.NewStyle().Underline(true).Render(qt)
		} else {
			qt = sel.Render(" " + qt + " ")
			at = lipgloss.NewStyle().Underline(true).Render(at)
		}
		lines = append(lines, spread(m.w-1, " "+mark("tab-q", qt)+"  "+mark("tab-ask", at), p.muted.Render(fmt.Sprintf("%d of %d", m.cur+1, len(m.qs)))))
	}
	return strings.Join(lines, "\n") + "\n" + m.rule()
}

func fmtDur(d time.Duration) string {
	if d >= time.Minute {
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
	return fmt.Sprintf("%d s", max(0, int(d.Seconds())))
}

// footItem is one footer element; short is used when the full footer does not fit.
type footItem struct {
	id, label, short string
	kind             int // 0 link, 1 button, 2 muted text
}

func (m *model) viewFooter() string {
	var left, right []footItem
	switch m.scr {
	case scrQuestion:
		left = []footItem{{"sendback", "Send this question back", "Send back", 0}, {"", m.focusHint(), "", 2}}
		next := footItem{"next", "Review →", "Review →", 1}
		if m.cur+1 < len(m.qs) {
			next = footItem{"next", "Next: " + m.header(m.cur+1) + " →", "Next →", 1}
		}
		if m.cur > 0 {
			right = append(right, footItem{"back", "‹ Back", "‹", 0})
		}
		right = append(right, next)
	case scrReview:
		left = []footItem{{"sendback", "Send a question back instead", "Send back", 0}}
		right = []footItem{{"back", "‹ Back", "‹", 0}}
		if n := len(m.qs); m.answeredCount() < n {
			right = append(right, footItem{"", fmt.Sprintf("Answer all %d questions to send", n), "Answer all to send", 2})
		} else {
			right = append(right, footItem{"submit", fmt.Sprintf("Send %d answers ✓", n), "Send ✓", 1})
		}
	case scrReframe:
		right = []footItem{{"reason-cancel", "Cancel", "Cancel", 0}, {"reason-send", "Send back ↩", "Send back ↩", 1}}
	case scrExpired:
		right = []footItem{{"close", "Close", "Close", 0}, {"copyall", "Copy answers and discussion", "Copy all", 1}}
	}
	if m.flash != "" {
		left = append(left, footItem{"", m.flash, "", 3})
	}
	// Try full labels, then drop hints, then short labels, so the main button always shows.
	for stage := 0; stage < 3; stage++ {
		width := func(items []footItem, dropHints bool) (w int) {
			for _, it := range items {
				l := it.label
				if stage == 2 && it.short != "" {
					l = it.short
				}
				if stage >= 1 && dropHints && it.kind == 2 {
					continue
				}
				w += lipgloss.Width(l) + 3
			}
			return w
		}
		if width(left, true)+width(right, false)+4 <= m.w || stage == 2 {
			return m.rule() + "\n" + spread(m.w-1, "  "+m.renderFoot(left, stage, true), m.renderFoot(right, stage, false)+" ")
		}
	}
	return ""
}

func (m *model) renderFoot(items []footItem, stage int, dropHints bool) string {
	p := m.pal()
	var parts []string
	for _, it := range items {
		l := it.label
		if stage == 2 && it.short != "" {
			l = it.short
		}
		switch it.kind {
		case 0:
			parts = append(parts, link(it.id, l))
		case 1:
			parts = append(parts, m.button(it.id, l))
		case 2:
			if stage >= 1 && dropHints {
				continue
			}
			parts = append(parts, p.muted.Render(l))
		case 3:
			parts = append(parts, p.warn.Render(l))
		}
	}
	return strings.Join(parts, "   ")
}

func (m *model) stepper() string {
	p := m.pal()
	var parts []string
	for i := 0; i <= len(m.qs); i++ {
		label, done := "Review", false
		if i < len(m.qs) {
			label, done = m.header(i), m.answered(i)
		}
		active := (m.scr == scrReview && i == len(m.qs)) || (m.scr != scrReview && i == m.cur)
		var s string
		switch {
		case active:
			s = p.accent.Render("●") + " " + lipgloss.NewStyle().Bold(true).Render(label)
		case done:
			s = p.ok.Render("✓") + " " + p.muted.Render(label)
		default:
			s = p.muted.Render("○ " + label)
		}
		parts = append(parts, mark(fmt.Sprintf("step-%d", i), s))
	}
	return strings.Join(parts, p.muted.Render(" ── "))
}

func (m *model) viewQuestion() string {
	left := m.viewOptions(m.leftWidth() - 6)
	if !m.wide() {
		if m.askTab {
			return m.viewThread(m.w-4, m.h-6)
		}
		h := m.h - 6
		out, off := clip(indent(left, 3), m.leftOff, h)
		m.leftOff = off
		return out
	}
	h := m.h - 4
	l, off := clip(indent(left, 3), m.leftOff, h)
	m.leftOff = off
	sep := m.pal().muted.Render(strings.TrimRight(strings.Repeat("│\n", h), "\n"))
	r := m.viewThread(m.chatWidth(), h)
	return lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(m.leftWidth()).Render(l), sep, " ", r)
}

func (m *model) viewOptions(w int) string {
	p := m.pal()
	q := m.req.Questions[m.cur]
	st := m.qs[m.cur]
	var b strings.Builder
	b.WriteString("\n" + m.stepper() + "\n\n")
	b.WriteString(p.muted.Render(fmt.Sprintf("Question %d of %d", m.cur+1, len(m.qs))))
	if q.MultiSelect {
		b.WriteString(p.muted.Render(" · pick any"))
	}
	b.WriteString("\n" + lipgloss.NewStyle().Bold(true).Render(wrap(q.Question, w)) + "\n\n")
	n := len(q.Options)
	for i := 0; i <= n; i++ {
		label, desc, preview := otherLabel, "", ""
		if i < n {
			label, desc, preview = q.Options[i].Label, q.Options[i].Description, q.Options[i].Preview
		}
		label, rec := stripRecommended(label)
		on := st.picked[i]
		glyph := choiceGlyph(q.MultiSelect, on)
		if on {
			glyph = p.accent.Render(glyph)
		}
		lbl := label
		if i == st.cursor || on {
			lbl = lipgloss.NewStyle().Bold(true).Render(label)
		}
		head := glyph + " " + lbl
		if rec {
			head += "   " + p.ok.Render("Recommended")
		}
		var block []string
		block = append(block, mark(fmt.Sprintf("opt-%d-%d#0", m.cur, i), head))
		if desc != "" {
			for j, l := range strings.Split(wrap(desc, w-4), "\n") {
				block = append(block, mark(fmt.Sprintf("opt-%d-%d#%d", m.cur, i, j+1), "  "+p.muted.Render(l)))
			}
		}
		if i == st.cursor && preview != "" {
			pv := strings.Split(m.markdown(preview, w-4), "\n")
			capLines := max(6, min(14, m.h/4))
			if len(pv) > capLines && !st.showMore {
				pv = append(pv[:capLines], link("showmore", fmt.Sprintf("Show %d more lines", len(pv)-capLines)))
			} else if len(pv) > capLines {
				pv = append(pv, link("showmore", "Show less"))
			}
			block = append(block, "")
			for _, l := range pv {
				block = append(block, "  "+l)
			}
		}
		if i == n && on {
			block = append(block, markLines("other-box", indent(m.boxed(&m.otherIn, m.focus == fOther, w-6), 2)))
		}
		text := strings.Join(block, "\n")
		if i == st.cursor && m.focus == fOptions {
			text = bar(p.accent, "▌", text)
		} else {
			text = indent(text, 2)
		}
		b.WriteString(text + "\n\n")
	}
	if m.editNote == m.cur && m.focus == fNote {
		b.WriteString(p.muted.Render("Note for the agent") + "  " + p.accent.Render("editing") + "\n")
		b.WriteString(markLines("note-box", m.boxed(&m.notes, true, w)) + "\n")
		b.WriteString(spread(w, "", link("note-clear", "Clear")+"   "+mark("note-done", "[ Done ]")) + "\n")
	} else if st.note != "" {
		who := "your note"
		if st.noteByAgent {
			who = "drafted by the assistant"
		}
		b.WriteString(p.muted.Render("Note for the agent  ") + p.muted.Italic(true).Render(who) + "   " + link("add-note", "Edit") + "\n")
		b.WriteString(bar(p.muted, "│", wrap(st.note, w-2)) + "\n")
	} else {
		b.WriteString(link("add-note", "+ Add a note for the agent") + "\n" + p.muted.Render(wrap("Optional. The assistant drafts one from your discussion at review.", w)) + "\n")
	}
	return b.String()
}

// boxed draws an input in a rounded border exactly w cells wide.
func (m *model) boxed(t *textarea.Model, focused bool, w int) string {
	t.SetWidth(max(w-4, 10))
	ld := lipgloss.LightDark(m.dark)
	c := ld(lipgloss.Color("7"), lipgloss.Color("240"))
	if focused {
		c = ld(lipgloss.Color("4"), lipgloss.Color("12"))
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(c).Padding(0, 1).Render(t.View())
}

func (m *model) viewThread(w, h int) string {
	p := m.pal()
	st := &m.qs[m.cur]
	title := lipgloss.NewStyle().Bold(true).Render("Ask about this question")
	modelSw := link("model", "model: "+m.model+" ⇄")
	head := spread(w, title, modelSw) + "\n" + p.muted.Render("The assistant can read your agent's conversation.") + "\n"
	var b strings.Builder
	if len(st.thread) == 0 {
		b.WriteString(p.muted.Render(wrap("Ask what the question means, what each option implies, or what the assistant would pick and why.", w-2)))
	}
	for i, c := range st.thread {
		if c.You {
			b.WriteString(bar(p.muted, "│", lipgloss.NewStyle().Bold(true).Render("You")+"\n"+wrap(c.Text, w-3)))
		} else {
			name := p.accent.Bold(true).Render("Assistant")
			switch {
			case c.Live:
				name += "  " + p.muted.Italic(true).Render(c.Status)
			case c.Failed:
				name += "  " + p.bad.Render("✗ failed")
			case c.Copied:
				name += "  " + p.ok.Render("✓ copied")
			default:
				name += "  " + p.muted.Render(link(fmt.Sprintf("copy-%d", i), "Copy"))
			}
			body := c.Text
			if body != "" {
				body = m.markdown(body, w-3)
			}
			b.WriteString(bar(p.accent, "▎", strings.TrimRight(name+"\n"+body, "\n")))
		}
		b.WriteString("\n\n")
	}
	input := markLines("chat-box", m.boxed(&m.chat, m.focus == fChat, w))
	hint := p.muted.Render("Enter sends · Shift+Enter new line") + "   " + mark("send-chat", lipgloss.NewStyle().Bold(true).Render("[ Send ]"))
	footer := input + "\n" + spread(w, "", hint)
	avail := h - lipgloss.Height(head) - lipgloss.Height(footer) - 1
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	maxOff := max(0, len(lines)-avail)
	m.threadOff = min(m.threadOff, maxOff)
	start := max(0, len(lines)-avail-m.threadOff)
	view := lines[start:min(len(lines), start+avail)]
	for len(view) < avail {
		view = append(view, "")
	}
	if m.threadOff > 0 {
		view[len(view)-1] = p.muted.Render(fmt.Sprintf("↓ %d more lines below", m.threadOff))
	}
	return head + "\n" + strings.Join(view, "\n") + "\n" + footer
}

func (m *model) center(s string, w int) string {
	pad := max(2, (m.w-w)/2)
	return indent(s, pad)
}

func (m *model) viewReview() string {
	p := m.pal()
	w := min(m.w-6, 96)
	var b strings.Builder
	b.WriteString("\n" + m.stepper() + "\n\n")
	n := m.answeredCount()
	count := p.ok.Render(fmt.Sprintf("✓ %d of %d answered", n, len(m.qs)))
	if n < len(m.qs) {
		count = p.warn.Render(fmt.Sprintf("▲ %d of %d answered", n, len(m.qs)))
	}
	b.WriteString(spread(w, lipgloss.NewStyle().Bold(true).Render("Review your answers"), count) + "\n")
	b.WriteString(p.muted.Render("Notes are drafted from your discussions. Edit anything before you send.") + "\n")
	for i := range m.qs {
		st := m.qs[i]
		b.WriteString("\n" + p.muted.Render(strings.Repeat("─", w)) + "\n\n")
		b.WriteString(spread(w, fmt.Sprintf("%d   ", i+1)+lipgloss.NewStyle().Bold(true).Render(m.header(i)), link(fmt.Sprintf("change-%d", i), "Change")) + "\n")
		ans := m.answerText(i)
		if ans == "" {
			ans = p.warn.Render("▲ not answered yet")
		} else {
			ans = p.accent.Render(choiceGlyph(m.req.Questions[i].MultiSelect, true)) + " " + ans
		}
		b.WriteString("    " + ans + "\n\n")
		switch {
		case m.editNote == i && m.focus == fNote:
			b.WriteString("    " + p.muted.Render("Note for the agent  ") + p.accent.Render("editing") + "\n")
			b.WriteString(markLines("note-box", indent(m.boxed(&m.notes, true, w-4), 4)) + "\n")
			b.WriteString(spread(w, "", link("note-rewrite", "Rewrite with assistant")+"   "+link("note-clear", "Clear")+"   "+mark("note-done", "[ Done ]")) + "\n")
		case st.drafting:
			b.WriteString(spread(w, "    "+p.muted.Render("Note for the agent  ")+p.muted.Italic(true).Render("drafting…"), "") + "\n")
		case st.note != "":
			who := "your note"
			if st.noteByAgent {
				who = "drafted by the assistant"
			}
			b.WriteString(spread(w, "    "+p.muted.Render("Note for the agent  ")+p.muted.Italic(true).Render(who), link(fmt.Sprintf("edit-%d", i), "Edit")) + "\n")
			b.WriteString(indent(bar(p.muted, "│", wrap(st.note, w-8)), 4) + "\n")
		default:
			b.WriteString(spread(w, "    "+p.muted.Render("No note"), link(fmt.Sprintf("edit-%d", i), "Add note")) + "\n")
		}
	}
	out, off := clip(m.center(b.String(), w), m.leftOff, m.h-4)
	m.leftOff = off
	return out
}

func (m *model) viewReframe() string {
	p := m.pal()
	w := min(m.w-6, 92)
	var b strings.Builder
	b.WriteString("\n\n" + lipgloss.NewStyle().Bold(true).Render("Send this question back") + "\n")
	b.WriteString(p.muted.Render(wrap("Use this when the question itself is wrong. The agent reads your reason and asks again.", w)) + "\n\n")
	b.WriteString(p.muted.Render("The question") + "\n")
	b.WriteString(bar(p.muted, "│", wrap(m.req.Questions[m.cur].Question, w-2)) + "\n\n")
	b.WriteString(lipgloss.NewStyle().Bold(true).Render("What is wrong with it?") + "\n")
	b.WriteString(markLines("reason-box", m.boxed(&m.reason, m.focus == fReason, w)) + "\n")
	draft := ""
	switch {
	case m.qs[m.cur].drafting:
		draft = p.muted.Italic(true).Render("drafting…")
	case len(m.qs[m.cur].thread) > 0:
		draft = link("reason-draft", "Draft from my discussion")
	}
	b.WriteString(spread(w, "", draft) + "\n\n")
	box := "☐"
	if m.inclSum {
		box = p.accent.Render("☑")
	}
	b.WriteString(mark("reason-incl", box+" Include my note from the discussion") + "\n")
	b.WriteString(p.muted.Render("Your answers to the other questions are kept.") + "\n")
	return m.center(b.String(), w)
}

func (m *model) viewExpired() string {
	p := m.pal()
	w := min(m.w-6, 92)
	var b strings.Builder
	b.WriteString("\n\n\n" + p.bad.Render("✗") + " " + lipgloss.NewStyle().Bold(true).Render("The agent stopped waiting") + "\n")
	b.WriteString(p.muted.Render(wrap(filepath.Base(m.req.Cwd)+" moved on, so answers can no longer be sent. Your discussion is saved: copy it and paste it into the agent's terminal.", w)) + "\n\n")
	b.WriteString(p.muted.Render("What you had so far") + "\n" + p.muted.Render(strings.Repeat("─", w)) + "\n")
	for i := range m.qs {
		msgs := len(m.qs[i].thread)
		meta := fmt.Sprintf("%d messages", msgs)
		if msgs == 0 {
			meta = "no messages"
		}
		if m.qs[i].note != "" {
			meta += " · note"
		}
		b.WriteString(spread(w, fmt.Sprintf("%-16s %s", m.header(i), or(m.answerText(i), p.muted.Render("no answer"))), p.muted.Render(meta)) + "\n")
	}
	b.WriteString(p.muted.Render(strings.Repeat("─", w)) + "\n")
	return m.center(b.String(), w)
}

// focusHint says how to move between the options and the text boxes.
func (m *model) focusHint() string {
	switch {
	case m.focus != fOptions:
		return "Esc: back to options"
	case m.wide():
		return "Tab: chat · type to add a note"
	case m.askTab:
		return "Tab: question"
	}
	return "Tab: ask about it · type to add a note"
}
