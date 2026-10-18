### Changed

- Each turn costs less CPU in a session of a few hundred turns: a goroutine's ID is read from the runtime's goroutine record instead of formatting its stack (the stream and lock checks asked on every request and event), the goroutines a turn starts grow their stack in one step, and a request build no longer deep-copies every tool schema several times. Messages, events, session files and request bodies are unchanged.
