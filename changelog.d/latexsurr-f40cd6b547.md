### Fixed

- LaTeX math treats two surrogate halves that end up side by side in a fraction, a root or next to a named operator (`\sqrt{\ud835{}\udc9c}`, `\frac{x}{\ud835{}\udfce}`, `\ud835{}\udc9c\sin x`) as the one astral letter or digit they spell, so the parentheses and the spacing match Pi. <!-- fix: f40cd6b547 -->
