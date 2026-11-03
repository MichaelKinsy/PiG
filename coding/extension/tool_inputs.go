// The input types of upstream's bash, powershell, read, edit, write, grep and ls tools (core/tools/*.ts). The file declares no function body: a type has no
// statement to execute, and the JSON methods of these types live in tool_input_json.go.

package extension

import "encoding/json"

// BashToolInput is upstream's BashToolInput (bash.ts bashSchema).
type BashToolInput struct {
	Command string   `json:"command"`
	Timeout *float64 `json:"timeout,omitempty"`
	// Extra holds members the schema does not declare; upstream's input is a plain object that keeps them.
	Extra map[string]json.RawMessage `json:"-"`
}

// PowerShellToolInput is the bash input shape (upstream powershell.ts).
type PowerShellToolInput = BashToolInput

// ReadToolInput is upstream's ReadToolInput (read.ts readSchema).
type ReadToolInput struct {
	Path   string   `json:"path"`
	Offset *float64 `json:"offset,omitempty"`
	Limit  *float64 `json:"limit,omitempty"`
	// Extra holds members the schema does not declare; upstream's input is a plain object that keeps them.
	Extra map[string]json.RawMessage `json:"-"`
}

// EditToolInput is upstream's EditToolInput (edit.ts editSchema).
type EditToolInput struct {
	Path  string                `json:"path"`
	Edits []EditToolInputChange `json:"edits"`
	// Extra holds members the schema does not declare; upstream's input is a plain object that keeps them.
	Extra map[string]json.RawMessage `json:"-"`
}

// EditToolInputChange is one targeted replacement of an EditToolInput (edit.ts replaceEditSchema).
type EditToolInputChange struct {
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

// WriteToolInput is upstream's WriteToolInput (write.ts writeSchema).
type WriteToolInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	// Extra holds members the schema does not declare; upstream's input is a plain object that keeps them.
	Extra map[string]json.RawMessage `json:"-"`
}

// GrepToolInput is upstream's GrepToolInput (grep.ts grepSchema).
type GrepToolInput struct {
	Pattern    string   `json:"pattern"`
	Path       *string  `json:"path,omitempty"`
	Glob       *string  `json:"glob,omitempty"`
	IgnoreCase *bool    `json:"ignoreCase,omitempty"`
	Literal    *bool    `json:"literal,omitempty"`
	Context    *float64 `json:"context,omitempty"`
	Limit      *float64 `json:"limit,omitempty"`
	// Extra holds members the schema does not declare; upstream's input is a plain object that keeps them.
	Extra map[string]json.RawMessage `json:"-"`
}

// LsToolInput is upstream's LsToolInput (ls.ts lsSchema).
type LsToolInput struct {
	Path  *string  `json:"path,omitempty"`
	Limit *float64 `json:"limit,omitempty"`
	// Extra holds members the schema does not declare; upstream's input is a plain object that keeps them.
	Extra map[string]json.RawMessage `json:"-"`
}
