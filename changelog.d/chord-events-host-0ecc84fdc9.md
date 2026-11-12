### Added

- Chord: event members (fire-and-forget) on the host: an object declares `self.Events()` sources, references subscribe with `ref.Events(name).On`, older consumers get their version's mapped values, and a reference can re-expose another's event member. One flush per turn after the published state, listeners in entry then subscription order; `event_mapping_failed` reports a failing mapping once per failure episode. Ports Pi 8d30e6306's host layer; the link and remote layers follow.
