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
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone/v2"
)

type screen int

const (
	scrQuestion screen = iota
	scrReview
	scrReframe
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
	Images []string
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
	visited     bool   // left once, so an empty answer reads as skipped
	flagged     bool   // ticked in the Send back card as a wrong question
	reason      string // why it is wrong
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
	reasonQ    int // the question whose reason the reason box is editing, -1 none
	sel        *selection
	selDone    bool
	frame      []string // last rendered lines, for copying a selection
	img        *kitty
	toSend     []string // image data waiting to go to the terminal
	bigImage   string   // the image opened large, "" when none
	bodyH      int      // rows between header and footer in the current frame
	padX, padY int      // outer margin around everything
	under      screen   // the screen an overlay is drawn over
	showKeys   bool
	flashAt    time.Time
	modalFrom  int // clickables before this index sit under an open overlay
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
	m := &model{req: req, dir: dir, dark: true, img: newKitty(), editNote: -1, spin: spinner.New(spinner.WithSpinner(spinner.MiniDot)), model: "sonnet", reqs: map[int]*pending{}, md: map[string]string{}, inclSum: true}
	m.qs = make([]qstate, len(req.Questions))
	for i := range m.qs {
		m.qs[i].picked = map[int]bool{}
		for oi, o := range req.Questions[i].Options { // start on Claude's pick
			if _, rec := stripRecommended(o.Label); rec {
				m.qs[i].cursor = oi
				break
			}
		}
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
	t.SetStyles(inputStyles(true))
	t.SetVirtualCursor(false) // the terminal's own blinking cursor shows where typing goes
	return t
}

// Init asks Rex for its colours and subscribes to its light/dark switches (DEC mode 2031),
// so huddle always paints with the theme Rex is showing right now.
func (m *model) Init() tea.Cmd {
	return tea.Batch(tick(), tea.RequestBackgroundColor, tea.Raw(ansi.SetModeLightDark+ansi.RequestLightDarkReport))
}

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
		// an outer margin so nothing presses against the pane's edges
		m.padX, m.padY = 2, 1
		if msg.Width < 90 {
			m.padX = 1
		}
		m.w, m.h = msg.Width-2*m.padX, msg.Height-2*m.padY
		return m, nil
	case spinner.TickMsg:
		if !m.anyLive() && !m.drafting() {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case uv.DarkColorSchemeEvent, uv.LightColorSchemeEvent:
		return m, tea.RequestBackgroundColor // the theme changed: fetch its real background
	case tea.BackgroundColorMsg:
		m.dark, m.bg = msg.IsDark(), msg.Color
		for _, t := range []*textarea.Model{&m.chat, &m.notes, &m.otherIn, &m.reason} {
			t.SetStyles(inputStyles(m.dark))
		}
		m.md = map[string]string{}
		return m, nil
	case flushImages:
		if len(m.toSend) == 0 {
			return m, nil
		}
		out := strings.Join(m.toSend, "")
		m.toSend = nil
		return m, tea.Raw(out)
	case tickMsg:
		if m.flash != "" && time.Since(m.flashAt) > 4*time.Second {
			m.flash = ""
		}
		if m.timeLeft() <= 0 || !alive(m.req.HookPID) { // the asking session stopped waiting: just close
			m.stopAgent()
			return m, tea.Quit
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
			m.sel = &selection{ax: mo.X, ay: mo.Y, hx: mo.X, hy: mo.Y, press: msg, minX: m.padX, maxX: m.padX + m.w}
			if m.scr == scrQuestion && m.wide() {
				if mo.X-m.padX >= m.leftWidth() {
					m.sel.minX = m.padX + m.leftWidth() + 1
				} else {
					m.sel.maxX = m.padX + m.leftWidth()
				}
			}
			m.selDone = false
		}
		return m, nil
	case tea.MouseMotionMsg:
		if mo := msg.Mouse(); mo.Button == tea.MouseNone {
			hoverID = m.hit(mo.X, mo.Y)
			return m, nil
		}
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
			m.flashAt, m.flash = time.Now(), fmt.Sprintf("Copied %d characters · Cmd+V pastes into the note or chat", len([]rune(text)))
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
	if m.showKeys || m.bigImage != "" {
		m.showKeys, m.bigImage = false, ""
		return nil
	}
	if s == "?" && m.input() == nil {
		m.showKeys = true
		return nil
	}
	if in := m.input(); in != nil {
		switch s {
		case "esc":
			if m.focus == fReason {
				m.scr = m.under
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
	if oi == len(opts) && q.picked[oi] {
		q.cursor = oi
		m.otherIn.SetValue(q.other)
		return m.setFocus(fOther)
	}
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
		imgs := imagesIn(text)
		q.thread = append(q.thread, chatMsg{You: true, Text: text, Images: imgs}, chatMsg{Live: true, Status: "Starting…"})
		m.threadOff = 0
		return tea.Batch(m.ask(m.cur, "chat", fmt.Sprintf("[Q%d: %s] %s", m.cur+1, m.header(m.cur), text), imgs...), m.spin.Tick)
	case fOther:
		if !m.req.Questions[m.cur].MultiSelect && strings.TrimSpace(m.otherIn.Value()) != "" {
			return tea.Batch(m.setFocus(fOptions), m.next())
		}
		return m.setFocus(fOptions)
	case fNote:
		return m.setFocus(fOptions) // leaving the note saves it
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
func (m *model) ask(q int, kind, text string, images ...string) tea.Cmd {
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
	if err := m.agent.Send(id, text, images...); err != nil {
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
			switch {
			case out == "NONE":
				m.flashAt, m.flash = time.Now(), fmt.Sprintf("Nothing in the %s discussion is worth passing on as a note.", m.header(p.q))
			case !q.noteEdited:
				q.note, q.noteByAgent = out, true
			}
		case "reason":
			q.drafting = false
			if out != "NONE" {
				q.reason = out
				if p.q == m.reasonQ {
					m.reason.SetValue(out)
				}
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
	m.qs[m.cur].visited = true
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
		m.scr = m.under
		return m.setFocus(fOptions)
	case scrQuestion:
		if m.cur > 0 {
			return m.goTo(m.cur - 1)
		}
	}
	return nil
}

func (m *model) review() tea.Cmd {
	m.qs[m.cur].visited = true
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
	if m.expired {
		return nil
	}
	if m.drafting() && !m.sendAnyway {
		m.sendAnyway = true
		m.flashAt, m.flash = time.Now(), "Notes are still drafting. Send again to send without them."
		return nil
	}
	resp := Response{Kind: "answer", Answers: map[string]string{}, Annotations: map[string]map[string]string{}}
	for i, q := range m.req.Questions {
		resp.Answers[q.Question] = or(m.answerText(i), skipped)
		if n := strings.TrimSpace(m.qs[i].note); n != "" {
			resp.Annotations[q.Question] = map[string]string{"notes": n}
		}
	}
	return m.finish(resp)
}

func (m *model) sendBack() tea.Cmd {
	m.stashReason()
	var b strings.Builder
	var kept []string
	for i, q := range m.req.Questions {
		st := m.qs[i]
		switch {
		case st.flagged && strings.TrimSpace(st.reason) != "":
			fmt.Fprintf(&b, "Question: %s\nWhat is wrong with it: %s\n", q.Question, strings.TrimSpace(st.reason))
			if n := strings.TrimSpace(st.note); m.inclSum && n != "" {
				fmt.Fprintf(&b, "Summary of the user's discussion: %s\n", n)
			}
			b.WriteString("\n")
		case m.answered(i):
			kept = append(kept, fmt.Sprintf("- %s → %s", q.Question, m.answerText(i)))
		}
	}
	if b.Len() == 0 {
		m.flashAt, m.flash = time.Now(), "Tick a question and say what is wrong with it."
		return nil
	}
	if len(kept) > 0 {
		b.WriteString("The user's answers to the other questions, to keep when you ask again:\n" + strings.Join(kept, "\n") + "\n")
	}
	return m.finish(Response{Kind: "reframe", Reason: b.String()})
}

// openSendBack shows the Send back card; from a question screen that question starts ticked.
func (m *model) openSendBack() tea.Cmd {
	m.under, m.scr = m.scr, scrReframe
	m.reasonQ = -1
	if m.under == scrQuestion {
		m.qs[m.cur].flagged = true
	}
	for i := range m.qs {
		if m.qs[i].flagged {
			return m.editReason(i)
		}
	}
	return m.setFocus(fOptions)
}

// stashReason saves the reason box into the question it is editing.
func (m *model) stashReason() {
	if m.reasonQ >= 0 {
		m.qs[m.reasonQ].reason = m.reason.Value()
	}
}

func (m *model) editReason(i int) tea.Cmd {
	m.stashReason()
	m.reasonQ = i
	m.reason.SetValue(m.qs[i].reason)
	return m.setFocus(fReason)
}

func (m *model) toggleFlag(i int) tea.Cmd {
	m.stashReason()
	m.qs[i].flagged = !m.qs[i].flagged
	if m.qs[i].flagged {
		return m.editReason(i)
	}
	if m.reasonQ == i {
		m.reasonQ = -1
		for j := range m.qs {
			if m.qs[j].flagged {
				return m.editReason(j)
			}
		}
		return m.setFocus(fOptions)
	}
	return nil
}

func (m *model) finish(resp Response) tea.Cmd {
	if err := writeJSON(filepath.Join(m.dir, "response.json"), resp); err != nil {
		m.flashAt, m.flash = time.Now(), "Could not save the answer: "+err.Error()
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
		m.flashAt, m.flash = time.Now(), "Wait for the assistant to finish before switching models."
		return nil
	}
	sid := m.agent.sessionID
	m.agent.Stop()
	a, err := startAgent(m.req, next, sid)
	if err != nil {
		m.flashAt, m.flash = time.Now(), "Could not switch models: "+err.Error()
		m.agent = nil
		return nil
	}
	m.agent, m.model = a, next
	return waitAgent(a)
}

func (m *model) finishNote() {
	if m.editNote < 0 {
		return // no note open: never guess which question a note belongs to
	}
	q := &m.qs[m.editNote]
	v := strings.TrimSpace(m.notes.Value())
	if v != q.note {
		q.note, q.noteEdited, q.noteByAgent = v, true, false
	}
	m.editNote = -1
}

func (m *model) startNote(i int) tea.Cmd {
	if m.focus == fNote {
		m.setFocus(fOptions) // saves the open note into its own question first
	}
	m.editNote = i
	m.notes.SetValue(m.qs[i].note)
	return m.setFocus(fNote)
}

func copyText(s string) { c := exec.Command("pbcopy"); c.Stdin = strings.NewReader(s); _ = c.Run() }
func or(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// ---------- mouse ----------

// hit returns the clickable id under a cell, ignoring anything beneath an open overlay.
func (m *model) hit(x, y int) string {
	for i := len(clickables) - 1; i >= m.modalFrom; i-- {
		if z := zone.Get(clickables[i]); z != nil && !z.IsZero() && x >= z.StartX && x <= z.EndX && y >= z.StartY && y <= z.EndY {
			return clickables[i]
		}
	}
	return ""
}

func (m *model) click(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Mouse().Button != tea.MouseLeft {
		return nil
	}
	if id := m.hit(msg.Mouse().X, msg.Mouse().Y); id != "" {
		return m.action(id)
	}
	if m.modalFrom > 0 {
		return nil // a click outside an overlay does nothing
	}
	if m.scr == scrQuestion && (m.askTab || (m.wide() && msg.Mouse().X-m.padX >= m.leftWidth())) {
		return m.setFocus(fChat)
	}
	if m.scr == scrQuestion && m.focus != fOptions {
		return m.setFocus(fOptions)
	}
	return nil
}

func (m *model) action(id string) tea.Cmd {
	if path, ok := strings.CutPrefix(id, "img:"); ok {
		m.bigImage, _, _ = strings.Cut(path, "#")
		return nil
	}
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
	case sscan(id, "flag-%d", &a):
		return m.toggleFlag(a)
	case sscan(id, "why-%d", &a):
		return m.editReason(a)
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
		return m.setFocus(fOptions)
	case "note-clear":
		m.notes.SetValue("")
	case "note-rewrite":
		i := m.editNote
		if i < 0 {
			i = m.cur
		}
		m.setFocus(fOptions)
		if len(m.qs[i].thread) == 0 {
			m.flashAt, m.flash = time.Now(), "Nothing to draft from yet: chat about this question in Ask about it, then Rewrite."
			return nil
		}
		m.qs[i].noteEdited, m.qs[i].draftedAt = false, -1
		return tea.Batch(m.draftNote(i), m.spin.Tick)
	case "showmore":
		m.qs[m.cur].showMore = !m.qs[m.cur].showMore
	case "model":
		return m.switchModel()
	case "submit":
		return m.submit()
	case "sendback":
		return m.openSendBack()
	case "reason-box":
		return m.setFocus(fReason)
	case "reason-draft":
		if q := m.reasonQ; q >= 0 && len(m.qs[q].thread) > 0 && !m.qs[q].drafting {
			m.qs[q].drafting = true
			return m.ask(q, "reason", fmt.Sprintf("[DRAFT REASON Q%d] Write why question %d should be asked differently.", q+1, q+1))
		}
	case "reason-incl":
		m.inclSum = !m.inclSum
	case "reason-send":
		return m.sendBack()
	case "reason-cancel":
		m.scr = m.under
		return m.setFocus(fOptions)
	case "close-image":
		m.bigImage = ""
	case "keys":
		m.showKeys = !m.showKeys
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
	boxes := map[string]*textarea.Model{"note-box": &m.notes, "other-box": &m.otherIn, "chat-box": &m.chat, "reason-box": &m.reason}
	id, _, _ := strings.Cut(m.hit(mouse.X, mouse.Y), "#")
	if t := boxes[id]; t != nil { // the pointer is over a text box: scroll its text
		for range max(d, -d) {
			if d < 0 {
				t.CursorUp()
			} else {
				t.CursorDown()
			}
		}
		return
	}
	inThread := m.askTab || (m.wide() && mouse.X-m.padX >= m.leftWidth())
	if m.scr == scrQuestion && inThread {
		m.threadOff = max(0, m.threadOff-d)
	} else {
		m.leftOff = max(0, m.leftOff+d)
	}
}

// choiceGlyph draws circle (single choice) and square (multiple choice) markers; chosen ones are filled.
func choiceGlyph(multi, on bool) string {
	switch {
	case multi && on:
		return "\U000F0132" // checkbox-marked: a filled square with a tick
	case multi:
		return "\U000F0131" // checkbox-blank-outline: an empty square
	case on:
		return "\U000F012F" // checkbox-blank-circle: a filled circle
	}
	return "\U000F0130" // checkbox-blank-circle-outline: an empty circle
}

func (m *model) wide() bool { return m.w >= 120 }
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
	accent := st("4", "12")
	// reverse video: the accent becomes the fill and the label takes the terminal's background,
	// which reads in any theme (Rex's light "bright white" is grey, so a fixed white would not)
	onAccent := lipgloss.NewStyle().Bold(true).Reverse(true).Foreground(ld(lipgloss.Color("4"), lipgloss.Color("12")))
	linkColor = accent.GetForeground()
	return palette{
		accent:   accent,
		muted:    st("8", "245"),
		ok:       st("2", "10"),
		warn:     st("3", "11"),
		bad:      st("1", "9"),
		text:     lipgloss.NewStyle(),
		onAccent: onAccent,
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

func mark(id, s string) string { clickables = append(clickables, id); return zone.Mark(id, s) }

// markLines marks each line on its own, so clipping a tall block never leaves a zone unclosed.
func markLines(id, block string) string {
	lines := strings.Split(block, "\n")
	for i, l := range lines {
		lines[i] = mark(fmt.Sprintf("%s#%d", id, i), l)
	}
	return strings.Join(lines, "\n")
}

// hoverID is the clickable under the mouse pointer, set from motion events.
var hoverID string

func hovered(id string) bool {
	base, _, _ := strings.Cut(id, "#")
	h, _, _ := strings.Cut(hoverID, "#")
	return h != "" && h == base
}

// link is accent text at rest and underlined under the pointer, so the screen stays quiet.
func link(id, s string) string {
	st := lipgloss.NewStyle().Foreground(linkColor)
	if hovered(id) {
		st = st.Underline(true)
	}
	return mark(id, st.Render(s))
}

var linkColor color.Color = lipgloss.Color("4")

func (m *model) button(id, s string) string {
	return mark(id, m.pal().onAccent.Render(" "+s+" "))
}

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
	cfg.CodeBlock.Chroma, cfg.CodeBlock.Theme = nil, ""
	// inline code: the palette's accent colour, no band, so it suits light and dark alike
	cfg.Code.BackgroundColor = nil
	cfg.Code.Prefix, cfg.Code.Suffix = "", ""
	accent := "12"
	if !m.dark {
		accent = "4"
	}
	cfg.Code.Color = &accent
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
	out := append([]string(nil), lines[off:end]...)
	for len(out) < h {
		out = append(out, "")
	}
	if below := len(lines) - end; below > 0 && h > 2 {
		out[h-1] = lipgloss.NewStyle().Faint(true).Render(fmt.Sprintf("   ↓ %d more lines below · scroll", below))
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
	header := m.viewHeader()
	footer := m.viewFooter()
	m.bodyH = m.h - lipgloss.Height(header) - lipgloss.Height(footer)
	var body string
	switch m.scr {
	case scrQuestion:
		body = m.viewQuestion()
	case scrReview:
		body = m.viewReview()
	case scrReframe:
		if m.under == scrReview {
			body = m.viewReview()
		} else {
			body = m.viewQuestion()
		}
	}
	body, _ = clip(body, 0, m.bodyH)
	page := lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
	m.modalFrom = 0
	switch {
	case m.scr == scrReframe:
		m.modalFrom = len(clickables)
		page = m.overlay(page, m.viewReframe())
	case m.showKeys:
		m.modalFrom = len(clickables)
		page = m.overlay(page, m.viewKeys())
	case m.bigImage != "":
		m.modalFrom = len(clickables)
		page = m.overlay(page, m.viewImage())
	}
	if m.flash != "" {
		page = m.toast(page, m.flash)
	}
	content := zone.Scan(m.pad(page))
	content, v.Cursor = m.placeCursor(content)
	m.frame = strings.Split(content, "\n")
	v.SetContent(m.highlight(content))
	v.MouseMode = tea.MouseModeAllMotion
	if left := m.timeLeft(); !m.expired {
		state := tea.ProgressBarDefault
		if left < 5*time.Minute {
			state = tea.ProgressBarWarning
		}
		v.ProgressBar = &tea.ProgressBar{State: state, Value: max(1, int(100*left/hookTimeout))}
	}
	return v
}

func (m *model) rule() string { return m.pal().muted.Render(strings.Repeat("─", m.w)) }

func (m *model) viewHeader() string {
	p := m.pal()
	left := " " + p.accent.Bold(true).Render("huddle")
	if repo := filepath.Base(m.req.Cwd); repo != "huddle" {
		left += "  " + repo
	}
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
	if m.scr == scrQuestion || m.scr == scrReview {
		sb, keys := icBack+" Send back…", "? Keys"
		if !m.wide() {
			sb, keys = icBack+" Send back", "?"
		}
		right = p.muted.Render(link("sendback", sb)) + "   " + p.muted.Render(link("keys", keys)) + "   " + right
	}
	lines := []string{spread(m.w-1, left, right)}
	if !m.wide() && m.scr == scrQuestion {
		chip := func(title string, on bool) string {
			if on {
				return p.onAccent.Render(" " + title + " ")
			}
			return lipgloss.NewStyle().Foreground(p.muted.GetForeground()).Render(title)
		}
		qt, at := chip("Answer", !m.askTab), chip("Ask about it", m.askTab)
		if n := m.qs[m.cur].unread; n > 0 {
			at += p.accent.Render(fmt.Sprintf(" · %d new", n))
		}
		lines = append(lines, spread(m.w-1, " "+mark("tab-q", qt)+"  "+mark("tab-ask", at), m.stepper(true)))
	} else if m.scr == scrQuestion || m.scr == scrReview || m.scr == scrReframe {
		lines = append(lines, " "+m.stepper(!m.wide()))
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
		label := fmt.Sprintf("Send %d answers ✓", len(m.qs))
		if skip := len(m.qs) - m.answeredCount(); skip > 0 {
			label = fmt.Sprintf("Send (%d skipped) →", skip)
		}
		right = append(right, footItem{"submit", label, "Send ✓", 1})
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
	left := m.keyBar(m.w - lipgloss.Width(r) - 8)
	return m.rule() + "\n" + spread(m.w-1, "  "+left, r+" ")
}

type keyHint struct{ id, key, desc string }

// keyBar names where keystrokes go right now; each hint with an id is also clickable.
func (m *model) keyBar(w int) string {
	p := m.pal()
	var hints []keyHint
	switch {
	case m.scr == scrReview:
		hints = []keyHint{{"", "⏎", "send"}}
	case m.scr != scrQuestion:
		hints = []keyHint{{"", "⏎", "send"}, {"", "esc", "back"}}
	case m.focus == fChat:
		hints = []keyHint{{"send-chat", "⏎", "send"}, {"", "⇧⏎", "newline"}, {"", "pgup/dn", "scroll"}, {"kb-esc", "esc", "back"}}
	case m.focus == fNote:
		hints = []keyHint{{"note-done", "⏎", "done"}, {"", "⇧⏎", "newline"}, {"kb-esc", "esc", "back"}}
	case m.focus == fOther:
		hints = []keyHint{{"", "⏎", "done"}, {"kb-esc", "esc", "back"}}
	case m.req.Questions[m.cur].MultiSelect:
		hints = []keyHint{{"", "↑↓", "move"}, {"", "space", "toggle"}, {"next", "⏎", "next"}, {"kb-tab", "tab", "chat"}, {"kb-note", "/", "note"}}
	default:
		hints = []keyHint{{"", "↑↓", "move"}, {"", "⏎", "choose"}, {"", fmt.Sprintf("1-%d", len(m.req.Questions[m.cur].Options)+1), "pick"}, {"kb-tab", "tab", "chat"}, {"kb-note", "/", "note"}}
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

// stepper draws the questions as tabs: the current one filled, ✓ answered, – skipped, ○ not yet.
// compact shows numbers only, for narrow panes.
func (m *model) stepper(compact bool) string {
	p := m.pal()
	var parts []string
	for i := 0; i <= len(m.qs); i++ {
		label, state := "Review", ""
		if i < len(m.qs) {
			label, state = m.header(i), "○"
			switch {
			case m.answered(i):
				state = "✓"
			case m.qs[i].visited:
				state = "–"
			}
			if compact {
				label = fmt.Sprint(i + 1)
			}
		} else if compact {
			label = "✓"
		}
		text := label
		if state != "" && !compact {
			text = state + " " + label
		}
		active := (m.scr == scrReview && i == len(m.qs)) || (m.scr != scrReview && i == m.cur)
		var t string
		switch {
		case active:
			t = p.onAccent.Render(" " + text + " ")
		case state == "✓" && compact:
			t = p.ok.Render(" " + text + " ")
		case state == "✓":
			t = " " + p.ok.Render("✓") + lipgloss.NewStyle().Render(" "+label) + " "
		default:
			t = p.muted.Render(" " + text + " ")
		}
		parts = append(parts, mark(fmt.Sprintf("step-%d", i), t))
	}
	return strings.Join(parts, " ")
}

func (m *model) viewQuestion() string {
	h := m.bodyH
	if !m.wide() {
		if m.askTab {
			return indent(m.viewThread(m.w-4, h), 2)
		}
		out, off := clip(indent(m.viewOptions(m.w-6), 3), m.leftOff, h)
		m.leftOff = off
		return out
	}
	lw, rw := m.leftWidth(), m.w-m.leftWidth()-1
	leftFocused := m.focus != fChat
	lhead := m.paneHead(lw, "tab-q", "Answer", leftFocused, "")
	l, off := clip(indent(m.viewOptions(lw-6), 3), m.leftOff, h-2)
	m.leftOff = off
	rhead := m.paneHead(rw, "tab-ask", "Ask about it", !leftFocused, link("model", "model: "+m.model+" ⇄"))
	r := indent(m.viewThread(rw-4, h-2), 2)
	if n := lipgloss.Height(r); n < h-2 { // an empty chat is short; pad so the pane's shading fills it
		r += strings.Repeat("\n", h-2-n)
	}
	sep := m.pal().muted.Render(strings.TrimRight(strings.Repeat("│\n", h), "\n"))
	return lipgloss.JoinHorizontal(lipgloss.Top,
		m.tint(lhead+"\n"+l, lw, leftFocused), sep, m.tint(rhead+"\n"+r, rw, !leftFocused))
}

// paneHead is a pane's title chip (focus marker and click target) over a rule that is accent when focused.
func (m *model) paneHead(w int, id, title string, focused bool, right string) string {
	p := m.pal()
	chip := lipgloss.NewStyle().Underline(hovered(id)).Foreground(p.muted.GetForeground()).Render(title)
	rule := p.muted.Render(strings.Repeat("─", w))
	if focused {
		chip = p.onAccent.Render(" " + title + " ")
		rule = p.accent.Render(strings.Repeat("─", w))
	}
	return spread(w-1, " "+mark(id, chip), right) + "\n" + rule
}

// tint shades a pane off the terminal's own background so focus reads in any theme.
// The focused pane always reads brighter: a dark theme lifts the focused pane, a light theme
// greys the unfocused one.
func (m *model) tint(block string, w int, focused bool) string {
	on := focused == m.dark
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
		if on || i == st.cursor {
			glyph = p.accent.Render(glyph)
		}
		lst := lipgloss.NewStyle()
		if i == st.cursor || on {
			lst = lst.Bold(true)
		}
		if on {
			lst = lst.Foreground(p.accent.GetForeground())
		}
		if hovered(fmt.Sprintf("opt-%d-%d", m.cur, i)) {
			lst = lst.Underline(true)
		}
		lbl := lst.Render(label)
		head := p.muted.Render(fmt.Sprintf("%d", i+1)) + " " + glyph + " " + lbl
		if rec {
			head += "   " + p.ok.Render("Recommended")
		}
		block := []string{mark(fmt.Sprintf("opt-%d-%d#0", m.cur, i), head)}
		if desc != "" {
			for j, l := range strings.Split(wrap(desc, w-6), "\n") {
				block = append(block, mark(fmt.Sprintf("opt-%d-%d#%d", m.cur, i, j+1), "    "+l))
			}
		}
		text := m.row(strings.Join(block, "\n"), i == st.cursor, focused)
		if i == st.cursor && preview != "" {
			pv := strings.Split(m.markdown(preview, w-8), "\n")
			capLines := max(4, min(10, m.h/5))
			if len(pv) > capLines && !st.showMore {
				pv = append(pv[:capLines], link("showmore", fmt.Sprintf("Show full preview (%d more lines)", len(pv)-capLines)))
			} else if len(pv) > capLines {
				pv = append(pv, link("showmore", "Show less"))
			}
			text += "\n" + indent(strings.Join(pv, "\n"), 6)
		}
		if i == n && on {
			hint := "click to type · ⏎ when on this row"
			if m.focus == fOther {
				hint = "⏎ done · esc back to options"
			}
			text += "\n" + markLines("other-box", indent(m.boxed(&m.otherIn, m.focus == fOther, w-6), 6)) + indent(m.thumbsFor(m.otherIn.Value(), w-8), 6) + "\n" + indent(p.muted.Render(hint), 7)
		}
		b.WriteString(text + "\n\n")
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
		box := markLines("note-box", m.boxed(&m.notes, true, w-2)) + m.thumbsFor(m.notes.Value(), w-4)
		acts := link("note-clear", icClear+" Clear") + "    " + mark("note-done", p.accent.Bold(true).Render(icDone+" Done"))
		if len(st.thread) > 0 {
			acts = link("note-rewrite", icBot+" Rewrite with assistant") + "    " + acts
		}
		return bar(p.accent, "▌", head+"\n"+box+"\n"+spread(w-2, "", acts)) + "\n"
	}
	if st.note == "" {
		head := mark("note-row#0", p.muted.Render(icNote)+" "+lipgloss.NewStyle().Foreground(linkColor).Underline(hovered("note-row")).Render("Add a note for the agent")+p.muted.Render("   (/)"))
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
	view := t.View()
	if focused && t.Focused() {
		view = cursorMark + view
	}
	ld := lipgloss.LightDark(m.dark)
	c := ld(lipgloss.Color("8"), lipgloss.Color("240"))
	if focused {
		c = m.pal().accent.GetForeground()
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(c).Padding(0, 1).Render(view)
}

func (m *model) viewThread(w, h int) string {
	p := m.pal()
	st := &m.qs[m.cur]
	head := "\n" + p.muted.Render(wrap("The assistant has read access to your agent's conversation.", w))
	if !m.wide() {
		head = "\n" + spread(w, p.muted.Render("The assistant can read your agent's conversation."), link("model", m.model+" ⇄"))
	}
	head += "\n"
	var b strings.Builder
	if len(st.thread) == 0 {
		b.WriteString(p.muted.Render(wrap("Ask what the question means, what each option implies, or what the assistant would pick. Or start with:", w-2)) + "\n\n")
		for i, q := range starters {
			b.WriteString(mark(fmt.Sprintf("starter-%d", i), p.accent.Render("› ")+lipgloss.NewStyle().Foreground(linkColor).Underline(hovered(fmt.Sprintf("starter-%d", i))).Render(q)) + "\n")
		}
	}
	for i, c := range st.thread {
		if c.You {
			you := lipgloss.NewStyle().Bold(true).Render("You") + "\n" + wrap(c.Text, w-3)
			if len(c.Images) > 0 {
				you += "\n" + m.thumbs(c.Images, w-4, 10)
			}
			b.WriteString(bar(p.muted, "│", you))
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
	m.chat.Placeholder = "Ask a follow-up"
	if len(st.thread) == 0 {
		m.chat.Placeholder = "Ask about this question"
	}
	// a pasted image previews small here, so the chat pane keeps its height and its buttons
	input := markLines("chat-box", m.boxed(&m.chat, m.focus == fChat, w)) + m.thumbsFor(m.chat.Value(), w-2, 4)
	footer := input + "\n" + spread(w, "", mark("send-chat", p.accent.Bold(true).Render(icSend+" Send")))
	avail := max(2, h-lipgloss.Height(head)-lipgloss.Height(footer)-1)
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if len(st.thread) == 0 {
		avail = min(avail, len(lines)+1)
	}
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
	b.WriteString("\n")
	n := m.answeredCount()
	count := p.ok.Bold(true).Render(fmt.Sprintf("✓ %d answered", n))
	if skip := len(m.qs) - n; skip > 0 {
		count = lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("%d answered · ", n)) + p.warn.Bold(true).Render(fmt.Sprintf("%d skipped", skip))
	}
	b.WriteString(spread(w, lipgloss.NewStyle().Bold(true).Render("Review your answers"), count) + "\n")
	b.WriteString(p.muted.Render(wrap("Notes were drafted from your chats. Edit anything before sending.", w)) + "\n")
	for i := range m.qs {
		st := m.qs[i]
		b.WriteString("\n\n")
		acts := link(fmt.Sprintf("change-%d", i), "Change")
		b.WriteString(spread(w, p.muted.Render(fmt.Sprintf("%d  ", i+1))+lipgloss.NewStyle().Bold(true).Render(m.header(i)), acts) + "\n")
		b.WriteString(indent(p.muted.Render(ansi.Truncate(m.req.Questions[i].Question, w-4, "…")), 3) + "\n")
		ans := m.answerText(i)
		if ans == "" {
			b.WriteString(indent(p.warn.Render("– Skipped. The agent will see this as unanswered."), 3) + "\n")
		} else {
			g := p.accent.Render(choiceGlyph(m.req.Questions[i].MultiSelect, true)) + " "
			b.WriteString(indent(g+strings.ReplaceAll(wrap(m.answerShown(i), w-6), "\n", "\n  "), 3) + "\n")
		}
		switch {
		case m.editNote == i && m.focus == fNote:
			head := p.accent.Render(icNote) + " " + lipgloss.NewStyle().Bold(true).Render("Note for the agent")
			box := markLines("note-box", m.boxed(&m.notes, true, w-3)) + m.thumbsFor(m.notes.Value(), w-5)
			acts := link("note-rewrite", icBot+" Rewrite with assistant") + "    " + link("note-clear", icClear+" Clear") + "    " + mark("note-done", p.accent.Bold(true).Render(icDone+" Done"))
			b.WriteString("\n" + indent(head+"\n"+box+"\n"+spread(w-3, "", acts), 3) + "\n")
		case st.drafting:
			b.WriteString(indent(p.muted.Render(icNote+" ")+p.accent.Render(m.spin.View())+p.muted.Italic(true).Render(" drafting a note from your discussion…"), 3) + "\n")
		case st.note != "":
			head := p.muted.Render(icNote + " Your note")
			if st.noteByAgent {
				head = p.muted.Render(icNote + " Drafted by the assistant " + icBot)
			}
			head += "   " + p.muted.Render(link(fmt.Sprintf("edit-%d", i), "Edit"))
			body := p.muted.Render("│") + " " + strings.ReplaceAll(wrap(st.note, w-8), "\n", "\n"+p.muted.Render("│")+" ")
			b.WriteString("\n" + indent(head+"\n"+body, 3) + "\n")
		default:
			b.WriteString(indent(link(fmt.Sprintf("edit-%d", i), "+ Add note"), 3) + "\n")
		}
	}
	send := fmt.Sprintf("Send %d answers ✓", len(m.qs))
	if skip := len(m.qs) - n; skip > 0 {
		send = fmt.Sprintf("Send (%d skipped) →", skip)
	}
	b.WriteString("\n\n" + spread(w, "", m.button("submit#flow", send)) + "\n")
	out, off := clip(m.center(b.String(), w), m.leftOff, m.bodyH)
	m.leftOff = off
	return out
}

func (m *model) viewReframe() string {
	p := m.pal()
	w := min(m.w-12, 80)
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Render(icBack+"  Send questions back") + "\n\n")
	b.WriteString(p.muted.Render(wrap("Tick the questions that are wrong and say why. The agent will ask again.", w)) + "\n\n")
	for i, q := range m.req.Questions {
		st := m.qs[i]
		box := "\U000F0131"
		if st.flagged {
			box = p.accent.Render("\U000F0132")
		}
		title := lipgloss.NewStyle().Bold(st.flagged).Render(m.header(i))
		b.WriteString(mark(fmt.Sprintf("flag-%d", i), box+" "+title) + p.muted.Render("  "+ansi.Truncate(q.Question, w-lipgloss.Width(m.header(i))-6, "…")) + "\n")
		switch {
		case st.flagged && i == m.reasonQ:
			b.WriteString(markLines("reason-box", indent(m.boxed(&m.reason, m.focus == fReason, w-2), 2)) + indent(m.thumbsFor(m.reason.Value(), w-4), 2) + "\n")
			draft := ""
			switch {
			case st.drafting:
				draft = p.accent.Render(m.spin.View()) + p.muted.Italic(true).Render(" drafting…")
			case len(st.thread) > 0:
				draft = link("reason-draft", icBot+" Draft from my discussion")
			}
			if draft != "" {
				b.WriteString(spread(w, "", draft) + "\n")
			}
		case st.flagged:
			why := or(strings.TrimSpace(st.reason), "Say what is wrong…")
			b.WriteString(indent(mark(fmt.Sprintf("why-%d", i), p.muted.Render("│ ")+wrap(why, w-6)), 2) + "\n")
		}
		b.WriteString("\n")
	}
	box := "\U000F0131"
	if m.inclSum {
		box = p.accent.Render("\U000F0132")
	}
	b.WriteString(mark("reason-incl", box+" Include my notes for the agent") + "\n")
	b.WriteString(p.muted.Render(wrap("Your answers to the other questions go back too, so the agent can keep them.", w)) + "\n\n")
	b.WriteString(spread(w, "", link("reason-cancel", "Cancel")+"    "+m.button("reason-send", "Send back")))
	return b.String()
}
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
		a, b, ok := m.sel.span(y, m.w+2*m.padX)
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
		if a, b, ok := m.sel.span(y, m.w+2*m.padX); ok {
			line := strings.TrimSpace(ansi.Strip(ansi.Cut(l, a, b)))
			for _, deco := range []string{"▌", "▎", "│"} { // speaker and cursor bars are decoration
				line = strings.TrimSpace(strings.TrimPrefix(line, deco))
			}
			out = append(out, line)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// cursorMark is a zero-width OSC that tags where the focused text box starts; placeCursor
// turns it into the terminal's real cursor and strips it.
const cursorMark = "\x1b]7777;huddle-cursor\x07"

func (m *model) placeCursor(content string) (string, *tea.Cursor) {
	lines := strings.Split(content, "\n")
	for y, l := range lines {
		i := strings.Index(l, cursorMark)
		if i < 0 {
			continue
		}
		lines[y] = l[:i] + l[i+len(cursorMark):]
		in := m.input()
		if in == nil {
			break
		}
		c := in.Cursor()
		if c == nil {
			break
		}
		c.X += ansi.StringWidth(l[:i])
		c.Y += y
		c.Shape, c.Blink, c.Color = tea.CursorBar, true, m.pal().accent.GetForeground()
		return strings.Join(lines, "\n"), c
	}
	return strings.Join(lines, "\n"), nil
}

// overlay draws a card centred over the page; what lies beneath stays visible around it.
func (m *model) overlay(page, card string) string {
	ld := lipgloss.LightDark(m.dark)
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(ld(lipgloss.Color("7"), lipgloss.Color("240"))).Padding(min(1, max(0, (m.h-lipgloss.Height(card)-4)/2)), 3).Render(card)
	faded := strings.Split(page, "\n") // the page recedes behind the card
	for i, l := range faded {
		faded[i] = m.pal().muted.Faint(true).Render(blankImages(ansi.Strip(l)))
	}
	page = strings.Join(faded, "\n")
	return splice(page, box, max(0, (m.w-lipgloss.Width(box))/2), max(1, (m.h-lipgloss.Height(box))/2))
}

// toast shows a short message in the top-right corner for a few seconds.
func (m *model) toast(page, msg string) string {
	ld := lipgloss.LightDark(m.dark)
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(ld(lipgloss.Color("3"), lipgloss.Color("11"))).Padding(0, 1).Render(wrap(msg, min(48, m.w-8)))
	return splice(page, box, max(0, m.w-lipgloss.Width(box)-2), 2)
}

// splice writes block over page at column x, row y.
func splice(page, block string, x, y int) string {
	lines := strings.Split(page, "\n")
	for i, bl := range strings.Split(block, "\n") {
		r := y + i
		if r >= len(lines) {
			break
		}
		l := lines[r]
		if w := lipgloss.Width(l); w < x {
			l += strings.Repeat(" ", x-w)
		}
		lines[r] = ansi.Truncate(l, x, "") + "\x1b[m" + bl + "\x1b[m" + ansi.TruncateLeft(l, x+lipgloss.Width(bl), "")
	}
	return strings.Join(lines, "\n")
}

func (m *model) viewKeys() string {
	p := m.pal()
	groups := []struct {
		name string
		rows [][2]string
	}{
		{"Moving", [][2]string{{"↑ ↓", "move between options"}, {"← →", "previous / next question"}, {"tab", "switch between Answer and Ask about it"}, {"esc", "back (keeps what you typed)"}, {"pgup pgdn", "scroll the discussion"}}},
		{"Answering", [][2]string{{"⏎ or a number", "choose and go to the next question"}, {"space", "toggle (pick-any questions)"}}},
		{"Text", [][2]string{{"any letter", "start a message to the assistant"}, {"/", "write a note for the agent"}, {"⏎ / ⇧⏎", "send / new line in a text box"}, {"drag", "select text; copied when you let go"}}},
	}
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Render("Keys") + p.muted.Render("   everything here is also clickable") + "\n")
	for _, g := range groups {
		b.WriteString("\n" + p.muted.Render(g.name) + "\n")
		for _, r := range g.rows {
			b.WriteString(lipgloss.NewStyle().Bold(true).Width(17).Render(r[0]) + r[1] + "\n")
		}
	}
	b.WriteString("\n" + link("keys", "Close"))
	return b.String()
}

const skipped = "(skipped: the user chose not to answer this question)"

var starters = []string{"Explain the options in plain words", "What would you pick, and why?", "What in the conversation led to this question?"}

// answerShown is the answer as the screen shows it: "(Recommended)" becomes the green tag.
// Claude still gets the raw label, which it matches exactly.
func (m *model) answerShown(i int) string {
	p := m.pal()
	q := m.qs[i]
	opts := m.req.Questions[i].Options
	var parts []string
	for oi := range opts {
		if q.picked[oi] {
			label, rec := stripRecommended(opts[oi].Label)
			if rec {
				label += " " + p.ok.Render("Recommended")
			}
			parts = append(parts, label)
		}
	}
	if q.picked[len(opts)] && strings.TrimSpace(q.other) != "" {
		parts = append(parts, strings.TrimSpace(q.other))
	}
	return strings.Join(parts, ", ")
}

type flushImages struct{}

// prog lets drawing ask the program to send image data the frame needs.
var prog *tea.Program

// thumbs draws images as clickable thumbnails, each at most maxCols x maxRows cells.
func (m *model) thumbs(paths []string, maxCols, maxRows int) string {
	p := m.pal()
	var out []string
	for _, path := range paths {
		cols, rows := m.img.size(path, min(maxCols, 60), maxRows)
		if cols == 0 {
			continue
		}
		send, block := m.img.place(path, cols, rows)
		if block == "" {
			continue
		}
		if send != "" {
			m.toSend = append(m.toSend, send)
			if prog != nil {
				go prog.Send(flushImages{})
			}
		}
		out = append(out, markLines("img:"+path, block)+"\n"+p.muted.Render(filepath.Base(path)+" · click to enlarge"))
	}
	return strings.Join(out, "\n")
}

// thumbsFor shows thumbnails of any images pasted into a text box, under it.
func (m *model) thumbsFor(text string, w int, rows ...int) string {
	maxRows := 8
	if len(rows) > 0 {
		maxRows = rows[0]
	}
	if paths := imagesIn(text); len(paths) > 0 {
		return "\n" + m.thumbs(paths, w, maxRows)
	}
	return ""
}

func (m *model) viewImage() string {
	p := m.pal()
	cols, rows := m.img.size(m.bigImage, m.w-14, m.h-10)
	send, block := m.img.place(m.bigImage, cols, rows)
	if send != "" {
		m.toSend = append(m.toSend, send)
		if prog != nil {
			go prog.Send(flushImages{})
		}
	}
	return block + "\n\n" + spread(cols, p.muted.Render(filepath.Base(m.bigImage)), link("close-image", "Close"))
}

// blankImages turns image placeholders into spaces, for text that has lost the colour naming the image.
func blankImages(s string) string {
	if !strings.ContainsRune(s, placeholder) {
		return s
	}
	marks := map[rune]bool{}
	for _, d := range diacritics {
		marks[d] = true
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r == placeholder:
			return ' '
		case marks[r]:
			return -1
		}
		return r
	}, s)
}

// pad surrounds the page with the outer margin.
func (m *model) pad(page string) string {
	side := strings.Repeat(" ", m.padX)
	lines := strings.Split(page, "\n")
	for i, l := range lines {
		lines[i] = side + l
	}
	blank := strings.Repeat("\n", m.padY)
	return blank + strings.Join(lines, "\n") + blank
}
