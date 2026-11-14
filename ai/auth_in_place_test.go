//go:build !windows

package ai

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// A credential file with a second hard link is written in place, whatever replacing it would have saved: a rename would leave the other name with the old credentials, and the replacement is atomic only where it changes nothing but the inode. A login and a token refresh write the same bytes through the link as they write to a plain file.
func TestCredentialWritesGoInPlaceThroughAHardLink(t *testing.T) {
	scratch := t.TempDir()
	path := filepath.Join(scratch, "linked", "auth.json")
	control := filepath.Join(scratch, "control", "auth.json")
	seedCredentialFixture(t, path)
	seed, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(control), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(control, seed, 0o600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(scratch, "linked", "auth.json.second-name")
	if err := os.Link(path, other); err != nil {
		t.Skipf("hard links are not available here: %v", err)
	}
	inode := func(name string) uint64 {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		return info.Sys().(*syscall.Stat_t).Ino
	}
	before := inode(path)
	for _, target := range []string{path, control} {
		store, err := NewAuthStorage(target)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Set("anthropic", Credential{Type: CredentialOAuth, Access: "access-login", Refresh: "refresh-login", Expires: 4102444800000}); err != nil {
			t.Fatalf("login through %s: %v", target, err)
		}
		if _, err := store.Modify(context.Background(), "openai-codex", func(cur *Credential) (*Credential, error) {
			next := *cur
			next.Access, next.Refresh, next.Expires = "access-refreshed", "refresh-refreshed", 4102444800000
			return &next, nil
		}); err != nil {
			t.Fatalf("refresh through %s: %v", target, err)
		}
	}
	want, _ := os.ReadFile(control)
	if bytes.Equal(want, seed) {
		t.Fatal("the control write changed nothing")
	}
	for _, name := range []string{path, other} {
		if got, _ := os.ReadFile(name); !bytes.Equal(got, want) {
			t.Errorf("%s after a login and a refresh is %d bytes, want the %d bytes a plain file ends with:\n%s", name, len(got), len(want), got)
		}
	}
	if inode(path) != before || inode(other) != before {
		t.Error("the hard link was broken: the credential file was replaced, not written in place")
	}
	if got := credentialFixtureEntries(t, path); len(got) != len(credentialFixtureProviders) {
		t.Errorf("providers after the writes = %v, want %v", got, credentialFixtureProviders)
	}
}
