// Package harness holds the shared declarations of Pi's durable agent
// harness: tool, resource, stream-option, execution-environment, and
// shell-output contracts consumed by the session, execution, runtime, and
// utility packages beneath it.
package harness

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// JsonValue is a strict JSON value: nil, bool, number, string, []any, or
// map[string]any.
type JsonValue = any

// AgentToolResult is the harness tool result wire shape: text or image content
// returned to the model, arbitrary details, optional usage, and an optional
// termination hint. It replaces agent.AgentToolResult, whose legacy
// coding-agent representation flattens content to one string.
type AgentToolResult struct {
	Content   []ai.ToolResultMessageContent `json:"content"`
	Details   JsonValue                     `json:"details,omitempty"`
	Usage     *ai.Usage                     `json:"usage,omitempty"`
	Terminate *bool                         `json:"terminate,omitempty"`
}

// UnmarshalJSON decodes the text/image content union.
func (result *AgentToolResult) UnmarshalJSON(data []byte) error {
	var wire struct {
		Content   []json.RawMessage `json:"content"`
		Details   JsonValue         `json:"details"`
		Usage     *ai.Usage         `json:"usage"`
		Terminate *bool             `json:"terminate"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	content, err := DecodeToolResultContent(wire.Content)
	if err != nil {
		return err
	}
	*result = AgentToolResult{Content: content, Details: wire.Details, Usage: wire.Usage, Terminate: wire.Terminate}
	return nil
}

// DecodeToolResultContent decodes a text/image content array.
func DecodeToolResultContent(raw []json.RawMessage) ([]ai.ToolResultMessageContent, error) {
	if raw == nil {
		return nil, nil
	}
	content := make([]ai.ToolResultMessageContent, 0, len(raw))
	for index, item := range raw {
		block, err := ai.UnmarshalContentBlock(item)
		if err != nil {
			return nil, fmt.Errorf("content[%d]: %w", index, err)
		}
		value, ok := block.(ai.ToolResultMessageContent)
		if !ok {
			return nil, fmt.Errorf("content[%d]: not text or image content", index)
		}
		content = append(content, value)
	}
	return content, nil
}

// Skill is loaded from a SKILL.md file or provided by an application. Name,
// Description, and FilePath are inserted into the system prompt.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content"`
	FilePath    string `json:"filePath"`
	// DisableModelInvocation excludes the skill from model-visible listings
	// while still allowing explicit application invocation.
	DisableModelInvocation bool `json:"disableModelInvocation,omitempty"`
}

// PromptTemplate can be formatted into a prompt for explicit invocation.
type PromptTemplate struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Content     string `json:"content"`
}

// AgentHarnessResources are the resources available to explicit invocation
// methods and system-prompt callbacks.
type AgentHarnessResources struct {
	PromptTemplates []PromptTemplate `json:"promptTemplates,omitempty"`
	Skills          []Skill          `json:"skills,omitempty"`
}

// AgentHarnessToolUpdateOptions configures one live tool progress update.
type AgentHarnessToolUpdateOptions struct {
	// Checkpoint requests replacement of the invocation's durable recovery
	// checkpoint.
	Checkpoint bool
}

// AgentHarnessToolUpdateCallback is the synchronous full-snapshot progress
// callback supplied to harness-native tools.
type AgentHarnessToolUpdateCallback func(partialResult AgentToolResult, options AgentHarnessToolUpdateOptions)

// AgentHarnessToolInvocation is the stable harness identity for one logical
// tool call, unchanged during safe replay.
type AgentHarnessToolInvocation interface {
	// InvocationID is the opaque session-unique id equal to the call's
	// reserved result-entry id.
	InvocationID() string
	OperationID() string
	TurnID() string
	// GetMemo reads one invocation-scoped durable replay memo; ok is false
	// when the memo is unset.
	GetMemo(ctx context.Context, name string) (value JsonValue, ok bool, err error)
	// SetMemo sets one invocation-scoped durable replay memo; a nil value
	// deletes it.
	SetMemo(ctx context.Context, name string, value *JsonValue) error
}

// Tool replay policies for an effect whose durable intent exists but whose
// outcome is unknown.
const (
	ToolReplayNever = "never"
	ToolReplaySafe  = "safe"
)

// AgentHarnessTool is a tool executed by the harness with an
// application-defined tool context resolved for the current turn snapshot.
type AgentHarnessTool struct {
	ai.ToolSchema
	Label string
	// PrepareArguments is an optional compatibility shim applied to raw
	// tool-call arguments before schema validation.
	PrepareArguments func(args JsonValue) (JsonValue, error)
	// Replay is ToolReplayNever, ToolReplaySafe, or empty (never).
	Replay string
	// ExecutionMode overrides the default sequential/parallel mode.
	ExecutionMode agent.ToolExecutionMode
	Execute       func(ctx context.Context, toolCallID string, params map[string]JsonValue, onUpdate AgentHarnessToolUpdateCallback, toolContext any, invocation AgentHarnessToolInvocation) (AgentToolResult, error)
}

// AgentHarnessDeferredOption is the curated deferred-generation request:
// JSON `true`/`false` or an object with an optional window.
type AgentHarnessDeferredOption struct {
	// Object reports the object form; Enabled holds the boolean form.
	Object  bool
	Enabled bool
	// Window is "15m", "1h", "24h", or empty when absent.
	Window string
}

// MarshalJSON emits the boolean or object form.
func (option AgentHarnessDeferredOption) MarshalJSON() ([]byte, error) {
	if !option.Object {
		return json.Marshal(option.Enabled)
	}
	type object struct {
		Window string `json:"window,omitempty"`
	}
	return json.Marshal(object{Window: option.Window})
}

// UnmarshalJSON decodes the boolean or object form.
func (option *AgentHarnessDeferredOption) UnmarshalJSON(data []byte) error {
	var enabled bool
	if err := json.Unmarshal(data, &enabled); err == nil {
		*option = AgentHarnessDeferredOption{Enabled: enabled}
		return nil
	}
	var object struct {
		Window string `json:"window"`
	}
	if err := json.Unmarshal(data, &object); err != nil {
		return fmt.Errorf("deferred option: %w", err)
	}
	*option = AgentHarnessDeferredOption{Object: true, Window: object.Window}
	return nil
}

// AgentHarnessStreamOptions are the curated provider request options owned by
// the harness and snapshotted per turn.
type AgentHarnessStreamOptions struct {
	Transport       ai.Transport      `json:"transport,omitempty"`
	TimeoutMs       *int              `json:"timeoutMs,omitempty"`
	MaxRetries      *int              `json:"maxRetries,omitempty"`
	MaxRetryDelayMs *int              `json:"maxRetryDelayMs,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	Metadata        map[string]any    `json:"metadata,omitempty"`
	// CacheRetention is "none", "short", "long", or empty when absent.
	CacheRetention string                      `json:"cacheRetention,omitempty"`
	Deferred       *AgentHarnessDeferredOption `json:"deferred,omitempty"`
}

// CompactionSettings configures automatic compaction. It lives here rather than
// in the compaction package because session operation state embeds it and the
// compaction package depends on session entries.
type CompactionSettings struct {
	Enabled          bool `json:"enabled"`
	ReserveTokens    int  `json:"reserveTokens"`
	KeepRecentTokens int  `json:"keepRecentTokens"`
}

// FileKind is the kind of addressed filesystem object; symlinks are not
// followed automatically.
type FileKind string

const (
	FileKindFile      FileKind = "file"
	FileKindDirectory FileKind = "directory"
	FileKindSymlink   FileKind = "symlink"
)

// FileErrorCode is a stable, backend-independent file error code.
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

// FileError is returned by FileSystem operations.
type FileError struct {
	Code    FileErrorCode
	Message string
	// Path is the absolute addressed path associated with the failure, when
	// available.
	Path  string
	Cause error
}

func (err *FileError) Error() string { return err.Message }
func (err *FileError) Unwrap() error { return err.Cause }

// ExecutionErrorCode is a stable, backend-independent execution error code.
type ExecutionErrorCode string

const (
	ExecutionErrorAborted          ExecutionErrorCode = "aborted"
	ExecutionErrorTimeout          ExecutionErrorCode = "timeout"
	ExecutionErrorShellUnavailable ExecutionErrorCode = "shell_unavailable"
	ExecutionErrorSpawnError       ExecutionErrorCode = "spawn_error"
	ExecutionErrorCallbackError    ExecutionErrorCode = "callback_error"
	ExecutionErrorUnknown          ExecutionErrorCode = "unknown"
)

// ExecutionError is returned by Shell.Exec.
type ExecutionError struct {
	Code    ExecutionErrorCode
	Message string
	Cause   error
}

func (err *ExecutionError) Error() string { return err.Message }
func (err *ExecutionError) Unwrap() error { return err.Cause }

// CompactionErrorCode is a stable compaction error code.
type CompactionErrorCode string

const (
	CompactionErrorAborted             CompactionErrorCode = "aborted"
	CompactionErrorSummarizationFailed CompactionErrorCode = "summarization_failed"
)

// CompactionError is returned by compaction helpers.
type CompactionError struct {
	Code    CompactionErrorCode
	Message string
	Cause   error
}

func (err *CompactionError) Error() string { return err.Message }
func (err *CompactionError) Unwrap() error { return err.Cause }

// BranchSummaryErrorCode is a stable branch-summary error code.
type BranchSummaryErrorCode string

const (
	BranchSummaryErrorAborted             BranchSummaryErrorCode = "aborted"
	BranchSummaryErrorSummarizationFailed BranchSummaryErrorCode = "summarization_failed"
)

// BranchSummaryError is returned by branch summarization helpers.
type BranchSummaryError struct {
	Code    BranchSummaryErrorCode
	Message string
	Cause   error
}

func (err *BranchSummaryError) Error() string { return err.Message }
func (err *BranchSummaryError) Unwrap() error { return err.Cause }

// FileInfo is metadata for one filesystem object.
type FileInfo struct {
	// Name is the basename of Path.
	Name string `json:"name"`
	// Path is the absolute, syntactically normalized addressed path; symlinks
	// are not followed.
	Path string   `json:"path"`
	Kind FileKind `json:"kind"`
	Size int64    `json:"size"`
	// MtimeMs is the modification time in milliseconds since the Unix epoch.
	MtimeMs float64 `json:"mtimeMs"`
}

// TextLine is one UTF-8 line read from a text file.
type TextLine struct {
	Text string
	// Terminated reports whether the line ended with "\n"; callers use it to
	// discard a torn final record.
	Terminated bool
}

// TextLineReader is a pull-based UTF-8 line reader that preserves final-line
// termination.
type TextLineReader interface {
	// ReadLine returns the next line, or nil at end of file.
	ReadLine(ctx context.Context) (*TextLine, error)
	// Close releases the open file; it is best-effort and never fails.
	Close(ctx context.Context)
}

// ReadTextLinesOptions bounds FileSystem.ReadTextLines.
type ReadTextLinesOptions struct {
	// MaxLines stops reading after this many lines when positive.
	MaxLines *int
}

// CreateDirOptions configures FileSystem.CreateDir; Recursive defaults to
// true when nil.
type CreateDirOptions struct {
	Recursive *bool
}

// RemoveOptions configures FileSystem.Remove; both default to false.
type RemoveOptions struct {
	Recursive bool
	Force     bool
}

// CreateTempFileOptions configures FileSystem.CreateTempFile.
type CreateTempFileOptions struct {
	Prefix string
	Suffix string
}

// FileSystem is the filesystem capability used by the harness. Paths may be
// absolute or relative to Cwd. Operations never panic; every failure,
// including unexpected backend failures, is a *FileError.
type FileSystem interface {
	Cwd() string
	AbsolutePath(ctx context.Context, path string) (string, error)
	JoinPath(ctx context.Context, parts []string) (string, error)
	ReadTextFile(ctx context.Context, path string) (string, error)
	OpenTextLineReader(ctx context.Context, path string) (TextLineReader, error)
	ReadTextLines(ctx context.Context, path string, options *ReadTextLinesOptions) ([]string, error)
	ReadBinaryFile(ctx context.Context, path string) ([]byte, error)
	WriteFile(ctx context.Context, path string, content []byte) error
	AppendFile(ctx context.Context, path string, content []byte) error
	RenameFile(ctx context.Context, sourcePath, destinationPath string) error
	FileInfo(ctx context.Context, path string) (FileInfo, error)
	ListDir(ctx context.Context, path string) ([]FileInfo, error)
	CanonicalPath(ctx context.Context, path string) (string, error)
	Exists(ctx context.Context, path string) (bool, error)
	CreateDir(ctx context.Context, path string, options *CreateDirOptions) error
	Remove(ctx context.Context, path string, options *RemoveOptions) error
	// CreateTempDir defaults prefix to "tmp-" when nil.
	CreateTempDir(ctx context.Context, prefix *string) (string, error)
	CreateTempFile(ctx context.Context, options *CreateTempFileOptions) (string, error)
	// Cleanup releases resources; it is best-effort and never fails.
	Cleanup(ctx context.Context)
}

// ShellOutputRetention selects which portion of bounded output survives.
type ShellOutputRetention string

const (
	ShellOutputRetainHead ShellOutputRetention = "head"
	ShellOutputRetainTail ShellOutputRetention = "tail"
)

// ShellOutputLimits are the source-side limits for one combined output view.
type ShellOutputLimits struct {
	MaxBytes int `json:"maxBytes"`
	MaxLines int `json:"maxLines"`
	// Retain defaults to tail when empty.
	Retain ShellOutputRetention `json:"retain,omitempty"`
}

// ShellOutputCaptureOptions request bounded capture.
type ShellOutputCaptureOptions struct {
	Limits ShellOutputLimits `json:"limits"`
	// Spill preserves complete output in an environment-local file after the
	// limits are crossed.
	Spill bool `json:"spill,omitempty"`
}

// ShellOutputTruncation is truncation metadata without the retained text.
type ShellOutputTruncation struct {
	Truncated bool `json:"truncated"`
	// TruncatedBy is "lines", "bytes", or nil (JSON null) when not truncated.
	TruncatedBy           *string `json:"truncatedBy"`
	TotalLines            int     `json:"totalLines"`
	TotalBytes            int     `json:"totalBytes"`
	OutputLines           int     `json:"outputLines"`
	OutputBytes           int     `json:"outputBytes"`
	LastLinePartial       bool    `json:"lastLinePartial"`
	FirstLineExceedsLimit bool    `json:"firstLineExceedsLimit"`
	MaxLines              int     `json:"maxLines"`
	MaxBytes              int     `json:"maxBytes"`
}

// ShellOutputMetadata accompanies a bounded output view.
type ShellOutputMetadata struct {
	Truncation    ShellOutputTruncation `json:"truncation"`
	SpillPath     string                `json:"spillPath,omitempty"`
	LastLineBytes *int                  `json:"lastLineBytes,omitempty"`
}

// ShellOutputView is one complete bounded output view.
type ShellOutputView struct {
	Text string `json:"text"`
	ShellOutputMetadata
}

// Shell output update kinds.
const (
	ShellOutputUpdateReplace  = "replace"
	ShellOutputUpdateAppend   = "append"
	ShellOutputUpdateSlide    = "slide"
	ShellOutputUpdateMetadata = "metadata"
)

// ShellOutputUpdate is one incremental source-side change to a bounded view.
// Kind selects the populated fields: replace uses Output; append uses Text and
// Metadata; slide uses Drop, Text, and Metadata; metadata uses Metadata. Drop
// counts UTF-16 code units, as upstream string offsets do.
type ShellOutputUpdate struct {
	Kind     string
	Output   ShellOutputView
	Text     string
	Drop     int
	Metadata ShellOutputMetadata
}

// MarshalJSON emits the kind-specific upstream wire shape.
func (update ShellOutputUpdate) MarshalJSON() ([]byte, error) {
	switch update.Kind {
	case ShellOutputUpdateReplace:
		return json.Marshal(struct {
			Kind   string          `json:"kind"`
			Output ShellOutputView `json:"output"`
		}{update.Kind, update.Output})
	case ShellOutputUpdateAppend:
		return json.Marshal(struct {
			Kind     string              `json:"kind"`
			Text     string              `json:"text"`
			Metadata ShellOutputMetadata `json:"metadata"`
		}{update.Kind, update.Text, update.Metadata})
	case ShellOutputUpdateSlide:
		return json.Marshal(struct {
			Kind     string              `json:"kind"`
			Drop     int                 `json:"drop"`
			Text     string              `json:"text"`
			Metadata ShellOutputMetadata `json:"metadata"`
		}{update.Kind, update.Drop, update.Text, update.Metadata})
	case ShellOutputUpdateMetadata:
		return json.Marshal(struct {
			Kind     string              `json:"kind"`
			Metadata ShellOutputMetadata `json:"metadata"`
		}{update.Kind, update.Metadata})
	default:
		return nil, fmt.Errorf("unknown shell output update kind %q", update.Kind)
	}
}

// UnmarshalJSON decodes any update kind.
func (update *ShellOutputUpdate) UnmarshalJSON(data []byte) error {
	var wire struct {
		Kind     string              `json:"kind"`
		Output   ShellOutputView     `json:"output"`
		Text     string              `json:"text"`
		Drop     int                 `json:"drop"`
		Metadata ShellOutputMetadata `json:"metadata"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	switch wire.Kind {
	case ShellOutputUpdateReplace, ShellOutputUpdateAppend, ShellOutputUpdateSlide, ShellOutputUpdateMetadata:
	default:
		return fmt.Errorf("unknown shell output update kind %q", wire.Kind)
	}
	*update = ShellOutputUpdate(wire)
	return nil
}

// ShellExecResult is a bounded shell completion. Output text is delivered
// through ShellExecOptions.OnUpdate.
type ShellExecResult struct {
	ExitCode int `json:"exitCode"`
	ShellOutputMetadata
}

// ShellExecOptions configures Shell.Exec.
type ShellExecOptions struct {
	// Cwd defaults to the environment cwd; relative paths resolve against it.
	Cwd string
	// Env values override inherited defaults when InheritEnv is not false.
	Env map[string]string
	// InheritEnv defaults to true when nil.
	InheritEnv *bool
	// Timeout is in seconds; nil means no timeout.
	Timeout *float64
	// Capture requests bounded capture. Output is discarded when Capture and
	// OnUpdate are both nil.
	Capture *ShellOutputCaptureOptions
	// OnUpdate receives bounded output changes. A returned error stops the
	// command and fails Exec with a callback_error, as a throwing upstream
	// callback does.
	OnUpdate func(ctx context.Context, update ShellOutputUpdate) error
}

// Shell is the shell execution capability used by the harness. Every failure
// is an *ExecutionError.
type Shell interface {
	// Exec runs command in the environment cwd unless options.Cwd is set.
	Exec(ctx context.Context, command string, options *ShellExecOptions) (ShellExecResult, error)
	// Cleanup releases resources; it is best-effort and never fails.
	Cleanup(ctx context.Context)
}

// ExecutionEnv is the filesystem and process execution environment used by
// the harness.
type ExecutionEnv interface {
	FileSystem
	Shell
}
