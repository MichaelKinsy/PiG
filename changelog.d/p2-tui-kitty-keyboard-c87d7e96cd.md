### Fixed

- p2's terminal client pushes the kitty keyboard flags on the alternate screen the runtime draws on (the main and alternate screens keep separate keyboard-mode stacks), so Alt+Enter in a kitty-protocol terminal sends a follow-up instead of inserting a newline; the flags are popped before the runtime leaves that screen, on suspend and on cleanup. <!-- fix: c87d7e96cd3d5f46542252434c21b3168fe44d43 -->
