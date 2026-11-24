### Fixed

- Durable agent turns whose provider call failed with a node-fetch network error of the form `request to <url> failed, reason: ...` were not retried since v0.7.0 because the Durable core regex engine did not implement the `\S` non-space character class; they are now retried, as in Pi. <!-- fix: 83992d88018dacbc7e68b5874f3c030c1dd058fd, ce5cdf98237ed5b5252d12c015e736abdf46dd75 --> <!-- cause: durable/core/turn/rx.go only handled \d and \s after a backslash and fell through to literal characters for every other escape, so \S in patterns.go silently matched literal S. -->
