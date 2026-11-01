### Fixed

- The Chord delivery scheduler no longer reads goroutine stacks or depends on how many goroutines the process holds: `AwaitEntered` is a start handshake (every delivery scheduled before the call has started), so the Durable Session's hop between two publications of one storage call orders the same at any goroutine count.
- Nested tool calls reach the Session line in call order, also when the calls waited on the same first memo, and a call's own wait no longer holds the next call's start (a deadlock with the core driver).
- A wait of a Session continuation stays registered until the continuation has entered, so a publication that looks for the waits it readied after their goroutines ran still waits for it.
