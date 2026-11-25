# Huddle GUI (stage 1)

Native macOS front end for huddle. The Go TUI is untouched.

    ./gui/build.sh    # builds gui/Huddle.app: Huddle (the app) + huddle-hook (Foundation-only, starts in ~4 ms)

Use it: point the PreToolUse hook for AskUserQuestion at `gui/Huddle.app/Contents/MacOS/huddle-hook`,
then `echo gui > ~/.huddle/ui` (or `HUDDLE_UI=gui`). Any other value delegates to the Go hook
(`~/.local/bin/huddle hook`, override with `HUDDLE_TUI_BIN`).

Flow: hook -> `~/.huddle/gui.sock` (one JSON line, connection held open) -> inbox window -> one JSON
line back -> hook prints the decision. App not running: the hook starts it (`open -g`) and waits 2 s.
Socket drops (Quit, crash, agent Esc): the call falls back to the built-in dialog / shows Cancelled.

Stage 1: inbox, questions, previews (plain text), notes, review, send back, timers, menu bar.
Later: side-agent chat + model/effort pickers (stage 2); resize/zoom, pasted images, Markdown (stage 3).
No SwiftUI `@State` (the Command Line Tools lack the macro plugin): state lives in ObservableObjects.
