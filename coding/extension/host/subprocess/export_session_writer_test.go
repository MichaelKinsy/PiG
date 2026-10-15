package subprocess

// SessionWriterForTest exposes sessionWriter to the external test package, which can import internal/codingagent (the internal test package cannot: codingagent imports this package).
type SessionWriterForTest = sessionWriter
