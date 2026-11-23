package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// agentMsg is one event from the side agent, addressed to the request that caused it.
type agentMsg struct {
	Req  int
	Kind string // "delta", "tool", "done", "error"
	Text string
}

// Agent is a long-lived headless Claude fed over stream-json, one per question call.
type Agent struct {
	Model     string
	sessionID string
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	events    chan agentMsg
	mu        sync.Mutex
	queue     []int // requests in flight, answered in order
}

func startAgent(req *Request, model, resume string) (*Agent, error) {
	args := []string{"-p", "--model", model, "--effort", "medium",
		"--input-format", "stream-json", "--output-format", "stream-json",
		"--verbose", "--include-partial-messages",
		"--tools", "Read,Grep,Glob", "--permission-mode", "bypassPermissions",
		"--append-system-prompt", agentPrompt(req)}
	if dir := filepath.Dir(req.TranscriptPath); req.TranscriptPath != "" {
		args = append(args, "--add-dir", dir)
	}
	if resume != "" {
		args = append(args, "--resume", resume)
	}
	cmd := exec.Command("claude", args...)
	cmd.Dir = req.Cwd
	cmd.Env = append(os.Environ(), "HUDDLE_SIDE_AGENT=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	a := &Agent{Model: model, sessionID: resume, cmd: cmd, stdin: stdin, events: make(chan agentMsg, 256)}
	go a.read(stdout)
	return a, nil
}

// Send queues one user message; images go along as real image blocks the assistant can see.
func (a *Agent) Send(req int, text string, images ...string) error {
	var content any = text
	if len(images) > 0 {
		blocks := []map[string]any{{"type": "text", "text": text}}
		for _, path := range images {
			if data, err := pngBytes(path); err == nil {
				blocks = append(blocks, map[string]any{"type": "image", "source": map[string]any{
					"type": "base64", "media_type": "image/png", "data": base64.StdEncoding.EncodeToString(data)}})
			}
		}
		content = blocks
	}
	b, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": content}})
	a.mu.Lock()
	a.queue = append(a.queue, req)
	a.mu.Unlock()
	_, err := a.stdin.Write(append(b, '\n'))
	return err
}

func (a *Agent) Busy() bool { a.mu.Lock(); defer a.mu.Unlock(); return len(a.queue) > 0 }

func (a *Agent) Stop() {
	a.stdin.Close()
	if a.cmd.Process != nil {
		a.cmd.Process.Kill()
	}
}

func (a *Agent) head() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.queue) == 0 {
		return -1
	}
	return a.queue[0]
}

func (a *Agent) read(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	textSeen := false
	for sc.Scan() {
		var e struct {
			Type      string `json:"type"`
			Subtype   string `json:"subtype"`
			SessionID string `json:"session_id"`
			IsError   bool   `json:"is_error"`
			Result    string `json:"result"`
			Event     struct {
				Type         string `json:"type"`
				ContentBlock struct {
					Type string `json:"type"`
				} `json:"content_block"`
				Delta struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"delta"`
			} `json:"event"`
			Message struct {
				Content []struct {
					Type  string         `json:"type"`
					Name  string         `json:"name"`
					Input map[string]any `json:"input"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		req := a.head()
		switch e.Type {
		case "system":
			if e.SessionID != "" {
				a.sessionID = e.SessionID
			}
		case "stream_event":
			if e.Event.Type == "content_block_start" && e.Event.ContentBlock.Type == "text" && textSeen {
				a.events <- agentMsg{req, "delta", "\n\n"}
			}
			if e.Event.Type == "content_block_delta" && e.Event.Delta.Type == "text_delta" {
				textSeen = true
				a.events <- agentMsg{req, "delta", e.Event.Delta.Text}
			}
		case "assistant":
			for _, c := range e.Message.Content {
				if c.Type == "tool_use" {
					a.events <- agentMsg{req, "tool", toolLabel(c.Name, c.Input)}
				}
			}
		case "result":
			a.mu.Lock()
			if len(a.queue) > 0 {
				a.queue = a.queue[1:]
			}
			a.mu.Unlock()
			textSeen = false
			if e.IsError {
				a.events <- agentMsg{req, "error", e.Result}
			} else {
				a.events <- agentMsg{req, "done", ""}
			}
		}
	}
	a.mu.Lock()
	pending := a.queue
	a.queue = nil
	a.mu.Unlock()
	for _, req := range pending {
		a.events <- agentMsg{req, "error", "The assistant stopped unexpectedly."}
	}
	close(a.events)
}

func toolLabel(name string, in map[string]any) string {
	for _, k := range []string{"file_path", "pattern", "path"} {
		if v, ok := in[k].(string); ok && v != "" {
			if strings.Contains(v, "/.claude/projects/") {
				v = "the main conversation"
			} else {
				v = filepath.Base(v)
			}
			verb := map[string]string{"Read": "Reading", "Grep": "Searching for", "Glob": "Looking for"}[name]
			if verb == "" {
				verb = name
			}
			return verb + " " + v
		}
	}
	return name
}

func agentPrompt(req *Request) string {
	qs, _ := json.MarshalIndent(req.Questions, "", "  ")
	return fmt.Sprintf(`You are huddle's side assistant. Another Claude Code agent (the main agent) paused its work to ask the user the questions below. You help the user understand and answer them. You never answer for the user and never contact the main agent.

Main agent's working directory: %s
Main agent's conversation transcript (JSONL, newest at the end): %s
Read the transcript only when a question needs context: grep or read its tail first. Never preload it. You are read-only: you may only read and search files.

The questions (JSON):
%s

Each message from the user starts with a tag such as [Q2: Header] naming the question it is about. Stay on that question unless the user widens it. Write in plain, direct words with short paragraphs; use Markdown (lists, tables, code) when it helps. Say what you recommend when asked, with the reason.

Two special requests:
- A message starting [DRAFT NOTE Qn] asks you to write the note that goes to the main agent with the user's answer to question n. Write 1-5 short lines, plain text, no preamble or heading, addressed to the main agent: the user's reasoning, decisions and constraints from your discussion of question n. If the discussion holds nothing worth passing on, reply exactly NONE.
- A message starting [DRAFT REASON Qn] asks you to write why question n itself is wrong or should be asked differently, from the discussion, addressed to the main agent in 1-5 lines, no preamble.`,
		req.Cwd, req.TranscriptPath, qs)
}
