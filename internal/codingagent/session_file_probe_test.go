// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package codingagent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionFileInteropProbe(t *testing.T) {
	dir := t.TempDir()
	path := fileOperationWrite(t, dir, "unterminated.jsonl", fileOperationHeader("/project", "existing")+fileOperationUser)
	records, err := LoadEntriesFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	repaired := strings.HasSuffix(string(data), "\n") && len(records) == 2
	if !repaired {
		t.Fatal("unterminated record not repaired")
	}
	manager := NewSessionManagerWithDir(dir, dir)
	session, err := manager.Open(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	overridden := session.CWD() == dir && session.Header().CWD == "/project"
	if !overridden {
		t.Fatal("cwd override changed stored identity")
	}
	empty := fileOperationWrite(t, dir, "empty.jsonl", "")
	initialized, err := manager.Open(empty)
	if err != nil {
		t.Fatal(err)
	}
	headerOnly := len(readJSONLLines(t, empty)) == 1 && initialized.Header().Type == "session" && initialized.ID() != ""
	if !headerOnly {
		t.Fatal("empty session did not initialize")
	}
	invalid := filepath.Join(dir, "not-a-session.log")
	original := "{\"type\":\"event\",\"data\":\"not a session\"}\n"
	if err := os.WriteFile(invalid, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = manager.Open(invalid)
	if err == nil || err.Error() != "Session file is not a valid pi session: "+invalid {
		t.Fatal(err)
	}
	data, err = os.ReadFile(invalid)
	if err != nil || string(data) != original {
		t.Fatal("invalid session was modified")
	}
	fmt.Printf("SESSION_FILE repaired=%t override=%t initialized=%t preserved=true\n", repaired, overridden, headerOnly)
}
