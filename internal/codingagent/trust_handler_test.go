package codingagent

import (
	"os"
	"strings"
	"testing"
)

// Ports the observable contract of upstream trust-selector.ts (showTrustSelector):
// the selector presents the project-trust options, and the chosen option's
// decision is persisted to the trust store (or nothing is saved on cancel).
// trustHandler is the production /trust path; here it is driven with a fake
// ShowExtensionSelector instead of the TUI.
func TestTrustHandler_SavesSelectedDecision(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	cases := []struct {
		name       string
		pick       string // label prefix to select; "" cancels
		wantSaved  *bool  // expected trust store Get result for cwd
		wantStatus string // substring of the status line, "" = no status
	}{
		{"trust", "Trust", new(true), "decision: trusted"},
		{"do not trust", "Do not trust", new(false), "decision: untrusted"},
		{"cancel saves nothing", "", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			agentDir := t.TempDir()
			var status string
			var selectorShown bool
			sc := &SlashContext{
				AgentDir:   agentDir,
				ShowStatus: func(m string) { status = m },
				ShowExtensionSelector: func(title string, options []string, _ string) (string, bool) {
					selectorShown = true
					if !strings.Contains(title, cwd) {
						t.Errorf("selector title %q does not name cwd %q", title, cwd)
					}
					if c.pick == "" {
						return "", false
					}
					for _, o := range options {
						if strings.HasPrefix(o, c.pick) {
							return o, true
						}
					}
					t.Fatalf("no option with prefix %q in %v", c.pick, options)
					return "", false
				},
			}

			if err := trustHandler(sc); err != nil {
				t.Fatalf("trustHandler: %v", err)
			}
			if !selectorShown {
				t.Fatal("trust selector was never shown")
			}

			got, err := NewProjectTrustStore(agentDir).Get(cwd)
			if err != nil {
				t.Fatalf("trust store Get: %v", err)
			}
			if !boolPtrSame(got, c.wantSaved) {
				t.Fatalf("saved decision = %v, want %v", derefBool(got), derefBool(c.wantSaved))
			}
			if c.wantStatus == "" {
				if status != "" {
					t.Fatalf("expected no status on cancel, got %q", status)
				}
			} else if !strings.Contains(status, c.wantStatus) {
				t.Fatalf("status = %q, want substring %q", status, c.wantStatus)
			}
		})
	}
}

func TestTrustHandler_UnavailableSelectorIsGraceful(t *testing.T) {
	var appended string
	sc := &SlashContext{
		AgentDir: t.TempDir(),
		Append:   func(s string) { appended += s },
		// ShowExtensionSelector nil: no TUI selector available.
	}
	if err := trustHandler(sc); err != nil {
		t.Fatalf("trustHandler: %v", err)
	}
	if !strings.Contains(appended, "unavailable") {
		t.Fatalf("expected an unavailable notice, got %q", appended)
	}
}

func boolPtrSame(a, b *bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func derefBool(b *bool) any {
	if b == nil {
		return nil
	}
	return *b
}
