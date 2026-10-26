### Changed

- Fullscreen mode writes far less to the terminal while an answer streams. When the transcript grows or scrolls, PiG now moves the rows that only moved with a terminal scroll region and draws only the rows that changed, where Pi draws every changed row (D205). A 20,000-character answer in a 160x48 tmux pane takes 544 KB instead of 3,578 KB, and tmux's CPU for it falls by about three quarters. The screen is the same. Set `PIG_TUI_SCROLL_ROWS=0` to draw every changed row, as Pi does.
