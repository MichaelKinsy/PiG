package codingagent

import (
	"os"
	"path/filepath"
	"testing"
)

// interactive-mode.ts:5823-5836 getLogoutProviderOptions lists listCredentials() in the order of auth.json's members and sorts by
// `a.name.localeCompare(b.name)`, which is stable: providers whose names compare equal (the two canonical forms of "é" do) keep the file's order.
// PiG read the credentials through a Go map, so equal names came out in a random order.
func TestLogoutProviderOptionsKeepTheAuthFileOrderOfEqualNames(t *testing.T) {
	composed, decomposed := "z\u00e9", "ze\u0301"
	for _, order := range [][2]string{{composed, decomposed}, {decomposed, composed}} {
		dir := t.TempDir()
		content := `{"` + order[0] + `":{"type":"api_key","key":"k1"},"` + order[1] + `":{"type":"api_key","key":"k2"},"anthropic":{"type":"api_key","key":"k3"}}`
		if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		m := &InteractiveMode{opts: InteractiveModeOptions{AgentDir: dir}}
		for attempt := range 30 {
			providers, err := m.getLogoutProviderOptions()
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, provider := range providers {
				if provider.ID != "anthropic" {
					got = append(got, provider.ID)
				}
			}
			if len(got) != 2 || got[0] != order[0] || got[1] != order[1] {
				t.Fatalf("attempt %d: equal-name providers = %q, want the auth.json order %q", attempt, got, order)
			}
		}
	}
}

// interactive-mode.ts:5836 lists the credentials with `{ signal: AbortSignal.timeout(15_000) }`, and with a signal auth-storage.ts:431-432
// readLatestData rethrows a failed reload instead of falling back to the last snapshot: an unreadable auth.json fails /logout's listing.
func TestLogoutProviderOptionsReportAnUnreadableAuthFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"anthropic":`), 0o600); err != nil {
		t.Fatal(err)
	}
	m := &InteractiveMode{opts: InteractiveModeOptions{AgentDir: dir}}
	if providers, err := m.getLogoutProviderOptions(); err == nil {
		t.Fatalf("logout providers = %v with no error, want the reload's error", providers)
	}
}
