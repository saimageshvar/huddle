// Command huddle answers Claude Code's AskUserQuestion in a Rex pane, with a side agent to discuss each question.
//
//	huddle hook        PreToolUse hook: opens a pane beside the asking session, returns the answers
//	huddle ask <dir>   the pane itself: reads <dir>/request.json, writes <dir>/response.json
//	huddle history     recently answered questions
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"
)

func main() {
	args := os.Args[1:]
	switch {
	case len(args) == 1 && args[0] == "hook":
		runHook()
	case len(args) == 2 && args[0] == "ask":
		if err := runAsk(args[1]); err != nil {
			fmt.Fprintln(os.Stderr, "huddle:", err)
			os.Exit(1)
		}
	case len(args) == 1 && args[0] == "history":
		printHistory()
	default:
		fmt.Fprintln(os.Stderr, "usage: huddle hook | huddle ask <dir> | huddle history")
		os.Exit(2)
	}
}

func runAsk(dir string) error {
	b, err := os.ReadFile(filepath.Join(dir, "request.json"))
	if err != nil {
		return err
	}
	var req Request
	if err := json.Unmarshal(b, &req); err != nil {
		return err
	}
	if len(req.Questions) == 0 {
		return fmt.Errorf("no questions in %s", dir)
	}
	zone.NewGlobal()
	m := newModel(&req, dir)
	_, err = tea.NewProgram(m).Run()
	m.stopAgent()
	return err
}

type historyEntry struct {
	Time    time.Time `json:"time"`
	Project string    `json:"project"`
	Header  string    `json:"header"`
	Answer  string    `json:"answer"`
}

func recordHistory(req *Request, resp Response, m *model) {
	f, err := os.OpenFile(filepath.Join(huddleHome(), "history.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for i := range req.Questions {
		answer := m.answerText(i)
		if resp.Kind == "reframe" && i == m.cur {
			answer = "sent back with a reason"
		}
		_ = enc.Encode(historyEntry{time.Now(), filepath.Base(req.Cwd), m.header(i), answer})
	}
}

func printHistory() {
	b, _ := os.ReadFile(filepath.Join(huddleHome(), "history.jsonl"))
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) > 30 {
		lines = lines[len(lines)-30:]
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	for i := len(lines) - 1; i >= 0; i-- {
		var e historyEntry
		if json.Unmarshal([]byte(lines[i]), &e) == nil {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", e.Project, e.Header, e.Answer, e.Time.Format("Jan 2 15:04"))
		}
	}
	tw.Flush()
}
