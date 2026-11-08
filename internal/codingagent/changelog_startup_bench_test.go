//go:build !pig_strip_changelog

package codingagent

import "testing"

// BenchmarkStartupChangelogRecordedVersionAhead is the startup What's New check of a build older than the recorded version (a downgrade, or a
// settings file carrying a future version, as the Pi-Bolt harness writes): no entry is newer.
func BenchmarkStartupChangelogRecordedVersionAhead(b *testing.B) {
	sm := NewInMemorySettingsManager(Settings{LastChangelogVersion: "9999.0.0"})
	b.ReportAllocs()
	for b.Loop() {
		if got := startupChangelog(sm, "0.7.1"); got != nil {
			b.Fatalf("startupChangelog = %d entries, want none", len(got))
		}
	}
}
