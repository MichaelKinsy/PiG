### Fixed

- A packed Go, Python or Rust cell no longer hangs when a later member's factory calls the host before an earlier member's factory has run. The generated runner started every member on a thread of its own and let them race for the factory turn; the host accepts the members in order and serves a connection only after the members before it, so a later member whose factory subscribed to the event bus held the turn and waited for a host that waited for the earlier member. The runner now starts each member's factory in the cell's order. <!-- fix: d7c7f86a12 -->
