### Fixed

- LaTeX math that holds a lone UTF-16 surrogate (`$x\ud83d$`, a script, fraction, root, accent, `\text{}`, matrix or cases, inline or display) keeps the unit as one character of its own instead of rendering three replacement characters. <!-- fix: 2a0b22db2e -->
