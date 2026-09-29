package codingagent

// SessionImportFileNotFoundError reports a session import source that does
// not exist. Mirrors upstream SessionImportFileNotFoundError
// (agent-session-runtime.ts:46-54).
type SessionImportFileNotFoundError struct{ FilePath string }

func (err *SessionImportFileNotFoundError) Error() string { return "File not found: " + err.FilePath }
