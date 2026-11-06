### Fixed

- Accepting an autocomplete suggestion after the cursor moved off the typed text (a word move, Home or End) cuts the line where Pi does when the text before the cursor holds non-ASCII characters, instead of a few bytes away, where it could split a character. <!-- fix: 532a0099a8 -->
- Typing a space or a letter such as `é` right after a slash command whose arguments have completions no longer opens the argument popup unless Pi opens it. Pi opens a closed popup only for `/` at the start of the message, for a trigger character, and for a letter, digit, `.`, `-`, `_` or CJK character in a slash command or trigger context. <!-- fix: 24b67ccb05 -->
