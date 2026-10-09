package kitwire

import "encoding/json"

// The wire shapes of coding/extension/host/subprocess/protocol.go
// (ViewPayload, ViewNode and their parts), which this module cannot import.
// Field names, order and omission rules match the host's.

const (
	kindContainer     = "container"
	kindBox           = "box"
	kindText          = "text"
	kindTruncatedText = "truncated-text"
	kindMarkdown      = "markdown"
	kindSpacer        = "spacer"
	kindDynamicBorder = "dynamic-border"
	kindSelectList    = "select-list"
	kindSettingsList  = "settings-list"
	kindImage         = "image"
	kindLoader        = "loader"
	kindHStack        = "hstack"
	kindVStack        = "vstack"
	kindLines         = "lines"

	kindUserMessage      = "user-message"
	kindAssistantMessage = "assistant-message"
	kindToolExecution    = "tool-execution"
	kindBashExecution    = "bash-execution"
	kindDiff             = "diff"
)

type viewPayload struct {
	Root  viewNode          `json:"root"`
	Focus string            `json:"focus,omitempty"`
	Theme map[string]string `json:"theme,omitempty"`
}

type viewNode struct {
	Kind     string          `json:"kind"`
	ID       string          `json:"id,omitempty"`
	Children []viewNode      `json:"children,omitempty"`
	Stack    *viewStackEntry `json:"stack,omitempty"`

	Text             string            `json:"text,omitempty"`
	PaddingX         *int              `json:"paddingX,omitempty"`
	PaddingY         *int              `json:"paddingY,omitempty"`
	Bg               string            `json:"bg,omitempty"`
	Color            string            `json:"color,omitempty"`
	DefaultTextStyle *viewTextStyle    `json:"defaultTextStyle,omitempty"`
	RenderLatex      *bool             `json:"renderLatex,omitempty"`
	Lines            *int              `json:"lines,omitempty"`
	Items            []viewItem        `json:"items,omitempty"`
	MaxVisible       *int              `json:"maxVisible,omitempty"`
	Layout           *viewSelectLayout `json:"layout,omitempty"`
	SelectedIndex    *int              `json:"selectedIndex,omitempty"`
	Filter           *string           `json:"filter,omitempty"`
	EnableSearch     bool              `json:"enableSearch,omitempty"`
	Ref              string            `json:"ref,omitempty"`
	MimeType         string            `json:"mimeType,omitempty"`
	MaxWidthCells    *int              `json:"maxWidthCells,omitempty"`
	MaxHeightCells   *int              `json:"maxHeightCells,omitempty"`
	Filename         string            `json:"filename,omitempty"`
	FallbackColor    string            `json:"fallbackColor,omitempty"`
	// Message is a loader's message (a string) or an assistant-message's
	// message (a *viewAssistantMessage).
	Message      any                  `json:"message,omitempty"`
	SpinnerColor string               `json:"spinnerColor,omitempty"`
	MessageColor string               `json:"messageColor,omitempty"`
	Indicator    *viewLoaderIndicator `json:"indicator,omitempty"`
	Frame        *int                 `json:"frame,omitempty"`
	Gap          *int                 `json:"gap,omitempty"`
	Align        string               `json:"align,omitempty"`
	Content      []string             `json:"content,omitempty"`
	Image        *viewImageRef        `json:"image,omitempty"`
	Progress     *viewProgress        `json:"progress,omitempty"`
	List         *viewList            `json:"list,omitempty"`

	OutputPad           *int              `json:"outputPad,omitempty"`
	HideThinkingBlock   bool              `json:"hideThinkingBlock,omitempty"`
	HiddenThinkingLabel *string           `json:"hiddenThinkingLabel,omitempty"`
	IsStreaming         bool              `json:"isStreaming,omitempty"`
	ToolName            string            `json:"toolName,omitempty"`
	ToolCallID          string            `json:"toolCallId,omitempty"`
	Args                json.RawMessage   `json:"args,omitempty"`
	ToolDefinition      string            `json:"toolDefinition,omitempty"`
	Cwd                 string            `json:"cwd,omitempty"`
	ShowImages          *bool             `json:"showImages,omitempty"`
	ImageWidthCells     *int              `json:"imageWidthCells,omitempty"`
	ExecutionStarted    bool              `json:"executionStarted,omitempty"`
	ArgsComplete        bool              `json:"argsComplete,omitempty"`
	Expanded            bool              `json:"expanded,omitempty"`
	Result              *viewToolResult   `json:"result,omitempty"`
	IsPartial           *bool             `json:"isPartial,omitempty"`
	Command             string            `json:"command,omitempty"`
	ExcludeFromContext  bool              `json:"excludeFromContext,omitempty"`
	Output              string            `json:"output,omitempty"`
	Complete            *viewBashComplete `json:"complete,omitempty"`
	Diff                string            `json:"diff,omitempty"`
	FilePath            string            `json:"filePath,omitempty"`
}

type viewAssistantMessage struct {
	Content      []viewContentBlock `json:"content"`
	StopReason   string             `json:"stopReason,omitempty"`
	ErrorMessage string             `json:"errorMessage,omitempty"`
}

type viewContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Thinking string `json:"thinking,omitempty"`
	Ref      string `json:"ref,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

type viewToolResult struct {
	Content []viewContentBlock `json:"content"`
	IsError bool               `json:"isError,omitempty"`
	Details json.RawMessage    `json:"details,omitempty"`
}

type viewBashComplete struct {
	ExitCode       *int   `json:"exitCode,omitempty"`
	Cancelled      bool   `json:"cancelled,omitempty"`
	Truncated      bool   `json:"truncated,omitempty"`
	FullOutputPath string `json:"fullOutputPath,omitempty"`
}

type viewStackEntry struct {
	Basis   *int `json:"basis,omitempty"`
	Grow    *int `json:"grow,omitempty"`
	Shrink  *int `json:"shrink,omitempty"`
	MinSize *int `json:"minSize,omitempty"`
	MaxSize *int `json:"maxSize,omitempty"`
}

type viewTextStyle struct {
	Color         string `json:"color,omitempty"`
	BgColor       string `json:"bgColor,omitempty"`
	Bold          bool   `json:"bold,omitempty"`
	Italic        bool   `json:"italic,omitempty"`
	Strikethrough bool   `json:"strikethrough,omitempty"`
	Underline     bool   `json:"underline,omitempty"`
}

type viewItem struct {
	Value        string    `json:"value,omitempty"`
	ID           string    `json:"id,omitempty"`
	Label        string    `json:"label,omitempty"`
	Description  string    `json:"description,omitempty"`
	CurrentValue string    `json:"currentValue,omitempty"`
	Values       []string  `json:"values,omitempty"`
	Submenu      *viewNode `json:"submenu,omitempty"`
}

type viewSelectLayout struct {
	MinPrimaryColumnWidth *int `json:"minPrimaryColumnWidth,omitempty"`
	MaxPrimaryColumnWidth *int `json:"maxPrimaryColumnWidth,omitempty"`
}

type viewLoaderIndicator struct {
	Frames     *[]string `json:"frames,omitempty"`
	IntervalMs int       `json:"intervalMs,omitempty"`
}

// imageData is ViewImageData; Data marshals as standard base64.
type imageData struct {
	Ref      string `json:"ref"`
	MimeType string `json:"mimeType"`
	Data     []byte `json:"data"`
}

type viewImageRef struct {
	Ref string `json:"ref"`
}

type viewProgress struct {
	Value float64 `json:"value"`
	Max   float64 `json:"max"`
}

type viewList struct {
	Items         []viewListItem `json:"items"`
	SelectedIndex int            `json:"selectedIndex"`
}

type viewListItem struct {
	Label   string   `json:"label"`
	Detail  string   `json:"detail,omitempty"`
	Columns []string `json:"columns,omitempty"`
}
