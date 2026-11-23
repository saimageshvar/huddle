# UI toolkit available to huddle

Versions: bubbletea v2.1.2, bubbles v2.2.2, lipgloss v2.1.1, glamour v2.0.1 (checked 2026-11-23).

## Bubble Tea v2 (`tea.View` fields and messages)

| Feature | What it gives huddle |
|---|---|
| `MouseModeAllMotion` | Hover events: highlight the option, link or button under the pointer |
| `ReportFocus` → `FocusMsg` / `BlurMsg` | Know when the pane is focused: dim when blurred, chime only when unseen |
| `Cursor` (real terminal cursor, shape, colour, blink) | A proper blinking bar cursor in text boxes instead of a drawn block |
| `ProgramStatus` (OSC 7501) | Rex shows "blocked: question" on the pane/tab (in use) |
| `ProgressBar` (OSC 9;4) | Terminal-native progress, e.g. the hook countdown in the tab |
| `WindowTitle` | Pane title (in use) |
| `KeyboardEnhancements` | Shift+Enter vs Enter (verified in Rex) |
| `SetClipboard` (OSC 52) | Copy without pbcopy |
| `RequestBackgroundColor` → `BackgroundColorMsg` | The terminal's real background colour, for light/dark and derived tints |

## Lip Gloss v2

| Feature | What it gives huddle |
|---|---|
| `Compositor` + `Layer` (x, y, z, id) with `Hit(x, y)` | Overlays at any z-order (modal Send-back dialog, help popover, toasts) and native hit testing, which can replace bubblezone |
| Borders: Rounded, Normal, Thick, Double, Block, Inner/OuterHalfBlock, Hidden | Card styles; half-block borders make soft "pill" or card edges |
| `Blend1D/2D`, `Lighten`, `Darken`, `Alpha`, `Complementary` | Tints derived from the terminal's own background, so a focused panel can be a shade lighter in any theme |
| `lipgloss/list` (Bullet, Dash, Arabic, Roman, custom enumerators) | Option lists with custom markers |
| `lipgloss/table` | Review and time-up summaries with proper columns |
| `lipgloss/tree` | Not obviously needed |
| `Place`, `JoinHorizontal/Vertical`, `Wrap`, `StyleRanges` | Layout, centring, highlighting matched text |

## Bubbles v2

| Component | Use in huddle |
|---|---|
| `textarea` | Chat, notes, reason, Other (in use; dynamic height, selection, real cursor) |
| `viewport` | Scrollable thread and option column with mouse wheel, highlights, `EnsureVisible` |
| `help` + `key` | Short/full key hints that truncate to the width; the "?" overlay |
| `spinner` (Line, Dot, MiniDot, Jump, Pulse, Points, Meter, Ellipsis…) | "Assistant is thinking" indicator |
| `paginator` (dots) | A compact question pager: ● ○ ○ |
| `progress` | A drawn countdown bar (gradient or solid) |
| `timer` / `stopwatch` | Countdown ticking |
| `list` | Filterable list; overkill for 2-4 options |
| `table`, `textinput`, `filepicker`, `tree`, `cursor` | Not needed now |

## Glamour v2
Markdown for previews and replies; styles from `styles.DarkStyleConfig` / `LightStyleConfig`, tunable per element.
