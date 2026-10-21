package kit

// BranchSummary is coding-agent's BranchSummaryMessageComponent(message,
// getMarkdownTheme(), outputPad): the marker of a summarized branch in the
// transcript, with the summary's markdown behind a click or [Expanded].
// Like Pi's conversation components it draws with the active theme.
//
// A node with an ID keeps its host component across frames, so a click's
// toggle lives as long as the node: a different [Expanded] or OutputPad than
// this surface sent last applies through setExpanded and setOutputPad.
type BranchSummary struct {
	ID string
	// Summary is the LLM-generated markdown summary (message.summary).
	Summary string
	// OutputPad is the horizontal padding; upstream's outputPad setting is 0
	// or 1.
	OutputPad int
	// Expanded is setExpanded's argument.
	Expanded bool
}

// NewBranchSummary returns a BranchSummary with upstream's outputPad 1.
func NewBranchSummary(summary string) *BranchSummary {
	return &BranchSummary{Summary: summary, OutputPad: 1}
}

// SetExpanded is upstream setExpanded.
func (b *BranchSummary) SetExpanded(expanded bool) { b.Expanded = expanded }

// SetOutputPad is upstream setOutputPad.
func (b *BranchSummary) SetOutputPad(outputPad int) { b.OutputPad = outputPad }

func (*BranchSummary) isNode() {}

// WireNode implements [Wired].
func (b *BranchSummary) WireNode() WireForm {
	return WireForm{Kind: "branch-summary", ID: b.ID, Fields: messageFields{Summary: b.Summary, OutputPad: padPtr(b.OutputPad), Expanded: b.Expanded}}
}

// messageFields are the wire fields of the summary, custom and skill
// messages.
type messageFields struct {
	Summary      string       `json:"summary,omitempty"`
	TokensBefore *int         `json:"tokensBefore,omitempty"`
	Skill        *skillFields `json:"skill,omitempty"`
	CustomType   string       `json:"customType,omitempty"`
	Text         string       `json:"text,omitempty"`
	OutputPad    *int         `json:"outputPad,omitempty"`
	Expanded     bool         `json:"expanded,omitempty"`
}

type skillFields struct {
	Name    string `json:"name,omitempty"`
	Content string `json:"content,omitempty"`
}

// padPtr is outputPad on the wire: absent for upstream's default 1.
func padPtr(outputPad int) *int {
	if outputPad == 1 {
		return nil
	}
	return &outputPad
}
