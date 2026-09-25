package extension

import "testing"

// TestContext_Mode_DefaultsToPrint verifies an unset mode normalizes to
// ModePrint, matching upstream's `private mode: ExtensionMode = "print"`
// (runner.ts:229). Fails if normalize() drops the default.
func TestContext_Mode_DefaultsToPrint(t *testing.T) {
	c := NewContext("/work", nil, func() error { return nil }, ContextActions{})
	got, err := c.Mode()
	if err != nil {
		t.Fatalf("Mode() error = %v", err)
	}
	if got != ModePrint {
		t.Fatalf("Mode() = %q, want %q (unset normalizes to print)", got, ModePrint)
	}
}

// TestContext_Mode_ReportsInjected verifies a bound mode is returned verbatim.
func TestContext_Mode_ReportsInjected(t *testing.T) {
	c := NewContext("/work", nil, func() error { return nil }, ContextActions{Mode: ModeTUI})
	got, err := c.Mode()
	if err != nil {
		t.Fatalf("Mode() error = %v", err)
	}
	if got != ModeTUI {
		t.Fatalf("Mode() = %q, want %q", got, ModeTUI)
	}
}

// TestCommandContext_GetSystemPromptOptions_DefaultCwd verifies the nil-source
// fallback returns zero-value options carrying the runner cwd, matching
// upstream `getSystemPromptOptions ?? (() => ({ cwd: this.cwd }))`.
func TestCommandContext_GetSystemPromptOptions_DefaultCwd(t *testing.T) {
	base := NewContext("/runner/cwd", nil, func() error { return nil }, ContextActions{})
	cc := NewCommandContext(base, CommandActions{})
	got, err := cc.GetSystemPromptOptions()
	if err != nil {
		t.Fatalf("GetSystemPromptOptions() error = %v", err)
	}
	if got.Cwd != "/runner/cwd" {
		t.Fatalf("default options cwd = %q, want %q", got.Cwd, "/runner/cwd")
	}
	if got.CustomPrompt != "" || len(got.SelectedTools) != 0 {
		t.Fatalf("default options should be empty besides cwd, got %+v", got)
	}
}

// TestCommandContext_GetSystemPromptOptions_ReturnsSource verifies the wired
// source is returned verbatim (the base prompt inputs an extension inspects).
func TestCommandContext_GetSystemPromptOptions_ReturnsSource(t *testing.T) {
	want := BuildSystemPromptOptions{
		CustomPrompt:  "custom",
		SelectedTools: []string{"read", "bash"},
		Cwd:           "/probe",
	}
	base := NewContext("/runner/cwd", nil, func() error { return nil },
		ContextActions{GetSystemPromptOptions: func() BuildSystemPromptOptions { return want }})
	cc := NewCommandContext(base, CommandActions{})
	got, err := cc.GetSystemPromptOptions()
	if err != nil {
		t.Fatalf("GetSystemPromptOptions() error = %v", err)
	}
	if got.CustomPrompt != want.CustomPrompt || got.Cwd != want.Cwd || len(got.SelectedTools) != 2 {
		t.Fatalf("GetSystemPromptOptions() = %+v, want %+v", got, want)
	}
}

func TestCommandContext_SendUserMessage_CallsBoundHandler(t *testing.T) {
	var gotContent any
	var gotOpts *SendUserMessageOptions
	base := NewContext("/runner/cwd", nil, func() error { return nil }, ContextActions{
		SendUserMessage: func(content any, opts *SendUserMessageOptions) error {
			gotContent = content
			gotOpts = opts
			return nil
		},
	})
	cc := NewCommandContext(base, CommandActions{})

	opts := &SendUserMessageOptions{DeliverAs: DeliverAsFollowUp}
	if err := cc.SendUserMessage("hello", opts); err != nil {
		t.Fatalf("SendUserMessage() error = %v", err)
	}
	if gotContent != "hello" || gotOpts != opts {
		t.Fatalf("handler got (%#v, %#v), want (hello, opts)", gotContent, gotOpts)
	}
}
