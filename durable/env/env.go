// Package env declares the portable execution environment of pi-durable: a file system and a shell that tools and
// prompt sections use through a conversation's environment.
//
// Ports packages/durable/src/env/index.ts
//
// Go mapping: upstream operations return `Result<T, E>` instead of throwing. Each Go method returns `(T, error)`; an
// expected failure is a *FileError or *ExecutionError and never a panic. The Result helpers stay for callers that hold
// a value-or-failure pair.
package env

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Result is the value or the expected failure of a fallible operation.
type Result[TValue, TError any] struct {
	Ok    bool
	Value TValue
	Error TError
}

// Ok returns a successful result.
func Ok[TValue, TError any](value TValue) Result[TValue, TError] {
	return Result[TValue, TError]{Ok: true, Value: value}
}

// Err returns a failed result.
func Err[TValue, TError any](err TError) Result[TValue, TError] {
	return Result[TValue, TError]{Error: err}
}

// GetOrThrow returns the value, or the failure as a Go error.
func GetOrThrow[TValue any, TError error](result Result[TValue, TError]) (TValue, error) {
	if !result.Ok {
		var zero TValue
		return zero, result.Error
	}
	return result.Value, nil
}

// GetOrUndefined returns the value, or nil for a failure.
func GetOrUndefined[TValue, TError any](result Result[*TValue, TError]) *TValue {
	if result.Ok {
		return result.Value
	}
	return nil
}

// ToError converts a thrown value to an error: an error is kept, a string becomes its message, and any other value
// becomes its JSON text, or its default formatting when JSON fails.
func ToError(value any) error {
	switch typed := value.(type) {
	case error:
		return typed
	case string:
		return errors.New(typed)
	}
	// JSON.stringify leaves <, >, and & unescaped.
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return errors.New(fmt.Sprint(value))
	}
	return errors.New(strings.TrimSuffix(encoded.String(), "\n"))
}

// FileKind is "file", "directory", or "symlink".
type FileKind string

const (
	FileKindFile      FileKind = "file"
	FileKindDirectory FileKind = "directory"
	FileKindSymlink   FileKind = "symlink"
)

// FileErrorCode classifies a FileError.
type FileErrorCode string

const (
	FileErrorAborted          FileErrorCode = "aborted"
	FileErrorNotFound         FileErrorCode = "not_found"
	FileErrorPermissionDenied FileErrorCode = "permission_denied"
	FileErrorNotDirectory     FileErrorCode = "not_directory"
	FileErrorIsDirectory      FileErrorCode = "is_directory"
	FileErrorInvalid          FileErrorCode = "invalid"
	FileErrorNotSupported     FileErrorCode = "not_supported"
	FileErrorUnknown          FileErrorCode = "unknown"
)

// FileError is the expected failure of a FileSystem operation.
type FileError struct {
	Code    FileErrorCode
	Message string
	// Path is empty when the failure has no path.
	Path  string
	Cause error
}

// NewFileError returns a FileError; path and cause may be empty.
func NewFileError(code FileErrorCode, message, path string, cause error) *FileError {
	e := &FileError{Code: code, Message: message, Path: path, Cause: cause}
	return e
}

// Name is the `name` property, "FileError".
func (*FileError) Name() string { return "FileError" }

func (e *FileError) Error() string { return e.Message }

// Unwrap returns the cause.
func (e *FileError) Unwrap() error { return e.Cause }

// ExecutionErrorCode classifies an ExecutionError.
type ExecutionErrorCode string

const (
	ExecutionErrorAborted          ExecutionErrorCode = "aborted"
	ExecutionErrorTimeout          ExecutionErrorCode = "timeout"
	ExecutionErrorShellUnavailable ExecutionErrorCode = "shell_unavailable"
	ExecutionErrorSpawnError       ExecutionErrorCode = "spawn_error"
	ExecutionErrorCallbackError    ExecutionErrorCode = "callback_error"
	ExecutionErrorUnknown          ExecutionErrorCode = "unknown"
)

// ExecutionError is the expected failure of a Shell command.
type ExecutionError struct {
	Code    ExecutionErrorCode
	Message string
	// SpillPath is the spill file of a command that timed out or was aborted after its output crossed the spill
	// thresholds; empty otherwise.
	SpillPath string
	Cause     error
}

// NewExecutionError returns an ExecutionError; cause may be nil.
func NewExecutionError(code ExecutionErrorCode, message string, cause error) *ExecutionError {
	e := &ExecutionError{Code: code, Message: message, Cause: cause}
	return e
}

// Name is the error class name, Pi's `name` property.
func (*ExecutionError) Name() string { return "ExecutionError" }

func (e *ExecutionError) Error() string { return e.Message }

// Unwrap returns the cause.
func (e *ExecutionError) Unwrap() error { return e.Cause }

// FileInfo describes one file system entry.
type FileInfo struct {
	Name    string   `json:"name"`
	Path    string   `json:"path"`
	Kind    FileKind `json:"kind"`
	Size    int64    `json:"size"`
	MtimeMs float64  `json:"mtimeMs"`
}

// TextLine is one line read by a TextLineReader.
type TextLine struct {
	Text       string `json:"text"`
	Terminated bool   `json:"terminated"`
}

// TextLineReader reads a text file line by line.
type TextLineReader interface {
	// ReadLine returns nil at the end of the file.
	ReadLine(ctx context.Context) (*TextLine, error)
	Close(ctx context.Context) error
}

// BinaryReader makes positional reads from one opened regular file; all calls see the same file even if its path is
// renamed. Close is idempotent and every other call fails with an invalid FileError after it.
type BinaryReader interface {
	// Info is the metadata of the opened file, not of whatever its path names now.
	Info(ctx context.Context) (FileInfo, error)
	// Read returns up to length bytes at offset, fewer only at the end of the file. A negative offset or length, or
	// one above JavaScript's largest safe integer, is invalid.
	Read(ctx context.Context, offset, length int64) ([]byte, error)
	// ScanLines is one pass over the file that locates lines [StartLine, EndLine) (EndLine nil: to the end), 0-based,
	// where line k starts after the k-th newline byte. Decoded sizes are those of the text a TextDecoder would produce
	// for that range, so a byte-order mark at the start of the file is not counted.
	ScanLines(ctx context.Context, options ScanLinesOptions) (LineScan, error)
	Close(ctx context.Context) error
}

// ScanLinesOptions selects the lines BinaryReader.ScanLines measures.
type ScanLinesOptions struct {
	StartLine int64
	// EndLine is nil to scan to the end of the file.
	EndLine *int64
}

// LineScan is where the lines of a file are, as BinaryReader.ScanLines found them.
type LineScan struct {
	// Newlines is the number of newline bytes in the whole file; it has Newlines + 1 lines.
	Newlines int64
	// Start and End are the byte range of the selected lines: from the start of the first to the end of the last,
	// without the newline that ends it. A selection past the last line is empty at the end of the file.
	Start, End int64
	// FirstLineEnd is where the first selected line ends: its newline, or the end of the file.
	FirstLineEnd int64
	// LastLineStart is where the last selected line starts.
	LastLineStart int64
	// SelectedBytes and FirstLineBytes are the UTF-8 byte lengths of the decoded selection and of its first line.
	SelectedBytes, FirstLineBytes int64
}

// DirPage is one page of a DirReader.
type DirPage struct {
	Entries []FileInfo
	// Done marks the end; it may come with the last entries or with an empty page.
	Done bool
}

// DirReader pages the entries of one directory.
type DirReader interface {
	// Next returns up to maxEntries entries in the order the file system returns them, continuing where the previous
	// call stopped. An entry that disappears before its metadata is read is skipped, as are entries of unsupported
	// kinds. After a failed or aborted call, close the reader.
	Next(ctx context.Context, maxEntries int) (DirPage, error)
	Close(ctx context.Context) error
}

// OpenBinaryReaderOptions configures FileSystem.OpenBinaryReader.
type OpenBinaryReaderOptions struct {
	// NoFollow refuses a symbolic link as the final path component with an invalid FileError instead of following it;
	// earlier components are still resolved.
	NoFollow bool
}

// WatchExclude names the entries below a watched path that are neither watched nor reported.
type WatchExclude struct {
	// Hidden excludes names starting with ".".
	Hidden bool
	Names  []string
}

// WatchTarget is a file or directory to watch. It may be missing; creating it is a change.
type WatchTarget struct {
	Path string
	// Recursive watches everything below a directory, not only its entries. Symbolic links below it are not followed.
	Recursive bool
	Exclude   *WatchExclude
}

// WatchChange is what changed. It is WatchChangePaths, WatchChangeOverflow, or WatchChangeError.
type WatchChange interface{ watchChange() }

// WatchChangePaths reports that something at or below each path may have changed (a directory path covers its whole
// subtree). Calls may be spurious; a change is never missed while the watcher is healthy.
type WatchChangePaths struct{ Paths []string }

// WatchChangeOverflow reports that coverage was uncertain for a while (lost events, reconnect): rescan everything that
// is watched.
type WatchChangeOverflow struct{}

// WatchChangeError reports that the watcher stopped, for example because the watched tree grew past the environment's
// limit; no calls follow.
type WatchChangeError struct{ Error *FileError }

func (WatchChangePaths) watchChange()    {}
func (WatchChangeOverflow) watchChange() {}
func (WatchChangeError) watchChange()    {}

// WatchMode is how a FileWatcher learns of changes.
type WatchMode string

const (
	// WatchNative: changes are reported within about two seconds.
	WatchNative WatchMode = "native"
	// WatchPolling: the environment compares snapshots, because the file system does not report changes reliably
	// (network and FUSE file systems); a change undone between two snapshots can be missed.
	WatchPolling WatchMode = "polling"
)

// FileWatcher is a running watch.
type FileWatcher interface {
	Mode() WatchMode
	// Close stops watching; no onChange call starts after it returns. It is idempotent.
	Close(ctx context.Context) error
}

// ReadTextLinesOptions bounds FileSystem.ReadTextLines; nil MaxLines reads every line.
type ReadTextLinesOptions struct {
	MaxLines *int
}

// CreateDirOptions configures FileSystem.CreateDir.
type CreateDirOptions struct {
	// Recursive creates missing parents; nil means true, as upstream's options?.recursive ?? true.
	Recursive *bool
}

// RemoveOptions configures FileSystem.Remove.
type RemoveOptions struct {
	Recursive bool
	Force     bool
}

// CreateTempFileOptions configures FileSystem.CreateTempFile.
type CreateTempFileOptions struct {
	Prefix string
	Suffix string
}

// FileSystem is the portable file system capability. Operations return failures rather than panicking. Context comes first, as Go convention, where upstream passes it last.
type FileSystem interface {
	// Id names the file namespace: equal ids see the same files at the same paths, whatever their cwd. Every local
	// environment shares one id; each container or remote host has its own.
	Id() string
	Cwd() string
	SetCwd(cwd string)
	AbsolutePath(ctx context.Context, path string) (string, error)
	JoinPath(ctx context.Context, parts []string) (string, error)
	ReadTextFile(ctx context.Context, path string) (string, error)
	OpenTextLineReader(ctx context.Context, path string) (TextLineReader, error)
	ReadTextLines(ctx context.Context, path string, options *ReadTextLinesOptions) ([]string, error)
	ReadBinaryFile(ctx context.Context, path string) ([]byte, error)
	// OpenBinaryReader opens a regular file for bounded positional reads. A directory fails with is_directory, other
	// non-regular files with invalid. options may be nil.
	OpenBinaryReader(ctx context.Context, path string, options *OpenBinaryReaderOptions) (BinaryReader, error)
	// WriteFile writes content, a string or []byte.
	WriteFile(ctx context.Context, path string, content any) error
	// AppendFile appends content, a string or []byte.
	AppendFile(ctx context.Context, path string, content any) error
	// TruncateFile truncates or extends a file to exactly size bytes.
	TruncateFile(ctx context.Context, path string, size int64) error
	// FlushFile flushes file contents and the metadata needed to retrieve them from an open file handle.
	FlushFile(ctx context.Context, path string) error
	RenameFile(ctx context.Context, sourcePath, destinationPath string) error
	FileInfo(ctx context.Context, path string) (FileInfo, error)
	ListDir(ctx context.Context, path string) ([]FileInfo, error)
	OpenDirReader(ctx context.Context, path string) (DirReader, error)
	// Watch reports changes to files and directories, for hosts that load resources from the environment. When it
	// returns a watcher, coverage is established: a host that watches before it loads cannot miss a change made during
	// the load. See WatchChange for what is reported and FileWatcher.Mode for how reliably.
	Watch(ctx context.Context, targets []WatchTarget, onChange func(WatchChange)) (FileWatcher, error)
	CanonicalPath(ctx context.Context, path string) (string, error)
	Exists(ctx context.Context, path string) (bool, error)
	CreateDir(ctx context.Context, path string, options *CreateDirOptions) error
	Remove(ctx context.Context, path string, options *RemoveOptions) error
	CreateTempDir(ctx context.Context, prefix *string) (string, error)
	CreateTempFile(ctx context.Context, options *CreateTempFileOptions) (string, error)
	Cleanup(ctx context.Context) error
}

// ShellSpillOptions spills the complete output to a temporary file once it exceeds either threshold.
type ShellSpillOptions struct {
	AfterBytes int64
	// AfterLines counts complete or partial lines.
	AfterLines int64
}

// ShellExecResult is the result of a completed command.
type ShellExecResult struct {
	ExitCode int
	// SpillPath is the temporary file holding the complete raw output when the spill thresholds were exceeded.
	SpillPath string
}

// ShellStream names the stream an output chunk came from.
type ShellStream string

const (
	ShellStdout ShellStream = "stdout"
	ShellStderr ShellStream = "stderr"
)

// ShellOutputWindow is the tail of the combined output a caller keeps, and how often it samples it. An environment that
// transfers output over a slow link uses it to omit what the caller would drop anyway and to send no faster than the
// caller commits.
type ShellOutputWindow struct {
	// MaxBytes is the UTF-8 bytes of decoded text kept at the end of the output.
	MaxBytes int
	// MaxLines is the lines kept at the end of the output.
	MaxLines int
	// MinIntervalMs is the minimum pause between the caller's samples of the output.
	MinIntervalMs float64
	// BytesPerSecond: each sample also pauses the caller in proportion to its size at this rate.
	BytesPerSecond float64
}

// ShellOutputSkip is output an environment omitted, measured on the decoded text OnOutput would have received: every
// U+FFFD counts as three bytes, and no sanitizing is applied.
type ShellOutputSkip struct {
	// Bytes is the UTF-8 byte length of the omitted text.
	Bytes int
	// Newlines is the number of newlines (U+000A) in the omitted text.
	Newlines int
	// EndsWithNewline reports whether the omitted text ends with a newline.
	EndsWithNewline bool
}

// ShellOutputInfo describes one output chunk.
type ShellOutputInfo struct {
	Stream ShellStream
	// Skipped is output omitted immediately before this chunk, only with ShellExecOptions.Window. The chunk then holds
	// all output after the omission up to its end, and that is more than the window by at least one byte or one line:
	// more than Window.MaxBytes bytes or more than Window.MaxLines newlines. So the omitted text can never be in the
	// kept tail. The omission and such a chunk may span both streams in arrival order; Stream then names the chunk's
	// last stream. Callers that need the streams apart do not pass Window.
	Skipped *ShellOutputSkip
}

// ShellExecOptions configures Shell.Exec.
type ShellExecOptions struct {
	Cwd        string
	Env        map[string]string
	InheritEnv *bool
	// Timeout is in seconds; nil has no timeout.
	Timeout *float64
	// OnOutput receives every decoded chunk of stdout and stderr as it arrives, in arrival order, with the stream it
	// came from: raw, unbounded, and unthrottled. Each stream is decoded separately, so a character split across
	// chunks survives.
	OnOutput func(ctx context.Context, text string, info ShellOutputInfo)
	Spill    *ShellSpillOptions
	// Window is the tail the caller keeps, so the environment may omit output outside it and report the omission as
	// ShellOutputInfo.Skipped. Without it, every chunk is delivered.
	Window *ShellOutputWindow
}

// Shell runs commands.
type Shell interface {
	// Exec runs a command. A string runs through the environment's shell. A []string runs its first element directly
	// with the rest as its arguments, without a shell, so they reach the program unparsed; an empty one is a
	// spawn_error. Aborting ctx or a timeout kills only this command's processes.
	Exec(ctx context.Context, command any, options *ShellExecOptions) (ShellExecResult, error)
	// Cleanup kills every command this environment still runs; for its owner's shutdown, never for one request.
	Cleanup(ctx context.Context) error
}

// ExecutionEnv is a file system and a shell.
type ExecutionEnv interface {
	FileSystem
	Shell
}
