# Huddle

A native macOS window for Claude Code's `AskUserQuestion` prompts. When an agent stops to ask you something, Huddle opens an inbox window with the options, previews, a note per question and a side chat with an assistant that has read the session. Answer in any order, send a question back, or let the timer lapse and the agent falls back to its built-in dialog.

## Install

Needs macOS and the Command Line Tools (`xcode-select --install`). Huddle is built on your Mac, so there is no signing or Gatekeeper prompt.

    curl -fsSL https://raw.githubusercontent.com/saimageshvar/huddle/master/install.sh | sh

or with Homebrew:

    brew install saimageshvar/huddle/huddle-gui
    huddle-gui setup

Both end with `huddle-gui setup`, which registers the `AskUserQuestion` hook in `~/.claude/settings.json` (a backup is kept as `settings.json.huddle-bak`). Restart running Claude Code sessions so they pick it up.

The curl installer puts `Huddle.app` in `~/Applications`. The side chat uses the `claude` CLI, so Claude Code must be installed.

## Commands

    huddle-gui setup       register the hook and link ~/.huddle/gui-hook
    huddle-gui status      show what is registered
    huddle-gui uninstall   remove the hook and links (then delete Huddle.app)

`HUDDLE_UI=tui` in the environment, or `tui` in `~/.huddle/ui`, hands a question to the terminal TUI instead (the TUI is separate and not installed by these routes).

## Build from source

    ./gui/build.sh        # gui/Huddle.app

See `gui/README.md` for the architecture (hook helper, unix socket, resident menu bar app).

## Brew tap

The formula lives in `packaging/homebrew/huddle-gui.rb`. Copy it to `Formula/huddle-gui.rb` in a repo named `homebrew-huddle`.

## Licence

MIT
