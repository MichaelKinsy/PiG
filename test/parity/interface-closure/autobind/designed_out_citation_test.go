package main

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// A designed-out row the rules design out is derived again on every run, and a reviewed one is written by hand in reviewed-decisions.json,
// so a pin bump must re-review the hand text. The committed rows must cite the pinned Pi version, and only ai ModelsError.name and .stack
// may rest on the owner approval of 2026-10-01 (C5), which says not to widen it to any other row.
func TestDesignedOutRowsCiteThePinAndC5OnlyForModelsError(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	var mapping struct {
		Mappings []struct {
			ID          string `json:"id"`
			Disposition string `json:"disposition"`
			Rationale   string `json:"rationale"`
		} `json:"mappings"`
	}
	if err := readJSON(filepath.Join(root, "test/parity/interfaces", "mapping-v"+pigversion.UpstreamVersion+".json"), &mapping); err != nil {
		t.Fatal(err)
	}
	version := regexp.MustCompile(`\bPi (\d+\.\d+\.\d+(?:\+[0-9a-f]{8})?)`)
	designedOut := 0
	for _, row := range mapping.Mappings {
		if row.Disposition != "designed-out" {
			continue
		}
		designedOut++
		for _, match := range version.FindAllStringSubmatch(row.Rationale, -1) {
			if match[1] != pigversion.UpstreamVersion {
				t.Errorf("%s cites Pi %s, not the pinned %s: %q", row.ID, match[1], pigversion.UpstreamVersion, row.Rationale)
			}
		}
		if strings.Contains(row.Rationale, "owner approved on 2026-10-01") && !strings.Contains(row.ID, "ModelsError::property:") {
			t.Errorf("%s rests on the 2026-10-01 approval, which covers only ModelsError.name and .stack: %q", row.ID, row.Rationale)
		}
	}
	if designedOut == 0 {
		t.Fatal("the mapping has no designed-out row")
	}
}
