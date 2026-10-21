### Fixed

- Two parallel `edit` calls through two names of one existing file (a hard link, or a case alias on a case-insensitive filesystem such as the macOS default) no longer lose one of the edits. The built-in file mutation queue made them run at once, so both read the original text and the later write dropped the earlier edit while both calls reported success; it now orders them in call order. Pi does not order hard-link aliases (D190). <!-- fix: b1cea06458 -->
