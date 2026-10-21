// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver

import (
	"context"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable/core"
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
	"github.com/MichaelKinsy/PiG/durable/core/store"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/internal/schemabackup"
)

// OpenHarness opens the Durable core as a native Harness over the SQLite store at path (":memory:" or a file; a file written
// by pi-durable opens as it is, CONTRACT section 3). A file at an older schema version is copied to
// <path>.schema-<N>.bak before the core upgrades it (D188). It is the D17 counterpart of harness.OpenHarness: the same
// options and the same harness.Harness, with one owner goroutine holding the core and the connection, and the store
// closed with the Harness. The registry may keep changing while the Harness runs.
func OpenHarness(ctx context.Context, path string, options harness.HarnessOptions) (harness.Harness, error) {
	return openHarness(ctx, path, options, nil)
}

func openHarness(ctx context.Context, path string, options harness.HarnessOptions, onCommit func(abi.Commit, []string)) (harness.Harness, error) {
	// pig divergence (D188): a store an older Pi release wrote is copied before the core upgrades its schema.
	if _, err := schemabackup.BeforeUpgrade(ctx, path, "durable_schema", store.SupportedSchemaVersion); err != nil {
		return nil, err
	}
	db, err := sqlhost.OpenDB(ctx, path)
	if err != nil {
		return nil, err
	}
	uuid := func() string { id, _ := ai.UUIDv7(nil); return id }
	s, err := open(ctx, SessionConfig{Core: core.NewSession(uuid), SQL: core.SQLTable(), DB: db, Harness: options, onCommit: onCommit})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}
