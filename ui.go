package main

import (
	"fmt"
	"image/color"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
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
	You    bool
	Text   string
	Status string // what the assistant is doing right now
	Live   bool
	Failed bool
	Copied bool
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
	req        *Request
	dir        string
	w, h       int
	dark       bool
	bg         color.Color
	spin       spinner.Model
	sendAnyway bool
	sel        *selection
	selDone    bool
	frame      []string // last rendered lines, for copying a selection
	scr        screen
	cur        int // question in focus
	qs         []qstate
	focus      focus
	askTab     bool // narrow layout: showing "Ask about it"
	editNote   int  // review: question whose note is being edited, -1 none
	chat       textarea.Model
	notes      textarea.Model
	otherIn    textarea.Model
	reason     textarea.Model
	inclSum    bool
	agent      *Agent
	model      string
	reqs       map[int]*pending
	nextReq    int
	leftOff    int
	threadOff  int // lines scrolled up from the bottom
	md         map[string]string
	flash      string
	expired    bool
}

type tickMsg time.Time
type agentClosed struct{ a *Agent }

const otherLabel = "Something else…"

var clickables []string

func newModel(req *Request, dir string) *model {
	m := &model{req: req, dir: dir, dark: systemDark(), editNote: -1, spin: spinner.New(spinner.WithSpinner(spinner.MiniDot)), model: "sonnet", reqs: map[int]*pending{}, md: map[string]string{}, inclSum: true}
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
	t.SetStyles(inputStyles(systemDark()))
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
	case spinner.TickMsg:
		if !m.anyLive() && !m.drafting() {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case tea.BackgroundColorMsg:
		m.dark, m.bg = msg.IsDark(), msg.Color
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
		mo := msg.Mouse()
		if mo.Button == tea.MouseLeft {
			m.sel = &selection{ax: mo.X, ay: mo.Y, hx: mo.X, hy: mo.Y, press: msg, maxX: m.w}
			if m.scr == scrQuestion && m.wide() {
				if mo.X >= m.leftWidth() {
					m.sel.minX = m.leftWidth() + 1
				} else {
					m.sel.maxX = m.leftWidth()
				}
			}
			m.selDone = false
		}
		return m, nil
	case tea.MouseMotionMsg:
		if mo := msg.Mouse(); m.sel != nil && !m.selDone {
			m.sel.hx, m.sel.hy, m.sel.dragged = mo.X, mo.Y, m.sel.dragged || mo.X != m.sel.ax || mo.Y != m.sel.ay
		}
		return m, nil
	case tea.MouseReleaseMsg:
		if m.sel == nil || m.selDone {
			return m, nil
		}
		if !m.sel.dragged { // a plain click
			press := m.sel.press
			m.sel = nil
			return m, m.click(press)
		}
		m.selDone = true
		if text := m.selectedText(); text != "" {
			copyText(text)
			m.flash = fmt.Sprintf("Copied %d characters · Cmd+V pastes into the note or chat", len([]rune(text)))
		}
		return m, nil
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

// setFocus moves keyboard focus. Leaving the note saves it; in the narrow layout the visible
// tab always follows focus, so keys never land in a pane that is off screen.
func (m *model) setFocus(f focus) tea.Cmd {
	if m.focus == fNote && f != fNote {
		m.finishNote()
	}
	for _, t := range []*textarea.Model{&m.chat, &m.notes, &m.otherIn, &m.reason} {
		t.Blur()
	}
	m.focus = f
	if m.scr == scrQuestion && !m.wide() {
		m.askTab = f == fChat
		if m.askTab {
			m.qs[m.cur].unread = 0
		}
	}
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
			return m.setFocus(fOptions)
		case "pgup", "pgdown":
			if m.focus == fChat {
				m.threadOff = max(0, m.threadOff+map[string]int{"pgup": 10, "pgdown": -10}[s])
				return nil
			}
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
	noteRow := n
	switch s {
	case "tab", "shift+tab":
		return m.toggleTab()
	case "up":
		if m.scr == scrQuestion && q.cursor > 0 {
			q.cursor--
			q.showMore = false
		}
	case "down":
		if m.scr == scrQuestion && q.cursor < noteRow {
			q.cursor++
			q.showMore = false
		}
	case "space":
		if m.scr == scrQuestion && q.cursor == noteRow {
			return m.startNote(m.cur)
		}
		if m.scr == scrQuestion {
			return m.pick(m.cur, q.cursor)
		}
	case "enter":
		switch {
		case m.scr == scrReview:
			return m.submit()
		case m.scr != scrQuestion:
		case q.cursor == noteRow:
			return m.startNote(m.cur)
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
		if s == "/" {
			return m.startNote(m.cur)
		}
		if t := k.Key().Text; t != "" && t != " " { // typing on the options starts a chat message
			cmd := m.setFocus(fChat)
			m.chat.InsertString(t)
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
	if m.focus == fChat {
		return m.setFocus(fOptions)
	}
	return m.setFocus(fChat)
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
		return tea.Batch(m.ask(m.cur, "chat", fmt.Sprintf("[Q%d: %s] %s", m.cur+1, m.header(m.cur), text)), m.spin.Tick)
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
	return tea.Batch(append(cmds, m.spin.Tick)...)
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
	if m.drafting() && !m.sendAnyway {
		m.sendAnyway = true
		m.flash = "Notes are still drafting. Send again to send without them."
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
	case sscan(id, "sendback-%d", &a):
		m.cur = a
		m.scr = scrReframe
		return m.setFocus(fReason)
	case sscan(id, "starter-%d", &a):
		m.chat.SetValue(starters[a])
		m.setFocus(fChat)
		return m.submitInput()
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
	case "tab-q", "kb-esc":
		return m.setFocus(fOptions)
	case "tab-ask", "kb-tab":
		return m.setFocus(fChat)
	case "kb-note", "note-row":
		return m.startNote(m.cur)
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

func (m *model) wide() bool { return m.w >= 130 }
func (m *model) leftWidth() int {
	if m.wide() {
		return m.w * 56 / 100
	}
	return m.w
}
func (m *model) chatWidth() int {
	if m.wide() {
		return m.w - m.leftWidth() - 3
	}
	return m.w - 4
}

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

// inputStyles keeps text boxes on the terminal's own colours: no line band, default text colour.
func inputStyles(dark bool) textarea.Styles {
	s := textarea.DefaultStyles(dark)
	for _, st := range []*textarea.StyleState{&s.Focused, &s.Blurred} {
		st.Base, st.CursorLine, st.Text, st.EndOfBuffer = lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle()
		st.Placeholder = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	}
	return s
}

// systemDark guesses the theme from macOS appearance until the terminal reports its background.
func systemDark() bool {
	out, _ := exec.Command("defaults", "read", "-g", "AppleInterfaceStyle").Output()
	return strings.Contains(string(out), "Dark")
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
	content := zone.Scan(lipgloss.JoinVertical(lipgloss.Left, header, body, footer))
	m.frame = strings.Split(content, "\n")
	v.SetContent(m.highlight(content))
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
		chip := func(title string, on bool) string {
			if on {
				return p.onAccent.Render(" " + title + " ")
			}
			return lipgloss.NewStyle().Underline(true).Foreground(p.muted.GetForeground()).Render(title)
		}
		qt, at := chip("Answer", !m.askTab), chip("Ask about it", m.askTab)
		if n := m.qs[m.cur].unread; n > 0 {
			at += p.accent.Render(fmt.Sprintf(" · %d new", n))
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
	var right []footItem
	switch m.scr {
	case scrQuestion:
		right = append(right, footItem{"sendback", icBack + " Send back", icBack, 0})
		if m.cur > 0 {
			right = append(right, footItem{"back", "‹ Back", "‹", 0})
		}
		label := "Review →"
		if m.cur+1 < len(m.qs) {
			label = "Next →"
		}
		right = append(right, footItem{"next", label, label, 1})
	case scrReview:
		right = []footItem{{"back", "‹ Back", "‹", 0}}
		if n := len(m.qs); m.answeredCount() < n {
			right = append(right, footItem{"", fmt.Sprintf("Answer all %d questions to send", n), "Answer all", 2})
		} else {
			right = append(right, footItem{"submit", fmt.Sprintf("Send %d answers ✓", n), "Send ✓", 1})
		}
	case scrReframe:
		right = []footItem{{"reason-cancel", "Cancel", "Cancel", 0}, {"reason-send", "Send back ↩", "Send back ↩", 1}}
	case scrExpired:
		right = []footItem{{"close", "Close", "Close", 0}, {"copyall", "Copy answers and discussion", "Copy all", 1}}
	}
	short := 0
	for _, it := range right {
		short += lipgloss.Width(it.label) + 3
	}
	stage := 0
	if short+12 > m.w {
		stage = 2
	}
	r := m.renderFoot(right, stage, false)
	left := m.keyBar(m.w - lipgloss.Width(r) - 6)
	if m.flash != "" {
		left = m.pal().warn.Render(m.flash)
	}
	return m.rule() + "\n" + spread(m.w-1, "  "+left, r+" ")
}

type keyHint struct{ id, key, desc string }

// keyBar names where keystrokes go right now; each hint with an id is also clickable.
func (m *model) keyBar(w int) string {
	p := m.pal()
	var hints []keyHint
	switch {
	case m.scr == scrReview:
		hints = []keyHint{{"submit", "⏎", "send"}, {"", "←", "back"}}
	case m.scr != scrQuestion:
		hints = []keyHint{{"", "⏎", "send"}, {"", "esc", "cancel"}}
	case m.focus == fChat:
		hints = []keyHint{{"send-chat", "⏎", "send"}, {"", "⇧⏎", "newline"}, {"", "pgup/dn", "scroll"}, {"kb-esc", "esc", "options"}}
	case m.focus == fNote:
		hints = []keyHint{{"note-done", "⏎", "done"}, {"", "⇧⏎", "newline"}, {"kb-esc", "esc", "done"}}
	case m.focus == fOther:
		hints = []keyHint{{"", "⏎", "done"}, {"kb-esc", "esc", "options"}}
	case m.req.Questions[m.cur].MultiSelect:
		hints = []keyHint{{"", "↑↓", "move"}, {"", "space", "toggle"}, {"next", "⏎", "next"}, {"kb-tab", "tab", "ask"}, {"kb-note", "/", "note"}}
	default:
		hints = []keyHint{{"", "↑↓", "move"}, {"", "⏎", "choose"}, {"", fmt.Sprintf("1-%d", len(m.req.Questions[m.cur].Options)+1), "pick"}, {"kb-tab", "tab", "ask"}, {"kb-note", "/", "note"}}
	}
	var parts []string
	used := 0
	for _, h := range hints {
		s := lipgloss.NewStyle().Bold(true).Render(h.key) + " " + p.muted.Render(h.desc)
		if used+lipgloss.Width(s)+3 > w {
			break
		}
		used += lipgloss.Width(s) + 3
		if h.id != "" {
			s = mark(h.id+"#kb", s)
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, p.muted.Render("  ·  "))
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
	h := m.h - 4
	if !m.wide() {
		h = m.h - 5
		if m.askTab {
			return indent(m.viewThread(m.w-4, h), 2)
		}
		out, off := clip(indent(m.viewOptions(m.w-6), 3), m.leftOff, h)
		m.leftOff = off
		return out
	}
	lw, rw := m.leftWidth(), m.w-m.leftWidth()-1
	leftFocused := m.focus != fChat
	lhead := m.paneHead(lw, "tab-q", "Answer", leftFocused, m.stepper())
	l, off := clip(indent(m.viewOptions(lw-6), 3), m.leftOff, h-2)
	m.leftOff = off
	rhead := m.paneHead(rw, "tab-ask", "Ask about it", !leftFocused, link("model", "model: "+m.model+" ⇄"))
	r := indent(m.viewThread(rw-4, h-2), 2)
	sep := m.pal().muted.Render(strings.TrimRight(strings.Repeat("│\n", h), "\n"))
	return lipgloss.JoinHorizontal(lipgloss.Top,
		m.tint(lhead+"\n"+l, lw, leftFocused), sep, m.tint(rhead+"\n"+r, rw, !leftFocused))
}

// paneHead is a pane's title chip (focus marker and click target) over a rule that is accent when focused.
func (m *model) paneHead(w int, id, title string, focused bool, right string) string {
	p := m.pal()
	chip := lipgloss.NewStyle().Underline(true).Foreground(p.muted.GetForeground()).Render(title)
	rule := p.muted.Render(strings.Repeat("─", w))
	if focused {
		chip = p.onAccent.Render(" " + title + " ")
		rule = p.accent.Render(strings.Repeat("━", w))
	}
	return spread(w-1, " "+mark(id, chip), right) + "\n" + rule
}

// tint paints a focused pane a shade off the terminal's own background, so focus reads in any theme.
func (m *model) tint(block string, w int, on bool) string {
	lines := strings.Split(block, "\n")
	for i, l := range lines {
		if pad := w - lipgloss.Width(l); pad > 0 {
			l += strings.Repeat(" ", pad)
		}
		lines[i] = l
	}
	if !on || m.bg == nil {
		return strings.Join(lines, "\n")
	}
	shade := lipgloss.Lighten(m.bg, 0.06)
	if !m.dark {
		shade = lipgloss.Darken(m.bg, 0.035)
	}
	r, g, b, _ := shade.RGBA()
	bg := fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r>>8, g>>8, b>>8)
	for i, l := range lines {
		for _, reset := range []string{"\x1b[m", "\x1b[0m", "\x1b[49m"} {
			l = strings.ReplaceAll(l, reset, reset+bg)
		}
		lines[i] = bg + l + "\x1b[49m"
	}
	return strings.Join(lines, "\n")
}

func (m *model) viewOptions(w int) string {
	p := m.pal()
	q := m.req.Questions[m.cur]
	st := m.qs[m.cur]
	focused := m.focus == fOptions
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Bold(true).Render(wrap(q.Question, w)) + "\n")
	if q.MultiSelect {
		b.WriteString(wrap("Select all that apply.", w) + "\n")
	}
	b.WriteString("\n")
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
		head := p.muted.Render(fmt.Sprintf("%d", i+1)) + " " + glyph + " " + lbl
		if rec {
			head += "   " + p.ok.Render("Recommended")
		}
		block := []string{mark(fmt.Sprintf("opt-%d-%d#0", m.cur, i), head)}
		if desc != "" {
			for j, l := range strings.Split(wrap(desc, w-6), "\n") {
				block = append(block, mark(fmt.Sprintf("opt-%d-%d#%d", m.cur, i, j+1), "    "+p.muted.Render(l)))
			}
		}
		if i == st.cursor && preview != "" {
			pv := strings.Split(m.markdown(preview, w-6), "\n")
			capLines := max(6, min(14, m.h/4))
			if len(pv) > capLines && !st.showMore {
				pv = append(pv[:capLines], link("showmore", fmt.Sprintf("Show %d more lines", len(pv)-capLines)))
			} else if len(pv) > capLines {
				pv = append(pv, link("showmore", "Show less"))
			}
			block = append(block, "")
			for _, l := range pv {
				block = append(block, "    "+l)
			}
		}
		if i == n && on {
			block = append(block, markLines("other-box", indent(m.boxed(&m.otherIn, m.focus == fOther, w-6), 4)))
		}
		b.WriteString(m.row(strings.Join(block, "\n"), i == st.cursor, focused) + "\n\n")
	}
	b.WriteString(m.noteRow(w, st.cursor == n+1, focused))
	return b.String()
}

// row marks the option under the cursor: an accent bar when the options have focus, muted when parked.
func (m *model) row(text string, cursor, focused bool) string {
	p := m.pal()
	switch {
	case cursor && focused:
		return bar(p.accent, "▌", text)
	case cursor:
		return bar(p.muted, "▌", text)
	}
	return indent(text, 2)
}

const (
	icNote  = "\U000F03EB" // nf-md-pencil
	icBot   = "\U000F06A9" // nf-md-robot
	icDone  = "\U000F012C" // nf-md-check
	icClear = "\U000F0156" // nf-md-close
	icBack  = "\U000F054C" // nf-md-undo
	icCopy  = "\U000F018F" // nf-md-content_copy
	icSend  = "\U000F048A" // nf-md-send
)

// noteRow is the note for the agent as one more row of the list, so arrows and clicks reach it.
func (m *model) noteRow(w int, cursor, focused bool) string {
	p := m.pal()
	st := m.qs[m.cur]
	if m.editNote == m.cur && m.focus == fNote {
		head := p.accent.Render(icNote) + " " + lipgloss.NewStyle().Bold(true).Render("Note for the agent")
		box := markLines("note-box", m.boxed(&m.notes, true, w-2))
		acts := link("note-clear", icClear+" Clear") + "    " + mark("note-done", p.accent.Bold(true).Render(icDone+" Done"))
		return bar(p.accent, "▌", head+"\n"+box+"\n"+spread(w-2, "", acts)) + "\n"
	}
	if st.note == "" {
		head := mark("note-row#0", p.muted.Render(icNote)+" "+lipgloss.NewStyle().Underline(true).Render("Add a note for the agent")+p.muted.Render("   /"))
		return m.row(head, cursor, focused) + "\n"
	}
	head := p.muted.Render(icNote) + " " + lipgloss.NewStyle().Bold(true).Render("Note for the agent")
	if st.noteByAgent {
		head += "  " + p.muted.Render(icBot+" drafted")
	}
	head += "   " + p.muted.Render(link("note-row#1", "Edit"))
	body := p.muted.Render("│") + " " + strings.ReplaceAll(wrap(st.note, w-6), "\n", "\n"+p.muted.Render("│")+" ")
	return m.row(mark("note-row#0", head)+"\n"+indent(body, 2), cursor, focused) + "\n"
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
	head := "\n" + p.muted.Render("The assistant has read access to your agent's conversation.")
	if !m.wide() {
		head = "\n" + spread(w, p.muted.Render("The assistant can read your agent's conversation."), link("model", m.model+" ⇄"))
	}
	head += "\n"
	var b strings.Builder
	if len(st.thread) == 0 {
		b.WriteString(p.muted.Render(wrap("Ask what the question means, what each option implies, or what the assistant would pick. Or start with:", w-2)) + "\n\n")
		for i, q := range starters {
			b.WriteString(mark(fmt.Sprintf("starter-%d", i), p.accent.Render("› ")+lipgloss.NewStyle().Underline(true).Render(q)) + "\n")
		}
	}
	for i, c := range st.thread {
		if c.You {
			b.WriteString(bar(p.muted, "│", lipgloss.NewStyle().Bold(true).Render("You")+"\n"+wrap(c.Text, w-3)))
		} else {
			name := p.accent.Bold(true).Render("Assistant")
			switch {
			case c.Live:
				name += "  " + p.accent.Render(m.spin.View()) + " " + p.muted.Italic(true).Render(c.Status)
			case c.Failed:
				name += "  " + p.bad.Render("✗ failed")
			case c.Copied:
				name += "  " + p.ok.Render("✓ copied")
			default:
				name += "  " + p.muted.Render(link(fmt.Sprintf("copy-%d", i), icCopy+" Copy"))
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
	footer := input + "\n" + spread(w, "", mark("send-chat", p.accent.Bold(true).Render(icSend+" Send")))
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
	w := min(m.w-8, 96)
	var b strings.Builder
	b.WriteString("\n" + m.stepper() + "\n\n")
	n := m.answeredCount()
	count := p.ok.Render(fmt.Sprintf("✓ %d of %d answered", n, len(m.qs)))
	if n < len(m.qs) {
		count = p.warn.Render(fmt.Sprintf("▲ %d of %d answered", n, len(m.qs)))
	}
	b.WriteString(spread(w, lipgloss.NewStyle().Bold(true).Render("Review your answers"), count) + "\n")
	b.WriteString(p.muted.Render(wrap("Notes are drafted from your discussions. Edit anything before you send.", w)) + "\n")
	for i := range m.qs {
		st := m.qs[i]
		b.WriteString("\n\n")
		acts := p.muted.Render(link(fmt.Sprintf("sendback-%d", i), icBack+" Send back")) + "   " + link(fmt.Sprintf("change-%d", i), "Change")
		b.WriteString(spread(w, p.muted.Render(fmt.Sprintf("%d  ", i+1))+lipgloss.NewStyle().Bold(true).Render(m.header(i)), acts) + "\n")
		ans := m.answerText(i)
		if ans == "" {
			b.WriteString(indent(p.warn.Render("▲ not answered yet"), 3) + "\n")
		} else {
			g := p.accent.Render(choiceGlyph(m.req.Questions[i].MultiSelect, true)) + " "
			b.WriteString(indent(g+strings.ReplaceAll(wrap(ans, w-6), "\n", "\n  "), 3) + "\n")
		}
		switch {
		case m.editNote == i && m.focus == fNote:
			head := p.accent.Render(icNote) + " " + lipgloss.NewStyle().Bold(true).Render("Note for the agent")
			box := markLines("note-box", m.boxed(&m.notes, true, w-3))
			acts := link("note-rewrite", icBot+" Rewrite") + "    " + link("note-clear", icClear+" Clear") + "    " + mark("note-done", p.accent.Bold(true).Render(icDone+" Done"))
			b.WriteString("\n" + indent(head+"\n"+box+"\n"+spread(w-3, "", acts), 3) + "\n")
		case st.drafting:
			b.WriteString(indent(p.muted.Render(icNote+" ")+p.accent.Render(m.spin.View())+p.muted.Italic(true).Render(" drafting a note from your discussion…"), 3) + "\n")
		case st.note != "":
			head := p.muted.Render(icNote + " Note")
			if st.noteByAgent {
				head += "  " + p.muted.Render(icBot+" drafted")
			}
			head += "   " + p.muted.Render(link(fmt.Sprintf("edit-%d", i), "Edit"))
			body := p.muted.Render("│") + " " + strings.ReplaceAll(wrap(st.note, w-8), "\n", "\n"+p.muted.Render("│")+" ")
			b.WriteString("\n" + indent(head+"\n"+body, 3) + "\n")
		default:
			b.WriteString(indent(p.muted.Render(link(fmt.Sprintf("edit-%d", i), icNote+" Add a note")), 3) + "\n")
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

var starters = []string{"Explain the options in plain words", "What would you pick, and why?", "What in the conversation led to this question?"}

func (m *model) anyLive() bool {
	for _, q := range m.qs {
		if n := len(q.thread); n > 0 && q.thread[n-1].Live {
			return true
		}
	}
	return false
}

func (m *model) drafting() bool {
	for _, q := range m.qs {
		if q.drafting {
			return true
		}
	}
	return false
}

// selection is a mouse drag over the screen, in cells; it is copied when the button is released.
type selection struct {
	ax, ay, hx, hy int
	minX, maxX     int // the pane the drag started in; a selection never crosses panes
	dragged        bool
	press          tea.MouseClickMsg
}

// span returns the selection's columns on line y, in reading order.
func (s *selection) span(y, width int) (int, int, bool) {
	sy, sx, ey, ex := s.ay, s.ax, s.hy, s.hx
	if ey < sy || (ey == sy && ex < sx) {
		sy, sx, ey, ex = ey, ex, sy, sx
	}
	if y < sy || y > ey {
		return 0, 0, false
	}
	from, to := s.minX, min(width, s.maxX)
	if y == sy {
		from = sx
	}
	if y == ey {
		to = ex + 1
	}
	from, to = max(from, s.minX), min(to, s.maxX)
	return from, to, from < to
}

func (m *model) highlight(content string) string {
	if m.sel == nil || !m.sel.dragged {
		return content
	}
	lines := strings.Split(content, "\n")
	rev := lipgloss.NewStyle().Reverse(true)
	for y, l := range lines {
		a, b, ok := m.sel.span(y, m.w)
		if !ok {
			continue
		}
		lines[y] = ansi.Truncate(l, a, "") + rev.Render(ansi.Strip(ansi.Cut(l, a, b))) + ansi.TruncateLeft(l, b, "")
	}
	return strings.Join(lines, "\n")
}

func (m *model) selectedText() string {
	var out []string
	for y, l := range m.frame {
		if a, b, ok := m.sel.span(y, m.w); ok {
			line := strings.TrimSpace(ansi.Strip(ansi.Cut(l, a, b)))
			for _, deco := range []string{"▌", "▎", "│"} { // speaker and cursor bars are decoration
				line = strings.TrimSpace(strings.TrimPrefix(line, deco))
			}
			out = append(out, line)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
