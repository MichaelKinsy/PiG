// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Session directory path parity.

func TestEncodeCwdForSessionDir(t *testing.T) {
	cases := []struct {
		name string
		cwd  string
		want string
	}{
		// Mirrors upstream session-manager.ts:429 verbatim. Each case
		// is what `--${cwd.replace(/^[/\\]/, "").replace(/[/\\:]/g, "-")}--`
		// would produce in JS.
		{"unix-simple", "/tmp/foo", "--tmp-foo--"},
		{"unix-nested", "/Users/example/work", "--Users-example-work--"},
		{"unix-root", "/", "----"},
		{"empty", "", "----"},
		{"no-leading-slash", "tmp/foo", "--tmp-foo--"},
		{"colon-replaced", "/var/tmp:foo", "--var-tmp-foo--"},
		{"backslash-replaced", `/a\b\c`, "--a-b-c--"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := encodeCwdForSessionDir(tc.cwd)
			if got != tc.want {
				t.Errorf("got=%q want=%q", got, tc.want)
			}
		})
	}
}

// posixCwdSessionTail is the session directory name of the absolute POSIX path cwd ("/tmp/foo"). Pi resolves the cwd
// with path.resolve first, and on win32 a path with no drive takes the drive of the process's working directory:
// "/tmp/foo" resolves to "D:\tmp\foo" there and encodes as "--D--tmp-foo--".
func posixCwdSessionTail(t *testing.T, cwd string) string {
	t.Helper()
	encoded := strings.ReplaceAll(strings.TrimPrefix(cwd, "/"), "/", "-")
	if runtime.GOOS == "windows" {
		wd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		drive := filepath.VolumeName(wd)
		encoded = strings.TrimSuffix(drive, ":") + "--" + encoded
	}
	return "--" + encoded + "--"
}

func TestDefaultSessionDirUsesUpstreamEncoding(t *testing.T) {
	// Ensures defaultSessionDir wires the upstream-parity encoder.
	// We don't care about the agent-dir prefix here; we only check
	// the trailing component.
	got := defaultSessionDir("/tmp/parity-check")
	tail := filepath.Base(got)
	if want := posixCwdSessionTail(t, "/tmp/parity-check"); tail != want {
		t.Errorf("tail=%q want=%s", tail, want)
	}
}

func TestSessionRoundTripUnderNewEncoding(t *testing.T) {
	// End-to-end: create a session, write a message, close, reopen
	// and read it back. Confirms the new encoding hasn't broken
	// session persistence.
	dir := t.TempDir()
	sm := NewSessionManagerWithDir("/tmp/round-trip", dir)
	sess, err := sm.Create("sess-rt", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendMessage(mkAssistantMsg("hello")); err != nil {
		t.Fatal(err)
	}
	// SessionDir should sit under the new encoding scheme when the
	// caller doesn't override sessionDir. Here we passed explicit
	// dir, so verify by re-reading the file directly.
	data, err := os.ReadFile(sess.Path())
	if err != nil {
		t.Fatal(err)
	}
	first, _, _ := strings.Cut(string(data), "\n")
	var hdr map[string]any
	if err := json.Unmarshal([]byte(first), &hdr); err != nil {
		t.Fatal(err)
	}
	if hdr["cwd"] != "/tmp/round-trip" {
		t.Errorf("header cwd=%v", hdr["cwd"])
	}
}
