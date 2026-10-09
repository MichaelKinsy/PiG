// Ports packages/durable/src/harness/define.ts.

package harness

import (
	"context"

	"github.com/MichaelKinsy/PiG/durable"
)

// DefineExtension types an extension. Upstream returns its argument; the Go registry compares extensions by pointer, so it returns a pointer to it.
//
//go:fix inline
func DefineExtension(extension durable.Extension) *durable.Extension {
	return new(extension)
}

// DefineTool types a tool; the Harness validates arguments against Parameters before Execute.
func DefineTool(tool durable.ToolRegistration) *durable.ToolRegistration {
	return new(tool)
}

// SectionOptions are the options of Section.
type SectionOptions struct {
	// Tag false renders the text unwrapped; nil leaves the section tagged.
	Tag *bool
}

// Section returns a prompt section; tagged unless options set Tag false. Without options, or with Tag nil, Tag stays unset, as upstream leaves the field absent when options?.tag is undefined (define.ts:20-26).
func Section(key string, render func(ctx context.Context, input durable.PromptInput) (*string, error), options ...SectionOptions) *durable.PromptSection {
	section := &durable.PromptSection{Key: key, Render: render}
	if len(options) > 0 && options[0].Tag != nil {
		section.Tag = new(*options[0].Tag)
	}
	return section
}

// Hook returns hook handlers for tasks with task's name. handlers is the task's hook handler set (for example GenerationHooks); unset members do not run. The handler set's type is the task's hook type, as upstream types handlers by HooksOf<K>.
func Hook[I, S, R, H any](task durable.Task[I, S, R, H], handlers H) durable.HookRegistration {
	return durable.HookRegistration{Task: task.AnyDefinition().Name, Handlers: handlers}
}

// WrapTool wraps the tool named like tool wherever the wrapping extension is selected.
func WrapTool(tool *durable.ToolRegistration, wrapper func(tool *durable.ToolRegistration) *durable.ToolRegistration) durable.Wrap {
	return durable.ToolWrap{Tool: tool.Name, Wrap: wrapper}
}

// WrapSection wraps the section key wherever the wrapping extension is selected.
func WrapSection(key string, wrapper func(section *durable.PromptSection) *durable.PromptSection) durable.Wrap {
	return durable.SectionWrap{Section: key, Wrap: wrapper}
}
