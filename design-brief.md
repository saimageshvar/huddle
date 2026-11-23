# Design brief: huddle, a terminal UI for answering an AI agent's questions

## The scenario

A developer runs Claude Code (an AI coding agent) in one terminal pane. Partway through a task, the
agent stops and asks the developer 1 to 4 multiple-choice questions at once (one "question call").
Today those questions appear in a cramped built-in dialog: the question text is terse, notes are a
single line, and there is no way to ask "what do you mean?" before answering.

**huddle** is a separate full-screen terminal app, in its own pane beside the agent, where those
questions land instead. For each question the developer can:

1. Read the question, its options, and each option's description and **preview** (a Markdown
   snippet: code, a diff, an ASCII mockup or a short explanation).
2. **Discuss the question with a side agent** in a chat thread. The side agent has read the main
   agent's conversation, so it can explain what the question really means, what each option
   implies and what it would recommend. Each question has its own thread; the same side agent
   serves all the questions in the call and knows about the other threads.
3. Pick an option (or several, for multi-select questions), or type a free-text answer ("Other").
4. Review **notes** that go back with the answer: the side agent drafts a 3-5 line summary of the
   discussion, and the developer edits it in a real multi-line editor.
5. **Submit** all answers at once. Or **Reframe**: reject the question with a written reason
   ("the question assumes X, which is wrong") so the main agent rethinks instead.

While huddle waits for an answer, the main agent is paused. That wait has a time limit (about 1
hour), so a quiet countdown matters.

## Who uses it

A senior developer who lives in the terminal, often runs several agents at once, and reads a lot
of text quickly. They want density and clarity, not decoration. They use the mouse as much as the
keyboard: **every action must be visible on screen and clickable**. Keyboard shortcuts speed
things up but never hide a feature.

## Screens and states to design

1. **Idle**: no question pending. Shows that huddle is listening, and optionally the last few
   answered questions (session, question, answer, time).
2. **Question call arrives**: identity of the asking session (project folder, git branch, session
   title, short session id) and the 1-4 questions as tabs or a list, each with a short header chip
   (e.g. "Auth method"), an answered/unanswered marker and a "discussed" marker.
3. **Question in focus**: full question text; options with label, description and the rendered
   Markdown preview of the highlighted option. One option may be labelled "(Recommended)".
   Single-select (radio) vs multi-select (checkbox) must look different. "Other" with free text is
   always the last choice.
4. **Discussion thread** for the focused question: the developer's messages and the side agent's
   replies (Markdown: headings, lists, code blocks, tables). States: agent starting up ("reading
   the conversation…"), reply streaming in token by token, agent idle, agent error or crashed (with
   retry). A multi-line chat input at the bottom, where the developer may paste extra context
   (logs, links, long text).
5. **Notes editor**: the drafted summary, editable, with "Regenerate summary" and
   "Clear". Shows that it was AI-drafted until the developer edits it.
6. **Review and submit**: all questions in the call with their chosen answer and notes; unanswered
   questions flagged; Submit and Reframe actions.
7. **Reframe**: a reason editor for one question, with send and cancel.
8. **Edge states**: countdown running low (under 5 minutes); the main agent gave up waiting
   (answers can no longer be sent, but the thread can be copied); a second question call arriving
   from another session while one is open (a queue indicator, not an interruption).

## Layout

- **Wide (144 columns and up)**: two columns. Left: question tabs, question, options, preview,
  notes. Right: the discussion thread and chat input. This is the primary layout.
- **Medium (100-143 columns)**: same content, tighter; the preview may collapse into the option
  list.
- **Narrow (80 columns, 24 rows minimum)**: a single column with a visible, clickable toggle
  between "Question" and "Discussion".
- Show where focus is at all times (which pane, which option, which input).

## Terminal and theme constraints (strict)

- It is a terminal: a monospace character grid, no images, no pixel sizes, no gradients, no
  custom fonts. Everything is text, Unicode box-drawing characters and terminal colours.
- **Follow the terminal's own theme, light or dark, whatever the system uses.** Never paint a full
  background: the terminal's background shows through everywhere. Colours come from the terminal's
  16-colour ANSI palette (or adaptive light/dark pairs), so the app looks native in any theme.
  Provide every mockup in **both a light and a dark terminal**.
- Meaning must never rely on colour alone: pair it with a symbol, label, weight or position
  (focused, selected, recommended, unanswered, error).
- Use bold, dim/faint, italic, underline and reverse video deliberately; they survive any theme.
- Borders: rounded, normal or thick box-drawing lines; one border style family across the app.
- Avoid emoji for meaning: widths differ between terminals. Simple Unicode marks (● ○ ◉ ☐ ☑ ▸ ✓ ✗
  ⏳) are fine.
- Built with Bubble Tea and Lip Gloss (Go), so anything expressible as styled text boxes,
  padding, borders and alignment is fair game. Markdown in previews and chat is rendered by
  Glamour.

## What to deliver

- Mockups of the states above at wide (160×45) and narrow (80×24) sizes, each in light and dark.
  Monospace text mockups are ideal (exact characters, with a colour legend); screenshots are fine.
- A small style sheet: the colour roles (accent, muted, success, warning, error, focus,
  user message, agent message) mapped to ANSI colours or light/dark pairs, the border style, and
  the symbols used.
- An interaction map: every action, its clickable target, and its keyboard shortcut.
