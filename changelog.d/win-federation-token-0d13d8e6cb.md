### Fixed

- Windows: a missing Anthropic identity token file reports Pi's message, "Failed to read identity token file at <path>: Error: ENOENT: no such file or directory, open '<path>'", without Windows' own wording ("The system cannot find the file specified.") appended after it. <!-- fix: 0d13d8e6cb778532821d841a6a48a51311315228 -->
