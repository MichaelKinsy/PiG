// Copyright 2026 The Sqlite Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sqlite

import (
	"fmt"
	"net/url"

	sqlite3 "modernc.org/sqlite/lib"
)

// getThreadingMode validates the _threading_mode DSN query parameter and returns the SQLite open flag it selects:
// SQLITE_OPEN_FULLMUTEX for "serialized" (the default when absent) or SQLITE_OPEN_NOMUTEX for "multi-thread".
// See https://www.sqlite.org/threadsafe.html.
//
// Multi-thread mode drops SQLite's per-connection mutex, so one connection must never be used by two goroutines
// at the same time; the driver's own per-connection state is not safe for that either (see the package
// documentation). The parameter is single-valued and case-sensitive.
//
// modernc DSN nomutex PR (owner to post); draft: ~/PiG-launch-newer/handoffs/next/modernc-dsn-nomutex-pr.md
func getThreadingMode(query string) (int32, error) {
	q, err := url.ParseQuery(query)
	if err != nil {
		return 0, err
	}
	values, ok := q["_threading_mode"]
	if !ok {
		return sqlite3.SQLITE_OPEN_FULLMUTEX, nil
	}
	if len(values) != 1 {
		return 0, fmt.Errorf("_threading_mode must be specified exactly once, got %d values", len(values))
	}
	switch values[0] {
	case "serialized":
		return sqlite3.SQLITE_OPEN_FULLMUTEX, nil
	case "multi-thread":
		return sqlite3.SQLITE_OPEN_NOMUTEX, nil
	}
	return 0, fmt.Errorf("invalid _threading_mode %q, expecting one of: serialized multi-thread", values[0])
}
