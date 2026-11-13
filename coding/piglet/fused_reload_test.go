package piglet

import (
	"strings"
	"testing"
)

// build.fusedReload is a closed key (D31): absent or "source"; any other value is refused by the parser and the schema.
func TestBuildFusedReloadIsAClosedKey(t *testing.T) {
	absent, err := ParseBytes([]byte("name: x\nbuild:\n  extensionRealization: fused\n"))
	if err != nil || absent.Build.FusedReload != "" {
		t.Fatalf("absent: %v, %+v", err, absent)
	}
	selected, err := ParseBytes([]byte("name: x\nbuild:\n  fusedReload: source\n"))
	if err != nil || selected.Build.FusedReload != FusedReloadSource {
		t.Fatalf("source: %v, %+v", err, selected)
	}
	for _, value := range []string{"rebuild", "Source", "true", "subprocess"} {
		doc := []byte("name: x\nbuild:\n  fusedReload: " + value + "\n")
		if _, err := ParseBytes(doc); err == nil || !strings.Contains(err.Error(), "build.fusedReload") {
			t.Errorf("parser on %q: %v, want a build.fusedReload error", value, err)
		}
		if err := ValidateAgainstSchema(doc); err == nil {
			t.Errorf("schema accepts fusedReload: %s", value)
		}
	}
	if err := ValidateAgainstSchema([]byte("name: x\nfusedReload: source\n")); err == nil {
		t.Error("schema accepts fusedReload outside build")
	}
}
