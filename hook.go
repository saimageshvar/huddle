package main

import (
	"bufio"

	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// hookTimeout must match "timeout" in the hook's settings entry.
const hookTimeout = 3600 * time.Second

type Option struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Preview     string `json:"preview,omitempty"`
}

type Question struct {
	Question    string   `json:"question"`
	Header      string   `json:"header,omitempty"`
	MultiSelect bool     `json:"multiSelect,omitempty"`
	Options     []Option `json:"options"`
}

// Request is what the hook hands to the huddle pane.
type Request struct {
	SessionID      string     `json:"session_id"`
	Cwd            string     `json:"cwd"`
	TranscriptPath string     `json:"transcript_path"`
	Title          string     `json:"title"`
	Branch         string     `json:"branch"`
	Questions      []Question `json:"questions"`
	Deadline       int64      `json:"deadline"`
	HookPID        int        `json:"hook_pid"`
}

// Response is what the huddle pane hands back. Kind is "answer" or "reframe".
type Response struct {
	Kind        string                       `json:"kind"`
	Answers     map[string]string            `json:"answers,omitempty"`
	Annotations map[string]map[string]string `json:"annotations,omitempty"`
	Reason      string                       `json:"reason,omitempty"`
}

func huddleHome() string { return filepath.Join(os.Getenv("HOME"), ".huddle") }

// runHook is the PreToolUse hook. Printing nothing leaves the built-in dialog in charge.
func runHook() {
	raw, _ := io.ReadAll(os.Stdin)
	var in struct {
		SessionID      string          `json:"session_id"`
		Cwd            string          `json:"cwd"`
		TranscriptPath string          `json:"transcript_path"`
		ToolName       string          `json:"tool_name"`
		ToolInput      json.RawMessage `json:"tool_input"`
	}
	if json.Unmarshal(raw, &in) != nil || in.ToolName != "AskUserQuestion" || stepAside(in.SessionID) {
		return
	}
	var input struct {
		Questions []Question `json:"questions"`
	}
	if json.Unmarshal(in.ToolInput, &input) != nil || len(input.Questions) == 0 {
		return
	}
	gui := os.Getenv("HUDDLE_GUI")
	session, block := askingPane()
	if block == "" && gui == "" {
		return
	}
	pruneCalls()
	dir := filepath.Join(huddleHome(), "calls", fmt.Sprintf("%s-%.8s", time.Now().Format("20060102-150405"), in.SessionID))
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	req := Request{
		SessionID: in.SessionID, Cwd: in.Cwd, TranscriptPath: in.TranscriptPath,
		Title: sessionTitle(in.TranscriptPath), Branch: gitBranch(in.Cwd),
		Questions: input.Questions,
		Deadline:  time.Now().Add(hookTimeout - 20*time.Second).Unix(),
		HookPID:   os.Getpid(),
	}
	if writeJSON(filepath.Join(dir, "request.json"), req) != nil {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if gui != "" {
		_ = exec.Command(gui, dir).Run()
		finish(dir, in.ToolInput)
		return
	}
	// The pane is created beside the asking session (so it lands in that tab), then floated over
	// the tab like a modal. rex prints the new block id first; it stays out of the hook's output.
	cmd := exec.Command(rexBin(), "run", "-b", block, "--split=right", "--ratio", "60", "--focus=false",
		"--wait", "--shell=none", "--", exe, "ask", dir)
	stdout, err := cmd.StdoutPipe()
	cmd.Stderr = io.Discard
	if err != nil || cmd.Start() != nil {
		return
	}
	sc := bufio.NewScanner(stdout)
	if sc.Scan() && session != "" {
		if id := strings.TrimSpace(sc.Text()); strings.HasPrefix(id, "block:") {
			float(session, id)
		}
	}
	_, _ = io.Copy(io.Discard, stdout)
	_ = cmd.Wait()
	finish(dir, in.ToolInput)
}

// finish turns the pane's (or GUI's) response.json into the hook's decision on stdout.
func finish(dir string, rawInput json.RawMessage) {
	var resp Response
	b, err := os.ReadFile(filepath.Join(dir, "response.json"))
	if err != nil || json.Unmarshal(b, &resp) != nil {
		return // pane closed without an answer
	}
	out := map[string]any{"hookEventName": "PreToolUse"}
	switch resp.Kind {
	case "reframe":
		out["permissionDecision"] = "deny"
		out["permissionDecisionReason"] = "The user sent this question back instead of answering it:\n\n" + resp.Reason
	case "answer":
		var toolInput map[string]any
		_ = json.Unmarshal(rawInput, &toolInput)
		toolInput["answers"] = resp.Answers
		if len(resp.Annotations) > 0 {
			toolInput["annotations"] = resp.Annotations
		}
		out["permissionDecision"] = "allow"
		out["updatedInput"] = toolInput
	default:
		return
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"hookSpecificOutput": out})
}

// stepAside reports whether away mode or hap full mode owns questions for this session.
func stepAside(sid string) bool {
	home := os.Getenv("HOME")
	away := filepath.Join(home, ".claude/away/state")
	if exists(filepath.Join(away, "active.json")) || (sid != "" && exists(filepath.Join(away, "sessions", sid+".json"))) {
		return true
	}
	hap := filepath.Join(home, ".local/state/hap")
	var mode struct {
		Mode string `json:"mode"`
		Dry  bool   `json:"dry"`
	}
	b, err := os.ReadFile(filepath.Join(hap, "sessions", sid+".json"))
	if err != nil {
		b, err = os.ReadFile(filepath.Join(hap, "global.json"))
	}
	return err == nil && json.Unmarshal(b, &mode) == nil && mode.Mode == "full" && !mode.Dry
}

// askingPane is the Rex session and block of the Claude that asked. Inside hc, REX_BLOCK may be stale.
func askingPane() (session, block string) {
	if os.Getenv("HC_TERMINAL_ID") != "" {
		out, err := exec.Command("hc-rex", "env").Output()
		if err != nil {
			return "", ""
		}
		for _, f := range strings.Fields(string(out)) {
			f = strings.TrimPrefix(f, "export ")
			if v, ok := strings.CutPrefix(f, "REX_BLOCK="); ok {
				block = strings.Trim(v, `'"`)
			}
			if v, ok := strings.CutPrefix(f, "REX_SESSION="); ok {
				session = strings.Trim(v, `'"`)
			}
		}
		return session, block
	}
	return os.Getenv("REX_SESSION"), os.Getenv("REX_BLOCK")
}

// float lifts the huddle pane into a floating layer over its tab; if Rex refuses, it stays a split.
func float(session, block string) {
	args, _ := json.Marshal(map[string]any{"session_id": session, "block_id": block, "bounds": []float64{0.04, 0.04, 0.92, 0.92}})
	_ = exec.Command(rexBin(), "api", "call", "session.float_block", string(args)).Run()
}

func sessionTitle(transcript string) string {
	f, err := os.Open(transcript)
	if err != nil {
		return ""
	}
	defer f.Close()
	title := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if !strings.Contains(string(line), "-title") {
			continue
		}
		var e struct{ Type, CustomTitle, AiTitle string }
		if json.Unmarshal(line, &e) == nil {
			if e.Type == "custom-title" && e.CustomTitle != "" {
				title = e.CustomTitle
			} else if e.Type == "ai-title" && e.AiTitle != "" {
				title = e.AiTitle
			}
		}
	}
	return title
}

func gitBranch(dir string) string {
	out, _ := exec.Command("git", "-C", dir, "branch", "--show-current").Output()
	return strings.TrimSpace(string(out))
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// rexBin finds the rex CLI. Sessions run under hc (herdr-core) don't have it on PATH, so fall
// back to where Rex installs it, as hc-rex does.
func rexBin() string {
	if p, err := exec.LookPath("rex"); err == nil {
		return p
	}
	p := filepath.Join(os.Getenv("HOME"), "Library/Application Support/com.superlogical.rex/RexServer/Versions/Current/Rex Server.app/Contents/MacOS/rex")
	if exists(p) {
		return p
	}
	return "rex"
}

// pruneCalls drops call directories untouched for a week; each holds one question's request, response and images.
func pruneCalls() {
	root := filepath.Join(huddleHome(), "calls")
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > 7*24*time.Hour {
			os.RemoveAll(filepath.Join(root, e.Name()))
		}
	}
}
