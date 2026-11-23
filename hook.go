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
	block := askingPane()
	if block == "" {
		return
	}
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
	// rex prints the new block id on stdout; keep it out of the hook's output.
	cmd := exec.Command("rex", "run", "-b", block, "--split=right", "--ratio", "60", "--focus=false",
		"--wait", "--shell=none", "--", exe, "ask", dir)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	_ = cmd.Run()

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
		_ = json.Unmarshal(in.ToolInput, &toolInput)
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

// askingPane is the Rex block of the Claude that asked. Inside hc, REX_BLOCK may be stale.
func askingPane() string {
	if os.Getenv("HC_TERMINAL_ID") != "" {
		out, err := exec.Command("hc-rex", "env").Output()
		if err != nil {
			return ""
		}
		for _, f := range strings.Fields(string(out)) {
			if v, ok := strings.CutPrefix(f, "REX_BLOCK="); ok {
				return strings.Trim(v, `'"`)
			}
		}
		return ""
	}
	return os.Getenv("REX_BLOCK")
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
