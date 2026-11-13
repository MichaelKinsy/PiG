### Fixed

- A Python or Lisp `session_before_compact` or `session_before_tree` handler dispatched after the operation was aborted can now make host calls, such as a notification on its first line, as Pi's handler can. The handler still starts with its signal aborted. <!-- fix: d93f8bccf09, f8180c331b0 -->
