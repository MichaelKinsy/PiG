package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Ports the observable contract of upstream trust-selector.ts (showTrustSelector):
// the selector presents the project-trust options, and the chosen option's
// decision is persisted to the trust store (or nothing is saved on cancel).
// trustHandler is the production /trust path; here it is driven with a fake
// ShowTrustSelector instead of the TUI.
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
				ShowTrustSelector: func(options TrustSelectorOptions) (TrustSelection, bool) {
					selectorShown = true
					if options.Cwd != cwd {
						t.Errorf("selector cwd %q, want %q", options.Cwd, cwd)
					}
					if c.pick == "" {
						return TrustSelection{}, false
					}
					for _, o := range GetProjectTrustOptions(options.Cwd, false) {
						if strings.HasPrefix(o.Label, c.pick) {
							selection := TrustSelection{Trusted: o.Trusted, Updates: o.Updates}
							options.OnSelect(selection)
							return selection, true
						}
					}
					t.Fatalf("no option with prefix %q", c.pick)
					return TrustSelection{}, false
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
		// ShowTrustSelector nil: no TUI selector available.
	}
	if err := trustHandler(sc); err != nil {
		t.Fatalf("trustHandler: %v", err)
	}
	if !strings.Contains(appended, "unavailable") {
		t.Fatalf("expected an unavailable notice, got %q", appended)
	}
}

func TestTrustHandlerSurfacesStorageFailures(t *testing.T) {
	// Pi interactive-mode.ts:5160,5168 does not discard trust store read/write errors.
	for _, phase := range []string{"read", "save"} {
		t.Run(phase, func(t *testing.T) {
			agentDir := t.TempDir()
			blockStore := func() {
				t.Helper()
				if err := os.Mkdir(filepath.Join(agentDir, "trust.json"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "read" {
				blockStore()
			}
			shown, status := false, false
			sc := &SlashContext{
				AgentDir:   agentDir,
				ShowStatus: func(string) { status = true },
				ShowTrustSelector: func(options TrustSelectorOptions) (TrustSelection, bool) {
					shown = true
					blockStore()
					option := GetProjectTrustOptions(options.Cwd, false)[0]
					selection := TrustSelection{Trusted: option.Trusted, Updates: option.Updates}
					options.OnSelect(selection)
					return selection, true
				},
			}
			if err := trustHandler(sc); err == nil || !strings.Contains(err.Error(), "trust.json") {
				t.Fatalf("%s error=%v, want actionable trust store failure", phase, err)
			}
			if shown != (phase == "save") || status {
				t.Fatalf("%s: shown=%v status=%v", phase, shown, status)
			}
		})
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
