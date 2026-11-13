### Added

- Chord plugins (internal): the control channel between a Go host and its Node plugin host carries a link's `event` and `ack` messages in both directions, one message per flush, each acknowledged by count; the Go side writes them from a sender that never blocks a host's flush or the channel's reader and has ended when the process has. Nothing uses them yet: a type with an event member still fails at the bridge with the error that says events do not cross it.
