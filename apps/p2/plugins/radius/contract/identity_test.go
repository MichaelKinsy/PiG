package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Ports apps/p2/test/p2/radius.test.ts (Pi 5c37d3be1): "Brain names are one line of at most 63 characters, else the device's name" and "A brain
// whose data directory cannot hold its name still gets the device's". The `normalizeBrainName(42)` line has no Go form: the parameter is a string; a
// non-string `name` in identity.json is covered by the stored-content cases below.
func TestBrainNamesAreOneLineOfAtMost63CharactersElseTheDevicesName(t *testing.T) {
	if got, _ := NormalizeBrainName("  Cristina's\u0000  MacBook\nPro "); got != "Cristina's MacBook Pro" {
		t.Fatalf("control characters and runs of space = %q", got)
	}
	if got, _ := NormalizeBrainName(strings.Repeat("x", 80)); got != strings.Repeat("x", 63) {
		t.Fatalf("long name = %q", got)
	}
	if got, ok := NormalizeBrainName(" \t "); ok || got != "" {
		t.Fatalf("blank = %q, %v", got, ok)
	}
	if got, _ := NormalizeBrainName(strings.Repeat("é", 70)); got != strings.Repeat("é", 63) {
		t.Fatalf("the limit counts characters, not bytes: %q", got)
	}
	directory := t.TempDir()
	device := BrainIdentity{Name: DeviceName()}
	for _, content := range []string{"{ not json", "null", "[]", `{ "name": "  " }`, `{ "name": 42 }`} {
		if err := os.WriteFile(filepath.Join(directory, "identity.json"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		identity, ok := ReadBrainIdentity(directory)
		if device.Name == "" {
			if ok {
				t.Fatalf("%q: no device name, yet %v", content, identity)
			}
			continue
		}
		if !ok || identity != device {
			t.Fatalf("%q: identity = %v, %v; want the device's %v", content, identity, ok, device)
		}
		var saved BrainIdentity
		raw, err := os.ReadFile(filepath.Join(directory, "identity.json"))
		if err != nil || json.Unmarshal(raw, &saved) != nil || saved != device {
			t.Fatalf("%q: the file is replaced with the device name: %s (%v)", content, raw, err)
		}
	}
}

func TestABrainWhoseDataDirectoryCannotHoldItsNameStillGetsTheDevicesName(t *testing.T) {
	device := BrainIdentity{Name: DeviceName()}
	want := func(identity BrainIdentity, ok bool) bool {
		return ok == (device.Name != "") && identity == device || device.Name == "" && !ok
	}
	// A read-only data directory (a locked-down sandbox): nothing can be saved, the name stands in.
	readOnly := t.TempDir()
	if err := os.Chmod(readOnly, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnly, 0o700) })
	if identity, ok := ReadBrainIdentity(readOnly); !want(identity, ok) {
		t.Fatalf("read-only directory: %v, %v", identity, ok)
	}
	// identity.json that is not a file: left alone.
	odd := t.TempDir()
	if err := os.Mkdir(filepath.Join(odd, "identity.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if identity, ok := ReadBrainIdentity(odd); !want(identity, ok) {
		t.Fatalf("directory as identity.json: %v, %v", identity, ok)
	}
	if info, err := os.Stat(filepath.Join(odd, "identity.json")); err != nil || !info.IsDir() {
		t.Fatalf("identity.json is left alone: %v, %v", info, err)
	}
}
