### Known issues

- tui (Windows): in fullscreen mode PiG scrolls the rows that only moved inside a scroll region (D205) on every terminal whose environment sets `TERM`, which on Windows includes Git Bash (mintty) and a Windows Terminal tab that runs one; a terminal that sets no `TERM` gets Pi's bytes. Windows Terminal itself was not run with scroll regions. `PIG_TUI_SCROLL_ROWS=0` restores Pi's bytes.
