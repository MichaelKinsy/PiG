// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package storehost

import (
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/store"
)

// ABI section 3, invariant 3: the host applies a read step's commits before it runs the reads. Cold open's step commits
// CREATE TABLE IF NOT EXISTS durable_schema and reads the version; on a new file the read fails when the commit has not run.
// A reopen goes through the same step on a store that has the table.
func TestRunAppliesAReadStepsCommitsFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.sqlite")
	for _, round := range []string{"new", "reopen"} {
		h, err := Open(path, nil)
		if err != nil {
			t.Fatal(err)
		}
		st := store.New(store.Options{EmptyNew: true})
		var live *store.Live
		if err := h.Run(st, func() (err error) { live, err = st.Open(h.B); return }); err != nil {
			t.Fatalf("%s: %v", round, err)
		}
		if live == nil || live.NextSeq != 1 {
			t.Fatalf("%s: live %+v", round, live)
		}
		var version int
		if err := h.db.QueryRow("SELECT version FROM durable_schema WHERE singleton = 1").Scan(&version); err != nil || version != store.SupportedSchemaVersion {
			t.Fatalf("%s: schema version %d, %v", round, version, err)
		}
		if err := h.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
