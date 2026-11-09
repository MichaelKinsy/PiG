### Fixed

- Aborting a compaction or a session-tree move while extensions were handling `session_before_compact` or `session_before_tree` no longer drops the handler's reply and keeps every later extension from hearing about it. As in Pi, each handler now receives the event in order, `event.signal` aborts for the one that is running, and a handler dispatched after the abort starts with the signal already aborted. A `{ cancel: true }`, summary, instructions or label returned after a tree move was aborted is honoured. <!-- fix: 05b3416ad99 -->
