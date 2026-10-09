package codingagent

import "github.com/MichaelKinsy/PiG/tui"

// newToolCardForTest builds a card the way the pre-1.1.0 constructor did in these tests: the call header is the given preview text.
func newToolCardForTest(name, argsPreview string, options ...tui.ToolExecutionOptions) *tui.ToolExecutionComponent {
	var opts tui.ToolExecutionOptions
	if len(options) > 0 {
		opts = options[0]
	}
	c := tui.NewToolExecutionComponent(name, "", nil, opts, nil, nil, "")
	c.ArgsPreview = argsPreview
	return c
}
