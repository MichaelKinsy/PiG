package subprocess

import (
	"time"
)

// ReloadCellReport records one extension placement decision.
type ReloadCellReport struct {
	Key           string        // planner cell key
	Strategy      CellStrategy  // isolated, packed-go, packed-rust, packed-python
	Language      string        // runtime language ("go", "rust", "python", "")
	Extensions    []string      // member extension names (planner order)
	Hash          string        // packed cell artifact hash, empty for isolated
	BinaryPath    string        // resolved artifact path
	Cached        bool          // true if packed artifact was reused from cache
	BuildDuration time.Duration // cold-build duration; zero on cache hit / no build
	Replaced      bool          // true if cell replaced an existing one
	Quarantined   bool          // true if planner refused to pack due to quarantine
	Reason        string        // short human reason ("factory shared-ok", "isolated source", ...)
}

// pig additive (D21): ReloadReport exposes subprocess placement diagnostics.
// ReloadReport records the latest Reload operation.
type ReloadReport struct {
	StartedAt time.Time
	Duration  time.Duration
	Cells     []ReloadCellReport
	Removed   []string
	Error     string
	// Issues lists, as "<path>: Failed to load extension: <error>", the
	// extensions that failed to resolve, build, start, or register. They do
	// not fail the reload.
	Issues []string
}

func (h *Host) recordReloadReport(rep *ReloadReport) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if rep == nil {
		return
	}
	h.lastReload = rep
}

// LastReloadReport returns a copy of the latest reload report.
func (h *Host) LastReloadReport() *ReloadReport {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.lastReload == nil {
		return nil
	}
	cp := *h.lastReload
	cp.Cells = append([]ReloadCellReport(nil), h.lastReload.Cells...)
	for i := range cp.Cells {
		cp.Cells[i].Extensions = append([]string(nil), h.lastReload.Cells[i].Extensions...)
	}
	cp.Removed = append([]string(nil), h.lastReload.Removed...)
	cp.Issues = append([]string(nil), h.lastReload.Issues...)
	return &cp
}
