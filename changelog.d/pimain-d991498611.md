### Fixed

- Display math (`$$...$$`, `\[...\]`) wider than the Markdown content width renders as its raw LaTeX source, which wraps like text, instead of wrapping each row of the 2D layout on its own and splitting fractions and sums apart. Layouts that fit render as before. Ports Pi's `latexBlock` width check. <!-- fix: d991498611 -->
