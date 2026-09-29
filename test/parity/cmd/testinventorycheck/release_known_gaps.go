package main

import (
	"fmt"
	"strings"
)

// validateKnownGap permits only reviewed, hash-bound open hot paths. The mapping remains the authority for missing cases; a deferral never changes its disposition or evidence.
func validateKnownGap(version string, entry policyEntry, disposition mappingEntry, openHotPath bool) error {
	if !openHotPath {
		return fmt.Errorf("release policy %q: stale 0.3.x known gap; retain hot-path and remove the deferral only when closure is reviewed", entry.Path)
	}
	missing := "Missing cases: test/parity/interfaces/test-mapping-v" + version + ".json#" + entry.Path
	if !strings.HasPrefix(entry.Rationale, "0.3.x known gap: ") ||
		!strings.Contains(entry.Rationale, "SCRUTINIZED:approved") ||
		!strings.Contains(entry.Rationale, "FOLLOWUP-"+entry.Area+";") ||
		!strings.HasSuffix(entry.Rationale, missing) || strings.TrimSpace(disposition.Rationale) == "" {
		return fmt.Errorf("release policy %q: 0.3.x known gap needs an explicit reason, approval, area follow-up and exact missing-case mapping reference", entry.Path)
	}
	return nil
}
