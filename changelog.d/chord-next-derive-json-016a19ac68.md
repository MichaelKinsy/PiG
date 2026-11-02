### Fixed

- A Chord derive that returns a value no JSON has (a channel, a function) now fails its delivery, which the host reports as `delivery_failed` while the later frames of the commit are still delivered, as Pi's batch does when diffing throws. The derive's watchers used to receive the non-JSON frame without any report. <!-- fix: 016a19ac68 -->
