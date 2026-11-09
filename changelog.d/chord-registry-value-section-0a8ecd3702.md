### Fixed

- A Chord registry value source reads its composition in a section of the host's loop (inline when the caller is in one): `Value`, `Start` and `Current` from another goroutine (a native's open callback, a request) no longer race a contribution added as an object goes live or a waiter registering. <!-- fix: 0a8ecd3702b -->
