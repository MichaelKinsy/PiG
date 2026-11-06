### Fixed

- Markdown text that holds a lone UTF-16 surrogate (a paragraph, heading, emphasis, code span, link, table cell, list item, quote or link reference definition) now measures and wraps as Pi does: the unit is one code point of no width instead of three replacement characters, and a reference definition whose label holds one still resolves. <!-- fix: 20bdd2c341 -->
