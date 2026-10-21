package tui

// view_widget_state.go: read access to the state the extension component
// kit reports to a frontend and compares against the state its sender sent
// (D107, docs/plan/extension-component-kit.md §2.2). Pi's components expose
// this state as public fields or have none; the Go ports keep it private.

// Expanded reports the component's expanded state, which a click toggles.
func (c *BranchSummaryMessageComponent) Expanded() bool { return c.expanded }

// Expanded reports the component's expanded state, which a click toggles.
func (c *CompactionSummaryMessageComponent) Expanded() bool { return c.expanded }

// Expanded reports the component's expanded state, which a click toggles.
func (s *SkillInvocationMessageComponent) Expanded() bool { return s.expanded }

// Expanded reports the component's expanded state (upstream _expanded).
func (c *CustomMessageComponent) Expanded() bool { return c.expanded }

// Cursor is the cursor's UTF-16 offset in the value.
func (t *TextInput) Cursor() int { return t.cursor }

// Loader returns the loader the bordered loader draws.
func (bl *BorderedLoader) Loader() *Loader { return bl.loader }
