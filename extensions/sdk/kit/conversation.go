package kit

// The conversation kinds are Pi's conversation components, which Pi exports
// to extensions: a user message, an assistant message with its thinking, a
// tool call's card, a `!` command and a colored diff. The host draws each with
// the port PiG's main transcript uses, and a frontend draws them as it draws
// the transcript. A node with a non-empty ID keeps its host component across
// frames while its constructor arguments stay the same, as a Pi author keeps a
// component and calls its update methods; then a field that differs from the
// value sent last applies as that method. See
// docs/plan/extension-component-kit.md §2.1.

// UserMessage is upstream UserMessageComponent(text, getMarkdownTheme(),
// outputPad): the user's Markdown on the user message background.
type UserMessage struct {
	Text string
	// OutputPad is 0 or 1, the values of Pi's outputPad setting.
	OutputPad int
}

// NewUserMessage returns a UserMessage with upstream's outputPad 1.
func NewUserMessage(text string) *UserMessage { return &UserMessage{Text: text, OutputPad: 1} }

// SetOutputPad is upstream setOutputPad.
func (m *UserMessage) SetOutputPad(padding int) { m.OutputPad = padding }

// ContentBlock is a content block of an assistant message: [TextBlock],
// [ThinkingBlock] or [ToolCallBlock].
type ContentBlock struct {
	Type     string
	Text     string
	Thinking string
}

// TextBlock is a text content block.
func TextBlock(text string) ContentBlock { return ContentBlock{Type: "text", Text: text} }

// ThinkingBlock is a thinking content block.
func ThinkingBlock(thinking string) ContentBlock {
	return ContentBlock{Type: "thinking", Thinking: thinking}
}

// ToolCallBlock is a tool call content block. The component draws none; it
// separates thinking runs and leaves abort and error lines to the tool
// cards.
func ToolCallBlock() ContentBlock { return ContentBlock{Type: "toolCall"} }

// Message is the part of upstream AssistantMessage the component draws.
// StopReason is "stop" (also ""), "length", "toolUse", "error" or "aborted".
type Message struct {
	Content      []ContentBlock
	StopReason   string
	ErrorMessage string
}

// AssistantMessage is upstream AssistantMessageComponent(message,
// hideThinkingBlock, getMarkdownTheme(), hiddenThinkingLabel, outputPad):
// text blocks as Markdown, thinking runs in the thinking color (or the
// hidden label), and a length, abort or error line. A click on a thinking
// run toggles it on the host.
type AssistantMessage struct {
	// ID keeps the host component across frames.
	ID                  string
	Message             *Message
	HideThinkingBlock   bool
	HiddenThinkingLabel string
	OutputPad           int
	IsStreaming         bool
}

// NewAssistantMessage returns an AssistantMessage with upstream's defaults:
// thinking shown, the label "Thinking..." and outputPad 1. A nil message
// draws nothing.
func NewAssistantMessage(message *Message) *AssistantMessage {
	return &AssistantMessage{Message: message, HiddenThinkingLabel: "Thinking...", OutputPad: 1}
}

// UpdateContent is upstream updateContent(message, isStreaming).
func (m *AssistantMessage) UpdateContent(message Message, isStreaming bool) {
	m.Message, m.IsStreaming = &message, isStreaming
}

// SetHideThinkingBlock is upstream setHideThinkingBlock; on the host it also
// clears the runs a click toggled.
func (m *AssistantMessage) SetHideThinkingBlock(hide bool) { m.HideThinkingBlock = hide }

// SetHiddenThinkingLabel is upstream setHiddenThinkingLabel.
func (m *AssistantMessage) SetHiddenThinkingLabel(label string) { m.HiddenThinkingLabel = label }

// SetOutputPad is upstream setOutputPad (0 or 1).
func (m *AssistantMessage) SetOutputPad(padding int) { m.OutputPad = padding }

// ToolDefinition names the renderers of a [ToolExecution], since a tool
// definition is closures.
type ToolDefinition string

const (
	// ToolDefinitionBuiltin is the built-in tool's definition when the tool
	// name has one (read, bash, powershell, edit, write, grep, find, ls), and
	// a definition without renderers otherwise: the card the main transcript
	// draws for a tool without renderers of its own.
	ToolDefinitionBuiltin ToolDefinition = "builtin"
	// ToolDefinitionEmpty is a definition without renderers ({}): Pi's call
	// and result fallbacks for any name.
	ToolDefinitionEmpty ToolDefinition = "empty"
)

// ToolResultContent is a content block of a tool result: [TextContent] or
// [ImageContent].
type ToolResultContent struct {
	Text  string
	Image *Image
}

// TextContent is a text block of a tool result.
func TextContent(text string) ToolResultContent { return ToolResultContent{Text: text} }

// ImageContent is an image block of a tool result: the decoded bytes data.
// The SDK sends them as other kit images, once per connection.
func ImageContent(data []byte, mimeType string) ToolResultContent {
	return ToolResultContent{Image: NewImage(data, mimeType)}
}

// ToolResult is the result upstream updateResult receives. Details is any
// JSON value the built-in renderers read (an edit's diff, a read's
// truncation), or nil.
type ToolResult struct {
	Content []ToolResultContent
	IsError bool
	Details any
}

// ToolExecution is upstream ToolExecutionComponent(toolName, toolCallId,
// args, {showImages, imageWidthCells}, toolDefinition, ui, cwd) and its
// state: the tool card the main transcript draws. A click on a result
// toggles its expansion on the host.
type ToolExecution struct {
	// ID keeps the host component across frames, and with it the card's
	// renderer state, such as a shell command's elapsed time.
	ID              string
	ToolName        string
	ToolCallID      string
	Args            any
	ToolDefinition  ToolDefinition
	Cwd             string
	ShowImages      bool
	ImageWidthCells int

	ExecutionStarted bool
	ArgsComplete     bool
	Expanded         bool
	// Result is nil until a result arrives; IsPartial is true until it is
	// final.
	Result    *ToolResult
	IsPartial bool
}

// NewToolExecution returns a ToolExecution with upstream's defaults: the
// built-in definition, images shown 60 cells wide, and no result yet.
func NewToolExecution(toolName, toolCallID string, args any, cwd string) *ToolExecution {
	return &ToolExecution{ToolName: toolName, ToolCallID: toolCallID, Args: args, ToolDefinition: ToolDefinitionBuiltin, Cwd: cwd, ShowImages: true, ImageWidthCells: 60, IsPartial: true}
}

// UpdateArgs is upstream updateArgs.
func (t *ToolExecution) UpdateArgs(args any) { t.Args = args }

// MarkExecutionStarted is upstream markExecutionStarted.
func (t *ToolExecution) MarkExecutionStarted() { t.ExecutionStarted = true }

// SetArgsComplete is upstream setArgsComplete.
func (t *ToolExecution) SetArgsComplete() { t.ArgsComplete = true }

// UpdateResult is upstream updateResult(result, isPartial).
func (t *ToolExecution) UpdateResult(result ToolResult, isPartial bool) {
	t.Result, t.IsPartial = &result, isPartial
}

// SetExpanded is upstream setExpanded.
func (t *ToolExecution) SetExpanded(expanded bool) { t.Expanded = expanded }

// SetShowImages is upstream setShowImages.
func (t *ToolExecution) SetShowImages(show bool) { t.ShowImages = show }

// SetImageWidthCells is upstream setImageWidthCells: at least 1.
func (t *ToolExecution) SetImageWidthCells(width int) { t.ImageWidthCells = max(1, width) }

// BashComplete is upstream setComplete's arguments: the exit code (nil when
// unknown), whether the command was cancelled, whether its output was
// truncated, and where the full output is ("" for nowhere).
type BashComplete struct {
	ExitCode       *int
	Cancelled      bool
	Truncated      bool
	FullOutputPath string
}

// BashExecution is upstream BashExecutionComponent(command, ui,
// excludeFromContext): a `!` command between two borders, with its spinner
// while it runs (the host animates it) and its status after.
type BashExecution struct {
	// ID keeps the host component across frames.
	ID                 string
	Command            string
	ExcludeFromContext bool
	// Output is the output appended so far. The host cleans it as upstream
	// appendOutput does: no ANSI escapes, and "\r\n" and "\r" as "\n".
	Output   string
	Complete *BashComplete
	Expanded bool
}

// NewBashExecution returns a running BashExecution.
func NewBashExecution(command string, excludeFromContext bool) *BashExecution {
	return &BashExecution{Command: command, ExcludeFromContext: excludeFromContext}
}

// AppendOutput is upstream appendOutput.
func (b *BashExecution) AppendOutput(chunk string) { b.Output += chunk }

// SetComplete is upstream setComplete(exitCode, cancelled, truncated ?
// {truncated} : undefined, fullOutputPath).
func (b *BashExecution) SetComplete(exitCode *int, cancelled, truncated bool, fullOutputPath string) {
	b.Complete = &BashComplete{ExitCode: exitCode, Cancelled: cancelled, Truncated: truncated, FullOutputPath: fullOutputPath}
}

// SetExpanded is upstream setExpanded.
func (b *BashExecution) SetExpanded(expanded bool) { b.Expanded = expanded }

// Diff is upstream renderDiff(diff, {filePath}) drawn in a
// Text(…, paddingX, paddingY), as Pi's edit renderer draws it: context,
// removed and added lines in the diff colors, with a changed word inverted.
type Diff struct {
	Diff               string
	FilePath           string
	PaddingX, PaddingY int
}

// NewDiff returns a Diff without padding.
func NewDiff(diff string) *Diff { return &Diff{Diff: diff} }

func (*UserMessage) isNode()      {}
func (*AssistantMessage) isNode() {}
func (*ToolExecution) isNode()    {}
func (*BashExecution) isNode()    {}
func (*Diff) isNode()             {}

func (*UserMessage) isStackChild()      {}
func (*AssistantMessage) isStackChild() {}
func (*ToolExecution) isStackChild()    {}
func (*BashExecution) isStackChild()    {}
func (*Diff) isStackChild()             {}
