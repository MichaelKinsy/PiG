package runtime

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Ports the file cases of test/p2/server-name.test.ts (Pi main b22e525b3): server names, and identity.json that holds the
// owner's name only while there is one. The server's objects are e2e/server_name_test.go.

func TestServerNamesAreOneLineOfAtMost63Characters(t *testing.T) {
	for _, each := range []struct {
		name, want string
		ok         bool
	}{
		{"  Cristina's\u0000  MacBook\nPro ", "Cristina's MacBook Pro", true},
		{strings.Repeat("x", 80), strings.Repeat("x", 63), true},
		{" \t ", "", false},
	} {
		if got, ok := NormalizeServerName(each.name); got != each.want || ok != each.ok {
			t.Errorf("NormalizeServerName(%q) = %q, %v; want %q, %v", each.name, got, ok, each.want, each.ok)
		}
	}
	if device, ok := DeviceName(); ok {
		if got, kept := NormalizeServerName(device); !kept || got != device {
			t.Errorf("NormalizeServerName(%q) = %q, %v: the device's name is already a name", device, got, kept)
		}
	}
}

func TestIdentityJSONHoldsTheOwnersNameOnlyWhileThereIsOneABrokenFileSaysWhy(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "identity.json")
	exists := func() bool {
		_, err := os.Stat(path)
		return err == nil
	}
	if name, ok, err := ReadChosenServerName(directory); name != "" || ok || err != nil {
		t.Fatalf("no file: %q, %v, %v; want none", name, ok, err)
	}
	studio := "Studio"
	if err := WriteChosenServerName(directory, &studio); err != nil {
		t.Fatal(err)
	}
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal(text, &saved); err != nil || len(saved) != 1 || saved["name"] != "Studio" {
		t.Fatalf("identity.json = %s (%v), want {\"name\": \"Studio\"}", text, err)
	}
	if name, ok, err := ReadChosenServerName(directory); name != "Studio" || !ok || err != nil {
		t.Fatalf("after a rename: %q, %v, %v; want Studio", name, ok, err)
	}
	if err := WriteChosenServerName(directory, nil); err != nil {
		t.Fatal(err)
	}
	if exists() {
		t.Fatal("identity.json is still there after the name was removed")
	}
	if name, ok, err := ReadChosenServerName(directory); name != "" || ok || err != nil {
		t.Fatalf("after removing it: %q, %v, %v; want none", name, ok, err)
	}
	// Removing a name that is not there is no error.
	if err := WriteChosenServerName(directory, nil); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"{ not json", "null", "[]", `{ "name": "  " }`, `{ "name": 42 }`} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if name, ok, err := ReadChosenServerName(directory); err == nil || ok || name != "" {
			t.Errorf("%q: %q, %v, %v; want an error saying why", content, name, ok, err)
		}
	}
	// A name that is not in a file but a directory cannot be read: the error is the file system's, not "no name".
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadChosenServerName(directory); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Errorf("identity.json a directory: %v, want a read error", err)
	}
}
