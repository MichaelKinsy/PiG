package app

import "testing"

// Ports files-index.ts `SpaceSummary`: a space that holds nothing summarizes as zero files, versions and bytes and no newest version,
// and a folder entry's kind is the one that names what it holds.
func TestSpaceSummaryOfAnEmptySpaceIsZero(t *testing.T) {
	summary := SpaceSummary{}.JSON()
	for _, key := range []string{"files", "versions", "bytes"} {
		if got, ok := summary.Value(key).(float64); !ok || got != 0 {
			t.Fatalf("%s = %v, want 0", key, summary.Value(key))
		}
	}
	if list, ok := summary.Value("newest").([]any); !ok || len(list) != 0 {
		t.Fatalf("newest = %v, want an empty list", summary.Value("newest"))
	}
}

func TestSessionInfoShowsEveryMemberOfTheListEntry(t *testing.T) {
	info := SessionInfo{ID: "i", Label: "l", Kind: SessionDevice, Principal: "p", Created: 1, LastUsed: 2}.JSON()
	for key, want := range map[string]any{"id": "i", "label": "l", "kind": "device", "principal": "p", "created": float64(1), "lastUsed": float64(2)} {
		if got := info.Value(key); got != want {
			t.Fatalf("%s = %v, want %v", key, got, want)
		}
	}
}

func TestEphemeralUserIsAUserPrincipal(t *testing.T) {
	principal := EphemeralUser{Kind: "user", ID: "x", Name: "n"}.Principal()
	if principal.Value("kind") != "user" || principal.Value("id") != "x" || principal.Value("name") != "n" {
		t.Fatalf("principal = %v", principal)
	}
}
