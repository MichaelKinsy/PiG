### Added

- Chord: the store writes the commits queued behind a running commit step in one backend transaction (group commit, Pi main `8889e623d`, decision S18): each keeps its own seq, outbox commit and adoption in queue order and the metadata is written once; a refusal in a group rolls it back and runs its commits one by one, so only the refusing commit is refused. A backend failure while the store closes is latched and refuses the commits admitted after it. <!-- feat: 8889e623d -->
