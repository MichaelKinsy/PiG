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
	return &FileError{Code: code, Message: message, Path: path, Cause: cause}
}

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
	return &ExecutionError{Code: code, Message: message, Cause: cause}
}

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

// ShellExecOptions configures Shell.Exec.
type ShellExecOptions struct {
	Cwd        string
	Env        map[string]string
	InheritEnv *bool
	// Timeout is in seconds; nil has no timeout.
	Timeout *float64
	// OnOutput receives every decoded chunk of combined stdout and stderr as it arrives: raw, unbounded, and
	// unthrottled.
	OnOutput func(ctx context.Context, text string)
	Spill    *ShellSpillOptions
}

// Shell runs commands.
type Shell interface {
	Exec(ctx context.Context, command string, options *ShellExecOptions) (ShellExecResult, error)
	Cleanup(ctx context.Context) error
}

// ExecutionEnv is a file system and a shell.
type ExecutionEnv interface {
	FileSystem
	Shell
}
