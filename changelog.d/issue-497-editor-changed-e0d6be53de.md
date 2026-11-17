### Fixed

- A custom editor installed by a Go, Rust, Python or Lisp extension no longer ends the interactive session with `autocomplete provider is no longer available` when the install ran before the autocomplete rebuild that follows `session_start` (and on a later `/reload`). The host reports each rebuild to the editor's runtime, and those SDKs answered the notice with an error that the host raised as an uncaught exception; they now accept it, as the Node runtime always did. <!-- fix: e0d6be53de -->
