package codingagent

import (
	"path/filepath"
	"testing"
)

func TestPortWave09FormatResumeCommand(t *testing.T) {
	t.Parallel()

	// upstream: packages/coding-agent/test/format-resume-command.test.ts:53
	t.Run("returns a session resume command for default session dirs", func(t *testing.T) {
		sessionManager := resumeSessionForTest(resumeSessionFileForTest(t))
		sessionManager.sessionID = "test-session"
		requirePathResult(t, formatResumeCommand(sessionManager, true), AppName+" --session test-session")
	})

	// upstream: packages/coding-agent/test/format-resume-command.test.ts:61
	t.Run("includes unquoted safe session dirs for non-default session dirs", func(t *testing.T) {
		sessionManager := resumeSessionForTest(resumeSessionFileForTest(t))
		sessionManager.sessionID = "test-session"
		sessionManager.sessionDir = "/tmp/custom-pi-sessions"
		sessionManager.usesDefaultSessionDir = false
		requirePathResult(t, formatResumeCommand(sessionManager, true), AppName+" --session-dir /tmp/custom-pi-sessions --session test-session")
	})

	// upstream: packages/coding-agent/test/format-resume-command.test.ts:76
	t.Run("quotes session dirs containing spaces", func(t *testing.T) {
		sessionManager := resumeSessionForTest(resumeSessionFileForTest(t))
		sessionManager.sessionID = "test-session"
		sessionManager.sessionDir = "/tmp/custom pi sessions"
		sessionManager.usesDefaultSessionDir = false
		requirePathResult(t, formatResumeCommand(sessionManager, true), AppName+" --session-dir '/tmp/custom pi sessions' --session test-session")
	})

	// upstream: packages/coding-agent/test/format-resume-command.test.ts:91
	t.Run("quotes session dirs containing single quotes", func(t *testing.T) {
		sessionManager := resumeSessionForTest(resumeSessionFileForTest(t))
		sessionManager.sessionID = "test-session"
		sessionManager.sessionDir = "/tmp/custom pi's sessions"
		sessionManager.usesDefaultSessionDir = false
		requirePathResult(t, formatResumeCommand(sessionManager, true), AppName+` --session-dir '/tmp/custom pi'\''s sessions' --session test-session`)
	})

	// upstream: packages/coding-agent/test/format-resume-command.test.ts:106
	t.Run("returns undefined when stdout is not a TTY", func(t *testing.T) {
		sessionManager := resumeSessionForTest(resumeSessionFileForTest(t))
		requirePathResult(t, formatResumeCommand(sessionManager, false), "")
	})

	// upstream: packages/coding-agent/test/format-resume-command.test.ts:114
	t.Run("returns undefined for in-memory sessions", func(t *testing.T) {
		sessionManager := resumeSessionForTest(resumeSessionFileForTest(t))
		sessionManager.persisted = false
		requirePathResult(t, formatResumeCommand(sessionManager, true), "")
	})

	// upstream: packages/coding-agent/test/format-resume-command.test.ts:122
	t.Run("returns undefined when the session file is missing", func(t *testing.T) {
		sessionManager := resumeSessionForTest(filepath.Join(t.TempDir(), "pi-missing-session.jsonl"))
		requirePathResult(t, formatResumeCommand(sessionManager, true), "")
	})

	// upstream: packages/coding-agent/test/format-resume-command.test.ts:129
	t.Run("returns undefined when the session file is not set", func(t *testing.T) {
		sessionManager := resumeSessionForTest("")
		requirePathResult(t, formatResumeCommand(sessionManager, true), "")
	})
}

// upstream: packages/coding-agent/test/format-resume-command.test.ts:27-33
func resumeSessionFileForTest(t *testing.T) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "session.jsonl")
	writeContextFile(t, file, "\n")
	return file
}

// upstream: packages/coding-agent/test/format-resume-command.test.ts:35-50: preserve the mocked SessionManager's independent state and defaults.
func resumeSessionForTest(sessionFile string) resumeCommandSession {
	return resumeCommandSession{
		persisted:             true,
		sessionFile:           sessionFile,
		sessionID:             "0197f6e4-4cf9-7f44-a2d8-f8f7f49ee9d3",
		sessionDir:            "/tmp/pi-sessions",
		usesDefaultSessionDir: true,
	}
}
