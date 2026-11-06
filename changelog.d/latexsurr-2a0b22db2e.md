### Fixed

- LaTeX math that holds a lone UTF-16 surrogate (`$x\ud83d$`, a script, fraction, root, accent, `\text{}`, matrix or cases, inline or display) now renders as Pi does: the unit is one character of its own instead of three replacement characters. <!-- fix: 2a0b22db2e -->
