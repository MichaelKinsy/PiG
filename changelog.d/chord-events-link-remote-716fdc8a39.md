### Added

- Chord: event messages over links and relays (one `event` message per flush per link and origin, a per-origin event window of 32 by default with dropped events counted as `chord.events.dropped{window}`, event acks apart from state acks), `TuiScope.On`, and call windows only for untrusted links: a trusted link announces no call window and its peer sends every call at once. Ports the link and remote layers of Pi 8d30e6306.
