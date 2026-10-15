package codingagent

import (
	"reflect"
	"testing"
)

// RT-F14: a start whose recorded changelog version is this version, or that records the first one, shows no What's New, so it must not parse the bundled CHANGELOG.md.
func TestStartupChangelogParsesOnlyForAnUpdate(t *testing.T) {
	if bundledChangelog() == "" {
		t.Skip("this build strips the changelog")
	}
	sm := NewInMemorySettingsManager(Settings{LastChangelogVersion: "0.2.0"})
	allocs := testing.AllocsPerRun(20, func() {
		if got := startupChangelog(sm, "0.2.0"); got != nil {
			t.Fatalf("startupChangelog(same version) = %v, want nil", got)
		}
	})
	if allocs > 2 {
		t.Fatalf("startupChangelog with the recorded version current allocates %.0f times, want at most 2 (no changelog parse)", allocs)
	}
}

// Parsing on demand changes no outcome: the entries shown and the version recorded match the eager parse for a first start, the recorded version, an update and a downgrade.
func TestStartupChangelogMatchesTheEagerParse(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	for _, tc := range []struct{ lastSeen, appVersion string }{
		{"", "0.2.0"}, {"0.2.0", "0.2.0"}, {"0.0.1", "999.0.0"}, {"999.0.0", "0.0.1"},
	} {
		lazy := NewInMemorySettingsManager(Settings{LastChangelogVersion: tc.lastSeen})
		eager := NewInMemorySettingsManager(Settings{LastChangelogVersion: tc.lastSeen})
		got := startupChangelog(lazy, tc.appVersion)
		want := recordChangelogVersionAndMaybeReportInstall(eager, tc.appVersion, ParseChangelog(bundledChangelog()))
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%+v: startupChangelog = %d entries, eager parse %d", tc, len(got), len(want))
		}
		if lazy.GetLastChangelogVersion() != eager.GetLastChangelogVersion() {
			t.Fatalf("%+v: recorded %q, eager parse records %q", tc, lazy.GetLastChangelogVersion(), eager.GetLastChangelogVersion())
		}
		if tc.lastSeen == "0.0.1" && len(got) == 0 {
			t.Fatalf("%+v: an update from 0.0.1 shows no entries; the case does not reach the parse", tc)
		}
	}
}
