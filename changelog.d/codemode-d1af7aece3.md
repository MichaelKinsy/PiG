### Fixed

- The crash message of a deterministic code mode VM that is told a result number it cannot take now reads as Pi's does, `Error: Result 2 has sequence number 5, expected 1 or later, once`, and names `undefined` for a result that carries no number, where it read `Result 2 has sequence number 0, ...`. <!-- fix: d1af7aece304c704c75b1bc32e8690dbeedf0d8f -->
- The errors that name a code mode tool or global (`Tool "x" is already registered`, `Invalid global name "x"`, `Global "x" is already registered`, `Global "x" conflicts with the namespace "x"`) write the name as it was given, as Pi does, instead of Go-escaped (`"we\"ird"`). <!-- fix: d1af7aece304c704c75b1bc32e8690dbeedf0d8f -->
- A code mode `done` message that carries no store writes now fails the execution with Pi's `store writes is not valid JSON` instead of `unknown message from the worker`. <!-- fix: d1af7aece304c704c75b1bc32e8690dbeedf0d8f -->
