package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Ports the identity cases of test/p2/radius.test.ts (Pi main 5c37d3be1): brain names, and a data directory that cannot hold
// one.

func TestBrainNamesAreOneLineOfAtMost63CharactersElseTheDevicesName(t *testing.T) {
	for _, each := range []struct {
		name, want string
		ok         bool
	}{
		{"  Cristina's\u0000  MacBook\nPro ", "Cristina's MacBook Pro", true},
		{strings.Repeat("x", 80), strings.Repeat("x", 63), true},
		{" \t ", "", false},
	} {
		if got, ok := NormalizeBrainName(each.name); got != each.want || ok != each.ok {
			t.Errorf("NormalizeBrainName(%q) = %q, %v; want %q, %v", each.name, got, ok, each.want, each.ok)
		}
	}
	directory := t.TempDir()
	device, ok := DeviceName()
	if !ok {
		t.Skip("this machine has no device name")
	}
	for _, content := range []string{"{ not json", "null", "[]", `{ "name": "  " }`, `{ "name": 42 }`} {
		if err := os.WriteFile(filepath.Join(directory, "identity.json"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := ReadBrainIdentity(directory); got == nil || got.Name != device {
			t.Fatalf("ReadBrainIdentity after %q = %v, want %q", content, got, device)
		}
		text, err := os.ReadFile(filepath.Join(directory, "identity.json"))
		if err != nil {
			t.Fatal(err)
		}
		var saved BrainIdentity
		if err := json.Unmarshal(text, &saved); err != nil || saved.Name != device {
			t.Fatalf("identity.json after %q = %s (%v), want the device's name %q", content, text, err, device)
		}
	}
}

func TestABrainWhoseDataDirectoryCannotHoldItsNameStillGetsTheDevices(t *testing.T) {
	device, ok := DeviceName()
	if !ok {
		t.Skip("this machine has no device name")
	}
	// A read-only data directory (a locked-down sandbox): nothing can be saved, the name stands in.
	readOnly := t.TempDir()
	if err := os.Chmod(readOnly, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnly, 0o700) })
	if got := ReadBrainIdentity(readOnly); got == nil || got.Name != device {
		t.Fatalf("read-only directory: %v, want %q", got, device)
	}
	// identity.json that is not a file: left alone.
	odd := t.TempDir()
	if err := os.Mkdir(filepath.Join(odd, "identity.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := ReadBrainIdentity(odd); got == nil || got.Name != device {
		t.Fatalf("identity.json a directory: %v, want %q", got, device)
	}
	if info, err := os.Stat(filepath.Join(odd, "identity.json")); err != nil || !info.IsDir() {
		t.Fatalf("identity.json is no longer a directory: %v, %v", info, err)
	}
}
