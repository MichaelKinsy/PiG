// Copyright 2026 The Sqlite Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sqlite

import (
	"context"
	"strings"
	"testing"

	sqlite3 "modernc.org/sqlite/lib"
)

// connectionMutex reports whether SQLite gave the connection opened with dsn a per-connection mutex: sqlite3_db_mutex
// returns NULL for a connection opened SQLITE_OPEN_NOMUTEX.
func connectionMutex(t *testing.T, dsn string) bool {
	t.Helper()
	connector, err := NewConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := connector.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = opened.Close() }()
	c := opened.(*conn)
	return sqlite3.Xsqlite3_db_mutex(c.tls, c.db) != 0
}

func TestThreadingModeSelectsTheMutexMode(t *testing.T) {
	for _, test := range []struct {
		dsn   string
		mutex bool
	}{
		{":memory:", true},
		{":memory:?_threading_mode=serialized", true},
		{":memory:?_threading_mode=multi-thread", false},
		{"file::memory:?_threading_mode=multi-thread", false},
	} {
		if got := connectionMutex(t, test.dsn); got != test.mutex {
			t.Errorf("%q: per-connection mutex = %v, want %v", test.dsn, got, test.mutex)
		}
	}
}

func TestThreadingModeRejectsBadValues(t *testing.T) {
	for dsn, want := range map[string]string{
		":memory:?_threading_mode=single-thread":                                "invalid _threading_mode",
		":memory:?_threading_mode=":                                             "invalid _threading_mode",
		":memory:?_threading_mode=Serialized":                                   "invalid _threading_mode",
		":memory:?_threading_mode=serialized&_threading_mode=multi-thread":      "exactly once",
		"file::memory:?_threading_mode=multi-thread&_threading_mode=serialized": "exactly once",
	} {
		connector, err := NewConnector(dsn)
		if err == nil {
			_, err = connector.Connect(context.Background())
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error = %v, want one containing %q", dsn, err, want)
		}
	}
}
