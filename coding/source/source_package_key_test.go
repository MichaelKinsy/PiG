package source

import (
	"encoding/json"
	"testing"
)

// Pi b4b72a35 source-info.ts getSourcePackageKey: JSON.stringify({ source, scope }).
func TestGetSourcePackageKey(t *testing.T) {
	got := GetSourcePackageKey(`npm:@scope/pkg "x"`, "project")
	if want := `{"source":"npm:@scope/pkg \"x\"","scope":"project"}`; got != want {
		t.Fatalf("GetSourcePackageKey = %s, want %s", got, want)
	}
	// JSON.stringify writes U+2028 and U+2029 raw, where encoding/json escapes them.
	got = GetSourcePackageKey("./a\u2028b\u2029c", "project")
	if want := "{\"source\":\"./a\u2028b\u2029c\",\"scope\":\"project\"}"; got != want {
		t.Fatalf("GetSourcePackageKey = %q, want %q", got, want)
	}
}

// JSON.stringify leaves <, > and & unescaped.
func TestGetSourcePackageKeyDoesNotEscapeHTML(t *testing.T) {
	got := GetSourcePackageKey("git:a&b<c>", "user")
	if want := `{"source":"git:a&b<c>","scope":"user"}`; got != want {
		t.Fatalf("GetSourcePackageKey = %s, want %s", got, want)
	}
}

func TestSourceInfoCarriesChangelogPathOnlyWhenSet(t *testing.T) {
	if got := mustJSON(t, SourceInfo{Path: "p", Source: "s", Scope: "user", Origin: "package"}); got != `{"path":"p","source":"s","scope":"user","origin":"package"}` {
		t.Errorf("without a changelog: %s", got)
	}
	if got := mustJSON(t, SourceInfo{Path: "p", Source: "s", Scope: "user", Origin: "package", ChangelogPath: "/pkg/CHANGELOG.md"}); got != `{"path":"p","source":"s","scope":"user","origin":"package","changelogPath":"/pkg/CHANGELOG.md"}` {
		t.Errorf("with a changelog: %s", got)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
