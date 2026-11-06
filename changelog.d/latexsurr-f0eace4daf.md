### Fixed

- LaTeX math renders two surrogate halves that end up side by side in one argument (`\vec{\ud83d{}\ude00}`, `x^{\ud83d{}\ude00}`, `\not{...}`, `\frac{x}{...}`) as the one character they spell, so the script, fraction, accent and `\not` layout matches Pi instead of treating them as two. <!-- fix: f0eace4daf -->
- LaTeX math reads an unbraced astral argument (`\vec😀`, `\frac😀x`, `\sqrt😀`, `\not😀`, `x^😀` in display mode) as Pi does, one UTF-16 unit: the high half is the argument and the low half is the next character. <!-- fix: f0eace4daf -->
