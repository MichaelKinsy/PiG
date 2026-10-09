//go:build !pig_strip_changelog

package codingagent

import (
	"strings"
	"testing"
)

func TestChangelogHandler_AppendsDevelopmentBaseline(t *testing.T) {
	sc, out := newFakeSlashCtx()
	if err := changelogHandler(sc); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "**What's New**") || !strings.Contains(got, "[0.0.0]") {
		t.Errorf("expected versioned development baseline: %q", got[:minClampInt(120, len(got))])
	}
}

func TestChangelogHandler_IgnoresCollapseSetting(t *testing.T) {
	sc, out := newFakeSlashCtx()
	sc.SettingsManager = &SettingsManager{merged: Settings{CollapseChangelog: true}}
	if err := changelogHandler(sc); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "**What's New**") || !strings.Contains(got, "[0.0.0]") {
		t.Fatalf("expected full development changelog block: %q", got[:minClampInt(120, len(got))])
	}
	if strings.Contains(got, "Updated to v") {
		t.Fatalf("/changelog should ignore collapse setting, got condensed text: %q", got)
	}
}
