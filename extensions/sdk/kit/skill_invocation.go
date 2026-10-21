package kit

// SkillInvocation is coding-agent's SkillInvocationMessageComponent(skillBlock,
// getMarkdownTheme(), outputPad): a skill's invocation in the transcript, one
// line collapsed and the skill's content behind a click or [Expanded]. It
// draws with the active theme, and a node with an ID keeps its host component
// across frames, as [BranchSummary] does.
type SkillInvocation struct {
	ID string
	// Name and Content are the skill block's name and content.
	Name    string
	Content string
	// OutputPad is the horizontal padding; upstream's outputPad setting is 0
	// or 1.
	OutputPad int
	// Expanded is setExpanded's argument.
	Expanded bool
}

// NewSkillInvocation returns a SkillInvocation with upstream's outputPad 1.
func NewSkillInvocation(name, content string) *SkillInvocation {
	return &SkillInvocation{Name: name, Content: content, OutputPad: 1}
}

// SetExpanded is upstream setExpanded.
func (s *SkillInvocation) SetExpanded(expanded bool) { s.Expanded = expanded }

// SetOutputPad is upstream setOutputPad.
func (s *SkillInvocation) SetOutputPad(outputPad int) { s.OutputPad = outputPad }

func (*SkillInvocation) isNode() {}

// WireNode implements [Wired].
func (s *SkillInvocation) WireNode() WireForm {
	return WireForm{Kind: "skill-invocation", ID: s.ID, Fields: messageFields{Skill: &skillFields{Name: s.Name, Content: s.Content}, OutputPad: padPtr(s.OutputPad), Expanded: s.Expanded}}
}
