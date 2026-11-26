// SPDX-License-Identifier: MIT

package tui

// Ports packages/tui/src/program-resume.ts (Pi PR #12557, commit cf419553da817fefd2f86f170ff3e93eff0bfd28).
//
// Program Resume Protocol (OSC 7577): a program tells the terminal which command brings it back after the
// terminal restarts. Reports are authenticated with a token the terminal puts in the environment, so output
// that merely passes through the terminal (`cat`, logs, `ssh`) cannot register commands.
//
// Spec: https://www.superlogical.com/rex/docs/build/program-resume

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ProgramResumeTokenEnv is the environment variable through which the terminal hands its token to the first process.
const ProgramResumeTokenEnv = "OSC7577_TOKEN"

// ProgramResumeCommand is the command a terminal runs to resume a program.
type ProgramResumeCommand struct {
	// Argv holds the program and its arguments. The program is a bare command name looked up on PATH, or an absolute path.
	Argv []string
	// Cwd is the optional absolute working directory to resume in. Empty means absent.
	Cwd string
}

// ProgramResumeSender identifies who sends a report. Time is in seconds since the Unix epoch; terminals ignore reports far from their clock.
type ProgramResumeSender struct {
	Token string
	Pid   int
	Time  int64
}

// Limits every terminal accepts (Pi cf419553da packages/tui/src/program-resume.ts:42-43). Longer reports may be ignored, which would leave an older command in place.
// pig divergence (D662): carried from Pi cf419553da ahead of pinned Pi 1.6.0; retire D662 once the pin contains cf419553da.
const (
	programResumeMaxSequenceBytes = 2048
	programResumeMaxArgvElements  = 64
)

// TakeProgramResumeToken removes OSC7577_TOKEN from the process environment and returns it, or "" when unset or empty.
// A program that reports must not pass the token on: a child that kept it could replace the parent's command or,
// if it runs with fewer rights, register one that later runs with the user's full rights.
func TakeProgramResumeToken() string {
	token := os.Getenv(ProgramResumeTokenEnv)
	_ = os.Unsetenv(ProgramResumeTokenEnv)
	return token
}

// TakeProgramResumeTokenFromMap removes OSC7577_TOKEN from env and returns it, or "" when unset or empty.
// Mirrors upstream takeProgramResumeToken(env) for deterministic unit tests.
func TakeProgramResumeTokenFromMap(env map[string]string) string {
	if env == nil {
		return ""
	}
	token := env[ProgramResumeTokenEnv]
	delete(env, ProgramResumeTokenEnv)
	return token
}

// FormatProgramResume encodes a report of command. Returns ("", false) when the protocol cannot carry the command
// (control characters, invalid UTF-8, a relative program or cwd, or over the limits); the caller then sends
// FormatProgramResumeClear so the terminal does not resume an older command in its place.
func FormatProgramResume(command ProgramResumeCommand, sender ProgramResumeSender) (string, bool) {
	if !isReportableProgramResumeCommand(command) {
		return "", false
	}
	encoded := make([]string, len(command.Argv))
	for i, arg := range command.Argv {
		encoded[i] = base64.StdEncoding.EncodeToString([]byte(arg))
	}
	pairs := []string{"argv=" + strings.Join(encoded, ",")}
	if command.Cwd != "" {
		pairs = append(pairs, "cwd="+base64.StdEncoding.EncodeToString([]byte(command.Cwd)))
	}
	sequence := formatProgramResumeReport(pairs, sender)
	//portlint:allow utf16units sequence is pure ASCII (OSC prefix, digits, ASCII keys, base64, BEL) and programResumeMaxSequenceBytes is a wire byte limit
	if len(sequence) > programResumeMaxSequenceBytes {
		return "", false
	}
	return sequence, true
}

// FormatProgramResumeClear encodes a report that removes the sender's command, for example when the user quits the program.
func FormatProgramResumeClear(sender ProgramResumeSender) string {
	return formatProgramResumeReport([]string{"clear=1"}, sender)
}

func formatProgramResumeReport(pairs []string, sender ProgramResumeSender) string {
	parts := make([]string, 0, 2+len(pairs))
	parts = append(parts, "pid="+strconv.Itoa(sender.Pid), "time="+strconv.FormatInt(sender.Time, 10))
	parts = append(parts, pairs...)
	body := strings.Join(parts, ":")
	// Plain SHA-256 with the token last, not an HMAC: the spec chose it so shell scripts can report with sha256sum.
	sum := sha256.Sum256([]byte(body + ":" + sender.Token))
	return "\x1b]7577;" + body + ":mac=" + hex.EncodeToString(sum[:]) + "\x1b\\"
}

func isReportableProgramResumeText(text string) bool {
	if !utf8.ValidString(text) {
		return false
	}
	for _, r := range text {
		if r <= 0x1f || (r >= 0x7f && r <= 0x9f) {
			return false
		}
	}
	return true
}

func isReportableProgramResumeCommand(command ProgramResumeCommand) bool {
	if len(command.Argv) == 0 || len(command.Argv) > programResumeMaxArgvElements {
		return false
	}
	program := command.Argv[0]
	isBareName := len(program) > 0 && !strings.HasPrefix(program, "-") && !strings.ContainsAny(program, `/\`)
	if !isBareName && !filepath.IsAbs(program) {
		return false
	}
	if command.Cwd != "" && (!filepath.IsAbs(command.Cwd) || !isReportableProgramResumeText(command.Cwd)) {
		return false
	}
	for _, arg := range command.Argv {
		if !isReportableProgramResumeText(arg) {
			return false
		}
	}
	return true
}
