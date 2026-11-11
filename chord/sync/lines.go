// Package sync is session sync over a Chord store's outbox (Pi's `@earendil-works/chord/sync`, experimental): upload the
// commits after the last sequence a copy shares with a remote into a slot the remote creates once, pull a copy forward
// with ApplyBatch, restore from an archive, fold history into snapshots, and decide when to upload. Portable: the remote
// and the host's sync state are interfaces, and the directory-backed ones take their file operations (SyncFileSystem).
//
// Bodies are streams (SyncBody), so neither side holds a whole batch or snapshot in memory.
package sync

import (
	"fmt"
	"math"
	"strconv"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/sync/lines.ts

// LogVersion is the version of the line envelope. It changes when the envelope changes; the writes inside carry the
// store outbox's own `format`, which ApplyBatch checks.
const LogVersion = 2

// LogFormatError is a log line, or a batch body, that is not what EncodeLogLine writes.
type LogFormatError struct {
	Message string
	Cause   error
}

func (err *LogFormatError) Error() string { return err.Message }

// Unwrap is the error that made the line malformed, if any.
func (err *LogFormatError) Unwrap() error { return err.Cause }

// NewLogFormatError is Pi's `new LogFormatError(message, { cause })`: cause is nil when the error has none.
func NewLogFormatError(message string, cause error) *LogFormatError {
	return &LogFormatError{Message: message, Cause: cause}
}

func logFormatError(cause error, format string, args ...any) *LogFormatError {
	return NewLogFormatError(fmt.Sprintf(format, args...), cause)
}

// EncodeLogLine is one outbox commit as a JSON line, terminated by a newline. A body's encoder appends the line to the
// chunk it fills (appendLogLine), so no copy of it is made there.
func EncodeLogLine(commit store.OutboxCommit) (string, error) {
	line, err := appendLogLine(make([]byte, 0, 128), commit)
	if err != nil {
		return "", err
	}
	return string(line), nil
}

// appendLogLine appends EncodeLogLine's line to dst.
func appendLogLine(line []byte, commit store.OutboxCommit) ([]byte, error) {
	line = append(line, `{"v":`...)
	line = strconv.AppendInt(line, LogVersion, 10)
	line = append(line, `,"seq":`...)
	line = strconv.AppendInt(line, commit.Seq, 10)
	line = append(line, `,"nextId":`...)
	line = strconv.AppendInt(line, commit.NextID, 10)
	line = append(line, `,"writes":`...)
	line, err := appendWrites(line, commit)
	if err != nil {
		return nil, err
	}
	return append(line, "}\n"...), nil
}

// appendWrites appends the commit's writes as JSON: the recorded value as it is, or the typed writes of a commit built
// by hand.
func appendWrites(dst []byte, commit store.OutboxCommit) ([]byte, error) {
	if commit.Raw != nil {
		return jsstring.AppendMarshalJSON(dst, commit.Raw)
	}
	if len(commit.Writes) == 0 {
		return append(dst, "[]"...), nil
	}
	return jsstring.AppendMarshalJSON(dst, commit.Writes)
}

// DecodeLogLine is one line (without its newline) as an outbox commit; number names the line in errors. A line of an
// unknown version is an error: an older pi must not apply writes it does not understand. The writes themselves are
// checked by ApplyBatch, a trust boundary.
func DecodeLogLine(text string, number int) (store.OutboxCommit, error) {
	value, err := delta.DecodeJson([]byte(text))
	if err != nil {
		return store.OutboxCommit{}, logFormatError(err, "Log line %d is not JSON", number)
	}
	line, ok := value.(*delta.JsonObject)
	if !ok {
		return store.OutboxCommit{}, logFormatError(nil, "Log line %d is not an object", number)
	}
	if version, ok := line.Value("v").(float64); !ok || version != LogVersion {
		return store.OutboxCommit{}, logFormatError(nil, "Log line %d has unknown version %s", number, describe(line, "v"))
	}
	seq, ok := safeInteger(line.Value("seq"))
	if !ok || seq < 1 {
		return store.OutboxCommit{}, logFormatError(nil, "Log line %d has no valid sequence", number)
	}
	nextID, ok := safeInteger(line.Value("nextId"))
	if !ok {
		return store.OutboxCommit{}, logFormatError(nil, "Log line %d has no valid next id", number)
	}
	writes := line.Value("writes")
	if _, ok := writes.([]any); !ok {
		return store.OutboxCommit{}, logFormatError(nil, "Log line %d has no writes", number)
	}
	return store.NewOutboxCommit(seq, nextID, writes), nil
}

// safeInteger is value as `Number.isSafeInteger` accepts it.
func safeInteger(value any) (int64, bool) {
	number, ok := value.(float64)
	if !ok || number != math.Trunc(number) || math.Abs(number) > float64(store.MaxSafeInteger) {
		return 0, false
	}
	//portlint:allow numbers number is an integer within MaxSafeInteger by the checks above
	return int64(number), true
}

// describe is the member of object as JavaScript's `String(value)` prints it in a message: `undefined` when absent.
func describe(object *delta.JsonObject, key string) string {
	value, present := object.Get(key)
	if !present {
		return "undefined"
	}
	if number, ok := value.(float64); ok {
		return strconv.FormatFloat(number, 'f', -1, 64)
	}
	text, err := jsstring.MarshalJSON(value)
	if err != nil {
		return "?"
	}
	return string(text)
}
