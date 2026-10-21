package kit

// CompactionSummary is coding-agent's CompactionSummaryMessageComponent(message,
// getMarkdownTheme(), outputPad): the marker of a compaction in the transcript
// ("Compacted from 12,345 tokens"), with the summary's markdown behind a click
// or [Expanded]. It draws with the active theme, and a node with an ID keeps
// its host component across frames, as [BranchSummary] does.
type CompactionSummary struct {
	ID string
	// Summary is the compaction's markdown summary (message.summary).
	Summary string
	// TokensBefore is the context size before compaction
	// (message.tokensBefore).
	TokensBefore int
	// OutputPad is the horizontal padding; upstream's outputPad setting is 0
	// or 1.
	OutputPad int
	// Expanded is setExpanded's argument.
	Expanded bool
}

// NewCompactionSummary returns a CompactionSummary with upstream's outputPad
// 1.
func NewCompactionSummary(summary string, tokensBefore int) *CompactionSummary {
	return &CompactionSummary{Summary: summary, TokensBefore: tokensBefore, OutputPad: 1}
}

// SetExpanded is upstream setExpanded.
func (c *CompactionSummary) SetExpanded(expanded bool) { c.Expanded = expanded }

// SetOutputPad is upstream setOutputPad.
func (c *CompactionSummary) SetOutputPad(outputPad int) { c.OutputPad = outputPad }

func (*CompactionSummary) isNode() {}

// WireNode implements [Wired].
func (c *CompactionSummary) WireNode() WireForm {
	tokens := c.TokensBefore
	return WireForm{Kind: "compaction-summary", ID: c.ID, Fields: messageFields{Summary: c.Summary, TokensBefore: &tokens, OutputPad: padPtr(c.OutputPad), Expanded: c.Expanded}}
}
