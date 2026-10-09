// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package codingagent

import (
	"fmt"
	"os"
)

// Ports packages/coding-agent/src/core/session-cwd.ts.
// SessionCwdIssue identifies a persisted Session whose effective working directory is missing.
type SessionCwdIssue struct {
	SessionFile string `json:"sessionFile,omitempty"`
	SessionCwd  string `json:"sessionCwd"`
	FallbackCwd string `json:"fallbackCwd"`
}

func GetMissingSessionCwdIssue(session *Session, fallbackCwd string) *SessionCwdIssue {
	if session == nil || session.Path() == "" || session.CWD() == "" {
		return nil
	}
	if _, err := os.Stat(session.CWD()); err == nil {
		return nil
	}
	return &SessionCwdIssue{SessionFile: session.Path(), SessionCwd: session.CWD(), FallbackCwd: fallbackCwd}
}

func FormatMissingSessionCwdError(issue SessionCwdIssue) string {
	file := ""
	if issue.SessionFile != "" {
		file = "\nSession file: " + issue.SessionFile
	}
	return fmt.Sprintf("Stored session working directory does not exist: %s%s\nCurrent working directory: %s", issue.SessionCwd, file, issue.FallbackCwd)
}
func FormatMissingSessionCwdPrompt(issue SessionCwdIssue) string {
	return fmt.Sprintf("cwd from session file does not exist\n%s\n\ncontinue in current cwd\n%s", issue.SessionCwd, issue.FallbackCwd)
}

type MissingSessionCwdError struct{ Issue SessionCwdIssue }

func (err *MissingSessionCwdError) Error() string { return FormatMissingSessionCwdError(err.Issue) }

func AssertSessionCwdExists(session *Session, fallbackCwd string) error {
	if issue := GetMissingSessionCwdIssue(session, fallbackCwd); issue != nil {
		return &MissingSessionCwdError{Issue: *issue}
	}
	return nil
}
