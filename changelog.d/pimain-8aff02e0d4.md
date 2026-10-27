### Fixed

- `grep` with `context` numbers a file's lines at line feeds only, as ripgrep does: a lone carriage return no longer adds a line, so the context lines after it keep their line numbers and the match is no longer shown on the wrong line. The carriage return is dropped from the printed line. <!-- fix: 8aff02e0d4 -->
