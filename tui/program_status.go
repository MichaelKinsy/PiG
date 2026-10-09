package tui

// Ports packages/tui/src/program-status.ts.
//
// Program Status Protocol (OSC 7501): a program tells the terminal whether it is idle, working, blocked on the user,
// done, or failed. Only the root record is supported.
//
// Spec: https://www.superlogical.com/rex/docs/build/program-status

import (
	"encoding/base64"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// ProgramState is the state a [ProgramStatus] reports. `clear` removes the status instead of reporting one.
type ProgramState string

const (
	ProgramStateIdle    ProgramState = "idle"
	ProgramStateWorking ProgramState = "working"
	ProgramStateBlocked ProgramState = "blocked"
	ProgramStateDone    ProgramState = "done"
	ProgramStateError   ProgramState = "error"
	ProgramStateClear   ProgramState = "clear"
)

// ProgramStatusKind is what a blocked program waits for.
type ProgramStatusKind string

const (
	ProgramStatusKindPermission ProgramStatusKind = "permission"
	ProgramStatusKindQuestion   ProgramStatusKind = "question"
	ProgramStatusKindAuth       ProgramStatusKind = "auth"
)

// ProgramStatus is one status report. A field that is the empty string is upstream's absent (`undefined`) field.
type ProgramStatus struct {
	State ProgramState
	// App is a stable program name, `[A-Za-z0-9_.+-]{1,32}`. Other values are omitted.
	App string
	// Kind is what a blocked program waits for. Omitted for other states.
	Kind ProgramStatusKind
	// Message is one human-readable line. Control characters become spaces; longer text is cut to the spec limit.
	Message string
}

// ProgramStatusQuery is the feature detection query. A supporting terminal replies with the same body.
const ProgramStatusQuery = "\x1b]7501;?\x1b\\"

var programStatusReply = lazyregexp.New(`^\x1b\]7501;\?[^\x07\x1b]*(?:\x07|\x1b\\)$`)

// IsProgramStatusReply reports whether sequence is the reply to [ProgramStatusQuery]. Later spec revisions may add pairs after the `?`.
func IsProgramStatusReply(sequence string) bool {
	return programStatusReply.MatchString(sequence)
}

var programStatusApp = lazyregexp.New(`^[A-Za-z0-9_.+-]{1,32}$`)

// programStatusMaxMessageBytes is the decoded `msg` limit. Its base64 encoding stays under the 2732-byte encoded limit.
const programStatusMaxMessageBytes = 2048

// truncateUTF8 cuts text to at most maxBytes without splitting a code point.
func truncateUTF8(text string, maxBytes int) string {
	if len(text) <= maxBytes {
		return text
	}
	bytes := 0
	end := 0
	for _, char := range text {
		size := utf8.RuneLen(char)
		if bytes+size > maxBytes {
			break
		}
		bytes += size
		end += size
	}
	return text[:end]
}

// replaceControlCharacters is `text.replace(/[\u0000-\u001f\u007f-\u009f]+/g, " ")`: each run of control characters becomes one space.
func replaceControlCharacters(text string) string {
	var out strings.Builder
	inRun := false
	for _, char := range text {
		if char <= 0x1f || (char >= 0x7f && char <= 0x9f) {
			if !inRun {
				out.WriteByte(' ')
				inRun = true
			}
			continue
		}
		inRun = false
		out.WriteRune(char)
	}
	return out.String()
}

// FormatProgramStatus encodes a status report. Terminals discard reports whose text contains control characters, so they are replaced.
func FormatProgramStatus(status ProgramStatus) string {
	pairs := []string{"state=" + string(status.State)}
	if status.App != "" && programStatusApp.MatchString(status.App) {
		pairs = append(pairs, "app="+status.App)
	}
	if status.State == ProgramStateBlocked && status.Kind != "" {
		pairs = append(pairs, "kind="+string(status.Kind))
	}
	// A message is UTF-8 text: bytes that are not valid become U+FFFD, as encoding a JavaScript string does for a lone surrogate.
	message := truncateUTF8(widthx.JSTrim(replaceControlCharacters(strings.ToValidUTF8(status.Message, "\uFFFD"))), programStatusMaxMessageBytes)
	if message != "" {
		pairs = append(pairs, "msg="+base64.StdEncoding.EncodeToString([]byte(message)))
	}
	return "\x1b]7501;" + strings.Join(pairs, ":") + "\x1b\\"
}
