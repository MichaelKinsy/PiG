### Added

- Chord: the client runtimes of the page and the terminal (the vendored TypeScript client) have event members: a contract declares `event(schema)` members, an object emits with `self.events()`, a reference subscribes with `member.on`, and React components subscribe with `useEvents(member, listener)`. Ports the client side of Pi 8d30e6306; the wire goldens of the link now cover the event message, the event ack and a hello without a call window.
