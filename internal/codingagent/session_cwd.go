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

// FormatMissingSessionCwdPrompt ports formatMissingSessionCwdPrompt.
func FormatMissingSessionCwdPrompt(issue SessionCwdIssue) string {
	return "Session cwd does not exist: " + issue.SessionCwd
}

// MissingSessionCwdChoice is how to open a session whose stored cwd is missing: run it in the current cwd once, or fork it there.
type MissingSessionCwdChoice string

const (
	// MissingSessionCwdContinue runs the session in the current cwd; the choice is not stored.
	MissingSessionCwdContinue MissingSessionCwdChoice = "continue"
	// MissingSessionCwdFork copies the session into the current cwd as a new session.
	MissingSessionCwdFork MissingSessionCwdChoice = "fork"
)

// MissingSessionCwdOption is one prompt option; an empty Value cancels (Pi's undefined).
type MissingSessionCwdOption struct {
	Label string
	Value MissingSessionCwdChoice
}

// GetMissingSessionCwdOptions ports getMissingSessionCwdOptions: the prompt options for a missing session cwd, naming the current cwd.
func GetMissingSessionCwdOptions(issue SessionCwdIssue) []MissingSessionCwdOption {
	return []MissingSessionCwdOption{
		{Label: "Continue in " + issue.FallbackCwd + " (asks again next time)", Value: MissingSessionCwdContinue},
		{Label: "Fork into " + issue.FallbackCwd + " (new session ID, original kept)", Value: MissingSessionCwdFork},
		{Label: "Cancel"},
	}
}

type MissingSessionCwdError struct{ Issue SessionCwdIssue }

func (err *MissingSessionCwdError) Error() string { return FormatMissingSessionCwdError(err.Issue) }

func AssertSessionCwdExists(session *Session, fallbackCwd string) error {
	if issue := GetMissingSessionCwdIssue(session, fallbackCwd); issue != nil {
		return &MissingSessionCwdError{Issue: *issue}
	}
	return nil
}
