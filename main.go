// Command huddle answers Claude Code's AskUserQuestion in a Rex pane, with a side agent to discuss each question.
//
//	huddle hook        PreToolUse hook: opens a pane beside the asking session, returns the answers
//	huddle ask <dir>   the pane itself: reads <dir>/request.json, writes <dir>/response.json
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

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
	default:
		fmt.Fprintln(os.Stderr, "usage: huddle hook | huddle ask <dir>")
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
	_ = exec.Command("afplay", "/System/Library/Sounds/Glass.aiff").Start() // a quiet chime: the pane opens without focus
	zone.NewGlobal()
	reapStale()
	m := newModel(&req, dir)
	prog = tea.NewProgram(m)
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	go func() {
		<-hup
		m.stopAgent()
		os.Exit(1)
	}()
	_, err = prog.Run()
	m.stopAgent()
	return err
}
